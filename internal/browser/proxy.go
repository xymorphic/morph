package browser

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/xymorphic/morph/internal/permissions"
)

const defaultProxyReadHeaderTimeout = 10 * time.Second
const proxyDenialLogWindow = 5 * time.Second

type proxyDenial struct {
	last       time.Time
	suppressed int
}

type egressProxy struct {
	authorization localAuthorization
	sessionID     string
	permits       *transportPermitLedger
	background    func(context.Context, permissions.NetworkTarget) (*transportPermitLease, error)
	listener      net.Listener
	server        *http.Server
	policyMu      sync.RWMutex
	policy        NetworkPolicy
	policyKey     string
	mu            sync.Mutex
	closed        bool
	connections   map[net.Conn]struct{}
	denials       map[string]proxyDenial
}

func startEgressProxy(policy NetworkPolicy) (*egressProxy, error) {
	return startEgressProxyWithLedger(policy, nil)
}

func startEgressProxyWithLedger(
	policy NetworkPolicy,
	ledger *transportPermitLedger,
) (*egressProxy, error) {
	authorization, err := newLocalAuthorization()
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	if ledger == nil {
		ledger = newTransportPermitLedger(time.Now)
	}
	proxy := &egressProxy{
		authorization: authorization, policy: policy, listener: listener, connections: make(map[net.Conn]struct{}),
		permits: ledger, policyKey: getNetworkPolicyKey(policy), denials: make(map[string]proxyDenial),
	}
	proxy.server = &http.Server{
		Handler:           http.HandlerFunc(proxy.handle),
		ReadHeaderTimeout: defaultProxyReadHeaderTimeout,
		ErrorLog:          stdlog.New(io.Discard, "", 0),
	}
	go func() {
		_ = proxy.server.Serve(listener)
	}()

	return proxy, nil
}

func (p *egressProxy) URL() string {
	if p == nil || p.listener == nil {
		return ""
	}

	return (&url.URL{
		Scheme: "http",
		User:   p.authorization.userinfo(),
		Host:   p.listener.Addr().String(),
	}).String()
}

func (p *egressProxy) chromiumURL() string {
	if p == nil || p.listener == nil {
		return ""
	}

	return (&url.URL{Scheme: "http", Host: p.listener.Addr().String()}).String()
}

func (p *egressProxy) setPolicy(policy NetworkPolicy) {
	if p == nil {
		return
	}
	policyKey := getNetworkPolicyKey(policy)
	p.policyMu.Lock()
	changed := p.policyKey != policyKey
	p.policy = policy
	p.policyKey = policyKey
	p.policyMu.Unlock()
	if changed {
		_ = p.permits.invalidate()
		log.Debug().
			Str("browser_session_id", p.sessionID).
			Msg("Browser proxy invalidated transport authority after a network policy change")
	}
}

func (p *egressProxy) getPolicy() NetworkPolicy {
	p.policyMu.RLock()
	defer p.policyMu.RUnlock()

	return p.policy
}

func getNetworkPolicyKey(policy NetworkPolicy) string {
	values := []string{strconv.FormatBool(policy.Strict)}
	values = append(values, policy.AllowedHosts...)
	for _, prefix := range policy.AllowedCIDRs {
		values = append(values, prefix.String())
	}
	return strings.Join(values, "\x00")
}

func (p *egressProxy) Close(ctx context.Context) error {
	if p == nil || p.server == nil {
		return nil
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()

	shutdownErr := p.server.Shutdown(ctx)
	if shutdownErr != nil {
		shutdownErr = errors.Join(shutdownErr, p.server.Close())
	}
	return errors.Join(shutdownErr, p.closeConnections(), p.permits.close())
}

func (p *egressProxy) closeConnections() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	connections := make([]net.Conn, 0, len(p.connections))
	for connection := range p.connections {
		connections = append(connections, connection)
	}
	p.mu.Unlock()
	var closeErrors []error
	for _, connection := range connections {
		if err := connection.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			closeErrors = append(closeErrors, err)
		}
	}

	return errors.Join(closeErrors...)
}

