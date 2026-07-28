package client

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	protomock "github.com/wandxy/morph/internal/mocks/proto"
	morphpb "github.com/wandxy/morph/internal/rpc/proto"
	agentsession "github.com/wandxy/morph/pkg/agent/session"
)

func TestSessionQueueClient_MapsSubmitStateAndObserve(t *testing.T) {
	streamEnabled := false
	stub := &protomock.MorphServiceClientStub{
		SubmitMessageResp: &morphpb.SubmitSessionMessageResponse{
			Entry: &morphpb.SessionQueueEntry{
				Id: "qmsg_test", SessionId: "default",
				Instruct:              "be concise",
				Stream:                &streamEnabled,
				RequestedDeliveryMode: string(agentsession.DeliveryModeSteering),
				DeliveryMode:          string(agentsession.DeliveryModeFollowUp),
				SteeringFallback:      string(agentsession.SteeringFallbackFollowUp),
			},
		},
		StateResp: &morphpb.GetSessionStateResponse{
			Id: "default", Cursor: 4, RetainedCursorFloor: 2, QueueDepth: 1,
			Progress: []*morphpb.SessionProgressEvent{{
				RunId:        "run_test",
				QueueEntryId: "qmsg_test",
				Kind:         "text_delta",
				Channel:      "assistant",
				Text:         "working",
				Sequence:     7,
				TraceEvent: &morphpb.SessionTimelineTraceEvent{
					Type:        "tool.invocation.started",
					PayloadJson: `{"id":"call_1","name":"read_file"}`,
				},
			}},
		},
		ObserveEvents: []*morphpb.ObserveSessionResponse{{
			Event: &morphpb.SessionEvent{
				SessionId: "default", Cursor: 5,
				Type:                 string(agentsession.EventTypeQueueUpdated),
				ProgressKind:         "text_delta",
				ProgressChannel:      "assistant",
				ProgressText:         "done",
				ProgressRunId:        "run_test",
				ProgressQueueEntryId: "qmsg_test",
				ProgressSequence:     8,
				ProgressTraceEvent: &morphpb.SessionTimelineTraceEvent{
					Type:        "tool.invocation.completed",
					PayloadJson: `{"tool_call_id":"call_1","name":"read_file"}`,
				},
			},
		}},
	}
	client := NewSessionService(stub)

	entry, err := client.SubmitMessage(context.Background(), SubmitMessageOptions{
		SessionID: "default", Message: "steer", ClientSubmissionID: "submission-1",
		DeliveryMode:     agentsession.DeliveryModeSteering,
		SteeringFallback: agentsession.SteeringFallbackFollowUp,
		Instruct:         "be concise",
		Stream:           &streamEnabled,
	})
	require.NoError(t, err)
	require.Equal(t, agentsession.DeliveryModeSteering, entry.RequestedDeliveryMode)
	require.Equal(t, "be concise", entry.Instruct)
	require.NotNil(t, entry.Stream)
	require.False(t, *entry.Stream)
	require.Equal(t, string(agentsession.DeliveryModeSteering), stub.SubmitMessageReq.GetDeliveryMode())
	require.Equal(t, "be concise", stub.SubmitMessageReq.GetInstruct())
	require.NotNil(t, stub.SubmitMessageReq.Stream)
	require.False(t, stub.SubmitMessageReq.GetStream())

	state, err := client.State(context.Background(), "default")
	require.NoError(t, err)
	require.Equal(t, int64(4), state.Cursor)
	require.Equal(t, int64(2), state.RetainedCursorFloor)
	require.Equal(t, []agentsession.ProgressEvent{{
		RunID:        "run_test",
		QueueEntryID: "qmsg_test",
		Kind:         "text_delta",
		Channel:      "assistant",
		Text:         "working",
		Sequence:     7,
		TraceEvent: &agentsession.TraceEvent{
			Type: "tool.invocation.started",
			Payload: map[string]any{
				"id":   "call_1",
				"name": "read_file",
			},
		},
	}}, state.Progress)

	var events []SessionEvent
	require.NoError(t, client.Observe(context.Background(), "default", state.Cursor, func(event SessionEvent) error {
		events = append(events, event)
		return nil
	}))
	require.Len(t, events, 1)
	require.Equal(t, int64(5), events[0].Cursor)
	require.Equal(t, &agentsession.ProgressEvent{
		RunID:        "run_test",
		QueueEntryID: "qmsg_test",
		Kind:         "text_delta",
		Channel:      "assistant",
		Text:         "done",
		Sequence:     8,
		TraceEvent: &agentsession.TraceEvent{
			Type: "tool.invocation.completed",
			Payload: map[string]any{
				"tool_call_id": "call_1",
				"name":         "read_file",
			},
		},
	}, events[0].Progress)
	require.Equal(t, int64(4), stub.ObserveReq.GetAfterCursor())
}

