package storesqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	state "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/pkg/nanoid"
)

var (
	testAutomationJobA = nanoid.MustFromSeed(
		state.AutomationJobIDPrefix,
		"daily-headlines",
		"AutomationJobSeedValue123",
	)
	testAutomationJobB = nanoid.MustFromSeed(
		state.AutomationJobIDPrefix,
		"weekly-maintenance",
		"AutomationJobSeedValue123",
	)
	testAutomationJobC = nanoid.MustFromSeed(
		state.AutomationJobIDPrefix,
		"disabled-report",
		"AutomationJobSeedValue123",
	)
	testAutomationJobD = nanoid.MustFromSeed(
		state.AutomationJobIDPrefix,
		"ad-hoc-report",
		"AutomationJobSeedValue123",
	)
	testAutomationJobE = nanoid.MustFromSeed(
		state.AutomationJobIDPrefix,
		"status-check",
		"AutomationJobSeedValue123",
	)
	testAutomationRunA = nanoid.MustFromSeed(
		state.AutomationRunIDPrefix,
		"daily-headlines-run",
		"AutomationRunSeedValue123",
	)
	testAutomationRunB = nanoid.MustFromSeed(
		state.AutomationRunIDPrefix,
		"daily-headlines-run-two",
		"AutomationRunSeedValue123",
	)
	testAutomationRunC = nanoid.MustFromSeed(
		state.AutomationRunIDPrefix,
		"daily-headlines-run-three",
		"AutomationRunSeedValue123",
	)
	testAutomationRunMissing = nanoid.MustFromSeed(
		state.AutomationRunIDPrefix,
		"missing-run",
		"AutomationRunSeedValue123",
	)
)

