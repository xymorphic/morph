package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xymorphic/morph/internal/constants"
	modelprovider "github.com/xymorphic/morph/internal/model/provider"
)

func TestPreloadEnvFile_LoadsValues(t *testing.T) {
	clearEnvKeys(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "OPENROUTER_API_KEY", "OPENAI_API_KEY", "OPENROUTER_API_KEY",
		"MORPH_MODEL_BASE_URL", "MORPH_MODEL_API", "MORPH_RPC_ADDRESS", "MORPH_RPC_PORT", "MORPH_SESSION_MAX_ITERATIONS", "MORPH_LOG_LEVEL",
		"MORPH_LOG_FILE", "MORPH_LOG_MAX_SIZE_MB", "MORPH_LOG_MAX_BACKUPS", "MORPH_LOG_MAX_AGE_DAYS", "MORPH_LOG_COMPRESS",
		"MORPH_LOG_NO_COLOR", "MORPH_DEBUG_REQUESTS", "MORPH_RULES_FILES", "MORPH_SESSION_INSTRUCT", "MORPH_PLATFORM", "MORPH_CAP_FS", "MORPH_CAP_NET",
		"MORPH_CAP_EXEC", "MORPH_CAP_MEM", "MORPH_CAP_BROWSER", "MORPH_MEMORY_ENABLED", "MORPH_MEMORY_PROVIDER", "MORPH_MEMORY_BACKEND",
		"MORPH_MEMORY_PINNED_ENABLED", "MORPH_MEMORY_PINNED_MAX_CHARS", "MORPH_MEMORY_PINNED_MAX_ITEM_CHARS")

	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	require.NoError(t, os.WriteFile(envPath, []byte(`
MORPH_NAME=env-agent
MORPH_MODEL=env-model
MORPH_MODEL_PROVIDER=openrouter
OPENAI_API_KEY=openai-env-key
OPENROUTER_API_KEY=openrouter-env-key
MORPH_MODEL_BASE_URL=https://env.example/v1
MORPH_RPC_ADDRESS=0.0.0.0
MORPH_RPC_PORT=6000
MORPH_SESSION_MAX_ITERATIONS=45
MORPH_LOG_LEVEL=warn
MORPH_LOG_FILE=/tmp/morph.log
MORPH_LOG_MAX_SIZE_MB=25
MORPH_LOG_MAX_BACKUPS=9
MORPH_LOG_MAX_AGE_DAYS=30
MORPH_LOG_COMPRESS=false
MORPH_LOG_NO_COLOR=true
MORPH_DEBUG_REQUESTS=true
MORPH_RULES_FILES=morph.md,custom.md
MORPH_SESSION_INSTRUCT=be terse
MORPH_PLATFORM=cli
MORPH_CAP_FS=false
MORPH_CAP_NET=false
MORPH_CAP_EXEC=false
MORPH_CAP_MEM=false
MORPH_CAP_BROWSER=true
MORPH_MEMORY_ENABLED=true
MORPH_MEMORY_PROVIDER=default-memory
MORPH_MEMORY_BACKEND=memory
MORPH_MEMORY_PINNED_ENABLED=false
MORPH_MEMORY_PINNED_MAX_CHARS=2000
MORPH_MEMORY_PINNED_MAX_ITEM_CHARS=500
`), 0o600))

	require.NoError(t, PreloadEnvFile(envPath))
	require.Equal(t, "env-agent", os.Getenv("MORPH_NAME"))
	require.Equal(t, "env-model", os.Getenv("MORPH_MODEL"))
	require.Equal(t, "openrouter", os.Getenv("MORPH_MODEL_PROVIDER"))
	require.Equal(t, "openai-env-key", os.Getenv("OPENAI_API_KEY"))
	require.Equal(t, "openrouter-env-key", os.Getenv("OPENROUTER_API_KEY"))
	require.Equal(t, "https://env.example/v1", os.Getenv("MORPH_MODEL_BASE_URL"))
	require.Equal(t, "0.0.0.0", os.Getenv("MORPH_RPC_ADDRESS"))
	require.Equal(t, "6000", os.Getenv("MORPH_RPC_PORT"))
	require.Equal(t, "45", os.Getenv("MORPH_SESSION_MAX_ITERATIONS"))
	require.Equal(t, "warn", os.Getenv("MORPH_LOG_LEVEL"))
	require.Equal(t, "/tmp/morph.log", os.Getenv("MORPH_LOG_FILE"))
	require.Equal(t, "25", os.Getenv("MORPH_LOG_MAX_SIZE_MB"))
	require.Equal(t, "9", os.Getenv("MORPH_LOG_MAX_BACKUPS"))
	require.Equal(t, "30", os.Getenv("MORPH_LOG_MAX_AGE_DAYS"))
	require.Equal(t, "false", os.Getenv("MORPH_LOG_COMPRESS"))
	require.Equal(t, "true", os.Getenv("MORPH_LOG_NO_COLOR"))
	require.Equal(t, "true", os.Getenv("MORPH_DEBUG_REQUESTS"))
	require.Equal(t, "morph.md,custom.md", os.Getenv("MORPH_RULES_FILES"))
	require.Equal(t, "be terse", os.Getenv("MORPH_SESSION_INSTRUCT"))
	require.Equal(t, "cli", os.Getenv("MORPH_PLATFORM"))
	require.Equal(t, "false", os.Getenv("MORPH_CAP_FS"))
	require.Equal(t, "false", os.Getenv("MORPH_CAP_NET"))
	require.Equal(t, "false", os.Getenv("MORPH_CAP_EXEC"))
	require.Equal(t, "false", os.Getenv("MORPH_CAP_MEM"))
	require.Equal(t, "true", os.Getenv("MORPH_CAP_BROWSER"))
	require.Equal(t, "true", os.Getenv("MORPH_MEMORY_ENABLED"))
	require.Equal(t, "default-memory", os.Getenv("MORPH_MEMORY_PROVIDER"))
	require.Equal(t, "memory", os.Getenv("MORPH_MEMORY_BACKEND"))
	require.Equal(t, "false", os.Getenv("MORPH_MEMORY_PINNED_ENABLED"))
	require.Equal(t, "2000", os.Getenv("MORPH_MEMORY_PINNED_MAX_CHARS"))
	require.Equal(t, "500", os.Getenv("MORPH_MEMORY_PINNED_MAX_ITEM_CHARS"))
}

