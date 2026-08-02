package daemon

import (
	"errors"

	urfavecli "github.com/urfave/cli/v3"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/profile"
)

// ConfigInputs are the resolved profile-aware config inputs for a daemon command.
type ConfigInputs struct {
	Profile    profile.Profile
	EnvPath    string
	ConfigPath string
}

// Dependencies contains CLI package hooks the daemon runtime needs.
type Dependencies struct {
	LoadConfig                func(*urfavecli.Command) (*config.Config, ConfigInputs, error)
	ApplyConfigOverrides      func(*urfavecli.Command, *config.Config)
	AddStartupFilesystemRoots func(*config.Config, ConfigInputs)
	SafetySummary             func(*config.Config) string
}

func (deps Dependencies) loadConfig(cmd *urfavecli.Command) (*config.Config, ConfigInputs, error) {
	if deps.LoadConfig == nil {
		return nil, ConfigInputs{}, errors.New("daemon config loader is required")
	}

	return deps.LoadConfig(cmd)
}

func (deps Dependencies) applyConfigOverrides(cmd *urfavecli.Command, cfg *config.Config) {
	if deps.ApplyConfigOverrides != nil {
		deps.ApplyConfigOverrides(cmd, cfg)
	}
}

func (deps Dependencies) addStartupFilesystemRoots(cfg *config.Config, inputs ConfigInputs) {
	if deps.AddStartupFilesystemRoots != nil {
		deps.AddStartupFilesystemRoots(cfg, inputs)
	}
}

func (deps Dependencies) safetySummary(cfg *config.Config) string {
	if deps.SafetySummary == nil {
		return ""
	}

	return deps.SafetySummary(cfg)
}
