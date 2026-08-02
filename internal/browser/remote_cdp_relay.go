package browser

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/xymorphic/morph/internal/permissions"
)

const maxCDPDiscoveryResponseBytes = 1 << 20

type remoteCDPRelay struct {
	authorization         localAuthorization
	listener              net.Listener
	server                *http.Server
	transport             *http.Transport
	endpoint              *url.URL
	upstreamAuthorization string
	policyMu              sync.RWMutex
	policy                NetworkPolicy
	addresses             []net.IP
	mu                    sync.Mutex
	connections           map[net.Conn]struct{}
	closeOnce             sync.Once
	closeErr              error
}

func startRemoteCDPRelay(
	ctx context.Context,
	rawEndpoint string,
	upstreamAuthorization string,
	policy NetworkPolicy,
) (*remoteCDPRelay, error) {
	authorization, err := newLocalAuthorization()
	if err != nil {
		return nil, err
	}
	endpoint, err := url.Parse(strings.TrimSpace(rawEndpoint))
	if err != nil || endpoint.Hostname() == "" {
		return nil, errors.New("browser CDP endpoint is invalid")
	}
	target, err := permissions.NetworkTargetFromURL(rawEndpoint, http.MethodConnect, permissions.NetworkRequestCDP)
	if err != nil {
		return nil, err
	}
	resolved, err := policy.Resolve(ctx, target)
	if err != nil {
		return nil, err
	}
	addresses := make([]net.IP, 0, len(resolved))
	for _, address := range resolved {
		addresses = append(addresses, net.IP(address.AsSlice()))
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	relay := &remoteCDPRelay{
		authorization: authorization, listener: listener, endpoint: endpoint, policy: policy, addresses: addresses,
		upstreamAuthorization: upstreamAuthorization, connections: make(map[net.Conn]struct{}),
	}
	relay.transport = relay.getTransport(target.Port)
	proxy := &httputil.ReverseProxy{
		Director:       relay.directRequest,
		Transport:      relay.transport,
		ModifyResponse: relay.rewriteDiscoveryResponse,
		ErrorLog:       log.New(io.Discard, "", 0),
	}
	relay.server = &http.Server{
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if !relay.authorization.requireRelay(writer, request) {
				return
			}
			if _, resolveErr := relay.getPolicy().Resolve(request.Context(), target); resolveErr != nil {
				http.Error(writer, "blocked browser CDP endpoint", http.StatusForbidden)
				return
			}
			proxy.ServeHTTP(writer, request)
		}),
		ReadHeaderTimeout: defaultProxyReadHeaderTimeout,
		ConnState:         relay.updateConnectionState,
	}
	go func() {
		_ = relay.server.Serve(listener)
	}()

	return relay, nil
}

func (r *remoteCDPRelay) URL() string {
	if r == nil || r.listener == nil || r.endpoint == nil {
		return ""
	}
	scheme := "http"
	if r.endpoint.Scheme == "ws" || r.endpoint.Scheme == "wss" {
		scheme = "ws"
	}
	return (&url.URL{
		Scheme:   scheme,
		Host:     r.listener.Addr().String(),
		Path:     r.endpoint.Path,
		RawQuery: url.Values{localRelayTokenParameter: []string{r.authorization.password}}.Encode(),
	}).String()
}

func (r *remoteCDPRelay) setPolicy(policy NetworkPolicy) {
	if r == nil {
		return
	}
	r.policyMu.Lock()
	r.policy = policy
	r.policyMu.Unlock()
}

func (r *remoteCDPRelay) getPolicy() NetworkPolicy {
	r.policyMu.RLock()
	defer r.policyMu.RUnlock()

	return r.policy
}

func (r *remoteCDPRelay) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		if r.server != nil {
			r.closeErr = r.server.Shutdown(ctx)
			if r.closeErr != nil {
				r.closeErr = errors.Join(r.closeErr, r.server.Close())
			}
		}
		r.mu.Lock()
		connections := make([]net.Conn, 0, len(r.connections))
		for connection := range r.connections {
			connections = append(connections, connection)
		}
		r.mu.Unlock()
		for _, connection := range connections {
			if err := connection.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				r.closeErr = errors.Join(r.closeErr, err)
			}
		}
		if r.transport != nil {
			r.transport.CloseIdleConnections()
		}
	})

	return r.closeErr
}

func (r *remoteCDPRelay) updateConnectionState(connection net.Conn, state http.ConnState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if state == http.StateClosed || state == http.StateHijacked {
		if state == http.StateClosed {
			delete(r.connections, connection)
		}
		return
	}
	r.connections[connection] = struct{}{}
}

