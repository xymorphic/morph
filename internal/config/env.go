package config

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/wandxy/morph/internal/constants"
	"github.com/wandxy/morph/pkg/str"
)

func applyEnvOverrides(cfg *Config) {
	if cfg == nil {
		return
	}

	envValue := str.String(os.Getenv("MORPH_NAME"))
	if value := envValue.Trim(); value != "" {
		cfg.Name = value
	}
	envValue2 := str.String(os.Getenv("MORPH_MODEL"))
	if value := envValue2.Trim(); value != "" {
		cfg.Models.Main.Name = value
	}
	envValue3 := str.String(os.Getenv("MORPH_MODEL_SUMMARY"))
	if value := envValue3.Trim(); value != "" {
		cfg.Models.Summary.Name = value
	}
	if value, ok := parseOptionalBoolEnv("MORPH_MODEL_STREAM"); ok {
		cfg.Models.Main.Stream = new(value)
	}
	envValue4 := str.String(os.Getenv("MORPH_MODEL_CONTEXT_LENGTH"))
	if value := envValue4.Trim(); value != "" {
		if contextLength, err := strconv.Atoi(value); err == nil {
			cfg.Models.Main.ContextLength = contextLength
		}
	}
	envValue5 := str.String(os.Getenv("MORPH_MODEL_MAX_RETRIES"))
	if value := envValue5.Trim(); value != "" {
		if retries, err := strconv.Atoi(value); err == nil {
			cfg.Models.MaxRetries = &retries
		}
	}
	envValue6 := str.String(os.Getenv("MORPH_MODEL_PROVIDER"))
	if value := envValue6.Trim(); value != "" {
		cfg.Models.Main.Provider = value
	}
	envValue7 := str.String(os.Getenv("MORPH_MODEL_EMBEDDING_PROVIDER"))
	if value := envValue7.Trim(); value != "" {
		cfg.Models.Embedding.Provider = value
	}
	envValue8 := str.String(os.Getenv("MORPH_MODEL_EMBEDDING_MODEL"))
	if value := envValue8.Trim(); value != "" {
		cfg.Models.Embedding.Name = value
	}
	envValue9 := str.String(os.Getenv("MORPH_MODEL_BASE_URL"))
	if value := envValue9.Trim(); value != "" {
		cfg.Models.Main.BaseURL = value
	}
	envValue10 := str.String(os.Getenv("MORPH_MODEL_SUMMARY_PROVIDER"))
	if value := envValue10.Trim(); value != "" {
		cfg.Models.Summary.Provider = value
	}
	envValue11 := str.String(os.Getenv("MORPH_MODEL_SUMMARY_BASE_URL"))
	if value := envValue11.Trim(); value != "" {
		cfg.Models.Summary.BaseURL = value
	}
	envValue12 := str.String(os.Getenv("MORPH_MODEL_API"))
	if value := envValue12.Trim(); value != "" {
		cfg.Models.Main.API = value
	}
	envValue13 := str.String(os.Getenv("MORPH_MODEL_SUMMARY_API"))
	if value := envValue13.Trim(); value != "" {
		cfg.Models.Summary.API = value
	}
	envValue14 := str.String(os.Getenv("MORPH_RPC_ADDRESS"))
	if value := envValue14.Trim(); value != "" {
		cfg.RPC.Address = value
	}
	envValue15 := str.String(os.Getenv("MORPH_RPC_PORT"))
	if value := envValue15.Trim(); value != "" {
		if port, err := strconv.Atoi(value); err == nil {
			cfg.RPC.Port = port
		}
	}
	if value := strings.TrimSpace(os.Getenv("MORPH_AUTH_KEY")); value != "" {
		cfg.Auth.Key = value
	}
	if value := strings.TrimSpace(os.Getenv("MORPH_AUTH_TOKEN")); value != "" {
		cfg.Auth.Token = value
	}
	if value := strings.TrimSpace(os.Getenv("MORPH_AUTH_AUDIENCE")); value != "" {
		cfg.Auth.Audience = value
	}
	if value := strings.TrimSpace(os.Getenv("MORPH_RPC_TLS_MODE")); value != "" {
		cfg.Auth.TLS.Mode = value
	}
	if value := strings.TrimSpace(os.Getenv("MORPH_RPC_TLS_CERT")); value != "" {
		cfg.Auth.TLS.ServerCertificate = value
	}
	if value := strings.TrimSpace(os.Getenv("MORPH_RPC_TLS_KEY")); value != "" {
		cfg.Auth.TLS.ServerKey = value
	}
	if value := strings.TrimSpace(os.Getenv("MORPH_RPC_TLS_SERVER_CA")); value != "" {
		cfg.Auth.TLS.ServerCA = value
	}
	if value := strings.TrimSpace(os.Getenv("MORPH_RPC_TLS_CLIENT_CA")); value != "" {
		cfg.Auth.TLS.ClientCA = value
	}
	if value := strings.TrimSpace(os.Getenv("MORPH_RPC_TLS_CLIENT_CERT")); value != "" {
		cfg.Auth.TLS.ClientCertificate = value
	}
	if value := strings.TrimSpace(os.Getenv("MORPH_RPC_TLS_CLIENT_KEY")); value != "" {
		cfg.Auth.TLS.ClientKey = value
	}
	if value := strings.TrimSpace(os.Getenv("MORPH_RPC_TLS_SERVER_NAME")); value != "" {
		cfg.Auth.TLS.ServerName = value
	}
	if value := strings.TrimSpace(os.Getenv("MORPH_RPC_TLS_MIN_VERSION")); value != "" {
		cfg.Auth.TLS.MinimumVersion = value
	}
	if value, ok := parseOptionalBoolEnv("MORPH_GATEWAY_ENABLED"); ok {
		cfg.Gateway.Enabled = value
	}
	envValue16 := str.String(os.Getenv("MORPH_GATEWAY_ADDRESS"))
	if value := envValue16.Trim(); value != "" {
		cfg.Gateway.Address = value
	}
	envValue17 := str.String(os.Getenv("MORPH_GATEWAY_PORT"))
	if value := envValue17.Trim(); value != "" {
		if port, err := strconv.Atoi(value); err == nil {
			cfg.Gateway.Port = port
		}
	}
	envValue18 := str.String(os.Getenv("MORPH_GATEWAY_AUTH_TOKEN"))
	if value := envValue18.Trim(); value != "" {
		cfg.Gateway.AuthToken = value
	}
	envValue19 := str.String(os.Getenv("MORPH_GATEWAY_PAIRING_SECRET"))
	if value := envValue19.Trim(); value != "" {
		cfg.Gateway.PairingSecret = value
	}
	envValue20 := str.String(os.Getenv("MORPH_GATEWAY_ALLOWED_USERS"))
	if value := envValue20.Trim(); value != "" {
		cfg.Gateway.AllowedUsers = splitAndTrimCSV(value)
	}
	if value, ok := parseOptionalBoolEnv("MORPH_GATEWAY_TELEGRAM_ENABLED"); ok {
		cfg.Gateway.Telegram.Enabled = value
	}
	envValue21 := str.String(os.Getenv("MORPH_GATEWAY_TELEGRAM_MODE"))
	if value := envValue21.Trim(); value != "" {
		cfg.Gateway.Telegram.Mode = value
	}
	envValue22 := str.String(os.Getenv("MORPH_GATEWAY_TELEGRAM_BOT_TOKEN"))
	if value := envValue22.Trim(); value != "" {
		cfg.Gateway.Telegram.BotToken = value
	}
	envValue23 := str.String(os.Getenv("MORPH_GATEWAY_TELEGRAM_WEBHOOK_SECRET"))
	if value := envValue23.Trim(); value != "" {
		cfg.Gateway.Telegram.WebhookSecret = value
	}
	envValue24 := str.String(os.Getenv("MORPH_GATEWAY_TELEGRAM_ALLOWED_USERS"))
	if value := envValue24.Trim(); value != "" {
		cfg.Gateway.Telegram.AllowedUsers = splitAndTrimCSV(value)
	}
	if value, ok := parseOptionalBoolEnv("MORPH_GATEWAY_SLACK_ENABLED"); ok {
		cfg.Gateway.Slack.Enabled = value
	}
	envValue25 := str.String(os.Getenv("MORPH_GATEWAY_SLACK_MODE"))
	if value := envValue25.Trim(); value != "" {
		cfg.Gateway.Slack.Mode = value
	}
	envValue26 := str.String(os.Getenv("MORPH_GATEWAY_SLACK_RESPONSE_MODE"))
	if value := envValue26.Trim(); value != "" {
		cfg.Gateway.Slack.ResponseMode = value
	}
	envValue27 := str.String(os.Getenv("MORPH_GATEWAY_SLACK_BOT_TOKEN"))
	if value := envValue27.Trim(); value != "" {
		cfg.Gateway.Slack.BotToken = value
	}
	envValue28 := str.String(os.Getenv("MORPH_GATEWAY_SLACK_APP_TOKEN"))
	if value := envValue28.Trim(); value != "" {
		cfg.Gateway.Slack.AppToken = value
	}
	envValue29 := str.String(os.Getenv("MORPH_GATEWAY_SLACK_SIGNING_SECRET"))
	if value := envValue29.Trim(); value != "" {
		cfg.Gateway.Slack.SigningSecret = value
	}
	envValue30 := str.String(os.Getenv("MORPH_GATEWAY_SLACK_ALLOWED_USERS"))
	if value := envValue30.Trim(); value != "" {
		cfg.Gateway.Slack.AllowedUsers = splitAndTrimCSV(value)
	}
	envValue31 := str.String(os.Getenv("MORPH_SESSION_MAX_ITERATIONS"))
	if value := envValue31.Trim(); value != "" {
		if maxIterations, err := strconv.Atoi(value); err == nil {
			cfg.Session.MaxIterations = maxIterations
		}
	}
	envValue32 := str.String(os.Getenv("MORPH_LOG_LEVEL"))
	if value := envValue32.Trim(); value != "" {
		cfg.Log.Level = value
	}
	envValue33 := str.String(os.Getenv("MORPH_LOG_FILE"))
	if value := envValue33.Trim(); value != "" {
		cfg.Log.File = value
	}
	envValue34 := str.String(os.Getenv("MORPH_LOG_MAX_SIZE_MB"))
	if value := envValue34.Trim(); value != "" {
		if maxSizeMB, err := strconv.Atoi(value); err == nil {
			cfg.Log.MaxSizeMB = maxSizeMB
		}
	}
	envValue35 := str.String(os.Getenv("MORPH_LOG_MAX_BACKUPS"))
	if value := envValue35.Trim(); value != "" {
		if maxBackups, err := strconv.Atoi(value); err == nil {
			cfg.Log.MaxBackups = maxBackups
		}
	}
	envValue36 := str.String(os.Getenv("MORPH_LOG_MAX_AGE_DAYS"))
	if value := envValue36.Trim(); value != "" {
		if maxAgeDays, err := strconv.Atoi(value); err == nil {
			cfg.Log.MaxAgeDays = maxAgeDays
		}
	}
	if value, ok := parseOptionalBoolEnv("MORPH_LOG_COMPRESS"); ok {
		cfg.Log.Compress = value
	}
	envValue37 := str.String(os.Getenv("MORPH_LOG_NO_COLOR"))
	if value := envValue37.Normalized(); value != "" {
		cfg.Log.NoColor = value == "1" || value == "true" || value == "yes"
	}
	envValue38 := str.String(os.Getenv("MORPH_DEBUG_REQUESTS"))
	if value := envValue38.Normalized(); value != "" {
		cfg.Debug.Requests = value == "1" || value == "true" || value == "yes"
	}
	devRetainUnsafeValue := str.String(os.Getenv("MORPH_DEV_RETAIN_UNSAFE"))
	if value := devRetainUnsafeValue.Normalized(); value != "" {
		cfg.Dev.RetainUnsafe = value == "1" || value == "true" || value == "yes"
	}
	if value, ok := parseOptionalBoolEnv("MORPH_SAFETY_INPUT"); ok {
		cfg.Safety.Input = new(value)
	}
	if value, ok := parseOptionalBoolEnv("MORPH_SAFETY_OUTPUT"); ok {
		cfg.Safety.Output = new(value)
	}
	if value, ok := parseOptionalBoolEnv("MORPH_SAFETY_PII"); ok {
		cfg.Safety.PII = new(value)
	}
	envValue39 := str.String(os.Getenv("MORPH_TRACE_ENABLED"))
	if value := envValue39.Normalized(); value != "" {
		cfg.Trace.Enabled = value == "1" || value == "true" || value == "yes"
	}
	if value, ok := parseOptionalBoolEnv("MORPH_TRACE_DISK_ENABLED"); ok {
		cfg.Trace.Disk.Enabled = new(value)
	}
	envValue40 := str.String(os.Getenv("MORPH_TRACE_DISK_DIR"))
	if value := envValue40.Trim(); value != "" {
		cfg.Trace.Disk.Dir = value
	}
	if value, ok := parseOptionalBoolEnv("MORPH_TRACE_DATABASE_ENABLED"); ok {
		cfg.Trace.Database.Enabled = new(value)
	}
	envValue41 := str.String(os.Getenv("MORPH_TRACE_DATABASE_MAX_EVENTS_PER_SESSION"))
	if value := envValue41.Trim(); value != "" {
		if maxEvents, err := strconv.Atoi(value); err == nil {
			cfg.Trace.Database.MaxEventsPerSession = maxEvents
		}
	}
	if value, ok := parseOptionalBoolEnv("MORPH_TUI_THINKING_COMPOSER"); ok {
		cfg.TUI.ThinkingComposer = new(value)
	}
	envValue42 := str.String(os.Getenv("MORPH_WEB_PROVIDER"))
	if value := envValue42.Trim(); value != "" {
		cfg.Web.Provider = value
	}
	envValue43 := str.String(os.Getenv("MORPH_WEB_API_KEY"))
	if value := envValue43.Trim(); value != "" {
		cfg.Web.APIKey = value
	}
	envValue44 := str.String(os.Getenv("MORPH_WEB_BASE_URL"))
	if value := envValue44.Trim(); value != "" {
		cfg.Web.BaseURL = value
	}
	envValue45 := str.String(os.Getenv("MORPH_WEB_MAX_CHAR_PER_RESULT"))
	if value := envValue45.Trim(); value != "" {
		if chars, err := strconv.Atoi(value); err == nil {
			cfg.Web.MaxCharPerResult = chars
		}
	}
	envValue46 := str.String(os.Getenv("MORPH_WEB_MAX_EXTRACT_CHAR_PER_RESULT"))
	if value := envValue46.Trim(); value != "" {
		if chars, err := strconv.Atoi(value); err == nil {
			cfg.Web.MaxExtractCharPerResult = chars
		}
	}
	envValue47 := str.String(os.Getenv("MORPH_WEB_MAX_EXTRACT_RESPONSE_BYTES"))
	if value := envValue47.Trim(); value != "" {
		if bytes, err := strconv.Atoi(value); err == nil {
			cfg.Web.MaxExtractResponseBytes = bytes
		}
	}
	envValue48 := str.String(os.Getenv("MORPH_WEB_CACHE_TTL"))
	if value := envValue48.Trim(); value != "" {
		cfg.Web.CacheTTL = parseDurationOrZero(value)
	}
	if value, ok := parseOptionalBoolEnv("MORPH_WEB_BLOCKED_DOMAINS_ENABLED"); ok {
		cfg.Web.BlockedDomainsEnabled = value
	}
	envValue49 := str.String(os.Getenv("MORPH_WEB_BLOCKED_DOMAINS"))
	if value := envValue49.Trim(); value != "" {
		cfg.Web.BlockedDomains = splitAndTrimCSV(value)
	}
	envValue50 := str.String(os.Getenv("MORPH_WEB_BLOCKED_DOMAIN_FILES"))
	if value := envValue50.Trim(); value != "" {
		cfg.Web.BlockedDomainFiles = splitAndTrimCSV(value)
	}
	envValue51 := str.String(os.Getenv("MORPH_WEB_NATIVE_ALLOWED_HOSTS"))
	if value := envValue51.Trim(); value != "" {
		cfg.Web.NativeAllowedHosts = splitAndTrimCSV(value)
	}
	envValue52 := str.String(os.Getenv("MORPH_WEB_NATIVE_BLOCKED_HOSTS"))
	if value := envValue52.Trim(); value != "" {
		cfg.Web.NativeBlockedHosts = splitAndTrimCSV(value)
	}
	envValue53 := str.String(os.Getenv("MORPH_WEB_NATIVE_ALLOWED_HOST_FILES"))
	if value := envValue53.Trim(); value != "" {
		cfg.Web.NativeAllowedHostFiles = splitAndTrimCSV(value)
	}
	envValue54 := str.String(os.Getenv("MORPH_WEB_NATIVE_BLOCKED_HOST_FILES"))
	if value := envValue54.Trim(); value != "" {
		cfg.Web.NativeBlockedHostFiles = splitAndTrimCSV(value)
	}
	envValue55 := str.String(os.Getenv("MORPH_WEB_EXTRACT_MIN_SUMMARIZE_CHARS"))
	if value := envValue55.Trim(); value != "" {
		if chars, err := strconv.Atoi(value); err == nil {
			cfg.Web.ExtractMinSummarizeChars = chars
		}
	}
	envValue56 := str.String(os.Getenv("MORPH_WEB_EXTRACT_MAX_SUMMARY_CHARS"))
	if value := envValue56.Trim(); value != "" {
		if chars, err := strconv.Atoi(value); err == nil {
			cfg.Web.ExtractMaxSummaryChars = chars
		}
	}
	envValue57 := str.String(os.Getenv("MORPH_WEB_EXTRACT_MAX_SUMMARY_CHUNK_CHARS"))
	if value := envValue57.Trim(); value != "" {
		if chars, err := strconv.Atoi(value); err == nil {
			cfg.Web.ExtractMaxSummaryChunkChars = chars
		}
	}
	envValue58 := str.String(os.Getenv("MORPH_WEB_EXTRACT_REFUSAL_THRESHOLD_CHARS"))
	if value := envValue58.Trim(); value != "" {
		if chars, err := strconv.Atoi(value); err == nil {
			cfg.Web.ExtractRefusalThresholdChars = chars
		}
	}
	if cfg.Web.Provider == "" {
		firecrawlAPIKey := str.String(os.Getenv("MORPH_FIRECRAWL_API_KEY"))
		firecrawlAPIURL := str.String(os.Getenv("MORPH_FIRECRAWL_API_URL"))
		parallelAPIKey := str.String(os.Getenv("MORPH_PARALLEL_API_KEY"))
		tavilyAPIKey := str.String(os.Getenv("MORPH_TAVILY_API_KEY"))
		exaAPIKey := str.String(os.Getenv("MORPH_EXA_API_KEY"))
		switch {
		case firecrawlAPIKey.Trim() != "" || firecrawlAPIURL.Trim() != "":
			cfg.Web.Provider = constants.WebProviderFirecrawl
		case parallelAPIKey.Trim() != "":
			cfg.Web.Provider = constants.WebProviderParallel
		case tavilyAPIKey.Trim() != "":
			cfg.Web.Provider = constants.WebProviderTavily
		case exaAPIKey.Trim() != "":
			cfg.Web.Provider = constants.WebProviderExa
		}
	}
	if cfg.Web.APIKey == "" {
		providerValue := str.String(cfg.Web.Provider)
		switch providerValue.Normalized() {
		case constants.WebProviderFirecrawl:
			envValue59 := str.String(os.Getenv("MORPH_FIRECRAWL_API_KEY"))
			cfg.Web.APIKey = envValue59.Trim()
		case constants.WebProviderParallel:
			envValue60 := str.String(os.Getenv("MORPH_PARALLEL_API_KEY"))
			cfg.Web.APIKey = envValue60.Trim()
		case constants.WebProviderTavily:
			envValue61 := str.String(os.Getenv("MORPH_TAVILY_API_KEY"))
			cfg.Web.APIKey = envValue61.Trim()
		case constants.WebProviderExa:
			envValue62 := str.String(os.Getenv("MORPH_EXA_API_KEY"))
			cfg.Web.APIKey = envValue62.Trim()
		}
	}
	providerValue2 := str.String(cfg.Web.Provider)
	if cfg.Web.BaseURL == "" && providerValue2.Normalized() == constants.WebProviderFirecrawl {
		envValue63 := str.String(os.Getenv("MORPH_FIRECRAWL_API_URL"))
		cfg.Web.BaseURL = envValue63.Trim()
	}
	envValue64 := str.String(os.Getenv("MORPH_RULES_FILES"))
	if value := envValue64.Trim(); value != "" {
		cfg.Rules.Files = splitAndTrimCSV(value)
	}
	envValue65 := str.String(os.Getenv("MORPH_SESSION_INSTRUCT"))
	if value := envValue65.Trim(); value != "" {
		cfg.Session.Instruct = value
	}
	envValue66 := str.String(os.Getenv("MORPH_PLATFORM"))
	if value := envValue66.Trim(); value != "" {
		cfg.Platform = value
	}
	envValue67 := str.String(os.Getenv("MORPH_FS_ROOTS"))
	if value := envValue67.Trim(); value != "" {
		cfg.FS.Roots = splitAndTrimCSV(value)
	}

	if value, ok := parseOptionalBoolEnv("MORPH_CAP_FS"); ok {
		cfg.Cap.Filesystem = new(value)
	}
	if value, ok := parseOptionalBoolEnv("MORPH_CAP_NET"); ok {
		cfg.Cap.Network = new(value)
	}
	if value, ok := parseOptionalBoolEnv("MORPH_CAP_EXEC"); ok {
		cfg.Cap.Exec = new(value)
	}
	if value, ok := parseOptionalBoolEnv("MORPH_CAP_MEM"); ok {
		cfg.Cap.Memory = new(value)
	}
	if value, ok := parseOptionalBoolEnv("MORPH_CAP_BROWSER"); ok {
		cfg.Cap.Browser = new(value)
	}
	envValue68 := str.String(os.Getenv("MORPH_EXEC_ALLOW"))
	if value := envValue68.Trim(); value != "" {
		cfg.Exec.Allow = splitAndTrimCSV(value)
	}
	envValue69 := str.String(os.Getenv("MORPH_EXEC_ASK"))
	if value := envValue69.Trim(); value != "" {
		cfg.Exec.Ask = splitAndTrimCSV(value)
	}
	envValue70 := str.String(os.Getenv("MORPH_EXEC_DENY"))
	if value := envValue70.Trim(); value != "" {
		cfg.Exec.Deny = splitAndTrimCSV(value)
	}
	if value := strings.TrimSpace(os.Getenv("MORPH_EXECUTION_BACKEND")); value != "" {
		cfg.Execution.Backend = value
	}
	if value := strings.TrimSpace(os.Getenv("MORPH_EXECUTION_DOCKER_SCOPE")); value != "" {
		cfg.Execution.Docker.Scope = value
	}
	if value := strings.TrimSpace(os.Getenv("MORPH_EXECUTION_DOCKER_ENDPOINT")); value != "" {
		cfg.Execution.Docker.Endpoint = value
	}
	if value := strings.TrimSpace(os.Getenv("MORPH_EXECUTION_DOCKER_IMAGE")); value != "" {
		cfg.Execution.Docker.Image = value
	}
	if value := strings.TrimSpace(os.Getenv("MORPH_EXECUTION_DOCKER_CONTRACT")); value != "" {
		cfg.Execution.Docker.Contract = value
	}
	if value := strings.TrimSpace(os.Getenv("MORPH_EXECUTION_DOCKER_WORKSPACE_MODE")); value != "" {
		cfg.Execution.Docker.Workspace.Mode = value
	}
	if value := strings.TrimSpace(os.Getenv("MORPH_EXECUTION_DOCKER_WORKSPACE_SOURCE")); value != "" {
		cfg.Execution.Docker.Workspace.Source = value
	}
	if value := strings.TrimSpace(os.Getenv("MORPH_EXECUTION_DOCKER_NETWORK")); value != "" {
		cfg.Execution.Docker.Network = value
	}
	envValue71 := str.String(os.Getenv("MORPH_STORAGE_BACKEND"))
	if value := envValue71.Trim(); value != "" {
		cfg.Storage.Backend = value
	}
	envValue72 := str.String(os.Getenv("MORPH_SESSION_DEFAULT_IDLE_EXPIRY"))
	if value := envValue72.Trim(); value != "" {
		cfg.Session.DefaultIdleExpiry = parseDurationOrZero(value)
	}
	envValue73 := str.String(os.Getenv("MORPH_SESSION_ARCHIVE_RETENTION"))
	if value := envValue73.Trim(); value != "" {
		cfg.Session.ArchiveRetention = parseDurationOrZero(value)
	}
	if value, ok := parseOptionalBoolEnv("MORPH_SEARCH_VECTOR_ENABLED"); ok {
		cfg.Search.Vector.Enabled = value
	}
	if value, ok := parseOptionalBoolEnv("MORPH_MEMORY_ENABLED"); ok {
		cfg.Memory.Enabled = new(value)
	}
	envValue74 := str.String(os.Getenv("MORPH_MEMORY_PROVIDER"))
	if value := envValue74.Trim(); value != "" {
		cfg.Memory.Provider = value
	}
	envValue75 := str.String(os.Getenv("MORPH_MEMORY_BACKEND"))
	if value := envValue75.Trim(); value != "" {
		cfg.Memory.Backend = value
	}
	if value, ok := parseOptionalBoolEnv("MORPH_MEMORY_PINNED_ENABLED"); ok {
		cfg.Memory.Pinned.Enabled = new(value)
	}
	if value, ok := parseOptionalBoolEnv("MORPH_MEMORY_RETRIEVAL_ENABLED"); ok {
		cfg.Memory.Retrieval.Enabled = new(value)
	}
	if value, ok := parseOptionalBoolEnv("MORPH_MEMORY_FLUSH_ENABLED"); ok {
		cfg.Memory.Flush.Enabled = new(value)
	}
	envValue76 := str.String(os.Getenv("MORPH_MEMORY_FLUSH_MAX_CALLS"))
	if value := envValue76.Trim(); value != "" {
		if maxCalls, err := strconv.Atoi(value); err == nil {
			cfg.Memory.Flush.MaxCalls = maxCalls
		}
	}
	envValue77 := str.String(os.Getenv("MORPH_MEMORY_FLUSH_MAX_OUTPUT_TOKENS"))
	if value := envValue77.Trim(); value != "" {
		if maxOutputTokens, err := strconv.ParseInt(value, 10, 64); err == nil {
			cfg.Memory.Flush.MaxOutputTokens = maxOutputTokens
		}
	}
	envValue78 := str.String(os.Getenv("MORPH_MEMORY_FLUSH_TIMEOUT"))
	if value := envValue78.Trim(); value != "" {
		if timeout, err := time.ParseDuration(value); err == nil {
			cfg.Memory.Flush.Timeout = timeout
		}
	}
	envValue79 := str.String(os.Getenv("MORPH_MEMORY_PINNED_MAX_CHARS"))
	if value := envValue79.Trim(); value != "" {
		if maxChars, err := strconv.Atoi(value); err == nil {
			cfg.Memory.Pinned.MaxChars = maxChars
		}
	}
	envValue80 := str.String(os.Getenv("MORPH_MEMORY_PINNED_MAX_ITEM_CHARS"))
	if value := envValue80.Trim(); value != "" {
		if maxChars, err := strconv.Atoi(value); err == nil {
			cfg.Memory.Pinned.MaxItemChars = maxChars
		}
	}
	if value, ok := parseOptionalBoolEnv("MORPH_MEMORY_EPISODIC_ENABLED"); ok {
		cfg.Memory.Episodic.Enabled = new(value)
	}
	envValue81 := str.String(os.Getenv("MORPH_MEMORY_EPISODIC_INTERVAL"))
	if value := envValue81.Trim(); value != "" {
		cfg.Memory.Episodic.Interval = parseDurationOrZero(value)
	}
	envValue82 := str.String(os.Getenv("MORPH_MEMORY_EPISODIC_IDLE_AFTER"))
	if value := envValue82.Trim(); value != "" {
		cfg.Memory.Episodic.IdleAfter = parseDurationOrZero(value)
	}
	envValue83 := str.String(os.Getenv("MORPH_MEMORY_EPISODIC_MIN_MESSAGES"))
	if value := envValue83.Trim(); value != "" {
		if count, err := strconv.Atoi(value); err == nil {
			cfg.Memory.Episodic.MinMessages = count
		}
	}
	envValue84 := str.String(os.Getenv("MORPH_MEMORY_EPISODIC_WINDOW_SIZE"))
	if value := envValue84.Trim(); value != "" {
		if count, err := strconv.Atoi(value); err == nil {
			cfg.Memory.Episodic.WindowSize = count
		}
	}
	envValue85 := str.String(os.Getenv("MORPH_MEMORY_EPISODIC_MAX_WINDOWS"))
	if value := envValue85.Trim(); value != "" {
		if count, err := strconv.Atoi(value); err == nil {
			cfg.Memory.Episodic.MaxWindows = count
		}
	}
	envValue86 := str.String(os.Getenv("MORPH_MEMORY_EPISODIC_MAX_WINDOW_CHARS"))
	if value := envValue86.Trim(); value != "" {
		if count, err := strconv.Atoi(value); err == nil {
			cfg.Memory.Episodic.MaxWindowChars = count
		}
	}
	envValue87 := str.String(os.Getenv("MORPH_MEMORY_EPISODIC_MAX_WINDOW_TOKENS"))
	if value := envValue87.Trim(); value != "" {
		if count, err := strconv.Atoi(value); err == nil {
			cfg.Memory.Episodic.MaxWindowTokens = count
		}
	}
	envValue88 := str.String(os.Getenv("MORPH_MEMORY_EPISODIC_MAX_RETRIES"))
	if value := envValue88.Trim(); value != "" {
		if count, err := strconv.Atoi(value); err == nil {
			cfg.Memory.Episodic.MaxRetries = count
		}
	}
	if value, ok := parseOptionalBoolEnv("MORPH_MEMORY_REFLECTION_ENABLED"); ok {
		cfg.Memory.Reflection.Enabled = new(value)
	}
	envValue89 := str.String(os.Getenv("MORPH_MEMORY_REFLECTION_INTERVAL"))
	if value := envValue89.Trim(); value != "" {
		cfg.Memory.Reflection.Interval = parseDurationOrZero(value)
	}
	envValue90 := str.String(os.Getenv("MORPH_MEMORY_REFLECTION_LIMIT"))
	if value := envValue90.Trim(); value != "" {
		if count, err := strconv.Atoi(value); err == nil {
			cfg.Memory.Reflection.Limit = count
		}
	}
	envValue91 := str.String(os.Getenv("MORPH_MEMORY_REFLECTION_RELATED_LIMIT"))
	if value := envValue91.Trim(); value != "" {
		if count, err := strconv.Atoi(value); err == nil {
			cfg.Memory.Reflection.RelatedLimit = count
		}
	}
	if value, ok := parseOptionalBoolEnv("MORPH_MEMORY_PROMOTION_ENABLED"); ok {
		cfg.Memory.Promotion.Enabled = new(value)
	}
	envValue92 := str.String(os.Getenv("MORPH_MEMORY_PROMOTION_INTERVAL"))
	if value := envValue92.Trim(); value != "" {
		cfg.Memory.Promotion.Interval = parseDurationOrZero(value)
	}
	envValue93 := str.String(os.Getenv("MORPH_MEMORY_PROMOTION_LIMIT"))
	if value := envValue93.Trim(); value != "" {
		if count, err := strconv.Atoi(value); err == nil {
			cfg.Memory.Promotion.Limit = count
		}
	}
	envValue94 := str.String(os.Getenv("MORPH_MEMORY_PROMOTION_EVALUATED_RETENTION"))
	if value := envValue94.Trim(); value != "" {
		cfg.Memory.Promotion.EvaluatedRetention = parseDurationOrZero(value)
	}
	if value, ok := parseOptionalBoolEnv("MORPH_MEMORY_WRITE_ENABLED"); ok {
		cfg.Memory.Write.Enabled = new(value)
	}
	if value, ok := parseOptionalBoolEnv("MORPH_SEARCH_VECTOR_REQUIRED"); ok {
		cfg.Search.Vector.Required = value
	}
	envValue95 := str.String(os.Getenv("MORPH_SEARCH_VECTOR_REBUILD_BATCH_SIZE"))
	if value := envValue95.Trim(); value != "" {
		if batchSize, err := strconv.Atoi(value); err == nil {
			cfg.Search.Vector.RebuildBatchSize = batchSize
		}
	}
	if value := strings.TrimSpace(os.Getenv("MORPH_SEARCH_VECTOR_MAX_INPUT_BYTES")); value != "" {
		if maxInputBytes, err := strconv.Atoi(value); err == nil {
			cfg.Search.Vector.MaxInputBytes = maxInputBytes
		}
	}
	if value := strings.TrimSpace(os.Getenv("MORPH_SEARCH_VECTOR_MAX_DOCUMENT_BYTES")); value != "" {
		if maxDocumentBytes, err := strconv.Atoi(value); err == nil {
			cfg.Search.Vector.MaxDocumentBytes = maxDocumentBytes
		}
	}
	if value, ok := parseOptionalBoolEnv("MORPH_RERANKER_ENABLED"); ok {
		cfg.Reranker.Enabled = new(value)
	}
	if value, ok := parseOptionalBoolEnv("MORPH_SEARCH_ENABLE_RERANK"); ok {
		cfg.Search.EnableRerank = new(value)
	}
	envValue96 := str.String(os.Getenv("MORPH_RERANKER_TYPE"))
	if value := envValue96.Trim(); value != "" {
		cfg.Reranker.Type = value
	}
	envValue97 := str.String(os.Getenv("MORPH_RERANKER_MODEL"))
	if value := envValue97.Trim(); value != "" {
		cfg.Reranker.Model = value
	}
	envValue98 := str.String(os.Getenv("MORPH_RERANKER_MAX_CANDIDATES"))
	if value := envValue98.Trim(); value != "" {
		if maxCandidates, err := strconv.Atoi(value); err == nil {
			cfg.Reranker.MaxCandidates = maxCandidates
		}
	}
	envValue99 := str.String(os.Getenv("MORPH_RERANKER_MAX_CANDIDATE_TEXT_CHARS"))
	if value := envValue99.Trim(); value != "" {
		if maxChars, err := strconv.Atoi(value); err == nil {
			cfg.Reranker.MaxCandidateTextChars = maxChars
		}
	}
	envValue100 := str.String(os.Getenv("MORPH_RERANKER_MAX_OUTPUT_TOKENS"))
	if value := envValue100.Trim(); value != "" {
		if maxTokens, err := strconv.Atoi(value); err == nil {
			cfg.Reranker.MaxOutputTokens = maxTokens
		}
	}
	envValue101 := str.String(os.Getenv("MORPH_RERANKER_OVERRIDES"))
	if value := envValue101.Trim(); value != "" {
		var overrides map[string]RerankerOverrideConfig
		if err := json.Unmarshal([]byte(value), &overrides); err == nil {
			cfg.Reranker.Overrides = overrides
		}
	}

	if value, ok := parseOptionalBoolEnv("MORPH_COMPACTION_ENABLED"); ok {
		cfg.Compaction.Enabled = new(value)
	}
	envValue102 := str.String(os.Getenv("MORPH_COMPACTION_TRIGGER_PERCENT"))
	if value := envValue102.Trim(); value != "" {
		if percent, err := strconv.ParseFloat(value, 64); err == nil {
			cfg.Compaction.TriggerPercent = percent
		}
	}
	envValue103 := str.String(os.Getenv("MORPH_COMPACTION_WARN_PERCENT"))
	if value := envValue103.Trim(); value != "" {
		if percent, err := strconv.ParseFloat(value, 64); err == nil {
			cfg.Compaction.WarnPercent = percent
		}
	}
}
