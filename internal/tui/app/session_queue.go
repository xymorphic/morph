package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	rpcclient "github.com/xymorphic/morph/internal/rpc/client"
	agentsession "github.com/xymorphic/morph/pkg/agent/session"
	"github.com/xymorphic/morph/pkg/nanoid"
)

const sessionQueueDisplayLimit = 5
const sessionQueueStateRetryDelay = 500 * time.Millisecond
const sessionRuntimeStateRetryLimit = 5

const (
	sessionQueueSteerIcon   = "↳"
	sessionQueuePromoteIcon = "↥"
	sessionQueueEditIcon    = "✎"
	sessionQueueRemoveIcon  = "×"
	sessionQueueActions     = sessionQueueSteerIcon + "  " + sessionQueuePromoteIcon + "  " +
		sessionQueueEditIcon + "  " + sessionQueueRemoveIcon
)

type sessionQueuePanelRow struct {
	entry        rpcclient.SessionQueueEntry
	pendingIndex int
}

type sessionExecutionStateLoadedMsg struct {
	State         rpcclient.SessionExecutionState
	Runtime       rpcclient.ModelRuntime
	RuntimeLoaded bool
}

type sessionExecutionStateLoadFailedMsg struct {
	Err error
}

type sessionQueueEventMsg struct {
	SessionID  string
	ObserverID uint64
	Event      rpcclient.SessionEvent
}

type sessionQueueEventsClosedMsg struct {
	SessionID  string
	ObserverID uint64
	Err        error
}

type sessionQueueMutationCompletedMsg struct {
	Action string
	Draft  string
	Entry  rpcclient.SessionQueueEntry
	Err    error
}

type sessionInterruptCompletedMsg struct {
	Transitioned bool
	Err          error
}

func loadSessionExecutionStateCmd(
	ctx context.Context,
	client rpcclient.ChatAPI,
	sessionID string,
) tea.Cmd {
	if client == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		state, err := client.State(ctx, sessionID)
		if err != nil {
			return sessionExecutionStateLoadFailedMsg{Err: err}
		}
		return sessionExecutionStateLoadedMsg{State: state}
	}
}

func loadSessionRuntimeStateCmd(
	ctx context.Context,
	client rpcclient.ChatAPI,
	modelClient rpcclient.ModelAPI,
	sessionID string,
) tea.Cmd {
	if client == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		runtime := rpcclient.ModelRuntime{}
		runtimeLoaded := false
		if modelClient != nil {
			value, err := modelClient.RuntimeModel(ctx)
			if err != nil {
				return sessionExecutionStateLoadFailedMsg{Err: err}
			}
			runtime = value
			runtimeLoaded = strings.TrimSpace(runtime.Provider) != "" ||
				strings.TrimSpace(runtime.API) != "" ||
				strings.TrimSpace(runtime.Model) != ""
		}
		state, err := client.State(ctx, sessionID)
		if err != nil {
			return sessionExecutionStateLoadFailedMsg{Err: err}
		}
		return sessionExecutionStateLoadedMsg{
			State:         state,
			Runtime:       runtime,
			RuntimeLoaded: runtimeLoaded,
		}
	}
}

func retrySessionExecutionStateLoadCmd(
	ctx context.Context,
	client rpcclient.ChatAPI,
	sessionID string,
) tea.Cmd {
	if client == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		timer := time.NewTimer(sessionQueueStateRetryDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
		return loadSessionExecutionStateCmd(ctx, client, sessionID)()
	}
}

