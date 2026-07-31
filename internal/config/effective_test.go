package config

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/wandxy/morph/internal/constants"
	appcredential "github.com/wandxy/morph/internal/credential"
	modelprovider "github.com/wandxy/morph/internal/model/provider"
)

func TestConfig_SafetyDefaultsAndValidation(t *testing.T) {
	cfg := &Config{}
	cfg.Normalize()
	require.True(t, cfg.InputSafetyEnabled())
	require.True(t, cfg.OutputSafetyEnabled())
	require.True(t, cfg.OutputPIIRedactionEnabled())
	require.False(t, cfg.RetainUnsafeEnabled())

	cfg.Safety.Input = new(false)
	cfg.Safety.Output = new(false)
	cfg.Safety.PII = new(false)
	require.False(t, cfg.InputSafetyEnabled())
	require.False(t, cfg.OutputSafetyEnabled())
	require.False(t, cfg.OutputPIIRedactionEnabled())

	cfg.Safety.PII = new(true)
	require.True(t, cfg.OutputPIIRedactionEnabled())
	cfg.Dev.RetainUnsafe = true
	require.True(t, cfg.RetainUnsafeEnabled())
}

func TestConfig_StreamEnabledDefaultsToTrue(t *testing.T) {
	require.True(t, (&Config{}).StreamEnabled())
	require.False(t, (&Config{Models: ModelsConfig{Main: MainModelConfig{Stream: new(false)}}}).StreamEnabled())
}

func TestConfig_ResolveModelAuthUsesOpenRouterSpecificKey(t *testing.T) {
	cfg := &Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openrouter": {APIKey: "openrouter-key"}},
			Main:      MainModelConfig{Name: constants.DefaultModel, Provider: "openrouter"},
		},
	}

	auth, err := cfg.ResolveModelAuth()

	require.NoError(t, err)
	require.Equal(t, "openrouter", auth.Provider)
	require.Equal(t, "openrouter-key", auth.APIKey)
	require.Equal(t, getDefaultBaseURLForProvider("openrouter", modelprovider.APIOpenAIResponses), auth.BaseURL)
}

func TestConfig_ResolveModelAuthUsesOpenAISpecificKey(t *testing.T) {
	stubModelProviderToken(t, func(string) (StoredModelCredential, error) {
		return StoredModelCredential{}, nil
	})

	cfg := &Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openai": {APIKey: "openai-key"}},
			Main:      MainModelConfig{Name: constants.DefaultModel, Provider: "openai"},
		},
	}

	auth, err := cfg.ResolveModelAuth()

	require.NoError(t, err)
	require.Equal(t, "openai", auth.Provider)
	require.Equal(t, "openai-key", auth.APIKey)
	require.Equal(t, "https://api.openai.com/v1", auth.BaseURL)
}

func TestConfig_ResolveModelAuthUsesProviderConfigDefaults(t *testing.T) {
	stubModelProviderToken(t, func(string) (StoredModelCredential, error) {
		return StoredModelCredential{}, nil
	})

	cfg := &Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{
				"openai": {
					APIKey:  "openai-key",
					API:     modelprovider.APIOpenAICompletions,
					BaseURL: " http://127.0.0.1:8080/v1 ",
					Headers: map[string]string{
						" X-Provider ": " configured ",
					},
				},
			},
			Main: MainModelConfig{Name: constants.DefaultModel, Provider: "openai"},
		},
	}

	auth, err := cfg.ResolveModelAuth()

	require.NoError(t, err)
	require.Equal(t, modelprovider.APIOpenAICompletions, auth.API)
	require.Equal(t, "http://127.0.0.1:8080/v1", auth.BaseURL)
	require.Equal(t, map[string]string{"X-Provider": "configured"}, auth.Headers)
}

func TestConfig_GetProviderConfigHandlesNilAndMissingProviders(t *testing.T) {
	require.Equal(t, ProviderModelConfig{}, (*Config)(nil).getProviderConfig("openai"))
	require.Equal(t, ProviderModelConfig{}, (&Config{}).getProviderConfig("openai"))
}

func TestConfig_SummaryModelAPIEffectiveUsesSummaryProviderDefaults(t *testing.T) {
	cfg := &Config{
		Models: ModelsConfig{
			Main: MainModelConfig{
				Name:     constants.DefaultModel,
				Provider: constants.ModelProviderOpenAI,
			},
			Summary: SummaryModelConfig{
				Provider: constants.ModelProviderAnthropic,
			},
		},
	}

	require.Equal(t, modelprovider.APIAnthropicMessages, cfg.SummaryModelAPIEffective())

	cfg.Models.Summary.Provider = constants.ModelProviderOpenRouter
	cfg.Models.Providers = map[string]ProviderModelConfig{
		constants.ModelProviderOpenRouter: {API: modelprovider.APIOpenAICompletions},
	}
	require.Equal(t, modelprovider.APIOpenAICompletions, cfg.SummaryModelAPIEffective())
}

func TestConfig_SummaryModelAuthUsesSummaryProviderBaseURLConfig(t *testing.T) {
	stubModelProviderToken(t, func(string) (StoredModelCredential, error) {
		return StoredModelCredential{}, nil
	})

	cfg := &Config{
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{
				constants.ModelProviderOpenAI:     {APIKey: "openai-key"},
				constants.ModelProviderOpenRouter: {APIKey: "router-key", BaseURL: "https://router.example/v1"},
			},
			Main: MainModelConfig{
				Name:     constants.DefaultModel,
				Provider: constants.ModelProviderOpenAI,
			},
			Summary: SummaryModelConfig{
				Provider: constants.ModelProviderOpenRouter,
			},
		},
	}

	auth, err := cfg.ResolveSummaryModelAuth()

	require.NoError(t, err)
	require.Equal(t, constants.ModelProviderOpenRouter, auth.Provider)
	require.Equal(t, "https://router.example/v1", auth.BaseURL)
}

func TestConfig_RerankerBaseURLUsesProviderConfigForDifferentModelAPI(t *testing.T) {
	stubModelRegistry(t, modelprovider.NewRegistry(
		[]modelprovider.APIDefinition{
			{ID: modelprovider.APIOpenAICompletions},
			{ID: modelprovider.APIOpenAIResponses},
		},
		[]modelprovider.ProviderDefinition{{
			ID:             "multi",
			DefaultAPI:     modelprovider.APIOpenAIResponses,
			SupportsModels: true,
			BaseURLs: map[string]string{
				modelprovider.APIOpenAICompletions: "https://multi.example/completions",
				modelprovider.APIOpenAIResponses:   "https://multi.example/responses",
			},
		}},
		[]modelprovider.ModelDefinition{{
			ID:       "completion-model",
			Provider: "multi",
			API:      modelprovider.APIOpenAICompletions,
		}},
	))

	cfg := &Config{
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{
				"multi": {BaseURL: "https://configured.example/v1"},
			},
			Main:    MainModelConfig{Provider: "multi", API: modelprovider.APIOpenAIResponses},
			Summary: SummaryModelConfig{Provider: "multi", API: modelprovider.APIOpenAIResponses},
		},
		Reranker: RerankerConfig{Model: "completion-model"},
	}

	require.Equal(t, "https://configured.example/v1", cfg.rerankerModelBaseURLEffective())
}

func TestConfig_EmbeddingModelAPIEffectiveUsesProviderEmbeddingAPI(t *testing.T) {
	cfg := &Config{
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{
				constants.ModelProviderOpenAI: {API: modelprovider.APIOpenAIEmbeddings},
			},
			Main: MainModelConfig{
				Provider: constants.ModelProviderOpenRouter,
			},
			Embedding: EmbeddingModelConfig{
				Provider: constants.ModelProviderOpenAI,
			},
		},
	}

	require.Equal(t, modelprovider.APIOpenAIEmbeddings, cfg.EmbeddingModelAPIEffective())

	cfg = &Config{
		Models: ModelsConfig{
			Main: MainModelConfig{
				Provider: constants.ModelProviderOllama,
			},
			Embedding: EmbeddingModelConfig{
				Provider: constants.ModelProviderOllama,
			},
		},
	}

	require.Equal(t, modelprovider.APIOllamaEmbeddings, cfg.EmbeddingModelAPIEffective())
}

func TestConfig_EmbeddingModelAPIEffectiveReturnsEmptyWithoutEmbeddingAPI(t *testing.T) {
	stubModelRegistry(t, modelprovider.NewRegistry(
		[]modelprovider.APIDefinition{{ID: modelprovider.APIOpenAIResponses}},
		[]modelprovider.ProviderDefinition{{
			ID:             "chat-only",
			DefaultAPI:     modelprovider.APIOpenAIResponses,
			SupportsModels: true,
			BaseURLs: map[string]string{
				modelprovider.APIOpenAIResponses: "https://chat.example/v1",
			},
		}},
		nil,
	))

	cfg := &Config{
		Models: ModelsConfig{
			Main:      MainModelConfig{Provider: "chat-only"},
			Embedding: EmbeddingModelConfig{Provider: "chat-only"},
		},
	}

	require.Empty(t, cfg.EmbeddingModelAPIEffective())
}

