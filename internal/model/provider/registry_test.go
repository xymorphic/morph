package provider

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wandxy/morph/internal/constants"
)

func TestDefaultRegistry_RegistersBuiltInAPIs(t *testing.T) {
	registry := DefaultRegistry()

	completions, ok := registry.GetAPI(APIOpenAICompletions)
	require.True(t, ok)
	require.Equal(t, APIOpenAICompletions, completions.ID)

	embeddings, ok := registry.GetAPI(APIOpenAIEmbeddings)
	require.True(t, ok)
	require.Equal(t, APIOpenAIEmbeddings, embeddings.ID)

	openRouterEmbeddings, ok := registry.GetAPI(APIOpenRouterEmbeddings)
	require.True(t, ok)
	require.Equal(t, APIOpenRouterEmbeddings, openRouterEmbeddings.ID)

	require.ElementsMatch(t, []string{
		APIAnthropicMessages,
		APIOllamaNative,
		APIOllamaEmbeddings,
		APIOpenAICompletions,
		APIOpenAIResponses,
		APIOpenAIEmbeddings,
		APIOpenRouterEmbeddings,
	}, registry.GetAPIIDs())

	anthropic, ok := registry.GetAPI(APIAnthropicMessages)
	require.True(t, ok)
	require.Equal(t, APIAnthropicMessages, anthropic.ID)

	ollamaNative, ok := registry.GetAPI(APIOllamaNative)
	require.True(t, ok)
	require.Equal(t, APIOllamaNative, ollamaNative.ID)

	ollamaEmbeddings, ok := registry.GetAPI(APIOllamaEmbeddings)
	require.True(t, ok)
	require.Equal(t, APIOllamaEmbeddings, ollamaEmbeddings.ID)
}

