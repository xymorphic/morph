package telegram

import (
	"regexp"
	"strings"

	"github.com/xymorphic/morph/pkg/str"
)

const ParseModeMarkdownV2 = "MarkdownV2"

const markdownV2EscapableCharacters = "_*[]()~`>#+-=|{}.!\\"

func FormatMarkdownV2(text string) string {
	if text == "" {
		return text
	}

	placeholders := map[string]string{}
	placeholderIndex := 0
	nextPlaceholder := func(value string) string {
		key := "\x00TGPH" + string(rune(placeholderIndex)) + "\x00"
		placeholderIndex++
		placeholders[key] = value
		return key
	}

	formatted := text
	formatted = protectFencedCode(formatted, nextPlaceholder)
	formatted = regexp.MustCompile("`[^`]+`").ReplaceAllStringFunc(formatted, func(match string) string {
		return nextPlaceholder(strings.ReplaceAll(match, `\`, `\\`))
	})
	formatted = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`).ReplaceAllStringFunc(formatted, func(match string) string {
		parts := regexp.MustCompile(`^\[([^\]]+)\]\(([^)]+)\)$`).FindStringSubmatch(match)
		text := EscapeMarkdownV2(parts[1])
		url := strings.ReplaceAll(strings.ReplaceAll(parts[2], `\`, `\\`), ")", `\)`)
		return nextPlaceholder("[" + text + "](" + url + ")")
	})
	formatted = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`).ReplaceAllStringFunc(formatted, func(match string) string {
		parts := regexp.MustCompile(`^#{1,6}\s+(.+)$`).FindStringSubmatch(match)
		inner := regexp.MustCompile(`\*\*(.+?)\*\*`).ReplaceAllString(parts[1], "$1")
		innerValue := str.String(inner)
		return nextPlaceholder("*" + EscapeMarkdownV2(innerValue.Trim()) + "*")
	})
	formatted = regexp.MustCompile(`\*\*(.+?)\*\*`).ReplaceAllStringFunc(formatted, func(match string) string {
		parts := regexp.MustCompile(`^\*\*(.+?)\*\*$`).FindStringSubmatch(match)
		return nextPlaceholder("*" + EscapeMarkdownV2(parts[1]) + "*")
	})
	formatted = regexp.MustCompile(`__([^_\n]+)__`).ReplaceAllStringFunc(formatted, func(match string) string {
		parts := regexp.MustCompile(`^__([^_\n]+)__$`).FindStringSubmatch(match)
		return nextPlaceholder("__" + EscapeMarkdownV2(parts[1]) + "__")
	})
	formatted = regexp.MustCompile(`(^|[^A-Za-z0-9])_([^_\n]+)_([^A-Za-z0-9]|$)`).ReplaceAllStringFunc(formatted, func(match string) string {
		parts := regexp.MustCompile(`(^|[^A-Za-z0-9])_([^_\n]+)_([^A-Za-z0-9]|$)`).FindStringSubmatch(match)
		return parts[1] + nextPlaceholder("_"+EscapeMarkdownV2(parts[2])+"_") + parts[3]
	})
	formatted = regexp.MustCompile(`\*([^*\n]+)\*`).ReplaceAllStringFunc(formatted, func(match string) string {
		parts := regexp.MustCompile(`^\*([^*\n]+)\*$`).FindStringSubmatch(match)
		return nextPlaceholder("*" + EscapeMarkdownV2(parts[1]) + "*")
	})
	formatted = regexp.MustCompile(`~~(.+?)~~`).ReplaceAllStringFunc(formatted, func(match string) string {
		parts := regexp.MustCompile(`^~~(.+?)~~$`).FindStringSubmatch(match)
		return nextPlaceholder("~" + EscapeMarkdownV2(parts[1]) + "~")
	})
	formatted = regexp.MustCompile(`~([^~\n]+)~`).ReplaceAllStringFunc(formatted, func(match string) string {
		parts := regexp.MustCompile(`^~([^~\n]+)~$`).FindStringSubmatch(match)
		return nextPlaceholder("~" + EscapeMarkdownV2(parts[1]) + "~")
	})
	formatted = regexp.MustCompile(`\|\|(.+?)\|\|`).ReplaceAllStringFunc(formatted, func(match string) string {
		parts := regexp.MustCompile(`^\|\|(.+?)\|\|$`).FindStringSubmatch(match)
		return nextPlaceholder("||" + EscapeMarkdownV2(parts[1]) + "||")
	})
	formatted = regexp.MustCompile(`(?m)^\*\*>[\s\S]*?\|\|`).ReplaceAllStringFunc(formatted, func(match string) string {
		return nextPlaceholder(formatExpandableBlockQuoteMarkdownV2(match))
	})
	formatted = regexp.MustCompile(`(?m)^(>{1,3}) ?(.+)$`).ReplaceAllStringFunc(formatted, func(match string) string {
		parts := regexp.MustCompile(`^(>{1,3}) ?(.+)$`).FindStringSubmatch(match)
		return nextPlaceholder(parts[1] + " " + EscapeMarkdownV2(parts[2]))
	})
	formatted = EscapeMarkdownV2(formatted)

	for key, value := range placeholders {
		formatted = strings.ReplaceAll(formatted, EscapeMarkdownV2(key), value)
		formatted = strings.ReplaceAll(formatted, key, value)
	}

	return formatted
}

func EscapeMarkdownV2(text string) string {
	var escaped strings.Builder
	escaped.Grow(len(text))

	for i := 0; i < len(text); i++ {
		character := text[i]
		if character == '\\' && i+1 < len(text) && isMarkdownV2Escapable(text[i+1]) {
			escaped.WriteByte(character)
			escaped.WriteByte(text[i+1])
			i++
			continue
		}
		if isMarkdownV2Escapable(character) {
			escaped.WriteByte('\\')
		}
		escaped.WriteByte(character)
	}

	return escaped.String()
}

func isMarkdownV2Escapable(character byte) bool {
	return strings.ContainsRune(markdownV2EscapableCharacters, rune(character))
}

func PlainTextFromMarkdownV2(text string) string {
	cleaned := regexp.MustCompile(`\\([_*\[\]()~`+"`"+`>#\+\-=|{}.!\\])`).ReplaceAllString(text, "$1")
	cleaned = regexp.MustCompile(`\*\*(.+?)\*\*`).ReplaceAllString(cleaned, "$1")
	cleaned = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`).ReplaceAllString(cleaned, "$1")
	cleaned = regexp.MustCompile("```(?:[^\\n]*\\n)?([\\s\\S]*?)```").ReplaceAllString(cleaned, "$1")
	cleaned = regexp.MustCompile("`([^`]+)`").ReplaceAllString(cleaned, "$1")
	cleaned = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`).ReplaceAllString(cleaned, "$1 ($2)")
	cleaned = regexp.MustCompile(`__([^_\n]+)__`).ReplaceAllString(cleaned, "$1")
	cleaned = regexp.MustCompile(`(^|[^A-Za-z0-9])_([^_\n]+)_([^A-Za-z0-9]|$)`).ReplaceAllString(cleaned, "$1$2$3")
	cleaned = regexp.MustCompile(`\*([^*\n]+)\*`).ReplaceAllString(cleaned, "$1")
	cleaned = regexp.MustCompile(`~~(.+?)~~`).ReplaceAllString(cleaned, "$1")
	cleaned = regexp.MustCompile(`~([^~\n]+)~`).ReplaceAllString(cleaned, "$1")
	cleaned = regexp.MustCompile(`\|\|(.+?)\|\|`).ReplaceAllString(cleaned, "$1")

	return cleaned
}

func protectFencedCode(text string, nextPlaceholder func(string) string) string {
	return regexp.MustCompile("```(?:[^\\n]*\\n)?[\\s\\S]*?```").ReplaceAllStringFunc(text, func(match string) string {
		openingEnd := 3
		if newline := strings.Index(match[3:], "\n"); newline >= 0 {
			openingEnd = 3 + newline + 1
		}
		opening := match[:openingEnd]
		body := strings.TrimSuffix(match[openingEnd:], "```")
		body = strings.ReplaceAll(body, `\`, `\\`)
		body = strings.ReplaceAll(body, "`", "\\`")
		return nextPlaceholder(opening + body + "```")
	})
}

func formatExpandableBlockQuoteMarkdownV2(text string) string {
	body := strings.TrimSuffix(text, "||")
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		prefix := ">"
		if i == 0 && strings.HasPrefix(line, "**>") {
			prefix = "**>"
			line = strings.TrimPrefix(line, "**>")
		} else {
			line = strings.TrimPrefix(line, ">")
		}

		lines[i] = prefix + EscapeMarkdownV2(strings.TrimPrefix(line, " "))
	}

	return strings.Join(lines, "\n") + "||"
}
