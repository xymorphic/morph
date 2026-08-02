package tui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/xymorphic/morph/pkg/str"
)

type chromeRenderer interface {
	RenderHeader(headerPanel) string
	RenderNoticeBar(noticePanel) string
}

type lipglossChromeRenderer struct{}

var defaultChromeRenderer chromeRenderer = lipglossChromeRenderer{}

var morphBannerColors = []color.Color{
	lipgloss.Color("38"),
	lipgloss.Color("44"),
	lipgloss.Color("49"),
	lipgloss.Color("48"),
	lipgloss.Color("83"),
}

func (lipglossChromeRenderer) RenderHeader(panel headerPanel) string {
	width := max(panel.Width, 1)

	return lipgloss.NewStyle().
		Width(width).
		Render(renderHeaderContent(panel))
}

func (lipglossChromeRenderer) RenderNoticeBar(panel noticePanel) string {
	return renderNoticePanel(panel)
}

func renderHeaderContent(panel headerPanel) string {
	notice := panel.Notice
	notice.Width = panel.Width
	parts := []string{renderNoticePanel(notice)}
	for range noticeBarMarginBottom {
		parts = append(parts, "")
	}
	parts = append(parts, renderHeaderBody(panel))

	return strings.Join(parts, "\n")
}

func renderNoticePanel(panel noticePanel) string {
	width := max(panel.Width, 1)
	content := renderNoticeBarContent(renderNoticePanelLeft(panel), renderNoticePanelRight(panel), width)

	return lipgloss.NewStyle().
		Background(lipgloss.Color(defaultTUITheme.NoticeBackground)).
		Foreground(lipgloss.Color(defaultTUITheme.NoticeForeground)).
		Width(width).
		Render(content)
}

func renderNoticeBarContent(left string, right string, width int) string {
	leftValue := str.String(left)
	left = leftValue.Trim()
	rightValue := str.String(right)
	right = rightValue.Trim()
	if width <= lipgloss.Width(left) || right == "" {
		return left
	}

	spacerWidth := width - lipgloss.Width(left) - lipgloss.Width(right)
	if spacerWidth < 1 {
		return left
	}

	spacer := lipgloss.NewStyle().
		Background(lipgloss.Color(defaultTUITheme.NoticeBackground)).
		Render(strings.Repeat(" ", spacerWidth))

	return left + spacer + right
}

func renderNoticePanelLeft(panel noticePanel) string {
	muted := lipgloss.NewStyle().
		Padding(0, 0, 0, 1).
		Background(lipgloss.Color(defaultTUITheme.NoticeBackground)).
		Foreground(lipgloss.Color(defaultTUITheme.NoticeMuted))
	highlight := lipgloss.NewStyle().
		Background(lipgloss.Color(defaultTUITheme.NoticeBackground)).
		Foreground(lipgloss.Color(defaultTUITheme.NoticeForeground))

	return muted.Render(panel.LeftLead) + highlight.Render(panel.Name)
}

func renderNoticePanelRight(panel noticePanel) string {
	muted := lipgloss.NewStyle().
		Background(lipgloss.Color(defaultTUITheme.NoticeBackground)).
		Foreground(lipgloss.Color(defaultTUITheme.NoticeMuted))
	highlight := lipgloss.NewStyle().
		Background(lipgloss.Color(defaultTUITheme.NoticeBackground)).
		Foreground(lipgloss.Color(defaultTUITheme.NoticeForeground))

	return muted.Render(panel.Lead) +
		highlight.Render(panel.Link) +
		muted.Padding(0, 1, 0, 0).Render(panel.Tail)
}

func renderHeaderBody(panel headerPanel) string {
	bodyWidth := getHeaderBodyContentWidth(panel.Width)
	left := renderHeaderBannerGroup(panel)
	right := renderHeaderInfoPanel(panel)
	if right == "" {
		return padHeaderBody(left, panel.Width)
	}

	right = alignHeaderInfoPanel(right, lipgloss.Height(left))
	availableLeftWidth := max(bodyWidth-lipgloss.Width(right)-headerGapWidth, lipgloss.Width(left))
	left = lipgloss.NewStyle().
		Width(availableLeftWidth).
		Render(left)

	body := lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", headerGapWidth), right)
	return padHeaderBody(body, panel.Width)
}

