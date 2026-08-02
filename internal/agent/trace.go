package agent

import (
	agentsession "github.com/xymorphic/morph/pkg/agent/session"

	storage "github.com/xymorphic/morph/internal/state/core"
)

func storageTraceEventFromAgentTraceEvent(value agentsession.TraceEvent) storage.TraceEvent {
	return storage.TraceEvent{
		ID:        value.ID,
		SessionID: value.SessionID,
		Sequence:  value.Sequence,
		Type:      value.Type,
		Timestamp: value.Timestamp,
		Payload:   value.Payload,
	}
}

func agentTraceEventFromStorageTraceEvent(value storage.TraceEvent) agentsession.TraceEvent {
	return agentsession.TraceEvent{
		ID:        value.ID,
		SessionID: value.SessionID,
		Sequence:  value.Sequence,
		Type:      value.Type,
		Timestamp: value.Timestamp,
		Payload:   value.Payload,
	}
}