func TestConfig_ResolveModelAuthUsesLocalProviderMarker(t *testing.T) {
	stubModelProviderToken(t, func(string) (StoredModelCredential, error) {
		return StoredModelCredential{}, nil
	})
	stubModelRegistry(t, modelprovider.NewRegistry(
		[]modelprovider.APIDefinition{
			{ID: modelprovider.APIOllamaNative},
			{ID: modelprovider.APIOpenAICompletions},
			{ID: modelprovider.APIOpenAIResponses},
		},
		[]modelprovider.ProviderDefinition{{
			ID:             constants.ModelProviderOllama,
			DefaultAPI:     modelprovider.APIOllamaNative,
			SupportsModels: true,
			BaseURLs: map[string]string{
				modelprovider.APIOllamaNative: constants.DefaultOllamaBaseURL,
			},
			Local: &modelprovider.LocalProviderDefinition{
				NativeChatAPI: modelprovider.APIOllamaNative,
				OpenAICompatibleChatAPIs: []string{
					modelprovider.APIOpenAICompletions,
					modelprovider.APIOpenAIResponses,
				},
				AuthMarker: constants.OllamaLocalAuthMarker,
				Capabilities: modelprovider.CapabilitySet{
					Tools:  true,
					Vision: true,
				},
			},
		}},
		nil,
	))

	cfg := NewDefaultConfig()
	cfg.Search.Vector.Enabled = false
	cfg.Models.Main.Name = "llama3.1:8b"
	cfg.Models.Main.Provider = constants.ModelProviderOllama

	require.NoError(t, cfg.Validate())
	auth, err := cfg.ResolveModelAuth()
	require.NoError(t, err)
	require.Equal(t, ModelAuth{
		Provider: constants.ModelProviderOllama,
		API:      modelprovider.APIOllamaNative,
		APIKey:   constants.OllamaLocalAuthMarker,
		BaseURL:  constants.DefaultOllamaBaseURL,
		CredentialSource: ModelCredentialSource{
			Kind: ModelCredentialSourceLocalProvider,
			Name: constants.ModelProviderOllama,
		},
	}, auth)
	require.Equal(t, string(ModelCredentialSourceLocalProvider), auth.AuthType())
}

func TestConfig_ResolveModelAuthUsesFallbackLocalProviderMarker(t *testing.T) {
	stubModelProviderToken(t, func(string) (StoredModelCredential, error) {
		return StoredModelCredential{}, nil
	})
	stubModelRegistry(t, modelprovider.NewRegistry(
		[]modelprovider.APIDefinition{{ID: modelprovider.APIOllamaNative}},
		[]modelprovider.ProviderDefinition{{
			ID:             constants.ModelProviderOllama,
			DefaultAPI:     modelprovider.APIOllamaNative,
			SupportsModels: true,
			BaseURLs: map[string]string{
				modelprovider.APIOllamaNative: constants.DefaultOllamaBaseURL,
			},
			Local: &modelprovider.LocalProviderDefinition{NativeChatAPI: modelprovider.APIOllamaNative},
		}},
		nil,
	))

	cfg := NewDefaultConfig()
	cfg.Search.Vector.Enabled = false
	cfg.Models.Main.Name = "llama3.1:8b"
	cfg.Models.Main.Provider = constants.ModelProviderOllama

	auth, err := cfg.ResolveModelAuth()

	require.NoError(t, err)
	require.Equal(t, constants.LocalProviderAuthMarker, auth.APIKey)
	require.Equal(t, ModelCredentialSourceLocalProvider, auth.CredentialSource.Kind)
}

func TestConfig_ResolveModelAuthAcceptsOpenAIProviderAlias(t *testing.T) {
	stubModelProviderToken(t, func(string) (StoredModelCredential, error) {
		return StoredModelCredential{}, nil
	})

	cfg := &Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openai": {APIKey: "openai-key"}},
			Main:      MainModelConfig{Name: constants.DefaultModel, Provider: "openai"},
		},
	}

	auth, err := cfg.ResolveModelAuth()

	require.NoError(t, err)
	require.Equal(t, "openai", auth.Provider)
	require.Equal(t, "openai-key", auth.APIKey)
	require.Equal(t, "https://api.openai.com/v1", auth.BaseURL)
}

func TestConfig_ResolveModelAuthUsesCredentialResolverOrder(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "registry-env-key")
	t.Setenv("CUSTOM_OPENROUTER_KEY", "provider-env-key")
	expiresAt := time.Now().Add(time.Hour)
	storedToken := "store-key"
	stubModelProviderToken(t, func(provider string) (StoredModelCredential, error) {
		require.Equal(t, "openrouter", provider)
		if storedToken == "" {
			return StoredModelCredential{}, nil
		}
		return StoredModelCredential{Type: "oauth", Token: storedToken, ExpiresAt: &expiresAt}, nil
	})

	cfg := &Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{
				"openrouter": {
					APIKey:    "provider-config-key",
					APIKeyEnv: []string{"CUSTOM_OPENROUTER_KEY"},
				},
			},
			Main: MainModelConfig{
				Name:     constants.DefaultModel,
				Provider: "openrouter",
				APIKey:   "role-key",
			},
		},
	}

	auth, err := cfg.ResolveModelAuth()
	require.NoError(t, err)
	require.Equal(t, "role-key", auth.APIKey)
	require.Equal(t, ModelCredentialSource{Kind: ModelCredentialSourceRoleConfig}, auth.CredentialSource)

	cfg.Models.Main.APIKey = ""
	auth, err = cfg.ResolveModelAuth()
	require.NoError(t, err)
	require.Equal(t, "provider-env-key", auth.APIKey)
	require.Equal(t, ModelCredentialSource{
		Kind: ModelCredentialSourceProviderEnv,
		Name: "CUSTOM_OPENROUTER_KEY",
	}, auth.CredentialSource)

	storedToken = ""
	auth, err = cfg.ResolveModelAuth()
	require.NoError(t, err)
	require.Equal(t, "provider-env-key", auth.APIKey)
	require.Equal(t, ModelCredentialSource{
		Kind: ModelCredentialSourceProviderEnv,
		Name: "CUSTOM_OPENROUTER_KEY",
	}, auth.CredentialSource)

	cfg.Models.Providers["openrouter"] = ProviderModelConfig{APIKeyEnv: []string{"CUSTOM_OPENROUTER_KEY"}}
	auth, err = cfg.ResolveModelAuth()
	require.NoError(t, err)
	require.Equal(t, "provider-env-key", auth.APIKey)
	require.Equal(t, ModelCredentialSource{
		Kind: ModelCredentialSourceProviderEnv,
		Name: "CUSTOM_OPENROUTER_KEY",
	}, auth.CredentialSource)

	cfg.Models.Providers["openrouter"] = ProviderModelConfig{}
	auth, err = cfg.ResolveModelAuth()
	require.NoError(t, err)
	require.Equal(t, "registry-env-key", auth.APIKey)
	require.Equal(t, ModelCredentialSource{
		Kind: ModelCredentialSourceProviderEnv,
		Name: "OPENROUTER_API_KEY",
	}, auth.CredentialSource)

	t.Setenv("OPENROUTER_API_KEY", "")
	cfg.Models.Providers["openrouter"] = ProviderModelConfig{APIKey: "provider-config-key"}
	auth, err = cfg.ResolveModelAuth()
	require.NoError(t, err)
	require.Equal(t, "provider-config-key", auth.APIKey)
	require.Equal(t, ModelCredentialSource{
		Kind: ModelCredentialSourceProviderConfig,
		Name: "openrouter",
	}, auth.CredentialSource)
}

func TestConfig_ResolveSummaryModelAuthUsesMainRoleKeyWhenRouteMatches(t *testing.T) {
	cfg := &Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Main: MainModelConfig{
				Name:     constants.DefaultModel,
				Provider: "openrouter",
				APIKey:   "main-role-key",
			},
		},
	}

	main, err := cfg.ResolveModelAuth()
	require.NoError(t, err)
	summary, err := cfg.ResolveSummaryModelAuth()
	require.NoError(t, err)

	require.True(t, ModelAuthEqual(main, summary))
	require.Equal(t, "main-role-key", summary.APIKey)
	require.Equal(t, ModelCredentialSource{Kind: ModelCredentialSourceRoleConfig}, summary.CredentialSource)
}

func TestConfig_ResolveSummaryModelAuthUsesSummaryRoleKeyWhenSet(t *testing.T) {
	cfg := &Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Main:    MainModelConfig{Name: constants.DefaultModel, Provider: "openrouter", APIKey: "main-role-key"},
			Summary: SummaryModelConfig{APIKey: "summary-role-key"},
		},
	}

	auth, err := cfg.ResolveSummaryModelAuth()
	require.NoError(t, err)
	require.Equal(t, "summary-role-key", auth.APIKey)
	require.Equal(t, ModelCredentialSource{Kind: ModelCredentialSourceRoleConfig}, auth.CredentialSource)
}

func TestConfig_ResolveModelAuthUsesProviderTokenStore(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	stubModelProviderToken(t, func(provider string) (StoredModelCredential, error) {
		require.Equal(t, constants.ModelProviderOpenAICodex, provider)
		return StoredModelCredential{Type: appcredential.TypeOAuth, Token: "stored-token"}, nil
	})

	cfg := &Config{
		Name:   "test-agent",
		Models: ModelsConfig{Main: MainModelConfig{Name: "gpt-5.4", Provider: constants.ModelProviderOpenAICodex}},
	}

	auth, err := cfg.ResolveModelAuth()
	require.NoError(t, err)
	require.Equal(t, "stored-token", auth.APIKey)
	require.Equal(t, ModelCredentialSource{
		Kind: ModelCredentialSourceTokenStore,
		Name: constants.ModelProviderOpenAICodex,
		Type: appcredential.TypeOAuth,
	}, auth.CredentialSource)
	require.False(t, auth.SupportsMaxOutputTokens())
	require.False(t, cfg.SummaryModelSupportsMaxOutputTokens())
}

