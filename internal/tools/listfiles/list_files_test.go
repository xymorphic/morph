package listfiles

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/guardrails"
	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/internal/tools"
	nativemocks "github.com/xymorphic/morph/internal/tools/mocks"
)

type listPayload struct {
	Root    string `json:"root"`
	Path    string `json:"path"`
	Entries []struct {
		Path string `json:"path"`
		Type string `json:"type"`
		Size int64  `json:"size"`
	} `json:"entries"`
}

func TestListFiles_ToolListsThroughExecutionService(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "nested"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "visible.txt"), []byte("hello"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".hidden"), []byte("secret"), 0o644))
	registry := nativemocks.RegisterRuntime(t, root, guardrails.CommandPolicy{}, Definition)

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name:  "list_files",
		Input: `{"path":".","recursive":false}`,
	})

	require.NoError(t, err)
	var payload listPayload
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Equal(t, root, payload.Root)
	require.Equal(t, ".", payload.Path)
	require.Len(t, payload.Entries, 2)
	require.Equal(t, "nested", payload.Entries[0].Path)
	require.Equal(t, "dir", payload.Entries[0].Type)
	require.Equal(t, "visible.txt", payload.Entries[1].Path)
}

func TestListFiles_ToolSupportsRecursiveHiddenAndLimits(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "nested"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "nested", "one.txt"), []byte("1"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".hidden"), []byte("2"), 0o644))
	registry := nativemocks.RegisterRuntime(t, root, guardrails.CommandPolicy{}, Definition)

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name:  "list_files",
		Input: `{"path":".","recursive":true,"include_hidden":true,"max_entries":2}`,
	})

	require.NoError(t, err)
	var payload listPayload
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Len(t, payload.Entries, 2)
}

func TestListFiles_ToolRejectsInvalidInputAndOutsideRoot(t *testing.T) {
	root := t.TempDir()
	registry := nativemocks.RegisterRuntime(t, root, guardrails.CommandPolicy{}, Definition)

	result, err := registry.Invoke(
		context.Background(),
		tools.Call{
			Name:  "list_files",
			Input: `{"path":`,
		},
	)
	require.NoError(t, err)
	require.Contains(t, result.Error, `"code":"invalid_input"`)

	result, err = registry.Invoke(context.Background(), tools.Call{
		Name:  "list_files",
		Input: `{"path":` + nativemocks.QuoteJSON(t.TempDir()) + `}`,
	})
	require.NoError(t, err)
	require.Contains(t, result.Error, `"code":"path_outside_roots"`)
}

func TestListFiles_AskPresetListsOutsideWorkspace(t *testing.T) {
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "file.txt"), []byte("hello"), 0o644))
	registry := nativemocks.RegisterRuntimeWithPermissionPolicy(
		t,
		t.TempDir(),
		guardrails.CommandPolicy{},
		permissions.Policy{Preset: permissions.PresetAskForApproval},
		Definition,
	)
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{
			Kind: permissions.ActorLocalOwner,
		},
		Surface: permissions.SurfaceTUI,
	})

	result, err := registry.Invoke(ctx, tools.Call{
		Name:  "list_files",
		Input: `{"path":` + nativemocks.QuoteJSON(outside) + `}`,
	})

	require.NoError(t, err)
	require.Empty(t, result.Error)
	var payload listPayload
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Len(t, payload.Entries, 1)
	require.Equal(t, "file.txt", payload.Entries[0].Path)
}

func TestListFiles_DefinitionDeclaresStrictRequiredSchema(t *testing.T) {
	definition := Definition(nativemocks.NewRuntime(t.TempDir(), guardrails.CommandPolicy{}))
	require.Equal(
		t,
		[]string{"path", "recursive", "include_hidden", "max_entries"},
		definition.InputSchema["required"],
	)
}
