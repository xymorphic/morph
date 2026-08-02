package cli

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	cli "github.com/urfave/cli/v3"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/constants"
	modelprovider "github.com/xymorphic/morph/internal/model/provider"
)

func TestApplyConfigOverrides_AppliesRulesFiles(t *testing.T) {
	cfg := &config.Config{}
	cmd := &cli.Command{Flags: RootFlags(nil, nil)}
	cmd.Action = func(context.Context, *cli.Command) error {
		ApplyConfigOverrides(cmd, cfg)
		return nil
	}

	err := cmd.Run(context.Background(), []string{"morph", "--rules.files", "/tmp/Morph.md, ./custom.md ,/tmp/CLAUDE.md"})

	require.NoError(t, err)
	require.Equal(t, []string{"/tmp/Morph.md", "./custom.md", "/tmp/CLAUDE.md"}, cfg.Rules.Files)
}

func TestApplyConfigOverrides_AppliesInstruct(t *testing.T) {
	cfg := &config.Config{}
	cmd := &cli.Command{Flags: []cli.Flag{RequestInstructFlag()}}
	cmd.Action = func(context.Context, *cli.Command) error {
		ApplyConfigOverrides(cmd, cfg)
		return nil
	}

	err := cmd.Run(context.Background(), []string{"morph", "--instruct", " be terse "})

	require.NoError(t, err)
	require.Equal(t, "be terse", cfg.Session.Instruct)
}

func TestApplyConfigOverrides_NoColorForcesLogNoColor(t *testing.T) {
	cfg := &config.Config{}
	cmd := &cli.Command{Flags: RootFlags(nil, nil)}
	cmd.Action = func(context.Context, *cli.Command) error {
		ApplyConfigOverrides(cmd, cfg)
		return nil
	}

	err := cmd.Run(context.Background(), []string{"morph", "--log.no-color=false", "--no-color"})

	require.NoError(t, err)
	require.True(t, cfg.Log.NoColor)
}

func TestChatFlag_AcceptsLongAndShortForms(t *testing.T) {
	for _, args := range [][]string{
		{"morph", "--chat", "hello"},
		{"morph", "-c", "hello"},
	} {
		var gotChat bool
		var gotArgs []string
		cmd := &cli.Command{
			Flags: []cli.Flag{ChatFlag()},
			Action: func(_ context.Context, cmd *cli.Command) error {
				gotChat = cmd.Bool("chat")
				gotArgs = cmd.Args().Slice()
				return nil
			},
		}

		err := cmd.Run(context.Background(), args)

		require.NoError(t, err)
		require.True(t, gotChat)
		require.Equal(t, []string{"hello"}, gotArgs)
	}
}

func TestApplyConfigOverrides_AppliesPlatformAndCapabilities(t *testing.T) {
	cfg := &config.Config{}
	cmd := &cli.Command{Flags: RootFlags(nil, nil)}
	cmd.Action = func(context.Context, *cli.Command) error {
		ApplyConfigOverrides(cmd, cfg)
		return nil
	}

	err := cmd.Run(context.Background(), []string{"morph", "--platform", "cli", "--cap.fs=false", "--cap.browser"})

	require.NoError(t, err)
	cfg.Normalize()
	require.Equal(t, "cli", cfg.Platform)
	require.False(t, getBoolValue(cfg.Cap.Filesystem))
	require.True(t, getBoolValue(cfg.Cap.Network))
	require.True(t, getBoolValue(cfg.Cap.Exec))
	require.True(t, getBoolValue(cfg.Cap.Memory))
	require.True(t, getBoolValue(cfg.Cap.Browser))
}

func TestApplyConfigOverrides_AppliesModelMaxRetries(t *testing.T) {
	cfg := &config.Config{}
	cmd := &cli.Command{Flags: RootFlags(nil, nil)}
	cmd.Action = func(context.Context, *cli.Command) error {
		ApplyConfigOverrides(cmd, cfg)
		return nil
	}

	err := cmd.Run(context.Background(), []string{"morph", "--model.max-retries", "0"})

	require.NoError(t, err)
	require.Equal(t, 0, cfg.ModelMaxRetriesEffective())
}