func TestDefaultRegistry_RegistersBuiltInProviders(t *testing.T) {
	registry := DefaultRegistry()

	openrouter, ok := registry.GetProvider(constants.ModelProviderOpenRouter)
	require.True(t, ok)
	require.Equal(t, APIOpenAIResponses, openrouter.DefaultAPI)
	require.Equal(t, constants.DefaultOpenRouterResponsesBaseURL, registry.GetBaseURL("openrouter", ""))
	require.Equal(t, constants.DefaultOpenRouterBaseURL, registry.GetBaseURL("openrouter", APIOpenAICompletions))
	require.Equal(t, constants.DefaultOpenRouterResponsesBaseURL, registry.GetBaseURL("openrouter", APIOpenAIResponses))
	require.Equal(t, constants.DefaultOpenRouterEmbeddingsBaseURL, registry.GetBaseURL("openrouter", APIOpenRouterEmbeddings))
	require.Equal(t, []string{"OPENROUTER_API_KEY"}, openrouter.APIKeyEnv)

	openai, ok := registry.GetProvider(constants.ModelProviderOpenAI)
	require.True(t, ok)
	require.Equal(t, APIOpenAIResponses, openai.DefaultAPI)
	require.True(t, openai.HasDisplayIndex)
	require.Equal(t, 0, openai.DisplayIndex)
	require.Equal(t, constants.DefaultOpenAIBaseURL, registry.GetBaseURL("openai", ""))
	require.Equal(t, constants.DefaultOpenAIBaseURL, registry.GetBaseURL("openai", APIOpenAICompletions))
	require.Equal(t, constants.DefaultOpenAIBaseURL, registry.GetBaseURL("openai", APIOpenAIResponses))
	require.Equal(t, constants.DefaultOpenAIEmbeddingsBaseURL, registry.GetBaseURL("openai", APIOpenAIEmbeddings))
	require.Equal(t, []string{"OPENAI_API_KEY"}, openai.APIKeyEnv)
	require.False(t, openai.SupportsOAuth)

	openaiCodex, ok := registry.GetProvider(constants.ModelProviderOpenAICodex)
	require.True(t, ok)
	require.Equal(t, APIOpenAIResponses, openaiCodex.DefaultAPI)
	require.True(t, openaiCodex.HasDisplayIndex)
	require.Equal(t, 0, openaiCodex.DisplayIndex)
	require.Equal(t, constants.DefaultOpenAISubscriptionBaseURL, registry.GetBaseURL("openai-codex", ""))
	require.Equal(t, constants.DefaultOpenAISubscriptionBaseURL, registry.GetBaseURL("openai-codex", APIOpenAIResponses))
	require.Empty(t, openaiCodex.APIKeyEnv)
	require.True(t, openaiCodex.SupportsOAuth)

	anthropic, ok := registry.GetProvider(constants.ModelProviderAnthropic)
	require.True(t, ok)
	require.Equal(t, APIAnthropicMessages, anthropic.DefaultAPI)
	require.True(t, anthropic.HasDisplayIndex)
	require.Equal(t, 1, anthropic.DisplayIndex)
	require.Equal(t, constants.DefaultAnthropicBaseURL, registry.GetBaseURL("anthropic", ""))
	require.Equal(t, constants.DefaultAnthropicBaseURL, registry.GetBaseURL("anthropic", APIAnthropicMessages))
	require.Equal(t, []string{"ANTHROPIC_API_KEY"}, anthropic.APIKeyEnv)
	require.True(t, anthropic.SupportsOAuth)

	copilot, ok := registry.GetProvider(constants.ModelProviderGitHubCopilot)
	require.True(t, ok)
	require.Equal(t, APIOpenAIResponses, copilot.DefaultAPI)
	require.True(t, copilot.HasDisplayIndex)
	require.Equal(t, 2, copilot.DisplayIndex)
	require.Equal(t, constants.DefaultGitHubCopilotBaseURL, registry.GetBaseURL("github-copilot", ""))
	require.Equal(t, constants.DefaultGitHubCopilotBaseURL, registry.GetBaseURL("github-copilot", APIOpenAICompletions))
	require.Equal(t, constants.DefaultGitHubCopilotBaseURL, registry.GetBaseURL("github-copilot", APIOpenAIResponses))
	require.Equal(t, constants.DefaultGitHubCopilotBaseURL, registry.GetBaseURL("github-copilot", APIAnthropicMessages))
	require.Equal(t, []string{"COPILOT_GITHUB_TOKEN"}, copilot.APIKeyEnv)
	require.True(t, copilot.SupportsOAuth)

	ollama, ok := registry.GetProvider(constants.ModelProviderOllama)
	require.True(t, ok)
	require.Equal(t, APIOllamaNative, ollama.DefaultAPI)
	require.True(t, ollama.HasDisplayIndex)
	require.Equal(t, 4, ollama.DisplayIndex)
	require.Equal(t, constants.DefaultOllamaBaseURL, registry.GetBaseURL("ollama", ""))
	require.Equal(t, constants.DefaultOllamaBaseURL+"/v1", registry.GetBaseURL("ollama", APIOpenAICompletions))
	require.Equal(t, constants.DefaultOllamaBaseURL, registry.GetBaseURL("ollama", APIOllamaNative))
	require.Equal(t, constants.DefaultOllamaBaseURL, registry.GetBaseURL("ollama", APIOllamaEmbeddings))
	require.NotNil(t, ollama.Local)
	require.Equal(t, constants.OllamaLocalAuthMarker, ollama.Local.AuthMarker)
	require.Equal(t, APIOllamaNative, ollama.Local.NativeChatAPI)
	require.Equal(t, APIOllamaEmbeddings, ollama.Local.EmbeddingsAPI)
	require.Equal(t, []string{APIOpenAICompletions}, ollama.Local.OpenAICompatibleChatAPIs)
	require.True(t, ollama.Local.Capabilities.Tools)
	require.True(t, ollama.Local.Capabilities.Vision)

	require.ElementsMatch(t, []string{
		constants.ModelProviderAnthropic,
		constants.ModelProviderGitHubCopilot,
		constants.ModelProviderOllama,
		constants.ModelProviderOpenAI,
		constants.ModelProviderOpenAICodex,
		constants.ModelProviderOpenRouter,
	}, registry.GetProviderIDs())
}

