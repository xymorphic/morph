package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	agentsummary "github.com/wandxy/morph/internal/agent/context/summary"
	"github.com/wandxy/morph/internal/agent/runcontext"
	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/constants"
	"github.com/wandxy/morph/internal/environment"
	envtypes "github.com/wandxy/morph/internal/environment/types"
	"github.com/wandxy/morph/internal/guardrails"
	models "github.com/wandxy/morph/internal/model"
	"github.com/wandxy/morph/internal/permissions"
	"github.com/wandxy/morph/internal/profile"
	storage "github.com/wandxy/morph/internal/state/core"
	statemanager "github.com/wandxy/morph/internal/state/manager"
	"github.com/wandxy/morph/internal/state/search"
	"github.com/wandxy/morph/internal/tools"
	webextract "github.com/wandxy/morph/internal/tools/webextract"
	"github.com/wandxy/morph/internal/trace"
	agentcore "github.com/wandxy/morph/pkg/agent"
	morphmsg "github.com/wandxy/morph/pkg/agent/message"
	pkgcache "github.com/wandxy/morph/pkg/cache"
	"github.com/wandxy/morph/pkg/gateway/pairing"
	"github.com/wandxy/morph/pkg/logutils"
	"github.com/wandxy/morph/pkg/str"
)

var jsonMarshal = json.Marshal

var listModelOptions = models.ListOptions

const defaultRecallSummaryCacheTTL = constants.DefaultRecallSummaryCacheTTL

const (
	EventKindTextDelta = agentcore.EventKindTextDelta
	EventKindTrace     = agentcore.EventKindTrace
)

var agentLog = logutils.Module("agent")

var runRecallSessionSummary = func(
	service *agentsummary.Service,
	ctx context.Context,
	session storage.Session,
	traceSession trace.Session,
) (*agentsummary.SummaryState, error) {
	return service.RecallSessionSummary(ctx, session, traceSession)
}

var newRecallSummaryCache = func() *pkgcache.Cache[string, storage.SessionSummary] {
	return pkgcache.New(pkgcache.Options[string, storage.SessionSummary]{
		TTL: defaultRecallSummaryCacheTTL,
		Clone: func(summary storage.SessionSummary) storage.SessionSummary {
			return storage.CloneSessionSummary(summary)
		},
	})
}

// Agent coordinates agent lifecycle, sessions, and turn execution.
type Agent struct {
	ctx                context.Context
	cfg                *config.Config
	core               *agentcore.Agent
	modelClient        models.Client
	summaryClient      models.Client
	rerankerClient     models.Client
	env                environment.Environment
	stateMgr           *statemanager.Manager
	recallSummaryCache *pkgcache.Cache[string, storage.SessionSummary]
	turnCoordinator    TurnCoordinator
	turnScope          string
	turnMessages       []morphmsg.Message
	approvalService    *permissions.ApprovalService
	authorizationMu    sync.RWMutex
	sessionAuth        map[string]permissions.AuthorizationContext
	initialized        bool
}

// NewAgent returns an Agent with its runtime dependencies wired in.
// When optional clients are missing, summary and reranker calls reuse modelClient.
func NewAgent(ctx context.Context, cfg *config.Config, modelClient models.Client, optionalClients ...models.Client) *Agent {
	var summaryClient models.Client
	if len(optionalClients) > 0 {
		summaryClient = optionalClients[0]
	}
	if summaryClient == nil {
		summaryClient = modelClient
	}
	var rerankerClient models.Client
	if len(optionalClients) > 1 {
		rerankerClient = optionalClients[1]
	}
	if rerankerClient == nil {
		rerankerClient = summaryClient
	}

	return &Agent{
		ctx:                ctx,
		cfg:                cfg,
		modelClient:        modelClient,
		summaryClient:      summaryClient,
		rerankerClient:     rerankerClient,
		recallSummaryCache: newRecallSummaryCache(),
		turnCoordinator:    defaultTurnCoordinator,
		turnScope:          getTurnCoordinationScope(),
	}
}

func (a *Agent) SetTurnCoordinator(coordinator TurnCoordinator, scope string) {
	if a == nil {
		return
	}

	if coordinator == nil {
		coordinator = defaultTurnCoordinator
	}
	a.turnCoordinator = coordinator
	a.turnScope = str.String(scope).Trim()
}

// Start opens state, prepares the runtime environment, and marks the agent ready for requests.
func (a *Agent) Start(ctx context.Context) error {
	if a == nil {
		return errors.New("agent is required")
	}
	if a.cfg == nil {
		return errors.New("config is required")
	}

	ctx = normalizeContext(ctx)
	a.ctx = ctx
	var err error

	// State is started before environment preparation because tools may need
	// session, trace, memory, or vector-store access during Prepare.
	if err = a.ensureStateManager(); err != nil {
		return err
	}

	if err = a.stateMgr.Start(ctx); err != nil {
		return err
	}

	if store, ok := a.stateMgr.PermissionStore(); ok {
		a.approvalService, err = permissions.NewApprovalService(store, permissions.ApprovalOptions{
			Auditor:          permissionApprovalAuditor{},
			RequestRetention: a.cfg.Permissions.RequestRetention,
			GrantRetention:   a.cfg.Permissions.GrantRetention,
			CleanupInterval:  a.cfg.Permissions.CleanupInterval,
			CleanupBatchSize: a.cfg.Permissions.CleanupBatchSize,
			RateLimit:        a.cfg.Permissions.ApprovalRateLimit,
			RateWindow:       a.cfg.Permissions.ApprovalRateWindow,
		})
		if err != nil {
			return err
		}
		if err := a.approvalService.Recover(context.WithoutCancel(ctx)); err != nil {
			return err
		}
		a.approvalService.StartCleanup(ctx)
	}

	// Environment setup wires the durable state and summary model client into
	// tools so they can execute with the same runtime identity as the agent.
	a.env = NewEnvironment(ctx, a.cfg)
	a.env.SetStateManager(a.stateMgr)
	a.env.SetApprovalService(a.approvalService)
	a.env.SetModelClient(a.summaryClient)
	if err := a.env.Prepare(); err != nil {
		return err
	}

	a.core, err = a.buildCoreAgent()
	if err != nil {
		return err
	}

	a.turnMessages = nil
	a.initialized = true

	agentLog.Info().Msg("agent started")

	return nil
}

