package config

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/wandxy/morph/internal/constants"
	appcredential "github.com/wandxy/morph/internal/credential"
	modelprovider "github.com/wandxy/morph/internal/model/provider"
	"github.com/wandxy/morph/pkg/str"
)

func getDefaultBaseURLForProvider(provider, apiID string) string {
	apiIDValue := str.String(apiID)
	if apiIDValue.Trim() == "" {
		return modelRegistry.GetBaseURL(provider, "")
	}

	api, ok := getModelAPI(apiID)
	if !ok {
		return ""
	}

	return modelRegistry.GetBaseURL(provider, api.ID)
}

func getDefaultAPIForProvider(provider string) string {
	definition, ok := modelRegistry.GetProvider(provider)
	if !ok {
		return ""
	}

	return definition.DefaultAPI
}

func getModelAPI(apiID string) (modelprovider.APIDefinition, bool) {
	apiIDValue2 := str.String(apiID)
	apiID = apiIDValue2.Normalized()
	if apiID == "" {
		return modelprovider.APIDefinition{}, false
	}

	return modelRegistry.GetAPI(apiID)
}

func getModelAPIID(apiID string) string {
	api, ok := getModelAPI(apiID)
	if !ok {
		return ""
	}

	return api.ID
}

func (c *Config) getProviderConfig(provider string) ProviderModelConfig {
	if c == nil {
		return ProviderModelConfig{}
	}

	providerValue := str.String(provider)
	provider = providerValue.Normalized()
	if provider == "" || len(c.Models.Providers) == 0 {
		return ProviderModelConfig{}
	}

	return c.Models.Providers[provider]
}

func (c *Config) getProviderAPIConfig(provider string) string {
	aPIValue := str.String(c.getProviderConfig(provider).API)
	return aPIValue.Normalized()
}

func (c *Config) getProviderBaseURLConfig(provider string) string {
	baseURLValue := str.String(c.getProviderConfig(provider).BaseURL)
	return baseURLValue.Trim()
}

func (c *Config) getProviderHeadersConfig(provider string) map[string]string {
	return normalizeStringMap(c.getProviderConfig(provider).Headers)
}

func hasModelProvider(provider string) bool {
	_, ok := modelRegistry.GetProvider(provider)
	return ok
}

func getModelProviderList() string {
	ids := modelRegistry.GetProviderIDs()
	sort.Strings(ids)

	return strings.Join(ids, ", ")
}

func getModelAPIList(allowed map[string]struct{}) string {
	ids := modelRegistry.GetAPIIDs()
	if len(allowed) != 0 {
		ids = ids[:0]
		for id := range allowed {
			if _, ok := modelRegistry.GetAPI(id); ok {
				ids = append(ids, id)
			}
		}
	}
	sort.Strings(ids)

	return strings.Join(ids, ", ")
}

func (c *Config) StreamEnabled() bool {
	if c == nil {
		return true
	}

	return getBoolValueDefault(c.Models.Main.Stream, true)
}

func (c *Config) InputSafetyEnabled() bool {
	if c == nil {
		return constants.DefaultSafetyInputEnabled
	}

	c.normalizeFields()
	return getBoolValueDefault(c.Safety.Input, constants.DefaultSafetyInputEnabled)
}

func (c *Config) OutputSafetyEnabled() bool {
	if c == nil {
		return constants.DefaultSafetyOutputEnabled
	}

	c.normalizeFields()
	return getBoolValueDefault(c.Safety.Output, constants.DefaultSafetyOutputEnabled)
}

func (c *Config) OutputPIIRedactionEnabled() bool {
	if c == nil {
		return constants.DefaultSafetyPIIEnabled
	}

	c.normalizeFields()
	return getBoolValueDefault(c.Safety.PII, constants.DefaultSafetyPIIEnabled)
}

func (c *Config) RetainUnsafeEnabled() bool {
	return c != nil && c.Dev.RetainUnsafe
}

func (c *Config) TUIThinkingComposerEnabled() bool {
	if c == nil {
		return constants.DefaultTUIThinkingComposerEnabled
	}

	c.normalizeFields()
	return getBoolValueDefault(c.TUI.ThinkingComposer, constants.DefaultTUIThinkingComposerEnabled)
}

