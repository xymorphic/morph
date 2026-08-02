package core

import (
	"errors"
	"strings"
	"time"

	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/nanoid"
	"github.com/xymorphic/morph/pkg/str"
)

// ValidateSessionID checks that id can be used as a persisted session ID.
func ValidateSessionID(id string) error {
	idValue := str.String(id)
	id = idValue.Trim()
	if id == "" {
		return errors.New("session id is required")
	}

	if id == DefaultSessionID {
		return nil
	}

	if !strings.HasPrefix(id, SessionIDPrefix) || nanoid.ValidateID(id) != nil {
		return errors.New("session id must be a valid ses_ nanoid")
	}

	return nil
}

// MarkSessionArchived returns session with archive metadata applied.
func MarkSessionArchived(session Session, archivedAt time.Time, expiresAt time.Time) (Session, error) {
	iDValue := str.String(session.ID)
	session.ID = iDValue.Trim()
	if err := ValidateSessionID(session.ID); err != nil {
		return Session{}, err
	}

	if session.ID == DefaultSessionID {
		return Session{}, errors.New("default session cannot be archived")
	}

	if archivedAt.IsZero() {
		archivedAt = time.Now().UTC()
	} else {
		archivedAt = archivedAt.UTC()
	}

	if expiresAt.IsZero() {
		return Session{}, errors.New("archive expiry is required")
	}

	session.Archived = true
	session.ArchivedAt = archivedAt
	session.ExpiresAt = expiresAt.UTC()
	session.Title, session.TitleSource = NormalizeSessionTitleMetadata(session.Title, session.TitleSource)

	return session, nil
}

// ClearSessionArchive returns session with archive metadata removed.
func ClearSessionArchive(session Session) (Session, error) {
	iDValue2 := str.String(session.ID)
	session.ID = iDValue2.Trim()
	if err := ValidateSessionID(session.ID); err != nil {
		return Session{}, err
	}

	if !session.Archived {
		return Session{}, errors.New("session is not archived")
	}

	session.Archived = false
	session.ArchivedAt = time.Time{}
	session.ExpiresAt = time.Time{}

	return session, nil
}

// NormalizeSessionTitle normalizes session title.
func NormalizeSessionTitle(title string) string {
	titleValue := str.String(title)
	return titleValue.Trim()
}

// NormalizeSessionTitleSource normalizes session title source.
func NormalizeSessionTitleSource(source string) string {
	sourceValue := str.String(source)
	source = sourceValue.Trim()
	switch source {
	case SessionTitleSourceGenerated, SessionTitleSourceManual:
		return source
	default:
		return ""
	}
}

// NormalizeSessionTitleMetadata normalizes session title metadata.
func NormalizeSessionTitleMetadata(title string, source string) (string, string) {
	title = NormalizeSessionTitle(title)
	if title == "" {
		return "", ""
	}

	return title, NormalizeSessionTitleSource(source)
}

// CloneMessages clones clone messages.
func CloneMessages(messages []morphmsg.Message) []morphmsg.Message {
	return morphmsg.CloneMessages(messages)
}

// NormalizeSessionSummary normalizes session summary.
func NormalizeSessionSummary(summary SessionSummary) (SessionSummary, error) {
	sessionIDValue := str.String(summary.SessionID)
	summary.SessionID = sessionIDValue.Trim()
	if err := ValidateSessionID(summary.SessionID); err != nil {
		if err.Error() == "session id is required" {
			return SessionSummary{}, errors.New("session id is required")
		}

		return SessionSummary{}, err
	}
	sessionSummaryValue := str.String(summary.SessionSummary)
	summary.SessionSummary = sessionSummaryValue.Trim()
	if summary.SessionSummary == "" {
		return SessionSummary{}, errors.New("session summary is required")
	}

	if summary.SourceEndOffset < 0 {
		return SessionSummary{}, errors.New("summary source end offset must be greater than or equal to zero")
	}

	if summary.SourceMessageCount < 0 {
		return SessionSummary{}, errors.New("summary source message count must be greater than or equal to zero")
	}

	if summary.SourceEndOffset > summary.SourceMessageCount {
		return SessionSummary{}, errors.New("summary source end offset cannot exceed source message count")
	}

	if summary.UpdatedAt.IsZero() {
		summary.UpdatedAt = time.Now().UTC()
	} else {
		summary.UpdatedAt = summary.UpdatedAt.UTC()
	}
	currentTaskValue := str.String(summary.CurrentTask)
	summary.CurrentTask = currentTaskValue.Trim()
	summary.Discoveries = cloneStrings(summary.Discoveries)
	summary.OpenQuestions = cloneStrings(summary.OpenQuestions)
	summary.NextActions = cloneStrings(summary.NextActions)

	return summary, nil
}

// CloneSessionSummary clones clone session summary.
func CloneSessionSummary(summary SessionSummary) SessionSummary {
	cloned := summary
	cloned.Discoveries = cloneStrings(summary.Discoveries)
	cloned.OpenQuestions = cloneStrings(summary.OpenQuestions)
	cloned.NextActions = cloneStrings(summary.NextActions)
	return cloned
}

// UniqueStrings trims, de-duplicates, and preserves the first occurrence order of strings.
func UniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		valueText := str.String(value).Trim()
		if valueText == "" {
			continue
		}
		if _, ok := seen[valueText]; ok {
			continue
		}
		seen[valueText] = struct{}{}
		unique = append(unique, valueText)
	}

	return unique
}

// NormalizeMatchValue canonicalizes role, tool, and filter values before comparison.
func NormalizeMatchValue(value string) string {
	value2 := str.String(value)
	return strings.ToLower(strings.Join(strings.Fields(value2.Trim()), " "))
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	cloned := make([]string, 0, len(values))
	for _, value := range values {
		value3 := str.String(value)
		value = value3.Trim()
		if value == "" {
			continue
		}

		cloned = append(cloned, value)
	}

	if len(cloned) == 0 {
		return nil
	}

	return cloned
}
