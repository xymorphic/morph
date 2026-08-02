package provider_copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	gitHubCopilotClientID     = "Iv1.b507a08c87ecfe98"
	gitHubCopilotDeviceCode   = "https://github.com/login/device/code"
	gitHubCopilotAccessToken  = "https://github.com/login/oauth/access_token"
	gitHubCopilotToken        = "https://api.github.com/copilot_internal/v2/token"
	gitHubCopilotScope        = "read:user"
	gitHubCopilotUserAgent    = "GitHubCopilotChat/0.35.0"
	gitHubCopilotEditor       = "vscode/1.107.0"
	gitHubCopilotEditorPlugin = "copilot-chat/0.35.0"
	gitHubCopilotIntegration  = "vscode-chat"
)

var runGitHubCopilotOpenURL = func(name string, args ...string) error {
	return exec.Command(name, args...).Start()
}

type deviceTokenPollStatus string

const (
	deviceTokenPollComplete deviceTokenPollStatus = "complete"
	deviceTokenPollPending  deviceTokenPollStatus = "pending"
	deviceTokenPollSlowDown deviceTokenPollStatus = "slow_down"
)

type GitHubCopilotSubscriptionProvider struct {
	DeviceCodeURL      string
	AccessTokenURL     string
	CopilotTokenURL    string
	ModelPolicyBaseURL string
	HTTPClient         *http.Client
	OpenBrowser        func(string) error
	Now                func() time.Time
}

func init() {
	appcredential.RegisterSubscriptionProvider(
		constants.ModelProviderGitHubCopilot,
		GitHubCopilotSubscriptionProvider{},
	)
}

func (p GitHubCopilotSubscriptionProvider) Login(
	ctx context.Context,
	options appcredential.LoginOptions,
) (appcredential.StoredCredential, error) {
	p = p.withDefaults()

	device, err := p.startDeviceFlow(ctx)
	if err != nil {
		return appcredential.StoredCredential{}, err
	}
	if options.Output != nil {
		_, _ = fmt.Fprintf(
			options.Output,
			"Open this URL to authenticate GitHub Copilot:\n%s\nEnter code: %s\n",
			device.VerificationURI,
			device.UserCode,
		)
	}
	if p.OpenBrowser != nil {
		_ = p.OpenBrowser(device.VerificationURI)
	}

	githubToken, err := p.pollAccessToken(ctx, device)
	if err != nil {
		return appcredential.StoredCredential{}, err
	}

	credential, err := p.fetchCopilotToken(ctx, githubToken)
	if err != nil {
		return appcredential.StoredCredential{}, err
	}
	_ = p.enableKnownModels(ctx, credential.Token)

	return credential, nil
}

func (p GitHubCopilotSubscriptionProvider) Refresh(
	ctx context.Context,
	credential appcredential.StoredCredential,
) (appcredential.StoredCredential, error) {
	p = p.withDefaults()
	refreshValue := str.String(credential.Refresh)
	refreshToken := refreshValue.Trim()
	if refreshToken == "" {
		return appcredential.StoredCredential{}, errors.New("GitHub Copilot refresh token is required")
	}

	return p.fetchCopilotToken(ctx, refreshToken)
}

func (p GitHubCopilotSubscriptionProvider) AuthHeaders(
	_ context.Context,
	credential appcredential.StoredCredential,
) (map[string]string, error) {
	tokenValue := str.String(credential.Token)
	token := tokenValue.Trim()
	if token == "" {
		return nil, errors.New("GitHub Copilot access token is required")
	}

	headers := gitHubCopilotHeaders()
	headers["Authorization"] = "Bearer " + token
	headers["X-Initiator"] = "user"
	headers["Openai-Intent"] = "conversation-edits"
	return headers, nil
}

func (p GitHubCopilotSubscriptionProvider) withDefaults() GitHubCopilotSubscriptionProvider {
	deviceCodeURLValue := str.String(p.DeviceCodeURL)
	if deviceCodeURLValue.Trim() == "" {
		p.DeviceCodeURL = gitHubCopilotDeviceCode
	}
	accessTokenURLValue := str.String(p.AccessTokenURL)
	if accessTokenURLValue.Trim() == "" {
		p.AccessTokenURL = gitHubCopilotAccessToken
	}
	copilotTokenURLValue := str.String(p.CopilotTokenURL)
	if copilotTokenURLValue.Trim() == "" {
		p.CopilotTokenURL = gitHubCopilotToken
	}
	if p.HTTPClient == nil {
		p.HTTPClient = http.DefaultClient
	}
	if p.OpenBrowser == nil {
		p.OpenBrowser = openGitHubCopilotURLInBrowser
	}
	if p.Now == nil {
		p.Now = time.Now
	}

	return p
}

