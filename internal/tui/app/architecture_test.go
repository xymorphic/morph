package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/str"
)

func TestTUIAction_SetViewportSizeBoundsValues(t *testing.T) {
	state := newTUIState(nil, false)

	setViewportSizeAction{Width: 0, Height: -10}.apply(&state)

	require.Equal(t, 1, state.width)
	require.Equal(t, 1, state.height)
}

func TestTUIAction_TranscriptCellActions(t *testing.T) {
	state := newTUIState(nil, false)

	appendTranscriptCellAction{Cell: userTranscriptCell{text: "hello"}}.apply(&state)
	setLiveTranscriptCellAction{Cell: assistantTranscriptCell{text: "world"}}.apply(&state)

	require.Len(t, state.messages, 1)
	require.Equal(t, transcriptCellUser, state.messages[0].Kind())
	require.Equal(t, transcriptCellAssistant, state.live.Kind())

	clearTranscriptAction{}.apply(&state)

	require.Empty(t, state.messages)
	require.Nil(t, state.live)
	require.False(t, state.showIntro)
	require.Equal(t, -1, state.reasoningMessageIndex)
}

func TestTUIAction_ReplaceTranscriptCell(t *testing.T) {
	state := newTUIState(nil, false)
	appendTranscriptCellAction{Cell: userTranscriptCell{text: "before"}}.apply(&state)

	replaceTranscriptCellAction{
		Index: 0,
		Cell:  assistantTranscriptCell{text: "after"},
	}.apply(&state)

	require.Len(t, state.messages, 1)
	require.Equal(t, transcriptCellAssistant, state.messages[0].Kind())
	require.Equal(t, "Morph: after", state.messages[0].PlainText())
}

func TestTUIAction_SetSessionTitleFallsBackToDefault(t *testing.T) {
	state := newTUIState(nil, false)

	setSessionTitleAction{Title: "  Project Notes  "}.apply(&state)
	require.Equal(t, "Project Notes", state.sessionTitle)

	setSessionTitleAction{}.apply(&state)
	require.Equal(t, defaultSessionTitle, state.sessionTitle)
}

func TestTUILayout_ComputesStableRegions(t *testing.T) {
	layout := getTUILayout(100, 30, 2)
	mainWidth := getMainPaneWidth(100)

	require.Equal(t, 0, layout.Transcript.Y)
	require.Equal(t, mainWidth, layout.Composer.Width)
	require.Equal(t, layout.Transcript.Height, layout.JumpToBottom.Y)
	require.Equal(t, layout.JumpToBottom.Y+transcriptComposerGapHeight, layout.Composer.Y)
	require.Equal(t, getPanelContentWidth(mainWidth), layout.PanelContentWidth)
}

func TestRenderedTranscriptDocument_StripsAnsiAndTracksOffsets(t *testing.T) {
	document := newRenderedTranscriptDocument("\x1b[31mHello\x1b[0m\nWorld")

	require.Equal(t, "Hello\nWorld", document.PlainText)
	require.Len(t, document.Lines, 2)
	require.Equal(t, "Hello", document.Lines[0].PlainText)
	require.Equal(t, 0, document.Lines[0].StartOffset)
	require.Equal(t, 5, document.Lines[0].EndOffset)
	require.Equal(t, "World", document.Lines[1].PlainText)
	require.Equal(t, 6, document.Lines[1].StartOffset)
	require.Equal(t, []string{"Hello", "World"}, document.PlainLines())
	require.Equal(t, "lo\nWo", document.PlainRange(3, 8))

	line, ok := document.Line(1)
	require.True(t, ok)
	require.Equal(t, "World", line.PlainText)

	_, ok = document.Line(2)
	require.False(t, ok)
}

func TestTranscriptRenderer_GroupsAdjacentToolCells(t *testing.T) {
	rendered := defaultTranscriptRenderer.RenderCells(
		[]transcriptCell{
			toolTranscriptTestCell("call_1", "read_file", "read_file a.txt"),
			toolTranscriptTestCell("call_2", "read_file", "read_file b.txt"),
		},
		transcriptRenderContext{Width: 80},
	)
	plain := stripANSI(rendered)

	require.Contains(t, plain, "Read")
	require.Contains(t, plain, "├ read_file a.txt")
	require.Contains(t, plain, "└ read_file b.txt")
	require.Equal(t, 1, strings.Count(plain, "Read"))
}

