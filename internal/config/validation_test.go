package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xymorphic/morph/internal/constants"
	modelprovider "github.com/xymorphic/morph/internal/model/provider"
)

func TestConfig_ValidateRequiresProvider(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")

	cfg := &Config{
		Name:   "test-agent",
		Models: ModelsConfig{Main: MainModelConfig{Name: constants.DefaultModel}},
		Log:    LogConfig{Level: "info"},
	}
	require.EqualError(t, cfg.Validate(), "model provider is required")
	require.Empty(t, cfg.Models.Main.Provider)
	require.Empty(t, cfg.Models.Main.BaseURL)
}

func TestConfig_ValidateNilConfig(t *testing.T) {
	var cfg *Config
	require.EqualError(t, cfg.Validate(), "config is required")
}

func TestConfig_ValidateRelaxedAllowsMissingModelSelection(t *testing.T) {
	cfg := &Config{
		Name: "test-agent",
		Log:  LogConfig{Level: "info"},
	}

	require.NoError(t, cfg.ValidateRelaxed())
	require.Empty(t, cfg.Models.Main.Name)
	require.Empty(t, cfg.Models.Main.Provider)
}

func TestConfig_ValidateRelaxedAllowsMissingGatewayCredentials(t *testing.T) {
	cfg := validGatewayConfig()
	cfg.Gateway.Telegram.BotToken = ""
	cfg.Gateway.Slack.BotToken = ""
	cfg.Gateway.Slack.AppToken = ""

	require.NoError(t, cfg.ValidateRelaxed())
	require.EqualError(t, cfg.Validate(), "gateway telegram bot token is required when telegram gateway is enabled; "+
		"set MORPH_GATEWAY_TELEGRAM_BOT_TOKEN, provide it in config, or use --gateway.telegram.bot-token")
}

func TestConfig_ValidateGatewayChecksOnlyGatewaySettings(t *testing.T) {
	cfg := validGatewayConfig()
	cfg.Models.Main.Name = ""
	cfg.Models.Main.Provider = ""

	require.NoError(t, cfg.ValidateGateway())

	cfg.Gateway.Telegram.BotToken = ""
	require.EqualError(t, cfg.ValidateGateway(), "gateway telegram bot token is required when telegram gateway is enabled; "+
		"set MORPH_GATEWAY_TELEGRAM_BOT_TOKEN, provide it in config, or use --gateway.telegram.bot-token")
}

func TestConfig_ValidateGatewayRejectsNilConfig(t *testing.T) {
	var cfg *Config

	require.EqualError(t, cfg.ValidateGateway(), "config is required")
}

func TestConfig_ValidateRelaxedRejectsInvalidGatewayResponseMode(t *testing.T) {
	cfg := validGatewayConfig()
	cfg.Gateway.Slack.ResponseMode = "ephemeral"

	require.EqualError(t, cfg.ValidateRelaxed(), "gateway slack response mode must be one of: thread, message")
}

func TestConfig_ValidateRelaxedAllowsUnsupportedModelProvider(t *testing.T) {
	cfg := &Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Main: MainModelConfig{
				Name:     constants.DefaultModel,
				Provider: "unsupported",
				BaseURL:  "https://config.example/v1",
			},
		},
		Log: LogConfig{Level: "info"},
	}

	require.NoError(t, cfg.ValidateRelaxed())
	require.EqualError(t, cfg.Validate(), "model provider must be one of: anthropic, github-copilot, ollama, openai, openai-codex, openrouter")
}

func TestConfig_ValidateAllowsProviderSpecificAuthWithoutModelKey(t *testing.T) {
	cfg := &Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openrouter": {APIKey: "openrouter-key"}},
			Main:      MainModelConfig{Name: constants.DefaultModel, Provider: "openrouter"},
		},
		Log: LogConfig{Level: "info"},
	}

	require.NoError(t, cfg.Validate())
}

