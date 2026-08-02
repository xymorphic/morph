package status

import (
	"time"

	"github.com/xymorphic/morph/pkg/str"
)

const (
	DefaultSessionTitle    = "default session"
	ReadySuffix            = "enter to send · ctrl+c to quit"
	DefaultText            = ReadySuffix
	AutoHideWindow         = 3 * time.Second
	ExitConfirmationWindow = 2 * time.Second
)

// Model describes the status bar text rendered by the tui.
type Model struct {
	defaultText string
	text        string
	startedAt   time.Time
	hideAfter   time.Duration
}

// New returns a status model with the supplied initial text.
func New() Model {
	return Model{
		defaultText: DefaultText,
		hideAfter:   AutoHideWindow,
	}
}

func (m Model) Text() string {
	textValue := str.String(m.text)
	if text := textValue.Trim(); text != "" {
		return text
	}
	defaultTextValue := str.String(m.defaultText)
	if text := defaultTextValue.Trim(); text != "" {
		return text
	}

	return ReadySuffix
}

func (m Model) HasTransient() bool {
	return !m.startedAt.IsZero()
}

func (m Model) StartedAt() time.Time {
	return m.startedAt
}

func (m Model) HideAfter() time.Duration {
	if m.hideAfter <= 0 {
		return AutoHideWindow
	}

	return m.hideAfter
}

func (m *Model) SetHideAfter(duration time.Duration) {
	m.hideAfter = duration
}

func (m *Model) SetDefault(text string) {
	textValue2 := str.String(text)
	m.defaultText = textValue2.Trim()
}

func (m *Model) SetTransient(text string, now time.Time) bool {
	textValue3 := str.String(text)
	m.text = textValue3.Trim()
	if m.text == "" {
		m.startedAt = time.Time{}
		return false
	}

	m.startedAt = now

	return true
}

func (m *Model) Expire(startedAt time.Time) {
	if m.startedAt.IsZero() || !m.startedAt.Equal(startedAt) {
		return
	}

	m.text = ""
	m.startedAt = time.Time{}
}
