package agent

import (
	"github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/agent/session"
)

// SessionTimelineOptions configures a transcript and trace event page load.
type SessionTimelineOptions struct {
	SessionID     string
	MessageOffset int
	MessageLimit  int
	TraceOffset   int
	TraceLimit    int
}

// SessionTimeline carries session transcript and trace records for UI hydration.
type SessionTimeline struct {
	SessionID             string
	Title                 string
	TitleSource           string
	Messages              []SessionTimelineMessage
	TraceEvents           []SessionTimelineTraceEvent
	MessagesHasMore       bool
	TracesHasMore         bool
	TracesTruncatedBefore bool
	FirstTraceSequence    int
	LastTraceSequence     int
}

// SessionTimelineMessage records a persisted message and its absolute offset.
type SessionTimelineMessage struct {
	Message message.Message
	Offset  int
}

// SessionTimelineTraceEvent records a persisted trace event for the timeline.
type SessionTimelineTraceEvent struct {
	Event session.TraceEvent
}
