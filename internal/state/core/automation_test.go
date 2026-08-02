package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/pkg/nanoid"
)

var (
	testAutomationJobID = nanoid.MustFromSeed(
		AutomationJobIDPrefix,
		"daily-headlines",
		"AutomationJobSeedValue123",
	)
	testAutomationRunID = nanoid.MustFromSeed(
		AutomationRunIDPrefix,
		"daily-headlines-run",
		"AutomationRunSeedValue123",
	)
)

func TestValidateAutomationIDs(t *testing.T) {
	require.NoError(t, ValidateAutomationJobID(testAutomationJobID))
	require.NoError(t, ValidateAutomationRunID(testAutomationRunID))

	require.EqualError(t, ValidateAutomationJobID(""), "automation job id is required")
	require.EqualError(t, ValidateAutomationJobID("job_invalid"), "automation job id must be a valid auto_ nanoid")
	require.EqualError(t, ValidateAutomationRunID(""), "automation run id is required")
	require.EqualError(t, ValidateAutomationRunID("run_invalid"), "automation run id must be a valid autorun_ nanoid")
}

func TestHasAutomationRunDeleteFilter(t *testing.T) {
	require.False(t, HasAutomationRunDeleteFilter(AutomationRunDeleteQuery{}))
	require.True(t, HasAutomationRunDeleteFilter(AutomationRunDeleteQuery{JobID: " " + testAutomationJobID + " "}))
	require.True(t, HasAutomationRunDeleteFilter(AutomationRunDeleteQuery{IDs: []string{testAutomationRunID}}))
	require.True(t, HasAutomationRunDeleteFilter(AutomationRunDeleteQuery{
		StartedBefore: time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC),
	}))
	require.True(t, HasAutomationRunDeleteFilter(AutomationRunDeleteQuery{
		Status: []AutomationRunStatus{AutomationRunStatusOK},
	}))
}

func TestAutomationRunStatusHelpers(t *testing.T) {
	statusSet := AutomationRunStatusSet([]AutomationRunStatus{
		"",
		AutomationRunStatusOK,
		AutomationRunStatusError,
	})

	_, hasEmpty := statusSet[""]
	_, hasOK := statusSet[AutomationRunStatusOK]
	_, hasError := statusSet[AutomationRunStatusError]
	require.False(t, hasEmpty)
	require.True(t, hasOK)
	require.True(t, hasError)
	require.Equal(t, []string{"ok", "error"}, AutomationRunStatusesToStrings([]AutomationRunStatus{
		"",
		AutomationRunStatusOK,
		AutomationRunStatusError,
	}))
}

func TestAutomationJob_CloneCopiesPayloadMetadata(t *testing.T) {
	job := AutomationJob{
		ID: testAutomationJobID,
		Payload: AutomationPayload{
			ToolGroups: []string{"memory"},
			Metadata:   map[string]string{"topic": "news"},
		},
	}

	cloned := job.Clone()
	cloned.Payload.ToolGroups[0] = "shell"
	cloned.Payload.Metadata["topic"] = "weather"

	require.Equal(t, []string{"memory"}, job.Payload.ToolGroups)
	require.Equal(t, []string{"shell"}, cloned.Payload.ToolGroups)
	require.Equal(t, "news", job.Payload.Metadata["topic"])
	require.Equal(t, "weather", cloned.Payload.Metadata["topic"])
}

