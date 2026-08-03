package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xymorphic/morph/internal/constants"
)

func TestLoad_ReturnsPreloadEnvFileError(t *testing.T) {
	originalLoadDotEnv := loadDotEnv
	t.Cleanup(func() {
		loadDotEnv = originalLoadDotEnv
	})

	loadDotEnv = func(...string) error {
		return errors.New("boom")
	}

	_, err := Load("broken.env", "")
	require.EqualError(t, err, `failed to load env file "broken.env": boom`)
}

func TestLoad_UsesEnvOverConfigFile(t *testing.T) {
	clearEnvKeys(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "OPENROUTER_API_KEY", "OPENAI_API_KEY",
		"OPENROUTER_API_KEY",
		"MORPH_MODEL_BASE_URL", "MORPH_MODEL_API", "MORPH_RPC_ADDRESS", "MORPH_RPC_PORT", "MORPH_SESSION_MAX_ITERATIONS",
		"MORPH_LOG_LEVEL", "MORPH_LOG_FILE", "MORPH_LOG_MAX_SIZE_MB", "MORPH_LOG_MAX_BACKUPS", "MORPH_LOG_MAX_AGE_DAYS",
		"MORPH_LOG_COMPRESS", "MORPH_LOG_NO_COLOR",
		"MORPH_MODEL_MAX_RETRIES",
		"MORPH_WEB_PROVIDER", "MORPH_WEB_API_KEY", "MORPH_WEB_BASE_URL", "MORPH_WEB_MAX_CHAR_PER_RESULT",
		"MORPH_WEB_MAX_EXTRACT_CHAR_PER_RESULT", "MORPH_WEB_MAX_EXTRACT_RESPONSE_BYTES",
		"MORPH_WEB_CACHE_TTL", "MORPH_WEB_BLOCKED_DOMAINS_ENABLED", "MORPH_WEB_BLOCKED_DOMAINS",
		"MORPH_WEB_BLOCKED_DOMAIN_FILES", "MORPH_WEB_NATIVE_ALLOWED_HOSTS", "MORPH_WEB_NATIVE_BLOCKED_HOSTS",
		"MORPH_WEB_NATIVE_ALLOWED_HOST_FILES", "MORPH_WEB_NATIVE_BLOCKED_HOST_FILES",
		"MORPH_WEB_EXTRACT_MIN_SUMMARIZE_CHARS", "MORPH_WEB_EXTRACT_MAX_SUMMARY_CHARS",
		"MORPH_WEB_EXTRACT_MAX_SUMMARY_CHUNK_CHARS", "MORPH_WEB_EXTRACT_REFUSAL_THRESHOLD_CHARS",
		"MORPH_DEBUG_REQUESTS", "MORPH_RULES_FILES", "MORPH_SESSION_INSTRUCT", "MORPH_PLATFORM", "MORPH_CAP_FS",
		"MORPH_CAP_NET", "MORPH_CAP_EXEC", "MORPH_CAP_MEM", "MORPH_CAP_BROWSER", "MORPH_MEMORY_ENABLED", "MORPH_MEMORY_PROVIDER",
		"MORPH_MEMORY_BACKEND",
		"MORPH_MEMORY_PINNED_ENABLED", "MORPH_MEMORY_PINNED_MAX_CHARS", "MORPH_MEMORY_PINNED_MAX_ITEM_CHARS",
		"MORPH_MEMORY_EPISODIC_ENABLED", "MORPH_MEMORY_EPISODIC_INTERVAL",
		"MORPH_MEMORY_EPISODIC_IDLE_AFTER", "MORPH_MEMORY_EPISODIC_MIN_MESSAGES",
		"MORPH_MEMORY_EPISODIC_WINDOW_SIZE", "MORPH_MEMORY_EPISODIC_MAX_WINDOWS",
		"MORPH_MEMORY_EPISODIC_MAX_WINDOW_CHARS", "MORPH_MEMORY_EPISODIC_MAX_WINDOW_TOKENS",
		"MORPH_MEMORY_EPISODIC_MAX_RETRIES", "MORPH_MEMORY_REFLECTION_ENABLED",
		"MORPH_MEMORY_REFLECTION_INTERVAL", "MORPH_MEMORY_REFLECTION_LIMIT",
		"MORPH_MEMORY_REFLECTION_RELATED_LIMIT", "MORPH_MEMORY_PROMOTION_ENABLED",
		"MORPH_MEMORY_PROMOTION_INTERVAL", "MORPH_MEMORY_PROMOTION_LIMIT",
		"MORPH_MEMORY_PROMOTION_EVALUATED_RETENTION")

	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(envPath, []byte(`
MORPH_NAME=env-agent
MORPH_MODEL=env-model
MORPH_MODEL_PROVIDER=openrouter
OPENROUTER_API_KEY=env-key
MORPH_MODEL_BASE_URL=https://env.example/v1
MORPH_MODEL_MAX_RETRIES=0
MORPH_RPC_ADDRESS=127.0.0.1
MORPH_RPC_PORT=7000
MORPH_SESSION_MAX_ITERATIONS=55
MORPH_LOG_LEVEL=warn
MORPH_LOG_FILE=/tmp/env-morph.log
MORPH_LOG_MAX_SIZE_MB=25
MORPH_LOG_MAX_BACKUPS=9
MORPH_LOG_MAX_AGE_DAYS=30
MORPH_LOG_COMPRESS=false
MORPH_LOG_NO_COLOR=false
MORPH_DEBUG_REQUESTS=false
MORPH_WEB_PROVIDER=tavily
MORPH_WEB_API_KEY=web-env-key
MORPH_WEB_BASE_URL=https://env-web.example
MORPH_WEB_MAX_CHAR_PER_RESULT=3100
MORPH_WEB_MAX_EXTRACT_CHAR_PER_RESULT=12400
MORPH_WEB_MAX_EXTRACT_RESPONSE_BYTES=4096
MORPH_WEB_CACHE_TTL=30m
MORPH_WEB_BLOCKED_DOMAINS_ENABLED=true
MORPH_WEB_BLOCKED_DOMAINS=blocked.example,ads.example
MORPH_WEB_BLOCKED_DOMAIN_FILES=blocked.txt,shared.txt
MORPH_WEB_NATIVE_ALLOWED_HOSTS=allowed.example,docs.example
MORPH_WEB_NATIVE_BLOCKED_HOSTS=blocked.example,raw.example
MORPH_WEB_NATIVE_ALLOWED_HOST_FILES=allow.txt,safe.txt
MORPH_WEB_NATIVE_BLOCKED_HOST_FILES=deny.txt,banned.txt
MORPH_WEB_EXTRACT_MIN_SUMMARIZE_CHARS=13000
MORPH_WEB_EXTRACT_MAX_SUMMARY_CHARS=3200
MORPH_WEB_EXTRACT_MAX_SUMMARY_CHUNK_CHARS=70000
MORPH_WEB_EXTRACT_REFUSAL_THRESHOLD_CHARS=190000
MORPH_RULES_FILES=morph.md,custom.md
MORPH_SESSION_INSTRUCT=be terse
MORPH_PLATFORM=cli
MORPH_CAP_FS=true
MORPH_CAP_NET=true
MORPH_CAP_EXEC=true
MORPH_CAP_MEM=true
MORPH_CAP_BROWSER=false
MORPH_MEMORY_ENABLED=false
MORPH_MEMORY_PROVIDER=default-memory
MORPH_MEMORY_BACKEND=sqlite
MORPH_MEMORY_PINNED_ENABLED=false
MORPH_MEMORY_PINNED_MAX_CHARS=3000
MORPH_MEMORY_PINNED_MAX_ITEM_CHARS=600
MORPH_MEMORY_REFLECTION_ENABLED=true
MORPH_MEMORY_REFLECTION_INTERVAL=5m
MORPH_MEMORY_REFLECTION_LIMIT=9
MORPH_MEMORY_REFLECTION_RELATED_LIMIT=4
MORPH_MEMORY_PROMOTION_ENABLED=true
MORPH_MEMORY_PROMOTION_INTERVAL=3m
MORPH_MEMORY_PROMOTION_LIMIT=8
MORPH_MEMORY_PROMOTION_EVALUATED_RETENTION=48h
`), 0o600))
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: config-agent
models:
  maxRetries: 4
  main:
    name: config-model
    provider: openrouter
    apiKey: config-key
    baseUrl: https://config.example/v1
