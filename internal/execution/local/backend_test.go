package local

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/wandxy/morph/internal/execution"
	"github.com/wandxy/morph/internal/guardrails"
)

func TestBackend_SearchFilesPreservesRegexRecursionAndHiddenPolicy(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "nested"), 0o755))
	require.NoError(
		t,
		os.WriteFile(filepath.Join(root, "nested", "visible.txt"), []byte("Ticket 42\n"), 0o600),
	)
	require.NoError(t, os.WriteFile(filepath.Join(root, ".hidden"), []byte("Ticket 99\n"), 0o600))
	path, err := execution.NewPreparedPath(execution.PreparedPathInput{
		LogicalPath:        root,
		HostSourceIdentity: root,
		ContainerPath:      root,
		Mode:               execution.MountReadWrite,
		Action:             execution.FilesystemSearch,
		SecurityGeneration: "generation",
	})
	require.NoError(t, err)

	exposure, err := execution.NewExposure(execution.ExposureInput{
		Backend:               execution.BackendLocal,
		Scope:                 execution.ScopeSession,
		WorkspaceIdentity:     "default:session:default",
		WorkspaceMode:         execution.WorkspaceReadWrite,
		Network:               execution.NetworkBridge,
		SecurityGeneration:    "generation",
		EnvironmentIdleExpiry: time.Minute,
		Limits: execution.Limits{
			Runtime:   time.Minute,
			StopGrace: time.Second,
		},
	})
	require.NoError(t, err)

	spec, err := execution.NewSpec(execution.Owner{
		Profile:            "default",
		ActorKind:          "local_owner",
		Surface:            "cli",
		PublicSessionID:    "default",
		EffectiveSessionID: "default",
	}, exposure, execution.Operation{
		Kind: execution.OperationFilesystem,
		Filesystem: &execution.FilesystemOperation{
			Action:    execution.FilesystemSearch,
			Path:      path,
			Query:     `Ticket \d+`,
			Recursive: true,
		},
	})
	require.NoError(t, err)

	matches, err := New(
		guardrails.FilesystemPolicy{Roots: []string{root}},
		nil,
	).SearchFiles(context.Background(), spec, 10)
	require.NoError(t, err)
	require.Len(t, matches, 1)
	require.Equal(t, "nested/visible.txt", matches[0].Path)
}

func TestBackend_SearchFilesSupportsLongLines(t *testing.T) {
	root := t.TempDir()
	longLine := strings.Repeat("a", 70*1024) + " needle"
	require.NoError(t, os.WriteFile(filepath.Join(root, "long.txt"), []byte(longLine+"\n"), 0o600))
	spec := getFilesystemSpec(t, root, execution.FilesystemOperation{
		Action: execution.FilesystemSearch,
		Query:  "needle",
	})

	matches, err := New(
		guardrails.FilesystemPolicy{Roots: []string{root}},
		nil,
	).SearchFiles(context.Background(), spec, 10)

	require.NoError(t, err)
	require.Len(t, matches, 1)
	require.Equal(t, 70*1024+2, matches[0].Column)
}

func getFilesystemSpec(
	t *testing.T,
	root string,
	operation execution.FilesystemOperation,
) execution.Spec {
	t.Helper()
	path, err := execution.NewPreparedPath(execution.PreparedPathInput{
		LogicalPath:        root,
		HostSourceIdentity: root,
		ContainerPath:      root,
		Mode:               execution.MountReadWrite,
		Action:             operation.Action,
		SecurityGeneration: "generation",
	})
	require.NoError(t, err)

	operation.Path = path

	exposure, err := execution.NewExposure(execution.ExposureInput{
		Backend:               execution.BackendLocal,
		Scope:                 execution.ScopeSession,
		WorkspaceIdentity:     "default:session:default",
		WorkspaceMode:         execution.WorkspaceReadWrite,
		Network:               execution.NetworkBridge,
		SecurityGeneration:    "generation",
		EnvironmentIdleExpiry: time.Minute,
		Limits: execution.Limits{
			Runtime:   time.Minute,
			StopGrace: time.Second,
		},
	})
	require.NoError(t, err)

	spec, err := execution.NewSpec(execution.Owner{
		Profile:            "default",
		ActorKind:          "local_owner",
		Surface:            "cli",
		PublicSessionID:    "default",
		EffectiveSessionID: "default",
	}, exposure, execution.Operation{
		Kind:       execution.OperationFilesystem,
		Filesystem: &operation,
	})
	require.NoError(t, err)

	return spec
}
