package logutils

import (
	"fmt"
	"hash/fnv"
	"io"
	"time"

	"github.com/rs/zerolog"
	"github.com/xymorphic/morph/pkg/str"
)

const consoleModuleField = "module"

var consoleModuleColors = []int{
	32,
	33,
	34,
	35,
	36,
	92,
	93,
	94,
	95,
	96,
}

func newConsoleWriter(out io.Writer, noColor bool) zerolog.ConsoleWriter {
	return zerolog.ConsoleWriter{
		Out:        out,
		TimeFormat: time.RFC3339,
		NoColor:    noColor,
		PartsOrder: []string{
			zerolog.TimestampFieldName,
			zerolog.LevelFieldName,
			consoleModuleField,
			zerolog.MessageFieldName},
		FieldsExclude: []string{consoleModuleField},
		FormatPartValueByName: func(value any, name string) string {
			if name != consoleModuleField {
				return fmt.Sprintf("%s", value)
			}

			return formatConsoleModule(value, noColor)
		},
	}
}

func formatConsoleModule(value any, noColor bool) string {
	if value == nil {
		return ""
	}

	sprintValue := str.String(fmt.Sprint(value))
	module := sprintValue.Trim()
	if module == "" {
		return ""
	}

	formatted := "[" + module + "]"
	if noColor {
		return formatted
	}

	return fmt.Sprintf("\x1b[%dm%s\x1b[0m", consoleModuleColor(module), formatted)
}

func consoleModuleColor(module string) int {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(module))

	return consoleModuleColors[int(hash.Sum32())%len(consoleModuleColors)]
}
