package client

import (
	"context"
	"errors"
	"testing"

	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/constants"
	models "github.com/xymorphic/morph/internal/model"
	modelprovider "github.com/xymorphic/morph/internal/model/provider"
	provider_anthropic "github.com/xymorphic/morph/internal/model/provider_anthropic"
	provider_openai "github.com/xymorphic/morph/internal/model/provider_openai"
)

type stubClient struct{}

func (stubClient) Complete(context.Context, models.Request) (*models.Response, error) {
	return &models.Response{}, nil
}

func (stubClient) CompleteStream(
	context.Context,
	models.Request,
	func(models.StreamDelta),
) (*models.Response, error) {
	return &models.Response{}, nil
}

func TestClientFactory_ResolveUsesRegistryDefaults(t *testing.T) {
	factory := NewClientFactory(modelprovider.DefaultRegistry())

	resolved, err := factory.Resolve(ClientRequest{
		Role:     ModelRoleMain,
		Model:    "gpt-4o-mini",
		Provider: " openai ",
		APIKey:   " key ",
	})

	require.NoError(t, err)
	require.Equal(t, ModelRoleMain, resolved.Role)
	require.True(t, resolved.ModelKnown)
	require.Equal(t, "gpt-4o-mini", resolved.Model.ID)
	require.Equal(t, "openai", resolved.Provider.ID)
	require.Equal(t, modelprovider.APIOpenAIResponses, resolved.API.ID)
	require.Equal(t, "key", resolved.APIKey)
	require.Equal(t, "https://api.openai.com/v1", resolved.BaseURL)
}

func TestNewClientFactory_UsesDefaultRegistryWhenNil(t *testing.T) {
	factory := NewClientFactory(nil)

	resolved, err := factory.Resolve(ClientRequest{Provider: "openai"})

	require.NoError(t, err)
	require.Equal(t, "openai", resolved.Provider.ID)
}

func TestNewDefaultClientFactory_UsesBuiltInRegistry(t *testing.T) {
	factory := NewDefaultClientFactory()

	resolved, err := factory.Resolve(ClientRequest{Provider: "openrouter"})

	require.NoError(t, err)
	require.Equal(t, modelprovider.APIOpenAIResponses, resolved.API.ID)
}

func TestClientFactory_ResolveUsesRequestOverrides(t *testing.T) {
	factory := NewClientFactory(modelprovider.DefaultRegistry())

	resolved, err := factory.Resolve(ClientRequest{
		Role:       ModelRoleSummary,
		Model:      "custom-model",
		Provider:   "openai",
		API:        modelprovider.APIOpenAICompletions,
		APIKey:     "key",
		BaseURL:    " https://proxy.example/v1 ",
		Headers:    map[string]string{" x-request ": " custom "},
		MaxRetries: 7,
	})

	require.NoError(t, err)
	require.Equal(t, ModelRoleSummary, resolved.Role)
	require.False(t, resolved.ModelKnown)
	require.Equal(t, "custom-model", resolved.Model.ID)
	require.Equal(t, "openai", resolved.Provider.ID)
	require.Equal(t, modelprovider.APIOpenAICompletions, resolved.API.ID)
	require.Equal(t, "https://proxy.example/v1", resolved.BaseURL)
	require.Equal(t, map[string]string{"x-request": "custom"}, resolved.Headers)
	require.Equal(t, 7, resolved.MaxRetries)
}

func TestClientFactory_ResolveMergesProviderAndRequestHeaders(t *testing.T) {
	registry := modelprovider.NewRegistry(
		[]modelprovider.APIDefinition{{ID: modelprovider.APIOpenAIResponses}},
		[]modelprovider.ProviderDefinition{{
			ID:         "custom",
			DefaultAPI: modelprovider.APIOpenAIResponses,
			BaseURLs: map[string]string{
				modelprovider.APIOpenAIResponses: "https://custom.example/v1",
			},
			Headers: map[string]string{
				"x-provider": "provider",
				"x-override": "provider",
			},
		}},
		nil,
	)
	factory := NewClientFactory(registry)

	resolved, err := factory.Resolve(ClientRequest{
		Provider: "custom",
		Headers: map[string]string{
			"x-request":  "request",
			"x-override": "request",
			" ":          "ignored",
		},
	})

	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"x-provider": "provider",
		"x-request":  "request",
		"x-override": "request",
	}, resolved.Headers)
}