func TestSQLiteStore_AutomationJobLifecycle(t *testing.T) {
	store := newAutomationSQLiteStore(t)
	ctx := context.Background()
	firstRun := time.Date(2026, 7, 5, 9, 0, 0, 0, time.UTC)
	secondRun := time.Date(2026, 7, 6, 9, 0, 0, 0, time.UTC)

	created, err := store.CreateJob(ctx, state.AutomationJob{
		ID:      " " + testAutomationJobB + " ",
		Name:    "Weekly maintenance",
		Enabled: true,
		Profile: "default",
		Payload: state.AutomationPayload{Kind: state.AutomationPayloadPrompt, Metadata: map[string]string{"scope": "weekly"}},
		Delivery: state.AutomationDelivery{
			Mode:            state.AutomationDeliveryLocal,
			Channel:         "chat",
			FailureTarget:   "ops",
			FailureAfter:    2,
			FailureCooldown: time.Hour,
		},
		State: state.AutomationJobState{
			NextRunAt:           secondRun,
			LastStatus:          state.AutomationRunStatusOK,
			LastFailureNoticeAt: firstRun,
		},
		CreatedAt:   time.Date(2026, 7, 1, 9, 0, 0, 0, time.FixedZone("WAT", 3600)),
		Description: "Run maintenance",
		Authorization: state.AutomationAuthorizationProvenance{
			ActorKind: "local_owner", ActorID: "owner", Surface: "tui", CapturedAt: firstRun,
		},
	})
	require.NoError(t, err)
	require.Equal(t, testAutomationJobB, created.ID)
	require.Equal(t, time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC), created.CreatedAt)
	require.False(t, created.UpdatedAt.IsZero())

	var persisted automationJobModel
	require.NoError(t, store.db.First(&persisted, "id = ?", testAutomationJobB).Error)
	require.NotNil(t, persisted.NextRunAt)
	require.Equal(t, secondRun, persisted.NextRunAt.UTC())

	created.Payload.Metadata["scope"] = "mutated"
	loaded, ok, err := store.GetJob(ctx, testAutomationJobB)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "weekly", loaded.Payload.Metadata["scope"])
	require.Equal(t, state.AutomationDeliveryLocal, loaded.Delivery.Mode)
	require.Equal(t, "ops", loaded.Delivery.FailureTarget)
	require.Equal(t, 2, loaded.Delivery.FailureAfter)
	require.Equal(t, time.Hour, loaded.Delivery.FailureCooldown)
	require.Equal(t, state.AutomationRunStatusOK, loaded.State.LastStatus)
	require.Equal(t, firstRun, loaded.State.LastFailureNoticeAt)
	require.Equal(t, "local_owner", loaded.Authorization.ActorKind)
	require.Equal(t, "owner", loaded.Authorization.ActorID)
	require.Equal(t, firstRun, loaded.Authorization.CapturedAt)

	_, err = store.CreateJob(ctx, state.AutomationJob{ID: testAutomationJobB, Enabled: true})
	require.Error(t, err)

	_, err = store.CreateJob(ctx, state.AutomationJob{ID: "bad", Enabled: true})
	require.EqualError(t, err, "automation job id must be a valid auto_ nanoid")

	_, err = store.CreateJob(ctx, state.AutomationJob{
		ID:            testAutomationJobA,
		Name:          "Daily headlines",
		Enabled:       true,
		Profile:       "default",
		SessionTarget: "chat",
		State:         state.AutomationJobState{NextRunAt: firstRun},
	})
	require.NoError(t, err)

	_, err = store.CreateJob(ctx, state.AutomationJob{
		ID:      testAutomationJobC,
		Name:    "Disabled report",
		Enabled: false,
		Profile: "default",
	})
	require.NoError(t, err)

	generated, err := store.CreateJob(ctx, state.AutomationJob{Enabled: true})
	require.NoError(t, err)
	require.NoError(t, state.ValidateAutomationJobID(generated.ID))
	require.False(t, generated.CreatedAt.IsZero())

	_, err = store.CreateJob(ctx, state.AutomationJob{
		ID:      testAutomationJobD,
		Name:    "Ad hoc report",
		Enabled: true,
	})
	require.NoError(t, err)

	_, err = store.CreateJob(ctx, state.AutomationJob{
		ID:      testAutomationJobE,
		Name:    "Status check",
		Enabled: true,
	})
	require.NoError(t, err)

	list, err := store.ListJobs(ctx, state.AutomationJobQuery{})
	require.NoError(t, err)
	require.Subset(t, automationJobIDs(list.Jobs), []string{testAutomationJobA, testAutomationJobB})
	require.Equal(t, []string{testAutomationJobA, testAutomationJobB}, automationJobIDs(list.Jobs)[:2])

	list, err = store.ListJobs(ctx, state.AutomationJobQuery{IncludeDisabled: true, Limit: 2})
	require.NoError(t, err)
	require.Equal(t, []string{testAutomationJobA, testAutomationJobB}, automationJobIDs(list.Jobs))

	enabled := false
	list, err = store.ListJobs(ctx, state.AutomationJobQuery{Enabled: &enabled})
	require.NoError(t, err)
	require.Equal(t, []string{testAutomationJobC}, automationJobIDs(list.Jobs))

	list, err = store.ListJobs(ctx, state.AutomationJobQuery{Profile: "default", SessionTarget: "chat"})
	require.NoError(t, err)
	require.Equal(t, []string{testAutomationJobA}, automationJobIDs(list.Jobs))

	list, err = store.ListJobs(ctx, state.AutomationJobQuery{IDs: []string{" ", testAutomationJobB}})
	require.NoError(t, err)
	require.Equal(t, []string{testAutomationJobB}, automationJobIDs(list.Jobs))

	list, err = store.ListJobs(ctx, state.AutomationJobQuery{IDs: []string{testAutomationJobC}})
	require.NoError(t, err)
	require.Empty(t, list.Jobs)

	list, err = store.ListJobs(ctx, state.AutomationJobQuery{Profile: "missing"})
	require.NoError(t, err)
	require.Empty(t, list.Jobs)

	list, err = store.ListJobs(ctx, state.AutomationJobQuery{SessionTarget: "missing"})
	require.NoError(t, err)
	require.Empty(t, list.Jobs)

	_, err = store.ListJobs(ctx, state.AutomationJobQuery{IDs: []string{"bad"}})
	require.EqualError(t, err, "automation job id must be a valid auto_ nanoid")

	name := "Daily local headlines"
	patched, err := store.PatchJob(ctx, state.AutomationJobPatch{
		ID:   testAutomationJobA,
		Name: &name,
	})
	require.NoError(t, err)
	require.Equal(t, "Daily local headlines", patched.Name)
	require.False(t, patched.UpdatedAt.IsZero())

	_, err = store.PatchJob(ctx, state.AutomationJobPatch{ID: "bad"})
	require.EqualError(t, err, "automation job id must be a valid auto_ nanoid")

	_, err = store.PatchJob(ctx, state.AutomationJobPatch{ID: testAutomationJobC})
	require.NoError(t, err)

	require.NoError(t, store.DeleteJob(ctx, testAutomationJobC))
	_, ok, err = store.GetJob(ctx, testAutomationJobC)
	require.NoError(t, err)
	require.False(t, ok)

	_, err = store.PatchJob(ctx, state.AutomationJobPatch{ID: testAutomationJobC})
	require.EqualError(t, err, "automation job not found")

	_, _, err = store.GetJob(ctx, "bad")
	require.EqualError(t, err, "automation job id must be a valid auto_ nanoid")

	require.NoError(t, store.DeleteJob(ctx, testAutomationJobC))
	require.EqualError(t, store.DeleteJob(ctx, "bad"), "automation job id must be a valid auto_ nanoid")
}

