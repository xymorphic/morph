package daemon

import (
	"fmt"
	"strings"

	"github.com/wandxy/morph/internal/brand"
	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/constants"
	"github.com/wandxy/morph/internal/permissions"
	"github.com/wandxy/morph/internal/profile"
	"github.com/wandxy/morph/pkg/str"
)

const (
	AppDescription         = constants.AppDescription
	colorGray              = "\x1b[90m"
	colorReset             = "\x1b[0m"
	startupLogoColumnWidth = 64
	startupColumnGap       = 3
)

var startupLogoColors = []string{
	"\x1b[38;5;38m",
	"\x1b[38;5;44m",
	"\x1b[38;5;49m",
	"\x1b[38;5;48m",
	"\x1b[38;5;83m",
}

func renderStartupPanel(cfg *config.Config) string {
	badge := getStartupBadge()
	if cfg == nil {
		return badge
	}

	detailRows := getStartupDetailRows(cfg)
	panel := renderStartupBannerPanel(badge, detailRows, cfg.Log.NoColor)

	return "\n" + panel + "\n\n"
}

type startupDetailRow struct {
	label string
	value string
}

func getStartupDetailRows(cfg *config.Config) []startupDetailRow {
	logStyle := "color"
	debugRequests := "disabled"
	if cfg.Log.NoColor {
		logStyle = "plain"
	}

	if cfg.Debug.Requests {
		debugRequests = "enabled"
	}
	traceStatus := "disabled"
	if cfg.Trace.Enabled {
		dirValue := str.String(cfg.Trace.Disk.Dir)
		traceDir := dirValue.Trim()
		traceStatus = fmt.Sprintf("enabled (%s)", traceDir)
	}

	rows := []startupDetailRow{
		{label: "Version", value: formatStartupVersion()},
		{label: "Instance", value: cfg.Name},
		{label: "Profile", value: getStartupProfileName()},
		{label: "Model", value: cfg.Models.Main.Name},
		{label: "Provider", value: cfg.Models.Main.Provider},
		{label: "Summary model", value: cfg.SummaryModelEffective()},
		{label: "Summary provider", value: cfg.SummaryProviderEffective()},
		{label: "Storage", value: getEffectiveStorageBackend(cfg)},
		{label: "Permissions", value: getPermissionStartupSummary(cfg)},
	}
	if cfg.SummaryModelAPIEffective() != cfg.Models.Main.API {
		rows = append(rows, startupDetailRow{label: "Summary API", value: cfg.SummaryModelAPIEffective()})
	}
	rows = append(rows,
		startupDetailRow{label: "Streaming", value: fmt.Sprintf("%t", cfg.StreamEnabled())},
		startupDetailRow{label: "RPC", value: fmt.Sprintf("%s:%d", cfg.RPC.Address, cfg.RPC.Port)},
		startupDetailRow{label: "Gateway", value: getGatewayStartupSummary(cfg)},
		startupDetailRow{label: "Logs", value: fmt.Sprintf("%s (%s)", cfg.Log.Level, logStyle)},
		startupDetailRow{label: "Debug requests", value: debugRequests},
		startupDetailRow{label: "Traces", value: traceStatus},
		startupDetailRow{label: "Safety", value: daemonDependencies.safetySummary(cfg)},
	)
	if cfg.RetainUnsafeEnabled() {
		rows = append(rows, startupDetailRow{
			label: "Unsafe evidence",
			value: "PLAINTEXT; same-user/full-access readable; cleanup is manual",
		})
	}
	if cfg.Search.Vector.Enabled {
		rows = append(rows,
			startupDetailRow{label: "Embedding model", value: cfg.Models.Embedding.Name},
			startupDetailRow{label: "Embedding provider", value: cfg.ModelEmbeddingProviderEffective()},
			startupDetailRow{label: "Reranker", value: cfg.RerankerEffective()},
		)
	}

	return rows
}

func getPermissionStartupSummary(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}

	policy := cfg.Permissions
	policy.Normalize()
	if policy.EffectivePreset() == permissions.PresetFullAccess {
		return "full_access (UNSAFE: command and filesystem guardrails bypassed)"
	}

	return policy.Label()
}

func getStartupProfileName() string {
	nameValue := str.String(profile.Active().Name)
	name := nameValue.Trim()
	if name == "" {
		return profile.DefaultName
	}

	return name
}

func getGatewayStartupSummary(cfg *config.Config) string {
	if cfg == nil || !cfg.Gateway.Enabled {
		return "disabled"
	}

	parts := []string{fmt.Sprintf("%s:%d", cfg.Gateway.Address, cfg.Gateway.Port)}
	if cfg.Gateway.Telegram.Enabled {
		parts = append(parts, "telegram="+cfg.Gateway.Telegram.Mode)
	}
	if cfg.Gateway.Slack.Enabled {
		parts = append(parts, "slack="+cfg.Gateway.Slack.Mode)
	}

	return strings.Join(parts, " ")
}

