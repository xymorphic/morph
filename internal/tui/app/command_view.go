package tui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	rpcclient "github.com/wandxy/morph/internal/rpc/client"
	storage "github.com/wandxy/morph/internal/state/core"
	agentsession "github.com/wandxy/morph/pkg/agent/session"
	"github.com/wandxy/morph/pkg/str"
)

const (
	commandViewMaxHeight    = 16
	commandViewChromeHeight = 2

	commandViewKindArchive        = "archive"
	commandViewKindChats          = "chats"
	commandViewKindModels         = "models"
	commandViewKindApproval       = "permission-approval"
	commandViewKindPermissions    = "permissions"
	commandViewKindEffort         = "effort"
	commandViewKindProviderAPIKey = "provider-api-key"
	commandViewKindProviders      = "providers"
	commandViewKindArtifactSave   = "artifact-save"
)

type commandViewPayload struct {
	TitleIcon        string
	TitleLeft        string
	TitleSubtext     string
	TitleRight       string
	AccentColor      string
	TitleRightColor  string
	Content          string
	Kind             string
	Chats            []storage.Session
	Models           []rpcclient.ModelOption
	Providers        []rpcclient.ProviderOption
	ModelProvider    string
	ModelAuthType    string
	PendingModelID   string
	PendingModelAPI  string
	EffortSessionID  string
	EffortModel      agentsession.ReasoningModelTuple
	Efforts          []agentsession.ReasoningEffort
	EffortReasoning  bool
	EffortAdjustable bool
}

type commandViewSelectionAutoScrollTickMsg struct{}

func (m model) isCommandViewVisible() bool {
	return m.commandView.Visible
}

func (m *model) showCommandView(payload commandViewPayload) {
	m.applyAction(showCommandViewAction(payload))
	m.resize()
}

func (m model) hideCommandView() model {
	m.applyAction(hideCommandViewAction{})
	m.resize()

	return m
}

func (m *model) resizeCommandViewIfHeightChanged(previousHeight int) {
	if m.getCommandViewHeight() != previousHeight {
		m.resize()
	}
}

func (m model) getCommandViewHeight() int {
	contentHeight := m.getCommandViewNaturalContentHeight()
	available := max(m.height-transcriptComposerGapHeight-1, 1)

	return min(min(contentHeight+commandViewChromeHeight, commandViewMaxHeight), available)
}

func (m model) getCommandViewNaturalContentHeight() int {
	switch {
	case m.isSessionListCommandView():
		return max(len(m.commandView.Chats), 1)
	case m.isModelsCommandView():
		if len(m.commandView.Models) == 0 {
			return 1
		}
		return lipgloss.Height(m.renderModelFilterBlock(m.getCommandViewContentWidth())) +
			max(len(m.filteredCommandModels()), 1)
	case m.isProvidersCommandView():
		return max(len(m.commandView.Providers), 1)
	case m.isPermissionApprovalCommandView():
		message, ok := m.pendingApprovalMessages[m.pendingApprovalID]
		if !ok {
			return 1
		}
		return max(len(getPermissionApprovalOptions(message)), 1)
	case m.isPermissionsCommandView():
		return max(len(permissionPresetOptions), 1)
	case m.isEffortCommandView():
		if detail := getEffortCommandUnavailableDetail(m.reasoning); detail != "" {
			view := newCommandViewContentViewport(commandViewContent{
				Text: detail, Width: m.getCommandViewContentWidth(), Height: 1,
			})
			return max(view.TotalLineCount(), 1)
		}
		return max(len(m.commandView.Efforts), 1)
	case m.isProviderAPIKeyCommandView(), m.isBrowserArtifactSaveCommandView():
		return 1
	default:
		view := m.newCommandViewContentViewport(commandViewContent{
			Text: m.renderCommandViewContentText(), Width: m.getCommandViewContentWidth(), Height: 1,
		})
		return max(view.TotalLineCount(), 1)
	}
}

