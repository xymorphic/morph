package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	agentsession "github.com/xymorphic/morph/pkg/agent/session"
)

func TestTimelineTypes_CarryMessagesAndTraceEvents(t *testing.T) {
	timeline := SessionTimeline{
		SessionID:          "session",
		Messages:           []SessionTimelineMessage{{Offset: 3, Message: morphmsg.Message{Role: morphmsg.RoleUser, Content: "hello"}}},
		TraceEvents:        []SessionTimelineTraceEvent{{Event: agentsession.TraceEvent{Sequence: 7, Type: "event"}}},
		FirstTraceSequence: 7,
		LastTraceSequence:  7,
	}

	require.Equal(t, "session", timeline.SessionID)
	require.Equal(t, 3, timeline.Messages[0].Offset)
	require.Equal(t, "hello", timeline.Messages[0].Message.Content)
	require.Equal(t, "event", timeline.TraceEvents[0].Event.Type)
	require.Equal(t, 7, timeline.FirstTraceSequence)
	require.Equal(t, 7, timeline.LastTraceSequence)
}
