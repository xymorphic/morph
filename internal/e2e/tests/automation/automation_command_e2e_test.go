package automation

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	automationcmd "github.com/xymorphic/morph/cmd/automation"
	daemoncmd "github.com/xymorphic/morph/cmd/daemon"
	coreautomation "github.com/xymorphic/morph/internal/automation"
	"github.com/xymorphic/morph/internal/e2e"
	"github.com/xymorphic/morph/internal/profile"
	rpcclient "github.com/xymorphic/morph/internal/rpc/client"
	"github.com/xymorphic/morph/pkg/logutils"
)

func init() {
	logutils.SetOutput(io.Discard)
}

func Test_E2E_AutomationCommand_RunsDueOneShotJob(t *testing.T) {
	daemon := startAutomationDaemon(t)
	defer daemon.stop(t)

	runAt := time.Now().UTC().Add(500 * time.Millisecond).Format(time.RFC3339Nano)
	output, err := runAutomationCommand(t,
		"automation", "add",
		"--name", "One Shot",
		"--schedule", runAt,
		"--system-event", "wake",
		"--delivery", "none",
	)
	require.NoError(t, err)
	jobID := strings.TrimSpace(output)
	require.NotEmpty(t, jobID)

	run := waitForAutomationRun(t, daemon.client.AutomationAPI(), coreautomation.RunQuery{JobID: jobID})
	require.Equal(t, coreautomation.RunStatusSkipped, run.Status)
	require.Equal(t, jobID, run.JobID)

	job := getAutomationJob(t, daemon.client.AutomationAPI(), jobID)
	require.Equal(t, coreautomation.RunStatusSkipped, job.State.LastStatus)
	require.True(t, job.State.NextRunAt.IsZero())
}

func Test_E2E_AutomationCommand_RejectsInvalidDeliveryBeforePersistence(t *testing.T) {
	daemon := startAutomationDaemon(t)
	defer daemon.stop(t)

	output, err := runAutomationCommand(t,
		"automation", "add",
		"--name", "Invalid delivery",
		"--schedule", "every 5m",
		"--prompt", "Say: no delivery",
		"--delivery", "n",
	)

	require.ErrorContains(t, err, `unsupported automation delivery mode "n"`)
	require.Empty(t, output)

	_, err = daemon.client.AutomationAPI().Add(context.Background(), coreautomation.Job{
		Enabled:  false,
		Schedule: coreautomation.Schedule{Kind: coreautomation.ScheduleEvery, Every: 5 * time.Minute},
		Payload: coreautomation.Payload{
			Kind:   coreautomation.PayloadPrompt,
			Prompt: "Say: no delivery",
		},
		Delivery: coreautomation.Delivery{Mode: coreautomation.DeliveryMode("n")},
	})
	require.ErrorContains(t, err, `unsupported automation delivery mode "n"`)

	list, listErr := daemon.client.AutomationAPI().List(context.Background(), coreautomation.JobQuery{
		IncludeDisabled: true,
	})
	require.NoError(t, listErr)
	require.Empty(t, list.Jobs)
}

func Test_E2E_AutomationCommand_ManualRecurringRunPersistsAcrossDaemonRestart(t *testing.T) {
	daemon := startAutomationDaemon(t)

	output, err := runAutomationCommand(t,
		"automation", "add",
		"--name", "Recurring",
		"--schedule", "every 1h",
		"--system-event", "wake",
		"--delivery", "none",
	)
	require.NoError(t, err)
	jobID := strings.TrimSpace(output)
	require.NotEmpty(t, jobID)

	runOutput, err := runAutomationCommand(t, "automation", "run", jobID)
	require.NoError(t, err)
	require.Contains(t, runOutput, "Status:               skipped")

	run := waitForAutomationRun(t, daemon.client.AutomationAPI(), coreautomation.RunQuery{JobID: jobID})
	require.Equal(t, coreautomation.RunStatusSkipped, run.Status)

	job := getAutomationJob(t, daemon.client.AutomationAPI(), jobID)
	require.Equal(t, coreautomation.RunStatusSkipped, job.State.LastStatus)
	require.True(t, job.State.NextRunAt.After(time.Now().UTC().Add(30*time.Minute)))

	daemon.stop(t)
	daemon = startAutomationDaemonAt(t, daemon.profileHome, daemon.port)
	defer daemon.stop(t)

	listOutput, err := runAutomationCommand(t, "automation", "list", "--all")
	require.NoError(t, err)
	require.Contains(t, listOutput, jobID)
	require.Contains(t, listOutput, "Recurring")

	runsOutput, err := runAutomationCommand(t, "automation", "runs", "--job", jobID)
	require.NoError(t, err)
	require.Contains(t, runsOutput, run.ID)
	require.Contains(t, runsOutput, "skipped")
}

