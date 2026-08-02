package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xymorphic/morph/internal/constants"
	modelprovider "github.com/xymorphic/morph/internal/model/provider"
)

func TestConfig_ValidateRejectsCompactionThresholdsAboveOrEqualOne(t *testing.T) {
	err := (&Config{
		Name:       "test-agent",
		Models:     ModelsConfig{Main: MainModelConfig{APIKey: "test-key", Name: constants.DefaultModel, Provider: "openai"}},
		RPC:        RPCConfig{Address: "127.0.0.1", Port: 50051},
		Session:    SessionConfig{MaxIterations: 1, DefaultIdleExpiry: time.Hour, ArchiveRetention: 24 * time.Hour},
		Log:        LogConfig{Level: "info"},
		Storage:    StorageConfig{Backend: "memory"},
		Compaction: CompactionConfig{Enabled: new(true), TriggerPercent: 1, WarnPercent: 1},
	}).Validate()

	require.EqualError(t, err, "compaction trigger percent must be greater than zero and less than one")

	err = (&Config{
		Name:       "test-agent",
		Models:     ModelsConfig{Main: MainModelConfig{APIKey: "test-key", Name: constants.DefaultModel, Provider: "openai"}},
		RPC:        RPCConfig{Address: "127.0.0.1", Port: 50051},
		Session:    SessionConfig{MaxIterations: 1, DefaultIdleExpiry: time.Hour, ArchiveRetention: 24 * time.Hour},
		Log:        LogConfig{Level: "info"},
		Storage:    StorageConfig{Backend: "memory"},
		Compaction: CompactionConfig{Enabled: new(true), TriggerPercent: 0.9, WarnPercent: 1},
	}).Validate()

	require.EqualError(t, err, "compaction warn percent must be greater than zero and less than one")
}

func TestLoad_UsesCompactionSettingsFromConfig(t *testing.T) {
	clearEnvKeys(t, "MORPH_MODEL_CONTEXT_LENGTH", "MORPH_COMPACTION_ENABLED", "MORPH_COMPACTION_TRIGGER_PERCENT",
		"MORPH_COMPACTION_WARN_PERCENT")

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
models:
  main:
    contextLength: 64000
compaction:
  enabled: false
  triggerPercent: 0.7
  warnPercent: 0.9
  recentSessionTail: 3
`), 0o600))

	cfg, err := Load("", configPath)
	require.NoError(t, err)
	require.Equal(t, 64000, cfg.Models.Main.ContextLength)
	require.False(t, getBoolValue(cfg.Compaction.Enabled))
	require.False(t, cfg.CompactionEnabled())
	require.Equal(t, 0.7, cfg.Compaction.TriggerPercent)
	require.Equal(t, 0.9, cfg.Compaction.WarnPercent)
	require.Equal(t, 3, cfg.CompactionRecentSessionTailEffective())
}

func TestConfig_NormalizeDefaultsCompactionSettings(t *testing.T) {
	cfg := &Config{}
	cfg.Normalize()
	require.Equal(t, constants.DefaultContextLength, cfg.Models.Main.ContextLength)
	require.True(t, getBoolValue(cfg.Compaction.Enabled))
	require.True(t, cfg.CompactionEnabled())
	require.Equal(t, 0.85, cfg.Compaction.TriggerPercent)
	require.Equal(t, 0.95, cfg.Compaction.WarnPercent)
	require.Equal(t, 8, cfg.CompactionRecentSessionTailEffective())
}

func TestConfig_ValidateRejectsInvalidCompactionSettings(t *testing.T) {
	cfg := &Config{
		Name: "daemon",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openrouter": {APIKey: "key"}},
			Main:      MainModelConfig{Name: "openai/model", ContextLength: 128000, Provider: "openrouter", BaseURL: "https://example.com", API: modelprovider.APIOpenAICompletions},
		},
		RPC:        RPCConfig{Address: "127.0.0.1", Port: 50051},
		Session:    SessionConfig{MaxIterations: 1, DefaultIdleExpiry: time.Hour, ArchiveRetention: 24 * time.Hour},
		Log:        LogConfig{Level: "info"},
		Storage:    StorageConfig{Backend: "memory"},
		Compaction: CompactionConfig{Enabled: new(true), TriggerPercent: 0.96, WarnPercent: 0.95},
	}

	err := cfg.Validate()
	require.EqualError(t, err, "compaction warn percent must be greater than or equal to "+
		"compaction trigger percent")
}

func TestConfig_ValidateRejectsInvalidCompactionRecentSessionTail(t *testing.T) {
	invalidTail := -1
	cfg := &Config{
		Name: "daemon",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openrouter": {APIKey: "key"}},
			Main: MainModelConfig{
				Name:          "openai/model",
				ContextLength: 128000,
				Provider:      "openrouter",
				BaseURL:       "https://example.com",
				API:           modelprovider.APIOpenAICompletions,
			},
		},
		RPC:     RPCConfig{Address: "127.0.0.1", Port: 50051},
		Session: SessionConfig{MaxIterations: 1, DefaultIdleExpiry: time.Hour, ArchiveRetention: 24 * time.Hour},
		Log:     LogConfig{Level: "info"},
		Storage: StorageConfig{Backend: "memory"},
		Compaction: CompactionConfig{
			Enabled:           new(true),
			TriggerPercent:    0.85,
			WarnPercent:       0.95,
			RecentSessionTail: &invalidTail,
		},
	}

	err := cfg.Validate()
	require.EqualError(t, err, "compaction recent session tail must be greater than or equal to zero")
}
