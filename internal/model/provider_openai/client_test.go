package provider_openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/respjson"
	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/openai/openai-go/v3/responses"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/require"

	models "github.com/xymorphic/morph/internal/model"
	modelprovider "github.com/xymorphic/morph/internal/model/provider"
	browsertool "github.com/xymorphic/morph/internal/tools/browser"
	runcommandtool "github.com/xymorphic/morph/internal/tools/runcommand"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/logutils"
)

func TestNewOpenAIClient_IncludesAPIKeyOptionWhenProvided(t *testing.T) {
	originalCompletionFactory := newOpenAICompletionCaller
	originalResponseFactory := newOpenAIResponseCaller
	t.Cleanup(func() {
		newOpenAICompletionCaller = originalCompletionFactory
		newOpenAIResponseCaller = originalResponseFactory
	})

	completionCalls := 0
	responseCalls := 0
	completionOptCount := 0
	responseOptCount := 0
	newOpenAICompletionCaller = func(opts ...option.RequestOption) func(context.Context, openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		completionCalls++
		completionOptCount = len(opts)
		return func(context.Context, openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
			return nil, nil
		}
	}
	newOpenAIResponseCaller = func(opts ...option.RequestOption) func(context.Context, responses.ResponseNewParams) (*responses.Response, error) {
		responseCalls++
		responseOptCount = len(opts)
		return func(context.Context, responses.ResponseNewParams) (*responses.Response, error) {
			return nil, nil
		}
	}

	client, err := NewOpenAIClient(" test-key ", models.APIOpenAIResponses, option.WithBaseURL("https://example.com/v1"))
	require.NoError(t, err)
	require.NotNil(t, client)
	require.Equal(t, 1, completionCalls)
	require.Equal(t, 1, responseCalls)
	require.Equal(t, 2, completionOptCount)
	require.Equal(t, 2, responseOptCount)
}

func TestNewOpenAIClient_OmitsAPIKeyOptionWhenEmpty(t *testing.T) {
	originalCompletionFactory := newOpenAICompletionCaller
	originalResponseFactory := newOpenAIResponseCaller
	t.Cleanup(func() {
		newOpenAICompletionCaller = originalCompletionFactory
		newOpenAIResponseCaller = originalResponseFactory
	})

	completionOptCount := 0
	responseOptCount := 0
	newOpenAICompletionCaller = func(opts ...option.RequestOption) func(context.Context, openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		completionOptCount = len(opts)
		return func(context.Context, openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
			return nil, nil
		}
	}
	newOpenAIResponseCaller = func(opts ...option.RequestOption) func(context.Context, responses.ResponseNewParams) (*responses.Response, error) {
		responseOptCount = len(opts)
		return func(context.Context, responses.ResponseNewParams) (*responses.Response, error) {
			return nil, nil
		}
	}

	client, err := NewOpenAIClient("   ", models.APIOpenAIResponses, option.WithBaseURL("https://example.com/v1"))
	require.NoError(t, err)
	require.NotNil(t, client)
	require.Equal(t, 1, completionOptCount)
	require.Equal(t, 1, responseOptCount)
}

func TestNewOpenAICompletionCaller_UsesSDKClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/chat/completions", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"id":"resp_123","object":"chat.completion","created":0,"model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"hello back"},"finish_reason":"stop"}]}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	caller := newOpenAICompletionCaller(option.WithBaseURL(server.URL), option.WithAPIKey("test-key"))
	resp, err := caller(context.Background(), openai.ChatCompletionNewParams{
		Model:    openai.ChatModel("test-model"),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hello")},
	})

	require.NoError(t, err)
	require.Equal(t, "resp_123", resp.ID)
}

func TestNewOpenAIResponseCaller_UsesSDKClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/responses", r.URL.Path)
		require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"id":"resp_123","object":"response","created_at":0,"model":"gpt-5.1","output":[{"id":"msg_123","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello back","annotations":[]}]}],"parallel_tool_calls":false,"temperature":1,"tool_choice":"auto","tools":[],"top_p":1,"status":"completed","text":{"format":{"type":"text"}}}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	caller := newOpenAIResponseCaller(option.WithBaseURL(server.URL), option.WithAPIKey("test-key"))
	resp, err := caller(context.Background(), responses.ResponseNewParams{
		Model: "gpt-5.1",
		Input: responses.ResponseNewParamsInputUnion{OfString: openai.String("hello")},
	})

	require.NoError(t, err)
	require.Equal(t, "resp_123", resp.ID)
	require.Equal(t, "hello back", resp.OutputText())
}

func TestNewOpenAIResponseCaller_UsesSubscriptionAuthorizationHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/responses", r.URL.Path)
		require.Equal(t, "Bearer subscription-token", r.Header.Get("Authorization"))
		require.Equal(t, "acct-test", r.Header.Get("ChatGPT-Account-ID"))
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"id":"resp_123","object":"response","created_at":0,"model":"gpt-5.1","output":[{"id":"msg_123","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello back","annotations":[]}]}],"parallel_tool_calls":false,"temperature":1,"tool_choice":"auto","tools":[],"top_p":1,"status":"completed","text":{"format":{"type":"text"}}}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	caller := newOpenAIResponseCaller(
		option.WithBaseURL(server.URL),
		option.WithAPIKey("api-key-that-should-be-overridden"),
		option.WithHeader("Authorization", "Bearer subscription-token"),
		option.WithHeader("ChatGPT-Account-ID", "acct-test"),
	)
	resp, err := caller(context.Background(), responses.ResponseNewParams{
		Model: "gpt-5.1",
		Input: responses.ResponseNewParamsInputUnion{OfString: openai.String("hello")},
	})

	require.NoError(t, err)
	require.Equal(t, "resp_123", resp.ID)
}

func TestOpenAIClient_ChatRequiresClient(t *testing.T) {
	var nilClient *OpenAIClient
	_, err := nilClient.Complete(context.Background(), Request{})
	require.EqualError(t, err, "model client is required")
}

func TestNewOpenAIClient_RejectsInvalidAPI(t *testing.T) {
	_, err := NewOpenAIClient("test-key", "invalid")

	require.EqualError(t, err, "model API must be one of: openai-completions, openai-responses")
}

func TestOpenAIClient_GetProviderModelIDRoutesOpenRouterOpenAIModels(t *testing.T) {
	openRouterClient := &OpenAIClient{provider: "openrouter"}
	require.Equal(t, "gpt-4o-mini", openRouterClient.getProviderModelID("gpt-4o-mini"))
	require.Equal(t, "minimax/minimax-m2.7", openRouterClient.getProviderModelID("minimax/minimax-m2.7"))
	require.Equal(t, "openai/gpt-4o-mini", openRouterClient.getProviderModelID("openai/gpt-4o-mini"))
	require.Equal(t, "gpt-unknown", openRouterClient.getProviderModelID("gpt-unknown"))

	openAIClient := &OpenAIClient{provider: "openai"}
	require.Equal(t, "gpt-4o-mini", openAIClient.getProviderModelID("openai/gpt-4o-mini"))
}

func TestOpenAIClient_GetProviderModelIDUsesConfiguredRegistry(t *testing.T) {
	registry := modelprovider.NewRegistry(
		[]modelprovider.APIDefinition{{ID: models.APIOpenAIResponses}},
		[]modelprovider.ProviderDefinition{{ID: "openrouter", DefaultAPI: models.APIOpenAIResponses}},
		[]modelprovider.ModelDefinition{{
			ID:       "custom-model",
			Owner:    "custom-owner",
			Provider: "openrouter",
			API:      models.APIOpenAIResponses,
		}},
	)
	client := &OpenAIClient{
		provider: "openrouter",
		api:      models.APIOpenAIResponses,
		registry: registry,
	}

	require.Equal(t, "custom-model", client.getProviderModelID("custom-model"))
	require.Equal(t, "custom-owner", client.getModelOwner("custom-model"))
	require.Same(t, registry, client.registryOrDefault())
}

func TestOpenAIClient_GetModelOwnerHandlesNilClient(t *testing.T) {
	var client *OpenAIClient

	require.Empty(t, client.getModelOwner("gpt-4o-mini"))
	require.NotNil(t, client.registryOrDefault())
}

func TestOpenAIClient_ChatRequiresModel(t *testing.T) {
	client := &OpenAIClient{api: models.APIOpenAICompletions}

	_, err := client.Complete(context.Background(), Request{
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})
	require.EqualError(t, err, "model is required")
}

func TestOpenAIClient_ChatRequiresMessages(t *testing.T) {
	client := &OpenAIClient{api: models.APIOpenAICompletions}

	_, err := client.Complete(context.Background(), Request{Model: "test-model"})
	require.EqualError(t, err, "messages are required")
}

func TestOpenAIClient_ChatRequiresAPI(t *testing.T) {
	client := &OpenAIClient{}
	_, err := client.Complete(context.Background(), Request{
		Model:    "test-model",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})
	require.EqualError(t, err, "model API is required")
}

func TestOpenAIClient_ChatRequiresSelectedAPIHandler(t *testing.T) {
	client := &OpenAIClient{api: models.APIOpenAIResponses}
	_, err := client.Complete(context.Background(), Request{
		Model:    "test-model",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})
	require.EqualError(t, err, "model client is required")
}

func TestOpenAIClient_ChatRequiresChatCompletionsHand(t *testing.T) {
	client := &OpenAIClient{api: models.APIOpenAICompletions, createResponse: func(context.Context, responses.ResponseNewParams) (*responses.Response, error) {
		return &responses.Response{}, nil
	}}

	_, err := client.Complete(context.Background(), Request{
		Model:    "test-model",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})
	require.EqualError(t, err, "model client is required")
}

func TestOpenAIClient_ChatRejectsInvalidMessageRole(t *testing.T) {
	client := &OpenAIClient{api: models.APIOpenAICompletions}
	_, err := client.Complete(context.Background(), Request{
		Model:    "test-model",
		API:      models.APIOpenAICompletions,
		Messages: []morphmsg.Message{{Role: morphmsg.Role("invalid"), Content: "hello"}},
	})
	require.EqualError(t, err, "message role must be one of user, assistant, or tool; developer messages must be provided via instructions")
}

func TestOpenAIClient_ChatRejectsEmptyMessageContent(t *testing.T) {
	client := &OpenAIClient{api: models.APIOpenAICompletions}
	_, err := client.Complete(context.Background(), Request{
		Model:    "test-model",
		API:      models.APIOpenAICompletions,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "   "}},
	})
	require.EqualError(t, err, "message content is required")
}

func TestOpenAIClient_ChatRejectsDeveloperMessageInConversation(t *testing.T) {
	client := &OpenAIClient{api: models.APIOpenAICompletions}
	_, err := client.Complete(context.Background(), Request{
		Model:    "test-model",
		API:      models.APIOpenAICompletions,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleDeveloper, Content: "system"}},
	})
	require.EqualError(t, err, "developer messages must be provided via instructions")
}

func TestOpenAIClient_ChatRejectsBlankToolDefinitionName(t *testing.T) {
	client := &OpenAIClient{api: models.APIOpenAICompletions}
	_, err := client.Complete(context.Background(), Request{
		Model:    "test-model",
		API:      models.APIOpenAICompletions,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
		Tools:    []ToolDefinition{{Name: "   "}},
	})
	require.EqualError(t, err, "tool name is required")
}

func TestOpenAIClient_ChatReturnsAPIErrorChatCompletions(t *testing.T) {
	expectedErr := errors.New("upstream failed")
	client := &OpenAIClient{
		api: models.APIOpenAICompletions,
		createChatCompletion: func(context.Context, openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
			return nil, expectedErr
		},
	}

	_, err := client.Complete(context.Background(), Request{
		Model:    "test-model",
		API:      models.APIOpenAICompletions,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})
	require.ErrorIs(t, err, expectedErr)
}

