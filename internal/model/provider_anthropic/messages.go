package provider_anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"

	models "github.com/wandxy/morph/internal/model"
	morphmsg "github.com/wandxy/morph/pkg/agent/message"
	"github.com/wandxy/morph/pkg/str"
)

func buildMessagesRequest(req normalizedGenerateRequest) (anthropic.MessageNewParams, error) {
	messages := make([]anthropic.MessageParam, 0, len(req.Messages))
	system := make([]anthropic.TextBlockParam, 0, 2)
	if req.SubscriptionAuth {
		system = append(system, anthropic.TextBlockParam{
			Text: "You are Claude Code, Anthropic's official CLI for Claude.",
		})
	}
	if req.Instructions != "" {
		system = append(system, anthropic.TextBlockParam{Text: req.Instructions})
	}

	for _, message := range req.Messages {
		switch message.Role {
		case morphmsg.RoleDeveloper:
			system = append(system, anthropic.TextBlockParam{Text: message.Content})
		case morphmsg.RoleUser:
			messages = append(messages, anthropic.NewUserMessage(anthropic.NewTextBlock(message.Content)))
		case morphmsg.RoleAssistant:
			blocks, err := assistantMessageToAnthropicBlocks(message)
			if err != nil {
				return anthropic.MessageNewParams{}, err
			}
			messages = append(messages, anthropic.NewAssistantMessage(blocks...))
		case morphmsg.RoleTool:
			messages = append(messages, anthropic.NewUserMessage(anthropic.NewToolResultBlock(
				message.ToolCallID,
				message.Content,
				false,
			)))
		}
	}
	if len(messages) == 0 {
		return anthropic.MessageNewParams{}, errors.New("messages must include at least one user, assistant," +
			" or tool message")
	}

	params := anthropic.MessageNewParams{
		MaxTokens: req.MaxOutputTokens,
		Messages:  messages,
		Model:     anthropic.Model(req.Model),
	}
	if len(system) > 0 {
		params.System = system
	}
	if len(req.Tools) > 0 {
		params.Tools = buildAnthropicTools(req.Tools)
	}
	if req.Temperature > 0 {
		params.Temperature = anthropic.Float(req.Temperature)
	}
	if req.StructuredOutput != nil {
		params.OutputConfig.Format = anthropic.JSONOutputFormatParam{
			Schema: req.StructuredOutput.Schema,
		}
	}
	if req.Reasoning != nil {
		params.OutputConfig.Effort = anthropic.OutputConfigEffort(req.Reasoning.Effort)
	}

	return params, nil
}

func assistantMessageToAnthropicBlocks(message morphmsg.Message) ([]anthropic.ContentBlockParamUnion, error) {
	blocks := make([]anthropic.ContentBlockParamUnion, 0, len(message.ToolCalls)+1)
	if message.Content != "" {
		blocks = append(blocks, anthropic.NewTextBlock(message.Content))
	}

	for _, toolCall := range message.ToolCalls {
		input, err := getToolCallInput(toolCall.Input)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, anthropic.NewToolUseBlock(toolCall.ID, input, toolCall.Name))
	}

	return blocks, nil
}

func getToolCallInput(value string) (any, error) {
	valueText := str.String(value).Trim()
	if valueText == "" {
		return map[string]any{}, nil
	}

	var decoded any
	if err := json.Unmarshal([]byte(valueText), &decoded); err != nil {
		return nil, fmt.Errorf("tool call input must be valid JSON: %w", err)
	}

	return decoded, nil
}

func buildAnthropicTools(definitions []ToolDefinition) []anthropic.ToolUnionParam {
	tools := make([]anthropic.ToolUnionParam, 0, len(definitions))
	for _, definition := range definitions {
		inputSchema := anthropic.ToolInputSchemaParam{
			ExtraFields: definition.InputSchema,
		}
		tool := anthropic.ToolUnionParamOfTool(inputSchema, definition.Name)
		tool.OfTool.Description = anthropic.String(definition.Description)
		tool.OfTool.Strict = anthropic.Bool(false)
		tools = append(tools, tool)
	}

	return tools
}

func extractMessageResponse(resp *anthropic.Message) (*Response, error) {
	var (
		outputText strings.Builder
		toolCalls  []ToolCall
	)
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			outputText.WriteString(block.Text)
		case "tool_use":
			nameValue := str.String(block.Name)
			name := nameValue.Trim()
			if name == "" {
				return nil, errors.New("tool call name is required")
			}
			iDValue := str.String(block.ID)
			id := iDValue.Trim()
			if id == "" {
				id = getFallbackToolCallID(name, len(toolCalls))
			}
			inputValue := str.String(string(block.Input))
			toolCalls = append(toolCalls, ToolCall{
				ID:    id,
				Name:  name,
				Input: inputValue.Trim(),
			})
		}
	}
	textValue := str.String(outputText.String())
	text := textValue.Trim()
	if text == "" && len(toolCalls) == 0 {
		return nil, errors.New("model returned empty response")
	}

	promptTokens := int(resp.Usage.InputTokens + resp.Usage.CacheCreationInputTokens + resp.Usage.CacheReadInputTokens)
	completionTokens := int(resp.Usage.OutputTokens)

	return &Response{
		ID:                resp.ID,
		Model:             string(resp.Model),
		OutputText:        text,
		ToolCalls:         toolCalls,
		RequiresToolCalls: len(toolCalls) > 0,
		PromptTokens:      promptTokens,
		CompletionTokens:  completionTokens,
		TotalTokens:       promptTokens + completionTokens,
	}, nil
}

func (c *AnthropicClient) completeMessageStream(
	ctx context.Context,
	params anthropic.MessageNewParams,
	onTextDelta func(StreamDelta),
) (*Response, error) {
	stream := c.createMessageStream(ctx, params)
	if stream == nil {
		return nil, errors.New("model response is required")
	}

	finalMessage := anthropic.Message{}
	for stream.Next() {
		event := stream.Current()
		if err := finalMessage.Accumulate(event); err != nil {
			return nil, err
		}
		if onTextDelta == nil {
			continue
		}
		if delta := getMessageStreamDelta(event); delta.Text != "" {
			onTextDelta(delta)
		}
	}
	if err := stream.Err(); err != nil {
		return nil, err
	}
	if finalMessage.ID == "" && len(finalMessage.Content) == 0 {
		return nil, errors.New("model response is required")
	}

	return extractMessageResponse(&finalMessage)
}

func getMessageStreamDelta(event anthropic.MessageStreamEventUnion) StreamDelta {
	switch event.Type {
	case "content_block_delta":
		delta := event.AsContentBlockDelta().Delta
		switch delta.Type {
		case "text_delta":
			return StreamDelta{Channel: models.StreamChannelAssistant, Text: delta.Text}
		case "thinking_delta":
			return StreamDelta{Channel: models.StreamChannelReasoning, Text: delta.Thinking}
		default:
			return StreamDelta{}
		}
	default:
		return StreamDelta{}
	}
}

func getFallbackToolCallID(name string, index int) string {
	return fmt.Sprintf("call_%s_%d", strings.ReplaceAll(name, " ", "_"), index)
}
