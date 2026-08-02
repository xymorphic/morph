package runcommand

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	commandplan "github.com/xymorphic/morph/internal/command"
	"github.com/xymorphic/morph/internal/execution"
	"github.com/xymorphic/morph/internal/guardrails"
	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/internal/tools"
	nativemocks "github.com/xymorphic/morph/internal/tools/mocks"
)

func TestDefinition_ExposesConfiguredSecretCatalog(t *testing.T) {
	runtime := &nativemocks.Runtime{ExecutionSecretCatalogValue: []execution.SecretCatalogEntry{
		{
			Name:        "staging_database",
			Description: "Run migrations against the staging database",
		},
		{
			Name:        "github_release",
			Description: "Publish release artifacts to GitHub",
		},
	}}

	properties := Definition(runtime).InputSchema["properties"].(map[string]any)
	secrets := properties["secrets"].(map[string]any)
	items := secrets["items"].(map[string]any)

	require.Equal(t, []string{"github_release", "staging_database"}, items["enum"])
	require.Contains(
		t,
		secrets["description"],
		"github_release — Publish release artifacts to GitHub",
	)
	require.Contains(
		t,
		secrets["description"],
		"staging_database — Run migrations against the staging database",
	)
	require.NotContains(t, secrets["description"], "SANDBOX_GITHUB_TOKEN")
}

func TestDefinition_HidesSecretInputWithoutConfiguredCatalog(t *testing.T) {
	properties := Definition(&nativemocks.Runtime{}).InputSchema["properties"].(map[string]any)

	require.NotContains(t, properties, "secrets")
}

var (
	askGitPushPolicy = guardrails.CommandPolicy{AskCommands: []commandplan.Selector{{
		Executable: "git", ArgumentPrefix: []string{"push"},
	}}}
	denyGitPushPolicy = guardrails.CommandPolicy{DenyCommands: []commandplan.Selector{{
		Executable: "git", ArgumentPrefix: []string{"push"},
	}}}
)

type runCommandPayload struct {
	ExitCode         int     `json:"exit_code"`
	Stdout           string  `json:"stdout"`
	Stderr           string  `json:"stderr"`
	TimedOut         bool    `json:"timed_out"`
	TimeoutSeconds   int     `json:"timeout_seconds"`
	ElapsedSeconds   float64 `json:"elapsed_seconds"`
	RemainingSeconds float64 `json:"remaining_seconds"`
}

func TestRunCommand_ToolRunsCommand(t *testing.T) {
	root := t.TempDir()
	registry := nativemocks.RegisterRuntime(t, root, guardrails.CommandPolicy{}, Definition)

	result, err := registry.Invoke(
		context.Background(),
		tools.Call{
			Name:  "run_command",
			Input: `{"command":"printf","args":["hello"]}`,
		},
	)

	require.NoError(t, err)
	var payload runCommandPayload
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Equal(t, 0, payload.ExitCode)
	require.Equal(t, "hello", payload.Stdout)
	require.Empty(t, payload.Stderr)
	require.False(t, payload.TimedOut)
	require.Equal(t, 30, payload.TimeoutSeconds)
	require.GreaterOrEqual(t, payload.ElapsedSeconds, 0.0)
	require.GreaterOrEqual(t, payload.RemainingSeconds, 0.0)
	require.LessOrEqual(t, payload.RemainingSeconds, float64(payload.TimeoutSeconds))
	require.Equal(t, "stdout: hello", result.SemanticContent)
}

func TestRunCommand_DirectModeTreatsShellMetacharactersAsLiteralArguments(t *testing.T) {
	root := t.TempDir()
	registry := nativemocks.RegisterRuntime(t, root, guardrails.CommandPolicy{}, Definition)

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name:  "run_command",
		Input: `{"command":"printf","args":["%s|%s|%s","&&","$(uname)","*"]}`,
	})

	require.NoError(t, err)
	var payload runCommandPayload
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Equal(t, "&&|$(uname)|*", payload.Stdout)
}

