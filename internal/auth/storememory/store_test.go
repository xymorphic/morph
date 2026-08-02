package storememory_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	morphauth "github.com/xymorphic/morph/internal/auth"
	"github.com/xymorphic/morph/internal/auth/storememory"
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

	pruned, err := store.Prune(ctx, morphauth.PruneOptions{
		Before: time.Now().Add(2 * time.Hour),
		Limit:  100,
	})
	require.NoError(t, err)
	require.Positive(t, pruned.Total())
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

func TestStore_PruneRemovesTerminalRecordsInDependencyOrder(t *testing.T) {
	before := time.Now().UTC()
	terminalAt := before.Add(-time.Hour)
	snapshot := storememory.Snapshot{
		Authorizations: map[string]morphauth.Authorization{
			"identity": {
				IdentityID: "identity",
				Status:     morphauth.StatusRevoked,
				RevokedAt:  &terminalAt,
			},
		},
		Sessions: map[string]morphauth.Session{
			"session": {
				ID:         "session",
				IdentityID: "identity",
				Status:     morphauth.StatusRevoked,
				RevokedAt:  &terminalAt,
			},
		},
		Tokens: map[string]morphauth.Token{
			"token": {
				ID:         "token",
				SessionID:  "session",
				IdentityID: "identity",
				Status:     morphauth.StatusRevoked,
				RevokedAt:  &terminalAt,
			},
		},
		Audit: []morphauth.AuditEvent{{
			ID: "audit", CreatedAt: terminalAt,
		}},
	}
	store := storememory.NewFromSnapshot(snapshot)

	pruned, err := store.Prune(context.Background(), morphauth.PruneOptions{
		Before: before,
		Limit:  4,
	})
	require.NoError(t, err)
	require.Equal(t, morphauth.PruneResult{
		Tokens: 1, Sessions: 1, Authorizations: 1, AuditEvents: 1,
	}, pruned)
	remaining := store.Snapshot()
	require.Empty(t, remaining.Tokens)
	require.Empty(t, remaining.Sessions)
	require.Empty(t, remaining.Authorizations)
	require.Empty(t, remaining.Audit)
}

func TestStore_PrunePreservesDependenciesUntilChildrenAreEligible(t *testing.T) {
	before := time.Now().UTC()
	old := before.Add(-time.Hour)
	recent := before.Add(time.Hour)
	snapshot := storememory.Snapshot{
		Authorizations: map[string]morphauth.Authorization{
			"identity": {
				IdentityID: "identity",
				Status:     morphauth.StatusRevoked,
				RevokedAt:  &old,
			},
		},
		Sessions: map[string]morphauth.Session{
			"session": {
				ID:         "session",
				IdentityID: "identity",
				Status:     morphauth.StatusRevoked,
				RevokedAt:  &old,
			},
		},
		Tokens: map[string]morphauth.Token{
			"token": {
				ID:         "token",
				SessionID:  "session",
				IdentityID: "identity",
				Status:     morphauth.StatusRevoked,
				RevokedAt:  &recent,
			},
		},
	}
	store := storememory.NewFromSnapshot(snapshot)

	pruned, err := store.Prune(context.Background(), morphauth.PruneOptions{
		Before: before,
		Limit:  10,
	})
	require.NoError(t, err)
	require.Zero(t, pruned.Total())
	require.Len(t, store.Snapshot().Tokens, 1)
	require.Len(t, store.Snapshot().Sessions, 1)
	require.Len(t, store.Snapshot().Authorizations, 1)
}

func TestStore_PruneDryRunReportsWithoutMutation(t *testing.T) {
	before := time.Now().UTC()
	expired := before.Add(-time.Hour)
	store := storememory.NewFromSnapshot(storememory.Snapshot{
		Tokens: map[string]morphauth.Token{
			"token": {ID: "token", ExpiresAt: expired},
		},
	})

	pruned, err := store.Prune(context.Background(), morphauth.PruneOptions{
		Before: before,
		Limit:  1,
		DryRun: true,
	})
	require.NoError(t, err)
	require.Equal(t, morphauth.PruneResult{Tokens: 1}, pruned)
	require.Len(t, store.Snapshot().Tokens, 1)
}

