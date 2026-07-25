package storesqlite_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	morphauth "github.com/wandxy/morph/internal/auth"
	"github.com/wandxy/morph/internal/auth/storesqlite"
)

func TestStore_CreatesNormalizedSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.db")
	store, err := storesqlite.Open(path)
	require.NoError(t, err)
	require.NoError(t, store.Close())

	db, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	rows, err := db.Query(`
		SELECT name
		FROM sqlite_master
		WHERE type = 'table'
		ORDER BY name
	`)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, rows.Close())
	}()
	var tables []string
	for rows.Next() {
		var table string
		require.NoError(t, rows.Scan(&table))
		tables = append(tables, table)
	}
	require.NoError(t, rows.Err())
	require.ElementsMatch(t, []string{
		"audit_events",
		"auth_schema",
		"authorization_values",
		"authorizations",
		"session_roles",
		"sessions",
		"token_method_usage",
		"token_values",
		"tokens",
	}, tables)
}

func TestStore_SecuresDatabaseDirectoryAndFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file modes do not apply on Windows")
	}
	directory := filepath.Join(t.TempDir(), "data")
	require.NoError(t, os.Mkdir(directory, 0o755))
	path := filepath.Join(directory, "auth.db")
	store, err := storesqlite.Open(path)
	require.NoError(t, err)
	directoryInfo, err := os.Stat(directory)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), directoryInfo.Mode().Perm())
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(candidate)
		if os.IsNotExist(err) {
			continue
		}
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
	require.NoError(t, store.Close())
}

func TestStore_RejectsUnsafeDatabasePaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic-link behavior differs on Windows")
	}
	directory := t.TempDir()
	target := filepath.Join(directory, "target.db")
	require.NoError(t, os.WriteFile(target, nil, 0o600))
	symlink := filepath.Join(directory, "symlink.db")
	require.NoError(t, os.Symlink(target, symlink))
	_, err := storesqlite.Open(symlink)
	require.ErrorContains(t, err, "must not be a symbolic link")

	_, err = storesqlite.Open(directory)
	require.ErrorContains(t, err, "must be a regular file")

	dataTarget := filepath.Join(directory, "real-data")
	require.NoError(t, os.Mkdir(dataTarget, 0o700))
	dataSymlink := filepath.Join(directory, "data")
	require.NoError(t, os.Symlink(dataTarget, dataSymlink))
	_, err = storesqlite.Open(filepath.Join(dataSymlink, "auth.db"))
	require.ErrorContains(t, err, "directory must not be a symbolic link")
}

func TestStore_PersistsUsageAndAuditAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "data", "auth.db")
	store, err := storesqlite.Open(path)
	require.NoError(t, err)
	require.FileExists(t, path)
	service, identity := newSQLiteService(t, store)
	raw, claims, err := morphauth.SignAccessToken(identity, morphauth.TokenRequest{
		Audience: "morph-rpc:test", Subject: identity.ID,
		SessionID: "session", TokenID: "token", OwnerID: "owner", Source: "cli",
		Roles: []string{morphauth.RoleOwner}, Services: []string{morphauth.RootScope},
		TTL: time.Hour, NotBefore: time.Now().Add(-time.Second),
		AuthorizationRevision: 1,
	})
	require.NoError(t, err)
	_, err = service.OpenSession(ctx, raw, "cli")
	require.NoError(t, err)
	_, err = service.Authenticate(ctx, raw, "/morph.v1.SessionService/List", "")
	require.NoError(t, err)
	require.NoError(t, store.Close())

	store, err = storesqlite.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	token, err := store.GetToken(ctx, claims.ID)
	require.NoError(t, err)
	require.Equal(t, uint64(1), token.MethodUse["/morph.v1.SessionService/List"].Count)
	reopenedService, err := morphauth.NewService(morphauth.ServiceOptions{
		Audience: "morph-rpc:test", Store: store,
		SessionIdleTTL: time.Minute, SessionMaxTTL: 24 * time.Hour,
	})
	require.NoError(t, err)
	principal, err := reopenedService.Authenticate(
		ctx,
		raw,
		"/morph.v1.SessionService/List",
		"",
	)
	require.NoError(t, err)
	require.Equal(t, identity.ID, principal.IdentityID)
	events, err := store.ListAudit(ctx, 100)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(events), 3)
}

