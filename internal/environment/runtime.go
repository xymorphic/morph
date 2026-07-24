package environment

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"slices"

	"github.com/wandxy/morph/internal/agent/runcontext"
	"github.com/wandxy/morph/internal/environment/planstore"
	"github.com/wandxy/morph/internal/environment/process"
	"github.com/wandxy/morph/internal/environment/sessionmessages"
	"github.com/wandxy/morph/internal/environment/sessionsearch"
	envtypes "github.com/wandxy/morph/internal/environment/types"
	"github.com/wandxy/morph/internal/guardrails"
	"github.com/wandxy/morph/internal/memory"
	"github.com/wandxy/morph/internal/memory/episodic"
	statemanager "github.com/wandxy/morph/internal/state/manager"
)

var getwd = os.Getwd

// Runtime exposes environment-backed services to tools.
type Runtime struct {
	filePolicy    guardrails.FilesystemPolicy
	commandPolicy guardrails.CommandPolicy
	commandShell  string
	commandKey    []byte
	processMgr    process.Manager
	plans         planstore.Store
	stateMgr      *statemanager.Manager
	automation    envtypes.AutomationService
	browser       envtypes.BrowserService
	memory        memory.Provider
}

// NewRuntime returns a runtime implementation bound to env.
func NewRuntime(roots []string, policy guardrails.CommandPolicy, stateMgr *statemanager.Manager) *Runtime {
	if len(roots) == 0 {
		cwd, err := getwd()
		if err != nil {
			cwd = "."
		}
		roots = []string{filepath.Clean(cwd)}
	}

	runtime := &Runtime{
		filePolicy:    guardrails.FilesystemPolicy{Roots: guardrails.NormalizeRoots(roots)},
		commandPolicy: policy.Normalize(),
		processMgr:    &process.DefaultManager{},
		plans:         &planstore.MemoryPlanStore{},
		stateMgr:      stateMgr,
	}
	runtime.commandKey = make([]byte, 32)
	if _, err := rand.Read(runtime.commandKey); err != nil {
		panic(err)
	}
	return runtime
}

func (r *Runtime) FilePolicy() guardrails.FilesystemPolicy {
	if r == nil {
		return guardrails.FilesystemPolicy{Roots: guardrails.NormalizeRoots(nil)}
	}

	return r.filePolicy
}

func (r *Runtime) CommandPolicy() guardrails.CommandPolicy {
	if r == nil {
		return guardrails.CommandPolicy{}.Normalize()
	}

	return r.commandPolicy
}

func (r *Runtime) CommandShell() string {
	if r == nil {
		return ""
	}
	return r.commandShell
}

func (r *Runtime) CommandIdentityKey() []byte {
	if r == nil {
		return nil
	}
	return slices.Clone(r.commandKey)
}

func (r *Runtime) setCommandIdentityKey(key []byte) {
	if r == nil || len(key) == 0 {
		return
	}
	digest := hmac.New(sha256.New, key)
	_, _ = digest.Write([]byte("morph/command-identity/v1"))
	r.commandKey = digest.Sum(nil)
}

func (r *Runtime) StartProcess(
	ctx context.Context,
	sessionID string,
	req process.StartRequest,
) (process.Info, error) {
	if r == nil || r.processMgr == nil {
		return process.Info{}, errors.New("process manager is required")
	}

	return r.processMgr.Start(ctx, sessionID, req)
}

func (r *Runtime) GetProcess(sessionID string, processID string) (process.Info, error) {
	if r == nil || r.processMgr == nil {
		return process.Info{}, errors.New("process manager is required")
	}

	return r.processMgr.Get(sessionID, processID)
}

func (r *Runtime) ReadProcess(sessionID string, req process.ReadRequest) (process.Output, error) {
	if r == nil || r.processMgr == nil {
		return process.Output{}, errors.New("process manager is required")
	}

	return r.processMgr.Read(sessionID, req)
}

func (r *Runtime) StopProcess(ctx context.Context, sessionID string, processID string) (process.Info, error) {
	if r == nil || r.processMgr == nil {
		return process.Info{}, errors.New("process manager is required")
	}

	return r.processMgr.Stop(ctx, sessionID, processID)
}

func (r *Runtime) ListProcesses(sessionID string) []process.Info {
	if r == nil || r.processMgr == nil {
		return nil
	}

	return r.processMgr.List(sessionID)
}

func (r *Runtime) SearchSession(
	ctx context.Context,
	req sessionsearch.SessionSearchRequest,
) ([]sessionsearch.SessionSearchResult, error) {
	if r == nil || r.stateMgr == nil {
		return nil, errors.New("state manager is required")
	}

	return sessionsearch.Search(ctx, r.stateMgr, req)
}

func (r *Runtime) AutomationService(context.Context) (envtypes.AutomationService, bool, error) {
	if r == nil {
		return nil, false, errors.New("runtime is required")
	}
	if r.automation == nil {
		return nil, false, nil
	}

	return r.automation, true, nil
}

