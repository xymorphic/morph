package search

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
)

func TestVectorInputsFromIndexRows(t *testing.T) {
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
	}, {
		MessageID: 1,
		SessionID: "ses_a",
		Role:      string(morphmsg.RoleAssistant),
		Body:      `tool_call: process {"action":"read"}`,
	}}

	inputs := VectorInputsFromIndexRows(rows, VectorChunkOptions{})
	require.Len(t, inputs, 2)
	require.Equal(t, SourceIDForMessage("ses_a", 1)+":row:1:chunk:1", inputs[0].ID)
	require.Equal(t, SourceIDForMessage("ses_a", 1)+":row:2:chunk:1", inputs[1].ID)
	require.Equal(t, "process", inputs[1].ToolName)
	require.Nil(t, VectorInputsFromIndexRows(nil, VectorChunkOptions{}))
}

func TestGetVectorInputDiagnostics(t *testing.T) {
	rows := []MessageIndexRow{{
		MessageID: 1, SessionID: "ses_a", ToolName: "browser", SemanticBody: "123456789",
	}, {
		MessageID: 2, SessionID: "ses_a", ToolName: "process", SemanticBody: "short",
	}}
	options := VectorChunkOptions{MaxInputBytes: 4, MaxDocumentBytes: 8}
	inputs := VectorInputsFromIndexRows(rows, options)

	diagnostics := GetVectorInputDiagnostics(rows, inputs, options)

	require.Equal(t, []string{
		SourceIDForMessage("ses_a", 1),
		SourceIDForMessage("ses_a", 2),
	}, diagnostics.SourceIDs)
	require.Equal(t, []string{"browser", "process"}, diagnostics.ToolNames)
	require.Equal(t, len(inputs), diagnostics.ChunkCount)
	require.Equal(t, 1, diagnostics.TruncatedSourceCount)
}

func TestCheckVectorInputSizes(t *testing.T) {
	require.EqualError(t, CheckVectorInputSizes(nil, 0), "vector max input bytes must be greater than zero")
	require.NoError(t, CheckVectorInputSizes([]VectorInput{{ID: "one", Text: "1234"}}, 4))
	require.EqualError(t, CheckVectorInputSizes([]VectorInput{{ID: "one", Text: "12345"}}, 4),
		`vector input "one" exceeds the configured byte limit`)
}

func TestGetMaxVectorInputBytes(t *testing.T) {
	require.Zero(t, GetMaxVectorInputBytes(nil))
	require.Equal(t, 5, GetMaxVectorInputBytes([]VectorInput{{Text: "one"}, {Text: "12345"}}))
}