rpc:
  address: 0.0.0.0
  port: 6000
session:
  maxIterations: 45
  instruct: be formal
web:
  provider: firecrawl
  apiKey: config-web-key
  baseUrl: https://config-web.example
  maxCharPerResult: 1800
  maxExtractCharPerResult: 7200
  maxExtractResponseBytes: 2048
  cacheTTL: 15m
cap:
  fs: false
  net: false
  exec: false
  mem: false
  browser: true
platform: cli
log:
  level: error
  noColor: true
debug:
  requests: true
rules:
  files:
    - agents.md
`), 0o600))

	cfg, err := Load(envPath, configPath)

	require.NoError(t, err)
	require.Equal(t, "env-agent", cfg.Name)
	require.Equal(t, "env-model", cfg.Models.Main.Name)
	require.Equal(t, "openrouter", cfg.Models.Main.Provider)
	auth, err := cfg.ResolveModelAuth()
	require.NoError(t, err)
	require.Equal(t, "config-key", auth.APIKey)
	require.Equal(t, ModelCredentialSource{Kind: ModelCredentialSourceRoleConfig}, auth.CredentialSource)
	require.Equal(t, "https://env.example/v1", cfg.Models.Main.BaseURL)
	require.Equal(t, 0, cfg.ModelMaxRetriesEffective())
	require.Equal(t, "127.0.0.1", cfg.RPC.Address)
	require.Equal(t, 7000, cfg.RPC.Port)
	require.Equal(t, 55, cfg.Session.MaxIterations)
	require.Equal(t, "warn", cfg.Log.Level)
	require.Equal(t, "/tmp/env-morph.log", cfg.Log.File)
	require.Equal(t, 25, cfg.Log.MaxSizeMB)
	require.Equal(t, 9, cfg.Log.MaxBackups)
	require.Equal(t, 30, cfg.Log.MaxAgeDays)
	require.False(t, cfg.Log.Compress)
	require.False(t, cfg.Log.NoColor)
	require.False(t, cfg.Debug.Requests)
	require.Equal(t, "tavily", cfg.Web.Provider)
	require.Equal(t, "web-env-key", cfg.Web.APIKey)
	require.Equal(t, "https://env-web.example", cfg.Web.BaseURL)
	require.Equal(t, 3100, cfg.Web.MaxCharPerResult)
	require.Equal(t, 12400, cfg.Web.MaxExtractCharPerResult)
	require.Equal(t, 4096, cfg.Web.MaxExtractResponseBytes)
	require.Equal(t, 30*time.Minute, cfg.Web.CacheTTL)
	require.True(t, cfg.Web.BlockedDomainsEnabled)
	require.Equal(t, []string{"blocked.example", "ads.example"}, cfg.Web.BlockedDomains)
	require.Equal(t, []string{"blocked.txt", "shared.txt"}, cfg.Web.BlockedDomainFiles)
	require.Equal(t, []string{"allowed.example", "docs.example"}, cfg.Web.NativeAllowedHosts)
	require.Equal(t, []string{"blocked.example", "raw.example"}, cfg.Web.NativeBlockedHosts)
	require.Equal(t, []string{"allow.txt", "safe.txt"}, cfg.Web.NativeAllowedHostFiles)
	require.Equal(t, []string{"deny.txt", "banned.txt"}, cfg.Web.NativeBlockedHostFiles)
	require.Equal(t, 13000, cfg.Web.ExtractMinSummarizeChars)
	require.Equal(t, 3200, cfg.Web.ExtractMaxSummaryChars)
	require.Equal(t, 70000, cfg.Web.ExtractMaxSummaryChunkChars)
	require.Equal(t, 190000, cfg.Web.ExtractRefusalThresholdChars)
	require.Equal(t, []string{"morph.md", "custom.md"}, cfg.Rules.Files)
	require.Equal(t, "be terse", cfg.Session.Instruct)
	require.Equal(t, "cli", cfg.Platform)
	require.True(t, getBoolValue(cfg.Cap.Filesystem))
	require.True(t, getBoolValue(cfg.Cap.Network))
	require.True(t, getBoolValue(cfg.Cap.Exec))
	require.True(t, getBoolValue(cfg.Cap.Memory))
	require.False(t, getBoolValue(cfg.Cap.Browser))
	require.False(t, cfg.MemoryEnabled())
	require.Equal(t, "default-memory", cfg.Memory.Provider)
	require.Equal(t, "sqlite", cfg.Memory.Backend)
	require.False(t, getBoolValue(cfg.Memory.Pinned.Enabled))
	require.Equal(t, 3000, cfg.Memory.Pinned.MaxChars)
	require.Equal(t, 600, cfg.Memory.Pinned.MaxItemChars)
	require.True(t, getBoolValue(cfg.Memory.Reflection.Enabled))
	require.Equal(t, 5*time.Minute, cfg.Memory.Reflection.Interval)
	require.Equal(t, 9, cfg.Memory.Reflection.Limit)
	require.Equal(t, 4, cfg.Memory.Reflection.RelatedLimit)
	require.True(t, getBoolValue(cfg.Memory.Promotion.Enabled))
	require.Equal(t, 3*time.Minute, cfg.Memory.Promotion.Interval)
	require.Equal(t, 8, cfg.Memory.Promotion.Limit)
	require.Equal(t, 48*time.Hour, cfg.Memory.Promotion.EvaluatedRetention)
}

func TestLoad_UsesModelStreamFromConfigAndEnv(t *testing.T) {
	clearEnvKeys(t, "MORPH_MODEL_STREAM")

	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(envPath, []byte("MORPH_MODEL_STREAM=true\n"), 0o600))
	require.NoError(t, os.WriteFile(configPath, []byte(`
