package environment

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/xymorphic/morph/pkg/logutils"
	"github.com/xymorphic/morph/pkg/str"

	"github.com/xymorphic/morph/internal/agent/runcontext"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/constants"
	"github.com/xymorphic/morph/internal/datadir"
	"github.com/xymorphic/morph/internal/environment/budget"
	"github.com/xymorphic/morph/internal/environment/planstore"
	envtypes "github.com/xymorphic/morph/internal/environment/types"
	"github.com/xymorphic/morph/internal/execution"
	executiondocker "github.com/xymorphic/morph/internal/execution/docker"
	executionlocal "github.com/xymorphic/morph/internal/execution/local"
	"github.com/xymorphic/morph/internal/guardrails"
	"github.com/xymorphic/morph/internal/instructions"
	"github.com/xymorphic/morph/internal/memory"
	memguardrails "github.com/xymorphic/morph/internal/memory/guardrails"
	memoryobservability "github.com/xymorphic/morph/internal/memory/observability"
	models "github.com/xymorphic/morph/internal/model"
	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/internal/personality"
	"github.com/xymorphic/morph/internal/profile"
	webprovider "github.com/xymorphic/morph/internal/providers/web"
	storage "github.com/xymorphic/morph/internal/state/core"
	statemanager "github.com/xymorphic/morph/internal/state/manager"
	"github.com/xymorphic/morph/internal/tools"
	automationtool "github.com/xymorphic/morph/internal/tools/automation"
	browsertool "github.com/xymorphic/morph/internal/tools/browser"
	"github.com/xymorphic/morph/internal/tools/listfiles"
	"github.com/xymorphic/morph/internal/tools/memoryextract"
	"github.com/xymorphic/morph/internal/tools/memorysearch"
	"github.com/xymorphic/morph/internal/tools/memorywrite"
	"github.com/xymorphic/morph/internal/tools/patch"
	"github.com/xymorphic/morph/internal/tools/plan"
	"github.com/xymorphic/morph/internal/tools/process"
	"github.com/xymorphic/morph/internal/tools/readfile"
	"github.com/xymorphic/morph/internal/tools/runcommand"
	"github.com/xymorphic/morph/internal/tools/searchfiles"
	"github.com/xymorphic/morph/internal/tools/sessionmessages"
	"github.com/xymorphic/morph/internal/tools/sessionsearch"
	"github.com/xymorphic/morph/internal/tools/time"
	"github.com/xymorphic/morph/internal/tools/webextract"
	"github.com/xymorphic/morph/internal/tools/websearch"
	"github.com/xymorphic/morph/internal/tools/writefile"
	"github.com/xymorphic/morph/internal/trace"
	"github.com/xymorphic/morph/internal/workspace"
)

var (
	log                = logutils.Module("environment")
	loadWorkspaceRules = workspace.Load
	loadPersonality    = personality.Load
	newMemoryProvider  = memory.NewProvider
)

const configInstructInstructionName = "config.instruct"

// Environment exposes runtime services used by tools and agent turns.
type Environment interface {
	// Prepare registers native tools and builds system instructions from config,
	// personality overlays, workspace rules, and optional config instruct. Call once
	// before using Instructions or Tools for a run.
	Prepare() error

	// Instructions returns a copy of the base prompts.
	Instructions() instructions.Instructions
	SafetyTraceEvents() []guardrails.SafetyTracePayloadOptions

	// Tools returns the registry for tools.
	Tools() ToolRegistry

	// ToolPolicy reflects configured capabilities and platform for resolving and gating tools.
	ToolPolicy() tools.Policy

	// NewIterationBudget creates the tool-calling iteration limit from config (max iterations).
	NewIterationBudget() budget.IterationBudget

	// NewTraceSession opens a trace sink for the given storage session when debug tracing is enabled.
	NewTraceSession(sessionID string) trace.Session
	NewTraceSessionForRun(runcontext.Context) trace.Session

	// MemoryProvider returns the configured durable memory provider, when enabled.
	MemoryProvider() memory.Provider

	// CurrentPlan returns the in-memory plan state for the given session.
	CurrentPlan(sessionID string) planstore.Plan

	// HydratePlan seeds the in-memory plan state for the given session.
	HydratePlan(sessionID string, plan planstore.Plan)

	// SetStateManager wires state-backed features into the environment runtime.
	SetStateManager(*statemanager.Manager)
	SetApprovalService(permissions.Approver)
	SetAutomationService(envtypes.AutomationService)
	SetBrowserService(envtypes.BrowserService)
	SetCommandIdentityKey([]byte)
	SetModelClient(models.Client)
}