func TestPreloadEnvFile_DoesNotOverrideShellEnv(t *testing.T) {
	clearEnvKeys(t, "OPENROUTER_API_KEY")
	t.Setenv("OPENROUTER_API_KEY", "shell-key")

	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	require.NoError(t, os.WriteFile(envPath, []byte("OPENROUTER_API_KEY=env-key\n"), 0o600))

	require.NoError(t, PreloadEnvFile(envPath))
	require.Equal(t, "shell-key", os.Getenv("OPENROUTER_API_KEY"))
}

func TestPreloadEnvFile_ReturnsErrorForUnreadablePath(t *testing.T) {
	dir := t.TempDir()
	require.EqualError(t, PreloadEnvFile(dir), `failed to load env file "`+dir+`": read `+dir+`: is a directory`)
}

func TestPreloadEnvFile_UsesDefaultPathWhenEmpty(t *testing.T) {
	originalLoadDotEnv := loadDotEnv
	t.Cleanup(func() {
		loadDotEnv = originalLoadDotEnv
	})

	calledWith := ""
	loadDotEnv = func(filenames ...string) error {
		if len(filenames) > 0 {
			calledWith = filenames[0]
		}

		return nil
	}

	require.NoError(t, PreloadEnvFile(""))
	require.Equal(t, ".env", calledWith)
}

func TestPreloadEnvFile_IgnoresMissingFile(t *testing.T) {
	originalLoadDotEnv := loadDotEnv
	t.Cleanup(func() {
		loadDotEnv = originalLoadDotEnv
	})

	loadDotEnv = func(...string) error {
		return os.ErrNotExist
	}

	require.NoError(t, PreloadEnvFile("missing.env"))
}

