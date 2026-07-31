package tui

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type transcriptLayoutState struct {
	width             int
	height            int
	inputHeight       int
	commandView       bool
	commandViewHeight int
	namePrompt        bool
	profileSetup      bool
}

const (
	inputFrameHorizontalPadding = 1
	inputFrameVerticalPadding   = 0
	inputFrameBorderWidth       = 2
	inputFrameChromeHeight      = inputFrameBorderWidth + inputFrameVerticalPadding*2
	bottomStatusPanelHeight     = 1
	minInputHeight              = 1
	inputPrompt                 = "❯ "
)

// newInputComposer creates the multiline prompt editor.
func newInputComposer() textarea.Model {
	input := textarea.New()
	input.Placeholder = "Ask Morph..."
	input.SetPromptFunc(lipgloss.Width(inputPrompt), renderInputPrompt)
	input.ShowLineNumbers = false
	setInputTransparentStyles(&input)
	input.SetHeight(1)
	input.Focus()

	return input
}

// setInputTransparentStyles removes Bubble's default focused-line background.
func setInputTransparentStyles(input *textarea.Model) {
	styles := input.Styles()
	styles.Cursor.Blink = false
	styles.Focused.Base = styles.Focused.Base.UnsetBackground()
	styles.Focused.Text = styles.Focused.Text.UnsetBackground()
	styles.Focused.Placeholder = styles.Focused.Placeholder.UnsetBackground()
	styles.Focused.Prompt = styles.Focused.Prompt.UnsetBackground()
	styles.Focused.CursorLine = lipgloss.NewStyle()
	styles.Focused.EndOfBuffer = styles.Focused.EndOfBuffer.UnsetBackground()
	styles.Blurred.Base = styles.Blurred.Base.UnsetBackground()
	styles.Blurred.Text = styles.Blurred.Text.UnsetBackground()
	styles.Blurred.Placeholder = styles.Blurred.Placeholder.UnsetBackground()
	styles.Blurred.Prompt = styles.Blurred.Prompt.UnsetBackground()
	styles.Blurred.CursorLine = styles.Blurred.CursorLine.UnsetBackground()
	styles.Blurred.EndOfBuffer = styles.Blurred.EndOfBuffer.UnsetBackground()
	input.SetStyles(styles)
}

// renderInputPrompt shows the arrow only on the first visible row.
func renderInputPrompt(info textarea.PromptInfo) string {
	if info.LineNumber == 0 {
		return renderComposerInputPrompt()
	}

	return ""
}

func renderComposerInputPrompt() string {
	return lipgloss.NewStyle().
		Background(lipgloss.NoColor{}).
		Foreground(lipgloss.Color(defaultTUITheme.UserTranscriptPrompt)).
		Render(inputPrompt)
}

// renderInput draws the composer and its model/context/status row.
func (m model) renderInput() string {
	width := m.getMainPaneWidth()
	inputBox := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderTop(true).
		BorderRight(true).
		BorderBottom(true).
		BorderLeft(true).
		BorderForeground(lipgloss.Color(m.getInputFrameBorderColor())).
		Padding(inputFrameVerticalPadding, inputFrameHorizontalPadding).
		Width(getInputBoxWidth(width)).
		Render(m.input.View())

	parts := make([]string, 0, 3)
	if commandMenu := m.renderCommandMenu(); commandMenu != "" {
		parts = append(parts, commandMenu)
	}
	parts = append(parts, inputBox, m.renderBottomStatusPanel())

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m model) getInputFrameBorderColor() string {
	if !m.isThinkingComposerVisible() {
		return defaultTUITheme.InputFrameBorder
	}

	return getThinkingComposerBorderColor(m.thinkingComposerFrame)
}

// resize distributes terminal rows between transcript and composer.
func (m *model) resize() {
	if m.transcriptRenderSuppressed {
		m.transcriptResizePending = true
		return
	}
	m.transcriptResizes++

	wasAtBottom := m.isTranscriptAtAbsoluteBottom()

	width := m.getMainPaneWidth()
	m.input.SetWidth(getInputInnerWidth(width))
	if m.shouldShowNamePrompt() || m.shouldShowProfileModelSetup() {
		m.transcript.SetWidth(width)
		m.transcript.SetHeight(max(m.height, 1))
		m.transcriptLayout = m.getTranscriptLayoutState()
		if wasAtBottom {
			m.transcript.GotoBottom()
		}
		return
	}

	inputHeight := m.getInputHeight()
	layout := m.getTUILayout(inputHeight)

	m.input.SetHeight(inputHeight)
	m.transcript.SetWidth(layout.Composer.Width)
	m.transcript.SetHeight(layout.Transcript.Height)
	m.transcriptLayout = m.getTranscriptLayoutState()
	if wasAtBottom {
		m.transcript.GotoBottom()
	}
}