func (r *Runtime) BrowserService(context.Context) (envtypes.BrowserService, bool, error) {
	if r == nil {
		return nil, false, errors.New("runtime is required")
	}
	if r.browser == nil {
		return nil, false, nil
	}

	return r.browser, true, nil
}

func (r *Runtime) GetSessionMessages(
	ctx context.Context,
	req sessionmessages.SessionMessagesRequest,
) (sessionmessages.SessionMessagesResponse, error) {
	if r == nil || r.stateMgr == nil {
		return sessionmessages.SessionMessagesResponse{}, errors.New("state manager is required")
	}

	return sessionmessages.Get(ctx, r.stateMgr, req)
}

func (r *Runtime) SupportsMemorySearch(ctx context.Context) (bool, error) {
	_, supported, err := r.memorySearchProvider(ctx)
	return supported, err
}

func (r *Runtime) memorySearchProvider(ctx context.Context) (memory.SearchProvider, bool, error) {
	if r == nil || r.memory == nil {
		return nil, false, nil
	}

	searchProvider, ok := r.memory.(memory.SearchProvider)
	if !ok {
		return nil, false, nil
	}

	caps, err := r.memory.Capabilities(ctx)
	if err != nil {
		return nil, false, err
	}

	return searchProvider, caps.SupportsSearch, nil
}

func (r *Runtime) SearchMemory(ctx context.Context, query memory.SearchQuery) (memory.SearchResult, error) {
	if r == nil || r.memory == nil {
		return memory.SearchResult{}, errors.New("memory search is not configured")
	}

	searchProvider, supported, err := r.memorySearchProvider(ctx)
	if err != nil {
		return memory.SearchResult{}, err
	}
	if !supported {
		return memory.SearchResult{}, errors.New("memory search is not supported by provider")
	}

	return searchProvider.Search(ctx, query)
}

func (r *Runtime) SupportsMemoryExtraction(ctx context.Context) (bool, error) {
	if r == nil || r.memory == nil {
		return false, nil
	}

	caps, err := r.memory.Capabilities(ctx)
	if err != nil {
		return false, err
	}

	_, supportsExtraction := r.memory.(memory.ExtractionProvider)
	return caps.SupportsEpisodeRecording && supportsExtraction, nil
}

func (r *Runtime) ExtractEpisodes(ctx context.Context, req episodic.Request) (episodic.Result, error) {
	if r == nil || r.memory == nil {
		return episodic.Result{}, errors.New("memory provider is required")
	}

	supported, err := r.SupportsMemoryExtraction(ctx)
	if err != nil {
		return episodic.Result{}, err
	}
	if !supported {
		return episodic.Result{}, errors.New("memory extraction is not supported by provider")
	}

	extractor := r.memory.(memory.ExtractionProvider)
	return extractor.ExtractEpisodes(ctx, req)
}

func (r *Runtime) SupportsMemoryWrite(ctx context.Context) (bool, error) {
	if r == nil || r.memory == nil {
		return false, nil
	}

	caps, err := r.memory.Capabilities(ctx)
	if err != nil {
		return false, err
	}

	_, supportsSemanticRecording := r.memory.(memory.SemanticProvider)
	_, supportsProceduralRecording := r.memory.(memory.ProceduralProvider)
	_, supportsLifecycle := r.memory.(memory.LifecycleProvider)
	_, supportsUpdate := r.memory.(memory.UpdateProvider)
	_, supportsDelete := r.memory.(memory.WriteProvider)

	return caps.SupportsWrite &&
		caps.SupportsSemanticRecording &&
		caps.SupportsProceduralRecording &&
		caps.SupportsDelete &&
		supportsSemanticRecording &&
		supportsProceduralRecording &&
		supportsLifecycle &&
		supportsUpdate &&
		supportsDelete, nil
}

func (r *Runtime) RecordSemanticMemory(
	ctx context.Context,
	record memory.SemanticRecord,
) (memory.MemoryItem, error) {
	provider, err := r.memorySemanticProvider(ctx)
	if err != nil {
		return memory.MemoryItem{}, err
	}
	if runCtx, ok := runcontext.FromContext(ctx); ok {
		record.Item = memory.ApplyRunProvenance(
			record.Item,
			runCtx,
			record.Item.Metadata[memory.MemoryMetadataTrigger],
		)
	}

	return provider.RecordSemanticMemory(ctx, record)
}

func (r *Runtime) RecordProceduralMemory(
	ctx context.Context,
	record memory.ProceduralRecord,
) (memory.MemoryItem, error) {
	provider, err := r.memoryProceduralProvider(ctx)
	if err != nil {
		return memory.MemoryItem{}, err
	}
	if runCtx, ok := runcontext.FromContext(ctx); ok {
		record.Item = memory.ApplyRunProvenance(
			record.Item,
			runCtx,
			record.Item.Metadata[memory.MemoryMetadataTrigger],
		)
	}

	return provider.RecordProceduralMemory(ctx, record)
}

