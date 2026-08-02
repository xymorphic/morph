package inspect

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	models "github.com/xymorphic/morph/internal/model"
	storage "github.com/xymorphic/morph/internal/state/core"
	morphtrace "github.com/xymorphic/morph/internal/trace"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
)

func Test_Store_ListSessions_BuildsSummariesAndDetail(t *testing.T) {
	dir := t.TempDir()
	writeTraceFile(t, dir, "20260329T002738.170520000Z-4fca4857", []any{
		morphtrace.Event{
			SessionID: "4fca4857",
			Type:      morphtrace.EvtChatStarted,
			Timestamp: time.Date(2026, 3, 29, 0, 27, 38, 171258000, time.UTC),
			Payload: morphtrace.Metadata{
				AgentName: "Daemon",
				Model:     "qwen/qwen3.5-27b",
				API:       "openai-completions",
				Source:    "agent",
				TraceDir:  ".morph/traces",
			},
		},
		morphtrace.Event{
			SessionID: "4fca4857",
			Type:      morphtrace.EvtUserMessageAccepted,
			Timestamp: time.Date(2026, 3, 29, 0, 27, 38, 171671000, time.UTC),
			Payload:   map[string]any{"message": "List files in the root"},
		},
		morphtrace.Event{
			SessionID: "4fca4857",
			Type:      morphtrace.EvtModelRequest,
			Timestamp: time.Date(2026, 3, 29, 0, 27, 38, 171759000, time.UTC),
			Payload: models.Request{
				Model:        "qwen/qwen3.5-27b",
				API:          "openai-completions",
				Instructions: "Daemon is the user's personal agent.",
				Messages: []morphmsg.Message{
					{
						Role:      morphmsg.RoleUser,
						Content:   "List files in the root",
						CreatedAt: time.Date(2026, 3, 29, 0, 27, 38, 171668000, time.UTC),
					},
				},
				Tools: []models.ToolDefinition{
					{Name: "list_files", Description: "List files and directories under an allowed workspace root."},
				},
			},
		},
		morphtrace.Event{
			SessionID: "4fca4857",
			Type:      morphtrace.EvtModelResponse,
			Timestamp: time.Date(2026, 3, 29, 0, 27, 41, 430260000, time.UTC),
			Payload: models.Response{
				ID:                "gen-1",
				Model:             "qwen/qwen3.5-27b-20260224",
				RequiresToolCalls: true,
				ToolCalls:         []models.ToolCall{{ID: "call-1", Name: "list_files", Input: "{}"}},
			},
		},
		morphtrace.Event{
			SessionID: "4fca4857",
			Type:      morphtrace.EvtModelRequest,
			Timestamp: time.Date(2026, 3, 29, 0, 27, 45, 171759000, time.UTC),
			Payload: models.Request{
				Model:       "qwen/qwen3.5-27b",
				API:         "openai-completions",
				Messages:    []morphmsg.Message{{Role: morphmsg.RoleTool, Content: `{"entries":[]}`}},
				Temperature: 0,
			},
		},
		morphtrace.Event{
			SessionID: "4fca4857",
			Type:      morphtrace.EvtModelResponse,
			Timestamp: time.Date(2026, 3, 29, 0, 27, 46, 430260000, time.UTC),
			Payload: models.Response{
				ID:         "gen-2",
				Model:      "qwen/qwen3.5-27b-20260224",
				OutputText: "Here are the files and directories in the root.",
			},
		},
		morphtrace.Event{
			SessionID: "4fca4857",
			Type:      morphtrace.EvtToolInvocationStarted,
			Timestamp: time.Date(2026, 3, 29, 0, 27, 41, 430685000, time.UTC),
			Payload:   models.ToolCall{ID: "call-1", Name: "list_files", Input: "{}"},
		},
		morphtrace.Event{
			SessionID: "4fca4857",
			Type:      morphtrace.EvtToolInvocationCompleted,
			Timestamp: time.Date(2026, 3, 29, 0, 27, 41, 447625000, time.UTC),
			Payload: morphmsg.Message{
				Role:       morphmsg.RoleTool,
				Name:       "list_files",
				ToolCallID: "call-1",
				Content:    `{"name":"list_files","output":"{\"entries\":[]}"}`,
				CreatedAt:  time.Date(2026, 3, 29, 0, 27, 41, 447625000, time.UTC),
			},
		},
		morphtrace.Event{
			SessionID: "4fca4857",
			Type:      morphtrace.EvtFinalAssistantResponse,
			Timestamp: time.Date(2026, 3, 29, 0, 27, 47, 273707000, time.UTC),
			Payload:   map[string]any{"message": "Here are the files and directories in the root."},
		},
	})

	writeTraceFile(t, dir, "20260330T002738.170520000Z-failed", []any{
		morphtrace.Event{
			SessionID: "failed",
			Type:      morphtrace.EvtChatStarted,
			Timestamp: time.Date(2026, 3, 30, 0, 27, 38, 171258000, time.UTC),
			Payload: morphtrace.Metadata{
				AgentName: "Daemon",
				Model:     "qwen/qwen3.5-27b",
				API:       "openai-completions",
				Source:    "agent",
			},
		},
		morphtrace.Event{
			SessionID: "failed",
			Type:      morphtrace.EvtSessionFailed,
			Timestamp: time.Date(2026, 3, 30, 0, 27, 39, 171258000, time.UTC),
			Payload:   map[string]any{"error": "tool failed"},
		},
	})

	store := NewStore(dir)
	summaries, err := store.ListSessions()

	require.NoError(t, err)
	require.Len(t, summaries, 2)
	require.Equal(t, "failed", summaries[0].ID)
	require.Equal(t, "failed", summaries[0].FinalStatus)
	require.Equal(t, "4fca4857", summaries[1].ID)
	require.Equal(t, "completed", summaries[1].FinalStatus)
	require.Equal(t, "Daemon", summaries[1].AgentName)

	detail, err := store.GetSession("4fca4857")
	require.NoError(t, err)
	require.Empty(t, detail.LoadError)
	require.Equal(t, "completed", detail.Summary.FinalStatus)
	require.Len(t, detail.Timeline, 9)
	require.NotNil(t, detail.Timeline[2].ModelRequest)
	require.Equal(t, 1, detail.Timeline[2].ModelRequest.Sequence)
	require.Equal(t, 36, detail.Timeline[2].ModelRequest.Context.InstructionChars)
	require.Equal(t, 1, detail.Timeline[2].ModelRequest.Context.MessageCount)
	require.Equal(t, len("List files in the root"), detail.Timeline[2].ModelRequest.Context.MessageChars)
	require.Equal(t, 1, detail.Timeline[2].ModelRequest.Context.ToolCount)
	require.Equal(t, 0, detail.Timeline[2].ModelRequest.Context.ToolCallCount)
	require.NotNil(t, detail.Timeline[3].ModelResponse)
	require.Equal(t, 1, detail.Timeline[3].ModelResponse.Sequence)
	require.NotNil(t, detail.Timeline[4].ModelRequest)
	require.Equal(t, 2, detail.Timeline[4].ModelRequest.Sequence)
	require.NotNil(t, detail.Timeline[5].ModelResponse)
	require.Equal(t, 2, detail.Timeline[5].ModelResponse.Sequence)
	require.NotNil(t, detail.Timeline[6].ToolInvocation)
	require.NotNil(t, detail.Timeline[7].ToolInvocation)
	require.Equal(t, 7, *detail.Timeline[6].ToolInvocation.PairIndex)
	require.Equal(t, 6, *detail.Timeline[7].ToolInvocation.PairIndex)
}

