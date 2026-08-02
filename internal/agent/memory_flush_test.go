package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/agent/context/compaction"
	"github.com/xymorphic/morph/internal/agent/context/summary"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/mocks"
	models "github.com/xymorphic/morph/internal/model"
	storage "github.com/xymorphic/morph/internal/state/core"
	statemanager "github.com/xymorphic/morph/internal/state/manager"
	"github.com/xymorphic/morph/internal/tools"
	"github.com/xymorphic/morph/internal/trace"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	agenttool "github.com/xymorphic/morph/pkg/agent/tool"
)

func TestTurn_AvailableMemoryFlushToolDefinitionsExcludesMemoryExtract(t *testing.T) {
	turn := NewTurnWithSessionStore(
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		&memoryFlushToolRegistryStub{definitions: []agenttool.Definition{
			{Name: "memory_extract"},
			{Name: "memory_add"},
			{Name: "memory_update"},
			{Name: "memory_delete"},
			{Name: "time"},
		}},
		agenttool.Policy{},
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	definitions, err := turn.availableMemoryFlushToolDefinitions()
	require.NoError(t, err)

	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		names = append(names, definition.Name)
	}
	require.Equal(t, []string{"memory_add", "memory_update", "memory_delete"}, names)
}

func TestGetMemoryFlushToolError_ReturnsNilForSuccessfulToolPayload(t *testing.T) {
	err := getMemoryFlushToolError(morphmsg.Message{
		Role:    morphmsg.RoleTool,
		Name:    "memory_add",
		Content: `{"name":"memory_add","output":"ok"}`,
	})

	require.NoError(t, err)
}

func TestGetMemoryFlushToolError_ReturnsStructuredToolError(t *testing.T) {
	err := getMemoryFlushToolError(morphmsg.Message{
		Role:    morphmsg.RoleTool,
		Name:    "memory_extract",
		Content: `{"name":"memory_extract","error":{"code":"tool_error","message":"context deadline exceeded"}}`,
	})

	require.EqualError(t, err, "memory flush tool memory_extract failed: context deadline exceeded")
}

func TestGetMemoryFlushToolError_ReturnsEncodedToolError(t *testing.T) {
	err := getMemoryFlushToolError(morphmsg.Message{
		Role:    morphmsg.RoleTool,
		Name:    "memory_extract",
		Content: `{"name":"memory_extract","error":"{\"code\":\"tool_error\",\"message\":\"context deadline exceeded\"}"}`,
	})

	require.EqualError(t, err, "memory flush tool memory_extract failed: context deadline exceeded")
}

func TestMemoryFlushHelpersRecordAndInvoke(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	recordMemoryFlushSkipped(traceSession, "compression", " no_tools ")
	recordMemoryFlushCompleted(traceSession, "compression", "default", "bounded", 2)
	recordMemoryFlushFailure(traceSession, "compression", context.DeadlineExceeded)
	recordMemoryFlushFailure(traceSession, "compression", nil)
	require.Equal(t, []string{
		trace.EvtMemoryFlushSkipped,
		trace.EvtMemoryFlushCompleted,
		trace.EvtMemoryFlushTimeout,
	}, []string{traceSession.Events[0].Type, traceSession.Events[1].Type, traceSession.Events[2].Type})

	require.Equal(t, "message", getToolErrorText(map[string]any{"message": "message"}))
	require.Equal(t, "code", getToolErrorText(map[string]any{"code": "code"}))
	require.Equal(t, "plain", getToolErrorStringText(" plain "))
	require.Empty(t, getToolErrorStringText(" "))
	require.EqualError(t, getMemoryFlushToolError(morphmsg.Message{
		Role:    morphmsg.RoleTool,
		Content: `{"error":{"code":"tool_error"}}`,
	}), "memory flush tool unknown failed: tool_error")
	require.NoError(t, getMemoryFlushToolError(morphmsg.Message{Role: morphmsg.RoleAssistant, Content: "{}"}))
	require.NoError(t, getMemoryFlushToolError(morphmsg.Message{Role: morphmsg.RoleTool, Content: "not-json"}))
	require.EqualError(t, getMemoryFlushToolError(morphmsg.Message{
		Role:    morphmsg.RoleTool,
		Content: `{"error":{}}`,
	}), "memory flush tool unknown failed: tool failed")
	require.Empty(t, getToolErrorText(42))

	toolCall := models.ToolCall{ID: "call", Name: "memory_add", Input: "{}"}
	message := (*Turn)(nil).invokeFlushTool(context.Background(), toolCall)
	require.Equal(t, "call", message.ToolCallID)
	require.JSONEq(t, `{"name":"memory_add","error":"tool registry is required"}`, message.Content)
	message = (&Turn{}).invokeFlushTool(context.Background(), toolCall)
	require.JSONEq(t, `{"name":"memory_add","error":"tool registry is required"}`, message.Content)
}

