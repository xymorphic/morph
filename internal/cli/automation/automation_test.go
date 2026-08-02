package automation

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	cli "github.com/urfave/cli/v3"

	coreautomation "github.com/xymorphic/morph/internal/automation"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/profile"
)

var (
	testAutomationCommandJobID = "auto_projectaprojectaproje"
	testAutomationCommandRunID = "autorun_projectaprojectaproj"
)

func newTestCommand() *cli.Command {
	return &cli.Command{
		Name: "automation",
		Commands: []*cli.Command{
			NewStatusCommand(),
			NewListCommand(),
			NewAddCommand(),
			NewUpdateCommand(),
			NewPauseCommand(),
			NewResumeCommand(),
			NewRunCommand(),
			NewRemoveCommand(),
			NewRunsCommand(),
			NewDiagnoseCommand(),
			NewInspectCommand(),
			NewRecoverCommand(),
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			return cli.ShowSubcommandHelp(cmd)
		},
	}
}

func TestNewCommand_StatusCallsRPC(t *testing.T) {
	api, output := setupAutomationCommandTest(t)
	api.status = coreautomation.Status{
		Running:      true,
		JobCount:     2,
		RunningCount: 1,
		StartedAt:    time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC),
		NextWakeAt:   time.Date(2026, 7, 5, 9, 0, 0, 0, time.UTC),
	}

	require.NoError(t, newTestCommand().Run(context.Background(), []string{"automation", "status"}))

	require.Equal(t, "Automation scheduler\n"+
		"  Running:              true\n"+
		"  Jobs:                 2\n"+
		"  Running jobs:         1\n"+
		"  Started at:           2026-07-05T08:00:00Z\n"+
		"  Next wake at:         2026-07-05T09:00:00Z\n", output.String())
}

func TestNewCommand_AddParsesJobFlags(t *testing.T) {
	api, output := setupAutomationCommandTest(t)

	require.NoError(t, newTestCommand().Run(context.Background(), []string{
		"automation", "add",
		"--name", "Daily",
		"--schedule", "every 1h",
		"--prompt", "summarize",
		"--tool-group", "memory",
		"--delivery", "local",
	}))

	require.Equal(t, testAutomationCommandJobID+"\n", output.String())
	require.Equal(t, "Daily", api.added.Name)
	require.True(t, api.added.Enabled)
	require.Equal(t, time.Hour, api.added.Schedule.Every)
	require.Equal(t, "summarize", api.added.Payload.Prompt)
	require.Equal(t, []string{"memory"}, api.added.Payload.ToolGroups)
	require.Equal(t, coreautomation.DeliveryLocal, api.added.Delivery.Mode)
}

func TestNewCommand_ListAndUpdateCallRPC(t *testing.T) {
	api, output := setupAutomationCommandTest(t)
	api.added = coreautomation.Job{
		ID:      testAutomationCommandJobID,
		Name:    "Daily",
		Enabled: true,
		Schedule: coreautomation.Schedule{
			Kind:  coreautomation.ScheduleEvery,
			Every: time.Hour,
		},
		State: coreautomation.JobState{NextRunAt: time.Date(2026, 7, 5, 9, 0, 0, 0, time.UTC)},
	}

	require.NoError(t, newTestCommand().Run(context.Background(), []string{"automation", "list", "--all"}))
	require.True(t, strings.HasPrefix(output.String(), "ID"))
	require.Contains(t, output.String(), "ID                          NAME   ENABLED  SCHEDULE")
	require.Contains(t, output.String(), testAutomationCommandJobID+"  Daily  true     every 1h0m0s")

	output.Reset()
	require.NoError(t, newTestCommand().Run(context.Background(), []string{
		"automation", "update", testAutomationCommandJobID,
		"--name", "Renamed",
		"--schedule", "every 2h",
		"--prompt", "next",
	}))
	require.Equal(t, testAutomationCommandJobID+"\n", output.String())
	require.Equal(t, testAutomationCommandJobID, api.patch.ID)
	require.NotNil(t, api.patch.Name)
	require.Equal(t, "Renamed", *api.patch.Name)
	require.NotNil(t, api.patch.Schedule)
	require.Equal(t, 2*time.Hour, api.patch.Schedule.Every)
	require.NotNil(t, api.patch.Payload)
	require.Equal(t, "next", api.patch.Payload.Prompt)
}

