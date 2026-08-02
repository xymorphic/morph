package slack

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/websocket"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/gateway/dispatch"
	pkgslack "github.com/xymorphic/morph/pkg/gateway/slack"
)

func TestStartSocketWithClient_DispatchesSocketEvents(t *testing.T) {
	stubSocketReconnectSleep(t, nil)
	origNewSlackAPI := newSlackAPI
	t.Cleanup(func() { newSlackAPI = origNewSlackAPI })
	api := &fakeSlackAPI{}
	newSlackAPI = func(config.GatewaySlackConfig) API { return api }
	service := newSlackServiceStub()
	cfg := slackGatewayConfig()
	cfg.Slack.Mode = config.GatewaySlackModeSocket
	cfg.AllowedUsers = []string{"U1"}
	ctx, cancel := context.WithCancel(context.Background())
	client := &fakeSocketClient{
		run: func(ctx context.Context, handler func(context.Context, pkgslack.SocketEnvelope) error) error {
			err := handler(ctx, slackSocketEnvelope(t))
			require.Eventually(t, func() bool {
				return service.callCount() == 1
			}, time.Second, 10*time.Millisecond)
			cancel()
			return err
		},
	}

	err := StartSocketWithClient(ctx, cfg, service, client)

	require.NoError(t, err)
	require.Equal(t, 1, service.callCount())
	require.Len(t, api.allCalls(), 3)
}

func TestStartSocketWithClient_DeduplicatesSocketEvents(t *testing.T) {
	stubSocketReconnectSleep(t, nil)
	origNewSlackAPI := newSlackAPI
	t.Cleanup(func() { newSlackAPI = origNewSlackAPI })
	api := &fakeSlackAPI{}
	newSlackAPI = func(config.GatewaySlackConfig) API { return api }
	service := newSlackServiceStub()
	cfg := slackGatewayConfig()
	cfg.Slack.Mode = config.GatewaySlackModeSocket
	cfg.AllowedUsers = []string{"U1"}
	ctx, cancel := context.WithCancel(context.Background())
	client := &fakeSocketClient{
		run: func(ctx context.Context, handler func(context.Context, pkgslack.SocketEnvelope) error) error {
			envelope := slackSocketEnvelope(t)
			require.NoError(t, handler(ctx, envelope))
			require.NoError(t, handler(ctx, envelope))
			require.Eventually(t, func() bool {
				return service.callCount() == 1
			}, time.Second, 10*time.Millisecond)
			cancel()
			return nil
		},
	}

	err := StartSocketWithClient(ctx, cfg, service, client)

	require.NoError(t, err)
	require.Equal(t, 1, service.callCount())
	require.Len(t, api.allCalls(), 3)
}

func TestStartSocket_WaitsWhenDisabled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- StartSocket(ctx, config.GatewayConfig{}, newSlackServiceStub())
	}()

	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("disabled socket did not stop after context cancellation")
	}
}

func TestStartSocketWithClient_WaitsWhenDisabled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- StartSocketWithClient(ctx, config.GatewayConfig{}, newSlackServiceStub(), &fakeSocketClient{})
	}()

	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("disabled socket did not stop after context cancellation")
	}
}

func TestNewSocketClientSetsDefaults(t *testing.T) {
	client := newSocketClient(" xapp-token ")

	require.Equal(t, "xapp-token", client.appToken)
	require.Equal(t, defaultSlackAPIBase, client.baseURL)
	require.NotNil(t, client.http)
	require.NotNil(t, client.dial)
	_, err := client.dial("://bad")
	require.Error(t, err)

	server := httptest.NewServer(websocket.Handler(func(conn *websocket.Conn) {
		_ = conn.Close()
	}))
	defer server.Close()

	conn, err := client.dial("ws" + strings.TrimPrefix(server.URL, "http"))
	require.NoError(t, err)
	require.NoError(t, conn.Close())
}

func TestStartSocketWithClient_RequiresDependencies(t *testing.T) {
	cfg := slackGatewayConfig()
	cfg.Slack.Mode = config.GatewaySlackModeSocket

	require.EqualError(t, StartSocketWithClient(context.Background(), cfg, nil, &fakeSocketClient{}),
		"slack gateway service is required")
	require.EqualError(t, StartSocketWithClient(context.Background(), cfg, newSlackServiceStub(), nil),
		"slack socket client is required")
}

