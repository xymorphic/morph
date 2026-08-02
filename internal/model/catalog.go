package model

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/constants"
	modelprovider "github.com/xymorphic/morph/internal/model/provider"
	"github.com/xymorphic/morph/pkg/str"
)

type Option struct {
	ID                    string
	Name                  string
	Provider              string
	API                   string
	ContextWindow         int
	MaxTokens             int
	Input                 []string
	Reasoning             bool
	ReasoningCapabilities modelprovider.ReasoningCapability
	SupportsTools         bool
	SupportsOAuth         bool
	DisplayDefault        bool
	Current               bool
	LocalMissing          bool
	BaseURL               string
	Source                OptionSource
}

func (option Option) Key() modelprovider.ModelKey {
	return modelprovider.CanonicalModelKey(option.Provider, option.API, option.ID)
}

type OptionSource string

const (
	OptionSourceCatalog   OptionSource = "catalog"
	OptionSourceConfig    OptionSource = "config"
	OptionSourceDiscovery OptionSource = "discovery"
)

type ProviderOption struct {
	ID              string
	Name            string
	DisplayIndex    int
	HasDisplayIndex bool
	Type            string
	ModelCount      int
	SupportsAPIKey  bool
	SupportsOAuth   bool
	Local           bool
	AuthType        string
	Current         bool
}

type OptionQuery struct {
	Context             context.Context
	Provider            string
	API                 string
	Current             string
	OAuthOnly           bool
	Registry            *modelprovider.Registry
	Config              *config.Config
	BaseURL             string
	LocalDiscovery      bool
	Refresh             bool
	DiscoveryTTL        time.Duration
	DiscoverLocalModels func(context.Context, string) ([]modelprovider.ModelDefinition, error)
}

type ProviderQuery struct {
	Current    string
	Auth       map[string]string
	OAuthOnly  bool
	APIKeyOnly bool
	Registry   *modelprovider.Registry
}

type localDiscoveryCacheEntry struct {
	options []Option
	stored  time.Time
}

var localDiscoveryCache = struct {
	sync.Mutex
	values map[string]localDiscoveryCacheEntry
}{
	values: make(map[string]localDiscoveryCacheEntry),
}

const defaultLocalDiscoveryTTL = 30 * time.Second

func ListOptions(query OptionQuery) ([]Option, error) {
	registry := query.Registry
	if registry == nil {
		registry = modelprovider.DefaultRegistry()
	}
	providerValue := str.String(query.Provider)
	provider := providerValue.Normalized()
	currentValue := str.String(query.Current)
	current := currentValue.Trim()
	options := listRegistryOptions(registry, provider, current, query.OAuthOnly)

	hasExplicitConfig := hasExplicitProviderModelDefinitions(query.Config, provider)
	explicitOptions := listExplicitConfigOptions(query.Config, registry, provider, current, query.OAuthOnly)
	if len(explicitOptions) > 0 {
		options = mergeOptions(explicitOptions, options, false)
	}

	providerDef, _ := registry.GetProvider(provider)
	if query.LocalDiscovery &&
		query.DiscoverLocalModels != nil &&
		providerDef.Local != nil &&
		provider == constants.ModelProviderOllama &&
		!hasExplicitConfig {
		discovered, err := getDiscoveredLocalOptions(query, providerDef, current)
		if err != nil {
			return nil, err
		}
		options = mergeOptions(discovered, options, true)
	}

	setCurrentOption(options, provider, getCurrentOptionAPI(query, registry, provider), current)
	sortOptions(options)

	return options, nil
}

func getCurrentOptionAPI(query OptionQuery, registry *modelprovider.Registry, provider string) string {
	apiValue := str.String(query.API)
	if api := apiValue.Normalized(); api != "" {
		return api
	}
	if query.Config != nil && strings.EqualFold(query.Config.Models.Main.Provider, provider) {
		configAPIValue := str.String(query.Config.MainModelAPIEffective())
		if api := configAPIValue.Normalized(); api != "" {
			return api
		}
	}

	return getProviderDefaultAPI(registry, provider)
}

func setCurrentOption(options []Option, provider, api, current string) {
	if api == "" {
		return
	}
	currentKey := modelprovider.CanonicalModelKey(provider, api, current)
	for i := range options {
		options[i].Current = options[i].Key() == currentKey
	}
}

