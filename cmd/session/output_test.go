package session

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	storage "github.com/xymorphic/morph/internal/state/core"
)

func TestSessionListToText_FormatsRowsAndEmptyState(t *testing.T) {
	require.Equal(t, "No sessions found.\n", sessionListToText(nil))

	longTitle := strings.Repeat("a", sessionTitleDisplayLimit+10)
	output := sessionListToText([]storage.Session{{
		ID:        "ses_projectaprojectaproje",
		Title:     longTitle,
		Origin:    storage.SessionOrigin{Source: storage.SessionOriginSourceAutomation},
		UpdatedAt: time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC),
	}})

	require.True(t, strings.HasPrefix(output, "ID"))
	require.Contains(t, output, "TITLE")
	require.Contains(t, output, strings.Repeat("a", sessionTitleDisplayLimit-3)+"...")
	require.NotContains(t, output, longTitle)
	require.Contains(t, output, "automation")
	require.Contains(t, output, "2026-07-05T08:00:00Z")
}

func TestGetSessionTitleDisplay_CapsTitleLength(t *testing.T) {
	exact := strings.Repeat("a", sessionTitleDisplayLimit)
	long := strings.Repeat("b", sessionTitleDisplayLimit+1)
	unicodeTitle := strings.Repeat("界", sessionTitleDisplayLimit+1)

	require.Equal(t, "Short title", getSessionTitleDisplay(" Short title "))
	require.Equal(t, "Line break title", getSessionTitleDisplay("Line\nbreak\ttitle"))
	require.Equal(t, exact, getSessionTitleDisplay(exact))
	require.Equal(t, strings.Repeat("b", sessionTitleDisplayLimit-3)+"...", getSessionTitleDisplay(long))
	require.Equal(t, strings.Repeat("界", sessionTitleDisplayLimit-3)+"...", getSessionTitleDisplay(unicodeTitle))
	require.Equal(t, sessionTitleDisplayLimit, utf8.RuneCountInString(getSessionTitleDisplay(unicodeTitle)))
	require.Equal(t, "-", getSessionTitleDisplay(""))
}

func TestSessionOutputHelpers_FormatValuesAndPropagateErrors(t *testing.T) {
	require.Empty(t, formatSessionTime(time.Time{}))
	require.Equal(
		t,
		"2024-05-01T07:00:00Z",
		formatSessionTime(time.Date(2024, 5, 1, 8, 0, 0, 0, time.FixedZone("test", 3600))),
	)
	require.Equal(t, "50.00%", formatSessionPercentage(0.5))
	require.Equal(t, `"line\nbreak"`, getSessionDisplayText("line\nbreak"))

	originalOutput := sessionOutput
	t.Cleanup(func() { sessionOutput = originalOutput })
	expected := errors.New("write failed")
	sessionOutput = failingSessionWriter{err: expected}
	require.ErrorIs(t, writeSessionOutput("value"), expected)
}
