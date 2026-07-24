package runcommand

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"time"

	"github.com/wandxy/morph/pkg/logutils"

	commandplan "github.com/wandxy/morph/internal/command"
	envtypes "github.com/wandxy/morph/internal/environment/types"
	"github.com/wandxy/morph/internal/permissions"
	"github.com/wandxy/morph/internal/tools"
	"github.com/wandxy/morph/internal/tools/common"
)

type input struct {
	Mode           commandplan.Mode             `json:"mode"`
	Command        string                       `json:"command"`
	Args           []string                     `json:"args"`
	Cwd            string                       `json:"cwd"`
	Env            []common.EnvironmentVariable `json:"env"`
	TimeoutSeconds int                          `json:"timeout_seconds"`
}

var (
	log        = logutils.Module("tool.runcommand")
	newCommand = func(ctx context.Context, plan commandplan.Plan) (*exec.Cmd, error) {
		return plan.NewCommand(ctx)
	}
	waitCommand = (*exec.Cmd).Wait
)

// Definition returns the model-visible tool definition.
func Definition(runtime envtypes.Runtime) tools.Definition {
	return tools.Definition{
		Name: "run_command",
		Description: common.JoinStrings(
			"Run a short-lived, non-interactive command.",
			"Default timeout 30s, max 120s.",
			"Kills the process (main/child/background) on timeout.",
		),
		Groups:        []string{"core"},
		Requires:      tools.Capabilities{Exec: true},
		SemanticIndex: tools.ProjectSemanticIndex(tools.ProjectJSONFieldsForSemanticIndex("stdout", "stderr")),
		Permission: permissions.Operation{
			Resource: permissions.ResourceProcess,
			Action:   permissions.ActionExecute,
			Effects:  []permissions.Effect{permissions.EffectExecution},
		},
		PreparePermission: preparePermission(runtime),
		ResolvePermission: resolvePermission(runtime),
		InputSchema: common.ObjectSchema(map[string]any{
			"mode": map[string]any{
				"type":        "string",
				"description": "Execution mode. Defaults to direct.",
				"enum":        []string{"direct", "posix_shell"},
			},
			"command": common.StringSchema("Executable name in direct mode or POSIX shell source in posix_shell mode."),
			"args": map[string]any{
				"type":        "array",
				"description": "Arguments passed directly to the command.",
				"items": map[string]any{
					"type": "string",
				},
			},
			"cwd": common.StringSchema("Absolute working directory or path relative to the configured workspace root."),
			"env": common.EnvironmentVariablesSchema("Environment variable overrides as name/value entries."),
			"timeout_seconds": common.IntegerSchema(common.JoinStrings(
				"Timeout in seconds. Default 30. Max 120.",
				"Terminates the command/processes when reached.",
			)),
		}, "command"),
		Handler: tools.HandlerFunc(func(ctx context.Context, call tools.Call) (tools.Result, error) {
			var req input
			if result := common.DecodeInput(call, &req); result.Error != "" {
				return result, nil
			}
			environment, err := common.EnvironmentVariablesToMap(req.Env)
			if err != nil {
				return common.ToolError("invalid_input", err.Error()), nil
			}
			plan, err := getCommandPlan(ctx, runtime, call, req, environment)
			if err != nil {
				return common.ToolError(common.CommandErrorCode(err), err.Error()), nil
			}
			if code, message := common.CheckCommandPlan(
				ctx,
				runtime,
				plan,
				permissions.ActionExecute,
				[]permissions.Effect{permissions.EffectExecution},
			); code != "" {
				return common.ToolError(code, message), nil
			}
			if _, err := common.ResolveFilesystemPathForOperation(
				ctx,
				common.FilesystemPolicyFromRuntime(runtime),
				plan.CWD,
				permissions.ActionRead,
			); err != nil {
				return common.FileError(err), nil
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

			cmd, err := newCommand(context.Background(), plan)
			if err != nil {
				return common.ToolError("command_failed", err.Error()), nil
			}
			configureCommandProcess(cmd)

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			log.Debug().
				Str("tool", "run_command").
				Str("phase", "execute").
				Msg("command execution started")

			startedAt := time.Now()
			if err := cmd.Start(); err != nil {
				log.Warn().
					Str("tool", "run_command").
					Str("phase", "error").
					Str("error_kind", "start_failed").
					Msg("command execution failed to start")
				return common.ToolError("command_failed", err.Error()), nil
			}

			done := make(chan error, 1)
			go func() {
				done <- waitCommand(cmd)
			}()

			select {
			case err = <-done:
			case <-runCtx.Done():
				terminateCommandProcess(cmd)
				err = <-done
			}

			elapsedSeconds := time.Since(startedAt).Seconds()

			if runCtx.Err() == context.DeadlineExceeded {
				log.Info().
					Str("tool", "run_command").
					Str("phase", "complete").
					Int("exit_code", -1).
					Bool("timed_out", true).
					Int("stdout_bytes", len(stdout.String())).
					Int("stderr_bytes", len(stderr.String())).
					Float64("elapsed_seconds", elapsedSeconds).
					Msg("command tool timed out")

				return common.EncodeOutput(buildRunCommandOutput(
					-1,
					common.TrimOutput(stdout.String(), common.MaxOutputBytes),
					common.TrimOutput(stderr.String(), common.MaxOutputBytes),
					true,
					timeout,
					elapsedSeconds,
				))
			}

			if runCtx.Err() == context.Canceled {
				log.Warn().
					Str("tool", "run_command").
					Str("phase", "error").
					Str("error_kind", "context_canceled").
					Msg("command execution canceled")
				return common.ToolError("command_failed", runCtx.Err().Error()), nil
			}

			exitCode := 0
			if err != nil {
				if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
					exitCode = exitErr.ExitCode()
				} else {
					log.Warn().
						Str("tool", "run_command").
						Str("phase", "error").
						Str("error_kind", "execution_failed").
						Msg("command execution failed")
					return common.ToolError("command_failed", err.Error()), nil
				}
			}

			log.Info().
				Str("tool", "run_command").
				Str("phase", "complete").
				Int("exit_code", exitCode).
				Bool("timed_out", false).
				Int("stdout_bytes", len(stdout.String())).
				Int("stderr_bytes", len(stderr.String())).
				Float64("elapsed_seconds", elapsedSeconds).
				Msg("command tool completed")

			return common.EncodeOutput(buildRunCommandOutput(
				exitCode,
				common.TrimOutput(stdout.String(), common.MaxOutputBytes),
				common.TrimOutput(stderr.String(), common.MaxOutputBytes),
				false,
				timeout,
				elapsedSeconds,
			))
		}),
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
			return tools.PermissionPreparation{}, tools.NewPermissionResolutionError("invalid_input", "invalid tool input")
		}
		environment, err := common.EnvironmentVariablesToMap(req.Env)
		if err != nil {
			return tools.PermissionPreparation{}, tools.NewPermissionResolutionError("invalid_input", err.Error())
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
			return tools.PermissionPreparation{}, tools.NewPermissionResolutionError(common.CommandErrorCode(err), err.Error())
		}
		return tools.PermissionPreparation{
			Inputs: common.CommandPermissionInputs(
				ctx,
				runtime,
				plan,
				permissions.ActionExecute,
				[]permissions.Effect{permissions.EffectExecution},
			),
			Prepared: plan,
		}, nil
	}
}

func getCommandPlan(
	ctx context.Context,
	runtime envtypes.Runtime,
	call tools.Call,
	req input,
	environment map[string]string,
) (commandplan.Plan, error) {
	if prepared, ok := tools.GetPreparedCall(ctx, call); ok {
		if plan, planOK := prepared.(commandplan.Plan); planOK {
			return plan, nil
		}
		return commandplan.Plan{}, errors.New("prepared command plan is invalid")
	}
	return common.AnalyzeCommand(ctx, runtime, req.Mode, req.Command, req.Args, req.Cwd, environment)
}

func buildRunCommandOutput(exitCode int, stdout, stderr string, timedOut bool, timeoutSeconds int, elapsedSeconds float64) map[string]any {
	remainingSeconds := 0.0
	if !timedOut {
		remainingSeconds = float64(timeoutSeconds) - elapsedSeconds
		if remainingSeconds < 0 {
			remainingSeconds = 0
		}
	}

	return map[string]any{
		"exit_code":         exitCode,
		"stdout":            stdout,
		"stderr":            stderr,
		"timed_out":         timedOut,
		"timeout_seconds":   timeoutSeconds,
		"elapsed_seconds":   elapsedSeconds,
		"remaining_seconds": remainingSeconds,
	}
}
