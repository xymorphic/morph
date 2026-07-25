package storesqlite_test

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	morphauth "github.com/wandxy/morph/internal/auth"
	"github.com/wandxy/morph/internal/auth/storesqlite"
)

func TestStore_PersistsUsageAndAuditAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "auth.db")
	store, err := storesqlite.Open(path)
	require.NoError(t, err)
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
	events, err := store.ListAudit(ctx, 100)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(events), 3)
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