func TestSessionQueueClient_MapsMutationsAndInterrupt(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{
		EditQueuedResp: &morphpb.EditQueuedSessionMessageResponse{
			Entry: &morphpb.SessionQueueEntry{Id: "qmsg_test", Content: "edited"},
		},
		RemoveQueuedResp: &morphpb.RemoveQueuedSessionMessageResponse{
			Entry: &morphpb.SessionQueueEntry{Id: "qmsg_test", Status: "cancelled"},
		},
		PromoteQueuedResp: &morphpb.PromoteQueuedSessionMessageResponse{
			Entry: &morphpb.SessionQueueEntry{Id: "qmsg_test", Priority: 2},
		},
		InterruptRunResp: &morphpb.InterruptSessionRunResponse{
			Run: &morphpb.SessionActiveRun{Id: "run_test"}, Transitioned: true,
		},
	}
	client := NewSessionService(stub)

	edited, err := client.EditQueuedMessage(context.Background(), "default", "qmsg_test", "edited")
	require.NoError(t, err)
	require.Equal(t, "edited", edited.Content)
	_, err = client.RemoveQueuedMessage(context.Background(), "default", "qmsg_test")
	require.NoError(t, err)
	promoted, err := client.PromoteQueuedMessage(context.Background(), "default", "qmsg_test")
	require.NoError(t, err)
	require.Equal(t, int64(2), promoted.Priority)
	run, transitioned, err := client.InterruptRun(context.Background(), "default")
	require.NoError(t, err)
	require.True(t, transitioned)
	require.Equal(t, "run_test", run.ID)
}

func TestSessionQueueClient_PublicClientDelegatesQueueOperations(t *testing.T) {
	stub := &protomock.MorphServiceClientStub{
		SubmitMessageResp: &morphpb.SubmitSessionMessageResponse{
			Entry: &morphpb.SessionQueueEntry{Id: "qmsg_test"},
		},
		StateResp: &morphpb.GetSessionStateResponse{Id: "default"},
		EditQueuedResp: &morphpb.EditQueuedSessionMessageResponse{
			Entry: &morphpb.SessionQueueEntry{Id: "qmsg_test"},
		},
		RemoveQueuedResp: &morphpb.RemoveQueuedSessionMessageResponse{
			Entry: &morphpb.SessionQueueEntry{Id: "qmsg_test"},
		},
		PromoteQueuedResp: &morphpb.PromoteQueuedSessionMessageResponse{
			Entry: &morphpb.SessionQueueEntry{Id: "qmsg_test"},
		},
		InterruptRunResp: &morphpb.InterruptSessionRunResponse{},
	}
	client := &Client{Session: NewSessionService(stub)}

	_, err := client.SubmitMessage(context.Background(), SubmitMessageOptions{SessionID: "default"})
	require.NoError(t, err)
	_, err = client.State(context.Background(), "default")
	require.NoError(t, err)
	require.NoError(t, client.Observe(context.Background(), "default", 0, func(SessionEvent) error {
		return nil
	}))
	_, err = client.EditQueuedMessage(context.Background(), "default", "qmsg_test", "revised")
	require.NoError(t, err)
	_, err = client.RemoveQueuedMessage(context.Background(), "default", "qmsg_test")
	require.NoError(t, err)
	_, err = client.PromoteQueuedMessage(context.Background(), "default", "qmsg_test")
	require.NoError(t, err)
	_, _, err = client.InterruptRun(context.Background(), "default")
	require.NoError(t, err)

	var unavailable *Client
	_, err = unavailable.SubmitMessage(context.Background(), SubmitMessageOptions{})
	require.EqualError(t, err, "RPC client is unavailable")
	_, err = unavailable.State(context.Background(), "default")
	require.EqualError(t, err, "RPC client is unavailable")
	require.EqualError(
		t,
		unavailable.Observe(context.Background(), "default", 0, func(SessionEvent) error { return nil }),
		"RPC client is unavailable",
	)
	_, err = unavailable.EditQueuedMessage(context.Background(), "default", "qmsg_test", "revised")
	require.EqualError(t, err, "RPC client is unavailable")
	_, err = unavailable.RemoveQueuedMessage(context.Background(), "default", "qmsg_test")
	require.EqualError(t, err, "RPC client is unavailable")
	_, err = unavailable.PromoteQueuedMessage(context.Background(), "default", "qmsg_test")
	require.EqualError(t, err, "RPC client is unavailable")
	_, _, err = unavailable.InterruptRun(context.Background(), "default")
	require.EqualError(t, err, "RPC client is unavailable")
}