func (m model) renderCommandView() string {
	frame := m.getCommandViewFrame()
	content := frame.Content
	if m.commandViewSelection.active {
		start, end := m.commandViewSelection.offsetBounds()
		content = renderCommandViewLines(commandViewContent{
			Text: highlightTranscriptSelection(
				m.commandViewSelection.content,
				start,
				end,
				lipgloss.NewStyle().Reverse(true),
			),
			Width:  m.getCommandViewContentWidth(),
			Height: m.getCommandViewContentHeight(),
			Offset: m.commandViewOffset,
		})
	}

	body := lipgloss.JoinVertical(lipgloss.Left, frame.Title, content)

	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderTop(true).
		BorderRight(true).
		BorderBottom(false).
		BorderLeft(true).
		BorderForeground(lipgloss.Color(frame.BorderColor)).
		Padding(0, 1).
		Width(frame.Width).
		Height(m.getCommandViewHeight()).
		Render(body)
}

func (m model) getCommandViewFrame() commandViewFrame {
	height := m.getCommandViewHeight()
	width := getInputBoxWidth(m.getMainPaneWidth())
	contentWidth := max(width-4, 1)
	contentHeight := m.getCommandViewContentHeight()
	accentColorValue := str.String(m.commandView.AccentColor)
	accentColor := accentColorValue.Trim()
	if accentColor == "" {
		accentColor = defaultTUITheme.NoticeForeground
	}
	titleRightColorValue := str.String(m.commandView.TitleRightColor)
	rightColor := titleRightColorValue.Trim()
	if rightColor == "" {
		rightColor = defaultTUITheme.MutedText
	}

	content := renderCommandViewContent(commandViewContent{
		Text:   m.renderCommandViewContentText(),
		Width:  contentWidth,
		Height: contentHeight,
		Offset: m.commandViewOffset,
	})
	if m.isSessionListCommandView() {
		content = m.renderSessionListCommandViewContent(commandViewContent{
			Width:  contentWidth,
			Height: contentHeight,
			Offset: m.commandViewOffset,
		})
	}
	if m.isModelsCommandView() {
		content = m.renderModelsCommandViewContent(commandViewContent{
			Width:  contentWidth,
			Height: contentHeight,
			Offset: m.commandViewOffset,
		})
	}
	if m.isProvidersCommandView() {
		content = m.renderProvidersCommandViewContent(commandViewContent{
			Width:  contentWidth,
			Height: contentHeight,
			Offset: m.commandViewOffset,
		})
	}
	if m.isPermissionApprovalCommandView() {
		content = m.renderPermissionApprovalCommandViewContent(commandViewContent{
			Width:  contentWidth,
			Height: contentHeight,
			Offset: m.commandViewOffset,
		})
	}
	if m.isPermissionsCommandView() {
		content = m.renderPermissionsCommandViewContent(commandViewContent{
			Width:  contentWidth,
			Height: contentHeight,
			Offset: m.commandViewOffset,
		})
	}
	if m.isEffortCommandView() {
		content = m.renderEffortCommandViewContent(commandViewContent{
			Width: contentWidth, Height: contentHeight, Offset: m.commandViewOffset,
		})
	}
	if m.isProviderAPIKeyCommandView() {
		content = m.renderProviderAPIKeyCommandViewContent(commandViewContent{
			Width:  contentWidth,
			Height: contentHeight,
		})
	}
	if m.isBrowserArtifactSaveCommandView() {
		content = m.renderBrowserArtifactSaveCommandViewContent(commandViewContent{
			Width: contentWidth, Height: contentHeight,
		})
	}

	return commandViewFrame{
		Width:       width,
		Height:      height,
		AccentColor: accentColor,
		BorderColor: defaultTUITheme.InputFrameBorder,
		Title: renderCommandViewTitle(commandViewTitle{
			Icon:       m.commandView.TitleIcon,
			Left:       defaultCommandViewTitle(m.commandView.TitleLeft),
			Subtext:    m.commandView.TitleSubtext,
			Right:      m.commandView.TitleRight,
			Width:      contentWidth,
			Accent:     accentColor,
			Muted:      defaultTUITheme.MutedText,
			RightColor: rightColor,
		}),
		Content: content,
	}
}

type commandViewFrame struct {
	Width       int
	Height      int
	AccentColor string
	BorderColor string
	Title       string
	Content     string
}