func observeSessionQueueCmd(
	ctx context.Context,
	client rpcclient.ChatAPI,
	sessionID string,
	afterCursor int64,
	observerID uint64,
	events chan<- tea.Msg,
) tea.Cmd {
	return func() tea.Msg {
		err := client.Observe(ctx, sessionID, afterCursor, func(event rpcclient.SessionEvent) error {
			select {
			case events <- sessionQueueEventMsg{
				SessionID: sessionID, ObserverID: observerID, Event: event,
			}:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		select {
		case events <- sessionQueueEventsClosedMsg{
			SessionID: sessionID, ObserverID: observerID, Err: err,
		}:
		case <-ctx.Done():
		}
		close(events)
		return nil
	}
}

func waitForSessionQueueEvent(events <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return nil
		}
		return event
	}
}

func (m *model) applySessionExecutionState(msg sessionExecutionStateLoadedMsg) tea.Cmd {
	if msg.State.SessionID != m.getCurrentSessionID() {
		return nil
	}
	reasoningReported := hasReasoningModelTuple(msg.State.Reasoning.Model)
	reasoningMismatch := msg.RuntimeLoaded &&
		reasoningReported &&
		!reasoningTupleMatchesRuntime(msg.State.Reasoning.Model, msg.Runtime)

	previousActiveRunID := m.getActiveSessionRunID()
	if msg.State.SessionID != m.sessionObserverSessionID {
		m.sessionProgressSequences = nil
		m.sessionDeferredProgress = nil
	}
	selectedEntryID := m.getSelectedSessionQueueEntryID()
	if m.sessionObserverCancel != nil {
		m.sessionObserverCancel()
	}
	nextActiveRunID := ""
	if msg.State.ActiveRun != nil {
		nextActiveRunID = msg.State.ActiveRun.ID
	}
	if previousActiveRunID != "" && previousActiveRunID != nextActiveRunID {
		m.finalizeObservedRunResponse(previousActiveRunID)
	}
	if !m.responding && previousActiveRunID != nextActiveRunID {
		m.clearInterruptConfirmation()
	}
	m.sessionExecutionState = msg.State
	var reasoningStateCmd tea.Cmd
	switch {
	case reasoningMismatch:
		m.applyAction(setSessionReasoningAction{})
		retryKey := getSessionRuntimeStateRetryKey(msg)
		if retryKey != m.runtimeStateRetryKey {
			m.runtimeStateRetryKey = retryKey
			m.runtimeStateRetryAttempts = 0
		}
		if m.runtimeStateRetryAttempts >= sessionRuntimeStateRetryLimit {
			reasoningStateCmd = m.setStatus("reasoning state still waiting for daemon restart")
			break
		}
		m.runtimeStateRetryAttempts++
		reasoningStateCmd = tea.Batch(
			m.setStatus("reasoning state reconnecting"),
			retrySessionRuntimeStateLoadCmd(
				m.chatCtx,
				m.chatClient,
				m.modelClient,
				msg.State.SessionID,
				m.runtimeStateRetryAttempts,
			),
		)
	case msg.RuntimeLoaded:
		m.resetSessionRuntimeStateRetry()
		m.applyAction(setSessionReasoningAction{Settings: msg.State.Reasoning})
		m.modelRestartPending = false
		m.runtimeInfo.Provider = msg.Runtime.Provider
		m.runtimeInfo.API = msg.Runtime.API
		m.runtimeInfo.Model = msg.Runtime.Model
		m.modelName = getRuntimeModelDisplayName(msg.Runtime.Provider, msg.Runtime.API, msg.Runtime.Model)
	default:
		m.resetSessionRuntimeStateRetry()
		if !m.modelRestartPending && reasoningModelTupleMatchesRuntimeInfo(
			msg.State.Reasoning.Model,
			m.runtimeInfo,
		) {
			m.applyAction(setSessionReasoningAction{Settings: msg.State.Reasoning})
		}
	}
	m.initializeObservedRunTranscriptFollow(previousActiveRunID)
	m.sessionQueueStale = false
	m.setSessionQueueSelectionByID(selectedEntryID)
	m.clampSessionQueueSelection()
	m.resizeForSessionQueue()
	m.sessionObserverID++
	observerID := m.sessionObserverID

	observerCtx := m.chatCtx
	if observerCtx == nil {
		observerCtx = context.Background()
	}
	observerCtx, cancel := context.WithCancel(observerCtx)
	events := make(chan tea.Msg, 64)
	m.sessionObserverCancel = cancel
	m.sessionObserverEvents = events
	m.sessionObserverSessionID = msg.State.SessionID

	cmds := []tea.Cmd{
		observeSessionQueueCmd(
			observerCtx,
			m.chatClient,
			msg.State.SessionID,
			msg.State.Cursor,
			observerID,
			events,
		),
		waitForSessionQueueEvent(events),
	}
	if reasoningStateCmd != nil {
		cmds = append(cmds, reasoningStateCmd)
	}
	if msg.State.ActiveRun != nil {
		cmds = append(cmds, m.startThinkingComposer())
		cmds = append(
			cmds,
			m.flushDeferredSessionProgress(msg.State.ActiveRun.QueueEntryID)...,
		)
	}
	for _, progress := range msg.State.Progress {
		if !m.acceptSessionProgress(progress) {
			continue
		}
		if !m.isActiveRunProgress(progress) {
			continue
		}
		message, ok := sessionProgressToTUIMessage(progress)
		if !ok {
			continue
		}
		cmds = append(cmds, m.applyTUIMessage(message))
	}
	if m.pendingResponseCompletion != nil {
		cmds = append(
			cmds,
			m.completePendingObservedResponse(
				m.pendingResponseCompletion.QueueEntryID,
			),
		)
	}
	return tea.Batch(cmds...)
}

func retrySessionRuntimeStateLoadCmd(
	ctx context.Context,
	client rpcclient.ChatAPI,
	modelClient rpcclient.ModelAPI,
	sessionID string,
	attempts ...int,
) tea.Cmd {
	return func() tea.Msg {
		if ctx == nil {
			ctx = context.Background()
		}
		delay := sessionQueueStateRetryDelay
		if len(attempts) > 0 {
			for attempt := 1; attempt < attempts[0] && delay < 4*time.Second; attempt++ {
				delay *= 2
			}
		}
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
		cmd := loadSessionRuntimeStateCmd(ctx, client, modelClient, sessionID)
		if cmd == nil {
			return nil
		}
		return cmd()
	}
}

func getSessionRuntimeStateRetryKey(msg sessionExecutionStateLoadedMsg) string {
	return strings.Join([]string{
		msg.State.SessionID,
		msg.State.Reasoning.Model.Provider,
		msg.State.Reasoning.Model.API,
		msg.State.Reasoning.Model.Model,
		msg.Runtime.Provider,
		msg.Runtime.API,
		msg.Runtime.Model,
	}, "\x00")
}

func (m *model) resetSessionRuntimeStateRetry() {
	m.runtimeStateRetryKey = ""
	m.runtimeStateRetryAttempts = 0
}

func reasoningModelTupleMatchesRuntimeInfo(
	tuple agentsession.ReasoningModelTuple,
	runtime runtimeInfo,
) bool {
	return strings.EqualFold(strings.TrimSpace(tuple.Provider), strings.TrimSpace(runtime.Provider)) &&
		strings.EqualFold(strings.TrimSpace(tuple.API), strings.TrimSpace(runtime.API)) &&
		strings.EqualFold(strings.TrimSpace(tuple.Model), strings.TrimSpace(runtime.Model))
}

func reasoningTupleMatchesRuntime(
	tuple agentsession.ReasoningModelTuple,
	runtime rpcclient.ModelRuntime,
) bool {
	return strings.EqualFold(strings.TrimSpace(tuple.Provider), strings.TrimSpace(runtime.Provider)) &&
		strings.EqualFold(strings.TrimSpace(tuple.API), strings.TrimSpace(runtime.API)) &&
		strings.EqualFold(strings.TrimSpace(tuple.Model), strings.TrimSpace(runtime.Model))
}

func hasReasoningModelTuple(tuple agentsession.ReasoningModelTuple) bool {
	return strings.TrimSpace(tuple.Provider) != "" ||
		strings.TrimSpace(tuple.API) != "" ||
		strings.TrimSpace(tuple.Model) != ""
}

func (m *model) applySessionQueueEvent(msg sessionQueueEventMsg) tea.Cmd {
	if msg.SessionID != m.getCurrentSessionID() ||
		msg.SessionID != m.sessionObserverSessionID ||
		msg.ObserverID != m.sessionObserverID {
		return nil
	}
	event := msg.Event
	if event.Progress != nil {
		cmds := []tea.Cmd{waitForSessionQueueEvent(m.sessionObserverEvents)}
		if m.acceptSessionProgress(*event.Progress) {
			if m.isActiveRunProgress(*event.Progress) {
				if message, ok := sessionProgressToTUIMessage(*event.Progress); ok {
					cmds = append(cmds, m.applyTUIMessage(message))
				}
			} else {
				m.sessionDeferredProgress = append(
					m.sessionDeferredProgress,
					*event.Progress,
				)
			}
		}
		return tea.Batch(cmds...)
	}
	if event.Cursor <= m.sessionExecutionState.Cursor {
		return waitForSessionQueueEvent(m.sessionObserverEvents)
	}
	cmds := []tea.Cmd{waitForSessionQueueEvent(m.sessionObserverEvents)}
	selectedEntryID := m.getSelectedSessionQueueEntryID()
	m.sessionExecutionState.Cursor = event.Cursor
	terminalEntryID := ""
	if event.Queue != nil {
		m.setSessionQueueEntry(*event.Queue)
		if isTerminalQueueStatus(event.Queue.Status) {
			terminalEntryID = event.Queue.ID
		}
	}
	if event.Run != nil {
		if !m.responding && (event.Run.Status != agentsession.RunStatusRunning ||
			m.getActiveSessionRunID() != event.Run.ID) {
			m.clearInterruptConfirmation()
		}
		if event.Run.Status == agentsession.RunStatusRunning {
			previousActiveRunID := m.getActiveSessionRunID()
			run := *event.Run
			m.sessionExecutionState.ActiveRun = &run
			m.setActiveRunReasoning(run.Reasoning)
			m.initializeObservedRunTranscriptFollow(previousActiveRunID)
			cmds = append(cmds, m.startThinkingComposer())
			cmds = append(cmds, m.flushDeferredSessionProgress(run.QueueEntryID)...)
		} else {
			m.finalizeObservedRunResponse(event.Run.ID)
			m.sessionExecutionState.ActiveRun = nil
			m.setActiveRunReasoning(agentsession.ReasoningSnapshot{})
			m.dropDeferredSessionProgress(event.Run.QueueEntryID)
		}
	}
	m.updateSessionQueueMetrics()
	m.setSessionQueueSelectionByID(selectedEntryID)
	m.clampSessionQueueSelection()
	m.resizeForSessionQueue()

	completedResponse := false
	if terminalEntryID != "" {
		if cmd := m.completePendingObservedResponse(terminalEntryID); cmd != nil {
			cmds = append(cmds, cmd)
			completedResponse = true
		}
	}
	if event.Run != nil &&
		event.Run.Status != agentsession.RunStatusRunning &&
		!completedResponse {
		cmds = append(cmds,
			loadSessionTimelineCmd(m.chatCtx, m.timeline, msg.SessionID),
			loadSessionContextCmd(m.chatCtx, m.contextLoader, msg.SessionID),
		)
	}
	return tea.Batch(cmds...)
}

func (m *model) finalizeObservedRunResponse(runID string) {
	activeRun := m.sessionExecutionState.ActiveRun
	if m.responding ||
		activeRun == nil ||
		activeRun.ID != runID ||
		m.live == nil ||
		m.live.IsEmpty() {
		return
	}

	m.completeAssistantResponse("", m.getCompletedResponseDuration())
}

func (m *model) setActiveRunReasoning(snapshot agentsession.ReasoningSnapshot) {
	if strings.TrimSpace(snapshot.Provider) == "" &&
		strings.TrimSpace(snapshot.API) == "" &&
		strings.TrimSpace(snapshot.Model) == "" &&
		strings.TrimSpace(string(snapshot.Effort)) == "" {
		m.reasoning.ActiveRunSnapshot = nil
		m.sessionExecutionState.Reasoning.ActiveRunSnapshot = nil
		return
	}
	value := snapshot
	m.reasoning.ActiveRunSnapshot = &value
	m.sessionExecutionState.Reasoning.ActiveRunSnapshot = &value
}

func (m model) getActiveSessionRunID() string {
	if m.sessionExecutionState.ActiveRun == nil {
		return ""
	}
	return m.sessionExecutionState.ActiveRun.ID
}

func (m *model) initializeObservedRunTranscriptFollow(previousRunID string) {
	activeRun := m.sessionExecutionState.ActiveRun
	if m.responding || activeRun == nil || activeRun.ID == previousRunID {
		return
	}

	m.responseTranscriptFollow = m.isTranscriptAtAbsoluteBottom()
	m.responseTranscriptScrolled = false
}

func (m *model) isActiveRunProgress(
	progress agentsession.ProgressEvent,
) bool {
	activeRun := m.sessionExecutionState.ActiveRun
	return activeRun != nil && progress.QueueEntryID == activeRun.QueueEntryID
}

func (m *model) acceptSessionProgress(progress agentsession.ProgressEvent) bool {
	if m.sessionProgressSequences == nil {
		m.sessionProgressSequences = make(map[string]int64)
	}
	runID := progress.RunID
	if runID == "" {
		runID = progress.QueueEntryID
	}
	if progress.Sequence <= m.sessionProgressSequences[runID] {
		return false
	}
	m.sessionProgressSequences[runID] = progress.Sequence
	return true
}

func (m *model) flushDeferredSessionProgress(entryID string) []tea.Cmd {
	var (
		cmds      []tea.Cmd
		remaining []agentsession.ProgressEvent
	)
	for _, progress := range m.sessionDeferredProgress {
		if progress.QueueEntryID != entryID {
			remaining = append(remaining, progress)
			continue
		}
		if message, ok := sessionProgressToTUIMessage(progress); ok {
			cmds = append(cmds, m.applyTUIMessage(message))
		}
	}
	m.sessionDeferredProgress = remaining
	return cmds
}

func (m *model) dropDeferredSessionProgress(entryID string) {
	m.sessionDeferredProgress = slices.DeleteFunc(
		m.sessionDeferredProgress,
		func(progress agentsession.ProgressEvent) bool {
			return progress.QueueEntryID == entryID
		},
	)
}

func (m *model) handleSessionQueueEventsClosed(msg sessionQueueEventsClosedMsg) tea.Cmd {
	if msg.SessionID != m.getCurrentSessionID() ||
		msg.SessionID != m.sessionObserverSessionID ||
		msg.ObserverID != m.sessionObserverID {
		return nil
	}
	m.sessionObserverEvents = nil
	m.sessionObserverCancel = nil
	if errors.Is(msg.Err, context.Canceled) {
		return nil
	}
	m.sessionQueueStale = true
	m.resizeForSessionQueue()
	return tea.Batch(
		m.setStatus("session queue reconnecting"),
		loadSessionRuntimeStateCmd(m.chatCtx, m.chatClient, m.modelClient, msg.SessionID),
	)
}

func (m *model) setSessionQueueEntry(entry rpcclient.SessionQueueEntry) {
	for index := range m.sessionExecutionState.Queue {
		if m.sessionExecutionState.Queue[index].ID == entry.ID {
			m.sessionExecutionState.Queue[index] = entry
			return
		}
	}
	m.sessionExecutionState.Queue = append(m.sessionExecutionState.Queue, entry)
}

func (m *model) updateSessionQueueMetrics() {
	m.sessionExecutionState.QueueDepth = 0
	m.sessionExecutionState.OldestPendingCreated = time.Time{}
	oldestSet := false
	for _, entry := range m.sessionExecutionState.Queue {
		if entry.Status != agentsession.QueueStatusPending {
			continue
		}
		m.sessionExecutionState.QueueDepth++
		if !oldestSet || entry.CreatedAt.Before(m.sessionExecutionState.OldestPendingCreated) {
			m.sessionExecutionState.OldestPendingCreated = entry.CreatedAt
			oldestSet = true
		}
	}
	if !oldestSet {
		m.sessionExecutionState.OldestPendingCreated = time.Time{}
	}
}

func submitQueuedMessageCmd(
	ctx context.Context,
	client rpcclient.ChatAPI,
	sessionID string,
	message string,
	mode agentsession.DeliveryMode,
	fallback agentsession.SteeringFallback,
) tea.Cmd {
	return func() tea.Msg {
		submissionID, err := nanoid.Generate("sub_")
		if err != nil {
			return sessionQueueMutationCompletedMsg{Action: "submit", Draft: message, Err: err}
		}
		entry, err := client.EnqueueMessage(ctx, rpcclient.EnqueueMessageOptions{
			SessionID:          sessionID,
			Message:            message,
			ClientSubmissionID: submissionID,
			DeliveryMode:       mode,
			SteeringFallback:   fallback,
		})
		return sessionQueueMutationCompletedMsg{
			Action: "submit",
			Draft:  message,
			Entry:  entry,
			Err:    err,
		}
	}
}

func editQueuedMessageCmd(
	ctx context.Context,
	client rpcclient.ChatAPI,
	sessionID string,
	entryID string,
	message string,
) tea.Cmd {
	return func() tea.Msg {
		entry, err := client.EditQueuedMessage(ctx, sessionID, entryID, message)
		return sessionQueueMutationCompletedMsg{
			Action: "edit",
			Draft:  message,
			Entry:  entry,
			Err:    err,
		}
	}
}

func mutateQueuedMessageCmd(
	ctx context.Context,
	client rpcclient.ChatAPI,
	sessionID string,
	entryID string,
	action string,
) tea.Cmd {
	return func() tea.Msg {
		var entry rpcclient.SessionQueueEntry
		var err error
		switch action {
		case "remove":
			entry, err = client.RemoveQueuedMessage(ctx, sessionID, entryID)
		case "promote":
			entry, err = client.PromoteQueuedMessage(ctx, sessionID, entryID)
		case "steer":
			entry, err = client.SteerQueuedMessage(ctx, sessionID, entryID)
		default:
			err = errors.New("unsupported queue action")
		}
		return sessionQueueMutationCompletedMsg{Action: action, Entry: entry, Err: err}
	}
}

func interruptSessionRunCmd(
	ctx context.Context,
	client rpcclient.ChatAPI,
	sessionID string,
) tea.Cmd {
	if client == nil {
		return nil
	}
	return func() tea.Msg {
		_, transitioned, err := client.InterruptRun(ctx, sessionID)
		return sessionInterruptCompletedMsg{Transitioned: transitioned, Err: err}
	}
}

func (m *model) completeSessionQueueMutation(msg sessionQueueMutationCompletedMsg) tea.Cmd {
	if msg.Err != nil {
		if msg.Action == "edit" && m.sessionQueueEditingEntryID != "" {
			m.sessionQueueEditSaving = false
			return m.setStatus("queue edit failed")
		}
		if msg.Draft != "" && strings.TrimSpace(m.input.Value()) == "" {
			m.input.SetValue(msg.Draft)
			m.resize()
		} else if msg.Draft != "" && m.input.Value() != msg.Draft {
			m.input.SetValue(msg.Draft + "\n" + m.input.Value())
			m.updateCommandMenuForInput(m.input.Value())
			m.resize()
		}
		return m.setStatus("queue " + msg.Action + " failed")
	}
	m.setSessionQueueEntry(msg.Entry)
	m.updateSessionQueueMetrics()
	if msg.Action != "remove" {
		m.setSessionQueueSelectionByID(msg.Entry.ID)
	}
	if msg.Action == "edit" && m.sessionQueueEditingEntryID == msg.Entry.ID {
		m.finishSessionQueueEdit()
	}
	m.clampSessionQueueSelection()
	m.resizeForSessionQueue()
	return tea.Batch(
		m.setStatus("queue "+msg.Action+" complete"),
		loadSessionExecutionStateCmd(m.chatCtx, m.chatClient, m.getCurrentSessionID()),
	)
}

func (m *model) completeSessionInterrupt(msg sessionInterruptCompletedMsg) tea.Cmd {
	if msg.Err != nil {
		return m.setStatus("interrupt failed")
	}
	status := "no active run"
	if msg.Transitioned {
		status = "run interrupted"
	}
	return tea.Batch(
		m.setStatus(status),
		loadSessionExecutionStateCmd(m.chatCtx, m.chatClient, m.getCurrentSessionID()),
	)
}

func (m *model) submitSteeringMessage(message string) tea.Cmd {
	message = strings.TrimSpace(message)
	if message == "" {
		return m.setStatus("usage: /steer <message>")
	}
	return submitQueuedMessageCmd(
		m.chatCtx,
		m.chatClient,
		m.getCurrentSessionID(),
		message,
		agentsession.DeliveryModeSteering,
		agentsession.SteeringFallbackFollowUp,
	)
}

func (m *model) requestSessionInterrupt() tea.Cmd {
	return interruptSessionRunCmd(m.chatCtx, m.chatClient, m.getCurrentSessionID())
}

func (m *model) handleQueueCommand(args string) tea.Cmd {
	args = strings.TrimSpace(args)
	if args != "" {
		return m.setStatus("usage: /queue")
	}
	if m.sessionQueueStale {
		return tea.Batch(
			m.setStatus("queue state is stale; refreshing"),
			loadSessionExecutionStateCmd(m.chatCtx, m.chatClient, m.getCurrentSessionID()),
		)
	}
	if len(m.getPendingSessionQueueEntries()) == 0 {
		return tea.Batch(
			m.setStatus("no queued messages"),
			loadSessionExecutionStateCmd(m.chatCtx, m.chatClient, m.getCurrentSessionID()),
		)
	}
	m.sessionQueueFocused = true
	m.clampSessionQueueSelection()
	m.input.Blur()
	return tea.Batch(
		m.setStatus("queue focused"),
		loadSessionExecutionStateCmd(m.chatCtx, m.chatClient, m.getCurrentSessionID()),
	)
}

func (m model) handleSessionQueueKeyPress(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	if m.sessionQueueEditingEntryID != "" {
		if msg.Keystroke() == "esc" {
			m.cancelSessionQueueEdit()
			return m, m.setStatus("queue edit cancelled"), true
		}
		if m.sessionQueueEditSaving {
			return m, nil, true
		}
		return m, nil, false
	}

	if msg.Keystroke() == "ctrl+q" {
		if m.sessionQueueFocused {
			m.leaveSessionQueue()
			return m, nil, true
		}
		if m.sessionQueueStale {
			return m, m.setStatus("queue state is stale; refreshing"), true
		}
		if len(m.getPendingSessionQueueEntries()) == 0 {
			return m, m.setStatus("no queued messages"), true
		}
		m.sessionQueueFocused = true
		m.clampSessionQueueSelection()
		m.input.Blur()
		return m, nil, true
	}
	if !m.sessionQueueFocused {
		return m, nil, false
	}
	if m.sessionQueueStale {
		return m, m.setStatus("queue state is stale; refreshing"), true
	}

	pending := m.getPendingSessionQueueEntries()
	if len(pending) == 0 {
		m.leaveSessionQueue()
		return m, nil, true
	}
	switch msg.Keystroke() {
	case "esc", "ctrl+q":
		m.leaveSessionQueue()
		return m, nil, true
	case "up", "k":
		m.sessionQueueSelected = max(m.sessionQueueSelected-1, 0)
		return m, nil, true
	case "down", "j":
		m.sessionQueueSelected = min(m.sessionQueueSelected+1, len(pending)-1)
		return m, nil, true
	case "enter":
		entry := pending[m.sessionQueueSelected]
		return m, mutateQueuedMessageCmd(
			m.chatCtx,
			m.chatClient,
			m.getCurrentSessionID(),
			entry.ID,
			"promote",
		), true
	case "s":
		entry := pending[m.sessionQueueSelected]
		return m, mutateQueuedMessageCmd(
			m.chatCtx,
			m.chatClient,
			m.getCurrentSessionID(),
			entry.ID,
			"steer",
		), true
	case "e":
		entry := pending[m.sessionQueueSelected]
		return m, m.beginSessionQueueEdit(entry), true
	case "x", "delete", "backspace":
		entry := pending[m.sessionQueueSelected]
		return m, mutateQueuedMessageCmd(
			m.chatCtx,
			m.chatClient,
			m.getCurrentSessionID(),
			entry.ID,
			"remove",
		), true
	default:
		return m, nil, true
	}
}

func (m *model) handleSessionQueueClick(msg tea.MouseClickMsg) (tea.Cmd, bool) {
	if msg.Button != tea.MouseLeft {
		return nil, false
	}
	layout := m.getTUILayout(m.input.Height())
	panel := layout.SessionQueue
	if panel.Height == 0 || msg.Y < panel.Y || msg.Y >= panel.Y+panel.Height {
		return nil, false
	}
	if msg.Y == panel.Y {
		if m.sessionQueueStale {
			return m.setStatus("queue state is stale; refreshing"), true
		}
		if len(m.getPendingSessionQueueEntries()) > 0 {
			m.sessionQueueFocused = true
			m.clampSessionQueueSelection()
			m.input.Blur()
		}
		return nil, true
	}

	rows := m.getSessionQueuePanelRows()
	rowIndex := msg.Y - panel.Y - 1
	if rowIndex < 0 || rowIndex >= len(rows) {
		return nil, true
	}
	row := rows[rowIndex]
	if row.pendingIndex < 0 {
		return nil, true
	}
	m.sessionQueueSelected = row.pendingIndex
	m.sessionQueueFocused = true
	m.input.Blur()
	if m.sessionQueueStale {
		return m.setStatus("queue state is stale; refreshing"), true
	}

	actionStart := panel.X + panel.Width - 1 - lipgloss.Width(sessionQueueActions)
	if msg.X < actionStart {
		return nil, true
	}
	switch {
	case msg.X < actionStart+2:
		return mutateQueuedMessageCmd(
			m.chatCtx,
			m.chatClient,
			m.getCurrentSessionID(),
			row.entry.ID,
			"steer",
		), true
	case msg.X < actionStart+5:
		return mutateQueuedMessageCmd(
			m.chatCtx,
			m.chatClient,
			m.getCurrentSessionID(),
			row.entry.ID,
			"promote",
		), true
	case msg.X < actionStart+8:
		return m.beginSessionQueueEdit(row.entry), true
	default:
		return mutateQueuedMessageCmd(
			m.chatCtx,
			m.chatClient,
			m.getCurrentSessionID(),
			row.entry.ID,
			"remove",
		), true
	}
}

func (m *model) beginSessionQueueEdit(entry rpcclient.SessionQueueEntry) tea.Cmd {
	m.sessionQueueComposerDraft = m.input.Value()
	m.sessionQueueEditingEntryID = entry.ID
	m.sessionQueueEditSaving = false
	m.sessionQueueFocused = false
	m.input.SetValue(entry.Content)
	m.commandMenuOffset = 0
	m.commandMenuSelected = 0
	m.commandMenuPrefix = ""
	m.input.Focus()
	m.resize()
	return m.setStatus("editing queued message · enter to save · esc to cancel")
}

func (m *model) finishSessionQueueEdit() {
	m.input.SetValue(m.sessionQueueComposerDraft)
	m.sessionQueueEditingEntryID = ""
	m.sessionQueueComposerDraft = ""
	m.sessionQueueEditSaving = false
	m.updateCommandMenuForInput(m.input.Value())
	m.input.Focus()
}

func (m *model) cancelSessionQueueEdit() {
	m.finishSessionQueueEdit()
	m.resize()
}

func (m *model) leaveSessionQueue() {
	m.sessionQueueFocused = false
	m.input.Focus()
}

func (m *model) clampSessionQueueSelection() {
	pending := m.getPendingSessionQueueEntries()
	if len(pending) == 0 {
		m.sessionQueueSelected = 0
		m.sessionQueueFocused = false
		m.input.Focus()
		return
	}
	m.sessionQueueSelected = min(max(m.sessionQueueSelected, 0), len(pending)-1)
}

func (m model) getSelectedSessionQueueEntryID() string {
	if !m.sessionQueueFocused {
		return ""
	}
	return m.getSelectedPendingSessionQueueEntryID()
}

func (m model) getSelectedPendingSessionQueueEntryID() string {
	pending := m.getPendingSessionQueueEntries()
	if m.sessionQueueSelected < 0 || m.sessionQueueSelected >= len(pending) {
		return ""
	}
	return pending[m.sessionQueueSelected].ID
}

func (m *model) setSessionQueueSelectionByID(entryID string) {
	if entryID == "" {
		return
	}
	for index, entry := range m.getPendingSessionQueueEntries() {
		if entry.ID == entryID {
			m.sessionQueueSelected = index
			return
		}
	}
}

func (m model) getPendingSessionQueueEntries() []rpcclient.SessionQueueEntry {
	entries := make(
		[]rpcclient.SessionQueueEntry,
		0,
		max(m.sessionExecutionState.QueueDepth, 0),
	)
	for _, entry := range m.sessionExecutionState.Queue {
		if entry.Status == agentsession.QueueStatusPending {
			entries = append(entries, entry)
		}
	}
	slices.SortStableFunc(entries, func(left rpcclient.SessionQueueEntry, right rpcclient.SessionQueueEntry) int {
		if left.Priority != right.Priority {
			if left.Priority > right.Priority {
				return -1
			}
			return 1
		}
		switch {
		case left.Sequence < right.Sequence:
			return -1
		case left.Sequence > right.Sequence:
			return 1
		default:
			return 0
		}
	})
	return entries
}

func (m model) getSessionQueuePanelRows() []sessionQueuePanelRow {
	pending := m.getPendingSessionQueueEntries()
	if len(pending) == 0 {
		return nil
	}

	rows := make([]sessionQueuePanelRow, 0, sessionQueueDisplayLimit)
	available := sessionQueueDisplayLimit
	start := 0
	if m.sessionQueueFocused && m.sessionQueueSelected >= available {
		start = m.sessionQueueSelected - available + 1
	}
	end := min(start+available, len(pending))
	for index := start; index < end; index++ {
		rows = append(rows, sessionQueuePanelRow{
			entry:        pending[index],
			pendingIndex: index,
		})
	}
	return rows
}

func (m model) renderSessionQueue() string {
	if m.shouldShowNamePrompt() || m.shouldShowProfileModelSetup() ||
		m.isCommandViewVisible() {
		return ""
	}
	rows := m.getSessionQueuePanelRows()
	if len(rows) == 0 {
		return ""
	}

	width := m.getMainPaneWidth()
	rendered := make([]string, 0, len(rows)+1)
	rendered = append(rendered, m.renderSessionQueueHeader(width))
	for _, row := range rows {
		rendered = append(rendered, m.renderSessionQueueRow(row, width))
	}
	return strings.Join(rendered, "\n")
}

func (m model) renderSessionQueueHeader(width int) string {
	pendingCount := len(m.getPendingSessionQueueEntries())
	left := "Queue"
	if pendingCount > 0 {
		left += "  " + formatQueueCount(pendingCount)
	}
	if m.sessionQueueStale {
		left += "  reconnecting"
	}
	right := ""
	if pendingCount > 0 {
		right = "ctrl+q"
	}
	if m.sessionQueueFocused {
		right = "↑↓ select · ↳ steer · ↥ next · ✎ edit · × remove · esc"
	}
	return renderSessionQueueSplitLine(
		left,
		right,
		width,
		defaultTUITheme.ToolTitle,
		defaultTUITheme.MutedText,
		defaultTUITheme.InputFrameBackground,
	)
}

func (m model) renderSessionQueueRow(row sessionQueuePanelRow, width int) string {
	background := defaultTUITheme.InputFrameBackground
	selected := m.sessionQueueFocused &&
		row.pendingIndex == m.sessionQueueSelected
	if selected {
		background = defaultTUITheme.NoticeBackground
	}

	selection := " "
	if selected {
		selection = "›"
	}
	icon := "○"
	if row.entry.RequestedDeliveryMode == agentsession.DeliveryModeSteering {
		icon = "↳"
	}
	right := sessionQueueActions
	if m.sessionQueueStale {
		right = "unavailable"
	}
	left := selection + " " + icon + " " + compactQueuePreview(row.entry.Content, max(width, 1))
	rightColor := defaultTUITheme.MutedText
	if selected {
		rightColor = defaultTUITheme.NoticeForeground
	}
	return renderSessionQueueSplitLine(
		left,
		right,
		width,
		defaultTUITheme.ToolTitle,
		rightColor,
		background,
	)
}

func renderSessionQueueSplitLine(
	left string,
	right string,
	width int,
	leftColor string,
	rightColor string,
	background string,
) string {
	width = max(width, 1)
	innerWidth := max(width-2, 1)
	rightWidth := lipgloss.Width(right)
	if rightWidth+4 > innerWidth {
		right = ""
		rightWidth = 0
	}
	leftWidth := innerWidth
	if right != "" {
		leftWidth = max(innerWidth-rightWidth-1, 1)
	}
	left = truncateCommandMenuText(left, leftWidth)
	gap := max(innerWidth-lipgloss.Width(left)-rightWidth, 0)

	base := lipgloss.NewStyle().Background(lipgloss.Color(background))
	leftText := base.
		Foreground(lipgloss.Color(leftColor)).
		Render(left)
	rightText := base.
		Foreground(lipgloss.Color(rightColor)).
		Render(right)
	line := " " + leftText + base.Render(strings.Repeat(" ", gap)) + rightText + " "
	return base.Width(width).Render(line)
}

func formatQueueCount(count int) string {
	if count == 1 {
		return "1 message"
	}
	return fmt.Sprintf("%d messages", count)
}

func (m model) getSessionQueueHeight() int {
	panel := m.renderSessionQueue()
	if panel == "" {
		return 0
	}
	return lipgloss.Height(panel)
}

func (m *model) resizeForSessionQueue() {
	position := m.getTranscriptWindowPosition()
	m.resize()
	m.refreshTranscriptContentAtPosition(position)
}

func compactQueuePreview(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit-1]) + "…"
}
