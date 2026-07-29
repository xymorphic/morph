package provider_anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
	"github.com/stretchr/testify/require"

	models "github.com/wandxy/morph/internal/model"
	morphmsg "github.com/wandxy/morph/pkg/agent/message"
	"github.com/wandxy/morph/pkg/logutils"
)

func init() {
	logutils.SetOutput(io.Discard)
}

func TestNewAnthropicClient_IncludesAPIKeyOptionWhenProvided(t *testing.T) {
	originalMessageFactory := newAnthropicMessageCaller
	originalStreamFactory := newAnthropicMessageStreamCaller
	t.Cleanup(func() {
		newAnthropicMessageCaller = originalMessageFactory
		newAnthropicMessageStreamCaller = originalStreamFactory
	})

	messageOptCount := 0
	streamOptCount := 0
	newAnthropicMessageCaller = func(opts ...option.RequestOption) func(
		context.Context,
		anthropic.MessageNewParams,
	) (*anthropic.Message, error) {
		messageOptCount = len(opts)
		return func(context.Context, anthropic.MessageNewParams) (*anthropic.Message, error) {
			return nil, nil
		}
	}
	newAnthropicMessageStreamCaller = func(opts ...option.RequestOption) func(
		context.Context,
		anthropic.MessageNewParams,
	) *ssestream.Stream[anthropic.MessageStreamEventUnion] {
		streamOptCount = len(opts)
		return nil
	}

	client, err := NewAnthropicClient(" test-key ", option.WithBaseURL("https://example.com"))

	require.NoError(t, err)
	require.NotNil(t, client)
	require.Equal(t, 2, messageOptCount)
	require.Equal(t, 2, streamOptCount)
}

func TestNewAnthropicMessageCaller_UsesSDKClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/messages", r.URL.Path)
		require.Equal(t, "test-key", r.Header.Get("x-api-key"))
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{
			"id":"msg_123",
			"type":"message",
			"role":"assistant",
			"model":"claude-sonnet-4-5",
			"content":[{"type":"text","text":"hello back"}],
			"stop_reason":"end_turn",
			"stop_sequence":null,
			"usage":{"input_tokens":3,"output_tokens":4}
		}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	caller := newAnthropicMessageCaller(option.WithBaseURL(server.URL), option.WithAPIKey("test-key"))
	resp, err := caller(context.Background(), anthropic.MessageNewParams{
		MaxTokens: 32,
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("hello"))},
		Model:     anthropic.Model("claude-sonnet-4-5"),
	})

	require.NoError(t, err)
	require.Equal(t, "msg_123", resp.ID)
	require.Equal(t, "hello back", resp.Content[0].Text)
}

func TestNewAnthropicClient_UsesBearerAuthWithoutAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/messages", r.URL.Path)
		require.Empty(t, r.Header.Get("x-api-key"))
		require.Equal(t, "Bearer oauth-token", r.Header.Get("Authorization"))
		require.Contains(t, r.Header.Get("anthropic-beta"), anthropicOAuthBeta)
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{
			"id":"msg_123",
			"type":"message",
			"role":"assistant",
			"model":"claude-sonnet-4-5",
			"content":[{"type":"text","text":"hello back"}],
			"stop_reason":"end_turn",
			"stop_sequence":null,
			"usage":{"input_tokens":3,"output_tokens":4}
		}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	client, err := NewAnthropicClient(
		"",
		option.WithBaseURL(server.URL),
		option.WithHeader("Authorization", "Bearer oauth-token"),
		option.WithHeader("anthropic-beta", strings.Join([]string{anthropicClaudeCodeBeta, anthropicOAuthBeta}, ",")),
	)
	require.NoError(t, err)

	resp, err := client.Complete(context.Background(), Request{
		Model:    "claude-sonnet-4-5",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})

	require.NoError(t, err)
	require.Equal(t, "hello back", resp.OutputText)
}

