package tui

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/xymorphic/morph/internal/permissions"
	rpcclient "github.com/xymorphic/morph/internal/rpc/client"
	"github.com/xymorphic/morph/internal/rpc/rpcmeta"
	"github.com/xymorphic/morph/internal/trace"
	tuirpc "github.com/xymorphic/morph/internal/tui/rpc"
	agentsession "github.com/xymorphic/morph/pkg/agent/session"
	"github.com/xymorphic/morph/pkg/nanoid"
)

type responseEventMsg = tuirpc.ResponseEvent
type responseEventsClosedMsg = tuirpc.ResponseEventsClosed
type responseCompletedMsg = tuirpc.ResponseCompleted

const responseEventBatchLimit = 64

var streamingTranscriptRenderInterval = 33 * time.Millisecond
var permissionApprovalPollInterval = 100 * time.Millisecond

var errSessionObservationComplete = errors.New("session observation complete")

type responseEventBatchMsg struct {
	ResponseID int
	Messages   []tea.Msg
	Closed     bool
}

type streamingTranscriptFlushMsg struct {
	ResponseID int
}

type responsePermissionApprovalsMsg struct {
	ResponseID int
	Requests   []permissions.ApprovalRequest
	Err        error
}

func respondToPromptCmd(
	client rpcclient.ChatAPI,
	responseID int,
	ctx context.Context,
	sessionID string,
	prompt string,
	preset permissions.Preset,
	events chan<- tea.Msg,
) tea.Cmd {
	return func() tea.Msg {
		defer close(events)

		if ctx == nil {
			ctx = context.Background()
		}
		ctx = rpcmeta.WithOutgoingPermissionSurface(ctx, permissions.SurfaceTUI)
		ctx = rpcmeta.WithOutgoingPermissionPreset(ctx, preset)

		submissionID, err := nanoid.Generate("sub_")
		if err != nil {
			return responseCompletedMsg{ResponseID: responseID, Err: err}
		}
		entry, err := client.EnqueueMessage(ctx, rpcclient.EnqueueMessageOptions{
			SessionID:          sessionID,
			Message:            prompt,
			ClientSubmissionID: submissionID,
			DeliveryMode:       agentsession.DeliveryModeFollowUp,
			SteeringFallback:   agentsession.SteeringFallbackFollowUp,
		})
		if err != nil {
			return responseCompletedMsg{ResponseID: responseID, Err: err}
		}
		if isTerminalQueueStatus(entry.Status) {
			return responseCompletedMsg{
				ResponseID: responseID,
				Err:        getSessionQueueTerminalError(entry),
			}
		}
		state, err := client.State(ctx, sessionID)
		if err != nil {
			return responseCompletedMsg{ResponseID: responseID, Err: err}
		}
		var progressSequence int64
		for _, progress := range state.Progress {
			if progress.QueueEntryID != entry.ID {
				continue
			}
			progressSequence = max(progressSequence, progress.Sequence)
		}
		for _, queued := range state.Queue {
			if queued.ID == entry.ID && isTerminalQueueStatus(queued.Status) {
				return responseCompletedMsg{
					ResponseID:   responseID,
					QueueEntryID: entry.ID,
					Err:          getSessionQueueTerminalError(queued),
				}
			}
		}
		terminalObserved := false
		err = client.Observe(ctx, sessionID, state.Cursor, func(event rpcclient.SessionEvent) error {
			if event.Progress != nil &&
				event.Progress.QueueEntryID == entry.ID &&
				event.Progress.Sequence > progressSequence {
				progressSequence = event.Progress.Sequence
			}
			if event.Queue != nil && event.Queue.ID == entry.ID &&
				isTerminalQueueStatus(event.Queue.Status) {
				terminalObserved = true
				if err := getSessionQueueTerminalError(*event.Queue); err != nil {
					return err
				}
				return errSessionObservationComplete
			}
			if event.Run != nil && event.Run.QueueEntryID == entry.ID &&
				event.Run.Status != agentsession.RunStatusRunning {
				terminalObserved = true
				if err := getSessionRunTerminalError(*event.Run); err != nil {
					return err
				}
				return errSessionObservationComplete
			}
			return nil
		})
		if errors.Is(err, errSessionObservationComplete) {
			err = nil
		}
		if !terminalObserved {
			return responseCompletedMsg{ResponseID: responseID, Err: err}
		}
		return responseCompletedMsg{
			ResponseID:   responseID,
			QueueEntryID: entry.ID,
			Err:          err,
		}
	}
}

