package slack

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/gateway/dispatch"
	pkgslack "github.com/xymorphic/morph/pkg/gateway/slack"
)

func TestHandleEvents_VerifiesSignatureBeforeDecode(t *testing.T) {
	handler := HandleWebhook(slackGatewayConfig(), newSlackServiceStub(), dispatch.New(dispatch.Options{}))
	req := httptest.NewRequest(http.MethodPost, WebhookPath, bytes.NewBufferString(`not json`))
	timestamp := currentSlackTimestamp()
	req.Header.Set(pkgslack.TimestampHeader, timestamp)
	req.Header.Set(pkgslack.SignatureHeader, pkgslack.SignRequest("wrong", timestamp, []byte(`not json`)))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestNewSlackAPIReturnsHTTPClient(t *testing.T) {
	api := newSlackAPI(config.GatewaySlackConfig{BotToken: "xoxb-token"})

	client, ok := api.(*HTTPClient)
	require.True(t, ok)
	require.Equal(t, "xoxb-token", client.token)
}

func TestHandleEvents_RejectsOversizedBody(t *testing.T) {
	body := bytes.Repeat([]byte("a"), maxEventsBodyBytes+1)
	handler := HandleWebhook(slackGatewayConfig(), newSlackServiceStub(), dispatch.New(dispatch.Options{}))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, signedSlackRequest(body))

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestHandleEvents_URLVerification(t *testing.T) {
	body := []byte(`{"type":"url_verification","challenge":"challenge-value"}`)
	handler := HandleWebhook(slackGatewayConfig(), newSlackServiceStub(), dispatch.New(dispatch.Options{}))
	req := signedSlackRequest(body)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.JSONEq(t, `{"challenge":"challenge-value"}`, recorder.Body.String())
}

func TestHandleEvents_EnqueuesAndDeduplicatesEvents(t *testing.T) {
	origNewSlackAPI := newSlackAPI
	t.Cleanup(func() { newSlackAPI = origNewSlackAPI })
	api := &fakeSlackAPI{}
	newSlackAPI = func(config.GatewaySlackConfig) API { return api }
	service := newSlackServiceStub()
	cfg := slackGatewayConfig()
	cfg.AllowedUsers = []string{"U1"}
	dispatcher := dispatch.New(dispatch.Options{Workers: 1, Capacity: 2})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dispatcher.Start(ctx)
	t.Cleanup(func() { require.NoError(t, dispatcher.Shutdown(context.Background())) })
	handler := HandleWebhook(cfg, service, dispatcher)
	body := []byte(`{
		"type":"event_callback",
		"team_id":"T1",
		"event_id":"Ev1",
		"event":{"type":"message","channel":"D1","channel_type":"im","user":"U1","text":"hello","ts":"100.1"}
	}`)

	for range 2 {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, signedSlackRequest(body))
		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, "ok\n", recorder.Body.String())
	}

	require.Eventually(t, func() bool {
		return service.callCount() == 1 && dispatcher.Status().Duplicates == 1
	}, time.Second, 10*time.Millisecond)
	require.Len(t, api.allCalls(), 3)
}

func TestHandleEvents_IgnoresUnsupportedEvents(t *testing.T) {
	service := newSlackServiceStub()
	dispatcher := dispatch.New(dispatch.Options{Workers: 1, Capacity: 1})
	dispatcher.Start(context.Background())
	t.Cleanup(func() { require.NoError(t, dispatcher.Shutdown(context.Background())) })
	handler := HandleWebhook(slackGatewayConfig(), service, dispatcher)
	body := []byte(`{
		"type":"event_callback",
		"team_id":"T1",
		"event_id":"Ev1",
		"event":{"type":"reaction_added","channel":"D1","user":"U1","text":"hello"}
	}`)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, signedSlackRequest(body))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "ok\n", recorder.Body.String())
	require.Zero(t, service.callCount())
}

func TestHandleEvents_ReturnsServiceUnavailableWhenQueueFull(t *testing.T) {
	dispatcher := dispatch.New(dispatch.Options{Workers: 1, Capacity: 1})
	_, err := dispatcher.Enqueue(dispatch.Job{
		ID: "blocker",
		Run: func(context.Context) error {
			return nil
		},
	})
	require.NoError(t, err)
	handler := HandleWebhook(slackGatewayConfig(), newSlackServiceStub(), dispatcher)
	body := []byte(`{
		"type":"event_callback",
		"team_id":"T1",
		"event_id":"Ev1",
		"event":{"type":"message","channel":"D1","channel_type":"im","user":"U1","text":"hello","ts":"100.1"}
	}`)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, signedSlackRequest(body))

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}