func TestAnthropicClient_CompleteBuildsMessagesRequest(t *testing.T) {
	client := &AnthropicClient{
		createMessage: func(_ context.Context, params anthropic.MessageNewParams) (*anthropic.Message, error) {
			data, err := json.Marshal(params)
			require.NoError(t, err)

			var body map[string]any
			require.NoError(t, json.Unmarshal(data, &body))
			require.Equal(t, "claude-sonnet-4-5", body["model"])
			require.Equal(t, float64(123), body["max_tokens"])
			require.Equal(t, float64(0.2), body["temperature"])

			system := body["system"].([]any)
			require.Len(t, system, 2)
			require.Equal(t, "base instructions", system[0].(map[string]any)["text"])
			require.Equal(t, "developer guidance", system[1].(map[string]any)["text"])

			messages := body["messages"].([]any)
			require.Len(t, messages, 4)
			require.Equal(t, "user", messages[0].(map[string]any)["role"])
			require.Equal(t, "assistant", messages[1].(map[string]any)["role"])
			assistantBlocks := messages[1].(map[string]any)["content"].([]any)
			require.Equal(t, "text", assistantBlocks[0].(map[string]any)["type"])
			require.Equal(t, "tool_use", assistantBlocks[1].(map[string]any)["type"])
			require.Equal(t, "call_1", assistantBlocks[1].(map[string]any)["id"])
			require.Equal(t, "read_file", assistantBlocks[1].(map[string]any)["name"])
			require.Equal(t, map[string]any{"path": "README.md"}, assistantBlocks[1].(map[string]any)["input"])
			require.Equal(t, "user", messages[2].(map[string]any)["role"])
			toolResult := messages[2].(map[string]any)["content"].([]any)[0].(map[string]any)
			require.Equal(t, "tool_result", toolResult["type"])
			require.Equal(t, "call_1", toolResult["tool_use_id"])

			tools := body["tools"].([]any)
			require.Len(t, tools, 1)
			require.Equal(t, "read_file", tools[0].(map[string]any)["name"])
			require.Equal(t, false, tools[0].(map[string]any)["strict"])

			format := body["output_config"].(map[string]any)["format"].(map[string]any)
			require.Equal(t, "json_schema", format["type"])
			require.Equal(t, map[string]any{"type": "object"}, format["schema"])
			require.Equal(t, "high", body["output_config"].(map[string]any)["effort"])

			return testAnthropicTextMessage("msg_123", "claude-sonnet-4-5", "done"), nil
		},
	}

	resp, err := client.Complete(context.Background(), Request{
		Model:        "claude-sonnet-4-5",
		Instructions: "base instructions",
		Messages: []morphmsg.Message{
			{Role: morphmsg.RoleDeveloper, Content: "developer guidance"},
			{Role: morphmsg.RoleUser, Content: "hello"},
			{
				Role:    morphmsg.RoleAssistant,
				Content: "I'll read it",
				ToolCalls: []morphmsg.ToolCall{{
					ID:    "call_1",
					Name:  "read_file",
					Input: `{"path":"README.md"}`,
				}},
			},
			{Role: morphmsg.RoleTool, ToolCallID: "call_1", Content: "contents"},
			{Role: morphmsg.RoleUser, Content: "summarize"},
		},
		Tools: []ToolDefinition{{
			Name:        "read_file",
			Description: "Read a file",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"path": map[string]any{"type": "string"}},
				"required":   []any{"path"},
			},
		}},
		StructuredOutput: &StructuredOutput{
			Schema: map[string]any{"type": "object"},
		},
		MaxOutputTokens: 123,
		Temperature:     0.2,
		Reasoning:       &models.ReasoningOptions{Effort: "high"},
	})

	require.NoError(t, err)
	require.Equal(t, "done", resp.OutputText)
}

func TestAnthropicClient_CompleteAddsSubscriptionIdentitySystemPrompt(t *testing.T) {
	client := &AnthropicClient{
		subscriptionAuth: true,
		createMessage: func(_ context.Context, params anthropic.MessageNewParams) (*anthropic.Message, error) {
			require.Len(t, params.System, 2)
			require.Equal(t, "You are Claude Code, Anthropic's official CLI for Claude.", params.System[0].Text)
			require.Equal(t, "base instructions", params.System[1].Text)
			return testAnthropicTextMessage("msg_123", "claude-sonnet-4-5", "done"), nil
		},
	}

	resp, err := client.Complete(context.Background(), Request{
		Model:        "claude-sonnet-4-5",
		Instructions: "base instructions",
		Messages:     []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})

	require.NoError(t, err)
	require.Equal(t, "done", resp.OutputText)
}

