package rpc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentstub "github.com/wandxy/morph/internal/mocks/agentstub"
	morphpb "github.com/wandxy/morph/internal/rpc/proto"
	"github.com/wandxy/morph/internal/trace"
	agentsession "github.com/wandxy/morph/pkg/agent/session"
)

type sessionObserveServerStub struct {
	grpc.ServerStream
	ctx       context.Context
	responses []*morphpb.ObserveSessionResponse
}

func (s *sessionObserveServerStub) Context() context.Context {
	return s.ctx
}

func (s *sessionObserveServerStub) Send(response *morphpb.ObserveSessionResponse) error {
	s.responses = append(s.responses, response)
	return nil
}

func TestSessionQueueService_SubmitStateObserveAndInterrupt(t *testing.T) {
	now := time.Now().UTC()
	entry := agentsession.QueueEntry{
		ID:                    "qmsg_test",
		SessionID:             "default",
		Content:               "change direction",
		ClientSubmissionID:    "submission-1",
		RequestedDeliveryMode: agentsession.DeliveryModeSteering,
		DeliveryMode:          agentsession.DeliveryModeFollowUp,
		SteeringFallback:      agentsession.SteeringFallbackFollowUp,
		Status:                agentsession.QueueStatusPending,
		CreatedAt:             now,
	}
	run := agentsession.ActiveRun{
		ID: "run_test", SessionID: "default", QueueEntryID: entry.ID,
		Status: agentsession.RunStatusInterrupted,
	}
	api := &agentstub.AgentServiceStub{
		QueueEntry: entry,
		ExecutionState: agentsession.ExecutionState{
			SessionID: "default", Queue: []agentsession.QueueEntry{entry},
			Cursor: 9, RetainedCursorFloor: 3, QueueDepth: 1,
			Progress: []agentsession.ProgressEvent{{
				RunID:        run.ID,
				QueueEntryID: entry.ID,
				Kind:         "text_delta",
				Channel:      "assistant",
				Text:         "working",
				Sequence:     7,
				TraceEvent: &agentsession.TraceEvent{
					Type: trace.EvtToolInvocationStarted,
					Payload: map[string]any{
						"id":    "call_1",
						"name":  "read_file",
						"input": `{"path":"secret.txt"}`,
					},
				},
			}},
		},
		SessionEvents: []agentsession.Event{{
			SessionID: "default", Type: agentsession.EventTypeRunInterrupted,
			Cursor: 10, Run: &run,
			Progress: &agentsession.ProgressEvent{
				RunID:        run.ID,
				QueueEntryID: entry.ID,
				Kind:         "text_delta",
				Channel:      "assistant",
				Text:         "done",
				Sequence:     8,
				TraceEvent: &agentsession.TraceEvent{
					Type: trace.EvtToolInvocationCompleted,
					Payload: map[string]any{
						"tool_call_id": "call_1",
						"name":         "read_file",
						"content":      "secret contents",
					},
				},
			},
		}},
		InterruptedRun:  run,
		RunTransitioned: true,
	}
	service := newAllowedService(api)
	streamEnabled := false

	submitted, err := service.SubmitMessage(context.Background(), &morphpb.SubmitSessionMessageRequest{
		Id: "default", Message: entry.Content, ClientSubmissionId: entry.ClientSubmissionID,
		DeliveryMode:     string(agentsession.DeliveryModeSteering),
		SteeringFallback: string(agentsession.SteeringFallbackFollowUp),
		Instruct:         "be concise",
		Stream:           &streamEnabled,
	})
	require.NoError(t, err)
	require.Equal(t, string(agentsession.DeliveryModeSteering), submitted.GetEntry().GetRequestedDeliveryMode())
	require.Equal(t, agentsession.DeliveryModeSteering, api.SubmittedMessage.DeliveryMode)
	require.Equal(t, "be concise", api.SubmittedMessage.Instruct)
	require.NotNil(t, api.SubmittedMessage.Stream)
	require.False(t, *api.SubmittedMessage.Stream)

	state, err := service.State(context.Background(), &morphpb.GetSessionStateRequest{Id: "default"})
	require.NoError(t, err)
	require.Equal(t, int64(9), state.GetCursor())
	require.Equal(t, int64(3), state.GetRetainedCursorFloor())
	require.Equal(t, int32(1), state.GetQueueDepth())
	require.Len(t, state.GetProgress(), 1)
	require.Equal(t, run.ID, state.GetProgress()[0].GetRunId())
	require.Equal(t, entry.ID, state.GetProgress()[0].GetQueueEntryId())
	require.Equal(t, int64(7), state.GetProgress()[0].GetSequence())
	require.Equal(t, trace.EvtToolInvocationStarted, state.GetProgress()[0].GetTraceEvent().GetType())
	require.JSONEq(
		t,
		`{"detail":"read_file secret.txt","id":"call_1","name":"read_file"}`,
		state.GetProgress()[0].GetTraceEvent().GetPayloadJson(),
	)

	stream := &sessionObserveServerStub{ctx: context.Background()}
	require.NoError(t, service.Observe(
		&morphpb.ObserveSessionRequest{Id: "default", AfterCursor: 9},
		stream,
	))
	require.Len(t, stream.responses, 1)
	require.Equal(t, int64(10), stream.responses[0].GetEvent().GetCursor())
	require.Equal(t, run.ID, stream.responses[0].GetEvent().GetProgressRunId())
	require.Equal(t, entry.ID, stream.responses[0].GetEvent().GetProgressQueueEntryId())
	require.Equal(t, int64(8), stream.responses[0].GetEvent().GetProgressSequence())
	require.Equal(
		t,
		trace.EvtToolInvocationCompleted,
		stream.responses[0].GetEvent().GetProgressTraceEvent().GetType(),
	)
	require.NotContains(
		t,
		stream.responses[0].GetEvent().GetProgressTraceEvent().GetPayloadJson(),
		"secret contents",
	)

	interrupted, err := service.InterruptRun(
		context.Background(),
		&morphpb.InterruptSessionRunRequest{Id: "default"},
	)
	require.NoError(t, err)
	require.True(t, interrupted.GetTransitioned())
	require.Equal(t, agentsession.RunStatusInterrupted, agentsession.RunStatus(interrupted.GetRun().GetStatus()))
}

