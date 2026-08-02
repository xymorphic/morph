package gateway

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	cli "github.com/urfave/cli/v3"

	morphcli "github.com/xymorphic/morph/internal/cli"
	"github.com/xymorphic/morph/internal/config"
	telegramgateway "github.com/xymorphic/morph/internal/gateway/telegram"
	"github.com/xymorphic/morph/internal/permissions"
	rpcclient "github.com/xymorphic/morph/internal/rpc/client"
	"github.com/xymorphic/morph/internal/runtime"
	"github.com/xymorphic/morph/pkg/logutils"
	"github.com/xymorphic/morph/pkg/str"
)

const gatewayPairingNameDisplayLimit = 40

var (
	gatewayOutput io.Writer = os.Stdout
	newClient               = func(ctx context.Context, cfg *config.Config) (gatewayClient, error) {
		return rpcclient.NewClient(ctx, rpcclient.OptionsWithConfigAuth(rpcclient.Options{
			Address:           cfg.RPC.Address,
			Port:              cfg.RPC.Port,
			PermissionSurface: permissions.SurfaceCLI,
			PermissionPreset:  cfg.Permissions.EffectivePreset(),
			AuthServices:      []string{"/morph.v1.GatewayService"},
		}, cfg))
	}
	setTelegramWebhook = telegramgateway.SetWebhook
)

type gatewayClient interface {
	Close() error
	GatewayAPI() rpcclient.GatewayAPI
}

func SetOutput(w io.Writer) io.Writer {
	previous := gatewayOutput
	if w == nil {
		gatewayOutput = io.Discard
		return previous
	}
	gatewayOutput = w
	return previous
}

func NewCommand() *cli.Command {
	return &cli.Command{
		Name:  "gateway",
		Usage: "Manage external gateway integrations",
		Commands: []*cli.Command{
			newStatusCommand(),
			newStartCommand(),
			newStopCommand(),
			newRestartCommand(),
			newSetWebhookCommand(),
			{
				Name:  "pairing",
				Usage: "Manage gateway sender pairings",
				Commands: []*cli.Command{
					newPairingListCommand(),
					newPairingApproveCommand(),
					newPairingRevokeCommand(),
					newPairingClearPendingCommand(),
				},
			},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			return cli.ShowSubcommandHelp(cmd)
		},
	}
}

func newSetWebhookCommand() *cli.Command {
	return &cli.Command{
		Name:  "setwebhook",
		Usage: "Register gateway webhooks with providers",
		Commands: []*cli.Command{
			newSetTelegramWebhookCommand(),
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			return cli.ShowSubcommandHelp(cmd)
		},
	}
}

func newSetTelegramWebhookCommand() *cli.Command {
	return &cli.Command{
		Name:      "telegram",
		Usage:     "Register the Telegram webhook URL with Telegram",
		ArgsUsage: "[url]",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			firstValue := str.String(cmd.Args().First())
			url := firstValue.Trim()

			cfg, err := loadGatewayConfig(cmd)
			if err != nil {
				return err
			}

			if err := setTelegramWebhook(ctx, cfg.Gateway.Telegram, url); err != nil {
				return err
			}

			if url == "" {
				_, err = fmt.Fprintln(gatewayOutput, "telegram webhook unset")
				return err
			}
			_, err = fmt.Fprintf(gatewayOutput, "telegram webhook set url=%s\n", url)
			return err
		},
	}
}

func newStatusCommand() *cli.Command {
	return &cli.Command{
		Name:  "status",
		Usage: "Show daemon gateway runtime status",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			client, err := getGatewayClient(ctx, cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()

			status, err := client.GatewayAPI().GatewayStatus(ctx)
			if err != nil {
				return err
			}

			return writeGatewayStatus(gatewayOutput, status)
		},
	}
}

func newStartCommand() *cli.Command {
	return &cli.Command{
		Name:  "start",
		Usage: "Start the daemon gateway runtime",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runGatewayRuntimeCommand(ctx, cmd, func(api rpcclient.GatewayAPI) (rpcclient.GatewayStatus, error) {
				return api.Start(ctx)
			})
		},
	}
}