func Test_Store_GetSession_BuildsSafetyEventWithoutRawContent(t *testing.T) {
	dir := t.TempDir()
	writeTraceFile(t, dir, "20260515T002738.170520000Z-default", []any{
		morphtrace.Event{
			SessionID: "default",
			Type:      morphtrace.EvtOutputSafetyApplied,
			Timestamp: time.Date(2026, 5, 15, 0, 27, 38, 171258000, time.UTC),
			Payload: map[string]any{
				"session_id":     "default",
				"source":         "assistant",
				"action":         "blocked",
				"content_length": 42,
				"blocked":        true,
				"redacted":       false,
				"findings": []map[string]string{{
					"id":       "output_prompt_leak",
					"category": "hidden_or_obfuscated_instruction",
					"source":   "assistant",
				}},
			},
		},
	})

	detail, err := NewStore(dir).GetSession("default")

	require.NoError(t, err)
	require.Len(t, detail.Timeline, 1)
	event := detail.Timeline[0]
	require.NotNil(t, event.SafetyEvent)
	require.Equal(t, "assistant", event.SafetyEvent.Source)
	require.Equal(t, "blocked", event.SafetyEvent.Action)
	require.Equal(t, 42, event.SafetyEvent.ContentLength)
	require.True(t, event.SafetyEvent.Blocked)
	require.False(t, event.SafetyEvent.Redacted)
	require.Empty(t, event.GenericPayloadRaw)
	require.NotContains(t, event.Raw, "Environment Context")
	require.Contains(t, event.SafetyEvent.Findings, map[string]string{
		"id":       "output_prompt_leak",
		"category": "hidden_or_obfuscated_instruction",
		"source":   "assistant",
	})
}

func Test_Store_ListSessions_SortsTiesByIDDescending(t *testing.T) {
	dir := t.TempDir()
	timestamp := time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC)
	writeTraceFile(t, dir, "aaa", []any{
		morphtrace.Event{SessionID: "aaa", Type: morphtrace.EvtChatStarted, Timestamp: timestamp, Payload: morphtrace.Metadata{AgentName: "A"}},
	})
	writeTraceFile(t, dir, "zzz", []any{
		morphtrace.Event{SessionID: "zzz", Type: morphtrace.EvtChatStarted, Timestamp: timestamp, Payload: morphtrace.Metadata{AgentName: "Z"}},
	})

	summaries, err := NewStore(dir).ListSessions()
	require.NoError(t, err)
	require.Len(t, summaries, 2)
	require.Equal(t, "zzz", summaries[0].ID)
	require.Equal(t, "aaa", summaries[1].ID)
}

func Test_Store_GetSession_DecodesWorkspaceRulesTruncatedEvent(t *testing.T) {
	dir := t.TempDir()
	writeTraceFile(t, dir, "rules", []any{
		morphtrace.Event{
			SessionID: "rules",
			Type:      morphtrace.EvtChatStarted,
			Timestamp: time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC),
			Payload: morphtrace.Metadata{
				AgentName: "Daemon",
				Model:     "openai/gpt-4o-mini",
				API:       "openai-responses",
				Source:    "agent",
			},
		},
		morphtrace.Event{
			SessionID: "rules",
			Type:      morphtrace.EvtWorkspaceRulesTruncated,
			Timestamp: time.Date(2026, 4, 7, 0, 0, 1, 0, time.UTC),
			Payload: map[string]any{
				"original_length":    24000,
				"truncated_length":   15000,
				"max_content_length": 15000,
				"marker":             "[... workspace rules truncated ...]",
			},
		},
	})

	detail, err := NewStore(dir).GetSession("rules")
	require.NoError(t, err)
	require.Len(t, detail.Timeline, 2)
	require.NotNil(t, detail.Timeline[1].WorkspaceRules)
	require.Equal(t, 24000, detail.Timeline[1].WorkspaceRules.OriginalLength)
	require.Equal(t, 15000, detail.Timeline[1].WorkspaceRules.TruncatedLength)
	require.Equal(t, 15000, detail.Timeline[1].WorkspaceRules.MaxContentLength)
	require.Equal(t, "[... workspace rules truncated ...]", detail.Timeline[1].WorkspaceRules.Marker)
}

