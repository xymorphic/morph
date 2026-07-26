package rpc

import (
	"context"
	"crypto/ed25519"
	"errors"
	"strings"
	"time"

	morphauth "github.com/wandxy/morph/internal/auth"
	morphpb "github.com/wandxy/morph/internal/rpc/proto"
	"github.com/wandxy/morph/internal/rpc/rpcmeta"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type AuthService struct {
	morphpb.UnimplementedAuthServiceServer
	auth                 *morphauth.Service
	prepareIdentityApply func(context.Context, string, uint64) (func(), error)
}

type AuthServiceOption func(*AuthService)

func WithIdentityRotationApply(
	prepare func(context.Context, string, uint64) (func(), error),
) AuthServiceOption {
	return func(service *AuthService) {
		service.prepareIdentityApply = prepare
	}
}

func NewAuthService(service *morphauth.Service, options ...AuthServiceOption) *AuthService {
	result := &AuthService{auth: service}
	for _, option := range options {
		option(result)
	}
	return result
}

func (s *AuthService) OpenSession(
	ctx context.Context,
	_ *morphpb.OpenAuthSessionRequest,
) (*morphpb.OpenAuthSessionResponse, error) {
	principal, err := requireAuthPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	session, err := s.auth.Store().GetSession(ctx, principal.SessionID)
	if err != nil {
		return nil, authStoreErrorToStatus(err)
	}

	return &morphpb.OpenAuthSessionResponse{
		Principal: authPrincipalToProto(principal),
		Session:   authSessionToProto(session),
	}, nil
}

func (s *AuthService) CloseSession(
	ctx context.Context,
	_ *morphpb.CloseAuthSessionRequest,
) (*morphpb.CloseAuthSessionResponse, error) {
	principal, err := requireAuthPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.auth.RevokeSession(ctx, principal.SessionID, "authenticated client closed"); err != nil {
		return nil, authStoreErrorToStatus(err)
	}
	session, err := s.auth.Store().GetSession(ctx, principal.SessionID)
	if err != nil {
		return nil, authStoreErrorToStatus(err)
	}

	return &morphpb.CloseAuthSessionResponse{Session: authSessionToProto(session)}, nil
}

func (s *AuthService) ListSessions(
	ctx context.Context,
	request *morphpb.ListAuthSessionsRequest,
) (*morphpb.ListAuthSessionsResponse, error) {
	if err := requireOwner(ctx); err != nil {
		return nil, err
	}
	if request.GetLimit() < 0 {
		return nil, status.Error(codes.InvalidArgument, "session list limit must not be negative")
	}
	statusFilter, err := getAuthStatusFilter(request.GetStatus(), true)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	sessions, err := s.auth.Store().ListSessions(ctx)
	if err != nil {
		return nil, authStoreErrorToStatus(err)
	}
	response := &morphpb.ListAuthSessionsResponse{
		Sessions: make([]*morphpb.AuthSession, 0, len(sessions)),
	}
	now := time.Now().UTC()
	for _, session := range sessions {
		session.Status = getEffectiveSessionStatus(session, now)
		if statusFilter != "" && session.Status != statusFilter {
			continue
		}
		response.Sessions = append(response.Sessions, authSessionToProto(session))
		if hasReachedAuthListLimit(len(response.Sessions), request.GetLimit()) {
			break
		}
	}

	return response, nil
}

func (s *AuthService) RevokeSession(
	ctx context.Context,
	request *morphpb.RevokeAuthSessionRequest,
) (*morphpb.RevokeAuthSessionResponse, error) {
	if err := requireOwner(ctx); err != nil {
		return nil, err
	}
	if request == nil || strings.TrimSpace(request.GetId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "auth session ID is required")
	}
	if err := s.auth.RevokeSession(ctx, strings.TrimSpace(request.GetId()), request.GetReason()); err != nil {
		return nil, authStoreErrorToStatus(err)
	}
	session, err := s.auth.Store().GetSession(ctx, strings.TrimSpace(request.GetId()))
	if err != nil {
		return nil, authStoreErrorToStatus(err)
	}

	return &morphpb.RevokeAuthSessionResponse{Session: authSessionToProto(session)}, nil
}