func (c *Config) ModelMaxRetriesEffective() int {
	if c == nil {
		return constants.DefaultModelMaxRetries
	}

	c.normalizeFields()
	return *c.Models.MaxRetries
}

func (c *Config) SummaryModelEffective() string {
	if c == nil {
		return ""
	}

	c.normalizeFields()
	if c.Models.Summary.Name != "" {
		return c.Models.Summary.Name
	}

	return c.Models.Main.Name
}

func (c *Config) SummaryProviderEffective() string {
	if c == nil {
		return ""
	}

	c.normalizeFields()
	if c.Models.Summary.Provider != "" {
		return c.Models.Summary.Provider
	}

	return c.Models.Main.Provider
}

// MainModelAPIEffective returns the registry API ID for the main model.
func (c *Config) MainModelAPIEffective() string {
	if c == nil {
		return ""
	}

	c.normalizeFields()
	return getModelAPIID(c.Models.Main.API)
}

// SummaryModelAPIEffective returns the registry API ID for the summary model.
func (c *Config) SummaryModelAPIEffective() string {
	if c == nil {
		return ""
	}

	c.normalizeFields()
	if c.Models.Summary.API != "" {
		return getModelAPIID(c.Models.Summary.API)
	}
	if provider := c.SummaryProviderEffective(); provider != "" && provider != c.Models.Main.Provider {
		if api := c.getProviderAPIConfig(provider); api != "" {
			return getModelAPIID(api)
		}

		return getModelAPIID(getDefaultAPIForProvider(provider))
	}

	return getModelAPIID(c.Models.Main.API)
}

func (c *Config) RerankerEffective() string {
	if c == nil {
		return constants.RerankerDeterministic
	}

	c.normalizeFields()
	if c.Reranker.Type != "" {
		return c.Reranker.Type
	}

	return constants.RerankerDeterministic
}

func (c *Config) MemoryEnabled() bool {
	if c == nil {
		return false
	}

	c.normalizeFields()
	return getBoolValueDefault(c.Memory.Enabled, true)
}

func (c *Config) MemoryBackendEffective() string {
	if c == nil {
		return ""
	}

	c.normalizeFields()
	if c.Memory.Backend != "" {
		return c.Memory.Backend
	}

	return c.Storage.Backend
}

func (c *Config) MemoryPinnedEnabled() bool {
	if c == nil {
		return false
	}

	c.normalizeFields()
	return getBoolValueDefault(c.Memory.Pinned.Enabled, constants.DefaultProfileMemoryPinnedEnabled)
}

func (c *Config) MemoryRetrievalEnabled() bool {
	if c == nil {
		return false
	}

	c.normalizeFields()
	return getBoolValueDefault(c.Memory.Retrieval.Enabled, true)
}

func (c *Config) MemoryFlushEnabled() bool {
	if c == nil {
		return false
	}

	c.normalizeFields()
	return getBoolValueDefault(c.Memory.Flush.Enabled, true)
}

func (c *Config) MemoryEpisodicEnabled() bool {
	if c == nil {
		return false
	}

	c.normalizeFields()
	return getBoolValueDefault(c.Memory.Episodic.Enabled, constants.DefaultMemoryEpisodicEnabled)
}

func (c *Config) MemoryReflectionEnabled() bool {
	if c == nil {
		return false
	}

	c.normalizeFields()
	return getBoolValueDefault(c.Memory.Reflection.Enabled, constants.DefaultMemoryReflectionEnabled)
}

func (c *Config) MemoryPromotionEnabled() bool {
	if c == nil {
		return false
	}

	c.normalizeFields()
	return getBoolValueDefault(c.Memory.Promotion.Enabled, constants.DefaultProfileMemoryPromotionEnabled)
}

func (c *Config) MemoryWriteEnabled() bool {
	if c == nil {
		return false
	}

	c.normalizeFields()
	return getBoolValueDefault(c.Memory.Write.Enabled, true)
}

func (c *Config) RerankerModelEffective() string {
	if c == nil {
		return ""
	}

	c.normalizeFields()
	if c.Reranker.Model != "" {
		return c.Reranker.Model
	}

	return c.SummaryModelEffective()
}

