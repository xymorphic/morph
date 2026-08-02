package browser

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/require"
	"github.com/xymorphic/morph/internal/permissions"
)

func TestEgressProxy_ForwardsPermittedHTTPAndRejectsRequestsWithoutAuthority(t *testing.T) {
	observed := make(chan *http.Request, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		observed <- request.Clone(context.Background())
		writer.Header().Set("X-Test", "forwarded")
		writer.Header().Set("Connection", "X-Remove")
		writer.Header().Set("X-Remove", "internal")
		_, _ = io.WriteString(writer, "ok")
	}))
	defer upstream.Close()

	permissive := NetworkPolicy{Strict: false}
	proxy, err := startEgressProxy(permissive)
	require.NoError(t, err)
	installHTTPProxyPermit(t, proxy, requestTarget(t, upstream.URL, http.MethodGet))
	proxyURL, err := url.Parse(proxy.URL())
	require.NoError(t, err)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, upstream.URL, nil)
	require.NoError(t, err)
	response, err := client.Do(request)
	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, "forwarded", response.Header.Get("X-Test"))
	require.Empty(t, response.Header.Get("X-Remove"))
	require.Equal(t, "ok", string(body))
	forwarded := <-observed
	require.Equal(t, request.URL.Host, forwarded.Host)
	require.Empty(t, forwarded.Header.Get("Proxy-Authorization"))
	proxy.setPolicy(NetworkPolicy{Strict: true})
	response, err = client.Do(request)
	require.NoError(t, err)
	require.Equal(t, http.StatusForbidden, response.StatusCode)
	require.NoError(t, response.Body.Close())
	unauthorizedURL := *proxyURL
	unauthorizedURL.User = nil
	unauthorizedClient := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(&unauthorizedURL)}}
	response, err = unauthorizedClient.Get(upstream.URL)
	require.NoError(t, err)
	require.Equal(t, http.StatusProxyAuthRequired, response.StatusCode)
	require.NoError(t, response.Body.Close())
	require.NoError(t, proxy.Close(context.Background()))
	require.NoError(t, proxy.Close(context.Background()))

	blocked, err := startEgressProxy(NetworkPolicy{Strict: true})
	require.NoError(t, err)
	blockedURL, err := url.Parse(blocked.URL())
	require.NoError(t, err)
	blockedClient := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(blockedURL)}}
	response, err = blockedClient.Get(upstream.URL)
	require.NoError(t, err)
	require.Equal(t, http.StatusForbidden, response.StatusCode)
	require.NoError(t, response.Body.Close())
	require.NoError(t, blocked.Close(context.Background()))
}

func TestEgressProxy_DialsPinnedAddressesWithoutResolvingAgain(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "pinned")
	}))
	defer upstream.Close()
	upstreamURL, err := url.Parse(upstream.URL)
	require.NoError(t, err)
	_, port, err := splitProxyAddress(upstreamURL.Host, 80)
	require.NoError(t, err)
	resolveCalls := 0
	policy := NetworkPolicy{
		Strict: false,
		ResolveHost: func(context.Context, string) ([]netip.Addr, error) {
			resolveCalls++
			if resolveCalls == 1 {
				return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
			}
			return []netip.Addr{netip.MustParseAddr("192.0.2.1")}, nil
		},
	}
	proxy, err := startEgressProxy(policy)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, proxy.Close(context.Background())) })
	targetURL := fmt.Sprintf("http://example.test:%d/news", port)
	proxyURL, err := url.Parse(proxy.URL())
	require.NoError(t, err)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	response, err := client.Get(targetURL)
	require.NoError(t, err)
	require.Equal(t, http.StatusForbidden, response.StatusCode)
	require.NoError(t, response.Body.Close())
	require.Zero(t, resolveCalls)
	installHTTPProxyPermit(t, proxy, requestTarget(t, targetURL, http.MethodGet))

	response, err = client.Get(targetURL)

	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, "pinned", string(body))
	require.Equal(t, 1, resolveCalls)
}

