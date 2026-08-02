package message

import (
	"errors"
	"time"

	"github.com/xymorphic/morph/pkg/str"
)

type Role string

const (
	RoleDeveloper Role = "developer"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	ID              uint
	Role            Role
	Content         string
	Name            string
	ToolCallID      string
	ToolCalls       []ToolCall
	SemanticContent string
	CreatedAt       time.Time
}

type ToolCall struct {
	ID    string
	Name  string
	Input string
}

func New(role Role, content string) (Message, error) {
	normalizedRole, err := normalizeRole(role)
	if err != nil {
		return Message{}, err
	}
	contentValue := str.String(content)
	trimmedContent := contentValue.Trim()
	if trimmedContent == "" {
		return Message{}, errors.New("message content is required")
	}

	return Message{
		Role:      normalizedRole,
		Content:   trimmedContent,
		CreatedAt: time.Now().UTC(),
	}, nil
}

func NewMessage(role Role, content string) (Message, error) {
	return New(role, content)
}

func Normalize(message Message) (Message, error) {
	role, err := normalizeRole(message.Role)
	if err != nil {
		return Message{}, err
	}

	toolCalls, err := normalizeToolCalls(message.ToolCalls)
	if err != nil {
		return Message{}, err
	}
	toolCallIDValue := str.String(message.ToolCallID)
	toolCallID := toolCallIDValue.Trim()
	nameValue := str.String(message.Name)
	name := nameValue.Trim()
	contentValue2 := str.String(message.Content)
	content := contentValue2.Trim()
	semanticContent := str.String(message.SemanticContent).Trim()

	if content == "" && (role != RoleAssistant || len(toolCalls) == 0) {
		return Message{}, errors.New("message content is required")
	}

	if role == RoleTool && toolCallID == "" {
		return Message{}, errors.New("tool call id is required")
	}

	normalized := Message{
		ID:              message.ID,
		Role:            role,
		Content:         content,
		Name:            name,
		ToolCallID:      toolCallID,
		ToolCalls:       toolCalls,
		SemanticContent: semanticContent,
		CreatedAt:       message.CreatedAt.UTC(),
	}

	if normalized.CreatedAt.IsZero() {
		normalized.CreatedAt = time.Now().UTC()
	}

	return normalized, nil
}

func NormalizeMessage(message Message) (Message, error) {
	return Normalize(message)
}

func CloneMessages(messages []Message) []Message {
	if len(messages) == 0 {
		return nil
	}

	cloned := make([]Message, len(messages))
	for i, message := range messages {
		copyMessage := message
		if len(message.ToolCalls) > 0 {
			copyMessage.ToolCalls = make([]ToolCall, len(message.ToolCalls))
			copy(copyMessage.ToolCalls, message.ToolCalls)
		}
		cloned[i] = copyMessage
	}

	return cloned
}

func normalizeToolCalls(toolCalls []ToolCall) ([]ToolCall, error) {
	if len(toolCalls) == 0 {
		return nil, nil
	}

	normalized := make([]ToolCall, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		iDValue := str.String(toolCall.ID)
		id := iDValue.Trim()
		nameValue2 := str.String(toolCall.Name)
		name := nameValue2.Trim()
		inputValue := str.String(toolCall.Input)
		input := inputValue.Trim()

		if id == "" {
			return nil, errors.New("tool call id is required")
		}

		if name == "" {
			return nil, errors.New("tool call name is required")
		}

		normalized = append(normalized, ToolCall{ID: id, Name: name, Input: input})
	}

	return normalized, nil
}

func normalizeRole(role Role) (Role, error) {
	roleValue := str.String(string(role))
	switch Role(roleValue.Normalized()) {
	case RoleDeveloper:
		return RoleDeveloper, nil
	case RoleUser:
		return RoleUser, nil
	case RoleAssistant:
		return RoleAssistant, nil
	case RoleTool:
		return RoleTool, nil
	default:
		return "", errors.New("message role must be one of developer, user, assistant, or tool")
	}
}
