package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xymorphic/morph/internal/constants"
	modelprovider "github.com/xymorphic/morph/internal/model/provider"
)

func TestConfig_NormalizeLeavesRulesFilesEmptyWhenUnset(t *testing.T) {
	cfg := &Config{}
	cfg.Normalize()
	require.Empty(t, cfg.Rules.Files)
}

func TestConfig_NormalizeNormalizesRulesFiles(t *testing.T) {
	cfg := &Config{Rules: RulesConfig{Files: []string{" ./Morph.md ", "custom.md", "Morph.md", ""}}}
	cfg.Normalize()
	require.Equal(t, []string{"Morph.md", "custom.md"}, cfg.Rules.Files)
}

func TestConfig_NormalizeTrimsInstruct(t *testing.T) {
	cfg := &Config{Session: SessionConfig{Instruct: "  be terse  "}}
	cfg.Normalize()
	require.Equal(t, "be terse", cfg.Session.Instruct)
}

func TestConfig_NormalizeLeavesProviderEmptyWhenUnset(t *testing.T) {
	cfg := &Config{
		Models: ModelsConfig{Main: MainModelConfig{Name: constants.DefaultModel}},
		Log:    LogConfig{Level: "info"},
	}
	cfg.Normalize()
	require.Empty(t, cfg.Models.Main.Provider)
	require.Empty(t, cfg.Models.Main.BaseURL)
}

func TestConfig_NormalizeIgnoresNilReceiver(t *testing.T) {
	var cfg *Config
	cfg.Normalize()
}

func TestConfig_NormalizeDefaultsModelAndLogLevel(t *testing.T) {
	cfg := &Config{}
	cfg.Normalize()
	require.Equal(t, constants.DefaultName, cfg.Name)
	require.Empty(t, cfg.Models.Main.Name)
	require.Empty(t, cfg.Models.Main.Provider)
	require.Equal(t, "cli", cfg.Platform)
	require.True(t, getBoolValue(cfg.Cap.Filesystem))
	require.True(t, getBoolValue(cfg.Cap.Network))
	require.True(t, getBoolValue(cfg.Cap.Exec))
	require.True(t, getBoolValue(cfg.Cap.Memory))
	require.False(t, getBoolValue(cfg.Cap.Browser))
	require.Empty(t, cfg.Models.Main.BaseURL)
	require.Equal(t, "127.0.0.1", cfg.RPC.Address)
	require.Equal(t, 50051, cfg.RPC.Port)
	require.Equal(t, constants.DefaultMaxIterations, cfg.Session.MaxIterations)
	require.Equal(t, "info", cfg.Log.Level)
	require.Equal(t, constants.DefaultWebMaxCharPerResult, cfg.Web.MaxCharPerResult)
	require.Equal(t, constants.DefaultWebMaxExtractCharPerResult, cfg.Web.MaxExtractCharPerResult)
	require.Equal(t, constants.DefaultWebMaxExtractResponseBytes, cfg.Web.MaxExtractResponseBytes)
	require.Equal(t, constants.DefaultWebCacheTTL, cfg.Web.CacheTTL)
	require.False(t, cfg.Web.BlockedDomainsEnabled)
	require.Empty(t, cfg.Web.BlockedDomains)
	require.Empty(t, cfg.Web.BlockedDomainFiles)
	require.Empty(t, cfg.Web.NativeAllowedHosts)
	require.Empty(t, cfg.Web.NativeBlockedHosts)
	require.Empty(t, cfg.Web.NativeAllowedHostFiles)
	require.Empty(t, cfg.Web.NativeBlockedHostFiles)
	require.Equal(t, constants.DefaultWebExtractMinSummarizeChars, cfg.Web.ExtractMinSummarizeChars)
	require.Equal(t, constants.DefaultWebExtractMaxSummaryChars, cfg.Web.ExtractMaxSummaryChars)
	require.Equal(t, constants.DefaultWebExtractMaxSummaryChunkChars, cfg.Web.ExtractMaxSummaryChunkChars)
	require.Less(t, cfg.Web.ExtractMaxSummaryChunkChars, cfg.Web.MaxExtractCharPerResult)
	require.Equal(t, constants.DefaultWebExtractRefusalThresholdChars, cfg.Web.ExtractRefusalThresholdChars)
}

func TestConfig_NormalizeDisablesNegativeWebCacheTTL(t *testing.T) {
	cfg := &Config{Web: WebConfig{CacheTTL: -time.Second}}
	cfg.Normalize()
	require.Equal(t, constants.DefaultWebCacheTTL, cfg.Web.CacheTTL)
}

