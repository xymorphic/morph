package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	cli "github.com/urfave/cli/v3"

	daemoncmd "github.com/wandxy/morph/cmd/daemon"
	doctorcmd "github.com/wandxy/morph/cmd/doctor"
	profilecmd "github.com/wandxy/morph/cmd/profile"
	tuicmd "github.com/wandxy/morph/cmd/tui"
	morphcli "github.com/wandxy/morph/internal/cli"
	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/constants"
	"github.com/wandxy/morph/internal/datadir"
	"github.com/wandxy/morph/internal/profile"
	"github.com/wandxy/morph/pkg/logutils"
)

func init() {
	logutils.SetOutput(io.Discard)
}

func TestNewCommand_RegistersDaemonCommand(t *testing.T) {
	cmd := newCommand()

	var names []string
	for _, command := range cmd.Commands {
		names = append(names, command.Name)
	}

	require.Contains(t, names, "daemon")
	require.Contains(t, names, "browser")
	require.Contains(t, names, "config")
	require.Contains(t, names, "provider")
	require.Contains(t, names, "version")
	require.NotContains(t, names, "setup")
}

func TestNewCommand_UsesConfigFileValues(t *testing.T) {
	clearEnvKeys(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "OPENROUTER_API_KEY", "MORPH_MODEL_BASE_URL", "MORPH_LOG_LEVEL", "MORPH_LOG_NO_COLOR", "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetGlobals(t)

	dir := t.TempDir()
	serverURL := newOpenRouterModelsServer(t, "openai/gpt-4o-mini")
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: config-agent
models:
  providers:
    openrouter:
      apiKey: config-key
  main:
    name: gpt-4o-mini
    provider: openrouter
    baseUrl: `+serverURL+`
log:
  level: error
  noColor: true
storage:
  backend: memory
search:
  vector:
    enabled: false
`), 0o600))

	cmd := newCommand()
	err := cmd.Run(canceledContext(), []string{
		"morph",
		"--config", configPath,
		"--rpc.port", nextTestPort(t),
		"daemon",
	})
	require.NoError(t, err)

	cfg := config.Get()
	require.Equal(t, "config-agent", cfg.Name)
	require.Equal(t, "openrouter", cfg.Models.Main.Provider)
	require.Equal(t, "config-key", cfg.Models.Providers["openrouter"].APIKey)
	require.Equal(t, "gpt-4o-mini", cfg.Models.Main.Name)
	require.Equal(t, serverURL, cfg.Models.Main.BaseURL)
	require.Equal(t, "error", cfg.Log.Level)
	require.True(t, cfg.Log.NoColor)
}

func TestNewCommand_UsesEnvOverConfigFile(t *testing.T) {
	clearEnvKeys(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "OPENROUTER_API_KEY", "MORPH_MODEL_BASE_URL", "MORPH_LOG_LEVEL", "MORPH_LOG_NO_COLOR", "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetGlobals(t)

	dir := t.TempDir()
	serverURL := newOpenRouterModelsServer(t, "openai/gpt-4o-mini")
	envPath := filepath.Join(dir, ".env")
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(envPath, []byte(`
MORPH_NAME=env-agent
MORPH_MODEL=openai/gpt-4o-mini
MORPH_MODEL_PROVIDER=openrouter
OPENROUTER_API_KEY=env-key
MORPH_MODEL_BASE_URL=`+serverURL+`
MORPH_LOG_LEVEL=warn
MORPH_LOG_NO_COLOR=false
`), 0o600))
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: config-agent
models:
  providers:
    openrouter:
      apiKey: config-key
  main:
    name: openai/gpt-4o-mini
    provider: openrouter
    baseUrl: `+serverURL+`
log:
  level: error
  noColor: true
storage:
  backend: memory
search:
  vector:
    enabled: false
`), 0o600))

	cmd := newCommand()
	err := cmd.Run(canceledContext(), []string{
		"morph",
		"--env-file", envPath,
		"--config", configPath,
		"--rpc.port", nextTestPort(t),
		"daemon",
	})
	require.NoError(t, err)

	cfg := config.Get()
	require.Equal(t, "env-agent", cfg.Name)
	require.Equal(t, "openrouter", cfg.Models.Main.Provider)
	auth, err := cfg.ResolveModelAuth()
	require.NoError(t, err)
	require.Equal(t, "env-key", auth.APIKey)
	require.Equal(t, config.ModelCredentialSource{Kind: config.ModelCredentialSourceProviderEnv, Name: "OPENROUTER_API_KEY"}, auth.CredentialSource)
	require.Equal(t, "openai/gpt-4o-mini", cfg.Models.Main.Name)
	require.Equal(t, serverURL, cfg.Models.Main.BaseURL)
	require.Equal(t, "warn", cfg.Log.Level)
	require.False(t, cfg.Log.NoColor)
}

