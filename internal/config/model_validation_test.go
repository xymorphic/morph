package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/wandxy/morph/internal/constants"
	modelprovider "github.com/wandxy/morph/internal/model/provider"
)

func TestLoad_UsesRegistryModelMetadataWhenContextLengthIsUnset(t *testing.T) {
	stubModelRegistry(t, registryWithGenerationModel(constants.ModelProviderOpenRouter, "openai/test-chat-small", 8191))

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: config-agent
models:
  providers:
    openrouter:
      apiKey: config-key
  main:
    name: openai/test-chat-small
    provider: openrouter
    contextLength: 0
rpc:
  address: 127.0.0.1
  port: 50051
log:
  level: info
`), 0o600))

	cfg, err := Load("", configPath)
	require.NoError(t, err)
	require.Equal(t, 8191, cfg.Models.Main.ContextLength)
}

func TestLoad_UsesRegistryModelMetadataWhenConfiguredContextLengthIsTooLarge(t *testing.T) {
	stubModelRegistry(t, registryWithGenerationModel(constants.ModelProviderOpenAI, "openai/test-chat-small", 8191))

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: config-agent
models:
  providers:
    openai:
      apiKey: config-key
  main:
    name: openai/test-chat-small
    provider: openai
    contextLength: 999999
rpc:
  address: 127.0.0.1
  port: 50051
log:
  level: info
`), 0o600))

	cfg, err := Load("", configPath)
	require.NoError(t, err)
	require.Equal(t, 8191, cfg.Models.Main.ContextLength)
}

