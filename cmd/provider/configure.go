package providercmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"

	cli "github.com/urfave/cli/v3"

	morphcli "github.com/wandxy/morph/internal/cli"
	"github.com/wandxy/morph/internal/cli/setup"
)

func NewProviderConfigureCommand(input io.Reader, output io.Writer) *cli.Command {
	if input == nil {
		input = os.Stdin
	}
	if output == nil {
		output = io.Discard
	}

	return &cli.Command{
		Name:      "configure",
		Usage:     "Configure the default model provider",
		ArgsUsage: "[provider]",
		Flags: []cli.Flag{
			morphcli.ProfileFlag(),
			&cli.StringFlag{Name: "provider", Usage: "Model provider to persist"},
			&cli.StringFlag{Name: "model", Usage: "Model ID to persist"},
			&cli.StringFlag{Name: "base-url", Usage: "Provider base URL"},
			&cli.StringFlag{Name: "api", Usage: "Provider API to persist"},
			&cli.StringFlag{Name: "api-key", Usage: "Provider API key to persist"},
			&cli.BoolFlag{Name: "pull", Usage: "Pull the selected local model when it is missing"},
			&cli.BoolFlag{Name: "pull-quiet", Usage: "Suppress local model pull progress output"},
			&cli.BoolFlag{Name: "refresh", Usage: "Refresh local model discovery cache"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
			defer stop()

			inputs, err := morphcli.ResolveConfigInputs(cmd)
			if err != nil {
				return err
			}

			result, err := setup.RunProvider(ctx, setup.ProviderOptions{
				Input:      input,
				Output:     output,
				EnvPath:    inputs.EnvPath,
				ConfigPath: inputs.ConfigPath,
				Provider:   getProviderArg(cmd),
				Model:      cmd.String("model"),
				BaseURL:    cmd.String("base-url"),
				API:        cmd.String("api"),
				APIKey:     cmd.String("api-key"),
				Pull:       cmd.Bool("pull"),
				PullQuiet:  cmd.Bool("pull-quiet"),
				Refresh:    cmd.Bool("refresh"),
			})
			if err != nil {
				return err
			}

			_, err = fmt.Fprintf(
				output,
				"Configured %s with model %s in %s\n",
				result.Provider,
				result.Model,
				result.ConfigPath,
			)
			return err
		},
	}
}

func getProviderArg(cmd *cli.Command) string {
	if provider := cmd.Args().First(); provider != "" {
		return provider
	}

	return cmd.String("provider")
}
