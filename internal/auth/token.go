package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	TokenType               = "at+jwt"
	DefaultMaximumTokenSize = 16 * 1024
	DefaultNonceBytes       = 24
)

type Confirmation struct {
	CertificateThumbprint string `json:"x5t#S256,omitempty"`
}

type AccessClaims struct {
	jwt.RegisteredClaims
	SessionID             string        `json:"sid"`
	Nonce                 string        `json:"nonce"`
	OwnerID               string        `json:"owner_id"`
	Source                string        `json:"source"`
	Roles                 []string      `json:"roles"`
	Services              []string      `json:"services,omitempty"`
	Methods               []string      `json:"methods,omitempty"`
	IdentityGeneration    uint64        `json:"identity_generation"`
	AuthorizationRevision uint64        `json:"authorization_revision"`
	Confirmation          *Confirmation `json:"cnf,omitempty"`
}

type TokenRequest struct {
	Audience              string
	Subject               string
	SessionID             string
	TokenID               string
	OwnerID               string
	Source                string
	Roles                 []string
	Services              []string
	Methods               []string
	TTL                   time.Duration
	NotBefore             time.Time
	NonceBytes            int
	AuthorizationRevision uint64
	CertificateThumbprint string
}

type VerifyOptions struct {
	Audience string
	Issuer   string
	Now      time.Time
	Leeway   time.Duration
	MaxSize  int
}

func SignAccessToken(identity Identity, request TokenRequest) (string, AccessClaims, error) {
	if len(identity.PrivateKey) != ed25519.PrivateKeySize || identity.ID == "" {
		return "", AccessClaims{}, errors.New("valid signing identity is required")
	}
	now := time.Now().UTC()
	if request.NotBefore.IsZero() {
		request.NotBefore = now
	}
	if request.TTL <= 0 {
		return "", AccessClaims{}, errors.New("token TTL must be greater than zero")
	}
	if strings.TrimSpace(request.Audience) == "" || strings.TrimSpace(request.Subject) == "" ||
		strings.TrimSpace(request.SessionID) == "" || strings.TrimSpace(request.OwnerID) == "" {
		return "", AccessClaims{}, errors.New("token audience, subject, session, and owner are required")
	}
	if len(request.Roles) == 0 || len(request.Services) == 0 && len(request.Methods) == 0 {
		return "", AccessClaims{}, errors.New("token roles and RPC scopes are required")
	}
	if request.Source == "" {
		request.Source = "rpc"
	}
	if !isValidSource(request.Source) {
		return "", AccessClaims{}, errors.New("token source is invalid")
	}
	if err := validateTokenAuthorization(request.Roles, request.Services, request.Methods); err != nil {
		return "", AccessClaims{}, err
	}
	if request.TokenID == "" {
		var err error
		request.TokenID, err = randomID(24)
		if err != nil {
			return "", AccessClaims{}, err
		}
	}
	nonceBytes := request.NonceBytes
	if nonceBytes == 0 {
		nonceBytes = DefaultNonceBytes
	}
	if nonceBytes < 16 || nonceBytes > 64 {
		return "", AccessClaims{}, errors.New("token nonce bytes must be between 16 and 64")
	}
	nonce, err := randomID(nonceBytes)
	if err != nil {
		return "", AccessClaims{}, err
	}
	claims := AccessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    identity.ID,
			Subject:   strings.TrimSpace(request.Subject),
			Audience:  jwt.ClaimStrings{strings.TrimSpace(request.Audience)},
			ExpiresAt: jwt.NewNumericDate(request.NotBefore.Add(request.TTL)),
			NotBefore: jwt.NewNumericDate(request.NotBefore),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        request.TokenID,
		},
		SessionID:             strings.TrimSpace(request.SessionID),
		Nonce:                 nonce,
		OwnerID:               strings.TrimSpace(request.OwnerID),
		Source:                request.Source,
		Roles:                 append([]string(nil), request.Roles...),
		Services:              append([]string(nil), request.Services...),
		Methods:               append([]string(nil), request.Methods...),
		IdentityGeneration:    identity.Generation,
		AuthorizationRevision: request.AuthorizationRevision,
	}
	if request.CertificateThumbprint != "" {
		claims.Confirmation = &Confirmation{CertificateThumbprint: request.CertificateThumbprint}
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["typ"] = TokenType
	token.Header["kid"] = identity.ID
	signed, err := token.SignedString(identity.PrivateKey)
	if err != nil {
		return "", AccessClaims{}, fmt.Errorf("sign access token: %w", err)
	}

	return signed, claims, nil
}

