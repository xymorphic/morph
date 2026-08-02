package rpc

import (
	"context"
	"errors"
	"strings"

	"github.com/xymorphic/morph/internal/browser"
	"github.com/xymorphic/morph/internal/permissions"
	morphpb "github.com/xymorphic/morph/internal/rpc/proto"
	"github.com/xymorphic/morph/internal/rpc/rpcmeta"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const defaultBrowserRPCOwnerSession = "browser-cli"
const browserArtifactChunkSize = 64 << 10

type BrowserService struct {
	morphpb.UnimplementedBrowserServiceServer
	service *Service
}

func NewBrowserService(service *Service) *BrowserService {
	return &BrowserService{service: service}
}

func (s *BrowserService) Status(
	ctx context.Context,
	req *morphpb.GetBrowserStatusRequest,
) (*morphpb.GetBrowserStatusResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "browser status request is required")
	}
	ctx, err := s.getOwnerContext(ctx, "", "")
	if err != nil {
		return nil, err
	}
	if err := s.checkOperation(ctx, permissions.ActionRead, []permissions.Effect{permissions.EffectRead}, "status"); err != nil {
		return nil, err
	}
	return &morphpb.GetBrowserStatusResponse{Status: browserStatusToProto(s.getStatus())}, nil
}

func (s *BrowserService) ListProfiles(
	ctx context.Context,
	req *morphpb.ListBrowserProfilesRequest,
) (*morphpb.ListBrowserProfilesResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "browser profiles request is required")
	}
	ctx, err := s.getOwnerContext(ctx, "", "")
	if err != nil {
		return nil, err
	}
	if err := s.checkOperation(ctx, permissions.ActionList, []permissions.Effect{permissions.EffectRead}, "profiles"); err != nil {
		return nil, err
	}
	statusValue := s.getStatus()
	profiles := make([]*morphpb.BrowserProfile, 0, len(statusValue.Profiles))
	for _, profile := range statusValue.Profiles {
		profiles = append(profiles, browserProfileToProto(profile))
	}

	return &morphpb.ListBrowserProfilesResponse{Profiles: profiles}, nil
}

func (s *BrowserService) ListSessions(
	ctx context.Context,
	req *morphpb.ListBrowserSessionsRequest,
) (*morphpb.ListBrowserSessionsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "browser sessions request is required")
	}
	ctx, err := s.getOwnerContext(ctx, "", "")
	if err != nil {
		return nil, err
	}
	if err := s.checkOperation(ctx, permissions.ActionList, []permissions.Effect{permissions.EffectRead}, "sessions"); err != nil {
		return nil, err
	}
	statusValue := s.getStatus()
	sessions := make([]*morphpb.BrowserSession, 0, len(statusValue.Sessions))
	for _, session := range statusValue.Sessions {
		sessions = append(sessions, browserSessionToProto(session))
	}

	return &morphpb.ListBrowserSessionsResponse{Sessions: sessions}, nil
}

func (s *BrowserService) Start(
	ctx context.Context,
	req *morphpb.StartBrowserRequest,
) (*morphpb.StartBrowserResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "browser start request is required")
	}
	ctx, err := s.getOwnerContext(ctx, req.GetOwnerSessionId(), "")
	if err != nil {
		return nil, err
	}
	profileName := strings.TrimSpace(req.GetProfile())
	if profileName == "" {
		profileName = s.service.browserConfig.DefaultProfile
	}
	if err := s.checkOperation(
		ctx,
		permissions.ActionStart,
		[]permissions.Effect{permissions.EffectExecution},
		"profile:"+profileName,
	); err != nil {
		return nil, err
	}
	runtime, err := s.getRuntime()
	if err != nil {
		return nil, err
	}
	session, err := runtime.Start(ctx, browser.StartRequest{Profile: req.GetProfile()})
	if err != nil {
		return nil, getBrowserGRPCError(err)
	}

	return &morphpb.StartBrowserResponse{Session: browserSessionToProto(session)}, nil
}

func (s *BrowserService) Stop(
	ctx context.Context,
	req *morphpb.StopBrowserRequest,
) (*morphpb.StopBrowserResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "browser stop request is required")
	}
	if strings.TrimSpace(req.GetId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "browser session id is required")
	}
	ctx, err := s.getOwnerContext(ctx, req.GetOwnerSessionId(), "")
	if err != nil {
		return nil, err
	}
	if err := s.checkOperation(
		ctx,
		permissions.ActionStop,
		[]permissions.Effect{
			permissions.EffectWrite,
			permissions.EffectExecution,
			permissions.EffectDestructive,
		},
		"session:"+strings.TrimSpace(req.GetId()),
	); err != nil {
		return nil, err
	}
	runtime, err := s.getRuntime()
	if err != nil {
		return nil, err
	}
	session, err := runtime.Stop(ctx, req.GetId())
	if err != nil {
		return nil, getBrowserGRPCError(err)
	}

	return &morphpb.StopBrowserResponse{Session: browserSessionToProto(session)}, nil
}

