package client

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xymorphic/morph/internal/browser"
	"github.com/xymorphic/morph/internal/permissions"
	morphpb "github.com/xymorphic/morph/internal/rpc/proto"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestBrowserService_TranslatesRPCOperations(t *testing.T) {
	now := time.Date(2026, 7, 19, 14, 0, 0, 0, time.UTC)
	stub := &browserServiceClientStub{
		status: &morphpb.BrowserStatus{
			Enabled: true,
			Profiles: []*morphpb.BrowserProfile{{
				Name: "default", Mode: "managed_ephemeral", Default: true, Available: true, Warning: "profile warning",
			}},
			Sessions: []*morphpb.BrowserSession{{
				Id: "browser_1", Profile: "default", State: "ready",
				CreatedAt: timestamppb.New(now), LastActive: timestamppb.New(now), Warning: "session warning",
			}},
		},
		artifact: &morphpb.BrowserArtifact{
			Handle: "artifact_1", Kind: "screenshot", Name: "shot.png", MimeType: "image/png", Size: 3,
			Profile: "default", SessionId: "browser_1", RunId: "run_1", Effects: []string{"read"},
			CreatedAt: timestamppb.New(now), ExpiresAt: timestamppb.New(now.Add(time.Hour)),
		},
	}
	service := NewBrowserService(stub)

	statusValue, err := service.Status(context.Background())
	require.NoError(t, err)
	require.True(t, statusValue.Enabled)
	require.Equal(t, now, statusValue.Sessions[0].CreatedAt)
	require.Equal(t, "session warning", statusValue.Sessions[0].Warning)
	profiles, err := service.Profiles(context.Background())
	require.NoError(t, err)
	require.Equal(t, "default", profiles[0].Name)
	require.Equal(t, "profile warning", profiles[0].Warning)
	sessions, err := service.Sessions(context.Background())
	require.NoError(t, err)
	require.Equal(t, browser.SessionReady, sessions[0].State)

	started, err := service.Start(context.Background(), "default", "main")
	require.NoError(t, err)
	require.Equal(t, "browser_1", started.ID)
	require.Equal(t, "default", stub.startRequest.GetProfile())
	require.Equal(t, "main", stub.startRequest.GetOwnerSessionId())
	stopped, err := service.Stop(context.Background(), "browser_1", "main")
	require.NoError(t, err)
	require.Equal(t, "browser_1", stopped.ID)
	require.Equal(t, "browser_1", stub.stopRequest.GetId())

	content, err := service.ReadArtifact(context.Background(), "artifact_1", "main", "run_1")
	require.NoError(t, err)
	require.Equal(t, []byte("png"), content.Data)
	require.Equal(t, []permissions.Effect{permissions.EffectRead}, content.Artifact.Effects)
	require.Equal(t, "artifact_1", stub.artifactRequest.GetHandle())

	effective, err := service.EffectiveConfig(context.Background())
	require.NoError(t, err)
	require.Equal(t, permissions.PresetApproveForMe, effective.PermissionPreset)
	require.True(t, effective.NetworkStrict)
}

func TestBrowserService_PropagatesRPCFailures(t *testing.T) {
	expected := errors.New("rpc failed")
	service := NewBrowserService(&browserServiceClientStub{err: expected})

	_, err := service.Status(context.Background())
	require.ErrorIs(t, err, expected)
	_, err = service.Profiles(context.Background())
	require.ErrorIs(t, err, expected)
	_, err = service.Sessions(context.Background())
	require.ErrorIs(t, err, expected)
	_, err = service.Start(context.Background(), "", "")
	require.ErrorIs(t, err, expected)
	_, err = service.Stop(context.Background(), "id", "")
	require.ErrorIs(t, err, expected)
	_, err = service.ReadArtifact(context.Background(), "handle", "", "")
	require.ErrorIs(t, err, expected)
	_, err = service.EffectiveConfig(context.Background())
	require.ErrorIs(t, err, expected)
}

func TestBrowserService_PropagatesArtifactStreamFailure(t *testing.T) {
	expected := errors.New("stream failed")
	service := NewBrowserService(&browserServiceClientStub{
		artifact: &morphpb.BrowserArtifact{Handle: "handle", Size: 3}, streamErr: expected,
	})

	_, err := service.ReadArtifact(context.Background(), "handle", "", "")
	require.ErrorIs(t, err, expected)
}