func (c *Config) RerankerProviderEffective() string {
	if c == nil {
		return ""
	}

	c.normalizeFields()
	return c.SummaryProviderEffective()
}

func (c *Config) RerankerModelAPIEffective() string {
	if c == nil {
		return ""
	}

	c.normalizeFields()
	return c.RerankerModelAPIEffectiveForModel(c.RerankerModelEffective())
}

func (c *Config) RerankerModelAPIEffectiveForModel(modelID string) string {
	if c == nil {
		return ""
	}

	c.normalizeFields()
	provider := c.RerankerProviderEffective()
	if model, ok := modelRegistry.GetModel(provider, modelID); ok {
		return getModelAPIID(model.API)
	}

	return c.SummaryModelAPIEffective()
}

func (c *Config) RerankerOverrideEffective(override RerankerOverrideConfig) RerankerEffectiveConfig {
	if c == nil {
		return RerankerEffectiveConfig{}
	}

	c.normalizeFields()
	trimmedValueValue := str.String(override.Type)
	rerankerType := trimmedValueValue.Normalized()
	if rerankerType == "" {
		rerankerType = c.RerankerEffective()
	}
	modelValue := str.String(override.Model)
	model := modelValue.Trim()
	if model == "" {
		model = c.RerankerModelEffective()
	}

	maxCandidates := c.Reranker.MaxCandidates
	maxCandidatesSet := maxCandidates != 0
	if override.MaxCandidates != nil {
		maxCandidates = *override.MaxCandidates
		maxCandidatesSet = true
	}

	maxCandidateTextChars := c.Reranker.MaxCandidateTextChars
	maxCandidateTextCharsSet := maxCandidateTextChars != 0
	if override.MaxCandidateTextChars != nil {
		maxCandidateTextChars = *override.MaxCandidateTextChars
		maxCandidateTextCharsSet = true
	}

	maxOutputTokens := c.Reranker.MaxOutputTokens
	if override.MaxOutputTokens != nil {
		maxOutputTokens = *override.MaxOutputTokens
	}

	return RerankerEffectiveConfig{
		Type:                     rerankerType,
		Model:                    model,
		MaxCandidates:            maxCandidates,
		MaxCandidatesSet:         maxCandidatesSet,
		MaxCandidateTextChars:    maxCandidateTextChars,
		MaxCandidateTextCharsSet: maxCandidateTextCharsSet,
		MaxOutputTokens:          maxOutputTokens,
	}
}

func (c *Config) summaryModelBaseURLEffective() string {
	main := c.Models.Main.Provider
	sum := c.SummaryProviderEffective()
	sumAPI := c.SummaryModelAPIEffective()
	mainAPI := c.MainModelAPIEffective()

	if sum == main && sumAPI == mainAPI {
		return c.Models.Main.BaseURL
	}
	baseURLValue2 := str.String(c.Models.Summary.BaseURL)
	if u := baseURLValue2.Trim(); u != "" {
		return u
	}
	if u := c.getProviderBaseURLConfig(sum); u != "" {
		return u
	}

	return getDefaultBaseURLForProvider(sum, sumAPI)
}

func (c *Config) summaryAPIKeyEffective() string {
	aPIKeyValue := str.String(c.Models.Summary.APIKey)
	if key := aPIKeyValue.Trim(); key != "" {
		return key
	}

	if c.SummaryProviderEffective() == c.Models.Main.Provider &&
		c.SummaryModelAPIEffective() == c.MainModelAPIEffective() {
		return c.Models.Main.APIKey
	}

	return ""
}

func (c *Config) rerankerModelBaseURLEffective() string {
	provider := c.RerankerProviderEffective()
	api := c.RerankerModelAPIEffective()

	if provider == c.SummaryProviderEffective() && api == c.SummaryModelAPIEffective() {
		return c.summaryModelBaseURLEffective()
	}
	if u := c.getProviderBaseURLConfig(provider); u != "" {
		return u
	}

	return getDefaultBaseURLForProvider(provider, api)
}

