package agent

import (
	"time"

	"github.com/xymorphic/morph/internal/guardrails"
	"github.com/xymorphic/morph/internal/trace"
	"github.com/xymorphic/morph/pkg/str"
)

// fanoutTraceSession writes trace records to a primary session and mirrors
// selected sanitized events to a live callback.
type fanoutTraceSession struct {
	sessionID string
	primary   trace.Session
	redactor  guardrails.Redactor
	onEvent   func(trace.Event)
}

// newFanoutTraceSession creates a trace session that writes to a primary session
// and mirrors selected sanitized events to a live callback.
func newFanoutTraceSession(
	primary trace.Session,
	sessionID string,
	onEvent func(trace.Event),
) trace.Session {
	if onEvent == nil {
		if primary == nil {
			return trace.NoopSession()
		}

		return primary
	}

	if primary == nil {
		primary = trace.NoopSession()
	}
	iDValue := str.String(primary.ID())
	if value := iDValue.Trim(); value != "" {
		sessionID = value
	}
	sessionIDValue := str.String(sessionID)
	return fanoutTraceSession{
		sessionID: sessionIDValue.Trim(),
		primary:   primary,
		redactor:  guardrails.NewRedactor(),
		onEvent:   onEvent,
	}
}

// ID returns the primary trace session ID when available, otherwise the fallback session ID.
func (s fanoutTraceSession) ID() string {
	if s.primary != nil {
		iDValue2 := str.String(s.primary.ID())
		if id := iDValue2.Trim(); id != "" {
			return id
		}
	}

	return s.sessionID
}

// Record writes all events to the primary trace and streams selected redacted events live.
func (s fanoutTraceSession) Record(eventType string, payload any) {
	if s.primary != nil {
		s.primary.Record(eventType, payload)
	}
	if s.onEvent == nil || !isStreamableTraceEvent(eventType) {
		return
	}
	eventTypeValue :=

		// Live trace payloads may be rendered immediately in the TUI, so sanitize
		// before they leave the trace subsystem.

		str.String(eventType)
	event := trace.Event{
		SessionID: s.ID(),
		Type:      eventTypeValue.Trim(),
		Timestamp: time.Now().UTC(),
	}
	if payload != nil {
		event.Payload = s.redactor.Sanitize(payload)
	}

	s.onEvent(event)
}

// Close closes the primary trace session.
func (s fanoutTraceSession) Close() {
	if s.primary != nil {
		s.primary.Close()
	}
}

// isStreamableTraceEvent whitelists trace events that are useful during a live response.
func isStreamableTraceEvent(eventType string) bool {
	eventTypeValue2 := str.String(eventType)
	switch eventTypeValue2.Trim() {
	case trace.EvtUserMessageAccepted,
		trace.EvtToolInvocationStarted,
		trace.EvtToolInvocationCompleted,
		trace.EvtPermissionApprovalChanged,
		trace.EvtInputSafetyBlocked,
		trace.EvtOutputSafetyApplied,
		trace.EvtToolOutputSafetyApplied,
		trace.EvtSessionFailed,
		trace.EvtPlanHydrated,
		trace.EvtContextCompactionPending,
		trace.EvtContextCompactionRunning,
		trace.EvtContextCompactionSucceeded,
		trace.EvtContextCompactionFailed,
		trace.EvtFinalAssistantResponse:
		return true
	default:
		return false
	}
}
