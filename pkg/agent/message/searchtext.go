package message

import (
	"strings"

	"github.com/xymorphic/morph/pkg/jsonterms"
	"github.com/xymorphic/morph/pkg/str"
)

func MessageSearchText(message Message) string {
	switch message.Role {
	case RoleAssistant:
		if len(message.ToolCalls) == 0 {
			return ""
		}
		return ToolCallsSearchText(message.ToolCalls)
	case RoleTool:
		return jsonterms.Terms(message.Content)
	default:
		return ""
	}
}

func ToolCallsSearchText(toolCalls []ToolCall) string {
	if len(toolCalls) == 0 {
		return ""
	}

	parts := make([]string, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		text := ToolCallSearchText(toolCall)
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}

	return strings.Join(parts, "\n")
}

func ToolCallSearchText(toolCall ToolCall) string {
	lines := make([]string, 0, 4)

	name := normalizeSearchTextScalar(toolCall.Name)
	if name != "" {
		lines = append(lines, "tool "+name, "tool", name, "tool_name "+name)
	}

	id := normalizeSearchTextScalar(toolCall.ID)
	if id != "" {
		lines = append(lines, "tool_call_id "+id)
	}

	input := jsonterms.Terms(toolCall.Input, "input")
	if input != "" {
		lines = append(lines, input)
	}

	return dedupeSearchTextLines(lines)
}

func SearchableMessageText(message Message, toolName string) (string, string) {
	switch message.Role {
	case RoleAssistant:
		if len(message.ToolCalls) == 0 {
			if toolName != "" {
				return "", ""
			}

			contentValue := str.String(message.Content)
			return contentValue.Trim(), ""
		}

		matchedToolName := getAssistantToolNameMatch(message.ToolCalls, toolName)
		if toolName != "" && matchedToolName == "" {
			return "", ""
		}

		if toolName != "" {
			for _, toolCall := range message.ToolCalls {
				nameValue := str.String(toolCall.Name)
				if strings.EqualFold(nameValue.Trim(), toolName) {
					nameValue2 := str.String(toolCall.Name)
					return ToolCallSearchText(toolCall), nameValue2.Trim()
				}
			}
		}

		searchText := getAssistantSearchText(message)
		if searchText == "" {
			return "", ""
		}

		if matchedToolName == "" && len(message.ToolCalls) == 1 {
			nameValue3 := str.String(message.ToolCalls[0].Name)
			matchedToolName = nameValue3.Trim()
		}

		return searchText, matchedToolName
	case RoleTool:
		messageName := str.String(message.Name)
		messageToolName := messageName.Trim()
		if toolName != "" && !strings.EqualFold(messageToolName, toolName) {
			return "", ""
		}

		searchText := MessageSearchText(message)
		if searchText == "" {
			contentValue2 := str.String(message.Content)
			searchText = contentValue2.Trim()
		}

		return searchText, messageToolName
	default:
		if toolName != "" {
			return "", ""
		}
		messageContent := str.String(message.Content)
		return messageContent.Trim(), ""
	}
}

func getAssistantToolNameMatch(toolCalls []ToolCall, toolName string) string {
	if toolName == "" {
		return ""
	}

	for _, toolCall := range toolCalls {
		nameValue4 := str.String(toolCall.Name)
		if strings.EqualFold(nameValue4.Trim(), toolName) {
			nameValue5 := str.String(toolCall.Name)
			return nameValue5.Trim()
		}
	}

	return ""
}

func getAssistantSearchText(message Message) string {
	parts := make([]string, 0, 2)
	contentValue3 := str.String(message.Content)
	if content := contentValue3.Trim(); content != "" {
		parts = append(parts, content)
	}

	if toolText := MessageSearchText(message); toolText != "" {
		parts = append(parts, toolText)
	}

	return strings.Join(parts, "\n")
}

func normalizeSearchTextScalar(value string) string {
	valueText := str.String(value).Normalized()
	if valueText == "" {
		return ""
	}
	return strings.Join(strings.Fields(valueText), " ")
}

func dedupeSearchTextLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}

	seen := make(map[string]struct{}, len(lines))
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		lineValue := str.String(line)
		line = lineValue.Trim()
		if line == "" {
			continue
		}
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		out = append(out, line)
	}

	return strings.Join(out, "\n")
}