func TestRunCommand_ToolRejectsInvalidJSONInput(t *testing.T) {
	root := t.TempDir()
	registry := nativemocks.RegisterRuntime(t, root, guardrails.CommandPolicy{}, Definition)

	result, err := registry.Invoke(
		context.Background(),
		tools.Call{
			Name:  "run_command",
			Input: `{"command":`,
		},
	)

	require.NoError(t, err)
	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, "invalid_input", toolErr.Code)
	require.Equal(t, "invalid tool input", toolErr.Message)
}

func TestRunCommand_ToolRequiresCommand(t *testing.T) {
	root := t.TempDir()
	registry := nativemocks.RegisterRuntime(t, root, guardrails.CommandPolicy{}, Definition)

	result, err := registry.Invoke(
		context.Background(),
		tools.Call{
			Name:  "run_command",
			Input: `{"command":"   "}`,
		},
	)

	require.NoError(t, err)
	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, "invalid_input", toolErr.Code)
	require.Equal(t, "command is required", toolErr.Message)
}

func TestRunCommand_ToolReturnsApprovalRequiredWithoutExecution(t *testing.T) {
	root := t.TempDir()
	registry := nativemocks.RegisterRuntime(t, root, askGitPushPolicy, Definition)

	result, err := registry.Invoke(
		context.Background(),
		tools.Call{
			Name:  "run_command",
			Input: `{"command":"git","args":["push","origin","main"]}`,
		},
	)

	require.NoError(t, err)
	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, "approval_required", toolErr.Code)
	require.Contains(t, toolErr.Message, "command-selector:")
}

func TestRunCommand_ToolReturnsBuiltInApprovalMessageWithoutRule(t *testing.T) {
	root := t.TempDir()
	registry := nativemocks.RegisterRuntime(t, root, guardrails.CommandPolicy{}, Definition)

	result, err := registry.Invoke(
		context.Background(),
		tools.Call{
			Name:  "run_command",
			Input: `{"command":"rm","args":["-rf","/"]}`,
		},
	)

	require.NoError(t, err)
	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, "approval_required", toolErr.Code)
	require.Contains(t, toolErr.Message, "dangerous destructive command")
}

func TestRunCommand_ToolReturnsDeniedWithoutExecution(t *testing.T) {
	root := t.TempDir()
	registry := nativemocks.RegisterRuntime(t, root, denyGitPushPolicy, Definition)

	result, err := registry.Invoke(
		context.Background(),
		tools.Call{
			Name:  "run_command",
			Input: `{"command":"git","args":["push","origin","main"]}`,
		},
	)

	require.NoError(t, err)
	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, permissions.ErrorCodeDenied, toolErr.Code)
	require.Contains(t, toolErr.Message, "matched typed deny rule")
}

func TestRunCommand_EnforcementBlocksPolicyDenialBeforeExecution(t *testing.T) {
	root := t.TempDir()
	registry := nativemocks.RegisterRuntimeWithPermissionPolicy(
		t,
		root,
		guardrails.CommandPolicy{},
		permissions.Policy{Rules: []permissions.Rule{
			{
				Name:     "deny execution",
				Effects:  []permissions.Effect{permissions.EffectExecution},
				Decision: permissions.DecisionDeny,
			},
		}},
		Definition,
	)
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{
			Kind: permissions.ActorLocalOwner,
		},
		Surface: permissions.SurfaceCLI,
	})

	result, err := registry.Invoke(
		ctx,
		tools.Call{
			Name:  "run_command",
			Input: `{"command":"printf","args":["hello"]}`,
		},
	)

	require.NoError(t, err)
	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, permissions.ErrorCodeDenied, toolErr.Code)
}