type environment struct {
	ctx            context.Context
	cfg            *config.Config
	instructions   instructions.Instructions
	workspace      workspace.Result
	tools          tools.Registry
	traces         trace.Factory
	memory         memory.Provider
	runtime        *Runtime
	stateMgr       *statemanager.Manager
	modelClient    models.Client
	safetyEvents   []guardrails.SafetyTracePayloadOptions
	unsafeEvidence guardrails.UnsafeEvidenceRecorder
}

// ToolRegistry resolves model-visible tool definitions and invokers.
type ToolRegistry interface {
	GetGroup(string) (tools.Group, bool)
	List() tools.Definitions
	ListGroups() []tools.Group
	Resolve(tools.Policy) (tools.Definitions, error)
	Invoke(context.Context, tools.Call) (tools.Result, error)
}

func (e *environment) ToolPolicy() tools.Policy {
	if e == nil || e.cfg == nil {
		return tools.Policy{
			Capabilities: tools.Capabilities{
				Filesystem: true,
				Network:    true,
				Exec:       true,
				Memory:     true,
			},
			Platform: "cli",
		}
	}

	return tools.Policy{
		Capabilities: tools.Capabilities{
			Filesystem: *e.cfg.Cap.Filesystem,
			Network:    *e.cfg.Cap.Network,
			Exec:       *e.cfg.Cap.Exec,
			Memory:     *e.cfg.Cap.Memory,
			Browser:    *e.cfg.Cap.Browser,
		},
		Platform: e.cfg.Platform,
	}
}

func (e *environment) MemoryProvider() memory.Provider {
	if e == nil {
		return nil
	}

	return e.memory
}

// NewEnvironment builds the runtime environment from config and process dependencies.
func NewEnvironment(ctx context.Context, cfg *config.Config) Environment {
	var registryOptions tools.RegistryOptions
	if cfg != nil {
		registryOptions.PermissionPolicy = cfg.Permissions
	}
	registry := tools.NewDefaultRegistry(registryOptions)

	return &environment{
		ctx:          ctx,
		cfg:          cfg,
		instructions: instructions.Instructions{},
		tools:        registry,
		traces:       trace.NoopFactory(),
	}
}

func (e *environment) Prepare() error {
	if e == nil {
		return errors.New("environment is required")
	}

	if e.cfg == nil {
		return errors.New("config is required")
	}

	e.cfg.Normalize()

	e.prepareUnsafeEvidence()
	e.prepareInstructions()

	if e.stateMgr == nil {
		return errors.New("state manager is required")
	}

	e.prepareTraceFactory()

	if err := e.prepareMemory(); err != nil {
		return err
	}

	return e.prepareTools()
}

func (e *environment) prepareUnsafeEvidence() {
	if e == nil {
		return
	}
	e.unsafeEvidence = nil
	if e.cfg == nil || !e.cfg.RetainUnsafeEnabled() {
		return
	}
	e.unsafeEvidence = guardrails.NewFileUnsafeEvidenceStore(datadir.UnsafeEvidenceDir())
}

func (e *environment) prepareTraceFactory() {
	if e == nil || e.cfg == nil || !e.cfg.Trace.Enabled {
		e.traces = trace.NoopFactory()
		return
	}

	factories := make([]trace.Factory, 0, 2)
	redactor := guardrails.NewRedactor()

	if e.cfg.Trace.Disk.Enabled == nil || *e.cfg.Trace.Disk.Enabled {
		traceDir := e.cfg.Trace.Disk.Dir
		if traceDir == "" {
			traceDir = datadir.DebugTraceDir()
		}
		factories = append(factories, trace.NewFileFactory(traceDir, redactor))
	}

	if e.cfg.Trace.Database.Enabled == nil || *e.cfg.Trace.Database.Enabled {
		if e.stateMgr != nil {
			factories = append(factories, trace.NewStateFactory(
				e.stateMgr,
				redactor,
				e.cfg.Trace.Database.MaxEventsPerSession,
			))
		}
	}

	e.traces = trace.NewMultiFactory(factories...)
}

