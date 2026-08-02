package context

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
)

func TestBuilder_BuildReturnsSessionHistoryThenEmittedMessages(t *testing.T) {
	builder := New()
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)

	messages := builder.Build(Input{
		SessionHistory:  []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "before", CreatedAt: now}},
		EmittedMessages: []morphmsg.Message{{Role: morphmsg.RoleAssistant, Content: "after", CreatedAt: now.Add(time.Second)}},
	})

	require.Equal(t, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "before", CreatedAt: now},
		{Role: morphmsg.RoleAssistant, Content: "after", CreatedAt: now.Add(time.Second)},
	}, messages)
}

func TestBuilder_BuildClonesReturnedMessages(t *testing.T) {
	builder := New()
	input := Input{
		SessionHistory: []morphmsg.Message{{
			Role:      morphmsg.RoleAssistant,
			ToolCalls: []morphmsg.ToolCall{{ID: "call-1", Name: "time", Input: "{}"}},
		}},
		EmittedMessages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	}

	messages := builder.Build(input)
	messages[0].ToolCalls[0].Name = "changed"
	messages[1].Content = "mutated"

	require.Equal(t, "time", input.SessionHistory[0].ToolCalls[0].Name)
	require.Equal(t, "hello", input.EmittedMessages[0].Content)
}

func TestBuilder_BuildReturnsOnlyEmittedMessagesWhenHistoryEmpty(t *testing.T) {
	builder := New()
	messages := builder.Build(Input{
		EmittedMessages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	})
	require.Equal(t, []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}}, messages)
}

func TestBuilder_BuildReturnsOnlySessionHistoryWhenEmittedMessagesEmpty(t *testing.T) {
	builder := New()
	messages := builder.Build(Input{
		SessionHistory: []morphmsg.Message{{Role: morphmsg.RoleAssistant, Content: "hello"}},
	})
	require.Equal(t, []morphmsg.Message{{Role: morphmsg.RoleAssistant, Content: "hello"}}, messages)
}

func TestBuilder_BuildIncludesPrefixMessages(t *testing.T) {
	builder := New()
	messages := builder.Build(Input{
		PrefixMessages:  []morphmsg.Message{{Role: morphmsg.RoleDeveloper, Content: "summary"}},
		SessionHistory:  []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "history"}},
		EmittedMessages: []morphmsg.Message{{Role: morphmsg.RoleAssistant, Content: "emitted"}},
	})

	require.Equal(t, []morphmsg.Message{
		{Role: morphmsg.RoleDeveloper, Content: "summary"},
		{Role: morphmsg.RoleUser, Content: "history"},
		{Role: morphmsg.RoleAssistant, Content: "emitted"},
	}, messages)
}

func TestBuilder_BuildReturnsNilWhenAllInputsEmpty(t *testing.T) {
	builder := New()
	require.Nil(t, builder.Build(Input{}))
}

func TestBuilder_BuildAddsUnavailableToolResultForMissingToolCallResponse(t *testing.T) {
	builder := New()

	messages := builder.Build(Input{
		SessionHistory: []morphmsg.Message{
			{
				Role: morphmsg.RoleAssistant,
				ToolCalls: []morphmsg.ToolCall{{
					ID:   "call-1",
					Name: "session_search",
				}},
			},
			{Role: morphmsg.RoleUser, Content: "next turn"},
		},
	})

	require.Len(t, messages, 3)
	require.Equal(t, morphmsg.RoleAssistant, messages[0].Role)
	require.Equal(t, morphmsg.RoleTool, messages[1].Role)
	require.Equal(t, "call-1", messages[1].ToolCallID)
	require.Equal(t, "session_search", messages[1].Name)
	require.Contains(t, messages[1].Content, "Tool result unavailable")
	require.Equal(t, morphmsg.RoleUser, messages[2].Role)
}

func TestBuilder_BuildDropsOrphanToolMessages(t *testing.T) {
	builder := New()

	messages := builder.Build(Input{
		SessionHistory: []morphmsg.Message{
			{Role: morphmsg.RoleUser, Content: "hello"},
			{Role: morphmsg.RoleTool, ToolCallID: "orphan", Name: "time", Content: "12:00"},
			{Role: morphmsg.RoleAssistant, Content: "hi"},
		},
	})

	require.Equal(t, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "hello"},
		{Role: morphmsg.RoleAssistant, Content: "hi"},
	}, messages)
}

func TestBuilder_BuildKeepsToolResultsImmediatelyAfterAssistantToolCalls(t *testing.T) {
	builder := New()

	messages := builder.Build(Input{
		SessionHistory: []morphmsg.Message{
			{
				Role: morphmsg.RoleAssistant,
				ToolCalls: []morphmsg.ToolCall{
					{ID: "call-1", Name: "read_file"},
					{ID: "call-2", Name: "write_file"},
				},
			},
			{Role: morphmsg.RoleTool, ToolCallID: "call-2", Name: "write_file", Content: "wrote"},
			{Role: morphmsg.RoleTool, ToolCallID: "call-1", Name: "read_file", Content: "read"},
			{Role: morphmsg.RoleAssistant, Content: "done"},
		},
	})

	require.Len(t, messages, 4)
	require.Equal(t, morphmsg.RoleAssistant, messages[0].Role)
	require.Equal(t, "call-1", messages[1].ToolCallID)
	require.Equal(t, "read", messages[1].Content)
	require.Equal(t, "call-2", messages[2].ToolCallID)
	require.Equal(t, "wrote", messages[2].Content)
	require.Equal(t, "done", messages[3].Content)
}
