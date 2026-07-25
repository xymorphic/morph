package storesqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
	morphauth "github.com/wandxy/morph/internal/auth"
	"github.com/wandxy/morph/internal/auth/storememory"
)

type Store struct {
	mu             sync.Mutex
	db             *sql.DB
	memory         *storememory.Store
	lastUsePersist time.Time
	useDirty       bool
}

const recordUsePersistInterval = time.Second

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("auth database path is required")
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("auth database must not be a symbolic link")
	}
	dsn := "file:" + filepath.ToSlash(path) + "?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=on"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open auth database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS morph_auth_state (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			payload BLOB NOT NULL,
			updated_at TEXT NOT NULL
		)
	`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize auth database: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("secure auth database: %w", err)
	}
	snapshot, err := loadSnapshot(db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	return &Store{db: db, memory: storememory.NewFromSnapshot(snapshot)}, nil
}

func (s *Store) SeedRoot(
	ctx context.Context,
	authorization morphauth.Authorization,
) (morphauth.Authorization, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := s.memory.Snapshot()
	result, err := s.memory.SeedRoot(ctx, authorization)
	if err != nil {
		return morphauth.Authorization{}, err
	}
	if err := s.persist(ctx); err != nil {
		s.memory = storememory.NewFromSnapshot(before)
		return morphauth.Authorization{}, err
	}

	return result, nil
}

func (s *Store) GetAuthorization(
	ctx context.Context,
	identityID string,
) (morphauth.Authorization, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.memory.GetAuthorization(ctx, identityID)
}

func (s *Store) PutAuthorization(
	ctx context.Context,
	authorization morphauth.Authorization,
) (morphauth.Authorization, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := s.memory.Snapshot()
	result, err := s.memory.PutAuthorization(ctx, authorization)
	if err != nil {
		return morphauth.Authorization{}, err
	}
	if err := s.persist(ctx); err != nil {
		s.memory = storememory.NewFromSnapshot(before)
		return morphauth.Authorization{}, err
	}

	return result, nil
}

func (s *Store) RotateIdentity(
	ctx context.Context,
	currentIdentityID string,
	next morphauth.Authorization,
	now time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := s.memory.Snapshot()
	if err := s.memory.RotateIdentity(ctx, currentIdentityID, next, now); err != nil {
		return err
	}

	return s.persistOrRestore(ctx, before)
}

func (s *Store) ListAuthorizations(ctx context.Context) ([]morphauth.Authorization, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.memory.ListAuthorizations(ctx)
}

func (s *Store) Activate(
	ctx context.Context,
	session morphauth.Session,
	token morphauth.Token,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := s.memory.Snapshot()
	if err := s.memory.Activate(ctx, session, token); err != nil {
		return err
	}

	return s.persistOrRestore(ctx, before)
}

func (s *Store) GetSession(ctx context.Context, id string) (morphauth.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.memory.GetSession(ctx, id)
}

func (s *Store) ListSessions(ctx context.Context) ([]morphauth.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.memory.ListSessions(ctx)
}

func (s *Store) GetToken(ctx context.Context, id string) (morphauth.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.memory.GetToken(ctx, id)
}

func (s *Store) ListTokens(ctx context.Context) ([]morphauth.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.memory.ListTokens(ctx)
}

func (s *Store) RecordUse(
	ctx context.Context,
	sessionID, tokenID, method string,
	now, idleExpiresAt time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	shouldPersist := s.lastUsePersist.IsZero() ||
		now.Sub(s.lastUsePersist) >= recordUsePersistInterval
	var before storememory.Snapshot
	if shouldPersist {
		before = s.memory.Snapshot()
	}
	if err := s.memory.RecordUse(ctx, sessionID, tokenID, method, now, idleExpiresAt); err != nil {
		return err
	}
	s.useDirty = true
	if !shouldPersist {
		return nil
	}
	if err := s.persistOrRestore(ctx, before); err != nil {
		return err
	}
	s.lastUsePersist = now
	s.useDirty = false

	return nil
}

func (s *Store) KeepAliveSession(
	ctx context.Context,
	sessionID string,
	now, idleExpiresAt time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.memory.KeepAliveSession(ctx, sessionID, now, idleExpiresAt); err != nil {
		return err
	}
	s.useDirty = true

	return nil
}

func (s *Store) RevokeSession(
	ctx context.Context,
	id, reason string,
	now time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := s.memory.Snapshot()
	if err := s.memory.RevokeSession(ctx, id, reason, now); err != nil {
		return err
	}

	return s.persistOrRestore(ctx, before)
}

func (s *Store) RevokeToken(
	ctx context.Context,
	id, reason string,
	now time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := s.memory.Snapshot()
	if err := s.memory.RevokeToken(ctx, id, reason, now); err != nil {
		return err
	}

	return s.persistOrRestore(ctx, before)
}

func (s *Store) AppendAudit(ctx context.Context, event morphauth.AuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := s.memory.Snapshot()
	if err := s.memory.AppendAudit(ctx, event); err != nil {
		return err
	}

	return s.persistOrRestore(ctx, before)
}

func (s *Store) ListAudit(ctx context.Context, limit int) ([]morphauth.AuditEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.memory.ListAudit(ctx, limit)
}

func (s *Store) Prune(ctx context.Context, cutoff time.Time, limit int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := s.memory.Snapshot()
	count, err := s.memory.Prune(ctx, cutoff, limit)
	if err != nil {
		return 0, err
	}
	if count == 0 {
		return 0, nil
	}
	if err := s.persistOrRestore(ctx, before); err != nil {
		return 0, err
	}

	return count, nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	var persistErr error
	if s.useDirty {
		persistErr = s.persist(context.Background())
	}
	closeErr := s.db.Close()
	s.db = nil
	if persistErr != nil {
		return persistErr
	}
	return closeErr
}

func (s *Store) persist(ctx context.Context) error {
	if s.db == nil {
		return errors.New("auth database is closed")
	}
	body, err := json.Marshal(s.memory.Snapshot())
	if err != nil {
		return fmt.Errorf("encode auth state: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO morph_auth_state (id, payload, updated_at)
		VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET payload = excluded.payload, updated_at = excluded.updated_at
	`, body, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("persist auth state: %w", err)
	}
	s.useDirty = false

	return nil
}

func (s *Store) persistOrRestore(
	ctx context.Context,
	before storememory.Snapshot,
) error {
	if err := s.persist(ctx); err != nil {
		s.memory = storememory.NewFromSnapshot(before)
		return err
	}

	return nil
}

func loadSnapshot(db *sql.DB) (storememory.Snapshot, error) {
	var body []byte
	err := db.QueryRow(`SELECT payload FROM morph_auth_state WHERE id = 1`).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return storememory.Snapshot{}, nil
	}
	if err != nil {
		return storememory.Snapshot{}, fmt.Errorf("load auth state: %w", err)
	}
	var snapshot storememory.Snapshot
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return storememory.Snapshot{}, fmt.Errorf("parse auth state: %w", err)
	}

	return snapshot, nil
}