func getDiscoveredLocalOptions(
	query OptionQuery,
	provider modelprovider.ProviderDefinition,
	current string,
) ([]Option, error) {
	ctx := query.Context
	if ctx == nil {
		ctx = context.Background()
	}

	baseURL := getLocalDiscoveryBaseURL(query, provider)
	cacheKey := strings.Join([]string{provider.ID, baseURL}, "\x00")
	ttl := query.DiscoveryTTL
	if ttl <= 0 {
		ttl = defaultLocalDiscoveryTTL
	}
	if !query.Refresh {
		if options, ok := getCachedLocalDiscoveryOptions(cacheKey, ttl, current); ok {
			return options, nil
		}
	}

	models, err := query.DiscoverLocalModels(ctx, baseURL)
	if err != nil {
		return nil, err
	}

	options := modelDefinitionsToOptions(models, current, baseURL, OptionSourceDiscovery)
	setCachedLocalDiscoveryOptions(cacheKey, options)

	return cloneOptionsWithCurrent(options, current), nil
}

func getLocalDiscoveryBaseURL(query OptionQuery, provider modelprovider.ProviderDefinition) string {
	baseURLValue := str.String(query.BaseURL)
	if value := baseURLValue.Trim(); value != "" {
		return value
	}
	defaultAPIValue := str.String(provider.DefaultAPI)
	api := defaultAPIValue.Trim()
	if query.Config != nil {
		if strings.EqualFold(query.Config.Models.Main.Provider, provider.ID) {
			baseURLValue2 := str.String(query.Config.Models.Main.BaseURL)
			if value := baseURLValue2.Trim(); value != "" {
				return value
			}
			aPIValue := str.String(query.Config.Models.Main.API)
			if value := aPIValue.Trim(); value != "" {
				api = value
			}
		}
		if providerConfig, ok := getExplicitProviderConfig(query.Config, provider.ID); ok {
			baseURLValue3 := str.String(providerConfig.BaseURL)
			if value := baseURLValue3.Trim(); value != "" {
				return value
			}
			aPIValue2 := str.String(providerConfig.API)
			if value := aPIValue2.Trim(); value != "" {
				api = value
			}
		}
	}
	apiValue := str.String(api)
	baseURLsValue := str.String(provider.BaseURLs[apiValue.Normalized()])
	return baseURLsValue.Trim()
}

func getCachedLocalDiscoveryOptions(cacheKey string, ttl time.Duration, current string) ([]Option, bool) {
	localDiscoveryCache.Lock()
	defer localDiscoveryCache.Unlock()

	entry, ok := localDiscoveryCache.values[cacheKey]
	if !ok || time.Since(entry.stored) > ttl {
		return nil, false
	}

	return cloneOptionsWithCurrent(entry.options, current), true
}

func setCachedLocalDiscoveryOptions(cacheKey string, options []Option) {
	localDiscoveryCache.Lock()
	defer localDiscoveryCache.Unlock()

	localDiscoveryCache.values[cacheKey] = localDiscoveryCacheEntry{
		options: cloneOptionsWithCurrent(options, ""),
		stored:  time.Now(),
	}
}

func cloneOptionsWithCurrent(options []Option, current string) []Option {
	cloned := make([]Option, 0, len(options))
	currentValue2 := str.String(current)
	current = currentValue2.Trim()
	for _, option := range options {
		option.Input = append([]string(nil), option.Input...)
		option.ReasoningCapabilities.Efforts = append(
			[]modelprovider.ReasoningEffort(nil),
			option.ReasoningCapabilities.Efforts...,
		)
		iDValue := str.String(option.ID)
		option.Current = iDValue.Trim() == current
		cloned = append(cloned, option)
	}

	return cloned
}

func ListProviders(query ProviderQuery) []ProviderOption {
	registry := query.Registry
	if registry == nil {
		registry = modelprovider.DefaultRegistry()
	}
	currentValue3 := str.String(query.Current)
	current := currentValue3.Normalized()
	providers := registry.GetProviders()
	options := make([]ProviderOption, 0, len(providers))
	for _, provider := range providers {
		if !provider.SupportsModels {
			continue
		}
		if query.OAuthOnly && !provider.SupportsOAuth {
			continue
		}
		if query.APIKeyOnly && !provider.SupportsAPIKey {
			continue
		}

		count := countGenerationModels(registry, provider.ID)
		if count == 0 {
			continue
		}
		iDValue2 := str.String(provider.ID)
		displayNameValue := str.String(provider.DisplayName)
		authValue := str.String(query.Auth[provider.ID])
		iDValue3 := str.String(provider.ID)
		options = append(options, ProviderOption{
			ID:              iDValue2.Trim(),
			Name:            displayNameValue.Trim(),
			DisplayIndex:    provider.DisplayIndex,
			HasDisplayIndex: provider.HasDisplayIndex,
			Type:            getProviderOptionType(provider),
			ModelCount:      count,
			SupportsAPIKey:  provider.SupportsAPIKey,
			SupportsOAuth:   provider.SupportsOAuth,
			Local:           provider.Local != nil,
			AuthType:        authValue.Trim(),
			Current:         iDValue3.Trim() == current,
		})
	}

	sort.Slice(options, func(i, j int) bool {
		if options[i].HasDisplayIndex != options[j].HasDisplayIndex {
			return options[i].HasDisplayIndex
		}
		if options[i].HasDisplayIndex && options[i].DisplayIndex != options[j].DisplayIndex {
			return options[i].DisplayIndex < options[j].DisplayIndex
		}
		if options[i].Current != options[j].Current {
			return options[i].Current
		}

		return strings.ToLower(options[i].ID) < strings.ToLower(options[j].ID)
	})

	return options
}