func TestEgressProxy_LogsSafeUpstreamFailureDetails(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())

	var output bytes.Buffer
	originalLogger := log.Logger
	log.Logger = zerolog.New(&output).Level(zerolog.DebugLevel)
	t.Cleanup(func() { log.Logger = originalLogger })
	proxy, err := startEgressProxy(NetworkPolicy{Strict: false})
	require.NoError(t, err)
	installHTTPProxyPermit(t, proxy, requestTarget(t, "http://"+address+"/news?token=secret", http.MethodGet))
	t.Cleanup(func() { require.NoError(t, proxy.Close(context.Background())) })
	proxyURL, err := url.Parse(proxy.URL())
	require.NoError(t, err)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

	response, err := client.Get("http://" + address + "/news?token=secret")

	require.NoError(t, err)
	require.Equal(t, http.StatusBadGateway, response.StatusCode)
	require.NoError(t, response.Body.Close())
	logOutput := output.String()
	require.Contains(t, logOutput, "Browser proxy request to the upstream target failed")
	require.Contains(t, logOutput, `"browser_network_stage":"proxy_upstream"`)
	require.Contains(t, logOutput, `"network_host":"127.0.0.1"`)
	require.Contains(t, logOutput, `"network_has_query":true`)
	require.NotContains(t, logOutput, "/news")
	require.Contains(t, logOutput, `"error":"network_operation_failed"`)
	require.NotContains(t, logOutput, "connection refused")
	require.NotContains(t, logOutput, "token")
	require.NotContains(t, logOutput, "secret")
}

func TestGetSafeBrowserNetworkError_ReturnsOnlyTypedFailures(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "cancelled", err: context.Canceled, want: "cancelled"},
		{name: "timeout", err: context.DeadlineExceeded, want: "timeout"},
		{name: "dns", err: &net.DNSError{Err: "secret.invalid", Name: "secret.example"}, want: "dns_failed"},
		{name: "operation", err: &net.OpError{Op: "dial", Err: errors.New("secret")}, want: "network_operation_failed"},
		{name: "URL", err: &url.Error{
			Op: "GET", URL: "https://example.com/secret", Err: context.Canceled,
		}, want: "cancelled"},
		{
			name: "permission", err: &permissions.DecisionError{Code: permissions.ErrorCodeDenied},
			want: permissions.ErrorCodeDenied,
		},
		{name: "other", err: errors.New("secret"), want: "network_error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, getSafeBrowserNetworkError(test.err))
		})
	}
}

func TestEgressProxy_RejectsMalformedAndUnpermittedConnectTargets(t *testing.T) {
	proxy, err := startEgressProxy(NetworkPolicy{Strict: true})
	require.NoError(t, err)
	tests := []struct {
		target string
		status int
	}{
		{target: "bad:port", status: http.StatusBadRequest},
		{target: "127.0.0.1:443", status: http.StatusForbidden},
	}
	for _, test := range tests {
		connection, dialErr := net.Dial("tcp", proxy.listener.Addr().String())
		require.NoError(t, dialErr)
		_, dialErr = fmt.Fprintf(
			connection, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: %s\r\n\r\n",
			test.target, test.target, proxy.authorization.header(),
		)
		require.NoError(t, dialErr)
		response, readErr := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodConnect})
		require.NoError(t, readErr)
		require.Equal(t, test.status, response.StatusCode)
		require.NoError(t, response.Body.Close())
		require.NoError(t, connection.Close())
	}
	require.NoError(t, proxy.Close(context.Background()))
}

func TestEgressProxy_BlocksWebSocketUpgradesWithoutPermit(t *testing.T) {
	proxy := &egressProxy{policy: NetworkPolicy{Strict: false}}
	request := httptest.NewRequest(http.MethodGet, "http://example.com/socket", nil)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	recorder := httptest.NewRecorder()

	proxy.handleHTTP(recorder, request)

	require.Equal(t, http.StatusForbidden, recorder.Code)
}

