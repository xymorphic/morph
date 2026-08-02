package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	commandplan "github.com/xymorphic/morph/internal/command"
	"github.com/xymorphic/morph/internal/constants"
	"github.com/xymorphic/morph/internal/permissions"
)

func TestConfig_ValidateRejectsInvalidLogLevel(t *testing.T) {
	err := (&Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openai": {APIKey: "test-key"}},
			Main:      MainModelConfig{Name: constants.DefaultModel, Provider: "openai"},
		},
		Log: LogConfig{Level: "trace"},
	}).Validate()
	require.EqualError(t, err, "log level must be one of debug, info, warn, or error; use --log.level")
}

func TestConfig_ValidateRejectsInvalidLogRotationSettings(t *testing.T) {
	tests := []struct {
		name    string
		log     LogConfig
		message string
	}{
		{
			name:    "max size",
			log:     LogConfig{Level: "info", MaxSizeMB: -1},
			message: "log max size must be non-negative; use --log.max-size-mb",
		},
		{
			name:    "max backups",
			log:     LogConfig{Level: "info", MaxBackups: -1},
			message: "log max backups must be non-negative; use --log.max-backups",
		},
		{
			name:    "max age",
			log:     LogConfig{Level: "info", MaxAgeDays: -1},
			message: "log max age days must be non-negative; use --log.max-age-days",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := (&Config{
				Name: "test-agent",
				Models: ModelsConfig{
					Providers: map[string]ProviderModelConfig{"openai": {APIKey: "test-key"}},
					Main:      MainModelConfig{Name: constants.DefaultModel, Provider: "openai"},
				},
				Log: tt.log,
			}).Validate()

			require.EqualError(t, err, tt.message)
		})
	}
}

func TestConfig_ValidateRejectsEmptyProvider(t *testing.T) {
	err := (&Config{
		Name:   "test-agent",
		Models: ModelsConfig{Main: MainModelConfig{APIKey: "test-key", Name: constants.DefaultModel}},
	}).Validate()
	require.EqualError(t, err, "model provider is required")
}

func TestConfig_ValidateRejectsEmptyRPCAddress(t *testing.T) {
	cfg := &Config{
		Name:   "test-agent",
		Models: ModelsConfig{Main: MainModelConfig{APIKey: "test-key", Name: constants.DefaultModel, Provider: "openai"}},
		RPC:    RPCConfig{Address: "   ", Port: 50051},
		Log:    LogConfig{Level: "info"},
	}

	require.EqualError(t, cfg.Validate(), "rpc address is required; set MORPH_RPC_ADDRESS, provide it in config, or use --rpc.address")
}

func TestConfig_ValidateRejectsInvalidRPCPort(t *testing.T) {
	cfg := &Config{
		Name:   "test-agent",
		Models: ModelsConfig{Main: MainModelConfig{APIKey: "test-key", Name: constants.DefaultModel, Provider: "openai"}},
		RPC:    RPCConfig{Address: "127.0.0.1", Port: -1},
		Log:    LogConfig{Level: "info"},
	}

	require.EqualError(t, cfg.Validate(), "rpc port must be non-negative; set MORPH_RPC_PORT, provide it in config, or use --rpc.port")
}

func TestConfig_ValidateAllowsZeroRPCPortForDynamicBind(t *testing.T) {
	cfg := &Config{
		Name:   "test-agent",
		Models: ModelsConfig{Main: MainModelConfig{APIKey: "test-key", Name: constants.DefaultModel, Provider: "openai"}},
		RPC:    RPCConfig{Address: "127.0.0.1", Port: 0},
		Log:    LogConfig{Level: "info"},
	}

	require.NoError(t, cfg.Validate())
}

func TestConfig_ValidateRejectsInvalidMaxIterations(t *testing.T) {
	cfg := &Config{
		Name:    "test-agent",
		Models:  ModelsConfig{Main: MainModelConfig{APIKey: "test-key", Name: constants.DefaultModel, Provider: "openai"}},
		RPC:     RPCConfig{Address: "127.0.0.1", Port: 50051},
		Session: SessionConfig{MaxIterations: -1},
		Log:     LogConfig{Level: "info"},
	}

	require.EqualError(t, cfg.Validate(), "max iterations must be greater than zero; set "+
		"MORPH_SESSION_MAX_ITERATIONS, provide it in config, or use --max-iterations")
}

