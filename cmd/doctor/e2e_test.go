package doctor

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	cli "github.com/urfave/cli/v3"

	morphcli "github.com/xymorphic/morph/internal/cli"
	"github.com/xymorphic/morph/internal/profile"
	"github.com/xymorphic/morph/pkg/logutils"
)

func init() {
	logutils.SetOutput(io.Discard)
}

func Test_E2E_DoctorCommand_ConfigPassAndFail(t *testing.T) {
	originalProfile := profile.Active()
	t.Cleanup(func() { profile.SetActive(originalProfile) })
	profile.SetActive(profile.WithMetadataPaths(profile.Profile{
		Name:    "doctor-e2e",
		HomeDir: t.TempDir(),
	}))

	t.Run("passes for valid config", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.yaml")
		require.NoError(t, os.WriteFile(configPath, []byte(`
name: config-agent
models:
  providers:
    openrouter:
      apiKey: config-key
  main:
    name: gpt-4o-mini
    provider: openrouter
`), 0o600))

		output, err := runDoctorCommand(t, "morph", "--config", configPath, "doctor")
		require.NoError(t, err)
		assert.Contains(t, output, "config validation: configuration is valid")
		assert.Contains(t, output, "doctor checks passed")
	})

	t.Run("fails clearly for invalid config", func(t *testing.T) {
		t.Setenv("OPENROUTER_API_KEY", "")

		configPath := filepath.Join(t.TempDir(), "config.yaml")
		require.NoError(t, os.WriteFile(configPath, []byte(`
name: config-agent
models:
  main:
    name: gpt-4o-mini
    provider: openrouter
search:
  vector:
    enabled: false
`), 0o600))

		output, err := runDoctorCommand(t, "morph", "--config", configPath, "doctor")
		require.ErrorContains(t, err, "model API key is required")
		assert.Contains(t, output, "config validation")
		assert.Contains(t, output, "models:")
	})
}

func runDoctorCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()

	originalOutput := doctorOutput
	t.Cleanup(func() {
		doctorOutput = originalOutput
	})

	var output bytes.Buffer
	doctorOutput = &output

	envFile := ".env"
	configFile := "config.yaml"

	cmd := &cli.Command{
		Name:  "morph",
		Flags: morphcli.RootFlags(&envFile, &configFile),
		Commands: []*cli.Command{
			NewCommand(),
		},
	}

	err := cmd.Run(context.Background(), args)
	return output.String(), err
}
