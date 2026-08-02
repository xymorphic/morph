package trace

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
)

func TestDecodePayload_CoversAllKnownEventTypes(t *testing.T) {
	payload := map[string]any{
		"id":                         "call_1",
		"name":                       "plan_tool",
		"tool_call_id":               "call_1",
		"input":                      `{"steps":[]}`,
		"content":                    `{"summary":{"total":3}}`,
		"message":                    "hello",
		"text":                       "hello",
		"error":                      "boom",
		"duration_ms":                123,
		"remaining_iterations":       1,
		"action":                     "block",
		"blocked":                    true,
		"redacted":                   true,
		"findings":                   []map[string]string{{"id": "test"}},
		"source":                     "test",
		"prompt_tokens":              10,
		"completion_tokens":          2,
		"total_tokens":               12,
		"context_limit":              100,
		"trigger_threshold":          80,
		"warn_threshold":             70,
		"session_id":                 "default",
		"source_end_offset":          3,
		"source_message_count":       5,
		"status":                     "running",
		"target_message_count":       3,
		"target_offset":              3,
		"original_length":            100,
		"truncated_length":           50,
		"max_content_length":         50,
		"marker":                     "...",
		"steps":                      []map[string]any{{"id": "step-1", "content": "Do it", "status": "pending"}},
		"summary":                    map[string]any{"total": 1, "pending": 1},
		"active_step_id":             "step-1",
		"background_run_id":          "run_1",
		"result_count":               1,
		"candidate_count":            2,
		"eligible":                   true,
		"episodic_checkpoint_offset": 4,
	}

	for _, eventType := range AllTraceEventTypes() {
		t.Run(eventType, func(t *testing.T) {
			eventPayload := payload
			if eventType == EvtModelReasoningCompleted {
				eventPayload = make(map[string]any, len(payload))
				for key, value := range payload {
					eventPayload[key] = value
				}
				eventPayload["summary"] = "Checked the current state."
			}

			decoded, ok := DecodePayload(eventType, eventPayload)
			require.True(t, ok)
			require.NotNil(t, decoded)
		})
	}
}

func TestDecodePayloadJSON_DecodesKnownPayload(t *testing.T) {
	raw := json.RawMessage(`{"message":"hello"}`)

	payload, ok := DecodePayloadJSON(EvtUserMessageAccepted, raw)

	require.True(t, ok)
	require.Equal(t, UserMessageAcceptedPayload{Message: "hello"}, payload)
}

func TestDecodePayload_DecodesPermissionDecision(t *testing.T) {
	payload, ok := DecodePayload(EvtPermissionDecisionObserved, map[string]any{
		"actor_kind":     "local_owner",
		"surface_kind":   "local",
		"surface":        "cli",
		"tool":           "write_file",
		"resource":       "file",
		"action":         "update",
		"effects":        []string{"write"},
		"decision":       "ask",
		"reason_code":    "surface_default",
		"rule":           "owner writes",
		"preset":         "ask",
		"owner_required": true,
		"network": map[string]any{
			"scheme": "https", "host": "example.com", "port": float64(443), "path": "/news",
			"method": "GET", "request_class": "navigation", "has_query": true,
		},
		"command": map[string]any{
			"mode": "direct", "executable": "git", "invocation_count": float64(1),
			"redirect_count": float64(0), "complete": false,
			"dynamic_reasons": []string{"dynamic_argument"},
		},
	})

	require.True(t, ok)
	require.Equal(t, PermissionDecisionPayload{
		ActorKind:     "local_owner",
		SurfaceKind:   "local",
		Surface:       "cli",
		Tool:          "write_file",
		Resource:      "file",
		Action:        "update",
		Effects:       []string{"write"},
		Decision:      "ask",
		ReasonCode:    "surface_default",
		Rule:          "owner writes",
		Preset:        "ask",
		OwnerRequired: true,
		Network: &PermissionNetworkTargetPayload{
			Scheme: "https", Host: "example.com", Port: 443, Path: "/news",
			Method: "GET", RequestClass: "navigation", HasQuery: true,
		},
		Command: &PermissionCommandTargetPayload{
			Mode: "direct", Executable: "git", InvocationCount: 1, Complete: false,
			DynamicReasons: []string{"dynamic_argument"},
		},
	}, payload)
}

