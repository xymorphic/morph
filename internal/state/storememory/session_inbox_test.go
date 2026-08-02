package storememory

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	agentsession "github.com/xymorphic/morph/pkg/agent/session"
	"github.com/xymorphic/morph/pkg/nanoid"
)

func TestMemoryStore_EnqueueMessageIsIdempotentAndPublishesEvents(t *testing.T) {
	store := NewStore()
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))

	req := agentsession.EnqueueRequest{
		ID:                 memoryInboxQueueID("first"),
		SessionID:          testSessionA,
		Content:            "first follow-up",
		ClientSubmissionID: "client-1",
		DeliveryMode:       agentsession.DeliveryModeFollowUp,
		SteeringFallback:   agentsession.SteeringFallbackFollowUp,
		Provenance: agentsession.Provenance{
			ActorKind: "local_owner",
			Surface:   "tui",
			Profile:   "default",
		},
	}
	first, err := store.EnqueueMessage(context.Background(), req)
	require.NoError(t, err)
	retry, err := store.EnqueueMessage(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, first, retry)

	conflict := req
	conflict.Content = "different"
	_, err = store.EnqueueMessage(context.Background(), conflict)
	require.EqualError(
		t,
		err,
		"client submission id is already used by a different message",
	)

	state, err := store.GetExecutionState(context.Background(), testSessionA)
	require.NoError(t, err)
	require.Equal(t, int64(1), state.Cursor)
	require.Equal(t, int64(1), state.RetainedCursorFloor)
	require.Equal(t, 1, state.QueueDepth)
	require.Len(t, state.Queue, 1)
	require.Equal(t, req.Provenance, state.Queue[0].Provenance)

	batch, err := store.ListEvents(context.Background(), testSessionA, 0, 10)
	require.NoError(t, err)
	require.Equal(t, state.Cursor, batch.Cursor)
	require.Len(t, batch.Events, 1)
	require.Equal(t, agentsession.EventTypeQueueEnqueued, batch.Events[0].Type)
	require.Equal(t, first.ID, batch.Events[0].Queue.ID)

	batch.Events[0].Queue.Content = "mutated"
	reloaded, err := store.GetExecutionState(context.Background(), testSessionA)
	require.NoError(t, err)
	require.Equal(t, "first follow-up", reloaded.Queue[0].Content)

	_, err = store.ListEvents(context.Background(), testSessionA, 2, 10)
	require.ErrorIs(t, err, agentsession.ErrCursorBeyondSession)
}

func TestMemoryStore_EnqueueMessageNormalizesDefaultsAndClonesStream(t *testing.T) {
	store := NewStore()
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	stream := true

	entry, err := store.EnqueueMessage(context.Background(), agentsession.EnqueueRequest{
		ID:                 " " + memoryInboxQueueID("defaults") + " ",
		SessionID:          " " + testSessionA + " ",
		Content:            " hello ",
		Instruct:           " concise ",
		Stream:             &stream,
		ClientSubmissionID: " client-defaults ",
		Provenance: agentsession.Provenance{
			ActorKind:   " local_owner ",
			ActorID:     " actor ",
			SurfaceKind: " terminal ",
			Surface:     " tui ",
			Profile:     " default ",
		},
	})
	require.NoError(t, err)
	require.Equal(t, agentsession.DeliveryModeFollowUp, entry.DeliveryMode)
	require.Equal(t, agentsession.SteeringFallbackFollowUp, entry.SteeringFallback)
	require.Equal(t, "hello", entry.Content)
	require.Equal(t, "concise", entry.Instruct)
	require.Equal(t, "local_owner", entry.Provenance.ActorKind)
	require.Equal(t, "actor", entry.Provenance.ActorID)
	require.Equal(t, "terminal", entry.Provenance.SurfaceKind)
	require.Equal(t, "tui", entry.Provenance.Surface)
	require.Equal(t, "default", entry.Provenance.Profile)
	require.NotSame(t, &stream, entry.Stream)
	require.True(t, *entry.Stream)

	stream = false
	state, err := store.GetExecutionState(context.Background(), testSessionA)
	require.NoError(t, err)
	require.True(t, *state.Queue[0].Stream)

	retryStream := true
	retry, err := store.EnqueueMessage(context.Background(), agentsession.EnqueueRequest{
		ID:                 memoryInboxQueueID("different-retry-id"),
		SessionID:          testSessionA,
		Content:            "hello",
		Instruct:           "concise",
		Stream:             &retryStream,
		ClientSubmissionID: "client-defaults",
	})
	require.NoError(t, err)
	require.Equal(t, entry.ID, retry.ID)

	retryStream = false
	_, err = store.EnqueueMessage(context.Background(), agentsession.EnqueueRequest{
		ID:                 memoryInboxQueueID("different-retry-id"),
		SessionID:          testSessionA,
		Content:            "hello",
		Instruct:           "concise",
		Stream:             &retryStream,
		ClientSubmissionID: "client-defaults",
	})
	require.EqualError(
		t,
		err,
		"client submission id is already used by a different message",
	)
}

