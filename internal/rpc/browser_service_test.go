package rpc

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xymorphic/morph/internal/browser"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/permissions"
	morphpb "github.com/xymorphic/morph/internal/rpc/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestBrowserService_InspectsStartsStopsAndReadsOwnedRuntime(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	runtime := &browserRPCStub{status: browser.Status{
		Enabled:  true,
		Profiles: []browser.Profile{{Name: "default", Mode: config.BrowserProfileManagedEphemeral, Default: true, Available: true}},
		Sessions: []browser.Session{{ID: "browser_existing", Profile: "default", State: browser.SessionReady, CreatedAt: now, LastActive: now}},
	}}
	runtime.startResult = browser.Session{
		ID: "browser_started", Profile: "default", ProfileMode: config.BrowserProfileManagedEphemeral,
		State: browser.SessionReady, CreatedAt: now, LastActive: now,
	}
	runtime.stopResult = browser.Session{ID: "browser_started", Profile: "default", State: browser.SessionStopped}
	artifactData := bytes.Repeat([]byte("p"), 2*browserArtifactChunkSize+3)
	runtime.artifactResult = browser.ArtifactContent{
		Artifact: browser.Artifact{
			Handle: "artifact_1", Kind: browser.ArtifactScreenshot, Name: "shot.png", MIMEType: "image/png",
			Size: 3, Profile: "default", SessionID: "browser_started", RunID: "run_1",
			Effects: []permissions.Effect{permissions.EffectRead}, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
		},
		Data: artifactData,
	}
	service := newBrowserRPCService(runtime, true)
	ctx := permissionOperatorContext("cli", "127.0.0.1")

	statusResponse, err := service.Status(ctx, &morphpb.GetBrowserStatusRequest{})
	require.NoError(t, err)
	require.True(t, statusResponse.GetStatus().GetEnabled())
	require.Len(t, statusResponse.GetStatus().GetProfiles(), 1)

	profiles, err := service.ListProfiles(ctx, &morphpb.ListBrowserProfilesRequest{})
	require.NoError(t, err)
	require.Equal(t, "default", profiles.GetProfiles()[0].GetName())
	sessions, err := service.ListSessions(ctx, &morphpb.ListBrowserSessionsRequest{})
	require.NoError(t, err)
	require.Equal(t, "browser_existing", sessions.GetSessions()[0].GetId())

	started, err := service.Start(ctx, &morphpb.StartBrowserRequest{Profile: "default", OwnerSessionId: "main"})
	require.NoError(t, err)
	require.Equal(t, "browser_started", started.GetSession().GetId())
	require.Equal(t, "default", runtime.startRequest.Profile)
	requireBrowserAuthorization(t, runtime.startContext, "main", "")

	stopped, err := service.Stop(ctx, &morphpb.StopBrowserRequest{Id: "browser_started", OwnerSessionId: "main"})
	require.NoError(t, err)
	require.Equal(t, "stopped", stopped.GetSession().GetState())
	require.Equal(t, "browser_started", runtime.stopID)
	requireBrowserAuthorization(t, runtime.stopContext, "main", "")

	artifactStream := &browserArtifactServerStream{ctx: ctx}
	err = service.ReadArtifact(&morphpb.ReadBrowserArtifactRequest{
		Handle: "artifact_1", OwnerSessionId: "main", RunId: "run_1",
	}, artifactStream)
	require.NoError(t, err)
	require.Len(t, artifactStream.responses, 3)
	var streamedData []byte
	for _, response := range artifactStream.responses {
		streamedData = append(streamedData, response.GetData()...)
	}
	require.Equal(t, artifactData, streamedData)
	require.Equal(t, "artifact_1", artifactStream.responses[0].GetArtifact().GetHandle())
	require.Nil(t, artifactStream.responses[1].GetArtifact())
	requireBrowserAuthorization(t, runtime.artifactContext, "main", "run_1")
}

