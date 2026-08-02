package logutils

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/xymorphic/morph/pkg/str"
)

type ModuleLogger struct {
	module string
}

type moduleEnsuringWriter struct {
	writer io.Writer
	module string
}

func Module(module string) ModuleLogger {
	moduleValue := str.String(module)
	return ModuleLogger{module: moduleValue.Trim()}
}

func (logger ModuleLogger) Trace() *zerolog.Event {
	return logger.event().Trace()
}

func (logger ModuleLogger) Debug() *zerolog.Event {
	return logger.event().Debug()
}

func (logger ModuleLogger) Info() *zerolog.Event {
	return logger.event().Info()
}

func (logger ModuleLogger) Warn() *zerolog.Event {
	return logger.event().Warn()
}

func (logger ModuleLogger) Error() *zerolog.Event {
	return logger.event().Error()
}

func (logger ModuleLogger) Fatal() *zerolog.Event {
	return logger.event().Fatal()
}

func (logger ModuleLogger) Panic() *zerolog.Event {
	return logger.event().Panic()
}

func (logger ModuleLogger) Logger() *zerolog.Logger {
	return logger.event()
}

func (logger ModuleLogger) event() *zerolog.Logger {
	if logger.module == "" {
		return &log.Logger
	}

	moduleLogger := log.With().Str("module", logger.module).Logger()
	return &moduleLogger
}

func newModuleEnsuringWriter(writer io.Writer, module string) io.Writer {
	if writer == nil {
		return io.Discard
	}

	moduleValue2 := str.String(module)
	return moduleEnsuringWriter{writer: writer, module: moduleValue2.Trim()}
}

func (writer moduleEnsuringWriter) Write(p []byte) (int, error) {
	if writer.module == "" {
		n, err := writer.writer.Write(p)
		if n > len(p) {
			n = len(p)
		}
		return n, err
	}

	output := ensureLogModule(p, writer.module)
	_, err := writer.writer.Write(output)
	if err != nil {
		return 0, err
	}

	return len(p), nil
}

func ensureLogModule(input []byte, module string) []byte {
	var trailing []byte
	trimmed := bytes.TrimRight(input, "\r\n")
	if len(trimmed) < len(input) {
		trailing = input[len(trimmed):]
	}

	var fields map[string]any
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return input
	}
	if _, ok := fields[consoleModuleField]; !ok {
		fields[consoleModuleField] = module
	}

	output, err := json.Marshal(fields)
	if err != nil {
		return input
	}

	return append(output, trailing...)
}