func (p *egressProxy) handle(writer http.ResponseWriter, request *http.Request) {
	if !p.authorization.requireProxy(writer, request) {
		return
	}
	if request.Method == http.MethodConnect {
		p.handleConnect(writer, request)
		return
	}
	p.handleHTTP(writer, request)
}

func (p *egressProxy) handleConnect(writer http.ResponseWriter, request *http.Request) {
	host, port, err := splitProxyAddress(request.Host, 443)
	if err != nil {
		log.Warn().
			Str("browser_network_stage", "proxy_connect_validation").
			Str("network_method", http.MethodConnect).
			Str("error", getSafeBrowserNetworkError(err)).
			Msg("Browser proxy rejected an invalid CONNECT target")
		http.Error(writer, "invalid proxy target", http.StatusBadRequest)
		return
	}
	target := permissions.NetworkTarget{
		Scheme: "https", Host: host, Port: port, Path: "/", Method: http.MethodConnect,
		RequestClass: permissions.NetworkRequestSubresource,
	}
	lease, err := p.acquirePermit(request.Context(), target)
	if err != nil {
		p.logPermitDenial(target, err, "Browser proxy denied CONNECT without transport authority")
		http.Error(writer, "blocked proxy target", http.StatusForbidden)
		return
	}
	releaseLease := true
	defer func() {
		if releaseLease {
			lease.Release()
		}
	}()
	upstream, err := dialResolvedTarget(request.Context(), lease.Addresses(), target.Port)
	if err != nil {
		addBrowserNetworkLogFields(log.Warn(), target).
			Str("browser_network_stage", "proxy_connect").
			Str("error", getSafeBrowserNetworkError(err)).
			Msg("Browser proxy blocked or failed to connect to an upstream target")
		http.Error(writer, "blocked proxy target", http.StatusForbidden)
		return
	}
	if err := lease.Attach(upstream); err != nil {
		_ = upstream.Close()
		http.Error(writer, "blocked proxy target", http.StatusForbidden)
		return
	}
	hijacker, ok := writer.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		http.Error(writer, "proxy transport unavailable", http.StatusInternalServerError)
		return
	}
	client, buffer, err := hijacker.Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	if err := lease.Attach(client); err != nil {
		_ = client.Close()
		_ = upstream.Close()
		return
	}
	_, _ = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
	if err := buffer.Flush(); err != nil {
		addBrowserNetworkLogFields(log.Warn(), target).
			Str("browser_network_stage", "proxy_connect_response").
			Str("error", getSafeBrowserNetworkError(err)).
			Msg("Browser proxy failed to establish a CONNECT tunnel")
		_ = client.Close()
		_ = upstream.Close()
		return
	}
	if buffered := buffer.Reader.Buffered(); buffered > 0 {
		if _, err := io.CopyN(upstream, buffer.Reader, int64(buffered)); err != nil {
			_ = client.Close()
			_ = upstream.Close()
			return
		}
	}
	if !p.trackConnections(client, upstream) {
		_ = client.Close()
		_ = upstream.Close()
		return
	}
	addBrowserNetworkLogFields(log.Debug(), target).
		Str("browser_network_stage", "proxy_connect").
		Msg("Browser proxy established an upstream tunnel")
	go func() {
		proxyTunnel(client, upstream)
		p.untrackConnections(client, upstream)
		lease.Release()
		addBrowserNetworkLogFields(log.Debug(), target).
			Str("browser_session_id", p.sessionID).
			Str("browser_network_stage", "proxy_connect").
			Msg("Browser proxy closed an upstream tunnel")
	}()
	releaseLease = false
}

