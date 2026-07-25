package provider_ollama

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	models "github.com/wandxy/morph/internal/model"
	morphmsg "github.com/wandxy/morph/pkg/agent/message"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) Do(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

func (errorReader) Close() error {
	return nil
}

func TestOllamaClient_CompleteConvertsRequestAndResponse(t *testing.T) {
	var captured chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/chat", r.URL.Path)
		require.Equal(t, "trace", r.Header.Get("X-Test"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))

		_, _ = w.Write([]byte(`{
			"model":"llama3.2:3b",
			"message":{
				"role":"assistant",
				"content":" done ",
				"tool_calls":[{"function":{"name":"lookup","arguments":{"city":"Lagos"}}}]
			},
			"prompt_eval_count":7,
			"eval_count":11
		}`))
	}))
	defer server.Close()

	client, err := NewOllamaClient(server.URL, map[string]string{" X-Test ": " trace "})
	require.NoError(t, err)

	resp, err := client.Complete(t.Context(), Request{
		Model:        "llama3.2:3b",
		Instructions: "be brief",
		Messages: []morphmsg.Message{
			{Role: morphmsg.RoleUser, Content: "hello"},
			{
				Role:    morphmsg.RoleAssistant,
				Content: "calling",
				ToolCalls: []morphmsg.ToolCall{{
					ID:    "call-1",
					Name:  "lookup",
					Input: `{"city":"Lagos"}`,
				}},
			},
			{Role: morphmsg.RoleTool, Content: "sunny", ToolCallID: "call-1"},
		},
		Tools: []ToolDefinition{{
			Name:        "lookup",
			Description: "Lookup weather",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"city": map[string]any{"type": "string"},
				},
			},
		}},
		StructuredOutput: &StructuredOutput{
			Name:   "answer",
			Schema: map[string]any{"type": "object"},
		},
		ContextLength:   8192,
		MaxOutputTokens: 32,
		Temperature:     0.2,
	})

	require.NoError(t, err)
	require.Equal(t, "done", resp.OutputText)
	require.Equal(t, []ToolCall{{
		ID:    "ollama_lookup_0",
		Name:  "lookup",
		Input: `{"city":"Lagos"}`,
	}}, resp.ToolCalls)
	require.True(t, resp.RequiresToolCalls)
	require.Equal(t, 7, resp.PromptTokens)
	require.Equal(t, 11, resp.CompletionTokens)
	require.Equal(t, 18, resp.TotalTokens)

	require.Equal(t, "llama3.2:3b", captured.Model)
	require.False(t, captured.Stream)
	require.Equal(t, []chatMessage{
		{Role: "system", Content: "be brief"},
		{Role: "user", Content: "hello"},
		{
			Role:    "assistant",
			Content: "calling",
			ToolCalls: []chatToolCall{{
				Function: chatToolCallFunction{
					Name:      "lookup",
					Arguments: json.RawMessage(`{"city":"Lagos"}`),
				},
			}},
		},
		{Role: "tool", Content: "sunny"},
	}, captured.Messages)
	require.Equal(t, "lookup", captured.Tools[0].Function.Name)
	require.Equal(t, map[string]any{"type": "object"}, captured.Format)
	require.Equal(t, map[string]any{"num_ctx": float64(8192), "num_predict": float64(32), "temperature": 0.2}, captured.Options)
}

func TestOllamaClient_CompleteStreamAccumulatesChunksAndUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var captured chatRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		require.True(t, captured.Stream)

		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte("\n"))
		_, _ = w.Write([]byte(`{"model":"llama3.2:3b","message":{"role":"assistant","thinking":"thinking"}}` + "\n"))
		_, _ = w.Write([]byte(`{"model":"llama3.2:3b","message":{"role":"assistant","content":"hel"}}` + "\n"))
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"lo"}}` + "\n"))
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","tool_calls":[{"function":{"name":"time","arguments":"{\"zone\":\"UTC\"}"}}]},"prompt_eval_count":2,"eval_count":3,"done":true}` + "\n"))
	}))
	defer server.Close()

	client, err := NewOllamaClient(server.URL, nil)
	require.NoError(t, err)

	var deltas []models.StreamDelta
	resp, err := client.CompleteStream(t.Context(), Request{
		Model:    "llama3.2:3b",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	}, func(delta models.StreamDelta) {
		deltas = append(deltas, delta)
	})

	require.NoError(t, err)
	require.Equal(t, []models.StreamDelta{
		{Channel: models.StreamChannelReasoning, Text: "thinking"},
		{Channel: models.StreamChannelAssistant, Text: "hel"},
		{Channel: models.StreamChannelAssistant, Text: "lo"},
	}, deltas)
	require.Equal(t, "hello", resp.OutputText)
	require.Equal(t, []ToolCall{{ID: "ollama_time_0", Name: "time", Input: `{"zone":"UTC"}`}}, resp.ToolCalls)
	require.Equal(t, 2, resp.PromptTokens)
	require.Equal(t, 3, resp.CompletionTokens)
	require.Equal(t, 5, resp.TotalTokens)
}

