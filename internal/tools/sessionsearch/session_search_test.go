package sessionsearch

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	envtypes "github.com/xymorphic/morph/internal/environment/types"
	"github.com/xymorphic/morph/internal/instructions"
	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/internal/tools"
	toolmocks "github.com/xymorphic/morph/internal/tools/mocks"
)

func TestSessionSearch_DefinitionIncludesUsageInstruction(t *testing.T) {
	definition := Definition(&toolmocks.Runtime{})

	require.Equal(t, instructions.BuildSessionSearchGuidance(), definition.UsageInstruction)
	require.Equal(t, permissions.Operation{
		Resource: permissions.ResourceSession,
		Action:   permissions.ActionSearch,
		Effects:  []permissions.Effect{permissions.EffectRead},
	}, definition.Permission)
}

func TestSessionSearch_ToolSearchesExplicitSession(t *testing.T) {
	result, err := Definition(&toolmocks.Runtime{
		SearchSessionFunc: func(_ context.Context, req envtypes.SessionSearchRequest) ([]envtypes.SessionSearchResult, error) {
			require.Equal(t, "session-1", req.SessionID)
			require.Empty(t, req.IgnoreSessionID)
			require.Equal(t, "hello", req.Query)
			require.Equal(t, "tool", req.Role)
			require.Equal(t, "process", req.ToolName)
			require.Equal(t, 3, req.MaxResults)
			return []envtypes.SessionSearchResult{{
				SessionID:  "session-1",
				MatchCount: 1,
				Messages: []envtypes.SessionSearchMessageHit{{
					MessageID:     11,
					Role:          "tool",
					ToolName:      "process",
					CreatedAt:     "2026-04-15T12:00:00Z",
					Snippet:       "hello world",
					FullTextBytes: 11,
					MatchIndex:    0,
				}},
			}}, nil
		},
	}).Handler.Invoke(tools.WithSessionID(context.Background(), "session-1"), tools.Call{
		Name:  "session_search",
		Input: `{"session_id":"session-1","query":"hello","role":"tool","tool_name":"process","max_results":3}`,
	})

	require.NoError(t, err)
	var payload struct {
		Results []envtypes.SessionSearchResult `json:"results"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Len(t, payload.Results, 1)
	require.Equal(t, uint(11), payload.Results[0].Messages[0].MessageID)
}

func TestSessionSearch_ToolSearchesOtherSessionsWhenSessionIDIsOmitted(t *testing.T) {
	result, err := Definition(&toolmocks.Runtime{
		SearchSessionFunc: func(_ context.Context, req envtypes.SessionSearchRequest) ([]envtypes.SessionSearchResult, error) {
			require.Empty(t, req.SessionID)
			require.Equal(t, "session-1", req.IgnoreSessionID)
			require.Equal(t, "hello", req.Query)
			return nil, nil
		},
	}).Handler.Invoke(tools.WithSessionID(context.Background(), "session-1"), tools.Call{
		Name:  "session_search",
		Input: `{"query":"hello"}`,
	})

	require.NoError(t, err)
	require.JSONEq(t, `{"results":null}`, result.Output)
}

func TestSessionSearch_ToolValidatesInputs(t *testing.T) {
	definition := Definition(&toolmocks.Runtime{})

	result, err := definition.Handler.Invoke(context.Background(), tools.Call{
		Name:  "session_search",
		Input: `{}`,
	})
	require.NoError(t, err)
	requireToolError(t, result.Error, "invalid_input", "query is required")

	result, err = definition.Handler.Invoke(context.Background(), tools.Call{
		Name:  "session_search",
		Input: `{"query":"hello","role":"developer"}`,
	})
	require.NoError(t, err)
	requireToolError(t, result.Error, "invalid_input", `unsupported role "developer"`)
}

func TestSessionSearch_ToolRejectsMalformedInput(t *testing.T) {
	result, err := Definition(&toolmocks.Runtime{}).Handler.Invoke(context.Background(), tools.Call{
		Name:  "session_search",
		Input: `{`,
	})

	require.NoError(t, err)
	requireToolError(t, result.Error, "invalid_input", "invalid tool input")
}

func TestSessionSearch_ToolNormalizesTrimmedInputs(t *testing.T) {
	result, err := Definition(&toolmocks.Runtime{
		SearchSessionFunc: func(_ context.Context, req envtypes.SessionSearchRequest) ([]envtypes.SessionSearchResult, error) {
			require.Equal(t, "hello", req.Query)
			require.Equal(t, "assistant", req.Role)
			require.Equal(t, "process", req.ToolName)
			require.Equal(t, 0, req.MaxResults)
			return nil, nil
		},
	}).Handler.Invoke(context.Background(), tools.Call{
		Name:  "session_search",
		Input: `{"query":"  hello  ","role":" Assistant ","tool_name":" process "}`,
	})

	require.NoError(t, err)
	require.JSONEq(t, `{"results":null}`, result.Output)
}

func TestSessionSearch_ToolReturnsRuntimeError(t *testing.T) {
	result, err := Definition(&toolmocks.Runtime{
		SearchSessionFunc: func(context.Context, envtypes.SessionSearchRequest) ([]envtypes.SessionSearchResult, error) {
			return nil, errors.New("boom")
		},
	}).Handler.Invoke(context.Background(), tools.Call{
		Name:  "session_search",
		Input: `{"query":"hello"}`,
	})

	require.NoError(t, err)
	requireToolError(t, result.Error, "tool_error", "boom")
}

func TestSessionSearch_ToolRequiresRuntime(t *testing.T) {
	result, err := Definition(nil).Handler.Invoke(context.Background(), tools.Call{
		Name:  "session_search",
		Input: `{"query":"hello"}`,
	})

	require.NoError(t, err)
	requireToolError(t, result.Error, "tool_error", "session search is not configured")
}

func requireToolError(t *testing.T, raw, code, message string) {
	t.Helper()
	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(raw), &toolErr))
	require.Equal(t, code, toolErr.Code)
	require.Equal(t, message, toolErr.Message)
}
