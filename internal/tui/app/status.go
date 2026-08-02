package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	storage "github.com/xymorphic/morph/internal/state/core"
	tuistatus "github.com/xymorphic/morph/internal/tui/status"
)

const (
	defaultSessionID            = storage.DefaultSessionID
	defaultSessionTitle         = tuistatus.DefaultSessionTitle
	defaultStatus               = tuistatus.DefaultText
	statusReadySuffix           = tuistatus.ReadySuffix
	statusCancelSuffix          = "esc twice to stop · ctrl+c to quit"
	exitConfirmationWindow      = tuistatus.ExitConfirmationWindow
	interruptConfirmationWindow = exitConfirmationWindow
)

var currentTime = time.Now

type statusExpiredMsg struct {
	startedAt time.Time
}

type exitConfirmationExpiredMsg struct {
	startedAt time.Time
}

type interruptConfirmationExpiredMsg struct {
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

func (m model) confirmInterrupt() (tea.Model, tea.Cmd) {
	if !m.isTranscriptResponseActive() {
		m.clearInterruptConfirmation()
		return m, nil
	}

	now := currentTime()
	if m.isInterruptConfirmed(now) {
		m.clearInterruptConfirmation()
		return m, m.cancelActiveResponse()
	}

	m.interruptAt = now
	m.interruptResponseID = 0
	if m.responding {
		m.interruptResponseID = m.responseID
	}
	m.interruptRunID = m.getActiveSessionRunID()
	m.status.SetTransient("Press Esc again to interrupt", now)

	return m, tea.Tick(interruptConfirmationWindow, func(time.Time) tea.Msg {
		return interruptConfirmationExpiredMsg{startedAt: now}
	})
}

func (m model) isInterruptConfirmed(now time.Time) bool {
	if m.interruptAt.IsZero() {
		return false
	}
	elapsed := now.Sub(m.interruptAt)
	if elapsed < 0 || elapsed > interruptConfirmationWindow {
		return false
	}
	if m.responding && m.interruptResponseID != 0 && m.interruptResponseID == m.responseID {
		return true
	}
	return m.interruptRunID != "" && m.interruptRunID == m.getActiveSessionRunID()
}

func (m *model) clearInterruptConfirmation() {
	startedAt := m.interruptAt
	m.interruptAt = time.Time{}
	m.interruptResponseID = 0
	m.interruptRunID = ""
	if !startedAt.IsZero() {
		m.status.Expire(startedAt)
	}
}

func (m model) expireInterruptConfirmation(msg interruptConfirmationExpiredMsg) tea.Model {
	if m.interruptAt.IsZero() || !m.interruptAt.Equal(msg.startedAt) {
		return m
	}

	m.clearInterruptConfirmation()
	return m
}
