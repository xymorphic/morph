package provider

import (
	"fmt"
	"strings"

	"github.com/xymorphic/morph/internal/constants"
	"github.com/xymorphic/morph/pkg/str"
)

const (
	// APIOpenAICompletions identifies the OpenAI-compatible chat completions protocol.
	APIOpenAICompletions = "openai-completions"
	// APIOpenAIResponses identifies the OpenAI responses protocol.
	APIOpenAIResponses = "openai-responses"
	// APIOllamaNative identifies Ollama's native chat protocol.
	APIOllamaNative = "ollama-native"
	// APIOpenAIEmbeddings identifies the OpenAI-compatible embeddings protocol.
	APIOpenAIEmbeddings = "openai-embeddings"
	// APIOpenRouterEmbeddings identifies the OpenRouter embeddings protocol.
	APIOpenRouterEmbeddings = "openrouter-embeddings"
	// APIOllamaEmbeddings identifies Ollama's native embeddings protocol.
	APIOllamaEmbeddings = "ollama-embeddings"
	// APIAnthropicMessages identifies the Anthropic Messages protocol.
	APIAnthropicMessages = "anthropic-messages"
)

// InputKind describes an input modality supported by a model.
type InputKind string

const (
	// InputText identifies plain text input support.
	InputText InputKind = "text"
	// InputImage identifies image input support.
	InputImage InputKind = "image"
)

type ReasoningEffort string

type ReasoningCapability struct {
	Efforts       []ReasoningEffort
	DefaultEffort ReasoningEffort
	Summary       bool
}

type ModelKey struct {
	Provider string
	API      string
	Model    string
}

func CanonicalModelKey(providerID, apiID, modelID string) ModelKey {
	providerValue := str.String(providerID)
	apiValue := str.String(apiID)
	modelValue := str.String(modelID)

	return ModelKey{
		Provider: providerValue.Normalized(),
		API:      apiValue.Normalized(),
		Model:    modelValue.Trim(),
	}
}

// APIDefinition describes a request protocol adapter known to the model registry.
type APIDefinition struct {
	ID          string
	DisplayName string
}

// CapabilitySet describes model/provider capabilities that can be discovered or configured.
type CapabilitySet struct {
	Tools     bool
	Vision    bool
	Reasoning bool
}

// LocalProviderDefinition describes local-runtime capabilities without implying a specific client.
type LocalProviderDefinition struct {
	NativeChatAPI            string
	OpenAICompatibleChatAPIs []string
	EmbeddingsAPI            string
	AuthMarker               string
	Capabilities             CapabilitySet
}

// ProviderDefinition describes provider-level routing, defaults, and credential metadata.
type ProviderDefinition struct {
	ID                 string
	DisplayName        string
	DisplayIndex       int
	HasDisplayIndex    bool
	DefaultAPI         string
	BaseURLs           map[string]string
	Headers            map[string]string
	APIKeyEnv          []string
	SupportsModels     bool
	RequiresKnownModel bool
	SupportsAPIKey     bool
	SupportsOAuth      bool
	Local              *LocalProviderDefinition
}

// ModelDefinition describes provider-specific model metadata used for resolution and validation.
type ModelDefinition struct {
	ID                    string
	Name                  string
	Owner                 string
	Provider              string
	API                   string
	Input                 []InputKind
	Reasoning             bool
	ReasoningCapabilities ReasoningCapability
	SupportsTools         bool
	SupportsOAuth         bool
	DisplayDefault        bool
	ContextWindow         int
	MaxTokens             int
}

// Registry stores API, provider, and model definitions for model resolution.
type Registry struct {
	apis      map[string]APIDefinition
	providers map[string]ProviderDefinition
	models    map[ModelKey]ModelDefinition
	modelKeys map[string][]ModelKey
}

