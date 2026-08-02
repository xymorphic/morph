package logutils

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/require"
	"github.com/xymorphic/morph/pkg/str"
)

func TestSetOutput_SetsCustomConsoleWriterAndDefaultsToStderr(t *testing.T) {
	restoreLogger(t)

	buf := &bytes.Buffer{}
	SetOutput(buf)
	require.Same(t, buf, consoleOutput)

	SetOutput(nil)
	require.Same(t, os.Stderr, consoleOutput)
}

func TestConfigureLogger_WritesConsoleLog(t *testing.T) {
	restoreLogger(t)

	buf := &bytes.Buffer{}
	SetOutput(buf)

	logger := ConfigureLogger("   ", true)
	require.NotNil(t, logger)

	log.Info().Msg("Hello")

	output := buf.String()
	require.Contains(t, output, "Hello")
	require.Contains(t, output, "[agent]")
	require.NotContains(t, output, "program=")
	require.NotContains(t, output, "\x1b[")
}

func TestConfigureLogger_WritesConsoleAndFileSinks(t *testing.T) {
	restoreLogger(t)

	console := &bytes.Buffer{}
	file := &bytes.Buffer{}
	SetOutput(console)
	SetFileOutput(file)

	ConfigureLogger("svc", true)
	log.Info().Msg("Dual sink")

	require.Contains(t, console.String(), "Dual sink")
	require.Contains(t, console.String(), "[svc]")
	require.Contains(t, file.String(), `"message":"Dual sink"`)
	require.Contains(t, file.String(), `"module":"svc"`)
	require.NotContains(t, file.String(), `"program"`)
}

func TestConfigureLogger_RendersConsoleModuleAsColumn(t *testing.T) {
	restoreLogger(t)

	console := &bytes.Buffer{}
	SetOutput(console)
	ConfigureLogger("svc", true)

	Module("daemon").Info().Str("attr", "value").Msg("Configuration loaded")

	output := console.String()
	require.Contains(t, output, "INF [daemon] Configuration loaded")
	require.Contains(t, output, "attr=value")
	require.NotContains(t, output, "module=daemon")
	require.NotContains(t, output, "\x1b[")
}

func TestConfigureLogger_DisablesConsoleWithoutDisablingFile(t *testing.T) {
	restoreLogger(t)

	console := &bytes.Buffer{}
	file := &bytes.Buffer{}
	SetOutput(console)
	SetFileOutput(file)
	SetConsoleEnabled(false)

	ConfigureLogger("svc", true)
	log.Info().Msg("File only")

	require.Empty(t, console.String())
	require.Contains(t, file.String(), `"message":"File only"`)
	require.Contains(t, file.String(), `"module":"svc"`)
	require.NotContains(t, file.String(), `"program"`)
}

func TestModule_AddsModuleField(t *testing.T) {
	restoreLogger(t)

	file := &bytes.Buffer{}
	SetFileOutput(file)
	SetConsoleEnabled(false)
	ConfigureLogger("svc", true)

	Module("daemon").Info().Msg("Module log")

	output := file.String()
	require.Contains(t, output, `"message":"Module log"`)
	require.Contains(t, output, `"module":"daemon"`)
	require.NotContains(t, output, `"program"`)
}

func TestModuleLogger_AddsModuleField(t *testing.T) {
	restoreLogger(t)

	file := &bytes.Buffer{}
	SetFileOutput(file)
	SetConsoleEnabled(false)
	ConfigureLogger("svc", true)

	Module("environment").Logger().Info().Msg("Logger module")

	output := file.String()
	require.Contains(t, output, `"message":"Logger module"`)
	require.Contains(t, output, `"module":"environment"`)
}

func TestModule_EmptyModuleUsesBaseLogger(t *testing.T) {
	restoreLogger(t)

	file := &bytes.Buffer{}
	SetFileOutput(file)
	SetConsoleEnabled(false)
	ConfigureLogger("svc", true)

	Module(" ").Info().Msg("Base logger")

	output := file.String()
	require.Contains(t, output, `"message":"Base logger"`)
	require.Contains(t, output, `"module":"svc"`)
	require.NotContains(t, output, `"program"`)
}