func (s *BrowserService) ReadArtifact(
	req *morphpb.ReadBrowserArtifactRequest,
	stream morphpb.BrowserService_ReadArtifactServer,
) error {
	if req == nil {
		return status.Error(codes.InvalidArgument, "browser artifact request is required")
	}
	if strings.TrimSpace(req.GetHandle()) == "" {
		return status.Error(codes.InvalidArgument, "browser artifact handle is required")
	}
	ctx := stream.Context()
	ctx, err := s.getOwnerContext(ctx, req.GetOwnerSessionId(), req.GetRunId())
	if err != nil {
		return err
	}
	if err := s.checkOperation(
		ctx,
		permissions.ActionRead,
		[]permissions.Effect{permissions.EffectRead},
		"artifact:"+strings.TrimSpace(req.GetHandle()),
	); err != nil {
		return err
	}
	runtime, err := s.getRuntime()
	if err != nil {
		return err
	}
	content, err := runtime.ReadArtifact(ctx, req.GetHandle())
	if err != nil {
		return getBrowserGRPCError(err)
	}
	artifact := browserArtifactToProto(content.Artifact)
	if len(content.Data) == 0 {
		return stream.Send(&morphpb.ReadBrowserArtifactResponse{Artifact: artifact})
	}
	for offset := 0; offset < len(content.Data); offset += browserArtifactChunkSize {
		end := min(offset+browserArtifactChunkSize, len(content.Data))
		response := &morphpb.ReadBrowserArtifactResponse{Data: append([]byte(nil), content.Data[offset:end]...)}
		if offset == 0 {
			response.Artifact = artifact
		}
		if err := stream.Send(response); err != nil {
			return err
		}
	}

	return nil
}

func (s *BrowserService) EffectiveConfig(
	ctx context.Context,
	req *morphpb.GetBrowserEffectiveConfigRequest,
) (*morphpb.GetBrowserEffectiveConfigResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "browser effective config request is required")
	}
	ctx, err := s.getOwnerContext(ctx, "", "")
	if err != nil {
		return nil, err
	}
	if err := s.checkOperation(ctx, permissions.ActionRead, []permissions.Effect{permissions.EffectRead}, "config"); err != nil {
		return nil, err
	}

	return &morphpb.GetBrowserEffectiveConfigResponse{
		Enabled:              s.service.browserConfig.Enabled,
		CapabilityEnabled:    s.service.browserCapability,
		DefaultProfile:       s.service.browserConfig.DefaultProfile,
		NetworkStrict:        s.service.browserConfig.Network.StrictEnabled(),
		PermissionPreset:     string(s.service.permissions.Preset(ctx)),
		ExecutableConfigured: strings.TrimSpace(s.service.browserConfig.Executable) != "",
	}, nil
}

func (s *BrowserService) getRuntime() (BrowserAPI, error) {
	if s == nil || s.service == nil {
		return nil, status.Error(codes.Internal, "browser RPC service is required")
	}
	if s.service.browser == nil {
		return nil, status.Error(codes.FailedPrecondition, "browser service is unavailable")
	}

	return s.service.browser, nil
}

func (s *BrowserService) getStatus() browser.Status {
	if s == nil || s.service == nil {
		return browser.Status{}
	}
	if s.service.browser != nil {
		return s.service.browser.Status()
	}

	profiles := make([]browser.Profile, 0, len(s.service.browserConfig.Profiles))
	for _, profile := range s.service.browserConfig.Profiles {
		profiles = append(profiles, browser.Profile{
			Name: profile.Name, Mode: profile.Mode,
			Default: profile.Name == s.service.browserConfig.DefaultProfile,
			Warning: browser.GetProfileWarning(profile),
		})
	}

	return browser.Status{Enabled: s.service.browserConfig.Enabled, Profiles: profiles}
}

