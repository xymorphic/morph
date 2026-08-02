package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	readability "codeberg.org/readeck/go-readability/v2"
	"github.com/stretchr/testify/require"
	"github.com/xymorphic/morph/internal/guardrails"
	"github.com/xymorphic/morph/pkg/logutils"
	"golang.org/x/net/html"
)

type nativeRoundTripFunc func(*http.Request) (*http.Response, error)

func (f nativeRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestNativeFetchPolicy_Check(t *testing.T) {
	t.Run("nil parsed url", func(t *testing.T) {
		policy := nativeFetchPolicy{}

		err := policy.Check(context.Background(), nil)

		require.NoError(t, err)
	})

	t.Run("blocked by host policy", func(t *testing.T) {
		policy := nativeFetchPolicy{
			hostPolicy: guardrails.NewHostPolicy(nil, []string{"blocked.example"}, nil, nil),
		}

		err := policy.Check(context.Background(), mustParseURL(t, "https://blocked.example/page"))

		require.EqualError(t, err, `blocked by configured native host denylist policy: "blocked.example" matched "blocked.example" from "config"`)
	})

	t.Run("allowed without website check", func(t *testing.T) {
		policy := nativeFetchPolicy{
			hostPolicy: guardrails.NewHostPolicy([]string{"allowed.example"}, nil, nil, nil),
		}

		err := policy.Check(context.Background(), mustParseURL(t, "https://allowed.example/page"))

		require.NoError(t, err)
	})

	t.Run("blocked by website check", func(t *testing.T) {
		policy := nativeFetchPolicy{
			websiteCheck: func(_ context.Context, rawURL string) error {
				require.Equal(t, "https://example.com/page", rawURL)
				return errors.New("blocked by website policy")
			},
		}

		err := policy.Check(context.Background(), mustParseURL(t, "https://example.com/page"))

		require.EqualError(t, err, "blocked by website policy")
	})
}

func TestNativeProvider_SearchReturnsUnsupportedError(t *testing.T) {
	provider := &NativeProvider{}

	_, err := provider.Search(context.Background(), "query", 5)

	require.ErrorIs(t, err, errNativeSearchUnsupported)
}

func TestNewNative_ConfiguresHTTPClientAndLimits(t *testing.T) {
	provider, err := NewNative(Options{
		MaxExtractCharPerResult: 123,
		MaxExtractResponseBytes: 456,
		NativeAllowedHosts:      []string{"allowed.example"},
		NativeBlockedHosts:      []string{"blocked.example"},
	})

	require.NoError(t, err)
	native, ok := provider.(*NativeProvider)
	require.True(t, ok)
	require.NotNil(t, native.client)
	require.NotNil(t, native.resolveHost)
	require.Equal(t, 123, native.maxExtractCharsPerResult)
	require.Equal(t, 456, native.maxExtractResponseBytes)
	require.Len(t, native.hostPolicy.AllowRules, 1)
	require.Len(t, native.hostPolicy.DenyRules, 1)

	_, _ = native.resolveHost(context.Background(), "localhost")
}

func TestNativeProvider_ExtractReadsHTMLAndRemovesBoilerplate(t *testing.T) {
	provider := newNativeTestProvider(`<html>
		<head><title>Example Title</title><script>secret()</script></head>
		<body>
			<nav>Navigation</nav>
			<main><article>
				<h1>Heading</h1>
				<p>Hello <strong>world</strong>. This article body has enough text for readability extraction.</p>
				<p>Another paragraph keeps the main content candidate stronger than the surrounding boilerplate.</p>
			</article></main>
			<footer>Footer</footer>
		</body>
	</html>`, "text/html")

	results, err := provider.Extract(context.Background(), []string{"https://example.com/page"})

	logutils.PrettyPrint(results)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "https://example.com/page", results[0].URL)
	require.Equal(t, "Example Title", results[0].Title)
	require.Equal(t, "text", results[0].ContentFormat)
	require.Contains(t, results[0].Content, "Heading")
	require.Contains(t, results[0].Content, "Hello")
	require.Contains(t, results[0].Content, "world")
	require.NotContains(t, results[0].Content, "Navigation")
	require.NotContains(t, results[0].Content, "secret")
	require.NotContains(t, results[0].Content, "Footer")
}