func TestNewCommand_UpdatePreservesUnspecifiedPayloadAndDeliveryFields(t *testing.T) {
	api, _ := setupAutomationCommandTest(t)
	api.jobs = []coreautomation.Job{{
		ID: testAutomationCommandJobID,
		Payload: coreautomation.Payload{
			Kind:          coreautomation.PayloadPrompt,
			Prompt:        "summarize",
			Model:         "old-model",
			Provider:      "openai",
			BaseURL:       "https://api.example.test",
			NoTimeout:     true,
			MaxRuntime:    2 * time.Minute,
			MaxIterations: 4,
			RetryAttempts: 3,
			RetryBackoff:  time.Second,
			RetryMaxDelay: 10 * time.Second,
			ToolGroups:    []string{"core", "web"},
			Metadata:      map[string]string{"origin": "test"},
		},
		Delivery: coreautomation.Delivery{
			Mode:            coreautomation.DeliveryGateway,
			Channel:         "telegram",
			Target:          "old-target",
			ThreadID:        "topic-1",
			BestEffort:      true,
			FailureTarget:   "admin",
			FailureAfter:    3,
			FailureCooldown: time.Hour,
		},
	}}

	require.NoError(t, newTestCommand().Run(context.Background(), []string{
		"automation", "update", testAutomationCommandJobID,
		"--model", "new-model",
		"--target=-1003973172572",
	}))

	require.Equal(t, []string{testAutomationCommandJobID}, api.jobQuery.IDs)
	require.True(t, api.jobQuery.IncludeDisabled)
	require.NotNil(t, api.patch.Payload)
	require.Equal(t, coreautomation.Payload{
		Kind:          coreautomation.PayloadPrompt,
		Prompt:        "summarize",
		Model:         "new-model",
		Provider:      "openai",
		BaseURL:       "https://api.example.test",
		NoTimeout:     true,
		MaxRuntime:    2 * time.Minute,
		MaxIterations: 4,
		RetryAttempts: 3,
		RetryBackoff:  time.Second,
		RetryMaxDelay: 10 * time.Second,
		ToolGroups:    []string{"core", "web"},
		Metadata:      map[string]string{"origin": "test"},
	}, *api.patch.Payload)
	require.NotNil(t, api.patch.Delivery)
	require.Equal(t, coreautomation.Delivery{
		Mode:            coreautomation.DeliveryGateway,
		Channel:         "telegram",
		Target:          "-1003973172572",
		ThreadID:        "topic-1",
		BestEffort:      true,
		FailureTarget:   "admin",
		FailureAfter:    3,
		FailureCooldown: time.Hour,
	}, *api.patch.Delivery)
}

func TestNewCommand_UpdateCanSwitchPayloadKindAndClearValues(t *testing.T) {
	api, _ := setupAutomationCommandTest(t)
	api.jobs = []coreautomation.Job{{
		ID: testAutomationCommandJobID,
		Payload: coreautomation.Payload{
			Kind:       coreautomation.PayloadPrompt,
			Prompt:     "summarize",
			NoTimeout:  true,
			MaxRuntime: time.Minute,
		},
		Delivery: coreautomation.Delivery{
			Mode:       coreautomation.DeliveryGateway,
			Channel:    "telegram",
			Target:     "chat",
			BestEffort: true,
		},
	}}

	require.NoError(t, newTestCommand().Run(context.Background(), []string{
		"automation", "update", testAutomationCommandJobID,
		"--system-event", "wake",
		"--no-timeout=false",
		"--max-runtime", "0s",
		"--best-effort=false",
	}))

	require.Equal(t, coreautomation.PayloadSystemEvent, api.patch.Payload.Kind)
	require.Empty(t, api.patch.Payload.Prompt)
	require.Equal(t, "wake", api.patch.Payload.SystemEvent)
	require.False(t, api.patch.Payload.NoTimeout)
	require.Zero(t, api.patch.Payload.MaxRuntime)
	require.False(t, api.patch.Delivery.BestEffort)
	require.Equal(t, coreautomation.DeliveryGateway, api.patch.Delivery.Mode)
	require.Equal(t, "telegram", api.patch.Delivery.Channel)
}

