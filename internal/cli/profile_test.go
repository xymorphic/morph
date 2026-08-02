package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	cli "github.com/urfave/cli/v3"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/profile"
)

func TestResolveConfigInputs_UsesProfileDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearEnv(t, profile.EnvName, "MORPH_ENV_FILE", "MORPH_CONFIG")

	var got ConfigInputs
	cmd := newProfileInputCommand(t, func(cmd *cli.Command) error {
		var err error
		got, err = ResolveConfigInputs(cmd)
		return err
	})

	err := cmd.Run(context.Background(), []string{"morph", "--profile", "Work"})

	require.NoError(t, err)
	profileHome := filepath.Join(home, ".morph", "profiles", "work")
	require.Equal(t, "work", got.Profile.Name)
	require.Equal(t, filepath.Join(profileHome, ".env"), got.EnvPath)
	require.Equal(t, filepath.Join(profileHome, "config.yaml"), got.ConfigPath)
	require.Equal(t, got.Profile, profile.Active())
}

func TestResolveConfigInputs_UsesProfileShortmorph(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearEnv(t, profile.EnvName, "MORPH_ENV_FILE", "MORPH_CONFIG")

	var got ConfigInputs
	cmd := newProfileInputCommand(t, func(cmd *cli.Command) error {
		var err error
		got, err = ResolveConfigInputs(cmd)
		return err
	})

	err := cmd.Run(context.Background(), []string{"morph", "-p", "Work"})

	require.NoError(t, err)
	profileHome := filepath.Join(home, ".morph", "profiles", "work")
	require.Equal(t, "work", got.Profile.Name)
	require.Equal(t, filepath.Join(profileHome, ".env"), got.EnvPath)
	require.Equal(t, filepath.Join(profileHome, "config.yaml"), got.ConfigPath)
}

func TestResolveConfigInputs_KeepsExplicitPathOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearEnv(t, profile.EnvName, "MORPH_ENV_FILE", "MORPH_CONFIG")

	envPath := filepath.Join(t.TempDir(), "custom.env")
	configPath := filepath.Join(t.TempDir(), "custom.yaml")

	var got ConfigInputs
	cmd := newProfileInputCommand(t, func(cmd *cli.Command) error {
		var err error
		got, err = ResolveConfigInputs(cmd)
		return err
	})

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--profile", "Work",
		"--env-file", envPath,
		"--config", configPath,
	})

	require.NoError(t, err)
	require.Equal(t, "work", got.Profile.Name)
	require.Equal(t, envPath, got.EnvPath)
	require.Equal(t, configPath, got.ConfigPath)
}

func TestResolveConfigInputs_KeepsEnvironmentPathOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearEnv(t, profile.EnvName)
	envPath := filepath.Join(t.TempDir(), "custom.env")
	configPath := filepath.Join(t.TempDir(), "custom.yaml")
	t.Setenv("MORPH_ENV_FILE", envPath)
	t.Setenv("MORPH_CONFIG", configPath)

	var got ConfigInputs
	cmd := newProfileInputCommand(t, func(cmd *cli.Command) error {
		var err error
		got, err = ResolveConfigInputs(cmd)
		return err
	})

	err := cmd.Run(context.Background(), []string{"morph", "--profile", "Work"})

	require.NoError(t, err)
	require.Equal(t, "work", got.Profile.Name)
	require.Equal(t, envPath, got.EnvPath)
	require.Equal(t, configPath, got.ConfigPath)
}

func TestResolveConfigInputs_UsesProfileEnvVar(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearEnv(t, "MORPH_ENV_FILE", "MORPH_CONFIG")
	t.Setenv(profile.EnvName, "Desk")

	var got ConfigInputs
	cmd := newProfileInputCommand(t, func(cmd *cli.Command) error {
		var err error
		got, err = ResolveConfigInputs(cmd)
		return err
	})

	err := cmd.Run(context.Background(), []string{"morph"})

	require.NoError(t, err)
	profileHome := filepath.Join(home, ".morph", "profiles", "desk")
	require.Equal(t, "desk", got.Profile.Name)
	require.Equal(t, filepath.Join(profileHome, ".env"), got.EnvPath)
	require.Equal(t, filepath.Join(profileHome, "config.yaml"), got.ConfigPath)
}

func TestResolveConfigInputs_UsesActiveProfileWhenCommandHasNoProfile(t *testing.T) {
	home := t.TempDir()
	active := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(active)
	})
	profile.SetActive(profile.Profile{
		Name:       "work",
		HomeDir:    home,
		ConfigPath: filepath.Join(home, "config.yaml"),
		EnvPath:    filepath.Join(home, ".env"),
	})

	inputs, err := ResolveConfigInputs(&cli.Command{})

	require.NoError(t, err)
	require.Equal(t, "work", inputs.Profile.Name)
	require.Equal(t, filepath.Join(home, ".env"), inputs.EnvPath)
	require.Equal(t, filepath.Join(home, "config.yaml"), inputs.ConfigPath)
	require.Equal(t, filepath.Join(home, ".env"), profile.Active().EnvPath)
	require.Equal(t, filepath.Join(home, "config.yaml"), profile.Active().ConfigPath)
}

