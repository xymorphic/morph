package model

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/constants"
	modelprovider "github.com/wandxy/morph/internal/model/provider"
	"github.com/wandxy/morph/pkg/logutils"
)

func TestListOptions_FiltersGenerationModelsAndOrdersDisplayDefaultFirst(t *testing.T) {
	options, err := ListOptions(OptionQuery{
		Provider: constants.ModelProviderOpenAI,
		Current:  constants.DefaultModel,
	})

	require.NoError(t, err)
	require.NotEmpty(t, options)
	require.Equal(t, "gpt-5.5", options[0].ID)
	require.True(t, options[0].DisplayDefault)
	current := findOption(t, options, constants.DefaultModel)
	require.True(t, current.Current)
	for _, option := range options {
		require.NotEqual(t, modelprovider.APIOpenAIEmbeddings, option.API)
		require.NotEmpty(t, option.Input)
	}
}

func TestListOptions_FiltersOAuthOnlyModels(t *testing.T) {
	options, err := ListOptions(OptionQuery{
		Provider:  constants.ModelProviderOpenAICodex,
		Current:   "gpt-5.4",
		OAuthOnly: true,
	})

	require.NoError(t, err)
	require.NotEmpty(t, options)
	require.Equal(t, "gpt-5.5", options[0].ID)
	require.True(t, options[0].DisplayDefault)
	current := findOption(t, options, "gpt-5.4")
	require.True(t, current.Current)
	for _, option := range options {
		require.True(t, option.SupportsOAuth)
	}
}

func TestListOptions_ExcludesOpenAIPlatformModelsFromOAuthOnly(t *testing.T) {
	options, err := ListOptions(OptionQuery{
		Provider:  constants.ModelProviderOpenAI,
		Current:   "gpt-5.2",
		OAuthOnly: true,
	})
	require.NoError(t, err)
	require.Empty(t, options)
}

func TestListOptions_FiltersAnthropicOAuthOnlyModels(t *testing.T) {
	options, err := ListOptions(OptionQuery{
		Provider:  constants.ModelProviderAnthropic,
		Current:   "claude-sonnet-4-6",
		OAuthOnly: true,
	})

	require.NoError(t, err)
	require.NotEmpty(t, options)
	require.Equal(t, "claude-sonnet-4-6", options[0].ID)
	require.True(t, options[0].DisplayDefault)
	require.True(t, options[0].Current)
	require.Len(t, options, 3)

	ids := make([]string, 0, len(options))
	for _, option := range options {
		require.True(t, option.SupportsOAuth, option.ID)
		ids = append(ids, option.ID)
	}
	require.ElementsMatch(t, []string{
		"claude-haiku-4-5",
		"claude-opus-4-7",
		"claude-sonnet-4-6",
	}, ids)
}

func TestListOptions_OrdersOpenRouterDisplayDefaultFirst(t *testing.T) {
	options, err := ListOptions(OptionQuery{
		Provider: constants.ModelProviderOpenRouter,
		Current:  "openai/gpt-5.5",
	})

	require.NoError(t, err)
	require.NotEmpty(t, options)
	require.Equal(t, constants.DefaultProfileModel, options[0].ID)
	require.True(t, options[0].DisplayDefault)
	current := findOption(t, options, "openai/gpt-5.5")
	require.True(t, current.Current)
}

func TestListOptions_ReturnsEmptyForMissingProviderOrRegistry(t *testing.T) {
	options, err := ListOptions(OptionQuery{Provider: "missing"})
	require.NoError(t, err)
	require.Empty(t, options)
	options, err = ListOptions(OptionQuery{
		Provider: "missing",
		Registry: modelprovider.NewRegistry(nil, nil, nil),
	})
	require.NoError(t, err)
	require.Empty(t, options)
}

