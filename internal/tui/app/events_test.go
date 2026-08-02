package tui

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/guardrails"
	models "github.com/xymorphic/morph/internal/model"
	rpcclient "github.com/xymorphic/morph/internal/rpc/client"
	"github.com/xymorphic/morph/internal/trace"
	agent "github.com/xymorphic/morph/pkg/agent"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
)

func TestAgentEventToTUIMessage_ConvertsAssistantDelta(t *testing.T) {
	msg, ok := agentEventToTUIMessage(agent.Event{
		Kind:    agent.EventKindTextDelta,
		Channel: "assistant",
		Text:    "hello",
	})

	require.True(t, ok)
	require.Equal(t, assistantTextDeltaMsg{Channel: "assistant", Text: "hello"}, msg)
}

func TestAgentEventToTUIMessage_DefaultsMissingChannel(t *testing.T) {
	msg, ok := agentEventToTUIMessage(agent.Event{Kind: agent.EventKindTextDelta, Text: "hello"})

	require.True(t, ok)
	require.Equal(t, assistantTextDeltaMsg{Channel: "assistant", Text: "hello"}, msg)
}

func TestAgentEventToTUIMessage_ConvertsRPCClientEvent(t *testing.T) {
	msg, ok := agentEventToTUIMessage(rpcclient.Event{
		Kind:    agent.EventKindTextDelta,
		Channel: "assistant",
		Text:    "daemon delta",
	})

	require.True(t, ok)
	require.Equal(t, assistantTextDeltaMsg{Channel: "assistant", Text: "daemon delta"}, msg)
}

func TestAgentEventToTUIMessage_ConvertsRPCTraceEvent(t *testing.T) {
	traceEvent := trace.Event{
		Type:    trace.EvtSessionFailed,
		Payload: map[string]any{"error": "model unavailable"},
	}

	msg, ok := agentEventToTUIMessage(rpcclient.Event{
		Kind:       agent.EventKindTrace,
		TraceEvent: &traceEvent,
	})

	require.True(t, ok)
	require.Equal(t, sessionErrorMsg{Message: "model unavailable"}, msg)
}

func TestAgentEventToTUIMessage_PreservesWhitespaceDelta(t *testing.T) {
	msg, ok := agentEventToTUIMessage(agent.Event{
		Kind:    agent.EventKindTextDelta,
		Channel: "assistant",
		Text:    " ",
	})

	require.True(t, ok)
	require.Equal(t, assistantTextDeltaMsg{Channel: "assistant", Text: " "}, msg)
}

func TestAgentEventToTUIMessage_IgnoresZeroLengthDelta(t *testing.T) {
	msg, ok := agentEventToTUIMessage(agent.Event{
		Kind:    agent.EventKindTextDelta,
		Channel: "assistant",
	})

	require.False(t, ok)
	require.Nil(t, msg)
}

func TestTraceEventToTUIMessage_ConvertsUserMessageAccepted(t *testing.T) {
	msg, ok := traceEventToTUIMessage(trace.Event{
		Type:    trace.EvtUserMessageAccepted,
		Payload: map[string]any{"message": "hello"},
	})

	require.True(t, ok)
	require.Equal(t, userMessageAcceptedMsg{Text: "hello"}, msg)
}

func TestTraceEventToTUIMessage_ConvertsAssistantResponseCompleted(t *testing.T) {
	msg, ok := traceEventToTUIMessage(trace.Event{
		Type:    trace.EvtFinalAssistantResponse,
		Payload: map[string]any{"message": "done"},
	})

	require.True(t, ok)
	require.Equal(t, assistantResponseCompletedMsg{Text: "done"}, msg)
}

func TestTraceEventToTUIMessage_ConvertsReasoningCompleted(t *testing.T) {
	msg, ok := traceEventToTUIMessage(trace.Event{
		Type: trace.EvtModelReasoningCompleted,
		Payload: map[string]any{
			"duration_ms": float64(2500),
			"summary":     "Checked the current state.",
		},
	})

	require.True(t, ok)
	require.Equal(t, reasoningCompletedMsg{
		Duration: 2500 * time.Millisecond,
		Summary:  "Checked the current state.",
	}, msg)
}

