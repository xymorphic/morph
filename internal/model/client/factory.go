package client

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/openai/openai-go/v3/option"

	"github.com/xymorphic/morph/internal/constants"
	models "github.com/xymorphic/morph/internal/model"
	modelprovider "github.com/xymorphic/morph/internal/model/provider"
	provider_anthropic "github.com/xymorphic/morph/internal/model/provider_anthropic"
	_ "github.com/xymorphic/morph/internal/model/provider_copilot"
	provider_ollama "github.com/xymorphic/morph/internal/model/provider_ollama"
	provider_openai "github.com/xymorphic/morph/internal/model/provider_openai"
	"github.com/xymorphic/morph/pkg/str"
)

// ModelRole identifies a model-consuming runtime role.
type ModelRole string

const (
	// ModelRoleMain identifies the normal turn model role.
	ModelRoleMain ModelRole = "main"
	// ModelRoleSummary identifies the summary and compaction model role.
	ModelRoleSummary ModelRole = "summary"
	// ModelRoleReranker identifies the LLM reranker model role.
	ModelRoleReranker ModelRole = "reranker"
	// ModelRoleEmbedding identifies the embedding model role.
	ModelRoleEmbedding ModelRole = "embedding"
)

// ClientRequest describes the resolved provider data needed to construct a model client.
type ClientRequest struct {
	Role       ModelRole
	Model      string
	Provider   string
	API        string
	APIKey     string
	BaseURL    string
	Headers    map[string]string
	MaxRetries int
}

// ResolvedClientRequest is a client request after registry-backed provider and API resolution.
type ResolvedClientRequest struct {
	Role       ModelRole
	Model      modelprovider.ModelDefinition
	ModelKnown bool
	Provider   modelprovider.ProviderDefinition
	API        modelprovider.APIDefinition
	APIKey     string
	BaseURL    string
	Headers    map[string]string
	MaxRetries int
}

// OpenAIClientBuilder constructs an OpenAI-compatible model client for one provider/API route.
type OpenAIClientBuilder func(string, string, string, *modelprovider.Registry, ...option.RequestOption) (models.Client, error)

// AnthropicClientBuilder constructs an Anthropic Messages model client.
type AnthropicClientBuilder func(string, ...anthropicoption.RequestOption) (models.Client, error)

// OllamaClientBuilder constructs a native Ollama model client.
type OllamaClientBuilder func(string, map[string]string) (models.Client, error)

// ClientFactory constructs model clients from registry-backed provider definitions.
type ClientFactory struct {
	Registry        *modelprovider.Registry
	OpenAIClient    OpenAIClientBuilder
	AnthropicClient AnthropicClientBuilder
	OllamaClient    OllamaClientBuilder
	mu              sync.Mutex
	clients         map[string]models.Client
}

// NewClientFactory returns a model client factory backed by registry.
func NewClientFactory(registry *modelprovider.Registry) *ClientFactory {
	if registry == nil {
		registry = modelprovider.DefaultRegistry()
	}

	return &ClientFactory{
		Registry:        registry,
		OpenAIClient:    newOpenAIClient,
		AnthropicClient: newAnthropicClient,
		OllamaClient:    newOllamaClient,
		clients:         make(map[string]models.Client),
	}
}

// NewDefaultClientFactory returns a model client factory backed by the built-in registry.
func NewDefaultClientFactory() *ClientFactory {
	return NewClientFactory(modelprovider.DefaultRegistry())
}

