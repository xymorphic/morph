package client

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	agentapi "github.com/xymorphic/morph/internal/agent"
	"github.com/xymorphic/morph/internal/execution"
	morphpb "github.com/xymorphic/morph/internal/rpc/proto"
	storage "github.com/xymorphic/morph/internal/state/core"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	agentsession "github.com/xymorphic/morph/pkg/agent/session"
	"github.com/xymorphic/morph/pkg/str"
)

func (s *SessionService) Create(ctx context.Context, id string) (storage.Session, error) {
	return s.CreateWithOptions(ctx, CreateSessionOptions{ID: id})
}

func (s *SessionService) CreateWithOptions(ctx context.Context, opts CreateSessionOptions) (storage.Session, error) {
	client, err := s.getClient()
	if err != nil {
		return storage.Session{}, err
	}
	id := str.String(opts.ID)
	originSource := str.String(opts.OriginSource)
	req := &morphpb.CreateSessionRequest{
		Id:           id.Trim(),
		OriginSource: originSource.Trim(),
	}
	if opts.AutoSwitch != nil {
		req.AutoSwitch = opts.AutoSwitch
	}

	prepareRPCConnection(s.reconnector)
	resp, err := client.Create(ctx, req)
	if err != nil {
		return storage.Session{}, err
	}

	if resp.GetSession() == nil {
		return storage.Session{}, nil
	}

	return protoSessionSummaryToSession(resp.GetSession()), nil
}

func (s *SessionService) List(ctx context.Context, opts ...SessionListOptions) ([]storage.Session, error) {
	client, err := s.getClient()
	if err != nil {
		return nil, err
	}

	listOpts := getSessionListOptions(opts...)
	prepareRPCConnection(s.reconnector)
	originSource := str.String(listOpts.OriginSource)
	resp, err := client.List(ctx, &morphpb.ListSessionsRequest{
		Archived:     listOpts.Archived,
		OriginSource: originSource.Trim(),
	})
	if err != nil {
		return nil, err
	}

	items := make([]storage.Session, 0, len(resp.GetSessions()))
	for _, session := range resp.GetSessions() {
		item := protoSessionSummaryToSession(session)
		if listOpts.Archived != nil {
			item.Archived = *listOpts.Archived
		}
		items = append(items, item)
	}

	return items, nil
}

func getSessionListOptions(opts ...SessionListOptions) SessionListOptions {
	if len(opts) == 0 {
		active := false
		return SessionListOptions{Archived: &active}
	}

	return opts[0]
}

func (s *SessionService) Use(ctx context.Context, id string) error {
	client, err := s.getClient()
	if err != nil {
		return err
	}

	prepareRPCConnection(s.reconnector)
	idValue := str.String(id)
	_, err = client.Use(ctx, &morphpb.UseSessionRequest{Id: idValue.Trim()})
	return err
}

func (s *SessionService) Archive(ctx context.Context, id string) error {
	client, err := s.getClient()
	if err != nil {
		return err
	}

	prepareRPCConnection(s.reconnector)
	idValue := str.String(id)
	_, err = client.Archive(ctx, &morphpb.ArchiveSessionRequest{Id: idValue.Trim()})
	return err
}

func (s *SessionService) ListExecutionEnvironments(ctx context.Context, sessionID string) ([]execution.EnvironmentDetails, error) {
	client, err := s.getClient()
	if err != nil {
		return nil, err
	}
	prepareRPCConnection(s.reconnector)
	response, err := client.ListExecutionEnvironments(ctx, &morphpb.ListExecutionEnvironmentsRequest{SessionId: sessionID})
	if err != nil {
		return nil, err
	}
	result := make([]execution.EnvironmentDetails, 0, len(response.GetEnvironments()))
	for _, environment := range response.GetEnvironments() {
		result = append(result, executionEnvironmentFromProto(environment))
	}
	return result, nil
}

func (s *SessionService) ExplainExecutionEnvironment(
	ctx context.Context,
	sessionID string,
	environmentID string,
) (execution.EnvironmentDetails, error) {
	client, err := s.getClient()
	if err != nil {
		return execution.EnvironmentDetails{}, err
	}
	prepareRPCConnection(s.reconnector)
	response, err := client.ExplainExecutionEnvironment(ctx, &morphpb.ExplainExecutionEnvironmentRequest{
		SessionId:     sessionID,
		EnvironmentId: environmentID,
	})
	if err != nil {
		return execution.EnvironmentDetails{}, err
	}
	return executionEnvironmentFromProto(response.GetEnvironment()), nil
}

