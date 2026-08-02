package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/environment"
	"github.com/xymorphic/morph/internal/mocks"
	models "github.com/xymorphic/morph/internal/model"
	morphtools "github.com/xymorphic/morph/internal/tools"
	"github.com/xymorphic/morph/internal/trace"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	agenttool "github.com/xymorphic/morph/pkg/agent/tool"
)

func TestToolRegistry_ResolveUsesEnvironmentPolicyAndConvertsDefinitions(t *testing.T) {
	stub := &mocks.ToolRegistryStub{
		Definitions: morphtools.Definitions{{
			Name:         "memory_extract",
			Description:  "Extract memory",
			InputSchema:  map[string]any{"type": "object"},
			ParallelSafe: true,
			Groups:       []string{"core"},
			Requires:     morphtools.Capabilities{Memory: true},
			Platforms:    []string{"darwin"},
		}},
	}
	env := &mocks.EnvironmentStub{
		ToolRegistry: stub,
		Policy: morphtools.Policy{
			GroupNames:   []string{"core"},
			Capabilities: morphtools.Capabilities{Memory: true},
			Platform:     "darwin",
		},
	}
	registry := NewToolRegistry(env, nil)

	definitions, err := registry.Resolve(agenttool.Policy{})
	require.NoError(t, err)
	require.Equal(t, []agenttool.Definition{{
		Name:         "memory_extract",
		Description:  "Extract memory",
		InputSchema:  map[string]any{"type": "object"},
		ParallelSafe: true,
		Groups:       []string{"core"},
		Requires:     agenttool.Capabilities{Memory: true},
		Platforms:    []string{"darwin"},
	}}, definitions)
	require.Equal(t, env.Policy, stub.LastToolPolicy)
}

func TestToolRegistry_InvokeDelegatesToHostInvoker(t *testing.T) {
	env := &mocks.EnvironmentStub{}
	var capturedEnv any
	var capturedCall models.ToolCall
	registry := NewToolRegistry(
		env,
		func(_ context.Context, runtimeEnv environment.Environment, toolCall models.ToolCall) morphmsg.Message {
			capturedEnv = runtimeEnv
			capturedCall = toolCall
			return morphmsg.Message{
				Role:       morphmsg.RoleTool,
				Name:       toolCall.Name,
				ToolCallID: toolCall.ID,
				Content:    `{"ok":true}`,
			}
		},
	)

	message := registry.Invoke(context.Background(), agenttool.Call{ID: "call-1", Name: "time", Input: "{}"})

	require.Same(t, env, capturedEnv)
	require.Equal(t, models.ToolCall{ID: "call-1", Name: "time", Input: "{}"}, capturedCall)
	require.Equal(t, morphmsg.RoleTool, message.Role)
	require.Equal(t, "time", message.Name)
	require.Equal(t, "call-1", message.ToolCallID)
	require.Equal(t, `{"ok":true}`, message.Content)
}