func TestStore_DoesNotPersistRawTokenOrPrivateKey(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "auth.db")
	store, err := storesqlite.Open(path)
	require.NoError(t, err)
	service, identity := newSQLiteService(t, store)
	raw, _, err := morphauth.SignAccessToken(identity, morphauth.TokenRequest{
		Audience: "morph-rpc:test", Subject: identity.ID,
		SessionID: "secret-session", TokenID: "secret-token",
		OwnerID: "owner", Source: "cli",
		Roles: []string{morphauth.RoleOwner}, Services: []string{morphauth.RootScope},
		TTL: time.Hour, NotBefore: time.Now().Add(-time.Second),
		AuthorizationRevision: 1,
	})
	require.NoError(t, err)
	_, err = service.OpenSession(ctx, raw, "cli")
	require.NoError(t, err)
	requireAuthFilesExcludeSecrets(t, path, raw, identity)
	require.NoError(t, store.Close())
	requireAuthFilesExcludeSecrets(t, path, raw, identity)
}

func TestStore_RoundTripsAllNormalizedRecordFields(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "auth.db")
	store, err := storesqlite.Open(path)
	require.NoError(t, err)
	_, rootIdentity := newSQLiteService(t, store)
	delegatedIdentity, err := morphauth.GenerateIdentity(7)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Microsecond)
	authorization := morphauth.Authorization{
		IdentityID: delegatedIdentity.ID,
		PublicKey:  delegatedIdentity.PublicKey,
		OwnerID:    rootIdentity.ID,
		UserID:     "operator",
		Roles:      []string{morphauth.RoleOperator, "auditor"},
		Services: []string{
			"/morph.v1.SessionService/*",
			"/morph.v1.ModelService/*",
		},
		Methods: []string{
			"/morph.v1.ModelService/ListModels",
			"/morph.v1.SessionService/List",
		},
		MaxTTL:     45 * time.Minute,
		Generation: delegatedIdentity.Generation,
		Revision:   3,
		Status:     morphauth.StatusActive,
		CreatedAt:  now,
		UpdatedAt:  now.Add(time.Second),
	}
	_, err = store.PutAuthorization(ctx, authorization)
	require.NoError(t, err)
	revokedIdentity, err := morphauth.GenerateIdentity(8)
	require.NoError(t, err)
	authorizationRevokedAt := now.Add(2 * time.Second)
	revokedAuthorization := morphauth.Authorization{
		IdentityID:     revokedIdentity.ID,
		PublicKey:      revokedIdentity.PublicKey,
		OwnerID:        rootIdentity.ID,
		UserID:         "former-operator",
		Roles:          []string{morphauth.RoleOperator, "auditor"},
		Services:       []string{"/morph.v1.SessionService/*"},
		Methods:        []string{"/morph.v1.SessionService/List"},
		MaxTTL:         15 * time.Minute,
		Generation:     revokedIdentity.Generation,
		Revision:       4,
		Status:         morphauth.StatusRevoked,
		CreatedAt:      now,
		UpdatedAt:      authorizationRevokedAt,
		RevokedAt:      &authorizationRevokedAt,
		RevocationNote: "access removed",
	}
	_, err = store.PutAuthorization(ctx, revokedAuthorization)
	require.NoError(t, err)
	session := morphauth.Session{
		ID: "round-trip-session", IdentityID: delegatedIdentity.ID,
		OwnerID: rootIdentity.ID, UserID: "operator",
		Roles: []string{morphauth.RoleOperator, "auditor"}, Source: "cli",
		Status: morphauth.StatusActive, CreatedAt: now,
		LastSeenAt: now, IdleExpiresAt: now.Add(time.Minute),
		AbsoluteExpiresAt:     now.Add(time.Hour),
		IdentityGeneration:    delegatedIdentity.Generation,
		AuthorizationRevision: authorization.Revision,
	}
	token := morphauth.Token{
		ID: "round-trip-token", SessionID: session.ID,
		IdentityID: delegatedIdentity.ID, OwnerID: rootIdentity.ID,
		UserID: "operator", Roles: []string{morphauth.RoleOperator, "auditor"},
		Services: []string{
			"/morph.v1.SessionService/*",
			"/morph.v1.ModelService/*",
		},
		Methods: []string{
			"/morph.v1.ModelService/ListModels",
			"/morph.v1.SessionService/List",
		},
		Nonce: "001122", IssuedAt: now, NotBefore: now.Add(-time.Second),
		ExpiresAt: now.Add(30 * time.Minute), Status: morphauth.StatusActive,
		IdentityGeneration:    delegatedIdentity.Generation,
		AuthorizationRevision: authorization.Revision,
		CertificateThumbprint: "certificate-thumbprint",
	}
	require.NoError(t, store.Activate(ctx, session, token))
	usedAt := now.Add(10 * time.Second)
	require.NoError(t, store.RecordUse(
		ctx,
		session.ID,
		token.ID,
		token.Methods[0],
		usedAt,
		usedAt.Add(time.Minute),
	))
	revokedAt := now.Add(20 * time.Second)
	require.NoError(t, store.RevokeToken(ctx, token.ID, "token test", revokedAt))
	require.NoError(t, store.RevokeSession(ctx, session.ID, "session test", revokedAt))
	require.NoError(t, store.AppendAudit(ctx, morphauth.AuditEvent{
		ID: "custom-audit", Type: "custom", IdentityID: delegatedIdentity.ID,
		SessionID: session.ID, TokenID: token.ID, Method: token.Methods[0],
		Reason: "round trip", CreatedAt: now.Add(30 * time.Second),
	}))
	require.NoError(t, store.Close())

	store, err = storesqlite.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	persistedAuthorization, err := store.GetAuthorization(ctx, delegatedIdentity.ID)
	require.NoError(t, err)
	require.Equal(t, authorization, persistedAuthorization)
	persistedRevokedAuthorization, err := store.GetAuthorization(ctx, revokedIdentity.ID)
	require.NoError(t, err)
	require.Equal(t, revokedAuthorization, persistedRevokedAuthorization)
	persistedSession, err := store.GetSession(ctx, session.ID)
	require.NoError(t, err)
	require.Equal(t, morphauth.StatusRevoked, persistedSession.Status)
	require.Equal(t, "session test", persistedSession.RevocationNote)
	require.Equal(t, &revokedAt, persistedSession.RevokedAt)
	persistedToken, err := store.GetToken(ctx, token.ID)
	require.NoError(t, err)
	require.Equal(t, morphauth.StatusRevoked, persistedToken.Status)
	require.Equal(t, "token test", persistedToken.RevocationNote)
	require.Equal(t, &revokedAt, persistedToken.RevokedAt)
	require.Equal(t, &usedAt, persistedToken.LastUsedAt)
	require.Equal(t, uint64(1), persistedToken.UseCount)
	require.Equal(t, uint64(1), persistedToken.MethodUse[token.Methods[0]].Count)
	require.Equal(t, token.Roles, persistedToken.Roles)
	require.Equal(t, token.Services, persistedToken.Services)
	require.Equal(t, token.Methods, persistedToken.Methods)
	require.Equal(t, token.Nonce, persistedToken.Nonce)
	require.Equal(t, token.CertificateThumbprint, persistedToken.CertificateThumbprint)
	events, err := store.ListAudit(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, "custom-audit", events[0].ID)
}

func TestStore_RotatesAndListsAuthorizationsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "auth.db")
	store, err := storesqlite.Open(path)
	require.NoError(t, err)
	service, current := newSQLiteService(t, store)
	invalid, err := morphauth.GenerateIdentity(current.Generation)
	require.NoError(t, err)
	err = store.RotateIdentity(ctx, current.ID, morphauth.Authorization{
		IdentityID: invalid.ID,
		PublicKey:  invalid.PublicKey,
		Generation: invalid.Generation,
		Status:     morphauth.StatusActive,
	}, time.Now().UTC())
	require.ErrorIs(t, err, morphauth.ErrPermissionDenied)

	next, err := morphauth.GenerateIdentity(current.Generation + 1)
	require.NoError(t, err)
	require.NoError(t, service.RotateRoot(ctx, current.ID, next, "owner"))
	authorizations, err := store.ListAuthorizations(ctx)
	require.NoError(t, err)
	require.Len(t, authorizations, 2)
	require.Less(t, authorizations[0].IdentityID, authorizations[1].IdentityID)
	currentAuthorization, err := store.GetAuthorization(ctx, current.ID)
	require.NoError(t, err)
	require.Equal(t, morphauth.StatusRevoked, currentAuthorization.Status)
	require.Equal(t, "identity rotated", currentAuthorization.RevocationNote)
	nextAuthorization, err := store.GetAuthorization(ctx, next.ID)
	require.NoError(t, err)
	require.Equal(t, morphauth.StatusActive, nextAuthorization.Status)
	require.NoError(t, store.Close())

	store, err = storesqlite.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	authorizations, err = store.ListAuthorizations(ctx)
	require.NoError(t, err)
	require.Len(t, authorizations, 2)
	currentAuthorization, err = store.GetAuthorization(ctx, current.ID)
	require.NoError(t, err)
	require.Equal(t, morphauth.StatusRevoked, currentAuthorization.Status)
	require.NotNil(t, currentAuthorization.RevokedAt)
	nextAuthorization, err = store.GetAuthorization(ctx, next.ID)
	require.NoError(t, err)
	require.Equal(t, morphauth.StatusActive, nextAuthorization.Status)
	require.Equal(t, next.PublicKey, nextAuthorization.PublicKey)
}

