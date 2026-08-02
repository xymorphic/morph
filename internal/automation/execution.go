package automation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	morphagent "github.com/xymorphic/morph/internal/agent"
	"github.com/xymorphic/morph/internal/config"
	models "github.com/xymorphic/morph/internal/model"
	modelclient "github.com/xymorphic/morph/internal/model/client"
	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/internal/profile"
	state "github.com/xymorphic/morph/internal/state/core"
	agentcore "github.com/xymorphic/morph/pkg/agent"
	"github.com/xymorphic/morph/pkg/str"
)

const (
	SessionTargetIsolated = "isolated"
	SessionTargetMain     = "main"
	SessionTargetOrigin   = "origin"
	SessionTargetCurrent  = "current"
	SessionTargetPrefix   = "session:"

	automationNoopOutput = "noop"
)

var resolveProfileFromOptions = profile.Resolve

type RuntimeAgent interface {
	SetTurnCoordinator(morphagent.TurnCoordinator, string)
	Start(context.Context) error
	Respond(context.Context, string, agentcore.RespondOptions) (string, error)
	CreateSession(context.Context, string, ...state.SessionCreateOptions) (state.Session, error)
	CurrentSession(context.Context) (state.Session, error)
	Close() error
}

type AgentFactory func(context.Context, *config.Config, models.Client, models.Client) RuntimeAgent

type ProfileResolver func(str.String) (profile.Profile, error)

type ConfigLoader func(string, string) (*config.Config, error)

type ModelClientFactory interface {
	NewClient(modelclient.ClientRequest) (models.Client, error)
}

type AgentRunnerOptions struct {
	ProfileResolver    ProfileResolver
	ConfigLoader       ConfigLoader
	ModelClientFactory ModelClientFactory
	AgentFactory       AgentFactory
	DefaultMaxRuntime  time.Duration
}

type AgentRunner struct {
	resolveProfile ProfileResolver
	loadConfig     ConfigLoader
	clientFactory  ModelClientFactory
	agentFactory   AgentFactory
	defaultTimeout time.Duration
}

func NewAgentRunner(opts AgentRunnerOptions) *AgentRunner {
	resolveProfile := opts.ProfileResolver
	if resolveProfile == nil {
		resolveProfile = resolveAutomationProfile
	}
	loadConfig := opts.ConfigLoader
	if loadConfig == nil {
		loadConfig = config.Load
	}
	clientFactory := opts.ModelClientFactory
	if clientFactory == nil {
		clientFactory = modelclient.NewDefaultClientFactory()
	}
	agentFactory := opts.AgentFactory
	if agentFactory == nil {
		agentFactory = newRuntimeAgent
	}

	return &AgentRunner{
		resolveProfile: resolveProfile,
		loadConfig:     loadConfig,
		clientFactory:  clientFactory,
		agentFactory:   agentFactory,
		defaultTimeout: opts.DefaultMaxRuntime,
	}
}

