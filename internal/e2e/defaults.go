package e2e

import (
	"path/filepath"
	"time"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/pkg/str"
)

// ConfigOptions customizes the default e2e config.
type ConfigOptions struct {
	Name           string
	StorageBackend string
	Stream         bool
}

// DefaultSpec returns the default e2e scenario specification.
func DefaultSpec(home string) HarnessSpec {
	homeValue := str.String(home)
	home = homeValue.Trim()
	dataDir := filepath.Join(home, "data")

	return HarnessSpec{
		PrimaryEntrypoint:   EntrypointDirectAgent,
		SecondaryEntrypoint: EntrypointCommandRPC,
		Config:              ConfigInput{AllowInMemory: true},
		Isolation: Isolation{
			WorkspaceDir: filepath.Join(home, "workspace"),
			DataDir:      dataDir,
			StoragePath:  filepath.Join(dataDir, "state.db"),
			TraceDir:     filepath.Join(home, "traces"),
		},
	}
}

// DefaultConfig returns the default e2e harness configuration.
func DefaultConfig(opts ConfigOptions) *config.Config {
	stream := opts.Stream
	nameValue := str.String(opts.Name)
	name := nameValue.Trim()
	if name == "" {
		name = "Test Morph"
	}
	storageBackendValue := str.String(opts.StorageBackend)
	storageBackend := storageBackendValue.Trim()
	if storageBackend == "" {
		storageBackend = "sqlite"
	}

	return &config.Config{
		Name:    name,
		Models:  config.ModelsConfig{Main: config.MainModelConfig{Name: "test-model", Stream: &stream}},
		Storage: config.StorageConfig{Backend: storageBackend},
		Session: config.SessionConfig{DefaultIdleExpiry: time.Hour, ArchiveRetention: 24 * time.Hour},
		Permissions: permissions.Policy{Rules: []permissions.Rule{{
			Name:     "allow e2e harness operations",
			Decision: permissions.DecisionAllow,
		}}},
	}
}
