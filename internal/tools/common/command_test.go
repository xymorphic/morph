package common

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	commandplan "github.com/wandxy/morph/internal/command"
	"github.com/wandxy/morph/internal/guardrails"
	"github.com/wandxy/morph/internal/permissions"
	toolmocks "github.com/wandxy/morph/internal/tools/mocks"
)

func TestCommandErrorCode_ClassifiesContextAndInputFailures(t *testing.T) {
	require.Equal(t, "command_failed", CommandErrorCode(context.Canceled))
	require.Equal(t, "command_failed", CommandErrorCode(context.DeadlineExceeded))
	require.Equal(t, "invalid_input", CommandErrorCode(errors.New("invalid")))
}

func TestAnalyzeCommand_UsesRuntimeExecutionContext(t *testing.T) {
	root := t.TempDir()
	runtime := &toolmocks.Runtime{
		FilePolicyValue:         guardrails.FilesystemPolicy{Roots: []string{root}},
		CommandShellValue:       "/bin/sh",
		CommandIdentityKeyValue: []byte("0123456789abcdef0123456789abcdef"),
	}

	plan, err := AnalyzeCommand(
		context.Background(),
		runtime,
		commandplan.ModePOSIXShell,
		"printf done",
		nil,
		"nested",
		map[string]string{"MORPH_VALUE": "value"},
	)

	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "nested"), plan.CWD)
	require.Regexp(t, `^workspace:[0-9a-f]{64}$`, plan.CWDIdentity)
	require.Equal(t, "/bin/sh", plan.ShellPath)
	require.NotEmpty(t, plan.EnvironmentDigest)
	require.NotEmpty(t, plan.Digest())

	withoutRuntime, err := AnalyzeCommand(
		context.Background(),
		nil,
		commandplan.ModeDirect,
		"true",
		nil,
		"",
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, commandplan.ModeDirect, withoutRuntime.Mode)
}

func TestCommandPermissionInputs_ReusesIdentityAcrossWorkspaceDirectories(t *testing.T) {
	root := t.TempDir()
	runtime := &toolmocks.Runtime{
		FilePolicyValue:         guardrails.FilesystemPolicy{Roots: []string{root}},
		CommandIdentityKeyValue: []byte("0123456789abcdef0123456789abcdef"),
	}
	left, err := AnalyzeCommand(
		context.Background(), runtime, commandplan.ModeDirect, "git", []string{"status"},
		filepath.Join(root, "one"), nil,
	)
	require.NoError(t, err)
	right, err := AnalyzeCommand(
		context.Background(), runtime, commandplan.ModeDirect, "git", []string{"status"},
		filepath.Join(root, "two"), nil,
	)
	require.NoError(t, err)

	leftInputs := CommandPermissionInputs(
		context.Background(), runtime, left, permissions.ActionExecute,
		[]permissions.Effect{permissions.EffectExecution},
	)
	rightInputs := CommandPermissionInputs(
		context.Background(), runtime, right, permissions.ActionExecute,
		[]permissions.Effect{permissions.EffectExecution},
	)

	require.Equal(t, leftInputs[0].Operation.Target, rightInputs[0].Operation.Target)
	require.True(t, leftInputs[1].Operation.Command.Equal(*rightInputs[1].Operation.Command))
}

func TestCommandPermissionInputs_DoesNotAuthorizeNullDeviceRedirection(t *testing.T) {
	runtime := &toolmocks.Runtime{
		FilePolicyValue:         guardrails.FilesystemPolicy{Roots: []string{t.TempDir()}},
		CommandIdentityKeyValue: []byte("0123456789abcdef0123456789abcdef"),
	}
	plan, err := AnalyzeCommand(
		context.Background(), runtime, commandplan.ModePOSIXShell,
		"printf ok > /dev/null 2> /dev/null", nil, "", nil,
	)
	require.NoError(t, err)

	inputs := CommandPermissionInputs(
		context.Background(), runtime, plan, permissions.ActionExecute,
		[]permissions.Effect{permissions.EffectExecution},
	)

	require.Len(t, inputs, 2)
	require.Equal(t, permissions.ResourceFile, inputs[0].Operation.Resource)
	require.Equal(t, permissions.ResourceProcess, inputs[1].Operation.Resource)
}

