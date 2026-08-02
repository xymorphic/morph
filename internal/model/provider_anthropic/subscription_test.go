package provider_anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	appcredential "github.com/xymorphic/morph/internal/credential"
)

func TestMain(m *testing.M) {
	previous := runAnthropicOpenURL
	runAnthropicOpenURL = func(string, ...string) error {
		return errors.New("real Anthropic browser opener disabled in tests")
	}
	code := m.Run()
	runAnthropicOpenURL = previous
	os.Exit(code)
}

func TestAnthropicSubscriptionProvider_AuthHeadersUsesBearerToken(t *testing.T) {
	headers, err := AnthropicSubscriptionProvider{}.AuthHeaders(context.Background(), appcredential.StoredCredential{
		Type:  appcredential.TypeOAuth,
		Token: "access-secret",
	})

	require.NoError(t, err)
	require.Equal(t, "Bearer access-secret", headers["Authorization"])
	require.Equal(t, "claude-code-20250219,oauth-2025-04-20", headers["anthropic-beta"])
	require.Equal(t, "true", headers["anthropic-dangerous-direct-browser-access"])
	require.Equal(t, "claude-cli/morph", headers["user-agent"])
	require.Equal(t, "cli", headers["x-app"])
}

func TestAnthropicSubscriptionProvider_AuthHeadersValidatesToken(t *testing.T) {
	_, err := AnthropicSubscriptionProvider{}.AuthHeaders(context.Background(), appcredential.StoredCredential{})

	require.ErrorContains(t, err, "access token is required")
}

func TestAnthropicSubscriptionProvider_RefreshPostsRefreshGrantAndPreservesRefreshToken(t *testing.T) {
	var received map[string]string
	var beta string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		beta = r.Header.Get("anthropic-beta")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&received))
		_, _ = io.WriteString(w, `{"access_token":"access-new","expires_in":3600,"scope":"user:profile user:inference"}`)
	}))
	defer server.Close()

	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	provider := AnthropicSubscriptionProvider{
		TokenURL:    server.URL,
		OpenBrowser: func(string) error { return nil },
		Now:         func() time.Time { return now },
	}

	credential, err := provider.Refresh(context.Background(), appcredential.StoredCredential{
		Type:    appcredential.TypeOAuth,
		Token:   "access-old",
		Refresh: "refresh-secret",
	})

	require.NoError(t, err)
	require.Equal(t, anthropicOAuthBeta, beta)
	require.Equal(t, map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     anthropicSubscriptionClientID,
		"refresh_token": "refresh-secret",
	}, received)
	require.Equal(t, appcredential.TypeOAuth, credential.Type)
	require.Equal(t, "access-new", credential.Token)
	require.Equal(t, "refresh-secret", credential.Refresh)
	require.Equal(t, []string{"user:profile", "user:inference"}, credential.Scopes)
	require.NotNil(t, credential.ExpiresAt)
	require.Equal(t, now.Add(55*time.Minute), *credential.ExpiresAt)
}

func TestAnthropicSubscriptionProvider_RefreshValidatesAndPropagatesTokenErrors(t *testing.T) {
	_, err := AnthropicSubscriptionProvider{}.Refresh(context.Background(), appcredential.StoredCredential{})
	require.ErrorContains(t, err, "refresh token is required")

	for name, tc := range map[string]struct {
		body   string
		status int
		want   string
	}{
		"non_success": {
			body:   `{"error":"bad"}`,
			status: http.StatusInternalServerError,
			want:   "anthropic token request failed: 500 Internal Server Error",
		},
		"invalid_json": {
			body:   `{`,
			status: http.StatusOK,
			want:   "unexpected end of JSON input",
		},
		"missing_access_token": {
			body:   `{"refresh_token":"refresh-new"}`,
			status: http.StatusOK,
			want:   "did not include an access token",
		},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()

			provider := AnthropicSubscriptionProvider{
				TokenURL:    server.URL,
				OpenBrowser: func(string) error { return nil },
			}
			_, err := provider.Refresh(context.Background(), appcredential.StoredCredential{Refresh: "refresh-secret"})
			require.ErrorContains(t, err, tc.want)
		})
	}

	provider := AnthropicSubscriptionProvider{
		TokenURL:    "://bad-url",
		OpenBrowser: func(string) error { return nil },
	}
	_, err = provider.Refresh(context.Background(), appcredential.StoredCredential{Refresh: "refresh-secret"})
	require.Error(t, err)

	provider = AnthropicSubscriptionProvider{
		TokenURL: "https://token.test",
		HTTPClient: &http.Client{Transport: anthropicRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("network down")
		})},
		OpenBrowser: func(string) error { return nil },
	}
	_, err = provider.Refresh(context.Background(), appcredential.StoredCredential{Refresh: "refresh-secret"})
	require.ErrorContains(t, err, "network down")
}

