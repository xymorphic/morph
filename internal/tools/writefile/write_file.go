package writefile

import (
	"context"
	"encoding/json"

	"github.com/xymorphic/morph/pkg/str"

	envtypes "github.com/xymorphic/morph/internal/environment/types"
	"github.com/xymorphic/morph/internal/execution"
	"github.com/xymorphic/morph/internal/guardrails"
	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/internal/tools"
	"github.com/xymorphic/morph/internal/tools/common"
)

type input struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	CreateDirs *bool  `json:"create_dirs"`
}

// Definition returns the model-visible tool definition.
func Definition(runtime envtypes.Runtime) tools.Definition {
	return tools.Definition{
		Name:          "write_file",
		Description:   "Create or overwrite a text file at an absolute or workspace-relative path, subject to the current permission policy.",
		Groups:        []string{"core"},
		Requires:      tools.Capabilities{Filesystem: true},
		SemanticIndex: tools.SkipSemanticIndex(),
		Permission: permissions.Operation{
			Resource: permissions.ResourceFile,
			Action:   permissions.ActionUpdate,
			Effects:  []permissions.Effect{permissions.EffectWrite},
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
			path := str.String(req.Path).Trim()
			if path == "" {
				return tools.PermissionPreparation{}, tools.NewPermissionResolutionError(
					"invalid_input",
					"path is required",
				)
			}
			target, targetScope := common.ResolveFilesystemPermissionTarget(
				common.FilesystemPolicyFromRuntime(runtime),
				path,
			)

			operation := permissions.Operation{
				Resource:    permissions.ResourceFile,
				Action:      permissions.ActionUpdate,
				Effects:     []permissions.Effect{permissions.EffectWrite},
				Target:      target,
				TargetScope: targetScope,
			}
			preparation, err := common.PrepareFilesystemExecution(
				ctx,
				runtime,
				operation,
				path,
				execution.FilesystemOperation{
					Action: execution.FilesystemWrite,
					Data:   []byte(req.Content),
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
				"Absolute path to the file or path relative to the configured workspace root.",
			),
			"content": common.StringSchema("Text content to write to the target file."),
			"create_dirs": common.BooleanSchema(
				"When true, create missing parent directories before writing. Defaults to true.",
			),
		}, "path", "content"),
		Handler: tools.HandlerFunc(
			func(ctx context.Context, call tools.Call) (tools.Result, error) {
				var req input
				if result := common.DecodeInput(call, &req); result.Error != "" {
					return result, nil
				}
				pathValue := str.String(req.Path)
				if pathValue.Trim() == "" {
					return common.ToolError("invalid_input", "path is required"), nil
				}

				if guardrails.IsBinary([]byte(req.Content)) {
					return common.ToolError("not_text", "content must be text"), nil
				}
				createDirs := true
				if req.CreateDirs != nil {
					createDirs = *req.CreateDirs
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
				info, writeErr := common.ObserveExecution(
					ctx,
					spec,
					func() (execution.FileInfo, error) {
						return service.WriteFile(ctx, spec, createDirs)
					},
				)
				if writeErr != nil {
					return common.FileError(writeErr), nil
				}
				return common.EncodeOutput(map[string]any{
					"path": common.NormalizedDisplayPath(
						req.Path,
					), "bytes_written": info.Size, "created": info.Created,
				})
			},
		),
	}
}