func TestStartSocketWithClient_StopsWhenReconnectSleepIsCanceled(t *testing.T) {
	stubSocketReconnectSleep(t, nil)
	cfg := slackGatewayConfig()
	cfg.Slack.Mode = config.GatewaySlackModeSocket
	ctx, cancel := context.WithCancel(context.Background())
	client := &fakeSocketClient{
		run: func(ctx context.Context, handler func(context.Context, pkgslack.SocketEnvelope) error) error {
			cancel()
			return errSlackTest
		},
	}

	err := StartSocketWithClient(ctx, cfg, newSlackServiceStub(), client)

	require.NoError(t, err)
}

func TestStartSocketWithClient_ReconnectsAfterClientError(t *testing.T) {
	var delays []time.Duration
	stubSocketReconnectSleep(t, &delays)
	cfg := slackGatewayConfig()
	cfg.Slack.Mode = config.GatewaySlackModeSocket
	ctx, cancel := context.WithCancel(context.Background())
	runs := 0
	client := &fakeSocketClient{
		run: func(ctx context.Context, handler func(context.Context, pkgslack.SocketEnvelope) error) error {
			runs++
			if runs == 1 {
				return errSlackTest
			}
			cancel()
			return nil
		},
	}

	err := StartSocketWithClient(ctx, cfg, newSlackServiceStub(), client)

	require.NoError(t, err)
	require.Equal(t, 2, runs)
	require.Equal(t, []time.Duration{defaultSocketReconnectBaseDelay}, delays)
}

func TestStartSocketWithClient_TreatsNormalizeErrorAsReconnectable(t *testing.T) {
	stubSocketReconnectSleep(t, nil)
	cfg := slackGatewayConfig()
	cfg.Slack.Mode = config.GatewaySlackModeSocket
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &fakeSocketClient{
		run: func(ctx context.Context, handler func(context.Context, pkgslack.SocketEnvelope) error) error {
			err := handler(ctx, pkgslack.SocketEnvelope{Type: "events_api", Payload: []byte(`not json`)})
			cancel()
			return err
		},
	}

	err := StartSocketWithClient(ctx, cfg, newSlackServiceStub(), client)

	require.NoError(t, err)
}

func TestStartSocketWithClient_SleepStopsWhenContextCancelsAfterRunError(t *testing.T) {
	origSleep := sleepSlackSocketReconnect
	sleepSlackSocketReconnect = func(ctx context.Context, delay time.Duration) bool {
		<-ctx.Done()
		return false
	}
	t.Cleanup(func() {
		sleepSlackSocketReconnect = origSleep
	})

	cfg := slackGatewayConfig()
	cfg.Slack.Mode = config.GatewaySlackModeSocket
	ctx, cancel := context.WithCancel(context.Background())
	client := &fakeSocketClient{
		run: func(ctx context.Context, handler func(context.Context, pkgslack.SocketEnvelope) error) error {
			go func() {
				time.Sleep(10 * time.Millisecond)
				cancel()
			}()
			return errSlackTest
		},
	}

	err := StartSocketWithClient(ctx, cfg, newSlackServiceStub(), client)

	require.NoError(t, err)
}

func TestStartSocketWithClient_ReconnectsWithBackoff(t *testing.T) {
	var delays []time.Duration
	stubSocketReconnectSleep(t, &delays)
	cfg := slackGatewayConfig()
	cfg.Slack.Mode = config.GatewaySlackModeSocket
	ctx, cancel := context.WithCancel(context.Background())
	runs := 0
	client := &fakeSocketClient{
		run: func(ctx context.Context, handler func(context.Context, pkgslack.SocketEnvelope) error) error {
			runs++
			if runs <= 3 {
				return errSlackTest
			}
			cancel()
			return nil
		},
	}

	err := StartSocketWithClient(ctx, cfg, newSlackServiceStub(), client)

	require.NoError(t, err)
	require.Equal(t, 4, runs)
	require.Equal(t, []time.Duration{
		defaultSocketReconnectBaseDelay,
		2 * defaultSocketReconnectBaseDelay,
		4 * defaultSocketReconnectBaseDelay,
	}, delays)
}

func TestStartSocketWithClient_RequiresDispatcher(t *testing.T) {
	err := startSocketWithClient(
		context.Background(),
		slackGatewayConfig(),
		newSlackServiceStub(),
		&fakeSocketClient{},
		nil,
	)

	require.EqualError(t, err, "slack socket dispatcher is required")
}