func TestConfig_ResolveModelAuthUsesOpenAISubscriptionHeaders(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	stubModelProviderToken(t, func(provider string) (StoredModelCredential, error) {
		require.Equal(t, constants.ModelProviderOpenAICodex, provider)
		return StoredModelCredential{Type: appcredential.TypeOAuth, Token: "stored-token"}, nil
	})
	stubSubscriptionProvider(t, func(provider string) (appcredential.SubscriptionProvider, bool) {
		require.Equal(t, constants.ModelProviderOpenAICodex, provider)
		return modelAuthSubscriptionProvider{
			headers: map[string]string{
				"Authorization":      "Bearer stored-token",
				"ChatGPT-Account-ID": "acct-test",
			},
		}, true
	})

	cfg := &Config{
		Name:   "test-agent",
		Models: ModelsConfig{Main: MainModelConfig{Name: "gpt-5.4", Provider: constants.ModelProviderOpenAICodex}},
	}

	auth, err := cfg.ResolveModelAuth()
	require.NoError(t, err)
	require.Equal(t, constants.DefaultOpenAISubscriptionBaseURL, auth.BaseURL)
	require.Equal(t, map[string]string{
		"Authorization":      "Bearer stored-token",
		"ChatGPT-Account-ID": "acct-test",
	}, auth.Headers)
	require.Equal(t, ModelCredentialSource{
		Kind: ModelCredentialSourceTokenStore,
		Name: constants.ModelProviderOpenAICodex,
		Type: appcredential.TypeOAuth,
	}, auth.CredentialSource)
}

func TestConfig_ResolveModelAuthUsesAnthropicSubscriptionHeaders(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	stubModelProviderToken(t, func(provider string) (StoredModelCredential, error) {
		require.Equal(t, "anthropic", provider)
		return StoredModelCredential{Type: appcredential.TypeOAuth, Token: "stored-token"}, nil
	})
	stubSubscriptionProvider(t, func(provider string) (appcredential.SubscriptionProvider, bool) {
		require.Equal(t, "anthropic", provider)
		return modelAuthSubscriptionProvider{
			headers: map[string]string{
				"Authorization":  "Bearer stored-token",
				"anthropic-beta": "claude-code-20250219,oauth-2025-04-20",
			},
		}, true
	})

	cfg := &Config{
		Name:   "test-agent",
		Models: ModelsConfig{Main: MainModelConfig{Name: "claude-sonnet-4-6", Provider: "anthropic"}},
	}

	auth, err := cfg.ResolveModelAuth()
	require.NoError(t, err)
	require.Equal(t, "stored-token", auth.APIKey)
	require.Equal(t, constants.DefaultAnthropicBaseURL, auth.BaseURL)
	require.Equal(t, map[string]string{
		"Authorization":  "Bearer stored-token",
		"anthropic-beta": "claude-code-20250219,oauth-2025-04-20",
	}, auth.Headers)
	require.Equal(t, ModelCredentialSource{
		Kind: ModelCredentialSourceTokenStore,
		Name: "anthropic",
		Type: appcredential.TypeOAuth,
	}, auth.CredentialSource)
	require.True(t, auth.SupportsMaxOutputTokens())
}

func TestConfig_ResolveModelAuthUsesAnthropicOAuthEnvBeforeAPIKeyEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_OAUTH_TOKEN", "oauth-env-token")
	t.Setenv("ANTHROPIC_API_KEY", "api-env-key")
	stubModelProviderToken(t, func(provider string) (StoredModelCredential, error) {
		require.Equal(t, "anthropic", provider)
		return StoredModelCredential{}, nil
	})
	stubSubscriptionProvider(t, func(provider string) (appcredential.SubscriptionProvider, bool) {
		require.Equal(t, "anthropic", provider)
		return modelAuthSubscriptionProvider{
			headers: map[string]string{
				"Authorization":  "Bearer oauth-env-token",
				"anthropic-beta": "claude-code-20250219,oauth-2025-04-20",
			},
		}, true
	})

	cfg := &Config{
		Name:   "test-agent",
		Models: ModelsConfig{Main: MainModelConfig{Name: "claude-sonnet-4-6", Provider: "anthropic"}},
	}

	auth, err := cfg.ResolveModelAuth()
	require.NoError(t, err)
	require.Equal(t, "oauth-env-token", auth.APIKey)
	require.Equal(t, "Bearer oauth-env-token", auth.Headers["Authorization"])
	require.Equal(t, ModelCredentialSource{
		Kind: ModelCredentialSourceProviderEnv,
		Name: "ANTHROPIC_OAUTH_TOKEN",
		Type: appcredential.TypeOAuth,
	}, auth.CredentialSource)
}

func TestConfig_ResolveModelAuthReturnsOAuthEnvHeaderError(t *testing.T) {
	t.Setenv("ANTHROPIC_OAUTH_TOKEN", "oauth-env-token")
	stubModelProviderToken(t, func(provider string) (StoredModelCredential, error) {
		require.Equal(t, "anthropic", provider)
		return StoredModelCredential{}, nil
	})
	stubSubscriptionProvider(t, func(provider string) (appcredential.SubscriptionProvider, bool) {
		require.Equal(t, "anthropic", provider)
		return modelAuthSubscriptionProvider{err: errors.New("headers failed")}, true
	})

	cfg := &Config{
		Name:   "test-agent",
		Models: ModelsConfig{Main: MainModelConfig{Name: "claude-sonnet-4-6", Provider: "anthropic"}},
	}

	_, err := cfg.ResolveModelAuth()
	require.EqualError(t, err, "headers failed")
}

func TestConfig_ResolveModelAuthUsesCopilotTokenEnvAsOAuth(t *testing.T) {
	t.Setenv("COPILOT_GITHUB_TOKEN", "copilot-token")
	stubModelProviderToken(t, func(provider string) (StoredModelCredential, error) {
		require.Equal(t, "github-copilot", provider)
		return StoredModelCredential{}, nil
	})
	stubSubscriptionProvider(t, func(provider string) (appcredential.SubscriptionProvider, bool) {
		require.Equal(t, "github-copilot", provider)
		return modelAuthSubscriptionProvider{
			headers: map[string]string{
				"Authorization": "Bearer copilot-token",
				"X-Initiator":   "user",
			},
		}, true
	})

	cfg := &Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Main: MainModelConfig{Name: "gpt-5.4-mini", Provider: "github-copilot"},
		},
	}

	auth, err := cfg.ResolveModelAuth()
	require.NoError(t, err)
	require.Equal(t, "copilot-token", auth.APIKey)
	require.Equal(t, constants.DefaultGitHubCopilotBaseURL, auth.BaseURL)
	require.Equal(t, "Bearer copilot-token", auth.Headers["Authorization"])
	require.Equal(t, "user", auth.Headers["X-Initiator"])
	require.Equal(t, ModelCredentialSource{
		Kind: ModelCredentialSourceProviderEnv,
		Name: "COPILOT_GITHUB_TOKEN",
		Type: appcredential.TypeOAuth,
	}, auth.CredentialSource)
}

func TestConfig_ResolveModelAuthFallsBackFromUnsupportedAnthropicOAuthEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_OAUTH_TOKEN", "oauth-env-token")
	t.Setenv("ANTHROPIC_API_KEY", "api-env-key")
	stubModelProviderToken(t, func(provider string) (StoredModelCredential, error) {
		require.Equal(t, "anthropic", provider)
		return StoredModelCredential{}, nil
	})

	registry := modelprovider.NewRegistry(
		[]modelprovider.APIDefinition{{ID: modelprovider.APIAnthropicMessages}},
		[]modelprovider.ProviderDefinition{{
			ID:            constants.ModelProviderAnthropic,
			DefaultAPI:    modelprovider.APIAnthropicMessages,
			APIKeyEnv:     []string{"ANTHROPIC_API_KEY"},
			SupportsOAuth: true,
			BaseURLs: map[string]string{
				modelprovider.APIAnthropicMessages: constants.DefaultAnthropicBaseURL,
			},
		}},
		[]modelprovider.ModelDefinition{{
			ID:       "claude-api-only",
			Provider: constants.ModelProviderAnthropic,
			Owner:    constants.ModelProviderAnthropic,
			API:      modelprovider.APIAnthropicMessages,
		}},
	)
	stubModelRegistry(t, registry)

	cfg := &Config{
		Name:   "test-agent",
		Models: ModelsConfig{Main: MainModelConfig{Name: "claude-api-only", Provider: "anthropic"}},
	}

	auth, err := cfg.ResolveModelAuth()
	require.NoError(t, err)
	require.Equal(t, "api-env-key", auth.APIKey)
	require.Nil(t, auth.Headers)
	require.Equal(t, ModelCredentialSource{
		Kind: ModelCredentialSourceProviderEnv,
		Name: "ANTHROPIC_API_KEY",
	}, auth.CredentialSource)
}

func TestConfig_ResolveModelAuthReturnsUnsupportedAnthropicOAuthEnvError(t *testing.T) {
	t.Setenv("ANTHROPIC_OAUTH_TOKEN", "oauth-env-token")
	stubModelProviderToken(t, func(provider string) (StoredModelCredential, error) {
		require.Equal(t, "anthropic", provider)
		return StoredModelCredential{}, nil
	})

	registry := modelprovider.NewRegistry(
		[]modelprovider.APIDefinition{{ID: modelprovider.APIAnthropicMessages}},
		[]modelprovider.ProviderDefinition{{
			ID:            constants.ModelProviderAnthropic,
			DefaultAPI:    modelprovider.APIAnthropicMessages,
			SupportsOAuth: true,
			BaseURLs: map[string]string{
				modelprovider.APIAnthropicMessages: constants.DefaultAnthropicBaseURL,
			},
		}},
		[]modelprovider.ModelDefinition{{
			ID:       "claude-api-only",
			Provider: constants.ModelProviderAnthropic,
			Owner:    constants.ModelProviderAnthropic,
			API:      modelprovider.APIAnthropicMessages,
		}},
	)
	stubModelRegistry(t, registry)

	cfg := &Config{
		Name:   "test-agent",
		Models: ModelsConfig{Main: MainModelConfig{Name: "claude-api-only", Provider: "anthropic"}},
	}

	_, err := cfg.ResolveModelAuth()
	require.EqualError(t, err, `model "claude-api-only" is not available through OAuth for provider "anthropic"`)
}