func TestToolRegistry_NilAndListGroupPaths(t *testing.T) {
	require.Nil(t, (*ToolRegistry)(nil).ListGroups())
	definitions, err := (*ToolRegistry)(nil).Resolve(agenttool.Policy{})
	require.NoError(t, err)
	require.Nil(t, definitions)
	message := (*ToolRegistry)(nil).Invoke(context.Background(), agenttool.Call{ID: "call", Name: "time"})
	require.JSONEq(t, `{"error":"tool invocation is required"}`, message.Content)
	require.Equal(t, agenttool.Policy{}, ToolPolicyFromEnvironment(nil))

	stub := &mocks.ToolRegistryStub{
		Groups: []morphtools.Group{{Name: "core", Tools: []string{"time"}, Includes: []string{"read"}}},
	}
	registry := NewToolRegistry(&mocks.EnvironmentStub{ToolRegistry: stub}, nil)
	require.Equal(t, []agenttool.Group{{Name: "core", Tools: []string{"time"}, Includes: []string{"read"}}}, registry.ListGroups())
	require.Nil(t, NewToolRegistry(&mocks.EnvironmentStub{ToolRegistry: &mocks.ToolRegistryStub{}}, nil).ListGroups())

	rootErr := errors.New("resolve failed")
	definitions, err = NewToolRegistry(
		&mocks.EnvironmentStub{
			ToolRegistry: &mocks.ToolRegistryStub{ResolveErr: rootErr},
		},
		nil,
	).Resolve(agenttool.Policy{Platform: "linux"})
	require.ErrorIs(t, err, rootErr)
	require.Nil(t, definitions)
	require.Nil(t, agentDefinitionsFromToolsDefinitions(nil))
}
func TestTurn_ExecuteToolCallsParallel_AppendsResultsInModelOrder(t *testing.T) {
	completed := make(chan string, 2)
	secondCompleted := make(chan struct{})
	turn := &Turn{
		invokeToolFn: func(_ context.Context, toolCall models.ToolCall) morphmsg.Message {
			switch toolCall.ID {
			case "call-1":
				select {
				case <-secondCompleted:
				case <-time.After(250 * time.Millisecond):
				}
			case "call-2":
				completed <- toolCall.ID
				close(secondCompleted)

				return toolExecutionTestMessage(toolCall, `{"ok":true}`)
			}
			completed <- toolCall.ID

			return toolExecutionTestMessage(toolCall, `{"ok":true}`)
		},
	}

	messages, err := turn.executeToolCalls(
		context.Background(),
		trace.NoopSession(),
		[]models.ToolCall{
			{ID: "call-1", Name: "time", Input: "{}"},
			{ID: "call-2", Name: "time", Input: "{}"},
		},
		[]models.ToolDefinition{{Name: "time", ParallelSafe: true}},
	)

	require.NoError(t, err)
	require.Equal(t, "call-2", <-completed)
	require.Equal(t, "call-1", <-completed)
	require.Equal(t, []string{"call-1", "call-2"}, toolExecutionTestMessageIDs(messages))
}

func TestAssistantToolCallMessageFromResponse_PreservesMultipleToolCalls(t *testing.T) {
	message, err := assistantToolCallMessageFromResponse(&models.Response{
		OutputText: "checking both",
		ToolCalls: []models.ToolCall{
			{ID: "call-1", Name: "time", Input: "{}"},
			{ID: "call-2", Name: "web_search", Input: `{"query":"morph"}`},
		},
	})

	require.NoError(t, err)
	require.Equal(t, morphmsg.RoleAssistant, message.Role)
	require.Equal(t, "checking both", message.Content)
	require.Equal(t, []morphmsg.ToolCall{
		{ID: "call-1", Name: "time", Input: "{}"},
		{ID: "call-2", Name: "web_search", Input: `{"query":"morph"}`},
	}, message.ToolCalls)
}

func TestTurn_ExecuteToolCallsParallel_PreservesToolErrorPayloads(t *testing.T) {
	turn := &Turn{
		invokeToolFn: func(_ context.Context, toolCall models.ToolCall) morphmsg.Message {
			if toolCall.ID == "call-2" {
				return toolExecutionTestMessage(toolCall, `{"error":"blocked"}`)
			}

			return toolExecutionTestMessage(toolCall, `{"ok":true}`)
		},
	}

	messages, err := turn.executeToolCalls(
		context.Background(),
		trace.NoopSession(),
		[]models.ToolCall{
			{ID: "call-1", Name: "time", Input: "{}"},
			{ID: "call-2", Name: "time", Input: "{}"},
		},
		[]models.ToolDefinition{{Name: "time", ParallelSafe: true}},
	)

	require.NoError(t, err)
	require.Equal(t, []string{"call-1", "call-2"}, toolExecutionTestMessageIDs(messages))
	require.Equal(t, `{"error":"blocked"}`, messages[1].Content)
}

func TestTurn_ExecuteToolCall_RecordsFailedOutcome(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	turn := &Turn{
		invokeToolFn: func(_ context.Context, toolCall models.ToolCall) morphmsg.Message {
			return toolExecutionTestMessage(toolCall, `{"error":"blocked"}`)
		},
	}

	_, err := turn.executeToolCall(
		context.Background(),
		traceSession,
		models.ToolCall{ID: "call-1", Name: "time", Input: "{}"},
	)

	require.NoError(t, err)
	require.Len(t, traceSession.Events, 2)
	payload, ok := traceSession.Events[1].Payload.(trace.ToolInvocationCompletedPayload)
	require.True(t, ok)
	require.True(t, payload.Failed)
	require.Equal(t, "skipped", payload.SemanticProjectionStatus)
}