func TestOpenAIClient_ChatRequiresChatCompletionsResponse(t *testing.T) {
	client := &OpenAIClient{
		api: models.APIOpenAICompletions,
		createChatCompletion: func(context.Context, openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
			return nil, nil
		},
	}

	_, err := client.Complete(context.Background(), Request{
		Model:    "test-model",
		API:      models.APIOpenAICompletions,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})
	require.EqualError(t, err, "model response is required")
}

func TestOpenAIClient_ChatReturnsResponseAndBuildsChatCompletionsRequest(t *testing.T) {
	var captured openai.ChatCompletionNewParams
	client := &OpenAIClient{
		api: models.APIOpenAICompletions,
		createChatCompletion: func(_ context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
			captured = params
			return &openai.ChatCompletion{
				ID:    "resp_123",
				Model: "returned-model",
				Choices: []openai.ChatCompletionChoice{{
					Message: openai.ChatCompletionMessage{Content: "hello back"},
				}},
			}, nil
		},
	}

	resp, err := client.Complete(context.Background(), Request{
		Model:        "test-model",
		API:          models.APIOpenAICompletions,
		Instructions: "  be concise  ",
		Messages: []morphmsg.Message{
			{Role: morphmsg.RoleUser, Content: "  hello  "},
			{Role: morphmsg.RoleAssistant, Content: " previous reply "},
		},
		MaxOutputTokens: 123,
		Temperature:     0.7,
	})
	require.NoError(t, err)
	require.Equal(t, &Response{ID: "resp_123", Model: "returned-model", OutputText: "hello back"}, resp)

	raw, err := json.Marshal(captured)
	require.NoError(t, err)
	rawText := string(raw)
	require.Contains(t, rawText, `"model":"test-model"`)
	require.Contains(t, rawText, `"max_completion_tokens":123`)
	require.Contains(t, rawText, `"temperature":0.7`)
	require.Contains(t, rawText, `"role":"developer"`)
	require.Contains(t, rawText, `"content":"be concise"`)
	require.Contains(t, rawText, `"role":"user"`)
	require.Contains(t, rawText, `"content":"hello"`)
	require.Contains(t, rawText, `"role":"assistant"`)
	require.Contains(t, rawText, `"content":"previous reply"`)
}

func TestOpenAIClient_ChatBuildsChatCompletionsStructuredOutputRequest(t *testing.T) {
	var captured openai.ChatCompletionNewParams
	client := &OpenAIClient{
		api: models.APIOpenAICompletions,
		createChatCompletion: func(_ context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
			captured = params
			return &openai.ChatCompletion{
				ID:    "resp_123",
				Model: "returned-model",
				Choices: []openai.ChatCompletionChoice{{
					Message: openai.ChatCompletionMessage{Content: `{"session_summary":"ok","current_task":"","discoveries":[],"open_questions":[],"next_actions":[]}`},
				}},
			}, nil
		},
	}

	_, err := client.Complete(context.Background(), Request{
		Model:    "test-model",
		API:      models.APIOpenAICompletions,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
		StructuredOutput: &StructuredOutput{
			Name:        "session_summary",
			Description: "summary payload",
			Strict:      true,
			Schema:      map[string]any{"type": "object"},
		},
	})
	require.NoError(t, err)

	raw, err := json.Marshal(captured)
	require.NoError(t, err)
	rawText := string(raw)
	require.Contains(t, rawText, `"response_format":{`)
	require.Contains(t, rawText, `"type":"json_schema"`)
	require.Contains(t, rawText, `"name":"session_summary"`)
	require.Contains(t, rawText, `"description":"summary payload"`)
	require.Contains(t, rawText, `"strict":true`)
}

func TestExtractChatCompletionsResponse_IncludesUsage(t *testing.T) {
	resp, err := extractChatCompletionsResponse(&openai.ChatCompletion{
		ID:    "resp_123",
		Model: "gpt-test",
		Choices: []openai.ChatCompletionChoice{{
			Message: openai.ChatCompletionMessage{Content: "hello"},
		}},
		Usage: openai.CompletionUsage{
			PromptTokens:     12,
			CompletionTokens: 7,
			TotalTokens:      19,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 12, resp.PromptTokens)
	require.Equal(t, 7, resp.CompletionTokens)
	require.Equal(t, 19, resp.TotalTokens)
}

func TestExtractResponsesResponse_IncludesUsage(t *testing.T) {
	resp, err := extractResponsesResponse(&responses.Response{
		ID:     "resp_123",
		Model:  "gpt-test",
		Status: responses.ResponseStatusCompleted,
		Output: []responses.ResponseOutputItemUnion{{
			Type:    "message",
			ID:      "msg_123",
			Role:    "assistant",
			Status:  "completed",
			Content: []responses.ResponseOutputMessageContentUnion{{Type: "output_text", Text: "hello"}},
		}},
		Usage: responses.ResponseUsage{
			InputTokens:  10,
			OutputTokens: 5,
			TotalTokens:  15,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 10, resp.PromptTokens)
	require.Equal(t, 5, resp.CompletionTokens)
	require.Equal(t, 15, resp.TotalTokens)
}

func TestOpenAIClient_ChatReturnsToolCallsFromChatCompletions(t *testing.T) {
	client := &OpenAIClient{
		api: models.APIOpenAICompletions,
		createChatCompletion: func(_ context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
			raw, err := json.Marshal(params)
			require.NoError(t, err)
			require.Contains(t, string(raw), `"name":"time"`)

			return &openai.ChatCompletion{
				ID:    "resp_123",
				Model: "returned-model",
				Choices: []openai.ChatCompletionChoice{{
					Message: openai.ChatCompletionMessage{ToolCalls: []openai.ChatCompletionMessageToolCallUnion{{
						ID:   "call-1",
						Type: "function",
						Function: openai.ChatCompletionMessageFunctionToolCallFunction{
							Name:      "time",
							Arguments: "{}",
						},
					}}},
				}},
			}, nil
		},
	}

	resp, err := client.Complete(context.Background(), Request{
		Model:    "test-model",
		API:      models.APIOpenAICompletions,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "what time is it?"}},
		Tools:    []ToolDefinition{{Name: "time", Description: "Returns the current time.", InputSchema: map[string]any{"type": "object"}}},
	})
	require.NoError(t, err)
	require.True(t, resp.RequiresToolCalls)
	require.Equal(t, []ToolCall{{ID: "call-1", Name: "time", Input: "{}"}}, resp.ToolCalls)
}

func TestOpenAIClient_ChatUsesFallbackIDForChatCompletionToolCallWithoutID(t *testing.T) {
	client := &OpenAIClient{
		api: models.APIOpenAICompletions,
		createChatCompletion: func(context.Context, openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
			return &openai.ChatCompletion{
				Choices: []openai.ChatCompletionChoice{{
					Message: openai.ChatCompletionMessage{ToolCalls: []openai.ChatCompletionMessageToolCallUnion{{
						Function: openai.ChatCompletionMessageFunctionToolCallFunction{Name: "time", Arguments: "{}"},
					}}},
				}},
			}, nil
		},
	}

	resp, err := client.Complete(context.Background(), Request{
		Model:    "test-model",
		API:      models.APIOpenAICompletions,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})
	require.NoError(t, err)
	require.Equal(t, []ToolCall{{ID: "functions.time:0", Name: "time", Input: "{}"}}, resp.ToolCalls)
}

func TestOpenAIClient_ChatRejectsChatCompletionToolCallWithoutName(t *testing.T) {
	client := &OpenAIClient{
		api: models.APIOpenAICompletions,
		createChatCompletion: func(context.Context, openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
			return &openai.ChatCompletion{
				Choices: []openai.ChatCompletionChoice{{
					Message: openai.ChatCompletionMessage{ToolCalls: []openai.ChatCompletionMessageToolCallUnion{{
						ID:       "call-1",
						Function: openai.ChatCompletionMessageFunctionToolCallFunction{Arguments: "{}"},
					}}},
				}},
			}, nil
		},
	}

	_, err := client.Complete(context.Background(), Request{
		Model:    "test-model",
		API:      models.APIOpenAICompletions,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})
	require.EqualError(t, err, "tool call name is required")
}

func TestOpenAIClient_ChatRejectsChatCompletionResponseWithoutChoices(t *testing.T) {
	client := &OpenAIClient{
		api: models.APIOpenAICompletions,
		createChatCompletion: func(context.Context, openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
			return &openai.ChatCompletion{ID: "resp_123"}, nil
		},
	}

	_, err := client.Complete(context.Background(), Request{
		Model:    "test-model",
		API:      models.APIOpenAICompletions,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})
	require.EqualError(t, err, `chat completion response "resp_123" contained no choices`)
}

func TestOpenAIClient_ChatRejectsEmptyChatCompletionResponse(t *testing.T) {
	client := &OpenAIClient{
		api: models.APIOpenAICompletions,
		createChatCompletion: func(context.Context, openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
			return &openai.ChatCompletion{
				ID:    "resp_empty",
				Model: "returned-model",
				Choices: []openai.ChatCompletionChoice{{
					Message: openai.ChatCompletionMessage{},
				}},
			}, nil
		},
	}

	_, err := client.Complete(context.Background(), Request{
		Model:    "test-model",
		API:      models.APIOpenAICompletions,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})
	require.EqualError(t, err, "model returned empty response")
}

func TestOpenAIClient_ChatUsesRefusalAsOutputText(t *testing.T) {
	client := &OpenAIClient{
		api: models.APIOpenAICompletions,
		createChatCompletion: func(context.Context, openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
			return &openai.ChatCompletion{
				ID:    "resp_refusal",
				Model: "returned-model",
				Choices: []openai.ChatCompletionChoice{{
					Message: openai.ChatCompletionMessage{Refusal: "I can't do that."},
				}},
			}, nil
		},
	}

	resp, err := client.Complete(context.Background(), Request{
		Model:    "test-model",
		API:      models.APIOpenAICompletions,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})
	require.NoError(t, err)
	require.Equal(t, &Response{ID: "resp_refusal", Model: "returned-model", OutputText: "I can't do that."}, resp)
}

func TestOpenAIClient_ChatReturnsResponseAndBuildsResponsesRequest(t *testing.T) {
	var captured responses.ResponseNewParams
	client := &OpenAIClient{
		api:      models.APIOpenAIResponses,
		provider: "openai",
		createResponse: func(_ context.Context, params responses.ResponseNewParams) (*responses.Response, error) {
			captured = params
			return &responses.Response{
				ID:    "resp_456",
				Model: "gpt-5.1",
				Output: []responses.ResponseOutputItemUnion{{
					Type: "message",
					Content: []responses.ResponseOutputMessageContentUnion{{
						Type: "output_text",
						Text: "hello from responses",
					}},
				}},
			}, nil
		},
	}

	resp, err := client.Complete(context.Background(), Request{
		Model:        "gpt-5.1",
		API:          models.APIOpenAIResponses,
		Instructions: "  be concise  ",
		Messages: []morphmsg.Message{
			{Role: morphmsg.RoleUser, Content: "  hello  "},
			{Role: morphmsg.RoleAssistant, Content: "calling tool", ToolCalls: []morphmsg.ToolCall{{ID: "call-1", Name: "time", Input: " {} "}}},
			{Role: morphmsg.RoleTool, Content: `{"output":"2026-03-24T00:00:00Z"}`, ToolCallID: "call-1"},
		},
		Tools:           []ToolDefinition{{Name: "time", Description: "Returns the current time.", InputSchema: map[string]any{"type": "object"}}},
		MaxOutputTokens: 111,
		Temperature:     0.5,
		Reasoning:       &models.ReasoningOptions{Effort: "high", Summary: true},
	})
	require.NoError(t, err)
	require.Equal(t, &Response{ID: "resp_456", Model: "gpt-5.1", OutputText: "hello from responses"}, resp)

	logutils.PrettyPrintJSON(captured, "<<")
	raw, err := json.Marshal(captured)
	require.NoError(t, err)
	rawText := string(raw)
	require.Contains(t, rawText, `"model":"gpt-5.1"`)
	require.Contains(t, rawText, `"instructions":"be concise"`)
	require.Contains(t, rawText, `"store":false`)
	require.Contains(t, rawText, `"parallel_tool_calls":true`)
	require.Contains(t, rawText, `"tool_choice":"auto"`)
	require.Contains(t, rawText, `"include":["reasoning.encrypted_content"]`)
	require.Contains(t, rawText, `"reasoning":{"effort":"high","summary":"auto"}`)
	require.Contains(t, rawText, `"max_output_tokens":111`)
	require.Contains(t, rawText, `"temperature":0.5`)
	require.Contains(t, rawText, `"type":"message"`)
	require.Contains(t, rawText, `"id":"msg_assistant_1"`)
	require.NotContains(t, rawText, `"id":"assistant_1"`)
	require.Contains(t, rawText, `"type":"input_text"`)
	require.Contains(t, rawText, `"type":"function_call"`)
	require.Contains(t, rawText, `"type":"function_call_output"`)
	require.Contains(t, rawText, `"call_id":"call-1"`)
	require.Contains(t, rawText, `"name":"time"`)
}

func TestOpenAIClient_ChatRoutesConfiguredAPIToResponses(t *testing.T) {
	var responseCalled bool
	var captured responses.ResponseNewParams
	client := &OpenAIClient{
		api: models.APIOpenAIResponses,
		createChatCompletion: func(context.Context, openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
			return nil, errors.New("completions should not be called")
		},
		createResponse: func(_ context.Context, params responses.ResponseNewParams) (*responses.Response, error) {
			responseCalled = true
			captured = params
			return &responses.Response{
				ID:    "resp_api",
				Model: "gpt-5.1",
				Output: []responses.ResponseOutputItemUnion{{
					Type: "message",
					Content: []responses.ResponseOutputMessageContentUnion{{
						Type: "output_text",
						Text: "hello from explicit api",
					}},
				}},
			}, nil
		},
	}

	resp, err := client.Complete(context.Background(), Request{
		Model:    "gpt-5.1",
		API:      models.APIOpenAIResponses,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})

	require.NoError(t, err)
	require.True(t, responseCalled)
	require.Equal(t, &Response{ID: "resp_api", Model: "gpt-5.1", OutputText: "hello from explicit api"}, resp)
	raw, err := json.Marshal(captured)
	require.NoError(t, err)
	rawText := string(raw)
	require.NotContains(t, rawText, `"tool_choice"`)
	require.NotContains(t, rawText, `"parallel_tool_calls"`)
}

func TestOpenAIClient_ChatBuildsResponsesStructuredOutputRequest(t *testing.T) {
	var captured responses.ResponseNewParams
	client := &OpenAIClient{
		api: models.APIOpenAIResponses,
		createResponse: func(_ context.Context, params responses.ResponseNewParams) (*responses.Response, error) {
			captured = params
			return &responses.Response{
				ID:    "resp_456",
				Model: "gpt-5.1",
				Output: []responses.ResponseOutputItemUnion{{
					Type: "message",
					Content: []responses.ResponseOutputMessageContentUnion{{
						Type: "output_text",
						Text: `{"session_summary":"ok","current_task":"","discoveries":[],"open_questions":[],"next_actions":[]}`,
					}},
				}},
			}, nil
		},
	}

	_, err := client.Complete(context.Background(), Request{
		Model:    "gpt-5.1",
		API:      models.APIOpenAIResponses,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
		StructuredOutput: &StructuredOutput{
			Name:        "session_summary",
			Description: "summary payload",
			Strict:      true,
			Schema:      map[string]any{"type": "object"},
		},
	})
	require.NoError(t, err)

	raw, err := json.Marshal(captured)
	require.NoError(t, err)
	rawText := string(raw)
	require.Contains(t, rawText, `"text":{"format":{`)
	require.Contains(t, rawText, `"type":"json_schema"`)
	require.Contains(t, rawText, `"name":"session_summary"`)
	require.Contains(t, rawText, `"description":"summary payload"`)
	require.Contains(t, rawText, `"strict":true`)
}

func TestOpenAIClient_ChatReturnsToolCallsFromResponses(t *testing.T) {
	client := &OpenAIClient{
		api: models.APIOpenAIResponses,
		createResponse: func(_ context.Context, params responses.ResponseNewParams) (*responses.Response, error) {
			raw, err := json.Marshal(params)
			require.NoError(t, err)
			require.Contains(t, string(raw), `"type":"function"`)

			return &responses.Response{
				ID:     "resp_789",
				Model:  "gpt-5.1",
				Output: []responses.ResponseOutputItemUnion{responseOutputItemFromJSON(t, `{"type":"function_call","call_id":"call-1","name":"time","arguments":"{}"}`)},
			}, nil
		},
	}

	resp, err := client.Complete(context.Background(), Request{
		Model:    "gpt-5.1",
		API:      models.APIOpenAIResponses,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "what time is it?"}},
		Tools:    []ToolDefinition{{Name: "time", Description: "Returns the current time.", InputSchema: map[string]any{"type": "object"}}},
	})
	require.NoError(t, err)
	require.True(t, resp.RequiresToolCalls)
	require.Equal(t, []ToolCall{{ID: "call-1", Name: "time", Input: "{}"}}, resp.ToolCalls)
}