type permissionApprovalAuditor struct{}

func (permissionApprovalAuditor) ApprovalChanged(ctx context.Context, request permissions.ApprovalRequest) {
	recorder := tools.TraceRecorderFromContext(ctx)
	if recorder == nil {
		return
	}
	effects := make([]string, len(request.Effects))
	for index, effect := range request.Effects {
		effects[index] = string(effect)
	}
	recorder.Record(trace.EvtPermissionApprovalChanged, trace.PermissionApprovalPayload{
		RequestID:  request.ID,
		Status:     string(request.Status),
		Scope:      string(request.Scope),
		Tool:       request.Tool,
		Resource:   string(request.Resource),
		Action:     string(request.Action),
		Effects:    effects,
		Summary:    request.Summary,
		Reason:     request.Reason,
		Operations: slices.Clone(request.Operations),
		ExpiresAt:  request.ExpiresAt,
	})
}

func (a *Agent) ApprovalService() *permissions.ApprovalService {
	if a == nil {
		return nil
	}

	return a.approvalService
}

func (a *Agent) SetAutomationService(service envtypes.AutomationService) {
	if a == nil || a.env == nil {
		return
	}

	a.env.SetAutomationService(service)
}

func (a *Agent) SetBrowserService(service envtypes.BrowserService) {
	if a == nil || a.env == nil {
		return
	}

	a.env.SetBrowserService(service)
}

func (a *Agent) SetCommandIdentityKey(key []byte) {
	if a == nil || a.env == nil {
		return
	}
	a.env.SetCommandIdentityKey(key)
}

func (a *Agent) ListProviders(context.Context) (ProviderList, error) {
	if a == nil {
		return ProviderList{}, errors.New("agent is required")
	}
	if a.cfg == nil {
		return ProviderList{}, errors.New("config is required")
	}

	auth := make(map[string]string)
	currentProviderModels, err := listModelOptions(models.OptionQuery{Provider: a.cfg.Models.Main.Provider})
	if err != nil {
		return ProviderList{}, err
	}
	auth[a.cfg.Models.Main.Provider] = a.getProviderAuthTypeForModelList(a.cfg.Models.Main.Provider, currentProviderModels)
	for _, provider := range models.ListProviders(models.ProviderQuery{Current: a.cfg.Models.Main.Provider}) {
		if _, ok := auth[provider.ID]; ok {
			continue
		}

		providerModels, err := listModelOptions(models.OptionQuery{Provider: provider.ID})
		if err != nil {
			return ProviderList{}, err
		}
		auth[provider.ID] = a.getProviderAuthTypeForModelList(provider.ID, providerModels)
	}

	return ProviderList{
		Providers: models.ListProviders(models.ProviderQuery{
			Current: a.cfg.Models.Main.Provider,
			Auth:    auth,
		}),
	}, nil
}

func (a *Agent) ListModels(_ context.Context, opts ...ModelListOptions) (ModelList, error) {
	if a == nil {
		return ModelList{}, errors.New("agent is required")
	}
	if a.cfg == nil {
		return ModelList{}, errors.New("config is required")
	}

	providerValue := str.String(getModelListOptions(opts...).Provider)
	provider := providerValue.Normalized()
	if provider == "" {
		provider = a.cfg.Models.Main.Provider
	}
	providerValue2 := str.String(provider)
	provider = providerValue2.Normalized()
	if provider == "" {
		return ModelList{}, errors.New("model provider is required")
	}

	modelsForProvider, err := listModelOptions(models.OptionQuery{
		Provider: provider,
		Current:  a.getCurrentModelForProvider(provider),
	})
	if err != nil {
		return ModelList{}, err
	}
	if len(modelsForProvider) == 0 {
		return ModelList{}, fmt.Errorf("model provider %q is not available", provider)
	}
	authType := a.getProviderAuthTypeForModelList(provider, modelsForProvider)
	if authType == "oauth" {
		modelsForProvider, err = listModelOptions(models.OptionQuery{
			Provider:  provider,
			Current:   a.getCurrentModelForProvider(provider),
			OAuthOnly: true,
		})
		if err != nil {
			return ModelList{}, err
		}
	}

	return ModelList{
		Provider: provider,
		AuthType: authType,
		Models:   modelsForProvider,
	}, nil
}

func getModelListOptions(opts ...ModelListOptions) ModelListOptions {
	if len(opts) == 0 {
		return ModelListOptions{}
	}

	return opts[0]
}

func getModelSelectOptions(opts ...ModelSelectOptions) ModelSelectOptions {
	if len(opts) == 0 {
		return ModelSelectOptions{}
	}

	return opts[0]
}

func (a *Agent) getProviderAuthTypeForModelList(provider string, options []models.Option) string {
	for _, option := range options {
		if authType := a.getProviderAuthType(provider, option.ID); authType != "none" {
			return authType
		}
	}

	return "none"
}

func (a *Agent) getProviderAuthType(provider string, modelID string) string {
	if a == nil || a.cfg == nil {
		return "none"
	}

	cfg := *a.cfg
	cfg.Models.Providers = cloneAgentProviderModelConfigs(a.cfg.Models.Providers)
	providerValue3 := str.String(provider)
	cfg.Models.Main.Provider = providerValue3.Normalized()
	modelIDValue := str.String(modelID)
	cfg.Models.Main.Name = modelIDValue.Trim()
	providerValue4 := str.String(a.cfg.Models.Main.Provider)
	if cfg.Models.Main.Provider != providerValue4.Normalized() {
		cfg.Models.Main.APIKey = ""
	}

	auth, err := cfg.ResolveModelAuth()
	if err != nil {
		return "none"
	}

	return auth.AuthType()
}

func (a *Agent) getCurrentModelForProvider(provider string) string {
	if a == nil || a.cfg == nil {
		return ""
	}

	providerValue5 := str.String(provider)
	providerValue6 := str.String(a.cfg.Models.Main.Provider)
	if providerValue5.Normalized() != providerValue6.Normalized() {
		return ""
	}

	return a.cfg.Models.Main.Name
}

