package sessionmessages

import (
	"context"
	"errors"
	"fmt"

	"github.com/xymorphic/morph/internal/constants"
	"github.com/xymorphic/morph/internal/environment/sessionmessages"
	envtypes "github.com/xymorphic/morph/internal/environment/types"
	"github.com/xymorphic/morph/internal/instructions"
	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/internal/tools"
	"github.com/xymorphic/morph/internal/tools/common"
)

const (
	maxMessageIDs       = constants.SessionMessagesToolMaxMessageIDs
	maxAnchorWindowSize = constants.SessionMessagesToolMaxAnchorWindowSize
	maxOffsetRangeSize  = constants.SessionMessagesToolMaxOffsetRangeSize
	defaultMaxChars     = constants.SessionMessagesToolDefaultMaxChars
	maxMaxChars         = constants.SessionMessagesToolMaxChars
)

// Definition returns the model-visible tool definition.
func Definition(runtime envtypes.Runtime) tools.Definition {
	return tools.Definition{
		Name:             "session_messages",
		Description:      "Fetch exact session transcript messages by message id, anchor window, or offset range.",
		UsageInstruction: instructions.BuildSessionMessagesGuidance(),
		ParallelSafe:     true,
		Groups:           []string{"core"},
		Requires:         tools.Capabilities{Memory: true},
		SemanticIndex:    tools.SkipSemanticIndex(),
		Permission: permissions.Operation{
			Resource: permissions.ResourceSession,
			Action:   permissions.ActionRead,
			Effects:  []permissions.Effect{permissions.EffectRead},
		},
		InputSchema: common.ObjectSchema(map[string]any{
			"session_id": common.StringSchema("Optional session id. When omitted, read from the current session."),
			"message_ids": map[string]any{
				"type":        "array",
				"description": "Optional message ids to fetch directly in transcript order.",
				"items":       map[string]any{"type": "integer"},
			},
			"anchor_message_id": common.IntegerSchema("Optional anchor message id for centered context search."),
			"offset_start":      common.IntegerSchema("Optional inclusive start offset for range search."),
			"offset_end":        common.IntegerSchema("Optional exclusive end offset for range search."),
			"before":            common.IntegerSchema("Optional number of earlier messages to include around an anchor."),
			"after":             common.IntegerSchema("Optional number of later messages to include around an anchor."),
			"max_chars":         common.IntegerSchema("Optional maximum characters to return per message or tool-call input."),
		}),
		Handler: tools.HandlerFunc(func(ctx context.Context, call tools.Call) (tools.Result, error) {
			var req sessionmessages.SessionMessagesRequest
			if result := common.DecodeInput(call, &req); result.Error != "" {
				return result, nil
			}
			if runtime == nil {
				return common.ToolError("tool_error", "session messages is not configured"), nil
			}
			if err := req.Validate(); err != nil {
				return common.ToolError("invalid_input", err.Error()), nil
			}
			req, err := normalizeRequest(req)
			if err != nil {
				return common.ToolError("invalid_input", err.Error()), nil
			}

			response, err := runtime.GetSessionMessages(ctx, req)
			if err != nil {
				return common.ToolError("tool_error", err.Error()), nil
			}

			return common.EncodeOutput(response)
		}),
	}
}

func normalizeRequest(req sessionmessages.SessionMessagesRequest) (sessionmessages.SessionMessagesRequest, error) {
	selector, err := req.Selector()
	if err != nil {
		return req, err
	}

	if req.MaxChars <= 0 {
		req.MaxChars = defaultMaxChars
	} else if req.MaxChars > maxMaxChars {
		req.MaxChars = maxMaxChars
	}

	switch selector {
	case sessionmessages.SessionMessagesSelectorMessageIDs:
		if len(req.MessageIDs) > maxMessageIDs {
			return req, errors.New("message_ids must contain at most 50 ids")
		}
	case sessionmessages.SessionMessagesSelectorAnchor:
		if req.Before > maxAnchorWindowSize {
			return req, fmt.Errorf("before must be less than or equal to %d", maxAnchorWindowSize)
		}
		if req.After > maxAnchorWindowSize {
			return req, fmt.Errorf("after must be less than or equal to %d", maxAnchorWindowSize)
		}
	case sessionmessages.SessionMessagesSelectorOffsetRange:
		if req.OffsetStart != nil && req.OffsetEnd != nil && *req.OffsetEnd-*req.OffsetStart > maxOffsetRangeSize {
			return req, fmt.Errorf("offset range must include at most %d messages", maxOffsetRangeSize)
		}
	}

	return req, nil
}