func TestConfig_ResolveModelAuthUsesStoredAPIKeyCredential(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	stubModelProviderToken(t, func(provider string) (StoredModelCredential, error) {
		require.Equal(t, "openai", provider)
		return StoredModelCredential{Type: "api_key", Key: "stored-api-key"}, nil
	})

	cfg := &Config{
		Name:   "test-agent",
		Models: ModelsConfig{Main: MainModelConfig{Name: "gpt-5.4-mini", Provider: "openai"}},
	}

	auth, err := cfg.ResolveModelAuth()
	require.NoError(t, err)
	require.Equal(t, "stored-api-key", auth.APIKey)
	require.Equal(t, ModelCredentialSource{
		Kind: ModelCredentialSourceTokenStore,
		Name: "openai",
		Type: appcredential.TypeAPIKey,
	}, auth.CredentialSource)
}

func TestConfig_ResolveModelAuthDoesNotLoadOpenAICredentialForOpenAICodex(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	stubModelProviderToken(t, func(provider string) (StoredModelCredential, error) {
		require.Equal(t, constants.ModelProviderOpenAICodex, provider)
		return StoredModelCredential{}, nil
	})

	cfg := &Config{
		Name:   "test-agent",
		Models: ModelsConfig{Main: MainModelConfig{Name: "gpt-5.4", Provider: constants.ModelProviderOpenAICodex}},
	}

	_, err := cfg.ResolveModelAuth()
	require.ErrorContains(t, err, `model API key is required for provider "openai-codex"`)
}

func TestConfig_ResolveModelAuthRefreshesExpiredStoredCredential(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	expired := time.Now().Add(-time.Minute)
	expiresAt := time.Now().Add(time.Hour)
	stubModelProviderToken(t, func(provider string) (StoredModelCredential, error) {
		require.Equal(t, constants.ModelProviderOpenAICodex, provider)
		return StoredModelCredential{Type: appcredential.TypeOAuth, Token: "expired-token", ExpiresAt: &expired}, nil
	})
	stubRefreshModelProviderToken(t, func(_ context.Context, provider string) (StoredModelCredential, bool, error) {
		require.Equal(t, constants.ModelProviderOpenAICodex, provider)
		return StoredModelCredential{Type: "oauth", Token: "fresh-token", ExpiresAt: &expiresAt}, true, nil
	})

	cfg := &Config{
		Name:   "test-agent",
		Models: ModelsConfig{Main: MainModelConfig{Name: "gpt-5.4", Provider: constants.ModelProviderOpenAICodex}},
	}

	auth, err := cfg.ResolveModelAuth()
	require.NoError(t, err)
	require.Equal(t, "fresh-token", auth.APIKey)
	require.Equal(t, ModelCredentialSource{
		Kind:      ModelCredentialSourceTokenStore,
		Name:      constants.ModelProviderOpenAICodex,
		Type:      appcredential.TypeOAuth,
		HasExpiry: true,
	}, auth.CredentialSource)
}

func TestConfig_ResolveModelAuthRejectsOpenAISubscriptionModelWithoutOAuthSupport(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	stubModelProviderToken(t, func(provider string) (StoredModelCredential, error) {
		require.Equal(t, "openai", provider)
		return StoredModelCredential{Type: appcredential.TypeOAuth, Token: "stored-token"}, nil
	})

	cfg := &Config{
		Name:   "test-agent",
		Models: ModelsConfig{Main: MainModelConfig{Name: constants.DefaultModel, Provider: "openai"}},
	}

	_, err := cfg.ResolveModelAuth()
	require.EqualError(t, err, `model "gpt-4o-mini" is not available through OAuth for provider "openai"`)
}

func TestConfig_ResolveModelAuthSkipsExpiredStoredCredentialWithoutRefreshProvider(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "env-key")
	expired := time.Now().Add(-time.Minute)
	stubModelProviderToken(t, func(provider string) (StoredModelCredential, error) {
		require.Equal(t, "openai", provider)
		return StoredModelCredential{Type: "oauth", Token: "expired-token", ExpiresAt: &expired}, nil
	})
	stubRefreshModelProviderToken(t, func(_ context.Context, provider string) (StoredModelCredential, bool, error) {
		require.Equal(t, "openai", provider)
		return StoredModelCredential{}, false, nil
	})

	cfg := &Config{
		Name:   "test-agent",
		Models: ModelsConfig{Main: MainModelConfig{Name: constants.DefaultModel, Provider: "openai"}},
	}

	auth, err := cfg.ResolveModelAuth()
	require.NoError(t, err)
	require.Equal(t, "env-key", auth.APIKey)
	require.Equal(t, ModelCredentialSource{
		Kind: ModelCredentialSourceProviderEnv,
		Name: "OPENAI_API_KEY",
	}, auth.CredentialSource)
}

func TestConfig_ResolveModelAuthReturnsProviderTokenStoreError(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	stubModelProviderToken(t, func(string) (StoredModelCredential, error) {
		return StoredModelCredential{}, errors.New("token store failed")
	})

	cfg := &Config{
		Name:   "test-agent",
		Models: ModelsConfig{Main: MainModelConfig{Name: constants.DefaultModel, Provider: "openai"}},
	}

	_, err := cfg.ResolveModelAuth()
	require.EqualError(t, err, "token store failed")
}

func TestConfig_ResolveEmbeddingModelAuth(t *testing.T) {
	cfg := &Config{
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openrouter": {APIKey: "router-key"}},
			Main:      MainModelConfig{Provider: "openrouter"},
			Embedding: EmbeddingModelConfig{Provider: "openrouter"},
		},
	}

	auth, err := cfg.ResolveEmbeddingModelAuth()

	require.NoError(t, err)
	require.Equal(t, ModelAuth{
		Provider:         "openrouter",
		API:              modelprovider.APIOpenRouterEmbeddings,
		APIKey:           "router-key",
		BaseURL:          "https://openrouter.ai/api/v1/embeddings",
		CredentialSource: ModelCredentialSource{Kind: ModelCredentialSourceProviderConfig, Name: "openrouter"},
	}, auth)

	cfg = &Config{
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openrouter": {APIKey: "router-key"}},
			Main:      MainModelConfig{Provider: "openrouter", API: modelprovider.APIOpenAIResponses},
			Embedding: EmbeddingModelConfig{Provider: "openrouter"},
		},
	}

	auth, err = cfg.ResolveEmbeddingModelAuth()

	require.NoError(t, err)
	require.Equal(t, getDefaultBaseURLForProvider("openrouter", modelprovider.APIOpenRouterEmbeddings), auth.BaseURL)

	cfg = &Config{
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openrouter": {APIKey: "router-key"}},
			Main:      MainModelConfig{Provider: "openrouter"},
		},
	}

	auth, err = cfg.ResolveEmbeddingModelAuth()

	require.NoError(t, err)
	require.Equal(t, ModelAuth{
		Provider:         "openrouter",
		API:              modelprovider.APIOpenRouterEmbeddings,
		APIKey:           "router-key",
		BaseURL:          "https://openrouter.ai/api/v1/embeddings",
		CredentialSource: ModelCredentialSource{Kind: ModelCredentialSourceProviderConfig, Name: "openrouter"},
	}, auth)

	cfg = &Config{
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openai": {APIKey: "openai-key"}},
			Main:      MainModelConfig{Provider: "openrouter"},
			Embedding: EmbeddingModelConfig{Provider: "openai"},
		},
	}

	auth, err = cfg.ResolveEmbeddingModelAuth()

	require.NoError(t, err)
	require.Equal(t, ModelAuth{
		Provider:         "openai",
		API:              modelprovider.APIOpenAIEmbeddings,
		APIKey:           "openai-key",
		BaseURL:          "https://api.openai.com/v1/embeddings",
		CredentialSource: ModelCredentialSource{Kind: ModelCredentialSourceProviderConfig, Name: "openai"},
	}, auth)

	cfg = &Config{
		Models: ModelsConfig{
			Embedding: EmbeddingModelConfig{
				Provider: "openai",
				APIKey:   "embedding-role-key",
			},
		},
	}
	auth, err = cfg.ResolveEmbeddingModelAuth()
	require.NoError(t, err)
	require.Equal(t, "embedding-role-key", auth.APIKey)
	require.Equal(t, ModelCredentialSource{Kind: ModelCredentialSourceRoleConfig}, auth.CredentialSource)

	cfg = &Config{
		Models: ModelsConfig{
			Main: MainModelConfig{
				Name:     constants.DefaultOllamaModel,
				Provider: constants.ModelProviderOllama,
				BaseURL:  "http://127.0.0.1:11435/v1",
			},
			Embedding: EmbeddingModelConfig{
				Name:     constants.DefaultOllamaEmbeddingModel,
				Provider: constants.ModelProviderOllama,
			},
		},
	}
	auth, err = cfg.ResolveEmbeddingModelAuth()
	require.NoError(t, err)
	require.Equal(t, ModelAuth{
		Provider:         constants.ModelProviderOllama,
		API:              modelprovider.APIOllamaEmbeddings,
		APIKey:           constants.OllamaLocalAuthMarker,
		BaseURL:          "http://127.0.0.1:11435",
		CredentialSource: ModelCredentialSource{Kind: ModelCredentialSourceLocalProvider, Name: constants.ModelProviderOllama},
	}, auth)

	t.Setenv("OPENAI_API_KEY", "")
	_, err = (&Config{Models: ModelsConfig{Embedding: EmbeddingModelConfig{Provider: "openai"}}}).ResolveEmbeddingModelAuth()
	require.EqualError(t, err, `embedding API key is required for provider "openai"; set a provider API key, provider env var, or role apiKey`)

	_, err = (&Config{
		Models: ModelsConfig{Providers: map[string]ProviderModelConfig{"openrouter": {APIKey: "key"}}, Embedding: EmbeddingModelConfig{Provider: "test"}},
	}).ResolveEmbeddingModelAuth()
	require.EqualError(t, err, "embedding provider must be one of: anthropic, github-copilot, ollama, openai, openai-codex, openrouter")
}