func TestLoad_UsesFilesystemRootsAndExecRulesFromConfig(t *testing.T) {
	clearEnvKeys(t, "MORPH_FS_ROOTS", "MORPH_EXEC_ALLOW", "MORPH_EXEC_ASK", "MORPH_EXEC_DENY")
	configDir := t.TempDir()
	workingDir := t.TempDir()
	t.Chdir(workingDir)
	configPath := filepath.Join(configDir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: config-agent
models:
  providers:
    openrouter:
      apiKey: config-key
  main:
    name: config-model
    provider: openai
rpc:
  address: 127.0.0.1
  port: 50051
log:
  level: info
fs:
  roots:
    - .
    - ./nested
exec:
  shell: ./bin/sh
  allowCommands:
    - executable: git
      argumentPrefix: [status]
  askCommands:
    - executable: make
      allowIndirect: true
  denyCommands:
    - resolvedPath: /tmp/untrusted/git
`), 0o600))

	cfg, err := Load("", configPath)

	require.NoError(t, err)
	require.Equal(t, []string{
		filepath.Join(workingDir),
		filepath.Join(workingDir, "nested"),
	}, cfg.FS.Roots)
	require.Equal(t, filepath.Join(configDir, "bin", "sh"), cfg.Exec.Shell)
	require.Equal(t, []commandplan.Selector{{
		Executable: "git", ArgumentPrefix: []string{"status"}, Modes: []commandplan.Mode{commandplan.ModeDirect},
	}}, cfg.Exec.AllowCommands)
	require.Equal(t, []commandplan.Selector{{
		Executable: "make", Modes: []commandplan.Mode{commandplan.ModeDirect}, AllowIndirect: true,
	}}, cfg.Exec.AskCommands)
	require.Equal(t, []commandplan.Selector{{
		ResolvedPath: "/tmp/untrusted/git",
		Modes:        []commandplan.Mode{commandplan.ModeDirect, commandplan.ModePOSIXShell},
	}}, cfg.Exec.DenyCommands)
}

func TestConfig_ValidateRejectsInvalidExecSettings(t *testing.T) {
	cfg := &Config{}
	cfg.Normalize()
	cfg.Exec.Shell = "relative/sh"
	require.EqualError(t, cfg.ValidateRelaxed(), "exec shell must be an absolute path")

	cfg.Exec.Shell = ""
	cfg.Exec.AllowCommands = []commandplan.Selector{{}}
	require.EqualError(t, cfg.ValidateRelaxed(),
		"exec.allowCommands[0]: command selector requires an executable or resolved path")

	cfg.Exec.AllowCommands = nil
	cfg.Exec.Allow = []string{"git status"}
	require.EqualError(t, cfg.ValidateRelaxed(),
		"exec.allow, exec.ask, and exec.deny are no longer supported; "+
			"use typed command selectors in exec.allowCommands, exec.askCommands, and exec.denyCommands")

	cfg.Exec.Allow = nil
	cfg.Permissions.Rules = []permissions.Rule{{
		Name: "invalid command", Commands: []commandplan.Selector{{}}, Decision: permissions.DecisionDeny,
	}}
	require.EqualError(t, cfg.ValidateRelaxed(),
		`permission rule "invalid command" commands[0]: command selector requires an executable or resolved path`)
}

func TestLoad_DefaultsNoProfileAccessToTrueWhenOmitted(t *testing.T) {
	clearEnvKeys(t, "MORPH_CONFIG")
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: config-agent
`), 0o600))

	cfg, err := Load("", configPath)

	require.NoError(t, err)
	require.True(t, cfg.FS.NoProfileAccess)
}

func TestLoad_AllowsNoProfileAccessOverrideFromConfig(t *testing.T) {
	clearEnvKeys(t, "MORPH_CONFIG")
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: config-agent
fs:
  noProfileAccess: false
`), 0o600))

	cfg, err := Load("", configPath)

	require.NoError(t, err)
	require.False(t, cfg.FS.NoProfileAccess)
}

func TestLoad_UsesFilesystemRootsFromEnvAndRejectsLegacyExecRules(t *testing.T) {
	clearEnvKeys(t, "MORPH_FS_ROOTS", "MORPH_EXEC_ALLOW", "MORPH_EXEC_ASK", "MORPH_EXEC_DENY")
	dir := t.TempDir()
	t.Chdir(dir)
	envPath := filepath.Join(dir, ".env")
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(envPath, []byte("MORPH_FS_ROOTS=.,./nested\nMORPH_EXEC_ALLOW=git status\nMORPH_EXEC_ASK=git push\nMORPH_EXEC_DENY=git reset --hard\n"), 0o600))
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: config-agent
models:
  providers:
    openrouter:
      apiKey: config-key
  main:
    name: config-model
    provider: openai
rpc:
  address: 127.0.0.1
  port: 50051
log:
  level: info
`), 0o600))

	cfg, err := Load(envPath, configPath)

	require.NoError(t, err)
	require.Equal(t, []string{
		filepath.Join(dir),
		filepath.Join(dir, "nested"),
	}, cfg.FS.Roots)
	require.ErrorContains(t, cfg.ValidateRelaxed(), "exec.allow, exec.ask, and exec.deny are no longer supported")
}
