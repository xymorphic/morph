package session

import (
	"context"
	"fmt"
	"io"
	"os"

	cli "github.com/urfave/cli/v3"

	morphcli "github.com/wandxy/morph/internal/cli"
	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/permissions"
	rpcclient "github.com/wandxy/morph/internal/rpc/client"
	"github.com/wandxy/morph/internal/runtime"
	"github.com/wandxy/morph/pkg/logutils"
	"github.com/wandxy/morph/pkg/str"
)

var (
	sessionOutput io.Writer = os.Stdout
	newClient               = func(ctx context.Context, cfg *config.Config) (sessionClient, error) {
		return rpcclient.NewClient(ctx, rpcclient.OptionsWithConfigAuth(rpcclient.Options{
			Address:           cfg.RPC.Address,
			Port:              cfg.RPC.Port,
			PermissionSurface: permissions.SurfaceCLI,
			PermissionPreset:  cfg.Permissions.EffectivePreset(),
			AuthServices:      []string{"/morph.v1.SessionService"},
		}, cfg))
	}
)

type sessionClient interface {
	Close() error
	SessionAPI() rpcclient.SessionAPI
}

func SetOutput(w io.Writer) io.Writer {
	previous := sessionOutput
	if w == nil {
		sessionOutput = io.Discard
		return previous
	}
	sessionOutput = w
	return previous
}

func NewCommand() *cli.Command {
	return &cli.Command{
		Name:  "session",
		Usage: "Manage persisted chat sessions over RPC",
		Commands: []*cli.Command{
			{
				Name:      "new",
				Usage:     "Create a new session",
				ArgsUsage: "<session-id>",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					client, err := getSessionClient(ctx, cmd)
					if err != nil {
						return err
					}
					defer func() { _ = client.Close() }()
					sessions := client.SessionAPI()

					autoSwitch := false
					firstValue := str.String(cmd.Args().First())
					session, err := sessions.CreateWithOptions(ctx, rpcclient.CreateSessionOptions{
						ID:         firstValue.Trim(),
						AutoSwitch: &autoSwitch,
					})
					if err != nil {
						return err
					}
					_, err = fmt.Fprintln(sessionOutput, session.ID)
					return err
				},
			},
			{
				Name:  "list",
				Usage: "List persisted sessions",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "source", Usage: "Filter by session origin source"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					client, err := getSessionClient(ctx, cmd)
					if err != nil {
						return err
					}
					defer func() { _ = client.Close() }()
					sessionClient := client.SessionAPI()

					active := false
					source := str.String(cmd.String("source"))
					sessions, err := sessionClient.List(ctx, rpcclient.SessionListOptions{
						Archived:     &active,
						OriginSource: source.Trim(),
					})
					if err != nil {
						return err
					}
					return writeSessionList(sessions)
				},
			},
			{
				Name:      "use",
				Usage:     "Mark a session as the current session",
				ArgsUsage: "<session-id>",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					client, err := getSessionClient(ctx, cmd)
					if err != nil {
						return err
					}
					defer func() { _ = client.Close() }()
					sessions := client.SessionAPI()
					firstValue2 := str.String(cmd.Args().First())
					id := firstValue2.Trim()
					if err := sessions.Use(ctx, id); err != nil {
						return err
					}
					_, err = fmt.Fprintln(sessionOutput, id)
					return err
				},
			},
			{
				Name:      "unarchive",
				Usage:     "Restore an archived session",
				ArgsUsage: "<session-id>",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					client, err := getSessionClient(ctx, cmd)
					if err != nil {
						return err
					}
					defer func() { _ = client.Close() }()
					sessions := client.SessionAPI()
					firstValue3 := str.String(cmd.Args().First())
					session, err := sessions.Unarchive(ctx, firstValue3.Trim())
					if err != nil {
						return err
					}
					_, err = fmt.Fprintln(sessionOutput, session.ID)
					return err
				},
			},
			{
				Name:  "current",
				Usage: "Show the current session selection",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					client, err := getSessionClient(ctx, cmd)
					if err != nil {
						return err
					}
					defer func() { _ = client.Close() }()
					sessions := client.SessionAPI()

					session, err := sessions.Current(ctx)
					if err != nil {
						return err
					}
					return writeCurrentSession(session)
				},
			},
			{
				Name:      "compact",
				Usage:     "Force summary compaction for a session",
				ArgsUsage: "[session-id]",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					client, err := getSessionClient(ctx, cmd)
					if err != nil {
						return err
					}
					defer func() { _ = client.Close() }()
					sessions := client.SessionAPI()
					firstValue4 := str.String(cmd.Args().First())
					result, err := sessions.Compact(ctx, firstValue4.Trim())
					if err != nil {
						return err
					}

					return writeCompactionResult(result)
				},
			},
			{
				Name:      "repair",
				Usage:     "Repair session storage artifacts",
				ArgsUsage: "[session-id]",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "full",
						Usage: "Rebuild all repairable artifacts instead of only missing or stale artifacts",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					client, err := getSessionClient(ctx, cmd)
					if err != nil {
						return err
					}
					defer func() { _ = client.Close() }()
					sessions := client.SessionAPI()
					firstValue5 := str.String(cmd.Args().First())
					result, err := sessions.Repair(
						ctx,
						rpcclient.RepairSessionOptions{
							SessionID: firstValue5.Trim(),
							Full:      cmd.Bool("full"),
						},
					)
					if err != nil {
						return err
					}

					return writeRepairResult(result)
				},
			},
			{
				Name:      "status",
				Usage:     "Show session context usage",
				ArgsUsage: "[session-id]",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					client, err := getSessionClient(ctx, cmd)
					if err != nil {
						return err
					}
					defer func() { _ = client.Close() }()
					sessions := client.SessionAPI()
					firstValue6 := str.String(cmd.Args().First())
					result, err := sessions.Status(ctx, firstValue6.Trim())
					if err != nil {
						return err
					}

					return writeSessionStatus(result)
				},
			},
		},
	}
}

func getSessionClient(ctx context.Context, cmd *cli.Command) (sessionClient, error) {
	cfg, inputs, err := morphcli.LoadConfig(cmd)
	if err != nil {
		return nil, err
	}

	morphcli.ApplyConfigOverrides(cmd, cfg)
	morphcli.AddStartupFilesystemRoots(cfg, inputs)

	endpoint, err := runtime.ResolveRPC(ctx, cmd, cfg)
	if err != nil {
		return nil, err
	}
	cfg.RPC = endpoint

	config.Set(cfg)
	_ = logutils.ConfigureLogger("morph", cfg.Log.NoColor)
	logutils.SetLogLevel(cfg.Log.Level)

	return newClient(ctx, cfg)
}
