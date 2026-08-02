package patch

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
	"github.com/xymorphic/morph/pkg/logutils"
	"github.com/xymorphic/morph/pkg/str"

	envtypes "github.com/xymorphic/morph/internal/environment/types"
	"github.com/xymorphic/morph/internal/execution"
	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/internal/tools"
	"github.com/xymorphic/morph/internal/tools/common"
)

var log = logutils.Module("tool.patch")

type input struct {
	Patch string `json:"patch"`
	Strip int    `json:"strip"`
}

type patchTarget struct {
	newPath string
	isNew   bool
}

// Definition returns the model-visible tool definition.
func Definition(runtime envtypes.Runtime) tools.Definition {
	return tools.Definition{
		Name:          "patch",
		Description:   "Apply a unified diff patch to absolute or workspace-relative paths, subject to the current permission policy.",
		Groups:        []string{"core"},
		Requires:      tools.Capabilities{Filesystem: true},
		SemanticIndex: tools.SkipSemanticIndex(),
		Permission: permissions.Operation{
			Resource: permissions.ResourceFile,
			Action:   permissions.ActionUpdate,
			Effects:  []permissions.Effect{permissions.EffectWrite},
		},
		PreparePermission: preparePermissionForRuntime(runtime),
		ResolvePermission: resolvePermissionForRuntime(runtime),
		InputSchema: common.ObjectSchema(map[string]any{
			"patch": common.StringSchema("Unified diff patch content to apply."),
			"strip": common.IntegerSchema(
				"Number of leading path components to strip from file paths, similar to git apply -p.",
			),
		}, "patch"),
		Handler: tools.HandlerFunc(
			func(ctx context.Context, call tools.Call) (tools.Result, error) {
				var req input
				if result := common.DecodeInput(call, &req); result.Error != "" {
					return result, nil
				}
				patchValue := str.String(req.Patch)
				if patchValue.Trim() == "" {
					return common.ToolError("invalid_input", "patch is required"), nil
				}
				patchValue2 := str.String(req.Patch)
				log.Info().
					Str("tool", "patch").
					Str("phase", "start").
					Int("patch_chars", len([]rune(patchValue2.Trim()))).
					Int("strip", req.Strip).
					Msg("patch tool started")

				log.Debug().
					Str("tool", "patch").
					Str("phase", "execute").
					Msg("patch application started")
				spec, specErr := common.RequireExecutionSpec(
					ctx,
					call,
					preparePermissionForRuntime(runtime),
				)
				if specErr != nil {
					return common.FileError(specErr), nil
				}
				service, serviceErr := common.ExecutionService(runtime)
				if serviceErr != nil {
					return common.ToolError("tool_error", serviceErr.Error()), nil
				}
				if _, patchErr := common.ObserveExecution(ctx, spec, func() (execution.FileInfo, error) {
					return service.PatchFile(ctx, spec)
				}); patchErr != nil {
					if errors.Is(patchErr, execution.ErrPatchConflict) {
						return common.ToolError("conflict", patchErr.Error()), nil
					}
					return common.FileError(patchErr), nil
				}
				files, _, parseErr := gitdiff.Parse(strings.NewReader(req.Patch))
				if parseErr != nil {
					return common.ToolError("internal_error", parseErr.Error()), nil
				}
				applied := make([]string, 0, len(files))
				created := make([]string, 0)
				for _, file := range files {
					target, targetErr := getPatchTarget(file, req.Strip)
					if targetErr != nil {
						return common.ToolError("internal_error", targetErr.Error()), nil
					}
					name := filepath.ToSlash(target.newPath)
					applied = append(applied, name)
					if target.isNew {
						created = append(created, name)
					}
				}
				sort.Strings(applied)
				sort.Strings(created)
				return common.EncodeOutput(
					map[string]any{"applied_files": applied, "created_files": created},
				)
			},
		),
	}
}

func resolvePermissionForRuntime(runtime envtypes.Runtime) tools.PermissionResolver {
	return func(ctx context.Context, call tools.Call) ([]permissions.EvaluationInput, error) {
		preparation, err := preparePermissionForRuntime(runtime)(ctx, call)
		return preparation.Inputs, err
	}
}