func TestApplyAutomationJobPatch(t *testing.T) {
	createdAt := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 7, 2, 9, 0, 0, 0, time.UTC)
	nextRunAt := time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)
	name := "Daily headlines"
	description := "Collect news headlines"
	enabled := false
	schedule := AutomationSchedule{Kind: AutomationScheduleEvery, Every: time.Hour}
	payload := AutomationPayload{
		Kind:          AutomationPayloadPrompt,
		Prompt:        "Summarize headlines",
		NoTimeout:     true,
		MaxRuntime:    time.Minute,
		MaxIterations: 3,
		RetryAttempts: 2,
		RetryBackoff:  time.Second,
		RetryMaxDelay: 5 * time.Second,
		ToolGroups:    []string{"memory"},
		Metadata:      map[string]string{"source": "bbc"},
	}
	delivery := AutomationDelivery{
		Mode:            AutomationDeliveryLocal,
		Channel:         "chat",
		FailureTarget:   "ops",
		FailureAfter:    2,
		FailureCooldown: time.Hour,
	}
	profile := "work"
	sessionTarget := "current"
	deleteAfterRun := true
	state := AutomationJobState{
		NextRunAt:           nextRunAt,
		LastFailureNoticeAt: updatedAt,
	}

	job := ApplyAutomationJobPatch(AutomationJob{
		ID:        testAutomationJobID,
		Enabled:   true,
		CreatedAt: createdAt,
	}, AutomationJobPatch{
		Name:           &name,
		Description:    &description,
		Enabled:        &enabled,
		Schedule:       &schedule,
		Payload:        &payload,
		Delivery:       &delivery,
		Profile:        &profile,
		SessionTarget:  &sessionTarget,
		DeleteAfterRun: &deleteAfterRun,
		State:          &state,
	}, updatedAt)

	payload.Metadata["source"] = "cnn"

	require.Equal(t, testAutomationJobID, job.ID)
	require.Equal(t, createdAt, job.CreatedAt)
	require.Equal(t, updatedAt, job.UpdatedAt)
	require.Equal(t, "Daily headlines", job.Name)
	require.Equal(t, "Collect news headlines", job.Description)
	require.False(t, job.Enabled)
	require.Equal(t, schedule, job.Schedule)
	require.Equal(t, AutomationPayloadPrompt, job.Payload.Kind)
	require.Equal(t, "Summarize headlines", job.Payload.Prompt)
	require.True(t, job.Payload.NoTimeout)
	require.Equal(t, time.Minute, job.Payload.MaxRuntime)
	require.Equal(t, 3, job.Payload.MaxIterations)
	require.Equal(t, 2, job.Payload.RetryAttempts)
	require.Equal(t, time.Second, job.Payload.RetryBackoff)
	require.Equal(t, 5*time.Second, job.Payload.RetryMaxDelay)
	require.Equal(t, []string{"memory"}, job.Payload.ToolGroups)
	require.Equal(t, "bbc", job.Payload.Metadata["source"])
	require.Equal(t, delivery, job.Delivery)
	require.Equal(t, "work", job.Profile)
	require.Equal(t, "current", job.SessionTarget)
	require.True(t, job.DeleteAfterRun)
	require.Equal(t, nextRunAt, job.State.NextRunAt)
	require.Equal(t, updatedAt, job.State.LastFailureNoticeAt)
}

func TestApplyAutomationRunPatch(t *testing.T) {
	startedAt := time.Date(2026, 7, 5, 9, 0, 0, 0, time.UTC)
	endedAt := startedAt.Add(2 * time.Minute)
	usage := AutomationUsage{InputTokens: 3, OutputTokens: 5, TotalTokens: 8}

	run := ApplyAutomationRunPatch(AutomationRun{
		ID:        testAutomationRunID,
		JobID:     testAutomationJobID,
		Status:    AutomationRunStatusRunning,
		StartedAt: startedAt,
	}, AutomationRunPatch{
		ID:             testAutomationRunID,
		Status:         AutomationRunStatusOK,
		EndedAt:        endedAt,
		Output:         "done",
		SessionID:      "  ses_projectaprojectaproje  ",
		DeliveryStatus: AutomationDeliveryStatusDelivered,
		Model:          " gpt-test ",
		Provider:       " openai ",
		Usage:          &usage,
	}, time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC))

	require.Equal(t, AutomationRunStatusOK, run.Status)
	require.Equal(t, endedAt, run.EndedAt)
	require.Equal(t, 2*time.Minute, run.Duration)
	require.Equal(t, "done", run.Output)
	require.Equal(t, "ses_projectaprojectaproje", run.SessionID)
	require.Equal(t, AutomationDeliveryStatusDelivered, run.DeliveryStatus)
	require.Equal(t, "gpt-test", run.Model)
	require.Equal(t, "openai", run.Provider)
	require.Equal(t, usage, run.Usage)

	now := time.Date(2026, 7, 5, 11, 0, 0, 0, time.UTC)
	run = ApplyAutomationRunPatch(AutomationRun{ID: testAutomationRunID}, AutomationRunPatch{}, now)

	require.Equal(t, now, run.StartedAt)
	require.Equal(t, now, run.EndedAt)
	require.Zero(t, run.Duration)
}
