package daemon

import (
	"context"
	"errors"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/constants"
	models "github.com/xymorphic/morph/internal/model"
	modelclient "github.com/xymorphic/morph/internal/model/client"
	"github.com/xymorphic/morph/pkg/str"
)

type modelClientFactoryAPI interface {
	NewClient(modelclient.ClientRequest) (models.Client, error)
}

var modelClientFactory modelClientFactoryAPI = modelclient.NewDefaultClientFactory()

// resolveSummaryAuth resolves summary model credentials (hooked in tests).
var resolveSummaryAuth = func(cfg *config.Config) (config.ModelAuth, error) {
	return cfg.ResolveSummaryModelAuth()
}

var resolveRerankerAuth = func(cfg *config.Config) (config.ModelAuth, error) {
	return cfg.ResolveRerankerModelAuth()
}

type unavailableModelClient struct {
	err error
}

func (c unavailableModelClient) Complete(context.Context, models.Request) (*models.Response, error) {
	return nil, c.err
}

func (c unavailableModelClient) CompleteStream(
	context.Context,
	models.Request,
	func(models.StreamDelta),
) (*models.Response, error) {
	return nil, c.err
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

func rerankerModelClientRequired(cfg *config.Config) bool {
	if cfg == nil || !cfg.Search.Vector.Enabled {
		return false
	}
	if cfg.Reranker.Enabled != nil && !*cfg.Reranker.Enabled {
		return false
	}
	if cfg.Search.EnableRerank != nil && !*cfg.Search.EnableRerank {
		return false
	}
	if cfg.RerankerEffective() == constants.RerankerLLM {
		return true
	}

	for _, override := range cfg.Reranker.Overrides {
		if cfg.RerankerOverrideEffective(override).Type == constants.RerankerLLM {
			return true
		}
	}

	return false
}

func prepareDaemonRuntimeConfig(cfg *config.Config) *config.Config {
	if cfg == nil {
		return cfg
	}

	needsRuntimeConfig := false
	runtimeCfg := *cfg

	if !hasDaemonModelSelection(cfg) && cfg.Gateway.Enabled {
		runtimeCfg.Gateway.Enabled = false
		needsRuntimeConfig = true
		daemonLog.Warn().Msg("Starting daemon with gateway disabled until model config is available")
	}

	if cfg.Search.Vector.Enabled {
		if _, err := cfg.ResolveEmbeddingModelAuth(); err == nil {
			summaryModelEffectiveValue := str.String(cfg.SummaryModelEffective())
			if summaryModelEffectiveValue.Trim() != "" {
				if !needsRuntimeConfig {
					return cfg
				}

				return &runtimeCfg
			}
		} else {
			daemonLog.Warn().Err(err).Msg("Starting daemon with vector retrieval disabled until embedding config is available")
		}

		runtimeCfg.Search.Vector.Enabled = false
		needsRuntimeConfig = true
	}
	summaryModelEffectiveValue2 := str.String(cfg.SummaryModelEffective())
	if summaryModelEffectiveValue2.Trim() == "" {
		disabled := false
		runtimeCfg.Memory.Enabled = &disabled
		needsRuntimeConfig = true
		daemonLog.Warn().Msg("Starting daemon with memory disabled until model config is available")
	}

	if needsRuntimeConfig {
		return &runtimeCfg
	}

	return cfg
}

func hasDaemonModelSelection(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}

	nameValue := str.String(cfg.Models.Main.Name)
	providerValue := str.String(cfg.Models.Main.Provider)
	return nameValue.Trim() != "" && providerValue.Trim() != ""
}

func buildDaemonModelClients(cfg *config.Config) (models.Client, models.Client, models.Client, error) {
	modelClient, auth, err := buildDaemonMainModelClient(cfg)
	if err != nil {
		return nil, nil, nil, err
	}

	if _, ok := modelClient.(unavailableModelClient); ok {
		return modelClient, modelClient, modelClient, nil
	}

	summaryClient, summaryAuth, err := buildDaemonSummaryModelClient(cfg, modelClient, auth)
	if err != nil {
		return nil, nil, nil, err
	}

	rerankerClient := summaryClient
	if rerankerModelClientRequired(cfg) {
		rerankerClient, err = buildDaemonRerankerModelClient(cfg, modelClient, summaryClient, auth, summaryAuth)
		if err != nil {
			return nil, nil, nil, err
		}
	}

	return modelClient, summaryClient, rerankerClient, nil
}

func buildDaemonMainModelClient(cfg *config.Config) (models.Client, config.ModelAuth, error) {
	nameValue2 := str.String(cfg.Models.Main.Name)
	if nameValue2.Trim() == "" {
		err := errors.New("model is required")
		daemonLog.Warn().Err(err).Msg("Starting daemon without a configured model")
		return unavailableModelClient{err: err}, config.ModelAuth{}, nil
	}
	providerValue2 := str.String(cfg.Models.Main.Provider)
	if providerValue2.Trim() == "" {
		err := errors.New("model provider is required")
		daemonLog.Warn().Err(err).Msg("Starting daemon without a configured model provider")
		return unavailableModelClient{err: err}, config.ModelAuth{}, nil
	}

	auth, err := cfg.ResolveModelAuth()
	if err != nil {
		daemonLog.Warn().Err(err).Msg("Starting daemon without model credentials")
		return unavailableModelClient{err: err}, config.ModelAuth{}, nil
	}

	modelClient, err := modelClientFactory.NewClient(
		modelClientRequest(
			modelclient.ModelRoleMain,
			cfg.Models.Main.Name,
			auth,
			cfg.ModelMaxRetriesEffective(),
		),
	)
	if err != nil {
		return nil, config.ModelAuth{}, err
	}

	return modelClient, auth, nil
}

func buildDaemonSummaryModelClient(
	cfg *config.Config,
	modelClient models.Client,
	auth config.ModelAuth,
) (models.Client, config.ModelAuth, error) {
	summaryAuth, err := resolveSummaryAuth(cfg)
	if err != nil {
		daemonLog.Warn().Err(err).Msg("Starting daemon without summary model credentials")
		return unavailableModelClient{err: err}, config.ModelAuth{}, nil
	}

	if config.ModelAuthEqual(auth, summaryAuth) {
		return modelClient, summaryAuth, nil
	}

	summaryClient, err := modelClientFactory.NewClient(
		modelClientRequest(
			modelclient.ModelRoleSummary,
			cfg.SummaryModelEffective(),
			summaryAuth,
			cfg.ModelMaxRetriesEffective(),
		),
	)
	if err != nil {
		return nil, config.ModelAuth{}, err
	}

	return summaryClient, summaryAuth, nil
}

func buildDaemonRerankerModelClient(
	cfg *config.Config,
	modelClient,
	summaryClient models.Client,
	auth,
	summaryAuth config.ModelAuth,
) (models.Client, error) {
	rerankerAuth, err := resolveRerankerAuth(cfg)
	if err != nil {
		daemonLog.Warn().Err(err).Msg("Starting daemon without reranker model credentials")
		return unavailableModelClient{err: err}, nil
	}

	switch {
	case config.ModelAuthEqual(auth, rerankerAuth):
		return modelClient, nil
	case config.ModelAuthEqual(summaryAuth, rerankerAuth):
		return summaryClient, nil
	}

	rerankerClient, err := modelClientFactory.NewClient(
		modelClientRequest(
			modelclient.ModelRoleReranker,
			cfg.RerankerModelEffective(),
			rerankerAuth,
			cfg.ModelMaxRetriesEffective(),
		),
	)
	if err != nil {
		return nil, err
	}

	return rerankerClient, nil
}