func TestConfig_NormalizeTrimsWebBlockedDomains(t *testing.T) {
	cfg := &Config{
		Web: WebConfig{
			BlockedDomains:         []string{" blocked.example ", "blocked.example", ""},
			BlockedDomainFiles:     []string{" blocked.txt ", "blocked.txt", ""},
			NativeAllowedHosts:     []string{" allowed.example ", "allowed.example", ""},
			NativeBlockedHosts:     []string{" blocked.example ", "blocked.example", ""},
			NativeAllowedHostFiles: []string{" allow.txt ", "allow.txt", ""},
			NativeBlockedHostFiles: []string{" deny.txt ", "deny.txt", ""},
		},
	}

	cfg.Normalize()

	require.Equal(t, []string{"blocked.example"}, cfg.Web.BlockedDomains)
	require.Equal(t, []string{"blocked.txt"}, cfg.Web.BlockedDomainFiles)
	require.Equal(t, []string{"allowed.example"}, cfg.Web.NativeAllowedHosts)
	require.Equal(t, []string{"blocked.example"}, cfg.Web.NativeBlockedHosts)
	require.Equal(t, []string{"allow.txt"}, cfg.Web.NativeAllowedHostFiles)
	require.Equal(t, []string{"deny.txt"}, cfg.Web.NativeBlockedHostFiles)
}

func TestConfig_NormalizePreservesExplicitFalseCapabilities(t *testing.T) {
	cfg := &Config{
		Cap: CapConfig{
			Filesystem: new(false),
			Network:    new(false),
			Exec:       new(false),
			Memory:     new(false),
			Browser:    new(false),
		},
	}

	cfg.Normalize()

	require.False(t, getBoolValue(cfg.Cap.Filesystem))
	require.False(t, getBoolValue(cfg.Cap.Network))
	require.False(t, getBoolValue(cfg.Cap.Exec))
	require.False(t, getBoolValue(cfg.Cap.Memory))
	require.False(t, getBoolValue(cfg.Cap.Browser))
}

func TestConfig_NormalizeDefaultsUnsetCapabilitiesIndividually(t *testing.T) {
	cfg := &Config{Cap: CapConfig{Filesystem: new(false)}}

	cfg.Normalize()

	require.False(t, getBoolValue(cfg.Cap.Filesystem))
	require.True(t, getBoolValue(cfg.Cap.Network))
	require.True(t, getBoolValue(cfg.Cap.Exec))
	require.True(t, getBoolValue(cfg.Cap.Memory))
	require.False(t, getBoolValue(cfg.Cap.Browser))
}

func TestConfig_NormalizeUsesMappedBaseURLWhenProviderWasExplicitlySet(t *testing.T) {
	cfg := &Config{
		Models: ModelsConfig{Main: MainModelConfig{Name: constants.DefaultModel, Provider: constants.DefaultModelProvider}},
		Log:    LogConfig{Level: "info"},
	}
	cfg.Normalize()
	require.Equal(t, constants.DefaultModelProvider, cfg.Models.Main.Provider)
	require.Equal(t, getDefaultBaseURLForProvider(constants.DefaultModelProvider, modelprovider.APIOpenAIResponses), cfg.Models.Main.BaseURL)
}

func TestConfig_NormalizeKeepsOpenaiProvider(t *testing.T) {
	cfg := &Config{
		Models: ModelsConfig{Main: MainModelConfig{APIKey: "test-key", Name: constants.DefaultModel, Provider: "openai"}},
		Log:    LogConfig{Level: "info"},
	}
	cfg.Normalize()
	require.Equal(t, "openai", cfg.Models.Main.Provider)
	require.Equal(t, "https://api.openai.com/v1", cfg.Models.Main.BaseURL)
}

func TestConfig_NormalizeRemapsInheritedProviderDefaultBaseURL(t *testing.T) {
	cfg := NewDefaultConfig()
	cfg.Models.Main.Provider = "openai"

	cfg.Normalize()

	require.Equal(t, "openai", cfg.Models.Main.Provider)
	require.Equal(t, "https://api.openai.com/v1", cfg.Models.Main.BaseURL)
}