models:
  main:
    stream: false
`), 0o600))

	cfg, err := Load(envPath, configPath)

	require.NoError(t, err)
	require.True(t, cfg.StreamEnabled())
}

func TestLoad_UsesGatewayConfigFromConfigAndEnv(t *testing.T) {
	clearEnvKeys(t,
		"MORPH_GATEWAY_ENABLED",
		"MORPH_GATEWAY_ADDRESS",
		"MORPH_GATEWAY_PORT",
		"MORPH_GATEWAY_AUTH_TOKEN",
		"MORPH_GATEWAY_PAIRING_SECRET",
		"MORPH_GATEWAY_ALLOWED_USERS",
		"MORPH_GATEWAY_TELEGRAM_ENABLED",
		"MORPH_GATEWAY_TELEGRAM_MODE",
		"MORPH_GATEWAY_TELEGRAM_BOT_TOKEN",
		"MORPH_GATEWAY_TELEGRAM_WEBHOOK_SECRET",
		"MORPH_GATEWAY_TELEGRAM_ALLOWED_USERS",
		"MORPH_GATEWAY_SLACK_ENABLED",
		"MORPH_GATEWAY_SLACK_MODE",
		"MORPH_GATEWAY_SLACK_RESPONSE_MODE",
		"MORPH_GATEWAY_SLACK_BOT_TOKEN",
		"MORPH_GATEWAY_SLACK_APP_TOKEN",
		"MORPH_GATEWAY_SLACK_SIGNING_SECRET",
		"MORPH_GATEWAY_SLACK_ALLOWED_USERS",
	)

	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(envPath, []byte(strings.Join([]string{
		"MORPH_GATEWAY_ENABLED=true",
		"MORPH_GATEWAY_ADDRESS=127.0.0.2",
		"MORPH_GATEWAY_PORT=7200",
		"MORPH_GATEWAY_AUTH_TOKEN=MORPH_GATEWAY_AUTH_TOKEN",
		"MORPH_GATEWAY_PAIRING_SECRET=MORPH_GATEWAY_PAIRING_SECRET",
		"MORPH_GATEWAY_ALLOWED_USERS=123, 456,123",
		"MORPH_GATEWAY_TELEGRAM_ENABLED=true",
		"MORPH_GATEWAY_TELEGRAM_MODE=webhook",
		"MORPH_GATEWAY_TELEGRAM_BOT_TOKEN=MORPH_GATEWAY_TELEGRAM_BOT_TOKEN",
		"MORPH_GATEWAY_TELEGRAM_WEBHOOK_SECRET=MORPH_GATEWAY_TELEGRAM_WEBHOOK_SECRET",
		"MORPH_GATEWAY_TELEGRAM_ALLOWED_USERS=789, 987",
		"MORPH_GATEWAY_SLACK_ENABLED=true",
		"MORPH_GATEWAY_SLACK_MODE=http",
		"MORPH_GATEWAY_SLACK_RESPONSE_MODE=message",
		"MORPH_GATEWAY_SLACK_BOT_TOKEN=MORPH_GATEWAY_SLACK_BOT_TOKEN",
		"MORPH_GATEWAY_SLACK_APP_TOKEN=MORPH_GATEWAY_SLACK_APP_TOKEN",
		"MORPH_GATEWAY_SLACK_SIGNING_SECRET=MORPH_GATEWAY_SLACK_SIGNING_SECRET",
		"MORPH_GATEWAY_SLACK_ALLOWED_USERS=U1, U2,U1",
		"",
	}, "\n")), 0o600))
	require.NoError(t, os.WriteFile(configPath, []byte(`
