package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	agentapi "github.com/wandxy/morph/internal/agent"
	browserdomain "github.com/wandxy/morph/internal/browser"
	"github.com/wandxy/morph/internal/rpc/client"
	storage "github.com/wandxy/morph/internal/state/core"
	"github.com/wandxy/morph/internal/trace"
	morphmsg "github.com/wandxy/morph/pkg/agent/message"
	agentsession "github.com/wandxy/morph/pkg/agent/session"
	"github.com/wandxy/morph/pkg/str"
)

func TestBrowserTranscriptActionTitles_CoverEverySupportedAction(t *testing.T) {
	for _, action := range browserdomain.SupportedActions() {
		titles, ok := browserTranscriptActionTitles[string(action)]
		require.True(t, ok, "missing browser transcript title for %s", action)
		require.NotEmpty(t, titles.label)
		require.NotEmpty(t, titles.pending)
		require.NotEmpty(t, titles.completed)
		require.NotEmpty(t, titles.failed)
		require.NotEmpty(t, titles.interrupted)

		details := []toolTranscriptDetail{{text: string(action)}}
		require.Equal(t, titles.pending, getBrowserToolTranscriptTitle(details, false, false, false))
		require.Equal(t, titles.completed, getBrowserToolTranscriptTitle(details, true, false, false))
		require.Equal(t, titles.failed, getBrowserToolTranscriptTitle(details, false, true, false))
		require.Equal(t, titles.interrupted, getBrowserToolTranscriptTitle(details, false, false, true))
	}
}

func transcriptCellPlainText(cell transcriptCell) string {
	if cell == nil || cell.IsEmpty() {
		return ""
	}

	return cell.PlainText()
}

func transcriptCellPlainTexts(cells []transcriptCell) []string {
	values := make([]string, 0, len(cells))
	for _, cell := range cells {
		if value := transcriptCellPlainText(cell); value != "" {
			values = append(values, value)
		}
	}

	return values
}

func TestRenderErrorTranscriptCell_FillsWidth(t *testing.T) {
	rendered := renderErrorTranscriptCell(
		`rpc error: code = Unavailable desc = connection error: desc = "transport: Error while dialing: dial tcp 127.0.0.1:50051: connect: connection refused"`,
		80,
	)

	for _, line := range strings.Split(rendered, "\n") {
		require.Equal(t, 80, lipgloss.Width(stripANSI(line)), line)
	}
}

func renderTranscriptTestCellWithWidth(cell transcriptCell, width int) string {
	if cell == nil || cell.IsEmpty() {
		return ""
	}
	if toolCell, ok := cell.(toolTranscriptCell); ok {
		group := toolTranscriptGroup{action: toolCell.action}
		group.add(toolCell)
		return renderToolTranscriptGroup(group, 0)
	}

	return defaultTranscriptRenderer.RenderCell(cell, transcriptRenderContext{Width: width, Now: currentTime()})
}

func renderTranscriptTestCell(cell transcriptCell) string {
	return renderTranscriptTestCellWithWidth(cell, defaultWidth)
}

func toolTranscriptTestCell(id string, name string, detail string, completed ...bool) transcriptCell {
	isCompleted := len(completed) > 0 && completed[0]

	return newToolTranscriptCell(id, name, detail, nil, nil, time.Time{}, time.Time{}, isCompleted)
}

func TestRenderToolTranscriptGroup_RendersTerminalRunStates(t *testing.T) {
	startedAt := time.Date(2026, 7, 11, 18, 0, 0, 0, time.UTC)
	terminalAt := startedAt.Add(2 * time.Second)

	for _, test := range []struct {
		name   string
		status toolTranscriptTerminalStatus
		want   string
	}{
		{name: "failed", status: toolTranscriptTerminalStatusFailed, want: "Failed 1 shell command"},
		{name: "interrupted", status: toolTranscriptTerminalStatusInterrupted, want: "Interrupted 1 shell command"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cell := toolTranscriptCell{
				id:             "call_1",
				action:         "Run",
				detail:         "echo hello",
				startedAt:      startedAt,
				completedAt:    terminalAt,
				terminalStatus: test.status,
			}
			group := toolTranscriptGroup{action: "Run"}
			group.add(cell)

			rendered := stripANSI(renderToolTranscriptGroupWithContext(
				group,
				transcriptRenderContext{Width: 80, Now: terminalAt},
			))

			require.Contains(t, rendered, test.want)
			require.Contains(t, rendered, "$ echo hello (2s)")
		})
	}
}

func TestRenderToolTranscriptGroup_RendersCompletedWithIssues(t *testing.T) {
	group := toolTranscriptGroup{action: "Run"}
	group.add(toolTranscriptCell{
		id:             "call_1",
		action:         "Run",
		detail:         "false",
		terminalStatus: toolTranscriptTerminalStatusFailed,
		failure:        "exit status 1",
	})
	group.add(toolTranscriptCell{
		id:        "call_2",
		action:    "Run",
		detail:    "true",
		completed: true,
	})

	rendered := stripANSI(renderToolTranscriptGroup(group, 0))

	require.Contains(t, rendered, "Ran 2 shell commands with issues")
	require.NotContains(t, rendered, "Failed 2 shell commands")
	require.False(t, hasRunningToolTranscriptCell([]transcriptCell{
		toolTranscriptCell{
			id:             "call_1",
			action:         "Run",
			detail:         "false",
			terminalStatus: toolTranscriptTerminalStatusFailed,
		},
		toolTranscriptCell{id: "call_2", action: "Run", detail: "true", completed: true},
	}))
}

func toolTranscriptTestCellWithTiming(
	id string,
	name string,
	detail string,
	startedAt time.Time,
	completedAt time.Time,
	completed bool,
) transcriptCell {
	return newToolTranscriptCell(id, name, detail, nil, nil, startedAt, completedAt, completed)
}

func toolTranscriptTestCellWithPlanState(
	id string,
	name string,
	planState *trace.PlanToolState,
	startedAt time.Time,
	completedAt time.Time,
	completed bool,
) transcriptCell {
	return newToolTranscriptCell(id, name, "", planState, nil, startedAt, completedAt, completed)
}

func toolTranscriptTestCellWithProcessState(
	id string,
	name string,
	processState *trace.ProcessToolState,
	startedAt time.Time,
	completedAt time.Time,
	completed bool,
) transcriptCell {
	return newToolTranscriptCell(id, name, "", nil, processState, startedAt, completedAt, completed)
}

func TestLoadSessionTimelineCmdHandlesNilClientAndNilContext(t *testing.T) {
	require.Nil(t, loadSessionTimelineCmd(context.Background(), nil, "session-a"))

	client := &fakeTUIChatClient{
		timeline: client.SessionTimeline{SessionID: "session-a"},
	}

	var nilContext context.Context
	cmd := loadSessionTimelineCmd(nilContext, client, " session-a ")

	require.NotNil(t, cmd)
	require.Equal(t, sessionTimelineLoadedMsg{Timeline: client.timeline}, cmd())
	require.Equal(t, "session-a", client.timelineSessionID)
}

func TestLoadOlderSessionTimelineCmdRequestsPreviousMessageAndTracePages(t *testing.T) {
	page := client.SessionTimeline{SessionID: "session-a"}
	fakeClient := &fakeTUIChatClient{timeline: page}
	timeline := client.SessionTimeline{
		SessionID: "session-a",
		Messages: []agentapi.SessionTimelineMessage{
			{Offset: 100},
			{Offset: 199},
		},
		TraceEvents: []agentapi.SessionTimelineTraceEvent{
			{Event: agentsession.TraceEvent{Sequence: 501}},
			{Event: agentsession.TraceEvent{Sequence: 600}},
		},
		MessagesHasMore:    true,
		TracesHasMore:      true,
		FirstTraceSequence: 501,
		LastTraceSequence:  600,
	}

	cmd := loadOlderSessionTimelineCmd(context.Background(), fakeClient, timeline)

	require.NotNil(t, cmd)
	require.Equal(t, olderSessionTimelineLoadedMsg{
		Timeline: page,
		Options: client.SessionTimelineOptions{
			SessionID:     "session-a",
			MessageOffset: 0,
			MessageLimit:  100,
			TraceOffset:   1,
			TraceLimit:    500,
		},
	}, cmd())
	require.Equal(t, []client.SessionTimelineOptions{{
		SessionID:     "session-a",
		MessageOffset: 0,
		MessageLimit:  100,
		TraceOffset:   1,
		TraceLimit:    500,
	}}, fakeClient.timelineOptions)
}

func TestModel_PrependsOlderTimelineWithoutMovingViewportOrDroppingLiveCells(t *testing.T) {
	runModel := newTranscriptWindowTestModel(80, 12)
	runModel.sessionID = "session-a"
	runModel.loadedTimeline = timelineMessagePage(10, 10)
	baseCells := sessionTimelineToTranscriptCells(runModel.loadedTimeline)
	runModel.timelineBaseCells = len(baseCells)
	runModel.messages = append(baseCells, assistantTranscriptCell{text: "live suffix"})
	runModel.renderTranscriptWindowIntoViewport(transcriptWindowHead)
	runModel.transcript.SetYOffset(len(runModel.getTranscriptHeaderLines()))
	visibleBefore := firstNonemptyTranscriptLine(runModel.transcript.View())
	require.Contains(t, visibleBefore, "history-0010")
	page := timelineMessagePage(0, 10)

	runModel.prependOlderSessionTimeline(page, client.SessionTimelineOptions{
		SessionID:     "session-a",
		MessageOffset: 0,
		MessageLimit:  10,
	})

	require.Equal(t, visibleBefore, firstNonemptyTranscriptLine(runModel.transcript.View()))
	require.Len(t, runModel.loadedTimeline.Messages, 20)
	require.Len(t, runModel.messages, 21)
	require.Equal(t, "Morph: live suffix", transcriptCellPlainText(runModel.messages[20]))
	require.False(t, runModel.loadedTimeline.MessagesHasMore)
}

func TestModel_LoadsOlderTimelineOnlyOnceWhileRequestIsPending(t *testing.T) {
	fakeClient := &fakeTUIChatClient{timeline: timelineMessagePage(0, 10)}
	runModel := newTranscriptWindowTestModel(80, 12)
	runModel.sessionID = "session-a"
	runModel.timeline = fakeClient
	runModel.loadedTimeline = timelineMessagePage(10, 10)
	runModel.timelineBaseCells = 10
	runModel.messages = sessionTimelineToTranscriptCells(runModel.loadedTimeline)
	runModel.renderTranscriptWindowIntoViewport(transcriptWindowHead)
	runModel.transcript.GotoTop()

	cmd := runModel.loadOlderTimelinePageIfNeeded()

	require.NotNil(t, cmd)
	require.True(t, runModel.timelinePageLoading)
	require.Nil(t, runModel.loadOlderTimelinePageIfNeeded())
	msg, ok := cmd().(olderSessionTimelineLoadedMsg)
	require.True(t, ok)
	updated, updateCmd := runModel.Update(msg)
	require.Nil(t, updateCmd)
	runModel = updated.(model)
	require.False(t, runModel.timelinePageLoading)
	require.Len(t, runModel.loadedTimeline.Messages, 20)
}

func TestMergeSessionTimelinesKeepsPagingTowardOldestTraceSequence(t *testing.T) {
	current := client.SessionTimeline{
		SessionID: "session-a",
		TraceEvents: []agentapi.SessionTimelineTraceEvent{
			{Event: agentsession.TraceEvent{Sequence: 1_001}},
			{Event: agentsession.TraceEvent{Sequence: 1_002}},
		},
		TracesHasMore:      true,
		FirstTraceSequence: 1_001,
		LastTraceSequence:  1_002,
	}
	page := client.SessionTimeline{
		SessionID: "session-a",
		TraceEvents: []agentapi.SessionTimelineTraceEvent{
			{Event: agentsession.TraceEvent{Sequence: 501}},
			{Event: agentsession.TraceEvent{Sequence: 1_001}},
		},
	}

	merged := mergeSessionTimelines(current, page, client.SessionTimelineOptions{
		SessionID:   "session-a",
		TraceOffset: 501,
		TraceLimit:  500,
	})

	require.Equal(t, []int{501, 1_001, 1_002}, []int{
		merged.TraceEvents[0].Event.Sequence,
		merged.TraceEvents[1].Event.Sequence,
		merged.TraceEvents[2].Event.Sequence,
	})
	require.Equal(t, 501, merged.FirstTraceSequence)
	require.Equal(t, 1_002, merged.LastTraceSequence)
	require.True(t, merged.TracesHasMore)

	merged = mergeSessionTimelines(merged, client.SessionTimeline{
		SessionID: "session-a",
		TraceEvents: []agentapi.SessionTimelineTraceEvent{
			{Event: agentsession.TraceEvent{Sequence: 1}},
			{Event: agentsession.TraceEvent{Sequence: 501}},
		},
	}, client.SessionTimelineOptions{
		SessionID:   "session-a",
		TraceOffset: 1,
		TraceLimit:  500,
	})

	require.Equal(t, 1, merged.FirstTraceSequence)
	require.False(t, merged.TracesHasMore)
}

func timelineMessagePage(offset, count int) client.SessionTimeline {
	messages := make([]agentapi.SessionTimelineMessage, count)
	for index := range messages {
		messageOffset := offset + index
		messages[index] = agentapi.SessionTimelineMessage{
			Offset: messageOffset,
			Message: morphmsg.Message{
				Role:      morphmsg.RoleAssistant,
				Content:   fmt.Sprintf("history-%04d", messageOffset),
				CreatedAt: time.Unix(int64(messageOffset), 0),
			},
		}
	}

	return client.SessionTimeline{
		SessionID:       "session-a",
		Messages:        messages,
		MessagesHasMore: offset > 0,
	}
}

