package session

import (
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	rpcclient "github.com/xymorphic/morph/internal/rpc/client"
	storage "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/internal/state/search"
)

const sessionTitleDisplayLimit = 40

func writeSessionList(sessions []storage.Session) error {
	return writeSessionOutput(sessionListToText(sessions))
}

func sessionListToText(sessions []storage.Session) string {
	if len(sessions) == 0 {
		return "No sessions found.\n"
	}

	var output strings.Builder
	table := tabwriter.NewWriter(&output, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(table, "ID\tTITLE\tSOURCE\tUPDATED")
	for _, session := range sessions {
		_, _ = fmt.Fprintf(
			table,
			"%s\t%s\t%s\t%s\n",
			getSessionDisplayText(session.ID),
			getSessionTitleDisplay(session.Title),
			getSessionDisplayText(session.Origin.Source),
			getSessionDisplayText(formatSessionTime(session.UpdatedAt)),
		)
	}
	_ = table.Flush()
	return output.String()
}

func writeCurrentSession(session storage.Session) error {
	var output strings.Builder
	appendSessionSection(&output, "Session")
	appendSessionField(&output, "ID", session.ID)
	appendSessionField(&output, "Title", session.Title)
	return writeSessionOutput(output.String())
}

func writeCompactionResult(result rpcclient.CompactSessionResult) error {
	var output strings.Builder
	appendSessionSection(&output, "Compaction")
	appendSessionField(&output, "Session ID", result.SessionID)
	appendSessionField(&output, "Source end offset", strconv.Itoa(result.SourceEndOffset))
	appendSessionField(&output, "Source messages", strconv.Itoa(result.SourceMessageCount))
	appendSessionField(&output, "Updated at", formatSessionTime(result.UpdatedAt))
	appendSessionField(&output, "Current context", strconv.Itoa(result.CurrentContextLength))
	appendSessionField(&output, "Total context", strconv.Itoa(result.TotalContextLength))
	return writeSessionOutput(output.String())
}

func writeRepairResult(result search.VectorRepairResult) error {
	var output strings.Builder
	appendSessionSection(&output, "Session repair")
	appendSessionField(&output, "Sessions scanned", strconv.Itoa(result.SessionsScanned))
	appendSessionField(&output, "Messages scanned", strconv.Itoa(result.MessagesScanned))
	appendSessionField(&output, "Rows scanned", strconv.Itoa(result.RowsScanned))
	appendSessionField(&output, "Missing rows", strconv.Itoa(result.MissingRows))
	appendSessionField(&output, "Stale rows", strconv.Itoa(result.StaleRows))
	appendSessionField(&output, "Unchanged rows", strconv.Itoa(result.UnchangedRows))
	appendSessionField(&output, "Rebuilt rows", strconv.Itoa(result.RebuiltRows))
	appendSessionField(&output, "Deleted sources", strconv.Itoa(result.DeletedSources))
	appendSessionField(&output, "Batches", strconv.Itoa(result.Batches))
	appendSessionField(&output, "Attempted sources", strconv.Itoa(result.AttemptedSources))
	appendSessionField(&output, "Recovered sources", strconv.Itoa(result.RecoveredSources))
	appendSessionField(&output, "Still failed sources", strconv.Itoa(result.StillFailedSources))
	return writeSessionOutput(output.String())
}

func writeSessionStatus(status rpcclient.ContextStatus) error {
	var output strings.Builder
	appendSessionSection(&output, "Session")
	appendSessionField(&output, "ID", status.SessionID)
	appendSessionField(&output, "Created at", formatSessionTime(status.CreatedAt))
	appendSessionField(&output, "Updated at", formatSessionTime(status.UpdatedAt))
	appendSessionField(&output, "Compaction status", status.CompactionStatus)

	appendSessionSection(&output, "Context")
	appendSessionField(&output, "Offset", strconv.Itoa(status.Offset))
	appendSessionField(&output, "Size", strconv.Itoa(status.Size))
	appendSessionField(&output, "Length", strconv.Itoa(status.Length))
	appendSessionField(&output, "Used", strconv.Itoa(status.Used))
	appendSessionField(&output, "Remaining", strconv.Itoa(status.Remaining))
	appendSessionField(&output, "Used percent", formatSessionPercentage(status.UsedPct))
	appendSessionField(&output, "Remaining percent", formatSessionPercentage(status.RemainingPct))
	return writeSessionOutput(output.String())
}

func appendSessionSection(output *strings.Builder, title string) {
	if output.Len() > 0 {
		output.WriteByte('\n')
	}
	output.WriteString(title)
	output.WriteByte('\n')
}

func appendSessionField(output *strings.Builder, label string, value string) {
	fmt.Fprintf(output, "  %-20s %s\n", label+":", getSessionDisplayText(value))
}

func writeSessionOutput(value string) error {
	_, err := fmt.Fprint(sessionOutput, value)
	return err
}

func getSessionTitleDisplay(title string) string {
	title = strings.Map(func(value rune) rune {
		if unicode.IsControl(value) {
			return ' '
		}

		return value
	}, title)
	display := strings.Join(strings.Fields(title), " ")
	if display == "" {
		return "-"
	}
	runes := []rune(display)
	if len(runes) <= sessionTitleDisplayLimit {
		return display
	}

	return string(runes[:sessionTitleDisplayLimit-3]) + "..."
}

func getSessionDisplayText(value string) string {
	if value == "" {
		return "-"
	}
	if strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\t") {
		return strconv.Quote(value)
	}

	return value
}

func formatSessionTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}

	return value.UTC().Format(time.RFC3339)
}

func formatSessionPercentage(value float64) string {
	return fmt.Sprintf("%.2f%%", value*100)
}
