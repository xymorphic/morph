package fetch

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xymorphic/morph/pkg/netpolicy"
)

type stubPolicy struct {
	check func(context.Context, *url.URL) error
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func (p stubPolicy) Check(ctx context.Context, parsed *url.URL) error {
	if p.check == nil {
		return nil
	}

	return p.check(ctx, parsed)
}

func TestFetcher_ValidateURLRejectsInvalidInputs(t *testing.T) {
	fetcher := New()

	_, err := fetcher.ValidateURL(context.Background(), "%")
	require.Error(t, err)

	_, err = fetcher.ValidateURL(context.Background(), "file:///etc/passwd")
	require.EqualError(t, err, "url scheme must be http or https")

	_, err = fetcher.ValidateURL(context.Background(), "https:///missing-host")
	require.EqualError(t, err, "url host is required")

	_, err = fetcher.ValidateURL(context.Background(), "https://user@example.com/page")
	require.EqualError(t, err, "url userinfo is not allowed")
}

func TestFetcher_ValidateURLAppliesPolicy(t *testing.T) {
	fetcher := New(
		WithResolveHost(func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}),
		WithPolicy(stubPolicy{
			check: func(_ context.Context, parsed *url.URL) error {
				require.Equal(t, "https://example.com/page", parsed.String())
				require.Equal(t, "example.com", parsed.Hostname())
				return errors.New("blocked by policy")
			},
		}),
	)

	_, err := fetcher.ValidateURL(context.Background(), "https://example.com/page")
	require.EqualError(t, err, "blocked by policy")
}

func TestFetcher_ValidateURLReturnsResolverErrorAndSuccess(t *testing.T) {
	fetcher := New(WithResolveHost(func(context.Context, string) ([]netip.Addr, error) {
		return nil, errors.New("resolver failed")
	}))

	_, err := fetcher.ValidateURL(context.Background(), "https://example.com/page")
	require.EqualError(t, err, "resolver failed")

	fetcher = New(WithResolveHost(func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}))

	parsed, err := fetcher.ValidateURL(context.Background(), "https://example.com/page")
	require.NoError(t, err)
	require.Equal(t, "https://example.com/page", parsed.String())
}

func TestFetcher_ResolveAndValidateHostHandlesLiteralResolverAndBlockedAddresses(t *testing.T) {
	t.Run("empty host", func(t *testing.T) {
		fetcher := New()
		_, err := fetcher.ResolveAndValidateHost(context.Background(), " ")
		require.EqualError(t, err, "url host is required")
	})

	t.Run("literal address succeeds", func(t *testing.T) {
		fetcher := New()
		addrs, err := fetcher.ResolveAndValidateHost(context.Background(), "93.184.216.34")
		require.NoError(t, err)
		require.Equal(t, []netip.Addr{netip.MustParseAddr("93.184.216.34")}, addrs)
	})

	t.Run("literal loopback address is blocked", func(t *testing.T) {
		fetcher := New()
		_, err := fetcher.ResolveAndValidateHost(context.Background(), "127.0.0.1")
		require.EqualError(t, err, "url host resolves to a blocked address")
	})

	t.Run("resolver returns error", func(t *testing.T) {
		fetcher := New(WithResolveHost(func(context.Context, string) ([]netip.Addr, error) {
			return nil, errors.New("resolver failed")
		}))
		_, err := fetcher.ResolveAndValidateHost(context.Background(), "example.com")
		require.EqualError(t, err, "resolver failed")
	})

	t.Run("resolver returns no addresses", func(t *testing.T) {
		fetcher := New(WithResolveHost(func(context.Context, string) ([]netip.Addr, error) {
			return nil, nil
		}))
		_, err := fetcher.ResolveAndValidateHost(context.Background(), "example.com")
		require.EqualError(t, err, "url host resolved to no addresses")
	})

	t.Run("resolver returns blocked address", func(t *testing.T) {
		fetcher := New(WithResolveHost(func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("10.0.0.1")}, nil
		}))
		_, err := fetcher.ResolveAndValidateHost(context.Background(), "example.com")
		require.EqualError(t, err, "url host resolves to a blocked address")
	})

	t.Run("blocked prefix prevents resolution", func(t *testing.T) {
		fetcher := New(WithBlockedAddressPrefixes([]netip.Prefix{
			netip.MustParsePrefix("93.184.216.0/24"),
		}))
		_, err := fetcher.ResolveAndValidateHost(context.Background(), "93.184.216.34")
		require.EqualError(t, err, "url host resolves to a blocked address")
	})
}

