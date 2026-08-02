package provider_anthropic

import models "github.com/xymorphic/morph/internal/model"

// Client is the provider-neutral model client contract implemented by AnthropicClient.
type Client = models.Client

// Request is the provider-neutral model request accepted by AnthropicClient.
type Request = models.Request

// Response is the provider-neutral model response returned by AnthropicClient.
type Response = models.Response

// StreamDelta is a single streaming text or reasoning update.
type StreamDelta = models.StreamDelta

// StructuredOutput describes a JSON schema response format request.
type StructuredOutput = models.StructuredOutput

// ToolCall is a normalized model tool call.
type ToolCall = models.ToolCall

// ToolDefinition is a normalized model-visible tool schema.
type ToolDefinition = models.ToolDefinition
