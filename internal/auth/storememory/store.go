package storememory

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	morphauth "github.com/wandxy/morph/internal/auth"
)

const maximumAuditEvents = 10000

type Store struct {
	mu             sync.RWMutex
	authorizations map[string]morphauth.Authorization
	sessions       map[string]morphauth.Session
	tokens         map[string]morphauth.Token
	audit          []morphauth.AuditEvent
}

type Snapshot struct {
	Authorizations map[string]morphauth.Authorization `json:"authorizations"`
	Sessions       map[string]morphauth.Session       `json:"sessions"`
	Tokens         map[string]morphauth.Token         `json:"tokens"`
	Audit          []morphauth.AuditEvent             `json:"audit"`
}

func New() *Store {
	return &Store{
		authorizations: make(map[string]morphauth.Authorization),
		sessions:       make(map[string]morphauth.Session),
		tokens:         make(map[string]morphauth.Token),
	}
}

func NewFromSnapshot(snapshot Snapshot) *Store {
	store := New()
	for id, authorization := range snapshot.Authorizations {
		store.authorizations[id] = cloneAuthorization(authorization)
	}
	for id, session := range snapshot.Sessions {
		store.sessions[id] = cloneSession(session)
	}
	for id, token := range snapshot.Tokens {
		store.tokens[id] = cloneToken(token)
	}
	store.audit = append([]morphauth.AuditEvent(nil), snapshot.Audit...)

	return store
}

func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot := Snapshot{
		Authorizations: make(map[string]morphauth.Authorization, len(s.authorizations)),
		Sessions:       make(map[string]morphauth.Session, len(s.sessions)),
		Tokens:         make(map[string]morphauth.Token, len(s.tokens)),
		Audit:          append([]morphauth.AuditEvent(nil), s.audit...),
	}
	for id, authorization := range s.authorizations {
		snapshot.Authorizations[id] = cloneAuthorization(authorization)
	}
	for id, session := range s.sessions {
		snapshot.Sessions[id] = cloneSession(session)
	}
	for id, token := range s.tokens {
		snapshot.Tokens[id] = cloneToken(token)
	}

	return snapshot
}

func (s *Store) SeedRoot(
	_ context.Context,
	authorization morphauth.Authorization,
) (morphauth.Authorization, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current, ok := s.authorizations[authorization.IdentityID]; ok {
		if current.Status != morphauth.StatusActive ||
			!current.PublicKey.Equal(authorization.PublicKey) ||
			current.Generation != authorization.Generation ||
			!hasRootScope(current.Services) {
			return morphauth.Authorization{}, morphauth.ErrPermissionDenied
		}
		return cloneAuthorization(current), nil
	}
	for _, current := range s.authorizations {
		if current.Status == morphauth.StatusActive &&
			hasRootScope(current.Services) {
			return morphauth.Authorization{}, morphauth.ErrPermissionDenied
		}
	}
	authorization = cloneAuthorization(authorization)
	s.authorizations[authorization.IdentityID] = authorization
	s.appendAuditLocked(morphauth.AuditEvent{
		Type: "root_authorization_seeded", IdentityID: authorization.IdentityID,
		CreatedAt: authorization.CreatedAt,
	})

	return cloneAuthorization(authorization), nil
}

func (s *Store) GetAuthorization(
	_ context.Context,
	identityID string,
) (morphauth.Authorization, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	authorization, ok := s.authorizations[identityID]
	if !ok {
		return morphauth.Authorization{}, morphauth.ErrNotFound
	}

	return cloneAuthorization(authorization), nil
}

