package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	ctxbuilder "github.com/wandxy/morph/internal/agent/context"
	"github.com/wandxy/morph/internal/agent/context/compaction"
	summarizer "github.com/wandxy/morph/internal/agent/context/summary"
	"github.com/wandxy/morph/internal/agent/runcontext"
	"github.com/wandxy/morph/internal/config"
	envbudget "github.com/wandxy/morph/internal/environment/budget"
	envtypes "github.com/wandxy/morph/internal/environment/types"
	"github.com/wandxy/morph/internal/guardrails"
	instruct "github.com/wandxy/morph/internal/instructions"
	"github.com/wandxy/morph/internal/memory"
	models "github.com/wandxy/morph/internal/model"
	"github.com/wandxy/morph/internal/profile"
	storage "github.com/wandxy/morph/internal/state/core"
	"github.com/wandxy/morph/internal/tools"
	"github.com/wandxy/morph/internal/trace"
	agentcore "github.com/wandxy/morph/pkg/agent"
	morphmsg "github.com/wandxy/morph/pkg/agent/message"
	agentprompt "github.com/wandxy/morph/pkg/agent/prompt"
	agentsession "github.com/wandxy/morph/pkg/agent/session"
	agenttool "github.com/wandxy/morph/pkg/agent/tool"
	"github.com/wandxy/morph/pkg/str"
)

const requestInstructionName = "request.instruct"

type traceSessionFactory interface {
	NewTraceSessionForRun(runcontext.Context) trace.Session
}

type safetyTraceEventSource interface {
	SafetyTraceEvents() []guardrails.SafetyTracePayloadOptions
}

type memoryProviderSource interface {
	MemoryProvider() memory.Provider
}

type iterationBudgetFactory interface {
	NewIterationBudget() envbudget.IterationBudget
}

type planStateStore interface {
	CurrentPlan(string) envtypes.Plan
	HydratePlan(string, envtypes.Plan)
}

type environmentToolRegistry interface {
	ListGroups() []tools.Group
	Resolve(tools.Policy) (tools.Definitions, error)
	Invoke(context.Context, tools.Call) (tools.Result, error)
}

// Turn executes a single response turn against a resolved session.
type Turn struct {
	// Request context for session writes during the turn.
	ctx           context.Context
	sessionOrigin storage.SessionOrigin

	// Model and execution settings for the turn.
	cfg *config.Config

	// Model client responsible for model requests for the turn.
	modelClient models.Client

	// Summary client for compaction/summary model requests. Falls back to modelClient if nil.
	summaryClient models.Client

	// Session store used for turn-scoped session reads and writes.
	sessionStore agentsession.Store

	// Trace recorder used for turn-scoped persisted trace writes.
	traceRecorder agentsession.TraceRecorder

	// Loads/persists summary state for the turn.
	summaryService *summarizer.Service

	// Summary store used to lazily build the summary service with current turn config.
	summaryStore summarizer.SummaryStore

	// Tool registry used to resolve and invoke model-visible tools.
	toolRegistry agenttool.Registry

	// Tool policy used to filter tool definitions for this runtime.
	toolPolicy agenttool.Policy

	// Prompt provider supplies reusable prompt inputs from the agent runtime.
	promptProvider agentprompt.Provider

	// Trace sessions are opened by the agent runtime.
	traceSessions traceSessionFactory

	// Safety events loaded by the agent runtime are replayed into the turn trace.
	safetyEvents safetyTraceEventSource

	// Memory provider source supplies durable memory for prompt retrieval.
	memoryProviders memoryProviderSource

	// Iteration budget factory controls model/tool loop limits.
	iterationBudgets iterationBudgetFactory

	// Plan state store keeps active plan context for the turn.
	plans planStateStore

	// env carries the Morph runtime environment for turn-scoped services.
	env any

	// Tool invocation function for executing tool calls during runtime hooks.
	invokeToolFn any

	// Builds model-visible message context for the turn.
	contextBuilder *ctxbuilder.Builder

	// Base instruction set sent to model.
	instructions instruct.Instructions

	// Per-call guidance from agentcore.RespondOptions.Instruct.
	requestInstruction instruct.Instruction

	// Durable memory retrieved for the current turn.
	memoryInstruction instruct.Instruction

	// Persisted messages loaded before the turn starts.
	sessionHistory []morphmsg.Message

	// Messages emitted during the current turn.
	emittedMessages []morphmsg.Message

	// Summary state used for active context assembly.
	summary *summarizer.State

	// Offset represented by sessionHistory[0].
	sessionHistoryOffset int

	// Session ID being read/written to.
	sessionID string

	// State identity, profile, and lineage for this run.
	runCtx runcontext.Context

	// Most recent provider measurement and the request prefix it covers.
	compactionAnchor compaction.Anchor

	// Tracks context size last evaluated for summary refresh.
	summaryRefreshAttemptedMessageCount int

	// Indicates if plan state was restored from session history.
	planHydrated bool

	afterToolBatchPersisted func(context.Context) (bool, error)
	reasoningSnapshot       agentsession.ReasoningSnapshot
}

// NewTurnWithSessionStore returns a turn with reusable session dependencies.
func NewTurnWithSessionStore(
	cfg *config.Config,
	modelClient models.Client,
	summaryClient models.Client,
	summaryStore summarizer.SummaryStore,
	sessionStore agentsession.Store,
	traceRecorder agentsession.TraceRecorder,
	toolRegistry agenttool.Registry,
	toolPolicy agenttool.Policy,
	promptProvider agentprompt.Provider,
	traceSessions traceSessionFactory,
	safetyEvents safetyTraceEventSource,
	memoryProviders memoryProviderSource,
	iterationBudgets iterationBudgetFactory,
	plans planStateStore,
	runtime any,
	invokeToolFn any,
) *Turn {
	if summaryClient == nil {
		summaryClient = modelClient
	}

	return &Turn{
		cfg:              cfg,
		modelClient:      modelClient,
		summaryClient:    summaryClient,
		summaryStore:     summaryStore,
		sessionStore:     sessionStore,
		traceRecorder:    traceRecorder,
		toolRegistry:     toolRegistry,
		toolPolicy:       toolPolicy,
		promptProvider:   promptProvider,
		traceSessions:    traceSessions,
		safetyEvents:     safetyEvents,
		memoryProviders:  memoryProviders,
		iterationBudgets: iterationBudgets,
		plans:            plans,
		env:              runtime,
		invokeToolFn:     invokeToolFn,
		contextBuilder:   ctxbuilder.New(),
	}
}