func TestStore_RollsBackNormalizedTransactionFailure(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "auth.db")
	store, err := storesqlite.Open(path)
	require.NoError(t, err)
	_, identity := newSQLiteService(t, store)
	db, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	_, err = db.Exec(`
		CREATE TRIGGER fail_token_insert
		BEFORE INSERT ON tokens
		BEGIN
			SELECT RAISE(FAIL, 'injected token failure');
		END
	`)
	require.NoError(t, err)
	now := time.Now().UTC()
	session := morphauth.Session{
		ID: "rollback-session", IdentityID: identity.ID,
		Status: morphauth.StatusActive, CreatedAt: now,
		AbsoluteExpiresAt: now.Add(time.Hour),
	}
	token := morphauth.Token{
		ID: "rollback-token", SessionID: session.ID, IdentityID: identity.ID,
		Status: morphauth.StatusActive, ExpiresAt: now.Add(time.Hour),
	}
	err = store.Activate(ctx, session, token)
	require.ErrorContains(t, err, "injected token failure")
	_, err = store.GetSession(ctx, session.ID)
	require.ErrorIs(t, err, morphauth.ErrNotFound)
	_, err = store.GetToken(ctx, token.ID)
	require.ErrorIs(t, err, morphauth.ErrNotFound)
	_, err = db.Exec(`DROP TRIGGER fail_token_insert`)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	require.NoError(t, store.Close())

	store, err = storesqlite.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	_, err = store.GetSession(ctx, session.ID)
	require.ErrorIs(t, err, morphauth.ErrNotFound)
	_, err = store.GetToken(ctx, token.ID)
	require.ErrorIs(t, err, morphauth.ErrNotFound)
	_, err = store.GetAuthorization(ctx, identity.ID)
	require.NoError(t, err)
}