func TestTurn_ExecuteToolCall_RecordsSemanticProjectionMetadata(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	turn := &Turn{
		invokeToolFn: func(_ context.Context, toolCall models.ToolCall) morphmsg.Message {
			message := toolExecutionTestMessage(toolCall, `{"output":"full result"}`)
			message.SemanticContent = "semantic result"
			return message
		},
	}

	_, err := turn.executeToolCall(
		context.Background(),
		traceSession,
		models.ToolCall{ID: "call-1", Name: "read_file", Input: `{}`},
	)

	require.NoError(t, err)
	payload, ok := traceSession.Events[1].Payload.(trace.ToolInvocationCompletedPayload)
	require.True(t, ok)
	require.Equal(t, "projected", payload.SemanticProjectionStatus)
	require.Equal(t, len("semantic result"), payload.SemanticContentBytes)
}

func TestTurn_ExecuteToolCalls_RunsAdjacentSafeGroupsInParallel(t *testing.T) {
	completed := make(chan string, 4)
	fourthStarted := make(chan struct{})
	turn := &Turn{
		invokeToolFn: func(_ context.Context, toolCall models.ToolCall) morphmsg.Message {
			switch toolCall.ID {
			case "call-3":
				select {
				case <-fourthStarted:
				case <-time.After(250 * time.Millisecond):
				}
			case "call-4":
				close(fourthStarted)
				completed <- toolCall.ID

				return toolExecutionTestMessage(toolCall, `{"ok":true}`)
			}
			completed <- toolCall.ID

			return toolExecutionTestMessage(toolCall, `{"ok":true}`)
		},
	}

	messages, err := turn.executeToolCalls(
		context.Background(),
		trace.NoopSession(),
		[]models.ToolCall{
			{ID: "call-1", Name: "web_search"},
			{ID: "call-2", Name: "memory_add"},
			{ID: "call-3", Name: "web_extract"},
			{ID: "call-4", Name: "time"},
		},
		[]models.ToolDefinition{
			{Name: "web_search", ParallelSafe: true},
			{Name: "web_extract", ParallelSafe: true},
			{Name: "time", ParallelSafe: true},
			{Name: "memory_add"},
		},
	)

	require.NoError(t, err)
	require.Equal(t, "call-1", <-completed)
	require.Equal(t, "call-2", <-completed)
	require.Equal(t, "call-4", <-completed)
	require.Equal(t, "call-3", <-completed)
	require.Equal(t, []string{"call-1", "call-2", "call-3", "call-4"}, toolExecutionTestMessageIDs(messages))
}