func TestApplyConfigOverrides_ProviderSwitchResolvesProviderDefaults(t *testing.T) {
	cfg := &config.Config{
		Models: config.ModelsConfig{
			Main: config.MainModelConfig{
				Provider: constants.ModelProviderOpenRouter,
				API:      modelprovider.APIOpenAIResponses,
				BaseURL:  constants.DefaultOpenRouterResponsesBaseURL,
			},
		},
	}
	cmd := &cli.Command{Flags: RootFlags(nil, nil)}
	cmd.Action = func(context.Context, *cli.Command) error {
		ApplyConfigOverrides(cmd, cfg)
		return nil
	}

	err := cmd.Run(context.Background(), []string{"morph", "--provider", "ollama"})

	require.NoError(t, err)
	cfg.Normalize()
	require.Equal(t, constants.ModelProviderOllama, cfg.Models.Main.Provider)
	require.Equal(t, modelprovider.APIOllamaNative, cfg.Models.Main.API)
	require.Equal(t, constants.DefaultOllamaBaseURL, cfg.Models.Main.BaseURL)
}

func TestApplyConfigOverrides_ProviderSwitchKeepsExplicitAPIAndBaseURL(t *testing.T) {
	cfg := &config.Config{
		Models: config.ModelsConfig{
			Main: config.MainModelConfig{
				Provider: constants.ModelProviderOpenRouter,
				API:      modelprovider.APIOpenAIResponses,
				BaseURL:  constants.DefaultOpenRouterResponsesBaseURL,
			},
		},
	}
	cmd := &cli.Command{Flags: RootFlags(nil, nil)}
	cmd.Action = func(context.Context, *cli.Command) error {
		ApplyConfigOverrides(cmd, cfg)
		return nil
	}

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--provider", "ollama",
		"--model.api", modelprovider.APIOpenAICompletions,
		"--base-url", constants.DefaultOllamaBaseURL + "/v1",
	})

	require.NoError(t, err)
	cfg.Normalize()
	require.Equal(t, constants.ModelProviderOllama, cfg.Models.Main.Provider)
	require.Equal(t, modelprovider.APIOpenAICompletions, cfg.Models.Main.API)
	require.Equal(t, constants.DefaultOllamaBaseURL+"/v1", cfg.Models.Main.BaseURL)
}

func TestApplyConfigOverrides_AppliesLogRotationSettings(t *testing.T) {
	cfg := &config.Config{}
	cmd := &cli.Command{Flags: RootFlags(nil, nil)}
	cmd.Action = func(context.Context, *cli.Command) error {
		ApplyConfigOverrides(cmd, cfg)
		return nil
	}

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--log.max-size-mb", "25",
		"--log.max-backups", "9",
		"--log.max-age-days", "30",
		"--log.compress=false",
	})

	require.NoError(t, err)
	require.Equal(t, 25, cfg.Log.MaxSizeMB)
	require.Equal(t, 9, cfg.Log.MaxBackups)
	require.Equal(t, 30, cfg.Log.MaxAgeDays)
	require.False(t, cfg.Log.Compress)
}

