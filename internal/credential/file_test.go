package credential

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/profile"
)

func TestDefaultPathUsesActiveProfile(t *testing.T) {
	restoreActiveProfile(t)
	home := t.TempDir()
	profile.SetActive(profile.Profile{Name: "test", HomeDir: home})

	require.Equal(t, filepath.Join(home, "auth.json"), DefaultPath())
	require.Equal(t, filepath.Join(home, "auth.json"), NewFileStore("").Path)
}

func TestLoadStoredProviderCredentialUsesDefaultFileStore(t *testing.T) {
	restoreActiveProfile(t)
	home := t.TempDir()
	profile.SetActive(profile.Profile{Name: "test", HomeDir: home})
	require.NoError(t, NewFileStore("").Set("openai", StoredCredential{Type: TypeAPIKey, Key: "key"}))

	credential, err := LoadStoredProviderCredential("openai")
	require.NoError(t, err)
	require.Equal(t, "key", credential.Key)
}

func TestRefreshStoredProviderCredentialUsesRegisteredProvider(t *testing.T) {
	restoreActiveProfile(t)
	home := t.TempDir()
	providerName := "test-refresh-default"
	expired := time.Now().Add(-time.Minute)
	profile.SetActive(profile.Profile{Name: "test", HomeDir: home})
	require.NoError(t, NewFileStore("").Set(providerName, StoredCredential{
		Type:      TypeOAuth,
		Token:     "old",
		ExpiresAt: &expired,
	}))

	_, ok, err := RefreshStoredProviderCredential(context.Background(), "missing-refresh-default")
	require.NoError(t, err)
	require.False(t, ok)

	RegisterSubscriptionProvider(providerName, &refreshProvider{
		next: StoredCredential{Type: TypeOAuth, Token: "new"},
	})

	credential, ok, err := RefreshStoredProviderCredential(context.Background(), providerName)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "new", credential.Token)
}

func TestFileStore_SetCreatesPrivateCredentialFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "auth.json")
	store := NewFileStore(path)

	require.NoError(t, store.Set("OpenAI", StoredCredential{Type: TypeAPIKey, Key: "sk-test"}))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	parent, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), parent.Mode().Perm())
}

func TestFileStore_GetNormalizesProviderAndClonesCredential(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	expiresAt := time.Now().Add(time.Hour).UTC()
	store := NewFileStore(path)
	require.NoError(t, store.Set("Anthropic", StoredCredential{
		Type:      TypeOAuth,
		Token:     "token",
		Refresh:   "refresh",
		ExpiresAt: &expiresAt,
		Scopes:    []string{"read", "read", "write"},
	}))

	credential, ok, err := store.Get(" anthropic ")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, TypeOAuth, credential.Type)
	require.Equal(t, "token", credential.Token)
	require.Equal(t, []string{"read", "write"}, credential.Scopes)

	credential.Scopes[0] = "mutated"
	credential, ok, err = store.Get("anthropic")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []string{"read", "write"}, credential.Scopes)
}

func TestFileStore_GetRejectsMissingProvider(t *testing.T) {
	_, _, err := NewFileStore(filepath.Join(t.TempDir(), "auth.json")).Get("")

	require.EqualError(t, err, "provider is required")
}

func TestFileStore_GetMissingCredentialDoesNotCreateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")

	_, ok, err := NewFileStore(path).Get("openai")
	require.NoError(t, err)
	require.False(t, ok)
	require.NoFileExists(t, path)
}

func TestFileStore_RemoveDeletesCredential(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "auth.json"))
	require.NoError(t, store.Set("openai", StoredCredential{Type: TypeAPIKey, Key: "key"}))

	require.NoError(t, store.Remove("openai"))

	_, ok, err := store.Get("openai")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestFileStore_RemoveMissingCredentialDoesNotCreateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	store := NewFileStore(path)

	require.NoError(t, store.Remove("openai"))

	require.NoFileExists(t, path)
}

func TestFileStore_RemoveRejectsMissingProvider(t *testing.T) {
	err := NewFileStore(filepath.Join(t.TempDir(), "auth.json")).Remove("")

	require.EqualError(t, err, "provider is required")
}

