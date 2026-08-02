package sessionsearch

import (
	"context"
	"fmt"

	envtypes "github.com/xymorphic/morph/internal/environment/types"
	"github.com/xymorphic/morph/internal/instructions"
	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/internal/tools"
	"github.com/xymorphic/morph/internal/tools/common"
	"github.com/xymorphic/morph/pkg/str"
)

type input struct {
	SessionID  string `json:"session_id"`
	Query      string `json:"query"`
	Role       string `json:"role"`
	ToolName   string `json:"tool_name"`
	MaxResults int    `json:"max_results"`
}

// Definition returns the model-visible tool definition.
func Definition(runtime envtypes.Runtime) tools.Definition {
	return tools.Definition{
		Name:             "session_search",
		Description:      "Search prior messages in the current session or other sessions.",
		UsageInstruction: instructions.BuildSessionSearchGuidance(),
		ParallelSafe:     true,
		Groups:           []string{"core"},
		Requires:         tools.Capabilities{Memory: true},
		SemanticIndex:    tools.SkipSemanticIndex(),
		Permission: permissions.Operation{
			Resource: permissions.ResourceSession,
			Action:   permissions.ActionSearch,
			Effects:  []permissions.Effect{permissions.EffectRead},
		},
		InputSchema: common.ObjectSchema(map[string]any{
			"session_id":  common.StringSchema("Optional session id. When omitted, search other sessions and exclude the current session."),
			"query":       common.StringSchema("Search query for prior messages."),
			"role":        common.StringSchema("Optional role filter: user, assistant, or tool."),
			"tool_name":   common.StringSchema("Optional tool-name filter."),
			"max_results": common.IntegerSchema("Optional maximum number of results."),
		}, "query"),
		Handler: tools.HandlerFunc(func(ctx context.Context, call tools.Call) (tools.Result, error) {
			var req input
			if result := common.DecodeInput(call, &req); result.Error != "" {
				return result, nil
			}
			if runtime == nil {
				return common.ToolError("tool_error", "session search is not configured"), nil
			}
			queryValue := str.String(req.Query)
			query := queryValue.Trim()
			if query == "" {
				return common.ToolError("invalid_input", "query is required"), nil
			}
			roleValue := str.String(req.Role)
			role := roleValue.Normalized()
			switch role {
			case "", "user", "assistant", "tool":
			default:
				return common.ToolError("invalid_input", fmt.Sprintf("unsupported role %q", role)), nil
			}
			sessionIDValue := str.String(req.SessionID)
			sessionID := sessionIDValue.Trim()
			ignoreSessionID := ""
			if sessionID == "" {
				sessionIDFromContextValue := str.String(tools.SessionIDFromContext(ctx))
				ignoreSessionID = sessionIDFromContextValue.Trim()
			}
			toolNameValue := str.String(req.ToolName)
			results, err := runtime.SearchSession(ctx, envtypes.SessionSearchRequest{
				SessionID:       sessionID,
				IgnoreSessionID: ignoreSessionID,
				Query:           query,
				Role:            role,
				ToolName:        toolNameValue.Trim(),
				MaxResults:      req.MaxResults,
			})
			if err != nil {
				return common.ToolError("tool_error", err.Error()), nil
			}

			return common.EncodeOutput(map[string]any{"results": results})
		}),
	}
}