func TestNewCommand_DaemonStartsWhenProviderEmpty(t *testing.T) {
	clearEnvKeys(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "OPENROUTER_API_KEY", "MORPH_MODEL_BASE_URL", "MORPH_LOG_LEVEL", "MORPH_LOG_NO_COLOR", "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetGlobals(t)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: config-agent
models:
  main:
    name: openai/gpt-4o-mini
log:
  level: info
storage:
  backend: memory
search:
  vector:
    enabled: false
`), 0o600))

	cmd := newCommand()
	err := cmd.Run(canceledContext(), []string{
		"morph",
		"--config", configPath,
		"--rpc.port", nextTestPort(t),
		"daemon",
	})
	require.NoError(t, err)
}

func TestNewCommand_UsesMappedBaseURLWhenProviderSetAndBaseURLUnset(t *testing.T) {
	clearEnvKeys(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "OPENROUTER_API_KEY", "MORPH_MODEL_BASE_URL", "MORPH_LOG_LEVEL", "MORPH_LOG_NO_COLOR", "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetGlobals(t)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: config-agent
models:
  providers:
    openrouter:
      apiKey: config-key
  main:
    provider: openrouter
    name: openai/gpt-4o-mini
log:
  level: info
storage:
  backend: memory
`), 0o600))

	cmd := newCommand()
	err := cmd.Run(canceledContext(), []string{
		"morph",
		"--config", configPath,
		"--rpc.port", nextTestPort(t),
		"daemon",
	})
	require.NoError(t, err)

	cfg := config.Get()
	require.Equal(t, "openrouter", cfg.Models.Main.Provider)
	require.Equal(t, "https://openrouter.ai/api/v1", cfg.Models.Main.BaseURL)
}

func TestNewCommand_UsesMappedBaseURLWhenOpenAIProviderSetAndBaseURLUnset(t *testing.T) {
	clearEnvKeys(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "OPENROUTER_API_KEY", "MORPH_MODEL_BASE_URL", "MORPH_LOG_LEVEL", "MORPH_LOG_NO_COLOR", "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetGlobals(t)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: config-agent
models:
  providers:
    openrouter:
      apiKey: config-key
  main:
    name: gpt-4o-mini
    provider: openrouter
log:
  level: info
storage:
  backend: memory
`), 0o600))

	cmd := newCommand()
	err := cmd.Run(canceledContext(), []string{
		"morph",
		"--config", configPath,
		"--rpc.port", nextTestPort(t),
		"--model.provider", "openrouter",
		"daemon",
	})
	require.NoError(t, err)

	cfg := config.Get()
	require.Equal(t, "openrouter", cfg.Models.Main.Provider)
	require.Equal(t, "https://openrouter.ai/api/v1", cfg.Models.Main.BaseURL)
}

func TestNewCommand_FlagsOverrideEnvAndConfig(t *testing.T) {
	clearEnvKeys(t, "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "OPENROUTER_API_KEY", "MORPH_MODEL_BASE_URL", "MORPH_LOG_LEVEL", "MORPH_LOG_NO_COLOR", "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetGlobals(t)

	dir := t.TempDir()
	serverURL := newOpenRouterModelsServer(t, "openai/gpt-4o-mini")
	envPath := filepath.Join(dir, ".env")
	configPath := filepath.Join(dir, "config.yaml")

	require.NoError(t, os.WriteFile(envPath, []byte(`
MORPH_NAME=env-agent
MORPH_MODEL=openai/gpt-4o-mini
MORPH_MODEL_PROVIDER=openrouter
OPENROUTER_API_KEY=env-key
MORPH_MODEL_BASE_URL=`+serverURL+`
MORPH_LOG_LEVEL=warn
MORPH_LOG_NO_COLOR=false
`), 0o600))
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: config-agent
models:
  providers:
    openrouter:
      apiKey: config-key
  main:
    name: openai/gpt-4o-mini
    provider: openrouter
    baseUrl: `+serverURL+`
log:
  level: error
  noColor: true
storage:
  backend: memory
`), 0o600))

	cmd := newCommand()
	err := cmd.Run(canceledContext(), []string{
		"morph",
		"--env-file", envPath,
		"--config", configPath,
		"--name", "flag-agent",
		"--model", "openai/gpt-4o-mini",
		"--model.provider", "openrouter",
		"--model.api-key", "flag-key",
		"--model.base-url", serverURL,
		"--rpc.port", nextTestPort(t),
		"--log.level", "debug",
		"--log.no-color=true",
		"daemon",
	})
	require.NoError(t, err)

	cfg := config.Get()
	require.Equal(t, "flag-agent", cfg.Name)
	require.Equal(t, "openrouter", cfg.Models.Main.Provider)
	require.Equal(t, "flag-key", cfg.Models.Providers["openrouter"].APIKey)
	require.Equal(t, "openai/gpt-4o-mini", cfg.Models.Main.Name)
	require.Equal(t, serverURL, cfg.Models.Main.BaseURL)
	require.Equal(t, "debug", cfg.Log.Level)
	require.True(t, cfg.Log.NoColor)
}