func TestLoadStartupSessionTimelineCmdHandlesNilClientAndNilContext(t *testing.T) {
	require.Nil(t, loadStartupSessionTimelineCmd(context.Background(), nil, "session-a"))

	client := &fakeTUIChatClient{
		sessions: []storage.Session{{ID: "session-a"}},
		timeline: client.SessionTimeline{SessionID: "session-a"},
	}

	var nilContext context.Context
	cmd := loadStartupSessionTimelineCmd(nilContext, client, "session-a")

	require.NotNil(t, cmd)
	require.Equal(t, sessionTimelineLoadedMsg{Timeline: client.timeline}, cmd())
	require.Equal(t, "session-a", client.usedSessionID)
	require.Equal(t, "session-a", client.timelineSessionID)
}

func TestLoadStartupSessionTimelineCmdPrefersCurrentSession(t *testing.T) {
	client := &fakeTUIChatClient{
		sessions: []storage.Session{
			{ID: "session-remembered"},
			{ID: "session-current"},
		},
		currentSession: storage.Session{ID: "session-current"},
		timeline:       client.SessionTimeline{SessionID: "session-current"},
	}

	cmd := loadStartupSessionTimelineCmd(context.Background(), client, "session-remembered")

	require.NotNil(t, cmd)
	require.Equal(t, sessionTimelineLoadedMsg{Timeline: client.timeline}, cmd())
	require.Equal(t, "session-current", client.usedSessionID)
	require.Equal(t, "session-current", client.timelineSessionID)
	require.Equal(t, 1, client.currentSessionCalls)
}

func TestLoadStartupSessionTimelineCmdFallsBackToRememberedWhenCurrentSessionIsInactive(t *testing.T) {
	client := &fakeTUIChatClient{
		sessions: []storage.Session{
			{ID: "session-remembered"},
		},
		currentSession: storage.Session{ID: "session-archived"},
		timeline:       client.SessionTimeline{SessionID: "session-remembered"},
	}

	cmd := loadStartupSessionTimelineCmd(context.Background(), client, "session-remembered")

	require.NotNil(t, cmd)
	require.Equal(t, sessionTimelineLoadedMsg{Timeline: client.timeline}, cmd())
	require.Equal(t, "session-remembered", client.usedSessionID)
	require.Equal(t, "session-remembered", client.timelineSessionID)
}

func TestLoadStartupSessionTimelineCmdFallsBackToDefaultWhenCurrentAndRememberedAreInactive(t *testing.T) {
	client := &fakeTUIChatClient{
		sessions:       []storage.Session{{ID: defaultSessionID}},
		currentSession: storage.Session{ID: "session-current-archived"},
		timeline:       client.SessionTimeline{SessionID: defaultSessionID},
	}

	cmd := loadStartupSessionTimelineCmd(context.Background(), client, "session-remembered-archived")

	require.NotNil(t, cmd)
	require.Equal(t, sessionTimelineLoadedMsg{Timeline: client.timeline}, cmd())
	require.Equal(t, defaultSessionID, client.usedSessionID)
	require.Equal(t, defaultSessionID, client.timelineSessionID)
}

func TestLoadStartupSessionTimelineCmdFallsBackWhenUseSessionFails(t *testing.T) {
	client := &fakeTUIChatClient{
		sessions:      []storage.Session{{ID: "session-a"}},
		useSessionErr: errors.New("use failed"),
		timeline:      client.SessionTimeline{SessionID: defaultSessionID},
	}

	cmd := loadStartupSessionTimelineCmd(context.Background(), client, "session-a")

	require.NotNil(t, cmd)
	require.Equal(t, sessionTimelineLoadedMsg{Timeline: client.timeline}, cmd())
	require.Equal(t, 2, client.useSessionCalls)
	require.Equal(t, defaultSessionID, client.usedSessionID)
	require.Equal(t, defaultSessionID, client.timelineSessionID)
}

func TestLoadStartupSessionTimelineCmdFallsBackWhenRememberedTimelineFails(t *testing.T) {
	client := &startupTimelineFallbackClient{
		sessions: []storage.Session{{ID: "session-a"}},
		timelines: map[string]client.SessionTimeline{
			defaultSessionID: {SessionID: defaultSessionID},
		},
		errors: map[string]error{
			"session-a": errors.New("timeline failed"),
		},
	}

	cmd := loadStartupSessionTimelineCmd(context.Background(), client, "session-a")

	require.NotNil(t, cmd)
	require.Equal(t, sessionTimelineLoadedMsg{Timeline: client.timelines[defaultSessionID]}, cmd())
	require.Equal(t, []string{"session-a", defaultSessionID}, client.usedSessionIDs)
	require.Equal(t, []string{"session-a", defaultSessionID}, client.timelineSessionIDs)
}

func TestLoadStartupSessionTimelineCmdReturnsFailureWhenDefaultTimelineFails(t *testing.T) {
	expected := errors.New("timeline failed")
	client := &fakeTUIChatClient{timelineErr: expected}

	cmd := loadStartupSessionTimelineCmd(context.Background(), client, defaultSessionID)

	require.NotNil(t, cmd)
	require.Equal(t, sessionTimelineLoadFailedMsg{Err: expected}, cmd())
	require.Equal(t, defaultSessionID, client.usedSessionID)
	require.Equal(t, defaultSessionID, client.timelineSessionID)
}

func TestModel_LoadStartupSessionTimelineFallsBackWhenRememberedStateIsUnreadable(t *testing.T) {
	home := t.TempDir()
	setActiveTestProfile(t, home)
	require.NoError(t, os.WriteFile(appTUIStatePath(), []byte("{"), 0o600))
	client := &fakeTUIChatClient{
		timeline: client.SessionTimeline{SessionID: defaultSessionID},
	}
	runModel := newModelWithClient(client)

	cmd := runModel.loadStartupSessionTimeline()

	require.NotNil(t, cmd)
	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	require.Len(t, batch, 2)

	msg, ok := batch[1]().(sessionTimelineLoadedMsg)
	require.True(t, ok)
	require.Equal(t, defaultSessionID, msg.Timeline.SessionID)
	require.Equal(t, defaultSessionID, client.usedSessionID)
}

func TestGetStartupSessionIDUsesDefaultForBlankDefaultAndListErrors(t *testing.T) {
	require.Equal(t, defaultSessionID, getStartupSessionID(
		context.Background(),
		&fakeTUIChatClient{sessions: []storage.Session{{ID: defaultSessionID}}},
		" ",
	))
	require.Equal(t, defaultSessionID, getStartupSessionID(context.Background(), &fakeTUIChatClient{}, defaultSessionID))
	require.Equal(t, defaultSessionID, getStartupSessionID(
		context.Background(),
		&fakeTUIChatClient{listSessionsErr: errors.New("list failed")},
		"session-a",
	))
}

func TestGetKnownStartupSessionIDMatchesOnlyKnownNonDefaultSessions(t *testing.T) {
	sessions := []storage.Session{{ID: "session-a"}}

	require.Empty(t, getKnownStartupSessionID(sessions, " "))
	require.Equal(t, defaultSessionID, getKnownStartupSessionID(nil, defaultSessionID))
	require.Equal(t, "session-a", getKnownStartupSessionID(sessions, " session-a "))
	require.Empty(t, getKnownStartupSessionID(sessions, "session-missing"))
}

func TestLoadSessionTitleCmdHandlesNilClientAndFailures(t *testing.T) {
	require.Nil(t, loadSessionTitleCmd(context.Background(), nil))

	client := &fakeTUIChatClient{currentSessionErr: errors.New("title failed")}

	cmd := loadSessionTitleCmd(context.Background(), client)

	require.NotNil(t, cmd)
	require.Equal(t, sessionTitleLoadFailedMsg{}, cmd())
	require.Equal(t, 1, client.currentSessionCalls)
}

func TestModel_HydrateSessionTimelineReportsLastSessionSaveFailure(t *testing.T) {
	runModel := newModel()
	homeFile, err := os.CreateTemp(t.TempDir(), "profile-home-*")
	require.NoError(t, err)
	require.NoError(t, homeFile.Close())
	setActiveTestProfile(t, homeFile.Name())

	cmd := runModel.hydrateSessionTimeline(client.SessionTimeline{SessionID: "session-a"})

	require.NotNil(t, cmd)
	require.Equal(t, "last session unavailable", runModel.status.Text())
}

func TestModel_HydrateSessionTimelineRendersCurrentSessionInChrome(t *testing.T) {
	runModel := newModel()
	runModel.width = 180
	runModel.resize()

	runModel.hydrateSessionTimeline(client.SessionTimeline{
		SessionID: "session-current",
		Title:     "Current Session",
		Messages: []agentapi.SessionTimelineMessage{{
			Message: morphmsg.Message{Role: morphmsg.RoleAssistant, Content: "loaded"},
		}},
	})

	content := stripANSI(runModel.transcript.GetContent())
	require.Contains(t, content, "session-current")
	require.NotContains(t, content, "session  default")
}

func TestModel_RefreshSessionTitleRendersCurrentSessionInChrome(t *testing.T) {
	runModel := newModel()
	runModel.width = 180
	runModel.resize()
	runModel.messages = []transcriptCell{assistantTranscriptCell{text: "loaded"}}
	runModel.setTranscriptContent()

	runModel.refreshSessionTitleFromSession(storage.Session{
		ID:    "session-current",
		Title: "Current Session",
	})

	content := stripANSI(runModel.transcript.GetContent())
	require.Contains(t, content, "session-current")
	require.NotContains(t, content, "session  default")
}

func TestTimelineMessageToTranscriptCell_MapsVisibleRoles(t *testing.T) {
	cases := []struct {
		name    string
		message morphmsg.Message
		want    string
	}{
		{
			name:    "user",
			message: morphmsg.Message{Role: morphmsg.RoleUser, Content: "hello"},
			want:    "You: hello",
		},
		{
			name:    "assistant",
			message: morphmsg.Message{Role: morphmsg.RoleAssistant, Content: "hi"},
			want:    "Morph: hi",
		},
		{
			name:    "tool",
			message: morphmsg.Message{Role: morphmsg.RoleTool, Name: "read_file", Content: "done"},
			want:    transcriptCellPlainText(toolTranscriptTestCell("", "read_file", "", true)),
		},
		{
			name:    "tool fallback",
			message: morphmsg.Message{Role: morphmsg.RoleTool, Content: "done"},
			want:    transcriptCellPlainText(toolTranscriptTestCell("", "tool", "", true)),
		},
		{
			name:    "unknown",
			message: morphmsg.Message{Role: "system", Content: "note"},
			want:    "system: note",
		},
		{
			name:    "empty content",
			message: morphmsg.Message{Role: morphmsg.RoleUser, Content: " "},
			want:    "",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, transcriptCellPlainText(timelineMessageToTranscriptCell(tt.message, nil)))
		})
	}
}

func TestGetTimelineToolCallDetailsIgnoresBlankToolCallIDs(t *testing.T) {
	details := getTimelineToolCallDetails([]agentapi.SessionTimelineMessage{{
		Message: morphmsg.Message{
			ToolCalls: []morphmsg.ToolCall{
				{ID: " ", Name: "read_file"},
				{ID: "call_1", Name: "read_file", Input: `{"path":"README.md"}`},
			},
		},
	}})

	require.Len(t, details, 1)
	require.Contains(t, details, "call_1")
}

type startupTimelineFallbackClient struct {
	sessions           []storage.Session
	currentSession     storage.Session
	timelines          map[string]client.SessionTimeline
	errors             map[string]error
	usedSessionIDs     []string
	timelineSessionIDs []string
}

func (c *startupTimelineFallbackClient) List(context.Context, ...client.SessionListOptions) ([]storage.Session, error) {
	return c.sessions, nil
}

func (c *startupTimelineFallbackClient) Current(context.Context) (storage.Session, error) {
	return c.currentSession, nil
}

func (c *startupTimelineFallbackClient) Use(_ context.Context, id string) error {
	c.usedSessionIDs = append(c.usedSessionIDs, id)
	return nil
}

func (c *startupTimelineFallbackClient) Timeline(
	_ context.Context,
	opts client.SessionTimelineOptions,
) (client.SessionTimeline, error) {
	c.timelineSessionIDs = append(c.timelineSessionIDs, opts.SessionID)
	if err := c.errors[opts.SessionID]; err != nil {
		return client.SessionTimeline{}, err
	}

	return c.timelines[opts.SessionID], nil
}

func TestTUIMessageToTranscriptCell_MapsLiveDisplayMessages(t *testing.T) {
	cases := []struct {
		name string
		msg  any
		want string
	}{
		{name: "user", msg: userMessageAcceptedMsg{Text: "hello"}, want: "You: hello"},
		{name: "assistant delta", msg: assistantTextDeltaMsg{Text: "hi"}, want: "Morph: hi"},
		{name: "reasoning delta", msg: assistantTextDeltaMsg{Channel: "reasoning", Text: "thinking"}, want: "Thinking: thinking"},
		{name: "assistant complete", msg: assistantResponseCompletedMsg{Text: "done"}, want: "Morph: done"},
		{name: "tool started", msg: toolInvocationStartedMsg{Name: "read_file"}, want: transcriptCellPlainText(toolTranscriptTestCell("", "read_file", ""))},
		{name: "tool completed", msg: toolInvocationCompletedMsg{Name: "read_file"}, want: transcriptCellPlainText(toolTranscriptTestCell("", "read_file", "", true))},
		{name: "safety", msg: safetyEventMsg{Action: "blocked", FindingIDs: []string{"prompt_exfiltration"}}, want: "Safety: blocked: prompt_exfiltration"},
		{name: "error", msg: sessionErrorMsg{Message: "failed"}, want: "Error: failed"},
		{
			name: "provider json error",
			msg: sessionErrorMsg{
				Message: `POST "https://api.anthropic.com/v1/messages": 400 Bad Request {"type":"error","error":{"type":"invalid_request_error","message":"additionalProperties must be false"}}`,
			},
			want: "Error: Model provider rejected the request: additionalProperties must be false",
		},
		{name: "empty", msg: userMessageAcceptedMsg{Text: " "}, want: ""},
		{name: "unknown", msg: struct{}{}, want: ""},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, transcriptCellPlainText(tuiMessageToTranscriptCell(tt.msg)))
		})
	}
}

