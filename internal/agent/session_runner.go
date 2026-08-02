package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wandxy/morph/internal/permissions"
	"github.com/wandxy/morph/internal/profile"
	storage "github.com/wandxy/morph/internal/state/core"
	statemanager "github.com/wandxy/morph/internal/state/manager"
	"github.com/wandxy/morph/internal/trace"
	agentcore "github.com/wandxy/morph/pkg/agent"
	morphmsg "github.com/wandxy/morph/pkg/agent/message"
	agentsession "github.com/wandxy/morph/pkg/agent/session"
	"github.com/wandxy/morph/pkg/nanoid"
	"github.com/wandxy/morph/pkg/str"
)

const (
	steeringQuietWindow     = 30 * time.Millisecond
	sessionRunnerRetryDelay = 100 * time.Millisecond
)

var errSessionRunInterrupted = errors.New("session run interrupted")

type sessionRunner struct {
	agent     *Agent
	sessionID string
	wake      chan struct{}
}

func (a *Agent) StartSessionRunner(ctx context.Context) error {
	if a != nil && a.stateMgr != nil && !a.stateMgr.SupportsSessionInbox() {
		return nil
	}
	if err := a.checkSessionQueueReady(); err != nil {
		return err
	}
	ctx = normalizeContext(ctx)

	a.runnerMu.Lock()
	defer a.runnerMu.Unlock()
	if a.runnerCtx != nil && a.runnerCtx.Err() == nil {
		return nil
	}

	generation, err := nanoid.Generate("gen_")
	if err != nil {
		return err
	}
	reconciled, err := a.stateMgr.ReconcileActiveRuns(context.WithoutCancel(ctx), generation)
	if err != nil {
		return err
	}
	runnableSessions, err := a.stateMgr.ListRunnableSessions(context.WithoutCancel(ctx))
	if err != nil {
		return err
	}

	a.runnerGeneration = generation
	a.runnerCtx, a.runnerCancel = context.WithCancel(ctx)
	a.runnerStopping = false
	for _, run := range reconciled.Runs {
		a.recordSessionQueueTrace(
			context.WithoutCancel(ctx),
			trace.EvtSessionQueueInterrupted,
			agentsession.QueueEntry{
				ID:        run.QueueEntryID,
				SessionID: run.SessionID,
			},
			&run,
			"daemon_restart",
		)
	}
	for _, sessionID := range runnableSessions {
		a.startSessionRunnerLocked(sessionID)
	}
	return nil
}

func (a *Agent) EnqueueSessionMessage(
	ctx context.Context,
	req agentsession.EnqueueRequest,
) (agentsession.QueueEntry, error) {
	if err := a.checkSessionQueueReady(); err != nil {
		return agentsession.QueueEntry{}, err
	}
	session, authorization, err := a.resolveSessionAuthorization(ctx, req.SessionID)
	if err != nil {
		return agentsession.QueueEntry{}, err
	}
	if req.ID == "" {
		req.ID, err = nanoid.Generate("qmsg_")
		if err != nil {
			return agentsession.QueueEntry{}, err
		}
	}
	req.SessionID = session.ID
	req.Provenance = provenanceFromAuthorization(authorization)
	entry, err := a.stateMgr.EnqueueMessage(ctx, req)
	if err != nil {
		return agentsession.QueueEntry{}, err
	}
	a.setSessionAuthorization(session.ID, authorization)
	a.recordSessionQueueTrace(ctx, trace.EvtSessionQueueEnqueued, entry, nil, "")
	a.wakeSessionRunner(session.ID)
	return entry, nil
}

func (a *Agent) GetSessionExecutionState(
	ctx context.Context,
	sessionID string,
) (agentsession.ExecutionState, error) {
	if err := a.checkSessionQueueReady(); err != nil {
		return agentsession.ExecutionState{}, err
	}
	session, _, err := a.resolveSessionAuthorization(ctx, sessionID)
	if err != nil {
		return agentsession.ExecutionState{}, err
	}
	state, err := a.stateMgr.GetExecutionState(ctx, session.ID)
	if err != nil {
		return agentsession.ExecutionState{}, err
	}
	state.Progress = a.getSessionProgress(session.ID)
	state.Reasoning = a.resolveSessionReasoning(session, state)
	return state, nil
}

