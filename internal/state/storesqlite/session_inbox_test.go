package storesqlite

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	agentsession "github.com/wandxy/morph/pkg/agent/session"
	"github.com/wandxy/morph/pkg/nanoid"
	"gorm.io/gorm"
)

func TestSQLiteStore_SubmitMessageIsDurableAndIdempotent(t *testing.T) {
	path := t.TempDir() + "/session.db"
	store, err := NewStore(path)
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))

	req := agentsession.SubmitRequest{
		ID:                 nanoid.MustFromSeed("qmsg_", "first", "QueueMessageSeed"),
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
	first, err := store.SubmitMessage(context.Background(), req)
	require.NoError(t, err)
	retry, err := store.SubmitMessage(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, first, retry)

	conflict := req
	conflict.Content = "different"
	_, err = store.SubmitMessage(context.Background(), conflict)
	require.EqualError(t, err, "client submission id is already used by a different message")

	reopened, err := NewStore(path)
	require.NoError(t, err)
	reloaded, err := reopened.GetExecutionState(context.Background(), testSessionA)
	require.NoError(t, err)
	require.Len(t, reloaded.Queue, 1)

	state, err := store.GetExecutionState(context.Background(), testSessionA)
	require.NoError(t, err)
	require.Equal(t, int64(1), state.Cursor)
	require.Equal(t, int64(1), state.RetainedCursorFloor)
	require.Equal(t, 1, state.QueueDepth)
	require.Len(t, state.Queue, 1)
	require.Equal(t, req.Provenance, state.Queue[0].Provenance)

	batch, err := store.ListEvents(context.Background(), testSessionA, 0, 10)
	require.NoError(t, err)
	require.Len(t, batch.Events, 1)
	require.Equal(t, agentsession.EventTypeQueueEnqueued, batch.Events[0].Type)
	require.Equal(t, first.ID, batch.Events[0].Queue.ID)
}

func TestSQLiteStore_ExecutionStateCapsTerminalQueueHistory(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/session.db")
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))

	now := time.Now().UTC()
	records := make([]sessionQueueEntryModel, 0, sessionStateTerminalLimit+45)
	for index := 1; index <= sessionStateTerminalLimit+44; index++ {
		records = append(records, sessionQueueEntryModel{
			ID:                    fmt.Sprintf("qmsg_terminal_%03d", index),
			SessionID:             testSessionA,
			Content:               "completed",
			ClientSubmissionID:    fmt.Sprintf("submission-%03d", index),
			RequestedDeliveryMode: string(agentsession.DeliveryModeFollowUp),
			DeliveryMode:          string(agentsession.DeliveryModeFollowUp),
			SteeringFallback:      string(agentsession.SteeringFallbackFollowUp),
			Status:                string(agentsession.QueueStatusCompleted),
			Sequence:              int64(index),
			CreatedAt:             now,
			UpdatedAt:             now,
			CompletedAt:           now,
		})
	}
	records = append(records, sessionQueueEntryModel{
		ID:                    "qmsg_pending",
		SessionID:             testSessionA,
		Content:               "pending",
		ClientSubmissionID:    "submission-pending",
		RequestedDeliveryMode: string(agentsession.DeliveryModeFollowUp),
		DeliveryMode:          string(agentsession.DeliveryModeFollowUp),
		SteeringFallback:      string(agentsession.SteeringFallbackFollowUp),
		Status:                string(agentsession.QueueStatusPending),
		Sequence:              int64(sessionStateTerminalLimit + 45),
		CreatedAt:             now,
		UpdatedAt:             now,
	})
	require.NoError(t, store.db.Create(&records).Error)

	state, err := store.GetExecutionState(context.Background(), testSessionA)
	require.NoError(t, err)
	require.Len(t, state.Queue, sessionStateTerminalLimit+1)
	require.Equal(t, int64(45), state.Queue[0].Sequence)
	require.Equal(t, "qmsg_pending", state.Queue[len(state.Queue)-1].ID)
	require.Equal(t, 1, state.QueueDepth)
}