func GetUnverifiedIdentityID(raw string, maxSize int) (string, error) {
	if maxSize <= 0 {
		maxSize = DefaultMaximumTokenSize
	}
	if len(raw) == 0 || len(raw) > maxSize {
		return "", errors.New("access token size is invalid")
	}
	token, _, err := jwt.NewParser(jwt.WithStrictDecoding()).ParseUnverified(raw, &AccessClaims{})
	if err != nil {
		return "", errors.New("access token is malformed")
	}
	kid, ok := token.Header["kid"].(string)
	if !ok || strings.TrimSpace(kid) == "" {
		return "", errors.New("access token key ID is required")
	}

	return strings.TrimSpace(kid), nil
}

func VerifyAccessToken(raw string, publicKey ed25519.PublicKey, options VerifyOptions) (AccessClaims, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return AccessClaims{}, errors.New("Ed25519 verification key is required")
	}
	if options.MaxSize <= 0 {
		options.MaxSize = DefaultMaximumTokenSize
	}
	if len(raw) == 0 || len(raw) > options.MaxSize {
		return AccessClaims{}, errors.New("access token size is invalid")
	}
	if options.Now.IsZero() {
		options.Now = time.Now().UTC()
	}
	claims := AccessClaims{}
	parserOptions := []jwt.ParserOption{
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithStrictDecoding(),
		jwt.WithTimeFunc(func() time.Time { return options.Now }),
		jwt.WithLeeway(options.Leeway),
	}
	if options.Audience != "" {
		parserOptions = append(parserOptions, jwt.WithAudience(options.Audience))
	}
	if options.Issuer != "" {
		parserOptions = append(parserOptions, jwt.WithIssuer(options.Issuer))
	}
	token, err := jwt.ParseWithClaims(raw, &claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodEdDSA {
			return nil, errors.New("access token must use EdDSA")
		}
		if typ, ok := token.Header["typ"].(string); !ok || typ != TokenType {
			return nil, errors.New("access token type is invalid")
		}
		kid, ok := token.Header["kid"].(string)
		if !ok || kid == "" {
			return nil, errors.New("access token key ID is required")
		}
		if options.Issuer != "" && kid != options.Issuer {
			return nil, errors.New("access token key ID is invalid")
		}
		if critical, ok := token.Header["crit"]; ok && critical != nil {
			return nil, errors.New("access token critical headers are unsupported")
		}

		return publicKey, nil
	}, parserOptions...)
	if err != nil || token == nil || !token.Valid {
		return AccessClaims{}, errors.New("access token is invalid")
	}
	if err := validateAccessClaims(claims); err != nil {
		return AccessClaims{}, err
	}

	return claims, nil
}

func validateAccessClaims(claims AccessClaims) error {
	if claims.Issuer == "" || claims.Subject == "" || len(claims.Audience) == 0 ||
		claims.ExpiresAt == nil || claims.NotBefore == nil || claims.IssuedAt == nil || claims.ID == "" {
		return errors.New("access token required claims are missing")
	}
	if claims.SessionID == "" || claims.Nonce == "" || claims.OwnerID == "" ||
		!isValidSource(claims.Source) ||
		claims.IdentityGeneration == 0 || claims.AuthorizationRevision == 0 {
		return errors.New("access token Morph claims are missing")
	}
	if len(claims.Roles) == 0 || len(claims.Services) == 0 && len(claims.Methods) == 0 {
		return errors.New("access token authorization claims are missing")
	}
	if err := validateTokenAuthorization(claims.Roles, claims.Services, claims.Methods); err != nil {
		return err
	}
	nonce, err := hex.DecodeString(claims.Nonce)
	if err != nil || len(nonce) < 16 || len(nonce) > 64 {
		return errors.New("access token nonce is invalid")
	}

	return nil
}

func isValidSource(source string) bool {
	return source == "cli" || source == "tui" || source == "rpc"
}

func validateTokenAuthorization(roles, services, methods []string) error {
	owner := hasRole(roles, RoleOwner)
	for _, role := range roles {
		if role != RoleOwner && role != RoleOperator {
			return errors.New("access token role is invalid")
		}
	}
	for _, service := range services {
		normalized, err := NormalizeService(service)
		if err != nil || normalized == RootScope && !owner {
			return errors.New("access token service scope is invalid")
		}
	}
	for _, method := range methods {
		if _, err := NormalizeMethod(method); err != nil {
			return errors.New("access token method scope is invalid")
		}
	}

	return nil
}

func randomID(size int) (string, error) {
	body := make([]byte, size)
	if _, err := rand.Read(body); err != nil {
		return "", fmt.Errorf("generate random identifier: %w", err)
	}

	return hex.EncodeToString(body), nil
}
