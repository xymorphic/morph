package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/agent/model"
	"github.com/xymorphic/morph/pkg/agent/prompt"
	"github.com/xymorphic/morph/pkg/agent/session"
	"github.com/xymorphic/morph/pkg/agent/tool"
)

func TestAgent_RespondCompletesWithPublicDependencies(t *testing.T) {
	store := newStubSessionStore()
	client := &stubModelClient{
		complete: func(_ context.Context, request model.Request) (*model.Response, error) {
			require.Len(t, request.Messages, 1)
			require.Equal(t, message.RoleUser, request.Messages[0].Role)

			return &model.Response{OutputText: "hello from public core", PromptTokens: 12}, nil
		},
	}

	core, err := NewAgent(Options{
		Model:        "test-model",
		API:          model.APIOpenAICompletions,
		ModelClient:  client,
		SessionStore: store,
	})
	require.NoError(t, err)

	reply, err := core.Respond(context.Background(), "hello", RespondOptions{})
	require.NoError(t, err)
	require.Equal(t, "hello from public core", reply)
	require.Equal(t, 12, store.lastPromptTokens)
	require.Len(t, store.messages[session.DefaultID], 2)
}

func TestAgent_RespondRunsToolLoopWithPublicDependencies(t *testing.T) {
	store := newStubSessionStore()
	registry := &stubToolRegistry{
		invoke: func(_ context.Context, call tool.Call) message.Message {
			require.Equal(t, "lookup", call.Name)

			return message.Message{
				Role:       message.RoleTool,
				Name:       call.Name,
				ToolCallID: call.ID,
				Content:    `{"ok":true}`,
			}
		},
	}
	client := &stubModelClient{}
	client.complete = func(_ context.Context, request model.Request) (*model.Response, error) {
		if len(request.Messages) == 1 {
			return &model.Response{
				RequiresToolCalls: true,
				ToolCalls: []model.ToolCall{{
					ID:    "call_1",
					Name:  "lookup",
					Input: `{"query":"hello"}`,
				}},
			}, nil
		}

		require.Equal(t, message.RoleTool, request.Messages[len(request.Messages)-1].Role)
		return &model.Response{OutputText: "tool result handled"}, nil
	}

	core, err := New(Options{
		Model:         "test-model",
		API:           model.APIOpenAICompletions,
		ModelClient:   client,
		SessionStore:  store,
		ToolRegistry:  registry,
		MaxIterations: 2,
	})
	require.NoError(t, err)

	reply, err := core.Respond(context.Background(), "hello", RespondOptions{})
	require.NoError(t, err)
	require.Equal(t, "tool result handled", reply)
	require.Len(t, registry.Calls(), 1)
	require.Len(t, store.messages[session.DefaultID], 4)
}

func TestAgent_RespondUsesRequestToolGroups(t *testing.T) {
	registry := &stubToolRegistry{}
	client := &stubModelClient{
		complete: func(context.Context, model.Request) (*model.Response, error) {
			return &model.Response{OutputText: "done"}, nil
		},
	}
	core, err := New(Options{
		ModelClient:   client,
		SessionStore:  newStubSessionStore(),
		ToolRegistry:  registry,
		ToolPolicy:    tool.Policy{GroupNames: []string{"default"}},
		MaxIterations: 1,
	})
	require.NoError(t, err)

	reply, err := core.Respond(context.Background(), "hello", RespondOptions{
		ToolGroups: []string{"memory", "shell"},
	})
	require.NoError(t, err)
	require.Equal(t, "done", reply)
	require.Equal(t, []string{"memory", "shell"}, registry.policy.GroupNames)
}

