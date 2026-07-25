package config

import (
	"maps"
	"slices"
	"time"

	commandplan "github.com/wandxy/morph/internal/command"
	"github.com/wandxy/morph/internal/constants"
	"github.com/wandxy/morph/internal/permissions"
)

var DefaultConfig = Config{
	Name: constants.DefaultName,
	Models: ModelsConfig{
		Main: MainModelConfig{
			Stream:        new(constants.DefaultProfileModelStream),
			ContextLength: constants.DefaultContextLength,
		},
		Summary: SummaryModelConfig{},
		Embedding: EmbeddingModelConfig{
			Name: constants.DefaultProfileEmbeddingModel,
		},
		MaxRetries: new(constants.DefaultModelMaxRetries),
	},
	Session: SessionConfig{
		MaxIterations:     constants.DefaultMaxIterations,
		DefaultIdleExpiry: constants.DefaultSessionIdleExpiry,
		ArchiveRetention:  constants.DefaultArchiveRetention,
	},
	RPC: RPCConfig{
		Address: constants.DefaultRPCAddress,
		Port:    constants.DefaultRPCPort,
	},
	Auth: AuthConfig{
		Generation:        1,
		CLITokenTTL:       5 * time.Minute,
		TUITokenTTL:       8 * time.Hour,
		MaximumTokenTTL:   24 * time.Hour,
		SessionIdleTTL:    15 * time.Minute,
		SessionMaximumTTL: 24 * time.Hour,
		MaximumTokenBytes: 16 * 1024,
		NonceBytes:        24,
		TLS: AuthTLSConfig{
			Mode:           AuthTLSDisabled,
			MinimumVersion: "1.3",
		},
	},
	Gateway: GatewayConfig{
		Address: constants.DefaultRPCAddress,
		Port:    constants.DefaultGatewayPort,
		Telegram: GatewayTelegramConfig{
			Mode: GatewayTelegramModePolling,
		},
		Slack: GatewaySlackConfig{
			Mode:         GatewaySlackModeSocket,
			ResponseMode: GatewaySlackResponseModeThread,
		},
	},
	FS: FSConfig{
		NoProfileAccess: true,
	},
	Log: LogConfig{
		Level:      constants.DefaultProfileLogLevel,
		MaxSizeMB:  constants.DefaultLogMaxSizeMB,
		MaxBackups: constants.DefaultLogMaxBackups,
		MaxAgeDays: constants.DefaultLogMaxAgeDays,
		Compress:   constants.DefaultLogCompress,
	},
	Debug: DebugConfig{
		Requests: constants.DefaultProfileDebugRequests,
	},
	Trace: TraceConfig{
		Enabled: constants.DefaultProfileTraceEnabled,
		Disk: TraceDiskConfig{
			Enabled: new(constants.DefaultProfileTraceDiskEnabled),
		},
		Database: TraceDatabaseConfig{
			Enabled:             new(constants.DefaultProfileTraceDatabaseEnabled),
			MaxEventsPerSession: constants.DefaultTraceMaxEventsPerSession,
		},
	},
	TUI: TUIConfig{
		ThinkingComposer: new(constants.DefaultTUIThinkingComposerEnabled),
	},
	Safety: SafetyConfig{
		Input:  new(constants.DefaultSafetyInputEnabled),
		Output: new(constants.DefaultSafetyOutputEnabled),
		PII:    new(constants.DefaultSafetyPIIEnabled),
	},
	Web: WebConfig{
		Provider:                     constants.DefaultProfileWebProvider,
		MaxCharPerResult:             constants.DefaultWebMaxCharPerResult,
		MaxExtractCharPerResult:      constants.DefaultWebMaxExtractCharPerResult,
		MaxExtractResponseBytes:      constants.DefaultWebMaxExtractResponseBytes,
		CacheTTL:                     constants.DefaultProfileWebCacheTTL,
		BlockedDomainsEnabled:        constants.DefaultProfileWebBlockedDomainsEnabled,
		ExtractMinSummarizeChars:     constants.DefaultWebExtractMinSummarizeChars,
		ExtractMaxSummaryChars:       constants.DefaultWebExtractMaxSummaryChars,
		ExtractMaxSummaryChunkChars:  constants.DefaultWebExtractMaxSummaryChunkChars,
		ExtractRefusalThresholdChars: constants.DefaultWebExtractRefusalThresholdChars,
	},
	Browser: BrowserConfig{
		DefaultProfile:    DefaultBrowserProfileName,
		StartTimeout:      defaultBrowserStartTimeout,
		InactivityTimeout: defaultBrowserInactivityTimeout,
		CleanupInterval:   defaultBrowserCleanupInterval,
		TerminalRetention: defaultBrowserTerminalRetention,
		Profiles: []BrowserProfileConfig{{
			Name: DefaultBrowserProfileName,
			Mode: BrowserProfileManagedEphemeral,
		}},
		Network: BrowserNetworkConfig{Strict: new(true)},
		Artifacts: BrowserArtifactConfig{
			MaxBytes:      defaultBrowserArtifactMaxBytes,
			MaxTotalBytes: defaultBrowserArtifactTotalBytes,
			Retention:     defaultBrowserArtifactRetention,
		},
	},
	Platform: constants.DefaultPlatform,
	Cap: CapConfig{
		Filesystem: new(constants.DefaultProfileCapabilityFilesystem),
		Network:    new(constants.DefaultProfileCapabilityNetwork),
		Exec:       new(constants.DefaultProfileCapabilityExec),
		Memory:     new(constants.DefaultProfileCapabilityMemory),
		Browser:    new(constants.DefaultProfileCapabilityBrowser),
	},
	Storage: StorageConfig{
		Backend: constants.DefaultStorageBackend,
	},
	Search: SearchConfig{
		EnableRerank: new(constants.DefaultProfileSearchEnableRerank),
		Vector: SearchVectorConfig{
			Enabled:          constants.DefaultProfileSearchVectorEnabled,
			Required:         constants.DefaultProfileSearchVectorRequired,
			RebuildBatchSize: constants.DefaultVectorStoreRebuildBatchSize,
			MaxInputBytes:    constants.DefaultVectorMaxInputBytes,
			MaxDocumentBytes: constants.DefaultVectorMaxDocumentBytes,
		},
	},
	Reranker: RerankerConfig{
		Enabled:               new(constants.DefaultProfileRerankerEnabled),
		Type:                  constants.RerankerDeterministic,
		MaxCandidates:         constants.DefaultProfileRerankerMaxCandidates,
		MaxCandidateTextChars: constants.DefaultProfileRerankerMaxCandidateTextChars,
		MaxOutputTokens:       constants.DefaultProfileRerankerMaxOutputTokens,
		Overrides: map[string]RerankerOverrideConfig{
			"memory_episodic_extraction": {Type: constants.RerankerLLM},
			"memory_promotion":           {Type: constants.RerankerLLM},
			"memory_reflection":          {Type: constants.RerankerLLM},
		},
	},
	Compaction: CompactionConfig{
		Enabled:           new(constants.DefaultProfileCompactionEnabled),
		TriggerPercent:    constants.DefaultCompactionTrigger,
		WarnPercent:       constants.DefaultCompactionWarn,
		RecentSessionTail: new(constants.RecentSessionTail),
	},
	Memory: MemoryConfig{
		Enabled:  new(constants.DefaultProfileMemoryEnabled),
		Provider: constants.MemoryProviderDefault,
		Pinned: PinnedMemoryConfig{
			Enabled:      new(constants.DefaultProfileMemoryPinnedEnabled),
			MaxChars:     constants.DefaultProfileMemoryPinnedMaxChars,
			MaxItemChars: constants.DefaultProfileMemoryPinnedMaxItemChars,
		},
		Retrieval: RetrievalMemoryConfig{
			Enabled: new(constants.DefaultProfileMemoryRetrievalEnabled),
		},
		Flush: FlushMemoryConfig{
			Enabled:         new(constants.DefaultProfileMemoryFlushEnabled),
			MaxCalls:        constants.DefaultProfileMemoryFlushMaxCalls,
			MaxOutputTokens: constants.DefaultProfileMemoryFlushMaxOutputTokens,
			Timeout:         constants.DefaultProfileMemoryFlushTimeout,
		},
		Episodic: EpisodicMemoryConfig{
			Enabled:         new(constants.DefaultProfileMemoryEpisodicEnabled),
			Interval:        constants.DefaultProfileMemoryEpisodicInterval,
			IdleAfter:       constants.DefaultProfileMemoryEpisodicIdleAfter,
			MinMessages:     constants.DefaultProfileMemoryEpisodicMinMessages,
			WindowSize:      constants.DefaultProfileMemoryEpisodicWindowSize,
			MaxWindows:      constants.DefaultProfileMemoryEpisodicMaxWindows,
			MaxWindowChars:  constants.DefaultProfileMemoryEpisodicMaxWindowChars,
			MaxWindowTokens: constants.DefaultProfileMemoryEpisodicMaxWindowTokens,
			MaxRetries:      constants.DefaultProfileMemoryEpisodicMaxRetries,
		},
		Reflection: ReflectionMemoryConfig{
			Enabled:      new(constants.DefaultProfileMemoryReflectionEnabled),
			Interval:     constants.DefaultProfileMemoryReflectionInterval,
			Limit:        constants.DefaultProfileMemoryReflectionLimit,
			RelatedLimit: constants.DefaultProfileMemoryReflectionRelatedLimit,
		},
		Promotion: PromotionMemoryConfig{
			Enabled:            new(constants.DefaultProfileMemoryPromotionEnabled),
			Interval:           constants.DefaultProfileMemoryPromotionInterval,
			Limit:              constants.DefaultProfileMemoryPromotionLimit,
			EvaluatedRetention: constants.DefaultProfileMemoryPromotionRetention,
		},
		Write: WriteMemoryConfig{
			Enabled: new(constants.DefaultProfileMemoryWriteEnabled),
		},
	},
}

