package readiness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/permissions"
)

func TestBuildBrowserGroup_ReportsDisabledAndEnabledReadiness(t *testing.T) {
	cfg := browserReadinessConfig(t)
	group := buildBrowserGroup(context.Background(), cfg)
	require.Equal(t, StatusPass, getReadinessGroupCheck(t, group, "status").Status)
	require.Contains(t, getReadinessGroupCheck(t, group, "profiles").Message, "skipped")

	originalDiscover := discoverBrowserExecutable
	discoverBrowserExecutable = func(string) (string, error) { return "/usr/bin/chromium", nil }
	t.Cleanup(func() { discoverBrowserExecutable = originalDiscover })
	cfg.Browser.Enabled = true
	enabled := true
	cfg.Cap.Browser = &enabled
	group = buildBrowserGroup(context.Background(), cfg)
	require.Equal(t, StatusPass, getReadinessGroupCheck(t, group, "status").Status)
	require.Contains(t, getReadinessGroupCheck(t, group, "executable").Message, "/usr/bin/chromium")
}

func TestBuildBrowserGroup_ReportsCapabilityExecutableAndSecurityProblems(t *testing.T) {
	cfg := browserReadinessConfig(t)
	cfg.Browser.Enabled = true
	originalDiscover := discoverBrowserExecutable
	discoverBrowserExecutable = func(string) (string, error) { return "", errors.New("Chromium not found") }
	t.Cleanup(func() { discoverBrowserExecutable = originalDiscover })

	group := buildBrowserGroup(context.Background(), cfg)
	require.Equal(t, StatusWarn, getReadinessGroupCheck(t, group, "status").Status)
	require.Equal(t, StatusFail, getReadinessGroupCheck(t, group, "executable").Status)
	require.NotEmpty(t, getReadinessGroupCheck(t, group, "executable").Actions)

	cfg.Permissions.Preset = permissions.PresetFullAccess
	group = buildBrowserGroup(context.Background(), cfg)
	require.Equal(t, StatusWarn, getReadinessGroupCheck(t, group, "security").Status)
	require.Contains(t, getReadinessGroupCheck(t, group, "security").Message, "bypasses")
}

func TestBuildBrowserProfileCheck_ValidatesRemoteEndpointPolicyAndReachability(t *testing.T) {
	cfg := browserReadinessConfig(t)
	cfg.Browser.Enabled = true
	cfg.Browser.Profiles = []config.BrowserProfileConfig{{
		Name: "remote", Mode: config.BrowserProfileRemoteCDP, CDPEndpoint: "http://127.0.0.1:9222",
	}}
	cfg.Browser.DefaultProfile = "remote"

	checkValue := buildBrowserProfileCheck(context.Background(), cfg)
	require.Equal(t, StatusWarn, checkValue.Status)
	require.Contains(t, checkValue.Message, "endpoint is not ready")

	originalDial := dialBrowserEndpoint
	dialBrowserEndpoint = func(context.Context, string, string) error { return errors.New("connection refused") }
	t.Cleanup(func() { dialBrowserEndpoint = originalDial })
	cfg.Browser.Network.DevelopmentAllowedHosts = []string{"localhost"}
	cfg.Browser.Profiles[0].CDPEndpoint = "http://localhost:9222"
	checkValue = buildBrowserProfileCheck(context.Background(), cfg)
	require.Equal(t, StatusWarn, checkValue.Status)
	require.Contains(t, checkValue.Message, "connection refused")

	dialBrowserEndpoint = func(context.Context, string, string) error { return nil }
	checkValue = buildBrowserProfileCheck(context.Background(), cfg)
	require.Equal(t, StatusPass, checkValue.Status)
	require.Contains(t, checkValue.Message, "remote=1")
}

func TestBuildBrowserStorageCheck_FindsStaleRuntimeDirectories(t *testing.T) {
	cfg := browserReadinessConfig(t)
	stalePaths := []string{
		filepath.Join(cfg.Browser.TemporaryRoot, "browser-profile-old"),
		filepath.Join(cfg.Browser.TemporaryRoot, "uploads", "browser_old"),
		filepath.Join(cfg.Browser.Artifacts.Root, ".downloads", "browser_old"),
	}
	for _, path := range stalePaths {
		require.NoError(t, os.MkdirAll(path, 0o700))
		old := time.Now().Add(-2 * cfg.Browser.Artifacts.Retention)
		require.NoError(t, os.Chtimes(path, old, old))
	}

	checkValue := buildBrowserStorageCheck(cfg)
	require.Equal(t, StatusWarn, checkValue.Status)
	require.Contains(t, checkValue.Message, "3 stale")

	require.NoError(t, os.Remove(cfg.Browser.ProfileRoot))
	require.NoError(t, os.WriteFile(cfg.Browser.ProfileRoot, []byte("not a directory"), 0o600))
	checkValue = buildBrowserStorageCheck(cfg)
	require.Equal(t, StatusFail, checkValue.Status)
}

func TestBuildBrowserGroup_RejectsMissingConfig(t *testing.T) {
	group := buildBrowserGroup(context.Background(), nil)
	require.Equal(t, StatusFail, getReadinessGroupCheck(t, group, "config").Status)
}

func browserReadinessConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := config.NewDefaultConfig()
	root := t.TempDir()
	cfg.Browser.ProfileRoot = filepath.Join(root, "profiles")
	cfg.Browser.TemporaryRoot = filepath.Join(root, "temporary")
	cfg.Browser.Artifacts.Root = filepath.Join(root, "artifacts")
	for _, path := range []string{cfg.Browser.ProfileRoot, cfg.Browser.TemporaryRoot, cfg.Browser.Artifacts.Root} {
		require.NoError(t, os.MkdirAll(path, 0o700))
	}
	return cfg
}
