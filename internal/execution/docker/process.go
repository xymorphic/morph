package docker

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	processenv "github.com/wandxy/morph/internal/environment/process"
	"github.com/wandxy/morph/internal/execution"
)

type dockerProcess struct {
	mu          sync.Mutex
	owner       execution.Owner
	generation  string
	containerID string
	incarnation string
	info        processenv.Info
	stdout      *boundedWriter
	stderr      *boundedWriter
	done        chan struct{}
	shared      bool
	token       string
	spec        execution.Spec
}

var processStopTimeout = 10 * time.Second

var processOutputDrainTimeout = time.Second

func (b *Backend) StartProcess(ctx context.Context, spec execution.Spec) (processenv.Info, error) {
	operation := spec.Operation().Process
	if operation == nil || operation.Action != execution.ProcessStart || operation.Plan == nil {
		return processenv.Info{}, errors.New("docker process start specification is invalid")
	}
	if len(b.processKey) < 32 {
		return processenv.Info{}, errors.New("docker process identity key is unavailable")
	}

	if spec.Exposure().Scope() == execution.ScopeShared &&
		len(spec.Exposure().SecretReferences()) == 0 {
		return b.startSharedProcess(ctx, spec)
	}
	if _, err := b.Acquire(ctx, spec); err != nil {
		return processenv.Info{}, err
	}

	command, arguments, _, _, _ := getContainerCommand(spec)

	commandLine := append([]string{command}, arguments...)

	incarnation, err := newDockerIncarnation()
	if err != nil {
		return processenv.Info{}, err
	}

	token, err := newDockerIncarnation()
	if err != nil {
		return processenv.Info{}, err
	}

	codec, err := execution.NewProcessCodec(
		b.processKey,
		spec.Exposure().SecurityGeneration(),
		b.daemonIncarnation,
	)
	if err != nil {
		return processenv.Info{}, err
	}

	handle, err := codec.Encode(spec.Owner(), incarnation, token)
	if err != nil {
		return processenv.Info{}, err
	}
	var standardInput []byte
	secretValues := []string(nil)
	if references := spec.Exposure().SecretReferences(); len(references) > 0 {
		if b.secretResolver == nil {
			return processenv.Info{}, errors.New("execution secret resolver is unavailable")
		}

		resolved, resolveErr := b.secretResolver.Resolve(references)
		if resolveErr != nil {
			return processenv.Info{}, resolveErr
		}
		standardInput, err = encodeControlFrame(
			resolved.Values,
			spec.Exposure().Limits().ControlInputBytes,
		)
		if err != nil {
			return processenv.Info{}, err
		}

		commandLine = append(
			[]string{
				"--control",
				strconv.FormatInt(spec.Exposure().Limits().ControlInputBytes, 10),
			},
			commandLine...,
		)

		for _, value := range resolved.Values {
			secretValues = append(secretValues, value)
		}
	}

	workspaceVolume := getWorkspaceVolumeName(spec)
	if err := b.ensureWorkspace(ctx, spec, workspaceVolume); err != nil {
		return processenv.Info{}, err
	}
	networkName, err := b.ensureNetwork(ctx, spec)
	if err != nil {
		return processenv.Info{}, err
	}

	options, err := BuildContainerOptions(ContainerInput{
		Spec:                 spec,
		Image:                b.image,
		Contract:             b.contract,
		DaemonIncarnation:    b.daemonIncarnation,
		ContainerIncarnation: incarnation,
		ResourceKind:         "process",
		WorkspaceVolume:      workspaceVolume,
		NetworkName:          networkName,
		Command:              commandLine,
		OpenStdin:            len(standardInput) > 0,
	})
	if err != nil {
		return processenv.Info{}, err
	}

	engine := b.client.Engine()
	created, err := engine.ContainerCreate(ctx, options)
	if err != nil {
		return processenv.Info{}, err
	}

	cleanupCreated := true
	defer func() {
		if cleanupCreated {
			_ = b.removeContainer(created.ID, spec.Exposure().Limits().StopGrace)
		}
	}()

	attached, err := engine.ContainerAttach(ctx, created.ID, client.ContainerAttachOptions{
		Stream: true,
		Stdin:  len(standardInput) > 0,
		Stdout: true,
		Stderr: true,
	})
	if err != nil {
		return processenv.Info{}, err
	}

	limit := operation.OutputBufferBytes
	if limit <= 0 || int64(limit) > spec.Exposure().Limits().OutputBytes {
		limit = int(spec.Exposure().Limits().OutputBytes)
	}

	process := &dockerProcess{
		owner:       spec.Owner(),
		generation:  spec.Exposure().SecurityGeneration(),
		containerID: created.ID,
		incarnation: incarnation,
		stdout:      newBoundedWriter(limit, secretValues),
		stderr:      newBoundedWriter(limit, secretValues),
		done:        make(chan struct{}),
		info: processenv.Info{
			ID:        handle,
			Label:     operation.Label,
			Command:   operation.Plan.Summary(),
			CWD:       operation.Plan.CWD,
			Status:    processenv.StatusRunning,
			StartedAt: time.Now().UTC(),
		},
	}

	b.mu.Lock()
	ownerKey := spec.Owner().Fingerprint()
	if operation.Label != "" && b.hasProcessLabelLocked(ownerKey, operation.Label) {
		b.mu.Unlock()
		attached.Close()
		return processenv.Info{}, errors.New("process label already exists")
	}
	b.processes[handle] = process
	b.processOrder[ownerKey] = append(b.processOrder[ownerKey], handle)
	b.mu.Unlock()

	copyDone := make(chan error, 1)
	go func() {
		_, copyErr := stdcopy.StdCopy(process.stdout, process.stderr, attached.Reader)
		copyDone <- copyErr
	}()

	if _, err := engine.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		b.forgetProcess(handle)
		attached.Close()
		return processenv.Info{}, err
	}
	if len(standardInput) > 0 {
		if _, err := attached.Conn.Write(standardInput); err != nil {
			b.forgetProcess(handle)
			attached.Close()
			return processenv.Info{}, err
		}
		_ = attached.CloseWrite()
	}

	cleanupCreated = false
	go b.waitProcess(
		process,
		attached.HijackedResponse,
		copyDone,
		spec.Exposure().Limits().StopGrace,
	)
	return process.snapshot(), nil
}

