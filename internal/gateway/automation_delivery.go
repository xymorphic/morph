package gateway

import (
	"context"
	"errors"
	"fmt"

	"github.com/xymorphic/morph/internal/automation"
	"github.com/xymorphic/morph/internal/config"
	slackprovider "github.com/xymorphic/morph/internal/gateway/slack"
	telegramprovider "github.com/xymorphic/morph/internal/gateway/telegram"
	pkgslack "github.com/xymorphic/morph/pkg/gateway/slack"
	pkgtelegram "github.com/xymorphic/morph/pkg/gateway/telegram"
	"github.com/xymorphic/morph/pkg/str"
)

type automationDeliverySenders struct {
	telegram func(context.Context, config.GatewayTelegramConfig, pkgtelegram.Target, string) error
	slack    func(context.Context, config.GatewaySlackConfig, pkgslack.Target, string) error
}

type AutomationDeliverySink struct {
	cfg     config.GatewayConfig
	senders automationDeliverySenders
}

func NewAutomationDeliverySink(cfg config.GatewayConfig) *AutomationDeliverySink {
	return newAutomationDeliverySink(cfg, automationDeliverySenders{})
}

func newAutomationDeliverySink(cfg config.GatewayConfig, senders automationDeliverySenders) *AutomationDeliverySink {
	if senders.telegram == nil {
		senders.telegram = telegramprovider.SendFinal
	}
	if senders.slack == nil {
		senders.slack = slackprovider.SendFinal
	}

	return &AutomationDeliverySink{cfg: cfg, senders: senders}
}

func (s *AutomationDeliverySink) DeliverAutomation(ctx context.Context, req automation.DeliveryRequest) error {
	if s == nil {
		return errors.New("automation gateway delivery sink is required")
	}
	if !s.cfg.Enabled {
		return errors.New("gateway is disabled")
	}

	channelValue := str.String(req.Target.Channel)
	channel := channelValue.Normalized()
	if channel == "" {
		return errors.New("automation gateway delivery channel is required")
	}
	targetValue := str.String(req.Target.Target)
	target := targetValue.Trim()
	if target == "" {
		return errors.New("automation gateway delivery target is required")
	}
	threadIDValue := str.String(req.Target.ThreadID)
	threadID := threadIDValue.Trim()
	text := getAutomationDeliveryText(req)
	if text == "" {
		return errors.New("automation gateway delivery message is required")
	}

	switch channel {
	case "telegram":
		if !s.cfg.Telegram.Enabled {
			return errors.New("telegram gateway is disabled")
		}
		return s.senders.telegram(ctx, s.cfg.Telegram, pkgtelegram.Target{
			ChatID:   target,
			ThreadID: threadID,
		}, text)
	case "slack":
		if !s.cfg.Slack.Enabled {
			return errors.New("slack gateway is disabled")
		}
		return s.senders.slack(ctx, s.cfg.Slack, pkgslack.Target{
			ChannelID: target,
			ThreadTS:  threadID,
		}, text)
	default:
		return fmt.Errorf("unsupported automation gateway delivery channel %q", channel)
	}
}

func getAutomationDeliveryText(req automation.DeliveryRequest) string {
	outputValue := str.String(req.Output)
	output := outputValue.Trim()
	errorValue := str.String(req.Error)
	errorText := errorValue.Trim()
	if errorText == "" {
		return output
	}
	if output == "" {
		return "Error: " + errorText
	}

	return output + "\n\nError: " + errorText
}
