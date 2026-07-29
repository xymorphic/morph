package provider_openai

import (
	"context"
	"errors"
	"fmt"
	"strings"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	models "github.com/wandxy/morph/internal/model"
	morphmsg "github.com/wandxy/morph/pkg/agent/message"
	"github.com/wandxy/morph/pkg/str"
)

// responsesHandler handles responses requests.
type responsesHandler struct{}

func (responsesHandler) Complete(
	ctx context.Context,
	client *OpenAIClient,
	req normalizedGenerateRequest,
	stream bool,
	onTextDelta func(StreamDelta),
) (*Response, error) {
	params := buildResponsesRequest(req)
	if client.isReasoningModel(req.Model) {
		params.Reasoning = shared.ReasoningParam{
			Summary: shared.ReasoningSummaryAuto,
		}
	}
	if req.DebugRequests {
		logRequestDebugMetadata(req)
	}

	if stream {
		if client.createResponseStream == nil {
			return nil, errors.New("model client is required")
		}

		return client.completeResponsesStream(ctx, params, onTextDelta)
	}

	if client.createResponse == nil {
		return nil, errors.New("model client is required")
	}
	providerResp, callErr := client.createResponse(ctx, params)
	if callErr != nil {
		return nil, callErr
	}
	if providerResp == nil {
		return nil, errors.New("model response is required")
	}
	return extractResponsesResponse(providerResp)
}

func (c *OpenAIClient) completeResponsesStream(
	ctx context.Context,
	params responses.ResponseNewParams,
	onTextDelta func(StreamDelta),
) (*Response, error) {
	stream := c.createResponseStream(ctx, params)
	if stream == nil {
		return nil, errors.New("model response is required")
	}

	var finalResponse *responses.Response
	var streamToolCalls []ToolCall
	var outputText strings.Builder
	var finalizedOutputText string
	for stream.Next() {
		event := stream.Current()
		if text := getResponsesStreamFinalText(event); text != "" {
			finalizedOutputText = text
		}
		if toolCall, ok, err := getResponsesStreamToolCall(event, len(streamToolCalls)); err != nil {
			return nil, err
		} else if ok {
			streamToolCalls = append(streamToolCalls, toolCall)
		}
		if textDelta, terminalResponse, err := HandesponsesStreamEvent(event); err != nil {
			return nil, err
		} else {
			if textDelta.Text != "" {
				if textDelta.Channel == models.StreamChannelAssistant {
					outputText.WriteString(textDelta.Text)
				}
				if onTextDelta != nil {
					onTextDelta(textDelta)
				}
			}
			if terminalResponse != nil {
				finalResponse = terminalResponse
			}
		}
	}
	if err := stream.Err(); err != nil {
		return nil, err
	}
	if finalResponse == nil {
		return nil, errors.New("model response is required")
	}
	finalizedOutputTextValue := str.String(finalizedOutputText)
	streamOutputText := finalizedOutputTextValue.Trim()
	if streamOutputText == "" {
		textValue4 := str.String(outputText.String())
		streamOutputText = textValue4.Trim()
	}

	return extractResponsesResponseWithFallback(finalResponse, streamOutputText, streamToolCalls)
}

func getResponsesStreamFinalText(event responses.ResponseStreamEventUnion) string {
	switch event.Type {
	case "response.output_text.done":
		textValue := str.String(event.AsResponseOutputTextDone().Text)
		return textValue.Trim()
	case "response.content_part.done":
		part := event.AsResponseContentPartDone().Part
		switch part.Type {
		case "output_text":
			textValue2 := str.String(part.Text)
			return textValue2.Trim()
		case "refusal":
			refusalValue := str.String(part.Refusal)
			return refusalValue.Trim()
		default:
			return ""
		}
	case "response.output_item.done":
		return getResponseOutputItemText(event.AsResponseOutputItemDone().Item)
	default:
		return ""
	}
}

func getResponseOutputItemText(item responses.ResponseOutputItemUnion) string {
	if item.Type != "message" {
		return ""
	}

	message := item.AsMessage()
	parts := make([]string, 0, len(message.Content))
	for _, content := range message.Content {
		switch content.Type {
		case "output_text":
			textValue3 := str.String(content.Text)
			parts = append(parts, textValue3.Trim())
		case "refusal":
			refusalValue2 := str.String(content.Refusal)
			parts = append(parts, refusalValue2.Trim())
		}
	}
	joinValue := str.String(strings.Join(parts, ""))
	return joinValue.Trim()
}

