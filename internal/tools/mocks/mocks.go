package mocks

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	processenv "github.com/wandxy/morph/internal/environment/process"
	envsessionmessages "github.com/wandxy/morph/internal/environment/sessionmessages"
	envtypes "github.com/wandxy/morph/internal/environment/types"
	"github.com/wandxy/morph/internal/guardrails"
	"github.com/wandxy/morph/internal/memory"
	"github.com/wandxy/morph/internal/memory/episodic"
	"github.com/wandxy/morph/internal/permissions"
	"github.com/wandxy/morph/internal/tools"
)

// Runtime exposes environment-backed services to tools.
type Runtime struct {
	FilePolicyValue              guardrails.FilesystemPolicy
	CommandPolicyValue           guardrails.CommandPolicy
	CommandShellValue            string
	CommandIdentityKeyValue      []byte
	StartProcessFunc             func(context.Context, string, processenv.StartRequest) (processenv.Info, error)
	GetProcessFunc               func(string, string) (processenv.Info, error)
	ReadProcessFunc              func(string, processenv.ReadRequest) (processenv.Output, error)
	StopProcessFunc              func(context.Context, string, string) (processenv.Info, error)
	ListProcessesFunc            func(string) []processenv.Info
	SearchSessionFunc            func(context.Context, envtypes.SessionSearchRequest) ([]envtypes.SessionSearchResult, error)
	GetSessionMessagesFunc       func(context.Context, envsessionmessages.SessionMessagesRequest) (envsessionmessages.SessionMessagesResponse, error)
	SupportsMemorySearchFunc     func(context.Context) (bool, error)
	SearchMemoryFunc             func(context.Context, memory.SearchQuery) (memory.SearchResult, error)
	SupportsMemoryExtractionFunc func(context.Context) (bool, error)
	ExtractEpisodesFunc          func(context.Context, episodic.Request) (episodic.Result, error)
	SupportsMemoryWriteFunc      func(context.Context) (bool, error)
	RecordSemanticMemoryFunc     func(context.Context, memory.SemanticRecord) (memory.MemoryItem, error)
	RecordProceduralMemoryFunc   func(context.Context, memory.ProceduralRecord) (memory.MemoryItem, error)
	PromoteMemoryCandidateFunc   func(context.Context, memory.PromotionRequest) (memory.LifecycleResult, error)
	UpdateMemoryFunc             func(context.Context, memory.UpdateRequest) (memory.UpdateResult, error)
	DeleteMemoryFunc             func(context.Context, memory.DeleteRequest) error
	AutomationServiceValue       envtypes.AutomationService
	AutomationServiceOK          bool
	AutomationServiceErr         error
	BrowserServiceValue          envtypes.BrowserService
	BrowserServiceOK             bool
	BrowserServiceErr            error
}

