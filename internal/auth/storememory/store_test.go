package storememory_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	morphauth "github.com/wandxy/morph/internal/auth"
	"github.com/wandxy/morph/internal/auth/storememory"
)

func TestStore_TracksUseRevocationAuditAndRotation(t *testing.T) {
	ctx := context.Background()
	store := storememory.New()
	service, identity := newService(t, store)
	raw, claims := signToken(t, identity, "session-1", "token-1")

	principal, err := service.OpenSession(ctx, raw, "cli")
	require.NoError(t, err)
	require.Equal(t, "cli", principal.Source)
	_, err = service.Authenticate(ctx, raw, "/morph.v1.SessionService/List", "")
	require.NoError(t, err)

	token, err := store.GetToken(ctx, claims.ID)
	require.NoError(t, err)
	require.Equal(t, uint64(1), token.UseCount)
	require.Equal(t, uint64(1), token.MethodUse["/morph.v1.SessionService/List"].Count)

	require.NoError(t, service.RevokeToken(ctx, claims.ID, "operator"))
	_, err = service.Authenticate(ctx, raw, "/morph.v1.SessionService/List", "")
	require.ErrorIs(t, err, morphauth.ErrInactiveCredential)
	_, err = service.OpenSession(ctx, raw, "cli")
	require.ErrorIs(t, err, morphauth.ErrUnauthenticated)

	rotationRaw, _ := signToken(t, identity, "rotation-session", "rotation-token")
	_, err = service.OpenSession(ctx, rotationRaw, "cli")
	require.NoError(t, err)
	next, err := morphauth.GenerateIdentity(identity.Generation + 1)
	require.NoError(t, err)
	require.NoError(t, service.RotateRoot(ctx, identity.ID, next, "owner"))
	_, err = service.Authenticate(ctx, rotationRaw, "/morph.v1.SessionService/List", "")
	require.ErrorIs(t, err, morphauth.ErrUnauthenticated)
	current, err := store.GetAuthorization(ctx, identity.ID)
	require.NoError(t, err)
	require.Equal(t, morphauth.StatusRevoked, current.Status)
	replacement, err := store.GetAuthorization(ctx, next.ID)
	require.NoError(t, err)
	require.Equal(t, morphauth.StatusActive, replacement.Status)

	events, err := store.ListAudit(ctx, 100)
	require.NoError(t, err)
	require.NotEmpty(t, events)
	for _, event := range events {
		require.NotEmpty(t, event.ID)
		require.NotZero(t, event.CreatedAt)
	}

	pruned, err := store.Prune(ctx, time.Now().Add(2*time.Hour), 100)
	require.NoError(t, err)
	require.Positive(t, pruned)
	_, err = store.GetToken(ctx, claims.ID)
	require.ErrorIs(t, err, morphauth.ErrNotFound)
}

func TestStore_RejectsSecondRootIdentity(t *testing.T) {
	store := storememory.New()
	service, current := newService(t, store)
	next, err := morphauth.GenerateIdentity(current.Generation + 1)
	require.NoError(t, err)

	_, err = service.SeedRoot(context.Background(), next, "owner")
	require.ErrorIs(t, err, morphauth.ErrPermissionDenied)

	authorizations, err := store.ListAuthorizations(context.Background())
	require.NoError(t, err)
	require.Len(t, authorizations, 1)
	require.Equal(t, current.ID, authorizations[0].IdentityID)
}

func newService(
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

func signToken(
	t *testing.T,
	identity morphauth.Identity,
	sessionID, tokenID string,
) (string, morphauth.AccessClaims) {
	t.Helper()
	raw, claims, err := morphauth.SignAccessToken(identity, morphauth.TokenRequest{
		Audience: "morph-rpc:test", Subject: identity.ID,
		SessionID: sessionID, TokenID: tokenID, OwnerID: "owner", Source: "cli",
		Roles: []string{morphauth.RoleOwner}, Services: []string{morphauth.RootScope},
		TTL: time.Hour, NotBefore: time.Now().Add(-time.Second),
		AuthorizationRevision: 1,
	})
	require.NoError(t, err)

	return raw, claims
}
