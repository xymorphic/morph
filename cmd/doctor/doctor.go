package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	cli "github.com/urfave/cli/v3"

	morphcli "github.com/xymorphic/morph/internal/cli"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/diagnostics"
	"github.com/xymorphic/morph/internal/diagnostics/readiness"
	"github.com/xymorphic/morph/pkg/str"
)

var doctorOutput io.Writer = os.Stdout

var doctorOutputWidth = func() int {
	width, _, err := term.GetSize(os.Stdout.Fd())
	if err != nil {
		return 0
	}

	return width
}

func SetOutput(w io.Writer) io.Writer {
	previous := doctorOutput
	if w == nil {
		doctorOutput = io.Discard
		return previous
	}
	doctorOutput = w
	return previous
}

const (
	colorReset  = "\x1b[0m"
	colorGray   = "\x1b[90m"
	colorGreen  = "\x1b[32m"
	colorYellow = "\x1b[33m"
	colorRed    = "\x1b[31m"
	colorWhite  = "\x1b[97m"
)

func NewCommand() *cli.Command {
	return &cli.Command{
		Name:  "doctor",
		Usage: "Run startup diagnostics",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "Print diagnostics and readiness as JSON"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, inputs, err := morphcli.LoadConfig(cmd)
			if err == nil {
				morphcli.ApplyConfigOverrides(cmd, cfg)
				morphcli.AddStartupFilesystemRoots(cfg, inputs)
			}

			report := diagnostics.Build(inputs.EnvPath, inputs.ConfigPath, cfg, err)
			safety := ""
			if cfg != nil {
				safety = morphcli.SafetySummary(cfg)
			}
			var readinessReport readiness.Report
			if cfg != nil {
				readinessReport = readiness.Build(ctx, readiness.Options{
					Config:     cfg,
					Profile:    inputs.Profile,
					EnvPath:    inputs.EnvPath,
					ConfigPath: inputs.ConfigPath,
				})
			}
			if cmd.Bool("json") {
				if err := renderJSONReport(doctorOutput, report, readinessReport, safety); err != nil {
					return err
				}

				return doctorError(report, readinessReport)
			}

			if err := renderDoctorReport(doctorOutput, report, readinessReport, cfg); err != nil {
				return err
			}
			if err := doctorError(report, readinessReport); err != nil {
				return err
			}

			_, err = fmt.Fprintln(doctorOutput, "\n[OK] doctor checks passed")
			return err
		},
	}
}

type jsonReport struct {
	OK      bool        `json:"ok"`
	Summary string      `json:"summary"`
	Safety  string      `json:"safety,omitempty"`
	Groups  []jsonGroup `json:"groups,omitempty"`
}

type jsonGroup struct {
	Name   string      `json:"name"`
	Checks []jsonCheck `json:"checks"`
}

type jsonCheck struct {
	Name    string       `json:"name"`
	Status  string       `json:"status"`
	Message string       `json:"message"`
	Actions []jsonAction `json:"actions,omitempty"`
}

type jsonAction struct {
	Command     string `json:"command"`
	Description string `json:"description,omitempty"`
}

type CheckFailedError struct {
	Summary string
}

func (err CheckFailedError) Error() string {
	return "doctor checks failed: " + err.Summary
}

func IsCheckFailed(err error) bool {
	var checkErr CheckFailedError
	return errors.As(err, &checkErr)
}

func renderJSONReport(w io.Writer, diagnosticsReport diagnostics.Report, readinessReport readiness.Report, safety string) error {
	safetyValue := str.String(safety)
	payload := jsonReport{
		OK:      !diagnosticsReport.HasFailures() && !readinessReport.HasFailures(),
		Summary: getDoctorSummary(diagnosticsReport, readinessReport),
		Safety:  safetyValue.Trim(),
		Groups:  doctorGroupsToJSON(diagnosticsReport, readinessReport),
	}

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(payload)
}

func diagnosticsChecksToJSON(checks []diagnostics.Check) []jsonCheck {
	items := make([]jsonCheck, 0, len(checks))
	for _, check := range checks {
		items = append(items, diagnosticCheckToJSON(check))
	}

	return items
}