func TestClientFactory_ResolveRejectsInvalidRequests(t *testing.T) {
	cases := []struct {
		name string
		req  ClientRequest
		err  string
	}{
		{
			name: "missing provider",
			req:  ClientRequest{},
			err:  "model provider is required",
		},
		{
			name: "unknown provider",
			req:  ClientRequest{Provider: "missing"},
			err:  `model provider "missing" is not registered`,
		},
		{
			name: "unknown api",
			req:  ClientRequest{Provider: "openai", API: "missing"},
			err:  `model API "missing" is not registered`,
		},
		{
			name: "missing base url",
			req: ClientRequest{
				Provider: "custom",
				API:      modelprovider.APIOpenAICompletions,
			},
			err: `model base URL is required for provider "custom" API "openai-completions"`,
		},
	}

	registry := modelprovider.NewRegistry(
		[]modelprovider.APIDefinition{{ID: modelprovider.APIOpenAICompletions}},
		[]modelprovider.ProviderDefinition{{ID: "custom", DefaultAPI: modelprovider.APIOpenAICompletions}},
		nil,
	)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			factory := NewClientFactory(modelprovider.DefaultRegistry())
			if tc.name == "missing base url" {
				factory = NewClientFactory(registry)
			}

			_, err := factory.Resolve(tc.req)
			require.EqualError(t, err, tc.err)
		})
	}
}

func TestClientFactory_NewClientBuildsOpenAICompatibleClient(t *testing.T) {
	var capturedKey string
	var capturedProvider string
	var capturedOptions int
	factory := NewClientFactory(modelprovider.DefaultRegistry())
	factory.OpenAIClient = func(apiKey string, _ string, provider string, _ *modelprovider.Registry, opts ...option.RequestOption) (models.Client, error) {
		capturedKey = apiKey
		capturedProvider = provider
		capturedOptions = len(opts)
		return &provider_openai.OpenAIClient{}, nil
	}

	client, err := factory.NewClient(ClientRequest{
		Provider:   "openai",
		API:        modelprovider.APIOpenAIResponses,
		APIKey:     "key",
		BaseURL:    "https://api.example/v1",
		Headers:    map[string]string{"x-test": "value"},
		MaxRetries: 3,
	})

	require.NoError(t, err)
	require.NotNil(t, client)
	require.Equal(t, "key", capturedKey)
	require.Equal(t, "openai", capturedProvider)
	require.Equal(t, 3, capturedOptions)
}

func TestClientFactory_NewClientForcesResponsesStreamForOpenAISubscription(t *testing.T) {
	factory := NewClientFactory(modelprovider.DefaultRegistry())
	factory.OpenAIClient = func(string, string, string, *modelprovider.Registry, ...option.RequestOption) (models.Client, error) {
		return &provider_openai.OpenAIClient{}, nil
	}

	subscriptionClient, err := factory.NewClient(ClientRequest{
		Provider: constants.ModelProviderOpenAI,
		API:      modelprovider.APIOpenAIResponses,
		BaseURL:  constants.DefaultOpenAISubscriptionBaseURL,
	})
	require.NoError(t, err)

	openAIClient, ok := subscriptionClient.(*provider_openai.OpenAIClient)
	require.True(t, ok)
	require.True(t, openAIClient.ForceResponsesStreamEnabled())

	apiClient, err := factory.NewClient(ClientRequest{
		Provider: constants.ModelProviderOpenAI,
		API:      modelprovider.APIOpenAIResponses,
		BaseURL:  constants.DefaultOpenAIBaseURL,
	})
	require.NoError(t, err)

	openAIClient, ok = apiClient.(*provider_openai.OpenAIClient)
	require.True(t, ok)
	require.False(t, openAIClient.ForceResponsesStreamEnabled())
}

