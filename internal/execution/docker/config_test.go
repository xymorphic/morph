package docker

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	commandplan "github.com/xymorphic/morph/internal/command"
	"github.com/xymorphic/morph/internal/execution"
)

func TestBuildContainerOptions_EnforcesHardeningAndCleanEnvironment(t *testing.T) {
	plan, err := commandplan.Analyze(context.Background(), commandplan.Request{
		Mode:    commandplan.ModeDirect,
		Command: "echo",
		Args:    []string{"hello"},
		CWD:     "/workspace",
		Environment: map[string]string{
			"PATH":    "/usr/bin",
			"VISIBLE": "yes",
		},
		CleanEnvironment: true,
		LookPath:         func(string) (string, error) { return "/usr/bin/echo", nil },
	})
	require.NoError(t, err)

	exposure, err := execution.NewExposure(testDockerExposure())
	require.NoError(t, err)

	spec, err := execution.NewSpec(execution.Owner{
		Profile:            "default",
		ActorKind:          "local_owner",
		Surface:            "cli",
		PublicSessionID:    "default",
		EffectiveSessionID: "default",
	}, exposure, execution.Operation{
		Kind:    execution.OperationCommand,
		Command: &plan,
	})
	require.NoError(t, err)

	options, err := BuildContainerOptions(ContainerInput{
		Spec:                 spec,
		Image:                testDockerExposure().ImageDigest,
		Contract:             testContract(),
		DaemonIncarnation:    "daemon",
		ContainerIncarnation: "container",
		ResourceKind:         "foreground",
		WorkspaceVolume:      "workspace",
	})
	require.NoError(t, err)

	require.True(t, options.HostConfig.ReadonlyRootfs)
	require.Equal(t, []string{"ALL"}, options.HostConfig.CapDrop)
	require.Contains(t, options.HostConfig.SecurityOpt, "no-new-privileges")
	require.False(t, options.HostConfig.Privileged)
	require.Equal(t, "none", string(options.HostConfig.NetworkMode))
	require.ElementsMatch(t, []string{"PATH=/usr/bin", "VISIBLE=yes"}, options.Config.Env)
}

func TestBuildContainerOptions_HelperOverridePreservesPlanContextAndStdin(t *testing.T) {
	plan, err := commandplan.Analyze(context.Background(), commandplan.Request{
		Mode:             commandplan.ModeDirect,
		Command:          "echo",
		Args:             []string{"hello"},
		CWD:              "/workspace/project",
		Environment:      map[string]string{"VISIBLE": "yes"},
		CleanEnvironment: true,
		LookPath:         func(string) (string, error) { return "/usr/bin/echo", nil },
	})
	require.NoError(t, err)

	exposure, err := execution.NewExposure(testDockerExposure())
	require.NoError(t, err)

	spec, err := execution.NewSpec(execution.Owner{
		Profile:            "default",
		ActorKind:          "local_owner",
		Surface:            "cli",
		PublicSessionID:    "default",
		EffectiveSessionID: "default",
	}, exposure, execution.Operation{
		Kind:    execution.OperationCommand,
		Command: &plan,
	})
	require.NoError(t, err)

	options, err := BuildContainerOptions(ContainerInput{
		Spec:                 spec,
		Image:                testDockerExposure().ImageDigest,
		Contract:             testContract(),
		DaemonIncarnation:    "daemon",
		ContainerIncarnation: "container",
		ResourceKind:         "foreground",
		WorkspaceVolume:      "workspace",
		Command:              []string{"--control", "1024", "/usr/bin/echo", "hello"},
		OpenStdin:            true,
	})
	require.NoError(t, err)

	require.True(t, options.Config.AttachStdin)
	require.True(t, options.Config.OpenStdin)
	require.True(t, options.Config.StdinOnce)
	require.Equal(t, "/workspace/project", options.Config.WorkingDir)
	require.Contains(t, options.Config.Env, "VISIBLE=yes")
}

func TestBackend_SharedGateSurvivesEnvironmentReplacement(t *testing.T) {
	backend := &Backend{sharedGates: map[string]chan struct{}{}}
	require.Equal(t, backend.getSharedGate("shared"), backend.getSharedGate("shared"))
}

func TestBoundedWriter_RedactsAcrossChunksAndBoundsOutput(t *testing.T) {
	writer := newBoundedWriter(16, []string{"secret-value"})
	_, err := writer.Write([]byte("a secret-"))
	require.NoError(t, err)
	_, err = writer.Write([]byte("value tail that is long"))
	require.NoError(t, err)
	require.NotContains(t, writer.String(), "secret-value")
	require.LessOrEqual(t, len(writer.String()), 16)
	require.True(t, writer.Truncated())
}

func testDockerExposure() execution.ExposureInput {
	input := execution.ExposureInput{
		Backend:             execution.BackendDocker,
		Scope:               execution.ScopeSession,
		WorkspaceIdentity:   "default:session:default",
		WorkspaceMode:       execution.WorkspaceNone,
		Network:             execution.NetworkNone,
		ImageDigest:         "image@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ImageContractDigest: testContract().Digest(),
		SecurityGeneration:  "generation",
		Limits: execution.Limits{
			MemoryBytes:       1 << 20,
			CPUMilli:          100,
			PIDs:              16,
			OpenFiles:         32,
			TemporaryBytes:    1 << 20,
			OutputBytes:       1024,
			ControlInputBytes: 1024,
		},
		EnvironmentIdleExpiry:   1,
		SharedDisabledRetention: 0,
	}
	input.Limits.Runtime = 1
	input.Limits.StopGrace = 1
	return input
}

func testContract() execution.ImageContract {
	return execution.ImageContract{
		Version:      SandboxRuntimeCompatibility,
		GOOS:         "linux",
		Architecture: "amd64",
		User:         "65532:65532",
		Shell:        "/bin/sh",
		PATH: []string{
			"/usr/bin",
		},
		Executables:   map[string]string{"echo": "/usr/bin/echo"},
		Helper:        "/usr/local/bin/morph-sandbox",
		WorkspacePath: "/workspace",
		HomePath:      "/home/morph",
		TemporaryPath: "/tmp",
		ControlPath:   "/run/morph",
	}
}
