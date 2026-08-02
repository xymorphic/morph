package client

import (
	"context"
	"errors"
	"io"

	morphpb "github.com/xymorphic/morph/internal/rpc/proto"
	agentsession "github.com/xymorphic/morph/pkg/agent/session"
)

func (s *SessionService) EnqueueMessage(
	ctx context.Context,
	opts EnqueueMessageOptions,
) (SessionQueueEntry, error) {
	client, err := s.getClient()
	if err != nil {
		return SessionQueueEntry{}, err
	}
	prepareRPCConnection(s.reconnector)
	response, err := client.EnqueueMessage(ctx, &morphpb.EnqueueSessionMessageRequest{
		Id:                 opts.SessionID,
		Message:            opts.Message,
		Instruct:           opts.Instruct,
		Stream:             opts.Stream,
		ClientSubmissionId: opts.ClientSubmissionID,
		DeliveryMode:       string(opts.DeliveryMode),
		SteeringFallback:   string(opts.SteeringFallback),
	})
	if err != nil {
		return SessionQueueEntry{}, err
	}
	return protoToSessionQueueEntry(response.GetEntry()), nil
}

func (s *SessionService) State(
	ctx context.Context,
	sessionID string,
) (SessionExecutionState, error) {
	client, err := s.getClient()
	if err != nil {
		return SessionExecutionState{}, err
	}
	prepareRPCConnection(s.reconnector)
	response, err := client.State(ctx, &morphpb.GetSessionStateRequest{Id: sessionID})
	if err != nil {
		return SessionExecutionState{}, err
	}
	queue := make([]agentsession.QueueEntry, len(response.GetQueue()))
	for index, entry := range response.GetQueue() {
		queue[index] = protoToSessionQueueEntry(entry)
	}
	progress := make([]agentsession.ProgressEvent, len(response.GetProgress()))
	for index, event := range response.GetProgress() {
		traceEvent, err := sessionProgressTraceEventFromProto(event.GetTraceEvent())
		if err != nil {
			return SessionExecutionState{}, err
		}
		progress[index] = agentsession.ProgressEvent{
			RunID:        event.GetRunId(),
			QueueEntryID: event.GetQueueEntryId(),
			Kind:         event.GetKind(),
			Channel:      event.GetChannel(),
			Text:         event.GetText(),
			Sequence:     event.GetSequence(),
			TraceEvent:   traceEvent,
		}
	}
	return SessionExecutionState{
		SessionID:           response.GetId(),
		ActiveRun:           protoToSessionActiveRun(response.GetActiveRun()),
		Queue:               queue,
		Cursor:              response.GetCursor(),
		RetainedCursorFloor: response.GetRetainedCursorFloor(),
		QueueDepth:          int(response.GetQueueDepth()),
		OldestPendingCreated: protoTimestampToTime(
			response.GetOldestPendingCreatedAt(),
		),
		Progress:  progress,
		Reasoning: protoToSessionReasoningSettings(response.GetReasoning()),
	}, nil
}

func (s *SessionService) SetReasoningEffort(
	ctx context.Context,
	opts SetReasoningEffortOptions,
) (agentsession.ReasoningSettings, error) {
	client, err := s.getClient()
	if err != nil {
		return agentsession.ReasoningSettings{}, err
	}
	prepareRPCConnection(s.reconnector)
	response, err := client.SetReasoningEffort(
		ctx,
		&morphpb.SetSessionReasoningEffortRequest{
			Id:               opts.SessionID,
			ExpectedProvider: opts.ExpectedProvider,
			ExpectedApi:      opts.ExpectedAPI,
			ExpectedModel:    opts.ExpectedModel,
			Effort:           opts.Effort,
			Reset_:           opts.Reset,
		},
	)
	if err != nil {
		return agentsession.ReasoningSettings{}, err
	}
	return protoToSessionReasoningSettings(response.GetReasoning()), nil
}