func (b *Backend) GetProcess(ctx context.Context, spec execution.Spec) (processenv.Info, error) {
	process, err := b.getProcess(spec, spec.Operation().Process.ProcessID)
	if err != nil {
		return processenv.Info{}, err
	}
	if process.shared {
		return b.refreshSharedProcess(ctx, spec, process)
	}
	return process.snapshot(), nil
}

func (b *Backend) ReadProcess(
	ctx context.Context,
	spec execution.Spec,
	req processenv.ReadRequest,
) (processenv.Output, error) {
	process, err := b.getProcess(spec, req.ProcessID)
	if err != nil {
		return processenv.Output{}, err
	}
	if process.shared {
		result, runErr := b.execute(
			ctx,
			spec,
			"process-control",
			[]string{"supervisor-read", process.token},
			nil,
		)
		if runErr != nil {
			return processenv.Output{}, runErr
		}
		if result.ExitCode != 0 {
			return processenv.Output{}, errors.New(result.Stderr)
		}
		var payload struct {
			Stdout string `json:"stdout"`
			Stderr string `json:"stderr"`
		}
		if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil {
			return processenv.Output{}, err
		}
		stdoutCursor := getCursor(req.StdoutCursor, len(payload.Stdout))
		stderrCursor := getCursor(req.StderrCursor, len(payload.Stderr))
		return processenv.Output{
			Stdout:           payload.Stdout[stdoutCursor:],
			Stderr:           payload.Stderr[stderrCursor:],
			StdoutBytes:      len(payload.Stdout),
			StderrBytes:      len(payload.Stderr),
			NextStdoutCursor: len(payload.Stdout),
			NextStderrCursor: len(payload.Stderr),
		}, nil
	}
	stdout := process.stdout.Bytes()
	stderr := process.stderr.Bytes()
	stdoutCursor := getCursor(req.StdoutCursor, len(stdout))
	stderrCursor := getCursor(req.StderrCursor, len(stderr))
	return processenv.Output{
		Stdout:           string(stdout[stdoutCursor:]),
		Stderr:           string(stderr[stderrCursor:]),
		StdoutBytes:      process.stdout.Total(),
		StderrBytes:      process.stderr.Total(),
		NextStdoutCursor: len(stdout),
		NextStderrCursor: len(stderr),
		StdoutTruncated:  process.stdout.Truncated(),
		StderrTruncated:  process.stderr.Truncated(),
	}, nil
}