func TestNewCommand_UpdateAppliesEveryPayloadAndDeliveryFlag(t *testing.T) {
	api, _ := setupAutomationCommandTest(t)
	api.jobs = []coreautomation.Job{{
		ID: testAutomationCommandJobID,
		Payload: coreautomation.Payload{
			Kind:        coreautomation.PayloadSystemEvent,
			SystemEvent: "wake",
			Metadata:    map[string]string{"origin": "test"},
		},
		Delivery: coreautomation.Delivery{
			Mode:       coreautomation.DeliveryWebhook,
			WebhookURL: "https://old-hook.test",
		},
	}}

	require.NoError(t, newTestCommand().Run(context.Background(), []string{
		"automation", "update", testAutomationCommandJobID,
		"--prompt", "summarize",
		"--model", "new-model",
		"--provider", "openai",
		"--base-url", "https://api.example.test",
		"--no-timeout=false",
		"--max-runtime", "3m",
		"--max-iterations", "8",
		"--retry-attempts", "4",
		"--retry-backoff", "2s",
		"--retry-max-delay", "20s",
		"--tool-group", "core",
		"--delivery", "gateway",
		"--channel", "telegram",
		"--target", "chat",
		"--thread", "topic",
		"--best-effort=false",
	}))

	require.Equal(t, coreautomation.Payload{
		Kind:          coreautomation.PayloadPrompt,
		Prompt:        "summarize",
		Model:         "new-model",
		Provider:      "openai",
		BaseURL:       "https://api.example.test",
		MaxRuntime:    3 * time.Minute,
		MaxIterations: 8,
		RetryAttempts: 4,
		RetryBackoff:  2 * time.Second,
		RetryMaxDelay: 20 * time.Second,
		ToolGroups:    []string{"core"},
		Metadata:      map[string]string{"origin": "test"},
	}, *api.patch.Payload)
	require.Equal(t, coreautomation.Delivery{
		Mode:       coreautomation.DeliveryGateway,
		Channel:    "telegram",
		Target:     "chat",
		ThreadID:   "topic",
		BestEffort: false,
	}, *api.patch.Delivery)
}

func TestNewCommand_UpdateClearsRoutingWhenChangingToLocalDelivery(t *testing.T) {
	api, _ := setupAutomationCommandTest(t)
	api.jobs = []coreautomation.Job{{
		ID: testAutomationCommandJobID,
		Delivery: coreautomation.Delivery{
			Mode:       coreautomation.DeliveryGateway,
			Channel:    "telegram",
			Target:     "chat",
			ThreadID:   "topic",
			BestEffort: true,
		},
	}}

	require.NoError(t, newTestCommand().Run(context.Background(), []string{
		"automation", "update", testAutomationCommandJobID,
		"--delivery", "local",
	}))

	require.Equal(t, coreautomation.Delivery{
		Mode:       coreautomation.DeliveryLocal,
		BestEffort: true,
	}, *api.patch.Delivery)
}

func TestNewCommand_UpdateReturnsCurrentJobLookupErrors(t *testing.T) {
	t.Run("list failure", func(t *testing.T) {
		api, _ := setupAutomationCommandTest(t)
		expected := errors.New("list failed")
		api.listErr = expected

		err := newTestCommand().Run(context.Background(), []string{
			"automation", "update", testAutomationCommandJobID,
			"--model", "new-model",
		})

		require.ErrorIs(t, err, expected)
		require.Empty(t, api.patch)
	})

	t.Run("job missing", func(t *testing.T) {
		api, _ := setupAutomationCommandTest(t)
		api.jobs = []coreautomation.Job{}

		err := newTestCommand().Run(context.Background(), []string{
			"automation", "update", testAutomationCommandJobID,
			"--target", "chat",
		})

		require.EqualError(t, err, "automation job not found")
		require.Empty(t, api.patch)
	})
}

func TestNewCommand_PauseResumeRunRemoveAndRunsCallRPC(t *testing.T) {
	api, output := setupAutomationCommandTest(t)

	require.NoError(t, newTestCommand().Run(context.Background(), []string{"automation", "pause", testAutomationCommandJobID}))
	require.NotNil(t, api.patch.Enabled)
	require.False(t, *api.patch.Enabled)
	require.Contains(t, output.String(), "enabled=false")

	output.Reset()
	require.NoError(t, newTestCommand().Run(context.Background(), []string{"automation", "resume", testAutomationCommandJobID}))
	require.NotNil(t, api.patch.Enabled)
	require.True(t, *api.patch.Enabled)
	require.Contains(t, output.String(), "enabled=true")

	output.Reset()
	require.NoError(t, newTestCommand().Run(context.Background(), []string{"automation", "run", testAutomationCommandJobID}))
	require.Contains(t, output.String(), testAutomationCommandRunID)

	output.Reset()
	require.NoError(t, newTestCommand().Run(context.Background(), []string{"automation", "remove", testAutomationCommandJobID}))
	require.Equal(t, testAutomationCommandJobID, api.removedID)
	require.Equal(t, testAutomationCommandJobID+"\n", output.String())

	output.Reset()
	api.runs = []coreautomation.Run{{ID: testAutomationCommandRunID, JobID: testAutomationCommandJobID, Status: coreautomation.RunStatusError}}
	require.NoError(t, newTestCommand().Run(context.Background(), []string{
		"automation", "runs",
		"--job", testAutomationCommandJobID,
		"--status", "error",
	}))
	require.Equal(t, testAutomationCommandJobID, api.runQuery.JobID)
	require.Equal(t, []coreautomation.RunStatus{coreautomation.RunStatusError}, api.runQuery.Status)
	require.True(t, strings.HasPrefix(output.String(), "RUN ID"))
	require.Contains(t, output.String(), "RUN ID                        JOB ID")
	require.Contains(t, output.String(), testAutomationCommandRunID+"  "+testAutomationCommandJobID+"  error")
}