func protoToSessionReasoningSettings(
	value *morphpb.SessionReasoningSettings,
) agentsession.ReasoningSettings {
	if value == nil {
		return agentsession.ReasoningSettings{}
	}
	efforts := make([]agentsession.ReasoningEffort, len(value.GetSupportedEfforts()))
	for index, effort := range value.GetSupportedEfforts() {
		efforts[index] = agentsession.ReasoningEffort(effort)
	}
	settings := agentsession.ReasoningSettings{
		Model: agentsession.ReasoningModelTuple{
			Provider:    value.GetModel().GetProvider(),
			API:         value.GetModel().GetApi(),
			Model:       value.GetModel().GetModel(),
			DisplayName: value.GetModel().GetDisplayName(),
		},
		SupportedEfforts: efforts,
		SessionOverride:  agentsession.ReasoningEffort(value.GetSessionOverride()),
		ProfileDefault:   agentsession.ReasoningEffort(value.GetProfileDefault()),
		CatalogDefault:   agentsession.ReasoningEffort(value.GetCatalogDefault()),
		EffectiveEffort:  agentsession.ReasoningEffort(value.GetEffectiveEffort()),
		DormantEffort:    agentsession.ReasoningEffort(value.GetDormantEffort()),
		Source:           agentsession.ReasoningResolutionSource(value.GetSource()),
		Fallback:         agentsession.ReasoningFallbackCode(value.GetFallback()),
		Reasoning:        value.GetReasoning(),
		Adjustable:       value.GetAdjustable(),
		SummarySupported: value.GetSummarySupported(),
	}
	if active := value.GetActiveRun(); active != nil {
		snapshot := agentsession.ReasoningSnapshot{
			Provider: active.GetProvider(),
			API:      active.GetApi(),
			Model:    active.GetModel(),
			Effort:   agentsession.ReasoningEffort(active.GetEffort()),
			Summary:  active.GetSummary(),
		}
		settings.ActiveRunSnapshot = &snapshot
	}
	return settings
}

func (s *SessionService) Observe(
	ctx context.Context,
	sessionID string,
	afterCursor int64,
	observe func(SessionEvent) error,
) error {
	if observe == nil {
		return errors.New("session event observer is required")
	}
	client, err := s.getClient()
	if err != nil {
		return err
	}
	prepareRPCConnection(s.reconnector)
	stream, err := client.Observe(ctx, &morphpb.ObserveSessionRequest{
		Id:          sessionID,
		AfterCursor: afterCursor,
	})
	if err != nil {
		return err
	}
	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if response.GetEvent() == nil {
			continue
		}
		event, err := protoToSessionEvent(response.GetEvent())
		if err != nil {
			return err
		}
		if err := observe(event); err != nil {
			return err
		}
	}
}

func (s *SessionService) EditQueuedMessage(
	ctx context.Context,
	sessionID string,
	entryID string,
	message string,
) (SessionQueueEntry, error) {
	client, err := s.getClient()
	if err != nil {
		return SessionQueueEntry{}, err
	}
	prepareRPCConnection(s.reconnector)
	response, err := client.EditQueuedMessage(ctx, &morphpb.EditQueuedSessionMessageRequest{
		Id:      sessionID,
		EntryId: entryID,
		Message: message,
	})
	if err != nil {
		return SessionQueueEntry{}, err
	}
	return protoToSessionQueueEntry(response.GetEntry()), nil
}

func (s *SessionService) RemoveQueuedMessage(
	ctx context.Context,
	sessionID string,
	entryID string,
) (SessionQueueEntry, error) {
	client, err := s.getClient()
	if err != nil {
		return SessionQueueEntry{}, err
	}
	prepareRPCConnection(s.reconnector)
	response, err := client.RemoveQueuedMessage(ctx, &morphpb.RemoveQueuedSessionMessageRequest{
		Id:      sessionID,
		EntryId: entryID,
	})
	if err != nil {
		return SessionQueueEntry{}, err
	}
	return protoToSessionQueueEntry(response.GetEntry()), nil
}

func (s *SessionService) PromoteQueuedMessage(
	ctx context.Context,
	sessionID string,
	entryID string,
) (SessionQueueEntry, error) {
	client, err := s.getClient()
	if err != nil {
		return SessionQueueEntry{}, err
	}
	prepareRPCConnection(s.reconnector)
	response, err := client.PromoteQueuedMessage(ctx, &morphpb.PromoteQueuedSessionMessageRequest{
		Id:      sessionID,
		EntryId: entryID,
	})
	if err != nil {
		return SessionQueueEntry{}, err
	}
	return protoToSessionQueueEntry(response.GetEntry()), nil
}

