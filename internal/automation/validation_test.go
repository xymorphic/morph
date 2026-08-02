package automation

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/state/storememory"
)

func TestService_AddNormalizesAutomationDefinition(t *testing.T) {
	clock := newAutomationTestClock(time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC))
	service := newAutomationTestService(t, storememory.NewStore(), clock, &automationRunnerStub{})

	job, err := service.Add(context.Background(), Job{
		ID:          testServiceJobA,
		Name:        " Daily summary ",
		Description: " Summarize activity ",
		Enabled:     true,
		Schedule: Schedule{
			Kind:  ScheduleKind(" EVERY "),
			Every: time.Hour,
		},
		Payload: Payload{
			Kind:       PayloadKind(" PROMPT "),
			Prompt:     " Summarize ",
			Provider:   " OpenAI ",
			ToolGroups: []string{" Memory ", "memory"},
		},
		Delivery: Delivery{
			Mode:     DeliveryMode(" GATEWAY "),
			Channel:  " Slack ",
			Target:   " C1 ",
			ThreadID: " 123.456 ",
		},
		Profile:       " Work ",
		SessionTarget: " main ",
	})

	require.NoError(t, err)
	require.Equal(t, "Daily summary", job.Name)
	require.Equal(t, "Summarize activity", job.Description)
	require.Equal(t, ScheduleEvery, job.Schedule.Kind)
	require.Equal(t, PayloadPrompt, job.Payload.Kind)
	require.Equal(t, "Summarize", job.Payload.Prompt)
	require.Equal(t, "openai", job.Payload.Provider)
	require.Equal(t, []string{"memory"}, job.Payload.ToolGroups)
	require.Equal(t, DeliveryGateway, job.Delivery.Mode)
	require.Equal(t, "slack", job.Delivery.Channel)
	require.Equal(t, "C1", job.Delivery.Target)
	require.Equal(t, "123.456", job.Delivery.ThreadID)
	require.Equal(t, "work", job.Profile)
	require.Equal(t, SessionTargetMain, job.SessionTarget)
	require.Equal(t, clock.Now().Add(time.Hour), job.State.NextRunAt)
}