func cloneAgentProviderModelConfigs(values map[string]config.ProviderModelConfig) map[string]config.ProviderModelConfig {
	if len(values) == 0 {
		return nil
	}

	cloned := make(map[string]config.ProviderModelConfig, len(values))
	for key, value := range values {
		value.APIKeyEnv = append([]string(nil), value.APIKeyEnv...)
		cloned[key] = value
	}

	return cloned
}

func (a *Agent) SelectModel(ctx context.Context, id string, opts ...ModelSelectOptions) (models.Option, error) {
	if a == nil {
		return models.Option{}, errors.New("agent is required")
	}

	idValue := str.String(id)
	id = idValue.Trim()
	if id == "" {
		return models.Option{}, errors.New("model id is required")
	}
	providerValue7 := str.String(getModelSelectOptions(opts...).Provider)
	provider := providerValue7.Normalized()
	list, err := a.ListModels(ctx, ModelListOptions{Provider: provider})
	if err != nil {
		return models.Option{}, err
	}

	var selected models.Option
	for _, option := range list.Models {
		iDValue := str.String(option.ID)
		if iDValue.Trim() == id {
			selected = option
			selected.Current = true
			break
		}
	}
	iDValue2 := str.String(selected.ID)
	if iDValue2.Trim() == "" {
		return models.Option{}, fmt.Errorf("model %q is not available for provider %q with %s auth",
			id, list.Provider, list.AuthType)
	}
	if err := a.checkModelSelectionAuth(list.Provider, selected); err != nil {
		return models.Option{}, err
	}

	active := profile.WithMetadataPaths(profile.Active())
	if err := saveMainModelSelection(active.EnvPath, active.ConfigPath, list.Provider, selected); err != nil {
		return models.Option{}, err
	}

	return selected, nil
}

func (a *Agent) checkModelSelectionAuth(provider string, selected models.Option) error {
	cfg := *a.cfg
	cfg.Models.Providers = cloneAgentProviderModelConfigs(a.cfg.Models.Providers)
	providerValue8 := str.String(provider)
	cfg.Models.Main.Provider = providerValue8.Normalized()
	iDValue3 := str.String(selected.ID)
	cfg.Models.Main.Name = iDValue3.Trim()
	aPIValue := str.String(selected.API)
	cfg.Models.Main.API = aPIValue.Trim()
	providerValue9 := str.String(a.cfg.Models.Main.Provider)
	if cfg.Models.Main.Provider != providerValue9.Normalized() {
		cfg.Models.Main.APIKey = ""
	}

	if _, err := cfg.ResolveModelAuth(); err != nil {
		return err
	}

	return nil
}

func (a *Agent) SetProviderAPIKey(_ context.Context, provider string, apiKey string) error {
	if a == nil {
		return errors.New("agent is required")
	}
	if a.cfg == nil {
		return errors.New("config is required")
	}

	providerValue10 := str.String(provider)
	provider = providerValue10.Normalized()
	if provider == "" {
		return errors.New("model provider is required")
	}
	apiKeyValue := str.String(apiKey)
	apiKey = apiKeyValue.Trim()
	if apiKey == "" {
		return errors.New("provider API key is required")
	}

	active := profile.WithMetadataPaths(profile.Active())
	if err := saveProviderAPIKey(active.EnvPath, active.ConfigPath, provider, apiKey); err != nil {
		return err
	}

	if a.cfg.Models.Providers == nil {
		a.cfg.Models.Providers = make(map[string]config.ProviderModelConfig)
	}
	providerConfig := a.cfg.Models.Providers[provider]
	providerConfig.APIKey = apiKey
	a.cfg.Models.Providers[provider] = providerConfig

	return nil
}

func saveMainModelSelection(envPath string, configPath string, provider string, option models.Option) error {
	configPathValue := str.String(configPath)
	configPath = configPathValue.Trim()
	if configPath == "" {
		return errors.New("profile config path is required")
	}
	providerValue11 := str.String(provider)
	provider = providerValue11.Normalized()
	if provider == "" {
		return errors.New("model provider is required")
	}
	iDValue4 := str.String(option.ID)
	modelID := iDValue4.Trim()
	if modelID == "" {
		return errors.New("model id is required")
	}
	aPIValue2 := str.String(option.API)
	api := aPIValue2.Trim()
	if _, err := config.SetConfigValues(envPath, configPath, []config.ConfigUpdate{
		{Path: "models.main.provider", Value: provider},
		{Path: "models.main.name", Value: modelID},
		{Path: "models.main.api", Value: api},
		{Path: "models.summary.provider", Value: provider},
		{Path: "models.summary.name", Value: modelID},
		{Path: "models.summary.api", Value: api},
	}); err != nil {
		return err
	}

	return nil
}

func saveProviderAPIKey(envPath string, configPath string, provider string, apiKey string) error {
	configPathValue2 := str.String(configPath)
	configPath = configPathValue2.Trim()
	if configPath == "" {
		return errors.New("profile config path is required")
	}
	providerValue12 := str.String(provider)
	provider = providerValue12.Normalized()
	if provider == "" {
		return errors.New("model provider is required")
	}
	apiKeyValue2 := str.String(apiKey)
	apiKey = apiKeyValue2.Trim()
	if apiKey == "" {
		return errors.New("provider API key is required")
	}

	_, err := config.SetConfigValues(envPath, configPath, []config.ConfigUpdate{
		{Path: "models.providers." + provider + ".apiKey", Value: apiKey},
	})

	return err
}

func (a *Agent) buildCoreAgent() (*agentcore.Agent, error) {
	if a == nil {
		return nil, errors.New("agent is required")
	}

	return agentcore.NewAgent(agentcore.Options{
		Model:          a.cfg.Models.Main.Name,
		API:            a.cfg.MainModelAPIEffective(),
		ModelClient:    a.modelClient,
		SessionStore:   NewSessionStore(a.stateMgr),
		ToolRegistry:   NewToolRegistry(a.env, a.invokeToolWithEnvironment),
		ToolPolicy:     ToolPolicyFromEnvironment(a.env),
		PromptProvider: NewPromptProvider(a.env),
		DebugRequests:  a.cfg.Debug.Requests,
	})
}