func (s *BrowserService) getOwnerContext(
	ctx context.Context,
	ownerSessionID string,
	runID string,
) (context.Context, error) {
	if s == nil || s.service == nil {
		return nil, status.Error(codes.Internal, "browser RPC service is required")
	}
	actor := rpcmeta.PermissionActorFromIncomingContext(ctx)
	if actor.Kind != permissions.ActorLocalOwner || strings.TrimSpace(actor.ID) == "" {
		return nil, status.Error(codes.Unauthenticated, "authenticated local owner is required")
	}
	profileName := strings.TrimSpace(s.service.profileName)
	if profileName == "" {
		return nil, status.Error(codes.FailedPrecondition, "active profile is unavailable")
	}
	ownerSessionID = strings.TrimSpace(ownerSessionID)
	if ownerSessionID == "" {
		ownerSessionID = defaultBrowserRPCOwnerSession
	}
	ctx = permissions.WithContext(ctx, permissions.AuthorizationContext{
		Actor: actor, Surface: rpcmeta.PermissionSurfaceFromIncomingContext(ctx), Profile: profileName,
		SessionID: ownerSessionID, RunID: strings.TrimSpace(runID),
	})

	return withIncomingPermissionPreset(ctx), nil
}

func (s *BrowserService) checkOperation(
	ctx context.Context,
	action permissions.Action,
	effects []permissions.Effect,
	target string,
) error {
	operation, err := (permissions.Operation{
		Tool: "browser", Resource: permissions.ResourceBrowser, Action: action, Effects: effects,
		Target: target, OwnerRequired: true,
	}).Normalize()
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}

	return getBrowserGRPCError(s.service.checkPermission(ctx, operation))
}

func browserStatusToProto(value browser.Status) *morphpb.BrowserStatus {
	profiles := make([]*morphpb.BrowserProfile, 0, len(value.Profiles))
	for _, profile := range value.Profiles {
		profiles = append(profiles, browserProfileToProto(profile))
	}
	sessions := make([]*morphpb.BrowserSession, 0, len(value.Sessions))
	for _, session := range value.Sessions {
		sessions = append(sessions, browserSessionToProto(session))
	}

	return &morphpb.BrowserStatus{Enabled: value.Enabled, Profiles: profiles, Sessions: sessions}
}

func browserProfileToProto(value browser.Profile) *morphpb.BrowserProfile {
	return &morphpb.BrowserProfile{
		Name: value.Name, Mode: value.Mode, Default: value.Default, Available: value.Available, Warning: value.Warning,
	}
}

func browserSessionToProto(value browser.Session) *morphpb.BrowserSession {
	return &morphpb.BrowserSession{
		Id: value.ID, Profile: value.Profile, ProfileMode: value.ProfileMode, State: string(value.State),
		CreatedAt: timeToProto(value.CreatedAt), LastActive: timeToProto(value.LastActive),
		Error: value.Error, Warning: value.Warning,
	}
}

func browserArtifactToProto(value browser.Artifact) *morphpb.BrowserArtifact {
	effects := make([]string, 0, len(value.Effects))
	for _, effect := range value.Effects {
		effects = append(effects, string(effect))
	}

	return &morphpb.BrowserArtifact{
		Handle: value.Handle, Kind: string(value.Kind), Name: value.Name, MimeType: value.MIMEType,
		Size: value.Size, Profile: value.Profile, SessionId: value.SessionID, RunId: value.RunID,
		Effects: effects, Sensitive: value.Sensitive,
		CreatedAt: timeToProto(value.CreatedAt), ExpiresAt: timeToProto(value.ExpiresAt),
	}
}

func getBrowserGRPCError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	if decisionErr, ok := permissions.GetDecisionError(err); ok {
		switch decisionErr.Code {
		case permissions.ErrorCodeApprovalRequired:
			return status.Error(codes.FailedPrecondition, decisionErr.Error())
		case permissions.ErrorCodeApprovalRateLimited:
			return status.Error(codes.ResourceExhausted, decisionErr.Error())
		default:
			return status.Error(codes.PermissionDenied, decisionErr.Error())
		}
	}
	if browserErr, ok := browser.GetError(err); ok {
		switch browserErr.Code {
		case browser.ErrorInvalidRequest:
			return status.Error(codes.InvalidArgument, browserErr.Error())
		case browser.ErrorNotFound:
			return status.Error(codes.NotFound, browserErr.Error())
		case browser.ErrorOwnership:
			return status.Error(codes.PermissionDenied, browserErr.Error())
		case browser.ErrorClosed, browser.ErrorNotReady, browser.ErrorStaleReference:
			return status.Error(codes.FailedPrecondition, browserErr.Error())
		case browser.ErrorUnavailable, browser.ErrorStartFailed, browser.ErrorHealthFailed:
			return status.Error(codes.Unavailable, browserErr.Error())
		case browser.ErrorTimeout:
			return status.Error(codes.DeadlineExceeded, browserErr.Error())
		case browser.ErrorCancelled:
			return status.Error(codes.Canceled, browserErr.Error())
		}
	}
	if errors.Is(err, context.Canceled) {
		return status.Error(codes.Canceled, err.Error())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return status.Error(codes.DeadlineExceeded, err.Error())
	}
	return status.Error(codes.Internal, err.Error())
}