func TestDefaultRegistry_RegistersBuiltInModelsByProvider(t *testing.T) {
	registry := DefaultRegistry()

	openAIModel, ok := registry.GetModel("openai", constants.DefaultModel)
	require.True(t, ok)
	require.Equal(t, constants.ModelProviderOpenAI, openAIModel.Owner)
	require.Equal(t, APIOpenAIResponses, openAIModel.API)
	require.Equal(t, []InputKind{InputText, InputImage}, openAIModel.Input)
	require.Equal(t, constants.DefaultContextLength, openAIModel.ContextWindow)
	require.False(t, openAIModel.SupportsOAuth)
	openAIDefaultModel, ok := registry.GetModel("openai", "gpt-5.5")
	require.True(t, ok)
	require.True(t, openAIDefaultModel.DisplayDefault)

	openAICodexModel, ok := registry.GetModel("openai-codex", "gpt-5.4")
	require.True(t, ok)
	require.Equal(t, constants.ModelProviderOpenAI, openAICodexModel.Owner)
	require.Equal(t, constants.ModelProviderOpenAICodex, openAICodexModel.Provider)
	require.Equal(t, APIOpenAIResponses, openAICodexModel.API)
	require.True(t, openAICodexModel.Reasoning)
	require.True(t, openAICodexModel.SupportsOAuth)
	require.Equal(t, 272000, openAICodexModel.ContextWindow)
	require.Equal(t, 128000, openAICodexModel.MaxTokens)

	for _, modelID := range []string{
		"gpt-5.3-codex-spark",
		"gpt-5.4",
		"gpt-5.4-mini",
		"gpt-5.5",
	} {
		model, ok := registry.GetModel("openai-codex", modelID)
		require.True(t, ok)
		require.True(t, model.SupportsOAuth)
	}
	openAICodexDefaultModel, ok := registry.GetModel("openai-codex", "gpt-5.5")
	require.True(t, ok)
	require.True(t, openAICodexDefaultModel.DisplayDefault)
	for _, modelID := range []string{"gpt-5.2", "gpt-5.2-codex", "gpt-5.3-codex"} {
		_, ok = registry.GetModel("openai-codex", modelID)
		require.False(t, ok, modelID)
	}

	ollamaModel, ok := registry.GetModel(constants.ModelProviderOllama, constants.DefaultOllamaModel)
	require.True(t, ok)
	require.Equal(t, constants.ModelProviderOllama, ollamaModel.Owner)
	require.Equal(t, APIOllamaNative, ollamaModel.API)
	require.Equal(t, []InputKind{InputText}, ollamaModel.Input)
	require.True(t, ollamaModel.SupportsTools)
	require.True(t, ollamaModel.DisplayDefault)
	require.Equal(t, 131072, ollamaModel.ContextWindow)
	ollamaEmbedding, ok := registry.GetModel(constants.ModelProviderOllama, constants.DefaultOllamaEmbeddingModel)
	require.True(t, ok)
	require.Equal(t, constants.ModelProviderOllama, ollamaEmbedding.Owner)
	require.Equal(t, APIOllamaEmbeddings, ollamaEmbedding.API)
	require.Equal(t, []InputKind{InputText}, ollamaEmbedding.Input)
	require.Equal(t, 8192, ollamaEmbedding.ContextWindow)
	for _, modelID := range []string{
		"llama3.2:3b",
		"llama3.1:8b",
		"llama3.3:70b",
		"qwen2.5-coder:7b",
		"mistral:latest",
	} {
		model, ok := registry.GetModel(constants.ModelProviderOllama, modelID)
		require.True(t, ok, modelID)
		require.Equal(t, constants.ModelProviderOllama, model.Provider)
		require.Equal(t, APIOllamaNative, model.API)
		require.True(t, model.SupportsTools)
	}
	for _, modelID := range []string{
		"qwen3.5:latest",
		"qwen3.6:latest",
		"deepseek-r1:8b",
		"phi4-mini:latest",
		"phi4-mini-reasoning:latest",
		"lfm2.5-thinking:latest",
	} {
		model, ok := registry.GetModel(constants.ModelProviderOllama, modelID)
		require.True(t, ok, modelID)
		require.True(t, model.Reasoning)
		require.Equal(t, APIOllamaNative, model.API)
	}

	copilotResponsesModel, ok := registry.GetModel("github-copilot", "gpt-5.4-mini")
	require.True(t, ok)
	require.Equal(t, constants.ModelProviderGitHubCopilot, copilotResponsesModel.Owner)
	require.Equal(t, APIOpenAIResponses, copilotResponsesModel.API)
	require.True(t, copilotResponsesModel.SupportsOAuth)
	require.True(t, copilotResponsesModel.Reasoning)
	require.Equal(t, []InputKind{InputText, InputImage}, copilotResponsesModel.Input)
	copilotDefaultModel, ok := registry.GetModel("github-copilot", "gpt-5.5")
	require.True(t, ok)
	require.True(t, copilotDefaultModel.DisplayDefault)

	copilotCompletionModel, ok := registry.GetModel("github-copilot", "gpt-4o")
	require.True(t, ok)
	require.Equal(t, APIOpenAICompletions, copilotCompletionModel.API)
	require.True(t, copilotCompletionModel.SupportsOAuth)

	copilotAnthropicModel, ok := registry.GetModel("github-copilot", "claude-sonnet-4.5")
	require.True(t, ok)
	require.Equal(t, APIAnthropicMessages, copilotAnthropicModel.API)
	require.True(t, copilotAnthropicModel.SupportsOAuth)

	copilotAnthropicModel, ok = registry.GetModel("github-copilot", "claude-sonnet-4-5")
	require.True(t, ok)
	require.Equal(t, APIAnthropicMessages, copilotAnthropicModel.API)
	require.True(t, copilotAnthropicModel.SupportsOAuth)

	legacyAnthropicModel, ok := registry.GetModel("anthropic", "claude-3-5-sonnet-20241022")
	require.True(t, ok)
	require.Equal(t, APIAnthropicMessages, legacyAnthropicModel.API)
	require.False(t, legacyAnthropicModel.SupportsOAuth)

	anthropicOAuthModel, ok := registry.GetModel("anthropic", "claude-sonnet-4-6")
	require.True(t, ok)
	require.Equal(t, APIAnthropicMessages, anthropicOAuthModel.API)
	require.True(t, anthropicOAuthModel.SupportsOAuth)
	require.True(t, anthropicOAuthModel.DisplayDefault)

	for modelID, api := range map[string]string{
		"claude-haiku-4.5":       APIAnthropicMessages,
		"claude-opus-4.5":        APIAnthropicMessages,
		"claude-opus-4.6":        APIAnthropicMessages,
		"claude-opus-4.7":        APIAnthropicMessages,
		"claude-sonnet-4.5":      APIAnthropicMessages,
		"claude-sonnet-4.6":      APIAnthropicMessages,
		"gemini-2.5-pro":         APIOpenAICompletions,
		"gemini-3-flash-preview": APIOpenAICompletions,
		"gemini-3.1-pro-preview": APIOpenAICompletions,
		"gemini-3.5-flash":       APIOpenAICompletions,
		"gpt-4.1":                APIOpenAICompletions,
		"gpt-4o":                 APIOpenAICompletions,
		"gpt-5-mini":             APIOpenAIResponses,
		"gpt-5.2":                APIOpenAIResponses,
		"gpt-5.2-codex":          APIOpenAIResponses,
		"gpt-5.3-codex":          APIOpenAIResponses,
		"gpt-5.4":                APIOpenAIResponses,
		"gpt-5.4-mini":           APIOpenAIResponses,
		"gpt-5.5":                APIOpenAIResponses,
		"grok-code-fast-1":       APIOpenAICompletions,
	} {
		model, ok := registry.GetModel("github-copilot", modelID)
		require.True(t, ok, modelID)
		require.Equal(t, api, model.API, modelID)
		require.True(t, model.SupportsOAuth, modelID)
	}

	for _, modelID := range []string{
		"gpt-4",
		"gpt-4-turbo",
		"gpt-4.1",
		"gpt-4.1-mini",
		"gpt-4.1-nano",
		"gpt-4o",
		"gpt-4o-2024-05-13",
		"gpt-4o-2024-08-06",
		"gpt-4o-2024-11-20",
		"gpt-5",
		"gpt-5-chat-latest",
		"gpt-5-codex",
		"gpt-5-mini",
		"gpt-5-nano",
		"gpt-5-pro",
		"gpt-5.1",
		"gpt-5.1-chat-latest",
		"gpt-5.1-codex",
		"gpt-5.1-codex-max",
		"gpt-5.1-codex-mini",
		"gpt-5.2",
		"gpt-5.2-chat-latest",
		"gpt-5.2-codex",
		"gpt-5.2-pro",
		"gpt-5.3-chat-latest",
		"gpt-5.3-codex",
		"gpt-5.3-codex-spark",
		"gpt-5.4",
		"gpt-5.4-mini",
		"gpt-5.4-nano",
		"gpt-5.4-pro",
		"gpt-5.5",
		"gpt-5.5-pro",
		"o1",
		"o1-pro",
		"o3",
		"o3-deep-research",
		"o3-mini",
		"o3-pro",
		"o4-mini",
		"o4-mini-deep-research",
	} {
		model, ok := registry.GetModel("openai", modelID)
		require.True(t, ok)
		require.False(t, model.SupportsOAuth)
	}

	openRouterModel, ok := registry.GetModel("openrouter", constants.DefaultProfileModel)
	require.True(t, ok)
	require.Equal(t, "minimax", openRouterModel.Owner)
	require.Equal(t, APIOpenAIResponses, openRouterModel.API)
	require.Equal(t, []InputKind{InputText}, openRouterModel.Input)
	require.True(t, openRouterModel.DisplayDefault)

	openRouterOpenAIModel, ok := registry.GetModel("openrouter", "openai/"+constants.DefaultModel)
	require.True(t, ok)
	require.Equal(t, constants.ModelProviderOpenAI, openRouterOpenAIModel.Owner)
	require.Equal(t, APIOpenAIResponses, openRouterOpenAIModel.API)
	require.Equal(t, []InputKind{InputText, InputImage}, openRouterOpenAIModel.Input)

	_, ok = registry.GetModel("openrouter", constants.DefaultModel)
	require.False(t, ok)

	openRouterClaudeModel, ok := registry.GetModel("openrouter", "anthropic/claude-sonnet-4.5")
	require.True(t, ok)
	require.Equal(t, constants.ModelProviderAnthropic, openRouterClaudeModel.Owner)
	require.Equal(t, APIOpenAIResponses, openRouterClaudeModel.API)
	require.True(t, openRouterClaudeModel.Reasoning)
	require.Equal(t, 1000000, openRouterClaudeModel.ContextWindow)

	embedding, ok := registry.GetModel("openrouter", constants.DefaultProfileEmbeddingModel)
	require.True(t, ok)
	require.Equal(t, constants.ModelProviderOpenAI, embedding.Owner)
	require.Equal(t, APIOpenRouterEmbeddings, embedding.API)
	require.Equal(t, []InputKind{InputText}, embedding.Input)

	_, ok = registry.GetModel("openrouter", "openai/"+constants.DefaultProfileEmbeddingModel)
	require.False(t, ok)

	sonnet, ok := registry.GetModel("anthropic", "claude-sonnet-4-6")
	require.True(t, ok)
	require.Equal(t, "Claude Sonnet 4.6", sonnet.Name)
	require.Equal(t, APIAnthropicMessages, sonnet.API)
	require.Equal(t, []InputKind{InputText, InputImage}, sonnet.Input)
	require.True(t, sonnet.SupportsOAuth)
	require.Equal(t, 1000000, sonnet.ContextWindow)
	require.Equal(t, 64000, sonnet.MaxTokens)

	opus47, ok := registry.GetModel("anthropic", "claude-opus-4-7")
	require.True(t, ok)
	require.Equal(t, APIAnthropicMessages, opus47.API)
	require.True(t, opus47.Reasoning)
	require.Equal(t, 1000000, opus47.ContextWindow)

	opus, ok := registry.GetModel("anthropic", "claude-opus-4-1")
	require.True(t, ok)
	require.Equal(t, APIAnthropicMessages, opus.API)

	haiku, ok := registry.GetModel("anthropic", "claude-3-haiku-20240307")
	require.True(t, ok)
	require.Equal(t, APIAnthropicMessages, haiku.API)
}

