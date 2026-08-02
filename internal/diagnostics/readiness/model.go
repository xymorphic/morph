package readiness

import (
	"context"
	"fmt"
	"strings"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/constants"
	modelprovider "github.com/xymorphic/morph/internal/model/provider"
	provider_ollama "github.com/xymorphic/morph/internal/model/provider_ollama"
	"github.com/xymorphic/morph/pkg/str"
)

var discoverOllamaModels = func(ctx context.Context, baseURL string) ([]modelprovider.ModelDefinition, error) {
	discoverer, err := provider_ollama.NewDiscoverer(baseURL)
	if err != nil {
		return nil, err
	}

	return discoverer.DiscoverModels(ctx)
}

func buildModelGroup(ctx context.Context, cfg *config.Config) Group {
	if cfg == nil {
		return Group{
			Name:   "models",
			Checks: []Check{check("config", StatusFail, "config is required")},
		}
	}

	checks := []Check{
		buildModelRoleCheck("main", cfg.Models.Main.Provider, cfg.Models.Main.Name, cfg.ResolveModelAuth),
		buildModelRoleCheck("summary", cfg.SummaryProviderEffective(), cfg.SummaryModelEffective(),
			cfg.ResolveSummaryModelAuth),
		buildEmbeddingRoleCheck(cfg),
	}
	checks = append(checks, buildOllamaReadinessChecks(ctx, cfg)...)

	return Group{Name: "models", Checks: checks}
}

func buildOllamaReadinessChecks(ctx context.Context, cfg *config.Config) []Check {
	if cfg == nil {
		return nil
	}

	providerValue := str.String(cfg.Models.Main.Provider)
	mainIsOllama := providerValue.Normalized() == constants.ModelProviderOllama
	modelEmbeddingProviderEffectiveValue := str.String(cfg.ModelEmbeddingProviderEffective())
	embeddingIsOllama := cfg.Search.Vector.Enabled && modelEmbeddingProviderEffectiveValue.
		Normalized() == constants.ModelProviderOllama
	if !mainIsOllama && !embeddingIsOllama {
		return nil
	}

	baseURL := getOllamaReadinessBaseURL(cfg)
	nameValue := str.String(cfg.Models.Main.Name)
	modelID := nameValue.Trim()
	models, err := discoverOllamaModels(ctx, baseURL)
	if err != nil {
		return []Check{check(
			"ollama",
			StatusFail,
			err.Error(),
			commandAction("ollama serve", "start Ollama"),
			commandAction("morph provider configure --provider ollama --base-url "+baseURL, "update Ollama base URL"),
		)}
	}

	checks := []Check{check(
		"ollama",
		StatusPass,
		fmt.Sprintf("reachable at %s, discovered %d model(s)", baseURL, len(models)),
	)}
	if !mainIsOllama {
		checks = append(checks, buildOllamaEmbeddingCheck(cfg, models)...)
		return checks
	}

	selected, ok := getOllamaReadinessModel(models, modelID)
	if !ok {
		return append(checks, check(
			"ollama model",
			StatusFail,
			fmt.Sprintf("model %q is not installed", modelID),
			commandAction(ollamaSetupPullCommand(baseURL, modelID), "pull the selected Ollama model"),
		))
	}

	checks = append(checks, check(
		"ollama model",
		StatusPass,
		fmt.Sprintf("model %q is installed", modelID),
	))
	checks = append(checks, buildOllamaContextCheck(cfg, selected))
	checks = append(checks, buildOllamaToolSupportCheck(selected))
	checks = append(checks, buildOllamaEmbeddingCheck(cfg, models)...)

	return checks
}

func getOllamaReadinessBaseURL(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}

	providerValue2 := str.String(cfg.Models.Main.Provider)
	if providerValue2.Normalized() == constants.ModelProviderOllama {
		baseURLValue := str.String(cfg.Models.Main.BaseURL)
		if value := baseURLValue.Trim(); value != "" {
			return value
		}
	}
	if auth, err := cfg.ResolveEmbeddingModelAuth(); err == nil {
		authProvider := str.String(auth.Provider)
		if authProvider.Normalized() == constants.ModelProviderOllama {
			baseURLValue2 := str.String(auth.BaseURL)
			return baseURLValue2.Trim()
		}
	}
	baseURLValue3 := str.String(cfg.Models.Embedding.BaseURL)
	if value := baseURLValue3.Trim(); value != "" {
		return value
	}
	if providerConfig, ok := cfg.Models.Providers[constants.ModelProviderOllama]; ok {
		baseURLValue4 := str.String(providerConfig.BaseURL)
		if value := baseURLValue4.Trim(); value != "" {
			return value
		}
	}

	return constants.DefaultOllamaBaseURL
}

