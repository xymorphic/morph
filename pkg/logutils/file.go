package logutils

import (
	"io"
	"os"
	"path/filepath"

	"github.com/xymorphic/morph/pkg/str"
	"gopkg.in/natefinch/lumberjack.v2"
)

const defaultLogFilename = "morph.log"
const defaultLogMaxSizeMB = 10
const defaultLogMaxBackups = 5
const defaultLogMaxAgeDays = 14
const defaultLogCompress = true

type mkdirAllFunc func(string, os.FileMode) error

type newLogFileWriterFunc func(logFileSettings) (io.WriteCloser, error)

type logFileSettings struct {
	path       string
	maxSizeMB  int
	maxBackups int
	maxAgeDays int
	compress   bool
}

func getFileOutputLocked() io.Writer {
	settings := getLogFileSettings()
	if fileOutput != nil {
		if fileSettings.path == "" || fileSettings == settings {
			return fileOutput
		}

		closeFileLocked()
		fileOutput = nil
		fileSettings = logFileSettings{}
	}
	if !defaultFileEnabled() {
		return nil
	}

	if err := mkdirAll(filepath.Dir(settings.path), 0o700); err != nil {
		return nil
	}

	file, err := newLogFileWriter(settings)
	if err != nil {
		return nil
	}

	fileOutput = file
	fileSettings = settings
	fileCloser = file

	return fileOutput
}

func newLumberjackFileWriter(settings logFileSettings) (io.WriteCloser, error) {
	return &lumberjack.Logger{
		Filename:   settings.path,
		MaxSize:    settings.maxSizeMB,
		MaxBackups: settings.maxBackups,
		MaxAge:     settings.maxAgeDays,
		Compress:   settings.compress,
	}, nil
}

func getLogFileSettings() logFileSettings {
	settings := logFileSettings{
		path:       getLogFilePath(),
		maxSizeMB:  defaultLogMaxSizeMB,
		maxBackups: defaultLogMaxBackups,
		maxAgeDays: defaultLogMaxAgeDays,
		compress:   defaultLogCompress,
	}
	if configProvider == nil {
		return settings
	}

	cfg := configProvider()
	if cfg.MaxSizeMB > 0 {
		settings.maxSizeMB = cfg.MaxSizeMB
	}
	if cfg.MaxBackups > 0 {
		settings.maxBackups = cfg.MaxBackups
	}
	if cfg.MaxAgeDays > 0 {
		settings.maxAgeDays = cfg.MaxAgeDays
	}
	settings.compress = cfg.Compress

	return settings
}

func getLogFilePath() string {
	if configProvider != nil {
		logFileValue := str.String(configProvider().LogFile)
		if path := logFileValue.Trim(); path != "" {
			return path
		}
	}

	home, err := os.UserHomeDir()
	homeValue := str.String(home)
	if err != nil || homeValue.Trim() == "" {
		return defaultLogFilename
	}

	return filepath.Join(home, ".morph", defaultLogFilename)
}

func closeFileLocked() {
	if fileCloser != nil {
		_ = fileCloser.Close()
	}
	fileCloser = nil
}