func TestRegistry_GetModelForAPIUsesExactTransportIdentity(t *testing.T) {
	registry := NewRegistry(
		nil,
		[]ProviderDefinition{{ID: "example", DefaultAPI: "responses"}},
		[]ModelDefinition{
			{
				ID:        "shared-model",
				Provider:  " Example ",
				API:       " Responses ",
				Reasoning: true,
				ReasoningCapabilities: ReasoningCapability{
					Efforts:       []ReasoningEffort{"low", "high"},
					DefaultEffort: "low",
				},
			},
			{
				ID:       "shared-model",
				Provider: "example",
				API:      "completions",
			},
		},
	)

	responses, ok := registry.GetModelForAPI("EXAMPLE", "responses", "shared-model")
	require.True(t, ok)
	require.Equal(t, []ReasoningEffort{"low", "high"}, responses.ReasoningCapabilities.Efforts)

	completions, ok := registry.GetModelByKey(ModelKey{
		Provider: "example",
		API:      "completions",
		Model:    "shared-model",
	})
	require.True(t, ok)
	require.False(t, completions.Reasoning)
	require.Empty(t, completions.ReasoningCapabilities.Efforts)

	defaultModel, ok := registry.GetModel("example", "shared-model")
	require.True(t, ok)
	require.Equal(t, "responses", defaultModel.API)
}

