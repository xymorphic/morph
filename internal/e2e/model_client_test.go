package e2e

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	models "github.com/xymorphic/morph/internal/model"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
)

func TestNewTextClient_ReturnsSingleResponse(t *testing.T) {
	client := NewTextClient(" hello ")

	resp, err := client.Complete(context.Background(), models.Request{
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "ping"}},
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "hello", resp.OutputText)
	assert.Len(t, client.Requests(), 1)
	assert.Equal(t, "ping", client.Requests()[0].Messages[0].Content)
}

func TestNewToolClient_ValidatesToolRoundTrip(t *testing.T) {
	client := NewToolClient(models.ToolCall{ID: "call-1", Name: "time", Input: "{}"}, "done")

	first, err := client.Complete(context.Background(), models.Request{
		Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "what time"}},
	})
	require.NoError(t, err)
	require.NotNil(t, first)
	require.True(t, first.RequiresToolCalls)
	require.Len(t, first.ToolCalls, 1)
	assert.Equal(t, "time", first.ToolCalls[0].Name)

	second, err := client.Complete(context.Background(), models.Request{
		Messages: []morphmsg.Message{
			{Role: morphmsg.RoleUser, Content: "what time"},
			{Role: morphmsg.RoleAssistant, ToolCalls: []morphmsg.ToolCall{{ID: "call-1", Name: "time", Input: "{}"}}},
			{Role: morphmsg.RoleTool, Name: "time", ToolCallID: "call-1", Content: "12:00 UTC"},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.Equal(t, "done", second.OutputText)
}

func TestClient_StreamingAndErrors(t *testing.T) {
	t.Run("streams configured deltas", func(t *testing.T) {
		client := NewClient(StreamStep(
			"final",
			models.StreamDelta{Channel: models.StreamChannelAssistant, Text: "fi"},
			models.StreamDelta{Channel: models.StreamChannelAssistant, Text: "nal"},
		))

		var deltas []models.StreamDelta
		resp, err := client.CompleteStream(context.Background(), models.Request{}, func(delta models.StreamDelta) {
			deltas = append(deltas, delta)
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, "final", resp.OutputText)
		assert.Equal(t, []models.StreamDelta{
			{Channel: models.StreamChannelAssistant, Text: "fi"},
			{Channel: models.StreamChannelAssistant, Text: "nal"},
		}, deltas)
	})

	t.Run("returns configured error", func(t *testing.T) {
		client := NewClient(Step{Err: errors.New("boom")})
		resp, err := client.Complete(context.Background(), models.Request{})
		require.Error(t, err)
		assert.Nil(t, resp)
		assert.EqualError(t, err, "boom")
	})

	t.Run("nil client", func(t *testing.T) {
		resp, err := (*Client)(nil).Complete(context.Background(), models.Request{})
		require.Error(t, err)
		assert.Nil(t, resp)
		assert.EqualError(t, err, "e2e model client is required")
		assert.Nil(t, (*Client)(nil).Requests())
	})
}

func TestClient_ValidationPaths(t *testing.T) {
	t.Run("missing step", func(t *testing.T) {
		client := NewClient()
		resp, err := client.Complete(context.Background(), models.Request{})
		require.Error(t, err)
		assert.Nil(t, resp)
		assert.EqualError(t, err, "missing model client step")
	})

	t.Run("missing response", func(t *testing.T) {
		client := NewClient(Step{})
		resp, err := client.Complete(context.Background(), models.Request{})
		require.Error(t, err)
		assert.Nil(t, resp)
		assert.EqualError(t, err, "model client step response is required")
	})

	t.Run("check failure", func(t *testing.T) {
		client := NewClient(Step{
			Check: func(models.Request) error { return errors.New("bad request") },
			Response: &models.Response{
				OutputText: "ignored",
			},
		})

		resp, err := client.Complete(context.Background(), models.Request{})
		require.Error(t, err)
		assert.Nil(t, resp)
		assert.EqualError(t, err, "bad request")
	})

	t.Run("tool round trip failure", func(t *testing.T) {
		client := NewToolClient(models.ToolCall{ID: "call-1", Name: "time", Input: "{}"}, "done")
		_, err := client.Complete(context.Background(), models.Request{})
		require.NoError(t, err)

		resp, err := client.Complete(context.Background(), models.Request{
			Messages: []morphmsg.Message{
				{Role: morphmsg.RoleUser, Content: "what time"},
				{Role: morphmsg.RoleAssistant, ToolCalls: []morphmsg.ToolCall{{ID: "call-1", Name: "time", Input: "{}"}}},
			},
		})
		require.Error(t, err)
		assert.Nil(t, resp)
		assert.EqualError(t, err, "expected tool round-trip request messages")
	})

	t.Run("tool round trip missing assistant", func(t *testing.T) {
		check := AssertToolRoundTrip(models.ToolCall{ID: "call-1", Name: "time"})
		err := check(models.Request{
			Messages: []morphmsg.Message{
				{Role: morphmsg.RoleUser, Content: "what time"},
				{Role: morphmsg.RoleTool, Name: "time", ToolCallID: "call-1", Content: "12:00 UTC"},
				{Role: morphmsg.RoleAssistant, Content: "plain"},
			},
		})
		require.Error(t, err)
		assert.EqualError(t, err, "expected assistant tool-call message before follow-up completion")
	})

	t.Run("tool round trip missing matching tool message", func(t *testing.T) {
		check := AssertToolRoundTrip(models.ToolCall{ID: "call-1", Name: "time"})
		err := check(models.Request{
			Messages: []morphmsg.Message{
				{Role: morphmsg.RoleUser, Content: "what time"},
				{Role: morphmsg.RoleAssistant, ToolCalls: []morphmsg.ToolCall{{ID: "call-1", Name: "time"}}},
				{Role: morphmsg.RoleTool, Name: "time", ToolCallID: "other-call", Content: "12:00 UTC"},
			},
		})
		require.Error(t, err)
		assert.EqualError(t, err, `expected tool message for tool call "call-1"`)
	})

	t.Run("tool round trip invalid assistant or tool naming", func(t *testing.T) {
		check := AssertToolRoundTrip(models.ToolCall{ID: "call-1", Name: "time"})

		err := check(models.Request{
			Messages: []morphmsg.Message{
				{Role: morphmsg.RoleUser, Content: "what time"},
				{Role: morphmsg.RoleAssistant, ToolCalls: []morphmsg.ToolCall{
					{ID: "call-1", Name: "time"},
					{ID: "call-2", Name: "time"},
				}},
				{Role: morphmsg.RoleTool, Name: "time", ToolCallID: "call-1", Content: "12:00 UTC"},
			},
		})
		require.Error(t, err)
		assert.EqualError(t, err, "expected exactly one assistant tool call")

		err = check(models.Request{
			Messages: []morphmsg.Message{
				{Role: morphmsg.RoleUser, Content: "what time"},
				{Role: morphmsg.RoleAssistant, ToolCalls: []morphmsg.ToolCall{{ID: "wrong", Name: "time"}}},
				{Role: morphmsg.RoleTool, Name: "time", ToolCallID: "call-1", Content: "12:00 UTC"},
			},
		})
		require.Error(t, err)
		assert.EqualError(t, err, `expected assistant tool call id "call-1"`)

		err = check(models.Request{
			Messages: []morphmsg.Message{
				{Role: morphmsg.RoleUser, Content: "what time"},
				{Role: morphmsg.RoleAssistant, ToolCalls: []morphmsg.ToolCall{{ID: "call-1", Name: "clock"}}},
				{Role: morphmsg.RoleTool, Name: "time", ToolCallID: "call-1", Content: "12:00 UTC"},
			},
		})
		require.Error(t, err)
		assert.EqualError(t, err, `expected assistant tool call name "time"`)

		err = check(models.Request{
			Messages: []morphmsg.Message{
				{Role: morphmsg.RoleUser, Content: "what time"},
				{Role: morphmsg.RoleAssistant, ToolCalls: []morphmsg.ToolCall{{ID: "call-1", Name: "time"}}},
				{Role: morphmsg.RoleTool, Name: "clock", ToolCallID: "call-1", Content: "12:00 UTC"},
			},
		})
		require.Error(t, err)
		assert.EqualError(t, err, `expected tool message name "time"`)
	})

	t.Run("request cloning protects captured requests", func(t *testing.T) {
		client := NewTextClient("ok")
		req := models.Request{
			Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
			Tools:    []models.ToolDefinition{{Name: "time"}},
		}

		_, err := client.Complete(context.Background(), req)
		require.NoError(t, err)

		req.Messages[0].Content = "changed"
		req.Tools[0].Name = "changed"

		requests := client.Requests()
		require.Len(t, requests, 1)
		assert.Equal(t, "hello", requests[0].Messages[0].Content)
		assert.Equal(t, "time", requests[0].Tools[0].Name)
	})
}