func executionEnvironmentFromProto(value *morphpb.ExecutionEnvironment) execution.EnvironmentDetails {
	if value == nil {
		return execution.EnvironmentDetails{}
	}
	mounts := make([]execution.Mount, 0, len(value.GetMounts()))
	for _, mount := range value.GetMounts() {
		mounts = append(mounts, execution.Mount{
			Name:    mount.GetName(),
			Target:  mount.GetTarget(),
			Mode:    execution.MountMode(mount.GetMode()),
			Purpose: mount.GetPurpose(),
		})
	}
	limits := value.GetLimits()
	details := execution.EnvironmentDetails{
		Status: execution.EnvironmentStatus{
			ID:                   value.GetId(),
			Backend:              execution.Backend(value.GetBackend()),
			Scope:                execution.Scope(value.GetScope()),
			State:                execution.EnvironmentState(value.GetState()),
			WorkspaceMode:        execution.WorkspaceMode(value.GetWorkspaceMode()),
			Network:              execution.NetworkMode(value.GetNetwork()),
			ImageDigest:          value.GetImageDigest(),
			SecurityGeneration:   value.GetSecurityGeneration(),
			DaemonIncarnation:    value.GetDaemonIncarnation(),
			ContainerIncarnation: value.GetContainerIncarnation(),
			ParticipantCount:     int(value.GetParticipantCount()),
			ProcessCount:         int(value.GetProcessCount()),
			FailureCode:          value.GetFailureCode(),
		},
		Mounts:           mounts,
		SecretReferences: value.GetSecretReferences(),
		PolicyHash:       value.GetPolicyHash(),
		ImageContract:    value.GetImageContractDigest(),
	}
	if limits != nil {
		details.Limits = execution.Limits{
			MemoryBytes:    limits.GetMemoryBytes(),
			CPUMilli:       limits.GetCpuMilli(),
			PIDs:           limits.GetPids(),
			OpenFiles:      limits.GetOpenFiles(),
			TemporaryBytes: limits.GetTemporaryBytes(),
			OutputBytes:    limits.GetOutputBytes(),
			Runtime:        time.Duration(limits.GetRuntimeMillis()) * time.Millisecond,
		}
	}
	return details
}

func (s *SessionService) Unarchive(ctx context.Context, id string) (storage.Session, error) {
	client, err := s.getClient()
	if err != nil {
		return storage.Session{}, err
	}

	prepareRPCConnection(s.reconnector)
	idValue := str.String(id)
	resp, err := client.Unarchive(ctx, &morphpb.UnarchiveSessionRequest{Id: idValue.Trim()})
	if err != nil {
		return storage.Session{}, err
	}

	return protoSessionSummaryToSession(resp.GetSession()), nil
}

func (s *SessionService) Rename(ctx context.Context, id string, title string) (storage.Session, error) {
	client, err := s.getClient()
	if err != nil {
		return storage.Session{}, err
	}

	prepareRPCConnection(s.reconnector)
	idValue := str.String(id)
	titleValue := str.String(title)
	resp, err := client.Rename(ctx, &morphpb.RenameSessionRequest{
		Id:    idValue.Trim(),
		Title: titleValue.Trim(),
	})
	if err != nil {
		return storage.Session{}, err
	}

	return protoSessionSummaryToSession(resp.GetSession()), nil
}

func (s *SessionService) Current(ctx context.Context) (storage.Session, error) {
	client, err := s.getClient()
	if err != nil {
		return storage.Session{}, err
	}

	prepareRPCConnection(s.reconnector)
	resp, err := client.Current(ctx, &morphpb.CurrentSessionRequest{})
	if err != nil {
		return storage.Session{}, err
	}

	return storage.Session{
		ID:          resp.GetId(),
		Title:       resp.GetTitle(),
		TitleSource: resp.GetTitleSource(),
	}, nil
}