func TestRegistry_ClonesReasoningCapabilityEfforts(t *testing.T) {
	efforts := []ReasoningEffort{"high", "low"}
	registry := NewRegistry(nil, nil, []ModelDefinition{{
		ID:        "reasoner",
		Provider:  "example",
		API:       "responses",
		Reasoning: true,
		ReasoningCapabilities: ReasoningCapability{
			Efforts:       efforts,
			DefaultEffort: "high",
		},
	}})
	efforts[0] = "changed"

	first, ok := registry.GetModelForAPI("example", "responses", "reasoner")
	require.True(t, ok)
	require.Equal(t, []ReasoningEffort{"high", "low"}, first.ReasoningCapabilities.Efforts)
	first.ReasoningCapabilities.Efforts[0] = "changed"

	second, ok := registry.GetModelForAPI("example", "responses", "reasoner")
	require.True(t, ok)
	require.Equal(t, []ReasoningEffort{"high", "low"}, second.ReasoningCapabilities.Efforts)
}

func TestNormalizeReasoningCapability_ValidatesMetadata(t *testing.T) {
	tests := []struct {
		name       string
		reasoning  bool
		capability ReasoningCapability
		want       string
	}{
		{
			name:      "non-reasoning efforts",
			reasoning: false,
			capability: ReasoningCapability{
				Efforts:       []ReasoningEffort{"low"},
				DefaultEffort: "low",
			},
			want: "non-reasoning models cannot advertise efforts or summaries",
		},
		{
			name:      "non-reasoning summary",
			reasoning: false,
			capability: ReasoningCapability{
				Summary: true,
			},
			want: "non-reasoning models cannot advertise efforts or summaries",
		},
		{
			name:      "blank effort",
			reasoning: true,
			capability: ReasoningCapability{
				Efforts:       []ReasoningEffort{" "},
				DefaultEffort: "low",
			},
			want: "effort values must not be blank",
		},
		{
			name:      "duplicate effort",
			reasoning: true,
			capability: ReasoningCapability{
				Efforts:       []ReasoningEffort{"low", "LOW"},
				DefaultEffort: "low",
			},
			want: `effort value "LOW" is duplicated`,
		},
		{
			name:      "reserved effort",
			reasoning: true,
			capability: ReasoningCapability{
				Efforts:       []ReasoningEffort{"reset"},
				DefaultEffort: "reset",
			},
			want: `effort value "reset" is reserved`,
		},
		{
			name:      "missing default",
			reasoning: true,
			capability: ReasoningCapability{
				Efforts: []ReasoningEffort{"low"},
			},
			want: "adjustable reasoning models require a default effort",
		},
		{
			name:      "unsupported default",
			reasoning: true,
			capability: ReasoningCapability{
				Efforts:       []ReasoningEffort{"low"},
				DefaultEffort: "high",
			},
			want: `default effort "high" is not supported`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NormalizeReasoningCapability(tt.reasoning, tt.capability)
			require.EqualError(t, err, tt.want)
		})
	}
}

