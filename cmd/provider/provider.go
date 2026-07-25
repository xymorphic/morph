package providercmd

import (
	"context"
	"io"

	cli "github.com/urfave/cli/v3"

	authcmd "github.com/wandxy/morph/cmd/auth"
)

func NewCommand(input io.Reader, output io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "provider",
		Usage: "Manage model and web providers",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "Print machine-readable JSON"},
		},
		Commands: []*cli.Command{
			authcmd.NewProviderLoginCommand(),
			authcmd.NewProviderStatusCommand(),
			authcmd.NewProviderLogoutCommand(),
			NewProviderConfigureCommand(input, output),
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			return cli.ShowSubcommandHelp(cmd)
		},
	}
}