gateway:
  enabled: false
  address: 127.0.0.1
  port: 7100
  authToken: CONFIG_GATEWAY_TOKEN
  pairingSecret: CONFIG_GATEWAY_PAIRING_SECRET
  allowedUsers:
    - config-user
  telegram:
    enabled: false
    mode: polling
    botToken: CONFIG_MORPH_GATEWAY_TELEGRAM_BOT_TOKEN
    allowedUsers:
      - config-telegram-user
  slack:
    enabled: false
    mode: socket
    botToken: CONFIG_MORPH_GATEWAY_SLACK_BOT_TOKEN
    appToken: CONFIG_MORPH_GATEWAY_SLACK_APP_TOKEN
    allowedUsers:
      - config-slack-user
`), 0o600))

	cfg, err := Load(envPath, configPath)

	require.NoError(t, err)
	require.True(t, cfg.Gateway.Enabled)
	require.Equal(t, "127.0.0.2", cfg.Gateway.Address)
	require.Equal(t, 7200, cfg.Gateway.Port)
	require.Equal(t, "MORPH_GATEWAY_AUTH_TOKEN", cfg.Gateway.AuthToken)
	require.Equal(t, "MORPH_GATEWAY_PAIRING_SECRET", cfg.Gateway.PairingSecret)
	require.Equal(t, []string{"123", "456"}, cfg.Gateway.AllowedUsers)
	require.True(t, cfg.Gateway.Telegram.Enabled)
	require.Equal(t, GatewayTelegramModeWebhook, cfg.Gateway.Telegram.Mode)
	require.Equal(t, "MORPH_GATEWAY_TELEGRAM_BOT_TOKEN", cfg.Gateway.Telegram.BotToken)
	require.Equal(t, "MORPH_GATEWAY_TELEGRAM_WEBHOOK_SECRET", cfg.Gateway.Telegram.WebhookSecret)
	require.Equal(t, []string{"789", "987"}, cfg.Gateway.Telegram.AllowedUsers)
	require.True(t, cfg.Gateway.Slack.Enabled)
	require.Equal(t, GatewaySlackModeHTTP, cfg.Gateway.Slack.Mode)
	require.Equal(t, GatewaySlackResponseModeMessage, cfg.Gateway.Slack.ResponseMode)
	require.Equal(t, "MORPH_GATEWAY_SLACK_BOT_TOKEN", cfg.Gateway.Slack.BotToken)
	require.Equal(t, "MORPH_GATEWAY_SLACK_APP_TOKEN", cfg.Gateway.Slack.AppToken)
	require.Equal(t, "MORPH_GATEWAY_SLACK_SIGNING_SECRET", cfg.Gateway.Slack.SigningSecret)
	require.Equal(t, []string{"U1", "U2"}, cfg.Gateway.Slack.AllowedUsers)
}

func TestLoad_UsesGatewayConfigFromConfigFile(t *testing.T) {
	clearEnvKeys(t,
		"MORPH_GATEWAY_ENABLED",
		"MORPH_GATEWAY_ADDRESS",
		"MORPH_GATEWAY_PORT",
		"MORPH_GATEWAY_AUTH_TOKEN",
		"MORPH_GATEWAY_PAIRING_SECRET",
		"MORPH_GATEWAY_ALLOWED_USERS",
		"MORPH_GATEWAY_TELEGRAM_ENABLED",
		"MORPH_GATEWAY_TELEGRAM_MODE",
		"MORPH_GATEWAY_TELEGRAM_BOT_TOKEN",
		"MORPH_GATEWAY_TELEGRAM_WEBHOOK_SECRET",
		"MORPH_GATEWAY_TELEGRAM_ALLOWED_USERS",
		"MORPH_GATEWAY_SLACK_ENABLED",
		"MORPH_GATEWAY_SLACK_MODE",
		"MORPH_GATEWAY_SLACK_RESPONSE_MODE",
		"MORPH_GATEWAY_SLACK_BOT_TOKEN",
		"MORPH_GATEWAY_SLACK_APP_TOKEN",
		"MORPH_GATEWAY_SLACK_SIGNING_SECRET",
		"MORPH_GATEWAY_SLACK_ALLOWED_USERS",
	)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
gateway:
  enabled: true
  address: 127.0.0.3
  port: 7300
  authToken: CONFIG_GATEWAY_TOKEN
  pairingSecret: CONFIG_GATEWAY_PAIRING_SECRET
  allowedUsers:
    - " 123 "
    - "123"
    - "456"
  telegram:
    enabled: true
    mode: webhook
    botToken: CONFIG_MORPH_GATEWAY_TELEGRAM_BOT_TOKEN
    webhookSecret: CONFIG_MORPH_GATEWAY_TELEGRAM_WEBHOOK_SECRET
    allowedUsers:
      - "789"
      - " 987 "
  slack:
    enabled: true
    mode: http
    responseMode: message
    botToken: CONFIG_MORPH_GATEWAY_SLACK_BOT_TOKEN
    appToken: CONFIG_MORPH_GATEWAY_SLACK_APP_TOKEN
    signingSecret: CONFIG_MORPH_GATEWAY_SLACK_SIGNING_SECRET
    allowedUsers:
      - " U1 "
      - "U1"
      - "U2"
`), 0o600))

	cfg, err := Load("", configPath)

	require.NoError(t, err)
	require.True(t, cfg.Gateway.Enabled)
	require.Equal(t, "127.0.0.3", cfg.Gateway.Address)
	require.Equal(t, 7300, cfg.Gateway.Port)
	require.Equal(t, "CONFIG_GATEWAY_TOKEN", cfg.Gateway.AuthToken)
	require.Equal(t, "CONFIG_GATEWAY_PAIRING_SECRET", cfg.Gateway.PairingSecret)
	require.Equal(t, []string{"123", "456"}, cfg.Gateway.AllowedUsers)
	require.True(t, cfg.Gateway.Telegram.Enabled)
	require.Equal(t, GatewayTelegramModeWebhook, cfg.Gateway.Telegram.Mode)
	require.Equal(t, "CONFIG_MORPH_GATEWAY_TELEGRAM_BOT_TOKEN", cfg.Gateway.Telegram.BotToken)
	require.Equal(t, "CONFIG_MORPH_GATEWAY_TELEGRAM_WEBHOOK_SECRET", cfg.Gateway.Telegram.WebhookSecret)
	require.Equal(t, []string{"789", "987"}, cfg.Gateway.Telegram.AllowedUsers)
	require.True(t, cfg.Gateway.Slack.Enabled)
	require.Equal(t, GatewaySlackModeHTTP, cfg.Gateway.Slack.Mode)
	require.Equal(t, GatewaySlackResponseModeMessage, cfg.Gateway.Slack.ResponseMode)
	require.Equal(t, "CONFIG_MORPH_GATEWAY_SLACK_BOT_TOKEN", cfg.Gateway.Slack.BotToken)
	require.Equal(t, "CONFIG_MORPH_GATEWAY_SLACK_APP_TOKEN", cfg.Gateway.Slack.AppToken)
	require.Equal(t, "CONFIG_MORPH_GATEWAY_SLACK_SIGNING_SECRET", cfg.Gateway.Slack.SigningSecret)
	require.Equal(t, []string{"U1", "U2"}, cfg.Gateway.Slack.AllowedUsers)
}