func TestTurn_ExecuteToolCallsParallel_CancelsSiblingsOnFatalError(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	var once sync.Once
	turn := &Turn{
		invokeToolFn: func(ctx context.Context, toolCall models.ToolCall) morphmsg.Message {
			if toolCall.ID == "call-1" {
				once.Do(func() { close(started) })
				<-ctx.Done()
				close(cancelled)
				return toolExecutionTestMessage(toolCall, `{"cancelled":true}`)
			}

			<-started
			return morphmsg.Message{
				Role:    morphmsg.RoleTool,
				Name:    toolCall.Name,
				Content: `{"invalid":true}`,
			}
		},
	}

	_, err := turn.executeToolCalls(
		context.Background(),
		trace.NoopSession(),
		[]models.ToolCall{
			{ID: "call-1", Name: "time", Input: "{}"},
			{ID: "call-2", Name: "time", Input: "{}"},
		},
		[]models.ToolDefinition{{Name: "time", ParallelSafe: true}},
	)

	require.EqualError(t, err, "tool call id is required")
	require.Eventually(t, func() bool {
		select {
		case <-cancelled:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
}

func TestTurn_ExecuteToolCalls_UsesSequentialPathForUnsafeTools(t *testing.T) {
	var calls []string
	turn := &Turn{
		invokeToolFn: func(_ context.Context, toolCall models.ToolCall) morphmsg.Message {
			calls = append(calls, toolCall.ID)

			return toolExecutionTestMessage(toolCall, `{"ok":true}`)
		},
	}

	messages, err := turn.executeToolCalls(
		context.Background(),
		trace.NoopSession(),
		[]models.ToolCall{
			{ID: "call-1", Name: "memory_add", Input: "{}"},
			{ID: "call-2", Name: "memory_add", Input: "{}"},
		},
		[]models.ToolDefinition{{Name: "memory_add"}},
	)

	require.NoError(t, err)
	require.Equal(t, []string{"call-1", "call-2"}, calls)
	require.Equal(t, []string{"call-1", "call-2"}, toolExecutionTestMessageIDs(messages))
}

func TestTurn_ExecuteToolCalls_ReturnsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	turn := &Turn{
		invokeToolFn: func(_ context.Context, toolCall models.ToolCall) morphmsg.Message {
			return toolExecutionTestMessage(toolCall, `{"ok":true}`)
		},
	}

	_, err := turn.executeToolCalls(
		ctx,
		trace.NoopSession(),
		[]models.ToolCall{{ID: "call-1", Name: "time", Input: "{}"}},
		[]models.ToolDefinition{{Name: "time", ParallelSafe: true}},
	)

	require.ErrorIs(t, err, context.Canceled)
}

func TestTurn_InvokeToolLegacyHookAndRuntimeFallbacks(t *testing.T) {
	toolCall := models.ToolCall{ID: "call-1", Name: "time", Input: "{}"}
	message := (*Turn)(nil).invokeTool(context.Background(), toolCall)
	require.JSONEq(t, `{"error":"tool invocation is required"}`, message.Content)

	turn := &Turn{
		invokeToolFn: func(_ context.Context, call models.ToolCall) morphmsg.Message {
			return toolExecutionTestMessage(call, `{"direct":true}`)
		},
	}

	message = turn.invokeTool(context.Background(), toolCall)
	require.Equal(t, `{"direct":true}`, message.Content)

	env := &mocks.EnvironmentStub{}
	turn = &Turn{
		env: env,
		invokeToolFn: func(_ context.Context, runtime environment.Environment, call models.ToolCall) morphmsg.Message {
			require.Same(t, env, runtime)
			return toolExecutionTestMessage(call, `{"reflect":true}`)
		},
	}
	message = turn.invokeTool(context.Background(), toolCall)
	require.Equal(t, `{"reflect":true}`, message.Content)

	turn = &Turn{invokeToolFn: "not a function"}
	message = turn.invokeTool(context.Background(), toolCall)
	require.JSONEq(t, `{"error":"tool invocation is required"}`, message.Content)

	turn = &Turn{invokeToolFn: func(context.Context, environment.Environment, models.ToolCall) string { return "bad" }}
	message = turn.invokeTool(context.Background(), toolCall)
	require.JSONEq(t, `{"error":"tool invocation is required"}`, message.Content)

	registry := &mocks.ToolRegistryStub{Result: morphtools.Result{Output: `{"ok":true}`}}
	turn = &Turn{env: &mocks.EnvironmentStub{ToolRegistry: registry}}
	message = turn.invokeTool(context.Background(), toolCall)
	require.Equal(t, map[string]any{
		"name":   "time",
		"output": `{"ok":true}`,
	}, toolExecutionTestContent(t, message))

	registry = &mocks.ToolRegistryStub{Err: errors.New("runtime failed")}
	turn = &Turn{env: &mocks.EnvironmentStub{ToolRegistry: registry}}
	message = turn.invokeTool(context.Background(), toolCall)
	require.Equal(t, map[string]any{
		"name":  "time",
		"error": "runtime failed",
	}, toolExecutionTestContent(t, message))

	registry = &mocks.ToolRegistryStub{Result: morphtools.Result{Error: morphtools.Error{Code: "tool_error", Message: "failed"}.String()}}
	turn = &Turn{env: &mocks.EnvironmentStub{ToolRegistry: registry}}
	message = turn.invokeTool(context.Background(), toolCall)
	require.Equal(t, map[string]any{
		"name": "time",
		"error": map[string]any{
			"code":    "tool_error",
			"message": "failed",
		},
	}, toolExecutionTestContent(t, message))

	turn = &Turn{}
	message = turn.invokeToolWithLegacyRuntime(context.Background(), nil, toolCall)
	require.Equal(t, map[string]any{
		"name":  "time",
		"error": "tool registry is required",
	}, toolExecutionTestContent(t, message))
}