func TestTranscriptRenderer_UsesRenderContextTimeForRunningTools(t *testing.T) {
	startedAt := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	rendered := defaultTranscriptRenderer.RenderCells(
		[]transcriptCell{
			toolTranscriptTestCellWithTiming(
				"call_1",
				"read_file",
				"read_file a.txt",
				startedAt,
				time.Time{},
				false,
			),
		},
		transcriptRenderContext{Width: 80, Now: startedAt.Add(3 * time.Second)},
	)

	require.Contains(t, stripANSI(rendered), "read_file a.txt (3s)")
}

func TestTranscriptRenderer_VisualSnapshot(t *testing.T) {
	startedAt := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	rendered := defaultTranscriptRenderer.RenderCells(
		[]transcriptCell{
			userTranscriptCell{text: "hello"},
			thoughtTranscriptCell{duration: 2 * time.Second},
			assistantTranscriptCell{text: "**Done**"},
			toolTranscriptTestCellWithTiming(
				"call_1",
				"read_file",
				"read_file notes.txt",
				startedAt,
				startedAt.Add(3*time.Second),
				true,
			),
		},
		transcriptRenderContext{Width: 48, Now: startedAt.Add(3 * time.Second)},
	)

	require.Equal(t, strings.Join([]string{
		strings.Repeat("▄", 48),
		"❯ hello",
		strings.Repeat("▀", 48),
		"",
		"Thought for 2s",
		"",
		assistantTranscriptIndicatorGlyph + "Done",
		"",
		"● Read (3s)",
		"  └ read_file notes.txt (3s)",
	}, "\n"), trimRightSnapshotLines(stripANSI(rendered)))
}

func TestTranscriptRenderer_RendersCellsWithoutCellRenderMethods(t *testing.T) {
	original := defaultTranscriptRenderer
	t.Cleanup(func() {
		defaultTranscriptRenderer = original
	})
	renderer := recordingTranscriptRenderer{output: "rendered"}
	defaultTranscriptRenderer = &renderer

	output := defaultTranscriptRenderer.RenderCell(
		assistantTranscriptCell{text: "hello"},
		transcriptRenderContext{Width: 20},
	)

	require.Equal(t, "rendered", output)
	require.Equal(t, transcriptCellAssistant, renderer.kind)
	require.Equal(t, 20, renderer.ctx.Width)
}

func TestModelView_FullScreenLayoutSnapshot(t *testing.T) {
	runModel := newModel()
	runModel.width = 64
	runModel.height = 18
	runModel.messages = []transcriptCell{
		userTranscriptCell{text: "hello"},
		assistantTranscriptCell{text: "Hi"},
	}
	runModel.showIntro = false
	runModel.resize()
	runModel.setTranscriptContent()

	view := stripANSI(runModel.View().Content)

	require.Contains(t, view, "░████████  Morph")
	require.Contains(t, view, "❯ hello")
	require.Contains(t, view, "Hi")
	require.Contains(t, view, "Ask Morph...")
	require.NotContains(t, view, "minimax-m2.7")
	require.Contains(t, view, statusReadySuffix)
	require.Less(t, strings.Index(view, "❯ hello"), strings.Index(view, "Ask Morph..."))
	require.Less(t, strings.Index(view, "Ask Morph..."), strings.Index(view, statusReadySuffix))
}

func TestBubbleTeaAdapter_UpdatesInputOnlyForPlainTyping(t *testing.T) {
	runModel := newModel()
	for i := 0; i < 20; i++ {
		runModel.messages = append(runModel.messages, assistantTranscriptCell{text: strings.Repeat("line\n", 4)})
	}
	runModel.resize()
	runModel.setTranscriptContent()
	runModel.transcript.SetYOffset(max(runModel.transcript.TotalLineCount()-2, 0))
	offset := runModel.transcript.YOffset()

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	require.NotNil(t, updated)
	require.NotNil(t, cmd)
	next := updated.(model)

	require.Equal(t, offset, next.transcript.YOffset())
	require.Equal(t, "a", next.input.Value())
}

