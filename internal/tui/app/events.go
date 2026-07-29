package tui

import (
	"strings"
	"time"

	models "github.com/wandxy/morph/internal/model"
	"github.com/wandxy/morph/internal/trace"
	agent "github.com/wandxy/morph/pkg/agent"
	morphmsg "github.com/wandxy/morph/pkg/agent/message"
	"github.com/wandxy/morph/pkg/str"
)

type userMessageAcceptedMsg struct {
	Text         string
	QueueEntryID string
}

type assistantTextDeltaMsg struct {
	Channel string
	Text    string
}

type assistantResponseCompletedMsg struct {
	Text string
}

type reasoningCompletedMsg struct {
	Duration time.Duration
	Summary  string
	ID       string
}

type toolInvocationStartedMsg struct {
	ID           string
	Name         string
	Detail       string
	PlanState    *trace.PlanToolState
	ProcessState *trace.ProcessToolState
	StartedAt    time.Time
}

type toolInvocationCompletedMsg struct {
	ID           string
	Name         string
	Detail       string
	Failure      string
	Failed       bool
	PlanState    *trace.PlanToolState
	ProcessState *trace.ProcessToolState
	CompletedAt  time.Time
	Artifact     *browserArtifact
}

type safetyEventMsg struct {
	Kind       string
	Action     string
	Refusal    string
	Blocked    bool
	Redacted   bool
	FindingIDs []string
}

type sessionErrorMsg struct {
	Message string
}

type manualCompactionMsg struct {
	State manualCompactionState
}

type permissionApprovalMsg struct {
	RequestID  string
	Status     string
	Scope      string
	Summary    string
	Reason     string
	Effects    []string
	Operations []string
	ExpiresAt  time.Time
}

type permissionResolutionCompletedMsg struct {
	RequestID  string
	Status     string
	Scope      string
	Summary    string
	Reason     string
	Effects    []string
	Operations []string
	ExpiresAt  time.Time
	Err        error
}

func agentEventToTUIMessage(event agent.Event) (any, bool) {
	if traceEvent, ok := traceEventFromAgentEvent(event); ok {
		return traceEventToTUIMessage(traceEvent)
	}
	if event.Text == "" {
		return nil, false
	}

	channelValue := str.String(event.Channel)
	channel := channelValue.Trim()
	if channel == "" {
		channel = "assistant"
	}

	return assistantTextDeltaMsg{Channel: channel, Text: event.Text}, true
}

func traceEventFromAgentEvent(event agent.Event) (trace.Event, bool) {
	switch value := event.TraceEvent.(type) {
	case trace.Event:
		return value, true
	case *trace.Event:
		if value == nil {
			return trace.Event{}, false
		}
		return *value, true
	default:
		return trace.Event{}, false
	}
}

func traceEventToTUIMessage(event trace.Event) (any, bool) {
	typedPayload, payloadOK := trace.DecodePayload(event.Type, event.Payload)
	trimmedValueValue := str.String(event.Type)
	switch trimmedValueValue.Trim() {
	case trace.EvtUserMessageAccepted:
		payload, ok := typedPayload.(trace.UserMessageAcceptedPayload)
		if text := firstNonEmptyTUI(payload.Message, payload.Text); payloadOK && ok && text != "" {
			return userMessageAcceptedMsg{Text: text}, true
		}
	case trace.EvtFinalAssistantResponse:
		payload, ok := typedPayload.(trace.FinalAssistantResponsePayload)
		if text := firstNonEmptyTUI(payload.Message, payload.Text); payloadOK && ok && text != "" {
			return assistantResponseCompletedMsg{Text: text}, true
		}
	case trace.EvtModelReasoningCompleted:
		payload, ok := typedPayload.(trace.ModelReasoningCompletedPayload)
		if payloadOK && ok && payload.DurationMS > 0 {
			return reasoningCompletedMsg{
				Duration: time.Duration(payload.DurationMS) * time.Millisecond,
				Summary:  payload.Summary,
			}, true
		}
	case trace.EvtToolInvocationStarted:
		msg, ok := toolCallPayloadToTUIMessage(event.Payload)
		if !ok {
			return nil, false
		}
		if toolMsg, ok := msg.(toolInvocationStartedMsg); ok {
			toolMsg.StartedAt = getTraceEventTimestamp(event)
			return toolMsg, true
		}
		return msg, true
	case trace.EvtToolInvocationCompleted:
		msg, ok := toolMessagePayloadToTUIMessage(event.Payload)
		if !ok {
			return nil, false
		}
		if toolMsg, ok := msg.(toolInvocationCompletedMsg); ok {
			toolMsg.CompletedAt = getTraceEventTimestamp(event)
			return toolMsg, true
		}
		return msg, true
	case trace.EvtInputSafetyBlocked,
		trace.EvtOutputSafetyApplied,
		trace.EvtToolOutputSafetyApplied,
		trace.EvtLoadedContentSafetyBlocked:
		return safetyPayloadToTUIMessage(event.Type, event.Payload)
	case trace.EvtSessionFailed:
		payload, ok := typedPayload.(trace.SessionFailedPayload)
		if message := firstNonEmptyTUI(payload.Error, payload.Message); payloadOK && ok && message != "" {
			if isUserStoppedSessionError(message) {
				return nil, false
			}

			return sessionErrorMsg{Message: message}, true
		}
	case trace.EvtContextCompactionPending,
		trace.EvtContextCompactionRunning,
		trace.EvtContextCompactionSucceeded,
		trace.EvtContextCompactionFailed:
		payload, ok := typedPayload.(trace.CompactionEventPayload)
		if !payloadOK || !ok {
			return nil, false
		}
		if state := manualCompactionStateFromTraceEvent(event.Type, payload); state.isVisible() {
			return manualCompactionMsg{State: state}, true
		}
	case trace.EvtPermissionApprovalChanged:
		payload, ok := typedPayload.(trace.PermissionApprovalPayload)
		if payloadOK && ok && payload.RequestID != "" {
			return permissionApprovalMsg{
				RequestID:  payload.RequestID,
				Status:     payload.Status,
				Scope:      payload.Scope,
				Summary:    payload.Summary,
				Reason:     payload.Reason,
				Effects:    append([]string(nil), payload.Effects...),
				Operations: append([]string(nil), payload.Operations...),
				ExpiresAt:  payload.ExpiresAt,
			}, true
		}
	}

	return nil, false
}