func (s *AuthService) ListTokens(
	ctx context.Context,
	request *morphpb.ListAuthTokensRequest,
) (*morphpb.ListAuthTokensResponse, error) {
	if err := requireOwner(ctx); err != nil {
		return nil, err
	}
	if request.GetLimit() < 0 {
		return nil, status.Error(codes.InvalidArgument, "token list limit must not be negative")
	}
	statusFilter, err := getAuthStatusFilter(request.GetStatus(), true)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	tokens, err := s.auth.Store().ListTokens(ctx)
	if err != nil {
		return nil, authStoreErrorToStatus(err)
	}
	response := &morphpb.ListAuthTokensResponse{
		Tokens: make([]*morphpb.AuthToken, 0, len(tokens)),
	}
	now := time.Now().UTC()
	for _, token := range tokens {
		token.Status = getEffectiveTokenStatus(token, now)
		if statusFilter != "" && token.Status != statusFilter {
			continue
		}
		response.Tokens = append(response.Tokens, authTokenToProto(token))
		if hasReachedAuthListLimit(len(response.Tokens), request.GetLimit()) {
			break
		}
	}

	return response, nil
}

func (s *AuthService) RevokeToken(
	ctx context.Context,
	request *morphpb.RevokeAuthTokenRequest,
) (*morphpb.RevokeAuthTokenResponse, error) {
	if err := requireOwner(ctx); err != nil {
		return nil, err
	}
	if request == nil || strings.TrimSpace(request.GetId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "auth token ID is required")
	}
	if err := s.auth.RevokeToken(ctx, strings.TrimSpace(request.GetId()), request.GetReason()); err != nil {
		return nil, authStoreErrorToStatus(err)
	}
	token, err := s.auth.Store().GetToken(ctx, strings.TrimSpace(request.GetId()))
	if err != nil {
		return nil, authStoreErrorToStatus(err)
	}

	return &morphpb.RevokeAuthTokenResponse{Token: authTokenToProto(token)}, nil
}

func (s *AuthService) ListAuthorizations(
	ctx context.Context,
	request *morphpb.ListAuthAuthorizationsRequest,
) (*morphpb.ListAuthAuthorizationsResponse, error) {
	if err := requireOwner(ctx); err != nil {
		return nil, err
	}
	statusFilter, err := getAuthStatusFilter(request.GetStatus(), false)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	authorizations, err := s.auth.Store().ListAuthorizations(ctx)
	if err != nil {
		return nil, authStoreErrorToStatus(err)
	}
	response := &morphpb.ListAuthAuthorizationsResponse{
		Authorizations: make([]*morphpb.AuthAuthorization, 0, len(authorizations)),
	}
	for _, authorization := range authorizations {
		if statusFilter != "" && authorization.Status != statusFilter {
			continue
		}
		response.Authorizations = append(response.Authorizations, authAuthorizationToProto(authorization))
	}

	return response, nil
}

func (s *AuthService) GrantAuthorization(
	ctx context.Context,
	request *morphpb.GrantAuthAuthorizationRequest,
) (*morphpb.GrantAuthAuthorizationResponse, error) {
	if err := requireOwner(ctx); err != nil {
		return nil, err
	}
	authorization, err := authAuthorizationFromProto(request.GetAuthorization())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	authorization, err = s.auth.GrantAuthorization(ctx, authorization)
	if err != nil {
		return nil, authStoreErrorToStatus(err)
	}

	return &morphpb.GrantAuthAuthorizationResponse{
		Authorization: authAuthorizationToProto(authorization),
	}, nil
}

func (s *AuthService) RevokeAuthorization(
	ctx context.Context,
	request *morphpb.RevokeAuthAuthorizationRequest,
) (*morphpb.RevokeAuthAuthorizationResponse, error) {
	principal, err := requireAuthPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if !principal.IsRootOwner() {
		return nil, status.Error(codes.PermissionDenied, "owner authorization is required")
	}
	if request != nil && strings.TrimSpace(request.GetIdentityId()) == principal.IdentityID {
		return nil, status.Error(codes.FailedPrecondition,
			"active root identity must be changed through identity rotation")
	}
	if request == nil || strings.TrimSpace(request.GetIdentityId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "identity ID is required")
	}
	authorization, err := s.auth.Store().GetAuthorization(ctx, strings.TrimSpace(request.GetIdentityId()))
	if err != nil {
		return nil, authStoreErrorToStatus(err)
	}
	if hasRootAuthorization(authorization) {
		return nil, status.Error(codes.FailedPrecondition,
			"active root identity must be changed through identity rotation")
	}
	now := time.Now().UTC()
	authorization.Status = morphauth.StatusRevoked
	authorization.RevokedAt = &now
	authorization.RevocationNote = strings.TrimSpace(request.GetReason())
	authorization.UpdatedAt = now
	authorization.Revision++
	authorization, err = s.auth.Store().PutAuthorization(ctx, authorization)
	if err != nil {
		return nil, authStoreErrorToStatus(err)
	}

	return &morphpb.RevokeAuthAuthorizationResponse{
		Authorization: authAuthorizationToProto(authorization),
	}, nil
}

