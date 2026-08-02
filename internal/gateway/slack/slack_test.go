package slack

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/permissions"
	storage "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/pkg/gateway/bindings"
	"github.com/xymorphic/morph/pkg/gateway/pairing"
	pkgslack "github.com/xymorphic/morph/pkg/gateway/slack"
)

func TestAdapter_DispatchesAllowedSenderAndCreatesSessionBinding(t *testing.T) {
	service := newSlackServiceStub()
	api := &fakeSlackAPI{}
	cfg := slackGatewayConfig()
	cfg.AllowedUsers = []string{"U1"}
	inbound := slackInboundMessage()

	handled, err := NewAdapter(cfg, service, api).DispatchInbound(context.Background(), inbound)

	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, 1, service.callCount())
	authorization, ok := permissions.FromContext(service.respondContext)
	require.True(t, ok)
	require.Equal(t, permissions.ActorGatewayUser, authorization.Actor.Kind)
	require.Equal(t, "U1", authorization.Actor.ID)
	require.Equal(t, permissions.SurfaceKindGateway, authorization.SurfaceKind)
	require.Equal(t, permissions.SurfaceSlack, authorization.Surface)
	require.Equal(t, service.createdSession.ID, authorization.SessionID)
	require.Equal(t, "hello", service.lastMessage)
	require.Equal(t, service.createdSession.ID, service.lastOptions.SessionID)
	key, err := bindings.Slack("T1", "D1", "100.1")
	require.NoError(t, err)
	binding, ok := service.binding(key.String())
	require.True(t, ok)
	require.Equal(t, service.createdSession.ID, binding.SessionID)
	session, ok := service.sessions[service.createdSession.ID]
	require.True(t, ok)
	require.Equal(t, storage.SessionOrigin{
		Source:         bindings.SourceSlack,
		AccountID:      "T1",
		ConversationID: "D1",
		ThreadID:       "100.1",
	}, session.Origin)
	require.Equal(t, []slackAPICall{
		{method: "startStream", target: inbound.Target},
		{method: "appendStream", stream: pkgslack.Stream{ChannelID: "D1", TS: "stream-ts"}, text: "stream delta"},
		{method: "stopStream", stream: pkgslack.Stream{ChannelID: "D1", TS: "stream-ts"}},
	}, api.allCalls())
}

func TestAdapter_DispatchesSlackAllowedSender(t *testing.T) {
	service := newSlackServiceStub()
	api := &fakeSlackAPI{}
	cfg := slackGatewayConfig()
	cfg.Slack.AllowedUsers = []string{"U1"}

	handled, err := NewAdapter(cfg, service, api).DispatchInbound(context.Background(), slackInboundMessage())

	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, 1, service.callCount())
}

func TestAdapter_DispatchesResponseAsNewMessageWhenConfigured(t *testing.T) {
	service := newSlackServiceStub()
	api := &fakeSlackAPI{}
	cfg := slackGatewayConfig()
	cfg.Slack.AllowedUsers = []string{"U1"}
	cfg.Slack.ResponseMode = config.GatewaySlackResponseModeMessage
	inbound := slackInboundMessage()
	responseTarget := inbound.Target
	responseTarget.ThreadTS = ""

	handled, err := NewAdapter(cfg, service, api).DispatchInbound(context.Background(), inbound)

	require.NoError(t, err)
	require.True(t, handled)
	key, err := bindings.Slack("T1", "D1", "100.1")
	require.NoError(t, err)
	binding, ok := service.binding(key.String())
	require.True(t, ok)
	require.Equal(t, service.createdSession.ID, binding.SessionID)
	require.Equal(t, []slackAPICall{
		{method: "startStream", target: responseTarget},
		{method: "appendStream", stream: pkgslack.Stream{ChannelID: "D1", TS: "stream-ts"}, text: "stream delta"},
		{method: "stopStream", stream: pkgslack.Stream{ChannelID: "D1", TS: "stream-ts"}},
	}, api.allCalls())
}

func TestAdapter_DispatchesResponseInThreadWhenConfiguredForMessageAndInboundIsThreadReply(t *testing.T) {
	service := newSlackServiceStub()
	api := &fakeSlackAPI{}
	cfg := slackGatewayConfig()
	cfg.Slack.AllowedUsers = []string{"U1"}
	cfg.Slack.ResponseMode = config.GatewaySlackResponseModeMessage
	inbound := slackInboundMessage()
	inbound.ThreadTS = "100.1"
	inbound.MessageTS = "100.2"
	inbound.Target.ThreadTS = "100.1"

	handled, err := NewAdapter(cfg, service, api).DispatchInbound(context.Background(), inbound)

	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, []slackAPICall{
		{method: "startStream", target: inbound.Target},
		{method: "appendStream", stream: pkgslack.Stream{ChannelID: "D1", TS: "stream-ts"}, text: "stream delta"},
		{method: "stopStream", stream: pkgslack.Stream{ChannelID: "D1", TS: "stream-ts"}},
	}, api.allCalls())
}