func (s *Store) PutAuthorization(
	_ context.Context,
	authorization morphauth.Authorization,
) (morphauth.Authorization, error) {
	if authorization.IdentityID == "" || len(authorization.PublicKey) != ed25519.PublicKeySize {
		return morphauth.Authorization{}, errors.New("valid identity authorization is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if current, ok := s.authorizations[authorization.IdentityID]; ok {
		if authorization.Revision <= current.Revision {
			authorization.Revision = current.Revision + 1
		}
		if authorization.CreatedAt.IsZero() {
			authorization.CreatedAt = current.CreatedAt
		}
		for id, session := range s.sessions {
			if session.IdentityID != authorization.IdentityID ||
				session.AuthorizationRevision == authorization.Revision {
				continue
			}
			now := authorization.UpdatedAt
			session.Status = morphauth.StatusRevoked
			session.RevokedAt = &now
			session.RevocationNote = "authorization changed"
			s.sessions[id] = session
			revokeSessionTokens(s.tokens, id, "authorization changed", now)
		}
	}
	authorization = cloneAuthorization(authorization)
	s.authorizations[authorization.IdentityID] = authorization
	eventType := "authorization_granted"
	if authorization.Status != morphauth.StatusActive {
		eventType = "authorization_revoked"
	}
	s.appendAuditLocked(morphauth.AuditEvent{
		Type: eventType, IdentityID: authorization.IdentityID,
		Reason: authorization.RevocationNote, CreatedAt: authorization.UpdatedAt,
	})

	return cloneAuthorization(authorization), nil
}

func (s *Store) RotateIdentity(
	_ context.Context,
	currentIdentityID string,
	next morphauth.Authorization,
	now time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.authorizations[currentIdentityID]
	if !ok || current.Status != morphauth.StatusActive ||
		next.IdentityID == "" || next.IdentityID == currentIdentityID ||
		len(next.PublicKey) != ed25519.PublicKeySize ||
		next.Generation <= current.Generation {
		return morphauth.ErrPermissionDenied
	}
	if _, exists := s.authorizations[next.IdentityID]; exists {
		return morphauth.ErrPermissionDenied
	}
	current.Status = morphauth.StatusRevoked
	current.RevokedAt = &now
	current.RevocationNote = "identity rotated"
	current.UpdatedAt = now
	current.Revision++
	s.authorizations[currentIdentityID] = cloneAuthorization(current)
	s.authorizations[next.IdentityID] = cloneAuthorization(next)
	for id, session := range s.sessions {
		if session.IdentityID != currentIdentityID {
			continue
		}
		session.Status = morphauth.StatusRevoked
		session.RevokedAt = &now
		session.RevocationNote = "identity rotated"
		s.sessions[id] = session
		revokeSessionTokens(s.tokens, id, "identity rotated", now)
	}
	s.appendAuditLocked(morphauth.AuditEvent{
		Type: "identity_rotated", IdentityID: next.IdentityID,
		Reason: currentIdentityID, CreatedAt: now,
	})

	return nil
}

func (s *Store) ListAuthorizations(_ context.Context) ([]morphauth.Authorization, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]morphauth.Authorization, 0, len(s.authorizations))
	for _, authorization := range s.authorizations {
		result = append(result, cloneAuthorization(authorization))
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].IdentityID < result[j].IdentityID
	})

	return result, nil
}

func (s *Store) Activate(
	_ context.Context,
	session morphauth.Session,
	token morphauth.Token,
) error {
	if session.ID == "" || token.ID == "" || token.SessionID != session.ID ||
		session.IdentityID == "" || token.IdentityID != session.IdentityID {
		return errors.New("valid auth session and token are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	openedSession := true
	if existing, ok := s.sessions[session.ID]; ok {
		openedSession = false
		if existing.Status != morphauth.StatusActive ||
			existing.IdentityID != session.IdentityID ||
			existing.OwnerID != session.OwnerID ||
			existing.UserID != session.UserID ||
			!slices.Equal(existing.Roles, session.Roles) ||
			existing.Source != session.Source ||
			existing.IdentityGeneration != session.IdentityGeneration ||
			existing.AuthorizationRevision != session.AuthorizationRevision ||
			!session.CreatedAt.Before(existing.IdleExpiresAt) ||
			!session.CreatedAt.Before(existing.AbsoluteExpiresAt) {
			return morphauth.ErrInactiveCredential
		}
		if existingToken, tokenOK := s.tokens[token.ID]; tokenOK {
			if !isSameActiveToken(existingToken, token) {
				return morphauth.ErrInactiveCredential
			}
			return nil
		}
		existing.LastSeenAt = session.LastSeenAt
		existing.IdleExpiresAt = session.IdleExpiresAt
		if existing.IdleExpiresAt.After(existing.AbsoluteExpiresAt) {
			existing.IdleExpiresAt = existing.AbsoluteExpiresAt
		}
		s.sessions[session.ID] = existing
		session = existing
	}
	if existing, ok := s.tokens[token.ID]; ok {
		if !isSameActiveToken(existing, token) {
			return morphauth.ErrInactiveCredential
		}
		return nil
	}
	if token.Nonce != "" {
		for _, existing := range s.tokens {
			if existing.ID != token.ID &&
				existing.IdentityID == token.IdentityID &&
				existing.Nonce == token.Nonce {
				return morphauth.ErrInactiveCredential
			}
		}
	}
	s.sessions[session.ID] = cloneSession(session)
	s.tokens[token.ID] = cloneToken(token)
	if openedSession {
		s.appendAuditLocked(morphauth.AuditEvent{
			Type: "session_opened", IdentityID: session.IdentityID,
			SessionID: session.ID, TokenID: token.ID, CreatedAt: session.CreatedAt,
		})
	}
	s.appendAuditLocked(morphauth.AuditEvent{
		Type: "token_activated", IdentityID: token.IdentityID,
		SessionID: session.ID, TokenID: token.ID, CreatedAt: token.IssuedAt,
	})

	return nil
}

func (s *Store) GetSession(_ context.Context, id string) (morphauth.Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[id]
	if !ok {
		return morphauth.Session{}, morphauth.ErrNotFound
	}

	return cloneSession(session), nil
}

func (s *Store) ListSessions(_ context.Context) ([]morphauth.Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]morphauth.Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		result = append(result, cloneSession(session))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})

	return result, nil
}