func TestLoad_LeavesFreeFormModelContextLengthAtDefault(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: config-agent
models:
  providers:
    openrouter:
      apiKey: config-key
  main:
    name: openai/unregistered-model
    provider: openrouter
rpc:
  address: 127.0.0.1
  port: 50051
log:
  level: info
`), 0o600))

	cfg, err := Load("", configPath)
	require.NoError(t, err)
	require.Equal(t, constants.DefaultContextLength, cfg.Models.Main.ContextLength)
}

func TestConfig_ValidateRequiresModelWhenEmpty(t *testing.T) {
	cfg := &Config{Name: "test-agent", Models: ModelsConfig{Providers: map[string]ProviderModelConfig{"openrouter": {APIKey: "test-key"}}}, Log: LogConfig{Level: "info"}}
	require.EqualError(t, cfg.Validate(), "model is required")
}

func TestConfig_ValidateAcceptsProviderNativeModelID(t *testing.T) {
	err := (&Config{
		Name:   "test-agent",
		Models: ModelsConfig{Main: MainModelConfig{APIKey: "test-key", Name: "gpt-4o-mini", Provider: "openai"}},
		RPC:    RPCConfig{Address: "127.0.0.1", Port: 50051},
		Log:    LogConfig{Level: "info"},
	}).Validate()

	require.NoError(t, err)
}

func TestConfig_ValidateRejectsModelWithEmptyOwnerOrName(t *testing.T) {
	cases := []string{"/gpt-4o-mini", "openai/", "gpt-4o-mini//extra"}

	for _, model := range cases {
		t.Run(model, func(t *testing.T) {
			err := (&Config{
				Name:   "test-agent",
				Models: ModelsConfig{Main: MainModelConfig{APIKey: "test-key", Name: model, Provider: "openai"}},
				RPC:    RPCConfig{Address: "127.0.0.1", Port: 50051},
				Log:    LogConfig{Level: "info"},
			}).Validate()

			require.EqualError(t, err, "model is required")
		})
	}
}

func TestConfig_ValidateRejectsUnsupportedProvider(t *testing.T) {
	openRouterDefault := getDefaultBaseURLForProvider(constants.DefaultModelProvider, modelprovider.APIOpenAIResponses)
	err := (&Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openrouter": {APIKey: "test-key"}},
			Main:      MainModelConfig{Name: constants.DefaultModel, Provider: "missing", BaseURL: openRouterDefault},
		},
		Log: LogConfig{Level: "info"},
	}).Validate()
	require.EqualError(t, err, "model provider must be one of: anthropic, github-copilot, ollama, openai, openai-codex, openrouter")
}

func TestConfig_ValidateRejectsProviderAPIIncompatibilityWithoutNetwork(t *testing.T) {
	err := (&Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Main: MainModelConfig{
				APIKey:   "test-key",
				Name:     constants.DefaultModel,
				Provider: "anthropic",
				API:      modelprovider.APIOpenAIResponses,
				BaseURL:  "https://api.example/v1",
			},
		},
		RPC: RPCConfig{Address: "127.0.0.1", Port: 50051},
		Log: LogConfig{Level: "info"},
	}).Validate()

	require.EqualError(t, err, `model API "openai-responses" is not supported by provider "anthropic"`)
}

func TestValidateReasoningSettings_AllowsDormantProfilePreference(t *testing.T) {
	err := validateReasoningSettings(ModelsConfig{
		Main: MainModelConfig{ReasoningEffort: "future-provider-level"},
	})

	require.NoError(t, err)
}

func TestValidateReasoningSettings_PreservesNoneAsExplicitPreference(t *testing.T) {
	cfg := ModelsConfig{Main: MainModelConfig{ReasoningEffort: " none "}}
	config := &Config{Models: cfg}
	config.Normalize()

	require.Equal(t, modelprovider.ReasoningEffort("none"), config.Models.Main.ReasoningEffort)
	require.NoError(t, validateReasoningSettings(config.Models))
}

func TestValidateReasoningSettings_RejectsReservedProfilePreference(t *testing.T) {
	err := validateReasoningSettings(ModelsConfig{
		Main: MainModelConfig{ReasoningEffort: "RESET"},
	})

	require.EqualError(t, err, `models.main.reasoningEffort "RESET" is reserved`)
}

func TestValidateReasoningSettings_ValidatesCustomModelMetadata(t *testing.T) {
	reasoning := true
	valid := ModelsConfig{
		Providers: map[string]ProviderModelConfig{
			"example": {
				Models: map[string]ProviderModelMetadata{
					"reasoner": {
						Reasoning:              &reasoning,
						ReasoningEfforts:       []modelprovider.ReasoningEffort{"high", "low"},
						ReasoningEffortDefault: "high",
					},
				},
			},
		},
	}
	require.NoError(t, validateReasoningSettings(valid))

	invalid := valid
	metadata := invalid.Providers["example"].Models["reasoner"]
	metadata.ReasoningEfforts = []modelprovider.ReasoningEffort{"low", "LOW"}
	invalid.Providers["example"].Models["reasoner"] = metadata
	err := validateReasoningSettings(invalid)
	require.EqualError(
		t,
		err,
		`models.providers.example.models.reasoner reasoning metadata is invalid: effort value "LOW" is duplicated`,
	)
}

func TestConfig_ValidateRejectsFreeFormModelWhenProviderRequiresKnownModels(t *testing.T) {
	stubModelRegistry(t, modelprovider.NewRegistry(
		[]modelprovider.APIDefinition{{ID: modelprovider.APIOpenAIResponses}},
		[]modelprovider.ProviderDefinition{
			{
				ID:                 "strict",
				DefaultAPI:         modelprovider.APIOpenAIResponses,
				SupportsModels:     true,
				RequiresKnownModel: true,
				BaseURLs: map[string]string{
					modelprovider.APIOpenAIResponses: "https://strict.example/v1",
				},
			},
		},
		[]modelprovider.ModelDefinition{
			{
				ID:       "known/model",
				Provider: "strict",
				API:      modelprovider.APIOpenAIResponses,
			},
		},
	))

	err := (&Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Main: MainModelConfig{
				APIKey:   "test-key",
				Name:     "unknown/model",
				Provider: "strict",
			},
		},
		RPC: RPCConfig{Address: "127.0.0.1", Port: 50051},
		Log: LogConfig{Level: "info"},
	}).Validate()

	require.EqualError(t, err, `models.main.name "unknown/model" is not registered for provider "strict"`)
}

func TestConfig_ValidateRejectsFreeFormModelWhenProviderDoesNotSupportModels(t *testing.T) {
	stubModelRegistry(t, modelprovider.NewRegistry(
		[]modelprovider.APIDefinition{{ID: modelprovider.APIOpenAIResponses}},
		[]modelprovider.ProviderDefinition{
			{
				ID:         "fixed",
				DefaultAPI: modelprovider.APIOpenAIResponses,
				BaseURLs: map[string]string{
					modelprovider.APIOpenAIResponses: "https://fixed.example/v1",
				},
			},
		},
		[]modelprovider.ModelDefinition{
			{
				ID:       "known/model",
				Provider: "fixed",
				API:      modelprovider.APIOpenAIResponses,
			},
		},
	))

	err := (&Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Main: MainModelConfig{
				APIKey:   "test-key",
				Name:     "unknown/model",
				Provider: "fixed",
			},
		},
		RPC: RPCConfig{Address: "127.0.0.1", Port: 50051},
		Log: LogConfig{Level: "info"},
	}).Validate()

	require.EqualError(t, err, `models.main.name "unknown/model" is not registered for provider "fixed"`)
}

func TestConfig_ValidateAllowsFreeFormModelForProviderThatSupportsModels(t *testing.T) {
	err := (&Config{
		Name:   "test-agent",
		Models: ModelsConfig{Main: MainModelConfig{APIKey: "test-key", Name: "openai/gpt-unknown", Provider: "openrouter"}},
		RPC:    RPCConfig{Address: "127.0.0.1", Port: 50051},
		Log:    LogConfig{Level: "info"},
	}).Validate()

	require.NoError(t, err)
}

func TestConfig_ValidateRejectsKnownModelWithIncompatibleRole(t *testing.T) {
	err := (&Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openai": {APIKey: "test-key"}},
			Main:      MainModelConfig{Name: constants.DefaultProfileEmbeddingModel, Provider: "openai"},
		},
		RPC: RPCConfig{Address: "127.0.0.1", Port: 50051},
		Log: LogConfig{Level: "info"},
	}).Validate()

	require.EqualError(t, err, `models.main.name "text-embedding-3-small" is not compatible with this model role`)
}

func TestConfig_ValidateAllowsFreeFormSummaryModelForProviderThatSupportsModels(t *testing.T) {
	err := (&Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openrouter": {APIKey: "test-key"}},
			Main:      MainModelConfig{Name: constants.DefaultModel, Provider: "openrouter"},
			Summary:   SummaryModelConfig{Name: "openai/gpt-unknown-summary"},
		},
		RPC: RPCConfig{Address: "127.0.0.1", Port: 50051},
		Log: LogConfig{Level: "info"},
	}).Validate()

	require.NoError(t, err)
}

func TestConfig_ValidateRejectsInvalidSummaryModelAPI(t *testing.T) {
	err := (&Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openrouter": {APIKey: "test-key"}},
			Main:      MainModelConfig{Name: constants.DefaultModel, Provider: "openrouter"},
			Summary:   SummaryModelConfig{API: "invalid"},
		},
		RPC: RPCConfig{Address: "127.0.0.1", Port: 50051},
		Log: LogConfig{Level: "info"},
	}).Validate()

	require.EqualError(t, err, "summary model API must be one of: anthropic-messages, ollama-native, openai-completions, openai-responses")
}

func TestConfig_ValidateAcceptsSummaryModelAPIResponses(t *testing.T) {
	err := (&Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openrouter": {APIKey: "test-key"}},
			Main:      MainModelConfig{Name: constants.DefaultModel, Provider: "openrouter"},
			Summary:   SummaryModelConfig{API: modelprovider.APIOpenAIResponses},
		},
		RPC: RPCConfig{Address: "127.0.0.1", Port: 50051},
		Log: LogConfig{Level: "info"},
	}).Validate()

	require.NoError(t, err)
}

func TestConfig_ValidateAcceptsSummaryModelAPICompletions(t *testing.T) {
	err := (&Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openrouter": {APIKey: "test-key"}},
			Main:      MainModelConfig{Name: constants.DefaultModel, Provider: "openrouter"},
			Summary:   SummaryModelConfig{API: modelprovider.APIOpenAICompletions},
		},
		RPC: RPCConfig{Address: "127.0.0.1", Port: 50051},
		Log: LogConfig{Level: "info"},
	}).Validate()

	require.NoError(t, err)
}

func TestLoad_UsesModelAPIFromConfig(t *testing.T) {
	clearEnvKeys(t, "MORPH_MODEL_API")

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: config-agent
models:
  providers:
    openrouter:
      apiKey: config-key
  main:
    name: config-model
    provider: openai
    api: openai-responses
rpc:
  address: 127.0.0.1
  port: 50051
log:
  level: info
`), 0o600))

	cfg, err := Load("", configPath)
	require.NoError(t, err)
	require.Equal(t, "openai-responses", cfg.Models.Main.API)
}