func TestMemoryStore_EnqueueMessageValidatesRequest(t *testing.T) {
	valid := agentsession.EnqueueRequest{
		ID:                 memoryInboxQueueID("valid"),
		SessionID:          testSessionA,
		Content:            "message",
		ClientSubmissionID: "client-valid",
		DeliveryMode:       agentsession.DeliveryModeFollowUp,
		SteeringFallback:   agentsession.SteeringFallbackFollowUp,
	}
	tests := []struct {
		name      string
		mutate    func(*agentsession.EnqueueRequest)
		wantError string
	}{
		{
			name: "missing queue id",
			mutate: func(req *agentsession.EnqueueRequest) {
				req.ID = " "
			},
			wantError: "queue entry id is required",
		},
		{
			name: "invalid session id",
			mutate: func(req *agentsession.EnqueueRequest) {
				req.SessionID = "invalid"
			},
			wantError: "session id must be a valid ses_ nanoid",
		},
		{
			name: "missing content",
			mutate: func(req *agentsession.EnqueueRequest) {
				req.Content = " "
			},
			wantError: "message is required",
		},
		{
			name: "missing client submission id",
			mutate: func(req *agentsession.EnqueueRequest) {
				req.ClientSubmissionID = " "
			},
			wantError: "client submission id is required",
		},
		{
			name: "invalid delivery mode",
			mutate: func(req *agentsession.EnqueueRequest) {
				req.DeliveryMode = "later"
			},
			wantError: "delivery mode is invalid",
		},
		{
			name: "invalid steering fallback",
			mutate: func(req *agentsession.EnqueueRequest) {
				req.SteeringFallback = "maybe"
			},
			wantError: "steering fallback is invalid",
		},
	}

	store := NewStore()
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := valid
			test.mutate(&req)
			_, err := store.EnqueueMessage(context.Background(), req)
			require.EqualError(t, err, test.wantError)
		})
	}

	_, err := store.EnqueueMessage(context.Background(), valid)
	require.NoError(t, err)
	duplicateID := valid
	duplicateID.ClientSubmissionID = "different-client"
	_, err = store.EnqueueMessage(context.Background(), duplicateID)
	require.EqualError(t, err, "queue entry id is already used")

	missingSession := valid
	missingSession.ID = memoryInboxQueueID("missing-session")
	missingSession.SessionID = testSessionB
	missingSession.ClientSubmissionID = "missing-session"
	_, err = store.EnqueueMessage(context.Background(), missingSession)
	require.EqualError(t, err, "session not found")

	now := time.Now().UTC()
	require.NoError(t, store.Save(context.Background(), Session{
		ID:         testSessionA,
		Archived:   true,
		ArchivedAt: now,
		ExpiresAt:  now.Add(time.Hour),
	}))
	_, err = store.EnqueueMessage(context.Background(), valid)
	require.EqualError(t, err, "session not found")
}

func TestMemoryStore_ClaimsFollowUpsOneAtATimeAndHonorsPromotion(t *testing.T) {
	store := NewStore()
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	activateMemoryInboxRunner(t, store, "generation-1")

	first := submitMemoryInboxMessage(
		t,
		store,
		"first",
		"client-1",
		agentsession.DeliveryModeFollowUp,
		agentsession.SteeringFallbackFollowUp,
	)
	second := submitMemoryInboxMessage(
		t,
		store,
		"second",
		"client-2",
		agentsession.DeliveryModeFollowUp,
		agentsession.SteeringFallbackFollowUp,
	)
	_, err := store.PromoteQueueEntry(
		context.Background(),
		agentsession.QueueMutationRequest{
			SessionID: testSessionA,
			EntryID:   second.ID,
		},
	)
	require.NoError(t, err)

	entry, run, claimed, err := store.ClaimNextFollowUp(
		context.Background(),
		agentsession.ClaimRequest{
			SessionID:  testSessionA,
			RunID:      memoryInboxRunID("second"),
			Generation: "generation-1",
		},
	)
	require.NoError(t, err)
	require.True(t, claimed)
	require.Equal(t, second.ID, entry.ID)

	state, err := store.GetExecutionState(context.Background(), testSessionA)
	require.NoError(t, err)
	require.NotNil(t, state.ActiveRun)
	require.Equal(t, run.ID, state.ActiveRun.ID)
	batch, err := store.ListEvents(context.Background(), testSessionA, 1, 1)
	require.NoError(t, err)
	require.Len(t, batch.Events, 1)
	require.Equal(t, int64(2), batch.Events[0].Cursor)

	runnable, err := store.ListRunnableSessions(context.Background())
	require.NoError(t, err)
	require.Empty(t, runnable)

	_, _, claimed, err = store.ClaimNextFollowUp(
		context.Background(),
		agentsession.ClaimRequest{
			SessionID:  testSessionA,
			RunID:      memoryInboxRunID("blocked"),
			Generation: "generation-1",
		},
	)
	require.NoError(t, err)
	require.False(t, claimed)

	finished, transitioned, err := store.FinishSessionRun(
		context.Background(),
		agentsession.RunFinishRequest{
			SessionID:  testSessionA,
			RunID:      run.ID,
			Generation: run.Generation,
			Status:     agentsession.RunStatusCompleted,
		},
	)
	require.NoError(t, err)
	require.True(t, transitioned)
	require.Equal(t, agentsession.RunStatusCompleted, finished.Status)

	_, _, claimed, err = store.ClaimNextFollowUp(
		context.Background(),
		agentsession.ClaimRequest{
			SessionID:  testSessionA,
			RunID:      run.ID,
			Generation: "generation-1",
		},
	)
	require.EqualError(t, err, "run id is already used")
	require.False(t, claimed)

	entry, _, claimed, err = store.ClaimNextFollowUp(
		context.Background(),
		agentsession.ClaimRequest{
			SessionID:  testSessionA,
			RunID:      memoryInboxRunID("first"),
			Generation: "generation-1",
		},
	)
	require.NoError(t, err)
	require.True(t, claimed)
	require.Equal(t, first.ID, entry.ID)
}

func TestMemoryStore_MutatesOnlyPendingQueueEntries(t *testing.T) {
	store := NewStore()
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))

	editedEntry := submitMemoryInboxMessage(
		t,
		store,
		"draft",
		"client-edit",
		agentsession.DeliveryModeFollowUp,
		agentsession.SteeringFallbackFollowUp,
	)
	cancelledEntry := submitMemoryInboxMessage(
		t,
		store,
		"cancel",
		"client-cancel",
		agentsession.DeliveryModeFollowUp,
		agentsession.SteeringFallbackFollowUp,
	)

	edited, err := store.EditQueueEntry(
		context.Background(),
		agentsession.QueueEditRequest{
			SessionID: testSessionA,
			EntryID:   editedEntry.ID,
			Content:   " revised ",
		},
	)
	require.NoError(t, err)
	require.Equal(t, "revised", edited.Content)

	cancelled, err := store.CancelQueueEntry(
		context.Background(),
		agentsession.QueueMutationRequest{
			SessionID: testSessionA,
			EntryID:   cancelledEntry.ID,
		},
	)
	require.NoError(t, err)
	require.Equal(t, agentsession.QueueStatusCancelled, cancelled.Status)
	require.False(t, cancelled.CompletedAt.IsZero())

	_, err = store.EditQueueEntry(
		context.Background(),
		agentsession.QueueEditRequest{
			SessionID: testSessionA,
			EntryID:   cancelledEntry.ID,
			Content:   "too late",
		},
	)
	require.EqualError(t, err, "pending queue entry not found")
	_, err = store.EditQueueEntry(
		context.Background(),
		agentsession.QueueEditRequest{
			SessionID: testSessionA,
			EntryID:   editedEntry.ID,
			Content:   " ",
		},
	)
	require.EqualError(t, err, "message is required")

	state, err := store.GetExecutionState(context.Background(), testSessionA)
	require.NoError(t, err)
	require.Equal(t, 1, state.QueueDepth)
	require.Equal(t, "revised", getMemoryInboxEntry(t, state.Queue, editedEntry.ID).Content)
	require.Equal(
		t,
		agentsession.QueueStatusCancelled,
		getMemoryInboxEntry(t, state.Queue, cancelledEntry.ID).Status,
	)
}

