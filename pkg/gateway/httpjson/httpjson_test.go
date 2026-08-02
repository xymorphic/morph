package httpjson

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	gatewaytypes "github.com/xymorphic/morph/pkg/gateway/types"
)

func TestWriteSetsJSONStatusAndBody(t *testing.T) {
	recorder := httptest.NewRecorder()

	Write(recorder, http.StatusAccepted, gatewaytypes.RespondResponse{Text: "hello"})

	require.Equal(t, http.StatusAccepted, recorder.Code)
	require.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	var response gatewaytypes.RespondResponse
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&response))
	require.Equal(t, "hello", response.Text)
}

func TestWriteErrorUsesSafeGatewayErrorShape(t *testing.T) {
	recorder := httptest.NewRecorder()

	WriteError(recorder, http.StatusUnauthorized, gatewaytypes.ErrorCodeUnauthorized, "unauthorized")

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	var response gatewaytypes.RespondResponse
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&response))
	require.Equal(t, &gatewaytypes.ErrorResponse{Code: gatewaytypes.ErrorCodeUnauthorized, Message: "unauthorized"}, response.Error)
}