// NewDefaultConfig returns an independent default config instance.
func NewDefaultConfig() *Config {
	cfg := cloneConfig(DefaultConfig)
	cfg.FS.Roots = getDefaultFSRoots()

	return &cfg
}

func NewProfileConfig() *Config {
	cfg := NewDefaultConfig()
	cfg.Permissions.Preset = permissions.PresetApproveForMe
	cfg.Models.Main.Name = ""
	cfg.Models.Main.Provider = ""
	cfg.Models.Main.API = ""
	cfg.Models.Main.BaseURL = ""
	cfg.Models.Summary.Name = ""
	cfg.Models.Summary.Provider = ""
	cfg.Models.Summary.API = ""
	cfg.Models.Summary.BaseURL = ""
	cfg.Models.Embedding.Name = ""
	cfg.Models.Embedding.Provider = ""
	cfg.Models.Embedding.API = ""
	cfg.Models.Embedding.BaseURL = ""

	return cfg
}

func cloneConfig(cfg Config) Config {
	cfg.Models.MaxRetries = cloneIntPtr(cfg.Models.MaxRetries)
	cfg.Models.Providers = cloneProviderModelConfigs(cfg.Models.Providers)
	cfg.Models.Main.Stream = cloneBoolPtr(cfg.Models.Main.Stream)
	cfg.Search.EnableRerank = cloneBoolPtr(cfg.Search.EnableRerank)
	cfg.Memory.Enabled = cloneBoolPtr(cfg.Memory.Enabled)
	cfg.Memory.Pinned.Enabled = cloneBoolPtr(cfg.Memory.Pinned.Enabled)
	cfg.Memory.Retrieval.Enabled = cloneBoolPtr(cfg.Memory.Retrieval.Enabled)
	cfg.Memory.Flush.Enabled = cloneBoolPtr(cfg.Memory.Flush.Enabled)
	cfg.Memory.Episodic.Enabled = cloneBoolPtr(cfg.Memory.Episodic.Enabled)
	cfg.Memory.Reflection.Enabled = cloneBoolPtr(cfg.Memory.Reflection.Enabled)
	cfg.Memory.Promotion.Enabled = cloneBoolPtr(cfg.Memory.Promotion.Enabled)
	cfg.Memory.Write.Enabled = cloneBoolPtr(cfg.Memory.Write.Enabled)
	cfg.Reranker.Enabled = cloneBoolPtr(cfg.Reranker.Enabled)
	cfg.Reranker.Overrides = cloneRerankerOverrides(cfg.Reranker.Overrides)
	cfg.Compaction.Enabled = cloneBoolPtr(cfg.Compaction.Enabled)
	cfg.Compaction.RecentSessionTail = cloneIntPtr(cfg.Compaction.RecentSessionTail)
	cfg.Cap.Filesystem = cloneBoolPtr(cfg.Cap.Filesystem)
	cfg.Cap.Network = cloneBoolPtr(cfg.Cap.Network)
	cfg.Cap.Exec = cloneBoolPtr(cfg.Cap.Exec)
	cfg.Cap.Memory = cloneBoolPtr(cfg.Cap.Memory)
	cfg.Cap.Browser = cloneBoolPtr(cfg.Cap.Browser)
	cfg.Browser.Network.Strict = cloneBoolPtr(cfg.Browser.Network.Strict)
	cfg.Browser.Network.DevelopmentAllowedHosts = slices.Clone(cfg.Browser.Network.DevelopmentAllowedHosts)
	cfg.Browser.Network.DevelopmentAllowedCIDRs = slices.Clone(cfg.Browser.Network.DevelopmentAllowedCIDRs)
	cfg.Browser.Profiles = slices.Clone(cfg.Browser.Profiles)
	for index := range cfg.Browser.Profiles {
		cfg.Browser.Profiles[index].TargetIDs = slices.Clone(cfg.Browser.Profiles[index].TargetIDs)
	}
	cfg.Trace.Disk.Enabled = cloneBoolPtr(cfg.Trace.Disk.Enabled)
	cfg.Trace.Database.Enabled = cloneBoolPtr(cfg.Trace.Database.Enabled)
	cfg.TUI.ThinkingComposer = cloneBoolPtr(cfg.TUI.ThinkingComposer)
	cfg.Safety.Input = cloneBoolPtr(cfg.Safety.Input)
	cfg.Safety.Output = cloneBoolPtr(cfg.Safety.Output)
	cfg.Safety.PII = cloneBoolPtr(cfg.Safety.PII)
	cfg.FS.Roots = slices.Clone(cfg.FS.Roots)
	cfg.Exec.Allow = slices.Clone(cfg.Exec.Allow)
	cfg.Exec.Ask = slices.Clone(cfg.Exec.Ask)
	cfg.Exec.Deny = slices.Clone(cfg.Exec.Deny)
	cfg.Exec.AllowCommands = cloneCommandSelectors(cfg.Exec.AllowCommands)
	cfg.Exec.AskCommands = cloneCommandSelectors(cfg.Exec.AskCommands)
	cfg.Exec.DenyCommands = cloneCommandSelectors(cfg.Exec.DenyCommands)
	cfg.Permissions.SurfaceKindDefaults = maps.Clone(cfg.Permissions.SurfaceKindDefaults)
	cfg.Permissions.SurfaceDefaults = maps.Clone(cfg.Permissions.SurfaceDefaults)
	cfg.Permissions.Rules = slices.Clone(cfg.Permissions.Rules)
	for index := range cfg.Permissions.Rules {
		cfg.Permissions.Rules[index].Commands = cloneCommandSelectors(cfg.Permissions.Rules[index].Commands)
		cfg.Permissions.Rules[index].Profiles = slices.Clone(cfg.Permissions.Rules[index].Profiles)
		cfg.Permissions.Rules[index].ActorKinds = slices.Clone(cfg.Permissions.Rules[index].ActorKinds)
		cfg.Permissions.Rules[index].ActorIDs = slices.Clone(cfg.Permissions.Rules[index].ActorIDs)
		cfg.Permissions.Rules[index].ParentActorKinds = slices.Clone(cfg.Permissions.Rules[index].ParentActorKinds)
		cfg.Permissions.Rules[index].SurfaceKinds = slices.Clone(cfg.Permissions.Rules[index].SurfaceKinds)
		cfg.Permissions.Rules[index].Surfaces = slices.Clone(cfg.Permissions.Rules[index].Surfaces)
		cfg.Permissions.Rules[index].Tools = slices.Clone(cfg.Permissions.Rules[index].Tools)
		cfg.Permissions.Rules[index].Resources = slices.Clone(cfg.Permissions.Rules[index].Resources)
		cfg.Permissions.Rules[index].Actions = slices.Clone(cfg.Permissions.Rules[index].Actions)
		cfg.Permissions.Rules[index].Effects = slices.Clone(cfg.Permissions.Rules[index].Effects)
		cfg.Permissions.Rules[index].TargetScopes = slices.Clone(cfg.Permissions.Rules[index].TargetScopes)
		cfg.Permissions.Rules[index].TargetPrefixes = slices.Clone(cfg.Permissions.Rules[index].TargetPrefixes)
		cfg.Permissions.Rules[index].Network = slices.Clone(cfg.Permissions.Rules[index].Network)
	}
	cfg.Web.BlockedDomains = slices.Clone(cfg.Web.BlockedDomains)
	cfg.Web.BlockedDomainFiles = slices.Clone(cfg.Web.BlockedDomainFiles)
	cfg.Web.NativeAllowedHosts = slices.Clone(cfg.Web.NativeAllowedHosts)
	cfg.Web.NativeBlockedHosts = slices.Clone(cfg.Web.NativeBlockedHosts)
	cfg.Web.NativeAllowedHostFiles = slices.Clone(cfg.Web.NativeAllowedHostFiles)
	cfg.Web.NativeBlockedHostFiles = slices.Clone(cfg.Web.NativeBlockedHostFiles)
	cfg.Rules.Files = slices.Clone(cfg.Rules.Files)
	cfg.Personalities = clonePersonalityConfigs(cfg.Personalities)

	return cfg
}