func (a *Agent) SetSessionReasoningEffort(
	ctx context.Context,
	req agentsession.SetReasoningEffortRequest,
) (agentsession.ReasoningSettings, error) {
	effort := strings.TrimSpace(string(req.Effort))
	if req.Reset == (effort != "") {
		return agentsession.ReasoningSettings{}, fmt.Errorf(
			"%w: specify exactly one of reset or effort",
			agentsession.ErrReasoningInvalid,
		)
	}
	if err := a.checkSessionQueueReady(); err != nil {
		return agentsession.ReasoningSettings{}, err
	}
	session, _, err := a.resolveSessionAuthorization(ctx, req.SessionID)
	if err != nil {
		return agentsession.ReasoningSettings{}, err
	}

	reasoningContext := a.getReasoningStateContext()
	if !isSameReasoningModelTuple(req.ExpectedModel, reasoningContext.Model) {
		return agentsession.ReasoningSettings{}, fmt.Errorf(
			"%w: expected %s/%s/%s, current selection is %s/%s/%s",
			agentsession.ErrReasoningStaleTuple,
			req.ExpectedModel.Provider,
			req.ExpectedModel.API,
			req.ExpectedModel.Model,
			reasoningContext.Model.Provider,
			reasoningContext.Model.API,
			reasoningContext.Model.Model,
		)
	}

	value := ""
	if !req.Reset {
		if !reasoningContext.CatalogFound ||
			!reasoningContext.APISupported ||
			!reasoningContext.Reasoning ||
			len(reasoningContext.Capability.Efforts) == 0 {
			return agentsession.ReasoningSettings{}, agentsession.ErrReasoningUnavailable
		}
		canonical, ok := getCanonicalReasoningEffort(
			agentsession.ReasoningEffort(effort),
			reasoningContext.Capability.Efforts,
		)
		if !ok {
			return agentsession.ReasoningSettings{}, fmt.Errorf(
				"%w: %q is not supported by %s",
				agentsession.ErrReasoningUnsupported,
				effort,
				reasoningContext.Model.Model,
			)
		}
		value = string(canonical)
	}

	updated, err := a.stateMgr.PatchSession(
		ctx,
		session.ID,
		storage.SessionPatch{ReasoningEffortOverride: &value},
	)
	if err != nil {
		return agentsession.ReasoningSettings{}, err
	}
	state, err := a.stateMgr.GetExecutionState(ctx, session.ID)
	if err != nil {
		return agentsession.ReasoningSettings{}, err
	}
	return a.resolveSessionReasoning(updated, state), nil
}

func (a *Agent) resolveSessionReasoning(
	session storage.Session,
	state agentsession.ExecutionState,
) agentsession.ReasoningSettings {
	context := a.getReasoningStateContext()
	var activeRun *agentsession.ReasoningSnapshot
	if state.ActiveRun != nil {
		snapshot := state.ActiveRun.Reasoning
		activeRun = &snapshot
	}
	return agentsession.ResolveReasoningSettings(agentsession.ReasoningResolutionInput{
		Model:           context.Model,
		Capability:      context.Capability,
		SessionOverride: agentsession.ReasoningEffort(session.ReasoningEffortOverride),
		ProfileDefault:  context.ProfileDefault,
		ActiveRun:       activeRun,
		Reasoning:       context.Reasoning,
		CatalogFound:    context.CatalogFound,
		APISupported:    context.APISupported,
	})
}

func isSameReasoningModelTuple(
	expected agentsession.ReasoningModelTuple,
	current agentsession.ReasoningModelTuple,
) bool {
	return strings.EqualFold(strings.TrimSpace(expected.Provider), current.Provider) &&
		strings.EqualFold(strings.TrimSpace(expected.API), current.API) &&
		strings.TrimSpace(expected.Model) == current.Model
}

func getCanonicalReasoningEffort(
	requested agentsession.ReasoningEffort,
	supported []agentsession.ReasoningEffort,
) (agentsession.ReasoningEffort, bool) {
	value := strings.TrimSpace(string(requested))
	for _, effort := range supported {
		if strings.EqualFold(value, string(effort)) {
			return effort, true
		}
	}
	return "", false
}