func TestTranscriptCells_ExposeTypedCellContract(t *testing.T) {
	startedAt := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC)
	completedAt := startedAt.Add(2 * time.Second)
	cases := []struct {
		name       string
		cell       transcriptCell
		kind       transcriptCellKind
		plainText  string
		renderText string
	}{
		{
			name:       "user",
			cell:       userTranscriptCell{text: "hello"},
			kind:       transcriptCellUser,
			plainText:  "You: hello",
			renderText: "❯ hello",
		},
		{
			name:       "assistant",
			cell:       assistantTranscriptCell{text: "hi"},
			kind:       transcriptCellAssistant,
			plainText:  "Morph: hi",
			renderText: "hi",
		},
		{
			name:       "reasoning",
			cell:       thinkingTranscriptCell{summary: "thinking", startedAt: startedAt},
			kind:       transcriptCellThinking,
			plainText:  "Thinking: thinking",
			renderText: "Thinking",
		},
		{
			name:       "thought",
			cell:       thoughtTranscriptCell{duration: 3 * time.Second},
			kind:       transcriptCellThought,
			plainText:  "Thought: 3s",
			renderText: "Thought for 3s",
		},
		{
			name:       "safety",
			cell:       safetyTranscriptCell{action: "blocked", findingIDs: []string{"prompt_exfiltration"}},
			kind:       transcriptCellSafety,
			plainText:  "Safety: blocked: prompt_exfiltration",
			renderText: "Safety: blocked: prompt_exfiltration",
		},
		{
			name:       "error",
			cell:       errorTranscriptCell{message: "failed"},
			kind:       transcriptCellError,
			plainText:  "Error: failed",
			renderText: "Error",
		},
		{
			name:       "system",
			cell:       systemTranscriptCell{text: "note"},
			kind:       transcriptCellSystem,
			plainText:  "note",
			renderText: "note",
		},
		{
			name:       "compaction",
			cell:       manualCompactionTranscriptCell{state: manualCompactionState{Status: "succeeded"}},
			kind:       transcriptCellCompaction,
			plainText:  "Manual compaction completed",
			renderText: "● Manual Compaction",
		},
		{
			name:       "tool",
			cell:       newToolTranscriptCell("call_1", "list_files", "list_files(path=.)", nil, nil, startedAt, completedAt, true),
			kind:       transcriptCellTool,
			plainText:  "Tool List Files:\nid: call_1\ndetail: list_files(path=.)\nstarted_at: 2026-05-20T09:00:00Z\ncompleted_at: 2026-05-20T09:00:02Z\nstatus: completed",
			renderText: "List Files",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			require.NotNil(t, tt.cell)
			require.Equal(t, tt.kind, tt.cell.Kind())
			require.False(t, tt.cell.IsEmpty())
			require.Equal(t, tt.plainText, tt.cell.PlainText())
			rendered := defaultTranscriptRenderer.RenderCell(
				tt.cell,
				transcriptRenderContext{Width: 40, Now: completedAt},
			)
			require.Contains(t, stripANSI(rendered), tt.renderText)
			if tt.kind == transcriptCellError {
				require.Contains(t, stripANSI(rendered), "failed")
				require.NotContains(t, stripANSI(rendered), "╭")
				require.NotContains(t, stripANSI(rendered), "╰")
			}
			if tt.kind == transcriptCellCompaction {
				require.NotContains(t, stripANSI(rendered), strings.Repeat("─", 40))
			}
		})
	}
}

func TestTranscriptCells_ReportEmptyState(t *testing.T) {
	cases := []transcriptCell{
		userTranscriptCell{text: " "},
		assistantTranscriptCell{text: " "},
		thinkingTranscriptCell{summary: " "},
		thoughtTranscriptCell{},
		safetyTranscriptCell{},
		errorTranscriptCell{},
		systemTranscriptCell{text: " "},
	}

	for _, cell := range cases {
		require.True(t, cell.IsEmpty())
		require.Empty(t, cell.PlainText())
	}
}

func TestRenderTranscriptCell_StylesCanonicalCells(t *testing.T) {
	rendered := renderTranscriptCells([]transcriptCell{
		timelineMessageToTranscriptCell(morphmsg.Message{Role: morphmsg.RoleUser, Content: "hello"}, nil),
		tuiMessageToTranscriptCell(assistantResponseCompletedMsg{Text: "hi"}),
		tuiMessageToTranscriptCell(toolInvocationStartedMsg{Name: "read_file"}),
		tuiMessageToTranscriptCell(safetyEventMsg{Action: "blocked"}),
		tuiMessageToTranscriptCell(sessionErrorMsg{Message: "failed"}),
	})

	plain := stripANSI(rendered)
	require.Contains(t, plain, "❯ hello")
	require.NotContains(t, plain, "┌")
	require.NotContains(t, plain, "│ ❯ hello")
	require.NotContains(t, plain, "You: hello")
	require.Contains(t, plain, "hi")
	require.NotContains(t, plain, "Morph: hi")
	require.Contains(t, plain, "● Read")
	require.Contains(t, plain, "└ read_file")
	require.Contains(t, plain, "Safety: blocked")
	require.Contains(t, plain, "Error")
	require.Contains(t, plain, "failed")
	require.NotContains(t, plain, "╭")
	require.NotContains(t, plain, "╰")
	require.Contains(t, rendered, "\x1b[")
}

func TestRenderTranscriptCell_RendersReasoningDeltas(t *testing.T) {
	rendered := renderTranscriptTestCellWithWidth(
		thinkingTranscriptCell{summary: "first token\nsecond token"},
		40,
	)

	plain := stripANSI(rendered)
	require.Contains(t, plain, "Thinking")
	require.Contains(t, plain, "└ first token")
	require.Contains(t, plain, "  second token")
	require.NotContains(t, plain, "Reasoning:")
}

func TestRenderTranscriptCell_RemovesThinkingSummaryMarkdown(t *testing.T) {
	want := strings.Join([]string{
		"Designing idempotent execution constraints",
		"Defining concurrency-safe execution state machine",
		"Designing execution intent for safe retries",
	}, "\n")
	cell := thinkingTranscriptCell{
		summary: "**Designing idempotent execution constraints****Defining concurrency-safe execution state machine****Designing execution intent for safe retries**",
	}

	plain := stripANSI(renderTranscriptTestCellWithWidth(cell, 80))

	require.Equal(t, want, getThinkingSummaryDisplayText(cell.summary))
	require.Equal(t, want, getThinkingSummaryDisplayText(
		`**Designing idempotent execution constraints\*\*\*\*Defining concurrency-safe execution state machine\*\*\*\*Designing execution intent for safe retries**`,
	))
	require.Equal(t, "Designing idempotent execution constraints", getThinkingSummaryDisplayText(
		"**Designing idempotent execution constraints",
	))
	require.Contains(t, plain, "└ Designing idempotent execution constraints")
	require.Contains(t, plain, "  Defining concurrency-safe execution state machine")
	require.Contains(t, plain, "  Designing execution intent for safe retries")
	require.NotContains(t, plain, "*")
	require.NotContains(t, cell.PlainText(), "*")
	require.Contains(t, getThinkingSummaryDisplayText("Comparing O(n*m) with `claim_id`."), "O(n*m) with claim_id")
}

func TestRenderTranscriptCell_RendersCollapsedThought(t *testing.T) {
	rendered := renderTranscriptTestCellWithWidth(thoughtTranscriptCell{duration: 3 * time.Second}, 40)

	plain := stripANSI(rendered)
	require.Equal(t, "Thought for 3s", plain)
	require.NotContains(t, plain, "Thought:")
}

func TestRenderTranscriptCell_RendersFoldableThinkingSummary(t *testing.T) {
	cell := thinkingTranscriptCell{
		id:        "trace-7",
		summary:   "Checked the current state.",
		duration:  3 * time.Second,
		completed: true,
	}

	rendered := renderTranscriptTestCellWithWidth(cell, 60)
	plain := stripANSI(rendered)
	require.Contains(t, plain, "Thought for 3s")
	require.Contains(t, plain, "View thinking")
	require.NotContains(t, plain, cell.summary)
	require.Contains(t, rendered, "\x1b]8;;morph-thinking://trace-7\a")

	cell.expanded = true
	rendered = renderTranscriptTestCellWithWidth(cell, 60)
	plain = stripANSI(rendered)
	require.Contains(t, plain, "Hide thinking")
	require.Contains(t, plain, "└ Checked the current state.")
}

func TestRenderTranscriptCells_GroupsAdjacentToolOperationsByAction(t *testing.T) {
	rendered := renderTranscriptCells([]transcriptCell{
		toolTranscriptTestCell("call_1", "write_file", ""),
		toolTranscriptTestCell("call_2", "write_file", ""),
		toolTranscriptTestCell("call_3", "read_file", ""),
	})
	plain := stripANSI(rendered)

	require.Equal(t, 1, strings.Count(plain, "● Write"))
	require.Equal(t, 1, strings.Count(plain, "● Read"))
	require.Contains(t, plain, "├ write_file")
	require.Contains(t, plain, "└ write_file")
	require.Contains(t, plain, "└ read_file")
}

func TestRenderTranscriptCells_DeduplicatesStartedAndCompletedToolEvents(t *testing.T) {
	rendered := renderTranscriptCells([]transcriptCell{
		toolTranscriptTestCell("call_1", "web_search", `Search "what is todays news..."`),
		toolTranscriptTestCell("call_1", "web_search", `Search "what is todays news..."`, true),
	})
	plain := stripANSI(rendered)

	require.Equal(t, 1, strings.Count(plain, "● Searched"))
	require.Equal(t, 1, strings.Count(plain, `Search "what is todays news..."`))
	require.NotContains(t, plain, "web_search")
}

func TestRenderTranscriptCells_DeduplicatesToolEventsAcrossSafetyCells(t *testing.T) {
	rendered := renderTranscriptCells([]transcriptCell{
		toolTranscriptTestCell("call_1", "web_extract", ""),
		safetyTranscriptCell{action: "blocked", findingIDs: []string{"invisible_unicode"}},
		toolTranscriptTestCell("call_1", "web_extract", "", true),
	})
	plain := stripANSI(rendered)

	require.NotContains(t, plain, "Extracting from web")
	require.Equal(t, 1, strings.Count(plain, "Extraction finished"))
	require.Contains(t, plain, "Safety: blocked: invisible_unicode")
}

func TestRenderTranscriptCells_DoesNotDeduplicateToolEventsAcrossUserTurns(t *testing.T) {
	rendered := renderTranscriptCells([]transcriptCell{
		userTranscriptCell{text: "first"},
		toolTranscriptTestCell("call_1", "web_extract", ""),
		assistantTranscriptCell{text: "first done"},
		userTranscriptCell{text: "second"},
		toolTranscriptTestCell("call_1", "web_extract", "", true),
	})
	plain := stripANSI(rendered)

	require.Contains(t, plain, "Extracting from web")
	require.Contains(t, plain, "Extraction finished")
	require.Contains(t, plain, "first")
	require.Contains(t, plain, "second")
}

func TestRenderTranscriptCells_RendersMemorySearchLikeSearch(t *testing.T) {
	rendered := renderTranscriptCells([]transcriptCell{
		toolTranscriptTestCell("call_1", "memory_search", `Search "commit preferences"`),
		toolTranscriptTestCell("call_1", "memory_search", `Search "commit preferences"`, true),
	})
	plain := stripANSI(rendered)

	require.Contains(t, plain, "● Searched Memory")
	require.Contains(t, plain, `Search "commit preferences"`)
	require.NotContains(t, plain, "memory_search")
}

func TestRenderTranscriptCells_RendersRunningMemorySearchTitle(t *testing.T) {
	rendered := renderTranscriptCells([]transcriptCell{
		toolTranscriptTestCell("call_1", "memory_search", `Search "commit preferences"`),
	})
	plain := stripANSI(rendered)

	require.Contains(t, plain, "● Searching Memory")
	require.Contains(t, plain, `Search "commit preferences"`)
	require.NotContains(t, plain, "Memory Search")
}

func TestRenderTranscriptCells_RendersMemoryToolsWithFriendlyText(t *testing.T) {
	cases := []struct {
		name      string
		tool      string
		running   string
		completed string
		branch    string
	}{
		{
			name:      "extract",
			tool:      "memory_extract",
			running:   "Extracting Memory",
			completed: "Extracted Memory",
			branch:    "Extract memories",
		},
		{
			name:      "add",
			tool:      "memory_add",
			running:   "Adding Memory",
			completed: "Added Memory",
			branch:    "Add memory",
		},
		{
			name:      "update",
			tool:      "memory_update",
			running:   "Updating Memory",
			completed: "Updated Memory",
			branch:    "Update memory",
		},
		{
			name:      "delete",
			tool:      "memory_delete",
			running:   "Deleting Memory",
			completed: "Deleted Memory",
			branch:    "Delete memory",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			running := stripANSI(renderTranscriptCells([]transcriptCell{
				toolTranscriptTestCell("call_1", tt.tool, ""),
			}))
			completed := stripANSI(renderTranscriptCells([]transcriptCell{
				toolTranscriptTestCell("call_1", tt.tool, "", true),
			}))

			require.Contains(t, running, "● "+tt.running)
			require.Contains(t, running, "└ "+tt.branch)
			require.Contains(t, completed, "● "+tt.completed)
			require.Contains(t, completed, "└ "+tt.branch)
			require.NotContains(t, running, tt.tool)
			require.NotContains(t, completed, tt.tool)
		})
	}
}

