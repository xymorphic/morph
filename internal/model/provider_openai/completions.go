package provider_openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/shared"

	models "github.com/xymorphic/morph/internal/model"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/str"
)

// chatCompletionsHandler handles chat completions requests.
type chatCompletionsHandler struct{}

func (chatCompletionsHandler) Complete(
	ctx context.Context,
	client *OpenAIClient,
	req normalizedGenerateRequest,
	stream bool,
	onTextDelta func(StreamDelta),
) (*Response, error) {
	params := buildChatCompletionsRequest(req)
	if stream {
		params.StreamOptions = openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		}
	}
	if req.DebugRequests {
		logRequestDebugMetadata(req)
	}

	if stream {
		if client.createChatStream == nil {
			return nil, errors.New("model client is required")
		}

		return client.completeChatStream(ctx, params, onTextDelta)
	}

	if client.createChatCompletion == nil {
		return nil, errors.New("model client is required")
	}
	providerResp, callErr := client.createChatCompletion(ctx, params)
	if callErr != nil {
		return nil, callErr
	}
	if providerResp == nil {
		return nil, errors.New("model response is required")
	}
	return extractChatCompletionsResponse(providerResp)
}

func (c *OpenAIClient) completeChatStream(
	ctx context.Context,
	params openai.ChatCompletionNewParams,
	onTextDelta func(StreamDelta),
) (*Response, error) {
	stream := c.createChatStream(ctx, params)
	if stream == nil {
		return nil, errors.New("model response is required")
	}

	acc := openai.ChatCompletionAccumulator{}
	for stream.Next() {
		chunk := stream.Current()
		if !acc.AddChunk(chunk) {
			return nil, errors.New("failed to accumulate chat completion stream")
		}

		if onTextDelta != nil {
			for _, choice := range chunk.Choices {
				if reasoning := getChatStreamReasoningDelta(choice.Delta); reasoning != "" {
					onTextDelta(StreamDelta{Channel: models.StreamChannelReasoning, Text: reasoning})
				}
				if choice.Delta.Content != "" {
					onTextDelta(StreamDelta{Channel: models.StreamChannelAssistant, Text: choice.Delta.Content})
				}
			}
		}
	}
	if err := stream.Err(); err != nil {
		return nil, err
	}

	return extractChatCompletionsResponse(&acc.ChatCompletion)
}

func getChatStreamReasoningDelta(delta openai.ChatCompletionChunkChoiceDelta) string {
	for _, key := range []string{"reasoning", "reasoning_content", "reasoning_text"} {
		field, ok := delta.JSON.ExtraFields[key]
		if !ok || !field.Valid() {
			continue
		}

		var text string
		if err := json.Unmarshal([]byte(field.Raw()), &text); err == nil {
			return text
		}
	}

	var rawFields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(delta.RawJSON()), &rawFields); err != nil {
		return ""
	}
	for _, key := range []string{"reasoning", "reasoning_content", "reasoning_text"} {
		raw, ok := rawFields[key]
		if !ok {
			continue
		}

		var text string
		if err := json.Unmarshal(raw, &text); err == nil {
			return text
		}
	}

	return ""
}

