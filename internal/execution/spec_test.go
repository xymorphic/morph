package execution

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	commandplan "github.com/xymorphic/morph/internal/command"
)

func TestNewSpec_BindsNormalizedExposureAndOperation(t *testing.T) {
	plan, err := commandplan.Analyze(context.Background(), commandplan.Request{
		Mode:             commandplan.ModeDirect,
		Command:          "echo",
		Args:             []string{"hello"},
		CWD:              "/workspace",
		Environment:      map[string]string{"PATH": "/usr/bin"},
		CleanEnvironment: true,
		LookPath:         func(string) (string, error) { return "/usr/bin/echo", nil },
	})
	require.NoError(t, err)
	exposure, err := NewExposure(testExposureInput())
	require.NoError(t, err)
	owner := Owner{
		Profile:            "default",
		ActorKind:          "local_owner",
		Surface:            "cli",
		PublicSessionID:    "default",
		EffectiveSessionID: "default",
	}
	spec, err := NewSpec(owner, exposure, Operation{
		Kind:             OperationCommand,
		SecretReferences: []string{"token"},
		Command:          &plan,
	})
	require.NoError(t, err)
	require.NotEmpty(t, spec.Digest())
	require.Equal(t, []string{"token"}, spec.Exposure().SecretReferences())
	operation := spec.Operation()
	operation.SecretReferences[0] = "changed"
	require.Equal(t, []string{"token"}, spec.Operation().SecretReferences)
}

func TestNewExposure_ChangesDigestForSecurityRelevantFields(t *testing.T) {
	base, err := NewExposure(testExposureInput())
	require.NoError(t, err)
	changedInput := testExposureInput()
	changedInput.Network = NetworkBridge
	changed, err := NewExposure(changedInput)
	require.NoError(t, err)
	require.NotEqual(t, base.Digest(), changed.Digest())
}

func testExposureInput() ExposureInput {
	return ExposureInput{
		Backend:             BackendDocker,
		Scope:               ScopeSession,
		WorkspaceIdentity:   "default:session:default",
		WorkspaceMode:       WorkspaceNone,
		Network:             NetworkNone,
		SecretReferences:    []string{"token"},
		ImageDigest:         "image@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ImageContractDigest: "contract",
		SecurityGeneration:  "generation",
		PolicyHash:          "policy",
		Limits: Limits{
			MemoryBytes:       1 << 20,
			CPUMilli:          100,
			PIDs:              16,
			OpenFiles:         32,
			TemporaryBytes:    1 << 20,
			OutputBytes:       1024,
			ControlInputBytes: 1024,
			Runtime:           time.Minute,
			StopGrace:         time.Second,
		},
		EnvironmentIdleExpiry:   time.Minute,
		SharedDisabledRetention: time.Hour,
	}
}
