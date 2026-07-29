package config

import (
	"fmt"
	"strings"

	modelprovider "github.com/wandxy/morph/internal/model/provider"
	"github.com/wandxy/morph/pkg/str"
)

func isValidModelID(value string) bool {
	valueText := str.String(value).Trim()
	if valueText == "" {
		return false
	}

	if strings.Count(valueText, "/") > 1 {
		return false
	}

	segments := strings.SplitSeq(valueText, "/")
	for segment := range segments {
		segmentValue := str.String(segment)
		if segmentValue.Trim() == "" {
			return false
		}
	}

	return true
}

func applyRegistryModelMetadata(cfg *Config, requestedContextLength int) {
	if cfg == nil {
		return
	}

	model, ok := modelRegistry.GetModelForAPI(
		cfg.Models.Main.Provider,
		cfg.MainModelAPIEffective(),
		cfg.Models.Main.Name,
	)
	if !ok || model.ContextWindow <= 0 {
		return
	}

	if requestedContextLength <= 0 || requestedContextLength > model.ContextWindow {
		cfg.Models.Main.ContextLength = model.ContextWindow
	}
}

func validateReasoningSettings(cfg ModelsConfig) error {
	preference := strings.TrimSpace(string(cfg.Main.ReasoningEffort))
	if strings.EqualFold(preference, "default") || strings.EqualFold(preference, "reset") {
		return fmt.Errorf("models.main.reasoningEffort %q is reserved", preference)
	}

	for providerID, provider := range cfg.Providers {
		for modelID, metadata := range provider.Models {
			reasoning := metadata.Reasoning != nil && *metadata.Reasoning
			if _, err := modelprovider.NormalizeReasoningCapability(
				reasoning,
				metadata.ReasoningCapability(),
			); err != nil {
				return fmt.Errorf(
					"models.providers.%s.models.%s reasoning metadata is invalid: %w",
					providerID,
					modelID,
					err,
				)
			}
		}
	}

	return nil
}

func validateProviderAPI(field string, providerID string, apiID string) error {
	providerIDValue := str.String(providerID)
	providerID = providerIDValue.Normalized()
	apiIDValue := str.String(apiID)
	apiID = apiIDValue.Normalized()
	if _, ok := modelRegistry.GetAPI(apiID); !ok {
		return fmt.Errorf("%s must be one of: %s", field, getModelAPIList(nil))
	}
	if !modelRegistry.SupportsProviderAPI(providerID, apiID) {
		return fmt.Errorf("%s %q is not supported by provider %q", field, apiID, providerID)
	}

	return nil
}

func ValidateModelGenerationAPIForProvider(field string, providerID string, apiID string) error {
	apiID = getModelAPIID(apiID)
	if err := validateModelRoleAPI(field, apiID, modelGenerationAPIs()); err != nil {
		return err
	}

	return validateProviderAPI(field, providerID, apiID)
}

func validateModelRoleAPI(field string, apiID string, allowedAPIs map[string]struct{}) error {
	apiIDValue2 := str.String(apiID)
	apiID = apiIDValue2.Normalized()
	if _, ok := allowedAPIs[apiID]; ok {
		return nil
	}

	return fmt.Errorf("%s must be one of: %s", field, getModelAPIList(allowedAPIs))
}

func validateRegistryModel(
	field string,
	providerID string,
	apiID string,
	modelID string,
	allowedAPIs map[string]struct{},
) error {
	if modelID == "" {
		return nil
	}

	provider, ok := modelRegistry.GetProvider(providerID)
	if !ok {
		return fmt.Errorf("%s provider must be one of: %s", field, getModelProviderList())
	}

	model, known := modelRegistry.GetModelForAPI(provider.ID, apiID, modelID)
	if !known {
		if registered, registeredOnAnotherAPI := modelRegistry.GetModel(provider.ID, modelID); registeredOnAnotherAPI {
			if _, ok := allowedAPIs[registered.API]; !ok {
				return fmt.Errorf("%s %q is not compatible with this model role", field, modelID)
			}
		}
		if provider.RequiresKnownModel || !provider.SupportsModels {
			return fmt.Errorf("%s %q is not registered for provider %q", field, modelID, provider.ID)
		}

		return nil
	}

	if len(allowedAPIs) != 0 {
		if _, ok := allowedAPIs[apiID]; !ok {
			return fmt.Errorf("%s %q is not compatible with this model role", field, modelID)
		}
		if _, ok := allowedAPIs[model.API]; !ok {
			return fmt.Errorf("%s %q is not compatible with this model role", field, modelID)
		}
	}

	return nil
}

func modelGenerationAPIs() map[string]struct{} {
	return map[string]struct{}{
		modelprovider.APIOpenAICompletions: {},
		modelprovider.APIOpenAIResponses:   {},
		modelprovider.APIOllamaNative:      {},
		modelprovider.APIAnthropicMessages: {},
	}
}

func modelEmbeddingAPIs() map[string]struct{} {
	return map[string]struct{}{
		modelprovider.APIOpenAIEmbeddings:     {},
		modelprovider.APIOpenRouterEmbeddings: {},
		modelprovider.APIOllamaEmbeddings:     {},
	}
}
