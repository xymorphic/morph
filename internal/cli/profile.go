package cli

import (
	"os"
	"path/filepath"

	cli "github.com/urfave/cli/v3"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/profile"
	"github.com/xymorphic/morph/pkg/str"
)

// ConfigInputs are the resolved profile-aware config inputs for a command.
type ConfigInputs struct {
	Profile    profile.Profile
	EnvPath    string
	ConfigPath string
}

// ResolveConfigInputs resolves the active profile before config and env loading.
func ResolveConfigInputs(cmd *cli.Command) (ConfigInputs, error) {
	profileName := getCommandProfile(cmd)
	resolved := profile.Active()
	homeDirValue := str.String(resolved.HomeDir)
	if profileName != "" || homeDirValue.Trim() == "" {
		var err error
		resolved, err = profile.Resolve(profile.ResolveOptions{Name: profileName})
		if err != nil {
			return ConfigInputs{}, err
		}
	}

	resolved = profile.WithMetadataPaths(resolved)
	profile.SetActive(resolved)
	inputs := ConfigInputs{
		Profile:    resolved,
		EnvPath:    resolved.EnvPath,
		ConfigPath: resolved.ConfigPath,
	}
	if cmd != nil {
		if cmd.IsSet("env-file") {
			literalValue := str.String(cmd.String("env-file"))
			inputs.EnvPath = literalValue.Trim()
		}
		if cmd.IsSet("config") {
			literalValue2 := str.String(cmd.String("config"))
			inputs.ConfigPath = literalValue2.Trim()
		}
	}

	return inputs, nil
}

// LoadConfig loads config from the active profile unless command flags override the paths.
func LoadConfig(cmd *cli.Command) (*config.Config, ConfigInputs, error) {
	inputs, err := ResolveConfigInputs(cmd)
	if err != nil {
		return nil, ConfigInputs{}, err
	}

	cfg, err := config.Load(inputs.EnvPath, inputs.ConfigPath)
	if err != nil {
		return nil, inputs, err
	}

	return cfg, inputs, nil
}

// AddStartupFilesystemRoots adds startup filesystem roots to cfg from CLI flags.
func AddStartupFilesystemRoots(cfg *config.Config, inputs ConfigInputs) {
	if cfg == nil {
		return
	}

	roots := make([]string, 0, 2)
	if !cfg.FS.NoProfileAccess {
		roots = append(roots, inputs.Profile.HomeDir)
	} else {
		cfg.FS.Roots = removeFilesystemRoot(cfg.FS.Roots, inputs.Profile.HomeDir)
	}
	if cwd, err := os.Getwd(); err == nil {
		roots = append(roots, cwd)
	}
	config.AddFilesystemRoots(cfg, roots...)
}

func removeFilesystemRoot(roots []string, target string) []string {
	targetValue := str.String(target)
	target = targetValue.Trim()
	if target == "" {
		return roots
	}

	normalizedTarget := normalizeFilesystemRootTarget(target)
	filtered := make([]string, 0, len(roots))
	for _, root := range roots {
		if normalizeFilesystemRootTarget(root) == normalizedTarget {
			continue
		}

		filtered = append(filtered, root)
	}

	return filtered
}

func normalizeFilesystemRootTarget(root string) string {
	rootValue := str.String(root)
	root = rootValue.Trim()
	if root == "" {
		return ""
	}
	if filepath.IsAbs(root) {
		return filepath.Clean(root)
	}
	if cwd, err := os.Getwd(); err == nil {
		return filepath.Clean(filepath.Join(cwd, root))
	}
	return filepath.Clean(root)
}

func getCommandProfile(cmd *cli.Command) string {
	if cmd == nil {
		return ""
	}

	for _, candidate := range cmd.Lineage() {
		if candidate.IsSet("profile") {
			literalValue3 := str.String(candidate.String("profile"))
			return literalValue3.Trim()
		}
	}

	return ""
}