func TestListOptions_IncludesLocalGenerationModels(t *testing.T) {
	registry := modelprovider.NewRegistry(
		[]modelprovider.APIDefinition{{ID: modelprovider.APIOllamaNative}},
		[]modelprovider.ProviderDefinition{{
			ID:             constants.ModelProviderOllama,
			DisplayName:    "Ollama",
			DefaultAPI:     modelprovider.APIOllamaNative,
			SupportsModels: true,
			Local: &modelprovider.LocalProviderDefinition{
				NativeChatAPI: modelprovider.APIOllamaNative,
				AuthMarker:    constants.OllamaLocalAuthMarker,
			},
		}},
		[]modelprovider.ModelDefinition{{
			ID:            "llama3.1:8b",
			Name:          "Llama 3.1 8B",
			Provider:      constants.ModelProviderOllama,
			API:           modelprovider.APIOllamaNative,
			Input:         []modelprovider.InputKind{modelprovider.InputText, modelprovider.InputImage},
			SupportsTools: true,
		}},
	)

	options, err := ListOptions(OptionQuery{Provider: constants.ModelProviderOllama, Registry: registry})
	require.NoError(t, err)
	require.Len(t, options, 1)
	require.Equal(t, "llama3.1:8b", options[0].ID)
	require.True(t, options[0].SupportsTools)
	require.Equal(t, []string{"text", "image"}, options[0].Input)

	providers := ListProviders(ProviderQuery{Registry: registry})
	require.Len(t, providers, 1)
	require.Equal(t, constants.ModelProviderOllama, providers[0].ID)
	require.Equal(t, "local", providers[0].Type)
	require.True(t, providers[0].Local)
}

func TestListOptions_MergesOllamaDiscoveryAndSuggestedModels(t *testing.T) {
	resetLocalDiscoveryCache(t)

	options, err := ListOptions(OptionQuery{
		Context:        context.Background(),
		Provider:       constants.ModelProviderOllama,
		Current:        constants.DefaultOllamaModel,
		BaseURL:        "http://127.0.0.1:11434",
		LocalDiscovery: true,
		DiscoverLocalModels: func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
			return []modelprovider.ModelDefinition{{
				ID:            "installed:latest",
				Name:          "Installed",
				Provider:      constants.ModelProviderOllama,
				API:           modelprovider.APIOllamaNative,
				Input:         []modelprovider.InputKind{modelprovider.InputText},
				ContextWindow: 4096,
			}}, nil
		},
	})

	require.NoError(t, err)
	logutils.PrettyPrint(options)
	require.Equal(t, "installed:latest", options[0].ID)
	require.False(t, options[0].LocalMissing)
	require.Equal(t, OptionSourceDiscovery, options[0].Source)
	require.Equal(t, "http://127.0.0.1:11434", options[0].BaseURL)
	defaultModel := findOption(t, options, constants.DefaultOllamaModel)
	require.True(t, defaultModel.LocalMissing)
	require.Equal(t, OptionSourceCatalog, defaultModel.Source)
}

func TestListOptions_UsesCachedOllamaDiscoveryUntilRefresh(t *testing.T) {
	resetLocalDiscoveryCache(t)

	calls := 0
	discover := func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
		calls++
		return []modelprovider.ModelDefinition{
			{ID: "cached:latest", Provider: constants.ModelProviderOllama,
				API:           modelprovider.APIOllamaNative,
				Input:         []modelprovider.InputKind{modelprovider.InputText},
				ContextWindow: 4096,
			},
		}, nil
	}
	query := OptionQuery{
		Context:             context.Background(),
		Provider:            constants.ModelProviderOllama,
		BaseURL:             "http://127.0.0.1:11434",
		LocalDiscovery:      true,
		DiscoveryTTL:        time.Minute,
		DiscoverLocalModels: discover,
	}

	options, err := ListOptions(query)
	require.NoError(t, err)
	require.Equal(t, "cached:latest", options[0].ID)
	require.Equal(t, 1, calls)

	options, err = ListOptions(query)
	require.NoError(t, err)
	require.Equal(t, "cached:latest", options[0].ID)
	require.Equal(t, 1, calls)

	query.Refresh = true
	_, err = ListOptions(query)
	require.NoError(t, err)
	require.Equal(t, 2, calls)
}

func TestListOptions_ReturnsLocalDiscoveryError(t *testing.T) {
	resetLocalDiscoveryCache(t)

	errDiscovery := errors.New("ollama unavailable")
	options, err := ListOptions(OptionQuery{
		Provider:       constants.ModelProviderOllama,
		BaseURL:        "http://127.0.0.1:11434",
		LocalDiscovery: true,
		DiscoverLocalModels: func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
			return nil, errDiscovery
		},
	})

	require.ErrorIs(t, err, errDiscovery)
	require.Nil(t, options)
}