func (c *Config) rerankerAPIKeyEffective() string {
	if c.RerankerProviderEffective() == c.SummaryProviderEffective() &&
		c.RerankerModelAPIEffective() == c.SummaryModelAPIEffective() {
		return c.summaryAPIKeyEffective()
	}

	return ""
}

func (c *Config) ResolveSummaryModelAuth() (ModelAuth, error) {
	if c == nil {
		return ModelAuth{}, errors.New("config is required")
	}

	c.Normalize()

	auth := ModelAuth{
		Provider: c.SummaryProviderEffective(),
		API:      getModelAPIID(c.SummaryModelAPIEffective()),
		BaseURL:  c.summaryModelBaseURLEffective(),
	}

	credential, err := c.resolveCredentialForProvider(
		auth.Provider,
		c.summaryAPIKeyEffective(),
		true,
		"summary model",
		c.SummaryModelEffective(),
	)
	if err != nil {
		return ModelAuth{}, err
	}

	auth.APIKey = credential.Value
	auth.Headers = mergeModelAuthHeaders(c.getProviderHeadersConfig(auth.Provider), credential.Headers)
	auth.CredentialSource = credential.Source
	auth.applySubscriptionDefaults()
	aPIKeyValue2 := str.String(auth.APIKey)
	if aPIKeyValue2.Trim() == "" {
		return ModelAuth{}, newMissingModelCredentialError("model", auth.Provider)
	}

	return auth, nil
}

func (c *Config) ResolveRerankerModelAuth() (ModelAuth, error) {
	if c == nil {
		return ModelAuth{}, errors.New("config is required")
	}

	c.Normalize()

	auth := ModelAuth{
		Provider: c.RerankerProviderEffective(),
		API:      c.RerankerModelAPIEffective(),
		BaseURL:  c.rerankerModelBaseURLEffective(),
	}

	credential, err := c.resolveCredentialForProvider(
		auth.Provider,
		c.rerankerAPIKeyEffective(),
		true,
		"reranker model",
		c.RerankerModelEffective(),
	)
	if err != nil {
		return ModelAuth{}, err
	}
	auth.APIKey = credential.Value
	auth.Headers = mergeModelAuthHeaders(c.getProviderHeadersConfig(auth.Provider), credential.Headers)
	auth.CredentialSource = credential.Source
	auth.applySubscriptionDefaults()
	aPIKeyValue3 := str.String(auth.APIKey)
	if aPIKeyValue3.Trim() == "" {
		return ModelAuth{}, newMissingModelCredentialError("model", auth.Provider)
	}

	return auth, nil
}

// ModelAuthEqual reports whether two auth values describe the same provider, API, endpoint, and key.
func ModelAuthEqual(a, b ModelAuth) bool {
	providerValue2 := str.String(a.Provider)
	providerValue3 := str.String(b.Provider)
	aPIValue2 := str.String(a.API)
	aPIValue3 := str.String(b.API)
	baseURLValue3 := str.String(a.BaseURL)
	baseURLValue4 := str.String(b.BaseURL)
	aPIKeyValue4 := str.String(a.APIKey)
	aPIKeyValue5 := str.String(b.APIKey)
	return providerValue2.Normalized() == providerValue3.Normalized() && aPIValue2.
		Normalized() == aPIValue3.Normalized() && baseURLValue3.
		Trim() == baseURLValue4.Trim() && aPIKeyValue4.
		Trim() == aPIKeyValue5.Trim() &&
		stringMapsEqual(a.Headers, b.Headers)
}

