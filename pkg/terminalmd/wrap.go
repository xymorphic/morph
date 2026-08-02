package terminalmd

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/xymorphic/morph/pkg/str"
)

func wrapANSI(text string, width int, firstPrefix string, restPrefix string) []string {
	textValue := str.String(text)
	text = textValue.Trim()
	if text == "" {
		return []string{firstPrefix}
	}
	if width <= 0 {
		width = defaultWidth
	}

	lines := make([]string, 0)
	for paragraphIndex, paragraph := range strings.Split(text, "\n") {
		// After an explicit line break, all subsequent paragraphs should align
		// with continuation text rather than reuse the first-line marker.
		if paragraphIndex > 0 {
			firstPrefix = restPrefix
		}

		words := strings.Fields(paragraph)
		currentPrefix := firstPrefix
		current := ""
		currentWidth := 0
		for _, word := range words {
			// available changes when currentPrefix changes from firstPrefix to
			// restPrefix after the first wrapped line.
			available := max(1, width-ansi.StringWidth(currentPrefix))
			wordWidth := ansi.StringWidth(word)
			if current == "" {
				current = word
				currentWidth = wordWidth
				continue
			}
			if currentWidth+1+wordWidth > available {
				lines = append(lines, currentPrefix+current)
				currentPrefix = restPrefix
				current = word
				currentWidth = wordWidth
				continue
			}
			current += " " + word
			currentWidth += 1 + wordWidth
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

// joinBlocks trims empty rendered blocks and separates remaining blocks with one
// blank line, matching transcript readability expectations.
func joinBlocks(blocks []string) string {
	clean := make([]string, 0, len(blocks))
	for _, block := range blocks {
		blockValue := str.String(block)
		block = blockValue.Trim()
		if block != "" {
			clean = append(clean, block)
		}
	}

	return strings.Join(clean, "\n\n")
}

func maxLineWidth(value string) int {
	width := 0
	for _, line := range strings.Split(value, "\n") {
		width = max(width, ansi.StringWidth(line))
	}

	return width
}

// trimLeadingTaskMarker removes the literal marker left behind after a task