func TestAdapter_DispatchesApprovedSender(t *testing.T) {
	service := newSlackServiceStub()
	service.approvedSenders[pairingKey(bindings.SourceSlack, "U1")] = approvedSlackSender("U1")
	api := &fakeSlackAPI{}
	cfg := slackGatewayConfig()

	handled, err := NewAdapter(cfg, service, api).DispatchInbound(context.Background(), slackInboundMessage())

	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, 1, service.callCount())
}

func TestAdapter_SendsPairingChallengeForUnknownDirectMessageSender(t *testing.T) {
	service := newSlackServiceStub()
	api := &fakeSlackAPI{}
	cfg := slackGatewayConfig()
	inbound := slackInboundMessage()

	handled, err := NewAdapter(cfg, service, api).DispatchInbound(context.Background(), inbound)

	require.NoError(t, err)
	require.True(t, handled)
	require.Zero(t, service.callCount())
	require.Len(t, service.pendingRequests, 1)
	require.Len(t, api.allCalls(), 1)
	call := api.allCalls()[0]
	require.Equal(t, "postMessage", call.method)
	require.Equal(t, inbound.Target, call.target)
	require.Contains(t, call.text, "pair")
	require.Contains(t, call.text, "morph gateway pairing approve slack")
}

func TestAdapter_SendsPairingChallengeAsNewMessageWhenConfigured(t *testing.T) {
	service := newSlackServiceStub()
	api := &fakeSlackAPI{}
	cfg := slackGatewayConfig()
	cfg.Slack.ResponseMode = config.GatewaySlackResponseModeMessage
	inbound := slackInboundMessage()
	responseTarget := inbound.Target
	responseTarget.ThreadTS = ""

	handled, err := NewAdapter(cfg, service, api).DispatchInbound(context.Background(), inbound)

	require.NoError(t, err)
	require.True(t, handled)
	require.Zero(t, service.callCount())
	require.Len(t, api.allCalls(), 1)
	call := api.allCalls()[0]
	require.Equal(t, "postMessage", call.method)
	require.Equal(t, responseTarget, call.target)
	require.Contains(t, call.text, "morph gateway pairing approve slack")
}

func TestAdapter_SendsPairingChallengeInThreadWhenConfiguredForMessageAndInboundIsThreadReply(t *testing.T) {
	service := newSlackServiceStub()
	api := &fakeSlackAPI{}
	cfg := slackGatewayConfig()
	cfg.Slack.ResponseMode = config.GatewaySlackResponseModeMessage
	inbound := slackInboundMessage()
	inbound.ThreadTS = "100.1"
	inbound.MessageTS = "100.2"
	inbound.Target.ThreadTS = "100.1"

	handled, err := NewAdapter(cfg, service, api).DispatchInbound(context.Background(), inbound)

	require.NoError(t, err)
	require.True(t, handled)
	require.Zero(t, service.callCount())
	require.Len(t, api.allCalls(), 1)
	call := api.allCalls()[0]
	require.Equal(t, "postMessage", call.method)
	require.Equal(t, inbound.Target, call.target)
	require.Contains(t, call.text, "morph gateway pairing approve slack")
}

func TestAdapter_IgnoresUnknownChannelSender(t *testing.T) {
	service := newSlackServiceStub()
	api := &fakeSlackAPI{}
	cfg := slackGatewayConfig()
	inbound := slackInboundMessage()
	inbound.Target.ChannelType = "channel"

	handled, err := NewAdapter(cfg, service, api).DispatchInbound(context.Background(), inbound)

	require.NoError(t, err)
	require.True(t, handled)
	require.Zero(t, service.callCount())
	require.Empty(t, api.allCalls())
}

func TestAdapter_IgnoresInboundWithoutSender(t *testing.T) {
	service := newSlackServiceStub()
	api := &fakeSlackAPI{}
	inbound := slackInboundMessage()
	inbound.SenderID = " "

	handled, err := NewAdapter(slackGatewayConfig(), service, api).DispatchInbound(context.Background(), inbound)

	require.NoError(t, err)
	require.True(t, handled)
	require.Zero(t, service.callCount())
	require.Empty(t, api.allCalls())
}

func TestAdapter_ReturnsPairingErrorWhenSecretMissing(t *testing.T) {
	cfg := slackGatewayConfig()
	cfg.PairingSecret = ""

	handled, err := NewAdapter(cfg, newSlackServiceStub(), &fakeSlackAPI{}).
		DispatchInbound(context.Background(), slackInboundMessage())

	require.ErrorIs(t, err, pairing.ErrSecretRequired)
	require.True(t, handled)
}

