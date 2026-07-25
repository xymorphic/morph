package rpc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	morphauth "github.com/wandxy/morph/internal/auth"
	"github.com/wandxy/morph/internal/auth/storememory"
	morphpb "github.com/wandxy/morph/internal/rpc/proto"
	"github.com/wandxy/morph/internal/rpc/rpcmeta"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAuthService_GrantsListsAndRevokesBoundedAuthorization(t *testing.T) {
	service, store := newRPCAuthService(t)
	ctx := ownerAuthContext()
	delegated, err := morphauth.GenerateIdentity(1)
	require.NoError(t, err)

	granted, err := service.GrantAuthorization(ctx, &morphpb.GrantAuthAuthorizationRequest{
		Authorization: &morphpb.AuthAuthorization{
			IdentityId: delegated.ID, PublicKey: delegated.PublicKey,
			OwnerId: "owner", UserId: delegated.ID,
			Roles: []string{morphauth.RoleOperator},
			Methods: []string{
				morphpb.AuthService_OpenSession_FullMethodName,
				morphpb.BrowserService_Status_FullMethodName,
			},
			MaximumTtlSeconds: 300, Generation: 1,
		},
	})
	require.NoError(t, err)
	require.Equal(t, uint64(1), granted.GetAuthorization().GetRevision())
	require.NotEmpty(t, granted.GetAuthorization().GetCreatedAt())

	listed, err := service.ListAuthorizations(ctx, &morphpb.ListAuthAuthorizationsRequest{})
	require.NoError(t, err)
	require.Len(t, listed.GetAuthorizations(), 2)

	revoked, err := service.RevokeAuthorization(ctx, &morphpb.RevokeAuthAuthorizationRequest{
		IdentityId: delegated.ID, Reason: "operator",
	})
	require.NoError(t, err)
	require.Equal(t, morphauth.StatusRevoked, revoked.GetAuthorization().GetStatus())
	stored, err := store.GetAuthorization(context.Background(), delegated.ID)
	require.NoError(t, err)
	require.Equal(t, morphauth.StatusRevoked, stored.Status)
}