func (e *environment) prepareMemory() error {
	if e == nil || e.cfg == nil || !e.cfg.MemoryEnabled() {
		e.memory = nil
		return nil
	}

	hasMaxOutputTokens := e.cfg.SummaryModelSupportsMaxOutputTokens()
	opts := memory.Options{
		Guardrails:             memguardrails.New(guardrails.NewRedactor()),
		StateManager:           e.stateMgr,
		StorageBackend:         e.cfg.Storage.Backend,
		MemoryBackend:          e.cfg.Memory.Backend,
		ModelClient:            e.modelClient,
		Model:                  e.cfg.SummaryModelEffective(),
		API:                    e.cfg.SummaryModelAPIEffective(),
		MaxOutputTokensEnabled: &hasMaxOutputTokens,
		DebugRequests:          e.cfg.Debug.Requests,
		Pinned: memory.PinnedOptions{
			Enabled:      e.cfg.Memory.Pinned.Enabled,
			MaxChars:     e.cfg.Memory.Pinned.MaxChars,
			MaxItemChars: e.cfg.Memory.Pinned.MaxItemChars,
		},
		EpisodicBackground: memory.EpisodicBackgroundOptions{
			Enabled:         *e.cfg.Memory.Episodic.Enabled,
			Interval:        e.cfg.Memory.Episodic.Interval,
			IdleAfter:       e.cfg.Memory.Episodic.IdleAfter,
			MinMessages:     e.cfg.Memory.Episodic.MinMessages,
			WindowSize:      e.cfg.Memory.Episodic.WindowSize,
			MaxWindows:      e.cfg.Memory.Episodic.MaxWindows,
			MaxWindowChars:  e.cfg.Memory.Episodic.MaxWindowChars,
			MaxWindowTokens: e.cfg.Memory.Episodic.MaxWindowTokens,
			MaxRetries:      e.cfg.Memory.Episodic.MaxRetries,
		},
		ReflectionBackground: memory.ReflectionBackgroundOptions{
			Enabled:      *e.cfg.Memory.Reflection.Enabled,
			Interval:     e.cfg.Memory.Reflection.Interval,
			Limit:        e.cfg.Memory.Reflection.Limit,
			RelatedLimit: e.cfg.Memory.Reflection.RelatedLimit,
		},
		PromotionBackground: memory.PromotionBackgroundOptions{
			Enabled:            *e.cfg.Memory.Promotion.Enabled,
			Interval:           e.cfg.Memory.Promotion.Interval,
			Limit:              e.cfg.Memory.Promotion.Limit,
			EvaluatedRetention: e.cfg.Memory.Promotion.EvaluatedRetention,
		},
	}
	provider, err := newMemoryProvider(e.cfg.Memory.Provider, opts)
	if err != nil {
		return err
	}
	if err := provider.ConfigureObservability(memoryobservability.New(log.Logger(), nil)); err != nil {
		return err
	}
	if background, ok := provider.(memory.BackgroundProvider); ok {
		backgroundCtx := guardrails.WithUnsafeEvidenceRecorder(e.ctx, e.unsafeEvidence)
		if err := background.StartBackground(backgroundCtx); err != nil {
			return err
		}
	}

	e.memory = provider
	return nil
}

