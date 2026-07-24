package cli

import (
	"strings"

	cli "github.com/urfave/cli/v3"

	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/constants"
	"github.com/wandxy/morph/internal/profile"
	"github.com/wandxy/morph/pkg/str"
)

// AppDescription is the package-level app description constant.
const AppDescription = constants.AppDescription

// RootFlags returns CLI flags shared by root commands.
func RootFlags(envFile, configFile *string) []cli.Flag {
	flags := []cli.Flag{
		ProfileFlag(),
		&cli.StringFlag{
			Name:  "name",
			Usage: "The name of your morph",
			Value: config.Get().Name,
		},
		&cli.StringFlag{
			Name:    "model.provider",
			Aliases: []string{"provider"},
			Usage:   "Model provider: openrouter (default), openai, anthropic, or ollama",
			Value:   config.Get().Models.Main.Provider,
			Hidden:  true,
		},
		&cli.StringFlag{
			Name:   "model.api-key",
			Usage:  "Authentication key for the selected model provider",
			Hidden: true,
		},
		&cli.StringFlag{
			Name:  "model",
			Usage: "Model ID to send to the provider, for example gpt-4o-mini",
			Value: config.Get().Models.Main.Name,
		},
		&cli.BoolFlag{
			Name:   "pull",
			Usage:  "Pull the selected Ollama model before sending the request when it is missing locally",
			Hidden: true,
		},
		&cli.BoolFlag{
			Name:   "pull-quiet",
			Usage:  "Suppress Ollama model pull progress output",
			Hidden: true,
		},
		&cli.StringFlag{
			Name:  "model.summary",
			Usage: "Optional model ID for compaction summarization; defaults to --model when unset",
			Value: config.Get().Models.Summary.Name,
		},
		&cli.BoolFlag{
			Name:  "model.stream",
			Usage: "Stream assistant text responses as they are generated",
			Value: config.Get().StreamEnabled(),
		},
		&cli.StringFlag{
			Name:    "model.base-url",
			Aliases: []string{"base-url"},
			Usage:   "Base URL for the model provider API",
			Value:   config.Get().Models.Main.BaseURL,
			Hidden:  true,
		},
		&cli.StringFlag{
			Name:   "model.summary-provider",
			Usage:  "Optional provider for compaction/summary calls; defaults to --model.provider when unset",
			Value:  config.Get().Models.Summary.Provider,
			Hidden: true,
		},
		&cli.StringFlag{
			Name:   "model.summary-base-url",
			Usage:  "Base URL for the summary provider when it differs from the main provider",
			Value:  config.Get().Models.Summary.BaseURL,
			Hidden: true,
		},
		&cli.StringFlag{
			Name:   "model.summary-api",
			Usage:  "API for compaction/summary (openai-completions or openai-responses); defaults to --model.api when unset",
			Value:  config.Get().Models.Summary.API,
			Hidden: true,
		},
		&cli.StringFlag{
			Name:   "model.api",
			Usage:  "Provider API: openai-completions or openai-responses",
			Value:  config.Get().Models.Main.API,
			Hidden: true,
		},
		&cli.IntFlag{
			Name:   "model.max-retries",
			Usage:  "Maximum SDK retry attempts for model requests; set 0 to disable retries",
			Value:  config.Get().ModelMaxRetriesEffective(),
			Hidden: true,
		},
		&cli.StringFlag{
			Name:  "rpc.address",
			Usage: "Bind address for the RPC service",
			Value: config.Get().RPC.Address,
		},
		&cli.IntFlag{
			Name:  "rpc.port",
			Usage: "Bind port for the RPC service",
			Value: config.Get().RPC.Port,
		},
		&cli.BoolFlag{
			Name:  "gateway.enabled",
			Usage: "Enable the external client gateway inside the daemon",
			Value: config.Get().Gateway.Enabled,
		},
		&cli.StringFlag{
			Name:  "gateway.address",
			Usage: "Bind address for the external client gateway",
			Value: config.Get().Gateway.Address,
		},
		&cli.IntFlag{
			Name:  "gateway.port",
			Usage: "Bind port for the external client gateway",
			Value: config.Get().Gateway.Port,
		},
		&cli.StringFlag{
			Name:   "gateway.auth-token",
			Usage:  "Bearer token for generic HTTP gateway requests",
			Value:  config.Get().Gateway.AuthToken,
			Hidden: true,
		},
		&cli.BoolFlag{
			Name:  "gateway.telegram.enabled",
			Usage: "Enable Telegram gateway ingress",
			Value: config.Get().Gateway.Telegram.Enabled,
		},
		&cli.StringFlag{
			Name:  "gateway.telegram.mode",
			Usage: "Telegram ingress mode: polling or webhook",
			Value: config.Get().Gateway.Telegram.Mode,
		},
		&cli.StringFlag{
			Name:   "gateway.telegram.bot-token",
			Usage:  "Telegram bot token",
			Value:  config.Get().Gateway.Telegram.BotToken,
			Hidden: true,
		},
		&cli.StringFlag{
			Name:   "gateway.telegram.webhook-secret",
			Usage:  "Telegram webhook secret token",
			Value:  config.Get().Gateway.Telegram.WebhookSecret,
			Hidden: true,
		},
		&cli.BoolFlag{
			Name:  "gateway.slack.enabled",
			Usage: "Enable Slack gateway ingress",
			Value: config.Get().Gateway.Slack.Enabled,
		},
		&cli.StringFlag{
			Name:  "gateway.slack.mode",
			Usage: "Slack ingress mode: socket or http",
			Value: config.Get().Gateway.Slack.Mode,
		},
		&cli.StringFlag{
			Name:  "gateway.slack.response-mode",
			Usage: "Slack response placement: thread or message",
			Value: config.Get().Gateway.Slack.ResponseMode,
		},
		&cli.StringFlag{
			Name:   "gateway.slack.bot-token",
			Usage:  "Slack bot token",
			Value:  config.Get().Gateway.Slack.BotToken,
			Hidden: true,
		},
		&cli.StringFlag{
			Name:   "gateway.slack.app-token",
			Usage:  "Slack app token for socket mode",
			Value:  config.Get().Gateway.Slack.AppToken,
			Hidden: true,
		},
		&cli.StringFlag{
			Name:   "gateway.slack.signing-secret",
			Usage:  "Slack signing secret for HTTP mode",
			Value:  config.Get().Gateway.Slack.SigningSecret,
			Hidden: true,
		},
		&cli.IntFlag{
			Name:   "max-iterations",
			Usage:  "Maximum model iterations allowed in a tool-calling loop",
			Value:  config.Get().Session.MaxIterations,
			Hidden: true,
		},
		&cli.StringFlag{
			Name:  "log.level",
			Usage: "Set the minimum log level: debug, info, warn, or error",
			Value: config.Get().Log.Level,
		},
		&cli.StringFlag{
			Name:   "log.file",
			Usage:  "Write logs to this file in addition to console output",
			Value:  config.Get().Log.File,
			Hidden: true,
		},
		&cli.IntFlag{
			Name:   "log.max-size-mb",
			Usage:  "Rotate the log file after it reaches this size in megabytes",
			Value:  config.Get().Log.MaxSizeMB,
			Hidden: true,
		},
		&cli.IntFlag{
			Name:   "log.max-backups",
			Usage:  "Maximum number of rotated log files to retain",
			Value:  config.Get().Log.MaxBackups,
			Hidden: true,
		},
		&cli.IntFlag{
			Name:   "log.max-age-days",
			Usage:  "Maximum number of days to retain rotated log files",
			Value:  config.Get().Log.MaxAgeDays,
			Hidden: true,
		},
		&cli.BoolFlag{
			Name:   "log.compress",
			Usage:  "Compress rotated log files",
			Value:  config.Get().Log.Compress,
			Hidden: true,
		},
		&cli.BoolFlag{
			Name:   "log.no-color",
			Usage:  "Emit plain log output without ANSI color codes",
			Hidden: true,
		},
		&cli.BoolFlag{
			Name:   "no-color",
			Usage:  "Emit plain terminal output without ANSI color codes",
			Hidden: true,
		},
		&cli.BoolFlag{
			Name:  "debug.requests",
			Usage: "Log sanitized model request payloads at debug level",
		},
		&cli.BoolFlag{
			Name:  "trace.enabled",
			Usage: "Persist sanitized per-session trace events for debugging",
		},
		&cli.BoolFlag{
			Name:   "trace.disk.enabled",
			Usage:  "Persist debug trace events as JSONL files",
			Hidden: true,
		},
		&cli.StringFlag{
			Name:   "trace.disk.dir",
			Usage:  "Directory for persisted debug trace JSONL files",
			Value:  config.Get().Trace.Disk.Dir,
			Hidden: true,
		},
		&cli.BoolFlag{
			Name:   "trace.database.enabled",
			Usage:  "Persist debug trace events to state storage",
			Hidden: true,
		},
		&cli.IntFlag{
			Name:   "trace.database.max-events-per-session",
			Usage:  "Maximum stored debug trace events per session",
			Value:  config.Get().Trace.Database.MaxEventsPerSession,
			Hidden: true,
		},
		&cli.BoolFlag{
			Name:   "tui.thinking-composer",
			Usage:  "Animate the TUI composer border while the model is thinking",
			Value:  config.Get().TUIThinkingComposerEnabled(),
			Hidden: true,
		},
		&cli.StringFlag{
			Name:   "web.provider",
			Usage:  "Web provider: firecrawl, parallel, tavily, exa, or native",
			Value:  config.Get().Web.Provider,
			Hidden: true,
		},
		&cli.StringFlag{
			Name:   "web.key",
			Usage:  "Authentication key for the selected web provider",
			Hidden: true,
		},
		&cli.StringFlag{
			Name:   "web.base-url",
			Usage:  "Base URL for the selected web provider API",
			Value:  config.Get().Web.BaseURL,
			Hidden: true,
		},
		&cli.IntFlag{
			Name:   "web.max-char-per-result",
			Usage:  "Maximum characters returned per web search result",
			Value:  config.Get().Web.MaxCharPerResult,
			Hidden: true,
		},
		&cli.IntFlag{
			Name:   "web.max-extract-char-per-result",
			Usage:  "Maximum characters returned per web extraction result",
			Value:  config.Get().Web.MaxExtractCharPerResult,
			Hidden: true,
		},
		&cli.IntFlag{
			Name:   "web.max-extract-response-bytes",
			Usage:  "Maximum raw response bytes processed per web extraction result",
			Value:  config.Get().Web.MaxExtractResponseBytes,
			Hidden: true,
		},
		&cli.DurationFlag{
			Name:   "web.cache-ttl",
			Usage:  "Time to keep successful web search and extraction results in the in-process cache",
			Value:  config.Get().Web.CacheTTL,
			Hidden: true,
		},
		&cli.BoolFlag{
			Name:   "web.blocked-domains-enabled",
			Usage:  "Enable configured domain blocklist checks for web search and extraction",
			Value:  config.Get().Web.BlockedDomainsEnabled,
			Hidden: true,
		},
		&cli.StringFlag{
			Name:   "web.blocked-domains",
			Usage:  "Comma-separated domains blocked from web search and extraction results",
			Value:  strings.Join(config.Get().Web.BlockedDomains, ","),
			Hidden: true,
		},
		&cli.StringFlag{
			Name:   "web.blocked-domain-files",
			Usage:  "Comma-separated files containing domains blocked from web search and extraction results",
			Value:  strings.Join(config.Get().Web.BlockedDomainFiles, ","),
			Hidden: true,
		},
		&cli.StringFlag{
			Name:   "web.native-allowed-hosts",
			Usage:  "Comma-separated host patterns the native web extractor may fetch; when set, other hosts are rejected",
			Value:  strings.Join(config.Get().Web.NativeAllowedHosts, ","),
			Hidden: true,
		},
		&cli.StringFlag{
			Name:   "web.native-blocked-hosts",
			Usage:  "Comma-separated host patterns the native web extractor must never fetch",
			Value:  strings.Join(config.Get().Web.NativeBlockedHosts, ","),
			Hidden: true,
		},
		&cli.StringFlag{
			Name:   "web.native-allowed-host-files",
			Usage:  "Comma-separated files containing native web extractor host allowlist rules",
			Value:  strings.Join(config.Get().Web.NativeAllowedHostFiles, ","),
			Hidden: true,
		},
		&cli.StringFlag{
			Name:   "web.native-blocked-host-files",
			Usage:  "Comma-separated files containing native web extractor host denylist rules",
			Value:  strings.Join(config.Get().Web.NativeBlockedHostFiles, ","),
			Hidden: true,
		},
		&cli.IntFlag{
			Name:   "web.extract-min-summarize-chars",
			Usage:  "Minimum extracted content characters before optional web extraction summarization runs",
			Value:  config.Get().Web.ExtractMinSummarizeChars,
			Hidden: true,
		},
		&cli.IntFlag{
			Name:   "web.extract-max-summary-chars",
			Usage:  "Maximum characters returned by optional web extraction summaries",
			Value:  config.Get().Web.ExtractMaxSummaryChars,
			Hidden: true,
		},
		&cli.IntFlag{
			Name:   "web.extract-max-summary-chunk-chars",
			Usage:  "Maximum extracted content characters per optional summarization chunk",
			Value:  config.Get().Web.ExtractMaxSummaryChunkChars,
			Hidden: true,
		},
		&cli.IntFlag{
			Name:   "web.extract-refusal-threshold-chars",
			Usage:  "Extracted content character threshold above which optional summarization is refused",
			Value:  config.Get().Web.ExtractRefusalThresholdChars,
			Hidden: true,
		},
		&cli.StringFlag{
			Name:   "rules.files",
			Usage:  "Comma-separated rule file paths to load in addition to workspace defaults",
			Value:  strings.Join(config.Get().Rules.Files, ","),
			Hidden: true,
		},
		&cli.StringFlag{
			Name:   "platform",
			Usage:  "Active runtime platform used for tool filtering",
			Value:  config.Get().Platform,
			Hidden: true,
		},
		&cli.StringFlag{
			Name:   "fs.roots",
			Usage:  "Comma-separated filesystem roots allowed for file tools",
			Value:  strings.Join(config.Get().FS.Roots, ","),
			Hidden: true,
		},
		&cli.BoolFlag{
			Name:   "cap.fs",
			Usage:  "Enable filesystem tool capability filtering",
			Value:  getBoolValue(config.Get().Cap.Filesystem),
			Hidden: true,
		},
		&cli.BoolFlag{
			Name:   "cap.net",
			Usage:  "Enable network tool capability filtering",
			Value:  getBoolValue(config.Get().Cap.Network),
			Hidden: true,
		},
		&cli.BoolFlag{
			Name:   "cap.exec",
			Usage:  "Enable exec tool capability filtering",
			Value:  getBoolValue(config.Get().Cap.Exec),
			Hidden: true,
		},
		&cli.BoolFlag{
			Name:   "cap.mem",
			Usage:  "Enable memory tool capability filtering",
			Value:  getBoolValue(config.Get().Cap.Memory),
			Hidden: true,
		},
		&cli.BoolFlag{
			Name:   "cap.browser",
			Usage:  "Enable browser tool capability filtering",
			Value:  getBoolValue(config.Get().Cap.Browser),
			Hidden: true,
		},
		&cli.StringFlag{
			Name:   "storage.backend",
			Usage:  "Storage backend: memory or sqlite",
			Value:  config.Get().Storage.Backend,
			Hidden: true,
		},
		&cli.StringFlag{
			Name:   "memory.backend",
			Usage:  "Memory backend override: memory or sqlite",
			Value:  config.Get().Memory.Backend,
			Hidden: true,
		},
		&cli.DurationFlag{
			Name:   "session.default-idle-expiry",
			Usage:  "Idle duration before the default session is archived and cleared",
			Value:  config.Get().Session.DefaultIdleExpiry,
			Hidden: true,
		},
		&cli.DurationFlag{
			Name:   "session.archive-retention",
			Usage:  "How long archived default-session conversations are retained before deletion",
			Value:  config.Get().Session.ArchiveRetention,
			Hidden: true,
		},
		&cli.StringFlag{
			Name:  "session",
			Usage: "Session id to use for this chat request; defaults to the persistent default session",
		},
	}

	if envFile != nil {
		flags = append([]cli.Flag{
			&cli.StringFlag{
				Name:        "env-file",
				Usage:       "Load environment overrides from this .env file",
				Value:       ".env",
				Destination: envFile,
				Sources: cli.NewValueSourceChain(
					cli.EnvVar("MORPH_ENV_FILE"),
				),
			},
		}, flags...)
	}

	if configFile != nil {
		insertAt := 1
		if envFile == nil {
			insertAt = 0
		}
		configFlag := &cli.StringFlag{
			Name:        "config",
			Usage:       "Read base settings from this YAML config file",
			Value:       "config.yaml",
			Destination: configFile,
			Sources: cli.NewValueSourceChain(
				cli.EnvVar("MORPH_CONFIG"),
			),
		}
		flags = append(flags[:insertAt], append([]cli.Flag{configFlag}, flags[insertAt:]...)...)
	}

	return flags
}

