package tui

import (
	"context"
	"fmt"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"

	agentapi "github.com/xymorphic/morph/internal/agent"
	rpcclient "github.com/xymorphic/morph/internal/rpc/client"
	storage "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/internal/trace"
	tuirpc "github.com/xymorphic/morph/internal/tui/rpc"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/str"
)

type sessionTimelineLoader = tuirpc.SessionTimelineLoader

// SessionTimelineLoader aliases tuirpc.SessionTimelineLoader at this package boundary.
type SessionTimelineLoader = tuirpc.SessionTimelineLoader
type sessionTimelineLoadedMsg = tuirpc.SessionTimelineLoaded
type sessionTimelineLoadFailedMsg = tuirpc.SessionTimelineLoadFailed

type sessionTitleLoader interface {
	Current(context.Context) (storage.Session, error)
}

type startupSessionLoader interface {
	Current(context.Context) (storage.Session, error)
	List(context.Context, ...rpcclient.SessionListOptions) ([]storage.Session, error)
	Use(context.Context, string) error
	Timeline(context.Context, rpcclient.SessionTimelineOptions) (rpcclient.SessionTimeline, error)
}

type sessionTitleLoadedMsg struct {
	Session storage.Session
}

type sessionTitleLoadFailedMsg struct{}

type olderSessionTimelineLoadedMsg struct {
	Timeline rpcclient.SessionTimeline
	Options  rpcclient.SessionTimelineOptions
}

type olderSessionTimelineLoadFailedMsg struct {
	SessionID string
}

const (
	sessionTimelineMessagePageSize = 100
	sessionTimelineTracePageSize   = 500
)

func loadSessionTimelineCmd(ctx context.Context, client sessionTimelineLoader, sessionID string) tea.Cmd {
	if client == nil {
		return nil
	}

	if ctx == nil {
		ctx = context.Background()
	}

	return func() tea.Msg {
		sessionIDValue := str.String(sessionID)
		timeline, err := client.Timeline(ctx, rpcclient.SessionTimelineOptions{
			SessionID: sessionIDValue.Trim(),
		})
		if err != nil {
			return sessionTimelineLoadFailedMsg{Err: err}
		}

		return sessionTimelineLoadedMsg{Timeline: timeline}
	}
}

func loadStartupSessionTimelineCmd(ctx context.Context, client startupSessionLoader, rememberedID string) tea.Cmd {
	if client == nil {
		return nil
	}

	if ctx == nil {
		ctx = context.Background()
	}

	return func() tea.Msg {
		sessionID := getStartupSessionID(ctx, client, rememberedID)
		if err := client.Use(ctx, sessionID); err != nil && sessionID != defaultSessionID {
			sessionID = defaultSessionID
			_ = client.Use(ctx, sessionID)
		}

		timeline, err := client.Timeline(ctx, rpcclient.SessionTimelineOptions{SessionID: sessionID})
		if err != nil && sessionID != defaultSessionID {
			sessionID = defaultSessionID
			_ = client.Use(ctx, sessionID)
			timeline, err = client.Timeline(ctx, rpcclient.SessionTimelineOptions{
				SessionID: sessionID,
			})
		}
		if err != nil {
			return sessionTimelineLoadFailedMsg{Err: err}
		}

		return sessionTimelineLoadedMsg{Timeline: timeline}
	}
}

func (m model) loadStartupSessionTimeline() tea.Cmd {
	client, ok := m.sessionClient.(startupSessionLoader)
	if !ok {
		return m.runEffect(loadSessionTimelineEffect{})
	}

	rememberedID, err := loadLastSessionID()
	if err != nil {
		return tea.Batch(
			m.setStatus("last session unavailable"),
			loadStartupSessionTimelineCmd(m.chatCtx, client, defaultSessionID),
		)
	}

	return loadStartupSessionTimelineCmd(m.chatCtx, client, rememberedID)
}

func getStartupSessionID(ctx context.Context, client startupSessionLoader, rememberedID string) string {
	sessions, err := client.List(ctx)
	if err != nil {
		return defaultSessionID
	}

	currentSession, err := client.Current(ctx)
	if err == nil {
		if sessionID := getKnownStartupSessionID(sessions, currentSession.ID); sessionID != "" {
			return sessionID
		}
	}

	if sessionID := getKnownStartupSessionID(sessions, rememberedID); sessionID != "" {
		return sessionID
	}

	return defaultSessionID
}

func getKnownStartupSessionID(sessions []storage.Session, id string) string {
	idValue := str.String(id)
	id = idValue.Trim()
	if id == "" {
		return ""
	}
	if id == defaultSessionID {
		return defaultSessionID
	}

	for _, session := range sessions {
		iDValue := str.String(session.ID)
		if iDValue.Trim() == id {
			return id
		}
	}

	return ""
}

