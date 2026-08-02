package agent

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"

	"github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/agent/model"
	"github.com/xymorphic/morph/pkg/agent/prompt"
	"github.com/xymorphic/morph/pkg/agent/session"
	"github.com/xymorphic/morph/pkg/agent/tool"
	"github.com/xymorphic/morph/pkg/str"
)

const defaultMaxIterations = 8

type Responder interface {
	Respond(context.Context, string, RespondOptions) (string, error)
}

type Options struct {
	Model          string
	API            string
	ModelClient    model.Client
	SessionStore   session.Store
	ToolRegistry   tool.Registry
	ToolPolicy     tool.Policy
	PromptProvider prompt.Provider
	RunContext     prompt.RunContext
	MaxIterations  int
	DebugRequests  bool
}

type RespondOptions struct {
	Instruct    string
	SessionID   string
	ToolGroups  []string
	Stream      *bool
	TraceEvents bool
	OnEvent     func(Event)
}

type Agent struct {
	opts Options
}

type ToolCallExecutor func(context.Context, model.ToolCall) (message.Message, error)

type ToolCallExecutionOptions struct {
	ToolCalls   []model.ToolCall
	Definitions []model.ToolDefinition
	Execute     ToolCallExecutor
}

type ToolCallBatch struct {
	ToolCalls []model.ToolCall
	Parallel  bool
}

func New(opts Options) (*Agent, error) {
	return NewAgent(opts)
}

func NewAgent(opts Options) (*Agent, error) {
	if opts.ModelClient == nil {
		return nil, errors.New("model client is required")
	}
	if opts.SessionStore == nil {
		return nil, errors.New("session store is required")
	}

	if opts.MaxIterations <= 0 {
		opts.MaxIterations = defaultMaxIterations
	}

	return &Agent{opts: opts}, nil
}

func (a *Agent) Respond(ctx context.Context, input string, opts RespondOptions) (string, error) {
	if a == nil {
		return "", errors.New("agent is required")
	}

	inputValue := str.String(input)
	input = inputValue.Trim()
	if input == "" {
		return "", errors.New("message is required")
	}

	resolved, err := a.opts.SessionStore.Resolve(ctx, opts.SessionID)
	if err != nil {
		return "", err
	}

	history, err := a.opts.SessionStore.GetMessages(ctx, resolved.ID, session.MessageQuery{})
	if err != nil {
		return "", err
	}

	userMessage, _ := message.New(message.RoleUser, input)
	if err := a.opts.SessionStore.AppendMessages(ctx, resolved.ID, []message.Message{userMessage}); err != nil {
		return "", err
	}

	emitted := []message.Message{userMessage}
	remainingIterations := a.opts.MaxIterations
	runStep := func(ctx context.Context) (LoopDecision, error) {
		request, err := a.buildRequest(ctx, resolved.ID, history, emitted, opts)
		if err != nil {
			return LoopDecision{}, err
		}

		resp, err := a.complete(ctx, request, opts)
		if err != nil {
			return LoopDecision{}, err
		}
		if resp == nil {
			return LoopDecision{}, errors.New("model response is required")
		}

		if resp.PromptTokens > 0 {
			if err := a.opts.SessionStore.UpdateLastPromptTokens(ctx, resolved.ID, resp.PromptTokens); err != nil {
				return LoopDecision{}, err
			}
		}

		if !resp.RequiresToolCalls {
			assistantMessage, err := message.New(message.RoleAssistant, resp.OutputText)
			if err != nil {
				return LoopDecision{}, err
			}
			if err := a.opts.SessionStore.AppendMessages(ctx, resolved.ID, []message.Message{assistantMessage}); err != nil {
				return LoopDecision{}, err
			}

			return LoopDecision{Done: true, Reply: assistantMessage.Content}, nil
		}

		if len(resp.ToolCalls) == 0 {
			return LoopDecision{}, errors.New("model requested tool execution without tool calls")
		}
		if a.opts.ToolRegistry == nil {
			return LoopDecision{}, errors.New("tool registry is required")
		}

		assistantMessage, err := message.Normalize(message.Message{
			Role:      message.RoleAssistant,
			ToolCalls: model.ToolCallsToMessageToolCalls(resp.ToolCalls),
		})
		if err != nil {
			return LoopDecision{}, err
		}
		if err := a.opts.SessionStore.AppendMessages(ctx, resolved.ID, []message.Message{assistantMessage}); err != nil {
			return LoopDecision{}, err
		}
		emitted = append(emitted, assistantMessage)

		toolMessages, err := a.executeToolCalls(ctx, resp.ToolCalls, request.Tools)
		if err != nil {
			return LoopDecision{}, err
		}
		if err := a.opts.SessionStore.AppendMessages(ctx, resolved.ID, toolMessages); err != nil {
			return LoopDecision{}, err
		}
		emitted = append(emitted, toolMessages...)

		return LoopDecision{}, nil
	}

	return RunLoop(ctx, LoopOptions{
		Consume: func() bool {
			if remainingIterations <= 0 {
				return false
			}

			remainingIterations--
			return true
		},
		RunStep: runStep,
		OnExhausted: func(context.Context) (string, error) {
			return "", errors.New("iteration budget exhausted after " + strconv.Itoa(a.opts.MaxIterations) + " steps")
		},
	})
}