func TestFetcher_DialContextHandlesErrorsAndSuccess(t *testing.T) {
	t.Run("invalid address returns error", func(t *testing.T) {
		fetcher := New()
		_, err := fetcher.DialContext(context.Background(), "tcp", "bad-address")
		require.Error(t, err)
	})

	t.Run("resolver fails", func(t *testing.T) {
		fetcher := New(WithResolveHost(func(context.Context, string) ([]netip.Addr, error) {
			return nil, errors.New("resolver failed")
		}))
		_, err := fetcher.DialContext(context.Background(), "tcp", "example.com:443")
		require.EqualError(t, err, "resolver failed")
	})

	t.Run("dial fails", func(t *testing.T) {
		fetcher := New(
			WithResolveHost(func(context.Context, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
			}),
			WithDial(func(context.Context, string, string) (net.Conn, error) {
				return nil, errors.New("dial failed")
			}),
		)
		_, err := fetcher.DialContext(context.Background(), "tcp", "example.com:443")
		require.EqualError(t, err, "dial failed")
	})

	t.Run("first address fails, second succeeds", func(t *testing.T) {
		fetcher := New(
			WithResolveHost(func(context.Context, string) ([]netip.Addr, error) {
				return []netip.Addr{
					netip.MustParseAddr("93.184.216.34"),
					netip.MustParseAddr("93.184.216.35"),
				}, nil
			}),
			WithDial(func(_ context.Context, _ string, address string) (net.Conn, error) {
				if address == "93.184.216.34:443" {
					return nil, errors.New("first failed")
				}

				clientConn, serverConn := net.Pipe()
				t.Cleanup(func() {
					_ = serverConn.Close()
				})
				return clientConn, nil
			}),
		)

		conn, err := fetcher.DialContext(context.Background(), "tcp", "example.com:443")
		require.NoError(t, err)
		require.NotNil(t, conn)
		require.NoError(t, conn.Close())
	})
}

func TestFetcher_DialContextUsesDefaultDialer(t *testing.T) {
	fetcher := New(WithResolveHost(func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}))

	_, err := fetcher.DialContext(context.Background(), "tcp", "example.com:1")
	require.Error(t, err)
}

func TestFetcher_DialContextReturnsLastErrorAfterAllAddressesFail(t *testing.T) {
	fetcher := New(
		WithResolveHost(func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{
				netip.MustParseAddr("93.184.216.34"),
				netip.MustParseAddr("93.184.216.35"),
			}, nil
		}),
		WithDial(func(_ context.Context, _ string, address string) (net.Conn, error) {
			if address == "93.184.216.34:443" {
				return nil, errors.New("first failed")
			}

			return nil, errors.New("second failed")
		}),
	)

	_, err := fetcher.DialContext(context.Background(), "tcp", "example.com:443")
	require.EqualError(t, err, "second failed")
}

