package sessionmessages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/environment/sessionmessages"
	"github.com/xymorphic/morph/internal/instructions"
	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/internal/tools"
	toolmocks "github.com/xymorphic/morph/internal/tools/mocks"
)

func TestSessionMessages_DefinitionIncludesUsageInstruction(t *testing.T) {
	definition := Definition(&toolmocks.Runtime{})

	require.Equal(t, instructions.BuildSessionMessagesGuidance(), definition.UsageInstruction)
	require.Equal(t, permissions.Operation{
		Resource: permissions.ResourceSession,
		Action:   permissions.ActionRead,
		Effects:  []permissions.Effect{permissions.EffectRead},
	}, definition.Permission)
}

func TestSessionMessages_ToolFetchesByMessageIDs(t *testing.T) {
	result, err := Definition(&toolmocks.Runtime{
		GetSessionMessagesFunc: func(_ context.Context, req sessionmessages.SessionMessagesRequest) (sessionmessages.SessionMessagesResponse, error) {
			require.Equal(t, "session-1", req.SessionID)
			require.Equal(t, []uint{11, 13}, req.MessageIDs)
			require.Equal(t, 120, req.MaxChars)

			return sessionmessages.SessionMessagesResponse{
				SessionID: "session-1",
				Messages: []sessionmessages.SessionMessageRecord{
					{MessageID: 11, Offset: 2, Role: "user", Content: "alpha"},
					{MessageID: 13, Offset: 4, Role: "assistant", Content: "beta"},
				},
			}, nil
		},
	}).Handler.Invoke(context.Background(), tools.Call{
		Name:  "session_messages",
		Input: `{"session_id":"session-1","message_ids":[11,13],"max_chars":120}`,
	})

	require.NoError(t, err)

	var payload sessionmessages.SessionMessagesResponse
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Equal(t, "session-1", payload.SessionID)
	require.Len(t, payload.Messages, 2)
	require.Equal(t, uint(11), payload.Messages[0].MessageID)
}

func TestSessionMessages_ToolFetchesByAnchorWindow(t *testing.T) {
	result, err := Definition(&toolmocks.Runtime{
		GetSessionMessagesFunc: func(_ context.Context, req sessionmessages.SessionMessagesRequest) (sessionmessages.SessionMessagesResponse, error) {
			require.Empty(t, req.SessionID)
			require.Equal(t, uint(7), req.AnchorMessageID)
			require.Equal(t, 2, req.Before)
			require.Equal(t, 1, req.After)
			require.Equal(t, 4000, req.MaxChars)

			return sessionmessages.SessionMessagesResponse{
				SessionID: "default",
				Messages:  []sessionmessages.SessionMessageRecord{{MessageID: 7, Offset: 5, Role: "user", Content: "anchor"}},
			}, nil
		},
	}).Handler.Invoke(context.Background(), tools.Call{
		Name:  "session_messages",
		Input: `{"anchor_message_id":7,"before":2,"after":1}`,
	})

	require.NoError(t, err)

	var payload sessionmessages.SessionMessagesResponse
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Equal(t, "default", payload.SessionID)
	require.Len(t, payload.Messages, 1)
	require.Equal(t, "anchor", payload.Messages[0].Content)
}

func TestSessionMessages_ToolFetchesByOffsetRange(t *testing.T) {
	result, err := Definition(&toolmocks.Runtime{
		GetSessionMessagesFunc: func(_ context.Context, req sessionmessages.SessionMessagesRequest) (sessionmessages.SessionMessagesResponse, error) {
			require.Equal(t, "session-2", req.SessionID)
			require.NotNil(t, req.OffsetStart)
			require.NotNil(t, req.OffsetEnd)
			require.Equal(t, 3, *req.OffsetStart)
			require.Equal(t, 5, *req.OffsetEnd)

			return sessionmessages.SessionMessagesResponse{
				SessionID: "session-2",
				Messages:  []sessionmessages.SessionMessageRecord{{MessageID: 21, Offset: 3, Role: "assistant", Content: "range"}},
				Truncated: true,
			}, nil
		},
	}).Handler.Invoke(context.Background(), tools.Call{
		Name:  "session_messages",
		Input: `{"session_id":"session-2","offset_start":3,"offset_end":5}`,
	})

	require.NoError(t, err)

	var payload sessionmessages.SessionMessagesResponse
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.True(t, payload.Truncated)
	require.Equal(t, uint(21), payload.Messages[0].MessageID)
}

