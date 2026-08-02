package agent

import (
	"context"
	"sync"

	"github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/agent/model"
	"github.com/xymorphic/morph/pkg/agent/prompt"
	"github.com/xymorphic/morph/pkg/agent/session"
	"github.com/xymorphic/morph/pkg/agent/tool"
)

type stubSessionStore struct {
	messages         map[string][]message.Message
	lastPromptTokens int

	appendCalls           int
	appendErrAt           int
	appendErr             error
	getMessagesErr        error
	resolveErr            error
	updatePromptTokensErr error
}

func newStubSessionStore() *stubSessionStore {
	return &stubSessionStore{messages: map[string][]message.Message{}}
}

func (s *stubSessionStore) Resolve(_ context.Context, id string) (session.Session, error) {
	if s.resolveErr != nil {
		return session.Session{}, s.resolveErr
	}

	if id == "" {
		id = session.DefaultID
	}

	return session.Session{ID: id}, nil
}

func (s *stubSessionStore) GetMessages(
	_ context.Context,
	id string,
	_ session.MessageQuery,
) ([]message.Message, error) {
	if s.getMessagesErr != nil {
		return nil, s.getMessagesErr
	}

	return message.CloneMessages(s.messages[id]), nil
}

func (s *stubSessionStore) AppendMessages(_ context.Context, id string, messages []message.Message) error {
	s.appendCalls++
	if s.appendCalls == s.appendErrAt {
		return s.appendErr
	}

	if s.messages == nil {
		s.messages = map[string][]message.Message{}
	}
	if id == "" {
		id = session.DefaultID
	}

	s.messages[id] = append(s.messages[id], message.CloneMessages(messages)...)
	return nil
}

func (s *stubSessionStore) UpdateLastPromptTokens(_ context.Context, _ string, tokens int) error {
	if s.updatePromptTokensErr != nil {
		return s.updatePromptTokensErr
	}

	s.lastPromptTokens = tokens
	return nil
}

type stubModelClient struct {
	complete       func(context.Context, model.Request) (*model.Response, error)
	response       *model.Response
	completeErr    error
	streamDeltas   []model.StreamDelta
	streamResponse *model.Response
}

func (c *stubModelClient) Complete(ctx context.Context, request model.Request) (*model.Response, error) {
	if c.complete != nil {
		return c.complete(ctx, request)
	}

	return c.response, c.completeErr
}

func (c *stubModelClient) CompleteStream(
	ctx context.Context,
	request model.Request,
	onDelta func(model.StreamDelta),
) (*model.Response, error) {
	if c.complete != nil {
		return c.complete(ctx, request)
	}

	for _, delta := range c.streamDeltas {
		onDelta(delta)
	}

	return c.streamResponse, c.completeErr
}

type stubToolRegistry struct {
	mu          sync.Mutex
	calls       []tool.Call
	policy      tool.Policy
	definitions []tool.Definition
	resolveErr  error
	invoke      func(context.Context, tool.Call) message.Message
}

func (r *stubToolRegistry) Resolve(policy tool.Policy) ([]tool.Definition, error) {
	r.policy = policy
	if r.resolveErr != nil {
		return nil, r.resolveErr
	}
	if len(r.definitions) != 0 {
		return r.definitions, nil
	}

	return []tool.Definition{{Name: "lookup"}}, nil
}

func (r *stubToolRegistry) Invoke(ctx context.Context, call tool.Call) message.Message {
	r.mu.Lock()
	r.calls = append(r.calls, call)
	r.mu.Unlock()

	if r.invoke != nil {
		return r.invoke(ctx, call)
	}

	return message.Message{
		Role:       message.RoleTool,
		Name:       call.Name,
		ToolCallID: call.ID,
		Content:    "{}",
	}
}

func (r *stubToolRegistry) Calls() []tool.Call {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]tool.Call(nil), r.calls...)
}

func toolExecutionTestMessage(call tool.Call, content string) message.Message {
	return message.Message{
		Role:       message.RoleTool,
		Name:       call.Name,
		ToolCallID: call.ID,
		Content:    content,
	}
}

func toolExecutionTestCallIDs(toolCalls []model.ToolCall) []string {
	ids := make([]string, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		ids = append(ids, toolCall.ID)
	}

	return ids
}

type stubPromptProvider struct {
	base             prompt.Instructions
	env              prompt.Instruction
	baseErr          error
	envErr           error
	baseContext      prompt.RunContext
	environmentInput prompt.EnvironmentInput
}

func (p *stubPromptProvider) LoadBaseInstructions(
	_ context.Context,
	runContext prompt.RunContext,
) (prompt.Instructions, error) {
	p.baseContext = runContext
	return p.base, p.baseErr
}

func (p *stubPromptProvider) BuildEnvironmentInstruction(
	_ context.Context,
	input prompt.EnvironmentInput,
) (prompt.Instruction, error) {
	p.environmentInput = input
	return p.env, p.envErr
}
