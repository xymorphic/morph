package automation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	coreautomation "github.com/xymorphic/morph/internal/automation"
	"github.com/xymorphic/morph/internal/config"
)

func TestNewCommand_DiagnoseInspectAndRecoverCallRPC(t *testing.T) {
	api, output := setupAutomationCommandTest(t)
	runningAt := time.Now().UTC().Add(-20 * time.Minute)
	api.jobs = []coreautomation.Job{{
		ID:       testAutomationCommandJobID,
		Enabled:  true,
		Schedule: coreautomation.Schedule{Kind: coreautomation.ScheduleEvery, Every: time.Hour},
		Delivery: coreautomation.Delivery{Mode: coreautomation.DeliveryWebhook},
		State:    coreautomation.JobState{RunningAt: runningAt},
	}}

	require.NoError(t, newTestCommand().Run(context.Background(), []string{"automation", "diagnose", "--all"}))
	require.True(t, api.jobQuery.IncludeDisabled)
	require.Contains(t, output.String(), "delivery_webhook_url_missing")

	output.Reset()
	api.runs = []coreautomation.Run{{
		ID:             testAutomationCommandRunID,
		JobID:          testAutomationCommandJobID,
		Status:         coreautomation.RunStatusError,
		Error:          "failed",
		SessionID:      "ses_projectaprojectaproje",
		DeliveryStatus: coreautomation.DeliveryStatusNotDelivered,
		DeliveryError:  "delivery failed",
	}}
	require.NoError(t, newTestCommand().Run(context.Background(), []string{
		"automation", "inspect", testAutomationCommandJobID,
	}))
	require.Equal(t, testAutomationCommandJobID, api.jobQuery.IDs[0])
	require.Contains(t, output.String(), "Trace session:        ses_projectaprojectaproje")
	require.Contains(t, output.String(), "Run ID:               "+testAutomationCommandRunID)

	output.Reset()
	require.NoError(t, newTestCommand().Run(context.Background(), []string{
		"automation", "recover", "recompute-schedules",
	}))
	require.Contains(t, output.String(), "recomputed=1")

	output.Reset()
	require.NoError(t, newTestCommand().Run(context.Background(), []string{
		"automation", "recover", "clear-running", testAutomationCommandJobID,
	}))
	require.NotNil(t, api.patch.State)
	require.True(t, api.patch.State.RunningAt.IsZero())
	require.Contains(t, output.String(), "running=false")

	output.Reset()
	require.NoError(t, newTestCommand().Run(context.Background(), []string{
		"automation", "recover", "rerun-failed", testAutomationCommandJobID,
	}))
	require.Equal(t, []coreautomation.RunStatus{coreautomation.RunStatusError}, api.runQuery.Status)
	require.Contains(t, output.String(), testAutomationCommandRunID)
}

func TestNewCommand_DiagnoseReportsHealthyState(t *testing.T) {
	api, output := setupAutomationCommandTest(t)
	api.jobs = []coreautomation.Job{{
		ID:       testAutomationCommandJobID,
		Enabled:  true,
		Schedule: coreautomation.Schedule{Kind: coreautomation.ScheduleEvery, Every: time.Hour},
		Delivery: coreautomation.Delivery{Mode: coreautomation.DeliveryLocal},
	}}

	require.NoError(t, newTestCommand().Run(context.Background(), []string{"automation", "diagnose"}))

	require.Equal(t, "Automation diagnostics\n  Status:               passed\n", output.String())
}