func renderHeaderBannerGroup(panel headerPanel) string {
	banner := renderHeaderBrandText(panel.Banner)
	if panel.Mark == "" {
		return banner
	}

	mark := renderMorphBannerWithColors(panel.Mark, panel.BannerRows)
	return lipgloss.JoinHorizontal(lipgloss.Center, mark, strings.Repeat(" ", headerGapWidth), banner)
}

func renderHeaderBrandText(text string) string {
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		style := lipgloss.NewStyle().
			Foreground(lipgloss.Color(defaultTUITheme.MutedText))
		if index == 0 {
			style = style.
				Bold(true).
				Foreground(lipgloss.Color(defaultTUITheme.NoticeForeground))
		}
		lines[index] = style.Render(line)
	}

	return strings.Join(lines, "\n")
}

func padHeaderBody(body string, width int) string {
	width = max(width, 1)
	lines := strings.Split(body, "\n")
	for index, line := range lines {
		lineWidth := lipgloss.Width(line)
		if width <= headerBodyPadding*2 || lineWidth >= width {
			lines[index] = line
			continue
		}

		rightPadding := max(width-lineWidth-headerBodyPadding, 0)
		lines[index] = strings.Repeat(" ", headerBodyPadding) + line + strings.Repeat(" ", rightPadding)
	}

	return strings.Join(lines, "\n")
}

func renderHeaderInfoPanel(panel headerPanel) string {
	if !panel.ShowInfo {
		return ""
	}

	leftRows, rightRows := splitHeaderInfoRows(panel.InfoRows)
	columnWidth := getHeaderInfoColumnWidth(panel.InfoRows)
	lines := make([]string, 0, max(len(leftRows), len(rightRows)))
	for index := range max(len(leftRows), len(rightRows)) {
		left := renderHeaderInfoCell(getHeaderInfoRowAt(leftRows, index), columnWidth)
		if len(rightRows) == 0 {
			lines = append(lines, left)
			continue
		}

		right := renderHeaderInfoCell(getHeaderInfoRowAt(rightRows, index), columnWidth)
		lines = append(lines, left+strings.Repeat(" ", headerInfoColumnGap)+right)
	}

	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(defaultTUITheme.MutedText)).
		Width(getHeaderInfoWidth(panel.InfoRows)).
		Render(strings.Join(lines, "\n"))
}

func splitHeaderInfoRows(rows []headerInfoRow) ([]headerInfoRow, []headerInfoRow) {
	if len(rows) <= 1 {
		return rows, nil
	}

	midpoint := (len(rows) + 1) / 2
	return rows[:midpoint], rows[midpoint:]
}

func getHeaderInfoRowAt(rows []headerInfoRow, index int) headerInfoRow {
	if index < 0 || index >= len(rows) {
		return headerInfoRow{}
	}

	return rows[index]
}

func renderHeaderInfoCell(row headerInfoRow, width int) string {
	if row == (headerInfoRow{}) {
		return strings.Repeat(" ", max(width, 0))
	}

	key := lipgloss.NewStyle().
		Width(headerInfoKeyWidth).
		Align(lipgloss.Right).
		Render(row.key)

	return lipgloss.NewStyle().
		Width(width).
		Render(key + ": " + row.value)
}

func renderMorphBannerWithColors(banner string, colors []color.Color) string {
	if len(colors) == 0 {
		colors = morphBannerColors
	}

	lines := strings.Split(banner, "\n")
	for index, line := range lines {
		colorIndex := min(index, len(colors)-1)
		lines[index] = lipgloss.NewStyle().
			Foreground(colors[colorIndex]).
			Render(line)
	}

	return strings.Join(lines, "\n")
}
