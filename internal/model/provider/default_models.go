package provider

import (
	"strings"

	"github.com/wandxy/morph/internal/constants"
	"github.com/wandxy/morph/pkg/str"
)

func defaultModels() []ModelDefinition {
	return []ModelDefinition{

		// OpenAI models
		openAIModel("gpt-4", "GPT-4", []InputKind{InputText}, 8192, 8192),
		openAIModel("gpt-4-turbo", "GPT-4 Turbo", []InputKind{InputText, InputImage}, 128000, 4096),
		openAIModel("gpt-4.1", "GPT-4.1", []InputKind{InputText, InputImage}, 1047576, 32768),
		openAIModel("gpt-4.1-mini", "GPT-4.1 mini", []InputKind{InputText, InputImage}, 1047576, 32768),
		openAIModel("gpt-4.1-nano", "GPT-4.1 nano", []InputKind{InputText, InputImage}, 1047576, 32768),
		openAIModel("gpt-4o", "GPT-4o", []InputKind{InputText, InputImage}, 128000, 16384),
		openAIModel("gpt-4o-2024-05-13", "GPT-4o 2024-05-13", []InputKind{InputText, InputImage}, 128000, 4096),
		openAIModel("gpt-4o-2024-08-06", "GPT-4o 2024-08-06", []InputKind{InputText, InputImage}, 128000, 16384),
		openAIModel("gpt-4o-2024-11-20", "GPT-4o 2024-11-20", []InputKind{InputText, InputImage}, 128000, 16384),
		openAIModel("gpt-4o-mini", "GPT-4o mini", []InputKind{InputText, InputImage}, 128000, 16384),
		openAIReasoningModel("gpt-5", "GPT-5", []InputKind{InputText, InputImage}, 400000, 128000),
		openAIModel("gpt-5-chat-latest", "GPT-5 Chat Latest", []InputKind{InputText, InputImage}, 128000, 16384),
		openAIReasoningModel("gpt-5-codex", "GPT-5 Codex", []InputKind{InputText, InputImage}, 400000, 128000),
		openAIReasoningModel("gpt-5-mini", "GPT-5 mini", []InputKind{InputText, InputImage}, 400000, 128000),
		openAIReasoningModel("gpt-5-nano", "GPT-5 nano", []InputKind{InputText, InputImage}, 400000, 128000),
		openAIReasoningModel("gpt-5-pro", "GPT-5 pro", []InputKind{InputText, InputImage}, 400000, 272000),
		openAIReasoningModel("gpt-5.1", "GPT-5.1", []InputKind{InputText, InputImage}, 400000, 128000),
		openAIReasoningModel("gpt-5.1-chat-latest", "GPT-5.1 Chat Latest", []InputKind{InputText, InputImage}, 128000, 16384),
		openAIReasoningModel("gpt-5.1-codex", "GPT-5.1 Codex", []InputKind{InputText, InputImage}, 400000, 128000),
		openAIReasoningModel("gpt-5.1-codex-max", "GPT-5.1 Codex Max", []InputKind{InputText, InputImage}, 400000, 128000),
		openAIReasoningModel("gpt-5.1-codex-mini", "GPT-5.1 Codex Mini", []InputKind{InputText, InputImage}, 400000, 128000),
		openAIReasoningModel("gpt-5.2", "GPT-5.2", []InputKind{InputText, InputImage}, 272000, 128000),
		openAIReasoningModel("gpt-5.2-chat-latest", "GPT-5.2 Chat Latest", []InputKind{InputText, InputImage}, 128000, 16384),
		openAIReasoningModel("gpt-5.2-codex", "GPT-5.2 Codex", []InputKind{InputText, InputImage}, 400000, 128000),
		openAIReasoningModel("gpt-5.2-pro", "GPT-5.2 pro", []InputKind{InputText, InputImage}, 400000, 128000),
		openAIModel("gpt-5.3-chat-latest", "GPT-5.3 Chat Latest", []InputKind{InputText, InputImage}, 128000, 16384),
		openAIReasoningModel("gpt-5.3-codex", "GPT-5.3 Codex", []InputKind{InputText, InputImage}, 272000, 128000),
		openAIReasoningModel("gpt-5.3-codex-spark", "GPT-5.3 Codex Spark", []InputKind{InputText}, 272000, 128000),
		openAIReasoningModel("gpt-5.4", "GPT-5.4", []InputKind{InputText, InputImage}, 272000, 128000),
		openAIReasoningModel("gpt-5.4-mini", "GPT-5.4 mini", []InputKind{InputText, InputImage}, 272000, 128000),
		openAIReasoningModel("gpt-5.4-nano", "GPT-5.4 nano", []InputKind{InputText, InputImage}, 400000, 128000),
		openAIReasoningModel("gpt-5.4-pro", "GPT-5.4 pro", []InputKind{InputText, InputImage}, 1050000, 128000),
		displayDefaultModel(openAIReasoningModel("gpt-5.5", "GPT-5.5", []InputKind{InputText, InputImage}, 272000, 128000)),
		openAIReasoningModel("gpt-5.5-pro", "GPT-5.5 pro", []InputKind{InputText, InputImage}, 1050000, 128000),
		openAIReasoningModel("o1", "o1", []InputKind{InputText, InputImage}, 200000, 100000),
		openAIReasoningModel("o1-pro", "o1 pro", []InputKind{InputText, InputImage}, 200000, 100000),
		openAIReasoningModel("o3", "o3", []InputKind{InputText, InputImage}, 200000, 100000),
		openAIReasoningModel("o3-deep-research", "o3 deep research", []InputKind{InputText, InputImage}, 200000, 100000),
		openAIReasoningModel("o3-mini", "o3 mini", []InputKind{InputText}, 200000, 100000),
		openAIReasoningModel("o3-pro", "o3 pro", []InputKind{InputText, InputImage}, 200000, 100000),
		openAIReasoningModel("o4-mini", "o4 mini", []InputKind{InputText, InputImage}, 200000, 100000),
		openAIReasoningModel("o4-mini-deep-research", "o4 mini deep research", []InputKind{InputText, InputImage}, 200000, 100000),
		{
			ID:            constants.DefaultProfileEmbeddingModel,
			Name:          "Text Embedding 3 Small",
			Owner:         constants.ModelProviderOpenAI,
			Provider:      constants.ModelProviderOpenAI,
			API:           APIOpenAIEmbeddings,
			Input:         []InputKind{InputText},
			ContextWindow: 8191,
		},

		// OpenAI Codex OAuth models
		openAICodexModel("gpt-5.3-codex-spark", "GPT-5.3 Codex Spark", []InputKind{InputText}, 272000, 128000),
		openAICodexModel("gpt-5.4", "GPT-5.4", []InputKind{InputText, InputImage}, 272000, 128000),
		openAICodexModel("gpt-5.4-mini", "GPT-5.4 mini", []InputKind{InputText, InputImage}, 272000, 128000),
		displayDefaultModel(openAICodexModel("gpt-5.5", "GPT-5.5", []InputKind{InputText, InputImage}, 272000, 128000)),

		// Ollama local models
		displayDefaultModel(ollamaModel(constants.DefaultOllamaModel, "Gemma 4 8B", []InputKind{InputText}, 131072, 0)),
		ollamaModel("llama3.2:3b", "Llama 3.2 3B", []InputKind{InputText}, 131072, 0),
		ollamaModel("llama3.1:8b", "Llama 3.1 8B", []InputKind{InputText}, 131072, 0),
		ollamaModel("llama3.3:70b", "Llama 3.3 70B", []InputKind{InputText}, 131072, 0),
		ollamaReasoningModel("qwen3.5:latest", "Qwen 3.5", []InputKind{InputText, InputImage}, 262144, 0),
		ollamaReasoningModel("qwen3.6:latest", "Qwen 3.6", []InputKind{InputText, InputImage}, 262144, 0),
		ollamaModel("qwen2.5-coder:7b", "Qwen 2.5 Coder 7B", []InputKind{InputText}, 32768, 0),
		ollamaReasoningModel("deepseek-r1:8b", "DeepSeek R1 8B", []InputKind{InputText}, 131072, 0),
		ollamaModel("mistral:latest", "Mistral 7B", []InputKind{InputText}, 32768, 0),
		ollamaReasoningModel("phi4-mini:latest", "Phi 4 Mini", []InputKind{InputText}, 131072, 0),
		ollamaReasoningModel("phi4-mini-reasoning:latest", "Phi 4 Mini Reasoning", []InputKind{InputText}, 131072, 0),
		ollamaReasoningModel("lfm2.5-thinking:latest", "LFM 2.5 Thinking", []InputKind{InputText}, 131072, 0),
		ollamaEmbeddingModel(constants.DefaultOllamaEmbeddingModel, "Nomic Embed Text", 8192),

		// GitHub Copilot models
		gitHubCopilotAnthropicModel("claude-haiku-4.5", "Claude Haiku 4.5", []InputKind{InputText, InputImage}, 144000, 32000),
		gitHubCopilotAnthropicModel("claude-opus-4.5", "Claude Opus 4.5", []InputKind{InputText, InputImage}, 160000, 32000),
		gitHubCopilotAnthropicModel("claude-opus-4.6", "Claude Opus 4.6", []InputKind{InputText, InputImage}, 1000000, 64000),
		gitHubCopilotAnthropicModel("claude-opus-4.7", "Claude Opus 4.7", []InputKind{InputText, InputImage}, 144000, 64000),
		gitHubCopilotAnthropicModel("claude-sonnet-4.5", "Claude Sonnet 4.5", []InputKind{InputText, InputImage}, 144000, 32000),
		gitHubCopilotAnthropicModel("claude-sonnet-4.6", "Claude Sonnet 4.6", []InputKind{InputText, InputImage}, 1000000, 32000),
		gitHubCopilotAnthropicModel("claude-haiku-4-5", "Claude Haiku 4.5", []InputKind{InputText, InputImage}, 144000, 32000),
		gitHubCopilotAnthropicModel("claude-opus-4-5", "Claude Opus 4.5", []InputKind{InputText, InputImage}, 160000, 32000),
		gitHubCopilotAnthropicModel("claude-opus-4-6", "Claude Opus 4.6", []InputKind{InputText, InputImage}, 1000000, 64000),
		gitHubCopilotAnthropicModel("claude-opus-4-7", "Claude Opus 4.7", []InputKind{InputText, InputImage}, 144000, 64000),
		gitHubCopilotAnthropicModel("claude-sonnet-4-5", "Claude Sonnet 4.5", []InputKind{InputText, InputImage}, 144000, 32000),
		gitHubCopilotAnthropicModel("claude-sonnet-4-6", "Claude Sonnet 4.6", []InputKind{InputText, InputImage}, 1000000, 32000),
		gitHubCopilotCompletionModel("gemini-2.5-pro", "Gemini 2.5 Pro", []InputKind{InputText, InputImage}, false, 128000, 64000),
		gitHubCopilotCompletionModel("gemini-3-flash-preview", "Gemini 3 Flash", []InputKind{InputText, InputImage}, true, 128000, 64000),
		gitHubCopilotCompletionModel("gemini-3.1-pro-preview", "Gemini 3.1 Pro Preview", []InputKind{InputText, InputImage}, true, 128000, 64000),
		gitHubCopilotCompletionModel("gemini-3.5-flash", "Gemini 3.5 Flash", []InputKind{InputText, InputImage}, true, 128000, 64000),
		gitHubCopilotCompletionModel("gpt-4.1", "GPT-4.1", []InputKind{InputText, InputImage}, false, 128000, 16384),
		gitHubCopilotCompletionModel("gpt-4o", "GPT-4o", []InputKind{InputText, InputImage}, false, 128000, 4096),
		gitHubCopilotResponsesModel("gpt-5-mini", "GPT-5 mini", []InputKind{InputText, InputImage}, 264000, 64000),
		gitHubCopilotResponsesModel("gpt-5.2", "GPT-5.2", []InputKind{InputText, InputImage}, 264000, 64000),
		gitHubCopilotResponsesModel("gpt-5.2-codex", "GPT-5.2 Codex", []InputKind{InputText, InputImage}, 400000, 128000),
		gitHubCopilotResponsesModel("gpt-5.3-codex", "GPT-5.3 Codex", []InputKind{InputText, InputImage}, 400000, 128000),
		gitHubCopilotResponsesModel("gpt-5.4", "GPT-5.4", []InputKind{InputText, InputImage}, 400000, 128000),
		gitHubCopilotResponsesModel("gpt-5.4-mini", "GPT-5.4 Mini", []InputKind{InputText, InputImage}, 400000, 128000),
		displayDefaultModel(gitHubCopilotResponsesModel("gpt-5.5", "GPT-5.5", []InputKind{InputText, InputImage}, 400000, 128000)),
		gitHubCopilotCompletionModel("grok-code-fast-1", "Grok Code Fast 1", []InputKind{InputText}, true, 128000, 64000),

		// OpenRouter models
		openRouterModel("ai21/jamba-large-1.7", "AI21: Jamba Large 1.7", []InputKind{InputText}, 256000, 4096),
		openRouterReasoningModel("alibaba/tongyi-deepresearch-30b-a3b", "Tongyi DeepResearch 30B A3B", []InputKind{InputText}, 131072, 131072),
		openRouterReasoningModel("amazon/nova-2-lite-v1", "Amazon: Nova 2 Lite", []InputKind{InputText, InputImage}, 1000000, 65535),
		openRouterModel("amazon/nova-lite-v1", "Amazon: Nova Lite 1.0", []InputKind{InputText, InputImage}, 300000, 5120),
		openRouterModel("amazon/nova-micro-v1", "Amazon: Nova Micro 1.0", []InputKind{InputText}, 128000, 5120),
		openRouterModel("amazon/nova-premier-v1", "Amazon: Nova Premier 1.0", []InputKind{InputText, InputImage}, 1000000, 32000),
		openRouterModel("amazon/nova-pro-v1", "Amazon: Nova Pro 1.0", []InputKind{InputText, InputImage}, 300000, 5120),
		openRouterModel("anthropic/claude-3-haiku", "Anthropic: Claude 3 Haiku", []InputKind{InputText, InputImage}, 200000, 4096),
		openRouterModel("anthropic/claude-3.5-haiku", "Anthropic: Claude 3.5 Haiku", []InputKind{InputText, InputImage}, 200000, 8192),
		openRouterReasoningModel("anthropic/claude-haiku-4.5", "Anthropic: Claude Haiku 4.5", []InputKind{InputText, InputImage}, 200000, 64000),
		openRouterReasoningModel("anthropic/claude-opus-4", "Anthropic: Claude Opus 4", []InputKind{InputText, InputImage}, 200000, 32000),
		openRouterReasoningModel("anthropic/claude-opus-4.1", "Anthropic: Claude Opus 4.1", []InputKind{InputText, InputImage}, 200000, 32000),
		openRouterReasoningModel("anthropic/claude-opus-4.5", "Anthropic: Claude Opus 4.5", []InputKind{InputText, InputImage}, 200000, 64000),
		openRouterReasoningModel("anthropic/claude-opus-4.6", "Anthropic: Claude Opus 4.6", []InputKind{InputText, InputImage}, 1000000, 128000),
		openRouterReasoningModel("anthropic/claude-opus-4.6-fast", "Anthropic: Claude Opus 4.6 (Fast)", []InputKind{InputText, InputImage}, 1000000, 128000),
		openRouterReasoningModel("anthropic/claude-opus-4.7", "Anthropic: Claude Opus 4.7", []InputKind{InputText, InputImage}, 1000000, 128000),
		openRouterReasoningModel("anthropic/claude-opus-4.7-fast", "Anthropic: Claude Opus 4.7 (Fast)", []InputKind{InputText, InputImage}, 1000000, 128000),
		openRouterReasoningModel("anthropic/claude-sonnet-4", "Anthropic: Claude Sonnet 4", []InputKind{InputText, InputImage}, 1000000, 64000),
		openRouterReasoningModel("anthropic/claude-sonnet-4.5", "Anthropic: Claude Sonnet 4.5", []InputKind{InputText, InputImage}, 1000000, 64000),
		openRouterReasoningModel("anthropic/claude-sonnet-4.6", "Anthropic: Claude Sonnet 4.6", []InputKind{InputText, InputImage}, 1000000, 128000),
		openRouterReasoningModel("arcee-ai/trinity-large-thinking", "Arcee AI: Trinity Large Thinking", []InputKind{InputText}, 262144, 262144),
		openRouterReasoningModel("arcee-ai/trinity-large-thinking:free", "Arcee AI: Trinity Large Thinking (free)", []InputKind{InputText}, 262144, 80000),
		openRouterReasoningModel("arcee-ai/trinity-mini", "Arcee AI: Trinity Mini", []InputKind{InputText}, 131072, 131072),
		openRouterModel("arcee-ai/virtuoso-large", "Arcee AI: Virtuoso Large", []InputKind{InputText}, 131072, 64000),
		openRouterReasoningModel("auto", "Auto", []InputKind{InputText, InputImage}, 2000000, 30000),
		openRouterReasoningModel("baidu/cobuddy:free", "Baidu Qianfan: CoBuddy (free)", []InputKind{InputText}, 131072, 65536),
		openRouterModel("baidu/ernie-4.5-21b-a3b", "Baidu: ERNIE 4.5 21B A3B", []InputKind{InputText}, 131072, 8000),
		openRouterReasoningModel("baidu/ernie-4.5-vl-28b-a3b", "Baidu: ERNIE 4.5 VL 28B A3B", []InputKind{InputText, InputImage}, 131072, 8000),
		openRouterReasoningModel("bytedance-seed/seed-1.6", "ByteDance Seed: Seed 1.6", []InputKind{InputText, InputImage}, 262144, 32768),
		openRouterReasoningModel("bytedance-seed/seed-1.6-flash", "ByteDance Seed: Seed 1.6 Flash", []InputKind{InputText, InputImage}, 262144, 32768),
		openRouterReasoningModel("bytedance-seed/seed-2.0-lite", "ByteDance Seed: Seed-2.0-Lite", []InputKind{InputText, InputImage}, 262144, 131072),
		openRouterReasoningModel("bytedance-seed/seed-2.0-mini", "ByteDance Seed: Seed-2.0-Mini", []InputKind{InputText, InputImage}, 262144, 131072),
		openRouterModel("cohere/command-r-08-2024", "Cohere: Command R (08-2024)", []InputKind{InputText}, 128000, 4000),
		openRouterModel("cohere/command-r-plus-08-2024", "Cohere: Command R+ (08-2024)", []InputKind{InputText}, 128000, 4000),
		openRouterModel("deepseek/deepseek-chat", "DeepSeek: DeepSeek V3", []InputKind{InputText}, 163840, 16384),
		openRouterModel("deepseek/deepseek-chat-v3-0324", "DeepSeek: DeepSeek V3 0324", []InputKind{InputText}, 163840, 16384),
		openRouterReasoningModel("deepseek/deepseek-chat-v3.1", "DeepSeek: DeepSeek V3.1", []InputKind{InputText}, 163840, 32768),
		openRouterReasoningModel("deepseek/deepseek-r1", "DeepSeek: R1", []InputKind{InputText}, 163840, 16000),
		openRouterReasoningModel("deepseek/deepseek-r1-0528", "DeepSeek: R1 0528", []InputKind{InputText}, 163840, 32768),
		openRouterReasoningModel("deepseek/deepseek-v3.1-terminus", "DeepSeek: DeepSeek V3.1 Terminus", []InputKind{InputText}, 163840, 32768),
		openRouterReasoningModel("deepseek/deepseek-v3.2", "DeepSeek: DeepSeek V3.2", []InputKind{InputText}, 131072, 65536),
		openRouterReasoningModel("deepseek/deepseek-v3.2-exp", "DeepSeek: DeepSeek V3.2 Exp", []InputKind{InputText}, 163840, 65536),
		openRouterReasoningModel("deepseek/deepseek-v4-flash", "DeepSeek: DeepSeek V4 Flash", []InputKind{InputText}, 1048576, 16384),
		openRouterReasoningModel("deepseek/deepseek-v4-flash:free", "DeepSeek: DeepSeek V4 Flash (free)", []InputKind{InputText}, 1048576, 384000),
		openRouterReasoningModel("deepseek/deepseek-v4-pro", "DeepSeek: DeepSeek V4 Pro", []InputKind{InputText}, 1048576, 384000),
		openRouterModel("essentialai/rnj-1-instruct", "EssentialAI: Rnj 1 Instruct", []InputKind{InputText}, 32768, 4096),
		openRouterModel("google/gemini-2.0-flash-001", "Google: Gemini 2.0 Flash", []InputKind{InputText, InputImage}, 1000000, 8192),
		openRouterModel("google/gemini-2.0-flash-lite-001", "Google: Gemini 2.0 Flash Lite", []InputKind{InputText, InputImage}, 1048576, 8192),
		openRouterReasoningModel("google/gemini-2.5-flash", "Google: Gemini 2.5 Flash", []InputKind{InputText, InputImage}, 1048576, 65535),
		openRouterReasoningModel("google/gemini-2.5-flash-lite", "Google: Gemini 2.5 Flash Lite", []InputKind{InputText, InputImage}, 1048576, 65535),
		openRouterReasoningModel("google/gemini-2.5-flash-lite-preview-09-2025", "Google: Gemini 2.5 Flash Lite Preview 09-2025", []InputKind{InputText, InputImage}, 1048576, 65535),
		openRouterReasoningModel("google/gemini-2.5-pro", "Google: Gemini 2.5 Pro", []InputKind{InputText, InputImage}, 1048576, 65536),
		openRouterReasoningModel("google/gemini-2.5-pro-preview", "Google: Gemini 2.5 Pro Preview 06-05", []InputKind{InputText, InputImage}, 1048576, 65536),
		openRouterReasoningModel("google/gemini-2.5-pro-preview-05-06", "Google: Gemini 2.5 Pro Preview 05-06", []InputKind{InputText, InputImage}, 1048576, 65535),
		openRouterReasoningModel("google/gemini-3-flash-preview", "Google: Gemini 3 Flash Preview", []InputKind{InputText, InputImage}, 1048576, 65536),
		openRouterReasoningModel("google/gemini-3.1-flash-lite", "Google: Gemini 3.1 Flash Lite", []InputKind{InputText, InputImage}, 1048576, 65536),
		openRouterReasoningModel("google/gemini-3.1-flash-lite-preview", "Google: Gemini 3.1 Flash Lite Preview", []InputKind{InputText, InputImage}, 1048576, 65536),
		openRouterReasoningModel("google/gemini-3.1-pro-preview", "Google: Gemini 3.1 Pro Preview", []InputKind{InputText, InputImage}, 1048576, 65536),
		openRouterReasoningModel("google/gemini-3.1-pro-preview-customtools", "Google: Gemini 3.1 Pro Preview Custom Tools", []InputKind{InputText, InputImage}, 1048756, 65536),
		openRouterReasoningModel("google/gemini-3.5-flash", "Google: Gemini 3.5 Flash", []InputKind{InputText, InputImage}, 1048576, 65536),
		openRouterModel("google/gemma-3-12b-it", "Google: Gemma 3 12B", []InputKind{InputText, InputImage}, 131072, 16384),
		openRouterModel("google/gemma-3-27b-it", "Google: Gemma 3 27B", []InputKind{InputText, InputImage}, 131072, 16384),
		openRouterReasoningModel("google/gemma-4-26b-a4b-it", "Google: Gemma 4 26B A4B ", []InputKind{InputText, InputImage}, 262144, 4096),
		openRouterReasoningModel("google/gemma-4-26b-a4b-it:free", "Google: Gemma 4 26B A4B  (free)", []InputKind{InputText, InputImage}, 262144, 32768),
		openRouterReasoningModel("google/gemma-4-31b-it", "Google: Gemma 4 31B", []InputKind{InputText, InputImage}, 262144, 16384),
		openRouterReasoningModel("google/gemma-4-31b-it:free", "Google: Gemma 4 31B (free)", []InputKind{InputText, InputImage}, 262144, 32768),
		openRouterModel("ibm-granite/granite-4.1-8b", "IBM: Granite 4.1 8B", []InputKind{InputText}, 131072, 131072),
		openRouterReasoningModel("inception/mercury-2", "Inception: Mercury 2", []InputKind{InputText}, 128000, 50000),
		openRouterModel("inclusionai/ling-2.6-1t", "inclusionAI: Ling-2.6-1T", []InputKind{InputText}, 262144, 32768),
		openRouterModel("inclusionai/ling-2.6-flash", "inclusionAI: Ling-2.6-flash", []InputKind{InputText}, 262144, 32768),
		openRouterReasoningModel("inclusionai/ring-2.6-1t", "inclusionAI: Ring-2.6-1T", []InputKind{InputText}, 262144, 65536),
		openRouterModel("kwaipilot/kat-coder-pro-v2", "Kwaipilot: KAT-Coder-Pro V2", []InputKind{InputText}, 256000, 80000),
		openRouterModel("meta-llama/llama-3.1-70b-instruct", "Meta: Llama 3.1 70B Instruct", []InputKind{InputText}, 131072, 16384),
		openRouterModel("meta-llama/llama-3.1-8b-instruct", "Meta: Llama 3.1 8B Instruct", []InputKind{InputText}, 131072, 16384),
		openRouterModel("meta-llama/llama-3.3-70b-instruct", "Meta: Llama 3.3 70B Instruct", []InputKind{InputText}, 131072, 16384),
		openRouterModel("meta-llama/llama-3.3-70b-instruct:free", "Meta: Llama 3.3 70B Instruct (free)", []InputKind{InputText}, 131072, 4096),
		openRouterModel("meta-llama/llama-4-scout", "Meta: Llama 4 Scout", []InputKind{InputText, InputImage}, 10000000, 16384),
		openRouterReasoningModel("minimax/minimax-m1", "MiniMax: MiniMax M1", []InputKind{InputText}, 1000000, 40000),
		openRouterReasoningModel("minimax/minimax-m2", "MiniMax: MiniMax M2", []InputKind{InputText}, 204800, 196608),
		openRouterReasoningModel("minimax/minimax-m2.1", "MiniMax: MiniMax M2.1", []InputKind{InputText}, 204800, 196608),
		openRouterReasoningModel("minimax/minimax-m2.5", "MiniMax: MiniMax M2.5", []InputKind{InputText}, 204800, 196608),
		openRouterReasoningModel("minimax/minimax-m2.5:free", "MiniMax: MiniMax M2.5 (free)", []InputKind{InputText}, 204800, 8192),
		displayDefaultModel(openRouterReasoningModel("minimax/minimax-m2.7", "MiniMax: MiniMax M2.7", []InputKind{InputText}, 204800, 131072)),
		openRouterModel("mistralai/codestral-2508", "Mistral: Codestral 2508", []InputKind{InputText}, 256000, 4096),
		openRouterModel("mistralai/devstral-2512", "Mistral: Devstral 2 2512", []InputKind{InputText}, 262144, 4096),
		openRouterModel("mistralai/devstral-medium", "Mistral: Devstral Medium", []InputKind{InputText}, 131072, 4096),
		openRouterModel("mistralai/devstral-small", "Mistral: Devstral Small 1.1", []InputKind{InputText}, 131072, 4096),
		openRouterModel("mistralai/ministral-14b-2512", "Mistral: Ministral 3 14B 2512", []InputKind{InputText, InputImage}, 262144, 4096),
		openRouterModel("mistralai/ministral-3b-2512", "Mistral: Ministral 3 3B 2512", []InputKind{InputText, InputImage}, 131072, 4096),
		openRouterModel("mistralai/ministral-8b-2512", "Mistral: Ministral 3 8B 2512", []InputKind{InputText, InputImage}, 262144, 4096),
		openRouterModel("mistralai/mistral-large", "Mistral Large", []InputKind{InputText}, 128000, 4096),
		openRouterModel("mistralai/mistral-large-2407", "Mistral Large 2407", []InputKind{InputText}, 131072, 4096),
		openRouterModel("mistralai/mistral-large-2411", "Mistral Large 2411", []InputKind{InputText}, 131072, 4096),
		openRouterModel("mistralai/mistral-large-2512", "Mistral: Mistral Large 3 2512", []InputKind{InputText, InputImage}, 262144, 4096),
		openRouterModel("mistralai/mistral-medium-3", "Mistral: Mistral Medium 3", []InputKind{InputText, InputImage}, 131072, 4096),
		openRouterReasoningModel("mistralai/mistral-medium-3-5", "Mistral: Mistral Medium 3.5", []InputKind{InputText, InputImage}, 262144, 4096),
		openRouterModel("mistralai/mistral-medium-3.1", "Mistral: Mistral Medium 3.1", []InputKind{InputText, InputImage}, 131072, 4096),
		openRouterModel("mistralai/mistral-nemo", "Mistral: Mistral Nemo", []InputKind{InputText}, 131072, 4096),
		openRouterModel("mistralai/mistral-saba", "Mistral: Saba", []InputKind{InputText}, 32768, 4096),
		openRouterReasoningModel("mistralai/mistral-small-2603", "Mistral: Mistral Small 4", []InputKind{InputText, InputImage}, 262144, 4096),
		openRouterModel("mistralai/mistral-small-3.2-24b-instruct", "Mistral: Mistral Small 3.2 24B", []InputKind{InputText, InputImage}, 128000, 16384),
		openRouterModel("mistralai/mixtral-8x22b-instruct", "Mistral: Mixtral 8x22B Instruct", []InputKind{InputText}, 65536, 4096),
		openRouterModel("mistralai/pixtral-large-2411", "Mistral: Pixtral Large 2411", []InputKind{InputText, InputImage}, 131072, 4096),
		openRouterModel("mistralai/voxtral-small-24b-2507", "Mistral: Voxtral Small 24B 2507", []InputKind{InputText}, 32000, 4096),
		openRouterModel("moonshotai/kimi-k2", "MoonshotAI: Kimi K2 0711", []InputKind{InputText}, 131072, 32768),
		openRouterModel("moonshotai/kimi-k2-0905", "MoonshotAI: Kimi K2 0905", []InputKind{InputText}, 262144, 262144),
		openRouterReasoningModel("moonshotai/kimi-k2-thinking", "MoonshotAI: Kimi K2 Thinking", []InputKind{InputText}, 262144, 262144),
		openRouterReasoningModel("moonshotai/kimi-k2.5", "MoonshotAI: Kimi K2.5", []InputKind{InputText, InputImage}, 262144, 4096),
		openRouterReasoningModel("moonshotai/kimi-k2.6", "MoonshotAI: Kimi K2.6", []InputKind{InputText, InputImage}, 262144, 262142),
		openRouterModel("nex-agi/deepseek-v3.1-nex-n1", "Nex AGI: DeepSeek V3.1 Nex N1", []InputKind{InputText}, 131072, 163840),
		openRouterReasoningModel("nvidia/llama-3.3-nemotron-super-49b-v1.5", "NVIDIA: Llama 3.3 Nemotron Super 49B V1.5", []InputKind{InputText}, 131072, 16384),
		openRouterReasoningModel("nvidia/nemotron-3-nano-30b-a3b", "NVIDIA: Nemotron 3 Nano 30B A3B", []InputKind{InputText}, 262144, 228000),
		openRouterReasoningModel("nvidia/nemotron-3-nano-30b-a3b:free", "NVIDIA: Nemotron 3 Nano 30B A3B (free)", []InputKind{InputText}, 256000, 4096),
		openRouterReasoningModel("nvidia/nemotron-3-nano-omni-30b-a3b-reasoning:free", "NVIDIA: Nemotron 3 Nano Omni (free)", []InputKind{InputText, InputImage}, 256000, 65536),
		openRouterReasoningModel("nvidia/nemotron-3-super-120b-a12b", "NVIDIA: Nemotron 3 Super", []InputKind{InputText}, 1000000, 4096),
		openRouterReasoningModel("nvidia/nemotron-3-super-120b-a12b:free", "NVIDIA: Nemotron 3 Super (free)", []InputKind{InputText}, 1000000, 262144),
		openRouterReasoningModel("nvidia/nemotron-nano-12b-v2-vl:free", "NVIDIA: Nemotron Nano 12B 2 VL (free)", []InputKind{InputText, InputImage}, 128000, 128000),
		openRouterReasoningModel("nvidia/nemotron-nano-9b-v2", "NVIDIA: Nemotron Nano 9B V2", []InputKind{InputText}, 131072, 16384),
		openRouterReasoningModel("nvidia/nemotron-nano-9b-v2:free", "NVIDIA: Nemotron Nano 9B V2 (free)", []InputKind{InputText}, 128000, 4096),
		openRouterModel("openai/gpt-3.5-turbo", "OpenAI: GPT-3.5 Turbo", []InputKind{InputText}, 16385, 4096),
		openRouterModel("openai/gpt-3.5-turbo-0613", "OpenAI: GPT-3.5 Turbo (older v0613)", []InputKind{InputText}, 4095, 4096),
		openRouterModel("openai/gpt-3.5-turbo-16k", "OpenAI: GPT-3.5 Turbo 16k", []InputKind{InputText}, 16385, 4096),
		openRouterModel("openai/gpt-4", "OpenAI: GPT-4", []InputKind{InputText}, 8191, 4096),
		openRouterModel("openai/gpt-4-0314", "OpenAI: GPT-4 (older v0314)", []InputKind{InputText}, 8191, 4096),
		openRouterModel("openai/gpt-4-1106-preview", "OpenAI: GPT-4 Turbo (older v1106)", []InputKind{InputText}, 128000, 4096),
		openRouterModel("openai/gpt-4-turbo", "OpenAI: GPT-4 Turbo", []InputKind{InputText, InputImage}, 128000, 4096),
		openRouterModel("openai/gpt-4-turbo-preview", "OpenAI: GPT-4 Turbo Preview", []InputKind{InputText}, 128000, 4096),
		openRouterModel("openai/gpt-4.1", "OpenAI: GPT-4.1", []InputKind{InputText, InputImage}, 1047576, 4096),
		openRouterModel("openai/gpt-4.1-mini", "OpenAI: GPT-4.1 Mini", []InputKind{InputText, InputImage}, 1047576, 32768),
		openRouterModel("openai/gpt-4.1-nano", "OpenAI: GPT-4.1 Nano", []InputKind{InputText, InputImage}, 1047576, 32768),
		openRouterModel("openai/gpt-4o", "OpenAI: GPT-4o", []InputKind{InputText, InputImage}, 128000, 16384),
		openRouterModel("openai/gpt-4o-2024-05-13", "OpenAI: GPT-4o (2024-05-13)", []InputKind{InputText, InputImage}, 128000, 4096),
		openRouterModel("openai/gpt-4o-2024-08-06", "OpenAI: GPT-4o (2024-08-06)", []InputKind{InputText, InputImage}, 128000, 16384),
		openRouterModel("openai/gpt-4o-2024-11-20", "OpenAI: GPT-4o (2024-11-20)", []InputKind{InputText, InputImage}, 128000, 16384),
		openRouterModel("openai/gpt-4o-audio-preview", "OpenAI: GPT-4o Audio", []InputKind{InputText}, 128000, 16384),
		openRouterModel("openai/gpt-4o-mini", "OpenAI: GPT-4o-mini", []InputKind{InputText, InputImage}, 128000, 16384),
		openRouterModel("openai/gpt-4o-mini-2024-07-18", "OpenAI: GPT-4o-mini (2024-07-18)", []InputKind{InputText, InputImage}, 128000, 16384),
		openRouterReasoningModel("openai/gpt-5", "OpenAI: GPT-5", []InputKind{InputText, InputImage}, 400000, 128000),
		openRouterReasoningModel("openai/gpt-5-codex", "OpenAI: GPT-5 Codex", []InputKind{InputText, InputImage}, 400000, 128000),
		openRouterReasoningModel("openai/gpt-5-mini", "OpenAI: GPT-5 Mini", []InputKind{InputText, InputImage}, 400000, 128000),
		openRouterReasoningModel("openai/gpt-5-nano", "OpenAI: GPT-5 Nano", []InputKind{InputText, InputImage}, 400000, 4096),
		openRouterReasoningModel("openai/gpt-5-pro", "OpenAI: GPT-5 Pro", []InputKind{InputText, InputImage}, 400000, 128000),
		openRouterReasoningModel("openai/gpt-5.1", "OpenAI: GPT-5.1", []InputKind{InputText, InputImage}, 400000, 128000),
		openRouterModel("openai/gpt-5.1-chat", "OpenAI: GPT-5.1 Chat", []InputKind{InputText, InputImage}, 128000, 16384),
		openRouterReasoningModel("openai/gpt-5.1-codex", "OpenAI: GPT-5.1-Codex", []InputKind{InputText, InputImage}, 400000, 128000),
		openRouterReasoningModel("openai/gpt-5.1-codex-max", "OpenAI: GPT-5.1-Codex-Max", []InputKind{InputText, InputImage}, 400000, 128000),
		openRouterReasoningModel("openai/gpt-5.1-codex-mini", "OpenAI: GPT-5.1-Codex-Mini", []InputKind{InputText, InputImage}, 400000, 128000),
		openRouterReasoningModel("openai/gpt-5.2", "OpenAI: GPT-5.2", []InputKind{InputText, InputImage}, 400000, 128000),
		openRouterModel("openai/gpt-5.2-chat", "OpenAI: GPT-5.2 Chat", []InputKind{InputText, InputImage}, 128000, 32000),
		openRouterReasoningModel("openai/gpt-5.2-codex", "OpenAI: GPT-5.2-Codex", []InputKind{InputText, InputImage}, 400000, 128000),
		openRouterReasoningModel("openai/gpt-5.2-pro", "OpenAI: GPT-5.2 Pro", []InputKind{InputText, InputImage}, 400000, 128000),
		openRouterModel("openai/gpt-5.3-chat", "OpenAI: GPT-5.3 Chat", []InputKind{InputText, InputImage}, 128000, 16384),
		openRouterReasoningModel("openai/gpt-5.3-codex", "OpenAI: GPT-5.3-Codex", []InputKind{InputText, InputImage}, 400000, 128000),
		openRouterReasoningModel("openai/gpt-5.4", "OpenAI: GPT-5.4", []InputKind{InputText, InputImage}, 1050000, 128000),
		openRouterReasoningModel("openai/gpt-5.4-mini", "OpenAI: GPT-5.4 Mini", []InputKind{InputText, InputImage}, 400000, 128000),
		openRouterReasoningModel("openai/gpt-5.4-nano", "OpenAI: GPT-5.4 Nano", []InputKind{InputText, InputImage}, 400000, 128000),
		openRouterReasoningModel("openai/gpt-5.4-pro", "OpenAI: GPT-5.4 Pro", []InputKind{InputText, InputImage}, 1050000, 128000),
		openRouterReasoningModel("openai/gpt-5.5", "OpenAI: GPT-5.5", []InputKind{InputText, InputImage}, 1050000, 128000),
		openRouterReasoningModel("openai/gpt-5.5-pro", "OpenAI: GPT-5.5 Pro", []InputKind{InputText, InputImage}, 1050000, 128000),
		openRouterModel("openai/gpt-audio", "OpenAI: GPT Audio", []InputKind{InputText}, 128000, 16384),
		openRouterModel("openai/gpt-audio-mini", "OpenAI: GPT Audio Mini", []InputKind{InputText}, 128000, 16384),
		openRouterModel("openai/gpt-chat-latest", "OpenAI: GPT Chat Latest", []InputKind{InputText, InputImage}, 400000, 128000),
		openRouterReasoningModel("openai/gpt-oss-120b", "OpenAI: gpt-oss-120b", []InputKind{InputText}, 131072, 4096),
		openRouterReasoningModel("openai/gpt-oss-120b:free", "OpenAI: gpt-oss-120b (free)", []InputKind{InputText}, 131072, 131072),
		openRouterReasoningModel("openai/gpt-oss-20b", "OpenAI: gpt-oss-20b", []InputKind{InputText}, 131072, 131072),
		openRouterReasoningModel("openai/gpt-oss-20b:free", "OpenAI: gpt-oss-20b (free)", []InputKind{InputText}, 131072, 8192),
		openRouterReasoningModel("openai/gpt-oss-safeguard-20b", "OpenAI: gpt-oss-safeguard-20b", []InputKind{InputText}, 131072, 65536),
		openRouterReasoningModel("openai/o1", "OpenAI: o1", []InputKind{InputText, InputImage}, 200000, 100000),
		openRouterReasoningModel("openai/o3", "OpenAI: o3", []InputKind{InputText, InputImage}, 200000, 100000),
		openRouterReasoningModel("openai/o3-deep-research", "OpenAI: o3 Deep Research", []InputKind{InputText, InputImage}, 200000, 100000),
		openRouterReasoningModel("openai/o3-mini", "OpenAI: o3 Mini", []InputKind{InputText}, 200000, 100000),
		openRouterReasoningModel("openai/o3-mini-high", "OpenAI: o3 Mini High", []InputKind{InputText}, 200000, 100000),
		openRouterReasoningModel("openai/o3-pro", "OpenAI: o3 Pro", []InputKind{InputText, InputImage}, 200000, 100000),
		openRouterReasoningModel("openai/o4-mini", "OpenAI: o4 Mini", []InputKind{InputText, InputImage}, 200000, 100000),
		openRouterReasoningModel("openai/o4-mini-deep-research", "OpenAI: o4 Mini Deep Research", []InputKind{InputText, InputImage}, 200000, 100000),
		openRouterReasoningModel("openai/o4-mini-high", "OpenAI: o4 Mini High", []InputKind{InputText, InputImage}, 200000, 100000),
		openRouterReasoningModel("openrouter/auto", "Auto Router", []InputKind{InputText, InputImage}, 2000000, 4096),
		openRouterReasoningModel("openrouter/free", "Free Models Router", []InputKind{InputText, InputImage}, 200000, 4096),
		openRouterModel("openrouter/owl-alpha", "Owl Alpha", []InputKind{InputText}, 1048756, 262144),
		openRouterReasoningModel("poolside/laguna-m.1:free", "Poolside: Laguna M.1 (free)", []InputKind{InputText}, 131072, 8192),
		openRouterReasoningModel("poolside/laguna-xs.2:free", "Poolside: Laguna XS.2 (free)", []InputKind{InputText}, 131072, 8192),
		openRouterReasoningModel("prime-intellect/intellect-3", "Prime Intellect: INTELLECT-3", []InputKind{InputText}, 131072, 131072),
		openRouterModel("qwen/qwen-2.5-72b-instruct", "Qwen2.5 72B Instruct", []InputKind{InputText}, 131072, 16384),
		openRouterModel("qwen/qwen-2.5-7b-instruct", "Qwen: Qwen2.5 7B Instruct", []InputKind{InputText}, 131072, 32768),
		openRouterModel("qwen/qwen-plus", "Qwen: Qwen-Plus", []InputKind{InputText}, 1000000, 32768),
		openRouterModel("qwen/qwen-plus-2025-07-28", "Qwen: Qwen Plus 0728", []InputKind{InputText}, 1000000, 32768),
		openRouterReasoningModel("qwen/qwen-plus-2025-07-28:thinking", "Qwen: Qwen Plus 0728 (thinking)", []InputKind{InputText}, 1000000, 32768),
		openRouterReasoningModel("qwen/qwen3-14b", "Qwen: Qwen3 14B", []InputKind{InputText}, 131702, 40960),
		openRouterReasoningModel("qwen/qwen3-235b-a22b", "Qwen: Qwen3 235B A22B", []InputKind{InputText}, 131072, 8192),
		openRouterModel("qwen/qwen3-235b-a22b-2507", "Qwen: Qwen3 235B A22B Instruct 2507", []InputKind{InputText}, 262144, 16384),
		openRouterReasoningModel("qwen/qwen3-235b-a22b-thinking-2507", "Qwen: Qwen3 235B A22B Thinking 2507", []InputKind{InputText}, 262144, 4096),
		openRouterReasoningModel("qwen/qwen3-30b-a3b", "Qwen: Qwen3 30B A3B", []InputKind{InputText}, 131072, 20000),
		openRouterModel("qwen/qwen3-30b-a3b-instruct-2507", "Qwen: Qwen3 30B A3B Instruct 2507", []InputKind{InputText}, 262144, 262144),
		openRouterReasoningModel("qwen/qwen3-30b-a3b-thinking-2507", "Qwen: Qwen3 30B A3B Thinking 2507", []InputKind{InputText}, 131072, 131072),
		openRouterReasoningModel("qwen/qwen3-32b", "Qwen: Qwen3 32B", []InputKind{InputText}, 131072, 16384),
		openRouterReasoningModel("qwen/qwen3-8b", "Qwen: Qwen3 8B", []InputKind{InputText}, 131072, 8192),
		openRouterModel("qwen/qwen3-coder", "Qwen: Qwen3 Coder 480B A35B", []InputKind{InputText}, 1048576, 65536),
		openRouterModel("qwen/qwen3-coder-30b-a3b-instruct", "Qwen: Qwen3 Coder 30B A3B Instruct", []InputKind{InputText}, 160000, 32768),
		openRouterModel("qwen/qwen3-coder-flash", "Qwen: Qwen3 Coder Flash", []InputKind{InputText}, 1000000, 65536),
		openRouterModel("qwen/qwen3-coder-next", "Qwen: Qwen3 Coder Next", []InputKind{InputText}, 262144, 262144),
		openRouterModel("qwen/qwen3-coder-plus", "Qwen: Qwen3 Coder Plus", []InputKind{InputText}, 1000000, 65536),
		openRouterModel("qwen/qwen3-coder:free", "Qwen: Qwen3 Coder 480B A35B (free)", []InputKind{InputText}, 1048576, 262000),
		openRouterModel("qwen/qwen3-max", "Qwen: Qwen3 Max", []InputKind{InputText}, 262144, 32768),
		openRouterReasoningModel("qwen/qwen3-max-thinking", "Qwen: Qwen3 Max Thinking", []InputKind{InputText}, 262144, 32768),
		openRouterModel("qwen/qwen3-next-80b-a3b-instruct", "Qwen: Qwen3 Next 80B A3B Instruct", []InputKind{InputText}, 262144, 16384),
		openRouterModel("qwen/qwen3-next-80b-a3b-instruct:free", "Qwen: Qwen3 Next 80B A3B Instruct (free)", []InputKind{InputText}, 262144, 4096),
		openRouterReasoningModel("qwen/qwen3-next-80b-a3b-thinking", "Qwen: Qwen3 Next 80B A3B Thinking", []InputKind{InputText}, 262144, 32768),
		openRouterModel("qwen/qwen3-vl-235b-a22b-instruct", "Qwen: Qwen3 VL 235B A22B Instruct", []InputKind{InputText, InputImage}, 262144, 16384),
		openRouterReasoningModel("qwen/qwen3-vl-235b-a22b-thinking", "Qwen: Qwen3 VL 235B A22B Thinking", []InputKind{InputText, InputImage}, 131072, 32768),
		openRouterModel("qwen/qwen3-vl-30b-a3b-instruct", "Qwen: Qwen3 VL 30B A3B Instruct", []InputKind{InputText, InputImage}, 262144, 32768),
		openRouterReasoningModel("qwen/qwen3-vl-30b-a3b-thinking", "Qwen: Qwen3 VL 30B A3B Thinking", []InputKind{InputText, InputImage}, 131072, 32768),
		openRouterModel("qwen/qwen3-vl-32b-instruct", "Qwen: Qwen3 VL 32B Instruct", []InputKind{InputText, InputImage}, 262144, 32768),
		openRouterModel("qwen/qwen3-vl-8b-instruct", "Qwen: Qwen3 VL 8B Instruct", []InputKind{InputText, InputImage}, 256000, 32768),
		openRouterReasoningModel("qwen/qwen3-vl-8b-thinking", "Qwen: Qwen3 VL 8B Thinking", []InputKind{InputText, InputImage}, 256000, 32768),
		openRouterReasoningModel("qwen/qwen3.5-122b-a10b", "Qwen: Qwen3.5-122B-A10B", []InputKind{InputText, InputImage}, 262144, 262144),
		openRouterReasoningModel("qwen/qwen3.5-27b", "Qwen: Qwen3.5-27B", []InputKind{InputText, InputImage}, 262144, 65536),
		openRouterReasoningModel("qwen/qwen3.5-35b-a3b", "Qwen: Qwen3.5-35B-A3B", []InputKind{InputText, InputImage}, 262144, 4096),
		openRouterReasoningModel("qwen/qwen3.5-397b-a17b", "Qwen: Qwen3.5 397B A17B", []InputKind{InputText, InputImage}, 262144, 65536),
		openRouterReasoningModel("qwen/qwen3.5-9b", "Qwen: Qwen3.5-9B", []InputKind{InputText, InputImage}, 262144, 81920),
		openRouterReasoningModel("qwen/qwen3.5-flash-02-23", "Qwen: Qwen3.5-Flash", []InputKind{InputText, InputImage}, 1000000, 65536),
		openRouterReasoningModel("qwen/qwen3.5-plus-02-15", "Qwen: Qwen3.5 Plus 2026-02-15", []InputKind{InputText, InputImage}, 1000000, 65536),
		openRouterReasoningModel("qwen/qwen3.5-plus-20260420", "Qwen: Qwen3.5 Plus 2026-04-20", []InputKind{InputText, InputImage}, 1000000, 65536),
		openRouterReasoningModel("qwen/qwen3.6-27b", "Qwen: Qwen3.6 27B", []InputKind{InputText, InputImage}, 262144, 262144),
		openRouterReasoningModel("qwen/qwen3.6-35b-a3b", "Qwen: Qwen3.6 35B A3B", []InputKind{InputText, InputImage}, 262144, 262140),
		openRouterReasoningModel("qwen/qwen3.6-flash", "Qwen: Qwen3.6 Flash", []InputKind{InputText, InputImage}, 1000000, 65536),
		openRouterReasoningModel("qwen/qwen3.6-max-preview", "Qwen: Qwen3.6 Max Preview", []InputKind{InputText}, 262144, 65536),
		openRouterReasoningModel("qwen/qwen3.6-plus", "Qwen: Qwen3.6 Plus", []InputKind{InputText, InputImage}, 1000000, 65536),
		openRouterReasoningModel("qwen/qwen3.7-max", "Qwen: Qwen3.7 Max", []InputKind{InputText}, 1000000, 65536),
		openRouterModel("rekaai/reka-edge", "Reka Edge", []InputKind{InputText, InputImage}, 16384, 16384),
		openRouterModel("relace/relace-search", "Relace: Relace Search", []InputKind{InputText}, 256000, 128000),
		openRouterModel("sao10k/l3-euryale-70b", "Sao10k: Llama 3 Euryale 70B v2.1", []InputKind{InputText}, 8192, 8192),
		openRouterModel("sao10k/l3.1-euryale-70b", "Sao10K: Llama 3.1 Euryale 70B v2.2", []InputKind{InputText}, 131072, 16384),
		openRouterReasoningModel("stepfun/step-3.5-flash", "StepFun: Step 3.5 Flash", []InputKind{InputText}, 262144, 16384),
		openRouterReasoningModel("tencent/hy3-preview", "Tencent: Hy3 preview", []InputKind{InputText}, 262144, 262144),
		openRouterModel("thedrummer/rocinante-12b", "TheDrummer: Rocinante 12B", []InputKind{InputText}, 32768, 32768),
		openRouterModel("thedrummer/unslopnemo-12b", "TheDrummer: UnslopNemo 12B", []InputKind{InputText}, 32768, 32768),
		openRouterReasoningModel("upstage/solar-pro-3", "Upstage: Solar Pro 3", []InputKind{InputText}, 128000, 4096),
		openRouterReasoningModel("x-ai/grok-4.20", "xAI: Grok 4.20", []InputKind{InputText, InputImage}, 2000000, 4096),
		openRouterReasoningModel("x-ai/grok-4.3", "xAI: Grok 4.3", []InputKind{InputText, InputImage}, 1000000, 4096),
		openRouterReasoningModel("x-ai/grok-build-0.1", "xAI: Grok Build 0.1", []InputKind{InputText, InputImage}, 256000, 4096),
		openRouterReasoningModel("xiaomi/mimo-v2-flash", "Xiaomi: MiMo-V2-Flash", []InputKind{InputText}, 262144, 65536),
		openRouterReasoningModel("xiaomi/mimo-v2-omni", "Xiaomi: MiMo-V2-Omni", []InputKind{InputText, InputImage}, 262144, 65536),
		openRouterReasoningModel("xiaomi/mimo-v2-pro", "Xiaomi: MiMo-V2-Pro", []InputKind{InputText}, 1048576, 131072),
		openRouterReasoningModel("xiaomi/mimo-v2.5", "Xiaomi: MiMo-V2.5", []InputKind{InputText, InputImage}, 1048576, 131072),
		openRouterReasoningModel("xiaomi/mimo-v2.5-pro", "Xiaomi: MiMo-V2.5-Pro", []InputKind{InputText}, 1048576, 16384),
		openRouterModel("z-ai/glm-4-32b", "Z.ai: GLM 4 32B ", []InputKind{InputText}, 128000, 4096),
		openRouterReasoningModel("z-ai/glm-4.5", "Z.ai: GLM 4.5", []InputKind{InputText}, 131072, 98304),
		openRouterReasoningModel("z-ai/glm-4.5-air", "Z.ai: GLM 4.5 Air", []InputKind{InputText}, 131072, 98304),
		openRouterReasoningModel("z-ai/glm-4.5-air:free", "Z.ai: GLM 4.5 Air (free)", []InputKind{InputText}, 131072, 96000),
		openRouterReasoningModel("z-ai/glm-4.5v", "Z.ai: GLM 4.5V", []InputKind{InputText, InputImage}, 65536, 16384),
		openRouterReasoningModel("z-ai/glm-4.6", "Z.ai: GLM 4.6", []InputKind{InputText}, 202752, 131072),
		openRouterReasoningModel("z-ai/glm-4.6v", "Z.ai: GLM 4.6V", []InputKind{InputText, InputImage}, 131072, 24000),
		openRouterReasoningModel("z-ai/glm-4.7", "Z.ai: GLM 4.7", []InputKind{InputText}, 202752, 131072),
		openRouterReasoningModel("z-ai/glm-4.7-flash", "Z.ai: GLM 4.7 Flash", []InputKind{InputText}, 202752, 16384),
		openRouterReasoningModel("z-ai/glm-5", "Z.ai: GLM 5", []InputKind{InputText}, 202752, 4096),
		openRouterReasoningModel("z-ai/glm-5-turbo", "Z.ai: GLM 5 Turbo", []InputKind{InputText}, 202752, 131072),
		openRouterReasoningModel("z-ai/glm-5.1", "Z.ai: GLM 5.1", []InputKind{InputText}, 202752, 4096),
		openRouterReasoningModel("z-ai/glm-5v-turbo", "Z.ai: GLM 5V Turbo", []InputKind{InputText, InputImage}, 202752, 131072),
		openRouterReasoningModel("~anthropic/claude-haiku-latest", "Anthropic Claude Haiku Latest", []InputKind{InputText, InputImage}, 200000, 64000),
		openRouterReasoningModel("~anthropic/claude-opus-latest", "Anthropic: Claude Opus Latest", []InputKind{InputText, InputImage}, 1000000, 128000),
		openRouterReasoningModel("~anthropic/claude-sonnet-latest", "Anthropic Claude Sonnet Latest", []InputKind{InputText, InputImage}, 1000000, 128000),
		openRouterReasoningModel("~google/gemini-flash-latest", "Google Gemini Flash Latest", []InputKind{InputText, InputImage}, 1048576, 65536),
		openRouterReasoningModel("~google/gemini-pro-latest", "Google Gemini Pro Latest", []InputKind{InputText, InputImage}, 1048576, 65536),
		openRouterReasoningModel("~moonshotai/kimi-latest", "MoonshotAI Kimi Latest", []InputKind{InputText, InputImage}, 262144, 262142),
		openRouterReasoningModel("~openai/gpt-latest", "OpenAI GPT Latest", []InputKind{InputText, InputImage}, 1050000, 128000),
		openRouterReasoningModel("~openai/gpt-mini-latest", "OpenAI GPT Mini Latest", []InputKind{InputText, InputImage}, 400000, 128000),
		{
			ID:            constants.DefaultProfileEmbeddingModel,
			Name:          "Text Embedding 3 Small via OpenRouter",
			Owner:         constants.ModelProviderOpenAI,
			Provider:      constants.ModelProviderOpenRouter,
			API:           APIOpenRouterEmbeddings,
			Input:         []InputKind{InputText},
			ContextWindow: 8191,
		},

		// Anthropic models
		anthropicModel("claude-3-5-haiku-20241022", "Claude Haiku 3.5", []InputKind{InputText, InputImage}, 200000, 8192),
		anthropicModel("claude-3-5-haiku-latest", "Claude Haiku 3.5 (latest)", []InputKind{InputText, InputImage}, 200000, 8192),
		anthropicModel("claude-3-5-sonnet-20240620", "Claude Sonnet 3.5", []InputKind{InputText, InputImage}, 200000, 8192),
		anthropicModel("claude-3-5-sonnet-20241022", "Claude Sonnet 3.5 v2", []InputKind{InputText, InputImage}, 200000, 8192),
		anthropicReasoningModel("claude-3-7-sonnet-20250219", "Claude Sonnet 3.7", []InputKind{InputText, InputImage}, 200000, 64000),
		anthropicModel("claude-3-haiku-20240307", "Claude Haiku 3", []InputKind{InputText, InputImage}, 200000, 4096),
		anthropicModel("claude-3-opus-20240229", "Claude Opus 3", []InputKind{InputText, InputImage}, 200000, 4096),
		anthropicModel("claude-3-sonnet-20240229", "Claude Sonnet 3", []InputKind{InputText, InputImage}, 200000, 4096),
		anthropicOAuthReasoningModel("claude-haiku-4-5", "Claude Haiku 4.5 (latest)", []InputKind{InputText, InputImage}, 200000, 64000),
		anthropicReasoningModel("claude-haiku-4-5-20251001", "Claude Haiku 4.5", []InputKind{InputText, InputImage}, 200000, 64000),
		anthropicReasoningModel("claude-opus-4-0", "Claude Opus 4 (latest)", []InputKind{InputText, InputImage}, 200000, 32000),
		anthropicReasoningModel("claude-opus-4-1", "Claude Opus 4.1 (latest)", []InputKind{InputText, InputImage}, 200000, 32000),
		anthropicReasoningModel("claude-opus-4-1-20250805", "Claude Opus 4.1", []InputKind{InputText, InputImage}, 200000, 32000),
		anthropicReasoningModel("claude-opus-4-20250514", "Claude Opus 4", []InputKind{InputText, InputImage}, 200000, 32000),
		anthropicReasoningModel("claude-opus-4-5", "Claude Opus 4.5 (latest)", []InputKind{InputText, InputImage}, 200000, 64000),
		anthropicReasoningModel("claude-opus-4-5-20251101", "Claude Opus 4.5", []InputKind{InputText, InputImage}, 200000, 64000),
		anthropicReasoningModel("claude-opus-4-6", "Claude Opus 4.6", []InputKind{InputText, InputImage}, 1000000, 128000),
		anthropicOAuthReasoningModel("claude-opus-4-7", "Claude Opus 4.7", []InputKind{InputText, InputImage}, 1000000, 128000),
		anthropicReasoningModel("claude-sonnet-4-0", "Claude Sonnet 4 (latest)", []InputKind{InputText, InputImage}, 200000, 64000),
		anthropicReasoningModel("claude-sonnet-4-20250514", "Claude Sonnet 4", []InputKind{InputText, InputImage}, 200000, 64000),
		anthropicReasoningModel("claude-sonnet-4-5", "Claude Sonnet 4.5 (latest)", []InputKind{InputText, InputImage}, 200000, 64000),
		anthropicReasoningModel("claude-sonnet-4-5-20250929", "Claude Sonnet 4.5", []InputKind{InputText, InputImage}, 200000, 64000),
		displayDefaultModel(anthropicOAuthReasoningModel("claude-sonnet-4-6", "Claude Sonnet 4.6", []InputKind{InputText, InputImage}, 1000000, 64000)),
	}
}

