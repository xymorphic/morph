package provider_copilot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	appcredential "github.com/xymorphic/morph/internal/credential"
)

func TestMain(m *testing.M) {
	previous := runGitHubCopilotOpenURL
	runGitHubCopilotOpenURL = func(string, ...string) error {
		return errors.New("real GitHub Copilot browser opener disabled in tests")
	}
	code := m.Run()
	runGitHubCopilotOpenURL = previous
	os.Exit(code)
}

func TestGitHubCopilotSubscriptionProvider_AuthHeadersUsesBearerToken(t *testing.T) {
	headers, err := GitHubCopilotSubscriptionProvider{}.AuthHeaders(
		context.Background(),
		appcredential.StoredCredential{Type: appcredential.TypeOAuth, Token: "copilot-token"},
	)

	require.NoError(t, err)
	require.Equal(t, "Bearer copilot-token", headers["Authorization"])
	require.Equal(t, gitHubCopilotUserAgent, headers["User-Agent"])
	require.Equal(t, gitHubCopilotEditor, headers["Editor-Version"])
	require.Equal(t, gitHubCopilotEditorPlugin, headers["Editor-Plugin-Version"])
	require.Equal(t, gitHubCopilotIntegration, headers["Copilot-Integration-Id"])
	require.Equal(t, "user", headers["X-Initiator"])
	require.Equal(t, "conversation-edits", headers["Openai-Intent"])
}

func TestGitHubCopilotSubscriptionProvider_AuthHeadersValidatesToken(t *testing.T) {
	_, err := GitHubCopilotSubscriptionProvider{}.AuthHeaders(context.Background(), appcredential.StoredCredential{})

	require.ErrorContains(t, err, "access token is required")
}

func TestGitHubCopilotSubscriptionProvider_RefreshFetchesCopilotToken(t *testing.T) {
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		authorization = r.Header.Get("Authorization")
		require.Equal(t, gitHubCopilotUserAgent, r.Header.Get("User-Agent"))
		_, _ = io.WriteString(w, `{"token":"copilot-new","expires_at":1779912000}`)
	}))
	defer server.Close()

	provider := GitHubCopilotSubscriptionProvider{CopilotTokenURL: server.URL}

	credential, err := provider.Refresh(context.Background(), appcredential.StoredCredential{
		Type:    appcredential.TypeOAuth,
		Token:   "copilot-old",
		Refresh: "github-access",
	})

	require.NoError(t, err)
	require.Equal(t, "Bearer github-access", authorization)
	require.Equal(t, appcredential.TypeOAuth, credential.Type)
	require.Equal(t, "copilot-new", credential.Token)
	require.Equal(t, "github-access", credential.Refresh)
	require.Equal(t, []string{gitHubCopilotScope}, credential.Scopes)
	require.NotNil(t, credential.ExpiresAt)
	require.True(t, time.Unix(1779912000, 0).Add(-5*time.Minute).Equal(*credential.ExpiresAt))
}

func TestGitHubCopilotSubscriptionProvider_RefreshValidatesAndPropagatesErrors(t *testing.T) {
	_, err := GitHubCopilotSubscriptionProvider{}.Refresh(context.Background(), appcredential.StoredCredential{})
	require.ErrorContains(t, err, "refresh token is required")

	for name, tc := range map[string]struct {
		body   string
		status int
		want   string
	}{
		"non_success": {
			body:   `{"message":"bad"}`,
			status: http.StatusUnauthorized,
			want:   "GitHub Copilot token request failed: 401 Unauthorized",
		},
		"invalid_json": {
			body:   `{`,
			status: http.StatusOK,
			want:   "unexpected end of JSON input",
		},
		"missing_token": {
			body:   `{"expires_at":1779912000}`,
			status: http.StatusOK,
			want:   "invalid GitHub Copilot token response",
		},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()

			provider := GitHubCopilotSubscriptionProvider{CopilotTokenURL: server.URL}
			_, err := provider.Refresh(context.Background(), appcredential.StoredCredential{Refresh: "github-access"})
			require.ErrorContains(t, err, tc.want)
		})
	}

	provider := GitHubCopilotSubscriptionProvider{CopilotTokenURL: "://bad-url"}
	_, err = provider.Refresh(context.Background(), appcredential.StoredCredential{Refresh: "github-access"})
	require.Error(t, err)

	provider = GitHubCopilotSubscriptionProvider{
		CopilotTokenURL: "https://copilot.test",
		HTTPClient: &http.Client{Transport: copilotRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("network down")
		})},
	}
	_, err = provider.Refresh(context.Background(), appcredential.StoredCredential{Refresh: "github-access"})
	require.ErrorContains(t, err, "network down")
}