func (p *egressProxy) handleHTTP(writer http.ResponseWriter, request *http.Request) {
	webSocket := strings.EqualFold(strings.TrimSpace(request.Header.Get("Upgrade")), "websocket")
	if request.URL == nil || request.URL.Hostname() == "" {
		log.Warn().
			Str("browser_network_stage", "proxy_http_validation").
			Str("network_method", request.Method).
			Msg("Browser proxy rejected an invalid HTTP target")
		http.Error(writer, "invalid proxy target", http.StatusBadRequest)
		return
	}
	target, err := permissions.NetworkTargetFromURL(
		request.URL.String(), request.Method, permissions.NetworkRequestSubresource,
	)
	if err != nil {
		log.Warn().
			Str("browser_network_stage", "proxy_http_validation").
			Str("network_method", request.Method).
			Str("error", getSafeBrowserNetworkError(err)).
			Msg("Browser proxy rejected an invalid HTTP target")
		http.Error(writer, "invalid proxy target", http.StatusBadRequest)
		return
	}
	if webSocket {
		target.Scheme = "ws"
		target.RequestClass = permissions.NetworkRequestWebSocket
	}
	lease, err := p.acquirePermit(request.Context(), target)
	if err != nil {
		p.logPermitDenial(target, err, "Browser proxy denied HTTP without transport authority")
		http.Error(writer, "blocked proxy target", http.StatusForbidden)
		return
	}
	releaseLease := true
	defer func() {
		if releaseLease {
			lease.Release()
		}
	}()
	var upstream net.Conn
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			connection, dialErr := dialResolvedTarget(ctx, lease.Addresses(), target.Port)
			if dialErr != nil {
				return nil, dialErr
			}
			if attachErr := lease.Attach(connection); attachErr != nil {
				_ = connection.Close()
				return nil, attachErr
			}
			upstream = connection
			return connection, nil
		},
	}
	defer transport.CloseIdleConnections()
	forward := request.Clone(request.Context())
	forward.RequestURI = ""
	forward.Host = request.URL.Host
	if webSocket {
		forward.URL.Scheme = "http"
		forward.Header.Del("Proxy-Authorization")
		forward.Header.Del("Proxy-Connection")
	} else {
		removeProxyHopHeaders(forward.Header)
	}
	response, err := transport.RoundTrip(forward)
	if err != nil {
		addBrowserNetworkLogFields(log.Warn(), target).
			Str("browser_network_stage", "proxy_upstream").
			Str("error", getSafeBrowserNetworkError(err)).
			Msg("Browser proxy request to the upstream target failed")
		http.Error(writer, "proxy request failed", http.StatusBadGateway)
		return
	}
	if webSocket && response.StatusCode == http.StatusSwitchingProtocols {
		stream, ok := response.Body.(io.ReadWriteCloser)
		if !ok || upstream == nil {
			_ = response.Body.Close()
			http.Error(writer, "proxy transport unavailable", http.StatusBadGateway)
			return
		}
		hijacker, ok := writer.(http.Hijacker)
		if !ok {
			_ = response.Body.Close()
			http.Error(writer, "proxy transport unavailable", http.StatusInternalServerError)
			return
		}
		client, buffer, hijackErr := hijacker.Hijack()
		if hijackErr != nil {
			_ = response.Body.Close()
			return
		}
		if err := lease.Attach(client); err != nil {
			_ = client.Close()
			_ = response.Body.Close()
			return
		}
		_, _ = fmt.Fprintf(buffer, "HTTP/1.1 %d %s\r\n", response.StatusCode, http.StatusText(response.StatusCode))
		_ = response.Header.Write(buffer)
		_, _ = buffer.WriteString("\r\n")
		if err := buffer.Flush(); err != nil {
			_ = client.Close()
			_ = response.Body.Close()
			return
		}
		if !p.trackConnections(client, upstream) {
			_ = client.Close()
			_ = response.Body.Close()
			return
		}
		go func() {
			proxyUpgrade(client, stream)
			p.untrackConnections(client, upstream)
			lease.Release()
			addBrowserNetworkLogFields(log.Debug(), target).
				Str("browser_session_id", p.sessionID).
				Str("browser_network_stage", "proxy_websocket").
				Msg("Browser proxy closed an upstream WebSocket")
		}()
		releaseLease = false
		return
	}
	defer func() { _ = response.Body.Close() }()
	copyProxyHeaders(writer.Header(), response.Header)
	writer.WriteHeader(response.StatusCode)
	_, _ = io.Copy(writer, response.Body)
	addBrowserNetworkLogFields(log.Debug(), target).
		Str("browser_network_stage", "proxy_upstream").
		Int("http_status", response.StatusCode).
		Msg("Browser proxy request completed")
}

