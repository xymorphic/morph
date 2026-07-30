package tui

import tea "charm.land/bubbletea/v2"

type slashCommandDefinition struct {
	Name        string
	Description string
}

var slashCommandDefinitions = []slashCommandDefinition{
	{Name: "changelog", Description: "Show the latest changelog entry"},
	{Name: "chats", Description: "Show recent chat sessions"},
	{Name: "clear", Description: "Clear the transcript"},
	{Name: "compact", Description: "Compact the current session"},
	{Name: "copy", Description: "Copy the transcript"},
	{Name: "effort", Description: "Inspect or set reasoning effort"},
	{Name: "models", Description: "Show supported models"},
	{Name: "new-chat", Description: "Start a new chat session"},
	{Name: "permissions", Description: "Choose a permission preset for this TUI session"},
	{Name: "queue", Description: "Focus queued session messages"},
	{Name: "steer", Description: "Steer the active run after its current tool batch"},
	{Name: "interrupt", Description: "Explicitly interrupt the active session run"},
	{Name: "archive", Description: "Show archived chat sessions"},
	{Name: "artifact", Description: "Open or save a browser artifact"},
	{Name: "providers", Description: "Show supported model providers"},
	{Name: "setup", Description: "Open setup"},
}

func (m *model) handleSlashCommand(input composerInput) tea.Cmd {
	var cmd tea.Cmd
	switch input.Name {
	case "archive":
		cmd = m.startArchiveCommand()
	case "artifact":
		cmd = m.handleBrowserArtifactCommand(input.Args)
	case "changelog":
		cmd = m.showChangelogCommand()
	case "chats":
		cmd = m.startChatsCommand()
	case "clear":
		m.applyAction(clearTranscriptAction{})
		m.transcriptCache.clear()
		cmd = m.setStatus("transcript cleared")
	case "compact":
		cmd = m.startCompactSession()
	case "effort":
		cmd = m.handleEffortCommand(input.Args)
	case "models":
		cmd = m.startModelsCommand()
	case "providers":
		cmd = m.startProvidersCommand()
	case "permissions":
		cmd = m.startPermissionsCommand()
	case "queue":
		cmd = m.handleQueueCommand(input.Args)
	case "steer":
		cmd = m.submitSteeringMessage(input.Args)
	case "interrupt":
		cmd = m.requestSessionInterrupt()
	case "setup":
		cmd = m.startProfileSetup(true)
	case "copy":
		cmd = m.copyTranscript()
	case "new-chat":
		cmd = m.startNewChat()
	case "":
		cmd = m.setStatus("empty command")
	default:
		cmd = m.setStatus("unknown command: /" + input.Name)
	}

	if m.responding {
		m.setTranscriptContentForResponseUpdate()
	} else {
		m.setTranscriptContent()
	}
	return cmd
}
