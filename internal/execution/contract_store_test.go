package execution

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContractStore_DescribesReleaseAndCustomContracts(t *testing.T) {
	store := NewContractStore(t.TempDir())
	image := "ghcr.io/xymorphic/morph-sandbox@sha256:" + strings.Repeat("a", 64)
	release := contractStoreTestContract()
	releasePath, err := store.SaveRelease(image, release)
	require.NoError(t, err)

	provenance, err := store.Describe(image, releasePath)
	require.NoError(t, err)
	require.Equal(t, ContractProvenanceRelease, provenance.Kind)
	require.Equal(t, release.Digest(), provenance.OriginalDigest)
	require.Equal(t, release.Digest(), provenance.ActiveDigest)

	custom := release
	custom.Shell = "/usr/bin/sh"
	custom.Executables = map[string]string{"sh": "/usr/bin/sh"}
	customPath, err := store.SaveCustom(custom)
	require.NoError(t, err)
	provenance, err = store.Describe(image, customPath)
	require.NoError(t, err)
	require.Equal(t, ContractProvenanceCustom, provenance.Kind)
	require.Equal(t, release.Digest(), provenance.OriginalDigest)
	require.Equal(t, custom.Digest(), provenance.ActiveDigest)
}

func TestContractStore_PreservesReleaseContractByImageAndDigest(t *testing.T) {
	store := NewContractStore(t.TempDir())
	image := "registry.example/sandbox@sha256:" + strings.Repeat("b", 64)
	contract := contractStoreTestContract()

	first, err := store.SaveRelease(image, contract)
	require.NoError(t, err)
	second, err := store.SaveRelease(image, contract)
	require.NoError(t, err)

	require.Equal(t, first, second)
	require.Equal(t, filepath.Join(store.Root, "releases"), filepath.Dir(first))
	loadedPath, loaded, err := store.LoadRelease(image)
	require.NoError(t, err)
	require.Equal(t, first, loadedPath)
	require.Equal(t, contract.Digest(), loaded.Digest())
}

func TestContractStore_SeparatesReleaseAndActiveContracts(t *testing.T) {
	store := NewContractStore(t.TempDir())
	image := "registry.example/sandbox@sha256:" + strings.Repeat("c", 64)
	release := contractStoreTestContract()
	releasePath, err := store.SaveRelease(image, release)
	require.NoError(t, err)
	activePath, err := store.SaveActive(image, release)
	require.NoError(t, err)

	require.NotEqual(t, releasePath, activePath)
	custom := release
	custom.Shell = "/usr/bin/sh"
	custom.Executables = map[string]string{"sh": "/usr/bin/sh"}
	customPath, err := store.SaveActive(image, custom)
	require.NoError(t, err)
	require.NotEqual(t, activePath, customPath)

	_, preserved, err := store.LoadRelease(image)
	require.NoError(t, err)
	require.Equal(t, release.Digest(), preserved.Digest())
	provenance, err := store.Describe(image, customPath)
	require.NoError(t, err)
	require.Equal(t, ContractProvenanceCustom, provenance.Kind)
}

func TestContractStore_ResetRestoresActiveCopy(t *testing.T) {
	store := NewContractStore(t.TempDir())
	image := "registry.example/sandbox@sha256:" + strings.Repeat("d", 64)
	release := contractStoreTestContract()
	releasePath, err := store.SaveRelease(image, release)
	require.NoError(t, err)
	custom := release
	custom.Shell = "/usr/bin/sh"
	custom.Executables = map[string]string{"sh": "/usr/bin/sh"}
	activePath, err := store.SaveActive(image, custom)
	require.NoError(t, err)

	resetPath, reset, err := store.ResetActive(image)
	require.NoError(t, err)
	require.NotEqual(t, activePath, resetPath)
	require.NotEqual(t, releasePath, resetPath)
	require.Equal(t, release.Digest(), reset.Digest())
	provenance, err := store.Describe(image, resetPath)
	require.NoError(t, err)
	require.Equal(t, ContractProvenanceRelease, provenance.Kind)
}

func TestContractStore_LoadReleaseRejectsSymlinkAndDigestMismatch(t *testing.T) {
	store := NewContractStore(t.TempDir())
	image := "registry.example/sandbox@sha256:" + strings.Repeat("e", 64)
	release := contractStoreTestContract()
	releasePath, err := store.SaveRelease(image, release)
	require.NoError(t, err)
	data, err := os.ReadFile(releasePath)
	require.NoError(t, err)
	require.NoError(t, os.Remove(releasePath))
	target := filepath.Join(t.TempDir(), "target.json")
	require.NoError(t, os.WriteFile(target, data, 0o600))
	require.NoError(t, os.Symlink(target, releasePath))

	_, _, err = store.LoadRelease(image)
	require.ErrorContains(t, err, "is not a regular file")

	require.NoError(t, os.Remove(releasePath))
	tampered := release
	tampered.Shell = "/usr/bin/sh"
	tampered.Executables = map[string]string{"sh": "/usr/bin/sh"}
	_, tamperedData, err := encodeImageContract(tampered)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(releasePath, tamperedData, 0o600))
	_, _, err = store.LoadRelease(image)
	require.ErrorContains(t, err, "does not match filename digest")
}

func TestContractStore_RejectsMutableImageReference(t *testing.T) {
	store := NewContractStore(t.TempDir())

	_, err := store.SaveRelease("registry.example/sandbox:latest", contractStoreTestContract())

	require.EqualError(t, err, "sandbox image must be pinned by sha256 digest")
}

func contractStoreTestContract() ImageContract {
	return ImageContract{
		Version:       "1",
		GOOS:          "linux",
		Architectures: []string{"amd64", "arm64"},
		User:          "65532:65532",
		Shell:         "/bin/sh",
		PATH:          []string{"/bin", "/usr/bin"},
		Executables:   map[string]string{"sh": "/bin/sh"},
		Helper:        "/usr/local/bin/morph-sandbox",
		WorkspacePath: "/workspace",
		HomePath:      "/home/morph",
		TemporaryPath: "/tmp",
		ControlPath:   "/run/morph",
	}
}
