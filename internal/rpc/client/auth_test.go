package client

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	morphpb "github.com/xymorphic/morph/internal/rpc/proto"
	"google.golang.org/grpc"
)

func TestAuthService_ListRequestsIncludeFilters(t *testing.T) {
	since := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	stub := &authListServiceClientStub{}
	service := newAuthService(stub)

	_, err := service.ListSessions(context.Background(), AuthSessionListOptions{
		Limit: 10, Status: "active",
	})
	require.NoError(t, err)
	require.Equal(t, int32(10), stub.sessionRequest.GetLimit())
	require.Equal(t, "active", stub.sessionRequest.GetStatus())

	_, err = service.ListTokens(context.Background(), AuthTokenListOptions{
		Limit: 20, Status: "expired",
	})
	require.NoError(t, err)
	require.Equal(t, int32(20), stub.tokenRequest.GetLimit())
	require.Equal(t, "expired", stub.tokenRequest.GetStatus())

	_, err = service.ListAuthorizations(context.Background(), AuthAuthorizationListOptions{
		Status: "revoked",
	})
	require.NoError(t, err)
	require.Equal(t, "revoked", stub.authorizationRequest.GetStatus())

	_, err = service.ListAudit(context.Background(), AuthAuditListOptions{
		Limit: 25, Type: "scope_denied", IdentityID: "identity-1",
		SessionID: "session-1", TokenID: "token-1",
		Method: "/morph.v1.AuthService/ListTokens", Since: since,
	})
	require.NoError(t, err)
	require.Equal(t, int32(25), stub.auditRequest.GetLimit())
	require.Equal(t, "scope_denied", stub.auditRequest.GetType())
	require.Equal(t, "identity-1", stub.auditRequest.GetIdentityId())
	require.Equal(t, "session-1", stub.auditRequest.GetSessionId())
	require.Equal(t, "token-1", stub.auditRequest.GetTokenId())
	require.Equal(t, "/morph.v1.AuthService/ListTokens", stub.auditRequest.GetMethod())
	require.Equal(t, since, stub.auditRequest.GetSince().AsTime())

	pruned, err := service.Prune(context.Background(), AuthPruneOptions{
		Before: since,
		Limit:  100,
		DryRun: true,
	})
	require.NoError(t, err)
	require.Equal(t, since, stub.pruneRequest.GetBefore().AsTime())
	require.Equal(t, int32(100), stub.pruneRequest.GetLimit())
	require.True(t, stub.pruneRequest.GetDryRun())
	require.Equal(t, int32(2), pruned.GetTokens())

	stub.pruneErr = errors.New("prune failed")
	_, err = service.Prune(context.Background(), AuthPruneOptions{Before: since, Limit: 1})
	require.ErrorContains(t, err, "prune failed")
}

type authListServiceClientStub struct {
	morphpb.AuthServiceClient
	sessionRequest       *morphpb.ListAuthSessionsRequest
	tokenRequest         *morphpb.ListAuthTokensRequest
	authorizationRequest *morphpb.ListAuthAuthorizationsRequest
	auditRequest         *morphpb.ListAuthAuditRequest
	pruneRequest         *morphpb.PruneAuthRequest
	pruneErr             error
}

func (s *authListServiceClientStub) ListSessions(
	_ context.Context,
	request *morphpb.ListAuthSessionsRequest,
	_ ...grpc.CallOption,
) (*morphpb.ListAuthSessionsResponse, error) {
	s.sessionRequest = request
	return &morphpb.ListAuthSessionsResponse{}, nil
}

func (s *authListServiceClientStub) ListTokens(
	_ context.Context,
	request *morphpb.ListAuthTokensRequest,
	_ ...grpc.CallOption,
) (*morphpb.ListAuthTokensResponse, error) {
	s.tokenRequest = request
	return &morphpb.ListAuthTokensResponse{}, nil
}

func (s *authListServiceClientStub) ListAuthorizations(
	_ context.Context,
	request *morphpb.ListAuthAuthorizationsRequest,
	_ ...grpc.CallOption,
) (*morphpb.ListAuthAuthorizationsResponse, error) {
	s.authorizationRequest = request
	return &morphpb.ListAuthAuthorizationsResponse{}, nil
}

func (s *authListServiceClientStub) ListAudit(
	_ context.Context,
	request *morphpb.ListAuthAuditRequest,
	_ ...grpc.CallOption,
) (*morphpb.ListAuthAuditResponse, error) {
	s.auditRequest = request
	return &morphpb.ListAuthAuditResponse{}, nil
}

func (s *authListServiceClientStub) Prune(
	_ context.Context,
	request *morphpb.PruneAuthRequest,
	_ ...grpc.CallOption,
) (*morphpb.PruneAuthResponse, error) {
	s.pruneRequest = request
	if s.pruneErr != nil {
		return nil, s.pruneErr
	}
	return &morphpb.PruneAuthResponse{Tokens: 2}, nil
}