// load initializes all the dependencies, session, summary, instructions, message history,
// and plan state for a new Turn execution. Returns error if required initializations fail.
func (t *Turn) load(ctx context.Context, opts agentcore.RespondOptions) error {
	if t == nil {
		return errors.New("agent is required")
	}

	if t.cfg == nil {
		return errors.New("config is required")
	}

	if t.modelClient == nil {
		return errors.New("model client is required")
	}

	if !t.hasRuntimeCapabilities() {
		return errors.New("runtime environment is required")
	}

	if t.sessionStore == nil {
		return errors.New("session store is required")
	}

	// Resolve and load session for the turn; fail if not found.
	session, err := t.sessionStore.Resolve(ctx, opts.SessionID)
	if err != nil {
		return err
	}

	if t.summaryService == nil {
		if t.summaryStore == nil {
			return errors.New("summary store is required")
		}

		t.summaryService = summarizer.NewService(t.cfg, t.modelClient, t.summaryClient, t.summaryStore)
	}

	// Load active summary state for context assembly.
	summary, err := t.summaryService.Load(ctx, session.ID)
	if err != nil {
		return err
	}

	// Offset for loading messages, using summary end if available.
	tailOffset := 0
	if summary != nil && summary.Current != nil {
		tailOffset = max(summary.Current.SourceEndOffset, 0)
	}

	// Load messages after the (possibly summarized) offset.
	messages, err := t.sessionStore.GetMessages(ctx, session.ID, agentsession.MessageQuery{Offset: tailOffset})
	if err != nil {
		return err
	}

	// New identity context for session run.
	t.runCtx, err = newRootRunContext(session.ID)
	if err != nil {
		return err
	}

	instructions, err := t.loadBaseInstructions(ctx, session.ID)
	if err != nil {
		return err
	}

	// Assign all loaded state to this Turn instance.
	t.ctx = ctx
	t.instructions = instructions
	t.requestInstruction = instruct.Instruction{}
	t.memoryInstruction = instruct.Instruction{}
	t.sessionHistory = messages
	t.emittedMessages = nil
	t.summary = summary
	t.sessionHistoryOffset = tailOffset
	t.sessionID = session.ID
	t.sessionOrigin = storageSessionOriginFromAgentSessionOrigin(session.Origin)

	t.compactionAnchor = compaction.Anchor{}
	t.summaryRefreshAttemptedMessageCount = 0

	// Optionally hydrate restored plan from session history.
	t.planHydrated, err = t.hydratePlanFromHistory(ctx, t.getStateSessionID())
	if err != nil {
		return err
	}

	agentLog.Debug().
		Str("session_id", session.ID).
		Int("history_offset", tailOffset).
		Int("history_messages", len(messages)).
		Msg("turn context loaded for response generation")

	return nil
}

func (t *Turn) loadBaseInstructions(ctx context.Context, sessionID string) (instruct.Instructions, error) {
	if t == nil {
		return nil, nil
	}

	if t.promptProvider != nil {
		instructions, err := t.promptProvider.LoadBaseInstructions(ctx, agentprompt.RunContext{
			SessionID:          sessionID,
			PublicSessionID:    t.runCtx.Session.PublicID,
			EffectiveSessionID: t.runCtx.Session.EffectiveID,
			ProfileName:        t.runCtx.ProfileName,
		})
		if err != nil {
			return nil, err
		}

		return instructionsFromPromptInstructions(instructions), nil
	}

	return nil, nil
}

func (t *Turn) hasRuntimeCapabilities() bool {
	return t != nil &&
		(t.promptProvider != nil ||
			t.traceSessions != nil ||
			t.safetyEvents != nil ||
			t.memoryProviders != nil ||
			t.iterationBudgets != nil ||
			t.plans != nil ||
			t.toolRegistry != nil ||
			t.invokeToolFn != nil ||
			t.env != nil)
}

func instructionsFromPromptInstructions(instructions agentprompt.Instructions) instruct.Instructions {
	if len(instructions) == 0 {
		return nil
	}

	result := make(instruct.Instructions, 0, len(instructions))
	for _, instruction := range instructions {
		result = append(result, instruct.Instruction{
			Name:  instruction.Name,
			Value: instruction.Value,
		})
	}

	return result
}

func (t *Turn) newTraceSessionForRun() trace.Session {
	if t == nil {
		return trace.NoopSession()
	}
	if source, ok := t.env.(traceSessionFactory); ok {
		return source.NewTraceSessionForRun(t.runCtx)
	}
	if t.traceSessions == nil {
		return trace.NoopSession()
	}

	return t.traceSessions.NewTraceSessionForRun(t.runCtx)
}

func (t *Turn) newIterationBudget() envbudget.IterationBudget {
	if t == nil {
		return envbudget.New(0)
	}
	if source, ok := t.env.(iterationBudgetFactory); ok {
		return source.NewIterationBudget()
	}
	if t.iterationBudgets == nil {
		return envbudget.New(0)
	}

	return t.iterationBudgets.NewIterationBudget()
}

func (t *Turn) currentPlan(sessionID string) envtypes.Plan {
	if t == nil {
		return envtypes.Plan{}
	}
	if source, ok := t.env.(planStateStore); ok {
		return source.CurrentPlan(sessionID)
	}
	if t.plans == nil {
		return envtypes.Plan{}
	}

	return t.plans.CurrentPlan(sessionID)
}

func (t *Turn) hydratePlan(sessionID string, plan envtypes.Plan) {
	if t == nil {
		return
	}
	if store, ok := t.env.(planStateStore); ok {
		store.HydratePlan(sessionID, plan)
		return
	}
	if t.plans == nil {
		return
	}

	t.plans.HydratePlan(sessionID, plan)
}

func (t *Turn) environmentToolRegistryAndPolicy() (environmentToolRegistry, tools.Policy, bool) {
	registry, ok := t.environmentToolRegistry()
	if !ok || registry == nil {
		return nil, tools.Policy{}, false
	}

	policy, _ := t.environmentToolPolicy()
	return registry, policy, true
}

func (t *Turn) environmentToolRegistry() (environmentToolRegistry, bool) {
	if t == nil || t.env == nil {
		return nil, false
	}

	method := reflect.ValueOf(t.env).MethodByName("Tools")
	if !method.IsValid() || method.Type().NumIn() != 0 || method.Type().NumOut() != 1 {
		return nil, false
	}

	result := method.Call(nil)[0]
	if !result.IsValid() || result.IsNil() {
		return nil, false
	}

	registry, ok := result.Interface().(environmentToolRegistry)
	return registry, ok
}

func (t *Turn) environmentToolPolicy() (tools.Policy, bool) {
	if t == nil || t.env == nil {
		return tools.Policy{}, false
	}

	method := reflect.ValueOf(t.env).MethodByName("ToolPolicy")
	if !method.IsValid() || method.Type().NumIn() != 0 || method.Type().NumOut() != 1 {
		return tools.Policy{}, false
	}

	policy, ok := method.Call(nil)[0].Interface().(tools.Policy)
	return policy, ok
}

