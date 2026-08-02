package db

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/datadir"
	"github.com/xymorphic/morph/internal/profile"
)

func TestOpen_ValidatesConfigAndBackend(t *testing.T) {
	db, err := Open(nil)
	require.Nil(t, db)
	require.EqualError(t, err, "config is required")
}

func TestOpenSQLite_ValidatesPath(t *testing.T) {
	db, err := OpenSQLite("")

	require.Nil(t, db)
	require.EqualError(t, err, "sqlite path is required")
}

func TestOpenSQLite_ConfiguresSQLiteConnection(t *testing.T) {
	db, err := OpenSQLite(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, sqlDB.Close())
	})

	require.Equal(t, 1, sqlDB.Stats().MaxOpenConnections)

	var busyTimeout int
	require.NoError(t, db.Raw(`PRAGMA busy_timeout`).Scan(&busyTimeout).Error)
	require.Equal(t, 2000, busyTimeout)

	var journalMode string
	require.NoError(t, db.Raw(`PRAGMA journal_mode`).Scan(&journalMode).Error)
	require.Equal(t, "wal", journalMode)

	var foreignKeys int
	require.NoError(t, db.Raw(`PRAGMA foreign_keys`).Scan(&foreignKeys).Error)
	require.Equal(t, 1, foreignKeys)
}

func TestSQLiteDSN_UsesImmediateWriteTransactions(t *testing.T) {
	dsn := sqliteDSN(filepath.Join(t.TempDir(), "state.db"))

	require.Contains(t, dsn, "_txlock=immediate")
	require.NotContains(t, dsn, "_journal_mode")
	require.Contains(t, sqliteDSN("file:state.db?mode=rw"), "&_txlock=immediate")
}

func TestOpen_OpensSQLiteAtStateDBPath(t *testing.T) {
	homeDir := t.TempDir()
	setProfileHome(t, homeDir)

	db, err := Open(&config.Config{Storage: config.StorageConfig{Backend: "sqlite"}})
	require.NoError(t, err)
	require.NotNil(t, db)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, sqlDB.Close())
	})

	require.FileExists(t, datadir.StateDBPath())
	require.Equal(t, filepath.Join(homeDir, "data", "state.db"), datadir.StateDBPath())
}

func setProfileHome(t *testing.T, home string) {
	t.Helper()

	original := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(original)
	})
	profile.SetActive(profile.Profile{Name: "test", HomeDir: home})
}
