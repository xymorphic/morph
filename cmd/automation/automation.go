package automation

import (
	"context"
	"io"

	cli "github.com/urfave/cli/v3"

	automationcli "github.com/xymorphic/morph/internal/cli/automation"
)

func SetOutput(w io.Writer) io.Writer {
	return automationcli.SetOutput(w)
}

func NewCommand() *cli.Command {
	return &cli.Command{
		Name:  "automation",
		Usage: "Manage scheduled automation jobs",
		Commands: []*cli.Command{
			automationcli.NewStatusCommand(),
			automationcli.NewListCommand(),
			automationcli.NewAddCommand(),
			automationcli.NewUpdateCommand(),
			automationcli.NewPauseCommand(),
			automationcli.NewResumeCommand(),
			automationcli.NewRunCommand(),
			automationcli.NewRemoveCommand(),
			automationcli.NewRunsCommand(),
			automationcli.NewDiagnoseCommand(),
			automationcli.NewInspectCommand(),
			automationcli.NewRecoverCommand(),
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			return cli.ShowSubcommandHelp(cmd)
		},
	}
}
