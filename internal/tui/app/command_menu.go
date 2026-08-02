package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/xymorphic/morph/pkg/str"
)

const (
	maxCommandMenuHeight = 10
	commandMenuGap       = 0
	commandMenuLeftPad   = 4
)

func (m model) renderCommandMenu() string {
	if !m.isCommandMenuVisible() {
		return ""
	}

	width := m.getMainPaneWidth()
	visibleCommands := getVisibleSlashCommands(m.input.Value(), m.commandMenuOffset, m.getCommandMenuHeight())
	rows := make([]string, 0, len(visibleCommands))
	for index, command := range visibleCommands {
		rows = append(rows, renderCommandMenuRow(command, index+m.commandMenuOffset == m.commandMenuSelected, width))
	}

	return strings.Join(rows, "\n")
}

func renderCommandMenuRow(command slashCommandDefinition, selected bool, width int) string {
	leftPad := min(commandMenuLeftPad, max(width-1, 0))
	contentWidth := max(width-leftPad, 1)
	commandText := "/" + command.Name
	commandWidth := min(max(lipgloss.Width(commandText)+2, 14), max(contentWidth/3, 10))
	descriptionWidth := max(contentWidth-commandWidth-2, 1)

	commandStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(defaultTUITheme.ToolDetail)).
		Width(commandWidth)
	descriptionStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(defaultTUITheme.ToolBranch)).
		Width(descriptionWidth)
	rowStyle := lipgloss.NewStyle().Width(width)
	if selected {
		commandStyle = commandStyle.Foreground(lipgloss.Color(defaultTUITheme.MarkdownLinkForeground)).
			Background(lipgloss.Color(defaultTUITheme.JumpToBottomBackground))
		descriptionStyle = descriptionStyle.Foreground(lipgloss.Color(defaultTUITheme.JumpToBottomForeground)).
			Background(lipgloss.Color(defaultTUITheme.JumpToBottomBackground))
		rowStyle = rowStyle.Background(lipgloss.Color(defaultTUITheme.JumpToBottomBackground))
	}

	row := strings.Repeat(" ", leftPad) +
		commandStyle.Render(commandText) +
		descriptionStyle.Render(truncateCommandMenuText(command.Description, descriptionWidth))

	return rowStyle.Render(row)
}

func truncateCommandMenuText(value string, width int) string {
	valueText := strings.Join(strings.Fields(str.String(value).Trim()), " ")
	if width <= 0 || lipgloss.Width(valueText) <= width {
		return valueText
	}
	if width <= 1 {
		return ""
	}

	runes := []rune(valueText)
	for len(runes) > 0 && lipgloss.Width(string(runes)+"…") > width {
		runes = runes[:len(runes)-1]
	}

	return string(runes) + "…"
}

func (m model) isCommandMenuVisible() bool {
	return isCommandMenuVisibleForValue(m.input.Value())
}

func isCommandMenuVisibleForValue(value string) bool {
	value2 := str.String(value)
	return strings.HasPrefix(value2.Trim(), "/")
}

func (m model) getCommandMenuHeight() int {
	return getCommandMenuHeightForValue(m.input.Value())
}

func getCommandMenuHeightForValue(value string) int {
	if !isCommandMenuVisibleForValue(value) {
		return 0
	}

	return min(len(getFilteredSlashCommands(value)), maxCommandMenuHeight)
}

func (m model) getInputChromeHeightForValue(value string) int {
	return baseInputChromeHeight + getCommandMenuHeightForValue(value) + commandMenuGap
}

func getVisibleSlashCommands(value string, offset int, height int) []slashCommandDefinition {
	commands := getFilteredSlashCommands(value)
	if height <= 0 || len(commands) == 0 {
		return nil
	}

	offset = clampCommandMenuOffset(offset, height, len(commands))
	end := min(offset+height, len(commands))

	return commands[offset:end]
}

func (m model) getSelectedSlashCommand() (slashCommandDefinition, bool) {
	if !m.isCommandMenuVisible() {
		return slashCommandDefinition{}, false
	}

	commands := getFilteredSlashCommands(m.input.Value())
	if len(commands) == 0 {
		return slashCommandDefinition{}, false
	}

	selection := clampCommandMenuSelection(m.commandMenuSelected, len(commands))
	return commands[selection], true
}