func (b *Backend) StopProcess(ctx context.Context, spec execution.Spec) (processenv.Info, error) {
	processID := spec.Operation().Process.ProcessID
	process, err := b.getProcess(spec, processID)
	if err != nil {
		return processenv.Info{}, err
	}
	if process.shared {
		result, runErr := b.execute(
			ctx,
			spec,
			"process-control",
			[]string{"supervisor-stop", process.token},
			nil,
		)
		if runErr != nil {
			return processenv.Info{}, runErr
		}
		if result.ExitCode != 0 {
			key := getEnvironmentKey(spec, b.daemonIncarnation)
			b.mu.Lock()
			current := b.environments[key]
			b.mu.Unlock()
			if recreateErr := b.recreateSharedLocked(spec, current); recreateErr != nil {
				return processenv.Info{}, errors.Join(errors.New(result.Stderr), recreateErr)
			}
			process.mu.Lock()
			process.info.Status = processenv.StatusStopped
			endedAt := time.Now().UTC()
			process.info.EndedAt = &endedAt
			process.mu.Unlock()
			return process.snapshot(), nil
		}
		return b.refreshSharedProcess(ctx, spec, process)
	}
	process.mu.Lock()
	running := process.info.Status == processenv.StatusRunning
	process.mu.Unlock()
	if running {
		seconds := int(spec.Exposure().Limits().StopGrace.Seconds())
		_, err := b.client.Engine().ContainerStop(
			ctx,
			process.containerID,
			client.ContainerStopOptions{
				Timeout: &seconds,
			},
		)
		if err != nil && !isNotFound(err) {
			return processenv.Info{}, err
		}
		select {
		case <-process.done:
		case <-ctx.Done():
			return processenv.Info{}, ctx.Err()
		case <-time.After(processStopTimeout):
			return processenv.Info{}, errors.New("docker process stop did not become terminal")
		}
		process.mu.Lock()
		process.info.Status = processenv.StatusStopped
		process.mu.Unlock()
	}
	return process.snapshot(), nil
}

func (b *Backend) ListProcesses(
	ctx context.Context,
	spec execution.Spec,
) ([]processenv.Info, error) {
	ownerKey := spec.Owner().Fingerprint()
	b.mu.Lock()
	handles := slices.Clone(b.processOrder[ownerKey])
	processes := make([]*dockerProcess, 0, len(handles))
	for _, handle := range handles {
		if process := b.processes[handle]; process != nil {
			processes = append(processes, process)
		}
	}
	b.mu.Unlock()
	result := make([]processenv.Info, 0, len(processes))
	for _, process := range processes {
		if process.shared {
			info, err := b.refreshSharedProcess(ctx, spec, process)
			if err == nil {
				result = append(result, info)
			}
		} else {
			result = append(result, process.snapshot())
		}
	}
	return result, nil
}

