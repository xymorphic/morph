//go:build execution_docker

package docker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"

	commandplan "github.com/xymorphic/morph/internal/command"
	processenv "github.com/xymorphic/morph/internal/environment/process"
	"github.com/xymorphic/morph/internal/execution"
)

func TestBackend_IntegrationLifecycle(t *testing.T) {
	backend, image, contract := newIntegrationBackend(t)
	owner := integrationOwner("session")
	exposure := integrationExposure(
		t,
		image,
		contract,
		execution.ScopeSession,
		execution.NetworkNone,
		nil,
	)
	require.NoError(
		t,
		backend.removeSessionResources(
			context.Background(),
			owner.Profile,
			owner.EffectiveSessionID,
			true,
		),
	)

	commandSpec := integrationCommandSpec(
		t,
		owner,
		exposure,
		integrationDirectPlan(t, "/bin/echo", "hello"),
	)
	status, err := backend.Acquire(context.Background(), commandSpec)
	require.NoError(t, err)
	require.Equal(t, execution.EnvironmentReady, status.State)
	result, err := backend.Run(context.Background(), commandSpec)
	require.NoError(t, err)
	require.Equal(t, 0, result.ExitCode)
	require.Contains(t, result.Stdout, "hello")

	statuses, err := backend.Status(context.Background(), owner)
	require.NoError(t, err)
	require.NotEmpty(t, statuses)

	writeSpec := integrationFilesystemSpec(
		t,
		owner,
		exposure,
		execution.FilesystemOperation{
			Action: execution.FilesystemWrite,
			Path: integrationPath(
				t,
				execution.FilesystemWrite,
				"/workspace/nested/file.txt",
			),
			Data: []byte("needle\n"),
		},
	)
	fileInfo, err := backend.WriteFile(context.Background(), writeSpec, true)
	require.NoError(t, err)
	require.True(t, fileInfo.Created)

	readSpec := integrationFilesystemSpec(
		t,
		owner,
		exposure,
		execution.FilesystemOperation{
			Action: execution.FilesystemRead,
			Path: integrationPath(
				t,
				execution.FilesystemRead,
				"/workspace/nested/file.txt",
			),
		},
	)
	content, err := backend.ReadFile(context.Background(), readSpec, 1024)
	require.NoError(t, err)
	require.Equal(t, "needle\n", string(content))

	listSpec := integrationFilesystemSpec(
		t,
		owner,
		exposure,
		execution.FilesystemOperation{
			Action:    execution.FilesystemList,
			Path:      integrationPath(t, execution.FilesystemList, "/workspace"),
			Recursive: true,
		},
	)
	entries, err := backend.ListFiles(context.Background(), listSpec, 20)
	require.NoError(t, err)
	require.NotEmpty(t, entries)

	searchSpec := integrationFilesystemSpec(
		t,
		owner,
		exposure,
		execution.FilesystemOperation{
			Action:    execution.FilesystemSearch,
			Path:      integrationPath(t, execution.FilesystemSearch, "/workspace"),
			Query:     "needle",
			Recursive: true,
		},
	)
	matches, err := backend.SearchFiles(context.Background(), searchSpec, 20)
	require.NoError(t, err)
	require.NotEmpty(t, matches)

	patchSpec := integrationFilesystemSpec(
		t,
		owner,
		exposure,
		execution.FilesystemOperation{
			Action: execution.FilesystemPatch,
			Path: integrationPath(
				t,
				execution.FilesystemPatch,
				"/workspace/nested/file.txt",
			),
			Data: []byte(
				"--- a/nested/file.txt\n+++ b/nested/file.txt\n@@ -1 +1 @@\n-needle\n+updated\n",
			),
			Strip: 1,
		},
	)
	_, err = backend.PatchFile(context.Background(), patchSpec)
	require.NoError(t, err)

	processPlan := integrationShellPlan(t, "printf process-started; sleep 10")
	startSpec := integrationProcessSpec(t, owner, exposure, execution.ProcessOperation{
		Action:            execution.ProcessStart,
		Plan:              &processPlan,
		Label:             "worker",
		OutputBufferBytes: 4096,
	})
	process, err := backend.StartProcess(context.Background(), startSpec)
	require.NoError(t, err)
	require.Equal(t, processenv.StatusRunning, process.Status)

	listProcessSpec := integrationProcessSpec(t, owner, exposure, execution.ProcessOperation{
		Action: execution.ProcessList,
	})
	processes, err := backend.ListProcesses(context.Background(), listProcessSpec)
	require.NoError(t, err)
	require.NotEmpty(t, processes)

	getSpec := integrationProcessSpec(t, owner, exposure, execution.ProcessOperation{
		Action:    execution.ProcessStatus,
		ProcessID: process.ID,
	})
	process, err = backend.GetProcess(context.Background(), getSpec)
	require.NoError(t, err)

	readProcessSpec := integrationProcessSpec(t, owner, exposure, execution.ProcessOperation{
		Action:    execution.ProcessRead,
		ProcessID: process.ID,
	})
	var processOutput processenv.Output
	require.Eventually(t, func() bool {
		processOutput, err = backend.ReadProcess(
			context.Background(),
			readProcessSpec,
			processenv.ReadRequest{
				ProcessID: process.ID,
			},
		)
		return err == nil && strings.Contains(processOutput.Stdout, "process-started")
	}, 5*time.Second, 50*time.Millisecond)

	stopSpec := integrationProcessSpec(t, owner, exposure, execution.ProcessOperation{
		Action:    execution.ProcessStop,
		ProcessID: process.ID,
	})
	process, err = backend.StopProcess(context.Background(), stopSpec)
	require.NoError(t, err)
	require.NotEqual(t, processenv.StatusRunning, process.Status)

	require.NoError(
		t,
		backend.CloseSession(context.Background(), owner.Profile, owner.EffectiveSessionID, true),
	)
}