func TestBuildAnthropicTools_PreservesMapLikeSchemas(t *testing.T) {
	inputSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"env": map[string]any{
				"type":                 "object",
				"additionalProperties": map[string]any{"type": "string"},
			},
			"items": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": true,
				},
			},
		},
		"additionalProperties": map[string]any{"type": "string"},
	}

	tools := buildAnthropicTools([]ToolDefinition{{
		Name:        "custom",
		InputSchema: inputSchema,
	}})

	raw, err := json.Marshal(tools)
	require.NoError(t, err)
	rawText := string(raw)
	require.Contains(t, rawText, `"additionalProperties":{"type":"string"}`)
	require.Contains(t, rawText, `"additionalProperties":true`)
	require.Contains(t, rawText, `"strict":false`)
	require.Equal(t, map[string]any{"type": "string"}, inputSchema["additionalProperties"])
}

func TestAnthropicClient_CompleteUsesDefaultMaxOutputTokens(t *testing.T) {
	client := &AnthropicClient{
		createMessage: func(_ context.Context, params anthropic.MessageNewParams) (*anthropic.Message, error) {
			require.Equal(t, defaultMaxOutputTokens, params.MaxTokens)
			return testAnthropicTextMessage("msg_123", "claude-sonnet-4-5", "done"), nil
		},
	}

	_, err := client.Complete(context.Background(), Request{
		Model:    "claude-sonnet-4-5",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})

	require.NoError(t, err)
}

func TestAnthropicClient_CompleteRejectsNilClient(t *testing.T) {
	var client *AnthropicClient

	_, err := client.Complete(context.Background(), Request{
		Model:    "claude-sonnet-4-5",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})

	require.EqualError(t, err, "model client is required")
}

func TestAnthropicClient_CompleteRejectsMissingCaller(t *testing.T) {
	client := &AnthropicClient{}

	_, err := client.Complete(context.Background(), Request{
		Model:    "claude-sonnet-4-5",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})

	require.EqualError(t, err, "model client is required")
}

func TestAnthropicClient_CompleteReturnsCallerError(t *testing.T) {
	wantErr := errors.New("anthropic unavailable")
	client := &AnthropicClient{
		createMessage: func(context.Context, anthropic.MessageNewParams) (*anthropic.Message, error) {
			return nil, wantErr
		},
	}

	_, err := client.Complete(context.Background(), Request{
		Model:    "claude-sonnet-4-5",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})

	require.ErrorIs(t, err, wantErr)
}

func TestAnthropicClient_CompleteRejectsNilResponse(t *testing.T) {
	client := &AnthropicClient{
		createMessage: func(context.Context, anthropic.MessageNewParams) (*anthropic.Message, error) {
			return nil, nil
		},
	}

	_, err := client.Complete(context.Background(), Request{
		Model:    "claude-sonnet-4-5",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})

	require.EqualError(t, err, "model response is required")
}

func TestAnthropicClient_CompleteLogsDebugMetadata(t *testing.T) {
	client := &AnthropicClient{
		createMessage: func(context.Context, anthropic.MessageNewParams) (*anthropic.Message, error) {
			return testAnthropicTextMessage("msg_123", "claude-sonnet-4-5", "done"), nil
		},
	}

	_, err := client.Complete(context.Background(), Request{
		Model:         "claude-sonnet-4-5",
		Messages:      []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
		DebugRequests: true,
	})

	require.NoError(t, err)
}

func TestAnthropicClient_CompleteExtractsToolCallsAndUsage(t *testing.T) {
	client := &AnthropicClient{
		createMessage: func(context.Context, anthropic.MessageNewParams) (*anthropic.Message, error) {
			return testAnthropicToolMessage(t), nil
		},
	}

	resp, err := client.Complete(context.Background(), Request{
		Model:    "claude-sonnet-4-5",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})

	require.NoError(t, err)
	require.Equal(t, "msg_tools", resp.ID)
	require.Equal(t, "claude-sonnet-4-5", resp.Model)
	require.Equal(t, "I will use a tool.", resp.OutputText)
	require.True(t, resp.RequiresToolCalls)
	require.Equal(t, []ToolCall{{ID: "toolu_123", Name: "read_file", Input: `{"path":"README.md"}`}}, resp.ToolCalls)
	require.Equal(t, 13, resp.PromptTokens)
	require.Equal(t, 7, resp.CompletionTokens)
	require.Equal(t, 20, resp.TotalTokens)
}

