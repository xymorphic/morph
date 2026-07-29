package model

import agentmodel "github.com/wandxy/morph/pkg/agent/model"

const (
	APIOpenAICompletions = agentmodel.APIOpenAICompletions
	APIOpenAIResponses   = agentmodel.APIOpenAIResponses
	APIOllamaNative      = agentmodel.APIOllamaNative
	APIAnthropicMessages = agentmodel.APIAnthropicMessages
)

type Client = agentmodel.Client
type StreamChannel = agentmodel.StreamChannel

const (
	StreamChannelAssistant        = agentmodel.StreamChannelAssistant
	StreamChannelReasoning        = agentmodel.StreamChannelReasoning
	StreamChannelReasoningSummary = agentmodel.StreamChannelReasoningSummary
)

type StreamDelta = agentmodel.StreamDelta
type Request = agentmodel.Request
type ReasoningOptions = agentmodel.ReasoningOptions
type StructuredOutput = agentmodel.StructuredOutput
type Response = agentmodel.Response
type ToolDefinition = agentmodel.ToolDefinition
type ToolCall = agentmodel.ToolCall

var ToolCallsToMessageToolCalls = agentmodel.ToolCallsToMessageToolCalls