func (a *Agent) ObserveSessionEvents(
	ctx context.Context,
	sessionID string,
	afterCursor int64,
	observe func(agentsession.Event) error,
) error {
	if observe == nil {
		return errors.New("session event observer is required")
	}
	if err := a.checkSessionQueueReady(); err != nil {
		return err
	}
	session, _, err := a.resolveSessionAuthorization(ctx, sessionID)
	if err != nil {
		return err
	}
	cursor := afterCursor
	progressWake, unsubscribe := a.subscribeSessionProgress(session.ID)
	defer unsubscribe()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var progressSequence int64
	observeProgress := func() error {
		progress, err := a.getSessionProgressAfter(session.ID, progressSequence)
		if err != nil {
			return err
		}
		for _, item := range progress {
			if err := observe(agentsession.Event{
				SessionID: session.ID,
				CreatedAt: time.Now().UTC(),
				Progress:  cloneProgressEvent(item),
			}); err != nil {
				return err
			}
			progressSequence = item.Sequence
		}
		return nil
	}

	for {
		batch, err := a.stateMgr.ListEvents(ctx, session.ID, cursor, 256)
		if err != nil {
			return err
		}
		for _, event := range batch.Events {
			if isTerminalSessionEvent(event) {
				if err := observeProgress(); err != nil {
					return err
				}
			}
			if err := observe(event); err != nil {
				return err
			}
			cursor = event.Cursor
		}
		if len(batch.Events) == 256 {
			continue
		}
		if err := observeProgress(); err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-progressWake:
		case <-ticker.C:
		}
	}
}

func isTerminalSessionEvent(event agentsession.Event) bool {
	if event.Queue != nil {
		switch event.Queue.Status {
		case agentsession.QueueStatusDelivered,
			agentsession.QueueStatusCompleted,
			agentsession.QueueStatusInterrupted,
			agentsession.QueueStatusFailed,
			agentsession.QueueStatusCancelled:
			return true
		}
	}
	switch event.Type {
	case agentsession.EventTypeRunCompleted,
		agentsession.EventTypeRunInterrupted,
		agentsession.EventTypeRunFailed,
		agentsession.EventTypeRunCancelled:
		return true
	default:
		return false
	}
}

func (a *Agent) EditSessionQueueEntry(
	ctx context.Context,
	req agentsession.QueueEditRequest,
) (agentsession.QueueEntry, error) {
	if err := a.checkSessionQueueReady(); err != nil {
		return agentsession.QueueEntry{}, err
	}
	session, _, err := a.resolveSessionAuthorization(ctx, req.SessionID)
	if err != nil {
		return agentsession.QueueEntry{}, err
	}
	req.SessionID = session.ID
	entry, err := a.stateMgr.EditQueueEntry(ctx, req)
	if err == nil {
		a.wakeSessionRunner(session.ID)
	}
	return entry, err
}

func (a *Agent) CancelSessionQueueEntry(
	ctx context.Context,
	req agentsession.QueueMutationRequest,
) (agentsession.QueueEntry, error) {
	if err := a.checkSessionQueueReady(); err != nil {
		return agentsession.QueueEntry{}, err
	}
	session, _, err := a.resolveSessionAuthorization(ctx, req.SessionID)
	if err != nil {
		return agentsession.QueueEntry{}, err
	}
	req.SessionID = session.ID
	entry, err := a.stateMgr.CancelQueueEntry(ctx, req)
	if err == nil {
		a.recordSessionQueueTrace(ctx, trace.EvtSessionQueueCancelled, entry, nil, "")
		a.wakeSessionRunner(session.ID)
	}
	return entry, err
}

func (a *Agent) PromoteSessionQueueEntry(
	ctx context.Context,
	req agentsession.QueueMutationRequest,
) (agentsession.QueueEntry, error) {
	if err := a.checkSessionQueueReady(); err != nil {
		return agentsession.QueueEntry{}, err
	}
	session, _, err := a.resolveSessionAuthorization(ctx, req.SessionID)
	if err != nil {
		return agentsession.QueueEntry{}, err
	}
	req.SessionID = session.ID
	entry, err := a.stateMgr.PromoteQueueEntry(ctx, req)
	if err == nil {
		a.wakeSessionRunner(session.ID)
	}
	return entry, err
}