func TestOpenAIClient_ChatReturnsResponseErrorResponses(t *testing.T) {
	expectedErr := errors.New("upstream failed")
	client := &OpenAIClient{
		api: models.APIOpenAIResponses,
		createResponse: func(context.Context, responses.ResponseNewParams) (*responses.Response, error) {
			return nil, expectedErr
		},
	}

	_, err := client.Complete(context.Background(), Request{
		Model:    "gpt-5.1",
		API:      models.APIOpenAIResponses,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})
	require.ErrorIs(t, err, expectedErr)
}

func TestOpenAIClient_ChatRequiresResponsesResponse(t *testing.T) {
	client := &OpenAIClient{
		api: models.APIOpenAIResponses,
		createResponse: func(context.Context, responses.ResponseNewParams) (*responses.Response, error) {
			return nil, nil
		},
	}

	_, err := client.Complete(context.Background(), Request{
		Model:    "gpt-5.1",
		API:      models.APIOpenAIResponses,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})
	require.EqualError(t, err, "model response is required")
}

func TestOpenAIClient_ChatRejectsEmptyResponsesOutput(t *testing.T) {
	client := &OpenAIClient{
		api: models.APIOpenAIResponses,
		createResponse: func(context.Context, responses.ResponseNewParams) (*responses.Response, error) {
			return &responses.Response{Status: responses.ResponseStatusCompleted}, nil
		},
	}

	_, err := client.Complete(context.Background(), Request{
		Model:    "gpt-5.1",
		API:      models.APIOpenAIResponses,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})
	require.EqualError(t, err, "model response contained no text or tool calls")
}

func TestOpenAIClient_ChatUsesFallbackIDForResponseToolCallWithoutID(t *testing.T) {
	client := &OpenAIClient{
		api: models.APIOpenAIResponses,
		createResponse: func(context.Context, responses.ResponseNewParams) (*responses.Response, error) {
			return &responses.Response{Output: []responses.ResponseOutputItemUnion{
				responseOutputItemFromJSON(t, `{"type":"function_call","name":"time","arguments":"{}"}`),
			}}, nil
		},
	}

	resp, err := client.Complete(context.Background(), Request{
		Model:    "gpt-5.1",
		API:      models.APIOpenAIResponses,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})
	require.NoError(t, err)
	require.Equal(t, []ToolCall{{ID: "functions.time:0", Name: "time", Input: "{}"}}, resp.ToolCalls)
}

func TestOpenAIClient_ChatRejectsResponseToolCallWithoutName(t *testing.T) {
	client := &OpenAIClient{
		api: models.APIOpenAIResponses,
		createResponse: func(context.Context, responses.ResponseNewParams) (*responses.Response, error) {
			return &responses.Response{Output: []responses.ResponseOutputItemUnion{
				responseOutputItemFromJSON(t, `{"type":"function_call","call_id":"call-1","arguments":"{}"}`),
			}}, nil
		},
	}

	_, err := client.Complete(context.Background(), Request{
		Model:    "gpt-5.1",
		API:      models.APIOpenAIResponses,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})
	require.EqualError(t, err, "tool call name is required")
}

func TestOpenAIClient_ChatReturnsResponsesFailureError(t *testing.T) {
	client := &OpenAIClient{
		api: models.APIOpenAIResponses,
		createResponse: func(context.Context, responses.ResponseNewParams) (*responses.Response, error) {
			return &responses.Response{
				Status: responses.ResponseStatusFailed,
				Error:  responses.ResponseError{Message: "provider failed"},
			}, nil
		},
	}

	_, err := client.Complete(context.Background(), Request{
		Model:    "gpt-5.1",
		API:      models.APIOpenAIResponses,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})
	require.EqualError(t, err, "provider failed")
}

func TestOpenAIClient_ChatReturnsResponsesIncompleteErrorWithoutUsableOutput(t *testing.T) {
	client := &OpenAIClient{
		api: models.APIOpenAIResponses,
		createResponse: func(context.Context, responses.ResponseNewParams) (*responses.Response, error) {
			return &responses.Response{
				Status:            responses.ResponseStatusIncomplete,
				IncompleteDetails: responses.ResponseIncompleteDetails{Reason: "max_output_tokens"},
			}, nil
		},
	}

	_, err := client.Complete(context.Background(), Request{
		Model:    "gpt-5.1",
		API:      models.APIOpenAIResponses,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})
	require.EqualError(t, err, "response incomplete: max_output_tokens")
}

func TestOpenAIClient_ChatReturnsResponsesIncompleteSuccessWithText(t *testing.T) {
	client := &OpenAIClient{
		api: models.APIOpenAIResponses,
		createResponse: func(context.Context, responses.ResponseNewParams) (*responses.Response, error) {
			return &responses.Response{
				ID:     "resp_123",
				Model:  "gpt-5.1",
				Status: responses.ResponseStatusIncomplete,
				Output: []responses.ResponseOutputItemUnion{{
					Type: "message",
					Content: []responses.ResponseOutputMessageContentUnion{{
						Type: "output_text",
						Text: "partial answer",
					}},
				}},
			}, nil
		},
	}

	resp, err := client.Complete(context.Background(), Request{
		Model:    "gpt-5.1",
		API:      models.APIOpenAIResponses,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})
	require.NoError(t, err)
	require.Equal(t, "partial answer", resp.OutputText)
}

func TestOpenAIClient_ChatRejectsUnexpectedResponsesStatus(t *testing.T) {
	client := &OpenAIClient{
		api: models.APIOpenAIResponses,
		createResponse: func(context.Context, responses.ResponseNewParams) (*responses.Response, error) {
			return &responses.Response{Status: responses.ResponseStatusInProgress}, nil
		},
	}

	_, err := client.Complete(context.Background(), Request{
		Model:    "gpt-5.1",
		API:      models.APIOpenAIResponses,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})
	require.EqualError(t, err, "response status is in_progress")
}

