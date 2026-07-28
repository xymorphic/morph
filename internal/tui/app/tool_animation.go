package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

var toolAnimationInterval = 180 * time.Millisecond

type toolAnimationTickMsg struct{}

func (m *model) startToolAnimation() tea.Cmd {
	if !m.hasRunningToolTranscriptCells() && !m.hasRunningCompactionTranscriptCells() && !m.manualCompactionActive {
		m.toolAnimationActive = false
		return nil
	}
	if m.toolAnimationActive {
		return nil
	}

	m.toolAnimationActive = true
	return toolAnimationTickCmd()
}

func (m *model) updateToolAnimation() (tea.Model, tea.Cmd) {
	if !m.hasRunningToolTranscriptCells() && !m.hasRunningCompactionTranscriptCells() && !m.manualCompactionActive {
		m.toolAnimationActive = false
		return *m, nil
	}

	m.toolAnimationFrame++
	if !m.selection.active {
		if m.responding {
			m.setTranscriptContentForResponseUpdate()
		} else {
			m.setTranscriptContentForActiveTurn()
		}
	}
	m.resizeTranscriptIfLayoutChanged()

	return *m, toolAnimationTickCmd()
}

func (m model) hasRunningToolTranscriptCells() bool {
	return hasRunningToolTranscriptCell(m.messages)
}

func (m model) hasRunningCompactionTranscriptCells() bool {
	for _, cell := range m.messages {
		compactionCell, ok := cell.(manualCompactionTranscriptCell)
		if ok && compactionCell.state.isInProgress() {
			return true
		}
	}

	return false
}

func toolAnimationTickCmd() tea.Cmd {
	return tea.Tick(toolAnimationInterval, func(time.Time) tea.Msg {
		return toolAnimationTickMsg{}
	})
}

func hasRunningToolTranscriptCell(cells []transcriptCell) bool {
	var toolGroup *toolTranscriptGroup
	for _, cell := range cells {
		toolCell, ok := cell.(toolTranscriptCell)
		if !ok {
			if toolGroup != nil && toolGroup.outcome() == toolTranscriptGroupOutcomeRunning {
				return true
			}

			toolGroup = nil
			continue
		}
		if toolGroup == nil || toolGroup.action != toolCell.action {
			if toolGroup != nil && toolGroup.outcome() == toolTranscriptGroupOutcomeRunning {
				return true
			}

			toolGroup = &toolTranscriptGroup{action: toolCell.action}
		}
		toolGroup.add(toolCell)
	}

	return toolGroup != nil && toolGroup.outcome() == toolTranscriptGroupOutcomeRunning
}