func sessionProgressToTUIMessage(progress agentsession.ProgressEvent) (tea.Msg, bool) {
	if progress.TraceEvent != nil {
		message, ok := traceEventToTUIMessage(trace.Event{
			SessionID: progress.TraceEvent.SessionID,
			Type:      progress.TraceEvent.Type,
			Timestamp: progress.TraceEvent.Timestamp,
			Payload:   progress.TraceEvent.Payload,
		})
		if !ok {
			return nil, false
		}
		if _, completed := message.(assistantResponseCompletedMsg); completed {
			return nil, false
		}
		if accepted, ok := message.(userMessageAcceptedMsg); ok {
			accepted.QueueEntryID = progress.QueueEntryID
			return accepted, true
		}
		return message, true
	}
	if progress.Text == "" {
		return nil, false
	}
	return assistantTextDeltaMsg{
		Channel: progress.Channel,
		Text:    progress.Text,
	}, true
}

func isTerminalQueueStatus(status agentsession.QueueStatus) bool {
	switch status {
	case agentsession.QueueStatusDelivered,
		agentsession.QueueStatusCompleted,
		agentsession.QueueStatusInterrupted,
		agentsession.QueueStatusFailed,
		agentsession.QueueStatusCancelled:
		return true
	default:
		return false
	}
}

func getSessionQueueTerminalError(entry rpcclient.SessionQueueEntry) error {
	switch entry.Status {
	case agentsession.QueueStatusInterrupted:
		if entry.LastError != "" {
			return fmt.Errorf("session run interrupted: %s", entry.LastError)
		}
		return errors.New("session run interrupted")
	case agentsession.QueueStatusFailed:
		if entry.LastError != "" {
			return errors.New(entry.LastError)
		}
		return errors.New("session run failed")
	case agentsession.QueueStatusCancelled:
		return errors.New("session run cancelled")
	default:
		return nil
	}
}

func getSessionRunTerminalError(run rpcclient.SessionActiveRun) error {
	switch run.Status {
	case agentsession.RunStatusFailed:
		if run.LastError != "" {
			return errors.New(run.LastError)
		}
		return errors.New("session run failed")
	case agentsession.RunStatusInterrupted:
		if run.Reason != "" {
			return fmt.Errorf("session run interrupted: %s", run.Reason)
		}
		return errors.New("session run interrupted")
	case agentsession.RunStatusCancelled:
		return errors.New("session run cancelled")
	default:
		return nil
	}
}

func waitForResponseEvent(responseID int, events <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-events
		if !ok {
			return responseEventsClosedMsg{ResponseID: responseID}
		}

		batch := responseEventBatchMsg{
			ResponseID: responseID,
			Messages:   []tea.Msg{msg},
		}
		for len(batch.Messages) < responseEventBatchLimit {
			select {
			case next, open := <-events:
				if !open {
					batch.Closed = true
					return batch
				}
				batch.Messages = append(batch.Messages, next)
			default:
				return batch
			}
		}

		return batch
	}
}

func pollResponsePermissionApprovalsCmd(
	ctx context.Context,
	client rpcclient.PermissionAPI,
	responseID int,
	sessionID string,
) tea.Cmd {
	if client == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		timer := time.NewTimer(permissionApprovalPollInterval)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}

		ctx = rpcmeta.WithOutgoingPermissionSurface(ctx, permissions.SurfaceTUI)
		requests, err := client.ListApprovalRequests(ctx, permissions.ApprovalQuery{
			Status: permissions.ApprovalPending,
			Limit:  100,
		})
		if err != nil {
			return responsePermissionApprovalsMsg{ResponseID: responseID, Err: err}
		}
		matching := make([]permissions.ApprovalRequest, 0, len(requests))
		for _, request := range requests {
			if request.SessionID == sessionID && request.Surface == permissions.SurfaceTUI {
				matching = append(matching, request)
			}
		}
		return responsePermissionApprovalsMsg{ResponseID: responseID, Requests: matching}
	}
}

func permissionApprovalMessageFromRequest(request permissions.ApprovalRequest) permissionApprovalMsg {
	return permissionApprovalMsg{
		RequestID:  request.ID,
		Status:     string(request.Status),
		Scope:      string(request.Scope),
		Summary:    request.Summary,
		Reason:     request.Reason,
		Effects:    effectsToStrings(request.Effects),
		Operations: append([]string(nil), request.Operations...),
		ExpiresAt:  request.ExpiresAt,
	}
}

func (m *model) startResponse(prompt string, followTranscript bool) tea.Cmd {
	if m.chatClient == nil {
		return nil
	}

	m.clearInterruptConfirmation()
	m.cancelResponseAndDrainEvents()
	responseCtx := m.chatCtx
	if responseCtx == nil {
		responseCtx = context.Background()
	}
	responseCtx, cancel := context.WithCancel(responseCtx)

	events := make(chan tea.Msg, 32)
	m.responseID++
	m.events = events
	m.responseCancel = cancel
	m.applyAction(setRespondingAction{Responding: true, ResponseID: m.responseID})
	m.responseStartMessageIndex = len(m.messages)
	m.responseStartedAt = currentTime()
	m.responseTranscriptFollow = followTranscript
	m.responseTranscriptScrolled = false
	m.responseRunningToolCount = 0
	m.responseEventStreamActive = true
	m.pendingResponseCompletion = nil
	m.toolAnimationActive = false
	m.stream.Reset()
	m.applyAction(setLiveTranscriptCellAction{})
	m.clearReasoningTranscriptState()

	return tea.Batch(
		m.startThinkingComposer(),
		respondToPromptCmd(
			m.chatClient,
			m.responseID,
			responseCtx,
			m.getCurrentSessionID(),
			prompt,
			m.permissionPreset,
			events,
		),
		waitForResponseEvent(m.responseID, events),
		pollResponsePermissionApprovalsCmd(
			responseCtx,
			m.permissionClient,
			m.responseID,
			m.getCurrentSessionID(),
		),
	)
}