func TestDefaultRegistry_ReasoningMetadataIsTransportSpecific(t *testing.T) {
	registry := DefaultRegistry()

	openAI, ok := registry.GetModelForAPI(
		constants.ModelProviderOpenAI,
		APIOpenAIResponses,
		"gpt-5.5",
	)
	require.True(t, ok)
	require.Equal(
		t,
		[]ReasoningEffort{"none", "low", "medium", "high", "xhigh"},
		openAI.ReasoningCapabilities.Efforts,
	)
	require.Equal(t, ReasoningEffort("medium"), openAI.ReasoningCapabilities.DefaultEffort)
	require.True(t, openAI.ReasoningCapabilities.Summary)

	openAICodex, ok := registry.GetModelForAPI(
		constants.ModelProviderOpenAICodex,
		APIOpenAIResponses,
		"gpt-5.5",
	)
	require.True(t, ok)
	require.Equal(t, openAI.ReasoningCapabilities, openAICodex.ReasoningCapabilities)

	openRouter, ok := registry.GetModelForAPI(
		constants.ModelProviderOpenRouter,
		APIOpenAIResponses,
		"openai/gpt-5.5",
	)
	require.True(t, ok)
	require.True(t, openRouter.Reasoning)
	require.Equal(
		t,
		[]ReasoningEffort{"xhigh", "high", "medium", "low", "none"},
		openRouter.ReasoningCapabilities.Efforts,
	)
	require.Equal(t, ReasoningEffort("medium"), openRouter.ReasoningCapabilities.DefaultEffort)
	require.True(t, openRouter.ReasoningCapabilities.Summary)

	openRouterPro, ok := registry.GetModelForAPI(
		constants.ModelProviderOpenRouter,
		APIOpenAIResponses,
		"openai/gpt-5.5-pro",
	)
	require.True(t, ok)
	require.Equal(
		t,
		[]ReasoningEffort{"xhigh", "high", "medium"},
		openRouterPro.ReasoningCapabilities.Efforts,
	)
	require.Equal(t, ReasoningEffort("medium"), openRouterPro.ReasoningCapabilities.DefaultEffort)

	openRouterUnknown, ok := registry.GetModelForAPI(
		constants.ModelProviderOpenRouter,
		APIOpenAIResponses,
		"openrouter/auto",
	)
	require.True(t, ok)
	require.Empty(t, openRouterUnknown.ReasoningCapabilities.Efforts)

	copilot, ok := registry.GetModelForAPI(
		constants.ModelProviderGitHubCopilot,
		APIOpenAIResponses,
		"gpt-5.5",
	)
	require.True(t, ok)
	require.Empty(t, copilot.ReasoningCapabilities.Efforts)
	require.False(t, copilot.ReasoningCapabilities.Summary)

	ollama, ok := registry.GetModelForAPI(
		constants.ModelProviderOllama,
		APIOllamaNative,
		"qwen3.5:latest",
	)
	require.True(t, ok)
	require.True(t, ollama.Reasoning)
	require.Empty(t, ollama.ReasoningCapabilities.Efforts)
}

