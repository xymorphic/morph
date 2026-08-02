package daemon

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	urfavecli "github.com/urfave/cli/v3"

	daemoncmd "github.com/xymorphic/morph/cmd/daemon"
	morphcli "github.com/xymorphic/morph/internal/cli"
	"github.com/xymorphic/morph/internal/datadir"
	"github.com/xymorphic/morph/internal/e2e"
	"github.com/xymorphic/morph/internal/profile"
	"github.com/xymorphic/morph/pkg/logutils"
)

func init() {
	logutils.SetOutput(io.Discard)
}

func Test_E2E_DaemonCommand_BootsAndServesRPC(t *testing.T) {
	resetDaemonCommandE2E(t)
	activeProfile := profile.Active()
	require.NotEmpty(t, activeProfile.HomeDir)
	require.Equal(t, filepath.Join(activeProfile.HomeDir, "data", "state.db"), datadir.StateDBPath())

	var startupBuffer bytes.Buffer
	previousOutput := daemoncmd.SetOutput(&startupBuffer)
	t.Cleanup(func() {
		daemoncmd.SetOutput(previousOutput)
	})

	port, err := e2e.ReserveRPCPort()
	require.NoError(t, err)

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(fmt.Sprintf(`
name: yaml-up
rpc:
  address: 127.0.0.1
  port: %d
`, port)), 0o600))

	envPath := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(envPath, []byte("MORPH_NAME=env-up\n"), 0o600))

	envFile := ""
	configFile := ""
	rootCmd := &urfavecli.Command{
		Name:  "morph",
		Flags: morphcli.RootFlags(&envFile, &configFile),
		Commands: []*urfavecli.Command{
			daemoncmd.NewCommand(),
		},
	}

	runCtx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- rootCmd.Run(runCtx, []string{
			"morph",
			"--env-file", envPath,
			"--config", configPath,
			"--name", "cli-up",
			"daemon",
		})
	}()

	client, err := e2e.WaitForRPC("127.0.0.1", port, 5*time.Second)
	require.NoError(t, err)

	t.Cleanup(func() {
		require.NoError(t, client.Close())
		cancel()
		require.NoError(t, <-errCh)
	})

	current, err := client.Session.Current(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, current.ID)
	assert.Contains(t, startupBuffer.String(), "cli-up")
	assert.Contains(t, startupBuffer.String(), fmt.Sprintf("127.0.0.1:%d", port))
}

func resetDaemonCommandE2E(t *testing.T) {
	t.Helper()

	originalProfile := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(originalProfile)
	})
	testHome := t.TempDir()
	profile.SetActive(profile.WithMetadataPaths(profile.Profile{
		Name:    profile.DefaultName,
		HomeDir: filepath.Join(testHome, ".morph", "profiles", profile.DefaultName),
	}))
	t.Setenv("HOME", testHome)
	clearDaemonCommandEnv(t,
		"MORPH_NAME",
		"OPENAI_API_KEY",
		"OPENROUTER_API_KEY",
		"ANTHROPIC_API_KEY",
		"COPILOT_GITHUB_TOKEN",
	)
}

func clearDaemonCommandEnv(t *testing.T, keys ...string) {
	t.Helper()

	for _, key := range keys {
		original, ok := os.LookupEnv(key)
		if ok {
			t.Cleanup(func() {
				require.NoError(t, os.Setenv(key, original))
			})
		} else {
			t.Cleanup(func() {
				require.NoError(t, os.Unsetenv(key))
			})
		}
		require.NoError(t, os.Unsetenv(key))
	}
}