func TestLoad_UsesGatewayCredentialEnvVars(t *testing.T) {
	clearEnvKeys(t,
		"MORPH_GATEWAY_AUTH_TOKEN",
		"MORPH_GATEWAY_PAIRING_SECRET",
		"MORPH_GATEWAY_ALLOWED_USERS",
		"MORPH_GATEWAY_TELEGRAM_BOT_TOKEN",
		"MORPH_GATEWAY_TELEGRAM_WEBHOOK_SECRET",
		"MORPH_GATEWAY_TELEGRAM_ALLOWED_USERS",
		"MORPH_GATEWAY_SLACK_BOT_TOKEN",
		"MORPH_GATEWAY_SLACK_APP_TOKEN",
		"MORPH_GATEWAY_SLACK_SIGNING_SECRET",
		"MORPH_GATEWAY_SLACK_ALLOWED_USERS",
	)

	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	require.NoError(t, os.WriteFile(envPath, []byte(strings.Join([]string{
		"MORPH_GATEWAY_AUTH_TOKEN=generic-token",
		"MORPH_GATEWAY_PAIRING_SECRET=pairing-secret",
		"MORPH_GATEWAY_ALLOWED_USERS=123,456",
		"MORPH_GATEWAY_TELEGRAM_BOT_TOKEN=telegram-token",
		"MORPH_GATEWAY_TELEGRAM_WEBHOOK_SECRET=telegram-secret",
		"MORPH_GATEWAY_TELEGRAM_ALLOWED_USERS=789,987",
		"MORPH_GATEWAY_SLACK_BOT_TOKEN=slack-bot-token",
		"MORPH_GATEWAY_SLACK_APP_TOKEN=slack-app-token",
		"MORPH_GATEWAY_SLACK_SIGNING_SECRET=slack-signing-secret",
		"MORPH_GATEWAY_SLACK_ALLOWED_USERS=U1,U2",
		"",
	}, "\n")), 0o600))

	cfg, err := Load(envPath, "")

	require.NoError(t, err)
	require.Equal(t, "generic-token", cfg.Gateway.AuthToken)
	require.Equal(t, "pairing-secret", cfg.Gateway.PairingSecret)
	require.Equal(t, []string{"123", "456"}, cfg.Gateway.AllowedUsers)
	require.Equal(t, "telegram-token", cfg.Gateway.Telegram.BotToken)
	require.Equal(t, "telegram-secret", cfg.Gateway.Telegram.WebhookSecret)
	require.Equal(t, []string{"789", "987"}, cfg.Gateway.Telegram.AllowedUsers)
	require.Equal(t, "slack-bot-token", cfg.Gateway.Slack.BotToken)
	require.Equal(t, "slack-app-token", cfg.Gateway.Slack.AppToken)
	require.Equal(t, "slack-signing-secret", cfg.Gateway.Slack.SigningSecret)
	require.Equal(t, []string{"U1", "U2"}, cfg.Gateway.Slack.AllowedUsers)
}

func TestLoad_UsesSafetyConfigFromConfigAndEnv(t *testing.T) {
	clearEnvKeys(t, "MORPH_DEV_RETAIN_UNSAFE", "MORPH_SAFETY_INPUT", "MORPH_SAFETY_OUTPUT", "MORPH_SAFETY_PII")

	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(envPath, []byte(strings.Join([]string{
		"MORPH_SAFETY_INPUT=true",
		"MORPH_SAFETY_OUTPUT=false",
		"MORPH_SAFETY_PII=true",
		"MORPH_DEV_RETAIN_UNSAFE=true",
		"",
	}, "\n")), 0o600))
	require.NoError(t, os.WriteFile(configPath, []byte(`
safety:
  input: false
  output: true
  pii: false
`), 0o600))

	cfg, err := Load(envPath, configPath)

	require.NoError(t, err)
	require.True(t, cfg.InputSafetyEnabled())
	require.False(t, cfg.OutputSafetyEnabled())
	require.True(t, cfg.OutputPIIRedactionEnabled())
	require.True(t, cfg.RetainUnsafeEnabled())
}