func TestAuthService_RejectsIdentityMismatchUnknownScopeAndExcessiveTTL(t *testing.T) {
	service, _ := newRPCAuthService(t)
	ctx := ownerAuthContext()
	delegated, err := morphauth.GenerateIdentity(1)
	require.NoError(t, err)
	request := &morphpb.GrantAuthAuthorizationRequest{Authorization: &morphpb.AuthAuthorization{
		IdentityId: "wrong", PublicKey: delegated.PublicKey,
		OwnerId: "owner", UserId: delegated.ID, Roles: []string{morphauth.RoleOperator},
		Methods:           []string{morphpb.BrowserService_Status_FullMethodName},
		MaximumTtlSeconds: 300, Generation: 1,
	}}
	_, err = service.GrantAuthorization(ctx, request)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	request.Authorization.IdentityId = delegated.ID
	request.Authorization.Methods = []string{"/morph.v1.UnknownService/Status"}
	_, err = service.GrantAuthorization(ctx, request)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	request.Authorization.Methods = []string{morphpb.BrowserService_Status_FullMethodName}
	request.Authorization.MaximumTtlSeconds = int64((25 * time.Hour) / time.Second)
	_, err = service.GrantAuthorization(ctx, request)
	require.Equal(t, codes.PermissionDenied, status.Code(err))

	request.Authorization.MaximumTtlSeconds = 300
	request.Authorization.Roles = []string{morphauth.RoleOwner}
	request.Authorization.Services = nil
	request.Authorization.Methods = []string{morphpb.BrowserService_Status_FullMethodName}
	_, err = service.GrantAuthorization(ctx, request)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestAuthService_RejectsDelegatedOwnerAdministration(t *testing.T) {
	service, store := newRPCAuthService(t)
	authorizations, err := store.ListAuthorizations(context.Background())
	require.NoError(t, err)
	require.Len(t, authorizations, 1)

	ctx := rpcmeta.WithAuthenticatedPrincipal(context.Background(), morphauth.Principal{
		IdentityID: "delegated-owner", OwnerID: "owner", UserID: "delegated-owner",
		Roles: []string{morphauth.RoleOwner}, SessionID: "session", TokenID: "token",
	})
	_, err = service.RevokeAuthorization(ctx, &morphpb.RevokeAuthAuthorizationRequest{
		IdentityId: authorizations[0].IdentityID,
	})
	require.Equal(t, codes.PermissionDenied, status.Code(err))

	stored, err := store.GetAuthorization(context.Background(), authorizations[0].IdentityID)
	require.NoError(t, err)
	require.Equal(t, morphauth.StatusActive, stored.Status)

	_, err = service.ListSessions(ctx, &morphpb.ListAuthSessionsRequest{})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestAuthService_ClosesOnlyAuthenticatedSession(t *testing.T) {
	service, store := newRPCAuthService(t)
	now := time.Now().UTC()
	require.NoError(t, store.Activate(
		context.Background(),
		morphauth.Session{
			ID: "current-session", IdentityID: "identity", OwnerID: "owner",
			UserID: "user", Roles: []string{morphauth.RoleOperator}, Source: "cli",
			Status: morphauth.StatusActive, CreatedAt: now, LastSeenAt: now,
			IdleExpiresAt: now.Add(time.Minute), AbsoluteExpiresAt: now.Add(time.Hour),
		},
		morphauth.Token{
			ID: "current-token", SessionID: "current-session", IdentityID: "identity",
			OwnerID: "owner", UserID: "user", Roles: []string{morphauth.RoleOperator},
			Methods: []string{morphpb.AuthService_CloseSession_FullMethodName},
			Status:  morphauth.StatusActive, IssuedAt: now, NotBefore: now,
			ExpiresAt: now.Add(time.Hour),
		},
	))
	ctx := rpcmeta.WithAuthenticatedPrincipal(context.Background(), morphauth.Principal{
		IdentityID: "identity", OwnerID: "owner", UserID: "user",
		Roles:     []string{morphauth.RoleOperator},
		SessionID: "current-session", TokenID: "current-token",
	})

	response, err := service.CloseSession(ctx, &morphpb.CloseAuthSessionRequest{})
	require.NoError(t, err)
	require.Equal(t, "current-session", response.GetSession().GetId())
	require.Equal(t, morphauth.StatusRevoked, response.GetSession().GetStatus())
}

func TestAuthService_RejectsInvalidSessionClose(t *testing.T) {
	service, _ := newRPCAuthService(t)
	_, err := service.CloseSession(context.Background(), &morphpb.CloseAuthSessionRequest{})
	require.Equal(t, codes.Unauthenticated, status.Code(err))

	ctx := rpcmeta.WithAuthenticatedPrincipal(context.Background(), morphauth.Principal{
		IdentityID: "identity", SessionID: "missing-session", TokenID: "token",
	})
	_, err = service.CloseSession(ctx, &morphpb.CloseAuthSessionRequest{})
	require.Equal(t, codes.NotFound, status.Code(err))

	store := &closeSessionStoreStub{
		Store:         storememory.New(),
		getSessionErr: morphauth.ErrNotFound,
	}
	authService, err := morphauth.NewService(morphauth.ServiceOptions{
		Audience: "morph-rpc:test",
		Store:    store,
	})
	require.NoError(t, err)
	service = NewAuthService(authService)
	_, err = service.CloseSession(ctx, &morphpb.CloseAuthSessionRequest{})
	require.Equal(t, codes.NotFound, status.Code(err))
}

func TestAuthService_RotatesOnlyTheAuthenticatedRootIdentity(t *testing.T) {
	service, store := newRPCAuthService(t)
	var applied bool
	service.prepareIdentityApply = func(
		_ context.Context,
		_ string,
		_ uint64,
	) (func(), error) {
		return func() { applied = true }, nil
	}
	authorizations, err := store.ListAuthorizations(context.Background())
	require.NoError(t, err)
	require.Len(t, authorizations, 1)
	current := authorizations[0]
	ctx := rpcmeta.WithAuthenticatedPrincipal(context.Background(), morphauth.Principal{
		IdentityID: current.IdentityID, OwnerID: current.OwnerID, UserID: current.UserID,
		Roles: []string{morphauth.RoleOwner}, RootAuthorization: true,
		SessionID: "session", TokenID: "token",
	})
	next, err := morphauth.GenerateIdentity(current.Generation + 1)
	require.NoError(t, err)

	response, err := service.RotateIdentity(ctx, &morphpb.RotateAuthIdentityRequest{
		CurrentIdentityId: current.IdentityID, NextIdentityId: next.ID,
		NextPublicKey: next.PublicKey, NextGeneration: next.Generation,
	})
	require.NoError(t, err)
	require.Equal(t, next.ID, response.GetAuthorization().GetIdentityId())
	require.True(t, applied)
	previous, err := store.GetAuthorization(context.Background(), current.IdentityID)
	require.NoError(t, err)
	require.Equal(t, morphauth.StatusRevoked, previous.Status)

	_, err = service.RotateIdentity(ctx, &morphpb.RotateAuthIdentityRequest{
		CurrentIdentityId: next.ID, NextIdentityId: next.ID,
		NextPublicKey: next.PublicKey, NextGeneration: next.Generation,
	})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

func newRPCAuthService(
	t *testing.T,
) (*AuthService, *storememory.Store) {
	t.Helper()
	store := storememory.New()
	auth, err := morphauth.NewService(morphauth.ServiceOptions{
		Audience: "morph-rpc:test", Store: store,
		SessionIdleTTL: time.Minute, SessionMaxTTL: 24 * time.Hour,
	})
	require.NoError(t, err)
	identity, err := morphauth.GenerateIdentity(1)
	require.NoError(t, err)
	_, err = auth.SeedRoot(context.Background(), identity, "owner")
	require.NoError(t, err)

	return NewAuthService(auth), store
}

func ownerAuthContext() context.Context {
	return rpcmeta.WithAuthenticatedPrincipal(context.Background(), morphauth.Principal{
		IdentityID: "root", OwnerID: "owner", UserID: "root",
		Roles: []string{morphauth.RoleOwner}, RootAuthorization: true,
		SessionID: "session", TokenID: "token",
	})
}

type closeSessionStoreStub struct {
	morphauth.Store
	getSessionErr error
}

func (s *closeSessionStoreStub) RevokeSession(
	context.Context,
	string,
	string,
	time.Time,
) error {
	return nil
}

func (s *closeSessionStoreStub) GetSession(
	context.Context,
	string,
) (morphauth.Session, error) {
	return morphauth.Session{}, s.getSessionErr
}