func (r *remoteCDPRelay) getTransport(port uint16) *http.Transport {
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialResolvedIPs(ctx, r.addresses, port)
		},
	}
	if r.endpoint.Scheme == "https" || r.endpoint.Scheme == "wss" {
		transport.TLSClientConfig = &tls.Config{ServerName: r.endpoint.Hostname(), MinVersion: tls.VersionTLS12}
	}

	return transport
}

func (r *remoteCDPRelay) directRequest(request *http.Request) {
	scheme := r.endpoint.Scheme
	switch scheme {
	case "ws":
		scheme = "http"
	case "wss":
		scheme = "https"
	}
	request.URL.Scheme = scheme
	request.URL.Host = r.endpoint.Host
	request.URL.User = r.endpoint.User
	request.Host = r.endpoint.Host
	request.Header.Del("Authorization")
	if r.upstreamAuthorization != "" {
		request.Header.Set("Authorization", r.upstreamAuthorization)
	}
}

func (r *remoteCDPRelay) rewriteDiscoveryResponse(response *http.Response) error {
	if response.StatusCode >= http.StatusMultipleChoices && response.StatusCode < http.StatusBadRequest {
		return errors.New("browser CDP endpoint redirects are not allowed")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxCDPDiscoveryResponseBytes+1))
	if err != nil {
		return err
	}
	_ = response.Body.Close()
	if len(body) > maxCDPDiscoveryResponseBytes {
		return errors.New("browser CDP discovery response is too large")
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return errors.New("browser CDP discovery returned invalid JSON")
	}
	if err := r.rewriteDiscoveryPayload(response.Request.Context(), payload); err != nil {
		return err
	}
	body, err = json.Marshal(payload)
	if err != nil {
		return err
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	response.ContentLength = int64(len(body))
	response.Header.Set("Content-Length", strconv.Itoa(len(body)))

	return nil
}

func (r *remoteCDPRelay) rewriteDiscoveryPayload(ctx context.Context, payload any) error {
	switch value := payload.(type) {
	case map[string]any:
		delete(value, "devtoolsFrontendUrl")
		rawWebSocket, exists := value["webSocketDebuggerUrl"]
		if !exists {
			return nil
		}
		webSocket, ok := rawWebSocket.(string)
		if !ok || strings.TrimSpace(webSocket) == "" {
			return errors.New("browser CDP discovery returned an invalid WebSocket endpoint")
		}
		rewritten, err := r.getLocalWebSocketURL(ctx, webSocket)
		if err != nil {
			return err
		}
		value["webSocketDebuggerUrl"] = rewritten
		return nil
	case []any:
		for _, item := range value {
			if _, ok := item.(map[string]any); !ok {
				return errors.New("browser CDP discovery returned an unsupported payload")
			}
			if err := r.rewriteDiscoveryPayload(ctx, item); err != nil {
				return err
			}
		}
		return nil
	default:
		return errors.New("browser CDP discovery returned an unsupported payload")
	}
}

func (r *remoteCDPRelay) getLocalWebSocketURL(ctx context.Context, raw string) (string, error) {
	endpoint, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || endpoint.Hostname() == "" || (endpoint.Scheme != "ws" && endpoint.Scheme != "wss") {
		return "", errors.New("browser CDP discovery returned an invalid WebSocket endpoint")
	}
	target, err := permissions.NetworkTargetFromURL(raw, http.MethodConnect, permissions.NetworkRequestCDP)
	if err != nil {
		return "", err
	}
	configured, err := permissions.NetworkTargetFromURL(
		r.endpoint.String(), http.MethodConnect, permissions.NetworkRequestCDP,
	)
	if err != nil {
		return "", err
	}
	if target.Host != configured.Host || target.Port != configured.Port ||
		!isMatchingCDPScheme(r.endpoint.Scheme, endpoint.Scheme) {
		return "", errors.New("browser CDP discovery changed endpoint origin")
	}
	if _, err := r.getPolicy().Resolve(ctx, target); err != nil {
		return "", err
	}

	query := endpoint.Query()
	query.Set(localRelayTokenParameter, r.authorization.password)
	return (&url.URL{
		Scheme:   "ws",
		Host:     r.listener.Addr().String(),
		Path:     endpoint.Path,
		RawQuery: query.Encode(),
	}).String(), nil
}

func isMatchingCDPScheme(configured, discovered string) bool {
	return configured == "http" && discovered == "ws" ||
		configured == "https" && discovered == "wss" ||
		configured == discovered
}

func dialResolvedIPs(ctx context.Context, addresses []net.IP, port uint16) (net.Conn, error) {
	var dialErrors []error
	dialer := net.Dialer{}
	for _, address := range addresses {
		endpoint := net.JoinHostPort(address.String(), strconv.Itoa(int(port)))
		connection, err := dialer.DialContext(ctx, "tcp", endpoint)
		if err == nil {
			return connection, nil
		}
		dialErrors = append(dialErrors, err)
	}

	return nil, errors.Join(dialErrors...)
}
