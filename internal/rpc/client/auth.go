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

type AuthSessionListOptions struct {
	Limit  int32
	Status string
}

type AuthTokenListOptions struct {
	Limit  int32
	Status string
}

type AuthAuthorizationListOptions struct {
	Status string
}

type AuthAuditListOptions struct {
	Limit      int32
	Type       string
	IdentityID string
	SessionID  string
	TokenID    string
	Method     string
	Since      time.Time
}

type AuthPruneOptions struct {
	Before time.Time
	Limit  int32
	DryRun bool
}

type AuthAPI interface {
	ListSessions(context.Context, AuthSessionListOptions) ([]*morphpb.AuthSession, error)
	RevokeSession(context.Context, string, string) (*morphpb.AuthSession, error)
	ListTokens(context.Context, AuthTokenListOptions) ([]*morphpb.AuthToken, error)
	RevokeToken(context.Context, string, string) (*morphpb.AuthToken, error)
	ListAuthorizations(context.Context, AuthAuthorizationListOptions) ([]*morphpb.AuthAuthorization, error)
	GrantAuthorization(context.Context, *morphpb.AuthAuthorization) (*morphpb.AuthAuthorization, error)
	RevokeAuthorization(context.Context, string, string) (*morphpb.AuthAuthorization, error)
	ListAudit(context.Context, AuthAuditListOptions) ([]*morphpb.AuthAuditEvent, error)
	Prune(context.Context, AuthPruneOptions) (*morphpb.PruneAuthResponse, error)
	RotateIdentity(context.Context, string, string, []byte, uint64) (*morphpb.AuthAuthorization, error)
	IdentityStatus(context.Context) (*morphpb.GetAuthIdentityStatusResponse, error)
}

func newAuthService(client morphpb.AuthServiceClient) *AuthService {
	return &AuthService{client: client}
}

func (s *AuthService) ListSessions(
	ctx context.Context,
	options AuthSessionListOptions,
) ([]*morphpb.AuthSession, error) {
	response, err := s.client.ListSessions(ctx, &morphpb.ListAuthSessionsRequest{
		Limit: options.Limit, Status: options.Status,
	})
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

func (s *AuthService) ListTokens(
	ctx context.Context,
	options AuthTokenListOptions,
) ([]*morphpb.AuthToken, error) {
	response, err := s.client.ListTokens(ctx, &morphpb.ListAuthTokensRequest{
		Limit: options.Limit, Status: options.Status,
	})
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
	options AuthAuthorizationListOptions,
) ([]*morphpb.AuthAuthorization, error) {
	response, err := s.client.ListAuthorizations(ctx, &morphpb.ListAuthAuthorizationsRequest{
		Status: options.Status,
	})
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
	options AuthAuditListOptions,
) ([]*morphpb.AuthAuditEvent, error) {
	request := &morphpb.ListAuthAuditRequest{
		Limit:      options.Limit,
		Type:       options.Type,
		IdentityId: options.IdentityID,
		SessionId:  options.SessionID,
		TokenId:    options.TokenID,
		Method:     options.Method,
	}
	if !options.Since.IsZero() {
		request.Since = timestamppb.New(options.Since)
	}
	response, err := s.client.ListAudit(ctx, request)
	if err != nil {
		return nil, err
	}

	return response.GetEvents(), nil
}

func (s *AuthService) Prune(
	ctx context.Context,
	options AuthPruneOptions,
) (*morphpb.PruneAuthResponse, error) {
	response, err := s.client.Prune(ctx, &morphpb.PruneAuthRequest{
		Before: timestamppb.New(options.Before),
		Limit:  options.Limit,
		DryRun: options.DryRun,
	})
	if err != nil {
		return nil, err
	}

	return response, nil
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
