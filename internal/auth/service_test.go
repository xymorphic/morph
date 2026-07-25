package auth_test

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	morphauth "github.com/wandxy/morph/internal/auth"
	"github.com/wandxy/morph/internal/auth/storememory"
	"github.com/wandxy/morph/internal/auth/storesqlite"
)

func TestService_ActivatesAuthenticatesAndRevokesToken(t *testing.T) {
	ctx := context.Background()
	store := storememory.New()
	service, identity := newAuthService(t, store)
	raw, claims := signOwnerToken(t, identity, "session", "token")

	principal, err := service.OpenSession(ctx, raw, "cli")
	require.NoError(t, err)
	require.Equal(t, identity.ID, principal.IdentityID)
	require.Equal(t, "cli", principal.Source)
	require.True(t, principal.IsRootOwner())

	principal, err = service.Authenticate(ctx, raw, "/morph.v1.SessionService/List", "")
	require.NoError(t, err)
	require.Equal(t, claims.ID, principal.TokenID)
	require.True(t, principal.IsRootOwner())
	token, err := store.GetToken(ctx, claims.ID)
	require.NoError(t, err)
	require.Equal(t, uint64(1), token.UseCount)

	require.NoError(t, service.RevokeToken(ctx, claims.ID, "operator"))
	_, err = service.Authenticate(ctx, raw, "/morph.v1.SessionService/List", "")
	require.ErrorIs(t, err, morphauth.ErrInactiveCredential)
}

func TestService_RejectsScopeWideningAndWrongMethod(t *testing.T) {
	ctx := context.Background()
	store := storememory.New()
	service, identity := newAuthService(t, store)
	request := ownerTokenRequest("session", "token")
	request.Subject = identity.ID
	request.Services = nil
	request.Methods = []string{
		"/morph.v1.AuthService/OpenSession",
		"/morph.v1.SessionService/List",
	}
	raw, _, err := morphauth.SignAccessToken(identity, request)
	require.NoError(t, err)
	_, err = service.OpenSession(ctx, raw, "cli")
	require.NoError(t, err)

	authorization, err := store.GetAuthorization(ctx, identity.ID)
	require.NoError(t, err)
	authorization.Methods = append([]string(nil), request.Methods...)
	authorization.Services = nil
	authorization.Revision++
	authorization.UpdatedAt = time.Now().UTC()
	authorization, err = store.PutAuthorization(ctx, authorization)
	require.NoError(t, err)

	request.AuthorizationRevision = authorization.Revision
	request.SessionID = "bounded-session"
	request.TokenID = "bounded-token"
	raw, _, err = morphauth.SignAccessToken(identity, request)
	require.NoError(t, err)
	_, err = service.OpenSession(ctx, raw, "cli")
	require.NoError(t, err)
	_, err = service.Authenticate(ctx, raw, "/morph.v1.SessionService/Archive", "")
	require.ErrorIs(t, err, morphauth.ErrPermissionDenied)
}

func TestService_UsesMaximumTokenTTLIndependentlyFromSessionTTL(t *testing.T) {
	store := storememory.New()
	service, err := morphauth.NewService(morphauth.ServiceOptions{
		Audience: "morph-rpc:test", Store: store,
		MaximumTokenTTL: time.Hour, SessionIdleTTL: time.Minute,
		SessionMaxTTL: 24 * time.Hour,
	})
	require.NoError(t, err)
	root, err := morphauth.GenerateIdentity(1)
	require.NoError(t, err)
	authorization, err := service.SeedRoot(context.Background(), root, "owner")
	require.NoError(t, err)
	require.Equal(t, time.Hour, authorization.MaxTTL)

	delegated, err := morphauth.GenerateIdentity(1)
	require.NoError(t, err)
	_, err = service.GrantAuthorization(context.Background(), morphauth.Authorization{
		IdentityID: delegated.ID, PublicKey: delegated.PublicKey,
		OwnerID: "owner", UserID: delegated.ID,
		Roles: []string{morphauth.RoleOperator},
		Methods: []string{
			"/morph.v1.AuthService/OpenSession",
			"/morph.v1.SessionService/List",
		},
		MaxTTL: 2 * time.Hour, Generation: 1, Status: morphauth.StatusActive,
	})
	require.ErrorIs(t, err, morphauth.ErrPermissionDenied)

	request := ownerTokenRequest("session", "token")
	request.Subject = root.ID
	request.TTL = 2 * time.Hour
	raw, _, err := morphauth.SignAccessToken(root, request)
	require.NoError(t, err)
	reloaded, err := morphauth.NewService(morphauth.ServiceOptions{
		Audience: "morph-rpc:test", Store: store,
		MaximumTokenTTL: time.Hour, SessionIdleTTL: time.Minute,
		SessionMaxTTL: 24 * time.Hour,
	})
	require.NoError(t, err)
	_, err = reloaded.OpenSession(context.Background(), raw, "cli")
	require.ErrorIs(t, err, morphauth.ErrUnauthenticated)
}

