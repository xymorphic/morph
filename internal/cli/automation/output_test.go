package automation

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	coreautomation "github.com/xymorphic/morph/internal/automation"
)

func TestJobListToText_FormatsRowsAndEmptyState(t *testing.T) {
	require.Equal(t, "No automation jobs found.\n", jobListToText(nil))

	output := jobListToText([]coreautomation.Job{{
		ID:      testAutomationCommandJobID,
		Name:    "Daily summary",
		Enabled: true,
		Schedule: coreautomation.Schedule{
			Kind:     coreautomation.ScheduleCron,
			Cron:     "0 8 * * *",
			Timezone: "Africa/Lagos",
		},
		State: coreautomation.JobState{
			NextRunAt:  time.Date(2026, 7, 5, 9, 0, 0, 0, time.UTC),
			LastStatus: coreautomation.RunStatusOK,
		},
	}})

	require.True(t, strings.HasPrefix(output, "ID"))
	require.Contains(t, output, "ID                          NAME           ENABLED  SCHEDULE")
	require.Contains(t, output, testAutomationCommandJobID+"  Daily summary  true     cron 0 8 * * *")
	require.Contains(t, output, "2026-07-05T10:00:00+01:00  ok")
}

func TestRunListToText_FormatsRowsAndEmptyState(t *testing.T) {
	require.Equal(t, "No automation runs found.\n", runListToText(nil))

	output := runListToText([]coreautomation.Run{{
		ID:             testAutomationCommandRunID,
		JobID:          testAutomationCommandJobID,
		Status:         coreautomation.RunStatusOK,
		StartedAt:      time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC),
		Duration:       12 * time.Second,
		DeliveryStatus: coreautomation.DeliveryStatusDelivered,
	}})

	require.True(t, strings.HasPrefix(output, "RUN ID"))
	require.Contains(t, output, "RUN ID                        JOB ID")
	require.Contains(t, output, testAutomationCommandRunID+"  "+testAutomationCommandJobID+"  ok")
	require.Contains(t, output, "2026-07-05T08:00:00Z  12s       delivered")
}

func TestDiagnosticFindingsToText_FormatsFindingsAndPassedState(t *testing.T) {
	require.Equal(
		t,
		"Automation diagnostics\n  Status:               passed\n",
		diagnosticFindingsToText(nil),
	)

	output := diagnosticFindingsToText([]coreautomation.DiagnosticFinding{
		{
			Severity: coreautomation.DiagnosticSeverityError,
			Code:     "delivery_webhook_url_missing",
			JobID:    testAutomationCommandJobID,
			Message:  "webhook delivery requires a webhook URL",
			Action:   "morph automation update job --webhook-url <url>",
		},
		{
			Severity: coreautomation.DiagnosticSeverityWarn,
			Code:     "job_stuck_running",
			JobID:    testAutomationCommandJobID,
			Message:  "job appears stuck",
			Action:   "morph automation recover clear-running job",
		},
	})

	require.Contains(t, output, "[ERROR] delivery_webhook_url_missing")
	require.Contains(t, output, "Job ID:               "+testAutomationCommandJobID)
	require.Contains(t, output, "Message:              webhook delivery requires a webhook URL")
	require.Contains(t, output, "Action:               morph automation update job --webhook-url <url>")
	require.Contains(t, output, "[WARN] job_stuck_running")
}

func TestWriteRunSummary_OutputsAllRunFields(t *testing.T) {
	_, output := setupAutomationCommandTest(t)
	run := coreautomation.Run{
		ID:             testAutomationCommandRunID,
		JobID:          testAutomationCommandJobID,
		Status:         coreautomation.RunStatusOK,
		StartedAt:      time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC),
		EndedAt:        time.Date(2026, 7, 5, 8, 0, 12, 0, time.UTC),
		Duration:       12 * time.Second,
		Output:         "completed",
		Error:          "none",
		SessionID:      "ses_projectaprojectaproje",
		DeliveryStatus: coreautomation.DeliveryStatusDelivered,
		DeliveryError:  "none",
		Model:          "gpt-test",
		Provider:       "openai",
		Usage: coreautomation.Usage{
			InputTokens:      10,
			OutputTokens:     20,
			TotalTokens:      30,
			CacheReadTokens:  4,
			CacheWriteTokens: 5,
		},
	}

	require.NoError(t, writeRunSummary(run))
	for _, expected := range []string{
		"Run\n",
		"ID:                   " + testAutomationCommandRunID,
		"Job ID:               " + testAutomationCommandJobID,
		"Status:               ok",
		"Started at:           2026-07-05T08:00:00Z",
		"Ended at:             2026-07-05T08:00:12Z",
		"Duration:             12s",
		"Output:               completed",
		"Error:                none",
		"Session ID:           ses_projectaprojectaproje",
		"Trace session:        ses_projectaprojectaproje",
		"Delivery status:      delivered",
		"Delivery error:       none",
		"Model:                gpt-test",
		"Provider:             openai",
		"Input tokens:         10",
		"Output tokens:        20",
		"Total tokens:         30",
		"Cache read tokens:    4",
		"Cache write tokens:   5",
	} {
		require.Contains(t, output.String(), expected)
	}
}