func TestStore_RejectsUnsupportedSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.db")
	store, err := storesqlite.Open(path)
	require.NoError(t, err)
	require.NoError(t, store.Close())
	db, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE auth_schema SET version = 2`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	_, err = storesqlite.Open(path)
	require.ErrorContains(t, err, "unsupported auth database schema version 2")
}

func TestStore_RejectsStaleConcurrentWriter(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "auth.db")
	first, err := storesqlite.Open(path)
	require.NoError(t, err)
	_, identity := newSQLiteService(t, first)
	second, err := storesqlite.Open(path)
	require.NoError(t, err)
	now := time.Now().UTC()
	require.NoError(t, first.AppendAudit(ctx, morphauth.AuditEvent{
		ID: "first-writer", Type: "first", IdentityID: identity.ID, CreatedAt: now,
	}))
	err = second.AppendAudit(ctx, morphauth.AuditEvent{
		ID: "stale-writer", Type: "stale", IdentityID: identity.ID,
		CreatedAt: now.Add(time.Second),
	})
	require.ErrorContains(t, err, "auth database changed by another writer")
	require.NoError(t, second.Close())
	require.NoError(t, first.Close())

	reopened, err := storesqlite.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	events, err := reopened.ListAudit(ctx, 100)
	require.NoError(t, err)
	var ids []string
	for _, event := range events {
		ids = append(ids, event.ID)
	}
	require.Contains(t, ids, "first-writer")
	require.NotContains(t, ids, "stale-writer")
}

func TestStore_RejectsOrphanNormalizedRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.db")
	store, err := storesqlite.Open(path)
	require.NoError(t, err)
	require.NoError(t, store.Close())
	db, err := sql.Open("sqlite3", "file:"+path+"?_foreign_keys=off")
	require.NoError(t, err)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = db.Exec(`
		INSERT INTO token_method_usage (
			token_id, method, use_count, first_used_at, last_used_at
		) VALUES ('missing-token', '/morph.v1.SessionService/List', 1, ?, ?)
	`, now, now)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	_, err = storesqlite.Open(path)
	require.ErrorContains(t, err, `method usage references unknown token "missing-token"`)
}

func TestStore_RejectsCorruptNormalizedTimestamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.db")
	store, err := storesqlite.Open(path)
	require.NoError(t, err)
	require.NoError(t, store.Close())
	db, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	_, err = db.Exec(`
		INSERT INTO audit_events (
			sequence, id, type, identity_id, session_id,
			token_id, method, reason, created_at
		) VALUES (1, 'bad-time', 'test', '', '', '', '', '', 'not-a-time')
	`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	_, err = storesqlite.Open(path)
	require.ErrorContains(t, err, `parse auth timestamp "not-a-time"`)
}

func TestStore_AuditRolloverKeepsStableSequences(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "auth.db")
	store, err := storesqlite.Open(path)
	require.NoError(t, err)
	require.NoError(t, store.Close())
	db, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	tx, err := db.Begin()
	require.NoError(t, err)
	statement, err := tx.Prepare(`
		INSERT INTO audit_events (
			sequence, id, type, identity_id, session_id,
			token_id, method, reason, created_at
		) VALUES (?, ?, 'test', '', '', '', '', '', ?)
	`)
	require.NoError(t, err)
	now := time.Now().UTC()
	for index := 1; index <= 10000; index++ {
		_, err = statement.Exec(index, "audit-"+strconv.Itoa(index), now.Format(time.RFC3339Nano))
		require.NoError(t, err)
	}
	require.NoError(t, statement.Close())
	require.NoError(t, tx.Commit())
	require.NoError(t, db.Close())

	store, err = storesqlite.Open(path)
	require.NoError(t, err)
	require.NoError(t, store.AppendAudit(ctx, morphauth.AuditEvent{
		ID: "audit-new", Type: "test", CreatedAt: now.Add(time.Second),
	}))
	require.NoError(t, store.Close())

	db, err = sql.Open("sqlite3", path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	var count, minimum, maximum int
	err = db.QueryRow(`
		SELECT COUNT(*), MIN(sequence), MAX(sequence)
		FROM audit_events
	`).Scan(&count, &minimum, &maximum)
	require.NoError(t, err)
	require.Equal(t, 10000, count)
	require.Equal(t, 2, minimum)
	require.Equal(t, 10001, maximum)
}

func TestStore_RejectsDuplicateAuditEventID(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "auth.db")
	store, err := storesqlite.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	now := time.Now().UTC()
	require.NoError(t, store.AppendAudit(ctx, morphauth.AuditEvent{
		ID: "duplicate", Type: "first", CreatedAt: now,
	}))
	err = store.AppendAudit(ctx, morphauth.AuditEvent{
		ID: "duplicate", Type: "second", CreatedAt: now.Add(time.Second),
	})
	require.ErrorContains(t, err, `duplicate audit event ID "duplicate"`)
	events, err := store.ListAudit(ctx, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "first", events[0].Type)
}

func TestStore_CloseReportsFailedDeferredUsageFlush(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "auth.db")
	store, err := storesqlite.Open(path)
	require.NoError(t, err)
	service, identity := newSQLiteService(t, store)
	raw, claims, err := morphauth.SignAccessToken(identity, morphauth.TokenRequest{
		Audience: "morph-rpc:test", Subject: identity.ID,
		SessionID: "close-session", TokenID: "close-token",
		OwnerID: "owner", Source: "cli",
		Roles: []string{morphauth.RoleOwner}, Services: []string{morphauth.RootScope},
		TTL: time.Hour, NotBefore: time.Now().Add(-time.Second),
		AuthorizationRevision: 1,
	})
	require.NoError(t, err)
	_, err = service.OpenSession(ctx, raw, "cli")
	require.NoError(t, err)

	firstUseAt := time.Now().UTC()
	require.NoError(t, store.RecordUse(
		ctx,
		claims.SessionID,
		claims.ID,
		"/morph.v1.SessionService/List",
		firstUseAt,
		firstUseAt.Add(time.Minute),
	))
	concurrent, err := storesqlite.Open(path)
	require.NoError(t, err)
	require.NoError(t, store.RecordUse(
		ctx,
		claims.SessionID,
		claims.ID,
		"/morph.v1.SessionService/List",
		firstUseAt.Add(100*time.Millisecond),
		firstUseAt.Add(time.Minute),
	))
	require.NoError(t, concurrent.AppendAudit(ctx, morphauth.AuditEvent{
		ID: "concurrent-write", Type: "test", CreatedAt: firstUseAt.Add(time.Second),
	}))
	require.NoError(t, concurrent.Close())

	err = store.Close()
	require.ErrorContains(t, err, "auth database changed by another writer")
}

func TestStore_PersistsStreamKeepAliveBeforeClose(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "auth.db")
	store, err := storesqlite.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	service, identity := newSQLiteService(t, store)
	raw, claims, err := morphauth.SignAccessToken(identity, morphauth.TokenRequest{
		Audience: "morph-rpc:test", Subject: identity.ID,
		SessionID: "stream-session", TokenID: "stream-token",
		OwnerID: "owner", Source: "cli",
		Roles: []string{morphauth.RoleOwner}, Services: []string{morphauth.RootScope},
		TTL: time.Hour, NotBefore: time.Now().Add(-time.Second),
		AuthorizationRevision: 1,
	})
	require.NoError(t, err)
	_, err = service.OpenSession(ctx, raw, "cli")
	require.NoError(t, err)
	session, err := store.GetSession(ctx, claims.SessionID)
	require.NoError(t, err)
	keepAliveAt := session.LastSeenAt.Add(30 * time.Second)
	idleExpiresAt := keepAliveAt.Add(15 * time.Minute)
	require.NoError(t, store.KeepAliveSession(
		ctx,
		claims.SessionID,
		keepAliveAt,
		idleExpiresAt,
	))

	reopened, err := storesqlite.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	persisted, err := reopened.GetSession(ctx, claims.SessionID)
	require.NoError(t, err)
	require.Equal(t, keepAliveAt, persisted.LastSeenAt)
	require.Equal(t, idleExpiresAt, persisted.IdleExpiresAt)
}

func TestStore_PersistsKeepAliveBeforeShortDurableLeaseExpires(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "auth.db")
	store, err := storesqlite.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	service, err := morphauth.NewService(morphauth.ServiceOptions{
		Audience: "morph-rpc:test", Store: store,
		SessionIdleTTL: 500 * time.Millisecond, SessionMaxTTL: 24 * time.Hour,
	})
	require.NoError(t, err)
	identity, err := morphauth.GenerateIdentity(1)
	require.NoError(t, err)
	_, err = service.SeedRoot(context.Background(), identity, "owner")
	require.NoError(t, err)
	raw, claims, err := morphauth.SignAccessToken(identity, morphauth.TokenRequest{
		Audience: "morph-rpc:test", Subject: identity.ID,
		SessionID: "short-session", TokenID: "short-token",
		OwnerID: "owner", Source: "cli",
		Roles: []string{morphauth.RoleOwner}, Services: []string{morphauth.RootScope},
		TTL: time.Hour, NotBefore: time.Now().Add(-time.Second),
		AuthorizationRevision: 1,
	})
	require.NoError(t, err)
	_, err = service.OpenSession(ctx, raw, "cli")
	require.NoError(t, err)
	_, err = service.Authenticate(ctx, raw, "/morph.v1.SessionService/List", "")
	require.NoError(t, err)
	session, err := store.GetSession(ctx, claims.SessionID)
	require.NoError(t, err)
	keepAliveAt := session.LastSeenAt.Add(200 * time.Millisecond)
	idleExpiresAt := keepAliveAt.Add(500 * time.Millisecond)
	require.NoError(t, store.KeepAliveSession(
		ctx,
		claims.SessionID,
		keepAliveAt,
		idleExpiresAt,
	))

	reopened, err := storesqlite.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	persisted, err := reopened.GetSession(ctx, claims.SessionID)
	require.NoError(t, err)
	require.Equal(t, keepAliveAt, persisted.LastSeenAt)
	require.Equal(t, idleExpiresAt, persisted.IdleExpiresAt)
}

func TestStore_DefersKeepAliveWithinDurableLeaseMargin(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "auth.db")
	store, err := storesqlite.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	service, identity := newSQLiteService(t, store)
	raw, claims, err := morphauth.SignAccessToken(identity, morphauth.TokenRequest{
		Audience: "morph-rpc:test", Subject: identity.ID,
		SessionID: "deferred-session", TokenID: "deferred-token",
		OwnerID: "owner", Source: "cli",
		Roles: []string{morphauth.RoleOwner}, Services: []string{morphauth.RootScope},
		TTL: time.Hour, NotBefore: time.Now().Add(-time.Second),
		AuthorizationRevision: 1,
	})
	require.NoError(t, err)
	_, err = service.OpenSession(ctx, raw, "cli")
	require.NoError(t, err)
	_, err = service.Authenticate(ctx, raw, "/morph.v1.SessionService/List", "")
	require.NoError(t, err)
	session, err := store.GetSession(ctx, claims.SessionID)
	require.NoError(t, err)
	keepAliveAt := session.LastSeenAt.Add(100 * time.Millisecond)
	require.NoError(t, store.KeepAliveSession(
		ctx,
		claims.SessionID,
		keepAliveAt,
		keepAliveAt.Add(time.Minute),
	))

	reopened, err := storesqlite.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	persisted, err := reopened.GetSession(ctx, claims.SessionID)
	require.NoError(t, err)
	require.NotEqual(t, keepAliveAt, persisted.LastSeenAt)
}

func TestStore_RestoresKeepAliveAfterPersistenceFailure(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "auth.db")
	store, err := storesqlite.Open(path)
	require.NoError(t, err)
	service, identity := newSQLiteService(t, store)
	raw, claims, err := morphauth.SignAccessToken(identity, morphauth.TokenRequest{
		Audience: "morph-rpc:test", Subject: identity.ID,
		SessionID: "rollback-session", TokenID: "rollback-token",
		OwnerID: "owner", Source: "cli",
		Roles: []string{morphauth.RoleOwner}, Services: []string{morphauth.RootScope},
		TTL: time.Hour, NotBefore: time.Now().Add(-time.Second),
		AuthorizationRevision: 1,
	})
	require.NoError(t, err)
	_, err = service.OpenSession(ctx, raw, "cli")
	require.NoError(t, err)
	before, err := store.GetSession(ctx, claims.SessionID)
	require.NoError(t, err)
	require.NoError(t, store.Close())

	err = store.KeepAliveSession(
		ctx,
		claims.SessionID,
		before.LastSeenAt.Add(time.Second),
		before.IdleExpiresAt.Add(time.Second),
	)
	require.ErrorContains(t, err, "auth database is closed")
	restored, err := store.GetSession(ctx, claims.SessionID)
	require.NoError(t, err)
	require.Equal(t, before.LastSeenAt, restored.LastSeenAt)
	require.Equal(t, before.IdleExpiresAt, restored.IdleExpiresAt)
}

func TestStore_PrunesMultipleBatchesInOneOperation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "auth.db")
	store, err := storesqlite.Open(path)
	require.NoError(t, err)
	before := time.Now().UTC()
	expired := before.Add(-time.Hour)
	for index := range 6 {
		sessionID := "session-" + strconv.Itoa(index)
		tokenID := "token-" + strconv.Itoa(index)
		require.NoError(t, store.Activate(
			ctx,
			morphauth.Session{
				ID: sessionID, IdentityID: "identity", Status: morphauth.StatusActive,
				AbsoluteExpiresAt: expired,
			},
			morphauth.Token{
				ID: tokenID, SessionID: sessionID, IdentityID: "identity",
				Status:    morphauth.StatusActive,
				ExpiresAt: expired,
			},
		))
	}

	pruned, err := store.PruneBatches(ctx, before, 2, 3)
	require.NoError(t, err)
	require.Equal(t, 6, pruned)
	require.NoError(t, store.Close())

	reopened, err := storesqlite.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	sessions, err := reopened.ListSessions(ctx)
	require.NoError(t, err)
	tokens, err := reopened.ListTokens(ctx)
	require.NoError(t, err)
	require.Len(t, sessions, 3)
	require.Len(t, tokens, 3)
}

func TestStore_PruneBatchesHandlesBoundsCancellationAndEmptyState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.db")
	store, err := storesqlite.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	cutoff := time.Now().UTC()

	pruned, err := store.Prune(context.Background(), cutoff, 10)
	require.NoError(t, err)
	require.Zero(t, pruned)
	pruned, err = store.PruneBatches(context.Background(), cutoff, 0, 1)
	require.NoError(t, err)
	require.Zero(t, pruned)
	pruned, err = store.PruneBatches(context.Background(), cutoff, 1, 0)
	require.NoError(t, err)
	require.Zero(t, pruned)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	pruned, err = store.PruneBatches(cancelled, cutoff, 1, 1)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, pruned)
}

func TestStore_CancelledPrunePreservesDeferredUsage(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "auth.db")
	store, err := storesqlite.Open(path)
	require.NoError(t, err)
	service, identity := newSQLiteService(t, store)
	raw, claims, err := morphauth.SignAccessToken(identity, morphauth.TokenRequest{
		Audience: "morph-rpc:test", Subject: identity.ID,
		SessionID: "prune-session", TokenID: "prune-token",
		OwnerID: "owner", Source: "cli",
		Roles: []string{morphauth.RoleOwner}, Services: []string{morphauth.RootScope},
		TTL: time.Hour, NotBefore: time.Now().Add(-time.Second),
		AuthorizationRevision: 1,
	})
	require.NoError(t, err)
	_, err = service.OpenSession(ctx, raw, "cli")
	require.NoError(t, err)
	firstUseAt := time.Now().UTC()
	require.NoError(t, store.RecordUse(
		ctx,
		claims.SessionID,
		claims.ID,
		"/morph.v1.SessionService/List",
		firstUseAt,
		firstUseAt.Add(time.Minute),
	))
	deferredUseAt := firstUseAt.Add(100 * time.Millisecond)
	require.NoError(t, store.RecordUse(
		ctx,
		claims.SessionID,
		claims.ID,
		"/morph.v1.SessionService/List",
		deferredUseAt,
		deferredUseAt.Add(time.Minute),
	))
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = store.PruneBatches(cancelled, time.Now().UTC(), 10, 1)
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, store.Close())

	store, err = storesqlite.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	token, err := store.GetToken(ctx, claims.ID)
	require.NoError(t, err)
	require.Equal(t, uint64(2), token.UseCount)
	require.Equal(t, deferredUseAt, *token.LastUsedAt)
}

func TestStore_CancelledLaterPruneBatchRestoresEarlierRemovals(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "auth.db")
	store, err := storesqlite.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	cutoff := time.Now().UTC()
	for index := range 2 {
		sessionID := "cancel-session-" + strconv.Itoa(index)
		tokenID := "cancel-token-" + strconv.Itoa(index)
		require.NoError(t, store.Activate(
			ctx,
			morphauth.Session{
				ID: sessionID, IdentityID: "identity", Status: morphauth.StatusActive,
				AbsoluteExpiresAt: cutoff.Add(-time.Hour),
			},
			morphauth.Token{
				ID: tokenID, SessionID: sessionID, IdentityID: "identity",
				Status: morphauth.StatusActive, ExpiresAt: cutoff.Add(-time.Hour),
			},
		))
	}
	cancelled := &cancelAfterFirstCheckContext{Context: ctx}
	_, err = store.PruneBatches(cancelled, cutoff, 1, 2)
	require.ErrorIs(t, err, context.Canceled)
	sessions, err := store.ListSessions(ctx)
	require.NoError(t, err)
	tokens, err := store.ListTokens(ctx)
	require.NoError(t, err)
	require.Len(t, sessions, 2)
	require.Len(t, tokens, 2)
}

type cancelAfterFirstCheckContext struct {
	context.Context
	checks int
}

func (c *cancelAfterFirstCheckContext) Err() error {
	c.checks++
	if c.checks > 1 {
		return context.Canceled
	}

	return nil
}

func newSQLiteService(
	t *testing.T,
	store morphauth.Store,
) (*morphauth.Service, morphauth.Identity) {
	t.Helper()
	service, err := morphauth.NewService(morphauth.ServiceOptions{
		Audience: "morph-rpc:test", Store: store,
		SessionIdleTTL: time.Minute, SessionMaxTTL: 24 * time.Hour,
	})
	require.NoError(t, err)
	identity, err := morphauth.GenerateIdentity(1)
	require.NoError(t, err)
	_, err = service.SeedRoot(context.Background(), identity, "owner")
	require.NoError(t, err)

	return service, identity
}

func requireAuthFilesExcludeSecrets(
	t *testing.T,
	path, rawToken string,
	identity morphauth.Identity,
) {
	t.Helper()
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		body, err := os.ReadFile(candidate)
		if os.IsNotExist(err) {
			continue
		}
		require.NoError(t, err)
		require.NotContains(t, string(body), rawToken)
		require.NotContains(t, string(body), hex.EncodeToString(identity.PrivateKey))
		require.NotContains(t, string(body), hex.EncodeToString(identity.PrivateKey.Seed()))
		require.False(t, bytes.Contains(body, identity.PrivateKey))
		require.False(t, bytes.Contains(body, identity.PrivateKey.Seed()))
	}
}
