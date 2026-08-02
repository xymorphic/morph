package config

import (
	"time"

	commandplan "github.com/xymorphic/morph/internal/command"
)

// RPCConfig contains daemon RPC address and port settings.
type RPCConfig struct {
	Address string `yaml:"address"`
	Port    int    `yaml:"port"`
}

// FSConfig contains filesystem access policy settings.
type FSConfig struct {
	NoProfileAccess bool     `yaml:"noProfileAccess"`
	Roots           []string `yaml:"roots"`
}

// ExecConfig contains command execution policy settings. The legacy allow, ask,
// and deny string lists are unsupported and rejected at validation.
type ExecConfig struct {
	Allow         []string               `yaml:"allow"`
	Ask           []string               `yaml:"ask"`
	Deny          []string               `yaml:"deny"`
	AllowCommands []commandplan.Selector `yaml:"allowCommands"`
	AskCommands   []commandplan.Selector `yaml:"askCommands"`
	DenyCommands  []commandplan.Selector `yaml:"denyCommands"`
	Shell         string                 `yaml:"shell"`
}

// StorageConfig selects the durable state backend.
type StorageConfig struct {
	Backend string `yaml:"backend"`
}

// SessionConfig contains turn limits, session instructions, and retention settings.
type SessionConfig struct {
	MaxIterations     int           `yaml:"maxIterations"`
	Instruct          string        `yaml:"instruct"`
	DefaultIdleExpiry time.Duration `yaml:"defaultIdleExpiry"`
	ArchiveRetention  time.Duration `yaml:"archiveRetention"`
}

// CapConfig contains capability overrides for filesystem, network, exec, memory, and browser access.
type CapConfig struct {
	Filesystem *bool `yaml:"fs"`
	Network    *bool `yaml:"net"`
	Exec       *bool `yaml:"exec"`
	Memory     *bool `yaml:"mem"`
	Browser    *bool `yaml:"browser"`
}

// LogConfig controls application logging.
type LogConfig struct {
	Level      string `yaml:"level"`
	NoColor    bool   `yaml:"noColor"`
	File       string `yaml:"file"`
	MaxSizeMB  int    `yaml:"maxSizeMB"`
	MaxBackups int    `yaml:"maxBackups"`
	MaxAgeDays int    `yaml:"maxAgeDays"`
	Compress   bool   `yaml:"compress"`
}

// DebugConfig toggles debug-only request logging.
type DebugConfig struct {
	Requests bool `yaml:"requests"`
}

type DevConfig struct {
	RetainUnsafe bool `yaml:"retainUnsafe"`
}

// TUIConfig contains terminal UI feature settings.
type TUIConfig struct {
	ThinkingComposer *bool `yaml:"thinkingComposer"`
}

// SafetyConfig toggles input, output, and PII safety checks.
type SafetyConfig struct {
	Input  *bool `yaml:"input"`
	Output *bool `yaml:"output"`
	PII    *bool `yaml:"pii"`
}

// RulesConfig lists additional workspace rule files to load.
type RulesConfig struct {
	Files []string `yaml:"files"`
}
