package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const jumpToBottomLabel = "Jump to bottom (ctrl+End) ↓"

// View composes the scrollable transcript and fixed input composer.
func (m model) View() tea.View {
	bottomPanel := m.renderInput()
	if m.shouldShowNamePrompt() || m.shouldShowProfileModelSetup() {
		bottomPanel = ""
	}
	if m.isCommandViewVisible() {
		bottomPanel = m.renderCommandView()
	}
	parts := []string{
		m.renderTranscript(),
		m.renderJumpToBottom(),
	}
	if sessionQueue := m.renderSessionQueue(); sessionQueue != "" {
		parts = append(parts, sessionQueue)
	}
	parts = append(parts, bottomPanel)
	mainContent := lipgloss.JoinVertical(lipgloss.Left, parts...)
	view := tea.NewView(mainContent)
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion

	return view
}

func (m model) renderJumpToBottom() string {
	if m.shouldShowNamePrompt() || m.shouldShowProfileModelSetup() {
		return ""
	}
	if m.isTranscriptAtAbsoluteBottom() || m.isCommandMenuVisible() || m.isCommandViewVisible() {
		return ""
	}

	label := lipgloss.NewStyle().
		Foreground(lipgloss.Color(defaultTUITheme.JumpToBottomForeground)).
		Background(lipgloss.Color(defaultTUITheme.JumpToBottomBackground)).
		Padding(0, 1).
		Render(jumpToBottomLabel)

	return lipgloss.NewStyle().
		Width(m.getMainPaneWidth()).
		Align(lipgloss.Center).
		Render(label)
}

func (m model) clicksJumpToBottomIndicator(msg tea.MouseClickMsg) bool {
	if msg.Button != tea.MouseLeft || m.isTranscriptAtAbsoluteBottom() || m.isCommandMenuVisible() || m.isCommandViewVisible() {
		return false
	}

	return msg.Y == m.getJumpToBottomIndicatorRow()
}

func (m model) getJumpToBottomIndicatorRow() int {
	return m.getTUILayout(m.input.Height()).JumpToBottom.Y
}

func (m *model) jumpTranscriptToBottom() {
	if m.selection.active {
		m.expandTranscriptSelectionToEdge(1)
	} else {
		m.renderTranscriptWindowForScroll(transcriptWindowTail)
	}
	m.transcript.GotoBottom()
	if m.isTranscriptResponseActive() {
		m.responseTranscriptFollow = true
		m.responseTranscriptScrolled = false
	}
}

func (m model) getMainPaneWidth() int {
	return getMainPaneWidth(m.width)
}

func (m model) getTUILayout(inputHeight int) tuiLayout {
	if m.isCommandViewVisible() {
		return getTUILayoutWithBottomPaneHeight(
			m.width,
			m.height,
			m.getCommandViewHeight(),
		)
	}

	queueHeight := m.getSessionQueueHeight()
	layout := getTUILayoutWithInputChromeHeight(
		m.width,
		m.height,
		inputHeight,
		m.getInputChromeHeightForValue(m.input.Value())+queueHeight,
	)
	layout.SessionQueue = tuiRect{
		X:      0,
		Y:      layout.Composer.Y,
		Width:  layout.Composer.Width,
		Height: queueHeight,
	}
	layout.Composer.Y += queueHeight
	layout.BottomStatusPanel.Y += queueHeight
	return layout
}