func TestService_RejectsDelegatedOwnerAuthorization(t *testing.T) {
	store := storememory.New()
	service, _ := newAuthService(t, store)
	delegated, err := morphauth.GenerateIdentity(1)
	require.NoError(t, err)

	_, err = service.GrantAuthorization(context.Background(), morphauth.Authorization{
		IdentityID: delegated.ID, PublicKey: delegated.PublicKey,
		OwnerID: "owner", UserID: delegated.ID,
		Roles: []string{morphauth.RoleOwner},
		Methods: []string{
			"/morph.v1.AuthService/OpenSession",
			"/morph.v1.SessionService/List",
		},
		MaxTTL: time.Hour, Generation: 1, Status: morphauth.StatusActive,
	})
	require.ErrorIs(t, err, morphauth.ErrPermissionDenied)
}

func TestService_DoesNotDeriveRootAuthorityFromDelegatedToken(t *testing.T) {
	ctx := context.Background()
	store := storememory.New()
	service, _ := newAuthService(t, store)
	delegated, err := morphauth.GenerateIdentity(1)
	require.NoError(t, err)
	authorization, err := store.PutAuthorization(ctx, morphauth.Authorization{
		IdentityID: delegated.ID, PublicKey: delegated.PublicKey,
		OwnerID: "owner", UserID: delegated.ID,
		Roles: []string{morphauth.RoleOperator},
		Methods: []string{
			"/morph.v1.AuthService/OpenSession",
			"/morph.v1.SessionService/List",
		},
		MaxTTL: time.Hour, Generation: 1, Revision: 1,
		Status: morphauth.StatusActive, CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	})
	require.NoError(t, err)
	raw, _, err := morphauth.SignAccessToken(delegated, morphauth.TokenRequest{
		Audience: "morph-rpc:test", Subject: delegated.ID,
		SessionID: "delegated-session", TokenID: "delegated-token",
		OwnerID: "owner", Source: "cli",
		Roles: authorization.Roles, Methods: authorization.Methods,
		TTL: time.Minute, NotBefore: time.Now().Add(-time.Second),
		AuthorizationRevision: authorization.Revision,
	})
	require.NoError(t, err)

	principal, err := service.OpenSession(ctx, raw, "cli")
	require.NoError(t, err)
	require.False(t, principal.IsRootOwner())
	principal, err = service.Authenticate(
		ctx,
		raw,
		"/morph.v1.SessionService/List",
		"",
	)
	require.NoError(t, err)
	require.False(t, principal.IsRootOwner())
}

func TestService_RejectsExpiredTokenAndExpiredSessionDeadlines(t *testing.T) {
	now := time.Now().UTC()
	clock := func() time.Time { return now }
	store := storememory.New()
	service, err := morphauth.NewService(morphauth.ServiceOptions{
		Audience: "morph-rpc:test", Store: store, Clock: clock,
		SessionIdleTTL: time.Minute, SessionMaxTTL: time.Hour,
	})
	require.NoError(t, err)
	identity, err := morphauth.GenerateIdentity(1)
	require.NoError(t, err)
	_, err = service.SeedRoot(context.Background(), identity, "owner")
	require.NoError(t, err)

	expiredRequest := ownerTokenRequest("expired-session", "expired-token")
	expiredRequest.Subject = identity.ID
	expiredRequest.NotBefore = now.Add(-2 * time.Hour)
	expiredRequest.TTL = time.Hour
	expired, _, err := morphauth.SignAccessToken(identity, expiredRequest)
	require.NoError(t, err)
	_, err = service.OpenSession(context.Background(), expired, "cli")
	require.ErrorIs(t, err, morphauth.ErrUnauthenticated)

	activeRequest := ownerTokenRequest("idle-session", "idle-token")
	activeRequest.Subject = identity.ID
	activeRequest.NotBefore = now.Add(-time.Second)
	active, _, err := morphauth.SignAccessToken(identity, activeRequest)
	require.NoError(t, err)
	_, err = service.OpenSession(context.Background(), active, "cli")
	require.NoError(t, err)
	now = now.Add(2 * time.Minute)
	_, err = service.Authenticate(
		context.Background(), active, "/morph.v1.SessionService/List", "",
	)
	require.ErrorIs(t, err, morphauth.ErrInactiveCredential)

	now = now.Add(-2 * time.Minute)
	absoluteRequest := ownerTokenRequest("absolute-session", "absolute-token")
	absoluteRequest.Subject = identity.ID
	absoluteRequest.NotBefore = now.Add(-time.Second)
	absoluteRequest.TTL = 2 * time.Hour
	absolute, _, err := morphauth.SignAccessToken(identity, absoluteRequest)
	require.NoError(t, err)
	_, err = service.OpenSession(context.Background(), absolute, "cli")
	require.NoError(t, err)
	now = now.Add(time.Hour + time.Second)
	_, err = service.Authenticate(
		context.Background(), absolute, "/morph.v1.SessionService/List", "",
	)
	require.ErrorIs(t, err, morphauth.ErrInactiveCredential)
}

