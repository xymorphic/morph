package permissions

import (
	"context"
	"io"

	cli "github.com/urfave/cli/v3"

	permissionscli "github.com/wandxy/morph/internal/cli/permissions"
)

func SetOutput(writer io.Writer) io.Writer {
	return permissionscli.SetOutput(writer)
}

func NewCommand() *cli.Command {
	return &cli.Command{
		Name:  "permissions",
		Usage: "Inspect and resolve permission approvals",
		Commands: []*cli.Command{
			permissionscli.NewListCommand(),
			permissionscli.NewPendingCommand(),
			permissionscli.NewGrantsCommand(),
			permissionscli.NewInspectCommand(),
			permissionscli.NewPresetCommand(),
			permissionscli.NewPruneCommand(),
			permissionscli.NewApproveCommand(),
			permissionscli.NewDenyCommand(),
			permissionscli.NewRevokeCommand(),
			permissionscli.NewDeleteCommand(),
			permissionscli.NewExplainCommand(),
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			return cli.ShowSubcommandHelp(cmd)
		},
	}
}