func getFilteredSlashCommands(value string) []slashCommandDefinition {
	prefix := getSlashCommandPrefix(value)
	if prefix == "" {
		return slashCommandDefinitions
	}

	commands := make([]slashCommandDefinition, 0, len(slashCommandDefinitions))
	for _, command := range slashCommandDefinitions {
		if strings.HasPrefix(command.Name, prefix) {
			commands = append(commands, command)
		}
	}

	return commands
}

func getSlashCommandPrefix(value string) string {
	input := parseComposerInput(value)
	if input.Kind != composerInputCommand {
		return ""
	}
	nameValue := str.String(input.Name)
	return nameValue.Trim()
}

func clampCommandMenuOffset(offset int, height int, commandCount int) int {
	maxOffset := max(commandCount-height, 0)
	return min(max(offset, 0), maxOffset)
}

func (m *model) updateCommandMenuForInput(value string) {
	height := getCommandMenuHeightForValue(value)
	if height == 0 {
		m.commandMenuOffset = 0
		m.commandMenuSelected = 0
		m.commandMenuPrefix = ""
		return
	}

	prefix := getSlashCommandPrefix(value)
	if prefix != m.commandMenuPrefix {
		m.commandMenuSelected = 0
		m.commandMenuOffset = 0
		m.commandMenuPrefix = prefix
		return
	}

	commandCount := len(getFilteredSlashCommands(value))
	m.commandMenuSelected = clampCommandMenuSelection(m.commandMenuSelected, commandCount)
	m.commandMenuOffset = getCommandMenuOffsetForSelection(m.commandMenuSelected, m.commandMenuOffset, height, commandCount)
}

func (m *model) scrollCommandMenu(delta int) bool {
	if !m.isCommandMenuVisible() {
		return false
	}

	height := m.getCommandMenuHeight()
	commandCount := len(getFilteredSlashCommands(m.input.Value()))
	m.commandMenuSelected = clampCommandMenuSelection(m.commandMenuSelected+delta, commandCount)
	m.commandMenuOffset = getCommandMenuOffsetForSelection(m.commandMenuSelected, m.commandMenuOffset, height, commandCount)

	return true
}

func clampCommandMenuSelection(selection int, commandCount int) int {
	if commandCount == 0 {
		return 0
	}

	return min(max(selection, 0), commandCount-1)
}

func getCommandMenuOffsetForSelection(selection int, offset int, height int, commandCount int) int {
	offset = clampCommandMenuOffset(offset, height, commandCount)
	if selection < offset {
		return selection
	}
	if selection >= offset+height {
		return clampCommandMenuOffset(selection-height+1, height, commandCount)
	}

	return offset
}

func (m *model) scrollCommandMenuWithMouse(msg tea.MouseWheelMsg) bool {
	if !m.isMouseOverCommandMenu(msg) {
		return false
	}

	switch msg.Button {
	case tea.MouseWheelUp:
		return m.scrollCommandMenu(-1)
	case tea.MouseWheelDown:
		return m.scrollCommandMenu(1)
	default:
		return false
	}
}

func (m model) isMouseOverCommandMenu(msg tea.MouseWheelMsg) bool {
	height := m.getCommandMenuHeight()
	if height == 0 {
		return false
	}

	layout := m.getTUILayout(m.input.Height())

	return msg.Y >= layout.Composer.Y && msg.Y < layout.Composer.Y+height
}

func (m *model) updateCommandMenuHover(msg tea.MouseMotionMsg) bool {
	if !m.isMouseOverCommandMenuMotion(msg) {
		return false
	}

	layout := m.getTUILayout(m.input.Height())
	row := msg.Y - layout.Composer.Y
	commandCount := len(getFilteredSlashCommands(m.input.Value()))
	m.commandMenuSelected = clampCommandMenuSelection(m.commandMenuOffset+row, commandCount)

	return true
}

func (m model) isMouseOverCommandMenuMotion(msg tea.MouseMotionMsg) bool {
	height := m.getCommandMenuHeight()
	if height == 0 {
		return false
	}

	layout := m.getTUILayout(m.input.Height())

	return msg.Y >= layout.Composer.Y && msg.Y < layout.Composer.Y+height
}