func TestRenderTranscriptCells_RendersAutomationActionsWithFriendlyText(t *testing.T) {
	cases := []struct {
		name            string
		input           string
		running         string
		completed       string
		runningBranch   string
		completedBranch string
	}{
		{
			name:            "status",
			input:           `{"action":"status"}`,
			running:         "Checking Automation Status",
			completed:       "Checked Automation Status",
			runningBranch:   "Checking automation status",
			completedBranch: "Checked automation status",
		},
		{
			name:            "list",
			input:           `{"action":"list"}`,
			running:         "Listing Automations",
			completed:       "Listed Automations",
			runningBranch:   "Listing automations",
			completedBranch: "Listed automations",
		},
		{
			name:            "add",
			input:           `{"action":"add","job":{"name":"Nigeria time every 5 minutes"}}`,
			running:         "Adding Automation",
			completed:       "Added Automation",
			runningBranch:   "Adding automation Nigeria time every 5 minutes",
			completedBranch: "Added automation Nigeria time every 5 minutes",
		},
		{
			name:            "update",
			input:           `{"action":"update","id":"auto_job"}`,
			running:         "Updating Automation",
			completed:       "Updated Automation",
			runningBranch:   "Updating automation auto_job",
			completedBranch: "Updated automation auto_job",
		},
		{
			name:            "pause",
			input:           `{"action":"pause","id":"auto_job"}`,
			running:         "Pausing Automation",
			completed:       "Paused Automation",
			runningBranch:   "Pausing automation auto_job",
			completedBranch: "Paused automation auto_job",
		},
		{
			name:            "resume",
			input:           `{"action":"resume","id":"auto_job"}`,
			running:         "Resuming Automation",
			completed:       "Resumed Automation",
			runningBranch:   "Resuming automation auto_job",
			completedBranch: "Resumed automation auto_job",
		},
		{
			name:            "run",
			input:           `{"action":"run","id":"auto_job"}`,
			running:         "Running Automation",
			completed:       "Ran Automation",
			runningBranch:   "Running automation auto_job",
			completedBranch: "Ran automation auto_job",
		},
		{
			name:            "remove",
			input:           `{"action":"remove","id":"auto_job"}`,
			running:         "Removing Automation",
			completed:       "Removed Automation",
			runningBranch:   "Removing automation auto_job",
			completedBranch: "Removed automation auto_job",
		},
		{
			name:            "runs",
			input:           `{"action":"runs","run_query":{"job_id":"auto_job"}}`,
			running:         "Listing Automation Runs",
			completed:       "Listed Automation Runs",
			runningBranch:   "Listing runs for auto_job",
			completedBranch: "Listed runs for auto_job",
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			detail := getToolInputDisplayDetail("automation", test.input)
			running := stripANSI(renderTranscriptCells([]transcriptCell{
				toolTranscriptTestCell("call_1", "automation", detail),
			}))
			completed := stripANSI(renderTranscriptCells([]transcriptCell{
				toolTranscriptTestCell("call_1", "automation", detail, true),
			}))

			require.Contains(t, running, "● "+test.running)
			require.Contains(t, running, "└ "+test.runningBranch)
			require.Contains(t, completed, "● "+test.completed)
			require.Contains(t, completed, "└ "+test.completedBranch)
			require.NotContains(t, running, "└ automation")
			require.NotContains(t, completed, "└ automation")
		})
	}
}

func TestRenderTranscriptCells_RendersSessionMessagesWithFriendlyText(t *testing.T) {
	running := stripANSI(renderTranscriptCells([]transcriptCell{
		toolTranscriptTestCell("call_1", "session_messages", ""),
	}))
	completed := stripANSI(renderTranscriptCells([]transcriptCell{
		toolTranscriptTestCell("call_1", "session_messages", "", true),
	}))
	withDetail := stripANSI(renderTranscriptCells([]transcriptCell{
		toolTranscriptTestCell("call_1", "session_messages", "session_messages(offset_start=3 offset_end=9)", true),
	}))

	require.Contains(t, running, "● Fetching Session Messages")
	require.NotContains(t, running, "└")
	require.NotContains(t, running, "session_messages")
	require.Contains(t, completed, "● Fetched Session Messages")
	require.NotContains(t, completed, "└")
	require.NotContains(t, completed, "session_messages")
	require.Contains(t, withDetail, "● Fetched Session Messages")
	require.NotContains(t, withDetail, "└")
	require.NotContains(t, withDetail, "session_messages")
}

func TestBrowserToolDisplayDetail_RedactsURLPathAndDescribesLifecycle(t *testing.T) {
	detail := getToolInputDisplayDetail("browser", `{
		"action":"navigate",
		"session_id":"browser_1",
		"tab_id":"tab_1",
		"url":"https://example.com/private/path?token=secret"
	}`)

	require.Equal(t, "navigate:https://example.com", detail)
	require.Equal(
		t, "https://example.com",
		getToolBranchDisplayDetail("Browser", detail, false),
	)
	require.Equal(
		t, "https://example.com",
		getToolBranchDisplayDetail("Browser", detail, true),
	)
}

func TestBrowserToolDisplayDetail_DescribesEverySupportedAction(t *testing.T) {
	seen := make(map[browserdomain.Action]struct{})
	for _, test := range []struct {
		action browserdomain.Action
		input  string
		want   string
	}{
		{action: browserdomain.ActionStatus, input: `{"action":"status"}`, want: "status"},
		{action: browserdomain.ActionProfiles, input: `{"action":"profiles"}`, want: "profiles"},
		{action: browserdomain.ActionStart, input: `{"action":"start","profile":"default"}`, want: "start:Profile default"},
		{action: browserdomain.ActionStop, input: `{"action":"stop","session_id":"browser_1"}`, want: "stop:Session browser_1"},
		{action: browserdomain.ActionTabs, input: `{"action":"tabs","session_id":"browser_1"}`, want: "tabs:Session browser_1"},
		{action: browserdomain.ActionOpen, input: `{"action":"open","session_id":"browser_1","url":"https://example.com/private?token=secret"}`, want: "open:https://example.com"},
		{action: browserdomain.ActionFocus, input: `{"action":"focus","session_id":"browser_1","tab_id":"tab_1"}`, want: "focus:Tab tab_1"},
		{action: browserdomain.ActionClose, input: `{"action":"close","session_id":"browser_1","tab_id":"tab_1"}`, want: "close:Tab tab_1"},
		{action: browserdomain.ActionNavigate, input: `{"action":"navigate","session_id":"browser_1","tab_id":"tab_1","url":"https://example.com/private?token=secret"}`, want: "navigate:https://example.com"},
		{action: browserdomain.ActionReload, input: `{"action":"reload","session_id":"browser_1","tab_id":"tab_1"}`, want: "reload:Tab tab_1"},
		{action: browserdomain.ActionSnapshot, input: `{"action":"snapshot","session_id":"browser_1","tab_id":"tab_1"}`, want: "snapshot:Tab tab_1"},
		{action: browserdomain.ActionScreenshot, input: `{"action":"screenshot","session_id":"browser_1","tab_id":"tab_1","full_page":true}`, want: "screenshot:Full page · Tab tab_1"},
		{action: browserdomain.ActionPDF, input: `{"action":"pdf","session_id":"browser_1","tab_id":"tab_1"}`, want: "pdf:Tab tab_1"},
		{action: browserdomain.ActionConsole, input: `{"action":"console","session_id":"browser_1","tab_id":"tab_1","limit":20}`, want: "console:Tab tab_1"},
		{action: browserdomain.ActionClick, input: `{"action":"click","session_id":"browser_1","tab_id":"tab_1","ref":"g1e7"}`, want: "click:Element g1e7"},
		{action: browserdomain.ActionType, input: `{"action":"type","session_id":"browser_1","tab_id":"tab_1","ref":"g1e7","text":"secret text"}`, want: "type:Element g1e7"},
		{action: browserdomain.ActionPress, input: `{"action":"press","session_id":"browser_1","tab_id":"tab_1","key":"Enter"}`, want: "press:Tab tab_1"},
		{action: browserdomain.ActionScroll, input: `{"action":"scroll","session_id":"browser_1","tab_id":"tab_1","y":800}`, want: "scroll:Tab tab_1"},
		{action: browserdomain.ActionSelect, input: `{"action":"select","session_id":"browser_1","tab_id":"tab_1","ref":"g1e7","value":"secret option"}`, want: "select:Element g1e7"},
		{action: browserdomain.ActionUpload, input: `{"action":"upload","session_id":"browser_1","tab_id":"tab_1","ref":"g1e7","path":"/secret/file.txt"}`, want: "upload:Element g1e7"},
		{action: browserdomain.ActionDownload, input: `{"action":"download","session_id":"browser_1","tab_id":"tab_1","ref":"g1e7"}`, want: "download:Element g1e7"},
		{action: browserdomain.ActionExportArtifact, input: `{"action":"export_artifact","handle":"artifact_1","path":"/secret/saved.png"}`, want: "export_artifact"},
		{action: browserdomain.ActionAcceptDialog, input: `{"action":"accept_dialog","session_id":"browser_1","tab_id":"tab_1","ref":"dialog_1","text":"secret response"}`, want: "accept_dialog:Dialog dialog_1"},
		{action: browserdomain.ActionDismissDialog, input: `{"action":"dismiss_dialog","session_id":"browser_1","tab_id":"tab_1","ref":"dialog_1"}`, want: "dismiss_dialog:Dialog dialog_1"},
		{action: browserdomain.ActionWait, input: `{"action":"wait","session_id":"browser_1","tab_id":"tab_1","condition":"text","value":"secret text"}`, want: "wait:Text appears · Tab tab_1"},
		{action: browserdomain.ActionBack, input: `{"action":"back","session_id":"browser_1","tab_id":"tab_1"}`, want: "back:Tab tab_1"},
		{action: browserdomain.ActionForward, input: `{"action":"forward","session_id":"browser_1","tab_id":"tab_1"}`, want: "forward:Tab tab_1"},
	} {
		t.Run(string(test.action), func(t *testing.T) {
			detail := getToolInputDisplayDetail("browser", test.input)
			require.Equal(t, test.want, detail)
			for _, secret := range []string{"secret", "/private"} {
				require.NotContains(t, detail, secret)
			}
		})
		seen[test.action] = struct{}{}
	}
	for _, action := range browserdomain.SupportedActions() {
		_, ok := seen[action]
		require.True(t, ok, "missing browser transcript detail test for %s", action)
	}
}

func TestBrowserToolDisplayDetail_PreservesNonDefaultPort(t *testing.T) {
	detail := getToolInputDisplayDetail("browser", `{
		"action":"navigate",
		"session_id":"browser_1",
		"tab_id":"tab_1",
		"url":"http://127.0.0.1:8080/news?token=secret"
	}`)

	require.Equal(t, "navigate:http://127.0.0.1:8080", detail)
}

func TestBrowserToolDisplayDetail_DescribesActionVariants(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "viewport screenshot",
			input: `{"action":"screenshot","session_id":"browser_1","tab_id":"tab_1"}`,
			want:  "screenshot:Viewport · Tab tab_1",
		},
		{
			name:  "page load",
			input: `{"action":"wait","session_id":"browser_1","tab_id":"tab_1","condition":"load"}`,
			want:  "wait:Page load · Tab tab_1",
		},
		{
			name:  "URL match",
			input: `{"action":"wait","session_id":"browser_1","tab_id":"tab_1","condition":"url","value":"secret"}`,
			want:  "wait:URL matches · Tab tab_1",
		},
		{
			name:  "visible element",
			input: `{"action":"wait","session_id":"browser_1","tab_id":"tab_1","condition":"visible","ref":"g1e7"}`,
			want:  "wait:Element becomes visible · Element g1e7",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			detail := getToolInputDisplayDetail("browser", test.input)
			require.Equal(t, test.want, detail)
			require.NotContains(t, detail, "secret")
		})
	}
}

func TestBrowserToolDisplayDetail_PresentsSafeArtifactMetadata(t *testing.T) {
	detail := getToolOutputDisplayDetail("browser", `{
		"handle":"artifact_123","kind":"screenshot","name":"screenshot.png","size":2048,
		"source":"https://example.com/private?token=secret"
	}`)
	require.Equal(t, "screenshot:screenshot.png · artifact_123 · 2048 bytes", detail)
	require.Equal(
		t,
		"screenshot.png · artifact_123 · 2048 bytes",
		getToolBranchDisplayDetail("Browser", detail, true),
	)
	require.NotContains(t, detail, "token")
}

func TestBrowserToolFailureDisplayDetail_PresentsStructuredErrorsOnly(t *testing.T) {
	require.Equal(
		t,
		"Browser is unavailable · retryable",
		getToolFailureDisplayDetail(
			"browser",
			`{"error":{"code":"browser_unavailable","message":"browser is unavailable","retryable":true}}`,
		),
	)
	require.Empty(t, getToolFailureDisplayDetail("browser", `{"error":"secret backend detail"}`))
	require.Empty(t, getToolFailureDisplayDetail("read_file", `{"error":{"message":"access denied"}}`))
	require.Empty(t, getToolFailureDisplayDetail("browser", "not-json"))
}

func TestRenderBrowserTranscript_ShowsActionSpecificLifecycleStates(t *testing.T) {
	for _, test := range []struct {
		name      string
		completed bool
		terminal  toolTranscriptTerminalStatus
		wantTitle string
	}{
		{name: "pending", wantTitle: "Navigating Browser"},
		{name: "completed", completed: true, wantTitle: "Navigated Browser"},
		{name: "failed", terminal: toolTranscriptTerminalStatusFailed, wantTitle: "Browser Navigation Failed"},
		{name: "interrupted", terminal: toolTranscriptTerminalStatusInterrupted, wantTitle: "Browser Navigation Interrupted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cell := toolTranscriptCell{
				id: "call_1", action: "Browser", detail: "navigate:https://example.com",
				completed: test.completed, terminalStatus: test.terminal,
			}
			group := toolTranscriptGroup{action: "Browser"}
			group.add(cell)
			rendered := stripANSI(renderToolTranscriptGroup(group, 0))
			require.Contains(t, rendered, test.wantTitle)
			require.Contains(t, rendered, "└ https://example.com")
			require.NotContains(t, rendered, "browser navigate")
			require.NotContains(t, rendered, "/private")
		})
	}
}

