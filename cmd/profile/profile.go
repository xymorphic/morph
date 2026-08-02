package profilecmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	cli "github.com/urfave/cli/v3"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/profile"
	"github.com/xymorphic/morph/pkg/str"
)

var profileOutput io.Writer = os.Stdout

func SetOutput(w io.Writer) io.Writer {
	previous := profileOutput
	if w == nil {
		profileOutput = io.Discard
		return previous
	}
	profileOutput = w
	return previous
}

func NewCommand() *cli.Command {
	return &cli.Command{
		Name:  "profile",
		Usage: "Manage Morph profiles",
		Commands: []*cli.Command{
			newUseCommand(),
			newListCommand(),
			newCurrentCommand(),
			newInitCommand(),
			newPathCommand(),
			newDoctorCommand(),
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			return cli.ShowSubcommandHelp(cmd)
		},
	}
}

func newUseCommand() *cli.Command {
	return &cli.Command{
		Name:      "use",
		Usage:     "Set the machine-local current profile",
		ArgsUsage: "<name>",
		Action: func(_ context.Context, cmd *cli.Command) error {
			firstValue := str.String(cmd.Args().First())
			name := firstValue.Trim()
			if name == "" {
				return fmt.Errorf("profile name is required")
			}

			resolved, err := profile.Resolve(profile.ResolveOptions{Name: name})
			if err != nil {
				return err
			}
			if !pathExists(resolved.HomeDir) {
				return fmt.Errorf("profile %q does not exist; run `morph profile init %s` first", resolved.Name, resolved.Name)
			}

			name, err = profile.StoreCurrentName(resolved.Name, "")
			if err != nil {
				return err
			}

			_, err = fmt.Fprintln(profileOutput, name)
			return err
		},
	}
}

func newListCommand() *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "List existing profile directories",
		Action: func(_ context.Context, _ *cli.Command) error {
			names, err := profile.List("")
			if err != nil {
				return err
			}

			for _, name := range names {
				if _, err := fmt.Fprintln(profileOutput, name); err != nil {
					return err
				}
			}

			return nil
		},
	}
}

func newCurrentCommand() *cli.Command {
	return &cli.Command{
		Name:  "current",
		Usage: "Print the stored current profile",
		Action: func(_ context.Context, _ *cli.Command) error {
			name, ok, err := profile.LoadCurrentName("")
			if err != nil {
				return err
			}
			if !ok {
				name = profile.DefaultName
			}

			_, err = fmt.Fprintln(profileOutput, name)
			return err
		},
	}
}

func newInitCommand() *cli.Command {
	return &cli.Command{
		Name:      "init",
		Usage:     "Create a profile directory",
		ArgsUsage: "<name>",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "bare",
				Usage: "Create only the profile directory without config.yaml",
			},
			&cli.BoolFlag{
				Name:  "use",
				Usage: "Set the new profile as the machine-local current profile",
			},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			firstValue2 := str.String(cmd.Args().First())
			name := firstValue2.Trim()
			if name == "" {
				return fmt.Errorf("profile name is required")
			}

			resolved, err := profile.Init(name, "")
			if err != nil {
				return err
			}
			if !cmd.Bool("bare") {
				cfg := config.NewProfileConfig()
				cfg.Name = resolved.Name
				if err := config.SaveYAML(resolved.ConfigPath, cfg); err != nil {
					return getProfileInitConfigError(resolved, err)
				}
			}
			if cmd.Bool("use") {
				if _, err := profile.StoreCurrentName(resolved.Name, ""); err != nil {
					return err
				}
			}

			_, err = fmt.Fprintln(profileOutput, resolved.HomeDir)
			return err
		},
	}
}

func getProfileInitConfigError(resolved profile.Profile, err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "config file already exists") {
		return fmt.Errorf(
			"profile %q already exists at %s; run `morph profile use %s` to select it, or choose a different profile name",
			resolved.Name,
			resolved.HomeDir,
			resolved.Name,
		)
	}

	return err
}

func newPathCommand() *cli.Command {
	return &cli.Command{
		Name:      "path",
		Usage:     "Print a profile home path",
		ArgsUsage: "[name]",
		Action: func(_ context.Context, cmd *cli.Command) error {
			resolved, err := loadCommandProfile(cmd.Args().First())
			if err != nil {
				return err
			}

			_, err = fmt.Fprintln(profileOutput, resolved.HomeDir)
			return err
		},
	}
}

func newDoctorCommand() *cli.Command {
	return &cli.Command{
		Name:      "doctor",
		Usage:     "Print profile paths and file status",
		ArgsUsage: "[name]",
		Action: func(_ context.Context, cmd *cli.Command) error {
			resolved, err := loadCommandProfile(cmd.Args().First())
			if err != nil {
				return err
			}

			return writeProfileDoctor(profileOutput, resolved)
		},
	}
}

func writeProfileDoctor(out io.Writer, resolved profile.Profile) error {
	var output strings.Builder
	output.WriteString("Profile\n")
	appendProfileDoctorField(&output, "Name", resolved.Name)

	output.WriteString("\nPaths\n")
	appendProfileDoctorField(&output, "Home", resolved.HomeDir)
	appendProfileDoctorField(&output, "Config", resolved.ConfigPath)
	appendProfileDoctorField(&output, "Environment", resolved.EnvPath)
	appendProfileDoctorField(&output, "Runtime", resolved.RuntimePath)
	appendProfileDoctorField(&output, "PID", resolved.PIDPath)

	output.WriteString("\nStatus\n")
	appendProfileDoctorField(&output, "Home", formatPathStatus(resolved.HomeDir))
	appendProfileDoctorField(&output, "Config", formatPathStatus(resolved.ConfigPath))
	appendProfileDoctorField(&output, "Environment", formatPathStatus(resolved.EnvPath))
	appendProfileDoctorField(&output, "Runtime", formatPathStatus(resolved.RuntimePath))

	_, err := fmt.Fprint(out, output.String())
	return err
}

func appendProfileDoctorField(output *strings.Builder, label string, value string) {
	if value == "" {
		value = "-"
	}
	fmt.Fprintf(output, "  %-13s %s\n", label+":", value)
}

func formatPathStatus(path string) string {
	if pathExists(path) {
		return "present"
	}

	return "missing"
}

func loadCommandProfile(name string) (profile.Profile, error) {
	nameValue := str.String(name)
	name = nameValue.Trim()
	if name != "" {
		return profile.Resolve(profile.ResolveOptions{Name: name})
	}

	return loadActiveProfile()
}

func loadActiveProfile() (profile.Profile, error) {
	active := profile.WithMetadataPaths(profile.Active())
	homeDirValue := str.String(active.HomeDir)
	if homeDirValue.Trim() != "" {
		return active, nil
	}

	resolved, err := profile.Resolve(profile.ResolveOptions{})
	if err != nil {
		return profile.Profile{}, err
	}
	profile.SetActive(resolved)

	return resolved, nil
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
