package daemon

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	morphcli "github.com/xymorphic/morph/internal/cli"
)

var errDaemonTestWrite = errors.New("write failed")

type errWriter struct{}

func (errWriter) Write([]byte) (int, error) {
	return 0, errDaemonTestWrite
}

func TestSetOutputReturnsPreviousAndDiscardsNil(t *testing.T) {
	originalOutput := daemonOutput
	originalStartupOutput := morphcli.SetDaemonOutput(io.Discard)
	t.Cleanup(func() {
		daemonOutput = originalOutput
		morphcli.SetDaemonOutput(originalStartupOutput)
	})
	var output bytes.Buffer

	previous := SetOutput(&output)
	require.Same(t, originalOutput, previous)
	previous = SetOutput(nil)
	require.Same(t, &output, previous)

	_, err := daemonOutput.Write([]byte("discarded"))
	require.NoError(t, err)
	require.Empty(t, output.String())
}

func TestStatusCommandPrintsDaemonStatus(t *testing.T) {
	originalGetDaemonStatus := getDaemonStatus
	originalOutput := daemonOutput
	t.Cleanup(func() {
		getDaemonStatus = originalGetDaemonStatus
		daemonOutput = originalOutput
	})

	startedAt := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	var output bytes.Buffer
	daemonOutput = &output
	getDaemonStatus = func(context.Context) (morphcli.DaemonStatus, error) {
		return morphcli.DaemonStatus{
			State:     "running",
			Health:    "SERVING",
			Profile:   "work",
			PID:       1234,
			Address:   "127.0.0.1",
			Port:      50051,
			StartedAt: startedAt,
			Uptime:    90 * time.Second,
		}, nil
	}

	err := NewCommand().Run(context.Background(), []string{"daemon", "status"})

	require.NoError(t, err)
	require.Equal(
		t,
		"Daemon\n"+
			"  State:       running\n"+
			"  Health:      SERVING\n"+
			"  Profile:     work\n"+
			"  PID:         1234\n"+
			"  RPC:         127.0.0.1:50051\n"+
			"  Uptime:      1m30s\n"+
			"  Started at:  2026-06-16T10:00:00Z\n",
		output.String(),
	)
}

func TestStatusCommandPrintsMissingDaemonWithoutError(t *testing.T) {
	originalGetDaemonStatus := getDaemonStatus
	originalOutput := daemonOutput
	t.Cleanup(func() {
		getDaemonStatus = originalGetDaemonStatus
		daemonOutput = originalOutput
	})

	var output bytes.Buffer
	daemonOutput = &output
	getDaemonStatus = func(context.Context) (morphcli.DaemonStatus, error) {
		return morphcli.DaemonStatus{State: "missing", Profile: "default"}, nil
	}

	err := NewCommand().Run(context.Background(), []string{"daemon", "status"})

	require.NoError(t, err)
	require.Equal(
		t,
		"Daemon\n"+
			"  State:       missing\n"+
			"  Health:      -\n"+
			"  Profile:     default\n"+
			"  PID:         0\n"+
			"  RPC:         -\n"+
			"  Uptime:      0s\n"+
			"  Started at:  -\n",
		output.String(),
	)
}

func TestStatusCommandReturnsStatusErrorAfterPrintingStatus(t *testing.T) {
	originalGetDaemonStatus := getDaemonStatus
	originalOutput := daemonOutput
	t.Cleanup(func() {
		getDaemonStatus = originalGetDaemonStatus
		daemonOutput = originalOutput
	})

	expectedErr := errors.New("daemon is invalid")
	var output bytes.Buffer
	daemonOutput = &output
	getDaemonStatus = func(context.Context) (morphcli.DaemonStatus, error) {
		return morphcli.DaemonStatus{State: "invalid", Profile: "default"}, expectedErr
	}

	err := NewCommand().Run(context.Background(), []string{"daemon", "status"})

	require.ErrorIs(t, err, expectedErr)
	require.Contains(t, output.String(), "State:       invalid")
}

func TestStatusCommandReturnsWriteError(t *testing.T) {
	originalGetDaemonStatus := getDaemonStatus
	originalOutput := daemonOutput
	t.Cleanup(func() {
		getDaemonStatus = originalGetDaemonStatus
		daemonOutput = originalOutput
	})

	daemonOutput = errWriter{}
	getDaemonStatus = func(context.Context) (morphcli.DaemonStatus, error) {
		return morphcli.DaemonStatus{State: "running"}, nil
	}

	err := NewCommand().Run(context.Background(), []string{"daemon", "status"})

	require.ErrorIs(t, err, errDaemonTestWrite)
}

func TestStartSubcommandIsNotAccepted(t *testing.T) {
	originalGetDaemonStatus := getDaemonStatus
	t.Cleanup(func() {
		getDaemonStatus = originalGetDaemonStatus
	})
	getDaemonStatus = func(context.Context) (morphcli.DaemonStatus, error) {
		t.Fatal("status should not run for start")
		return morphcli.DaemonStatus{}, nil
	}

	err := NewCommand().Run(context.Background(), []string{"daemon", "start"})

	require.EqualError(t, err, `unknown daemon command "start"`)
}

func TestWriteDaemonStatusReturnsWriteError(t *testing.T) {
	err := writeDaemonStatus(errWriter{}, morphcli.DaemonStatus{State: "running"})

	require.ErrorIs(t, err, errDaemonTestWrite)
}

func TestWriteDaemonStatusDefaultsEmptyState(t *testing.T) {
	var output bytes.Buffer

	err := writeDaemonStatus(&output, morphcli.DaemonStatus{})

	require.NoError(t, err)
	require.Contains(t, output.String(), "State:       unknown")
}
