package docker

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	processenv "github.com/wandxy/morph/internal/environment/process"
	"github.com/wandxy/morph/internal/execution"
)

type Backend struct {
	client              *Client
	image               string
	contract            execution.ImageContract
	daemonIncarnation   string
	secretResolver      *SecretResolver
	processKey          []byte
	mu                  sync.Mutex
	admissionMu         sync.Mutex
	statuses            map[string]execution.EnvironmentStatus
	processes           map[string]*dockerProcess
	processOrder        map[string][]string
	environments        map[string]*sharedEnvironment
	sharedGates         map[string]chan struct{}
	environmentLocks    map[string]*sync.Mutex
	networks            map[string]struct{}
	reconciledProfiles  map[string]struct{}
	maximumEnvironments int
	maximumVolumes      int
	reservedFreeBytes   int64
	endpoint            string
	configuredScope     execution.Scope
	sharedRetention     time.Duration
	sharedDisabledAt    time.Time
	sessionExists       func(context.Context, string) (bool, error)
	allowTestImage      bool
	imageVerificationMu sync.Mutex
	imageVerified       bool
	closing             bool
	recordLifecycle     func(string, string, any)
	executeOverride     func(
		context.Context,
		execution.Spec,
		string,
		[]string,
		[]byte,
	) (execution.CommandResult, error)
}

type BackendOptions struct {
	Endpoint             string
	Image                string
	Contract             execution.ImageContract
	DaemonIncarnation    string
	SecretResolver       *SecretResolver
	ProcessIdentityKey   []byte
	AllowTestImageTag    bool
	MaximumEnvironments  int
	MaximumVolumes       int
	ReservedFreeBytes    int64
	ConfiguredScope      execution.Scope
	SharedRetention      time.Duration
	SharedDisabledMarker string
	SessionExists        func(context.Context, string) (bool, error)
	RecordLifecycle      func(string, string, any)
}

var newDockerIncarnation = execution.NewIncarnation

var writeSharedDisabledMarker = os.WriteFile

var mkdirSharedDisabledMarker = os.MkdirAll

var containerRemovalConflictTimeout = time.Second

var containerRemovalPollInterval = 10 * time.Millisecond

var containerOutputDrainTimeout = time.Second

func NewBackend(options BackendOptions) (*Backend, error) {
	if err := ValidateImageReference(options.Image); err != nil && !options.AllowTestImageTag {
		return nil, err
	}
	if strings.TrimSpace(options.Image) == "" {
		return nil, errors.New("docker sandbox image is required")
	}
	contract, err := options.Contract.Normalize()
	if err != nil {
		return nil, err
	}
	if options.DaemonIncarnation == "" {
		return nil, errors.New("docker daemon incarnation is required")
	}
	engine, err := NewClient(options.Endpoint)
	if err != nil {
		return nil, err
	}
	sharedDisabledAt, err := loadSharedDisabledAt(
		options.ConfiguredScope,
		options.SharedDisabledMarker,
	)
	if err != nil {
		_ = engine.Close()
		return nil, err
	}
	backend := &Backend{
		client:              engine,
		image:               options.Image,
		contract:            contract,
		daemonIncarnation:   options.DaemonIncarnation,
		secretResolver:      options.SecretResolver,
		processKey:          append([]byte(nil), options.ProcessIdentityKey...),
		statuses:            map[string]execution.EnvironmentStatus{},
		processes:           map[string]*dockerProcess{},
		processOrder:        map[string][]string{},
		environments:        map[string]*sharedEnvironment{},
		sharedGates:         map[string]chan struct{}{},
		environmentLocks:    map[string]*sync.Mutex{},
		networks:            map[string]struct{}{},
		reconciledProfiles:  map[string]struct{}{},
		maximumEnvironments: options.MaximumEnvironments,
		maximumVolumes:      options.MaximumVolumes,
		reservedFreeBytes:   options.ReservedFreeBytes,
		endpoint:            options.Endpoint,
		configuredScope:     options.ConfiguredScope,
		sharedRetention:     options.SharedRetention,
		sharedDisabledAt:    sharedDisabledAt,
		sessionExists:       options.SessionExists,
		allowTestImage:      options.AllowTestImageTag,
		recordLifecycle:     options.RecordLifecycle,
	}
	if !options.AllowTestImageTag {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := backend.verifyImage(ctx); err != nil {
			_ = backend.client.Close()
			return nil, err
		}
	}
	return backend, nil
}