func (a *Agent) SteerSessionQueueEntry(
	ctx context.Context,
	req agentsession.QueueMutationRequest,
) (agentsession.QueueEntry, error) {
	if err := a.checkSessionQueueReady(); err != nil {
		return agentsession.QueueEntry{}, err
	}
	session, _, err := a.resolveSessionAuthorization(ctx, req.SessionID)
	if err != nil {
		return agentsession.QueueEntry{}, err
	}
	req.SessionID = session.ID
	entry, err := a.stateMgr.SteerQueueEntry(ctx, req)
	if err == nil {
		a.wakeSessionRunner(session.ID)
	}
	return entry, err
}

func (a *Agent) InterruptSessionRun(
	ctx context.Context,
	sessionID string,
) (agentsession.ActiveRun, bool, error) {
	if err := a.checkSessionQueueReady(); err != nil {
		return agentsession.ActiveRun{}, false, err
	}
	session, _, err := a.resolveSessionAuthorization(ctx, sessionID)
	if err != nil {
		return agentsession.ActiveRun{}, false, err
	}
	state, err := a.stateMgr.GetExecutionState(ctx, session.ID)
	if err != nil {
		return agentsession.ActiveRun{}, false, err
	}
	if state.ActiveRun == nil {
		return agentsession.ActiveRun{}, false, nil
	}
	run, transitioned, err := a.stateMgr.FinishSessionRun(ctx, agentsession.RunFinishRequest{
		SessionID:  session.ID,
		RunID:      state.ActiveRun.ID,
		Generation: state.ActiveRun.Generation,
		Status:     agentsession.RunStatusInterrupted,
		Reason:     "user_interrupt",
	})
	if err != nil {
		return agentsession.ActiveRun{}, false, err
	}
	if transitioned {
		for _, entry := range state.Queue {
			if entry.ID == run.QueueEntryID {
				a.recordSessionQueueTrace(
					ctx,
					trace.EvtSessionQueueInterrupted,
					entry,
					&run,
					run.Reason,
				)
				break
			}
		}
		a.cancelSessionRun(run.ID, errSessionRunInterrupted)
		a.wakeSessionRunner(session.ID)
	}
	return run, transitioned, nil
}

func (a *Agent) checkSessionQueueReady() error {
	if a == nil {
		return errors.New("agent is required")
	}
	if !a.initialized || a.stateMgr == nil || a.env == nil {
		return errors.New("environment has not been initialized")
	}
	if !a.stateMgr.SupportsSessionInbox() {
		return statemanager.ErrSessionInboxUnsupported
	}
	return nil
}

func (a *Agent) resolveSessionAuthorization(
	ctx context.Context,
	sessionID string,
) (storage.Session, permissions.AuthorizationContext, error) {
	ctx = normalizeContext(ctx)
	sessionID = str.String(sessionID).Trim()
	if sessionID == "" {
		sessionID = storage.DefaultSessionID
	}
	session, err := a.stateMgr.Resolve(ctx, sessionID)
	if err != nil {
		return storage.Session{}, permissions.AuthorizationContext{}, err
	}
	authorization, ok := permissions.FromContext(ctx)
	if !ok {
		authorization = permissions.AuthorizationContext{
			Actor:   permissions.Actor{Kind: permissions.ActorLocalOwner},
			Surface: permissions.SurfaceCLI,
		}
	} else if authorizedSessionID := str.String(authorization.SessionID).Trim(); authorizedSessionID != "" && authorizedSessionID != session.ID {
		return storage.Session{}, permissions.AuthorizationContext{},
			errors.New("authorization does not match the requested session")
	}
	authorization.Profile = str.String(profile.Active().Name).Trim()
	authorization.SessionID = session.ID
	authorization, err = authorization.Normalize()
	if err != nil {
		return storage.Session{}, permissions.AuthorizationContext{}, err
	}
	return session, authorization, nil
}

func provenanceFromAuthorization(authorization permissions.AuthorizationContext) agentsession.Provenance {
	return agentsession.Provenance{
		ActorKind:   string(authorization.Actor.Kind),
		ActorID:     authorization.Actor.ID,
		SurfaceKind: string(authorization.SurfaceKind),
		Surface:     string(authorization.Surface),
		Profile:     authorization.Profile,
	}
}