func TestAnthropicSubscriptionProvider_PostTokenHandlesReadErrorAndNoExpiry(t *testing.T) {
	provider := AnthropicSubscriptionProvider{
		TokenURL: "https://token.test",
		HTTPClient: &http.Client{Transport: anthropicRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       anthropicErrReadCloser{},
				Header:     make(http.Header),
			}, nil
		})},
		OpenBrowser: func(string) error { return nil },
	}.withDefaults()

	_, err := provider.postToken(context.Background(), map[string]string{})
	require.ErrorContains(t, err, "read failed")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"access_token":"access-no-expiry"}`)
	}))
	defer server.Close()

	provider = AnthropicSubscriptionProvider{
		TokenURL:    server.URL,
		OpenBrowser: func(string) error { return nil },
	}.withDefaults()
	credential, err := provider.postToken(context.Background(), map[string]string{})
	require.NoError(t, err)
	require.Equal(t, appcredential.TypeOAuth, credential.Type)
	require.Equal(t, "access-no-expiry", credential.Token)
	require.Nil(t, credential.ExpiresAt)

	shortExpiryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"access_token":"access-short","expires_in":60}`)
	}))
	defer shortExpiryServer.Close()

	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	provider.TokenURL = shortExpiryServer.URL
	provider.Now = func() time.Time { return now }
	credential, err = provider.postToken(context.Background(), map[string]string{})
	require.NoError(t, err)
	require.NotNil(t, credential.ExpiresAt)
	require.Equal(t, now.Add(time.Minute), *credential.ExpiresAt)
}