func TestGitHubCopilotSubscriptionProvider_LoginCompletesDeviceFlow(t *testing.T) {
	var deviceForm url.Values
	var tokenForm url.Values
	var copilotAuthorization string
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)

	mux := http.NewServeMux()
	mux.HandleFunc("/login/device/code", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.NoError(t, r.ParseForm())
		deviceForm = r.Form
		_, _ = io.WriteString(w, `{
			"device_code":"device-code",
			"user_code":"ABCD-EFGH",
			"verification_uri":"https://github.com/login/device",
			"interval":1,
			"expires_in":900
		}`)
	})
	mux.HandleFunc("/login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.NoError(t, r.ParseForm())
		tokenForm = r.Form
		_, _ = io.WriteString(w, `{"access_token":"github-access"}`)
	})
	mux.HandleFunc("/copilot_internal/v2/token", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		copilotAuthorization = r.Header.Get("Authorization")
		require.Equal(t, gitHubCopilotIntegration, r.Header.Get("Copilot-Integration-Id"))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "copilot-access",
			"expires_at": now.Add(time.Hour).Unix(),
		})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	var opened string
	provider := GitHubCopilotSubscriptionProvider{
		DeviceCodeURL:   server.URL + "/login/device/code",
		AccessTokenURL:  server.URL + "/login/oauth/access_token",
		CopilotTokenURL: server.URL + "/copilot_internal/v2/token",
		OpenBrowser: func(rawURL string) error {
			opened = rawURL
			return nil
		},
		Now: func() time.Time { return now },
	}

	var output strings.Builder
	credential, err := provider.Login(context.Background(), appcredential.LoginOptions{Output: &output})

	require.NoError(t, err)
	require.Contains(t, output.String(), "https://github.com/login/device")
	require.Contains(t, output.String(), "ABCD-EFGH")
	require.Equal(t, gitHubCopilotClientID, deviceForm.Get("client_id"))
	require.Equal(t, gitHubCopilotScope, deviceForm.Get("scope"))
	require.Equal(t, gitHubCopilotClientID, tokenForm.Get("client_id"))
	require.Equal(t, "device-code", tokenForm.Get("device_code"))
	require.Equal(t, "urn:ietf:params:oauth:grant-type:device_code", tokenForm.Get("grant_type"))
	require.Equal(t, "Bearer github-access", copilotAuthorization)
	require.Equal(t, "https://github.com/login/device", opened)
	require.Equal(t, appcredential.TypeOAuth, credential.Type)
	require.Equal(t, "copilot-access", credential.Token)
	require.Equal(t, "github-access", credential.Refresh)
	require.NotNil(t, credential.ExpiresAt)
	require.True(t, now.Add(55*time.Minute).Equal(*credential.ExpiresAt))
}

func TestGitHubCopilotSubscriptionProvider_EnableKnownModelsPostsPolicyRequests(t *testing.T) {
	var count int
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Contains(t, r.URL.Path, "/models/")
		require.Equal(t, "chat-policy", r.Header.Get("Openai-Intent"))
		require.Equal(t, "chat-policy", r.Header.Get("x-interaction-type"))
		authorization = r.Header.Get("Authorization")
		count++
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()

	provider := GitHubCopilotSubscriptionProvider{ModelPolicyBaseURL: server.URL}.withDefaults()

	err := provider.enableKnownModels(context.Background(), "copilot-token")

	require.NoError(t, err)
	require.Equal(t, "Bearer copilot-token", authorization)
	require.Equal(t, len(gitHubCopilotPolicyModelIDs()), count)
}