func (s *Store) GetToken(_ context.Context, id string) (morphauth.Token, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	token, ok := s.tokens[id]
	if !ok {
		return morphauth.Token{}, morphauth.ErrNotFound
	}

	return cloneToken(token), nil
}

func (s *Store) ListTokens(_ context.Context) ([]morphauth.Token, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]morphauth.Token, 0, len(s.tokens))
	for _, token := range s.tokens {
		result = append(result, cloneToken(token))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].IssuedAt.Equal(result[j].IssuedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].IssuedAt.After(result[j].IssuedAt)
	})

	return result, nil
}

func (s *Store) RecordUse(
	_ context.Context,
	sessionID, tokenID, method string,
	now, idleExpiresAt time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[sessionID]
	if !ok || session.Status != morphauth.StatusActive {
		return morphauth.ErrInactiveCredential
	}
	token, ok := s.tokens[tokenID]
	if !ok || token.Status != morphauth.StatusActive || token.SessionID != sessionID {
		return morphauth.ErrInactiveCredential
	}
	session.LastSeenAt = now
	if idleExpiresAt.After(session.AbsoluteExpiresAt) {
		idleExpiresAt = session.AbsoluteExpiresAt
	}
	session.IdleExpiresAt = idleExpiresAt
	s.sessions[sessionID] = session
	token.LastUsedAt = &now
	token.UseCount++
	if token.MethodUse == nil {
		token.MethodUse = make(map[string]morphauth.MethodUse)
	}
	methodUse := token.MethodUse[method]
	if methodUse.Count == 0 {
		methodUse.FirstUsedAt = now
	}
	methodUse.Count++
	methodUse.LastUsedAt = now
	token.MethodUse[method] = methodUse
	s.tokens[tokenID] = token

	return nil
}

func (s *Store) KeepAliveSession(
	_ context.Context,
	sessionID string,
	now, idleExpiresAt time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[sessionID]
	if !ok || session.Status != morphauth.StatusActive ||
		!now.Before(session.IdleExpiresAt) ||
		!now.Before(session.AbsoluteExpiresAt) {
		return morphauth.ErrInactiveCredential
	}
	session.LastSeenAt = now
	if idleExpiresAt.After(session.AbsoluteExpiresAt) {
		idleExpiresAt = session.AbsoluteExpiresAt
	}
	session.IdleExpiresAt = idleExpiresAt
	s.sessions[sessionID] = session

	return nil
}

func (s *Store) RevokeSession(
	_ context.Context,
	id, reason string,
	now time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	if !ok {
		return morphauth.ErrNotFound
	}
	if session.Status == morphauth.StatusRevoked {
		return nil
	}
	session.Status = morphauth.StatusRevoked
	session.RevokedAt = &now
	session.RevocationNote = reason
	s.sessions[id] = session
	revokeSessionTokens(s.tokens, id, reason, now)
	s.appendAuditLocked(morphauth.AuditEvent{
		Type: "session_revoked", IdentityID: session.IdentityID,
		SessionID: id, Reason: reason, CreatedAt: now,
	})

	return nil
}

func (s *Store) RevokeToken(
	_ context.Context,
	id, reason string,
	now time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	token, ok := s.tokens[id]
	if !ok {
		return morphauth.ErrNotFound
	}
	if token.Status == morphauth.StatusRevoked {
		return nil
	}
	token.Status = morphauth.StatusRevoked
	token.RevokedAt = &now
	token.RevocationNote = reason
	s.tokens[id] = token
	s.appendAuditLocked(morphauth.AuditEvent{
		Type: "token_revoked", IdentityID: token.IdentityID,
		SessionID: token.SessionID, TokenID: id, Reason: reason, CreatedAt: now,
	})

	return nil
}

func (s *Store) AppendAudit(_ context.Context, event morphauth.AuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendAuditLocked(event)

	return nil
}

func (s *Store) ListAudit(_ context.Context, limit int) ([]morphauth.AuditEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > len(s.audit) {
		limit = len(s.audit)
	}
	result := make([]morphauth.AuditEvent, 0, limit)
	for index := len(s.audit) - 1; index >= len(s.audit)-limit; index-- {
		result = append(result, s.audit[index])
	}

	return result, nil
}