func (b *Backend) getProcess(spec execution.Spec, value string) (*dockerProcess, error) {
	owner := spec.Owner()
	value = strings.TrimSpace(value)
	b.mu.Lock()
	process := b.processes[value]
	if process == nil {
		for _, handle := range b.processOrder[owner.Fingerprint()] {
			candidate := b.processes[handle]
			if candidate != nil && candidate.info.Label == value {
				process = candidate
				value = handle
				break
			}
		}
	}
	b.mu.Unlock()
	if process != nil {
		if process.generation != spec.Exposure().SecurityGeneration() {
			return nil, execution.ErrProcessStale
		}
		if process.shared {
			key := getEnvironmentKey(process.spec, b.daemonIncarnation)
			b.mu.Lock()
			environment := b.environments[key]
			b.mu.Unlock()
			if environment == nil || environment.incarnation != process.incarnation {
				return nil, execution.ErrProcessStale
			}
		}
		codec, err := execution.NewProcessCodec(
			b.processKey,
			process.generation,
			b.daemonIncarnation,
		)
		if err != nil {
			return nil, err
		}
		if _, err := codec.Decode(value, owner, process.incarnation); err != nil {
			return nil, err
		}
		return process, nil
	}
	if strings.Contains(value, ".") {
		codec, err := execution.NewProcessCodec(
			b.processKey,
			spec.Exposure().SecurityGeneration(),
			b.daemonIncarnation,
		)
		if err != nil {
			return nil, err
		}
		identity, decodeErr := codec.Verify(value)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if identity.DaemonIncarnation != b.daemonIncarnation ||
			identity.SecurityGeneration != spec.Exposure().SecurityGeneration() {
			return nil, execution.ErrProcessStale
		}
		if identity.OwnerFingerprint != owner.Fingerprint() {
			return nil, execution.ErrProcessDenied
		}
	} else if value != "" {
		return nil, execution.ErrInvalidProcessID
	}
	return nil, execution.ErrProcessNotFound
}

func (b *Backend) startSharedProcess(
	ctx context.Context,
	spec execution.Spec,
) (processenv.Info, error) {
	operation := spec.Operation().Process
	if _, err := b.Acquire(ctx, spec); err != nil {
		return processenv.Info{}, err
	}
	command, arguments, cwd, environment, _ := getContainerCommand(spec)
	outputBytes := spec.Exposure().Limits().OutputBytes / 4
	if outputBytes < 1024 {
		outputBytes = 1024
	}
	workingDirectory := mapWorkingDirectory(cwd, spec.Exposure())
	if cwd != "" && workingDirectory == "" {
		return processenv.Info{}, errors.New(
			"docker process working directory is outside configured mounts",
		)
	}
	request, _ := json.Marshal(struct {
		Command     []string `json:"command"`
		CWD         string   `json:"cwd"`
		Env         []string `json:"env"`
		Label       string   `json:"label"`
		OutputBytes int64    `json:"output_bytes"`
	}{
		Command:     append([]string{command}, arguments...),
		CWD:         workingDirectory,
		Env:         environment,
		Label:       operation.Label,
		OutputBytes: outputBytes,
	})
	result, err := b.execute(ctx, spec, "process-control", []string{"supervisor-start"}, request)
	if err != nil {
		return processenv.Info{}, err
	}
	if result.ExitCode != 0 {
		return processenv.Info{}, errors.New(result.Stderr)
	}

	var state sharedProcessState
	if err := json.Unmarshal([]byte(result.Stdout), &state); err != nil {
		return processenv.Info{}, err
	}
	key := getEnvironmentKey(spec, b.daemonIncarnation)
	b.mu.Lock()
	environmentUnit := b.environments[key]
	b.mu.Unlock()
	if environmentUnit == nil {
		return processenv.Info{}, errors.New(
			"shared Docker environment disappeared during process start",
		)
	}
	codec, err := execution.NewProcessCodec(
		b.processKey,
		spec.Exposure().SecurityGeneration(),
		b.daemonIncarnation,
	)
	if err != nil {
		return processenv.Info{}, err
	}
	handle, err := codec.Encode(spec.Owner(), environmentUnit.incarnation, state.Token)
	if err != nil {
		return processenv.Info{}, err
	}

	process := &dockerProcess{
		owner:       spec.Owner(),
		generation:  spec.Exposure().SecurityGeneration(),
		incarnation: environmentUnit.incarnation,
		containerID: environmentUnit.containerID,
		shared:      true,
		token:       state.Token,
		spec:        spec,
		done:        make(chan struct{}),
		stdout:      newBoundedWriter(int(spec.Exposure().Limits().OutputBytes), nil),
		stderr:      newBoundedWriter(int(spec.Exposure().Limits().OutputBytes), nil),
		info: processenv.Info{
			ID:        handle,
			Label:     operation.Label,
			Command:   operation.Plan.Summary(),
			CWD:       operation.Plan.CWD,
			Status:    processenv.StatusRunning,
			StartedAt: state.StartedAt,
		},
	}

	b.mu.Lock()
	ownerKey := spec.Owner().Fingerprint()
	if operation.Label != "" && b.hasProcessLabelLocked(ownerKey, operation.Label) {
		b.mu.Unlock()
		_, _ = b.execute(
			ctx,
			spec,
			"process-control",
			[]string{"supervisor-stop", state.Token},
			nil,
		)
		return processenv.Info{}, errors.New("process label already exists")
	}
	b.processes[handle] = process
	b.processOrder[ownerKey] = append(b.processOrder[ownerKey], handle)
	b.mu.Unlock()
	return process.snapshot(), nil
}

