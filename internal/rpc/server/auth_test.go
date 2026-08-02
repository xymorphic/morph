package server

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	morphauth "github.com/xymorphic/morph/internal/auth"
	"github.com/xymorphic/morph/internal/auth/storememory"
	"github.com/xymorphic/morph/internal/permissions"
	morphpb "github.com/xymorphic/morph/internal/rpc/proto"
	"github.com/xymorphic/morph/internal/rpc/rpcmeta"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func TestAuthUnaryServerInterceptor_RequiresAndPropagatesJWTPrincipal(t *testing.T) {
	service, raw := newServerAuthFixture(t)
	interceptor := authUnaryServerInterceptor(service)
	handlerCalled := false
	_, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{
		FullMethod: morphpb.SessionService_List_FullMethodName,
	}, func(context.Context, any) (any, error) {
		handlerCalled = true
		return nil, nil
	})
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	require.False(t, handlerCalled)

	openContext := incomingAuthContext(raw, permissions.SurfaceCLI)
	_, err = interceptor(openContext, &morphpb.OpenAuthSessionRequest{Source: "cli"},
		&grpc.UnaryServerInfo{FullMethod: morphpb.AuthService_OpenSession_FullMethodName},
		func(ctx context.Context, _ any) (any, error) {
			principal, ok := rpcmeta.AuthenticatedPrincipal(ctx)
			require.True(t, ok)
			require.Equal(t, "owner", principal.OwnerID)
			return nil, nil
		})
	require.NoError(t, err)

	response, err := interceptor(openContext, &morphpb.ListSessionsRequest{},
		&grpc.UnaryServerInfo{FullMethod: morphpb.SessionService_List_FullMethodName},
		func(ctx context.Context, _ any) (any, error) {
			return rpcmeta.PermissionActorFromIncomingContext(ctx), nil
		})
	require.NoError(t, err)
	require.Equal(t, permissions.Actor{Kind: permissions.ActorLocalOwner, ID: "owner"}, response)
}

func TestAuthUnaryServerInterceptor_RejectsWrongMethodScope(t *testing.T) {
	service, raw := newServerAuthFixtureWithScopes(
		t, nil, []string{morphpb.SessionService_List_FullMethodName},
	)
	interceptor := authUnaryServerInterceptor(service)
	ctx := incomingAuthContext(raw, permissions.SurfaceCLI)
	_, err := interceptor(ctx, &morphpb.OpenAuthSessionRequest{Source: "cli"},
		&grpc.UnaryServerInfo{FullMethod: morphpb.AuthService_OpenSession_FullMethodName},
		func(context.Context, any) (any, error) { return nil, nil })
	require.NoError(t, err)

	_, err = interceptor(ctx, &morphpb.ArchiveSessionRequest{},
		&grpc.UnaryServerInfo{FullMethod: morphpb.SessionService_Archive_FullMethodName},
		func(context.Context, any) (any, error) {
			t.Fatal("handler must not run")
			return nil, nil
		})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestAuthStreamServerInterceptor_PropagatesPrincipal(t *testing.T) {
	service, raw := newServerAuthFixtureForSource(t, "tui")
	unary := authUnaryServerInterceptor(service)
	ctx := incomingAuthContext(raw, permissions.SurfaceTUI)
	_, err := unary(ctx, &morphpb.OpenAuthSessionRequest{Source: "tui"},
		&grpc.UnaryServerInfo{FullMethod: morphpb.AuthService_OpenSession_FullMethodName},
		func(context.Context, any) (any, error) { return nil, nil })
	require.NoError(t, err)

	stream := &authTestServerStream{ctx: ctx}
	err = authStreamServerInterceptor(service)(nil, stream, &grpc.StreamServerInfo{
		FullMethod: morphpb.SessionService_Observe_FullMethodName,
	}, func(_ any, authenticated grpc.ServerStream) error {
		principal, ok := rpcmeta.AuthenticatedPrincipal(authenticated.Context())
		require.True(t, ok)
		require.Equal(t, "tui", principal.Source)
		return nil
	})
	require.NoError(t, err)
}

func TestAuthUnaryServerInterceptor_RequiresMatchingBoundCertificate(t *testing.T) {
	certificate := &x509.Certificate{Raw: []byte("client-certificate")}
	digest := sha256.Sum256(certificate.Raw)
	thumbprint := base64.RawURLEncoding.EncodeToString(digest[:])
	service, raw := newServerAuthFixtureWithCertificate(t, thumbprint)
	interceptor := authUnaryServerInterceptor(service)
	ctx := incomingAuthContext(raw, permissions.SurfaceCLI)
	ctx = peer.NewContext(ctx, &peer.Peer{AuthInfo: credentials.TLSInfo{
		State: tls.ConnectionState{PeerCertificates: []*x509.Certificate{certificate}},
	}})
	_, err := interceptor(ctx, &morphpb.OpenAuthSessionRequest{Source: "cli"},
		&grpc.UnaryServerInfo{FullMethod: morphpb.AuthService_OpenSession_FullMethodName},
		func(context.Context, any) (any, error) { return nil, nil })
	require.NoError(t, err)

	service, raw = newServerAuthFixtureWithCertificate(t, "different")
	interceptor = authUnaryServerInterceptor(service)
	ctx = incomingAuthContext(raw, permissions.SurfaceCLI)
	ctx = peer.NewContext(ctx, &peer.Peer{AuthInfo: credentials.TLSInfo{
		State: tls.ConnectionState{PeerCertificates: []*x509.Certificate{certificate}},
	}})
	_, err = interceptor(ctx, &morphpb.OpenAuthSessionRequest{Source: "cli"},
		&grpc.UnaryServerInfo{FullMethod: morphpb.AuthService_OpenSession_FullMethodName},
		func(context.Context, any) (any, error) { return nil, nil })
	require.Equal(t, codes.Unauthenticated, status.Code(err))
}

type authTestServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *authTestServerStream) Context() context.Context {
	return s.ctx
}