func loadSharedDisabledAt(scope execution.Scope, path string) (time.Time, error) {
	if path == "" {
		return time.Time{}, nil
	}
	if scope == execution.ScopeShared {
		_ = os.Remove(path)
		return time.Time{}, nil
	}
	raw, err := os.ReadFile(path)
	if err == nil {
		return time.Parse(time.RFC3339Nano, strings.TrimSpace(string(raw)))
	}
	if !errors.Is(err, os.ErrNotExist) {
		return time.Time{}, err
	}
	value := time.Now().UTC()
	if err := mkdirSharedDisabledMarker(filepath.Dir(path), 0o700); err != nil {
		return time.Time{}, err
	}
	return value, writeSharedDisabledMarker(
		path,
		[]byte(value.Format(time.RFC3339Nano)),
		0o600,
	)
}

func (b *Backend) Acquire(
	ctx context.Context,
	spec execution.Spec,
) (execution.EnvironmentStatus, error) {
	if b == nil || b.client == nil {
		return execution.EnvironmentStatus{}, errors.New("docker backend is required")
	}
	if spec.Owner().Fingerprint() == "" ||
		spec.Exposure().Backend() != execution.BackendDocker ||
		spec.Exposure().Digest() == "" {
		return execution.EnvironmentStatus{}, errors.New("docker execution specification is invalid")
	}
	b.record(spec, "execution.environment.acquiring", nil)
	b.mu.Lock()
	closing := b.closing
	b.mu.Unlock()
	if closing {
		return execution.EnvironmentStatus{}, errors.New("docker execution backend is closing")
	}
	if _, err := b.client.Ping(ctx); err != nil {
		return execution.EnvironmentStatus{}, fmt.Errorf("docker backend unavailable: %w", err)
	}
	if err := b.verifyImage(ctx); err != nil {
		return execution.EnvironmentStatus{}, err
	}
	b.admissionMu.Lock()
	defer b.admissionMu.Unlock()
	if err := b.cleanupIdle(ctx, spec); err != nil {
		return execution.EnvironmentStatus{}, err
	}
	if err := b.checkEnvironmentAdmission(spec); err != nil {
		return execution.EnvironmentStatus{}, err
	}
	if err := b.reconcileProfile(ctx, spec.Owner().Profile); err != nil {
		return execution.EnvironmentStatus{}, err
	}
	lock := b.getEnvironmentLock(getEnvironmentKey(spec, b.daemonIncarnation))
	lock.Lock()
	defer lock.Unlock()
	workspaceVolume := getWorkspaceVolumeName(spec)
	if err := b.ensureWorkspace(ctx, spec, workspaceVolume); err != nil {
		return execution.EnvironmentStatus{}, err
	}
	if _, err := b.ensureNetwork(ctx, spec); err != nil {
		return execution.EnvironmentStatus{}, err
	}
	if spec.Exposure().Scope() == execution.ScopeShared {
		if _, err := b.ensureSharedLocked(ctx, spec, workspaceVolume); err != nil {
			return execution.EnvironmentStatus{}, err
		}
	}
	status := b.getStatus(spec, execution.EnvironmentReady, "")
	b.setStatus(status)
	b.record(spec, "execution.environment.ready", status)
	return status, nil
}

func (b *Backend) record(spec execution.Spec, event string, payload any) {
	if b.recordLifecycle != nil {
		b.recordLifecycle(spec.Owner().EffectiveSessionID, event, payload)
	}
}

func (b *Backend) Status(
	_ context.Context,
	owner execution.Owner,
) ([]execution.EnvironmentStatus, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	result := make([]execution.EnvironmentStatus, 0)
	for _, status := range b.statuses {
		if !strings.HasPrefix(status.WorkspaceIdentity, owner.Profile+":") {
			continue
		}
		if status.Scope == execution.ScopeShared ||
			status.WorkspaceIdentity == owner.Profile+":session:"+owner.EffectiveSessionID {
			participants := map[string]struct{}{}
			for _, process := range b.processes {
				if process.incarnation == status.ContainerIncarnation &&
					process.snapshot().Status == processenv.StatusRunning {
					status.ProcessCount++
					participants[process.owner.EffectiveSessionID] = struct{}{}
				}
			}
			status.ParticipantCount = len(participants)
			result = append(result, status)
		}
	}
	return result, nil
}