func TestOllamaClient_CompleteReturnsActionableConnectionError(t *testing.T) {
	client, err := NewOllamaClient("http://127.0.0.1:1", nil)
	require.NoError(t, err)

	_, err = client.Complete(t.Context(), Request{
		Model:    "llama3.2:3b",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})

	require.ErrorContains(t, err, "ollama is not reachable at http://127.0.0.1:1")
}

func TestOllamaClient_CompleteReturnsProviderStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "model not found", http.StatusNotFound)
	}))
	defer server.Close()

	client, err := NewOllamaClient(server.URL, nil)
	require.NoError(t, err)

	_, err = client.Complete(t.Context(), Request{
		Model:    "missing",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})

	require.EqualError(
		t,
		err,
		`ollama model "missing" is not installed or could not be found; run morph provider configure --provider ollama --model missing --pull or ollama pull missing: ollama request failed with status 404: model not found`,
	)
}

func TestOllamaClient_CompleteReturnsActionableToolError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "tools are unsupported by this model", http.StatusBadRequest)
	}))
	defer server.Close()

	client, err := NewOllamaClient(server.URL, nil)
	require.NoError(t, err)

	_, err = client.Complete(t.Context(), Request{
		Model:    "llama3.2:3b",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})

	require.ErrorContains(
		t,
		err,
		"ollama tool calling failed; choose a tool-capable Ollama model or disable tools",
	)
}

func TestOllamaClient_CompleteRejectsRawToolJSONOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"model":"llama3.2:3b","message":{"role":"assistant","content":"{\"tool\":\"lookup\",\"arguments\":{\"city\":\"Lagos\"}}"}}`))
	}))
	defer server.Close()

	client, err := NewOllamaClient(server.URL, nil)
	require.NoError(t, err)

	_, err = client.Complete(t.Context(), Request{
		Model:    "llama3.2:3b",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
		Tools:    []ToolDefinition{{Name: "lookup"}},
	})

	require.EqualError(
		t,
		err,
		`ollama model "llama3.2:3b" returned raw tool JSON instead of a structured tool call; choose a tool-capable Ollama model or disable tools`,
	)
}

func TestOllamaClient_CompleteRejectsEmptyProviderResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"model":"llama3.2:3b","message":{"role":"assistant","content":" "}}`))
	}))
	defer server.Close()

	client, err := NewOllamaClient(server.URL, nil)
	require.NoError(t, err)

	_, err = client.Complete(t.Context(), Request{
		Model:    "llama3.2:3b",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})

	require.EqualError(t, err, "model returned empty response")
}

func TestOllamaClient_CompleteReturnsValidationError(t *testing.T) {
	client, err := NewOllamaClient("http://127.0.0.1:11434", nil)
	require.NoError(t, err)

	_, err = client.Complete(t.Context(), Request{})

	require.EqualError(t, err, "model is required")
}

func TestOllamaClient_RejectsNativeBaseURLWithV1Path(t *testing.T) {
	_, err := NewOllamaClient("http://127.0.0.1:11434/v1", nil)

	require.EqualError(t, err, "ollama native API base URL must not include /v1: http://127.0.0.1:11434/v1")
}

func TestOllamaClient_RejectsInvalidConstruction(t *testing.T) {
	_, err := NewOllamaClient(" ", nil)
	require.EqualError(t, err, "ollama base URL is required")

	_, err = NewOllamaClient("://bad", nil)
	require.EqualError(t, err, `ollama base URL "://bad" is invalid`)

	_, err = newOllamaClient("http://127.0.0.1:11434", nil, nil)
	require.EqualError(t, err, "ollama HTTP client is required")
}

func TestOllamaClient_CompleteRejectsNilClient(t *testing.T) {
	var client *OllamaClient

	_, err := client.Complete(t.Context(), Request{})

	require.EqualError(t, err, "model client is required")
}