func (s *SessionService) SteerQueuedMessage(
	ctx context.Context,
	sessionID string,
	entryID string,
) (SessionQueueEntry, error) {
	client, err := s.getClient()
	if err != nil {
		return SessionQueueEntry{}, err
	}
	prepareRPCConnection(s.reconnector)
	response, err := client.SteerQueuedMessage(ctx, &morphpb.SteerQueuedSessionMessageRequest{
		Id:      sessionID,
		EntryId: entryID,
	})
	if err != nil {
		return SessionQueueEntry{}, err
	}
	return protoToSessionQueueEntry(response.GetEntry()), nil
}

func (s *SessionService) InterruptRun(
	ctx context.Context,
	sessionID string,
) (SessionActiveRun, bool, error) {
	client, err := s.getClient()
	if err != nil {
		return SessionActiveRun{}, false, err
	}
	prepareRPCConnection(s.reconnector)
	response, err := client.InterruptRun(ctx, &morphpb.InterruptSessionRunRequest{Id: sessionID})
	if err != nil {
		return SessionActiveRun{}, false, err
	}
	run := protoToSessionActiveRun(response.GetRun())
	if run == nil {
		return SessionActiveRun{}, response.GetTransitioned(), nil
	}
	return *run, response.GetTransitioned(), nil
}

func (c *Client) EnqueueMessage(
	ctx context.Context,
	opts EnqueueMessageOptions,
) (SessionQueueEntry, error) {
	if c == nil || c.Session == nil {
		return SessionQueueEntry{}, errors.New("RPC client is unavailable")
	}
	return c.Session.EnqueueMessage(ctx, opts)
}

func (c *Client) State(ctx context.Context, sessionID string) (SessionExecutionState, error) {
	if c == nil || c.Session == nil {
		return SessionExecutionState{}, errors.New("RPC client is unavailable")
	}
	return c.Session.State(ctx, sessionID)
}

func (c *Client) SetReasoningEffort(
	ctx context.Context,
	opts SetReasoningEffortOptions,
) (agentsession.ReasoningSettings, error) {
	if c == nil || c.Session == nil {
		return agentsession.ReasoningSettings{}, errors.New("RPC client is unavailable")
	}
	return c.Session.SetReasoningEffort(ctx, opts)
}

func (c *Client) Observe(
	ctx context.Context,
	sessionID string,
	afterCursor int64,
	observe func(SessionEvent) error,
) error {
	if c == nil || c.Session == nil {
		return errors.New("RPC client is unavailable")
	}
	return c.Session.Observe(ctx, sessionID, afterCursor, observe)
}

func (c *Client) EditQueuedMessage(
	ctx context.Context,
	sessionID string,
	entryID string,
	message string,
) (SessionQueueEntry, error) {
	if c == nil || c.Session == nil {
		return SessionQueueEntry{}, errors.New("RPC client is unavailable")
	}
	return c.Session.EditQueuedMessage(ctx, sessionID, entryID, message)
}

func (c *Client) RemoveQueuedMessage(
	ctx context.Context,
	sessionID string,
	entryID string,
) (SessionQueueEntry, error) {
	if c == nil || c.Session == nil {
		return SessionQueueEntry{}, errors.New("RPC client is unavailable")
	}
	return c.Session.RemoveQueuedMessage(ctx, sessionID, entryID)
}

func (c *Client) PromoteQueuedMessage(
	ctx context.Context,
	sessionID string,
	entryID string,
) (SessionQueueEntry, error) {
	if c == nil || c.Session == nil {
		return SessionQueueEntry{}, errors.New("RPC client is unavailable")
	}
	return c.Session.PromoteQueuedMessage(ctx, sessionID, entryID)
}

func (c *Client) SteerQueuedMessage(
	ctx context.Context,
	sessionID string,
	entryID string,
) (SessionQueueEntry, error) {
	if c == nil || c.Session == nil {
		return SessionQueueEntry{}, errors.New("RPC client is unavailable")
	}
	return c.Session.SteerQueuedMessage(ctx, sessionID, entryID)
}

func (c *Client) InterruptRun(
	ctx context.Context,
	sessionID string,
) (SessionActiveRun, bool, error) {
	if c == nil || c.Session == nil {
		return SessionActiveRun{}, false, errors.New("RPC client is unavailable")
	}
	return c.Session.InterruptRun(ctx, sessionID)
}