type sharedProcessState struct {
	Token     string     `json:"token"`
	PID       int        `json:"pid"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at"`
	ExitCode  *int       `json:"exit_code"`
	Stopped   bool       `json:"stopped"`
}

func (b *Backend) refreshSharedProcess(
	ctx context.Context,
	spec execution.Spec,
	process *dockerProcess,
) (processenv.Info, error) {
	result, err := b.execute(
		ctx,
		spec,
		"process-control",
		[]string{"supervisor-status", process.token},
		nil,
	)
	if err != nil {
		return processenv.Info{}, err
	}
	if result.ExitCode != 0 {
		return processenv.Info{}, errors.New(result.Stderr)
	}
	var state sharedProcessState
	if err := json.Unmarshal([]byte(result.Stdout), &state); err != nil {
		return processenv.Info{}, err
	}
	process.mu.Lock()
	process.info.EndedAt = state.EndedAt
	process.info.ExitCode = state.ExitCode
	if state.EndedAt != nil {
		if state.Stopped {
			process.info.Status = processenv.StatusStopped
		} else {
			process.info.Status = processenv.StatusExited
		}
	}
	process.mu.Unlock()
	return process.snapshot(), nil
}

func (b *Backend) waitProcess(
	process *dockerProcess,
	attached client.HijackedResponse,
	copyDone <-chan error,
	grace time.Duration,
) {
	wait := b.client.Engine().
		ContainerWait(context.Background(), process.containerID, client.ContainerWaitOptions{
			Condition: container.WaitConditionNotRunning,
		})
	exitCode := -1
	select {
	case response := <-wait.Result:
		exitCode = int(response.StatusCode)
	case <-wait.Error:
	}
	attached.Close()
	select {
	case <-copyDone:
	case <-time.After(processOutputDrainTimeout):
	}
	_ = process.stdout.String()
	_ = process.stderr.String()
	process.mu.Lock()
	endedAt := time.Now().UTC()
	process.info.EndedAt = &endedAt
	process.info.ExitCode = &exitCode
	process.info.StdoutBytes = process.stdout.Total()
	process.info.StderrBytes = process.stderr.Total()
	process.info.StdoutTruncated = process.stdout.Truncated()
	process.info.StderrTruncated = process.stderr.Truncated()
	if process.info.Status == processenv.StatusRunning {
		process.info.Status = processenv.StatusExited
	}
	process.mu.Unlock()
	_ = b.removeContainer(process.containerID, grace)
	close(process.done)
}