func getOllamaReadinessModel(models []modelprovider.ModelDefinition, modelID string) (modelprovider.ModelDefinition, bool) {
	modelIDValue := str.String(modelID)
	modelID = modelIDValue.Trim()
	for _, model := range models {
		if provider_ollama.ModelIDMatches(model.ID, modelID) {
			return model, true
		}
	}

	return modelprovider.ModelDefinition{}, false
}

func buildOllamaContextCheck(cfg *config.Config, model modelprovider.ModelDefinition) Check {
	iDValue := str.String(model.ID)
	modelID := iDValue.Trim()
	if model.ContextWindow <= 0 {
		return check("ollama context", StatusWarn, fmt.Sprintf("context metadata is unavailable for model %q", modelID))
	}

	configured := cfg.Models.Main.ContextLength
	if configured > model.ContextWindow {
		return check(
			"ollama context",
			StatusWarn,
			fmt.Sprintf("configured contextLength=%d exceeds model %q reported context window=%d", configured, modelID, model.ContextWindow),
			commandAction(
				fmt.Sprintf("morph config set models.main.contextLength %d", model.ContextWindow),
				"fit configured context to the selected model",
			),
		)
	}

	return check(
		"ollama context",
		StatusPass,
		fmt.Sprintf("model %q reports context window=%d, configured contextLength=%d", modelID, model.ContextWindow, configured),
	)
}

func buildOllamaToolSupportCheck(model modelprovider.ModelDefinition) Check {
	iDValue2 := str.String(model.ID)
	modelID := iDValue2.Trim()
	if model.SupportsTools {
		return check("ollama tools", StatusPass, fmt.Sprintf("model %q reports tool support", modelID))
	}

	return check(
		"ollama tools",
		StatusWarn,
		fmt.Sprintf("model %q does not report tool support; tool-using workflows may fail", modelID),
	)
}

func buildOllamaEmbeddingCheck(cfg *config.Config, models []modelprovider.ModelDefinition) []Check {
	modelEmbeddingProviderEffectiveValue2 := str.String(cfg.ModelEmbeddingProviderEffective())
	if cfg == nil ||
		!cfg.Search.Vector.Enabled || modelEmbeddingProviderEffectiveValue2.
		Normalized() != constants.ModelProviderOllama {
		return nil
	}
	nameValue2 := str.String(cfg.Models.Embedding.Name)
	modelID := nameValue2.Trim()
	if modelID == "" {
		return []Check{check("ollama embeddings", StatusFail, "embedding model is required")}
	}
	if _, ok := getOllamaReadinessModel(models, modelID); ok {
		return []Check{check(
			"ollama embeddings",
			StatusPass,
			fmt.Sprintf("embedding model %q is installed", modelID),
		)}
	}

	return []Check{check(
		"ollama embeddings",
		StatusWarn,
		fmt.Sprintf("embedding model %q is not installed; vector search will fail until it is pulled", modelID),
		commandAction(ollamaSetupPullCommand(getOllamaReadinessBaseURL(cfg), modelID), "pull the selected Ollama embedding model"),
	)}
}

func ollamaSetupPullCommand(baseURL string, modelID string) string {
	parts := []string{"morph provider configure --provider ollama"}
	baseURLValue5 := str.String(baseURL)
	if baseURLValue5.Trim() != "" {
		baseURLValue6 := str.String(baseURL)
		parts = append(parts, "--base-url "+baseURLValue6.Trim())
	}
	modelIDValue2 := str.String(modelID)
	if modelIDValue2.Trim() != "" {
		modelIDValue3 := str.String(modelID)
		parts = append(parts, "--model "+modelIDValue3.Trim())
	}
	parts = append(parts, "--pull")

	return strings.Join(parts, " ")
}