func (c *Config) ResolveEmbeddingModelAuth() (ModelAuth, error) {
	if c == nil {
		return ModelAuth{}, errors.New("config is required")
	}

	c.Normalize()

	provider := c.ModelEmbeddingProviderEffective()
	if !hasModelProvider(provider) {
		return ModelAuth{}, fmt.Errorf("embedding provider must be one of: %s", getModelProviderList())
	}
	api := c.EmbeddingModelAPIEffective()
	baseURLValue5 := str.String(c.Models.Embedding.BaseURL)
	baseURL := baseURLValue5.Trim()
	if baseURL == "" {
		baseURL = c.getEmbeddingProviderRoleBaseURL(provider)
	}
	if baseURL == "" {
		baseURL = c.getProviderBaseURLConfig(provider)
	}
	if baseURL == "" {
		baseURL = getDefaultBaseURLForProvider(provider, api)
	}

	auth := ModelAuth{
		Provider: provider,
		API:      api,
		BaseURL:  baseURL,
	}
	credential, err := c.resolveCredentialForProvider(provider, c.Models.Embedding.APIKey, false, "", "")
	if err != nil {
		return ModelAuth{}, err
	}
	auth.APIKey = credential.Value
	auth.Headers = mergeModelAuthHeaders(c.getProviderHeadersConfig(auth.Provider), credential.Headers)
	auth.CredentialSource = credential.Source
	aPIKeyValue6 := str.String(auth.APIKey)
	if aPIKeyValue6.Trim() == "" {
		return ModelAuth{}, newMissingModelCredentialError("embedding", auth.Provider)
	}

	return auth, nil
}

func (c *Config) EmbeddingModelAPIEffective() string {
	if c == nil {
		return ""
	}

	c.normalizeFields()
	if c.Models.Embedding.API != "" {
		return getModelAPIID(c.Models.Embedding.API)
	}

	provider := c.ModelEmbeddingProviderEffective()
	if api := c.getProviderAPIConfig(provider); api != "" {
		if _, ok := modelEmbeddingAPIs()[api]; ok {
			return getModelAPIID(api)
		}
	}
	if model, ok := modelRegistry.GetModel(provider, c.Models.Embedding.Name); ok {
		if _, ok := modelEmbeddingAPIs()[model.API]; ok {
			return getModelAPIID(model.API)
		}
	}

	if modelRegistry.SupportsProviderAPI(provider, modelprovider.APIOpenRouterEmbeddings) {
		return modelprovider.APIOpenRouterEmbeddings
	}
	if modelRegistry.SupportsProviderAPI(provider, modelprovider.APIOpenAIEmbeddings) {
		return modelprovider.APIOpenAIEmbeddings
	}
	if modelRegistry.SupportsProviderAPI(provider, modelprovider.APIOllamaEmbeddings) {
		return modelprovider.APIOllamaEmbeddings
	}

	return ""
}

func (c *Config) ModelEmbeddingProviderEffective() string {
	if c == nil {
		return ""
	}

	c.normalizeFields()
	if c.Models.Embedding.Provider != "" {
		return c.Models.Embedding.Provider
	}

	return c.Models.Main.Provider
}

func (c *Config) getEmbeddingProviderRoleBaseURL(provider string) string {
	providerValue4 := str.String(provider)
	if c == nil || providerValue4.Normalized() != constants.ModelProviderOllama {
		return ""
	}
	if !strings.EqualFold(c.Models.Main.Provider, provider) {
		return ""
	}

	return normalizeOllamaEmbeddingBaseURL(c.Models.Main.BaseURL)
}

func normalizeOllamaEmbeddingBaseURL(value string) string {
	valueText := strings.TrimRight(str.String(value).Trim(), "/")
	if strings.HasSuffix(strings.ToLower(valueText), "/v1") {
		valueText = strings.TrimRight(valueText[:len(valueText)-len("/v1")], "/")
	}

	return valueText
}

func (c *Config) ResolveModelAuth() (ModelAuth, error) {
	if c == nil {
		return ModelAuth{}, errors.New("config is required")
	}

	c.Normalize()

	auth := ModelAuth{
		Provider: c.Models.Main.Provider,
		API:      getModelAPIID(c.Models.Main.API),
		BaseURL:  c.Models.Main.BaseURL,
	}

	credential, err := c.resolveCredentialForProvider(
		c.Models.Main.Provider,
		c.Models.Main.APIKey,
		true,
		"model",
		c.Models.Main.Name,
	)
	if err != nil {
		return ModelAuth{}, err
	}
	auth.APIKey = credential.Value
	auth.Headers = mergeModelAuthHeaders(c.getProviderHeadersConfig(auth.Provider), credential.Headers)
	auth.CredentialSource = credential.Source
	auth.applySubscriptionDefaults()
	aPIKeyValue7 := str.String(auth.APIKey)
	if aPIKeyValue7.Trim() == "" {
		return ModelAuth{}, newMissingModelCredentialError("model", auth.Provider)
	}

	return auth, nil
}