func TestTraceEventToTUIMessage_ConvertsAutoCompactionEvent(t *testing.T) {
	msg, ok := traceEventToTUIMessage(trace.Event{
		Type: trace.EvtContextCompactionSucceeded,
		Payload: map[string]any{
			"session_id": "default",
			"status":     "succeeded",
			"auto":       true,
		},
	})

	require.True(t, ok)
	require.Equal(t, manualCompactionMsg{
		State: manualCompactionState{Status: "succeeded", Label: autoCompactionLabel},
	}, msg)
}

func TestTraceEventToTUIMessage_ConvertsToolInvocationStarted(t *testing.T) {
	msg, ok := traceEventToTUIMessage(trace.Event{
		Type: trace.EvtToolInvocationStarted,
		Payload: models.ToolCall{
			ID:    "call_1",
			Name:  "read_file",
			Input: `{"path":"notes.txt"}`,
		},
	})

	require.True(t, ok)
	require.Equal(t, toolInvocationStartedMsg{
		ID:     "call_1",
		Name:   "read_file",
		Detail: "read_file notes.txt",
	}, msg)
}

func TestTraceEventToTUIMessage_DerivesAutomationDetailFromTraceInput(t *testing.T) {
	msg, ok := traceEventToTUIMessage(trace.Event{
		Type: trace.EvtToolInvocationStarted,
		Payload: map[string]any{
			"id":    "call_1",
			"name":  "automation",
			"input": `{"action":"pause","id":"auto_q6fjD5VBDz4JsJMTbnTz0"}`,
		},
	})

	require.True(t, ok)
	require.Equal(t, toolInvocationStartedMsg{
		ID:     "call_1",
		Name:   "automation",
		Detail: "pause:auto_q6fjD5VBDz4JsJMTbnTz0",
	}, msg)
}

func TestTraceEventToTUIMessage_DerivesBrowserDetailFromTraceInput(t *testing.T) {
	msg, ok := traceEventToTUIMessage(trace.Event{
		Type: trace.EvtToolInvocationStarted,
		Payload: map[string]any{
			"id":    "call_1",
			"name":  "browser",
			"input": `{"action":"start","profile":"default"}`,
		},
	})

	require.True(t, ok)
	require.Equal(t, toolInvocationStartedMsg{
		ID:     "call_1",
		Name:   "browser",
		Detail: "start:Profile default",
	}, msg)
}

func TestTraceEventToTUIMessage_RedactsSensitiveBrowserInput(t *testing.T) {
	msg, ok := traceEventToTUIMessage(trace.Event{
		Type: trace.EvtToolInvocationStarted,
		Payload: map[string]any{
			"id":   "call_1",
			"name": "browser",
			"input": `{
				"action":"type","session_id":"browser_1","tab_id":"tab_1",
				"ref":"g1e7","text":"secret value"
			}`,
		},
	})

	require.True(t, ok)
	require.Equal(t, toolInvocationStartedMsg{
		ID:     "call_1",
		Name:   "browser",
		Detail: "type:Element g1e7",
	}, msg)
}

func TestTraceEventToTUIMessage_ConvertsStreamedPlanInvocationStarted(t *testing.T) {
	msg, ok := traceEventToTUIMessage(trace.Event{
		Type: trace.EvtToolInvocationStarted,
		Payload: map[string]any{
			"id":   "call_1",
			"name": "plan_tool",
			"plan_state": map[string]any{
				"operation":     "update",
				"changed_count": float64(3),
			},
		},
	})

	require.True(t, ok)
	require.Equal(t, toolInvocationStartedMsg{
		ID:   "call_1",
		Name: "plan_tool",
		PlanState: &trace.PlanToolState{
			Operation:    trace.PlanToolOperationUpdate,
			ChangedCount: 3,
		},
	}, msg)
}

func TestToolCallPayloadToTUIMessage_ConvertsMessageToolCall(t *testing.T) {
	msg, ok := toolCallPayloadToTUIMessage(morphmsg.ToolCall{
		ID:    " call_1 ",
		Name:  " read_file ",
		Input: `{"path":"notes.txt"}`,
	})

	require.True(t, ok)
	require.Equal(t, toolInvocationStartedMsg{
		ID:     "call_1",
		Name:   "read_file",
		Detail: "read_file notes.txt",
	}, msg)
}