func TestEgressProxy_ForwardsPermittedWebSocketUpgrade(t *testing.T) {
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, upstream.Close()) })
	go func() {
		connection, acceptErr := upstream.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = connection.Close() }()
		reader := bufio.NewReader(connection)
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				return
			}
			if line == "\r\n" {
				break
			}
		}
		_, _ = io.WriteString(connection, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		_, _ = io.Copy(connection, reader)
	}()

	proxy, err := startEgressProxy(NetworkPolicy{Strict: false})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, proxy.Close(context.Background())) })
	rawURL := "ws://" + upstream.Addr().String() + "/socket"
	generation := installHTTPProxyPermit(t, proxy, requestTarget(t, rawURL, http.MethodGet))
	connection, err := net.Dial("tcp", proxy.listener.Addr().String())
	require.NoError(t, err)
	defer func() { _ = connection.Close() }()
	_, err = fmt.Fprintf(
		connection,
		"GET %s HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nProxy-Authorization: %s\r\n\r\n",
		rawURL, upstream.Addr(), proxy.authorization.header(),
	)
	require.NoError(t, err)
	reader := bufio.NewReader(connection)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, response.StatusCode)
	_, err = connection.Write([]byte("ping"))
	require.NoError(t, err)
	buffer := make([]byte, 4)
	_, err = io.ReadFull(reader, buffer)
	require.NoError(t, err)
	require.Equal(t, "ping", string(buffer))
	require.NoError(t, proxy.permits.revokeGeneration(generation))
	require.NoError(t, connection.SetReadDeadline(time.Now().Add(time.Second)))
	_, err = reader.ReadByte()
	require.Error(t, err)
}

func TestEgressProxy_TunnelsAllowedConnectTarget(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, echo.Close()) })
	go func() {
		connection, acceptErr := echo.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = connection.Close() }()
		_, _ = io.Copy(connection, connection)
	}()

	proxy, err := startEgressProxy(NetworkPolicy{Strict: false})
	require.NoError(t, err)
	installConnectProxyPermit(t, proxy, echo.Addr().String())
	connection, err := net.Dial("tcp", proxy.listener.Addr().String())
	require.NoError(t, err)
	defer func() { require.NoError(t, connection.Close()) }()
	_, err = fmt.Fprintf(
		connection,
		"CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: %s\r\n\r\nping",
		echo.Addr(), echo.Addr(), proxy.authorization.header(),
	)
	require.NoError(t, err)
	reader := bufio.NewReader(connection)
	status, err := reader.ReadString('\n')
	require.NoError(t, err)
	require.Contains(t, status, "200")
	for {
		line, readErr := reader.ReadString('\n')
		require.NoError(t, readErr)
		if line == "\r\n" {
			break
		}
	}
	response := make([]byte, 4)
	_, err = io.ReadFull(reader, response)
	require.NoError(t, err)
	require.Equal(t, "ping", string(response))
	_, err = connection.Write([]byte("pong"))
	require.NoError(t, err)
	_, err = io.ReadFull(reader, response)
	require.NoError(t, err)
	require.Equal(t, "pong", string(response))
	require.NoError(t, proxy.closeConnections())
	require.NoError(t, connection.SetReadDeadline(time.Now().Add(time.Second)))
	_, err = reader.ReadByte()
	require.Error(t, err)
	require.Eventually(t, func() bool {
		proxy.mu.Lock()
		defer proxy.mu.Unlock()
		return len(proxy.connections) == 0
	}, time.Second, 10*time.Millisecond)
	require.NoError(t, proxy.Close(context.Background()))
}

func TestEgressProxy_RequestsBackgroundAuthorityWhenNoPermitMatches(t *testing.T) {
	ledger := newTestTransportPermitLedger(t, time.Now)
	proxy := &egressProxy{permits: ledger}
	target := permissions.NetworkTarget{
		Scheme: "https", Host: "background.example", Port: 443, Path: "/", Method: http.MethodConnect,
		RequestClass: permissions.NetworkRequestSubresource,
	}
	var backgroundTargets []permissions.NetworkTarget
	proxy.background = func(
		_ context.Context,
		requested permissions.NetworkTarget,
	) (*transportPermitLease, error) {
		backgroundTargets = append(backgroundTargets, requested)
		require.Equal(t, permissions.NetworkRequestBackground, requested.RequestClass)
		return nil, errors.New("background denied")
	}

	_, err := proxy.acquirePermit(context.Background(), target)
	require.EqualError(t, err, "background denied")
	require.Equal(t, []permissions.NetworkTarget{{
		Scheme: "https", Host: "background.example", Port: 443, Path: "/", Method: http.MethodConnect,
		RequestClass: permissions.NetworkRequestBackground,
	}}, backgroundTargets)

	generation, err := ledger.beginGeneration(context.Background())
	require.NoError(t, err)
	require.NoError(t, ledger.install(generation, []transportPermitInput{{
		Target: target, Addresses: []netip.Addr{netip.MustParseAddr("192.0.2.1")}, Uses: 1,
		ExpiresAt: time.Now().Add(time.Minute),
	}}))
	for range defaultConnectDialBudget {
		lease, acquireErr := proxy.acquirePermit(context.Background(), target)
		require.NoError(t, acquireErr)
		lease.Release()
	}
	_, err = proxy.acquirePermit(context.Background(), target)
	requirePermitFailure(t, err, transportPermitExhausted)
	require.Len(t, backgroundTargets, 1)

	mismatched := target
	mismatched.Path = "/other"
	mismatched.Method = http.MethodGet
	_, err = proxy.acquirePermit(context.Background(), mismatched)
	requirePermitFailure(t, err, transportPermitExhausted)
	require.Len(t, backgroundTargets, 1)

	mismatched.Host = "other.example"
	_, err = proxy.acquirePermit(context.Background(), mismatched)
	require.EqualError(t, err, "background denied")
	require.Equal(t, "other.example", backgroundTargets[1].Host)
}

