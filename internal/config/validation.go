package config

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/wandxy/morph/internal/constants"
	modelprovider "github.com/wandxy/morph/internal/model/provider"
	"github.com/wandxy/morph/pkg/str"
)

type validationOptions struct {
	requireModels          bool
	skipGatewayCredentials bool
}

func (c *Config) Validate() error {
	return c.validate(validationOptions{requireModels: true})
}

func (c *Config) ValidateRelaxed() error {
	return c.validate(validationOptions{
		skipGatewayCredentials: true,
	})
}

func (c *Config) ValidateGateway() error {
	if c == nil {
		return errors.New("config is required")
	}

	c.Normalize()
	return c.validateGatewaySettings()
}

func (c *Config) validate(options validationOptions) error {
	if c == nil {
		return errors.New("config is required")
	}

	requestedContextLength := c.Models.Main.ContextLength
	if err := c.validatePersonalityNames(); err != nil {
		return err
	}

	c.Normalize()
	applyRegistryModelMetadata(c, requestedContextLength)

	if err := c.validatePersonalities(); err != nil {
		return err
	}

	if c.Platform != constants.DefaultPlatform {
		return errors.New("platform must be cli")
	}

	if options.requireModels {
		if !isValidModelID(c.Models.Main.Name) {
			return errors.New("model is required")
		}

		if c.Models.Summary.Name != "" && !isValidModelID(c.Models.Summary.Name) {
			return errors.New("summary model is invalid")
		}

		providerValue := str.String(c.Models.Main.Provider)
		if providerValue.Trim() == "" {
			return errors.New("model provider is required")
		}
		if !hasModelProvider(c.Models.Main.Provider) {
			return fmt.Errorf("model provider must be one of: %s", getModelProviderList())
		}

		if c.Models.Summary.Provider != "" {
			if !hasModelProvider(c.Models.Summary.Provider) {
				return fmt.Errorf("summary model provider must be one of: %s", getModelProviderList())
			}
		}

		if err := c.validateModelSettings(); err != nil {
			return err
		}

		if err := c.validateRerankerSettings(); err != nil {
			return err
		}

		if err := c.validateSearchVectorSettings(); err != nil {
			return err
		}

		if _, err := c.ResolveModelAuth(); err != nil {
			return err
		}

		if _, err := c.ResolveSummaryModelAuth(); err != nil {
			return err
		}
	}
	addressValue := str.String(c.RPC.Address)
	if addressValue.Trim() == "" {
		return errors.New("rpc address is required; set MORPH_RPC_ADDRESS, provide it in config, or use --rpc.address")
	}

	if c.RPC.Port < 0 {
		return errors.New("rpc port must be non-negative; set MORPH_RPC_PORT, provide it in config, or use --rpc.port")
	}
	if err := c.validateAuth(); err != nil {
		return err
	}

	if err := c.validateGatewaySettings(gatewayValidationOptions{
		skipCredentials: options.skipGatewayCredentials,
	}); err != nil {
		return err
	}

	if c.Session.MaxIterations <= 0 {
		return errors.New("max iterations must be greater than zero; set MORPH_SESSION_MAX_ITERATIONS, provide it in config, " +
			"or use --max-iterations")
	}
	if c.ModelMaxRetriesEffective() < 0 {
		return errors.New("model max retries must be greater than or equal to zero; use --model.max-retries")
	}

	if c.Storage.Backend != "memory" && c.Storage.Backend != "sqlite" {
		return errors.New("storage backend must be one of: memory, sqlite")
	}
	if c.Memory.Backend != "" && c.Memory.Backend != "memory" && c.Memory.Backend != "sqlite" {
		return errors.New("memory backend must be one of: memory, sqlite")
	}
	if err := c.Permissions.Validate(); err != nil {
		return err
	}
	if err := c.validateExecSettings(); err != nil {
		return err
	}
	if err := c.validateExecution(); err != nil {
		return err
	}
	if err := c.validateBrowserSettings(); err != nil {
		return err
	}
	if c.Compaction.TriggerPercent >= 1 {
		return errors.New("compaction trigger percent must be greater than zero and less than one")
	}
	if c.Compaction.WarnPercent >= 1 {
		return errors.New("compaction warn percent must be greater than zero and less than one")
	}
	if c.Compaction.WarnPercent < c.Compaction.TriggerPercent {
		return errors.New("compaction warn percent must be greater than or equal to compaction trigger percent")
	}
	if c.Compaction.RecentSessionTail != nil && *c.Compaction.RecentSessionTail < 0 {
		return errors.New("compaction recent session tail must be greater than or equal to zero")
	}
	levelValue := str.String(c.Log.Level)
	switch levelValue.Normalized() {
	case "", "debug", "info", "warn", "error":
	default:
		return errors.New("log level must be one of debug, info, warn, or error; use --log.level")
	}
	if c.Log.MaxSizeMB < 0 {
		return errors.New("log max size must be non-negative; use --log.max-size-mb")
	}
	if c.Log.MaxBackups < 0 {
		return errors.New("log max backups must be non-negative; use --log.max-backups")
	}
	if c.Log.MaxAgeDays < 0 {
		return errors.New("log max age days must be non-negative; use --log.max-age-days")
	}

	return nil
}