func TestRenderBrowserTranscript_ShowsFailureReasonWithoutActionDetail(t *testing.T) {
	group := toolTranscriptGroup{action: "Browser"}
	group.add(toolTranscriptCell{
		id:             "call_1",
		action:         "Browser",
		detail:         "browser",
		failure:        "Browser is unavailable · retryable",
		terminalStatus: toolTranscriptTerminalStatusFailed,
	})

	rendered := stripANSI(renderToolTranscriptGroup(group, 0))

	require.Contains(t, rendered, "Browser Action Failed")
	require.Contains(t, rendered, "└ Browser is unavailable · retryable")
}

func TestRenderBrowserTranscript_OmitsRedundantBranchAndDuration(t *testing.T) {
	startedAt := time.Date(2026, 7, 20, 21, 0, 0, 0, time.UTC)
	now := startedAt.Add(17 * time.Second)
	cell := toolTranscriptCell{
		id: "call_1", action: "Browser", detail: "start", startedAt: startedAt,
	}
	group := toolTranscriptGroup{action: "Browser"}
	group.add(cell)

	rendered := stripANSI(renderToolTranscriptGroupWithContext(
		group,
		transcriptRenderContext{Width: 80, Now: now},
	))

	require.Contains(t, rendered, "● Starting Browser (17s)")
	require.NotContains(t, rendered, "└")
	require.NotContains(t, rendered, "browser browser")
}

func TestRenderBrowserTranscript_LabelsMixedActions(t *testing.T) {
	group := toolTranscriptGroup{action: "Browser"}
	group.add(toolTranscriptCell{id: "call_1", action: "Browser", detail: "navigate:https://example.com"})
	group.add(toolTranscriptCell{id: "call_2", action: "Browser", detail: "snapshot:Tab tab_1"})

	rendered := stripANSI(renderToolTranscriptGroup(group, 0))

	require.Contains(t, rendered, "● Running Browser Actions")
	require.Contains(t, rendered, "├ Navigation · https://example.com")
	require.Contains(t, rendered, "└ Snapshot · Tab tab_1")
}

func TestSessionTimelineToTranscriptCells_PreservesBrowserBranchesAfterHydration(t *testing.T) {
	startedAt := time.Date(2026, 7, 28, 18, 10, 55, 0, time.UTC)
	toolCalls := []struct {
		id        string
		input     string
		startedAt time.Time
		duration  time.Duration
	}{
		{"call_status", `{"action":"status"}`, startedAt, 10 * time.Millisecond},
		{"call_start", `{"action":"start","profile":"default"}`, startedAt.Add(7 * time.Second), 6 * time.Second},
		{"call_open", `{"action":"open","url":"https://www.cnn.com/private?token=secret"}`, startedAt.Add(21 * time.Second), 4 * time.Second},
		{"call_snapshot", `{"action":"snapshot","tab_id":"tab_1"}`, startedAt.Add(35 * time.Second), time.Second},
	}
	messages := []agentapi.SessionTimelineMessage{{
		Message: morphmsg.Message{Role: morphmsg.RoleUser, Content: "get news", CreatedAt: startedAt.Add(-time.Second)},
	}}
	traceEvents := make([]agentapi.SessionTimelineTraceEvent, 0, len(toolCalls)*2)
	for _, call := range toolCalls {
		messages = append(messages,
			agentapi.SessionTimelineMessage{Message: morphmsg.Message{
				Role:      morphmsg.RoleAssistant,
				CreatedAt: call.startedAt,
				ToolCalls: []morphmsg.ToolCall{{ID: call.id, Name: "browser", Input: call.input}},
			}},
			agentapi.SessionTimelineMessage{Message: morphmsg.Message{
				Role:       morphmsg.RoleTool,
				Name:       "browser",
				ToolCallID: call.id,
				Content:    `{"name":"browser","output":"{}"}`,
				CreatedAt:  call.startedAt.Add(call.duration),
			}},
		)
		traceEvents = append(traceEvents,
			agentapi.SessionTimelineTraceEvent{Event: agentsession.TraceEvent{
				Type:      trace.EvtToolInvocationStarted,
				Timestamp: call.startedAt,
				Payload:   map[string]any{"id": call.id, "name": "browser"},
			}},
			agentapi.SessionTimelineTraceEvent{Event: agentsession.TraceEvent{
				Type:      trace.EvtToolInvocationCompleted,
				Timestamp: call.startedAt.Add(call.duration),
				Payload:   map[string]any{"tool_call_id": call.id, "name": "browser"},
			}},
		)
	}

	cells := sessionTimelineToTranscriptCells(client.SessionTimeline{
		Messages:    messages,
		TraceEvents: traceEvents,
	})
	rendered := stripANSI(renderTranscriptCells(cells))

	require.Contains(t, rendered, "Start · Profile default")
	require.Contains(t, rendered, "Tab Opening · https://www.cnn.com")
	require.Contains(t, rendered, "Snapshot · Tab tab_1")
	require.NotContains(t, rendered, "private")
	require.NotContains(t, rendered, "token=secret")
}

func TestRenderBrowserTranscript_LabelsMixedTerminalOutcomesAsCompletedWithIssues(t *testing.T) {
	group := toolTranscriptGroup{action: "Browser"}
	group.add(toolTranscriptCell{
		id:             "call_1",
		action:         "Browser",
		detail:         "reload:Tab stale",
		terminalStatus: toolTranscriptTerminalStatusFailed,
		failure:        "Browser session not found",
	})
	group.add(toolTranscriptCell{
		id:        "call_2",
		action:    "Browser",
		detail:    "start:Profile default",
		completed: true,
	})
	group.add(toolTranscriptCell{
		id:        "call_3",
		action:    "Browser",
		detail:    "open:https://cnn.com",
		completed: true,
	})

	rendered := stripANSI(renderToolTranscriptGroup(group, 0))

	require.Contains(t, rendered, "Browser Actions Completed with Issues")
	require.Contains(t, rendered, "Reload · Tab stale · Browser session not found")
	require.Contains(t, rendered, "Start · Profile default")
	require.Contains(t, rendered, "Tab Opening · https://cnn.com")
	require.NotContains(t, rendered, "Browser Actions Failed")
}

func TestBrowserTranscriptTitle_HandlesActionCatalogAndMixedGroups(t *testing.T) {
	require.Equal(
		t, "Capturing Browser Screenshot",
		getBrowserToolTranscriptTitle([]toolTranscriptDetail{{text: "screenshot:artifact"}}, false, false, false),
	)
	require.Equal(
		t, "Captured Browser Screenshot",
		getBrowserToolTranscriptTitle([]toolTranscriptDetail{{text: "screenshot:artifact"}}, true, false, false),
	)
	require.Equal(
		t, "Running Browser Actions",
		getBrowserToolTranscriptTitle(
			[]toolTranscriptDetail{{text: "navigate:url"}, {text: "snapshot:tab"}}, false, false, false,
		),
	)
}

func TestRenderTranscriptCells_CompactsConsecutiveManualCompactionEvents(t *testing.T) {
	rendered := stripANSI(renderTranscriptCells([]transcriptCell{
		manualCompactionTranscriptCell{state: manualCompactionState{Status: "running", Label: autoCompactionLabel}},
		manualCompactionTranscriptCell{state: manualCompactionState{Status: "succeeded", Label: autoCompactionLabel}},
	}))

	require.NotContains(t, rendered, "Automatic compaction started")
	require.Equal(t, 1, strings.Count(rendered, "Automatic Compaction"))
}

func TestRenderManualCompactionCell_UsesToolEventLifecycleRendering(t *testing.T) {
	tests := []struct {
		name        string
		state       manualCompactionState
		wantOutcome toolTranscriptGroupOutcome
		want        []string
	}{
		{
			name:        "running",
			state:       manualCompactionState{Status: "running", Label: autoCompactionLabel},
			wantOutcome: toolTranscriptGroupOutcomeRunning,
			want:        []string{"● Automatic Compaction"},
		},
		{
			name:        "completed",
			state:       manualCompactionState{Status: "succeeded", Label: autoCompactionLabel},
			wantOutcome: toolTranscriptGroupOutcomeCompleted,
			want:        []string{"● Automatic Compaction"},
		},
		{
			name:        "failed",
			state:       manualCompactionState{Status: "failed", Label: autoCompactionLabel, Error: "summary failed"},
			wantOutcome: toolTranscriptGroupOutcomeFailed,
			want:        []string{"● Failed Automatic Compaction", "└ summary failed"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.wantOutcome, getCompactionToolGroup(tt.state).outcome())

			rendered := stripANSI(renderManualCompactionCell(
				manualCompactionTranscriptCell{state: tt.state},
				transcriptRenderContext{Width: 40},
			))

			for _, want := range tt.want {
				require.Contains(t, rendered, want)
			}
			require.NotContains(t, rendered, strings.Repeat("─", 40))
		})
	}
}

func TestRenderManualCompactionCell_HidesUnknownLifecycleStatus(t *testing.T) {
	rendered := renderManualCompactionCell(
		manualCompactionTranscriptCell{state: manualCompactionState{Status: "unknown"}},
		transcriptRenderContext{Width: 40},
	)

	require.Empty(t, rendered)
}

func TestRenderTranscriptCells_RendersSessionSearchWithFriendlyText(t *testing.T) {
	startedAt := time.Date(2026, 5, 20, 23, 0, 0, 0, time.UTC)
	running := stripANSI(defaultTranscriptRenderer.RenderCells([]transcriptCell{
		toolTranscriptTestCellWithTiming("call_1", "session_search", `Search "favorite color"`, startedAt, time.Time{}, false),
	}, transcriptRenderContext{Width: 80, Now: startedAt.Add(45 * time.Second)}))
	completed := stripANSI(defaultTranscriptRenderer.RenderCells([]transcriptCell{
		toolTranscriptTestCellWithTiming("call_1", "session_search", `Search "favorite color"`, startedAt, startedAt.Add(45*time.Second), true),
	}, transcriptRenderContext{Width: 80, Now: startedAt.Add(45 * time.Second)}))

	require.Contains(t, running, "● Searching Session (45s)")
	require.NotContains(t, running, "└")
	require.NotContains(t, running, "session_search")
	require.Contains(t, completed, "● Searched Session (45s)")
	require.NotContains(t, completed, "└")
	require.NotContains(t, completed, "session_search")
}

func TestRenderTranscriptCells_RendersWebExtractWithFriendlyText(t *testing.T) {
	startedAt := time.Date(2026, 5, 20, 23, 0, 0, 0, time.UTC)
	running := stripANSI(defaultTranscriptRenderer.RenderCells([]transcriptCell{
		toolTranscriptTestCellWithTiming("call_1", "web_extract", "web_extract", startedAt, time.Time{}, false),
	}, transcriptRenderContext{Width: 80, Now: startedAt.Add(4 * time.Second)}))
	completed := stripANSI(defaultTranscriptRenderer.RenderCells([]transcriptCell{
		toolTranscriptTestCellWithTiming("call_1", "web_extract", "web_extract", startedAt, startedAt.Add(4*time.Second), true),
	}, transcriptRenderContext{Width: 80, Now: startedAt.Add(4 * time.Second)}))

	require.Contains(t, running, "● Extracting from web (4s)")
	require.NotContains(t, running, "└")
	require.NotContains(t, running, "web_extract")
	require.Contains(t, completed, "● Extraction finished (4s)")
	require.NotContains(t, completed, "└")
	require.NotContains(t, completed, "web_extract")
}

func TestRenderTranscriptCells_RendersTimeWithFriendlyText(t *testing.T) {
	startedAt := time.Date(2026, 5, 20, 23, 0, 0, 0, time.UTC)
	running := stripANSI(defaultTranscriptRenderer.RenderCells([]transcriptCell{
		toolTranscriptTestCellWithTiming("call_1", "time", "time", startedAt, time.Time{}, false),
	}, transcriptRenderContext{Width: 80, Now: startedAt.Add(2 * time.Second)}))
	completed := stripANSI(defaultTranscriptRenderer.RenderCells([]transcriptCell{
		toolTranscriptTestCellWithTiming("call_1", "time", "time", startedAt, startedAt.Add(2*time.Second), true),
	}, transcriptRenderContext{Width: 80, Now: startedAt.Add(2 * time.Second)}))

	require.Contains(t, running, "● Checking time (2s)")
	require.NotContains(t, running, "└")
	require.Contains(t, completed, "● Checked time (2s)")
	require.NotContains(t, completed, "└")
}