func TestTranscriptCellInterface_IsPureData(t *testing.T) {
	var cell transcriptCell = assistantTranscriptCell{text: "hello"}
	_, hasRender := any(cell).(interface {
		Render(transcriptRenderContext) string
	})

	require.False(t, hasRender)
}

func TestChromePanelData_SeparatesModelStateFromRendering(t *testing.T) {
	runModel := newModel()
	runModel.width = 180
	runModel.modelName = "openai/gpt-4o-mini"

	panel := getHeaderPanel(runModel, runModel.width)

	require.Equal(t, 180, panel.Width)
	require.Equal(t, getHeaderBrandText(runModel.runtimeInfo), panel.Banner)
	require.True(t, panel.ShowInfo)
	require.Equal(t, "Welcome, ", panel.Notice.LeftLead)
	require.Equal(t, "/changelog", panel.Notice.Link)
	require.Contains(t, headerInfoRowsToPlainText(panel.InfoRows), "model=gpt-4o-mini")
}

func TestChromeRenderer_RendersHeaderAndNoticeFromPanelData(t *testing.T) {
	runModel := newModel()
	panel := getHeaderPanel(runModel, 96)
	renderer := lipglossChromeRenderer{}

	header := stripANSI(renderer.RenderHeader(panel))
	notice := stripANSI(renderer.RenderNoticeBar(panel.Notice))

	require.Contains(t, header, "Welcome, Kennedy")
	require.Contains(t, header, "Use /changelog to see what changed")
	require.Contains(t, header, "░██")
	require.Contains(t, notice, "Welcome, Kennedy")
	require.Equal(t, 96, lipgloss.Width(strings.Split(notice, "\n")[0]))
}

func TestBottomStatusPanelData_SeparatesModelStateFromRendering(t *testing.T) {
	runModel := newModel()
	runModel.width = 100
	runModel.responding = true
	runModel.thinkingComposerFrame = 2
	runModel.sessionTitle = "Project Planning"
	runModel.exitAt = time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)

	panel := getBottomStatusPanel(getInputBoxWidth(runModel.width), runModel)

	require.Equal(t, getInputBoxWidth(runModel.width), panel.Width)
	require.Equal(t, getPanelContentWidth(panel.Width), panel.ContentWidth)
	require.Empty(t, panel.ModelName)
	require.Equal(t, statusCancelSuffix, panel.Status)
	require.True(t, panel.Thinking)
	require.Equal(t, 2, panel.ThinkingFrame)
	require.True(t, panel.ExitConfirmation)
}

func TestBottomStatusPanelRenderer_RendersFromPanelData(t *testing.T) {
	renderer := lipglossBottomStatusPanelRenderer{}
	panel := bottomStatusPanel{
		Width:             80,
		HorizontalPadding: 1,
		ContentWidth:      78,
		ModelName:         "GPT 5.5",
		Status:            statusReadySuffix,
		Context:           "60,000 used",
		Thinking:          true,
		ThinkingFrame:     1,
	}

	content := stripANSI(renderer.Render(panel))

	require.Equal(t, 80, lipgloss.Width(content))
	require.Contains(t, content, "Thinking")
	require.Contains(t, content, "GPT 5.5")
	require.Contains(t, content, "60,000 used")
	require.Less(t, strings.Index(content, "Thinking"), strings.Index(content, "GPT 5.5"))
}

func trimRightSnapshotLines(value string) string {
	valueText := str.String(value)
	lines := strings.Split(valueText.Trim(), "\n")
	for index, line := range lines {
		lines[index] = strings.TrimRight(line, " ")
	}

	return strings.Join(lines, "\n")
}

func headerInfoRowsToPlainText(rows []headerInfoRow) string {
	parts := make([]string, 0, len(rows))
	for _, row := range rows {
		parts = append(parts, row.key+"="+row.value)
	}

	return strings.Join(parts, "\n")
}