func TestCommandPermissionInputs_DescribesInvocationsRedirectsAndDebuggerAccess(t *testing.T) {
	root := t.TempDir()
	runtime := &toolmocks.Runtime{
		FilePolicyValue: guardrails.FilesystemPolicy{Roots: []string{root}},
	}
	plan, err := commandplan.Analyze(context.Background(), commandplan.Request{
		Mode:          commandplan.ModePOSIXShell,
		Command:       "printf done < input.txt > output.txt; make test",
		CWD:           root,
		WorkspaceRoot: root,
		ShellPath:     "/bin/sh",
	})
	require.NoError(t, err)
	plan.DebuggerAttach = true
	plan.Redirects = append(plan.Redirects, commandplan.Redirect{
		Action: commandplan.RedirectUpdate,
		Path:   "",
		Static: false,
	})

	inputs := CommandPermissionInputs(
		context.Background(),
		runtime,
		plan,
		permissions.ActionExecute,
		[]permissions.Effect{permissions.EffectExecution},
	)

	require.Len(t, inputs, 7)
	require.Equal(t, permissions.ResourceFile, inputs[0].Operation.Resource)
	require.Equal(t, permissions.ActionRead, inputs[0].Operation.Action)
	require.Equal(t, plan.CWDIdentity, inputs[0].Operation.Target)
	require.Equal(t, permissions.TargetScopeWorkspace, inputs[0].Operation.TargetScope)

	require.Equal(t, permissions.ResourceProcess, inputs[1].Operation.Resource)
	require.NotNil(t, inputs[1].Operation.Command)
	require.Equal(t, "printf", inputs[1].Operation.Command.Executable)

	require.Equal(t, permissions.ResourceProcess, inputs[2].Operation.Resource)
	require.Contains(t, inputs[2].Operation.Effects, permissions.EffectIndirectExecution)
	require.Equal(t, "make", inputs[2].Operation.Command.Executable)

	require.Equal(t, permissions.ActionRead, inputs[3].Operation.Action)
	require.Equal(t, filepath.Join(root, "input.txt"), inputs[3].Operation.Target)
	require.Equal(t, permissions.ActionCreate, inputs[4].Operation.Action)
	require.Equal(t, filepath.Join(root, "output.txt"), inputs[4].Operation.Target)
	require.Equal(t, permissions.ActionUpdate, inputs[5].Operation.Action)
	require.Equal(t, filepath.Join(root, "output.txt"), inputs[5].Operation.Target)

	debugger := inputs[6]
	require.Equal(t, permissions.ResourceBrowser, debugger.Operation.Resource)
	require.Equal(t, permissions.ActionConnect, debugger.Operation.Action)
	require.Contains(t, debugger.ApprovalReason, "managed browser")
}

