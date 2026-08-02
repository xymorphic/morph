package searchfiles

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

func TestSearchFiles_ToolSearchesThroughExecutionService(t *testing.T) {
	root := t.TempDir()
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(root, "main.go"),
			[]byte("package main\nfunc main() { println(\"hello\") }\n"),
			0o644,
		),
	)
	registry := nativemocks.RegisterRuntime(t, root, guardrails.CommandPolicy{}, Definition)

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name:  "search_files",
		Input: `{"pattern":"println","path":"."}`,
	})
	require.NoError(t, err)

	var payload struct {
		Path    string         `json:"path"`
		Pattern string         `json:"pattern"`
		Matches []contentMatch `json:"matches"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Equal(t, ".", payload.Path)
	require.Equal(t, "println", payload.Pattern)
	require.Equal(t, []contentMatch{{
		Path:   "main.go",
		Line:   2,
		Column: 15,
		Text:   `func main() { println("hello") }`,
	}}, payload.Matches)
	require.Equal(t, `main.go:2: func main() { println("hello") }`, result.SemanticContent)
}

func TestSearchFiles_ToolValidatesInput(t *testing.T) {
	registry := nativemocks.RegisterRuntime(t, t.TempDir(), guardrails.CommandPolicy{}, Definition)

	result, err := registry.Invoke(
		context.Background(),
		tools.Call{
			Name:  "search_files",
			Input: `{"pattern":`,
		},
	)
	require.NoError(t, err)
	require.Contains(t, result.Error, `"code":"invalid_input"`)

	result, err = registry.Invoke(
		context.Background(),
		tools.Call{
			Name:  "search_files",
			Input: `{"pattern":" "}`,
		},
	)
	require.NoError(t, err)
	require.Contains(t, result.Error, "pattern is required")
}

func TestSearchFiles_ToolRejectsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "file.txt")
	require.NoError(t, os.WriteFile(outside, []byte("needle\n"), 0o644))
	registry := nativemocks.RegisterRuntime(t, root, guardrails.CommandPolicy{}, Definition)

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name:  "search_files",
		Input: `{"pattern":"needle","path":` + nativemocks.QuoteJSON(outside) + `}`,
	})

	require.NoError(t, err)
	require.Contains(t, result.Error, `"code":"path_outside_roots"`)
}

func TestSearchFiles_AskPresetSearchesOutsideWorkspace(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "file.txt")
	require.NoError(t, os.WriteFile(outside, []byte("needle\n"), 0o644))
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
		Name: "search_files", Input: `{"pattern":"needle","path":` + nativemocks.QuoteJSON(outside) + `}`,
	})

	require.NoError(t, err)
	require.Empty(t, result.Error)
	var payload struct {
		Matches []contentMatch `json:"matches"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Equal(
		t,
		[]contentMatch{
			{
				Path:   "file.txt",
				Line:   1,
				Column: 1,
				Text:   "needle",
			},
		},
		payload.Matches,
	)
}

func TestSearchFiles_ToolHonorsHiddenAndResultLimits(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, ".hidden"), []byte("needle\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("needle\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "b.txt"), []byte("needle\n"), 0o644))
	registry := nativemocks.RegisterRuntime(t, root, guardrails.CommandPolicy{}, Definition)

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name:  "search_files",
		Input: `{"pattern":"needle","path":".","max_results":1}`,
	})

	require.NoError(t, err)
	var payload struct {
		Matches []contentMatch `json:"matches"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Equal(
		t,
		[]contentMatch{
			{
				Path:   "a.txt",
				Line:   1,
				Column: 1,
				Text:   "needle",
			},
		},
		payload.Matches,
	)
}

func TestProjectSemanticContent_RejectsMalformedOutput(t *testing.T) {
	require.Empty(t, projectSemanticContent(tools.Call{}, tools.Result{Output: "not-json"}))
}
