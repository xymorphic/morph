package client

import (
	"context"
	"errors"
	"io"

	"github.com/xymorphic/morph/internal/browser"
	"github.com/xymorphic/morph/internal/permissions"
	morphpb "github.com/xymorphic/morph/internal/rpc/proto"
)

func (s *BrowserService) Status(ctx context.Context) (browser.Status, error) {
	client, err := s.getClient()
	if err != nil {
		return browser.Status{}, err
	}
	prepareRPCConnection(s.reconnector)
	response, err := client.Status(ctx, &morphpb.GetBrowserStatusRequest{})
	if err != nil {
		return browser.Status{}, err
	}

	return browserStatusFromProto(response.GetStatus()), nil
}

func (s *BrowserService) Profiles(ctx context.Context) ([]browser.Profile, error) {
	client, err := s.getClient()
	if err != nil {
		return nil, err
	}
	prepareRPCConnection(s.reconnector)
	response, err := client.ListProfiles(ctx, &morphpb.ListBrowserProfilesRequest{})
	if err != nil {
		return nil, err
	}
	profiles := make([]browser.Profile, 0, len(response.GetProfiles()))
	for _, profile := range response.GetProfiles() {
		profiles = append(profiles, browserProfileFromProto(profile))
	}

	return profiles, nil
}

func (s *BrowserService) Sessions(ctx context.Context) ([]browser.Session, error) {
	client, err := s.getClient()
	if err != nil {
		return nil, err
	}
	prepareRPCConnection(s.reconnector)
	response, err := client.ListSessions(ctx, &morphpb.ListBrowserSessionsRequest{})
	if err != nil {
		return nil, err
	}
	sessions := make([]browser.Session, 0, len(response.GetSessions()))
	for _, session := range response.GetSessions() {
		sessions = append(sessions, browserSessionFromProto(session))
	}

	return sessions, nil
}

func (s *BrowserService) Start(
	ctx context.Context,
	profileName string,
	ownerSessionID string,
) (browser.Session, error) {
	client, err := s.getClient()
	if err != nil {
		return browser.Session{}, err
	}
	prepareRPCConnection(s.reconnector)
	response, err := client.Start(ctx, &morphpb.StartBrowserRequest{
		Profile: profileName, OwnerSessionId: ownerSessionID,
	})
	if err != nil {
		return browser.Session{}, err
	}

	return browserSessionFromProto(response.GetSession()), nil
}

func (s *BrowserService) Stop(
	ctx context.Context,
	id string,
	ownerSessionID string,
) (browser.Session, error) {
	client, err := s.getClient()
	if err != nil {
		return browser.Session{}, err
	}
	prepareRPCConnection(s.reconnector)
	response, err := client.Stop(ctx, &morphpb.StopBrowserRequest{Id: id, OwnerSessionId: ownerSessionID})
	if err != nil {
		return browser.Session{}, err
	}

	return browserSessionFromProto(response.GetSession()), nil
}

func (s *BrowserService) ReadArtifact(
	ctx context.Context,
	handle string,
	ownerSessionID string,
	runID string,
) (browser.ArtifactContent, error) {
	client, err := s.getClient()
	if err != nil {
		return browser.ArtifactContent{}, err
	}
	prepareRPCConnection(s.reconnector)
	stream, err := client.ReadArtifact(ctx, &morphpb.ReadBrowserArtifactRequest{
		Handle: handle, OwnerSessionId: ownerSessionID, RunId: runID,
	})
	if err != nil {
		return browser.ArtifactContent{}, err
	}
	var content browser.ArtifactContent
	metadataReceived := false
	for {
		response, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			if !metadataReceived {
				return browser.ArtifactContent{}, errors.New("morph: browser artifact stream is missing metadata")
			}
			if content.Artifact.Handle != handle {
				return browser.ArtifactContent{}, errors.New("morph: browser artifact stream handle does not match the request")
			}
			if content.Artifact.Size < 0 || int64(len(content.Data)) != content.Artifact.Size {
				return browser.ArtifactContent{}, errors.New("morph: browser artifact stream size does not match metadata")
			}
			return content, nil
		}
		if recvErr != nil {
			return browser.ArtifactContent{}, recvErr
		}
		if response.GetArtifact() != nil {
			if metadataReceived {
				return browser.ArtifactContent{}, errors.New("morph: browser artifact stream repeated metadata")
			}
			content.Artifact = browserArtifactFromProto(response.GetArtifact())
			metadataReceived = true
		} else if !metadataReceived {
			return browser.ArtifactContent{}, errors.New("morph: browser artifact stream data arrived before metadata")
		}
		content.Data = append(content.Data, response.GetData()...)
		if content.Artifact.Size >= 0 && int64(len(content.Data)) > content.Artifact.Size {
			return browser.ArtifactContent{}, errors.New("morph: browser artifact stream exceeds metadata size")
		}
	}
}

