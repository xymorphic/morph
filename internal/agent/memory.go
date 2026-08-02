package agent

import (
	"github.com/xymorphic/morph/internal/environment"
	"github.com/xymorphic/morph/internal/memory"
)

// MemorySource exposes the environment memory provider to agent turn code.
type MemorySource struct {
	env environment.Environment
}

// NewMemorySource returns a memory provider source backed by env.
func NewMemorySource(env environment.Environment) MemorySource {
	return MemorySource{env: env}
}

// MemoryProvider returns the configured memory provider, if one is available.
func (s MemorySource) MemoryProvider() memory.Provider {
	if s.env == nil {
		return nil
	}

	return s.env.MemoryProvider()
}