func TestRegistry_GetModelsReturnsClonedProviderModels(t *testing.T) {
	registry := DefaultRegistry()

	models := registry.GetModels(constants.ModelProviderOpenAI)
	require.NotEmpty(t, models)

	models[0].Input = append(models[0].Input, InputKind("mutated"))
	fresh := registry.GetModels(constants.ModelProviderOpenAI)
	require.NotContains(t, fresh[0].Input, InputKind("mutated"))

	require.Empty(t, registry.GetModels("missing"))
	require.Empty(t, (*Registry)(nil).GetModels(constants.ModelProviderOpenAI))
}

func TestRegistry_GetProvidersReturnsClonedProviders(t *testing.T) {
	registry := NewRegistry(
		[]APIDefinition{{ID: APIOllamaNative}},
		[]ProviderDefinition{{
			ID:             constants.ModelProviderOllama,
			DefaultAPI:     APIOllamaNative,
			SupportsModels: true,
			Local: &LocalProviderDefinition{
				NativeChatAPI: APIOllamaNative,
				AuthMarker:    constants.OllamaLocalAuthMarker,
			},
		}, {
			ID:             "blank-local",
			DefaultAPI:     APIOllamaNative,
			SupportsModels: true,
			Local: &LocalProviderDefinition{
				OpenAICompatibleChatAPIs: []string{" "},
			},
		}},
		nil,
	)

	providers := registry.GetProviders()
	require.Len(t, providers, 2)
	byID := map[string]ProviderDefinition{}
	for _, provider := range providers {
		byID[provider.ID] = provider
	}

	require.NotNil(t, byID[constants.ModelProviderOllama].Local)
	require.Equal(t, APIOllamaNative, byID[constants.ModelProviderOllama].Local.NativeChatAPI)
	require.Empty(t, byID["blank-local"].Local.OpenAICompatibleChatAPIs)

	byID[constants.ModelProviderOllama].Local.NativeChatAPI = APIOpenAICompletions
	fresh := registry.GetProviders()
	for _, provider := range fresh {
		byID[provider.ID] = provider
	}
	require.Equal(t, APIOllamaNative, byID[constants.ModelProviderOllama].Local.NativeChatAPI)
	require.Empty(t, byID[constants.ModelProviderOllama].Local.OpenAICompatibleChatAPIs)

	require.Empty(t, (*Registry)(nil).GetProviders())
}

func TestRegistry_ReturnsCopies(t *testing.T) {
	registry := DefaultRegistry()

	provider, ok := registry.GetProvider("openrouter")
	require.True(t, ok)
	provider.BaseURLs[APIOpenAICompletions] = "changed"
	provider.APIKeyEnv[0] = "CHANGED"

	provider, ok = registry.GetProvider("openrouter")
	require.True(t, ok)
	require.Equal(t, constants.DefaultOpenRouterBaseURL, provider.BaseURLs[APIOpenAICompletions])
	require.Nil(t, provider.Headers)
	require.Equal(t, []string{"OPENROUTER_API_KEY"}, provider.APIKeyEnv)
	require.Nil(t, provider.Local)

	model, ok := registry.GetModel("openai", constants.DefaultModel)
	require.True(t, ok)
	model.Input[0] = InputImage

	model, ok = registry.GetModel("openai", constants.DefaultModel)
	require.True(t, ok)
	require.Equal(t, []InputKind{InputText, InputImage}, model.Input)
}

func TestRegistry_NilReceiverLookupsReturnFalse(t *testing.T) {
	var registry *Registry

	_, ok := registry.GetAPI(APIOpenAICompletions)
	require.False(t, ok)
	_, ok = registry.GetProvider(constants.ModelProviderOpenAI)
	require.False(t, ok)
	_, ok = registry.GetModel(constants.ModelProviderOpenAI, constants.DefaultModel)
	require.False(t, ok)
	require.Nil(t, registry.GetProviderIDs())
	require.Nil(t, registry.GetAPIIDs())
	require.Empty(t, registry.GetBaseURL(constants.ModelProviderOpenAI, APIOpenAIResponses))
}

func TestRegistry_MissingLookupsReturnFalse(t *testing.T) {
	registry := DefaultRegistry()

	_, ok := registry.GetAPI("missing")
	require.False(t, ok)
	_, ok = registry.GetProvider("missing")
	require.False(t, ok)
	_, ok = registry.GetModel("missing", constants.DefaultModel)
	require.False(t, ok)
	_, ok = registry.GetModel(constants.ModelProviderOpenAI, "missing")
	require.False(t, ok)
	require.Empty(t, registry.GetBaseURL("missing", APIOpenAIResponses))
	require.Empty(t, registry.GetBaseURL(constants.ModelProviderOpenAI, "missing"))
	require.False(t, registry.SupportsProviderAPI("missing", APIOpenAIResponses))
	require.False(t, registry.SupportsProviderAPI(constants.ModelProviderOpenAI, "missing"))
}