func TestToolInvocationStartedPayloadFrom_DecodesStructAndMapPayloads(t *testing.T) {
	payload, ok := ToolInvocationStartedPayloadFrom(struct {
		ID    string
		Name  string
		Input string
	}{
		ID:    "call_1",
		Name:  "plan_tool",
		Input: `{"steps":[]}`,
	})

	require.True(t, ok)
	require.Equal(t, ToolInvocationStartedPayload{
		ID:    "call_1",
		Name:  "plan_tool",
		Input: `{"steps":[]}`,
	}, payload)

	payload, ok = ToolInvocationStartedPayloadFrom(map[string]any{
		"id":   "call_2",
		"name": "plan_tool",
		"plan_state": map[string]any{
			"operation":     "update",
			"changed_count": float64(3),
		},
	})

	require.True(t, ok)
	require.Equal(t, ToolInvocationStartedPayload{
		ID:   "call_2",
		Name: "plan_tool",
		PlanState: &PlanToolState{
			Operation:    PlanToolOperationUpdate,
			ChangedCount: 3,
		},
	}, payload)

	payload, ok = ToolInvocationStartedPayloadFrom(map[string]any{
		"id":   "call_3",
		"name": "process",
		"process_state": map[string]any{
			"operation": "start",
			"command":   "sleep 10",
		},
	})

	require.True(t, ok)
	require.Equal(t, ToolInvocationStartedPayload{
		ID:   "call_3",
		Name: "process",
		ProcessState: &ProcessToolState{
			Operation: ProcessToolOperationStart,
			Command:   "sleep 10",
		},
	}, payload)
}

func TestToolInvocationCompletedPayloadFrom_DecodesStructAndMapPayloads(t *testing.T) {
	payload, ok := ToolInvocationCompletedPayloadFrom(struct {
		ToolCallID string
		Name       string
		Content    string
	}{
		ToolCallID: "call_1",
		Name:       "plan_tool",
		Content:    `{"summary":{"total":3}}`,
	})

	require.True(t, ok)
	require.Equal(t, ToolInvocationCompletedPayload{
		ToolCallID: "call_1",
		Name:       "plan_tool",
		Content:    `{"summary":{"total":3}}`,
	}, payload)

	payload, ok = ToolInvocationCompletedPayloadFrom(map[string]any{
		"tool_call_id":               "call_2",
		"name":                       "plan_tool",
		"semantic_projection_status": "skipped",
		"semantic_content_bytes":     float64(0),
		"plan_state": map[string]any{
			"total_count":     float64(3),
			"completed_count": float64(1),
			"changes": []any{
				map[string]any{
					"index":  float64(2),
					"id":     "step-2",
					"action": "completed",
					"fields": []any{"status"},
				},
			},
		},
	})

	require.True(t, ok)
	require.Equal(t, ToolInvocationCompletedPayload{
		ToolCallID:               "call_2",
		Name:                     "plan_tool",
		SemanticProjectionStatus: "skipped",
		PlanState: &PlanToolState{
			TotalCount:     3,
			CompletedCount: 1,
			Changes: []PlanToolChange{
				{Index: 2, ID: "step-2", Action: "completed", Fields: []string{"status"}},
			},
		},
	}, payload)

	payload, ok = ToolInvocationCompletedPayloadFrom(map[string]any{
		"tool_call_id": "call_3",
		"name":         "process",
		"process_state": map[string]any{
			"process_id":   "proc_1",
			"status":       "exited",
			"exit_code":    float64(0),
			"stdout_bytes": float64(12),
		},
	})

	exitCode := 0
	require.True(t, ok)
	require.Equal(t, ToolInvocationCompletedPayload{
		ToolCallID: "call_3",
		Name:       "process",
		ProcessState: &ProcessToolState{
			ProcessID:   "proc_1",
			Status:      "exited",
			ExitCode:    &exitCode,
			StdoutBytes: 12,
		},
	}, payload)
}

func TestToolInvocationFailed_DetectsStructuredToolErrors(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
		failed  bool
	}{
		{name: "structured error", content: `{"name":"browser","error":{"code":"permission_denied","message":"approval denied"}}`, failed: true},
		{name: "string error", content: `{"name":"browser","error":"failed"}`, failed: true},
		{name: "empty error", content: `{"name":"browser","error":""}`},
		{name: "null error", content: `{"name":"browser","error":null}`},
		{name: "successful output", content: `{"name":"browser","output":"done"}`},
		{name: "invalid content", content: `not json`},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.failed, ToolInvocationFailed(test.content))
		})
	}
}

func TestToolInvocationCompletedPayloadFrom_DerivesFailureFromContent(t *testing.T) {
	payload, ok := ToolInvocationCompletedPayloadFrom(ToolInvocationCompletedPayload{
		ToolCallID: "call_1",
		Name:       "browser",
		Content:    `{"error":{"code":"permission_denied","message":"approval denied"}}`,
	})

	require.True(t, ok)
	require.True(t, payload.Failed)
}