func TestConfig_ValidateNormalizesFields(t *testing.T) {
	cfg := &Config{
		Name: "  Test Agent  ",
		Models: ModelsConfig{
			Main: MainModelConfig{Name: "  openai/test-model  ", Provider: " OpenRouter ", APIKey: "  test-key  "},
		},
		Log:      LogConfig{Level: " WARN "},
		Platform: " CLI ",
	}

	require.NoError(t, cfg.Validate())
	require.Equal(t, "Test Agent", cfg.Name)
	require.Equal(t, "openai/test-model", cfg.Models.Main.Name)
	require.Equal(t, "openrouter", cfg.Models.Main.Provider)
	require.Equal(t, "test-key", cfg.Models.Main.APIKey)
	require.Equal(t, getDefaultBaseURLForProvider("openrouter", modelprovider.APIOpenAIResponses), cfg.Models.Main.BaseURL)
	require.Equal(t, "warn", cfg.Log.Level)
	require.Equal(t, "cli", cfg.Platform)
}

func TestConfig_ValidateRejectsUnsupportedPlatform(t *testing.T) {
	cfg := &Config{Name: "test-agent", Platform: "desktop"}

	require.EqualError(t, cfg.ValidateRelaxed(), "platform must be cli")
}

func TestConfig_ValidateAppliesGatewayDefaults(t *testing.T) {
	cfg := &Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openrouter": {APIKey: "test-key"}},
			Main:      MainModelConfig{Name: constants.DefaultModel, Provider: "openrouter"},
		},
		Log: LogConfig{Level: "info"},
	}

	require.NoError(t, cfg.Validate())
	require.False(t, cfg.Gateway.Enabled)
	require.Equal(t, constants.DefaultRPCAddress, cfg.Gateway.Address)
	require.Equal(t, constants.DefaultGatewayPort, cfg.Gateway.Port)
	require.Equal(t, GatewayTelegramModePolling, cfg.Gateway.Telegram.Mode)
	require.Equal(t, GatewaySlackModeSocket, cfg.Gateway.Slack.Mode)
	require.Equal(t, GatewaySlackResponseModeThread, cfg.Gateway.Slack.ResponseMode)
}

func TestConfig_ValidateRejectsGatewayNonLoopbackWithoutAuthToken(t *testing.T) {
	cfg := validGatewayConfig()
	cfg.Gateway.Address = "0.0.0.0"
	cfg.Gateway.AuthToken = ""

	err := cfg.Validate()

	require.EqualError(t, err, "gateway auth token is required for non-loopback binds; set "+
		"MORPH_GATEWAY_AUTH_TOKEN, provide it in config, or use --gateway.auth-token")
}

func TestConfig_ValidateAcceptsGatewayNonLoopbackWithAuthToken(t *testing.T) {
	cfg := validGatewayConfig()
	cfg.Gateway.Address = "::"

	require.NoError(t, cfg.Validate())
}

func TestConfig_ValidateRejectsNegativeGatewayPort(t *testing.T) {
	cfg := validGatewayConfig()
	cfg.Gateway.Port = -1

	require.EqualError(t, cfg.Validate(), "gateway port must be non-negative; set MORPH_GATEWAY_PORT, provide it in config, "+
		"or use --gateway.port")
}

func TestConfig_ValidateGatewaySettingsRejectsEmptyAddress(t *testing.T) {
	cfg := validGatewayConfig()
	cfg.Gateway.Address = ""

	require.EqualError(t, cfg.validateGatewaySettings(), "gateway address is required; set MORPH_GATEWAY_ADDRESS, "+
		"provide it in config, or use --gateway.address")
}

func TestConfig_ValidateRejectsInvalidGatewayModes(t *testing.T) {
	cfg := validGatewayConfig()
	cfg.Gateway.Telegram.Mode = "long-polling"

	require.EqualError(t, cfg.Validate(), "gateway telegram mode must be one of: polling, webhook")

	cfg = validGatewayConfig()
	cfg.Gateway.Slack.Mode = "events"

	require.EqualError(t, cfg.Validate(), "gateway slack mode must be one of: socket, http")

	cfg = validGatewayConfig()
	cfg.Gateway.Slack.ResponseMode = "ephemeral"

	require.EqualError(t, cfg.Validate(), "gateway slack response mode must be one of: thread, message")
}