func TestClientFactory_NewClientRoutesRequestsThroughResolvedAPI(t *testing.T) {
	var capturedAPI string
	factory := NewClientFactory(modelprovider.DefaultRegistry())
	factory.OpenAIClient = func(_ string, api string, _ string, _ *modelprovider.Registry, _ ...option.RequestOption) (models.Client, error) {
		capturedAPI = api
		return &provider_openai.OpenAIClient{}, nil
	}

	client, err := factory.NewClient(ClientRequest{
		Provider: "openai",
		API:      modelprovider.APIOpenAIResponses,
		BaseURL:  "https://api.example/v1",
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	require.Equal(t, modelprovider.APIOpenAIResponses, capturedAPI)
}

func TestClientFactory_NewClientBuildsAnthropicClient(t *testing.T) {
	var capturedKey string
	var capturedOptions int
	factory := NewClientFactory(modelprovider.DefaultRegistry())
	factory.AnthropicClient = func(apiKey string, opts ...anthropicoption.RequestOption) (models.Client, error) {
		capturedKey = apiKey
		capturedOptions = len(opts)
		return &provider_anthropic.AnthropicClient{}, nil
	}

	client, err := factory.NewClient(ClientRequest{
		Provider:   "anthropic",
		APIKey:     "key",
		Headers:    map[string]string{"x-test": "value"},
		MaxRetries: 3,
	})

	require.NoError(t, err)
	require.NotNil(t, client)
	require.Equal(t, "key", capturedKey)
	require.Equal(t, 3, capturedOptions)
}

func TestClientFactory_NewClientBuildsOllamaClient(t *testing.T) {
	var capturedBaseURL string
	var capturedHeaders map[string]string
	factory := NewClientFactory(modelprovider.DefaultRegistry())
	factory.OllamaClient = func(baseURL string, headers map[string]string) (models.Client, error) {
		capturedBaseURL = baseURL
		capturedHeaders = headers
		return stubClient{}, nil
	}

	client, err := factory.NewClient(ClientRequest{
		Provider: constants.ModelProviderOllama,
		API:      modelprovider.APIOllamaNative,
		BaseURL:  "http://127.0.0.1:11434",
		Headers:  map[string]string{"x-test": "value"},
	})

	require.NoError(t, err)
	require.NotNil(t, client)
	require.Equal(t, "http://127.0.0.1:11434", capturedBaseURL)
	require.Equal(t, map[string]string{"x-test": "value"}, capturedHeaders)
}

func TestClientFactory_NewClientBuildsAnthropicOAuthClientWithoutAPIKey(t *testing.T) {
	var capturedKey string
	var capturedOptions int
	factory := NewClientFactory(modelprovider.DefaultRegistry())
	factory.AnthropicClient = func(apiKey string, opts ...anthropicoption.RequestOption) (models.Client, error) {
		capturedKey = apiKey
		capturedOptions = len(opts)
		return &provider_anthropic.AnthropicClient{}, nil
	}

	client, err := factory.NewClient(ClientRequest{
		Provider: "anthropic",
		APIKey:   "oauth-token",
		Headers: map[string]string{
			"Authorization":  "Bearer oauth-token",
			"anthropic-beta": "claude-code-20250219,oauth-2025-04-20",
		},
		MaxRetries: 3,
	})

	require.NoError(t, err)
	require.NotNil(t, client)
	require.Empty(t, capturedKey)
	require.Equal(t, 4, capturedOptions)
	anthropicClient, ok := client.(*provider_anthropic.AnthropicClient)
	require.True(t, ok)
	require.True(t, anthropicClient.SubscriptionAuthEnabled())
}

func TestClientFactory_NewClientBuildsCopilotAnthropicBearerClientWithoutAnthropicSubscriptionAuth(t *testing.T) {
	var capturedKey string
	factory := NewClientFactory(modelprovider.DefaultRegistry())
	factory.AnthropicClient = func(apiKey string, _ ...anthropicoption.RequestOption) (models.Client, error) {
		capturedKey = apiKey
		return &provider_anthropic.AnthropicClient{}, nil
	}

	client, err := factory.NewClient(ClientRequest{
		Provider: "github-copilot",
		API:      modelprovider.APIAnthropicMessages,
		Model:    "claude-sonnet-4-5",
		APIKey:   "copilot-token",
		Headers: map[string]string{
			"Authorization": "Bearer copilot-token",
			"X-Initiator":   "user",
		},
	})

	require.NoError(t, err)
	require.NotNil(t, client)
	require.Empty(t, capturedKey)
	anthropicClient, ok := client.(*provider_anthropic.AnthropicClient)
	require.True(t, ok)
	require.False(t, anthropicClient.SubscriptionAuthEnabled())
}

func TestHasHeader(t *testing.T) {
	require.True(t, hasHeader(map[string]string{" Authorization ": " Bearer token "}, "authorization"))
	require.False(t, hasHeader(map[string]string{"Authorization": " "}, "authorization"))
	require.False(t, hasHeader(map[string]string{"Authorization": "Bearer token"}, " "))
	require.False(t, hasHeader(nil, "authorization"))
}

func TestClientFactory_NewClientReturnsAnthropicBuilderError(t *testing.T) {
	factory := NewClientFactory(modelprovider.DefaultRegistry())
	factory.AnthropicClient = func(string, ...anthropicoption.RequestOption) (models.Client, error) {
		return nil, errors.New("anthropic build failed")
	}

	_, err := factory.NewClient(ClientRequest{Provider: "anthropic"})

	require.EqualError(t, err, "anthropic build failed")
}

func TestClientFactory_NewClientRejectsNilAnthropicClient(t *testing.T) {
	factory := NewClientFactory(modelprovider.DefaultRegistry())
	factory.AnthropicClient = func(string, ...anthropicoption.RequestOption) (models.Client, error) {
		return nil, nil
	}

	_, err := factory.NewClient(ClientRequest{Provider: "anthropic"})

	require.EqualError(t, err, "model client is required")
}

func TestClientFactory_NewClientReturnsResolveError(t *testing.T) {
	factory := NewClientFactory(modelprovider.DefaultRegistry())

	_, err := factory.NewClient(ClientRequest{})

	require.EqualError(t, err, "model provider is required")
}

func TestClientFactory_NewClientUsesDefaultBuilder(t *testing.T) {
	var factory *ClientFactory

	client, err := factory.NewClient(ClientRequest{
		Provider: "openai",
		API:      modelprovider.APIOpenAIResponses,
		BaseURL:  "https://api.example/v1",
	})

	require.NoError(t, err)
	require.NotNil(t, client)
}

func TestClientFactory_NewClientReusesEquivalentClients(t *testing.T) {
	var builds int
	factory := NewClientFactory(modelprovider.DefaultRegistry())
	factory.OpenAIClient = func(string, string, string, *modelprovider.Registry, ...option.RequestOption) (models.Client, error) {
		builds++
		return &provider_openai.OpenAIClient{}, nil
	}

	first, err := factory.NewClient(ClientRequest{
		Provider:   "openai",
		API:        modelprovider.APIOpenAIResponses,
		BaseURL:    "https://api.example/v1",
		Headers:    map[string]string{"x-b": "b", "x-a": "a"},
		MaxRetries: 3,
	})
	require.NoError(t, err)

	second, err := factory.NewClient(ClientRequest{
		Provider:   "OPENAI",
		API:        modelprovider.APIOpenAIResponses,
		BaseURL:    "https://api.example/v1",
		Headers:    map[string]string{"x-a": "a", "x-b": "b"},
		MaxRetries: 3,
	})
	require.NoError(t, err)

	third, err := factory.NewClient(ClientRequest{
		Provider:   "openai",
		API:        modelprovider.APIOpenAIResponses,
		BaseURL:    "https://api.example/v1",
		Headers:    map[string]string{"x-a": "changed", "x-b": "b"},
		MaxRetries: 3,
	})
	require.NoError(t, err)

	require.Same(t, first, second)
	require.NotSame(t, first, third)
	require.Equal(t, 2, builds)
}

func TestClientFactory_NewClientInitializesNilCache(t *testing.T) {
	var builds int
	factory := NewClientFactory(modelprovider.DefaultRegistry())
	factory.clients = nil
	factory.OpenAIClient = func(string, string, string, *modelprovider.Registry, ...option.RequestOption) (models.Client, error) {
		builds++
		return &provider_openai.OpenAIClient{}, nil
	}

	client, err := factory.NewClient(ClientRequest{
		Provider: "openai",
		API:      modelprovider.APIOpenAIResponses,
		BaseURL:  "https://api.example/v1",
	})

	require.NoError(t, err)
	require.NotNil(t, client)
	require.Equal(t, 1, builds)
	require.NotNil(t, factory.clients)
}

func TestClientFactory_NewClientReturnsBuilderError(t *testing.T) {
	factory := NewClientFactory(modelprovider.DefaultRegistry())
	factory.OpenAIClient = func(string, string, string, *modelprovider.Registry, ...option.RequestOption) (models.Client, error) {
		return nil, errors.New("build failed")
	}

	_, err := factory.NewClient(ClientRequest{
		Provider: "openai",
		API:      modelprovider.APIOpenAIResponses,
		BaseURL:  "https://api.example/v1",
	})

	require.EqualError(t, err, "build failed")
}

func TestClientFactory_NewClientReturnsOllamaBuilderError(t *testing.T) {
	factory := NewClientFactory(modelprovider.DefaultRegistry())
	factory.OllamaClient = func(string, map[string]string) (models.Client, error) {
		return nil, errors.New("ollama build failed")
	}

	_, err := factory.NewClient(ClientRequest{Provider: constants.ModelProviderOllama})

	require.EqualError(t, err, "ollama build failed")
}

func TestClientFactory_NewClientRejectsNilOllamaClient(t *testing.T) {
	factory := NewClientFactory(modelprovider.DefaultRegistry())
	factory.OllamaClient = func(string, map[string]string) (models.Client, error) {
		return nil, nil
	}

	_, err := factory.NewClient(ClientRequest{Provider: constants.ModelProviderOllama})

	require.EqualError(t, err, "model client is required")
}

func TestClientFactory_NewClientRejectsNilBuiltClient(t *testing.T) {
	factory := NewClientFactory(modelprovider.DefaultRegistry())
	factory.OpenAIClient = func(string, string, string, *modelprovider.Registry, ...option.RequestOption) (models.Client, error) {
		return nil, nil
	}

	_, err := factory.NewClient(ClientRequest{
		Provider: "openai",
		API:      modelprovider.APIOpenAIResponses,
		BaseURL:  "https://api.example/v1",
	})

	require.EqualError(t, err, "model client is required")
}

func TestClientFactory_NewClientRejectsUnsupportedChatAPI(t *testing.T) {
	factory := NewClientFactory(modelprovider.DefaultRegistry())

	_, err := factory.NewClient(ClientRequest{
		Provider: "openai",
		API:      modelprovider.APIOpenAIEmbeddings,
	})

	require.EqualError(t, err, `model API "openai-embeddings" is not supported for chat clients`)
}

func TestClientFactory_NilFactoryUsesDefaults(t *testing.T) {
	var factory *ClientFactory

	resolved, err := factory.Resolve(ClientRequest{Provider: "openai"})
	require.NoError(t, err)
	require.Equal(t, "openai", resolved.Provider.ID)
	require.Equal(t, modelprovider.APIOpenAIResponses, resolved.API.ID)
}