// Run executes the turn's logic, handling instructions, tool actions, tracing,
// safety enforcement, and returns the final assistant reply for this turn.
func (t *Turn) Run(ctx context.Context, msg string, opts agentcore.RespondOptions) (string, error) {
	var traceSession trace.Session
	budget := envbudget.New(0)
	streamingEnabled := false
	lastToolFailure := ""
	repeatedToolFailureCount := 0

	return agentcore.RunTurnLifecycle(ctx, msg, opts, agentcore.TurnLifecycle{
		Load: t.load,
		SetRequestInstruction: func(requestInstruct string) {
			if requestInstruct == "" {
				return
			}

			t.requestInstruction = instruct.Instruction{
				Name:  requestInstructionName,
				Value: requestInstruct,
			}
		},
		Open: func(context.Context, agentcore.RespondOptions) (agentcore.TurnCloser, error) {
			traceSession = t.newTraceSessionForRun()
			if opts.TraceEvents && opts.OnEvent != nil {
				traceSession = newFanoutTraceSession(traceSession, t.getStateSessionID(), func(event trace.Event) {
					opts.OnEvent(agentcore.Event{
						Kind:       EventKindTrace,
						TraceEvent: &event,
					})
				})
			}

			return traceSession, nil
		},
		Prepare: func(context.Context) error {
			t.recordLoadedContentSafety(traceSession)

			if t.planHydrated {
				plan := t.currentPlan(t.getStateSessionID())
				explanationValue := str.String(plan.Explanation)
				traceSession.Record(
					trace.EvtPlanHydrated,
					trace.PlanEventPayload{
						SessionID:    t.getStateSessionID(),
						Steps:        hydratedPlanStepsToTracePayload(plan.Steps),
						Summary:      hydratedPlanSummaryToTracePayload(summarizeHydratedPlan(plan)),
						ActiveStepID: getActiveHydratedPlanStepID(plan),
						Explanation:  explanationValue.Trim(),
						Source:       "history",
					},
				)
			}

			return nil
		},
		CheckInput: func(ctx context.Context, msg string) (agentcore.InputCheck, error) {
			if !t.cfg.InputSafetyEnabled() {
				return agentcore.InputCheck{}, nil
			}

			inputSafety := guardrails.CheckInputSafety(msg, "user")
			if !inputSafety.Blocked {
				return agentcore.InputCheck{}, nil
			}

			t.retainUnsafeEvidence(ctx, guardrails.UnsafeEvidence{
				Source:   "user",
				Action:   "blocked",
				Blocked:  true,
				Findings: guardrails.SafetyFindingLogFields(inputSafety.Findings),
				Original: msg,
				Safe:     inputSafety.RefusalMessage,
			})
			traceSession.Record(trace.EvtInputSafetyBlocked, getInputSafetyTracePayload(t.sessionID, msg, inputSafety))
			return agentcore.InputCheck{Blocked: true, Reply: inputSafety.RefusalMessage}, nil
		},
		AcceptUserMessage: func(_ context.Context, msg string) error {
			userMessage, err := morphmsg.NewMessage(morphmsg.RoleUser, msg)
			if err != nil {
				traceSession.Record(trace.EvtSessionFailed, trace.SessionFailedPayload{Error: err.Error()})
				return err
			}
			t.emittedMessages = append(t.emittedMessages, userMessage)

			if err := t.appendSessionMessages([]morphmsg.Message{userMessage}); err != nil {
				traceSession.Record(trace.EvtSessionFailed, trace.SessionFailedPayload{Error: err.Error()})
				return err
			}

			traceSession.Record(trace.EvtUserMessageAccepted, trace.UserMessageAcceptedPayload{Message: msg})
			return nil
		},
		LoadMemory: func(ctx context.Context, msg string) error {
			t.memoryInstruction = t.retrieveMemoryInstruction(ctx, msg, traceSession)
			budget = t.newIterationBudget()
			streamingEnabled = t.cfg.StreamEnabled()
			if opts.Stream != nil {
				streamingEnabled = *opts.Stream
			}
			return nil
		},
		ConsumeIteration: func() bool {
			return budget.Consume()
		},
		RunStep: func(ctx context.Context) (agentcore.LoopDecision, error) {
			// Check for context cancellation.
			if err := ctx.Err(); err != nil {
				traceSession.Record(trace.EvtSessionFailed, trace.SessionFailedPayload{Error: err.Error()})
				return agentcore.LoopDecision{}, err
			}

			// Query available tool definitions for this turn; may vary per session/tool policy.
			availableToolDefinitions, err := t.availableToolDefinitions()
			if err != nil {
				traceSession.Record(trace.EvtSessionFailed, trace.SessionFailedPayload{Error: err.Error()})
				return agentcore.LoopDecision{}, err
			}

			// Build model request and assemble all prompt-side context for completion.
			provider, api, model := t.getMainModelRequestIdentity()
			request := models.Request{
				Model:         model,
				Provider:      provider,
				API:           api,
				Instructions:  t.buildRequestInstructions(availableToolDefinitions),
				Messages:      t.Context(),
				Tools:         availableToolDefinitions,
				ContextLength: t.cfg.Models.Main.ContextLength,
				DebugRequests: t.cfg.Debug.Requests,
				Reasoning:     t.getReasoningOptions(),
			}

			// Refresh summary and possibly adjust session context after model request construction.
			t.maybeRefreshSummary(ctx, request, traceSession)

			// Rebuild prompt/context after summary possibly changed.
			request.Instructions = t.buildRequestInstructions(availableToolDefinitions)
			request.Messages = t.Context()

			// Trace summary application and preflight compaction/model events.
			t.summary.RecordSummaryApplied(traceSession)
			recordPreflightCompactionTrace(traceSession, t.cfg, request, t.compactionAnchor, t.canCompactPersistedHistory())
			recordModelRequest(traceSession, request)

			agentLog.Info().
				Str("provider", request.Provider).
				Str("api", request.API).
				Str("model", request.Model).
				Bool("stream", streamingEnabled).
				Int("context_messages", len(request.Messages)).
				Int("tools", len(request.Tools)).
				Bool("debug_requests", t.cfg.Debug.Requests).
				Msg("model request dispatch started")

			// --- Make model inference call (streaming or blocking) ---
			var (
				resp               *models.Response
				reasoningStartedAt time.Time
				reasoningEndedAt   time.Time
				reasoningSummary   strings.Builder
				assistantText      strings.Builder
			)

			if streamingEnabled {
				resp, err = t.modelClient.CompleteStream(ctx, request, func(delta models.StreamDelta) {
					if delta.Text == "" {
						return
					}

					if isReasoningStreamChannel(delta.Channel) {
						now := time.Now().UTC()
						if reasoningStartedAt.IsZero() {
							reasoningStartedAt = now
						}
						reasoningEndedAt = now
					}
					if delta.Channel == models.StreamChannelReasoningSummary {
						reasoningSummary.WriteString(delta.Text)
					}
					if delta.Channel == models.StreamChannelAssistant {
						assistantText.WriteString(delta.Text)
					}
					event := agentcore.Event{Kind: EventKindTextDelta, Channel: string(delta.Channel), Text: delta.Text}
					if opts.OnEvent != nil {
						opts.OnEvent(event)
					}
				})
			} else {
				// Blocking, non-stream model completion.
				resp, err = t.modelClient.Complete(ctx, request)
			}

			// Model request failed or provided no response.
			if err != nil {
				if persistErr := t.persistPartialAssistantResponse(ctx, assistantText.String()); persistErr != nil {
					err = errors.Join(err, fmt.Errorf("persist partial assistant response: %w", persistErr))
				}
				agentLog.Warn().
					Str("provider", request.Provider).
					Str("api", request.API).
					Str("model", request.Model).
					Bool("stream", streamingEnabled).
					Str("error_kind", getAgentModelErrorKind(err)).
					Msg("model request dispatch failed")
				traceSession.Record(trace.EvtSessionFailed, trace.SessionFailedPayload{Error: err.Error()})
				return agentcore.LoopDecision{}, err
			}

			if resp == nil {
				err = errors.New("model response is required")
				agentLog.Warn().
					Str("provider", request.Provider).
					Str("api", request.API).
					Str("model", request.Model).
					Bool("stream", streamingEnabled).
					Str("error_kind", "missing_response").
					Msg("model request dispatch failed")
				traceSession.Record(trace.EvtSessionFailed, trace.SessionFailedPayload{Error: err.Error()})
				return agentcore.LoopDecision{}, err
			}

			// Mark model inference timing for diagnostics/storage.
			t.recordModelReasoningCompleted(
				reasoningStartedAt,
				reasoningEndedAt,
				reasoningSummary.String(),
			)
			recordModelResponse(traceSession, resp)

			agentLog.Info().
				Str("provider", request.Provider).
				Str("api", request.API).
				Str("model", request.Model).
				Str("response_model", resp.Model).
				Bool("stream", streamingEnabled).
				Int("prompt_tokens", resp.PromptTokens).
				Int("completion_tokens", resp.CompletionTokens).
				Int("total_tokens", resp.TotalTokens).
				Int("tool_call_count", len(resp.ToolCalls)).
				Bool("requires_tool_calls", resp.RequiresToolCalls).
				Msg("model response received")

			// Record postflight token usage for usage/analytics.
			if err := t.recordPostflightUsage(traceSession, resp, len(request.Messages)); err != nil {
				return agentcore.LoopDecision{}, err
			}

			// -- Assistant textual reply path (no tool calls required) --
			if !resp.RequiresToolCalls {
				reply := t.applyAssistantOutputSafety(traceSession, resp.OutputText, streamingEnabled)

				assistantMessage, err := morphmsg.NewMessage(morphmsg.RoleAssistant, reply)
				if err != nil {
					traceSession.Record(trace.EvtSessionFailed, trace.SessionFailedPayload{Error: err.Error()})
					return agentcore.LoopDecision{}, err
				}

				// Append assistant message to emitted messages.
				t.emittedMessages = append(t.emittedMessages, assistantMessage)

				// Append assistant message to session history.
				if err := t.appendSessionMessages([]morphmsg.Message{assistantMessage}); err != nil {
					traceSession.Record(trace.EvtSessionFailed, trace.SessionFailedPayload{Error: err.Error()})
					return agentcore.LoopDecision{}, err
				}

				traceSession.Record(trace.EvtFinalAssistantResponse, trace.FinalAssistantResponsePayload{Message: reply})
				agentLog.Info().
					Str("session_id", t.sessionID).
					Msg("turn completed")

				return agentcore.LoopDecision{Done: true, Reply: reply}, nil
			}

			// -- Tool call required path --

			// If model asks for tool calls, ensure at least one is present.
			if len(resp.ToolCalls) == 0 {
				err = errors.New("model requested tool execution without tool calls")
				traceSession.Record(trace.EvtSessionFailed, trace.SessionFailedPayload{Error: err.Error()})
				return agentcore.LoopDecision{}, err
			}

			// Represent tool call(s) as assistant message in session.
			assistantMessage, err := assistantToolCallMessageFromResponse(resp)
			if err != nil {
				traceSession.Record(trace.EvtSessionFailed, trace.SessionFailedPayload{Error: err.Error()})
				return agentcore.LoopDecision{}, err
			}

			// Append assistant message to emitted messages.
			t.emittedMessages = append(t.emittedMessages, assistantMessage)
			if err := t.appendSessionMessages([]morphmsg.Message{assistantMessage}); err != nil {
				traceSession.Record(trace.EvtSessionFailed, trace.SessionFailedPayload{Error: err.Error()})
				return agentcore.LoopDecision{}, err
			}

			toolMessages, err := t.executeToolCalls(ctx, traceSession, resp.ToolCalls, availableToolDefinitions)
			if err != nil {
				traceSession.Record(trace.EvtSessionFailed, trace.SessionFailedPayload{Error: err.Error()})
				return agentcore.LoopDecision{}, err
			}

			t.emittedMessages = append(t.emittedMessages, toolMessages...)
			if err := t.appendSessionMessages(toolMessages); err != nil {
				traceSession.Record(trace.EvtSessionFailed, trace.SessionFailedPayload{Error: err.Error()})
				return agentcore.LoopDecision{}, err
			}
			if t.afterToolBatchPersisted != nil {
				delivered, err := t.afterToolBatchPersisted(ctx)
				if err != nil {
					traceSession.Record(trace.EvtSessionFailed, trace.SessionFailedPayload{Error: err.Error()})
					return agentcore.LoopDecision{}, err
				}
				if delivered {
					lastToolFailure = ""
					repeatedToolFailureCount = 0
					return agentcore.LoopDecision{}, nil
				}
			}

			failureSignature, failureMessage, failed := getEquivalentToolFailure(toolMessages)
			if !failed {
				lastToolFailure = ""
				repeatedToolFailureCount = 0
				return agentcore.LoopDecision{}, nil
			}
			if failureSignature == lastToolFailure {
				repeatedToolFailureCount++
			} else {
				lastToolFailure = failureSignature
				repeatedToolFailureCount = 1
			}
			if repeatedToolFailureCount < 2 {
				return agentcore.LoopDecision{}, nil
			}

			reply := "I stopped retrying because " + toolMessages[0].Name +
				" failed twice with the same error: " + failureMessage
			repeatedFailureMessage, err := morphmsg.NewMessage(morphmsg.RoleAssistant, reply)
			if err != nil {
				return agentcore.LoopDecision{}, err
			}
			t.emittedMessages = append(t.emittedMessages, repeatedFailureMessage)
			if err := t.appendSessionMessages([]morphmsg.Message{repeatedFailureMessage}); err != nil {
				return agentcore.LoopDecision{}, err
			}
			traceSession.Record(trace.EvtFinalAssistantResponse, trace.FinalAssistantResponsePayload{Message: reply})

			return agentcore.LoopDecision{Done: true, Reply: reply}, nil
		},
		OnExhausted: func(ctx context.Context) (string, error) {
			// If iteration budget is exhausted, fallback to summary-based result and finish.
			agentLog.Warn().
				Str("session_id", t.sessionID).
				Msg("iteration budget exhausted, falling back to summary")

			return t.summaryFallback(ctx, budget, traceSession)
		},
	})
}