func TestFileStore_ListReturnsSortedProviders(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "auth.json"))
	require.NoError(t, store.Set("openrouter", StoredCredential{Type: TypeAPIKey, Key: "router"}))
	require.NoError(t, store.Set("anthropic", StoredCredential{Type: TypeAPIKey, Key: "ant"}))

	providers, err := store.List()
	require.NoError(t, err)
	require.Equal(t, []string{"anthropic", "openrouter"}, providers)
}

func TestFileStore_ListMissingStoreReturnsEmptyList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")

	providers, err := NewFileStore(path).List()
	require.NoError(t, err)
	require.Empty(t, providers)
	require.NoFileExists(t, path)
}

func TestFileStore_RefreshUpdatesExpiredOAuthCredential(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "auth.json"))
	expired := time.Now().Add(-time.Minute)
	nextExpiry := time.Now().Add(time.Hour)
	require.NoError(t, store.Set("github-copilot", StoredCredential{
		Type:      TypeOAuth,
		Token:     "old",
		Refresh:   "refresh",
		ExpiresAt: &expired,
	}))

	provider := &refreshProvider{
		next: StoredCredential{Type: TypeOAuth, Token: "new", Refresh: "refresh", ExpiresAt: &nextExpiry},
	}
	credential, ok, err := store.Refresh(context.Background(), "github-copilot", provider)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "new", credential.Token)
	require.Equal(t, 1, provider.calls)

	stored, ok, err := store.Get("github-copilot")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "new", stored.Token)
}

func TestFileStore_RefreshDefaultsOAuthType(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "auth.json"))
	expired := time.Now().Add(-time.Minute)
	require.NoError(t, store.Set("github-copilot", StoredCredential{
		Type:      TypeOAuth,
		Token:     "old",
		ExpiresAt: &expired,
	}))

	credential, ok, err := store.Refresh(context.Background(), "github-copilot", &refreshProvider{
		next: StoredCredential{Token: "new"},
	})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, TypeOAuth, credential.Type)
	require.Equal(t, "new", credential.Token)
}

func TestFileStore_RefreshSkipsUnexpiredOAuthCredential(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "auth.json"))
	expiresAt := time.Now().Add(time.Hour)
	require.NoError(t, store.Set("github-copilot", StoredCredential{
		Type:      TypeOAuth,
		Token:     "current",
		ExpiresAt: &expiresAt,
	}))

	provider := &refreshProvider{err: errors.New("should not refresh")}
	credential, ok, err := store.Refresh(context.Background(), "github-copilot", provider)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "current", credential.Token)
	require.Zero(t, provider.calls)
}

func TestFileStore_RefreshMissingCredentialDoesNotCreateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	store := NewFileStore(path)

	_, ok, err := store.Refresh(context.Background(), "github-copilot", &refreshProvider{})
	require.NoError(t, err)
	require.False(t, ok)
	require.NoFileExists(t, path)
}

func TestFileStore_RefreshSkipsAPIKeyCredential(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "auth.json"))
	require.NoError(t, store.Set("openai", StoredCredential{Type: TypeAPIKey, Key: "key"}))

	_, ok, err := store.Refresh(context.Background(), "openai", &refreshProvider{err: errors.New("unused")})
	require.NoError(t, err)
	require.False(t, ok)
}

func TestFileStore_RefreshRejectsInvalidInput(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "auth.json"))

	_, _, err := store.Refresh(context.Background(), "", &refreshProvider{})
	require.EqualError(t, err, "provider is required")

	_, _, err = store.Refresh(context.Background(), "openai", nil)
	require.EqualError(t, err, "subscription provider is required")
}

func TestFileStore_RefreshRejectsInvalidRefreshedCredential(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "auth.json"))
	expired := time.Now().Add(-time.Minute)
	require.NoError(t, store.Set("github-copilot", StoredCredential{
		Type:      TypeOAuth,
		Token:     "old",
		ExpiresAt: &expired,
	}))

	_, _, err := store.Refresh(context.Background(), "github-copilot", &refreshProvider{
		next: StoredCredential{Type: TypeAPIKey, Key: "key"},
	})
	require.EqualError(t, err, "refreshed credential must be OAuth")

	_, _, err = store.Refresh(context.Background(), "github-copilot", &refreshProvider{
		next: StoredCredential{Type: TypeOAuth},
	})
	require.EqualError(t, err, "refreshed OAuth token credential is required")
}

