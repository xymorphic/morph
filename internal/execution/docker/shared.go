package docker

import (
	"context"
	"errors"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"

	"github.com/wandxy/morph/internal/execution"
)

type sharedEnvironment struct {
	containerID string
	incarnation string
}

func (b *Backend) execute(
	ctx context.Context,
	spec execution.Spec,
	resourceKind string,
	command []string,
	standardInput []byte,
) (execution.CommandResult, error) {
	if b.executeOverride != nil {
		return b.executeOverride(ctx, spec, resourceKind, command, standardInput)
	}
	if runtimeLimit := spec.Exposure().Limits().Runtime; runtimeLimit > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, runtimeLimit)
		defer cancel()
	}
	if spec.Exposure().Scope() == execution.ScopeShared &&
		len(spec.Exposure().SecretReferences()) == 0 {
		return b.runShared(ctx, spec, command, standardInput)
	}
	return b.runDisposable(ctx, spec, resourceKind, command, standardInput)
}

func (b *Backend) ensureSharedLocked(
	ctx context.Context,
	spec execution.Spec,
	workspaceVolume string,
) (*sharedEnvironment, error) {
	key := getEnvironmentKey(spec, b.daemonIncarnation)
	b.mu.Lock()
	environment := b.environments[key]
	b.mu.Unlock()
	if environment != nil && environment.containerID != "" {
		if inspect, err := b.client.Engine().ContainerInspect(
			ctx,
			environment.containerID,
			client.ContainerInspectOptions{},
		); err == nil &&
			inspect.Container.State != nil &&
			inspect.Container.State.Running {
			return environment, nil
		}

		_ = b.removeContainer(environment.containerID, spec.Exposure().Limits().StopGrace)
	}

	incarnation, err := newDockerIncarnation()
	if err != nil {
		return nil, err
	}
	networkName, err := b.ensureNetwork(ctx, spec)
	if err != nil {
		return nil, err
	}

	options, err := BuildContainerOptions(ContainerInput{
		Spec:                 spec,
		Image:                b.image,
		Contract:             b.contract,
		DaemonIncarnation:    b.daemonIncarnation,
		ContainerIncarnation: incarnation,
		ResourceKind:         "shared",
		WorkspaceVolume:      workspaceVolume,
		NetworkName:          networkName,
		Command:              []string{"sleep-forever"},
	})
	if err != nil {
		return nil, err
	}
	options.Config.AttachStdout = false
	options.Config.AttachStderr = false

	created, err := b.client.Engine().ContainerCreate(ctx, options)
	if err != nil {
		return nil, err
	}
	if _, err := b.client.Engine().ContainerStart(
		ctx,
		created.ID,
		client.ContainerStartOptions{},
	); err != nil {
		_ = b.removeContainer(created.ID, spec.Exposure().Limits().StopGrace)
		return nil, err
	}
	environment = &sharedEnvironment{
		containerID: created.ID,
		incarnation: incarnation,
	}
	b.mu.Lock()
	b.environments[key] = environment
	b.mu.Unlock()
	b.setStatus(b.getStatus(spec, execution.EnvironmentReady, incarnation))
	return environment, nil
}