func TestToolInvocationCompletedPayloadFrom_ReportsMessageSemanticProjection(t *testing.T) {
	payload, ok := ToolInvocationCompletedPayloadFrom(morphmsg.Message{
		ToolCallID:      "call_1",
		Name:            "read_file",
		Content:         `{"output":"full result"}`,
		SemanticContent: "projected result",
	})

	require.True(t, ok)
	require.Equal(t, "projected", payload.SemanticProjectionStatus)
	require.Equal(t, len("projected result"), payload.SemanticContentBytes)

	payload, ok = ToolInvocationCompletedPayloadFrom(morphmsg.Message{ToolCallID: "call_2", Name: "time"})
	require.True(t, ok)
	require.Equal(t, "skipped", payload.SemanticProjectionStatus)
	require.Zero(t, payload.SemanticContentBytes)
}

func TestPlanToolOutputState_DecodesSummaryAndChanges(t *testing.T) {
	state := PlanToolOutputState(`{"summary":{"total":3,"completed":1},"changes":[{"index":2,"id":"step-2","action":"completed","fields":["status"]}]}`)

	require.Equal(t, &PlanToolState{
		TotalCount:     3,
		CompletedCount: 1,
		Changes: []PlanToolChange{
			{Index: 2, ID: "step-2", Action: "completed", Fields: []string{"status"}},
		},
	}, state)
}

func TestPlanToolOutputState_DecodesToolMessageEnvelope(t *testing.T) {
	state := PlanToolOutputState(`{
		"name": "plan_tool",
		"output": "{\"summary\":{\"total\":3,\"completed\":1},\"changes\":[{\"index\":2,\"id\":\"step-2\",\"action\":\"completed\",\"fields\":[\"status\"]}]}"
	}`)

	require.Equal(t, &PlanToolState{
		TotalCount:     3,
		CompletedCount: 1,
		Changes: []PlanToolChange{
			{Index: 2, ID: "step-2", Action: "completed", Fields: []string{"status"}},
		},
	}, state)
}

func TestProcessToolInputState_DecodesActions(t *testing.T) {
	require.Equal(t, &ProcessToolState{
		Operation: ProcessToolOperationStart,
		Command:   "sleep 10",
	}, ProcessToolInputState(`{"action":"start","command":"sleep","args":["10"]}`))

	require.Equal(t, &ProcessToolState{
		Operation: ProcessToolOperationRead,
		ProcessID: "proc_1",
	}, ProcessToolInputState(`{"action":"read","process_id":"proc_1"}`))

	require.Equal(t, &ProcessToolState{
		Operation: ProcessToolOperationList,
	}, ProcessToolInputState(`{"action":"list"}`))
}

func TestProcessToolOutputState_DecodesProcessAndOutput(t *testing.T) {
	exitCode := 0

	require.Equal(t, &ProcessToolState{
		ProcessID:   "proc_1",
		Command:     "printf hello",
		Status:      "exited",
		ExitCode:    &exitCode,
		StdoutBytes: 5,
	}, ProcessToolOutputState(`{"process":{"id":"proc_1","command":"printf","args":["hello"],"status":"exited","exit_code":0,"stdout_bytes":5}}`))

	require.Equal(t, &ProcessToolState{
		Operation:   ProcessToolOperationRead,
		ProcessID:   "proc_1",
		Status:      "running",
		StdoutBytes: 12,
		StderrBytes: 3,
	}, ProcessToolOutputState(`{"process":{"id":"proc_1","status":"running"},"output":{"stdout_bytes":12,"stderr_bytes":3}}`))

	require.Equal(t, &ProcessToolState{
		Operation: ProcessToolOperationList,
		Count:     2,
	}, ProcessToolOutputState(`{"processes":[{"id":"proc_1"},{"id":"proc_2"}]}`))

	require.Equal(t, &ProcessToolState{
		Operation: ProcessToolOperationList,
		Count:     1,
	}, ProcessToolOutputState(`{"name":"process","output":"{\"processes\":[{\"id\":\"proc_1\"}]}"}`))

	require.Equal(t, &ProcessToolState{
		Status:    "failed",
		ErrorCode: "process_start_failed",
		Error:     "address already in use",
	}, ProcessToolOutputState(`{"name":"process","error":{"code":"process_start_failed","message":"address already in use"}}`))
}