func TestSQLiteStore_ClaimsFollowUpsOneAtATimeAndHonorsPromotion(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/session.db")
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	activateInboxTestRunner(t, store, "generation-1")

	first := submitInboxTestMessage(t, store, "first", "client-1", agentsession.DeliveryModeFollowUp)
	second := submitInboxTestMessage(t, store, "second", "client-2", agentsession.DeliveryModeFollowUp)
	_, err = store.PromoteQueueEntry(context.Background(), agentsession.QueueMutationRequest{
		SessionID: testSessionA,
		EntryID:   second.ID,
	})
	require.NoError(t, err)

	entry, run, claimed, err := store.ClaimNextFollowUp(context.Background(), agentsession.ClaimRequest{
		SessionID:  testSessionA,
		RunID:      nanoid.MustFromSeed("run_", "second", "RunSeed"),
		Generation: "generation-1",
	})
	require.NoError(t, err)
	require.True(t, claimed)
	require.Equal(t, second.ID, entry.ID)
	require.Equal(t, agentsession.RunStatusRunning, run.Status)

	_, _, claimed, err = store.ClaimNextFollowUp(context.Background(), agentsession.ClaimRequest{
		SessionID:  testSessionA,
		RunID:      nanoid.MustFromSeed("run_", "blocked", "RunSeed"),
		Generation: "generation-1",
	})
	require.NoError(t, err)
	require.False(t, claimed)

	finished, transitioned, err := store.FinishSessionRun(context.Background(), agentsession.RunFinishRequest{
		SessionID:  testSessionA,
		RunID:      run.ID,
		Generation: "generation-1",
		Status:     agentsession.RunStatusCompleted,
	})
	require.NoError(t, err)
	require.True(t, transitioned)
	require.Equal(t, agentsession.RunStatusCompleted, finished.Status)

	entry, _, claimed, err = store.ClaimNextFollowUp(context.Background(), agentsession.ClaimRequest{
		SessionID:  testSessionA,
		RunID:      nanoid.MustFromSeed("run_", "first", "RunSeed"),
		Generation: "generation-1",
	})
	require.NoError(t, err)
	require.True(t, claimed)
	require.Equal(t, first.ID, entry.ID)
}

