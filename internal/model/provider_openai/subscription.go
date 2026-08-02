package provider_openai

import (
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
	openAISubscriptionClientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	openAISubscriptionAuthorize    = "https://auth.openai.com/oauth/authorize"
	openAISubscriptionToken        = "https://auth.openai.com/oauth/token"
	openAISubscriptionCallbackPort = 1455
	openAISubscriptionFallbackPort = 1457
	openAISubscriptionCallbackPath = "/auth/callback"
	openAISubscriptionScope        = "openid profile email offline_access api.connectors.read api.connectors.invoke"
	openAISubscriptionOriginator   = "morph"
)

var (
	openAIRandomReader = cryptorand.Reader
	runOpenURLCommand  = func(name string, args ...string) error {
		return exec.Command(name, args...).Start()
	}
)

// OpenAISubscriptionProvider implements ChatGPT subscription OAuth for OpenAI models.
type OpenAISubscriptionProvider struct {
	AuthorizeURL string
	TokenURL     string
	RedirectURI  string
	ListenAddr   string
	HTTPClient   *http.Client
	OpenBrowser  func(string) error
	Now          func() time.Time
}

func init() {
	appcredential.RegisterSubscriptionProvider(constants.ModelProviderOpenAI, OpenAISubscriptionProvider{})
	appcredential.RegisterSubscriptionProvider(constants.ModelProviderOpenAICodex, OpenAISubscriptionProvider{})
}

// Login completes the browser OAuth flow and returns a stored OAuth credential.
func (p OpenAISubscriptionProvider) Login(
	ctx context.Context,
	options appcredential.LoginOptions,
) (appcredential.StoredCredential, error) {
	p = p.withDefaults()

	codeVerifier, challenge, err := newOpenAIPKCE()
	if err != nil {
		return appcredential.StoredCredential{}, err
	}
	state, err := randomOpenAIString(32)
	if err != nil {
		return appcredential.StoredCredential{}, err
	}

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
		_, _ = fmt.Fprintf(options.Output, "Open this URL to authenticate OpenAI:\n%s\n", authURL)
	}
	if p.OpenBrowser != nil {
		_ = p.OpenBrowser(authURL)
	}

	select {
	case code := <-codeCh:
		return p.exchangeCode(ctx, code, codeVerifier, redirectURI)
	case err := <-errCh:
		return appcredential.StoredCredential{}, err
	case <-ctx.Done():
		return appcredential.StoredCredential{}, ctx.Err()
	}
}

// Refresh exchanges an expired access token for a fresh one.
func (p OpenAISubscriptionProvider) Refresh(
	ctx context.Context,
	credential appcredential.StoredCredential,
) (appcredential.StoredCredential, error) {
	p = p.withDefaults()
	refreshValue := str.String(credential.Refresh)
	refreshToken := refreshValue.Trim()
	if refreshToken == "" {
		return appcredential.StoredCredential{}, errors.New("OpenAI subscription refresh token is required")
	}

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {openAISubscriptionClientID},
	}

	next, err := p.postToken(ctx, form)
	if err != nil {
		return appcredential.StoredCredential{}, err
	}
	if next.Refresh == "" {
		next.Refresh = refreshToken
	}

	return next, nil
}

// AuthHeaders converts an OpenAI subscription token into request headers.
func (p OpenAISubscriptionProvider) AuthHeaders(
	_ context.Context,
	credential appcredential.StoredCredential,
) (map[string]string, error) {
	tokenValue := str.String(credential.Token)
	token := tokenValue.Trim()
	if token == "" {
		return nil, errors.New("OpenAI subscription access token is required")
	}

	accountID, err := getOpenAIAccountID(token)
	if err != nil {
		return nil, err
	}

	return map[string]string{
		"Authorization":      "Bearer " + token,
		"ChatGPT-Account-ID": accountID,
		"OpenAI-Beta":        "responses=experimental",
		"Originator":         openAISubscriptionOriginator,
		"User-Agent":         "Morph",
	}, nil
}

func (p OpenAISubscriptionProvider) withDefaults() OpenAISubscriptionProvider {
	authorizeURLValue := str.String(p.AuthorizeURL)
	if authorizeURLValue.Trim() == "" {
		p.AuthorizeURL = openAISubscriptionAuthorize
	}
	tokenURLValue := str.String(p.TokenURL)
	if tokenURLValue.Trim() == "" {
		p.TokenURL = openAISubscriptionToken
	}
	listenAddrValue := str.String(p.ListenAddr)
	if listenAddrValue.Trim() == "" {
		p.ListenAddr = fmt.Sprintf("127.0.0.1:%d", openAISubscriptionCallbackPort)
	}
	if p.HTTPClient == nil {
		p.HTTPClient = http.DefaultClient
	}
	if p.OpenBrowser == nil {
		p.OpenBrowser = openURLInBrowser
	}
	if p.Now == nil {
		p.Now = time.Now
	}

	return p
}

