package client

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"

	morphauth "github.com/wandxy/morph/internal/auth"
	"github.com/wandxy/morph/internal/credential"
	"github.com/wandxy/morph/internal/permissions"
	"github.com/wandxy/morph/internal/profile"
)

func TestAuthResolver_UsesExplicitThenStoredTokenPrecedence(t *testing.T) {
	home := setResolverProfile(t)
	store := credential.NewFileStore(filepath.Join(home, "auth.json"))
	_, err := store.LoadOrCreateIdentity()
	require.NoError(t, err)
	record, found, err := store.LoadMorphAuth()
	require.NoError(t, err)
	require.True(t, found)
	record.Token = "stored-token"
	require.NoError(t, store.SetMorphAuth(record))

	resolver := newAuthResolver(Options{AuthToken: "explicit-token"})
	token, err := resolver.getToken()
	require.NoError(t, err)
	require.Equal(t, "explicit-token", token)
	require.True(t, resolver.explicit)

	resolver = newAuthResolver(Options{})
	token, err = resolver.getToken()
	require.NoError(t, err)
	require.Equal(t, "stored-token", token)
	require.True(t, resolver.explicit)
}

func TestAuthResolver_InvalidExplicitKeyDoesNotFallBack(t *testing.T) {
	home := setResolverProfile(t)
	_, err := credential.NewFileStore(
		filepath.Join(home, "auth.json"),
	).LoadOrCreateIdentity()
	require.NoError(t, err)

	resolver := newAuthResolver(Options{AuthKey: []byte(filepath.Join(home, "missing-key.pem"))})
	_, err = resolver.getToken()
	require.Error(t, err)
}

func TestAuthResolver_AutomaticTokenUsesRequestedScopesAndRenewsTUI(t *testing.T) {
	home := setResolverProfile(t)
	store := credential.NewFileStore(filepath.Join(home, "auth.json"))
	identity, err := store.LoadOrCreateIdentity()
	require.NoError(t, err)
	resolver := newAuthResolver(Options{
		PermissionSurface: permissions.SurfaceTUI,
		AuthAudience:      "morph-rpc:remote",
		AuthTokenTTL:      time.Hour,
		AuthMethods:       []string{"/morph.v1.SessionService/List"},
	})

	first, err := resolver.getToken()
	require.NoError(t, err)
	claims, err := morphauth.VerifyAccessToken(first, identity.PublicKey, morphauth.VerifyOptions{
		Audience: "morph-rpc:remote", Issuer: identity.ID,
	})
	require.NoError(t, err)
	require.Equal(t, "remote", claims.OwnerID)
	require.Equal(t, "tui", claims.Source)
	require.Contains(t, claims.Methods, "/morph.v1.SessionService/List")
	require.Contains(t, claims.Methods, openSessionMethod)
	require.NotContains(t, claims.Methods, revokeSessionMethod)
	require.NotContains(t, claims.Services, morphauth.RootScope)

	resolver.active.Store(true)
	resolver.tokenMu.Lock()
	resolver.claims.ExpiresAt = jwt.NewNumericDate(time.Now())
	resolver.tokenMu.Unlock()
	second, err := resolver.getToken()
	require.NoError(t, err)
	require.NotEqual(t, first, second)
	require.Equal(t, claims.SessionID, resolver.claims.SessionID)
	require.False(t, resolver.active.Load())

	record, found, err := store.LoadMorphAuth()
	require.NoError(t, err)
	require.True(t, found)
	require.Empty(t, record.Token)
}

func TestAuthResolver_RefreshesAutomaticSessionBeforeIdleExpiry(t *testing.T) {
	home := setResolverProfile(t)
	store := credential.NewFileStore(filepath.Join(home, "auth.json"))
	identity, err := store.LoadOrCreateIdentity()
	require.NoError(t, err)
	resolver := newAuthResolver(Options{
		PermissionSurface:  permissions.SurfaceCLI,
		AuthAudience:       "morph-rpc:test",
		AuthTokenTTL:       time.Hour,
		AuthSessionIdleTTL: time.Minute,
		AuthMethods:        []string{"/morph.v1.SessionService/List"},
	})

	first, err := resolver.getToken()
	require.NoError(t, err)
	firstClaims, err := morphauth.VerifyAccessToken(first, identity.PublicKey, morphauth.VerifyOptions{
		Audience: "morph-rpc:test", Issuer: identity.ID,
	})
	require.NoError(t, err)

	resolver.active.Store(true)
	resolver.tokenMu.Lock()
	resolver.lastUse = time.Now().Add(-time.Minute)
	resolver.tokenMu.Unlock()
	second, err := resolver.getToken()
	require.NoError(t, err)
	secondClaims, err := morphauth.VerifyAccessToken(second, identity.PublicKey, morphauth.VerifyOptions{
		Audience: "morph-rpc:test", Issuer: identity.ID,
	})
	require.NoError(t, err)
	require.NotEqual(t, firstClaims.SessionID, secondClaims.SessionID)
	require.False(t, resolver.active.Load())
}

func setResolverProfile(t *testing.T) string {
	t.Helper()
	original := profile.Active()
	home := t.TempDir()
	profile.SetActive(profile.WithMetadataPaths(profile.Profile{Name: "test", HomeDir: home}))
	t.Cleanup(func() { profile.SetActive(original) })

	return home
}