func TestMemoryStore_SteersPendingFollowUpIntoActiveRun(t *testing.T) {
	store := NewStore()
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	activateMemoryInboxRunner(t, store, "generation-1")

	submitMemoryInboxMessage(
		t,
		store,
		"start",
		"client-start",
		agentsession.DeliveryModeFollowUp,
		agentsession.SteeringFallbackFollowUp,
	)
	queued := submitMemoryInboxMessage(
		t,
		store,
		"change direction",
		"client-queued",
		agentsession.DeliveryModeFollowUp,
		agentsession.SteeringFallbackFollowUp,
	)
	_, run, claimed, err := store.ClaimNextFollowUp(
		context.Background(),
		agentsession.ClaimRequest{
			SessionID:  testSessionA,
			RunID:      memoryInboxRunID("active"),
			Generation: "generation-1",
		},
	)
	require.NoError(t, err)
	require.True(t, claimed)

	steering, err := store.SteerQueueEntry(
		context.Background(),
		agentsession.QueueMutationRequest{
			SessionID: testSessionA,
			EntryID:   queued.ID,
		},
	)

	require.NoError(t, err)
	require.Equal(t, queued.ID, steering.ID)
	require.Equal(t, agentsession.DeliveryModeSteering, steering.RequestedDeliveryMode)
	require.Equal(t, agentsession.DeliveryModeSteering, steering.DeliveryMode)
	require.Equal(t, agentsession.SteeringFallbackFollowUp, steering.SteeringFallback)
	require.Equal(t, run.ID, steering.TargetRunID)
	pending, err := store.HasPendingSteering(
		context.Background(),
		agentsession.SteeringClaimRequest{
			SessionID:  testSessionA,
			RunID:      run.ID,
			Generation: "generation-1",
		},
	)
	require.NoError(t, err)
	require.True(t, pending)

	delivered, err := store.ClaimSteering(
		context.Background(),
		agentsession.SteeringClaimRequest{
			SessionID:  testSessionA,
			RunID:      run.ID,
			Generation: "generation-1",
		},
	)
	require.NoError(t, err)
	require.Len(t, delivered, 1)
	require.Equal(t, queued.ID, delivered[0].ID)
	require.Equal(t, agentsession.QueueStatusDelivered, delivered[0].Status)
}

func TestMemoryStore_SteerFallsBackToFollowUpWithoutActiveRun(t *testing.T) {
	store := NewStore()
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	queued := submitMemoryInboxMessage(
		t,
		store,
		"change direction",
		"client-queued",
		agentsession.DeliveryModeFollowUp,
		agentsession.SteeringFallbackFollowUp,
	)

	steering, err := store.SteerQueueEntry(
		context.Background(),
		agentsession.QueueMutationRequest{
			SessionID: testSessionA,
			EntryID:   queued.ID,
		},
	)

	require.NoError(t, err)
	require.Equal(t, agentsession.DeliveryModeSteering, steering.RequestedDeliveryMode)
	require.Equal(t, agentsession.DeliveryModeFollowUp, steering.DeliveryMode)
	require.Equal(t, agentsession.SteeringFallbackFollowUp, steering.SteeringFallback)
	require.Empty(t, steering.TargetRunID)

	activateMemoryInboxRunner(t, store, "generation-1")
	claimedEntry, _, claimed, err := store.ClaimNextFollowUp(
		context.Background(),
		agentsession.ClaimRequest{
			SessionID:  testSessionA,
			RunID:      memoryInboxRunID("steering-fallback"),
			Generation: "generation-1",
		},
	)
	require.NoError(t, err)
	require.True(t, claimed)
	require.Equal(t, queued.ID, claimedEntry.ID)
}

func TestMemoryStore_SteeringBindsToAndDeliversIntoActiveRun(t *testing.T) {
	store := NewStore()
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	activateMemoryInboxRunner(t, store, "generation-1")
	submitMemoryInboxMessage(
		t,
		store,
		"start",
		"client-start",
		agentsession.DeliveryModeFollowUp,
		agentsession.SteeringFallbackFollowUp,
	)
	_, run, claimed, err := store.ClaimNextFollowUp(
		context.Background(),
		agentsession.ClaimRequest{
			SessionID:  testSessionA,
			RunID:      memoryInboxRunID("active"),
			Generation: "generation-1",
		},
	)
	require.NoError(t, err)
	require.True(t, claimed)

	pending, err := store.HasPendingSteering(
		context.Background(),
		agentsession.SteeringClaimRequest{
			SessionID:  testSessionA,
			RunID:      run.ID,
			Generation: run.Generation,
		},
	)
	require.NoError(t, err)
	require.False(t, pending)
	delivered, err := store.ClaimSteering(
		context.Background(),
		agentsession.SteeringClaimRequest{
			SessionID:  testSessionA,
			RunID:      run.ID,
			Generation: run.Generation,
		},
	)
	require.NoError(t, err)
	require.Empty(t, delivered)

	steering := submitMemoryInboxMessage(
		t,
		store,
		"change direction",
		"client-steer",
		agentsession.DeliveryModeSteering,
		agentsession.SteeringFallbackFollowUp,
	)
	require.Equal(t, run.ID, steering.TargetRunID)

	pending, err = store.HasPendingSteering(
		context.Background(),
		agentsession.SteeringClaimRequest{
			SessionID:  testSessionA,
			RunID:      run.ID,
			Generation: run.Generation,
		},
	)
	require.NoError(t, err)
	require.True(t, pending)

	delivered, err = store.ClaimSteering(
		context.Background(),
		agentsession.SteeringClaimRequest{
			SessionID:  testSessionA,
			RunID:      run.ID,
			Generation: "stale-generation",
		},
	)
	require.NoError(t, err)
	require.Empty(t, delivered)

	delivered, err = store.ClaimSteering(
		context.Background(),
		agentsession.SteeringClaimRequest{
			SessionID:  testSessionA,
			RunID:      run.ID,
			Generation: run.Generation,
		},
	)
	require.NoError(t, err)
	require.Len(t, delivered, 1)
	require.Equal(t, agentsession.QueueStatusDelivered, delivered[0].Status)
	pending, err = store.HasPendingSteering(
		context.Background(),
		agentsession.SteeringClaimRequest{
			SessionID:  testSessionA,
			RunID:      run.ID,
			Generation: run.Generation,
		},
	)
	require.NoError(t, err)
	require.False(t, pending)

	messages, err := store.GetMessages(
		context.Background(),
		testSessionA,
		MessageQueryOptions{},
	)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, "change direction", messages[0].Content)
}