func TestRunCommand_EnforcementPreservesCommandHardDenyAndApproval(t *testing.T) {
	tests := []struct {
		name          string
		commandPolicy guardrails.CommandPolicy
		code          string
	}{
		{
			name:          "hard deny",
			commandPolicy: denyGitPushPolicy,
			code:          permissions.ErrorCodeDenied,
		},
		{
			name:          "approval",
			commandPolicy: askGitPushPolicy,
			code:          permissions.ErrorCodeApprovalRequired,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := nativemocks.RegisterRuntimeWithPermissionPolicy(
				t,
				t.TempDir(),
				test.commandPolicy,
				permissions.Policy{Rules: []permissions.Rule{
					{
						Name:     "allow execution",
						Effects:  []permissions.Effect{permissions.EffectExecution},
						Decision: permissions.DecisionAllow,
					},
				}},
				Definition,
			)
			ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
				Actor: permissions.Actor{
					Kind: permissions.ActorLocalOwner,
				},
				Surface: permissions.SurfaceCLI,
			})

			result, err := registry.Invoke(ctx, tools.Call{
				Name: "run_command", Input: `{"command":"git","args":["push","origin","main"]}`,
			})

			require.NoError(t, err)
			var toolErr tools.Error
			require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
			require.Equal(t, test.code, toolErr.Code)
		})
	}
}

func TestRunCommand_AskPresetRequiresApprovalBeforeOrdinaryExecution(t *testing.T) {
	root := t.TempDir()
	registry := nativemocks.RegisterRuntimeWithPermissionPolicy(
		t,
		root,
		guardrails.CommandPolicy{},
		permissions.Policy{Preset: permissions.PresetAskForApproval},
		Definition,
	)
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{
			Kind: permissions.ActorLocalOwner,
		},
		Surface: permissions.SurfaceCLI,
	})

	result, err := registry.Invoke(ctx, tools.Call{
		Name: "run_command", Input: `{"command":"printf","args":["hello"]}`,
	})

	require.NoError(t, err)
	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, permissions.ErrorCodeApprovalRequired, toolErr.Code)
}

func TestRunCommand_ApprovePresetRunsOrdinaryCommandsButPreservesUnsafeApproval(t *testing.T) {
	tests := []struct {
		name          string
		commandPolicy guardrails.CommandPolicy
		wantCode      string
	}{
		{name: "ordinary command"},
		{
			name:          "unsafe command",
			commandPolicy: askGitPushPolicy,
			wantCode:      permissions.ErrorCodeApprovalRequired,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			registry := nativemocks.RegisterRuntimeWithPermissionPolicy(
				t,
				root,
				test.commandPolicy,
				permissions.Policy{Preset: permissions.PresetApproveForMe},
				Definition,
			)
			ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
				Actor: permissions.Actor{
					Kind: permissions.ActorLocalOwner,
				},
				Surface: permissions.SurfaceCLI,
			})
			input := `{"command":"printf","args":["hello"]}`
			if test.wantCode != "" {
				input = `{"command":"git","args":["push","origin","main"]}`
			}

			result, err := registry.Invoke(ctx, tools.Call{Name: "run_command", Input: input})

			require.NoError(t, err)
			if test.wantCode == "" {
				require.Empty(t, result.Error)
				var payload runCommandPayload
				require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
				require.Equal(t, "hello", payload.Stdout)
				return
			}

			var toolErr tools.Error
			require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
			require.Equal(t, test.wantCode, toolErr.Code)
		})
	}
}

func TestRunCommand_FullAccessDoesNotBypassCommandGuardrails(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	registry := nativemocks.RegisterRuntimeWithPermissionPolicy(
		t,
		root,
		denyGitPushPolicy,
		permissions.Policy{Preset: permissions.PresetFullAccess},
		Definition,
	)

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name: "run_command",
		Input: `{"command":"git","args":["push","origin","main"],"cwd":` +
			nativemocks.QuoteJSON(outside) + `}`,
	})

	require.NoError(t, err)
	require.NotEmpty(t, result.Error)
}

func TestRunCommand_ResolvePermissionRequiresExecutionRuntime(t *testing.T) {
	resolver := resolvePermission(nil)

	inputs, err := resolver(
		context.Background(),
		tools.Call{Input: `{"command":"printf","args":["hello"]}`},
	)

	require.Nil(t, inputs)
	require.EqualError(t, err, "execution backend is not configured")
}