func (r *AgentRunner) RunAutomation(ctx context.Context, job Job) (RunResult, error) {
	if r == nil {
		return RunResult{}, errors.New("automation runner is required")
	}

	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return RunResult{}, err
	}

	payload, err := preparePayload(job.Payload)
	if err != nil {
		return RunResult{}, err
	}
	targetValue := str.String(job.SessionTarget)
	target, err := parseSessionTarget(targetValue)
	if err != nil {
		return RunResult{}, err
	}
	if payload.Kind == PayloadSystemEvent {
		return RunResult{
			Status:    RunStatusSkipped,
			Output:    automationNoopOutput,
			SessionID: staticTargetSessionID(target),
		}, nil
	}

	ctx, cancel := r.contextWithPayloadTimeout(ctx, payload)
	defer cancel()

	profileName := str.String(job.Profile)
	activeProfile, err := r.resolveProfile(profileName)
	if err != nil {
		return RunResult{}, err
	}
	cfg, err := r.loadConfig(activeProfile.EnvPath, activeProfile.ConfigPath)
	if err != nil {
		return RunResult{}, err
	}
	applyPayloadOverrides(cfg, payload)

	modelClient, summaryClient, err := r.newModelClients(cfg)
	if err != nil {
		return RunResult{}, err
	}

	agent := r.agentFactory(ctx, cfg, modelClient, summaryClient)
	if agent == nil {
		return RunResult{}, errors.New("automation runtime agent is required")
	}
	agent.SetTurnCoordinator(nil, activeProfile.HomeDir)
	if err := agent.Start(ctx); err != nil {
		return RunResult{}, err
	}
	defer func() { _ = agent.Close() }()

	sessionID, err := resolveRunSessionID(ctx, agent, target)
	if err != nil {
		return RunResult{}, err
	}
	ctx = permissions.WithContext(ctx, permissions.AuthorizationContext{
		Actor:           permissions.Actor{Kind: permissions.ActorAutomation, ID: job.ID},
		Surface:         permissions.SurfaceAutomation,
		Profile:         activeProfile.Name,
		SessionID:       sessionID,
		ParentActorKind: permissions.ActorKind(job.Authorization.ActorKind),
		ParentActorID:   job.Authorization.ActorID,
		ParentRunID:     job.Authorization.RunID,
	})

	stream := false
	output, err := agent.Respond(ctx, payload.Prompt, agentcore.RespondOptions{
		SessionID:  sessionID,
		ToolGroups: append([]string(nil), payload.ToolGroups...),
		Stream:     &stream,
	})
	if err != nil {
		return RunResult{}, err
	}

	return RunResult{
		Status:    RunStatusOK,
		Output:    output,
		SessionID: sessionID,
		Model:     cfg.Models.Main.Name,
		Provider:  cfg.Models.Main.Provider,
	}, nil
}

type sessionTarget struct {
	Kind      string
	SessionID string
}

func preparePayload(payload Payload) (Payload, error) {
	payload = normalizeAutomationPayload(payload)
	if payload.Kind == "" {
		payload.Kind = PayloadPrompt
	}
	if err := checkAutomationPayloadContent(payload); err != nil {
		return Payload{}, err
	}
	if err := checkAutomationPayloadLimits(payload); err != nil {
		return Payload{}, err
	}

	return payload, nil
}

func parseSessionTarget(value str.String) (sessionTarget, error) {
	trimmed := value.Trim()
	if trimmed == "" {
		trimmed = SessionTargetIsolated
	}

	switch trimmed {
	case SessionTargetIsolated, SessionTargetMain, SessionTargetOrigin, SessionTargetCurrent:
		return sessionTarget{Kind: trimmed}, nil
	}
	if rawSessionID, ok := strings.CutPrefix(trimmed, SessionTargetPrefix); ok {
		sessionID := str.String(rawSessionID)
		trimmedSessionID := sessionID.Trim()
		if err := state.ValidateSessionID(trimmedSessionID); err != nil {
			return sessionTarget{}, err
		}
		return sessionTarget{Kind: SessionTargetPrefix, SessionID: trimmedSessionID}, nil
	}

	return sessionTarget{}, fmt.Errorf("unsupported automation session target %q", trimmed)
}

func normalizeToolGroups(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, raw := range values {
		value := str.String(raw)
		group := value.Normalized()
		if group == "" {
			continue
		}
		if _, ok := seen[group]; ok {
			continue
		}
		seen[group] = struct{}{}
		normalized = append(normalized, group)
	}

	return normalized
}

func (r *AgentRunner) contextWithPayloadTimeout(
	ctx context.Context,
	payload Payload,
) (context.Context, context.CancelFunc) {
	timeout := payload.MaxRuntime
	if payload.NoTimeout {
		return ctx, func() {}
	}
	if timeout <= 0 {
		timeout = r.defaultTimeout
	}
	if timeout <= 0 {
		return ctx, func() {}
	}

	return context.WithTimeout(ctx, timeout)
}