func getResponsesStreamToolCall(event responses.ResponseStreamEventUnion, idx int) (ToolCall, bool, error) {
	if event.Type != "response.output_item.done" {
		return ToolCall{}, false, nil
	}

	return getResponseOutputItemToolCall(event.AsResponseOutputItemDone().Item, idx)
}

func getResponseOutputItemToolCall(item responses.ResponseOutputItemUnion, idx int) (ToolCall, bool, error) {
	if item.Type != "function_call" {
		return ToolCall{}, false, nil
	}

	functionCall := item.AsFunctionCall()
	callIDValue := str.String(functionCall.CallID)
	callID := callIDValue.Trim()
	nameValue := str.String(functionCall.Name)
	name := nameValue.Trim()
	if name == "" {
		return ToolCall{}, false, errors.New("tool call name is required")
	}
	if callID == "" {
		callID = getFallbackToolCallID(name, idx)
	}
	argumentsValue := str.String(functionCall.Arguments)
	return ToolCall{
		ID:    callID,
		Name:  name,
		Input: argumentsValue.Trim(),
	}, true, nil
}

func HandesponsesStreamEvent(event responses.ResponseStreamEventUnion) (StreamDelta, *responses.Response, error) {
	switch event.Type {
	case "response.output_text.delta":
		return StreamDelta{
			Channel: models.StreamChannelAssistant,
			Text:    event.AsResponseOutputTextDelta().Delta,
		}, nil, nil
	case "response.reasoning_text.delta":
		return StreamDelta{
			Channel: models.StreamChannelReasoning,
			Text:    event.AsResponseReasoningTextDelta().Delta,
		}, nil, nil
	case "response.reasoning_summary_text.delta":
		return StreamDelta{
			Channel: models.StreamChannelReasoningSummary,
			Text:    event.AsResponseReasoningSummaryTextDelta().Delta,
		}, nil, nil
	case "response.completed":
		completed := event.AsResponseCompleted()
		return StreamDelta{}, &completed.Response, nil
	case "response.failed":
		failed := event.AsResponseFailed()
		return StreamDelta{}, &failed.Response, nil
	case "response.incomplete":
		incomplete := event.AsResponseIncomplete()
		return StreamDelta{}, &incomplete.Response, nil
	case "error":
		apierr := event.AsError()
		messageValue := str.String(apierr.Message)
		message := messageValue.Trim()
		if message == "" {
			message = "response failed"
		}
		return StreamDelta{}, nil, errors.New(message)
	default:
		return StreamDelta{}, nil, nil
	}
}

func buildResponsesRequest(req normalizedGenerateRequest) responses.ResponseNewParams {
	items := make(responses.ResponseInputParam, 0, len(req.Messages)*2)
	assistantIndex := 0

	for _, message := range req.Messages {
		switch message.Role {
		case morphmsg.RoleDeveloper, morphmsg.RoleUser:
			items = append(items, responses.ResponseInputItemParamOfInputMessage(
				responses.ResponseInputMessageContentListParam{
					{
						OfInputText: &responses.ResponseInputTextParam{Text: message.Content},
					},
				},
				string(message.Role),
			))
		case morphmsg.RoleAssistant:
			if message.Content != "" {
				assistantIndex++
				items = append(items, responses.ResponseInputItemParamOfOutputMessage(
					[]responses.ResponseOutputMessageContentUnionParam{{
						OfOutputText: &responses.ResponseOutputTextParam{
							Annotations: []responses.ResponseOutputTextAnnotationUnionParam{},
							Text:        message.Content,
						},
					}},
					fmt.Sprintf("msg_assistant_%d", assistantIndex),
					responses.ResponseOutputMessageStatusCompleted,
				))
			}
			for _, toolCall := range message.ToolCalls {
				items = append(items, responses.ResponseInputItemParamOfFunctionCall(toolCall.Input, toolCall.ID, toolCall.Name))
			}
		case morphmsg.RoleTool:
			items = append(items, responses.ResponseInputItemParamOfFunctionCallOutput(message.ToolCallID, message.Content))
		}
	}

	params := responses.ResponseNewParams{
		Model:   shared.ResponsesModel(req.Model),
		Input:   responses.ResponseNewParamsInputUnion{OfInputItemList: items},
		Include: []responses.ResponseIncludable{responses.ResponseIncludableReasoningEncryptedContent},
		Store:   openai.Bool(false),
	}
	if req.Instructions != "" {
		params.Instructions = openai.String(req.Instructions)
	}
	if len(req.Tools) > 0 {
		params.Tools = buildResponsesTools(req.Tools)
		params.ParallelToolCalls = openai.Bool(true)
		params.ToolChoice = responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: openai.Opt(responses.ToolChoiceOptionsAuto),
		}
	}
	if req.MaxOutputTokens > 0 {
		params.MaxOutputTokens = openai.Int(req.MaxOutputTokens)
	}
	if req.Temperature > 0 {
		params.Temperature = openai.Float(req.Temperature)
	}
	if req.StructuredOutput != nil {
		params.Text = responses.ResponseTextConfigParam{
			Format: responses.ResponseFormatTextConfigUnionParam{
				OfJSONSchema: &responses.ResponseFormatTextJSONSchemaConfigParam{
					Name:        req.StructuredOutput.Name,
					Schema:      req.StructuredOutput.Schema,
					Description: openai.String(req.StructuredOutput.Description),
					Strict:      openai.Bool(req.StructuredOutput.Strict),
				},
			},
		}
	}

	return params
}

