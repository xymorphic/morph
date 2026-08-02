package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	storage "github.com/xymorphic/morph/internal/state/core"
	agentsession "github.com/xymorphic/morph/pkg/agent/session"
)

func TestTrace_ConvertsAgentAndStorageTraceEvents(t *testing.T) {
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	agentEvent := agentsession.TraceEvent{
		ID:        4,
		SessionID: "session",
		Sequence:  9,
		Type:      "model.request",
		Timestamp: now,
		Payload:   map[string]any{"ok": true},
	}

	storageEvent := storageTraceEventFromAgentTraceEvent(agentEvent)
	require.Equal(t, storage.TraceEvent{
		ID:        4,
		SessionID: "session",
		Sequence:  9,
		Type:      "model.request",
		Timestamp: now,
		Payload:   map[string]any{"ok": true},
	}, storageEvent)
	require.Equal(t, agentEvent, agentTraceEventFromStorageTraceEvent(storageEvent))
}
