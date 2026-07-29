package tui

import (
	"context"
	"fmt"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"

	agentapi "github.com/wandxy/morph/internal/agent"
	rpcclient "github.com/wandxy/morph/internal/rpc/client"
	storage "github.com/wandxy/morph/internal/state/core"
	"github.com/wandxy/morph/internal/trace"
	tuirpc "github.com/wandxy/morph/internal/tui/rpc"
	morphmsg "github.com/wandxy/morph/pkg/agent/message"
	"github.com/wandxy/morph/pkg/str"
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

func (m *model) hydrateSessionTimeline(timeline rpcclient.SessionTimeline) tea.Cmd {
	cells := sessionTimelineToTranscriptCells(timeline)

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