func doctorGroupsToJSON(diagnosticsReport diagnostics.Report, readinessReport readiness.Report) []jsonGroup {
	profileDiagnostics := getRenderableDiagnosticChecks(diagnosticsReport)
	if len(readinessReport.Groups) == 0 {
		if len(profileDiagnostics) == 0 {
			return nil
		}

		return []jsonGroup{{
			Name:   "config",
			Checks: diagnosticsChecksToJSON(profileDiagnostics),
		}}
	}

	return readinessGroupsToJSON(readinessReport.Groups, profileDiagnostics)
}

func readinessGroupsToJSON(groups []readiness.Group, profileDiagnostics []diagnostics.Check) []jsonGroup {
	items := make([]jsonGroup, 0, len(groups))
	for _, group := range groups {
		item := jsonGroup{
			Name:   group.Name,
			Checks: make([]jsonCheck, 0, len(group.Checks)),
		}
		for _, check := range group.Checks {
			item.Checks = append(item.Checks, readinessCheckToJSON(check))
			if group.Name == "profile" && check.Name == "env" {
				item.Checks = append(item.Checks, diagnosticsChecksToJSON(profileDiagnostics)...)
			}
		}
		items = append(items, item)
	}

	return items
}

func diagnosticCheckToJSON(check diagnostics.Check) jsonCheck {
	return jsonCheck{
		Name:    check.Name,
		Status:  string(check.Status),
		Message: check.Message,
	}
}

func readinessCheckToJSON(check readiness.Check) jsonCheck {
	return jsonCheck{
		Name:    check.Name,
		Status:  string(check.Status),
		Message: check.Message,
		Actions: readinessActionsToJSON(check.Actions),
	}
}

func readinessActionsToJSON(actions []readiness.Action) []jsonAction {
	items := make([]jsonAction, 0, len(actions))
	for _, action := range actions {
		items = append(items, jsonAction{
			Command:     action.Command,
			Description: action.Description,
		})
	}

	return items
}

func doctorError(diagnosticsReport diagnostics.Report, readinessReport readiness.Report) error {
	if diagnosticsReport.HasFailures() {
		return CheckFailedError{Summary: diagnosticsReport.Summary()}
	}
	if readinessReport.HasFailures() {
		return CheckFailedError{Summary: readinessReport.Summary()}
	}

	return nil
}

func getDoctorSummary(diagnosticsReport diagnostics.Report, readinessReport readiness.Report) string {
	if diagnosticsReport.HasFailures() {
		return diagnosticsReport.Summary()
	}
	if readinessReport.HasFailures() {
		return readinessReport.Summary()
	}

	return "doctor checks passed"
}

func renderDiagnosticsReport(w io.Writer, report diagnostics.Report, cfg *config.Config) error {
	checks := getRenderableDiagnosticChecks(report)
	if len(checks) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w, "config:"); err != nil {
		return err
	}
	for _, check := range checks {
		if err := renderCheckLine(w, formatStatus(check.Status, cfg), check.Name, check.Message, cfg); err != nil {
			return err
		}
	}

	return nil
}

func getRenderableDiagnosticChecks(report diagnostics.Report) []diagnostics.Check {
	checks := make([]diagnostics.Check, 0, len(report.Checks))
	for _, check := range report.Checks {
		switch check.Name {
		case "config load", "config validation":
			checks = append(checks, check)
		}
	}

	return checks
}

func renderDoctorReport(
	w io.Writer,
	diagnosticsReport diagnostics.Report,
	readinessReport readiness.Report,
	cfg *config.Config,
) error {
	if len(readinessReport.Groups) == 0 {
		return renderDiagnosticsReport(w, diagnosticsReport, cfg)
	}

	return renderReadinessReportWithDiagnostics(
		w,
		readinessReport,
		getRenderableDiagnosticChecks(diagnosticsReport),
		cfg,
	)
}

