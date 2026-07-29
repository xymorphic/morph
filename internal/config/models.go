package config

import (
	"strings"

	appcredential "github.com/wandxy/morph/internal/credential"
	modelprovider "github.com/wandxy/morph/internal/model/provider"
	"github.com/wandxy/morph/pkg/str"
)

type ModelCredentialSourceKind string

const (
	modelAuthTypeAPIKey = "api-key"
	modelAuthTypeNone   = "none"
)

const (
	// ModelCredentialSourceRoleConfig means the credential came from the concrete model role config.
	ModelCredentialSourceRoleConfig ModelCredentialSourceKind = "role-config"

	// ModelCredentialSourceProviderConfig means the credential came from provider-specific config.
	ModelCredentialSourceProviderConfig ModelCredentialSourceKind = "provider-config"

	// ModelCredentialSourceProviderEnv means the credential came from a provider-specific environment variable.
	ModelCredentialSourceProviderEnv ModelCredentialSourceKind = "provider-env"

	// ModelCredentialSourceTokenStore means the credential came from a local OAuth or subscription token store.
	ModelCredentialSourceTokenStore ModelCredentialSourceKind = "token-store"

	// ModelCredentialSourceLocalProvider means the credential is a non-secret local provider marker.
	ModelCredentialSourceLocalProvider ModelCredentialSourceKind = "local-provider"
)

// ModelsConfig contains provider credentials and model-specific settings.
type ModelsConfig struct {
	MaxRetries *int                           `yaml:"maxRetries"`
	Providers  map[string]ProviderModelConfig `yaml:"providers"`
	Main       MainModelConfig                `yaml:"main"`
	Summary    SummaryModelConfig             `yaml:"summary"`
	Embedding  EmbeddingModelConfig           `yaml:"embedding"`
}

// ProviderModelConfig describes static credential settings for one model provider.
type ProviderModelConfig struct {
	APIKey    string                           `yaml:"apiKey"`
	APIKeyEnv []string                         `yaml:"apiKeyEnv"`
	API       string                           `yaml:"api"`
	BaseURL   string                           `yaml:"baseUrl"`
	Headers   map[string]string                `yaml:"headers"`
	Models    map[string]ProviderModelMetadata `yaml:"models"`
}

// ProviderModelMetadata describes explicit metadata for one provider-local model.
type ProviderModelMetadata struct {
	ContextLength          int                             `yaml:"contextLength"`
	MaxOutputTokens        int64                           `yaml:"maxOutputTokens"`
	SupportsTools          *bool                           `yaml:"supportsTools"`
	SupportsVision         *bool                           `yaml:"supportsVision"`
	Reasoning              *bool                           `yaml:"reasoning"`
	ReasoningEfforts       []modelprovider.ReasoningEffort `yaml:"reasoningEfforts"`
	ReasoningEffortDefault modelprovider.ReasoningEffort   `yaml:"reasoningEffortDefault"`
	ReasoningSummary       *bool                           `yaml:"reasoningSummary"`
}

func (metadata ProviderModelMetadata) ReasoningCapability() modelprovider.ReasoningCapability {
	return modelprovider.ReasoningCapability{
		Efforts:       append([]modelprovider.ReasoningEffort(nil), metadata.ReasoningEfforts...),
		DefaultEffort: metadata.ReasoningEffortDefault,
		Summary:       metadata.ReasoningSummary != nil && *metadata.ReasoningSummary,
	}
}

// MainModelConfig selects the model used for normal agent turns.
type MainModelConfig struct {
	Name            string                        `yaml:"name"`
	Provider        string                        `yaml:"provider"`
	API             string                        `yaml:"api"`
	APIKey          string                        `yaml:"apiKey"`
	BaseURL         string                        `yaml:"baseUrl"`
	ContextLength   int                           `yaml:"contextLength"`
	Stream          *bool                         `yaml:"stream"`
	ReasoningEffort modelprovider.ReasoningEffort `yaml:"reasoningEffort"`
}

// SummaryModelConfig selects the model used for summaries and compaction.
type SummaryModelConfig struct {
	Name     string `yaml:"name"`
	Provider string `yaml:"provider"`
	API      string `yaml:"api"`
	APIKey   string `yaml:"apiKey"`
	BaseURL  string `yaml:"baseUrl"`
}

// EmbeddingModelConfig selects the model used for vector embeddings.
type EmbeddingModelConfig struct {
	Name     string `yaml:"name"`
	Provider string `yaml:"provider"`
	API      string `yaml:"api"`
	APIKey   string `yaml:"apiKey"`
	BaseURL  string `yaml:"baseUrl"`
}

// ModelCredentialSource describes credential provenance without containing the credential value.
type ModelCredentialSource struct {
	Kind      ModelCredentialSourceKind
	Name      string
	Type      string
	HasExpiry bool
}

// StoredModelCredential describes a locally stored provider credential.
type StoredModelCredential = appcredential.StoredCredential

// ModelAuth describes authentication metadata for a model provider.
type ModelAuth struct {
	Provider         string
	API              string
	APIKey           string
	BaseURL          string
	Headers          map[string]string
	CredentialSource ModelCredentialSource
}

func (auth ModelAuth) AuthType() string {
	trimmedValueValue := str.String(auth.CredentialSource.Type)
	if value := trimmedValueValue.Trim(); value != "" {
		return strings.ToLower(value)
	}
	if auth.CredentialSource.Kind == ModelCredentialSourceLocalProvider {
		return string(ModelCredentialSourceLocalProvider)
	}
	aPIKeyValue := str.String(auth.APIKey)
	if aPIKeyValue.Trim() != "" {
		return modelAuthTypeAPIKey
	}
	kindValue := str.String(string(auth.CredentialSource.Kind))
	if value := kindValue.Trim(); value != "" {
		return value
	}

	return modelAuthTypeNone
}
