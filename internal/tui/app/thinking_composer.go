package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

var thinkingComposerInterval = 140 * time.Millisecond

var thinkingComposerBorderColors = []string{
	"36",
	"37",
	"38",
	"39",
	"75",
	"81",
}

const (
	thinkingStatusBaseColor    = "8"
	thinkingStatusEdgeColor    = "7"
	thinkingStatusShimmerColor = "15"
)

type thinkingComposerTickMsg struct{}

func (m *model) startThinkingComposer() tea.Cmd {
	if !m.isModelThinking() {
		m.thinkingComposerActive = false
		return nil
	}
	if m.thinkingComposerActive {
		return nil
	}

	m.thinkingComposerActive = true
	return thinkingComposerTickCmd()
}

func (m *model) startSuccessorThinkingComposer(completedEntryID string) tea.Cmd {
	activeRun := m.sessionExecutionState.ActiveRun
	if activeRun == nil ||
		completedEntryID == "" ||
		activeRun.QueueEntryID == completedEntryID {
		return nil
	}
	return m.startThinkingComposer()
}

func (m *model) updateThinkingComposer() (tea.Model, tea.Cmd) {
	if !m.isModelThinking() {
		m.thinkingComposerActive = false
		return *m, nil
	}

	m.thinkingComposerFrame++
	return *m, thinkingComposerTickCmd()
}

func (m model) isThinkingComposerVisible() bool {
	return m.thinkingComposerEnabled && m.isModelThinking()
}

func renderThinkingStatusCell(frame int) string {
	const text = "Thinking"

	var builder strings.Builder
	for index, char := range text {
		color := getThinkingStatusColor(index, frame, len(text))
		builder.WriteString(lipgloss.NewStyle().
			Inline(true).
			Foreground(lipgloss.Color(color)).
			Render(string(char)))
	}

	return builder.String()
}

func (m model) isModelThinking() bool {
	return m.isTranscriptResponseActive() &&
		(m.live == nil || m.live.IsEmpty()) &&
		m.responseRunningToolCount == 0
}

func getThinkingComposerBorderColor(frame int) string {
	if len(thinkingComposerBorderColors) == 0 {
		return "8"
	}

	index := frame % len(thinkingComposerBorderColors)
	if index < 0 {
		index += len(thinkingComposerBorderColors)
	}

	return thinkingComposerBorderColors[index]
}

func getThinkingStatusColor(index, frame, length int) string {
	if length <= 0 {
		return thinkingStatusBaseColor
	}

	shimmerIndex := frame % length
	if shimmerIndex < 0 {
		shimmerIndex += length
	}
	if index == shimmerIndex {
		return thinkingStatusShimmerColor
	}
	if index == wrapThinkingStatusIndex(shimmerIndex-1, length) ||
		index == wrapThinkingStatusIndex(shimmerIndex+1, length) {
		return thinkingStatusEdgeColor
	}

	return thinkingStatusBaseColor
}

func wrapThinkingStatusIndex(index, length int) int {
	if length <= 0 {
		return 0
	}

	index %= length
	if index < 0 {
		index += length
	}

	return index
}

func thinkingComposerTickCmd() tea.Cmd {
	return tea.Tick(thinkingComposerInterval, func(time.Time) tea.Msg {
		return thinkingComposerTickMsg{}
	})
}
