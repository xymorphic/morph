package common

import (
	"context"
	"path/filepath"

	"github.com/xymorphic/morph/internal/execution"
	"github.com/xymorphic/morph/internal/guardrails"
	"github.com/xymorphic/morph/internal/tools"
	"github.com/xymorphic/morph/internal/trace"
)

func ObserveExecution[T any](
	ctx context.Context,
	spec execution.Spec,
	operation func() (T, error),
) (T, error) {
	recorder := tools.TraceRecorderFromContext(ctx)
	RecordExecutionTrace(recorder, trace.EvtExecutionStarted, spec, execution.CommandResult{}, nil)
	value, err := operation()
	if err != nil {
		RecordExecutionTrace(
			recorder,
			trace.EvtExecutionFailed,
			spec,
			execution.CommandResult{},
			err,
		)
		return value, err
	}
	RecordExecutionTrace(
		recorder,
		trace.EvtExecutionCompleted,
		spec,
		execution.CommandResult{},
		nil,
	)
	return value, nil
}

func RecordExecutionTrace(
	ctxRecorder tools.TraceRecorder,
	event string,
	spec execution.Spec,
	result execution.CommandResult,
	err error,
) {
	if ctxRecorder == nil {
		return
	}
	exposure := spec.Exposure()
	mounts := make([]string, 0, len(exposure.Mounts()))
	for _, mount := range exposure.Mounts() {
		mounts = append(mounts, filepath.ToSlash(mount.Target)+":"+string(mount.Mode))
	}
	payload := trace.ExecutionEventPayload{
		ExecutionID:        spec.Digest(),
		Backend:            string(exposure.Backend()),
		Scope:              string(exposure.Scope()),
		Operation:          string(spec.Operation().Kind),
		ImageDigest:        exposure.ImageDigest(),
		PolicyHash:         exposure.PolicyHash(),
		Mounts:             mounts,
		Network:            string(exposure.Network()),
		SecretReferences:   exposure.SecretReferences(),
		SecurityGeneration: exposure.SecurityGeneration(),
		ExitCode:           result.ExitCode,
		DurationMS:         result.Duration.Milliseconds(),
		StdoutBytes:        len(result.Stdout),
		StderrBytes:        len(result.Stderr),
		TimedOut:           result.TimedOut,
		Interrupted:        result.Interrupted,
	}
	if err != nil {
		if sanitized, ok := guardrails.Sanitize(err.Error()).(string); ok {
			payload.Error = sanitized
		}
	}
	ctxRecorder.Record(event, payload)
}
