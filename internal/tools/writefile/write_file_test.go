package writefile

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wandxy/morph/internal/guardrails"
	"github.com/wandxy/morph/internal/permissions"
	"github.com/wandxy/morph/internal/tools"
	"github.com/wandxy/morph/internal/tools/common"
	nativemocks "github.com/wandxy/morph/internal/tools/mocks"
)

func TestWriteFile_ToolCreatesFile(t *testing.T) {
	root := t.TempDir()
	registry := nativemocks.RegisterRuntime(t, root, guardrails.CommandPolicy{}, Definition)

	result, err := registry.Invoke(context.Background(), tools.Call{Name: "write_file", Input: `{"path":"nested/file.txt","content":"hello"}`})

	require.NoError(t, err)
	var payload struct {
		Path         string `json:"path"`
		BytesWritten int    `json:"bytes_written"`
		Created      bool   `json:"created"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Equal(t, "nested/file.txt", payload.Path)
	require.Equal(t, 5, payload.BytesWritten)
	require.True(t, payload.Created)
	content, readErr := os.ReadFile(filepath.Join(root, "nested", "file.txt"))
	require.NoError(t, readErr)
	require.Equal(t, "hello", string(content))
}

func TestWriteFile_ToolOverwritesExistingFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file.txt")
	require.NoError(t, os.WriteFile(path, []byte("before"), 0o644))
	registry := nativemocks.RegisterRuntime(t, root, guardrails.CommandPolicy{}, Definition)

	result, err := registry.Invoke(context.Background(), tools.Call{Name: "write_file", Input: `{"path":"file.txt","content":"after"}`})

	require.NoError(t, err)
	var payload struct {
		Path         string `json:"path"`
		BytesWritten int    `json:"bytes_written"`
		Created      bool   `json:"created"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Equal(t, "file.txt", payload.Path)
	require.Equal(t, 5, payload.BytesWritten)
	require.False(t, payload.Created)
	content, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	require.Equal(t, "after", string(content))
}

func TestWriteFile_EnforcementDeniesBeforeFilesystemMutation(t *testing.T) {
	root := t.TempDir()
	registry := nativemocks.RegisterRuntimeWithPermissionPolicy(
		t,
		root,
		guardrails.CommandPolicy{},
		permissions.Policy{Rules: []permissions.Rule{{
			Name: "deny writes", Tools: []string{"write_file"}, Decision: permissions.DecisionDeny,
		}}},
		Definition,
	)
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{Kind: permissions.ActorLocalOwner}, Surface: permissions.SurfaceCLI,
	})

	result, err := registry.Invoke(ctx, tools.Call{
		Name: "write_file", Input: `{"path":"blocked.txt","content":"should not exist"}`,
	})

	require.NoError(t, err)
	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, permissions.ErrorCodeDenied, toolErr.Code)
	_, statErr := os.Stat(filepath.Join(root, "blocked.txt"))
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestWriteFile_ResolvePermissionValidatesAndNormalizesTarget(t *testing.T) {
	resolver := Definition(nil).ResolvePermission

	path := filepath.Join("nested", "file.txt")
	inputs, err := resolver(context.Background(), tools.Call{Input: `{"path":` + nativemocks.QuoteJSON(path) + `}`})
	require.NoError(t, err)
	require.Equal(t, "nested/file.txt", inputs[0].Operation.Target)

	inputs, err = resolver(context.Background(), tools.Call{Input: `{"path":`})
	require.EqualError(t, err, "invalid tool input")
	require.Nil(t, inputs)

	inputs, err = resolver(context.Background(), tools.Call{Input: `{}`})
	require.EqualError(t, err, "path is required")
	require.Nil(t, inputs)
}

