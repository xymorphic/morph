package environment

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wandxy/morph/internal/agent/runcontext"
	"github.com/wandxy/morph/internal/environment/planstore"
	"github.com/wandxy/morph/internal/environment/process"
	"github.com/wandxy/morph/internal/environment/sessionmessages"
	"github.com/wandxy/morph/internal/environment/sessionsearch"
	envtypes "github.com/wandxy/morph/internal/environment/types"
	"github.com/wandxy/morph/internal/execution"
	"github.com/wandxy/morph/internal/guardrails"
	"github.com/wandxy/morph/internal/memory"
	"github.com/wandxy/morph/internal/memory/episodic"
	"github.com/wandxy/morph/internal/permissions"
	statemanager "github.com/wandxy/morph/internal/state/manager"
)

var getwd = os.Getwd

// Runtime exposes environment-backed services to tools.
type Runtime struct {
	filePolicy       guardrails.FilesystemPolicy
	commandPolicy    guardrails.CommandPolicy
	commandShell     string
	commandKey       []byte
	processMgr       process.Manager
	plans            planstore.Store
	stateMgr         *statemanager.Manager
	automation       envtypes.AutomationService
	browser          envtypes.BrowserService
	memory           memory.Provider
	execution        execution.Service
	executionBase    execution.ExposureInput
	executionTarget  execution.CommandTarget
	executionSecrets map[string]string
	profile          string
}

