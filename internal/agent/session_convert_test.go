package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	storage "github.com/wandxy/morph/internal/state/core"
	agentsession "github.com/wandxy/morph/pkg/agent/session"
)

func TestSessionConvert_ConvertsStorageSessionAndCompaction(t *testing.T) {
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	archivedAt := now.Add(time.Minute)
	expiresAt := now.Add(24 * time.Hour)
	session := agentSessionFromStorageSession(storage.Session{
		ArchivedAt:                 archivedAt,
		CreatedAt:                  now,
		ExpiresAt:                  expiresAt,
		Compaction:                 storage.SessionCompaction{Status: storage.CompactionStatusFailed, TargetOffset: 8, TargetMessageCount: 11, LastError: "failed"},
		Origin:                     storage.SessionOrigin{Source: storage.SessionOriginSourceTelegram, ConversationID: "-100"},
		ID:                         "session",
		ReasoningEffortOverride:    "future-effort",
		Archived:                   true,
		EpisodicCheckpointOffset:   2,
		LastPromptTokens:           500,
		ReflectionCheckpointOffset: 3,
		Title:                      "Title",
		TitleSource:                storage.SessionTitleSourceGenerated,
		UpdatedAt:                  now.Add(time.Minute),
	})

	require.Equal(t, agentsession.Session{
		ArchivedAt:                 archivedAt,
		CreatedAt:                  now,
		ExpiresAt:                  expiresAt,
		Compaction:                 agentsession.Compaction{Status: agentsession.CompactionStatusFailed, TargetOffset: 8, TargetMessageCount: 11, LastError: "failed"},
		Origin:                     agentsession.Origin{Source: storage.SessionOriginSourceTelegram, ConversationID: "-100"},
		ID:                         "session",
		ReasoningEffortOverride:    "future-effort",
		Archived:                   true,
		EpisodicCheckpointOffset:   2,
		LastPromptTokens:           500,
		ReflectionCheckpointOffset: 3,
		Title:                      "Title",
		TitleSource:                storage.SessionTitleSourceGenerated,
		UpdatedAt:                  now.Add(time.Minute),
	}, session)
}