func isUserStoppedSessionError(message string) bool {
	messageValue := str.String(message)
	message = messageValue.Normalized()
	if message == "" {
		return false
	}

	return message == "context canceled" ||
		message == "context_canceled" ||
		strings.Contains(message, "context canceled") ||
		strings.Contains(message, "context_canceled")
}

func getTraceEventTimestamp(event trace.Event) time.Time {
	return event.Timestamp
}

func toolCallPayloadToTUIMessage(payload any) (any, bool) {
	switch value := payload.(type) {
	case models.ToolCall:
		msg, ok := toolInvocationStartedMsgFromModelToolCall(value, time.Time{})
		if !ok {
			return nil, false
		}
		return msg, true
	case morphmsg.ToolCall:
		msg, ok := toolInvocationStartedMsgFromMessageToolCall(value, time.Time{})
		if !ok {
			return nil, false
		}
		return msg, true
	default:
		toolPayload, ok := trace.ToolInvocationStartedPayloadFrom(payload)
		if !ok {
			return nil, false
		}
		msg, ok := newToolInvocationStartedMsgWithState(
			toolPayload.ID,
			toolPayload.Name,
			firstNonEmptyTUI(
				toolPayload.Detail,
				getSafeTraceToolInputDisplayDetail(toolPayload.Name, toolPayload.Input),
			),
			toolPayload.PlanState,
			toolPayload.ProcessState,
			time.Time{},
		)
		if !ok {
			return nil, false
		}

		return msg, true
	}
}

func toolMessagePayloadToTUIMessage(payload any) (any, bool) {
	switch value := payload.(type) {
	case morphmsg.Message:
		msg, ok := toolInvocationCompletedMsgFromMessage(value, time.Time{})
		if !ok {
			return nil, false
		}
		return msg, true
	default:
		toolPayload, ok := trace.ToolInvocationCompletedPayloadFrom(payload)
		if !ok {
			return nil, false
		}
		msg, ok := newToolInvocationCompletedMsgWithState(
			toolPayload.ToolCallID,
			toolPayload.Name,
			toolPayload.Detail,
			toolPayload.Failed,
			toolPayload.PlanState,
			toolPayload.ProcessState,
			time.Time{},
		)
		if !ok {
			return nil, false
		}

		msg.Failure = getToolFailureDisplayDetail(toolPayload.Name, toolPayload.Content)
		msg.Artifact = getBrowserArtifact(toolPayload.Name, toolPayload.Content)
		return msg, true
	}
}

func getSafeTraceToolInputDisplayDetail(name string, input string) string {
	switch normalizeToolDisplayName(name) {
	case "automation", "browser":
	default:
		return ""
	}

	return getToolInputDisplayDetail(name, input)
}

func safetyPayloadToTUIMessage(kind string, payload any) (any, bool) {
	typedPayload, ok := payload.(trace.SafetyEventPayload)
	if !ok {
		decoded, decodedOK := trace.DecodePayload(kind, payload)
		typedPayload, ok = decoded.(trace.SafetyEventPayload)
		if !decodedOK || !ok {
			return nil, false
		}
	}
	kindValue := str.String(kind)
	actionValue := str.String(typedPayload.Action)
	refusalValue := str.String(typedPayload.Refusal)
	msg := safetyEventMsg{
		Kind:       kindValue.Trim(),
		Action:     actionValue.Trim(),
		Refusal:    refusalValue.Trim(),
		Blocked:    typedPayload.Blocked,
		Redacted:   typedPayload.Redacted,
		FindingIDs: getSafetyFindingIDsFromTypedPayload(typedPayload),
	}
	if msg.Kind == "" {
		return nil, false
	}

	return msg, true
}

func firstNonEmptyTUI(values ...string) string {
	for _, value := range values {
		valueText := str.String(value).Trim()
		if valueText != "" {
			return valueText
		}
	}

	return ""
}

func getSafetyFindingIDsFromTypedPayload(payload trace.SafetyEventPayload) []string {
	if len(payload.Findings) == 0 {
		return nil
	}

	ids := make([]string, 0, len(payload.Findings))
	for _, finding := range payload.Findings {
		findingValue := str.String(finding["id"])
		id := findingValue.Trim()
		if id != "" {
			ids = append(ids, id)
		}
	}

	return ids
}