func TestConfig_ResolveEmbeddingModelAuthUsesRegistryModelAPIAndCustomProvider(t *testing.T) {
	registry := modelprovider.NewRegistry(
		[]modelprovider.APIDefinition{
			{ID: modelprovider.APIOpenAIEmbeddings},
		},
		[]modelprovider.ProviderDefinition{{
			ID:             "custom-embed",
			DefaultAPI:     modelprovider.APIOpenAIEmbeddings,
			SupportsModels: true,
			SupportsAPIKey: true,
			BaseURLs: map[string]string{
				modelprovider.APIOpenAIEmbeddings: "https://embeddings.example/v1/embeddings",
			},
		}},
		[]modelprovider.ModelDefinition{{
			ID:       "custom-embedding-model",
			Provider: "custom-embed",
			API:      modelprovider.APIOpenAIEmbeddings,
			Input:    []modelprovider.InputKind{modelprovider.InputText},
		}},
	)
	stubModelRegistry(t, registry)

	cfg := &Config{
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"custom-embed": {APIKey: "custom-key"}},
			Main:      MainModelConfig{Provider: "custom-embed"},
			Embedding: EmbeddingModelConfig{
				Name:     "custom-embedding-model",
				Provider: "custom-embed",
			},
		},
	}

	auth, err := cfg.ResolveEmbeddingModelAuth()

	require.NoError(t, err)
	require.Equal(t, ModelAuth{
		Provider:         "custom-embed",
		API:              modelprovider.APIOpenAIEmbeddings,
		APIKey:           "custom-key",
		BaseURL:          "https://embeddings.example/v1/embeddings",
		CredentialSource: ModelCredentialSource{Kind: ModelCredentialSourceProviderConfig, Name: "custom-embed"},
	}, auth)
}

func TestConfig_ResolveEmbeddingModelAuthUsesExplicitAPIAndBaseURL(t *testing.T) {
	cfg := &Config{
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openrouter": {APIKey: "router-key"}},
			Embedding: EmbeddingModelConfig{
				Name:     "text-embedding-3-small",
				Provider: "openrouter",
				API:      modelprovider.APIOpenRouterEmbeddings,
				BaseURL:  "https://proxy.example/embeddings",
			},
		},
	}

	auth, err := cfg.ResolveEmbeddingModelAuth()

	require.NoError(t, err)
	require.Equal(t, modelprovider.APIOpenRouterEmbeddings, auth.API)
	require.Equal(t, "https://proxy.example/embeddings", auth.BaseURL)
	require.Equal(t, "router-key", auth.APIKey)
}

func TestConfig_ResolveRerankerModelAuthUsesSummaryRoleByDefault(t *testing.T) {
	cfg := &Config{
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openrouter": {APIKey: "router-key"}},
			Main:      MainModelConfig{Provider: "openrouter"},
			Summary:   SummaryModelConfig{Name: "openai/gpt-4o-mini", Provider: "openrouter"},
		},
	}

	auth, err := cfg.ResolveRerankerModelAuth()

	require.NoError(t, err)
	require.Equal(t, ModelAuth{
		Provider:         "openrouter",
		API:              modelprovider.APIOpenAIResponses,
		APIKey:           "router-key",
		BaseURL:          constants.DefaultOpenRouterResponsesBaseURL,
		CredentialSource: ModelCredentialSource{Kind: ModelCredentialSourceProviderConfig, Name: "openrouter"},
	}, auth)
	require.Equal(t, "openai/gpt-4o-mini", cfg.RerankerModelEffective())
}

func TestConfig_ResolveRerankerModelAuthUsesRegistryModelAPI(t *testing.T) {
	t.Setenv("COPILOT_GITHUB_TOKEN", "")
	stubModelProviderToken(t, func(string) (StoredModelCredential, error) {
		return StoredModelCredential{}, nil
	})

	cfg := &Config{
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"github-copilot": {APIKey: "copilot-key"}},
			Main:      MainModelConfig{Name: "gpt-4.1", Provider: "github-copilot", API: modelprovider.APIOpenAICompletions},
			Summary:   SummaryModelConfig{Name: "gpt-4.1", Provider: "github-copilot", API: modelprovider.APIOpenAICompletions},
		},
		Reranker: RerankerConfig{Model: "claude-sonnet-4.5"},
	}

	auth, err := cfg.ResolveRerankerModelAuth()

	require.NoError(t, err)
	require.Equal(t, constants.ModelProviderGitHubCopilot, auth.Provider)
	require.Equal(t, modelprovider.APIAnthropicMessages, auth.API)
	require.Equal(t, constants.DefaultGitHubCopilotBaseURL, auth.BaseURL)
	require.Equal(t, "copilot-key", auth.APIKey)
	require.Equal(t, modelprovider.APIAnthropicMessages, cfg.RerankerModelAPIEffective())
}

func TestConfig_ResolveRerankerModelAuthReturnsCredentialErrors(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	stubModelProviderToken(t, func(string) (StoredModelCredential, error) {
		return StoredModelCredential{}, errors.New("store failed")
	})

	cfg := &Config{
		Models: ModelsConfig{
			Main:    MainModelConfig{Name: constants.DefaultModel, Provider: constants.ModelProviderOpenAI},
			Summary: SummaryModelConfig{Name: constants.DefaultModel, Provider: constants.ModelProviderOpenAI},
		},
	}

	_, err := cfg.ResolveRerankerModelAuth()
	require.EqualError(t, err, "store failed")

	stubModelProviderToken(t, func(string) (StoredModelCredential, error) {
		return StoredModelCredential{}, nil
	})

	_, err = cfg.ResolveRerankerModelAuth()
	require.EqualError(t, err, `model API key is required for provider "openai"; set a provider API key, provider env var, role apiKey, or provider login`)
}

func TestConfig_ResolveEmbeddingModelAuthSkipsStoredOAuthCredential(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "env-api-key")
	stubModelProviderToken(t, func(provider string) (StoredModelCredential, error) {
		require.Equal(t, "openai", provider)
		return StoredModelCredential{Type: appcredential.TypeOAuth, Token: "subscription-token"}, nil
	})
	stubSubscriptionProvider(t, func(string) (appcredential.SubscriptionProvider, bool) {
		require.FailNow(t, "embedding auth must not request subscription headers")
		return nil, false
	})

	cfg := &Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Main:      MainModelConfig{Name: constants.DefaultModel, Provider: "openai"},
			Embedding: EmbeddingModelConfig{Name: constants.DefaultProfileEmbeddingModel},
		},
	}

	auth, err := cfg.ResolveEmbeddingModelAuth()
	require.NoError(t, err)
	require.Equal(t, "env-api-key", auth.APIKey)
	require.Equal(t, ModelCredentialSource{
		Kind: ModelCredentialSourceProviderEnv,
		Name: "OPENAI_API_KEY",
	}, auth.CredentialSource)
}

func TestConfig_ModelEmbeddingProviderEffective(t *testing.T) {
	var cfg *Config
	require.Empty(t, cfg.ModelEmbeddingProviderEffective())

	cfg = &Config{Models: ModelsConfig{Main: MainModelConfig{Provider: " OpenRouter "}}}
	require.Equal(t, "openrouter", cfg.ModelEmbeddingProviderEffective())

	cfg = &Config{
		Models: ModelsConfig{
			Main:      MainModelConfig{Provider: "openrouter"},
			Embedding: EmbeddingModelConfig{Provider: " OpenAI "},
		},
	}
	require.Equal(t, "openai", cfg.ModelEmbeddingProviderEffective())
}

func TestConfig_GetEmbeddingProviderRoleBaseURL(t *testing.T) {
	var cfg *Config
	require.Empty(t, cfg.getEmbeddingProviderRoleBaseURL(constants.ModelProviderOllama))

	cfg = &Config{
		Models: ModelsConfig{
			Main: MainModelConfig{
				Provider: constants.ModelProviderOpenAI,
				BaseURL:  "https://api.openai.com/v1",
			},
		},
	}
	require.Empty(t, cfg.getEmbeddingProviderRoleBaseURL(constants.ModelProviderOllama))
	require.Empty(t, cfg.getEmbeddingProviderRoleBaseURL(constants.ModelProviderOpenAI))

	cfg.Models.Main.Provider = constants.ModelProviderOllama
	cfg.Models.Main.BaseURL = "http://127.0.0.1:11434/v1"
	require.Equal(t, "http://127.0.0.1:11434", cfg.getEmbeddingProviderRoleBaseURL(constants.ModelProviderOllama))
}