func TestTurn_FlushMemoryBeforeContextLossCompletesNoOpAndToolCall(t *testing.T) {
	cfg := &config.Config{Models: config.ModelsConfig{Summary: config.SummaryModelConfig{Name: "summary", API: models.APIOpenAIResponses}}}
	cfg.Memory.Flush.MaxCalls = 2
	client := &mocks.ModelClientStub{Responses: []*models.Response{{OutputText: "nothing"}}}
	turn := &Turn{
		cfg:           cfg,
		summaryClient: client,
		summary:       &summary.State{},
		toolRegistry: &memoryFlushToolRegistryStub{definitions: []agenttool.Definition{
			{Name: "memory_add"},
		}},
	}
	traceSession := &mocks.TraceSessionStub{}

	require.NoError(t, turn.flushMemoryBeforeContextLoss(context.Background(), "compression", traceSession))
	require.Equal(t, trace.EvtMemoryFlushCompleted, traceSession.Events[len(traceSession.Events)-1].Type)

	client = &mocks.ModelClientStub{Responses: []*models.Response{{
		RequiresToolCalls: true,
		ToolCalls:         []models.ToolCall{{ID: "call", Name: "memory_add", Input: "{}"}},
	}}}
	turn.summaryClient = client
	turn.invokeToolFn = func(_ context.Context, toolCall models.ToolCall) morphmsg.Message {
		return morphmsg.Message{Role: morphmsg.RoleTool, Name: toolCall.Name, ToolCallID: toolCall.ID, Content: `{"output":"ok"}`}
	}
	traceSession.Events = nil

	require.NoError(t, turn.flushMemoryBeforeContextLoss(context.Background(), "compression", traceSession))
	require.Equal(t, trace.EvtMemoryFlushCompleted, traceSession.Events[len(traceSession.Events)-1].Type)
}

func TestAgent_ShouldFlushMemoryBeforeContextLossPrerequisites(t *testing.T) {
	cfg := &config.Config{}
	core := &Agent{}
	require.False(t, core.shouldFlushMemoryBeforeContextLoss())

	core = &Agent{
		cfg:         cfg,
		initialized: true,
		stateMgr:    nil,
		env:         &mocks.EnvironmentStub{},
		modelClient: &mocks.ModelClientStub{},
	}
	require.False(t, core.shouldFlushMemoryBeforeContextLoss())

	core.stateMgr = &statemanager.Manager{}
	require.True(t, core.shouldFlushMemoryBeforeContextLoss())

	disabled := false
	cfg.Memory.Flush.Enabled = &disabled
	require.False(t, core.shouldFlushMemoryBeforeContextLoss())
}

func TestTurn_ShouldFlushMemoryBeforeCompactionPrerequisites(t *testing.T) {
	cfg := &config.Config{Models: config.ModelsConfig{Main: config.MainModelConfig{ContextLength: 10}}}
	cfg.Compaction.TriggerPercent = 0.1
	turn := &Turn{
		cfg:          cfg,
		modelClient:  &mocks.ModelClientStub{},
		toolRegistry: &memoryFlushToolRegistryStub{definitions: []agenttool.Definition{{Name: "memory_add"}}},
	}

	require.True(t, turn.shouldFlushMemoryBeforeCompaction(models.Request{
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "this is enough text to trigger"}},
	}))
	require.False(t, (*Turn)(nil).shouldFlushMemoryBeforeCompaction(models.Request{}))
	require.False(t, (&Turn{cfg: cfg}).shouldFlushMemoryBeforeCompaction(models.Request{}))

	disabled := false
	cfg.Compaction.Enabled = &disabled
	require.False(t, turn.shouldFlushMemoryBeforeCompaction(models.Request{}))
}