func TestMemoryStore_ValidatesQueueOperations(t *testing.T) {
	store := NewStore()
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	activateMemoryInboxRunner(t, store, "generation-1")

	state, err := store.GetExecutionState(context.Background(), testSessionA)
	require.NoError(t, err)
	require.Equal(t, testSessionA, state.SessionID)
	require.Empty(t, state.Queue)
	_, err = store.GetExecutionState(context.Background(), "invalid")
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
	_, err = store.GetExecutionState(context.Background(), testSessionB)
	require.EqualError(t, err, "session not found")

	batch, err := store.ListEvents(context.Background(), testSessionA, 0, 0)
	require.NoError(t, err)
	require.Empty(t, batch.Events)
	_, err = store.ListEvents(context.Background(), testSessionA, 1, 10)
	require.ErrorIs(t, err, agentsession.ErrCursorBeyondSession)
	_, err = store.ListEvents(context.Background(), testSessionA, -1, 10)
	require.EqualError(t, err, "after cursor must be greater than or equal to zero")
	_, err = store.ListEvents(context.Background(), "invalid", 0, 10)
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
	_, err = store.ListEvents(context.Background(), testSessionB, 0, 10)
	require.EqualError(t, err, "session not found")

	_, err = store.EditQueueEntry(context.Background(), agentsession.QueueEditRequest{
		SessionID: "invalid",
		EntryID:   "entry",
		Content:   "message",
	})
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
	_, err = store.EditQueueEntry(context.Background(), agentsession.QueueEditRequest{
		SessionID: testSessionA,
		Content:   "message",
	})
	require.EqualError(t, err, "queue entry id is required")
	_, err = store.CancelQueueEntry(
		context.Background(),
		agentsession.QueueMutationRequest{
			SessionID: testSessionB,
			EntryID:   "missing",
		},
	)
	require.EqualError(t, err, "pending queue entry not found")

	_, _, claimed, err := store.ClaimNextFollowUp(
		context.Background(),
		agentsession.ClaimRequest{
			SessionID:  testSessionA,
			RunID:      memoryInboxRunID("none"),
			Generation: "generation-1",
		},
	)
	require.NoError(t, err)
	require.False(t, claimed)
	_, _, _, err = store.ClaimNextFollowUp(
		context.Background(),
		agentsession.ClaimRequest{SessionID: "invalid"},
	)
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
	_, _, _, err = store.ClaimNextFollowUp(
		context.Background(),
		agentsession.ClaimRequest{SessionID: testSessionA},
	)
	require.EqualError(t, err, "run id and generation are required")

	_, err = store.HasPendingSteering(
		context.Background(),
		agentsession.SteeringClaimRequest{SessionID: "invalid"},
	)
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
	_, err = store.ClaimSteering(
		context.Background(),
		agentsession.SteeringClaimRequest{SessionID: testSessionA},
	)
	require.EqualError(t, err, "run id and generation are required")
	pending, err := store.HasPendingSteering(
		context.Background(),
		agentsession.SteeringClaimRequest{
			SessionID:  testSessionA,
			RunID:      memoryInboxRunID("missing"),
			Generation: "generation-1",
		},
	)
	require.NoError(t, err)
	require.False(t, pending)

	_, transitioned, err := store.FinishSessionRun(
		context.Background(),
		agentsession.RunFinishRequest{
			SessionID:  testSessionA,
			RunID:      memoryInboxRunID("missing"),
			Generation: "generation-1",
			Status:     agentsession.RunStatusCompleted,
		},
	)
	require.NoError(t, err)
	require.False(t, transitioned)
	_, _, err = store.FinishSessionRun(
		context.Background(),
		agentsession.RunFinishRequest{SessionID: "invalid"},
	)
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
	_, _, err = store.FinishSessionRun(
		context.Background(),
		agentsession.RunFinishRequest{SessionID: testSessionA},
	)
	require.EqualError(t, err, "run id and generation are required")
	_, _, err = store.FinishSessionRun(
		context.Background(),
		agentsession.RunFinishRequest{
			SessionID:  testSessionA,
			RunID:      memoryInboxRunID("invalid-status"),
			Generation: "generation-1",
			Status:     agentsession.RunStatusRunning,
		},
	)
	require.EqualError(t, err, "terminal run status is required")

	_, err = store.ReconcileActiveRuns(context.Background(), " ")
	require.EqualError(t, err, "generation is required")
}