func TestNativeProvider_ExtractSupportsMarkdownHTML(t *testing.T) {
	provider := newNativeTestProvider(`<html>
		<head><title>Example</title></head>
		<body><article><h2>Overview</h2><p>Detailed article content for readability extraction.</p><ul><li>First point</li></ul></article></body>
	</html>`, "text/html")
	ctx := WithExtractOptions(context.Background(), ExtractOptions{Format: "markdown"})

	results, err := provider.Extract(ctx, []string{"https://example.com/page"})

	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "markdown", results[0].ContentFormat)
	require.Contains(t, results[0].Content, "## Overview")
	require.Contains(t, results[0].Content, "Detailed article content")
	require.Contains(t, results[0].Content, "- First point")
}

func TestNativeProvider_ExtractReadsPlainText(t *testing.T) {
	provider := newNativeTestProvider(" plain text content ", "text/plain")

	results, err := provider.Extract(context.Background(), []string{"https://example.com/page"})

	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "plain text content", results[0].Content)
	require.Equal(t, "text", results[0].ContentFormat)
}

func TestNativeProvider_ExtractUsesBrowserUserAgent(t *testing.T) {
	provider := newNativeTestProvider("plain text content", "text/plain")
	var userAgent string
	provider.client.Transport = nativeRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		userAgent = req.Header.Get("User-Agent")

		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"text/plain"}},
			Body:       io.NopCloser(strings.NewReader("plain text content")),
			Request:    req,
		}, nil
	})

	results, err := provider.Extract(context.Background(), []string{"https://example.com/page"})

	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Contains(t, userAgent, "Mozilla/5.0")
	require.Contains(t, userAgent, "Chrome/")
}

func TestNativeProvider_ExtractReturnsClientAndReadErrors(t *testing.T) {
	expectedErr := errors.New("network failed")
	provider := newNativeTestProvider("ignored", "text/plain")
	provider.client.Transport = nativeRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, expectedErr
	})

	results, err := provider.Extract(context.Background(), []string{"https://example.com/page"})

	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Contains(t, results[0].Error, "network failed")

	provider = newNativeTestProvider("ignored", "text/plain")
	provider.client.Transport = nativeRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"text/plain"}},
			Body:       errReader{err: errors.New("read failed")},
			Request:    req,
		}, nil
	})

	results, err = provider.Extract(context.Background(), []string{"https://example.com/page"})

	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "read failed", results[0].Error)
	require.False(t, results[0].DownloadTruncated)
}

func TestNativeProvider_ExtractReturnsRequestCreationError(t *testing.T) {
	provider := newNativeTestProvider("ignored", "text/plain")
	provider.newRequest = func(context.Context, string, string, io.Reader) (*http.Request, error) {
		return nil, errors.New("request failed")
	}

	results, err := provider.Extract(context.Background(), []string{"https://example.com/page"})

	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "request failed", results[0].Error)
}

func TestNativeProvider_ExtractBuildsClientWhenMissing(t *testing.T) {
	provider := newNativeTestProvider("ignored", "text/plain")
	provider.client = nil
	provider.makeClient = func() *http.Client {
		return &http.Client{
			Transport: nativeRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     http.Header{"Content-Type": []string{"text/plain"}},
					Body:       io.NopCloser(strings.NewReader("created client")),
					Request:    req,
				}, nil
			}),
		}
	}

	results, err := provider.Extract(context.Background(), []string{"https://example.com/page"})

	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "created client", results[0].Content)
}

func TestNativeProvider_ExtractUsesDefaultClientFactoryWhenMissing(t *testing.T) {
	previous := nativeDefaultHTTPClient
	t.Cleanup(func() {
		nativeDefaultHTTPClient = previous
	})
	nativeDefaultHTTPClient = func(*NativeProvider) *http.Client {
		return &http.Client{
			Transport: nativeRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     http.Header{"Content-Type": []string{"text/plain"}},
					Body:       io.NopCloser(strings.NewReader("default client")),
					Request:    req,
				}, nil
			}),
		}
	}
	provider := newNativeTestProvider("ignored", "text/plain")
	provider.client = nil

	results, err := provider.Extract(context.Background(), []string{"https://example.com/page"})

	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "default client", results[0].Content)
}

func TestNativeProvider_ExtractReturnsReadabilityError(t *testing.T) {
	provider := newNativeTestProvider("", "text/html")

	results, err := provider.Extract(context.Background(), []string{"https://example.com/page"})

	require.NoError(t, err)
	require.Len(t, results, 1)
	require.NotEmpty(t, results[0].Error)
	require.Empty(t, results[0].Content)
}