func ChatFlag() cli.Flag {
	return &cli.BoolFlag{
		Name:    "chat",
		Aliases: []string{"c"},
		Usage:   "Send root arguments as a one-shot chat request",
	}
}

// ProfileFlag returns the persistent profile selection flag.
func ProfileFlag() cli.Flag {
	return &cli.StringFlag{
		Name:    "profile",
		Aliases: []string{"p"},
		Usage:   "Profile name for profile-local config, env, and runtime metadata",
		Sources: cli.NewValueSourceChain(
			cli.EnvVar(profile.EnvName),
		),
	}
}

// RequestInstructFlag returns the flag for one-turn instruction text.
func RequestInstructFlag() cli.Flag {
	return &cli.StringFlag{
		Name:  "instruct",
		Usage: "Per-request instruction appended after workspace rules and cleared when the response finishes",
		Value: config.Get().Session.Instruct,
	}
}

// PersistentInstructFlag returns the flag for persisted instruction text.
func PersistentInstructFlag() cli.Flag {
	return &cli.StringFlag{
		Name:  "instruct",
		Usage: "Server instruction appended after workspace rules and kept until the process exits",
		Value: config.Get().Session.Instruct,
	}
}

// ApplyConfigOverrides applies config overrides.
func ApplyConfigOverrides(cmd *cli.Command, cfg *config.Config) {
	if cfg == nil || cmd == nil {
		return
	}

	if cmd.IsSet("name") {
		literalValue := str.String(cmd.String("name"))
		cfg.Name = literalValue.Trim()
	}
	if cmd.IsSet("model") {
		literalValue2 := str.String(cmd.String("model"))
		cfg.Models.Main.Name = literalValue2.Trim()
	}
	if cmd.IsSet("model.summary") {
		literalValue3 := str.String(cmd.String("model.summary"))
		cfg.Models.Summary.Name = literalValue3.Trim()
	}
	if cmd.IsSet("model.stream") {
		cfg.Models.Main.Stream = new(cmd.Bool("model.stream"))
	}
	if cmd.IsSet("model.provider") {
		providerValue := str.String(cfg.Models.Main.Provider)
		previousProvider := providerValue.Normalized()
		literalValue4 := str.String(cmd.String("model.provider"))
		cfg.Models.Main.Provider = literalValue4.Trim()
		providerValue2 := str.String(cfg.Models.Main.Provider)
		nextProvider := providerValue2.Normalized()
		if nextProvider != "" && nextProvider != previousProvider {
			if !cmd.IsSet("model.api") {
				cfg.Models.Main.API = ""
			}
			if !cmd.IsSet("model.base-url") {
				cfg.Models.Main.BaseURL = ""
			}
		}
	}
	if cmd.IsSet("model.api-key") {
		providerValue3 := str.String(cfg.Models.Main.Provider)
		provider := providerValue3.Normalized()
		if provider == "" {
			return
		}
		if cfg.Models.Providers == nil {
			cfg.Models.Providers = make(map[string]config.ProviderModelConfig)
		}
		providerConfig := cfg.Models.Providers[provider]
		literalValue5 := str.String(cmd.String("model.api-key"))
		providerConfig.APIKey = literalValue5.Trim()
		cfg.Models.Providers[provider] = providerConfig
	}
	if cmd.IsSet("model.base-url") {
		literalValue6 := str.String(cmd.String("model.base-url"))
		cfg.Models.Main.BaseURL = literalValue6.Trim()
	}
	if cmd.IsSet("model.summary-provider") {
		literalValue7 := str.String(cmd.String("model.summary-provider"))
		cfg.Models.Summary.Provider = literalValue7.Trim()
	}
	if cmd.IsSet("model.summary-base-url") {
		literalValue8 := str.String(cmd.String("model.summary-base-url"))
		cfg.Models.Summary.BaseURL = literalValue8.Trim()
	}
	if cmd.IsSet("model.summary-api") {
		literalValue9 := str.String(cmd.String("model.summary-api"))
		cfg.Models.Summary.API = literalValue9.Trim()
	}
	if cmd.IsSet("model.api") {
		literalValue10 := str.String(cmd.String("model.api"))
		cfg.Models.Main.API = literalValue10.Trim()
	}
	if cmd.IsSet("model.max-retries") {
		retries := cmd.Int("model.max-retries")
		cfg.Models.MaxRetries = &retries
	}
	if cmd.IsSet("rpc.address") {
		literalValue11 := str.String(cmd.String("rpc.address"))
		cfg.RPC.Address = literalValue11.Trim()
	}
	if cmd.IsSet("rpc.port") {
		cfg.RPC.Port = cmd.Int("rpc.port")
	}
	if cmd.IsSet("gateway.enabled") {
		cfg.Gateway.Enabled = cmd.Bool("gateway.enabled")
	}
	if cmd.IsSet("gateway.address") {
		literalValue12 := str.String(cmd.String("gateway.address"))
		cfg.Gateway.Address = literalValue12.Trim()
	}
	if cmd.IsSet("gateway.port") {
		cfg.Gateway.Port = cmd.Int("gateway.port")
	}
	if cmd.IsSet("gateway.auth-token") {
		literalValue13 := str.String(cmd.String("gateway.auth-token"))
		cfg.Gateway.AuthToken = literalValue13.Trim()
	}
	if cmd.IsSet("gateway.telegram.enabled") {
		cfg.Gateway.Telegram.Enabled = cmd.Bool("gateway.telegram.enabled")
	}
	if cmd.IsSet("gateway.telegram.mode") {
		literalValue14 := str.String(cmd.String("gateway.telegram.mode"))
		cfg.Gateway.Telegram.Mode = literalValue14.Trim()
	}
	if cmd.IsSet("gateway.telegram.bot-token") {
		literalValue15 := str.String(cmd.String("gateway.telegram.bot-token"))
		cfg.Gateway.Telegram.BotToken = literalValue15.Trim()
	}
	if cmd.IsSet("gateway.telegram.webhook-secret") {
		literalValue16 := str.String(cmd.String("gateway.telegram.webhook-secret"))
		cfg.Gateway.Telegram.WebhookSecret = literalValue16.Trim()
	}
	if cmd.IsSet("gateway.slack.enabled") {
		cfg.Gateway.Slack.Enabled = cmd.Bool("gateway.slack.enabled")
	}
	if cmd.IsSet("gateway.slack.mode") {
		literalValue17 := str.String(cmd.String("gateway.slack.mode"))
		cfg.Gateway.Slack.Mode = literalValue17.Trim()
	}
	if cmd.IsSet("gateway.slack.response-mode") {
		literalValue18 := str.String(cmd.String("gateway.slack.response-mode"))
		cfg.Gateway.Slack.ResponseMode = literalValue18.Trim()
	}
	if cmd.IsSet("gateway.slack.bot-token") {
		literalValue19 := str.String(cmd.String("gateway.slack.bot-token"))
		cfg.Gateway.Slack.BotToken = literalValue19.Trim()
	}
	if cmd.IsSet("gateway.slack.app-token") {
		literalValue20 := str.String(cmd.String("gateway.slack.app-token"))
		cfg.Gateway.Slack.AppToken = literalValue20.Trim()
	}
	if cmd.IsSet("gateway.slack.signing-secret") {
		literalValue21 := str.String(cmd.String("gateway.slack.signing-secret"))
		cfg.Gateway.Slack.SigningSecret = literalValue21.Trim()
	}
	if cmd.IsSet("max-iterations") {
		cfg.Session.MaxIterations = cmd.Int("max-iterations")
	}
	if cmd.IsSet("log.level") {
		literalValue22 := str.String(cmd.String("log.level"))
		cfg.Log.Level = literalValue22.Trim()
	}
	if cmd.IsSet("log.file") {
		literalValue23 := str.String(cmd.String("log.file"))
		cfg.Log.File = literalValue23.Trim()
	}
	if cmd.IsSet("log.max-size-mb") {
		cfg.Log.MaxSizeMB = cmd.Int("log.max-size-mb")
	}
	if cmd.IsSet("log.max-backups") {
		cfg.Log.MaxBackups = cmd.Int("log.max-backups")
	}
	if cmd.IsSet("log.max-age-days") {
		cfg.Log.MaxAgeDays = cmd.Int("log.max-age-days")
	}
	if cmd.IsSet("log.compress") {
		cfg.Log.Compress = cmd.Bool("log.compress")
	}
	if cmd.IsSet("log.no-color") {
		cfg.Log.NoColor = cmd.Bool("log.no-color")
	}
	if cmd.IsSet("no-color") && cmd.Bool("no-color") {
		cfg.Log.NoColor = true
	}
	if cmd.IsSet("debug.requests") {
		cfg.Debug.Requests = cmd.Bool("debug.requests")
	}
	if cmd.IsSet("trace.enabled") {
		cfg.Trace.Enabled = cmd.Bool("trace.enabled")
	}
	if cmd.IsSet("trace.disk.enabled") {
		enabled := cmd.Bool("trace.disk.enabled")
		cfg.Trace.Disk.Enabled = &enabled
	}
	if cmd.IsSet("trace.disk.dir") {
		literalValue24 := str.String(cmd.String("trace.disk.dir"))
		cfg.Trace.Disk.Dir = literalValue24.Trim()
	}
	if cmd.IsSet("trace.database.enabled") {
		enabled := cmd.Bool("trace.database.enabled")
		cfg.Trace.Database.Enabled = &enabled
	}
	if cmd.IsSet("trace.database.max-events-per-session") {
		cfg.Trace.Database.MaxEventsPerSession = cmd.Int("trace.database.max-events-per-session")
	}
	if cmd.IsSet("tui.thinking-composer") {
		cfg.TUI.ThinkingComposer = new(cmd.Bool("tui.thinking-composer"))
	}
	if cmd.IsSet("web.provider") {
		literalValue25 := str.String(cmd.String("web.provider"))
		cfg.Web.Provider = literalValue25.Trim()
	}
	if cmd.IsSet("web.key") {
		literalValue26 := str.String(cmd.String("web.key"))
		cfg.Web.APIKey = literalValue26.Trim()
	}
	if cmd.IsSet("web.base-url") {
		literalValue27 := str.String(cmd.String("web.base-url"))
		cfg.Web.BaseURL = literalValue27.Trim()
	}
	if cmd.IsSet("web.max-char-per-result") {
		cfg.Web.MaxCharPerResult = cmd.Int("web.max-char-per-result")
	}
	if cmd.IsSet("web.max-extract-char-per-result") {
		cfg.Web.MaxExtractCharPerResult = cmd.Int("web.max-extract-char-per-result")
	}
	if cmd.IsSet("web.max-extract-response-bytes") {
		cfg.Web.MaxExtractResponseBytes = cmd.Int("web.max-extract-response-bytes")
	}
	if cmd.IsSet("web.cache-ttl") {
		cfg.Web.CacheTTL = cmd.Duration("web.cache-ttl")
	}
	if cmd.IsSet("web.blocked-domains-enabled") {
		cfg.Web.BlockedDomainsEnabled = cmd.Bool("web.blocked-domains-enabled")
	}
	if cmd.IsSet("web.blocked-domains") {
		cfg.Web.BlockedDomains = splitConfigCSVAndTrim(cmd.String("web.blocked-domains"))
	}
	if cmd.IsSet("web.blocked-domain-files") {
		cfg.Web.BlockedDomainFiles = splitConfigCSVAndTrim(cmd.String("web.blocked-domain-files"))
	}
	if cmd.IsSet("web.native-allowed-hosts") {
		cfg.Web.NativeAllowedHosts = splitConfigCSVAndTrim(cmd.String("web.native-allowed-hosts"))
	}
	if cmd.IsSet("web.native-blocked-hosts") {
		cfg.Web.NativeBlockedHosts = splitConfigCSVAndTrim(cmd.String("web.native-blocked-hosts"))
	}
	if cmd.IsSet("web.native-allowed-host-files") {
		cfg.Web.NativeAllowedHostFiles = splitConfigCSVAndTrim(cmd.String("web.native-allowed-host-files"))
	}
	if cmd.IsSet("web.native-blocked-host-files") {
		cfg.Web.NativeBlockedHostFiles = splitConfigCSVAndTrim(cmd.String("web.native-blocked-host-files"))
	}
	if cmd.IsSet("web.extract-min-summarize-chars") {
		cfg.Web.ExtractMinSummarizeChars = cmd.Int("web.extract-min-summarize-chars")
	}
	if cmd.IsSet("web.extract-max-summary-chars") {
		cfg.Web.ExtractMaxSummaryChars = cmd.Int("web.extract-max-summary-chars")
	}
	if cmd.IsSet("web.extract-max-summary-chunk-chars") {
		cfg.Web.ExtractMaxSummaryChunkChars = cmd.Int("web.extract-max-summary-chunk-chars")
	}
	if cmd.IsSet("web.extract-refusal-threshold-chars") {
		cfg.Web.ExtractRefusalThresholdChars = cmd.Int("web.extract-refusal-threshold-chars")
	}
	if cmd.IsSet("rules.files") {
		cfg.Rules.Files = splitConfigCSVAndTrim(cmd.String("rules.files"))
	}
	if cmd.IsSet("instruct") {
		literalValue28 := str.String(cmd.String("instruct"))
		cfg.Session.Instruct = literalValue28.Trim()
	}
	if cmd.IsSet("platform") {
		literalValue29 := str.String(cmd.String("platform"))
		cfg.Platform = literalValue29.Trim()
	}
	if cmd.IsSet("fs.roots") {
		cfg.FS.Roots = splitConfigCSVAndTrim(cmd.String("fs.roots"))
	}
	if cmd.IsSet("cap.fs") {
		cfg.Cap.Filesystem = new(cmd.Bool("cap.fs"))
	}
	if cmd.IsSet("cap.net") {
		cfg.Cap.Network = new(cmd.Bool("cap.net"))
	}
	if cmd.IsSet("cap.exec") {
		cfg.Cap.Exec = new(cmd.Bool("cap.exec"))
	}
	if cmd.IsSet("cap.mem") {
		cfg.Cap.Memory = new(cmd.Bool("cap.mem"))
	}
	if cmd.IsSet("cap.browser") {
		cfg.Cap.Browser = new(cmd.Bool("cap.browser"))
	}
	if cmd.IsSet("storage.backend") {
		literalValue30 := str.String(cmd.String("storage.backend"))
		cfg.Storage.Backend = literalValue30.Trim()
	}
	if cmd.IsSet("memory.backend") {
		literalValue31 := str.String(cmd.String("memory.backend"))
		cfg.Memory.Backend = literalValue31.Trim()
	}
	if cmd.IsSet("session.default-idle-expiry") {
		cfg.Session.DefaultIdleExpiry = cmd.Duration("session.default-idle-expiry")
	}
	if cmd.IsSet("session.archive-retention") {
		cfg.Session.ArchiveRetention = cmd.Duration("session.archive-retention")
	}
}

func splitConfigCSVAndTrim(value string) []string {
	valueText := str.String(value).Trim()
	if valueText == "" {
		return nil
	}

	parts := strings.Split(valueText, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		partValue := str.String(part)
		trimmed := partValue.Trim()
		if trimmed == "" {
			continue
		}
		values = append(values, trimmed)
	}

	return values
}

func getBoolValue(value *bool) bool {
	if value == nil {
		return false
	}

	return *value
}