func TestConfig_ToYAMLAndSaveYAML(t *testing.T) {
	cfg := NewDefaultConfig()
	cfg.Name = "alpha"
	path := filepath.Join(t.TempDir(), "profile", "config.yaml")

	require.NoError(t, SaveYAML(path, cfg))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	loaded, err := loadConfigFile(path)
	require.NoError(t, err)
	require.Equal(t, "alpha", loaded.Name)
	require.Equal(t, cfg.Models.Main.Name, loaded.Models.Main.Name)
	require.True(t, getBoolValue(loaded.Safety.Input))
	require.True(t, getBoolValue(loaded.Safety.Output))
	require.True(t, getBoolValue(loaded.Safety.PII))
	require.Equal(t, map[string]RerankerOverrideConfig{
		"memory_episodic_extraction": {Type: constants.RerankerLLM},
		"memory_promotion":           {Type: constants.RerankerLLM},
		"memory_reflection":          {Type: constants.RerankerLLM},
	}, loaded.Reranker.Overrides)
}

func TestSaveYAML_RefusesOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("name: existing\n"), 0o600))

	cfg := NewDefaultConfig()
	cfg.Name = "alpha"
	err := SaveYAML(path, cfg)

	require.EqualError(t, err, "config file already exists: "+path)
	data, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	require.Equal(t, "name: existing\n", string(data))
}

func TestSaveYAML_ReturnsValidationErrors(t *testing.T) {
	err := SaveYAML("", NewDefaultConfig())
	require.EqualError(t, err, "config path is required")

	err = SaveYAML(filepath.Join(t.TempDir(), "config.yaml"), nil)
	require.EqualError(t, err, "config is required")
}