func TestFetcher_NewHTTPClientCheckRedirect(t *testing.T) {
	t.Run("too many redirects", func(t *testing.T) {
		fetcher := New(WithResolveHost(func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}))

		client := fetcher.NewHTTPClient(time.Second)
		req, err := http.NewRequest(http.MethodGet, "https://example.com/page", nil)
		require.NoError(t, err)

		via := []*http.Request{
			mustNewRequest(t, "https://example.com/1"),
			mustNewRequest(t, "https://example.com/2"),
			mustNewRequest(t, "https://example.com/3"),
			mustNewRequest(t, "https://example.com/4"),
			mustNewRequest(t, "https://example.com/5"),
		}

		err = client.CheckRedirect(req, via)
		require.EqualError(t, err, "too many redirects")
	})

	t.Run("blocked by policy", func(t *testing.T) {
		fetcher := New(
			WithResolveHost(func(context.Context, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
			}),
			WithPolicy(stubPolicy{
				check: func(_ context.Context, parsed *url.URL) error {
					require.Equal(t, "https://example.com/page", parsed.String())
					return errors.New("blocked redirect")
				},
			}),
		)

		client := fetcher.NewHTTPClient(time.Second)
		req, err := http.NewRequest(http.MethodGet, "https://example.com/page", nil)
		require.NoError(t, err)

		err = client.CheckRedirect(req, nil)
		require.EqualError(t, err, "blocked redirect")
	})
}

func TestFetcher_GetReturnsValidatedResponse(t *testing.T) {
	fetcher := New(WithResolveHost(func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}))

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			require.Equal(t, "text/plain", req.Header.Get("Accept"))

			finalReq, err := http.NewRequest(http.MethodGet, "https://example.com/final", nil)
			require.NoError(t, err)

			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
				Body:       io.NopCloser(strings.NewReader("abcdef")),
				Request:    finalReq,
			}, nil
		}),
	}

	resp, err := fetcher.Get(context.Background(), GetRequest{
		URL:      "https://example.com/page",
		Header:   http.Header{"Accept": []string{"text/plain"}},
		MaxBytes: 3,
		Client:   client,
	})

	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode)
	require.Equal(t, "200 OK", resp.Status)
	require.Equal(t, "https://example.com/final", resp.FinalURL)
	require.Equal(t, []byte("abc"), resp.Body)
	require.True(t, resp.Truncated)
}

func TestFetcher_GetReturnsValidationRequestAndClientErrors(t *testing.T) {
	t.Run("invalid scheme returns error", func(t *testing.T) {
		fetcher := New()
		_, err := fetcher.Get(context.Background(), GetRequest{URL: "file:///etc/passwd"})
		require.EqualError(t, err, "url scheme must be http or https")
	})

	t.Run("NewRequest returns error", func(t *testing.T) {
		fetcher := New()
		_, err := fetcher.Get(context.Background(), GetRequest{
			URL: "https://example.com/page",
			NewRequest: func(context.Context, string, string, io.Reader) (*http.Request, error) {
				return nil, errors.New("request failed")
			},
		})
		require.EqualError(t, err, "request failed")
	})

	t.Run("Client returns error", func(t *testing.T) {
		fetcher := New(WithResolveHost(func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}))

		_, err := fetcher.Get(context.Background(), GetRequest{
			URL: "https://example.com/page",
			Client: &http.Client{
				Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return nil, errors.New("network failed")
				}),
			},
		})
		require.ErrorContains(t, err, "network failed")
	})
}

func TestFetcher_GetUsesDefaultClientAndFallsBackToParsedURL(t *testing.T) {
	server := http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("default-client"))
		}),
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = server.Close()
		_ = listener.Close()
	})
	go func() {
		_ = server.Serve(listener)
	}()

	fetcher := New(
		WithResolveHost(func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}),
		WithDial(func(ctx context.Context, network, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, listener.Addr().String())
		}),
	)

	resp, err := fetcher.Get(context.Background(), GetRequest{
		URL: "http://example.com/page",
	})
	require.NoError(t, err)
	require.Equal(t, "http://example.com/page", resp.FinalURL)
	require.Equal(t, []byte("default-client"), resp.Body)
}

