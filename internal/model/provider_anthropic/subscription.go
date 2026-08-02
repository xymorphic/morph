package provider_anthropic

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/xymorphic/morph/internal/constants"
	appcredential "github.com/xymorphic/morph/internal/credential"
	"github.com/xymorphic/morph/pkg/str"
)

const (
	anthropicSubscriptionClientID     = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	anthropicSubscriptionAuthorize    = "https://claude.ai/oauth/authorize"
	anthropicSubscriptionToken        = "https://platform.claude.com/v1/oauth/token"
	anthropicSubscriptionCallbackPath = "/callback"
	anthropicSubscriptionScope        = "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
	anthropicOAuthBeta                = "oauth-2025-04-20"
	anthropicClaudeCodeBeta           = "claude-code-20250219"
)

var (
	anthropicRandomReader = cryptorand.Reader
	runAnthropicOpenURL   = func(name string, args ...string) error {
		return exec.Command(name, args...).Start()
	}
)

// AnthropicSubscriptionProvider implements Claude subscription OAuth for Anthropic models.
type AnthropicSubscriptionProvider struct {
	AuthorizeURL string
	TokenURL     string
	RedirectURI  string
	ListenAddr   string
	HTTPClient   *http.Client
	OpenBrowser  func(string) error
	Now          func() time.Time
}

func init() {
	appcredential.RegisterSubscriptionProvider(constants.ModelProviderAnthropic, AnthropicSubscriptionProvider{})
}

// Login completes the browser OAuth flow and returns a stored OAuth credential.
func (p AnthropicSubscriptionProvider) Login(
	ctx context.Context,
	options appcredential.LoginOptions,
) (appcredential.StoredCredential, error) {
	p = p.withDefaults()

	codeVerifier, challenge, err := newAnthropicPKCE()
	if err != nil {
		return appcredential.StoredCredential{}, err
	}
	state := codeVerifier

	listener, redirectURI, err := p.listenForCallback()
	if err != nil {
		return appcredential.StoredCredential{}, err
	}
	defer func() { _ = listener.Close() }()

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	server := p.startCallbackServer(listener, state, codeCh, errCh)
	defer func() { _ = server.Close() }()

	authURL := p.getAuthorizeURL(redirectURI, state, challenge)
	if options.Output != nil {
		_, _ = fmt.Fprintf(options.Output, "Open this URL to authenticate Anthropic:\n%s\n", authURL)
	}
	if p.OpenBrowser != nil {
		_ = p.OpenBrowser(authURL)
	}

	select {
	case code := <-codeCh:
		return p.exchangeCode(ctx, code, state, codeVerifier, redirectURI)
	case err := <-errCh:
		return appcredential.StoredCredential{}, err
	case <-ctx.Done():
		return appcredential.StoredCredential{}, ctx.Err()
	}
}

// Refresh exchanges an expired access token for a fresh one.
func (p AnthropicSubscriptionProvider) Refresh(
	ctx context.Context,
	credential appcredential.StoredCredential,
) (appcredential.StoredCredential, error) {
	p = p.withDefaults()
	refreshValue := str.String(credential.Refresh)
	refreshToken := refreshValue.Trim()
	if refreshToken == "" {
		return appcredential.StoredCredential{}, errors.New("anthropic subscription refresh token is required")
	}

	body := map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     anthropicSubscriptionClientID,
		"refresh_token": refreshToken,
	}

	next, err := p.postToken(ctx, body)
	if err != nil {
		return appcredential.StoredCredential{}, err
	}
	if next.Refresh == "" {
		next.Refresh = refreshToken
	}

	return next, nil
}

// AuthHeaders converts an Anthropic subscription token into request headers.
func (p AnthropicSubscriptionProvider) AuthHeaders(
	_ context.Context,
	credential appcredential.StoredCredential,
) (map[string]string, error) {
	tokenValue := str.String(credential.Token)
	token := tokenValue.Trim()
	if token == "" {
		return nil, errors.New("anthropic subscription access token is required")
	}

	return map[string]string{
		"Authorization":  "Bearer " + token,
		"anthropic-beta": strings.Join([]string{anthropicClaudeCodeBeta, anthropicOAuthBeta}, ","),
		"anthropic-dangerous-direct-browser-access": "true",
		"user-agent": "claude-cli/morph",
		"x-app":      "cli",
	}, nil
}

func (p AnthropicSubscriptionProvider) withDefaults() AnthropicSubscriptionProvider {
	authorizeURLValue := str.String(p.AuthorizeURL)
	if authorizeURLValue.Trim() == "" {
		p.AuthorizeURL = anthropicSubscriptionAuthorize
	}
	tokenURLValue := str.String(p.TokenURL)
	if tokenURLValue.Trim() == "" {
		p.TokenURL = anthropicSubscriptionToken
	}
	listenAddrValue := str.String(p.ListenAddr)
	if listenAddrValue.Trim() == "" {
		p.ListenAddr = "127.0.0.1:0"
	}
	if p.HTTPClient == nil {
		p.HTTPClient = http.DefaultClient
	}
	if p.OpenBrowser == nil {
		p.OpenBrowser = openAnthropicURLInBrowser
	}
	if p.Now == nil {
		p.Now = time.Now
	}

	return p
}