func TestService_AddRejectsInvalidAutomationDefinitionBeforePersistence(t *testing.T) {
	clock := newAutomationTestClock(time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC))
	tests := []struct {
		name   string
		mutate func(*Job)
		err    string
	}{
		{
			name: "invalid job id",
			mutate: func(job *Job) {
				job.ID = "bad"
			},
			err: "automation job id must be a valid auto_ nanoid",
		},
		{
			name: "unsupported schedule kind",
			mutate: func(job *Job) {
				job.Schedule.Kind = ScheduleKind("sometimes")
			},
			err: `unsupported automation schedule kind "sometimes"`,
		},
		{
			name: "conflicting schedule fields",
			mutate: func(job *Job) {
				job.Schedule.At = clock.Now()
			},
			err: "automation interval schedule cannot include one-shot or cron fields",
		},
		{
			name: "missing one-shot time",
			mutate: func(job *Job) {
				job.Schedule = Schedule{Kind: ScheduleAt}
			},
			err: "automation one-shot schedule time is required",
		},
		{
			name: "conflicting one-shot fields",
			mutate: func(job *Job) {
				job.Schedule = Schedule{Kind: ScheduleAt, At: clock.Now(), Every: time.Minute}
			},
			err: "automation one-shot schedule cannot include interval or cron fields",
		},
		{
			name: "missing cron expression",
			mutate: func(job *Job) {
				job.Schedule = Schedule{Kind: ScheduleCron}
			},
			err: "automation cron schedule expression is required",
		},
		{
			name: "conflicting cron fields",
			mutate: func(job *Job) {
				job.Schedule = Schedule{Kind: ScheduleCron, Cron: "0 8 * * *", Every: time.Hour}
			},
			err: "automation cron schedule cannot include one-shot or interval fields",
		},
		{
			name: "missing prompt",
			mutate: func(job *Job) {
				job.Payload.Prompt = ""
			},
			err: "automation prompt payload is required",
		},
		{
			name: "conflicting payload fields",
			mutate: func(job *Job) {
				job.Payload.SystemEvent = "wake"
			},
			err: "automation prompt payload cannot include a system event",
		},
		{
			name: "missing system event",
			mutate: func(job *Job) {
				job.Payload = Payload{Kind: PayloadSystemEvent}
			},
			err: "automation system event payload is required",
		},
		{
			name: "system event with prompt",
			mutate: func(job *Job) {
				job.Payload = Payload{Kind: PayloadSystemEvent, SystemEvent: "wake", Prompt: "conflict"}
			},
			err: "automation system event payload cannot include a prompt",
		},
		{
			name: "unsupported payload kind",
			mutate: func(job *Job) {
				job.Payload.Kind = PayloadKind("unknown")
			},
			err: `unsupported automation payload kind "unknown"`,
		},
		{
			name: "negative payload policy",
			mutate: func(job *Job) {
				job.Payload.RetryAttempts = -1
			},
			err: "automation retry attempts must be non-negative",
		},
		{
			name: "unsupported delivery mode",
			mutate: func(job *Job) {
				job.Delivery.Mode = DeliveryMode("n")
			},
			err: `unsupported automation delivery mode "n"`,
		},
		{
			name: "none delivery with route",
			mutate: func(job *Job) {
				job.Delivery.Target = "main"
			},
			err: "automation none delivery cannot include routing fields",
		},
		{
			name: "gateway delivery without channel",
			mutate: func(job *Job) {
				job.Delivery = Delivery{Mode: DeliveryGateway, Target: "C1"}
			},
			err: "automation gateway delivery channel is required",
		},
		{
			name: "gateway delivery without target",
			mutate: func(job *Job) {
				job.Delivery = Delivery{Mode: DeliveryGateway, Channel: "slack"}
			},
			err: "automation gateway delivery target is required",
		},
		{
			name: "gateway delivery with webhook URL",
			mutate: func(job *Job) {
				job.Delivery = Delivery{
					Mode:       DeliveryGateway,
					Channel:    "slack",
					Target:     "C1",
					WebhookURL: "https://example.com/hook",
				}
			},
			err: "automation gateway delivery cannot include a webhook URL",
		},
		{
			name: "origin delivery without captured route",
			mutate: func(job *Job) {
				job.Delivery = Delivery{Mode: DeliveryOrigin}
			},
			err: "automation origin delivery channel is required",
		},
		{
			name: "origin delivery without target",
			mutate: func(job *Job) {
				job.Delivery = Delivery{Mode: DeliveryOrigin, Channel: "slack"}
			},
			err: "automation origin delivery target is required",
		},
		{
			name: "origin delivery with webhook URL",
			mutate: func(job *Job) {
				job.Delivery = Delivery{
					Mode:       DeliveryOrigin,
					Channel:    "slack",
					Target:     "C1",
					WebhookURL: "https://example.com/hook",
				}
			},
			err: "automation origin delivery cannot include a webhook URL",
		},
		{
			name: "webhook delivery without URL",
			mutate: func(job *Job) {
				job.Delivery = Delivery{Mode: DeliveryWebhook}
			},
			err: "automation webhook URL is required",
		},
		{
			name: "webhook delivery with invalid URL",
			mutate: func(job *Job) {
				job.Delivery = Delivery{Mode: DeliveryWebhook, WebhookURL: "ftp://example.com/hook"}
			},
			err: "automation webhook URL must be an absolute HTTP or HTTPS URL",
		},
		{
			name: "webhook delivery with gateway route",
			mutate: func(job *Job) {
				job.Delivery = Delivery{
					Mode:       DeliveryWebhook,
					Channel:    "slack",
					WebhookURL: "https://example.com/hook",
				}
			},
			err: "automation webhook delivery cannot include gateway routing fields",
		},
		{
			name: "malformed webhook URL",
			mutate: func(job *Job) {
				job.Delivery = Delivery{Mode: DeliveryWebhook, WebhookURL: "://bad"}
			},
			err: "automation webhook URL must be an absolute HTTP or HTTPS URL",
		},
		{
			name: "negative delivery threshold",
			mutate: func(job *Job) {
				job.Delivery.FailureAfter = -1
			},
			err: "automation delivery failure threshold must be non-negative",
		},
		{
			name: "negative delivery cooldown",
			mutate: func(job *Job) {
				job.Delivery.FailureCooldown = -time.Second
			},
			err: "automation delivery failure cooldown must be non-negative",
		},
		{
			name: "invalid profile",
			mutate: func(job *Job) {
				job.Profile = "work/team"
			},
			err: `invalid profile name "work/team": must match [a-zA-Z0-9][a-zA-Z0-9_-]{0,63}`,
		},
		{
			name: "invalid session target",
			mutate: func(job *Job) {
				job.SessionTarget = "workspace"
			},
			err: `unsupported automation session target "workspace"`,
		},
		{
			name: "invalid named session target",
			mutate: func(job *Job) {
				job.SessionTarget = "session:bad"
			},
			err: "session id must be a valid ses_ nanoid",
		},
		{
			name: "invalid run status",
			mutate: func(job *Job) {
				job.State.LastStatus = RunStatus("done")
			},
			err: `unsupported automation run status "done"`,
		},
		{
			name: "negative last duration",
			mutate: func(job *Job) {
				job.State.LastDuration = -time.Second
			},
			err: "automation last duration must be non-negative",
		},
		{
			name: "negative consecutive errors",
			mutate: func(job *Job) {
				job.State.ConsecutiveErrors = -1
			},
			err: "automation consecutive errors must be non-negative",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := storememory.NewStore()
			service := newAutomationTestService(t, store, clock, &automationRunnerStub{})
			job := validMutationTestJob(clock.Now())
			test.mutate(&job)

			_, err := service.Add(context.Background(), job)

			require.EqualError(t, err, test.err)
			list, listErr := store.ListJobs(context.Background(), JobQuery{IncludeDisabled: true})
			require.NoError(t, listErr)
			require.Empty(t, list.Jobs)
		})
	}
}

