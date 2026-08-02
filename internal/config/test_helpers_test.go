package config

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xymorphic/morph/internal/constants"
	appcredential "github.com/xymorphic/morph/internal/credential"
	modelprovider "github.com/xymorphic/morph/internal/model/provider"
	"github.com/xymorphic/morph/internal/profile"
)

func stubModelProviderToken(t *testing.T, fn func(string) (StoredModelCredential, error)) {
	t.Helper()

	original := loadStoredProviderToken
	loadStoredProviderToken = fn
	t.Cleanup(func() {
		loadStoredProviderToken = original
	})
}

func stubRefreshModelProviderToken(
	t *testing.T,
	fn func(context.Context, string) (StoredModelCredential, bool, error),
) {
	t.Helper()

	original := refreshStoredProviderToken
	refreshStoredProviderToken = fn
	t.Cleanup(func() {
		refreshStoredProviderToken = original
	})
}

func stubSubscriptionProvider(
	t *testing.T,
	fn func(string) (appcredential.SubscriptionProvider, bool),
) {
	t.Helper()

	original := getSubscriptionProvider
	getSubscriptionProvider = fn
	t.Cleanup(func() {
		getSubscriptionProvider = original
	})
}

func stubProviderDefaultBaseURL(t *testing.T, provider string, apiID string, value string) {
	t.Helper()

	api, ok := getModelAPI(apiID)
	require.True(t, ok)
	originalRegistry := modelRegistry
	modelRegistry = registryWithProviderBaseURL(t, originalRegistry, provider, api.ID, value)
	t.Cleanup(func() {
		modelRegistry = originalRegistry
	})
}

func stubModelRegistry(t *testing.T, registry *modelprovider.Registry) {
	t.Helper()

	originalRegistry := modelRegistry
	modelRegistry = registry
	t.Cleanup(func() {
		modelRegistry = originalRegistry
	})
}

func registryWithProviderBaseURL(
	t *testing.T,
	registry *modelprovider.Registry,
	provider string,
	api string,
	value string,
) *modelprovider.Registry {
	t.Helper()

	apis := []modelprovider.APIDefinition{
		{ID: modelprovider.APIOpenAICompletions},
		{ID: modelprovider.APIOpenAIResponses},
		{ID: modelprovider.APIOpenAIEmbeddings},
		{ID: modelprovider.APIOpenRouterEmbeddings},
	}
	providers := make([]modelprovider.ProviderDefinition, 0, len(registry.GetProviderIDs()))
	matched := false
	for _, providerID := range registry.GetProviderIDs() {
		definition, ok := registry.GetProvider(providerID)
		require.True(t, ok)
		if providerID == provider {
			matched = true
			definition.BaseURLs[api] = value
		}
		providers = append(providers, definition)
	}
	require.True(t, matched)

	return modelprovider.NewRegistry(apis, providers, nil)
}

func registryWithGenerationModel(providerID string, modelID string, contextWindow int) *modelprovider.Registry {
	return modelprovider.NewRegistry(
		[]modelprovider.APIDefinition{
			{ID: modelprovider.APIOpenAICompletions},
			{ID: modelprovider.APIOpenAIResponses},
			{ID: modelprovider.APIOpenAIEmbeddings},
			{ID: modelprovider.APIOpenRouterEmbeddings},
		},
		[]modelprovider.ProviderDefinition{
			{
				ID:             constants.ModelProviderOpenRouter,
				DefaultAPI:     modelprovider.APIOpenAIResponses,
				SupportsModels: true,
				BaseURLs: map[string]string{
					modelprovider.APIOpenAICompletions:    constants.DefaultOpenRouterBaseURL,
					modelprovider.APIOpenAIResponses:      constants.DefaultOpenRouterResponsesBaseURL,
					modelprovider.APIOpenRouterEmbeddings: constants.DefaultOpenRouterEmbeddingsBaseURL,
				},
			},
			{
				ID:             constants.ModelProviderOpenAI,
				DefaultAPI:     modelprovider.APIOpenAIResponses,
				SupportsModels: true,
				BaseURLs: map[string]string{
					modelprovider.APIOpenAICompletions: constants.DefaultOpenAIBaseURL,
					modelprovider.APIOpenAIResponses:   constants.DefaultOpenAIBaseURL,
					modelprovider.APIOpenAIEmbeddings:  constants.DefaultOpenAIEmbeddingsBaseURL,
				},
			},
		},
		[]modelprovider.ModelDefinition{
			{
				ID:            modelID,
				Provider:      providerID,
				API:           modelprovider.APIOpenAIResponses,
				Input:         []modelprovider.InputKind{modelprovider.InputText},
				ContextWindow: contextWindow,
			},
		},
	)
}

func clearEnvKeys(t *testing.T, keys ...string) {
	t.Helper()
	keys = append(keys, "OPENAI_API_KEY", "OPENROUTER_API_KEY", "ANTHROPIC_API_KEY", "COPILOT_GITHUB_TOKEN")
	for _, key := range keys {
		original, ok := os.LookupEnv(key)
		if ok {
			t.Cleanup(func() {
				_ = os.Setenv(key, original)
			})
		} else {
			t.Cleanup(func() {
				_ = os.Unsetenv(key)
			})
		}
		_ = os.Unsetenv(key)
	}
}

func setProfileHome(t *testing.T, home string) {
	t.Helper()

	original := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(original)
	})
	profile.SetActive(profile.Profile{Name: "test", HomeDir: home})
}

func testIntPtr(value int) *int {
	return &value
}