func TestSQLiteStore_AutomationRunLifecycle(t *testing.T) {
	startedAt := time.Date(2026, 7, 5, 9, 0, 0, 0, time.UTC)
	endedAt := startedAt.Add(42 * time.Second)

	newStoreWithJob := func(t *testing.T) (*Store, context.Context) {
		t.Helper()

		store := newAutomationSQLiteStore(t)
		ctx := context.Background()
		_, err := store.CreateJob(ctx, state.AutomationJob{
			ID:      testAutomationJobA,
			Name:    "Daily headlines",
			Enabled: true,
		})
		require.NoError(t, err)

		return store, ctx
	}

	seedRuns := func(t *testing.T, store *Store, ctx context.Context) string {
		t.Helper()

		_, err := store.CreateRun(ctx, state.AutomationRun{
			ID:        testAutomationRunA,
			JobID:     testAutomationJobA,
			Status:    state.AutomationRunStatusOK,
			StartedAt: startedAt,
		})
		require.NoError(t, err)
		_, err = store.CreateRun(ctx, state.AutomationRun{
			ID:        testAutomationRunB,
			JobID:     testAutomationJobA,
			Status:    state.AutomationRunStatusSkipped,
			StartedAt: startedAt.Add(time.Minute),
		})
		require.NoError(t, err)
		_, err = store.CreateRun(ctx, state.AutomationRun{
			ID:        testAutomationRunC,
			JobID:     testAutomationJobA,
			Status:    state.AutomationRunStatusSkipped,
			StartedAt: startedAt.Add(time.Minute),
		})
		require.NoError(t, err)
		generated, err := store.CreateRun(ctx, state.AutomationRun{
			JobID:     testAutomationJobA,
			StartedAt: startedAt.Add(2 * time.Minute),
		})
		require.NoError(t, err)

		return generated.ID
	}

	t.Run("create validates ids and job existence", func(t *testing.T) {
		store, ctx := newStoreWithJob(t)

		run, err := store.CreateRun(ctx, state.AutomationRun{
			ID:        " " + testAutomationRunA + " ",
			JobID:     " " + testAutomationJobA + " ",
			StartedAt: startedAt,
		})
		require.NoError(t, err)
		require.Equal(t, testAutomationRunA, run.ID)
		require.Equal(t, testAutomationJobA, run.JobID)
		require.Equal(t, state.AutomationRunStatusRunning, run.Status)
		require.Equal(t, startedAt, run.StartedAt)

		_, err = store.CreateRun(ctx, state.AutomationRun{ID: testAutomationRunA, JobID: testAutomationJobA})
		require.Error(t, err)

		_, err = store.CreateRun(ctx, state.AutomationRun{ID: testAutomationRunB, JobID: testAutomationJobB})
		require.EqualError(t, err, "automation job not found")

		generated, err := store.CreateRun(ctx, state.AutomationRun{
			JobID:     testAutomationJobA,
			StartedAt: startedAt.Add(2 * time.Minute),
		})
		require.NoError(t, err)
		require.NoError(t, state.ValidateAutomationRunID(generated.ID))
		require.Equal(t, state.AutomationRunStatusRunning, generated.Status)
		require.Equal(t, startedAt.Add(2*time.Minute), generated.StartedAt)

		_, err = store.CreateRun(ctx, state.AutomationRun{ID: "bad", JobID: testAutomationJobA})
		require.EqualError(t, err, "automation run id must be a valid autorun_ nanoid")

		_, err = store.CreateRun(ctx, state.AutomationRun{ID: testAutomationRunB, JobID: "bad"})
		require.EqualError(t, err, "automation job id must be a valid auto_ nanoid")
	})

	t.Run("finish records completion metadata", func(t *testing.T) {
		store, ctx := newStoreWithJob(t)
		_, err := store.CreateRun(ctx, state.AutomationRun{
			ID:        testAutomationRunA,
			JobID:     testAutomationJobA,
			StartedAt: startedAt,
		})
		require.NoError(t, err)

		usage := state.AutomationUsage{InputTokens: 11, OutputTokens: 13, TotalTokens: 24}
		finished, err := store.FinishRun(ctx, state.AutomationRunPatch{
			ID:             testAutomationRunA,
			Status:         state.AutomationRunStatusOK,
			EndedAt:        endedAt,
			Output:         "done",
			SessionID:      "ses_projectaprojectaproje",
			DeliveryStatus: state.AutomationDeliveryStatusDelivered,
			Model:          "gpt-test",
			Provider:       "openai",
			Usage:          &usage,
		})
		require.NoError(t, err)
		require.Equal(t, state.AutomationRunStatusOK, finished.Status)
		require.Equal(t, endedAt, finished.EndedAt)
		require.Equal(t, 42*time.Second, finished.Duration)
		require.Equal(t, "done", finished.Output)
		require.Equal(t, "ses_projectaprojectaproje", finished.SessionID)
		require.Equal(t, state.AutomationDeliveryStatusDelivered, finished.DeliveryStatus)
		require.Equal(t, "gpt-test", finished.Model)
		require.Equal(t, "openai", finished.Provider)
		require.Equal(t, usage, finished.Usage)

		_, err = store.FinishRun(ctx, state.AutomationRunPatch{ID: testAutomationRunB})
		require.EqualError(t, err, "automation run not found")

		_, err = store.FinishRun(ctx, state.AutomationRunPatch{ID: "bad"})
		require.EqualError(t, err, "automation run id must be a valid autorun_ nanoid")
	})

	t.Run("list filters and sorts runs", func(t *testing.T) {
		store, ctx := newStoreWithJob(t)
		generatedID := seedRuns(t, store, ctx)

		list, err := store.ListRuns(ctx, state.AutomationRunQuery{JobID: testAutomationJobA})
		require.NoError(t, err)
		require.Equal(t, []string{generatedID, testAutomationRunC, testAutomationRunB, testAutomationRunA}, automationRunIDs(list.Runs))

		list, err = store.ListRuns(ctx, state.AutomationRunQuery{
			IDs:    []string{testAutomationRunA},
			Status: []state.AutomationRunStatus{state.AutomationRunStatusOK},
			Limit:  1,
		})
		require.NoError(t, err)
		require.Equal(t, []string{testAutomationRunA}, automationRunIDs(list.Runs))

		list, err = store.ListRuns(ctx, state.AutomationRunQuery{IDs: []string{testAutomationRunMissing}})
		require.NoError(t, err)
		require.Empty(t, list.Runs)

		list, err = store.ListRuns(ctx, state.AutomationRunQuery{Status: []state.AutomationRunStatus{state.AutomationRunStatusError}})
		require.NoError(t, err)
		require.Empty(t, list.Runs)

		list, err = store.ListRuns(ctx, state.AutomationRunQuery{Limit: 1})
		require.NoError(t, err)
		require.Len(t, list.Runs, 1)

		_, err = store.ListRuns(ctx, state.AutomationRunQuery{JobID: "bad"})
		require.EqualError(t, err, "automation job id must be a valid auto_ nanoid")

		_, err = store.ListRuns(ctx, state.AutomationRunQuery{IDs: []string{"bad"}})
		require.EqualError(t, err, "automation run id must be a valid autorun_ nanoid")
	})

	t.Run("delete filters runs and requires safe query", func(t *testing.T) {
		store, ctx := newStoreWithJob(t)
		generatedID := seedRuns(t, store, ctx)

		deleted, err := store.DeleteRuns(ctx, state.AutomationRunDeleteQuery{
			JobID:         testAutomationJobA,
			StartedBefore: startedAt.Add(90 * time.Second),
			Status:        []state.AutomationRunStatus{state.AutomationRunStatusSkipped},
			Limit:         1,
		})
		require.NoError(t, err)
		require.Equal(t, 1, deleted)
		list, err := store.ListRuns(ctx, state.AutomationRunQuery{JobID: testAutomationJobA})
		require.NoError(t, err)
		require.Equal(t, []string{generatedID, testAutomationRunB, testAutomationRunA}, automationRunIDs(list.Runs))

		deleted, err = store.DeleteRuns(ctx, state.AutomationRunDeleteQuery{IDs: []string{testAutomationRunA}})
		require.NoError(t, err)
		require.Equal(t, 1, deleted)
		list, err = store.ListRuns(ctx, state.AutomationRunQuery{IDs: []string{testAutomationRunA}})
		require.NoError(t, err)
		require.Empty(t, list.Runs)

		_, err = store.DeleteRuns(ctx, state.AutomationRunDeleteQuery{JobID: "bad"})
		require.EqualError(t, err, "automation job id must be a valid auto_ nanoid")

		_, err = store.DeleteRuns(ctx, state.AutomationRunDeleteQuery{IDs: []string{"bad"}})
		require.EqualError(t, err, "automation run id must be a valid autorun_ nanoid")

		_, err = store.DeleteRuns(ctx, state.AutomationRunDeleteQuery{})
		require.EqualError(t, err, "automation run delete query requires a filter")
	})
}