func (s *Store) Prune(ctx context.Context, before time.Time, limit int) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		return 0, nil
	}

	budgets := [3]int{limit / 3, limit / 3, limit / 3}
	for index := 0; index < limit%len(budgets); index++ {
		budgets[index]++
	}
	pruned := s.pruneTokens(before, budgets[0])
	pruned += s.pruneSessions(before, budgets[1])
	pruned += s.pruneAudit(before, budgets[2])
	for pruned < limit {
		remaining := limit - pruned
		additional := s.pruneTokens(before, remaining)
		remaining -= additional
		additional += s.pruneSessions(before, remaining)
		remaining = limit - pruned - additional
		additional += s.pruneAudit(before, remaining)
		if additional == 0 {
			break
		}
		pruned += additional
	}

	return pruned, nil
}

func (s *Store) pruneTokens(before time.Time, limit int) int {
	pruned := 0
	for id, token := range s.tokens {
		if pruned >= limit {
			break
		}
		if !token.ExpiresAt.Before(before) {
			continue
		}
		delete(s.tokens, id)
		pruned++
	}

	return pruned
}

func (s *Store) pruneSessions(before time.Time, limit int) int {
	pruned := 0
	for id, session := range s.sessions {
		if pruned >= limit {
			break
		}
		if !session.AbsoluteExpiresAt.Before(before) {
			continue
		}
		delete(s.sessions, id)
		pruned++
	}

	return pruned
}

func (s *Store) pruneAudit(before time.Time, limit int) int {
	pruned := 0
	kept := s.audit[:0]
	for _, event := range s.audit {
		if pruned < limit && event.CreatedAt.Before(before) {
			pruned++
			continue
		}
		kept = append(kept, event)
	}
	s.audit = kept

	return pruned
}

func (s *Store) Close() error {
	return nil
}

func revokeSessionTokens(
	tokens map[string]morphauth.Token,
	sessionID, reason string,
	now time.Time,
) {
	for id, token := range tokens {
		if token.SessionID != sessionID || token.Status == morphauth.StatusRevoked {
			continue
		}
		token.Status = morphauth.StatusRevoked
		token.RevokedAt = &now
		token.RevocationNote = reason
		tokens[id] = token
	}
}

func isSameActiveToken(left, right morphauth.Token) bool {
	return left.Status == morphauth.StatusActive &&
		left.ID == right.ID &&
		left.SessionID == right.SessionID &&
		left.IdentityID == right.IdentityID &&
		left.OwnerID == right.OwnerID &&
		left.UserID == right.UserID &&
		slices.Equal(left.Roles, right.Roles) &&
		slices.Equal(left.Services, right.Services) &&
		slices.Equal(left.Methods, right.Methods) &&
		left.Nonce == right.Nonce &&
		left.IssuedAt.Equal(right.IssuedAt) &&
		left.NotBefore.Equal(right.NotBefore) &&
		left.ExpiresAt.Equal(right.ExpiresAt) &&
		left.IdentityGeneration == right.IdentityGeneration &&
		left.AuthorizationRevision == right.AuthorizationRevision &&
		left.CertificateThumbprint == right.CertificateThumbprint
}

func hasRootScope(services []string) bool {
	for _, service := range services {
		if service == morphauth.RootScope {
			return true
		}
	}

	return false
}

func cloneAuthorization(value morphauth.Authorization) morphauth.Authorization {
	value.PublicKey = append(ed25519.PublicKey(nil), value.PublicKey...)
	value.Roles = append([]string(nil), value.Roles...)
	value.Services = append([]string(nil), value.Services...)
	value.Methods = append([]string(nil), value.Methods...)
	return value
}

func cloneSession(value morphauth.Session) morphauth.Session {
	value.Roles = append([]string(nil), value.Roles...)
	return value
}

func cloneToken(value morphauth.Token) morphauth.Token {
	value.Roles = append([]string(nil), value.Roles...)
	value.Services = append([]string(nil), value.Services...)
	value.Methods = append([]string(nil), value.Methods...)
	methodUse := value.MethodUse
	value.MethodUse = make(map[string]morphauth.MethodUse, len(methodUse))
	for method, usage := range methodUse {
		value.MethodUse[method] = usage
	}
	return value
}

func (s *Store) appendAuditLocked(event morphauth.AuditEvent) {
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if event.ID == "" {
		event.ID = fmt.Sprintf("audit-%d-%d", event.CreatedAt.UnixNano(), len(s.audit)+1)
	}
	if len(s.audit) >= maximumAuditEvents {
		copy(s.audit, s.audit[len(s.audit)-maximumAuditEvents+1:])
		s.audit = s.audit[:maximumAuditEvents-1]
	}
	s.audit = append(s.audit, event)
}