func TestLoad_UsesConfigFileValues(t *testing.T) {
	clearEnvKeys(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "OPENROUTER_API_KEY", "OPENAI_API_KEY",
		"OPENROUTER_API_KEY",
		"MORPH_MODEL_BASE_URL", "MORPH_MODEL_API", "MORPH_RPC_ADDRESS", "MORPH_RPC_PORT", "MORPH_SESSION_MAX_ITERATIONS",
		"MORPH_LOG_LEVEL", "MORPH_LOG_NO_COLOR",
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
		"MORPH_MEMORY_REFLECTION_ENABLED", "MORPH_MEMORY_REFLECTION_INTERVAL",
		"MORPH_MEMORY_REFLECTION_LIMIT", "MORPH_MEMORY_REFLECTION_RELATED_LIMIT",
		"MORPH_MEMORY_PROMOTION_ENABLED", "MORPH_MEMORY_PROMOTION_INTERVAL",
		"MORPH_MEMORY_PROMOTION_LIMIT", "MORPH_MEMORY_PROMOTION_EVALUATED_RETENTION")

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
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
  instruct: be terse
cap:
  fs: false
  net: false
  exec: false
  mem: false
  browser: true
platform: cli
memory:
  enabled: true
  provider: default-memory
  backend: memory
  pinned:
    enabled: false
    maxChars: 2000
    maxItemChars: 500
  episodic:
    enabled: true
    interval: 30m
    idleAfter: 15m
    minMessages: 3
    windowSize: 12
    maxWindows: 4
    maxWindowChars: 5000
    maxWindowTokens: 1250
    maxRetries: 2
  reflection:
    enabled: true
    interval: 4m
    limit: 6
    relatedLimit: 2
  promotion:
    enabled: true
    interval: 2m
    limit: 7
    evaluatedRetention: 72h
log:
  level: error
  file: /tmp/config-morph.log
  noColor: true
debug:
  requests: true
web:
  provider: exa
  apiKey: web-key
  baseUrl: https://web.example
  maxCharPerResult: 2400
  maxExtractCharPerResult: 9600
  maxExtractResponseBytes: 2048
  cacheTTL: 15m
  blockedDomains:
    enabled: true
    domains:
      - blocked.example
    files:
      - blocked.txt
  native:
    allowedHosts:
      - allowed.example
    blockedHosts:
      - blocked.example
    allowedHostFiles:
      - allow.txt
    blockedHostFiles:
      - deny.txt
  extractMinSummarizeChars: 12000
  extractMaxSummaryChars: 3000
  extractMaxSummaryChunkChars: 60000
  extractRefusalThresholdChars: 180000
rules:
  files:
    - morph.md
    - custom.md
`), 0o600))

	cfg, err := Load("", configPath)

	require.NoError(t, err)
	require.Equal(t, "config-agent", cfg.Name)
	require.Equal(t, "config-model", cfg.Models.Main.Name)
	require.Equal(t, "openrouter", cfg.Models.Main.Provider)
	require.Equal(t, "config-key", cfg.Models.Main.APIKey)
	require.Equal(t, "https://config.example/v1", cfg.Models.Main.BaseURL)
	require.Equal(t, 4, cfg.ModelMaxRetriesEffective())
	require.Equal(t, "0.0.0.0", cfg.RPC.Address)
	require.Equal(t, 6000, cfg.RPC.Port)
	require.Equal(t, 45, cfg.Session.MaxIterations)
	require.Equal(t, "error", cfg.Log.Level)
	require.Equal(t, "/tmp/config-morph.log", cfg.Log.File)
	require.True(t, cfg.Log.NoColor)
	require.True(t, cfg.Debug.Requests)
	require.Equal(t, "exa", cfg.Web.Provider)
	require.Equal(t, "web-key", cfg.Web.APIKey)
	require.Equal(t, "https://web.example", cfg.Web.BaseURL)
	require.Equal(t, 2400, cfg.Web.MaxCharPerResult)
	require.Equal(t, 9600, cfg.Web.MaxExtractCharPerResult)
	require.Equal(t, 2048, cfg.Web.MaxExtractResponseBytes)
	require.Equal(t, 15*time.Minute, cfg.Web.CacheTTL)
	require.True(t, cfg.Web.BlockedDomainsEnabled)
	require.Equal(t, []string{"blocked.example"}, cfg.Web.BlockedDomains)
	require.Equal(t, []string{filepath.Join(dir, "blocked.txt")}, cfg.Web.BlockedDomainFiles)
	require.Equal(t, []string{"allowed.example"}, cfg.Web.NativeAllowedHosts)
	require.Equal(t, []string{"blocked.example"}, cfg.Web.NativeBlockedHosts)
	require.Equal(t, []string{filepath.Join(dir, "allow.txt")}, cfg.Web.NativeAllowedHostFiles)
	require.Equal(t, []string{filepath.Join(dir, "deny.txt")}, cfg.Web.NativeBlockedHostFiles)
	require.Equal(t, 12000, cfg.Web.ExtractMinSummarizeChars)
	require.Equal(t, 3000, cfg.Web.ExtractMaxSummaryChars)
	require.Equal(t, 60000, cfg.Web.ExtractMaxSummaryChunkChars)
	require.Equal(t, 180000, cfg.Web.ExtractRefusalThresholdChars)
	require.Equal(t, []string{"morph.md", "custom.md"}, cfg.Rules.Files)
	require.Equal(t, "be terse", cfg.Session.Instruct)
	require.Equal(t, "cli", cfg.Platform)
	require.True(t, cfg.MemoryEnabled())
	require.Equal(t, "default-memory", cfg.Memory.Provider)
	require.Equal(t, "memory", cfg.Memory.Backend)
	require.False(t, getBoolValue(cfg.Memory.Pinned.Enabled))
	require.Equal(t, 2000, cfg.Memory.Pinned.MaxChars)
	require.Equal(t, 500, cfg.Memory.Pinned.MaxItemChars)
	require.True(t, getBoolValue(cfg.Memory.Episodic.Enabled))
	require.Equal(t, 30*time.Minute, cfg.Memory.Episodic.Interval)
	require.Equal(t, 15*time.Minute, cfg.Memory.Episodic.IdleAfter)
	require.Equal(t, 3, cfg.Memory.Episodic.MinMessages)
	require.Equal(t, 12, cfg.Memory.Episodic.WindowSize)
	require.Equal(t, 4, cfg.Memory.Episodic.MaxWindows)
	require.Equal(t, 5000, cfg.Memory.Episodic.MaxWindowChars)
	require.Equal(t, 1250, cfg.Memory.Episodic.MaxWindowTokens)
	require.Equal(t, 2, cfg.Memory.Episodic.MaxRetries)
	require.True(t, getBoolValue(cfg.Memory.Reflection.Enabled))
	require.Equal(t, 4*time.Minute, cfg.Memory.Reflection.Interval)
	require.Equal(t, 6, cfg.Memory.Reflection.Limit)
	require.Equal(t, 2, cfg.Memory.Reflection.RelatedLimit)
	require.True(t, getBoolValue(cfg.Memory.Promotion.Enabled))
	require.Equal(t, 2*time.Minute, cfg.Memory.Promotion.Interval)
	require.Equal(t, 7, cfg.Memory.Promotion.Limit)
	require.Equal(t, 72*time.Hour, cfg.Memory.Promotion.EvaluatedRetention)
	require.False(t, getBoolValue(cfg.Cap.Filesystem))
	require.False(t, getBoolValue(cfg.Cap.Network))
	require.False(t, getBoolValue(cfg.Cap.Exec))
	require.False(t, getBoolValue(cfg.Cap.Memory))
	require.True(t, getBoolValue(cfg.Cap.Browser))
}

func TestLoad_PreservesSmallerConfiguredContextLengthThanRegistryMetadata(t *testing.T) {
	stubModelRegistry(t, registryWithGenerationModel(constants.ModelProviderOpenAI, "openai/test-chat-small", 8191))

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: config-agent
models:
  providers:
    openai:
      apiKey: config-key
  main:
    name: openai/test-chat-small
    provider: openai
    contextLength: 4000
rpc:
  address: 127.0.0.1
  port: 50051
log:
  level: info
`), 0o600))

	cfg, err := Load("", configPath)
	require.NoError(t, err)
	require.Equal(t, 4000, cfg.Models.Main.ContextLength)
}