func authorizationFromQueueEntry(entry agentsession.QueueEntry, runID string) permissions.AuthorizationContext {
	return permissions.AuthorizationContext{
		Actor: permissions.Actor{
			Kind: permissions.ActorKind(entry.Provenance.ActorKind),
			ID:   entry.Provenance.ActorID,
		},
		SurfaceKind: permissions.SurfaceKind(entry.Provenance.SurfaceKind),
		Surface:     permissions.Surface(entry.Provenance.Surface),
		Profile:     entry.Provenance.Profile,
		SessionID:   entry.SessionID,
		RunID:       runID,
	}
}

func (a *Agent) wakeSessionRunner(sessionID string) {
	if a == nil {
		return
	}
	sessionID = str.String(sessionID).Trim()
	if sessionID == "" {
		return
	}

	a.runnerMu.Lock()
	if a.runnerCtx == nil || a.runnerCtx.Err() != nil || a.runnerStopping {
		a.runnerMu.Unlock()
		return
	}
	runner := a.startSessionRunnerLocked(sessionID)
	a.runnerMu.Unlock()

	select {
	case runner.wake <- struct{}{}:
	default:
	}
}

func (a *Agent) startSessionRunnerLocked(sessionID string) *sessionRunner {
	if a.sessionRunners == nil {
		a.sessionRunners = make(map[string]*sessionRunner)
	}
	if runner := a.sessionRunners[sessionID]; runner != nil {
		return runner
	}

	runner := &sessionRunner{
		agent:     a,
		sessionID: sessionID,
		wake:      make(chan struct{}, 1),
	}
	a.sessionRunners[sessionID] = runner
	a.runnerWG.Add(1)
	go func(ctx context.Context) {
		defer a.runnerWG.Done()
		runner.run(ctx)
	}(a.runnerCtx)
	select {
	case runner.wake <- struct{}{}:
	default:
	}
	return runner
}

func (r *sessionRunner) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
		}

		for {
			claimed, err := r.claimAndRun(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				if errors.Is(err, agentsession.ErrStaleRunnerGeneration) {
					return
				}
				agentLog.Error().
					Err(err).
					Str("session_id", r.sessionID).
					Msg("session runner failed")
				timer := time.NewTimer(sessionRunnerRetryDelay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-r.wake:
					timer.Stop()
				case <-timer.C:
				}
				continue
			}
			if !claimed {
				break
			}
		}
	}
}

func (r *sessionRunner) claimAndRun(ctx context.Context) (bool, error) {
	coordinator := r.agent.turnCoordinator
	if coordinator == nil {
		coordinator = defaultTurnCoordinator
	}
	release, err := coordinator.Acquire(ctx, r.agent.turnScope, r.sessionID)
	if err != nil {
		return false, err
	}
	defer release()

	runID, err := nanoid.Generate("run_")
	if err != nil {
		return false, err
	}
	entry, run, claimed, err := r.agent.stateMgr.ClaimNextFollowUp(ctx, agentsession.ClaimRequest{
		SessionID:  r.sessionID,
		RunID:      runID,
		Generation: r.agent.runnerGeneration,
		Reasoning:  r.agent.getReasoningClaimContext(),
	})
	if err != nil || !claimed {
		return claimed, err
	}
	r.agent.recordSessionQueueTrace(ctx, trace.EvtSessionQueueClaimed, entry, &run, "")
	r.agent.executeSessionRun(ctx, entry, run)
	return true, nil
}