func TestBackend_IntegrationSharedNetworkSecretsAndTimeout(t *testing.T) {
	require.NoError(t, os.Setenv("MORPH_DOCKER_TEST_SECRET", "secret-value"))
	t.Cleanup(func() {
		require.NoError(t, os.Unsetenv("MORPH_DOCKER_TEST_SECRET"))
	})
	resolver, err := NewSecretResolver([]SecretReference{
		{
			Name: "token",
			Env:  "MORPH_DOCKER_TEST_SECRET",
		},
	})
	require.NoError(t, err)
	backend, image, contract := newIntegrationBackendWithResolver(t, resolver)
	owner := integrationOwner("shared-session")

	shared := integrationExposure(
		t,
		image,
		contract,
		execution.ScopeShared,
		execution.NetworkBridge,
		nil,
	)
	sharedSpec := integrationCommandSpec(
		t,
		owner,
		shared,
		integrationDirectPlan(t, "/bin/echo", "shared"),
	)
	result, err := backend.Run(context.Background(), sharedSpec)
	require.NoError(t, err)
	require.Contains(t, result.Stdout, "shared")

	processPlan := integrationShellPlan(t, "printf shared-process; sleep 10")
	startSpec := integrationProcessSpec(t, owner, shared, execution.ProcessOperation{
		Action: execution.ProcessStart,
		Plan:   &processPlan,
		Label:  "shared-worker",
	})
	process, err := backend.StartProcess(context.Background(), startSpec)
	require.NoError(t, err)
	readSpec := integrationProcessSpec(t, owner, shared, execution.ProcessOperation{
		Action:    execution.ProcessRead,
		ProcessID: process.ID,
	})
	output, err := backend.ReadProcess(
		context.Background(),
		readSpec,
		processenv.ReadRequest{
			ProcessID: process.ID,
		},
	)
	require.NoError(t, err)
	require.Contains(t, output.Stdout, "shared-process")
	stopSpec := integrationProcessSpec(t, owner, shared, execution.ProcessOperation{
		Action:    execution.ProcessStop,
		ProcessID: process.ID,
	})
	_, err = backend.StopProcess(context.Background(), stopSpec)
	require.NoError(t, err)

	secretExposure := integrationExposure(
		t,
		image,
		contract,
		execution.ScopeSession,
		execution.NetworkNone,
		[]string{"token"},
	)
	secretSpec := integrationCommandSpec(
		t,
		owner,
		secretExposure,
		integrationShellPlan(t, `printf "$MORPH_SECRET_TOKEN"`),
	)
	result, err = backend.Run(context.Background(), secretSpec)
	require.NoError(t, err)
	require.NotContains(t, result.Stdout, "secret-value")

	timeoutExposure := integrationExposure(
		t,
		image,
		contract,
		execution.ScopeSession,
		execution.NetworkNone,
		nil,
	)
	timeoutSpec := integrationCommandSpec(
		t,
		owner,
		timeoutExposure,
		integrationShellPlan(t, "sleep 10"),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	result, err = backend.Run(ctx, timeoutSpec)
	require.NoError(t, err)
	require.True(t, result.TimedOut)

	sharedTimeoutSpec := integrationCommandSpec(
		t,
		owner,
		shared,
		integrationShellPlan(t, "sleep 10"),
	)
	ctx, cancel = context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	result, err = backend.Run(ctx, sharedTimeoutSpec)
	require.NoError(t, err)
	require.True(t, result.TimedOut)
}

func TestBackend_IntegrationFailureAndCleanupPaths(t *testing.T) {
	backend, image, contract := newIntegrationBackend(t)
	owner := integrationOwner("failure-session")
	exposure := integrationExposure(
		t,
		image,
		contract,
		execution.ScopeSession,
		execution.NetworkNone,
		nil,
	)

	_, err := backend.Acquire(context.Background(), execution.Spec{})
	require.Error(t, err)
	_, err = backend.Run(context.Background(), execution.Spec{})
	require.Error(t, err)
	_, err = backend.StartProcess(context.Background(), execution.Spec{})
	require.EqualError(t, err, "docker process start specification is invalid")

	readSpec := integrationFilesystemSpec(
		t,
		owner,
		exposure,
		execution.FilesystemOperation{
			Action: execution.FilesystemRead,
			Path: integrationPath(
				t,
				execution.FilesystemRead,
				"/workspace/missing.txt",
			),
		},
	)
	_, err = backend.ReadFile(context.Background(), readSpec, 32)
	require.Error(t, err)
	_, err = backend.WriteFile(context.Background(), readSpec, false)
	require.EqualError(t, err, "docker filesystem execution specification is invalid")
	_, err = backend.PatchFile(context.Background(), readSpec)
	require.EqualError(t, err, "docker filesystem execution specification is invalid")
	_, err = backend.ListFiles(context.Background(), readSpec, 10)
	require.EqualError(t, err, "docker filesystem execution specification is invalid")
	_, err = backend.SearchFiles(context.Background(), readSpec, 10)
	require.EqualError(t, err, "docker filesystem execution specification is invalid")

	binaryWrite := integrationFilesystemSpec(
		t,
		owner,
		exposure,
		execution.FilesystemOperation{
			Action: execution.FilesystemWrite,
			Path: integrationPath(
				t,
				execution.FilesystemWrite,
				"/workspace/binary.bin",
			),
			Data: []byte{0, 1, 2},
		},
	)
	_, err = backend.WriteFile(context.Background(), binaryWrite, false)
	require.NoError(t, err)
	binaryRead := integrationFilesystemSpec(
		t,
		owner,
		exposure,
		execution.FilesystemOperation{
			Action: execution.FilesystemRead,
			Path: integrationPath(
				t,
				execution.FilesystemRead,
				"/workspace/binary.bin",
			),
		},
	)
	_, err = backend.ReadFile(context.Background(), binaryRead, 32)
	require.EqualError(t, err, "file is not text")

	writeSpec := integrationFilesystemSpec(
		t,
		owner,
		exposure,
		execution.FilesystemOperation{
			Action: execution.FilesystemWrite,
			Path: integrationPath(
				t,
				execution.FilesystemWrite,
				"/workspace/patch.txt",
			),
			Data: []byte("old\n"),
		},
	)
	_, err = backend.WriteFile(context.Background(), writeSpec, false)
	require.NoError(t, err)
	patchSpec := integrationFilesystemSpec(
		t,
		owner,
		exposure,
		execution.FilesystemOperation{
			Action: execution.FilesystemPatch,
			Path: integrationPath(
				t,
				execution.FilesystemPatch,
				"/workspace/patch.txt",
			),
			Data: []byte(
				"--- a/patch.txt\n+++ b/patch.txt\n@@ -1 +1 @@\n-old\n+new\n",
			),
			Strip: 1,
		},
	)
	_, err = backend.PatchFile(context.Background(), patchSpec)
	require.NoError(t, err)
	_, err = backend.PatchFile(context.Background(), patchSpec)
	require.ErrorIs(t, err, execution.ErrPatchConflict)

	listSpec := integrationFilesystemSpec(
		t,
		owner,
		exposure,
		execution.FilesystemOperation{
			Action: execution.FilesystemList,
			Path:   integrationPath(t, execution.FilesystemList, "/workspace"),
		},
	)
	_, err = backend.ListFiles(context.Background(), listSpec, 0)
	require.Error(t, err)
	searchSpec := integrationFilesystemSpec(
		t,
		owner,
		exposure,
		execution.FilesystemOperation{
			Action: execution.FilesystemSearch,
			Path:   integrationPath(t, execution.FilesystemSearch, "/workspace"),
			Query:  "[",
		},
	)
	_, err = backend.SearchFiles(context.Background(), searchSpec, 10)
	require.Error(t, err)

	processPlan := integrationShellPlan(t, "sleep 10")
	startSpec := integrationProcessSpec(t, owner, exposure, execution.ProcessOperation{
		Action: execution.ProcessStart,
		Plan:   &processPlan,
		Label:  "duplicate",
	})
	process, err := backend.StartProcess(context.Background(), startSpec)
	require.NoError(t, err)
	_, err = backend.StartProcess(context.Background(), startSpec)
	require.EqualError(t, err, "process label already exists")

	labelSpec := integrationProcessSpec(t, owner, exposure, execution.ProcessOperation{
		Action:    execution.ProcessStatus,
		ProcessID: "duplicate",
	})
	byLabel, err := backend.GetProcess(context.Background(), labelSpec)
	require.NoError(t, err)
	require.Equal(t, process.ID, byLabel.ID)

	missingSpec := integrationProcessSpec(t, owner, exposure, execution.ProcessOperation{
		Action:    execution.ProcessStatus,
		ProcessID: "missing",
	})
	_, err = backend.GetProcess(context.Background(), missingSpec)
	require.ErrorIs(t, err, execution.ErrInvalidProcessID)

	otherOwner := integrationOwner("other-session")
	deniedSpec := integrationProcessSpec(t, otherOwner, exposure, execution.ProcessOperation{
		Action:    execution.ProcessStatus,
		ProcessID: process.ID,
	})
	_, err = backend.GetProcess(context.Background(), deniedSpec)
	require.ErrorIs(t, err, execution.ErrProcessDenied)

	require.NoError(t, backend.CloseOwner(context.Background(), owner))
	require.NoError(t, backend.Reconcile(context.Background()))
}

func newIntegrationBackend(
	t *testing.T,
) (*Backend, string, execution.ImageContract) {
	t.Helper()
	return newIntegrationBackendWithResolver(t, nil)
}

func newIntegrationBackendWithResolver(
	t *testing.T,
	resolver *SecretResolver,
) (*Backend, string, execution.ImageContract) {
	t.Helper()
	if os.Getenv("MORPH_EXECUTION_DOCKER_TEST") != "1" {
		t.Skip("explicit Docker execution test lane is disabled")
	}
	image := os.Getenv("MORPH_EXECUTION_DOCKER_IMAGE")
	contract, err := LoadImageContract("../../../containers/sandbox/contract.json")
	require.NoError(t, err)
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index + 1)
	}
	backend, err := NewBackend(BackendOptions{
		Endpoint:            "/var/run/docker.sock",
		Image:               image,
		Contract:            contract,
		DaemonIncarnation:   "integration-daemon",
		SecretResolver:      resolver,
		ProcessIdentityKey:  key,
		AllowTestImageTag:   true,
		MaximumEnvironments: 20,
		MaximumVolumes:      20,
		ConfiguredScope:     execution.ScopeSession,
		SessionExists: func(context.Context, string) (bool, error) {
			return true, nil
		},
	})
	require.NoError(t, err)
	cleanupClient, err := NewClient("/var/run/docker.sock")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, backend.Close(context.Background()))
		filters := make(client.Filters).Add(
			"label",
			LabelProfile+"=docker-coverage",
		)
		volumes, listErr := cleanupClient.Engine().VolumeList(
			context.Background(),
			client.VolumeListOptions{
				Filters: filters,
			},
		)
		require.NoError(t, listErr)
		for _, volume := range volumes.Items {
			_, removeErr := cleanupClient.Engine().VolumeRemove(
				context.Background(),
				volume.Name,
				client.VolumeRemoveOptions{
					Force: true,
				},
			)
			require.NoError(t, removeErr)
		}
		require.NoError(t, cleanupClient.Close())
	})
	return backend, image, contract
}