func buildChatCompletionsRequest(req normalizedGenerateRequest) openai.ChatCompletionNewParams {
	messages := make([]openai.ChatCompletionMessageParamUnion, 0, len(req.Messages)+1)
	if req.Instructions != "" {
		messages = append(messages, openai.DeveloperMessage(req.Instructions))
	}

	for _, message := range req.Messages {
		switch message.Role {
		case morphmsg.RoleUser:
			messages = append(messages, openai.UserMessage(message.Content))
		case morphmsg.RoleAssistant:
			if len(message.ToolCalls) == 0 {
				messages = append(messages, openai.AssistantMessage(message.Content))
				continue
			}

			toolCalls := make([]openai.ChatCompletionMessageToolCallUnionParam, 0, len(message.ToolCalls))
			for _, toolCall := range message.ToolCalls {
				toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallUnionParam{
					OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
						ID: toolCall.ID,
						Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
							Name:      toolCall.Name,
							Arguments: toolCall.Input,
						},
					},
				})
			}

			assistant := openai.ChatCompletionAssistantMessageParam{ToolCalls: toolCalls}
			if message.Content != "" {
				assistant.Content = openai.ChatCompletionAssistantMessageParamContentUnion{
					OfString: openai.String(message.Content),
				}
			}
			messages = append(messages, openai.ChatCompletionMessageParamUnion{OfAssistant: &assistant})
		case morphmsg.RoleTool:
			messages = append(messages, openai.ToolMessage(message.Content, message.ToolCallID))
		}
	}

	params := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(req.Model),
		Messages: messages,
	}
	if req.Reasoning != nil {
		params.ReasoningEffort = shared.ReasoningEffort(req.Reasoning.Effort)
	}
	if len(req.Tools) > 0 {
		params.Tools = buildChatCompletionsTools(req.Tools)
	}
	if req.MaxOutputTokens > 0 {
		params.MaxCompletionTokens = openai.Int(req.MaxOutputTokens)
	}
	if req.Temperature > 0 {
		params.Temperature = openai.Float(req.Temperature)
	}
	if req.StructuredOutput != nil {
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:        req.StructuredOutput.Name,
					Description: openai.String(req.StructuredOutput.Description),
					Schema:      req.StructuredOutput.Schema,
					Strict:      openai.Bool(req.StructuredOutput.Strict),
				},
			},
		}
	}

	return params
}

func extractChatCompletionsResponse(resp *openai.ChatCompletion) (*Response, error) {
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("chat completion response %q contained no choices", resp.ID)
	}

	message := resp.Choices[0].Message
	toolCalls, err := extractChatCompletionsToolCalls(message.ToolCalls)
	if err != nil {
		return nil, err
	}
	contentValue := str.String(message.Content)
	outputText := contentValue.Trim()
	if outputText == "" {
		refusalValue := str.String(message.Refusal)
		outputText = refusalValue.Trim()
	}
	if outputText == "" && len(toolCalls) == 0 {
		return nil, errors.New("model returned empty response")
	}

	return &Response{
		ID:                resp.ID,
		Model:             resp.Model,
		OutputText:        outputText,
		ToolCalls:         toolCalls,
		RequiresToolCalls: len(toolCalls) > 0,
		PromptTokens:      int(resp.Usage.PromptTokens),
		CompletionTokens:  int(resp.Usage.CompletionTokens),
		TotalTokens:       int(resp.Usage.TotalTokens),
	}, nil
}

func buildChatCompletionsTools(definitions []ToolDefinition) []openai.ChatCompletionToolUnionParam {
	tools := make([]openai.ChatCompletionToolUnionParam, 0, len(definitions))
	for _, definition := range definitions {
		tools = append(tools, openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name:        definition.Name,
			Description: openai.String(definition.Description),
			Parameters:  shared.FunctionParameters(normalizeStrictJSONSchema(definition.InputSchema)),
			Strict:      openai.Bool(true),
		}))
	}

	return tools
}

func extractChatCompletionsToolCalls(toolCalls []openai.ChatCompletionMessageToolCallUnion) ([]ToolCall, error) {
	if len(toolCalls) == 0 {
		return nil, nil
	}

	normalized := make([]ToolCall, 0, len(toolCalls))
	for idx, toolCall := range toolCalls {
		iDValue := str.String(toolCall.ID)
		id := iDValue.Trim()
		nameValue := str.String(toolCall.Function.Name)
		name := nameValue.Trim()
		if name == "" {
			return nil, errors.New("tool call name is required")
		}
		if id == "" {
			id = getFallbackToolCallID(name, idx)
		}
		argumentsValue := str.String(toolCall.Function.Arguments)
		normalized = append(normalized, ToolCall{
			ID:    id,
			Name:  name,
			Input: argumentsValue.Trim(),
		})
	}

	return normalized, nil
}

func getFallbackToolCallID(name string, index int) string {
	nameValue2 := str.String(name)
	return "functions." + nameValue2.Trim() + ":" + strconv.Itoa(index)
}