func TestListOptions_ExplicitConfigOverridesOllamaDiscovery(t *testing.T) {
	resetLocalDiscoveryCache(t)

	supportsTools := true
	cfg := config.NewProfileConfig()
	cfg.Models.Providers = map[string]config.ProviderModelConfig{
		constants.ModelProviderOllama: {
			BaseURL: "http://configured.local:11434",
			Models: map[string]config.ProviderModelMetadata{
				constants.DefaultOllamaModel: {
					ContextLength:   8192,
					MaxOutputTokens: 2048,
					SupportsTools:   &supportsTools,
				},
			},
		},
	}

	options, err := ListOptions(OptionQuery{
		Context:        context.Background(),
		Provider:       constants.ModelProviderOllama,
		Current:        constants.DefaultOllamaModel,
		Config:         cfg,
		BaseURL:        "http://127.0.0.1:11434",
		LocalDiscovery: true,
		DiscoverLocalModels: func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
			t.Fatal("discovery should not run when provider model metadata is pinned")
			return nil, nil
		},
	})

	require.NoError(t, err)
	pinned := findOption(t, options, constants.DefaultOllamaModel)
	require.Equal(t, OptionSourceConfig, pinned.Source)
	require.Equal(t, modelprovider.APIOllamaNative, pinned.API)
	require.Equal(t, 8192, pinned.ContextWindow)
	require.Equal(t, 2048, pinned.MaxTokens)
	require.True(t, pinned.SupportsTools)
	require.False(t, pinned.LocalMissing)
	require.True(t, pinned.Current)
}

func TestListOptions_ExplicitConfigDisablesOllamaDiscoveryWhenFiltered(t *testing.T) {
	resetLocalDiscoveryCache(t)

	cfg := config.NewProfileConfig()
	cfg.Models.Providers = map[string]config.ProviderModelConfig{
		"Ollama": {
			API: modelprovider.APIOllamaEmbeddings,
			Models: map[string]config.ProviderModelMetadata{
				"pinned-embedding:latest": {},
			},
		},
	}

	options, err := ListOptions(OptionQuery{
		Context:        context.Background(),
		Provider:       constants.ModelProviderOllama,
		Config:         cfg,
		LocalDiscovery: true,
		DiscoverLocalModels: func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
			t.Fatal("discovery should not run when provider model metadata is pinned")
			return nil, nil
		},
	})

	require.NoError(t, err)
	require.NotContains(t, getOptionIDs(options), "pinned-embedding:latest")
	require.NotEmpty(t, options)
	require.Equal(t, OptionSourceCatalog, options[0].Source)
}