func Test_Store_GetSession_DecodesPlanEvents(t *testing.T) {
	dir := t.TempDir()
	writeTraceFile(t, dir, "plan", []any{
		morphtrace.Event{
			SessionID: "plan",
			Type:      morphtrace.EvtChatStarted,
			Timestamp: time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC),
			Payload:   morphtrace.Metadata{AgentName: "Daemon", Model: "test-model", API: "openai-responses"},
		},
		morphtrace.Event{
			SessionID: "plan",
			Type:      morphtrace.EvtPlanHydrated,
			Timestamp: time.Date(2026, 4, 7, 0, 0, 1, 0, time.UTC),
			Payload: map[string]any{
				"session_id":     "plan",
				"steps":          []map[string]any{{"id": "step-1", "content": "Implement feature", "status": "in_progress"}},
				"summary":        map[string]any{"total": 1, "pending": 0, "in_progress": 1, "completed": 0, "cancelled": 0},
				"active_step_id": "step-1",
				"explanation":    "restored",
				"source":         "history",
			},
		},
	})

	detail, err := NewStore(dir).GetSession("plan")
	require.NoError(t, err)
	require.Len(t, detail.Timeline, 2)
	require.NotNil(t, detail.Timeline[1].PlanEvent)
	require.Equal(t, "plan", detail.Timeline[1].PlanEvent.SessionID)
	require.Equal(t, "step-1", detail.Timeline[1].PlanEvent.ActiveStepID)
	require.Equal(t, "history", detail.Timeline[1].PlanEvent.Source)
	require.Equal(t, "Implement feature", detail.Timeline[1].PlanEvent.Steps[0].Content)
}

func Test_Store_ListSessions_SortsOlderSessionsAfterNewerOnComparatorReversePath(t *testing.T) {
	dir := t.TempDir()
	writeTraceFile(t, dir, "older", []any{
		morphtrace.Event{SessionID: "older", Type: morphtrace.EvtChatStarted, Timestamp: time.Date(2026, 3, 28, 0, 0, 0, 0, time.UTC), Payload: morphtrace.Metadata{AgentName: "Older"}},
	})
	writeTraceFile(t, dir, "newer", []any{
		morphtrace.Event{SessionID: "newer", Type: morphtrace.EvtChatStarted, Timestamp: time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC), Payload: morphtrace.Metadata{AgentName: "Newer"}},
	})
	writeTraceFile(t, dir, "newest", []any{
		morphtrace.Event{SessionID: "newest", Type: morphtrace.EvtChatStarted, Timestamp: time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC), Payload: morphtrace.Metadata{AgentName: "Newest"}},
	})

	permutations := [][]string{
		{"older", "newer", "newest"},
		{"older", "newest", "newer"},
		{"newer", "older", "newest"},
		{"newer", "newest", "older"},
		{"newest", "older", "newer"},
		{"newest", "newer", "older"},
	}

	restoreReadDirectory(t)
	for _, ids := range permutations {
		readDirectory = func(string) ([]os.DirEntry, error) {
			entries := make([]os.DirEntry, 0, len(ids))
			for _, id := range ids {
				entries = append(entries, mustDirEntry(t, filepath.Join(dir, id+".jsonl")))
			}

			return entries, nil
		}

		summaries, err := NewStore(dir).ListSessions()
		require.NoError(t, err)
		require.Len(t, summaries, 3)
		require.Equal(t, "newest", summaries[0].ID)
		require.Equal(t, "newer", summaries[1].ID)
		require.Equal(t, "older", summaries[2].ID)
	}
}

func Test_Store_ListSessions_OrdersByLastActivityNotSessionStart(t *testing.T) {
	dir := t.TempDir()
	// Started earlier but last event is newer — should sort above the session that started later.
	writeTraceFile(t, dir, "a-long-session", []any{
		morphtrace.Event{
			SessionID: "a-long-session",
			Type:      morphtrace.EvtChatStarted,
			Timestamp: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			Payload:   morphtrace.Metadata{AgentName: "A"},
		},
		morphtrace.Event{
			SessionID: "a-long-session",
			Type:      morphtrace.EvtUserMessageAccepted,
			Timestamp: time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC),
			Payload:   map[string]any{"message": "still going"},
		},
	})
	writeTraceFile(t, dir, "b-recent-start", []any{
		morphtrace.Event{
			SessionID: "b-recent-start",
			Type:      morphtrace.EvtChatStarted,
			Timestamp: time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC),
			Payload:   morphtrace.Metadata{AgentName: "B"},
		},
		morphtrace.Event{
			SessionID: "b-recent-start",
			Type:      morphtrace.EvtFinalAssistantResponse,
			Timestamp: time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC),
			Payload:   map[string]any{"message": "done"},
		},
	})

	summaries, err := NewStore(dir).ListSessions()
	require.NoError(t, err)
	require.Len(t, summaries, 2)
	require.Equal(t, "a-long-session", summaries[0].ID)
	require.True(t, summaries[0].UpdatedAt.Equal(time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)))
	require.Equal(t, "b-recent-start", summaries[1].ID)
	require.True(t, summaries[1].UpdatedAt.Equal(time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC)))
}

func Test_LoadSessionFile_SurfacesMalformedJSONAsLoadError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("{\n"), 0o600))

	detail, err := LoadSessionFile(path)

	require.NoError(t, err)
	require.Equal(t, "load_error", detail.Summary.FinalStatus)
	require.Contains(t, detail.LoadError, "failed to parse line 1")
}