func TestToolCallPayloadToTUIMessage_ExtractsGenericToolParams(t *testing.T) {
	msg, ok := toolCallPayloadToTUIMessage(models.ToolCall{
		ID:   "call_1",
		Name: "list_files",
		Input: `{
			"path": "/tmp/project",
			"recursive": true,
			"includeHidden": false,
			"maxEntries": 50,
			"empty": ""
		}`,
	})

	require.True(t, ok)
	require.Equal(t, toolInvocationStartedMsg{
		ID:     "call_1",
		Name:   "list_files",
		Detail: "list_files(includeHidden=false maxEntries=50 path=/tmp/project recursive=true)",
	}, msg)
}

func TestToolCallPayloadToTUIMessage_ExtractsFileToolDetails(t *testing.T) {
	cases := []struct {
		name     string
		toolName string
		input    string
		detail   string
	}{
		{
			name:     "read file",
			toolName: "read_file",
			input:    `{"path":"notes/file.txt"}`,
			detail:   "read_file notes/file.txt",
		},
		{
			name:     "write file",
			toolName: "write_file",
			input:    `{"path":"notes/file.txt","content":"SECRET=example"}`,
			detail:   "write_file notes/file.txt",
		},
		{
			name:     "patch",
			toolName: "patch",
			input:    `{"patch":"--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new\n"}`,
			detail:   "patch file.txt +1 -1",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			msg, ok := toolCallPayloadToTUIMessage(models.ToolCall{
				ID:    "call_1",
				Name:  tt.toolName,
				Input: tt.input,
			})

			require.True(t, ok)
			require.Equal(t, toolInvocationStartedMsg{
				ID:     "call_1",
				Name:   tt.toolName,
				Detail: tt.detail,
			}, msg)
			require.NotContains(t, fmt.Sprintf("%#v", msg), "SECRET=example")
		})
	}
}

func TestToolCallPayloadToTUIMessage_ShortensLongListFilesPath(t *testing.T) {
	msg, ok := toolCallPayloadToTUIMessage(models.ToolCall{
		ID:    "call_1",
		Name:  "list_files",
		Input: `{"path":"/Users/nedy/projects/xymorphic/morph/internal/tools/listfiles","recursive":true}`,
	})

	require.True(t, ok)
	require.Equal(t, toolInvocationStartedMsg{
		ID:     "call_1",
		Name:   "list_files",
		Detail: "list_files(path=/Users/nedy/projects/xymorphi/.../listfiles recursive=true)",
	}, msg)
}

func TestToolCallPayloadToTUIMessage_ExtractsRunCommandDetail(t *testing.T) {
	msg, ok := toolCallPayloadToTUIMessage(models.ToolCall{
		ID:    "call_1",
		Name:  "run_command",
		Input: `{"command":"sleep 10 && echo \"Done\"","timeout_seconds":8}`,
	})

	require.True(t, ok)
	require.Equal(t, toolInvocationStartedMsg{
		ID:     "call_1",
		Name:   "run_command",
		Detail: `sleep 10 && echo "Done" [timeout 8s]`,
	}, msg)
}

func TestToolCallPayloadToTUIMessage_ExtractsWebSearchDetail(t *testing.T) {
	msg, ok := toolCallPayloadToTUIMessage(models.ToolCall{
		ID:    "call_1",
		Name:  "web_search",
		Input: `{"query":"what is todays news about open source ai releases and model updates happening around the world"}`,
	})

	require.True(t, ok)
	require.Equal(t, toolInvocationStartedMsg{
		ID:     "call_1",
		Name:   "web_search",
		Detail: `Search "what is todays news about open source ai releases and model updates happening..."`,
	}, msg)
}

func TestToolCallPayloadToTUIMessage_ExtractsMemorySearchDetail(t *testing.T) {
	msg, ok := toolCallPayloadToTUIMessage(models.ToolCall{
		ID:    "call_1",
		Name:  "memory_search",
		Input: `{"query":"what does the user prefer for commit messages"}`,
	})

	require.True(t, ok)
	require.Equal(t, toolInvocationStartedMsg{
		ID:     "call_1",
		Name:   "memory_search",
		Detail: `Search "what does the user prefer for commit messages"`,
	}, msg)
}

func TestToolCallPayloadToTUIMessage_ExtractsSessionMessagesDetail(t *testing.T) {
	msg, ok := toolCallPayloadToTUIMessage(models.ToolCall{
		ID:   "call_1",
		Name: "session_messages",
		Input: `{
			"anchor_message_id": 42,
			"before": 2,
			"after": 3,
			"max_chars": 1200
		}`,
	})

	require.True(t, ok)
	require.Equal(t, toolInvocationStartedMsg{
		ID:     "call_1",
		Name:   "session_messages",
		Detail: "session_messages(anchor_message_id=42 before=2 after=3 max_chars=1200)",
	}, msg)
}

