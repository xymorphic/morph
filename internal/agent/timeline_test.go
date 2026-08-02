package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	storage "github.com/xymorphic/morph/internal/state/core"
	statemanager "github.com/xymorphic/morph/internal/state/manager"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	agentsession "github.com/xymorphic/morph/pkg/agent/session"
)

func TestTimelineHelpersValidateLimitAndConvertTrace(t *testing.T) {
	require.EqualError(t, checkTimelineOptions(SessionTimelineOptions{MessageOffset: -1}), "message offset must be greater than or equal to zero")
	require.EqualError(t, checkTimelineOptions(SessionTimelineOptions{MessageLimit: -1}), "message limit must be greater than or equal to zero")
	require.EqualError(t, checkTimelineOptions(SessionTimelineOptions{TraceOffset: -1}), "trace offset must be greater than or equal to zero")
	require.EqualError(t, checkTimelineOptions(SessionTimelineOptions{TraceLimit: -1}), "trace limit must be greater than or equal to zero")
	require.NoError(t, checkTimelineOptions(SessionTimelineOptions{}))
	require.Equal(t, defaultSessionTimelineLimit, getTimelineLimit(0))
	require.Equal(t, maxSessionTimelineLimit, getTimelineLimit(maxSessionTimelineLimit+1))
	require.Equal(t, 5, getTimelineLimit(5))

	events := []storage.TraceEvent{{Sequence: 1}, {Sequence: 2}}
	reverseTraceEvents(events)
	require.Equal(t, []storage.TraceEvent{{Sequence: 2}, {Sequence: 1}}, events)

	traceEvents := []SessionTimelineTraceEvent{
		{Event: agentsession.TraceEvent{Sequence: 7}},
		{Event: agentsession.TraceEvent{Sequence: 9}},
	}
	require.Equal(t, 7, getFirstTraceSequence(traceEvents))
	require.Equal(t, 9, getLastTraceSequence(traceEvents))
	require.Zero(t, getFirstTraceSequence(nil))
	require.Zero(t, getLastTraceSequence(nil))

	now := time.Now().UTC()
	converted := timelineTraceEventFromStorageTraceEvent(storage.TraceEvent{
		ID:        3,
		SessionID: "session-1",
		Sequence:  4,
		Type:      "event",
		Timestamp: now,
		Payload:   map[string]any{"ok": true},
	})
	require.Equal(t, agentsession.TraceEvent{
		ID:        3,
		SessionID: "session-1",
		Sequence:  4,
		Type:      "event",
		Timestamp: now,
		Payload:   map[string]any{"ok": true},
	}, converted)
}

func TestAgent_TimelineLoadsRecentTailAndTraceFlags(t *testing.T) {
	store := &stateStoreStub{
		session: storage.Session{ID: storage.DefaultSessionID, Title: "Title"},
		messages: []morphmsg.Message{
			{Role: morphmsg.RoleUser, Content: "one"},
			{Role: morphmsg.RoleAssistant, Content: "two"},
			{Role: morphmsg.RoleUser, Content: "three"},
		},
		traceEvents: []storage.TraceEvent{
			{ID: 1, SessionID: storage.DefaultSessionID, Sequence: 2, Type: "two"},
			{ID: 2, SessionID: storage.DefaultSessionID, Sequence: 3, Type: "three"},
			{ID: 3, SessionID: storage.DefaultSessionID, Sequence: 4, Type: "four"},
		},
	}
	manager, err := statemanager.NewManager(store, time.Hour, time.Hour)
	require.NoError(t, err)
	core := &Agent{stateMgr: manager}

	timeline, err := core.GetSessionTimeline(context.Background(), SessionTimelineOptions{
		MessageLimit: 2,
		TraceLimit:   2,
	})

	require.NoError(t, err)
	require.Equal(t, storage.DefaultSessionID, timeline.SessionID)
	require.Equal(t, "Title", timeline.Title)
	require.True(t, timeline.MessagesHasMore)
	require.Equal(t, []int{0, 1}, []int{timeline.Messages[0].Offset, timeline.Messages[1].Offset})
	require.True(t, timeline.TracesHasMore)
	require.True(t, timeline.TracesTruncatedBefore)
	require.Equal(t, 2, timeline.FirstTraceSequence)
	require.Equal(t, 3, timeline.LastTraceSequence)
}

func TestAgent_TimelineHandlesUnsupportedTraceStoreAndErrors(t *testing.T) {
	_, err := (*Agent)(nil).GetSessionTimeline(context.Background(), SessionTimelineOptions{})
	require.EqualError(t, err, "agent is required")
	_, err = (&Agent{}).GetSessionTimeline(context.Background(), SessionTimelineOptions{})
	require.EqualError(t, err, "state manager is required")

	store := &stateStoreStub{
		session:  storage.Session{ID: storage.DefaultSessionID},
		messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "one"}},
		traceErr: storage.ErrTraceStoreUnsupported,
	}
	manager, err := statemanager.NewManager(store, time.Hour, time.Hour)
	require.NoError(t, err)
	core := &Agent{stateMgr: manager}

	timeline, err := core.GetSessionTimeline(context.Background(), SessionTimelineOptions{})
	require.NoError(t, err)
	require.Empty(t, timeline.TraceEvents)

	store.traceErr = errors.New("trace failed")
	_, err = core.GetSessionTimeline(context.Background(), SessionTimelineOptions{})
	require.EqualError(t, err, "trace failed")
}