func TestNewCommand_RunsUpCommandExplicitly(t *testing.T) {
	clearEnvKeys(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "OPENROUTER_API_KEY", "MORPH_MODEL_BASE_URL", "MORPH_LOG_LEVEL", "MORPH_LOG_NO_COLOR", "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetGlobals(t)

	dir := t.TempDir()
	serverURL := newOpenRouterModelsServer(t, "openai/gpt-4o-mini")
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: config-agent
models:
  providers:
    openrouter:
      apiKey: config-key
  main:
    name: openai/gpt-4o-mini
    provider: openrouter
    baseUrl: `+serverURL+`
log:
  level: info
storage:
  backend: memory
`), 0o600))

	cmd := newCommand()
	err := cmd.Run(canceledContext(), []string{
		"agent",
		"--config", configPath,
		"--rpc.port", nextTestPort(t),
		"daemon",
	})
	require.NoError(t, err)

	cfg := config.Get()
	require.Equal(t, "config-agent", cfg.Name)
	require.Equal(t, "openai/gpt-4o-mini", cfg.Models.Main.Name)
	require.Equal(t, "openrouter", cfg.Models.Main.Provider)
	require.Equal(t, "config-key", cfg.Models.Providers["openrouter"].APIKey)
	require.Equal(t, serverURL, cfg.Models.Main.BaseURL)
}

func TestResolveEnvFilePrefersFlag(t *testing.T) {
	clearEnvKeys(t, "MORPH_ENV_FILE")
	resetGlobals(t)
	require.Equal(t, "/tmp/test.env", getEnvFile([]string{"morph", "--env-file", "/tmp/test.env"}))
	require.Equal(t, "/tmp/test2.env", getEnvFile([]string{"morph", "--env-file=/tmp/test2.env"}))
}

func TestResolveEnvFilePrefersEnvVar(t *testing.T) {
	clearEnvKeys(t, "MORPH_ENV_FILE")
	resetGlobals(t)
	t.Setenv("MORPH_ENV_FILE", "/tmp/from-env.env")
	require.Equal(t, "/tmp/from-env.env", getEnvFile([]string{"morph", "--env-file", "/tmp/ignored.env"}))
}

func TestResolveEnvFileUsesDefaultWhenUnset(t *testing.T) {
	clearEnvKeys(t, "MORPH_ENV_FILE")
	resetGlobals(t)
	require.Equal(t, ".env", getEnvFile([]string{"morph"}))
}

func TestConfigureProfileDefaults_UsesSelectedProfilePaths(t *testing.T) {
	clearEnvKeys(t, "MORPH_PROFILE", "MORPH_ENV_FILE", "MORPH_CONFIG")
	resetGlobals(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	err := configureProfileDefaults([]string{"morph", "--profile", "Work"})

	require.NoError(t, err)
	profileHome := filepath.Join(home, ".morph", "profiles", "work")
	require.Equal(t, filepath.Join(profileHome, ".env"), envFile)
	require.Equal(t, filepath.Join(profileHome, "config.yaml"), configFile)
	require.Equal(t, filepath.Join(profileHome, ".env"), getEnvFile([]string{"morph"}))
}

func TestConfigureProfileDefaults_UsesProfileShortmorph(t *testing.T) {
	clearEnvKeys(t, "MORPH_PROFILE", "MORPH_ENV_FILE", "MORPH_CONFIG")
	resetGlobals(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	err := configureProfileDefaults([]string{"morph", "-p", "Work"})

	require.NoError(t, err)
	profileHome := filepath.Join(home, ".morph", "profiles", "work")
	require.Equal(t, filepath.Join(profileHome, ".env"), envFile)
	require.Equal(t, filepath.Join(profileHome, "config.yaml"), configFile)
}

func TestConfigureProfileDefaults_IgnoresProfileTextAfterTerminator(t *testing.T) {
	clearEnvKeys(t, "MORPH_PROFILE", "MORPH_ENV_FILE", "MORPH_CONFIG")
	resetGlobals(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	err := configureProfileDefaults([]string{"morph", "--", "--profile", "Work"})

	require.NoError(t, err)
	profileHome := filepath.Join(home, ".morph", "profiles", "default")
	require.Equal(t, filepath.Join(profileHome, ".env"), envFile)
	require.Equal(t, filepath.Join(profileHome, "config.yaml"), configFile)
}

func TestConfigureProfileDefaults_UsesStoredCurrentProfile(t *testing.T) {
	clearEnvKeys(t, "MORPH_PROFILE", "MORPH_ENV_FILE", "MORPH_CONFIG")
	resetGlobals(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	_, err := profile.StoreCurrentName("Work", home)
	require.NoError(t, err)

	err = configureProfileDefaults([]string{"morph"})
	require.NoError(t, err)

	profileHome := filepath.Join(home, ".morph", "profiles", "work")
	require.Equal(t, filepath.Join(profileHome, ".env"), envFile)
	require.Equal(t, filepath.Join(profileHome, "config.yaml"), configFile)
}

func TestConfigureProfileDefaults_ProfileFlagOverridesStoredCurrentProfile(t *testing.T) {
	clearEnvKeys(t, "MORPH_PROFILE", "MORPH_ENV_FILE", "MORPH_CONFIG")
	resetGlobals(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	_, err := profile.StoreCurrentName("Work", home)
	require.NoError(t, err)

	err = configureProfileDefaults([]string{"morph", "--profile", "Desk"})

	require.NoError(t, err)
	profileHome := filepath.Join(home, ".morph", "profiles", "desk")
	require.Equal(t, filepath.Join(profileHome, ".env"), envFile)
	require.Equal(t, filepath.Join(profileHome, "config.yaml"), configFile)
}

func TestConfigureProfileDefaults_EnvOverridesStoredCurrentProfile(t *testing.T) {
	clearEnvKeys(t, "MORPH_PROFILE", "MORPH_ENV_FILE", "MORPH_CONFIG")
	resetGlobals(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MORPH_PROFILE", "Desk")
	_, err := profile.StoreCurrentName("Work", home)
	require.NoError(t, err)

	err = configureProfileDefaults([]string{"morph"})

	require.NoError(t, err)
	profileHome := filepath.Join(home, ".morph", "profiles", "desk")
	require.Equal(t, filepath.Join(profileHome, ".env"), envFile)
	require.Equal(t, filepath.Join(profileHome, "config.yaml"), configFile)
}

func TestNewCommand_RootActionStartsTUI(t *testing.T) {
	clearEnvKeys(t, "MORPH_ENV_FILE")
	resetGlobals(t)

	called := false
	originalRunRootTUI := runRootTUI
	t.Cleanup(func() {
		runRootTUI = originalRunRootTUI
	})
	runRootTUI = func(context.Context, *cli.Command) error {
		called = true
		return nil
	}

	cmd := newCommand()
	err := cmd.Run(context.Background(), []string{"morph"})
	require.NoError(t, err)
	require.True(t, called)
}

func TestNewCommand_RootActionRejectsMessageWithoutChatFlag(t *testing.T) {
	clearEnvKeys(t, "MORPH_ENV_FILE")
	resetGlobals(t)

	originalRunRootTUI := runRootTUI
	t.Cleanup(func() {
		runRootTUI = originalRunRootTUI
	})
	runRootTUI = func(context.Context, *cli.Command) error {
		t.Fatal("root args should not start TUI")
		return nil
	}

	cmd := newCommand()
	err := cmd.Run(context.Background(), []string{"morph", "hello"})
	require.EqualError(t, err, `unexpected root arguments "hello"; use --chat or -c to send a one-shot chat request`)
}

func TestNewCommand_RootActionUsesChatFlag(t *testing.T) {
	clearEnvKeys(t, "MORPH_ENV_FILE")
	resetGlobals(t)

	var gotArgs []string
	newRootChatAction = func(options morphcli.MainActionOptions) func(context.Context, *cli.Command) error {
		require.Equal(t, os.Stdin, options.Input)
		require.Equal(t, rootOutput, options.Output)
		return func(_ context.Context, cmd *cli.Command) error {
			gotArgs = cmd.Args().Slice()
			return nil
		}
	}
	runRootTUI = func(context.Context, *cli.Command) error {
		t.Fatal("chat flag should not start TUI")
		return nil
	}

	cmd := newCommand()
	err := cmd.Run(context.Background(), []string{"morph", "--chat", "hello", "world"})
	require.NoError(t, err)
	require.Equal(t, []string{"hello", "world"}, gotArgs)
}

func TestNewCommand_RootActionUsesChatShortFlag(t *testing.T) {
	clearEnvKeys(t, "MORPH_ENV_FILE")
	resetGlobals(t)

	var gotArgs []string
	newRootChatAction = func(morphcli.MainActionOptions) func(context.Context, *cli.Command) error {
		return func(_ context.Context, cmd *cli.Command) error {
			gotArgs = cmd.Args().Slice()
			return nil
		}
	}
	runRootTUI = func(context.Context, *cli.Command) error {
		t.Fatal("chat short flag should not start TUI")
		return nil
	}

	cmd := newCommand()
	err := cmd.Run(context.Background(), []string{"morph", "-c", "hello"})
	require.NoError(t, err)
	require.Equal(t, []string{"hello"}, gotArgs)
}

func TestNewCommand_HelpShowsUpdatedExamples(t *testing.T) {
	clearEnvKeys(t, "MORPH_ENV_FILE")
	resetGlobals(t)

	var output bytes.Buffer
	cmd := newCommand()
	cmd.Writer = &output
	cmd.ErrWriter = &output
	err := cmd.Run(context.Background(), []string{"morph", "--help"})
	require.NoError(t, err)
	require.Contains(t, output.String(), "EXAMPLES:")
	require.Contains(t, output.String(), "morph daemon")
	require.Contains(t, output.String(), "morph --profile work daemon")
	require.Contains(t, output.String(), "morph profile use work")
	require.Contains(t, output.String(), "morph --config ./config.yaml --trace.enabled daemon")
	require.Contains(t, output.String(), `morph --chat "summarize the failing tests"`)
	require.Contains(t, output.String(), `morph -c --profile work "continue"`)
	require.Contains(t, output.String(), `morph --chat --session ses_abc123 --instruct "be brief" "continue from the last debugging step"`)
	require.Contains(t, output.String(), "MORPH_PROFILE=work morph session list")
	require.Contains(t, output.String(), "morph trace view")
	require.Contains(t, output.String(), "morph --config ./config.yaml trace view --listen 127.0.0.1:9090")
	require.Contains(t, output.String(), "--gateway.enabled")
	require.Contains(t, output.String(), "--gateway.telegram.mode")
	require.Contains(t, output.String(), "--gateway.slack.mode")
}

func TestNewCommand_DaemonHelpShowsStatusCommand(t *testing.T) {
	clearEnvKeys(t, "MORPH_ENV_FILE")
	resetGlobals(t)

	var output bytes.Buffer
	cmd := newCommand()
	cmd.Writer = &output
	cmd.ErrWriter = &output
	err := cmd.Run(context.Background(), []string{"morph", "daemon", "--help"})
	require.NoError(t, err)
	require.Contains(t, output.String(), "Start the Morph daemon")
	require.Contains(t, output.String(), "status")
	require.Contains(t, output.String(), "Show daemon health and connection status")
}

func TestNewCommand_DoesNotAliasMisspelledDaemon(t *testing.T) {
	clearEnvKeys(t, "MORPH_ENV_FILE")
	resetGlobals(t)

	var output bytes.Buffer
	cmd := newCommand()
	cmd.Writer = &output
	cmd.ErrWriter = &output

	err := cmd.Run(context.Background(), []string{"morph", "deamon", "status"})

	require.Error(t, err)
	require.Contains(t, err.Error(), `unexpected root arguments "deamon status"`)
}

func TestNewCommand_DaemonStartIsNotAccepted(t *testing.T) {
	clearEnvKeys(t, "MORPH_ENV_FILE")
	resetGlobals(t)

	var output bytes.Buffer
	cmd := newCommand()
	cmd.Writer = &output
	cmd.ErrWriter = &output

	err := cmd.Run(context.Background(), []string{"morph", "daemon", "start"})

	require.Error(t, err)
}

func TestNewCommand_VersionCommandShowsVersionAndCommit(t *testing.T) {
	resetGlobals(t)
	setBuildInfo(t, "1.2.3", "abc1234")

	var output bytes.Buffer
	rootOutput = &output
	cmd := newCommand()
	err := cmd.Run(context.Background(), []string{"morph", "version"})
	require.NoError(t, err)

	require.Equal(t, "morph version 1.2.3\ncommit abc1234\n", output.String())
}

func TestNewCommand_VersionFlagShowsVersionAndCommit(t *testing.T) {
	resetGlobals(t)
	setBuildInfo(t, "1.2.3", "abc1234")

	for _, flag := range []string{"--version", "-v"} {
		var output bytes.Buffer
		cmd := newCommand()
		cmd.Writer = &output
		err := cmd.Run(context.Background(), []string{"morph", flag})
		require.NoError(t, err)

		require.Contains(t, output.String(), "morph version 1.2.3 (commit abc1234)")
	}
}

func TestNewCommand_RunsDoctorCommandExplicitly(t *testing.T) {
	clearEnvKeys(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "OPENROUTER_API_KEY", "MORPH_MODEL_BASE_URL", "MORPH_LOG_LEVEL", "MORPH_LOG_NO_COLOR", "MORPH_DEBUG_REQUESTS", "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetGlobals(t)

	cmd := newCommand()
	err := cmd.Run(context.Background(), []string{
		"morph",
		"--name", "flag-agent",
		"--model", "openai/gpt-4o-mini",
		"--model.provider", "openrouter",
		"--model.api-key", "flag-key",
		"doctor",
	})
	require.NoError(t, err)
}

func TestNewCommand_DaemonStartsWhenProviderUnsupported(t *testing.T) {
	clearEnvKeys(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "OPENROUTER_API_KEY", "MORPH_MODEL_BASE_URL", "MORPH_LOG_LEVEL", "MORPH_LOG_NO_COLOR", "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetGlobals(t)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: config-agent
models:
  providers:
    openai:
      apiKey: config-key
  main:
    name: openai/gpt-4o-mini
    provider: unsupported
    baseUrl: https://config.example/v1
`), 0o600))

	cmd := newCommand()
	err := cmd.Run(canceledContext(), []string{
		"morph",
		"--config", configPath,
		"--rpc.port", nextTestPort(t),
		"daemon",
	})
	require.NoError(t, err)
}

