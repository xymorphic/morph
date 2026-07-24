package common

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"

	commandplan "github.com/wandxy/morph/internal/command"
	envtypes "github.com/wandxy/morph/internal/environment/types"
	"github.com/wandxy/morph/internal/guardrails"
	"github.com/wandxy/morph/internal/permissions"
)

func CommandErrorCode(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "command_failed"
	}
	return "invalid_input"
}

func AnalyzeCommand(
	ctx context.Context,
	runtime envtypes.Runtime,
	mode commandplan.Mode,
	command string,
	arguments []string,
	cwd string,
	environment map[string]string,
) (commandplan.Plan, error) {
	policy := FilesystemPolicyFromRuntime(runtime)
	workspaceRoot := ""
	if len(policy.Roots) > 0 {
		workspaceRoot = policy.Roots[0]
	}
	shell := ""
	var identityKey []byte
	if runtime != nil {
		shell = runtime.CommandShell()
		identityKey = runtime.CommandIdentityKey()
	}

	return commandplan.Analyze(ctx, commandplan.Request{
		Mode:          mode,
		Command:       command,
		Args:          slices.Clone(arguments),
		CWD:           cwd,
		WorkspaceRoot: workspaceRoot,
		Environment:   cloneStringMap(environment),
		IdentityKey:   identityKey,
		ShellPath:     shell,
	})
}

func CommandPermissionInputs(
	ctx context.Context,
	runtime envtypes.Runtime,
	plan commandplan.Plan,
	action permissions.Action,
	effects []permissions.Effect,
) []permissions.EvaluationInput {
	inputs := make([]permissions.EvaluationInput, 0, len(plan.Invocations)+len(plan.Redirects)+2)
	cwdTarget, cwdScope := ResolveFilesystemPermissionTarget(FilesystemPolicyFromRuntime(runtime), plan.CWD)
	if cwdScope == permissions.TargetScopeWorkspace {
		cwdTarget = plan.CWDIdentity
	}
	inputs = append(inputs, permissions.EvaluationInput{Operation: permissions.Operation{
		Resource:    permissions.ResourceFile,
		Action:      permissions.ActionRead,
		Effects:     []permissions.Effect{permissions.EffectRead},
		Target:      cwdTarget,
		TargetScope: cwdScope,
	}})
	for _, invocation := range plan.Invocations {
		invocationEffects := slices.Clone(effects)
		if invocation.Indirect {
			invocationEffects = append(invocationEffects, permissions.EffectIndirectExecution)
		}
		target := plan.Target(invocation)
		inputs = append(inputs, permissions.EvaluationInput{Operation: permissions.Operation{
			Resource: permissions.ResourceProcess,
			Action:   action,
			Effects:  invocationEffects,
			Command:  &target,
		}})
	}

	for _, redirect := range plan.Redirects {
		if !redirect.Static || strings.TrimSpace(redirect.Path) == "" {
			continue
		}
		path := redirect.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(plan.CWD, path)
		}
		if filepath.ToSlash(filepath.Clean(path)) == "/dev/null" {
			continue
		}
		target, targetScope := ResolveFilesystemPermissionTarget(FilesystemPolicyFromRuntime(runtime), path)
		redirectAction, redirectEffects := redirectPermission(redirect.Action)
		inputs = append(inputs, permissions.EvaluationInput{Operation: permissions.Operation{
			Resource:    permissions.ResourceFile,
			Action:      redirectAction,
			Effects:     redirectEffects,
			Target:      target,
			TargetScope: targetScope,
		}})
	}

	if plan.DebuggerAttach {
		inputs = append(inputs, permissions.EvaluationInput{
			Operation: permissions.Operation{
				Resource: permissions.ResourceBrowser,
				Action:   permissions.ActionConnect,
				Effects: []permissions.Effect{
					permissions.EffectExecution,
					permissions.EffectExternalSystem,
				},
				Target: "managed-browser-debugger",
			},
			ApprovalReason: "This command attempts to attach to Morph's managed browser.",
		})
	}

	applyCommandGuardrail(ctx, runtime, plan, inputs)
	return inputs
}

func CheckCommandPlan(
	ctx context.Context,
	runtime envtypes.Runtime,
	plan commandplan.Plan,
	action permissions.Action,
	effects []permissions.Effect,
) (string, string) {
	if permissions.HasFullAccess(ctx) {
		return "", ""
	}
	for _, input := range CommandPermissionInputs(ctx, runtime, plan, action, effects) {
		if input.HardDenyReason != "" {
			return "command_denied", input.HardDenyReason
		}
		if input.ApprovalReason != "" && !permissions.IsOperationAuthorized(ctx, input.Operation) {
			return "approval_required", input.ApprovalReason
		}
	}
	return "", ""
}

func applyCommandGuardrail(
	ctx context.Context,
	runtime envtypes.Runtime,
	plan commandplan.Plan,
	inputs []permissions.EvaluationInput,
) {
	if len(inputs) == 0 {
		return
	}
	index := slices.IndexFunc(inputs, func(input permissions.EvaluationInput) bool {
		return input.Operation.Resource == permissions.ResourceProcess
	})
	if index < 0 {
		return
	}
	if !plan.Complete && !isInteractiveCommandApproval(ctx) {
		inputs[index].HardDenyReason = "incomplete command plans require an interactive local owner"
		return
	}
	policy := guardrails.CommandPolicy{}
	if runtime != nil {
		policy = runtime.CommandPolicy()
	}
	evaluation := guardrails.EvaluateCommandPlan(policy, plan)
	switch evaluation.Decision {
	case guardrails.CommandDenied:
		inputs[index].HardDenyReason = evaluation.Reason
	case guardrails.CommandApprovalRequired:
		inputs[index].ApprovalReason = evaluation.Reason
		if evaluation.Rule != "" {
			inputs[index].ApprovalReason = "Command matches approval rule: " + evaluation.Rule
		}
	}
}

func isInteractiveCommandApproval(ctx context.Context) bool {
	authorization, ok := permissions.FromContext(ctx)
	return ok &&
		authorization.Actor.Kind == permissions.ActorLocalOwner &&
		(authorization.Surface == permissions.SurfaceCLI ||
			authorization.Surface == permissions.SurfaceTUI)
}

func redirectPermission(action commandplan.RedirectAction) (permissions.Action, []permissions.Effect) {
	switch action {
	case commandplan.RedirectRead:
		return permissions.ActionRead, []permissions.Effect{permissions.EffectRead}
	case commandplan.RedirectCreate:
		return permissions.ActionCreate, []permissions.Effect{permissions.EffectWrite}
	default:
		return permissions.ActionUpdate, []permissions.Effect{permissions.EffectWrite}
	}
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