func (p GitHubCopilotSubscriptionProvider) startDeviceFlow(ctx context.Context) (deviceCodeResponse, error) {
	form := url.Values{
		"client_id": {gitHubCopilotClientID},
		"scope":     {gitHubCopilotScope},
	}
	var device deviceCodeResponse
	if err := p.postForm(ctx, p.DeviceCodeURL, "", form, &device); err != nil {
		return deviceCodeResponse{}, err
	}
	deviceCodeValue := str.String(device.DeviceCode)
	userCodeValue := str.String(device.UserCode)
	verificationURIValue := str.String(device.VerificationURI)
	if deviceCodeValue.Trim() == "" || userCodeValue.
		Trim() == "" || verificationURIValue.
		Trim() == "" ||
		device.ExpiresIn <= 0 {
		return deviceCodeResponse{}, errors.New("invalid GitHub Copilot device code response")
	}

	return device, nil
}

func (p GitHubCopilotSubscriptionProvider) pollAccessToken(
	ctx context.Context,
	device deviceCodeResponse,
) (string, error) {
	interval := time.Duration(device.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	expires := p.Now().Add(time.Duration(device.ExpiresIn) * time.Second)

	for {
		token, status, err := p.pollAccessTokenOnce(ctx, device.DeviceCode)
		if status == deviceTokenPollComplete || err != nil {
			return token, err
		}
		if status == deviceTokenPollSlowDown {
			interval += 5 * time.Second
		}

		timer := time.NewTimer(interval)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		}
		if !p.Now().Before(expires) {
			return "", errors.New("GitHub Copilot device code expired")
		}
	}
}

func (p GitHubCopilotSubscriptionProvider) pollAccessTokenOnce(
	ctx context.Context,
	deviceCode string,
) (string, deviceTokenPollStatus, error) {
	form := url.Values{
		"client_id":   {gitHubCopilotClientID},
		"device_code": {deviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	}
	var response deviceTokenResponse
	if err := p.postForm(ctx, p.AccessTokenURL, "", form, &response); err != nil {
		return "", "", err
	}
	accessTokenValue := str.String(response.AccessToken)
	if token := accessTokenValue.Trim(); token != "" {
		return token, deviceTokenPollComplete, nil
	}
	errorValue := str.String(response.Error)
	switch errorValue.Trim() {
	case "authorization_pending":
		return "", deviceTokenPollPending, nil
	case "slow_down":
		return "", deviceTokenPollSlowDown, nil
	case "":
		return "", deviceTokenPollComplete, errors.New("invalid GitHub Copilot device token response")
	default:
		message := "GitHub Copilot device flow failed: " + response.Error
		errorDescriptionValue := str.String(response.ErrorDescription)
		if description := errorDescriptionValue.Trim(); description != "" {
			message += ": " + description
		}
		return "", deviceTokenPollComplete, errors.New(message)
	}
}

func (p GitHubCopilotSubscriptionProvider) fetchCopilotToken(
	ctx context.Context,
	githubToken string,
) (appcredential.StoredCredential, error) {
	var token copilotTokenResponse
	if err := p.getJSON(ctx, p.CopilotTokenURL, githubToken, &token); err != nil {
		return appcredential.StoredCredential{}, err
	}
	tokenValue2 := str.String(token.Token)
	copilotToken := tokenValue2.Trim()
	if copilotToken == "" || token.ExpiresAt <= 0 {
		return appcredential.StoredCredential{}, errors.New("invalid GitHub Copilot token response")
	}

	expiresAt := time.Unix(token.ExpiresAt, 0).Add(-5 * time.Minute)
	githubTokenValue := str.String(githubToken)
	return appcredential.StoredCredential{
		Type:      appcredential.TypeOAuth,
		Token:     copilotToken,
		Refresh:   githubTokenValue.Trim(),
		ExpiresAt: &expiresAt,
		Scopes:    []string{gitHubCopilotScope},
	}, nil
}

func (p GitHubCopilotSubscriptionProvider) enableKnownModels(ctx context.Context, token string) error {
	modelPolicyBaseURLValue := str.String(p.ModelPolicyBaseURL)
	baseURL := strings.TrimRight(modelPolicyBaseURLValue.Trim(), "/")
	if baseURL == "" {
		baseURL = getGitHubCopilotBaseURLFromToken(token)
	}
	if baseURL == "" {
		return nil
	}

	for _, modelID := range gitHubCopilotPolicyModelIDs() {
		if err := p.enableModel(ctx, baseURL, token, modelID); err != nil {
			return err
		}
	}

	return nil
}

func (p GitHubCopilotSubscriptionProvider) enableModel(
	ctx context.Context,
	baseURL string,
	token string,
	modelID string,
) error {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		baseURL+"/models/"+url.PathEscape(modelID)+"/policy",
		strings.NewReader(`{"state":"enabled"}`),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Openai-Intent", "chat-policy")
	req.Header.Set("x-interaction-type", "chat-policy")
	setGitHubCopilotRequestHeaders(req, token)

	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("GitHub Copilot model policy request failed: %s", resp.Status)
	}

	return nil
}