func TestCheckCommandPlan_AppliesGuardrailsAndAuthorizedApproval(t *testing.T) {
	root := t.TempDir()
	runtime := &toolmocks.Runtime{
		FilePolicyValue: guardrails.FilesystemPolicy{Roots: []string{root}},
		CommandPolicyValue: guardrails.CommandPolicy{
			DenyCommands: []commandplan.Selector{{
				Executable: "git",
			}},
		},
	}
	plan, err := commandplan.Analyze(context.Background(), commandplan.Request{
		Mode: commandplan.ModeDirect, Command: "git", Args: []string{"status"},
		CWD: root, WorkspaceRoot: root,
	})
	require.NoError(t, err)

	code, message := CheckCommandPlan(
		context.Background(),
		runtime,
		plan,
		permissions.ActionExecute,
		[]permissions.Effect{permissions.EffectExecution},
	)
	require.Equal(t, "command_denied", code)
	require.Equal(t, "matched typed deny rule", message)

	runtime.CommandPolicyValue = guardrails.CommandPolicy{
		AskCommands: []commandplan.Selector{{
			Executable: "git",
		}},
	}
	authorization := permissions.AuthorizationContext{
		Actor:   permissions.Actor{Kind: permissions.ActorLocalOwner},
		Surface: permissions.SurfaceTUI,
	}
	ctx := permissions.WithContext(context.Background(), authorization)
	code, message = CheckCommandPlan(
		ctx,
		runtime,
		plan,
		permissions.ActionExecute,
		[]permissions.Effect{permissions.EffectExecution},
	)
	require.Equal(t, "approval_required", code)
	require.Contains(t, message, "Command matches approval rule")

	inputs := CommandPermissionInputs(
		ctx,
		runtime,
		plan,
		permissions.ActionExecute,
		[]permissions.Effect{permissions.EffectExecution},
	)
	ctx = permissions.WithAuthorizedOperations(ctx, []permissions.Operation{inputs[1].Operation})
	code, message = CheckCommandPlan(
		ctx,
		runtime,
		plan,
		permissions.ActionExecute,
		[]permissions.Effect{permissions.EffectExecution},
	)
	require.Empty(t, code)
	require.Empty(t, message)

	code, message = CheckCommandPlan(
		permissions.WithFullAccess(context.Background()),
		runtime,
		plan,
		permissions.ActionExecute,
		[]permissions.Effect{permissions.EffectExecution},
	)
	require.Empty(t, code)
	require.Empty(t, message)
}

func TestApplyCommandGuardrail_DeniesIncompletePlanOutsideInteractiveOwnerSurface(t *testing.T) {
	plan := commandplan.Plan{
		Mode: commandplan.ModePOSIXShell,
		Invocations: []commandplan.Invocation{{
			Mode: commandplan.ModePOSIXShell, Executable: "printf", Static: true,
		}},
	}
	inputs := []permissions.EvaluationInput{{Operation: permissions.Operation{
		Resource: permissions.ResourceProcess,
		Action:   permissions.ActionExecute,
	}}}

	applyCommandGuardrail(context.Background(), nil, plan, inputs)

	require.Equal(
		t,
		"incomplete command plans require an interactive local owner",
		inputs[0].HardDenyReason,
	)

	require.False(t, isInteractiveCommandApproval(context.Background()))
	require.True(t, isInteractiveCommandApproval(permissions.WithContext(
		context.Background(),
		permissions.AuthorizationContext{
			Actor:   permissions.Actor{Kind: permissions.ActorLocalOwner},
			Surface: permissions.SurfaceCLI,
		},
	)))

	require.NotPanics(t, func() {
		applyCommandGuardrail(context.Background(), nil, plan, nil)
		applyCommandGuardrail(
			context.Background(),
			nil,
			plan,
			[]permissions.EvaluationInput{{Operation: permissions.Operation{
				Resource: permissions.ResourceFile,
			}}},
		)
	})
}

func TestCommandHelpers_MapRedirectsAndCloneEnvironment(t *testing.T) {
	action, effects := redirectPermission(commandplan.RedirectRead)
	require.Equal(t, permissions.ActionRead, action)
	require.Equal(t, []permissions.Effect{permissions.EffectRead}, effects)

	action, effects = redirectPermission(commandplan.RedirectCreate)
	require.Equal(t, permissions.ActionCreate, action)
	require.Equal(t, []permissions.Effect{permissions.EffectWrite}, effects)

	action, effects = redirectPermission(commandplan.RedirectUpdate)
	require.Equal(t, permissions.ActionUpdate, action)
	require.Equal(t, []permissions.Effect{permissions.EffectWrite}, effects)

	require.Nil(t, cloneStringMap(nil))
	original := map[string]string{"KEY": "value"}
	cloned := cloneStringMap(original)
	cloned["KEY"] = "changed"
	require.Equal(t, "value", original["KEY"])
}