func (b *Backend) Run(ctx context.Context, spec execution.Spec) (execution.CommandResult, error) {
	command, arguments, _, _, err := getContainerCommand(spec)
	if err != nil {
		return execution.CommandResult{}, err
	}
	return b.execute(ctx, spec, "foreground", append([]string{command}, arguments...), nil)
}

func (b *Backend) runDisposable(
	ctx context.Context,
	spec execution.Spec,
	resourceKind string,
	command []string,
	standardInput []byte,
) (execution.CommandResult, error) {
	if _, err := b.Acquire(ctx, spec); err != nil {
		return execution.CommandResult{}, err
	}
	incarnation, err := newDockerIncarnation()
	if err != nil {
		return execution.CommandResult{}, err
	}
	workspaceVolume := getWorkspaceVolumeName(spec)
	if err := b.ensureWorkspace(ctx, spec, workspaceVolume); err != nil {
		return execution.CommandResult{}, err
	}
	networkName, err := b.ensureNetwork(ctx, spec)
	if err != nil {
		return execution.CommandResult{}, err
	}
	secretValues := []string(nil)
	if references := spec.Exposure().SecretReferences(); len(references) > 0 &&
		resourceKind != "filesystem" {
		if b.secretResolver == nil {
			return execution.CommandResult{}, errors.New("execution secret resolver is unavailable")
		}
		resolved, resolveErr := b.secretResolver.Resolve(references)
		if resolveErr != nil {
			return execution.CommandResult{}, resolveErr
		}
		frame, frameErr := encodeControlFrame(
			resolved.Values,
			spec.Exposure().Limits().ControlInputBytes,
		)
		if frameErr != nil {
			return execution.CommandResult{}, frameErr
		}
		standardInput = frame
		command = append(
			[]string{
				"--control",
				strconv.FormatInt(spec.Exposure().Limits().ControlInputBytes, 10),
			},
			command...)
		for _, value := range resolved.Values {
			secretValues = append(secretValues, value)
		}
	}
	options, err := BuildContainerOptions(ContainerInput{
		Spec:                 spec,
		Image:                b.image,
		Contract:             b.contract,
		DaemonIncarnation:    b.daemonIncarnation,
		ContainerIncarnation: incarnation,
		ResourceKind:         resourceKind,
		WorkspaceVolume:      workspaceVolume,
		NetworkName:          networkName,
		Command:              command,
		OpenStdin:            len(standardInput) > 0,
	})
	if err != nil {
		return execution.CommandResult{}, err
	}
	engine := b.client.Engine()
	created, err := engine.ContainerCreate(ctx, options)
	if err != nil {
		return execution.CommandResult{}, err
	}
	var cleanupOnce sync.Once
	var cleanupErr error
	cleanup := func() error {
		cleanupOnce.Do(
			func() { cleanupErr = b.removeContainer(created.ID, spec.Exposure().Limits().StopGrace) },
		)
		return cleanupErr
	}
	defer func() { _ = cleanup() }()

	attached, err := engine.ContainerAttach(ctx, created.ID, client.ContainerAttachOptions{
		Stream: true,
		Stdin:  len(standardInput) > 0,
		Stdout: true,
		Stderr: true,
	})
	if err != nil {
		return execution.CommandResult{}, err
	}
	defer attached.Close()
	outputLimit := int(spec.Exposure().Limits().OutputBytes)
	stdout := newBoundedWriter(outputLimit, secretValues)
	stderr := newBoundedWriter(outputLimit, secretValues)
	copyDone := make(chan error, 1)
	go func() {
		_, copyErr := stdcopy.StdCopy(stdout, stderr, attached.Reader)
		copyDone <- copyErr
	}()
	started := time.Now()
	if _, err := engine.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		return execution.CommandResult{}, err
	}
	if len(standardInput) > 0 {
		if _, err := attached.Conn.Write(standardInput); err != nil {
			return execution.CommandResult{}, err
		}
		_ = attached.CloseWrite()
	}
	b.setStatus(b.getStatus(spec, execution.EnvironmentRunning, incarnation))
	wait := engine.ContainerWait(
		context.WithoutCancel(ctx),
		created.ID,
		client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning},
	)
	result := execution.CommandResult{}
	select {
	case response := <-wait.Result:
		result.ExitCode = int(response.StatusCode)
		if response.Error != nil {
			return execution.CommandResult{}, errors.New(response.Error.Message)
		}
		if inspect, inspectErr := engine.ContainerInspect(
			context.WithoutCancel(ctx),
			created.ID,
			client.ContainerInspectOptions{},
		); inspectErr == nil &&
			inspect.Container.State != nil {
			result.OOMKilled = inspect.Container.State.OOMKilled
		}
	case err := <-wait.Error:
		return execution.CommandResult{}, err
	case <-ctx.Done():
		result.ExitCode = -1
		result.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
		result.Interrupted = errors.Is(ctx.Err(), context.Canceled)
		if err := cleanup(); err != nil {
			return execution.CommandResult{}, err
		}
	}
	attached.Close()
	select {
	case <-copyDone:
	case <-time.After(containerOutputDrainTimeout):
	}
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()
	result.Duration = time.Since(started)
	b.setStatus(b.getStatus(spec, execution.EnvironmentReady, incarnation))
	return result, nil
}

