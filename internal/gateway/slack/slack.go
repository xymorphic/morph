package slack

import (
	"context"
	"errors"

	"github.com/xymorphic/morph/pkg/logutils"
	"github.com/xymorphic/morph/pkg/str"

	"github.com/xymorphic/morph/internal/config"
	gatewaysession "github.com/xymorphic/morph/internal/gateway/session"
	"github.com/xymorphic/morph/internal/permissions"
	agentcore "github.com/xymorphic/morph/pkg/agent"
	"github.com/xymorphic/morph/pkg/gateway/bindings"
	"github.com/xymorphic/morph/pkg/gateway/pairing"
	slack "github.com/xymorphic/morph/pkg/gateway/slack"
)

var log = logutils.Module("gateway.slack")

type Service interface {
	gatewaysession.Service
	pairing.Store
}

type Adapter struct {
	cfg     config.GatewayConfig
	service Service
	sender  *Sender
}

func NewAdapter(cfg config.GatewayConfig, service Service, api API) *Adapter {
	return &Adapter{
		cfg:     cfg,
		service: service,
		sender:  NewSender(api),
	}
}

func (a *Adapter) DispatchInbound(ctx context.Context, inbound slack.InboundMessage) (bool, error) {
	if a == nil || a.service == nil || a.sender == nil {
		return false, errors.New("slack adapter is required")
	}

	textValue := str.String(inbound.Text)
	if textValue.Trim() == "" {
		return false, nil
	}
	log.Debug().
		Str("slack_event_id", inbound.EventID).
		Str("slack_channel_id", inbound.ChannelID).
		Str("slack_sender_id", inbound.SenderID).
		Str("slack_channel_type", inbound.Target.ChannelType).
		Msg("Slack inbound message dispatch started")

	authorized, err := a.authorize(ctx, inbound)
	if err != nil || !authorized {
		return true, err
	}

	key, err := bindings.Slack(inbound.TeamID, inbound.ChannelID, inbound.ThreadTS)
	if err != nil {
		return false, err
	}
	session, err := gatewaysession.NewResolver(a.service).Resolve(ctx, key)
	if err != nil {
		return false, err
	}
	ctx = permissions.WithContext(ctx, permissions.AuthorizationContext{
		Actor:     permissions.Actor{Kind: permissions.ActorGatewayUser, ID: inbound.SenderID},
		Surface:   permissions.SurfaceSlack,
		SessionID: session.ID,
	})

	responseTarget := getSlackResponseTarget(a.cfg.Slack.ResponseMode, inbound)
	err = a.sender.StreamTurn(ctx, responseTarget, func(onDelta func(string)) (string, error) {
		return a.service.Respond(ctx, inbound.Text, agentcore.RespondOptions{
			SessionID: session.ID,
			OnEvent: func(event agentcore.Event) {
				if event.Kind == agentcore.EventKindTextDelta && event.Channel == "assistant" {
					onDelta(event.Text)
				}
			},
		})
	})
	if err != nil {
		log.Warn().Err(err).Msg("Slack gateway dispatch failed")
		return true, err
	}

	return true, nil
}

func (a *Adapter) authorize(ctx context.Context, inbound slack.InboundMessage) (bool, error) {
	senderIDValue := str.String(inbound.SenderID)
	senderID := senderIDValue.Trim()
	if senderID == "" {
		return false, nil
	}
	if hasAllowedSender(a.cfg.AllowedUsers, senderID) || hasAllowedSender(a.cfg.Slack.AllowedUsers, senderID) {
		return true, nil
	}
	pairingSecretValue := str.String(a.cfg.PairingSecret)
	manager := pairing.NewManager(pairing.Options{
		Store:  a.service,
		Secret: pairingSecretValue.Trim(),
	})
	approved, err := manager.IsApproved(ctx, bindings.SourceSlack, senderID)
	if err != nil {
		return false, err
	}
	if approved {
		return true, nil
	}
	if !isSlackPrivateTarget(inbound.Target) {
		log.Debug().
			Str("slack_sender_id", senderID).
			Str("slack_channel_type", inbound.Target.ChannelType).
			Msg("Slack sender ignored because it is not paired or allowlisted")
		return false, nil
	}

	challenge, err := manager.Request(ctx, pairing.Identity{
		Source:   bindings.SourceSlack,
		SenderID: senderID,
		Metadata: map[string]string{
			"team_id":    inbound.TeamID,
			"channel_id": inbound.ChannelID,
		},
	})
	if err != nil {
		return false, err
	}
	responseTarget := getSlackResponseTarget(a.cfg.Slack.ResponseMode, inbound)
	if err := a.sender.SendFinal(ctx, responseTarget, pairing.ChallengeMessage(challenge)); err != nil {
		return false, err
	}
	log.Debug().
		Str("slack_sender_id", senderID).
		Msg("Slack pairing challenge sent")

	return false, nil
}

func getSlackResponseTarget(responseMode string, inbound slack.InboundMessage) slack.Target {
	target := inbound.Target
	responseModeValue := str.String(responseMode)
	if responseModeValue.Normalized() == config.GatewaySlackResponseModeMessage &&
		!isSlackThreadReply(inbound) {
		target.ThreadTS = ""
	}

	return target
}

func isSlackThreadReply(inbound slack.InboundMessage) bool {
	threadTSValue := str.String(inbound.ThreadTS)
	threadTS := threadTSValue.Trim()
	messageTSValue := str.String(inbound.MessageTS)
	messageTS := messageTSValue.Trim()
	return threadTS != "" && messageTS != "" && threadTS != messageTS
}

func hasAllowedSender(allowed []string, senderID string) bool {
	senderIDValue2 := str.String(senderID)
	senderID = senderIDValue2.Trim()
	if senderID == "" {
		return false
	}
	for _, allowedID := range allowed {
		allowedIDValue := str.String(allowedID)
		if allowedIDValue.Trim() == senderID {
			return true
		}
	}

	return false
}

func isSlackPrivateTarget(target slack.Target) bool {
	channelTypeValue := str.String(target.ChannelType)
	switch channelTypeValue.Trim() {
	case "im", "mpim":
		return true
	default:
		return false
	}
}
