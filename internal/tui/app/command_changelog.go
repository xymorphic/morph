package tui

import (
	tea "charm.land/bubbletea/v2"

	changelog "github.com/xymorphic/morph"
)

func (m *model) showChangelogCommand() tea.Cmd {
	m.showCommandView(commandViewPayload{
		TitleIcon:       "✦",
		TitleLeft:       "Changelog",
		TitleSubtext:    "See what is new",
		TitleRight:      "esc to close",
		TitleRightColor: defaultTUITheme.MutedText,
		Content:         changelog.Latest(),
	})

	return nil
}
