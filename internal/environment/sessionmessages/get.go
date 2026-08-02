package sessionmessages

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	storage "github.com/xymorphic/morph/internal/state/core"
	statemanager "github.com/xymorphic/morph/internal/state/manager"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/str"
)

// Get returns session messages selected by offset, message ID, or anchor window.
func Get(
	ctx context.Context,
	manager *statemanager.Manager,
	req SessionMessagesRequest,
) (SessionMessagesResponse, error) {
	if manager == nil {
		return SessionMessagesResponse{}, errors.New("state manager is required")
	}
	if err := req.Validate(); err != nil {
		return SessionMessagesResponse{}, err
	}

	sessionIDValue := str.String(req.SessionID)
	sessionID := sessionIDValue.Trim()
	if sessionID == "" {
		currentSessionID, err := manager.CurrentSession(ctx)
		if err != nil {
			return SessionMessagesResponse{}, err
		}
		sessionID = currentSessionID
	}

	selector, _ := req.Selector()

	switch selector {
	case SessionMessagesSelectorOffsetRange:
		start := *req.OffsetStart
		end := *req.OffsetEnd
		messages, err := manager.GetMessages(ctx, sessionID, storage.MessageQueryOptions{
			Offset: start,
			Limit:  end - start,
		})
		if err != nil {
			return SessionMessagesResponse{}, err
		}

		return buildSessionMessagesResponse(sessionID, messagesToMessageRecords(start, messages), req.MaxChars), nil
	case SessionMessagesSelectorAnchor:
		records, err := manager.GetMessageWindow(ctx, sessionID, req.AnchorMessageID, req.Before, req.After)
		if err != nil {
			return SessionMessagesResponse{}, err
		}
		return buildSessionMessagesResponse(sessionID, records, req.MaxChars), nil
	default:
		records, err := manager.GetMessagesByIDs(ctx, sessionID, req.MessageIDs)
		if err != nil {
			return SessionMessagesResponse{}, err
		}
		return buildSessionMessagesResponse(sessionID, records, req.MaxChars), nil
	}
}

func buildSessionMessagesResponse(
	sessionID string,
	records []storage.MessageRecord,
	maxChars int,
) SessionMessagesResponse {
	sessionIDValue2 := str.String(sessionID)
	response := SessionMessagesResponse{
		SessionID: sessionIDValue2.Trim(),
		Messages:  make([]SessionMessageRecord, 0, len(records)),
	}

	for _, messageRecord := range records {
		message := messageRecord.Message
		nameValue := str.String(message.Name)
		toolCallIDValue := str.String(message.ToolCallID)
		record := SessionMessageRecord{
			MessageID:  message.ID,
			Offset:     messageRecord.Offset,
			Role:       string(message.Role),
			Name:       nameValue.Trim(),
			ToolName:   getMessageToolName(message),
			ToolCallID: toolCallIDValue.Trim(),
			CreatedAt:  formatMessageTime(message.CreatedAt),
		}

		record.Content, record.Truncated = truncateMessageContent(message.Content, maxChars)
		if record.Truncated {
			response.Truncated = true
		}

		record.ToolCalls = buildToolCallRecords(message.ToolCalls, maxChars)
		for _, toolCall := range record.ToolCalls {
			if toolCall.Truncated {
				record.Truncated = true
				response.Truncated = true
				break
			}
		}

		response.Messages = append(response.Messages, record)
	}

	return response
}

func messagesToMessageRecords(start int, messages []morphmsg.Message) []storage.MessageRecord {
	records := make([]storage.MessageRecord, 0, len(messages))
	for idx, message := range messages {
		records = append(records, storage.MessageRecord{
			Offset:  start + idx,
			Message: message,
		})
	}

	return records
}

func getMessageToolName(message morphmsg.Message) string {
	if message.Role == morphmsg.RoleTool {
		nameValue2 := str.String(message.Name)
		return nameValue2.Trim()
	}
	if len(message.ToolCalls) == 1 {
		nameValue3 := str.String(message.ToolCalls[0].Name)
		return nameValue3.Trim()
	}

	return ""
}

func buildToolCallRecords(toolCalls []morphmsg.ToolCall, maxChars int) []SessionToolCallRecord {
	if len(toolCalls) == 0 {
		return nil
	}

	records := make([]SessionToolCallRecord, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		input, truncated := truncateMessageContent(toolCall.Input, maxChars)
		iDValue := str.String(toolCall.ID)
		nameValue4 := str.String(toolCall.Name)
		records = append(records, SessionToolCallRecord{
			ID:        iDValue.Trim(),
			Name:      nameValue4.Trim(),
			Input:     input,
			Truncated: truncated,
		})
	}

	return records
}

func truncateMessageContent(content string, maxChars int) (string, bool) {
	if !utf8.ValidString(content) {
		content = strings.ToValidUTF8(content, "")
	}
	if maxChars <= 0 {
		return content, false
	}

	runes := []rune(content)
	if len(runes) <= maxChars {
		return content, false
	}

	return string(runes[:maxChars]), true
}

func formatMessageTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}

	return value.UTC().Format("2006-01-02T15:04:05Z07:00")
}