func integrationOwner(sessionID string) execution.Owner {
	return execution.Owner{
		Profile:            "docker-coverage",
		ActorKind:          "local_owner",
		ActorID:            sessionID,
		Surface:            "cli",
		PublicSessionID:    sessionID,
		EffectiveSessionID: sessionID,
	}
}

func integrationExposure(
	t *testing.T,
	image string,
	contract execution.ImageContract,
	scope execution.Scope,
	network execution.NetworkMode,
	secrets []string,
) execution.Exposure {
	t.Helper()
	exposure, err := execution.NewExposure(execution.ExposureInput{
		Backend:             execution.BackendDocker,
		Scope:               scope,
		WorkspaceIdentity:   integrationWorkspaceIdentity(scope),
		WorkspaceMode:       execution.WorkspaceNone,
		Network:             network,
		SecretReferences:    secrets,
		ImageDigest:         image,
		ImageContractDigest: contract.Digest(),
		SecurityGeneration:  "generation",
		PolicyHash:          "policy",
		Limits: execution.Limits{
			MemoryBytes:       512 << 20,
			CPUMilli:          1000,
			PIDs:              128,
			OpenFiles:         1024,
			TemporaryBytes:    64 << 20,
			OutputBytes:       1 << 20,
			ControlInputBytes: 1 << 20,
			Runtime:           time.Minute,
			StopGrace:         2 * time.Second,
		},
		EnvironmentIdleExpiry:   time.Minute,
		SharedDisabledRetention: time.Hour,
	})
	require.NoError(t, err)
	return exposure
}