func TestStore_PruneUsesGlobalLimitAndRemovesOldestTokensFirst(t *testing.T) {
	before := time.Now().UTC()
	snapshot := storememory.Snapshot{Tokens: make(map[string]morphauth.Token)}
	for index := range 3 {
		id := fmt.Sprintf("token-%d", index)
		snapshot.Tokens[id] = morphauth.Token{
			ID: id, ExpiresAt: before.Add(time.Duration(index-3) * time.Hour),
		}
	}
	store := storememory.NewFromSnapshot(snapshot)

	pruned, err := store.Prune(context.Background(), morphauth.PruneOptions{
		Before: before,
		Limit:  2,
	})
	require.NoError(t, err)
	require.Equal(t, morphauth.PruneResult{Tokens: 2}, pruned)
	remaining := store.Snapshot()
	require.Contains(t, remaining.Tokens, "token-2")
	require.NotContains(t, remaining.Tokens, "token-0")
	require.NotContains(t, remaining.Tokens, "token-1")
}

func TestStore_PruneUsesEffectiveExpiryAndPreservesIneligibleRecords(t *testing.T) {
	before := time.Now().UTC()
	old := before.Add(-time.Hour)
	future := before.Add(time.Hour)
	snapshot := storememory.Snapshot{
		Authorizations: map[string]morphauth.Authorization{
			"active-root": {
				IdentityID: "active-root",
				Status:     morphauth.StatusActive,
				Services:   []string{morphauth.RootScope},
				RevokedAt:  &old,
			},
			"revoked-without-time": {
				IdentityID: "revoked-without-time",
				Status:     morphauth.StatusRevoked,
			},
		},
		Sessions: map[string]morphauth.Session{
			"absolute-only": {
				ID: "absolute-only", AbsoluteExpiresAt: old,
			},
			"idle-only": {
				ID: "idle-only", IdleExpiresAt: old,
			},
			"idle-first": {
				ID: "idle-first", IdleExpiresAt: old, AbsoluteExpiresAt: future,
			},
			"absolute-first": {
				ID: "absolute-first", IdleExpiresAt: future, AbsoluteExpiresAt: old,
			},
			"revoked-without-time": {
				ID: "revoked-without-time", Status: morphauth.StatusRevoked,
			},
			"active": {
				ID: "active", IdleExpiresAt: future, AbsoluteExpiresAt: future,
			},
		},
		Tokens: map[string]morphauth.Token{
			"revoked-without-time": {
				ID: "revoked-without-time", Status: morphauth.StatusRevoked,
			},
			"active": {
				ID: "active", Status: morphauth.StatusActive, ExpiresAt: future,
			},
		},
		Audit: []morphauth.AuditEvent{
			{ID: "old", CreatedAt: old},
			{ID: "new", CreatedAt: future},
		},
	}
	store := storememory.NewFromSnapshot(snapshot)

	pruned, err := store.Prune(context.Background(), morphauth.PruneOptions{
		Before: before,
		Limit:  100,
	})
	require.NoError(t, err)
	require.Equal(t, morphauth.PruneResult{Sessions: 4, AuditEvents: 1}, pruned)
	remaining := store.Snapshot()
	require.Len(t, remaining.Authorizations, 2)
	require.Len(t, remaining.Sessions, 2)
	require.Len(t, remaining.Tokens, 2)
	require.Equal(t, []morphauth.AuditEvent{{ID: "new", CreatedAt: future}}, remaining.Audit)
}

func TestStore_PruneIgnoresNonPositiveLimit(t *testing.T) {
	store := storememory.NewFromSnapshot(storememory.Snapshot{
		Tokens: map[string]morphauth.Token{
			"token": {ID: "token", ExpiresAt: time.Now().Add(-time.Hour)},
		},
	})

	pruned, err := store.Prune(context.Background(), morphauth.PruneOptions{
		Before: time.Now(),
	})
	require.NoError(t, err)
	require.Zero(t, pruned.Total())
	require.Len(t, store.Snapshot().Tokens, 1)
}

func TestStore_PruneStopsForCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	pruned, err := storememory.New().Prune(ctx, morphauth.PruneOptions{
		Before: time.Now(),
		Limit:  1,
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, pruned.Total())
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