func TestNewCommand_PropagatesOperationActionErrors(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		mutate func(*automationCommandAPIStub)
	}{
		{
			name: "diagnose rpc",
			args: []string{"automation", "diagnose"},
			mutate: func(api *automationCommandAPIStub) {
				api.listErr = errors.New("diagnose failed")
			},
		},
		{
			name: "diagnose write pass",
			args: []string{"automation", "diagnose"},
			mutate: func(*automationCommandAPIStub) {
				automationOutput = errorWriter{}
			},
		},
		{
			name: "diagnose write finding",
			args: []string{"automation", "diagnose"},
			mutate: func(api *automationCommandAPIStub) {
				api.jobs = []coreautomation.Job{{
					ID:       testAutomationCommandJobID,
					Enabled:  true,
					Schedule: coreautomation.Schedule{Kind: coreautomation.ScheduleEvery, Every: time.Hour},
					Delivery: coreautomation.Delivery{Mode: coreautomation.DeliveryWebhook},
				}}
				automationOutput = errorWriter{}
			},
		},
		{
			name: "inspect missing id",
			args: []string{"automation", "inspect"},
		},
		{
			name: "inspect list rpc",
			args: []string{"automation", "inspect", testAutomationCommandJobID},
			mutate: func(api *automationCommandAPIStub) {
				api.listErr = errors.New("inspect failed")
			},
		},
		{
			name: "inspect missing job",
			args: []string{"automation", "inspect", testAutomationCommandJobID},
			mutate: func(api *automationCommandAPIStub) {
				api.jobs = []coreautomation.Job{}
				api.added = coreautomation.Job{}
			},
		},
		{
			name: "inspect runs rpc",
			args: []string{"automation", "inspect", testAutomationCommandJobID},
			mutate: func(api *automationCommandAPIStub) {
				api.jobs = []coreautomation.Job{{ID: testAutomationCommandJobID}}
				api.runsErr = errors.New("runs failed")
			},
		},
		{
			name: "inspect write",
			args: []string{"automation", "inspect", testAutomationCommandJobID},
			mutate: func(api *automationCommandAPIStub) {
				api.jobs = []coreautomation.Job{{ID: testAutomationCommandJobID}}
				automationOutput = errorWriter{}
			},
		},
		{
			name: "recover recompute list rpc",
			args: []string{"automation", "recover", "recompute-schedules"},
			mutate: func(api *automationCommandAPIStub) {
				api.listErr = errors.New("list failed")
			},
		},
		{
			name: "recover recompute update rpc",
			args: []string{"automation", "recover", "recompute-schedules"},
			mutate: func(api *automationCommandAPIStub) {
				api.jobs = []coreautomation.Job{{ID: testAutomationCommandJobID, Enabled: true}}
				api.updateErr = errors.New("update failed")
			},
		},
		{
			name: "recover recompute write",
			args: []string{"automation", "recover", "recompute-schedules"},
			mutate: func(api *automationCommandAPIStub) {
				api.jobs = []coreautomation.Job{{ID: testAutomationCommandJobID, Enabled: false}}
				automationOutput = errorWriter{}
			},
		},
		{
			name: "recover clear missing id",
			args: []string{"automation", "recover", "clear-running"},
		},
		{
			name: "recover clear list rpc",
			args: []string{"automation", "recover", "clear-running", testAutomationCommandJobID},
			mutate: func(api *automationCommandAPIStub) {
				api.listErr = errors.New("list failed")
			},
		},
		{
			name: "recover clear missing job",
			args: []string{"automation", "recover", "clear-running", testAutomationCommandJobID},
			mutate: func(api *automationCommandAPIStub) {
				api.jobs = []coreautomation.Job{}
				api.added = coreautomation.Job{}
			},
		},
		{
			name: "recover clear update rpc",
			args: []string{"automation", "recover", "clear-running", testAutomationCommandJobID},
			mutate: func(api *automationCommandAPIStub) {
				api.jobs = []coreautomation.Job{{ID: testAutomationCommandJobID}}
				api.updateErr = errors.New("update failed")
			},
		},
		{
			name: "recover clear write",
			args: []string{"automation", "recover", "clear-running", testAutomationCommandJobID},
			mutate: func(api *automationCommandAPIStub) {
				api.jobs = []coreautomation.Job{{ID: testAutomationCommandJobID}}
				automationOutput = errorWriter{}
			},
		},
		{
			name: "recover rerun missing id",
			args: []string{"automation", "recover", "rerun-failed"},
		},
		{
			name: "recover rerun runs rpc",
			args: []string{"automation", "recover", "rerun-failed", testAutomationCommandJobID},
			mutate: func(api *automationCommandAPIStub) {
				api.runsErr = errors.New("runs failed")
			},
		},
		{
			name: "recover rerun no failures",
			args: []string{"automation", "recover", "rerun-failed", testAutomationCommandJobID},
		},
		{
			name: "recover rerun rpc",
			args: []string{"automation", "recover", "rerun-failed", testAutomationCommandJobID},
			mutate: func(api *automationCommandAPIStub) {
				api.runs = []coreautomation.Run{{ID: testAutomationCommandRunID}}
				api.runErr = errors.New("run failed")
			},
		},
		{
			name: "recover rerun write",
			args: []string{"automation", "recover", "rerun-failed", testAutomationCommandJobID},
			mutate: func(api *automationCommandAPIStub) {
				api.runs = []coreautomation.Run{{ID: testAutomationCommandRunID}}
				automationOutput = errorWriter{}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api, _ := setupAutomationCommandTest(t)
			if test.mutate != nil {
				test.mutate(api)
			}

			require.Error(t, newTestCommand().Run(context.Background(), test.args))
		})
	}
}