func cloneRerankerOverrides(overrides map[string]RerankerOverrideConfig) map[string]RerankerOverrideConfig {
	if len(overrides) == 0 {
		return nil
	}

	cloned := make(map[string]RerankerOverrideConfig, len(overrides))
	maps.Copy(cloned, overrides)
	for useCase, override := range cloned {
		override.MaxCandidates = cloneIntPtr(override.MaxCandidates)
		override.MaxCandidateTextChars = cloneIntPtr(override.MaxCandidateTextChars)
		override.MaxOutputTokens = cloneIntPtr(override.MaxOutputTokens)
		cloned[useCase] = override
	}

	return cloned
}

func cloneProviderModelConfigs(values map[string]ProviderModelConfig) map[string]ProviderModelConfig {
	if len(values) == 0 {
		return nil
	}

	cloned := make(map[string]ProviderModelConfig, len(values))
	for provider, value := range values {
		value.APIKeyEnv = slices.Clone(value.APIKeyEnv)
		value.Headers = maps.Clone(value.Headers)
		value.Models = cloneProviderModelMetadata(value.Models)
		cloned[provider] = value
	}

	return cloned
}

func cloneProviderModelMetadata(values map[string]ProviderModelMetadata) map[string]ProviderModelMetadata {
	if len(values) == 0 {
		return nil
	}

	cloned := make(map[string]ProviderModelMetadata, len(values))
	for model, value := range values {
		value.SupportsTools = cloneBoolPtr(value.SupportsTools)
		value.SupportsVision = cloneBoolPtr(value.SupportsVision)
		value.Reasoning = cloneBoolPtr(value.Reasoning)
		cloned[model] = value
	}

	return cloned
}

