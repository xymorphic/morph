package automation

import (
	"context"
	"fmt"
	"time"

	cli "github.com/urfave/cli/v3"

	coreautomation "github.com/xymorphic/morph/internal/automation"
)

func NewDiagnoseCommand() *cli.Command {
	return &cli.Command{
		Name:  "diagnose",
		Usage: "Check automation jobs for operational issues",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "all", Usage: "Include disabled jobs"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			api, closeClient, err := getAutomationAPI(ctx, cmd)
			if err != nil {
				return err
			}
			defer closeClient()

			list, err := api.List(ctx, coreautomation.JobQuery{IncludeDisabled: cmd.Bool("all")})
			if err != nil {
				return err
			}
			findings := coreautomation.DiagnoseJobs(list.Jobs, coreautomation.DiagnosticOptions{})
			return writeDiagnosticFindings(findings)
		},
	}
}

func NewInspectCommand() *cli.Command {
	return &cli.Command{
		Name:      "inspect",
		Usage:     "Inspect an automation job and recent runs",
		ArgsUsage: "<job-id>",
		Flags: []cli.Flag{
			&cli.IntFlag{Name: "failures", Value: 5, Usage: "Recent failures to show"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			api, closeClient, err := getAutomationAPI(ctx, cmd)
			if err != nil {
				return err
			}
			defer closeClient()

			id, err := getRequiredArg(cmd, "automation job id is required")
			if err != nil {
				return err
			}
			list, err := api.List(ctx, coreautomation.JobQuery{
				IDs:             []string{id},
				IncludeDisabled: true,
				Limit:           1,
			})
			if err != nil {
				return err
			}
			if len(list.Jobs) == 0 {
				return fmt.Errorf("automation job not found")
			}
			runs, err := api.Runs(ctx, coreautomation.RunQuery{JobID: id, Limit: 20})
			if err != nil {
				return err
			}

			return writeInspection(coreautomation.InspectRunHistory(list.Jobs[0], runs.Runs, cmd.Int("failures")))
		},
	}
}

func NewRecoverCommand() *cli.Command {
	return &cli.Command{
		Name:  "recover",
		Usage: "Repair automation scheduler state",
		Commands: []*cli.Command{
			newRecoverRecomputeSchedulesCommand(),
			newRecoverClearRunningCommand(),
			newRecoverRerunFailedCommand(),
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			return cli.ShowSubcommandHelp(cmd)
		},
	}
}

func newRecoverRecomputeSchedulesCommand() *cli.Command {
	return &cli.Command{
		Name:  "recompute-schedules",
		Usage: "Recompute next run times for enabled jobs",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			api, closeClient, err := getAutomationAPI(ctx, cmd)
			if err != nil {
				return err
			}
			defer closeClient()

			list, err := api.List(ctx, coreautomation.JobQuery{IncludeDisabled: true})
			if err != nil {
				return err
			}
			count := 0
			for _, job := range list.Jobs {
				if !job.Enabled {
					continue
				}

				state := job.State
				if _, err := api.Update(ctx, coreautomation.JobPatch{ID: job.ID, State: &state}); err != nil {
					return err
				}
				count++
			}

			_, err = fmt.Fprintf(automationOutput, "recomputed=%d\n", count)
			return err
		},
	}
}

func newRecoverClearRunningCommand() *cli.Command {
	return &cli.Command{
		Name:      "clear-running",
		Usage:     "Clear a stuck running marker",
		ArgsUsage: "<job-id>",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			api, closeClient, err := getAutomationAPI(ctx, cmd)
			if err != nil {
				return err
			}
			defer closeClient()

			id, err := getRequiredArg(cmd, "automation job id is required")
			if err != nil {
				return err
			}
			list, err := api.List(ctx, coreautomation.JobQuery{
				IDs:             []string{id},
				IncludeDisabled: true,
				Limit:           1,
			})
			if err != nil {
				return err
			}
			if len(list.Jobs) == 0 {
				return fmt.Errorf("automation job not found")
			}
			state := list.Jobs[0].State
			state.RunningAt = time.Time{}
			job, err := api.Update(ctx, coreautomation.JobPatch{ID: id, State: &state})
			if err != nil {
				return err
			}

			_, err = fmt.Fprintf(automationOutput, "%s running=false\n", job.ID)
			return err
		},
	}
}

func newRecoverRerunFailedCommand() *cli.Command {
	return &cli.Command{
		Name:      "rerun-failed",
		Usage:     "Run a job again after a failed run",
		ArgsUsage: "<job-id>",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			api, closeClient, err := getAutomationAPI(ctx, cmd)
			if err != nil {
				return err
			}
			defer closeClient()

			id, err := getRequiredArg(cmd, "automation job id is required")
			if err != nil {
				return err
			}
			list, err := api.Runs(ctx, coreautomation.RunQuery{
				JobID:  id,
				Status: []coreautomation.RunStatus{coreautomation.RunStatusError},
				Limit:  1,
			})
			if err != nil {
				return err
			}
			if len(list.Runs) == 0 {
				return fmt.Errorf("automation job has no failed runs")
			}
			run, err := api.Run(ctx, id)
			if err != nil {
				return err
			}

			return writeRunSummary(run)
		},
	}
}
