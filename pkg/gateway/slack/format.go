package slack

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/xymorphic/morph/pkg/str"
)

func FormatMrkdwn(text string) string {
	if text == "" {
		return text
	}

	placeholders := map[string]string{}
	placeholderIndex := 0
	nextPlaceholder := func(value string) string {
		key := fmt.Sprintf("{{SLPH%d}}", placeholderIndex)
		placeholderIndex++
		placeholders[key] = value
		return key
	}

	formatted := text
	formatted = protectMrkdwnFencedCode(formatted, nextPlaceholder)
	formatted = protectSlackTokens(formatted, nextPlaceholder)
	formatted = regexp.MustCompile("`[^`]+`").ReplaceAllStringFunc(formatted, func(match string) string {
		return nextPlaceholder("`" + EscapeMrkdwnText(strings.Trim(match, "`")) + "`")
	})
	formatted = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`).ReplaceAllStringFunc(formatted, func(match string) string {
		parts := regexp.MustCompile(`^\[([^\]]+)\]\(([^)]+)\)$`).FindStringSubmatch(match)
		label := strings.ReplaceAll(EscapeMrkdwnText(parts[1]), "|", "-")
		partsValue := str.String(parts[2])
		url := strings.ReplaceAll(partsValue.Trim(), ">", "%3E")
		return nextPlaceholder("<" + url + "|" + label + ">")
	})
	formatted = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`).ReplaceAllStringFunc(formatted, func(match string) string {
		parts := regexp.MustCompile(`^#{1,6}\s+(.+)$`).FindStringSubmatch(match)
		inner := regexp.MustCompile(`\*\*(.+?)\*\*`).ReplaceAllString(parts[1], "$1")
		innerValue := str.String(inner)
		return nextPlaceholder("*" + EscapeMrkdwnText(innerValue.Trim()) + "*")
	})
	formatted = regexp.MustCompile(`\*\*(.+?)\*\*`).ReplaceAllStringFunc(formatted, func(match string) string {
		parts := regexp.MustCompile(`^\*\*(.+?)\*\*$`).FindStringSubmatch(match)
		return nextPlaceholder("*" + EscapeMrkdwnText(parts[1]) + "*")
	})
	formatted = regexp.MustCompile(`__([^_\n]+)__`).ReplaceAllStringFunc(formatted, func(match string) string {
		parts := regexp.MustCompile(`^__([^_\n]+)__$`).FindStringSubmatch(match)
		return nextPlaceholder("*" + EscapeMrkdwnText(parts[1]) + "*")
	})
	formatted = regexp.MustCompile(`\*([^*\n]+)\*`).ReplaceAllStringFunc(formatted, func(match string) string {
		parts := regexp.MustCompile(`^\*([^*\n]+)\*$`).FindStringSubmatch(match)
		return nextPlaceholder("_" + EscapeMrkdwnText(parts[1]) + "_")
	})
	formatted = regexp.MustCompile(`(^|[^A-Za-z0-9])_([^_\n]+)_([^A-Za-z0-9]|$)`).ReplaceAllStringFunc(formatted, func(match string) string {
		parts := regexp.MustCompile(`(^|[^A-Za-z0-9])_([^_\n]+)_([^A-Za-z0-9]|$)`).FindStringSubmatch(match)
		return parts[1] + nextPlaceholder("_"+EscapeMrkdwnText(parts[2])+"_") + parts[3]
	})
	formatted = regexp.MustCompile(`~~(.+?)~~`).ReplaceAllStringFunc(formatted, func(match string) string {
		parts := regexp.MustCompile(`^~~(.+?)~~$`).FindStringSubmatch(match)
		return nextPlaceholder("~" + EscapeMrkdwnText(parts[1]) + "~")
	})
	formatted = regexp.MustCompile(`(?m)^(>{1,3}) ?(.+)$`).ReplaceAllStringFunc(formatted, func(match string) string {
		parts := regexp.MustCompile(`^(>{1,3}) ?(.+)$`).FindStringSubmatch(match)
		return nextPlaceholder(parts[1] + " " + EscapeMrkdwnText(parts[2]))
	})
	formatted = EscapeMrkdwnText(formatted)

	for key, value := range placeholders {
		formatted = strings.ReplaceAll(formatted, EscapeMrkdwnText(key), value)
		formatted = strings.ReplaceAll(formatted, key, value)
	}

	return formatted
}