func TestAnthropicClient_CompleteUsesFallbackToolCallID(t *testing.T) {
	var message anthropic.Message
	require.NoError(t, json.Unmarshal([]byte(`{
		"id":"msg_tools",
		"type":"message",
		"role":"assistant",
		"model":"claude-sonnet-4-5",
		"content":[{"type":"tool_use","id":"","name":"read file","input":{"path":"README.md"}}],
		"stop_reason":"tool_use",
		"stop_sequence":null,
		"usage":{"input_tokens":10,"output_tokens":7}
	}`), &message))

	resp, err := extractMessageResponse(&message)

	require.NoError(t, err)
	require.Equal(t, []ToolCall{{ID: "call_read_file_0", Name: "read file", Input: `{"path":"README.md"}`}}, resp.ToolCalls)
}

func TestAnthropicClient_CompleteRejectsEmptyResponse(t *testing.T) {
	var message anthropic.Message
	require.NoError(t, json.Unmarshal([]byte(`{
		"id":"msg_empty",
		"type":"message",
		"role":"assistant",
		"model":"claude-sonnet-4-5",
		"content":[],
		"stop_reason":"end_turn",
		"stop_sequence":null,
		"usage":{"input_tokens":1,"output_tokens":2}
	}`), &message))

	_, err := extractMessageResponse(&message)

	require.EqualError(t, err, "model returned empty response")
}

func TestAnthropicClient_CompleteRejectsUnnamedToolCall(t *testing.T) {
	var message anthropic.Message
	require.NoError(t, json.Unmarshal([]byte(`{
		"id":"msg_tool",
		"type":"message",
		"role":"assistant",
		"model":"claude-sonnet-4-5",
		"content":[{"type":"tool_use","id":"toolu_123","name":"","input":{}}],
		"stop_reason":"tool_use",
		"stop_sequence":null,
		"usage":{"input_tokens":1,"output_tokens":2}
	}`), &message))

	_, err := extractMessageResponse(&message)

	require.EqualError(t, err, "tool call name is required")
}

func TestAnthropicClient_CompleteRejectsInvalidAssistantToolInput(t *testing.T) {
	client := &AnthropicClient{
		createMessage: func(context.Context, anthropic.MessageNewParams) (*anthropic.Message, error) {
			t.Fatal("createMessage should not be called")
			return nil, nil
		},
	}

	_, err := client.Complete(context.Background(), Request{
		Model: "claude-sonnet-4-5",
		Messages: []morphmsg.Message{{
			Role:      morphmsg.RoleAssistant,
			ToolCalls: []morphmsg.ToolCall{{ID: "call_1", Name: "read_file", Input: "{bad json"}},
		}},
	})

	require.ErrorContains(t, err, "tool call input must be valid JSON")
}

func TestAnthropicClient_CompleteRejectsDeveloperOnlyMessages(t *testing.T) {
	client := &AnthropicClient{
		createMessage: func(context.Context, anthropic.MessageNewParams) (*anthropic.Message, error) {
			t.Fatal("createMessage should not be called")
			return nil, nil
		},
	}

	_, err := client.Complete(context.Background(), Request{
		Model:    "claude-sonnet-4-5",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleDeveloper, Content: "developer guidance"}},
	})

	require.EqualError(t, err, "messages must include at least one user, assistant, or tool message")
}

func TestAnthropicClient_CompleteRejectsInvalidRequests(t *testing.T) {
	client := &AnthropicClient{
		createMessage: func(context.Context, anthropic.MessageNewParams) (*anthropic.Message, error) {
			t.Fatal("createMessage should not be called")
			return nil, nil
		},
	}

	tests := []struct {
		name    string
		req     Request
		wantErr string
	}{
		{
			name:    "missing model",
			req:     Request{Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}}},
			wantErr: "model is required",
		},
		{
			name:    "missing messages",
			req:     Request{Model: "claude-sonnet-4-5"},
			wantErr: "messages are required",
		},
		{
			name: "invalid role",
			req: Request{
				Model:    "claude-sonnet-4-5",
				Messages: []morphmsg.Message{{Role: morphmsg.Role("system"), Content: "hello"}},
			},
			wantErr: "message role must be one of developer, user, assistant, or tool",
		},
		{
			name: "empty content",
			req: Request{
				Model:    "claude-sonnet-4-5",
				Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: " "}},
			},
			wantErr: "message content is required",
		},
		{
			name: "tool missing call id",
			req: Request{
				Model:    "claude-sonnet-4-5",
				Messages: []morphmsg.Message{{Role: morphmsg.RoleTool, Content: "result"}},
			},
			wantErr: "tool call id is required",
		},
		{
			name: "tool definition missing name",
			req: Request{
				Model:    "claude-sonnet-4-5",
				Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
				Tools:    []ToolDefinition{{Name: " "}},
			},
			wantErr: "tool name is required",
		},
		{
			name: "tool call missing id",
			req: Request{
				Model: "claude-sonnet-4-5",
				Messages: []morphmsg.Message{{
					Role:      morphmsg.RoleAssistant,
					ToolCalls: []morphmsg.ToolCall{{Name: "read_file", Input: `{}`}},
				}},
			},
			wantErr: "tool call id is required",
		},
		{
			name: "tool call missing name",
			req: Request{
				Model: "claude-sonnet-4-5",
				Messages: []morphmsg.Message{{
					Role:      morphmsg.RoleAssistant,
					ToolCalls: []morphmsg.ToolCall{{ID: "call_1", Input: `{}`}},
				}},
			},
			wantErr: "tool call name is required",
		},
		{
			name: "unknown reasoning effort",
			req: Request{
				Model:    "claude-sonnet-4-6",
				Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
				Reasoning: &models.ReasoningOptions{
					Effort: "approximate",
				},
			},
			wantErr: `anthropic reasoning effort "approximate" is not supported by the installed SDK`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.Complete(context.Background(), tt.req)
			require.EqualError(t, err, tt.wantErr)
		})
	}
}