func TestEgressProxy_ReclassifiesPlainHTTPPermitMismatchOnlyThroughBackgroundAuthority(t *testing.T) {
	ledger := newTestTransportPermitLedger(t, time.Now)
	generation, err := ledger.beginGeneration(context.Background())
	require.NoError(t, err)
	allowed := permissions.NetworkTarget{
		Scheme: "http", Host: "example.test", Port: 80, Path: "/allowed", Method: http.MethodGet,
		RequestClass: permissions.NetworkRequestSubresource,
	}
	require.NoError(t, ledger.install(generation, []transportPermitInput{{
		Target: allowed, Addresses: []netip.Addr{netip.MustParseAddr("192.0.2.1")}, Uses: 1,
		ExpiresAt: time.Now().Add(time.Minute),
	}}))
	var backgroundTarget permissions.NetworkTarget
	proxy := &egressProxy{
		permits: ledger,
		background: func(
			_ context.Context,
			target permissions.NetworkTarget,
		) (*transportPermitLease, error) {
			backgroundTarget = target
			return nil, errors.New("background denied")
		},
	}
	mismatched := allowed
	mismatched.Path = "/different"

	_, err = proxy.acquirePermit(context.Background(), mismatched)

	require.EqualError(t, err, "background denied")
	require.Equal(t, permissions.NetworkRequestBackground, backgroundTarget.RequestClass)
	require.Equal(t, http.MethodConnect, backgroundTarget.Method)
	require.Equal(t, "/", backgroundTarget.Path)
	require.Equal(t, allowed.Host, backgroundTarget.Host)
	require.Equal(t, allowed.Port, backgroundTarget.Port)
}

func TestEgressProxy_WaitsForLogicalWebSocketAuthorityBeforeOpeningPhysicalTunnel(t *testing.T) {
	ledger := newTestTransportPermitLedger(t, time.Now)
	generation, err := ledger.beginGeneration(context.Background())
	require.NoError(t, err)
	proxy := &egressProxy{permits: ledger}
	logical := permissions.NetworkTarget{
		Scheme: "wss", Host: "socket.example", Port: 443, Path: "/events", Method: http.MethodConnect,
		RequestClass: permissions.NetworkRequestWebSocket,
	}
	physical := permissions.NetworkTarget{
		Scheme: "https", Host: "socket.example", Port: 443, Path: "/", Method: http.MethodConnect,
		RequestClass: permissions.NetworkRequestSubresource,
	}
	finish := ledger.beginPending(logical)
	result := make(chan struct {
		lease *transportPermitLease
		err   error
	}, 1)
	go func() {
		lease, acquireErr := proxy.acquirePermit(context.Background(), physical)
		result <- struct {
			lease *transportPermitLease
			err   error
		}{lease: lease, err: acquireErr}
	}()
	select {
	case <-result:
		t.Fatal("proxy did not wait for pending WebSocket authority")
	case <-time.After(20 * time.Millisecond):
	}

	require.NoError(t, ledger.install(generation, []transportPermitInput{{
		Target: logical, Addresses: []netip.Addr{netip.MustParseAddr("192.0.2.1")},
	}}))
	finish()
	acquired := <-result
	require.NoError(t, acquired.err)
	require.Equal(t, []netip.Addr{netip.MustParseAddr("192.0.2.1")}, acquired.lease.Addresses())
	acquired.lease.Release()
}