func TestAgent_RespondRunsParallelSafeToolsConcurrentlyAndAppendsInOrder(t *testing.T) {
	store := newStubSessionStore()
	completed := make(chan string, 2)
	secondCompleted := make(chan struct{})
	registry := &stubToolRegistry{
		definitions: []tool.Definition{{Name: "lookup", ParallelSafe: true}},
		invoke: func(_ context.Context, call tool.Call) message.Message {
			switch call.ID {
			case "call_1":
				select {
				case <-secondCompleted:
				case <-time.After(250 * time.Millisecond):
				}
			case "call_2":
				completed <- call.ID
				close(secondCompleted)

				return message.Message{
					Role:       message.RoleTool,
					Name:       call.Name,
					ToolCallID: call.ID,
					Content:    `{"ok":true}`,
				}
			}
			completed <- call.ID

			return message.Message{
				Role:       message.RoleTool,
				Name:       call.Name,
				ToolCallID: call.ID,
				Content:    `{"ok":true}`,
			}
		},
	}
	client := &stubModelClient{}
	client.complete = func(_ context.Context, request model.Request) (*model.Response, error) {
		if len(request.Messages) == 1 {
			return &model.Response{
				RequiresToolCalls: true,
				ToolCalls: []model.ToolCall{
					{ID: "call_1", Name: "lookup", Input: `{"query":"first"}`},
					{ID: "call_2", Name: "lookup", Input: `{"query":"second"}`},
				},
			}, nil
		}

		require.Len(t, request.Messages, 4)
		require.Equal(t, message.RoleAssistant, request.Messages[1].Role)
		require.Equal(t, []message.ToolCall{
			{ID: "call_1", Name: "lookup", Input: `{"query":"first"}`},
			{ID: "call_2", Name: "lookup", Input: `{"query":"second"}`},
		}, request.Messages[1].ToolCalls)
		require.Equal(t, "call_1", request.Messages[2].ToolCallID)
		require.Equal(t, "call_2", request.Messages[3].ToolCallID)

		return &model.Response{OutputText: "done"}, nil
	}

	core, err := New(Options{
		Model:         "test-model",
		API:           model.APIOpenAICompletions,
		ModelClient:   client,
		SessionStore:  store,
		ToolRegistry:  registry,
		MaxIterations: 2,
	})
	require.NoError(t, err)

	reply, err := core.Respond(context.Background(), "hello", RespondOptions{})
	require.NoError(t, err)
	require.Equal(t, "done", reply)
	require.Equal(t, "call_2", <-completed)
	require.Equal(t, "call_1", <-completed)
	require.Equal(t, "call_1", store.messages[session.DefaultID][2].ToolCallID)
	require.Equal(t, "call_2", store.messages[session.DefaultID][3].ToolCallID)
}

func TestAgent_RespondKeepsUnsafeToolsSequential(t *testing.T) {
	store := newStubSessionStore()
	registry := &stubToolRegistry{
		definitions: []tool.Definition{{Name: "lookup"}},
		invoke: func(_ context.Context, call tool.Call) message.Message {
			return message.Message{
				Role:       message.RoleTool,
				Name:       call.Name,
				ToolCallID: call.ID,
				Content:    `{"ok":true}`,
			}
		},
	}
	client := &stubModelClient{}
	client.complete = func(_ context.Context, request model.Request) (*model.Response, error) {
		if len(request.Messages) == 1 {
			return &model.Response{
				RequiresToolCalls: true,
				ToolCalls: []model.ToolCall{
					{ID: "call_1", Name: "lookup", Input: `{"query":"first"}`},
					{ID: "call_2", Name: "lookup", Input: `{"query":"second"}`},
				},
			}, nil
		}

		return &model.Response{OutputText: "done"}, nil
	}

	core, err := New(Options{
		Model:         "test-model",
		API:           model.APIOpenAICompletions,
		ModelClient:   client,
		SessionStore:  store,
		ToolRegistry:  registry,
		MaxIterations: 2,
	})
	require.NoError(t, err)

	_, err = core.Respond(context.Background(), "hello", RespondOptions{})
	require.NoError(t, err)
	require.Equal(t, []tool.Call{
		{ID: "call_1", Name: "lookup", Input: `{"query":"first"}`, Source: "model"},
		{ID: "call_2", Name: "lookup", Input: `{"query":"second"}`, Source: "model"},
	}, registry.Calls())
}

