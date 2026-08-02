package agent

import (
	"context"
	"fmt"

	"github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/agent/model"
	"github.com/xymorphic/morph/pkg/agent/session"
	"github.com/xymorphic/morph/pkg/agent/tool"
)

func ExampleAgent_Respond() {
	store := newStubSessionStore()
	client := &stubModelClient{
		complete: func(context.Context, model.Request) (*model.Response, error) {
			return &model.Response{OutputText: "hello from an embedded agent"}, nil
		},
	}

	core, err := New(Options{
		Model:        "example-model",
		API:          model.APIOpenAICompletions,
		ModelClient:  client,
		SessionStore: store,
	})
	if err != nil {
		panic(err)
	}

	reply, err := core.Respond(context.Background(), "hello", RespondOptions{})
	if err != nil {
		panic(err)
	}

	fmt.Println(reply)
	// Output: hello from an embedded agent
}

func ExampleAgent_Respond_withToolCall() {
	store := newStubSessionStore()
	registry := &stubToolRegistry{
		invoke: func(context.Context, tool.Call) message.Message {
			return message.Message{
				Role:       message.RoleTool,
				Name:       "lookup",
				ToolCallID: "call_1",
				Content:    `{"answer":"from tool"}`,
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

		return &model.Response{OutputText: "used the tool"}, nil
	}

	core, err := New(Options{
		Model:         "example-model",
		API:           model.APIOpenAICompletions,
		ModelClient:   client,
		SessionStore:  store,
		ToolRegistry:  registry,
		MaxIterations: 2,
	})
	if err != nil {
		panic(err)
	}

	reply, err := core.Respond(context.Background(), "hello", RespondOptions{})
	if err != nil {
		panic(err)
	}

	fmt.Println(reply)
	fmt.Println(len(registry.calls))
	// Output:
	// used the tool
	// 1
}

var _ session.Store = (*stubSessionStore)(nil)