func TestRenderTranscriptCells_RendersProcessActionsWithFriendlyText(t *testing.T) {
	startedAt := time.Date(2026, 5, 20, 23, 0, 0, 0, time.UTC)
	exitCode := 0
	cases := []struct {
		name      string
		input     *trace.ProcessToolState
		output    *trace.ProcessToolState
		header    string
		branch    string
		completed bool
	}{
		{
			name:   "running start",
			input:  &trace.ProcessToolState{Operation: trace.ProcessToolOperationStart, Command: "sleep 10"},
			header: "● Starting process (2s)",
			branch: "└ sleep 10 (2s)",
		},
		{
			name:      "completed start",
			input:     &trace.ProcessToolState{Operation: trace.ProcessToolOperationStart, Command: "sleep 10"},
			output:    &trace.ProcessToolState{ProcessID: "proc_1", Status: "running"},
			header:    "● Process started (2s)",
			branch:    "└ proc_1 running (2s)",
			completed: true,
		},
		{
			name:      "status",
			input:     &trace.ProcessToolState{Operation: trace.ProcessToolOperationStatus, ProcessID: "proc_1"},
			output:    &trace.ProcessToolState{ProcessID: "proc_1", Status: "exited", ExitCode: &exitCode},
			header:    "● Process exited (2s)",
			branch:    "└ proc_1 exited exit 0 (2s)",
			completed: true,
		},
		{
			name:      "read",
			input:     &trace.ProcessToolState{Operation: trace.ProcessToolOperationRead, ProcessID: "proc_1"},
			output:    &trace.ProcessToolState{Operation: trace.ProcessToolOperationRead, ProcessID: "proc_1", StdoutBytes: 120, StderrBytes: 5},
			header:    "● Output read (2s)",
			branch:    "└ proc_1 120B stdout 5B stderr (2s)",
			completed: true,
		},
		{
			name:      "stop",
			input:     &trace.ProcessToolState{Operation: trace.ProcessToolOperationStop, ProcessID: "proc_1"},
			output:    &trace.ProcessToolState{ProcessID: "proc_1", Status: "stopped"},
			header:    "● Process stopped (2s)",
			branch:    "└ proc_1 stopped (2s)",
			completed: true,
		},
		{
			name:      "list",
			input:     &trace.ProcessToolState{Operation: trace.ProcessToolOperationList},
			output:    &trace.ProcessToolState{Operation: trace.ProcessToolOperationList, Count: 3},
			header:    "● Listed processes (2s)",
			branch:    "└ Found 3 processes (2s)",
			completed: true,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cells := []transcriptCell{
				toolTranscriptTestCellWithProcessState("call_1", "process", tt.input, startedAt, time.Time{}, false),
			}
			if tt.completed {
				cells = append(
					cells,
					toolTranscriptTestCellWithProcessState("call_1", "process", tt.output, time.Time{}, startedAt.Add(2*time.Second), true),
				)
			}

			rendered := stripANSI(defaultTranscriptRenderer.RenderCells(
				cells,
				transcriptRenderContext{Width: 80, Now: startedAt.Add(2 * time.Second)},
			))

			require.Contains(t, rendered, tt.header)
			require.Contains(t, rendered, tt.branch)
			require.NotContains(t, rendered, "└ process")
		})
	}
}

func TestRenderTranscriptCells_GroupsConsecutiveProcessRetries(t *testing.T) {
	startedAt := time.Date(2026, 5, 20, 23, 0, 0, 0, time.UTC)
	command := "python3 -m http.server 8000"
	cells := []transcriptCell{
		thoughtTranscriptCell{duration: time.Second},
		toolTranscriptTestCellWithProcessState(
			"call_1",
			"process",
			&trace.ProcessToolState{Operation: trace.ProcessToolOperationStart, Command: command},
			startedAt,
			time.Time{},
			false,
		),
		toolTranscriptTestCellWithProcessState(
			"call_1",
			"process",
			&trace.ProcessToolState{Status: "failed", ErrorCode: "process_start_failed", Error: "address already in use"},
			time.Time{},
			startedAt.Add(time.Second),
			true,
		),
		thoughtTranscriptCell{duration: time.Second},
		toolTranscriptTestCellWithProcessState(
			"call_2",
			"process",
			&trace.ProcessToolState{Operation: trace.ProcessToolOperationStart, Command: command},
			startedAt.Add(2*time.Second),
			time.Time{},
			false,
		),
		toolTranscriptTestCellWithProcessState(
			"call_2",
			"process",
			&trace.ProcessToolState{ProcessID: "proc_1", Status: "running"},
			time.Time{},
			startedAt.Add(3*time.Second),
			true,
		),
	}

	rendered := stripANSI(defaultTranscriptRenderer.RenderCells(
		cells,
		transcriptRenderContext{Width: 80, Now: startedAt.Add(4 * time.Second)},
	))

	require.Equal(t, 1, strings.Count(rendered, "Process started"))
	require.Equal(t, 1, strings.Count(rendered, "Thought for 1s"))
	require.Contains(t, rendered, "Failed 1 attempt: address already in use")
	require.Contains(t, rendered, "proc_1 running")
}

func TestRenderTranscriptCells_RendersPlanWithFriendlyText(t *testing.T) {
	startedAt := time.Date(2026, 5, 20, 23, 0, 0, 0, time.UTC)
	cases := []struct {
		name            string
		state           *trace.PlanToolState
		running         string
		completed       string
		runningBranch   string
		completedBranch string
	}{
		{
			name:            "read",
			state:           &trace.PlanToolState{Operation: trace.PlanToolOperationRead},
			running:         "● Reading plan (4s)",
			completed:       "● Plan read (4s)",
			runningBranch:   "Read current plan",
			completedBranch: "",
		},
		{
			name:            "update",
			state:           &trace.PlanToolState{Operation: trace.PlanToolOperationUpdate, ChangedCount: 1},
			running:         "● Updating plan (4s)",
			completed:       "● Plan updated (4s)",
			runningBranch:   "Updated 1 task",
			completedBranch: "",
		},
		{
			name:            "clear completed",
			state:           &trace.PlanToolState{Operation: trace.PlanToolOperationClearCompleted, ChangedCount: 1},
			running:         "● Clearing completed plan steps (4s)",
			completed:       "● Plan cleared (4s)",
			runningBranch:   "Cleared 1 task",
			completedBranch: "",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			running := stripANSI(defaultTranscriptRenderer.RenderCells([]transcriptCell{
				toolTranscriptTestCellWithPlanState("call_1", "plan_tool", tt.state, startedAt, time.Time{}, false),
			}, transcriptRenderContext{Width: 80, Now: startedAt.Add(4 * time.Second)}))
			completed := stripANSI(defaultTranscriptRenderer.RenderCells([]transcriptCell{
				toolTranscriptTestCellWithPlanState("call_1", "plan_tool", tt.state, startedAt, startedAt.Add(4*time.Second), true),
			}, transcriptRenderContext{Width: 80, Now: startedAt.Add(4 * time.Second)}))

			require.Contains(t, running, tt.running)
			require.Contains(t, running, "└ "+tt.runningBranch)
			require.NotContains(t, running, "plan_tool")
			require.Contains(t, completed, tt.completed)
			if tt.completedBranch == "" {
				require.NotContains(t, completed, "└")
			} else {
				require.Contains(t, completed, "└ "+tt.completedBranch)
			}
			require.NotContains(t, completed, "plan_tool")
		})
	}
}

func TestRenderTranscriptCells_RendersCompletedPlanSummaryBranch(t *testing.T) {
	startedAt := time.Date(2026, 5, 20, 23, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		input  *trace.PlanToolState
		output *trace.PlanToolState
		branch string
	}{
		{
			name:  "partial update",
			input: &trace.PlanToolState{Operation: trace.PlanToolOperationUpdate, ChangedCount: 1},
			output: &trace.PlanToolState{
				TotalCount:     3,
				CompletedCount: 1,
				Changes:        []trace.PlanToolChange{{Index: 2, ID: "step-2", Action: "completed"}},
			},
			branch: "Task 2 completed",
		},
		{
			name:  "content update",
			input: &trace.PlanToolState{Operation: trace.PlanToolOperationUpdate, ChangedCount: 1},
			output: &trace.PlanToolState{
				TotalCount: 3,
				Changes: []trace.PlanToolChange{
					{Index: 1, ID: "step-1", Action: "updated", Fields: []string{"content"}},
				},
			},
			branch: "Task 1 content updated",
		},
		{
			name:  "status update",
			input: &trace.PlanToolState{Operation: trace.PlanToolOperationUpdate, ChangedCount: 1},
			output: &trace.PlanToolState{
				TotalCount: 3,
				Changes: []trace.PlanToolChange{
					{Index: 2, ID: "step-2", Action: "updated", Fields: []string{"status"}},
				},
			},
			branch: "Task 2 status updated",
		},
		{
			name:  "content and status update",
			input: &trace.PlanToolState{Operation: trace.PlanToolOperationUpdate, ChangedCount: 1},
			output: &trace.PlanToolState{
				TotalCount: 3,
				Changes: []trace.PlanToolChange{
					{Index: 3, ID: "step-3", Action: "updated", Fields: []string{"status", "content"}},
				},
			},
			branch: "Task 3 status+content updated",
		},
		{
			name:  "many mixed updates",
			input: &trace.PlanToolState{Operation: trace.PlanToolOperationUpdate, ChangedCount: 3},
			output: &trace.PlanToolState{
				TotalCount:     3,
				CompletedCount: 2,
				Changes: []trace.PlanToolChange{
					{Index: 1, ID: "step-1", Action: "completed"},
					{Index: 2, ID: "step-2", Action: "completed"},
					{Index: 3, ID: "step-3", Action: "updated"},
				},
			},
			branch: "Completed 2 tasks",
		},
		{
			name:  "many status updates",
			input: &trace.PlanToolState{Operation: trace.PlanToolOperationUpdate, ChangedCount: 3},
			output: &trace.PlanToolState{
				TotalCount: 3,
				Changes: []trace.PlanToolChange{
					{Index: 1, ID: "step-1", Action: "updated", Fields: []string{"status"}},
					{Index: 2, ID: "step-2", Action: "updated", Fields: []string{"status"}},
					{Index: 3, ID: "step-3", Action: "updated", Fields: []string{"status"}},
				},
			},
			branch: "Updated status for 3 tasks",
		},
		{
			name:   "all completed",
			input:  &trace.PlanToolState{Operation: trace.PlanToolOperationUpdate, ChangedCount: 3},
			output: &trace.PlanToolState{TotalCount: 3, CompletedCount: 3},
			branch: "Completed all 3 tasks",
		},
		{
			name:   "read",
			input:  &trace.PlanToolState{Operation: trace.PlanToolOperationRead},
			output: &trace.PlanToolState{TotalCount: 3, CompletedCount: 1},
			branch: "Found 3 tasks",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			rendered := stripANSI(defaultTranscriptRenderer.RenderCells([]transcriptCell{
				toolTranscriptTestCellWithPlanState("call_1", "plan_tool", tt.input, startedAt, time.Time{}, false),
				toolTranscriptTestCellWithPlanState("call_1", "plan_tool", tt.output, time.Time{}, startedAt.Add(4*time.Second), true),
			}, transcriptRenderContext{Width: 80, Now: startedAt.Add(4 * time.Second)}))

			require.Contains(t, rendered, "● ")
			require.Contains(t, rendered, "└ "+tt.branch)
			require.NotContains(t, rendered, "plan_tool")
		})
	}
}

func TestTimelineMessageToTranscriptCell_MergesPlanOutputState(t *testing.T) {
	startedAt := time.Date(2026, 5, 20, 23, 0, 0, 0, time.UTC)
	completedAt := startedAt.Add(4 * time.Second)
	cell := timelineMessageToTranscriptCell(
		morphmsg.Message{
			Role:       morphmsg.RoleTool,
			Name:       "plan_tool",
			ToolCallID: "call_1",
			Content: `{
				"name": "plan_tool",
				"output": "{\"summary\":{\"total\":3,\"completed\":1},\"changes\":[{\"index\":2,\"id\":\"step-2\",\"action\":\"completed\"}]}"
			}`,
			CreatedAt: completedAt,
		},
		map[string]timelineToolCallDetail{
			"call_1": {
				planState: &trace.PlanToolState{
					Operation:    trace.PlanToolOperationUpdate,
					ChangedCount: 3,
				},
				startedAt: startedAt,
			},
		},
	)

	rendered := stripANSI(defaultTranscriptRenderer.RenderCells(
		[]transcriptCell{cell},
		transcriptRenderContext{Width: 80, Now: completedAt},
	))
	require.Contains(t, rendered, "└ Task 2 completed")
	require.NotContains(t, rendered, "Updated 3 tasks")
}

func TestRenderTranscriptCells_HidesCompletedPlanInputFallback(t *testing.T) {
	startedAt := time.Date(2026, 5, 20, 23, 0, 0, 0, time.UTC)
	completedAt := startedAt.Add(4 * time.Second)
	rendered := stripANSI(defaultTranscriptRenderer.RenderCells([]transcriptCell{
		toolTranscriptTestCellWithPlanState(
			"call_1",
			"plan_tool",
			&trace.PlanToolState{
				Operation:    trace.PlanToolOperationUpdate,
				ChangedCount: 3,
			},
			startedAt,
			completedAt,
			true,
		),
		toolTranscriptTestCellWithPlanState(
			"call_2",
			"plan_tool",
			&trace.PlanToolState{
				Operation:      trace.PlanToolOperationUpdate,
				ChangedCount:   2,
				TotalCount:     3,
				CompletedCount: 1,
				Changes: []trace.PlanToolChange{
					{Index: 1, ID: "step-1", Action: "completed", Fields: []string{"status"}},
					{Index: 2, ID: "step-2", Action: "updated", Fields: []string{"status"}},
				},
			},
			startedAt,
			completedAt,
			true,
		),
	}, transcriptRenderContext{Width: 80, Now: completedAt}))

	require.Contains(t, rendered, "Task 1 completed; Task 2 status updated")
	require.NotContains(t, rendered, "Updated 3 tasks")
}

func TestRenderTranscriptCells_AnimatesRunningToolDot(t *testing.T) {
	cells := []transcriptCell{toolTranscriptTestCell("call_1", "web_search", "")}
	first := stripANSI(renderTranscriptCellsWithFrame(cells, 80, 0))
	next := stripANSI(renderTranscriptCellsWithFrame(cells, 80, 1))
	completed := stripANSI(renderTranscriptCellsWithFrame([]transcriptCell{
		toolTranscriptTestCell("call_1", "web_search", "", true),
	}, 80, 1))

	require.Contains(t, first, "● Web Search")
	require.Contains(t, next, "◖ Web Search")
	require.Contains(t, completed, "● Searched")
}