func TestService_CoalescesRepeatedAuthenticationFailureAudit(t *testing.T) {
	now := time.Now().UTC()
	store := storememory.New()
	service, err := morphauth.NewService(morphauth.ServiceOptions{
		Audience: "morph-rpc:test", Store: store,
		Clock: func() time.Time { return now },
	})
	require.NoError(t, err)

	_, err = service.OpenSession(context.Background(), "invalid", "cli")
	require.ErrorIs(t, err, morphauth.ErrUnauthenticated)
	_, err = service.OpenSession(context.Background(), "invalid", "cli")
	require.ErrorIs(t, err, morphauth.ErrUnauthenticated)
	events, err := store.ListAudit(context.Background(), 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "session_open_rejected", events[0].Type)

	_, err = service.OpenSession(context.Background(), "different-invalid", "cli")
	require.ErrorIs(t, err, morphauth.ErrUnauthenticated)
	events, err = store.ListAudit(context.Background(), 10)
	require.NoError(t, err)
	require.Len(t, events, 2)

	now = now.Add(2 * time.Second)
	_, err = service.OpenSession(context.Background(), "invalid", "cli")
	require.ErrorIs(t, err, morphauth.ErrUnauthenticated)
	events, err = store.ListAudit(context.Background(), 10)
	require.NoError(t, err)
	require.Len(t, events, 3)
}

func TestService_AuditsUnverifiedAuthenticationFailure(t *testing.T) {
	store := storememory.New()
	service, err := morphauth.NewService(morphauth.ServiceOptions{
		Audience: "morph-rpc:test", Store: store,
	})
	require.NoError(t, err)

	_, err = service.Authenticate(
		context.Background(),
		"invalid",
		"/morph.v1.SessionService/List",
		"",
	)
	require.ErrorIs(t, err, morphauth.ErrUnauthenticated)
	events, err := store.ListAudit(context.Background(), 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "authentication_rejected", events[0].Type)
	require.Equal(t, "/morph.v1.SessionService/List", events[0].Reason)
}

func TestService_RateLimitsDistinctUnverifiedFailureAudit(t *testing.T) {
	now := time.Now().UTC()
	store := storememory.New()
	service, err := morphauth.NewService(morphauth.ServiceOptions{
		Audience: "morph-rpc:test", Store: store,
		Clock: func() time.Time { return now },
	})
	require.NoError(t, err)

	for index := 0; index < 256; index++ {
		_, err = service.OpenSession(
			context.Background(),
			"invalid-"+strconv.Itoa(index),
			"cli",
		)
		require.ErrorIs(t, err, morphauth.ErrUnauthenticated)
	}
	events, err := store.ListAudit(context.Background(), 300)
	require.NoError(t, err)
	require.Len(t, events, 128)

	now = now.Add(2 * time.Second)
	_, err = service.OpenSession(context.Background(), "next-window", "cli")
	require.ErrorIs(t, err, morphauth.ErrUnauthenticated)
	events, err = store.ListAudit(context.Background(), 300)
	require.NoError(t, err)
	require.Len(t, events, 129)
	require.Equal(t, "session_open_rejected", events[0].Type)
	require.Equal(t, "authentication_audit_rate_limited", events[1].Type)
	require.Equal(t, "additional authentication failures suppressed", events[1].Reason)
}

