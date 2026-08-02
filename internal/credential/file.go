package credential

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/gofrs/flock"
	"github.com/xymorphic/morph/internal/datadir"
	"github.com/xymorphic/morph/pkg/str"
)

var (
	lockRetryDelay = 20 * time.Millisecond
	lockRetries    = 100
)

// FileStore stores provider credentials in a JSON file.
type FileStore struct {
	// Path is the credential file path. When empty, DefaultPath is used.
	Path string
}

// DefaultPath returns the active profile credential file path.
func DefaultPath() string {
	return filepath.Join(datadir.HomeDir(), "auth.json")
}

// NewFileStore returns a file-backed credential store.
func NewFileStore(path string) *FileStore {
	pathValue := str.String(path)
	if pathValue.Trim() == "" {
		path = DefaultPath()
	}

	return &FileStore{Path: path}
}

// LoadStoredProviderCredential loads provider from the default credential store.
func LoadStoredProviderCredential(provider string) (StoredCredential, error) {
	credential, _, err := NewFileStore("").Get(provider)
	return credential, err
}

// RefreshStoredProviderCredential refreshes provider through its registered subscription provider.
func RefreshStoredProviderCredential(ctx context.Context, provider string) (StoredCredential, bool, error) {
	subscriptionProvider, ok := GetSubscriptionProvider(provider)
	if !ok {
		return StoredCredential{}, false, nil
	}

	return NewFileStore("").Refresh(ctx, provider, subscriptionProvider)
}

// Get returns a credential for provider when one exists.
func (s *FileStore) Get(provider string) (StoredCredential, bool, error) {
	provider = normalizeProvider(provider)
	if provider == "" {
		return StoredCredential{}, false, errors.New("provider is required")
	}
	if provider == morphAuthDocumentKey {
		return StoredCredential{}, false, errors.New("provider name is reserved")
	}

	var credential StoredCredential
	var ok bool
	err := s.withLockedData(false, func(data map[string]StoredCredential) (map[string]StoredCredential, bool, error) {
		credential, ok = data[provider]
		credential = cloneCredential(credential)
		return data, false, nil
	})
	if err != nil {
		return StoredCredential{}, false, err
	}

	return credential, ok, nil
}

// Set stores credential for provider.
func (s *FileStore) Set(provider string, credential StoredCredential) error {
	provider = normalizeProvider(provider)
	if provider == "" {
		return errors.New("provider is required")
	}
	if provider == morphAuthDocumentKey {
		return errors.New("provider name is reserved")
	}

	credential, err := checkStoredCredential(credential)
	if err != nil {
		return err
	}

	return s.withLockedData(true, func(data map[string]StoredCredential) (map[string]StoredCredential, bool, error) {
		data[provider] = credential
		return data, true, nil
	})
}

// Remove deletes the stored credential for provider.
func (s *FileStore) Remove(provider string) error {
	provider = normalizeProvider(provider)
	if provider == "" {
		return errors.New("provider is required")
	}
	if provider == morphAuthDocumentKey {
		return errors.New("provider name is reserved")
	}

	return s.withLockedData(false, func(data map[string]StoredCredential) (map[string]StoredCredential, bool, error) {
		if _, ok := data[provider]; !ok {
			return data, false, nil
		}

		delete(data, provider)
		return data, true, nil
	})
}

// List returns stored provider IDs in sorted order.
func (s *FileStore) List() ([]string, error) {
	var providers []string
	err := s.withLockedData(false, func(data map[string]StoredCredential) (map[string]StoredCredential, bool, error) {
		providers = make([]string, 0, len(data))
		for provider := range data {
			providers = append(providers, provider)
		}
		sort.Strings(providers)
		return data, false, nil
	})
	if err != nil {
		return nil, err
	}

	return providers, nil
}

// Refresh refreshes an expired OAuth credential for provider.
func (s *FileStore) Refresh(
	ctx context.Context,
	provider string,
	subscriptionProvider SubscriptionProvider,
) (StoredCredential, bool, error) {
	provider = normalizeProvider(provider)
	if provider == "" {
		return StoredCredential{}, false, errors.New("provider is required")
	}
	if subscriptionProvider == nil {
		return StoredCredential{}, false, errors.New("subscription provider is required")
	}

	var refreshed StoredCredential
	var ok bool
	err := s.withLockedData(false, func(data map[string]StoredCredential) (map[string]StoredCredential, bool, error) {
		current, exists := data[provider]
		if !exists || current.Type != TypeOAuth {
			return data, false, nil
		}
		if !IsExpired(current) {
			refreshed = cloneCredential(current)
			ok = true
			return data, false, nil
		}

		next, err := subscriptionProvider.Refresh(ctx, current)
		if err != nil {
			return data, false, err
		}
		next = normalizeCredential(next)
		if next.Type == "" {
			next.Type = TypeOAuth
		}
		if next.Type != TypeOAuth {
			return data, false, errors.New("refreshed credential must be OAuth")
		}
		tokenValue := str.String(next.Token)
		if tokenValue.Trim() == "" {
			return data, false, errors.New("refreshed OAuth token credential is required")
		}
		data[provider] = next
		refreshed = cloneCredential(next)
		ok = true
		return data, true, nil
	})
	if err != nil {
		credential, exists, loadErr := s.Get(provider)
		if loadErr == nil && exists && credential.Type == TypeOAuth && !IsExpired(credential) {
			return credential, true, nil
		}

		return StoredCredential{}, false, err
	}

	return refreshed, ok, nil
}

func (s *FileStore) withLockedData(
	create bool,
	fn func(map[string]StoredCredential) (map[string]StoredCredential, bool, error),
) error {
	if s == nil {
		return errors.New("credential store is required")
	}

	pathValue2 := str.String(s.Path)
	path := pathValue2.Trim()
	if path == "" {
		path = DefaultPath()
	}
	if !create {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			_, _, err := fn(make(map[string]StoredCredential))
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create credential dir: %w", err)
	}

	release, err := acquireFileLock(path + ".lock")
	if err != nil {
		return err
	}
	defer release()

	document, err := loadDocument(path)
	if err != nil {
		return err
	}
	data, err := providersFromDocument(document)
	if err != nil {
		return err
	}
	next, changed, err := fn(data)
	if err != nil {
		return err
	}
	if changed || create {
		if next == nil {
			next = make(map[string]StoredCredential)
		}
		document, err = replaceProviders(document, next)
		if err != nil {
			return err
		}
		if err := writeDocument(path, document); err != nil {
			return err
		}
	}

	return nil
}

func loadData(path string) (map[string]StoredCredential, error) {
	document, err := loadDocument(path)
	if err != nil {
		return nil, err
	}

	return providersFromDocument(document)
}

func writeData(path string, data map[string]StoredCredential) error {
	document, err := replaceProviders(make(rawDocument), data)
	if err != nil {
		return err
	}

	return writeDocument(path, document)
}

func acquireFileLock(path string) (func(), error) {
	lock := flock.New(path)
	var lastErr error
	for range lockRetries {
		locked, err := lock.TryLock()
		if err == nil && locked {
			return func() { _ = lock.Unlock() }, nil
		}
		if err != nil {
			return nil, fmt.Errorf("acquire credential store lock: %w", err)
		}
		lastErr = errors.New("credential store is locked")
		time.Sleep(lockRetryDelay)
	}

	return nil, fmt.Errorf("acquire credential store lock: %w", lastErr)
}