func TestMemoryStore_InboxRequiresStore(t *testing.T) {
	var store *Store
	ctx := context.Background()

	_, err := store.EnqueueMessage(ctx, agentsession.EnqueueRequest{})
	require.EqualError(t, err, "store is required")
	_, err = store.GetExecutionState(ctx, testSessionA)
	require.EqualError(t, err, "store is required")
	_, err = store.ListEvents(ctx, testSessionA, 0, 10)
	require.EqualError(t, err, "store is required")
	_, err = store.EditQueueEntry(ctx, agentsession.QueueEditRequest{Content: "message"})
	require.EqualError(t, err, "store is required")
	_, err = store.CancelQueueEntry(ctx, agentsession.QueueMutationRequest{})
	require.EqualError(t, err, "store is required")
	_, err = store.PromoteQueueEntry(ctx, agentsession.QueueMutationRequest{})
	require.EqualError(t, err, "store is required")
	_, _, _, err = store.ClaimNextFollowUp(ctx, agentsession.ClaimRequest{})
	require.EqualError(t, err, "store is required")
	_, err = store.HasPendingSteering(ctx, agentsession.SteeringClaimRequest{})
	require.EqualError(t, err, "store is required")
	_, err = store.ClaimSteering(ctx, agentsession.SteeringClaimRequest{})
	require.EqualError(t, err, "store is required")
	_, _, err = store.FinishSessionRun(ctx, agentsession.RunFinishRequest{})
	require.EqualError(t, err, "store is required")
	_, err = store.ReconcileActiveRuns(ctx, "generation")
	require.EqualError(t, err, "store is required")
	_, err = store.ListRunnableSessions(ctx)
	require.EqualError(t, err, "store is required")
}

func TestMemoryStore_ResolvesSteeringAndReconcilesAbandonedRuns(t *testing.T) {
	store := NewStore()
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))

	fallback := submitMemoryInboxMessage(
		t,
		store,
		"fallback",
		"client-fallback",
		agentsession.DeliveryModeSteering,
		agentsession.SteeringFallbackFollowUp,
	)
	require.Equal(t, agentsession.DeliveryModeSteering, fallback.RequestedDeliveryMode)
	require.Equal(t, agentsession.DeliveryModeFollowUp, fallback.DeliveryMode)

	_, err := store.EnqueueMessage(context.Background(), agentsession.EnqueueRequest{
		ID:                 memoryInboxQueueID("reject-without-run"),
		SessionID:          testSessionA,
		Content:            "reject",
		ClientSubmissionID: "client-reject-without-run",
		DeliveryMode:       agentsession.DeliveryModeSteering,
		SteeringFallback:   agentsession.SteeringFallbackReject,
	})
	require.ErrorIs(t, err, agentsession.ErrSteeringRequiresRun)

	activateMemoryInboxRunner(t, store, "old-generation")
	_, run, claimed, err := store.ClaimNextFollowUp(
		context.Background(),
		agentsession.ClaimRequest{
			SessionID:  testSessionA,
			RunID:      memoryInboxRunID("restart"),
			Generation: "old-generation",
		},
	)
	require.NoError(t, err)
	require.True(t, claimed)

	pendingFallback := submitMemoryInboxMessage(
		t,
		store,
		"later",
		"client-later",
		agentsession.DeliveryModeSteering,
		agentsession.SteeringFallbackFollowUp,
	)
	pendingReject := submitMemoryInboxMessage(
		t,
		store,
		"only now",
		"client-only-now",
		agentsession.DeliveryModeSteering,
		agentsession.SteeringFallbackReject,
	)

	result, err := store.ReconcileActiveRuns(context.Background(), "new-generation")
	require.NoError(t, err)
	require.Equal(t, 1, result.RunCount)
	require.Equal(t, []string{testSessionA}, result.SessionIDs)
	require.Len(t, result.Runs, 1)
	require.Equal(t, run.ID, result.Runs[0].ID)
	require.Equal(t, agentsession.RunStatusInterrupted, result.Runs[0].Status)
	require.Equal(t, "daemon_restart", result.Runs[0].Reason)
	require.Empty(t, result.Runs[0].LastError)

	state, err := store.GetExecutionState(context.Background(), testSessionA)
	require.NoError(t, err)
	require.Nil(t, state.ActiveRun)
	interrupted := getMemoryInboxEntry(t, state.Queue, run.QueueEntryID)
	require.Equal(t, agentsession.QueueStatusInterrupted, interrupted.Status)
	require.Equal(t, "daemon_restart", interrupted.LastError)

	pendingFallback = getMemoryInboxEntry(t, state.Queue, pendingFallback.ID)
	require.Equal(t, agentsession.DeliveryModeFollowUp, pendingFallback.DeliveryMode)
	require.Empty(t, pendingFallback.TargetRunID)
	pendingReject = getMemoryInboxEntry(t, state.Queue, pendingReject.ID)
	require.Equal(t, agentsession.QueueStatusCancelled, pendingReject.Status)

	runnable, err := store.ListRunnableSessions(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{testSessionA}, runnable)

	_, _, claimed, err = store.ClaimNextFollowUp(
		context.Background(),
		agentsession.ClaimRequest{
			SessionID:  testSessionA,
			RunID:      memoryInboxRunID("stale"),
			Generation: "old-generation",
		},
	)
	require.ErrorIs(t, err, agentsession.ErrStaleRunnerGeneration)
	require.False(t, claimed)
}

func TestMemoryStore_FinishSessionRunResolvesPendingSteering(t *testing.T) {
	store := NewStore()
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	activateMemoryInboxRunner(t, store, "generation-1")
	active := submitMemoryInboxMessage(
		t,
		store,
		"start",
		"client-start",
		agentsession.DeliveryModeFollowUp,
		agentsession.SteeringFallbackFollowUp,
	)
	_, run, claimed, err := store.ClaimNextFollowUp(
		context.Background(),
		agentsession.ClaimRequest{
			SessionID:  testSessionA,
			RunID:      memoryInboxRunID("finish-steering"),
			Generation: "generation-1",
		},
	)
	require.NoError(t, err)
	require.True(t, claimed)

	fallback := submitMemoryInboxMessage(
		t,
		store,
		"later",
		"client-fallback-after-finish",
		agentsession.DeliveryModeSteering,
		agentsession.SteeringFallbackFollowUp,
	)
	rejected := submitMemoryInboxMessage(
		t,
		store,
		"only now",
		"client-reject-after-finish",
		agentsession.DeliveryModeSteering,
		agentsession.SteeringFallbackReject,
	)

	_, transitioned, err := store.FinishSessionRun(
		context.Background(),
		agentsession.RunFinishRequest{
			SessionID:  testSessionA,
			RunID:      run.ID,
			Generation: run.Generation,
			Status:     agentsession.RunStatusCompleted,
		},
	)
	require.NoError(t, err)
	require.True(t, transitioned)

	state, err := store.GetExecutionState(context.Background(), testSessionA)
	require.NoError(t, err)
	require.Equal(
		t,
		agentsession.QueueStatusCompleted,
		getMemoryInboxEntry(t, state.Queue, active.ID).Status,
	)
	fallback = getMemoryInboxEntry(t, state.Queue, fallback.ID)
	require.Equal(t, agentsession.QueueStatusPending, fallback.Status)
	require.Equal(t, agentsession.DeliveryModeFollowUp, fallback.DeliveryMode)
	require.Empty(t, fallback.TargetRunID)
	rejected = getMemoryInboxEntry(t, state.Queue, rejected.ID)
	require.Equal(t, agentsession.QueueStatusCancelled, rejected.Status)
	require.Equal(t, "target run completed before steering delivery", rejected.LastError)
}