func TestConfig_ValidateRejectsMissingGatewayChannelSecrets(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Config)
		want string
	}{
		{
			name: "telegram bot token",
			edit: func(cfg *Config) {
				cfg.Gateway.Telegram.BotToken = ""
			},
			want: "gateway telegram bot token is required when telegram gateway is enabled; " +
				"set MORPH_GATEWAY_TELEGRAM_BOT_TOKEN, provide it in config, or use --gateway.telegram.bot-token",
		},
		{
			name: "telegram webhook secret",
			edit: func(cfg *Config) {
				cfg.Gateway.Telegram.Mode = GatewayTelegramModeWebhook
				cfg.Gateway.Telegram.WebhookSecret = ""
			},
			want: "gateway telegram webhook secret is required in webhook mode; " +
				"set MORPH_GATEWAY_TELEGRAM_WEBHOOK_SECRET, provide it in config, or use --gateway.telegram.webhook-secret",
		},
		{
			name: "slack bot token",
			edit: func(cfg *Config) {
				cfg.Gateway.Slack.BotToken = ""
			},
			want: "gateway slack bot token is required when slack gateway is enabled; " +
				"set MORPH_GATEWAY_SLACK_BOT_TOKEN, provide it in config, or use --gateway.slack.bot-token",
		},
		{
			name: "slack app token",
			edit: func(cfg *Config) {
				cfg.Gateway.Slack.AppToken = ""
			},
			want: "gateway slack app token is required in socket mode; " +
				"set MORPH_GATEWAY_SLACK_APP_TOKEN, provide it in config, or use --gateway.slack.app-token",
		},
		{
			name: "slack signing secret",
			edit: func(cfg *Config) {
				cfg.Gateway.Slack.Mode = GatewaySlackModeHTTP
				cfg.Gateway.Slack.SigningSecret = ""
			},
			want: "gateway slack signing secret is required in http mode; " +
				"set MORPH_GATEWAY_SLACK_SIGNING_SECRET, provide it in config, or use --gateway.slack.signing-secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validGatewayConfig()
			tt.edit(cfg)

			require.EqualError(t, cfg.Validate(), tt.want)
		})
	}
}

func TestConfig_ValidateSkipsDisabledGatewayChannels(t *testing.T) {
	cfg := validGatewayConfig()
	cfg.Gateway.Telegram.Enabled = false
	cfg.Gateway.Telegram.BotToken = ""
	cfg.Gateway.Telegram.WebhookSecret = ""
	cfg.Gateway.Slack.Enabled = false
	cfg.Gateway.Slack.BotToken = ""
	cfg.Gateway.Slack.AppToken = ""
	cfg.Gateway.Slack.SigningSecret = ""

	require.NoError(t, cfg.Validate())
}

func TestConfig_ValidateAcceptsGatewayWebhookAndSlackHTTPSecrets(t *testing.T) {
	cfg := validGatewayConfig()
	cfg.Gateway.Telegram.Mode = GatewayTelegramModeWebhook
	cfg.Gateway.Slack.Mode = GatewaySlackModeHTTP

	require.NoError(t, cfg.Validate())
}

func TestConfig_ValidateTelegramWebhookSecretFormat(t *testing.T) {
	cfg := validGatewayConfig()
	cfg.Gateway.Telegram.Mode = GatewayTelegramModeWebhook
	cfg.Gateway.Telegram.WebhookSecret = "AZaz09_-"

	require.NoError(t, cfg.Validate())

	for _, tt := range []struct {
		name   string
		secret string
	}{
		{name: "too long", secret: strings.Repeat("a", 257)},
		{name: "space", secret: "abc def"},
		{name: "symbol", secret: "abc.def"},
		{name: "unicode", secret: "abcé"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validGatewayConfig()
			cfg.Gateway.Telegram.Mode = GatewayTelegramModeWebhook
			cfg.Gateway.Telegram.WebhookSecret = tt.secret

			require.EqualError(t, cfg.Validate(), "gateway telegram webhook secret must be 1-256 characters and contain only "+
				"A-Z, a-z, 0-9, underscore, or hyphen")
		})
	}
}

func TestIsLoopbackGatewayAddress(t *testing.T) {
	require.True(t, isLoopbackGatewayAddress(""))
	require.True(t, isLoopbackGatewayAddress("localhost"))
	require.True(t, isLoopbackGatewayAddress("[::1]"))
	require.False(t, isLoopbackGatewayAddress("0.0.0.0"))
	require.False(t, isLoopbackGatewayAddress("gateway.example"))
}

