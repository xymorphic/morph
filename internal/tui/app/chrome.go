package tui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/wandxy/morph/internal/brand"
	modelprovider "github.com/wandxy/morph/internal/model/provider"
	"github.com/wandxy/morph/pkg/str"
)

const morphProductName = "Morph"

const morphHeaderMark = brand.Mark

const (
	headerBorderHeight    = 1
	noticeBarHeight       = 1
	noticeBarMarginBottom = 1
	headerBodyPadding     = 1
	headerInfoKeyWidth    = 9
	headerInfoColumnGap   = 2
	headerGapWidth        = 2
)

const (
	noticeBarLeftLead = "Welcome, "
	noticeBarName     = "Kennedy"
	noticeBarLead     = "Use "
	noticeBarLink     = "/changelog"
	noticeBarTail     = " to see what changed"
)

// renderHeader draws the fixed title bar.
func (m model) renderHeader() string {
	return m.renderHeaderWithWidth(m.width)
}

func (m model) renderHeaderWithWidth(width int) string {
	return defaultChromeRenderer.RenderHeader(getHeaderPanel(m, width))
}

// renderNoticeBar draws the solid announcement row above the banner.
func (m model) renderNoticeBar() string {
	return defaultChromeRenderer.RenderNoticeBar(getNoticePanel(m.width, m.userDisplayName()))
}

// renderNoticeBarLeft highlights the user name while keeping the greeting muted.
func renderNoticeBarLeft() string {
	return renderNoticePanelLeft(getNoticePanel(defaultWidth))
}

// renderNoticeBarRight styles the right-side notice command hint.
func renderNoticeBarRight() string {
	return renderNoticePanelRight(getNoticePanel(defaultWidth))
}

// renderHeaderInfoPanel renders the right-morph runtime information panel.
func (m model) renderHeaderInfoPanel() string {
	return renderHeaderInfoPanel(getHeaderPanel(m, m.width))
}

type headerInfoRow struct {
	key   string
	value string
}

type headerPanel struct {
	Width      int
	Banner     string
	Mark       string
	Notice     noticePanel
	InfoRows   []headerInfoRow
	ShowInfo   bool
	BannerRows []color.Color
}

type noticePanel struct {
	Width    int
	LeftLead string
	Name     string
	Lead     string
	Link     string
	Tail     string
}

func getHeaderPanel(m model, width int) headerPanel {
	width = max(width, 1)
	rows := getHeaderInfoRows(m)
	infoWidth := getHeaderInfoWidth(rows)
	bodyWidth := getHeaderBodyContentWidth(width)
	fullBanner := getHeaderBrandText(m.runtimeInfo)
	banner := getHeaderBanner(bodyWidth, m.runtimeInfo)
	mark := getHeaderMark(bodyWidth, banner, fullBanner)
	bannerWidth := getHeaderBannerGroupWidth(mark, banner)

	return headerPanel{
		Width:    width,
		Banner:   banner,
		Mark:     mark,
		Notice:   getNoticePanel(width, m.userDisplayName()),
		InfoRows: rows,
		ShowInfo: bodyWidth >= bannerWidth+headerGapWidth+infoWidth,
	}
}

func getHeaderBodyContentWidth(width int) int {
	return max(width-headerBodyPadding*2, 1)
}

func getNoticePanel(width int, names ...string) noticePanel {
	name := noticeBarName
	if len(names) > 0 {
		nameValue := str.String(names[0])
		if trimmedName := nameValue.Trim(); trimmedName != "" {
			name = trimmedName
		}
	}

	return noticePanel{
		Width:    max(width, 1),
		LeftLead: noticeBarLeftLead,
		Name:     name,
		Lead:     noticeBarLead,
		Link:     noticeBarLink,
		Tail:     noticeBarTail,
	}
}

// getHeaderInfoRows returns the runtime metadata rows shown in the header.
func getHeaderInfoRows(m model) []headerInfoRow {
	info := m.runtimeInfo

	return []headerInfoRow{
		{key: "version", value: getRuntimeValue(info.Version, "dev")},
		{key: "commit", value: getRuntimeValue(info.Commit, "unknown")},
		{key: "profile", value: getRuntimeValue(info.Profile, "default")},
		{key: "session", value: getRuntimeValue(m.sessionID, "default")},
		{key: "streaming", value: getRuntimeValue(info.Streaming, "on")},
		{key: "provider", value: getRuntimeValue(info.Provider, "openrouter")},
		{key: "model", value: m.getModelLabel()},
		{key: "summary", value: getModelDisplayName(info.SummaryModel)},
		{key: "embedding", value: getModelDisplayName(info.EmbeddingModel)},
		{key: "storage", value: getRuntimeValue(info.Storage, "sqlite")},
	}
}

