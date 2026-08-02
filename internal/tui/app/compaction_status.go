package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/xymorphic/morph/internal/trace"
	"github.com/xymorphic/morph/pkg/str"
)

const autoCompactionLabel = "Automatic compaction"
const manualCompactionLabel = "Manual compaction"
const autoCompactionAction = "Automatic Compaction"
const manualCompactionAction = "Manual Compaction"

type manualCompactionState struct {
	Status string
	Error  string
	Label  string
}

func manualCompactionStateFromTraceEvent(eventType string, payload any) manualCompactionState {
	compaction, _ := payload.(trace.CompactionEventPayload)
	label := manualCompactionLabel
	if compaction.Auto {
		label = autoCompactionLabel
	}
	eventTypeValue := str.String(eventType)
	switch eventTypeValue.Trim() {
	case trace.EvtContextCompactionPending, trace.EvtContextCompactionRunning:
		return manualCompactionState{Status: "running", Label: label}
	case trace.EvtContextCompactionSucceeded:
		return manualCompactionState{Status: "succeeded", Label: label}
	case trace.EvtContextCompactionFailed:
		return manualCompactionState{Status: "failed", Error: compaction.Error, Label: label}
	default:
		return manualCompactionState{}
	}
}

func (state manualCompactionState) isVisible() bool {
	statusValue := str.String(state.Status)
	return statusValue.Trim() != ""
}

func (state manualCompactionState) isInProgress() bool {
	statusValue2 := str.String(state.Status)
	switch statusValue2.Normalized() {
	case "pending", "running", "started":
		return true
	default:
		return false
	}
}

func (state manualCompactionState) displayText() string {
	labelValue := str.String(state.Label)
	label := labelValue.Trim()
	if label == "" {
		label = manualCompactionLabel
	}
	statusValue3 := str.String(state.Status)
	switch statusValue3.Normalized() {
	case "pending", "running", "started":
		return label + " started"
	case "succeeded", "completed":
		return label + " completed"
	case "failed":
		errorValue := str.String(state.Error)
		if err := errorValue.Trim(); err != "" {
			return label + " failed: " + err
		}
		return label + " failed"
	default:
		return ""
	}
}

func (m *model) startManualCompactionStatus() tea.Cmd {
	m.manualCompactionActive = true
	cell := manualCompactionTranscriptCell{state: manualCompactionState{Status: "running", Label: manualCompactionLabel}}
	m.applyAction(appendTranscriptCellAction{Cell: cell})
	m.manualCompactionIndex = len(m.messages) - 1
	m.input.Blur()
	m.setTranscriptContent()

	return m.startToolAnimation()
}

func (m *model) completeManualCompactionStatus(err error) {
	state := manualCompactionState{Status: "succeeded", Label: manualCompactionLabel}
	if err != nil {
		state = manualCompactionState{Status: "failed", Error: err.Error(), Label: manualCompactionLabel}
	}

	if m.manualCompactionIndex >= 0 && m.manualCompactionIndex < len(m.messages) {
		m.applyAction(replaceTranscriptCellAction{
			Index: m.manualCompactionIndex,
			Cell:  manualCompactionTranscriptCell{state: state},
		})
	} else {
		m.applyAction(appendTranscriptCellAction{Cell: manualCompactionTranscriptCell{state: state}})
	}

	m.manualCompactionActive = false
	m.manualCompactionIndex = -1
	m.input.Focus()
	m.setTranscriptContent()
}

func renderManualCompactionCell(cell manualCompactionTranscriptCell, ctx transcriptRenderContext) string {
	if cell.state.displayText() == "" {
		return ""
	}

	return renderToolTranscriptGroupWithContext(getCompactionToolGroup(cell.state), ctx)
}

func getCompactionToolGroup(state manualCompactionState) toolTranscriptGroup {
	group := toolTranscriptGroup{action: getCompactionToolAction(state.Label)}
	statusValue := str.String(state.Status)
	switch statusValue.Normalized() {
	case "succeeded", "completed":
		group.completed = true
	case "failed":
		group.terminalStatus = toolTranscriptTerminalStatusFailed
		errorValue := str.String(state.Error)
		if err := errorValue.Trim(); err != "" {
			group.details = []toolTranscriptDetail{{
				text:           err,
				terminalStatus: toolTranscriptTerminalStatusFailed,
			}}
		}
	}

	return group
}

func getCompactionToolAction(label string) string {
	labelValue := str.String(label)
	if labelValue.Normalized() == str.String(autoCompactionLabel).Normalized() {
		return autoCompactionAction
	}
	return manualCompactionAction
}
