package credential

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/xymorphic/morph/pkg/str"
)

const (
	// TypeAPIKey stores a static provider API key.
	TypeAPIKey = "api_key"

	// TypeOAuth stores an OAuth or subscription access token.
	TypeOAuth = "oauth"
)

// StoredCredential is a persisted credential record for a provider.
type StoredCredential struct {
	// Type identifies which credential fields are meaningful.
	Type string `json:"type"`

	// Key stores a static API key when Type is TypeAPIKey.
	Key string `json:"key,omitempty"`

	// Token stores an OAuth or subscription access token when Type is TypeOAuth.
	Token string `json:"token,omitempty"`

	// Refresh stores an optional refresh token for OAuth credentials.
	Refresh string `json:"refresh,omitempty"`

	// ExpiresAt stores the optional access-token expiry time.
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`

	// Scopes stores optional OAuth scopes granted to the credential.
	Scopes []string `json:"scopes,omitempty"`
}

// CredentialSource identifies where a resolved provider credential came from.
type CredentialSource string

const (
	// CredentialSourceStored means the credential came from the local credential store.
	CredentialSourceStored CredentialSource = "stored"

	// CredentialSourceEnvironment means the credential came from an environment variable.
	CredentialSourceEnvironment CredentialSource = "environment"

	// CredentialSourceRuntime means the credential came from a runtime override.
	CredentialSourceRuntime CredentialSource = "runtime"

	// CredentialSourceConfig means the credential came from provider config.
	CredentialSourceConfig CredentialSource = "provider-config"

	// CredentialSourceMissing means no credential source was configured.
	CredentialSourceMissing CredentialSource = "missing"
)

// Status describes provider auth configuration without exposing credential values.
type Status struct {
	// Provider is the normalized provider ID.
	Provider string

	// Configured reports whether a usable credential source exists.
	Configured bool

	// Source describes where the credential was found.
	Source CredentialSource

	// Type is the stored credential type when known.
	Type string

	// HasExpiry reports whether the credential has an expiry timestamp.
	HasExpiry bool

	// Expired reports whether the credential expiry has passed.
	Expired bool
}

// LoginOptions describes a subscription provider login request.
type LoginOptions struct {
	// Provider is the normalized provider ID to authenticate.
	Provider string

	// Input provides optional interactive input for browser login fallback flows.
	Input io.Reader

	// Output receives login instructions without exposing credential values.
	Output io.Writer
}

// SubscriptionProvider logs in, refreshes, and prepares auth headers for a subscription provider.
type SubscriptionProvider interface {
	// Login obtains a new credential for the provider.
	Login(context.Context, LoginOptions) (StoredCredential, error)

	// Refresh refreshes an existing stored credential.
	Refresh(context.Context, StoredCredential) (StoredCredential, error)

	// AuthHeaders converts a stored credential into provider request headers.
	AuthHeaders(context.Context, StoredCredential) (map[string]string, error)
}

// Store persists and refreshes provider credentials.
type Store interface {
	// Get returns a credential for provider when one exists.
	Get(provider string) (StoredCredential, bool, error)

	// Set stores a credential for provider.
	Set(provider string, credential StoredCredential) error

	// Remove deletes the stored credential for provider.
	Remove(provider string) error

	// List returns the provider IDs with stored credentials.
	List() ([]string, error)

	// Refresh refreshes an expired OAuth credential through subscriptionProvider.
	Refresh(context.Context, string, SubscriptionProvider) (StoredCredential, bool, error)
}

var subscriptionRegistry sync.Map

// RegisterSubscriptionProvider registers refresh/login support for a provider.
func RegisterSubscriptionProvider(provider string, subscriptionProvider SubscriptionProvider) {
	provider = normalizeProvider(provider)
	if provider == "" || subscriptionProvider == nil {
		return
	}

	subscriptionRegistry.Store(provider, subscriptionProvider)
}

// GetSubscriptionProvider returns the registered subscription provider for provider.
func GetSubscriptionProvider(provider string) (SubscriptionProvider, bool) {
	value, ok := subscriptionRegistry.Load(normalizeProvider(provider))
	if !ok {
		return nil, false
	}

	subscriptionProvider, ok := value.(SubscriptionProvider)
	return subscriptionProvider, ok
}

// IsExpired reports whether credential has an expiry at or before the current time.
func IsExpired(credential StoredCredential) bool {
	return credential.ExpiresAt != nil && !time.Now().Before(*credential.ExpiresAt)
}

func normalizeProvider(provider string) string {
	providerValue := str.String(provider)
	return providerValue.Normalized()
}

func normalizeCredential(credential StoredCredential) StoredCredential {
	trimmedValueValue := str.String(credential.Type)
	credential.Type = trimmedValueValue.Normalized()
	keyValue := str.String(credential.Key)
	credential.Key = keyValue.Trim()
	tokenValue := str.String(credential.Token)
	credential.Token = tokenValue.Trim()
	refreshValue := str.String(credential.Refresh)
	credential.Refresh = refreshValue.Trim()
	credential.Scopes = normalizeStrings(credential.Scopes)
	return credential
}

func checkStoredCredential(credential StoredCredential) (StoredCredential, error) {
	credential = normalizeCredential(credential)
	if credential.Type == "" {
		return StoredCredential{}, errors.New("credential type is required")
	}
	if credential.Type != TypeAPIKey && credential.Type != TypeOAuth {
		return StoredCredential{}, fmt.Errorf("credential type must be one of: %s, %s", TypeAPIKey, TypeOAuth)
	}
	keyValue2 := str.String(credential.Key)
	if credential.Type == TypeAPIKey && keyValue2.Trim() == "" {
		return StoredCredential{}, errors.New("API key credential is required")
	}
	tokenValue2 := str.String(credential.Token)
	if credential.Type == TypeOAuth && tokenValue2.Trim() == "" {
		return StoredCredential{}, errors.New("OAuth token credential is required")
	}

	return credential, nil
}

func cloneCredential(credential StoredCredential) StoredCredential {
	credential.Scopes = append([]string(nil), credential.Scopes...)
	if credential.ExpiresAt != nil {
		expiresAt := *credential.ExpiresAt
		credential.ExpiresAt = &expiresAt
	}
	return credential
}

func normalizeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		valueText := str.String(value).Trim()
		if valueText == "" {
			continue
		}
		if _, ok := seen[valueText]; ok {
			continue
		}
		seen[valueText] = struct{}{}
		normalized = append(normalized, valueText)
	}
	if len(normalized) == 0 {
		return nil
	}

	return normalized
}