func TestConfig_ValidateRejectsInvalidAPI(t *testing.T) {
	for _, mode := range []string{"invalid", "embeddings"} {
		t.Run(mode, func(t *testing.T) {
			err := (&Config{
				Name: "test-agent",
				Models: ModelsConfig{
					Providers: map[string]ProviderModelConfig{"openai": {APIKey: "test-key"}},
					Main:      MainModelConfig{Name: constants.DefaultModel, Provider: "openai", API: mode},
				},
				RPC: RPCConfig{Address: "127.0.0.1", Port: 50051},
				Log: LogConfig{Level: "info"},
			}).Validate()
			require.EqualError(t, err, "model API must be one of: anthropic-messages, ollama-native, openai-completions, openai-responses")
		})
	}
}

func TestConfig_ValidateAllowsResponsesModeWithOpenRouter(t *testing.T) {
	err := (&Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openrouter": {APIKey: "test-key"}},
			Main:      MainModelConfig{Name: constants.DefaultModel, Provider: "openrouter", API: modelprovider.APIOpenAIResponses},
		},
		RPC: RPCConfig{Address: "127.0.0.1", Port: 50051},
		Log: LogConfig{Level: "info"},
	}).Validate()
	require.NoError(t, err)
}

func TestConfig_ValidateAllowsAnthropicMessagesModel(t *testing.T) {
	cfg := &Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"anthropic": {APIKey: "test-key"}},
			Main: MainModelConfig{
				Name:     "claude-sonnet-4-5",
				Provider: "anthropic",
			},
			Summary: SummaryModelConfig{
				Name: "claude-3-haiku-20240307",
			},
		},
		RPC: RPCConfig{Address: "127.0.0.1", Port: 50051},
		Log: LogConfig{Level: "info"},
	}

	err := cfg.Validate()

	require.NoError(t, err)
	require.Equal(t, modelprovider.APIAnthropicMessages, cfg.Models.Main.API)
	require.Equal(t, constants.DefaultAnthropicBaseURL, cfg.Models.Main.BaseURL)
	require.Equal(t, 200000, cfg.Models.Main.ContextLength)
}

