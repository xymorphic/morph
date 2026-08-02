package configcmd

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	cli "github.com/urfave/cli/v3"

	morphcli "github.com/xymorphic/morph/internal/cli"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/profile"
)

func TestCommand_UpdatesSelectedProfileConfig(t *testing.T) {
	clearSetConfigEnv(t, "MORPH_CONFIG", "MORPH_ENV_FILE", "MORPH_PROFILE", "OPENROUTER_API_KEY")
	resetSetConfigProfileState(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := writeCommandProfileConfig(t, home, "work")

	var output bytes.Buffer
	cmd := newTestRootCommand(&output)

	require.NoError(t, cmd.Run(context.Background(), []string{
		"morph",
		"--profile", "work",
		"config",
		"set",
		"search.enableRank",
		"true",
	}))

	cfg, err := config.Load("", configPath)
	require.NoError(t, err)
	require.True(t, *cfg.Search.EnableRerank)
	require.Equal(t, "true (prev=false)\n", output.String())
}

func TestCommand_GetsSelectedProfileConfigValues(t *testing.T) {
	clearSetConfigEnv(t, "MORPH_CONFIG", "MORPH_ENV_FILE", "MORPH_PROFILE", "OPENROUTER_API_KEY")
	resetSetConfigProfileState(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	_ = writeCommandProfileConfig(t, home, "safety-manual")

	var output bytes.Buffer
	cmd := newTestRootCommand(&output)

	require.NoError(t, cmd.Run(context.Background(), []string{
		"morph",
		"config",
		"get",
		"-p", "safety-manual",
		"safety.pii",
		"search.enableRank",
	}))

	require.Equal(t, "safety.pii=false\nsearch.enableRerank=false\n", output.String())
}

func TestCommand_GetsSelectedProfileConfigValuesWithTrailingProfileFlag(t *testing.T) {
	clearSetConfigEnv(t, "MORPH_CONFIG", "MORPH_ENV_FILE", "MORPH_PROFILE", "OPENROUTER_API_KEY")
	resetSetConfigProfileState(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	_ = writeCommandProfileConfig(t, home, "safety-manual")

	var output bytes.Buffer
	cmd := newTestRootCommand(&output)

	require.NoError(t, cmd.Run(context.Background(), []string{
		"morph",
		"config",
		"get",
		"safety.pii",
		"--profile", "safety-manual",
	}))

	require.Equal(t, "false\n", output.String())
}

func TestCommand_GetRejectsUnknownProfile(t *testing.T) {
	clearSetConfigEnv(t, "MORPH_CONFIG", "MORPH_ENV_FILE", "MORPH_PROFILE", "OPENROUTER_API_KEY")
	resetSetConfigProfileState(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	cmd := newTestRootCommand(nil)

	err := cmd.Run(context.Background(), []string{
		"morph",
		"config",
		"get",
		"--profile", "missing",
		"safety.pii",
	})

	require.EqualError(t, err, `unknown profile "missing"`)
}

func TestCommand_GetRequiresPath(t *testing.T) {
	cmd := newTestRootCommand(nil)
	err := cmd.Run(context.Background(), []string{"morph", "config", "get"})

	require.EqualError(t, err, "config path is required")
}

func TestCommand_UpdatesSelectedProfileConfigWithInlineValueAndLocalProfileFlag(t *testing.T) {
	clearSetConfigEnv(t, "MORPH_CONFIG", "MORPH_ENV_FILE", "MORPH_PROFILE", "OPENROUTER_API_KEY")
	resetSetConfigProfileState(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := writeCommandProfileConfig(t, home, "safety-manual")

	var output bytes.Buffer
	cmd := newTestRootCommand(&output)

	require.NoError(t, cmd.Run(context.Background(), []string{
		"morph",
		"config",
		"set",
		"-p", "safety-manual",
		"safety.pii=true",
	}))

	cfg, err := config.Load("", configPath)
	require.NoError(t, err)
	require.NotNil(t, cfg.Safety.PII)
	require.True(t, *cfg.Safety.PII)
	require.Equal(t, "true (prev=false)\n", output.String())
}

func TestCommand_SetRejectsUnknownProfile(t *testing.T) {
	clearSetConfigEnv(t, "MORPH_CONFIG", "MORPH_ENV_FILE", "MORPH_PROFILE", "OPENROUTER_API_KEY")
	resetSetConfigProfileState(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	cmd := newTestRootCommand(nil)

	err := cmd.Run(context.Background(), []string{
		"morph",
		"config",
		"set",
		"-p", "missing",
		"safety.pii=true",
	})

	require.EqualError(t, err, `unknown profile "missing"`)
}

func TestCommand_UpdatesMultipleSelectedProfileConfigValues(t *testing.T) {
	clearSetConfigEnv(t, "MORPH_CONFIG", "MORPH_ENV_FILE", "MORPH_PROFILE", "OPENROUTER_API_KEY")
	resetSetConfigProfileState(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := writeCommandProfileConfig(t, home, "work")

	var output bytes.Buffer
	cmd := newTestRootCommand(&output)

	require.NoError(t, cmd.Run(context.Background(), []string{
		"morph",
		"--profile", "work",
		"config",
		"set",
		"search.enableRank=true",
		"safety.pii=true",
	}))

	cfg, err := config.Load("", configPath)
	require.NoError(t, err)
	require.True(t, *cfg.Search.EnableRerank)
	require.NotNil(t, cfg.Safety.PII)
	require.True(t, *cfg.Safety.PII)
	require.Equal(t, "search.enableRerank=true (prev=false)\nsafety.pii=true (prev=false)\n", output.String())
}

func TestCommand_UpdatesMultipleSelectedProfileConfigValuesWithSpacedPairs(t *testing.T) {
	clearSetConfigEnv(t, "MORPH_CONFIG", "MORPH_ENV_FILE", "MORPH_PROFILE", "OPENROUTER_API_KEY")
	resetSetConfigProfileState(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := writeCommandProfileConfig(t, home, "work")

	var output bytes.Buffer
	cmd := newTestRootCommand(&output)

	require.NoError(t, cmd.Run(context.Background(), []string{
		"morph",
		"--profile", "work",
		"config",
		"set",
		"search.enableRank", "true",
		"safety.pii", "true",
	}))

	cfg, err := config.Load("", configPath)
	require.NoError(t, err)
	require.True(t, *cfg.Search.EnableRerank)
	require.NotNil(t, cfg.Safety.PII)
	require.True(t, *cfg.Safety.PII)
	require.Equal(t, "search.enableRerank=true (prev=false)\nsafety.pii=true (prev=false)\n", output.String())
}

func TestCommand_RequiresPathAndValue(t *testing.T) {
	cmd := newTestRootCommand(nil)
	err := cmd.Run(context.Background(), []string{"morph", "config", "set", "search.enableRerank"})

	require.EqualError(t, err, "config path and value are required")
}

func newTestRootCommand(output io.Writer) *cli.Command {
	envFile := ".env"
	configFile := "config.yaml"
	return &cli.Command{
		Name:     "morph",
		Flags:    morphcli.RootFlags(&envFile, &configFile),
		Commands: []*cli.Command{NewCommand(output)},
	}
}

func clearSetConfigEnv(t *testing.T, keys ...string) {
	t.Helper()
	keys = append(keys, "OPENAI_API_KEY", "OPENROUTER_API_KEY", "ANTHROPIC_API_KEY", "COPILOT_GITHUB_TOKEN")

	for _, key := range keys {
		original, ok := os.LookupEnv(key)
		if ok {
			t.Cleanup(func() {
				_ = os.Setenv(key, original)
			})
		} else {
			t.Cleanup(func() {
				_ = os.Unsetenv(key)
			})
		}
		_ = os.Unsetenv(key)
	}
}

func resetSetConfigProfileState(t *testing.T) {
	t.Helper()

	originalProfile := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(originalProfile)
	})
	profile.SetActive(profile.Profile{})
}

func writeCommandProfileConfig(t *testing.T, home string, name string) string {
	t.Helper()

	profileHome := filepath.Join(home, ".morph", "profiles", name)
	require.NoError(t, os.MkdirAll(profileHome, 0o700))
	configPath := filepath.Join(profileHome, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: test-agent
models:
  providers:
    openrouter:
      apiKey: test-key
  main:
    name: openai/gpt-4o-mini
    provider: openrouter
search:
  enableRerank: false
  vector:
    enabled: false
storage:
  backend: memory
safety:
  pii: false
`), 0o600))

	return configPath
}
