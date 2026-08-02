package types

import (
	"errors"

	"github.com/xymorphic/morph/pkg/str"
)

const (
	ErrorCodeBadRequest    = "bad_request"
	ErrorCodeUnauthorized  = "unauthorized"
	ErrorCodeInternalError = "internal_error"
	SourceGenericHTTP      = "generic_http"
)

var (
	ErrConversationIDRequired = errors.New("conversation_id is required")
	ErrMessageRequired        = errors.New("message is required")
)

type RespondRequest struct {
	ConversationID string `json:"conversation_id"`
	Message        string `json:"message"`
	UserID         string `json:"user_id,omitempty"`
	Source         string `json:"source,omitempty"`
	Instruct       string `json:"instruct,omitempty"`
}

type RespondResponse struct {
	ConversationID string         `json:"conversation_id,omitempty"`
	SessionID      string         `json:"session_id,omitempty"`
	Text           string         `json:"text,omitempty"`
	Error          *ErrorResponse `json:"error,omitempty"`
}

type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func NormalizeRespondRequest(req RespondRequest) RespondRequest {
	conversationIDValue := str.String(req.ConversationID)
	req.ConversationID = conversationIDValue.Trim()
	messageValue := str.String(req.Message)
	req.Message = messageValue.Trim()
	userIDValue := str.String(req.UserID)
	req.UserID = userIDValue.Trim()
	sourceValue := str.String(req.Source)
	req.Source = sourceValue.Trim()
	instructValue := str.String(req.Instruct)
	req.Instruct = instructValue.Trim()
	if req.Source == "" {
		req.Source = SourceGenericHTTP
	}

	return req
}

func ValidateRespondRequest(req RespondRequest) error {
	req = NormalizeRespondRequest(req)
	if req.ConversationID == "" {
		return ErrConversationIDRequired
	}
	if req.Message == "" {
		return ErrMessageRequired
	}

	return nil
}

func NewErrorResponse(code string, message string) ErrorResponse {
	codeValue := str.String(code)
	messageValue2 := str.String(message)
	return ErrorResponse{
		Code:    codeValue.Trim(),
		Message: messageValue2.Trim(),
	}
}