func TestAnthropicClient_CompleteAllowsAssistantToolCallWithoutContent(t *testing.T) {
	client := &AnthropicClient{
		createMessage: func(context.Context, anthropic.MessageNewParams) (*anthropic.Message, error) {
			return testAnthropicTextMessage("msg_123", "claude-sonnet-4-5", "done"), nil
		},
	}

	_, err := client.Complete(context.Background(), Request{
		Model: "claude-sonnet-4-5",
		Messages: []morphmsg.Message{{
			Role:      morphmsg.RoleAssistant,
			ToolCalls: []morphmsg.ToolCall{{ID: "call_1", Name: "read_file", Input: ""}},
		}},
	})

	require.NoError(t, err)
}

func TestAnthropicClient_NormalizeStructuredOutputRejectsEmptySchema(t *testing.T) {
	require.Nil(t, normalizeStructuredOutput(nil))
	require.Nil(t, normalizeStructuredOutput(&StructuredOutput{Name: "response"}))
}

func TestAnthropicClient_GetMessageStreamDeltaIgnoresOtherEvents(t *testing.T) {
	require.Empty(t, getMessageStreamDelta(anthropic.MessageStreamEventUnion{}))

	var event anthropic.MessageStreamEventUnion
	require.NoError(t, json.Unmarshal([]byte(`{
		"type":"content_block_delta",
		"index":0,
		"delta":{"type":"signature_delta","signature":"abc"}
	}`), &event))

	require.Empty(t, getMessageStreamDelta(event))
}

func TestAnthropicClient_CompleteStreamSendsTextAndThinkingDeltas(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/messages", r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		_, err := w.Write([]byte(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_stream","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":5,"output_tokens":1}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"thinking"}}`,
			``,
			`event: content_block_stop`,
			`data: {"type":"content_block_stop","index":0}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"hello"}}`,
			``,
			`event: content_block_stop`,
			`data: {"type":"content_block_stop","index":1}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":4}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
			``,
		}, "\n")))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	client, err := NewAnthropicClient("test-key", option.WithBaseURL(server.URL))
	require.NoError(t, err)

	var deltas []StreamDelta
	resp, err := client.CompleteStream(context.Background(), Request{
		Model:    "claude-sonnet-4-5",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	}, func(delta StreamDelta) {
		deltas = append(deltas, delta)
	})

	require.NoError(t, err)
	require.Equal(t, "hello", resp.OutputText)
	require.Equal(t, []StreamDelta{
		{Channel: models.StreamChannelReasoning, Text: "thinking"},
		{Channel: models.StreamChannelAssistant, Text: "hello"},
	}, deltas)
	require.Equal(t, 5, resp.PromptTokens)
	require.Equal(t, 4, resp.CompletionTokens)
	require.Equal(t, 9, resp.TotalTokens)
}

