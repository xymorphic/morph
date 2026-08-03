package configcmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	cli "github.com/urfave/cli/v3"

	morphcli "github.com/xymorphic/morph/internal/cli"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/fileedit"
	"github.com/xymorphic/morph/pkg/str"
)

var runEditor = fileedit.RunEditor

func NewCommand(output io.Writer) *cli.Command {
	return NewCommandWithIO(os.Stdin, output)
}

func NewCommandWithIO(input io.Reader, output io.Writer) *cli.Command {
	if input == nil {
		input = strings.NewReader("")
	}
	if output == nil {
		output = io.Discard
	}
	if _, ok := input.(*bufio.Reader); !ok {
		input = bufio.NewReader(input)
	}

	return &cli.Command{
		Name:  "config",
		Usage: "Manage profile configuration",
		Commands: []*cli.Command{
			newGetCommand(output),
			newSetCommand(output),
			newEditCommand(input, output),
		},
	}
}

func newEditCommand(input io.Reader, output io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "edit",
		Usage: "Edit and validate the selected profile config",
		Flags: []cli.Flag{
			morphcli.ProfileFlag(),
			&cli.StringFlag{Name: "editor", Usage: "Editor command; defaults to VISUAL, EDITOR, or the platform editor"},
			&cli.BoolFlag{Name: "no-retry", Usage: "Do not offer to reopen an invalid candidate"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			inputs, err := resolveKnownConfigInputs(cmd)
			if err != nil {
				return err
			}
			if err := config.PreloadEnvFile(inputs.EnvPath); err != nil {
				return err
			}

			defaultData, err := config.NewProfileConfig().ToYAML()
			if err != nil {
				return err
			}
			result, err := fileedit.EditFile(ctx, fileedit.EditOptions{
				Path:        inputs.ConfigPath,
				DefaultData: defaultData,
				Editor:      cmd.String("editor"),
				RunEditor:   runEditor,
				Validate: func(candidatePath string) error {
					_, loadErr := config.LoadStrict(inputs.EnvPath, candidatePath)
					return loadErr
				},
				Retry: func(validationErr error, candidatePath string) bool {
					if cmd.Bool("no-retry") {
						return false
					}
					return promptRetryEdit(input, output, validationErr, candidatePath)
				},
			})
			if err != nil {
				if result.CandidatePath != "" {
					return fmt.Errorf("%w; candidate preserved at %s", err, result.CandidatePath)
				}
				return err
			}
			if !result.Changed {
				_, err = fmt.Fprintln(output, "Configuration unchanged")
				return err
			}
			_, err = fmt.Fprintf(output, "Updated %s\n", inputs.ConfigPath)
			return err
		},
	}
}

func promptRetryEdit(input io.Reader, output io.Writer, validationErr error, candidatePath string) bool {
	if _, err := fmt.Fprintf(
		output,
		"Validation failed: %v\nCandidate: %s\nReopen candidate? [y/N] ",
		validationErr,
		candidatePath,
	); err != nil {
		return false
	}

	reader, ok := input.(*bufio.Reader)
	if !ok {
		reader = bufio.NewReader(input)
	}
	answer, err := reader.ReadString('\n')
	if err != nil && len(answer) == 0 {
		return false
	}
	answer = strings.TrimSpace(strings.ToLower(answer))
	return answer == "y" || answer == "yes"
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
