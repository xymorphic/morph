package runcommand

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/xymorphic/morph/pkg/logutils"

	commandplan "github.com/xymorphic/morph/internal/command"
	envtypes "github.com/xymorphic/morph/internal/environment/types"
	"github.com/xymorphic/morph/internal/execution"
	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/internal/tools"
	"github.com/xymorphic/morph/internal/tools/common"
	"github.com/xymorphic/morph/internal/trace"
)

type input struct {
	Mode           commandplan.Mode             `json:"mode"`
	Command        string                       `json:"command"`
	Args           []string                     `json:"args"`
	Cwd            string                       `json:"cwd"`
	Env            []common.EnvironmentVariable `json:"env"`
	Secrets        []string                     `json:"secrets"`
	TimeoutSeconds int                          `json:"timeout_seconds"`
}

var log = logutils.Module("tool.runcommand")

// Definition returns the model-visible tool definition.
func Definition(runtime envtypes.Runtime) tools.Definition {
	return tools.Definition{
		Name: "run_command",
		Description: common.JoinStrings(
			"Run a short-lived, non-interactive command.",
			"Direct mode launches the executable itself; use posix_shell only for shell syntax.",
			"Default timeout 30s, max 120s.",
			"Kills the process (main/child/background) on timeout.",
		),
		Groups:   []string{"core"},
		Requires: tools.Capabilities{Exec: true},
		SemanticIndex: tools.ProjectSemanticIndex(
			tools.ProjectJSONFieldsForSemanticIndex("stdout", "stderr"),
		),
		Permission: permissions.Operation{
			Resource: permissions.ResourceProcess,
			Action:   permissions.ActionExecute,
			Effects:  []permissions.Effect{permissions.EffectExecution},
		},
		PreparePermission: preparePermission(runtime),
		ResolvePermission: resolvePermission(runtime),
		InputSchema: common.ObjectSchema(common.AddExecutionSecretsSchema(map[string]any{
			"mode": map[string]any{
				"type":        "string",
				"description": "Execution mode. Defaults to direct. Direct launches the executable itself; invoking sh -c is still indirect shell execution.",
				"enum":        []string{"direct", "posix_shell"},
			},
			"command": common.StringSchema(
				"Executable name in direct mode or POSIX shell source in posix_shell mode. Do not wrap commands with sh -c to imitate direct execution.",
			),
			"args": map[string]any{
				"type":        "array",
				"description": "Arguments passed directly to the command.",
				"items": map[string]any{
					"type": "string",
				},
			},
			"cwd": common.StringSchema(
				"Absolute working directory or path relative to the configured workspace root.",
			),
			"env": common.EnvironmentVariablesSchema(
				"Environment variable overrides as name/value entries.",
			),
			"timeout_seconds": common.IntegerSchema(common.JoinStrings(
				"Timeout in seconds. Default 30. Max 120.",
				"Terminates the command/processes when reached.",
			)),
		}, runtime, "Configured secret references to expose to this command as MORPH_SECRET_<NAME> variables."), "command"),
		Handler: tools.HandlerFunc(
			func(ctx context.Context, call tools.Call) (tools.Result, error) {
				var req input
				if result := common.DecodeInput(call, &req); result.Error != "" {
					return result, nil
				}
				environment, err := common.EnvironmentVariablesToMap(req.Env)
				if err != nil {
					return common.ToolError("invalid_input", err.Error()), nil
				}
				plan, spec, err := getCommandPlan(ctx, runtime, call, req, environment)
				if err != nil {
					return common.ToolError(common.CommandErrorCode(err), err.Error()), nil
				}
				if code, message := common.CheckCommandPlan(
					ctx,
					runtime,
					plan,
					spec.Exposure(),
					permissions.ActionExecute,
					[]permissions.Effect{permissions.EffectExecution},
				); code != "" {
					return common.ToolError(code, message), nil
				}
				if spec.Exposure().Backend() != execution.BackendDocker {
					if _, err := common.ResolveFilesystemPathForOperation(
						ctx,
						common.FilesystemPolicyFromRuntime(runtime),
						plan.CWD,
						permissions.ActionRead,
					); err != nil {
						return common.FileError(err), nil
					}
				}

				timeout := common.WithTimeoutSeconds(req.TimeoutSeconds)
				log.Info().
					Str("tool", "run_command").
					Str("phase", "start").
					Str("mode", string(plan.Mode)).
					Bool("cwd_provided", req.Cwd != "").
					Int("args_count", len(req.Args)).
					Int("env_overrides", len(req.Env)).
					Int("invocation_count", len(plan.Invocations)).
					Int("redirect_count", len(plan.Redirects)).
					Bool("complete", plan.Complete).
					Int("timeout_seconds", timeout).
					Msg("command tool started")

				runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
				defer cancel()

				if err := runCtx.Err(); err != nil {
					log.Warn().
						Str("tool", "run_command").
						Str("phase", "error").
						Str("error_kind", "context_unavailable_before_execution").
						Msg("run command failed before execution")
					return common.ToolError("command_failed", err.Error()), nil
				}

				service, serviceErr := common.ExecutionService(runtime)
				if serviceErr != nil {
					return common.ToolError("command_failed", serviceErr.Error()), nil
				}
				startedAt := time.Now()
				common.RecordExecutionTrace(
					tools.TraceRecorderFromContext(ctx),
					trace.EvtExecutionStarted,
					spec,
					execution.CommandResult{},
					nil,
				)
				result, runErr := service.Run(runCtx, spec)
				if runErr != nil {
					common.RecordExecutionTrace(
						tools.TraceRecorderFromContext(ctx),
						trace.EvtExecutionFailed,
						spec,
						result,
						runErr,
					)
					return common.ToolError("command_failed", runErr.Error()), nil
				}
				if result.Interrupted {
					common.RecordExecutionTrace(
						tools.TraceRecorderFromContext(ctx),
						trace.EvtExecutionFailed,
						spec,
						result,
						context.Canceled,
					)
					return common.ToolError("command_failed", context.Canceled.Error()), nil
				}
				common.RecordExecutionTrace(
					tools.TraceRecorderFromContext(ctx),
					trace.EvtExecutionCompleted,
					spec,
					result,
					nil,
				)
				return common.EncodeOutput(buildRunCommandOutput(
					result.ExitCode,
					common.TrimOutput(result.Stdout, common.MaxOutputBytes),
					common.TrimOutput(result.Stderr, common.MaxOutputBytes),
					result.TimedOut,
					timeout,
					time.Since(startedAt).Seconds(),
					tools.ApprovalWaitDurationFromContext(ctx).Seconds(),
				))
			},
		),
	}
}