type commandViewTitle struct {
	Icon       string
	Left       string
	Subtext    string
	Right      string
	Width      int
	Accent     string
	Muted      string
	RightColor string
}

type commandViewContent struct {
	Text   string
	Width  int
	Height int
	Offset int
}

func renderCommandViewTitle(title commandViewTitle) string {
	leftValue := str.String(title.Left)
	leftText := leftValue.Trim()
	iconValue := str.String(title.Icon)
	if icon := iconValue.Trim(); icon != "" {
		leftText = icon + " " + leftText
	}
	left := lipgloss.NewStyle().
		Inline(true).
		Foreground(lipgloss.Color(title.Accent)).
		Render(leftText)
	subtextValue := str.String(title.Subtext)
	subtext := subtextValue.Trim()
	if subtext != "" {
		left += lipgloss.NewStyle().
			Inline(true).
			Foreground(lipgloss.Color(title.Muted)).
			Render(" - " + subtext)
	}
	rightValue := str.String(title.Right)
	right := rightValue.Trim()
	if right != "" {
		right = lipgloss.NewStyle().
			Inline(true).
			Foreground(lipgloss.Color(title.RightColor)).
			Render(right)
	}

	return spaceBetweenCommandViewTitle(left, right, max(title.Width, 1))
}

func renderCommandViewContent(content commandViewContent) string {
	view := newCommandViewContentViewport(content)

	return view.View()
}

func (m model) newCommandViewContentViewport(content commandViewContent) viewport.Model {
	return newCommandViewContentViewport(content)
}

func newCommandViewContentViewport(content commandViewContent) viewport.Model {
	textValue := str.String(content.Text)
	text := textValue.Trim()
	if text == "" {
		text = "No content available."
	}

	view := viewport.New(
		viewport.WithWidth(max(content.Width, 1)),
		viewport.WithHeight(max(content.Height, 1)),
	)
	view.SoftWrap = true
	view.SetContent(text)
	view.SetYOffset(max(content.Offset, 0))

	return view
}

func renderCommandViewLines(content commandViewContent) string {
	text := strings.TrimRight(content.Text, "\n")
	textValue2 := str.String(text)
	if textValue2.Trim() == "" {
		text = "No content available."
	}

	view := viewport.New(
		viewport.WithWidth(max(content.Width, 1)),
		viewport.WithHeight(max(content.Height, 1)),
	)
	view.SoftWrap = false
	view.SetContent(text)
	view.SetYOffset(max(content.Offset, 0))

	return view.View()
}

func spaceBetweenCommandViewTitle(left string, right string, width int) string {
	leftValue2 := str.String(left)
	left = leftValue2.Trim()
	rightValue2 := str.String(right)
	right = rightValue2.Trim()
	if right == "" {
		return left
	}
	if left == "" {
		return right
	}

	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap <= 0 {
		return left + renderCommandViewTitleSeparator() + right
	}

	return left + strings.Repeat(" ", gap) + right
}

func renderCommandViewTitleSeparator() string {
	return lipgloss.NewStyle().
		Inline(true).
		Foreground(lipgloss.Color(defaultTUITheme.MutedText)).
		Render(" · ")
}

func defaultCommandViewTitle(value string) string {
	valueText := str.String(value).Trim()
	if valueText != "" {
		return valueText
	}

	return "Command"
}