func TestSessionQueueService_MapsQueueMutations(t *testing.T) {
	api := &agentstub.AgentServiceStub{
		QueueEntry: agentsession.QueueEntry{
			ID: "qmsg_test", SessionID: "default",
			Status: agentsession.QueueStatusPending,
		},
	}
	service := newAllowedService(api)

	edited, err := service.EditQueuedMessage(
		context.Background(),
		&morphpb.EditQueuedSessionMessageRequest{
			Id: "default", EntryId: "qmsg_test", Message: "revised",
		},
	)
	require.NoError(t, err)
	require.Equal(t, "qmsg_test", edited.GetEntry().GetId())
	require.Equal(t, "qmsg_test", api.EditedEntryID)
	require.Equal(t, "revised", api.EditedEntryContent)

	removed, err := service.RemoveQueuedMessage(
		context.Background(),
		&morphpb.RemoveQueuedSessionMessageRequest{Id: "default", EntryId: "qmsg_test"},
	)
	require.NoError(t, err)
	require.Equal(t, "qmsg_test", removed.GetEntry().GetId())
	require.Equal(t, "qmsg_test", api.RemovedEntryID)

	promoted, err := service.PromoteQueuedMessage(
		context.Background(),
		&morphpb.PromoteQueuedSessionMessageRequest{Id: "default", EntryId: "qmsg_test"},
	)
	require.NoError(t, err)
	require.Equal(t, "qmsg_test", promoted.GetEntry().GetId())
	require.Equal(t, "qmsg_test", api.PromotedEntryID)

	steered, err := service.SteerQueuedMessage(
		context.Background(),
		&morphpb.SteerQueuedSessionMessageRequest{Id: "default", EntryId: "qmsg_test"},
	)
	require.NoError(t, err)
	require.Equal(t, "qmsg_test", steered.GetEntry().GetId())
	require.Equal(t, "qmsg_test", api.SteeredEntryID)
}

func TestSessionQueueService_SteerQueuedMessageRejectsNilAndMapsDomainErrors(t *testing.T) {
	response, err := newAllowedService(&agentstub.AgentServiceStub{}).
		SteerQueuedMessage(context.Background(), nil)
	require.Nil(t, response)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	response, err = newAllowedService(&agentstub.AgentServiceStub{
		Err: agentsession.ErrSteeringRequiresRun,
	}).SteerQueuedMessage(
		context.Background(),
		&morphpb.SteerQueuedSessionMessageRequest{
			Id:      "default",
			EntryId: "qmsg_test",
		},
	)
	require.Nil(t, response)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.ErrorContains(t, err, agentsession.ErrSteeringRequiresRun.Error())
}

func TestSessionQueueService_RejectsExpiredObserveCursor(t *testing.T) {
	service := newAllowedService(&agentstub.AgentServiceStub{Err: agentsession.ErrCursorExpired})
	err := service.Observe(
		&morphpb.ObserveSessionRequest{Id: "default"},
		&sessionObserveServerStub{ctx: context.Background()},
	)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestSessionQueueService_RejectsExpiredProgressHistory(t *testing.T) {
	service := newAllowedService(&agentstub.AgentServiceStub{Err: agentsession.ErrProgressExpired})
	err := service.Observe(
		&morphpb.ObserveSessionRequest{Id: "default"},
		&sessionObserveServerStub{ctx: context.Background()},
	)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestSessionQueueService_MapsQueuePreconditions(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "future cursor", err: agentsession.ErrCursorBeyondSession},
		{name: "steering without run", err: agentsession.ErrSteeringRequiresRun},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := newAllowedService(&agentstub.AgentServiceStub{Err: test.err})
			_, err := service.SubmitMessage(
				context.Background(),
				&morphpb.SubmitSessionMessageRequest{Id: "default", Message: "test"},
			)
			require.Equal(t, codes.FailedPrecondition, status.Code(err))
		})
	}
}

func TestSessionQueueProtocolHasNoMorphService(t *testing.T) {
	require.Nil(t,
		morphpb.File_internal_rpc_proto_morph_proto.Services().ByName("MorphService"))
}