func (m *model) resizeTranscriptIfLayoutChanged() {
	if m.transcriptLayout == m.getTranscriptLayoutState() {
		return
	}

	m.resize()
}

func (m model) getTranscriptLayoutState() transcriptLayoutState {
	return transcriptLayoutState{
		width:             m.width,
		height:            m.height,
		inputHeight:       m.getInputHeight(),
		commandView:       m.isCommandViewVisible(),
		commandViewHeight: m.getCommandViewHeight(),
		namePrompt:        m.shouldShowNamePrompt(),
		profileSetup:      m.shouldShowProfileModelSetup(),
	}
}

// getInputHeight returns the visible composer height constrained by the screen.
func (m model) getInputHeight() int {
	return m.getInputHeightForValue(m.input.Value())
}

func (m model) getInputHeightForValue(value string) int {
	availableHeight := max(m.height-m.getInputChromeHeightForValue(value)-1, minInputHeight)
	contentWidth := m.input.Width()
	if contentWidth <= 0 {
		contentWidth = getInputInnerWidth(m.getMainPaneWidth())
	}
	contentHeight := getInputHeight(value, contentWidth)

	return min(contentHeight, availableHeight)
}

func (m *model) resizeInputForValue(value string) {
	m.input.SetWidth(getInputInnerWidth(m.getMainPaneWidth()))
	m.input.SetHeight(m.getInputHeightForValue(value))
	m.updateCommandMenuForInput(value)
}

// insertInputNewline expands the composer before adding a newline.
func (m model) insertInputNewline() (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	inputWidth := getInputInnerWidth(m.getMainPaneWidth())
	availableHeight := max(m.height-m.getInputChromeHeightForValue(m.input.Value()+"\n")-1, minInputHeight)
	m.input.SetWidth(inputWidth)
	m.input.SetHeight(min(getInputHeight(m.input.Value()+"\n", m.input.Width()), availableHeight))
	m.input, cmd = m.input.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.resize()

	return m, cmd
}

// deleteInputLine clears the current logical composer line.
func (m model) deleteInputLine() (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.input.CursorEnd()
	m.input, cmd = m.input.Update(tea.KeyPressMsg(tea.Key{
		Code: 'u',
		Mod:  tea.ModCtrl,
	}))
	m.resize()

	return m, cmd
}

// getInputBoxWidth returns the full composer width.
func getInputBoxWidth(width int) int {
	return max(width, 1)
}

// getInputInnerWidth returns the textarea wrapping width inside the composer.
func getInputInnerWidth(width int) int {
	return max(
		getInputBoxWidth(width)-inputFrameBorderWidth-(inputFrameHorizontalPadding*2),
		1,
	)
}

// getInputHeight returns the number of visible rows needed for the value.
func getInputHeight(value string, width int) int {
	if value == "" {
		return minInputHeight
	}

	height := 0
	for _, line := range strings.Split(value, "\n") {
		height += getWrappedLineHeight(line, width)
	}

	return max(height, minInputHeight)
}

// getWrappedLineHeight returns how many terminal rows a line occupies.
func getWrappedLineHeight(line string, width int) int {
	width = max(width, 1)
	lineWidth := lipgloss.Width(line)
	if lineWidth == 0 {
		return 1
	}

	return max((lineWidth+width-1)/width, 1)
}

// isInputLineDeleteKey reports whether a key should clear the current row.
func isInputLineDeleteKey(msg tea.KeyPressMsg) bool {
	key := msg.Key()
	switch {
	case key.Code == 'u' && key.Mod.Contains(tea.ModCtrl):
		return true
	case key.Code == tea.KeyBackspace || key.Code == tea.KeyDelete:
		return key.Mod.Contains(tea.ModSuper) ||
			key.Mod.Contains(tea.ModMeta) ||
			key.Mod.Contains(tea.ModCtrl)
	default:
		return false
	}
}