func TestOpenAIClient_ChatReturnsResponsesFailureErrorWithoutProviderMessage(t *testing.T) {
	client := &OpenAIClient{
		api: models.APIOpenAIResponses,
		createResponse: func(context.Context, responses.ResponseNewParams) (*responses.Response, error) {
			return &responses.Response{Status: responses.ResponseStatusFailed}, nil
		},
	}

	_, err := client.Complete(context.Background(), Request{
		Model:    "gpt-5.1",
		API:      models.APIOpenAIResponses,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})
	require.EqualError(t, err, "response failed")
}

func TestOpenAIClient_ChatReturnsResponsesIncompleteErrorWithUnknownReason(t *testing.T) {
	client := &OpenAIClient{
		api: models.APIOpenAIResponses,
		createResponse: func(context.Context, responses.ResponseNewParams) (*responses.Response, error) {
			return &responses.Response{Status: responses.ResponseStatusIncomplete}, nil
		},
	}

	_, err := client.Complete(context.Background(), Request{
		Model:    "gpt-5.1",
		API:      models.APIOpenAIResponses,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})
	require.EqualError(t, err, "response incomplete: unknown")
}

func TestHandesponsesStreamEvent_ReturnsFailedTerminalResponse(t *testing.T) {
	var event responses.ResponseStreamEventUnion
	require.NoError(t, event.UnmarshalJSON([]byte(`{
		"type":"response.failed",
		"sequence_number":1,
		"response":{
			"id":"resp_123",
			"object":"response",
			"created_at":0,
			"model":"gpt-5.1",
			"status":"failed",
			"error":{"message":"provider failed"},
			"output":[],
			"parallel_tool_calls":false,
			"temperature":1,
			"tool_choice":"auto",
			"tools":[],
			"top_p":1,
			"text":{"format":{"type":"text"}}
		}
	}`)))

	_, terminal, err := HandesponsesStreamEvent(event)

	require.NoError(t, err)
	require.NotNil(t, terminal)
	require.Equal(t, responses.ResponseStatusFailed, terminal.Status)
	require.Equal(t, "provider failed", terminal.Error.Message)
}

func TestHandesponsesStreamEvent_ReturnsIncompleteTerminalResponse(t *testing.T) {
	var event responses.ResponseStreamEventUnion
	require.NoError(t, event.UnmarshalJSON([]byte(`{
		"type":"response.incomplete",
		"sequence_number":1,
		"response":{
			"id":"resp_123",
			"object":"response",
			"created_at":0,
			"model":"gpt-5.1",
			"status":"incomplete",
			"incomplete_details":{"reason":"max_output_tokens"},
			"output":[],
			"parallel_tool_calls":false,
			"temperature":1,
			"tool_choice":"auto",
			"tools":[],
			"top_p":1,
			"text":{"format":{"type":"text"}}
		}
	}`)))

	_, terminal, err := HandesponsesStreamEvent(event)

	require.NoError(t, err)
	require.NotNil(t, terminal)
	require.Equal(t, responses.ResponseStatusIncomplete, terminal.Status)
	require.Equal(t, "max_output_tokens", terminal.IncompleteDetails.Reason)
}

func TestOpenAIClient_ChatLogsRequestDebugMetadataForChatCompletions(t *testing.T) {
	originalLogger := log.Logger
	originalLevel := zerolog.GlobalLevel()
	t.Cleanup(func() {
		log.Logger = originalLogger
		zerolog.SetGlobalLevel(originalLevel)
	})

	buf := &bytes.Buffer{}
	log.Logger = zerolog.New(buf)
	zerolog.SetGlobalLevel(zerolog.DebugLevel)

	client := &OpenAIClient{
		api: models.APIOpenAICompletions,
		createChatCompletion: func(_ context.Context, _ openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
			return &openai.ChatCompletion{
				ID:    "resp_123",
				Model: "test-model",
				Choices: []openai.ChatCompletionChoice{{
					Message: openai.ChatCompletionMessage{Content: "hello back"},
				}},
			}, nil
		},
	}

	_, err := client.Complete(context.Background(), Request{
		Model:         "test-model",
		API:           models.APIOpenAICompletions,
		Messages:      []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
		DebugRequests: true,
	})
	require.NoError(t, err)
	output := buf.String()
	require.Contains(t, output, "model client request started")
	require.Contains(t, output, "model client request completed")
	require.Contains(t, output, "model request debug metadata")
	require.Contains(t, output, `"api":"openai-completions"`)
	require.Contains(t, output, `"message_count":1`)
	require.Contains(t, output, `"tool_count":0`)
	require.Contains(t, output, `"requires_tool_calls":false`)
	require.NotContains(t, output, `"request"`)
	require.NotContains(t, output, "hello")
}

func TestOpenAIClient_ChatLogsRequestDebugMetadataForResponses(t *testing.T) {
	originalLogger := log.Logger
	originalLevel := zerolog.GlobalLevel()
	t.Cleanup(func() {
		log.Logger = originalLogger
		zerolog.SetGlobalLevel(originalLevel)
	})

	buf := &bytes.Buffer{}
	log.Logger = zerolog.New(buf)
	zerolog.SetGlobalLevel(zerolog.DebugLevel)

	client := &OpenAIClient{
		api: models.APIOpenAIResponses,
		createResponse: func(_ context.Context, _ responses.ResponseNewParams) (*responses.Response, error) {
			return &responses.Response{ID: "resp_123", Model: "gpt-5.1", Output: []responses.ResponseOutputItemUnion{{Type: "message", Content: []responses.ResponseOutputMessageContentUnion{{Type: "output_text", Text: "hello back"}}}}}, nil
		},
	}

	_, err := client.Complete(context.Background(), Request{Model: "gpt-5.1", API: models.APIOpenAIResponses, Instructions: "be concise", Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}}, DebugRequests: true})
	require.NoError(t, err)
	output := buf.String()
	require.Contains(t, output, "model client request started")
	require.Contains(t, output, "model client request completed")
	require.Contains(t, output, "model request debug metadata")
	require.Contains(t, output, `"api":"openai-responses"`)
	require.Contains(t, output, `"message_count":1`)
	require.Contains(t, output, `"stream":false`)
	require.NotContains(t, output, `"request"`)
	require.NotContains(t, output, "hello")
}

func TestOpenAIClient_ChatLogsModelClientRequestFailure(t *testing.T) {
	originalLogger := log.Logger
	originalLevel := zerolog.GlobalLevel()
	t.Cleanup(func() {
		log.Logger = originalLogger
		zerolog.SetGlobalLevel(originalLevel)
	})

	buf := &bytes.Buffer{}
	log.Logger = zerolog.New(buf)
	zerolog.SetGlobalLevel(zerolog.DebugLevel)

	client := &OpenAIClient{
		api: models.APIOpenAICompletions,
		createChatCompletion: func(context.Context, openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
			return nil, errors.New("upstream down")
		},
	}

	_, err := client.Complete(context.Background(), Request{
		Model:    "test-model",
		API:      models.APIOpenAICompletions,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})

	require.EqualError(t, err, "upstream down")
	output := buf.String()
	require.Contains(t, output, "model client request started")
	require.Contains(t, output, "model client request failed")
	require.Contains(t, output, `"error_kind":"operation_failed"`)
	require.NotContains(t, output, `"request"`)
	require.NotContains(t, output, "hello")
}

func TestOpenAIClient_ChatEnrichesAPIErrorWithResponseBody(t *testing.T) {
	originalLogger := log.Logger
	originalLevel := zerolog.GlobalLevel()
	t.Cleanup(func() {
		log.Logger = originalLogger
		zerolog.SetGlobalLevel(originalLevel)
	})

	buf := &bytes.Buffer{}
	log.Logger = zerolog.New(buf)
	zerolog.SetGlobalLevel(zerolog.DebugLevel)

	request, err := http.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", nil)
	require.NoError(t, err)
	client := &OpenAIClient{
		api: models.APIOpenAIResponses,
		createResponse: func(context.Context, responses.ResponseNewParams) (*responses.Response, error) {
			return nil, &openai.Error{
				Request:    request,
				Response:   &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(`{"message":"payload rejected"}`))},
				StatusCode: http.StatusBadRequest,
			}
		},
	}

	_, err = client.Complete(context.Background(), Request{
		Model:    "test-model",
		API:      models.APIOpenAIResponses,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), `{"message":"payload rejected"}`)
	require.Contains(t, buf.String(), `"provider_error":"{\"message\":\"payload rejected\"}"`)
}

func TestGetModelClientErrorKind_ClassifiesErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil", err: nil, want: ""},
		{name: "context canceled", err: context.Canceled, want: "context_canceled"},
		{name: "deadline exceeded", err: context.DeadlineExceeded, want: "timeout"},
		{name: "json", err: errors.New("invalid json payload"), want: "decode_failed"},
		{name: "timeout text", err: errors.New("request timeout"), want: "timeout"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, getModelClientErrorKind(tt.err))
		})
	}
}

func TestNormalizeGenerateRequestRequiresAPI(t *testing.T) {
	_, err := normalizeGenerateRequest(Request{Model: "test-model", Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}}})
	require.EqualError(t, err, "model API is required")
}

func TestNormalizeGenerateRequestAcceptsAPI(t *testing.T) {
	tests := []struct {
		name    string
		api     string
		wantAPI string
	}{
		{
			name:    "completions",
			api:     " OPENAI-COMPLETIONS ",
			wantAPI: models.APIOpenAICompletions,
		},
		{
			name:    "responses",
			api:     " OPENAI-RESPONSES ",
			wantAPI: models.APIOpenAIResponses,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			normalized, err := normalizeGenerateRequest(Request{
				Model:    "test-model",
				API:      tt.api,
				Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
			})

			require.NoError(t, err)
			require.Equal(t, tt.wantAPI, normalized.API)
		})
	}
}

func TestNormalizeGenerateRequestRejectsInvalidExplicitAPI(t *testing.T) {
	_, err := normalizeGenerateRequest(Request{
		Model:    "test-model",
		API:      models.APIAnthropicMessages,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})

	require.EqualError(t, err, "model API must be one of: openai-completions, openai-responses")
}

func TestBuildChatCompletionsRequestIncludesPlainAssistantMessage(t *testing.T) {
	params := buildChatCompletionsRequest(normalizedGenerateRequest{
		Model: "test-model",
		API:   models.APIOpenAICompletions,
		Messages: []morphmsg.Message{{
			Role:    morphmsg.RoleAssistant,
			Content: "hello back",
		}},
	})

	raw, err := json.Marshal(params)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"role":"assistant"`)
	require.Contains(t, string(raw), `"content":"hello back"`)
}

func TestBuildChatCompletionsRequestIncludesExplicitReasoningEffort(t *testing.T) {
	params := buildChatCompletionsRequest(normalizedGenerateRequest{
		Model:     "test-model",
		API:       models.APIOpenAICompletions,
		Messages:  []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
		Reasoning: &models.ReasoningOptions{Effort: "max"},
	})

	raw, err := json.Marshal(params)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"reasoning_effort":"max"`)
}

