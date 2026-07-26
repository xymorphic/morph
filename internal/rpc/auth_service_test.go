package rpc

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	morphauth "github.com/wandxy/morph/internal/auth"
	"github.com/wandxy/morph/internal/auth/storememory"
	morphpb "github.com/wandxy/morph/internal/rpc/proto"
	"github.com/wandxy/morph/internal/rpc/rpcmeta"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
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

	listed, err = service.ListAuthorizations(ctx, &morphpb.ListAuthAuthorizationsRequest{
		Status: morphauth.StatusActive,
	})
	require.NoError(t, err)
	require.Len(t, listed.GetAuthorizations(), 1)
	require.Equal(t, morphauth.StatusActive, listed.GetAuthorizations()[0].GetStatus())

	listed, err = service.ListAuthorizations(ctx, &morphpb.ListAuthAuthorizationsRequest{
		Status: morphauth.StatusRevoked,
	})
	require.NoError(t, err)
	require.Len(t, listed.GetAuthorizations(), 1)
	require.Equal(t, delegated.ID, listed.GetAuthorizations()[0].GetIdentityId())

	_, err = service.ListAuthorizations(ctx, &morphpb.ListAuthAuthorizationsRequest{
		Status: morphauth.StatusExpired,
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
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

func TestAuthService_PrunesTerminalAuthenticationStateAndSupportsDryRun(t *testing.T) {
	service, store := newRPCAuthService(t)
	now := time.Now().UTC()
	terminalAt := now.Add(-2 * time.Hour)
	cutoff := now.Add(-time.Hour)
	delegated, err := morphauth.GenerateIdentity(1)
	require.NoError(t, err)
	_, err = store.PutAuthorization(context.Background(), morphauth.Authorization{
		IdentityID: delegated.ID,
		PublicKey:  delegated.PublicKey,
		OwnerID:    "owner",
		UserID:     delegated.ID,
		Roles:      []string{morphauth.RoleOperator},
		Methods:    []string{morphpb.SessionService_List_FullMethodName},
		MaxTTL:     time.Hour,
		Generation: 1,
		Revision:   2,
		Status:     morphauth.StatusRevoked,
		CreatedAt:  terminalAt,
		UpdatedAt:  terminalAt,
		RevokedAt:  &terminalAt,
	})
	require.NoError(t, err)
	require.NoError(t, store.Activate(
		context.Background(),
		morphauth.Session{
			ID: "stale-session", IdentityID: delegated.ID,
			Status: morphauth.StatusRevoked, CreatedAt: terminalAt,
			IdleExpiresAt: terminalAt, AbsoluteExpiresAt: terminalAt,
			RevokedAt: &terminalAt,
		},
		morphauth.Token{
			ID: "stale-token", SessionID: "stale-session", IdentityID: delegated.ID,
			Status: morphauth.StatusRevoked, IssuedAt: terminalAt,
			ExpiresAt: terminalAt, RevokedAt: &terminalAt,
		},
	))

	preview, err := service.Prune(ownerAuthContext(), &morphpb.PruneAuthRequest{
		Before: timestamppb.New(cutoff),
		Limit:  100,
		DryRun: true,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), preview.GetTokens())
	require.Equal(t, int32(1), preview.GetSessions())
	require.Equal(t, int32(1), preview.GetAuthorizations())
	require.Equal(t, int32(3), preview.GetAuditEvents())
	require.True(t, preview.GetDryRun())
	_, err = store.GetToken(context.Background(), "stale-token")
	require.NoError(t, err)

	pruned, err := service.Prune(ownerAuthContext(), &morphpb.PruneAuthRequest{
		Before: timestamppb.New(cutoff),
		Limit:  100,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), pruned.GetTokens())
	require.Equal(t, int32(1), pruned.GetSessions())
	require.Equal(t, int32(1), pruned.GetAuthorizations())
	require.Equal(t, int32(3), pruned.GetAuditEvents())
	require.False(t, pruned.GetDryRun())
	_, err = store.GetToken(context.Background(), "stale-token")
	require.ErrorIs(t, err, morphauth.ErrNotFound)
	_, err = store.GetSession(context.Background(), "stale-session")
	require.ErrorIs(t, err, morphauth.ErrNotFound)
	_, err = store.GetAuthorization(context.Background(), delegated.ID)
	require.ErrorIs(t, err, morphauth.ErrNotFound)
}

func TestAuthService_PruneValidatesOwnerCutoffAndLimit(t *testing.T) {
	service, _ := newRPCAuthService(t)
	validBefore := timestamppb.New(time.Now().Add(-time.Hour))

	_, err := service.Prune(context.Background(), &morphpb.PruneAuthRequest{
		Before: validBefore, Limit: 1,
	})
	require.Equal(t, codes.Unauthenticated, status.Code(err))

	for _, request := range []*morphpb.PruneAuthRequest{
		nil,
		{Limit: 1},
		{Before: validBefore},
		{Before: validBefore, Limit: morphauth.MaximumPruneLimit + 1},
		{Before: &timestamppb.Timestamp{Seconds: 253402300800}, Limit: 1},
		{Before: timestamppb.New(time.Now().Add(time.Hour)), Limit: 1},
	} {
		_, err = service.Prune(ownerAuthContext(), request)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	}

	store := &pruneErrorStore{Store: storememory.New()}
	auth, err := morphauth.NewService(morphauth.ServiceOptions{
		Audience: "morph-rpc:test",
		Store:    store,
	})
	require.NoError(t, err)
	for storeErr, expectedCode := range map[error]codes.Code{
		context.Canceled:         codes.Canceled,
		context.DeadlineExceeded: codes.DeadlineExceeded,
	} {
		store.err = storeErr
		_, err = NewAuthService(auth).Prune(ownerAuthContext(), &morphpb.PruneAuthRequest{
			Before: validBefore,
			Limit:  1,
		})
		require.Equal(t, expectedCode, status.Code(err))
	}
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

func TestAuthService_ListSessionsAppliesLimit(t *testing.T) {
	service, store := newRPCAuthService(t)
	now := time.Now().UTC()
	for index := range 3 {
		id := strconv.Itoa(index)
		require.NoError(t, store.Activate(
			context.Background(),
			morphauth.Session{
				ID: "session-" + id, IdentityID: "identity",
				Status: morphauth.StatusActive, CreatedAt: now.Add(time.Duration(index) * time.Second),
				AbsoluteExpiresAt: now.Add(time.Hour),
			},
			morphauth.Token{
				ID: "token-" + id, SessionID: "session-" + id, IdentityID: "identity",
				Status: morphauth.StatusActive, IssuedAt: now.Add(time.Duration(index) * time.Second),
				ExpiresAt: now.Add(time.Hour),
			},
		))
	}

	response, err := service.ListSessions(
		ownerAuthContext(),
		&morphpb.ListAuthSessionsRequest{Limit: 2},
	)
	require.NoError(t, err)
	require.Len(t, response.GetSessions(), 2)
	require.Equal(t, "session-2", response.GetSessions()[0].GetId())
	require.Equal(t, "session-1", response.GetSessions()[1].GetId())

	response, err = service.ListSessions(
		ownerAuthContext(),
		&morphpb.ListAuthSessionsRequest{},
	)
	require.NoError(t, err)
	require.Len(t, response.GetSessions(), 3)

	_, err = service.ListSessions(
		ownerAuthContext(),
		&morphpb.ListAuthSessionsRequest{Limit: -1},
	)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestAuthService_ListSessionsFiltersEffectiveStatusBeforeLimit(t *testing.T) {
	service, store := newRPCAuthService(t)
	now := time.Now().UTC()
	for _, session := range []morphauth.Session{
		{
			ID: "active-session", IdentityID: "identity", Status: morphauth.StatusActive,
			CreatedAt: now.Add(-2 * time.Minute), IdleExpiresAt: now.Add(time.Hour),
			AbsoluteExpiresAt: now.Add(2 * time.Hour),
		},
		{
			ID: "expired-session", IdentityID: "identity", Status: morphauth.StatusActive,
			CreatedAt: now.Add(-time.Minute), IdleExpiresAt: now.Add(-time.Second),
			AbsoluteExpiresAt: now.Add(time.Hour),
		},
	} {
		require.NoError(t, store.Activate(
			context.Background(),
			session,
			morphauth.Token{
				ID: "token-" + session.ID, SessionID: session.ID, IdentityID: "identity",
				Status: morphauth.StatusActive, IssuedAt: session.CreatedAt,
				ExpiresAt: now.Add(time.Hour),
			},
		))
	}

	response, err := service.ListSessions(
		ownerAuthContext(),
		&morphpb.ListAuthSessionsRequest{Limit: 1, Status: morphauth.StatusActive},
	)
	require.NoError(t, err)
	require.Len(t, response.GetSessions(), 1)
	require.Equal(t, "active-session", response.GetSessions()[0].GetId())

	response, err = service.ListSessions(
		ownerAuthContext(),
		&morphpb.ListAuthSessionsRequest{Status: morphauth.StatusExpired},
	)
	require.NoError(t, err)
	require.Len(t, response.GetSessions(), 1)
	require.Equal(t, "expired-session", response.GetSessions()[0].GetId())
	require.Equal(t, morphauth.StatusExpired, response.GetSessions()[0].GetStatus())

	_, err = service.ListSessions(
		ownerAuthContext(),
		&morphpb.ListAuthSessionsRequest{Status: "unknown"},
	)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestAuthService_ListTokensAppliesLimit(t *testing.T) {
	service, store := newRPCAuthService(t)
	now := time.Now().UTC()
	for index := range 3 {
		id := strconv.Itoa(index)
		require.NoError(t, store.Activate(
			context.Background(),
			morphauth.Session{
				ID: "session-" + id, IdentityID: "identity",
				Status: morphauth.StatusActive, CreatedAt: now.Add(time.Duration(index) * time.Second),
				AbsoluteExpiresAt: now.Add(time.Hour),
			},
			morphauth.Token{
				ID: "token-" + id, SessionID: "session-" + id, IdentityID: "identity",
				Status: morphauth.StatusActive, IssuedAt: now.Add(time.Duration(index) * time.Second),
				ExpiresAt: now.Add(time.Hour),
			},
		))
	}

	response, err := service.ListTokens(
		ownerAuthContext(),
		&morphpb.ListAuthTokensRequest{Limit: 2},
	)
	require.NoError(t, err)
	require.Len(t, response.GetTokens(), 2)
	require.Equal(t, "token-2", response.GetTokens()[0].GetId())
	require.Equal(t, "token-1", response.GetTokens()[1].GetId())

	response, err = service.ListTokens(
		ownerAuthContext(),
		&morphpb.ListAuthTokensRequest{},
	)
	require.NoError(t, err)
	require.Len(t, response.GetTokens(), 3)

	_, err = service.ListTokens(
		ownerAuthContext(),
		&morphpb.ListAuthTokensRequest{Limit: -1},
	)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestAuthService_ListTokensFiltersEffectiveStatusBeforeLimit(t *testing.T) {
	service, store := newRPCAuthService(t)
	now := time.Now().UTC()
	for _, token := range []morphauth.Token{
		{
			ID: "active-token", SessionID: "active-session", IdentityID: "identity",
			Status: morphauth.StatusActive, IssuedAt: now.Add(-2 * time.Minute),
			ExpiresAt: now.Add(time.Hour),
		},
		{
			ID: "expired-token", SessionID: "expired-session", IdentityID: "identity",
			Status: morphauth.StatusActive, IssuedAt: now.Add(-time.Minute),
			ExpiresAt: now.Add(-time.Second),
		},
	} {
		require.NoError(t, store.Activate(
			context.Background(),
			morphauth.Session{
				ID: token.SessionID, IdentityID: "identity", Status: morphauth.StatusActive,
				CreatedAt: token.IssuedAt, IdleExpiresAt: now.Add(time.Hour),
				AbsoluteExpiresAt: now.Add(2 * time.Hour),
			},
			token,
		))
	}

	response, err := service.ListTokens(
		ownerAuthContext(),
		&morphpb.ListAuthTokensRequest{Limit: 1, Status: morphauth.StatusActive},
	)
	require.NoError(t, err)
	require.Len(t, response.GetTokens(), 1)
	require.Equal(t, "active-token", response.GetTokens()[0].GetId())

	response, err = service.ListTokens(
		ownerAuthContext(),
		&morphpb.ListAuthTokensRequest{Status: morphauth.StatusExpired},
	)
	require.NoError(t, err)
	require.Len(t, response.GetTokens(), 1)
	require.Equal(t, "expired-token", response.GetTokens()[0].GetId())
	require.Equal(t, morphauth.StatusExpired, response.GetTokens()[0].GetStatus())

	_, err = service.ListTokens(
		ownerAuthContext(),
		&morphpb.ListAuthTokensRequest{Status: "unknown"},
	)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestAuthService_ListAuditAppliesFiltersBeforeLimit(t *testing.T) {
	service, store := newRPCAuthService(t)
	now := time.Now().UTC()
	require.NoError(t, store.AppendAudit(context.Background(), morphauth.AuditEvent{
		ID: "matching", Type: "scope_denied", IdentityID: "identity-1",
		SessionID: "session-1", TokenID: "token-1",
		Method:    morphpb.AuthService_ListTokens_FullMethodName,
		CreatedAt: now.Add(-time.Hour),
	}))
	require.NoError(t, store.AppendAudit(context.Background(), morphauth.AuditEvent{
		ID: "newer-nonmatching", Type: "token_revoked", IdentityID: "identity-2",
		CreatedAt: now.Add(-time.Minute),
	}))

	response, err := service.ListAudit(ownerAuthContext(), &morphpb.ListAuthAuditRequest{
		Limit:      1,
		Type:       "scope_denied",
		IdentityId: "identity-1",
		SessionId:  "session-1",
		TokenId:    "token-1",
		Method:     morphpb.AuthService_ListTokens_FullMethodName,
		Since:      timestamppb.New(now.Add(-2 * time.Hour)),
	})
	require.NoError(t, err)
	require.Len(t, response.GetEvents(), 1)
	require.Equal(t, "matching", response.GetEvents()[0].GetId())

	_, err = service.ListAudit(ownerAuthContext(), &morphpb.ListAuthAuditRequest{
		Method: "invalid",
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = service.ListAudit(ownerAuthContext(), &morphpb.ListAuthAuditRequest{
		Since: timestamppb.New(now.Add(time.Hour)),
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = service.ListAudit(ownerAuthContext(), &morphpb.ListAuthAuditRequest{
		Limit: -1,
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
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

type pruneErrorStore struct {
	morphauth.Store
	err error
}

func (s *pruneErrorStore) Prune(
	context.Context,
	morphauth.PruneOptions,
) (morphauth.PruneResult, error) {
	return morphauth.PruneResult{}, s.err
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