func TestNewCommand_PropagatesOperationClientCreationErrors(t *testing.T) {
	tests := [][]string{
		{"automation", "diagnose"},
		{"automation", "inspect", testAutomationCommandJobID},
		{"automation", "recover", "recompute-schedules"},
		{"automation", "recover", "clear-running", testAutomationCommandJobID},
		{"automation", "recover", "rerun-failed", testAutomationCommandJobID},
	}

	for _, args := range tests {
		t.Run(args[1], func(t *testing.T) {
			_, _ = setupAutomationCommandTest(t)
			expected := errors.New("client failed")
			newClient = func(context.Context, *config.Config) (automationClient, error) {
				return nil, expected
			}

			err := newTestCommand().Run(context.Background(), args)

			require.ErrorIs(t, err, expected)
		})
	}
}

func TestNewRecoverCommand_ShowsHelpForMissingSubcommand(t *testing.T) {
	_, _ = setupAutomationCommandTest(t)

	require.NoError(t, newTestCommand().Run(context.Background(), []string{"automation", "recover"}))
}

func TestWriteInspection_CoversNoRunAndWriteErrors(t *testing.T) {
	_, output := setupAutomationCommandTest(t)
	require.NoError(t, writeInspection(coreautomation.RunInspection{
		Job: coreautomation.Job{ID: testAutomationCommandJobID},
	}))
	require.Contains(t, output.String(), "Last run\n  Status:               none")
	require.Contains(t, output.String(), "Session target:       isolated (default)")
	require.Contains(t, output.String(), "Mode:                 none (default)")

	automationOutput = errorWriter{}
	err := writeInspection(coreautomation.RunInspection{})
	require.Error(t, err)
}