func displayDefaultModel(model ModelDefinition) ModelDefinition {
	model.DisplayDefault = true

	return model
}

func openAIModel(
	id string,
	name string,
	input []InputKind,
	contextWindow int,
	maxTokens int,
) ModelDefinition {
	return ModelDefinition{
		ID:            id,
		Name:          name,
		Owner:         constants.ModelProviderOpenAI,
		Provider:      constants.ModelProviderOpenAI,
		API:           APIOpenAIResponses,
		Input:         input,
		ContextWindow: contextWindow,
		MaxTokens:     maxTokens,
	}
}

func openAIReasoningModel(
	id string,
	name string,
	input []InputKind,
	contextWindow int,
	maxTokens int,
) ModelDefinition {
	model := openAIModel(id, name, input, contextWindow, maxTokens)
	model.Reasoning = true
	model.ReasoningCapabilities.Summary = true
	if capability, ok := openAIReasoningCapability(id); ok {
		model.ReasoningCapabilities = capability
	}

	return model
}

func openAICodexModel(
	id string,
	name string,
	input []InputKind,
	contextWindow int,
	maxTokens int,
) ModelDefinition {
	model := ModelDefinition{
		ID:            id,
		Name:          name,
		Owner:         constants.ModelProviderOpenAI,
		Provider:      constants.ModelProviderOpenAICodex,
		API:           APIOpenAIResponses,
		Input:         input,
		Reasoning:     true,
		SupportsOAuth: true,
		ContextWindow: contextWindow,
		MaxTokens:     maxTokens,
	}
	model.ReasoningCapabilities.Summary = true
	if capability, ok := openAIReasoningCapability(id); ok {
		model.ReasoningCapabilities = capability
	}

	return model
}

