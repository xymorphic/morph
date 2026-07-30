package rpc

import (
	"context"
	"errors"
	"time"

	morphagent "github.com/wandxy/morph/internal/agent"
	"github.com/wandxy/morph/internal/permissions"
	morphpb "github.com/wandxy/morph/internal/rpc/proto"
	"github.com/wandxy/morph/internal/rpc/rpcmeta"
	agentsession "github.com/wandxy/morph/pkg/agent/session"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Service) EnqueueMessage(
	ctx context.Context,
	req *morphpb.EnqueueSessionMessageRequest,
) (*morphpb.EnqueueSessionMessageResponse, error) {
	api, err := s.getSessionQueueAPI()
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "enqueue message request is required")
	}
	ctx = sessionQueueAuthorizationContext(ctx, req.GetId())
	entry, err := api.EnqueueSessionMessage(ctx, agentsession.EnqueueRequest{
		SessionID:          req.GetId(),
		Content:            req.GetMessage(),
		Instruct:           req.GetInstruct(),
		Stream:             req.Stream,
		ClientSubmissionID: req.GetClientSubmissionId(),
		DeliveryMode:       agentsession.DeliveryMode(req.GetDeliveryMode()),
		SteeringFallback:   agentsession.SteeringFallback(req.GetSteeringFallback()),
	})
	if err != nil {
		return nil, getGRPCError(err)
	}
	return &morphpb.EnqueueSessionMessageResponse{Entry: sessionQueueEntryToProto(entry)}, nil
}

func (s *Service) State(
	ctx context.Context,
	req *morphpb.GetSessionStateRequest,
) (*morphpb.GetSessionStateResponse, error) {
	api, err := s.getSessionQueueAPI()
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "session state request is required")
	}
	ctx = sessionQueueAuthorizationContext(ctx, req.GetId())
	if err := s.checkPermission(ctx, permissions.Operation{
		Resource: permissions.ResourceSession,
		Action:   permissions.ActionRead,
		Effects:  []permissions.Effect{permissions.EffectRead},
		Target:   req.GetId(),
	}); err != nil {
		return nil, err
	}
	state, err := api.GetSessionExecutionState(ctx, req.GetId())
	if err != nil {
		return nil, getGRPCError(err)
	}
	queue := make([]*morphpb.SessionQueueEntry, len(state.Queue))
	for index, entry := range state.Queue {
		queue[index] = sessionQueueEntryToProto(entry)
	}
	progress := make([]*morphpb.SessionProgressEvent, len(state.Progress))
	for index, event := range state.Progress {
		progress[index] = &morphpb.SessionProgressEvent{
			Kind:         event.Kind,
			Channel:      event.Channel,
			Text:         event.Text,
			RunId:        event.RunID,
			QueueEntryId: event.QueueEntryID,
			Sequence:     event.Sequence,
			TraceEvent:   sessionProgressTraceEventToProto(event.TraceEvent),
		}
	}
	return &morphpb.GetSessionStateResponse{
		Id:                     state.SessionID,
		ActiveRun:              sessionActiveRunToProto(state.ActiveRun),
		Queue:                  queue,
		Cursor:                 state.Cursor,
		RetainedCursorFloor:    state.RetainedCursorFloor,
		QueueDepth:             int32(state.QueueDepth),
		OldestPendingCreatedAt: timeToProtoTimestamp(state.OldestPendingCreated),
		Progress:               progress,
		Reasoning:              sessionReasoningSettingsToProto(state.Reasoning, state.ActiveRun),
	}, nil
}

func (s *Service) SetReasoningEffort(
	ctx context.Context,
	req *morphpb.SetSessionReasoningEffortRequest,
) (*morphpb.SetSessionReasoningEffortResponse, error) {
	api, err := s.getSessionQueueAPI()
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "set reasoning effort request is required")
	}
	ctx = sessionQueueAuthorizationContext(ctx, req.GetId())
	if err := s.checkPermission(ctx, permissions.Operation{
		Resource: permissions.ResourceSession,
		Action:   permissions.ActionUpdate,
		Effects:  []permissions.Effect{permissions.EffectWrite},
		Target:   req.GetId(),
	}); err != nil {
		return nil, err
	}
	reasoning, err := api.SetSessionReasoningEffort(
		ctx,
		agentsession.SetReasoningEffortRequest{
			SessionID: req.GetId(),
			ExpectedModel: agentsession.ReasoningModelTuple{
				Provider: req.GetExpectedProvider(),
				API:      req.GetExpectedApi(),
				Model:    req.GetExpectedModel(),
			},
			Effort: agentsession.ReasoningEffort(req.GetEffort()),
			Reset:  req.GetReset_(),
		},
	)
	if err != nil {
		return nil, getGRPCError(err)
	}
	return &morphpb.SetSessionReasoningEffortResponse{
		Reasoning: sessionReasoningSettingsToProto(reasoning, nil),
	}, nil
}