func (t *Turn) setAfterToolBatchPersisted(callback func(context.Context) (bool, error)) {
	if t == nil {
		return
	}
	t.afterToolBatchPersisted = callback
}

func getEquivalentToolFailure(messages []morphmsg.Message) (string, string, bool) {
	if len(messages) != 1 {
		return "", "", false
	}

	var payload struct {
		Name  string          `json:"name"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal([]byte(messages[0].Content), &payload); err != nil || len(payload.Error) == 0 {
		return "", "", false
	}

	var toolError tools.Error
	if err := json.Unmarshal(payload.Error, &toolError); err == nil && toolError.Code != "" && toolError.Message != "" {
		return messages[0].Name + "\x00" + toolError.Code + "\x00" + toolError.Message, toolError.Message, true
	}

	var message string
	if err := json.Unmarshal(payload.Error, &message); err != nil || message == "" {
		return "", "", false
	}

	return messages[0].Name + "\x00" + message, message, true
}

// trimSessionHistoryToSummary trims session history up to summary end offset.
// Used after summary compaction and context refresh events.
func (t *Turn) trimSessionHistoryToSummary() {
	if t == nil || t.summary == nil || t.summary.Current == nil {
		return
	}

	targetOffset := max(t.summary.Current.SourceEndOffset, 0)
	if targetOffset <= t.sessionHistoryOffset {
		return
	}

	delta := targetOffset - t.sessionHistoryOffset
	if delta >= len(t.sessionHistory) {
		t.sessionHistory = nil
		t.sessionHistoryOffset = targetOffset
		return
	}

	t.sessionHistory = morphmsg.CloneMessages(t.sessionHistory[delta:])
	t.sessionHistoryOffset = targetOffset
}

// maybeRefreshSummary refreshes summary context if the message count has grown since last summary attempt.
// It may update summary state and trims session history if needed.
func (t *Turn) maybeRefreshSummary(ctx context.Context, request models.Request, traceSession trace.Session) {
	if t == nil || t.summaryService == nil {
		return
	}

	messageCount := len(request.Messages)
	if messageCount <= 0 || messageCount <= t.summaryRefreshAttemptedMessageCount {
		return
	}

	// Flush memory before compaction.
	t.maybeFlushMemoryBeforeCompaction(ctx, request, traceSession)

	// Maybe refresh summary.
	previousHistoryOffset := t.sessionHistoryOffset
	t.summaryRefreshAttemptedMessageCount = messageCount
	_ = t.summaryService.MaybeRefreshSummary(ctx, t.summary, summarizer.RefreshInput{
		Anchor:       t.compactionAnchor,
		Request:      request,
		SessionID:    t.sessionID,
		TraceSession: traceSession,
	})

	t.trimSessionHistoryToSummary()

	// A trim changes the request lineage. Re-anchor after the next provider response
	// and require message growth before another refresh attempt.
	if t.sessionHistoryOffset != previousHistoryOffset {
		t.compactionAnchor = compaction.Anchor{}
		t.summaryRefreshAttemptedMessageCount = len(t.Context())
	}
}

// canCompactPersistedHistory returns true if the session history is large enough to be compacted.
func (t *Turn) canCompactPersistedHistory() bool {
	if t == nil {
		return false
	}

	return len(t.sessionHistory) > t.cfg.CompactionRecentSessionTailEffective()
}

// appendSessionMessages persists new emitted messages to the session state.
func (t *Turn) appendSessionMessages(messages []morphmsg.Message) error {
	return t.sessionStore.AppendMessages(t.ctx, t.sessionID, messages)
}

func (t *Turn) persistPartialAssistantResponse(ctx context.Context, text string) error {
	textValue := str.String(text)
	if textValue.Trim() == "" {
		return nil
	}

	message, err := morphmsg.NewMessage(morphmsg.RoleAssistant, text)
	if err != nil {
		return err
	}
	if err := t.sessionStore.AppendMessages(
		context.WithoutCancel(ctx),
		t.sessionID,
		[]morphmsg.Message{message},
	); err != nil {
		return err
	}
	t.emittedMessages = append(t.emittedMessages, message)

	return nil
}

func isReasoningStreamChannel(channel models.StreamChannel) bool {
	return channel == models.StreamChannelReasoning ||
		channel == models.StreamChannelReasoningSummary
}

// applyAssistantOutputSafety performs output safety checks on assistant responses if enabled.
// For streaming, checks are deferred to client side.
func (t *Turn) applyAssistantOutputSafety(traceSession trace.Session, output string, streamingEnabled bool) string {
	if streamingEnabled {
		return output
	}
	if t == nil || t.cfg == nil || !t.cfg.OutputSafetyEnabled() {
		return output
	}

	result := guardrails.CheckOutputSafety(output, "assistant", t.getOutputRedactor())
	if result.Blocked || result.Redacted {
		t.retainUnsafeEvidence(t.ctx, guardrails.UnsafeEvidence{
			Source:   "assistant",
			Action:   getUnsafeEvidenceAction(result.Blocked),
			Blocked:  result.Blocked,
			Redacted: result.Redacted,
			Findings: guardrails.SafetyFindingLogFields(result.Findings),
			Original: output,
			Safe:     result.Content,
		})
		if traceSession != nil {
			traceSession.Record(trace.EvtOutputSafetyApplied, getOutputSafetyTracePayload(t.sessionID, output, result))
		}
	}

	return result.Content
}

// recordModelReasoningCompleted logs and saves model inference duration for a turn.
func (t *Turn) recordModelReasoningCompleted(
	startedAt time.Time,
	endedAt time.Time,
	summary string,
) {
	if t == nil || t.traceRecorder == nil || t.sessionID == "" || startedAt.IsZero() {
		return
	}

	if endedAt.IsZero() || endedAt.Before(startedAt) {
		endedAt = startedAt
	}

	duration := max(endedAt.Sub(startedAt).Round(time.Millisecond), time.Second)

	sanitizedSummary, _ := t.getOutputRedactor().Sanitize(summary).(string)
	if sanitizedSummary != summary {
		t.retainUnsafeEvidence(t.ctx, guardrails.UnsafeEvidence{
			Source:   "reasoning.summary",
			Action:   "redacted",
			Redacted: true,
			Original: summary,
			Safe:     sanitizedSummary,
		})
	}
	event := agentsession.TraceEvent{
		SessionID: t.sessionID,
		Type:      trace.EvtModelReasoningCompleted,
		Timestamp: endedAt,
		Payload: trace.ModelReasoningCompletedPayload{
			DurationMS: duration.Milliseconds(),
			Summary:    sanitizedSummary,
		},
	}
	if _, err := t.traceRecorder.AppendTraceEvent(t.ctx, event); err != nil {
		if !errors.Is(err, storage.ErrTraceStoreUnsupported) {
			agentLog.Warn().
				Err(err).
				Str("session_id", t.sessionID).
				Msg("failed to persist reasoning completion trace")
		}
		return
	}

	agentLog.Debug().
		Str("session_id", t.sessionID).
		Int64("duration_ms", duration.Milliseconds()).
		Msg("model reasoning completed")
}

// getOutputRedactor returns a redactor instance configured for the session and output PII settings.
func (t *Turn) getOutputRedactor() guardrails.Redactor {
	if t == nil || t.cfg == nil {
		return guardrails.NewRedactorWithOptions(guardrails.RedactorOptions{DisablePII: true})
	}

	return guardrails.NewRedactorWithOptions(guardrails.RedactorOptions{
		DisablePII: !t.cfg.OutputPIIRedactionEnabled(),
	})
}

// recordLoadedContentSafety emits trace events for any content safety violations loaded for this session.
func (t *Turn) recordLoadedContentSafety(traceSession trace.Session) {
	if t == nil || traceSession == nil {
		return
	}

	source, _ := t.env.(safetyTraceEventSource)
	if source == nil {
		source = t.safetyEvents
	}
	if source == nil {
		return
	}

	for _, event := range source.SafetyTraceEvents() {
		traceSession.Record(trace.EvtLoadedContentSafetyBlocked, safetyEventPayloadFromOptions(event))
	}
}

// getInputSafetyTracePayload builds a standardized payload for a blocked input safety event.
func getInputSafetyTracePayload(sessionID string, content string, result guardrails.InputSafetyResult) trace.SafetyEventPayload {
	return safetyEventPayloadFromOptions(guardrails.SafetyTracePayloadOptions{
		SessionID:     sessionID,
		Source:        "user",
		Action:        "blocked",
		ContentLength: len([]rune(content)),
		Blocked:       result.Blocked,
		Findings:      result.Findings,
		Refusal:       result.RefusalMessage,
	})
}

// getOutputSafetyTracePayload builds a standardized payload for assistant output safety check results.
func getOutputSafetyTracePayload(sessionID string, content string, result guardrails.OutputSafetyResult) trace.SafetyEventPayload {
	action := "redacted"
	if result.Blocked {
		action = "blocked"
	}
	return safetyEventPayloadFromOptions(guardrails.SafetyTracePayloadOptions{
		SessionID:     sessionID,
		Source:        "assistant",
		Action:        action,
		ContentLength: len([]rune(content)),
		Blocked:       result.Blocked,
		Redacted:      result.Redacted,
		Findings:      result.Findings,
		Refusal:       result.RefusalMessage,
	})
}

// safetyEventPayloadFromOptions normalizes guardrail trace payloads for logging/tracing.
func safetyEventPayloadFromOptions(opts guardrails.SafetyTracePayloadOptions) trace.SafetyEventPayload {
	sessionIDValue := str.String(opts.SessionID)
	sourceValue := str.String(opts.Source)
	actionValue := str.String(opts.Action)
	refusalValue := str.String(opts.Refusal)
	return trace.SafetyEventPayload{
		SessionID:     sessionIDValue.Trim(),
		Source:        sourceValue.Trim(),
		Action:        actionValue.Trim(),
		ContentLength: opts.ContentLength,
		Blocked:       opts.Blocked,
		Redacted:      opts.Redacted,
		Refusal:       refusalValue.Trim(),
		Findings:      guardrails.SafetyFindingLogFields(opts.Findings),
	}
}

// getPlanToolInputState returns plan tool state representation for tracing if name/type matches.
func getPlanToolInputState(name string, input string) *trace.PlanToolState {
	nameValue := str.String(name)
	if nameValue.Normalized() != "plan_tool" {
		return nil
	}
	return trace.PlanToolInputState(input)
}

// getPlanToolOutputState returns plan tool output state for tracing where matched.
func getPlanToolOutputState(name string, output string) *trace.PlanToolState {
	nameValue2 := str.String(name)
	if nameValue2.Normalized() != "plan_tool" {
		return nil
	}
	return trace.PlanToolOutputState(output)
}

// getProcessToolInputState returns process tool input state for tracing where matched.
func getProcessToolInputState(name string, input string) *trace.ProcessToolState {
	nameValue3 := str.String(name)
	if nameValue3.Normalized() != "process" {
		return nil
	}
	return trace.ProcessToolInputState(input)
}

// getProcessToolOutputState returns process tool output state for tracing where matched.
func getProcessToolOutputState(name string, output string) *trace.ProcessToolState {
	nameValue4 := str.String(name)
	if nameValue4.Normalized() != "process" {
		return nil
	}
	return trace.ProcessToolOutputState(output)
}

// getStateSessionID reports the canonical session ID for trace/state operations for this turn.
func (t *Turn) getStateSessionID() string {
	if t == nil {
		return storage.DefaultSessionID
	}

	effectiveIDValue := str.String(t.runCtx.Session.EffectiveID)
	publicIDValue := str.String(t.runCtx.Session.PublicID)
	if effectiveIDValue.Trim() != "" || publicIDValue.
		Trim() != "" {
		return t.runCtx.StateSessionID()
	}
	sessionIDValue2 := str.String(t.sessionID)
	if value := sessionIDValue2.Trim(); value != "" {
		return value
	}
	return t.runCtx.StateSessionID()
}

// getToolContext produces a context carrying the relevant session/run metadata for tools.
func (t *Turn) getToolContext(ctx context.Context) context.Context {
	if t == nil {
		return tools.WithSessionID(ctx, "")
	}

	publicIDValue2 := str.String(t.runCtx.Session.PublicID)
	if publicIDValue2.Trim() != "" {
		return tools.WithRunContext(ctx, t.runCtx)
	}
	return tools.WithSessionID(ctx, t.sessionID)
}

// availableToolDefinitions resolves tool definitions available for this turn, using environment/tool policy.
func (t *Turn) availableToolDefinitions() ([]models.ToolDefinition, error) {
	if t == nil {
		return nil, nil
	}

	if registry, policy, ok := t.environmentToolRegistryAndPolicy(); ok {
		definitions, err := registry.Resolve(policy)
		if err != nil {
			return nil, err
		}

		toolsList := make([]models.ToolDefinition, 0, len(definitions))
		for _, definition := range definitions {
			toolsList = append(toolsList, modelToolDefinitionFromToolDefinition(definition))
		}

		return toolsList, nil
	}

	if t.toolRegistry != nil {
		definitions, err := t.toolRegistry.Resolve(t.toolPolicy)
		if err != nil {
			return nil, err
		}

		return agenttool.DefinitionsToModel(definitions), nil
	}

	return nil, nil
}

func (t *Turn) executeToolCalls(
	ctx context.Context,
	traceSession trace.Session,
	toolCalls []models.ToolCall,
	definitions []models.ToolDefinition,
) ([]morphmsg.Message, error) {
	return agentcore.ExecuteToolCalls(ctx, agentcore.ToolCallExecutionOptions{
		ToolCalls:   toolCalls,
		Definitions: definitions,
		Execute: func(ctx context.Context, toolCall models.ToolCall) (morphmsg.Message, error) {
			return t.executeToolCall(ctx, traceSession, toolCall)
		},
	})
}

func (t *Turn) executeToolCall(
	ctx context.Context,
	traceSession trace.Session,
	toolCall models.ToolCall,
) (morphmsg.Message, error) {
	if err := ctx.Err(); err != nil {
		return morphmsg.Message{}, err
	}

	agentLog.Info().
		Str("tool", toolCall.Name).
		Str("tool_call_id", toolCall.ID).
		Msg("tool invocation started")

	traceSession.Record(trace.EvtToolInvocationStarted, trace.ToolInvocationStartedPayload{
		ID:           toolCall.ID,
		Name:         toolCall.Name,
		Input:        toolCall.Input,
		PlanState:    getPlanToolInputState(toolCall.Name, toolCall.Input),
		ProcessState: getProcessToolInputState(toolCall.Name, toolCall.Input),
	})

	toolCtx := tools.WithTraceRecorder(t.getToolContext(ctx), traceSession)
	toolCtx = guardrails.WithUnsafeEvidenceRecorder(toolCtx, t.getUnsafeEvidenceRecorder())
	toolMessage := t.invokeTool(toolCtx, toolCall)
	semanticProjectionStatus := "skipped"
	if toolMessage.SemanticContent != "" {
		semanticProjectionStatus = "projected"
	}

	traceSession.Record(trace.EvtToolInvocationCompleted, trace.ToolInvocationCompletedPayload{
		ToolCallID:               toolMessage.ToolCallID,
		Name:                     toolMessage.Name,
		Content:                  toolMessage.Content,
		Failed:                   trace.ToolInvocationFailed(toolMessage.Content),
		SemanticProjectionStatus: semanticProjectionStatus,
		SemanticContentBytes:     len(toolMessage.SemanticContent),
		PlanState:                getPlanToolOutputState(toolMessage.Name, toolMessage.Content),
		ProcessState:             getProcessToolOutputState(toolMessage.Name, toolMessage.Content),
	})

	agentLog.Info().
		Str("tool", toolCall.Name).
		Str("tool_call_id", toolCall.ID).
		Int("output_chars", len([]rune(toolMessage.Content))).
		Int("output_bytes", len(toolMessage.Content)).
		Str("semantic_projection_status", semanticProjectionStatus).
		Int("semantic_content_bytes", len(toolMessage.SemanticContent)).
		Msg("tool invocation completed")

	return normalizeTurnMessage(toolMessage)
}

// invokeTool executes a tool call, optionally using turn's tool invocation handler.
func (t *Turn) invokeTool(ctx context.Context, toolCall models.ToolCall) morphmsg.Message {
	if t == nil {
		return morphmsg.Message{
			Role:       morphmsg.RoleTool,
			Name:       toolCall.Name,
			ToolCallID: toolCall.ID,
			Content:    `{"error":"tool invocation is required"}`,
		}
	}

	if t.invokeToolFn != nil {
		if message, ok := t.invokeToolWithLegacyHook(ctx, toolCall); ok {
			return message
		}
	}

	if t.toolRegistry != nil {
		return t.toolRegistry.Invoke(ctx, agenttool.CallFromModel(toolCall))
	}

	if registry, _, ok := t.environmentToolRegistryAndPolicy(); ok {
		return t.invokeToolWithLegacyRuntime(ctx, registry, toolCall)
	}

	return morphmsg.Message{
		Role:       morphmsg.RoleTool,
		Name:       toolCall.Name,
		ToolCallID: toolCall.ID,
		Content:    `{"error":"tool invocation is required"}`,
	}
}

func (t *Turn) invokeToolWithLegacyRuntime(
	ctx context.Context,
	registry environmentToolRegistry,
	toolCall models.ToolCall,
) morphmsg.Message {
	result := map[string]any{"name": toolCall.Name}
	if registry == nil {
		result["error"] = "tool registry is required"
		return toolResultMessage(toolCall, result, "")
	}

	toolResult, err := registry.Invoke(ctx, tools.Call{
		Name:   toolCall.Name,
		Input:  toolCall.Input,
		Source: "model",
	})
	if err != nil {
		result["error"] = err.Error()
	}
	errorValue := str.String(toolResult.Error)
	if errorValue.Trim() != "" {
		errorValue2 := str.String(toolResult.Error)
		result["error"] = normalizeToolError(errorValue2.Trim())
	}
	outputValue := str.String(toolResult.Output)
	if outputValue.Trim() != "" {
		result["output"] = sanitizeToolOutputForModel(ctx, toolCall.Name, toolResult.Output, t.cfg)
	}

	semanticContent := sanitizeToolOutputForModel(ctx, toolCall.Name, toolResult.SemanticContent, t.cfg)
	return toolResultMessage(toolCall, result, semanticContent)
}

func (t *Turn) invokeToolWithLegacyHook(ctx context.Context, toolCall models.ToolCall) (morphmsg.Message, bool) {
	switch invoke := t.invokeToolFn.(type) {
	case func(context.Context, models.ToolCall) morphmsg.Message:
		return invoke(ctx, toolCall), true
	}

	value := reflect.ValueOf(t.invokeToolFn)
	if !value.IsValid() || value.Kind() != reflect.Func || value.Type().NumIn() != 3 || value.Type().NumOut() != 1 {
		return morphmsg.Message{}, false
	}
	if !value.Type().Out(0).AssignableTo(reflect.TypeOf(morphmsg.Message{})) {
		return morphmsg.Message{}, false
	}

	args := []reflect.Value{
		reflect.ValueOf(ctx),
		reflect.Zero(value.Type().In(1)),
		reflect.ValueOf(toolCall),
	}
	if t.env != nil {
		envValue := reflect.ValueOf(t.env)
		if envValue.Type().AssignableTo(value.Type().In(1)) {
			args[1] = envValue
		}
	}

	return value.Call(args)[0].Interface().(morphmsg.Message), true
}

// summaryFallback runs the fallback summary request and returns the assistant reply
// when iteration budget was exhausted and a summary response is needed for completion.
func (t *Turn) summaryFallback(ctx context.Context, budget envbudget.IterationBudget, traceSession trace.Session) (string, error) {
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		traceSession.Record(trace.EvtSessionFailed, trace.SessionFailedPayload{Error: err.Error()})
		return "", err
	}

	provider, api, model := t.getMainModelRequestIdentity()
	request := models.Request{
		Model:         model,
		Provider:      provider,
		API:           api,
		Instructions:  t.buildRequestInstructions(nil, instruct.BuildSummary(budget.Remaining())),
		Messages:      t.Context(),
		Tools:         nil,
		ContextLength: t.cfg.Models.Main.ContextLength,
		DebugRequests: t.cfg.Debug.Requests,
		Reasoning:     t.getReasoningOptions(),
	}

	traceSession.Record(
		trace.EvtSummaryFallbackStarted,
		trace.SummaryFallbackStartedPayload{RemainingIterations: budget.Remaining()},
	)
	t.summary.RecordSummaryApplied(traceSession)
	recordPreflightCompactionTrace(traceSession, t.cfg, request, t.compactionAnchor, t.canCompactPersistedHistory())
	recordModelRequest(traceSession, request)

	agentLog.Info().
		Str("provider", request.Provider).
		Str("api", request.API).
		Str("model", request.Model).
		Int("context_messages", len(request.Messages)).
		Bool("debug_requests", t.cfg.Debug.Requests).
		Msg("summary fallback model request started")

	resp, err := t.modelClient.Complete(ctx, request)
	if err != nil {
		agentLog.Error().
			Err(err).
			Str("session_id", t.sessionID).
			Str("provider", request.Provider).
			Str("api", request.API).
			Str("model", request.Model).
			Str("error_kind", getAgentModelErrorKind(err)).
			Msg("summary fallback model request failed")
		wrapped := fmt.Errorf("iteration limit reached and summary failed: %w", err)
		traceSession.Record(trace.EvtSessionFailed, trace.SessionFailedPayload{Error: wrapped.Error()})
		return "", wrapped
	}

	if resp == nil {
		err = errors.New("model response is required")
		agentLog.Error().
			Str("session_id", t.sessionID).
			Str("provider", request.Provider).
			Str("api", request.API).
			Str("model", request.Model).
			Str("error_kind", "missing_response").
			Msg("summary fallback model request failed")
		traceSession.Record(trace.EvtSessionFailed, trace.SessionFailedPayload{Error: err.Error()})
		return "", err
	}

	recordModelResponse(traceSession, resp)
	agentLog.Info().
		Str("provider", request.Provider).
		Str("api", request.API).
		Str("model", request.Model).
		Str("response_model", resp.Model).
		Int("prompt_tokens", resp.PromptTokens).
		Int("completion_tokens", resp.CompletionTokens).
		Int("total_tokens", resp.TotalTokens).
		Msg("summary fallback model response received")

	if err := t.recordPostflightUsage(traceSession, resp, len(request.Messages)); err != nil {
		return "", err
	}

	if resp.RequiresToolCalls {
		err = fmt.Errorf("iteration limit reached and summary requested more tools")
		traceSession.Record(trace.EvtSessionFailed, trace.SessionFailedPayload{Error: err.Error()})
		return "", err
	}

	reply := t.applyAssistantOutputSafety(traceSession, resp.OutputText, false)

	assistantMessage, err := morphmsg.NewMessage(morphmsg.RoleAssistant, reply)
	if err != nil {
		traceSession.Record(trace.EvtSessionFailed, trace.SessionFailedPayload{Error: err.Error()})
		return "", err
	}

	t.emittedMessages = append(t.emittedMessages, assistantMessage)
	if err := t.appendSessionMessages([]morphmsg.Message{assistantMessage}); err != nil {
		traceSession.Record(trace.EvtSessionFailed, trace.SessionFailedPayload{Error: err.Error()})
		return "", err
	}

	traceSession.Record(trace.EvtFinalAssistantResponse, trace.FinalAssistantResponsePayload{Message: reply})
	return reply, nil
}

func (t *Turn) setReasoningSnapshot(snapshot agentsession.ReasoningSnapshot) {
	if t == nil {
		return
	}
	t.reasoningSnapshot = snapshot
}

func (t *Turn) getReasoningOptions() *models.ReasoningOptions {
	if t == nil || t.reasoningSnapshot.Effort == "" && !t.reasoningSnapshot.Summary {
		return nil
	}
	return &models.ReasoningOptions{
		Effort:  string(t.reasoningSnapshot.Effort),
		Summary: t.reasoningSnapshot.Summary,
	}
}

func (t *Turn) getMainModelRequestIdentity() (string, string, string) {
	if t != nil && t.reasoningSnapshot.Model != "" {
		return t.reasoningSnapshot.Provider, t.reasoningSnapshot.API, t.reasoningSnapshot.Model
	}
	if t == nil || t.cfg == nil {
		return "", "", ""
	}
	return t.cfg.Models.Main.Provider, t.cfg.MainModelAPIEffective(), t.cfg.Models.Main.Name
}

// getAgentModelErrorKind standardizes error kind reporting for model errors.
func getAgentModelErrorKind(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "context_canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}

	value := strings.ToLower(err.Error())
	switch {
	case strings.Contains(value, "response is required"):
		return "missing_response"
	case strings.Contains(value, "timeout"):
		return "timeout"
	default:
		return "operation_failed"
	}
}

// buildRequestInstructions assembles the system prompt/instructions for the model in recommended order.
func (t *Turn) buildRequestInstructions(
	activeToolDefinitions []models.ToolDefinition,
	extra ...instruct.Instructions,
) string {
	if t == nil {
		return ""
	}

	instructions := t.instructions

	// Plan context: prepend plan instructions and policy if present.
	if planInstructions := t.renderPlanInstructions(); planInstructions != "" {
		if policy, ok := instructions.GetByName(instruct.PlanningPolicyInstructionName); ok {
			instructions = instruct.New(policy.Value).
				Append(instruct.Instruction{Value: planInstructions}).
				Append(instructions.WithoutName(instruct.PlanningPolicyInstructionName)...)
		} else {
			instructions = instruct.New(planInstructions).Append(instructions...)
		}
	}

	// Add any summary instructions from summary state.
	if t.summary != nil {
		if summaryInstructions, ok := t.summary.RenderSummaryInstructions(); ok {
			instructions = instructions.Append(instruct.Instruction{Value: summaryInstructions})
		}
	}

	// Add memory retrieved for this turn.
	instructions = instructions.Append(t.memoryInstruction)

	// Add environment context (tools, capabilities).
	environmentContext := t.buildEnvironmentContextInstruction(activeToolDefinitions)
	instructions = instructions.Append(environmentContext)

	// Add single-turn request instruction if provided.
	instructions = instructions.Append(t.requestInstruction)

	// Add any final extra instruction blocks (e.g. fallback summary).
	for _, block := range extra {
		instructions = instructions.Append(block...)
	}

	return instructions.String()
}

// newRootRunContext creates a normalized run context from the session ID and active profile (if any).
func newRootRunContext(sessionID string) (runcontext.Context, error) {
	runCtx, err := runcontext.NewParent(sessionID)
	if err != nil {
		return runcontext.Context{}, err
	}
	active := profile.Active()
	activeName := str.String(active.Name)
	if activeName.Trim() != "" {
		runCtx.ProfileName = active.Name
	}

	return runCtx.Normalize()
}

// Context rebuilds prompt-visible message context for a model turn, combining summary/prefix/history/emitted.
func (t *Turn) Context() []morphmsg.Message {
	builder := t.contextBuilder
	if builder == nil {
		builder = ctxbuilder.New()
	}

	recall := t.summary.Recall(t.sessionHistory)

	return builder.Build(ctxbuilder.Input{
		PrefixMessages:  recall.PrefixMessages,
		SessionHistory:  recall.SessionHistory,
		EmittedMessages: t.emittedMessages,
	})
}

func (t *Turn) getCurrentContextEstimate() (compaction.Estimate, error) {
	availableToolDefinitions, err := t.availableToolDefinitions()
	if err != nil {
		return compaction.Estimate{}, err
	}

	request := models.Request{
		Instructions: t.buildRequestInstructions(availableToolDefinitions),
		Messages:     t.Context(),
		Tools:        availableToolDefinitions,
	}

	return getCompactionEvaluator(t.cfg).Evaluate(request, compaction.Anchor{}), nil
}

// recordPostflightUsage persists post-model token usage for analytics and session tracking.
func (t *Turn) recordPostflightUsage(traceSession trace.Session, resp *models.Response, messageCount int) error {
	if t == nil || resp == nil || resp.PromptTokens <= 0 {
		return nil
	}

	if err := t.sessionStore.UpdateLastPromptTokens(t.ctx, t.sessionID, resp.PromptTokens); err != nil {
		traceSession.Record(trace.EvtSessionFailed, trace.SessionFailedPayload{Error: err.Error()})
		return err
	}
	t.compactionAnchor = compaction.Anchor{PromptTokens: resp.PromptTokens, MessageCount: messageCount}

	traceSession.Record(trace.EvtContextPostflightUsage, trace.ContextEventPayload{
		Source:             compaction.ActualSource,
		PromptTokens:       resp.PromptTokens,
		AnchorPromptTokens: resp.PromptTokens,
		AnchorMessageCount: messageCount,
		CompletionTokens:   resp.CompletionTokens,
		TotalTokens:        resp.TotalTokens,
	})

	return nil
}

// Messages returns copies of all assistant and tool messages emitted during this turn.
// Used in testing and downstream consumers.
func (t *Turn) Messages() []morphmsg.Message {
	if len(t.emittedMessages) == 0 {
		return nil
	}

	messages := make([]morphmsg.Message, len(t.emittedMessages))
	copy(messages, t.emittedMessages)

	return messages
}

// normalizeTurnMessage calls morphmsg.NormalizeMessage to check/standardize a turn message for correctness.
func normalizeTurnMessage(message morphmsg.Message) (morphmsg.Message, error) {
	return morphmsg.NormalizeMessage(message)
}