// Close flushes memory candidates when configured and then closes state resources.
func (a *Agent) Close() error {
	if a == nil || a.stateMgr == nil {
		return nil
	}

	if !a.shouldFlushMemoryBeforeContextLoss() {
		return a.stateMgr.Close()
	}

	ctx := normalizeContext(a.ctx)
	sessionID, err := a.stateMgr.CurrentSession(ctx)
	sessionIDValue := str.String(sessionID)
	if err == nil && sessionIDValue.Trim() != "" {
		traceSession := trace.NoopSession()
		if a.env != nil {
			traceSession = a.openTraceSessionForSession(sessionID)
		}
		if authorization, ok := a.getSessionAuthorization(sessionID); ok {
			ctx = permissions.WithContext(ctx, authorization)
			a.maybeFlushMemoryBeforeContextLoss(ctx, sessionID, memoryFlushTriggerControlledExit, traceSession)
		} else {
			recordMemoryFlushSkipped(
				traceSession,
				memoryFlushTriggerControlledExit,
				"missing_session_authorization",
			)
		}
		traceSession.Close()
	}

	return a.stateMgr.Close()
}

// Respond executes one user turn in the active or requested session.
func (a *Agent) Respond(ctx context.Context, msg string, opts agentcore.RespondOptions) (string, error) {
	if a == nil {
		return "", errors.New("agent is required")
	}
	if a.cfg == nil {
		return "", errors.New("config is required")
	}
	if a.modelClient == nil {
		return "", errors.New("model client is required")
	}

	msgValue := str.String(msg)
	if msgValue.Trim() == "" {
		return "", errors.New("message is required")
	}

	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if !a.initialized || a.stateMgr == nil || a.env == nil {
		return "", errors.New("environment has not been initialized")
	}

	if a.env.Tools() == nil {
		return "", errors.New("tool registry is required")
	}

	sessionID := str.String(opts.SessionID).Trim()
	if sessionID == "" {
		sessionID = storage.DefaultSessionID
	} else if err := storage.ValidateSessionID(sessionID); err != nil {
		return "", err
	}
	coordinator := a.turnCoordinator
	if coordinator == nil {
		coordinator = defaultTurnCoordinator
	}
	release, err := coordinator.Acquire(ctx, a.turnScope, sessionID)
	if err != nil {
		return "", err
	}
	defer release()
	session, err := NewSessionStore(a.stateMgr).Resolve(ctx, sessionID)
	if err != nil {
		return "", err
	}
	opts.SessionID = session.ID
	profileName := str.String(profile.Active().Name).Trim()
	authorization, ok := permissions.FromContext(ctx)
	if !ok {
		authorization = permissions.AuthorizationContext{
			Actor:     permissions.Actor{Kind: permissions.ActorLocalOwner},
			Surface:   permissions.SurfaceCLI,
			Profile:   profileName,
			SessionID: session.ID,
		}
	} else {
		authorization.Profile = profileName
		authorization.SessionID = session.ID
	}
	ctx = permissions.WithContext(ctx, authorization)
	a.setSessionAuthorization(session.ID, authorization)

	agentLog.Info().Str("session_id", opts.SessionID).
		Str("model", a.cfg.Models.Main.Name).
		Msg("responding to user message")

	// Turn owns per-response state such as loaded history, retrieved memory,
	// request instruction overrides, streaming callbacks, and emitted messages.
	turn := a.newTurn(a.env, a.invokeToolWithEnvironment)
	reply, err := turn.Run(ctx, msg, opts)
	a.turnMessages = turn.Messages()
	if err == nil {
		a.maybeGenerateSessionTitle(ctx, turn.sessionID)
	}

	return reply, err
}

func (a *Agent) setSessionAuthorization(
	sessionID string,
	authorization permissions.AuthorizationContext,
) {
	if a == nil {
		return
	}

	normalized, err := authorization.Normalize()
	if err != nil || normalized.SessionID != sessionID {
		return
	}

	a.authorizationMu.Lock()
	defer a.authorizationMu.Unlock()

	if a.sessionAuth == nil {
		a.sessionAuth = make(map[string]permissions.AuthorizationContext)
	}
	a.sessionAuth[sessionID] = normalized
}

func (a *Agent) getSessionAuthorization(
	sessionID string,
) (permissions.AuthorizationContext, bool) {
	if a == nil {
		return permissions.AuthorizationContext{}, false
	}

	a.authorizationMu.RLock()
	authorization, ok := a.sessionAuth[sessionID]
	a.authorizationMu.RUnlock()
	if !ok {
		return permissions.AuthorizationContext{}, false
	}

	normalized, err := authorization.Normalize()
	if err != nil || normalized.SessionID != sessionID {
		return permissions.AuthorizationContext{}, false
	}

	return normalized, true
}

// TurnMessages returns a defensive copy of messages emitted by the most recent turn.
func (a *Agent) TurnMessages() []morphmsg.Message {
	if a == nil || len(a.turnMessages) == 0 {
		return nil
	}

	messages := make([]morphmsg.Message, len(a.turnMessages))
	copy(messages, a.turnMessages)
	return messages
}

// getRootRunContext creates the root run identity for a public session ID.
func (a *Agent) getRootRunContext(sessionID string) (runcontext.Context, error) {
	return newRootRunContext(sessionID)
}

// openTraceSessionForSession opens a trace stream scoped to a session's root run.
func (a *Agent) openTraceSessionForSession(sessionID string) trace.Session {
	if a == nil || a.env == nil {
		return trace.NoopSession()
	}

	runCtx, err := a.getRootRunContext(sessionID)
	if err != nil {
		return trace.NoopSession()
	}

	return a.env.NewTraceSessionForRun(runCtx)
}

// invokeToolWithEnvironment executes a tool using a supplied environment.
func (a *Agent) invokeToolWithEnvironment(
	ctx context.Context,
	env environment.Environment,
	toolCall models.ToolCall) morphmsg.Message {
	return invokeToolWithEnvironment(ctx, env, toolCall, a.summaryClient, a.cfg)
}