func TestSQLiteStore_SteersPendingFollowUpIntoActiveRun(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/session.db")
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	activateInboxTestRunner(t, store, "generation-1")

	submitInboxTestMessage(t, store, "start", "client-start", agentsession.DeliveryModeFollowUp)
	queued := submitInboxTestMessage(
		t,
		store,
		"change direction",
		"client-queued",
		agentsession.DeliveryModeFollowUp,
	)
	_, run, claimed, err := store.ClaimNextFollowUp(
		context.Background(),
		agentsession.ClaimRequest{
			SessionID:  testSessionA,
			RunID:      nanoid.MustFromSeed("run_", "active", "RunSeed"),
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

func TestSQLiteStore_SteerFallsBackToFollowUpWithoutActiveRun(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/session.db")
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	queued := submitInboxTestMessage(
		t,
		store,
		"change direction",
		"client-queued",
		agentsession.DeliveryModeFollowUp,
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

	activateInboxTestRunner(t, store, "generation-1")
	claimedEntry, _, claimed, err := store.ClaimNextFollowUp(
		context.Background(),
		agentsession.ClaimRequest{
			SessionID:  testSessionA,
			RunID:      nanoid.MustFromSeed("run_", "steering-fallback", "RunSeed"),
			Generation: "generation-1",
		},
	)
	require.NoError(t, err)
	require.True(t, claimed)
	require.Equal(t, queued.ID, claimedEntry.ID)
}

func TestSQLiteStore_SteeringBindsToAndDeliversIntoActiveRun(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/session.db")
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	activateInboxTestRunner(t, store, "generation-1")
	submitInboxTestMessage(t, store, "start", "client-start", agentsession.DeliveryModeFollowUp)
	_, run, claimed, err := store.ClaimNextFollowUp(context.Background(), agentsession.ClaimRequest{
		SessionID:  testSessionA,
		RunID:      nanoid.MustFromSeed("run_", "active", "RunSeed"),
		Generation: "generation-1",
	})
	require.NoError(t, err)
	require.True(t, claimed)

	pending, err := store.HasPendingSteering(context.Background(), agentsession.SteeringClaimRequest{
		SessionID:  testSessionA,
		RunID:      run.ID,
		Generation: "generation-1",
	})
	require.NoError(t, err)
	require.False(t, pending)

	steering := submitInboxTestMessage(t, store, "change direction", "client-steer", agentsession.DeliveryModeSteering)
	require.Equal(t, run.ID, steering.TargetRunID)
	pending, err = store.HasPendingSteering(context.Background(), agentsession.SteeringClaimRequest{
		SessionID:  testSessionA,
		RunID:      run.ID,
		Generation: "generation-1",
	})
	require.NoError(t, err)
	require.True(t, pending)

	delivered, err := store.ClaimSteering(context.Background(), agentsession.SteeringClaimRequest{
		SessionID:  testSessionA,
		RunID:      run.ID,
		Generation: "stale-generation",
	})
	require.NoError(t, err)
	require.Empty(t, delivered)

	delivered, err = store.ClaimSteering(context.Background(), agentsession.SteeringClaimRequest{
		SessionID:  testSessionA,
		RunID:      run.ID,
		Generation: "generation-1",
	})
	require.NoError(t, err)
	require.Len(t, delivered, 1)
	require.Equal(t, agentsession.QueueStatusDelivered, delivered[0].Status)
	pending, err = store.HasPendingSteering(context.Background(), agentsession.SteeringClaimRequest{
		SessionID:  testSessionA,
		RunID:      run.ID,
		Generation: "generation-1",
	})
	require.NoError(t, err)
	require.False(t, pending)

	messages, err := store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{})
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, "change direction", messages[0].Content)
}

func TestSQLiteStore_SteeringFallbackAndRestartReconciliation(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/session.db")
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))

	fallback := submitInboxTestMessage(t, store, "fallback", "client-fallback", agentsession.DeliveryModeSteering)
	require.Equal(t, agentsession.DeliveryModeSteering, fallback.RequestedDeliveryMode)
	require.Equal(t, agentsession.DeliveryModeFollowUp, fallback.DeliveryMode)
	retry, err := store.SubmitMessage(context.Background(), agentsession.SubmitRequest{
		ID:                 nanoid.MustFromSeed("qmsg_", "client-fallback", "QueueMessageSeed"),
		SessionID:          testSessionA,
		Content:            "fallback",
		ClientSubmissionID: "client-fallback",
		DeliveryMode:       agentsession.DeliveryModeSteering,
		SteeringFallback:   agentsession.SteeringFallbackFollowUp,
	})
	require.NoError(t, err)
	require.Equal(t, fallback, retry)

	rejectRequest := agentsession.SubmitRequest{
		ID:                 nanoid.MustFromSeed("qmsg_", "reject", "QueueMessageSeed"),
		SessionID:          testSessionA,
		Content:            "reject",
		ClientSubmissionID: "client-reject",
		DeliveryMode:       agentsession.DeliveryModeSteering,
		SteeringFallback:   agentsession.SteeringFallbackReject,
	}
	_, err = store.SubmitMessage(context.Background(), rejectRequest)
	require.EqualError(t, err, "steering requires an active run")

	activateInboxTestRunner(t, store, "old-generation")
	_, run, claimed, err := store.ClaimNextFollowUp(context.Background(), agentsession.ClaimRequest{
		SessionID:  testSessionA,
		RunID:      nanoid.MustFromSeed("run_", "restart", "RunSeed"),
		Generation: "old-generation",
	})
	require.NoError(t, err)
	require.True(t, claimed)

	result, err := store.ReconcileActiveRuns(context.Background(), "new-generation")
	require.NoError(t, err)
	require.Equal(t, 1, result.RunCount)
	require.Equal(t, []string{testSessionA}, result.SessionIDs)
	require.Len(t, result.Runs, 1)
	require.Equal(t, run.ID, result.Runs[0].ID)
	require.Equal(t, agentsession.RunStatusInterrupted, result.Runs[0].Status)

	state, err := store.GetExecutionState(context.Background(), testSessionA)
	require.NoError(t, err)
	require.Nil(t, state.ActiveRun)
	interrupted := getInboxTestEntry(t, state.Queue, run.QueueEntryID)
	require.Equal(t, agentsession.QueueStatusInterrupted, interrupted.Status)
	require.Equal(t, "daemon_restart", interrupted.LastError)
}

