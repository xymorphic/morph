package config

import (
	"path/filepath"
	"slices"
	"strings"

	commandplan "github.com/wandxy/morph/internal/command"
	"github.com/wandxy/morph/internal/constants"
	"github.com/wandxy/morph/internal/datadir"
	modelprovider "github.com/wandxy/morph/internal/model/provider"
	"github.com/wandxy/morph/pkg/str"
)

// normalizeFields applies trimming and defaults except default model base URL resolution.
func (c *Config) normalizeFields() {
	if c == nil {
		return
	}

	nameValue := str.String(c.Name)
	c.Name = nameValue.Trim()
	if c.Name == "" {
		c.Name = constants.DefaultName
	}
	nameValue2 := str.String(c.Models.Main.Name)
	c.Models.Main.Name = nameValue2.Trim()
	nameValue3 := str.String(c.Models.Summary.Name)
	c.Models.Summary.Name = nameValue3.Trim()
	providerValue := str.String(c.Models.Main.Provider)
	c.Models.Main.Provider = providerValue.Normalized()
	providerValue2 := str.String(c.Models.Embedding.Provider)
	c.Models.Embedding.Provider = providerValue2.Normalized()
	nameValue4 := str.String(c.Models.Embedding.Name)
	c.Models.Embedding.Name = nameValue4.Trim()
	aPIValue := str.String(c.Models.Embedding.API)
	c.Models.Embedding.API = aPIValue.Normalized()
	c.Models.Providers = normalizeProviderModelConfigs(c.Models.Providers)
	aPIKeyValue := str.String(c.Models.Main.APIKey)
	c.Models.Main.APIKey = aPIKeyValue.Trim()
	baseURLValue := str.String(c.Models.Main.BaseURL)
	c.Models.Main.BaseURL = baseURLValue.Trim()
	providerValue3 := str.String(c.Models.Summary.Provider)
	c.Models.Summary.Provider = providerValue3.Normalized()
	aPIKeyValue2 := str.String(c.Models.Summary.APIKey)
	c.Models.Summary.APIKey = aPIKeyValue2.Trim()
	baseURLValue2 := str.String(c.Models.Summary.BaseURL)
	c.Models.Summary.BaseURL = baseURLValue2.Trim()
	aPIKeyValue3 := str.String(c.Models.Embedding.APIKey)
	c.Models.Embedding.APIKey = aPIKeyValue3.Trim()
	aPIValue2 := str.String(c.Models.Main.API)
	c.Models.Main.API = aPIValue2.Normalized()
	c.Models.Main.ReasoningEffort = modelprovider.ReasoningEffort(
		strings.TrimSpace(string(c.Models.Main.ReasoningEffort)),
	)
	aPIValue3 := str.String(c.Models.Summary.API)
	c.Models.Summary.API = aPIValue3.Normalized()
	levelValue := str.String(c.Log.Level)
	c.Log.Level = levelValue.Normalized()
	dirValue := str.String(c.Trace.Disk.Dir)
	c.Trace.Disk.Dir = dirValue.Trim()
	providerValue4 := str.String(c.Web.Provider)
	c.Web.Provider = providerValue4.Normalized()
	aPIKeyValue4 := str.String(c.Web.APIKey)
	c.Web.APIKey = aPIKeyValue4.Trim()
	baseURLValue3 := str.String(c.Web.BaseURL)
	c.Web.BaseURL = baseURLValue3.Trim()
	c.Web.BlockedDomains = dedupeAndTrim(c.Web.BlockedDomains)
	c.Web.BlockedDomainFiles = dedupeAndTrim(c.Web.BlockedDomainFiles)
	c.Web.NativeAllowedHosts = dedupeAndTrim(c.Web.NativeAllowedHosts)
	c.Web.NativeBlockedHosts = dedupeAndTrim(c.Web.NativeBlockedHosts)
	c.Web.NativeAllowedHostFiles = dedupeAndTrim(c.Web.NativeAllowedHostFiles)
	c.Web.NativeBlockedHostFiles = dedupeAndTrim(c.Web.NativeBlockedHostFiles)
	c.normalizeBrowser()
	addressValue := str.String(c.Gateway.Address)
	c.Gateway.Address = addressValue.Trim()
	authTokenValue := str.String(c.Gateway.AuthToken)
	c.Gateway.AuthToken = authTokenValue.Trim()
	pairingSecretValue := str.String(c.Gateway.PairingSecret)
	c.Gateway.PairingSecret = pairingSecretValue.Trim()
	c.Gateway.AllowedUsers = dedupeAndTrim(c.Gateway.AllowedUsers)
	modeValue := str.String(c.Gateway.Telegram.Mode)
	c.Gateway.Telegram.Mode = modeValue.Normalized()
	botTokenValue := str.String(c.Gateway.Telegram.BotToken)
	c.Gateway.Telegram.BotToken = botTokenValue.Trim()
	webhookSecretValue := str.String(c.Gateway.Telegram.WebhookSecret)
	c.Gateway.Telegram.WebhookSecret = webhookSecretValue.Trim()
	c.Gateway.Telegram.AllowedUsers = dedupeAndTrim(c.Gateway.Telegram.AllowedUsers)
	modeValue2 := str.String(c.Gateway.Slack.Mode)
	c.Gateway.Slack.Mode = modeValue2.Normalized()
	responseModeValue := str.String(c.Gateway.Slack.ResponseMode)
	c.Gateway.Slack.ResponseMode = responseModeValue.Normalized()
	botTokenValue2 := str.String(c.Gateway.Slack.BotToken)
	c.Gateway.Slack.BotToken = botTokenValue2.Trim()
	appTokenValue := str.String(c.Gateway.Slack.AppToken)
	c.Gateway.Slack.AppToken = appTokenValue.Trim()
	signingSecretValue := str.String(c.Gateway.Slack.SigningSecret)
	c.Gateway.Slack.SigningSecret = signingSecretValue.Trim()
	c.Gateway.Slack.AllowedUsers = dedupeAndTrim(c.Gateway.Slack.AllowedUsers)
	c.Rules.Files = normalizeRulePaths(c.Rules.Files)
	instructValue := str.String(c.Session.Instruct)
	c.Session.Instruct = instructValue.Trim()
	platformValue := str.String(c.Platform)
	c.Platform = platformValue.Normalized()
	c.FS.Roots = normalizeFSRoots(c.FS.Roots)
	c.Exec.Allow = dedupeAndTrim(c.Exec.Allow)
	c.Exec.Ask = dedupeAndTrim(c.Exec.Ask)
	c.Exec.Deny = dedupeAndTrim(c.Exec.Deny)
	c.Exec.AllowCommands = normalizeCommandSelectors(c.Exec.AllowCommands)
	c.Exec.AskCommands = normalizeCommandSelectors(c.Exec.AskCommands)
	c.Exec.DenyCommands = normalizeDenyCommandSelectors(c.Exec.DenyCommands)
	c.Exec.Shell = cleanOptionalPath(c.Exec.Shell)
	c.Permissions.Normalize()
	backendValue := str.String(c.Storage.Backend)
	c.Storage.Backend = backendValue.Normalized()
	providerValue5 := str.String(c.Memory.Provider)
	c.Memory.Provider = providerValue5.Normalized()
	backendValue2 := str.String(c.Memory.Backend)
	c.Memory.Backend = backendValue2.Normalized()
	trimmedValueValue := str.String(c.Reranker.Type)
	c.Reranker.Type = trimmedValueValue.Normalized()
	modelValue := str.String(c.Reranker.Model)
	c.Reranker.Model = modelValue.Trim()
	c.Reranker.Overrides = normalizeRerankerOverrides(c.Reranker.Overrides)
	c.normalizePersonalities()

	if c.Models.Main.Stream == nil {
		c.Models.Main.Stream = new(constants.DefaultProfileModelStream)
	}
	if c.Models.MaxRetries == nil {
		c.Models.MaxRetries = new(constants.DefaultModelMaxRetries)
	}
	if c.Models.Main.ContextLength <= 0 {
		c.Models.Main.ContextLength = constants.DefaultContextLength
	}

	if c.Models.Main.API == "" {
		c.Models.Main.API = c.getProviderAPIConfig(c.Models.Main.Provider)
	}
	if c.Models.Main.API == "" {
		c.Models.Main.API = getDefaultAPIForProvider(c.Models.Main.Provider)
	}

	if c.Log.Level == "" {
		c.Log.Level = constants.DefaultLogLevel
	}
	if c.Log.MaxSizeMB == 0 {
		c.Log.MaxSizeMB = constants.DefaultLogMaxSizeMB
	}
	if c.Log.MaxBackups == 0 {
		c.Log.MaxBackups = constants.DefaultLogMaxBackups
	}
	if c.Log.MaxAgeDays == 0 {
		c.Log.MaxAgeDays = constants.DefaultLogMaxAgeDays
	}
	if c.RPC.Address == "" {
		c.RPC.Address = constants.DefaultRPCAddress
	}

	if c.RPC.Port == 0 {
		c.RPC.Port = constants.DefaultRPCPort
	}
	c.normalizeAuth()
	if c.Gateway.Address == "" {
		c.Gateway.Address = constants.DefaultRPCAddress
	}
	if c.Gateway.Port == 0 {
		c.Gateway.Port = constants.DefaultGatewayPort
	}
	if c.Gateway.Telegram.Mode == "" {
		c.Gateway.Telegram.Mode = GatewayTelegramModePolling
	}
	if c.Gateway.Slack.Mode == "" {
		c.Gateway.Slack.Mode = GatewaySlackModeSocket
	}
	if c.Gateway.Slack.ResponseMode == "" {
		c.Gateway.Slack.ResponseMode = GatewaySlackResponseModeThread
	}
	if c.Session.MaxIterations == 0 {
		c.Session.MaxIterations = constants.DefaultMaxIterations
	}
	if c.Trace.Disk.Dir == "" {
		c.Trace.Disk.Dir = datadir.DebugTraceDir()
	}
	if c.Trace.Disk.Enabled == nil {
		c.Trace.Disk.Enabled = new(constants.DefaultProfileTraceDiskEnabled)
	}
	if c.Trace.Database.Enabled == nil {
		c.Trace.Database.Enabled = new(constants.DefaultProfileTraceDatabaseEnabled)
	}
	if c.Trace.Database.MaxEventsPerSession <= 0 {
		c.Trace.Database.MaxEventsPerSession = constants.DefaultTraceMaxEventsPerSession
	}
	if c.TUI.ThinkingComposer == nil {
		c.TUI.ThinkingComposer = new(constants.DefaultTUIThinkingComposerEnabled)
	}
	if c.Safety.Input == nil {
		c.Safety.Input = new(constants.DefaultSafetyInputEnabled)
	}
	if c.Safety.Output == nil {
		c.Safety.Output = new(constants.DefaultSafetyOutputEnabled)
	}
	if c.Safety.PII == nil {
		c.Safety.PII = new(constants.DefaultSafetyPIIEnabled)
	}
	if c.Platform == "" {
		c.Platform = constants.DefaultPlatform
	}
	if c.Web.MaxCharPerResult <= 0 {
		c.Web.MaxCharPerResult = constants.DefaultWebMaxCharPerResult
	}
	if c.Web.MaxExtractCharPerResult <= 0 {
		c.Web.MaxExtractCharPerResult = constants.DefaultWebMaxExtractCharPerResult
	}
	if c.Web.MaxExtractResponseBytes <= 0 {
		c.Web.MaxExtractResponseBytes = constants.DefaultWebMaxExtractResponseBytes
	}
	if c.Web.CacheTTL < 0 {
		c.Web.CacheTTL = constants.DefaultWebCacheTTL
	}
	if c.Web.ExtractMinSummarizeChars <= 0 {
		c.Web.ExtractMinSummarizeChars = constants.DefaultWebExtractMinSummarizeChars
	}
	if c.Web.ExtractMaxSummaryChars <= 0 {
		c.Web.ExtractMaxSummaryChars = constants.DefaultWebExtractMaxSummaryChars
	}
	if c.Web.ExtractMaxSummaryChunkChars <= 0 {
		c.Web.ExtractMaxSummaryChunkChars = constants.DefaultWebExtractMaxSummaryChunkChars
	}
	if c.Web.ExtractRefusalThresholdChars <= 0 {
		c.Web.ExtractRefusalThresholdChars = constants.DefaultWebExtractRefusalThresholdChars
	}

	if c.Cap.Filesystem == nil {
		c.Cap.Filesystem = new(constants.DefaultProfileCapabilityFilesystem)
	}
	if c.Cap.Network == nil {
		c.Cap.Network = new(constants.DefaultProfileCapabilityNetwork)
	}
	if c.Cap.Exec == nil {
		c.Cap.Exec = new(constants.DefaultProfileCapabilityExec)
	}
	if c.Cap.Memory == nil {
		c.Cap.Memory = new(constants.DefaultProfileCapabilityMemory)
	}
	if c.Cap.Browser == nil {
		c.Cap.Browser = new(constants.DefaultProfileCapabilityBrowser)
	}

	if len(c.FS.Roots) == 0 {
		c.FS.Roots = getDefaultFSRoots()
	}

	if c.Storage.Backend == "" {
		c.Storage.Backend = constants.DefaultStorageBackend
	}

	if c.Session.DefaultIdleExpiry <= 0 {
		c.Session.DefaultIdleExpiry = constants.DefaultSessionIdleExpiry
	}
	if c.Session.ArchiveRetention <= 0 {
		c.Session.ArchiveRetention = constants.DefaultArchiveRetention
	}
	if c.Compaction.Enabled == nil {
		c.Compaction.Enabled = new(constants.DefaultProfileCompactionEnabled)
	}
	if c.Compaction.TriggerPercent <= 0 {
		c.Compaction.TriggerPercent = constants.DefaultCompactionTrigger
	}
	if c.Compaction.WarnPercent <= 0 {
		c.Compaction.WarnPercent = constants.DefaultCompactionWarn
	}
	if c.Compaction.RecentSessionTail == nil {
		c.Compaction.RecentSessionTail = new(constants.RecentSessionTail)
	}
	if c.Memory.Enabled == nil {
		c.Memory.Enabled = new(constants.DefaultProfileMemoryEnabled)
	}
	if c.Memory.Provider == "" {
		c.Memory.Provider = constants.MemoryProviderDefault
	}
	if c.Memory.Pinned.Enabled == nil {
		c.Memory.Pinned.Enabled = new(constants.DefaultProfileMemoryPinnedEnabled)
	}
	if c.Memory.Retrieval.Enabled == nil {
		c.Memory.Retrieval.Enabled = new(constants.DefaultProfileMemoryRetrievalEnabled)
	}
	if c.Memory.Flush.Enabled == nil {
		c.Memory.Flush.Enabled = new(constants.DefaultProfileMemoryFlushEnabled)
	}
	if c.Memory.Flush.MaxCalls <= 0 {
		c.Memory.Flush.MaxCalls = constants.DefaultProfileMemoryFlushMaxCalls
	}
	if c.Memory.Flush.MaxOutputTokens <= 0 {
		c.Memory.Flush.MaxOutputTokens = constants.DefaultProfileMemoryFlushMaxOutputTokens
	}
	if c.Memory.Flush.Timeout <= 0 {
		c.Memory.Flush.Timeout = constants.DefaultProfileMemoryFlushTimeout
	}
	if c.Memory.Episodic.Enabled == nil {
		c.Memory.Episodic.Enabled = new(constants.DefaultMemoryEpisodicEnabled)
	}
	if c.Memory.Reflection.Enabled == nil {
		c.Memory.Reflection.Enabled = new(constants.DefaultMemoryReflectionEnabled)
	}
	if c.Memory.Promotion.Enabled == nil {
		c.Memory.Promotion.Enabled = new(constants.DefaultProfileMemoryPromotionEnabled)
	}
	if c.Memory.Promotion.EvaluatedRetention == 0 {
		c.Memory.Promotion.EvaluatedRetention = constants.DefaultProfileMemoryPromotionRetention
	}
	if c.Memory.Write.Enabled == nil {
		c.Memory.Write.Enabled = new(constants.DefaultProfileMemoryWriteEnabled)
	}

}

