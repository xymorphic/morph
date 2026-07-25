package client

import (
	"context"
	"time"

	morphpb "github.com/wandxy/morph/internal/rpc/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type AuthService struct {
	client morphpb.AuthServiceClient
}

type AuthAPI interface {
	ListSessions(context.Context) ([]*morphpb.AuthSession, error)
	RevokeSession(context.Context, string, string) (*morphpb.AuthSession, error)
	ListTokens(context.Context) ([]*morphpb.AuthToken, error)
	RevokeToken(context.Context, string, string) (*morphpb.AuthToken, error)
	ListAuthorizations(context.Context) ([]*morphpb.AuthAuthorization, error)
	GrantAuthorization(context.Context, *morphpb.AuthAuthorization) (*morphpb.AuthAuthorization, error)
	RevokeAuthorization(context.Context, string, string) (*morphpb.AuthAuthorization, error)
	ListAudit(context.Context, int32) ([]*morphpb.AuthAuditEvent, error)
	PruneAudit(context.Context, time.Time, int32) (int32, error)
	RotateIdentity(context.Context, string, string, []byte, uint64) (*morphpb.AuthAuthorization, error)
	IdentityStatus(context.Context) (*morphpb.GetAuthIdentityStatusResponse, error)
}

func newAuthService(client morphpb.AuthServiceClient) *AuthService {
	return &AuthService{client: client}
}

func (s *AuthService) ListSessions(ctx context.Context) ([]*morphpb.AuthSession, error) {
	response, err := s.client.ListSessions(ctx, &morphpb.ListAuthSessionsRequest{})
	if err != nil {
		return nil, err
	}

	return response.GetSessions(), nil
}

func (s *AuthService) RevokeSession(
	ctx context.Context,
	id, reason string,
) (*morphpb.AuthSession, error) {
	response, err := s.client.RevokeSession(ctx, &morphpb.RevokeAuthSessionRequest{
		Id: id, Reason: reason,
	})
	if err != nil {
		return nil, err
	}

	return response.GetSession(), nil
}

func (s *AuthService) ListTokens(ctx context.Context) ([]*morphpb.AuthToken, error) {
	response, err := s.client.ListTokens(ctx, &morphpb.ListAuthTokensRequest{})
	if err != nil {
		return nil, err
	}

	return response.GetTokens(), nil
}

func (s *AuthService) RevokeToken(
	ctx context.Context,
	id, reason string,
) (*morphpb.AuthToken, error) {
	response, err := s.client.RevokeToken(ctx, &morphpb.RevokeAuthTokenRequest{
		Id: id, Reason: reason,
	})
	if err != nil {
		return nil, err
	}

	return response.GetToken(), nil
}

func (s *AuthService) ListAuthorizations(
	ctx context.Context,
) ([]*morphpb.AuthAuthorization, error) {
	response, err := s.client.ListAuthorizations(ctx, &morphpb.ListAuthAuthorizationsRequest{})
	if err != nil {
		return nil, err
	}

	return response.GetAuthorizations(), nil
}

func (s *AuthService) GrantAuthorization(
	ctx context.Context,
	authorization *morphpb.AuthAuthorization,
) (*morphpb.AuthAuthorization, error) {
	response, err := s.client.GrantAuthorization(ctx, &morphpb.GrantAuthAuthorizationRequest{
		Authorization: authorization,
	})
	if err != nil {
		return nil, err
	}

	return response.GetAuthorization(), nil
}

func (s *AuthService) RevokeAuthorization(
	ctx context.Context,
	identityID, reason string,
) (*morphpb.AuthAuthorization, error) {
	response, err := s.client.RevokeAuthorization(ctx, &morphpb.RevokeAuthAuthorizationRequest{
		IdentityId: identityID, Reason: reason,
	})
	if err != nil {
		return nil, err
	}

	return response.GetAuthorization(), nil
}

func (s *AuthService) ListAudit(
	ctx context.Context,
	limit int32,
) ([]*morphpb.AuthAuditEvent, error) {
	response, err := s.client.ListAudit(ctx, &morphpb.ListAuthAuditRequest{Limit: limit})
	if err != nil {
		return nil, err
	}

	return response.GetEvents(), nil
}

func (s *AuthService) PruneAudit(
	ctx context.Context,
	before time.Time,
	limit int32,
) (int32, error) {
	response, err := s.client.PruneAudit(ctx, &morphpb.PruneAuthAuditRequest{
		Before: timestamppb.New(before), Limit: limit,
	})
	if err != nil {
		return 0, err
	}

	return response.GetPruned(), nil
}

func (s *AuthService) IdentityStatus(
	ctx context.Context,
) (*morphpb.GetAuthIdentityStatusResponse, error) {
	return s.client.IdentityStatus(ctx, &morphpb.GetAuthIdentityStatusRequest{})
}

func (s *AuthService) RotateIdentity(
	ctx context.Context,
	currentIdentityID, nextIdentityID string,
	nextPublicKey []byte,
	nextGeneration uint64,
) (*morphpb.AuthAuthorization, error) {
	response, err := s.client.RotateIdentity(ctx, &morphpb.RotateAuthIdentityRequest{
		CurrentIdentityId: currentIdentityID,
		NextIdentityId:    nextIdentityID,
		NextPublicKey:     append([]byte(nil), nextPublicKey...),
		NextGeneration:    nextGeneration,
	})
	if err != nil {
		return nil, err
	}

	return response.GetAuthorization(), nil
}

func (c *Client) AuthAPI() AuthAPI {
	if c == nil {
		return nil
	}

	return c.Auth
}
