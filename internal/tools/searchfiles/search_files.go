package searchfiles

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/xymorphic/morph/pkg/str"

	envtypes "github.com/xymorphic/morph/internal/environment/types"
	"github.com/xymorphic/morph/internal/execution"
	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/internal/tools"
	"github.com/xymorphic/morph/internal/tools/common"
)

type contentMatch struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
	Text   string `json:"text"`
}

// Definition returns the model-visible tool definition.
func Definition(runtime envtypes.Runtime) tools.Definition {
	type input struct {
		Pattern       string `json:"pattern"`
		Path          string `json:"path"`
		CaseSensitive bool   `json:"case_sensitive"`
		IncludeHidden bool   `json:"include_hidden"`
		MaxResults    int    `json:"max_results"`
	}

	type match struct {
		Path   string `json:"path"`
		Line   int    `json:"line"`
		Column int    `json:"column"`
		Text   string `json:"text"`
	}

	return tools.Definition{
		Name:          "search_files",
		Description:   "Search file contents at an absolute or workspace-relative path, subject to the current permission policy.",
		ParallelSafe:  true,
		Groups:        []string{"core"},
		Requires:      tools.Capabilities{Filesystem: true},
		SemanticIndex: tools.ProjectSemanticIndex(projectSemanticContent),
		Permission: permissions.Operation{
			Resource: permissions.ResourceFile,
			Action:   permissions.ActionSearch,
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
			if str.String(req.Pattern).Trim() == "" {
				return tools.PermissionPreparation{}, tools.NewPermissionResolutionError(
					"invalid_input",
					"pattern is required",
				)
			}
			target, targetScope := common.ResolveFilesystemPermissionTarget(
				common.FilesystemPolicyFromRuntime(runtime),
				req.Path,
			)
			operation := permissions.Operation{
				Resource:    permissions.ResourceFile,
				Action:      permissions.ActionSearch,
				Effects:     []permissions.Effect{permissions.EffectRead},
				Target:      target,
				TargetScope: targetScope,
			}
			preparation, err := common.PrepareFilesystemExecution(
				ctx,
				runtime,
				operation,
				req.Path,
				execution.FilesystemOperation{
					Action:        execution.FilesystemSearch,
					Query:         req.Pattern,
					IncludeHidden: req.IncludeHidden,
					CaseSensitive: req.CaseSensitive,
					Recursive:     true,
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
			"pattern": common.StringSchema("Text or pattern to search for within files."),
			"path": common.StringSchema(
				"Absolute path or path relative to the configured workspace root to search within. Defaults to the workspace root when omitted.",
			),
			"case_sensitive": common.BooleanSchema(
				"When true, match text using case-sensitive search.",
			),
			"include_hidden": common.BooleanSchema(
				"When true, include hidden files and directories in the search.",
			),
			"max_results": common.IntegerSchema(
				"Maximum number of matches to return. Values outside the supported range are clamped.",
			),
		}, "pattern"),
		Handler: tools.HandlerFunc(
			func(ctx context.Context, call tools.Call) (tools.Result, error) {
				var req input
				if result := common.DecodeInput(call, &req); result.Error != "" {
					return result, nil
				}
				patternValue := str.String(req.Pattern)
				if patternValue.Trim() == "" {
					return common.ToolError("invalid_input", "pattern is required"), nil
				}

				limit := req.MaxResults
				if limit <= 0 || limit > common.MaxSearchResults {
					limit = common.MaxSearchResults
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
				found, searchErr := common.ObserveExecution(
					ctx,
					spec,
					func() ([]execution.SearchMatch, error) {
						return service.SearchFiles(ctx, spec, limit)
					},
				)
				if searchErr != nil {
					return common.FileError(searchErr), nil
				}
				out := make([]match, 0, len(found))
				for _, item := range found {
					out = append(out, match(item))
				}
				return common.EncodeOutput(map[string]any{
					"root":    spec.Operation().Filesystem.Path.ContainerPath(),
					"path":    common.NormalizedDisplayPath(req.Path),
					"pattern": req.Pattern,
					"matches": out,
				})
			},
		),
	}
}

func projectSemanticContent(_ tools.Call, result tools.Result) string {
	var output struct {
		Matches []contentMatch `json:"matches"`
	}
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		return ""
	}

	lines := make([]string, 0, len(output.Matches))
	for _, match := range output.Matches {
		lines = append(lines, fmt.Sprintf("%s:%d: %s", match.Path, match.Line, match.Text))
	}
	return strings.Join(lines, "\n")
}