func (s *AuthService) ListAudit(
	ctx context.Context,
	request *morphpb.ListAuthAuditRequest,
) (*morphpb.ListAuthAuditResponse, error) {
	if err := requireOwner(ctx); err != nil {
		return nil, err
	}
	if request.GetLimit() < 0 {
		return nil, status.Error(codes.InvalidArgument, "audit list limit must not be negative")
	}
	since, err := getAuthAuditSince(request.GetSince())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	method := strings.TrimSpace(request.GetMethod())
	if method != "" {
		method, err = morphauth.NormalizeMethod(method)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "audit method filter is invalid")
		}
	}
	events, err := s.auth.Store().ListAudit(ctx, 0)
	if err != nil {
		return nil, authStoreErrorToStatus(err)
	}
	response := &morphpb.ListAuthAuditResponse{
		Events: make([]*morphpb.AuthAuditEvent, 0, len(events)),
	}
	eventType := strings.TrimSpace(request.GetType())
	identityID := strings.TrimSpace(request.GetIdentityId())
	sessionID := strings.TrimSpace(request.GetSessionId())
	tokenID := strings.TrimSpace(request.GetTokenId())
	for _, event := range events {
		if eventType != "" && event.Type != eventType ||
			identityID != "" && event.IdentityID != identityID ||
			sessionID != "" && event.SessionID != sessionID ||
			tokenID != "" && event.TokenID != tokenID ||
			method != "" && event.Method != method ||
			!since.IsZero() && event.CreatedAt.Before(since) {
			continue
		}
		response.Events = append(response.Events, authAuditEventToProto(event))
		if hasReachedAuthListLimit(len(response.Events), request.GetLimit()) {
			break
		}
	}

	return response, nil
}

func (s *AuthService) Prune(
	ctx context.Context,
	request *morphpb.PruneAuthRequest,
) (*morphpb.PruneAuthResponse, error) {
	if err := requireOwner(ctx); err != nil {
		return nil, err
	}
	if request == nil || request.GetBefore() == nil ||
		request.GetLimit() <= 0 || request.GetLimit() > morphauth.MaximumPruneLimit {
		return nil, status.Errorf(
			codes.InvalidArgument,
			"auth prune cutoff and limit between 1 and %d are required",
			morphauth.MaximumPruneLimit,
		)
	}
	if err := request.GetBefore().CheckValid(); err != nil {
		return nil, status.Error(codes.InvalidArgument, "auth prune cutoff is invalid")
	}
	if request.GetBefore().AsTime().After(time.Now()) {
		return nil, status.Error(codes.InvalidArgument, "auth prune cutoff must not be in the future")
	}
	pruned, err := s.auth.Store().Prune(ctx, morphauth.PruneOptions{
		Before: request.GetBefore().AsTime(),
		Limit:  int(request.GetLimit()),
		DryRun: request.GetDryRun(),
	})
	if err != nil {
		return nil, authStoreErrorToStatus(err)
	}

	return &morphpb.PruneAuthResponse{
		Tokens:         int32(pruned.Tokens),
		Sessions:       int32(pruned.Sessions),
		Authorizations: int32(pruned.Authorizations),
		AuditEvents:    int32(pruned.AuditEvents),
		DryRun:         request.GetDryRun(),
	}, nil
}