func normalizeCommandSelectors(values []commandplan.Selector) []commandplan.Selector {
	normalized, err := commandplan.NormalizeSelectors(values)
	if err != nil {
		return slices.Clone(values)
	}
	return normalized
}

func normalizeDenyCommandSelectors(values []commandplan.Selector) []commandplan.Selector {
	normalized, err := commandplan.NormalizeDenySelectors(values)
	if err != nil {
		return slices.Clone(values)
	}
	return normalized
}

func (c *Config) normalizeBrowser() {
	c.Browser.Executable = cleanOptionalPath(c.Browser.Executable)
	c.Browser.DefaultProfile = str.String(c.Browser.DefaultProfile).Normalized()
	if c.Browser.DefaultProfile == "" {
		c.Browser.DefaultProfile = DefaultBrowserProfileName
	}
	c.Browser.ProfileRoot = cleanOptionalPath(c.Browser.ProfileRoot)
	c.Browser.TemporaryRoot = cleanOptionalPath(c.Browser.TemporaryRoot)
	c.Browser.Artifacts.Root = cleanOptionalPath(c.Browser.Artifacts.Root)
	if c.Browser.ProfileRoot == "" {
		c.Browser.ProfileRoot = filepath.Join(datadir.HomeDir(), "browser", "profiles")
	}
	if c.Browser.TemporaryRoot == "" {
		c.Browser.TemporaryRoot = filepath.Join(datadir.HomeDir(), "browser", "tmp")
	}
	if c.Browser.Artifacts.Root == "" {
		c.Browser.Artifacts.Root = filepath.Join(datadir.HomeDir(), "browser", "artifacts")
	}
	if c.Browser.StartTimeout == 0 {
		c.Browser.StartTimeout = defaultBrowserStartTimeout
	}
	if c.Browser.InactivityTimeout == 0 {
		c.Browser.InactivityTimeout = defaultBrowserInactivityTimeout
	}
	if c.Browser.CleanupInterval == 0 {
		c.Browser.CleanupInterval = defaultBrowserCleanupInterval
	}
	if c.Browser.TerminalRetention == 0 {
		c.Browser.TerminalRetention = defaultBrowserTerminalRetention
	}
	if c.Browser.Artifacts.MaxBytes == 0 {
		c.Browser.Artifacts.MaxBytes = defaultBrowserArtifactMaxBytes
	}
	if c.Browser.Artifacts.MaxTotalBytes == 0 {
		c.Browser.Artifacts.MaxTotalBytes = defaultBrowserArtifactTotalBytes
	}
	if c.Browser.Artifacts.Retention == 0 {
		c.Browser.Artifacts.Retention = defaultBrowserArtifactRetention
	}
	if c.Browser.Network.Strict == nil {
		c.Browser.Network.Strict = new(true)
	}
	hosts := make([]string, 0, len(c.Browser.Network.DevelopmentAllowedHosts))
	for _, host := range c.Browser.Network.DevelopmentAllowedHosts {
		host = str.String(host).Normalized()
		if host != "" && !slices.Contains(hosts, host) {
			hosts = append(hosts, host)
		}
	}
	slices.Sort(hosts)
	c.Browser.Network.DevelopmentAllowedHosts = hosts
	c.Browser.Network.DevelopmentAllowedCIDRs = dedupeAndTrim(c.Browser.Network.DevelopmentAllowedCIDRs)

	profiles := make([]BrowserProfileConfig, 0, len(c.Browser.Profiles))
	for _, profile := range c.Browser.Profiles {
		profile.Name = str.String(profile.Name).Normalized()
		profile.Mode = str.String(profile.Mode).Normalized()
		profile.Directory = cleanOptionalPath(profile.Directory)
		profile.CDPEndpoint = str.String(profile.CDPEndpoint).Trim()
		profile.CredentialRef = str.String(profile.CredentialRef).Trim()
		profile.DataIdentity = str.String(profile.DataIdentity).Trim()
		profile.AttachmentScope = str.String(profile.AttachmentScope).Normalized()
		profile.BrowserContextID = str.String(profile.BrowserContextID).Trim()
		profile.TargetIDs = dedupeAndTrim(profile.TargetIDs)
		slices.Sort(profile.TargetIDs)
		profiles = append(profiles, profile)
	}
	if len(profiles) == 0 {
		profiles = []BrowserProfileConfig{{Name: DefaultBrowserProfileName, Mode: BrowserProfileManagedEphemeral}}
	}
	c.Browser.Profiles = profiles
}