func ollamaModel(
	id string,
	name string,
	input []InputKind,
	contextWindow int,
	maxTokens int,
) ModelDefinition {
	return ModelDefinition{
		ID:            id,
		Name:          name,
		Owner:         constants.ModelProviderOllama,
		Provider:      constants.ModelProviderOllama,
		API:           APIOllamaNative,
		Input:         input,
		SupportsTools: true,
		ContextWindow: contextWindow,
		MaxTokens:     maxTokens,
	}
}

func ollamaReasoningModel(
	id string,
	name string,
	input []InputKind,
	contextWindow int,
	maxTokens int,
) ModelDefinition {
	model := ollamaModel(id, name, input, contextWindow, maxTokens)
	model.Reasoning = true

	return model
}

func ollamaEmbeddingModel(id string, name string, contextWindow int) ModelDefinition {
	return ModelDefinition{
		ID:            id,
		Name:          name,
		Owner:         constants.ModelProviderOllama,
		Provider:      constants.ModelProviderOllama,
		API:           APIOllamaEmbeddings,
		Input:         []InputKind{InputText},
		ContextWindow: contextWindow,
	}
}

func gitHubCopilotResponsesModel(
	id string,
	name string,
	input []InputKind,
	contextWindow int,
	maxTokens int,
) ModelDefinition {
	return gitHubCopilotModel(id, name, APIOpenAIResponses, input, true, contextWindow, maxTokens)
}