func TestLoad_UsesUnsafeRetentionFromConfig(t *testing.T) {
	clearEnvKeys(t, "MORPH_DEV_RETAIN_UNSAFE")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
dev:
  retainUnsafe: true
`), 0o600))

	cfg, err := Load("", configPath)

	require.NoError(t, err)
	require.True(t, cfg.RetainUnsafeEnabled())
}

func TestLoad_UsesTUIConfigFromConfigAndEnv(t *testing.T) {
	clearEnvKeys(t, "MORPH_TUI_THINKING_COMPOSER")

	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(envPath, []byte("MORPH_TUI_THINKING_COMPOSER=true\n"), 0o600))
	require.NoError(t, os.WriteFile(configPath, []byte(`
tui:
  thinkingComposer: false
`), 0o600))

	cfg, err := Load(envPath, configPath)

	require.NoError(t, err)
	require.True(t, cfg.TUIThinkingComposerEnabled())
}

func TestLoad_IgnoresInvalidMaxIterationsEnvOverride(t *testing.T) {
	clearEnvKeys(t, "MORPH_SESSION_MAX_ITERATIONS", "MORPH_MODEL_API")

	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(envPath, []byte("MORPH_SESSION_MAX_ITERATIONS=invalid\n"), 0o600))
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: config-agent
models:
  providers:
    openrouter:
      apiKey: config-key
  main:
    name: config-model
    provider: openrouter
rpc:
  address: 127.0.0.1
  port: 50051
session:
  maxIterations: 45
log:
  level: info
`), 0o600))

	cfg, err := Load(envPath, configPath)

	require.NoError(t, err)
	require.Equal(t, 45, cfg.Session.MaxIterations)
}

func TestApplyEnvOverrides_IgnoresInvalidWebCacheTTL(t *testing.T) {
	clearEnvKeys(t, "MORPH_WEB_CACHE_TTL")
	t.Setenv("MORPH_WEB_CACHE_TTL", "not-a-duration")

	cfg := &Config{}
	applyEnvOverrides(cfg)
	cfg.Normalize()

	require.Equal(t, constants.DefaultWebCacheTTL, cfg.Web.CacheTTL)
}

func TestApplyEnvOverrides_SetsDockerImageVerification(t *testing.T) {
	clearEnvKeys(t, "MORPH_EXECUTION_DOCKER_IMAGE_VERIFICATION")
	t.Setenv("MORPH_EXECUTION_DOCKER_IMAGE_VERIFICATION", "digest")
	cfg := &Config{}

	applyEnvOverrides(cfg)

	require.Equal(
		t,
		ExecutionImageVerificationDigest,
		cfg.Execution.Docker.ImageVerification,
	)
}