func sessionReasoningSettingsToProto(
	settings agentsession.ReasoningSettings,
	activeRun *agentsession.ActiveRun,
) *morphpb.SessionReasoningSettings {
	efforts := make([]string, len(settings.SupportedEfforts))
	for index, effort := range settings.SupportedEfforts {
		efforts[index] = string(effort)
	}
	var snapshot *morphpb.ReasoningRunSnapshot
	if activeRun != nil {
		snapshot = reasoningRunSnapshotToProto(activeRun.Reasoning)
	} else if settings.ActiveRunSnapshot != nil {
		snapshot = reasoningRunSnapshotToProto(*settings.ActiveRunSnapshot)
	}
	return &morphpb.SessionReasoningSettings{
		Model: &morphpb.ReasoningModelTuple{
			Provider:    settings.Model.Provider,
			Api:         settings.Model.API,
			Model:       settings.Model.Model,
			DisplayName: settings.Model.DisplayName,
		},
		Reasoning:        settings.Reasoning,
		Adjustable:       settings.Adjustable,
		SupportedEfforts: efforts,
		SessionOverride:  string(settings.SessionOverride),
		ProfileDefault:   string(settings.ProfileDefault),
		CatalogDefault:   string(settings.CatalogDefault),
		EffectiveEffort:  string(settings.EffectiveEffort),
		DormantEffort:    string(settings.DormantEffort),
		Source:           string(settings.Source),
		Fallback:         string(settings.Fallback),
		SummarySupported: settings.SummarySupported,
		ActiveRun:        snapshot,
	}
}

func reasoningRunSnapshotToProto(
	snapshot agentsession.ReasoningSnapshot,
) *morphpb.ReasoningRunSnapshot {
	return &morphpb.ReasoningRunSnapshot{
		Provider: snapshot.Provider,
		Api:      snapshot.API,
		Model:    snapshot.Model,
		Effort:   string(snapshot.Effort),
		Summary:  snapshot.Summary,
	}
}

func (s *Service) Observe(
	req *morphpb.ObserveSessionRequest,
	stream morphpb.SessionService_ObserveServer,
) error {
	api, err := s.getSessionQueueAPI()
	if err != nil {
		return err
	}
	if req == nil {
		return status.Error(codes.InvalidArgument, "observe session request is required")
	}
	ctx := sessionQueueAuthorizationContext(stream.Context(), req.GetId())
	err = api.ObserveSessionEvents(ctx, req.GetId(), req.GetAfterCursor(), func(event agentsession.Event) error {
		return stream.Send(&morphpb.ObserveSessionResponse{Event: sessionEventToProto(event)})
	})
	if errors.Is(err, context.Canceled) {
		return nil
	}
	if errors.Is(err, agentsession.ErrCursorExpired) ||
		errors.Is(err, agentsession.ErrProgressExpired) {
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	return getGRPCError(err)
}

func (s *Service) EditQueuedMessage(
	ctx context.Context,
	req *morphpb.EditQueuedSessionMessageRequest,
) (*morphpb.EditQueuedSessionMessageResponse, error) {
	api, err := s.getSessionQueueAPI()
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "edit queued message request is required")
	}
	ctx = sessionQueueAuthorizationContext(ctx, req.GetId())
	entry, err := api.EditSessionQueueEntry(ctx, agentsession.QueueEditRequest{
		SessionID: req.GetId(),
		EntryID:   req.GetEntryId(),
		Content:   req.GetMessage(),
	})
	if err != nil {
		return nil, getGRPCError(err)
	}
	return &morphpb.EditQueuedSessionMessageResponse{Entry: sessionQueueEntryToProto(entry)}, nil
}