func TestNativeProvider_ExtractReturnsReadabilityParseError(t *testing.T) {
	previous := nativeReadabilityFromReader
	t.Cleanup(func() {
		nativeReadabilityFromReader = previous
	})
	nativeReadabilityFromReader = func(io.Reader, *url.URL) (readability.Article, error) {
		return readability.Article{}, errors.New("readability failed")
	}
	provider := newNativeTestProvider("<html></html>", "text/html")

	results, err := provider.Extract(context.Background(), []string{"https://example.com/page"})

	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "readability failed", results[0].Error)
}

func TestNativeProvider_ExtractAppliesResponseAndCharacterLimits(t *testing.T) {
	provider := newNativeTestProvider("abcdef", "text/plain")
	provider.maxExtractResponseBytes = 4
	provider.maxExtractCharsPerResult = 2

	results, err := provider.Extract(context.Background(), []string{"https://example.com/page"})

	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "ab", results[0].Content)
	require.True(t, results[0].Truncated)
	require.True(t, results[0].DownloadTruncated)
}

func TestNativeProvider_ExtractUsesRequestCharacterLimit(t *testing.T) {
	provider := newNativeTestProvider("abcdef", "text/plain")
	provider.maxExtractCharsPerResult = 5
	ctx := WithExtractOptions(context.Background(), ExtractOptions{MaxChars: 3})

	results, err := provider.Extract(ctx, []string{"https://example.com/page"})

	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "abc", results[0].Content)
	require.True(t, results[0].Truncated)
}

func TestNativeProvider_ExtractRejectsUnsafeURLs(t *testing.T) {
	provider := newNativeTestProvider("ignored", "text/plain")

	results, err := provider.Extract(context.Background(), []string{
		"file:///etc/passwd",
		"http://127.0.0.1/admin",
		"https://user@example.com/page",
	})

	require.NoError(t, err)
	require.Len(t, results, 3)
	require.Contains(t, results[0].Error, "url scheme must be http or https")
	require.Contains(t, results[1].Error, "blocked address")
	require.Contains(t, results[2].Error, "userinfo is not allowed")
}

func TestNativeProvider_ValidateURLRejectsBlockedHostPolicy(t *testing.T) {
	provider := &NativeProvider{
		hostPolicy: guardrails.NewHostPolicy(nil, []string{"blocked.example"}, nil, nil),
		resolveHost: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		},
	}

	_, err := provider.fetcher().ValidateURL(context.Background(), "https://blocked.example/page")

	require.EqualError(t, err, `blocked by configured native host denylist policy: "blocked.example" matched "blocked.example" from "config"`)
}

func TestNativeProvider_ValidateURLRejectsHostMissingFromAllowlist(t *testing.T) {
	provider := &NativeProvider{
		hostPolicy: guardrails.NewHostPolicy([]string{"allowed.example"}, nil, nil, nil),
		resolveHost: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		},
	}

	_, err := provider.fetcher().ValidateURL(context.Background(), "https://other.example/page")

	require.EqualError(t, err, `blocked by configured native host allowlist policy: "other.example" did not match any allowed host rule`)
}

func TestNativeProvider_ValidateURLRejectsMissingHostAndResolverErrors(t *testing.T) {
	provider := newNativeTestProvider("ignored", "text/plain")

	_, err := provider.fetcher().ValidateURL(context.Background(), "%")
	require.Error(t, err)

	_, err = provider.fetcher().ValidateURL(context.Background(), "https:///missing-host")
	require.EqualError(t, err, "url host is required")

	provider = &NativeProvider{
		resolveHost: func(context.Context, string) ([]netip.Addr, error) {
			return nil, errors.New("resolver failed")
		},
	}

	_, err = provider.fetcher().ValidateURL(context.Background(), "https://example.com/page")
	require.EqualError(t, err, "resolver failed")
}