// NewRegistry builds a registry from API, provider, and model definitions.
func NewRegistry(
	apis []APIDefinition,
	providers []ProviderDefinition,
	models []ModelDefinition,
) *Registry {
	r := &Registry{
		apis:      make(map[string]APIDefinition, len(apis)),
		providers: make(map[string]ProviderDefinition, len(providers)),
		models:    make(map[ModelKey]ModelDefinition),
		modelKeys: make(map[string][]ModelKey),
	}

	for _, api := range apis {
		api.ID = normalizeID(api.ID)
		if api.ID == "" {
			continue
		}
		r.apis[api.ID] = api
	}

	for _, provider := range providers {
		provider.ID = normalizeID(provider.ID)
		provider.DefaultAPI = normalizeID(provider.DefaultAPI)
		if provider.ID == "" {
			continue
		}
		provider.BaseURLs = cloneStringMap(provider.BaseURLs)
		provider.Headers = cloneStringMap(provider.Headers)
		provider.APIKeyEnv = append([]string(nil), provider.APIKeyEnv...)
		provider.Local = cloneLocalProviderDefinition(provider.Local)
		r.providers[provider.ID] = provider
	}

	for _, model := range models {
		key := CanonicalModelKey(model.Provider, model.API, model.ID)
		model.ID = key.Model
		model.Owner = normalizeID(model.Owner)
		model.Provider = key.Provider
		model.API = key.API
		if key.Provider == "" || key.API == "" || key.Model == "" {
			continue
		}
		capability, err := NormalizeReasoningCapability(model.Reasoning, model.ReasoningCapabilities)
		if err != nil {
			panic(fmt.Sprintf("invalid reasoning capability for %s/%s/%s: %v", key.Provider, key.API, key.Model, err))
		}
		model.ReasoningCapabilities = capability
		model.Input = append([]InputKind(nil), model.Input...)
		if _, exists := r.models[key]; !exists {
			r.modelKeys[key.Provider] = append(r.modelKeys[key.Provider], key)
		}
		r.models[key] = model
	}

	return r
}

// DefaultRegistry returns the built-in provider registry.
func DefaultRegistry() *Registry {
	return NewRegistry(defaultAPIs(), defaultProviders(), defaultModels())
}

// GetAPI looks up an API definition by ID.
func (r *Registry) GetAPI(id string) (APIDefinition, bool) {
	if r == nil {
		return APIDefinition{}, false
	}

	api, ok := r.apis[normalizeID(id)]
	return api, ok
}

// GetProvider looks up a provider definition by ID.
func (r *Registry) GetProvider(id string) (ProviderDefinition, bool) {
	if r == nil {
		return ProviderDefinition{}, false
	}

	provider, ok := r.providers[normalizeID(id)]
	if !ok {
		return ProviderDefinition{}, false
	}

	provider.BaseURLs = cloneStringMap(provider.BaseURLs)
	provider.Headers = cloneStringMap(provider.Headers)
	provider.APIKeyEnv = append([]string(nil), provider.APIKeyEnv...)
	provider.Local = cloneLocalProviderDefinition(provider.Local)
	return provider, true
}

// GetProviders returns all provider definitions registered in the registry.
func (r *Registry) GetProviders() []ProviderDefinition {
	if r == nil {
		return nil
	}

	providers := make([]ProviderDefinition, 0, len(r.providers))
	for _, provider := range r.providers {
		provider.BaseURLs = cloneStringMap(provider.BaseURLs)
		provider.Headers = cloneStringMap(provider.Headers)
		provider.APIKeyEnv = append([]string(nil), provider.APIKeyEnv...)
		provider.Local = cloneLocalProviderDefinition(provider.Local)
		providers = append(providers, provider)
	}

	return providers
}

