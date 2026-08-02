package terminalmd

import (
	"strings"

	"github.com/xymorphic/morph/pkg/str"
)

func normalizeCommonMarkdownArtifacts(markdown string) string {
	lines := strings.Split(markdown, "\n")
	for index, line := range lines {
		prefixLen := len(line) - len(strings.TrimLeft(line, " \t"))
		prefix := line[:prefixLen]
		trimmed := line[prefixLen:]
		for _, marker := range []string{"• ", "‣ ", "◦ ", "○ ", "▪ "} {
			if strings.HasPrefix(trimmed, marker) {
				lines[index] = prefix + "- " + strings.TrimPrefix(trimmed, marker)
				break
			}
		}
	}

	return strings.Join(lines, "\n")
}

// sanitizeHyperlinkDestination removes terminal-control characters from an OSC 8
// destination. This prevents malformed model output from breaking the terminal
func trimLeadingTaskMarker(text string) string {
	textValue := str.String(text)
	text = textValue.Trim()
	for _, marker := range []string{"[ ]", "[x]", "[X]"} {
		if strings.HasPrefix(text, marker) {
			trimPrefixValue := str.String(strings.TrimPrefix(text, marker))
			return trimPrefixValue.Trim()
		}
	}

	return text
}

// unescapeMarkdownText applies CommonMark-style backslash unescaping for text
// nodes. Goldmark leaves some escaped punctuation in raw text; terminal output
// should show the intended literal character instead of the escape slash.
func unescapeMarkdownText(text string) string {
	if !strings.Contains(text, "\\") {
		return text
	}

	var builder strings.Builder
	builder.Grow(len(text))
	escaped := false
	for _, char := range text {
		if escaped {
			if isEscapableMarkdownPunctuation(char) {
				builder.WriteRune(char)
			} else {
				builder.WriteRune('\\')
				builder.WriteRune(char)
			}
			escaped = false
			continue
		}
		if char == '\\' {
			escaped = true
			continue
		}

		builder.WriteRune(char)
	}
	if escaped {
		builder.WriteRune('\\')
	}

	return builder.String()
}

// stripHTMLTags removes simple raw HTML while preserving visible text and line
// breaks. This is intentionally conservative; terminalmd is not an HTML renderer.
func stripHTMLTags(text string) string {
	if !strings.Contains(text, "<") {
		return text
	}

	var builder strings.Builder
	builder.Grow(len(text))
	inTag := false
	for index := 0; index < len(text); index++ {
		char := text[index]
		switch {
		case char == '<':
			lower := strings.ToLower(text[index:])
			if strings.HasPrefix(lower, "<br>") {
				builder.WriteByte('\n')
				index += len("<br>") - 1
				continue
			}
			if strings.HasPrefix(lower, "<br/>") {
				builder.WriteByte('\n')
				index += len("<br/>") - 1
				continue
			}
			if strings.HasPrefix(lower, "<br />") {
				builder.WriteByte('\n')
				index += len("<br />") - 1
				continue
			}
			inTag = true
		case char == '>':
			inTag = false
		case !inTag:
			builder.WriteByte(char)
		}
	}
	textValue2 := str.String(builder.String())
	return textValue2.Trim()
}

// isEscapableMarkdownPunctuation reports whether CommonMark allows a punctuation
func isEscapableMarkdownPunctuation(char rune) bool {
	return strings.ContainsRune(`\`+"`*{}[]()#+-.!_|<>", char)
}
