package mocks

import (
	"context"
	"errors"

	"github.com/wandxy/morph/internal/agent/runcontext"
	"github.com/wandxy/morph/internal/constants"
	"github.com/wandxy/morph/internal/environment"
	envbudget "github.com/wandxy/morph/internal/environment/budget"
	envtypes "github.com/wandxy/morph/internal/environment/types"
	"github.com/wandxy/morph/internal/guardrails"
	instruct "github.com/wandxy/morph/internal/instructions"
	"github.com/wandxy/morph/internal/memory"
	models "github.com/wandxy/morph/internal/model"
	"github.com/wandxy/morph/internal/permissions"
	statemanager "github.com/wandxy/morph/internal/state/manager"
	"github.com/wandxy/morph/internal/tools"
	"github.com/wandxy/morph/internal/trace"
)

// ModelClientStub is a test stub for model client.
type ModelClientStub struct {
	Requests  []models.Request
	Responses []*models.Response
	Errors    []error
	Err       error
	CallCount int
	Deltas    [][]models.StreamDelta
}

func (s *ModelClientStub) Complete(_ context.Context, req models.Request) (*models.Response, error) {
	s.Requests = append(s.Requests, req)
	if s.CallCount < len(s.Errors) && s.Errors[s.CallCount] != nil {
		s.CallCount++
		return nil, s.Errors[s.CallCount-1]
	}

	if s.Err != nil {
		return nil, s.Err
	}

	if s.CallCount >= len(s.Responses) {
		return nil, errors.New("missing stubbed response")
	}
	response := s.Responses[s.CallCount]
	s.CallCount++
	return response, nil
}

func (s *ModelClientStub) CompleteStream(_ context.Context, req models.Request, onTextDelta func(models.StreamDelta)) (*models.Response, error) {
	s.Requests = append(s.Requests, req)
	if s.CallCount < len(s.Errors) && s.Errors[s.CallCount] != nil {
		s.CallCount++
		return nil, s.Errors[s.CallCount-1]
	}

	if s.Err != nil {
		return nil, s.Err
	}

	if onTextDelta != nil && s.CallCount < len(s.Deltas) {
		for _, delta := range s.Deltas[s.CallCount] {
			onTextDelta(delta)
		}
	}

	if s.CallCount >= len(s.Responses) {
		return nil, errors.New("missing stubbed response")
	}
	response := s.Responses[s.CallCount]
	s.CallCount++
	return response, nil
}

var _ environment.Environment = (*EnvironmentStub)(nil)

// EnvironmentStub is a test stub for environment.
type EnvironmentStub struct {
	PrepareErr       error
	InstructionsList instruct.Instructions
	ToolRegistry     environment.ToolRegistry
	Policy           tools.Policy
	IterationBudget  envbudget.IterationBudget
	TraceSession     trace.Session
	TraceRunContexts []runcontext.Context
	TraceSessionIDs  []string
	SafetyEvents     []guardrails.SafetyTracePayloadOptions
	Memory           memory.Provider
	Automation       envtypes.AutomationService
	AutomationSets   int
	Browser          envtypes.BrowserService
	BrowserSets      int
	CommandKeys      [][]byte
	Plan             envtypes.Plan
	PlanSessionIDs   []string
}

func (s *EnvironmentStub) Prepare() error {
	return s.PrepareErr
}

func (s *EnvironmentStub) Instructions() instruct.Instructions {
	return s.InstructionsList
}

func (s *EnvironmentStub) SafetyTraceEvents() []guardrails.SafetyTracePayloadOptions {
	return s.SafetyEvents
}

func (s *EnvironmentStub) Tools() environment.ToolRegistry {
	return s.ToolRegistry
}

func (s *EnvironmentStub) ToolPolicy() tools.Policy {
	return s.Policy
}

func (s *EnvironmentStub) SetCommandIdentityKey(key []byte) {
	s.CommandKeys = append(s.CommandKeys, append([]byte(nil), key...))
}

func (s *EnvironmentStub) NewIterationBudget() envbudget.IterationBudget {
	if s.IterationBudget.Remaining() <= 0 {
		return envbudget.New(constants.DefaultMaxIterations)
	}

	return s.IterationBudget
}

func (s *EnvironmentStub) NewTraceSession(sessionID string) trace.Session {
	s.TraceSessionIDs = append(s.TraceSessionIDs, sessionID)
	if s.TraceSession == nil {
		return trace.NoopSession()
	}

	return s.TraceSession
}

func (s *EnvironmentStub) NewTraceSessionForRun(runCtx runcontext.Context) trace.Session {
	s.TraceRunContexts = append(s.TraceRunContexts, runCtx)
	if s.TraceSession == nil {
		return trace.NoopSession()
	}

	return s.TraceSession
}

func (s *EnvironmentStub) MemoryProvider() memory.Provider {
	return s.Memory
}

func (s *EnvironmentStub) SetModelClient(models.Client) {}

func (s *EnvironmentStub) SetAutomationService(service envtypes.AutomationService) {
	s.Automation = service
	s.AutomationSets++
}

func (s *EnvironmentStub) SetBrowserService(service envtypes.BrowserService) {
	s.Browser = service
	s.BrowserSets++
}

func (s *EnvironmentStub) CurrentPlan(string) envtypes.Plan {
	return s.Plan
}

func (s *EnvironmentStub) HydratePlan(sessionID string, plan envtypes.Plan) {
	s.PlanSessionIDs = append(s.PlanSessionIDs, sessionID)
	s.Plan = plan
}

func (s *EnvironmentStub) SetStateManager(*statemanager.Manager) {}

func (s *EnvironmentStub) SetApprovalService(permissions.Approver) {}

// ToolRegistryStub is a test stub for tool registry.
type ToolRegistryStub struct {
	Definitions    tools.Definitions
	Groups         []tools.Group
	LastToolPolicy tools.Policy
	InvokeContext  context.Context
	InvokeCall     tools.Call
	Result         tools.Result
	Err            error
	ResolveErr     error
}

func (s *ToolRegistryStub) List() tools.Definitions {
	return s.Definitions
}

func (s *ToolRegistryStub) GetGroup(name string) (tools.Group, bool) {
	for _, group := range s.Groups {
		if group.Name == name {
			return group, true
		}
	}

	return tools.Group{}, false
}

func (s *ToolRegistryStub) ListGroups() []tools.Group {
	return s.Groups
}

func (s *ToolRegistryStub) Resolve(opts tools.Policy) (tools.Definitions, error) {
	s.LastToolPolicy = opts
	return s.Definitions, s.ResolveErr
}

func (s *ToolRegistryStub) Invoke(ctx context.Context, call tools.Call) (tools.Result, error) {
	s.InvokeContext = ctx
	s.InvokeCall = call
	return s.Result, s.Err
}

// TraceSessionStub is a test stub for trace session.
type TraceSessionStub struct {
	SessionID string
	Events    []trace.Event
	Closed    bool
}

func (s *TraceSessionStub) ID() string {
	return s.SessionID
}

func (s *TraceSessionStub) Record(eventType string, payload any) {
	s.Events = append(s.Events, trace.Event{Type: eventType, Payload: payload})
}

func (s *TraceSessionStub) Close() {
	s.Closed = true
}
