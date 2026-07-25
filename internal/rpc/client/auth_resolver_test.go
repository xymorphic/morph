package client

import (
	"context"
	"encoding/hex"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"

	morphauth "github.com/wandxy/morph/internal/auth"
	"github.com/wandxy/morph/internal/credential"
	"github.com/wandxy/morph/internal/permissions"
	"github.com/wandxy/morph/internal/profile"
	morphpb "github.com/wandxy/morph/internal/rpc/proto"
	"google.golang.org/grpc"
)

func TestRandomClientID_UsesHexEncoding(t *testing.T) {
	id, err := randomClientID()
	require.NoError(t, err)
	require.Len(t, id, 48)
	_, err = hex.DecodeString(id)
	require.NoError(t, err)
}

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

func TestAuthResolver_DefaultAutomaticTokenUsesRootScope(t *testing.T) {
	home := setResolverProfile(t)
	store := credential.NewFileStore(filepath.Join(home, "auth.json"))
	identity, err := store.LoadOrCreateIdentity()
	require.NoError(t, err)
	resolver := newAuthResolver(Options{AuthAudience: "morph-rpc:test"})

	raw, err := resolver.getToken()
	require.NoError(t, err)
	claims, err := morphauth.VerifyAccessToken(raw, identity.PublicKey, morphauth.VerifyOptions{
		Audience: "morph-rpc:test", Issuer: identity.ID,
	})
	require.NoError(t, err)
	require.Equal(t, []string{morphauth.RootScope}, claims.Services)
	require.Empty(t, claims.Methods)
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
	require.Contains(t, claims.Methods, closeSessionMethod)
	require.NotContains(t, claims.Methods, "/morph.v1.AuthService/RevokeSession")
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

func TestAuthResolver_CloseRevokesAutomaticSession(t *testing.T) {
	setResolverProfile(t)
	resolver := newAuthResolver(Options{
		PermissionSurface:  permissions.SurfaceCLI,
		AuthAudience:       "morph-rpc:test",
		AuthSessionIdleTTL: time.Minute,
		AuthMethods:        []string{"/morph.v1.SessionService/List"},
	})
	token, err := resolver.getToken()
	require.NoError(t, err)
	resolver.active.Store(true)
	resolver.tokenMu.Lock()
	resolver.lastUse = time.Now().Add(-time.Minute)
	resolver.tokenMu.Unlock()
	var closed bool
	resolver.authClient = &authServiceClientStub{
		closeSession: func(
			_ context.Context,
			_ *morphpb.CloseAuthSessionRequest,
		) (*morphpb.CloseAuthSessionResponse, error) {
			resolved, resolveErr := resolver.getToken()
			require.NoError(t, resolveErr)
			require.Equal(t, token, resolved)
			closed = true
			return &morphpb.CloseAuthSessionResponse{}, nil
		},
	}

	require.NoError(t, resolver.close(context.Background()))
	require.True(t, closed)
	require.False(t, resolver.active.Load())
}

func TestAuthResolver_CloseWaitsForInFlightActivation(t *testing.T) {
	resolver := newAuthResolver(Options{
		AuthMethods: []string{"/morph.v1.SessionService/List"},
	})
	resolver.tokenResolved = true
	resolver.token = "automatic-token"
	resolver.claims.SessionID = "automatic-session"
	openStarted := make(chan struct{})
	releaseOpen := make(chan struct{})
	closed := make(chan struct{})
	resolver.authClient = &authServiceClientStub{
		openSession: func(
			context.Context,
			*morphpb.OpenAuthSessionRequest,
		) (*morphpb.OpenAuthSessionResponse, error) {
			close(openStarted)
			<-releaseOpen
			return &morphpb.OpenAuthSessionResponse{}, nil
		},
		closeSession: func(
			context.Context,
			*morphpb.CloseAuthSessionRequest,
		) (*morphpb.CloseAuthSessionResponse, error) {
			close(closed)
			return &morphpb.CloseAuthSessionResponse{}, nil
		},
	}
	activationDone := make(chan error, 1)
	go func() {
		_, err := resolver.prepareAuthenticatedRequest(
			context.Background(),
			"/morph.v1.SessionService/List",
		)
		activationDone <- err
	}()
	<-openStarted
	closeDone := make(chan error, 1)
	go func() {
		closeDone <- resolver.close(context.Background())
	}()
	close(releaseOpen)

	require.NoError(t, <-activationDone)
	require.NoError(t, <-closeDone)
	<-closed
	require.False(t, resolver.active.Load())
}

func TestAuthResolver_ClosePreventsFutureActivationWhenInactive(t *testing.T) {
	resolver := newAuthResolver(Options{
		AuthMethods: []string{"/morph.v1.SessionService/List"},
	})
	resolver.tokenResolved = true
	resolver.token = "automatic-token"
	resolver.claims.SessionID = "automatic-session"
	resolver.authClient = &authServiceClientStub{}

	require.NoError(t, resolver.close(context.Background()))
	require.True(t, resolver.closing.Load())
	_, err := resolver.prepareAuthenticatedRequest(
		context.Background(),
		"/morph.v1.SessionService/List",
	)
	require.ErrorContains(t, err, "resolver is closing")
}

func TestAuthResolver_CloseReturnsCleanupFailure(t *testing.T) {
	resolver := newAuthResolver(Options{
		AuthMethods: []string{"/morph.v1.SessionService/List"},
	})
	resolver.tokenResolved = true
	resolver.token = "automatic-token"
	resolver.claims.SessionID = "automatic-session"
	resolver.active.Store(true)
	closeErr := errors.New("close failed")
	resolver.authClient = &authServiceClientStub{
		closeSession: func(
			context.Context,
			*morphpb.CloseAuthSessionRequest,
		) (*morphpb.CloseAuthSessionResponse, error) {
			return nil, closeErr
		},
	}

	require.ErrorIs(t, resolver.close(context.Background()), closeErr)
	require.True(t, resolver.active.Load())
}

func TestAuthResolver_CloseHandlesNilResolver(t *testing.T) {
	var resolver *authResolver
	require.NoError(t, resolver.close(context.Background()))
}

func TestAuthResolver_PreparePropagatesTokenAndActivationFailures(t *testing.T) {
	setResolverProfile(t)
	resolver := newAuthResolver(Options{AuthKey: []byte("missing-key.pem")})
	_, err := resolver.prepareAuthenticatedRequest(
		context.Background(),
		"/morph.v1.SessionService/List",
	)
	require.Error(t, err)

	resolver = newAuthResolver(Options{AuthToken: "explicit-token"})
	_, err = resolver.prepareAuthenticatedRequest(
		context.Background(),
		"/morph.v1.SessionService/List",
	)
	require.ErrorContains(t, err, "auth client is unavailable")

	resolver.active.Store(false)
	resolver.authClient = &authServiceClientStub{}
	resolver.closing.Store(true)
	_, err = resolver.prepareAuthenticatedRequest(
		context.Background(),
		closeSessionMethod,
	)
	require.ErrorContains(t, err, "resolver is closing")
}

func setResolverProfile(t *testing.T) string {
	t.Helper()
	original := profile.Active()
	home := t.TempDir()
	profile.SetActive(profile.WithMetadataPaths(profile.Profile{Name: "test", HomeDir: home}))
	t.Cleanup(func() { profile.SetActive(original) })

	return home
}

type authServiceClientStub struct {
	morphpb.AuthServiceClient
	openSession func(
		context.Context,
		*morphpb.OpenAuthSessionRequest,
	) (*morphpb.OpenAuthSessionResponse, error)
	closeSession func(
		context.Context,
		*morphpb.CloseAuthSessionRequest,
	) (*morphpb.CloseAuthSessionResponse, error)
}

func (s *authServiceClientStub) OpenSession(
	ctx context.Context,
	request *morphpb.OpenAuthSessionRequest,
	_ ...grpc.CallOption,
) (*morphpb.OpenAuthSessionResponse, error) {
	return s.openSession(ctx, request)
}

func (s *authServiceClientStub) CloseSession(
	ctx context.Context,
	request *morphpb.CloseAuthSessionRequest,
	_ ...grpc.CallOption,
) (*morphpb.CloseAuthSessionResponse, error) {
	return s.closeSession(ctx, request)
}
