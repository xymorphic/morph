//go:build execution_docker

package execution_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	commandplan "github.com/wandxy/morph/internal/command"
	"github.com/wandxy/morph/internal/execution"
	executiondocker "github.com/wandxy/morph/internal/execution/docker"
)

func TestDockerExecution_PrivateWorkspacePersistsAndIsolatesSessions(t *testing.T) {
	if os.Getenv("MORPH_EXECUTION_DOCKER_TEST") != "1" {
		t.Skip("explicit Docker execution test lane is disabled")
	}
	image := os.Getenv("MORPH_EXECUTION_DOCKER_IMAGE")
	contract, err := executiondocker.LoadImageContract("../../../../containers/sandbox/contract.json")
	require.NoError(t, err)
	incarnation, err := execution.NewIncarnation()
	require.NoError(t, err)
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index + 1)
	}
	backend, err := executiondocker.NewBackend(executiondocker.BackendOptions{
		Endpoint:           "/var/run/docker.sock",
		Image:              image,
		Contract:           contract,
		DaemonIncarnation:  incarnation,
		ProcessIdentityKey: key,
		AllowTestImageTag:  true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, backend.Close(context.Background())) })

	ownerA := execution.Owner{
		Profile:            "execution-e2e",
		ActorKind:          "local_owner",
		Surface:            "cli",
		PublicSessionID:    "session-a",
		EffectiveSessionID: "session-a",
	}
	ownerB := execution.Owner{
		Profile:            "execution-e2e",
		ActorKind:          "local_owner",
		Surface:            "cli",
		PublicSessionID:    "session-b",
		EffectiveSessionID: "session-b",
	}
	exposureA := newExposure(t, image, contract, "execution-e2e:session:session-a")
	exposureB := newExposure(t, image, contract, "execution-e2e:session:session-b")

	writePath := newPath(t, execution.FilesystemWrite, "/workspace/probe.txt", "generation")
	writeSpec, err := execution.NewSpec(
		ownerA,
		exposureA,
		execution.Operation{
			Kind: execution.OperationFilesystem,
			Filesystem: &execution.FilesystemOperation{
				Action: execution.FilesystemWrite,
				Path:   writePath,
				Data:   []byte("session-a"),
			},
		},
	)
	require.NoError(t, err)
	_, err = backend.WriteFile(context.Background(), writeSpec, true)
	require.NoError(t, err)

	readA := newPath(t, execution.FilesystemRead, "/workspace/probe.txt", "generation")
	readSpecA, err := execution.NewSpec(
		ownerA,
		exposureA,
		execution.Operation{
			Kind: execution.OperationFilesystem,
			Filesystem: &execution.FilesystemOperation{
				Action: execution.FilesystemRead,
				Path:   readA,
			},
		},
	)
	require.NoError(t, err)
	content, err := backend.ReadFile(context.Background(), readSpecA, 1024)
	require.NoError(t, err)
	require.Equal(t, "session-a", string(content))

	readB := newPath(t, execution.FilesystemRead, "/workspace/probe.txt", "generation")
	readSpecB, err := execution.NewSpec(
		ownerB,
		exposureB,
		execution.Operation{
			Kind: execution.OperationFilesystem,
			Filesystem: &execution.FilesystemOperation{
				Action: execution.FilesystemRead,
				Path:   readB,
			},
		},
	)
	require.NoError(t, err)
	_, err = backend.ReadFile(context.Background(), readSpecB, 1024)
	require.Error(t, err)

	plan, err := commandplan.Analyze(context.Background(), commandplan.Request{
		Mode:             commandplan.ModeDirect,
		Command:          "echo",
		Args:             []string{"contained"},
		CWD:              "/workspace",
		Environment:      map[string]string{"PATH": "/usr/bin:/bin"},
		CleanEnvironment: true,
		LookPath:         func(string) (string, error) { return "/bin/echo", nil },
	})
	require.NoError(t, err)
	commandSpec, err := execution.NewSpec(
		ownerA,
		exposureA,
		execution.Operation{
			Kind:    execution.OperationCommand,
			Command: &plan,
		},
	)
	require.NoError(t, err)
	result, err := backend.Run(context.Background(), commandSpec)
	require.NoError(t, err)
	require.Equal(t, 0, result.ExitCode)
	require.Contains(t, result.Stdout, "contained")
}

func newExposure(
	t *testing.T,
	image string,
	contract execution.ImageContract,
	workspace string,
) execution.Exposure {
	t.Helper()
	exposure, err := execution.NewExposure(execution.ExposureInput{
		Backend:             execution.BackendDocker,
		Scope:               execution.ScopeSession,
		WorkspaceIdentity:   workspace,
		WorkspaceMode:       execution.WorkspaceNone,
		Network:             execution.NetworkNone,
		ImageDigest:         image,
		ImageContractDigest: contract.Digest(),
		SecurityGeneration:  "generation",
		PolicyHash:          "policy",
		Limits: execution.Limits{
			MemoryBytes:       1 << 30,
			CPUMilli:          1000,
			PIDs:              128,
			OpenFiles:         1024,
			TemporaryBytes:    64 << 20,
			OutputBytes:       1 << 20,
			ControlInputBytes: 1 << 20,
			Runtime:           time.Minute,
			StopGrace:         3 * time.Second,
		},
		EnvironmentIdleExpiry:   time.Minute,
		SharedDisabledRetention: time.Hour,
	})
	require.NoError(t, err)
	return exposure
}

func newPath(
	t *testing.T,
	action execution.FilesystemAction,
	path string,
	generation string,
) execution.PreparedPath {
	t.Helper()
	prepared, err := execution.NewPreparedPath(execution.PreparedPathInput{
		LogicalPath:        path,
		ContainerPath:      path,
		Mode:               execution.MountReadWrite,
		Action:             action,
		SecurityGeneration: generation,
	})
	require.NoError(t, err)
	return prepared
}