func (s *Service) RemoveQueuedMessage(
	ctx context.Context,
	req *morphpb.RemoveQueuedSessionMessageRequest,
) (*morphpb.RemoveQueuedSessionMessageResponse, error) {
	api, err := s.getSessionQueueAPI()
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "remove queued message request is required")
	}
	ctx = sessionQueueAuthorizationContext(ctx, req.GetId())
	entry, err := api.CancelSessionQueueEntry(ctx, agentsession.QueueMutationRequest{
		SessionID: req.GetId(),
		EntryID:   req.GetEntryId(),
	})
	if err != nil {
		return nil, getGRPCError(err)
	}
	return &morphpb.RemoveQueuedSessionMessageResponse{Entry: sessionQueueEntryToProto(entry)}, nil
}

func (s *Service) PromoteQueuedMessage(
	ctx context.Context,
	req *morphpb.PromoteQueuedSessionMessageRequest,
) (*morphpb.PromoteQueuedSessionMessageResponse, error) {
	api, err := s.getSessionQueueAPI()
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "promote queued message request is required")
	}
	ctx = sessionQueueAuthorizationContext(ctx, req.GetId())
	entry, err := api.PromoteSessionQueueEntry(ctx, agentsession.QueueMutationRequest{
		SessionID: req.GetId(),
		EntryID:   req.GetEntryId(),
	})
	if err != nil {
		return nil, getGRPCError(err)
	}
	return &morphpb.PromoteQueuedSessionMessageResponse{Entry: sessionQueueEntryToProto(entry)}, nil
}

func (s *Service) SteerQueuedMessage(
	ctx context.Context,
	req *morphpb.SteerQueuedSessionMessageRequest,
) (*morphpb.SteerQueuedSessionMessageResponse, error) {
	api, err := s.getSessionQueueAPI()
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "steer queued message request is required")
	}
	ctx = sessionQueueAuthorizationContext(ctx, req.GetId())
	entry, err := api.SteerSessionQueueEntry(ctx, agentsession.QueueMutationRequest{
		SessionID: req.GetId(),
		EntryID:   req.GetEntryId(),
	})
	if err != nil {
		return nil, getGRPCError(err)
	}
	return &morphpb.SteerQueuedSessionMessageResponse{Entry: sessionQueueEntryToProto(entry)}, nil
}

func (s *Service) InterruptRun(
	ctx context.Context,
	req *morphpb.InterruptSessionRunRequest,
) (*morphpb.InterruptSessionRunResponse, error) {
	api, err := s.getSessionQueueAPI()
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "interrupt run request is required")
	}
	ctx = sessionQueueAuthorizationContext(ctx, req.GetId())
	run, transitioned, err := api.InterruptSessionRun(ctx, req.GetId())
	if err != nil {
		return nil, getGRPCError(err)
	}
	var protoRun *morphpb.SessionActiveRun
	if run.ID != "" {
		protoRun = sessionActiveRunToProto(&run)
	}
	return &morphpb.InterruptSessionRunResponse{Run: protoRun, Transitioned: transitioned}, nil
}

func (s *Service) getSessionQueueAPI() (morphagent.SessionQueueAPI, error) {
	if s == nil {
		return nil, status.Error(codes.Internal, "service is required")
	}
	if s.api == nil {
		return nil, status.Error(codes.Internal, "agent handler is required")
	}
	api, ok := s.api.(morphagent.SessionQueueAPI)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "session queue is not supported")
	}
	return api, nil
}

func sessionQueueAuthorizationContext(ctx context.Context, sessionID string) context.Context {
	ctx = permissions.WithContext(ctx, permissions.AuthorizationContext{
		Actor:     rpcmeta.PermissionActorFromIncomingContext(ctx),
		Surface:   rpcmeta.PermissionSurfaceFromIncomingContext(ctx),
		SessionID: sessionID,
	})
	return withIncomingPermissionPreset(ctx)
}

