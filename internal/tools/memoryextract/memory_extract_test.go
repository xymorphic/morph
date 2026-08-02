package memoryextract

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/instructions"
	"github.com/xymorphic/morph/internal/memory/episodic"
	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/internal/tools"
	toolmocks "github.com/xymorphic/morph/internal/tools/mocks"
)

func TestMemoryExtract_DefinitionIncludesUsageInstruction(t *testing.T) {
	definition := Definition(&toolmocks.Runtime{})

	require.Equal(t, instructions.BuildMemoryExtractGuidance(), definition.UsageInstruction)
	require.Equal(t, permissions.Operation{
		Resource: permissions.ResourceMemory,
		Action:   permissions.ActionCreate,
		Effects:  []permissions.Effect{permissions.EffectRead, permissions.EffectWrite},
	}, definition.Permission)
}

func TestMemoryExtract_DefinitionExtractsEpisodes(t *testing.T) {
	start := 2
	end := 8
	var captured episodic.Request
	runtime := &toolmocks.Runtime{
		ExtractEpisodesFunc: func(_ context.Context, req episodic.Request) (episodic.Result, error) {
			captured = req
			return episodic.Result{
				SessionID:      "default",
				CandidateCount: 1,
				WriteCount:     1,
			}, nil
		},
	}
	recorder := &toolmocks.TraceRecorder{}
	ctx := tools.WithTraceRecorder(context.Background(), recorder)

	result, err := Definition(runtime).Handler.Invoke(ctx, tools.Call{
		Name: "memory_extract",
		Input: `{
			"session_id":"default",
			"offset_start":2,
			"offset_end":8,
			"window_size":4,
			"max_windows":3,
			"max_window_chars":1200,
			"max_window_tokens":300
		}`,
	})

	require.NoError(t, err)
	require.Empty(t, result.Error)
	require.Equal(t, "default", captured.SessionID)
	require.Equal(t, &start, captured.OffsetStart)
	require.Equal(t, &end, captured.OffsetEnd)
	require.Equal(t, 4, captured.WindowSize)
	require.Equal(t, 3, captured.MaxWindows)
	require.Equal(t, 1200, captured.MaxWindowChars)
	require.Equal(t, 300, captured.MaxWindowTokens)
	require.Equal(t, "tool", captured.Trigger)
	require.Same(t, recorder, captured.Trace)

	var output episodic.Result
	require.NoError(t, json.Unmarshal([]byte(result.Output), &output))
	require.Equal(t, 1, output.CandidateCount)
	require.Equal(t, 1, output.WriteCount)
}

func TestMemoryExtract_DefinitionAppliesDefaultsAndBounds(t *testing.T) {
	var captured episodic.Request
	runtime := &toolmocks.Runtime{
		ExtractEpisodesFunc: func(_ context.Context, req episodic.Request) (episodic.Result, error) {
			captured = req
			return episodic.Result{}, nil
		},
	}

	result, err := Definition(runtime).Handler.Invoke(context.Background(), tools.Call{
		Name:  "memory_extract",
		Input: `{"window_size":1000,"max_windows":1000,"max_window_chars":100000,"max_window_tokens":100000}`,
	})

	require.NoError(t, err)
	require.Empty(t, result.Error)
	require.Equal(t, episodic.MaxWindowSize, captured.WindowSize)
	require.Equal(t, episodic.MaxWindows, captured.MaxWindows)
	require.Equal(t, episodic.MaxWindowChars, captured.MaxWindowChars)
	require.Equal(t, episodic.MaxWindowTokens, captured.MaxWindowTokens)

	result, err = Definition(runtime).Handler.Invoke(context.Background(), tools.Call{
		Name:  "memory_extract",
		Input: `{}`,
	})

	require.NoError(t, err)
	require.Empty(t, result.Error)
	require.Equal(t, episodic.DefaultWindowSize, captured.WindowSize)
	require.Equal(t, episodic.DefaultMaxWindows, captured.MaxWindows)
	require.Equal(t, episodic.DefaultMaxWindowChars, captured.MaxWindowChars)
	require.Equal(t, episodic.DefaultMaxWindowTokens, captured.MaxWindowTokens)
}

