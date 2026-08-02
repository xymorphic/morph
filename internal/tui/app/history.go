package tui

import (
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/xymorphic/morph/internal/datadir"
	tuicomposer "github.com/xymorphic/morph/internal/tui/composer"
	"github.com/xymorphic/morph/pkg/str"
)

const maxPromptHistory = tuicomposer.MaxPromptHistory

var promptHistoryPath = func() string {
	return filepath.Join(datadir.DataDir(), "tui-history.json")
}

func loadPromptHistory() ([]string, error) {
	return tuicomposer.LoadHistory(promptHistoryPath())
}

func savePromptHistory(history []string) error {
	return tuicomposer.SaveHistory(promptHistoryPath(), history)
}

func normalizePromptHistory(history []string) []string {
	return tuicomposer.NormalizeHistory(history)
}

func (m *model) addPromptHistory(value string) tea.Cmd {
	valueText := str.String(value).Trim()
	if valueText == "" {
		return nil
	}
	if len(m.history) > 0 && m.history[len(m.history)-1] == valueText {
		m.historyAt = len(m.history)
		return nil
	}

	m.history = normalizePromptHistory(append(m.history, valueText))
	m.historyAt = len(m.history)
	if err := savePromptHistory(m.history); err != nil {
		return m.setStatus("prompt history unavailable")
	}

	return nil
}

func (m *model) showPreviousPrompt() {
	if len(m.history) == 0 {
		return
	}

	if m.historyAt == len(m.history) {
		m.draft = m.input.Value()
	}
	if m.historyAt > 0 {
		m.historyAt--
	}

	m.input.SetValue(m.history[m.historyAt])
	m.input.CursorEnd()
	m.resize()
}

func (m *model) showNextPrompt() {
	if len(m.history) == 0 || m.historyAt >= len(m.history) {
		return
	}

	m.historyAt++
	if m.historyAt == len(m.history) {
		m.input.SetValue(m.draft)
		m.draft = ""
	} else {
		m.input.SetValue(m.history[m.historyAt])
	}
	m.input.CursorEnd()
	m.resize()
}

func (m model) shouldUseHistoryKey(msg tea.KeyPressMsg) bool {
	switch msg.Key().Code {
	case tea.KeyUp, tea.KeyDown:
		return !strings.Contains(m.input.Value(), "\n")
	default:
		return false
	}
}