func formatStartupVersion() string {
	appVersionValue := str.String(constants.AppVersion)
	version := appVersionValue.Trim()
	if version == "" {
		version = "dev"
	}
	commitHashValue := str.String(constants.CommitHash)
	commit := commitHashValue.Trim()
	if commit == "" {
		commit = "unknown"
	}

	return fmt.Sprintf("%s (commit %s)", version, commit)
}

func getStartupBadge() string {
	return joinStartupBanner(brand.Mark, getStartupBrandText())
}

func getStartupBrandText() string {
	return "Morph\n" + formatStartupVersion()
}

func renderStartupBannerPanel(logo string, rows []startupDetailRow, noColor bool) string {
	logoLines := splitStartupLines(logo)
	detailLines := renderStartupDetailLines(rows, noColor)
	height := max(len(logoLines), len(detailLines))
	logoLines = renderStartupLogoLines(logoLines, height, noColor)
	detailLines = padStartupBlockVertically(detailLines, height)

	lines := make([]string, 0, height)
	gap := strings.Repeat(" ", startupColumnGap)
	divider := styleStartup("│", noColor)
	for index := range height {
		lines = append(lines, logoLines[index]+gap+divider+gap+detailLines[index])
	}

	return strings.Join(lines, "\n")
}

func renderStartupLogoLines(lines []string, height int, noColor bool) []string {
	if len(lines) == 0 {
		return padStartupBlockVertically(nil, height)
	}

	topPadding := max((height-len(lines))/2, 0)
	rendered := make([]string, 0, height)
	rendered = appendStartupBlankLines(rendered, topPadding, startupLogoColumnWidth)
	for index, line := range lines {
		rendered = append(rendered, styleStartupLogoLine(centerStartupLine(line, startupLogoColumnWidth), index, noColor))
	}
	rendered = appendStartupBlankLines(rendered, height-len(rendered), startupLogoColumnWidth)

	return rendered
}

func styleStartupLogoLine(line string, index int, noColor bool) string {
	if noColor {
		return line
	}

	color := startupLogoColors[min(index, len(startupLogoColors)-1)]
	return color + line + colorReset
}

func renderStartupDetailLines(rows []startupDetailRow, noColor bool) []string {
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, fmt.Sprintf("%s %s", styleLabel(row.label, noColor), row.value))
	}

	return lines
}

func splitStartupLines(value string) []string {
	if value == "" {
		return nil
	}

	return strings.Split(value, "\n")
}

func centerStartupLine(line string, width int) string {
	lineWidth := len([]rune(line))
	if lineWidth >= width {
		return line
	}

	leftPadding := (width - lineWidth) / 2
	rightPadding := width - lineWidth - leftPadding
	return strings.Repeat(" ", leftPadding) + line + strings.Repeat(" ", rightPadding)
}

func padStartupBlockVertically(lines []string, height int) []string {
	if len(lines) >= height {
		return lines
	}

	padded := make([]string, 0, height)
	padded = append(padded, lines...)
	padded = appendStartupBlankLines(padded, height-len(lines), 0)

	return padded
}

func appendStartupBlankLines(lines []string, count int, width int) []string {
	for range count {
		lines = append(lines, strings.Repeat(" ", width))
	}

	return lines
}

func joinStartupBanner(mark string, wordmark string) string {
	markLines := strings.Split(mark, "\n")
	wordmarkLines := strings.Split(wordmark, "\n")
	markWidth := getStartupBlockWidth(markLines)
	wordmarkWidth := getStartupBlockWidth(wordmarkLines)
	lines := make([]string, 0, max(len(markLines), len(wordmarkLines)))
	height := max(len(markLines), len(wordmarkLines))

	for index := range height {
		markLine := getCenteredStartupBannerLine(markLines, index, height, markWidth)
		wordmarkLine := getCenteredStartupBannerLine(wordmarkLines, index, height, wordmarkWidth)
		lines = append(lines, markLine+"  "+wordmarkLine)
	}

	return strings.Join(lines, "\n")
}

func getStartupBlockWidth(lines []string) int {
	width := 0
	for _, line := range lines {
		width = max(width, len([]rune(line)))
	}

	return width
}

func getCenteredStartupBannerLine(lines []string, index int, height int, width int) string {
	topPadding := max((height-len(lines))/2, 0)
	line := getStartupBannerLine(lines, index-topPadding)
	return padStartupLineRight(line, width)
}

func getStartupBannerLine(lines []string, index int) string {
	if index < 0 || index >= len(lines) {
		return ""
	}

	return lines[index]
}

func padStartupLineRight(line string, width int) string {
	lineWidth := len([]rune(line))
	if lineWidth >= width {
		return line
	}

	return line + strings.Repeat(" ", width-lineWidth)
}

func getEffectiveStorageBackend(cfg *config.Config) string {
	if cfg == nil {
		return "sqlite"
	}

	backendValue := str.String(cfg.Storage.Backend)
	backend := backendValue.Normalized()
	if backend == "" {
		return "sqlite"
	}

	return backend
}

func styleStartup(value string, noColor bool) string {
	if noColor {
		return value
	}

	return colorGray + value + colorReset
}

func styleLabel(value string, noColor bool) string {
	if noColor {
		return value + ":"
	}

	return colorGray + value + ":" + colorReset
}