func Test_LoadSessionFile_RecordsSessionIDMismatchAndSummaryFallback(t *testing.T) {
	dir := t.TempDir()
	writeTraceFile(t, dir, "mismatch", []any{
		morphtrace.Event{
			SessionID: "different",
			Type:      morphtrace.EvtSummaryFallbackStarted,
			Timestamp: time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC),
			Payload:   map[string]any{"remaining": 0},
		},
	})

	detail, err := LoadSessionFile(filepath.Join(dir, "mismatch.jsonl"))

	require.NoError(t, err)
	require.Len(t, detail.Warnings, 1)
	require.Contains(t, detail.Warnings[0], "does not match")
	require.NotNil(t, detail.Timeline[0].SummaryFallback)
}

func Test_LoadSessionFile_HandlesGenericPayloadsAndInvalidStructuredPayloads(t *testing.T) {
	dir := t.TempDir()
	writeTraceFile(t, dir, "generic", []any{
		morphtrace.Event{
			SessionID: "generic",
			Type:      morphtrace.EvtChatStarted,
			Timestamp: time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC),
			Payload:   map[string]any{"agent_name": 99},
		},
		morphtrace.Event{
			SessionID: "generic",
			Type:      "mystery.event",
			Timestamp: time.Date(2026, 3, 29, 0, 0, 1, 0, time.UTC),
			Payload:   map[string]any{"raw": true},
		},
	})

	detail, err := LoadSessionFile(filepath.Join(dir, "generic.jsonl"))

	require.NoError(t, err)
	require.Empty(t, detail.Timeline[0].StartedMetadata)
	require.Contains(t, detail.Timeline[0].GenericPayloadRaw, `"agent_name":99`)
	require.Contains(t, detail.Timeline[1].GenericPayloadRaw, `"raw":true`)
}

func Test_LoadSessionFile_FinalStatusInProgressWhenUserMessageAfterCompletedTurn(t *testing.T) {
	dir := t.TempDir()
	writeTraceFile(t, dir, "20260101T000000.000000000Z-ses_turn", []any{
		morphtrace.Event{
			SessionID: "ses_turn",
			Type:      morphtrace.EvtFinalAssistantResponse,
			Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Payload:   map[string]any{"message": "first reply"},
		},
		morphtrace.Event{
			SessionID: "ses_turn",
			Type:      morphtrace.EvtUserMessageAccepted,
			Timestamp: time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC),
			Payload:   map[string]any{"message": "second turn"},
		},
	})

	detail, err := LoadSessionFile(filepath.Join(dir, "20260101T000000.000000000Z-ses_turn.jsonl"))
	require.NoError(t, err)
	require.Equal(t, "in_progress", detail.Summary.FinalStatus)
}

func Test_LoadSessionFile_FinalStatusCompletedWhenTurnEndsWithFinalResponse(t *testing.T) {
	dir := t.TempDir()
	writeTraceFile(t, dir, "20260101T000000.000000000Z-ses_done", []any{
		morphtrace.Event{
			SessionID: "ses_done",
			Type:      morphtrace.EvtUserMessageAccepted,
			Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Payload:   map[string]any{"message": "hello"},
		},
		morphtrace.Event{
			SessionID: "ses_done",
			Type:      morphtrace.EvtFinalAssistantResponse,
			Timestamp: time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC),
			Payload:   map[string]any{"message": "hi"},
		},
	})

	detail, err := LoadSessionFile(filepath.Join(dir, "20260101T000000.000000000Z-ses_done.jsonl"))
	require.NoError(t, err)
	require.Equal(t, "completed", detail.Summary.FinalStatus)
}

func Test_LoadSessionFile_ParsesContextSummaryAndCompactionEvents(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 4, 4, 10, 0, 0, 0, time.UTC)
	writeTraceFile(t, dir, "memory-events", []any{
		morphtrace.Event{
			SessionID: "memory-events",
			Type:      morphtrace.EvtContextPreflight,
			Timestamp: now,
			Payload: map[string]any{
				"source":               "anchored",
				"prompt_tokens":        144,
				"anchor_prompt_tokens": 120,
				"anchor_message_count": 8,
				"delta_prompt_tokens":  24,
				"context_limit":        1000,
				"trigger_threshold":    800,
				"warn_threshold":       650,
			},
		},
		morphtrace.Event{
			SessionID: "memory-events",
			Type:      morphtrace.EvtSummaryParseFailed,
			Timestamp: now.Add(time.Second),
			Payload: map[string]any{
				"session_id":           "memory-events",
				"source_end_offset":    14,
				"source_message_count": 22,
				"updated_at":           now.Add(time.Second),
				"error":                "summary requested tool calls",
			},
		},
		morphtrace.Event{
			SessionID: "memory-events",
			Type:      morphtrace.EvtContextCompactionRunning,
			Timestamp: now.Add(2 * time.Second),
			Payload: map[string]any{
				"session_id":           "memory-events",
				"status":               "running",
				"target_message_count": 22,
				"target_offset":        14,
				"requested_at":         now,
				"started_at":           now.Add(2 * time.Second),
			},
		},
	})

	detail, err := LoadSessionFile(filepath.Join(dir, "memory-events.jsonl"))

	require.NoError(t, err)
	require.Len(t, detail.Timeline, 3)
	require.NotNil(t, detail.Timeline[0].ContextEvent)
	require.Equal(t, "anchored", detail.Timeline[0].ContextEvent.Source)
	require.Equal(t, 144, detail.Timeline[0].ContextEvent.PromptTokens)
	require.Equal(t, 120, detail.Timeline[0].ContextEvent.AnchorPromptTokens)
	require.Equal(t, 8, detail.Timeline[0].ContextEvent.AnchorMessageCount)
	require.Equal(t, 24, detail.Timeline[0].ContextEvent.DeltaPromptTokens)
	require.Equal(t, 1000, detail.Timeline[0].ContextEvent.ContextLimit)
	require.Equal(t, 800, detail.Timeline[0].ContextEvent.TriggerThreshold)
	require.Equal(t, 650, detail.Timeline[0].ContextEvent.WarnThreshold)
	require.Empty(t, detail.Timeline[0].GenericPayloadRaw)

	require.NotNil(t, detail.Timeline[1].SummaryEvent)
	require.Equal(t, "memory-events", detail.Timeline[1].SummaryEvent.SessionID)
	require.Equal(t, 14, detail.Timeline[1].SummaryEvent.SourceEndOffset)
	require.Equal(t, 22, detail.Timeline[1].SummaryEvent.SourceMessageCount)
	require.Equal(t, "summary requested tool calls", detail.Timeline[1].SummaryEvent.Error)
	require.Empty(t, detail.Timeline[1].GenericPayloadRaw)

	require.NotNil(t, detail.Timeline[2].CompactionEvent)
	require.Equal(t, "memory-events", detail.Timeline[2].CompactionEvent.SessionID)
	require.Equal(t, "running", detail.Timeline[2].CompactionEvent.Status)
	require.Equal(t, 22, detail.Timeline[2].CompactionEvent.TargetMessageCount)
	require.Equal(t, 14, detail.Timeline[2].CompactionEvent.TargetOffset)
	require.True(t, detail.Timeline[2].CompactionEvent.RequestedAt.Equal(now))
	require.True(t, detail.Timeline[2].CompactionEvent.StartedAt.Equal(now.Add(2*time.Second)))
	require.Empty(t, detail.Timeline[2].GenericPayloadRaw)
}