func TestService_UpdateNormalizesCompleteAutomationMutation(t *testing.T) {
	clock := newAutomationTestClock(time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC))
	store := storememory.NewStore()
	service := newAutomationTestService(t, store, clock, &automationRunnerStub{})
	original := validMutationTestJob(clock.Now())
	_, err := service.Add(context.Background(), original)
	require.NoError(t, err)

	name := " Updated job "
	description := " Updated description "
	enabled := true
	profileName := " Work "
	sessionTarget := " session:ses_projectaprojectaproje "
	deleteAfterRun := true
	schedule := Schedule{Kind: ScheduleKind(" EVERY "), Every: 2 * time.Hour}
	payload := Payload{
		Kind:       PayloadKind(" PROMPT "),
		Prompt:     " Next run ",
		Provider:   " OpenAI ",
		ToolGroups: []string{" Memory ", "memory"},
	}
	delivery := Delivery{Mode: DeliveryMode(" WEBHOOK "), WebhookURL: "HTTPS://example.com/hook"}
	jobState := JobState{
		LastStatus:        RunStatusOK,
		LastDuration:      time.Second,
		ConsecutiveErrors: 2,
	}

	updated, err := service.Update(context.Background(), JobPatch{
		ID:             original.ID,
		Name:           &name,
		Description:    &description,
		Enabled:        &enabled,
		Schedule:       &schedule,
		Payload:        &payload,
		Delivery:       &delivery,
		Profile:        &profileName,
		SessionTarget:  &sessionTarget,
		DeleteAfterRun: &deleteAfterRun,
		State:          &jobState,
	})

	require.NoError(t, err)
	require.Equal(t, "Updated job", updated.Name)
	require.Equal(t, "Updated description", updated.Description)
	require.True(t, updated.Enabled)
	require.Equal(t, ScheduleEvery, updated.Schedule.Kind)
	require.Equal(t, 2*time.Hour, updated.Schedule.Every)
	require.Equal(t, PayloadPrompt, updated.Payload.Kind)
	require.Equal(t, "Next run", updated.Payload.Prompt)
	require.Equal(t, "openai", updated.Payload.Provider)
	require.Equal(t, []string{"memory"}, updated.Payload.ToolGroups)
	require.Equal(t, DeliveryWebhook, updated.Delivery.Mode)
	require.Equal(t, "HTTPS://example.com/hook", updated.Delivery.WebhookURL)
	require.Equal(t, "work", updated.Profile)
	require.Equal(t, "session:ses_projectaprojectaproje", updated.SessionTarget)
	require.True(t, updated.DeleteAfterRun)
	require.Equal(t, RunStatusOK, updated.State.LastStatus)
	require.Equal(t, time.Second, updated.State.LastDuration)
	require.Zero(t, updated.State.ConsecutiveErrors)
	require.Equal(t, clock.Now().Add(2*time.Hour), updated.State.NextRunAt)
}