func TestModule_LevelMethodsAddModuleField(t *testing.T) {
	restoreLogger(t)

	originalLevel := zerolog.GlobalLevel()
	t.Cleanup(func() {
		zerolog.SetGlobalLevel(originalLevel)
	})

	file := &bytes.Buffer{}
	SetFileOutput(file)
	SetConsoleEnabled(false)
	ConfigureLogger("svc", true)
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

	logger := Module("levels")
	logger.Trace().Msg("trace message")
	logger.Debug().Msg("debug message")
	logger.Info().Msg("info message")
	logger.Warn().Msg("warn message")
	logger.Error().Msg("error message")
	require.NotNil(t, logger.Fatal())
	require.NotNil(t, logger.Panic())

	output := file.String()
	require.Contains(t, output, `"level":"trace"`)
	require.Contains(t, output, `"message":"trace message"`)
	require.Contains(t, output, `"level":"debug"`)
	require.Contains(t, output, `"message":"debug message"`)
	require.Contains(t, output, `"level":"info"`)
	require.Contains(t, output, `"message":"info message"`)
	require.Contains(t, output, `"level":"warn"`)
	require.Contains(t, output, `"message":"warn message"`)
	require.Contains(t, output, `"level":"error"`)
	require.Contains(t, output, `"message":"error message"`)
	require.Contains(t, output, `"module":"levels"`)
}

func TestConfigureLogger_UsesConfiguredLogFile(t *testing.T) {
	restoreLogger(t)

	path := filepath.Join(t.TempDir(), "morph-test.log")
	setTestLogConfig(Config{LogFile: path})
	defaultFileEnabled = func() bool { return true }

	ConfigureLogger("svc", true)
	log.Info().Msg("File path")

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(data), `"message":"File path"`)
	require.Contains(t, string(data), `"module":"svc"`)
	require.NotContains(t, string(data), `"program"`)
}

func TestConfigureLogger_DefaultsLogFileToActiveProfileHome(t *testing.T) {
	restoreLogger(t)

	home := t.TempDir()
	setTestLogConfig(Config{LogFile: filepath.Join(home, defaultLogFilename)})
	defaultFileEnabled = func() bool { return true }

	ConfigureLogger("svc", true)
	log.Info().Msg("Default file path")

	data, err := os.ReadFile(filepath.Join(home, defaultLogFilename))
	require.NoError(t, err)
	require.Contains(t, string(data), `"message":"Default file path"`)
	require.Contains(t, string(data), `"module":"svc"`)
}

func TestConfigureLogger_ReopensWhenConfiguredLogFileChanges(t *testing.T) {
	restoreLogger(t)

	dir := t.TempDir()
	firstPath := filepath.Join(dir, "first.log")
	secondPath := filepath.Join(dir, "second.log")
	setTestLogConfig(Config{LogFile: firstPath})
	defaultFileEnabled = func() bool { return true }

	ConfigureLogger("svc", true)
	log.Info().Msg("First file")

	setTestLogConfig(Config{LogFile: secondPath})
	ConfigureLogger("svc", true)
	log.Info().Msg("Second file")

	firstData, err := os.ReadFile(firstPath)
	require.NoError(t, err)
	secondData, err := os.ReadFile(secondPath)
	require.NoError(t, err)
	require.Contains(t, string(firstData), `"message":"First file"`)
	require.Contains(t, string(firstData), `"module":"svc"`)
	require.NotContains(t, string(firstData), `"message":"Second file"`)
	require.Contains(t, string(secondData), `"message":"Second file"`)
	require.Contains(t, string(secondData), `"module":"svc"`)
}

func TestConfigureLogger_ReopensWhenLogRotationSettingsChange(t *testing.T) {
	restoreLogger(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "morph.log")
	setTestLogConfig(Config{
		LogFile:    path,
		MaxSizeMB:  11,
		MaxBackups: 7,
		MaxAgeDays: 21,
		Compress:   true,
	})
	defaultFileEnabled = func() bool { return true }

	var created []logFileSettings
	newLogFileWriter = func(settings logFileSettings) (io.WriteCloser, error) {
		created = append(created, settings)
		return nopWriteCloser{Writer: &bytes.Buffer{}}, nil
	}

	ConfigureLogger("svc", true)
	setTestLogConfig(Config{
		LogFile:    path,
		MaxSizeMB:  12,
		MaxBackups: 7,
		MaxAgeDays: 21,
		Compress:   true,
	})
	ConfigureLogger("svc", true)

	require.Len(t, created, 2)
	require.Equal(t, logFileSettings{
		path:       path,
		maxSizeMB:  11,
		maxBackups: 7,
		maxAgeDays: 21,
		compress:   true,
	}, created[0])
	require.Equal(t, logFileSettings{
		path:       path,
		maxSizeMB:  12,
		maxBackups: 7,
		maxAgeDays: 21,
		compress:   true,
	}, created[1])
}