func (a *Agent) buildRequest(
	ctx context.Context,
	sessionID string,
	history []message.Message,
	emitted []message.Message,
	opts RespondOptions,
) (model.Request, error) {
	definitions, err := a.resolveToolDefinitions(opts)
	if err != nil {
		return model.Request{}, err
	}

	instructions, err := a.buildInstructions(ctx, sessionID, definitions, opts)
	if err != nil {
		return model.Request{}, err
	}

	return model.Request{
		Model:         a.opts.Model,
		API:           a.opts.API,
		Instructions:  instructions,
		Messages:      append(message.CloneMessages(history), emitted...),
		Tools:         definitions,
		DebugRequests: a.opts.DebugRequests,
	}, nil
}

func (a *Agent) complete(ctx context.Context, request model.Request, opts RespondOptions) (*model.Response, error) {
	if opts.Stream != nil && *opts.Stream {
		return a.opts.ModelClient.CompleteStream(ctx, request, func(delta model.StreamDelta) {
			if opts.OnEvent == nil || delta.Text == "" {
				return
			}

			opts.OnEvent(Event{
				Kind:    EventKindTextDelta,
				Channel: string(delta.Channel),
				Text:    delta.Text,
			})
		})
	}

	return a.opts.ModelClient.Complete(ctx, request)
}

type toolCallResult struct {
	message message.Message
	err     error
}

func (a *Agent) executeToolCalls(
	ctx context.Context,
	toolCalls []model.ToolCall,
	definitions []model.ToolDefinition,
) ([]message.Message, error) {
	return ExecuteToolCalls(ctx, ToolCallExecutionOptions{
		ToolCalls:   toolCalls,
		Definitions: definitions,
		Execute:     a.executeToolCall,
	})
}

func ExecuteToolCalls(ctx context.Context, opts ToolCallExecutionOptions) ([]message.Message, error) {
	if opts.Execute == nil {
		return nil, errors.New("tool call executor is required")
	}

	batches := BuildToolCallBatches(opts.ToolCalls, opts.Definitions)
	messages := make([]message.Message, 0, len(opts.ToolCalls))
	for _, batch := range batches {
		var batchMessages []message.Message
		var err error
		if batch.Parallel {
			batchMessages, err = executeToolCallsParallel(ctx, batch.ToolCalls, opts.Execute)
		} else {
			batchMessages, err = executeToolCallsSequential(ctx, batch.ToolCalls, opts.Execute)
		}
		if err != nil {
			return nil, err
		}

		messages = append(messages, batchMessages...)
	}

	return messages, nil
}

func executeToolCallsSequential(
	ctx context.Context,
	toolCalls []model.ToolCall,
	execute ToolCallExecutor,
) ([]message.Message, error) {
	messages := make([]message.Message, 0, len(toolCalls))
	for _, modelToolCall := range toolCalls {
		toolMessage, err := execute(ctx, modelToolCall)
		if err != nil {
			return nil, err
		}

		messages = append(messages, toolMessage)
	}

	return messages, nil
}

func executeToolCallsParallel(
	ctx context.Context,
	toolCalls []model.ToolCall,
	execute ToolCallExecutor,
) ([]message.Message, error) {
	toolCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make([]toolCallResult, len(toolCalls))
	var wg sync.WaitGroup

	for index, modelToolCall := range toolCalls {
		index, modelToolCall := index, modelToolCall
		wg.Go(func() {
			toolMessage, err := execute(toolCtx, modelToolCall)
			if err != nil {
				cancel()
			}
			results[index] = toolCallResult{message: toolMessage, err: err}
		})
	}

	wg.Wait()

	messages := make([]message.Message, 0, len(results))
	for _, result := range results {
		messages = append(messages, result.message)
	}
	if err := getToolCallResultsError(results); err != nil {
		return nil, err
	}

	return messages, nil
}