func TestBuildResponsesRequestIncludesSummaryWithoutEffort(t *testing.T) {
	params := buildResponsesRequest(normalizedGenerateRequest{
		Model:     "test-model",
		API:       models.APIOpenAIResponses,
		Messages:  []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
		Reasoning: &models.ReasoningOptions{Summary: true},
	})
	raw, err := json.Marshal(params)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"reasoning":{"summary":"auto"}`)
	require.NotContains(t, string(raw), `"effort"`)
}

func TestNormalizeGenerateRequestRejectsUnknownReasoningEffort(t *testing.T) {
	_, err := normalizeGenerateRequest(Request{
		Model:     "test-model",
		API:       models.APIOpenAIResponses,
		Messages:  []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
		Reasoning: &models.ReasoningOptions{Effort: "approximate"},
	})

	require.EqualError(
		t,
		err,
		`OpenAI reasoning effort "approximate" is not supported by the installed SDK`,
	)
}

func TestBuildChatCompletionsRequestIncludesAssistantToolCallContent(t *testing.T) {
	params := buildChatCompletionsRequest(normalizedGenerateRequest{
		Model: "test-model",
		API:   models.APIOpenAICompletions,
		Messages: []morphmsg.Message{{
			Role:      morphmsg.RoleAssistant,
			Content:   "calling tool",
			ToolCalls: []morphmsg.ToolCall{{ID: "call-1", Name: "time", Input: "{}"}},
		}},
	})

	raw, err := json.Marshal(params)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"content":"calling tool"`)
	require.Contains(t, string(raw), `"tool_calls"`)
}

func TestBuildChatCompletionsRequestIncludesToolMessages(t *testing.T) {
	params := buildChatCompletionsRequest(normalizedGenerateRequest{
		Model:        "test-model",
		API:          models.APIOpenAICompletions,
		Instructions: "be concise",
		Messages: []morphmsg.Message{
			{Role: morphmsg.RoleAssistant, ToolCalls: []morphmsg.ToolCall{{ID: "call-1", Name: "time", Input: "{}"}}},
			{Role: morphmsg.RoleTool, Content: `{"output":"2026-03-24T00:00:00Z"}`, ToolCallID: "call-1"},
		},
		Tools: []ToolDefinition{{Name: "time", Description: "Returns time", InputSchema: map[string]any{"type": "object"}}},
	})
	raw, err := json.Marshal(params)
	require.NoError(t, err)
	rawText := string(raw)
	require.Contains(t, rawText, `"role":"developer"`)
	require.Contains(t, rawText, `"tool_calls"`)
	require.Contains(t, rawText, `"tool_call_id":"call-1"`)
	require.Contains(t, rawText, `"tools"`)
}

func TestBuildChatCompletionsTools_NormalizesStrictObjectSchema(t *testing.T) {
	tools := buildChatCompletionsTools([]ToolDefinition{{
		Name:        "list_files",
		Description: "List files",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":           map[string]any{"type": "string"},
				"include_hidden": map[string]any{"type": "boolean"},
			},
		},
	}})

	raw, err := json.Marshal(tools)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"required":["include_hidden","path"]`)
	require.Contains(t, string(raw), `"include_hidden":{"type":["boolean","null"]}`)
	require.Contains(t, string(raw), `"path":{"type":["string","null"]}`)
}

func TestBuildResponsesTools_NormalizesStrictObjectSchema(t *testing.T) {
	tools := buildResponsesTools([]ToolDefinition{{
		Name:        "list_files",
		Description: "List files",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":           map[string]any{"type": "string"},
				"include_hidden": map[string]any{"type": "boolean"},
			},
		},
	}})

	raw, err := json.Marshal(tools)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"required":["include_hidden","path"]`)
	require.Contains(t, string(raw), `"include_hidden":{"type":["boolean","null"]}`)
	require.Contains(t, string(raw), `"path":{"type":["string","null"]}`)
}

func TestBuildResponsesTools_NormalizesBrowserSchemaWithoutRootUnion(t *testing.T) {
	definition := browsertool.Definition(nil)
	tools := buildResponsesTools([]ToolDefinition{{
		Name: definition.Name, Description: definition.Description, InputSchema: definition.InputSchema,
	}})

	raw, err := json.Marshal(tools)
	require.NoError(t, err)
	require.NotContains(t, string(raw), `"oneOf"`)
	require.Contains(t, string(raw), `"strict":true`)

	parameters := tools[0].OfFunction.Parameters
	require.NotContains(t, parameters, "oneOf")
	properties := parameters["properties"].(map[string]any)
	require.NotEmpty(t, properties["action"].(map[string]any)["enum"])
	require.Equal(t, []any{"string", "null"}, properties["session_id"].(map[string]any)["type"])
}

func TestBuildResponsesTools_PreservesRunCommandEnvironment(t *testing.T) {
	definition := runcommandtool.Definition(nil)
	tools := buildResponsesTools([]ToolDefinition{{
		Name: definition.Name, Description: definition.Description, InputSchema: definition.InputSchema,
	}})

	parameters := tools[0].OfFunction.Parameters
	properties := parameters["properties"].(map[string]any)
	environment := properties["env"].(map[string]any)
	require.Equal(t, []any{"array", "null"}, environment["type"])

	items := environment["items"].(map[string]any)
	require.Equal(t, "object", items["type"])
	require.False(t, items["additionalProperties"].(bool))
	require.ElementsMatch(t, []string{"name", "value"}, items["required"])
	require.Contains(t, items["properties"].(map[string]any), "name")
	require.Contains(t, items["properties"].(map[string]any), "value")
}

func TestNormalizeStrictJSONSchema_RecursesWithoutMutatingInput(t *testing.T) {
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"config": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"recursive": map[string]any{"type": "boolean"},
				},
			},
			"path": map[string]any{"type": "string"},
		},
	}

	normalized := normalizeStrictJSONSchema(input)
	require.Equal(t, []string{"config", "path"}, normalized["required"])

	nested := normalized["properties"].(map[string]any)["config"].(map[string]any)
	require.Equal(t, []any{"object", "null"}, nested["type"])
	require.Equal(t, []string{"recursive"}, nested["required"])
	require.Equal(
		t,
		[]any{"boolean", "null"},
		nested["properties"].(map[string]any)["recursive"].(map[string]any)["type"],
	)
	require.Equal(
		t,
		[]any{"string", "null"},
		normalized["properties"].(map[string]any)["path"].(map[string]any)["type"],
	)

	_, ok := input["required"]
	require.False(t, ok)
}

func TestNormalizeStrictJSONSchema_PreservesOriginallyRequiredProperties(t *testing.T) {
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{"type": "string"},
			"id":     map[string]any{"type": "string"},
		},
		"required": []string{"action"},
	}

	normalized := normalizeStrictJSONSchema(input)
	properties := normalized["properties"].(map[string]any)

	require.Equal(t, "string", properties["action"].(map[string]any)["type"])
	require.Equal(t, []any{"string", "null"}, properties["id"].(map[string]any)["type"])
	require.Equal(t, []string{"action", "id"}, normalized["required"])
}

func TestNormalizeStrictJSONSchema_DropsFreeformObjectProperties(t *testing.T) {
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string"},
			"env": map[string]any{
				"type": "object",
				"additionalProperties": map[string]any{
					"type": "string",
				},
			},
		},
	}

	normalized := normalizeStrictJSONSchema(input)
	properties := normalized["properties"].(map[string]any)
	require.Contains(t, properties, "command")
	require.NotContains(t, properties, "env")
	require.Equal(t, []string{"command"}, normalized["required"])
}

func TestNormalizeMessagesAcceptsAssistantToolCallWithoutContent(t *testing.T) {
	messages, err := normalizeMessages([]morphmsg.Message{{
		Role:      morphmsg.RoleAssistant,
		ToolCalls: []morphmsg.ToolCall{{ID: "call-1", Name: "time", Input: " {} "}},
	}})
	require.NoError(t, err)
	require.Equal(t, []morphmsg.Message{{
		Role:      morphmsg.RoleAssistant,
		ToolCalls: []morphmsg.ToolCall{{ID: "call-1", Name: "time", Input: "{}"}},
	}}, messages)
}

func TestNormalizeMessagesPropagatesToolCallNormalizationError(t *testing.T) {
	_, err := normalizeMessages([]morphmsg.Message{{
		Role:      morphmsg.RoleAssistant,
		ToolCalls: []morphmsg.ToolCall{{Name: "time", Input: "{}"}},
	}})
	require.EqualError(t, err, "tool call id is required")
}

func TestNormalizeMessagesTrimsName(t *testing.T) {
	messages, err := normalizeMessages([]morphmsg.Message{{
		Role:       morphmsg.RoleTool,
		Content:    `{"output":"ok"}`,
		Name:       " time ",
		ToolCallID: "call-1",
	}})
	require.NoError(t, err)
	require.Equal(t, "time", messages[0].Name)
}

func TestNormalizeMessagesRequiresToolCallIDForToolMessages(t *testing.T) {
	_, err := normalizeMessages([]morphmsg.Message{{
		Role:    morphmsg.RoleTool,
		Content: `{"output":"ok"}`,
	}})
	require.EqualError(t, err, "tool call id is required")
}

func TestNormalizeToolCallsRejectsMissingID(t *testing.T) {
	_, err := normalizeToolCalls([]morphmsg.ToolCall{{Name: "time", Input: "{}"}})
	require.EqualError(t, err, "tool call id is required")
}

func TestNormalizeToolCallsRejectsMissingName(t *testing.T) {
	_, err := normalizeToolCalls([]morphmsg.ToolCall{{ID: "call-1", Input: "{}"}})
	require.EqualError(t, err, "tool call name is required")
}

func TestNormalizeToolCallsReturnsNilWhenEmpty(t *testing.T) {
	toolCalls, err := normalizeToolCalls(nil)
	require.NoError(t, err)
	require.Nil(t, toolCalls)
}

func TestExtractChatCompletionsToolCallsReturnsNilWhenEmpty(t *testing.T) {
	toolCalls, err := extractChatCompletionsToolCalls(nil)
	require.NoError(t, err)
	require.Nil(t, toolCalls)
}

func TestExtractChatCompletionsToolCallsUsesFallbackIDWhenMissing(t *testing.T) {
	toolCalls, err := extractChatCompletionsToolCalls([]openai.ChatCompletionMessageToolCallUnion{{
		Function: openai.ChatCompletionMessageFunctionToolCallFunction{Name: "time", Arguments: "{}"},
	}})
	require.NoError(t, err)
	require.Equal(t, []ToolCall{{ID: "functions.time:0", Name: "time", Input: "{}"}}, toolCalls)
}

func TestExtractChatCompletionsToolCallsRejectsMissingName(t *testing.T) {
	_, err := extractChatCompletionsToolCalls([]openai.ChatCompletionMessageToolCallUnion{{
		ID:       "call-1",
		Function: openai.ChatCompletionMessageFunctionToolCallFunction{Arguments: "{}"},
	}})
	require.EqualError(t, err, "tool call name is required")
}

func responseOutputItemFromJSON(t *testing.T, raw string) responses.ResponseOutputItemUnion {
	t.Helper()
	var item responses.ResponseOutputItemUnion
	require.NoError(t, json.Unmarshal([]byte(raw), &item))
	return item
}

func newChatStreamServer(t *testing.T, chunks []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range chunks {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func newResponsesStreamServer(t *testing.T, events []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range events {
			var parsed map[string]any
			require.NoError(t, json.Unmarshal([]byte(event), &parsed))
			eventType := parsed["type"].(string)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, event)
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func chatStreamRequest() Request {
	return Request{
		Model:    "test-model",
		API:      models.APIOpenAICompletions,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	}
}

func responsesStreamRequest() Request {
	return Request{
		Model:    "gpt-5.1",
		API:      models.APIOpenAIResponses,
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	}
}

func TestOpenAIClient_CompleteStreamDelegatesWithStreamFlag(t *testing.T) {
	server := newChatStreamServer(t, []string{
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","created":0,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"}}]}`,
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","created":0,"model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`,
	})
	t.Cleanup(server.Close)

	caller := newOpenAICompletionStreamCaller(option.WithBaseURL(server.URL), option.WithAPIKey("test"))
	client := &OpenAIClient{api: models.APIOpenAICompletions, createChatStream: caller}

	var deltas []StreamDelta
	resp, err := client.CompleteStream(context.Background(), chatStreamRequest(), func(delta StreamDelta) {
		deltas = append(deltas, delta)
	})

	require.NoError(t, err)
	require.Equal(t, "hi", resp.OutputText)
	require.Equal(t, []StreamDelta{{Channel: models.StreamChannelAssistant, Text: "hi"}}, deltas)
	require.Equal(t, 5, resp.PromptTokens)
	require.Equal(t, 1, resp.CompletionTokens)
	require.Equal(t, 6, resp.TotalTokens)
}