func TestAgent_RespondRunsAdjacentSafeToolGroupsInParallel(t *testing.T) {
	store := newStubSessionStore()
	completed := make(chan string, 4)
	fourthStarted := make(chan struct{})
	registry := &stubToolRegistry{
		definitions: []tool.Definition{
			{Name: "lookup", ParallelSafe: true},
			{Name: "memory_add"},
			{Name: "extract", ParallelSafe: true},
			{Name: "time", ParallelSafe: true},
		},
		invoke: func(_ context.Context, call tool.Call) message.Message {
			switch call.ID {
			case "call_3":
				select {
				case <-fourthStarted:
				case <-time.After(250 * time.Millisecond):
				}
			case "call_4":
				close(fourthStarted)
				completed <- call.ID

				return message.Message{
					Role:       message.RoleTool,
					Name:       call.Name,
					ToolCallID: call.ID,
					Content:    `{"ok":true}`,
				}
			}
			completed <- call.ID

			return message.Message{
				Role:       message.RoleTool,
				Name:       call.Name,
				ToolCallID: call.ID,
				Content:    `{"ok":true}`,
			}
		},
	}
	client := &stubModelClient{}
	client.complete = func(_ context.Context, request model.Request) (*model.Response, error) {
		if len(request.Messages) == 1 {
			return &model.Response{
				RequiresToolCalls: true,
				ToolCalls: []model.ToolCall{
					{ID: "call_1", Name: "lookup", Input: `{"query":"first"}`},
					{ID: "call_2", Name: "memory_add", Input: `{"text":"durable"}`},
					{ID: "call_3", Name: "extract", Input: `{"url":"https://example.com"}`},
					{ID: "call_4", Name: "time", Input: `{}`},
				},
			}, nil
		}

		require.Len(t, request.Messages, 6)
		require.Equal(t, "call_1", request.Messages[2].ToolCallID)
		require.Equal(t, "call_2", request.Messages[3].ToolCallID)
		require.Equal(t, "call_3", request.Messages[4].ToolCallID)
		require.Equal(t, "call_4", request.Messages[5].ToolCallID)

		return &model.Response{OutputText: "done"}, nil
	}

	core, err := New(Options{
		Model:         "test-model",
		API:           model.APIOpenAICompletions,
		ModelClient:   client,
		SessionStore:  store,
		ToolRegistry:  registry,
		MaxIterations: 2,
	})
	require.NoError(t, err)

	_, err = core.Respond(context.Background(), "hello", RespondOptions{})
	require.NoError(t, err)
	require.Equal(t, "call_1", <-completed)
	require.Equal(t, "call_2", <-completed)
	require.Equal(t, "call_4", <-completed)
	require.Equal(t, "call_3", <-completed)
	require.Equal(t, "call_1", store.messages[session.DefaultID][2].ToolCallID)
	require.Equal(t, "call_2", store.messages[session.DefaultID][3].ToolCallID)
	require.Equal(t, "call_3", store.messages[session.DefaultID][4].ToolCallID)
	require.Equal(t, "call_4", store.messages[session.DefaultID][5].ToolCallID)
}

func TestBuildToolCallBatches_GroupsAdjacentParallelSafeCalls(t *testing.T) {
	batches := BuildToolCallBatches(
		[]model.ToolCall{
			{ID: "call_1", Name: "web_search"},
			{ID: "call_2", Name: "memory_add"},
			{ID: "call_3", Name: "web_extract"},
			{ID: "call_4", Name: "time"},
			{ID: "call_5", Name: "memory_update"},
		},
		[]model.ToolDefinition{
			{Name: "web_search", ParallelSafe: true},
			{Name: "memory_add"},
			{Name: "web_extract", ParallelSafe: true},
			{Name: "time", ParallelSafe: true},
			{Name: "memory_update"},
		},
	)

	require.Len(t, batches, 4)
	require.False(t, batches[0].Parallel)
	require.Equal(t, []string{"call_1"}, toolExecutionTestCallIDs(batches[0].ToolCalls))
	require.False(t, batches[1].Parallel)
	require.Equal(t, []string{"call_2"}, toolExecutionTestCallIDs(batches[1].ToolCalls))
	require.True(t, batches[2].Parallel)
	require.Equal(t, []string{"call_3", "call_4"}, toolExecutionTestCallIDs(batches[2].ToolCalls))
	require.False(t, batches[3].Parallel)
	require.Equal(t, []string{"call_5"}, toolExecutionTestCallIDs(batches[3].ToolCalls))
}