func (s *AuthService) RotateIdentity(
	ctx context.Context,
	request *morphpb.RotateAuthIdentityRequest,
) (*morphpb.RotateAuthIdentityResponse, error) {
	principal, err := requireAuthPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if !principal.IsRootOwner() || request == nil ||
		strings.TrimSpace(request.GetCurrentIdentityId()) != principal.IdentityID ||
		len(request.GetNextPublicKey()) != ed25519.PublicKeySize {
		return nil, status.Error(codes.PermissionDenied, "root identity rotation is not authorized")
	}
	current, err := s.auth.Store().GetAuthorization(ctx, principal.IdentityID)
	if err != nil {
		return nil, authStoreErrorToStatus(err)
	}
	if !hasRootAuthorization(current) ||
		request.GetNextGeneration() != current.Generation+1 {
		return nil, status.Error(codes.PermissionDenied, "root identity rotation is not authorized")
	}
	publicKey := append(ed25519.PublicKey(nil), request.GetNextPublicKey()...)
	identityID, err := morphauth.PublicKeyIdentityID(publicKey)
	if err != nil || identityID != strings.TrimSpace(request.GetNextIdentityId()) {
		return nil, status.Error(codes.InvalidArgument, "next identity ID must match the public key")
	}
	next := morphauth.Identity{
		ID: identityID, Generation: request.GetNextGeneration(), PublicKey: publicKey,
	}
	var apply func()
	if s.prepareIdentityApply != nil {
		apply, err = s.prepareIdentityApply(ctx, next.ID, next.Generation)
		if err != nil {
			return nil, status.Error(codes.FailedPrecondition,
				"next identity is not prepared for runtime activation")
		}
	}
	if err := s.auth.RotateRoot(ctx, current.IdentityID, next, current.OwnerID); err != nil {
		return nil, authStoreErrorToStatus(err)
	}
	if apply != nil {
		apply()
	}
	authorization, err := s.auth.Store().GetAuthorization(ctx, identityID)
	if err != nil {
		return nil, authStoreErrorToStatus(err)
	}

	return &morphpb.RotateAuthIdentityResponse{
		Authorization: authAuthorizationToProto(authorization),
	}, nil
}

func (s *AuthService) IdentityStatus(
	ctx context.Context,
	_ *morphpb.GetAuthIdentityStatusRequest,
) (*morphpb.GetAuthIdentityStatusResponse, error) {
	principal, err := requireAuthPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	authorization, err := s.auth.Store().GetAuthorization(ctx, principal.IdentityID)
	if err != nil {
		return nil, authStoreErrorToStatus(err)
	}

	return &morphpb.GetAuthIdentityStatusResponse{
		IdentityId:            authorization.IdentityID,
		Generation:            authorization.Generation,
		AuthorizationRevision: authorization.Revision,
		Status:                authorization.Status,
	}, nil
}

func hasRootAuthorization(authorization morphauth.Authorization) bool {
	for _, service := range authorization.Services {
		if service == morphauth.RootScope {
			return true
		}
	}

	return false
}

func requireAuthPrincipal(ctx context.Context) (morphauth.Principal, error) {
	principal, ok := rpcmeta.AuthenticatedPrincipal(ctx)
	if !ok {
		return morphauth.Principal{}, status.Error(codes.Unauthenticated, "RPC authentication failed")
	}

	return principal, nil
}

func requireOwner(ctx context.Context) error {
	principal, err := requireAuthPrincipal(ctx)
	if err != nil {
		return err
	}
	if !principal.IsRootOwner() {
		return status.Error(codes.PermissionDenied, "owner authorization is required")
	}

	return nil
}

func authStoreErrorToStatus(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return status.FromContextError(err).Err()
	}
	if errors.Is(err, morphauth.ErrNotFound) {
		return status.Error(codes.NotFound, "auth record not found")
	}
	if errors.Is(err, morphauth.ErrPermissionDenied) {
		return status.Error(codes.PermissionDenied, "auth operation is not authorized")
	}

	return status.Error(codes.Internal, "auth state operation failed")
}

func authPrincipalToProto(principal morphauth.Principal) *morphpb.AuthPrincipal {
	return &morphpb.AuthPrincipal{
		IdentityId: principal.IdentityID, OwnerId: principal.OwnerID, UserId: principal.UserID,
		Roles: append([]string(nil), principal.Roles...), SessionId: principal.SessionID,
		TokenId: principal.TokenID, Services: append([]string(nil), principal.Services...),
		Methods: append([]string(nil), principal.Methods...), Source: principal.Source,
		IdentityGeneration:    principal.IdentityGeneration,
		AuthorizationRevision: principal.AuthorizationRevision,
		CertificateThumbprint: principal.CertificateThumbprint,
	}
}

