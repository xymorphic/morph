package httpjson

import (
	"encoding/json"
	"net/http"

	gatewaytypes "github.com/xymorphic/morph/pkg/gateway/types"
)

func Write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func WriteError(w http.ResponseWriter, status int, code string, message string) {
	Write(w, status, gatewaytypes.RespondResponse{
		Error: new(gatewaytypes.NewErrorResponse(code, message)),
	})
}