func TestBuildToolCallBatches_TreatsUnknownAndBlankToolsAsSequential(t *testing.T) {
	batches := BuildToolCallBatches(
		[]model.ToolCall{
			{ID: "call_1", Name: "web_search"},
			{ID: "call_2", Name: ""},
			{ID: "call_3", Name: "web_extract"},
		},
		[]model.ToolDefinition{
			{Name: "web_search", ParallelSafe: true},
			{Name: "web_extract", ParallelSafe: true},
		},
	)

	require.Len(t, batches, 3)
	require.False(t, batches[0].Parallel)
	require.Equal(t, []string{"call_1"}, toolExecutionTestCallIDs(batches[0].ToolCalls))
	require.False(t, batches[1].Parallel)
	require.Equal(t, []string{"call_2"}, toolExecutionTestCallIDs(batches[1].ToolCalls))
	require.False(t, batches[2].Parallel)
	require.Equal(t, []string{"call_3"}, toolExecutionTestCallIDs(batches[2].ToolCalls))
}

func TestBuildToolCallBatches_ReturnsNoBatchesForNoToolCalls(t *testing.T) {
	require.Empty(t, BuildToolCallBatches(nil, []model.ToolDefinition{{Name: "time", ParallelSafe: true}}))
}

func TestAgent_ExecuteToolCallsParallel_CancelsSiblingsOnFatalError(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	var once sync.Once
	core := &Agent{
		opts: Options{
			ToolRegistry: &stubToolRegistry{
				invoke: func(ctx context.Context, call tool.Call) message.Message {
					if call.ID == "call_1" {
						once.Do(func() { close(started) })
						<-ctx.Done()
						close(cancelled)
						return toolExecutionTestMessage(call, `{"cancelled":true}`)
					}

					<-started
					return message.Message{
						Role:    message.RoleTool,
						Name:    call.Name,
						Content: `{"invalid":true}`,
					}
				},
			},
		},
	}

	_, err := core.executeToolCalls(
		context.Background(),
		[]model.ToolCall{
			{ID: "call_1", Name: "time", Input: "{}"},
			{ID: "call_2", Name: "time", Input: "{}"},
		},
		[]model.ToolDefinition{{Name: "time", ParallelSafe: true}},
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

func TestAgent_ExecuteToolCalls_ReturnsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	core := &Agent{
		opts: Options{
			ToolRegistry: &stubToolRegistry{
				invoke: func(_ context.Context, call tool.Call) message.Message {
					return toolExecutionTestMessage(call, `{"ok":true}`)
				},
			},
		},
	}

	_, err := core.executeToolCalls(
		ctx,
		[]model.ToolCall{{ID: "call_1", Name: "time", Input: "{}"}},
		[]model.ToolDefinition{{Name: "time", ParallelSafe: true}},
	)

	require.ErrorIs(t, err, context.Canceled)
}

func TestExecuteToolCalls_RequiresExecutor(t *testing.T) {
	_, err := ExecuteToolCalls(context.Background(), ToolCallExecutionOptions{
		ToolCalls: []model.ToolCall{{ID: "call_1", Name: "time", Input: "{}"}},
	})

	require.EqualError(t, err, "tool call executor is required")
}

func TestGetToolCallResultsError_ReturnsContextErrorWhenOnlyContextFailures(t *testing.T) {
	err := getToolCallResultsError([]toolCallResult{
		{err: context.Canceled},
		{err: context.DeadlineExceeded},
	})

	require.ErrorIs(t, err, context.Canceled)
}

func TestGetToolCallResultsError_PrefersRootErrorOverContextCancellation(t *testing.T) {
	rootErr := errors.New("root failure")

	err := getToolCallResultsError([]toolCallResult{
		{err: context.Canceled},
		{err: rootErr},
	})

	require.ErrorIs(t, err, rootErr)
}

func TestGetToolCallResultsError_ReturnsNilWhenAllCallsSucceeded(t *testing.T) {
	require.NoError(t, getToolCallResultsError([]toolCallResult{
		{message: message.Message{Role: message.RoleTool, ToolCallID: "call_1", Content: "{}"}},
		{message: message.Message{Role: message.RoleTool, ToolCallID: "call_2", Content: "{}"}},
	}))
}

func TestGetParallelSafeToolNames_ReturnsNilForNoDefinitions(t *testing.T) {
	require.Nil(t, getParallelSafeToolNames(nil))
}

func TestNewAgent_ValidationAndDefaults(t *testing.T) {
	store := &stubSessionStore{}
	client := &stubModelClient{}

	_, err := NewAgent(Options{SessionStore: store})
	require.EqualError(t, err, "model client is required")

	_, err = NewAgent(Options{ModelClient: client})
	require.EqualError(t, err, "session store is required")

	core, err := NewAgent(Options{ModelClient: client, SessionStore: store})
	require.NoError(t, err)
	require.Equal(t, defaultMaxIterations, core.opts.MaxIterations)
}

func TestAgent_RespondRejectsNilAgentAndEmptyInput(t *testing.T) {
	var core *Agent

	_, err := core.Respond(context.Background(), "hello", RespondOptions{})
	require.EqualError(t, err, "agent is required")

	core = &Agent{}
	_, err = core.Respond(context.Background(), "  ", RespondOptions{})
	require.EqualError(t, err, "message is required")
}

func TestAgent_RespondPropagatesSetupErrors(t *testing.T) {
	expected := errors.New("boom")

	tests := []struct {
		name  string
		store *stubSessionStore
	}{
		{
			name:  "resolve",
			store: &stubSessionStore{resolveErr: expected},
		},
		{
			name:  "load messages",
			store: &stubSessionStore{getMessagesErr: expected},
		},
		{
			name:  "append user",
			store: &stubSessionStore{appendErrAt: 1, appendErr: expected},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			core := &Agent{opts: Options{
				ModelClient:  &stubModelClient{},
				SessionStore: test.store,
			}}

			_, err := core.Respond(context.Background(), "hello", RespondOptions{})
			require.ErrorIs(t, err, expected)
		})
	}
}

