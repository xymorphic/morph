package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	cli "github.com/urfave/cli/v3"

	authcmd "github.com/xymorphic/morph/cmd/auth"
	automationcmd "github.com/xymorphic/morph/cmd/automation"
	browsercmd "github.com/xymorphic/morph/cmd/browser"
	daemoncmd "github.com/xymorphic/morph/cmd/daemon"
	doctorcmd "github.com/xymorphic/morph/cmd/doctor"
	gatewaycmd "github.com/xymorphic/morph/cmd/gateway"
	configcmd "github.com/xymorphic/morph/cmd/morph/configcmd"
	permissionscmd "github.com/xymorphic/morph/cmd/permissions"
	profilecmd "github.com/xymorphic/morph/cmd/profile"
	providercmd "github.com/xymorphic/morph/cmd/provider"
	sandboxcmd "github.com/xymorphic/morph/cmd/sandbox"
	sessioncmd "github.com/xymorphic/morph/cmd/session"
	tracecmd "github.com/xymorphic/morph/cmd/trace"
	tuicmd "github.com/xymorphic/morph/cmd/tui"
	morphcli "github.com/xymorphic/morph/internal/cli"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/profile"
	"github.com/xymorphic/morph/pkg/logutils"
	"github.com/xymorphic/morph/pkg/str"
)

var log = logutils.Module("morph")

var (
	envFile                     = ".env"
	configFile                  = "config.yaml"
	rootOutput        io.Writer = os.Stdout
	runRootTUI                  = tuicmd.Run
	newRootChatAction           = morphcli.NewMainAction
)

const rootHelpTemplate = `MORPH_NAME:
   {{template "helpNameTemplate" .}}

USAGE:
   {{if .UsageText}}{{wrap .UsageText 3}}{{else}}{{.FullName}} {{if .VisibleFlags}}[global options]{{end}}{{if .VisibleCommands}} [command [command options]]{{end}}{{if .ArgsUsage}} {{.ArgsUsage}}{{else}}{{if .Arguments}} [arguments...]{{end}}{{end}}{{end}}{{if .Version}}{{if not .HideVersion}}

VERSION:
   {{.Version}}{{end}}{{end}}{{if .Description}}

DESCRIPTION:
   {{template "descriptionTemplate" .}}{{end}}
{{- if len .Authors}}

AUTHOR{{template "authorsTemplate" .}}{{end}}{{if .VisibleCommands}}

COMMANDS:{{template "visibleCommandCategoryTemplate" .}}{{end}}{{if .VisibleFlagCategories}}

GLOBAL OPTIONS:{{template "visibleFlagCategoryTemplate" .}}{{else if .VisibleFlags}}

GLOBAL OPTIONS:{{template "visibleFlagTemplate" .}}{{end}}

EXAMPLES:
   Start the interactive terminal UI:
      morph
      morph --profile work

   Start the daemon:
      morph daemon
      morph --profile work daemon
      morph profile use work
      morph --config ./config.yaml --trace.enabled daemon

   Chat with the agent:
      morph --chat "summarize the failing tests"
      morph -c --profile work "continue"
      morph --chat --session ses_abc123 --instruct "be brief" "continue from the last debugging step"
      MORPH_PROFILE=work morph session list

   Start the trace viewer:
      morph trace view
      morph --config ./config.yaml trace view --listen 127.0.0.1:9090
{{if .Copyright}}

COPYRIGHT:
   {{template "copyrightTemplate" .}}{{end}}
`

func main() {
	logutils.InitLogger("morph")

	if err := configureProfileDefaults(os.Args); err != nil {
		log.Fatal().Err(err).Msg("Failed to resolve profile")
	}

	envFile = getEnvFile(os.Args)
	if err := config.PreloadEnvFile(envFile); err != nil {
		log.Fatal().Err(err).Msg("Failed to preload environment")
	}

	cmd := newCommand()
	if err := cmd.Run(context.Background(), os.Args); err != nil {
		if exitErr, ok := errors.AsType[cli.ExitCoder](err); ok {
			os.Exit(exitErr.ExitCode())
		}
		if doctorcmd.IsCheckFailed(err) {
			os.Exit(1)
		}
		log.Fatal().Err(err).Msg("Failed to run")
	}
}

func newCommand() *cli.Command {
	cmd := &cli.Command{
		Name:                          "morph",
		Usage:                         "Run and manage your Morph daemon",
		Description:                   morphcli.AppDescription,
		Version:                       formatRootVersion(),
		CustomRootCommandHelpTemplate: rootHelpTemplate,
		Flags: append(
			morphcli.RootFlags(&envFile, &configFile),
			morphcli.ChatFlag(),
			morphcli.RequestInstructFlag()),
		Commands: []*cli.Command{
			automationcmd.NewCommand(),
			authcmd.NewCommand(),
			browsercmd.NewCommand(),
			newDatabaseCommand(),
			newVersionCommand(rootOutput),
			configcmd.NewCommand(rootOutput),
			doctorcmd.NewCommand(),
			gatewaycmd.NewCommand(),
			profilecmd.NewCommand(),
			providercmd.NewCommand(os.Stdin, rootOutput),
			permissionscmd.NewCommand(),
			sandboxcmd.NewCommand(),
			sessioncmd.NewCommand(),
			tracecmd.NewCommand(),
			daemoncmd.NewCommand(),
		},
		Action: newRootAction(),
	}

	return cmd
}

func newRootAction() func(context.Context, *cli.Command) error {
	chatAction := newRootChatAction(morphcli.MainActionOptions{
		Input:  os.Stdin,
		Output: rootOutput,
	})

	return func(ctx context.Context, cmd *cli.Command) error {
		if cmd.Bool("chat") {
			return chatAction(ctx, cmd)
		}
		if cmd.Args().Len() > 0 {
			return fmt.Errorf("unexpected root arguments %q; use --chat or -c to send a one-shot chat request",
				strings.Join(cmd.Args().Slice(), " "))
		}

		return runRootTUI(ctx, cmd)
	}
}

func getEnvFile(args []string) string {
	envValue := str.String(os.Getenv("MORPH_ENV_FILE"))
	if value := envValue.Trim(); value != "" {
		return value
	}

	for i := range args {
		argsValue := str.String(args[i])
		arg := argsValue.Trim()
		if arg == "--env-file" && i+1 < len(args) {
			argsValue2 := str.String(args[i+1])
			return argsValue2.Trim()
		}
		if value, ok := strings.CutPrefix(arg, "--env-file="); ok {
			valueText := str.String(value)
			return valueText.Trim()
		}
	}

	return envFile
}

func configureProfileDefaults(args []string) error {
	resolved, err := profile.Resolve(profile.ResolveOptions{Name: getProfileArg(args)})
	if err != nil {
		return err
	}

	profile.SetActive(resolved)
	envFile = resolved.EnvPath
	configFile = resolved.ConfigPath
	return nil
}

func getProfileArg(args []string) string {
	for i := range args {
		argsValue3 := str.String(args[i])
		arg := argsValue3.Trim()
		if arg == "--" {
			return ""
		}
		if (arg == "--profile" || arg == "-p") && i+1 < len(args) {
			argsValue4 := str.String(args[i+1])
			return argsValue4.Trim()
		}
		if value, ok := strings.CutPrefix(arg, "--profile="); ok {
			value2 := str.String(value)
			return value2.Trim()
		}
		if value, ok := strings.CutPrefix(arg, "-p="); ok {
			value3 := str.String(value)
			return value3.Trim()
		}
	}

	return ""
}
