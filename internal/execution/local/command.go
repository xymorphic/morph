package local

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"time"

	"github.com/xymorphic/morph/internal/execution"
)

var waitLocalCommand = func(command *exec.Cmd) error {
	return command.Wait()
}

func (b *Backend) Run(ctx context.Context, spec execution.Spec) (execution.CommandResult, error) {
	operation := spec.Operation()
	if operation.Command == nil {
		return execution.CommandResult{}, errors.New("local command specification is invalid")
	}
	b.rememberOwner(spec.Owner())
	cmd, err := operation.Command.NewCommand(context.Background())
	if err != nil {
		return execution.CommandResult{}, err
	}
	configureCommandProcess(cmd)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	started := time.Now()
	if err := cmd.Start(); err != nil {
		return execution.CommandResult{}, err
	}
	done := make(chan error, 1)
	go func() { done <- waitLocalCommand(cmd) }()
	select {
	case err = <-done:
	case <-ctx.Done():
		terminateCommandProcess(cmd)
		err = <-done
	}
	result := execution.CommandResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: time.Since(started),
	}
	if ctx.Err() != nil {
		result.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
		result.Interrupted = errors.Is(ctx.Err(), context.Canceled)
		result.ExitCode = -1
		return result, nil
	}
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return execution.CommandResult{}, err
		}
		result.ExitCode = exitErr.ExitCode()
	}
	return result, nil
}