func (r *Runtime) FilePolicy() guardrails.FilesystemPolicy { return r.FilePolicyValue }
func (r *Runtime) CommandPolicy() guardrails.CommandPolicy { return r.CommandPolicyValue }
func (r *Runtime) CommandShell() string                    { return r.CommandShellValue }
func (r *Runtime) CommandIdentityKey() []byte {
	return append([]byte(nil), r.CommandIdentityKeyValue...)
}
func (r *Runtime) StartProcess(ctx context.Context, sessionID string, req processenv.StartRequest) (processenv.Info, error) {
	if r != nil && r.StartProcessFunc != nil {
		return r.StartProcessFunc(ctx, sessionID, req)
	}

	return processenv.Info{}, nil
}
func (r *Runtime) GetProcess(sessionID string, processID string) (processenv.Info, error) {
	if r != nil && r.GetProcessFunc != nil {
		return r.GetProcessFunc(sessionID, processID)
	}

	return processenv.Info{}, nil
}
func (r *Runtime) ReadProcess(sessionID string, req processenv.ReadRequest) (processenv.Output, error) {
	if r != nil && r.ReadProcessFunc != nil {
		return r.ReadProcessFunc(sessionID, req)
	}

	return processenv.Output{}, nil
}
func (r *Runtime) StopProcess(ctx context.Context, sessionID string, processID string) (processenv.Info, error) {
	if r != nil && r.StopProcessFunc != nil {
		return r.StopProcessFunc(ctx, sessionID, processID)
	}

	return processenv.Info{}, nil
}
func (r *Runtime) ListProcesses(sessionID string) []processenv.Info {
	if r != nil && r.ListProcessesFunc != nil {
		return r.ListProcessesFunc(sessionID)
	}

	return nil
}
func (r *Runtime) SearchSession(ctx context.Context, req envtypes.SessionSearchRequest) ([]envtypes.SessionSearchResult, error) {
	if r != nil && r.SearchSessionFunc != nil {
		return r.SearchSessionFunc(ctx, req)
	}

	return nil, nil
}
func (r *Runtime) GetSessionMessages(ctx context.Context, req envsessionmessages.SessionMessagesRequest) (envsessionmessages.SessionMessagesResponse, error) {
	if r != nil && r.GetSessionMessagesFunc != nil {
		return r.GetSessionMessagesFunc(ctx, req)
	}

	return envsessionmessages.SessionMessagesResponse{}, nil
}
func (r *Runtime) SupportsMemorySearch(ctx context.Context) (bool, error) {
	if r != nil && r.SupportsMemorySearchFunc != nil {
		return r.SupportsMemorySearchFunc(ctx)
	}

	return false, nil
}
func (r *Runtime) SearchMemory(ctx context.Context, query memory.SearchQuery) (memory.SearchResult, error) {
	if r != nil && r.SearchMemoryFunc != nil {
		return r.SearchMemoryFunc(ctx, query)
	}

	return memory.SearchResult{}, nil
}
func (r *Runtime) SupportsMemoryExtraction(ctx context.Context) (bool, error) {
	if r != nil && r.SupportsMemoryExtractionFunc != nil {
		return r.SupportsMemoryExtractionFunc(ctx)
	}

	return false, nil
}
func (r *Runtime) ExtractEpisodes(ctx context.Context, req episodic.Request) (episodic.Result, error) {
	if r != nil && r.ExtractEpisodesFunc != nil {
		return r.ExtractEpisodesFunc(ctx, req)
	}

	return episodic.Result{}, nil
}
func (r *Runtime) SupportsMemoryWrite(ctx context.Context) (bool, error) {
	if r != nil && r.SupportsMemoryWriteFunc != nil {
		return r.SupportsMemoryWriteFunc(ctx)
	}

	return false, nil
}
func (r *Runtime) RecordSemanticMemory(ctx context.Context, record memory.SemanticRecord) (memory.MemoryItem, error) {
	if r != nil && r.RecordSemanticMemoryFunc != nil {
		return r.RecordSemanticMemoryFunc(ctx, record)
	}

	return memory.MemoryItem{}, nil
}
func (r *Runtime) RecordProceduralMemory(ctx context.Context, record memory.ProceduralRecord) (memory.MemoryItem, error) {
	if r != nil && r.RecordProceduralMemoryFunc != nil {
		return r.RecordProceduralMemoryFunc(ctx, record)
	}

	return memory.MemoryItem{}, nil
}
func (r *Runtime) PromoteMemoryCandidate(ctx context.Context, req memory.PromotionRequest) (memory.LifecycleResult, error) {
	if r != nil && r.PromoteMemoryCandidateFunc != nil {
		return r.PromoteMemoryCandidateFunc(ctx, req)
	}

	return memory.LifecycleResult{}, nil
}
func (r *Runtime) UpdateMemory(ctx context.Context, req memory.UpdateRequest) (memory.UpdateResult, error) {
	if r != nil && r.UpdateMemoryFunc != nil {
		return r.UpdateMemoryFunc(ctx, req)
	}

	return memory.UpdateResult{}, nil
}
func (r *Runtime) DeleteMemory(ctx context.Context, req memory.DeleteRequest) error {
	if r != nil && r.DeleteMemoryFunc != nil {
		return r.DeleteMemoryFunc(ctx, req)
	}

	return nil
}
func (r *Runtime) AutomationService(context.Context) (envtypes.AutomationService, bool, error) {
	if r == nil {
		return nil, false, nil
	}

	return r.AutomationServiceValue, r.AutomationServiceOK, r.AutomationServiceErr
}
func (r *Runtime) BrowserService(context.Context) (envtypes.BrowserService, bool, error) {
	if r == nil {
		return nil, false, nil
	}

	return r.BrowserServiceValue, r.BrowserServiceOK, r.BrowserServiceErr
}
func (r *Runtime) GetPlan(string) envtypes.Plan { return envtypes.Plan{} }
func (r *Runtime) ReplacePlan(string, envtypes.Plan) (envtypes.Plan, error) {
	return envtypes.Plan{}, nil
}
func (r *Runtime) MergePlan(string, []envtypes.PartialPlanStep, string, bool) (envtypes.Plan, error) {
	return envtypes.Plan{}, nil
}
func (r *Runtime) ClearPlan(string) envtypes.Plan    { return envtypes.Plan{} }
func (r *Runtime) HydratePlan(string, envtypes.Plan) {}