func (a *Agent) executeSessionRun(
	parent context.Context,
	entry agentsession.QueueEntry,
	run agentsession.ActiveRun,
) {
	runCtx, cancel := context.WithCancelCause(parent)
	a.registerSessionRunCancel(run.ID, cancel)
	defer a.unregisterSessionRunCancel(run.ID)
	runCtx = permissions.WithContext(runCtx, authorizationFromQueueEntry(entry, run.ID))

	var runErr error
	turn := a.newTurn(a.env, a.invokeToolWithEnvironment)
	turn.setReasoningSnapshot(run.Reasoning)
	turn.setAfterToolBatchPersisted(func(ctx context.Context) (bool, error) {
		steeringRequest := agentsession.SteeringClaimRequest{
			SessionID:  entry.SessionID,
			RunID:      run.ID,
			Generation: run.Generation,
		}
		pending, err := a.stateMgr.HasPendingSteering(ctx, steeringRequest)
		if err != nil || !pending {
			return false, err
		}
		timer := time.NewTimer(steeringQuietWindow)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-timer.C:
		}
		entries, err := a.stateMgr.ClaimSteering(ctx, steeringRequest)
		if err != nil {
			return false, err
		}
		for _, steering := range entries {
			message, err := morphmsg.NewMessage(morphmsg.RoleUser, steering.Content)
			if err != nil {
				return false, err
			}
			turn.emittedMessages = append(turn.emittedMessages, message)
			a.recordSessionQueueTrace(
				ctx,
				trace.EvtSessionSteeringDelivered,
				steering,
				&run,
				"",
			)
		}
		return len(entries) > 0, nil
	})

	instruct := entry.Instruct
	if instruct == "" {
		instruct = a.cfg.Session.Instruct
	}
	stream := entry.Stream
	if stream == nil {
		stream = a.cfg.Models.Main.Stream
	}
	_, runErr = turn.Run(runCtx, entry.Content, agentcore.RespondOptions{
		SessionID:   entry.SessionID,
		Instruct:    instruct,
		Stream:      stream,
		TraceEvents: true,
		OnEvent: func(event agentcore.Event) {
			a.publishSessionProgress(entry.SessionID, run.ID, entry.ID, event)
		},
	})
	a.setTurnMessages(turn.Messages())
	status := agentsession.RunStatusCompleted
	reason := ""
	lastError := ""
	if runErr != nil {
		lastError = runErr.Error()
		switch {
		case errors.Is(context.Cause(runCtx), errSessionRunInterrupted):
			status = agentsession.RunStatusInterrupted
			reason = "user_interrupt"
		case errors.Is(runErr, context.Canceled):
			status = agentsession.RunStatusCancelled
			reason = "daemon_shutdown"
		default:
			status = agentsession.RunStatusFailed
		}
	}

	finishCtx := context.WithoutCancel(parent)
	finishedRun, transitioned, err := a.stateMgr.FinishSessionRun(finishCtx, agentsession.RunFinishRequest{
		SessionID:  entry.SessionID,
		RunID:      run.ID,
		Generation: run.Generation,
		Status:     status,
		Reason:     reason,
		LastError:  lastError,
	})
	if err != nil {
		agentLog.Error().Err(err).Str("run_id", run.ID).Msg("session run terminal transition failed")
		return
	}
	if transitioned && status == agentsession.RunStatusCompleted {
		a.maybeGenerateSessionTitle(finishCtx, entry.SessionID)
	}
	if transitioned {
		a.recordSessionQueueTrace(
			finishCtx,
			sessionRunTraceEventType(status),
			entry,
			&finishedRun,
			reason,
		)
	}
}

func (a *Agent) subscribeSessionProgress(
	sessionID string,
) (<-chan struct{}, func()) {
	wake := make(chan struct{}, 1)
	a.progressMu.Lock()
	if a.progressObservers == nil {
		a.progressObservers = make(map[string]map[chan struct{}]struct{})
	}
	observers := a.progressObservers[sessionID]
	if observers == nil {
		observers = make(map[chan struct{}]struct{})
		a.progressObservers[sessionID] = observers
	}
	observers[wake] = struct{}{}
	a.progressMu.Unlock()
	return wake, func() {
		a.progressMu.Lock()
		delete(a.progressObservers[sessionID], wake)
		if len(a.progressObservers[sessionID]) == 0 {
			delete(a.progressObservers, sessionID)
		}
		a.progressMu.Unlock()
	}
}

func (a *Agent) publishSessionProgress(
	sessionID string,
	runID string,
	queueEntryID string,
	event agentcore.Event,
) {
	a.progressMu.Lock()
	defer a.progressMu.Unlock()
	if a.progressHistory == nil {
		a.progressHistory = make(map[string][]agentsession.ProgressEvent)
	}
	if a.progressSequence == nil {
		a.progressSequence = make(map[string]int64)
	}
	a.progressSequence[sessionID]++
	progress := agentsession.ProgressEvent{
		RunID:        runID,
		QueueEntryID: queueEntryID,
		Kind:         event.Kind,
		Channel:      event.Channel,
		Text:         event.Text,
		Sequence:     a.progressSequence[sessionID],
		TraceEvent:   sessionTraceEventFromAgentEvent(event),
	}
	a.progressHistory[sessionID] = append(
		a.progressHistory[sessionID],
		progress,
	)
	if len(a.progressHistory[sessionID]) > 256 {
		a.progressHistory[sessionID] = a.progressHistory[sessionID][len(a.progressHistory[sessionID])-256:]
	}
	for observer := range a.progressObservers[sessionID] {
		select {
		case observer <- struct{}{}:
		default:
		}
	}
}