func TestRenderTranscriptCells_RendersToolElapsedTime(t *testing.T) {
	originalCurrentTime := currentTime
	t.Cleanup(func() { currentTime = originalCurrentTime })
	startedAt := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	currentTime = func() time.Time {
		return startedAt.Add(10 * time.Second)
	}

	running := stripANSI(renderTranscriptCells([]transcriptCell{
		toolTranscriptTestCellWithTiming("call_1", "list_files", "", startedAt, time.Time{}, false),
	}))
	completed := stripANSI(renderTranscriptCells([]transcriptCell{
		toolTranscriptTestCellWithTiming("call_1", "list_files", "", startedAt, startedAt.Add(12*time.Second), true),
	}))

	require.Contains(t, running, "● List Files (10s)")
	require.Contains(t, running, "└ list_files (10s)")
	require.Contains(t, completed, "● List Files (12s)")
	require.Contains(t, completed, "└ list_files (12s)")
}

func TestRenderTranscriptCells_RendersFileToolDetails(t *testing.T) {
	startedAt := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	rendered := renderTranscriptCells([]transcriptCell{
		toolTranscriptTestCellWithTiming(
			"call_1",
			"write_file",
			"write_file file.txt",
			startedAt,
			startedAt.Add(30*time.Second),
			true,
		),
		toolTranscriptTestCellWithTiming(
			"call_2",
			"patch",
			"patch file.txt +1 -1",
			startedAt,
			startedAt.Add(30*time.Second),
			true,
		),
		toolTranscriptTestCellWithTiming(
			"call_3",
			"patch",
			"patch",
			startedAt,
			startedAt.Add(30*time.Second),
			true,
		),
		toolTranscriptTestCellWithTiming(
			"call_4",
			"read_file",
			"read_file file.txt",
			startedAt,
			startedAt.Add(30*time.Second),
			true,
		),
	})
	plain := stripANSI(rendered)

	require.Contains(t, plain, "● Wrote (30s)")
	require.Contains(t, plain, "└ write_file file.txt (30s)")
	require.Contains(t, plain, "● Patch")
	require.Contains(t, plain, "├ patch file.txt +1 -1 (30s)")
	require.Contains(t, plain, "└ patch (30s)")
	require.Contains(t, plain, "● Read (30s)")
	require.Contains(t, plain, "└ read_file file.txt (30s)")
	require.Contains(t, rendered, "\x1b[38;5;83m+1")
	require.Contains(t, rendered, "\x1b[38;5;203m-1")
}

func TestRenderTranscriptCells_RendersSearchFilesDetail(t *testing.T) {
	startedAt := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	rendered := renderTranscriptCells([]transcriptCell{
		toolTranscriptTestCellWithTiming(
			"call_1",
			"search_files",
			`Search "println" in .`,
			startedAt,
			startedAt.Add(3*time.Second),
			true,
		),
	})
	plain := stripANSI(rendered)

	require.Contains(t, plain, "● Searched Files (3s)")
	require.Contains(t, plain, `└ Search "println" in . (3s)`)
	require.NotContains(t, plain, "search_files")
}

func TestRenderTranscriptCells_RendersRunCommandsWithShellLayout(t *testing.T) {
	rendered := renderTranscriptCells([]transcriptCell{
		toolTranscriptTestCell("call_1", "run_command", `sleep 10 && echo "Done" [timeout 8s]`),
	})
	plain := stripANSI(rendered)

	require.Contains(t, plain, "● Running 1 shell command…")
	require.Contains(t, plain, `└ $ sleep 10 && echo "Done" [timeout 8s]`)
	require.NotContains(t, plain, "ctrl+b")
	require.Contains(t, rendered, "\x1b[")
}

func TestRenderTranscriptCells_RendersCompletedRunCommandsWithPastTense(t *testing.T) {
	rendered := renderTranscriptCells([]transcriptCell{
		toolTranscriptTestCell("call_1", "run_command", `sleep 10 [timeout 30s]`, true),
	})
	plain := stripANSI(rendered)

	require.Contains(t, plain, "● Ran 1 shell command")
	require.Contains(t, plain, "└ $ sleep 10")
	require.NotContains(t, plain, "[timeout 30s]")
	require.NotContains(t, plain, "Running")
	require.NotContains(t, plain, "…")
	require.Contains(t, rendered, "\x1b[")
}

func TestRenderTranscriptCells_NormalizesLegacyRunCommandTimeouts(t *testing.T) {
	startedAt := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	originalCurrentTime := currentTime
	t.Cleanup(func() { currentTime = originalCurrentTime })
	currentTime = func() time.Time {
		return startedAt.Add(6 * time.Second)
	}

	rendered := renderTranscriptCells([]transcriptCell{
		toolTranscriptTestCellWithTiming("call_1", "run_command", "sleep 10 (30s)", startedAt, time.Time{}, false),
	})
	plain := stripANSI(rendered)

	require.Contains(t, plain, "└ $ sleep 10 [timeout 30s] (6s)")
	require.NotContains(t, plain, "sleep 10 (30s) (6s)")
}

func TestRenderTranscriptCells_RemovesRunCommandTimeoutHintWhenCompleted(t *testing.T) {
	startedAt := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		detail     string
		notContain string
	}{
		{name: "current timeout hint", detail: "sleep 10 [timeout 30s]", notContain: "[timeout 30s]"},
		{name: "legacy termination hint", detail: "sleep 10 [terminates in 30s]", notContain: "[terminates in 30s]"},
		{name: "legacy parenthetical timeout", detail: "sleep 10 (30s)", notContain: "sleep 10 (30s) (6s)"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			rendered := renderTranscriptCells([]transcriptCell{
				toolTranscriptTestCellWithTiming(
					"call_1",
					"run_command",
					tt.detail,
					startedAt,
					startedAt.Add(6*time.Second),
					true,
				),
			})
			plain := stripANSI(rendered)

			require.Contains(t, plain, "└ $ sleep 10 (6s)")
			require.NotContains(t, plain, tt.notContain)
		})
	}
}

func TestRenderTranscriptCell_RendersUserMessageBox(t *testing.T) {
	rendered := renderTranscriptTestCellWithWidth(userTranscriptCell{text: "Some message."}, 40)
	plain := stripANSI(rendered)
	lines := strings.Split(plain, "\n")

	require.Contains(t, plain, "❯ Some message.")
	require.Len(t, lines, 3)
	require.Equal(t, strings.Repeat("▄", 40), lines[0])
	require.Equal(t, "❯ Some message.", strings.TrimRight(lines[1], " "))
	require.Equal(t, strings.Repeat("▀", 40), lines[2])
	require.NotContains(t, plain, "┌")
	require.NotContains(t, plain, "│")
	require.NotContains(t, plain, "└")
	require.NotContains(t, plain, "You:")
	require.Contains(t, rendered, "\x1b[")
	require.Contains(t, rendered, "48;5;235")
	require.Contains(t, rendered, "48;5;235mSome message")
}

func TestRenderTranscriptCell_RendersMultilineUserMessageWithSinglePrompt(t *testing.T) {
	rendered := renderTranscriptTestCellWithWidth(userTranscriptCell{text: "hello\nfriend"}, 40)
	plain := stripANSI(rendered)
	lines := strings.Split(plain, "\n")

	require.Len(t, lines, 4)
	require.Equal(t, strings.Repeat("▄", 40), lines[0])
	require.Equal(t, "❯ hello", strings.TrimRight(lines[1], " "))
	require.Equal(t, "  friend", strings.TrimRight(lines[2], " "))
	require.Equal(t, strings.Repeat("▀", 40), lines[3])
	require.Equal(t, 1, strings.Count(plain, "❯"))
	require.Contains(t, rendered, "\x1b[")
}

func TestRenderTranscriptCell_RendersAssistantMessageWithDotColumn(t *testing.T) {
	rendered := renderTranscriptTestCellWithWidth(assistantTranscriptCell{text: strings.Join([]string{
		"Morning spills across the sky,",
		"Soft gold where the shadows lie.",
		"A quiet breeze begins to sing,",
		"And wakes the world to everything.",
	}, "\n")}, 80)
	plain := stripANSI(rendered)
	lines := strings.Split(plain, "\n")

	require.Equal(t, []string{
		assistantTranscriptIndicatorGlyph + "Morning spills across the sky,",
		"  Soft gold where the shadows lie.",
		"  A quiet breeze begins to sing,",
		"  And wakes the world to everything.",
	}, lines)
	assistantTranscriptIndicatorGlyphValue := str.String(assistantTranscriptIndicatorGlyph)
	require.Equal(t, 1, strings.Count(plain, assistantTranscriptIndicatorGlyphValue.Trim()))
}

func TestRenderTranscriptCell_RendersAssistantWorkedDuration(t *testing.T) {
	rendered := renderTranscriptTestCellWithWidth(assistantTranscriptCell{
		text:     "Here's one for you.",
		duration: 6 * time.Second,
	}, 80)
	plain := stripANSI(rendered)
	lines := strings.Split(plain, "\n")

	require.Equal(t, []string{
		assistantTranscriptIndicatorGlyph + "Here's one for you.",
		"",
		assistantTranscriptWorkGlyph + "Worked for 6s",
	}, lines)
	require.Contains(t, rendered, "\x1b[")
}

func TestSessionTimelineToTranscriptCells_SkipsMessageBackedTraceDuplicates(t *testing.T) {
	now := time.Date(2026, 5, 18, 15, 0, 0, 0, time.UTC)
	cells := sessionTimelineToTranscriptCells(client.SessionTimeline{
		Messages: []agentapi.SessionTimelineMessage{
			{Message: morphmsg.Message{Role: morphmsg.RoleUser, Content: "hello there", CreatedAt: now}},
			{Message: morphmsg.Message{Role: morphmsg.RoleAssistant, Content: "hello back", CreatedAt: now.Add(time.Second)}},
		},
		TraceEvents: []agentapi.SessionTimelineTraceEvent{
			{Event: agentsession.TraceEvent{
				Type:      trace.EvtFinalAssistantResponse,
				Timestamp: now.Add(time.Second),
				Payload:   map[string]any{"message": "hello back"},
			}},
			{Event: agentsession.TraceEvent{
				Type:      trace.EvtToolInvocationStarted,
				Timestamp: now.Add(2 * time.Second),
				Payload:   map[string]any{"name": "read_file"},
			}},
		},
	})

	require.Equal(t, []string{
		"You: hello there",
		"Morph: hello back\nWorked for 1s",
		transcriptCellPlainText(toolTranscriptTestCellWithTiming("", "read_file", "", now.Add(2*time.Second), time.Time{}, false)),
	}, transcriptCellPlainTexts(cells))
}

func TestSessionTimelineToTranscriptCells_InterleavesMessagesAndTraceEventsByTime(t *testing.T) {
	now := time.Date(2026, 5, 18, 15, 0, 0, 0, time.UTC)

	cells := sessionTimelineToTranscriptCells(client.SessionTimeline{
		Messages: []agentapi.SessionTimelineMessage{
			{Message: morphmsg.Message{Role: morphmsg.RoleUser, Content: "older prompt", CreatedAt: now}},
			{Message: morphmsg.Message{Role: morphmsg.RoleAssistant, Content: "older answer", CreatedAt: now.Add(time.Second)}},
			{Message: morphmsg.Message{Role: morphmsg.RoleUser, Content: "Hi", CreatedAt: now.Add(10 * time.Second)}},
			{Message: morphmsg.Message{Role: morphmsg.RoleAssistant, Content: "Hi there", CreatedAt: now.Add(11 * time.Second)}},
		},
		TraceEvents: []agentapi.SessionTimelineTraceEvent{
			{Event: agentsession.TraceEvent{
				Type:      trace.EvtToolInvocationStarted,
				Timestamp: now.Add(2 * time.Second),
				Payload:   map[string]any{"name": "web_search"},
			}},
			{Event: agentsession.TraceEvent{
				Type:      trace.EvtToolInvocationCompleted,
				Timestamp: now.Add(3 * time.Second),
				Payload:   map[string]any{"name": "web_search"},
			}},
		},
	})

	require.Equal(t, []string{
		"You: older prompt",
		"Morph: older answer\nWorked for 1s",
		transcriptCellPlainText(toolTranscriptTestCellWithTiming("", "web_search", "", now.Add(2*time.Second), time.Time{}, false)),
		transcriptCellPlainText(toolTranscriptTestCellWithTiming("", "web_search", "", time.Time{}, now.Add(3*time.Second), true)),
		"You: Hi",
		"Morph: Hi there\nWorked for 1s",
	}, transcriptCellPlainTexts(cells))
}

func TestSessionTimelineToTranscriptCells_HydratesAssistantWorkedDurationAcrossTools(t *testing.T) {
	now := time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC)

	cells := sessionTimelineToTranscriptCells(client.SessionTimeline{
		Messages: []agentapi.SessionTimelineMessage{
			{Message: morphmsg.Message{Role: morphmsg.RoleUser, Content: "search then answer", CreatedAt: now}},
			{Message: morphmsg.Message{
				Role:      morphmsg.RoleAssistant,
				CreatedAt: now.Add(time.Second),
				ToolCalls: []morphmsg.ToolCall{{
					ID:   "call_1",
					Name: "web_search",
				}},
			}},
			{Message: morphmsg.Message{
				Role:       morphmsg.RoleTool,
				Name:       "web_search",
				ToolCallID: "call_1",
				Content:    "results",
				CreatedAt:  now.Add(3 * time.Second),
			}},
			{Message: morphmsg.Message{Role: morphmsg.RoleAssistant, Content: "final answer", CreatedAt: now.Add(6 * time.Second)}},
		},
	})

	require.Contains(t, transcriptCellPlainTexts(cells), "Morph: final answer\nWorked for 6s")
	require.Contains(t, stripANSI(renderTranscriptCells(cells)), assistantTranscriptWorkGlyph+"Worked for 6s")
}

