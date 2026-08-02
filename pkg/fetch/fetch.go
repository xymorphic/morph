package fetch

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"
	"unicode/utf8"

	"github.com/xymorphic/morph/pkg/netpolicy"
	"github.com/xymorphic/morph/pkg/str"
)

const defaultDialTimeout = 10 * time.Second

type ResolveHostFunc func(context.Context, string) ([]netip.Addr, error)

type DialFunc func(context.Context, string, string) (net.Conn, error)

type Policy interface {
	Check(context.Context, *url.URL) error
}

type Option func(*Fetcher)

type Fetcher struct {
	ResolveHost            ResolveHostFunc
	Dial                   DialFunc
	Policy                 Policy
	BlockedAddressPrefixes []netip.Prefix
}

func New(options ...Option) *Fetcher {
	fetcher := &Fetcher{}
	for _, option := range options {
		if option == nil {
			continue
		}

		option(fetcher)
	}

	return fetcher
}

func WithResolveHost(resolveHost ResolveHostFunc) Option {
	return func(fetcher *Fetcher) {
		if fetcher == nil {
			return
		}

		fetcher.ResolveHost = resolveHost
	}
}

func WithDial(dial DialFunc) Option {
	return func(fetcher *Fetcher) {
		if fetcher == nil {
			return
		}

		fetcher.Dial = dial
	}
}

func WithPolicy(policy Policy) Option {
	return func(fetcher *Fetcher) {
		if fetcher == nil {
			return
		}

		fetcher.Policy = policy
	}
}

func WithBlockedAddressPrefixes(prefixes []netip.Prefix) Option {
	return func(fetcher *Fetcher) {
		if fetcher == nil {
			return
		}
		if len(prefixes) == 0 {
			fetcher.BlockedAddressPrefixes = nil
			return
		}

		cloned := make([]netip.Prefix, len(prefixes))
		copy(cloned, prefixes)
		fetcher.BlockedAddressPrefixes = cloned
	}
}

type GetRequest struct {
	URL        string
	Header     http.Header
	Timeout    time.Duration
	MaxBytes   int
	Client     *http.Client
	NewRequest func(context.Context, string, string, io.Reader) (*http.Request, error)
}

type GetResponse struct {
	StatusCode int
	Status     string
	Header     http.Header
	Body       []byte
	FinalURL   string
	Truncated  bool
}

func (f *Fetcher) NewHTTPClient(timeout time.Duration) *http.Client {
	transport := &http.Transport{
		Proxy:                 nil,
		TLSHandshakeTimeout:   defaultDialTimeout,
		ResponseHeaderTimeout: 15 * time.Second,
		DialContext:           f.DialContext,
	}

	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}

			_, err := f.ValidateURL(req.Context(), req.URL.String())
			return err
		},
	}
}

func (f *Fetcher) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}

	addrs, err := f.ResolveAndValidateHost(ctx, host)
	if err != nil {
		return nil, err
	}

	dial := f.Dial
	if dial == nil {
		dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			dialer := net.Dialer{Timeout: defaultDialTimeout}
			return dialer.DialContext(ctx, network, address)
		}
	}

	var lastErr error
	for _, addr := range addrs {
		conn, err := dial(ctx, network, net.JoinHostPort(addr.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}

	return nil, lastErr
}

func (f *Fetcher) ValidateURL(ctx context.Context, rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("url scheme must be http or https")
	}
	hostnameValue := str.String(parsed.Hostname())
	if hostnameValue.Trim() == "" {
		return nil, errors.New("url host is required")
	}

	if parsed.User != nil {
		return nil, errors.New("url userinfo is not allowed")
	}

	if f.Policy != nil {
		if err := f.Policy.Check(ctx, parsed); err != nil {
			return nil, err
		}
	}

	if _, err := f.ResolveAndValidateHost(ctx, parsed.Hostname()); err != nil {
		return nil, err
	}

	return parsed, nil
}

func (f *Fetcher) ResolveAndValidateHost(ctx context.Context, host string) ([]netip.Addr, error) {
	hostValue := str.String(host)
	host = hostValue.Trim()
	if host == "" {
		return nil, errors.New("url host is required")
	}

	if addr, err := netip.ParseAddr(host); err == nil {
		if !netpolicy.SafeAddr(addr, f.BlockedAddressPrefixes) {
			return nil, errors.New("url host resolves to a blocked address")
		}

		return []netip.Addr{addr}, nil
	}

	resolveHost := f.ResolveHost
	if resolveHost == nil {
		resolveHost = func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		}
	}

	addrs, err := resolveHost(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, errors.New("url host resolved to no addresses")
	}
	for _, addr := range addrs {
		if !netpolicy.SafeAddr(addr, f.BlockedAddressPrefixes) {
			return nil, errors.New("url host resolves to a blocked address")
		}
	}

	return addrs, nil
}

func (f *Fetcher) Get(ctx context.Context, req GetRequest) (*GetResponse, error) {
	parsed, err := f.ValidateURL(ctx, req.URL)
	if err != nil {
		return nil, err
	}

	newRequest := req.NewRequest
	if newRequest == nil {
		newRequest = http.NewRequestWithContext
	}

	httpReq, err := newRequest(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}

	for key, values := range req.Header {
		for _, value := range values {
			httpReq.Header.Add(key, value)
		}
	}

	client := req.Client
	if client == nil {
		timeout := req.Timeout
		if timeout <= 0 {
			timeout = 15 * time.Second
		}
		client = f.NewHTTPClient(timeout)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	data, truncated, err := readResponseBody(resp.Body, req.MaxBytes)
	if err != nil {
		return nil, err
	}

	finalURL := parsed.String()
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}

	return &GetResponse{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Header:     resp.Header.Clone(),
		Body:       data,
		FinalURL:   finalURL,
		Truncated:  truncated,
	}, nil
}

func readResponseBody(body io.Reader, maxBytes int) ([]byte, bool, error) {
	if maxBytes <= 0 {
		data, err := io.ReadAll(body)
		return data, false, err
	}

	data, err := io.ReadAll(io.LimitReader(body, int64(maxBytes)+1))
	if err != nil {
		return nil, false, err
	}

	if len(data) > maxBytes {
		data = data[:maxBytes]
		for len(data) > 0 && !utf8.Valid(data) {
			data = data[:len(data)-1]
		}

		return data, true, nil
	}

	return data, false, nil
}
