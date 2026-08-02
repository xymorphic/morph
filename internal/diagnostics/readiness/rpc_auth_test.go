package readiness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/credential"
	"github.com/xymorphic/morph/internal/profile"
)

func TestBuildRPCAuthGroup_ReportsSafeSourcesWithoutSecrets(t *testing.T) {
	home := t.TempDir()
	store := credential.NewFileStore(filepath.Join(home, "auth.json"))
	identity, err := store.LoadOrCreateIdentity()
	require.NoError(t, err)
	cfg := config.NewDefaultConfig()
	cfg.Auth.Token = "secret-token"

	group := buildRPCAuthGroup(cfg, profile.Profile{Name: "test", HomeDir: home})

	identityCheck := getReadinessGroupCheck(t, group, "identity")
	require.Equal(t, StatusPass, identityCheck.Status)
	require.Contains(t, identityCheck.Message, identity.ID)
	tokenCheck := getReadinessGroupCheck(t, group, "token source")
	require.Equal(t, StatusPass, tokenCheck.Status)
	require.NotContains(t, tokenCheck.Message, cfg.Auth.Token)
	require.Equal(t, StatusPass, getReadinessGroupCheck(t, group, "transport").Status)
}

func TestBuildRPCAuthGroup_RejectsUnsafeTransportAndKeyReference(t *testing.T) {
	cfg := config.NewDefaultConfig()
	cfg.RPC.Address = "0.0.0.0"
	cfg.Auth.Key = filepath.Join(t.TempDir(), "missing.pem")

	group := buildRPCAuthGroup(cfg, profile.Profile{})

	require.Equal(t, StatusFail, getReadinessGroupCheck(t, group, "identity").Status)
	require.Equal(t, StatusFail, getReadinessGroupCheck(t, group, "transport").Status)

	keyPath := cfg.Auth.Key
	require.NoError(t, os.WriteFile(keyPath, []byte("not-a-key"), 0o600))
	group = buildRPCAuthGroup(cfg, profile.Profile{})
	require.Equal(t, StatusFail, getReadinessGroupCheck(t, group, "identity").Status)
}
