package sandbox

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	cli "github.com/urfave/cli/v3"

	morphcli "github.com/xymorphic/morph/internal/cli"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/execution"
	"github.com/xymorphic/morph/internal/permissions"
	rpcclient "github.com/xymorphic/morph/internal/rpc/client"
	storage "github.com/xymorphic/morph/internal/state/core"
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
	return NewCommandWithIO(os.Stdin, output)
}

func NewCommandWithIO(input io.Reader, commandOutput io.Writer) *cli.Command {
	if input == nil {
		input = os.Stdin
	}
	if commandOutput == nil {
		commandOutput = io.Discard
	}
	if _, ok := input.(*bufio.Reader); !ok {
		input = bufio.NewReader(input)
	}
	return &cli.Command{
		Name:  "sandbox",
		Usage: "Set up and inspect command execution environments",
		Flags: []cli.Flag{
			morphcli.ProfileFlag(),
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
			newSetupCommand(input, commandOutput),
			newContractCommand(input, commandOutput),
			{
				Name:  "list",
				Usage: "List execution environments",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return listEnvironmentsTo(ctx, cmd, commandOutput)
				},
			},
			{
				Name:      "explain",
				Usage:     "Explain an execution environment",
				ArgsUsage: "<environment-id>",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return explainEnvironmentTo(ctx, cmd, commandOutput)
				},
			},
		},
		Action: func(_ context.Context, cmd *cli.Command) error { return cli.ShowSubcommandHelp(cmd) },
	}
}

func listEnvironmentsTo(ctx context.Context, cmd *cli.Command, writer io.Writer) error {
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
		return writeJSONTo(writer, items)
	}

	for _, item := range items {
		if _, err := fmt.Fprintf(
			writer,
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

func explainEnvironmentTo(ctx context.Context, cmd *cli.Command, writer io.Writer) error {
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
		return writeJSONTo(writer, details)
	}

	return writeDetailsTo(writer, details)
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
	return writeDetailsTo(output, details)
}

func writeDetailsTo(writer io.Writer, details execution.EnvironmentDetails) error {
	status := details.Status
	_, err := fmt.Fprintf(
		writer,
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
	return writeJSONTo(output, value)
}

func writeJSONTo(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
