package e2e

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/permissions"
)

func TestDefaultSpec_DefaultPaths(t *testing.T) {
	spec := DefaultSpec("/tmp/morph-home")

	require.NoError(t, spec.Validate())
	assert.Equal(t, EntrypointDirectAgent, spec.PrimaryEntrypoint)
	assert.Equal(t, EntrypointCommandRPC, spec.SecondaryEntrypoint)
	assert.True(t, spec.Config.AllowInMemory)
	assert.Equal(t, "/tmp/morph-home/workspace", spec.Isolation.WorkspaceDir)
	assert.Equal(t, "/tmp/morph-home/data", spec.Isolation.DataDir)
	assert.Equal(t, "/tmp/morph-home/data/state.db", spec.Isolation.StoragePath)
	assert.Equal(t, "/tmp/morph-home/traces", spec.Isolation.TraceDir)
}

func TestDefaultConfig_DefaultsAndOverrides(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		cfg := DefaultConfig(ConfigOptions{})
		require.NotNil(t, cfg)
		require.NotNil(t, cfg.Models.Main.Stream)
		assert.Equal(t, "Test Morph", cfg.Name)
		assert.Equal(t, "test-model", cfg.Models.Main.Name)
		assert.Equal(t, "sqlite", cfg.Storage.Backend)
		assert.False(t, *cfg.Models.Main.Stream)
		require.Len(t, cfg.Permissions.Rules, 1)
		assert.Equal(t, permissions.DecisionAllow, cfg.Permissions.Rules[0].Decision)
	})

	t.Run("overrides", func(t *testing.T) {
		cfg := DefaultConfig(ConfigOptions{
			Name:           "RPC Test",
			StorageBackend: "memory",
			Stream:         true,
		})
		require.NotNil(t, cfg)
		require.NotNil(t, cfg.Models.Main.Stream)
		assert.Equal(t, "RPC Test", cfg.Name)
		assert.Equal(t, "memory", cfg.Storage.Backend)
		assert.True(t, *cfg.Models.Main.Stream)
	})

	t.Run("trims and falls back", func(t *testing.T) {
		cfg := DefaultConfig(ConfigOptions{
			Name:           "  ",
			StorageBackend: "  ",
		})
		require.NotNil(t, cfg)
		assert.Equal(t, "Test Morph", cfg.Name)
		assert.Equal(t, "sqlite", cfg.Storage.Backend)
	})
}

func TestDefaultSpec_EmptyHomeStillBuildsExpectedLayout(t *testing.T) {
	spec := DefaultSpec("")

	assert.Equal(t, filepath.Join("", "workspace"), spec.Isolation.WorkspaceDir)
	assert.Equal(t, filepath.Join("", "data"), spec.Isolation.DataDir)
	assert.Equal(t, filepath.Join("", "data", "state.db"), spec.Isolation.StoragePath)
	assert.Equal(t, filepath.Join("", "traces"), spec.Isolation.TraceDir)
}