func preparePermissionForRuntime(runtime envtypes.Runtime) tools.PermissionPreparer {
	return func(ctx context.Context, call tools.Call) (tools.PermissionPreparation, error) {
		var req input
		if err := json.Unmarshal([]byte(call.Input), &req); err != nil {
			return tools.PermissionPreparation{}, tools.NewPermissionResolutionError(
				"invalid_input",
				"invalid tool input",
			)
		}
		if str.String(req.Patch).Trim() == "" {
			return tools.PermissionPreparation{}, tools.NewPermissionResolutionError(
				"invalid_input",
				"patch is required",
			)
		}

		files, _, err := gitdiff.Parse(strings.NewReader(req.Patch))
		if err != nil {
			return tools.PermissionPreparation{}, tools.NewPermissionResolutionError(
				"internal_error",
				err.Error(),
			)
		}
		if len(files) == 0 {
			return tools.PermissionPreparation{}, tools.NewPermissionResolutionError(
				"invalid_input",
				"invalid patch",
			)
		}

		inputs := make([]permissions.EvaluationInput, 0, len(files))
		paths := make([]execution.PreparedPath, 0, len(files))
		creates := make([]bool, 0, len(files))
		for _, file := range files {
			target, err := getPatchTarget(file, req.Strip)
			if err != nil {
				return tools.PermissionPreparation{}, tools.NewPermissionResolutionError(
					"invalid_input",
					err.Error(),
				)
			}
			action := permissions.ActionUpdate
			if target.isNew {
				action = permissions.ActionCreate
			}
			permissionTarget, targetScope := common.ResolveFilesystemPermissionTarget(
				common.FilesystemPolicyFromRuntime(runtime),
				target.newPath,
			)
			inputs = append(inputs, permissions.EvaluationInput{Operation: permissions.Operation{
				Resource:    permissions.ResourceFile,
				Action:      action,
				Effects:     []permissions.Effect{permissions.EffectWrite},
				Target:      permissionTarget,
				TargetScope: targetScope,
			}})
			path, pathErr := common.PrepareExecutionPath(
				ctx,
				runtime,
				target.newPath,
				execution.FilesystemPatch,
			)
			if pathErr != nil {
				return tools.PermissionPreparation{}, common.NewFilePermissionResolutionError(
					pathErr,
				)
			}
			paths = append(paths, path)
			creates = append(creates, target.isNew)
		}
		sort.Slice(inputs, func(i, j int) bool {
			return inputs[i].Operation.Target < inputs[j].Operation.Target
		})

		operation := execution.FilesystemOperation{
			Action:  execution.FilesystemPatch,
			Path:    paths[0],
			Paths:   paths,
			Creates: creates,
			Data:    []byte(req.Patch),
			Strip:   req.Strip,
		}
		spec, specErr := common.PrepareExecutionSpec(ctx, runtime, execution.Operation{
			Kind:       execution.OperationFilesystem,
			Filesystem: &operation,
		})
		if specErr != nil {
			return tools.PermissionPreparation{}, tools.NewPermissionResolutionError(
				"execution_unavailable",
				specErr.Error(),
			)
		}
		preparation := tools.PermissionPreparation{
			Inputs:   inputs,
			Prepared: spec,
		}
		preparation.Inputs = append(preparation.Inputs, common.ExecutionPermissionInputs(spec)...)
		return preparation, nil
	}
}

func getPatchTarget(file *gitdiff.File, strip int) (patchTarget, error) {
	oldPath := stripPath(file.OldName, strip)
	newPath := stripPath(file.NewName, strip)
	if file.IsBinary {
		return patchTarget{}, errors.New("binary patches are not supported")
	}
	if file.IsDelete || newPath == "/dev/null" {
		return patchTarget{}, errors.New("delete patches are not supported")
	}
	isNew := file.IsNew || oldPath == "/dev/null"
	if file.IsRename || !isNew && oldPath != newPath {
		return patchTarget{}, errors.New("rename patches are not supported")
	}

	return patchTarget{newPath: newPath, isNew: isNew}, nil
}

func stripPath(path string, strip int) string {
	pathValue := str.String(path)
	path = pathValue.Trim()
	path = strings.TrimPrefix(path, "a/")
	path = strings.TrimPrefix(path, "b/")
	if path == "/dev/null" {
		return path
	}

	parts := strings.Split(filepath.ToSlash(path), "/")
	if strip >= len(parts) {
		return parts[len(parts)-1]
	}

	return filepath.FromSlash(strings.Join(parts[strip:], "/"))
}
