package auth_test

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"

	morphauth "github.com/wandxy/morph/internal/auth"
)

func TestAccessToken_SignsAndVerifiesStrictClaims(t *testing.T) {
	identity, err := morphauth.GenerateIdentity(2)
	require.NoError(t, err)
	now := time.Now().UTC()
	raw, signedClaims, err := morphauth.SignAccessToken(identity, morphauth.TokenRequest{
		Audience:              "morph-rpc:test",
		Subject:               "user",
		SessionID:             "session",
		OwnerID:               "owner",
		Roles:                 []string{morphauth.RoleOwner},
		Services:              []string{morphauth.RootScope},
		TTL:                   time.Hour,
		NotBefore:             now.Add(-time.Second),
		AuthorizationRevision: 4,
		CertificateThumbprint: "certificate",
	})
	require.NoError(t, err)

	identityID, err := morphauth.GetUnverifiedIdentityID(raw, 0)
	require.NoError(t, err)
	require.Equal(t, identity.ID, identityID)

	claims, err := morphauth.VerifyAccessToken(raw, identity.PublicKey, morphauth.VerifyOptions{
		Audience: "morph-rpc:test",
		Issuer:   identity.ID,
		Now:      now,
	})
	require.NoError(t, err)
	require.Equal(t, signedClaims.ID, claims.ID)
	require.Equal(t, "session", claims.SessionID)
	require.Equal(t, "rpc", claims.Source)
	require.Equal(t, "certificate", claims.Confirmation.CertificateThumbprint)
}

func TestAccessToken_RejectsWrongAudienceAndAlgorithm(t *testing.T) {
	identity, err := morphauth.GenerateIdentity(1)
	require.NoError(t, err)
	raw, _, err := morphauth.SignAccessToken(identity, validTokenRequest())
	require.NoError(t, err)

	_, err = morphauth.VerifyAccessToken(raw, identity.PublicKey, morphauth.VerifyOptions{
		Audience: "wrong",
		Issuer:   identity.ID,
	})
	require.EqualError(t, err, "access token is invalid")

	claims := jwt.MapClaims{
		"iss": identity.ID, "sub": "user", "aud": "morph-rpc:test",
		"iat": time.Now().Add(-time.Minute).Unix(), "nbf": time.Now().Add(-time.Minute).Unix(),
		"exp": time.Now().Add(time.Hour).Unix(), "jti": "token",
	}
	unsigned := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	unsigned.Header["typ"] = morphauth.TokenType
	unsigned.Header["kid"] = identity.ID
	raw, err = unsigned.SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	_, err = morphauth.VerifyAccessToken(raw, identity.PublicKey, morphauth.VerifyOptions{
		Audience: "morph-rpc:test",
		Issuer:   identity.ID,
	})
	require.EqualError(t, err, "access token is invalid")
}

func TestAccessToken_ValidatesRequiredInputs(t *testing.T) {
	identity, err := morphauth.GenerateIdentity(1)
	require.NoError(t, err)

	_, _, err = morphauth.SignAccessToken(identity, morphauth.TokenRequest{})
	require.EqualError(t, err, "token TTL must be greater than zero")

	request := validTokenRequest()
	request.NonceBytes = 8
	_, _, err = morphauth.SignAccessToken(identity, request)
	require.EqualError(t, err, "token nonce bytes must be between 16 and 64")

	request = validTokenRequest()
	request.Roles = []string{morphauth.RoleOperator}
	_, _, err = morphauth.SignAccessToken(identity, request)
	require.EqualError(t, err, "access token service scope is invalid")

	request = validTokenRequest()
	request.Source = "browser"
	_, _, err = morphauth.SignAccessToken(identity, request)
	require.EqualError(t, err, "token source is invalid")

	_, err = morphauth.GetUnverifiedIdentityID("malformed", 10)
	require.EqualError(t, err, "access token is malformed")
}

func TestScope_UsesStructuralExactMatching(t *testing.T) {
	require.True(t, morphauth.AllowsMethod(nil, []string{"/morph.v1.SessionService/List"},
		"/morph.v1.SessionService/List", false))
	require.False(t, morphauth.AllowsMethod(nil, []string{"/morph.v1.SessionService/List"},
		"/morph.v1.SessionService/ListAll", false))
	require.True(t, morphauth.AllowsMethod([]string{"/morph.v1.SessionService"}, nil,
		"/morph.v1.SessionService/Archive", false))
	require.False(t, morphauth.AllowsMethod([]string{morphauth.RootScope}, nil,
		"/morph.v1.SessionService/List", false))
	require.True(t, morphauth.AllowsMethod([]string{morphauth.RootScope}, nil,
		"/morph.v1.SessionService/List", true))
	require.True(t, morphauth.ScopesAreSubset(
		[]string{"/morph.v1.SessionService"}, nil,
		[]string{morphauth.RootScope}, nil, true,
	))
	require.False(t, morphauth.ScopesAreSubset(
		[]string{"/morph.v1.SessionService"}, nil,
		[]string{morphauth.RootScope}, nil, false,
	))
}

func validTokenRequest() morphauth.TokenRequest {
	return morphauth.TokenRequest{
		Audience:              "morph-rpc:test",
		Subject:               "user",
		SessionID:             "session",
		OwnerID:               "owner",
		Roles:                 []string{morphauth.RoleOwner},
		Services:              []string{morphauth.RootScope},
		TTL:                   time.Hour,
		NotBefore:             time.Now().Add(-time.Second),
		AuthorizationRevision: 1,
	}
}