func TestLoadConfig_UsesProfileConfigAndEnvDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearEnv(t, profile.EnvName, "MORPH_ENV_FILE", "MORPH_CONFIG", "MORPH_LOG_LEVEL")

	profileHome := filepath.Join(home, ".morph", "profiles", "work")
	require.NoError(t, os.MkdirAll(profileHome, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(profileHome, "config.yaml"), []byte(`
name: profile-agent
models:
`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(profileHome, ".env"), []byte("MORPH_LOG_LEVEL=debug\n"), 0o600))

	var got *config.Config
	var inputs ConfigInputs
	cmd := newProfileInputCommand(t, func(cmd *cli.Command) error {
		var err error
		got, inputs, err = LoadConfig(cmd)
		return err
	})

	err := cmd.Run(context.Background(), []string{"morph", "--profile", "Work"})

	require.NoError(t, err)
	require.Equal(t, filepath.Join(profileHome, ".env"), inputs.EnvPath)
	require.Equal(t, filepath.Join(profileHome, "config.yaml"), inputs.ConfigPath)
	require.NotNil(t, got)
	require.Equal(t, "profile-agent", got.Name)
	require.Equal(t, "debug", got.Log.Level)
}

func TestAddStartupFilesystemRoots_NormalizesConfiguredRootsAndAddsStartupRoots(t *testing.T) {
	profileHome := t.TempDir()
	workingDir := t.TempDir()
	t.Chdir(workingDir)
	cfg := &config.Config{
		FS: config.FSConfig{Roots: []string{"./workspace"}},
	}
	inputs := ConfigInputs{
		Profile: profile.Profile{HomeDir: profileHome},
	}

	AddStartupFilesystemRoots(cfg, inputs)

	require.Equal(t, []string{
		filepath.Join(workingDir, "workspace"),
		profileHome,
		workingDir,
	}, cfg.FS.Roots)
}

func TestAddStartupFilesystemRoots_SkipsProfileHomeWhenProfileAccessDisabled(t *testing.T) {
	profileHome := t.TempDir()
	workingDir := t.TempDir()
	t.Chdir(workingDir)
	cfg := &config.Config{
		FS: config.FSConfig{
			NoProfileAccess: true,
			Roots:           []string{profileHome, "./workspace"},
		},
	}
	inputs := ConfigInputs{
		Profile: profile.Profile{HomeDir: profileHome},
	}

	AddStartupFilesystemRoots(cfg, inputs)

	require.Equal(t, []string{
		filepath.Join(workingDir, "workspace"),
		workingDir,
	}, cfg.FS.Roots)
}

func TestLoadConfig_ReturnsConfigLoadError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearEnv(t, profile.EnvName, "MORPH_ENV_FILE", "MORPH_CONFIG")

	profileHome := filepath.Join(home, ".morph", "profiles", "work")
	require.NoError(t, os.MkdirAll(profileHome, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(profileHome, "config.yaml"), []byte("name: ["), 0o600))

	var inputs ConfigInputs
	cmd := newProfileInputCommand(t, func(cmd *cli.Command) error {
		var err error
		_, inputs, err = LoadConfig(cmd)
		return err
	})

	err := cmd.Run(context.Background(), []string{"morph", "--profile", "Work"})

	require.ErrorContains(t, err, "failed to parse config file")
	require.Equal(t, filepath.Join(profileHome, ".env"), inputs.EnvPath)
	require.Equal(t, filepath.Join(profileHome, "config.yaml"), inputs.ConfigPath)
}

func TestLoadConfig_ReturnsProfileResolutionError(t *testing.T) {
	cmd := newProfileInputCommand(t, func(cmd *cli.Command) error {
		_, _, err := LoadConfig(cmd)
		return err
	})

	err := cmd.Run(context.Background(), []string{"morph", "--profile", "work/team"})

	require.EqualError(t, err, `invalid profile name "work/team": must match [a-zA-Z0-9][a-zA-Z0-9_-]{0,63}`)
}

func TestResolveConfigInputs_UsesDefaultProfileWhenCommandNil(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearEnv(t, profile.EnvName, "MORPH_ENV_FILE", "MORPH_CONFIG")
	active := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(active)
	})
	profile.SetActive(profile.Profile{})

	inputs, err := ResolveConfigInputs(nil)

	require.NoError(t, err)
	profileHome := filepath.Join(home, ".morph", "profiles", "default")
	require.Equal(t, profile.DefaultName, inputs.Profile.Name)
	require.Equal(t, filepath.Join(profileHome, ".env"), inputs.EnvPath)
	require.Equal(t, filepath.Join(profileHome, "config.yaml"), inputs.ConfigPath)
}

func TestResolveConfigInputs_ReturnsInvalidProfileError(t *testing.T) {
	cmd := newProfileInputCommand(t, func(cmd *cli.Command) error {
		_, err := ResolveConfigInputs(cmd)
		return err
	})

	err := cmd.Run(context.Background(), []string{"morph", "--profile", "work/team"})

	require.EqualError(t, err, `invalid profile name "work/team": must match [a-zA-Z0-9][a-zA-Z0-9_-]{0,63}`)
}

func newProfileInputCommand(t *testing.T, action func(*cli.Command) error) *cli.Command {
	t.Helper()

	active := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(active)
	})

	envFile := ".env"
	configFile := "config.yaml"
	return &cli.Command{
		Flags: RootFlags(&envFile, &configFile),
		Action: func(_ context.Context, cmd *cli.Command) error {
			return action(cmd)
		},
	}
}

func clearEnv(t *testing.T, keys ...string) {
	t.Helper()
	keys = append(keys, "OPENAI_API_KEY", "OPENROUTER_API_KEY", "ANTHROPIC_API_KEY", "COPILOT_GITHUB_TOKEN")

	for _, key := range keys {
		original, ok := os.LookupEnv(key)
		if ok {
			t.Cleanup(func() {
				require.NoError(t, os.Setenv(key, original))
			})
		} else {
			t.Cleanup(func() {
				require.NoError(t, os.Unsetenv(key))
			})
		}
		require.NoError(t, os.Unsetenv(key))
	}
}