func TestConfig_SummaryModelEffective(t *testing.T) {
	t.Run("inherits_main_model_when_empty", func(t *testing.T) {
		cfg := &Config{Models: ModelsConfig{Main: MainModelConfig{Name: constants.DefaultModel}}}
		require.Equal(t, constants.DefaultModel, cfg.SummaryModelEffective())
	})

	t.Run("uses_summary_when_set", func(t *testing.T) {
		cfg := &Config{
			Models: ModelsConfig{
				Main:    MainModelConfig{Name: constants.DefaultModel},
				Summary: SummaryModelConfig{Name: "claude-3.5-haiku"},
			},
		}
		require.Equal(t, "claude-3.5-haiku", cfg.SummaryModelEffective())
	})
}

func TestConfig_SummaryProviderEffective(t *testing.T) {
	cfg := &Config{Models: ModelsConfig{Main: MainModelConfig{Provider: "openrouter"}}}
	require.Equal(t, "openrouter", cfg.SummaryProviderEffective())

	cfg.Models.Summary.Provider = "openai"
	require.Equal(t, "openai", cfg.SummaryProviderEffective())
}

func TestConfig_SummaryModelAPIEffective(t *testing.T) {
	cfg := &Config{Models: ModelsConfig{Main: MainModelConfig{API: modelprovider.APIOpenAIResponses}}}
	cfg.Normalize()
	require.Equal(t, modelprovider.APIOpenAIResponses, cfg.SummaryModelAPIEffective())

	cfg.Models.Summary.API = modelprovider.APIOpenAICompletions
	cfg.Normalize()
	require.Equal(t, modelprovider.APIOpenAICompletions, cfg.SummaryModelAPIEffective())
}

func TestConfig_ResolveSummaryModelAuth_UsesSummaryAPIForDefaultBaseURL(t *testing.T) {
	cfg := &Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openrouter": {APIKey: "k"}},
			Main:      MainModelConfig{Name: constants.DefaultModel, Provider: "openrouter", API: modelprovider.APIOpenAICompletions},
			Summary:   SummaryModelConfig{API: modelprovider.APIOpenAIResponses},
		},
	}
	cfg.Normalize()

	auth, err := cfg.ResolveSummaryModelAuth()
	require.NoError(t, err)
	require.Equal(t, "https://openrouter.ai/api/v1", auth.BaseURL)
}

func TestConfig_ResolveSummaryModelAuthMatchesMainWhenUnset(t *testing.T) {
	cfg := &Config{
		Name:   "test-agent",
		Models: ModelsConfig{Main: MainModelConfig{APIKey: "k", Name: constants.DefaultModel, Provider: "openrouter"}},
	}

	main, err := cfg.ResolveModelAuth()
	require.NoError(t, err)
	sum, err := cfg.ResolveSummaryModelAuth()
	require.NoError(t, err)
	require.True(t, ModelAuthEqual(main, sum))
}

func TestConfig_ResolveSummaryModelAuthUsesOpenAIWhenSummaryProviderDiffers(t *testing.T) {
	stubModelProviderToken(t, func(string) (StoredModelCredential, error) {
		return StoredModelCredential{}, nil
	})

	cfg := &Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{
				"openrouter": {APIKey: "k"},
				"openai":     {APIKey: "k"},
			},
			Main:    MainModelConfig{Name: constants.DefaultModel, Provider: "openrouter"},
			Summary: SummaryModelConfig{Provider: "openai", BaseURL: "https://api.example/v1"},
		},
	}
	cfg.Normalize()

	auth, err := cfg.ResolveSummaryModelAuth()
	require.NoError(t, err)
	require.Equal(t, "openai", auth.Provider)
	require.Equal(t, "https://api.example/v1", auth.BaseURL)
	require.Equal(t, "k", auth.APIKey)
}

func TestConfig_ModelAuthEqual(t *testing.T) {
	require.True(t, ModelAuthEqual(
		ModelAuth{Provider: "openai", API: modelprovider.APIOpenAIResponses, BaseURL: "http://a", APIKey: "k"},
		ModelAuth{Provider: "openai", API: modelprovider.APIOpenAIResponses, BaseURL: "http://a", APIKey: "k"},
	))
	require.False(t, ModelAuthEqual(
		ModelAuth{Provider: "openai", API: modelprovider.APIOpenAIResponses, BaseURL: "http://a", APIKey: "k"},
		ModelAuth{Provider: "openrouter", API: modelprovider.APIOpenAIResponses, BaseURL: "http://a", APIKey: "k"},
	))
	require.False(t, ModelAuthEqual(
		ModelAuth{Provider: "openai", API: modelprovider.APIOpenAIResponses, BaseURL: "http://a", APIKey: "k"},
		ModelAuth{Provider: "openai", API: modelprovider.APIOpenAICompletions, BaseURL: "http://a", APIKey: "k"},
	))
	require.False(t, ModelAuthEqual(
		ModelAuth{Provider: "openai", API: modelprovider.APIOpenAIResponses, BaseURL: "http://a", APIKey: "k"},
		ModelAuth{
			Provider: "openai",
			API:      modelprovider.APIOpenAIResponses,
			BaseURL:  "http://a",
			APIKey:   "k",
			Headers:  map[string]string{"Authorization": "Bearer token"},
		},
	))
}

func TestModelAuth_AuthType(t *testing.T) {
	require.Equal(t, appcredential.TypeOAuth, ModelAuth{
		CredentialSource: ModelCredentialSource{Type: " OAuth "},
	}.AuthType())
	require.Equal(t, modelAuthTypeAPIKey, ModelAuth{APIKey: " key "}.AuthType())
	require.Equal(t, string(ModelCredentialSourceProviderEnv), ModelAuth{
		CredentialSource: ModelCredentialSource{Kind: ModelCredentialSourceProviderEnv},
	}.AuthType())
	require.Equal(t, modelAuthTypeNone, ModelAuth{}.AuthType())
}

func TestResolveModelAuth_CoversDefaultBranchAndNilReceiver(t *testing.T) {
	var cfg *Config
	_, err := cfg.ResolveModelAuth()
	require.EqualError(t, err, "config is required")

	cfg = &Config{
		Models: ModelsConfig{Main: MainModelConfig{APIKey: "key", Provider: "custom"}},
	}
	auth, err := cfg.ResolveModelAuth()
	require.NoError(t, err)
	require.Equal(t, "key", auth.APIKey)
}

func TestDefaultBaseURLForProvider_DefaultsEmptyAPI(t *testing.T) {
	require.Equal(t, "https://openrouter.ai/api/v1", getDefaultBaseURLForProvider("openrouter", ""))
	require.Equal(t, "https://openrouter.ai/api/v1", getDefaultBaseURLForProvider("openrouter", "   "))
	require.Equal(t, "https://api.openai.com/v1", getDefaultBaseURLForProvider("openai", modelprovider.APIOpenAICompletions))
	require.Equal(t, "https://api.openai.com/v1", getDefaultBaseURLForProvider("openai", modelprovider.APIOpenAIResponses))
	require.Equal(t, "https://openrouter.ai/api/v1/embeddings", getDefaultBaseURLForProvider("openrouter", modelprovider.APIOpenRouterEmbeddings))
	require.Equal(t, "https://api.openai.com/v1/embeddings", getDefaultBaseURLForProvider("openai", modelprovider.APIOpenAIEmbeddings))
}

func TestModelProviders_CoverDayOneProviderBaseURLs(t *testing.T) {
	require.True(t, hasModelProvider("openai"))
	require.True(t, hasModelProvider("openrouter"))
	require.True(t, hasModelProvider("anthropic"))
	require.True(t, hasModelProvider("github-copilot"))
	require.True(t, hasModelProvider("openai-codex"))
	require.Equal(t, "anthropic, github-copilot, ollama, openai, openai-codex, openrouter", getModelProviderList())
	openai, ok := modelRegistry.GetProvider("openai")
	require.True(t, ok)
	require.Equal(t, "openai", openai.ID)
	openrouter, ok := modelRegistry.GetProvider("openrouter")
	require.True(t, ok)
	require.Equal(t, "openrouter", openrouter.ID)
	anthropic, ok := modelRegistry.GetProvider("anthropic")
	require.True(t, ok)
	require.Equal(t, []string{"ANTHROPIC_API_KEY"}, anthropic.APIKeyEnv)
	copilot, ok := modelRegistry.GetProvider("github-copilot")
	require.True(t, ok)
	require.Equal(t, []string{"COPILOT_GITHUB_TOKEN"}, copilot.APIKeyEnv)

	require.Equal(t, constants.DefaultOpenAIBaseURL, getDefaultBaseURLForProvider("openai", modelprovider.APIOpenAICompletions))
	require.Equal(t, constants.DefaultOpenAIBaseURL, getDefaultBaseURLForProvider("openai", modelprovider.APIOpenAIResponses))
	require.Equal(t, constants.DefaultOpenAIEmbeddingsBaseURL, getDefaultBaseURLForProvider("openai", modelprovider.APIOpenAIEmbeddings))
	require.Equal(t, constants.DefaultAnthropicBaseURL, getDefaultBaseURLForProvider("anthropic", modelprovider.APIAnthropicMessages))
	require.Equal(t, constants.DefaultOpenRouterBaseURL, getDefaultBaseURLForProvider("openrouter", modelprovider.APIOpenAICompletions))
	require.Equal(t, constants.DefaultOpenRouterResponsesBaseURL, getDefaultBaseURLForProvider("openrouter", modelprovider.APIOpenAIResponses))
	require.Equal(t, constants.DefaultOpenRouterEmbeddingsBaseURL, getDefaultBaseURLForProvider("openrouter", modelprovider.APIOpenRouterEmbeddings))
}