func Test_App_Hand_ServesIndexAndSessionEndpoints(t *testing.T) {
	dir := t.TempDir()
	writeTraceFile(t, dir, "session", []any{
		morphtrace.Event{
			SessionID: "session",
			Type:      morphtrace.EvtChatStarted,
			Timestamp: time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC),
			Payload: morphtrace.Metadata{
				AgentName: "Daemon",
				Model:     "model",
				API:       "openai-completions",
			},
		},
	})

	app := NewApp(dir)
	handler := app.Handler()

	indexReq := httptest.NewRequest(http.MethodGet, "/", nil)
	indexRec := httptest.NewRecorder()
	handler.ServeHTTP(indexRec, indexReq)
	require.Equal(t, http.StatusOK, indexRec.Code)
	require.Contains(t, indexRec.Body.String(), "Trace Viewer")

	listReq := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)
	require.Equal(t, http.StatusOK, listRec.Code)
	require.Contains(t, listRec.Body.String(), "\"sessions\"")

	detailReq := httptest.NewRequest(http.MethodGet, "/api/sessions/session", nil)
	detailRec := httptest.NewRecorder()
	handler.ServeHTTP(detailRec, detailReq)
	require.Equal(t, http.StatusOK, detailRec.Code)
	require.Contains(t, detailRec.Body.String(), "\"summary\"")

	missingReq := httptest.NewRequest(http.MethodGet, "/api/sessions/missing", nil)
	missingRec := httptest.NewRecorder()
	handler.ServeHTTP(missingRec, missingReq)
	require.Equal(t, http.StatusNotFound, missingRec.Code)

	_, err := NewStore(dir).GetSession("../../etc/passwd")
	require.ErrorIs(t, err, os.ErrNotExist)
}

func Test_App_Hand_RequiresBasicAuthWhenConfigured(t *testing.T) {
	dir := t.TempDir()
	writeTraceFile(t, dir, "session", []any{
		morphtrace.Event{
			SessionID: "session",
			Type:      morphtrace.EvtChatStarted,
			Timestamp: time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC),
			Payload: morphtrace.Metadata{
				AgentName: "Daemon",
				Model:     "model",
				API:       "openai-completions",
			},
		},
	})

	app := NewApp(dir)
	app.SetBasicAuth("viewer", "secret")
	handler := app.Handler()

	unauthorizedReq := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	unauthorizedRec := httptest.NewRecorder()
	handler.ServeHTTP(unauthorizedRec, unauthorizedReq)
	require.Equal(t, http.StatusUnauthorized, unauthorizedRec.Code)
	require.Contains(t, unauthorizedRec.Header().Get("WWW-Authenticate"), "Basic")

	authorizedReq := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	authorizedReq.SetBasicAuth("viewer", "secret")
	authorizedRec := httptest.NewRecorder()
	handler.ServeHTTP(authorizedRec, authorizedReq)
	require.Equal(t, http.StatusOK, authorizedRec.Code)
	require.Contains(t, authorizedRec.Body.String(), "\"sessions\"")
}

func Test_App_Hand_AttachesProviderMemoriesForSession(t *testing.T) {
	dir := t.TempDir()
	writeTraceFile(t, dir, "session", []any{
		morphtrace.Event{
			SessionID: "session",
			Type:      morphtrace.EvtChatStarted,
			Timestamp: time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC),
			Payload:   morphtrace.Metadata{AgentName: "Daemon", Model: "model", API: "openai-completions"},
		},
	})

	app := NewApp(dir)
	app.SetMemoryProvider(sessionMemoryProviderStub{
		items: []storage.MemoryItem{
			{
				ID:     "mem_session",
				Kind:   storage.MemoryKindEpisodic,
				Status: storage.MemoryStatusCandidate,
				Title:  "Decision captured",
				Text:   "Use provider memory records.",
				Metadata: map[string]string{
					"candidate_kind":    "decision",
					"source_session_id": "session",
				},
				SourceLinks: []storage.MemorySourceLink{{
					SessionID: "session",
					Offsets:   []int{2, 3},
				}},
				Confidence: 0.82,
			},
			{
				ID:       "mem_other",
				Kind:     storage.MemoryKindEpisodic,
				Status:   storage.MemoryStatusCandidate,
				Metadata: map[string]string{"source_session_id": "other"},
			},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/session", nil)
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var detail SessionDetail
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &detail))
	require.NotNil(t, detail.Memories)
	require.Equal(t, "state", detail.Memories.Source)
	require.Len(t, detail.Memories.Items, 2)
	require.Equal(t, "mem_session", detail.Memories.Items[0].ID)
	require.Equal(t, []int{2, 3}, detail.Memories.Items[0].SourceLinks[0].Offsets)
}