func TestOpenAIClient_StreamRequiresResponseStreamHand(t *testing.T) {
	client := &OpenAIClient{api: models.APIOpenAIResponses}
	_, err := client.CompleteStream(context.Background(), responsesStreamRequest(), nil)
	require.EqualError(t, err, "model client is required")
}

func TestOpenAIClient_StreamRequiresChatStreamHand(t *testing.T) {
	client := &OpenAIClient{api: models.APIOpenAICompletions}
	_, err := client.CompleteStream(context.Background(), chatStreamRequest(), nil)
	require.EqualError(t, err, "model client is required")
}

func TestOpenAIClient_CompleteChatStreamReturnsResponseAndDeltas(t *testing.T) {
	server := newChatStreamServer(t, []string{
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","created":0,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":"hel"}}]}`,
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","created":0,"model":"test-model","choices":[{"index":0,"delta":{"content":"lo"}}]}`,
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","created":0,"model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`,
	})
	t.Cleanup(server.Close)

	caller := newOpenAICompletionStreamCaller(option.WithBaseURL(server.URL), option.WithAPIKey("test"))
	client := &OpenAIClient{api: models.APIOpenAICompletions, createChatStream: caller}

	var deltas []StreamDelta
	resp, err := client.CompleteStream(context.Background(), chatStreamRequest(), func(delta StreamDelta) {
		deltas = append(deltas, delta)
	})

	require.NoError(t, err)
	require.Equal(t, "hello", resp.OutputText)
	require.Equal(t, []StreamDelta{
		{Channel: models.StreamChannelAssistant, Text: "hel"},
		{Channel: models.StreamChannelAssistant, Text: "lo"},
	}, deltas)
}

func TestOpenAIClient_CompleteChatStreamEmitsReasoningDeltas(t *testing.T) {
	server := newChatStreamServer(t, []string{
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","created":0,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"thinking"}}]}`,
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","created":0,"model":"test-model","choices":[{"index":0,"delta":{"content":"answer"}}]}`,
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","created":0,"model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`,
	})
	t.Cleanup(server.Close)

	caller := newOpenAICompletionStreamCaller(option.WithBaseURL(server.URL), option.WithAPIKey("test"))
	client := &OpenAIClient{api: models.APIOpenAICompletions, createChatStream: caller}

	var deltas []StreamDelta
	resp, err := client.CompleteStream(context.Background(), chatStreamRequest(), func(delta StreamDelta) {
		deltas = append(deltas, delta)
	})

	require.NoError(t, err)
	require.Equal(t, "answer", resp.OutputText)
	require.Equal(t, []StreamDelta{
		{Channel: models.StreamChannelReasoning, Text: "thinking"},
		{Channel: models.StreamChannelAssistant, Text: "answer"},
	}, deltas)
}

func TestGetChatStreamReasoningDelta_ReadsExtraFields(t *testing.T) {
	delta := openai.ChatCompletionChunkChoiceDelta{}
	delta.JSON.ExtraFields = map[string]respjson.Field{
		"reasoning": respjson.NewField(`"thinking"`),
	}

	require.Equal(t, "thinking", getChatStreamReasoningDelta(delta))
}

func TestGetChatStreamReasoningDelta_IgnoresInvalidExtraFieldJSON(t *testing.T) {
	delta := openai.ChatCompletionChunkChoiceDelta{}
	delta.JSON.ExtraFields = map[string]respjson.Field{
		"reasoning": respjson.NewField(`{"text":"thinking"}`),
	}

	require.Empty(t, getChatStreamReasoningDelta(delta))
}

func TestGetChatStreamReasoningDelta_IgnoresInvalidRawJSON(t *testing.T) {
	require.Empty(t, getChatStreamReasoningDelta(openai.ChatCompletionChunkChoiceDelta{}))
}

func TestOpenAIClient_CompleteChatStreamSkipsEmptyDeltas(t *testing.T) {
	server := newChatStreamServer(t, []string{
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","created":0,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","created":0,"model":"test-model","choices":[{"index":0,"delta":{"content":"hi"}}]}`,
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","created":0,"model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`,
	})
	t.Cleanup(server.Close)

	caller := newOpenAICompletionStreamCaller(option.WithBaseURL(server.URL), option.WithAPIKey("test"))
	client := &OpenAIClient{api: models.APIOpenAICompletions, createChatStream: caller}

	var deltas []StreamDelta
	resp, err := client.CompleteStream(context.Background(), chatStreamRequest(), func(delta StreamDelta) {
		deltas = append(deltas, delta)
	})

	require.NoError(t, err)
	require.Equal(t, "hi", resp.OutputText)
	require.Equal(t, []StreamDelta{{Channel: models.StreamChannelAssistant, Text: "hi"}}, deltas)
}

func TestOpenAIClient_CompleteChatStreamHandlesNilCallback(t *testing.T) {
	server := newChatStreamServer(t, []string{
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","created":0,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"}}]}`,
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","created":0,"model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`,
	})
	t.Cleanup(server.Close)

	caller := newOpenAICompletionStreamCaller(option.WithBaseURL(server.URL), option.WithAPIKey("test"))
	client := &OpenAIClient{api: models.APIOpenAICompletions, createChatStream: caller}

	resp, err := client.CompleteStream(context.Background(), chatStreamRequest(), nil)

	require.NoError(t, err)
	require.Equal(t, "ok", resp.OutputText)
}

func TestOpenAIClient_CompleteChatStreamReturnsNilStreamError(t *testing.T) {
	client := &OpenAIClient{
		api: models.APIOpenAICompletions,
		createChatStream: func(context.Context, openai.ChatCompletionNewParams) *ssestream.Stream[openai.ChatCompletionChunk] {
			return nil
		},
	}

	_, err := client.completeChatStream(context.Background(), openai.ChatCompletionNewParams{}, nil)
	require.EqualError(t, err, "model response is required")
}

func TestOpenAIClient_CompleteResponsesStreamReturnsResponseAndDeltas(t *testing.T) {
	completedResponse := `{"id":"resp_123","object":"response","created_at":0,"model":"gpt-5.1","output":[{"id":"msg_123","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello back","annotations":[]}]}],"parallel_tool_calls":false,"temperature":1,"tool_choice":"auto","tools":[],"top_p":1,"status":"completed","text":{"format":{"type":"text"}}}`
	server := newResponsesStreamServer(t, []string{
		`{"type":"response.output_text.delta","item_id":"item_1","output_index":0,"content_index":0,"delta":"hello "}`,
		`{"type":"response.output_text.delta","item_id":"item_1","output_index":0,"content_index":0,"delta":"back"}`,
		fmt.Sprintf(`{"type":"response.completed","sequence_number":1,"response":%s}`, completedResponse),
	})
	t.Cleanup(server.Close)

	caller := newOpenAIResponseStreamCaller(option.WithBaseURL(server.URL), option.WithAPIKey("test"))
	client := &OpenAIClient{api: models.APIOpenAIResponses, createResponseStream: caller}

	var deltas []StreamDelta
	resp, err := client.CompleteStream(context.Background(), responsesStreamRequest(), func(delta StreamDelta) {
		deltas = append(deltas, delta)
	})

	require.NoError(t, err)
	require.Equal(t, "hello back", resp.OutputText)
	require.Equal(t, []StreamDelta{
		{Channel: models.StreamChannelAssistant, Text: "hello "},
		{Channel: models.StreamChannelAssistant, Text: "back"},
	}, deltas)
}

func TestOpenAIClient_CompleteResponsesUsesStreamTransportWhenForced(t *testing.T) {
	completedResponse := `{"id":"resp_123","object":"response","created_at":0,"model":"gpt-5.1","output":[{"id":"msg_123","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello back","annotations":[]}]}],"parallel_tool_calls":false,"temperature":1,"tool_choice":"auto","tools":[],"top_p":1,"status":"completed","text":{"format":{"type":"text"}}}`
	server := newResponsesStreamServer(t, []string{
		fmt.Sprintf(`{"type":"response.completed","sequence_number":1,"response":%s}`, completedResponse),
	})
	t.Cleanup(server.Close)

	var nonStreamCalled bool
	caller := newOpenAIResponseStreamCaller(option.WithBaseURL(server.URL), option.WithAPIKey("test"))
	client := &OpenAIClient{
		api:                  models.APIOpenAIResponses,
		forceResponsesStream: true,
		createResponseStream: caller,
		createResponse: func(context.Context, responses.ResponseNewParams) (*responses.Response, error) {
			nonStreamCalled = true
			return nil, errors.New("non-stream response call should not be used")
		},
	}

	resp, err := client.Complete(context.Background(), responsesStreamRequest())

	require.NoError(t, err)
	require.False(t, nonStreamCalled)
	require.Equal(t, "hello back", resp.OutputText)
}

func TestOpenAIClient_ForceResponsesStreamAccessors(t *testing.T) {
	var client *OpenAIClient
	require.False(t, client.ForceResponsesStreamEnabled())
	client.SetForceResponsesStream(true)

	client = &OpenAIClient{}
	require.False(t, client.ForceResponsesStreamEnabled())

	client.SetForceResponsesStream(true)
	require.True(t, client.ForceResponsesStreamEnabled())

	client.SetForceResponsesStream(false)
	require.False(t, client.ForceResponsesStreamEnabled())
}

func TestOpenAIClient_CompleteResponsesStreamUsesDeltasWhenFinalResponseOmitsText(t *testing.T) {
	completedResponse := `{"id":"resp_123","object":"response","created_at":0,"model":"gpt-5.1","output":[],"parallel_tool_calls":false,"temperature":1,"tool_choice":"auto","tools":[],"top_p":1,"status":"completed","text":{"format":{"type":"text"}}}`
	server := newResponsesStreamServer(t, []string{
		`{"type":"response.output_text.delta","item_id":"item_1","output_index":0,"content_index":0,"delta":"hello "}`,
		`{"type":"response.output_text.delta","item_id":"item_1","output_index":0,"content_index":0,"delta":"back"}`,
		fmt.Sprintf(`{"type":"response.completed","sequence_number":1,"response":%s}`, completedResponse),
	})
	t.Cleanup(server.Close)

	caller := newOpenAIResponseStreamCaller(option.WithBaseURL(server.URL), option.WithAPIKey("test"))
	client := &OpenAIClient{api: models.APIOpenAIResponses, createResponseStream: caller}

	resp, err := client.CompleteStream(context.Background(), responsesStreamRequest(), nil)

	require.NoError(t, err)
	require.Equal(t, "hello back", resp.OutputText)
}

func TestOpenAIClient_CompleteResponsesStreamUsesOutputTextDoneWhenFinalResponseOmitsText(t *testing.T) {
	completedResponse := `{"id":"resp_123","object":"response","created_at":0,"model":"gpt-5.1","output":[],"parallel_tool_calls":false,"temperature":1,"tool_choice":"auto","tools":[],"top_p":1,"status":"completed","text":{"format":{"type":"text"}}}`
	server := newResponsesStreamServer(t, []string{
		`{"type":"response.output_text.done","item_id":"item_1","output_index":0,"content_index":0,"text":"The latest time is 17:20.","logprobs":[]}`,
		fmt.Sprintf(`{"type":"response.completed","sequence_number":1,"response":%s}`, completedResponse),
	})
	t.Cleanup(server.Close)

	caller := newOpenAIResponseStreamCaller(option.WithBaseURL(server.URL), option.WithAPIKey("test"))
	client := &OpenAIClient{api: models.APIOpenAIResponses, createResponseStream: caller}

	resp, err := client.CompleteStream(context.Background(), responsesStreamRequest(), nil)

	require.NoError(t, err)
	require.Equal(t, "The latest time is 17:20.", resp.OutputText)
}

func TestOpenAIClient_CompleteResponsesStreamUsesContentPartDoneWhenFinalResponseOmitsText(t *testing.T) {
	completedResponse := `{"id":"resp_123","object":"response","created_at":0,"model":"gpt-5.1","output":[],"parallel_tool_calls":false,"temperature":1,"tool_choice":"auto","tools":[],"top_p":1,"status":"completed","text":{"format":{"type":"text"}}}`
	server := newResponsesStreamServer(t, []string{
		`{"type":"response.content_part.done","item_id":"item_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":"The latest time is 17:20.","annotations":[]},"sequence_number":1}`,
		fmt.Sprintf(`{"type":"response.completed","sequence_number":2,"response":%s}`, completedResponse),
	})
	t.Cleanup(server.Close)

	caller := newOpenAIResponseStreamCaller(option.WithBaseURL(server.URL), option.WithAPIKey("test"))
	client := &OpenAIClient{api: models.APIOpenAIResponses, createResponseStream: caller}

	resp, err := client.CompleteStream(context.Background(), responsesStreamRequest(), nil)

	require.NoError(t, err)
	require.Equal(t, "The latest time is 17:20.", resp.OutputText)
}

func TestGetResponsesStreamFinalText_ReturnsContentPartRefusal(t *testing.T) {
	var event responses.ResponseStreamEventUnion
	require.NoError(t, event.UnmarshalJSON([]byte(`{"type":"response.content_part.done","item_id":"item_1","output_index":0,"content_index":0,"part":{"type":"refusal","refusal":"I cannot do that."},"sequence_number":1}`)))

	require.Equal(t, "I cannot do that.", getResponsesStreamFinalText(event))
}

func TestGetResponsesStreamFinalText_IgnoresUnknownContentPart(t *testing.T) {
	var event responses.ResponseStreamEventUnion
	require.NoError(t, event.UnmarshalJSON([]byte(`{"type":"response.content_part.done","item_id":"item_1","output_index":0,"content_index":0,"part":{"type":"input_text","text":"ignored"},"sequence_number":1}`)))

	require.Empty(t, getResponsesStreamFinalText(event))
}

func TestGetResponsesStreamFinalText_ReturnsOutputItemMessageText(t *testing.T) {
	var event responses.ResponseStreamEventUnion
	require.NoError(t, event.UnmarshalJSON([]byte(`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg_123","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Hello","annotations":[]},{"type":"refusal","refusal":"No"}]},"sequence_number":1}`)))

	require.Equal(t, "HelloNo", getResponsesStreamFinalText(event))
}