func (e *environment) prepareTools() error {
	if e.stateMgr == nil {
		return errors.New("state manager is required")
	}

	if e.runtime == nil {
		e.runtime = NewRuntime(e.fileRoots(), e.commandPolicy(), e.stateMgr)
	}
	if err := e.prepareExecution(); err != nil {
		return err
	}
	e.runtime.commandShell = e.commandShell()
	e.runtime.memory = e.memory

	if err := e.tools.RegisterGroup(tools.Group{Name: "core"}); err != nil {
		return err
	}

	definitions := tools.Definitions{
		time.Definition(),
		automationtool.Definition(e.runtime),
		browsertool.Definition(e.runtime),
		listfiles.Definition(e.runtime),
		readfile.Definition(e.runtime),
		searchfiles.Definition(e.runtime),
		writefile.Definition(e.runtime),
		patch.Definition(e.runtime),
		plan.Definition(e.runtime),
		process.Definition(e.runtime),
		runcommand.Definition(e.runtime),
		sessionsearch.Definition(e.runtime),
		sessionmessages.Definition(e.runtime),
	}

	if definition, ok, err := e.memorySearchDefinition(); err != nil {
		return err
	} else if ok {
		definitions = append(definitions, definition)
	}
	if definition, ok, err := e.memoryExtractionDefinition(); err != nil {
		return err
	} else if ok {
		definitions = append(definitions, definition)
	}
	if writeDefinitions, err := e.memoryWriteDefinitions(); err != nil {
		return err
	} else {
		definitions = append(definitions, writeDefinitions...)
	}

	webProvider, err := webprovider.NewProvider(e.cfg)

	switch {
	case errors.Is(err, webprovider.ErrProviderNotConfigured),
		errors.Is(err, webprovider.ErrProviderCredential):
	case err != nil:
		return err
	default:
		websitePolicy := guardrails.NewWebsitePolicy(
			e.cfg.Web.BlockedDomainsEnabled,
			e.cfg.Web.BlockedDomains,
			e.cfg.Web.BlockedDomainFiles,
		)

		if e.cfg.Web.CacheTTL > 0 {
			webProvider = webprovider.NewCachedProvider(
				webProvider,
				webprovider.CacheOptions{
					ProviderName: e.cfg.Web.Provider,
					TTL:          e.cfg.Web.CacheTTL,
				},
			)
		}

		definitions = append(definitions,
			webextract.Definition(
				webProvider,
				webextract.Options{
					MaxExtractCharPerResult:        e.cfg.Web.MaxExtractCharPerResult,
					MinSummarizeChars:              e.cfg.Web.ExtractMinSummarizeChars,
					MaxSummaryChars:                e.cfg.Web.ExtractMaxSummaryChars,
					MaxSummaryChunkChars:           e.cfg.Web.ExtractMaxSummaryChunkChars,
					SummarizeRefusalThresholdChars: e.cfg.Web.ExtractRefusalThresholdChars,
					WebsitePolicy:                  websitePolicy,
				},
			),
		)
		providerValue := str.String(e.cfg.Web.Provider)
		webProviderName := providerValue.Normalized()
		if webProviderName != "" && webProviderName != webprovider.ProviderNative {
			definitions = append(definitions,
				websearch.Definition(
					webProvider,
					websearch.Options{
						WebsitePolicy: websitePolicy,
					},
				),
			)
		}
	}

	for _, definition := range definitions {
		if err := e.tools.Register(definition); err != nil {
			return err
		}

		e.addInstruction(definition.UsageInstruction)
	}

	return nil
}

