package storesqlite

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	morphdb "github.com/wandxy/morph/internal/db"
	"github.com/wandxy/morph/internal/permissions"
	base "github.com/wandxy/morph/internal/state/core"
	"github.com/wandxy/morph/internal/state/search"
	"github.com/wandxy/morph/pkg/str"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Store persists sessions, messages, memory, and traces in SQLite.
type Store struct {
	vectors        *vectorConfig
	memoryReranker search.Reranker
	db             *gorm.DB
}

// NewStore returns a store backed by the supplied dependencies.
func NewStore(path string) (*Store, error) {
	pathValue := str.String(path)
	path = pathValue.Trim()
	if path == "" {
		return nil, errors.New("session sqlite path is required")
	}

	backend, err := gormOpenSQLite(path)
	if err != nil {
		return nil, err
	}

	return backend, nil
}

// NewStoreFromDB returns a store using an existing database handle.
func NewStoreFromDB(db *gorm.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("session db is required")
	}

	db = db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})

	if err := db.AutoMigrate(
		&sessionModel{},
		&stateModel{},
		&summaryModel{},
		&messageModel{},
		&automationJobModel{},
		&automationRunModel{},
		&gatewayBindingModel{},
		&traceEventModel{},
		&gatewayPairingRequestModel{},
		&gatewayPairedSenderModel{},
	); err != nil {
		return nil, fmt.Errorf("failed to migrate session db: %w", err)
	}
	if err := ensureMemoryStorage(db); err != nil {
		return nil, err
	}

	if err := ensureSearchIndex(db); err != nil {
		return nil, err
	}
	if err := db.AutoMigrate(&vectorIndexStateModel{}); err != nil {
		return nil, fmt.Errorf("failed to migrate vector index state: %w", err)
	}
	if err := db.AutoMigrate(&approvalRequestModel{}, &approvalGrantModel{}); err != nil {
		return nil, fmt.Errorf("failed to migrate permission db: %w", err)
	}
	if err := db.AutoMigrate(
		&sessionRunnerStateModel{},
		&sessionExecutionStateModel{},
		&sessionQueueEntryModel{},
		&sessionRunModel{},
		&sessionEventModel{},
	); err != nil {
		return nil, fmt.Errorf("failed to migrate session inbox db: %w", err)
	}
	if err := db.Exec(
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_session_runs_active
		 ON session_runs(session_id)
		 WHERE status = 'running'`,
	).Error; err != nil {
		return nil, fmt.Errorf("failed to migrate session run invariant: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Permission() (permissions.ApprovalStore, bool) {
	if s == nil || s.db == nil {
		return nil, false
	}

	return s, true
}

func (s *Store) Session() base.SessionStore {
	return s
}

func (s *Store) Automation() (base.AutomationStore, bool) {
	if s == nil || s.db == nil {
		return nil, false
	}

	return s, true
}

func (s *Store) Memory() (base.MemoryStore, bool) {
	if s == nil || s.db == nil {
		return nil, false
	}

	return s, true
}

func (s *Store) Trace() (base.TraceStore, bool) {
	if s == nil || s.db == nil {
		return nil, false
	}

	return s, true
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}

	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}

	return sqlDB.Close()
}

func gormOpenSQLite(path string) (*Store, error) {
	pathValue2 := str.String(path)
	path = pathValue2.Trim()
	if path == "" {
		return nil, errors.New("session sqlite path is required")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("failed to create session db directory: %w", err)
	}

	db, err := morphdb.OpenSQLite(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open session db: %w", err)
	}

	return NewStoreFromDB(db)
}