func TestBrowserService_ReportsDisabledConfiguredStateWithoutRuntime(t *testing.T) {
	service := newBrowserRPCService(nil, false)
	ctx := permissionOperatorContext("tui", "127.0.0.1")

	response, err := service.Status(ctx, &morphpb.GetBrowserStatusRequest{})
	require.NoError(t, err)
	require.False(t, response.GetStatus().GetEnabled())
	require.Equal(t, "default", response.GetStatus().GetProfiles()[0].GetName())
	require.False(t, response.GetStatus().GetProfiles()[0].GetAvailable())

	profiles, err := service.ListProfiles(ctx, &morphpb.ListBrowserProfilesRequest{})
	require.NoError(t, err)
	require.Len(t, profiles.GetProfiles(), 1)
	sessions, err := service.ListSessions(ctx, &morphpb.ListBrowserSessionsRequest{})
	require.NoError(t, err)
	require.Empty(t, sessions.GetSessions())

	_, err = service.Start(ctx, &morphpb.StartBrowserRequest{})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestBrowserService_ReportsUnmanagedEgressWarningWithoutRuntime(t *testing.T) {
	cfg := config.NewDefaultConfig()
	cfg.Browser.Profiles = []config.BrowserProfileConfig{{
		Name: "remote", Mode: config.BrowserProfileRemoteCDP, CDPEndpoint: "https://example.com",
		AttachmentScope: config.BrowserAttachmentBrowser, AcknowledgeUnmanagedEgress: true,
	}}
	cfg.Browser.DefaultProfile = "remote"
	service := NewBrowserService(newAllowedServiceWithOptions(nil, ServiceOptions{
		BrowserConfig: cfg.Browser, ProfileName: "default",
	}))

	response, err := service.Status(
		permissionOperatorContext("cli", "127.0.0.1"),
		&morphpb.GetBrowserStatusRequest{},
	)

	require.NoError(t, err)
	require.Contains(t, response.GetStatus().GetProfiles()[0].GetWarning(), "do not use Morph's managed egress proxy")
}

func TestBrowserService_RequiresAuthenticatedOwnerAndRejectsMalformedRequests(t *testing.T) {
	service := newBrowserRPCService(&browserRPCStub{}, true)
	ownerCtx := permissionOperatorContext("cli", "127.0.0.1")

	_, err := service.Status(context.Background(), &morphpb.GetBrowserStatusRequest{})
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	_, err = service.Status(ownerCtx, nil)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = service.Stop(ownerCtx, &morphpb.StopBrowserRequest{})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	err = service.ReadArtifact(&morphpb.ReadBrowserArtifactRequest{}, &browserArtifactServerStream{ctx: ownerCtx})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	service.service.profileName = ""
	_, err = service.Status(ownerCtx, &morphpb.GetBrowserStatusRequest{})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestBrowserService_MapsBrowserAndPermissionErrors(t *testing.T) {
	runtime := &browserRPCStub{}
	service := newBrowserRPCService(runtime, true)
	ctx := permissionOperatorContext("cli", "127.0.0.1")

	runtime.startErr = &browser.Error{Code: browser.ErrorInvalidRequest, Err: errors.New("invalid browser request")}
	_, err := service.Start(ctx, &morphpb.StartBrowserRequest{})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	runtime.startErr = &browser.Error{Code: browser.ErrorTimeout, Err: errors.New("browser timed out")}
	_, err = service.Start(ctx, &morphpb.StartBrowserRequest{})
	require.Equal(t, codes.DeadlineExceeded, status.Code(err))
	runtime.startErr = context.Canceled
	_, err = service.Start(ctx, &morphpb.StartBrowserRequest{})
	require.Equal(t, codes.Canceled, status.Code(err))
}

func TestBrowserService_MapsInspectionPermissionDenial(t *testing.T) {
	service := newBrowserRPCService(&browserRPCStub{}, true)
	service.service.permissions = permissions.NewEngine(permissions.Policy{
		Preset: permissions.PresetCustom, Default: permissions.DecisionDeny,
		SurfaceKindDefaults: map[permissions.SurfaceKind]permissions.Decision{
			permissions.SurfaceKindLocal: permissions.DecisionDeny,
		},
	})

	_, err := service.Status(
		permissionOperatorContext("cli", "127.0.0.1"),
		&morphpb.GetBrowserStatusRequest{},
	)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestBrowserService_PreflightsLifecycleAndArtifactPermissions(t *testing.T) {
	runtime := &browserRPCStub{}
	service := newBrowserRPCService(runtime, true)
	service.service.permissions = permissions.NewEngine(permissions.Policy{
		Preset: permissions.PresetCustom, Default: permissions.DecisionDeny,
		SurfaceKindDefaults: map[permissions.SurfaceKind]permissions.Decision{
			permissions.SurfaceKindLocal: permissions.DecisionDeny,
		},
	})
	ctx := permissionOperatorContext("cli", "127.0.0.1")

	_, err := service.Start(ctx, &morphpb.StartBrowserRequest{Profile: "default"})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Nil(t, runtime.startContext)

	_, err = service.Stop(ctx, &morphpb.StopBrowserRequest{Id: "browser_1"})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Nil(t, runtime.stopContext)

	err = service.ReadArtifact(
		&morphpb.ReadBrowserArtifactRequest{Handle: "artifact_1"},
		&browserArtifactServerStream{ctx: ctx},
	)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Nil(t, runtime.artifactContext)
}

func TestGetBrowserGRPCError_MapsDomainFailures(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		code codes.Code
	}{
		{name: "nil", code: codes.OK},
		{name: "permission denied", err: &permissions.DecisionError{Code: permissions.ErrorCodeDenied}, code: codes.PermissionDenied},
		{name: "approval required", err: &permissions.DecisionError{Code: permissions.ErrorCodeApprovalRequired}, code: codes.FailedPrecondition},
		{name: "approval rate limited", err: &permissions.DecisionError{Code: permissions.ErrorCodeApprovalRateLimited}, code: codes.ResourceExhausted},
		{name: "invalid request", err: browserFailure(browser.ErrorInvalidRequest), code: codes.InvalidArgument},
		{name: "not found", err: browserFailure(browser.ErrorNotFound), code: codes.NotFound},
		{name: "ownership", err: browserFailure(browser.ErrorOwnership), code: codes.PermissionDenied},
		{name: "closed", err: browserFailure(browser.ErrorClosed), code: codes.FailedPrecondition},
		{name: "not ready", err: browserFailure(browser.ErrorNotReady), code: codes.FailedPrecondition},
		{name: "stale reference", err: browserFailure(browser.ErrorStaleReference), code: codes.FailedPrecondition},
		{name: "unavailable", err: browserFailure(browser.ErrorUnavailable), code: codes.Unavailable},
		{name: "start failed", err: browserFailure(browser.ErrorStartFailed), code: codes.Unavailable},
		{name: "health failed", err: browserFailure(browser.ErrorHealthFailed), code: codes.Unavailable},
		{name: "timeout", err: browserFailure(browser.ErrorTimeout), code: codes.DeadlineExceeded},
		{name: "browser canceled", err: browserFailure(browser.ErrorCancelled), code: codes.Canceled},
		{name: "canceled", err: context.Canceled, code: codes.Canceled},
		{name: "deadline", err: context.DeadlineExceeded, code: codes.DeadlineExceeded},
		{name: "unknown", err: errors.New("unexpected failure"), code: codes.Internal},
	} {
		t.Run(test.name, func(t *testing.T) {
			actual := getBrowserGRPCError(test.err)
			if test.code == codes.OK {
				require.NoError(t, actual)
				return
			}
			require.Equal(t, test.code, status.Code(actual))
		})
	}
}

func browserFailure(code browser.ErrorCode) error {
	return &browser.Error{Code: code, Err: errors.New("browser failure")}
}

func TestBrowserService_EffectiveConfigIsSafeAndPresetAware(t *testing.T) {
	service := newBrowserRPCService(nil, false)
	ctx := permissionOperatorContext("cli", "127.0.0.1")

	response, err := service.EffectiveConfig(ctx, &morphpb.GetBrowserEffectiveConfigRequest{})
	require.NoError(t, err)
	require.False(t, response.GetEnabled())
	require.False(t, response.GetCapabilityEnabled())
	require.True(t, response.GetNetworkStrict())
	require.Equal(t, "default", response.GetDefaultProfile())
	require.Equal(t, "custom", response.GetPermissionPreset())
	require.False(t, response.GetExecutableConfigured())
}

type browserRPCStub struct {
	status          browser.Status
	startRequest    browser.StartRequest
	startContext    context.Context
	startResult     browser.Session
	startErr        error
	stopID          string
	stopContext     context.Context
	stopResult      browser.Session
	stopErr         error
	artifactHandle  string
	artifactContext context.Context
	artifactResult  browser.ArtifactContent
	artifactErr     error
}

type browserArtifactServerStream struct {
	grpc.ServerStream
	ctx       context.Context
	responses []*morphpb.ReadBrowserArtifactResponse
}

func (s *browserArtifactServerStream) Context() context.Context {
	return s.ctx
}

func (s *browserArtifactServerStream) Send(response *morphpb.ReadBrowserArtifactResponse) error {
	s.responses = append(s.responses, response)
	return nil
}

func (s *browserRPCStub) Status() browser.Status {
	return s.status
}

func (s *browserRPCStub) Start(ctx context.Context, request browser.StartRequest) (browser.Session, error) {
	s.startContext = ctx
	s.startRequest = request
	return s.startResult, s.startErr
}

func (s *browserRPCStub) Stop(ctx context.Context, id string) (browser.Session, error) {
	s.stopContext = ctx
	s.stopID = id
	return s.stopResult, s.stopErr
}

func (s *browserRPCStub) ReadArtifact(ctx context.Context, handle string) (browser.ArtifactContent, error) {
	s.artifactContext = ctx
	s.artifactHandle = handle
	return s.artifactResult, s.artifactErr
}

func newBrowserRPCService(runtime BrowserAPI, enabled bool) *BrowserService {
	cfg := config.NewDefaultConfig()
	cfg.Browser.Enabled = enabled
	service := newAllowedServiceWithOptions(nil, ServiceOptions{
		Browser: runtime, BrowserConfig: cfg.Browser, BrowserCapability: enabled, ProfileName: "default",
	})
	return NewBrowserService(service)
}

func requireBrowserAuthorization(t *testing.T, ctx context.Context, sessionID, runID string) {
	t.Helper()
	authorization, ok := permissions.FromContext(ctx)
	require.True(t, ok)
	require.Equal(t, permissions.Actor{Kind: permissions.ActorLocalOwner, ID: "default"}, authorization.Actor)
	require.Equal(t, permissions.SurfaceCLI, authorization.Surface)
	require.Equal(t, "default", authorization.Profile)
	require.Equal(t, sessionID, authorization.SessionID)
	require.Equal(t, runID, authorization.RunID)
}
