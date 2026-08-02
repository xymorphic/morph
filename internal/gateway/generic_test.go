package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/permissions"
	storage "github.com/xymorphic/morph/internal/state/core"
	agentcore "github.com/xymorphic/morph/pkg/agent"
	gatewaytypes "github.com/xymorphic/morph/pkg/gateway/types"
)

func TestGenericRespondRejectsMissingAndInvalidBearerToken(t *testing.T) {
	for _, tt := range []struct {
		name          string
		authorization string
	}{
		{name: "missing"},
		{name: "invalid", authorization: "Bearer wrong-token"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			responder := &genericResponderStub{}
			handler := newHTTPHandler(config.GatewayConfig{AuthToken: "secret-token"}, responder)
			req := httptest.NewRequest(http.MethodPost, "/v1/respond", bytes.NewBufferString(`{"conversation_id":"default","message":"hello"}`))
			req.Header.Set("Authorization", tt.authorization)
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, req)

			require.Equal(t, http.StatusUnauthorized, recorder.Code)
			require.False(t, responder.called)
			response := decodeGatewayResponse(t, recorder)
			require.Equal(t, &gatewaytypes.ErrorResponse{
				Code:    gatewaytypes.ErrorCodeUnauthorized,
				Message: "unauthorized",
			}, response.Error)
		})
	}
}

func TestGenericRespondCallsResponderAndReturnsAssistantText(t *testing.T) {
	responder := &genericResponderStub{
		createdSession: storage.Session{ID: genericCreatedSessionID},
		reply:          "hello back",
	}
	handler := newHTTPHandler(config.GatewayConfig{AuthToken: "secret-token"}, responder)
	req := httptest.NewRequest(http.MethodPost, "/v1/respond", bytes.NewBufferString(`{
		"conversation_id":"default",
		"message":" hello ",
		"user_id":"user-1",
		"source":"generic",
		"instruct":"be brief"
	}`))
	req.Header.Set("Authorization", "Bearer secret-token")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "hello", responder.message)
	authorization, ok := permissions.FromContext(responder.respondContext)
	require.True(t, ok)
	require.Equal(t, permissions.ActorGatewayUser, authorization.Actor.Kind)
	require.Equal(t, getGenericGatewayPrincipal("secret-token"), authorization.Actor.ID)
	require.NotContains(t, authorization.Actor.ID, "secret-token")
	require.Equal(t, permissions.SurfaceKindGateway, authorization.SurfaceKind)
	require.Equal(t, permissions.SurfaceHTTP, authorization.Surface)
	require.Equal(t, genericCreatedSessionID, authorization.SessionID)
	require.Equal(t, agentcore.RespondOptions{SessionID: genericCreatedSessionID, Instruct: "be brief"}, responder.options)
	require.Equal(t, storage.GatewayBinding{
		Key:       "generic::default:",
		SessionID: genericCreatedSessionID,
		CreatedAt: responder.savedBinding.CreatedAt,
		UpdatedAt: responder.savedBinding.UpdatedAt,
	}, responder.savedBinding)
	require.Equal(t, gatewaytypes.RespondResponse{
		ConversationID: "default",
		SessionID:      genericCreatedSessionID,
		Text:           "hello back",
	}, decodeGatewayResponse(t, recorder))
}

func TestGenericRespondReusesPersistedGatewayBinding(t *testing.T) {
	responder := &genericResponderStub{
		binding: storage.GatewayBinding{
			Key:       "generic::default:",
			SessionID: genericExistingSessionID,
		},
		bindingFound: true,
		reply:        "hello back",
	}
	handler := newHTTPHandler(config.GatewayConfig{}, responder)
	req := httptest.NewRequest(http.MethodPost, "/v1/respond",
		bytes.NewBufferString(`{"conversation_id":"default","message":"hello"}`))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.False(t, responder.created)
	require.Equal(t, agentcore.RespondOptions{SessionID: genericExistingSessionID}, responder.options)
	require.Equal(t, "hello back", decodeGatewayResponse(t, recorder).Text)
}