func sessionQueueEntryToProto(entry agentsession.QueueEntry) *morphpb.SessionQueueEntry {
	return &morphpb.SessionQueueEntry{
		Id:                    entry.ID,
		SessionId:             entry.SessionID,
		Content:               entry.Content,
		Instruct:              entry.Instruct,
		Stream:                entry.Stream,
		ClientSubmissionId:    entry.ClientSubmissionID,
		TargetRunId:           entry.TargetRunID,
		RequestedDeliveryMode: string(entry.RequestedDeliveryMode),
		DeliveryMode:          string(entry.DeliveryMode),
		SteeringFallback:      string(entry.SteeringFallback),
		Status:                string(entry.Status),
		ActorKind:             entry.Provenance.ActorKind,
		ActorId:               entry.Provenance.ActorID,
		SurfaceKind:           entry.Provenance.SurfaceKind,
		Surface:               entry.Provenance.Surface,
		Profile:               entry.Provenance.Profile,
		Sequence:              entry.Sequence,
		Priority:              entry.Priority,
		CreatedAt:             timeToProtoTimestamp(entry.CreatedAt),
		UpdatedAt:             timeToProtoTimestamp(entry.UpdatedAt),
		StartedAt:             timeToProtoTimestamp(entry.StartedAt),
		CompletedAt:           timeToProtoTimestamp(entry.CompletedAt),
		LastError:             entry.LastError,
	}
}

func sessionActiveRunToProto(run *agentsession.ActiveRun) *morphpb.SessionActiveRun {
	if run == nil {
		return nil
	}
	return &morphpb.SessionActiveRun{
		Id:           run.ID,
		SessionId:    run.SessionID,
		QueueEntryId: run.QueueEntryID,
		Generation:   run.Generation,
		Status:       string(run.Status),
		StartedAt:    timeToProtoTimestamp(run.StartedAt),
		CompletedAt:  timeToProtoTimestamp(run.CompletedAt),
		UpdatedAt:    timeToProtoTimestamp(run.UpdatedAt),
		Reason:       run.Reason,
		LastError:    run.LastError,
		Reasoning: &morphpb.ReasoningRunSnapshot{
			Provider: run.Reasoning.Provider,
			Api:      run.Reasoning.API,
			Model:    run.Reasoning.Model,
			Effort:   string(run.Reasoning.Effort),
			Summary:  run.Reasoning.Summary,
		},
	}
}

func sessionEventToProto(event agentsession.Event) *morphpb.SessionEvent {
	return &morphpb.SessionEvent{
		SessionId: event.SessionID,
		Type:      string(event.Type),
		Cursor:    event.Cursor,
		CreatedAt: timeToProtoTimestamp(event.CreatedAt),
		QueueEntry: func() *morphpb.SessionQueueEntry {
			if event.Queue == nil {
				return nil
			}
			return sessionQueueEntryToProto(*event.Queue)
		}(),
		Run: sessionActiveRunToProto(event.Run),
		ProgressKind: func() string {
			if event.Progress == nil {
				return ""
			}
			return event.Progress.Kind
		}(),
		ProgressChannel: func() string {
			if event.Progress == nil {
				return ""
			}
			return event.Progress.Channel
		}(),
		ProgressText: func() string {
			if event.Progress == nil {
				return ""
			}
			return event.Progress.Text
		}(),
		ProgressRunId: func() string {
			if event.Progress == nil {
				return ""
			}
			return event.Progress.RunID
		}(),
		ProgressQueueEntryId: func() string {
			if event.Progress == nil {
				return ""
			}
			return event.Progress.QueueEntryID
		}(),
		ProgressSequence: func() int64 {
			if event.Progress == nil {
				return 0
			}
			return event.Progress.Sequence
		}(),
		ProgressTraceEvent: func() *morphpb.SessionTimelineTraceEvent {
			if event.Progress == nil {
				return nil
			}
			return sessionProgressTraceEventToProto(event.Progress.TraceEvent)
		}(),
	}
}

func sessionProgressTraceEventToProto(
	event *agentsession.TraceEvent,
) *morphpb.SessionTimelineTraceEvent {
	if event == nil {
		return nil
	}
	protoEvent, ok := timelineTraceEventToProto(*event)
	if !ok {
		return nil
	}
	return protoEvent
}

func timeToProtoTimestamp(value time.Time) *timestamppb.Timestamp {
	if value.IsZero() {
		return nil
	}
	return timestamppb.New(value.UTC())
}