func authSessionToProto(session morphauth.Session) *morphpb.AuthSession {
	return &morphpb.AuthSession{
		Id: session.ID, IdentityId: session.IdentityID, OwnerId: session.OwnerID,
		UserId: session.UserID, Roles: append([]string(nil), session.Roles...),
		Source: session.Source, Status: session.Status, CreatedAt: timestamppb.New(session.CreatedAt),
		LastSeenAt: timestamppb.New(session.LastSeenAt), IdleExpiresAt: timestamppb.New(session.IdleExpiresAt),
		AbsoluteExpiresAt:     timestamppb.New(session.AbsoluteExpiresAt),
		IdentityGeneration:    session.IdentityGeneration,
		AuthorizationRevision: session.AuthorizationRevision,
		RevokedAt:             timePtrToProto(session.RevokedAt), RevocationNote: session.RevocationNote,
	}
}

func authTokenToProto(token morphauth.Token) *morphpb.AuthToken {
	return &morphpb.AuthToken{
		Id: token.ID, SessionId: token.SessionID, IdentityId: token.IdentityID,
		OwnerId: token.OwnerID, UserId: token.UserID, Roles: append([]string(nil), token.Roles...),
		Services: append([]string(nil), token.Services...), Methods: append([]string(nil), token.Methods...),
		IssuedAt: timestamppb.New(token.IssuedAt), NotBefore: timestamppb.New(token.NotBefore),
		ExpiresAt: timestamppb.New(token.ExpiresAt), LastUsedAt: timePtrToProto(token.LastUsedAt),
		UseCount: token.UseCount, Status: token.Status,
		IdentityGeneration:    token.IdentityGeneration,
		AuthorizationRevision: token.AuthorizationRevision,
		CertificateThumbprint: token.CertificateThumbprint,
		RevokedAt:             timePtrToProto(token.RevokedAt), RevocationNote: token.RevocationNote,
	}
}

func authAuthorizationToProto(authorization morphauth.Authorization) *morphpb.AuthAuthorization {
	return &morphpb.AuthAuthorization{
		IdentityId: authorization.IdentityID,
		PublicKey:  append([]byte(nil), authorization.PublicKey...),
		OwnerId:    authorization.OwnerID, UserId: authorization.UserID,
		Roles:             append([]string(nil), authorization.Roles...),
		Services:          append([]string(nil), authorization.Services...),
		Methods:           append([]string(nil), authorization.Methods...),
		MaximumTtlSeconds: int64(authorization.MaxTTL / time.Second),
		Generation:        authorization.Generation, Revision: authorization.Revision,
		Status: authorization.Status, CreatedAt: timestamppb.New(authorization.CreatedAt),
		UpdatedAt:      timestamppb.New(authorization.UpdatedAt),
		RevokedAt:      timePtrToProto(authorization.RevokedAt),
		RevocationNote: authorization.RevocationNote,
	}
}

