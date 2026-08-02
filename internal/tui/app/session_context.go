package tui

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	rpcclient "github.com/xymorphic/morph/internal/rpc/client"
	"github.com/xymorphic/morph/pkg/str"
)

type sessionContextLoader interface {
	Status(context.Context, string) (rpcclient.ContextStatus, error)
}

type sessionContextLoadedMsg struct {
	Status rpcclient.ContextStatus
}

type sessionContextLoadFailedMsg struct{}

func loadSessionContextCmd(ctx context.Context, client sessionContextLoader, sessionID string) tea.Cmd {
	if client == nil {
		return nil
	}

	return func() tea.Msg {
		if ctx == nil {
			ctx = context.Background()
		}
		sessionIDValue := str.String(sessionID)
		status, err := client.Status(ctx, sessionIDValue.Trim())
		if err != nil {
			return sessionContextLoadFailedMsg{}
		}

		return sessionContextLoadedMsg{Status: status}
	}
}

func (m *model) refreshSessionContext(status rpcclient.ContextStatus) {
	m.applyAction(setSessionContextAction{Context: formatSessionContextUsage(status)})
}

func formatSessionContextUsage(status rpcclient.ContextStatus) string {
	used := max(status.Used, 0)
	total := max(status.Length, 0)
	if used == 0 && total == 0 {
		return ""
	}

	usedPct := status.UsedPct
	if usedPct <= 0 && total > 0 {
		usedPct = float64(used) / float64(total)
	}
	percent := int(math.Round(max(usedPct, 0) * 100))
	prefix := ""
	usageSourceValue := str.String(status.UsageSource)
	if usageSourceValue.Normalized() == "estimated" {
		prefix = "~"
	}

	return fmt.Sprintf(
		"%s%s used · %s%d%%",
		prefix,
		formatContextTokenCount(used),
		prefix,
		percent,
	)
}

func formatContextTokenCount(value int) string {
	value = max(value, 0)
	digits := strconv.Itoa(value)
	if len(digits) <= 3 {
		return digits
	}

	var out strings.Builder
	prefix := len(digits) % 3
	if prefix == 0 {
		prefix = 3
	}
	out.WriteString(digits[:prefix])
	for i := prefix; i < len(digits); i += 3 {
		out.WriteByte(',')
		out.WriteString(digits[i : i+3])
	}

	return out.String()
}
