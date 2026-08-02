package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/datadir/files"
	"github.com/xymorphic/morph/internal/profile"
)

func TestLoadLastSessionIDReturnsEmptyWhenUnset(t *testing.T) {
	setActiveTestProfile(t, t.TempDir())

	id, err := loadLastSessionID()

	require.NoError(t, err)
	require.Empty(t, id)
}

func TestLoadLastSessionIDReturnsEmptyWhenStatePathIsUnavailable(t *testing.T) {
	setActiveProfileForLastSessionTest(t, profile.Profile{})

	id, err := loadLastSessionID()

	require.NoError(t, err)
	require.Empty(t, id)
}

func TestLoadLastSessionIDReportsReadError(t *testing.T) {
	home := t.TempDir()
	setActiveTestProfile(t, home)
	require.NoError(t, os.Mkdir(filepath.Join(home, files.StateFilename), 0o700))

	_, err := loadLastSessionID()

	require.ErrorContains(t, err, "read last session")
}

func TestSaveLastSessionIDPersistsTrimmedID(t *testing.T) {
	home := t.TempDir()
	setActiveTestProfile(t, home)

	err := saveLastSessionID(" session-a ")

	require.NoError(t, err)
	id, err := loadLastSessionID()
	require.NoError(t, err)
	require.Equal(t, "session-a", id)
}

func TestSaveLastSessionIDSkipsBlankAndUnavailableStatePath(t *testing.T) {
	home := t.TempDir()
	setActiveTestProfile(t, home)
	require.NoError(t, saveLastSessionID(" "))
	_, err := os.Stat(filepath.Join(home, files.StateFilename))
	require.True(t, os.IsNotExist(err))

	setActiveProfileForLastSessionTest(t, profile.Profile{})
	require.NoError(t, saveLastSessionID("session-a"))
}

func TestSaveLastSessionIDReportsCreateDirError(t *testing.T) {
	homeFile, err := os.CreateTemp(t.TempDir(), "profile-home-*")
	require.NoError(t, err)
	require.NoError(t, homeFile.Close())
	setActiveTestProfile(t, homeFile.Name())

	err = saveLastSessionID("session-a")

	require.ErrorContains(t, err, "create profile metadata dir")
}

func TestSaveLastSessionIDReportsStateLoadError(t *testing.T) {
	home := t.TempDir()
	setActiveTestProfile(t, home)
	require.NoError(t, os.WriteFile(filepath.Join(home, files.StateFilename), []byte("{"), 0o600))

	err := saveLastSessionID("session-a")

	require.ErrorContains(t, err, "parse tui state")
}

func TestLoadLastSessionIDReportsInvalidJSON(t *testing.T) {
	home := t.TempDir()
	setActiveTestProfile(t, home)
	require.NoError(t, os.WriteFile(
		filepath.Join(home, files.StateFilename),
		[]byte("{"),
		0o600,
	))

	_, err := loadLastSessionID()

	require.ErrorContains(t, err, "parse tui state")
}

func TestLoadAppTUIStateReportsReadAndParseErrors(t *testing.T) {
	state, err := loadAppTUIState(filepath.Join(t.TempDir(), "missing-state.json"))
	require.NoError(t, err)
	require.Empty(t, state)

	dirPath := filepath.Join(t.TempDir(), "state-dir")
	require.NoError(t, os.Mkdir(dirPath, 0o700))
	_, err = loadAppTUIState(dirPath)
	require.ErrorContains(t, err, "read tui state")

	badPath := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, os.WriteFile(badPath, []byte("{"), 0o600))
	_, err = loadAppTUIState(badPath)
	require.ErrorContains(t, err, "parse tui state")
}

func TestLastSessionIDIsStoredPerActiveProfile(t *testing.T) {
	root := t.TempDir()
	setActiveProfileForLastSessionTest(t, profile.Profile{
		Name:    "default",
		HomeDir: filepath.Join(root, ".morph", "profiles", "default"),
	})
	require.NoError(t, saveLastSessionID("session-default"))
	setActiveProfileForLastSessionTest(t, profile.Profile{
		Name:    "work",
		HomeDir: filepath.Join(root, ".morph", "profiles", "work"),
	})
	require.NoError(t, saveLastSessionID("session-work"))

	id, err := loadLastSessionID()

	require.NoError(t, err)
	require.Equal(t, "session-work", id)

	data, err := os.ReadFile(filepath.Join(root, ".morph", files.StateFilename))
	require.NoError(t, err)
	var state appTUIState
	require.NoError(t, json.Unmarshal(data, &state))
	require.Equal(t, map[string]string{
		"default": "session-default",
		"work":    "session-work",
	}, state.LastSessions)
}

func TestAppTUIStatePathAndProfileDefaults(t *testing.T) {
	setActiveProfileForLastSessionTest(t, profile.Profile{})
	require.Empty(t, appTUIStatePath())
	require.Equal(t, profile.DefaultName, getActiveProfileName())

	home := t.TempDir()
	setActiveProfileForLastSessionTest(t, profile.Profile{Name: " ", HomeDir: home})
	require.Equal(t, filepath.Join(home, files.StateFilename), appTUIStatePath())
	require.Equal(t, profile.DefaultName, getActiveProfileName())
	require.True(t, strings.HasSuffix(string(encodeAppTUIState(appTUIState{})), "{}"))
}

func setActiveProfileForLastSessionTest(t *testing.T, active profile.Profile) {
	t.Helper()

	original := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(original)
	})

	profile.SetActive(active)
}