func loadSessionTitleCmd(ctx context.Context, client sessionTitleLoader) tea.Cmd {
	if client == nil {
		return nil
	}

	return func() tea.Msg {
		session, err := client.Current(ctx)
		if err != nil {
			return sessionTitleLoadFailedMsg{}
		}

		return sessionTitleLoadedMsg{Session: session}
	}
}

func loadOlderSessionTimelineCmd(
	ctx context.Context,
	client sessionTimelineLoader,
	timeline rpcclient.SessionTimeline,
) tea.Cmd {
	if client == nil {
		return nil
	}

	if ctx == nil {
		ctx = context.Background()
	}

	options, ok := getOlderSessionTimelineOptions(timeline)
	if !ok {
		return nil
	}

	return func() tea.Msg {
		page, err := client.Timeline(ctx, options)
		if err != nil {
			return olderSessionTimelineLoadFailedMsg{SessionID: timeline.SessionID}
		}

		return olderSessionTimelineLoadedMsg{
			Timeline: page,
			Options:  options,
		}
	}
}

func getOlderSessionTimelineOptions(
	timeline rpcclient.SessionTimeline,
) (rpcclient.SessionTimelineOptions, bool) {
	if timeline.SessionID == "" || (!timeline.MessagesHasMore && !timeline.TracesHasMore) {
		return rpcclient.SessionTimelineOptions{}, false
	}

	options := rpcclient.SessionTimelineOptions{SessionID: timeline.SessionID}
	if timeline.MessagesHasMore && len(timeline.Messages) > 0 {
		firstOffset := timeline.Messages[0].Offset
		options.MessageOffset = max(firstOffset-sessionTimelineMessagePageSize, 0)
		options.MessageLimit = firstOffset - options.MessageOffset
	} else {
		options.MessageOffset = getTimelineNextMessageOffset(timeline.Messages)
		options.MessageLimit = 1
	}

	if timeline.TracesHasMore && timeline.FirstTraceSequence > 1 {
		options.TraceOffset = max(timeline.FirstTraceSequence-sessionTimelineTracePageSize, 1)
		options.TraceLimit = timeline.FirstTraceSequence - options.TraceOffset
	} else {
		options.TraceOffset = max(timeline.LastTraceSequence+1, 1)
		options.TraceLimit = 1
	}

	return options, true
}

func getTimelineNextMessageOffset(messages []agentapi.SessionTimelineMessage) int {
	if len(messages) == 0 {
		return 0
	}

	return messages[len(messages)-1].Offset + 1
}

func (m *model) hydrateSessionTimeline(timeline rpcclient.SessionTimeline) tea.Cmd {
	cells := sessionTimelineToTranscriptCells(timeline)

	m.loadedTimeline = timeline
	m.timelineBaseCells = len(cells)
	m.timelinePageLoading = false
	m.applyAction(setSessionAction{
		ID:    timeline.SessionID,
		Title: getSessionTimelineDisplayName(timeline),
	})
	m.applyAction(setTranscriptCellsAction{Cells: cells})
	m.applyAction(setLiveTranscriptCellAction{})
	m.showIntro = false
	m.stream.Reset()
	m.setTranscriptContent()
	m.setDefaultStatus(defaultStatus)
	m.resize()

	cmd := loadSessionContextCmd(m.chatCtx, m.contextLoader, m.getCurrentSessionID())
	if err := saveLastSessionID(m.getCurrentSessionID()); err != nil {
		return tea.Batch(m.setStatus("last session unavailable"), cmd)
	}

	return cmd
}

func (m *model) loadOlderTimelinePageIfNeeded() tea.Cmd {
	if m.timelinePageLoading || m.selection.active || !m.isNearLoadedTranscriptStart() {
		return nil
	}

	cmd := loadOlderSessionTimelineCmd(m.chatCtx, m.timeline, m.loadedTimeline)
	if cmd == nil {
		return nil
	}

	m.timelinePageLoading = true

	return cmd
}

func (m model) isNearLoadedTranscriptStart() bool {
	return m.transcriptWindow.startBlock == 0 &&
		m.transcriptWindow.startLine == 0 &&
		m.transcript.YOffset() <= max(m.transcript.Height(), 1)
}