func TestLoad_IgnoresMissingConfigFile(t *testing.T) {
	clearEnvKeys(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "OPENROUTER_API_KEY", "OPENAI_API_KEY",
		"OPENROUTER_API_KEY", "MORPH_MODEL_BASE_URL", "MORPH_MODEL_API", "MORPH_MODEL_MAX_RETRIES",
		"MORPH_RPC_ADDRESS", "MORPH_RPC_PORT", "MORPH_SESSION_MAX_ITERATIONS", "MORPH_LOG_LEVEL", "MORPH_LOG_NO_COLOR", "MORPH_DEBUG_REQUESTS")

	cfg, err := Load("", filepath.Join(t.TempDir(), "missing.yaml"))

	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Equal(t, DefaultConfig.Models.Main.Name, cfg.Models.Main.Name)
	require.Equal(t, DefaultConfig.Models.Main.Provider, cfg.Models.Main.Provider)
	require.Equal(t, constants.DefaultRPCAddress, cfg.RPC.Address)
	require.Equal(t, constants.DefaultRPCPort, cfg.RPC.Port)
	require.Equal(t, DefaultConfig.Session.MaxIterations, cfg.Session.MaxIterations)
	require.Equal(t, DefaultConfig.Platform, cfg.Platform)
	require.True(t, getBoolValue(cfg.Cap.Filesystem))
	require.True(t, getBoolValue(cfg.Cap.Network))
	require.True(t, getBoolValue(cfg.Cap.Exec))
	require.True(t, getBoolValue(cfg.Cap.Memory))
	require.False(t, getBoolValue(cfg.Cap.Browser))
	require.Equal(t, DefaultConfig.Log.Level, cfg.Log.Level)
}

func TestLoad_DefaultsOmittedMainAPIToSelectedProvider(t *testing.T) {
	clearEnvKeys(t, "MORPH_MODEL_API", "MORPH_MODEL_PROVIDER", "MORPH_MODEL_BASE_URL")

	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(envPath, nil, 0o600))
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: anthropic-agent
models:
  providers:
    anthropic:
      apiKey: test-key
  main:
    name: claude-sonnet-4-5
    provider: anthropic
    stream: true
rpc:
  address: 127.0.0.1
  port: 50051
log:
  level: info
`), 0o600))

	cfg, err := Load(envPath, configPath)

	require.NoError(t, err)
	require.Equal(t, modelprovider.APIAnthropicMessages, cfg.Models.Main.API)
	require.Equal(t, constants.DefaultAnthropicBaseURL, cfg.Models.Main.BaseURL)
}

func TestLoad_PreservesExplicitMainAPIForValidation(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
models:
  main:
    provider: anthropic
    api: openai-responses
`), 0o600))

	cfg, err := loadConfigFile(configPath)

	require.NoError(t, err)
	require.Equal(t, modelprovider.APIOpenAIResponses, cfg.Models.Main.API)
}

func TestLoadStrictRejectsMissingGatewayCredentials(t *testing.T) {
	clearEnvKeys(t, "OPENROUTER_API_KEY")

	configPath := writeLoadGatewayConfig(t, "")

	_, err := LoadStrict("", configPath)

	require.EqualError(t, err, "gateway telegram bot token is required when telegram gateway is enabled; "+
		"set MORPH_GATEWAY_TELEGRAM_BOT_TOKEN, provide it in config, or use --gateway.telegram.bot-token")
}

