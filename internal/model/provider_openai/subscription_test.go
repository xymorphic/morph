package provider_openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
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
	previous := runOpenURLCommand
	runOpenURLCommand = func(string, ...string) error {
		return errors.New("real OpenAI browser opener disabled in tests")
	}
	code := m.Run()
	runOpenURLCommand = previous
	os.Exit(code)
}

func TestOpenAISubscriptionProvider_AuthHeadersUsesJWTAccountMetadata(t *testing.T) {
	token := makeOpenAITestJWT(t, "acct-test")

	headers, err := OpenAISubscriptionProvider{}.AuthHeaders(context.Background(), appcredential.StoredCredential{
		Type:  appcredential.TypeOAuth,
		Token: token,
	})
	require.NoError(t, err)
	require.Equal(t, "Bearer "+token, headers["Authorization"])
	require.Equal(t, "acct-test", headers["ChatGPT-Account-ID"])
	require.Equal(t, "responses=experimental", headers["OpenAI-Beta"])
	require.Equal(t, "morph", headers["Originator"])
	require.NotEmpty(t, headers["User-Agent"])
}

func TestOpenAISubscriptionProvider_AuthHeadersRejectsTokenWithoutAccountMetadata(t *testing.T) {
	token := makeOpenAITestJWTWithClaims(t, map[string]any{"sub": "user"})

	_, err := OpenAISubscriptionProvider{}.AuthHeaders(context.Background(), appcredential.StoredCredential{
		Type:  appcredential.TypeOAuth,
		Token: token,
	})
	require.ErrorContains(t, err, "account metadata")
}

func TestOpenAISubscriptionProvider_AuthHeadersValidatesToken(t *testing.T) {
	_, err := OpenAISubscriptionProvider{}.AuthHeaders(context.Background(), appcredential.StoredCredential{})
	require.ErrorContains(t, err, "access token is required")

	_, err = getOpenAIAccountID("not-a-jwt")
	require.ErrorContains(t, err, "must be a JWT")

	_, err = getOpenAIAccountID("header.%%%.signature")
	require.ErrorContains(t, err, "decode OpenAI subscription token")

	invalidJSON := base64.RawURLEncoding.EncodeToString([]byte("{"))
	_, err = getOpenAIAccountID("header." + invalidJSON + ".signature")
	require.Error(t, err)

	token := makeOpenAITestJWTWithClaims(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{},
	})
	_, err = getOpenAIAccountID(token)
	require.ErrorContains(t, err, "missing account ID")
}

func TestOpenAISubscriptionProvider_RefreshPostsRefreshGrantAndPreservesRefreshToken(t *testing.T) {
	var received url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.NoError(t, r.ParseForm())
		received = r.PostForm
		_, _ = fmt.Fprintf(w, `{"access_token":%q,"expires_in":3600,"scope":"openid email"}`, makeOpenAITestJWT(t, "acct-new"))
	}))
	defer server.Close()

	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	provider := OpenAISubscriptionProvider{
		TokenURL:    server.URL,
		OpenBrowser: func(string) error { return nil },
		Now:         func() time.Time { return now },
	}

	credential, err := provider.Refresh(context.Background(), appcredential.StoredCredential{
		Type:    appcredential.TypeOAuth,
		Token:   makeOpenAITestJWT(t, "acct-old"),
		Refresh: "refresh-secret",
	})
	require.NoError(t, err)
	require.Equal(t, "refresh_token", received.Get("grant_type"))
	require.Equal(t, "refresh-secret", received.Get("refresh_token"))
	require.Equal(t, openAISubscriptionClientID, received.Get("client_id"))
	require.Equal(t, "refresh-secret", credential.Refresh)
	require.Equal(t, []string{"openid", "email"}, credential.Scopes)
	require.NotNil(t, credential.ExpiresAt)
	require.Equal(t, now.Add(time.Hour), *credential.ExpiresAt)
}

func TestOpenAISubscriptionProvider_RefreshValidatesAndPropagatesTokenErrors(t *testing.T) {
	_, err := OpenAISubscriptionProvider{}.Refresh(context.Background(), appcredential.StoredCredential{})
	require.ErrorContains(t, err, "refresh token is required")

	for name, tc := range map[string]struct {
		body   string
		status int
		want   string
	}{
		"non_success": {
			body:   `{"error":"bad"}`,
			status: http.StatusInternalServerError,
			want:   "OpenAI token request failed: 500 Internal Server Error",
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

			provider := OpenAISubscriptionProvider{
				TokenURL:    server.URL,
				OpenBrowser: func(string) error { return nil },
			}
			_, err := provider.Refresh(context.Background(), appcredential.StoredCredential{Refresh: "refresh-secret"})
			require.ErrorContains(t, err, tc.want)
		})
	}

	provider := OpenAISubscriptionProvider{
		TokenURL:    "://bad-url",
		OpenBrowser: func(string) error { return nil },
	}
	_, err = provider.Refresh(context.Background(), appcredential.StoredCredential{Refresh: "refresh-secret"})
	require.Error(t, err)

	provider = OpenAISubscriptionProvider{
		TokenURL: "https://token.test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("network down")
		})},
		OpenBrowser: func(string) error { return nil },
	}
	_, err = provider.Refresh(context.Background(), appcredential.StoredCredential{Refresh: "refresh-secret"})
	require.ErrorContains(t, err, "network down")
}