func (e *environment) prepareExecution() error {
	if e == nil || e.cfg == nil || e.runtime == nil {
		return errors.New("execution configuration is required")
	}
	if e.runtime.execution != nil {
		return nil
	}
	activeProfile := profile.Active()
	profileName := activeProfile.Name
	if profileName == "" {
		profileName = profile.DefaultName
	}
	securityConfiguration := any(struct {
		Backend string
		Command guardrails.CommandPolicy
	}{e.cfg.Execution.Backend, e.commandPolicy()})
	if e.cfg.Execution.Backend == config.ExecutionBackendDocker {
		securityConfiguration = struct {
			Backend string
			Docker  config.DockerExecutionConfig
			Command guardrails.CommandPolicy
		}{e.cfg.Execution.Backend, e.cfg.Execution.Docker, e.commandPolicy()}
	}
	base := execution.ExposureInput{
		Backend:                 execution.BackendLocal,
		Scope:                   execution.ScopeSession,
		WorkspaceMode:           execution.WorkspaceReadWrite,
		Network:                 execution.NetworkBridge,
		SecurityGeneration:      getExecutionSecurityGeneration(securityConfiguration),
		Limits:                  getExecutionLimits(e.cfg.Execution.Docker.Limits),
		EnvironmentIdleExpiry:   e.cfg.Execution.Docker.EnvironmentIdleExpiry,
		SharedDisabledRetention: e.cfg.Execution.Docker.SharedDisabledRetention,
	}
	managerKey := strings.Join(
		[]string{
			profileName,
			base.SecurityGeneration,
			string(base.Backend),
			strings.Join(e.runtime.filePolicy.Roots, "\x00"),
		},
		"\x00",
	)
	serviceFactory := func() (execution.Service, error) {
		return executionlocal.New(e.runtime.filePolicy, e.runtime.processMgr), nil
	}
	if e.cfg.Execution.Backend == config.ExecutionBackendDocker {
		contract, contractErr := executiondocker.LoadImageContract(e.cfg.Execution.Docker.Contract)
		if contractErr != nil {
			return contractErr
		}
		mounts, mountErr := getExecutionMounts(e.cfg.Execution.Docker)
		if mountErr != nil {
			return mountErr
		}
		secretReferences := make(map[string]string, len(e.cfg.Execution.Docker.Secrets))
		resolverReferences := make(
			[]executiondocker.SecretReference,
			len(e.cfg.Execution.Docker.Secrets),
		)
		for index, secret := range e.cfg.Execution.Docker.Secrets {
			secretReferences[secret.Name] = secret.Description
			resolverReferences[index] = executiondocker.SecretReference{
				Name: secret.Name,
				Env:  secret.Env,
			}
		}
		secretResolver, resolverErr := executiondocker.NewSecretResolver(resolverReferences)
		if resolverErr != nil {
			return resolverErr
		}
		base.Backend = execution.BackendDocker
		base.Scope = execution.Scope(e.cfg.Execution.Docker.Scope)
		base.WorkspaceMode = execution.WorkspaceMode(e.cfg.Execution.Docker.Workspace.Mode)
		base.Mounts = mounts
		base.Network = execution.NetworkMode(e.cfg.Execution.Docker.Network)
		e.runtime.executionSecrets = secretReferences
		base.ImageDigest = e.cfg.Execution.Docker.Image
		base.ImageContractDigest = contract.Digest()
		base.PolicyHash = base.SecurityGeneration
		e.runtime.executionTarget = execution.CommandTarget{
			GOOS:        contract.GOOS,
			Shell:       contract.Shell,
			PATH:        contract.PATH,
			Executables: contract.Executables,
		}
		identityKey, keyErr := execution.LoadOrCreateIdentityKey(datadir.ExecutionIdentityKeyPath())
		if keyErr != nil {
			return keyErr
		}
		managerKey = strings.Join(
			[]string{profileName, base.SecurityGeneration, string(base.Backend)},
			"\x00",
		)
		serviceFactory = func() (execution.Service, error) {
			daemonIncarnation, incarnationErr := execution.NewIncarnation()
			if incarnationErr != nil {
				return nil, incarnationErr
			}
			return executiondocker.NewBackend(executiondocker.BackendOptions{
				Endpoint:            e.cfg.Execution.Docker.Endpoint,
				Image:               e.cfg.Execution.Docker.Image,
				Contract:            contract,
				DaemonIncarnation:   daemonIncarnation,
				SecretResolver:      secretResolver,
				ProcessIdentityKey:  identityKey,
				MaximumEnvironments: e.cfg.Execution.Docker.MaximumEnvironments,
				MaximumVolumes:      e.cfg.Execution.Docker.MaximumVolumes,
				ReservedFreeBytes:   e.cfg.Execution.Docker.ReservedFreeBytes,
				ConfiguredScope:     execution.Scope(e.cfg.Execution.Docker.Scope),
				SharedRetention:     e.cfg.Execution.Docker.SharedDisabledRetention,
				SharedDisabledMarker: filepath.Join(
					activeProfile.HomeDir,
					"execution",
					"shared-disabled-at",
				),
				SessionExists: func(ctx context.Context, sessionID string) (bool, error) {
					archived := false
					_, exists, getErr := e.stateMgr.Get(
						ctx,
						sessionID,
						storage.SessionGetOptions{Archived: &archived},
					)
					return exists, getErr
				},
				RecordLifecycle: func(sessionID string, event string, payload any) {
					session := e.NewTraceSession(sessionID)
					session.Record(event, payload)
					session.Close()
				},
			})
		}
	}
	service, err := execution.AcquireManager(managerKey, serviceFactory)
	if err != nil {
		return err
	}
	e.runtime.execution = service
	e.runtime.executionBase = base
	e.runtime.profile = profileName
	return nil
}

func (e *environment) Close() error {
	if e == nil || e.runtime == nil || e.runtime.execution == nil {
		return nil
	}
	return e.runtime.execution.Close(context.Background())
}

func (e *environment) CloseSession(
	ctx context.Context,
	sessionID string,
	removeWorkspace bool,
) error {
	if e == nil || e.runtime == nil || e.runtime.execution == nil {
		return nil
	}
	return e.runtime.execution.CloseSession(ctx, e.runtime.profile, sessionID, removeWorkspace)
}

func (e *environment) CloseExecutionOwner(ctx context.Context) error {
	if e == nil || e.runtime == nil || e.runtime.execution == nil {
		return nil
	}
	owner, err := e.runtime.getExecutionOwner(ctx)
	if err != nil {
		return err
	}
	return e.runtime.execution.CloseOwner(ctx, owner)
}

func (e *environment) ListExecutionEnvironments(
	ctx context.Context,
) ([]execution.EnvironmentDetails, error) {
	if e == nil || e.runtime == nil {
		return nil, errors.New("execution runtime is required")
	}
	return e.runtime.ListExecutionEnvironments(ctx)
}

func getExecutionLimits(limits config.ExecutionLimitsConfig) execution.Limits {
	return execution.Limits{
		MemoryBytes:       limits.MemoryBytes,
		CPUMilli:          limits.CPUMilli,
		PIDs:              limits.PIDs,
		OpenFiles:         limits.OpenFiles,
		TemporaryBytes:    limits.TemporaryBytes,
		OutputBytes:       limits.OutputBytes,
		ControlInputBytes: limits.ControlInputBytes,
		Runtime:           limits.Runtime,
		StopGrace:         limits.StopGrace,
	}
}