func TestEgressProxy_DeniedPendingWebSocketDoesNotFallBackToBackgroundAuthority(t *testing.T) {
	ledger := newTestTransportPermitLedger(t, time.Now)
	_, err := ledger.beginGeneration(context.Background())
	require.NoError(t, err)
	backgroundCalls := 0
	proxy := &egressProxy{
		permits: ledger,
		background: func(context.Context, permissions.NetworkTarget) (*transportPermitLease, error) {
			backgroundCalls++
			return nil, errors.New("unexpected background authorization")
		},
	}
	logical := permissions.NetworkTarget{
		Scheme: "ws", Host: "socket.example", Port: 80, Path: "/events", Method: http.MethodGet,
		RequestClass: permissions.NetworkRequestWebSocket,
	}
	physical := permissions.NetworkTarget{
		Scheme: "ws", Host: "socket.example", Port: 80, Path: "/events", Method: http.MethodGet,
		RequestClass: permissions.NetworkRequestWebSocket,
	}
	finish := ledger.beginPending(logical)
	result := make(chan error, 1)
	go func() {
		_, acquireErr := proxy.acquirePermit(context.Background(), physical)
		result <- acquireErr
	}()
	finish()

	requirePermitFailure(t, <-result, transportPermitMissing)
	require.Zero(t, backgroundCalls)
}

func TestEgressProxy_CancelledPendingWebSocketDoesNotFallBackToBackgroundAuthority(t *testing.T) {
	ledger := newTestTransportPermitLedger(t, time.Now)
	_, err := ledger.beginGeneration(context.Background())
	require.NoError(t, err)
	backgroundCalls := 0
	proxy := &egressProxy{
		permits: ledger,
		background: func(context.Context, permissions.NetworkTarget) (*transportPermitLease, error) {
			backgroundCalls++
			return nil, errors.New("unexpected background authorization")
		},
	}
	target := permissions.NetworkTarget{
		Scheme: "wss", Host: "socket.example", Port: 443, Path: "/events", Method: http.MethodConnect,
		RequestClass: permissions.NetworkRequestWebSocket,
	}
	finish := ledger.beginPending(target)
	defer finish()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, acquireErr := proxy.acquirePermit(ctx, target)
		result <- acquireErr
	}()
	cancel()

	require.ErrorIs(t, <-result, context.Canceled)
	require.Zero(t, backgroundCalls)
}

func TestEgressProxy_ClassifiesUnattributedPlainHTTPAsBackgroundConnection(t *testing.T) {
	proxy := &egressProxy{permits: newTestTransportPermitLedger(t, time.Now)}
	target := permissions.NetworkTarget{
		Scheme: "http", Host: "background.example", Port: 80, Path: "/telemetry", Method: http.MethodPost,
		RequestClass: permissions.NetworkRequestSubresource,
	}
	var observed permissions.NetworkTarget
	proxy.background = func(
		_ context.Context,
		requested permissions.NetworkTarget,
	) (*transportPermitLease, error) {
		observed = requested
		return nil, errors.New("background denied")
	}

	_, err := proxy.acquirePermit(context.Background(), target)
	require.EqualError(t, err, "background denied")
	require.Equal(t, "http", observed.Scheme)
	require.Equal(t, "background.example", observed.Host)
	require.Equal(t, uint16(80), observed.Port)
	require.Equal(t, "/", observed.Path)
	require.Equal(t, http.MethodConnect, observed.Method)
	require.Equal(t, permissions.NetworkRequestBackground, observed.RequestClass)
}

