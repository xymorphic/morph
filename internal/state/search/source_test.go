package search

import (
	"testing"

	"github.com/stretchr/testify/require"

	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
)

func TestSourceIDForMessage(t *testing.T) {
	require.Equal(t, "session_message:ses_a:1", SourceIDForMessage("ses_a", 1))
	require.Equal(t, "session_message:ses_a:1", SourceIDForMessage(" ses_a ", 1))
}

func TestSourceIDsFromMessages(t *testing.T) {
	require.Equal(t, []string{
		SourceIDForMessage("ses_a", 1),
		SourceIDForMessage("ses_a", 2),
	}, SourceIDsFromMessages("ses_a", []morphmsg.Message{{ID: 1}, {ID: 2}}))
	require.Nil(t, SourceIDsFromMessages("ses_a", nil))
}

func TestMessageRefFromSourceID(t *testing.T) {
	sessionID, messageID, ok := MessageRefFromSourceID(SourceIDForMessage("ses_a", 2))
	require.True(t, ok)
	require.Equal(t, "ses_a", sessionID)
	require.Equal(t, uint(2), messageID)

	tests := []string{
		"bad",
		string(SourceKindSessionMessage) + ":ses_a:",
		SourceIDForMessage("ses_a", 0),
	}
	for _, sourceID := range tests {
		t.Run(sourceID, func(t *testing.T) {
			_, _, ok := MessageRefFromSourceID(sourceID)
			require.False(t, ok)
		})
	}
}

func TestStableMemoryItemID(t *testing.T) {
	require.Equal(t, "memory_item:mem_a", StableMemoryItemID("mem_a"))
	require.Equal(t, "memory_item:mem_a", StableMemoryItemID(" mem_a "))
}

func TestMemoryIDFromSourceID(t *testing.T) {
	id, ok := MemoryIDFromSourceID("memory_item:mem_test")
	require.True(t, ok)
	require.Equal(t, "mem_test", id)

	id, ok = MemoryIDFromSourceID("session_message:default:1")
	require.False(t, ok)
	require.Empty(t, id)

	id, ok = MemoryIDFromSourceID("memory_item: ")
	require.False(t, ok)
	require.Empty(t, id)
}