func gitHubCopilotCompletionModel(
	id string,
	name string,
	input []InputKind,
	reasoning bool,
	contextWindow int,
	maxTokens int,
) ModelDefinition {
	return gitHubCopilotModel(id, name, APIOpenAICompletions, input, reasoning, contextWindow, maxTokens)
}

func gitHubCopilotAnthropicModel(
	id string,
	name string,
	input []InputKind,
	contextWindow int,
	maxTokens int,
) ModelDefinition {
	return gitHubCopilotModel(id, name, APIAnthropicMessages, input, true, contextWindow, maxTokens)
}

func gitHubCopilotModel(
	id string,
	name string,
	api string,
	input []InputKind,
	reasoning bool,
	contextWindow int,
	maxTokens int,
) ModelDefinition {
	return ModelDefinition{
		ID:            id,
		Name:          name,
		Owner:         constants.ModelProviderGitHubCopilot,
		Provider:      constants.ModelProviderGitHubCopilot,
		API:           api,
		Input:         input,
		Reasoning:     reasoning,
		SupportsOAuth: true,
		ContextWindow: contextWindow,
		MaxTokens:     maxTokens,
	}
}

func openRouterModel(
	id string,
	name string,
	input []InputKind,
	contextWindow int,
	maxTokens int,
) ModelDefinition {
	return ModelDefinition{
		ID:            id,
		Name:          name,
		Owner:         getModelOwnerFromID(id),
		Provider:      constants.ModelProviderOpenRouter,
		API:           APIOpenAIResponses,
		Input:         input,
		ContextWindow: contextWindow,
		MaxTokens:     maxTokens,
	}
}

