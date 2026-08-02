package trace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/pmezard/go-difflib/difflib"
	cli "github.com/urfave/cli/v3"

	morphcli "github.com/xymorphic/morph/internal/cli"
	"github.com/xymorphic/morph/internal/datadir"
	"github.com/xymorphic/morph/internal/guardrails"
)

type unsafeEvidenceSummary struct {
	ID         string              `json:"id"`
	CapturedAt string              `json:"captured_at"`
	SessionID  string              `json:"session_id,omitempty"`
	RunID      string              `json:"run_id,omitempty"`
	Source     string              `json:"source"`
	Action     string              `json:"action"`
	Blocked    bool                `json:"blocked,omitempty"`
	Redacted   bool                `json:"redacted,omitempty"`
	Findings   []map[string]string `json:"findings,omitempty"`
}

type unsafeEvidenceChange struct {
	originalStart int
	originalEnd   int
	safeStart     int
	safeEnd       int
	column        int
	originalLines []string
	safeLines     []string
}

func newUnsafeCommand() *cli.Command {
	return &cli.Command{
		Name:  "unsafe",
		Usage: "Inspect retained plaintext safety evidence",
		Commands: []*cli.Command{
			newUnsafeListCommand(),
			newUnsafeShowCommand(),
			newUnsafePurgeCommand(),
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			return cli.ShowSubcommandHelp(cmd)
		},
	}
}

func newUnsafeListCommand() *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "List retained unsafe evidence without revealing original content",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			store, err := loadUnsafeEvidenceStore(cmd)
			if err != nil {
				return err
			}
			items, err := store.LoadUnsafeEvidence(ctx)
			if err != nil {
				return err
			}
			summaries := make([]unsafeEvidenceSummary, len(items))
			for index, item := range items {
				summaries[index] = unsafeEvidenceSummary{
					ID:         item.ID,
					CapturedAt: item.CapturedAt.Format(time.RFC3339Nano),
					SessionID:  item.SessionID,
					RunID:      item.RunID,
					Source:     item.Source,
					Action:     item.Action,
					Blocked:    item.Blocked,
					Redacted:   item.Redacted,
					Findings:   item.Findings,
				}
			}
			return writeUnsafeJSON(getCommandWriter(cmd), summaries)
		},
	}
}

func newUnsafeShowCommand() *cli.Command {
	return &cli.Command{
		Name:      "show",
		Usage:     "Reveal one retained unsafe evidence record",
		ArgsUsage: "<evidence-id>",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "Print the complete machine-readable record"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Args().Len() != 1 {
				return errors.New("unsafe evidence ID is required")
			}
			store, err := loadUnsafeEvidenceStore(cmd)
			if err != nil {
				return err
			}
			evidence, err := store.LoadUnsafeEvidenceByID(ctx, cmd.Args().First())
			if err != nil {
				return err
			}
			if cmd.Bool("json") {
				return writeUnsafeJSON(getCommandWriter(cmd), evidence)
			}
			return writeUnsafeEvidenceReport(getCommandWriter(cmd), evidence)
		},
	}
}

func newUnsafePurgeCommand() *cli.Command {
	return &cli.Command{
		Name:  "purge",
		Usage: "Delete all retained unsafe evidence",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "yes", Usage: "Confirm permanent deletion"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if !cmd.Bool("yes") {
				return errors.New("purging unsafe evidence is permanent; rerun with --yes to confirm")
			}
			store, err := loadUnsafeEvidenceStore(cmd)
			if err != nil {
				return err
			}
			removed, err := store.PurgeUnsafeEvidence(ctx)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(getCommandWriter(cmd), "Removed %d unsafe evidence records.\n", removed)
			return err
		},
	}
}

func loadUnsafeEvidenceStore(cmd *cli.Command) (*guardrails.FileUnsafeEvidenceStore, error) {
	if _, err := morphcli.ResolveConfigInputs(cmd); err != nil {
		return nil, err
	}
	return guardrails.NewFileUnsafeEvidenceStore(datadir.UnsafeEvidenceDir()), nil
}

func getCommandWriter(cmd *cli.Command) io.Writer {
	if cmd != nil {
		root := cmd.Root()
		if root.Writer != nil {
			return root.Writer
		}
	}
	return os.Stdout
}

func writeUnsafeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func writeUnsafeEvidenceReport(writer io.Writer, evidence guardrails.UnsafeEvidence) error {
	original, err := unsafeEvidenceText(evidence.Original)
	if err != nil {
		return err
	}
	safe, err := unsafeEvidenceText(evidence.Safe)
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintf(writer, "Unsafe evidence: %s\n", evidence.ID); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "Captured: %s\n", evidence.CapturedAt.Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "Source: %s\nAction: %s\n", evidence.Source, evidence.Action); err != nil {
		return err
	}
	if evidence.SessionID != "" {
		if _, err := fmt.Fprintf(writer, "Session: %s\n", evidence.SessionID); err != nil {
			return err
		}
	}
	if evidence.RunID != "" {
		if _, err := fmt.Fprintf(writer, "Run: %s\n", evidence.RunID); err != nil {
			return err
		}
	}
	if len(evidence.Findings) > 0 {
		if _, err := fmt.Fprintln(writer, "Findings:"); err != nil {
			return err
		}
		for _, finding := range evidence.Findings {
			if _, err := fmt.Fprintf(
				writer,
				"  - %s (category=%s, source=%s)\n",
				finding["id"],
				finding["category"],
				finding["source"],
			); err != nil {
				return err
			}
		}
	}

	changes := getUnsafeEvidenceChanges(original, safe)
	if len(changes) == 0 {
		if _, err := fmt.Fprintln(writer, "Changes: none"); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintln(writer, "Changes:"); err != nil {
			return err
		}
		for index, change := range changes {
			if err := writeUnsafeEvidenceChange(writer, index+1, change); err != nil {
				return err
			}
		}
	}

	_, err = fmt.Fprintf(
		writer,
		"\nFull record: morph trace unsafe show %s --json\n",
		evidence.ID,
	)
	return err
}