func TestSessionTimelineToTranscriptCells_SkipsUserStoppedSessionErrors(t *testing.T) {
	now := time.Date(2026, 5, 18, 15, 0, 0, 0, time.UTC)

	cells := sessionTimelineToTranscriptCells(client.SessionTimeline{
		Messages: []agentapi.SessionTimelineMessage{
			{Message: morphmsg.Message{Role: morphmsg.RoleUser, Content: "hi", CreatedAt: now}},
			{Message: morphmsg.Message{Role: morphmsg.RoleAssistant, Content: "hello", CreatedAt: now.Add(2 * time.Second)}},
		},
		TraceEvents: []agentapi.SessionTimelineTraceEvent{{
			Event: agentsession.TraceEvent{
				Type:      trace.EvtSessionFailed,
				Timestamp: now.Add(time.Second),
				Payload:   map[string]any{"error": "context canceled"},
			},
		}},
	})

	require.Equal(t, []string{
		"You: hi",
		"Morph: hello\nWorked for 2s",
	}, transcriptCellPlainTexts(cells))
}

func TestSessionTimelineToTranscriptCells_RendersPersistedThoughtSummary(t *testing.T) {
	now := time.Date(2026, 5, 18, 15, 0, 0, 0, time.UTC)

	cells := sessionTimelineToTranscriptCells(client.SessionTimeline{
		Messages: []agentapi.SessionTimelineMessage{
			{Message: morphmsg.Message{Role: morphmsg.RoleUser, Content: "think", CreatedAt: now}},
			{Message: morphmsg.Message{Role: morphmsg.RoleAssistant, Content: "done", CreatedAt: now.Add(2 * time.Second)}},
		},
		TraceEvents: []agentapi.SessionTimelineTraceEvent{{
			Event: agentsession.TraceEvent{
				Sequence:  7,
				Type:      trace.EvtModelReasoningCompleted,
				Timestamp: now.Add(time.Second),
				Payload: map[string]any{
					"duration_ms": float64(2000),
					"summary":     "Checked the current state.",
				},
			},
		}},
	})

	require.Equal(t, []string{
		"You: think",
		"Thought: 2s",
		"Morph: done\nWorked for 2s",
	}, transcriptCellPlainTexts(cells))
	thinking, ok := cells[1].(thinkingTranscriptCell)
	require.True(t, ok)
	require.Equal(t, "trace-7", thinking.id)
	require.Equal(t, "Checked the current state.", thinking.summary)
	require.True(t, thinking.completed)
	require.False(t, thinking.expanded)
}

func TestSessionTimelineToTranscriptCells_UsesPersistedToolCallInputForToolDetails(t *testing.T) {
	now := time.Date(2026, 5, 18, 15, 0, 0, 0, time.UTC)

	cells := sessionTimelineToTranscriptCells(client.SessionTimeline{
		Messages: []agentapi.SessionTimelineMessage{
			{Message: morphmsg.Message{
				Role:      morphmsg.RoleAssistant,
				ToolCalls: []morphmsg.ToolCall{{ID: "call_1", Name: "run_command", Input: `{"command":"sleep 10","timeout_seconds":30}`}},
				CreatedAt: now,
			}},
			{Message: morphmsg.Message{
				Role:       morphmsg.RoleTool,
				Name:       "run_command",
				ToolCallID: "call_1",
				Content:    `{"output":"done"}`,
				CreatedAt:  now.Add(time.Second),
			}},
		},
	})

	require.Equal(t, []string{
		transcriptCellPlainText(toolTranscriptTestCellWithTiming(
			"call_1",
			"run_command",
			"sleep 10 [timeout 30s]",
			now,
			now.Add(time.Second),
			true,
		)),
	}, transcriptCellPlainTexts(cells))
	plain := stripANSI(renderTranscriptCells(cells))
	require.Contains(t, plain, "● Ran 1 shell command")
	require.Contains(t, plain, "└ $ sleep 10 (1s)")
	require.NotContains(t, plain, "[timeout 30s]")
	require.NotContains(t, plain, "run_command")
}

func TestSessionTimelineToTranscriptCells_RendersHydratedRunCommandLikeLiveTrace(t *testing.T) {
	now := time.Date(2026, 5, 18, 15, 0, 0, 0, time.UTC)
	hydratedCells := sessionTimelineToTranscriptCells(client.SessionTimeline{
		Messages: []agentapi.SessionTimelineMessage{
			{Message: morphmsg.Message{
				Role:      morphmsg.RoleAssistant,
				ToolCalls: []morphmsg.ToolCall{{ID: "call_1", Name: "run_command", Input: `{"command":"sleep 10","timeout_seconds":30}`}},
				CreatedAt: now,
			}},
			{Message: morphmsg.Message{
				Role:       morphmsg.RoleTool,
				Name:       "run_command",
				ToolCallID: "call_1",
				Content:    `{"output":"done"}`,
				CreatedAt:  now.Add(6 * time.Second),
			}},
		},
	})
	liveCells := sessionTimelineToTranscriptCells(client.SessionTimeline{
		TraceEvents: []agentapi.SessionTimelineTraceEvent{
			{Event: agentsession.TraceEvent{
				Type:      trace.EvtToolInvocationStarted,
				Timestamp: now,
				Payload: map[string]any{
					"id":     "call_1",
					"name":   "run_command",
					"detail": "sleep 10 [timeout 30s]",
				},
			}},
			{Event: agentsession.TraceEvent{
				Type:      trace.EvtToolInvocationCompleted,
				Timestamp: now.Add(6 * time.Second),
				Payload: map[string]any{
					"tool_call_id": "call_1",
					"name":         "run_command",
				},
			}},
		},
	})

	hydratedPlain := stripANSI(renderTranscriptCells(hydratedCells))
	livePlain := stripANSI(renderTranscriptCells(liveCells))
	require.Contains(t, hydratedPlain, "● Ran 1 shell command")
	require.Contains(t, livePlain, "● Ran 1 shell command")
	require.NotContains(t, hydratedPlain, "[timeout 30s]")
	require.NotContains(t, livePlain, "[timeout 30s]")
	require.Contains(t, hydratedPlain, "└ $ sleep 10 (6s)")
	require.Contains(t, livePlain, "└ $ sleep 10 (6s)")
	require.Equal(t, livePlain, hydratedPlain)
}

func TestSessionTimelineToTranscriptCells_RendersHydratedListFilesLikeLiveTrace(t *testing.T) {
	now := time.Date(2026, 5, 18, 15, 0, 0, 0, time.UTC)
	detail := "list_files(include_hidden=false max_entries=50 path=. recursive=false)"
	hydratedCells := sessionTimelineToTranscriptCells(client.SessionTimeline{
		Messages: []agentapi.SessionTimelineMessage{
			{Message: morphmsg.Message{
				Role:      morphmsg.RoleAssistant,
				ToolCalls: []morphmsg.ToolCall{{ID: "call_1", Name: "list_files", Input: `{"path":".","recursive":false,"include_hidden":false,"max_entries":50}`}},
				CreatedAt: now,
			}},
			{Message: morphmsg.Message{
				Role:       morphmsg.RoleTool,
				Name:       "list_files",
				ToolCallID: "call_1",
				Content:    `{"output":"done"}`,
				CreatedAt:  now.Add(time.Second),
			}},
		},
	})
	liveCells := sessionTimelineToTranscriptCells(client.SessionTimeline{
		TraceEvents: []agentapi.SessionTimelineTraceEvent{
			{Event: agentsession.TraceEvent{
				Type:      trace.EvtToolInvocationStarted,
				Timestamp: now,
				Payload: map[string]any{
					"id":     "call_1",
					"name":   "list_files",
					"detail": detail,
				},
			}},
			{Event: agentsession.TraceEvent{
				Type:      trace.EvtToolInvocationCompleted,
				Timestamp: now.Add(time.Second),
				Payload: map[string]any{
					"tool_call_id": "call_1",
					"name":         "list_files",
				},
			}},
		},
	})

	hydratedPlain := stripANSI(renderTranscriptCells(hydratedCells))
	livePlain := stripANSI(renderTranscriptCells(liveCells))
	require.Contains(t, hydratedPlain, "● List Files (1s)")
	require.Contains(t, hydratedPlain, "└ "+detail+" (1s)")
	require.Equal(t, livePlain, hydratedPlain)
}

func TestSessionTimelineToTranscriptCells_RendersHydratedSessionMessagesLikeLiveTrace(t *testing.T) {
	now := time.Date(2026, 5, 18, 15, 0, 0, 0, time.UTC)
	detail := "session_messages(anchor_message_id=42 before=2 after=3 max_chars=1200)"
	hydratedCells := sessionTimelineToTranscriptCells(client.SessionTimeline{
		Messages: []agentapi.SessionTimelineMessage{
			{Message: morphmsg.Message{
				Role: morphmsg.RoleAssistant,
				ToolCalls: []morphmsg.ToolCall{{
					ID:    "call_1",
					Name:  "session_messages",
					Input: `{"anchor_message_id":42,"before":2,"after":3,"max_chars":1200}`,
				}},
				CreatedAt: now,
			}},
			{Message: morphmsg.Message{
				Role:       morphmsg.RoleTool,
				Name:       "session_messages",
				ToolCallID: "call_1",
				Content:    `{"messages":[]}`,
				CreatedAt:  now.Add(time.Second),
			}},
		},
	})
	liveCells := sessionTimelineToTranscriptCells(client.SessionTimeline{
		TraceEvents: []agentapi.SessionTimelineTraceEvent{
			{Event: agentsession.TraceEvent{
				Type:      trace.EvtToolInvocationStarted,
				Timestamp: now,
				Payload: map[string]any{
					"id":     "call_1",
					"name":   "session_messages",
					"detail": detail,
				},
			}},
			{Event: agentsession.TraceEvent{
				Type:      trace.EvtToolInvocationCompleted,
				Timestamp: now.Add(time.Second),
				Payload: map[string]any{
					"tool_call_id": "call_1",
					"name":         "session_messages",
				},
			}},
		},
	})

	hydratedPlain := stripANSI(renderTranscriptCells(hydratedCells))
	livePlain := stripANSI(renderTranscriptCells(liveCells))
	require.Contains(t, hydratedPlain, "● Fetched Session Messages (1s)")
	require.NotContains(t, hydratedPlain, "└")
	require.NotContains(t, hydratedPlain, detail)
	require.Equal(t, livePlain, hydratedPlain)
}

func TestSessionTimelineToTranscriptCells_RendersHydratedFileToolsLikeLiveTrace(t *testing.T) {
	now := time.Date(2026, 5, 18, 15, 0, 0, 0, time.UTC)
	hydratedCells := sessionTimelineToTranscriptCells(client.SessionTimeline{
		Messages: []agentapi.SessionTimelineMessage{
			{Message: morphmsg.Message{
				Role: morphmsg.RoleAssistant,
				ToolCalls: []morphmsg.ToolCall{
					{
						ID:    "call_1",
						Name:  "write_file",
						Input: `{"path":"file.txt","content":"SECRET=example"}`,
					},
					{
						ID:    "call_2",
						Name:  "patch",
						Input: `{"patch":"--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new\n"}`,
					},
					{
						ID:    "call_3",
						Name:  "read_file",
						Input: `{"path":"file.txt"}`,
					},
				},
				CreatedAt: now,
			}},
			{Message: morphmsg.Message{
				Role:       morphmsg.RoleTool,
				Name:       "write_file",
				ToolCallID: "call_1",
				Content:    `{"output":"done"}`,
				CreatedAt:  now.Add(30 * time.Second),
			}},
			{Message: morphmsg.Message{
				Role:       morphmsg.RoleTool,
				Name:       "patch",
				ToolCallID: "call_2",
				Content:    `{"output":"done"}`,
				CreatedAt:  now.Add(30 * time.Second),
			}},
			{Message: morphmsg.Message{
				Role:       morphmsg.RoleTool,
				Name:       "read_file",
				ToolCallID: "call_3",
				Content:    `{"output":"done"}`,
				CreatedAt:  now.Add(30 * time.Second),
			}},
		},
	})
	liveCells := sessionTimelineToTranscriptCells(client.SessionTimeline{
		TraceEvents: []agentapi.SessionTimelineTraceEvent{
			{Event: agentsession.TraceEvent{
				Type:      trace.EvtToolInvocationStarted,
				Timestamp: now,
				Payload: map[string]any{
					"id":     "call_1",
					"name":   "write_file",
					"detail": "write_file file.txt",
				},
			}},
			{Event: agentsession.TraceEvent{
				Type:      trace.EvtToolInvocationCompleted,
				Timestamp: now.Add(30 * time.Second),
				Payload: map[string]any{
					"tool_call_id": "call_1",
					"name":         "write_file",
				},
			}},
			{Event: agentsession.TraceEvent{
				Type:      trace.EvtToolInvocationStarted,
				Timestamp: now.Add(31 * time.Second),
				Payload: map[string]any{
					"id":     "call_2",
					"name":   "patch",
					"detail": "patch file.txt +1 -1",
				},
			}},
			{Event: agentsession.TraceEvent{
				Type:      trace.EvtToolInvocationCompleted,
				Timestamp: now.Add(61 * time.Second),
				Payload: map[string]any{
					"tool_call_id": "call_2",
					"name":         "patch",
				},
			}},
			{Event: agentsession.TraceEvent{
				Type:      trace.EvtToolInvocationStarted,
				Timestamp: now.Add(62 * time.Second),
				Payload: map[string]any{
					"id":     "call_3",
					"name":   "read_file",
					"detail": "read_file file.txt",
				},
			}},
			{Event: agentsession.TraceEvent{
				Type:      trace.EvtToolInvocationCompleted,
				Timestamp: now.Add(92 * time.Second),
				Payload: map[string]any{
					"tool_call_id": "call_3",
					"name":         "read_file",
				},
			}},
		},
	})

	hydratedPlain := stripANSI(renderTranscriptCells(hydratedCells))
	livePlain := stripANSI(renderTranscriptCells(liveCells))
	require.Contains(t, hydratedPlain, "└ write_file file.txt (30s)")
	require.Contains(t, hydratedPlain, "└ patch file.txt +1 -1 (30s)")
	require.Contains(t, hydratedPlain, "└ read_file file.txt (30s)")
	require.NotContains(t, hydratedPlain, "SECRET=example")
	require.Equal(t, livePlain, hydratedPlain)
}
