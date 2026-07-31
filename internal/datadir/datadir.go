package datadir

import (
	"os"
	"path/filepath"

	"github.com/wandxy/morph/internal/datadir/files"
	"github.com/wandxy/morph/internal/profile"
	"github.com/wandxy/morph/pkg/str"
)

const StateFilename = files.StateFilename

var (
	getenv      = os.Getenv
	userHomeDir = os.UserHomeDir
)

// ProjectHomeDir returns the per-project Morph data directory.
func ProjectHomeDir() string {
	return HomeDir()
}

// HomeDir returns the configured Morph home directory.
func HomeDir() string {
	active := profile.Active()
	activeHomeDir := str.String(active.HomeDir)
	if activeHomeDir.Trim() != "" {
		return active.HomeDir
	}

	userHome := loadUserHomeDir()
	if userHome == "" {
		return filepath.Join(".morph", "profiles", profile.DefaultName)
	}

	resolved, err := profile.Resolve(profile.ResolveOptions{
		Env:         map[string]string{profile.EnvName: getenv(profile.EnvName)},
		UserHomeDir: userHome,
	})
	if err != nil {
		return filepath.Join(".morph", "profiles", profile.DefaultName)
	}

	profile.SetActive(resolved)
	return resolved.HomeDir
}

func loadUserHomeDir() string {
	home, err := userHomeDir()
	homeValue := str.String(home)
	if err != nil || homeValue.Trim() == "" {
		return ""
	}

	return home
}

// DataDir returns the directory used for persistent Morph data.
func DataDir() string {
	return filepath.Join(HomeDir(), "data")
}

// DebugTraceDir returns the directory used for debug trace files.
func DebugTraceDir() string {
	return filepath.Join(HomeDir(), "traces")
}

func UnsafeEvidenceDir() string {
	return filepath.Join(HomeDir(), "unsafe-evidence")
}

// StateDBPath returns the path to the project state database.
func StateDBPath() string {
	return filepath.Join(DataDir(), "state.db")
}

func AuthDBPath() string {
	return filepath.Join(DataDir(), "auth.db")
}

// SessionDBPath returns the path to the project session database.
func SessionDBPath() string {
	return filepath.Join(DataDir(), "session.db")
}