func TestRunCommand_ResolvePermissionBuildsCompoundOperationSet(t *testing.T) {
	root := t.TempDir()
	runtime := nativemocks.NewRuntime(root, guardrails.CommandPolicy{})

	inputs, err := resolvePermission(runtime)(context.Background(), tools.Call{
		Input: `{"mode":"posix_shell","command":"printf ok > out.txt; git status"}`,
	})

	require.NoError(t, err)
	require.Len(t, inputs, 5)
	require.Equal(t, permissions.ResourceFile, inputs[0].Operation.Resource)
	require.Equal(t, permissions.ResourceProcess, inputs[1].Operation.Resource)
	require.Equal(t, commandplan.ModePOSIXShell, inputs[1].Operation.Command.Mode)
	require.Equal(t, "printf", inputs[1].Operation.Command.Executable)
	require.Equal(t, permissions.ResourceProcess, inputs[2].Operation.Resource)
	require.Equal(t, "git", inputs[2].Operation.Command.Executable)
	require.Equal(t, permissions.ResourceFile, inputs[3].Operation.Resource)
	require.Equal(t, permissions.ActionCreate, inputs[3].Operation.Action)
	require.Equal(t, filepath.Join(root, "out.txt"), inputs[3].Operation.Target)
	require.Equal(t, permissions.TargetScopeWorkspace, inputs[3].Operation.TargetScope)
	require.Equal(t, permissions.ResourceFile, inputs[4].Operation.Resource)
	require.Equal(t, permissions.ActionUpdate, inputs[4].Operation.Action)
	require.Equal(t, filepath.Join(root, "out.txt"), inputs[4].Operation.Target)
	require.Equal(t, permissions.TargetScopeWorkspace, inputs[4].Operation.TargetScope)
}

func TestRunCommand_CompoundDenialPreventsAnyExecution(t *testing.T) {
	policy := permissions.Policy{
		Default: permissions.DecisionDeny,
		Rules: []permissions.Rule{
			{
				Name:      "allow cwd",
				Resources: []permissions.Resource{permissions.ResourceFile},
				Actions: []permissions.Action{
					permissions.ActionRead,
				},
				Decision: permissions.DecisionAllow,
			},
			{
				Name: "allow printf", Resources: []permissions.Resource{permissions.ResourceProcess},
				Actions: []permissions.Action{permissions.ActionExecute},
				Commands: []commandplan.Selector{{
					Executable: "printf", Modes: []commandplan.Mode{commandplan.ModePOSIXShell},
				}},
				Decision: permissions.DecisionAllow,
			},
			{
				Name: "deny git push", Resources: []permissions.Resource{permissions.ResourceProcess},
				Actions: []permissions.Action{permissions.ActionExecute},
				Commands: []commandplan.Selector{{
					Executable: "git", ExactArguments: []string{"push"},
					Modes: []commandplan.Mode{commandplan.ModePOSIXShell},
				}},
				Decision: permissions.DecisionDeny,
			},
		},
	}
	registry := nativemocks.RegisterRuntimeWithPermissionPolicy(
		t, t.TempDir(), guardrails.CommandPolicy{}, policy, Definition,
	)
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{
			Kind: permissions.ActorLocalOwner,
		},
		Surface: permissions.SurfaceCLI,
	})

	result, err := registry.Invoke(ctx, tools.Call{
		Name: "run_command", Input: `{"mode":"posix_shell","command":"printf ok; git push"}`,
	})

	require.NoError(t, err)
	require.Contains(t, result.Error, permissions.ErrorCodeDenied)
}

func TestRunCommand_IncompletePlanIsDeniedOnUnattendedSurface(t *testing.T) {
	registry := nativemocks.RegisterRuntimeWithPermissionPolicy(
		t,
		t.TempDir(),
		guardrails.CommandPolicy{},
		permissions.Policy{
			Preset: permissions.PresetApproveForMe,
			Rules: []permissions.Rule{{
				Name:         "allow automation commands",
				ActorKinds:   []permissions.ActorKind{permissions.ActorAutomation},
				SurfaceKinds: []permissions.SurfaceKind{permissions.SurfaceKindAutomation},
				Decision:     permissions.DecisionAllow,
			}},
		},
		Definition,
	)
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{
			Kind: permissions.ActorAutomation,
			ID:   "job",
		},
		Surface: permissions.SurfaceAutomation,
	})

	result, err := registry.Invoke(ctx, tools.Call{
		Name: "run_command", Input: `{"command":"python3","args":["-c","print('hello')"]}`,
	})

	require.NoError(t, err)
	require.Contains(t, result.Error, permissions.ErrorCodeDenied)
	require.Contains(t, result.Error, "incomplete command plans require an interactive local owner")
}