func (a *Agent) executeToolCall(ctx context.Context, modelToolCall model.ToolCall) (message.Message, error) {
	if err := ctx.Err(); err != nil {
		return message.Message{}, err
	}

	return message.Normalize(a.opts.ToolRegistry.Invoke(ctx, tool.CallFromModel(modelToolCall)))
}

func BuildToolCallBatches(
	toolCalls []model.ToolCall,
	definitions []model.ToolDefinition,
) []ToolCallBatch {
	parallelSafe := getParallelSafeToolNames(definitions)
	batches := make([]ToolCallBatch, 0, len(toolCalls))
	var current []model.ToolCall

	for _, toolCall := range toolCalls {
		toolName := str.String(toolCall.Name)
		name := toolName.Trim()
		if name != "" && parallelSafe[name] {
			current = append(current, toolCall)
			continue
		}

		batches = appendToolCallBatch(batches, current)
		current = nil
		batches = append(batches, ToolCallBatch{ToolCalls: []model.ToolCall{toolCall}})
	}

	return appendToolCallBatch(batches, current)
}

func appendToolCallBatch(batches []ToolCallBatch, toolCalls []model.ToolCall) []ToolCallBatch {
	if len(toolCalls) == 0 {
		return batches
	}

	return append(batches, ToolCallBatch{
		ToolCalls: toolCalls,
		Parallel:  len(toolCalls) > 1,
	})
}

func getParallelSafeToolNames(definitions []model.ToolDefinition) map[string]bool {
	if len(definitions) == 0 {
		return nil
	}

	names := make(map[string]bool)
	for _, definition := range definitions {
		definitionName := str.String(definition.Name)
		name := definitionName.Trim()
		if name != "" && definition.ParallelSafe {
			names[name] = true
		}
	}

	return names
}

func getToolCallResultsError(results []toolCallResult) error {
	var contextErr error
	for _, result := range results {
		if result.err == nil {
			continue
		}
		if errors.Is(result.err, context.Canceled) || errors.Is(result.err, context.DeadlineExceeded) {
			if contextErr == nil {
				contextErr = result.err
			}
			continue
		}

		return result.err
	}

	return contextErr
}

func (a *Agent) resolveToolDefinitions(opts RespondOptions) ([]model.ToolDefinition, error) {
	if a.opts.ToolRegistry == nil {
		return nil, nil
	}

	policy := a.opts.ToolPolicy
	if len(opts.ToolGroups) > 0 {
		policy.GroupNames = append([]string(nil), opts.ToolGroups...)
	}
	definitions, err := a.opts.ToolRegistry.Resolve(policy)
	if err != nil {
		return nil, err
	}

	return tool.DefinitionsToModel(definitions), nil
}

func (a *Agent) buildInstructions(
	ctx context.Context,
	sessionID string,
	definitions []model.ToolDefinition,
	opts RespondOptions,
) (string, error) {
	blocks := make([]string, 0, 3)
	if a.opts.PromptProvider != nil {
		runContext := a.opts.RunContext
		runSessionID := str.String(runContext.SessionID)
		if runSessionID.Trim() == "" {
			runContext.SessionID = sessionID
		}

		baseInstructions, err := a.opts.PromptProvider.LoadBaseInstructions(ctx, runContext)
		if err != nil {
			return "", err
		}
		blocks = appendInstructionValues(blocks, baseInstructions)

		environmentInstruction, err := a.opts.PromptProvider.BuildEnvironmentInstruction(ctx, prompt.EnvironmentInput{
			SessionID:   sessionID,
			ActiveTools: modelToolNames(definitions),
			Model:       a.opts.Model,
			API:         a.opts.API,
		})
		if err != nil {
			return "", err
		}
		environmentValue := str.String(environmentInstruction.Value)
		if value := environmentValue.Trim(); value != "" {
			blocks = append(blocks, value)
		}
	}

	instructionOverride := str.String(opts.Instruct)
	if instruct := instructionOverride.Trim(); instruct != "" {
		blocks = append(blocks, instruct)
	}

	return strings.Join(blocks, "\n\n"), nil
}

func appendInstructionValues(blocks []string, instructions prompt.Instructions) []string {
	for _, instruction := range instructions {
		instructionValue := str.String(instruction.Value)
		if value := instructionValue.Trim(); value != "" {
			blocks = append(blocks, value)
		}
	}

	return blocks
}

func modelToolNames(definitions []model.ToolDefinition) []string {
	if len(definitions) == 0 {
		return nil
	}

	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		definitionName := str.String(definition.Name)
		if name := definitionName.Trim(); name != "" {
			names = append(names, name)
		}
	}

	return names
}