func TestRegistry_SupportsProviderAPI(t *testing.T) {
	registry := DefaultRegistry()

	require.True(t, registry.SupportsProviderAPI(constants.ModelProviderOpenRouter, ""))
	require.True(t, registry.SupportsProviderAPI(constants.ModelProviderOpenRouter, APIOpenAICompletions))
	require.True(t, registry.SupportsProviderAPI(constants.ModelProviderOpenRouter, APIOpenRouterEmbeddings))
	require.True(t, registry.SupportsProviderAPI(constants.ModelProviderOpenAI, APIOpenAIEmbeddings))
	require.True(t, registry.SupportsProviderAPI(constants.ModelProviderAnthropic, APIAnthropicMessages))
	require.False(t, registry.SupportsProviderAPI(constants.ModelProviderAnthropic, APIOpenAIResponses))
}

func TestRegistry_NormalizesDefinitionsAndSkipsIncompleteEntries(t *testing.T) {
	registry := NewRegistry(
		[]APIDefinition{
			{ID: " Custom-API "},
			{ID: "   "},
		},
		[]ProviderDefinition{
			{
				ID:         " Custom ",
				DefaultAPI: " Custom-API ",
				BaseURLs: map[string]string{
					" Custom-API ": " https://custom.example/v1 ",
					"   ":          "ignored",
					" blank ":      " ",
				},
				Headers: map[string]string{
					" X-Custom ": " value ",
					" ignored ":  " ",
				},
				Local: &LocalProviderDefinition{
					NativeChatAPI: " OLLAMA-NATIVE ",
					OpenAICompatibleChatAPIs: []string{
						" openai-completions ",
						"OPENAI-COMPLETIONS",
						"openai-responses",
					},
					EmbeddingsAPI: APIOllamaEmbeddings,
					AuthMarker:    " local-marker ",
					Capabilities:  CapabilitySet{Tools: true, Vision: true, Reasoning: true},
				},
			},
			{ID: "empty"},
			{
				ID: "blank-maps",
				BaseURLs: map[string]string{
					"blank": " ",
				},
				Headers: map[string]string{
					"blank": " ",
				},
			},
			{ID: "   "},
		},
		[]ModelDefinition{
			{ID: " model-one ", Provider: " Custom ", API: " Custom-API ", Input: []InputKind{InputText}},
			{ID: "", Provider: "custom", API: "custom-api"},
			{ID: "missing-provider"},
		},
	)

	api, ok := registry.GetAPI("custom-api")
	require.True(t, ok)
	require.Equal(t, "custom-api", api.ID)

	provider, ok := registry.GetProvider("custom")
	require.True(t, ok)
	require.Equal(t, "custom-api", provider.DefaultAPI)
	require.Equal(t, "https://custom.example/v1", provider.BaseURLs["custom-api"])
	require.NotContains(t, provider.BaseURLs, "")
	require.NotContains(t, provider.BaseURLs, "blank")
	require.Equal(t, "value", provider.Headers["x-custom"])
	require.NotContains(t, provider.Headers, "ignored")
	require.Equal(t, &LocalProviderDefinition{
		NativeChatAPI:            APIOllamaNative,
		OpenAICompatibleChatAPIs: []string{APIOpenAICompletions, APIOpenAIResponses},
		EmbeddingsAPI:            APIOllamaEmbeddings,
		AuthMarker:               "local-marker",
		Capabilities:             CapabilitySet{Tools: true, Vision: true, Reasoning: true},
	}, provider.Local)
	require.Equal(t, "https://custom.example/v1", registry.GetBaseURL("custom", ""))
	emptyProvider, ok := registry.GetProvider("empty")
	require.True(t, ok)
	require.Nil(t, emptyProvider.BaseURLs)
	blankMapsProvider, ok := registry.GetProvider("blank-maps")
	require.True(t, ok)
	require.Nil(t, blankMapsProvider.BaseURLs)
	require.Nil(t, blankMapsProvider.Headers)
	require.False(t, registry.SupportsProviderAPI("empty", ""))

	model, ok := registry.GetModel("custom", "model-one")
	require.True(t, ok)
	require.Equal(t, "custom-api", model.API)
	require.Equal(t, []InputKind{InputText}, model.Input)
	_, ok = registry.GetModel("custom", "")
	require.False(t, ok)
}