type resolvedModelCredential struct {
	Value   string
	Headers map[string]string
	Source  ModelCredentialSource
}

func (c *Config) resolveCredentialForProvider(
	provider string,
	roleAPIKey string,
	allowOAuth bool,
	oauthModelField string,
	oauthModelID string,
) (resolvedModelCredential, error) {
	providerValue5 := str.String(provider)
	provider = providerValue5.Normalized()
	roleAPIKeyValue := str.String(roleAPIKey)
	if value := roleAPIKeyValue.Trim(); value != "" {
		return resolvedModelCredential{
			Value:  value,
			Source: ModelCredentialSource{Kind: ModelCredentialSourceRoleConfig},
		}, nil
	}

	stored, err := loadStoredModelCredential(provider)
	if err != nil {
		return resolvedModelCredential{}, err
	}
	var oauthModelErr error
	trimmedValueValue2 := str.String(stored.Type)
	if trimmedValueValue2.Normalized() == appcredential.TypeOAuth && !allowOAuth {
		stored = StoredModelCredential{}
	}
	trimmedValueValue3 := str.String(stored.Type)
	if trimmedValueValue3.Normalized() == appcredential.TypeOAuth && allowOAuth {
		if err := checkOAuthModelSupported(oauthModelField, provider, oauthModelID); err != nil {
			oauthModelErr = err
			stored = StoredModelCredential{}
		}
	}
	if appcredential.IsExpired(stored) {
		refreshed, ok, err := refreshStoredModelCredential(provider)
		if err != nil {
			return resolvedModelCredential{}, err
		}
		if ok {
			stored = refreshed
		} else {
			stored = StoredModelCredential{}
		}
		trimmedValueValue4 := str.String(stored.Type)
		if trimmedValueValue4.Normalized() == appcredential.TypeOAuth && allowOAuth {
			if err := checkOAuthModelSupported(oauthModelField, provider, oauthModelID); err != nil {
				oauthModelErr = err
				stored = StoredModelCredential{}
			}
		}
	}
	if value := getStoredModelCredentialValue(stored); value != "" {
		headers, err := getStoredModelCredentialHeaders(provider, stored)
		if err != nil {
			return resolvedModelCredential{}, err
		}
		trimmedValueValue5 := str.String(stored.Type)
		return resolvedModelCredential{
			Value:   value,
			Headers: headers,
			Source: ModelCredentialSource{
				Kind:      ModelCredentialSourceTokenStore,
				Name:      provider,
				Type:      trimmedValueValue5.Normalized(),
				HasExpiry: stored.ExpiresAt != nil,
			},
		}, nil
	}

	if allowOAuth {
		if value, envName := getProviderOAuthEnvCredential(provider); value != "" {
			credential := StoredModelCredential{Type: appcredential.TypeOAuth, Token: value}
			if err := checkOAuthModelSupported(oauthModelField, provider, oauthModelID); err != nil {
				oauthModelErr = err
			} else {
				headers, err := getStoredModelCredentialHeaders(provider, credential)
				if err != nil {
					return resolvedModelCredential{}, err
				}

				return resolvedModelCredential{
					Value:   value,
					Headers: headers,
					Source: ModelCredentialSource{
						Kind: ModelCredentialSourceProviderEnv,
						Name: envName,
						Type: appcredential.TypeOAuth,
					},
				}, nil
			}
		}
	}

	providerConfig := c.Models.Providers[provider]
	if value, envName := getCredentialFromEnv(providerConfig.APIKeyEnv); value != "" {
		return resolvedModelCredential{
			Value:  value,
			Source: ModelCredentialSource{Kind: ModelCredentialSourceProviderEnv, Name: envName},
		}, nil
	}

	if providerDef, ok := modelRegistry.GetProvider(provider); ok {
		if value, envName := getCredentialFromEnv(providerDef.APIKeyEnv); value != "" {
			return resolvedModelCredential{
				Value:  value,
				Source: ModelCredentialSource{Kind: ModelCredentialSourceProviderEnv, Name: envName},
			}, nil
		}
	}
	aPIKeyValue8 := str.String(providerConfig.APIKey)
	if value := aPIKeyValue8.Trim(); value != "" {
		return resolvedModelCredential{
			Value:  value,
			Source: ModelCredentialSource{Kind: ModelCredentialSourceProviderConfig, Name: provider},
		}, nil
	}
	if marker := getLocalProviderAuthMarker(provider); marker != "" {
		return resolvedModelCredential{
			Value: marker,
			Source: ModelCredentialSource{
				Kind: ModelCredentialSourceLocalProvider,
				Name: provider,
			},
		}, nil
	}
	if oauthModelErr != nil {
		return resolvedModelCredential{}, oauthModelErr
	}

	return resolvedModelCredential{}, nil
}