func unsafeEvidenceText(value any) (string, error) {
	if value == nil {
		return "", nil
	}
	if text, ok := value.(string); ok {
		return text, nil
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", fmt.Errorf("format unsafe evidence value: %w", err)
	}
	return string(raw), nil
}

func getUnsafeEvidenceChanges(original, safe string) []unsafeEvidenceChange {
	originalLines := strings.Split(original, "\n")
	safeLines := strings.Split(safe, "\n")
	matcher := difflib.NewMatcher(originalLines, safeLines)
	groups := matcher.GetGroupedOpCodes(0)
	changes := make([]unsafeEvidenceChange, 0, len(groups))

	for _, group := range groups {
		change := unsafeEvidenceChange{
			originalStart: -1,
			safeStart:     -1,
		}
		for _, operation := range group {
			if operation.Tag == 'e' {
				continue
			}
			if change.originalStart < 0 || operation.I1 < change.originalStart {
				change.originalStart = operation.I1
			}
			if change.safeStart < 0 || operation.J1 < change.safeStart {
				change.safeStart = operation.J1
			}
			if operation.I2 > change.originalEnd {
				change.originalEnd = operation.I2
			}
			if operation.J2 > change.safeEnd {
				change.safeEnd = operation.J2
			}
		}
		if change.originalStart < 0 || change.safeStart < 0 {
			continue
		}
		change.originalLines = originalLines[change.originalStart:change.originalEnd]
		change.safeLines = safeLines[change.safeStart:change.safeEnd]
		if len(change.originalLines) == 1 && len(change.safeLines) == 1 {
			change.column = getFirstChangedColumn(change.originalLines[0], change.safeLines[0])
		}
		changes = append(changes, change)
	}

	return changes
}

func writeUnsafeEvidenceChange(
	writer io.Writer,
	index int,
	change unsafeEvidenceChange,
) error {
	if _, err := fmt.Fprintf(
		writer,
		"  %d. original %s -> safe %s\n",
		index,
		formatUnsafeEvidenceLineRange(change.originalStart, change.originalEnd),
		formatUnsafeEvidenceLineRange(change.safeStart, change.safeEnd),
	); err != nil {
		return err
	}
	if change.column > 0 {
		if _, err := fmt.Fprintf(writer, "     column %d\n", change.column); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(
			writer,
			"     - %s\n",
			getUnsafeEvidenceLineExcerpt(change.originalLines[0], change.column),
		); err != nil {
			return err
		}
		_, err := fmt.Fprintf(
			writer,
			"     + %s\n",
			getUnsafeEvidenceLineExcerpt(change.safeLines[0], change.column),
		)
		return err
	}
	if err := writeUnsafeEvidencePreview(writer, "-", change.originalLines); err != nil {
		return err
	}
	return writeUnsafeEvidencePreview(writer, "+", change.safeLines)
}

func formatUnsafeEvidenceLineRange(start, end int) string {
	if start == end {
		return fmt.Sprintf("after line %d", start)
	}
	if end-start == 1 {
		return fmt.Sprintf("line %d", start+1)
	}
	return fmt.Sprintf("lines %d-%d", start+1, end)
}

func getFirstChangedColumn(original, safe string) int {
	originalRunes := []rune(original)
	safeRunes := []rune(safe)
	limit := min(len(originalRunes), len(safeRunes))
	for index := range limit {
		if originalRunes[index] != safeRunes[index] {
			return index + 1
		}
	}
	if len(originalRunes) != len(safeRunes) {
		return limit + 1
	}
	return 0
}

func getUnsafeEvidenceLineExcerpt(value string, column int) string {
	const before = 80
	const after = 160

	runes := []rune(value)
	index := max(0, column-1)
	start := max(0, index-before)
	end := min(len(runes), index+after)
	excerpt := string(runes[start:end])
	if start > 0 {
		excerpt = "…" + excerpt
	}
	if end < len(runes) {
		excerpt += "…"
	}
	return excerpt
}

func writeUnsafeEvidencePreview(writer io.Writer, prefix string, lines []string) error {
	const previewLimit = 4

	if len(lines) <= previewLimit {
		for _, line := range lines {
			if _, err := fmt.Fprintf(writer, "     %s %s\n", prefix, truncateUnsafeEvidenceLine(line)); err != nil {
				return err
			}
		}
		return nil
	}

	for _, line := range lines[:2] {
		if _, err := fmt.Fprintf(writer, "     %s %s\n", prefix, truncateUnsafeEvidenceLine(line)); err != nil {
			return err
		}
	}
	omitted := len(lines) - previewLimit
	label := "lines"
	if omitted == 1 {
		label = "line"
	}
	if _, err := fmt.Fprintf(writer, "       … %d %s omitted …\n", omitted, label); err != nil {
		return err
	}
	for _, line := range lines[len(lines)-2:] {
		if _, err := fmt.Fprintf(writer, "     %s %s\n", prefix, truncateUnsafeEvidenceLine(line)); err != nil {
			return err
		}
	}
	return nil
}

func truncateUnsafeEvidenceLine(value string) string {
	const limit = 240

	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit-1]) + "…"
}