func (m *model) updateCommandView(msg tea.Msg) (tea.Model, tea.Cmd) {
	if !m.isCommandViewVisible() {
		return *m, nil
	}
	if m.isArchiveCommandView() {
		return m.updateArchiveCommandView(msg)
	}
	if m.isChatsCommandView() {
		return m.updateChatsCommandView(msg)
	}
	if m.isModelsCommandView() {
		return m.updateModelsCommandView(msg)
	}
	if m.isProvidersCommandView() {
		return m.updateProvidersCommandView(msg)
	}
	if m.isPermissionApprovalCommandView() {
		return m.updatePermissionApprovalCommandView(msg)
	}
	if m.isPermissionsCommandView() {
		return m.updatePermissionsCommandView(msg)
	}
	if m.isEffortCommandView() {
		return m.updateEffortCommandView(msg)
	}
	if m.isProviderAPIKeyCommandView() {
		return m.updateProviderAPIKeyCommandView(msg)
	}
	if m.isBrowserArtifactSaveCommandView() {
		return m.updateBrowserArtifactSaveCommandView(msg)
	}

	offset := m.commandViewOffset
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.Key().Code {
		case tea.KeyUp:
			offset--
		case tea.KeyDown:
			offset++
		case tea.KeyPgUp:
			offset -= max(m.getCommandViewHeight()-3, 1)
		case tea.KeyPgDown:
			offset += max(m.getCommandViewHeight()-3, 1)
		case tea.KeyHome:
			offset = 0
		case tea.KeyEnd:
			offset = 1 << 30
		default:
			return *m, nil
		}
	case tea.MouseWheelMsg:
		switch msg.Mouse().Button {
		case tea.MouseWheelUp:
			offset--
		case tea.MouseWheelDown:
			offset++
		default:
			return *m, nil
		}
	default:
		return *m, nil
	}
	m.clearCommandViewSelection()
	m.commandViewOffset = m.clampCommandViewOffset(offset)

	return *m, nil
}

func (m *model) copyCommandView() tea.Cmd {
	text := m.commandViewPlainText()
	if text == "" {
		return m.setStatus("command view is empty")
	}

	return m.runEffect(copyTranscriptEffect{
		Text:   text,
		Status: "command view copied",
	})
}

func (m *model) startCommandViewSelection(msg tea.MouseClickMsg) bool {
	if msg.Button != tea.MouseLeft {
		return false
	}
	if !m.isMouseInCommandViewContent(msg.Mouse()) {
		return false
	}

	point, ok := m.commandViewSelectionPointFromMouse(msg.Mouse())
	if !ok {
		return false
	}

	m.commandViewSelection = commandViewSelection{
		active:   true,
		dragging: true,
		content:  m.renderCommandViewSelectionDocument(),
		start:    point,
		end:      point,
		mouse:    msg.Mouse(),
	}

	return true
}

func (m *model) updateCommandViewSelection(msg tea.MouseMotionMsg) (bool, tea.Cmd) {
	if !m.commandViewSelection.dragging {
		return false, nil
	}

	cmd := m.updateCommandViewSelectionForMouse(msg.Mouse())

	return true, cmd
}

func (m *model) finishCommandViewSelection(msg tea.MouseReleaseMsg) (bool, tea.Cmd) {
	if !m.commandViewSelection.dragging {
		return false, nil
	}

	m.updateCommandViewSelectionForMouse(msg.Mouse())
	m.commandViewSelection.dragging = false
	m.commandViewSelection.scroll = 0
	m.commandViewSelection.ticking = false

	text := m.selectedCommandViewText()
	textValue3 := str.String(text)
	if textValue3.Trim() == "" {
		m.clearCommandViewSelection()
		return true, nil
	}
	if err := writeClipboard(text); err != nil {
		return true, m.setStatus("copy failed")
	}

	return true, nil
}

func (m *model) updateCommandViewSelectionForMouse(mouse tea.Mouse) tea.Cmd {
	m.commandViewSelection.mouse = mouse
	m.commandViewSelection.scroll = m.commandViewSelectionScrollDirection(mouse)

	if m.commandViewSelection.scroll != 0 {
		m.commandViewOffset = m.clampCommandViewOffset(m.commandViewOffset + m.commandViewSelection.scroll)
	}
	if point, ok := m.commandViewSelectionPointFromMouseClamped(mouse); ok {
		m.commandViewSelection.end = point
	}
	if m.commandViewSelection.scroll == 0 || m.commandViewSelection.ticking {
		return nil
	}

	m.commandViewSelection.ticking = true
	return commandViewSelectionAutoScrollTickCmd()
}

func (m *model) updateCommandViewSelectionAutoScroll() (tea.Model, tea.Cmd) {
	if !m.commandViewSelection.dragging {
		m.commandViewSelection.scroll = 0
		m.commandViewSelection.ticking = false
		return *m, nil
	}

	m.commandViewSelection.ticking = false
	cmd := m.updateCommandViewSelectionForMouse(m.commandViewSelection.mouse)

	return *m, cmd
}

