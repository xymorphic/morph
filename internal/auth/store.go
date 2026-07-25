package auth

import (
	"context"
	"crypto/ed25519"
	"errors"
	"time"
)

const (
	RoleOwner    = "owner"
	RoleOperator = "operator"

	StatusActive  = "active"
	StatusRevoked = "revoked"
	StatusExpired = "expired"
)

var (
	ErrNotFound            = errors.New("auth record not found")
	ErrUnauthenticated     = errors.New("RPC authentication failed")
	ErrPermissionDenied    = errors.New("RPC method is not authorized")
	ErrInactiveCredential  = errors.New("auth session or token is inactive")
	ErrAuthorizationChange = errors.New("identity authorization changed")
)

type Authorization struct {
	IdentityID     string
	PublicKey      ed25519.PublicKey
	OwnerID        string
	UserID         string
	Roles          []string
	Services       []string
	Methods        []string
	MaxTTL         time.Duration
	Generation     uint64
	Revision       uint64
	Status         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	RevokedAt      *time.Time
	RevocationNote string
}

type Session struct {
	ID                    string
	IdentityID            string
	OwnerID               string
	UserID                string
	Roles                 []string
	Source                string
	Status                string
	CreatedAt             time.Time
	LastSeenAt            time.Time
	IdleExpiresAt         time.Time
	AbsoluteExpiresAt     time.Time
	IdentityGeneration    uint64
	AuthorizationRevision uint64
	RevokedAt             *time.Time
	RevocationNote        string
}

type Token struct {
	ID                    string
	SessionID             string
	IdentityID            string
	OwnerID               string
	UserID                string
	Roles                 []string
	Services              []string
	Methods               []string
	Nonce                 string
	IssuedAt              time.Time
	NotBefore             time.Time
	ExpiresAt             time.Time
	LastUsedAt            *time.Time
	UseCount              uint64
	MethodUse             map[string]MethodUse
	Status                string
	IdentityGeneration    uint64
	AuthorizationRevision uint64
	CertificateThumbprint string
	RevokedAt             *time.Time
	RevocationNote        string
}

type MethodUse struct {
	Count       uint64
	FirstUsedAt time.Time
	LastUsedAt  time.Time
}

type AuditEvent struct {
	ID         string
	Type       string
	IdentityID string
	SessionID  string
	TokenID    string
	Method     string
	Reason     string
	CreatedAt  time.Time
}

type Principal struct {
	IdentityID            string
	OwnerID               string
	UserID                string
	Roles                 []string
	SessionID             string
	TokenID               string
	Services              []string
	Methods               []string
	Source                string
	IdentityGeneration    uint64
	AuthorizationRevision uint64
	CertificateThumbprint string
}

func (p Principal) HasRole(role string) bool {
	for _, candidate := range p.Roles {
		if candidate == role {
			return true
		}
	}

	return false
}

type Store interface {
	SeedRoot(context.Context, Authorization) (Authorization, error)
	GetAuthorization(context.Context, string) (Authorization, error)
	PutAuthorization(context.Context, Authorization) (Authorization, error)
	RotateIdentity(context.Context, string, Authorization, time.Time) error
	ListAuthorizations(context.Context) ([]Authorization, error)
	Activate(context.Context, Session, Token) error
	GetSession(context.Context, string) (Session, error)
	ListSessions(context.Context) ([]Session, error)
	GetToken(context.Context, string) (Token, error)
	ListTokens(context.Context) ([]Token, error)
	RecordUse(context.Context, string, string, string, time.Time, time.Time) error
	KeepAliveSession(context.Context, string, time.Time, time.Time) error
	RevokeSession(context.Context, string, string, time.Time) error
	RevokeToken(context.Context, string, string, time.Time) error
	AppendAudit(context.Context, AuditEvent) error
	ListAudit(context.Context, int) ([]AuditEvent, error)
	Prune(context.Context, time.Time, int) (int, error)
	Close() error
}