func openRouterReasoningModel(
	id string,
	name string,
	input []InputKind,
	contextWindow int,
	maxTokens int,
) ModelDefinition {
	model := openRouterModel(id, name, input, contextWindow, maxTokens)
	model.Reasoning = true
	if capability, ok := openRouterReasoningCapability(id); ok {
		model.ReasoningCapabilities = capability
	}

	return model
}

func openRouterReasoningCapability(id string) (ReasoningCapability, bool) {
	capability := ReasoningCapability{
		Efforts:       []ReasoningEffort{"xhigh", "high", "medium", "low", "none"},
		DefaultEffort: "medium",
		Summary:       true,
	}
	switch id {
	case "openai/gpt-5.5", "openai/gpt-5.4-mini", "openai/gpt-5.4-nano":
		return capability, true
	case "openai/gpt-5.5-pro":
		capability.Efforts = []ReasoningEffort{"xhigh", "high", "medium"}
		return capability, true
	default:
		return ReasoningCapability{}, false
	}
}

func anthropicModel(
	id string,
	name string,
	input []InputKind,
	contextWindow int,
	maxTokens int,
) ModelDefinition {
	return ModelDefinition{
		ID:            id,
		Name:          name,
		Owner:         constants.ModelProviderAnthropic,
		Provider:      constants.ModelProviderAnthropic,
		API:           APIAnthropicMessages,
		Input:         input,
		ContextWindow: contextWindow,
		MaxTokens:     maxTokens,
	}
}