func (m *model) clearCommandViewSelection() {
	m.commandViewSelection = commandViewSelection{}
}

func (m model) selectedCommandViewText() string {
	if !m.commandViewSelection.active {
		return ""
	}

	document := newRenderedTranscriptDocument(m.commandViewSelection.content)
	start, end := m.commandViewSelection.offsetBounds()
	text := document.PlainRange(start, end)
	if text == "" {
		return ""
	}
	textValue4 := str.String(text)
	return compactTranscriptSelectionBlankLines(textValue4.Trim())
}

func (m model) commandViewSelectionPointFromMouse(mouse tea.Mouse) (transcriptSelectionPoint, bool) {
	if !m.isMouseInCommandViewContent(mouse) {
		return transcriptSelectionPoint{}, false
	}

	row := m.commandViewOffset + mouse.Y - m.getCommandViewContentTop()
	x := max(mouse.X-m.getCommandViewContentLeft(), 0)

	return m.commandViewSelectionPointFromVisibleLine(row, x)
}

func (m model) commandViewSelectionPointFromMouseClamped(mouse tea.Mouse) (transcriptSelectionPoint, bool) {
	lines := newRenderedTranscriptDocument(m.getCommandViewSelectionContent()).PlainLines()
	if len(lines) == 0 {
		return transcriptSelectionPoint{}, false
	}

	row := m.commandViewOffset + mouse.Y - m.getCommandViewContentTop()
	row = min(max(row, 0), len(lines)-1)
	x := max(mouse.X-m.getCommandViewContentLeft(), 0)

	return m.commandViewSelectionPointFromVisibleLine(row, x)
}

func (m model) commandViewSelectionPointFromVisibleLine(
	lineIndex int,
	x int,
) (transcriptSelectionPoint, bool) {
	document := newRenderedTranscriptDocument(m.getCommandViewSelectionContent())
	if lineIndex < 0 || lineIndex >= len(document.PlainLines()) {
		return transcriptSelectionPoint{}, false
	}

	return getTranscriptSelectionPointFromDocument(document, lineIndex, x), true
}

func (m model) getCommandViewSelectionContent() string {
	if m.commandViewSelection.content != "" {
		return m.commandViewSelection.content
	}

	return m.renderCommandViewSelectionDocument()
}

func (m model) renderCommandViewSelectionDocument() string {
	if m.isSessionListCommandView() {
		return m.renderSessionListCommandViewContent(commandViewContent{
			Width:  m.getCommandViewContentWidth(),
			Height: max(len(m.commandView.Chats), 1),
			Offset: 0,
		})
	}
	if m.isModelsCommandView() {
		return m.renderModelsCommandViewContent(commandViewContent{
			Width:  m.getCommandViewContentWidth(),
			Height: m.getCommandViewSelectionModelHeight(),
			Offset: 0,
		})
	}
	if m.isProvidersCommandView() {
		return m.renderProvidersCommandViewContent(commandViewContent{
			Width:  m.getCommandViewContentWidth(),
			Height: max(len(m.commandView.Providers), 1),
			Offset: 0,
		})
	}
	if m.isProviderAPIKeyCommandView() {
		return m.renderProviderAPIKeyCommandViewContent(commandViewContent{
			Width:  m.getCommandViewContentWidth(),
			Height: m.getCommandViewContentHeight(),
			Offset: 0,
		})
	}
	if m.isBrowserArtifactSaveCommandView() {
		return m.renderBrowserArtifactSaveCommandViewContent(commandViewContent{
			Width: m.getCommandViewContentWidth(), Height: m.getCommandViewContentHeight(),
		})
	}

	view := m.newCommandViewContentViewport(commandViewContent{
		Text:   m.renderCommandViewContentText(),
		Width:  m.getCommandViewContentWidth(),
		Height: 1,
		Offset: 0,
	})
	view.SetHeight(max(view.TotalLineCount(), 1))
	view.SetYOffset(0)

	return view.View()
}