func getLocalProviderAuthMarker(provider string) string {
	providerDef, ok := modelRegistry.GetProvider(provider)
	if !ok || providerDef.Local == nil {
		return ""
	}
	authMarkerValue := str.String(providerDef.Local.AuthMarker)
	if marker := authMarkerValue.Trim(); marker != "" {
		return marker
	}

	return constants.LocalProviderAuthMarker
}

func getStoredModelCredentialHeaders(
	provider string,
	credential StoredModelCredential,
) (map[string]string, error) {
	trimmedValueValue6 := str.String(credential.Type)
	if trimmedValueValue6.Normalized() != appcredential.TypeOAuth {
		return nil, nil
	}

	if getSubscriptionProvider == nil {
		return nil, nil
	}

	subscriptionProvider, ok := getSubscriptionProvider(provider)
	if !ok {
		return nil, nil
	}

	headers, err := subscriptionProvider.AuthHeaders(context.Background(), credential)
	if err != nil {
		return nil, err
	}

	return normalizeStringMap(headers), nil
}

func checkOAuthModelSupported(
	field string,
	provider string,
	modelID string,
) error {
	fieldValue := str.String(field)
	field = fieldValue.Trim()
	if field == "" {
		field = "model"
	}
	modelIDValue := str.String(modelID)
	modelID = modelIDValue.Trim()
	if modelID == "" {
		return nil
	}
	providerValue6 := str.String(provider)
	provider = providerValue6.Normalized()
	providerDef, ok := modelRegistry.GetProvider(provider)
	if !ok {
		return nil
	}
	if !providerDef.SupportsOAuth {
		return fmt.Errorf("%s %q is not available through OAuth for provider %q", field, modelID, provider)
	}

	model, ok := modelRegistry.GetModel(provider, modelID)
	if !ok || !model.SupportsOAuth {
		return fmt.Errorf("%s %q is not available through OAuth for provider %q", field, modelID, provider)
	}

	return nil
}

func (auth *ModelAuth) applySubscriptionDefaults() {
	if auth == nil {
		return
	}
	if auth.CredentialSource.Kind != ModelCredentialSourceTokenStore ||
		auth.CredentialSource.Type != appcredential.TypeOAuth {
		return
	}

	providerValue7 := str.String(auth.Provider)
	providerValue8 := str.String(auth.Provider)
	if providerValue7.Normalized() != constants.ModelProviderOpenAI && providerValue8.
		Normalized() != constants.ModelProviderOpenAICodex {
		return
	}
	if !isProviderDefaultBaseURL(auth.BaseURL) {
		return
	}

	auth.BaseURL = constants.DefaultOpenAISubscriptionBaseURL
}

// SupportsMaxOutputTokens reports whether the resolved model route can request
// an explicit provider-enforced output token cap.
func (auth ModelAuth) SupportsMaxOutputTokens() bool {
	if auth.CredentialSource.Kind != ModelCredentialSourceTokenStore ||
		auth.CredentialSource.Type != appcredential.TypeOAuth {
		return true
	}

	providerValue9 := str.String(auth.Provider)
	provider := providerValue9.Normalized()
	return provider != constants.ModelProviderOpenAI &&
		provider != constants.ModelProviderOpenAICodex
}