func TestCommandHelpersCoverScheduleAndArgumentBranches(t *testing.T) {
	require.Equal(t, "at 2026-07-05T08:00:00Z", formatSchedule(coreautomation.Schedule{
		Kind: coreautomation.ScheduleAt,
		At:   time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC),
	}))
	require.Equal(t, "cron 0 8 * * *", formatSchedule(coreautomation.Schedule{
		Kind: coreautomation.ScheduleCron,
		Cron: "0 8 * * *",
	}))
	require.Equal(t, "", formatSchedule(coreautomation.Schedule{}))
	require.Nil(t, parseRunStatuses(""))
}

func TestFormatTimeInScheduleTimezone_UsesScheduleTimezoneWithUTCFallback(t *testing.T) {
	value := time.Date(2026, 7, 5, 9, 0, 0, 0, time.UTC)

	require.Equal(t, "-", formatTimeInScheduleTimezone(time.Time{}, coreautomation.Schedule{
		Timezone: "Africa/Lagos",
	}))
	require.Equal(t, "2026-07-05T10:00:00+01:00", formatTimeInScheduleTimezone(value, coreautomation.Schedule{
		Timezone: "Africa/Lagos",
	}))
	require.Equal(t, "2026-07-05T09:00:00Z", formatTimeInScheduleTimezone(value, coreautomation.Schedule{}))
	require.Equal(t, "2026-07-05T09:00:00Z", formatTimeInScheduleTimezone(value, coreautomation.Schedule{
		Timezone: "Missing/Zone",
	}))
}

func TestSetOutputHandlesNilAndWriter(t *testing.T) {
	previous := SetOutput(nil)
	t.Cleanup(func() { SetOutput(previous) })
	require.Equal(t, io.Discard, automationOutput)

	output := &bytes.Buffer{}
	require.Equal(t, io.Discard, SetOutput(output))
	require.Same(t, output, automationOutput)
}

func TestTestCommand_ShowsHelpForMissingSubcommand(t *testing.T) {
	_, _ = setupAutomationCommandTest(t)

	require.NoError(t, newTestCommand().Run(context.Background(), []string{"automation"}))
}