func (r *Registry) GetModel(providerID, modelID string) (ModelDefinition, bool) {
	if r == nil {
		return ModelDefinition{}, false
	}

	providerID = normalizeID(providerID)
	modelIDValue := str.String(modelID)
	modelID = modelIDValue.Trim()
	if providerID == "" || modelID == "" {
		return ModelDefinition{}, false
	}

	if provider, ok := r.providers[providerID]; ok && provider.DefaultAPI != "" {
		if model, ok := r.models[CanonicalModelKey(providerID, provider.DefaultAPI, modelID)]; ok {
			return cloneModelDefinition(model), true
		}
	}

	var found ModelDefinition
	for _, key := range r.modelKeys[providerID] {
		if key.Model != modelID {
			continue
		}
		if found.ID != "" {
			return ModelDefinition{}, false
		}
		found = r.models[key]
	}

	return cloneModelDefinition(found), found.ID != ""
}

func (r *Registry) GetModelForAPI(providerID, apiID, modelID string) (ModelDefinition, bool) {
	if r == nil {
		return ModelDefinition{}, false
	}

	key := CanonicalModelKey(providerID, apiID, modelID)
	if key.Provider == "" || key.API == "" || key.Model == "" {
		return ModelDefinition{}, false
	}
	model, ok := r.models[key]
	if !ok {
		return ModelDefinition{}, false
	}

	return cloneModelDefinition(model), true
}

func (r *Registry) GetModelByKey(key ModelKey) (ModelDefinition, bool) {
	return r.GetModelForAPI(key.Provider, key.API, key.Model)
}

// GetModels returns model definitions registered for a provider.
func (r *Registry) GetModels(providerID string) []ModelDefinition {
	if r == nil {
		return nil
	}

	keys := r.modelKeys[normalizeID(providerID)]
	if len(keys) == 0 {
		return nil
	}

	models := make([]ModelDefinition, 0, len(keys))
	for _, key := range keys {
		models = append(models, cloneModelDefinition(r.models[key]))
	}

	return models
}

func NormalizeReasoningCapability(
	reasoning bool,
	capability ReasoningCapability,
) (ReasoningCapability, error) {
	normalized := ReasoningCapability{
		Efforts: make([]ReasoningEffort, 0, len(capability.Efforts)),
		Summary: capability.Summary,
	}
	seen := make(map[string]struct{}, len(capability.Efforts))
	for _, effort := range capability.Efforts {
		value := strings.TrimSpace(string(effort))
		if value == "" {
			return ReasoningCapability{}, fmt.Errorf("effort values must not be blank")
		}
		key := strings.ToLower(value)
		if key == "default" || key == "reset" {
			return ReasoningCapability{}, fmt.Errorf("effort value %q is reserved", value)
		}
		if _, ok := seen[key]; ok {
			return ReasoningCapability{}, fmt.Errorf("effort value %q is duplicated", value)
		}
		seen[key] = struct{}{}
		normalized.Efforts = append(normalized.Efforts, ReasoningEffort(value))
	}

	defaultValue := strings.TrimSpace(string(capability.DefaultEffort))
	if defaultValue != "" {
		for _, effort := range normalized.Efforts {
			if strings.EqualFold(string(effort), defaultValue) {
				normalized.DefaultEffort = effort
				break
			}
		}
		if normalized.DefaultEffort == "" {
			return ReasoningCapability{}, fmt.Errorf("default effort %q is not supported", defaultValue)
		}
	}

	if !reasoning && (len(normalized.Efforts) > 0 || normalized.DefaultEffort != "" || normalized.Summary) {
		return ReasoningCapability{}, fmt.Errorf("non-reasoning models cannot advertise efforts or summaries")
	}
	if len(normalized.Efforts) > 0 && normalized.DefaultEffort == "" {
		return ReasoningCapability{}, fmt.Errorf("adjustable reasoning models require a default effort")
	}
	if len(normalized.Efforts) == 0 && normalized.DefaultEffort != "" {
		return ReasoningCapability{}, fmt.Errorf("default effort requires supported efforts")
	}
	if len(normalized.Efforts) == 0 {
		normalized.Efforts = nil
	}

	return normalized, nil
}

func cloneModelDefinition(model ModelDefinition) ModelDefinition {
	model.Input = append([]InputKind(nil), model.Input...)
	model.ReasoningCapabilities.Efforts = append(
		[]ReasoningEffort(nil),
		model.ReasoningCapabilities.Efforts...,
	)
	return model
}