func anthropicOAuthModel(
	id string,
	name string,
	input []InputKind,
	contextWindow int,
	maxTokens int,
) ModelDefinition {
	model := anthropicModel(id, name, input, contextWindow, maxTokens)
	model.SupportsOAuth = true

	return model
}

func anthropicReasoningModel(
	id string,
	name string,
	input []InputKind,
	contextWindow int,
	maxTokens int,
) ModelDefinition {
	model := anthropicModel(id, name, input, contextWindow, maxTokens)
	model.Reasoning = true
	model.ReasoningCapabilities = anthropicReasoningCapability(id)

	return model
}

func anthropicOAuthReasoningModel(
	id string,
	name string,
	input []InputKind,
	contextWindow int,
	maxTokens int,
) ModelDefinition {
	model := anthropicOAuthModel(id, name, input, contextWindow, maxTokens)
	model.Reasoning = true
	model.ReasoningCapabilities = anthropicReasoningCapability(id)

	return model
}

func openAIReasoningCapability(id string) (ReasoningCapability, bool) {
	var capability ReasoningCapability
	switch id {
	case "gpt-5":
		capability.Efforts = []ReasoningEffort{"minimal", "low", "medium", "high"}
		capability.DefaultEffort = "medium"
	case "gpt-5.1":
		capability.Efforts = []ReasoningEffort{"none", "low", "medium", "high"}
		capability.DefaultEffort = "none"
	case "gpt-5.2":
		capability.Efforts = []ReasoningEffort{"none", "low", "medium", "high", "xhigh"}
		capability.DefaultEffort = "none"
	case "gpt-5.4":
		capability.Efforts = []ReasoningEffort{"none", "low", "medium", "high", "xhigh"}
		capability.DefaultEffort = "none"
	case "gpt-5.4-pro":
		capability.Efforts = []ReasoningEffort{"medium", "high", "xhigh"}
		capability.DefaultEffort = "medium"
	case "gpt-5.5":
		capability.Efforts = []ReasoningEffort{"none", "low", "medium", "high", "xhigh"}
		capability.DefaultEffort = "medium"
	case "gpt-5.5-pro":
		capability.Efforts = []ReasoningEffort{"medium", "high", "xhigh"}
		capability.DefaultEffort = "high"
	default:
		return ReasoningCapability{}, false
	}
	capability.Summary = true

	return capability, true
}

func anthropicReasoningCapability(id string) ReasoningCapability {
	var efforts []ReasoningEffort
	switch {
	case strings.Contains(id, "opus-4-7"):
		efforts = []ReasoningEffort{"low", "medium", "high", "xhigh", "max"}
	case strings.Contains(id, "opus-4-6"), strings.Contains(id, "sonnet-4-6"):
		efforts = []ReasoningEffort{"low", "medium", "high", "max"}
	case strings.Contains(id, "opus-4-5"):
		efforts = []ReasoningEffort{"low", "medium", "high"}
	default:
		return ReasoningCapability{}
	}

	return ReasoningCapability{
		Efforts:       efforts,
		DefaultEffort: "high",
	}
}

func getModelOwnerFromID(id string) string {
	idValue := str.String(id)
	id = strings.TrimPrefix(idValue.Trim(), "~")
	owner, _, ok := strings.Cut(id, "/")
	if !ok {
		return constants.ModelProviderOpenRouter
	}
	ownerValue := str.String(owner)
	return ownerValue.Trim()
}