func encodeControlFrame(values map[string]string, limit int64) ([]byte, error) {
	payload, _ := json.Marshal(values)
	if int64(len(payload)) > limit || len(payload) > int(^uint32(0)) {
		return nil, errors.New("execution secret control payload exceeds the configured limit")
	}
	frame := make([]byte, len(payload)+4)
	binary.BigEndian.PutUint32(frame, uint32(len(payload)))
	copy(frame[4:], payload)
	return frame, nil
}

func (b *Backend) removeContainer(containerID string, grace time.Duration) error {
	if b == nil || b.client == nil || containerID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	seconds := int(grace.Seconds())
	if seconds < 0 {
		seconds = 0
	}
	engine := b.client.Engine()
	_, stopErr := engine.ContainerStop(
		ctx,
		containerID,
		client.ContainerStopOptions{Timeout: &seconds},
	)
	_, removeErr := engine.ContainerRemove(
		ctx,
		containerID,
		client.ContainerRemoveOptions{Force: true},
	)
	if isNotFound(stopErr) {
		stopErr = nil
	}
	if isNotFound(removeErr) {
		removeErr = nil
	} else if errdefs.IsConflict(removeErr) {
		deadline := time.Now().Add(containerRemovalConflictTimeout)
		for time.Now().Before(deadline) {
			_, inspectErr := engine.ContainerInspect(
				ctx,
				containerID,
				client.ContainerInspectOptions{},
			)
			if isNotFound(inspectErr) {
				removeErr = nil
				break
			}
			time.Sleep(containerRemovalPollInterval)
		}
	}
	return errors.Join(stopErr, removeErr)
}

func (b *Backend) ensureWorkspace(ctx context.Context, spec execution.Spec, name string) error {
	if spec.Exposure().WorkspaceMode() != execution.WorkspaceNone {
		return nil
	}
	if _, err := b.client.Engine().VolumeInspect(
		ctx,
		name,
		client.VolumeInspectOptions{},
	); err == nil {
		return nil
	} else if !isNotFound(err) {
		return err
	}
	if err := b.checkVolumeAdmission(ctx, spec.Owner().Profile); err != nil {
		return err
	}
	_, err := b.client.Engine().VolumeCreate(ctx, client.VolumeCreateOptions{
		Name: name,
		Labels: map[string]string{
			LabelProfile:            spec.Owner().Profile,
			LabelScopeOwner:         getScopeOwner(spec.Owner(), spec.Exposure().Scope()),
			LabelSecurityGeneration: spec.Exposure().SecurityGeneration(),
			LabelDaemonIncarnation:  b.daemonIncarnation,
			LabelResourceKind:       "workspace",
			LabelScope:              string(spec.Exposure().Scope()),
		},
	})
	return err
}

func (b *Backend) checkEnvironmentAdmission(spec execution.Spec) error {
	if b.maximumEnvironments <= 0 {
		return nil
	}
	key := getEnvironmentKey(spec, b.daemonIncarnation)
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.statuses[key]; exists {
		return nil
	}
	count := 0
	for _, status := range b.statuses {
		if strings.HasPrefix(status.WorkspaceIdentity, spec.Owner().Profile+":") {
			count++
		}
	}
	if count >= b.maximumEnvironments {
		return errors.New("docker execution environment limit reached")
	}
	return nil
}

