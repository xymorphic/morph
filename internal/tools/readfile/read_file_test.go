package readfile

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

func TestReadFile_ToolReadsText(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "file.txt"), []byte("hello"), 0o644))
	registry := nativemocks.RegisterRuntime(t, root, guardrails.CommandPolicy{}, Definition)

	result, err := registry.Invoke(context.Background(), tools.Call{Name: "read_file", Input: `{"path":"file.txt"}`})
	require.NoError(t, err)

	var payload struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Bytes   int    `json:"bytes"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Equal(t, "file.txt", payload.Path)
	require.Equal(t, "hello", payload.Content)
	require.Equal(t, 5, payload.Bytes)
	require.Equal(t, "content: hello\npath: file.txt", result.SemanticContent)
}

func TestReadFile_ToolRejectsInvalidJSONInput(t *testing.T) {
	root := t.TempDir()
	registry := nativemocks.RegisterRuntime(t, root, guardrails.CommandPolicy{}, Definition)

	result, err := registry.Invoke(context.Background(), tools.Call{Name: "read_file", Input: `{"path":`})

	require.NoError(t, err)
	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, "invalid_input", toolErr.Code)
	require.Equal(t, "invalid tool input", toolErr.Message)
}

func TestReadFile_ToolRejectsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "file.txt")
	require.NoError(t, os.WriteFile(outside, []byte("secret"), 0o644))
	registry := nativemocks.RegisterRuntime(t, root, guardrails.CommandPolicy{}, Definition)

	result, err := registry.Invoke(context.Background(), tools.Call{Name: "read_file", Input: `{"path":` + nativemocks.QuoteJSON(outside) + `}`})

	require.NoError(t, err)
	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, "path_outside_roots", toolErr.Code)
}

func TestReadFile_AskPresetAllowsExternalReadWithoutPrompt(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "file.txt")
	require.NoError(t, os.WriteFile(outside, []byte("external"), 0o644))
	registry := nativemocks.RegisterRuntimeWithPermissionPolicy(
		t,
		root,
		guardrails.CommandPolicy{},
		permissions.Policy{Preset: permissions.PresetAskForApproval},
		Definition,
	)
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{Kind: permissions.ActorLocalOwner}, Surface: permissions.SurfaceTUI,
	})

	result, err := registry.Invoke(ctx, tools.Call{
		Name: "read_file", Input: `{"path":` + nativemocks.QuoteJSON(outside) + `}`,
	})

	require.NoError(t, err)
	require.Empty(t, result.Error)
	var payload struct {
		Content string `json:"content"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Equal(t, "external", payload.Content)
}

func TestReadFile_ToolRejectsDirectories(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "nested"), 0o755))
	registry := nativemocks.RegisterRuntime(t, root, guardrails.CommandPolicy{}, Definition)

	result, err := registry.Invoke(context.Background(), tools.Call{Name: "read_file", Input: `{"path":"nested"}`})

	require.NoError(t, err)
	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, "invalid_input", toolErr.Code)
}

func TestReadFile_ResolvePermissionRequiresPath(t *testing.T) {
	inputs, err := Definition(nil).ResolvePermission(context.Background(), tools.Call{Input: `{}`})

	require.Nil(t, inputs)
	require.EqualError(t, err, "path is required")
}

func TestReadFile_HandlerReturnsDecodeAndPathErrors(t *testing.T) {
	root := t.TempDir()
	handler := Definition(nativemocks.NewRuntime(root, guardrails.CommandPolicy{})).Handler

	result, err := handler.Invoke(context.Background(), tools.Call{Input: `{"path":`})
	require.NoError(t, err)
	require.Contains(t, result.Error, `"code":"invalid_input"`)

	outside := filepath.Join(t.TempDir(), "outside.txt")
	result, err = handler.Invoke(context.Background(), tools.Call{
		Input: `{"path":` + nativemocks.QuoteJSON(outside) + `}`,
	})
	require.NoError(t, err)
	require.Contains(t, result.Error, `"code":"path_outside_roots"`)
}