// GetProviderIDs returns the provider IDs registered in the registry.
func (r *Registry) GetProviderIDs() []string {
	if r == nil {
		return nil
	}

	ids := make([]string, 0, len(r.providers))
	for id := range r.providers {
		ids = append(ids, id)
	}

	return ids
}

// GetAPIIDs returns the API IDs registered in the registry.
func (r *Registry) GetAPIIDs() []string {
	if r == nil {
		return nil
	}

	ids := make([]string, 0, len(r.apis))
	for id := range r.apis {
		ids = append(ids, id)
	}

	return ids
}

// GetBaseURL returns the provider's base URL for an API ID.
func (r *Registry) GetBaseURL(providerID, apiID string) string {
	provider, ok := r.GetProvider(providerID)
	if !ok {
		return ""
	}

	apiID = normalizeID(apiID)
	if apiID == "" {
		apiID = provider.DefaultAPI
	}
	baseURLsValue := str.String(provider.BaseURLs[apiID])
	return baseURLsValue.Trim()
}

// SupportsProviderAPI reports whether the provider can use the given API.
func (r *Registry) SupportsProviderAPI(providerID, apiID string) bool {
	provider, ok := r.GetProvider(providerID)
	if !ok {
		return false
	}

	apiID = normalizeID(apiID)
	if apiID == "" {
		apiID = provider.DefaultAPI
	}
	if apiID == "" {
		return false
	}

	if provider.DefaultAPI == apiID {
		return true
	}
	baseURLsValue2 := str.String(provider.BaseURLs[apiID])
	return baseURLsValue2.Trim() != ""
}

func normalizeID(value string) string {
	valueText := str.String(value)
	return valueText.Normalized()
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}

	cloned := make(map[string]string, len(values))
	for key, value := range values {
		key = normalizeID(key)
		value2 := str.String(value)
		value = value2.Trim()
		if key == "" || value == "" {
			continue
		}
		cloned[key] = value
	}
	if len(cloned) == 0 {
		return nil
	}

	return cloned
}

func cloneLocalProviderDefinition(value *LocalProviderDefinition) *LocalProviderDefinition {
	if value == nil {
		return nil
	}

	cloned := *value
	cloned.NativeChatAPI = normalizeID(cloned.NativeChatAPI)
	cloned.EmbeddingsAPI = normalizeID(cloned.EmbeddingsAPI)
	authMarkerValue := str.String(cloned.AuthMarker)
	cloned.AuthMarker = authMarkerValue.Trim()
	cloned.OpenAICompatibleChatAPIs = normalizeIDList(cloned.OpenAICompatibleChatAPIs)

	return &cloned
}

