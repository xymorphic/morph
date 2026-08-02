package telegram

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/gateway/dispatch"
	storage "github.com/xymorphic/morph/internal/state/core"
	tg "github.com/xymorphic/morph/pkg/gateway/telegram"
	gatewaytypes "github.com/xymorphic/morph/pkg/gateway/types"
)

func TestTelegramWebhookRejectsUnauthorizedRequestsBeforeDispatch(t *testing.T) {
	responder := &genericResponderStub{}
	handler := newWebhookHandler(telegramWebhookConfig(), responder)
	req := httptest.NewRequest(http.MethodPost, WebhookPath, bytes.NewBufferString(`{"update_id":1}`))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.False(t, responder.called)
	require.Equal(t, &gatewaytypes.ErrorResponse{
		Code:    gatewaytypes.ErrorCodeUnauthorized,
		Message: "unauthorized",
	}, decodeGatewayResponse(t, recorder).Error)
}

func TestTelegramWebhookRejectsUnsupportedRequests(t *testing.T) {
	for _, tt := range []struct {
		name   string
		cfg    config.GatewayConfig
		method string
		body   string
		status int
		error  *gatewaytypes.ErrorResponse
	}{
		{
			name:   "method",
			cfg:    telegramWebhookConfig(),
			method: http.MethodGet,
			status: http.StatusMethodNotAllowed,
			error:  &gatewaytypes.ErrorResponse{Code: gatewaytypes.ErrorCodeBadRequest, Message: "method not allowed"},
		},
		{
			name:   "disabled",
			cfg:    config.GatewayConfig{},
			method: http.MethodPost,
			body:   `{}`,
			status: http.StatusNotFound,
			error:  &gatewaytypes.ErrorResponse{Code: gatewaytypes.ErrorCodeBadRequest, Message: "telegram webhook is disabled"},
		},
		{
			name:   "invalid json",
			cfg:    telegramWebhookConfig(),
			method: http.MethodPost,
			body:   `{`,
			status: http.StatusBadRequest,
			error:  &gatewaytypes.ErrorResponse{Code: gatewaytypes.ErrorCodeBadRequest, Message: "invalid request"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			responder := &genericResponderStub{}
			handler := newWebhookHandler(tt.cfg, responder)
			req := httptest.NewRequest(tt.method, WebhookPath, bytes.NewBufferString(tt.body))
			req.Header.Set(tg.WebhookSecretHeader, "secret")
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, req)

			require.Equal(t, tt.status, recorder.Code)
			require.False(t, responder.called)
			require.Equal(t, tt.error, decodeGatewayResponse(t, recorder).Error)
		})
	}
}

func TestTelegramWebhookReturnsSafeErrorWhenServiceMissing(t *testing.T) {
	handler := newWebhookHandler(telegramWebhookConfig(), nil)
	req := httptest.NewRequest(http.MethodPost, WebhookPath, bytes.NewBufferString(`{"update_id":1}`))
	req.Header.Set(tg.WebhookSecretHeader, "secret")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	require.Equal(t, &gatewaytypes.ErrorResponse{
		Code:    gatewaytypes.ErrorCodeInternalError,
		Message: "gateway request failed",
	}, decodeGatewayResponse(t, recorder).Error)
}

func TestTelegramWebhookReturnsSafeErrorWhenDispatcherMissing(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(WebhookPath, HandleWebhook(telegramWebhookConfig(), &genericResponderStub{}, nil))
	req := httptest.NewRequest(http.MethodPost, WebhookPath, bytes.NewBufferString(`{"update_id":1}`))
	req.Header.Set(tg.WebhookSecretHeader, "secret")
	recorder := httptest.NewRecorder()

	mux.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	require.Equal(t, &gatewaytypes.ErrorResponse{
		Code:    gatewaytypes.ErrorCodeInternalError,
		Message: "gateway request failed",
	}, decodeGatewayResponse(t, recorder).Error)
}

