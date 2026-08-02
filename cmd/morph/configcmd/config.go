package configcmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	cli "github.com/urfave/cli/v3"

	morphcli "github.com/xymorphic/morph/internal/cli"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/pkg/str"
)

func NewCommand(output io.Writer) *cli.Command {
	if output == nil {
		output = io.Discard
	}

	return &cli.Command{
		Name:  "config",
		Usage: "Manage profile configuration",
		Commands: []*cli.Command{
			newGetCommand(output),
			newSetCommand(output),
		},
	}
}

func newGetCommand(output io.Writer) *cli.Command {
	return &cli.Command{
		Name:      "get",
		Usage:     "Get values from the selected profile config",
		ArgsUsage: "<path>...",
		Flags:     []cli.Flag{morphcli.ProfileFlag()},
		Action: func(_ context.Context, cmd *cli.Command) error {
			paths, err := getConfigGetPaths(cmd)
			if err != nil {
				return err
			}

			inputs, err := resolveKnownConfigInputs(cmd)
			if err != nil {
				return err
			}
			if err := config.PreloadEnvFile(inputs.EnvPath); err != nil {
				return err
			}

			values, err := morphcli.GetConfigValues(inputs.EnvPath, inputs.ConfigPath, paths)
			if err != nil {
				return err
			}
			if len(values) == 1 {
				_, err = fmt.Fprintln(output, values[0].Value)
				return err
			}
			for _, value := range values {
				if _, err := fmt.Fprintf(output, "%s=%s\n", value.Path, value.Value); err != nil {
					return err
				}
			}

			return nil
		},
	}
}

func newSetCommand(output io.Writer) *cli.Command {
	return &cli.Command{
		Name:      "set",
		Usage:     "Set values in the selected profile config file",
		ArgsUsage: "<path> <value>|<path=value>...",
		Flags:     []cli.Flag{morphcli.ProfileFlag()},
		Action: func(_ context.Context, cmd *cli.Command) error {
			updates, err := getSetConfigUpdates(cmd)
			if err != nil {
				return err
			}

			inputs, err := resolveKnownConfigInputs(cmd)
			if err != nil {
				return err
			}
			if err := config.PreloadEnvFile(inputs.EnvPath); err != nil {
				return err
			}

			oldValues, err := morphcli.GetConfigValues(inputs.EnvPath, inputs.ConfigPath, getUpdatePaths(updates))
			if err != nil {
				return err
			}
			updatedPaths, err := morphcli.SetConfigValues(inputs.EnvPath, inputs.ConfigPath, updates)
			if err != nil {
				return err
			}

			if len(updatedPaths) == 1 {
				_, err = fmt.Fprintf(output, "%s (prev=%s)\n", updates[0].Value, oldValues[0].Value)
				return err
			}
			for index, updatedPath := range updatedPaths {
				if _, err := fmt.Fprintf(
					output,
					"%s=%s (prev=%s)\n",
					updatedPath,
					updates[index].Value,
					oldValues[index].Value,
				); err != nil {
					return err
				}
			}

			return nil
		},
	}
}

func getUpdatePaths(updates []morphcli.ConfigUpdate) []string {
	paths := make([]string, 0, len(updates))
	for _, update := range updates {
		paths = append(paths, update.Path)
	}

	return paths
}

func resolveKnownConfigInputs(cmd *cli.Command) (morphcli.ConfigInputs, error) {
	inputs, err := morphcli.ResolveConfigInputs(cmd)
	if err != nil {
		return morphcli.ConfigInputs{}, err
	}
	if !hasExplicitProfile(cmd) {
		return inputs, nil
	}

	info, err := os.Stat(inputs.Profile.HomeDir)
	if err != nil {
		if os.IsNotExist(err) {
			return morphcli.ConfigInputs{}, fmt.Errorf("unknown profile %q", inputs.Profile.Name)
		}

		return morphcli.ConfigInputs{}, fmt.Errorf("read profile %q: %w", inputs.Profile.Name, err)
	}
	if !info.IsDir() {
		return morphcli.ConfigInputs{}, fmt.Errorf("profile %q is not a directory", inputs.Profile.Name)
	}

	return inputs, nil
}

func hasExplicitProfile(cmd *cli.Command) bool {
	if cmd == nil {
		return false
	}

	for _, candidate := range cmd.Lineage() {
		if candidate.IsSet("profile") {
			return true
		}
	}

	return false
}

func getConfigGetPaths(cmd *cli.Command) ([]string, error) {
	if cmd == nil || cmd.Args().Len() == 0 {
		return nil, fmt.Errorf("config path is required")
	}

	paths := make([]string, 0, cmd.Args().Len())
	for _, arg := range cmd.Args().Slice() {
		argValue := str.String(arg)
		path := argValue.Trim()
		if path == "" {
			return nil, fmt.Errorf("config path is required")
		}
		paths = append(paths, path)
	}

	return paths, nil
}

func getSetConfigUpdates(cmd *cli.Command) ([]morphcli.ConfigUpdate, error) {
	if cmd == nil {
		return nil, fmt.Errorf("config path and value are required")
	}

	args := cmd.Args().Slice()
	updates := make([]morphcli.ConfigUpdate, 0, len(args))
	for index := 0; index < len(args); index++ {
		argsValue := str.String(args[index])
		raw := argsValue.Trim()
		if raw == "" {
			return nil, fmt.Errorf("config path and value are required")
		}

		path, value, ok := strings.Cut(raw, "=")
		if ok {
			pathValue := str.String(path)
			path = pathValue.Trim()
			if path == "" {
				return nil, fmt.Errorf("config path and value are required")
			}
			valueText := str.String(value)
			updates = append(updates, morphcli.ConfigUpdate{Path: path, Value: valueText.Trim()})
			continue
		}

		if index+1 >= len(args) {
			return nil, fmt.Errorf("config path and value are required")
		}
		rawValue := str.String(raw)
		path = rawValue.Trim()
		if path == "" {
			return nil, fmt.Errorf("config path and value are required")
		}
		argsValue2 := str.String(args[index+1])
		updates = append(updates, morphcli.ConfigUpdate{Path: path, Value: argsValue2.Trim()})
		index++
	}

	if len(updates) == 0 {
		return nil, fmt.Errorf("config path and value are required")
	}

	return updates, nil
}
