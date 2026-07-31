package server

import (
	"context"

	morphagent "github.com/wandxy/morph/internal/agent"
	morphauth "github.com/wandxy/morph/internal/auth"
	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/permissions"
	"github.com/wandxy/morph/internal/rpc"
	morphpb "github.com/wandxy/morph/internal/rpc/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// Options configures the RPC server.
type Options struct {
	ShutdownContext      context.Context
	RuntimeModel         rpc.ModelRuntime
	Health               bool
	GatewayPairingSecret string
	GatewayConfig        config.GatewayConfig
	GatewayRuntime       rpc.GatewayRuntime
	Automation           rpc.AutomationAPI
	Browser              rpc.BrowserAPI
	BrowserConfig        config.BrowserConfig
	BrowserCapability    bool
	ProfileName          string
	PermissionPolicy     permissions.Policy
	Auth                 *morphauth.Service
	AuthServiceOptions   []rpc.AuthServiceOption
	TransportCredentials credentials.TransportCredentials
}

// New returns a gRPC server registered with the Morph RPC services.
func New(service morphagent.ServiceAPI, opts Options) *grpc.Server {
	serverOptions := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(authUnaryServerInterceptor(opts.Auth)),
		grpc.ChainStreamInterceptor(
			cancelStreamOnShutdownInterceptor(opts.ShutdownContext),
			authStreamServerInterceptor(opts.Auth),
		),
	}
	if opts.TransportCredentials != nil {
		serverOptions = append(serverOptions, grpc.Creds(opts.TransportCredentials))
	}
	server := grpc.NewServer(serverOptions...)
	rpcService := rpc.NewServiceWithOptions(service, rpc.ServiceOptions{
		RuntimeModel:         opts.RuntimeModel,
		GatewayPairingSecret: opts.GatewayPairingSecret,
		GatewayConfig:        opts.GatewayConfig,
		GatewayRuntime:       opts.GatewayRuntime,
		Automation:           opts.Automation,
		Browser:              opts.Browser,
		BrowserConfig:        opts.BrowserConfig,
		BrowserCapability:    opts.BrowserCapability,
		ProfileName:          opts.ProfileName,
		PermissionPolicy:     opts.PermissionPolicy,
	})
	morphpb.RegisterSessionServiceServer(server, rpcService)
	morphpb.RegisterModelServiceServer(server, rpcService)
	morphpb.RegisterGatewayServiceServer(server, rpc.NewGatewayService(rpcService))
	morphpb.RegisterAutomationServiceServer(server, rpc.NewAutomationService(rpcService))
	morphpb.RegisterPermissionServiceServer(server, rpc.NewPermissionService(rpcService))
	morphpb.RegisterBrowserServiceServer(server, rpc.NewBrowserService(rpcService))
	morphpb.RegisterAuthServiceServer(server, rpc.NewAuthService(opts.Auth, opts.AuthServiceOptions...))

	if opts.Health {
		healthcheck := health.NewServer()
		healthpb.RegisterHealthServer(server, healthcheck)
		healthcheck.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	}

	return server
}

func cancelStreamOnShutdownInterceptor(shutdownCtx context.Context) grpc.StreamServerInterceptor {
	return func(
		srv any,
		stream grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		if shutdownCtx == nil {
			return handler(srv, stream)
		}

		ctx, cancel := context.WithCancel(stream.Context())
		stop := context.AfterFunc(shutdownCtx, cancel)
		defer func() {
			stop()
			cancel()
		}()

		return handler(srv, &shutdownServerStream{ServerStream: stream, ctx: ctx})
	}
}

type shutdownServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *shutdownServerStream) Context() context.Context {
	return s.ctx
}