func TestSQLiteStore_AutomationCorruptJSONErrors(t *testing.T) {
	store := newAutomationSQLiteStore(t)
	ctx := context.Background()

	record := automationJobModel{
		ID:           testAutomationJobA,
		Enabled:      true,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
		ScheduleJSON: "{}",
		PayloadJSON:  "{}",
		DeliveryJSON: "{}",
		StateJSON:    "{}",
	}
	require.NoError(t, store.db.Create(&record).Error)

	require.NoError(t, store.db.Model(&automationJobModel{}).
		Where("id = ?", testAutomationJobA).
		Update("schedule_json", "{").Error)
	_, _, err := store.GetJob(ctx, testAutomationJobA)
	require.ErrorContains(t, err, "unexpected end of JSON input")

	require.NoError(t, store.db.Model(&automationJobModel{}).
		Where("id = ?", testAutomationJobA).
		Updates(map[string]any{"schedule_json": "{}", "payload_json": "{"}).Error)
	_, err = store.ListJobs(ctx, state.AutomationJobQuery{})
	require.ErrorContains(t, err, "unexpected end of JSON input")

	require.NoError(t, store.db.Model(&automationJobModel{}).
		Where("id = ?", testAutomationJobA).
		Updates(map[string]any{"payload_json": "{}", "delivery_json": "{"}).Error)
	_, err = store.PatchJob(ctx, state.AutomationJobPatch{ID: testAutomationJobA})
	require.ErrorContains(t, err, "unexpected end of JSON input")

	require.NoError(t, store.db.Model(&automationJobModel{}).
		Where("id = ?", testAutomationJobA).
		Updates(map[string]any{"delivery_json": "{}", "state_json": "{"}).Error)
	_, err = store.ListJobs(ctx, state.AutomationJobQuery{})
	require.ErrorContains(t, err, "unexpected end of JSON input")

	runRecord := automationRunModel{
		ID:        testAutomationRunA,
		JobID:     testAutomationJobA,
		Status:    string(state.AutomationRunStatusRunning),
		StartedAt: time.Now().UTC(),
		UsageJSON: "{",
	}
	require.NoError(t, store.db.Create(&runRecord).Error)

	_, err = store.FinishRun(ctx, state.AutomationRunPatch{ID: testAutomationRunA})
	require.ErrorContains(t, err, "unexpected end of JSON input")

	_, err = store.ListRuns(ctx, state.AutomationRunQuery{})
	require.ErrorContains(t, err, "unexpected end of JSON input")
}