func (p OpenAISubscriptionProvider) listenForCallback() (net.Listener, string, error) {
	listener, err := net.Listen("tcp", p.ListenAddr)
	if err != nil {
		if p.ListenAddr != fmt.Sprintf("127.0.0.1:%d", openAISubscriptionCallbackPort) {
			return nil, "", err
		}

		p.ListenAddr = fmt.Sprintf("127.0.0.1:%d", openAISubscriptionFallbackPort)
		listener, err = net.Listen("tcp", p.ListenAddr)
		if err != nil {
			return nil, "", err
		}
	}
	redirectURIValue := str.String(p.RedirectURI)
	if redirectURI := redirectURIValue.Trim(); redirectURI != "" {
		return listener, redirectURI, nil
	}

	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()
		return nil, "", errors.New("OpenAI OAuth listener must be TCP")
	}

	return listener, fmt.Sprintf("http://localhost:%d%s", tcpAddr.Port, openAISubscriptionCallbackPath), nil
}

func (p OpenAISubscriptionProvider) startCallbackServer(
	listener net.Listener,
	state string,
	codeCh chan<- string,
	errCh chan<- error,
) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc(openAISubscriptionCallbackPath, func(w http.ResponseWriter, r *http.Request) {
		getValue := str.String(r.URL.Query().Get("state"))
		if got := getValue.Trim(); got != state {
			http.Error(w, "invalid OAuth state", http.StatusBadRequest)
			errCh <- errors.New("OpenAI OAuth state mismatch")
			return
		}
		getValue2 := str.String(r.URL.Query().Get("code"))
		code := getValue2.Trim()
		if code == "" {
			http.Error(w, "missing OAuth code", http.StatusBadRequest)
			errCh <- errors.New("OpenAI OAuth code is required")
			return
		}

		_, _ = io.WriteString(w, "OpenAI authentication complete. You can return to Morph.\n")
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

func (p OpenAISubscriptionProvider) getAuthorizeURL(
	redirectURI string,
	state string,
	challenge string,
) string {
	values := url.Values{
		"response_type":              {"code"},
		"client_id":                  {openAISubscriptionClientID},
		"redirect_uri":               {redirectURI},
		"scope":                      {openAISubscriptionScope},
		"state":                      {state},
		"code_challenge":             {challenge},
		"code_challenge_method":      {"S256"},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
		"originator":                 {openAISubscriptionOriginator},
	}

	return strings.TrimRight(p.AuthorizeURL, "?") + "?" + values.Encode()
}

func (p OpenAISubscriptionProvider) exchangeCode(
	ctx context.Context,
	code string,
	codeVerifier string,
	redirectURI string,
) (appcredential.StoredCredential, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {openAISubscriptionClientID},
		"code":          {code},
		"code_verifier": {codeVerifier},
		"redirect_uri":  {redirectURI},
	}

	return p.postToken(ctx, form)
}

func (p OpenAISubscriptionProvider) postToken(
	ctx context.Context,
	form url.Values,
) (appcredential.StoredCredential, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return appcredential.StoredCredential{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return appcredential.StoredCredential{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return appcredential.StoredCredential{}, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return appcredential.StoredCredential{}, fmt.Errorf("OpenAI token request failed: %s", resp.Status)
	}

	var token tokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return appcredential.StoredCredential{}, err
	}
	accessTokenValue := str.String(token.AccessToken)
	if accessTokenValue.Trim() == "" {
		return appcredential.StoredCredential{}, errors.New("OpenAI token response did not include an access token")
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
		expiresAt := p.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
		credential.ExpiresAt = &expiresAt
	}

	return credential, nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

func newOpenAIPKCE() (string, string, error) {
	verifier, err := randomOpenAIString(64)
	if err != nil {
		return "", "", err
	}

	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

func randomOpenAIString(length int) (string, error) {
	buf := make([]byte, length)
	if _, err := io.ReadFull(openAIRandomReader, buf); err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func getOpenAIAccountID(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return "", errors.New("OpenAI subscription token must be a JWT with account metadata")
	}

	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode OpenAI subscription token: %w", err)
	}

	var claims map[string]any
	if err := json.Unmarshal(body, &claims); err != nil {
		return "", err
	}

	authClaims, ok := claims["https://api.openai.com/auth"].(map[string]any)
	if !ok {
		return "", errors.New("OpenAI subscription token is missing account metadata")
	}
	accountID, ok := authClaims["chatgpt_account_id"].(string)
	accountIDValue := str.String(accountID)
	if !ok || accountIDValue.Trim() == "" {
		return "", errors.New("OpenAI subscription token is missing account ID")
	}
	accountIDValue2 := str.String(accountID)
	return accountIDValue2.Trim(), nil
}

func openURLInBrowser(rawURL string) error {
	name, args := getOpenURLCommand(runtime.GOOS, rawURL)
	return runOpenURLCommand(name, args...)
}

func getOpenURLCommand(goos string, rawURL string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{rawURL}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHand", rawURL}
	default:
		return "xdg-open", []string{rawURL}
	}
}
