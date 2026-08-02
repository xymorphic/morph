package tui

import (
	tea "charm.land/bubbletea/v2"

	tuievents "github.com/xymorphic/morph/internal/tui/events"
)

type tuiEvent = tuievents.Event

type viewportResizedEvent = tuievents.ViewportResizedEvent

type submitComposerEvent = tuievents.SubmitComposerEvent
type copyTranscriptEvent = tuievents.CopyTranscriptEvent
type jumpTranscriptToBottomEvent = tuievents.JumpTranscriptToBottomEvent
type showPreviousPromptEvent = tuievents.ShowPreviousPromptEvent
type showNextPromptEvent = tuievents.ShowNextPromptEvent
type insertInputNewlineEvent = tuievents.InsertInputNewlineEvent
type deleteInputLineEvent = tuievents.DeleteInputLineEvent

type applyTUIMessageEvent = tuievents.ApplyTUIMessageEvent
type hydrateTimelineEvent = tuievents.HydrateTimelineEvent

func (m model) handleAppEvent(event tuiEvent) (model, tea.Cmd) {
	switch value := event.(type) {
	case viewportResizedEvent:
		position := m.getTranscriptWindowPosition()
		m.applyAction(setViewportSizeAction{Width: value.Width, Height: value.Height})
		m.resize()
		m.refreshTranscriptContentAtPosition(position)
		return m, nil
	case submitComposerEvent:
		cmd := m.submitPrompt()
		return m, cmd
	case copyTranscriptEvent:
		cmd := m.copyTranscript()
		return m, cmd
	case jumpTranscriptToBottomEvent:
		m.jumpTranscriptToBottom()
		return m, nil
	case showPreviousPromptEvent:
		m.showPreviousPrompt()
		return m, nil
	case showNextPromptEvent:
		m.showNextPrompt()
		return m, nil
	case insertInputNewlineEvent:
		updated, cmd := m.insertInputNewline()
		next, _ := updated.(model)
		return next, inputHandledCmd(cmd)
	case deleteInputLineEvent:
		updated, cmd := m.deleteInputLine()
		next, _ := updated.(model)
		return next, inputHandledCmd(cmd)
	case applyTUIMessageEvent:
		cmd := m.applyTUIMessage(value.Message)
		return m, cmd
	case hydrateTimelineEvent:
		cmd := m.hydrateSessionTimeline(value.Timeline)
		return m, cmd
	default:
		return m, nil
	}
}