func (s *BrowserService) EffectiveConfig(ctx context.Context) (BrowserEffectiveConfig, error) {
	client, err := s.getClient()
	if err != nil {
		return BrowserEffectiveConfig{}, err
	}
	prepareRPCConnection(s.reconnector)
	response, err := client.EffectiveConfig(ctx, &morphpb.GetBrowserEffectiveConfigRequest{})
	if err != nil {
		return BrowserEffectiveConfig{}, err
	}

	return BrowserEffectiveConfig{
		Enabled: response.GetEnabled(), CapabilityEnabled: response.GetCapabilityEnabled(),
		DefaultProfile: response.GetDefaultProfile(), NetworkStrict: response.GetNetworkStrict(),
		PermissionPreset:     permissions.Preset(response.GetPermissionPreset()),
		ExecutableConfigured: response.GetExecutableConfigured(),
	}, nil
}

func (s *BrowserService) getClient() (morphpb.BrowserServiceClient, error) {
	if s != nil && s.client != nil {
		return s.client, nil
	}

	return nil, errors.New("morph: browser service client is required")
}

func browserStatusFromProto(value *morphpb.BrowserStatus) browser.Status {
	if value == nil {
		return browser.Status{}
	}
	result := browser.Status{Enabled: value.GetEnabled()}
	for _, profile := range value.GetProfiles() {
		result.Profiles = append(result.Profiles, browserProfileFromProto(profile))
	}
	for _, session := range value.GetSessions() {
		result.Sessions = append(result.Sessions, browserSessionFromProto(session))
	}

	return result
}

func browserProfileFromProto(value *morphpb.BrowserProfile) browser.Profile {
	if value == nil {
		return browser.Profile{}
	}

	return browser.Profile{
		Name: value.GetName(), Mode: value.GetMode(), Default: value.GetDefault(), Available: value.GetAvailable(),
		Warning: value.GetWarning(),
	}
}

func browserSessionFromProto(value *morphpb.BrowserSession) browser.Session {
	if value == nil {
		return browser.Session{}
	}

	return browser.Session{
		ID: value.GetId(), Profile: value.GetProfile(), ProfileMode: value.GetProfileMode(),
		State: browser.SessionState(value.GetState()), CreatedAt: protoTimestampToTime(value.GetCreatedAt()),
		LastActive: protoTimestampToTime(value.GetLastActive()), Error: value.GetError(), Warning: value.GetWarning(),
	}
}

func browserArtifactFromProto(value *morphpb.BrowserArtifact) browser.Artifact {
	if value == nil {
		return browser.Artifact{}
	}
	effects := make([]permissions.Effect, 0, len(value.GetEffects()))
	for _, effect := range value.GetEffects() {
		effects = append(effects, permissions.Effect(effect))
	}

	return browser.Artifact{
		Handle: value.GetHandle(), Kind: browser.ArtifactKind(value.GetKind()), Name: value.GetName(),
		MIMEType: value.GetMimeType(), Size: value.GetSize(), Profile: value.GetProfile(),
		SessionID: value.GetSessionId(), RunID: value.GetRunId(), Effects: effects,
		Sensitive: value.GetSensitive(), CreatedAt: protoTimestampToTime(value.GetCreatedAt()),
		ExpiresAt: protoTimestampToTime(value.GetExpiresAt()),
	}
}
