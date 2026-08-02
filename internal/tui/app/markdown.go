package tui

import (
	"strings"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/xymorphic/morph/pkg/str"
	"github.com/xymorphic/morph/pkg/terminalmd"
)

func renderMarkdownForTranscript(markdown string, width int) string {
	markdownValue := str.String(markdown)
	markdown = markdownValue.Trim()
	if markdown == "" || !hasTranscriptMarkdown(markdown) {
		return markdown
	}

	rendered, err := terminalmd.NewRenderer(terminalmd.Options{
		Width:            max(width, 1),
		EnableHyperlinks: true,
		Theme: terminalmd.Theme{
			Text:        terminalmd.Style{Foreground: defaultTUITheme.NoticeMuted},
			Muted:       terminalmd.Style{Foreground: defaultTUITheme.MutedText},
			Heading:     terminalmd.Style{Foreground: defaultTUITheme.NoticeForeground, Bold: true},
			Code:        terminalmd.Style{Foreground: defaultTUITheme.MarkdownCodeForeground, Background: defaultTUITheme.MarkdownCodeBackground},
			CodeBlock:   terminalmd.Style{Foreground: defaultTUITheme.NoticeMuted},
			Link:        terminalmd.Style{Foreground: defaultTUITheme.MarkdownLinkForeground},
			QuoteMarker: terminalmd.Style{Foreground: defaultTUITheme.MutedText},
			TableBorder: terminalmd.Style{Foreground: defaultTUITheme.Separator},
		},
	}).Render(markdown)
	if err != nil {
		return markdown
	}
	stripValue := str.String(xansi.Strip(rendered))
	if stripValue.Trim() == "" {
		return markdown
	}

	return rendered
}

func hasTranscriptMarkdown(value string) bool {
	for _, line := range strings.Split(value, "\n") {
		lineValue := str.String(line)
		line = lineValue.Trim()
		switch {
		case strings.HasPrefix(line, "#"),
			hasMarkdownAutolinkText(line),
			strings.HasPrefix(line, "- "),
			strings.HasPrefix(line, "* "),
			strings.HasPrefix(line, "+ "),
			strings.HasPrefix(line, "• "),
			strings.HasPrefix(line, "‣ "),
			strings.HasPrefix(line, "◦ "),
			strings.HasPrefix(line, "> "),
			strings.HasPrefix(line, "```"),
			strings.HasPrefix(line, "~~~"),
			strings.HasPrefix(line, "|"),
			terminalmd.IsMermaidDiagramStart(line),
			isSetextHeadingUnderline(line),
			isOrderedMarkdownListItem(line):
			return true
		}
	}

	return strings.Contains(value, "**") ||
		strings.Contains(value, "__") ||
		strings.Contains(value, "~~") ||
		strings.Contains(value, "`") ||
		strings.Contains(value, "](") ||
		hasMarkdownAutolinkText(value) ||
		hasHTMLMarkdownArtifact(value)
}

func hasMarkdownAutolinkText(value string) bool {
	value = strings.ToLower(value)

	return strings.Contains(value, "http://") ||
		strings.Contains(value, "https://") ||
		strings.Contains(value, "mailto:") ||
		strings.Contains(value, "www.")
}

func isOrderedMarkdownListItem(line string) bool {
	dot := strings.Index(line, ". ")
	if dot <= 0 {
		return false
	}
	for _, char := range line[:dot] {
		if char < '0' || char > '9' {
			return false
		}
	}

	return true
}

func isSetextHeadingUnderline(line string) bool {
	if len(line) < 2 {
		return false
	}

	for _, char := range line {
		if char != '=' && char != '-' {
			return false
		}
	}

	return true
}

func hasHTMLMarkdownArtifact(value string) bool {
	for _, tag := range []string{"<br", "<strong", "<em", "<del", "<code", "<a "} {
		if strings.Contains(strings.ToLower(value), tag) {
			return true
		}
	}

	return false
}
