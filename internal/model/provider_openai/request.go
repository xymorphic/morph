package provider_openai

import (
	"errors"
	"fmt"
	"slices"

	models "github.com/wandxy/morph/internal/model"
	morphmsg "github.com/wandxy/morph/pkg/agent/message"
	"github.com/wandxy/morph/pkg/str"
)

type normalizedGenerateRequest struct {
	Model            string
	API              string
	Instructions     string
	Messages         []morphmsg.Message
	Tools            []ToolDefinition
	StructuredOutput *StructuredOutput
	MaxOutputTokens  int64
	Temperature      float64
	DebugRequests    bool
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

	api, err := normalizeRequestAPI(req.API)
	if err != nil {
		return normalizedGenerateRequest{}, err
	}
	reasoning, err := normalizeReasoningOptions(req.Reasoning)
	if err != nil {
		return normalizedGenerateRequest{}, err
	}
	instructionsValue := str.String(req.Instructions)
	return normalizedGenerateRequest{
		Model:            model,
		API:              api,
		Instructions:     instructionsValue.Trim(),
		Messages:         messages,
		Tools:            tools,
		StructuredOutput: normalizeStructuredOutput(req.StructuredOutput),
		MaxOutputTokens:  req.MaxOutputTokens,
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
	if effort == "" && value.Summary {
		return &models.ReasoningOptions{Summary: true}, nil
	}
	switch effort {
	case "none", "minimal", "low", "medium", "high", "xhigh", "max":
	default:
		return nil, fmt.Errorf("OpenAI reasoning effort %q is not supported by the installed SDK", effort)
	}
	return &models.ReasoningOptions{Effort: effort, Summary: value.Summary}, nil
}

func normalizeRequestAPI(api string) (string, error) {
	apiValue := str.String(api)
	api = apiValue.Normalized()
	switch api {
	case models.APIOpenAICompletions, models.APIOpenAIResponses:
		return api, nil
	case "":
		return "", errors.New("model API is required")
	default:
		return "", errors.New("model API must be one of: openai-completions, openai-responses")
	}
}

func normalizeStructuredOutput(value *StructuredOutput) *StructuredOutput {
	if value == nil {
		return nil
	}

	nameValue := str.String(value.Name)
	name := nameValue.Trim()
	if name == "" || len(value.Schema) == 0 {
		return nil
	}
	descriptionValue := str.String(value.Description)
	return &StructuredOutput{
		Name:        name,
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
		case morphmsg.RoleDeveloper:
			return nil, errors.New("developer messages must be provided via instructions")
		case morphmsg.RoleUser, morphmsg.RoleAssistant, morphmsg.RoleTool:
		default:
			return nil, errors.New("message role must be one of user, assistant, or tool; developer messages must be provided via instructions")
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

func normalizeStrictJSONSchema(schema map[string]any) map[string]any {
	if len(schema) == 0 {
		return nil
	}

	return normalizeStrictJSONSchemaValue(schema).(map[string]any)
}

func normalizeStrictJSONSchemaValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		cloned := make(map[string]any, len(typed))
		for key, item := range typed {
			cloned[key] = normalizeStrictJSONSchemaValue(item)
		}

		schemaType, _ := cloned["type"].(string)
		properties, _ := cloned["properties"].(map[string]any)
		if schemaType == "object" && len(properties) > 0 {
			originalRequired := getJSONSchemaRequiredNames(typed["required"])
			for key, property := range properties {
				if propertySchema, ok := property.(map[string]any); ok && isUnsupportedStrictJSONObjectProperty(propertySchema) {
					delete(properties, key)
					continue
				}

				if _, required := originalRequired[key]; !required {
					properties[key] = makeStrictJSONSchemaPropertyNullable(property)
				}
			}

			required := make([]string, 0, len(properties))
			for key := range properties {
				required = append(required, key)
			}
			slices.Sort(required)
			cloned["required"] = required
		}

		return cloned
	case []any:
		cloned := make([]any, 0, len(typed))
		for _, item := range typed {
			cloned = append(cloned, normalizeStrictJSONSchemaValue(item))
		}
		return cloned
	default:
		return value
	}
}

func getJSONSchemaRequiredNames(value any) map[string]struct{} {
	result := map[string]struct{}{}
	switch values := value.(type) {
	case []string:
		for _, name := range values {
			result[name] = struct{}{}
		}
	case []any:
		for _, value := range values {
			if name, ok := value.(string); ok {
				result[name] = struct{}{}
			}
		}
	}

	return result
}

func makeStrictJSONSchemaPropertyNullable(value any) any {
	schema, ok := value.(map[string]any)
	if !ok {
		return value
	}

	schemaType, ok := schema["type"].(string)
	if !ok || schemaType == "null" {
		return schema
	}

	schema["type"] = []any{schemaType, "null"}
	return schema
}

func isUnsupportedStrictJSONObjectProperty(schema map[string]any) bool {
	if len(schema) == 0 {
		return false
	}

	schemaType, _ := schema["type"].(string)
	if schemaType != "object" {
		return false
	}

	properties, _ := schema["properties"].(map[string]any)
	if len(properties) > 0 {
		return false
	}

	additionalProperties, ok := schema["additionalProperties"]
	if !ok {
		return false
	}

	switch typed := additionalProperties.(type) {
	case bool:
		return typed
	case map[string]any:
		return true
	default:
		return true
	}
}