// invokeToolWithEnvironment adapts the tool registry response into a model-visible tool message.
func invokeToolWithEnvironment(
	ctx context.Context,
	env environment.Environment,
	toolCall models.ToolCall,
	summaryClient models.Client,
	cfg *config.Config,
) morphmsg.Message {
	result := map[string]any{"name": toolCall.Name}

	// A missing registry is represented as a tool result instead of panicking so
	// the model sees a normal tool failure and the turn can complete cleanly.
	if env == nil || env.Tools() == nil {
		result["error"] = "tool registry is required"
		return toolResultMessage(toolCall, result, "")
	}

	// Web extraction can perform secondary summarization; thread the summary
	// client through context so the tool does not need to know about Agent.
	ctx = webextract.WithSummarizer(ctx, webextract.NewExtractSummarizer(summaryClient, cfg))

	toolResult, err := env.Tools().Invoke(ctx, tools.Call{
		Name:   toolCall.Name,
		Input:  toolCall.Input,
		Source: "model",
	})

	if err != nil {
		agentLog.Warn().Str("tool", toolCall.Name).Err(err).Msg("tool invocation failed")
		result["error"] = err.Error()
	}
	errorValue := str.String(toolResult.Error)
	if errorValue.Trim() != "" {
		errorValue2 := str.String(toolResult.Error)
		result["error"] = normalizeToolError(errorValue2.Trim())
	}
	outputValue := str.String(toolResult.Output)
	if outputValue.Trim() != "" {
		result["output"] = sanitizeToolOutputForModel(ctx, toolCall.Name, toolResult.Output, cfg)
	}

	semanticContent := sanitizeToolOutputForModel(ctx, toolCall.Name, toolResult.SemanticContent, cfg)
	return toolResultMessage(toolCall, result, semanticContent)
}

// sanitizeToolOutputForModel applies output guardrails before tool output is returned to the model.
func sanitizeToolOutputForModel(ctx context.Context, toolName string, output string, cfg *config.Config) string {
	outputValue2 := str.String(output)
	output = outputValue2.Trim()
	if output == "" {
		return ""
	}
	if cfg == nil || !cfg.OutputSafetyEnabled() {
		return output
	}
	toolNameValue := str.String(toolName)
	result := guardrails.CheckUntrustedContentSafety(
		output,
		"tool."+toolNameValue.Trim(),
		guardrails.NewRedactorWithOptions(guardrails.RedactorOptions{
			DisablePII: !cfg.OutputPIIRedactionEnabled(),
		}),
	)
	if result.Blocked || result.Redacted {
		recordToolOutputSafety(ctx, toolName, output, result)
	}
	contentValue := str.String(result.Content)
	return contentValue.Trim()
}

// recordToolOutputSafety records redaction/blocking decisions for tool output.
func recordToolOutputSafety(
	ctx context.Context,
	toolName string,
	output string,
	result guardrails.UntrustedContentSafetyResult,
) {
	recorder := tools.TraceRecorderFromContext(ctx)
	if recorder == nil {
		return
	}

	action := "redacted"
	if result.Blocked {
		action = "blocked"
	}
	toolNameValue2 := str.String(toolName)
	recorder.Record(trace.EvtToolOutputSafetyApplied, trace.SafetyEventPayload{
		Source:        "tool." + toolNameValue2.Trim(),
		Action:        action,
		ContentLength: len([]rune(output)),
		Blocked:       result.Blocked,
		Redacted:      result.Redacted,
		Findings:      guardrails.SafetyFindingLogFields(result.Findings),
	})
}

// toolResultMessage serializes a tool result map into the assistant conversation format.
func toolResultMessage(toolCall models.ToolCall, result map[string]any, semanticContent string) morphmsg.Message {
	raw, marshalErr := jsonMarshal(result)
	content := ""
	if marshalErr != nil {
		content = fmt.Sprintf(`{"name":%q,"error":%q}`, toolCall.Name, marshalErr.Error())
	} else {
		content = string(raw)
	}

	return morphmsg.Message{
		Role:            morphmsg.RoleTool,
		Name:            toolCall.Name,
		ToolCallID:      toolCall.ID,
		Content:         content,
		SemanticContent: str.String(semanticContent).Trim(),
	}
}

// CreateSession creates or returns a named session through the state manager.
func (a *Agent) CreateSession(
	ctx context.Context,
	id string,
	opts ...storage.SessionCreateOptions,
) (storage.Session, error) {
	if a == nil {
		return storage.Session{}, errors.New("agent is required")
	}

	if !a.initialized || a.stateMgr == nil {
		return storage.Session{}, errors.New("environment has not been initialized")
	}

	createOpts := getSessionCreateOptions(opts...)
	if createOpts.Origin == (storage.SessionOrigin{}) {
		createOpts.Origin = storage.SessionOrigin{Source: a.sessionOriginSource()}
	}

	return a.stateMgr.CreateSessionWithOptions(normalizeContext(ctx), id, createOpts)
}

func getSessionCreateOptions(opts ...storage.SessionCreateOptions) storage.SessionCreateOptions {
	if len(opts) == 0 {
		return storage.SessionCreateOptions{}
	}

	return opts[0]
}

func (a *Agent) Get(
	ctx context.Context,
	id string,
	opts storage.SessionGetOptions,
) (storage.Session, bool, error) {
	if a == nil {
		return storage.Session{}, false, errors.New("agent is required")
	}

	if !a.initialized || a.stateMgr == nil {
		return storage.Session{}, false, errors.New("environment has not been initialized")
	}

	return a.stateMgr.Get(normalizeContext(ctx), id, opts)
}

func (a *Agent) SaveGatewayBinding(ctx context.Context, binding storage.GatewayBinding) error {
	if a == nil {
		return errors.New("agent is required")
	}

	if !a.initialized || a.stateMgr == nil {
		return errors.New("environment has not been initialized")
	}

	return a.stateMgr.SaveGatewayBinding(normalizeContext(ctx), binding)
}

func (a *Agent) GetGatewayBinding(ctx context.Context, key string) (storage.GatewayBinding, bool, error) {
	if a == nil {
		return storage.GatewayBinding{}, false, errors.New("agent is required")
	}

	if !a.initialized || a.stateMgr == nil {
		return storage.GatewayBinding{}, false, errors.New("environment has not been initialized")
	}

	return a.stateMgr.GetGatewayBinding(normalizeContext(ctx), key)
}

func (a *Agent) SaveGatewayPairingRequest(ctx context.Context, request pairing.PendingRequest) error {
	if a == nil {
		return errors.New("agent is required")
	}
	if !a.initialized || a.stateMgr == nil {
		return errors.New("environment has not been initialized")
	}

	return a.stateMgr.SaveGatewayPairingRequest(normalizeContext(ctx), request)
}

