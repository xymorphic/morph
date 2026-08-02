package logutils

import (
	"io"
	"os"
	"strings"
	"sync"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/xymorphic/morph/pkg/str"
)

var (
	loggerMu       sync.Mutex
	consoleOutput  io.Writer = os.Stderr
	consoleEnabled           = true
	fileOutput     io.Writer
	fileSettings   logFileSettings
	fileCloser     io.Closer

	currentProgramName = "agent"
	currentNoColor     = getCurrentNoColorSetting()
	configProvider     ConfigProvider

	mkdirAll           mkdirAllFunc         = os.MkdirAll
	newLogFileWriter   newLogFileWriterFunc = newLumberjackFileWriter
	defaultFileEnabled                      = func() bool { return !isGoTestProcess() }
)

type Config struct {
	NoColor    bool
	LogFile    string
	MaxSizeMB  int
	MaxBackups int
	MaxAgeDays int
	Compress   bool
}

type ConfigProvider func() Config

func SetConfigProvider(provider ConfigProvider) ConfigProvider {
	loggerMu.Lock()
	defer loggerMu.Unlock()

	previous := configProvider
	configProvider = provider

	return previous
}

func InitLogger(programName string) *zerolog.Logger {
	return ConfigureLogger(programName, getCurrentNoColorSetting())
}

func SetOutput(out io.Writer) {
	loggerMu.Lock()
	defer loggerMu.Unlock()

	if out == nil {
		consoleOutput = os.Stderr
	} else {
		consoleOutput = out
	}

	configureLoggerLocked(currentProgramName, currentNoColor)
}

func SetConsoleEnabled(enabled bool) bool {
	loggerMu.Lock()
	defer loggerMu.Unlock()

	previous := consoleEnabled
	consoleEnabled = enabled
	configureLoggerLocked(currentProgramName, currentNoColor)

	return previous
}

func SetFileOutput(out io.Writer) {
	loggerMu.Lock()
	defer loggerMu.Unlock()

	closeFileLocked()
	fileOutput = out
	fileSettings = logFileSettings{}
	configureLoggerLocked(currentProgramName, currentNoColor)
}

func ConfigureLogger(programName string, noColor bool) *zerolog.Logger {
	programNameValue := str.String(programName)
	if programNameValue.Trim() == "" {
		programName = "agent"
	}

	loggerMu.Lock()
	defer loggerMu.Unlock()

	configureLoggerLocked(programName, getEffectiveNoColorSetting(noColor))

	return &log.Logger
}

func GetLogger(programName string) *zerolog.Logger {
	return ConfigureLogger(programName, getCurrentNoColorSetting())
}

func SetLogLevel(level string) {
	levelValue := str.String(level)
	switch levelValue.Normalized() {
	case "debug":
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case "warn":
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case "error":
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	default:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
}

func configureLoggerLocked(programName string, noColor bool) {
	currentProgramName = programName
	currentNoColor = noColor

	writers := make([]io.Writer, 0, 2)
	if consoleEnabled {
		writers = append(writers, newModuleEnsuringWriter(newConsoleWriter(consoleOutput, noColor), programName))
	}
	if writer := getFileOutputLocked(); writer != nil {
		writers = append(writers, newModuleEnsuringWriter(writer, programName))
	}
	if len(writers) == 0 {
		writers = append(writers, io.Discard)
	}

	log.Logger = zerolog.New(zerolog.MultiLevelWriter(writers...)).With().
		Timestamp().
		Logger()
}

func getCurrentNoColorSetting() bool {
	if configProvider == nil {
		return isGoTestProcess()
	}

	return configProvider().NoColor || isGoTestProcess()
}

func getEffectiveNoColorSetting(noColor bool) bool {
	return noColor || isGoTestProcess()
}

func isGoTestProcess() bool {
	return strings.HasSuffix(os.Args[0], ".test")
}