func (b *Backend) hasProcessLabelLocked(ownerKey string, label string) bool {
	for _, handle := range b.processOrder[ownerKey] {
		if process := b.processes[handle]; process != nil && process.info.Label == label {
			return true
		}
	}
	return false
}

func (b *Backend) forgetProcess(handle string) {
	b.mu.Lock()
	delete(b.processes, handle)
	for owner, handles := range b.processOrder {
		for index, candidate := range handles {
			if candidate == handle {
				b.processOrder[owner] = append(handles[:index], handles[index+1:]...)
				break
			}
		}
	}
	b.mu.Unlock()
}

func (p *dockerProcess) snapshot() processenv.Info {
	p.mu.Lock()
	defer p.mu.Unlock()
	info := p.info
	info.Args = slices.Clone(info.Args)
	if info.ExitCode != nil {
		value := *info.ExitCode
		info.ExitCode = &value
	}
	if info.EndedAt != nil {
		value := *info.EndedAt
		info.EndedAt = &value
	}
	return info
}

func getCursor(value *int, length int) int {
	if value == nil || *value < 0 {
		return 0
	}
	if *value > length {
		return length
	}
	return *value
}

func (b *Backend) CloseOwner(ctx context.Context, owner execution.Owner) error {
	b.mu.Lock()
	handles := slices.Clone(b.processOrder[owner.Fingerprint()])
	b.mu.Unlock()
	var joined error
	for _, handle := range handles {
		b.mu.Lock()
		process := b.processes[handle]
		b.mu.Unlock()
		if process == nil || process.owner.Fingerprint() != owner.Fingerprint() {
			continue
		}
		if process.snapshot().Status == processenv.StatusRunning {
			if process.shared {
				result, stopErr := b.execute(
					ctx,
					process.spec,
					"process-control",
					[]string{"supervisor-stop", process.token},
					nil,
				)
				if stopErr == nil && result.ExitCode != 0 {
					stopErr = errors.New(result.Stderr)
				}
				joined = errors.Join(joined, stopErr)
			} else {
				joined = errors.Join(joined, b.removeContainer(process.containerID, 3*time.Second))
			}
		}
	}
	return joined
}

func (b *Backend) CloseSession(
	ctx context.Context,
	profile string,
	sessionID string,
	removeWorkspace bool,
) error {
	b.mu.Lock()
	type ownedProcess struct {
		handle  string
		process *dockerProcess
	}
	processes := make([]ownedProcess, 0)
	for handle, process := range b.processes {
		if process.owner.Profile == profile && process.owner.EffectiveSessionID == sessionID {
			processes = append(processes, ownedProcess{
				handle:  handle,
				process: process,
			})
		}
	}
	b.mu.Unlock()
	var joined error
	for _, owned := range processes {
		process := owned.process
		if process.shared {
			key := getEnvironmentKey(process.spec, b.daemonIncarnation)
			b.mu.Lock()
			current := b.environments[key]
			b.mu.Unlock()
			if current == nil || current.incarnation != process.incarnation {
				b.forgetProcess(owned.handle)
				continue
			}
		}
		if process.snapshot().Status != processenv.StatusRunning {
			b.forgetProcess(owned.handle)
			continue
		}
		if process.shared {
			result, err := b.execute(
				ctx,
				process.spec,
				"process-control",
				[]string{"supervisor-stop", process.token},
				nil,
			)
			if err == nil && result.ExitCode != 0 {
				err = errors.New(result.Stderr)
			}
			joined = errors.Join(joined, err)
		} else {
			joined = errors.Join(joined, b.removeContainer(process.containerID, 3*time.Second))
		}
		b.forgetProcess(owned.handle)
	}
	joined = errors.Join(joined, b.removeSessionResources(ctx, profile, sessionID, removeWorkspace))
	return joined
}