func (a *Agent) GetGatewayPairingRequest(
	ctx context.Context,
	source string,
	senderID string,
) (pairing.PendingRequest, bool, error) {
	if a == nil {
		return pairing.PendingRequest{}, false, errors.New("agent is required")
	}
	if !a.initialized || a.stateMgr == nil {
		return pairing.PendingRequest{}, false, errors.New("environment has not been initialized")
	}

	return a.stateMgr.GetGatewayPairingRequest(normalizeContext(ctx), source, senderID)
}

func (a *Agent) ListGatewayPairingRequests(ctx context.Context, source string) ([]pairing.PendingRequest, error) {
	if a == nil {
		return nil, errors.New("agent is required")
	}
	if !a.initialized || a.stateMgr == nil {
		return nil, errors.New("environment has not been initialized")
	}

	return a.stateMgr.ListGatewayPairingRequests(normalizeContext(ctx), source)
}

func (a *Agent) DeleteGatewayPairingRequest(ctx context.Context, source string, senderID string) error {
	if a == nil {
		return errors.New("agent is required")
	}
	if !a.initialized || a.stateMgr == nil {
		return errors.New("environment has not been initialized")
	}

	return a.stateMgr.DeleteGatewayPairingRequest(normalizeContext(ctx), source, senderID)
}

func (a *Agent) ClearGatewayPairingRequests(ctx context.Context, source string) error {
	if a == nil {
		return errors.New("agent is required")
	}
	if !a.initialized || a.stateMgr == nil {
		return errors.New("environment has not been initialized")
	}

	return a.stateMgr.ClearGatewayPairingRequests(normalizeContext(ctx), source)
}

func (a *Agent) SaveGatewayPairedSender(ctx context.Context, sender pairing.ApprovedSender) error {
	if a == nil {
		return errors.New("agent is required")
	}
	if !a.initialized || a.stateMgr == nil {
		return errors.New("environment has not been initialized")
	}

	return a.stateMgr.SaveGatewayPairedSender(normalizeContext(ctx), sender)
}

func (a *Agent) GetGatewayPairedSender(
	ctx context.Context,
	source string,
	senderID string,
) (pairing.ApprovedSender, bool, error) {
	if a == nil {
		return pairing.ApprovedSender{}, false, errors.New("agent is required")
	}
	if !a.initialized || a.stateMgr == nil {
		return pairing.ApprovedSender{}, false, errors.New("environment has not been initialized")
	}

	return a.stateMgr.GetGatewayPairedSender(normalizeContext(ctx), source, senderID)
}

func (a *Agent) ListGatewayPairedSenders(ctx context.Context, source string) ([]pairing.ApprovedSender, error) {
	if a == nil {
		return nil, errors.New("agent is required")
	}
	if !a.initialized || a.stateMgr == nil {
		return nil, errors.New("environment has not been initialized")
	}

	return a.stateMgr.ListGatewayPairedSenders(normalizeContext(ctx), source)
}

func (a *Agent) DeleteGatewayPairedSender(ctx context.Context, source string, senderID string) error {
	if a == nil {
		return errors.New("agent is required")
	}
	if !a.initialized || a.stateMgr == nil {
		return errors.New("environment has not been initialized")
	}

	return a.stateMgr.DeleteGatewayPairedSender(normalizeContext(ctx), source, senderID)
}

// ListSessions returns known sessions.
func (a *Agent) ListSessions(ctx context.Context, opts ...storage.SessionListOptions) ([]storage.Session, error) {
	if a == nil {
		return nil, errors.New("agent is required")
	}

	if !a.initialized || a.stateMgr == nil {
		return nil, errors.New("environment has not been initialized")
	}

	return a.stateMgr.ListSessions(normalizeContext(ctx), opts...)
}

// UseSession switches the current session.
func (a *Agent) UseSession(ctx context.Context, id string) error {
	if a == nil {
		return errors.New("agent is required")
	}

	if !a.initialized || a.stateMgr == nil {
		return errors.New("environment has not been initialized")
	}

	ctx = normalizeContext(ctx)
	targetSession, err := a.stateMgr.Resolve(ctx, id)
	if err != nil {
		return err
	}

	return a.stateMgr.UseSession(ctx, targetSession.ID)
}

func (a *Agent) ArchiveSession(ctx context.Context, id string) error {
	if a == nil {
		return errors.New("agent is required")
	}

	if !a.initialized || a.stateMgr == nil {
		return errors.New("environment has not been initialized")
	}

	return a.stateMgr.ArchiveSession(normalizeContext(ctx), id)
}

func (a *Agent) UnarchiveSession(ctx context.Context, id string) (storage.Session, error) {
	if a == nil {
		return storage.Session{}, errors.New("agent is required")
	}

	if !a.initialized || a.stateMgr == nil {
		return storage.Session{}, errors.New("environment has not been initialized")
	}

	return a.stateMgr.UnarchiveSession(normalizeContext(ctx), id)
}

func (a *Agent) RenameSession(ctx context.Context, id string, title string) (storage.Session, error) {
	if a == nil {
		return storage.Session{}, errors.New("agent is required")
	}

	if !a.initialized || a.stateMgr == nil {
		return storage.Session{}, errors.New("environment has not been initialized")
	}

	return a.stateMgr.RenameSession(normalizeContext(ctx), id, title)
}

// CurrentSession returns the full current session record.
func (a *Agent) CurrentSession(ctx context.Context) (storage.Session, error) {
	if a == nil {
		return storage.Session{}, errors.New("agent is required")
	}

	if !a.initialized || a.stateMgr == nil {
		return storage.Session{}, errors.New("environment has not been initialized")
	}

	ctx = normalizeContext(ctx)
	id, err := a.stateMgr.CurrentSession(ctx)
	if err != nil {
		return storage.Session{}, err
	}

	session, ok, err := a.stateMgr.Get(ctx, id, storage.SessionGetOptions{})
	if err != nil {
		return storage.Session{}, err
	}
	if !ok {
		return storage.Session{}, fmt.Errorf("session %q not found", id)
	}

	return session, nil
}