func TestWriteFile_ExplicitGrantDoesNotOverrideFilesystemRoots(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	registry := nativemocks.RegisterRuntimeWithPermissionPolicy(
		t,
		root,
		guardrails.CommandPolicy{},
		permissions.Policy{Rules: []permissions.Rule{{
			Name: "allow writes", Tools: []string{"write_file"}, Decision: permissions.DecisionAllow,
		}}},
		Definition,
	)
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{Kind: permissions.ActorLocalOwner}, Surface: permissions.SurfaceCLI,
	})

	result, err := registry.Invoke(ctx, tools.Call{
		Name: "write_file", Input: `{"path":` + nativemocks.QuoteJSON(outside) + `,"content":"blocked"}`,
	})

	require.NoError(t, err)
	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, "path_outside_roots", toolErr.Code)
	_, statErr := os.Stat(outside)
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestWriteFile_FullAccessOverridesFilesystemRoots(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	registry := nativemocks.RegisterRuntimeWithPermissionPolicy(
		t,
		root,
		guardrails.CommandPolicy{},
		permissions.Policy{Preset: permissions.PresetFullAccess},
		Definition,
	)

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name: "write_file", Input: `{"path":` + nativemocks.QuoteJSON(outside) + `,"content":"allowed"}`,
	})

	require.NoError(t, err)
	require.Empty(t, result.Error)
	content, readErr := os.ReadFile(outside)
	require.NoError(t, readErr)
	require.Equal(t, "allowed", string(content))
}

func TestWriteFile_AskPresetApprovesExternalWriteBeforeBypassingRoots(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	approver := &approverStub{}
	registry := tools.NewDefaultRegistry(tools.RegistryOptions{
		PermissionPolicy: permissions.Policy{Preset: permissions.PresetAskForApproval},
		ApprovalService:  approver,
	})
	require.NoError(t, registry.Register(Definition(nativemocks.NewRuntime(root, guardrails.CommandPolicy{}))))
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{Kind: permissions.ActorLocalOwner}, Surface: permissions.SurfaceCLI,
	})

	result, err := registry.Invoke(ctx, tools.Call{
		Name: "write_file", Input: `{"path":` + nativemocks.QuoteJSON(outside) + `,"content":"approved"}`,
	})

	require.NoError(t, err)
	require.Empty(t, result.Error)
	require.Equal(t, 1, approver.calls)
	require.Equal(t, permissions.TargetScopeExternal, approver.input.Operation.TargetScope)
	content, readErr := os.ReadFile(outside)
	require.NoError(t, readErr)
	require.Equal(t, "approved", string(content))
}

func TestWriteFile_AskPresetBlocksExternalWriteWithoutApprovalService(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	registry := tools.NewDefaultRegistry(tools.RegistryOptions{
		PermissionPolicy: permissions.Policy{Preset: permissions.PresetAskForApproval},
	})
	require.NoError(t, registry.Register(Definition(nativemocks.NewRuntime(root, guardrails.CommandPolicy{}))))
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{Kind: permissions.ActorLocalOwner}, Surface: permissions.SurfaceCLI,
	})

	result, err := registry.Invoke(ctx, tools.Call{
		Name: "write_file", Input: `{"path":` + nativemocks.QuoteJSON(outside) + `,"content":"blocked"}`,
	})

	require.NoError(t, err)
	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, permissions.ErrorCodeApprovalRequired, toolErr.Code)
	_, statErr := os.Stat(outside)
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestWriteFile_ApprovePresetApprovesExternalWriteBeforeBypassingRoots(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	approver := &approverStub{}
	registry := tools.NewDefaultRegistry(tools.RegistryOptions{
		PermissionPolicy: permissions.Policy{Preset: permissions.PresetApproveForMe},
		ApprovalService:  approver,
	})
	require.NoError(t, registry.Register(Definition(nativemocks.NewRuntime(root, guardrails.CommandPolicy{}))))
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{Kind: permissions.ActorLocalOwner}, Surface: permissions.SurfaceCLI,
	})

	result, err := registry.Invoke(ctx, tools.Call{
		Name: "write_file", Input: `{"path":` + nativemocks.QuoteJSON(outside) + `,"content":"allowed"}`,
	})

	require.NoError(t, err)
	require.Empty(t, result.Error)
	require.Equal(t, 1, approver.calls)
	require.Equal(t, permissions.TargetScopeExternal, approver.input.Operation.TargetScope)
	content, readErr := os.ReadFile(outside)
	require.NoError(t, readErr)
	require.Equal(t, "allowed", string(content))
}