func TestConfig_ValidateAcceptsRegistryEmbeddingModelWithoutContextRequirement(t *testing.T) {
	cfg := Config{
		Name: "daemon",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openrouter": {APIKey: "key"}},
			Main:      MainModelConfig{Name: "openai/model", Provider: "openrouter", BaseURL: "https://example.com", API: modelprovider.APIOpenAICompletions},
			Embedding: EmbeddingModelConfig{Name: "text-embedding-3-small", Provider: "openrouter"},
		},
		RPC:        RPCConfig{Address: "127.0.0.1", Port: 50051},
		Session:    SessionConfig{MaxIterations: 1, DefaultIdleExpiry: time.Hour, ArchiveRetention: 24 * time.Hour},
		Log:        LogConfig{Level: "info"},
		Storage:    StorageConfig{Backend: "sqlite"},
		Search:     SearchConfig{Vector: SearchVectorConfig{Enabled: true}},
		Compaction: CompactionConfig{Enabled: new(true), TriggerPercent: 0.85, WarnPercent: 0.95},
	}

	err := cfg.Validate()

	require.NoError(t, err)
}

func TestConfig_ValidateRejectsKnownEmbeddingModelWithIncompatibleRole(t *testing.T) {
	cfg := Config{
		Name: "daemon",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openrouter": {APIKey: "key"}},
			Main:      MainModelConfig{Name: constants.DefaultProfileModel, Provider: "openrouter"},
			Embedding: EmbeddingModelConfig{Name: constants.DefaultProfileModel, Provider: "openrouter"},
		},
		RPC:        RPCConfig{Address: "127.0.0.1", Port: 50051},
		Session:    SessionConfig{MaxIterations: 1, DefaultIdleExpiry: time.Hour, ArchiveRetention: 24 * time.Hour},
		Log:        LogConfig{Level: "info"},
		Storage:    StorageConfig{Backend: "sqlite"},
		Search:     SearchConfig{Vector: SearchVectorConfig{Enabled: true}},
		Compaction: CompactionConfig{Enabled: new(true), TriggerPercent: 0.85, WarnPercent: 0.95},
	}

	err := cfg.Validate()

	require.EqualError(t, err, `models.embedding.name "minimax/minimax-m2.7" is not compatible with this model role`)
}