func Test_App_AttachMemories_HandlesNilInputsAndProviderErrors(t *testing.T) {
	var nilApp *App
	detail := &SessionDetail{}

	nilApp.SetMemoryProvider(sessionMemoryProviderStub{})
	nilApp.attachMemories(context.Background(), "session", detail)
	require.Nil(t, detail.Memories)

	app := NewApp(t.TempDir())
	app.attachMemories(context.Background(), "session", nil)
	app.attachMemories(context.Background(), "session", detail)
	require.Nil(t, detail.Memories)

	expected := errors.New("memory load failed")
	app.SetMemoryProvider(sessionMemoryProviderStub{err: expected})
	memories, err := app.loadSessionMemories(context.Background(), "session")
	require.ErrorIs(t, err, expected)
	require.Nil(t, memories)

	app.attachMemories(context.Background(), "session", detail)
	require.NotNil(t, detail.Memories)
	require.Equal(t, "state", detail.Memories.Source)
	require.Equal(t, expected.Error(), detail.Memories.LoadError)
}

func Test_Store_ValidateAndResolvePath(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "trace.jsonl")
	require.NoError(t, os.WriteFile(filePath, []byte("{}\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte("{}\n"), 0o600))

	require.EqualError(t, (*Store)(nil).Validate(), "trace directory is required")
	require.EqualError(t, NewStore(" ").Validate(), "trace directory is required")
	require.EqualError(t, NewStore(filepath.Join(dir, "missing")).Validate(), `trace directory "`+filepath.Join(dir, "missing")+`" does not exist`)
	require.EqualError(t, NewStore(filePath).Validate(), `trace directory "`+filePath+`" is not a directory`)
	require.NoError(t, NewStore(dir).Validate())

	restoreStatPath(t)
	statPath = func(path string) (os.FileInfo, error) {
		if path == dir {
			return nil, fs.ErrPermission
		}

		return os.Stat(path)
	}
	require.ErrorContains(t, NewStore(dir).Validate(), "failed to access trace directory")

	_, err := getSessionPath("", "session")
	require.ErrorIs(t, err, os.ErrNotExist)

	validPath, err := getSessionPath(dir, "session")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "session.jsonl"), validPath)

	for _, id := range []string{"", " ", ".", "..", "../etc/passwd", `..\etc\passwd`, "nested/file", `nested\file`} {
		_, err = getSessionPath(dir, id)
		require.ErrorIs(t, err, os.ErrNotExist)
	}
}

func Test_Store_ListSessions_IgnoresNonJSONLAndGetSessionErrors(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "note.txt"), []byte("ignore"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "subdir"), 0o755))
	writeTraceFile(t, dir, "session", []any{
		morphtrace.Event{
			SessionID: "session",
			Type:      morphtrace.EvtChatStarted,
			Timestamp: time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC),
			Payload: morphtrace.Metadata{
				AgentName: "Daemon",
			},
		},
	})

	store := NewStore(dir)
	summaries, err := store.ListSessions()
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	require.Equal(t, filepath.Join(dir, "session.jsonl"), summaries[0].Path)

	_, err = store.GetSession("missing")
	require.ErrorIs(t, err, os.ErrNotExist)

	restoreReadDirectory(t)
	readDirectory = func(string) ([]os.DirEntry, error) {
		return nil, fs.ErrPermission
	}
	_, err = store.ListSessions()
	require.ErrorContains(t, err, "failed to read trace directory")

	restoreStatPath(t)
	sessionPath := filepath.Join(dir, "session.jsonl")
	statPath = func(path string) (os.FileInfo, error) {
		if path == sessionPath {
			return nil, fs.ErrPermission
		}

		return os.Stat(path)
	}
	_, err = store.GetSession("session")
	require.ErrorIs(t, err, fs.ErrPermission)

	restoreStatPath(t)
	statPath = func(path string) (os.FileInfo, error) {
		if path == sessionPath {
			return nil, os.ErrNotExist
		}

		return os.Stat(path)
	}
	_, err = store.GetSession("session")
	require.ErrorIs(t, err, os.ErrNotExist)

	restoreStatPath(t)
	readDirectory = os.ReadDir
	statPath = func(path string) (os.FileInfo, error) {
		if path == sessionPath {
			return nil, fs.ErrInvalid
		}

		return os.Stat(path)
	}
	_, err = store.ListSessions()
	require.ErrorIs(t, err, fs.ErrInvalid)

	restoreOpenPath(t)
	openPath = func(path string) (io.ReadCloser, error) {
		if path == sessionPath {
			return nil, fs.ErrInvalid
		}

		return os.Open(path)
	}
	_, err = store.ListSessions()
	require.ErrorIs(t, err, fs.ErrInvalid)
}

func Test_Utility_Helpers(t *testing.T) {
	require.Nil(t, toolCallsToToolCallViews(nil))
	require.Equal(t, []ToolCallView{{
		ID:    "call-1",
		Name:  "list_files",
		Input: "{}",
	}}, toolCallsToToolCallViews([]morphmsg.ToolCall{{
		ID:    "call-1",
		Name:  "list_files",
		Input: "{}",
	}}))

	require.Equal(t, "", compactJSON(nil))
	require.Equal(t, "not-json", compactJSON([]byte(" not-json ")))
	require.Equal(t, `{"a":1}`, compactJSON([]byte("{\n  \"a\": 1\n}")))
}