func TestSQLiteStore_MutatesPendingEntriesAndListsRunnableSessions(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/session.db")
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionB}))
	activateInboxTestRunner(t, store, "generation-1")

	editedEntry := submitInboxTestMessage(t, store, "draft", "client-edit", agentsession.DeliveryModeFollowUp)
	promotedEntry := submitInboxTestMessage(t, store, "promote", "client-promote", agentsession.DeliveryModeFollowUp)
	cancelledEntry := submitInboxTestMessage(t, store, "cancel", "client-cancel", agentsession.DeliveryModeFollowUp)

	edited, err := store.EditQueueEntry(context.Background(), agentsession.QueueEditRequest{
		SessionID: testSessionA,
		EntryID:   editedEntry.ID,
		Content:   "revised",
	})
	require.NoError(t, err)
	require.Equal(t, "revised", edited.Content)

	promoted, err := store.PromoteQueueEntry(context.Background(), agentsession.QueueMutationRequest{
		SessionID: testSessionA,
		EntryID:   promotedEntry.ID,
	})
	require.NoError(t, err)
	require.Positive(t, promoted.Priority)

	cancelled, err := store.CancelQueueEntry(context.Background(), agentsession.QueueMutationRequest{
		SessionID: testSessionA,
		EntryID:   cancelledEntry.ID,
	})
	require.NoError(t, err)
	require.Equal(t, agentsession.QueueStatusCancelled, cancelled.Status)
	require.False(t, cancelled.CompletedAt.IsZero())

	runnable, err := store.ListRunnableSessions(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{testSessionA}, runnable)

	_, _, claimed, err := store.ClaimNextFollowUp(context.Background(), agentsession.ClaimRequest{
		SessionID:  testSessionA,
		RunID:      nanoid.MustFromSeed("run_", "promoted", "RunSeed"),
		Generation: "generation-1",
	})
	require.NoError(t, err)
	require.True(t, claimed)

	_, err = store.EditQueueEntry(context.Background(), agentsession.QueueEditRequest{
		SessionID: testSessionA,
		EntryID:   promotedEntry.ID,
		Content:   "too late",
	})
	require.EqualError(t, err, "pending queue entry not found")
	_, err = store.EditQueueEntry(context.Background(), agentsession.QueueEditRequest{
		SessionID: testSessionA,
		EntryID:   editedEntry.ID,
		Content:   " ",
	})
	require.EqualError(t, err, "message is required")
}