func (m *model) prependOlderSessionTimeline(
	page rpcclient.SessionTimeline,
	options rpcclient.SessionTimelineOptions,
) {
	m.timelinePageLoading = false
	if page.SessionID != m.getCurrentSessionID() ||
		page.SessionID != m.loadedTimeline.SessionID {
		return
	}

	oldTop := m.getTranscriptAbsoluteTopLine()
	oldLineCount := m.getTranscriptRenderedLineCount()
	localSuffix := cloneTranscriptCells(m.messages[min(m.timelineBaseCells, len(m.messages)):])
	m.loadedTimeline = mergeSessionTimelines(m.loadedTimeline, page, options)
	baseCells := sessionTimelineToTranscriptCells(m.loadedTimeline)
	cells := make([]transcriptCell, 0, len(baseCells)+len(localSuffix))
	cells = append(cells, baseCells...)
	cells = append(cells, localSuffix...)
	m.timelineBaseCells = len(baseCells)
	m.applyAction(setTranscriptCellsAction{Cells: cells})

	addedLines := max(m.getTranscriptRenderedLineCount()-oldLineCount, 0)
	m.renderTranscriptWindowAtAbsoluteLine(oldTop + addedLines)
}

func mergeSessionTimelines(
	current rpcclient.SessionTimeline,
	page rpcclient.SessionTimeline,
	options rpcclient.SessionTimelineOptions,
) rpcclient.SessionTimeline {
	merged := current
	merged.Messages = mergeTimelineMessages(current.Messages, page.Messages)
	merged.TraceEvents = mergeTimelineTraceEvents(current.TraceEvents, page.TraceEvents)
	merged.MessagesHasMore = len(merged.Messages) > 0 && merged.Messages[0].Offset > 0
	merged.FirstTraceSequence = getTimelineFirstTraceSequence(merged.TraceEvents)
	merged.LastTraceSequence = getTimelineLastTraceSequence(merged.TraceEvents)
	merged.TracesHasMore = hasOlderTimelineTraces(current, page, options)
	merged.TracesTruncatedBefore = page.TracesTruncatedBefore

	return merged
}

func mergeTimelineMessages(
	current []agentapi.SessionTimelineMessage,
	page []agentapi.SessionTimelineMessage,
) []agentapi.SessionTimelineMessage {
	byOffset := make(map[int]agentapi.SessionTimelineMessage, len(current)+len(page))
	for _, message := range current {
		byOffset[message.Offset] = message
	}
	for _, message := range page {
		byOffset[message.Offset] = message
	}

	messages := make([]agentapi.SessionTimelineMessage, 0, len(byOffset))
	for _, message := range byOffset {
		messages = append(messages, message)
	}
	sort.SliceStable(messages, func(left, right int) bool {
		return messages[left].Offset < messages[right].Offset
	})

	return messages
}

func mergeTimelineTraceEvents(
	current []agentapi.SessionTimelineTraceEvent,
	page []agentapi.SessionTimelineTraceEvent,
) []agentapi.SessionTimelineTraceEvent {
	bySequence := make(
		map[int]agentapi.SessionTimelineTraceEvent,
		len(current)+len(page),
	)
	for _, event := range current {
		bySequence[event.Event.Sequence] = event
	}
	for _, event := range page {
		bySequence[event.Event.Sequence] = event
	}

	events := make([]agentapi.SessionTimelineTraceEvent, 0, len(bySequence))
	for _, event := range bySequence {
		events = append(events, event)
	}
	sort.SliceStable(events, func(left, right int) bool {
		return events[left].Event.Sequence < events[right].Event.Sequence
	})

	return events
}

func hasOlderTimelineTraces(
	current rpcclient.SessionTimeline,
	page rpcclient.SessionTimeline,
	options rpcclient.SessionTimelineOptions,
) bool {
	if !current.TracesHasMore || options.TraceLimit <= 1 || len(page.TraceEvents) == 0 {
		return false
	}

	firstSequence := page.TraceEvents[0].Event.Sequence

	return options.TraceOffset > 1 && firstSequence <= options.TraceOffset
}

func getTimelineFirstTraceSequence(events []agentapi.SessionTimelineTraceEvent) int {
	if len(events) == 0 {
		return 0
	}

	return int(events[0].Event.Sequence)
}

func getTimelineLastTraceSequence(events []agentapi.SessionTimelineTraceEvent) int {
	if len(events) == 0 {
		return 0
	}

	return int(events[len(events)-1].Event.Sequence)
}

func (m *model) refreshSessionTitleFromSession(session storage.Session) {
	m.applyAction(setSessionAction{
		ID:    session.ID,
		Title: getSessionDisplayName(session),
	})
	m.refreshTranscriptContentAfterResize()
}

func getSessionDisplayName(session storage.Session) string {
	titleValue := str.String(session.Title)
	title := titleValue.Trim()
	iDValue2 := str.String(session.ID)
	sessionID := iDValue2.Trim()
	if title != "" {
		if sessionID == storage.DefaultSessionID {
			return fmt.Sprintf("%s (%s)", title, sessionID)
		}

		return title
	}
	if sessionID != "" {
		return sessionID
	}

	return "session"
}