func TestNewCommand_UsesDirectClientWhenProviderIsOpenai(t *testing.T) {
	clearEnvKeys(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "OPENROUTER_API_KEY", "MORPH_MODEL_BASE_URL", "MORPH_LOG_LEVEL", "MORPH_LOG_NO_COLOR", "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetGlobals(t)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: config-agent
models:
  providers:
    openai:
      apiKey: config-key
  main:
    name: openai/gpt-4o-mini
    provider: openai
log:
  level: info
storage:
  backend: memory
`), 0o600))

	cmd := newCommand()
	err := cmd.Run(canceledContext(), []string{
		"morph",
		"--config", configPath,
		"--rpc.port", nextTestPort(t),
		"daemon",
	})
	require.NoError(t, err)

	cfg := config.Get()
	require.Equal(t, "openai", cfg.Models.Main.Provider)
	require.Equal(t, "https://api.openai.com/v1", cfg.Models.Main.BaseURL)
}

func TestNewCommand_DatabaseResetDeletesConfiguredSQLiteDatabase(t *testing.T) {
	clearEnvKeys(t, "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetGlobals(t)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
storage:
  backend: sqlite
`), 0o600))

	statePath := datadir.StateDBPath()
	require.NoError(t, os.MkdirAll(filepath.Dir(statePath), 0o755))
	for _, path := range getConfiguredDatabasePaths() {
		require.NoError(t, os.WriteFile(path, []byte("database"), 0o600))
	}

	var output bytes.Buffer
	rootOutput = &output
	cmd := newCommand()
	err := cmd.Run(context.Background(), []string{
		"morph",
		"--config", configPath,
		"db",
		"reset",
		"--force",
	})
	require.NoError(t, err)

	for _, path := range getConfiguredDatabasePaths() {
		require.NoFileExists(t, path)
	}
	require.Contains(t, output.String(), "Reset database: "+statePath)
}

