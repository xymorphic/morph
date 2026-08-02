package daemon

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	urfavecli "github.com/urfave/cli/v3"

	morphcli "github.com/xymorphic/morph/internal/cli"
	"github.com/xymorphic/morph/pkg/str"
)

var daemonOutput io.Writer = os.Stdout
var getDaemonStatus = morphcli.GetDaemonStatus

func SetOutput(w io.Writer) io.Writer {
	previous := daemonOutput
	if w == nil {
		daemonOutput = io.Discard
	} else {
		daemonOutput = w
	}
	morphcli.SetDaemonOutput(daemonOutput)
	return previous
}

func NewCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:     "daemon",
		Usage:    "Start the Morph daemon",
		Commands: []*urfavecli.Command{newStatusCommand()},
		Flags:    []urfavecli.Flag{morphcli.PersistentInstructFlag()},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if cmd.Args().Len() > 0 {
				return fmt.Errorf("unknown daemon command %q", cmd.Args().First())
			}

			return morphcli.RunDaemonWithConfigRestarts(ctx, cmd)
		},
	}
}

func newStatusCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "status",
		Usage: "Show daemon health and connection status",
		Action: func(ctx context.Context, _ *urfavecli.Command) error {
			status, err := getDaemonStatus(ctx)
			if writeErr := writeDaemonStatus(daemonOutput, status); writeErr != nil {
				return writeErr
			}
			return err
		},
	}
}

func writeDaemonStatus(out io.Writer, status morphcli.DaemonStatus) error {
	stateValue := str.String(status.State)
	if stateValue.Trim() == "" {
		status.State = "unknown"
	}

	var output strings.Builder
	output.WriteString("Daemon\n")
	appendDaemonStatusField(&output, "State", status.State)
	appendDaemonStatusField(&output, "Health", formatStatusValue(status.Health))
	appendDaemonStatusField(&output, "Profile", formatStatusValue(status.Profile))
	appendDaemonStatusField(&output, "PID", fmt.Sprint(status.PID))
	appendDaemonStatusField(&output, "RPC", formatDaemonRPC(status.Address, status.Port))
	appendDaemonStatusField(&output, "Uptime", status.Uptime.String())
	appendDaemonStatusField(&output, "Started at", formatStatusTime(status.StartedAt))

	_, err := fmt.Fprint(out, output.String())
	return err
}

func appendDaemonStatusField(output *strings.Builder, label string, value string) {
	fmt.Fprintf(output, "  %-12s %s\n", label+":", value)
}

func formatDaemonRPC(address string, port int) string {
	address = formatStatusValue(address)
	if address == "-" && port == 0 {
		return "-"
	}

	return fmt.Sprintf("%s:%d", address, port)
}

func formatStatusValue(value string) string {
	valueText := str.String(value).Trim()
	if valueText == "" {
		return "-"
	}

	return valueText
}

func formatStatusTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}

	return value.Format("2006-01-02T15:04:05Z07:00")
}