func renderReadinessReport(w io.Writer, report readiness.Report, cfg *config.Config) error {
	return renderReadinessReportWithDiagnostics(w, report, nil, cfg)
}

func renderReadinessReportWithDiagnostics(
	w io.Writer,
	report readiness.Report,
	profileDiagnostics []diagnostics.Check,
	cfg *config.Config,
) error {
	for _, group := range report.Groups {
		if _, err := fmt.Fprintf(w, "\n%s:\n", group.Name); err != nil {
			return err
		}

		for _, check := range group.Checks {
			if err := renderCheckLine(w, formatStatus(check.Status, cfg), check.Name, check.Message, cfg); err != nil {
				return err
			}

			if group.Name == "profile" && check.Name == "env" {
				for _, diagnostic := range profileDiagnostics {
					if err := renderCheckLine(
						w,
						formatStatus(diagnostic.Status, cfg),
						diagnostic.Name,
						diagnostic.Message,
						cfg,
					); err != nil {
						return err
					}
				}
			}
			for _, action := range check.Actions {
				if _, err := fmt.Fprintf(w, "  fix: %s\n", formatAction(action, cfg)); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func renderCheckLine(w io.Writer, status string, name string, message string, cfg *config.Config) error {
	prefix := fmt.Sprintf("[%s] %s: ", status, name)
	continuation := strings.Repeat(" ", ansi.StringWidth(prefix))
	for _, line := range wrapCheckMessage(message, doctorOutputWidth(), prefix, continuation, cfg) {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}

	return nil
}

func wrapCheckMessage(message string, width int, firstPrefix string, restPrefix string, cfg *config.Config) []string {
	messageValue := str.String(message)
	message = messageValue.Trim()
	if message == "" {
		return []string{firstPrefix}
	}
	if width <= 0 {
		return []string{firstPrefix + message}
	}

	lines := make([]string, 0)
	for paragraphIndex, paragraph := range strings.Split(message, "\n") {
		currentPrefix := firstPrefix
		if paragraphIndex > 0 {
			currentPrefix = restPrefix
		}
		current := ""
		currentWidth := 0
		for _, word := range strings.Fields(paragraph) {
			available := max(1, width-ansi.StringWidth(currentPrefix))
			lines, current, currentWidth, currentPrefix = appendWrapWord(
				lines,
				current,
				currentWidth,
				currentPrefix,
				restPrefix,
				word,
				available,
			)
		}
		if current != "" {
			lines = append(lines, currentPrefix+current)
		}
	}
	if len(lines) == 0 {
		return []string{firstPrefix}
	}

	return lines
}

func appendWrapWord(
	lines []string,
	current string,
	currentWidth int,
	currentPrefix string,
	restPrefix string,
	word string,
	available int,
) ([]string, string, int, string) {
	wordWidth := ansi.StringWidth(word)
	if current == "" {
		return lines, word, wordWidth, currentPrefix
	}
	if currentWidth+1+wordWidth > available {
		lines = append(lines, currentPrefix+current)
		return lines, word, wordWidth, restPrefix
	}

	return lines, current + " " + word, currentWidth + 1 + wordWidth, currentPrefix
}

func formatAction(action readiness.Action, cfg *config.Config) string {
	commandValue := str.String(action.Command)
	command := commandValue.Trim()
	descriptionValue := str.String(action.Description)
	description := descriptionValue.Trim()
	if cfg != nil && cfg.Log.NoColor {
		command = "`" + command + "`"
		if description == "" {
			return command
		}

		return command + " - " + description
	}

	command = colorWhite + command + colorReset
	if description == "" {
		return command
	}

	return command + colorGray + " - " + description + colorReset
}

func formatStatus(status diagnostics.Status, cfg *config.Config) string {
	label := strings.ToUpper(string(status))
	if cfg != nil && cfg.Log.NoColor {
		return label
	}

	switch status {
	case diagnostics.StatusPass:
		return colorGreen + label + colorReset
	case diagnostics.StatusWarn:
		return colorYellow + label + colorReset
	case diagnostics.StatusFail:
		return colorRed + label + colorReset
	default:
		return label
	}
}