func TestNewCommand_DatabaseResetRequiresForce(t *testing.T) {
	clearEnvKeys(t, "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetGlobals(t)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
storage:
  backend: sqlite
`), 0o600))

	statePath := datadir.StateDBPath()
	require.NoError(t, os.MkdirAll(filepath.Dir(statePath), 0o755))
	require.NoError(t, os.WriteFile(statePath, []byte("database"), 0o600))

	cmd := newCommand()
	err := cmd.Run(context.Background(), []string{
		"morph",
		"--config", configPath,
		"db",
		"reset",
	})
	require.EqualError(t, err, "database reset requires --force")
	require.FileExists(t, statePath)
}

func TestNewCommand_DatabaseResetRejectsMemoryStorage(t *testing.T) {
	clearEnvKeys(t, "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetGlobals(t)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
storage:
  backend: memory
`), 0o600))

	cmd := newCommand()
	err := cmd.Run(context.Background(), []string{
		"morph",
		"--config", configPath,
		"db",
		"reset",
		"--force",
	})
	require.EqualError(t, err, "database reset requires sqlite storage backend")
}

func clearEnvKeys(t *testing.T, keys ...string) {
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

func resetGlobals(t *testing.T) {
	t.Helper()
	originalEnvFile := envFile
	originalConfigFile := configFile
	originalRootOutput := rootOutput
	originalRunRootTUI := runRootTUI
	originalNewRootChatAction := newRootChatAction
	originalDoctorOutput := doctorcmd.SetOutput(io.Discard)
	originalProfileOutput := profilecmd.SetOutput(io.Discard)
	originalStartupOutput := daemoncmd.SetOutput(io.Discard)
	originalConfig := config.Get()
	originalProfile := profile.Active()
	t.Cleanup(func() {
		envFile = originalEnvFile
		configFile = originalConfigFile
		rootOutput = originalRootOutput
		runRootTUI = originalRunRootTUI
		newRootChatAction = originalNewRootChatAction
		doctorcmd.SetOutput(originalDoctorOutput)
		profilecmd.SetOutput(originalProfileOutput)
		daemoncmd.SetOutput(originalStartupOutput)
		config.Set(originalConfig)
		profile.SetActive(originalProfile)
	})
	envFile = ".env"
	configFile = "config.yaml"
	rootOutput = io.Discard
	runRootTUI = tuicmd.Run
	newRootChatAction = morphcli.NewMainAction
	logutils.SetOutput(io.Discard)
	config.Set(nil)
	t.Setenv("HOME", t.TempDir())
	profile.SetActive(profile.Profile{})
}

func setBuildInfo(t *testing.T, version string, commit string) {
	t.Helper()
	originalVersion := constants.AppVersion
	originalCommit := constants.CommitHash
	t.Cleanup(func() {
		constants.AppVersion = originalVersion
		constants.CommitHash = originalCommit
	})
	constants.AppVersion = version
	constants.CommitHash = commit
}

func newOpenRouterModelsServer(t *testing.T, model string) string {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/models", r.URL.Path)
		_, _ = w.Write([]byte(`{"data":[{"id":"` + model + `","context_length":128000}]}`))
	}))
	t.Cleanup(server.Close)

	return server.URL
}

func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func nextTestPort(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = lis.Close() }()
	port := lis.Addr().(*net.TCPAddr).Port
	return strconv.Itoa(port)
}