func TestOllamaClient_CompleteReturnsEncodeError(t *testing.T) {
	client, err := newOllamaClient("http://127.0.0.1:11434", nil, roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			t.Fatal("request should not be sent when JSON encoding fails")
			return nil, nil
		},
	))
	require.NoError(t, err)

	_, err = client.doJSON(t.Context(), "/api/chat", map[string]any{"bad": math.Inf(1)})

	require.ErrorContains(t, err, "unsupported value")
}

func TestOllamaClient_CompleteReturnsRequestEncodeError(t *testing.T) {
	client, err := newOllamaClient("http://127.0.0.1:11434", nil, roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			t.Fatal("request should not be sent when tool arguments are invalid JSON")
			return nil, nil
		},
	))
	require.NoError(t, err)

	_, err = client.Complete(t.Context(), Request{
		Model: "llama3.2:3b",
		Messages: []morphmsg.Message{{
			Role: morphmsg.RoleAssistant,
			ToolCalls: []morphmsg.ToolCall{{
				ID:    "call-1",
				Name:  "broken",
				Input: "{bad json}",
			}},
		}},
	})

	require.ErrorContains(t, err, "invalid character")
}

func TestOllamaClient_DoJSONReturnsRequestBuildError(t *testing.T) {
	client := &OllamaClient{baseURL: "%", httpClient: roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			t.Fatal("request should not be sent when URL construction fails")
			return nil, nil
		},
	)}

	_, err := client.doJSON(t.Context(), "/api/chat", map[string]string{"ok": "true"})

	require.ErrorContains(t, err, "invalid URL")
}

func TestOllamaClient_CompleteStreamReturnsProviderStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client, err := NewOllamaClient(server.URL, nil)
	require.NoError(t, err)

	_, err = client.CompleteStream(t.Context(), Request{
		Model:    "llama3.2:3b",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	}, nil)

	require.EqualError(t, err, "ollama request failed with status 500: 500 Internal Server Error")
}

func TestOllamaClient_CompleteStreamRejectsRawToolJSONOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"model":"llama3.2:3b","message":{"role":"assistant","content":"{\"function\":\"lookup\",\"arguments\":{}}"}}` + "\n"))
	}))
	defer server.Close()

	client, err := NewOllamaClient(server.URL, nil)
	require.NoError(t, err)

	_, err = client.CompleteStream(t.Context(), Request{
		Model:    "llama3.2:3b",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
		Tools:    []ToolDefinition{{Name: "lookup"}},
	}, nil)

	require.ErrorContains(t, err, "returned raw tool JSON instead of a structured tool call")
}

func TestOllamaClient_CompleteStreamReturnsConnectionError(t *testing.T) {
	client, err := newOllamaClient("http://127.0.0.1:11434", nil, roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial failed")
		},
	))
	require.NoError(t, err)

	_, err = client.CompleteStream(t.Context(), Request{
		Model:    "llama3.2:3b",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	}, nil)

	require.ErrorContains(t, err, "ollama is not reachable at http://127.0.0.1:11434")
}

func TestOllamaClient_CompleteStreamReturnsDecodeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{bad json}\n"))
	}))
	defer server.Close()

	client, err := NewOllamaClient(server.URL, nil)
	require.NoError(t, err)

	_, err = client.CompleteStream(t.Context(), Request{
		Model:    "llama3.2:3b",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	}, nil)

	require.ErrorContains(t, err, "decode Ollama stream chunk")
}

func TestOllamaClient_CompleteStreamReturnsReadError(t *testing.T) {
	client, err := newOllamaClient("http://127.0.0.1:11434", nil, roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       errorReader{},
			}, nil
		},
	))
	require.NoError(t, err)

	_, err = client.CompleteStream(t.Context(), Request{
		Model:    "llama3.2:3b",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	}, nil)

	require.EqualError(t, err, "read failed")
}

func TestOllamaClient_CompleteStreamRejectsEmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"done":true}` + "\n"))
	}))
	defer server.Close()

	client, err := NewOllamaClient(server.URL, nil)
	require.NoError(t, err)

	_, err = client.CompleteStream(t.Context(), Request{
		Model:    "llama3.2:3b",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	}, nil)

	require.EqualError(t, err, "model returned empty response")
}