func TestAnthropicSubscriptionProvider_LoginCompletesBrowserOAuthFlow(t *testing.T) {
	var authURL string
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "authorization_code", body["grant_type"])
		require.Equal(t, "code-from-browser", body["code"])
		require.NotEmpty(t, body["state"])
		require.NotEmpty(t, body["code_verifier"])
		require.Contains(t, body["redirect_uri"], "/callback")
		_, _ = io.WriteString(w, `{"access_token":"access-login","refresh_token":"refresh-new","expires_in":60}`)
	}))
	defer tokenServer.Close()

	provider := AnthropicSubscriptionProvider{
		AuthorizeURL: "https://auth.test/authorize",
		TokenURL:     tokenServer.URL,
		ListenAddr:   "127.0.0.1:0",
		OpenBrowser: func(rawURL string) error {
			authURL = rawURL
			return nil
		},
	}

	resultCh := make(chan struct {
		credential appcredential.StoredCredential
		err        error
	}, 1)
	go func() {
		credential, err := provider.Login(context.Background(), appcredential.LoginOptions{})
		resultCh <- struct {
			credential appcredential.StoredCredential
			err        error
		}{credential: credential, err: err}
	}()

	require.Eventually(t, func() bool { return authURL != "" }, time.Second, 10*time.Millisecond)
	parsed, err := url.Parse(authURL)
	require.NoError(t, err)
	require.Equal(t, "true", parsed.Query().Get("code"))
	require.Equal(t, "code", parsed.Query().Get("response_type"))
	require.Equal(t, anthropicSubscriptionClientID, parsed.Query().Get("client_id"))
	require.Equal(t, anthropicSubscriptionScope, parsed.Query().Get("scope"))
	require.Equal(t, "S256", parsed.Query().Get("code_challenge_method"))
	require.NotEmpty(t, parsed.Query().Get("code_challenge"))

	callbackURL := parsed.Query().Get("redirect_uri")
	callback, err := url.Parse(callbackURL)
	require.NoError(t, err)
	query := callback.Query()
	query.Set("code", "code-from-browser")
	query.Set("state", parsed.Query().Get("state"))
	callback.RawQuery = query.Encode()
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(callback.String())
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result struct {
		credential appcredential.StoredCredential
		err        error
	}
	require.Eventually(t, func() bool {
		select {
		case result = <-resultCh:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
	require.NoError(t, result.err)
	require.Equal(t, appcredential.TypeOAuth, result.credential.Type)
	require.Equal(t, "access-login", result.credential.Token)
	require.Equal(t, "refresh-new", result.credential.Refresh)
}

func TestAnthropicSubscriptionProvider_LoginHandlesCallbackErrorsAndContext(t *testing.T) {
	t.Run("pkce generation error", func(t *testing.T) {
		previous := anthropicRandomReader
		anthropicRandomReader = anthropicErrReader{}
		t.Cleanup(func() { anthropicRandomReader = previous })

		provider := AnthropicSubscriptionProvider{
			ListenAddr:  "127.0.0.1:0",
			OpenBrowser: func(string) error { return nil },
		}
		_, err := provider.Login(context.Background(), appcredential.LoginOptions{})
		require.ErrorContains(t, err, "forced read failure")
	})

	t.Run("listen error", func(t *testing.T) {
		provider := AnthropicSubscriptionProvider{
			ListenAddr:  "127.0.0.1:-1",
			OpenBrowser: func(string) error { return nil },
		}
		_, err := provider.Login(context.Background(), appcredential.LoginOptions{})
		require.Error(t, err)
	})

	t.Run("context canceled", func(t *testing.T) {
		var output bytes.Buffer
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		provider := AnthropicSubscriptionProvider{
			ListenAddr:  "127.0.0.1:0",
			OpenBrowser: func(string) error { return nil },
		}
		_, err := provider.Login(ctx, appcredential.LoginOptions{Output: &output})
		require.ErrorIs(t, err, context.Canceled)
		require.Contains(t, output.String(), "Open this URL to authenticate Anthropic")
	})

	t.Run("invalid state", func(t *testing.T) {
		err := runAnthropicLoginCallback(t, func(callback *url.URL, auth *url.URL) {
			query := callback.Query()
			query.Set("state", "wrong")
			query.Set("code", "code-from-browser")
			callback.RawQuery = query.Encode()
		})
		require.ErrorContains(t, err, "state mismatch")
	})

	t.Run("missing code", func(t *testing.T) {
		err := runAnthropicLoginCallback(t, func(callback *url.URL, auth *url.URL) {
			query := callback.Query()
			query.Set("state", auth.Query().Get("state"))
			callback.RawQuery = query.Encode()
		})
		require.ErrorContains(t, err, "code is required")
	})

	t.Run("oauth callback error", func(t *testing.T) {
		err := runAnthropicLoginCallback(t, func(callback *url.URL, auth *url.URL) {
			query := callback.Query()
			query.Set("state", auth.Query().Get("state"))
			query.Set("error", "access_denied")
			callback.RawQuery = query.Encode()
		})
		require.ErrorContains(t, err, "access_denied")
	})
}

func TestAnthropicSubscriptionProvider_WithDefaultsAndCallbackRedirectURI(t *testing.T) {
	provider := AnthropicSubscriptionProvider{}.withDefaults()
	require.Equal(t, anthropicSubscriptionAuthorize, provider.AuthorizeURL)
	require.Equal(t, anthropicSubscriptionToken, provider.TokenURL)
	require.Empty(t, provider.RedirectURI)
	require.Equal(t, "127.0.0.1:0", provider.ListenAddr)
	require.NotNil(t, provider.HTTPClient)
	require.NotNil(t, provider.OpenBrowser)
	require.NotNil(t, provider.Now)

	listener, redirectURI, err := AnthropicSubscriptionProvider{}.withDefaults().listenForCallback()
	require.NoError(t, err)
	require.NoError(t, listener.Close())
	require.Contains(t, redirectURI, anthropicSubscriptionCallbackPath)
	require.NotContains(t, redirectURI, ":53692")

	listener, redirectURI, err = AnthropicSubscriptionProvider{
		RedirectURI: "http://custom.test/callback",
		ListenAddr:  "127.0.0.1:0",
	}.withDefaults().listenForCallback()
	require.NoError(t, err)
	require.NoError(t, listener.Close())
	require.Equal(t, "http://custom.test/callback", redirectURI)
}

func TestAnthropicSubscriptionProvider_StartCallbackServerReportsServeError(t *testing.T) {
	errCh := make(chan error, 1)
	server := AnthropicSubscriptionProvider{}.startCallbackServer(
		anthropicErrListener{},
		"state",
		make(chan string, 1),
		errCh,
	)
	defer func() { _ = server.Close() }()

	var err error
	require.Eventually(t, func() bool {
		select {
		case err = <-errCh:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
	require.ErrorContains(t, err, "serve failed")
}

func TestAnthropicSubscriptionProvider_OpenURLInBrowserRunsPlatformCommand(t *testing.T) {
	previous := runAnthropicOpenURL
	t.Cleanup(func() { runAnthropicOpenURL = previous })

	var gotName string
	var gotArgs []string
	runAnthropicOpenURL = func(name string, args ...string) error {
		gotName = name
		gotArgs = args
		return errors.New("open failed")
	}

	err := openAnthropicURLInBrowser("https://example.test")
	require.ErrorContains(t, err, "open failed")
	require.NotEmpty(t, gotName)
	require.Contains(t, gotArgs, "https://example.test")
}

func TestGetAnthropicOpenURLCommand(t *testing.T) {
	name, args := getAnthropicOpenURLCommand("darwin", "https://example.test")
	require.Equal(t, "open", name)
	require.Equal(t, []string{"https://example.test"}, args)

	name, args = getAnthropicOpenURLCommand("windows", "https://example.test")
	require.Equal(t, "rundll32", name)
	require.Equal(t, []string{"url.dll,FileProtocolHand", "https://example.test"}, args)

	name, args = getAnthropicOpenURLCommand("linux", "https://example.test")
	require.Equal(t, "xdg-open", name)
	require.Equal(t, []string{"https://example.test"}, args)
}

func runAnthropicLoginCallback(
	t *testing.T,
	setQuery func(callback *url.URL, auth *url.URL),
) error {
	t.Helper()

	var authURL string
	provider := AnthropicSubscriptionProvider{
		AuthorizeURL: "https://auth.test/authorize",
		TokenURL:     "https://token.test",
		ListenAddr:   "127.0.0.1:0",
		OpenBrowser: func(rawURL string) error {
			authURL = rawURL
			return nil
		},
	}

	resultCh := make(chan error, 1)
	go func() {
		_, err := provider.Login(context.Background(), appcredential.LoginOptions{})
		resultCh <- err
	}()

	require.Eventually(t, func() bool { return authURL != "" }, time.Second, 10*time.Millisecond)
	auth, err := url.Parse(authURL)
	require.NoError(t, err)
	callback, err := url.Parse(auth.Query().Get("redirect_uri"))
	require.NoError(t, err)
	setQuery(callback, auth)

	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(callback.String())
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	var result error
	require.Eventually(t, func() bool {
		select {
		case result = <-resultCh:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)

	return result
}

type anthropicRoundTripFunc func(*http.Request) (*http.Response, error)

func (f anthropicRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type anthropicErrReadCloser struct{}

func (anthropicErrReadCloser) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

func (anthropicErrReadCloser) Close() error {
	return nil
}

type anthropicErrListener struct{}

func (anthropicErrListener) Accept() (net.Conn, error) {
	return nil, errors.New("serve failed")
}

func (anthropicErrListener) Close() error {
	return nil
}

func (anthropicErrListener) Addr() net.Addr {
	return anthropicTestAddr("test")
}

type anthropicTestAddr string

func (a anthropicTestAddr) Network() string {
	return string(a)
}

func (a anthropicTestAddr) String() string {
	return string(a)
}

type anthropicErrReader struct{}

func (anthropicErrReader) Read([]byte) (int, error) {
	return 0, errors.New("forced read failure")
}

func TestNewAnthropicPKCEPropagatesRandomReadError(t *testing.T) {
	previous := anthropicRandomReader
	anthropicRandomReader = anthropicErrReader{}
	t.Cleanup(func() { anthropicRandomReader = previous })

	_, _, err := newAnthropicPKCE()
	require.ErrorContains(t, err, "forced read failure")
}

func TestRandomAnthropicStringReturnsBase64URLValue(t *testing.T) {
	value, err := randomAnthropicString(8)

	require.NoError(t, err)
	require.NotEmpty(t, value)
	require.NotContains(t, value, "+")
	require.NotContains(t, value, "/")
}