func integrationWorkspaceIdentity(scope execution.Scope) string {
	if scope == execution.ScopeShared {
		return "docker-coverage:shared"
	}
	return "docker-coverage:session:session"
}

func integrationDirectPlan(
	t *testing.T,
	command string,
	arguments ...string,
) commandplan.Plan {
	t.Helper()
	plan, err := commandplan.Analyze(context.Background(), commandplan.Request{
		Mode:             commandplan.ModeDirect,
		Command:          command,
		Args:             arguments,
		CWD:              "/workspace",
		Environment:      map[string]string{"PATH": "/usr/bin:/bin"},
		CleanEnvironment: true,
		LookPath: func(string) (string, error) {
			return command, nil
		},
	})
	require.NoError(t, err)
	return plan
}

func integrationShellPlan(t *testing.T, source string) commandplan.Plan {
	t.Helper()
	plan, err := commandplan.Analyze(context.Background(), commandplan.Request{
		Mode:             commandplan.ModePOSIXShell,
		Command:          source,
		CWD:              "/workspace",
		Environment:      map[string]string{"PATH": "/usr/bin:/bin"},
		CleanEnvironment: true,
		ShellPath:        "/bin/sh",
		LookPath: func(name string) (string, error) {
			return filepath.Join("/usr/bin", name), nil
		},
	})
	require.NoError(t, err)
	return plan
}

