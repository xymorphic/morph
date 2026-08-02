package provider_openai

import (
	"context"
	"errors"
	"strings"

	"github.com/xymorphic/morph/pkg/logutils"
)

var modelLog = logutils.Module("model.openai")

func logModelClientRequestStarted(req normalizedGenerateRequest, stream bool) {
	modelLog.Debug().
		Str("api", req.API).
		Str("model", req.Model).
		Bool("stream", stream).
		Int("message_count", len(req.Messages)).
		Int("tool_count", len(req.Tools)).
		Bool("structured_output", req.StructuredOutput != nil).
		Int64("max_output_tokens", req.MaxOutputTokens).
		Msg("model client request started")
}

func logModelClientRequestCompleted(req normalizedGenerateRequest, stream bool, resp *Response) {
	event := modelLog.Debug().
		Str("api", req.API).
		Str("model", req.Model).
		Bool("stream", stream)
	if resp != nil {
		event = event.
			Str("response_model", resp.Model).
			Int("prompt_tokens", resp.PromptTokens).
			Int("completion_tokens", resp.CompletionTokens).
			Int("total_tokens", resp.TotalTokens).
			Int("tool_call_count", len(resp.ToolCalls)).
			Bool("requires_tool_calls", resp.RequiresToolCalls)
	}
	event.Msg("model client request completed")
}

func logModelClientRequestFailed(req normalizedGenerateRequest, stream bool, err error) {
	event := modelLog.Debug().
		Str("api", req.API).
		Str("model", req.Model).
		Bool("stream", stream).
		Str("error_kind", getModelClientErrorKind(err))
	if detail := getModelClientProviderErrorDetail(err); detail != "" {
		event = event.Str("provider_error", detail)
	}
	event.Msg("model client request failed")
}

func getModelClientErrorKind(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "context_canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}

	value := strings.ToLower(err.Error())
	switch {
	case strings.Contains(value, "response is required"):
		return "missing_response"
	case strings.Contains(value, "failed to accumulate") ||
		strings.Contains(value, "stream failed") ||
		strings.Contains(value, "stream error") ||
		strings.Contains(value, "stream response"):
		return "stream_failed"
	case strings.Contains(value, "tool"):
		return "tool_call_failed"
	case strings.Contains(value, "json"):
		return "decode_failed"
	case strings.Contains(value, "timeout"):
		return "timeout"
	default:
		return "operation_failed"
	}
}

func logRequestDebugMetadata(req normalizedGenerateRequest) {
	modelLog.Debug().
		Str("api", req.API).
		Msg("model request debug metadata")
}