func TestSQLiteStore_ResolvesPendingSteeringWhenTargetRunEnds(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/session.db")
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	activateInboxTestRunner(t, store, "generation-1")
	activeEntry := submitInboxTestMessage(t, store, "start", "client-start", agentsession.DeliveryModeFollowUp)
	_, run, claimed, err := store.ClaimNextFollowUp(context.Background(), agentsession.ClaimRequest{
		SessionID:  testSessionA,
		RunID:      nanoid.MustFromSeed("run_", "active", "RunSeed"),
		Generation: "generation-1",
	})
	require.NoError(t, err)
	require.True(t, claimed)

	fallback := submitInboxTestMessage(t, store, "later", "client-fallback", agentsession.DeliveryModeSteering)
	rejected, err := store.SubmitMessage(context.Background(), agentsession.SubmitRequest{
		ID:                 nanoid.MustFromSeed("qmsg_", "client-reject", "QueueMessageSeed"),
		SessionID:          testSessionA,
		Content:            "only now",
		ClientSubmissionID: "client-reject",
		DeliveryMode:       agentsession.DeliveryModeSteering,
		SteeringFallback:   agentsession.SteeringFallbackReject,
	})
	require.NoError(t, err)

	finished, transitioned, err := store.FinishSessionRun(context.Background(), agentsession.RunFinishRequest{
		SessionID:  testSessionA,
		RunID:      run.ID,
		Generation: "generation-1",
		Status:     agentsession.RunStatusInterrupted,
		Reason:     "user_interrupt",
	})
	require.NoError(t, err)
	require.True(t, transitioned)
	require.Equal(t, agentsession.RunStatusInterrupted, finished.Status)

	state, err := store.GetExecutionState(context.Background(), testSessionA)
	require.NoError(t, err)
	require.Nil(t, state.ActiveRun)
	active := getInboxTestEntry(t, state.Queue, activeEntry.ID)
	require.Equal(t, agentsession.QueueStatusInterrupted, active.Status)
	require.Equal(t, "user_interrupt", active.LastError)
	fallback = getInboxTestEntry(t, state.Queue, fallback.ID)
	require.Equal(t, agentsession.DeliveryModeSteering, fallback.RequestedDeliveryMode)
	require.Equal(t, agentsession.DeliveryModeFollowUp, fallback.DeliveryMode)
	require.Equal(t, agentsession.QueueStatusPending, fallback.Status)
	require.Empty(t, fallback.TargetRunID)
	rejected = getInboxTestEntry(t, state.Queue, rejected.ID)
	require.Equal(t, agentsession.QueueStatusCancelled, rejected.Status)
	require.Equal(t, "target run completed before steering delivery", rejected.LastError)

	runnable, err := store.ListRunnableSessions(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{testSessionA}, runnable)
}