func isGenerationAPI(api string) bool {
	apiValue2 := str.String(api)
	switch apiValue2.Trim() {
	case modelprovider.APIOpenAICompletions,
		modelprovider.APIOpenAIResponses,
		modelprovider.APIOllamaNative,
		modelprovider.APIAnthropicMessages:
		return true
	default:
		return false
	}
}

func countGenerationModels(registry *modelprovider.Registry, provider string) int {
	count := 0
	for _, model := range registry.GetModels(provider) {
		if isGenerationAPI(model.API) {
			count++
		}
	}

	return count
}

func listRegistryOptions(
	registry *modelprovider.Registry,
	provider string,
	current string,
	oauthOnly bool,
) []Option {
	models := registry.GetModels(provider)
	options := make([]Option, 0, len(models))
	for _, model := range models {
		if !isGenerationAPI(model.API) {
			continue
		}
		if oauthOnly && !model.SupportsOAuth {
			continue
		}

		option := modelDefinitionToOption(model, current)
		option.Source = OptionSourceCatalog
		options = append(options, option)
	}

	sortOptions(options)

	return options
}

func listExplicitConfigOptions(
	cfg *config.Config,
	registry *modelprovider.Registry,
	provider string,
	current string,
	oauthOnly bool,
) []Option {
	if cfg == nil || len(cfg.Models.Providers) == 0 {
		return nil
	}

	providerConfig, ok := getExplicitProviderConfig(cfg, provider)
	if !ok || len(providerConfig.Models) == 0 {
		return nil
	}
	aPIValue3 := str.String(providerConfig.API)
	api := aPIValue3.Trim()
	if api == "" {
		api = getProviderDefaultAPI(registry, provider)
	}
	if !isGenerationAPI(api) {
		return nil
	}

	options := make([]Option, 0, len(providerConfig.Models))
	for modelID, metadata := range providerConfig.Models {
		modelIDValue := str.String(modelID)
		modelID = modelIDValue.Trim()
		if modelID == "" {
			continue
		}
		currentValue4 := str.String(current)
		baseURLValue4 := str.String(providerConfig.BaseURL)
		currentValue5 := str.String(current)
		option := Option{
			ID:                    modelID,
			Name:                  modelID,
			Provider:              provider,
			API:                   api,
			ContextWindow:         metadata.ContextLength,
			MaxTokens:             int(metadata.MaxOutputTokens),
			Input:                 []string{string(modelprovider.InputText)},
			Current:               modelID == currentValue4.Trim(),
			LocalMissing:          false,
			BaseURL:               baseURLValue4.Trim(),
			Source:                OptionSourceConfig,
			SupportsTools:         boolPtrValue(metadata.SupportsTools),
			Reasoning:             boolPtrValue(metadata.Reasoning),
			ReasoningCapabilities: metadata.ReasoningCapability(),
			DisplayDefault:        modelID == currentValue5.Trim(),
		}
		if boolPtrValue(metadata.SupportsVision) {
			option.Input = append(option.Input, string(modelprovider.InputImage))
		}
		if oauthOnly && !option.SupportsOAuth {
			continue
		}

		options = append(options, option)
	}

	sortOptions(options)

	return options
}

func hasExplicitProviderModelDefinitions(cfg *config.Config, provider string) bool {
	providerConfig, ok := getExplicitProviderConfig(cfg, provider)
	return ok && len(providerConfig.Models) > 0
}

func getExplicitProviderConfig(
	cfg *config.Config,
	provider string,
) (config.ProviderModelConfig, bool) {
	if cfg == nil || len(cfg.Models.Providers) == 0 {
		return config.ProviderModelConfig{}, false
	}

	providerValue2 := str.String(provider)
	provider = providerValue2.Normalized()
	if providerConfig, ok := cfg.Models.Providers[provider]; ok {
		return providerConfig, true
	}

	for key, providerConfig := range cfg.Models.Providers {
		keyValue := str.String(key)
		if strings.EqualFold(keyValue.Trim(), provider) {
			return providerConfig, true
		}
	}

	return config.ProviderModelConfig{}, false
}