type gatewayValidationOptions struct {
	skipCredentials bool
}

func (c *Config) validateGatewaySettings(options ...gatewayValidationOptions) error {
	var opts gatewayValidationOptions
	if len(options) > 0 {
		opts = options[0]
	}
	addressValue2 := str.String(c.Gateway.Address)
	if addressValue2.Trim() == "" {
		return errors.New("gateway address is required; set MORPH_GATEWAY_ADDRESS, provide it in config, or use --gateway.address")
	}
	if c.Gateway.Port < 0 {
		return errors.New("gateway port must be non-negative; set MORPH_GATEWAY_PORT, provide it in config, or use --gateway.port")
	}
	if !c.Gateway.Enabled {
		return nil
	}
	if opts.skipCredentials {
		return validateGatewayChannelModes(c.Gateway)
	}
	authTokenValue := str.String(c.Gateway.AuthToken)
	if !isLoopbackGatewayAddress(c.Gateway.Address) && authTokenValue.Trim() == "" {
		return errors.New("gateway auth token is required for non-loopback binds; set MORPH_GATEWAY_AUTH_TOKEN, " +
			"provide it in config, or use --gateway.auth-token")
	}
	if err := validateGatewayTelegramSettings(c.Gateway.Telegram); err != nil {
		return err
	}
	if err := validateGatewaySlackSettings(c.Gateway.Slack); err != nil {
		return err
	}

	return nil
}

func validateGatewayChannelModes(cfg GatewayConfig) error {
	if err := validateGatewayTelegramMode(cfg.Telegram.Mode); err != nil {
		return err
	}
	if err := validateGatewaySlackMode(cfg.Slack.Mode); err != nil {
		return err
	}
	if err := validateGatewaySlackResponseMode(cfg.Slack.ResponseMode); err != nil {
		return err
	}

	return nil
}

func validateGatewayTelegramMode(mode string) error {
	switch mode {
	case GatewayTelegramModePolling, GatewayTelegramModeWebhook:
		return nil
	default:
		return errors.New("gateway telegram mode must be one of: polling, webhook")
	}
}

func validateGatewayTelegramSettings(cfg GatewayTelegramConfig) error {
	if err := validateGatewayTelegramMode(cfg.Mode); err != nil {
		return err
	}
	if !cfg.Enabled {
		return nil
	}

	botTokenValue := str.String(cfg.BotToken)
	if botTokenValue.Trim() == "" {
		return errors.New("gateway telegram bot token is required when telegram gateway is enabled; " +
			"set MORPH_GATEWAY_TELEGRAM_BOT_TOKEN, provide it in config, or use --gateway.telegram.bot-token")
	}
	webhookSecretValue := str.String(cfg.WebhookSecret)
	if cfg.Mode == GatewayTelegramModeWebhook && webhookSecretValue.Trim() == "" {
		return errors.New("gateway telegram webhook secret is required in webhook mode; " +
			"set MORPH_GATEWAY_TELEGRAM_WEBHOOK_SECRET, provide it in config, or use --gateway.telegram.webhook-secret")
	}
	if cfg.Mode == GatewayTelegramModeWebhook && !isValidTelegramWebhookSecret(cfg.WebhookSecret) {
		return errors.New("gateway telegram webhook secret must be 1-256 characters and contain only " +
			"A-Z, a-z, 0-9, underscore, or hyphen")
	}

	return nil
}