func TestBrowserService_RejectsMalformedArtifactStreams(t *testing.T) {
	tests := []struct {
		name      string
		responses []*morphpb.ReadBrowserArtifactResponse
		want      string
	}{
		{
			name: "missing metadata", responses: []*morphpb.ReadBrowserArtifactResponse{},
			want: "morph: browser artifact stream is missing metadata",
		},
		{
			name: "wrong handle", responses: []*morphpb.ReadBrowserArtifactResponse{{
				Artifact: &morphpb.BrowserArtifact{Handle: "other", Size: 0},
			}}, want: "morph: browser artifact stream handle does not match the request",
		},
		{
			name: "data before metadata", responses: []*morphpb.ReadBrowserArtifactResponse{{Data: []byte("x")}},
			want: "morph: browser artifact stream data arrived before metadata",
		},
		{
			name: "repeated metadata", responses: []*morphpb.ReadBrowserArtifactResponse{
				{Artifact: &morphpb.BrowserArtifact{Handle: "artifact_1", Size: 0}},
				{Artifact: &morphpb.BrowserArtifact{Handle: "artifact_1", Size: 0}},
			}, want: "morph: browser artifact stream repeated metadata",
		},
		{
			name: "too much data", responses: []*morphpb.ReadBrowserArtifactResponse{{
				Artifact: &morphpb.BrowserArtifact{Handle: "artifact_1", Size: 1}, Data: []byte("xx"),
			}}, want: "morph: browser artifact stream exceeds metadata size",
		},
		{
			name: "incomplete data", responses: []*morphpb.ReadBrowserArtifactResponse{{
				Artifact: &morphpb.BrowserArtifact{Handle: "artifact_1", Size: 2}, Data: []byte("x"),
			}}, want: "morph: browser artifact stream size does not match metadata",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := NewBrowserService(&browserServiceClientStub{artifactResponses: test.responses})
			_, err := service.ReadArtifact(context.Background(), "artifact_1", "default", "")
			require.EqualError(t, err, test.want)
		})
	}
}

func TestBrowserService_RejectsMissingClientAndHandlesNilProtoValues(t *testing.T) {
	service := NewBrowserService(nil)
	_, err := service.Status(context.Background())
	require.EqualError(t, err, "morph: browser service client is required")

	require.Empty(t, browserStatusFromProto(nil))
	require.Empty(t, browserProfileFromProto(nil))
	require.Empty(t, browserSessionFromProto(nil))
	require.Empty(t, browserArtifactFromProto(nil))
}

type browserServiceClientStub struct {
	status            *morphpb.BrowserStatus
	artifact          *morphpb.BrowserArtifact
	err               error
	streamErr         error
	startRequest      *morphpb.StartBrowserRequest
	stopRequest       *morphpb.StopBrowserRequest
	artifactRequest   *morphpb.ReadBrowserArtifactRequest
	artifactResponses []*morphpb.ReadBrowserArtifactResponse
}

func (s *browserServiceClientStub) Status(
	context.Context,
	*morphpb.GetBrowserStatusRequest,
	...grpc.CallOption,
) (*morphpb.GetBrowserStatusResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &morphpb.GetBrowserStatusResponse{Status: s.status}, nil
}

func (s *browserServiceClientStub) ListProfiles(
	context.Context,
	*morphpb.ListBrowserProfilesRequest,
	...grpc.CallOption,
) (*morphpb.ListBrowserProfilesResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &morphpb.ListBrowserProfilesResponse{Profiles: s.status.GetProfiles()}, nil
}

func (s *browserServiceClientStub) ListSessions(
	context.Context,
	*morphpb.ListBrowserSessionsRequest,
	...grpc.CallOption,
) (*morphpb.ListBrowserSessionsResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &morphpb.ListBrowserSessionsResponse{Sessions: s.status.GetSessions()}, nil
}

func (s *browserServiceClientStub) Start(
	_ context.Context,
	req *morphpb.StartBrowserRequest,
	_ ...grpc.CallOption,
) (*morphpb.StartBrowserResponse, error) {
	s.startRequest = req
	if s.err != nil {
		return nil, s.err
	}
	return &morphpb.StartBrowserResponse{Session: s.status.GetSessions()[0]}, nil
}

func (s *browserServiceClientStub) Stop(
	_ context.Context,
	req *morphpb.StopBrowserRequest,
	_ ...grpc.CallOption,
) (*morphpb.StopBrowserResponse, error) {
	s.stopRequest = req
	if s.err != nil {
		return nil, s.err
	}
	return &morphpb.StopBrowserResponse{Session: s.status.GetSessions()[0]}, nil
}

func (s *browserServiceClientStub) ReadArtifact(
	_ context.Context,
	req *morphpb.ReadBrowserArtifactRequest,
	_ ...grpc.CallOption,
) (grpc.ServerStreamingClient[morphpb.ReadBrowserArtifactResponse], error) {
	s.artifactRequest = req
	if s.err != nil {
		return nil, s.err
	}
	responses := s.artifactResponses
	if responses == nil {
		responses = []*morphpb.ReadBrowserArtifactResponse{
			{Artifact: s.artifact, Data: []byte("p")},
			{Data: []byte("ng")},
		}
	}
	return &browserArtifactClientStream{responses: responses, err: s.streamErr}, nil
}

func (s *browserServiceClientStub) EffectiveConfig(
	context.Context,
	*morphpb.GetBrowserEffectiveConfigRequest,
	...grpc.CallOption,
) (*morphpb.GetBrowserEffectiveConfigResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &morphpb.GetBrowserEffectiveConfigResponse{
		Enabled: true, CapabilityEnabled: true, DefaultProfile: "default", NetworkStrict: true,
		PermissionPreset: "approve", ExecutableConfigured: true,
	}, nil
}

type browserArtifactClientStream struct {
	grpc.ClientStream
	responses []*morphpb.ReadBrowserArtifactResponse
	index     int
	err       error
}

func (s *browserArtifactClientStream) Recv() (*morphpb.ReadBrowserArtifactResponse, error) {
	if s.index >= len(s.responses) {
		if s.err != nil {
			return nil, s.err
		}
		return nil, io.EOF
	}
	response := s.responses[s.index]
	s.index++
	return response, nil
}