func TestNativeProvider_ExtractRejectsUnsafeRedirect(t *testing.T) {
	provider := &NativeProvider{
		resolveHost: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		},
	}
	client := provider.newHTTPClient()
	client.Transport = nativeRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "example.com" {
			return &http.Response{
				StatusCode: http.StatusFound,
				Status:     "302 Found",
				Header:     http.Header{"Location": []string{"http://127.0.0.1/admin"}},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    req,
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"text/plain"}},
			Body:       io.NopCloser(strings.NewReader("unsafe")),
			Request:    req,
		}, nil
	})
	provider.client = client

	results, err := provider.Extract(context.Background(), []string{"https://example.com/page"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Contains(t, results[0].Error, "blocked address")
}

func TestNativeProvider_ExtractRejectsWebsitePolicyRedirect(t *testing.T) {
	provider := &NativeProvider{
		resolveHost: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		},
	}
	client := provider.newHTTPClient()
	client.Transport = nativeRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "example.com" {
			return &http.Response{
				StatusCode: http.StatusFound,
				Status:     "302 Found",
				Header:     http.Header{"Location": []string{"https://blocked.example/admin"}},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    req,
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"text/plain"}},
			Body:       io.NopCloser(strings.NewReader("blocked")),
			Request:    req,
		}, nil
	})
	provider.client = client
	ctx := WithExtractOptions(context.Background(), ExtractOptions{
		WebsitePolicy: guardrails.NewWebsitePolicy(true, []string{"blocked.example"}, nil),
	})

	results, err := provider.Extract(ctx, []string{"https://example.com/page"})

	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Contains(t, results[0].Error, "blocked by configured website blocklist policy")
	require.Empty(t, results[0].Content)
}

func TestNativeProvider_HTTPClientRejectsTooManyRedirects(t *testing.T) {
	provider := newNativeTestProvider("ignored", "text/plain")
	client := provider.newHTTPClient()
	req := &http.Request{URL: mustParseURL(t, "https://example.com/page")}
	via := []*http.Request{
		{URL: mustParseURL(t, "https://example.com/1")},
		{URL: mustParseURL(t, "https://example.com/2")},
		{URL: mustParseURL(t, "https://example.com/3")},
		{URL: mustParseURL(t, "https://example.com/4")},
		{URL: mustParseURL(t, "https://example.com/5")},
	}

	err := client.CheckRedirect(req, via)

	require.EqualError(t, err, "too many redirects")
}

func TestNativeProvider_ExtractUsesFinalRedirectURL(t *testing.T) {
	finalURL := "https://final.example/article"
	provider := &NativeProvider{
		resolveHost: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		},
		maxExtractCharsPerResult: 1000,
	}
	provider.client = &http.Client{
		Transport: nativeRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			finalReq, err := http.NewRequest(http.MethodGet, finalURL, nil)
			require.NoError(t, err)

			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"text/html"}},
				Body: io.NopCloser(strings.NewReader(`<html>
					<head><title>Redirected</title></head>
					<body><article><h1>Final page</h1><p>Final redirected article content for readability extraction.</p></article></body>
				</html>`)),
				Request: finalReq,
			}, nil
		}),
	}

	results, err := provider.Extract(context.Background(), []string{"https://example.com/page"})

	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, finalURL, results[0].URL)
	require.Equal(t, "Redirected", results[0].Title)
	require.Contains(t, results[0].Content, "Final page")
}

func TestNativeProvider_ExtractReturnsHTTPAndContentTypeErrors(t *testing.T) {
	provider := newNativeTestProviderWithStatus("{}", "application/json", http.StatusOK)
	results, err := provider.Extract(context.Background(), []string{"https://example.com/json"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "unsupported content type: application/json", results[0].Error)

	provider = newNativeTestProviderWithStatus("missing", "text/plain", http.StatusNotFound)
	results, err = provider.Extract(context.Background(), []string{"https://example.com/missing"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Contains(t, results[0].Error, "404 Not Found")
}

func TestNativeProvider_ExtractFallsBackToStatusCodeWhenStatusIsBlank(t *testing.T) {
	provider := &NativeProvider{
		resolveHost: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		},
	}
	provider.client = &http.Client{
		Transport: nativeRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Status:     "",
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
				Body:       io.NopCloser(strings.NewReader("bad gateway")),
				Request:    req,
			}, nil
		}),
	}

	results, err := provider.Extract(context.Background(), []string{"https://example.com/failure"})

	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "web extraction request failed: 502", results[0].Error)
}

func TestNativeProvider_DialContextReturnsValidationErrors(t *testing.T) {
	provider := &NativeProvider{}

	_, err := provider.fetcher().DialContext(context.Background(), "tcp", "bad-address")
	require.Error(t, err)

	provider = &NativeProvider{
		resolveHost: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("10.0.0.1")}, nil
		},
	}

	_, err = provider.fetcher().DialContext(context.Background(), "tcp", "internal.example:443")
	require.EqualError(t, err, "url host resolves to a blocked address")
}