func TestAnthropicClient_CompleteStreamAccumulatesToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/messages", r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		_, err := w.Write([]byte(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_stream_tool","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":5,"output_tokens":1}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_123","name":"read_file","input":{}}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\""}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":":\"README.md\"}"}}`,
			``,
			`event: content_block_stop`,
			`data: {"type":"content_block_stop","index":0}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":8}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
			``,
		}, "\n")))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	client, err := NewAnthropicClient("test-key", option.WithBaseURL(server.URL))
	require.NoError(t, err)

	resp, err := client.CompleteStream(context.Background(), Request{
		Model:    "claude-sonnet-4-5",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	}, nil)

	require.NoError(t, err)
	require.Empty(t, resp.OutputText)
	require.True(t, resp.RequiresToolCalls)
	require.Equal(t, []ToolCall{{ID: "toolu_123", Name: "read_file", Input: `{"path":"README.md"}`}}, resp.ToolCalls)
	require.Equal(t, 5, resp.PromptTokens)
	require.Equal(t, 8, resp.CompletionTokens)
	require.Equal(t, 13, resp.TotalTokens)
}

func TestAnthropicClient_CompleteStreamRejectsMissingCaller(t *testing.T) {
	client := &AnthropicClient{}

	_, err := client.CompleteStream(context.Background(), Request{
		Model:    "claude-sonnet-4-5",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	}, nil)

	require.EqualError(t, err, "model client is required")
}

func TestAnthropicClient_CompleteStreamRejectsNilStream(t *testing.T) {
	client := &AnthropicClient{
		createMessageStream: func(context.Context, anthropic.MessageNewParams) *ssestream.Stream[anthropic.MessageStreamEventUnion] {
			return nil
		},
	}

	_, err := client.CompleteStream(context.Background(), Request{
		Model:    "claude-sonnet-4-5",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	}, nil)

	require.EqualError(t, err, "model response is required")
}

func TestAnthropicClient_CompleteStreamReturnsStreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/messages", r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		_, err := w.Write([]byte(strings.Join([]string{
			`event: error`,
			`data: {"type":"error","error":{"type":"api_error","message":"stream failed"}}`,
			``,
			``,
		}, "\n")))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	client, err := NewAnthropicClient("test-key", option.WithBaseURL(server.URL))
	require.NoError(t, err)

	_, err = client.CompleteStream(context.Background(), Request{
		Model:    "claude-sonnet-4-5",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	}, nil)

	require.ErrorContains(t, err, "stream failed")
}

func TestAnthropicClient_CompleteStreamReturnsAccumulatorError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/messages", r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		_, err := w.Write([]byte(strings.Join([]string{
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"orphan"}}`,
			``,
			``,
		}, "\n")))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	client, err := NewAnthropicClient("test-key", option.WithBaseURL(server.URL))
	require.NoError(t, err)

	_, err = client.CompleteStream(context.Background(), Request{
		Model:    "claude-sonnet-4-5",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	}, nil)

	require.ErrorContains(t, err, "there was no content block")
}

func TestAnthropicClient_CompleteStreamRejectsEmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/messages", r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		_, err := w.Write([]byte(strings.Join([]string{
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
			``,
		}, "\n")))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	client, err := NewAnthropicClient("test-key", option.WithBaseURL(server.URL))
	require.NoError(t, err)

	_, err = client.CompleteStream(context.Background(), Request{
		Model:    "claude-sonnet-4-5",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	}, nil)

	require.EqualError(t, err, "model response is required")
}

func testAnthropicTextMessage(id, model, text string) *anthropic.Message {
	var message anthropic.Message
	err := json.Unmarshal([]byte(`{
		"id":"`+id+`",
		"type":"message",
		"role":"assistant",
		"model":"`+model+`",
		"content":[{"type":"text","text":"`+text+`"}],
		"stop_reason":"end_turn",
		"stop_sequence":null,
		"usage":{"input_tokens":1,"output_tokens":2}
	}`), &message)
	if err != nil {
		panic(err)
	}

	return &message
}

func testAnthropicToolMessage(t *testing.T) *anthropic.Message {
	t.Helper()

	var message anthropic.Message
	require.NoError(t, json.Unmarshal([]byte(`{
		"id":"msg_tools",
		"type":"message",
		"role":"assistant",
		"model":"claude-sonnet-4-5",
		"content":[
			{"type":"text","text":"I will use a tool."},
			{"type":"tool_use","id":"toolu_123","name":"read_file","input":{"path":"README.md"}}
		],
		"stop_reason":"tool_use",
		"stop_sequence":null,
		"usage":{"input_tokens":10,"cache_creation_input_tokens":2,"cache_read_input_tokens":1,"output_tokens":7}
	}`), &message))

	return &message
}