func TestTurn_ShouldFlushMemoryBeforeCompactionUsesAnchoredUsage(t *testing.T) {
	cfg := &config.Config{Models: config.ModelsConfig{Main: config.MainModelConfig{ContextLength: 1000}}}
	cfg.Compaction.TriggerPercent = 0.5
	turn := &Turn{
		cfg:              cfg,
		modelClient:      &mocks.ModelClientStub{},
		toolRegistry:     &memoryFlushToolRegistryStub{definitions: []agenttool.Definition{{Name: "memory_add"}}},
		compactionAnchor: compaction.Anchor{PromptTokens: 20, MessageCount: 1},
	}
	request := models.Request{
		Instructions: "this deliberately inflates the full request estimate past the trigger " + strings.Repeat("x", 3000),
		Messages: []morphmsg.Message{
			{Role: morphmsg.RoleUser, Content: "measured"},
			{Role: morphmsg.RoleTool, Content: "small tail"},
		},
	}

	require.Greater(t, compaction.EstimateRequestRough(request), 500)
	require.False(t, turn.shouldFlushMemoryBeforeCompaction(request))
}

func TestTurn_MaybeFlushMemoryBeforeCompactionRecordsFailure(t *testing.T) {
	cfg := &config.Config{Models: config.ModelsConfig{Main: config.MainModelConfig{ContextLength: 10}}}
	cfg.Compaction.TriggerPercent = 0.1
	cfg.Memory.Flush.MaxCalls = 1
	turn := &Turn{
		cfg:          cfg,
		modelClient:  &mocks.ModelClientStub{Err: context.Canceled},
		toolRegistry: &memoryFlushToolRegistryStub{definitions: []agenttool.Definition{{Name: "memory_add"}}},
	}
	traceSession := &mocks.TraceSessionStub{}

	turn.maybeFlushMemoryBeforeCompaction(context.Background(), models.Request{
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "this is enough text to trigger"}},
	}, traceSession)

	require.Equal(t, trace.EvtMemoryFlushFailed, traceSession.Events[len(traceSession.Events)-1].Type)
}

func TestTurn_FlushMemoryBeforeContextLossHandlesNoToolsUnsupportedAndBounds(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	turn := &Turn{toolRegistry: &memoryFlushToolRegistryStub{}}

	require.NoError(t, turn.flushMemoryBeforeContextLoss(context.Background(), "compression", traceSession))
	require.Equal(t, trace.EvtMemoryFlushSkipped, traceSession.Events[len(traceSession.Events)-1].Type)

	cfg := &config.Config{Models: config.ModelsConfig{Summary: config.SummaryModelConfig{Name: "summary", API: models.APIOpenAIResponses}}}
	cfg.Memory.Flush.MaxCalls = 1
	turn = &Turn{
		cfg:           cfg,
		summaryClient: &mocks.ModelClientStub{Responses: []*models.Response{{RequiresToolCalls: true, ToolCalls: []models.ToolCall{{ID: "call", Name: "time"}}}}},
		toolRegistry:  &memoryFlushToolRegistryStub{definitions: []agenttool.Definition{{Name: "memory_add"}}},
	}
	traceSession.Events = nil
	require.NoError(t, turn.flushMemoryBeforeContextLoss(context.Background(), "compression", traceSession))
	require.Equal(t, trace.EvtMemoryFlushCompleted, traceSession.Events[len(traceSession.Events)-1].Type)

	turn.summaryClient = &mocks.ModelClientStub{Responses: []*models.Response{{
		RequiresToolCalls: true,
		ToolCalls: []models.ToolCall{
			{ID: "call-1", Name: "time"},
			{ID: "call-2", Name: "memory_add"},
		},
	}}}
	traceSession.Events = nil
	require.NoError(t, turn.flushMemoryBeforeContextLoss(context.Background(), "compression", traceSession))
	require.Equal(t, trace.EvtMemoryFlushCompleted, traceSession.Events[len(traceSession.Events)-1].Type)
}