func TestConfig_ValidateAllowsFreeFormEmbeddingModelForProviderThatSupportsModels(t *testing.T) {
	cfg := Config{
		Name: "daemon",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openrouter": {APIKey: "key"}},
			Main:      MainModelConfig{Name: "openai/model", Provider: "openrouter", BaseURL: "https://example.com", API: modelprovider.APIOpenAICompletions},
			Embedding: EmbeddingModelConfig{Name: "openai/text-embedding-missing", Provider: "openrouter"},
		},
		RPC:        RPCConfig{Address: "127.0.0.1", Port: 50051},
		Session:    SessionConfig{MaxIterations: 1, DefaultIdleExpiry: time.Hour, ArchiveRetention: 24 * time.Hour},
		Log:        LogConfig{Level: "info"},
		Storage:    StorageConfig{Backend: "sqlite"},
		Search:     SearchConfig{Vector: SearchVectorConfig{Enabled: true}},
		Compaction: CompactionConfig{Enabled: new(true), TriggerPercent: 0.85, WarnPercent: 0.95},
	}

	err := cfg.Validate()

	require.NoError(t, err)
}

func TestConfig_ValidateRejectsLLMRerankerOverrideWithDifferentAPI(t *testing.T) {
	t.Setenv("COPILOT_GITHUB_TOKEN", "")
	stubModelProviderToken(t, func(string) (StoredModelCredential, error) {
		return StoredModelCredential{}, nil
	})

	cfg := Config{
		Name: "daemon",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"github-copilot": {APIKey: "key"}},
			Main:      MainModelConfig{Name: "gpt-4.1", Provider: "github-copilot", API: modelprovider.APIOpenAICompletions},
			Summary:   SummaryModelConfig{Name: "gpt-4.1", Provider: "github-copilot", API: modelprovider.APIOpenAICompletions},
		},
		RPC:     RPCConfig{Address: "127.0.0.1", Port: 50051},
		Session: SessionConfig{MaxIterations: 1, DefaultIdleExpiry: time.Hour, ArchiveRetention: 24 * time.Hour},
		Log:     LogConfig{Level: "info"},
		Storage: StorageConfig{Backend: "memory"},
		Search:  SearchConfig{Vector: SearchVectorConfig{Enabled: true}},
		Reranker: RerankerConfig{
			Type:  constants.RerankerLLM,
			Model: "gpt-4.1",
			Overrides: map[string]RerankerOverrideConfig{
				"memory_reflection": {
					Type:  constants.RerankerLLM,
					Model: "claude-sonnet-4.5",
				},
			},
		},
		Compaction: CompactionConfig{Enabled: new(true), TriggerPercent: 0.85, WarnPercent: 0.95},
	}

	err := cfg.Validate()

	require.EqualError(t, err, `reranker override "memory_reflection" model API must match reranker model API`)
}