func TestService_RateLimitsDistinctVerifiedFailureAudit(t *testing.T) {
	now := time.Now().UTC()
	store := storememory.New()
	service, err := morphauth.NewService(morphauth.ServiceOptions{
		Audience: "morph-rpc:test", Store: store,
		Clock: func() time.Time { return now }, Leeway: time.Minute,
	})
	require.NoError(t, err)
	identity, err := morphauth.GenerateIdentity(1)
	require.NoError(t, err)
	_, err = service.SeedRoot(context.Background(), identity, "owner")
	require.NoError(t, err)

	for index := 0; index < 256; index++ {
		request := ownerTokenRequest("session-"+strconv.Itoa(index), "token-"+strconv.Itoa(index))
		request.Subject = identity.ID
		request.Services = nil
		request.Methods = []string{"/morph.v1.SessionService/List"}
		request.NotBefore = now.Add(-time.Second)
		raw, _, err := morphauth.SignAccessToken(identity, request)
		require.NoError(t, err)
		_, err = service.OpenSession(context.Background(), raw, "cli")
		require.ErrorIs(t, err, morphauth.ErrPermissionDenied)
	}
	events, err := store.ListAudit(context.Background(), 300)
	require.NoError(t, err)
	require.Len(t, events, 129)
	require.Equal(t, "authentication_audit_rate_limited", events[0].Type)
}

func TestService_KeepAliveExtendsActiveStreamSession(t *testing.T) {
	now := time.Now().UTC()
	store := storememory.New()
	service, err := morphauth.NewService(morphauth.ServiceOptions{
		Audience: "morph-rpc:test", Store: store,
		Clock:          func() time.Time { return now },
		SessionIdleTTL: 300 * time.Millisecond, SessionMaxTTL: time.Hour,
	})
	require.NoError(t, err)
	identity, err := morphauth.GenerateIdentity(1)
	require.NoError(t, err)
	_, err = service.SeedRoot(context.Background(), identity, "owner")
	require.NoError(t, err)
	raw, _ := signOwnerToken(t, identity, "stream-session", "stream-token")
	principal, err := service.OpenSession(context.Background(), raw, "cli")
	require.NoError(t, err)

	now = now.Add(200 * time.Millisecond)
	require.NoError(t, service.KeepAlivePrincipal(context.Background(), principal))
	now = now.Add(200 * time.Millisecond)
	require.NoError(t, service.CheckPrincipal(context.Background(), principal))
}

func TestSQLiteStore_PersistsRevocationAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.db")
	store, err := storesqlite.Open(path)
	require.NoError(t, err)
	service, identity := newAuthService(t, store)
	raw, claims := signOwnerToken(t, identity, "session", "token")
	_, err = service.OpenSession(context.Background(), raw, "cli")
	require.NoError(t, err)
	require.NoError(t, service.RevokeSession(context.Background(), claims.SessionID, "operator"))
	require.NoError(t, store.Close())

	store, err = storesqlite.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	session, err := store.GetSession(context.Background(), claims.SessionID)
	require.NoError(t, err)
	require.Equal(t, morphauth.StatusRevoked, session.Status)
	token, err := store.GetToken(context.Background(), claims.ID)
	require.NoError(t, err)
	require.Equal(t, morphauth.StatusRevoked, token.Status)
	reopened, err := morphauth.NewService(morphauth.ServiceOptions{
		Audience: "morph-rpc:test", Store: store,
		SessionIdleTTL: time.Hour, SessionMaxTTL: 24 * time.Hour,
	})
	require.NoError(t, err)
	_, err = reopened.Authenticate(
		context.Background(), raw, "/morph.v1.SessionService/List", "",
	)
	require.ErrorIs(t, err, morphauth.ErrInactiveCredential)
}

func newAuthService(t *testing.T, store morphauth.Store) (*morphauth.Service, morphauth.Identity) {
	t.Helper()
	service, err := morphauth.NewService(morphauth.ServiceOptions{
		Audience:       "morph-rpc:test",
		Store:          store,
		SessionIdleTTL: time.Hour,
		SessionMaxTTL:  24 * time.Hour,
	})
	require.NoError(t, err)
	identity, err := morphauth.GenerateIdentity(1)
	require.NoError(t, err)
	_, err = service.SeedRoot(context.Background(), identity, "owner")
	require.NoError(t, err)

	return service, identity
}

func signOwnerToken(
	t *testing.T,
	identity morphauth.Identity,
	sessionID, tokenID string,
) (string, morphauth.AccessClaims) {
	t.Helper()
	request := ownerTokenRequest(sessionID, tokenID)
	request.Subject = identity.ID
	raw, claims, err := morphauth.SignAccessToken(identity, request)
	require.NoError(t, err)
	return raw, claims
}

func ownerTokenRequest(sessionID, tokenID string) morphauth.TokenRequest {
	return morphauth.TokenRequest{
		Audience:              "morph-rpc:test",
		Subject:               "owner",
		SessionID:             sessionID,
		TokenID:               tokenID,
		OwnerID:               "owner",
		Source:                "cli",
		Roles:                 []string{morphauth.RoleOwner},
		Services:              []string{morphauth.RootScope},
		TTL:                   time.Hour,
		NotBefore:             time.Now().Add(-time.Second),
		AuthorizationRevision: 1,
	}
}
