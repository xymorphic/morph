package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	storage "github.com/xymorphic/morph/internal/state/core"
	tuistatus "github.com/xymorphic/morph/internal/tui/status"
)

const (
	defaultSessionID       = storage.DefaultSessionID
	defaultSessionTitle    = tuistatus.DefaultSessionTitle
	defaultStatus          = tuistatus.DefaultText
	statusReadySuffix      = tuistatus.ReadySuffix
	statusCancelSuffix     = "esc to stop · ctrl+c to quit"
	exitConfirmationWindow = tuistatus.ExitConfirmationWindow
)

var currentTime = time.Now

type statusExpiredMsg struct {
	startedAt time.Time
}

type exitConfirmationExpiredMsg struct {
	startedAt time.Time
}

type statusModel = tuistatus.Model

func newStatusModel() statusModel {
	return tuistatus.New()
}

func statusHasTransient(status statusModel) bool {
	return status.HasTransient()
}

func setStatusDefault(status *statusModel, text string) {
	status.SetDefault(text)
}

func setStatusTransient(status *statusModel, text string) tea.Cmd {
	if !status.SetTransient(text, currentTime()) {
		return nil
	}

	startedAt := status.StartedAt()
	hideAfter := status.HideAfter()

	return tea.Tick(hideAfter, func(time.Time) tea.Msg {
		return statusExpiredMsg{startedAt: startedAt}
	})
}

func expireStatus(status *statusModel, msg statusExpiredMsg) {
	status.Expire(msg.startedAt)
}

func (m *model) setStatus(text string) tea.Cmd {
	return setStatusTransient(&m.status, text)
}

func (m *model) setDefaultStatus(text string) {
	setStatusDefault(&m.status, text)
}

func (m model) bottomStatusText() string {
	if m.responding && !statusHasTransient(m.status) {
		return statusCancelSuffix
	}

	return m.status.Text()
}

func (m model) statusExpireCmd() tea.Cmd {
	if !statusHasTransient(m.status) {
		return nil
	}

	startedAt := m.status.StartedAt()
	hideAfter := m.status.HideAfter()

	return tea.Tick(hideAfter, func(time.Time) tea.Msg {
		return statusExpiredMsg{startedAt: startedAt}
	})
}

// confirmExit quits only after a second Ctrl-C inside a short window.
func (m model) confirmExit() (tea.Model, tea.Cmd) {
	now := currentTime()
	if !m.exitAt.IsZero() && now.Sub(m.exitAt) <= exitConfirmationWindow {
		m.cleanupBrowserArtifactCopies()
		return m, tea.Quit
	}

	m.exitAt = now
	startedAt := m.exitAt
	m.status.SetTransient("Press Ctrl-C again to exit", startedAt)

	return m, tea.Tick(exitConfirmationWindow, func(time.Time) tea.Msg {
		return exitConfirmationExpiredMsg{startedAt: startedAt}
	})
}

// hasPendingExitConfirmation reports whether Ctrl-C is awaiting confirmation.
func (m model) hasPendingExitConfirmation() bool {
	return !m.exitAt.IsZero()
}

// expireExitConfirmation clears a stale Ctrl-C exit confirmation.
func (m model) expireExitConfirmation(msg exitConfirmationExpiredMsg) tea.Model {
	if m.exitAt.IsZero() || !m.exitAt.Equal(msg.startedAt) {
		return m
	}

	m.exitAt = time.Time{}
	expireStatus(&m.status, statusExpiredMsg(msg))

	return m
}