func (m model) getCommandViewSelectionModelHeight() int {
	filterHeight := lipgloss.Height(m.renderModelFilterBlock(m.getCommandViewContentWidth()))
	modelHeight := max(len(m.filteredCommandModels()), 1)

	return filterHeight + modelHeight
}

func (m model) isMouseInCommandViewContent(mouse tea.Mouse) bool {
	if !m.isCommandViewVisible() {
		return false
	}

	row := mouse.Y - m.getCommandViewContentTop()
	if row < 0 || row >= m.getCommandViewContentHeight() {
		return false
	}

	col := mouse.X - m.getCommandViewContentLeft()
	return col >= 0 && col < m.getCommandViewContentWidth()
}

func (m model) getCommandViewContentTop() int {
	return m.getTUILayout(m.input.Height()).Composer.Y + 2
}

func (m model) getCommandViewContentLeft() int {
	return 2
}

func (m model) getCommandViewContentWidth() int {
	return max(getInputBoxWidth(m.getMainPaneWidth())-4, 1)
}

func (m model) getCommandViewContentHeight() int {
	return max(m.getCommandViewHeight()-commandViewChromeHeight, 1)
}

func (m model) commandViewSelectionScrollDirection(mouse tea.Mouse) int {
	row := mouse.Y - m.getCommandViewContentTop()
	switch {
	case row <= 0 && m.commandViewOffset > 0:
		return -1
	case row >= m.getCommandViewContentHeight()-1 &&
		m.commandViewOffset < m.getCommandViewMaxYOffset():
		return 1
	default:
		return 0
	}
}

func (m model) getCommandViewMaxYOffset() int {
	if m.isSessionListCommandView() {
		return max(len(m.commandView.Chats)-m.getCommandViewContentHeight(), 0)
	}
	if m.isModelsCommandView() {
		filterHeight := lipgloss.Height(m.renderModelFilterBlock(m.getCommandViewContentWidth()))
		visibleModels := max(m.getCommandViewContentHeight()-filterHeight, 1)
		return max(len(m.filteredCommandModels())-visibleModels, 0)
	}
	if m.isProvidersCommandView() {
		return max(len(m.commandView.Providers)-m.getCommandViewContentHeight(), 0)
	}
	if m.isPermissionApprovalCommandView() {
		message, ok := m.pendingApprovalMessages[m.pendingApprovalID]
		if !ok {
			return 0
		}
		return max(len(getPermissionApprovalOptions(message))-m.getCommandViewContentHeight(), 0)
	}
	if m.isPermissionsCommandView() {
		return max(len(permissionPresetOptions)-m.getCommandViewContentHeight(), 0)
	}
	if m.isEffortCommandView() && getEffortCommandUnavailableDetail(m.reasoning) == "" {
		return max(len(m.commandView.Efforts)-m.getCommandViewContentHeight(), 0)
	}

	view := m.newCommandViewContentViewport(commandViewContent{
		Text:   m.renderCommandViewContentText(),
		Width:  m.getCommandViewContentWidth(),
		Height: m.getCommandViewContentHeight(),
	})

	return max(view.TotalLineCount()-m.getCommandViewContentHeight(), 0)
}

func (m model) clampCommandViewOffset(offset int) int {
	return min(max(offset, 0), m.getCommandViewMaxYOffset())
}

func (m model) renderCommandViewContentText() string {
	return renderMarkdownForTranscript(m.commandView.Content, m.getCommandViewContentWidth())
}

func (m model) commandViewPlainText() string {
	document := newRenderedTranscriptDocument(m.renderCommandViewSelectionDocument())
	plainTextValue := str.String(document.PlainText)
	return plainTextValue.Trim()
}

func (s commandViewSelection) offsetBounds() (int, int) {
	if s.start.offset <= s.end.offset {
		return s.start.offset, s.end.offset
	}

	return s.end.offset, s.start.offset
}

func commandViewSelectionAutoScrollTickCmd() tea.Cmd {
	return tea.Tick(transcriptSelectionAutoScrollInterval, func(_ time.Time) tea.Msg {
		return commandViewSelectionAutoScrollTickMsg{}
	})
}
