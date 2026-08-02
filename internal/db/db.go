package db

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/datadir"
	"github.com/xymorphic/morph/pkg/str"
)

const sqliteBusyTimeout = 2 * time.Second

// Open opens the configured database connection.
func Open(cfg *config.Config) (*gorm.DB, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}

	return OpenSQLite(datadir.StateDBPath())
}

// OpenSQLite opens a SQLite database and applies connection settings.
func OpenSQLite(path string) (*gorm.DB, error) {
	pathValue := str.String(path)
	path = pathValue.Trim()
	if path == "" {
		return nil, errors.New("sqlite path is required")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("failed to create sqlite db directory: %w", err)
	}

	db, err := gorm.Open(sqlite.Open(sqliteDSN(path)), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite db: %w", err)
	}
	if err := ConfigureSQLite(db); err != nil {
		return nil, err
	}

	return db, nil
}

// ConfigureSQLite applies SQLite pragmas used by Morph stores.
func ConfigureSQLite(db *gorm.DB) error {
	if db == nil {
		return errors.New("sqlite db is required")
	}

	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to configure sqlite db: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)

	if err := db.Exec(`PRAGMA busy_timeout = ` + strconv.FormatInt(sqliteBusyTimeout.Milliseconds(), 10)).Error; err != nil {
		return fmt.Errorf("failed to configure sqlite busy timeout: %w", err)
	}
	if err := db.Exec(`PRAGMA foreign_keys = ON`).Error; err != nil {
		return fmt.Errorf("failed to configure sqlite foreign keys: %w", err)
	}
	if err := db.Exec(`PRAGMA journal_mode = WAL`).Error; err != nil {
		return fmt.Errorf("failed to configure sqlite journal mode: %w", err)
	}

	return nil
}

func sqliteDSN(path string) string {
	pathValue2 := str.String(path)
	path = pathValue2.Trim()

	params := "_busy_timeout=" + strconv.FormatInt(sqliteBusyTimeout.Milliseconds(), 10) +
		"&_foreign_keys=ON&_txlock=immediate"
	if strings.Contains(path, "?") {
		return path + "&" + params
	}

	return path + "?" + params
}