func TestMemoryStore_ArchivedSessionsCannotBeClaimed(t *testing.T) {
	store := NewStore()
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionB}))
	activateMemoryInboxRunner(t, store, "generation-1")

	submitMemoryInboxMessage(
		t,
		store,
		"active",
		"client-active-before-archive",
		agentsession.DeliveryModeFollowUp,
		agentsession.SteeringFallbackFollowUp,
	)
	_, run, claimed, err := store.ClaimNextFollowUp(
		context.Background(),
		agentsession.ClaimRequest{
			SessionID:  testSessionA,
			RunID:      memoryInboxRunID("archive-active"),
			Generation: "generation-1",
		},
	)
	require.NoError(t, err)
	require.True(t, claimed)
	submitMemoryInboxMessage(
		t,
		store,
		"steer",
		"client-steer-before-archive",
		agentsession.DeliveryModeSteering,
		agentsession.SteeringFallbackFollowUp,
	)
	_, err = store.EnqueueMessage(context.Background(), agentsession.EnqueueRequest{
		ID:                 memoryInboxQueueID("archive-pending"),
		SessionID:          testSessionB,
		Content:            "pending",
		ClientSubmissionID: "client-pending-before-archive",
		DeliveryMode:       agentsession.DeliveryModeFollowUp,
		SteeringFallback:   agentsession.SteeringFallbackFollowUp,
	})
	require.NoError(t, err)

	now := time.Now().UTC()
	require.NoError(t, store.Save(context.Background(), Session{
		ID:         testSessionA,
		Archived:   true,
		ArchivedAt: now,
	}))
	require.NoError(t, store.Save(context.Background(), Session{
		ID:         testSessionB,
		Archived:   true,
		ArchivedAt: now,
	}))

	runnable, err := store.ListRunnableSessions(context.Background())
	require.NoError(t, err)
	require.Empty(t, runnable)
	_, _, claimed, err = store.ClaimNextFollowUp(
		context.Background(),
		agentsession.ClaimRequest{
			SessionID:  testSessionB,
			RunID:      memoryInboxRunID("archive-pending"),
			Generation: "generation-1",
		},
	)
	require.EqualError(t, err, "session not found")
	require.False(t, claimed)
	pending, err := store.HasPendingSteering(
		context.Background(),
		agentsession.SteeringClaimRequest{
			SessionID:  testSessionA,
			RunID:      run.ID,
			Generation: run.Generation,
		},
	)
	require.NoError(t, err)
	require.False(t, pending)
	_, err = store.ClaimSteering(
		context.Background(),
		agentsession.SteeringClaimRequest{
			SessionID:  testSessionA,
			RunID:      run.ID,
			Generation: run.Generation,
		},
	)
	require.EqualError(t, err, "session not found")

	_, transitioned, err := store.FinishSessionRun(
		context.Background(),
		agentsession.RunFinishRequest{
			SessionID:  testSessionA,
			RunID:      run.ID,
			Generation: run.Generation,
			Status:     agentsession.RunStatusInterrupted,
		},
	)
	require.NoError(t, err)
	require.True(t, transitioned)
}

func TestMemoryStore_RunIDsAreUniqueAcrossSessionsUntilDeletion(t *testing.T) {
	store := NewStore()
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionB}))
	activateMemoryInboxRunner(t, store, "generation-1")
	for index, sessionID := range []string{testSessionA, testSessionB} {
		_, err := store.EnqueueMessage(context.Background(), agentsession.EnqueueRequest{
			ID:                 memoryInboxQueueID(fmt.Sprintf("global-run-%d", index)),
			SessionID:          sessionID,
			Content:            "message",
			ClientSubmissionID: fmt.Sprintf("global-run-%d", index),
			DeliveryMode:       agentsession.DeliveryModeFollowUp,
			SteeringFallback:   agentsession.SteeringFallbackFollowUp,
		})
		require.NoError(t, err)
	}

	runID := memoryInboxRunID("global")
	_, run, claimed, err := store.ClaimNextFollowUp(
		context.Background(),
		agentsession.ClaimRequest{
			SessionID:  testSessionA,
			RunID:      runID,
			Generation: "generation-1",
		},
	)
	require.NoError(t, err)
	require.True(t, claimed)
	_, transitioned, err := store.FinishSessionRun(
		context.Background(),
		agentsession.RunFinishRequest{
			SessionID:  testSessionA,
			RunID:      run.ID,
			Generation: run.Generation,
			Status:     agentsession.RunStatusCompleted,
		},
	)
	require.NoError(t, err)
	require.True(t, transitioned)

	_, _, claimed, err = store.ClaimNextFollowUp(
		context.Background(),
		agentsession.ClaimRequest{
			SessionID:  testSessionB,
			RunID:      runID,
			Generation: "generation-1",
		},
	)
	require.EqualError(t, err, "run id is already used")
	require.False(t, claimed)

	require.NoError(t, store.Delete(context.Background(), testSessionA))
	_, _, claimed, err = store.ClaimNextFollowUp(
		context.Background(),
		agentsession.ClaimRequest{
			SessionID:  testSessionB,
			RunID:      runID,
			Generation: "generation-1",
		},
	)
	require.NoError(t, err)
	require.True(t, claimed)
}

