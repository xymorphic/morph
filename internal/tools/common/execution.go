package common

import (
	"context"
	"errors"
	"slices"

	envtypes "github.com/wandxy/morph/internal/environment/types"
	"github.com/wandxy/morph/internal/execution"
	"github.com/wandxy/morph/internal/permissions"
	"github.com/wandxy/morph/internal/tools"
)

func PrepareExecutionSpec(
	ctx context.Context,
	runtime envtypes.Runtime,
	operation execution.Operation,
) (execution.Spec, error) {
	if runtime == nil || runtime.ExecutionService() == nil {
		return execution.Spec{}, errors.New("execution backend is not configured")
	}
	return runtime.PrepareExecutionSpec(ctx, operation)
}

func PrepareExecutionPath(
	ctx context.Context,
	runtime envtypes.Runtime,
	path string,
	action execution.FilesystemAction,
) (execution.PreparedPath, error) {
	if runtime == nil || runtime.ExecutionService() == nil {
		return execution.PreparedPath{}, errors.New("execution backend is not configured")
	}
	return runtime.PrepareExecutionPath(ctx, path, action)
}

func ExecutionService(runtime envtypes.Runtime) (execution.Service, error) {
	if runtime == nil || runtime.ExecutionService() == nil {
		return nil, errors.New("execution backend is not configured")
	}
	return runtime.ExecutionService(), nil
}

func GetPreparedExecutionSpec(ctx context.Context, call tools.Call) (execution.Spec, bool) {
	prepared, ok := tools.GetPreparedCall(ctx, call)
	if !ok {
		return execution.Spec{}, false
	}
	spec, ok := prepared.(execution.Spec)
	return spec, ok
}

func RequireExecutionSpec(
	ctx context.Context,
	call tools.Call,
	prepare tools.PermissionPreparer,
) (execution.Spec, error) {
	if spec, ok := GetPreparedExecutionSpec(ctx, call); ok {
		return spec, nil
	}
	if prepare == nil {
		return execution.Spec{}, errors.New("execution specification is required")
	}
	preparation, err := prepare(ctx, call)
	if err != nil {
		return execution.Spec{}, err
	}
	spec, ok := preparation.Prepared.(execution.Spec)
	if !ok {
		return execution.Spec{}, errors.New("prepared execution specification is invalid")
	}
	return spec, nil
}

func PrepareFilesystemExecution(
	ctx context.Context,
	runtime envtypes.Runtime,
	operation permissions.Operation,
	rawPath string,
	filesystem execution.FilesystemOperation,
) (tools.PermissionPreparation, error) {
	preparation := tools.PermissionPreparation{
		Inputs: []permissions.EvaluationInput{{Operation: operation}},
	}
	path, err := PrepareExecutionPath(ctx, runtime, rawPath, filesystem.Action)
	if err != nil {
		return tools.PermissionPreparation{}, err
	}
	filesystem.Path = path
	spec, err := PrepareExecutionSpec(ctx, runtime, execution.Operation{
		Kind:       execution.OperationFilesystem,
		Filesystem: &filesystem,
	})
	if err != nil {
		return tools.PermissionPreparation{}, err
	}
	preparation.Inputs = append(preparation.Inputs, ExecutionPermissionInputs(spec)...)
	preparation.Prepared = spec
	return preparation, nil
}

func ExecutionPermissionInputs(spec execution.Spec) []permissions.EvaluationInput {
	exposure := spec.Exposure()
	if exposure.Backend() != execution.BackendDocker {
		return nil
	}
	inputs := make([]permissions.EvaluationInput, 0, len(exposure.Mounts())+4)
	inputs = append(inputs, permissions.EvaluationInput{Operation: permissions.Operation{
		Resource: permissions.ResourceProcess,
		Action:   permissions.ActionExecute,
		Effects:  []permissions.Effect{permissions.EffectExecution},
		Target:   "execution-generation:" + exposure.SecurityGeneration() + ":" + exposure.ImageDigest(),
	}})
	for _, mount := range exposure.Mounts() {
		effects := []permissions.Effect{permissions.EffectRead}
		action := permissions.ActionRead
		if mount.Mode == execution.MountReadWrite {
			action = permissions.ActionUpdate
			effects = append(effects, permissions.EffectWrite)
		}
		if exposure.Scope() == execution.ScopeShared || mount.Target == "/workspace" {
			effects = append(effects, permissions.EffectSharedState)
		}
		inputs = append(inputs, permissions.EvaluationInput{
			Operation: permissions.Operation{
				Resource:    permissions.ResourceFile,
				Action:      action,
				Effects:     slices.Compact(effects),
				Target:      mount.SourceIdentity,
				TargetScope: permissions.TargetScopeExternal,
			},
			ApprovalReason: "Docker execution can access the configured host mount " + mount.Target + ".",
		})
	}
	if exposure.Network() == execution.NetworkBridge {
		inputs = append(inputs, permissions.EvaluationInput{
			Operation: permissions.Operation{
				Resource: permissions.ResourceNetwork,
				Action:   permissions.ActionConnect,
				Effects: []permissions.Effect{
					permissions.EffectNetwork,
					permissions.EffectExternalSystem,
				},
				Target: "unrestricted-outbound",
			},
			ApprovalReason: "Docker bridge access may reach public, host, private, link-local, or metadata services.",
		})
	}
	for _, name := range exposure.SecretReferences() {
		inputs = append(inputs, permissions.EvaluationInput{
			Operation: permissions.Operation{
				Resource: permissions.ResourceProcess,
				Action:   permissions.ActionExecute,
				Effects:  []permissions.Effect{permissions.EffectCredentialBearing},
				Target:   "secret:" + name,
			},
			ApprovalReason: "Execution receives configured secret reference " + name + ".",
		})
	}
	if exposure.Scope() == execution.ScopeShared {
		inputs = append(inputs, permissions.EvaluationInput{
			Operation: permissions.Operation{
				Resource: permissions.ResourceProcess,
				Action:   permissions.ActionManage,
				Effects:  []permissions.Effect{permissions.EffectSharedState},
				Target:   "profile-shared-execution",
			},
			ApprovalReason: "Shared execution exposes files, installed state, ambient processes, and serialized foreground access across profile sessions.",
		})
	}
	return inputs
}