func TestHandleEvents_ReturnsServiceUnavailableWhenDispatcherClosed(t *testing.T) {
	dispatcher := dispatch.New(dispatch.Options{Workers: 1, Capacity: 1})
	dispatcher.Close()
	handler := HandleWebhook(slackGatewayConfig(), newSlackServiceStub(), dispatcher)
	body := []byte(`{
		"type":"event_callback",
		"team_id":"T1",
		"event_id":"Ev1",
		"event":{"type":"message","channel":"D1","channel_type":"im","user":"U1","text":"hello","ts":"100.1"}
	}`)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, signedSlackRequest(body))

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}

func TestHandleEvents_ReturnsBadRequestForInvalidNormalizedEvent(t *testing.T) {
	handler := HandleWebhook(slackGatewayConfig(), newSlackServiceStub(), dispatch.New(dispatch.Options{}))
	body := []byte(`{
		"type":"event_callback",
		"team_id":"T1",
		"event_id":"Ev1",
		"event":{"type":"message","channel_type":"im","user":"U1","text":"hello","ts":"100.1"}
	}`)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, signedSlackRequest(body))

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestDispatchInboundReturnsAdapterError(t *testing.T) {
	origNewSlackAPI := newSlackAPI
	t.Cleanup(func() { newSlackAPI = origNewSlackAPI })
	newSlackAPI = func(config.GatewaySlackConfig) API { return &fakeSlackAPI{} }
	cfg := slackGatewayConfig()
	cfg.AllowedUsers = []string{"U1"}

	err := dispatchInbound(context.Background(), cfg, newSlackServiceStub(), pkgslack.InboundMessage{
		Text:      "hello",
		SenderID:  "U1",
		TeamID:    "",
		ChannelID: "D1",
		ThreadTS:  "100.1",
	})

	require.ErrorContains(t, err, "slack team id is required")
}

func TestGetSlackJobIDUsesStableFallbacks(t *testing.T) {
	require.Equal(t, "slack:event:Ev1", getSlackJobID(pkgslack.InboundMessage{EventID: "Ev1"}))
	require.Equal(t, "slack:socket:env1", getSlackJobID(pkgslack.InboundMessage{SocketID: "env1"}))
	require.Equal(t, "slack:message:T1:C1:100.1", getSlackJobID(pkgslack.InboundMessage{
		TeamID:    "T1",
		ChannelID: "C1",
		MessageTS: "100.1",
	}))
}

func TestHandleEvents_RejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name       string
		cfg        config.GatewayConfig
		method     string
		body       []byte
		dispatcher *dispatch.Dispatcher
		want       int
	}{
		{name: "method", cfg: slackGatewayConfig(), method: http.MethodGet, body: []byte(`{}`), dispatcher: dispatch.New(dispatch.Options{}), want: http.StatusMethodNotAllowed},
		{name: "disabled", cfg: config.GatewayConfig{}, method: http.MethodPost, body: []byte(`{}`), dispatcher: dispatch.New(dispatch.Options{}), want: http.StatusNotFound},
		{name: "bad json", cfg: slackGatewayConfig(), method: http.MethodPost, body: []byte(`not json`), dispatcher: dispatch.New(dispatch.Options{}), want: http.StatusBadRequest},
		{name: "missing dispatcher", cfg: slackGatewayConfig(), method: http.MethodPost, body: []byte(`{"type":"event_callback","team_id":"T1","event":{"type":"message","channel":"D1","channel_type":"im","user":"U1","text":"hello","ts":"100.1"}}`), want: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := HandleWebhook(tt.cfg, newSlackServiceStub(), tt.dispatcher)
			req := httptest.NewRequest(tt.method, WebhookPath, bytes.NewReader(tt.body))
			if tt.method == http.MethodPost {
				signSlackRequest(req, tt.body)
			}
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, req)

			require.Equal(t, tt.want, recorder.Code)
		})
	}
}

func signedSlackRequest(body []byte) *http.Request {
	req := httptest.NewRequest(http.MethodPost, WebhookPath, bytes.NewReader(body))
	signSlackRequest(req, body)
	return req
}

func signSlackRequest(req *http.Request, body []byte) {
	timestamp := currentSlackTimestamp()
	req.Header.Set(pkgslack.TimestampHeader, timestamp)
	req.Header.Set(pkgslack.SignatureHeader, pkgslack.SignRequest("signing-secret", timestamp, body))
}

func currentSlackTimestamp() string {
	return strconv.FormatInt(time.Now().Unix(), 10)
}