func TestOllamaClient_NormalizeRequestRejectsInvalidInput(t *testing.T) {
	_, err := normalizeRequest(Request{})
	require.EqualError(t, err, "model is required")

	_, err = normalizeRequest(Request{Model: "llama3.2:3b"})
	require.EqualError(t, err, "messages are required")

	_, err = normalizeRequest(Request{
		Model:    "llama3.2:3b",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleDeveloper, Content: "system"}},
	})
	require.EqualError(t, err, "developer messages must be provided via instructions")

	_, err = normalizeRequest(Request{
		Model:    "llama3.2:3b",
		Messages: []morphmsg.Message{{Role: "other", Content: "hello"}},
	})
	require.EqualError(t, err, "message role must be one of user, assistant, or tool; developer messages must be provided via instructions")

	_, err = normalizeRequest(Request{
		Model:    "llama3.2:3b",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: " "}},
	})
	require.EqualError(t, err, "message content is required")

	_, err = normalizeRequest(Request{
		Model:    "llama3.2:3b",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleTool, Content: "done"}},
	})
	require.EqualError(t, err, "tool call id is required")

	_, err = normalizeRequest(Request{
		Model: "llama3.2:3b",
		Messages: []morphmsg.Message{{
			Role:      morphmsg.RoleAssistant,
			ToolCalls: []morphmsg.ToolCall{{Name: "time", Input: "{}"}},
		}},
	})
	require.EqualError(t, err, "tool call id is required")

	_, err = normalizeRequest(Request{
		Model: "llama3.2:3b",
		Messages: []morphmsg.Message{{
			Role:      morphmsg.RoleAssistant,
			ToolCalls: []morphmsg.ToolCall{{ID: "call-1", Input: "{}"}},
		}},
	})
	require.EqualError(t, err, "tool call name is required")

	_, err = normalizeRequest(Request{
		Model:    "llama3.2:3b",
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
		Tools:    []ToolDefinition{{Name: " "}},
	})
	require.EqualError(t, err, "tool name is required")
}

func TestOllamaClient_NormalizesOptionalValues(t *testing.T) {
	require.Nil(t, normalizeStructuredOutput(nil))
	require.Nil(t, normalizeStructuredOutput(&StructuredOutput{Name: "answer"}))
	require.Nil(t, toolsToChatTools(nil))
	require.Nil(t, buildOptions(normalizedRequest{}))
	require.Nil(t, toolCallsFromChatToolCalls(nil, 0))
	require.Nil(t, normalizeHeaders(map[string]string{" ": "ignored", "x-empty": " "}))
	require.Empty(t, stringFromRawArguments(nil))
	require.Equal(t, "not-json", stringFromRawArguments(json.RawMessage("not-json")))
	require.Equal(t, "{}", defaultToolArguments(" "))
	require.Empty(t, structuredOutputToFormat(nil))
	require.False(t, isRawToolJSONOutput(`{"answer":"lookup"}`, []chatTool{{Function: chatFunction{Name: "lookup"}}}))
	require.False(t, isRawToolJSONOutput(`not-json`, []chatTool{{Function: chatFunction{Name: "lookup"}}}))
	require.False(t, isRawToolJSONOutput(`{"tool":"lookup"}`, nil))
	require.True(t, isRawToolJSONOutput(`{"name":"lookup"}`, []chatTool{{Function: chatFunction{Name: "lookup"}}}))

	toolCalls := toolCallsFromChatToolCalls([]chatToolCall{{Function: chatToolCallFunction{}}}, 0)
	require.Empty(t, toolCalls)
}

func TestOllamaClient_PreservesContextErrors(t *testing.T) {
	require.True(t, errors.Is(enrichOllamaConnectionError("http://ollama", context.Canceled), context.Canceled))
	require.True(t, errors.Is(enrichOllamaConnectionError("http://ollama", context.DeadlineExceeded), context.DeadlineExceeded))
	require.NoError(t, enrichOllamaConnectionError("http://ollama", nil))
}

func TestOllamaClient_DoJSONWrapsNetworkError(t *testing.T) {
	client, err := newOllamaClient("http://127.0.0.1:11434", nil, roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial failed")
		},
	))
	require.NoError(t, err)

	_, err = client.doJSON(t.Context(), "/api/chat", map[string]string{"ok": "true"})

	require.ErrorContains(t, err, "ollama is not reachable at http://127.0.0.1:11434")
	require.ErrorContains(t, err, "dial failed")
}

var _ io.ReadCloser = errorReader{}