// CompactSession forces persisted compaction for a session and returns compacted context metrics.
func (a *Agent) CompactSession(ctx context.Context, id string) (agentcore.CompactSessionResult, error) {
	summary, session, err := a.summarizeSession(ctx, id, agentsummary.SummarizeSessionOptions{})
	if err != nil {
		return agentcore.CompactSessionResult{}, err
	}

	return agentcore.CompactSessionResult{
		SessionID:            summary.SessionID,
		SourceEndOffset:      summary.SourceEndOffset,
		SourceMessageCount:   summary.SourceMessageCount,
		UpdatedAt:            summary.UpdatedAt,
		CurrentContextLength: session.LastPromptTokens,
		TotalContextLength:   a.cfg.Models.Main.ContextLength,
	}, nil
}

// RepairSession rebuilds or checks session vector indexes.
func (a *Agent) RepairSession(
	ctx context.Context,
	opts search.VectorRepairOptions,
) (search.VectorRepairResult, error) {
	if a == nil {
		return search.VectorRepairResult{}, errors.New("agent is required")
	}
	if !a.initialized || a.stateMgr == nil {
		return search.VectorRepairResult{}, errors.New("environment has not been initialized")
	}

	return a.stateMgr.RepairVectorStore(normalizeContext(ctx), opts)
}

// RecallSessionSummary returns a recall-specific session summary without persisting it.
func (a *Agent) RecallSessionSummary(ctx context.Context, id string) (storage.SessionSummary, error) {
	if a == nil {
		return storage.SessionSummary{}, errors.New("agent is required")
	}
	if a.cfg == nil {
		return storage.SessionSummary{}, errors.New("config is required")
	}
	if !a.initialized || a.stateMgr == nil {
		return storage.SessionSummary{}, errors.New("environment has not been initialized")
	}
	if a.modelClient == nil {
		return storage.SessionSummary{}, errors.New("model client is required")
	}

	ctx = normalizeContext(ctx)

	session, err := a.stateMgr.Resolve(ctx, id)
	if err != nil {
		return storage.SessionSummary{}, err
	}

	messageCount, err := a.stateMgr.CountMessages(ctx, session.ID, storage.MessageQueryOptions{})
	if err != nil {
		return storage.SessionSummary{}, err
	}

	if summary, ok := a.cachedRecallSummary(session.ID, messageCount); ok {
		return summary, nil
	}

	agentLog.Info().Str("session_id", session.ID).Msg("manual recall session summary requested")

	// Recall summaries are temporary: they are useful for inspection and
	// retrieval, but they should not advance the session compaction offset.
	traceSession := trace.NoopSession()
	if a.env != nil {
		traceSession = a.openTraceSessionForSession(session.ID)
	}
	defer traceSession.Close()

	summaryService := agentsummary.NewService(a.cfg, a.modelClient, a.summaryClient, a.stateMgr)
	summary, err := runRecallSessionSummary(summaryService, ctx, session, traceSession)
	if err != nil {
		return storage.SessionSummary{}, err
	}
	if summary == nil {
		return storage.SessionSummary{}, errors.New("summary is required")
	}

	result := storage.SessionSummary{
		SessionID:          summary.SessionID,
		SourceEndOffset:    summary.SourceEndOffset,
		SourceMessageCount: summary.SourceMessageCount,
		UpdatedAt:          summary.UpdatedAt,
		SessionSummary:     summary.SessionSummary,
		CurrentTask:        summary.CurrentTask,
		Discoveries:        summary.Discoveries,
		OpenQuestions:      summary.OpenQuestions,
		NextActions:        summary.NextActions,
	}

	a.storeRecallSummary(result)

	return result, nil
}

// summarizeSession forces a persisted summary/compaction pass for a session.
func (a *Agent) summarizeSession(
	ctx context.Context,
	id string,
	opts agentsummary.SummarizeSessionOptions,
) (storage.SessionSummary, storage.Session, error) {
	if a == nil {
		return storage.SessionSummary{}, storage.Session{}, errors.New("agent is required")
	}
	if a.cfg == nil {
		return storage.SessionSummary{}, storage.Session{}, errors.New("config is required")
	}
	if !a.initialized || a.stateMgr == nil {
		return storage.SessionSummary{}, storage.Session{}, errors.New("environment has not been initialized")
	}
	if a.modelClient == nil {
		return storage.SessionSummary{}, storage.Session{}, errors.New("model client is required")
	}

	session, err := a.stateMgr.Resolve(normalizeContext(ctx), id)
	if err != nil {
		return storage.SessionSummary{}, storage.Session{}, err
	}

	agentLog.Info().Str("session_id", session.ID).Msg("manual session summary requested")

	// Manual compaction is another context-loss boundary, so memory extraction
	// runs before the summary replaces old messages in active context.
	traceSession := trace.NoopSession()
	if a.env != nil {
		traceSession = a.openTraceSessionForSession(session.ID)
	}
	defer traceSession.Close()

	a.maybeFlushMemoryBeforeContextLoss(ctx, session.ID, memoryFlushTriggerCompression, traceSession)

	summaryService := agentsummary.NewService(a.cfg, a.modelClient, a.summaryClient, a.stateMgr)
	summary, err := summaryService.SummarizeSession(
		normalizeContext(ctx),
		session,
		agentsummary.SummarizeSessionOptions(opts),
		traceSession,
	)
	if err != nil {
		agentLog.Error().Str("session_id", session.ID).Err(err).Msg("session summary failed")
		return storage.SessionSummary{}, storage.Session{}, err
	}

	return storage.SessionSummary{
		SessionID:          summary.SessionID,
		SourceEndOffset:    summary.SourceEndOffset,
		SourceMessageCount: summary.SourceMessageCount,
		UpdatedAt:          summary.UpdatedAt,
		SessionSummary:     summary.SessionSummary,
		CurrentTask:        summary.CurrentTask,
		Discoveries:        append([]string(nil), summary.Discoveries...),
		OpenQuestions:      append([]string(nil), summary.OpenQuestions...),
		NextActions:        append([]string(nil), summary.NextActions...),
	}, session, nil
}