func FormatStreamMarkdown(text string) string {
	if text == "" {
		return text
	}

	var out strings.Builder
	inFence := false
	lines := strings.SplitAfter(text, "\n")
	for _, line := range lines {
		trimSuffixValue := str.String(strings.TrimSuffix(line, "\n"))
		trimmed := trimSuffixValue.Trim()
		fenceLine := strings.HasPrefix(trimmed, "```")
		if fenceLine {
			if inFence {
				inFence = false
				out.WriteString("```")
				if strings.HasSuffix(line, "\n") {
					out.WriteString("\n")
				}
			} else {
				inFence = true
				out.WriteString("```\n")
			}
			continue
		}
		if !inFence {
			line = formatSlackStreamLine(line)
		}
		out.WriteString(line)
	}

	return out.String()
}

func FormatStreamChunks(text string) []Chunk {
	textValue := str.String(text)
	if textValue.Trim() == "" {
		return nil
	}

	var chunks []Chunk
	lastIndex := 0
	codeBlock := regexp.MustCompile("```[\\s\\S]*?```")
	for _, match := range codeBlock.FindAllStringIndex(text, -1) {
		chunks = appendMarkdownTextChunks(chunks, FormatStreamMarkdown(text[lastIndex:match[0]]))
		chunks = append(chunks, FencedCodeChunk(getFencedCodeBody(text[match[0]:match[1]])))
		lastIndex = match[1]
	}
	chunks = appendMarkdownTextChunks(chunks, FormatStreamMarkdown(text[lastIndex:]))

	return chunks
}

func getFencedCodeBody(text string) string {
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	if strings.HasPrefix(text, "\n") {
		return strings.TrimPrefix(text, "\n")
	}

	header, body, ok := strings.Cut(text, "\n")
	if !ok {
		return text
	}
	headerValue := str.String(header)
	if headerValue.Trim() != "" && strings.Contains(body, "\n") {
		return body
	}

	return text
}

func appendMarkdownTextChunks(chunks []Chunk, text string) []Chunk {
	if text == "" {
		return chunks
	}

	runes := []rune(text)
	for len(runes) > 0 {
		n := min(len(runes), MarkdownTextLimit)
		chunks = append(chunks, MarkdownTextChunk(string(runes[:n])))
		runes = runes[n:]
	}

	return chunks
}

func formatSlackStreamLine(line string) string {
	placeholders := map[string]string{}
	placeholderIndex := 0
	nextPlaceholder := func(value string) string {
		key := fmt.Sprintf("{{SLPH%d}}", placeholderIndex)
		placeholderIndex++
		placeholders[key] = value
		return key
	}

	formatted := regexp.MustCompile("`[^`]+`").ReplaceAllStringFunc(line, nextPlaceholder)
	formatted = regexp.MustCompile(`(^|[^~])~([^~\n]+)~([^~]|$)`).ReplaceAllStringFunc(formatted, func(match string) string {
		parts := regexp.MustCompile(`(^|[^~])~([^~\n]+)~([^~]|$)`).FindStringSubmatch(match)
		return parts[1] + "~~" + parts[2] + "~~" + parts[3]
	})

	for key, value := range placeholders {
		formatted = strings.ReplaceAll(formatted, key, value)
	}

	return formatted
}

func EscapeMrkdwnText(text string) string {
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")
	return text
}

func protectMrkdwnFencedCode(text string, nextPlaceholder func(string) string) string {
	return regexp.MustCompile("```(?:[^\\n]*\\n)?[\\s\\S]*?```").ReplaceAllStringFunc(text, func(match string) string {
		openingEnd := 3
		if newline := strings.Index(match[3:], "\n"); newline >= 0 {
			openingEnd = 3 + newline + 1
		}
		opening := match[:openingEnd]
		body := strings.TrimSuffix(match[openingEnd:], "```")
		return nextPlaceholder(normalizeSlackFenceLine(opening) + EscapeMrkdwnText(body) + "```")
	})
}

func normalizeSlackFenceLine(line string) string {
	if strings.HasSuffix(line, "\n") {
		return "```\n"
	}

	return "```"
}

func protectSlackTokens(text string, nextPlaceholder func(string) string) string {
	return regexp.MustCompile(`<(@[A-Za-z0-9._-]+|#[A-Za-z0-9._-]+(?:\|[^>]+)?|!(?:here|channel|everyone)|!subteam\^[A-Za-z0-9._-]+(?:\|[^>]+)?|!date\^[^>]+\|[^>]+)>`).
		ReplaceAllStringFunc(text, nextPlaceholder)
}
