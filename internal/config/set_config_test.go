package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wandxy/morph/internal/profile"
)

func TestSetConfigValue_UpdatesTypedValues(t *testing.T) {
	clearEnvKeys(t, "MORPH_CONFIG", "MORPH_ENV_FILE", "MORPH_PROFILE", "OPENROUTER_API_KEY")
	resetSetConfigProfileState(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := writeSetConfigProfileConfig(t, home, "work")

	updatedPath, err := SetConfigValue("", configPath, "search.enableRank", "true")
	require.NoError(t, err)
	require.Equal(t, "search.enableRerank", updatedPath)

	_, err = SetConfigValue("", configPath, "session.defaultIdleExpiry", "2h")
	require.NoError(t, err)

	_, err = SetConfigValue("", configPath, "fs.roots", "/tmp/alpha,/tmp/beta")
	require.NoError(t, err)

	_, err = SetConfigValue("", configPath, "name", "edited-agent")
	require.NoError(t, err)

	_, err = SetConfigValue("", configPath, "compaction.triggerPercent", "0.7")
	require.NoError(t, err)

	_, err = SetConfigValue("", configPath, "reranker.overrides.memory_reflection.maxCandidates", "0")
	require.NoError(t, err)

	cfg, err := Load("", configPath)
	require.NoError(t, err)
	require.Equal(t, "edited-agent", cfg.Name)
	require.True(t, *cfg.Search.EnableRerank)
	require.Equal(t, 2*time.Hour, cfg.Session.DefaultIdleExpiry)
	require.Equal(t, []string{"/tmp/alpha", "/tmp/beta"}, cfg.FS.Roots)
	require.Equal(t, 0.7, cfg.Compaction.TriggerPercent)
	require.NotNil(t, cfg.Reranker.Overrides["memory_reflection"].MaxCandidates)
	require.Zero(t, *cfg.Reranker.Overrides["memory_reflection"].MaxCandidates)
}

func TestSetConfigValue_UpdatesUnsignedInteger(t *testing.T) {
	clearEnvKeys(t, "MORPH_CONFIG", "MORPH_ENV_FILE", "MORPH_PROFILE", "OPENROUTER_API_KEY")
	resetSetConfigProfileState(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := writeSetConfigProfileConfig(t, home, "work")

	updatedPath, err := SetConfigValue("", configPath, "auth.generation", "2")
	require.NoError(t, err)
	require.Equal(t, "auth.generation", updatedPath)

	cfg, err := Load("", configPath)
	require.NoError(t, err)
	require.Equal(t, uint64(2), cfg.Auth.Generation)

	beforeInvalid, err := os.ReadFile(configPath)
	require.NoError(t, err)
	_, err = SetConfigValue("", configPath, "auth.generation", "-1")
	require.Error(t, err)
	_, err = SetConfigValue("", configPath, "auth.generation", "18446744073709551616")
	require.Error(t, err)
	afterInvalid, err := os.ReadFile(configPath)
	require.NoError(t, err)
	require.Equal(t, beforeInvalid, afterInvalid)
}

func TestGetConfigValues_ReadsTypedValues(t *testing.T) {
	clearEnvKeys(t, "MORPH_CONFIG", "MORPH_ENV_FILE", "MORPH_PROFILE", "OPENROUTER_API_KEY")
	resetSetConfigProfileState(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := writeSetConfigProfileConfig(t, home, "work")

	_, err := SetConfigValues("", configPath, []ConfigUpdate{
		{Path: "search.enableRank", Value: "true"},
		{Path: "session.defaultIdleExpiry", Value: "2h"},
		{Path: "reranker.overrides.memory_reflection.type", Value: "llm"},
	})
	require.NoError(t, err)

	values, err := GetConfigValues("", configPath, []string{
		"search.enableRank",
		"session.defaultIdleExpiry",
		"reranker.overrides.memory_reflection.type",
	})
	require.NoError(t, err)
	require.Equal(t, []ConfigValue{
		{Path: "search.enableRerank", Value: "true"},
		{Path: "session.defaultIdleExpiry", Value: "2h0m0s"},
		{Path: "reranker.overrides.memory_reflection.type", Value: "llm"},
	}, values)
}

func TestSetConfigValues_UpdatesMultipleFieldsAtomically(t *testing.T) {
	clearEnvKeys(t, "MORPH_CONFIG", "MORPH_ENV_FILE", "MORPH_PROFILE", "OPENROUTER_API_KEY")
	resetSetConfigProfileState(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := writeSetConfigProfileConfig(t, home, "work")

	updatedPaths, err := SetConfigValues("", configPath, []ConfigUpdate{
		{Path: "search.enableRank", Value: "true"},
		{Path: "session.defaultIdleExpiry", Value: "2h"},
		{Path: "name", Value: "batch-agent"},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"search.enableRerank", "session.defaultIdleExpiry", "name"}, updatedPaths)

	cfg, err := Load("", configPath)
	require.NoError(t, err)
	require.Equal(t, "batch-agent", cfg.Name)
	require.True(t, *cfg.Search.EnableRerank)
	require.Equal(t, 2*time.Hour, cfg.Session.DefaultIdleExpiry)
}

func TestSetConfigValues_CanonicalizesBaseURLPath(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: test-agent
models:
    main:
        provider: ollama
        name: lfm2.5-thinking:latest
        api: ollama-native
        baseURL: http://127.0.0.1:11434/v1
search:
    vector:
        enabled: false
storage:
    backend: memory
`), 0o600))

	updatedPaths, err := SetConfigValuesRelaxed("", configPath, []ConfigUpdate{
		{Path: "models.main.baseURL", Value: "http://127.0.0.1:11434"},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"models.main.baseUrl"}, updatedPaths)

	cfg, err := Load("", configPath)
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:11434", cfg.Models.Main.BaseURL)

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)
	rendered := string(data)
	require.NotContains(t, rendered, "baseURL:")
	require.Contains(t, rendered, "baseUrl: http://127.0.0.1:11434")
}

func TestSetConfigValuesRelaxed_AllowsMissingGatewayCredentials(t *testing.T) {
	clearEnvKeys(t, "MORPH_CONFIG", "MORPH_ENV_FILE", "MORPH_PROFILE", "OPENROUTER_API_KEY")
	resetSetConfigProfileState(t)

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: test-agent
models:
    providers:
        openrouter:
            apiKey: router-key
    main:
        provider: ""
        name: ""
gateway:
    enabled: true
    telegram:
        enabled: true
        mode: polling
        botToken: ""
search:
    vector:
        enabled: false
`), 0o600))

	updatedPaths, err := SetConfigValuesRelaxed("", configPath, []ConfigUpdate{
		{Path: "models.main.provider", Value: "openrouter"},
		{Path: "models.main.name", Value: "openai/gpt-4o-mini"},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"models.main.provider", "models.main.name"}, updatedPaths)
	cfg, err := Load("", configPath)
	require.NoError(t, err)
	require.Equal(t, "openrouter", cfg.Models.Main.Provider)
	require.Equal(t, "openai/gpt-4o-mini", cfg.Models.Main.Name)
}

func TestSetConfigValues_RewritesFlowMappingsAsBlockYAML(t *testing.T) {
	clearEnvKeys(t, "MORPH_CONFIG", "MORPH_ENV_FILE", "MORPH_PROFILE", "OPENROUTER_API_KEY")
	resetSetConfigProfileState(t)

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: test-agent
models:
    maxRetries: 2
    providers: {openrouter: {apiKey: old-key}}
    main:
        name: openai/gpt-4o
        provider: openrouter
search:
    vector:
        enabled: false
storage:
    backend: memory
`), 0o600))

	_, err := SetConfigValue("", configPath, "models.providers.openrouter.apiKey", "new-key")
	require.NoError(t, err)

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)
	rendered := string(data)
	require.NotContains(t, rendered, "{openrouter:")
	require.NotContains(t, rendered, "{apiKey:")
	require.Contains(t, rendered, "providers:\n        openrouter:\n            apiKey: new-key")
}

func TestSetConfigValues_CreatesNestedMappingUnderNullNode(t *testing.T) {
	clearEnvKeys(t, "MORPH_CONFIG", "MORPH_ENV_FILE", "MORPH_PROFILE", "OPENROUTER_API_KEY")
	resetSetConfigProfileState(t)

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: test-agent
models:
    providers:
    main:
        name: claude-sonnet-4-6
        provider: anthropic
search:
    vector:
        enabled: false
storage:
    backend: memory
`), 0o600))

	_, err := SetConfigValue("", configPath, "models.providers.anthropic.apiKey", "new-key")
	require.NoError(t, err)

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)
	require.Contains(t, string(data), "providers:\n        anthropic:\n            apiKey: new-key")
}

