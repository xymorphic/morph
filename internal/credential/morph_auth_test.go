package credential

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileStore_MorphIdentityPreservesProviderCredentials(t *testing.T) {
	store := NewFileStore(t.TempDir() + "/auth.json")
	require.NoError(t, store.Set("openai", StoredCredential{Type: TypeAPIKey, Key: "provider-key"}))

	identity, err := store.LoadOrCreateIdentity()
	require.NoError(t, err)
	credential, ok, err := store.Get("openai")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "provider-key", credential.Key)

	providers, err := store.List()
	require.NoError(t, err)
	require.Equal(t, []string{"openai"}, providers)
	record, ok, err := store.LoadMorphAuth()
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, identity.ID, record.IdentityID)
	require.Equal(t, hex.EncodeToString(identity.PrivateKey.Seed()), record.PrivateKey)
}

func TestFileStore_LoadOrCreateIdentityIsConcurrentAndStable(t *testing.T) {
	store := NewFileStore(t.TempDir() + "/auth.json")
	const workers = 12
	ids := make(chan string, workers)
	errs := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			identity, err := store.LoadOrCreateIdentity()
			if err != nil {
				errs <- err
				return
			}
			ids <- identity.ID
		}()
	}
	wait.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var expected string
	for id := range ids {
		if expected == "" {
			expected = id
		}
		require.Equal(t, expected, id)
	}
}

func TestFileStore_RejectsReservedProviderName(t *testing.T) {
	store := NewFileStore(t.TempDir() + "/auth.json")
	err := store.Set(morphAuthDocumentKey, StoredCredential{Type: TypeAPIKey, Key: "key"})
	require.EqualError(t, err, "provider name is reserved")
	_, _, err = store.Get(morphAuthDocumentKey)
	require.EqualError(t, err, "provider name is reserved")
	err = store.Remove(morphAuthDocumentKey)
	require.EqualError(t, err, "provider name is reserved")
}

func TestFileStore_RotateIdentityAdvancesGeneration(t *testing.T) {
	store := NewFileStore(t.TempDir() + "/auth.json")
	first, err := store.LoadOrCreateIdentity()
	require.NoError(t, err)

	second, err := store.RotateIdentity()
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID)
	require.Equal(t, first.Generation+1, second.Generation)
}

func TestFileStore_IdentityRotationCanActivateOrRecoverByAbort(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "auth.json"))
	first, err := store.LoadOrCreateIdentity()
	require.NoError(t, err)

	current, pending, err := store.PrepareIdentityRotation()
	require.NoError(t, err)
	require.Equal(t, first.ID, current.IdentityID)
	loaded, err := store.LoadOrCreateIdentity()
	require.NoError(t, err)
	require.Equal(t, first.ID, loaded.ID)
	record, found, err := store.LoadMorphAuth()
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, pending.ID, record.Pending.IdentityID)
	require.Equal(t, hex.EncodeToString(pending.PrivateKey.Seed()), record.Pending.PrivateKey)

	require.NoError(t, store.AbortIdentityRotation(pending.ID))
	record, found, err = store.LoadMorphAuth()
	require.NoError(t, err)
	require.True(t, found)
	require.Nil(t, record.Pending)
	require.Equal(t, first.ID, record.IdentityID)

	_, pending, err = store.PrepareIdentityRotation()
	require.NoError(t, err)
	require.NoError(t, store.ActivateIdentityRotation(pending.ID))
	record, found, err = store.LoadMorphAuth()
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, pending.ID, record.IdentityID)
	require.Equal(t, pending.Generation, record.Generation)
	require.Nil(t, record.Pending)
}

func TestFileStore_RejectsOverexposedIdentityFile(t *testing.T) {
	directory := t.TempDir()
	require.NoError(t, os.Chmod(directory, 0o700))
	path := filepath.Join(directory, "auth.json")
	store := NewFileStore(path)
	_, err := store.LoadOrCreateIdentity()
	require.NoError(t, err)
	require.NoError(t, os.Chmod(path, 0o644))

	_, _, err = store.LoadMorphAuth()
	require.ErrorContains(t, err, "permissions are too broad")
}