func TestMemoryExtract_DefinitionUsesCurrentSessionWhenSessionIDIsOmitted(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "omitted", input: `{}`},
		{name: "current alias", input: `{"session_id":"current"}`},
		{name: "current session alias", input: `{"session_id":"current_session"}`},
		{name: "default session alias", input: `{"session_id":"default_session"}`},
		{name: "explicit session", input: `{"session_id":"session-42"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured episodic.Request
			runtime := &toolmocks.Runtime{
				ExtractEpisodesFunc: func(_ context.Context, req episodic.Request) (episodic.Result, error) {
					captured = req
					return episodic.Result{}, nil
				},
			}
			ctx := tools.WithSessionID(context.Background(), "ses_current")

			result, err := Definition(runtime).Handler.Invoke(ctx, tools.Call{
				Name:  "memory_extract",
				Input: tt.input,
			})

			require.NoError(t, err)
			require.Empty(t, result.Error)
			switch tt.name {
			case "default session alias":
				require.Equal(t, "default", captured.SessionID)
			case "explicit session":
				require.Equal(t, "session-42", captured.SessionID)
			default:
				require.Equal(t, "ses_current", captured.SessionID)
			}
		})
	}
}

func TestMemoryExtract_DefinitionValidatesInput(t *testing.T) {
	definition := Definition(&toolmocks.Runtime{})

	tests := []struct {
		name    string
		input   string
		message string
	}{
		{name: "negative start", input: `{"offset_start":-1}`, message: "offset_start must be greater than or equal to zero"},
		{name: "negative end", input: `{"offset_end":-1}`, message: "offset_end must be greater than or equal to zero"},
		{name: "empty explicit range", input: `{"offset_start":0,"offset_end":0}`, message: "offset_end must be greater than offset_start"},
		{name: "end before start", input: `{"offset_start":3,"offset_end":2}`, message: "offset_end must be greater than offset_start"},
		{name: "negative window", input: `{"window_size":-1}`, message: "window_size must be greater than or equal to 0"},
		{name: "negative windows", input: `{"max_windows":-1}`, message: "max_windows must be greater than or equal to 0"},
		{name: "negative chars", input: `{"max_window_chars":-1}`, message: "max_window_chars must be greater than or equal to 0"},
		{name: "negative tokens", input: `{"max_window_tokens":-1}`, message: "max_window_tokens must be greater than or equal to 0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := definition.Handler.Invoke(context.Background(), tools.Call{Name: "memory_extract", Input: tt.input})

			require.NoError(t, err)
			requireToolError(t, result.Error, "invalid_input", tt.message)
		})
	}
}

func TestMemoryExtract_DefinitionReturnsToolErrors(t *testing.T) {
	result, err := Definition(nil).Handler.Invoke(context.Background(), tools.Call{
		Name:  "memory_extract",
		Input: `{}`,
	})
	require.NoError(t, err)
	requireToolError(t, result.Error, "tool_error", "memory extraction is not configured")

	result, err = Definition(&toolmocks.Runtime{}).Handler.Invoke(context.Background(), tools.Call{
		Name:  "memory_extract",
		Input: `{`,
	})
	require.NoError(t, err)
	requireToolError(t, result.Error, "invalid_input", "invalid tool input")

	result, err = Definition(&toolmocks.Runtime{
		ExtractEpisodesFunc: func(context.Context, episodic.Request) (episodic.Result, error) {
			return episodic.Result{}, errors.New("extract failed")
		},
	}).Handler.Invoke(context.Background(), tools.Call{
		Name:  "memory_extract",
		Input: `{}`,
	})
	require.NoError(t, err)
	requireToolError(t, result.Error, "tool_error", "extract failed")
}

func requireToolError(t *testing.T, raw string, code string, message string) {
	t.Helper()

	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(raw), &toolErr))
	require.Equal(t, code, toolErr.Code)
	require.Equal(t, message, toolErr.Message)
}