func TestSessionMessages_ToolValidatesInputs(t *testing.T) {
	definition := Definition(&toolmocks.Runtime{})

	result, err := definition.Handler.Invoke(context.Background(), tools.Call{
		Name:  "session_messages",
		Input: `{"message_ids":[1],"anchor_message_id":2}`,
	})
	require.NoError(t, err)
	requireToolError(t, result.Error, "invalid_input", "exactly one session message selector must be provided")

	result, err = definition.Handler.Invoke(context.Background(), tools.Call{
		Name:  "session_messages",
		Input: `{"anchor_message_id":2,"before":-1}`,
	})
	require.NoError(t, err)
	requireToolError(t, result.Error, "invalid_input", "before and after must be greater than or equal to zero")

	result, err = definition.Handler.Invoke(context.Background(), tools.Call{
		Name:  "session_messages",
		Input: `{"message_ids":[` + strings.Repeat("1,", 50) + `51]}`,
	})
	require.NoError(t, err)
	requireToolError(t, result.Error, "invalid_input", "message_ids must contain at most 50 ids")

	result, err = definition.Handler.Invoke(context.Background(), tools.Call{
		Name:  "session_messages",
		Input: `{"anchor_message_id":2,"before":` + strconv.Itoa(maxAnchorWindowSize+1) + `}`,
	})
	require.NoError(t, err)
	requireToolError(t, result.Error, "invalid_input", fmt.Sprintf("before must be less than or equal to %d", maxAnchorWindowSize))

	result, err = definition.Handler.Invoke(context.Background(), tools.Call{
		Name:  "session_messages",
		Input: `{"offset_start":0,"offset_end":` + strconv.Itoa(maxOffsetRangeSize+1) + `}`,
	})
	require.NoError(t, err)
	requireToolError(t, result.Error, "invalid_input", fmt.Sprintf("offset range must include at most %d messages", maxOffsetRangeSize))
}

func TestSessionMessages_ToolClampsMaxChars(t *testing.T) {
	result, err := Definition(&toolmocks.Runtime{
		GetSessionMessagesFunc: func(_ context.Context, req sessionmessages.SessionMessagesRequest) (sessionmessages.SessionMessagesResponse, error) {
			require.Equal(t, 16000, req.MaxChars)
			return sessionmessages.SessionMessagesResponse{SessionID: "default"}, nil
		},
	}).Handler.Invoke(context.Background(), tools.Call{
		Name:  "session_messages",
		Input: `{"message_ids":[1],"max_chars":999999}`,
	})

	require.NoError(t, err)
	require.JSONEq(t, `{"session_id":"default","messages":null}`, result.Output)
}

func TestSessionMessages_ToolRejectsMalformedInput(t *testing.T) {
	result, err := Definition(&toolmocks.Runtime{}).Handler.Invoke(context.Background(), tools.Call{
		Name:  "session_messages",
		Input: `{`,
	})

	require.NoError(t, err)
	requireToolError(t, result.Error, "invalid_input", "invalid tool input")
}

func TestSessionMessages_ToolReturnsRuntimeError(t *testing.T) {
	result, err := Definition(&toolmocks.Runtime{
		GetSessionMessagesFunc: func(context.Context, sessionmessages.SessionMessagesRequest) (sessionmessages.SessionMessagesResponse, error) {
			return sessionmessages.SessionMessagesResponse{}, errors.New("boom")
		},
	}).Handler.Invoke(context.Background(), tools.Call{
		Name:  "session_messages",
		Input: `{"message_ids":[1]}`,
	})

	require.NoError(t, err)
	requireToolError(t, result.Error, "tool_error", "boom")
}

func TestSessionMessages_ToolRequiresRuntime(t *testing.T) {
	result, err := Definition(nil).Handler.Invoke(context.Background(), tools.Call{
		Name:  "session_messages",
		Input: `{"message_ids":[1]}`,
	})

	require.NoError(t, err)
	requireToolError(t, result.Error, "tool_error", "session messages is not configured")
}

func TestNormalizeRequest(t *testing.T) {
	t.Run("returns selector error", func(t *testing.T) {
		req, err := normalizeRequest(sessionmessages.SessionMessagesRequest{})

		require.Equal(t, sessionmessages.SessionMessagesRequest{}, req)
		require.EqualError(t, err, "exactly one session message selector must be provided")
	})

	t.Run("rejects after above max anchor window size", func(t *testing.T) {
		req, err := normalizeRequest(sessionmessages.SessionMessagesRequest{
			AnchorMessageID: 1,
			After:           maxAnchorWindowSize + 1,
		})

		require.Equal(t, sessionmessages.SessionMessagesRequest{
			AnchorMessageID: 1,
			After:           maxAnchorWindowSize + 1,
			MaxChars:        defaultMaxChars,
		}, req)
		require.EqualError(t, err, fmt.Sprintf("after must be less than or equal to %d", maxAnchorWindowSize))
	})
}

func requireToolError(t *testing.T, raw, code, message string) {
	t.Helper()
	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(raw), &toolErr))
	require.Equal(t, code, toolErr.Code)
	require.Equal(t, message, toolErr.Message)
}