func TestMemoryStore_MapsTerminalRunOutcomesToQueueAndEvents(t *testing.T) {
	tests := []struct {
		name        string
		runStatus   agentsession.RunStatus
		queueStatus agentsession.QueueStatus
		eventType   agentsession.EventType
	}{
		{
			name:        "completed",
			runStatus:   agentsession.RunStatusCompleted,
			queueStatus: agentsession.QueueStatusCompleted,
			eventType:   agentsession.EventTypeRunCompleted,
		},
		{
			name:        "interrupted",
			runStatus:   agentsession.RunStatusInterrupted,
			queueStatus: agentsession.QueueStatusInterrupted,
			eventType:   agentsession.EventTypeRunInterrupted,
		},
		{
			name:        "failed",
			runStatus:   agentsession.RunStatusFailed,
			queueStatus: agentsession.QueueStatusFailed,
			eventType:   agentsession.EventTypeRunFailed,
		},
		{
			name:        "cancelled",
			runStatus:   agentsession.RunStatusCancelled,
			queueStatus: agentsession.QueueStatusCancelled,
			eventType:   agentsession.EventTypeRunCancelled,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := NewStore()
			require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
			activateMemoryInboxRunner(t, store, "generation-1")
			entry := submitMemoryInboxMessage(
				t,
				store,
				test.name,
				"client-"+test.name,
				agentsession.DeliveryModeFollowUp,
				agentsession.SteeringFallbackFollowUp,
			)
			_, run, claimed, err := store.ClaimNextFollowUp(
				context.Background(),
				agentsession.ClaimRequest{
					SessionID:  testSessionA,
					RunID:      memoryInboxRunID(test.name),
					Generation: "generation-1",
				},
			)
			require.NoError(t, err)
			require.True(t, claimed)

			_, transitioned, err := store.FinishSessionRun(
				context.Background(),
				agentsession.RunFinishRequest{
					SessionID:  testSessionA,
					RunID:      run.ID,
					Generation: "stale-generation",
					Status:     test.runStatus,
				},
			)
			require.NoError(t, err)
			require.False(t, transitioned)

			finished, transitioned, err := store.FinishSessionRun(
				context.Background(),
				agentsession.RunFinishRequest{
					SessionID:  testSessionA,
					RunID:      run.ID,
					Generation: run.Generation,
					Status:     test.runStatus,
					Reason:     "reason",
					LastError:  "last error",
				},
			)
			require.NoError(t, err)
			require.True(t, transitioned)
			require.Equal(t, test.runStatus, finished.Status)

			state, err := store.GetExecutionState(context.Background(), testSessionA)
			require.NoError(t, err)
			require.Equal(
				t,
				test.queueStatus,
				getMemoryInboxEntry(t, state.Queue, entry.ID).Status,
			)
			batch, err := store.ListEvents(
				context.Background(),
				testSessionA,
				0,
				256,
			)
			require.NoError(t, err)
			require.Equal(t, test.eventType, batch.Events[len(batch.Events)-1].Type)

			sameRun, transitioned, err := store.FinishSessionRun(
				context.Background(),
				agentsession.RunFinishRequest{
					SessionID:  testSessionA,
					RunID:      run.ID,
					Generation: run.Generation,
					Status:     agentsession.RunStatusFailed,
				},
			)
			require.NoError(t, err)
			require.False(t, transitioned)
			require.Equal(t, test.runStatus, sameRun.Status)

			_, _, claimed, err = store.ClaimNextFollowUp(
				context.Background(),
				agentsession.ClaimRequest{
					SessionID:  testSessionA,
					RunID:      memoryInboxRunID("empty-" + test.name),
					Generation: "generation-1",
				},
			)
			require.NoError(t, err)
			require.False(t, claimed)
		})
	}
}

func TestMemoryStore_ReconcileActiveRunsOrdersMultipleSessions(t *testing.T) {
	store := NewStore()
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionB}))
	activateMemoryInboxRunner(t, store, "old-generation")

	for index, sessionID := range []string{testSessionB, testSessionA} {
		seed := fmt.Sprintf("multi-%d", index)
		_, err := store.EnqueueMessage(context.Background(), agentsession.EnqueueRequest{
			ID:                 memoryInboxQueueID(seed),
			SessionID:          sessionID,
			Content:            seed,
			ClientSubmissionID: seed,
			DeliveryMode:       agentsession.DeliveryModeFollowUp,
			SteeringFallback:   agentsession.SteeringFallbackFollowUp,
		})
		require.NoError(t, err)
		_, _, claimed, err := store.ClaimNextFollowUp(
			context.Background(),
			agentsession.ClaimRequest{
				SessionID:  sessionID,
				RunID:      memoryInboxRunID(seed),
				Generation: "old-generation",
			},
		)
		require.NoError(t, err)
		require.True(t, claimed)
	}

	result, err := store.ReconcileActiveRuns(context.Background(), "new-generation")
	require.NoError(t, err)
	require.Equal(t, 2, result.RunCount)
	require.Equal(t, []string{testSessionA, testSessionB}, result.SessionIDs)
	require.Len(t, result.Runs, 2)
	require.Equal(t, testSessionA, result.Runs[0].SessionID)
	require.Equal(t, testSessionB, result.Runs[1].SessionID)
}

func TestMemoryStore_ExecutionStateCapsTerminalQueueHistory(t *testing.T) {
	store := NewStore()
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	activateMemoryInboxRunner(t, store, "generation-1")

	for index := 1; index <= memorySessionStateTerminalLimit+1; index++ {
		seed := fmt.Sprintf("%03d", index)
		submitMemoryInboxMessage(
			t,
			store,
			"message-"+seed,
			"client-"+seed,
			agentsession.DeliveryModeFollowUp,
			agentsession.SteeringFallbackFollowUp,
		)
		_, run, claimed, err := store.ClaimNextFollowUp(
			context.Background(),
			agentsession.ClaimRequest{
				SessionID:  testSessionA,
				RunID:      memoryInboxRunID(seed),
				Generation: "generation-1",
			},
		)
		require.NoError(t, err)
		require.True(t, claimed)
		_, transitioned, err := store.FinishSessionRun(
			context.Background(),
			agentsession.RunFinishRequest{
				SessionID:  testSessionA,
				RunID:      run.ID,
				Generation: run.Generation,
				Status:     agentsession.RunStatusCompleted,
			},
		)
		require.NoError(t, err)
		require.True(t, transitioned)
	}

	state, err := store.GetExecutionState(context.Background(), testSessionA)
	require.NoError(t, err)
	require.Len(t, state.Queue, memorySessionStateTerminalLimit)
	require.Equal(t, int64(2), state.Queue[0].Sequence)
}

