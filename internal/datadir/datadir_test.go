package datadir

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wandxy/morph/internal/profile"
)

func TestHomeDir_UsesActiveProfile(t *testing.T) {
	resetProfile(t)

	profile.SetActive(profile.Profile{
		Name:    "work",
		HomeDir: filepath.Join("/Users/me", ".morph", "profiles", "work"),
	})

	require.Equal(t, filepath.Join("/Users/me", ".morph", "profiles", "work"), ProjectHomeDir())
	require.Equal(t, filepath.Join("/Users/me", ".morph", "profiles", "work"), HomeDir())
}

func TestHomeDir_ResolvesDefaultProfile(t *testing.T) {
	resetProfile(t)
	originalGetenv := getenv
	originalUserHomeDir := userHomeDir
	getenv = func(string) string { return "" }
	userHomeDir = func() (string, error) {
		return "/Users/me", nil
	}
	defer func() {
		getenv = originalGetenv
		userHomeDir = originalUserHomeDir
	}()

	require.Equal(t, filepath.Join("/Users/me", ".morph", "profiles", "default"), ProjectHomeDir())
	require.Equal(t, profile.DefaultName, profile.Active().Name)
}

func TestHomeDir_ResolvesEnvProfile(t *testing.T) {
	resetProfile(t)
	originalGetenv := getenv
	originalUserHomeDir := userHomeDir
	getenv = func(key string) string {
		if key == profile.EnvName {
			return "Research"
		}

		return ""
	}
	userHomeDir = func() (string, error) {
		return "/Users/me", nil
	}
	defer func() {
		getenv = originalGetenv
		userHomeDir = originalUserHomeDir
	}()

	require.Equal(t, filepath.Join("/Users/me", ".morph", "profiles", "research"), ProjectHomeDir())
	require.Equal(t, "research", profile.Active().Name)
}

func TestHomeDir_FallsBackWhenUserHomeFails(t *testing.T) {
	resetProfile(t)
	originalGetenv := getenv
	originalUserHomeDir := userHomeDir
	getenv = func(string) string { return "" }
	userHomeDir = func() (string, error) {
		return "", errors.New("home unavailable")
	}
	defer func() {
		getenv = originalGetenv
		userHomeDir = originalUserHomeDir
	}()

	require.Equal(t, filepath.Join(".morph", "profiles", "default"), ProjectHomeDir())
}

func TestHomeDir_FallsBackWhenEnvProfileIsInvalid(t *testing.T) {
	resetProfile(t)
	originalGetenv := getenv
	originalUserHomeDir := userHomeDir
	getenv = func(key string) string {
		if key == profile.EnvName {
			return "work/team"
		}

		return ""
	}
	userHomeDir = func() (string, error) {
		return "/Users/me", nil
	}
	defer func() {
		getenv = originalGetenv
		userHomeDir = originalUserHomeDir
	}()

	require.Equal(t, filepath.Join(".morph", "profiles", "default"), ProjectHomeDir())
	require.Empty(t, profile.Active().HomeDir)
}

func TestProjectPaths_DeriveFromActiveProfileHome(t *testing.T) {
	resetProfile(t)

	profileHome := filepath.Join("/Users/me", ".morph", "profiles", "work")
	profile.SetActive(profile.Profile{Name: "work", HomeDir: profileHome})

	require.Equal(t, profileHome, HomeDir())
	require.Equal(t, filepath.Join(profileHome, "data"), DataDir())
	require.Equal(t, filepath.Join(profileHome, "traces"), DebugTraceDir())
	require.Equal(t, filepath.Join(profileHome, "data", "state.db"), StateDBPath())
	require.Equal(t, filepath.Join(profileHome, "data", "auth.db"), AuthDBPath())
	require.Equal(t, filepath.Join(profileHome, "data", "session.db"), SessionDBPath())
}

func TestProjectPaths_IsolateProfiles(t *testing.T) {
	resetProfile(t)

	workHome := filepath.Join("/Users/me", ".morph", "profiles", "work")
	personalHome := filepath.Join("/Users/me", ".morph", "profiles", "personal")

	profile.SetActive(profile.Profile{Name: "work", HomeDir: workHome})
	require.Equal(t, filepath.Join(workHome, "data", "state.db"), StateDBPath())
	require.Equal(t, filepath.Join(workHome, "data", "auth.db"), AuthDBPath())
	require.Equal(t, filepath.Join(workHome, "traces"), DebugTraceDir())
	require.Equal(t, filepath.Join(workHome, "SOUL.md"), filepath.Join(HomeDir(), "SOUL.md"))
	require.Equal(t, filepath.Join(workHome, "memory.md"), filepath.Join(HomeDir(), "memory.md"))

	profile.SetActive(profile.Profile{Name: "personal", HomeDir: personalHome})
	require.Equal(t, filepath.Join(personalHome, "data", "state.db"), StateDBPath())
	require.Equal(t, filepath.Join(personalHome, "data", "auth.db"), AuthDBPath())
	require.Equal(t, filepath.Join(personalHome, "traces"), DebugTraceDir())
	require.Equal(t, filepath.Join(personalHome, "SOUL.md"), filepath.Join(HomeDir(), "SOUL.md"))
	require.Equal(t, filepath.Join(personalHome, "memory.md"), filepath.Join(HomeDir(), "memory.md"))
}

func resetProfile(t *testing.T) {
	t.Helper()

	original := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(original)
	})
	profile.SetActive(profile.Profile{})
}
