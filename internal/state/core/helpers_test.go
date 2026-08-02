package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/nanoid"
)

var (
	testSessionID = nanoid.MustFromSeed(SessionIDPrefix, "project-a", "SessionUtilTestSeedValue123")
)

func TestValidateSessionID(t *testing.T) {
	t.Run("rejects empty id", func(t *testing.T) {
		err := ValidateSessionID("   ")
		require.EqualError(t, err, "session id is required")
	})

	t.Run("accepts default id", func(t *testing.T) {
		require.NoError(t, ValidateSessionID(DefaultSessionID))
	})

	t.Run("rejects invalid id", func(t *testing.T) {
		err := ValidateSessionID("ses_invalid")
		require.EqualError(t, err, "session id must be a valid ses_ nanoid")
	})

	t.Run("accepts valid generated id", func(t *testing.T) {
		require.NoError(t, ValidateSessionID(testSessionID))
	})
}

func TestMarkSessionArchived(t *testing.T) {
	now := time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC)
	expiresAt := now.Add(24 * time.Hour)

	t.Run("rejects missing session id", func(t *testing.T) {
		session, err := MarkSessionArchived(Session{}, now, expiresAt)
		require.EqualError(t, err, "session id is required")
		require.Equal(t, Session{}, session)
	})

	t.Run("rejects invalid session id", func(t *testing.T) {
		session, err := MarkSessionArchived(Session{ID: "ses_invalid"}, now, expiresAt)
		require.EqualError(t, err, "session id must be a valid ses_ nanoid")
		require.Equal(t, Session{}, session)
	})

	t.Run("rejects default session", func(t *testing.T) {
		session, err := MarkSessionArchived(Session{ID: DefaultSessionID}, now, expiresAt)
		require.EqualError(t, err, "default session cannot be archived")
		require.Equal(t, Session{}, session)
	})

	t.Run("rejects missing expiry", func(t *testing.T) {
		session, err := MarkSessionArchived(Session{ID: testSessionID}, now, time.Time{})
		require.EqualError(t, err, "archive expiry is required")
		require.Equal(t, Session{}, session)
	})

	t.Run("defaults archived at and trims session id", func(t *testing.T) {
		session, err := MarkSessionArchived(Session{ID: "  " + testSessionID + "  "}, time.Time{}, expiresAt)
		require.NoError(t, err)
		require.Equal(t, testSessionID, session.ID)
		require.True(t, session.Archived)
		require.False(t, session.ArchivedAt.IsZero())
		require.Equal(t, time.UTC, session.ArchivedAt.Location())
		require.Equal(t, expiresAt, session.ExpiresAt)
	})

	t.Run("normalizes archive fields and preserves session data", func(t *testing.T) {
		location := time.FixedZone("UTC+2", 2*60*60)
		archivedAt := time.Date(2026, 5, 30, 14, 0, 0, 0, location)
		localExpiresAt := time.Date(2026, 5, 31, 14, 0, 0, 0, location)
		createdAt := now.Add(-time.Hour)
		updatedAt := now.Add(-time.Minute)

		session, err := MarkSessionArchived(Session{
			CreatedAt:                  createdAt,
			Compaction:                 SessionCompaction{Status: CompactionStatusSucceeded, TargetOffset: 7},
			ID:                         testSessionID,
			EpisodicCheckpointOffset:   2,
			LastPromptTokens:           300,
			ReflectionCheckpointOffset: 4,
			Title:                      "  Planning  ",
			TitleSource:                "  manual  ",
			UpdatedAt:                  updatedAt,
		}, archivedAt, localExpiresAt)
		require.NoError(t, err)
		require.True(t, session.Archived)
		require.Equal(t, archivedAt.UTC(), session.ArchivedAt)
		require.Equal(t, localExpiresAt.UTC(), session.ExpiresAt)
		require.Equal(t, createdAt, session.CreatedAt)
		require.Equal(t, updatedAt, session.UpdatedAt)
		require.Equal(t, SessionCompaction{Status: CompactionStatusSucceeded, TargetOffset: 7}, session.Compaction)
		require.Equal(t, 2, session.EpisodicCheckpointOffset)
		require.Equal(t, 300, session.LastPromptTokens)
		require.Equal(t, 4, session.ReflectionCheckpointOffset)
		require.Equal(t, "Planning", session.Title)
		require.Equal(t, SessionTitleSourceManual, session.TitleSource)
	})
}