func TestTelegramWebhookAcknowledgesAndDispatchesAsynchronously(t *testing.T) {
	setTelegramDraftID(t, 77)
	origNewTelegramAPI := newTelegramAPI
	t.Cleanup(func() { newTelegramAPI = origNewTelegramAPI })
	api := &fakeTelegramAPI{}
	newTelegramAPI = func(config.GatewayTelegramConfig) telegramAPI {
		return api
	}
	responder := &genericResponderStub{
		createdSession: storage.Session{ID: genericCreatedSessionID},
		reply:          "reply",
	}
	handler := newWebhookHandler(telegramWebhookConfig(), responder)
	req := httptest.NewRequest(http.MethodPost, WebhookPath, bytes.NewBufferString(`{
		"update_id": 15,
		"message": {
			"message_id": 5,
			"text": "hello",
			"chat": {"id": 123, "type": "private"}
		}
	}`))
	req.Header.Set(tg.WebhookSecretHeader, "secret")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "ok\n", recorder.Body.String())
	require.Eventually(t, func() bool {
		return responder.wasCalled()
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, "hello", responder.receivedMessage())
	require.Eventually(t, func() bool {
		return len(api.allCalls()) == 4
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, []telegramAPICall{
		{method: "sendChatAction", target: tg.Target{ChatID: "123", ReplyToMessageID: 5, ChatType: "private"}, action: "typing"},
		{method: "sendMessageDraft", target: tg.Target{ChatID: "123", ReplyToMessageID: 5, ChatType: "private"}, draftID: 77, text: "stream\n..."},
		{method: "sendMessageDraft", target: tg.Target{ChatID: "123", ReplyToMessageID: 5, ChatType: "private"}, draftID: 77, text: "stream delta\n..."},
		{method: "sendMessage", target: tg.Target{ChatID: "123", ReplyToMessageID: 5, ChatType: "private"}, text: "reply"},
	}, api.allCalls())
}

func TestTelegramWebhookDeduplicatesProviderRetries(t *testing.T) {
	setTelegramDraftID(t, 77)
	origNewTelegramAPI := newTelegramAPI
	t.Cleanup(func() { newTelegramAPI = origNewTelegramAPI })
	api := &fakeTelegramAPI{}
	newTelegramAPI = func(config.GatewayTelegramConfig) telegramAPI {
		return api
	}
	responder := &genericResponderStub{
		createdSession: storage.Session{ID: genericCreatedSessionID},
		reply:          "reply",
	}
	handler := newWebhookHandler(telegramWebhookConfig(), responder)
	body := `{
		"update_id": 15,
		"message": {
			"message_id": 5,
			"text": "hello",
			"chat": {"id": 123, "type": "private"}
		}
	}`

	for range 2 {
		req := httptest.NewRequest(http.MethodPost, WebhookPath, bytes.NewBufferString(body))
		req.Header.Set(tg.WebhookSecretHeader, "secret")
		recorder := httptest.NewRecorder()

		handler.ServeHTTP(recorder, req)

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, "ok\n", recorder.Body.String())
	}

	require.Eventually(t, func() bool {
		return len(api.allCalls()) == 4
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, []telegramAPICall{
		{method: "sendChatAction", target: tg.Target{ChatID: "123", ReplyToMessageID: 5, ChatType: "private"}, action: "typing"},
		{method: "sendMessageDraft", target: tg.Target{ChatID: "123", ReplyToMessageID: 5, ChatType: "private"}, draftID: 77, text: "stream\n..."},
		{method: "sendMessageDraft", target: tg.Target{ChatID: "123", ReplyToMessageID: 5, ChatType: "private"}, draftID: 77, text: "stream delta\n..."},
		{method: "sendMessage", target: tg.Target{ChatID: "123", ReplyToMessageID: 5, ChatType: "private"}, text: "reply"},
	}, api.allCalls())
}

func TestTelegramWebhookReturnsRetryableErrorWhenQueueIsFull(t *testing.T) {
	dispatcher := dispatch.New(dispatch.Options{Capacity: 1})
	handler := newWebhookHandlerWithDispatcher(telegramWebhookConfig(), &genericResponderStub{}, dispatcher)
	first := httptest.NewRequest(http.MethodPost, WebhookPath, bytes.NewBufferString(`{"update_id":1}`))
	first.Header.Set(tg.WebhookSecretHeader, "secret")
	firstRecorder := httptest.NewRecorder()
	handler.ServeHTTP(firstRecorder, first)
	require.Equal(t, http.StatusOK, firstRecorder.Code)

	second := httptest.NewRequest(http.MethodPost, WebhookPath, bytes.NewBufferString(`{"update_id":2}`))
	second.Header.Set(tg.WebhookSecretHeader, "secret")
	secondRecorder := httptest.NewRecorder()

	handler.ServeHTTP(secondRecorder, second)

	require.Equal(t, http.StatusServiceUnavailable, secondRecorder.Code)
	require.Equal(t, &gatewaytypes.ErrorResponse{
		Code:    gatewaytypes.ErrorCodeInternalError,
		Message: "gateway request failed",
	}, decodeGatewayResponse(t, secondRecorder).Error)
}

func TestTelegramWebhookAcknowledgesWhenBackgroundDispatchFails(t *testing.T) {
	origNewTelegramAPI := newTelegramAPI
	t.Cleanup(func() { newTelegramAPI = origNewTelegramAPI })
	api := &fakeTelegramAPI{sendErr: errTelegramTest}
	newTelegramAPI = func(config.GatewayTelegramConfig) telegramAPI {
		return api
	}
	responder := &genericResponderStub{
		createdSession: storage.Session{ID: genericCreatedSessionID},
		reply:          "reply",
	}
	handler := newWebhookHandler(telegramWebhookConfig(), responder)
	req := httptest.NewRequest(http.MethodPost, WebhookPath, bytes.NewBufferString(`{
		"update_id": 15,
			"message": {
				"message_id": 5,
				"text": "hello",
				"chat": {"id": -100, "type": "group"},
				"from": {"id": 123}
			}
	}`))
	req.Header.Set(tg.WebhookSecretHeader, "secret")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Eventually(t, func() bool {
		return len(api.allCalls()) > 0
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, 1, responder.callCount())
}

func TestTelegramWebhookReturnsRetryableErrorWhenDispatcherIsClosed(t *testing.T) {
	dispatcher := dispatch.New(dispatch.Options{})
	dispatcher.Close()
	responder := &genericResponderStub{
		createdSession: storage.Session{ID: genericCreatedSessionID},
		reply:          "reply",
	}
	handler := newWebhookHandlerWithDispatcher(telegramWebhookConfig(), responder, dispatcher)
	req := httptest.NewRequest(http.MethodPost, WebhookPath, bytes.NewBufferString(`{
		"update_id": 15,
		"message": {
			"message_id": 5,
			"text": "hello",
			"chat": {"id": 123, "type": "private"}
		}
	}`))
	req.Header.Set(tg.WebhookSecretHeader, "secret")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.False(t, responder.called)
	require.Equal(t, &gatewaytypes.ErrorResponse{
		Code:    gatewaytypes.ErrorCodeInternalError,
		Message: "gateway request failed",
	}, decodeGatewayResponse(t, recorder).Error)
}

func TestSetWebhookCallsTelegramAPI(t *testing.T) {
	origNewTelegramAPI := newTelegramAPI
	t.Cleanup(func() { newTelegramAPI = origNewTelegramAPI })
	api := &fakeTelegramAPI{}
	newTelegramAPI = func(config.GatewayTelegramConfig) telegramAPI {
		return api
	}

	err := SetWebhook(context.Background(), config.GatewayTelegramConfig{
		BotToken:      " token ",
		WebhookSecret: " secret ",
	}, " https://example.com/gateway/telegram/webhook ")

	require.NoError(t, err)
	require.Equal(t, []telegramAPICall{{
		method: "setWebhook",
		url:    "https://example.com/gateway/telegram/webhook",
		secret: "secret",
	}}, api.callsOfMethod("setWebhook"))
}

func TestSetWebhookValidatesConfig(t *testing.T) {
	err := SetWebhook(context.Background(), config.GatewayTelegramConfig{
		WebhookSecret: "secret",
	}, "https://example.com/gateway/telegram/webhook")
	require.EqualError(t, err, "gateway telegram bot token is required")

	err = SetWebhook(context.Background(), config.GatewayTelegramConfig{
		BotToken: "token",
	}, "https://example.com/gateway/telegram/webhook")
	require.EqualError(t, err, "gateway telegram webhook secret is required")
}

func TestSetWebhookWithEmptyURLDeletesTelegramWebhook(t *testing.T) {
	origNewTelegramAPI := newTelegramAPI
	t.Cleanup(func() { newTelegramAPI = origNewTelegramAPI })
	api := &fakeTelegramAPI{}
	newTelegramAPI = func(config.GatewayTelegramConfig) telegramAPI {
		return api
	}

	err := SetWebhook(context.Background(), config.GatewayTelegramConfig{
		BotToken: " token ",
	}, "")

	require.NoError(t, err)
	require.Equal(t, []telegramAPICall{{method: "deleteWebhook"}}, api.callsOfMethod("deleteWebhook"))
}

func telegramWebhookConfig() config.GatewayConfig {
	cfg := config.GatewayConfig{}
	cfg.PairingSecret = "pair-secret"
	cfg.Telegram.Enabled = true
	cfg.Telegram.Mode = config.GatewayTelegramModeWebhook
	cfg.Telegram.BotToken = "telegram-token"
	cfg.Telegram.WebhookSecret = "secret"
	cfg.Telegram.AllowedUsers = []string{"123"}
	return cfg
}

func newWebhookHandlerWithDispatcher(
	cfg config.GatewayConfig,
	service Service,
	dispatcher *dispatch.Dispatcher,
) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(WebhookPath, HandleWebhook(cfg, service, dispatcher))
	return mux
}