// NewRuntime returns a runtime implementation bound to env.
func NewRuntime(root string, policy guardrails.CommandPolicy) *Runtime {
	return &Runtime{
		FilePolicyValue:    guardrails.FilesystemPolicy{Roots: guardrails.NormalizeRoots([]string{root})},
		CommandPolicyValue: policy.Normalize(),
	}
}

// RegisterRuntime registers mock runtime tool definitions with registry.
func RegisterRuntime(
	t *testing.T,
	root string,
	policy guardrails.CommandPolicy,
	definitions ...func(envtypes.Runtime) tools.Definition,
) tools.Registry {
	return RegisterRuntimeWithPermissionPolicy(t, root, policy, permissions.Policy{
		Default:             permissions.DecisionAllow,
		SurfaceKindDefaults: map[permissions.SurfaceKind]permissions.Decision{},
	}, definitions...)
}

func RegisterRuntimeWithPermissionPolicy(
	t *testing.T,
	root string,
	commandPolicy guardrails.CommandPolicy,
	permissionPolicy permissions.Policy,
	definitions ...func(envtypes.Runtime) tools.Definition,
) tools.Registry {
	t.Helper()

	registry := tools.NewDefaultRegistry(tools.RegistryOptions{PermissionPolicy: permissionPolicy})
	runtime := NewRuntime(root, commandPolicy)

	require.NoError(t, registry.RegisterGroup(tools.Group{Name: "core"}))
	for _, definition := range definitions {
		require.NoError(t, registry.Register(definition(runtime)))
	}

	return registry
}