func getTransportPermitFailure(err error) string {
	var permitErr *transportPermitError
	if errors.As(err, &permitErr) {
		return string(permitErr.Failure)
	}
	if errors.Is(err, errBackgroundAuthorityUnavailable) {
		return "no_live_authority"
	}
	if errors.Is(err, errBackgroundRuleRequired) {
		return "explicit_rule_required"
	}
	if errors.Is(err, errBackgroundNetworkPolicyDenied) {
		return "network_policy_denied"
	}
	if decision, ok := permissions.GetDecisionError(err); ok {
		return decision.Code
	}
	return "background_denied"
}

func (p *egressProxy) logPermitDenial(target permissions.NetworkTarget, err error, message string) {
	failure := getTransportPermitFailure(err)
	key := target.Scheme + "\x00" + target.Host + "\x00" + strconv.Itoa(int(target.Port)) + "\x00" + failure
	now := time.Now()
	p.mu.Lock()
	if p.denials == nil {
		p.denials = make(map[string]proxyDenial)
	}
	denial := p.denials[key]
	if !denial.last.IsZero() && now.Sub(denial.last) < proxyDenialLogWindow {
		denial.suppressed++
		p.denials[key] = denial
		p.mu.Unlock()
		return
	}
	suppressed := denial.suppressed
	p.denials[key] = proxyDenial{last: now}
	p.mu.Unlock()
	addBrowserNetworkLogFields(log.Warn(), target).
		Str("browser_session_id", p.sessionID).
		Str("browser_network_stage", "proxy_permit").
		Str("transport_permit_failure", failure).
		Int("suppressed_attempts", suppressed).
		Msg(message)
}

func (p *egressProxy) acquirePermit(
	ctx context.Context,
	target permissions.NetworkTarget,
) (*transportPermitLease, error) {
	lease, err := p.permits.acquire(target)
	waitedForPending := false
	for err != nil {
		waited, waitErr := p.permits.waitForPending(ctx, target)
		if waitErr != nil {
			return nil, waitErr
		}
		if !waited {
			break
		}
		waitedForPending = true
		lease, err = p.permits.acquire(target)
		if err == nil {
			addBrowserNetworkLogFields(log.Debug(), target).
				Str("browser_session_id", p.sessionID).
				Uint64("transport_permit_generation", lease.generation).
				Msg("Browser proxy acquired pending transport authority")
		}
	}
	if waitedForPending {
		return lease, err
	}
	var permitErr *transportPermitError
	if err == nil || !errors.As(err, &permitErr) || !isBackgroundEligiblePermitFailure(permitErr.Failure) ||
		p.background == nil {
		if err == nil {
			addBrowserNetworkLogFields(log.Debug(), target).
				Str("browser_session_id", p.sessionID).
				Uint64("transport_permit_generation", lease.generation).
				Msg("Browser proxy acquired transport authority")
		}
		return lease, err
	}
	backgroundTarget := target
	backgroundTarget.Path = "/"
	backgroundTarget.QueryHash = ""
	backgroundTarget.Method = http.MethodConnect
	backgroundTarget.RequestClass = permissions.NetworkRequestBackground
	lease, err = p.background(ctx, backgroundTarget)
	if err == nil {
		addBrowserNetworkLogFields(log.Debug(), target).
			Str("browser_session_id", p.sessionID).
			Uint64("transport_permit_generation", lease.generation).
			Msg("Browser proxy acquired background transport authority")
	}
	return lease, err
}