func TestStartSocketWithClient_IgnoresNonEventEnvelopes(t *testing.T) {
	origSleep := sleepSlackSocketReconnect
	sleepSlackSocketReconnect = func(context.Context, time.Duration) bool {
		return false
	}
	t.Cleanup(func() {
		sleepSlackSocketReconnect = origSleep
	})

	service := newSlackServiceStub()
	cfg := slackGatewayConfig()
	cfg.Slack.Mode = config.GatewaySlackModeSocket
	dispatcher := dispatch.New(dispatch.Options{})
	dispatcher.Start(context.Background())
	t.Cleanup(dispatcher.Close)
	client := &fakeSocketClient{
		run: func(ctx context.Context, handler func(context.Context, pkgslack.SocketEnvelope) error) error {
			require.NoError(t, handler(ctx, pkgslack.SocketEnvelope{Type: "hello"}))
			return nil
		},
	}

	err := startSocketWithClient(context.Background(), cfg, service, client, dispatcher)

	require.NoError(t, err)
	require.Zero(t, service.callCount())
}

func TestSocketClient_RunReturnsInvalidEnvelopeAndHandlerErrors(t *testing.T) {
	tests := []struct {
		name    string
		message []byte
		handler func(context.Context, pkgslack.SocketEnvelope) error
	}{
		{name: "invalid json", message: []byte(`not json`)},
		{name: "handler error", message: slackSocketEnvelopeBytes(t), handler: func(context.Context, pkgslack.SocketEnvelope) error {
			return errSlackTest
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := socketClientWithConn(t, newFakeSocketConn(tt.message))

			err := client.Run(context.Background(), tt.handler)

			require.Error(t, err)
		})
	}
}

func TestSocketClient_RunReturnsReadError(t *testing.T) {
	conn := newFakeSocketConn(nil)
	conn.readErr = errSlackTest
	client := socketClientWithConn(t, conn)

	err := client.Run(context.Background(), nil)

	require.ErrorIs(t, err, errSlackTest)
}

func TestSocketClient_RunSkipsEmptyReads(t *testing.T) {
	conn := newFakeSocketConn(slackSocketEnvelopeBytes(t))
	conn.emptyReads = 1
	client := socketClientWithConn(t, conn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := client.Run(ctx, func(ctx context.Context, envelope pkgslack.SocketEnvelope) error {
		require.Equal(t, "env1", envelope.EnvelopeID)
		cancel()
		return nil
	})

	require.NoError(t, err)
}

func TestSocketClient_RunSkipsWhitespaceFrames(t *testing.T) {
	conn := newFakeSocketConnFrames([]byte(" \n\t"), slackSocketEnvelopeBytes(t))
	client := socketClientWithConn(t, conn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := client.Run(ctx, func(ctx context.Context, envelope pkgslack.SocketEnvelope) error {
		require.Equal(t, "env1", envelope.EnvelopeID)
		cancel()
		return nil
	})

	require.NoError(t, err)
}

func TestSocketClient_RunReturnsDialAndOpenErrors(t *testing.T) {
	t.Run("open error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
		}))
		defer server.Close()
		client := &socketClient{appToken: "xapp-token", http: server.Client(), baseURL: server.URL}

		err := client.Run(context.Background(), nil)

		require.EqualError(t, err, "invalid_auth")
	})

	t.Run("dial error", func(t *testing.T) {
		client := socketClientWithConn(t, nil)
		client.dial = func(string) (socketConn, error) {
			return nil, errSlackTest
		}

		err := client.Run(context.Background(), nil)

		require.ErrorIs(t, err, errSlackTest)
	})
}