func isValidTelegramWebhookSecret(secret string) bool {
	secretValue := str.String(secret)
	secret = secretValue.Trim()
	if len(secret) == 0 || len(secret) > 256 {
		return false
	}

	for _, r := range secret {
		if r >= 'A' && r <= 'Z' {
			continue
		}
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= '0' && r <= '9' {
			continue
		}
		if r == '_' || r == '-' {
			continue
		}

		return false
	}

	return true
}

func validateGatewaySlackMode(mode string) error {
	switch mode {
	case GatewaySlackModeSocket, GatewaySlackModeHTTP:
		return nil
	default:
		return errors.New("gateway slack mode must be one of: socket, http")
	}
}

func validateGatewaySlackResponseMode(mode string) error {
	switch mode {
	case GatewaySlackResponseModeThread, GatewaySlackResponseModeMessage:
		return nil
	default:
		return errors.New("gateway slack response mode must be one of: thread, message")
	}
}

func validateGatewaySlackSettings(cfg GatewaySlackConfig) error {
	if err := validateGatewaySlackMode(cfg.Mode); err != nil {
		return err
	}
	if err := validateGatewaySlackResponseMode(cfg.ResponseMode); err != nil {
		return err
	}
	if !cfg.Enabled {
		return nil
	}

	botTokenValue2 := str.String(cfg.BotToken)
	if botTokenValue2.Trim() == "" {
		return errors.New("gateway slack bot token is required when slack gateway is enabled; " +
			"set MORPH_GATEWAY_SLACK_BOT_TOKEN, provide it in config, or use --gateway.slack.bot-token")
	}
	switch cfg.Mode {
	case GatewaySlackModeSocket:
		appTokenValue := str.String(cfg.AppToken)
		if appTokenValue.Trim() == "" {
			return errors.New("gateway slack app token is required in socket mode; " +
				"set MORPH_GATEWAY_SLACK_APP_TOKEN, provide it in config, or use --gateway.slack.app-token")
		}
	case GatewaySlackModeHTTP:
		signingSecretValue := str.String(cfg.SigningSecret)
		if signingSecretValue.Trim() == "" {
			return errors.New("gateway slack signing secret is required in http mode; " +
				"set MORPH_GATEWAY_SLACK_SIGNING_SECRET, provide it in config, or use --gateway.slack.signing-secret")
		}
	}

	return nil
}

func isLoopbackGatewayAddress(address string) bool {
	trimValue := str.String(strings.Trim(address, "[]"))
	address = trimValue.Trim()
	if address == "" || strings.EqualFold(address, "localhost") {
		return true
	}

	ip := net.ParseIP(address)
	return ip != nil && ip.IsLoopback()
}

func (c *Config) validateModelSettings() error {
	if err := validateReasoningSettings(c.Models); err != nil {
		return err
	}
	if err := validateModelRoleAPI("model API", c.MainModelAPIEffective(), modelGenerationAPIs()); err != nil {
		return err
	}
	if err := validateProviderAPI("model API", c.Models.Main.Provider, c.MainModelAPIEffective()); err != nil {
		return err
	}
	if err := validateRegistryModel(
		"models.main.name",
		c.Models.Main.Provider,
		c.MainModelAPIEffective(),
		c.Models.Main.Name,
		modelGenerationAPIs(),
	); err != nil {
		return err
	}

	summaryProvider := c.SummaryProviderEffective()
	summaryAPI := c.SummaryModelAPIEffective()
	if err := validateModelRoleAPI("summary model API", summaryAPI, modelGenerationAPIs()); err != nil {
		return err
	}
	if err := validateProviderAPI("summary model API", summaryProvider, summaryAPI); err != nil {
		return err
	}
	if err := validateRegistryModel(
		"models.summary.name",
		summaryProvider,
		summaryAPI,
		c.SummaryModelEffective(),
		modelGenerationAPIs(),
	); err != nil {
		return err
	}

	return nil
}