func protoToSessionQueueEntry(entry *morphpb.SessionQueueEntry) SessionQueueEntry {
	if entry == nil {
		return SessionQueueEntry{}
	}
	return SessionQueueEntry{
		ID:                    entry.GetId(),
		SessionID:             entry.GetSessionId(),
		Content:               entry.GetContent(),
		Instruct:              entry.GetInstruct(),
		Stream:                entry.Stream,
		ClientSubmissionID:    entry.GetClientSubmissionId(),
		TargetRunID:           entry.GetTargetRunId(),
		RequestedDeliveryMode: agentsession.DeliveryMode(entry.GetRequestedDeliveryMode()),
		DeliveryMode:          agentsession.DeliveryMode(entry.GetDeliveryMode()),
		SteeringFallback:      agentsession.SteeringFallback(entry.GetSteeringFallback()),
		Status:                agentsession.QueueStatus(entry.GetStatus()),
		Provenance: agentsession.Provenance{
			ActorKind:   entry.GetActorKind(),
			ActorID:     entry.GetActorId(),
			SurfaceKind: entry.GetSurfaceKind(),
			Surface:     entry.GetSurface(),
			Profile:     entry.GetProfile(),
		},
		Sequence:    entry.GetSequence(),
		Priority:    entry.GetPriority(),
		CreatedAt:   protoTimestampToTime(entry.GetCreatedAt()),
		UpdatedAt:   protoTimestampToTime(entry.GetUpdatedAt()),
		StartedAt:   protoTimestampToTime(entry.GetStartedAt()),
		CompletedAt: protoTimestampToTime(entry.GetCompletedAt()),
		LastError:   entry.GetLastError(),
	}
}

func protoToSessionActiveRun(run *morphpb.SessionActiveRun) *SessionActiveRun {
	if run == nil {
		return nil
	}
	return &SessionActiveRun{
		ID:           run.GetId(),
		SessionID:    run.GetSessionId(),
		QueueEntryID: run.GetQueueEntryId(),
		Generation:   run.GetGeneration(),
		Status:       agentsession.RunStatus(run.GetStatus()),
		StartedAt:    protoTimestampToTime(run.GetStartedAt()),
		CompletedAt:  protoTimestampToTime(run.GetCompletedAt()),
		UpdatedAt:    protoTimestampToTime(run.GetUpdatedAt()),
		Reason:       run.GetReason(),
		LastError:    run.GetLastError(),
		Reasoning: agentsession.ReasoningSnapshot{
			Provider: run.GetReasoning().GetProvider(),
			API:      run.GetReasoning().GetApi(),
			Model:    run.GetReasoning().GetModel(),
			Effort:   agentsession.ReasoningEffort(run.GetReasoning().GetEffort()),
			Summary:  run.GetReasoning().GetSummary(),
		},
	}
}

func protoToSessionEvent(event *morphpb.SessionEvent) (SessionEvent, error) {
	value := SessionEvent{
		SessionID: event.GetSessionId(),
		Type:      agentsession.EventType(event.GetType()),
		Cursor:    event.GetCursor(),
		CreatedAt: protoTimestampToTime(event.GetCreatedAt()),
		Run:       protoToSessionActiveRun(event.GetRun()),
	}
	if event.GetQueueEntry() != nil {
		entry := protoToSessionQueueEntry(event.GetQueueEntry())
		value.Queue = &entry
	}
	if event.GetProgressKind() != "" ||
		event.GetProgressText() != "" ||
		event.GetProgressTraceEvent() != nil {
		traceEvent, err := sessionProgressTraceEventFromProto(event.GetProgressTraceEvent())
		if err != nil {
			return SessionEvent{}, err
		}
		value.Progress = &agentsession.ProgressEvent{
			RunID:        event.GetProgressRunId(),
			QueueEntryID: event.GetProgressQueueEntryId(),
			Kind:         event.GetProgressKind(),
			Channel:      event.GetProgressChannel(),
			Text:         event.GetProgressText(),
			Sequence:     event.GetProgressSequence(),
			TraceEvent:   traceEvent,
		}
	}
	return value, nil
}

func sessionProgressTraceEventFromProto(
	event *morphpb.SessionTimelineTraceEvent,
) (*agentsession.TraceEvent, error) {
	if event == nil {
		return nil, nil
	}
	timelineEvent, err := timelineTraceEventFromProto(event)
	if err != nil {
		return nil, err
	}
	return &timelineEvent.Event, nil
}