func getGitHubCopilotBaseURLFromToken(token string) string {
	for _, part := range strings.Split(token, ";") {
		partValue := str.String(part)
		key, value, ok := strings.Cut(partValue.Trim(), "=")
		if !ok || key != "proxy-ep" {
			continue
		}
		valueText := str.String(value)
		host := valueText.Trim()
		if host == "" {
			return ""
		}

		if strings.HasPrefix(host, "proxy.") {
			host = "api." + strings.TrimPrefix(host, "proxy.")
		}

		return "https://" + host
	}

	return ""
}

func gitHubCopilotPolicyModelIDs() []string {
	return []string{
		"claude-haiku-4.5",
		"claude-opus-4.5",
		"claude-opus-4.6",
		"claude-opus-4.7",
		"claude-sonnet-4.5",
		"claude-sonnet-4.6",
		"claude-haiku-4-5",
		"claude-opus-4-5",
		"claude-opus-4-6",
		"claude-opus-4-7",
		"claude-sonnet-4-5",
		"claude-sonnet-4-6",
		"gemini-2.5-pro",
		"gemini-3-flash-preview",
		"gemini-3.1-pro-preview",
		"gemini-3.5-flash",
		"gpt-4.1",
		"gpt-4o",
		"gpt-5-mini",
		"gpt-5.2",
		"gpt-5.2-codex",
		"gpt-5.3-codex",
		"gpt-5.4",
		"gpt-5.4-mini",
		"gpt-5.5",
		"grok-code-fast-1",
	}
}

func (p GitHubCopilotSubscriptionProvider) postForm(
	ctx context.Context,
	rawURL string,
	bearerToken string,
	form url.Values,
	target any,
) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	setGitHubCopilotRequestHeaders(req, bearerToken)

	return p.doJSON(req, target)
}

func (p GitHubCopilotSubscriptionProvider) getJSON(
	ctx context.Context,
	rawURL string,
	bearerToken string,
	target any,
) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	setGitHubCopilotRequestHeaders(req, bearerToken)

	return p.doJSON(req, target)
}

func (p GitHubCopilotSubscriptionProvider) doJSON(req *http.Request, target any) error {
	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("GitHub Copilot token request failed: %s", resp.Status)
	}
	if err := json.Unmarshal(body, target); err != nil {
		return err
	}

	return nil
}

func setGitHubCopilotRequestHeaders(req *http.Request, bearerToken string) {
	for key, value := range gitHubCopilotHeaders() {
		req.Header.Set(key, value)
	}
	bearerTokenValue := str.String(bearerToken)
	if token := bearerTokenValue.Trim(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

func gitHubCopilotHeaders() map[string]string {
	return map[string]string{
		"User-Agent":             gitHubCopilotUserAgent,
		"Editor-Version":         gitHubCopilotEditor,
		"Editor-Plugin-Version":  gitHubCopilotEditorPlugin,
		"Copilot-Integration-Id": gitHubCopilotIntegration,
	}
}

type deviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	Interval        int64  `json:"interval"`
	ExpiresIn       int64  `json:"expires_in"`
}

type deviceTokenResponse struct {
	AccessToken      string `json:"access_token"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

type copilotTokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
}

func openGitHubCopilotURLInBrowser(rawURL string) error {
	name, args := getGitHubCopilotOpenURLCommand(runtime.GOOS, rawURL)
	return runGitHubCopilotOpenURL(name, args...)
}

func getGitHubCopilotOpenURLCommand(goos string, rawURL string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{rawURL}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHand", rawURL}
	default:
		return "xdg-open", []string{rawURL}
	}
}