func TestListOptions_UsesConfiguredOllamaDiscoveryBaseURL(t *testing.T) {
	registry := modelprovider.NewRegistry(
		nil,
		[]modelprovider.ProviderDefinition{{
			ID:             constants.ModelProviderOllama,
			DefaultAPI:     "ollama-chat",
			SupportsModels: true,
			BaseURLs: map[string]string{
				"ollama-chat":       "http://chat.local:11434",
				"ollama-embeddings": "http://embeddings.local:11434",
			},
			Local: &modelprovider.LocalProviderDefinition{},
		}},
		nil,
	)

	tests := []struct {
		name     string
		queryURL string
		cfg      *config.Config
		want     string
	}{
		{
			name:     "query base URL",
			queryURL: "http://query.local:11434",
			cfg:      config.NewProfileConfig(),
			want:     "http://query.local:11434",
		},
		{
			name: "main base URL",
			cfg: func() *config.Config {
				cfg := config.NewProfileConfig()
				cfg.Models.Main.Provider = constants.ModelProviderOllama
				cfg.Models.Main.BaseURL = "http://main.local:11434"
				return cfg
			}(),
			want: "http://main.local:11434",
		},
		{
			name: "main API base URL",
			cfg: func() *config.Config {
				cfg := config.NewProfileConfig()
				cfg.Models.Main.Provider = constants.ModelProviderOllama
				cfg.Models.Main.API = "ollama-embeddings"
				return cfg
			}(),
			want: "http://embeddings.local:11434",
		},
		{
			name: "provider base URL",
			cfg: func() *config.Config {
				cfg := config.NewProfileConfig()
				cfg.Models.Providers = map[string]config.ProviderModelConfig{
					"Ollama": {BaseURL: "http://provider.local:11434"},
				}
				return cfg
			}(),
			want: "http://provider.local:11434",
		},
		{
			name: "provider API base URL",
			cfg: func() *config.Config {
				cfg := config.NewProfileConfig()
				cfg.Models.Providers = map[string]config.ProviderModelConfig{
					constants.ModelProviderOllama: {API: "ollama-embeddings"},
				}
				return cfg
			}(),
			want: "http://embeddings.local:11434",
		},
		{
			name: "provider default API base URL",
			cfg:  config.NewProfileConfig(),
			want: "http://chat.local:11434",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetLocalDiscoveryCache(t)
			var got string
			_, err := ListOptions(OptionQuery{
				Context:        context.Background(),
				Provider:       constants.ModelProviderOllama,
				Config:         tt.cfg,
				BaseURL:        tt.queryURL,
				LocalDiscovery: true,
				Refresh:        true,
				Registry:       registry,
				DiscoverLocalModels: func(_ context.Context, baseURL string) ([]modelprovider.ModelDefinition, error) {
					got = baseURL
					return nil, nil
				},
			})

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestListOptions_FiltersDiscoveredOllamaEmbeddingModels(t *testing.T) {
	resetLocalDiscoveryCache(t)

	options, err := ListOptions(OptionQuery{
		Context:        context.Background(),
		Provider:       constants.ModelProviderOllama,
		Current:        "chat:latest",
		LocalDiscovery: true,
		Refresh:        true,
		DiscoverLocalModels: func(context.Context, string) ([]modelprovider.ModelDefinition, error) {
			return []modelprovider.ModelDefinition{
				{
					ID:       "chat:latest",
					Name:     "Chat",
					Provider: constants.ModelProviderOllama,
					API:      modelprovider.APIOllamaNative,
					Input:    []modelprovider.InputKind{modelprovider.InputText},
				},
				{
					ID:       "nomic-embed-text:latest",
					Name:     "nomic-embed-text",
					Provider: constants.ModelProviderOllama,
					API:      modelprovider.APIOllamaEmbeddings,
					Input:    []modelprovider.InputKind{modelprovider.InputText},
				},
			}, nil
		},
	})

	require.NoError(t, err)
	require.Contains(t, getOptionIDs(options), "chat:latest")
	require.NotContains(t, getOptionIDs(options), "nomic-embed-text:latest")
	require.Equal(t, OptionSourceDiscovery, findOption(t, options, "chat:latest").Source)
}

func TestCatalogHelpersHandleFallbacks(t *testing.T) {
	require.Empty(t, getProviderDefaultAPI(nil, "missing"))
	require.Equal(t, modelprovider.APIOllamaNative, getProviderDefaultAPI(nil, constants.ModelProviderOllama))

	_, ok := getExplicitProviderConfig(config.NewProfileConfig(), constants.ModelProviderOllama)
	require.False(t, ok)
	cfg := config.NewProfileConfig()
	cfg.Models.Providers = map[string]config.ProviderModelConfig{
		"openai": {},
	}
	_, ok = getExplicitProviderConfig(cfg, constants.ModelProviderOllama)
	require.False(t, ok)

	options := mergeOptions(
		[]Option{{ID: " installed:latest "}, {ID: " "}},
		[]Option{
			{ID: "installed:latest"},
			{ID: "missing:latest"},
			{ID: " "},
		},
		true,
	)
	require.Equal(t, []string{"installed:latest", "missing:latest"}, getOptionIDs(options))
	require.False(t, options[0].LocalMissing)
	require.True(t, options[1].LocalMissing)

	converted := modelDefinitionsToOptions(
		[]modelprovider.ModelDefinition{
			{ID: " "},
			{
				ID:    "vision:latest",
				API:   modelprovider.APIOllamaNative,
				Input: []modelprovider.InputKind{" ", modelprovider.InputImage},
			},
		},
		"vision:latest",
		"http://local",
		OptionSourceDiscovery,
	)
	require.Len(t, converted, 1)
	require.Equal(t, "vision:latest", converted[0].ID)
	require.Equal(t, []string{"image"}, converted[0].Input)
	require.True(t, converted[0].Current)
	require.Equal(t, "http://local", converted[0].BaseURL)
	require.Equal(t, OptionSourceDiscovery, converted[0].Source)
}

func TestMergeOptions_UsesExactModelTupleAndClonesReasoningEfforts(t *testing.T) {
	primaryEfforts := []modelprovider.ReasoningEffort{"high", "low"}
	primary := []Option{{
		ID:       "shared",
		Provider: "example",
		API:      "responses",
		ReasoningCapabilities: modelprovider.ReasoningCapability{
			Efforts:       primaryEfforts,
			DefaultEffort: "high",
		},
	}}
	secondary := []Option{
		{ID: "shared", Provider: "example", API: "responses"},
		{ID: "shared", Provider: "example", API: "completions"},
	}

	merged := mergeOptions(primary, secondary, false)
	require.Len(t, merged, 2)
	require.Equal(t, "responses", merged[0].API)
	require.Equal(t, "completions", merged[1].API)

	primaryEfforts[0] = "changed"
	require.Equal(
		t,
		[]modelprovider.ReasoningEffort{"high", "low"},
		merged[0].ReasoningCapabilities.Efforts,
	)
}

func TestListOptions_MarksCurrentByExactAPI(t *testing.T) {
	registry := modelprovider.NewRegistry(
		nil,
		nil,
		[]modelprovider.ModelDefinition{
			{ID: "shared", Provider: "example", API: modelprovider.APIOpenAIResponses},
			{ID: "shared", Provider: "example", API: modelprovider.APIOpenAICompletions},
		},
	)

	options, err := ListOptions(OptionQuery{
		Provider: "example",
		API:      modelprovider.APIOpenAICompletions,
		Current:  "shared",
		Registry: registry,
	})
	require.NoError(t, err)
	require.Len(t, options, 2)

	for _, option := range options {
		require.Equal(t, option.API == modelprovider.APIOpenAICompletions, option.Current)
	}
}

func TestListOptions_MarksCurrentUsingProviderDefaultAPI(t *testing.T) {
	registry := modelprovider.NewRegistry(
		nil,
		[]modelprovider.ProviderDefinition{{
			ID:         "example",
			DefaultAPI: modelprovider.APIOpenAICompletions,
		}},
		[]modelprovider.ModelDefinition{
			{ID: "shared", Provider: "example", API: modelprovider.APIOpenAIResponses},
			{ID: "shared", Provider: "example", API: modelprovider.APIOpenAICompletions},
		},
	)

	options, err := ListOptions(OptionQuery{
		Provider: "example",
		Current:  "shared",
		Registry: registry,
	})
	require.NoError(t, err)
	require.Len(t, options, 2)

	for _, option := range options {
		require.Equal(t, option.API == modelprovider.APIOpenAICompletions, option.Current)
	}
}

func TestListExplicitConfigOptionsFiltersAndMapsMetadata(t *testing.T) {
	supportsVision := true
	cfg := config.NewProfileConfig()
	cfg.Models.Providers = map[string]config.ProviderModelConfig{
		constants.ModelProviderOllama: {
			API:     modelprovider.APIOllamaNative,
			BaseURL: "http://configured.local:11434",
			Models: map[string]config.ProviderModelMetadata{
				" ": {},
				"vision:latest": {
					SupportsVision: &supportsVision,
				},
			},
		},
	}

	options := listExplicitConfigOptions(
		cfg,
		nil,
		constants.ModelProviderOllama,
		"vision:latest",
		false,
	)
	require.Len(t, options, 1)
	require.Equal(t, "vision:latest", options[0].ID)
	require.Equal(t, []string{"text", "image"}, options[0].Input)
	require.True(t, options[0].Current)
	require.True(t, options[0].DisplayDefault)

	require.Empty(t, listExplicitConfigOptions(
		cfg,
		nil,
		constants.ModelProviderOllama,
		"vision:latest",
		true,
	))
}

func TestListExplicitConfigOptionsMapsReasoningMetadata(t *testing.T) {
	reasoning := true
	summary := true
	cfg := config.NewProfileConfig()
	cfg.Models.Providers = map[string]config.ProviderModelConfig{
		constants.ModelProviderOpenAI: {
			API: modelprovider.APIOpenAIResponses,
			Models: map[string]config.ProviderModelMetadata{
				"custom-reasoner": {
					Reasoning:              &reasoning,
					ReasoningEfforts:       []modelprovider.ReasoningEffort{"high", "low"},
					ReasoningEffortDefault: "high",
					ReasoningSummary:       &summary,
				},
			},
		},
	}

	options := listExplicitConfigOptions(
		cfg,
		nil,
		constants.ModelProviderOpenAI,
		"custom-reasoner",
		false,
	)
	require.Len(t, options, 1)
	require.True(t, options[0].Reasoning)
	require.Equal(t, []modelprovider.ReasoningEffort{"high", "low"}, options[0].ReasoningCapabilities.Efforts)
	require.Equal(t, modelprovider.ReasoningEffort("high"), options[0].ReasoningCapabilities.DefaultEffort)
	require.True(t, options[0].ReasoningCapabilities.Summary)
}

func findOption(t *testing.T, options []Option, id string) Option {
	t.Helper()

	for _, option := range options {
		if option.ID == id {
			return option
		}
	}

	t.Fatalf("model option %q not found", id)
	return Option{}
}

func getOptionIDs(options []Option) []string {
	ids := make([]string, 0, len(options))
	for _, option := range options {
		ids = append(ids, option.ID)
	}

	return ids
}

func resetLocalDiscoveryCache(t *testing.T) {
	t.Helper()

	localDiscoveryCache.Lock()
	defer localDiscoveryCache.Unlock()

	localDiscoveryCache.values = make(map[string]localDiscoveryCacheEntry)
}

func TestListProviders_ReturnsGenerationProviderCountsAndOrdersByDisplayIndex(t *testing.T) {
	options := ListProviders(ProviderQuery{
		Current: constants.ModelProviderOpenRouter,
		Auth: map[string]string{
			constants.ModelProviderOpenRouter: "api-key",
		},
	})

	require.NotEmpty(t, options)
	require.Equal(t, constants.ModelProviderOpenAI, options[0].ID)
	require.Equal(t, 0, options[0].DisplayIndex)
	require.True(t, options[0].HasDisplayIndex)

	openrouter := findProviderOption(t, options, constants.ModelProviderOpenRouter)
	require.Equal(t, "OpenRouter", openrouter.Name)
	require.Equal(t, "api-key", openrouter.Type)
	require.Equal(t, "api-key", openrouter.AuthType)
	require.True(t, openrouter.Current)
	require.True(t, openrouter.SupportsAPIKey)
	require.False(t, openrouter.SupportsOAuth)
	require.Greater(t, openrouter.ModelCount, 0)
}

func TestListProviders_ReturnsEmptyForNilRegistry(t *testing.T) {
	options := ListProviders(ProviderQuery{
		Registry: modelprovider.NewRegistry(nil, nil, nil),
	})

	require.Empty(t, options)
}

func TestListProviders_FiltersByAuthMethod(t *testing.T) {
	oauthOptions := ListProviders(ProviderQuery{OAuthOnly: true})
	require.NotEmpty(t, oauthOptions)
	require.Equal(t, constants.ModelProviderOpenAICodex, oauthOptions[0].ID)
	require.Equal(t, constants.ModelProviderAnthropic, oauthOptions[1].ID)
	require.Equal(t, constants.ModelProviderGitHubCopilot, oauthOptions[2].ID)
	oauthIDs := map[string]bool{}
	for _, option := range oauthOptions {
		require.True(t, option.SupportsOAuth, option.ID)
		oauthIDs[option.ID] = true
	}
	require.True(t, oauthIDs[constants.ModelProviderAnthropic])
	require.True(t, oauthIDs[constants.ModelProviderOpenAICodex])
	require.True(t, oauthIDs[constants.ModelProviderGitHubCopilot])
	require.False(t, oauthIDs[constants.ModelProviderOpenRouter])

	apiKeyOptions := ListProviders(ProviderQuery{APIKeyOnly: true})
	require.NotEmpty(t, apiKeyOptions)
	require.Equal(t, constants.ModelProviderOpenAI, apiKeyOptions[0].ID)
	require.Equal(t, constants.ModelProviderAnthropic, apiKeyOptions[1].ID)
	apiKeyIDs := map[string]bool{}
	for _, option := range apiKeyOptions {
		require.True(t, option.SupportsAPIKey, option.ID)
		apiKeyIDs[option.ID] = true
	}
	require.True(t, apiKeyIDs[constants.ModelProviderAnthropic])
	require.True(t, apiKeyIDs[constants.ModelProviderOpenAI])
	require.True(t, apiKeyIDs[constants.ModelProviderOpenRouter])
	require.False(t, apiKeyIDs[constants.ModelProviderOpenAICodex])
	require.True(t, apiKeyIDs[constants.ModelProviderGitHubCopilot])
}

func TestListProviders_OrdersCurrentProviderBeforeNameWithoutDisplayIndex(t *testing.T) {
	registry := modelprovider.NewRegistry(
		[]modelprovider.APIDefinition{{ID: modelprovider.APIOpenAIResponses}},
		[]modelprovider.ProviderDefinition{
			{ID: "alpha", DisplayName: "Alpha", SupportsModels: true},
			{ID: "zulu", DisplayName: "Zulu", SupportsModels: true},
		},
		[]modelprovider.ModelDefinition{
			{ID: "alpha-model", Provider: "alpha", API: modelprovider.APIOpenAIResponses},
			{ID: "zulu-model", Provider: "zulu", API: modelprovider.APIOpenAIResponses},
		},
	)

	options := ListProviders(ProviderQuery{Current: "zulu", Registry: registry})

	require.Len(t, options, 2)
	require.Equal(t, "zulu", options[0].ID)
	require.True(t, options[0].Current)
	require.Equal(t, "alpha", options[1].ID)
}

func findProviderOption(t *testing.T, options []ProviderOption, id string) ProviderOption {
	t.Helper()

	for _, option := range options {
		if option.ID == id {
			return option
		}
	}

	t.Fatalf("provider option %q not found", id)
	return ProviderOption{}
}

func TestListProviders_FormatsProviderTypes(t *testing.T) {
	registry := modelprovider.NewRegistry(nil, []modelprovider.ProviderDefinition{
		{ID: "oauth", DisplayName: "OAuth", SupportsModels: true, SupportsOAuth: true},
		{ID: "both", DisplayName: "Both", SupportsModels: true, SupportsAPIKey: true, SupportsOAuth: true},
		{ID: "none", DisplayName: "None", SupportsModels: true},
		{ID: "tools-only", DisplayName: "Tools Only", SupportsModels: false, SupportsAPIKey: true},
		{ID: "embedding-only", DisplayName: "Embedding Only", SupportsModels: true, SupportsAPIKey: true},
	}, []modelprovider.ModelDefinition{
		{ID: "m1", Provider: "oauth", API: modelprovider.APIOpenAIResponses, Input: []modelprovider.InputKind{modelprovider.InputText}},
		{ID: "m2", Provider: "both", API: modelprovider.APIOpenAIResponses, Input: []modelprovider.InputKind{modelprovider.InputText}},
		{ID: "m3", Provider: "none", API: modelprovider.APIOpenAIResponses, Input: []modelprovider.InputKind{modelprovider.InputText}},
		{ID: "m4", Provider: "embedding-only", API: modelprovider.APIOpenAIEmbeddings, Input: []modelprovider.InputKind{modelprovider.InputText}},
	})

	options := ListProviders(ProviderQuery{Registry: registry})

	require.Len(t, options, 3)
	types := map[string]string{}
	for _, option := range options {
		types[option.ID] = option.Type
	}
	require.Equal(t, "oauth", types["oauth"])
	require.Equal(t, "api-key/oauth", types["both"])
	require.Equal(t, "none", types["none"])
	require.Empty(t, types["tools-only"])
	require.Empty(t, types["embedding-only"])
}