func extractResponsesResponse(resp *responses.Response) (*Response, error) {
	return extractResponsesResponseWithFallback(resp, "", nil)
}

func extractResponsesResponseWithFallback(
	resp *responses.Response,
	fallbackOutputText string,
	fallbackToolCalls []ToolCall,
) (*Response, error) {
	var toolCalls []ToolCall
	for idx, item := range resp.Output {
		if item.Type != "function_call" {
			continue
		}

		functionCall := item.AsFunctionCall()
		callIDValue2 := str.String(functionCall.CallID)
		callID := callIDValue2.Trim()
		nameValue2 := str.String(functionCall.Name)
		name := nameValue2.Trim()
		if name == "" {
			return nil, errors.New("tool call name is required")
		}
		if callID == "" {
			callID = getFallbackToolCallID(name, idx)
		}
		argumentsValue2 := str.String(functionCall.Arguments)
		toolCalls = append(toolCalls, ToolCall{
			ID:    callID,
			Name:  name,
			Input: argumentsValue2.Trim(),
		})
	}
	if len(toolCalls) == 0 {
		toolCalls = append(toolCalls, fallbackToolCalls...)
	}
	outputTextValue := str.String(resp.OutputText())
	outputText := outputTextValue.Trim()
	if outputText == "" {
		fallbackOutputTextValue := str.String(fallbackOutputText)
		outputText = fallbackOutputTextValue.Trim()
	}
	switch resp.Status {
	case "", responses.ResponseStatusCompleted:
	case responses.ResponseStatusFailed:
		messageValue2 := str.String(resp.Error.Message)
		message := messageValue2.Trim()
		if message == "" {
			message = "response failed"
		}

		return nil, errors.New(message)
	case responses.ResponseStatusIncomplete:
		if outputText == "" && len(toolCalls) == 0 {
			reasonValue := str.String(resp.IncompleteDetails.Reason)
			reason := reasonValue.Trim()
			if reason == "" {
				reason = "unknown"
			}

			return nil, fmt.Errorf("response incomplete: %s", reason)
		}
	default:
		return nil, fmt.Errorf("response status is %s", resp.Status)
	}
	if outputText == "" && len(toolCalls) == 0 {
		return nil, errors.New("model response contained no text or tool calls")
	}

	return &Response{
		ID:                resp.ID,
		Model:             string(resp.Model),
		OutputText:        outputText,
		ToolCalls:         toolCalls,
		RequiresToolCalls: len(toolCalls) > 0,
		PromptTokens:      int(resp.Usage.InputTokens),
		CompletionTokens:  int(resp.Usage.OutputTokens),
		TotalTokens:       int(resp.Usage.TotalTokens),
	}, nil
}

func buildResponsesTools(definitions []ToolDefinition) []responses.ToolUnionParam {
	tools := make([]responses.ToolUnionParam, 0, len(definitions))
	for _, definition := range definitions {
		tools = append(tools, responses.ToolUnionParam{
			OfFunction: &responses.FunctionToolParam{
				Name:        definition.Name,
				Description: openai.String(definition.Description),
				Parameters:  normalizeStrictJSONSchema(definition.InputSchema),
				Strict:      openai.Bool(true),
			},
		})
	}

	return tools
}