func resolveAutomationProfile(name str.String) (profile.Profile, error) {
	active := profile.Active()
	trimmed := name.Trim()
	activeName := str.String(active.Name)
	activeHomeDir := str.String(active.HomeDir)

	if trimmed == "" {
		if activeName.Trim() != "" {
			return profile.WithMetadataPaths(active), nil
		}

		return resolveProfileFromOptions(profile.ResolveOptions{})
	}
	if activeName.Trim() == trimmed && activeHomeDir.Trim() != "" {
		return profile.WithMetadataPaths(active), nil
	}

	return resolveProfileFromOptions(profile.ResolveOptions{Name: trimmed})
}

func applyPayloadOverrides(cfg *config.Config, payload Payload) {
	if cfg == nil {
		return
	}

	if payload.Provider != "" {
		cfg.Models.Main.Provider = payload.Provider
	}
	if payload.Model != "" {
		cfg.Models.Main.Name = payload.Model
	}
	if payload.BaseURL != "" {
		cfg.Models.Main.BaseURL = payload.BaseURL
	}
	if payload.MaxIterations > 0 {
		cfg.Session.MaxIterations = payload.MaxIterations
	}
	cfg.Normalize()
}

func (r *AgentRunner) newModelClients(cfg *config.Config) (models.Client, models.Client, error) {
	if cfg == nil {
		return nil, nil, errors.New("config is required")
	}

	auth, err := cfg.ResolveModelAuth()
	if err != nil {
		return nil, nil, err
	}
	modelClient, err := r.clientFactory.NewClient(modelClientRequest(
		modelclient.ModelRoleMain,
		cfg.Models.Main.Name,
		auth,
		cfg.ModelMaxRetriesEffective(),
	))
	if err != nil {
		return nil, nil, err
	}

	summaryAuth, err := cfg.ResolveSummaryModelAuth()
	if err != nil {
		return nil, nil, err
	}
	if config.ModelAuthEqual(auth, summaryAuth) {
		return modelClient, modelClient, nil
	}

	summaryClient, err := r.clientFactory.NewClient(modelClientRequest(
		modelclient.ModelRoleSummary,
		cfg.SummaryModelEffective(),
		summaryAuth,
		cfg.ModelMaxRetriesEffective(),
	))
	if err != nil {
		return nil, nil, err
	}

	return modelClient, summaryClient, nil
}

func modelClientRequest(
	role modelclient.ModelRole,
	model string,
	auth config.ModelAuth,
	maxRetries int,
) modelclient.ClientRequest {
	return modelclient.ClientRequest{
		Role:       role,
		Model:      model,
		Provider:   auth.Provider,
		API:        auth.API,
		APIKey:     auth.APIKey,
		BaseURL:    auth.BaseURL,
		Headers:    auth.Headers,
		MaxRetries: maxRetries,
	}
}

func newRuntimeAgent(
	ctx context.Context,
	cfg *config.Config,
	modelClient models.Client,
	summaryClient models.Client,
) RuntimeAgent {
	return morphagent.NewAgent(ctx, cfg, modelClient, summaryClient)
}

func resolveRunSessionID(ctx context.Context, agent RuntimeAgent, target sessionTarget) (string, error) {
	switch target.Kind {
	case SessionTargetIsolated:
		session, err := agent.CreateSession(ctx, "", state.SessionCreateOptions{
			Origin: state.SessionOrigin{Source: state.SessionOriginSourceAutomation},
		})
		if err != nil {
			return "", err
		}
		return session.ID, nil
	case SessionTargetMain:
		return state.DefaultSessionID, nil
	case SessionTargetOrigin, SessionTargetCurrent:
		session, err := agent.CurrentSession(ctx)
		if err != nil {
			return "", err
		}
		return session.ID, nil
	case SessionTargetPrefix:
		return target.SessionID, nil
	default:
		return "", fmt.Errorf("unsupported automation session target %q", target.Kind)
	}
}

func staticTargetSessionID(target sessionTarget) string {
	switch target.Kind {
	case SessionTargetMain:
		return state.DefaultSessionID
	case SessionTargetPrefix:
		return target.SessionID
	default:
		return ""
	}
}