func newStopCommand() *cli.Command {
	return &cli.Command{
		Name:  "stop",
		Usage: "Stop the daemon gateway runtime",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runGatewayRuntimeCommand(ctx, cmd, func(api rpcclient.GatewayAPI) (rpcclient.GatewayStatus, error) {
				return api.Stop(ctx)
			})
		},
	}
}

func newRestartCommand() *cli.Command {
	return &cli.Command{
		Name:  "restart",
		Usage: "Restart the daemon gateway runtime",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runGatewayRuntimeCommand(ctx, cmd, func(api rpcclient.GatewayAPI) (rpcclient.GatewayStatus, error) {
				return api.Restart(ctx)
			})
		},
	}
}

func runGatewayRuntimeCommand(
	ctx context.Context,
	cmd *cli.Command,
	run func(rpcclient.GatewayAPI) (rpcclient.GatewayStatus, error),
) error {
	client, err := getGatewayClient(ctx, cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	status, err := run(client.GatewayAPI())
	if err != nil {
		return err
	}

	return writeGatewayStatus(gatewayOutput, status)
}

func writeGatewayStatus(out io.Writer, status rpcclient.GatewayStatus) error {
	var output strings.Builder
	output.WriteString("Gateway\n")
	appendGatewayStatusField(&output, "State", status.State)
	appendGatewayStatusField(&output, "Address", status.Address)
	appendGatewayStatusField(&output, "Port", strconv.Itoa(status.Port))
	appendGatewayStatusField(&output, "Telegram", status.TelegramMode)
	appendGatewayStatusField(&output, "Slack", status.SlackMode)
	appendGatewayStatusField(&output, "Last error", status.LastError)

	_, err := fmt.Fprint(out, output.String())
	return err
}

func appendGatewayStatusField(output *strings.Builder, label string, value string) {
	fmt.Fprintf(output, "  %-12s %s\n", label+":", getGatewayDisplayText(value))
}

func newPairingListCommand() *cli.Command {
	return &cli.Command{
		Name:      "list",
		Usage:     "List pending and approved gateway pairings",
		ArgsUsage: "[source]",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			client, err := getGatewayClient(ctx, cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()
			firstValue2 := str.String(cmd.Args().First())
			result, err := client.GatewayAPI().ListPairings(ctx, firstValue2.Trim())
			if err != nil {
				return err
			}

			return writePairingList(gatewayOutput, result)
		},
	}
}

func writePairingList(out io.Writer, result rpcclient.GatewayPairingList) error {
	writer := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "Pending"); err != nil {
		return err
	}
	if len(result.Pending) == 0 {
		if _, err := fmt.Fprintln(writer, "  None"); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintln(writer, "  SOURCE\tSENDER ID\tNAME\tEXPIRES"); err != nil {
			return err
		}

		for _, request := range result.Pending {
			if _, err := fmt.Fprintf(
				writer,
				"  %s\t%s\t%s\t%s\n",
				getGatewayDisplayText(request.Source),
				getGatewayDisplayText(request.SenderID),
				getGatewayPairingNameDisplay(request.DisplayName),
				formatGatewayTime(request.ExpiresAt),
			); err != nil {
				return err
			}
		}
	}
	if _, err := fmt.Fprintln(writer); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(writer, "Approved"); err != nil {
		return err
	}
	if len(result.Approved) == 0 {
		if _, err := fmt.Fprintln(writer, "  None"); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintln(writer, "  SOURCE\tSENDER ID\tNAME"); err != nil {
			return err
		}

		for _, sender := range result.Approved {
			if _, err := fmt.Fprintf(
				writer,
				"  %s\t%s\t%s\n",
				getGatewayDisplayText(sender.Source),
				getGatewayDisplayText(sender.SenderID),
				getGatewayPairingNameDisplay(sender.DisplayName),
			); err != nil {
				return err
			}
		}
	}

	return writer.Flush()
}

