package server

import (
	"context"
	"errors"
	"net"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	morphauth "github.com/xymorphic/morph/internal/auth"
	"github.com/xymorphic/morph/internal/browser"
	"github.com/xymorphic/morph/internal/config"
	agentstub "github.com/xymorphic/morph/internal/mocks/agentstub"
	"github.com/xymorphic/morph/internal/permissions"
	morphpb "github.com/xymorphic/morph/internal/rpc/proto"
	"github.com/xymorphic/morph/internal/rpc/rpcmeta"
	agentsession "github.com/xymorphic/morph/pkg/agent/session"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthgrpc "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestNew_RegistersSessionServiceWithoutHealth(t *testing.T) {
	server := New(&agentstub.AgentRunnerStub{}, Options{})

	serviceInfo := server.GetServiceInfo()
	require.Contains(t, serviceInfo, morphpb.SessionService_ServiceDesc.ServiceName)
	require.Contains(t, serviceInfo, morphpb.BrowserService_ServiceDesc.ServiceName)
	require.NotContains(t, serviceInfo, healthgrpc.Health_ServiceDesc.ServiceName)
}

func TestNew_RegistersHealthWhenEnabled(t *testing.T) {
	server := New(&agentstub.AgentRunnerStub{}, Options{Health: true})

	serviceInfo := server.GetServiceInfo()
	require.Contains(t, serviceInfo, morphpb.SessionService_ServiceDesc.ServiceName)
	require.Contains(t, serviceInfo, healthgrpc.Health_ServiceDesc.ServiceName)
}

func TestNew_RegisteredMethodsMatchAuthenticationCatalog(t *testing.T) {
	server := New(&agentstub.AgentRunnerStub{}, Options{Health: true})
	var methods []string
	for serviceName, service := range server.GetServiceInfo() {
		for _, method := range service.Methods {
			methods = append(methods, "/"+serviceName+"/"+method.Name)
		}
	}
	sort.Strings(methods)

	require.Equal(t, morphauth.RPCMethodCatalog(), methods)
}

func TestCancelStreamOnShutdownInterceptor_CancelsActiveHandler(t *testing.T) {
	shutdownCtx, cancelShutdown := context.WithCancel(context.Background())
	t.Cleanup(cancelShutdown)
	started := make(chan struct{})
	done := make(chan error, 1)
	stream := &authTestServerStream{ctx: context.Background()}

	go func() {
		done <- cancelStreamOnShutdownInterceptor(shutdownCtx)(
			nil,
			stream,
			&grpc.StreamServerInfo{},
			func(_ any, stream grpc.ServerStream) error {
				close(started)
				<-stream.Context().Done()
				return stream.Context().Err()
			},
		)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stream handler did not start")
	}
	cancelShutdown()

	select {
	case err := <-done:
		require.True(t, errors.Is(err, context.Canceled))
	case <-time.After(time.Second):
		t.Fatal("stream handler was not cancelled by server shutdown")
	}
}

func TestNew_ShutdownContextCancelsActiveStream(t *testing.T) {
	authService, token := newServerAuthFixtureForSource(t, "tui")
	shutdownCtx, cancelShutdown := context.WithCancel(context.Background())
	t.Cleanup(cancelShutdown)
	agent := &shutdownObserveAgent{
		started: make(chan struct{}),
		stopped: make(chan struct{}),
	}
	server := New(agent, Options{Auth: authService, ShutdownContext: shutdownCtx})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	go func() { _ = server.Serve(listener) }()

	connection, err := grpc.NewClient(
		listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })
	ctx := rpcmeta.WithOutgoingPermissionSurface(context.Background(), permissions.SurfaceTUI)
	ctx = metadata.AppendToOutgoingContext(ctx, authorizationMetadataKey, "Bearer "+token)
	_, err = morphpb.NewAuthServiceClient(connection).OpenSession(
		ctx,
		&morphpb.OpenAuthSessionRequest{Source: "tui"},
	)
	require.NoError(t, err)
	stream, err := morphpb.NewSessionServiceClient(connection).Observe(
		ctx,
		&morphpb.ObserveSessionRequest{Id: "default"},
	)
	require.NoError(t, err)

	select {
	case <-agent.started:
	case <-time.After(time.Second):
		t.Fatal("session observer did not start")
	}
	cancelShutdown()

	select {
	case <-agent.stopped:
	case <-time.After(time.Second):
		t.Fatal("session observer did not stop during server shutdown")
	}
	_, err = stream.Recv()
	require.Error(t, err)
}