func getSessionTimelineDisplayName(timeline rpcclient.SessionTimeline) string {
	return getSessionDisplayName(storage.Session{
		ID:    timeline.SessionID,
		Title: timeline.Title,
	})
}

type transcriptTimelineEntry struct {
	at    time.Time
	order int
	cell  transcriptCell
}

func (entry transcriptTimelineEntry) less(other transcriptTimelineEntry) bool {
	if entry.at.IsZero() || other.at.IsZero() {
		return !entry.at.IsZero() && other.at.IsZero()
	}
	if entry.at.Equal(other.at) {
		return entry.order < other.order
	}

	return entry.at.Before(other.at)
}

func isMessageBackedTimelineEvent(msg any) bool {
	switch msg.(type) {
	case userMessageAcceptedMsg, assistantResponseCompletedMsg:
		return true
	default:
		return false
	}
}

func sessionTimelineToTranscriptCells(timeline rpcclient.SessionTimeline) []transcriptCell {
	entries := make([]transcriptTimelineEntry, 0, len(timeline.Messages)+len(timeline.TraceEvents))
	toolCalls := getTimelineToolCallDetails(timeline.Messages)
	responseStartedAt := time.Time{}
	for index, message := range timeline.Messages {
		messageCell := timelineMessageToTranscriptCell(message.Message, toolCalls)
		if message.Message.Role == morphmsg.RoleUser {
			responseStartedAt = message.Message.CreatedAt
		}
		if cell, ok := messageCell.(assistantTranscriptCell); ok && !responseStartedAt.IsZero() {
			cell.duration = normalizeResponseDuration(message.Message.CreatedAt.Sub(responseStartedAt))
			messageCell = cell
			responseStartedAt = time.Time{}
		}
		if messageCell != nil && !messageCell.IsEmpty() {
			entries = append(entries, transcriptTimelineEntry{
				at:    message.Message.CreatedAt,
				order: index * 2,
				cell:  messageCell,
			})
		}
	}
	hasMessageTranscript := len(entries) > 0
	for index, event := range timeline.TraceEvents {
		traceEvent := trace.Event{
			Type:      event.Event.Type,
			Timestamp: event.Event.Timestamp,
			Payload:   event.Event.Payload,
		}
		if msg, ok := traceEventToTUIMessage(traceEvent); ok {
			if hasMessageTranscript && isMessageBackedTimelineEvent(msg) {
				continue
			}
			if reasoning, ok := msg.(reasoningCompletedMsg); ok {
				reasoning.ID = fmt.Sprintf("trace-%d", event.Event.Sequence)
				msg = reasoning
			}

			if cell := tuiMessageToTranscriptCell(msg); cell != nil && !cell.IsEmpty() {
				entries = append(entries, transcriptTimelineEntry{
					at:    event.Event.Timestamp,
					order: index*2 + 1,
					cell:  cell,
				})
			}
		}
	}

	sort.SliceStable(entries, func(left int, right int) bool {
		return entries[left].less(entries[right])
	})

	cells := make([]transcriptCell, 0, len(entries))
	for _, entry := range entries {
		cells = append(cells, entry.cell)
	}

	return cells
}

func timelineMessageToTranscriptCell(message morphmsg.Message, toolCalls map[string]timelineToolCallDetail) transcriptCell {
	return defaultTranscriptCellFactory.FromTimelineMessage(message, toolCalls)
}

type timelineToolCallDetail struct {
	detail       string
	planState    *trace.PlanToolState
	processState *trace.ProcessToolState
	startedAt    time.Time
}

func getTimelineToolCallDetails(messages []agentapi.SessionTimelineMessage) map[string]timelineToolCallDetail {
	details := map[string]timelineToolCallDetail{}
	for _, message := range messages {
		for _, toolCall := range message.Message.ToolCalls {
			iDValue3 := str.String(toolCall.ID)
			id := iDValue3.Trim()
			if id == "" {
				continue
			}
			startedMsg, _ := toolInvocationStartedMsgFromMessageToolCall(
				toolCall,
				message.Message.CreatedAt,
			)
			details[id] = timelineToolCallDetail{
				detail:       startedMsg.Detail,
				planState:    startedMsg.PlanState,
				processState: startedMsg.ProcessState,
				startedAt:    startedMsg.StartedAt,
			}
		}
	}

	return details
}

func tuiMessageToTranscriptCell(msg any) transcriptCell {
	return defaultTranscriptCellFactory.FromTUIMessage(msg)
}