func (c *Config) validatePersonalityNames() error {
	if c == nil || len(c.Personalities) == 0 {
		return nil
	}

	seen := make(map[string]string, len(c.Personalities))
	names := make([]string, 0, len(c.Personalities))
	for name := range c.Personalities {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		nameValue := str.String(name)
		trimmed := nameValue.Trim()
		if !validPersonalityName.MatchString(trimmed) {
			return fmt.Errorf("invalid personality name %q: must match %s", trimmed, personalityNamePattern)
		}

		normalized := strings.ToLower(trimmed)
		if existing, ok := seen[normalized]; ok {
			return fmt.Errorf("duplicate personality name %q conflicts with %q", trimmed, existing)
		}
		seen[normalized] = trimmed
	}

	return nil
}

func (c *Config) validatePersonalities() error {
	if c == nil {
		return nil
	}

	for name, personality := range c.Personalities {
		if err := validatePersonalityConfig(name, personality); err != nil {
			return err
		}
	}

	return nil
}

func validatePersonalityConfig(name string, personality PersonalityConfig) error {
	switch personality.State {
	case personalityStateShared, personalityStateIsolated, personalityStateReadonly:
	default:
		return fmt.Errorf("personalities.%s.state must be one of: shared, isolated, readonly", name)
	}

	switch personality.Tools.Memory {
	case "", personalityToolMemoryNone, personalityToolMemoryRead, personalityToolMemoryWrite:
	default:
		return fmt.Errorf("personalities.%s.tools.mem must be one of: none, read, write", name)
	}

	if personality.MaxIterations < 0 {
		return fmt.Errorf("personalities.%s.maxIterations must be non-negative", name)
	}

	if personality.Model.Name != "" && !isValidModelID(personality.Model.Name) {
		return fmt.Errorf("personalities.%s.model.name is invalid", name)
	}
	if personality.Model.Provider != "" {
		if !hasModelProvider(personality.Model.Provider) {
			return fmt.Errorf("personalities.%s.model.provider must be one of: %s", name, getModelProviderList())
		}
	}
	switch personality.Model.API {
	case "", modelprovider.APIOpenAICompletions, modelprovider.APIOpenAIResponses, modelprovider.APIAnthropicMessages:
	default:
		return fmt.Errorf("personalities.%s.model.api must be one of: %s", name, getModelAPIList(modelGenerationAPIs()))
	}

	return nil
}

func (c *Config) validateSearchVectorSettings() error {
	if !c.Search.Vector.Enabled {
		return nil
	}

	provider := c.ModelEmbeddingProviderEffective()
	if !hasModelProvider(provider) {
		return fmt.Errorf("embedding provider must be one of: %s", getModelProviderList())
	}
	if c.Models.Embedding.Name == "" {
		return errors.New("embedding model is required")
	}
	if c.Search.Vector.RebuildBatchSize < 0 {
		return errors.New("vector rebuild batch size must be non-negative")
	}
	if c.Search.Vector.MaxInputBytes < 0 {
		return errors.New("vector max input bytes must be non-negative")
	}
	if c.Search.Vector.MaxDocumentBytes < 0 {
		return errors.New("vector max document bytes must be non-negative")
	}
	maxInputBytes := c.Search.Vector.MaxInputBytes
	if maxInputBytes == 0 {
		maxInputBytes = constants.DefaultVectorMaxInputBytes
	}
	maxDocumentBytes := c.Search.Vector.MaxDocumentBytes
	if maxDocumentBytes == 0 {
		maxDocumentBytes = constants.DefaultVectorMaxDocumentBytes
	}
	if maxDocumentBytes < maxInputBytes {
		return errors.New("vector max document bytes must be greater than or equal to max input bytes")
	}
	auth, err := c.ResolveEmbeddingModelAuth()
	if err != nil {
		return err
	}
	if err := validateProviderAPI("embedding model API", auth.Provider, auth.API); err != nil {
		return err
	}
	if err := validateModelRoleAPI("embedding model API", auth.API, modelEmbeddingAPIs()); err != nil {
		return err
	}
	if err := validateRegistryModel(
		"models.embedding.name",
		auth.Provider,
		auth.API,
		c.Models.Embedding.Name,
		modelEmbeddingAPIs(),
	); err != nil {
		return err
	}

	return nil
}

