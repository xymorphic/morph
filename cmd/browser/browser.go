package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	cli "github.com/urfave/cli/v3"

	browserdomain "github.com/xymorphic/morph/internal/browser"
	morphcli "github.com/xymorphic/morph/internal/cli"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/permissions"
	rpcclient "github.com/xymorphic/morph/internal/rpc/client"
	"github.com/xymorphic/morph/pkg/str"
)

var (
	browserOutput io.Writer = os.Stdout
	newClient               = func(ctx context.Context, cfg *config.Config) (browserClient, error) {
		return rpcclient.NewClient(ctx, rpcclient.OptionsWithConfigAuth(rpcclient.Options{
			Address: cfg.RPC.Address, Port: cfg.RPC.Port,
			PermissionSurface: permissions.SurfaceCLI, PermissionPreset: cfg.Permissions.EffectivePreset(),
			AuthServices: []string{"/morph.v1.BrowserService"},
		}, cfg))
	}
)

type browserClient interface {
	Close() error
	BrowserAPI() rpcclient.BrowserAPI
}

func SetOutput(output io.Writer) io.Writer {
	previous := browserOutput
	if output == nil {
		browserOutput = io.Discard
	} else {
		browserOutput = output
	}

	return previous
}

func NewCommand() *cli.Command {
	return &cli.Command{
		Name:  "browser",
		Usage: "Inspect and manage the daemon browser runtime",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "Print machine-readable JSON"},
		},
		Commands: []*cli.Command{
			newStatusCommand(),
			newProfilesCommand(),
			newSessionsCommand(),
			newStartCommand(),
			newStopCommand(),
			newConfigCommand(),
			newArtifactCommand(),
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			return cli.ShowSubcommandHelp(cmd)
		},
	}
}

func newArtifactCommand() *cli.Command {
	return &cli.Command{
		Name: "artifact", Usage: "Retrieve browser artifacts",
		Commands: []*cli.Command{newArtifactGetCommand()},
		Action: func(_ context.Context, cmd *cli.Command) error {
			return cli.ShowSubcommandHelp(cmd)
		},
	}
}

func newArtifactGetCommand() *cli.Command {
	return &cli.Command{
		Name: "get", Usage: "Save an artifact to a local file", ArgsUsage: "<handle>",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Required: true, Usage: "Destination file path"},
			&cli.StringFlag{Name: "owner-session", Value: defaultOwnerSessionID, Usage: "Owner session identity"},
			&cli.StringFlag{Name: "run-id", Usage: "Owner run identity"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			handle := str.String(cmd.Args().First()).Trim()
			if handle == "" {
				return errors.New("browser artifact handle is required")
			}
			api, closeClient, err := getBrowserAPI(ctx, cmd)
			if err != nil {
				return err
			}
			defer closeClient()
			content, err := api.ReadArtifact(ctx, handle, cmd.String("owner-session"), cmd.String("run-id"))
			if err != nil {
				return err
			}
			destination, err := browserdomain.ResolveArtifactExportPath(cmd.String("output"))
			if err != nil {
				return err
			}
			if err := browserdomain.WriteArtifactExport(destination, content.Data); err != nil {
				return err
			}
			if cmd.Bool("json") {
				return writeJSON(newArtifactGetResult(content.Artifact, destination))
			}

			_, err = fmt.Fprintf(
				browserOutput, "saved %s to %s (%d bytes, expires %s)\n",
				content.Artifact.Name, destination, content.Artifact.Size, formatTime(content.Artifact.ExpiresAt),
			)
			return err
		},
	}
}

type artifactGetResult struct {
	Handle    string                     `json:"handle"`
	Kind      browserdomain.ArtifactKind `json:"kind"`
	Name      string                     `json:"name"`
	MIMEType  string                     `json:"mime_type"`
	Size      int64                      `json:"size"`
	CreatedAt time.Time                  `json:"created_at"`
	ExpiresAt time.Time                  `json:"expires_at"`
	SavedTo   string                     `json:"saved_to"`
}

func newArtifactGetResult(artifact browserdomain.Artifact, destination string) artifactGetResult {
	return artifactGetResult{
		Handle: artifact.Handle, Kind: artifact.Kind, Name: artifact.Name, MIMEType: artifact.MIMEType,
		Size: artifact.Size, CreatedAt: artifact.CreatedAt, ExpiresAt: artifact.ExpiresAt, SavedTo: destination,
	}
}

func newStatusCommand() *cli.Command {
	return &cli.Command{
		Name: "status", Usage: "Show browser runtime status",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			api, closeClient, err := getBrowserAPI(ctx, cmd)
			if err != nil {
				return err
			}
			defer closeClient()
			statusValue, err := api.Status(ctx)
			if err != nil {
				return err
			}
			if cmd.Bool("json") {
				return writeJSON(statusValue)
			}

			_, err = fmt.Fprintf(
				browserOutput, "enabled: %t\nprofiles: %d\nsessions: %d\n",
				statusValue.Enabled, len(statusValue.Profiles), len(statusValue.Sessions),
			)
			return err
		},
	}
}

