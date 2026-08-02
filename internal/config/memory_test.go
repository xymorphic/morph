package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	modelprovider "github.com/xymorphic/morph/internal/model/provider"
)

func TestConfig_MemoryDefaultsAndNormalize(t *testing.T) {
	var cfg *Config
	require.False(t, cfg.MemoryEnabled())
	require.Empty(t, cfg.MemoryBackendEffective())

	cfg = &Config{Memory: MemoryConfig{Provider: " Default-Memory ", Backend: " SQLite "}}
	cfg.Normalize()
	require.True(t, cfg.MemoryEnabled())
	require.Equal(t, "default-memory", cfg.Memory.Provider)
	require.Equal(t, "sqlite", cfg.Memory.Backend)
	require.True(t, getBoolValue(cfg.Memory.Pinned.Enabled))
	require.True(t, getBoolValue(cfg.Memory.Retrieval.Enabled))
	require.True(t, getBoolValue(cfg.Memory.Flush.Enabled))
	require.Equal(t, 2, cfg.Memory.Flush.MaxCalls)
	require.Equal(t, int64(512), cfg.Memory.Flush.MaxOutputTokens)
	require.Equal(t, 10*time.Second, cfg.Memory.Flush.Timeout)
	require.False(t, getBoolValue(cfg.Memory.Episodic.Enabled))
	require.False(t, getBoolValue(cfg.Memory.Reflection.Enabled))
	require.True(t, getBoolValue(cfg.Memory.Promotion.Enabled))
	require.True(t, getBoolValue(cfg.Memory.Write.Enabled))
	require.True(t, cfg.MemoryPinnedEnabled())
	require.True(t, cfg.MemoryRetrievalEnabled())
	require.True(t, cfg.MemoryFlushEnabled())
	require.False(t, cfg.MemoryEpisodicEnabled())
	require.False(t, cfg.MemoryReflectionEnabled())
	require.True(t, cfg.MemoryPromotionEnabled())
	require.Equal(t, 7*24*time.Hour, cfg.Memory.Promotion.EvaluatedRetention)
	require.True(t, cfg.MemoryWriteEnabled())

	cfg = &Config{Memory: MemoryConfig{Enabled: new(false)}}
	cfg.Normalize()
	require.False(t, cfg.MemoryEnabled())
	require.Equal(t, "default-memory", cfg.Memory.Provider)
	require.Equal(t, "sqlite", cfg.MemoryBackendEffective())

	cfg = &Config{Storage: StorageConfig{Backend: "memory"}, Memory: MemoryConfig{Backend: "sqlite"}}
	cfg.Normalize()
	require.Equal(t, "sqlite", cfg.MemoryBackendEffective())

	cfg = &Config{Memory: MemoryConfig{
		Reflection: ReflectionMemoryConfig{
			Enabled:      new(true),
			Interval:     time.Minute,
			Limit:        6,
			RelatedLimit: 2,
		},
		Promotion: PromotionMemoryConfig{
			Enabled:            new(true),
			Interval:           time.Minute,
			Limit:              7,
			EvaluatedRetention: 48 * time.Hour,
		},
		Write: WriteMemoryConfig{
			Enabled: new(true),
		},
	}}
	cfg.Normalize()
	require.True(t, getBoolValue(cfg.Memory.Reflection.Enabled))
	require.Equal(t, time.Minute, cfg.Memory.Reflection.Interval)
	require.Equal(t, 6, cfg.Memory.Reflection.Limit)
	require.Equal(t, 2, cfg.Memory.Reflection.RelatedLimit)
	require.True(t, getBoolValue(cfg.Memory.Promotion.Enabled))
	require.Equal(t, time.Minute, cfg.Memory.Promotion.Interval)
	require.Equal(t, 7, cfg.Memory.Promotion.Limit)
	require.Equal(t, 48*time.Hour, cfg.Memory.Promotion.EvaluatedRetention)
	require.True(t, getBoolValue(cfg.Memory.Write.Enabled))

	cfg = &Config{Memory: MemoryConfig{Pinned: PinnedMemoryConfig{MaxChars: 120, MaxItemChars: 60}}}
	cfg.Normalize()
	require.Equal(t, 120, cfg.Memory.Pinned.MaxChars)
	require.Equal(t, 60, cfg.Memory.Pinned.MaxItemChars)
}

func TestConfig_ValidateRejectsInvalidMemoryBackend(t *testing.T) {
	cfg := &Config{
		Name: "daemon",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openrouter": {APIKey: "key"}},
			Main:      MainModelConfig{Name: "openai/model", Provider: "openrouter", BaseURL: "https://example.com", API: modelprovider.APIOpenAICompletions},
		},
		RPC:     RPCConfig{Address: "127.0.0.1", Port: 50051},
		Session: SessionConfig{MaxIterations: 1},
		Log:     LogConfig{Level: "info"},
		Storage: StorageConfig{Backend: "sqlite"},
		Memory:  MemoryConfig{Backend: "bogus"},
	}

	err := cfg.Validate()
	require.EqualError(t, err, "memory backend must be one of: memory, sqlite")
}