func sessionTraceEventFromAgentEvent(event agentcore.Event) *agentsession.TraceEvent {
	var traceEvent trace.Event
	switch value := event.TraceEvent.(type) {
	case trace.Event:
		traceEvent = value
	case *trace.Event:
		if value == nil {
			return nil
		}
		traceEvent = *value
	default:
		return nil
	}
	return &agentsession.TraceEvent{
		SessionID: traceEvent.SessionID,
		Type:      traceEvent.Type,
		Timestamp: traceEvent.Timestamp,
		Payload:   traceEvent.Payload,
	}
}

func (a *Agent) getSessionProgress(sessionID string) []agentsession.ProgressEvent {
	a.progressMu.Lock()
	defer a.progressMu.Unlock()
	return append([]agentsession.ProgressEvent(nil), a.progressHistory[sessionID]...)
}

func (a *Agent) getSessionProgressAfter(
	sessionID string,
	afterSequence int64,
) ([]agentsession.ProgressEvent, error) {
	a.progressMu.Lock()
	defer a.progressMu.Unlock()

	history := a.progressHistory[sessionID]
	if len(history) == 0 {
		return nil, nil
	}
	if afterSequence > 0 && afterSequence+1 < history[0].Sequence {
		return nil, agentsession.ErrProgressExpired
	}
	index := 0
	for index < len(history) && history[index].Sequence <= afterSequence {
		index++
	}
	return append([]agentsession.ProgressEvent(nil), history[index:]...), nil
}

func cloneProgressEvent(event agentsession.ProgressEvent) *agentsession.ProgressEvent {
	value := event
	return &value
}

func (a *Agent) recordSessionQueueTrace(
	ctx context.Context,
	eventType string,
	entry agentsession.QueueEntry,
	run *agentsession.ActiveRun,
	reason string,
) {
	if a == nil || a.stateMgr == nil {
		return
	}
	payload := trace.SessionQueueEventPayload{
		QueueEntryID: entry.ID,
		DeliveryMode: string(entry.DeliveryMode),
		Status:       string(entry.Status),
		Reason:       reason,
	}
	if run != nil {
		payload.RunID = run.ID
		payload.Status = string(run.Status)
	}
	if _, err := a.stateMgr.AppendTraceEvent(ctx, storage.TraceEvent{
		SessionID: entry.SessionID,
		Type:      eventType,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}); err != nil && !errors.Is(err, storage.ErrTraceStoreUnsupported) {
		agentLog.Warn().
			Err(err).
			Str("session_id", entry.SessionID).
			Str("queue_entry_id", entry.ID).
			Str("event_type", eventType).
			Msg("session queue trace persistence failed")
	}
}

func sessionRunTraceEventType(status agentsession.RunStatus) string {
	switch status {
	case agentsession.RunStatusCompleted:
		return trace.EvtSessionQueueCompleted
	case agentsession.RunStatusInterrupted:
		return trace.EvtSessionQueueInterrupted
	case agentsession.RunStatusCancelled:
		return trace.EvtSessionQueueCancelled
	default:
		return trace.EvtSessionQueueFailed
	}
}

func (a *Agent) registerSessionRunCancel(runID string, cancel context.CancelCauseFunc) {
	a.runnerMu.Lock()
	defer a.runnerMu.Unlock()
	if a.runCancels == nil {
		a.runCancels = make(map[string]context.CancelCauseFunc)
	}
	a.runCancels[runID] = cancel
}

func (a *Agent) unregisterSessionRunCancel(runID string) {
	a.runnerMu.Lock()
	delete(a.runCancels, runID)
	a.runnerMu.Unlock()
}

func (a *Agent) cancelSessionRun(runID string, cause error) {
	a.runnerMu.Lock()
	cancel := a.runCancels[runID]
	a.runnerMu.Unlock()
	if cancel != nil {
		cancel(cause)
	}
}
