package search

import (
	"strconv"
	"strings"
	"time"

	state "github.com/xymorphic/morph/internal/state/core"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/str"
)

// MessageIndexRow is one searchable text row derived from a session message.
type MessageIndexRow struct {
	CreatedAt    time.Time
	UpdatedAt    time.Time
	MessageID    uint
	SessionID    string
	Role         string
	ToolName     string
	Body         string
	SemanticBody string
}

// MessageIndexRowsFromMessage converts a message into the text rows used by lexical and vector search.
func MessageIndexRowsFromMessage(sessionID string, message morphmsg.Message) []MessageIndexRow {
	sessionIDValue := str.String(sessionID)
	baseRow := MessageIndexRow{
		CreatedAt: message.CreatedAt,
		UpdatedAt: message.CreatedAt,
		MessageID: message.ID,
		SessionID: sessionIDValue.Trim(),
		Role:      state.NormalizeMatchValue(string(message.Role)),
	}
	contentValue := str.String(message.Content)
	body := contentValue.Trim()
	switch message.Role {
	case morphmsg.RoleAssistant:
		rows := make([]MessageIndexRow, 0, len(message.ToolCalls)+1)
		if body != "" {
			row := baseRow
			row.Body = body
			row.SemanticBody = body
			rows = append(rows, row)
		}
		for _, toolCall := range message.ToolCalls {
			toolCallSearchTextValue := str.String(morphmsg.ToolCallSearchText(toolCall))
			toolBody := toolCallSearchTextValue.Trim()
			if toolBody == "" {
				continue
			}
			row := baseRow
			row.ToolName = state.NormalizeMatchValue(toolCall.Name)
			row.Body = toolBody
			rows = append(rows, row)
		}
		if len(rows) == 0 {
			return nil
		}
		return rows
	case morphmsg.RoleTool:
		if body == "" {
			return nil
		}
		row := baseRow
		row.ToolName = state.NormalizeMatchValue(message.Name)
		row.Body = body
		row.SemanticBody = str.String(message.SemanticContent).Trim()
		return []MessageIndexRow{row}
	default:
		if body == "" {
			return nil
		}
		row := baseRow
		row.Body = body
		row.SemanticBody = body
		return []MessageIndexRow{row}
	}
}

// MessageIndexRowForVectorRecord returns the searchable row represented by a vector record ID.
func MessageIndexRowForVectorRecord(
	rows []MessageIndexRow,
	vectorID string,
	options VectorChunkOptions,
) (MessageIndexRow, bool) {
	if len(rows) == 0 {
		return MessageIndexRow{}, false
	}

	idx := strings.LastIndex(vectorID, ":row:")
	if idx < 0 {
		return MessageIndexRow{}, false
	}
	parts := strings.Split(vectorID[idx+5:], ":chunk:")
	if len(parts) != 2 {
		return MessageIndexRow{}, false
	}
	rowNumber, err := strconv.Atoi(parts[0])
	if err != nil || rowNumber <= 0 || rowNumber > len(rows) {
		return MessageIndexRow{}, false
	}
	chunkNumber, err := strconv.Atoi(parts[1])
	if err != nil || chunkNumber <= 0 {
		return MessageIndexRow{}, false
	}

	row := rows[rowNumber-1]
	if vectorID[:idx] != SourceIDForMessage(row.SessionID, row.MessageID) {
		return MessageIndexRow{}, false
	}
	options = NormalizeVectorChunkOptions(options)
	chunks, _ := ChunkVectorText(row.SemanticBody, options)
	if chunkNumber > len(chunks) {
		return MessageIndexRow{}, false
	}
	row.Body = chunks[chunkNumber-1]
	return row, true
}

// MessageIndexRowMatchesSearchOptions reports whether a row satisfies non-text search filters.
func MessageIndexRowMatchesSearchOptions(row MessageIndexRow, opts state.SearchMessageOptions) bool {
	if toolName := state.NormalizeMatchValue(opts.ToolName); toolName != "" && row.ToolName != toolName {
		return false
	}

	return true
}