// Resolve resolves a client request into provider, API, endpoint, and retry settings.
func (f *ClientFactory) Resolve(req ClientRequest) (ResolvedClientRequest, error) {
	registry := f.registry()

	providerID := normalizeID(req.Provider)
	if providerID == "" {
		return ResolvedClientRequest{}, errors.New("model provider is required")
	}

	provider, ok := registry.GetProvider(providerID)
	if !ok {
		return ResolvedClientRequest{}, fmt.Errorf("model provider %q is not registered", providerID)
	}

	apiID := normalizeID(req.API)
	if apiID == "" {
		apiID = provider.DefaultAPI
	}
	api, ok := registry.GetAPI(apiID)
	if !ok {
		return ResolvedClientRequest{}, fmt.Errorf("model API %q is not registered", apiID)
	}
	baseURLValue := str.String(req.BaseURL)
	baseURL := baseURLValue.Trim()
	if baseURL == "" {
		baseURL = registry.GetBaseURL(provider.ID, api.ID)
	}
	if baseURL == "" {
		return ResolvedClientRequest{}, fmt.Errorf("model base URL is required for provider %q API %q",
			provider.ID, api.ID)
	}
	modelValue := str.String(req.Model)
	modelID := modelValue.Trim()
	model, modelKnown := registry.GetModelForAPI(provider.ID, api.ID, modelID)
	if modelID != "" && !modelKnown {
		model = modelprovider.ModelDefinition{
			ID:       modelID,
			Provider: provider.ID,
			API:      api.ID,
		}
	}
	aPIKeyValue := str.String(req.APIKey)
	return ResolvedClientRequest{
		Role:       req.Role,
		Model:      model,
		ModelKnown: modelKnown,
		Provider:   provider,
		API:        api,
		APIKey:     aPIKeyValue.Trim(),
		BaseURL:    baseURL,
		Headers:    mergeHeaders(provider.Headers, req.Headers),
		MaxRetries: req.MaxRetries,
	}, nil
}

// NewClient constructs a model client for a resolved provider/API request.
func (f *ClientFactory) NewClient(req ClientRequest) (models.Client, error) {
	resolved, err := f.Resolve(req)
	if err != nil {
		return nil, err
	}

	switch resolved.API.ID {
	case modelprovider.APIOpenAICompletions, modelprovider.APIOpenAIResponses:
		return f.cachedClient(resolved)
	case modelprovider.APIAnthropicMessages:
		return f.cachedClient(resolved)
	case modelprovider.APIOllamaNative:
		return f.cachedClient(resolved)
	default:
		return nil, fmt.Errorf("model API %q is not supported for chat clients", resolved.API.ID)
	}
}

func (f *ClientFactory) registry() *modelprovider.Registry {
	if f == nil || f.Registry == nil {
		return modelprovider.DefaultRegistry()
	}

	return f.Registry
}

func (f *ClientFactory) cachedClient(req ResolvedClientRequest) (models.Client, error) {
	key := clientCacheKey(req)
	if f != nil {
		f.mu.Lock()
		if client := f.clients[key]; client != nil {
			f.mu.Unlock()
			return client, nil
		}
		f.mu.Unlock()
	}

	client, err := f.newClient(req)
	if err != nil {
		return nil, err
	}
	if f != nil {
		f.mu.Lock()
		if f.clients == nil {
			f.clients = make(map[string]models.Client)
		}
		f.clients[key] = client
		f.mu.Unlock()
	}

	return client, nil
}

func (f *ClientFactory) newClient(req ResolvedClientRequest) (models.Client, error) {
	switch req.API.ID {
	case modelprovider.APIOpenAICompletions, modelprovider.APIOpenAIResponses:
		return f.newOpenAIClient(req)
	case modelprovider.APIAnthropicMessages:
		return f.newAnthropicClient(req)
	case modelprovider.APIOllamaNative:
		return f.newOllamaClient(req)
	default:
		return nil, fmt.Errorf("model API %q is not supported for chat clients", req.API.ID)
	}
}

func (f *ClientFactory) newOpenAIClient(req ResolvedClientRequest) (models.Client, error) {
	builder := newOpenAIClient
	if f != nil && f.OpenAIClient != nil {
		builder = f.OpenAIClient
	}

	opts := []option.RequestOption{
		option.WithBaseURL(req.BaseURL),
		option.WithMaxRetries(req.MaxRetries),
	}
	for _, key := range sortedHeaderKeys(req.Headers) {
		opts = append(opts, option.WithHeader(key, req.Headers[key]))
	}

	client, err := builder(req.APIKey, req.API.ID, req.Provider.ID, f.registry(), opts...)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errors.New("model client is required")
	}
	if openAIClient, ok := client.(*provider_openai.OpenAIClient); ok {
		openAIClient.SetForceResponsesStream(isOpenAISubscriptionBaseURL(req.BaseURL))
	}

	return client, nil
}