func getProviderDefaultAPI(registry *modelprovider.Registry, provider string) string {
	if registry == nil {
		registry = modelprovider.DefaultRegistry()
	}
	providerDef, ok := registry.GetProvider(provider)
	if !ok {
		return ""
	}
	defaultAPIValue2 := str.String(providerDef.DefaultAPI)
	return defaultAPIValue2.Trim()
}

func boolPtrValue(value *bool) bool {
	return value != nil && *value
}

func mergeOptions(primary []Option, secondary []Option, markSecondaryMissing bool) []Option {
	merged := make([]Option, 0, len(primary)+len(secondary))
	seen := make(map[modelprovider.ModelKey]struct{}, len(primary)+len(secondary))
	for _, option := range primary {
		iDValue4 := str.String(option.ID)
		option.ID = iDValue4.Trim()
		if option.ID == "" {
			continue
		}
		option.ReasoningCapabilities.Efforts = append(
			[]modelprovider.ReasoningEffort(nil),
			option.ReasoningCapabilities.Efforts...,
		)
		merged = append(merged, option)
		seen[option.Key()] = struct{}{}
	}
	for _, option := range secondary {
		iDValue5 := str.String(option.ID)
		option.ID = iDValue5.Trim()
		if option.ID == "" {
			continue
		}
		if _, ok := seen[option.Key()]; ok {
			continue
		}
		option.ReasoningCapabilities.Efforts = append(
			[]modelprovider.ReasoningEffort(nil),
			option.ReasoningCapabilities.Efforts...,
		)
		if markSecondaryMissing {
			option.LocalMissing = true
		}
		merged = append(merged, option)
	}

	return merged
}

func sortOptions(options []Option) {
	sort.Slice(options, func(i, j int) bool {
		if options[i].LocalMissing != options[j].LocalMissing {
			return !options[i].LocalMissing
		}
		if options[i].DisplayDefault != options[j].DisplayDefault {
			return options[i].DisplayDefault
		}
		if options[i].Current != options[j].Current {
			return options[i].Current
		}

		return strings.ToLower(options[i].ID) < strings.ToLower(options[j].ID)
	})
}

func getProviderOptionType(provider modelprovider.ProviderDefinition) string {
	switch {
	case provider.Local != nil:
		return "local"
	case provider.SupportsAPIKey && provider.SupportsOAuth:
		return "api-key/oauth"
	case provider.SupportsOAuth:
		return "oauth"
	case provider.SupportsAPIKey:
		return "api-key"
	default:
		return "none"
	}
}

func modelDefinitionToOption(model modelprovider.ModelDefinition, current string) Option {
	inputs := make([]string, 0, len(model.Input))
	for _, input := range model.Input {
		inputValue := str.String(string(input))
		value := inputValue.Trim()
		if value != "" {
			inputs = append(inputs, value)
		}
	}
	iDValue6 := str.String(model.ID)
	nameValue := str.String(model.Name)
	providerValue3 := str.String(model.Provider)
	aPIValue4 := str.String(model.API)
	iDValue7 := str.String(model.ID)
	option := Option{
		ID:                    iDValue6.Trim(),
		Name:                  nameValue.Trim(),
		Provider:              providerValue3.Trim(),
		API:                   aPIValue4.Trim(),
		ContextWindow:         model.ContextWindow,
		MaxTokens:             model.MaxTokens,
		Input:                 inputs,
		Reasoning:             model.Reasoning,
		ReasoningCapabilities: model.ReasoningCapabilities,
		SupportsTools:         model.SupportsTools,
		SupportsOAuth:         model.SupportsOAuth,
		DisplayDefault:        model.DisplayDefault,
		Current:               iDValue7.Trim() == current,
		Source:                OptionSourceCatalog,
	}
	option.ReasoningCapabilities.Efforts = append(
		[]modelprovider.ReasoningEffort(nil),
		model.ReasoningCapabilities.Efforts...,
	)

	return option
}

func modelDefinitionsToOptions(
	models []modelprovider.ModelDefinition,
	current string,
	baseURL string,
	source OptionSource,
) []Option {
	options := make([]Option, 0, len(models))
	for _, model := range models {
		iDValue8 := str.String(model.ID)
		if iDValue8.Trim() == "" {
			continue
		}
		if !isGenerationAPI(model.API) {
			continue
		}
		option := modelDefinitionToOption(model, current)
		baseURLValue5 := str.String(baseURL)
		option.BaseURL = baseURLValue5.Trim()
		option.Source = source
		options = append(options, option)
	}

	sortOptions(options)

	return options
}