// SummaryModelSupportsMaxOutputTokens reports whether summary-model dependent
// background jobs should request explicit output token caps.
func (c *Config) SummaryModelSupportsMaxOutputTokens() bool {
	if c == nil {
		return true
	}

	auth, err := c.ResolveSummaryModelAuth()
	if err != nil {
		return true
	}

	return auth.SupportsMaxOutputTokens()
}

// SummaryModelMaxOutputTokensEffective returns maxOutputTokens only when the
// resolved summary model route supports the parameter.
func (c *Config) SummaryModelMaxOutputTokensEffective(maxOutputTokens int64) int64 {
	if maxOutputTokens <= 0 {
		return 0
	}
	if !c.SummaryModelSupportsMaxOutputTokens() {
		return 0
	}

	return maxOutputTokens
}

func normalizeStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}

	normalized := make(map[string]string, len(values))
	for key, value := range values {
		keyValue := str.String(key)
		key = keyValue.Trim()
		value2 := str.String(value)
		value = value2.Trim()
		if key == "" || value == "" {
			continue
		}
		normalized[key] = value
	}
	if len(normalized) == 0 {
		return nil
	}

	return normalized
}

func mergeModelAuthHeaders(values ...map[string]string) map[string]string {
	merged := make(map[string]string)
	for _, headers := range values {
		for key, value := range normalizeStringMap(headers) {
			merged[key] = value
		}
	}
	if len(merged) == 0 {
		return nil
	}

	return merged
}

func stringMapsEqual(a map[string]string, b map[string]string) bool {
	a = normalizeStringMap(a)
	b = normalizeStringMap(b)
	if len(a) != len(b) {
		return false
	}

	for key, value := range a {
		if b[key] != value {
			return false
		}
	}

	return true
}

func refreshStoredModelCredential(provider string) (StoredModelCredential, bool, error) {
	if refreshStoredProviderToken == nil {
		return StoredModelCredential{}, false, nil
	}

	providerValue10 := str.String(provider)
	provider = providerValue10.Normalized()
	return refreshStoredProviderToken(context.Background(), provider)
}

func loadStoredModelCredential(provider string) (StoredModelCredential, error) {
	if loadStoredProviderToken == nil {
		return StoredModelCredential{}, nil
	}

	providerValue11 := str.String(provider)
	provider = providerValue11.Normalized()
	credential, err := loadStoredProviderToken(provider)
	if err != nil {
		return StoredModelCredential{}, err
	}

	return credential, nil
}

func getProviderOAuthEnvCredential(provider string) (string, string) {
	providerValue12 := str.String(provider)
	switch providerValue12.Normalized() {
	case constants.ModelProviderAnthropic:
		return getCredentialFromEnv([]string{"ANTHROPIC_OAUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN"})
	case constants.ModelProviderGitHubCopilot:
		return getCredentialFromEnv([]string{"COPILOT_GITHUB_TOKEN"})
	default:
		return "", ""
	}
}

func getStoredModelCredentialValue(credential StoredModelCredential) string {
	trimmedValueValue7 := str.String(credential.Type)
	switch trimmedValueValue7.Normalized() {
	case appcredential.TypeAPIKey:
		keyValue2 := str.String(credential.Key)
		return keyValue2.Trim()
	case appcredential.TypeOAuth, "":
		tokenValue := str.String(credential.Token)
		return tokenValue.Trim()
	default:
		return ""
	}
}

func newMissingModelCredentialError(role string, provider string) error {
	roleValue := str.String(role)
	role = roleValue.Trim()
	if role == "" {
		role = "model"
	}
	providerValue13 := str.String(provider)
	provider = providerValue13.Normalized()
	if role == "embedding" {
		if provider == "" {
			return fmt.Errorf("%s API key is required; set a provider API key, provider env var, or role apiKey", role)
		}

		return fmt.Errorf("%s API key is required for provider %q; set a provider API key, provider env var, or role apiKey",
			role,
			provider,
		)
	}
	if provider == "" {
		return fmt.Errorf("%s API key is required; set a provider API key, provider env var, role apiKey, or provider login", role)
	}

	return fmt.Errorf("%s API key is required for provider %q; set a provider API key, provider env var, role apiKey, or provider login", role, provider)
}
