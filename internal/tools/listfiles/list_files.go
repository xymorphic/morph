package listfiles

import (
	"context"
	"encoding/json"

	envtypes "github.com/wandxy/morph/internal/environment/types"
	"github.com/wandxy/morph/internal/execution"
	"github.com/wandxy/morph/internal/permissions"
	"github.com/wandxy/morph/internal/tools"
	"github.com/wandxy/morph/internal/tools/common"
)

// Definition returns the model-visible tool definition.
func Definition(runtime envtypes.Runtime) tools.Definition {
	type input struct {
		Path          string `json:"path"`
		Recursive     *bool  `json:"recursive"`
		IncludeHidden bool   `json:"include_hidden"`
		MaxEntries    int    `json:"max_entries"`
	}

	type entry struct {
		Path string `json:"path"`
		Type string `json:"type"`
		Size int64  `json:"size,omitempty"`
	}

	return tools.Definition{
		Name:          "list_files",
		Description:   "List files and directories at an absolute or workspace-relative path, subject to the current permission policy.",
		ParallelSafe:  true,
		Groups:        []string{"core"},
		Requires:      tools.Capabilities{Filesystem: true},
		SemanticIndex: tools.SkipSemanticIndex(),
		Permission: permissions.Operation{
			Resource: permissions.ResourceFile,
			Action:   permissions.ActionList,
			Effects:  []permissions.Effect{permissions.EffectRead},
		},
		PreparePermission: func(
			ctx context.Context,
			call tools.Call,
		) (tools.PermissionPreparation, error) {
			var req input
			if err := json.Unmarshal([]byte(call.Input), &req); err != nil {
				return tools.PermissionPreparation{}, tools.NewPermissionResolutionError(
					"invalid_input",
					"invalid tool input",
				)
			}
			target, targetScope := common.ResolveFilesystemPermissionTarget(
				common.FilesystemPolicyFromRuntime(runtime),
				req.Path,
			)
			operation := permissions.Operation{
				Resource:    permissions.ResourceFile,
				Action:      permissions.ActionList,
				Effects:     []permissions.Effect{permissions.EffectRead},
				Target:      target,
				TargetScope: targetScope,
			}
			recursive := req.Recursive != nil && *req.Recursive
			preparation, err := common.PrepareFilesystemExecution(
				ctx,
				runtime,
				operation,
				req.Path,
				execution.FilesystemOperation{
					Action:        execution.FilesystemList,
					Recursive:     recursive,
					IncludeHidden: req.IncludeHidden,
				},
			)
			if err != nil {
				return tools.PermissionPreparation{}, common.NewFilePermissionResolutionError(err)
			}
			return preparation, nil
		},
		ResolvePermission: func(
			ctx context.Context,
			call tools.Call,
		) ([]permissions.EvaluationInput, error) {
			preparation, err := Definition(runtime).PreparePermission(ctx, call)
			return preparation.Inputs, err
		},
		InputSchema: common.ObjectSchema(map[string]any{
			"path": common.StringSchema(
				"Absolute path or path relative to the configured workspace root. Defaults to the workspace root when omitted.",
			),
			"recursive": common.BooleanSchema(
				"When true, list directory contents recursively. Defaults to false.",
			),
			"include_hidden": common.BooleanSchema(
				"When true, include hidden files and directories in the results.",
			),
			"max_entries": common.IntegerSchema(
				"Maximum number of entries to return. Values outside the supported range are clamped.",
			),
		}, "path", "recursive", "include_hidden", "max_entries"),
		Handler: tools.HandlerFunc(
			func(ctx context.Context, call tools.Call) (tools.Result, error) {
				var req input
				if result := common.DecodeInput(call, &req); result.Error != "" {
					return result, nil
				}

				limit := req.MaxEntries
				if limit <= 0 || limit > common.MaxListEntries {
					limit = common.MaxListEntries
				}
				spec, specErr := common.RequireExecutionSpec(
					ctx,
					call,
					Definition(runtime).PreparePermission,
				)
				if specErr != nil {
					return common.FileError(specErr), nil
				}
				service, serviceErr := common.ExecutionService(runtime)
				if serviceErr != nil {
					return common.ToolError("tool_error", serviceErr.Error()), nil
				}
				listed, listErr := common.ObserveExecution(
					ctx,
					spec,
					func() ([]execution.FileEntry, error) {
						return service.ListFiles(ctx, spec, limit)
					},
				)
				if listErr != nil {
					return common.FileError(listErr), nil
				}
				entries := make([]entry, 0, len(listed))
				for _, item := range listed {
					kind := "file"
					if item.IsDir {
						kind = "dir"
					}
					entries = append(entries, entry{
						Path: item.Path,
						Type: kind,
						Size: item.Size,
					})
				}
				return common.EncodeOutput(map[string]any{
					"root":    spec.Operation().Filesystem.Path.ContainerPath(),
					"path":    common.NormalizedDisplayPath(req.Path),
					"entries": entries,
				})
			},
		),
	}
}