func TestApplyEnvOverrides_CoversRemainingBranches(t *testing.T) {
	clearEnvKeys(t,
		"MORPH_MODEL_CONTEXT_LENGTH", "MORPH_MODEL_MAX_RETRIES", "OPENAI_API_KEY", "OPENROUTER_API_KEY",
		"MORPH_STORAGE_BACKEND", "MORPH_SESSION_DEFAULT_IDLE_EXPIRY", "MORPH_SESSION_ARCHIVE_RETENTION",
		"MORPH_SEARCH_VECTOR_ENABLED", "MORPH_MODEL_EMBEDDING_PROVIDER",
		"MORPH_MODEL_EMBEDDING_MODEL", "MORPH_SEARCH_VECTOR_REQUIRED",
		"MORPH_SEARCH_VECTOR_REBUILD_BATCH_SIZE", "MORPH_SEARCH_VECTOR_MAX_INPUT_BYTES",
		"MORPH_SEARCH_VECTOR_MAX_DOCUMENT_BYTES", "MORPH_SEARCH_ENABLE_RERANK", "MORPH_RERANKER_ENABLED",
		"MORPH_RERANKER_TYPE", "MORPH_RERANKER_MODEL", "MORPH_RERANKER_MAX_CANDIDATES",
		"MORPH_RERANKER_MAX_CANDIDATE_TEXT_CHARS", "MORPH_RERANKER_MAX_OUTPUT_TOKENS", "MORPH_RERANKER_OVERRIDES",
		"MORPH_COMPACTION_ENABLED", "MORPH_COMPACTION_TRIGGER_PERCENT", "MORPH_COMPACTION_WARN_PERCENT",
		"MORPH_MEMORY_ENABLED", "MORPH_MEMORY_PROVIDER", "MORPH_MEMORY_BACKEND",
		"MORPH_MEMORY_PINNED_ENABLED", "MORPH_MEMORY_PINNED_MAX_CHARS", "MORPH_MEMORY_PINNED_MAX_ITEM_CHARS",
		"MORPH_MEMORY_EPISODIC_ENABLED", "MORPH_MEMORY_EPISODIC_INTERVAL",
		"MORPH_MEMORY_EPISODIC_IDLE_AFTER", "MORPH_MEMORY_EPISODIC_MIN_MESSAGES",
		"MORPH_MEMORY_EPISODIC_WINDOW_SIZE", "MORPH_MEMORY_EPISODIC_MAX_WINDOWS",
		"MORPH_MEMORY_EPISODIC_MAX_WINDOW_CHARS", "MORPH_MEMORY_EPISODIC_MAX_WINDOW_TOKENS",
		"MORPH_MEMORY_EPISODIC_MAX_RETRIES",
		"MORPH_TUI_THINKING_COMPOSER",
		"MORPH_FIRECRAWL_API_KEY", "MORPH_FIRECRAWL_API_URL", "MORPH_PARALLEL_API_KEY", "MORPH_TAVILY_API_KEY", "MORPH_EXA_API_KEY",
	)

	cfg := &Config{}
	applyEnvOverrides(nil)

	t.Setenv("MORPH_MODEL_CONTEXT_LENGTH", "64000")
	t.Setenv("MORPH_MODEL_MAX_RETRIES", "0")
	t.Setenv("OPENAI_API_KEY", "openai-key")
	t.Setenv("OPENROUTER_API_KEY", "openrouter-key")
	t.Setenv("MORPH_STORAGE_BACKEND", "memory")
	t.Setenv("MORPH_SESSION_DEFAULT_IDLE_EXPIRY", "2h")
	t.Setenv("MORPH_SESSION_ARCHIVE_RETENTION", "48h")
	t.Setenv("MORPH_SEARCH_VECTOR_ENABLED", "true")
	t.Setenv("MORPH_MODEL_EMBEDDING_PROVIDER", "test")
	t.Setenv("MORPH_MODEL_EMBEDDING_MODEL", "text-embedding-test")
	t.Setenv("MORPH_SEARCH_VECTOR_REQUIRED", "true")
	t.Setenv("MORPH_SEARCH_VECTOR_REBUILD_BATCH_SIZE", "32")
	t.Setenv("MORPH_SEARCH_VECTOR_MAX_INPUT_BYTES", "1024")
	t.Setenv("MORPH_SEARCH_VECTOR_MAX_DOCUMENT_BYTES", "16384")
	t.Setenv("MORPH_SEARCH_ENABLE_RERANK", "false")
	t.Setenv("MORPH_RERANKER_ENABLED", "false")
	t.Setenv("MORPH_RERANKER_TYPE", constants.RerankerLLM)
	t.Setenv("MORPH_RERANKER_MODEL", "gpt-4o-mini")
	t.Setenv("MORPH_RERANKER_MAX_CANDIDATES", "12")
	t.Setenv("MORPH_RERANKER_MAX_CANDIDATE_TEXT_CHARS", "700")
	t.Setenv("MORPH_RERANKER_MAX_OUTPUT_TOKENS", "256")
	t.Setenv("MORPH_RERANKER_OVERRIDES", `{"memory_reflection":{"type":"llm","model":"gpt-4o-mini","maxCandidates":7,"maxCandidateTextChars":500,"maxOutputTokens":96}}`)
	t.Setenv("MORPH_COMPACTION_ENABLED", "false")
	t.Setenv("MORPH_COMPACTION_TRIGGER_PERCENT", "0.5")
	t.Setenv("MORPH_COMPACTION_WARN_PERCENT", "0.8")
	t.Setenv("MORPH_MEMORY_ENABLED", "true")
	t.Setenv("MORPH_MEMORY_PROVIDER", " Default-Memory ")
	t.Setenv("MORPH_MEMORY_BACKEND", " SQLite ")
	t.Setenv("MORPH_MEMORY_PINNED_ENABLED", "false")
	t.Setenv("MORPH_MEMORY_RETRIEVAL_ENABLED", "false")
	t.Setenv("MORPH_MEMORY_FLUSH_ENABLED", "true")
	t.Setenv("MORPH_MEMORY_FLUSH_MAX_CALLS", "3")
	t.Setenv("MORPH_MEMORY_FLUSH_MAX_OUTPUT_TOKENS", "256")
	t.Setenv("MORPH_MEMORY_FLUSH_TIMEOUT", "4s")
	t.Setenv("MORPH_MEMORY_PINNED_MAX_CHARS", "3200")
	t.Setenv("MORPH_MEMORY_PINNED_MAX_ITEM_CHARS", "700")
	t.Setenv("MORPH_MEMORY_EPISODIC_ENABLED", "true")
	t.Setenv("MORPH_MEMORY_EPISODIC_INTERVAL", "20m")
	t.Setenv("MORPH_MEMORY_EPISODIC_IDLE_AFTER", "10m")
	t.Setenv("MORPH_MEMORY_EPISODIC_MIN_MESSAGES", "5")
	t.Setenv("MORPH_MEMORY_EPISODIC_WINDOW_SIZE", "10")
	t.Setenv("MORPH_MEMORY_EPISODIC_MAX_WINDOWS", "3")
	t.Setenv("MORPH_MEMORY_EPISODIC_MAX_WINDOW_CHARS", "4000")
	t.Setenv("MORPH_MEMORY_EPISODIC_MAX_WINDOW_TOKENS", "1000")
	t.Setenv("MORPH_MEMORY_EPISODIC_MAX_RETRIES", "2")
	t.Setenv("MORPH_MEMORY_WRITE_ENABLED", "false")
	t.Setenv("MORPH_TUI_THINKING_COMPOSER", "false")

	applyEnvOverrides(cfg)

	require.Equal(t, 64000, cfg.Models.Main.ContextLength)
	require.Equal(t, 0, cfg.ModelMaxRetriesEffective())
	require.Equal(t, "memory", cfg.Storage.Backend)
	require.False(t, cfg.TUIThinkingComposerEnabled())
	require.Equal(t, 2*time.Hour, cfg.Session.DefaultIdleExpiry)
	require.Equal(t, 48*time.Hour, cfg.Session.ArchiveRetention)
	require.True(t, cfg.Search.Vector.Enabled)
	require.Equal(t, "test", cfg.Models.Embedding.Provider)
	require.Equal(t, "text-embedding-test", cfg.Models.Embedding.Name)
	require.True(t, cfg.Search.Vector.Required)
	require.Equal(t, 32, cfg.Search.Vector.RebuildBatchSize)
	require.Equal(t, 1024, cfg.Search.Vector.MaxInputBytes)
	require.Equal(t, 16384, cfg.Search.Vector.MaxDocumentBytes)
	require.False(t, getBoolValueDefault(cfg.Search.EnableRerank, true))
	require.False(t, getBoolValueDefault(cfg.Reranker.Enabled, true))
	require.Equal(t, constants.RerankerLLM, cfg.Reranker.Type)
	require.Equal(t, "gpt-4o-mini", cfg.Reranker.Model)
	require.Equal(t, 12, cfg.Reranker.MaxCandidates)
	require.Equal(t, 700, cfg.Reranker.MaxCandidateTextChars)
	require.Equal(t, 256, cfg.Reranker.MaxOutputTokens)
	require.Equal(t, RerankerOverrideConfig{
		Type:                  constants.RerankerLLM,
		Model:                 "gpt-4o-mini",
		MaxCandidates:         testIntPtr(7),
		MaxCandidateTextChars: testIntPtr(500),
		MaxOutputTokens:       testIntPtr(96),
	}, cfg.Reranker.Overrides["memory_reflection"])
	require.False(t, getBoolValue(cfg.Compaction.Enabled))
	require.Equal(t, 0.5, cfg.Compaction.TriggerPercent)
	require.Equal(t, 0.8, cfg.Compaction.WarnPercent)
	require.True(t, cfg.MemoryEnabled())
	require.Equal(t, "default-memory", cfg.Memory.Provider)
	require.Equal(t, "sqlite", cfg.Memory.Backend)
	require.False(t, getBoolValue(cfg.Memory.Pinned.Enabled))
	require.False(t, getBoolValue(cfg.Memory.Retrieval.Enabled))
	require.True(t, getBoolValue(cfg.Memory.Flush.Enabled))
	require.Equal(t, 3, cfg.Memory.Flush.MaxCalls)
	require.Equal(t, int64(256), cfg.Memory.Flush.MaxOutputTokens)
	require.Equal(t, 4*time.Second, cfg.Memory.Flush.Timeout)
	require.Equal(t, 3200, cfg.Memory.Pinned.MaxChars)
	require.Equal(t, 700, cfg.Memory.Pinned.MaxItemChars)
	require.True(t, getBoolValue(cfg.Memory.Episodic.Enabled))
	require.Equal(t, 20*time.Minute, cfg.Memory.Episodic.Interval)
	require.Equal(t, 10*time.Minute, cfg.Memory.Episodic.IdleAfter)
	require.Equal(t, 5, cfg.Memory.Episodic.MinMessages)
	require.Equal(t, 10, cfg.Memory.Episodic.WindowSize)
	require.Equal(t, 3, cfg.Memory.Episodic.MaxWindows)
	require.Equal(t, 4000, cfg.Memory.Episodic.MaxWindowChars)
	require.Equal(t, 1000, cfg.Memory.Episodic.MaxWindowTokens)
	require.Equal(t, 2, cfg.Memory.Episodic.MaxRetries)
	require.False(t, getBoolValue(cfg.Memory.Write.Enabled))
}

