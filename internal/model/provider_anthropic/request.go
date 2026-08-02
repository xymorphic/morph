package provider_anthropic

import (
	"errors"
	"fmt"

	models "github.com/xymorphic/morph/internal/model"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/str"
)

const defaultMaxOutputTokens int64 = 4096

type normalizedGenerateRequest struct {
	Model            string
	Instructions     string
	Messages         []morphmsg.Message
	Tools            []ToolDefinition
	StructuredOutput *StructuredOutput
	MaxOutputTokens  int64
	Temperature      float64
	DebugRequests    bool
	SubscriptionAuth bool
	Reasoning        *models.ReasoningOptions
}

func normalizeGenerateRequest(req Request) (normalizedGenerateRequest, error) {
	modelValue := str.String(req.Model)
	model := modelValue.Trim()
	if model == "" {
		return normalizedGenerateRequest{}, errors.New("model is required")
	}

	if len(req.Messages) == 0 {
		return normalizedGenerateRequest{}, errors.New("messages are required")
	}

	messages, err := normalizeMessages(req.Messages)
	if err != nil {
		return normalizedGenerateRequest{}, err
	}
	tools, err := normalizeToolDefinitions(req.Tools)
	if err != nil {
		return normalizedGenerateRequest{}, err
	}

	maxOutputTokens := req.MaxOutputTokens
	if maxOutputTokens <= 0 {
		maxOutputTokens = defaultMaxOutputTokens
	}
	instructionsValue := str.String(req.Instructions)
	reasoning, err := normalizeReasoningOptions(req.Reasoning)
	if err != nil {
		return normalizedGenerateRequest{}, err
	}
	return normalizedGenerateRequest{
		Model:            model,
		Instructions:     instructionsValue.Trim(),
		Messages:         messages,
		Tools:            tools,
		StructuredOutput: normalizeStructuredOutput(req.StructuredOutput),
		MaxOutputTokens:  maxOutputTokens,
		Temperature:      req.Temperature,
		DebugRequests:    req.DebugRequests,
		Reasoning:        reasoning,
	}, nil
}

func normalizeReasoningOptions(value *models.ReasoningOptions) (*models.ReasoningOptions, error) {
	if value == nil {
		return nil, nil
	}
	effort := str.String(value.Effort).Normalized()
	switch effort {
	case "low", "medium", "high", "xhigh", "max":
	default:
		return nil, fmt.Errorf("anthropic reasoning effort %q is not supported by the installed SDK", effort)
	}
	return &models.ReasoningOptions{Effort: effort}, nil
}

func normalizeStructuredOutput(value *StructuredOutput) *StructuredOutput {
	if value == nil || len(value.Schema) == 0 {
		return nil
	}

	nameValue := str.String(value.Name)
	descriptionValue := str.String(value.Description)
	return &StructuredOutput{
		Name:        nameValue.Trim(),
		Description: descriptionValue.Trim(),
		Schema:      value.Schema,
		Strict:      value.Strict,
	}
}

func normalizeMessages(messages []morphmsg.Message) ([]morphmsg.Message, error) {
	normalized := make([]morphmsg.Message, 0, len(messages))
	for _, message := range messages {
		roleValue := str.String(string(message.Role))
		role := morphmsg.Role(roleValue.Normalized())
		contentValue := str.String(message.Content)
		content := contentValue.Trim()
		toolCallIDValue := str.String(message.ToolCallID)
		toolCallID := toolCallIDValue.Trim()
		toolCalls, err := normalizeToolCalls(message.ToolCalls)
		if err != nil {
			return nil, err
		}

		switch role {
		case morphmsg.RoleDeveloper, morphmsg.RoleUser, morphmsg.RoleAssistant, morphmsg.RoleTool:
		default:
			return nil, errors.New("message role must be one of developer, user, assistant, or tool")
		}

		if content == "" && (role != morphmsg.RoleAssistant || len(toolCalls) == 0) {
			return nil, errors.New("message content is required")
		}
		if role == morphmsg.RoleTool && toolCallID == "" {
			return nil, errors.New("tool call id is required")
		}
		nameValue2 := str.String(message.Name)
		normalized = append(normalized, morphmsg.Message{
			Role:       role,
			Content:    content,
			Name:       nameValue2.Trim(),
			ToolCallID: toolCallID,
			ToolCalls:  toolCalls,
			CreatedAt:  message.CreatedAt,
		})
	}

	return normalized, nil
}

func normalizeToolDefinitions(definitions []ToolDefinition) ([]ToolDefinition, error) {
	if len(definitions) == 0 {
		return nil, nil
	}

	normalized := make([]ToolDefinition, 0, len(definitions))
	for _, definition := range definitions {
		nameValue3 := str.String(definition.Name)
		name := nameValue3.Trim()
		if name == "" {
			return nil, errors.New("tool name is required")
		}
		descriptionValue2 := str.String(definition.Description)
		normalized = append(normalized, ToolDefinition{
			Name:         name,
			Description:  descriptionValue2.Trim(),
			InputSchema:  definition.InputSchema,
			ParallelSafe: definition.ParallelSafe,
		})
	}

	return normalized, nil
}

func normalizeToolCalls(toolCalls []morphmsg.ToolCall) ([]morphmsg.ToolCall, error) {
	if len(toolCalls) == 0 {
		return nil, nil
	}

	normalized := make([]morphmsg.ToolCall, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		iDValue := str.String(toolCall.ID)
		id := iDValue.Trim()
		nameValue4 := str.String(toolCall.Name)
		name := nameValue4.Trim()
		if id == "" {
			return nil, errors.New("tool call id is required")
		}
		if name == "" {
			return nil, errors.New("tool call name is required")
		}
		inputValue := str.String(toolCall.Input)
		normalized = append(normalized, morphmsg.ToolCall{
			ID:    id,
			Name:  name,
			Input: inputValue.Trim(),
		})
	}

	return normalized, nil
}