func TestMemoryStore_EventRetentionExpiresOldCursors(t *testing.T) {
	store := NewStore()
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	now := time.Now().UTC()

	store.mu.Lock()
	inbox := store.getOrCreateSessionInboxLocked(testSessionA)
	inbox.cursor = memorySessionEventRetentionCount + 1
	inbox.retainedCursorFloor = 1
	inbox.events = []agentsession.Event{
		{
			SessionID: testSessionA,
			Cursor:    1,
			Type:      agentsession.EventTypeQueueEnqueued,
			CreatedAt: now.Add(-memorySessionEventRetentionAge - time.Hour),
		},
		{
			SessionID: testSessionA,
			Cursor:    memorySessionEventRetentionCount + 1,
			Type:      agentsession.EventTypeQueueUpdated,
			CreatedAt: now,
		},
	}
	store.pruneSessionInboxEventsLocked(inbox, now)
	store.mu.Unlock()

	_, err := store.ListEvents(context.Background(), testSessionA, 0, 10)
	require.ErrorIs(t, err, agentsession.ErrCursorExpired)
	batch, err := store.ListEvents(
		context.Background(),
		testSessionA,
		memorySessionEventRetentionCount,
		10,
	)
	require.NoError(t, err)
	require.Len(t, batch.Events, 1)
	require.Equal(t, memorySessionEventRetentionCount+1, batch.RetainedCursorFloor)
}

func TestMemoryStore_ConcurrentClaimCreatesOneActiveRun(t *testing.T) {
	store := NewStore()
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	activateMemoryInboxRunner(t, store, "generation-1")
	submitMemoryInboxMessage(
		t,
		store,
		"first",
		"client-1",
		agentsession.DeliveryModeFollowUp,
		agentsession.SteeringFallbackFollowUp,
	)

	var waitGroup sync.WaitGroup
	var mu sync.Mutex
	claims := 0
	errorsSeen := make([]error, 0, 2)
	for index := range 2 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			_, _, claimed, err := store.ClaimNextFollowUp(
				context.Background(),
				agentsession.ClaimRequest{
					SessionID:  testSessionA,
					RunID:      memoryInboxRunID(string(rune('a' + index))),
					Generation: "generation-1",
				},
			)
			mu.Lock()
			defer mu.Unlock()
			if claimed {
				claims++
			}
			errorsSeen = append(errorsSeen, err)
		}()
	}
	waitGroup.Wait()

	require.Equal(t, 1, claims)
	require.NoError(t, errorsSeen[0])
	require.NoError(t, errorsSeen[1])
}

func submitMemoryInboxMessage(
	t *testing.T,
	store *Store,
	content string,
	clientSubmissionID string,
	mode agentsession.DeliveryMode,
	fallback agentsession.SteeringFallback,
) agentsession.QueueEntry {
	t.Helper()
	entry, err := store.EnqueueMessage(context.Background(), agentsession.EnqueueRequest{
		ID:                 memoryInboxQueueID(clientSubmissionID),
		SessionID:          testSessionA,
		Content:            content,
		ClientSubmissionID: clientSubmissionID,
		DeliveryMode:       mode,
		SteeringFallback:   fallback,
	})
	require.NoError(t, err)
	return entry
}

func activateMemoryInboxRunner(t *testing.T, store *Store, generation string) {
	t.Helper()
	_, err := store.ReconcileActiveRuns(context.Background(), generation)
	require.NoError(t, err)
}

func TestMemoryStore_ClaimSnapshotsReasoningUnderSessionLock(t *testing.T) {
	ctx := context.Background()
	store := NewStore()
	require.NoError(t, store.Save(ctx, Session{
		ID:                      testSessionA,
		ReasoningEffortOverride: "high",
	}))
	activateMemoryInboxRunner(t, store, "generation-reasoning")
	submitMemoryInboxMessage(
		t,
		store,
		"reason",
		"reason",
		agentsession.DeliveryModeFollowUp,
		agentsession.SteeringFallbackFollowUp,
	)

	_, run, claimed, err := store.ClaimNextFollowUp(ctx, agentsession.ClaimRequest{
		SessionID:  testSessionA,
		RunID:      memoryInboxRunID("reasoning"),
		Generation: "generation-reasoning",
		Reasoning:  testReasoningClaimContext(),
	})
	require.NoError(t, err)
	require.True(t, claimed)
	require.Equal(t, agentsession.ReasoningEffort("high"), run.Reasoning.Effort)
	require.True(t, run.Reasoning.Summary)

	low := "low"
	_, err = store.Patch(ctx, testSessionA, SessionPatch{ReasoningEffortOverride: &low})
	require.NoError(t, err)
	state, err := store.GetExecutionState(ctx, testSessionA)
	require.NoError(t, err)
	require.Equal(t, agentsession.ReasoningEffort("high"), state.ActiveRun.Reasoning.Effort)
}

func testReasoningClaimContext() agentsession.ReasoningClaimContext {
	return agentsession.ReasoningClaimContext{
		Model: agentsession.ReasoningModelTuple{
			Provider: "openai",
			API:      "openai-responses",
			Model:    "gpt-test",
		},
		Capability: agentsession.ReasoningCapability{
			Efforts:       []agentsession.ReasoningEffort{"low", "medium", "high"},
			DefaultEffort: "medium",
			Summary:       true,
		},
		Reasoning:    true,
		CatalogFound: true,
		APISupported: true,
	}
}

func getMemoryInboxEntry(
	t *testing.T,
	entries []agentsession.QueueEntry,
	entryID string,
) agentsession.QueueEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.ID == entryID {
			return entry
		}
	}
	require.FailNow(t, "queue entry not found", entryID)
	return agentsession.QueueEntry{}
}

func memoryInboxQueueID(seed string) string {
	return nanoid.MustFromSeed("qmsg_", seed, "MemoryInboxQueueSeed")
}

func memoryInboxRunID(seed string) string {
	return nanoid.MustFromSeed("run_", seed, "MemoryInboxRunSeed")
}