func buildEmbeddingRoleCheck(cfg *config.Config) Check {
	if cfg.Search.Vector.Enabled {
		return buildModelRoleCheckWithActions(
			"embedding",
			cfg.ModelEmbeddingProviderEffective(),
			cfg.Models.Embedding.Name,
			cfg.ResolveEmbeddingModelAuth,
			embeddingModelErrorActions,
		)
	}

	return check(
		"embedding",
		StatusWarn,
		fmt.Sprintf(
			"embedding model %q on provider %q is configured; vector search is disabled",
			defaultString(cfg.Models.Embedding.Name, "default"),
			defaultString(cfg.ModelEmbeddingProviderEffective(), cfg.Models.Main.Provider),
		),
	)
}

func buildModelRoleCheck(
	role string,
	provider string,
	model string,
	resolve func() (config.ModelAuth, error),
) Check {
	return buildModelRoleCheckWithActions(role, provider, model, resolve, modelErrorActions)
}

func buildModelRoleCheckWithActions(
	role string,
	provider string,
	model string,
	resolve func() (config.ModelAuth, error),
	actions func(string, error) []Action,
) Check {
	auth, err := resolve()
	if err != nil {
		return check(
			role,
			StatusFail,
			err.Error(),
			actions(provider, err)...,
		)
	}

	return check(
		role,
		StatusPass,
		fmt.Sprintf(
			"%s model %q on provider %q using %s auth",
			role,
			defaultString(model, "default"),
			defaultString(auth.Provider, provider),
			formatCredentialSource(auth),
		),
	)
}

func modelErrorActions(provider string, err error) []Action {
	actions := []Action{
		commandAction("/providers", "review known providers in the TUI"),
		commandAction("/models", "review models for the selected provider"),
	}
	if isMissingAuthError(err) {
		actions = append(missingAuthActions(provider), actions...)
	}

	return actions
}

func embeddingModelErrorActions(provider string, err error) []Action {
	actions := []Action{
		commandAction("/providers", "review known providers in the TUI"),
		commandAction("/models", "review models for the selected provider"),
	}
	if isMissingAuthError(err) {
		actions = append(providerAPIKeyActions(provider), actions...)
	}

	return actions
}

func isMissingAuthError(err error) bool {
	if err == nil {
		return false
	}

	errorValue := str.String(err.Error())
	message := errorValue.Normalized()
	return strings.Contains(message, "api key is required") ||
		strings.Contains(message, "morph provider login")
}

func missingAuthActions(provider string) []Action {
	providerValue3 := str.String(provider)
	provider = providerValue3.Normalized()
	if provider == "" {
		return nil
	}
	switch provider {
	case constants.ModelProviderOpenAI, constants.ModelProviderOpenAICodex,
		constants.ModelProviderAnthropic, constants.ModelProviderGitHubCopilot:
		return []Action{commandAction("morph provider login "+provider, "store OAuth credentials for this provider")}
	default:
		return providerAPIKeyActions(provider)
	}
}

func providerAPIKeyActions(provider string) []Action {
	providerValue4 := str.String(provider)
	provider = providerValue4.Normalized()
	if provider == "" {
		return nil
	}

	return []Action{
		commandAction(
			fmt.Sprintf("morph provider login %s --api-key <api-key>", provider),
			"store a provider API key",
		),
		commandAction(
			fmt.Sprintf("morph config set models.providers.%s.apiKey <api-key>", provider),
			"write the provider API key to the profile config",
		),
	}
}

func formatCredentialSource(auth config.ModelAuth) string {
	source := auth.CredentialSource
	switch source.Kind {
	case config.ModelCredentialSourceRoleConfig:
		return "role-config"
	case config.ModelCredentialSourceProviderConfig:
		return "provider-config"
	case config.ModelCredentialSourceProviderEnv:
		trimmedValueValue := str.String(source.Type)
		if trimmedValueValue.Trim() != "" {
			trimmedValueValue2 := str.String(source.Type)
			return trimmedValueValue2.Trim() + " env"
		}
		return "environment"
	case config.ModelCredentialSourceTokenStore:
		parts := []string{"token-store"}
		trimmedValueValue3 := str.String(source.Type)
		if trimmedValueValue3.Trim() != "" {
			trimmedValueValue4 := str.String(source.Type)
			parts = append(parts, trimmedValueValue4.Trim())
		}
		if source.HasExpiry {
			parts = append(parts, "refreshable")
		}
		return strings.Join(parts, " ")
	default:
		return auth.AuthType()
	}
}