func authAuthorizationFromProto(
	authorization *morphpb.AuthAuthorization,
) (morphauth.Authorization, error) {
	if authorization == nil || strings.TrimSpace(authorization.GetIdentityId()) == "" ||
		len(authorization.GetPublicKey()) != ed25519.PublicKeySize ||
		strings.TrimSpace(authorization.GetOwnerId()) == "" ||
		strings.TrimSpace(authorization.GetUserId()) == "" ||
		len(authorization.GetRoles()) == 0 ||
		len(authorization.GetServices()) == 0 && len(authorization.GetMethods()) == 0 ||
		authorization.GetMaximumTtlSeconds() <= 0 || authorization.GetGeneration() == 0 {
		return morphauth.Authorization{}, errors.New("valid identity authorization is required")
	}
	publicKey := append(ed25519.PublicKey(nil), authorization.GetPublicKey()...)
	identityID, err := morphauth.PublicKeyIdentityID(publicKey)
	if err != nil || identityID != strings.TrimSpace(authorization.GetIdentityId()) {
		return morphauth.Authorization{}, errors.New("identity ID must match the public key")
	}
	ownerRole := false
	for _, role := range authorization.GetRoles() {
		switch strings.TrimSpace(role) {
		case morphauth.RoleOwner:
			ownerRole = true
		case morphauth.RoleOperator:
		default:
			return morphauth.Authorization{}, errors.New("authorization contains an unsupported role")
		}
	}
	for _, service := range authorization.GetServices() {
		normalized, normalizeErr := morphauth.NormalizeService(service)
		if normalizeErr != nil || normalized == morphauth.RootScope && !ownerRole ||
			normalized != morphauth.RootScope && !morphauth.IsKnownRPCService(normalized) {
			return morphauth.Authorization{}, errors.New("authorization contains an invalid service scope")
		}
	}
	for _, method := range authorization.GetMethods() {
		if !morphauth.IsKnownRPCMethod(method) {
			return morphauth.Authorization{}, errors.New("authorization contains an invalid method scope")
		}
	}
	now := time.Now().UTC()
	statusValue := strings.TrimSpace(authorization.GetStatus())
	if statusValue == "" {
		statusValue = morphauth.StatusActive
	}
	if statusValue != morphauth.StatusActive {
		return morphauth.Authorization{}, errors.New("new authorization must be active")
	}

	return morphauth.Authorization{
		IdentityID: strings.TrimSpace(authorization.GetIdentityId()),
		PublicKey:  publicKey,
		OwnerID:    strings.TrimSpace(authorization.GetOwnerId()),
		UserID:     strings.TrimSpace(authorization.GetUserId()),
		Roles:      append([]string(nil), authorization.GetRoles()...),
		Services:   append([]string(nil), authorization.GetServices()...),
		Methods:    append([]string(nil), authorization.GetMethods()...),
		MaxTTL:     time.Duration(authorization.GetMaximumTtlSeconds()) * time.Second,
		Generation: authorization.GetGeneration(), Revision: authorization.GetRevision(),
		Status: statusValue, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func authAuditEventToProto(event morphauth.AuditEvent) *morphpb.AuthAuditEvent {
	return &morphpb.AuthAuditEvent{
		Id: event.ID, Type: event.Type, IdentityId: event.IdentityID,
		SessionId: event.SessionID, TokenId: event.TokenID, Method: event.Method,
		Reason: event.Reason, CreatedAt: timestamppb.New(event.CreatedAt),
	}
}

func getAuthStatusFilter(value string, allowExpired bool) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", morphauth.StatusActive, morphauth.StatusRevoked:
		return value, nil
	case morphauth.StatusExpired:
		if allowExpired {
			return value, nil
		}
	}

	if allowExpired {
		return "", errors.New("auth status must be active, revoked, or expired")
	}
	return "", errors.New("authorization status must be active or revoked")
}

func getEffectiveSessionStatus(session morphauth.Session, now time.Time) string {
	if session.Status == morphauth.StatusRevoked {
		return morphauth.StatusRevoked
	}
	if session.Status == morphauth.StatusExpired ||
		!session.IdleExpiresAt.IsZero() && !now.Before(session.IdleExpiresAt) ||
		!session.AbsoluteExpiresAt.IsZero() && !now.Before(session.AbsoluteExpiresAt) {
		return morphauth.StatusExpired
	}
	return session.Status
}

func getEffectiveTokenStatus(token morphauth.Token, now time.Time) string {
	if token.Status == morphauth.StatusRevoked {
		return morphauth.StatusRevoked
	}
	if token.Status == morphauth.StatusExpired ||
		!token.ExpiresAt.IsZero() && !now.Before(token.ExpiresAt) {
		return morphauth.StatusExpired
	}
	return token.Status
}

func getAuthAuditSince(value *timestamppb.Timestamp) (time.Time, error) {
	if value == nil {
		return time.Time{}, nil
	}
	if err := value.CheckValid(); err != nil {
		return time.Time{}, errors.New("audit since filter is invalid")
	}
	since := value.AsTime()
	if since.After(time.Now()) {
		return time.Time{}, errors.New("audit since filter must not be in the future")
	}
	return since, nil
}

func hasReachedAuthListLimit(count int, limit int32) bool {
	return limit > 0 && count >= int(limit)
}

func timePtrToProto(value *time.Time) *timestamppb.Timestamp {
	if value == nil {
		return nil
	}

	return timestamppb.New(*value)
}