func resolvePermission(runtime envtypes.Runtime) tools.PermissionResolver {
	return func(ctx context.Context, call tools.Call) ([]permissions.EvaluationInput, error) {
		preparation, err := preparePermission(runtime)(ctx, call)
		return preparation.Inputs, err
	}
}

func preparePermission(runtime envtypes.Runtime) tools.PermissionPreparer {
	return func(ctx context.Context, call tools.Call) (tools.PermissionPreparation, error) {
		var req input
		if err := json.Unmarshal([]byte(call.Input), &req); err != nil {
			return tools.PermissionPreparation{}, tools.NewPermissionResolutionError(
				"invalid_input",
				"invalid tool input",
			)
		}
		environment, err := common.EnvironmentVariablesToMap(req.Env)
		if err != nil {
			return tools.PermissionPreparation{}, tools.NewPermissionResolutionError(
				"invalid_input",
				err.Error(),
			)
		}
		plan, err := common.AnalyzeCommand(
			ctx,
			runtime,
			req.Mode,
			req.Command,
			req.Args,
			req.Cwd,
			environment,
		)
		if err != nil {
			return tools.PermissionPreparation{}, tools.NewPermissionResolutionError(
				common.CommandErrorCode(err),
				err.Error(),
			)
		}
		spec, specErr := common.PrepareExecutionSpec(ctx, runtime, execution.Operation{
			Kind:             execution.OperationCommand,
			SecretReferences: req.Secrets,
			Command:          &plan,
		})
		if specErr != nil {
			return tools.PermissionPreparation{}, tools.NewPermissionResolutionError(
				"execution_unavailable",
				specErr.Error(),
			)
		}
		inputs := common.CommandPermissionInputs(
			ctx,
			runtime,
			plan,
			spec.Exposure(),
			permissions.ActionExecute,
			[]permissions.Effect{permissions.EffectExecution},
		)
		inputs = append(inputs, common.ExecutionPermissionInputs(spec)...)
		return tools.PermissionPreparation{
			Inputs:   inputs,
			Prepared: spec,
		}, nil
	}
}

func getCommandPlan(
	ctx context.Context,
	runtime envtypes.Runtime,
	call tools.Call,
	req input,
	environment map[string]string,
) (commandplan.Plan, execution.Spec, error) {
	if prepared, ok := tools.GetPreparedCall(ctx, call); ok {
		if spec, specOK := prepared.(execution.Spec); specOK {
			operation := spec.Operation()
			if operation.Command != nil {
				return operation.Command.Clone(), spec, nil
			}
		}
		return commandplan.Plan{}, execution.Spec{}, errors.New("prepared command plan is invalid")
	}
	plan, err := common.AnalyzeCommand(
		ctx,
		runtime,
		req.Mode,
		req.Command,
		req.Args,
		req.Cwd,
		environment,
	)
	if err != nil {
		return commandplan.Plan{}, execution.Spec{}, err
	}
	spec, err := common.PrepareExecutionSpec(ctx, runtime, execution.Operation{
		Kind:             execution.OperationCommand,
		SecretReferences: req.Secrets,
		Command:          &plan,
	})
	return plan, spec, err
}

func buildRunCommandOutput(
	exitCode int,
	stdout, stderr string,
	timedOut bool,
	timeoutSeconds int,
	elapsedSeconds float64,
	approvalWaitSeconds float64,
) map[string]any {
	remainingSeconds := 0.0
	if !timedOut {
		remainingSeconds = float64(timeoutSeconds) - elapsedSeconds
		if remainingSeconds < 0 {
			remainingSeconds = 0
		}
	}

	return map[string]any{
		"exit_code":             exitCode,
		"stdout":                stdout,
		"stderr":                stderr,
		"timed_out":             timedOut,
		"timeout_seconds":       timeoutSeconds,
		"elapsed_seconds":       elapsedSeconds,
		"approval_wait_seconds": approvalWaitSeconds,
		"remaining_seconds":     remainingSeconds,
	}
}