func TestWriteFile_EnforcementNormalizesTargetBeforeRuleMatching(t *testing.T) {
	root := t.TempDir()
	registry := nativemocks.RegisterRuntimeWithPermissionPolicy(
		t,
		root,
		guardrails.CommandPolicy{},
		permissions.Policy{Rules: []permissions.Rule{
			{Name: "allow writes", Tools: []string{"write_file"}, Decision: permissions.DecisionAllow},
			{Name: "deny blocked file", TargetPrefixes: []string{"blocked.txt"}, Decision: permissions.DecisionDeny},
		}},
		Definition,
	)
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{Kind: permissions.ActorLocalOwner}, Surface: permissions.SurfaceCLI,
	})

	result, err := registry.Invoke(ctx, tools.Call{
		Name: "write_file", Input: `{"path":"nested/../blocked.txt","content":"blocked"}`,
	})

	require.NoError(t, err)
	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, permissions.ErrorCodeDenied, toolErr.Code)
	_, statErr := os.Stat(filepath.Join(root, "blocked.txt"))
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestWriteFile_HandlerValidatesInputBeforeWriting(t *testing.T) {
	handler := Definition(nativemocks.NewRuntime(t.TempDir(), guardrails.CommandPolicy{})).Handler
	tests := []struct {
		name  string
		input string
		code  string
	}{
		{name: "invalid JSON", input: `{"path":`, code: "invalid_input"},
		{name: "missing path", input: `{"content":"hello"}`, code: "invalid_input"},
		{name: "binary content", input: "{\"path\":\"file.txt\",\"content\":\"\\u0000\"}", code: "not_text"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := handler.Invoke(context.Background(), tools.Call{Input: test.input})

			require.NoError(t, err)
			require.Contains(t, result.Error, `"code":"`+test.code+`"`)
		})
	}
}

func TestWriteFile_HandlerReturnsFilesystemErrors(t *testing.T) {
	root := t.TempDir()
	handler := Definition(nativemocks.NewRuntime(root, guardrails.CommandPolicy{})).Handler

	outside := filepath.Join(t.TempDir(), "outside.txt")
	result, err := handler.Invoke(context.Background(), tools.Call{
		Input: `{"path":` + nativemocks.QuoteJSON(outside) + `,"content":"blocked"}`,
	})
	require.NoError(t, err)
	require.Contains(t, result.Error, `"code":"path_outside_roots"`)

	originalMkdirAll := common.MkdirAll
	originalWriteFile := common.WriteFile
	t.Cleanup(func() {
		common.MkdirAll = originalMkdirAll
		common.WriteFile = originalWriteFile
	})

	common.MkdirAll = func(string, os.FileMode) error { return os.ErrPermission }
	result, err = handler.Invoke(context.Background(), tools.Call{
		Input: `{"path":"nested/file.txt","content":"blocked"}`,
	})
	require.NoError(t, err)
	require.Contains(t, result.Error, `"code":"access_denied"`)

	common.MkdirAll = originalMkdirAll
	common.WriteFile = func(string, []byte, os.FileMode) error { return errors.New("write failed") }
	result, err = handler.Invoke(context.Background(), tools.Call{
		Input: `{"path":"file.txt","content":"blocked","create_dirs":false}`,
	})
	require.NoError(t, err)
	require.Contains(t, result.Error, "write failed")
}

type approverStub struct {
	calls int
	input permissions.EvaluationInput
	err   error
}

func (s *approverStub) Authorize(_ context.Context, input permissions.EvaluationInput) error {
	s.calls++
	s.input = input
	return s.err
}

func (s *approverStub) PrepareBatch(
	ctx context.Context,
	inputs []permissions.EvaluationInput,
) (permissions.BatchApproval, error) {
	if len(inputs) != 1 {
		return nil, errors.New("approver stub expects one operation")
	}
	if err := s.Authorize(ctx, inputs[0]); err != nil {
		return nil, err
	}
	return approvalBatchStub{}, nil
}

type approvalBatchStub struct{}

func (approvalBatchStub) Commit(context.Context) error {
	return nil
}
