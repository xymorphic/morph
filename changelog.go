package changelog

import (
	_ "embed"
	"strings"

	"github.com/xymorphic/morph/pkg/str"
)

//go:embed CHANGELOG.md
var content string

func Latest() string {
	return latestSection(content)
}

func latestSection(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	lines := strings.Split(value, "\n")

	start := -1
	for index, line := range lines {
		lineValue := str.String(line)
		if strings.HasPrefix(lineValue.Trim(), "## ") {
			start = index
			break
		}
	}
	if start < 0 {
		valueText := str.String(value)
		return valueText.Trim()
	}

	end := len(lines)
	for index := start + 1; index < len(lines); index++ {
		linesValue := str.String(lines[index])
		if strings.HasPrefix(linesValue.Trim(), "## ") {
			end = index
			break
		}
	}
	joinValue := str.String(strings.Join(lines[start:end], "\n"))
	return joinValue.Trim()
}