func cleanOptionalPath(value string) string {
	value = str.String(value).Trim()
	if value == "" {
		return ""
	}

	return filepath.Clean(value)
}

func normalizeProviderModelConfigs(values map[string]ProviderModelConfig) map[string]ProviderModelConfig {
	if len(values) == 0 {
		return nil
	}

	normalized := make(map[string]ProviderModelConfig, len(values))
	for provider, value := range values {
		providerValue6 := str.String(provider)
		provider = providerValue6.Normalized()
		if provider == "" {
			continue
		}
		aPIKeyValue5 := str.String(value.APIKey)
		value.APIKey = aPIKeyValue5.Trim()
		value.APIKeyEnv = dedupeAndTrim(value.APIKeyEnv)
		aPIValue4 := str.String(value.API)
		value.API = aPIValue4.Normalized()
		baseURLValue4 := str.String(value.BaseURL)
		value.BaseURL = baseURLValue4.Trim()
		value.Headers = normalizeStringMap(value.Headers)
		value.Models = normalizeProviderModelMetadata(value.Models)
		normalized[provider] = value
	}
	if len(normalized) == 0 {
		return nil
	}

	return normalized
}

func normalizeProviderModelMetadata(values map[string]ProviderModelMetadata) map[string]ProviderModelMetadata {
	if len(values) == 0 {
		return nil
	}

	normalized := make(map[string]ProviderModelMetadata, len(values))
	for model, value := range values {
		modelValue2 := str.String(model)
		model = modelValue2.Trim()
		if model == "" {
			continue
		}

		if len(value.ReasoningEfforts) > 0 {
			efforts := make([]modelprovider.ReasoningEffort, 0, len(value.ReasoningEfforts))
			for _, effort := range value.ReasoningEfforts {
				efforts = append(efforts, modelprovider.ReasoningEffort(strings.TrimSpace(string(effort))))
			}
			value.ReasoningEfforts = efforts
		}
		value.ReasoningEffortDefault = modelprovider.ReasoningEffort(
			strings.TrimSpace(string(value.ReasoningEffortDefault)),
		)
		normalized[model] = value
	}
	if len(normalized) == 0 {
		return nil
	}

	return normalized
}