func TestNativeProvider_DialContextReturnsConnectionAndDialErrors(t *testing.T) {
	provider := &NativeProvider{
		resolveHost: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		},
		dial: func(context.Context, string, string) (net.Conn, error) {
			clientConn, serverConn := net.Pipe()
			t.Cleanup(func() {
				_ = serverConn.Close()
			})
			return clientConn, nil
		},
	}

	conn, err := provider.fetcher().DialContext(context.Background(), "tcp", "example.com:443")

	require.NoError(t, err)
	require.NotNil(t, conn)
	require.NoError(t, conn.Close())

	provider.dial = func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("dial failed")
	}

	conn, err = provider.fetcher().DialContext(context.Background(), "tcp", "example.com:443")

	require.Nil(t, conn)
	require.EqualError(t, err, "dial failed")
}

func TestNativeProvider_DialContextUsesDefaultDialer(t *testing.T) {
	previous := nativeDefaultDialContext
	t.Cleanup(func() {
		nativeDefaultDialContext = previous
	})
	nativeDefaultDialContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("default dial failed")
	}
	provider := &NativeProvider{
		resolveHost: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		},
	}

	conn, err := provider.fetcher().DialContext(context.Background(), "tcp", "example.com:443")
	require.Nil(t, conn)
	require.EqualError(t, err, "default dial failed")
}

func TestNativeProvider_ResolveAndValidateHostHandlesLiteralAndEmptyResults(t *testing.T) {
	provider := &NativeProvider{}

	_, err := provider.fetcher().ResolveAndValidateHost(context.Background(), " ")
	require.EqualError(t, err, "url host is required")

	addrs, err := provider.fetcher().ResolveAndValidateHost(context.Background(), "93.184.216.34")
	require.NoError(t, err)
	require.Equal(t, []netip.Addr{netip.MustParseAddr("93.184.216.34")}, addrs)

	provider = &NativeProvider{
		resolveHost: func(context.Context, string) ([]netip.Addr, error) {
			return nil, nil
		},
	}

	_, err = provider.fetcher().ResolveAndValidateHost(context.Background(), "empty.example")
	require.EqualError(t, err, "url host resolved to no addresses")

	provider = &NativeProvider{}
	_, err = provider.fetcher().ResolveAndValidateHost(context.Background(), "localhost")
	require.Error(t, err)
}

func TestNativeProvider_ValidateURLRejectsResolvedPrivateAddress(t *testing.T) {
	provider := &NativeProvider{
		resolveHost: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("10.0.0.1")}, nil
		},
	}

	_, err := provider.fetcher().ValidateURL(context.Background(), "https://internal.example/page")

	require.EqualError(t, err, "url host resolves to a blocked address")
}

func TestNativeProvider_SafeNativeAddrClassifiesAddresses(t *testing.T) {
	require.True(t, guardrails.SafeAddr(netip.MustParseAddr("93.184.216.34"), nil))
	require.False(t, guardrails.SafeAddr(netip.MustParseAddr("127.0.0.1"), nil))
	require.False(t, guardrails.SafeAddr(netip.MustParseAddr("0.0.0.1"), nil))
	require.False(t, guardrails.SafeAddr(netip.MustParseAddr("10.0.0.1"), nil))
	require.False(t, guardrails.SafeAddr(netip.MustParseAddr("100.64.0.1"), nil))
	require.False(t, guardrails.SafeAddr(netip.MustParseAddr("169.254.169.254"), nil))
	require.False(t, guardrails.SafeAddr(netip.MustParseAddr("192.0.2.1"), nil))
	require.False(t, guardrails.SafeAddr(netip.MustParseAddr("192.88.99.1"), nil))
	require.False(t, guardrails.SafeAddr(netip.MustParseAddr("198.18.0.1"), nil))
	require.False(t, guardrails.SafeAddr(netip.MustParseAddr("203.0.113.1"), nil))
	require.False(t, guardrails.SafeAddr(netip.MustParseAddr("240.0.0.1"), nil))
	require.False(t, guardrails.SafeAddr(netip.MustParseAddr("::1"), nil))
	require.False(t, guardrails.SafeAddr(netip.MustParseAddr("::ffff:127.0.0.1"), nil))
	require.False(t, guardrails.SafeAddr(netip.MustParseAddr("64:ff9b::1"), nil))
	require.False(t, guardrails.SafeAddr(netip.MustParseAddr("64:ff9b:1::1"), nil))
	require.False(t, guardrails.SafeAddr(netip.MustParseAddr("100::1"), nil))
	require.False(t, guardrails.SafeAddr(netip.MustParseAddr("2001:db8::1"), nil))
	require.False(t, guardrails.SafeAddr(netip.MustParseAddr("2002::1"), nil))
	require.False(t, guardrails.SafeAddr(netip.Addr{}, nil))
}

