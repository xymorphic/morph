package search

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
)

func TestMessageIndexRowsFromMessage(t *testing.T) {
	now := time.Now().UTC()

	require.Nil(t, MessageIndexRowsFromMessage("ses_a", morphmsg.Message{Role: morphmsg.RoleUser}))
	require.Nil(t, MessageIndexRowsFromMessage("ses_a", morphmsg.Message{Role: morphmsg.RoleTool, Name: "process"}))
	require.Nil(t, MessageIndexRowsFromMessage("ses_a", morphmsg.Message{
		Role:      morphmsg.RoleAssistant,
		ToolCalls: []morphmsg.ToolCall{{}},
	}))

	rows := MessageIndexRowsFromMessage("ses_a", morphmsg.Message{
		ID:        3,
		Role:      morphmsg.RoleUser,
		Content:   "user body",
		CreatedAt: now,
	})
	require.Len(t, rows, 1)
	require.Equal(t, "user body", rows[0].Body)

	rows = MessageIndexRowsFromMessage(" ses_a ", morphmsg.Message{
		ID:        1,
		Role:      morphmsg.RoleAssistant,
		Content:   "assistant body",
		CreatedAt: now,
		ToolCalls: []morphmsg.ToolCall{{
			ID:    "call-1",
			Name:  "Search Files",
			Input: `{"pattern":"needle"}`,
		}},
	})
	require.Len(t, rows, 2)
	require.Equal(t, "ses_a", rows[0].SessionID)
	require.Equal(t, "assistant body", rows[0].Body)
	require.Equal(t, "search files", rows[1].ToolName)

	rows = MessageIndexRowsFromMessage("ses_a", morphmsg.Message{
		ID:      2,
		Role:    morphmsg.RoleTool,
		Name:    "Plan Tool",
		Content: "tool body",
	})
	require.Len(t, rows, 1)
	require.Equal(t, "plan tool", rows[0].ToolName)
}

func TestMessageIndexRowForVectorRecord(t *testing.T) {
	now := time.Now().UTC()
	rows := []MessageIndexRow{{
		CreatedAt:    now,
		UpdatedAt:    now,
		MessageID:    1,
		SessionID:    "ses_a",
		Role:         string(morphmsg.RoleUser),
		Body:         "first",
		SemanticBody: "first",
	}, {
		CreatedAt:    now,
		UpdatedAt:    now,
		MessageID:    1,
		SessionID:    "ses_a",
		Role:         string(morphmsg.RoleUser),
		ToolName:     "process",
		Body:         "second",
		SemanticBody: "second",
	}}

	sourceID := string(SourceKindSessionMessage) + ":ses_a:1"
	row, ok := MessageIndexRowForVectorRecord(rows, sourceID+":row:2:chunk:1", VectorChunkOptions{})
	require.True(t, ok)
	require.Equal(t, "second", row.Body)

	_, ok = MessageIndexRowForVectorRecord(rows, sourceID, VectorChunkOptions{})
	require.False(t, ok)
	_, ok = MessageIndexRowForVectorRecord(rows, sourceID+":row:1", VectorChunkOptions{})
	require.False(t, ok)
	_, ok = MessageIndexRowForVectorRecord(rows, sourceID+":row:3:chunk:1", VectorChunkOptions{})
	require.False(t, ok)
	_, ok = MessageIndexRowForVectorRecord(rows, sourceID+":row:1:chunk:2", VectorChunkOptions{})
	require.False(t, ok)
	_, ok = MessageIndexRowForVectorRecord(nil, sourceID+":row:1:chunk:1", VectorChunkOptions{})
	require.False(t, ok)

	for _, vectorID := range []string{
		"wrong-source:row:1:chunk:1",
		sourceID + ":row:not-a-number:chunk:1",
		sourceID + ":row:0:chunk:1",
		sourceID + ":row:1:chunk:not-a-number",
		sourceID + ":row:1:chunk:0",
	} {
		_, ok = MessageIndexRowForVectorRecord(rows, vectorID, VectorChunkOptions{})
		require.False(t, ok, vectorID)
	}
}