func TestRunCommand_ToolRejectsOutsideWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	registry := nativemocks.RegisterRuntime(t, root, guardrails.CommandPolicy{}, Definition)

	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{
			Kind: permissions.ActorLocalOwner,
		},
		Surface: permissions.SurfaceCLI,
	})
	result, err := registry.Invoke(ctx, tools.Call{
		Name:  "run_command",
		Input: `{"command":"printf","args":["hello"],"cwd":` + nativemocks.QuoteJSON(outside) + `}`,
	})

	require.NoError(t, err)
	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, "path_outside_roots", toolErr.Code)
}

func TestRunCommand_ToolTimesOut(t *testing.T) {
	root := t.TempDir()
	registry := nativemocks.RegisterRuntime(t, root, guardrails.CommandPolicy{}, Definition)

	result, err := registry.Invoke(
		context.Background(),
		tools.Call{
			Name:  "run_command",
			Input: `{"command":"sleep","args":["2"],"timeout_seconds":1}`,
		},
	)

	require.NoError(t, err)
	var payload runCommandPayload
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Equal(t, -1, payload.ExitCode)
	require.True(t, payload.TimedOut)
	require.Equal(t, 1, payload.TimeoutSeconds)
	require.GreaterOrEqual(t, payload.ElapsedSeconds, 0.0)
	require.Equal(t, 0.0, payload.RemainingSeconds)
}

func TestRunCommand_ToolPassesEnvironmentVariables(t *testing.T) {
	root := t.TempDir()
	registry := nativemocks.RegisterRuntimeWithPermissionPolicy(
		t,
		root,
		guardrails.CommandPolicy{},
		permissions.Policy{Preset: permissions.PresetFullAccess},
		Definition,
	)

	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{
			Kind: permissions.ActorLocalOwner,
		},
		Surface: permissions.SurfaceCLI,
	})
	result, err := registry.Invoke(ctx, tools.Call{
		Name:  "run_command",
		Input: `{"mode":"posix_shell","command":"printf %s \"$MORPH_TEST_VAR\"","env":[{"name":"MORPH_TEST_VAR","value":"visible"}]}`,
	})

	require.NoError(t, err)
	var payload runCommandPayload
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Equal(t, 0, payload.ExitCode)
	require.Equal(t, "visible", payload.Stdout)
	require.False(t, payload.TimedOut)
	require.Equal(t, 30, payload.TimeoutSeconds)
	require.GreaterOrEqual(t, payload.ElapsedSeconds, 0.0)
	require.GreaterOrEqual(t, payload.RemainingSeconds, 0.0)
}

func TestRunCommand_ToolRejectsDuplicateEnvironmentVariables(t *testing.T) {
	root := t.TempDir()
	registry := nativemocks.RegisterRuntime(t, root, guardrails.CommandPolicy{}, Definition)

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name:  "run_command",
		Input: `{"command":"printf","env":[{"name":"KEY","value":"one"},{"name":" KEY ","value":"two"}]}`,
	})

	require.NoError(t, err)
	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, "invalid_input", toolErr.Code)
	require.Equal(t, "command environment contains duplicate variable names", toolErr.Message)
}

func TestRunCommand_ToolReturnsNonZeroExitCode(t *testing.T) {
	root := t.TempDir()
	registry := nativemocks.RegisterRuntime(t, root, guardrails.CommandPolicy{}, Definition)

	result, err := registry.Invoke(
		context.Background(),
		tools.Call{
			Name:  "run_command",
			Input: `{"command":"false"}`,
		},
	)

	require.NoError(t, err)
	var payload runCommandPayload
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Equal(t, 1, payload.ExitCode)
	require.False(t, payload.TimedOut)
	require.Equal(t, 30, payload.TimeoutSeconds)
	require.GreaterOrEqual(t, payload.ElapsedSeconds, 0.0)
	require.GreaterOrEqual(t, payload.RemainingSeconds, 0.0)
}

