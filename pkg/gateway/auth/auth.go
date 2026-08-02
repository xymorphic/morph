package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"strings"

	"github.com/xymorphic/morph/pkg/str"
)

var (
	ErrBearerTokenMissing = errors.New("bearer token is required")
	ErrBearerTokenInvalid = errors.New("bearer token is invalid")
)

func CheckBearer(header string, token string) error {
	tokenValue := str.String(token)
	token = tokenValue.Trim()
	if token == "" {
		return nil
	}

	actual, ok := bearerTokenFromHeader(header)
	if !ok {
		return ErrBearerTokenMissing
	}
	actualHash := sha256.Sum256([]byte(actual))
	tokenHash := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(actualHash[:], tokenHash[:]) != 1 {
		return ErrBearerTokenInvalid
	}

	return nil
}

func bearerTokenFromHeader(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}

	return parts[1], true
}