func normalizeIDList(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = normalizeID(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	if len(normalized) == 0 {
		return nil
	}

	return normalized
}

func defaultAPIs() []APIDefinition {
	return []APIDefinition{
		{
			ID:          APIOpenAICompletions,
			DisplayName: "OpenAI Chat Completions",
		},
		{
			ID:          APIOpenAIResponses,
			DisplayName: "OpenAI Responses",
		},
		{
			ID:          APIOllamaNative,
			DisplayName: "Ollama Native API",
		},
		{
			ID:          APIOpenAIEmbeddings,
			DisplayName: "OpenAI Embeddings",
		},
		{
			ID:          APIOpenRouterEmbeddings,
			DisplayName: "OpenRouter Embeddings",
		},
		{
			ID:          APIOllamaEmbeddings,
			DisplayName: "Ollama Embeddings",
		},
		{
			ID:          APIAnthropicMessages,
			DisplayName: "Anthropic Messages",
		},
	}
}

func defaultProviders() []ProviderDefinition {
	return []ProviderDefinition{
		{
			ID:              constants.ModelProviderOpenRouter,
			DisplayName:     "OpenRouter",
			DisplayIndex:    3,
			HasDisplayIndex: true,
			DefaultAPI:      APIOpenAIResponses,
			APIKeyEnv:       []string{"OPENROUTER_API_KEY"},
			SupportsModels:  true,
			SupportsAPIKey:  true,
			BaseURLs: map[string]string{
				APIOpenAICompletions:    constants.DefaultOpenRouterBaseURL,
				APIOpenAIResponses:      constants.DefaultOpenRouterResponsesBaseURL,
				APIOpenRouterEmbeddings: constants.DefaultOpenRouterEmbeddingsBaseURL,
			},
		},
		{
			ID:              constants.ModelProviderOpenAI,
			DisplayName:     "OpenAI",
			DisplayIndex:    0,
			HasDisplayIndex: true,
			DefaultAPI:      APIOpenAIResponses,
			APIKeyEnv:       []string{"OPENAI_API_KEY"},
			SupportsModels:  true,
			SupportsAPIKey:  true,
			BaseURLs: map[string]string{
				APIOpenAICompletions: constants.DefaultOpenAIBaseURL,
				APIOpenAIResponses:   constants.DefaultOpenAIBaseURL,
				APIOpenAIEmbeddings:  constants.DefaultOpenAIEmbeddingsBaseURL,
			},
		},
		{
			ID:              constants.ModelProviderOpenAICodex,
			DisplayName:     "OpenAI Codex",
			DisplayIndex:    0,
			HasDisplayIndex: true,
			DefaultAPI:      APIOpenAIResponses,
			SupportsModels:  true,
			SupportsOAuth:   true,
			BaseURLs: map[string]string{
				APIOpenAIResponses: constants.DefaultOpenAISubscriptionBaseURL,
			},
		},
		{
			ID:              constants.ModelProviderAnthropic,
			DisplayName:     "Anthropic",
			DisplayIndex:    1,
			HasDisplayIndex: true,
			DefaultAPI:      APIAnthropicMessages,
			APIKeyEnv:       []string{"ANTHROPIC_API_KEY"},
			SupportsModels:  true,
			SupportsAPIKey:  true,
			SupportsOAuth:   true,
			BaseURLs: map[string]string{
				APIAnthropicMessages: constants.DefaultAnthropicBaseURL,
			},
		},
		{
			ID:              constants.ModelProviderGitHubCopilot,
			DisplayName:     "GitHub Copilot",
			DisplayIndex:    2,
			HasDisplayIndex: true,
			DefaultAPI:      APIOpenAIResponses,
			APIKeyEnv:       []string{"COPILOT_GITHUB_TOKEN"},
			SupportsModels:  true,
			SupportsAPIKey:  true,
			SupportsOAuth:   true,
			BaseURLs: map[string]string{
				APIOpenAICompletions: constants.DefaultGitHubCopilotBaseURL,
				APIOpenAIResponses:   constants.DefaultGitHubCopilotBaseURL,
				APIAnthropicMessages: constants.DefaultGitHubCopilotBaseURL,
			},
		},
		{
			ID:              constants.ModelProviderOllama,
			DisplayName:     "Ollama",
			DisplayIndex:    4,
			HasDisplayIndex: true,
			DefaultAPI:      APIOllamaNative,
			SupportsModels:  true,
			BaseURLs: map[string]string{
				APIOpenAICompletions: constants.DefaultOllamaBaseURL + "/v1",
				APIOllamaNative:      constants.DefaultOllamaBaseURL,
				APIOllamaEmbeddings:  constants.DefaultOllamaBaseURL,
			},
			Local: &LocalProviderDefinition{
				NativeChatAPI: APIOllamaNative,
				OpenAICompatibleChatAPIs: []string{
					APIOpenAICompletions,
				},
				EmbeddingsAPI: APIOllamaEmbeddings,
				AuthMarker:    constants.OllamaLocalAuthMarker,
				Capabilities:  CapabilitySet{Tools: true, Vision: true},
			},
		},
	}
}
