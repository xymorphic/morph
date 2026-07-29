package storememory

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"time"

	base "github.com/wandxy/morph/internal/state/core"
	morphmsg "github.com/wandxy/morph/pkg/agent/message"
	agentsession "github.com/wandxy/morph/pkg/agent/session"
	"github.com/wandxy/morph/pkg/str"
)

const (
	memorySessionEventRetentionAge   = 7 * 24 * time.Hour
	memorySessionEventRetentionCount = int64(10000)
	memorySessionStateTerminalLimit  = 256
)

type sessionInboxState struct {
	entries             map[string]agentsession.QueueEntry
	submissionEntryIDs  map[string]string
	runs                map[string]agentsession.ActiveRun
	events              []agentsession.Event
	cursor              int64
	nextQueueSequence   int64
	retainedCursorFloor int64
}

var _ agentsession.InboxStore = (*Store)(nil)

func (s *Store) SubmitMessage(
	_ context.Context,
	req agentsession.SubmitRequest,
) (agentsession.QueueEntry, error) {
	if s == nil {
		return agentsession.QueueEntry{}, errors.New("store is required")
	}

	req, err := normalizeMemorySubmitRequest(req)
	if err != nil {
		return agentsession.QueueEntry{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.requireInboxSessionLocked(req.SessionID); err != nil {
		return agentsession.QueueEntry{}, err
	}
	inbox := s.getOrCreateSessionInboxLocked(req.SessionID)
	if entryID, ok := inbox.submissionEntryIDs[req.ClientSubmissionID]; ok {
		entry := inbox.entries[entryID]
		if !isSameMemorySubmission(entry, req) {
			return agentsession.QueueEntry{}, errors.New(
				"client submission id is already used by a different message",
			)
		}
		return cloneMemoryQueueEntry(entry), nil
	}
	if _, ok := inbox.entries[req.ID]; ok {
		return agentsession.QueueEntry{}, errors.New("queue entry id is already used")
	}

	effectiveDeliveryMode := req.DeliveryMode
	targetRunID := ""
	if effectiveDeliveryMode == agentsession.DeliveryModeSteering {
		if activeRun, ok := getMemoryActiveRun(inbox); ok {
			targetRunID = activeRun.ID
		} else if req.SteeringFallback == agentsession.SteeringFallbackFollowUp {
			effectiveDeliveryMode = agentsession.DeliveryModeFollowUp
		} else {
			return agentsession.QueueEntry{}, agentsession.ErrSteeringRequiresRun
		}
	}

	inbox.nextQueueSequence++
	now := time.Now().UTC()
	entry := agentsession.QueueEntry{
		ID:                    req.ID,
		SessionID:             req.SessionID,
		Content:               req.Content,
		Instruct:              req.Instruct,
		Stream:                cloneMemoryBool(req.Stream),
		ClientSubmissionID:    req.ClientSubmissionID,
		TargetRunID:           targetRunID,
		RequestedDeliveryMode: req.DeliveryMode,
		DeliveryMode:          effectiveDeliveryMode,
		SteeringFallback:      req.SteeringFallback,
		Status:                agentsession.QueueStatusPending,
		Provenance:            req.Provenance,
		Sequence:              inbox.nextQueueSequence,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	inbox.entries[entry.ID] = entry
	inbox.submissionEntryIDs[entry.ClientSubmissionID] = entry.ID
	s.appendSessionInboxEventLocked(inbox, agentsession.Event{
		SessionID: req.SessionID,
		Type:      agentsession.EventTypeQueueEnqueued,
		Queue:     cloneMemoryQueueEntryPointer(entry),
		CreatedAt: now,
	})

	return cloneMemoryQueueEntry(entry), nil
}

func (s *Store) GetExecutionState(
	_ context.Context,
	sessionID string,
) (agentsession.ExecutionState, error) {
	if s == nil {
		return agentsession.ExecutionState{}, errors.New("store is required")
	}

	sessionID, err := normalizeMemorySessionID(sessionID)
	if err != nil {
		return agentsession.ExecutionState{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if err := s.requireInboxSessionLocked(sessionID); err != nil {
		return agentsession.ExecutionState{}, err
	}

	state := agentsession.ExecutionState{SessionID: sessionID}
	inbox := s.sessionInboxes[sessionID]
	if inbox == nil {
		return state, nil
	}
	state.Cursor = inbox.cursor
	state.RetainedCursorFloor = inbox.retainedCursorFloor
	if run, ok := getMemoryActiveRun(inbox); ok {
		value := run
		state.ActiveRun = &value
	}

	terminal := make([]agentsession.QueueEntry, 0)
	for _, stored := range inbox.entries {
		entry := cloneMemoryQueueEntry(stored)
		switch entry.Status {
		case agentsession.QueueStatusPending, agentsession.QueueStatusActive:
			state.Queue = append(state.Queue, entry)
		default:
			terminal = append(terminal, entry)
		}
	}
	slices.SortFunc(terminal, func(left, right agentsession.QueueEntry) int {
		return cmp.Compare(right.Sequence, left.Sequence)
	})
	if len(terminal) > memorySessionStateTerminalLimit {
		terminal = terminal[:memorySessionStateTerminalLimit]
	}
	state.Queue = append(state.Queue, terminal...)
	slices.SortFunc(state.Queue, func(left, right agentsession.QueueEntry) int {
		return cmp.Compare(left.Sequence, right.Sequence)
	})
	for _, entry := range state.Queue {
		if entry.Status != agentsession.QueueStatusPending {
			continue
		}
		state.QueueDepth++
		if state.OldestPendingCreated.IsZero() ||
			entry.CreatedAt.Before(state.OldestPendingCreated) {
			state.OldestPendingCreated = entry.CreatedAt
		}
	}

	return state, nil
}

func (s *Store) ListEvents(
	_ context.Context,
	sessionID string,
	afterCursor int64,
	limit int,
) (agentsession.EventBatch, error) {
	if s == nil {
		return agentsession.EventBatch{}, errors.New("store is required")
	}

	sessionID, err := normalizeMemorySessionID(sessionID)
	if err != nil {
		return agentsession.EventBatch{}, err
	}
	if afterCursor < 0 {
		return agentsession.EventBatch{}, errors.New(
			"after cursor must be greater than or equal to zero",
		)
	}
	if limit <= 0 || limit > 256 {
		limit = 256
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if err := s.requireInboxSessionLocked(sessionID); err != nil {
		return agentsession.EventBatch{}, err
	}
	inbox := s.sessionInboxes[sessionID]
	if inbox == nil {
		if afterCursor > 0 {
			return agentsession.EventBatch{}, agentsession.ErrCursorBeyondSession
		}
		return agentsession.EventBatch{}, nil
	}
	if afterCursor > inbox.cursor {
		return agentsession.EventBatch{}, agentsession.ErrCursorBeyondSession
	}
	if inbox.retainedCursorFloor > 0 &&
		afterCursor+1 < inbox.retainedCursorFloor {
		return agentsession.EventBatch{}, agentsession.ErrCursorExpired
	}

	batch := agentsession.EventBatch{
		Cursor:              inbox.cursor,
		RetainedCursorFloor: inbox.retainedCursorFloor,
	}
	for _, event := range inbox.events {
		if event.Cursor <= afterCursor {
			continue
		}
		batch.Events = append(batch.Events, cloneMemorySessionEvent(event))
		if len(batch.Events) == limit {
			break
		}
	}

	return batch, nil
}

func (s *Store) EditQueueEntry(
	ctx context.Context,
	req agentsession.QueueEditRequest,
) (agentsession.QueueEntry, error) {
	content := str.String(req.Content).Trim()
	if content == "" {
		return agentsession.QueueEntry{}, errors.New("message is required")
	}
	return s.updatePendingMemoryQueueEntry(
		ctx,
		req.SessionID,
		req.EntryID,
		func(entry *agentsession.QueueEntry, _ *sessionInboxState) {
			entry.Content = content
			entry.UpdatedAt = time.Now().UTC()
		},
		agentsession.EventTypeQueueUpdated,
	)
}

func (s *Store) CancelQueueEntry(
	ctx context.Context,
	req agentsession.QueueMutationRequest,
) (agentsession.QueueEntry, error) {
	return s.updatePendingMemoryQueueEntry(
		ctx,
		req.SessionID,
		req.EntryID,
		func(entry *agentsession.QueueEntry, _ *sessionInboxState) {
			now := time.Now().UTC()
			entry.Status = agentsession.QueueStatusCancelled
			entry.CompletedAt = now
			entry.UpdatedAt = now
		},
		agentsession.EventTypeQueueCancelled,
	)
}

func (s *Store) PromoteQueueEntry(
	ctx context.Context,
	req agentsession.QueueMutationRequest,
) (agentsession.QueueEntry, error) {
	return s.updatePendingMemoryQueueEntry(
		ctx,
		req.SessionID,
		req.EntryID,
		func(entry *agentsession.QueueEntry, inbox *sessionInboxState) {
			var maxPriority int64
			for _, candidate := range inbox.entries {
				maxPriority = max(maxPriority, candidate.Priority)
			}
			entry.Priority = maxPriority + 1
			entry.UpdatedAt = time.Now().UTC()
		},
		agentsession.EventTypeQueueUpdated,
	)
}

func (s *Store) SteerQueueEntry(
	ctx context.Context,
	req agentsession.QueueMutationRequest,
) (agentsession.QueueEntry, error) {
	return s.updatePendingMemoryQueueEntry(
		ctx,
		req.SessionID,
		req.EntryID,
		func(entry *agentsession.QueueEntry, inbox *sessionInboxState) {
			entry.RequestedDeliveryMode = agentsession.DeliveryModeSteering
			entry.SteeringFallback = agentsession.SteeringFallbackFollowUp
			entry.TargetRunID = ""
			entry.DeliveryMode = agentsession.DeliveryModeFollowUp
			if activeRun, ok := getMemoryActiveRun(inbox); ok {
				entry.TargetRunID = activeRun.ID
				entry.DeliveryMode = agentsession.DeliveryModeSteering
			}
			entry.UpdatedAt = time.Now().UTC()
		},
		agentsession.EventTypeQueueUpdated,
	)
}

func (s *Store) updatePendingMemoryQueueEntry(
	_ context.Context,
	sessionID string,
	entryID string,
	update func(*agentsession.QueueEntry, *sessionInboxState),
	eventType agentsession.EventType,
) (agentsession.QueueEntry, error) {
	if s == nil {
		return agentsession.QueueEntry{}, errors.New("store is required")
	}

	sessionID, err := normalizeMemorySessionID(sessionID)
	if err != nil {
		return agentsession.QueueEntry{}, err
	}
	entryID = str.String(entryID).Trim()
	if entryID == "" {
		return agentsession.QueueEntry{}, errors.New("queue entry id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	inbox := s.sessionInboxes[sessionID]
	entry, ok := getPendingMemoryQueueEntry(inbox, entryID)
	if !ok {
		return agentsession.QueueEntry{}, errors.New("pending queue entry not found")
	}
	update(&entry, inbox)
	inbox.entries[entry.ID] = entry
	s.appendSessionInboxEventLocked(inbox, agentsession.Event{
		SessionID: sessionID,
		Type:      eventType,
		Queue:     cloneMemoryQueueEntryPointer(entry),
		CreatedAt: entry.UpdatedAt,
	})

	return cloneMemoryQueueEntry(entry), nil
}

func (s *Store) ClaimNextFollowUp(
	_ context.Context,
	req agentsession.ClaimRequest,
) (agentsession.QueueEntry, agentsession.ActiveRun, bool, error) {
	if s == nil {
		return agentsession.QueueEntry{}, agentsession.ActiveRun{}, false, errors.New("store is required")
	}

	req.SessionID = str.String(req.SessionID).Trim()
	req.RunID = str.String(req.RunID).Trim()
	req.Generation = str.String(req.Generation).Trim()
	if err := base.ValidateSessionID(req.SessionID); err != nil {
		return agentsession.QueueEntry{}, agentsession.ActiveRun{}, false, err
	}
	if req.RunID == "" || req.Generation == "" {
		return agentsession.QueueEntry{}, agentsession.ActiveRun{}, false, errors.New(
			"run id and generation are required",
		)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if req.Generation != s.runnerGeneration {
		return agentsession.QueueEntry{}, agentsession.ActiveRun{}, false,
			agentsession.ErrStaleRunnerGeneration
	}
	if err := s.requireInboxSessionLocked(req.SessionID); err != nil {
		return agentsession.QueueEntry{}, agentsession.ActiveRun{}, false, err
	}
	inbox := s.sessionInboxes[req.SessionID]
	if inbox == nil {
		return agentsession.QueueEntry{}, agentsession.ActiveRun{}, false, nil
	}
	if _, ok := getMemoryActiveRun(inbox); ok {
		return agentsession.QueueEntry{}, agentsession.ActiveRun{}, false, nil
	}
	if _, ok := s.sessionRunIDs[req.RunID]; ok {
		return agentsession.QueueEntry{}, agentsession.ActiveRun{}, false, errors.New(
			"run id is already used",
		)
	}

	entry, ok := getNextMemoryFollowUp(inbox)
	if !ok {
		return agentsession.QueueEntry{}, agentsession.ActiveRun{}, false, nil
	}

	now := time.Now().UTC()
	entry.Status = agentsession.QueueStatusActive
	entry.StartedAt = now
	entry.UpdatedAt = now
	inbox.entries[entry.ID] = entry
	run := agentsession.ActiveRun{
		ID:           req.RunID,
		SessionID:    req.SessionID,
		QueueEntryID: entry.ID,
		Generation:   req.Generation,
		Status:       agentsession.RunStatusRunning,
		StartedAt:    now,
		UpdatedAt:    now,
		Reasoning: agentsession.ResolveReasoningSnapshot(
			req.Reasoning,
			s.sessions[req.SessionID].ReasoningEffortOverride,
		),
	}
	inbox.runs[run.ID] = run
	s.sessionRunIDs[run.ID] = struct{}{}
	s.appendSessionInboxEventLocked(inbox, agentsession.Event{
		SessionID: req.SessionID,
		Type:      agentsession.EventTypeQueueClaimed,
		Queue:     cloneMemoryQueueEntryPointer(entry),
		CreatedAt: now,
	})
	s.appendSessionInboxEventLocked(inbox, agentsession.Event{
		SessionID: req.SessionID,
		Type:      agentsession.EventTypeRunStarted,
		Run:       cloneMemoryActiveRunPointer(run),
		CreatedAt: now,
	})

	return cloneMemoryQueueEntry(entry), run, true, nil
}

func (s *Store) HasPendingSteering(
	_ context.Context,
	req agentsession.SteeringClaimRequest,
) (bool, error) {
	if s == nil {
		return false, errors.New("store is required")
	}
	if err := normalizeMemorySteeringClaimRequest(&req); err != nil {
		return false, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if err := s.requireInboxSessionLocked(req.SessionID); err != nil {
		return false, nil
	}
	inbox := s.sessionInboxes[req.SessionID]
	if !hasMatchingMemoryRun(inbox, req) {
		return false, nil
	}
	for _, entry := range inbox.entries {
		if isPendingMemorySteering(entry, req.RunID) {
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) ClaimSteering(
	ctx context.Context,
	req agentsession.SteeringClaimRequest,
) ([]agentsession.QueueEntry, error) {
	if s == nil {
		return nil, errors.New("store is required")
	}
	if err := normalizeMemorySteeringClaimRequest(&req); err != nil {
		return nil, err
	}

	s.mu.Lock()
	session, ok := s.sessions[req.SessionID]
	if !ok || session.Archived {
		s.mu.Unlock()
		return nil, errors.New("session not found")
	}
	inbox := s.sessionInboxes[req.SessionID]
	if !hasMatchingMemoryRun(inbox, req) {
		s.mu.Unlock()
		return nil, nil
	}

	run := inbox.runs[req.RunID]
	entries := getPendingMemorySteering(inbox, req.RunID)
	if len(entries) == 0 {
		s.mu.Unlock()
		return nil, nil
	}

	now := time.Now().UTC()
	messages := make([]morphmsg.Message, 0, len(entries))
	for _, entry := range entries {
		messages = append(messages, morphmsg.Message{
			Role:      morphmsg.RoleUser,
			Content:   entry.Content,
			CreatedAt: now,
		})
	}
	persistedMessages := s.appendMessagesLocked(
		req.SessionID,
		session,
		messages,
	)

	for index := range entries {
		entries[index].Status = agentsession.QueueStatusDelivered
		entries[index].StartedAt = now
		entries[index].CompletedAt = now
		entries[index].UpdatedAt = now
		inbox.entries[entries[index].ID] = entries[index]
		s.appendSessionInboxEventLocked(inbox, agentsession.Event{
			SessionID: req.SessionID,
			Type:      agentsession.EventTypeSteeringSent,
			Queue:     cloneMemoryQueueEntryPointer(entries[index]),
			Run:       cloneMemoryActiveRunPointer(run),
			CreatedAt: now,
		})
	}
	s.mu.Unlock()

	s.indexPersistedMessages(ctx, req.SessionID, persistedMessages)

	cloned := make([]agentsession.QueueEntry, len(entries))
	for index, entry := range entries {
		cloned[index] = cloneMemoryQueueEntry(entry)
	}
	return cloned, nil
}

func (s *Store) FinishSessionRun(
	_ context.Context,
	req agentsession.RunFinishRequest,
) (agentsession.ActiveRun, bool, error) {
	if s == nil {
		return agentsession.ActiveRun{}, false, errors.New("store is required")
	}
	if err := validateMemoryRunFinishRequest(req); err != nil {
		return agentsession.ActiveRun{}, false, err
	}

	req.SessionID = str.String(req.SessionID).Trim()
	req.RunID = str.String(req.RunID).Trim()
	req.Generation = str.String(req.Generation).Trim()
	req.Reason = str.String(req.Reason).Trim()
	req.LastError = str.String(req.LastError).Trim()

	s.mu.Lock()
	defer s.mu.Unlock()

	inbox := s.sessionInboxes[req.SessionID]
	if inbox == nil {
		return agentsession.ActiveRun{}, false, nil
	}
	run, ok := inbox.runs[req.RunID]
	if !ok || run.Generation != req.Generation {
		return agentsession.ActiveRun{}, false, nil
	}
	if run.Status != agentsession.RunStatusRunning {
		return run, false, nil
	}

	run = s.finishMemorySessionRunLocked(inbox, run, req.Status, req.Reason, req.LastError)
	return run, true, nil
}

func (s *Store) ReconcileActiveRuns(
	_ context.Context,
	currentGeneration string,
) (agentsession.ReconcileResult, error) {
	if s == nil {
		return agentsession.ReconcileResult{}, errors.New("store is required")
	}
	currentGeneration = str.String(currentGeneration).Trim()
	if currentGeneration == "" {
		return agentsession.ReconcileResult{}, errors.New("generation is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.runnerGeneration = currentGeneration
	type sessionRun struct {
		sessionID string
		run       agentsession.ActiveRun
	}
	var activeRuns []sessionRun
	for sessionID, inbox := range s.sessionInboxes {
		for _, run := range inbox.runs {
			if run.Status == agentsession.RunStatusRunning &&
				run.Generation != currentGeneration {
				activeRuns = append(activeRuns, sessionRun{sessionID: sessionID, run: run})
			}
		}
	}
	slices.SortFunc(activeRuns, func(left, right sessionRun) int {
		return cmp.Compare(left.sessionID, right.sessionID)
	})

	result := agentsession.ReconcileResult{}
	seen := make(map[string]struct{})
	for _, active := range activeRuns {
		inbox := s.sessionInboxes[active.sessionID]
		run := s.finishMemorySessionRunLocked(
			inbox,
			active.run,
			agentsession.RunStatusInterrupted,
			"daemon_restart",
			"",
		)
		result.Runs = append(result.Runs, run)
		result.RunCount++
		seen[active.sessionID] = struct{}{}
	}
	for sessionID := range seen {
		result.SessionIDs = append(result.SessionIDs, sessionID)
	}
	slices.Sort(result.SessionIDs)

	return result, nil
}

func (s *Store) ListRunnableSessions(_ context.Context) ([]string, error) {
	if s == nil {
		return nil, errors.New("store is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var sessionIDs []string
	for sessionID, inbox := range s.sessionInboxes {
		session, ok := s.sessions[sessionID]
		if !ok || session.Archived {
			continue
		}
		if _, ok := getMemoryActiveRun(inbox); ok {
			continue
		}
		if _, ok := getNextMemoryFollowUp(inbox); ok {
			sessionIDs = append(sessionIDs, sessionID)
		}
	}
	slices.Sort(sessionIDs)
	return sessionIDs, nil
}

func (s *Store) getOrCreateSessionInboxLocked(sessionID string) *sessionInboxState {
	inbox := s.sessionInboxes[sessionID]
	if inbox != nil {
		return inbox
	}
	inbox = &sessionInboxState{
		entries:            make(map[string]agentsession.QueueEntry),
		submissionEntryIDs: make(map[string]string),
		runs:               make(map[string]agentsession.ActiveRun),
	}
	s.sessionInboxes[sessionID] = inbox
	return inbox
}

func (s *Store) requireInboxSessionLocked(sessionID string) error {
	session, ok := s.sessions[sessionID]
	if !ok || session.Archived {
		return errors.New("session not found")
	}
	return nil
}

func (s *Store) appendSessionInboxEventLocked(
	inbox *sessionInboxState,
	event agentsession.Event,
) {
	inbox.cursor++
	event.Cursor = inbox.cursor
	inbox.events = append(inbox.events, cloneMemorySessionEvent(event))
	s.pruneSessionInboxEventsLocked(inbox, event.CreatedAt)
}

func (s *Store) pruneSessionInboxEventsLocked(inbox *sessionInboxState, now time.Time) {
	maxDeletedCursor := inbox.cursor - memorySessionEventRetentionCount
	if maxDeletedCursor > 0 {
		cutoff := now.Add(-memorySessionEventRetentionAge)
		retained := inbox.events[:0]
		for _, event := range inbox.events {
			if event.Cursor <= maxDeletedCursor && event.CreatedAt.Before(cutoff) {
				continue
			}
			retained = append(retained, event)
		}
		inbox.events = retained
	}
	inbox.retainedCursorFloor = 0
	if len(inbox.events) > 0 {
		inbox.retainedCursorFloor = inbox.events[0].Cursor
	}
}

func (s *Store) finishMemorySessionRunLocked(
	inbox *sessionInboxState,
	run agentsession.ActiveRun,
	status agentsession.RunStatus,
	reason string,
	lastError string,
) agentsession.ActiveRun {
	now := time.Now().UTC()
	run.Status = status
	run.CompletedAt = now
	run.UpdatedAt = now
	run.Reason = reason
	run.LastError = lastError
	inbox.runs[run.ID] = run

	queueError := lastError
	if queueError == "" {
		queueError = reason
	}
	if entry, ok := inbox.entries[run.QueueEntryID]; ok &&
		entry.Status == agentsession.QueueStatusActive {
		entry.Status = memoryQueueStatusFromRunStatus(status)
		entry.CompletedAt = now
		entry.UpdatedAt = now
		entry.LastError = queueError
		inbox.entries[entry.ID] = entry
		eventType := agentsession.EventTypeQueueUpdated
		if entry.Status == agentsession.QueueStatusCancelled {
			eventType = agentsession.EventTypeQueueCancelled
		}
		s.appendSessionInboxEventLocked(inbox, agentsession.Event{
			SessionID: run.SessionID,
			Type:      eventType,
			Queue:     cloneMemoryQueueEntryPointer(entry),
			CreatedAt: now,
		})
	}
	s.resolvePendingMemorySteeringLocked(inbox, run, now)
	s.appendSessionInboxEventLocked(inbox, agentsession.Event{
		SessionID: run.SessionID,
		Type:      memoryRunStatusToEventType(status),
		Run:       cloneMemoryActiveRunPointer(run),
		CreatedAt: now,
	})
	return run
}

func (s *Store) resolvePendingMemorySteeringLocked(
	inbox *sessionInboxState,
	run agentsession.ActiveRun,
	now time.Time,
) {
	entries := getPendingMemorySteering(inbox, run.ID)
	for _, entry := range entries {
		eventType := agentsession.EventTypeQueueUpdated
		if entry.SteeringFallback == agentsession.SteeringFallbackFollowUp {
			entry.DeliveryMode = agentsession.DeliveryModeFollowUp
			entry.TargetRunID = ""
		} else {
			entry.Status = agentsession.QueueStatusCancelled
			entry.CompletedAt = now
			entry.LastError = "target run completed before steering delivery"
			eventType = agentsession.EventTypeQueueCancelled
		}
		entry.UpdatedAt = now
		inbox.entries[entry.ID] = entry
		s.appendSessionInboxEventLocked(inbox, agentsession.Event{
			SessionID: run.SessionID,
			Type:      eventType,
			Queue:     cloneMemoryQueueEntryPointer(entry),
			CreatedAt: now,
		})
	}
}

func getMemoryActiveRun(
	inbox *sessionInboxState,
) (agentsession.ActiveRun, bool) {
	for _, run := range inbox.runs {
		if run.Status == agentsession.RunStatusRunning {
			return run, true
		}
	}
	return agentsession.ActiveRun{}, false
}

func getPendingMemoryQueueEntry(
	inbox *sessionInboxState,
	entryID string,
) (agentsession.QueueEntry, bool) {
	if inbox == nil {
		return agentsession.QueueEntry{}, false
	}
	entry, ok := inbox.entries[entryID]
	if !ok || entry.Status != agentsession.QueueStatusPending {
		return agentsession.QueueEntry{}, false
	}
	return entry, true
}

func getNextMemoryFollowUp(
	inbox *sessionInboxState,
) (agentsession.QueueEntry, bool) {
	var selected agentsession.QueueEntry
	found := false
	for _, entry := range inbox.entries {
		if entry.Status != agentsession.QueueStatusPending ||
			entry.DeliveryMode != agentsession.DeliveryModeFollowUp {
			continue
		}
		if !found ||
			entry.Priority > selected.Priority ||
			(entry.Priority == selected.Priority && entry.Sequence < selected.Sequence) {
			selected = entry
			found = true
		}
	}
	return selected, found
}

func getPendingMemorySteering(
	inbox *sessionInboxState,
	runID string,
) []agentsession.QueueEntry {
	var entries []agentsession.QueueEntry
	for _, entry := range inbox.entries {
		if isPendingMemorySteering(entry, runID) {
			entries = append(entries, entry)
		}
	}
	slices.SortFunc(entries, func(left, right agentsession.QueueEntry) int {
		return cmp.Compare(left.Sequence, right.Sequence)
	})
	return entries
}

func isPendingMemorySteering(entry agentsession.QueueEntry, runID string) bool {
	return entry.TargetRunID == runID &&
		entry.Status == agentsession.QueueStatusPending &&
		entry.DeliveryMode == agentsession.DeliveryModeSteering
}

func hasMatchingMemoryRun(
	inbox *sessionInboxState,
	req agentsession.SteeringClaimRequest,
) bool {
	if inbox == nil {
		return false
	}
	run, ok := inbox.runs[req.RunID]
	return ok &&
		run.SessionID == req.SessionID &&
		run.Generation == req.Generation &&
		run.Status == agentsession.RunStatusRunning
}

func normalizeMemorySubmitRequest(
	req agentsession.SubmitRequest,
) (agentsession.SubmitRequest, error) {
	req.ID = str.String(req.ID).Trim()
	req.SessionID = str.String(req.SessionID).Trim()
	req.Content = str.String(req.Content).Trim()
	req.Instruct = str.String(req.Instruct).Trim()
	req.ClientSubmissionID = str.String(req.ClientSubmissionID).Trim()
	req.DeliveryMode = agentsession.DeliveryMode(str.String(req.DeliveryMode).Normalized())
	req.SteeringFallback = agentsession.SteeringFallback(
		str.String(req.SteeringFallback).Normalized(),
	)
	req.Provenance.ActorKind = str.String(req.Provenance.ActorKind).Trim()
	req.Provenance.ActorID = str.String(req.Provenance.ActorID).Trim()
	req.Provenance.SurfaceKind = str.String(req.Provenance.SurfaceKind).Trim()
	req.Provenance.Surface = str.String(req.Provenance.Surface).Trim()
	req.Provenance.Profile = str.String(req.Provenance.Profile).Trim()
	if req.ID == "" {
		return req, errors.New("queue entry id is required")
	}
	if err := base.ValidateSessionID(req.SessionID); err != nil {
		return req, err
	}
	if req.Content == "" {
		return req, errors.New("message is required")
	}
	if req.ClientSubmissionID == "" {
		return req, errors.New("client submission id is required")
	}
	if req.DeliveryMode == "" {
		req.DeliveryMode = agentsession.DeliveryModeFollowUp
	}
	if req.SteeringFallback == "" {
		req.SteeringFallback = agentsession.SteeringFallbackFollowUp
	}
	if req.DeliveryMode != agentsession.DeliveryModeFollowUp &&
		req.DeliveryMode != agentsession.DeliveryModeSteering {
		return req, errors.New("delivery mode is invalid")
	}
	if req.SteeringFallback != agentsession.SteeringFallbackReject &&
		req.SteeringFallback != agentsession.SteeringFallbackFollowUp {
		return req, errors.New("steering fallback is invalid")
	}
	return req, nil
}

func normalizeMemorySessionID(sessionID string) (string, error) {
	sessionID = str.String(sessionID).Trim()
	if err := base.ValidateSessionID(sessionID); err != nil {
		return "", err
	}
	return sessionID, nil
}

func normalizeMemorySteeringClaimRequest(
	req *agentsession.SteeringClaimRequest,
) error {
	req.SessionID = str.String(req.SessionID).Trim()
	req.RunID = str.String(req.RunID).Trim()
	req.Generation = str.String(req.Generation).Trim()
	if err := base.ValidateSessionID(req.SessionID); err != nil {
		return err
	}
	if req.RunID == "" || req.Generation == "" {
		return errors.New("run id and generation are required")
	}
	return nil
}

func validateMemoryRunFinishRequest(req agentsession.RunFinishRequest) error {
	if err := base.ValidateSessionID(str.String(req.SessionID).Trim()); err != nil {
		return err
	}
	if str.String(req.RunID).Trim() == "" ||
		str.String(req.Generation).Trim() == "" {
		return errors.New("run id and generation are required")
	}
	switch req.Status {
	case agentsession.RunStatusCompleted,
		agentsession.RunStatusInterrupted,
		agentsession.RunStatusFailed,
		agentsession.RunStatusCancelled:
		return nil
	default:
		return errors.New("terminal run status is required")
	}
}

func memoryQueueStatusFromRunStatus(
	status agentsession.RunStatus,
) agentsession.QueueStatus {
	switch status {
	case agentsession.RunStatusFailed:
		return agentsession.QueueStatusFailed
	case agentsession.RunStatusInterrupted:
		return agentsession.QueueStatusInterrupted
	case agentsession.RunStatusCancelled:
		return agentsession.QueueStatusCancelled
	default:
		return agentsession.QueueStatusCompleted
	}
}

func memoryRunStatusToEventType(
	status agentsession.RunStatus,
) agentsession.EventType {
	switch status {
	case agentsession.RunStatusCompleted:
		return agentsession.EventTypeRunCompleted
	case agentsession.RunStatusInterrupted:
		return agentsession.EventTypeRunInterrupted
	case agentsession.RunStatusCancelled:
		return agentsession.EventTypeRunCancelled
	default:
		return agentsession.EventTypeRunFailed
	}
}

func isSameMemorySubmission(
	entry agentsession.QueueEntry,
	req agentsession.SubmitRequest,
) bool {
	return entry.SessionID == req.SessionID &&
		entry.Content == req.Content &&
		entry.Instruct == req.Instruct &&
		equalMemoryBool(entry.Stream, req.Stream) &&
		entry.ClientSubmissionID == req.ClientSubmissionID &&
		entry.RequestedDeliveryMode == req.DeliveryMode &&
		entry.SteeringFallback == req.SteeringFallback
}

func cloneMemoryQueueEntry(entry agentsession.QueueEntry) agentsession.QueueEntry {
	entry.Stream = cloneMemoryBool(entry.Stream)
	return entry
}

func cloneMemoryQueueEntryPointer(
	entry agentsession.QueueEntry,
) *agentsession.QueueEntry {
	cloned := cloneMemoryQueueEntry(entry)
	return &cloned
}

func cloneMemoryActiveRunPointer(
	run agentsession.ActiveRun,
) *agentsession.ActiveRun {
	cloned := run
	return &cloned
}

func cloneMemorySessionEvent(event agentsession.Event) agentsession.Event {
	cloned := event
	if event.Queue != nil {
		cloned.Queue = cloneMemoryQueueEntryPointer(*event.Queue)
	}
	if event.Run != nil {
		cloned.Run = cloneMemoryActiveRunPointer(*event.Run)
	}
	return cloned
}

func cloneMemoryBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func equalMemoryBool(left *bool, right *bool) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