func Test_PairToolInvocations_IgnoresUnmatchedAndBlankIDs(t *testing.T) {
	timeline := []TimelineEvent{
		{ToolInvocation: &ToolInvocationView{Phase: "completed", ID: "missing"}},
		{ToolInvocation: &ToolInvocationView{Phase: "started", ID: "call-1"}},
		{ToolInvocation: &ToolInvocationView{Phase: "started", ID: " "}},
		{ToolInvocation: &ToolInvocationView{Phase: "completed", ID: "call-1"}},
	}

	pairToolInvocations(timeline)

	require.Nil(t, timeline[0].ToolInvocation.PairIndex)
	require.NotNil(t, timeline[1].ToolInvocation.PairIndex)
	require.Equal(t, 3, *timeline[1].ToolInvocation.PairIndex)
	require.Nil(t, timeline[2].ToolInvocation.PairIndex)
	require.NotNil(t, timeline[3].ToolInvocation.PairIndex)
	require.Equal(t, 1, *timeline[3].ToolInvocation.PairIndex)
}

func Test_App_AndAuth_Helpers(t *testing.T) {
	require.EqualError(t, (*App)(nil).Validate(), "trace app is required")

	app := &App{}
	require.EqualError(t, app.Validate(), "trace app is required")

	var nilApp *App
	nilApp.SetBasicAuth("user", "secret")
	require.False(t, nilApp.requiresAuth())
	require.True(t, nilApp.authorized(httptest.NewRequest(http.MethodGet, "/", nil)))

	app.SetBasicAuth(" viewer ", "secret")
	require.True(t, app.requiresAuth())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	require.False(t, app.authorized(req))

	req.SetBasicAuth("wrong", "secret")
	require.False(t, app.authorized(req))

	req.SetBasicAuth("viewer", "wrong")
	require.False(t, app.authorized(req))

	req.SetBasicAuth("viewer", "secret")
	require.True(t, app.authorized(req))
	require.NoError(t, NewApp(t.TempDir()).Validate())
}

func Test_App_Hand_ErrorPaths(t *testing.T) {
	missingDir := filepath.Join(t.TempDir(), "missing")
	app := NewApp(missingDir)
	handler := app.Handler()

	listReq := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)
	require.Equal(t, http.StatusInternalServerError, listRec.Code)
	require.Contains(t, listRec.Body.String(), "does not exist")

	emptyDetailReq := httptest.NewRequest(http.MethodGet, "/api/sessions/", nil)
	emptyDetailRec := httptest.NewRecorder()
	handler.ServeHTTP(emptyDetailRec, emptyDetailReq)
	require.Equal(t, http.StatusNotFound, emptyDetailRec.Code)

	detailReq := httptest.NewRequest(http.MethodGet, "/api/sessions/session", nil)
	detailRec := httptest.NewRecorder()
	handler.ServeHTTP(detailRec, detailReq)
	require.Equal(t, http.StatusInternalServerError, detailRec.Code)

	restoreReadAssetFile(t)
	readAssetFile = func(_ fs.FS, _ string) ([]byte, error) {
		return nil, fs.ErrNotExist
	}
	indexReq := httptest.NewRequest(http.MethodGet, "/", nil)
	indexRec := httptest.NewRecorder()
	NewApp(t.TempDir()).Handler().ServeHTTP(indexRec, indexReq)
	require.Equal(t, http.StatusInternalServerError, indexRec.Code)
}

func Test_LoadSessionFile_InputAndScannerErrors(t *testing.T) {
	_, err := LoadSessionFile(" ")
	require.EqualError(t, err, "trace session path is required")

	restoreStatPath(t)
	statPath = func(string) (os.FileInfo, error) {
		return nil, fs.ErrPermission
	}
	_, err = LoadSessionFile("blocked.jsonl")
	require.ErrorIs(t, err, fs.ErrPermission)

	dir := t.TempDir()
	path := filepath.Join(dir, "huge.jsonl")
	var line bytes.Buffer
	line.WriteString(`{"session_id":"huge","type":"chat.started","timestamp":"2026-03-29T00:00:00Z","payload":"`)
	line.WriteString(strings.Repeat("x", 8*1024*1024+1))
	line.WriteString("\"}\n")
	require.NoError(t, os.WriteFile(path, line.Bytes(), 0o600))

	_, err = LoadSessionFile(path)
	require.Error(t, err)

	restoreStatPath(t)
	restoreOpenPath(t)
	statPath = os.Stat
	openPath = func(string) (io.ReadCloser, error) {
		return nil, fs.ErrPermission
	}
	_, err = LoadSessionFile(path)
	require.ErrorIs(t, err, fs.ErrPermission)

	restoreStatPath(t)
	restoreOpenPath(t)
	statPath = os.Stat
	openPath = func(string) (io.ReadCloser, error) {
		return io.NopCloser(&failingReader{}), nil
	}
	_, err = LoadSessionFile(path)
	require.ErrorIs(t, err, fs.ErrInvalid)
}