func (f *ClientFactory) newAnthropicClient(req ResolvedClientRequest) (models.Client, error) {
	builder := newAnthropicClient
	if f != nil && f.AnthropicClient != nil {
		builder = f.AnthropicClient
	}

	opts := []anthropicoption.RequestOption{
		anthropicoption.WithBaseURL(req.BaseURL),
		anthropicoption.WithMaxRetries(req.MaxRetries),
	}
	for _, key := range sortedHeaderKeys(req.Headers) {
		opts = append(opts, anthropicoption.WithHeader(key, req.Headers[key]))
	}

	apiKey := req.APIKey
	if hasHeader(req.Headers, "Authorization") {
		apiKey = ""
	}

	client, err := builder(apiKey, opts...)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errors.New("model client is required")
	}
	if anthropicClient, ok := client.(*provider_anthropic.AnthropicClient); ok {
		anthropicClient.SetSubscriptionAuth(
			req.Provider.ID == constants.ModelProviderAnthropic &&
				hasHeader(req.Headers, "Authorization"),
		)
	}

	return client, nil
}

func (f *ClientFactory) newOllamaClient(req ResolvedClientRequest) (models.Client, error) {
	builder := newOllamaClient
	if f != nil && f.OllamaClient != nil {
		builder = f.OllamaClient
	}

	client, err := builder(req.BaseURL, req.Headers)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errors.New("model client is required")
	}

	return client, nil
}

func clientCacheKey(req ResolvedClientRequest) string {
	parts := []string{
		req.Provider.ID,
		req.API.ID,
		req.BaseURL,
		req.APIKey,
		fmt.Sprintf("%d", req.MaxRetries),
	}
	for _, key := range sortedHeaderKeys(req.Headers) {
		parts = append(parts, key, req.Headers[key])
	}

	return strings.Join(parts, "\x00")
}

func isOpenAISubscriptionBaseURL(baseURL string) bool {
	baseURLValue2 := str.String(baseURL)
	return strings.TrimRight(baseURLValue2.Trim(), "/") ==
		strings.TrimRight(constants.DefaultOpenAISubscriptionBaseURL, "/")
}

func hasHeader(headers map[string]string, name string) bool {
	nameValue := str.String(name)
	name = nameValue.Normalized()
	if name == "" {
		return false
	}

	for key, value := range headers {
		keyValue := str.String(key)
		valueText := str.String(value)
		if keyValue.Normalized() == name && valueText.Trim() != "" {
			return true
		}
	}

	return false
}

func mergeHeaders(values ...map[string]string) map[string]string {
	merged := make(map[string]string)
	for _, headers := range values {
		for key, value := range headers {
			keyValue2 := str.String(key)
			key = keyValue2.Trim()
			value2 := str.String(value)
			value = value2.Trim()
			if key == "" || value == "" {
				continue
			}
			merged[key] = value
		}
	}
	if len(merged) == 0 {
		return nil
	}

	return merged
}

func sortedHeaderKeys(headers map[string]string) []string {
	if len(headers) == 0 {
		return nil
	}

	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	return keys
}

func normalizeID(value string) string {
	value3 := str.String(value)
	return value3.Normalized()
}

func newOpenAIClient(
	apiKey string,
	api string,
	provider string,
	registry *modelprovider.Registry,
	opts ...option.RequestOption,
) (models.Client, error) {
	return provider_openai.NewOpenAIProviderClient(apiKey, api, provider, registry, opts...)
}

func newAnthropicClient(apiKey string, opts ...anthropicoption.RequestOption) (models.Client, error) {
	return provider_anthropic.NewAnthropicClient(apiKey, opts...)
}

func newOllamaClient(baseURL string, headers map[string]string) (models.Client, error) {
	return provider_ollama.NewOllamaClient(baseURL, headers)
}