func TestSQLiteStore_ArchivedSessionsCannotBeClaimed(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/session.db")
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionB}))
	activateInboxTestRunner(t, store, "generation-1")

	submitInboxTestMessage(t, store, "active", "client-active-before-archive", agentsession.DeliveryModeFollowUp)
	_, run, claimed, err := store.ClaimNextFollowUp(
		context.Background(),
		agentsession.ClaimRequest{
			SessionID:  testSessionA,
			RunID:      nanoid.MustFromSeed("run_", "archive-active", "RunSeed"),
			Generation: "generation-1",
		},
	)
	require.NoError(t, err)
	require.True(t, claimed)
	submitInboxTestMessage(t, store, "steer", "client-steer-before-archive", agentsession.DeliveryModeSteering)
	_, err = store.SubmitMessage(context.Background(), agentsession.SubmitRequest{
		ID:                 nanoid.MustFromSeed("qmsg_", "archive-pending", "QueueMessageSeed"),
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
			RunID:      nanoid.MustFromSeed("run_", "archive-pending", "RunSeed"),
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

func TestSQLiteStore_DeleteRemovesSessionInboxState(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/session.db")
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	activateInboxTestRunner(t, store, "generation-1")
	submitInboxTestMessage(t, store, "active", "client-delete", agentsession.DeliveryModeFollowUp)
	_, _, claimed, err := store.ClaimNextFollowUp(
		context.Background(),
		agentsession.ClaimRequest{
			SessionID:  testSessionA,
			RunID:      nanoid.MustFromSeed("run_", "delete", "RunSeed"),
			Generation: "generation-1",
		},
	)
	require.NoError(t, err)
	require.True(t, claimed)

	require.NoError(t, store.Delete(context.Background(), testSessionA))
	for _, model := range []any{
		&sessionQueueEntryModel{},
		&sessionRunModel{},
		&sessionEventModel{},
		&sessionExecutionStateModel{},
	} {
		var count int64
		require.NoError(
			t,
			store.db.Model(model).Where("session_id = ?", testSessionA).Count(&count).Error,
		)
		require.Zero(t, count)
	}
	runnable, err := store.ListRunnableSessions(context.Background())
	require.NoError(t, err)
	require.Empty(t, runnable)

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	state, err := store.GetExecutionState(context.Background(), testSessionA)
	require.NoError(t, err)
	require.Zero(t, state.Cursor)
	require.Empty(t, state.Queue)
	require.Nil(t, state.ActiveRun)
}

func TestSQLiteStore_ClaimSteeringRequiresRunOwnership(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/session.db")
	require.NoError(t, err)

	_, err = store.ClaimSteering(context.Background(), agentsession.SteeringClaimRequest{
		SessionID: testSessionA,
	})
	require.EqualError(t, err, "run id and generation are required")
}

func TestSQLiteStore_ConcurrentClaimCreatesOneActiveRun(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/session.db")
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	activateInboxTestRunner(t, store, "generation-1")
	submitInboxTestMessage(t, store, "first", "client-1", agentsession.DeliveryModeFollowUp)

	var wg sync.WaitGroup
	var mu sync.Mutex
	claims := 0
	errorsSeen := make([]error, 0, 2)
	for index := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, claimed, err := store.ClaimNextFollowUp(context.Background(), agentsession.ClaimRequest{
				SessionID:  testSessionA,
				RunID:      nanoid.MustFromSeed("run_", string(rune('a'+index)), "RunSeed"),
				Generation: "generation-1",
			})
			mu.Lock()
			defer mu.Unlock()
			if claimed {
				claims++
			}
			errorsSeen = append(errorsSeen, err)
		}()
	}
	wg.Wait()

	require.Equal(t, 1, claims)
	require.NoError(t, errorsSeen[0])
	require.NoError(t, errorsSeen[1])
}

func TestSQLiteStore_RejectsStaleRunnerGeneration(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/session.db")
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	activateInboxTestRunner(t, store, "current-generation")
	submitInboxTestMessage(t, store, "first", "client-1", agentsession.DeliveryModeFollowUp)

	_, _, claimed, err := store.ClaimNextFollowUp(context.Background(), agentsession.ClaimRequest{
		SessionID:  testSessionA,
		RunID:      nanoid.MustFromSeed("run_", "stale", "RunSeed"),
		Generation: "stale-generation",
	})
	require.ErrorIs(t, err, agentsession.ErrStaleRunnerGeneration)
	require.False(t, claimed)

	state, err := store.GetExecutionState(context.Background(), testSessionA)
	require.NoError(t, err)
	require.Nil(t, state.ActiveRun)
	require.Equal(t, 1, state.QueueDepth)
}

