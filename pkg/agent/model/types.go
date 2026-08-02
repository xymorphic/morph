package model

import (
	"context"

	"github.com/xymorphic/morph/pkg/agent/message"
)

const (
	APIOpenAICompletions = "openai-completions"
	APIOpenAIResponses   = "openai-responses"
	APIOllamaNative      = "ollama-native"
	APIAnthropicMessages = "anthropic-messages"
)

type Client interface {
	Complete(context.Context, Request) (*Response, error)
	CompleteStream(context.Context, Request, func(StreamDelta)) (*Response, error)
}

type StreamChannel string

const (
	StreamChannelAssistant        StreamChannel = "assistant"
	StreamChannelReasoning        StreamChannel = "reasoning"
	StreamChannelReasoningSummary StreamChannel = "reasoning_summary"
)

type StreamDelta struct {
	Channel StreamChannel
	Text    string
}

type Request struct {
	Model            string
	Provider         string
	API              string
	Instructions     string
	Messages         []message.Message
	Tools            []ToolDefinition
	StructuredOutput *StructuredOutput
	ContextLength    int
	MaxOutputTokens  int64
	Temperature      float64
	DebugRequests    bool
	Reasoning        *ReasoningOptions
}

type ReasoningOptions struct {
	Effort  string
	Summary bool
}

type StructuredOutput struct {
	Name        string
	Description string
	Schema      map[string]any
	Strict      bool
}

type Response struct {
	ID                string
	Model             string
	OutputText        string
	ToolCalls         []ToolCall
	RequiresToolCalls bool
	PromptTokens      int
	CompletionTokens  int
	TotalTokens       int
}

type ToolDefinition struct {
	Name         string
	Description  string
	InputSchema  map[string]any
	ParallelSafe bool
}

type ToolCall struct {
	ID    string
	Name  string
	Input string
}

func ToolCallsToMessageToolCalls(toolCalls []ToolCall) []message.ToolCall {
	if len(toolCalls) == 0 {
		return nil
	}

	converted := make([]message.ToolCall, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		converted = append(converted, message.ToolCall{
			ID:    toolCall.ID,
			Name:  toolCall.Name,
			Input: toolCall.Input,
		})
	}

	return converted
}