func TestConfigureLogger_FallsBackWhenLogDirectoryCannotBeCreated(t *testing.T) {
	restoreLogger(t)

	console := &bytes.Buffer{}
	SetOutput(console)
	setTestLogConfig(Config{LogFile: filepath.Join(t.TempDir(), "morph.log")})
	defaultFileEnabled = func() bool { return true }
	mkdirAll = func(string, os.FileMode) error {
		return errors.New("mkdir failed")
	}

	ConfigureLogger("svc", true)
	log.Info().Msg("Console fallback")

	require.Contains(t, console.String(), "Console fallback")
}

func TestConfigureLogger_FallsBackWhenLogFileCannotBeOpened(t *testing.T) {
	restoreLogger(t)

	console := &bytes.Buffer{}
	SetOutput(console)
	setTestLogConfig(Config{LogFile: filepath.Join(t.TempDir(), "morph.log")})
	defaultFileEnabled = func() bool { return true }
	newLogFileWriter = func(logFileSettings) (io.WriteCloser, error) {
		return nil, errors.New("open failed")
	}

	ConfigureLogger("svc", true)
	log.Info().Msg("Open fallback")

	require.Contains(t, console.String(), "Open fallback")
}

func TestConfigureLogger_UsesDiscardWhenConsoleAndFileAreDisabled(t *testing.T) {
	restoreLogger(t)

	SetConsoleEnabled(false)
	defaultFileEnabled = func() bool { return false }

	ConfigureLogger("svc", true)
	log.Info().Msg("Discarded")
}

func TestInitLogger_UsesCurrentNoColorSetting(t *testing.T) {
	restoreLogger(t)

	setTestLogConfig(Config{NoColor: true})
	buf := &bytes.Buffer{}
	SetOutput(buf)

	logger := InitLogger("svc")
	require.NotNil(t, logger)

	log.Info().Msg("Hello")
	output := buf.String()
	require.NotContains(t, output, "\x1b[")
}

func TestGetLogger_UsesCurrentNoColorSetting(t *testing.T) {
	restoreLogger(t)

	setTestLogConfig(Config{NoColor: false})
	buf := &bytes.Buffer{}
	SetOutput(buf)

	logger := GetLogger("svc")
	require.NotNil(t, logger)

	log.Info().Msg("Hello")
	output := buf.String()
	require.NotContains(t, output, "\x1b[")
}

func TestSetLogLevel_MapsLevels(t *testing.T) {
	original := zerolog.GlobalLevel()
	t.Cleanup(func() {
		zerolog.SetGlobalLevel(original)
	})

	tests := []struct {
		name     string
		input    string
		expected zerolog.Level
	}{
		{name: "debug", input: "debug", expected: zerolog.DebugLevel},
		{name: "warn", input: " warn ", expected: zerolog.WarnLevel},
		{name: "error", input: "ERROR", expected: zerolog.ErrorLevel},
		{name: "default", input: "invalid", expected: zerolog.InfoLevel},
		{name: "empty", input: "", expected: zerolog.InfoLevel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetLogLevel(tt.input)
			require.Equal(t, tt.expected, zerolog.GlobalLevel())
		})
	}
}

func TestNewConsoleWriter_ConfiguresFields(t *testing.T) {
	out := &bytes.Buffer{}
	writer := newConsoleWriter(out, true)

	require.Same(t, out, writer.Out)
	require.Equal(t, time.RFC3339, writer.TimeFormat)
	require.True(t, writer.NoColor)
	require.Equal(t, []string{"time", "level", "module", "message"}, writer.PartsOrder)
	require.Equal(t, []string{"module"}, writer.FieldsExclude)
}

