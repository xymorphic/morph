package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	cli "github.com/urfave/cli/v3"

	morphcli "github.com/wandxy/morph/internal/cli"
	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/execution"
	"github.com/wandxy/morph/internal/permissions"
	rpcclient "github.com/wandxy/morph/internal/rpc/client"
	storage "github.com/wandxy/morph/internal/state/core"
)

var (
	output    io.Writer = os.Stdout
	newClient           = func(ctx context.Context, cfg *config.Config) (sandboxClient, error) {
		return rpcclient.NewClient(ctx, rpcclient.OptionsWithConfigAuth(rpcclient.Options{
			Address:           cfg.RPC.Address,
			Port:              cfg.RPC.Port,
			PermissionSurface: permissions.SurfaceCLI,
			PermissionPreset:  cfg.Permissions.EffectivePreset(),
			AuthServices:      []string{"/morph.v1.SessionService"},
		}, cfg))
	}
)

type sandboxClient interface {
	Close() error
	SessionAPI() rpcclient.SessionAPI
}

func NewCommand() *cli.Command {
	return &cli.Command{
		Name:  "sandbox",
		Usage: "Inspect command execution environments",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "session",
				Value: storage.DefaultSessionID,
				Usage: "Session to inspect",
			},
			&cli.BoolFlag{
				Name:  "json",
				Usage: "Print machine-readable JSON",
			},
		},
		Commands: []*cli.Command{
			{
				Name:   "list",
				Usage:  "List execution environments",
				Action: listEnvironments,
			},
			{
				Name:      "explain",
				Usage:     "Explain an execution environment",
				ArgsUsage: "<environment-id>",
				Action:    explainEnvironment,
			},
		},
		Action: func(_ context.Context, cmd *cli.Command) error { return cli.ShowSubcommandHelp(cmd) },
	}
}

func listEnvironments(ctx context.Context, cmd *cli.Command) error {
	api, closeClient, err := getAPI(ctx, cmd)
	if err != nil {
		return err
	}
	defer closeClient()

	items, err := api.ListExecutionEnvironments(ctx, cmd.String("session"))
	if err != nil {
		return err
	}
	if cmd.Bool("json") {
		return writeJSON(items)
	}

	for _, item := range items {
		if _, err := fmt.Fprintf(
			output,
			"%s\t%s\t%s\t%s\n",
			item.Status.ID,
			item.Status.Backend,
			item.Status.Scope,
			item.Status.State,
		); err != nil {
			return err
		}
	}

	return nil
}

func explainEnvironment(ctx context.Context, cmd *cli.Command) error {
	id := cmd.Args().First()
	if id == "" {
		return errors.New("execution environment id is required")
	}

	api, closeClient, err := getAPI(ctx, cmd)
	if err != nil {
		return err
	}
	defer closeClient()

	details, err := api.ExplainExecutionEnvironment(ctx, cmd.String("session"), id)
	if err != nil {
		return err
	}
	if cmd.Bool("json") {
		return writeJSON(details)
	}

	return writeDetails(details)
}

func getAPI(ctx context.Context, cmd *cli.Command) (rpcclient.SessionAPI, func(), error) {
	cfg, _, err := morphcli.LoadConfig(cmd)
	if err != nil {
		return nil, func() {}, err
	}

	cfg.Normalize()

	client, err := newClient(ctx, cfg)
	if err != nil {
		return nil, func() {}, err
	}

	api := client.SessionAPI()
	if api == nil {
		_ = client.Close()
		return nil, func() {}, errors.New("session RPC client is unavailable")
	}

	return api, func() { _ = client.Close() }, nil
}

func writeDetails(details execution.EnvironmentDetails) error {
	status := details.Status
	_, err := fmt.Fprintf(
		output,
		"id: %s\nbackend: %s\nscope: %s\nstate: %s\nworkspace: %s\nnetwork: %s\nimage: %s\nsecurity generation: %s\n",
		status.ID,
		status.Backend,
		status.Scope,
		status.State,
		status.WorkspaceMode,
		status.Network,
		status.ImageDigest,
		status.SecurityGeneration,
	)
	return err
}

func writeJSON(value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
