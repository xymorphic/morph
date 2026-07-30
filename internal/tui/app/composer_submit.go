package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	tuicomposer "github.com/wandxy/morph/internal/tui/composer"
	agentsession "github.com/wandxy/morph/pkg/agent/session"
)

const (
	composerInputEmpty        = tuicomposer.InputEmpty
	composerInputPrompt       = tuicomposer.InputPrompt
	composerInputCommand      = tuicomposer.InputCommand
	composerInputLocalCommand = tuicomposer.InputLocalCommand
)

type composerInput = tuicomposer.Input

func parseComposerInput(value string) composerInput {
	return tuicomposer.ParseInput(value)
}

func (m model) parseComposerInputForSubmit() composerInput {
	input := parseComposerInput(m.input.Value())
	if input.Kind != composerInputCommand {
		return input
	}

	command, ok := m.getSelectedSlashCommand()
	if !ok {
		return input
	}

	text := "/" + command.Name
	if input.Args != "" {
		text += " " + input.Args
	}

	return composerInput{
		Kind: composerInputCommand,
		Text: text,
		Name: command.Name,
		Args: input.Args,
	}
}

func normalizeComposerPaste(value string) string {
	return tuicomposer.NormalizePaste(value)
}

// submitPrompt routes a non-empty composer value to prompt or command handling.
func (m *model) submitPrompt() tea.Cmd {
	if m.sessionQueueEditingEntryID != "" && m.sessionQueueStale {
		return tea.Batch(
			m.setStatus("queue state is stale; refreshing"),
			loadSessionExecutionStateCmd(m.chatCtx, m.chatClient, m.getCurrentSessionID()),
		)
	}

	if m.sessionQueueEditingEntryID != "" {
		content := strings.TrimSpace(m.input.Value())
		if content == "" {
			return m.setStatus("queued message cannot be empty")
		}
		if m.sessionQueueEditSaving {
			return nil
		}
		m.sessionQueueEditSaving = true
		return editQueuedMessageCmd(
			m.chatCtx,
			m.chatClient,
			m.getCurrentSessionID(),
			m.sessionQueueEditingEntryID,
			content,
		)
	}

	input := m.parseComposerInputForSubmit()

	if input.Kind == composerInputEmpty {
		return nil
	}

	var cmd tea.Cmd
	promptSubmitted := false

	switch input.Kind {
	case composerInputPrompt:
		if m.sessionQueueStale {
			return tea.Batch(
				m.setStatus("queue state is stale; refreshing"),
				loadSessionExecutionStateCmd(m.chatCtx, m.chatClient, m.getCurrentSessionID()),
			)
		}
		cmd = m.addPromptHistory(input.Text)
		if m.responding || m.sessionExecutionState.ActiveRun != nil {
			m.clearComposer()
			m.resize()
			return tea.Batch(
				cmd,
				submitQueuedMessageCmd(
					m.chatCtx,
					m.chatClient,
					m.getCurrentSessionID(),
					input.Text,
					agentsession.DeliveryModeFollowUp,
					agentsession.SteeringFallbackFollowUp,
				),
			)
		}
		followTranscript := m.isTranscriptAtAbsoluteBottom()

		m.applyAction(appendTranscriptCellAction{Cell: userTranscriptCell{text: input.Text}})
		m.clearComposer()
		m.resize()

		if followTranscript {
			m.setTranscriptContent()
		} else {
			m.setTranscriptContentForActiveTurn()
		}

		cmd = tea.Batch(
			cmd,
			m.runEffect(sendPromptEffect{
				Text:             input.Text,
				FollowTranscript: followTranscript,
			}),
		)
		promptSubmitted = true

	case composerInputCommand:
		cmd = tea.Batch(cmd, m.handleSlashCommand(input))

	case composerInputLocalCommand:
		cmd = m.addPromptHistory(input.Text)
		cmd = tea.Batch(cmd, m.handleLocalCommand(input))
	}

	if promptSubmitted {
		return cmd
	}

	if m.responding {
		m.setTranscriptContentForResponseUpdate()
	} else {
		m.setTranscriptContent()
	}

	m.clearComposer()
	m.resize()

	return cmd
}

func (m *model) clearComposer() {
	m.input.SetValue("")
	m.commandMenuOffset = 0
	m.commandMenuSelected = 0
	m.commandMenuPrefix = ""
	m.historyAt = len(m.history)
	m.draft = ""
}
