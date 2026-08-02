package providercmd

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/constants"
	modelprovider "github.com/xymorphic/morph/internal/model/provider"
	"github.com/xymorphic/morph/internal/profile"
)

func TestProviderConfigureCommandHandlesNilIO(t *testing.T) {
	cmd := NewProviderConfigureCommand(nil, nil)

	require.Equal(t, "configure", cmd.Name)
	require.NotNil(t, cmd.Action)
}

func TestProviderCommandPassesFlagsToSetupRunner(t *testing.T) {
	home, configPath := setupCommandTestProfile(t, "work")
	t.Setenv("HOME", home)

	var output bytes.Buffer
	cmd := NewProviderConfigureCommand(strings.NewReader(""), &output)

	err := cmd.Run(context.Background(), []string{
		"configure",
		"--profile", "work",
		"openai",
		"--model", "gpt-5.5",
		"--base-url", "https://proxy.example/v1",
		"--api", modelprovider.APIOpenAIResponses,
		"--api-key", "openai-key",
	})

	require.NoError(t, err)
	cfg, err := config.Load("", configPath)
	require.NoError(t, err)
	require.Equal(t, constants.ModelProviderOpenAI, cfg.Models.Main.Provider)
	require.Equal(t, "gpt-5.5", cfg.Models.Main.Name)
	require.Equal(t, modelprovider.APIOpenAIResponses, cfg.Models.Main.API)
	require.Equal(t, "https://proxy.example/v1", cfg.Models.Main.BaseURL)
	require.Equal(t, "openai-key", cfg.Models.Providers[constants.ModelProviderOpenAI].APIKey)
	require.Contains(t, output.String(), "Configured openai with model gpt-5.5")
}

func TestProviderCommandReadsProviderFlag(t *testing.T) {
	home, configPath := setupCommandTestProfile(t, "work")
	t.Setenv("HOME", home)

	var output bytes.Buffer
	cmd := NewProviderConfigureCommand(strings.NewReader(""), &output)

	err := cmd.Run(context.Background(), []string{
		"configure",
		"--profile", "work",
		"--provider", constants.ModelProviderOpenAI,
		"--model", "gpt-5.5",
		"--api-key", "openai-key",
	})

	require.NoError(t, err)
	cfg, err := config.Load("", configPath)
	require.NoError(t, err)
	require.Equal(t, constants.ModelProviderOpenAI, cfg.Models.Main.Provider)
	require.Equal(t, "gpt-5.5", cfg.Models.Main.Name)
	require.Contains(t, output.String(), "Configured openai with model gpt-5.5")
}

func TestProviderCommandProviderArgumentTakesPrecedenceOverFlag(t *testing.T) {
	home, configPath := setupCommandTestProfile(t, "work")
	t.Setenv("HOME", home)

	var output bytes.Buffer
	cmd := NewProviderConfigureCommand(strings.NewReader(""), &output)

	err := cmd.Run(context.Background(), []string{
		"configure",
		"--profile", "work",
		"--provider", constants.ModelProviderOllama,
		constants.ModelProviderOpenAI,
		"--model", "gpt-5.5",
		"--api-key", "openai-key",
	})

	require.NoError(t, err)
	cfg, err := config.Load("", configPath)
	require.NoError(t, err)
	require.Equal(t, constants.ModelProviderOpenAI, cfg.Models.Main.Provider)
	require.Equal(t, "gpt-5.5", cfg.Models.Main.Name)
	require.Contains(t, output.String(), "Configured openai with model gpt-5.5")
}

func TestProviderCommandHelpShowsAPIFlag(t *testing.T) {
	var output bytes.Buffer
	cmd := NewProviderConfigureCommand(strings.NewReader(""), io.Discard)
	cmd.Writer = &output

	err := cmd.Run(context.Background(), []string{"configure", "--help"})

	require.NoError(t, err)
	require.Contains(t, output.String(), "--provider")
	require.Contains(t, output.String(), "--api")
	require.Contains(t, output.String(), "--refresh")
	require.NotContains(t, output.String(), "--model.api")
}

func setupCommandTestProfile(t *testing.T, name string) (string, string) {
	t.Helper()

	originalProfile := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(originalProfile)
	})
	profile.SetActive(profile.Profile{})

	home := t.TempDir()
	profileHome := filepath.Join(home, ".morph", "profiles", name)
	require.NoError(t, os.MkdirAll(profileHome, 0o700))
	configPath := filepath.Join(profileHome, "config.yaml")
	cfg := config.NewProfileConfig()
	cfg.Name = name
	require.NoError(t, config.SaveYAML(configPath, cfg))

	return home, configPath
}