func TestAgent_RespondPropagatesModelAndPersistenceErrors(t *testing.T) {
	expected := errors.New("boom")

	tests := []struct {
		name   string
		core   *Agent
		errMsg string
	}{
		{
			name: "tool resolve",
			core: &Agent{opts: Options{
				ModelClient:   &stubModelClient{},
				SessionStore:  &stubSessionStore{},
				ToolRegistry:  &stubToolRegistry{resolveErr: expected},
				MaxIterations: 1,
			}},
		},
		{
			name: "base instruction",
			core: &Agent{opts: Options{
				ModelClient:    &stubModelClient{},
				SessionStore:   &stubSessionStore{},
				PromptProvider: &stubPromptProvider{baseErr: expected},
				MaxIterations:  1,
			}},
		},
		{
			name: "environment instruction",
			core: &Agent{opts: Options{
				ModelClient:  &stubModelClient{},
				SessionStore: &stubSessionStore{},
				PromptProvider: &stubPromptProvider{
					base:   []prompt.Instruction{{Value: "base"}},
					envErr: expected,
				},
				MaxIterations: 1,
			}},
		},
		{
			name: "complete",
			core: &Agent{opts: Options{
				ModelClient:   &stubModelClient{completeErr: expected},
				SessionStore:  &stubSessionStore{},
				MaxIterations: 1,
			}},
		},
		{
			name: "nil response",
			core: &Agent{opts: Options{
				ModelClient:   &stubModelClient{},
				SessionStore:  &stubSessionStore{},
				MaxIterations: 1,
			}},
			errMsg: "model response is required",
		},
		{
			name: "prompt tokens",
			core: &Agent{opts: Options{
				ModelClient: &stubModelClient{response: &model.Response{
					OutputText:   "ok",
					PromptTokens: 7,
				}},
				SessionStore:  &stubSessionStore{updatePromptTokensErr: expected},
				MaxIterations: 1,
			}},
		},
		{
			name: "empty assistant",
			core: &Agent{opts: Options{
				ModelClient:   &stubModelClient{response: &model.Response{}},
				SessionStore:  &stubSessionStore{},
				MaxIterations: 1,
			}},
			errMsg: "message content is required",
		},
		{
			name: "append assistant",
			core: &Agent{opts: Options{
				ModelClient:   &stubModelClient{response: &model.Response{OutputText: "ok"}},
				SessionStore:  &stubSessionStore{appendErrAt: 2, appendErr: expected},
				MaxIterations: 1,
			}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.core.Respond(context.Background(), "hello", RespondOptions{})
			if test.errMsg != "" {
				require.EqualError(t, err, test.errMsg)
				return
			}

			require.ErrorIs(t, err, expected)
		})
	}
}

