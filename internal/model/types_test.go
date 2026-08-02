package model

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/pkg/agent/message"
)

func TestToolCallsToMessageToolCalls_ConvertsModelToolCalls(t *testing.T) {
	toolCalls := []ToolCall{{
		ID:    "call-1",
		Name:  "time",
		Input: "{}",
	}}

	require.Equal(t, []message.ToolCall{{
		ID:    "call-1",
		Name:  "time",
		Input: "{}",
	}}, ToolCallsToMessageToolCalls(toolCalls))
}

func TestToolCallsToMessageToolCalls_ReturnsNilForEmptyInput(t *testing.T) {
	require.Nil(t, ToolCallsToMessageToolCalls(nil))
	require.Nil(t, ToolCallsToMessageToolCalls([]ToolCall{}))
}