func TestEgressProxy_PolicyChangeRevokesPermitsAndConnections(t *testing.T) {
	ledger := newTestTransportPermitLedger(t, time.Now)
	proxy := &egressProxy{
		permits: ledger, policy: NetworkPolicy{Strict: false},
		policyKey: getNetworkPolicyKey(NetworkPolicy{Strict: false}),
	}
	target := requestTarget(t, "http://example.com/events", http.MethodGet)
	generation, err := ledger.beginGeneration(context.Background())
	require.NoError(t, err)
	require.NoError(t, ledger.install(generation, []transportPermitInput{{
		Target: target, Addresses: []netip.Addr{netip.MustParseAddr("192.0.2.1")},
		ExpiresAt: time.Now().Add(time.Minute),
	}}))
	lease, err := ledger.acquire(target)
	require.NoError(t, err)
	left, right := net.Pipe()
	t.Cleanup(func() { _ = right.Close() })
	require.NoError(t, lease.Attach(left))

	proxy.setPolicy(NetworkPolicy{Strict: true})

	require.Error(t, right.SetWriteDeadline(time.Now().Add(time.Second)))
	_, err = right.Write([]byte("closed"))
	require.Error(t, err)
	_, err = ledger.acquire(target)
	requirePermitFailure(t, err, transportPermitMissing)
	lease.Release()
}

func TestGetTransportPermitFailure_ClassifiesBackgroundDenials(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{err: errBackgroundAuthorityUnavailable, want: "no_live_authority"},
		{err: errBackgroundRuleRequired, want: "explicit_rule_required"},
		{err: errBackgroundNetworkPolicyDenied, want: "network_policy_denied"},
		{err: &permissions.DecisionError{Code: permissions.ErrorCodeApprovalRequired}, want: permissions.ErrorCodeApprovalRequired},
	}
	for _, test := range tests {
		require.Equal(t, test.want, getTransportPermitFailure(test.err))
	}
}

func TestSplitProxyAddress_DefaultsAndRejectsInvalidPorts(t *testing.T) {
	host, port, err := splitProxyAddress("example.com", 443)
	require.NoError(t, err)
	require.Equal(t, "example.com", host)
	require.Equal(t, uint16(443), port)
	host, port, err = splitProxyAddress("example.com:8443", 443)
	require.NoError(t, err)
	require.Equal(t, "example.com", host)
	require.Equal(t, uint16(8443), port)
	_, _, err = splitProxyAddress("example.com:invalid", 443)
	require.Error(t, err)
	require.Empty(t, (&egressProxy{}).URL())
	require.NoError(t, (*egressProxy)(nil).Close(context.Background()))
	require.NoError(t, (&egressProxy{}).Close(context.Background()))
}

func TestRemoveProxyHopHeaders_RemovesNamedAndStandardHeaders(t *testing.T) {
	header := http.Header{
		"Connection":          []string{"X-Internal, keep-alive"},
		"X-Internal":          []string{"secret"},
		"Proxy-Authorization": []string{"secret"},
		"Upgrade":             []string{"websocket"},
		"X-Keep":              []string{"value"},
	}
	removeProxyHopHeaders(header)
	require.Equal(t, http.Header{"X-Keep": []string{"value"}}, header)
}

func requestTarget(t *testing.T, raw, method string) permissions.NetworkTarget {
	t.Helper()
	target, err := permissions.NetworkTargetFromURL(raw, method, permissions.NetworkRequestSubresource)
	require.NoError(t, err)
	return target
}

func installHTTPProxyPermit(t *testing.T, proxy *egressProxy, target permissions.NetworkTarget) uint64 {
	t.Helper()
	addresses, err := proxy.getPolicy().Resolve(context.Background(), target)
	require.NoError(t, err)
	generation, err := proxy.permits.beginGeneration(context.Background())
	require.NoError(t, err)
	require.NoError(t, proxy.permits.install(generation, []transportPermitInput{{
		Target: target, Addresses: addresses, Uses: 1, ExpiresAt: time.Now().Add(time.Minute),
	}}))
	return generation
}

func installConnectProxyPermit(t *testing.T, proxy *egressProxy, address string) uint64 {
	t.Helper()
	host, port, err := splitProxyAddress(address, 443)
	require.NoError(t, err)
	parsedAddress, err := netip.ParseAddr(host)
	require.NoError(t, err)
	target := permissions.NetworkTarget{
		Scheme: "https", Host: host, Port: port, Path: "/", Method: http.MethodConnect,
		RequestClass: permissions.NetworkRequestSubresource,
	}
	generation, err := proxy.permits.beginGeneration(context.Background())
	require.NoError(t, err)
	require.NoError(t, proxy.permits.install(generation, []transportPermitInput{{
		Target: target, Addresses: []netip.Addr{parsedAddress}, Uses: 1, ExpiresAt: time.Now().Add(time.Minute),
	}}))
	return generation
}