func isBackgroundEligiblePermitFailure(failure transportPermitFailure) bool {
	return failure == transportPermitMissing || failure == transportPermitMismatch
}

func addBrowserNetworkLogFields(event *zerolog.Event, target permissions.NetworkTarget) *zerolog.Event {
	return event.
		Str("network_scheme", target.Scheme).
		Str("network_host", target.Host).
		Uint16("network_port", target.Port).
		Str("network_method", target.Method).
		Str("network_request_class", string(target.RequestClass)).
		Bool("network_has_query", target.QueryHash != "")
}

func getSafeBrowserNetworkError(err error) string {
	var urlError *url.Error
	if errors.As(err, &urlError) && urlError.Err != nil {
		return getSafeBrowserNetworkError(urlError.Err)
	}
	if decision, ok := permissions.GetDecisionError(err); ok {
		return decision.Code
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) {
		return "dns_failed"
	}
	var operationError *net.OpError
	if errors.As(err, &operationError) {
		return "network_operation_failed"
	}
	return "network_error"
}

func (p *egressProxy) trackConnections(connections ...net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	for _, connection := range connections {
		p.connections[connection] = struct{}{}
	}

	return true
}

func (p *egressProxy) untrackConnections(connections ...net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, connection := range connections {
		delete(p.connections, connection)
	}
}

func dialResolvedTarget(ctx context.Context, addresses []netip.Addr, port uint16) (net.Conn, error) {
	values := make([]net.IP, 0, len(addresses))
	for _, address := range addresses {
		values = append(values, net.IP(address.AsSlice()))
	}
	return dialResolvedIPs(ctx, values, port)
}

func splitProxyAddress(raw string, defaultPort uint16) (string, uint16, error) {
	host, portText, err := net.SplitHostPort(raw)
	if err != nil {
		parsed, parseErr := url.Parse("https://" + raw)
		if parseErr != nil || parsed.Hostname() == "" || parsed.Port() != "" {
			return "", 0, err
		}
		return parsed.Hostname(), defaultPort, nil
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return "", 0, fmt.Errorf("invalid proxy port")
	}

	return host, uint16(port), nil
}

func copyProxyHeaders(destination http.Header, source http.Header) {
	removeProxyHopHeaders(source)
	for key, values := range source {
		for _, value := range values {
			destination.Add(key, value)
		}
	}
}

func removeProxyHopHeaders(header http.Header) {
	for _, value := range header.Values("Connection") {
		for token := range strings.SplitSeq(value, ",") {
			header.Del(strings.TrimSpace(token))
		}
	}
	for _, name := range []string{
		"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Proxy-Connection", "Te",
		"Trailer", "Transfer-Encoding", "Upgrade",
	} {
		header.Del(name)
	}
}

func proxyTunnel(left, right net.Conn) {
	defer func() { _ = left.Close() }()
	defer func() { _ = right.Close() }()
	done := make(chan struct{}, 2)
	copyConnection := func(destination net.Conn, source net.Conn) {
		_, _ = io.Copy(destination, bufio.NewReader(source))
		done <- struct{}{}
	}
	go copyConnection(left, right)
	go copyConnection(right, left)
	<-done
}

func proxyUpgrade(left net.Conn, right io.ReadWriteCloser) {
	defer func() { _ = left.Close() }()
	defer func() { _ = right.Close() }()
	done := make(chan struct{}, 2)
	copyConnection := func(destination io.Writer, source io.Reader) {
		_, _ = io.Copy(destination, source)
		done <- struct{}{}
	}
	go copyConnection(left, right)
	go copyConnection(right, left)
	<-done
}