func TestConfig_ModelSlotsResolveProviderBaseURLsThroughRegistry(t *testing.T) {
	stubProviderDefaultBaseURL(t, "openrouter", modelprovider.APIOpenAICompletions, "https://registry.openrouter.example/v1")
	stubProviderDefaultBaseURL(t, "openai", modelprovider.APIOpenAIResponses, "https://registry.openai.example/v1")
	stubProviderDefaultBaseURL(t, "openrouter", modelprovider.APIOpenRouterEmbeddings, "https://registry.openrouter.example/v1/embeddings")

	cfg := &Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Main: MainModelConfig{
				Name:     constants.DefaultModel,
				Provider: "openrouter",
				API:      modelprovider.APIOpenAICompletions,
				APIKey:   "router-key",
			},
			Summary: SummaryModelConfig{
				Provider: "openai",
				API:      modelprovider.APIOpenAIResponses,
				APIKey:   "openai-key",
			},
			Embedding: EmbeddingModelConfig{
				Name:     constants.DefaultProfileEmbeddingModel,
				Provider: "openrouter",
				APIKey:   "embedding-key",
			},
		},
	}

	mainAuth, err := cfg.ResolveModelAuth()
	require.NoError(t, err)
	require.Equal(t, ModelAuth{
		Provider:         "openrouter",
		API:              modelprovider.APIOpenAICompletions,
		APIKey:           "router-key",
		BaseURL:          "https://registry.openrouter.example/v1",
		CredentialSource: ModelCredentialSource{Kind: ModelCredentialSourceRoleConfig},
	}, mainAuth)

	summaryAuth, err := cfg.ResolveSummaryModelAuth()
	require.NoError(t, err)
	require.Equal(t, ModelAuth{
		Provider:         "openai",
		API:              modelprovider.APIOpenAIResponses,
		APIKey:           "openai-key",
		BaseURL:          "https://registry.openai.example/v1",
		CredentialSource: ModelCredentialSource{Kind: ModelCredentialSourceRoleConfig},
	}, summaryAuth)

	embeddingAuth, err := cfg.ResolveEmbeddingModelAuth()
	require.NoError(t, err)
	require.Equal(t, ModelAuth{
		Provider:         "openrouter",
		API:              modelprovider.APIOpenRouterEmbeddings,
		APIKey:           "embedding-key",
		BaseURL:          "https://registry.openrouter.example/v1/embeddings",
		CredentialSource: ModelCredentialSource{Kind: ModelCredentialSourceRoleConfig},
	}, embeddingAuth)
}

func TestDefaultBaseURLForProvider_ReturnsEmptyForUnknownMode(t *testing.T) {
	require.Empty(t, getDefaultBaseURLForProvider("openrouter", "not-a-mode"))
}

func TestConfig_NilReceiver_EffectiveHelpers(t *testing.T) {
	var cfg *Config

	require.True(t, cfg.StreamEnabled())
	require.Equal(t, constants.DefaultSafetyInputEnabled, cfg.InputSafetyEnabled())
	require.Equal(t, constants.DefaultSafetyOutputEnabled, cfg.OutputSafetyEnabled())
	require.True(t, cfg.OutputPIIRedactionEnabled())
	require.Equal(t, constants.DefaultTUIThinkingComposerEnabled, cfg.TUIThinkingComposerEnabled())
	require.Equal(t, constants.DefaultModelMaxRetries, cfg.ModelMaxRetriesEffective())
	require.Equal(t, "", cfg.SummaryModelEffective())
	require.Equal(t, "", cfg.SummaryProviderEffective())
	require.Equal(t, "", cfg.MainModelAPIEffective())
	require.Equal(t, "", cfg.SummaryModelAPIEffective())
	require.Equal(t, constants.RerankerDeterministic, cfg.RerankerEffective())
	require.False(t, cfg.MemoryEnabled())
	require.False(t, cfg.MemoryPinnedEnabled())
	require.False(t, cfg.MemoryRetrievalEnabled())
	require.False(t, cfg.MemoryFlushEnabled())
	require.False(t, cfg.MemoryEpisodicEnabled())
	require.False(t, cfg.MemoryReflectionEnabled())
	require.False(t, cfg.MemoryPromotionEnabled())
	require.False(t, cfg.MemoryWriteEnabled())
	require.Equal(t, "", cfg.RerankerModelEffective())
	require.Equal(t, "", cfg.RerankerProviderEffective())
	require.Equal(t, "", cfg.RerankerModelAPIEffective())
	require.Equal(t, "", cfg.RerankerModelAPIEffectiveForModel("model"))
	require.Equal(t, "", cfg.EmbeddingModelAPIEffective())
	require.Equal(t, RerankerEffectiveConfig{}, cfg.RerankerOverrideEffective(RerankerOverrideConfig{}))

	_, err := cfg.ResolveSummaryModelAuth()
	require.EqualError(t, err, "config is required")

	_, err = cfg.ResolveRerankerModelAuth()
	require.EqualError(t, err, "config is required")

	_, err = cfg.ResolveEmbeddingModelAuth()
	require.EqualError(t, err, "config is required")
}

func TestConfig_EffectiveAuthHelpersCoverFallbackBranches(t *testing.T) {
	t.Setenv("EFFECTIVE_TEST_KEY", " env-value ")
	require.Equal(t, "", getModelAPIID("missing"))
	require.False(t, hasModelProvider("missing"))

	value, name := getCredentialFromEnv([]string{" ", "EFFECTIVE_TEST_KEY"})
	require.Equal(t, "env-value", value)
	require.Equal(t, "EFFECTIVE_TEST_KEY", name)

	value, name = getCredentialFromEnv([]string{" ", "MISSING_EFFECTIVE_TEST_KEY"})
	require.Empty(t, value)
	require.Empty(t, name)

	require.Equal(t, "api-key", getStoredModelCredentialValue(StoredModelCredential{
		Type: appcredential.TypeAPIKey,
		Key:  " api-key ",
	}))
	require.Equal(t, "oauth-token", getStoredModelCredentialValue(StoredModelCredential{
		Type:  appcredential.TypeOAuth,
		Token: " oauth-token ",
	}))
	require.Equal(t, "bare-token", getStoredModelCredentialValue(StoredModelCredential{
		Token: " bare-token ",
	}))
	require.Empty(t, getStoredModelCredentialValue(StoredModelCredential{Type: "unknown", Token: "token"}))

	require.EqualError(
		t,
		newMissingModelCredentialError("", ""),
		"model API key is required; set a provider API key, provider env var, role apiKey, or provider login",
	)
	require.EqualError(
		t,
		newMissingModelCredentialError("embedding", ""),
		"embedding API key is required; set a provider API key, provider env var, or role apiKey",
	)
}

func TestConfig_StoredCredentialHeaderHelpersCoverFallbackBranches(t *testing.T) {
	headers, err := getStoredModelCredentialHeaders("openai", StoredModelCredential{
		Type: appcredential.TypeAPIKey,
		Key:  "key",
	})
	require.NoError(t, err)
	require.Nil(t, headers)

	original := getSubscriptionProvider
	getSubscriptionProvider = nil
	headers, err = getStoredModelCredentialHeaders("openai", StoredModelCredential{
		Type:  appcredential.TypeOAuth,
		Token: "token",
	})
	require.NoError(t, err)
	require.Nil(t, headers)
	getSubscriptionProvider = original

	stubSubscriptionProvider(t, func(string) (appcredential.SubscriptionProvider, bool) {
		return nil, false
	})
	headers, err = getStoredModelCredentialHeaders("openai", StoredModelCredential{
		Type:  appcredential.TypeOAuth,
		Token: "token",
	})
	require.NoError(t, err)
	require.Nil(t, headers)

	stubSubscriptionProvider(t, func(string) (appcredential.SubscriptionProvider, bool) {
		return modelAuthSubscriptionProvider{
			headers: map[string]string{
				" Authorization ": " Bearer token ",
				"blank":           " ",
				" ":               "ignored",
			},
		}, true
	})
	headers, err = getStoredModelCredentialHeaders("openai", StoredModelCredential{
		Type:  appcredential.TypeOAuth,
		Token: "token",
	})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"Authorization": "Bearer token"}, headers)

	stubSubscriptionProvider(t, func(string) (appcredential.SubscriptionProvider, bool) {
		return modelAuthSubscriptionProvider{err: errors.New("headers failed")}, true
	})
	_, err = getStoredModelCredentialHeaders("openai", StoredModelCredential{
		Type:  appcredential.TypeOAuth,
		Token: "token",
	})
	require.EqualError(t, err, "headers failed")
}

