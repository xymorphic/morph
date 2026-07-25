package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	morphauth "github.com/wandxy/morph/internal/auth"
	morphpb "github.com/wandxy/morph/internal/rpc/proto"
	"github.com/wandxy/morph/internal/rpc/rpcmeta"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

const (
	authorizationMetadataKey = "authorization"
	openSessionMethod        = "/morph.v1.AuthService/OpenSession"
)

func authUnaryServerInterceptor(service *morphauth.Service) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		request any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		authenticated, err := authenticateContext(ctx, request, info.FullMethod, service)
		if err != nil {
			return nil, err
		}

		return handler(authenticated, request)
	}
}

func authStreamServerInterceptor(service *morphauth.Service) grpc.StreamServerInterceptor {
	return func(
		srv any,
		stream grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		authenticated, err := authenticateContext(stream.Context(), nil, info.FullMethod, service)
		if err != nil {
			return err
		}
		principal, _ := rpcmeta.AuthenticatedPrincipal(authenticated)
		watched, cancel := context.WithCancel(authenticated)
		defer cancel()
		go watchPrincipal(watched, cancel, service, principal)

		return handler(srv, &authenticatedServerStream{ServerStream: stream, ctx: watched})
	}
}

type authenticatedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *authenticatedServerStream) Context() context.Context {
	return s.ctx
}

func authenticateContext(
	ctx context.Context,
	request any,
	method string,
	service *morphauth.Service,
) (context.Context, error) {
	if service == nil {
		return nil, status.Error(codes.Unauthenticated, "RPC authentication is unavailable")
	}
	raw, err := incomingBearerToken(ctx)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "RPC authentication failed")
	}
	var principal morphauth.Principal
	if method == openSessionMethod {
		source := ""
		if openRequest, ok := request.(*morphpb.OpenAuthSessionRequest); ok {
			source = openRequest.GetSource()
		}
		principal, err = service.OpenSessionBound(ctx, raw, source, certificateThumbprint(ctx))
	} else {
		principal, err = service.Authenticate(ctx, raw, method, certificateThumbprint(ctx))
	}
	if errors.Is(err, morphauth.ErrPermissionDenied) {
		return nil, status.Error(codes.PermissionDenied, "RPC method is not authorized")
	}
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "RPC authentication failed")
	}

	return rpcmeta.WithAuthenticatedPrincipal(ctx, principal), nil
}

func incomingBearerToken(ctx context.Context) (string, error) {
	values := metadata.ValueFromIncomingContext(ctx, authorizationMetadataKey)
	if len(values) != 1 {
		return "", errors.New("exactly one authorization value is required")
	}
	const prefix = "Bearer "
	if len(values[0]) <= len(prefix) || values[0][:len(prefix)] != prefix {
		return "", errors.New("bearer authorization is required")
	}

	return values[0][len(prefix):], nil
}

func certificateThumbprint(ctx context.Context) string {
	remote, ok := peer.FromContext(ctx)
	if !ok {
		return ""
	}
	tlsInfo, ok := remote.AuthInfo.(credentials.TLSInfo)
	if !ok || len(tlsInfo.State.PeerCertificates) == 0 {
		return ""
	}
	digest := sha256.Sum256(tlsInfo.State.PeerCertificates[0].Raw)

	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func watchPrincipal(
	ctx context.Context,
	cancel context.CancelFunc,
	service *morphauth.Service,
	principal morphauth.Principal,
) {
	ticker := time.NewTicker(service.PrincipalKeepAliveInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if service.KeepAlivePrincipal(ctx, principal) != nil {
				cancel()
				return
			}
		}
	}
}