type automationDaemon struct {
	profileHome string
	port        int
	client      *rpcclient.Client
	cancel      context.CancelFunc
	errCh       <-chan error
	stopped     bool
}

func startAutomationDaemon(t *testing.T) *automationDaemon {
	t.Helper()

	port, err := e2e.ReserveRPCPort()
	require.NoError(t, err)

	return startAutomationDaemonAt(t, filepath.Join(t.TempDir(), "profile"), port)
}

func startAutomationDaemonAt(t *testing.T, profileHome string, port int) *automationDaemon {
	t.Helper()

	originalProfile := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(originalProfile)
	})
	require.NoError(t, os.MkdirAll(profileHome, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(profileHome, "config.yaml"),
		[]byte(automationE2EConfig(port)),
		0o600,
	))
	profile.SetActive(profile.WithMetadataPaths(profile.Profile{
		Name:    "automation-e2e",
		HomeDir: profileHome,
	}))

	previousDaemonOutput := daemoncmd.SetOutput(io.Discard)
	t.Cleanup(func() {
		daemoncmd.SetOutput(previousDaemonOutput)
	})

	runCtx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- daemoncmd.NewCommand().Run(runCtx, []string{"daemon"})
	}()

	client, err := e2e.WaitForRPC("127.0.0.1", port, 5*time.Second)
	require.NoError(t, err)

	return &automationDaemon{
		profileHome: profileHome,
		port:        port,
		client:      client,
		cancel:      cancel,
		errCh:       errCh,
	}
}

func (d *automationDaemon) stop(t *testing.T) {
	t.Helper()

	if d == nil || d.stopped {
		return
	}
	d.stopped = true
	if d.client != nil {
		require.NoError(t, d.client.Close())
	}
	if d.cancel != nil {
		d.cancel()
	}
	if d.errCh != nil {
		require.NoError(t, <-d.errCh)
	}
}

func runAutomationCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var output bytes.Buffer
	previousOutput := automationcmd.SetOutput(&output)
	t.Cleanup(func() {
		automationcmd.SetOutput(previousOutput)
	})

	err := automationcmd.NewCommand().Run(context.Background(), args)
	return output.String(), err
}

func waitForAutomationRun(
	t *testing.T,
	api rpcclient.AutomationAPI,
	query coreautomation.RunQuery,
) coreautomation.Run {
	t.Helper()

	var latest coreautomation.Run
	require.Eventually(t, func() bool {
		list, err := api.Runs(context.Background(), query)
		if err != nil || len(list.Runs) == 0 {
			return false
		}
		latest = list.Runs[0]
		return latest.Status != coreautomation.RunStatusRunning
	}, 5*time.Second, 100*time.Millisecond)

	return latest
}

func getAutomationJob(
	t *testing.T,
	api rpcclient.AutomationAPI,
	id string,
) coreautomation.Job {
	t.Helper()

	list, err := api.List(context.Background(), coreautomation.JobQuery{
		IDs:             []string{id},
		IncludeDisabled: true,
	})
	require.NoError(t, err)
	require.Len(t, list.Jobs, 1)

	return list.Jobs[0]
}

func automationE2EConfig(port int) string {
	return fmt.Sprintf(`name: Automation E2E
models:
  main:
    name: test-model
    stream: false
storage:
  backend: sqlite
search:
  vector:
    enabled: false
rpc:
  address: 127.0.0.1
  port: %d
gateway:
  enabled: false
permissions:
  preset: custom
  rules:
    - name: allow automation e2e operations
      decision: allow
log:
  noColor: true
`, port)
}