func TestConfig_OAuthModelSupportAndSubscriptionDefaultsFallbacks(t *testing.T) {
	require.NoError(t, checkOAuthModelSupported("", "openai", ""))
	require.NoError(t, checkOAuthModelSupported("model", "missing", "gpt-4o-mini"))
	require.NoError(t, checkOAuthModelSupported("model", "anthropic", "claude-sonnet-4-6"))
	require.EqualError(
		t,
		checkOAuthModelSupported("", "anthropic", "claude-sonnet-4-5"),
		`model "claude-sonnet-4-5" is not available through OAuth for provider "anthropic"`,
	)
	require.EqualError(
		t,
		checkOAuthModelSupported("", "anthropic", "claude-3-5-sonnet-20241022"),
		`model "claude-3-5-sonnet-20241022" is not available through OAuth for provider "anthropic"`,
	)
	require.EqualError(
		t,
		checkOAuthModelSupported("", "openai", constants.DefaultModel),
		`model "gpt-4o-mini" is not available through OAuth for provider "openai"`,
	)
	require.EqualError(
		t,
		checkOAuthModelSupported("", constants.ModelProviderOpenAICodex, "gpt-5.2-codex"),
		`model "gpt-5.2-codex" is not available through OAuth for provider "openai-codex"`,
	)

	auth := ModelAuth{}
	auth.applySubscriptionDefaults()
	require.Empty(t, auth.BaseURL)

	auth = ModelAuth{
		Provider: constants.ModelProviderOpenAICodex,
		BaseURL:  "https://custom.example/v1",
		CredentialSource: ModelCredentialSource{
			Kind: ModelCredentialSourceTokenStore,
			Type: appcredential.TypeOAuth,
		},
	}
	auth.applySubscriptionDefaults()
	require.Equal(t, "https://custom.example/v1", auth.BaseURL)

	auth.BaseURL = constants.DefaultOpenAIBaseURL
	auth.applySubscriptionDefaults()
	require.Equal(t, constants.DefaultOpenAISubscriptionBaseURL, auth.BaseURL)
	require.False(t, auth.SupportsMaxOutputTokens())

	auth.Provider = constants.ModelProviderAnthropic
	require.True(t, auth.SupportsMaxOutputTokens())

	auth.CredentialSource.Type = appcredential.TypeAPIKey
	require.True(t, auth.SupportsMaxOutputTokens())

	cfg := &Config{
		Models: ModelsConfig{
			Main: MainModelConfig{
				Name:     "gpt-5.4",
				Provider: constants.ModelProviderOpenAICodex,
				APIKey:   "key",
			},
		},
	}
	require.True(t, cfg.SummaryModelSupportsMaxOutputTokens())
	require.True(t, (*Config)(nil).SummaryModelSupportsMaxOutputTokens())

	stubModelProviderToken(t, func(string) (StoredModelCredential, error) {
		return StoredModelCredential{}, errors.New("credential store unavailable")
	})
	cfg.Models.Main.APIKey = ""
	require.True(t, cfg.SummaryModelSupportsMaxOutputTokens())

	var nilAuth *ModelAuth
	nilAuth.applySubscriptionDefaults()
}

func TestConfig_SummaryModelMaxOutputTokensEffective(t *testing.T) {
	stubModelProviderToken(t, func(provider string) (StoredModelCredential, error) {
		require.Equal(t, constants.ModelProviderOpenAICodex, provider)
		return StoredModelCredential{Type: appcredential.TypeOAuth, Token: "stored-token"}, nil
	})
	cfg := &Config{
		Models: ModelsConfig{
			Main: MainModelConfig{
				Name:     "gpt-5.4",
				Provider: constants.ModelProviderOpenAICodex,
			},
		},
	}

	require.Zero(t, cfg.SummaryModelMaxOutputTokensEffective(512))
	require.Zero(t, cfg.SummaryModelMaxOutputTokensEffective(0))
	require.Zero(t, cfg.SummaryModelMaxOutputTokensEffective(-1))

	cfg.Models.Main.APIKey = "key"
	require.Equal(t, int64(512), cfg.SummaryModelMaxOutputTokensEffective(512))
	require.Equal(t, int64(512), (*Config)(nil).SummaryModelMaxOutputTokensEffective(512))
}

func TestConfig_StringMapHelpersNormalizeAndCompare(t *testing.T) {
	require.Nil(t, normalizeStringMap(nil))
	require.Nil(t, normalizeStringMap(map[string]string{" ": "ignored", "blank": " "}))
	require.Equal(t, map[string]string{"A": "B"}, normalizeStringMap(map[string]string{" A ": " B "}))

	require.True(t, stringMapsEqual(
		map[string]string{" A ": " B "},
		map[string]string{"A": "B"},
	))
	require.False(t, stringMapsEqual(
		map[string]string{"A": "B"},
		map[string]string{"A": "C"},
	))
	require.False(t, stringMapsEqual(
		map[string]string{"A": "B"},
		map[string]string{"A": "B", "C": "D"},
	))
}

func TestConfig_StoredCredentialLoadAndRefreshFallbacks(t *testing.T) {
	originalLoad := loadStoredProviderToken
	originalRefresh := refreshStoredProviderToken
	loadStoredProviderToken = nil
	refreshStoredProviderToken = nil
	t.Cleanup(func() {
		loadStoredProviderToken = originalLoad
		refreshStoredProviderToken = originalRefresh
	})

	credential, err := loadStoredModelCredential("openai")
	require.NoError(t, err)
	require.Equal(t, StoredModelCredential{}, credential)

	refreshed, ok, err := refreshStoredModelCredential("openai")
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, StoredModelCredential{}, refreshed)
}

func TestConfig_ResolveCredentialForProviderCoversRefreshBranches(t *testing.T) {
	expired := time.Now().Add(-time.Hour)
	cfg := &Config{}

	stubModelProviderToken(t, func(string) (StoredModelCredential, error) {
		return StoredModelCredential{
			Type:      appcredential.TypeOAuth,
			Token:     "expired",
			ExpiresAt: &expired,
		}, nil
	})
	stubRefreshModelProviderToken(t, func(context.Context, string) (StoredModelCredential, bool, error) {
		return StoredModelCredential{}, false, errors.New("refresh failed")
	})
	_, err := cfg.resolveCredentialForProvider(constants.ModelProviderOpenAICodex, "", true, "model", "gpt-5.4")
	require.EqualError(t, err, "refresh failed")

	stubRefreshModelProviderToken(t, func(context.Context, string) (StoredModelCredential, bool, error) {
		return StoredModelCredential{}, false, nil
	})
	credential, err := cfg.resolveCredentialForProvider(constants.ModelProviderOpenAICodex, "", true, "model", "gpt-5.4")
	require.NoError(t, err)
	require.Equal(t, resolvedModelCredential{}, credential)

	freshExpiry := time.Now().Add(time.Hour)
	stubRefreshModelProviderToken(t, func(context.Context, string) (StoredModelCredential, bool, error) {
		return StoredModelCredential{
			Type:      appcredential.TypeOAuth,
			Token:     "fresh",
			ExpiresAt: &freshExpiry,
		}, true, nil
	})
	stubSubscriptionProvider(t, func(string) (appcredential.SubscriptionProvider, bool) {
		return modelAuthSubscriptionProvider{err: errors.New("headers failed")}, true
	})
	_, err = cfg.resolveCredentialForProvider(constants.ModelProviderOpenAICodex, "", true, "model", "gpt-5.4")
	require.EqualError(t, err, "headers failed")

	stubModelProviderToken(t, func(string) (StoredModelCredential, error) {
		return StoredModelCredential{
			Type:      appcredential.TypeAPIKey,
			Key:       "expired-key",
			ExpiresAt: &expired,
		}, nil
	})
	stubRefreshModelProviderToken(t, func(context.Context, string) (StoredModelCredential, bool, error) {
		return StoredModelCredential{
			Type:      appcredential.TypeOAuth,
			Token:     "fresh",
			ExpiresAt: &freshExpiry,
		}, true, nil
	})
	_, err = cfg.resolveCredentialForProvider("openai", "", true, "model", constants.DefaultModel)
	require.EqualError(t, err, `model "gpt-4o-mini" is not available through OAuth for provider "openai"`)
}

func TestConfig_ResolveSummaryModelAuth_FailsWhenSummaryProviderHasNoKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	stubModelProviderToken(t, func(string) (StoredModelCredential, error) {
		return StoredModelCredential{}, nil
	})

	cfg := &Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openrouter": {APIKey: "router-only"}},
			Main:      MainModelConfig{Name: constants.DefaultModel, Provider: "openrouter"},
			Summary:   SummaryModelConfig{Provider: "openai", BaseURL: "https://api.openai.com/v1"},
		},
	}
	cfg.Normalize()

	_, err := cfg.ResolveSummaryModelAuth()
	require.ErrorContains(t, err, `model API key is required for provider "openai"`)
}

func TestConfig_ResolveSummaryModelAuth_ReturnsCredentialResolverError(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	stubModelProviderToken(t, func(string) (StoredModelCredential, error) {
		return StoredModelCredential{}, errors.New("store failed")
	})

	cfg := &Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Main: MainModelConfig{Name: "gpt-5.4-mini", Provider: "openai"},
		},
	}

	_, err := cfg.ResolveSummaryModelAuth()
	require.EqualError(t, err, "store failed")
}

func TestConfig_ResolveEmbeddingModelAuth_ReturnsCredentialResolverError(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	stubModelProviderToken(t, func(string) (StoredModelCredential, error) {
		return StoredModelCredential{}, errors.New("store failed")
	})

	cfg := &Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Main:      MainModelConfig{Name: "gpt-5.4-mini", Provider: "openai"},
			Embedding: EmbeddingModelConfig{Name: constants.DefaultProfileEmbeddingModel, Provider: "openai"},
		},
	}

	_, err := cfg.ResolveEmbeddingModelAuth()
	require.EqualError(t, err, "store failed")
}

type modelAuthSubscriptionProvider struct {
	headers map[string]string
	err     error
}

func (p modelAuthSubscriptionProvider) Login(
	context.Context,
	appcredential.LoginOptions,
) (appcredential.StoredCredential, error) {
	return appcredential.StoredCredential{}, nil
}

func (p modelAuthSubscriptionProvider) Refresh(
	context.Context,
	appcredential.StoredCredential,
) (appcredential.StoredCredential, error) {
	return appcredential.StoredCredential{}, nil
}

func (p modelAuthSubscriptionProvider) AuthHeaders(
	context.Context,
	appcredential.StoredCredential,
) (map[string]string, error) {
	if p.err != nil {
		return nil, p.err
	}

	return p.headers, nil
}