func TestFileStore_RefreshReturnsFreshCredentialAfterRefreshError(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "auth.json"))
	expired := time.Now().Add(-time.Minute)
	fresh := time.Now().Add(time.Hour)
	require.NoError(t, store.Set("github-copilot", StoredCredential{
		Type:      TypeOAuth,
		Token:     "old",
		ExpiresAt: &expired,
	}))

	provider := &refreshProvider{
		err: errors.New("refresh failed"),
		onRefresh: func() {
			require.NoError(t, writeData(store.Path, map[string]StoredCredential{
				"github-copilot": {
					Type:      TypeOAuth,
					Token:     "fresh",
					ExpiresAt: &fresh,
				},
			}))
		},
	}

	credential, ok, err := store.Refresh(context.Background(), "github-copilot", provider)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "fresh", credential.Token)
}

func TestFileStore_SetValidatesCredential(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "auth.json"))

	err := store.Set("openai", StoredCredential{Type: TypeAPIKey})
	require.EqualError(t, err, "API key credential is required")
}

func TestFileStore_SetRejectsMissingProvider(t *testing.T) {
	err := NewFileStore(filepath.Join(t.TempDir(), "auth.json")).Set("", StoredCredential{
		Type: TypeAPIKey,
		Key:  "key",
	})

	require.EqualError(t, err, "provider is required")
}

func TestFileStore_SetRejectsUnknownCredentialType(t *testing.T) {
	err := NewFileStore(filepath.Join(t.TempDir(), "auth.json")).Set("openai", StoredCredential{
		Type: "session",
		Key:  "key",
	})

	require.EqualError(t, err, "credential type must be one of: api_key, oauth")
}

func TestFileStore_SetRejectsMissingCredentialType(t *testing.T) {
	err := NewFileStore(filepath.Join(t.TempDir(), "auth.json")).Set("openai", StoredCredential{
		Key: "key",
	})

	require.EqualError(t, err, "credential type is required")
}

func TestFileStore_SetRejectsMissingOAuthToken(t *testing.T) {
	err := NewFileStore(filepath.Join(t.TempDir(), "auth.json")).Set("github-copilot", StoredCredential{
		Type: TypeOAuth,
	})

	require.EqualError(t, err, "OAuth token credential is required")
}

func TestFileStore_NilReceiverReturnsError(t *testing.T) {
	var store *FileStore

	_, err := store.List()
	require.EqualError(t, err, "credential store is required")
}

func TestFileStore_ReturnsParseError(t *testing.T) {
	directory := t.TempDir()
	require.NoError(t, os.Chmod(directory, 0o700))
	path := filepath.Join(directory, "auth.json")
	require.NoError(t, os.WriteFile(path, []byte("{"), 0o600))

	_, err := NewFileStore(path).List()
	require.ErrorContains(t, err, "parse credential store")
}

func TestFileStore_GetReturnsLoadError(t *testing.T) {
	directory := t.TempDir()
	require.NoError(t, os.Chmod(directory, 0o700))
	path := filepath.Join(directory, "auth.json")
	require.NoError(t, os.WriteFile(path, []byte("{"), 0o600))

	_, _, err := NewFileStore(path).Get("openai")
	require.ErrorContains(t, err, "parse credential store")
}

func TestFileStore_LoadsEmptyCredentialFile(t *testing.T) {
	directory := t.TempDir()
	require.NoError(t, os.Chmod(directory, 0o700))
	path := filepath.Join(directory, "auth.json")
	require.NoError(t, os.WriteFile(path, []byte(" \n"), 0o600))

	providers, err := NewFileStore(path).List()
	require.NoError(t, err)
	require.Empty(t, providers)
}