func getExecutionMounts(cfg config.DockerExecutionConfig) ([]execution.Mount, error) {
	mounts := make([]execution.Mount, 0, len(cfg.Mounts)+1)
	if cfg.Workspace.Mode != config.ExecutionWorkspaceNone {
		source, err := filepath.EvalSymlinks(filepath.Clean(cfg.Workspace.Source))
		if err != nil {
			return nil, err
		}
		mounts = append(mounts, execution.Mount{
			Name:           "workspace",
			SourceIdentity: source,
			Target:         "/workspace",
			Mode:           execution.MountMode(cfg.Workspace.Mode),
			Purpose:        "primary workspace",
		})
	}
	for _, configured := range cfg.Mounts {
		source, err := getConfiguredMountIdentity(configured.Source, configured.Create)
		if err != nil {
			return nil, err
		}
		mounts = append(mounts, execution.Mount{
			Name:           configured.Name,
			SourceIdentity: source,
			Target:         configured.Target(),
			Mode:           execution.MountMode(configured.Mode),
			Create:         configured.Create,
			Purpose:        configured.Purpose,
		})
	}
	for left := 0; left < len(mounts); left++ {
		for right := left + 1; right < len(mounts); right++ {
			leftSource := filepath.Clean(mounts[left].SourceIdentity)
			rightSource := filepath.Clean(mounts[right].SourceIdentity)
			if leftSource == rightSource ||
				strings.HasPrefix(leftSource, rightSource+string(filepath.Separator)) ||
				strings.HasPrefix(rightSource, leftSource+string(filepath.Separator)) {
				return nil, errors.New("execution mount sources must not overlap")
			}
		}
	}
	return mounts, nil
}

func getConfiguredMountIdentity(source string, create bool) (string, error) {
	cleaned := filepath.Clean(source)
	canonical, err := filepath.EvalSymlinks(cleaned)
	if err == nil || !create || !os.IsNotExist(err) {
		return canonical, err
	}

	ancestor := cleaned
	missing := make([]string, 0)
	for {
		if _, statErr := os.Lstat(ancestor); statErr == nil {
			break
		} else if !os.IsNotExist(statErr) {
			return "", statErr
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", err
		}
		missing = append(missing, filepath.Base(ancestor))
		ancestor = parent
	}
	resolvedAncestor, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return "", err
	}
	for index := len(missing) - 1; index >= 0; index-- {
		resolvedAncestor = filepath.Join(resolvedAncestor, missing[index])
	}
	return filepath.Clean(resolvedAncestor), nil
}

func (e *environment) memorySearchDefinition() (tools.Definition, bool, error) {
	if e == nil || e.runtime == nil {
		return tools.Definition{}, false, nil
	}

	ok, err := e.runtime.SupportsMemorySearch(e.ctx)
	if err != nil {
		return tools.Definition{}, false, err
	}
	if !ok {
		return tools.Definition{}, false, nil
	}

	return memorysearch.Definition(e.runtime), true, nil
}

func (e *environment) memoryExtractionDefinition() (tools.Definition, bool, error) {
	if e == nil || e.runtime == nil {
		return tools.Definition{}, false, nil
	}

	ok, err := e.runtime.SupportsMemoryExtraction(e.ctx)
	if err != nil {
		return tools.Definition{}, false, err
	}
	if !ok {
		return tools.Definition{}, false, nil
	}

	return memoryextract.Definition(e.runtime), true, nil
}