func TestGenericRespondRejectsInvalidRequestWithoutCallingResponder(t *testing.T) {
	for _, tt := range []struct {
		name    string
		body    string
		message string
	}{
		{name: "empty conversation", body: `{"conversation_id":" ","message":"hello"}`, message: "conversation_id is required"},
		{name: "empty message", body: `{"conversation_id":"default","message":" "}`, message: "message is required"},
		{name: "invalid json", body: `{`, message: "invalid request"},
		{name: "unknown field", body: `{"conversation_id":"default","message":"hello","extra":true}`, message: "invalid request"},
		{name: "trailing json", body: `{"conversation_id":"default","message":"hello"} true`, message: "invalid request"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			responder := &genericResponderStub{}
			handler := newHTTPHandler(config.GatewayConfig{}, responder)
			req := httptest.NewRequest(http.MethodPost, "/v1/respond", bytes.NewBufferString(tt.body))
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, req)

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.False(t, responder.called)
			response := decodeGatewayResponse(t, recorder)
			require.Equal(t, &gatewaytypes.ErrorResponse{
				Code:    gatewaytypes.ErrorCodeBadRequest,
				Message: tt.message,
			}, response.Error)
		})
	}
}

func TestGenericRespondReturnsSafeErrorWhenResponderFails(t *testing.T) {
	responder := &genericResponderStub{
		createdSession: storage.Session{ID: genericCreatedSessionID},
		err:            errors.New("provider stack trace: secret"),
	}
	handler := newHTTPHandler(config.GatewayConfig{}, responder)
	req := httptest.NewRequest(http.MethodPost, "/v1/respond", bytes.NewBufferString(`{"conversation_id":"default","message":"hello"}`))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	response := decodeGatewayResponse(t, recorder)
	require.Equal(t, &gatewaytypes.ErrorResponse{
		Code:    gatewaytypes.ErrorCodeInternalError,
		Message: "gateway request failed",
	}, response.Error)
	require.NotContains(t, recorder.Body.String(), "secret")
}

func TestGenericRespondReturnsSafeErrorWhenBindingStoreFails(t *testing.T) {
	for _, tt := range []struct {
		name      string
		responder *genericResponderStub
	}{
		{
			name:      "lookup",
			responder: &genericResponderStub{getBindingErr: errors.New("sqlite: secret path")},
		},
		{
			name: "save",
			responder: &genericResponderStub{
				createdSession: storage.Session{ID: genericCreatedSessionID},
				saveBindingErr: errors.New("sqlite: secret path"),
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			handler := newHTTPHandler(config.GatewayConfig{}, tt.responder)
			req := httptest.NewRequest(http.MethodPost, "/v1/respond",
				bytes.NewBufferString(`{"conversation_id":"default","message":"hello"}`))
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, req)

			require.Equal(t, http.StatusInternalServerError, recorder.Code)
			require.False(t, tt.responder.called)
			response := decodeGatewayResponse(t, recorder)
			require.Equal(t, &gatewaytypes.ErrorResponse{
				Code:    gatewaytypes.ErrorCodeInternalError,
				Message: "gateway request failed",
			}, response.Error)
			require.NotContains(t, recorder.Body.String(), "secret")
		})
	}
}

func TestGenericRespondReturnsSafeErrorWhenResponderMissing(t *testing.T) {
	handler := newHTTPHandler(config.GatewayConfig{}, nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/respond", bytes.NewBufferString(`{"conversation_id":"default","message":"hello"}`))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	response := decodeGatewayResponse(t, recorder)
	require.Equal(t, &gatewaytypes.ErrorResponse{
		Code:    gatewaytypes.ErrorCodeInternalError,
		Message: "gateway request failed",
	}, response.Error)
}

func TestGenericRespondRejectsUnsupportedMethod(t *testing.T) {
	handler := newHTTPHandler(config.GatewayConfig{}, &genericResponderStub{})
	req := httptest.NewRequest(http.MethodGet, "/v1/respond", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusMethodNotAllowed, recorder.Code)
	require.Equal(t, http.MethodPost, recorder.Header().Get("Allow"))
	response := decodeGatewayResponse(t, recorder)
	require.Equal(t, &gatewaytypes.ErrorResponse{
		Code:    gatewaytypes.ErrorCodeBadRequest,
		Message: "method not allowed",
	}, response.Error)
}

func decodeGatewayResponse(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
) gatewaytypes.RespondResponse {
	t.Helper()

	var response gatewaytypes.RespondResponse
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&response))
	return response
}
