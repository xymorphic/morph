package storesqlite

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	morphauth "github.com/xymorphic/morph/internal/auth"
	"github.com/xymorphic/morph/internal/auth/storememory"
)

const authSchemaVersion = 1

func initializeSchema(db *sql.DB) error {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS auth_schema (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			version INTEGER NOT NULL,
			state_revision INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS authorizations (
			identity_id TEXT PRIMARY KEY,
			public_key BLOB NOT NULL,
			owner_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			max_ttl_ns INTEGER NOT NULL,
			generation INTEGER NOT NULL,
			revision INTEGER NOT NULL,
			status TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			revoked_at TEXT,
			revocation_note TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS authorization_values (
			identity_id TEXT NOT NULL,
			kind TEXT NOT NULL CHECK (kind IN ('role', 'service', 'method')),
			position INTEGER NOT NULL,
			value TEXT NOT NULL,
			FOREIGN KEY (identity_id) REFERENCES authorizations (identity_id) ON DELETE CASCADE,
			PRIMARY KEY (identity_id, kind, position)
		);
		CREATE INDEX IF NOT EXISTS authorization_values_identity
			ON authorization_values (identity_id, kind);
		CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			identity_id TEXT NOT NULL,
			owner_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			source TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at TEXT NOT NULL,
			last_seen_at TEXT NOT NULL,
			idle_expires_at TEXT NOT NULL,
			absolute_expires_at TEXT NOT NULL,
			identity_generation INTEGER NOT NULL,
			authorization_revision INTEGER NOT NULL,
			revoked_at TEXT,
			revocation_note TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS sessions_identity ON sessions (identity_id);
		CREATE INDEX IF NOT EXISTS sessions_status ON sessions (status);
		CREATE TABLE IF NOT EXISTS session_roles (
			session_id TEXT NOT NULL,
			position INTEGER NOT NULL,
			role TEXT NOT NULL,
			FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE,
			PRIMARY KEY (session_id, position)
		);
		CREATE TABLE IF NOT EXISTS tokens (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			identity_id TEXT NOT NULL,
			owner_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			nonce TEXT NOT NULL,
			issued_at TEXT NOT NULL,
			not_before TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			last_used_at TEXT,
			use_count INTEGER NOT NULL,
			status TEXT NOT NULL,
			identity_generation INTEGER NOT NULL,
			authorization_revision INTEGER NOT NULL,
			certificate_thumbprint TEXT NOT NULL,
			revoked_at TEXT,
			revocation_note TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS tokens_session ON tokens (session_id);
		CREATE INDEX IF NOT EXISTS tokens_identity ON tokens (identity_id);
		CREATE INDEX IF NOT EXISTS tokens_status ON tokens (status);
		CREATE TABLE IF NOT EXISTS token_values (
			token_id TEXT NOT NULL,
			kind TEXT NOT NULL CHECK (kind IN ('role', 'service', 'method')),
			position INTEGER NOT NULL,
			value TEXT NOT NULL,
			FOREIGN KEY (token_id) REFERENCES tokens (id) ON DELETE CASCADE,
			PRIMARY KEY (token_id, kind, position)
		);
		CREATE INDEX IF NOT EXISTS token_values_token ON token_values (token_id, kind);
		CREATE TABLE IF NOT EXISTS token_method_usage (
			token_id TEXT NOT NULL,
			method TEXT NOT NULL,
			use_count INTEGER NOT NULL,
			first_used_at TEXT NOT NULL,
			last_used_at TEXT NOT NULL,
			FOREIGN KEY (token_id) REFERENCES tokens (id) ON DELETE CASCADE,
			PRIMARY KEY (token_id, method)
		);
		CREATE TABLE IF NOT EXISTS audit_events (
			sequence INTEGER PRIMARY KEY,
			id TEXT NOT NULL UNIQUE,
			type TEXT NOT NULL,
			identity_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			token_id TEXT NOT NULL,
			method TEXT NOT NULL,
			reason TEXT NOT NULL,
			created_at TEXT NOT NULL
		);
	`); err != nil {
		return err
	}
	var version int
	err := db.QueryRow(`SELECT version FROM auth_schema WHERE id = 1`).Scan(&version)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = db.Exec(
			`INSERT INTO auth_schema (id, version, state_revision) VALUES (1, ?, 0)`,
			authSchemaVersion,
		)
		return err
	case err != nil:
		return err
	case version != authSchemaVersion:
		return fmt.Errorf("unsupported auth database schema version %d", version)
	default:
		return nil
	}
}

func loadStateRevision(db *sql.DB) (uint64, error) {
	var revision uint64
	if err := db.QueryRow(`
		SELECT state_revision
		FROM auth_schema
		WHERE id = 1 AND version = ?
	`, authSchemaVersion).Scan(&revision); err != nil {
		return 0, fmt.Errorf("load auth database state revision: %w", err)
	}

	return revision, nil
}

func persistChanges(
	ctx context.Context,
	db *sql.DB,
	expectedRevision uint64,
	before, current storememory.Snapshot,
) (uint64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return expectedRevision, err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	result, err := tx.ExecContext(ctx, `
		UPDATE auth_schema
		SET state_revision = state_revision + 1
		WHERE id = 1 AND version = ? AND state_revision = ?
	`, authSchemaVersion, expectedRevision)
	if err != nil {
		return expectedRevision, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return expectedRevision, err
	}
	if updated != 1 {
		return expectedRevision, errors.New(
			"auth database changed by another writer; reopen it and retry",
		)
	}
	if err := persistAuthorizations(
		ctx,
		tx,
		before.Authorizations,
		current.Authorizations,
	); err != nil {
		return expectedRevision, err
	}
	if err := persistSessions(ctx, tx, before.Sessions, current.Sessions); err != nil {
		return expectedRevision, err
	}
	if err := persistTokens(ctx, tx, before.Tokens, current.Tokens); err != nil {
		return expectedRevision, err
	}
	if err := persistAudit(ctx, tx, before.Audit, current.Audit); err != nil {
		return expectedRevision, err
	}
	if err := tx.Commit(); err != nil {
		return expectedRevision, err
	}

	return expectedRevision + 1, nil
}

func persistAuthorizations(
	ctx context.Context,
	tx *sql.Tx,
	before, current map[string]morphauth.Authorization,
) error {
	for _, identityID := range sortedKeys(before) {
		if _, exists := current[identityID]; exists {
			continue
		}
		if _, err := tx.ExecContext(
			ctx,
			`DELETE FROM authorizations WHERE identity_id = ?`,
			identityID,
		); err != nil {
			return err
		}
	}
	for _, identityID := range sortedKeys(current) {
		authorization := current[identityID]
		if previous, exists := before[identityID]; exists &&
			reflect.DeepEqual(previous, authorization) {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO authorizations (
				identity_id, public_key, owner_id, user_id, max_ttl_ns,
				generation, revision, status, created_at, updated_at,
				revoked_at, revocation_note
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (identity_id) DO UPDATE SET
				public_key = excluded.public_key,
				owner_id = excluded.owner_id,
				user_id = excluded.user_id,
				max_ttl_ns = excluded.max_ttl_ns,
				generation = excluded.generation,
				revision = excluded.revision,
				status = excluded.status,
				created_at = excluded.created_at,
				updated_at = excluded.updated_at,
				revoked_at = excluded.revoked_at,
				revocation_note = excluded.revocation_note
		`,
			authorization.IdentityID,
			[]byte(authorization.PublicKey),
			authorization.OwnerID,
			authorization.UserID,
			int64(authorization.MaxTTL),
			authorization.Generation,
			authorization.Revision,
			authorization.Status,
			formatTime(authorization.CreatedAt),
			formatTime(authorization.UpdatedAt),
			formatOptionalTime(authorization.RevokedAt),
			authorization.RevocationNote,
		); err != nil {
			return err
		}
		if _, err := tx.ExecContext(
			ctx,
			`DELETE FROM authorization_values WHERE identity_id = ?`,
			authorization.IdentityID,
		); err != nil {
			return err
		}
		if err := persistValues(
			ctx,
			tx,
			"authorization_values",
			"identity_id",
			authorization.IdentityID,
			authorization.Roles,
			authorization.Services,
			authorization.Methods,
		); err != nil {
			return err
		}
	}

	return nil
}

func persistSessions(
	ctx context.Context,
	tx *sql.Tx,
	before, current map[string]morphauth.Session,
) error {
	for _, id := range sortedKeys(before) {
		if _, exists := current[id]; exists {
			continue
		}
		if _, err := tx.ExecContext(
			ctx,
			`DELETE FROM sessions WHERE id = ?`,
			id,
		); err != nil {
			return err
		}
	}
	for _, id := range sortedKeys(current) {
		session := current[id]
		if previous, exists := before[id]; exists &&
			reflect.DeepEqual(previous, session) {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO sessions (
				id, identity_id, owner_id, user_id, source, status,
				created_at, last_seen_at, idle_expires_at, absolute_expires_at,
				identity_generation, authorization_revision, revoked_at, revocation_note
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET
				identity_id = excluded.identity_id,
				owner_id = excluded.owner_id,
				user_id = excluded.user_id,
				source = excluded.source,
				status = excluded.status,
				created_at = excluded.created_at,
				last_seen_at = excluded.last_seen_at,
				idle_expires_at = excluded.idle_expires_at,
				absolute_expires_at = excluded.absolute_expires_at,
				identity_generation = excluded.identity_generation,
				authorization_revision = excluded.authorization_revision,
				revoked_at = excluded.revoked_at,
				revocation_note = excluded.revocation_note
		`,
			session.ID,
			session.IdentityID,
			session.OwnerID,
			session.UserID,
			session.Source,
			session.Status,
			formatTime(session.CreatedAt),
			formatTime(session.LastSeenAt),
			formatTime(session.IdleExpiresAt),
			formatTime(session.AbsoluteExpiresAt),
			session.IdentityGeneration,
			session.AuthorizationRevision,
			formatOptionalTime(session.RevokedAt),
			session.RevocationNote,
		); err != nil {
			return err
		}
		if _, err := tx.ExecContext(
			ctx,
			`DELETE FROM session_roles WHERE session_id = ?`,
			session.ID,
		); err != nil {
			return err
		}
		for position, role := range session.Roles {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO session_roles (session_id, position, role)
				VALUES (?, ?, ?)
			`, session.ID, position, role); err != nil {
				return err
			}
		}
	}

	return nil
}

func persistTokens(
	ctx context.Context,
	tx *sql.Tx,
	before, current map[string]morphauth.Token,
) error {
	for _, id := range sortedKeys(before) {
		if _, exists := current[id]; exists {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM tokens WHERE id = ?`, id); err != nil {
			return err
		}
	}
	for _, id := range sortedKeys(current) {
		token := current[id]
		if previous, exists := before[id]; exists &&
			reflect.DeepEqual(previous, token) {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO tokens (
				id, session_id, identity_id, owner_id, user_id, nonce,
				issued_at, not_before, expires_at, last_used_at, use_count,
				status, identity_generation, authorization_revision,
				certificate_thumbprint, revoked_at, revocation_note
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET
				session_id = excluded.session_id,
				identity_id = excluded.identity_id,
				owner_id = excluded.owner_id,
				user_id = excluded.user_id,
				nonce = excluded.nonce,
				issued_at = excluded.issued_at,
				not_before = excluded.not_before,
				expires_at = excluded.expires_at,
				last_used_at = excluded.last_used_at,
				use_count = excluded.use_count,
				status = excluded.status,
				identity_generation = excluded.identity_generation,
				authorization_revision = excluded.authorization_revision,
				certificate_thumbprint = excluded.certificate_thumbprint,
				revoked_at = excluded.revoked_at,
				revocation_note = excluded.revocation_note
		`,
			token.ID,
			token.SessionID,
			token.IdentityID,
			token.OwnerID,
			token.UserID,
			token.Nonce,
			formatTime(token.IssuedAt),
			formatTime(token.NotBefore),
			formatTime(token.ExpiresAt),
			formatOptionalTime(token.LastUsedAt),
			token.UseCount,
			token.Status,
			token.IdentityGeneration,
			token.AuthorizationRevision,
			token.CertificateThumbprint,
			formatOptionalTime(token.RevokedAt),
			token.RevocationNote,
		); err != nil {
			return err
		}
		for _, table := range []string{"token_values", "token_method_usage"} {
			if _, err := tx.ExecContext(
				ctx,
				"DELETE FROM "+table+" WHERE token_id = ?",
				token.ID,
			); err != nil {
				return err
			}
		}
		if err := persistValues(
			ctx,
			tx,
			"token_values",
			"token_id",
			token.ID,
			token.Roles,
			token.Services,
			token.Methods,
		); err != nil {
			return err
		}
		for _, method := range sortedKeys(token.MethodUse) {
			usage := token.MethodUse[method]
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO token_method_usage (
					token_id, method, use_count, first_used_at, last_used_at
				) VALUES (?, ?, ?, ?, ?)
			`,
				token.ID,
				method,
				usage.Count,
				formatTime(usage.FirstUsedAt),
				formatTime(usage.LastUsedAt),
			); err != nil {
				return err
			}
		}
	}

	return nil
}

func persistValues(
	ctx context.Context,
	tx *sql.Tx,
	table, idColumn, id string,
	roles, services, methods []string,
) error {
	for kind, values := range map[string][]string{
		"role": roles, "service": services, "method": methods,
	} {
		for position, value := range values {
			query := fmt.Sprintf(
				"INSERT INTO %s (%s, kind, position, value) VALUES (?, ?, ?, ?)",
				table,
				idColumn,
			)
			if _, err := tx.ExecContext(ctx, query, id, kind, position, value); err != nil {
				return err
			}
		}
	}

	return nil
}

func persistAudit(
	ctx context.Context,
	tx *sql.Tx,
	before, current []morphauth.AuditEvent,
) error {
	currentByID := make(map[string]morphauth.AuditEvent, len(current))
	for _, event := range current {
		if _, exists := currentByID[event.ID]; exists {
			return fmt.Errorf("duplicate audit event ID %q", event.ID)
		}
		currentByID[event.ID] = event
	}
	beforeByID := make(map[string]morphauth.AuditEvent, len(before))
	for _, event := range before {
		beforeByID[event.ID] = event
		if _, exists := currentByID[event.ID]; exists {
			continue
		}
		if _, err := tx.ExecContext(
			ctx,
			`DELETE FROM audit_events WHERE id = ?`,
			event.ID,
		); err != nil {
			return err
		}
	}
	var nextSequence int64
	if err := tx.QueryRowContext(
		ctx,
		`SELECT COALESCE(MAX(sequence), 0) FROM audit_events`,
	).Scan(&nextSequence); err != nil {
		return err
	}
	for _, event := range current {
		previous, exists := beforeByID[event.ID]
		if exists && reflect.DeepEqual(previous, event) {
			continue
		}
		if exists {
			if _, err := tx.ExecContext(ctx, `
				UPDATE audit_events
				SET type = ?, identity_id = ?, session_id = ?, token_id = ?,
					method = ?, reason = ?, created_at = ?
				WHERE id = ?
			`,
				event.Type,
				event.IdentityID,
				event.SessionID,
				event.TokenID,
				event.Method,
				event.Reason,
				formatTime(event.CreatedAt),
				event.ID,
			); err != nil {
				return err
			}
			continue
		}
		nextSequence++
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO audit_events (
				sequence, id, type, identity_id, session_id,
				token_id, method, reason, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			nextSequence,
			event.ID,
			event.Type,
			event.IdentityID,
			event.SessionID,
			event.TokenID,
			event.Method,
			event.Reason,
			formatTime(event.CreatedAt),
		); err != nil {
			return err
		}
	}

	return nil
}

func loadSnapshot(db *sql.DB) (storememory.Snapshot, error) {
	snapshot := storememory.Snapshot{
		Authorizations: make(map[string]morphauth.Authorization),
		Sessions:       make(map[string]morphauth.Session),
		Tokens:         make(map[string]morphauth.Token),
	}
	if err := loadAuthorizations(db, &snapshot); err != nil {
		return storememory.Snapshot{}, err
	}
	if err := loadSessions(db, &snapshot); err != nil {
		return storememory.Snapshot{}, err
	}
	if err := loadTokens(db, &snapshot); err != nil {
		return storememory.Snapshot{}, err
	}
	if err := loadAudit(db, &snapshot); err != nil {
		return storememory.Snapshot{}, err
	}

	return snapshot, nil
}

func loadAuthorizations(
	db *sql.DB,
	snapshot *storememory.Snapshot,
) (resultErr error) {
	rows, err := db.Query(`
		SELECT identity_id, public_key, owner_id, user_id, max_ttl_ns,
			generation, revision, status, created_at, updated_at,
			revoked_at, revocation_note
		FROM authorizations
	`)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, rows.Close())
	}()
	for rows.Next() {
		var authorization morphauth.Authorization
		var publicKey []byte
		var maxTTL int64
		var createdAt, updatedAt string
		var revokedAt sql.NullString
		if err := rows.Scan(
			&authorization.IdentityID,
			&publicKey,
			&authorization.OwnerID,
			&authorization.UserID,
			&maxTTL,
			&authorization.Generation,
			&authorization.Revision,
			&authorization.Status,
			&createdAt,
			&updatedAt,
			&revokedAt,
			&authorization.RevocationNote,
		); err != nil {
			return err
		}
		authorization.PublicKey = append(ed25519.PublicKey(nil), publicKey...)
		authorization.MaxTTL = time.Duration(maxTTL)
		if authorization.CreatedAt, err = parseTime(createdAt); err != nil {
			return err
		}
		if authorization.UpdatedAt, err = parseTime(updatedAt); err != nil {
			return err
		}
		if authorization.RevokedAt, err = parseOptionalTime(revokedAt); err != nil {
			return err
		}
		snapshot.Authorizations[authorization.IdentityID] = authorization
	}
	if err := rows.Err(); err != nil {
		return err
	}

	return loadValues(
		db,
		"authorization_values",
		"identity_id",
		func(id, kind, value string) error {
			authorization, exists := snapshot.Authorizations[id]
			if !exists {
				return fmt.Errorf(
					"authorization value references unknown identity %q",
					id,
				)
			}
			appendAuthorizationValue(&authorization, kind, value)
			snapshot.Authorizations[id] = authorization

			return nil
		},
	)
}

func loadSessions(
	db *sql.DB,
	snapshot *storememory.Snapshot,
) (resultErr error) {
	rows, err := db.Query(`
		SELECT id, identity_id, owner_id, user_id, source, status,
			created_at, last_seen_at, idle_expires_at, absolute_expires_at,
			identity_generation, authorization_revision, revoked_at, revocation_note
		FROM sessions
	`)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, rows.Close())
	}()
	for rows.Next() {
		var session morphauth.Session
		var createdAt, lastSeenAt, idleExpiresAt, absoluteExpiresAt string
		var revokedAt sql.NullString
		if err := rows.Scan(
			&session.ID,
			&session.IdentityID,
			&session.OwnerID,
			&session.UserID,
			&session.Source,
			&session.Status,
			&createdAt,
			&lastSeenAt,
			&idleExpiresAt,
			&absoluteExpiresAt,
			&session.IdentityGeneration,
			&session.AuthorizationRevision,
			&revokedAt,
			&session.RevocationNote,
		); err != nil {
			return err
		}
		if session.CreatedAt, err = parseTime(createdAt); err != nil {
			return err
		}
		if session.LastSeenAt, err = parseTime(lastSeenAt); err != nil {
			return err
		}
		if session.IdleExpiresAt, err = parseTime(idleExpiresAt); err != nil {
			return err
		}
		if session.AbsoluteExpiresAt, err = parseTime(absoluteExpiresAt); err != nil {
			return err
		}
		if session.RevokedAt, err = parseOptionalTime(revokedAt); err != nil {
			return err
		}
		snapshot.Sessions[session.ID] = session
	}
	if err := rows.Err(); err != nil {
		return err
	}
	roleRows, err := db.Query(`
		SELECT session_id, role
		FROM session_roles
		ORDER BY session_id, position
	`)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, roleRows.Close())
	}()
	for roleRows.Next() {
		var id, role string
		if err := roleRows.Scan(&id, &role); err != nil {
			return err
		}
		session, exists := snapshot.Sessions[id]
		if !exists {
			return fmt.Errorf("session role references unknown session %q", id)
		}
		session.Roles = append(session.Roles, role)
		snapshot.Sessions[id] = session
	}

	return roleRows.Err()
}

func loadTokens(
	db *sql.DB,
	snapshot *storememory.Snapshot,
) (resultErr error) {
	rows, err := db.Query(`
		SELECT id, session_id, identity_id, owner_id, user_id, nonce,
			issued_at, not_before, expires_at, last_used_at, use_count,
			status, identity_generation, authorization_revision,
			certificate_thumbprint, revoked_at, revocation_note
		FROM tokens
	`)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, rows.Close())
	}()
	for rows.Next() {
		var token morphauth.Token
		var issuedAt, notBefore, expiresAt string
		var lastUsedAt, revokedAt sql.NullString
		if err := rows.Scan(
			&token.ID,
			&token.SessionID,
			&token.IdentityID,
			&token.OwnerID,
			&token.UserID,
			&token.Nonce,
			&issuedAt,
			&notBefore,
			&expiresAt,
			&lastUsedAt,
			&token.UseCount,
			&token.Status,
			&token.IdentityGeneration,
			&token.AuthorizationRevision,
			&token.CertificateThumbprint,
			&revokedAt,
			&token.RevocationNote,
		); err != nil {
			return err
		}
		if token.IssuedAt, err = parseTime(issuedAt); err != nil {
			return err
		}
		if token.NotBefore, err = parseTime(notBefore); err != nil {
			return err
		}
		if token.ExpiresAt, err = parseTime(expiresAt); err != nil {
			return err
		}
		if token.LastUsedAt, err = parseOptionalTime(lastUsedAt); err != nil {
			return err
		}
		if token.RevokedAt, err = parseOptionalTime(revokedAt); err != nil {
			return err
		}
		token.MethodUse = make(map[string]morphauth.MethodUse)
		snapshot.Tokens[token.ID] = token
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := loadValues(
		db,
		"token_values",
		"token_id",
		func(id, kind, value string) error {
			token, exists := snapshot.Tokens[id]
			if !exists {
				return fmt.Errorf("token value references unknown token %q", id)
			}
			appendTokenValue(&token, kind, value)
			snapshot.Tokens[id] = token

			return nil
		},
	); err != nil {
		return err
	}
	usageRows, err := db.Query(`
		SELECT token_id, method, use_count, first_used_at, last_used_at
		FROM token_method_usage
		ORDER BY token_id, method
	`)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, usageRows.Close())
	}()
	for usageRows.Next() {
		var tokenID, method, firstUsedAt, lastUsedAt string
		var usage morphauth.MethodUse
		if err := usageRows.Scan(
			&tokenID,
			&method,
			&usage.Count,
			&firstUsedAt,
			&lastUsedAt,
		); err != nil {
			return err
		}
		if usage.FirstUsedAt, err = parseTime(firstUsedAt); err != nil {
			return err
		}
		if usage.LastUsedAt, err = parseTime(lastUsedAt); err != nil {
			return err
		}
		token, exists := snapshot.Tokens[tokenID]
		if !exists {
			return fmt.Errorf("method usage references unknown token %q", tokenID)
		}
		token.MethodUse[method] = usage
		snapshot.Tokens[tokenID] = token
	}

	return usageRows.Err()
}

func loadValues(
	db *sql.DB,
	table, idColumn string,
	appendValue func(string, string, string) error,
) (resultErr error) {
	query := fmt.Sprintf(
		"SELECT %s, kind, value FROM %s ORDER BY %s, kind, position",
		idColumn,
		table,
		idColumn,
	)
	rows, err := db.Query(query)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, rows.Close())
	}()
	for rows.Next() {
		var id, kind, value string
		if err := rows.Scan(&id, &kind, &value); err != nil {
			return err
		}
		if err := appendValue(id, kind, value); err != nil {
			return err
		}
	}

	return rows.Err()
}

func loadAudit(
	db *sql.DB,
	snapshot *storememory.Snapshot,
) (resultErr error) {
	rows, err := db.Query(`
		SELECT id, type, identity_id, session_id, token_id, method, reason, created_at
		FROM audit_events
		ORDER BY sequence
	`)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, rows.Close())
	}()
	for rows.Next() {
		var event morphauth.AuditEvent
		var createdAt string
		if err := rows.Scan(
			&event.ID,
			&event.Type,
			&event.IdentityID,
			&event.SessionID,
			&event.TokenID,
			&event.Method,
			&event.Reason,
			&createdAt,
		); err != nil {
			return err
		}
		if event.CreatedAt, err = parseTime(createdAt); err != nil {
			return err
		}
		snapshot.Audit = append(snapshot.Audit, event)
	}

	return rows.Err()
}

func appendAuthorizationValue(
	authorization *morphauth.Authorization,
	kind, value string,
) {
	switch kind {
	case "role":
		authorization.Roles = append(authorization.Roles, value)
	case "service":
		authorization.Services = append(authorization.Services, value)
	case "method":
		authorization.Methods = append(authorization.Methods, value)
	}
}

func appendTokenValue(token *morphauth.Token, kind, value string) {
	switch kind {
	case "role":
		token.Roles = append(token.Roles, value)
	case "service":
		token.Services = append(token.Services, value)
	case "method":
		token.Methods = append(token.Methods, value)
	}
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	return keys
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func formatOptionalTime(value *time.Time) any {
	if value == nil {
		return nil
	}

	return formatTime(*value)
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse auth timestamp %q: %w", value, err)
	}

	return parsed, nil
}

func parseOptionalTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return nil, err
	}

	return &parsed, nil
}