func TestLoadRelaxedSkipsMissingGatewayCredentials(t *testing.T) {
	clearEnvKeys(t, "OPENROUTER_API_KEY")

	configPath := writeLoadGatewayConfig(t, "")

	cfg, err := LoadRelaxed("", configPath)

	require.NoError(t, err)
	require.True(t, cfg.Gateway.Telegram.Enabled)
	require.Empty(t, cfg.Gateway.Telegram.BotToken)
	require.Equal(t, "openrouter", cfg.Models.Main.Provider)
	require.Equal(t, "openai/gpt-4o-mini", cfg.Models.Main.Name)
}

func TestLoad_ReturnsErrorForInvalidConfigFile(t *testing.T) {
	clearEnvKeys(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "OPENROUTER_API_KEY", "OPENAI_API_KEY",
		"OPENROUTER_API_KEY", "MORPH_MODEL_BASE_URL", "MORPH_MODEL_API", "MORPH_MODEL_MAX_RETRIES",
		"MORPH_RPC_ADDRESS", "MORPH_RPC_PORT", "MORPH_SESSION_MAX_ITERATIONS", "MORPH_LOG_LEVEL", "MORPH_LOG_NO_COLOR", "MORPH_DEBUG_REQUESTS")

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("name: [\n"), 0o600))

	_, err := Load("", configPath)

	require.Error(t, err)
	require.Contains(t, err.Error(), `failed to parse config file`)
}

func writeLoadGatewayConfig(t *testing.T, telegramBotToken string) string {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: test-agent
models:
  providers:
    openrouter:
      apiKey: router-key
  main:
    provider: openrouter
    name: openai/gpt-4o-mini
gateway:
  enabled: true
  telegram:
    enabled: true
    mode: polling
    botToken: "`+telegramBotToken+`"
search:
  vector:
    enabled: false
`), 0o600))

	return configPath
}

func TestLoadConfigFile_UsesDefaultPathWhenEmpty(t *testing.T) {
	cfg, err := loadConfigFile("")
	require.NoError(t, err)
	require.NotNil(t, cfg)
}

func TestLoadConfigFile_ReturnsReadError(t *testing.T) {
	dir := t.TempDir()

	_, err := loadConfigFile(dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), `failed to read config file`)
}

func TestGet_ReturnsDefaultsWhenConfigIsUnset(t *testing.T) {
	original := Get()
	Set(nil)
	t.Cleanup(func() {
		Set(original)
	})

	cfg := Get()
	require.Equal(t, constants.DefaultName, cfg.Name)
	require.Equal(t, DefaultConfig.Models.Main.Name, cfg.Models.Main.Name)
	require.Equal(t, DefaultConfig.Log.Level, cfg.Log.Level)
	require.False(t, cfg.Log.NoColor)
	require.Equal(t, DefaultConfig.Models.Main.Provider, cfg.Models.Main.Provider)
	require.Equal(t, DefaultConfig.Models.Main.BaseURL, cfg.Models.Main.BaseURL)
	require.Equal(t, constants.DefaultRPCAddress, cfg.RPC.Address)
	require.Equal(t, constants.DefaultRPCPort, cfg.RPC.Port)
	require.Equal(t, DefaultConfig.Session.MaxIterations, cfg.Session.MaxIterations)
	require.Equal(t, DefaultConfig.Platform, cfg.Platform)
	require.True(t, getBoolValue(cfg.Cap.Filesystem))
	require.True(t, getBoolValue(cfg.Cap.Network))
	require.True(t, getBoolValue(cfg.Cap.Exec))
	require.True(t, getBoolValue(cfg.Cap.Memory))
	require.False(t, getBoolValue(cfg.Cap.Browser))
}

func TestSet_StoresConfigGlobally(t *testing.T) {
	original := Get()
	t.Cleanup(func() {
		Set(original)
	})

	cfg := &Config{
		Name: "Test Agent",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openai": {APIKey: "test-key"}},
			Main:      MainModelConfig{Name: "test-model", Provider: "openai"},
		},
		Log: LogConfig{Level: "debug"},
	}
	Set(cfg)
	require.Same(t, cfg, Get())
}