func integrationCommandSpec(
	t *testing.T,
	owner execution.Owner,
	exposure execution.Exposure,
	plan commandplan.Plan,
) execution.Spec {
	t.Helper()
	spec, err := execution.NewSpec(owner, exposure, execution.Operation{
		Kind:    execution.OperationCommand,
		Command: &plan,
	})
	require.NoError(t, err)
	return spec
}

func integrationProcessSpec(
	t *testing.T,
	owner execution.Owner,
	exposure execution.Exposure,
	operation execution.ProcessOperation,
) execution.Spec {
	t.Helper()
	spec, err := execution.NewSpec(owner, exposure, execution.Operation{
		Kind:    execution.OperationProcess,
		Process: &operation,
	})
	require.NoError(t, err)
	return spec
}

func integrationFilesystemSpec(
	t *testing.T,
	owner execution.Owner,
	exposure execution.Exposure,
	operation execution.FilesystemOperation,
) execution.Spec {
	t.Helper()
	spec, err := execution.NewSpec(owner, exposure, execution.Operation{
		Kind:       execution.OperationFilesystem,
		Filesystem: &operation,
	})
	require.NoError(t, err)
	return spec
}

func integrationPath(
	t *testing.T,
	action execution.FilesystemAction,
	path string,
) execution.PreparedPath {
	t.Helper()
	prepared, err := execution.NewPreparedPath(execution.PreparedPathInput{
		LogicalPath:        path,
		ContainerPath:      path,
		Mode:               execution.MountReadWrite,
		Action:             action,
		SecurityGeneration: "generation",
	})
	require.NoError(t, err)
	return prepared
}
