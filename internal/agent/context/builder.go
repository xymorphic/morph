package context

import (
	messages "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/str"
)

// Input contains the sources used to assemble model-visible context for a turn.
type Input struct {
	PrefixMessages  []messages.Message
	SessionHistory  []messages.Message
	EmittedMessages []messages.Message
}

// Builder assembles the model-visible context for a turn.
type Builder struct{}

// New returns a new Builder.
func New() *Builder {
	return &Builder{}
}

// Build assembles the current turn context.
func (b *Builder) Build(input Input) []messages.Message {
	size := len(input.PrefixMessages) + len(input.SessionHistory) + len(input.EmittedMessages)

	if size == 0 {
		return nil
	}

	built := make([]messages.Message, 0, size)
	built = append(built, messages.CloneMessages(input.PrefixMessages)...)
	built = append(built, messages.CloneMessages(input.SessionHistory)...)
	built = append(built, messages.CloneMessages(input.EmittedMessages)...)
	return sanitizeToolCallMessageGroups(built)
}

// sanitizeToolCallMessageGroups keeps assistant tool calls adjacent to their tool results.
func sanitizeToolCallMessageGroups(input []messages.Message) []messages.Message {
	if len(input) == 0 {
		return nil
	}

	sanitized := make([]messages.Message, 0, len(input))
	for index := 0; index < len(input); index++ {
		message := input[index]
		if message.Role == messages.RoleTool {
			continue
		}
		if message.Role != messages.RoleAssistant || len(message.ToolCalls) == 0 {
			sanitized = append(sanitized, message)
			continue
		}

		// OpenAI-style tool calling requires tool results immediately after the
		// assistant message that requested them. If the previous run ended before
		// a result was recorded, synthesize a failure result to keep context valid.
		toolMessages := mapImmediateToolMessages(input, index+1)
		sanitized = append(sanitized, message)
		for _, toolCall := range message.ToolCalls {
			iDValue := str.String(toolCall.ID)
			toolCallID := iDValue.Trim()
			if toolCallID == "" {
				continue
			}
			toolMessage, ok := toolMessages[toolCallID]
			if !ok {
				toolMessage = unavailableToolResultMessage(toolCall)
			}
			sanitized = append(sanitized, toolMessage)
		}

		index += countImmediateToolMessages(input[index+1:])
	}

	return sanitized
}

// mapImmediateToolMessages maps only the contiguous tool result messages after start.
func mapImmediateToolMessages(input []messages.Message, start int) map[string]messages.Message {
	result := map[string]messages.Message{}
	for index := start; index < len(input); index++ {
		message := input[index]
		if message.Role != messages.RoleTool {
			break
		}
		toolCallIDValue := str.String(message.ToolCallID)
		toolCallID := toolCallIDValue.Trim()
		if toolCallID == "" {
			continue
		}
		if _, exists := result[toolCallID]; exists {
			continue
		}
		result[toolCallID] = message
	}

	return result
}

// countImmediateToolMessages counts contiguous tool messages at the start of a slice.
func countImmediateToolMessages(input []messages.Message) int {
	count := 0
	for _, message := range input {
		if message.Role != messages.RoleTool {
			break
		}

		count++
	}

	return count
}

// unavailableToolResultMessage creates a placeholder result for missing persisted tool output.
func unavailableToolResultMessage(toolCall messages.ToolCall) messages.Message {
	nameValue := str.String(toolCall.Name)
	iDValue2 := str.String(toolCall.ID)
	return messages.Message{
		Role:       messages.RoleTool,
		Name:       nameValue.Trim(),
		ToolCallID: iDValue2.Trim(),
		Content:    "[Tool result unavailable: the previous run ended before this tool response was recorded.]",
	}
}