func TestTurn_FlushMemoryBeforeContextLossReturnsValidationErrors(t *testing.T) {
	require.EqualError(t, (*Turn)(nil).
		flushMemoryBeforeContextLoss(context.Background(), "compression", trace.NoopSession()), "turn is required")

	turn := &Turn{toolRegistry: &memoryFlushToolRegistryStub{definitions: []agenttool.Definition{{Name: "memory_add"}}}}
	require.EqualError(t, turn.flushMemoryBeforeContextLoss(context.Background(), "compression", trace.NoopSession()), "memory flush model client is required")

	cfg := &config.Config{Models: config.ModelsConfig{Summary: config.SummaryModelConfig{Name: "summary", API: models.APIOpenAIResponses}}}
	cfg.Memory.Flush.MaxCalls = 1
	turn = &Turn{
		cfg:           cfg,
		summaryClient: &mocks.ModelClientStub{Responses: []*models.Response{nil}},
		toolRegistry:  &memoryFlushToolRegistryStub{definitions: []agenttool.Definition{{Name: "memory_add"}}},
	}
	require.EqualError(t, turn.flushMemoryBeforeContextLoss(context.Background(), "compression", trace.NoopSession()), "model response is required")

	turn.summaryClient = &mocks.ModelClientStub{Responses: []*models.Response{{RequiresToolCalls: true}}}
	require.EqualError(t, turn.flushMemoryBeforeContextLoss(context.Background(), "compression", trace.NoopSession()), "memory flush requested tool execution without tool calls")

	turn.summaryClient = &mocks.ModelClientStub{Responses: []*models.Response{{
		RequiresToolCalls: true,
		ToolCalls:         []models.ToolCall{{ID: "call", Name: "memory_add"}},
	}}}
	turn.invokeToolFn = func(_ context.Context, toolCall models.ToolCall) morphmsg.Message {
		return morphmsg.Message{Role: morphmsg.RoleTool, Name: toolCall.Name, ToolCallID: toolCall.ID, Content: `{"error":"failed"}`}
	}
	require.EqualError(t, turn.flushMemoryBeforeContextLoss(context.Background(), "compression", trace.NoopSession()), "memory flush tool memory_add failed: failed")

	turn.summaryClient = &mocks.ModelClientStub{Responses: []*models.Response{{
		RequiresToolCalls: true,
		ToolCalls:         []models.ToolCall{{ID: "call", Name: "memory_add"}},
	}}}
	turn.invokeToolFn = func(_ context.Context, toolCall models.ToolCall) morphmsg.Message {
		return morphmsg.Message{Role: morphmsg.RoleTool, Name: toolCall.Name, Content: "{}"}
	}
	require.EqualError(t, turn.flushMemoryBeforeContextLoss(context.Background(), "compression", trace.NoopSession()), "tool call id is required")

	expected := errors.New("resolve failed")
	turn = &Turn{toolRegistry: &memoryFlushToolRegistryStub{resolveErr: expected}}
	require.ErrorIs(t, turn.flushMemoryBeforeContextLoss(context.Background(), "compression", trace.NoopSession()), expected)

	turn = &Turn{
		cfg:           cfg,
		summaryClient: &mocks.ModelClientStub{Responses: []*models.Response{{RequiresToolCalls: true, ToolCalls: []models.ToolCall{{ID: "call"}}}}},
		toolRegistry:  &memoryFlushToolRegistryStub{definitions: []agenttool.Definition{{Name: "memory_add"}}},
	}
	require.EqualError(t, turn.flushMemoryBeforeContextLoss(context.Background(), "compression", trace.NoopSession()), "tool call name is required")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	turn = &Turn{
		cfg:           cfg,
		summaryClient: &mocks.ModelClientStub{Responses: []*models.Response{{OutputText: "unused"}}},
		toolRegistry:  &memoryFlushToolRegistryStub{definitions: []agenttool.Definition{{Name: "memory_add"}}},
	}
	require.ErrorIs(t, turn.flushMemoryBeforeContextLoss(ctx, "compression", trace.NoopSession()), context.Canceled)
}

func TestAgent_MaybeFlushMemoryBeforeContextLossRecordsFlushError(t *testing.T) {
	cfg := &config.Config{}
	cfg.Memory.Flush.MaxCalls = 1
	store := &stateStoreStub{
		session:  storage.Session{ID: storage.DefaultSessionID},
		messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	}
	manager, err := statemanager.NewManager(store, time.Hour, time.Hour)
	require.NoError(t, err)
	core := &Agent{
		cfg:         cfg,
		modelClient: &mocks.ModelClientStub{Err: errors.New("flush failed")},
		initialized: true,
		stateMgr:    manager,
		env: &mocks.EnvironmentStub{ToolRegistry: &mocks.ToolRegistryStub{
			Definitions: []tools.Definition{{Name: "memory_add"}},
		}},
	}
	traceSession := &mocks.TraceSessionStub{}

	core.maybeFlushMemoryBeforeContextLoss(context.Background(), storage.DefaultSessionID, memoryFlushTriggerControlledExit, traceSession)

	require.Equal(t, trace.EvtMemoryFlushFailed, traceSession.Events[len(traceSession.Events)-1].Type)
}