func normalizeRerankerOverrides(overrides map[string]RerankerOverrideConfig) map[string]RerankerOverrideConfig {
	if len(overrides) == 0 {
		return nil
	}

	normalized := make(map[string]RerankerOverrideConfig, len(overrides))
	for key, override := range overrides {
		keyValue := str.String(key)
		key = keyValue.Normalized()
		if key == "" {
			continue
		}
		trimmedValueValue2 := str.String(override.Type)
		override.Type = trimmedValueValue2.Normalized()
		modelValue3 := str.String(override.Model)
		override.Model = modelValue3.Trim()
		normalized[key] = override
	}
	if len(normalized) == 0 {
		return nil
	}

	return normalized
}

func (c *Config) normalizePersonalities() {
	if c == nil || len(c.Personalities) == 0 {
		return
	}

	normalized := make(map[string]PersonalityConfig, len(c.Personalities))
	for name, personality := range c.Personalities {
		nameValue5 := str.String(name)
		name = nameValue5.Normalized()
		soulValue := str.String(personality.Soul)
		personality.Soul = soulValue.Trim()
		instructValue2 := str.String(personality.Instruct)
		personality.Instruct = instructValue2.Trim()
		stateValue := str.String(personality.State)
		personality.State = stateValue.Normalized()
		if personality.State == "" {
			personality.State = personalityStateShared
		}
		memoryValue := str.String(personality.Tools.Memory)
		personality.Tools.Memory = memoryValue.Normalized()
		nameValue6 := str.String(personality.Model.Name)
		personality.Model.Name = nameValue6.Trim()
		providerValue7 := str.String(personality.Model.Provider)
		personality.Model.Provider = providerValue7.Normalized()
		aPIValue5 := str.String(personality.Model.API)
		personality.Model.API = aPIValue5.Normalized()
		baseURLValue5 := str.String(personality.Model.BaseURL)
		personality.Model.BaseURL = baseURLValue5.Trim()
		normalized[name] = personality
	}
	c.Personalities = normalized
}