func (p AnthropicSubscriptionProvider) listenForCallback() (net.Listener, string, error) {
	listener, err := net.Listen("tcp", p.ListenAddr)
	if err != nil {
		return nil, "", err
	}
	redirectURIValue := str.String(p.RedirectURI)
	if redirectURI := redirectURIValue.Trim(); redirectURI != "" {
		return listener, redirectURI, nil
	}

	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()
		return nil, "", errors.New("anthropic OAuth listener must be TCP")
	}

	return listener, fmt.Sprintf("http://localhost:%d%s", tcpAddr.Port, anthropicSubscriptionCallbackPath), nil
}

func (p AnthropicSubscriptionProvider) startCallbackServer(
	listener net.Listener,
	state string,
	codeCh chan<- string,
	errCh chan<- error,
) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc(anthropicSubscriptionCallbackPath, func(w http.ResponseWriter, r *http.Request) {
		getValue := str.String(r.URL.Query().Get("state"))
		if got := getValue.Trim(); got != state {
			http.Error(w, "invalid OAuth state", http.StatusBadRequest)
			errCh <- errors.New("anthropic OAuth state mismatch")
			return
		}
		getValue2 := str.String(r.URL.Query().Get("error"))
		if reason := getValue2.Trim(); reason != "" {
			http.Error(w, "OAuth error", http.StatusBadRequest)
			errCh <- fmt.Errorf("anthropic OAuth failed: %s", reason)
			return
		}
		getValue3 := str.String(r.URL.Query().Get("code"))
		code := getValue3.Trim()
		if code == "" {
			http.Error(w, "missing OAuth code", http.StatusBadRequest)
			errCh <- errors.New("anthropic OAuth code is required")
			return
		}

		_, _ = io.WriteString(w, "Anthropic authentication complete. You can return to Morph.\n")
		codeCh <- code
	})

	server := &http.Server{Handler: mux}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	return server
}

func (p AnthropicSubscriptionProvider) getAuthorizeURL(
	redirectURI string,
	state string,
	challenge string,
) string {
	values := url.Values{
		"code":                  {"true"},
		"client_id":             {anthropicSubscriptionClientID},
		"response_type":         {"code"},
		"redirect_uri":          {redirectURI},
		"scope":                 {anthropicSubscriptionScope},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}

	return strings.TrimRight(p.AuthorizeURL, "?") + "?" + values.Encode()
}

func (p AnthropicSubscriptionProvider) exchangeCode(
	ctx context.Context,
	code string,
	state string,
	codeVerifier string,
	redirectURI string,
) (appcredential.StoredCredential, error) {
	body := map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     anthropicSubscriptionClientID,
		"code":          code,
		"state":         state,
		"redirect_uri":  redirectURI,
		"code_verifier": codeVerifier,
	}

	return p.postToken(ctx, body)
}

func (p AnthropicSubscriptionProvider) postToken(
	ctx context.Context,
	body map[string]string,
) (appcredential.StoredCredential, error) {
	requestBody, err := json.Marshal(body)
	if err != nil {
		return appcredential.StoredCredential{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenURL, bytes.NewReader(requestBody))
	if err != nil {
		return appcredential.StoredCredential{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("anthropic-beta", anthropicOAuthBeta)

	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return appcredential.StoredCredential{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return appcredential.StoredCredential{}, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return appcredential.StoredCredential{}, fmt.Errorf("anthropic token request failed: %s", resp.Status)
	}

	var token anthropicTokenResponse
	if err := json.Unmarshal(respBody, &token); err != nil {
		return appcredential.StoredCredential{}, err
	}
	accessTokenValue := str.String(token.AccessToken)
	if accessTokenValue.Trim() == "" {
		return appcredential.StoredCredential{}, errors.New("anthropic token response did not include an access token")
	}
	accessTokenValue2 := str.String(token.AccessToken)
	refreshTokenValue := str.String(token.RefreshToken)
	credential := appcredential.StoredCredential{
		Type:    appcredential.TypeOAuth,
		Token:   accessTokenValue2.Trim(),
		Refresh: refreshTokenValue.Trim(),
		Scopes:  strings.Fields(token.Scope),
	}
	if token.ExpiresIn > 0 {
		expiresIn := time.Duration(token.ExpiresIn) * time.Second
		if expiresIn > 5*time.Minute {
			expiresIn -= 5 * time.Minute
		}
		expiresAt := p.Now().Add(expiresIn)
		credential.ExpiresAt = &expiresAt
	}

	return credential, nil
}

type anthropicTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

func newAnthropicPKCE() (string, string, error) {
	verifier, err := randomAnthropicString(64)
	if err != nil {
		return "", "", err
	}

	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

func randomAnthropicString(length int) (string, error) {
	buf := make([]byte, length)
	if _, err := io.ReadFull(anthropicRandomReader, buf); err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func openAnthropicURLInBrowser(rawURL string) error {
	name, args := getAnthropicOpenURLCommand(runtime.GOOS, rawURL)
	return runAnthropicOpenURL(name, args...)
}

func getAnthropicOpenURLCommand(goos string, rawURL string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{rawURL}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHand", rawURL}
	default:
		return "xdg-open", []string{rawURL}
	}
}