func TestOpenAISubscriptionProvider_PostTokenHandlesReadErrorAndNoExpiry(t *testing.T) {
	provider := OpenAISubscriptionProvider{
		TokenURL: "https://token.test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       errReadCloser{},
				Header:     make(http.Header),
			}, nil
		})},
		OpenBrowser: func(string) error { return nil },
	}.withDefaults()

	_, err := provider.postToken(context.Background(), url.Values{})
	require.ErrorContains(t, err, "read failed")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"access_token":%q}`, makeOpenAITestJWT(t, "acct-no-expiry"))
	}))
	defer server.Close()

	provider = OpenAISubscriptionProvider{
		TokenURL:    server.URL,
		OpenBrowser: func(string) error { return nil },
	}.withDefaults()
	credential, err := provider.postToken(context.Background(), url.Values{})
	require.NoError(t, err)
	require.Equal(t, appcredential.TypeOAuth, credential.Type)
	require.NotEmpty(t, credential.Token)
	require.Nil(t, credential.ExpiresAt)
}

func TestOpenAISubscriptionProvider_LoginCompletesBrowserOAuthFlow(t *testing.T) {
	var authURL string
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		require.Equal(t, "authorization_code", r.PostForm.Get("grant_type"))
		require.Equal(t, "code-from-browser", r.PostForm.Get("code"))
		require.NotEmpty(t, r.PostForm.Get("code_verifier"))
		require.Contains(t, r.PostForm.Get("redirect_uri"), "/auth/callback")
		_, _ = fmt.Fprintf(w, `{"access_token":%q,"refresh_token":"refresh-new","expires_in":60}`, makeOpenAITestJWT(t, "acct-login"))
	}))
	defer tokenServer.Close()

	provider := OpenAISubscriptionProvider{
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
	require.Equal(t, "code", parsed.Query().Get("response_type"))
	require.Equal(t, openAISubscriptionClientID, parsed.Query().Get("client_id"))
	require.Equal(t, "S256", parsed.Query().Get("code_challenge_method"))
	require.NotEmpty(t, parsed.Query().Get("code_challenge"))
	require.Equal(t, openAISubscriptionScope, parsed.Query().Get("scope"))
	require.Equal(t, "true", parsed.Query().Get("id_token_add_organizations"))
	require.Equal(t, "true", parsed.Query().Get("codex_cli_simplified_flow"))
	require.Equal(t, openAISubscriptionOriginator, parsed.Query().Get("originator"))
	require.Empty(t, parsed.Query().Get("prompt"))

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
	require.Equal(t, "refresh-new", result.credential.Refresh)
	require.NotEmpty(t, result.credential.Token)
}

func TestOpenAISubscriptionProvider_LoginHandlesCallbackErrorsAndContext(t *testing.T) {
	t.Run("pkce generation error", func(t *testing.T) {
		previous := openAIRandomReader
		openAIRandomReader = errReader{}
		t.Cleanup(func() { openAIRandomReader = previous })

		provider := OpenAISubscriptionProvider{
			ListenAddr:  "127.0.0.1:0",
			OpenBrowser: func(string) error { return nil },
		}
		_, err := provider.Login(context.Background(), appcredential.LoginOptions{})
		require.ErrorContains(t, err, "forced read failure")
	})

	t.Run("listen error", func(t *testing.T) {
		provider := OpenAISubscriptionProvider{
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

		provider := OpenAISubscriptionProvider{
			ListenAddr:  "127.0.0.1:0",
			OpenBrowser: func(string) error { return nil },
		}
		_, err := provider.Login(ctx, appcredential.LoginOptions{Output: &output})
		require.ErrorIs(t, err, context.Canceled)
		require.Contains(t, output.String(), "Open this URL to authenticate OpenAI")
	})

	t.Run("invalid state", func(t *testing.T) {
		err := runOpenAILoginCallback(t, func(callback *url.URL, auth *url.URL) {
			query := callback.Query()
			query.Set("state", "wrong")
			query.Set("code", "code-from-browser")
			callback.RawQuery = query.Encode()
		})
		require.ErrorContains(t, err, "state mismatch")
	})

	t.Run("missing code", func(t *testing.T) {
		err := runOpenAILoginCallback(t, func(callback *url.URL, auth *url.URL) {
			query := callback.Query()
			query.Set("state", auth.Query().Get("state"))
			callback.RawQuery = query.Encode()
		})
		require.ErrorContains(t, err, "code is required")
	})

	t.Run("state generation error", func(t *testing.T) {
		previous := openAIRandomReader
		openAIRandomReader = &limitedReader{remaining: 64}
		t.Cleanup(func() { openAIRandomReader = previous })

		provider := OpenAISubscriptionProvider{
			ListenAddr:  "127.0.0.1:0",
			OpenBrowser: func(string) error { return nil },
		}
		_, err := provider.Login(context.Background(), appcredential.LoginOptions{})
		require.ErrorContains(t, err, "forced read failure")
	})
}

func TestOpenAISubscriptionProvider_WithDefaultsAndCallbackRedirectURI(t *testing.T) {
	provider := OpenAISubscriptionProvider{}.withDefaults()
	require.Equal(t, openAISubscriptionAuthorize, provider.AuthorizeURL)
	require.Equal(t, openAISubscriptionToken, provider.TokenURL)
	require.Empty(t, provider.RedirectURI)
	require.Equal(t, "127.0.0.1:1455", provider.ListenAddr)
	require.NotNil(t, provider.HTTPClient)
	require.NotNil(t, provider.OpenBrowser)
	require.NotNil(t, provider.Now)

	listener, redirectURI, err := OpenAISubscriptionProvider{}.withDefaults().listenForCallback()
	require.NoError(t, err)
	require.NoError(t, listener.Close())
	require.Contains(t, redirectURI, openAISubscriptionCallbackPath)
	require.True(t,
		strings.HasPrefix(redirectURI, "http://localhost:1455/") ||
			strings.HasPrefix(redirectURI, "http://localhost:1457/"),
	)

	listener, redirectURI, err = OpenAISubscriptionProvider{
		RedirectURI: "http://custom.test/callback",
		ListenAddr:  "127.0.0.1:0",
	}.withDefaults().listenForCallback()
	require.NoError(t, err)
	require.NoError(t, listener.Close())
	require.Equal(t, "http://custom.test/callback", redirectURI)
}

func TestOpenAISubscriptionProvider_StartCallbackServerReportsServeError(t *testing.T) {
	errCh := make(chan error, 1)
	server := OpenAISubscriptionProvider{}.startCallbackServer(errListener{}, "state", make(chan string, 1), errCh)
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

func TestOpenAISubscriptionProvider_OpenURLInBrowserRunsPlatformCommand(t *testing.T) {
	previous := runOpenURLCommand
	t.Cleanup(func() { runOpenURLCommand = previous })

	var gotName string
	var gotArgs []string
	runOpenURLCommand = func(name string, args ...string) error {
		gotName = name
		gotArgs = args
		return errors.New("open failed")
	}

	err := openURLInBrowser("https://example.test")
	require.ErrorContains(t, err, "open failed")
	require.NotEmpty(t, gotName)
	require.Contains(t, gotArgs, "https://example.test")
}

func TestGetOpenURLCommand(t *testing.T) {
	name, args := getOpenURLCommand("darwin", "https://example.test")
	require.Equal(t, "open", name)
	require.Equal(t, []string{"https://example.test"}, args)

	name, args = getOpenURLCommand("windows", "https://example.test")
	require.Equal(t, "rundll32", name)
	require.Equal(t, []string{"url.dll,FileProtocolHand", "https://example.test"}, args)

	name, args = getOpenURLCommand("linux", "https://example.test")
	require.Equal(t, "xdg-open", name)
	require.Equal(t, []string{"https://example.test"}, args)
}

func TestNewOpenAIPKCEPropagatesRandomReadError(t *testing.T) {
	previous := openAIRandomReader
	openAIRandomReader = errReader{}
	t.Cleanup(func() { openAIRandomReader = previous })

	_, _, err := newOpenAIPKCE()
	require.ErrorContains(t, err, "forced read failure")
}

func runOpenAILoginCallback(
	t *testing.T,
	setQuery func(callback *url.URL, auth *url.URL),
) error {
	t.Helper()

	var authURL string
	provider := OpenAISubscriptionProvider{
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type errReadCloser struct{}

func (errReadCloser) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

func (errReadCloser) Close() error {
	return nil
}

type errListener struct{}

func (errListener) Accept() (net.Conn, error) {
	return nil, errors.New("serve failed")
}

func (errListener) Close() error {
	return nil
}

func (errListener) Addr() net.Addr {
	return testAddr("test")
}

type testAddr string

func (a testAddr) Network() string {
	return string(a)
}

func (a testAddr) String() string {
	return string(a)
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, errors.New("forced read failure")
}

type limitedReader struct {
	remaining int
}

func (r *limitedReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, errors.New("forced read failure")
	}

	n := len(p)
	if n > r.remaining {
		n = r.remaining
	}
	for i := range p[:n] {
		p[i] = byte(i)
	}
	r.remaining -= n
	return n, nil
}

func makeOpenAITestJWT(t *testing.T, accountID string) string {
	t.Helper()

	return makeOpenAITestJWTWithClaims(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
		},
	})
}

func makeOpenAITestJWTWithClaims(t *testing.T, claims map[string]any) string {
	t.Helper()

	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	body, err := json.Marshal(claims)
	require.NoError(t, err)
	payload := base64.RawURLEncoding.EncodeToString(body)

	return strings.Join([]string{header, payload, "signature"}, ".")
}