func TestAgent_TimelinePropagatesResolveMessageAndTruncationErrors(t *testing.T) {
	expected := errors.New("failed")
	store := &stateStoreStub{
		session:  storage.Session{ID: storage.DefaultSessionID},
		countErr: expected,
	}
	manager, err := statemanager.NewManager(store, time.Hour, time.Hour)
	require.NoError(t, err)
	core := &Agent{stateMgr: manager}

	_, err = core.GetSessionTimeline(context.Background(), SessionTimelineOptions{})
	require.ErrorIs(t, err, expected)

	store.countErr = nil
	store.messagesErr = expected
	_, err = core.GetSessionTimeline(context.Background(), SessionTimelineOptions{MessageLimit: 1})
	require.ErrorIs(t, err, expected)

	store.messagesErr = nil
	store.traceErr = expected
	_, err = core.GetSessionTimeline(context.Background(), SessionTimelineOptions{TraceOffset: 3})
	require.ErrorIs(t, err, expected)

	store.traceErr = nil
	store.getErr = expected
	_, err = core.GetSessionTimeline(context.Background(), SessionTimelineOptions{})
	require.ErrorIs(t, err, expected)
}

func TestAgent_TimelineDefaultTailAndValidationBranches(t *testing.T) {
	messages := make([]morphmsg.Message, 0, defaultSessionTimelineLimit+1)
	for i := 0; i < defaultSessionTimelineLimit+1; i++ {
		messages = append(messages, morphmsg.Message{Role: morphmsg.RoleUser, Content: "message"})
	}
	store := &stateStoreStub{
		session:  storage.Session{ID: storage.DefaultSessionID},
		messages: messages,
		traceEvents: []storage.TraceEvent{
			{SessionID: storage.DefaultSessionID, Sequence: 1},
			{SessionID: storage.DefaultSessionID, Sequence: 2},
			{SessionID: storage.DefaultSessionID, Sequence: 3},
		},
	}
	manager, err := statemanager.NewManager(store, time.Hour, time.Hour)
	require.NoError(t, err)
	core := &Agent{stateMgr: manager}

	_, err = core.GetSessionTimeline(context.Background(), SessionTimelineOptions{MessageOffset: -1})
	require.EqualError(t, err, "message offset must be greater than or equal to zero")

	timeline, err := core.GetSessionTimeline(context.Background(), SessionTimelineOptions{TraceLimit: 2})
	require.NoError(t, err)
	require.True(t, timeline.MessagesHasMore)
	require.Equal(t, 1, timeline.Messages[0].Offset)
	require.True(t, timeline.TracesHasMore)
	require.Equal(t, 1, timeline.FirstTraceSequence)
	require.Equal(t, 2, timeline.LastTraceSequence)

	traceEvents := make([]storage.TraceEvent, 0, defaultSessionTimelineLimit+1)
	for i := 1; i <= defaultSessionTimelineLimit+1; i++ {
		traceEvents = append(traceEvents, storage.TraceEvent{SessionID: storage.DefaultSessionID, Sequence: i})
	}
	store.traceEvents = traceEvents
	timeline, err = core.GetSessionTimeline(context.Background(), SessionTimelineOptions{})
	require.NoError(t, err)
	require.True(t, timeline.TracesHasMore)
	require.Equal(t, 2, timeline.FirstTraceSequence)
	require.Equal(t, defaultSessionTimelineLimit+1, timeline.LastTraceSequence)

	store.traceCalls = 0
	store.traceErrAt = 2
	store.traceErr = errors.New("truncation failed")
	_, err = core.GetSessionTimeline(context.Background(), SessionTimelineOptions{TraceOffset: defaultSessionTimelineLimit + 10})
	require.EqualError(t, err, "truncation failed")
}

func TestAgent_HasTruncatedTraceHistoryBranches(t *testing.T) {
	store := &stateStoreStub{
		session:     storage.Session{ID: storage.DefaultSessionID},
		traceEvents: []storage.TraceEvent{{SessionID: storage.DefaultSessionID, Sequence: 1}},
	}
	manager, err := statemanager.NewManager(store, time.Hour, time.Hour)
	require.NoError(t, err)
	core := &Agent{stateMgr: manager}

	truncated, err := core.hasTruncatedTraceHistory(
		context.Background(),
		storage.DefaultSessionID,
		0,
		[]storage.TraceEvent{{Sequence: 2}},
	)
	require.NoError(t, err)
	require.True(t, truncated)

	truncated, err = core.hasTruncatedTraceHistory(context.Background(), storage.DefaultSessionID, 3, nil)
	require.NoError(t, err)
	require.False(t, truncated)

	store.traceEvents = nil
	truncated, err = core.hasTruncatedTraceHistory(context.Background(), storage.DefaultSessionID, 3, nil)
	require.NoError(t, err)
	require.False(t, truncated)

	store.traceErr = storage.ErrTraceStoreUnsupported
	truncated, err = core.hasTruncatedTraceHistory(context.Background(), storage.DefaultSessionID, 3, nil)
	require.NoError(t, err)
	require.False(t, truncated)

	store.traceErr = errors.New("trace failed")
	_, err = core.hasTruncatedTraceHistory(context.Background(), storage.DefaultSessionID, 3, nil)
	require.EqualError(t, err, "trace failed")
}
