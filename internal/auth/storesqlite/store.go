package storesqlite

import (
	"context"
	"database/sql"
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
	mu                  sync.Mutex
	db                  *sql.DB
	memory              *storememory.Store
	persisted           storememory.Snapshot
	persistedAuditStart int
	stateRevision       uint64
	lastUsePersist      time.Time
	dirtySessions       map[string]struct{}
	dirtyTokens         map[string]struct{}
	auditIDs            map[string]struct{}
}

const recordUsePersistInterval = time.Second

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("auth database path is required")
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("auth database must not be a symbolic link")
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("auth database must be a regular file")
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect auth database: %w", err)
	}
	directory := filepath.Dir(path)
	if info, err := os.Lstat(directory); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("auth database directory must not be a symbolic link")
		}
		if !info.IsDir() {
			return nil, errors.New("auth database directory must be a directory")
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect auth database directory: %w", err)
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create auth database directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, fmt.Errorf("secure auth database directory: %w", err)
	}
	dsn := "file:" + filepath.ToSlash(path) + "?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=on"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open auth database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create auth database: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("secure auth database: %w", err)
	}
	if err := initializeSchema(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize auth database: %w", err)
	}
	snapshot, err := loadSnapshot(db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	stateRevision, err := loadStateRevision(db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	store := &Store{
		db: db, memory: storememory.NewFromSnapshot(snapshot),
		persisted: snapshot, stateRevision: stateRevision,
	}
	store.rebuildAuditIDs()

	return store, nil
}

func (s *Store) SeedRoot(
	ctx context.Context,
	authorization morphauth.Authorization,
) (morphauth.Authorization, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.memory.SeedRoot(ctx, authorization)
	if err != nil {
		return morphauth.Authorization{}, err
	}
	if err := s.persist(ctx); err != nil {
		s.restorePersisted()
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
	result, err := s.memory.PutAuthorization(ctx, authorization)
	if err != nil {
		return morphauth.Authorization{}, err
	}
	if err := s.persist(ctx); err != nil {
		s.restorePersisted()
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
	if err := s.memory.RotateIdentity(ctx, currentIdentityID, next, now); err != nil {
		return err
	}

	return s.persistOrRestore(ctx)
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
	if err := s.memory.Activate(ctx, session, token); err != nil {
		return err
	}

	return s.persistOrRestore(ctx)
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
	if err := s.memory.RecordUse(ctx, sessionID, tokenID, method, now, idleExpiresAt); err != nil {
		return err
	}
	s.markUsageDirty(sessionID, tokenID)
	if !shouldPersist {
		return nil
	}
	if err := s.persistDirtyUsage(ctx); err != nil {
		s.restorePersisted()
		return err
	}
	s.lastUsePersist = now

	return nil
}

func (s *Store) KeepAliveSession(
	ctx context.Context,
	sessionID string,
	now, idleExpiresAt time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	persistInterval := recordUsePersistInterval
	if leaseInterval := idleExpiresAt.Sub(now) / 3; leaseInterval < persistInterval {
		persistInterval = leaseInterval
	}
	shouldPersist := s.lastUsePersist.IsZero() ||
		now.Before(s.lastUsePersist) ||
		persistInterval <= 0 ||
		now.Sub(s.lastUsePersist) >= persistInterval
	if err := s.memory.KeepAliveSession(ctx, sessionID, now, idleExpiresAt); err != nil {
		return err
	}
	s.markUsageDirty(sessionID, "")
	if !shouldPersist {
		return nil
	}
	if err := s.persistDirtyUsage(ctx); err != nil {
		s.restorePersisted()
		return err
	}
	s.lastUsePersist = now

	return nil
}

func (s *Store) RevokeSession(
	ctx context.Context,
	id, reason string,
	now time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.memory.RevokeSession(ctx, id, reason, now); err != nil {
		return err
	}

	return s.persistOrRestore(ctx)
}

func (s *Store) RevokeToken(
	ctx context.Context,
	id, reason string,
	now time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.memory.RevokeToken(ctx, id, reason, now); err != nil {
		return err
	}

	return s.persistOrRestore(ctx)
}

func (s *Store) AppendAudit(ctx context.Context, event morphauth.AuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("auth database is closed")
	}
	if event.ID != "" {
		if _, exists := s.auditIDs[event.ID]; exists {
			return fmt.Errorf("duplicate audit event ID %q", event.ID)
		}
	}
	appended, evicted := s.memory.AppendAuditChange(event)
	before := storememory.Snapshot{}
	if evicted != nil {
		before.Audit = []morphauth.AuditEvent{*evicted}
	}
	current := storememory.Snapshot{
		Audit: []morphauth.AuditEvent{appended},
	}
	nextRevision, err := persistChanges(
		ctx,
		s.db,
		s.stateRevision,
		before,
		current,
	)
	if err != nil {
		s.restorePersisted()
		return fmt.Errorf("persist auth audit event: %w", err)
	}
	if evicted != nil {
		s.persisted.Audit[s.persistedAuditStart] = appended
		s.persistedAuditStart = (s.persistedAuditStart + 1) % len(s.persisted.Audit)
		delete(s.auditIDs, evicted.ID)
	} else {
		s.persisted.Audit = append(s.persisted.Audit, appended)
	}
	s.auditIDs[appended.ID] = struct{}{}
	s.stateRevision = nextRevision

	return nil
}

func (s *Store) ListAudit(ctx context.Context, limit int) ([]morphauth.AuditEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.memory.ListAudit(ctx, limit)
}

func (s *Store) Prune(
	ctx context.Context,
	options morphauth.PruneOptions,
) (morphauth.PruneResult, error) {
	return s.PruneBatches(ctx, options, 1)
}

func (s *Store) PruneBatches(
	ctx context.Context,
	options morphauth.PruneOptions,
	maximumBatches int,
) (morphauth.PruneResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if options.Limit <= 0 || maximumBatches <= 0 {
		return morphauth.PruneResult{}, nil
	}
	if options.DryRun {
		working := storememory.NewFromSnapshot(s.memory.Snapshot())
		options.DryRun = false
		return pruneMemoryBatches(ctx, working, options, maximumBatches)
	}
	before := s.memory.Snapshot()
	total, err := pruneMemoryBatches(ctx, s.memory, options, maximumBatches)
	if err != nil {
		s.memory = storememory.NewFromSnapshot(before)
		return morphauth.PruneResult{}, err
	}
	if total.Total() == 0 {
		return morphauth.PruneResult{}, nil
	}
	if err := s.persistOrRestore(ctx); err != nil {
		return morphauth.PruneResult{}, err
	}

	return total, nil
}

func pruneMemoryBatches(
	ctx context.Context,
	store *storememory.Store,
	options morphauth.PruneOptions,
	maximumBatches int,
) (morphauth.PruneResult, error) {
	total := morphauth.PruneResult{}
	for range maximumBatches {
		result, err := store.Prune(ctx, options)
		if err != nil {
			return morphauth.PruneResult{}, err
		}
		total.Tokens += result.Tokens
		total.Sessions += result.Sessions
		total.Authorizations += result.Authorizations
		total.AuditEvents += result.AuditEvents
		if result.Total() < options.Limit {
			break
		}
	}

	return total, nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	var persistErr error
	if len(s.dirtySessions) > 0 || len(s.dirtyTokens) > 0 {
		persistErr = s.persistDirtyUsage(context.Background())
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
	current := s.memory.Snapshot()
	nextRevision, err := persistChanges(
		ctx,
		s.db,
		s.stateRevision,
		s.getPersistedSnapshot(),
		current,
	)
	if err != nil {
		return fmt.Errorf("persist auth state: %w", err)
	}
	s.persisted = current
	s.persistedAuditStart = 0
	s.stateRevision = nextRevision
	s.clearDirtyUsage()
	s.rebuildAuditIDs()

	return nil
}

func (s *Store) persistOrRestore(
	ctx context.Context,
) error {
	if err := s.persist(ctx); err != nil {
		s.restorePersisted()
		return err
	}

	return nil
}

func (s *Store) persistDirtyUsage(ctx context.Context) error {
	if s.db == nil {
		return errors.New("auth database is closed")
	}
	before := storememory.Snapshot{
		Sessions: make(map[string]morphauth.Session, len(s.dirtySessions)),
		Tokens:   make(map[string]morphauth.Token, len(s.dirtyTokens)),
	}
	current := storememory.Snapshot{
		Sessions: make(map[string]morphauth.Session, len(s.dirtySessions)),
		Tokens:   make(map[string]morphauth.Token, len(s.dirtyTokens)),
	}
	for id := range s.dirtySessions {
		before.Sessions[id] = s.persisted.Sessions[id]
		session, err := s.memory.GetSession(ctx, id)
		if err != nil {
			return err
		}
		current.Sessions[id] = session
	}
	for id := range s.dirtyTokens {
		before.Tokens[id] = s.persisted.Tokens[id]
		token, err := s.memory.GetToken(ctx, id)
		if err != nil {
			return err
		}
		current.Tokens[id] = token
	}
	nextRevision, err := persistChanges(
		ctx,
		s.db,
		s.stateRevision,
		before,
		current,
	)
	if err != nil {
		return fmt.Errorf("persist auth usage: %w", err)
	}
	for id, session := range current.Sessions {
		s.persisted.Sessions[id] = session
	}
	for id, token := range current.Tokens {
		s.persisted.Tokens[id] = token
	}
	s.stateRevision = nextRevision
	s.clearDirtyUsage()

	return nil
}

func (s *Store) markUsageDirty(sessionID, tokenID string) {
	if s.dirtySessions == nil {
		s.dirtySessions = make(map[string]struct{})
	}
	s.dirtySessions[sessionID] = struct{}{}
	if tokenID == "" {
		return
	}
	if s.dirtyTokens == nil {
		s.dirtyTokens = make(map[string]struct{})
	}
	s.dirtyTokens[tokenID] = struct{}{}
}

func (s *Store) clearDirtyUsage() {
	clear(s.dirtySessions)
	clear(s.dirtyTokens)
}

func (s *Store) restorePersisted() {
	s.memory = storememory.NewFromSnapshot(s.getPersistedSnapshot())
	s.clearDirtyUsage()
}

func (s *Store) rebuildAuditIDs() {
	s.auditIDs = make(map[string]struct{}, len(s.persisted.Audit))
	for _, event := range s.persisted.Audit {
		s.auditIDs[event.ID] = struct{}{}
	}
}

func (s *Store) getPersistedSnapshot() storememory.Snapshot {
	snapshot := s.persisted
	snapshot.Audit = make([]morphauth.AuditEvent, len(s.persisted.Audit))
	for offset := range len(s.persisted.Audit) {
		index := (s.persistedAuditStart + offset) % len(s.persisted.Audit)
		snapshot.Audit[offset] = s.persisted.Audit[index]
	}

	return snapshot
}