func TestFileStore_NormalizesStoredProviderKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, writeData(path, map[string]StoredCredential{
		" OpenAI ": {Type: TypeAPIKey, Key: "key"},
		" ":        {Type: TypeAPIKey, Key: "ignored"},
	}))

	credential, ok, err := NewFileStore(path).Get("openai")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "key", credential.Key)
}

func TestFileStore_NormalizesEmptyScopes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	store := NewFileStore(path)
	require.NoError(t, store.Set("github-copilot", StoredCredential{
		Type:   TypeOAuth,
		Token:  "token",
		Scopes: []string{" ", ""},
	}))

	credential, ok, err := store.Get("github-copilot")
	require.NoError(t, err)
	require.True(t, ok)
	require.Nil(t, credential.Scopes)
}

func TestFileStore_LockTimeoutReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	lock := flock.New(path + ".lock")
	require.NoError(t, lock.Lock())
	t.Cleanup(func() { require.NoError(t, lock.Unlock()) })
	originalRetries := lockRetries
	originalDelay := lockRetryDelay
	lockRetries = 1
	lockRetryDelay = 0
	t.Cleanup(func() {
		lockRetries = originalRetries
		lockRetryDelay = originalDelay
	})

	err := NewFileStore(path).Set("openai", StoredCredential{Type: TypeAPIKey, Key: "key"})
	require.ErrorContains(t, err, "acquire credential store lock")
}

func TestFileStore_LockReturnsOpenError(t *testing.T) {
	_, err := acquireFileLock(filepath.Join(t.TempDir(), "missing", "auth.json.lock"))

	require.ErrorContains(t, err, "acquire credential store lock")
}

func TestFileStore_LoadDataReturnsReadError(t *testing.T) {
	_, err := loadData(t.TempDir())

	require.ErrorContains(t, err, "read credential store")
}

func TestFileStore_WriteDataReturnsCreateTempError(t *testing.T) {
	err := writeData(filepath.Join(t.TempDir(), "missing", "auth.json"), map[string]StoredCredential{})

	require.ErrorContains(t, err, "create credential store temp file")
}

func TestFileStore_WithLockedDataWritesEmptyMapWhenCreateRequested(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	store := NewFileStore(path)

	err := store.withLockedData(true, func(map[string]StoredCredential) (map[string]StoredCredential, bool, error) {
		return nil, false, nil
	})
	require.NoError(t, err)
	require.FileExists(t, path)
}

func TestFileStore_ConcurrentRefreshUsesOneWriter(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "auth.json"))
	expired := time.Now().Add(-time.Minute)
	expiresAt := time.Now().Add(time.Hour)
	require.NoError(t, store.Set("github-copilot", StoredCredential{
		Type:      TypeOAuth,
		Token:     "old",
		ExpiresAt: &expired,
	}))

	provider := &refreshProvider{
		next:  StoredCredential{Type: TypeOAuth, Token: "new", ExpiresAt: &expiresAt},
		block: make(chan struct{}),
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			credential, ok, err := store.Refresh(context.Background(), "github-copilot", provider)
			if err != nil {
				errs <- err
				return
			}
			if !ok || credential.Token != "new" {
				errs <- errors.New("unexpected credential")
			}
		}()
	}
	close(provider.block)
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, 1, provider.calls)
}

type refreshProvider struct {
	mu        sync.Mutex
	next      StoredCredential
	err       error
	block     chan struct{}
	onRefresh func()
	calls     int
}

func (p *refreshProvider) Login(context.Context, LoginOptions) (StoredCredential, error) {
	return StoredCredential{}, errors.New("unused")
}

func (p *refreshProvider) Refresh(_ context.Context, _ StoredCredential) (StoredCredential, error) {
	if p.block != nil {
		<-p.block
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.calls++
	if p.onRefresh != nil {
		p.onRefresh()
	}
	if p.err != nil {
		return StoredCredential{}, p.err
	}

	return p.next, nil
}

func (p *refreshProvider) AuthHeaders(context.Context, StoredCredential) (map[string]string, error) {
	return nil, errors.New("unused")
}

func restoreActiveProfile(t *testing.T) {
	t.Helper()

	active := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(active)
	})
}
