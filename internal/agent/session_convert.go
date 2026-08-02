package agent

import (
	agentsession "github.com/xymorphic/morph/pkg/agent/session"

	storage "github.com/xymorphic/morph/internal/state/core"
)

func agentSessionFromStorageSession(value storage.Session) agentsession.Session {
	return agentsession.Session{
		Compaction:                 agentCompactionFromStorageCompaction(value.Compaction),
		Origin:                     agentOriginFromStorageOrigin(value.Origin),
		ID:                         value.ID,
		ReasoningEffortOverride:    value.ReasoningEffortOverride,
		EpisodicCheckpointOffset:   value.EpisodicCheckpointOffset,
		LastPromptTokens:           value.LastPromptTokens,
		ReflectionCheckpointOffset: value.ReflectionCheckpointOffset,
		Title:                      value.Title,
		TitleSource:                value.TitleSource,
		Archived:                   value.Archived,
		ArchivedAt:                 value.ArchivedAt,
		CreatedAt:                  value.CreatedAt,
		UpdatedAt:                  value.UpdatedAt,
		ExpiresAt:                  value.ExpiresAt,
	}
}

func agentOriginFromStorageOrigin(value storage.SessionOrigin) agentsession.Origin {
	return agentsession.Origin{
		AccountID:      value.AccountID,
		ConversationID: value.ConversationID,
		Source:         value.Source,
		ThreadID:       value.ThreadID,
	}
}

func agentCompactionFromStorageCompaction(value storage.SessionCompaction) agentsession.Compaction {
	return agentsession.Compaction{
		CompletedAt:        value.CompletedAt,
		FailedAt:           value.FailedAt,
		LastError:          value.LastError,
		RequestedAt:        value.RequestedAt,
		StartedAt:          value.StartedAt,
		Status:             agentsession.CompactionStatus(value.Status),
		TargetMessageCount: value.TargetMessageCount,
		TargetOffset:       value.TargetOffset,
	}
}