func TestNativeMarkdownHelpers_RenderFormattingBoundaries(t *testing.T) {
	root := parseHTMLFragment(t, `<article>
		<h1>Title</h1><h3>Details</h3><h6>Fine Print</h6>
		<blockquote>Quoted text</blockquote>
		<p>Paragraph <strong>with emphasis</strong>.</p>
	</article>`)

	markdown := renderNativeMarkdown(root)

	require.Contains(t, markdown, "# Title")
	require.Contains(t, markdown, "### Details")
	require.Contains(t, markdown, "###### Fine Print")
	require.Contains(t, markdown, "Quoted text")
	require.Contains(t, markdown, "Paragraph with emphasis .")
	require.Empty(t, renderNativeMarkdown(nil))
	require.Empty(t, collectNativeText(nil))
	require.Equal(t, []string{"one", "", "two"}, compactNativeLines([]string{"", "one", "", "", "two", ""}))
	require.Equal(t, 2, getNativeHeadingLevel("h2"))
	require.Equal(t, 4, getNativeHeadingLevel("h4"))
	require.Equal(t, 5, getNativeHeadingLevel("h5"))
	require.Equal(t, 6, getNativeHeadingLevel("unknown"))
}

func TestReadNativeResponse_ReturnsTruncatedData(t *testing.T) {
	data, truncated, err := readNativeResponse(strings.NewReader("abcdef"), 3)

	require.NoError(t, err)
	require.True(t, truncated)
	require.Equal(t, "abc", string(data))
}

func TestReadNativeResponse_ReturnsFullDataWithinLimit(t *testing.T) {
	data, truncated, err := readNativeResponse(strings.NewReader("abcdef"), 6)

	require.NoError(t, err)
	require.False(t, truncated)
	require.Equal(t, "abcdef", string(data))
}

func TestReadNativeResponse_ReturnsLimitedReaderError(t *testing.T) {
	data, truncated, err := readNativeResponse(errReader{err: errors.New("limited read failed")}, 3)

	require.ErrorContains(t, err, "limited read failed")
	require.False(t, truncated)
	require.Nil(t, data)
}

func TestReadNativeResponse_ReadsUnlimitedWhenLimitDisabled(t *testing.T) {
	data, truncated, err := readNativeResponse(strings.NewReader("abcdef"), 0)

	require.NoError(t, err)
	require.False(t, truncated)
	require.Equal(t, "abcdef", string(data))
}

func TestReadNativeResponse_DropsPartialUTF8Rune(t *testing.T) {
	data, truncated, err := readNativeResponse(strings.NewReader("éclair"), 1)

	require.NoError(t, err)
	require.True(t, truncated)
	require.Empty(t, string(data))
}

func newNativeTestProvider(body, contentType string) *NativeProvider {
	return newNativeTestProviderWithStatus(body, contentType, http.StatusOK)
}

func newNativeTestProviderWithStatus(body, contentType string, status int) *NativeProvider {
	provider := &NativeProvider{
		resolveHost: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		},
		maxExtractCharsPerResult: 1000,
	}
	provider.client = &http.Client{
		Transport: nativeRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: status,
				Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
				Header:     http.Header{"Content-Type": []string{contentType}},
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    req,
			}, nil
		}),
	}

	return provider
}

type errReader struct {
	err error
}

func (r errReader) Read([]byte) (int, error) {
	return 0, r.err
}

func (r errReader) Close() error {
	return nil
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()

	parsed, err := url.Parse(raw)
	require.NoError(t, err)

	return parsed
}

func parseHTMLFragment(t *testing.T, raw string) *html.Node {
	t.Helper()

	nodes, err := html.ParseFragment(strings.NewReader(raw), nil)
	require.NoError(t, err)
	require.NotEmpty(t, nodes)

	return nodes[0]
}
