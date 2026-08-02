package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/pkg/str"
)

type bottomStatusPanelRenderer interface {
	Render(bottomStatusPanel) string
}

type lipglossBottomStatusPanelRenderer struct{}

var defaultBottomStatusPanelRenderer bottomStatusPanelRenderer = lipglossBottomStatusPanelRenderer{}

const permissionStatusIcon = "⛨"

func (lipglossBottomStatusPanelRenderer) Render(panel bottomStatusPanel) string {
	segments := make([]string, 0, 3)
	preset := panel.PermissionPreset
	if preset == "" && panel.FullAccess {
		preset = permissions.PresetFullAccess
	}
	if preset == permissions.PresetFullAccess {
		segments = append(segments, renderBottomStatusDangerCell(permissionStatusIcon+" Full access (unsafe)"))
	} else {
		label := panel.PermissionLabel
		if label == "" {
			label = preset.Label()
		}
		if label != "" {
			label = permissionStatusIcon + " " + label
		}
		segments = append(segments, renderBottomStatusMutedCell(label))
	}
	modelBudget := getBottomStatusModelBudget(panel, segments)
	modelName := truncateCommandMenuText(panel.ModelName, modelBudget)
	segments = append(
		segments,
		renderBottomStatusMutedCell(modelName),
		renderBottomStatusMutedCell(panel.Status),
	)
	if panel.Thinking {
		segments = append([]string{renderThinkingStatusCell(panel.ThinkingFrame)}, segments...)
	}

	left := joinBottomStatusPanelRenderedSegments(segments, panel.ContentWidth)
	right := renderBottomStatusMutedCell(panel.Context)
	if panel.ExitConfirmation || panel.InterruptConfirmation {
		left = renderBottomStatusMutedCell(panel.Status)
		right = ""
	} else if right != "" && lipgloss.Width(left)+lipgloss.Width(right)+3 > panel.ContentWidth {
		left = joinBottomStatusPanelRenderedSegments(segments[:len(segments)-1], panel.ContentWidth)
	}

	return lipgloss.NewStyle().
		Padding(0, panel.HorizontalPadding).
		Width(panel.Width).
		Render(spaceBetweenBottomStatusPanel(left, right, panel.ContentWidth))
}

func getBottomStatusModelBudget(panel bottomStatusPanel, leading []string) int {
	reserved := lipgloss.Width(renderBottomStatusMutedCell(panel.Context))
	segmentCount := 0
	for _, segment := range leading {
		if width := lipgloss.Width(segment); width > 0 {
			reserved += width
			segmentCount++
		}
	}
	if panel.Thinking {
		reserved += lipgloss.Width(renderThinkingStatusCell(panel.ThinkingFrame))
		segmentCount++
	}
	if statusWidth := lipgloss.Width(renderBottomStatusMutedCell(panel.Status)); statusWidth > 0 {
		reserved += statusWidth
		segmentCount++
	}
	if segmentCount > 0 {
		reserved += segmentCount * 3
	}
	if strings.TrimSpace(panel.Context) != "" {
		reserved += 3
	}
	return max(panel.ContentWidth-reserved, 1)
}

func renderBottomStatusDangerCell(text string) string {
	text = str.String(text).Trim()
	if text == "" {
		return ""
	}

	return lipgloss.NewStyle().
		Inline(true).
		Bold(true).
		Foreground(lipgloss.Color(defaultTUITheme.ToolDeletion)).
		Render(text)
}

func renderBottomStatusMutedCell(text string) string {
	textValue := str.String(text)
	text = textValue.Trim()
	if text == "" {
		return ""
	}

	return renderBottomStatusMutedText(text)
}

func renderBottomStatusMutedText(text string) string {
	if text == "" {
		return ""
	}

	return lipgloss.NewStyle().
		Inline(true).
		Foreground(lipgloss.Color(defaultTUITheme.MutedText)).
		Render(text)
}

func joinBottomStatusPanelRenderedSegments(segments []string, width int) string {
	visible := make([]string, 0, len(segments))
	for _, segment := range segments {
		segmentValue := str.String(segment)
		if segment = segmentValue.Trim(); segment != "" {
			visible = append(visible, segment)
		}
	}
	if len(visible) == 0 {
		return ""
	}
	if len(visible) == 1 {
		return visible[0]
	}

	wideSeparator := renderBottomStatusMutedText("  ·  ")
	value := strings.Join(visible, wideSeparator)
	if lipgloss.Width(value) <= width {
		return value
	}

	return strings.Join(visible, renderBottomStatusMutedText(" · "))
}

// joinBottomStatusPanelSegments joins metadata while preserving narrow-screen fallback.
func joinBottomStatusPanelSegments(segments []string, width int) string {
	visible := make([]string, 0, len(segments))
	for _, segment := range segments {
		segmentValue2 := str.String(segment)
		if segment = segmentValue2.Trim(); segment != "" {
			visible = append(visible, segment)
		}
	}
	if len(visible) == 0 {
		return ""
	}
	if len(visible) == 1 {
		return visible[0]
	}

	separator := "  ·  "
	value := strings.Join(visible, separator)
	if lipgloss.Width(value) <= width {
		return value
	}

	return strings.Join(visible, " · ")
}

// spaceBetweenBottomStatusPanel pushes context usage to the right edge when possible.
func spaceBetweenBottomStatusPanel(left, right string, width int) string {
	leftValue := str.String(left)
	left = leftValue.Trim()
	rightValue := str.String(right)
	right = rightValue.Trim()
	if left == "" {
		return right
	}
	if right == "" {
		return left
	}

	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap <= 0 {
		return left + renderBottomStatusMutedText(" · ") + right
	}

	return left + strings.Repeat(" ", gap) + right
}