func TestTranscriptCellFactory_BuildsToolCellsSharedByLiveAndHydratedPaths(t *testing.T) {
	startedAt := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	completedAt := startedAt.Add(5 * time.Second)
	toolCall := morphmsg.ToolCall{
		ID:    "call_1",
		Name:  "list_files",
		Input: `{"path":".","recursive":false,"include_hidden":false,"max_entries":50}`,
	}
	started, ok := toolInvocationStartedMsgFromMessageToolCall(toolCall, startedAt)
	require.True(t, ok)
	completed, ok := toolInvocationCompletedMsgFromMessage(morphmsg.Message{
		Role:       morphmsg.RoleTool,
		Name:       "list_files",
		ToolCallID: "call_1",
		CreatedAt:  completedAt,
	}, completedAt)
	require.True(t, ok)

	liveCells := []transcriptCell{
		defaultTranscriptCellFactory.FromTUIMessage(started),
		defaultTranscriptCellFactory.FromTUIMessage(completed),
	}
	hydratedCells := []transcriptCell{
		defaultTranscriptCellFactory.FromTimelineMessage(
			morphmsg.Message{
				Role:       morphmsg.RoleTool,
				Name:       "list_files",
				ToolCallID: "call_1",
				Content:    `{"output":"done"}`,
				CreatedAt:  completedAt,
			},
			map[string]timelineToolCallDetail{
				"call_1": {
					detail:    started.Detail,
					startedAt: started.StartedAt,
				},
			},
		),
	}

	livePlain := stripANSI(renderTranscriptCells(liveCells))
	hydratedPlain := stripANSI(renderTranscriptCells(hydratedCells))
	require.Equal(t, hydratedPlain, livePlain)
	require.Contains(t, livePlain, "list_files(include_hidden=false max_entries=50 path=. recursive=false) (5s)")
}

func TestTranscriptCellFactory_PreservesFailedToolOutcomeWhenHydrated(t *testing.T) {
	cell := defaultTranscriptCellFactory.FromTimelineMessage(
		morphmsg.Message{
			Role:       morphmsg.RoleTool,
			Name:       "browser",
			ToolCallID: "call_1",
			Content:    `{"error":{"code":"permission_denied","message":"approval denied"}}`,
		},
		map[string]timelineToolCallDetail{
			"call_1": {detail: "start:Profile default"},
		},
	)

	toolCell, ok := cell.(toolTranscriptCell)
	require.True(t, ok)
	require.False(t, toolCell.completed)
	require.Equal(t, toolTranscriptTerminalStatusFailed, toolCell.terminalStatus)
	rendered := stripANSI(renderTranscriptCells([]transcriptCell{cell}))
	require.Contains(t, rendered, "Failed to Start Browser")
	require.Contains(t, rendered, "└ Profile default · Approval denied")
}

func TestModel_HandleAppEventRoutesProductBehavior(t *testing.T) {
	runModel := newModel()

	updated, cmd := runModel.handleAppEvent(viewportResizedEvent{Width: 120, Height: 40})
	require.Nil(t, cmd)
	require.Equal(t, 120, updated.width)
	require.Equal(t, 40, updated.height)

	updated, cmd = updated.handleAppEvent(applyTUIMessageEvent{
		Message: assistantResponseCompletedMsg{Text: "hello"},
	})
	require.Nil(t, cmd)
	require.Equal(t, []string{"Morph: hello"}, transcriptCellPlainTexts(updated.messages))
}

type recordingTranscriptRenderer struct {
	output string
	kind   transcriptCellKind
	ctx    transcriptRenderContext
}

func (r *recordingTranscriptRenderer) RenderCell(cell transcriptCell, ctx transcriptRenderContext) string {
	r.kind = cell.Kind()
	r.ctx = ctx

	return r.output
}

func (r *recordingTranscriptRenderer) RenderCells(cells []transcriptCell, ctx transcriptRenderContext) string {
	if len(cells) == 0 {
		return ""
	}

	return r.RenderCell(cells[0], ctx)
}