func TestConfig_NormalizeDefaultBaseURLDependsOnAPI(t *testing.T) {
	t.Run("openai uses api root for completions and responses", func(t *testing.T) {
		for _, mode := range []string{modelprovider.APIOpenAICompletions, modelprovider.APIOpenAIResponses} {
			cfg := &Config{Models: ModelsConfig{Main: MainModelConfig{Provider: "openai", API: mode}}}
			cfg.Normalize()
			require.Equal(t, "https://api.openai.com/v1", cfg.Models.Main.BaseURL, mode)
		}
	})

	t.Run("openrouter defaults differ by api mode", func(t *testing.T) {
		cfgChat := &Config{Models: ModelsConfig{Main: MainModelConfig{Provider: "openrouter", API: modelprovider.APIOpenAICompletions}}}
		cfgChat.Normalize()
		require.Equal(t, "https://openrouter.ai/api/v1", cfgChat.Models.Main.BaseURL)

		cfgResp := &Config{Models: ModelsConfig{Main: MainModelConfig{Provider: "openrouter", API: modelprovider.APIOpenAIResponses}}}
		cfgResp.Normalize()
		require.Equal(t, "https://openrouter.ai/api/v1", cfgResp.Models.Main.BaseURL)
	})

	t.Run("unknown api mode does not fall back to default base url", func(t *testing.T) {
		cfg := &Config{Models: ModelsConfig{Main: MainModelConfig{Provider: "openrouter", API: "future-mode"}}}
		cfg.Normalize()
		require.Empty(t, cfg.Models.Main.BaseURL)
	})
}

func TestConfig_NormalizeTrimsAndLowercasesFields(t *testing.T) {
	cfg := &Config{
		Name: "  Test Agent  ",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{
				" OpenAI ": {
					API:     " OPENAI-COMPLETIONS ",
					BaseURL: " https://provider.example/v1 ",
					Headers: map[string]string{
						" X-Test ": " value ",
						" blank ":  " ",
					},
					Models: map[string]ProviderModelMetadata{
						" gpt-4o ": {ContextLength: 128000},
						" ":        {ContextLength: 1},
					},
				},
			},
			Main: MainModelConfig{
				Name:     "  test-model  ",
				Provider: " OpenRouter ",
				APIKey:   "  test-key  ",
				BaseURL:  "  https://example.com/v1  ",
			},
		},
		Log: LogConfig{Level: " WARN "},
		Gateway: GatewayConfig{
			Slack: GatewaySlackConfig{ResponseMode: " MESSAGE "},
		},
	}
	cfg.Normalize()
	require.Equal(t, "Test Agent", cfg.Name)
	require.Equal(t, "test-model", cfg.Models.Main.Name)
	require.Equal(t, "openrouter", cfg.Models.Main.Provider)
	require.Equal(t, "test-key", cfg.Models.Main.APIKey)
	require.Equal(t, "https://example.com/v1", cfg.Models.Main.BaseURL)
	require.Contains(t, cfg.Models.Providers, "openai")
	require.Equal(t, modelprovider.APIOpenAICompletions, cfg.Models.Providers["openai"].API)
	require.Equal(t, "https://provider.example/v1", cfg.Models.Providers["openai"].BaseURL)
	require.Equal(t, map[string]string{"X-Test": "value"}, cfg.Models.Providers["openai"].Headers)
	require.Equal(t, map[string]ProviderModelMetadata{"gpt-4o": {ContextLength: 128000}}, cfg.Models.Providers["openai"].Models)
	require.Equal(t, "warn", cfg.Log.Level)
	require.Equal(t, GatewaySlackResponseModeMessage, cfg.Gateway.Slack.ResponseMode)
}

func TestConfig_NormalizeDropsBlankProviderModelConfigEntries(t *testing.T) {
	cfg := &Config{
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{
				" ": {
					Models: map[string]ProviderModelMetadata{" ": {ContextLength: 1}},
				},
			},
		},
	}

	cfg.Normalize()

	require.Empty(t, cfg.Models.Providers)
}

func TestNormalizeProviderModelMetadataReturnsNilForEmptyInput(t *testing.T) {
	require.Nil(t, normalizeProviderModelMetadata(nil))
	require.Nil(t, normalizeProviderModelMetadata(map[string]ProviderModelMetadata{" ": {ContextLength: 1}}))
}

func TestNormalizeFields_NilReceiver_NoPanic(t *testing.T) {
	var cfg *Config
	cfg.normalizeFields()
}

func TestNormalizeRulePaths_EmptyInput(t *testing.T) {
	require.Empty(t, normalizeRulePaths(nil))
}

func TestConfig_NormalizeDefaultsFilesystemRootsToCWD(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfg := &Config{}
	cfg.Normalize()
	require.Equal(t, []string{dir}, cfg.FS.Roots)
}