func TestClearSessionArchive(t *testing.T) {
	now := time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC)
	expiresAt := now.Add(24 * time.Hour)

	t.Run("rejects missing session id", func(t *testing.T) {
		session, err := ClearSessionArchive(Session{Archived: true})
		require.EqualError(t, err, "session id is required")
		require.Equal(t, Session{}, session)
	})

	t.Run("rejects invalid session id", func(t *testing.T) {
		session, err := ClearSessionArchive(Session{ID: "ses_invalid", Archived: true})
		require.EqualError(t, err, "session id must be a valid ses_ nanoid")
		require.Equal(t, Session{}, session)
	})

	t.Run("rejects non archived session", func(t *testing.T) {
		session, err := ClearSessionArchive(Session{ID: testSessionID})
		require.EqualError(t, err, "session is not archived")
		require.Equal(t, Session{}, session)
	})

	t.Run("clears archive fields and preserves session data", func(t *testing.T) {
		createdAt := now.Add(-time.Hour)
		updatedAt := now.Add(-time.Minute)

		session, err := ClearSessionArchive(Session{
			ArchivedAt:                 now,
			CreatedAt:                  createdAt,
			ExpiresAt:                  expiresAt,
			Compaction:                 SessionCompaction{Status: CompactionStatusPending, TargetOffset: 6},
			ID:                         "  " + testSessionID + "  ",
			Archived:                   true,
			EpisodicCheckpointOffset:   3,
			LastPromptTokens:           250,
			ReflectionCheckpointOffset: 5,
			Title:                      "Planning",
			TitleSource:                SessionTitleSourceManual,
			UpdatedAt:                  updatedAt,
		})
		require.NoError(t, err)
		require.False(t, session.Archived)
		require.True(t, session.ArchivedAt.IsZero())
		require.True(t, session.ExpiresAt.IsZero())
		require.Equal(t, testSessionID, session.ID)
		require.Equal(t, createdAt, session.CreatedAt)
		require.Equal(t, updatedAt, session.UpdatedAt)
		require.Equal(t, SessionCompaction{Status: CompactionStatusPending, TargetOffset: 6}, session.Compaction)
		require.Equal(t, 3, session.EpisodicCheckpointOffset)
		require.Equal(t, 250, session.LastPromptTokens)
		require.Equal(t, 5, session.ReflectionCheckpointOffset)
		require.Equal(t, "Planning", session.Title)
		require.Equal(t, SessionTitleSourceManual, session.TitleSource)
	})
}

func TestNormalizeSessionTitleMetadata(t *testing.T) {
	title, source := NormalizeSessionTitleMetadata("  Planning Notes  ", "  manual  ")
	require.Equal(t, "Planning Notes", title)
	require.Equal(t, SessionTitleSourceManual, source)

	title, source = NormalizeSessionTitleMetadata("Planning Notes", "unknown")
	require.Equal(t, "Planning Notes", title)
	require.Empty(t, source)

	title, source = NormalizeSessionTitleMetadata("  ", SessionTitleSourceGenerated)
	require.Empty(t, title)
	require.Empty(t, source)
}

func TestCloneMessages(t *testing.T) {
	require.Nil(t, CloneMessages(nil))

	createdAt := time.Date(2026, 4, 2, 10, 0, 0, 0, time.UTC)
	original := []morphmsg.Message{{
		Role:      morphmsg.RoleAssistant,
		Content:   "reply",
		CreatedAt: createdAt,
		ToolCalls: []morphmsg.ToolCall{{
			ID:    "call-1",
			Name:  "lookup",
			Input: "{\"q\":\"hello\"}",
		}},
	}}

	cloned := CloneMessages(original)
	require.Equal(t, original, cloned)

	original[0].Content = "changed"
	original[0].ToolCalls[0].Name = "mutated"

	require.Equal(t, "reply", cloned[0].Content)
	require.Equal(t, "lookup", cloned[0].ToolCalls[0].Name)
}