func TestWriteInspection_OutputsAllJobFields(t *testing.T) {
	_, output := setupAutomationCommandTest(t)
	createdAt := time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(time.Minute)
	nextRunAt := createdAt.Add(time.Hour)
	runningAt := createdAt.Add(2 * time.Minute)
	lastRunAt := createdAt.Add(-time.Hour)
	lastFailureNoticeAt := createdAt.Add(-30 * time.Minute)

	err := writeInspection(coreautomation.RunInspection{
		Job: coreautomation.Job{
			ID:             testAutomationCommandJobID,
			Name:           "Daily summary",
			Description:    "Summarize project activity",
			Enabled:        true,
			CreatedAt:      createdAt,
			UpdatedAt:      updatedAt,
			Profile:        "work",
			SessionTarget:  "isolated",
			DeleteAfterRun: true,
			Schedule: coreautomation.Schedule{
				Kind:     coreautomation.ScheduleCron,
				At:       createdAt.Add(24 * time.Hour),
				Every:    2 * time.Hour,
				Cron:     "0 8 * * *",
				Timezone: "Africa/Lagos",
			},
			Payload: coreautomation.Payload{
				Kind:          coreautomation.PayloadPrompt,
				Prompt:        "Summarize activity",
				SystemEvent:   "daily_summary",
				Model:         "gpt-test",
				Provider:      "openai",
				BaseURL:       "https://api.example.test",
				NoTimeout:     true,
				MaxRuntime:    3 * time.Minute,
				MaxIterations: 9,
				RetryAttempts: 3,
				RetryBackoff:  10 * time.Second,
				RetryMaxDelay: time.Minute,
				ToolGroups:    []string{"memory", "search"},
				Metadata:      map[string]string{"origin": "gateway"},
			},
			Delivery: coreautomation.Delivery{
				Mode:            coreautomation.DeliveryGateway,
				Channel:         "telegram",
				Target:          "user-1",
				ThreadID:        "thread-1",
				WebhookURL:      "https://hooks.example.test",
				BestEffort:      true,
				FailureTarget:   "ops",
				FailureAfter:    2,
				FailureCooldown: time.Hour,
			},
			State: coreautomation.JobState{
				NextRunAt:           nextRunAt,
				RunningAt:           runningAt,
				LastRunAt:           lastRunAt,
				LastStatus:          coreautomation.RunStatusError,
				LastError:           "provider unavailable",
				LastDuration:        45 * time.Second,
				ConsecutiveErrors:   2,
				LastFailureNoticeAt: lastFailureNoticeAt,
			},
		},
	})

	require.NoError(t, err)
	for _, expected := range []string{
		"Job\n",
		"ID:                   " + testAutomationCommandJobID,
		"Name:                 Daily summary",
		"Description:          Summarize project activity",
		"Enabled:              true",
		"Created at:           2026-07-05T08:00:00Z",
		"Updated at:           2026-07-05T08:01:00Z",
		"Profile:              work",
		"Session target:       isolated",
		"Delete after run:     true",
		"Schedule\n",
		"Kind:                 cron",
		"At:                   2026-07-06T08:00:00Z",
		"Every:                2h0m0s",
		"Cron:                 0 8 * * *",
		"Timezone:             Africa/Lagos",
		"Payload\n",
		"Prompt:               Summarize activity",
		"System event:         daily_summary",
		"Model:                gpt-test",
		"Provider:             openai",
		"Base URL:             https://api.example.test",
		"No timeout:           true",
		"Max runtime:          3m0s",
		"Max iterations:       9",
		"Retry attempts:       3",
		"Retry backoff:        10s",
		"Retry max delay:      1m0s",
		"Tool groups:          memory, search",
		"Metadata:             origin=gateway",
		"Delivery\n",
		"Mode:                 gateway",
		"Channel:              telegram",
		"Target:               user-1",
		"Thread ID:            thread-1",
		"Webhook URL:          https://hooks.example.test",
		"Best effort:          true",
		"Failure target:       ops",
		"Failure after:        2",
		"Failure cooldown:     1h0m0s",
		"State\n",
		"Next run at:          2026-07-05T10:00:00+01:00",
		"Running at:           2026-07-05T08:02:00Z",
		"Last run at:          2026-07-05T07:00:00Z",
		"Last status:          error",
		"Last error:           provider unavailable",
		"Last duration:        45s",
		"Consecutive errors:   2",
		"Last failure notice:  2026-07-05T07:30:00Z",
		"Last run\n",
		"Status:               none",
		"Recent failures\n",
	} {
		require.Contains(t, output.String(), expected)
	}
}