func Test_LoadSessionFile_SkipsBlankLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blank.jsonl")
	content := "\n\n" + `{"session_id":"blank","type":"summary.fallback.started","timestamp":"2026-03-29T00:00:00Z","payload":{"remaining":0}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	detail, err := LoadSessionFile(path)
	require.NoError(t, err)
	require.Equal(t, 1, detail.Summary.EventCount)
	require.Len(t, detail.Timeline, 1)
}

func Test_LoadSessionFile_UsesFileModTimeWhenEventsHaveNoTimestamp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "untimed.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(`{"session_id":"untimed","type":"chat.started"}`+"\n"), 0o600))

	info, err := os.Stat(path)
	require.NoError(t, err)

	detail, err := LoadSessionFile(path)

	require.NoError(t, err)
	require.Equal(t, 1, detail.Summary.EventCount)
	require.True(t, detail.Summary.UpdatedAt.Equal(info.ModTime().UTC()))
}

func Test_LoadSessionFile_ReturnsDetailWithLoadErrorForMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("{bad json}\n"), 0o600))

	detail, err := LoadSessionFile(path)

	require.NoError(t, err)
	require.Equal(t, "broken", detail.Summary.ID)
	require.Equal(t, "load_error", detail.Summary.FinalStatus)
	require.Contains(t, detail.LoadError, "failed to parse line 1")
	require.Equal(t, detail.LoadError, detail.Summary.LoadError)
	require.False(t, detail.Summary.UpdatedAt.IsZero())
}

func Test_ApplyEvent_PreservesSummaryAndFallsBackToGenericPayload(t *testing.T) {
	detail := SessionDetail{
		Summary: SessionSummary{
			Model:       "existing-model",
			API:         "openai-responses",
			FinalStatus: "incomplete",
		},
	}
	timelineEvent := TimelineEvent{}

	requestPayload, err := json.Marshal(models.Request{
		Model: "new-model",
		API:   "openai-completions",
	})
	require.NoError(t, err)

	applyEvent(&detail, &timelineEvent, rawEvent{
		Type:    morphtrace.EvtModelRequest,
		Payload: requestPayload,
	})
	require.Equal(t, "existing-model", detail.Summary.Model)
	require.Equal(t, "openai-responses", detail.Summary.API)
	require.NotNil(t, timelineEvent.ModelRequest)

	timelineEvent = TimelineEvent{}
	applyEvent(&detail, &timelineEvent, rawEvent{
		Type:    morphtrace.EvtFinalAssistantResponse,
		Payload: []byte(`{"message":1}`),
	})
	require.Nil(t, timelineEvent.FinalResponse)
	require.Contains(t, timelineEvent.GenericPayloadRaw, `"message":1`)

	detail = SessionDetail{Summary: SessionSummary{FinalStatus: "incomplete"}}
	timelineEvent = TimelineEvent{}
	applyEvent(&detail, &timelineEvent, rawEvent{
		Type:    morphtrace.EvtModelRequest,
		Payload: requestPayload,
	})
	require.Equal(t, "new-model", detail.Summary.Model)
	require.Equal(t, "openai-completions", detail.Summary.API)
}

func Test_App_Hand_HandleSessionPermissionAndInternalErrors(t *testing.T) {
	dir := t.TempDir()
	writeTraceFile(t, dir, "session", []any{
		morphtrace.Event{
			SessionID: "session",
			Type:      morphtrace.EvtChatStarted,
			Timestamp: time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC),
			Payload: morphtrace.Metadata{
				AgentName: "Daemon",
			},
		},
	})

	sessionPath := filepath.Join(dir, "session.jsonl")
	restoreStatPath(t)
	statPath = func(path string) (os.FileInfo, error) {
		if path == sessionPath {
			return nil, fs.ErrPermission
		}

		return os.Stat(path)
	}

	forbiddenReq := httptest.NewRequest(http.MethodGet, "/api/sessions/session", nil)
	forbiddenRec := httptest.NewRecorder()
	NewApp(dir).Handler().ServeHTTP(forbiddenRec, forbiddenReq)
	require.Equal(t, http.StatusForbidden, forbiddenRec.Code)

	restoreStatPath(t)
	statPath = func(path string) (os.FileInfo, error) {
		if path == sessionPath {
			return nil, fs.ErrInvalid
		}

		return os.Stat(path)
	}

	internalReq := httptest.NewRequest(http.MethodGet, "/api/sessions/session", nil)
	internalRec := httptest.NewRecorder()
	NewApp(dir).Handler().ServeHTTP(internalRec, internalReq)
	require.Equal(t, http.StatusInternalServerError, internalRec.Code)
}

func restoreStatPath(t *testing.T) {
	t.Helper()
	original := statPath
	t.Cleanup(func() {
		statPath = original
	})
}

func restoreReadDirectory(t *testing.T) {
	t.Helper()
	original := readDirectory
	t.Cleanup(func() {
		readDirectory = original
	})
}

func restoreOpenPath(t *testing.T) {
	t.Helper()
	original := openPath
	t.Cleanup(func() {
		openPath = original
	})
}

func restoreReadAssetFile(t *testing.T) {
	t.Helper()
	original := readAssetFile
	t.Cleanup(func() {
		readAssetFile = original
	})
}

func mustDirEntry(t *testing.T, path string) os.DirEntry {
	t.Helper()
	entry, err := os.Stat(path)
	require.NoError(t, err)
	return statDirEntry{info: entry}
}

type statDirEntry struct {
	info os.FileInfo
}

func (e statDirEntry) Name() string               { return e.info.Name() }
func (e statDirEntry) IsDir() bool                { return e.info.IsDir() }
func (e statDirEntry) Type() fs.FileMode          { return e.info.Mode().Type() }
func (e statDirEntry) Info() (os.FileInfo, error) { return e.info, nil }

type failingReader struct {
	called bool
}

func (r *failingReader) Read(p []byte) (int, error) {
	if !r.called {
		r.called = true
		p[0] = '\n'
		return 1, nil
	}

	return 0, fs.ErrInvalid
}

type sessionMemoryProviderStub struct {
	items []storage.MemoryItem
	err   error
}

func (s sessionMemoryProviderStub) ListSessionMemories(context.Context, string) ([]storage.MemoryItem, error) {
	return s.items, s.err
}

func writeTraceFile(t *testing.T, dir, id string, events []any) {
	t.Helper()

	path := filepath.Join(dir, id+".jsonl")
	file, err := os.Create(path)
	require.NoError(t, err)
	defer func() { _ = file.Close() }()

	encoder := json.NewEncoder(file)
	for _, event := range events {
		require.NoError(t, encoder.Encode(event))
	}
}