func TestToolCallPayloadToTUIMessage_ExtractsPlanDetail(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  *trace.PlanToolState
	}{
		{
			name:  "read",
			input: `{}`,
			want:  &trace.PlanToolState{Operation: trace.PlanToolOperationRead},
		},
		{
			name:  "update",
			input: `{"steps":[{"id":"step-1","content":"Inspect","status":"pending"}]}`,
			want: &trace.PlanToolState{
				Operation:    trace.PlanToolOperationUpdate,
				ChangedCount: 1,
			},
		},
		{
			name:  "clear completed",
			input: `{"steps":[{"id":"step-1","content":"Done","status":"completed"}],"clear_completed":true}`,
			want: &trace.PlanToolState{
				Operation:    trace.PlanToolOperationClearCompleted,
				ChangedCount: 1,
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			msg, ok := toolCallPayloadToTUIMessage(models.ToolCall{
				ID:    "call_1",
				Name:  "plan_tool",
				Input: tt.input,
			})

			require.True(t, ok)
			require.Equal(t, toolInvocationStartedMsg{
				ID:        "call_1",
				Name:      "plan_tool",
				PlanState: tt.want,
			}, msg)
		})
	}
}

func TestToolMessagePayloadToTUIMessage_ExtractsPlanDetail(t *testing.T) {
	msg, ok := toolMessagePayloadToTUIMessage(morphmsg.Message{
		Role:       morphmsg.RoleTool,
		Name:       "plan_tool",
		ToolCallID: "call_1",
		Content: `{
			"name": "plan_tool",
			"output": "{\"summary\":{\"total\":3,\"completed\":1},\"changes\":[{\"index\":2,\"id\":\"step-2\",\"action\":\"completed\",\"fields\":[\"status\"]}]}"
		}`,
	})

	require.True(t, ok)
	require.Equal(t, toolInvocationCompletedMsg{
		ID:   "call_1",
		Name: "plan_tool",
		PlanState: &trace.PlanToolState{
			TotalCount:     3,
			CompletedCount: 1,
			Changes: []trace.PlanToolChange{
				{Index: 2, ID: "step-2", Action: "completed", Fields: []string{"status"}},
			},
		},
	}, msg)
}

func TestToolCallPayloadToTUIMessage_ExtractsSearchFilesDetail(t *testing.T) {
	msg, ok := toolCallPayloadToTUIMessage(models.ToolCall{
		ID:    "call_1",
		Name:  "search_files",
		Input: `{"pattern":"println","path":".","max_results":10}`,
	})

	require.True(t, ok)
	require.Equal(t, toolInvocationStartedMsg{
		ID:     "call_1",
		Name:   "search_files",
		Detail: `Search "println" in . max_results=10`,
	}, msg)
}

func TestToolCallPayloadToTUIMessage_IgnoresPayloadWithoutIdentity(t *testing.T) {
	msg, ok := toolCallPayloadToTUIMessage(map[string]any{"input": `{"path":"secret.txt"}`})

	require.False(t, ok)
	require.Nil(t, msg)
}

func TestTraceEventToTUIMessage_ConvertsToolInvocationCompleted(t *testing.T) {
	msg, ok := traceEventToTUIMessage(trace.Event{
		Type: trace.EvtToolInvocationCompleted,
		Payload: morphmsg.Message{
			Name:       "read_file",
			ToolCallID: "call_1",
			Content:    "SECRET=example",
		},
	})

	require.True(t, ok)
	require.Equal(t, toolInvocationCompletedMsg{ID: "call_1", Name: "read_file"}, msg)
}

func TestTraceEventToTUIMessage_PreservesFailedToolOutcome(t *testing.T) {
	msg, ok := traceEventToTUIMessage(trace.Event{
		Type: trace.EvtToolInvocationCompleted,
		Payload: map[string]any{
			"tool_call_id": "call_1",
			"name":         "browser",
			"content":      `{"error":{"code":"browser_unavailable","message":"browser is unavailable","retryable":true}}`,
			"failed":       true,
		},
	})

	require.True(t, ok)
	require.Equal(t, toolInvocationCompletedMsg{
		ID: "call_1", Name: "browser", Failed: true,
		Failure: "Browser is unavailable · retryable",
	}, msg)
}

