package tui

import (
	"time"

	models "github.com/xymorphic/morph/internal/model"
	"github.com/xymorphic/morph/internal/trace"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/str"
)

func newToolInvocationStartedMsg(
	id string,
	name string,
	detail string,
	startedAt time.Time,
) (toolInvocationStartedMsg, bool) {
	idValue := str.String(id)
	id = idValue.Trim()
	nameValue := str.String(name)
	name = nameValue.Trim()
	detailValue := str.String(detail)
	detail = detailValue.Trim()
	if name == "" && id == "" {
		return toolInvocationStartedMsg{}, false
	}

	return toolInvocationStartedMsg{
		ID:        id,
		Name:      name,
		Detail:    detail,
		StartedAt: startedAt,
	}, true
}

func newToolInvocationStartedMsgWithState(
	id string,
	name string,
	detail string,
	planState *trace.PlanToolState,
	processState *trace.ProcessToolState,
	startedAt time.Time,
) (toolInvocationStartedMsg, bool) {
	msg, ok := newToolInvocationStartedMsg(id, name, detail, startedAt)
	if !ok {
		return toolInvocationStartedMsg{}, false
	}
	msg.PlanState = planState
	msg.ProcessState = processState
	return msg, true
}

func newToolInvocationCompletedMsg(
	id string,
	name string,
	detail string,
	completedAt time.Time,
) (toolInvocationCompletedMsg, bool) {
	idValue2 := str.String(id)
	id = idValue2.Trim()
	nameValue2 := str.String(name)
	name = nameValue2.Trim()
	detailValue2 := str.String(detail)
	detail = detailValue2.Trim()
	if name == "" && id == "" {
		return toolInvocationCompletedMsg{}, false
	}

	return toolInvocationCompletedMsg{
		ID:          id,
		Name:        name,
		Detail:      detail,
		CompletedAt: completedAt,
	}, true
}

func newToolInvocationCompletedMsgWithState(
	id string,
	name string,
	detail string,
	failed bool,
	planState *trace.PlanToolState,
	processState *trace.ProcessToolState,
	completedAt time.Time,
) (toolInvocationCompletedMsg, bool) {
	msg, ok := newToolInvocationCompletedMsg(id, name, detail, completedAt)
	if !ok {
		return toolInvocationCompletedMsg{}, false
	}
	msg.Failed = failed
	msg.PlanState = planState
	msg.ProcessState = processState
	return msg, true
}

func toolInvocationStartedMsgFromModelToolCall(
	toolCall models.ToolCall,
	startedAt time.Time,
) (toolInvocationStartedMsg, bool) {
	return newToolInvocationStartedMsgWithState(
		toolCall.ID,
		toolCall.Name,
		getToolInputDisplayDetail(toolCall.Name, toolCall.Input),
		getToolInputDisplayState(toolCall.Name, toolCall.Input),
		getToolInputProcessDisplayState(toolCall.Name, toolCall.Input),
		startedAt,
	)
}

func toolInvocationStartedMsgFromMessageToolCall(
	toolCall morphmsg.ToolCall,
	startedAt time.Time,
) (toolInvocationStartedMsg, bool) {
	return newToolInvocationStartedMsgWithState(
		toolCall.ID,
		toolCall.Name,
		getToolInputDisplayDetail(toolCall.Name, toolCall.Input),
		getToolInputDisplayState(toolCall.Name, toolCall.Input),
		getToolInputProcessDisplayState(toolCall.Name, toolCall.Input),
		startedAt,
	)
}

func toolInvocationCompletedMsgFromMessage(
	message morphmsg.Message,
	completedAt time.Time,
) (toolInvocationCompletedMsg, bool) {
	msg, ok := newToolInvocationCompletedMsgWithState(
		message.ToolCallID,
		message.Name,
		getToolOutputDisplayDetail(message.Name, message.Content),
		trace.ToolInvocationFailed(message.Content),
		getToolOutputDisplayState(message.Name, message.Content),
		getToolOutputProcessDisplayState(message.Name, message.Content),
		completedAt,
	)
	if !ok {
		return toolInvocationCompletedMsg{}, false
	}
	msg.Failure = getToolFailureDisplayDetail(message.Name, message.Content)
	msg.Artifact = getBrowserArtifact(message.Name, message.Content)
	return msg, true
}