func (c *Config) applyDefaultModelBaseURL() {
	if c == nil {
		return
	}

	mapped := getDefaultBaseURLForProvider(c.Models.Main.Provider, c.Models.Main.API)
	configured := c.getProviderBaseURLConfig(c.Models.Main.Provider)
	if configured != "" && (c.Models.Main.BaseURL == "" || isProviderDefaultBaseURL(c.Models.Main.BaseURL)) {
		c.Models.Main.BaseURL = configured
		return
	}
	if mapped == "" {
		return
	}
	if c.Models.Main.BaseURL == "" || isProviderDefaultBaseURL(c.Models.Main.BaseURL) {
		c.Models.Main.BaseURL = mapped
	}
}

func isProviderDefaultBaseURL(value string) bool {
	valueText := str.String(value).Trim()
	if valueText == "" {
		return false
	}

	for _, providerID := range modelRegistry.GetProviderIDs() {
		providerDef, ok := modelRegistry.GetProvider(providerID)
		if !ok {
			continue
		}
		for _, baseURL := range providerDef.BaseURLs {
			baseURLValue6 := str.String(baseURL)
			if valueText == baseURLValue6.Trim() {
				return true
			}
		}
	}

	return false
}

// Normalize trims fields, applies defaults, and resolves default model base URL when unset.
func (c *Config) Normalize() {
	if c == nil {
		return
	}

	c.normalizeFields()
	c.applyDefaultModelBaseURL()
}
