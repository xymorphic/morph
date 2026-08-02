package readfile

import (
	"context"
	"encoding/json"

	"github.com/wandxy/morph/pkg/logutils"
	"github.com/wandxy/morph/pkg/str"

	envtypes "github.com/wandxy/morph/internal/environment/types"
	"github.com/wandxy/morph/internal/execution"
	"github.com/wandxy/morph/internal/permissions"
	"github.com/wandxy/morph/internal/tools"
	"github.com/wandxy/morph/internal/tools/common"
)

var log = logutils.Module("tool.readfile")

// Definition returns the model-visible tool definition.
func Definition(runtime envtypes.Runtime) tools.Definition {
	type input struct {
		Path string `json:"path"`
	}

	return tools.Definition{
		Name:         "read_file",
		Description:  "Read a text file at an absolute or workspace-relative path, subject to the current permission policy.",
		ParallelSafe: true,
		Groups:       []string{"core"},
		Requires:     tools.Capabilities{Filesystem: true},
		SemanticIndex: tools.ProjectSemanticIndex(
			tools.ProjectJSONFieldsForSemanticIndex("path", "content"),
		),
		Permission: permissions.Operation{
			Resource: permissions.ResourceFile,
			Action:   permissions.ActionRead,
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
				Action:      permissions.ActionRead,
				Effects:     []permissions.Effect{permissions.EffectRead},
				Target:      target,
				TargetScope: targetScope,
			}
			preparation, err := common.PrepareFilesystemExecution(
				ctx,
				runtime,
				operation,
				path,
				execution.FilesystemOperation{
					Action: execution.FilesystemRead,
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
				"Absolute path to the text file or path relative to the configured workspace root.",
			),
		}, "path"),
		Handler: tools.HandlerFunc(
			func(ctx context.Context, call tools.Call) (tools.Result, error) {
				var req input
				if result := common.DecodeInput(call, &req); result.Error != "" {
					return result, nil
				}

				log.Info().
					Str("tool", "read_file").
					Str("phase", "start").
					Str("path", common.NormalizedDisplayPath(req.Path)).
					Msg("read file tool started")

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
				content, readErr := common.ObserveExecution(ctx, spec, func() ([]byte, error) {
					return service.ReadFile(ctx, spec, int(common.MaxReadBytes))
				})
				if readErr != nil {
					return common.FileError(readErr), nil
				}
				return common.EncodeOutput(map[string]any{
					"path": common.NormalizedDisplayPath(
						req.Path,
					), "content": string(content), "bytes": len(content),
				})
			},
		),
	}
}