func TestSQLiteStore_AutomationClosedDBErrors(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)
	require.NoError(t, store.Close())

	ctx := context.Background()

	_, _, err = store.GetJob(ctx, testAutomationJobA)
	require.Error(t, err)

	_, err = store.ListJobs(ctx, state.AutomationJobQuery{})
	require.Error(t, err)

	_, err = store.PatchJob(ctx, state.AutomationJobPatch{ID: testAutomationJobA})
	require.Error(t, err)

	_, err = store.CreateRun(ctx, state.AutomationRun{
		ID:    testAutomationRunA,
		JobID: testAutomationJobA,
	})
	require.Error(t, err)

	_, err = store.FinishRun(ctx, state.AutomationRunPatch{ID: testAutomationRunA})
	require.Error(t, err)

	_, err = store.ListRuns(ctx, state.AutomationRunQuery{})
	require.Error(t, err)
}

func TestSQLiteStore_AutomationNilStoreErrors(t *testing.T) {
	var store *Store
	ctx := context.Background()

	_, err := store.CreateJob(ctx, state.AutomationJob{})
	require.EqualError(t, err, "store is required")

	_, _, err = store.GetJob(ctx, testAutomationJobA)
	require.EqualError(t, err, "store is required")

	_, err = store.ListJobs(ctx, state.AutomationJobQuery{})
	require.EqualError(t, err, "store is required")

	_, err = store.PatchJob(ctx, state.AutomationJobPatch{ID: testAutomationJobA})
	require.EqualError(t, err, "store is required")

	require.EqualError(t, store.DeleteJob(ctx, testAutomationJobA), "store is required")

	_, err = store.CreateRun(ctx, state.AutomationRun{})
	require.EqualError(t, err, "store is required")

	_, err = store.FinishRun(ctx, state.AutomationRunPatch{ID: testAutomationRunA})
	require.EqualError(t, err, "store is required")

	_, err = store.ListRuns(ctx, state.AutomationRunQuery{})
	require.EqualError(t, err, "store is required")
}

func newAutomationSQLiteStore(t *testing.T) *Store {
	t.Helper()

	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})

	return store
}

func automationJobIDs(jobs []state.AutomationJob) []string {
	ids := make([]string, 0, len(jobs))
	for _, job := range jobs {
		ids = append(ids, job.ID)
	}

	return ids
}

func automationRunIDs(runs []state.AutomationRun) []string {
	ids := make([]string, 0, len(runs))
	for _, run := range runs {
		ids = append(ids, run.ID)
	}

	return ids
}