func (m *model) handleResponseCompleted(msg responseCompletedMsg) tea.Cmd {
	if !m.isActiveResponse(msg.ResponseID) {
		return nil
	}
	if m.responseEventStreamActive {
		m.pendingResponseCompletion = &msg
		return nil
	}
	if !m.hasObservedResponseTerminal(msg.QueueEntryID) {
		m.pendingResponseCompletion = &msg
		return nil
	}

	return m.completeResponse(msg)
}

func (m *model) handleResponseEventsClosed(msg responseEventsClosedMsg) tea.Cmd {
	if !m.isActiveResponse(msg.ResponseID) {
		return nil
	}

	m.responseEventStreamActive = false
	m.events = nil
	if m.pendingResponseCompletion == nil {
		return nil
	}

	completion := *m.pendingResponseCompletion
	if !m.hasObservedResponseTerminal(completion.QueueEntryID) {
		return nil
	}
	m.pendingResponseCompletion = nil
	return m.completeResponse(completion)
}

func (m *model) hasObservedResponseTerminal(entryID string) bool {
	if entryID == "" {
		return true
	}
	for _, entry := range m.sessionExecutionState.Queue {
		if entry.ID == entryID {
			return isTerminalQueueStatus(entry.Status)
		}
	}
	return false
}

func (m *model) completePendingObservedResponse(entryID string) tea.Cmd {
	if m.responseEventStreamActive || m.pendingResponseCompletion == nil {
		return nil
	}
	completion := *m.pendingResponseCompletion
	if completion.QueueEntryID != entryID ||
		!m.hasObservedResponseTerminal(completion.QueueEntryID) {
		return nil
	}
	m.pendingResponseCompletion = nil
	return m.completeResponse(completion)
}

func (m *model) completeResponse(msg responseCompletedMsg) tea.Cmd {
	if !m.isActiveResponse(msg.ResponseID) {
		return nil
	}

	shouldFollowTranscript := m.responseTranscriptFollow && !m.responseTranscriptScrolled
	if msg.Err != nil {
		failure := formatToolFailureDisplayMessage(getUserFacingErrorMessage(msg.Err.Error()), false)
		m.failRunningToolTranscriptCells(currentTime(), failure)
		errorMsg := sessionErrorMsg{Message: msg.Err.Error()}
		m.addTranscriptMessage(errorMsg)
		m.resetResponseState()
		return tea.Batch(
			m.setStatus("response failed"),
			m.startSuccessorThinkingComposer(msg.QueueEntryID),
		)
	}

	m.interruptRunningToolTranscriptCells(currentTime())
	m.completeAssistantResponse(msg.Text, m.getCompletedResponseDuration())
	m.resetResponseState()
	if shouldFollowTranscript {
		m.transcript.GotoBottom()
	}
	return tea.Batch(
		m.startSuccessorThinkingComposer(msg.QueueEntryID),
		loadSessionTimelineCmd(m.chatCtx, m.timeline, m.getCurrentSessionID()),
		loadSessionTitleCmd(m.chatCtx, m.title),
		loadSessionContextCmd(m.chatCtx, m.contextLoader, m.getCurrentSessionID()),
	)
}

func (m *model) cancelActiveResponse() tea.Cmd {
	if !m.isTranscriptResponseActive() {
		return nil
	}

	m.cancelResponseAndDrainEvents()
	m.interruptRunningToolTranscriptCells(currentTime())
	m.resetResponseState()

	return tea.Batch(
		interruptSessionRunCmd(m.chatCtx, m.chatClient, m.getCurrentSessionID()),
		m.setStatus("interrupt requested"),
	)
}

func (m *model) cancelResponseAndDrainEvents() {
	if m.responseCancel != nil {
		m.responseCancel()
	}
	if m.events == nil {
		return
	}

	events := m.events
	m.events = nil
	go func() {
		for range events {
		}
	}()
}

func (m model) getCompletedResponseDuration() time.Duration {
	if m.responseStartedAt.IsZero() {
		return 0
	}

	return currentTime().Sub(m.responseStartedAt).Round(time.Second)
}

func (m model) isActiveResponse(responseID int) bool {
	return m.responding && responseID == m.responseID
}