func TestSQLiteStore_PreservesFIFOAcrossReopen(t *testing.T) {
	path := t.TempDir() + "/session.db"
	store, err := NewStore(path)
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	first := submitInboxTestMessage(t, store, "first", "client-1", agentsession.DeliveryModeFollowUp)
	second := submitInboxTestMessage(t, store, "second", "client-2", agentsession.DeliveryModeFollowUp)
	third := submitInboxTestMessage(t, store, "third", "client-3", agentsession.DeliveryModeFollowUp)

	reopened, err := NewStore(path)
	require.NoError(t, err)
	activateInboxTestRunner(t, reopened, "generation-reopened")

	var claimedIDs []string
	for index := range 3 {
		entry, run, claimed, err := reopened.ClaimNextFollowUp(
			context.Background(),
			agentsession.ClaimRequest{
				SessionID:  testSessionA,
				RunID:      nanoid.MustFromSeed("run_", string(rune('a'+index)), "ReopenRunSeed"),
				Generation: "generation-reopened",
			},
		)
		require.NoError(t, err)
		require.True(t, claimed)
		claimedIDs = append(claimedIDs, entry.ID)
		_, transitioned, err := reopened.FinishSessionRun(
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
	require.Equal(t, []string{first.ID, second.ID, third.ID}, claimedIDs)
}

func TestSQLiteStore_FinishRejectsStaleRunGeneration(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/session.db")
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	activateInboxTestRunner(t, store, "current-generation")
	submitInboxTestMessage(t, store, "first", "client-1", agentsession.DeliveryModeFollowUp)
	_, run, claimed, err := store.ClaimNextFollowUp(context.Background(), agentsession.ClaimRequest{
		SessionID:  testSessionA,
		RunID:      nanoid.MustFromSeed("run_", "current", "RunSeed"),
		Generation: "current-generation",
	})
	require.NoError(t, err)
	require.True(t, claimed)

	_, transitioned, err := store.FinishSessionRun(context.Background(), agentsession.RunFinishRequest{
		SessionID:  testSessionA,
		RunID:      run.ID,
		Generation: "stale-generation",
		Status:     agentsession.RunStatusCompleted,
	})
	require.NoError(t, err)
	require.False(t, transitioned)

	state, err := store.GetExecutionState(context.Background(), testSessionA)
	require.NoError(t, err)
	require.NotNil(t, state.ActiveRun)
	require.Equal(t, run.ID, state.ActiveRun.ID)
}

func submitInboxTestMessage(
	t *testing.T,
	store *Store,
	content string,
	clientSubmissionID string,
	mode agentsession.DeliveryMode,
) agentsession.QueueEntry {
	t.Helper()
	entry, err := store.SubmitMessage(context.Background(), agentsession.SubmitRequest{
		ID:                 nanoid.MustFromSeed("qmsg_", clientSubmissionID, "QueueMessageSeed"),
		SessionID:          testSessionA,
		Content:            content,
		ClientSubmissionID: clientSubmissionID,
		DeliveryMode:       mode,
		SteeringFallback:   agentsession.SteeringFallbackFollowUp,
	})
	require.NoError(t, err)
	return entry
}

func activateInboxTestRunner(t *testing.T, store *Store, generation string) {
	t.Helper()
	_, err := store.ReconcileActiveRuns(context.Background(), generation)
	require.NoError(t, err)
}

func getInboxTestEntry(
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

func TestSQLiteStore_ExpiredEventCursorRequiresRehydration(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/session.db")
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))

	old := time.Now().UTC().Add(-sessionEventRetentionAge - time.Hour)
	require.NoError(t, store.db.Create(&sessionExecutionStateModel{
		SessionID:     testSessionA,
		Cursor:        sessionEventRetentionCount + 2,
		RetainedFloor: 1,
	}).Error)
	records := []sessionEventModel{
		{SessionID: testSessionA, Cursor: 1, Type: string(agentsession.EventTypeQueueEnqueued), CreatedAt: old},
		{
			SessionID: testSessionA,
			Cursor:    sessionEventRetentionCount + 2,
			Type:      string(agentsession.EventTypeQueueUpdated),
			CreatedAt: time.Now().UTC(),
		},
	}
	require.NoError(t, store.db.Create(&records).Error)
	require.NoError(t, store.db.Transaction(func(tx *gorm.DB) error {
		return pruneSessionEvents(tx, testSessionA, time.Now().UTC())
	}))

	_, err = store.ListEvents(context.Background(), testSessionA, 0, 10)
	require.ErrorIs(t, err, agentsession.ErrCursorExpired)
}

func TestSQLiteStore_RejectsFutureEventCursor(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/session.db")
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	submitInboxTestMessage(t, store, "first", "client-1", agentsession.DeliveryModeFollowUp)

	_, err = store.ListEvents(context.Background(), testSessionA, 2, 10)
	require.EqualError(t, err, "after cursor is beyond the session cursor")
}