func (b *Backend) runShared(
	ctx context.Context,
	spec execution.Spec,
	command []string,
	standardInput []byte,
) (execution.CommandResult, error) {
	if _, err := b.Acquire(ctx, spec); err != nil {
		return execution.CommandResult{}, err
	}

	key := getEnvironmentKey(spec, b.daemonIncarnation)
	gate := b.getSharedGate(key)
	select {
	case <-ctx.Done():
		return execution.CommandResult{}, ctx.Err()
	case <-gate:
	}
	defer func() { gate <- struct{}{} }()

	b.mu.Lock()
	closing := b.closing
	b.mu.Unlock()
	if closing {
		return execution.CommandResult{}, errors.New("docker execution backend is closing")
	}

	workspaceVolume := getWorkspaceVolumeName(spec)
	lock := b.getEnvironmentLock(key)
	lock.Lock()
	current, err := b.ensureSharedLocked(ctx, spec, workspaceVolume)
	lock.Unlock()
	if err != nil {
		return execution.CommandResult{}, err
	}

	b.setStatus(b.getStatus(spec, execution.EnvironmentRunning, current.incarnation))
	defer b.setStatus(b.getStatus(spec, execution.EnvironmentReady, current.incarnation))

	planCommand, arguments, cwd, environmentValues, planErr := getContainerCommand(spec)
	if planErr != nil {
		cwd = "/workspace"
		environmentValues = nil
	} else if len(command) == 0 {
		command = append([]string{planCommand}, arguments...)
	}

	cmd := append([]string{b.contract.Helper}, command...)
	workingDirectory := mapWorkingDirectory(cwd, spec.Exposure())
	if cwd != "" && workingDirectory == "" {
		return execution.CommandResult{}, errors.New(
			"docker command working directory is outside configured mounts",
		)
	}

	created, err := b.client.Engine().ExecCreate(ctx, current.containerID, client.ExecCreateOptions{
		User:         b.contract.User,
		Privileged:   false,
		TTY:          false,
		AttachStdin:  len(standardInput) > 0,
		AttachStdout: true,
		AttachStderr: true,
		Env:          environmentValues,
		WorkingDir:   workingDirectory,
		Cmd:          cmd,
	})
	if err != nil {
		return execution.CommandResult{}, err
	}
	attached, err := b.client.Engine().
		ExecAttach(ctx, created.ID, client.ExecAttachOptions{TTY: false})
	if err != nil {
		return execution.CommandResult{}, err
	}
	defer attached.Close()

	stdout := newBoundedWriter(int(spec.Exposure().Limits().OutputBytes), nil)
	stderr := newBoundedWriter(int(spec.Exposure().Limits().OutputBytes), nil)
	done := make(chan error, 1)
	started := time.Now()

	go func() {
		_, copyErr := stdcopy.StdCopy(stdout, stderr, attached.Reader)
		done <- copyErr
	}()
	if len(standardInput) > 0 {
		if _, err := attached.Conn.Write(standardInput); err != nil {
			return execution.CommandResult{}, err
		}
		_ = attached.CloseWrite()
	}

	result := execution.CommandResult{}
	select {
	case copyErr := <-done:
		if copyErr != nil {
			return execution.CommandResult{}, copyErr
		}
		inspect, inspectErr := b.client.Engine().
			ExecInspect(context.WithoutCancel(ctx), created.ID, client.ExecInspectOptions{})
		if inspectErr != nil {
			return execution.CommandResult{}, inspectErr
		}
		result.ExitCode = inspect.ExitCode
	case <-ctx.Done():
		result.ExitCode = -1
		result.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
		result.Interrupted = errors.Is(ctx.Err(), context.Canceled)
		if err := b.recreateSharedLocked(spec, current); err != nil {
			return execution.CommandResult{}, err
		}
	}
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()
	result.Duration = time.Since(started)

	return result, nil
}

func (b *Backend) recreateSharedLocked(spec execution.Spec, current *sharedEnvironment) error {
	b.record(spec, "execution.environment.recreating", nil)
	key := getEnvironmentKey(spec, b.daemonIncarnation)
	lock := b.getEnvironmentLock(key)
	lock.Lock()
	defer lock.Unlock()
	if current != nil && current.containerID != "" {
		_ = b.removeContainer(current.containerID, spec.Exposure().Limits().StopGrace)
	}
	b.mu.Lock()
	delete(b.environments, key)
	closing := b.closing
	b.mu.Unlock()
	if closing {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := b.ensureSharedLocked(ctx, spec, getWorkspaceVolumeName(spec))
	if err != nil {
		status := b.getStatus(spec, execution.EnvironmentUnhealthy, "")
		status.FailureCode = "shared_recreation_failed"
		b.setStatus(status)
	}
	if err == nil {
		b.record(spec, "execution.environment.recreated", nil)
	}
	return err
}

func (b *Backend) getSharedGate(key string) chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	gate := b.sharedGates[key]
	if gate == nil {
		gate = make(chan struct{}, 1)
		gate <- struct{}{}
		b.sharedGates[key] = gate
	}
	return gate
}