func newProfilesCommand() *cli.Command {
	return &cli.Command{
		Name: "profiles", Usage: "List configured browser profiles",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			api, closeClient, err := getBrowserAPI(ctx, cmd)
			if err != nil {
				return err
			}
			defer closeClient()
			profiles, err := api.Profiles(ctx)
			if err != nil {
				return err
			}
			if cmd.Bool("json") {
				return writeJSON(profiles)
			}

			writer := tabwriter.NewWriter(browserOutput, 0, 4, 2, ' ', 0)
			if _, err := fmt.Fprintln(writer, "NAME\tMODE\tDEFAULT\tAVAILABLE\tWARNING"); err != nil {
				return err
			}
			for _, profile := range profiles {
				if _, err := fmt.Fprintf(
					writer, "%s\t%s\t%t\t%t\t%s\n",
					profile.Name, profile.Mode, profile.Default, profile.Available, profile.Warning,
				); err != nil {
					return err
				}
			}

			return writer.Flush()
		},
	}
}

func newSessionsCommand() *cli.Command {
	return &cli.Command{
		Name: "sessions", Usage: "List browser sessions",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			api, closeClient, err := getBrowserAPI(ctx, cmd)
			if err != nil {
				return err
			}
			defer closeClient()
			sessions, err := api.Sessions(ctx)
			if err != nil {
				return err
			}
			if cmd.Bool("json") {
				return writeJSON(sessions)
			}

			writer := tabwriter.NewWriter(browserOutput, 0, 4, 2, ' ', 0)
			if _, err := fmt.Fprintln(writer, "ID\tPROFILE\tSTATE\tLAST ACTIVE\tERROR\tWARNING"); err != nil {
				return err
			}
			for _, session := range sessions {
				if _, err := fmt.Fprintf(
					writer, "%s\t%s\t%s\t%s\t%s\t%s\n", session.ID, session.Profile, session.State,
					formatTime(session.LastActive), session.Error, session.Warning,
				); err != nil {
					return err
				}
			}

			return writer.Flush()
		},
	}
}

func newStartCommand() *cli.Command {
	return &cli.Command{
		Name: "start", Usage: "Start a managed browser session", ArgsUsage: "[profile]",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "owner-session", Value: defaultOwnerSessionID, Usage: "Owner session identity"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			api, closeClient, err := getBrowserAPI(ctx, cmd)
			if err != nil {
				return err
			}
			defer closeClient()
			profileName := str.String(cmd.Args().First()).Trim()
			session, err := api.Start(ctx, profileName, cmd.String("owner-session"))
			if err != nil {
				return err
			}
			if cmd.Bool("json") {
				return writeJSON(session)
			}

			_, err = fmt.Fprintln(browserOutput, session.ID)
			if err == nil && session.Warning != "" {
				_, err = fmt.Fprintln(browserOutput, "WARNING:", session.Warning)
			}
			return err
		},
	}
}

func newStopCommand() *cli.Command {
	return &cli.Command{
		Name: "stop", Usage: "Stop a managed browser session", ArgsUsage: "<session-id>",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "owner-session", Value: defaultOwnerSessionID, Usage: "Owner session identity"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			id := str.String(cmd.Args().First()).Trim()
			if id == "" {
				return errors.New("browser session id is required")
			}
			api, closeClient, err := getBrowserAPI(ctx, cmd)
			if err != nil {
				return err
			}
			defer closeClient()
			session, err := api.Stop(ctx, id, cmd.String("owner-session"))
			if err != nil {
				return err
			}
			if cmd.Bool("json") {
				return writeJSON(session)
			}

			_, err = fmt.Fprintf(browserOutput, "%s %s\n", session.ID, session.State)
			return err
		},
	}
}

func newConfigCommand() *cli.Command {
	return &cli.Command{
		Name: "config", Usage: "Show effective browser configuration",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			api, closeClient, err := getBrowserAPI(ctx, cmd)
			if err != nil {
				return err
			}
			defer closeClient()
			configValue, err := api.EffectiveConfig(ctx)
			if err != nil {
				return err
			}
			if cmd.Bool("json") {
				return writeJSON(configValue)
			}

			_, err = fmt.Fprintf(
				browserOutput,
				"enabled: %t\ncapability: %t\ndefault profile: %s\nnetwork strict: %t\npermission preset: %s\nexecutable configured: %t\n",
				configValue.Enabled, configValue.CapabilityEnabled, configValue.DefaultProfile,
				configValue.NetworkStrict, configValue.PermissionPreset, configValue.ExecutableConfigured,
			)
			return err
		},
	}
}

const defaultOwnerSessionID = "browser-cli"

func getBrowserAPI(ctx context.Context, cmd *cli.Command) (rpcclient.BrowserAPI, func(), error) {
	cfg, _, err := morphcli.LoadConfig(cmd)
	if err != nil {
		return nil, func() {}, err
	}
	cfg.Normalize()
	client, err := newClient(ctx, cfg)
	if err != nil {
		return nil, func() {}, err
	}
	api := client.BrowserAPI()
	if api == nil {
		_ = client.Close()
		return nil, func() {}, errors.New("browser RPC client is unavailable")
	}

	return api, func() { _ = client.Close() }, nil
}

func writeJSON(value any) error {
	encoder := json.NewEncoder(browserOutput)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}

	return value.Local().Format(time.RFC3339)
}