func TestNewCommand_PropagatesActionErrors(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		mutate func(*automationCommandAPIStub)
	}{
		{
			name: "status rpc",
			args: []string{"automation", "status"},
			mutate: func(api *automationCommandAPIStub) {
				api.statusErr = errors.New("status failed")
			},
		},
		{
			name: "status write",
			args: []string{"automation", "status"},
			mutate: func(*automationCommandAPIStub) {
				automationOutput = errorWriter{}
			},
		},
		{
			name: "list rpc",
			args: []string{"automation", "list"},
			mutate: func(api *automationCommandAPIStub) {
				api.listErr = errors.New("list failed")
			},
		},
		{
			name: "list write",
			args: []string{"automation", "list"},
			mutate: func(api *automationCommandAPIStub) {
				api.added = coreautomation.Job{ID: testAutomationCommandJobID}
				automationOutput = errorWriter{}
			},
		},
		{
			name: "add parse",
			args: []string{"automation", "add", "--prompt", "summarize"},
		},
		{
			name: "add rpc",
			args: []string{"automation", "add", "--schedule", "every 1h", "--prompt", "summarize"},
			mutate: func(api *automationCommandAPIStub) {
				api.addErr = errors.New("add failed")
			},
		},
		{
			name: "add write",
			args: []string{"automation", "add", "--schedule", "every 1h", "--prompt", "summarize"},
			mutate: func(*automationCommandAPIStub) {
				automationOutput = errorWriter{}
			},
		},
		{
			name: "update missing id",
			args: []string{"automation", "update"},
		},
		{
			name: "update parse",
			args: []string{"automation", "update", testAutomationCommandJobID, "--schedule", "not-a-schedule"},
		},
		{
			name: "update rpc",
			args: []string{"automation", "update", testAutomationCommandJobID, "--name", "Renamed"},
			mutate: func(api *automationCommandAPIStub) {
				api.updateErr = errors.New("update failed")
			},
		},
		{
			name: "update write",
			args: []string{"automation", "update", testAutomationCommandJobID, "--name", "Renamed"},
			mutate: func(*automationCommandAPIStub) {
				automationOutput = errorWriter{}
			},
		},
		{
			name: "pause missing id",
			args: []string{"automation", "pause"},
		},
		{
			name: "pause rpc",
			args: []string{"automation", "pause", testAutomationCommandJobID},
			mutate: func(api *automationCommandAPIStub) {
				api.updateErr = errors.New("pause failed")
			},
		},
		{
			name: "pause write",
			args: []string{"automation", "pause", testAutomationCommandJobID},
			mutate: func(*automationCommandAPIStub) {
				automationOutput = errorWriter{}
			},
		},
		{
			name: "run missing id",
			args: []string{"automation", "run"},
		},
		{
			name: "run rpc",
			args: []string{"automation", "run", testAutomationCommandJobID},
			mutate: func(api *automationCommandAPIStub) {
				api.runErr = errors.New("run failed")
			},
		},
		{
			name: "run write",
			args: []string{"automation", "run", testAutomationCommandJobID},
			mutate: func(*automationCommandAPIStub) {
				automationOutput = errorWriter{}
			},
		},
		{
			name: "remove missing id",
			args: []string{"automation", "remove"},
		},
		{
			name: "remove rpc",
			args: []string{"automation", "remove", testAutomationCommandJobID},
			mutate: func(api *automationCommandAPIStub) {
				api.removeErr = errors.New("remove failed")
			},
		},
		{
			name: "remove write",
			args: []string{"automation", "remove", testAutomationCommandJobID},
			mutate: func(*automationCommandAPIStub) {
				automationOutput = errorWriter{}
			},
		},
		{
			name: "runs rpc",
			args: []string{"automation", "runs"},
			mutate: func(api *automationCommandAPIStub) {
				api.runsErr = errors.New("runs failed")
			},
		},
		{
			name: "runs write",
			args: []string{"automation", "runs"},
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

func TestNewCommand_PropagatesClientCreationError(t *testing.T) {
	_, _ = setupAutomationCommandTest(t)
	expected := errors.New("client failed")
	newClient = func(context.Context, *config.Config) (automationClient, error) {
		return nil, expected
	}

	err := newTestCommand().Run(context.Background(), []string{"automation", "status"})

	require.ErrorIs(t, err, expected)
}

func TestNewCommand_PropagatesClientCreationErrorForEveryAction(t *testing.T) {
	tests := [][]string{
		{"automation", "list"},
		{"automation", "add", "--schedule", "every 1h", "--prompt", "summarize"},
		{"automation", "update", testAutomationCommandJobID, "--name", "Renamed"},
		{"automation", "pause", testAutomationCommandJobID},
		{"automation", "resume", testAutomationCommandJobID},
		{"automation", "run", testAutomationCommandJobID},
		{"automation", "remove", testAutomationCommandJobID},
		{"automation", "runs"},
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

func TestLoadAutomationConfig_ReturnsLoadError(t *testing.T) {
	originalProfile := profile.Active()
	t.Cleanup(func() { profile.SetActive(originalProfile) })
	home := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.yaml"), []byte("name: ["), 0o600))
	profile.SetActive(profile.WithMetadataPaths(profile.Profile{Name: "test", HomeDir: home}))

	_, err := loadAutomationConfig(nil)

	require.ErrorContains(t, err, "failed to parse config file")
}

func TestGetAutomationAPI_ReturnsLoadError(t *testing.T) {
	originalProfile := profile.Active()
	t.Cleanup(func() { profile.SetActive(originalProfile) })
	home := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.yaml"), []byte("name: ["), 0o600))
	profile.SetActive(profile.WithMetadataPaths(profile.Profile{Name: "test", HomeDir: home}))

	api, closeClient, err := getAutomationAPI(context.Background(), nil)

	require.Nil(t, api)
	require.NotNil(t, closeClient)
	require.ErrorContains(t, err, "failed to parse config file")
}

func TestPatchFromCommand_CoversOptionalFlags(t *testing.T) {
	api, _ := setupAutomationCommandTest(t)
	api.jobs = []coreautomation.Job{{ID: testAutomationCommandJobID}}
	cmd := newTestCommand()
	require.NoError(t, cmd.Run(context.Background(), []string{
		"automation", "update", testAutomationCommandJobID,
		"--description", "desc",
		"--system-event", "wake",
		"--delivery", "local",
		"--profile", "work",
		"--session-target", "main",
		"--delete-after-run",
	}))
}

func TestJobFromCommand_CoversDisabledAndSystemEvent(t *testing.T) {
	api, _ := setupAutomationCommandTest(t)

	require.NoError(t, newTestCommand().Run(context.Background(), []string{
		"automation", "add",
		"--disabled",
		"--schedule", "every 1h",
		"--system-event", "wake",
		"--no-timeout",
		"--max-runtime", "2m",
		"--max-iterations", "3",
		"--retry-attempts", "2",
		"--retry-backoff", "1s",
		"--retry-max-delay", "5s",
		"--base-url", "https://example.test",
		"--provider", "openai",
		"--model", "gpt-test",
		"--delivery", "gateway",
		"--channel", "telegram",
		"--target", "user",
		"--thread", "thread",
		"--best-effort",
		"--delete-after-run",
	}))

	require.False(t, api.added.Enabled)
	require.Equal(t, coreautomation.PayloadSystemEvent, api.added.Payload.Kind)
	require.Equal(t, "wake", api.added.Payload.SystemEvent)
	require.True(t, api.added.Payload.NoTimeout)
	require.Equal(t, coreautomation.DeliveryGateway, api.added.Delivery.Mode)
	require.Equal(t, "telegram", api.added.Delivery.Channel)
	require.Equal(t, "user", api.added.Delivery.Target)
	require.Equal(t, "thread", api.added.Delivery.ThreadID)
	require.True(t, api.added.Delivery.BestEffort)
	require.True(t, api.added.DeleteAfterRun)

	require.NoError(t, newTestCommand().Run(context.Background(), []string{
		"automation", "add",
		"--schedule", "every 1h",
		"--prompt", "notify",
		"--delivery", "webhook",
		"--webhook-url", "https://hook.test",
	}))
	require.Equal(t, coreautomation.DeliveryWebhook, api.added.Delivery.Mode)
	require.Equal(t, "https://hook.test", api.added.Delivery.WebhookURL)
}

func TestNewCommand_RejectsInvalidDeliveryModeBeforeRPC(t *testing.T) {
	api, _ := setupAutomationCommandTest(t)

	err := newTestCommand().Run(context.Background(), []string{
		"automation", "add",
		"--schedule", "every 5m",
		"--prompt", "notify",
		"--delivery", "n",
	})
	require.EqualError(t, err, `unsupported automation delivery mode "n"`)
	require.Empty(t, api.added)

	err = newTestCommand().Run(context.Background(), []string{
		"automation", "add",
		"--schedule", "every 5m",
		"--prompt", "notify",
		"--target", "C1",
	})
	require.EqualError(t, err, "--delivery is required when setting delivery options")

	api.jobs = []coreautomation.Job{{ID: testAutomationCommandJobID}}
	err = newTestCommand().Run(context.Background(), []string{
		"automation", "update", testAutomationCommandJobID,
		"--delivery", "n",
	})
	require.EqualError(t, err, `unsupported automation delivery mode "n"`)
	require.Empty(t, api.patch)
}

func setupAutomationCommandTest(t *testing.T) (*automationCommandAPIStub, *bytes.Buffer) {
	t.Helper()
	t.Setenv("MORPH_RPC_ADDRESS", "")
	t.Setenv("MORPH_RPC_PORT", "")

	originalProfile := profile.Active()
	originalNewClient := newClient
	originalOutput := automationOutput
	t.Cleanup(func() {
		profile.SetActive(originalProfile)
		newClient = originalNewClient
		automationOutput = originalOutput
	})
	profile.SetActive(profile.WithMetadataPaths(profile.Profile{Name: "test", HomeDir: t.TempDir()}))

	output := &bytes.Buffer{}
	automationOutput = output
	api := &automationCommandAPIStub{}
	newClient = func(context.Context, *config.Config) (automationClient, error) {
		return &automationCommandClientStub{api: api}, nil
	}

	return api, output
}