func TestService_UpdateRejectsInvalidAutomationMutationBeforePersistence(t *testing.T) {
	clock := newAutomationTestClock(time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC))
	tests := []struct {
		name  string
		patch JobPatch
		err   string
	}{
		{
			name:  "unsupported delivery mode",
			patch: JobPatch{Delivery: &Delivery{Mode: DeliveryMode("n")}},
			err:   `unsupported automation delivery mode "n"`,
		},
		{
			name:  "webhook without URL",
			patch: JobPatch{Delivery: &Delivery{Mode: DeliveryWebhook}},
			err:   "automation webhook URL is required",
		},
		{
			name: "invalid payload",
			patch: JobPatch{Payload: &Payload{
				Kind: PayloadPrompt,
			}},
			err: "automation prompt payload is required",
		},
		{
			name:  "invalid session target",
			patch: JobPatch{SessionTarget: new("workspace")},
			err:   `unsupported automation session target "workspace"`,
		},
		{
			name:  "invalid profile",
			patch: JobPatch{Profile: new("work/team")},
			err:   `invalid profile name "work/team": must match [a-zA-Z0-9][a-zA-Z0-9_-]{0,63}`,
		},
		{
			name: "invalid state",
			patch: JobPatch{State: &JobState{
				ConsecutiveErrors: -1,
			}},
			err: "automation consecutive errors must be non-negative",
		},
		{
			name:  "invalid schedule while disabled",
			patch: JobPatch{Schedule: &Schedule{Kind: ScheduleEvery}},
			err:   "automation interval schedule must be greater than zero",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := storememory.NewStore()
			service := newAutomationTestService(t, store, clock, &automationRunnerStub{})
			original := validMutationTestJob(clock.Now())
			_, err := service.Add(context.Background(), original)
			require.NoError(t, err)

			patch := test.patch
			patch.ID = original.ID
			_, err = service.Update(context.Background(), patch)

			require.EqualError(t, err, test.err)
			persisted, ok, getErr := store.GetJob(context.Background(), original.ID)
			require.NoError(t, getErr)
			require.True(t, ok)
			require.Equal(t, original.Delivery, persisted.Delivery)
			require.Equal(t, original.Payload, persisted.Payload)
			require.Equal(t, original.Schedule, persisted.Schedule)
			require.Equal(t, original.Profile, persisted.Profile)
			require.Equal(t, original.SessionTarget, persisted.SessionTarget)
		})
	}
}

func TestService_UpdateValidatesDependentAndEnabledDefinition(t *testing.T) {
	clock := newAutomationTestClock(time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC))

	t.Run("payload update preserves valid origin delivery", func(t *testing.T) {
		store := storememory.NewStore()
		service := newAutomationTestService(t, store, clock, &automationRunnerStub{})
		job := validMutationTestJob(clock.Now())
		job.Payload.Metadata = map[string]string{
			metadataOriginChannel: "slack",
			metadataOriginTarget:  "C1",
		}
		job.Delivery = Delivery{Mode: DeliveryOrigin}
		_, err := service.Add(context.Background(), job)
		require.NoError(t, err)

		payload := Payload{Kind: PayloadPrompt, Prompt: "next"}
		_, err = service.Update(context.Background(), JobPatch{ID: job.ID, Payload: &payload})

		require.EqualError(t, err, "automation origin delivery channel is required")
	})

	t.Run("enabling validates complete legacy definition", func(t *testing.T) {
		store := storememory.NewStore()
		service := newAutomationTestService(t, store, clock, &automationRunnerStub{})
		legacy := validMutationTestJob(clock.Now())
		legacy.Delivery = Delivery{Mode: DeliveryMode("n")}
		_, err := store.CreateJob(context.Background(), legacy)
		require.NoError(t, err)

		enabled := true
		_, err = service.Update(context.Background(), JobPatch{ID: legacy.ID, Enabled: &enabled})

		require.EqualError(t, err, `unsupported automation delivery mode "n"`)
	})

	t.Run("updating enabled job validates complete legacy definition", func(t *testing.T) {
		store := storememory.NewStore()
		service := newAutomationTestService(t, store, clock, &automationRunnerStub{})
		legacy := validMutationTestJob(clock.Now())
		legacy.Enabled = true
		legacy.Delivery = Delivery{Mode: DeliveryMode("n")}
		_, err := store.CreateJob(context.Background(), legacy)
		require.NoError(t, err)

		name := "renamed"
		_, err = service.Update(context.Background(), JobPatch{ID: legacy.ID, Name: &name})

		require.EqualError(t, err, `unsupported automation delivery mode "n"`)
	})
}

func validMutationTestJob(now time.Time) Job {
	return Job{
		ID:      testServiceJobA,
		Enabled: false,
		Schedule: Schedule{
			Kind:  ScheduleEvery,
			Every: time.Hour,
		},
		Payload: Payload{
			Kind:   PayloadPrompt,
			Prompt: "summarize",
		},
		Delivery: Delivery{Mode: DeliveryNone},
		State: JobState{
			NextRunAt: now.Add(time.Hour),
		},
	}
}