func TestApplyConfigOverrides_AppliesGatewaySettings(t *testing.T) {
	cfg := &config.Config{}
	cmd := &cli.Command{Flags: RootFlags(nil, nil)}
	cmd.Action = func(context.Context, *cli.Command) error {
		ApplyConfigOverrides(cmd, cfg)
		return nil
	}

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--gateway.enabled",
		"--gateway.address", " 127.0.0.2 ",
		"--gateway.port", "7100",
		"--gateway.auth-token", " MORPH_GATEWAY_AUTH_TOKEN ",
		"--gateway.telegram.enabled",
		"--gateway.telegram.mode", " WEBHOOK ",
		"--gateway.telegram.bot-token", " MORPH_GATEWAY_TELEGRAM_BOT_TOKEN ",
		"--gateway.telegram.webhook-secret", " MORPH_GATEWAY_TELEGRAM_WEBHOOK_SECRET ",
		"--gateway.slack.enabled",
		"--gateway.slack.mode", " HTTP ",
		"--gateway.slack.response-mode", " MESSAGE ",
		"--gateway.slack.bot-token", " MORPH_GATEWAY_SLACK_BOT_TOKEN ",
		"--gateway.slack.app-token", " MORPH_GATEWAY_SLACK_APP_TOKEN ",
		"--gateway.slack.signing-secret", " MORPH_GATEWAY_SLACK_SIGNING_SECRET ",
	})

	require.NoError(t, err)
	cfg.Normalize()
	require.True(t, cfg.Gateway.Enabled)
	require.Equal(t, "127.0.0.2", cfg.Gateway.Address)
	require.Equal(t, 7100, cfg.Gateway.Port)
	require.Equal(t, "MORPH_GATEWAY_AUTH_TOKEN", cfg.Gateway.AuthToken)
	require.True(t, cfg.Gateway.Telegram.Enabled)
	require.Equal(t, config.GatewayTelegramModeWebhook, cfg.Gateway.Telegram.Mode)
	require.Equal(t, "MORPH_GATEWAY_TELEGRAM_BOT_TOKEN", cfg.Gateway.Telegram.BotToken)
	require.Equal(t, "MORPH_GATEWAY_TELEGRAM_WEBHOOK_SECRET", cfg.Gateway.Telegram.WebhookSecret)
	require.True(t, cfg.Gateway.Slack.Enabled)
	require.Equal(t, config.GatewaySlackModeHTTP, cfg.Gateway.Slack.Mode)
	require.Equal(t, config.GatewaySlackResponseModeMessage, cfg.Gateway.Slack.ResponseMode)
	require.Equal(t, "MORPH_GATEWAY_SLACK_BOT_TOKEN", cfg.Gateway.Slack.BotToken)
	require.Equal(t, "MORPH_GATEWAY_SLACK_APP_TOKEN", cfg.Gateway.Slack.AppToken)
	require.Equal(t, "MORPH_GATEWAY_SLACK_SIGNING_SECRET", cfg.Gateway.Slack.SigningSecret)
}

func TestApplyConfigOverrides_AppliesModelStream(t *testing.T) {
	cfg := &config.Config{}
	cmd := &cli.Command{Flags: RootFlags(nil, nil)}
	cmd.Action = func(context.Context, *cli.Command) error {
		ApplyConfigOverrides(cmd, cfg)
		return nil
	}

	err := cmd.Run(context.Background(), []string{"morph", "--model.stream=false"})

	require.NoError(t, err)
	cfg.Normalize()
	require.False(t, cfg.StreamEnabled())
}

func TestApplyConfigOverrides_AppliesTUIThinkingComposer(t *testing.T) {
	cfg := &config.Config{}
	cmd := &cli.Command{Flags: RootFlags(nil, nil)}
	cmd.Action = func(context.Context, *cli.Command) error {
		ApplyConfigOverrides(cmd, cfg)
		return nil
	}

	err := cmd.Run(context.Background(), []string{"morph", "--tui.thinking-composer=false"})

	require.NoError(t, err)
	cfg.Normalize()
	require.False(t, cfg.TUIThinkingComposerEnabled())
}

func TestApplyConfigOverrides_AppliesFilesystemRoots(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfg := &config.Config{}
	cmd := &cli.Command{Flags: RootFlags(nil, nil)}
	cmd.Action = func(context.Context, *cli.Command) error {
		ApplyConfigOverrides(cmd, cfg)
		return nil
	}

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--fs.roots", "./workspace,./nested",
	})

	require.NoError(t, err)
	cfg.Normalize()
	require.Equal(t, []string{
		filepath.Join(dir, "workspace"),
		filepath.Join(dir, "nested"),
	}, cfg.FS.Roots)
}

func TestApplyConfigOverrides_RejectsRemovedExecRuleFlags(t *testing.T) {
	cmd := &cli.Command{Flags: RootFlags(nil, nil)}
	cmd.Action = func(context.Context, *cli.Command) error { return nil }

	err := cmd.Run(context.Background(), []string{"morph", "--exec.allow", "git status"})

	require.ErrorContains(t, err, "flag provided but not defined")
}

