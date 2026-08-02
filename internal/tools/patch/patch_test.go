package patch

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/guardrails"
	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/internal/tools"
	nativemocks "github.com/xymorphic/morph/internal/tools/mocks"
)

func TestPatch_ToolAppliesThroughExecutionService(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "hello.txt")
	require.NoError(t, os.WriteFile(path, []byte("hello\n"), 0o644))
	registry := nativemocks.RegisterRuntime(t, root, guardrails.CommandPolicy{}, Definition)

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name:  "patch",
		Input: `{"patch":"--- a/hello.txt\n+++ b/hello.txt\n@@ -1 +1 @@\n-hello\n+world\n"}`,
	})

	require.NoError(t, err)
	require.Empty(t, result.Error)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "world\n", string(raw))
	require.Contains(t, result.Output, "hello.txt")
}

func TestPatch_ToolCreatesFile(t *testing.T) {
	root := t.TempDir()
	registry := nativemocks.RegisterRuntime(t, root, guardrails.CommandPolicy{}, Definition)

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name:  "patch",
		Input: `{"patch":"--- /dev/null\n+++ b/nested/new.txt\n@@ -0,0 +1 @@\n+created\n"}`,
	})

	require.NoError(t, err)
	require.Empty(t, result.Error)
	raw, err := os.ReadFile(filepath.Join(root, "nested", "new.txt"))
	require.NoError(t, err)
	require.Equal(t, "created\n", string(raw))
}

func TestPatch_EnforcementChecksEveryTargetBeforeMutation(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "allowed.txt"), []byte("old\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "blocked.txt"), []byte("old\n"), 0o644))
	registry := nativemocks.RegisterRuntimeWithPermissionPolicy(
		t,
		root,
		guardrails.CommandPolicy{},
		permissions.Policy{Rules: []permissions.Rule{{
			Name:           "deny blocked",
			TargetPrefixes: []string{"blocked.txt"},
			Decision:       permissions.DecisionDeny,
		}}},
		Definition,
	)

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name:  "patch",
		Input: `{"patch":"--- a/allowed.txt\n+++ b/allowed.txt\n@@ -1 +1 @@\n-old\n+new\n--- a/blocked.txt\n+++ b/blocked.txt\n@@ -1 +1 @@\n-old\n+new\n"}`,
	})

	require.NoError(t, err)
	require.Contains(t, result.Error, permissions.ErrorCodeDenied)
	for _, name := range []string{"allowed.txt", "blocked.txt"} {
		raw, readErr := os.ReadFile(filepath.Join(root, name))
		require.NoError(t, readErr)
		require.Equal(t, "old\n", string(raw))
	}
}

func TestPatch_ToolValidatesPatchKindsAndConflicts(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "hello.txt"), []byte("other\n"), 0o644))
	registry := nativemocks.RegisterRuntime(t, root, guardrails.CommandPolicy{}, Definition)
	tests := []struct {
		name  string
		input string
		code  string
	}{
		{name: "invalid JSON", input: `{"patch":`, code: "invalid_input"},
		{
			name:  "blank",
			input: `{"patch":" "}`,
			code:  "invalid_input",
		},
		{
			name:  "delete",
			input: `{"patch":"--- a/hello.txt\n+++ /dev/null\n@@ -1 +0,0 @@\n-other\n"}`,
			code:  "invalid_input",
		},
		{
			name:  "rename",
			input: `{"patch":"similarity index 100%\nrename from hello.txt\nrename to renamed.txt\n"}`,
			code:  "invalid_input",
		},
		{
			name:  "conflict",
			input: `{"patch":"--- a/hello.txt\n+++ b/hello.txt\n@@ -1 +1 @@\n-missing\n+new\n"}`,
			code:  "conflict",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := registry.Invoke(
				context.Background(),
				tools.Call{
					Name:  "patch",
					Input: test.input,
				},
			)
			require.NoError(t, err)
			require.Contains(t, result.Error, `"code":"`+test.code+`"`)
		})
	}
}

func TestPatch_ToolRejectsOutsideAllowedRoots(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	require.NoError(t, os.WriteFile(outside, []byte("old\n"), 0o644))
	registry := nativemocks.RegisterRuntime(t, root, guardrails.CommandPolicy{}, Definition)

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name:  "patch",
		Input: `{"patch":"--- ` + outside + `\n+++ ` + outside + `\n@@ -1 +1 @@\n-old\n+new\n"}`,
	})

	require.NoError(t, err)
	require.Contains(t, result.Error, `"code":"path_outside_roots"`)
}

func TestStripPath_HandlesSpecialCases(t *testing.T) {
	require.Equal(t, "/dev/null", stripPath("/dev/null", 0))
	require.Equal(t, "file.txt", stripPath("a/nested/file.txt", 9))
	require.Equal(t, filepath.Join("nested", "file.txt"), stripPath("b/nested/file.txt", 0))
}
