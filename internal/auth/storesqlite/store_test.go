package storesqlite_test

import (
	"context"
	"path/filepath"
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