func TestRunCommand_ToolReportsClampedTimeout(t *testing.T) {
	root := t.TempDir()
	registry := nativemocks.RegisterRuntime(t, root, guardrails.CommandPolicy{}, Definition)

	result, err := registry.Invoke(
		context.Background(),
		tools.Call{
			Name:  "run_command",
			Input: `{"command":"printf","args":["hello"],"timeout_seconds":999}`,
		},
	)

	require.NoError(t, err)
	var payload runCommandPayload
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Equal(t, 120, payload.TimeoutSeconds)
	require.False(t, payload.TimedOut)
	require.GreaterOrEqual(t, payload.ElapsedSeconds, 0.0)
	require.GreaterOrEqual(t, payload.RemainingSeconds, 0.0)
	require.LessOrEqual(t, payload.RemainingSeconds, float64(payload.TimeoutSeconds))
}

func TestRunCommand_ToolSupportsContextCancellation(t *testing.T) {
	root := t.TempDir()
	registry := nativemocks.RegisterRuntime(t, root, guardrails.CommandPolicy{}, Definition)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := registry.Invoke(
		ctx,
		tools.Call{
			Name:  "run_command",
			Input: `{"command":"printf","args":["hello"]}`,
		},
	)

	require.NoError(t, err)
	require.Empty(t, result.Output)
	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, "command_failed", toolErr.Code)
	require.Contains(t, toolErr.Message, "context canceled")
}

func TestRunCommand_HandlerValidatesInputAndWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	handler := Definition(nativemocks.NewRuntime(root, guardrails.CommandPolicy{})).Handler

	result, err := handler.Invoke(context.Background(), tools.Call{Input: `{"command":`})
	require.NoError(t, err)
	require.Contains(t, result.Error, `"code":"invalid_input"`)

	result, err = handler.Invoke(context.Background(), tools.Call{Input: `{"command":" "}`})
	require.NoError(t, err)
	require.Contains(t, result.Error, "command is required")

	result, err = handler.Invoke(context.Background(), tools.Call{
		Input: `{"command":"printf","cwd":` + nativemocks.QuoteJSON(t.TempDir()) + `}`,
	})
	require.NoError(t, err)
	require.Contains(t, result.Error, `"code":"path_outside_roots"`)
}

func TestRunCommand_HandlerAppliesCommandPolicy(t *testing.T) {
	tests := []struct {
		name    string
		policy  guardrails.CommandPolicy
		command string
		args    []string
		message string
	}{
		{
			name:    "denied",
			policy:  denyGitPushPolicy,
			command: "git",
			args:    []string{"push"},
			message: "command_denied",
		},
		{
			name:    "configured approval",
			policy:  askGitPushPolicy,
			command: "git",
			args:    []string{"push"},
			message: "Command matches approval rule: command-selector:",
		},
		{
			name:    "built-in approval",
			command: "rm",
			args:    []string{"-rf", "/"},
			message: "dangerous destructive command",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := nativemocks.NewRuntime(t.TempDir(), test.policy)
			input, err := json.Marshal(input{Command: test.command, Args: test.args})
			require.NoError(t, err)

			result, err := Definition(
				runtime,
			).Handler.Invoke(
				context.Background(),
				tools.Call{Input: string(input)},
			)

			require.NoError(t, err)
			require.Contains(t, result.Error, test.message)
		})
	}
}

func TestRunCommand_HandlerReturnsStartError(t *testing.T) {
	root := t.TempDir()
	handler := Definition(nativemocks.NewRuntime(root, guardrails.CommandPolicy{})).Handler

	result, err := handler.Invoke(context.Background(), tools.Call{
		Input: `{"command":"definitely-not-a-real-command","args":["arg"]}`,
	})
	require.NoError(t, err)
	require.Contains(t, result.Error, `"code":"invalid_input"`)
}