func TestOpenAIClient_CompleteResponsesStreamUsesOutputItemDoneToolCallWhenFinalResponseOmitsOutput(t *testing.T) {
	completedResponse := `{"id":"resp_123","object":"response","created_at":0,"model":"gpt-5.1","output":[],"parallel_tool_calls":false,"temperature":1,"tool_choice":"auto","tools":[],"top_p":1,"status":"completed","text":{"format":{"type":"text"}}}`
	server := newResponsesStreamServer(t, []string{
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","call_id":"call-1","name":"time","arguments":"{\"format\":\"rfc3339\"}"},"sequence_number":1}`,
		fmt.Sprintf(`{"type":"response.completed","sequence_number":2,"response":%s}`, completedResponse),
	})
	t.Cleanup(server.Close)

	caller := newOpenAIResponseStreamCaller(option.WithBaseURL(server.URL), option.WithAPIKey("test"))
	client := &OpenAIClient{api: models.APIOpenAIResponses, createResponseStream: caller}

	resp, err := client.CompleteStream(context.Background(), responsesStreamRequest(), nil)

	require.NoError(t, err)
	require.True(t, resp.RequiresToolCalls)
	require.Equal(t, []ToolCall{{
		ID:    "call-1",
		Name:  "time",
		Input: `{"format":"rfc3339"}`,
	}}, resp.ToolCalls)
}

func TestGetResponseOutputItemToolCall_ReturnsFalseForNonFunctionItem(t *testing.T) {
	item := responseOutputItemFromJSON(t, `{"id":"msg_123","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello","annotations":[]}]}`)

	toolCall, ok, err := getResponseOutputItemToolCall(item, 0)

	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, ToolCall{}, toolCall)
}

func TestGetResponseOutputItemToolCall_UsesFallbackID(t *testing.T) {
	item := responseOutputItemFromJSON(t, `{"type":"function_call","name":"time","arguments":"{}"}`)

	toolCall, ok, err := getResponseOutputItemToolCall(item, 3)

	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, ToolCall{ID: "functions.time:3", Name: "time", Input: "{}"}, toolCall)
}

func TestOpenAIClient_CompleteResponsesStreamRejectsToolCallWithoutName(t *testing.T) {
	server := newResponsesStreamServer(t, []string{
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","call_id":"call-1","arguments":"{}"},"sequence_number":1}`,
	})
	t.Cleanup(server.Close)

	caller := newOpenAIResponseStreamCaller(option.WithBaseURL(server.URL), option.WithAPIKey("test"))
	client := &OpenAIClient{api: models.APIOpenAIResponses, createResponseStream: caller}

	_, err := client.CompleteStream(context.Background(), responsesStreamRequest(), nil)

	require.EqualError(t, err, "tool call name is required")
}

func TestOpenAIClient_CompleteResponsesStreamReturnsNilStreamError(t *testing.T) {
	client := &OpenAIClient{
		api: models.APIOpenAIResponses,
		createResponseStream: func(context.Context, responses.ResponseNewParams) *ssestream.Stream[responses.ResponseStreamEventUnion] {
			return nil
		},
	}

	_, err := client.completeResponsesStream(context.Background(), responses.ResponseNewParams{}, nil)
	require.EqualError(t, err, "model response is required")
}

func TestOpenAIClient_CompleteResponsesStreamRequiresFinalResponse(t *testing.T) {
	server := newResponsesStreamServer(t, []string{
		`{"type":"response.output_text.delta","item_id":"item_1","output_index":0,"content_index":0,"delta":"hello"}`,
	})
	t.Cleanup(server.Close)

	caller := newOpenAIResponseStreamCaller(option.WithBaseURL(server.URL), option.WithAPIKey("test"))
	client := &OpenAIClient{api: models.APIOpenAIResponses, createResponseStream: caller}

	_, err := client.CompleteStream(context.Background(), responsesStreamRequest(), nil)
	require.EqualError(t, err, "model response is required")
}

func TestOpenAIClient_CompleteResponsesStreamSkipsEmptyTextDeltas(t *testing.T) {
	completedResponse := `{"id":"resp_123","object":"response","created_at":0,"model":"gpt-5.1","output":[{"id":"msg_123","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok","annotations":[]}]}],"parallel_tool_calls":false,"temperature":1,"tool_choice":"auto","tools":[],"top_p":1,"status":"completed","text":{"format":{"type":"text"}}}`
	server := newResponsesStreamServer(t, []string{
		`{"type":"response.output_text.delta","item_id":"item_1","output_index":0,"content_index":0,"delta":""}`,
		`{"type":"response.output_text.delta","item_id":"item_1","output_index":0,"content_index":0,"delta":"ok"}`,
		fmt.Sprintf(`{"type":"response.completed","sequence_number":1,"response":%s}`, completedResponse),
	})
	t.Cleanup(server.Close)

	caller := newOpenAIResponseStreamCaller(option.WithBaseURL(server.URL), option.WithAPIKey("test"))
	client := &OpenAIClient{api: models.APIOpenAIResponses, createResponseStream: caller}

	var deltas []StreamDelta
	resp, err := client.CompleteStream(context.Background(), responsesStreamRequest(), func(delta StreamDelta) {
		deltas = append(deltas, delta)
	})

	require.NoError(t, err)
	require.Equal(t, "ok", resp.OutputText)
	require.Equal(t, []StreamDelta{{Channel: models.StreamChannelAssistant, Text: "ok"}}, deltas)
}

func TestHandesponsesStreamEvent_ReturnsOutputTextDelta(t *testing.T) {
	var event responses.ResponseStreamEventUnion
	require.NoError(t, event.UnmarshalJSON([]byte(`{"type":"response.output_text.delta","item_id":"item_1","output_index":0,"content_index":0,"delta":"hello"}`)))

	delta, terminal, err := HandesponsesStreamEvent(event)

	require.NoError(t, err)
	require.Nil(t, terminal)
	require.Equal(t, StreamDelta{Channel: models.StreamChannelAssistant, Text: "hello"}, delta)
}

func TestHandesponsesStreamEvent_ReturnsReasoningTextDelta(t *testing.T) {
	var event responses.ResponseStreamEventUnion
	require.NoError(t, event.UnmarshalJSON([]byte(`{"type":"response.reasoning_text.delta","item_id":"item_1","output_index":0,"content_index":0,"delta":"thinking..."}`)))

	delta, terminal, err := HandesponsesStreamEvent(event)

	require.NoError(t, err)
	require.Nil(t, terminal)
	require.Equal(t, StreamDelta{Channel: models.StreamChannelReasoning, Text: "thinking..."}, delta)
}

func TestHandesponsesStreamEvent_ReturnsReasoningSummaryTextDelta(t *testing.T) {
	var event responses.ResponseStreamEventUnion
	require.NoError(t, event.UnmarshalJSON([]byte(`{"type":"response.reasoning_summary_text.delta","item_id":"item_1","output_index":0,"content_index":0,"delta":"summary"}`)))

	delta, terminal, err := HandesponsesStreamEvent(event)

	require.NoError(t, err)
	require.Nil(t, terminal)
	require.Equal(t, StreamDelta{Channel: models.StreamChannelReasoningSummary, Text: "summary"}, delta)
}

func TestHandesponsesStreamEvent_ReturnsCompletedTerminalResponse(t *testing.T) {
	var event responses.ResponseStreamEventUnion
	require.NoError(t, event.UnmarshalJSON([]byte(`{
		"type":"response.completed",
		"sequence_number":1,
		"response":{
			"id":"resp_123",
			"object":"response",
			"created_at":0,
			"model":"gpt-5.1",
			"status":"completed",
			"output":[{"id":"msg_123","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done","annotations":[]}]}],
			"parallel_tool_calls":false,
			"temperature":1,
			"tool_choice":"auto",
			"tools":[],
			"top_p":1,
			"text":{"format":{"type":"text"}}
		}
	}`)))

	_, terminal, err := HandesponsesStreamEvent(event)

	require.NoError(t, err)
	require.NotNil(t, terminal)
	require.Equal(t, responses.ResponseStatusCompleted, terminal.Status)
}

func TestHandesponsesStreamEvent_ReturnsErrorWithMessage(t *testing.T) {
	var event responses.ResponseStreamEventUnion
	require.NoError(t, event.UnmarshalJSON([]byte(`{"type":"error","code":"server_error","message":"something broke"}`)))

	_, _, err := HandesponsesStreamEvent(event)

	require.EqualError(t, err, "something broke")
}