func (e *environment) memoryWriteDefinitions() (tools.Definitions, error) {
	if e == nil || e.runtime == nil || e.cfg == nil {
		return nil, nil
	}
	if !e.cfg.MemoryWriteEnabled() {
		return nil, nil
	}

	ok, err := e.runtime.SupportsMemoryWrite(e.ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	return tools.Definitions{
		memorywrite.AddDefinition(e.runtime),
		memorywrite.UpdateDefinition(e.runtime),
		memorywrite.DeleteDefinition(e.runtime),
	}, nil
}

func (e *environment) prepareInstructions() {
	e.safetyEvents = nil
	e.addInstruction(instructions.BuildPlanningPolicy())

	for _, instruction := range instructions.BuildBase(e.cfg.Name) {
		e.addInstruction(instruction)
	}

	personalityOverlay, err := loadPersonality(e.personalityLoadOptions())
	if err != nil {
		log.Warn().Err(err).Msg("Failed to load personality overlays")
	} else if personalityOverlay.Found {
		e.addInstruction(instructions.Instruction{Value: personalityOverlay.Content})
	}
	e.safetyEvents = append(e.safetyEvents, personalityOverlay.SafetyEvents...)
	e.captureLoadedUnsafeEvidence(personalityOverlay.UnsafeEvidence)

	workspaceRules, err := loadWorkspaceRules(e.cfg.Rules.Files...)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to load workspace rules")
		return
	}

	if workspaceRules.Found {
		e.workspace = workspaceRules
		e.addInstruction(instructions.Instruction{Value: workspaceRules.Content})
	}
	e.safetyEvents = append(e.safetyEvents, workspaceRules.SafetyEvents...)
	e.captureLoadedUnsafeEvidence(workspaceRules.UnsafeEvidence)

	if e.cfg != nil && e.cfg.Session.Instruct != "" {
		e.setInstruction(
			instructions.Instruction{
				Name:  configInstructInstructionName,
				Value: e.cfg.Session.Instruct,
			},
		)
	}
}

func (e *environment) captureLoadedUnsafeEvidence(events []guardrails.UnsafeEvidence) {
	if e == nil || e.unsafeEvidence == nil {
		return
	}
	for _, event := range events {
		guardrails.RetainUnsafeEvidence(e.ctx, e.unsafeEvidence, event)
	}
}

func (e *environment) personalityLoadOptions() personality.LoadOptions {
	return personality.LoadOptions{
		ProfileHome:    datadir.HomeDir(),
		AllowWorkspace: true,
	}
}

func (e *environment) Instructions() instructions.Instructions {
	if e == nil {
		return nil
	}

	copied := make(instructions.Instructions, len(e.instructions))
	copy(copied, e.instructions)
	return copied
}

func (e *environment) SafetyTraceEvents() []guardrails.SafetyTracePayloadOptions {
	if e == nil || len(e.safetyEvents) == 0 {
		return nil
	}

	events := make([]guardrails.SafetyTracePayloadOptions, len(e.safetyEvents))
	copy(events, e.safetyEvents)
	return events
}

func (e *environment) UnsafeEvidenceRecorder() guardrails.UnsafeEvidenceRecorder {
	if e == nil {
		return nil
	}
	return e.unsafeEvidence
}

func (e *environment) Tools() ToolRegistry {
	return e.tools
}

func (e *environment) NewIterationBudget() budget.IterationBudget {
	if e == nil || e.cfg == nil || e.cfg.Session.MaxIterations <= 0 {
		return budget.New(constants.DefaultMaxIterations)
	}

	return budget.New(e.cfg.Session.MaxIterations)
}

func (e *environment) NewTraceSession(sessionID string) trace.Session {
	if e == nil || e.traces == nil {
		return trace.NoopSession()
	}

	metadata := trace.Metadata{Source: "agent"}
	if e.cfg != nil {
		metadata.AgentName = e.cfg.Name
		metadata.Model = e.cfg.Models.Main.Name
		metadata.API = e.cfg.Models.Main.API
	}
	metadata.PublicSessionID = sessionID
	metadata.EffectiveSessionID = sessionID

	return e.openTraceSession(sessionID, metadata)
}

func (e *environment) NewTraceSessionForRun(runCtx runcontext.Context) trace.Session {
	if e == nil || e.traces == nil {
		return trace.NoopSession()
	}

	runCtx, err := runCtx.Normalize()
	if err != nil {
		return trace.NoopSession()
	}

	metadata := trace.Metadata{Source: "agent"}
	if e.cfg != nil {
		metadata.AgentName = e.cfg.Name
		metadata.Model = e.cfg.Models.Main.Name
		metadata.API = e.cfg.Models.Main.API
	}
	metadata.PublicSessionID = runCtx.Session.PublicID
	metadata.EffectiveSessionID = runCtx.Session.EffectiveID
	metadata.ChildSessionID = runCtx.Lineage.ChildSessionID
	metadata.ParentSessionID = runCtx.Lineage.ParentSessionID
	metadata.RunID = runCtx.Lineage.RunID
	metadata.PersonalityName = runCtx.Personality.Name
	metadata.StateMode = runCtx.State.Mode
	metadata.SourceProfile = runCtx.ProfileName
	if !runCtx.Lineage.SpawnedAt.IsZero() {
		metadata.SpawnedAt = &runCtx.Lineage.SpawnedAt
	}
	if !runCtx.Lineage.CompletedAt.IsZero() {
		metadata.CompletedAt = &runCtx.Lineage.CompletedAt
	}

	return e.openTraceSession(runCtx.StateSessionID(), metadata)
}