func TestAgent_RespondPropagatesToolLoopErrors(t *testing.T) {
	tests := []struct {
		name   string
		core   *Agent
		errMsg string
	}{
		{
			name: "requires tool calls without calls",
			core: &Agent{opts: Options{
				ModelClient: &stubModelClient{response: &model.Response{
					RequiresToolCalls: true,
				}},
				SessionStore:  &stubSessionStore{},
				ToolRegistry:  &stubToolRegistry{},
				MaxIterations: 1,
			}},
			errMsg: "model requested tool execution without tool calls",
		},
		{
			name: "missing tool registry",
			core: &Agent{opts: Options{
				ModelClient: &stubModelClient{response: &model.Response{
					RequiresToolCalls: true,
					ToolCalls:         []model.ToolCall{{ID: "call_1", Name: "lookup"}},
				}},
				SessionStore:  &stubSessionStore{},
				MaxIterations: 1,
			}},
			errMsg: "tool registry is required",
		},
		{
			name: "invalid assistant tool call",
			core: &Agent{opts: Options{
				ModelClient: &stubModelClient{response: &model.Response{
					RequiresToolCalls: true,
					ToolCalls:         []model.ToolCall{{ID: "call_1"}},
				}},
				SessionStore:  &stubSessionStore{},
				ToolRegistry:  &stubToolRegistry{},
				MaxIterations: 1,
			}},
			errMsg: "tool call name is required",
		},
		{
			name: "append assistant tool call",
			core: &Agent{opts: Options{
				ModelClient: &stubModelClient{response: &model.Response{
					RequiresToolCalls: true,
					ToolCalls:         []model.ToolCall{{ID: "call_1", Name: "lookup"}},
				}},
				SessionStore:  &stubSessionStore{appendErrAt: 2, appendErr: errors.New("append failed")},
				ToolRegistry:  &stubToolRegistry{},
				MaxIterations: 1,
			}},
			errMsg: "append failed",
		},
		{
			name: "tool execution",
			core: &Agent{opts: Options{
				ModelClient: &stubModelClient{response: &model.Response{
					RequiresToolCalls: true,
					ToolCalls:         []model.ToolCall{{ID: "call_1", Name: "lookup"}},
				}},
				SessionStore: &stubSessionStore{},
				ToolRegistry: &stubToolRegistry{
					invoke: func(context.Context, tool.Call) message.Message {
						return message.Message{Role: message.RoleTool, Content: "{}"}
					},
				},
				MaxIterations: 1,
			}},
			errMsg: "tool call id is required",
		},
		{
			name: "append tool result",
			core: &Agent{opts: Options{
				ModelClient: &stubModelClient{response: &model.Response{
					RequiresToolCalls: true,
					ToolCalls:         []model.ToolCall{{ID: "call_1", Name: "lookup"}},
				}},
				SessionStore: &stubSessionStore{appendErrAt: 3, appendErr: errors.New("append tool failed")},
				ToolRegistry: &stubToolRegistry{
					invoke: func(_ context.Context, call tool.Call) message.Message {
						return message.Message{
							Role:       message.RoleTool,
							Name:       call.Name,
							ToolCallID: call.ID,
							Content:    "{}",
						}
					},
				},
				MaxIterations: 1,
			}},
			errMsg: "append tool failed",
		},
		{
			name: "iteration exhausted",
			core: &Agent{opts: Options{
				ModelClient: &stubModelClient{response: &model.Response{
					RequiresToolCalls: true,
					ToolCalls:         []model.ToolCall{{ID: "call_1", Name: "lookup"}},
				}},
				SessionStore: &stubSessionStore{},
				ToolRegistry: &stubToolRegistry{
					invoke: func(_ context.Context, call tool.Call) message.Message {
						return message.Message{
							Role:       message.RoleTool,
							Name:       call.Name,
							ToolCallID: call.ID,
							Content:    "{}",
						}
					},
				},
				MaxIterations: 1,
			}},
			errMsg: "iteration budget exhausted after 1 steps",
		},
		{
			name: "empty iteration budget",
			core: &Agent{opts: Options{
				ModelClient:  &stubModelClient{response: &model.Response{OutputText: "unused"}},
				SessionStore: &stubSessionStore{},
			}},
			errMsg: "iteration budget exhausted after 0 steps",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.core.Respond(context.Background(), "hello", RespondOptions{})
			require.EqualError(t, err, test.errMsg)
		})
	}
}