func TestHandesponsesStreamEvent_ReturnsErrorWithDefaultMessage(t *testing.T) {
	var event responses.ResponseStreamEventUnion
	require.NoError(t, event.UnmarshalJSON([]byte(`{"type":"error","code":"server_error","message":""}`)))

	_, _, err := HandesponsesStreamEvent(event)

	require.EqualError(t, err, "response failed")
}

func TestHandesponsesStreamEvent_ReturnsEmptyForUnknownEvent(t *testing.T) {
	var event responses.ResponseStreamEventUnion
	require.NoError(t, event.UnmarshalJSON([]byte(`{"type":"response.created","sequence_number":0,"response":{"id":"resp_123","object":"response","created_at":0,"model":"gpt-5.1","status":"in_progress","output":[],"parallel_tool_calls":false,"temperature":1,"tool_choice":"auto","tools":[],"top_p":1,"text":{"format":{"type":"text"}}}}`)))

	delta, terminal, err := HandesponsesStreamEvent(event)

	require.NoError(t, err)
	require.Nil(t, terminal)
	require.Equal(t, StreamDelta{}, delta)
}

func TestNormalizeStructuredOutput_ReturnsNilForEmptyName(t *testing.T) {
	result := normalizeStructuredOutput(&StructuredOutput{
		Name:   "   ",
		Schema: map[string]any{"type": "object"},
	})
	require.Nil(t, result)
}

func TestNormalizeStructuredOutput_ReturnsNilForEmptySchema(t *testing.T) {
	result := normalizeStructuredOutput(&StructuredOutput{
		Name:   "test",
		Schema: nil,
	})
	require.Nil(t, result)
}

func TestNormalizeStrictJSONSchema_ReturnsNilForEmptySchema(t *testing.T) {
	require.Nil(t, normalizeStrictJSONSchema(nil))
	require.Nil(t, normalizeStrictJSONSchema(map[string]any{}))
}

func TestNormalizeStrictJSONSchemaValue_HandlesArrays(t *testing.T) {
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tags": map[string]any{
				"type":  "array",
				"items": []any{map[string]any{"type": "string"}, map[string]any{"type": "number"}},
			},
		},
	}

	result := normalizeStrictJSONSchema(input)
	tags := result["properties"].(map[string]any)["tags"].(map[string]any)
	items := tags["items"].([]any)
	require.Len(t, items, 2)
	require.Equal(t, "string", items[0].(map[string]any)["type"])
	require.Equal(t, "number", items[1].(map[string]any)["type"])
}

func TestNormalizeStrictJSONSchemaValue_ReturnsPrimitives(t *testing.T) {
	result := normalizeStrictJSONSchemaValue("hello")
	require.Equal(t, "hello", result)

	result = normalizeStrictJSONSchemaValue(42)
	require.Equal(t, 42, result)

	result = normalizeStrictJSONSchemaValue(true)
	require.Equal(t, true, result)
}

func TestIsUnsupportedStrictJSONObjectProperty_ReturnsFalseForEmptySchema(t *testing.T) {
	require.False(t, isUnsupportedStrictJSONObjectProperty(map[string]any{}))
}

func TestIsUnsupportedStrictJSONObjectProperty_ReturnsFalseForNonObjectType(t *testing.T) {
	require.False(t, isUnsupportedStrictJSONObjectProperty(map[string]any{
		"type":                 "string",
		"additionalProperties": true,
	}))
}

func TestIsUnsupportedStrictJSONObjectProperty_ReturnsFalseWithoutAdditionalProperties(t *testing.T) {
	require.False(t, isUnsupportedStrictJSONObjectProperty(map[string]any{
		"type": "object",
	}))
}

func TestIsUnsupportedStrictJSONObjectProperty_ReturnsFalseForBoolFalse(t *testing.T) {
	require.False(t, isUnsupportedStrictJSONObjectProperty(map[string]any{
		"type":                 "object",
		"additionalProperties": false,
	}))
}

func TestNewOpenAICompletionStreamCaller_UsesSDKClient(t *testing.T) {
	server := newChatStreamServer(t, []string{
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","created":0,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"}}]}`,
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","created":0,"model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
	})
	t.Cleanup(server.Close)

	caller := newOpenAICompletionStreamCaller(option.WithBaseURL(server.URL), option.WithAPIKey("test"))
	stream := caller(context.Background(), openai.ChatCompletionNewParams{
		Model:    openai.ChatModel("test-model"),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hello")},
	})

	require.NotNil(t, stream)
	require.True(t, stream.Next())
}

func TestNewOpenAIResponseStreamCaller_UsesSDKClient(t *testing.T) {
	completedResponse := `{"id":"resp_123","object":"response","created_at":0,"model":"gpt-5.1","output":[{"id":"msg_123","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok","annotations":[]}]}],"parallel_tool_calls":false,"temperature":1,"tool_choice":"auto","tools":[],"top_p":1,"status":"completed","text":{"format":{"type":"text"}}}`
	server := newResponsesStreamServer(t, []string{
		fmt.Sprintf(`{"type":"response.completed","sequence_number":1,"response":%s}`, completedResponse),
	})
	t.Cleanup(server.Close)

	caller := newOpenAIResponseStreamCaller(option.WithBaseURL(server.URL), option.WithAPIKey("test"))
	stream := caller(context.Background(), responses.ResponseNewParams{
		Model: "gpt-5.1",
		Input: responses.ResponseNewParamsInputUnion{OfString: openai.String("hello")},
	})

	require.NotNil(t, stream)
	require.True(t, stream.Next())
}

func TestOpenAIClient_StreamLogsDebugDumpForChatCompletions(t *testing.T) {
	originalLogger := log.Logger
	originalLevel := zerolog.GlobalLevel()
	t.Cleanup(func() {
		log.Logger = originalLogger
		zerolog.SetGlobalLevel(originalLevel)
	})

	buf := &bytes.Buffer{}
	log.Logger = zerolog.New(buf)
	zerolog.SetGlobalLevel(zerolog.DebugLevel)

	server := newChatStreamServer(t, []string{
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","created":0,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"}}]}`,
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","created":0,"model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
	})
	t.Cleanup(server.Close)

	caller := newOpenAICompletionStreamCaller(option.WithBaseURL(server.URL), option.WithAPIKey("test"))
	client := &OpenAIClient{api: models.APIOpenAICompletions, createChatStream: caller}

	_, err := client.CompleteStream(context.Background(), Request{
		Model:         "test-model",
		API:           models.APIOpenAICompletions,
		Messages:      []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
		DebugRequests: true,
	}, nil)
	require.NoError(t, err)
	require.Contains(t, buf.String(), `"api":"openai-completions"`)
}

func TestOpenAIClient_StreamLogsDebugDumpForResponses(t *testing.T) {
	originalLogger := log.Logger
	originalLevel := zerolog.GlobalLevel()
	t.Cleanup(func() {
		log.Logger = originalLogger
		zerolog.SetGlobalLevel(originalLevel)
	})

	buf := &bytes.Buffer{}
	log.Logger = zerolog.New(buf)
	zerolog.SetGlobalLevel(zerolog.DebugLevel)

	completedResponse := `{"id":"resp_123","object":"response","created_at":0,"model":"gpt-5.1","output":[{"id":"msg_123","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok","annotations":[]}]}],"parallel_tool_calls":false,"temperature":1,"tool_choice":"auto","tools":[],"top_p":1,"status":"completed","text":{"format":{"type":"text"}}}`
	server := newResponsesStreamServer(t, []string{
		fmt.Sprintf(`{"type":"response.completed","sequence_number":1,"response":%s}`, completedResponse),
	})
	t.Cleanup(server.Close)

	caller := newOpenAIResponseStreamCaller(option.WithBaseURL(server.URL), option.WithAPIKey("test"))
	client := &OpenAIClient{api: models.APIOpenAIResponses, createResponseStream: caller}

	_, err := client.CompleteStream(context.Background(), Request{
		Model:         "gpt-5.1",
		API:           models.APIOpenAIResponses,
		Messages:      []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
		DebugRequests: true,
	}, nil)
	require.NoError(t, err)
	require.Contains(t, buf.String(), `"api":"openai-responses"`)
}

func TestOpenAIClient_CompleteChatStreamReturnsStreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {invalid json\n\n")
	}))
	t.Cleanup(server.Close)

	caller := newOpenAICompletionStreamCaller(option.WithBaseURL(server.URL), option.WithAPIKey("test"))
	client := &OpenAIClient{api: models.APIOpenAICompletions, createChatStream: caller}

	_, err := client.CompleteStream(context.Background(), chatStreamRequest(), nil)
	require.Error(t, err)
}

func TestOpenAIClient_CompleteResponsesStreamReturnsStreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: error\ndata: {invalid json\n\n")
	}))
	t.Cleanup(server.Close)

	caller := newOpenAIResponseStreamCaller(option.WithBaseURL(server.URL), option.WithAPIKey("test"))
	client := &OpenAIClient{api: models.APIOpenAIResponses, createResponseStream: caller}

	_, err := client.CompleteStream(context.Background(), responsesStreamRequest(), nil)
	require.Error(t, err)
}

func TestOpenAIClient_CompleteResponsesStreamReturnsEventError(t *testing.T) {
	server := newResponsesStreamServer(t, []string{
		`{"type":"error","code":"server_error","message":"upstream broke"}`,
	})
	t.Cleanup(server.Close)

	caller := newOpenAIResponseStreamCaller(option.WithBaseURL(server.URL), option.WithAPIKey("test"))
	client := &OpenAIClient{api: models.APIOpenAIResponses, createResponseStream: caller}

	_, err := client.CompleteStream(context.Background(), responsesStreamRequest(), nil)
	require.Error(t, err)
}

func TestOpenAIClient_CompleteChatStreamReturnsAccumulateError(t *testing.T) {
	server := newChatStreamServer(t, []string{
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","created":0,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"}}]}`,
		`{"id":"chatcmpl-DIFFERENT","object":"chat.completion.chunk","created":0,"model":"test-model","choices":[{"index":0,"delta":{"content":"x"}}]}`,
	})
	t.Cleanup(server.Close)

	caller := newOpenAICompletionStreamCaller(option.WithBaseURL(server.URL), option.WithAPIKey("test"))
	client := &OpenAIClient{api: models.APIOpenAICompletions, createChatStream: caller}

	_, err := client.CompleteStream(context.Background(), chatStreamRequest(), nil)
	require.EqualError(t, err, "failed to accumulate chat completion stream")
}

func TestIsUnsupportedStrictJSONObjectProperty_ReturnsTrueForNonBoolNonMapAdditionalProperties(t *testing.T) {
	require.True(t, isUnsupportedStrictJSONObjectProperty(map[string]any{
		"type":                 "object",
		"additionalProperties": "true",
	}))
}

func TestLogRequestDebugMetadataOmitsRequestBody(t *testing.T) {
	originalLogger := log.Logger
	originalLevel := zerolog.GlobalLevel()
	t.Cleanup(func() {
		log.Logger = originalLogger
		zerolog.SetGlobalLevel(originalLevel)
	})

	buf := &bytes.Buffer{}
	log.Logger = zerolog.New(buf)
	zerolog.SetGlobalLevel(zerolog.DebugLevel)

	logRequestDebugMetadata(normalizedGenerateRequest{
		API: models.APIOpenAIResponses,
	})

	output := buf.String()
	require.Contains(t, output, "model request debug metadata")
	require.Contains(t, output, `"api":"openai-responses"`)
	require.NotContains(t, output, `"request"`)
	require.NotContains(t, output, "authorization")
	require.NotContains(t, output, "api_key")
	require.NotContains(t, output, "secret-token")
}