func TestApplyConfigOverrides_AppliesSessionSettings(t *testing.T) {
	cfg := &config.Config{}
	cmd := &cli.Command{Flags: RootFlags(nil, nil)}
	cmd.Action = func(context.Context, *cli.Command) error {
		ApplyConfigOverrides(cmd, cfg)
		return nil
	}

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--storage.backend", "memory",
		"--memory.backend", "sqlite",
		"--session.default-idle-expiry", "2h",
		"--session.archive-retention", "72h",
	})

	require.NoError(t, err)
	require.Equal(t, "memory", cfg.Storage.Backend)
	require.Equal(t, "sqlite", cfg.Memory.Backend)
	require.Equal(t, 2*time.Hour, cfg.Session.DefaultIdleExpiry)
	require.Equal(t, 72*time.Hour, cfg.Session.ArchiveRetention)
}

func TestApplyConfigOverrides_AppliesWebSettings(t *testing.T) {
	cfg := &config.Config{}
	cmd := &cli.Command{Flags: RootFlags(nil, nil)}
	cmd.Action = func(context.Context, *cli.Command) error {
		ApplyConfigOverrides(cmd, cfg)
		return nil
	}

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--web.provider", " exa ",
		"--web.key", " web-key ",
		"--web.base-url", " https://example.test ",
		"--web.max-char-per-result", "1300",
		"--web.max-extract-char-per-result", "51000",
		"--web.max-extract-response-bytes", "2097152",
		"--web.cache-ttl", "15m",
		"--web.blocked-domains-enabled",
		"--web.blocked-domains", " blocked.example , ads.example ",
		"--web.blocked-domain-files", " blocked.txt , shared.txt ",
		"--web.native-allowed-hosts", " allowed.example , docs.example ",
		"--web.native-blocked-hosts", " blocked.example , raw.example ",
		"--web.native-allowed-host-files", " allow.txt , safe.txt ",
		"--web.native-blocked-host-files", " deny.txt , banned.txt ",
		"--web.extract-min-summarize-chars", "12000",
		"--web.extract-max-summary-chars", "3000",
		"--web.extract-max-summary-chunk-chars", "70000",
		"--web.extract-refusal-threshold-chars", "190000",
	})

	require.NoError(t, err)
	cfg.Normalize()
	require.Equal(t, "exa", cfg.Web.Provider)
	require.Equal(t, "web-key", cfg.Web.APIKey)
	require.Equal(t, "https://example.test", cfg.Web.BaseURL)
	require.Equal(t, 1300, cfg.Web.MaxCharPerResult)
	require.Equal(t, 51000, cfg.Web.MaxExtractCharPerResult)
	require.Equal(t, 2097152, cfg.Web.MaxExtractResponseBytes)
	require.Equal(t, 15*time.Minute, cfg.Web.CacheTTL)
	require.True(t, cfg.Web.BlockedDomainsEnabled)
	require.Equal(t, []string{"blocked.example", "ads.example"}, cfg.Web.BlockedDomains)
	require.Equal(t, []string{"blocked.txt", "shared.txt"}, cfg.Web.BlockedDomainFiles)
	require.Equal(t, []string{"allowed.example", "docs.example"}, cfg.Web.NativeAllowedHosts)
	require.Equal(t, []string{"blocked.example", "raw.example"}, cfg.Web.NativeBlockedHosts)
	require.Equal(t, []string{"allow.txt", "safe.txt"}, cfg.Web.NativeAllowedHostFiles)
	require.Equal(t, []string{"deny.txt", "banned.txt"}, cfg.Web.NativeBlockedHostFiles)
	require.Equal(t, 12000, cfg.Web.ExtractMinSummarizeChars)
	require.Equal(t, 3000, cfg.Web.ExtractMaxSummaryChars)
	require.Equal(t, 70000, cfg.Web.ExtractMaxSummaryChunkChars)
	require.Equal(t, 190000, cfg.Web.ExtractRefusalThresholdChars)
}