func (c *Config) validateRerankerSettings() error {
	if err := validateRerankerType(c.RerankerEffective()); err != nil {
		return err
	}
	if c.Reranker.MaxCandidates < 0 {
		return errors.New("reranker max candidates must be non-negative")
	}
	if c.Reranker.MaxCandidateTextChars < 0 {
		return errors.New("reranker max candidate text chars must be non-negative")
	}
	if c.Reranker.MaxOutputTokens < 0 {
		return errors.New("reranker max output tokens must be non-negative")
	}

	if c.RerankerEffective() == constants.RerankerLLM {
		if err := c.validateRerankerModelRole(
			"reranker model",
			c.RerankerModelEffective(),
			c.RerankerProviderEffective(),
			c.RerankerModelAPIEffective(),
		); err != nil {
			return err
		}
	}
	for useCase, override := range c.Reranker.Overrides {
		if err := c.validateRerankerOverride(useCase, override); err != nil {
			return err
		}
	}

	return nil
}

func (c *Config) validateRerankerOverride(useCase string, override RerankerOverrideConfig) error {
	useCaseValue := str.String(useCase)
	useCase = useCaseValue.Trim()
	if useCase == "" {
		return errors.New("reranker override use case is required")
	}
	trimmedValueValue := str.String(override.Type)
	if trimmedValueValue.Trim() != "" {
		if err := validateRerankerType(override.Type); err != nil {
			return fmt.Errorf("reranker override %q: %w", useCase, err)
		}
	}
	reranker := c.RerankerOverrideEffective(override)
	if reranker.Type == constants.RerankerLLM {
		api := c.RerankerModelAPIEffectiveForModel(reranker.Model)
		if err := c.validateRerankerModelRole(
			fmt.Sprintf("reranker override %q model", useCase),
			reranker.Model,
			c.RerankerProviderEffective(),
			api,
		); err != nil {
			return err
		}
		if api != c.RerankerModelAPIEffective() {
			return fmt.Errorf("reranker override %q model API must match reranker model API", useCase)
		}
	}
	if override.MaxCandidates != nil && *override.MaxCandidates < 0 {
		return fmt.Errorf("reranker override %q max candidates must be non-negative", useCase)
	}
	if override.MaxCandidateTextChars != nil && *override.MaxCandidateTextChars < 0 {
		return fmt.Errorf("reranker override %q max candidate text chars must be non-negative", useCase)
	}
	if override.MaxOutputTokens != nil && *override.MaxOutputTokens < 0 {
		return fmt.Errorf("reranker override %q max output tokens must be non-negative", useCase)
	}

	return nil
}

func (c *Config) validateRerankerModelRole(field string, modelID string, provider string, api string) error {
	if err := validateModelRoleAPI(field+" API", api, modelGenerationAPIs()); err != nil {
		return err
	}
	if err := validateProviderAPI(field+" API", provider, api); err != nil {
		return err
	}
	if err := validateRegistryModel(field, provider, api, modelID, modelGenerationAPIs()); err != nil {
		return err
	}

	return nil
}

func validateRerankerType(rerankerType string) error {
	rerankerTypeValue := str.String(rerankerType)
	switch rerankerTypeValue.Normalized() {
	case constants.RerankerDeterministic, constants.RerankerNoop, constants.RerankerLLM:
		return nil
	default:
		return errors.New("reranker type must be one of: deterministic, noop, llm")
	}
}
