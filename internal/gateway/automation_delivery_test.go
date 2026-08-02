package gateway

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/automation"
	"github.com/xymorphic/morph/internal/config"
	pkgslack "github.com/xymorphic/morph/pkg/gateway/slack"
	pkgtelegram "github.com/xymorphic/morph/pkg/gateway/telegram"
)

func TestAutomationDeliverySink_RoutesTelegram(t *testing.T) {
	var target pkgtelegram.Target
	var text string
	sink := newAutomationDeliverySink(config.GatewayConfig{
		Enabled:  true,
		Telegram: config.GatewayTelegramConfig{Enabled: true, BotToken: "telegram-token"},
	}, automationDeliverySenders{
		telegram: func(_ context.Context, cfg config.GatewayTelegramConfig, value pkgtelegram.Target, message string) error {
			require.Equal(t, "telegram-token", cfg.BotToken)
			target = value
			text = message
			return nil
		},
	})

	err := sink.DeliverAutomation(context.Background(), automation.DeliveryRequest{
		Output: "daily summary",
		Target: automation.DeliveryTarget{
			Channel:  "telegram",
			Target:   "-100123456",
			ThreadID: "42",
		},
	})

	require.NoError(t, err)
	require.Equal(t, pkgtelegram.Target{ChatID: "-100123456", ThreadID: "42"}, target)
	require.Equal(t, "daily summary", text)
}

func TestAutomationDeliverySink_RoutesSlack(t *testing.T) {
	var target pkgslack.Target
	var text string
	sink := newAutomationDeliverySink(config.GatewayConfig{
		Enabled: true,
		Slack:   config.GatewaySlackConfig{Enabled: true, BotToken: "slack-token"},
	}, automationDeliverySenders{
		slack: func(_ context.Context, cfg config.GatewaySlackConfig, value pkgslack.Target, message string) error {
			require.Equal(t, "slack-token", cfg.BotToken)
			target = value
			text = message
			return nil
		},
	})

	err := sink.DeliverAutomation(context.Background(), automation.DeliveryRequest{
		Output: "daily summary",
		Target: automation.DeliveryTarget{
			Channel:  "slack",
			Target:   "C123",
			ThreadID: "1717618842.000100",
		},
	})

	require.NoError(t, err)
	require.Equal(t, pkgslack.Target{ChannelID: "C123", ThreadTS: "1717618842.000100"}, target)
	require.Equal(t, "daily summary", text)
}

func TestAutomationDeliverySink_FormatsFailureAndValidatesTarget(t *testing.T) {
	var text string
	expected := errors.New("send failed")
	sink := newAutomationDeliverySink(config.GatewayConfig{
		Enabled:  true,
		Telegram: config.GatewayTelegramConfig{Enabled: true},
	}, automationDeliverySenders{
		telegram: func(_ context.Context, _ config.GatewayTelegramConfig, _ pkgtelegram.Target, message string) error {
			text = message
			return expected
		},
	})

	err := sink.DeliverAutomation(context.Background(), automation.DeliveryRequest{
		Output: "partial output",
		Error:  "agent failed",
		Target: automation.DeliveryTarget{Channel: "telegram", Target: "123"},
	})
	require.ErrorIs(t, err, expected)
	require.Equal(t, "partial output\n\nError: agent failed", text)

	sink.senders.telegram = func(_ context.Context, _ config.GatewayTelegramConfig, _ pkgtelegram.Target, message string) error {
		text = message
		return nil
	}
	err = sink.DeliverAutomation(context.Background(), automation.DeliveryRequest{
		Error:  "agent failed",
		Target: automation.DeliveryTarget{Channel: "telegram", Target: "123"},
	})
	require.NoError(t, err)
	require.Equal(t, "Error: agent failed", text)

	tests := []struct {
		name string
		cfg  config.GatewayConfig
		req  automation.DeliveryRequest
		err  string
	}{
		{name: "nil sink", err: "automation gateway delivery sink is required"},
		{name: "disabled gateway", cfg: config.GatewayConfig{}, err: "gateway is disabled"},
		{name: "missing channel", cfg: config.GatewayConfig{Enabled: true}, err: "automation gateway delivery channel is required"},
		{
			name: "missing target",
			cfg:  config.GatewayConfig{Enabled: true, Telegram: config.GatewayTelegramConfig{Enabled: true}},
			req:  automation.DeliveryRequest{Target: automation.DeliveryTarget{Channel: "telegram"}},
			err:  "automation gateway delivery target is required",
		},
		{
			name: "disabled telegram",
			cfg:  config.GatewayConfig{Enabled: true},
			req: automation.DeliveryRequest{
				Output: "message",
				Target: automation.DeliveryTarget{Channel: "telegram", Target: "123"},
			},
			err: "telegram gateway is disabled",
		},
		{
			name: "disabled slack",
			cfg:  config.GatewayConfig{Enabled: true},
			req: automation.DeliveryRequest{
				Output: "message",
				Target: automation.DeliveryTarget{Channel: "slack", Target: "C123"},
			},
			err: "slack gateway is disabled",
		},
		{
			name: "missing message",
			cfg: config.GatewayConfig{
				Enabled:  true,
				Telegram: config.GatewayTelegramConfig{Enabled: true},
			},
			req: automation.DeliveryRequest{
				Target: automation.DeliveryTarget{Channel: "telegram", Target: "123"},
			},
			err: "automation gateway delivery message is required",
		},
		{
			name: "unsupported channel",
			cfg:  config.GatewayConfig{Enabled: true},
			req: automation.DeliveryRequest{
				Output: "message",
				Target: automation.DeliveryTarget{Channel: "email", Target: "ops"},
			},
			err: "unsupported automation gateway delivery channel \"email\"",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var testSink *AutomationDeliverySink
			if test.name == "disabled gateway" {
				testSink = NewAutomationDeliverySink(test.cfg)
			} else if test.name != "nil sink" {
				testSink = newAutomationDeliverySink(test.cfg, automationDeliverySenders{})
			}

			err := testSink.DeliverAutomation(context.Background(), test.req)

			require.EqualError(t, err, test.err)
		})
	}
}