func TestAdapter_ReturnsPairingStoreError(t *testing.T) {
	service := newSlackServiceStub()
	service.getPairedErr = errSlackTest

	handled, err := NewAdapter(slackGatewayConfig(), service, &fakeSlackAPI{}).
		DispatchInbound(context.Background(), slackInboundMessage())

	require.ErrorIs(t, err, errSlackTest)
	require.True(t, handled)
}

func TestAdapter_EmptyMessageIsNotHandled(t *testing.T) {
	handled, err := NewAdapter(slackGatewayConfig(), newSlackServiceStub(), &fakeSlackAPI{}).
		DispatchInbound(context.Background(), pkgslack.InboundMessage{Text: " "})

	require.NoError(t, err)
	require.False(t, handled)
}

func TestAdapter_ReturnsErrorWhenBindingCannotBeCreated(t *testing.T) {
	inbound := slackInboundMessage()
	inbound.TeamID = ""
	cfg := slackGatewayConfig()
	cfg.AllowedUsers = []string{"U1"}

	handled, err := NewAdapter(cfg, newSlackServiceStub(), &fakeSlackAPI{}).
		DispatchInbound(context.Background(), inbound)

	require.ErrorContains(t, err, "slack team id is required")
	require.False(t, handled)
}

func TestAdapter_ReturnsSessionResolverError(t *testing.T) {
	service := newSlackServiceStub()
	service.createErr = errSlackTest
	cfg := slackGatewayConfig()
	cfg.AllowedUsers = []string{"U1"}

	handled, err := NewAdapter(cfg, service, &fakeSlackAPI{}).
		DispatchInbound(context.Background(), slackInboundMessage())

	require.ErrorIs(t, err, errSlackTest)
	require.False(t, handled)
}

func TestAdapter_ReturnsStreamingError(t *testing.T) {
	cfg := slackGatewayConfig()
	cfg.AllowedUsers = []string{"U1"}

	handled, err := NewAdapter(cfg, newSlackServiceStub(), &fakeSlackAPI{stopErr: errSlackTest}).
		DispatchInbound(context.Background(), slackInboundMessage())

	require.ErrorIs(t, err, errSlackTest)
	require.True(t, handled)
}

func TestAdapter_ReturnsPairingChallengeDeliveryError(t *testing.T) {
	handled, err := NewAdapter(slackGatewayConfig(), newSlackServiceStub(), &fakeSlackAPI{postErr: errSlackTest}).
		DispatchInbound(context.Background(), slackInboundMessage())

	require.ErrorIs(t, err, errSlackTest)
	require.True(t, handled)
}

func TestAdapter_ReturnsPairingRequestSaveError(t *testing.T) {
	service := newSlackServiceStub()
	service.savePairingErr = errSlackTest

	handled, err := NewAdapter(slackGatewayConfig(), service, &fakeSlackAPI{}).
		DispatchInbound(context.Background(), slackInboundMessage())

	require.ErrorIs(t, err, errSlackTest)
	require.True(t, handled)
}

func TestAdapter_RequiresAdapterDependencies(t *testing.T) {
	handled, err := (*Adapter)(nil).DispatchInbound(context.Background(), slackInboundMessage())

	require.EqualError(t, err, "slack adapter is required")
	require.False(t, handled)
}

func TestHasAllowedSender(t *testing.T) {
	require.True(t, hasAllowedSender([]string{" U1 "}, "U1"))
	require.False(t, hasAllowedSender([]string{"U2"}, "U1"))
	require.False(t, hasAllowedSender([]string{"U1"}, " "))
}

func TestIsSlackPrivateTarget(t *testing.T) {
	require.True(t, isSlackPrivateTarget(pkgslack.Target{ChannelType: "im"}))
	require.True(t, isSlackPrivateTarget(pkgslack.Target{ChannelType: "mpim"}))
	require.False(t, isSlackPrivateTarget(pkgslack.Target{ChannelType: "channel"}))
}

func slackGatewayConfig() config.GatewayConfig {
	return config.GatewayConfig{
		Enabled:       true,
		PairingSecret: strings.Repeat("s", 16),
		Slack: config.GatewaySlackConfig{
			Enabled:       true,
			Mode:          config.GatewaySlackModeHTTP,
			BotToken:      "xoxb-token",
			SigningSecret: "signing-secret",
		},
	}
}

func slackInboundMessage() pkgslack.InboundMessage {
	return pkgslack.InboundMessage{
		EventID:   "Ev1",
		TeamID:    "T1",
		ChannelID: "D1",
		ThreadTS:  "100.1",
		MessageTS: "100.1",
		Text:      "hello",
		SenderID:  "U1",
		Target: pkgslack.Target{
			TeamID:          "T1",
			ChannelID:       "D1",
			ThreadTS:        "100.1",
			UserID:          "U1",
			ChannelType:     "im",
			RecipientUserID: "U1",
			RecipientTeamID: "T1",
		},
	}
}

var _ Service = (*slackServiceStub)(nil)
var _ pairing.Store = (*slackServiceStub)(nil)