// QuoteJSON returns value encoded as a JSON string literal.
func QuoteJSON(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

// FailingPlanRuntime is a mock runtime whose plan operations fail.
type FailingPlanRuntime struct {
	Runtime    envtypes.Runtime
	MergeErr   error
	ReplaceErr error
}

func (d *FailingPlanRuntime) FilePolicy() guardrails.FilesystemPolicy { return d.Runtime.FilePolicy() }
func (d *FailingPlanRuntime) CommandPolicy() guardrails.CommandPolicy {
	return d.Runtime.CommandPolicy()
}
func (d *FailingPlanRuntime) CommandShell() string {
	return d.Runtime.CommandShell()
}
func (d *FailingPlanRuntime) CommandIdentityKey() []byte {
	return d.Runtime.CommandIdentityKey()
}
func (d *FailingPlanRuntime) StartProcess(ctx context.Context, sessionID string, req processenv.StartRequest) (processenv.Info, error) {
	return d.Runtime.StartProcess(ctx, sessionID, req)
}
func (d *FailingPlanRuntime) GetProcess(sessionID string, processID string) (processenv.Info, error) {
	return d.Runtime.GetProcess(sessionID, processID)
}
func (d *FailingPlanRuntime) ReadProcess(sessionID string, req processenv.ReadRequest) (processenv.Output, error) {
	return d.Runtime.ReadProcess(sessionID, req)
}
func (d *FailingPlanRuntime) StopProcess(ctx context.Context, sessionID string, processID string) (processenv.Info, error) {
	return d.Runtime.StopProcess(ctx, sessionID, processID)
}
func (d *FailingPlanRuntime) ListProcesses(sessionID string) []processenv.Info {
	return d.Runtime.ListProcesses(sessionID)
}
func (d *FailingPlanRuntime) SearchSession(ctx context.Context, req envtypes.SessionSearchRequest) ([]envtypes.SessionSearchResult, error) {
	return d.Runtime.SearchSession(ctx, req)
}
func (d *FailingPlanRuntime) AutomationService(ctx context.Context) (envtypes.AutomationService, bool, error) {
	return d.Runtime.AutomationService(ctx)
}
func (d *FailingPlanRuntime) BrowserService(ctx context.Context) (envtypes.BrowserService, bool, error) {
	return d.Runtime.BrowserService(ctx)
}
func (d *FailingPlanRuntime) GetSessionMessages(ctx context.Context, req envsessionmessages.SessionMessagesRequest) (envsessionmessages.SessionMessagesResponse, error) {
	return d.Runtime.GetSessionMessages(ctx, req)
}
func (d *FailingPlanRuntime) SupportsMemorySearch(ctx context.Context) (bool, error) {
	return d.Runtime.SupportsMemorySearch(ctx)
}
func (d *FailingPlanRuntime) SearchMemory(ctx context.Context, query memory.SearchQuery) (memory.SearchResult, error) {
	return d.Runtime.SearchMemory(ctx, query)
}
func (d *FailingPlanRuntime) SupportsMemoryExtraction(ctx context.Context) (bool, error) {
	return d.Runtime.SupportsMemoryExtraction(ctx)
}
func (d *FailingPlanRuntime) ExtractEpisodes(ctx context.Context, req episodic.Request) (episodic.Result, error) {
	return d.Runtime.ExtractEpisodes(ctx, req)
}
func (d *FailingPlanRuntime) SupportsMemoryWrite(ctx context.Context) (bool, error) {
	return d.Runtime.SupportsMemoryWrite(ctx)
}
func (d *FailingPlanRuntime) RecordSemanticMemory(ctx context.Context, record memory.SemanticRecord) (memory.MemoryItem, error) {
	return d.Runtime.RecordSemanticMemory(ctx, record)
}
func (d *FailingPlanRuntime) RecordProceduralMemory(ctx context.Context, record memory.ProceduralRecord) (memory.MemoryItem, error) {
	return d.Runtime.RecordProceduralMemory(ctx, record)
}
func (d *FailingPlanRuntime) PromoteMemoryCandidate(ctx context.Context, req memory.PromotionRequest) (memory.LifecycleResult, error) {
	return d.Runtime.PromoteMemoryCandidate(ctx, req)
}
func (d *FailingPlanRuntime) UpdateMemory(ctx context.Context, req memory.UpdateRequest) (memory.UpdateResult, error) {
	return d.Runtime.UpdateMemory(ctx, req)
}
func (d *FailingPlanRuntime) DeleteMemory(ctx context.Context, req memory.DeleteRequest) error {
	return d.Runtime.DeleteMemory(ctx, req)
}
func (d *FailingPlanRuntime) GetPlan(sessionID string) envtypes.Plan {
	return d.Runtime.GetPlan(sessionID)
}
func (d *FailingPlanRuntime) ReplacePlan(sessionID string, plan envtypes.Plan) (envtypes.Plan, error) {
	if d.ReplaceErr != nil {
		return envtypes.Plan{}, d.ReplaceErr
	}

	return d.Runtime.ReplacePlan(sessionID, plan)
}
func (d *FailingPlanRuntime) MergePlan(sessionID string, updates []envtypes.PartialPlanStep, explanation string, clearCompleted bool) (envtypes.Plan, error) {
	if d.MergeErr != nil {
		return envtypes.Plan{}, d.MergeErr
	}

	return d.Runtime.MergePlan(sessionID, updates, explanation, clearCompleted)
}
func (d *FailingPlanRuntime) ClearPlan(sessionID string) envtypes.Plan {
	return d.Runtime.ClearPlan(sessionID)
}
func (d *FailingPlanRuntime) HydratePlan(sessionID string, plan envtypes.Plan) {
	d.Runtime.HydratePlan(sessionID, plan)
}

// RecordedEvent represents a recorded event.
type RecordedEvent struct {
	Type    string
	Payload any
}

// TraceRecorder describes trace recorder.
type TraceRecorder struct {
	Events []RecordedEvent
}

func (s *TraceRecorder) Record(eventType string, payload any) {
	s.Events = append(s.Events, RecordedEvent{Type: eventType, Payload: payload})
}