func (r *Runtime) PromoteMemoryCandidate(
	ctx context.Context,
	req memory.PromotionRequest,
) (memory.LifecycleResult, error) {
	if err := r.checkMemoryWriteSupported(ctx); err != nil {
		return memory.LifecycleResult{}, err
	}

	provider := r.memory.(memory.LifecycleProvider)
	return provider.PromoteCandidate(ctx, req)
}

func (r *Runtime) UpdateMemory(ctx context.Context, req memory.UpdateRequest) (memory.UpdateResult, error) {
	if err := r.checkMemoryWriteSupported(ctx); err != nil {
		return memory.UpdateResult{}, err
	}

	provider := r.memory.(memory.UpdateProvider)
	if runCtx, ok := runcontext.FromContext(ctx); ok {
		req.Replacement = memory.ApplyRunProvenance(
			req.Replacement,
			runCtx,
			req.Replacement.Metadata[memory.MemoryMetadataTrigger],
		)
	}

	return provider.Update(ctx, req)
}

func (r *Runtime) DeleteMemory(ctx context.Context, req memory.DeleteRequest) error {
	if err := r.checkMemoryWriteSupported(ctx); err != nil {
		return err
	}

	provider := r.memory.(memory.WriteProvider)
	return provider.Delete(ctx, req)
}

func (r *Runtime) memorySemanticProvider(
	ctx context.Context,
) (memory.SemanticProvider, error) {
	if err := r.checkSemanticMemoryWriteSupported(ctx); err != nil {
		return nil, err
	}

	return r.memory.(memory.SemanticProvider), nil
}

func (r *Runtime) memoryProceduralProvider(
	ctx context.Context,
) (memory.ProceduralProvider, error) {
	if err := r.checkProceduralMemoryWriteSupported(ctx); err != nil {
		return nil, err
	}

	return r.memory.(memory.ProceduralProvider), nil
}

func (r *Runtime) checkSemanticMemoryWriteSupported(ctx context.Context) error {
	if r == nil || r.memory == nil {
		return errors.New("memory write is not configured")
	}

	caps, err := r.memory.Capabilities(ctx)
	if err != nil {
		return err
	}
	if !caps.SupportsWrite || !caps.SupportsSemanticRecording {
		return errors.New("semantic memory write is not supported by provider")
	}
	if _, ok := r.memory.(memory.SemanticProvider); !ok {
		return errors.New("semantic memory write is not supported by provider")
	}

	return nil
}

func (r *Runtime) checkProceduralMemoryWriteSupported(ctx context.Context) error {
	if r == nil || r.memory == nil {
		return errors.New("memory write is not configured")
	}

	caps, err := r.memory.Capabilities(ctx)
	if err != nil {
		return err
	}
	if !caps.SupportsWrite || !caps.SupportsProceduralRecording {
		return errors.New("procedural memory write is not supported by provider")
	}
	if _, ok := r.memory.(memory.ProceduralProvider); !ok {
		return errors.New("procedural memory write is not supported by provider")
	}

	return nil
}

func (r *Runtime) checkMemoryWriteSupported(ctx context.Context) error {
	if r == nil || r.memory == nil {
		return errors.New("memory write is not configured")
	}

	supported, err := r.SupportsMemoryWrite(ctx)
	if err != nil {
		return err
	}
	if !supported {
		return errors.New("memory write is not supported by provider")
	}

	return nil
}

func (r *Runtime) GetPlan(sessionID string) planstore.Plan {
	if r == nil || r.plans == nil {
		return planstore.Plan{}
	}

	return r.plans.Get(sessionID)
}

func (r *Runtime) ReplacePlan(sessionID string, plan planstore.Plan) (planstore.Plan, error) {
	if r == nil || r.plans == nil {
		return planstore.ClonePlan(plan), errors.New("plan store is required")
	}

	return r.plans.Replace(sessionID, plan)
}

func (r *Runtime) MergePlan(sessionID string, updates []planstore.PartialPlanStep, explanation string, clearCompleted bool) (planstore.Plan, error) {
	if r == nil || r.plans == nil {
		return planstore.Plan{}, errors.New("plan store is required")
	}

	return r.plans.Merge(sessionID, updates, explanation, clearCompleted)
}

func (r *Runtime) ClearPlan(sessionID string) planstore.Plan {
	if r == nil || r.plans == nil {
		return planstore.Plan{}
	}

	return r.plans.Clear(sessionID)
}

func (r *Runtime) HydratePlan(sessionID string, plan planstore.Plan) {
	if r == nil || r.plans == nil {
		return
	}

	r.plans.Hydrate(sessionID, plan)
}