func getGatewayPairingNameDisplay(name string) string {
	name = strings.Map(func(value rune) rune {
		if unicode.IsControl(value) {
			return ' '
		}

		return value
	}, name)
	display := strings.Join(strings.Fields(name), " ")
	if display == "" {
		return "-"
	}
	runes := []rune(display)
	if len(runes) <= gatewayPairingNameDisplayLimit {
		return display
	}

	return string(runes[:gatewayPairingNameDisplayLimit-3]) + "..."
}

func getGatewayDisplayText(value string) string {
	if value == "" {
		return "-"
	}
	if strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\t") {
		return strconv.Quote(value)
	}

	return value
}

func formatGatewayTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}

	return value.UTC().Format(time.RFC3339)
}

func newPairingApproveCommand() *cli.Command {
	return &cli.Command{
		Name:      "approve",
		Usage:     "Approve a pending gateway sender pairing",
		ArgsUsage: "<source> <code>",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			getValue := str.String(cmd.Args().Get(0))
			source := getValue.Trim()
			getValue2 := str.String(cmd.Args().Get(1))
			code := getValue2.Trim()
			if source == "" || code == "" {
				return fmt.Errorf("source and code are required")
			}

			client, err := getGatewayClient(ctx, cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()

			sender, ok, err := client.GatewayAPI().ApprovePairing(ctx, source, code)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("no pending gateway pairing matched code")
			}

			_, err = fmt.Fprintf(gatewayOutput, "approved %s %s\n", sender.Source, sender.SenderID)
			return err
		},
	}
}

func newPairingRevokeCommand() *cli.Command {
	return &cli.Command{
		Name:      "revoke",
		Usage:     "Revoke an approved gateway sender pairing",
		ArgsUsage: "<source> <sender-id>",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			getValue3 := str.String(cmd.Args().Get(0))
			source := getValue3.Trim()
			getValue4 := str.String(cmd.Args().Get(1))
			senderID := getValue4.Trim()
			if source == "" || senderID == "" {
				return fmt.Errorf("source and sender id are required")
			}

			client, err := getGatewayClient(ctx, cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()
			if err := client.GatewayAPI().RevokePairing(ctx, source, senderID); err != nil {
				return err
			}

			_, err = fmt.Fprintf(gatewayOutput, "revoked %s %s\n", source, senderID)
			return err
		},
	}
}

func newPairingClearPendingCommand() *cli.Command {
	return &cli.Command{
		Name:      "clear-pending",
		Usage:     "Clear pending gateway pairing requests",
		ArgsUsage: "[source]",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			firstValue3 := str.String(cmd.Args().First())
			source := firstValue3.Trim()
			client, err := getGatewayClient(ctx, cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()
			if err := client.GatewayAPI().ClearPendingPairings(ctx, source); err != nil {
				return err
			}

			_, err = fmt.Fprintln(gatewayOutput, "cleared pending pairings")
			return err
		},
	}
}

func getGatewayClient(ctx context.Context, cmd *cli.Command) (gatewayClient, error) {
	cfg, err := loadGatewayConfig(cmd)
	if err != nil {
		return nil, err
	}

	endpoint, err := runtime.ResolveRPC(ctx, cmd, cfg)
	if err != nil {
		return nil, err
	}

	cfg.RPC = endpoint
	config.Set(cfg)

	return newClient(ctx, cfg)
}

func loadGatewayConfig(cmd *cli.Command) (*config.Config, error) {
	cfg, inputs, err := morphcli.LoadConfig(cmd)
	if err != nil {
		return nil, err
	}

	morphcli.ApplyConfigOverrides(cmd, cfg)
	morphcli.AddStartupFilesystemRoots(cfg, inputs)

	config.Set(cfg)

	_ = logutils.ConfigureLogger("morph", cfg.Log.NoColor)
	logutils.SetLogLevel(cfg.Log.Level)

	return cfg, nil
}