func (s *SessionService) Compact(ctx context.Context, id string) (CompactSessionResult, error) {
	client, err := s.getClient()
	if err != nil {
		return CompactSessionResult{}, err
	}

	prepareRPCConnection(s.reconnector)
	idValue := str.String(id)
	resp, err := client.Compact(ctx, &morphpb.CompactSessionRequest{Id: idValue.Trim()})
	if err != nil {
		return CompactSessionResult{}, err
	}

	return CompactSessionResult{
		SessionID:            resp.GetId(),
		SourceEndOffset:      int(resp.GetSourceEndOffset()),
		SourceMessageCount:   int(resp.GetSourceMessageCount()),
		UpdatedAt:            protoTimestampToTime(resp.GetUpdatedAt()),
		CurrentContextLength: int(resp.GetCurrentContextLength()),
		TotalContextLength:   int(resp.GetTotalContextLength()),
	}, nil
}

func (s *SessionService) Repair(
	ctx context.Context,
	opts RepairSessionOptions,
) (RepairSessionResult, error) {
	client, err := s.getClient()
	if err != nil {
		return RepairSessionResult{}, err
	}

	prepareRPCConnection(s.reconnector)
	sessionID := str.String(opts.SessionID)
	resp, err := client.Repair(ctx, &morphpb.RepairSessionRequest{
		Type: morphpb.RepairSessionRequest_VECTOR,
		Vector: &morphpb.VectorRepairOption{
			Id:   sessionID.Trim(),
			Full: opts.Full,
		},
	})
	if err != nil {
		return RepairSessionResult{}, err
	}
	vector := resp.GetVector()

	return RepairSessionResult{
		SessionsScanned:    int(vector.GetSessionsScanned()),
		MessagesScanned:    int(vector.GetMessagesScanned()),
		RowsScanned:        int(vector.GetRowsScanned()),
		MissingRows:        int(vector.GetMissingRows()),
		StaleRows:          int(vector.GetStaleRows()),
		UnchangedRows:      int(vector.GetUnchangedRows()),
		RebuiltRows:        int(vector.GetRebuiltRows()),
		DeletedSources:     int(vector.GetDeletedSources()),
		Batches:            int(vector.GetBatches()),
		AttemptedSources:   int(vector.GetAttemptedSources()),
		RecoveredSources:   int(vector.GetRecoveredSources()),
		StillFailedSources: int(vector.GetStillFailedSources()),
	}, nil
}

func (s *SessionService) Status(ctx context.Context, id string) (ContextStatus, error) {
	client, err := s.getClient()
	if err != nil {
		return ContextStatus{}, err
	}

	prepareRPCConnection(s.reconnector)
	idValue := str.String(id)
	resp, err := client.Status(ctx, &morphpb.GetSessionStatusRequest{
		Context: &morphpb.GetSessionStatusRequestContext{Id: idValue.Trim()},
	})
	if err != nil {
		return ContextStatus{}, err
	}
	cctx := resp.GetContext()
	if cctx == nil {
		return ContextStatus{}, fmt.Errorf("morph: get session status response context is required")
	}

	return ContextStatus{
		SessionID:        resp.GetId(),
		Offset:           int(cctx.GetOffset()),
		Size:             int(resp.GetSize()),
		Length:           int(cctx.GetLength()),
		Used:             int(cctx.GetUsed()),
		Remaining:        int(cctx.GetRemaining()),
		UsedPct:          cctx.GetUsedPct(),
		RemainingPct:     cctx.GetRemainingPct(),
		UsageSource:      cctx.GetUsageSource(),
		CreatedAt:        protoTimestampToTime(resp.GetCreatedAt()),
		UpdatedAt:        protoTimestampToTime(resp.GetUpdatedAt()),
		CompactionStatus: resp.GetCompactionStatus(),
	}, nil
}

func (s *SessionService) Timeline(
	ctx context.Context,
	opts SessionTimelineOptions,
) (SessionTimeline, error) {
	client, err := s.getClient()
	if err != nil {
		return SessionTimeline{}, err
	}

	prepareRPCConnection(s.reconnector)
	sessionID := str.String(opts.SessionID)
	resp, err := client.Timeline(ctx, &morphpb.GetSessionTimelineRequest{
		Id:            sessionID.Trim(),
		MessageOffset: int32(opts.MessageOffset),
		MessageLimit:  int32(opts.MessageLimit),
		TraceOffset:   int32(opts.TraceOffset),
		TraceLimit:    int32(opts.TraceLimit),
	})
	if err != nil {
		return SessionTimeline{}, err
	}

	return protoSessionTimelineToTimeline(resp)
}