func TestFormatConsoleModule_UsesDeterministicColorAndHonorsNoColor(t *testing.T) {
	first := formatConsoleModule("daemon", false)
	second := formatConsoleModule("daemon", false)

	require.Equal(t, first, second)
	require.Contains(t, first, "\x1b[")
	require.Contains(t, first, "[daemon]")
	require.Equal(t, "[daemon]", formatConsoleModule("daemon", true))
	require.Empty(t, formatConsoleModule(" ", false))
	require.Empty(t, formatConsoleModule(nil, false))

	redCodes := map[int]bool{31: true, 91: true}
	for _, module := range []string{"agent", "agent.summary", "daemon", "model.openai"} {
		colorCode := consoleModuleANSIColor(t, formatConsoleModule(module, false))
		require.False(t, redCodes[colorCode], "module %q used red ANSI color %d", module, colorCode)
	}
}

func consoleModuleANSIColor(t *testing.T, formatted string) int {
	t.Helper()

	matches := regexp.MustCompile(`\x1b\[(\d+)m`).FindStringSubmatch(formatted)
	require.Len(t, matches, 2)
	colorCode, err := strconv.Atoi(matches[1])
	require.NoError(t, err)
	return colorCode
}

func TestEnsureLogModule_AddsFallbackModuleOnlyWhenMissing(t *testing.T) {
	withModule := ensureLogModule([]byte(`{"level":"info","module":"daemon","message":"hello"}`+"\n"), "morph")
	withModuleValue := str.String(string(withModule))
	require.JSONEq(t, `{"level":"info","module":"daemon","message":"hello"}`, withModuleValue.Trim())
	require.True(t, bytes.HasSuffix(withModule, []byte("\n")))

	withoutModule := ensureLogModule([]byte(`{"level":"info","message":"hello"}`), "morph")
	require.JSONEq(t, `{"level":"info","module":"morph","message":"hello"}`, string(withoutModule))

	invalid := []byte("not-json\n")
	require.Equal(t, invalid, ensureLogModule(invalid, "morph"))
}

func TestCurrentNoColorSetting_UsesConfig(t *testing.T) {
	restoreLogger(t)

	SetConfigProvider(nil)
	require.True(t, getCurrentNoColorSetting())

	setTestLogConfig(Config{NoColor: true})
	require.True(t, getCurrentNoColorSetting())

	setTestLogConfig(Config{NoColor: false})
	require.True(t, getCurrentNoColorSetting())
}

func TestConfigureLogger_UsesConfiguredOutputWriter(t *testing.T) {
	restoreLogger(t)

	reader, writer := io.Pipe()
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})

	SetOutput(writer)
	ConfigureLogger("svc", true)

	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 256)
		n, _ := reader.Read(buf)
		done <- string(buf[:n])
	}()

	log.Info().Msg("Pipe-test")
	_ = writer.Close()

	output := <-done
	require.Contains(t, output, "Pipe-test")
	require.Contains(t, output, "[svc]")
}

func restoreLogger(t *testing.T) {
	t.Helper()

	originalConsoleOutput := consoleOutput
	originalConsoleEnabled := consoleEnabled
	originalFileOutput := fileOutput
	originalFileSettings := fileSettings
	originalFileCloser := fileCloser
	originalLogger := log.Logger
	originalConfigProvider := configProvider
	originalDefaultFileEnabled := defaultFileEnabled
	originalMkdirAll := mkdirAll
	originalNewLogFileWriter := newLogFileWriter
	t.Cleanup(func() {
		closeFileLocked()
		consoleOutput = originalConsoleOutput
		consoleEnabled = originalConsoleEnabled
		fileOutput = originalFileOutput
		fileSettings = originalFileSettings
		fileCloser = originalFileCloser
		log.Logger = originalLogger
		configProvider = originalConfigProvider
		defaultFileEnabled = originalDefaultFileEnabled
		mkdirAll = originalMkdirAll
		newLogFileWriter = originalNewLogFileWriter
	})

	defaultFileEnabled = func() bool { return false }
	consoleOutput = os.Stderr
	consoleEnabled = true
	fileOutput = nil
	fileSettings = logFileSettings{}
	fileCloser = nil
	configProvider = nil
	mkdirAll = os.MkdirAll
	newLogFileWriter = newLumberjackFileWriter
}

func setTestLogConfig(cfg Config) {
	SetConfigProvider(func() Config {
		return cfg
	})
}

type nopWriteCloser struct {
	io.Writer
}

func (writer nopWriteCloser) Close() error {
	return nil
}