func (m model) getModelLabel() string {
	info := m.runtimeInfo
	modelName := getRuntimeValue(m.modelName, getRuntimeModelDisplayName(info.Provider, info.API, info.Model))
	displayName := getModelDisplayName(modelName)
	matches := m.reasoningMatchesCurrentModel()
	if reasoningName := strings.TrimSpace(m.reasoning.Model.DisplayName); matches && reasoningName != "" {
		displayName = reasoningName
	}
	effort := strings.TrimSpace(string(m.reasoning.EffectiveEffort))
	if effort == "" || m.modelRestartPending || !matches {
		return displayName
	}
	return displayName + " (" + effort + ")"
}

func (m model) reasoningMatchesCurrentModel() bool {
	tuple := m.reasoning.Model
	if strings.TrimSpace(tuple.Provider) == "" || strings.TrimSpace(tuple.Model) == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(tuple.Provider), strings.TrimSpace(m.runtimeInfo.Provider)) &&
		strings.EqualFold(strings.TrimSpace(tuple.API), strings.TrimSpace(m.runtimeInfo.API)) &&
		strings.EqualFold(strings.TrimSpace(tuple.Model), strings.TrimSpace(m.runtimeInfo.Model))
}

// getHeaderInfoWidth returns the narrowest panel width that keeps values intact.
func getHeaderInfoWidth(rows []headerInfoRow) int {
	columnWidth := getHeaderInfoColumnWidth(rows)
	if len(rows) <= 1 {
		return columnWidth
	}

	return columnWidth*2 + headerInfoColumnGap
}

func getHeaderInfoColumnWidth(rows []headerInfoRow) int {
	maxValueWidth := 0
	for _, row := range rows {
		maxValueWidth = max(maxValueWidth, lipgloss.Width(row.value))
	}

	return headerInfoKeyWidth + 2 + maxValueWidth
}

// alignHeaderInfoPanel pads runtime info so it sits vertically centered beside the banner.
func alignHeaderInfoPanel(info string, targetHeight int) string {
	infoHeight := lipgloss.Height(info)
	if info == "" || infoHeight >= targetHeight {
		return info
	}

	topPadding := (targetHeight - infoHeight) / 2
	bottomPadding := targetHeight - infoHeight - topPadding

	return strings.Repeat("\n", topPadding) + info + strings.Repeat("\n", bottomPadding)
}

// getModelDisplayName removes the provider or owner prefix from a model identifier.
func getModelDisplayName(name string) string {
	nameValue := str.String(name)
	name = nameValue.Trim()
	if _, model, ok := strings.Cut(name, "/"); ok {
		modelValue := str.String(model)
		return modelValue.Trim()
	}

	return name
}

func getRuntimeModelDisplayName(provider, api, modelID string) string {
	registry := modelprovider.DefaultRegistry()
	model, ok := registry.GetModelForAPI(provider, api, modelID)
	if strings.TrimSpace(api) == "" {
		model, ok = registry.GetModel(provider, modelID)
	}
	if ok {
		nameValue := str.String(model.Name)
		if name := nameValue.Trim(); name != "" {
			return name
		}
	}

	return getModelDisplayName(modelID)
}

func getHeaderBrandText(info runtimeInfo) string {
	return morphProductName + "\n" + getHeaderBrandVersion(info)
}

func getHeaderCompactBrandText(info runtimeInfo) string {
	return morphProductName + " " + getHeaderBrandVersion(info)
}

func getHeaderBrandVersion(info runtimeInfo) string {
	version := getRuntimeValue(info.Version, "dev")
	commit := getRuntimeValue(info.Commit, "unknown")
	if version != "dev" && !strings.HasPrefix(strings.ToLower(version), "v") {
		version = "v" + version
	}

	return version + " (" + commit + ")"
}

func getHeaderBanner(width int, info runtimeInfo) string {
	full := getHeaderBrandText(info)
	if width >= lipgloss.Width(full) {
		return full
	}

	compact := getHeaderCompactBrandText(info)
	if width >= lipgloss.Width(compact) {
		return compact
	}

	return morphProductName
}

func getHeaderMark(width int, banner string, fullBanner string) string {
	if banner != fullBanner {
		return ""
	}
	if width < lipgloss.Width(morphHeaderMark)+headerGapWidth+lipgloss.Width(fullBanner) {
		return ""
	}

	return morphHeaderMark
}

func getHeaderBannerGroupWidth(mark string, banner string) int {
	width := lipgloss.Width(banner)
	if mark == "" {
		return width
	}

	return lipgloss.Width(mark) + headerGapWidth + width
}

// renderMorphBanner renders the generated figlet masthead.
func renderMorphBanner(banner string) string {
	return renderMorphBannerWithColors(banner, nil)
}

// getMorphBannerColor returns the stable lolcat-inspired color for a banner row.
func getMorphBannerColor(index int) color.Color {
	if index >= 0 && index < len(morphBannerColors) {
		return morphBannerColors[index]
	}

	return morphBannerColors[len(morphBannerColors)-1]
}
