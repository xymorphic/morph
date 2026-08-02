package readiness

import (
	"fmt"
	"net"
	"strings"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/pkg/str"
)

func buildGatewayGroup(cfg *config.Config) Group {
	if cfg == nil {
		return Group{Name: "gateway", Checks: []Check{check("config", StatusFail, "config is required")}}
	}

	return Group{Name: "gateway", Checks: []Check{
		buildGatewayListenerCheck(cfg.Gateway),
		buildGatewayTelegramCheck(cfg.Gateway),
		buildGatewaySlackCheck(cfg.Gateway),
	}}
}

func buildGatewayListenerCheck(cfg config.GatewayConfig) Check {
	if !cfg.Enabled {
		return check("listener", StatusPass, "disabled")
	}

	addressValue := str.String(cfg.Address)
	address := addressValue.Trim()
	authTokenValue := str.String(cfg.AuthToken)
	if !isReadinessLoopbackGatewayAddress(address) && authTokenValue.Trim() == "" {
		return check(
			"listener",
			StatusWarn,
			fmt.Sprintf("enabled on %s:%d without gateway auth token", address, cfg.Port),
			commandAction("morph config set gateway.authToken <token>", "set gateway auth token for non-loopback binds"),
		)
	}

	auth := "loopback"
	authTokenValue2 := str.String(cfg.AuthToken)
	if authTokenValue2.Trim() != "" {
		auth = "configured"
	}
	return check("listener", StatusPass, fmt.Sprintf("enabled on %s:%d, auth=%s", address, cfg.Port, auth))
}

func buildGatewayTelegramCheck(cfg config.GatewayConfig) Check {
	tg := cfg.Telegram
	if !tg.Enabled {
		return check("telegram", StatusPass, "disabled")
	}
	modeValue := str.String(tg.Mode)
	mode := modeValue.Trim()
	botTokenValue := str.String(tg.BotToken)
	if botTokenValue.Trim() == "" {
		return check(
			"telegram",
			StatusWarn,
			fmt.Sprintf("enabled in %s mode without bot token", mode),
			commandAction(
				"morph config set gateway.telegram.botToken <bot-token>",
				"configure Telegram bot token",
			),
		)
	}
	webhookSecretValue := str.String(tg.WebhookSecret)
	if mode == config.GatewayTelegramModeWebhook && webhookSecretValue.Trim() == "" {
		return check(
			"telegram",
			StatusWarn,
			"enabled in webhook mode without webhook secret",
			commandAction(
				"morph config set gateway.telegram.webhookSecret <secret-token>",
				"configure Telegram webhook secret token",
			),
		)
	}

	return check("telegram", StatusPass, fmt.Sprintf("enabled in %s mode, bot token configured", mode))
}

func buildGatewaySlackCheck(cfg config.GatewayConfig) Check {
	slack := cfg.Slack
	if !slack.Enabled {
		return check("slack", StatusPass, "disabled")
	}
	modeValue2 := str.String(slack.Mode)
	mode := modeValue2.Trim()
	botTokenValue2 := str.String(slack.BotToken)
	if botTokenValue2.Trim() == "" {
		return check(
			"slack",
			StatusWarn,
			fmt.Sprintf("enabled in %s mode without bot token", mode),
			commandAction("morph config set gateway.slack.botToken <bot-token>", "configure Slack bot token"),
		)
	}
	switch mode {
	case config.GatewaySlackModeSocket:
		appTokenValue := str.String(slack.AppToken)
		if appTokenValue.Trim() == "" {
			return check(
				"slack",
				StatusWarn,
				"enabled in socket mode without app token",
				commandAction("morph config set gateway.slack.appToken <app-token>", "configure Slack app token"),
			)
		}
	case config.GatewaySlackModeHTTP:
		signingSecretValue := str.String(slack.SigningSecret)
		if signingSecretValue.Trim() == "" {
			return check(
				"slack",
				StatusWarn,
				"enabled in http mode without signing secret",
				commandAction(
					"morph config set gateway.slack.signingSecret <signing-secret>",
					"configure Slack signing secret",
				),
			)
		}
	}

	return check("slack", StatusPass, fmt.Sprintf("enabled in %s mode, bot token configured", mode))
}

func isReadinessLoopbackGatewayAddress(address string) bool {
	trimValue := str.String(strings.Trim(address, "[]"))
	address = trimValue.Trim()
	if address == "" || strings.EqualFold(address, "localhost") {
		return true
	}

	ip := net.ParseIP(address)
	return ip != nil && ip.IsLoopback()
}