func TestBrowserService_EndToEndRequiresJWT(t *testing.T) {
	authService, token := newServerAuthFixture(t)
	cfg := config.NewDefaultConfig()
	runtime := &browserServerRuntime{status: browser.Status{
		Enabled:  true,
		Profiles: []browser.Profile{{Name: "default", Mode: config.BrowserProfileManagedEphemeral, Default: true, Available: true}},
	}, artifact: browser.ArtifactContent{Artifact: browser.Artifact{Handle: "artifact_1"}}}
	server := New(&agentstub.AgentRunnerStub{}, Options{
		Browser: runtime, BrowserConfig: cfg.Browser, BrowserCapability: true, ProfileName: "default",
		PermissionPolicy: permissions.Policy{Rules: []permissions.Rule{{Name: "allow", Decision: permissions.DecisionAllow}}},
		Auth:             authService,
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	go func() { _ = server.Serve(listener) }()
	connection, err := grpc.NewClient(
		listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })
	client := morphpb.NewBrowserServiceClient(connection)
	authClient := morphpb.NewAuthServiceClient(connection)

	_, err = client.Status(context.Background(), &morphpb.GetBrowserStatusRequest{})
	require.Equal(t, codes.Unauthenticated, status.Code(err))

	spoofed := rpcmeta.WithOutgoingPermissionSurface(context.Background(), permissions.SurfaceCLI)
	spoofed = rpcmeta.WithOutgoingPermissionPreset(spoofed, permissions.PresetFullAccess)
	_, err = client.Status(spoofed, &morphpb.GetBrowserStatusRequest{})
	require.Equal(t, codes.Unauthenticated, status.Code(err))

	authenticated := metadata.AppendToOutgoingContext(spoofed, authorizationMetadataKey, "Bearer "+token)
	_, err = authClient.OpenSession(authenticated, &morphpb.OpenAuthSessionRequest{Source: "cli"})
	require.NoError(t, err)
	request := &morphpb.GetBrowserStatusRequest{}
	response, err := client.Status(authenticated, request)
	require.NoError(t, err)
	require.True(t, response.GetStatus().GetEnabled())

	artifactRequest := &morphpb.ReadBrowserArtifactRequest{Handle: "artifact_1"}
	unauthenticatedStream, err := client.ReadArtifact(spoofed, artifactRequest)
	require.NoError(t, err)
	_, err = unauthenticatedStream.Recv()
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	authenticatedStream, err := client.ReadArtifact(authenticated, artifactRequest)
	require.NoError(t, err)
	artifactResponse, err := authenticatedStream.Recv()
	require.NoError(t, err)
	require.Equal(t, "artifact_1", artifactResponse.GetArtifact().GetHandle())
}

type browserServerRuntime struct {
	status   browser.Status
	artifact browser.ArtifactContent
}

type shutdownObserveAgent struct {
	agentstub.AgentRunnerStub
	started chan struct{}
	stopped chan struct{}
}

func (a *shutdownObserveAgent) ObserveSessionEvents(
	ctx context.Context,
	_ string,
	_ int64,
	_ func(agentsession.Event) error,
) error {
	close(a.started)
	<-ctx.Done()
	close(a.stopped)
	return ctx.Err()
}

func (s *browserServerRuntime) Status() browser.Status {
	return s.status
}

func (*browserServerRuntime) Start(context.Context, browser.StartRequest) (browser.Session, error) {
	return browser.Session{}, nil
}

func (*browserServerRuntime) Stop(context.Context, string) (browser.Session, error) {
	return browser.Session{}, nil
}

func (s *browserServerRuntime) ReadArtifact(context.Context, string) (browser.ArtifactContent, error) {
	return s.artifact, nil
}