func TestFetcher_GetHandlesReadResponseBranches(t *testing.T) {
	fetcher := New(WithResolveHost(func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}))

	t.Run("returns body, not truncated", func(t *testing.T) {
		resp, err := fetcher.Get(context.Background(), GetRequest{
			URL: "https://example.com/page",
			Client: &http.Client{
				Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Status:     "200 OK",
						Header:     http.Header{"Content-Type": []string{"text/plain"}},
						Body:       io.NopCloser(strings.NewReader("abcdef")),
						Request:    nil,
					}, nil
				}),
			},
		})
		require.NoError(t, err)
		require.Equal(t, "https://example.com/page", resp.FinalURL)
		require.Equal(t, []byte("abcdef"), resp.Body)
		require.False(t, resp.Truncated)
	})

	t.Run("max bytes = 1 with multibyte char truncates and returns empty", func(t *testing.T) {
		resp, err := fetcher.Get(context.Background(), GetRequest{
			URL:      "https://example.com/page",
			MaxBytes: 1,
			Client: &http.Client{
				Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Status:     "200 OK",
						Header:     http.Header{"Content-Type": []string{"text/plain"}},
						Body:       io.NopCloser(strings.NewReader("éclair")),
						Request:    req,
					}, nil
				}),
			},
		})
		require.NoError(t, err)
		require.Empty(t, resp.Body)
		require.True(t, resp.Truncated)
	})

	t.Run("read response returns error", func(t *testing.T) {
		_, err := fetcher.Get(context.Background(), GetRequest{
			URL:      "https://example.com/page",
			MaxBytes: 3,
			Client: &http.Client{
				Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Status:     "200 OK",
						Header:     http.Header{"Content-Type": []string{"text/plain"}},
						Body:       errReader{err: errors.New("read failed")},
						Request:    req,
					}, nil
				}),
			},
		})
		require.EqualError(t, err, "read failed")
	})
}

func TestFetcher_ResolveAndValidateHostUsesDefaultResolver(t *testing.T) {
	fetcher := New()

	addrs, err := fetcher.ResolveAndValidateHost(context.Background(), "localhost")
	require.Error(t, err)
	require.Nil(t, addrs)
}

func TestNetpolicySafeAddr(t *testing.T) {
	require.True(t, netpolicy.SafeAddr(netip.MustParseAddr("93.184.216.34"), nil))
}

func TestNew_AppliesOptionsAndClonesBlockedPrefixes(t *testing.T) {
	prefixes := []netip.Prefix{netip.MustParsePrefix("93.184.216.0/24")}
	resolveHost := func(context.Context, string) ([]netip.Addr, error) {
		return nil, nil
	}
	dial := func(context.Context, string, string) (net.Conn, error) {
		return nil, nil
	}
	policy := stubPolicy{}

	fetcher := New(
		nil,
		WithResolveHost(resolveHost),
		WithDial(dial),
		WithPolicy(policy),
		WithBlockedAddressPrefixes(prefixes),
	)

	require.NotNil(t, fetcher)
	require.NotNil(t, fetcher.ResolveHost)
	require.NotNil(t, fetcher.Dial)
	require.NotNil(t, fetcher.Policy)
	require.Equal(t, prefixes, fetcher.BlockedAddressPrefixes)

	prefixes[0] = netip.MustParsePrefix("8.8.8.0/24")
	require.NotEqual(t, prefixes, fetcher.BlockedAddressPrefixes)

	empty := New(WithBlockedAddressPrefixes(nil))
	require.Nil(t, empty.BlockedAddressPrefixes)
}

func TestOptions_HandleNilFetcher(t *testing.T) {
	require.NotPanics(t, func() {
		WithResolveHost(func(context.Context, string) ([]netip.Addr, error) {
			return nil, nil
		})(nil)
	})

	require.NotPanics(t, func() {
		WithDial(func(context.Context, string, string) (net.Conn, error) {
			return nil, nil
		})(nil)
	})

	require.NotPanics(t, func() {
		WithPolicy(stubPolicy{})(nil)
	})

	require.NotPanics(t, func() {
		WithBlockedAddressPrefixes([]netip.Prefix{
			netip.MustParsePrefix("93.184.216.0/24"),
		})(nil)
	})
}

func TestReadResponseBody(t *testing.T) {
	data, truncated, err := readResponseBody(strings.NewReader("abc"), 10)
	require.NoError(t, err)
	require.Equal(t, []byte("abc"), data)
	require.False(t, truncated)
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

func mustNewRequest(t *testing.T, rawURL string) *http.Request {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	require.NoError(t, err)

	return req
}