// NewRuntime returns a runtime implementation bound to env.
func NewRuntime(
	roots []string,
	policy guardrails.CommandPolicy,
	stateMgr *statemanager.Manager,
) *Runtime {
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

func (r *Runtime) ExecutionService() execution.Service {
	if r == nil {
		return nil
	}
	return r.execution
}

func (r *Runtime) ListExecutionEnvironments(
	ctx context.Context,
) ([]execution.EnvironmentDetails, error) {
	if r == nil || r.execution == nil {
		return nil, errors.New("execution service is required")
	}
	owner, err := r.getExecutionOwner(ctx)
	if err != nil {
		return nil, err
	}
	statuses, err := r.execution.Status(ctx, owner)
	if err != nil {
		return nil, err
	}
	result := make([]execution.EnvironmentDetails, 0, len(statuses))
	for _, status := range statuses {
		secretReferences := make([]string, 0, len(r.executionSecrets))
		for name := range r.executionSecrets {
			secretReferences = append(secretReferences, name)
		}
		slices.Sort(secretReferences)
		result = append(result, execution.EnvironmentDetails{
			Status:           status,
			Mounts:           slices.Clone(r.executionBase.Mounts),
			Limits:           r.executionBase.Limits,
			PolicyHash:       r.executionBase.PolicyHash,
			ImageContract:    r.executionBase.ImageContractDigest,
			SecretReferences: secretReferences,
		})
	}
	return result, nil
}

func (r *Runtime) ExecutionCommandTarget() (execution.CommandTarget, bool) {
	if r == nil || r.executionBase.Backend != execution.BackendDocker {
		return execution.CommandTarget{}, false
	}
	return r.executionTarget, true
}

func (r *Runtime) GetExecutionSecretCatalog() []execution.SecretCatalogEntry {
	if r == nil {
		return nil
	}
	entries := make([]execution.SecretCatalogEntry, 0, len(r.executionSecrets))
	for name, description := range r.executionSecrets {
		entries = append(
			entries,
			execution.SecretCatalogEntry{
				Name:        name,
				Description: description,
			},
		)
	}
	slices.SortFunc(entries, func(left, right execution.SecretCatalogEntry) int {
		return strings.Compare(left.Name, right.Name)
	})
	return entries
}

func (r *Runtime) PrepareExecutionSpec(
	ctx context.Context,
	operation execution.Operation,
) (execution.Spec, error) {
	if r == nil || r.execution == nil {
		return execution.Spec{}, errors.New("execution service is required")
	}
	owner, err := r.getExecutionOwner(ctx)
	if err != nil {
		return execution.Spec{}, err
	}
	input := r.executionBase
	input.SecretReferences = slices.Clone(operation.SecretReferences)
	for _, reference := range input.SecretReferences {
		if _, ok := r.executionSecrets[reference]; !ok {
			return execution.Spec{}, errors.New(
				"execution secret reference is not configured: " + reference,
			)
		}
	}
	if input.Scope == execution.ScopeShared {
		input.WorkspaceIdentity = strings.Join([]string{owner.Profile, "shared"}, ":")
	} else {
		input.WorkspaceIdentity = strings.Join(
			[]string{owner.Profile, "session", owner.EffectiveSessionID},
			":",
		)
	}
	exposure, err := execution.NewExposure(input)
	if err != nil {
		return execution.Spec{}, err
	}
	return execution.NewSpec(owner, exposure, operation)
}

func (r *Runtime) PrepareExecutionPath(
	ctx context.Context,
	path string,
	action execution.FilesystemAction,
) (execution.PreparedPath, error) {
	if r == nil {
		return execution.PreparedPath{}, errors.New("execution runtime is required")
	}
	if r.executionBase.Backend == execution.BackendLocal {
		permissionAction := permissions.ActionRead
		if action == execution.FilesystemWrite || action == execution.FilesystemPatch {
			permissionAction = permissions.ActionUpdate
		}
		resolved, err := r.resolveExecutionHostPath(ctx, path, permissionAction)
		if err != nil {
			return execution.PreparedPath{}, err
		}
		return execution.NewPreparedPath(execution.PreparedPathInput{
			LogicalPath:        resolved.Absolute,
			HostSourceIdentity: resolved.Absolute,
			ContainerPath:      filepath.ToSlash(resolved.Absolute),
			Mode:               execution.MountReadWrite,
			Action:             action,
			SecurityGeneration: r.executionBase.SecurityGeneration,
		})
	}
	return r.prepareDockerPath(path, action)
}

func (r *Runtime) prepareDockerPath(
	path string,
	action execution.FilesystemAction,
) (execution.PreparedPath, error) {
	cleaned := filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	containerPath := cleaned
	grant := "workspace"
	mode := execution.MountReadWrite
	if !strings.HasPrefix(containerPath, "/") {
		containerPath = filepath.ToSlash(filepath.Join("/workspace", containerPath))
	}
	if containerPath == "/workspace" || strings.HasPrefix(containerPath, "/workspace/") {
		if r.executionBase.WorkspaceMode == execution.WorkspaceReadOnly {
			mode = execution.MountReadOnly
		}
	} else if strings.HasPrefix(containerPath, "/mnt/") {
		parts := strings.Split(strings.TrimPrefix(containerPath, "/mnt/"), "/")
		grant = parts[0]
		found := false
		for _, mount := range r.executionBase.Mounts {
			if mount.Name == grant &&
				(containerPath == mount.Target ||
					strings.HasPrefix(containerPath, mount.Target+"/")) {
				mode = mount.Mode
				found = true
				break
			}
		}
		if !found {
			return execution.PreparedPath{}, errors.New("execution path does not map to a configured mount")
		}
	} else {
		return execution.PreparedPath{}, errors.New(
			"docker execution paths must be below /workspace or a configured /mnt grant",
		)
	}
	return execution.NewPreparedPath(execution.PreparedPathInput{
		LogicalPath:        containerPath,
		ContainerPath:      containerPath,
		Grant:              grant,
		Mode:               mode,
		Action:             action,
		SecurityGeneration: r.executionBase.SecurityGeneration,
	})
}

func (r *Runtime) getExecutionOwner(ctx context.Context) (execution.Owner, error) {
	authorization, hasAuthorization := permissions.FromContext(ctx)
	runCtx, hasRunContext := runcontext.FromContext(ctx)
	if r.executionBase.Backend == execution.BackendDocker && !hasAuthorization && !hasRunContext {
		return execution.Owner{}, errors.New(
			"docker execution requires an authenticated session owner",
		)
	}
	profileName := r.profile
	actorKind := string(permissions.ActorLocalOwner)
	surface := string(permissions.SurfaceCLI)
	publicSessionID := "default"
	effectiveSessionID := "default"
	if hasAuthorization {
		if authorization.Profile != "" {
			profileName = authorization.Profile
		}
		actorKind = string(authorization.Actor.Kind)
		surface = string(authorization.Surface)
		publicSessionID = authorization.SessionID
		effectiveSessionID = authorization.SessionID
	}
	if hasRunContext {
		if runCtx.Session.PublicID != "" {
			publicSessionID = runCtx.Session.PublicID
		}
		effectiveSessionID = runCtx.StateSessionID()
	}
	return (execution.Owner{
		Profile:            profileName,
		ActorKind:          actorKind,
		ActorID:            authorization.Actor.ID,
		Surface:            surface,
		PublicSessionID:    publicSessionID,
		EffectiveSessionID: effectiveSessionID,
		RunID:              authorization.RunID,
	}).Normalize()
}

func (r *Runtime) resolveExecutionHostPath(
	ctx context.Context,
	path string,
	action permissions.Action,
) (guardrails.ResolvedPath, error) {
	target := filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	targetScope := permissions.TargetScopeExternal
	if _, err := r.filePolicy.Resolve(path); err == nil {
		targetScope = permissions.TargetScopeWorkspace
	}
	operation := permissions.Operation{
		Resource:    permissions.ResourceFile,
		Action:      action,
		Target:      target,
		TargetScope: targetScope,
	}
	preset, _ := permissions.PresetFromContext(ctx)
	mayApproveExternal := preset == permissions.PresetAskForApproval ||
		preset == permissions.PresetApproveForMe
	if permissions.HasFullAccess(ctx) || mayApproveExternal ||
		permissions.IsOperationAuthorized(ctx, operation) {
		return r.filePolicy.ResolveUnrestricted(path)
	}
	return r.filePolicy.Resolve(path)
}

func getExecutionSecurityGeneration(value any) string {
	raw, _ := json.Marshal(value)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
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

func (r *Runtime) SearchMemory(
	ctx context.Context,
	query memory.SearchQuery,
) (memory.SearchResult, error) {
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

func (r *Runtime) ExtractEpisodes(
	ctx context.Context,
	req episodic.Request,
) (episodic.Result, error) {
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

func (r *Runtime) UpdateMemory(
	ctx context.Context,
	req memory.UpdateRequest,
) (memory.UpdateResult, error) {
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

func (r *Runtime) MergePlan(
	sessionID string,
	updates []planstore.PartialPlanStep,
	explanation string,
	clearCompleted bool,
) (planstore.Plan, error) {
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