func TestAgent_CompleteStreamsTextEvents(t *testing.T) {
	stream := true
	var events []Event
	core := &Agent{opts: Options{
		ModelClient: &stubModelClient{
			streamResponse: &model.Response{OutputText: "done"},
			streamDeltas: []model.StreamDelta{
				{Channel: model.StreamChannelReasoning, Text: "thinking"},
				{Channel: model.StreamChannelAssistant, Text: ""},
				{Channel: model.StreamChannelAssistant, Text: "hello"},
			},
		},
	}}

	resp, err := core.complete(context.Background(), model.Request{}, RespondOptions{
		Stream:  &stream,
		OnEvent: func(event Event) { events = append(events, event) },
	})

	require.NoError(t, err)
	require.Equal(t, "done", resp.OutputText)
	require.Equal(t, []Event{
		{Kind: EventKindTextDelta, Channel: string(model.StreamChannelReasoning), Text: "thinking"},
		{Kind: EventKindTextDelta, Channel: string(model.StreamChannelAssistant), Text: "hello"},
	}, events)
}

func TestAgent_BuildInstructionsIncludesPromptProviderValues(t *testing.T) {
	provider := &stubPromptProvider{
		base: []prompt.Instruction{
			{Value: " base "},
			{Value: " "},
		},
		env: prompt.Instruction{Value: " env "},
	}
	core := &Agent{opts: Options{
		Model:          "test-model",
		API:            model.APIOpenAIResponses,
		PromptProvider: provider,
		RunContext:     prompt.RunContext{SessionID: "existing"},
	}}

	instructions, err := core.buildInstructions(
		context.Background(),
		"session_1",
		[]model.ToolDefinition{{Name: " lookup "}, {Name: " "}},
		RespondOptions{Instruct: " extra "},
	)

	require.NoError(t, err)
	require.Equal(t, "base\n\nenv\n\nextra", instructions)
	require.Equal(t, "existing", provider.baseContext.SessionID)
	require.Equal(t, "session_1", provider.environmentInput.SessionID)
	require.Equal(t, []string{"lookup"}, provider.environmentInput.ActiveTools)
	require.Equal(t, "test-model", provider.environmentInput.Model)
	require.Equal(t, model.APIOpenAIResponses, provider.environmentInput.API)
}

func TestAgent_BuildInstructionsUsesResolvedSessionWhenRunContextHasNoSession(t *testing.T) {
	provider := &stubPromptProvider{}
	core := &Agent{opts: Options{PromptProvider: provider}}

	instructions, err := core.buildInstructions(context.Background(), "session_1", nil, RespondOptions{})

	require.NoError(t, err)
	require.Empty(t, instructions)
	require.Equal(t, "session_1", provider.baseContext.SessionID)
}

func TestModelToolNamesReturnsNilForNoDefinitions(t *testing.T) {
	require.Nil(t, modelToolNames(nil))
}