func (b *Backend) cleanupIdle(ctx context.Context, spec execution.Spec) error {
	expiry := spec.Exposure().EnvironmentIdleExpiry()
	cutoff := time.Now().UTC().Add(-expiry)
	b.mu.Lock()
	type staleEnvironment struct{ key, containerID string }
	stale := make([]staleEnvironment, 0)
	for key, status := range b.statuses {
		if status.UpdatedAt.After(cutoff) || status.State == execution.EnvironmentRunning ||
			!strings.HasPrefix(status.WorkspaceIdentity, spec.Owner().Profile+":") {
			continue
		}
		activeProcess := false
		for _, process := range b.processes {
			if process.shared && process.incarnation == status.ContainerIncarnation &&
				process.snapshot().Status == processenv.StatusRunning {
				activeProcess = true
				break
			}
		}
		if activeProcess {
			continue
		}
		unit := b.environments[key]
		containerID := ""
		if unit != nil {
			containerID = unit.containerID
			delete(b.environments, key)
		}
		delete(b.statuses, key)
		delete(b.sharedGates, key)
		delete(b.environmentLocks, key)
		stale = append(stale, staleEnvironment{
			key:         key,
			containerID: containerID,
		})
	}
	b.mu.Unlock()
	var joined error
	for _, unit := range stale {
		if unit.containerID != "" {
			joined = errors.Join(
				joined,
				b.removeContainer(unit.containerID, spec.Exposure().Limits().StopGrace),
			)
		}
	}
	return joined
}

func (b *Backend) ensureNetwork(ctx context.Context, spec execution.Spec) (string, error) {
	if spec.Exposure().Network() == execution.NetworkNone {
		return "", nil
	}
	name := getNetworkName(spec, b.daemonIncarnation)
	if _, err := b.client.Engine().NetworkInspect(
		ctx,
		name,
		client.NetworkInspectOptions{},
	); err == nil {
		b.trackNetwork(name)
		return name, nil
	}
	_, err := b.client.Engine().NetworkCreate(ctx, name, BuildNetworkOptions(map[string]string{
		LabelProfile:            spec.Owner().Profile,
		LabelScopeOwner:         getScopeOwner(spec.Owner(), spec.Exposure().Scope()),
		LabelSecurityGeneration: spec.Exposure().SecurityGeneration(),
		LabelDaemonIncarnation:  b.daemonIncarnation,
		LabelResourceKind:       "network",
		LabelScope:              string(spec.Exposure().Scope()),
	}))
	if err == nil {
		b.trackNetwork(name)
	}
	return name, err
}

func (b *Backend) getStatus(
	spec execution.Spec,
	state execution.EnvironmentState,
	containerIncarnation string,
) execution.EnvironmentStatus {
	exposure := spec.Exposure()
	return execution.EnvironmentStatus{
		ID:                   getEnvironmentKey(spec, b.daemonIncarnation),
		Backend:              execution.BackendDocker,
		Scope:                exposure.Scope(),
		State:                state,
		WorkspaceIdentity:    exposure.WorkspaceIdentity(),
		WorkspaceMode:        exposure.WorkspaceMode(),
		Network:              exposure.Network(),
		ImageDigest:          exposure.ImageDigest(),
		SecurityGeneration:   exposure.SecurityGeneration(),
		DaemonIncarnation:    b.daemonIncarnation,
		ContainerIncarnation: containerIncarnation,
		UpdatedAt:            time.Now().UTC(),
	}
}

func (b *Backend) getEnvironmentLock(key string) *sync.Mutex {
	b.mu.Lock()
	defer b.mu.Unlock()
	lock := b.environmentLocks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		b.environmentLocks[key] = lock
	}
	return lock
}

func getEnvironmentKey(spec execution.Spec, daemonIncarnation string) string {
	owner := spec.Owner()
	parts := []string{owner.Profile, string(spec.Exposure().Scope())}
	if spec.Exposure().Scope() == execution.ScopeSession {
		parts = append(parts, owner.EffectiveSessionID)
	}
	parts = append(parts, spec.Exposure().SecurityGeneration(), daemonIncarnation)
	return safeID(strings.Join(parts, "\x00"))
}

func (b *Backend) setStatus(status execution.EnvironmentStatus) {
	b.mu.Lock()
	b.statuses[status.ID] = status
	b.mu.Unlock()
}

func getWorkspaceVolumeName(spec execution.Spec) string {
	return "morph-ws-" + safeID(spec.Exposure().WorkspaceIdentity())
}

func getNetworkName(spec execution.Spec, daemonIncarnation string) string {
	return "morph-net-" + getEnvironmentKey(spec, daemonIncarnation)
}

func safeID(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:12])
}

func (b *Backend) trackNetwork(name string) {
	b.mu.Lock()
	b.networks[name] = struct{}{}
	b.mu.Unlock()
}