// ContextStatus reports prompt-token and compaction status for a session.
func (a *Agent) ContextStatus(ctx context.Context, id string) (agentcore.ContextStatus, error) {
	if a == nil {
		return agentcore.ContextStatus{}, errors.New("agent is required")
	}
	if a.cfg == nil {
		return agentcore.ContextStatus{}, errors.New("config is required")
	}
	if !a.initialized || a.stateMgr == nil {
		return agentcore.ContextStatus{}, errors.New("environment has not been initialized")
	}

	session, err := a.stateMgr.Resolve(normalizeContext(ctx), id)
	if err != nil {
		return agentcore.ContextStatus{}, err
	}

	summary, _, err := a.stateMgr.GetSummary(normalizeContext(ctx), session.ID)
	if err != nil {
		return agentcore.ContextStatus{}, err
	}

	total := max(a.cfg.Models.Main.ContextLength, 0)
	used := max(session.LastPromptTokens, 0)
	remaining := max(total-used, 0)

	status := agentcore.ContextStatus{
		SessionID:        session.ID,
		Offset:           max(summary.SourceEndOffset, 0),
		Size:             max(summary.SourceMessageCount, 0),
		Length:           total,
		Used:             used,
		Remaining:        remaining,
		CreatedAt:        session.CreatedAt,
		UpdatedAt:        session.UpdatedAt,
		CompactionStatus: string(session.Compaction.Status),
	}
	if total > 0 {
		status.UsedPct = float64(used) / float64(total)
		status.RemainingPct = float64(remaining) / float64(total)
	}

	return status, nil
}

// GetSessionStatus returns the same context status shape used by session inspection.
func (a *Agent) GetSessionStatus(ctx context.Context, id string) (agentcore.ContextStatus, error) {
	return a.ContextStatus(ctx, id)
}

// ensureStateManager lazily opens storage and creates the state manager.
func (a *Agent) ensureStateManager() error {
	if a == nil {
		return errors.New("agent is required")
	}
	if a.cfg == nil {
		return errors.New("config is required")
	}
	if a.stateMgr != nil {
		return nil
	}

	store, err := OpenStateStore(a.cfg, a.rerankerClient)
	if err != nil {
		return err
	}

	manager, err := NewStateManager(
		store,
		getDurationOrDefault(a.cfg.Session.DefaultIdleExpiry, 24*time.Hour),
		getDurationOrDefault(a.cfg.Session.ArchiveRetention, 30*24*time.Hour),
	)
	if err != nil {
		return err
	}

	a.stateMgr = manager
	return nil
}

func (a *Agent) sessionOriginSource() string {
	if a == nil || a.cfg == nil {
		return ""
	}

	platform := str.String(a.cfg.Platform)
	switch platform.Normalized() {
	case "", constants.DefaultPlatform:
		return storage.SessionOriginSourceCLI
	default:
		return ""
	}
}

// cachedRecallSummary returns a cached recall summary only when it still covers the full session.
func (a *Agent) cachedRecallSummary(sessionID string, messageCount int) (storage.SessionSummary, bool) {
	if a == nil || a.recallSummaryCache == nil {
		return storage.SessionSummary{}, false
	}

	summary, ok := a.recallSummaryCache.Get(sessionID)
	if !ok {
		return storage.SessionSummary{}, false
	}

	if !isFullRecallSummary(summary, messageCount) {
		a.recallSummaryCache.Delete(sessionID)
		return storage.SessionSummary{}, false
	}

	return summary, true
}

// storeRecallSummary stores a defensive copy in the recall summary cache.
func (a *Agent) storeRecallSummary(summary storage.SessionSummary) {
	sessionIDValue2 := str.String(summary.SessionID)
	if a == nil || a.recallSummaryCache == nil || sessionIDValue2.Trim() == "" {
		return
	}

	a.recallSummaryCache.Set(summary.SessionID, summary)
}

// isFullRecallSummary reports whether a cached summary covers every message in the session.
func isFullRecallSummary(summary storage.SessionSummary, messageCount int) bool {
	return summary.SourceMessageCount == messageCount && summary.SourceEndOffset == messageCount
}

// getDurationOrDefault chooses fallback when value is unset or invalid.
func getDurationOrDefault(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}

	return fallback
}

// normalizeToolError preserves structured tool errors when possible.
func normalizeToolError(raw string) any {
	var toolErr tools.Error
	if err := json.Unmarshal([]byte(raw), &toolErr); err == nil {
		code := str.String(toolErr.Code)
		message := str.String(toolErr.Message)
		if code.Trim() != "" && message.Trim() != "" {
			return toolErr
		}
	}

	return raw
}

// normalizeContext replaces a nil context with context.Background.
func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}

	return ctx
}

// modelToolDefinitionFromToolDefinition converts registry tool definitions into model tool definitions.
func modelToolDefinitionFromToolDefinition(definition tools.Definition) models.ToolDefinition {
	return models.ToolDefinition{
		Name:         definition.Name,
		Description:  definition.Description,
		InputSchema:  definition.InputSchema,
		ParallelSafe: definition.ParallelSafe,
	}
}

// assistantToolCallMessageFromResponse converts model tool calls into a persisted assistant message.
func assistantToolCallMessageFromResponse(resp *models.Response) (morphmsg.Message, error) {
	outputTextValue := str.String(resp.OutputText)
	return normalizeTurnMessage(morphmsg.Message{
		Role:      morphmsg.RoleAssistant,
		Content:   outputTextValue.Trim(),
		ToolCalls: modelToolCallsToContextToolCalls(resp.ToolCalls),
	})
}

// recordModelRequest records the full model request shape for trace inspection.
func recordModelRequest(traceSession trace.Session, request models.Request) {
	traceSession.Record(trace.EvtModelRequest, request)
}

// recordModelResponse records model response metadata while dropping assistant text from traces.
func recordModelResponse(traceSession trace.Session, resp *models.Response) {
	if resp == nil {
		traceSession.Record(trace.EvtModelResponse, resp)
		return
	}

	safeResponse := *resp
	safeResponse.OutputText = ""
	traceSession.Record(trace.EvtModelResponse, safeResponse)
}

// modelToolCallsToContextToolCalls converts model tool calls into session message tool calls.
func modelToolCallsToContextToolCalls(toolCalls []models.ToolCall) []morphmsg.ToolCall {
	return models.ToolCallsToMessageToolCalls(toolCalls)
}