func TestSocketClient_RunAcksBeforeMorphlingEnvelope(t *testing.T) {
	conn := newFakeSocketConn(slackSocketEnvelopeBytes(t))
	client := socketClientWithConn(t, conn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := client.Run(ctx, func(ctx context.Context, envelope pkgslack.SocketEnvelope) error {
		require.JSONEq(t, `{"envelope_id":"env1"}`, conn.writeString())
		cancel()
		return nil
	})

	require.NoError(t, err)
}

func TestSocketClient_RunReturnsAckWriteError(t *testing.T) {
	conn := newFakeSocketConn(slackSocketEnvelopeBytes(t))
	conn.writeErr = errSlackTest
	client := socketClientWithConn(t, conn)

	err := client.Run(context.Background(), nil)

	require.ErrorIs(t, err, errSlackTest)
}

func TestWebsocketSocketConnSendsReceivesAndCloses(t *testing.T) {
	server := httptest.NewServer(websocket.Handler(func(conn *websocket.Conn) {
		var message string
		require.NoError(t, websocket.Message.Receive(conn, &message))
		require.Equal(t, "ping", message)
		require.NoError(t, websocket.Message.Send(conn, "pong"))
	}))
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, err := websocket.Dial(url, "", server.URL)
	require.NoError(t, err)
	socket := websocketSocketConn{conn: conn}

	require.NoError(t, socket.Send([]byte("ping")))
	message, err := socket.Receive()
	require.NoError(t, err)
	require.Equal(t, []byte("pong"), message)
	require.NoError(t, socket.Close())
}

func TestWebsocketSocketConnReturnsReceiveError(t *testing.T) {
	server := httptest.NewServer(websocket.Handler(func(conn *websocket.Conn) {
		require.NoError(t, conn.Close())
	}))
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, err := websocket.Dial(url, "", server.URL)
	require.NoError(t, err)
	socket := websocketSocketConn{conn: conn}

	_, err = socket.Receive()

	require.Error(t, err)
}

func socketClientWithConn(t *testing.T, conn socketConn) *socketClient {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/apps.connections.open", r.URL.Path)
		require.Equal(t, "Bearer xapp-token", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"ok":true,"url":"wss://socket.test"}`))
	}))
	t.Cleanup(server.Close)

	return &socketClient{
		appToken: "xapp-token",
		http:     server.Client(),
		baseURL:  server.URL,
		dial: func(url string) (socketConn, error) {
			require.Equal(t, "wss://socket.test", url)
			return conn, nil
		},
	}
}

func TestSocketClient_OpenConnectionErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "http error", status: http.StatusBadGateway, body: `{"ok":true}`, want: "slack socket open failed"},
		{name: "invalid json", status: http.StatusOK, body: `not json`, want: "invalid character 'o' in literal null (expecting 'u')"},
		{name: "ok false", status: http.StatusOK, body: `{"ok":false,"error":"invalid_auth"}`, want: "invalid_auth"},
		{name: "ok false without message", status: http.StatusOK, body: `{"ok":false}`, want: "slack socket open returned ok=false"},
		{name: "missing url", status: http.StatusOK, body: `{"ok":true}`, want: "slack socket URL is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			client := &socketClient{appToken: "xapp-token", http: server.Client(), baseURL: server.URL}

			_, err := client.openConnection(context.Background())

			require.EqualError(t, err, tt.want)
		})
	}
}

func TestSocketClient_OpenConnectionReturnsRequestAndTransportErrors(t *testing.T) {
	t.Run("request", func(t *testing.T) {
		client := &socketClient{baseURL: "http://[::1", http: http.DefaultClient}

		_, err := client.openConnection(context.Background())

		require.Error(t, err)
	})

	t.Run("transport", func(t *testing.T) {
		client := &socketClient{
			baseURL: "https://slack.test",
			http: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, errSlackTest
			})},
		}

		_, err := client.openConnection(context.Background())

		require.ErrorIs(t, err, errSlackTest)
	})
}

func TestSocketClient_OpenConnectionDefaultsHTTPClientAndBaseURL(t *testing.T) {
	origTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = origTransport })
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, defaultSlackAPIBase+"/apps.connections.open", r.URL.String())
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"url":"wss://socket.test"}`)),
			Header:     make(http.Header),
		}, nil
	})
	client := &socketClient{appToken: "xapp-token"}

	url, err := client.openConnection(context.Background())

	require.NoError(t, err)
	require.Equal(t, "wss://socket.test", url)
}

func TestSleepSocketReconnectReturnsFalseWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.False(t, sleepSocketReconnect(ctx, time.Hour))
}

func TestSleepSocketReconnectReturnsTrueAfterDelay(t *testing.T) {
	require.True(t, sleepSocketReconnect(context.Background(), time.Nanosecond))
}

func TestSocketReconnectDelayCapsAtMax(t *testing.T) {
	require.Equal(t, defaultSocketReconnectBaseDelay, socketReconnectDelay(0))
	require.Equal(t, defaultSocketReconnectBaseDelay, socketReconnectDelay(1))
	require.Equal(t, 2*defaultSocketReconnectBaseDelay, socketReconnectDelay(2))
	require.Equal(t, defaultSocketReconnectMaxDelay, socketReconnectDelay(99))
}

func stubSocketReconnectSleep(t *testing.T, delays *[]time.Duration) {
	t.Helper()

	orig := sleepSlackSocketReconnect
	sleepSlackSocketReconnect = func(ctx context.Context, delay time.Duration) bool {
		if delays != nil {
			*delays = append(*delays, delay)
		}
		return ctx.Err() == nil
	}
	t.Cleanup(func() {
		sleepSlackSocketReconnect = orig
	})
}

type fakeSocketClient struct {
	run func(context.Context, func(context.Context, pkgslack.SocketEnvelope) error) error
}

func (c *fakeSocketClient) Run(
	ctx context.Context,
	handler func(context.Context, pkgslack.SocketEnvelope) error,
) error {
	if c.run == nil {
		<-ctx.Done()
		return nil
	}

	return c.run(ctx, handler)
}