func newServerAuthFixture(t *testing.T) (*morphauth.Service, string) {
	t.Helper()
	return newServerAuthFixtureForSource(t, "cli")
}

func newServerAuthFixtureForSource(
	t *testing.T,
	source string,
) (*morphauth.Service, string) {
	t.Helper()
	return newServerAuthFixtureWithSourceAndScopes(
		t, source, []string{morphauth.RootScope}, nil,
	)
}

func newServerAuthFixtureWithScopes(
	t *testing.T,
	services, methods []string,
) (*morphauth.Service, string) {
	return newServerAuthFixtureWithSourceAndScopes(t, "cli", services, methods)
}

func newServerAuthFixtureWithSourceAndScopes(
	t *testing.T,
	source string,
	services, methods []string,
) (*morphauth.Service, string) {
	return newServerAuthFixtureWithSourceScopesAndCertificate(t, source, services, methods, "")
}

func newServerAuthFixtureWithCertificate(
	t *testing.T,
	certificateThumbprint string,
) (*morphauth.Service, string) {
	t.Helper()
	return newServerAuthFixtureWithSourceScopesAndCertificate(
		t, "cli", []string{morphauth.RootScope}, nil, certificateThumbprint,
	)
}

func newServerAuthFixtureWithSourceScopesAndCertificate(
	t *testing.T,
	source string,
	services, methods []string,
	certificateThumbprint string,
) (*morphauth.Service, string) {
	t.Helper()
	store := storememory.New()
	service, err := morphauth.NewService(morphauth.ServiceOptions{
		Audience: "morph-rpc:test", Store: store,
		SessionIdleTTL: time.Hour, SessionMaxTTL: 24 * time.Hour,
	})
	require.NoError(t, err)
	identity, err := morphauth.GenerateIdentity(1)
	require.NoError(t, err)
	authorization, err := service.SeedRoot(context.Background(), identity, "owner")
	require.NoError(t, err)
	authorizedMethods := append([]string(nil), methods...)
	if len(services) == 0 {
		authorizedMethods = append(authorizedMethods, morphpb.AuthService_OpenSession_FullMethodName)
	}
	authorization.Services = append([]string(nil), services...)
	authorization.Methods = authorizedMethods
	authorization.UpdatedAt = time.Now().UTC()
	authorization, err = store.PutAuthorization(context.Background(), authorization)
	require.NoError(t, err)
	raw, _, err := morphauth.SignAccessToken(identity, morphauth.TokenRequest{
		Audience: "morph-rpc:test", Subject: identity.ID, SessionID: "session",
		TokenID: "token", OwnerID: "owner", Roles: []string{morphauth.RoleOwner},
		Source:   source,
		Services: services, Methods: authorizedMethods,
		TTL: time.Hour, AuthorizationRevision: authorization.Revision,
		CertificateThumbprint: certificateThumbprint,
	})
	require.NoError(t, err)

	return service, raw
}

func incomingAuthContext(raw string, surface permissions.Surface) context.Context {
	ctx := rpcmeta.WithOutgoingPermissionSurface(context.Background(), surface)
	ctx = metadata.AppendToOutgoingContext(ctx, authorizationMetadataKey, "Bearer "+raw)
	values, _ := metadata.FromOutgoingContext(ctx)
	return metadata.NewIncomingContext(context.Background(), values)
}