func TestSetConfigValue_RejectsInvalidPathOrValue(t *testing.T) {
	clearEnvKeys(t, "MORPH_CONFIG", "MORPH_ENV_FILE", "MORPH_PROFILE", "OPENROUTER_API_KEY")
	resetSetConfigProfileState(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := writeSetConfigProfileConfig(t, home, "work")
	before, err := os.ReadFile(configPath)
	require.NoError(t, err)

	_, err = SetConfigValue("", configPath, "search.missing", "true")
	require.EqualError(t, err, `unknown config path "search.missing"`)

	_, err = SetConfigValue("", configPath, "search.enableRerank", "maybe")
	require.EqualError(t, err, `search.enableRerank: expected bool, got "maybe"`)

	_, err = SetConfigValue("", configPath, "models.main.name", "/not-a-model")
	require.ErrorContains(t, err, "model is required")

	after, err := os.ReadFile(configPath)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after))
}

func TestSetConfigValues_DoesNotWritePartialBatchOnInvalidValue(t *testing.T) {
	clearEnvKeys(t, "MORPH_CONFIG", "MORPH_ENV_FILE", "MORPH_PROFILE", "OPENROUTER_API_KEY")
	resetSetConfigProfileState(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := writeSetConfigProfileConfig(t, home, "work")
	before, err := os.ReadFile(configPath)
	require.NoError(t, err)

	_, err = SetConfigValues("", configPath, []ConfigUpdate{
		{Path: "name", Value: "edited-agent"},
		{Path: "search.enableRerank", Value: "maybe"},
	})
	require.EqualError(t, err, `search.enableRerank: expected bool, got "maybe"`)

	after, err := os.ReadFile(configPath)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after))
}

func resetSetConfigProfileState(t *testing.T) {
	t.Helper()

	originalProfile := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(originalProfile)
	})
	profile.SetActive(profile.Profile{})
}

func writeSetConfigProfileConfig(t *testing.T, home string, name string) string {
	t.Helper()

	profileHome := filepath.Join(home, ".morph", "profiles", strings.ToLower(name))
	require.NoError(t, os.MkdirAll(profileHome, 0o700))
	configPath := filepath.Join(profileHome, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: test-agent
models:
  providers:
    openrouter:
      apiKey: test-key
  main:
    name: gpt-4o-mini
    provider: openrouter
search:
  enableRerank: false
  vector:
    enabled: false
session:
  maxIterations: 3
  defaultIdleExpiry: "24h"
  archiveRetention: "720h"
storage:
  backend: memory
`), 0o600))

	return configPath
}