func (e *environment) openTraceSession(sessionID string, metadata trace.Metadata) trace.Session {
	session := e.traces.OpenSession(e.ctx, sessionID, metadata)
	if e.workspace.Truncated {
		session.Record(trace.EvtWorkspaceRulesTruncated, trace.WorkspaceRulesTruncatedPayload{
			OriginalLength:   e.workspace.OriginalLength,
			TruncatedLength:  e.workspace.TruncatedLength,
			MaxContentLength: e.workspace.MaxContentLength,
			Marker:           e.workspace.TruncationMarker,
		})
	}

	return session
}

func (e *environment) CurrentPlan(sessionID string) planstore.Plan {
	if e == nil || e.runtime == nil {
		return planstore.Plan{}
	}

	return e.runtime.GetPlan(sessionID)
}

func (e *environment) HydratePlan(sessionID string, plan planstore.Plan) {
	if e == nil || e.runtime == nil {
		return
	}

	e.runtime.HydratePlan(sessionID, plan)
}

func (e *environment) SetStateManager(manager *statemanager.Manager) {
	if e == nil {
		return
	}

	e.stateMgr = manager
	if e.runtime != nil {
		e.runtime.stateMgr = manager
	}
}

func (e *environment) SetApprovalService(service permissions.Approver) {
	if e == nil {
		return
	}

	if registry, ok := e.tools.(*tools.DefaultRegistry); ok {
		registry.SetApprovalService(service)
	}
}

func (e *environment) SetAutomationService(service envtypes.AutomationService) {
	if e == nil {
		return
	}

	if e.runtime == nil {
		e.runtime = NewRuntime(e.fileRoots(), e.commandPolicy(), e.stateMgr)
	}
	e.runtime.commandShell = e.commandShell()
	e.runtime.automation = service
}

func (e *environment) SetBrowserService(service envtypes.BrowserService) {
	if e == nil {
		return
	}
	if e.runtime == nil {
		e.runtime = NewRuntime(e.fileRoots(), e.commandPolicy(), e.stateMgr)
	}
	e.runtime.browser = service
}

func (e *environment) SetCommandIdentityKey(key []byte) {
	if e == nil {
		return
	}
	if e.runtime == nil {
		e.runtime = NewRuntime(e.fileRoots(), e.commandPolicy(), e.stateMgr)
	}
	e.runtime.setCommandIdentityKey(key)
}

func (e *environment) SetModelClient(client models.Client) {
	if e == nil {
		return
	}

	e.modelClient = client
}

func (e *environment) fileRoots() []string {
	if e == nil || e.cfg == nil || len(e.cfg.FS.Roots) == 0 {
		return guardrails.NormalizeRoots(nil)
	}

	return guardrails.NormalizeRoots(e.cfg.FS.Roots)
}

func (e *environment) commandPolicy() guardrails.CommandPolicy {
	if e == nil || e.cfg == nil {
		return guardrails.CommandPolicy{}
	}

	return guardrails.CommandPolicy{
		AllowCommands: e.cfg.Exec.AllowCommands,
		AskCommands:   e.cfg.Exec.AskCommands,
		DenyCommands:  e.cfg.Exec.DenyCommands,
	}.Normalize()
}

func (e *environment) commandShell() string {
	if e == nil || e.cfg == nil {
		return ""
	}
	return e.cfg.Exec.Shell
}

func (e *environment) addInstruction(instruction instructions.Instruction) {
	value := str.String(instruction.Value)
	if value.Trim() == "" {
		return
	}

	e.instructions = append(e.instructions, instruction)
}

func (e *environment) setInstruction(instruction instructions.Instruction) {
	nameValue := str.String(instruction.Name)
	instruction.Name = nameValue.Trim()
	value2 := str.String(instruction.Value)
	instruction.Value = value2.Trim()

	if instruction.Name == "" {
		e.addInstruction(instruction)
		return
	}

	for idx, existing := range e.instructions {
		if existing.Name != instruction.Name {
			continue
		}

		if instruction.Value == "" {
			e.instructions = append(e.instructions[:idx], e.instructions[idx+1:]...)
			return
		}

		e.instructions[idx] = instruction
		return
	}

	if instruction.Value != "" {
		e.instructions = append(e.instructions, instruction)
	}
}

var _ Environment = (*environment)(nil)