func TestApplyEnvOverrides_IgnoresInvalidVectorByteLimits(t *testing.T) {
	cfg := Config{Search: SearchConfig{Vector: SearchVectorConfig{
		MaxInputBytes:    2048,
		MaxDocumentBytes: 32768,
	}}}
	t.Setenv("MORPH_SEARCH_VECTOR_MAX_INPUT_BYTES", "invalid")
	t.Setenv("MORPH_SEARCH_VECTOR_MAX_DOCUMENT_BYTES", "invalid")

	applyEnvOverrides(&cfg)

	require.Equal(t, 2048, cfg.Search.Vector.MaxInputBytes)
	require.Equal(t, 32768, cfg.Search.Vector.MaxDocumentBytes)
}

func TestApplyEnvOverrides_WebProviderSpecificFallback(t *testing.T) {
	clearEnvKeys(t,
		"MORPH_WEB_PROVIDER", "MORPH_WEB_API_KEY", "MORPH_WEB_BASE_URL",
		"MORPH_FIRECRAWL_API_KEY", "MORPH_FIRECRAWL_API_URL", "MORPH_PARALLEL_API_KEY", "MORPH_TAVILY_API_KEY", "MORPH_EXA_API_KEY",
	)

	cfg := &Config{}
	t.Setenv("MORPH_FIRECRAWL_API_URL", "http://localhost:3002")

	applyEnvOverrides(cfg)

	require.Equal(t, "firecrawl", cfg.Web.Provider)
	require.Equal(t, "", cfg.Web.APIKey)
	require.Equal(t, "http://localhost:3002", cfg.Web.BaseURL)

	cfg = &Config{}
	t.Setenv("MORPH_WEB_PROVIDER", "exa")
	t.Setenv("MORPH_EXA_API_KEY", "exa-key")

	applyEnvOverrides(cfg)

	require.Equal(t, "exa", cfg.Web.Provider)
	require.Equal(t, "exa-key", cfg.Web.APIKey)
}

func TestApplyEnvOverrides_SummaryModelAndRelatedEnv(t *testing.T) {
	clearEnvKeys(t,
		"MORPH_MODEL_SUMMARY", "MORPH_MODEL_SUMMARY_PROVIDER", "MORPH_MODEL_SUMMARY_BASE_URL",
		"MORPH_MODEL_API", "MORPH_MODEL_SUMMARY_API",
	)

	cfg := &Config{}
	t.Setenv("MORPH_MODEL_SUMMARY", "gpt-4o-mini")
	t.Setenv("MORPH_MODEL_SUMMARY_PROVIDER", "openai")
	t.Setenv("MORPH_MODEL_SUMMARY_BASE_URL", "https://example.com/v1")
	t.Setenv("MORPH_MODEL_API", "openai-responses")
	t.Setenv("MORPH_MODEL_SUMMARY_API", "openai-responses")

	applyEnvOverrides(cfg)

	require.Equal(t, "gpt-4o-mini", cfg.Models.Summary.Name)
	require.Equal(t, "openai", cfg.Models.Summary.Provider)
	require.Equal(t, "https://example.com/v1", cfg.Models.Summary.BaseURL)
	require.Equal(t, "openai-responses", cfg.Models.Main.API)
	require.Equal(t, "openai-responses", cfg.Models.Summary.API)
}

func TestLoad_UsesModelAPIFromEnvOverride(t *testing.T) {
	clearEnvKeys(t, "MORPH_MODEL_API")

	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(envPath, []byte("MORPH_MODEL_API=openai-responses\n"), 0o600))
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: config-agent
models:
  providers:
    openrouter:
      apiKey: config-key
  main:
    name: config-model
    provider: openai
    api: openai-completions
rpc:
  address: 127.0.0.1
  port: 50051
log:
  level: info
`), 0o600))

	cfg, err := Load(envPath, configPath)
	require.NoError(t, err)
	require.Equal(t, "openai-responses", cfg.Models.Main.API)
}