func TestGitHubCopilotSubscriptionProvider_PollAccessTokenHandlesPendingUntilContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"error":"authorization_pending"}`)
		cancel()
	}))
	defer server.Close()

	provider := GitHubCopilotSubscriptionProvider{AccessTokenURL: server.URL}.withDefaults()

	_, err := provider.pollAccessToken(ctx, deviceCodeResponse{
		DeviceCode: "device-code",
		Interval:   1,
		ExpiresIn:  900,
	})

	require.ErrorIs(t, err, context.Canceled)
}

func TestGitHubCopilotSubscriptionProvider_PollAccessTokenValidatesTokenResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()

	provider := GitHubCopilotSubscriptionProvider{AccessTokenURL: server.URL}.withDefaults()

	_, status, err := provider.pollAccessTokenOnce(context.Background(), "device-code")

	require.ErrorContains(t, err, "invalid GitHub Copilot device token response")
	require.Equal(t, deviceTokenPollComplete, status)
}

func TestGitHubCopilotSubscriptionProvider_PollAccessTokenRecognizesSlowDown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"error":"slow_down"}`)
	}))
	defer server.Close()

	provider := GitHubCopilotSubscriptionProvider{AccessTokenURL: server.URL}.withDefaults()

	_, status, err := provider.pollAccessTokenOnce(context.Background(), "device-code")

	require.NoError(t, err)
	require.Equal(t, deviceTokenPollSlowDown, status)
}

func TestGetGitHubCopilotBaseURLFromToken(t *testing.T) {
	require.Equal(
		t,
		"https://api.individual.githubcopilot.com",
		getGitHubCopilotBaseURLFromToken("tid=1;proxy-ep=proxy.individual.githubcopilot.com;exp=2"),
	)
	require.Empty(t, getGitHubCopilotBaseURLFromToken("tid=1;exp=2"))
}

func TestGetGitHubCopilotOpenURLCommand(t *testing.T) {
	name, args := getGitHubCopilotOpenURLCommand("darwin", "https://example.test")
	require.Equal(t, "open", name)
	require.Equal(t, []string{"https://example.test"}, args)

	name, args = getGitHubCopilotOpenURLCommand("windows", "https://example.test")
	require.Equal(t, "rundll32", name)
	require.Equal(t, []string{"url.dll,FileProtocolHand", "https://example.test"}, args)

	name, args = getGitHubCopilotOpenURLCommand("linux", "https://example.test")
	require.Equal(t, "xdg-open", name)
	require.Equal(t, []string{"https://example.test"}, args)
}

func TestOpenGitHubCopilotURLInBrowserRunsPlatformCommand(t *testing.T) {
	original := runGitHubCopilotOpenURL
	t.Cleanup(func() { runGitHubCopilotOpenURL = original })

	var capturedName string
	var capturedArgs []string
	runGitHubCopilotOpenURL = func(name string, args ...string) error {
		capturedName = name
		capturedArgs = args
		return nil
	}

	err := openGitHubCopilotURLInBrowser("https://example.test")

	require.NoError(t, err)
	require.NotEmpty(t, capturedName)
	require.Contains(t, capturedArgs, "https://example.test")
}

func TestGitHubCopilotSubscriptionProvider_LoginHandlesDeviceFlowErrors(t *testing.T) {
	t.Run("invalid_device_response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"device_code":"device-code"}`)
		}))
		defer server.Close()

		provider := GitHubCopilotSubscriptionProvider{DeviceCodeURL: server.URL}
		_, err := provider.Login(context.Background(), appcredential.LoginOptions{})
		require.ErrorContains(t, err, "invalid GitHub Copilot device code response")
	})

	t.Run("poll_failure", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/device", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{
				"device_code":"device-code",
				"user_code":"ABCD-EFGH",
				"verification_uri":"https://github.com/login/device",
				"interval":1,
				"expires_in":900
			}`)
		})
		mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"error":"access_denied","error_description":"nope"}`)
		})
		server := httptest.NewServer(mux)
		defer server.Close()

		provider := GitHubCopilotSubscriptionProvider{
			DeviceCodeURL:  server.URL + "/device",
			AccessTokenURL: server.URL + "/token",
			OpenBrowser:    func(string) error { return nil },
		}
		_, err := provider.Login(context.Background(), appcredential.LoginOptions{})
		require.ErrorContains(t, err, "GitHub Copilot device flow failed: access_denied: nope")
	})
}

type copilotRoundTripFunc func(*http.Request) (*http.Response, error)

func (f copilotRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