func TestTraceEventToTUIMessage_ConvertsStreamedPlanInvocationCompleted(t *testing.T) {
	msg, ok := traceEventToTUIMessage(trace.Event{
		Type: trace.EvtToolInvocationCompleted,
		Payload: map[string]any{
			"tool_call_id": "call_1",
			"name":         "plan_tool",
			"plan_state": map[string]any{
				"total_count":     float64(3),
				"completed_count": float64(1),
			},
		},
	})

	require.True(t, ok)
	require.Equal(t, toolInvocationCompletedMsg{
		ID:   "call_1",
		Name: "plan_tool",
		PlanState: &trace.PlanToolState{
			TotalCount:     3,
			CompletedCount: 1,
		},
	}, msg)
}

func TestToolMessagePayloadToTUIMessage_IgnoresPayloadWithoutIdentity(t *testing.T) {
	msg, ok := toolMessagePayloadToTUIMessage(map[string]any{"content": "SECRET=example"})

	require.False(t, ok)
	require.Nil(t, msg)
}

func TestTraceEventToTUIMessage_ConvertsMapToolEventsWithoutRawPayload(t *testing.T) {
	started, ok := traceEventToTUIMessage(trace.Event{
		Type: trace.EvtToolInvocationStarted,
		Payload: map[string]any{
			"id":    "call_1",
			"name":  "read_file",
			"input": `{"path":"secret.txt"}`,
		},
	})
	require.True(t, ok)
	require.Equal(t, toolInvocationStartedMsg{ID: "call_1", Name: "read_file"}, started)
	require.NotContains(t, fmt.Sprintf("%#v", started), "secret.txt")

	completed, ok := traceEventToTUIMessage(trace.Event{
		Type: trace.EvtToolInvocationCompleted,
		Payload: map[string]any{
			"tool_call_id": "call_1",
			"name":         "read_file",
			"content":      "SECRET=example",
		},
	})
	require.True(t, ok)
	require.Equal(t, toolInvocationCompletedMsg{ID: "call_1", Name: "read_file"}, completed)
	require.NotContains(t, fmt.Sprintf("%#v", completed), "SECRET=example")
}

func TestTraceEventToTUIMessage_ConvertsSafetyEventsWithoutRawPayload(t *testing.T) {
	msg, ok := traceEventToTUIMessage(trace.Event{
		Type: trace.EvtOutputSafetyApplied,
		Payload: map[string]any{
			"action":         "redacted",
			"blocked":        false,
			"redacted":       true,
			"refusal":        "safe refusal",
			"raw_content":    "SECRET=example",
			"content":        "developer instructions",
			"authorization":  "Bearer secret",
			"content_length": 42,
			"findings": []any{
				map[string]any{"id": "secret_exfiltration", "sample": "SECRET=example"},
			},
		},
	})

	require.True(t, ok)
	require.Equal(t, safetyEventMsg{
		Kind:       trace.EvtOutputSafetyApplied,
		Action:     "redacted",
		Refusal:    "safe refusal",
		Redacted:   true,
		FindingIDs: []string{"secret_exfiltration"},
	}, msg)
	displayPayload := fmt.Sprintf("%#v", msg)
	require.NotContains(t, displayPayload, "SECRET=example")
	require.NotContains(t, displayPayload, "Bearer secret")
}

func TestTraceEventToTUIMessage_ConvertsGuardrailSafetyFindingIDs(t *testing.T) {
	msg, ok := traceEventToTUIMessage(trace.Event{
		Type: trace.EvtInputSafetyBlocked,
		Payload: guardrails.SafetyTracePayload(guardrails.SafetyTracePayloadOptions{
			Action:  "blocked",
			Blocked: true,
			Findings: []guardrails.SafetyFinding{{
				ID:       guardrails.SafetyFindingPromptExfiltration,
				Category: guardrails.SafetyCategoryPromptExfiltration,
			}},
		}),
	})

	require.True(t, ok)
	require.Equal(t, safetyEventMsg{
		Kind:       trace.EvtInputSafetyBlocked,
		Action:     "blocked",
		Blocked:    true,
		FindingIDs: []string{string(guardrails.SafetyFindingPromptExfiltration)},
	}, msg)
}

func TestTraceEventToTUIMessage_ConvertsSessionError(t *testing.T) {
	msg, ok := traceEventToTUIMessage(trace.Event{
		Type:    trace.EvtSessionFailed,
		Payload: map[string]any{"error": "model unavailable"},
	})

	require.True(t, ok)
	require.Equal(t, sessionErrorMsg{Message: "model unavailable"}, msg)
}