func clonePersonalityConfigs(values map[string]PersonalityConfig) map[string]PersonalityConfig {
	if len(values) == 0 {
		return nil
	}

	cloned := make(map[string]PersonalityConfig, len(values))
	for name, personality := range values {
		personality.Memory.Pinned = cloneBoolPtr(personality.Memory.Pinned)
		personality.Memory.Retrieval = cloneBoolPtr(personality.Memory.Retrieval)
		personality.Memory.Write = cloneBoolPtr(personality.Memory.Write)
		personality.Memory.Episodic = cloneBoolPtr(personality.Memory.Episodic)
		personality.Memory.Reflection = cloneBoolPtr(personality.Memory.Reflection)
		personality.Memory.Promotion = cloneBoolPtr(personality.Memory.Promotion)
		personality.Memory.Flush = cloneBoolPtr(personality.Memory.Flush)
		personality.Tools.Filesystem = cloneBoolPtr(personality.Tools.Filesystem)
		personality.Tools.Network = cloneBoolPtr(personality.Tools.Network)
		personality.Tools.Exec = cloneBoolPtr(personality.Tools.Exec)
		personality.Model.Stream = cloneBoolPtr(personality.Model.Stream)
		cloned[name] = personality
	}

	return cloned
}

func cloneBoolPtr(value *bool) *bool {
	if value == nil {
		return nil
	}

	return new(*value)
}

func cloneCommandSelectors(values []commandplan.Selector) []commandplan.Selector {
	cloned := slices.Clone(values)
	for index := range cloned {
		cloned[index].ExactArguments = slices.Clone(cloned[index].ExactArguments)
		cloned[index].ArgumentPrefix = slices.Clone(cloned[index].ArgumentPrefix)
		cloned[index].Modes = slices.Clone(cloned[index].Modes)
		cloned[index].RequireComplete = cloneBoolPtr(cloned[index].RequireComplete)
	}
	return cloned
}

func cloneIntPtr(value *int) *int {
	if value == nil {
		return nil
	}

	return new(*value)
}