func TestConfig_ValidateDefaultsName(t *testing.T) {
	cfg := &Config{
		Models: ModelsConfig{Main: MainModelConfig{APIKey: "test-key", Name: constants.DefaultModel}},
		Log:    LogConfig{Level: "info"},
	}

	require.NoError(t, cfg.ValidateRelaxed())
	require.Equal(t, constants.DefaultName, cfg.Name)
}

func TestConfig_ValidateAcceptsProviderNativeSummaryModelID(t *testing.T) {
	err := (&Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openai": {APIKey: "test-key"}},
			Main:      MainModelConfig{Name: constants.DefaultModel, Provider: "openai"},
			Summary:   SummaryModelConfig{Name: "gpt-4o-mini"},
		},
		RPC: RPCConfig{Address: "127.0.0.1", Port: 50051},
		Log: LogConfig{Level: "info"},
	}).Validate()

	require.NoError(t, err)
}

func TestConfig_ValidateRejectsInvalidSummaryProvider(t *testing.T) {
	err := (&Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openrouter": {APIKey: "test-key"}},
			Main:      MainModelConfig{Name: constants.DefaultModel, Provider: "openrouter"},
			Summary:   SummaryModelConfig{Provider: "missing"},
		},
		RPC: RPCConfig{Address: "127.0.0.1", Port: 50051},
		Log: LogConfig{Level: "info"},
	}).Validate()

	require.EqualError(t, err, "summary model provider must be one of: anthropic, github-copilot, ollama, openai, openai-codex, openrouter")
}

func TestConfig_ValidateRejectsNegativeModelMaxRetries(t *testing.T) {
	retries := -1
	cfg := &Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Providers:  map[string]ProviderModelConfig{"openai": {APIKey: "test-key"}},
			MaxRetries: &retries,
			Main:       MainModelConfig{Name: constants.DefaultModel, Provider: "openai"},
		},
		RPC:     RPCConfig{Address: "127.0.0.1", Port: 50051},
		Session: SessionConfig{MaxIterations: 1},
		Log:     LogConfig{Level: "info"},
	}

	require.EqualError(t, cfg.Validate(), "model max retries must be greater than or equal to "+
		"zero; use --model.max-retries")
}

func TestConfig_Validate_ReturnsSummaryAuthErrorWhenOpenAIKeyMissing(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	stubModelProviderToken(t, func(string) (StoredModelCredential, error) {
		return StoredModelCredential{}, nil
	})

	err := (&Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openrouter": {APIKey: "router-only"}},
			Main:      MainModelConfig{Name: constants.DefaultModel, Provider: "openrouter"},
			Summary:   SummaryModelConfig{Provider: "openai"},
		},
		RPC: RPCConfig{Address: "127.0.0.1", Port: 50051},
		Log: LogConfig{Level: "info"},
	}).Validate()

	require.ErrorContains(t, err, `model API key is required for provider "openai"`)
}

func validGatewayConfig() *Config {
	return &Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openrouter": {APIKey: "test-key"}},
			Main:      MainModelConfig{Name: constants.DefaultModel, Provider: "openrouter"},
		},
		Gateway: GatewayConfig{
			Enabled:   true,
			Address:   constants.DefaultRPCAddress,
			Port:      constants.DefaultGatewayPort,
			AuthToken: "MORPH_GATEWAY_AUTH_TOKEN",
			Telegram: GatewayTelegramConfig{
				Enabled:       true,
				Mode:          GatewayTelegramModePolling,
				BotToken:      "MORPH_GATEWAY_TELEGRAM_BOT_TOKEN",
				WebhookSecret: "MORPH_GATEWAY_TELEGRAM_WEBHOOK_SECRET",
			},
			Slack: GatewaySlackConfig{
				Enabled:       true,
				Mode:          GatewaySlackModeSocket,
				ResponseMode:  GatewaySlackResponseModeThread,
				BotToken:      "MORPH_GATEWAY_SLACK_BOT_TOKEN",
				AppToken:      "MORPH_GATEWAY_SLACK_APP_TOKEN",
				SigningSecret: "MORPH_GATEWAY_SLACK_SIGNING_SECRET",
			},
		},
		Log: LogConfig{Level: "info"},
	}
}