func TestTraceEventToTUIMessage_IgnoresUserStoppedSessionError(t *testing.T) {
	for _, message := range []string{
		"context canceled",
		"context_canceled",
		"rpc error: code = Canceled desc = context canceled",
	} {
		msg, ok := traceEventToTUIMessage(trace.Event{
			Type:    trace.EvtSessionFailed,
			Payload: map[string]any{"error": message},
		})

		require.False(t, ok)
		require.Nil(t, msg)
	}
}

func TestTraceEventToTUIMessage_UsesFallbackPayloadKeys(t *testing.T) {
	userMsg, ok := traceEventToTUIMessage(trace.Event{
		Type:    trace.EvtUserMessageAccepted,
		Payload: map[string]any{"text": "hello"},
	})
	require.True(t, ok)
	require.Equal(t, userMessageAcceptedMsg{Text: "hello"}, userMsg)

	assistantMsg, ok := traceEventToTUIMessage(trace.Event{
		Type:    trace.EvtFinalAssistantResponse,
		Payload: map[string]any{"text": "done"},
	})
	require.True(t, ok)
	require.Equal(t, assistantResponseCompletedMsg{Text: "done"}, assistantMsg)

	errMsg, ok := traceEventToTUIMessage(trace.Event{
		Type:    trace.EvtSessionFailed,
		Payload: map[string]any{"message": "model unavailable"},
	})
	require.True(t, ok)
	require.Equal(t, sessionErrorMsg{Message: "model unavailable"}, errMsg)
}

func TestTraceEventToTUIMessage_IgnoresEmptyPayloadFields(t *testing.T) {
	for _, eventType := range []string{
		trace.EvtUserMessageAccepted,
		trace.EvtFinalAssistantResponse,
		trace.EvtSessionFailed,
	} {
		msg, ok := traceEventToTUIMessage(trace.Event{
			Type:    eventType,
			Payload: map[string]any{"message": " "},
		})

		require.False(t, ok)
		require.Nil(t, msg)
	}
}

func TestTraceEventToTUIMessage_IgnoresUnknownTraceEvent(t *testing.T) {
	msg, ok := traceEventToTUIMessage(trace.Event{
		Type:    trace.EvtModelRequest,
		Payload: map[string]any{"authorization": "Bearer secret"},
	})

	require.False(t, ok)
	require.Nil(t, msg)
}

func TestSafetyPayloadToTUIMessage_IgnoresEmptyKind(t *testing.T) {
	msg, ok := safetyPayloadToTUIMessage(" ", map[string]any{"blocked": true})

	require.False(t, ok)
	require.Nil(t, msg)
}

func TestPayloadHelpers_HandleMalformedAndConvertedPayloads(t *testing.T) {
	require.Nil(t, trace.PayloadFields(nil))
	require.Nil(t, trace.PayloadFields(make(chan int)))
	require.Nil(t, trace.PayloadFields("not an object"))
	require.Equal(t, map[string]any{"name": "read_file"}, trace.PayloadFields(map[string]any{"name": "read_file"}))

	fields := trace.PayloadFields(struct {
		Name string `json:"name"`
	}{
		Name: "read_file",
	})
	require.Equal(t, map[string]any{"name": "read_file"}, fields)
}

func TestGetSafetyFindingIDs_HandlesSupportedShapes(t *testing.T) {
	require.Nil(t, getSafetyFindingIDsFromTypedPayload(trace.SafetyEventPayload{}))

	ids := getSafetyFindingIDsFromTypedPayload(trace.SafetyEventPayload{
		Findings: []map[string]string{
			{"id": "secret_exfiltration"},
			{"id": " "},
		},
	})
	require.Equal(t, []string{"secret_exfiltration"}, ids)

	decoded, ok := trace.DecodePayload(trace.EvtInputSafetyBlocked, map[string]any{
		"findings": []any{map[string]any{"id": "prompt_exfiltration"}, map[string]any{"id": " "}},
	})
	require.True(t, ok)
	payload, ok := decoded.(trace.SafetyEventPayload)
	require.True(t, ok)
	ids = getSafetyFindingIDsFromTypedPayload(payload)
	require.Equal(t, []string{"prompt_exfiltration"}, ids)
}