func TestNormalizeSessionSummary(t *testing.T) {
	t.Run("rejects missing session id", func(t *testing.T) {
		summary, err := NormalizeSessionSummary(SessionSummary{})
		require.EqualError(t, err, "session id is required")
		require.Equal(t, SessionSummary{}, summary)
	})

	t.Run("rejects invalid session id", func(t *testing.T) {
		summary, err := NormalizeSessionSummary(SessionSummary{
			SessionID:      "ses_invalid",
			SessionSummary: "summary",
		})
		require.EqualError(t, err, "session id must be a valid ses_ nanoid")
		require.Equal(t, SessionSummary{}, summary)
	})

	t.Run("rejects missing summary", func(t *testing.T) {
		summary, err := NormalizeSessionSummary(SessionSummary{
			SessionID: testSessionID,
		})
		require.EqualError(t, err, "session summary is required")
		require.Equal(t, SessionSummary{}, summary)
	})

	t.Run("rejects negative source end offset", func(t *testing.T) {
		summary, err := NormalizeSessionSummary(SessionSummary{
			SessionID:       testSessionID,
			SessionSummary:  "summary",
			SourceEndOffset: -1,
		})
		require.EqualError(t, err, "summary source end offset must be greater than or equal to zero")
		require.Equal(t, SessionSummary{}, summary)
	})

	t.Run("rejects negative source message count", func(t *testing.T) {
		summary, err := NormalizeSessionSummary(SessionSummary{
			SessionID:          testSessionID,
			SessionSummary:     "summary",
			SourceMessageCount: -1,
		})
		require.EqualError(t, err, "summary source message count must be greater than or equal to zero")
		require.Equal(t, SessionSummary{}, summary)
	})

	t.Run("rejects source end offset larger than source count", func(t *testing.T) {
		summary, err := NormalizeSessionSummary(SessionSummary{
			SessionID:          testSessionID,
			SessionSummary:     "summary",
			SourceEndOffset:    3,
			SourceMessageCount: 2,
		})
		require.EqualError(t, err, "summary source end offset cannot exceed source message count")
		require.Equal(t, SessionSummary{}, summary)
	})

	t.Run("defaults updated at and trims strings", func(t *testing.T) {
		summary, err := NormalizeSessionSummary(SessionSummary{
			SessionID:          "  " + DefaultSessionID + "  ",
			SessionSummary:     "  summary  ",
			CurrentTask:        "  task  ",
			SourceEndOffset:    2,
			SourceMessageCount: 2,
			Discoveries:        []string{" one ", "", "   "},
			OpenQuestions:      []string{" two "},
			NextActions:        []string{" three ", ""},
		})
		require.NoError(t, err)
		require.Equal(t, DefaultSessionID, summary.SessionID)
		require.Equal(t, "summary", summary.SessionSummary)
		require.Equal(t, "task", summary.CurrentTask)
		require.False(t, summary.UpdatedAt.IsZero())
		require.Equal(t, time.UTC, summary.UpdatedAt.Location())
		require.Equal(t, []string{"one"}, summary.Discoveries)
		require.Equal(t, []string{"two"}, summary.OpenQuestions)
		require.Equal(t, []string{"three"}, summary.NextActions)
	})

	t.Run("normalizes updated at to utc", func(t *testing.T) {
		location := time.FixedZone("UTC+2", 2*60*60)
		updatedAt := time.Date(2026, 4, 2, 14, 0, 0, 0, location)

		summary, err := NormalizeSessionSummary(SessionSummary{
			SessionID:          testSessionID,
			SessionSummary:     "summary",
			UpdatedAt:          updatedAt,
			SourceEndOffset:    1,
			SourceMessageCount: 2,
		})
		require.NoError(t, err)
		require.Equal(t, updatedAt.UTC(), summary.UpdatedAt)
		require.Nil(t, summary.Discoveries)
		require.Nil(t, summary.OpenQuestions)
		require.Nil(t, summary.NextActions)
	})
}

func TestCloneSessionSummary(t *testing.T) {
	original := SessionSummary{
		SessionID:      testSessionID,
		SessionSummary: "summary",
		Discoveries:    []string{" one ", "", "two"},
		OpenQuestions:  []string{" three "},
		NextActions:    []string{" four "},
	}

	cloned := CloneSessionSummary(original)
	require.Equal(t, []string{"one", "two"}, cloned.Discoveries)
	require.Equal(t, []string{"three"}, cloned.OpenQuestions)
	require.Equal(t, []string{"four"}, cloned.NextActions)

	original.Discoveries[0] = "changed"
	original.OpenQuestions[0] = "changed"
	original.NextActions[0] = "changed"

	require.Equal(t, []string{"one", "two"}, cloned.Discoveries)
	require.Equal(t, []string{"three"}, cloned.OpenQuestions)
	require.Equal(t, []string{"four"}, cloned.NextActions)

	empty := CloneSessionSummary(SessionSummary{})
	require.Nil(t, empty.Discoveries)
	require.Nil(t, empty.OpenQuestions)
	require.Nil(t, empty.NextActions)

	whitespaceOnly := CloneSessionSummary(SessionSummary{
		Discoveries:   []string{" ", "\t"},
		OpenQuestions: []string{""},
		NextActions:   []string{"   "},
	})
	require.Nil(t, whitespaceOnly.Discoveries)
	require.Nil(t, whitespaceOnly.OpenQuestions)
	require.Nil(t, whitespaceOnly.NextActions)
}
