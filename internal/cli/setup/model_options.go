package setup

import (
	"context"
	"strings"

	"github.com/xymorphic/morph/internal/config"
	modelcatalog "github.com/xymorphic/morph/internal/model"
	modelprovider "github.com/xymorphic/morph/internal/model/provider"
	"github.com/xymorphic/morph/pkg/str"
)

type ModelOptions struct {
	Provider  string
	Current   string
	BaseURL   string
	OAuthOnly bool
	Config    *config.Config
	Refresh   bool
	Registry  *modelprovider.Registry
}

func ListModelOptions(ctx context.Context, opts ModelOptions) ([]modelcatalog.Option, bool, error) {
	registry := opts.Registry
	if registry == nil {
		registry = modelprovider.DefaultRegistry()
	}
	providerValue := str.String(opts.Provider)
	options, err := modelcatalog.ListOptions(modelcatalog.OptionQuery{
		Context:             ctx,
		Provider:            providerValue.Trim(),
		Current:             opts.Current,
		OAuthOnly:           opts.OAuthOnly,
		Config:              opts.Config,
		BaseURL:             ResolveModelOptionsBaseURL(opts),
		LocalDiscovery:      true,
		Refresh:             opts.Refresh,
		Registry:            registry,
		DiscoverLocalModels: discoverOllamaModels,
	})

	return options, hasInstalledLocalOptions(options), err
}

func ResolveModelOptionsBaseURL(opts ModelOptions) string {
	baseURLValue := str.String(opts.BaseURL)
	if value := baseURLValue.Trim(); value != "" {
		return value
	}

	registry := opts.Registry
	if registry == nil {
		registry = modelprovider.DefaultRegistry()
	}
	providerValue2 := str.String(opts.Provider)
	providerID := providerValue2.Normalized()
	provider, ok := registry.GetProvider(providerID)
	if !ok {
		return ""
	}
	defaultAPIValue := str.String(provider.DefaultAPI)
	api := defaultAPIValue.Trim()
	if opts.Config != nil {
		if strings.EqualFold(opts.Config.Models.Main.Provider, provider.ID) {
			baseURLValue2 := str.String(opts.Config.Models.Main.BaseURL)
			if value := baseURLValue2.Trim(); value != "" {
				return normalizeInheritedSetupBaseURL(provider.ID, provider.DefaultAPI, value)
			}
			aPIValue := str.String(opts.Config.Models.Main.API)
			if value := aPIValue.Trim(); value != "" {
				api = value
			}
		}
		if providerConfig, ok := getProviderModelConfig(opts.Config, provider.ID); ok {
			baseURLValue3 := str.String(providerConfig.BaseURL)
			if value := baseURLValue3.Trim(); value != "" {
				return normalizeInheritedSetupBaseURL(provider.ID, provider.DefaultAPI, value)
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

func getProviderModelConfig(cfg *config.Config, provider string) (config.ProviderModelConfig, bool) {
	if cfg == nil || len(cfg.Models.Providers) == 0 {
		return config.ProviderModelConfig{}, false
	}

	providerValue3 := str.String(provider)
	provider = providerValue3.Normalized()
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
