package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"
)

const auditCoalesceWindow = time.Second
const maximumFailureAuditsPerWindow = 128

type ServiceOptions struct {
	Audience         string
	Store            Store
	Clock            func() time.Time
	Leeway           time.Duration
	MaximumTokenSize int
	MaximumTokenTTL  time.Duration
	SessionIdleTTL   time.Duration
	SessionMaxTTL    time.Duration
}

type Service struct {
	audience         string
	store            Store
	clock            func() time.Time
	leeway           time.Duration
	maximumTokenSize int
	maximumTokenTTL  time.Duration
	sessionIdleTTL   time.Duration
	sessionMaxTTL    time.Duration
	auditMu          sync.Mutex
	auditLast        map[string]time.Time
	auditWindowStart time.Time
	failureAudits    int
	suppressionAudit bool
}

func NewService(options ServiceOptions) (*Service, error) {
	if strings.TrimSpace(options.Audience) == "" {
		return nil, errors.New("RPC auth audience is required")
	}
	if options.Store == nil {
		return nil, errors.New("RPC auth store is required")
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	if options.MaximumTokenSize <= 0 {
		options.MaximumTokenSize = DefaultMaximumTokenSize
	}
	if options.MaximumTokenTTL <= 0 {
		options.MaximumTokenTTL = 24 * time.Hour
	}
	if options.SessionIdleTTL <= 0 {
		options.SessionIdleTTL = 15 * time.Minute
	}
	if options.SessionMaxTTL <= 0 {
		options.SessionMaxTTL = 24 * time.Hour
	}

	return &Service{
		audience:         strings.TrimSpace(options.Audience),
		store:            options.Store,
		clock:            options.Clock,
		leeway:           options.Leeway,
		maximumTokenSize: options.MaximumTokenSize,
		maximumTokenTTL:  options.MaximumTokenTTL,
		sessionIdleTTL:   options.SessionIdleTTL,
		sessionMaxTTL:    options.SessionMaxTTL,
		auditLast:        make(map[string]time.Time),
	}, nil
}

func (s *Service) SeedRoot(ctx context.Context, identity Identity, ownerID string) (Authorization, error) {
	now := s.clock().UTC()
	return s.store.SeedRoot(ctx, getRootAuthorization(identity, ownerID, s.maximumTokenTTL, now))
}

func (s *Service) RotateRoot(
	ctx context.Context,
	currentIdentityID string,
	identity Identity,
	ownerID string,
) error {
	now := s.clock().UTC()
	return s.store.RotateIdentity(
		ctx, currentIdentityID,
		getRootAuthorization(identity, ownerID, s.maximumTokenTTL, now),
		now,
	)
}

func getRootAuthorization(
	identity Identity,
	ownerID string,
	maxTTL time.Duration,
	now time.Time,
) Authorization {
	authorization := Authorization{
		IdentityID: identity.ID,
		PublicKey:  append(ed25519.PublicKey(nil), identity.PublicKey...),
		OwnerID:    strings.TrimSpace(ownerID),
		UserID:     identity.ID,
		Roles:      []string{RoleOwner},
		Services:   []string{RootScope},
		MaxTTL:     maxTTL,
		Generation: identity.Generation,
		Revision:   1,
		Status:     StatusActive,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if authorization.OwnerID == "" {
		authorization.OwnerID = identity.ID
	}

	return authorization
}

func (s *Service) OpenSession(ctx context.Context, raw, source string) (Principal, error) {
	return s.OpenSessionBound(ctx, raw, source, "")
}

func (s *Service) OpenSessionBound(
	ctx context.Context,
	raw, source, certificateThumbprint string,
) (Principal, error) {
	authorization, claims, err := s.validateSignedToken(ctx, raw)
	if err != nil {
		s.auditUnverifiedFailure(ctx, "session_open_rejected", raw, err.Error())
		return Principal{}, ErrUnauthenticated
	}
	if err := checkClaimsWithinAuthorization(
		claims, authorization, s.maximumTokenTTL, s.clock().UTC(),
	); err != nil {
		s.auditFailure(ctx, "session_open_rejected", authorization.IdentityID, claims.SessionID, claims.ID, err.Error())
		return Principal{}, ErrUnauthenticated
	}
	source = strings.TrimSpace(source)
	if source != "" && source != claims.Source {
		s.auditFailure(ctx, "session_open_rejected",
			authorization.IdentityID, claims.SessionID, claims.ID, "source mismatch")
		return Principal{}, ErrUnauthenticated
	}
	if !AllowsMethod(
		claims.Services, claims.Methods,
		"/morph.v1.AuthService/OpenSession",
		hasRole(claims.Roles, RoleOwner),
	) {
		s.auditFailure(ctx, "session_open_rejected",
			authorization.IdentityID, claims.SessionID, claims.ID, "bootstrap scope")
		return Principal{}, ErrPermissionDenied
	}
	if claims.Confirmation != nil &&
		(certificateThumbprint == "" ||
			claims.Confirmation.CertificateThumbprint != certificateThumbprint) {
		s.auditFailure(ctx, "certificate_binding_failed",
			authorization.IdentityID, claims.SessionID, claims.ID, "session open")
		return Principal{}, ErrUnauthenticated
	}
	now := s.clock().UTC()
	absoluteExpiry := now.Add(s.sessionMaxTTL)
	idleExpiry := now.Add(s.sessionIdleTTL)
	if idleExpiry.After(absoluteExpiry) {
		idleExpiry = absoluteExpiry
	}
	session := Session{
		ID:                    claims.SessionID,
		IdentityID:            authorization.IdentityID,
		OwnerID:               claims.OwnerID,
		UserID:                claims.Subject,
		Roles:                 append([]string(nil), claims.Roles...),
		Source:                claims.Source,
		Status:                StatusActive,
		CreatedAt:             now,
		LastSeenAt:            now,
		IdleExpiresAt:         idleExpiry,
		AbsoluteExpiresAt:     absoluteExpiry,
		IdentityGeneration:    claims.IdentityGeneration,
		AuthorizationRevision: claims.AuthorizationRevision,
	}
	token := tokenFromClaims(claims, authorization.IdentityID)
	if err := s.store.Activate(ctx, session, token); err != nil {
		s.auditFailure(ctx, "session_open_rejected", authorization.IdentityID, claims.SessionID, claims.ID, err.Error())
		return Principal{}, ErrUnauthenticated
	}
	return principalFromClaims(
		claims,
		authorization.IdentityID,
		claims.Source,
		containsScope(authorization.Services, RootScope),
	), nil
}

func (s *Service) Authenticate(
	ctx context.Context,
	raw string,
	method string,
	certificateThumbprint string,
) (Principal, error) {
	authorization, claims, err := s.validateSignedToken(ctx, raw)
	if err != nil {
		s.auditUnverifiedFailure(ctx, "authentication_rejected", raw, method)
		return Principal{}, ErrUnauthenticated
	}
	if err := checkClaimsWithinAuthorization(
		claims, authorization, s.maximumTokenTTL, s.clock().UTC(),
	); err != nil {
		eventType := "authentication_rejected"
		if errors.Is(err, ErrAuthorizationChange) {
			eventType = "authorization_changed"
		}
		s.auditFailure(ctx, eventType,
			authorization.IdentityID, claims.SessionID, claims.ID, method)
		return Principal{}, ErrUnauthenticated
	}
	if !AllowsMethod(claims.Services, claims.Methods, method, hasRole(claims.Roles, RoleOwner)) {
		s.auditFailure(ctx, "scope_denied", authorization.IdentityID, claims.SessionID, claims.ID, method)
		return Principal{}, ErrPermissionDenied
	}
	if claims.Confirmation != nil {
		if certificateThumbprint == "" ||
			claims.Confirmation.CertificateThumbprint != certificateThumbprint {
			s.auditFailure(ctx, "certificate_binding_failed", authorization.IdentityID, claims.SessionID, claims.ID, method)
			return Principal{}, ErrUnauthenticated
		}
	}
	session, err := s.store.GetSession(ctx, claims.SessionID)
	if err != nil || !isLiveSession(session, claims, s.clock().UTC()) {
		s.auditFailure(ctx, "session_inactive",
			authorization.IdentityID, claims.SessionID, claims.ID, method)
		return Principal{}, ErrInactiveCredential
	}
	token, err := s.store.GetToken(ctx, claims.ID)
	if err != nil || !isLiveToken(token, claims, s.clock().UTC()) {
		s.auditFailure(ctx, "token_inactive",
			authorization.IdentityID, claims.SessionID, claims.ID, method)
		return Principal{}, ErrInactiveCredential
	}
	now := s.clock().UTC()
	if err := s.store.RecordUse(
		ctx, claims.SessionID, claims.ID, method, now, now.Add(s.sessionIdleTTL),
	); err != nil {
		return Principal{}, ErrUnauthenticated
	}

	return principalFromClaims(
		claims,
		authorization.IdentityID,
		session.Source,
		containsScope(authorization.Services, RootScope),
	), nil
}

func (s *Service) RevokeSession(ctx context.Context, id, reason string) error {
	return s.store.RevokeSession(ctx, id, reason, s.clock().UTC())
}

func (s *Service) RevokeToken(ctx context.Context, id, reason string) error {
	return s.store.RevokeToken(ctx, id, reason, s.clock().UTC())
}

func (s *Service) GrantAuthorization(
	ctx context.Context,
	authorization Authorization,
) (Authorization, error) {
	if hasRole(authorization.Roles, RoleOwner) ||
		containsScope(authorization.Services, RootScope) ||
		authorization.MaxTTL <= 0 || authorization.MaxTTL > s.maximumTokenTTL {
		return Authorization{}, ErrPermissionDenied
	}
	if authorization.Revision == 0 {
		authorization.Revision = 1
	}

	return s.store.PutAuthorization(ctx, authorization)
}

func containsScope(scopes []string, target string) bool {
	for _, scope := range scopes {
		if scope == target {
			return true
		}
	}

	return false
}

func (s *Service) CheckPrincipal(ctx context.Context, principal Principal) error {
	now := s.clock().UTC()
	session, err := s.store.GetSession(ctx, principal.SessionID)
	if err != nil || session.Status != StatusActive ||
		session.IdentityID != principal.IdentityID ||
		session.IdentityGeneration != principal.IdentityGeneration ||
		session.AuthorizationRevision != principal.AuthorizationRevision ||
		!now.Before(session.IdleExpiresAt) || !now.Before(session.AbsoluteExpiresAt) {
		return ErrInactiveCredential
	}
	token, err := s.store.GetToken(ctx, principal.TokenID)
	if err != nil || token.Status != StatusActive ||
		token.SessionID != principal.SessionID ||
		token.IdentityID != principal.IdentityID ||
		token.IdentityGeneration != principal.IdentityGeneration ||
		token.AuthorizationRevision != principal.AuthorizationRevision ||
		!now.Before(token.ExpiresAt) {
		return ErrInactiveCredential
	}

	return nil
}

func (s *Service) KeepAlivePrincipal(ctx context.Context, principal Principal) error {
	if err := s.CheckPrincipal(ctx, principal); err != nil {
		return err
	}
	now := s.clock().UTC()
	return s.store.KeepAliveSession(
		ctx, principal.SessionID, now, now.Add(s.sessionIdleTTL),
	)
}

func (s *Service) PrincipalKeepAliveInterval() time.Duration {
	interval := s.sessionIdleTTL / 3
	if interval > time.Minute {
		return time.Minute
	}
	if interval < 100*time.Millisecond {
		return 100 * time.Millisecond
	}
	return interval
}

func (s *Service) Store() Store {
	return s.store
}

func (s *Service) validateSignedToken(
	ctx context.Context,
	raw string,
) (Authorization, AccessClaims, error) {
	identityID, err := GetUnverifiedIdentityID(raw, s.maximumTokenSize)
	if err != nil {
		return Authorization{}, AccessClaims{}, err
	}
	authorization, err := s.store.GetAuthorization(ctx, identityID)
	if err != nil || authorization.Status != StatusActive {
		return Authorization{}, AccessClaims{}, ErrUnauthenticated
	}
	claims, err := VerifyAccessToken(raw, authorization.PublicKey, VerifyOptions{
		Audience: s.audience,
		Issuer:   authorization.IdentityID,
		Now:      s.clock().UTC(),
		Leeway:   s.leeway,
		MaxSize:  s.maximumTokenSize,
	})
	if err != nil {
		return Authorization{}, AccessClaims{}, err
	}

	return authorization, claims, nil
}

func checkClaimsWithinAuthorization(
	claims AccessClaims,
	authorization Authorization,
	maximumTokenTTL time.Duration,
	now time.Time,
) error {
	if claims.IdentityGeneration != authorization.Generation ||
		claims.AuthorizationRevision != authorization.Revision {
		return ErrAuthorizationChange
	}
	if claims.OwnerID != authorization.OwnerID || claims.Subject != authorization.UserID ||
		!IsSubset(claims.Roles, authorization.Roles) ||
		!ScopesAreSubset(
			claims.Services, claims.Methods,
			authorization.Services, authorization.Methods,
			hasRole(authorization.Roles, RoleOwner),
		) {
		return ErrPermissionDenied
	}
	if claims.ExpiresAt.Sub(claims.IssuedAt.Time) > authorization.MaxTTL ||
		claims.ExpiresAt.Sub(claims.IssuedAt.Time) > maximumTokenTTL ||
		claims.ExpiresAt.Before(now) {
		return ErrPermissionDenied
	}

	return nil
}

func tokenFromClaims(claims AccessClaims, identityID string) Token {
	token := Token{
		ID:                    claims.ID,
		SessionID:             claims.SessionID,
		IdentityID:            identityID,
		OwnerID:               claims.OwnerID,
		UserID:                claims.Subject,
		Roles:                 append([]string(nil), claims.Roles...),
		Services:              append([]string(nil), claims.Services...),
		Methods:               append([]string(nil), claims.Methods...),
		Nonce:                 claims.Nonce,
		IssuedAt:              claims.IssuedAt.Time,
		NotBefore:             claims.NotBefore.Time,
		ExpiresAt:             claims.ExpiresAt.Time,
		Status:                StatusActive,
		IdentityGeneration:    claims.IdentityGeneration,
		AuthorizationRevision: claims.AuthorizationRevision,
	}
	if claims.Confirmation != nil {
		token.CertificateThumbprint = claims.Confirmation.CertificateThumbprint
	}

	return token
}

func principalFromClaims(
	claims AccessClaims,
	identityID, source string,
	rootAuthorization bool,
) Principal {
	principal := Principal{
		IdentityID:            identityID,
		OwnerID:               claims.OwnerID,
		UserID:                claims.Subject,
		Roles:                 append([]string(nil), claims.Roles...),
		RootAuthorization:     rootAuthorization,
		SessionID:             claims.SessionID,
		TokenID:               claims.ID,
		Services:              append([]string(nil), claims.Services...),
		Methods:               append([]string(nil), claims.Methods...),
		Source:                source,
		IdentityGeneration:    claims.IdentityGeneration,
		AuthorizationRevision: claims.AuthorizationRevision,
	}
	if claims.Confirmation != nil {
		principal.CertificateThumbprint = claims.Confirmation.CertificateThumbprint
	}

	return principal
}

func isLiveSession(session Session, claims AccessClaims, now time.Time) bool {
	return session.Status == StatusActive &&
		session.IdentityID == claims.Issuer &&
		session.IdentityGeneration == claims.IdentityGeneration &&
		session.AuthorizationRevision == claims.AuthorizationRevision &&
		now.Before(session.IdleExpiresAt) && now.Before(session.AbsoluteExpiresAt)
}

func isLiveToken(token Token, claims AccessClaims, now time.Time) bool {
	return token.Status == StatusActive &&
		token.SessionID == claims.SessionID &&
		token.IdentityID == claims.Issuer &&
		token.OwnerID == claims.OwnerID &&
		token.UserID == claims.Subject &&
		slices.Equal(token.Roles, claims.Roles) &&
		slices.Equal(token.Services, claims.Services) &&
		slices.Equal(token.Methods, claims.Methods) &&
		token.Nonce == claims.Nonce &&
		token.IssuedAt.Equal(claims.IssuedAt.Time) &&
		token.NotBefore.Equal(claims.NotBefore.Time) &&
		token.ExpiresAt.Equal(claims.ExpiresAt.Time) &&
		token.IdentityGeneration == claims.IdentityGeneration &&
		token.AuthorizationRevision == claims.AuthorizationRevision &&
		now.Before(token.ExpiresAt)
}

func hasRole(roles []string, role string) bool {
	for _, candidate := range roles {
		if candidate == role {
			return true
		}
	}

	return false
}

func (s *Service) auditFailure(
	ctx context.Context,
	eventType, identityID, sessionID, tokenID, reason string,
) {
	now := s.clock().UTC()
	key := strings.Join([]string{
		eventType,
		identityID,
		sessionID,
		tokenID,
		reason,
	}, "\x00")
	shouldAppend, reportSuppression := s.reserveFailureAudit(now, key)
	s.appendFailureAudit(
		ctx,
		now,
		reportSuppression,
		AuditEvent{
			Type: eventType, IdentityID: identityID, SessionID: sessionID,
			TokenID: tokenID, Reason: reason, CreatedAt: now,
		},
		shouldAppend,
	)
}

func (s *Service) auditUnverifiedFailure(
	ctx context.Context,
	eventType, raw, reason string,
) {
	digest := sha256.Sum256([]byte(raw))
	now := s.clock().UTC()
	key := strings.Join([]string{
		eventType,
		reason,
		string(digest[:]),
	}, "\x00")
	shouldAppend, reportSuppression := s.reserveFailureAudit(now, key)
	s.appendFailureAudit(
		ctx,
		now,
		reportSuppression,
		AuditEvent{Type: eventType, Reason: reason, CreatedAt: now},
		shouldAppend,
	)
}

func (s *Service) appendFailureAudit(
	ctx context.Context,
	now time.Time,
	reportSuppression bool,
	event AuditEvent,
	shouldAppend bool,
) {
	if reportSuppression {
		_ = s.store.AppendAudit(ctx, AuditEvent{
			Type:      "authentication_audit_rate_limited",
			Reason:    "additional authentication failures suppressed",
			CreatedAt: now,
		})
	}
	if !shouldAppend {
		return
	}
	_ = s.store.AppendAudit(ctx, event)
}

func (s *Service) reserveFailureAudit(now time.Time, key string) (bool, bool) {
	s.auditMu.Lock()
	defer s.auditMu.Unlock()

	s.evictStaleAuditKeys(now)
	if s.auditWindowStart.IsZero() || now.Before(s.auditWindowStart) ||
		now.Sub(s.auditWindowStart) >= auditCoalesceWindow {
		s.auditWindowStart = now
		s.failureAudits = 0
		s.suppressionAudit = false
	}
	if last := s.auditLast[key]; !last.IsZero() &&
		now.Sub(last) < auditCoalesceWindow {
		return false, false
	}
	if s.failureAudits >= maximumFailureAuditsPerWindow-1 {
		if !s.suppressionAudit {
			s.suppressionAudit = true
			return false, true
		}
		return false, false
	}
	s.auditLast[key] = now
	s.failureAudits++

	return true, false
}

func (s *Service) evictStaleAuditKeys(now time.Time) {
	for key, last := range s.auditLast {
		if now.Before(last) || now.Sub(last) >= auditCoalesceWindow {
			delete(s.auditLast, key)
		}
	}
}