func (s *SessionService) getClient() (morphpb.SessionServiceClient, error) {
	if s != nil && s.client != nil {
		return s.client, nil
	}

	return nil, fmt.Errorf("morph: session service client is required")
}

func protoSessionTimelineToTimeline(resp *morphpb.GetSessionTimelineResponse) (SessionTimeline, error) {
	if resp == nil {
		return SessionTimeline{}, fmt.Errorf("morph: get session timeline response is required")
	}

	timeline := SessionTimeline{
		SessionID:             resp.GetId(),
		Title:                 resp.GetTitle(),
		TitleSource:           resp.GetTitleSource(),
		MessagesHasMore:       resp.GetMessagesHasMore(),
		TracesHasMore:         resp.GetTracesHasMore(),
		TracesTruncatedBefore: resp.GetTracesTruncatedBefore(),
		FirstTraceSequence:    int(resp.GetFirstTraceSequence()),
		LastTraceSequence:     int(resp.GetLastTraceSequence()),
		Messages:              make([]agentapi.SessionTimelineMessage, 0, len(resp.GetMessages())),
		TraceEvents:           make([]agentapi.SessionTimelineTraceEvent, 0, len(resp.GetTraceEvents())),
	}
	for _, message := range resp.GetMessages() {
		timeline.Messages = append(timeline.Messages, timelineMessageFromProto(message))
	}
	for _, event := range resp.GetTraceEvents() {
		timelineEvent, err := timelineTraceEventFromProto(event)
		if err != nil {
			return SessionTimeline{}, err
		}
		timeline.TraceEvents = append(timeline.TraceEvents, timelineEvent)
	}

	return timeline, nil
}

func timelineMessageFromProto(message *morphpb.SessionTimelineMessage) agentapi.SessionTimelineMessage {
	if message == nil {
		return agentapi.SessionTimelineMessage{}
	}

	toolCalls := make([]morphmsg.ToolCall, 0, len(message.GetToolCalls()))
	for _, toolCall := range message.GetToolCalls() {
		id := str.String(toolCall.GetId())
		name := str.String(toolCall.GetName())
		input := str.String(toolCall.GetInput())
		toolCalls = append(toolCalls, morphmsg.ToolCall{
			ID:    id.Trim(),
			Name:  name.Trim(),
			Input: input.Trim(),
		})
	}

	return agentapi.SessionTimelineMessage{
		Offset: int(message.GetOffset()),
		Message: morphmsg.Message{
			ID:         uint(message.GetId()),
			Role:       morphmsg.Role(message.GetRole()),
			Name:       message.GetName(),
			ToolCallID: message.GetToolCallId(),
			Content:    message.GetContent(),
			CreatedAt:  protoTimestampToTime(message.GetCreatedAt()),
			ToolCalls:  toolCalls,
		},
	}
}

func timelineTraceEventFromProto(event *morphpb.SessionTimelineTraceEvent) (agentapi.SessionTimelineTraceEvent, error) {
	if event == nil {
		return agentapi.SessionTimelineTraceEvent{}, nil
	}

	var payload any
	payloadValue := str.String(event.GetPayloadJson())
	if payloadJSON := payloadValue.Trim(); payloadJSON != "" {
		if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
			return agentapi.SessionTimelineTraceEvent{}, err
		}
	}
	eventType := str.String(event.GetType())
	return agentapi.SessionTimelineTraceEvent{
		Event: agentsession.TraceEvent{
			ID:        uint(event.GetId()),
			Sequence:  int(event.GetSequence()),
			Type:      eventType.Trim(),
			Timestamp: protoTimestampToTime(event.GetTimestamp()),
			Payload:   payload,
		},
	}, nil
}

func protoSessionSummaryToSession(summary *morphpb.SessionSummary) storage.Session {
	if summary == nil {
		return storage.Session{}
	}

	return storage.Session{
		ID: summary.GetId(),
		Origin: storage.SessionOrigin{
			Source: summary.GetOriginSource(),
		},
		Title:       summary.GetTitle(),
		TitleSource: summary.GetTitleSource(),
		UpdatedAt:   time.Unix(summary.GetUpdatedAtUnix(), 0).UTC(),
	}
}