func TestRunCommand_HandlerReturnsCancellationAfterStart(t *testing.T) {
	root := t.TempDir()
	handler := Definition(nativemocks.NewRuntime(root, guardrails.CommandPolicy{})).Handler
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	result, err := handler.Invoke(ctx, tools.Call{
		Input: `{"command":"sleep","args":["5"]}`,
	})

	require.NoError(t, err)
	require.Contains(t, result.Error, "context canceled")
}

func TestBuildRunCommandOutput_ClampsNegativeRemainingTime(t *testing.T) {
	output := buildRunCommandOutput(0, "", "", false, 1, 2)

	require.Equal(t, 0.0, output["remaining_seconds"])
}

func TestBuildCommand_UsesDirectExecutionWhenArgsAreProvided(t *testing.T) {
	plan, err := commandplan.Analyze(context.Background(), commandplan.Request{
		Mode: commandplan.ModeDirect, Command: "git", Args: []string{"status", "--short"},
	})
	require.NoError(t, err)
	cmd, err := plan.NewCommand(context.Background())
	require.NoError(t, err)
	require.Equal(t, plan.Invocations[0].ResolvedPath, cmd.Path)
	require.Equal(t, []string{plan.Invocations[0].ResolvedPath, "status", "--short"}, cmd.Args)
}

func TestBuildCommand_UsesPOSIXShellOnlyWhenExplicit(t *testing.T) {
	plan, err := commandplan.Analyze(context.Background(), commandplan.Request{
		Mode: commandplan.ModePOSIXShell, Command: "echo hello", ShellPath: "/bin/sh",
	})
	require.NoError(t, err)
	cmd, err := plan.NewCommand(context.Background())
	require.NoError(t, err)
	require.Equal(t, "/bin/sh", cmd.Path)
	require.Equal(t, []string{"/bin/sh", "-c", "echo hello"}, cmd.Args)
}

func TestRunCommand_DefaultModeSupportsZeroArgumentDirectCommand(t *testing.T) {
	registry := nativemocks.RegisterRuntime(t, t.TempDir(), guardrails.CommandPolicy{}, Definition)
	result, err := registry.Invoke(context.Background(), tools.Call{
		Name: "run_command", Input: `{"command":"true"}`,
	})
	require.NoError(t, err)
	require.Empty(t, result.Error)
}

func TestRunCommand_ToolKillsShellChildrenOnTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process group assertions are unix-only")
	}

	root := t.TempDir()
	registry := nativemocks.RegisterRuntimeWithPermissionPolicy(
		t,
		root,
		guardrails.CommandPolicy{},
		permissions.Policy{Preset: permissions.PresetFullAccess},
		Definition,
	)

	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{
			Kind: permissions.ActorLocalOwner,
		},
		Surface: permissions.SurfaceCLI,
	})
	result, err := registry.Invoke(ctx, tools.Call{
		Name:  "run_command",
		Input: `{"mode":"posix_shell","command":"sleep 30 & child=$!; echo $child > child.pid; wait","timeout_seconds":1}`,
	})

	require.NoError(t, err)

	var payload struct {
		ExitCode int  `json:"exit_code"`
		TimedOut bool `json:"timed_out"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
	require.Equal(t, -1, payload.ExitCode)
	require.True(t, payload.TimedOut)

	rawPID, readErr := os.ReadFile(filepath.Join(root, "child.pid"))
	require.NoError(t, readErr)
	childPID, parseErr := strconv.Atoi(bytesTrimSpace(rawPID))
	require.NoError(t, parseErr)

	require.Eventually(t, func() bool {
		err := syscall.Kill(childPID, 0)
		return errors.Is(err, syscall.ESRCH)
	}, 3*time.Second, 50*time.Millisecond)
}

func bytesTrimSpace(value []byte) string {
	start := 0
	for start < len(value) &&
		(value[start] == ' ' ||
			value[start] == '\n' ||
			value[start] == '\t' ||
			value[start] == '\r') {
		start++
	}
	end := len(value)
	for end > start &&
		(value[end-1] == ' ' ||
			value[end-1] == '\n' ||
			value[end-1] == '\t' ||
			value[end-1] == '\r') {
		end--
	}

	return string(value[start:end])
}
