package telegram

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/permissions"
	storage "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/pkg/gateway/pairing"
	tg "github.com/xymorphic/morph/pkg/gateway/telegram"
)

func TestTelegramAdapter_DispatchUpdateResolvesSessionAndStreamsReply(t *testing.T) {
	setTelegramDraftID(t, 77)
	api := &fakeTelegramAPI{}
	responder := &genericResponderStub{
		createdSession: storage.Session{ID: genericCreatedSessionID},
		reply:          "final reply",
	}
	adapter := newTelegramAdapter(telegramAdapterConfig(), responder, api)

	handled, err := adapter.DispatchUpdate(context.Background(), tg.Update{
		UpdateID: 7,
		Message: &tg.Message{
			MessageID: 11,
			Text:      " hello telegram ",
			Chat:      tg.Chat{ID: 123, Type: "private"},
			From:      &tg.User{ID: 9},
		},
	})

	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, "hello telegram", responder.message)
	authorization, ok := permissions.FromContext(responder.respondContext)
	require.True(t, ok)
	require.Equal(t, permissions.ActorGatewayUser, authorization.Actor.Kind)
	require.Equal(t, "9", authorization.Actor.ID)
	require.Equal(t, permissions.SurfaceKindGateway, authorization.SurfaceKind)
	require.Equal(t, permissions.SurfaceTelegram, authorization.Surface)
	require.Equal(t, genericCreatedSessionID, authorization.SessionID)
	require.Equal(t, genericCreatedSessionID, responder.options.SessionID)
	require.NotNil(t, responder.options.OnEvent)
	require.Equal(t, storage.GatewayBinding{
		Key:       "telegram::123:",
		SessionID: genericCreatedSessionID,
		CreatedAt: responder.savedBinding.CreatedAt,
		UpdatedAt: responder.savedBinding.UpdatedAt,
	}, responder.savedBinding)
	require.Equal(t, []telegramAPICall{
		{method: "sendChatAction", target: tg.Target{ChatID: "123", ReplyToMessageID: 11, ChatType: "private"}, action: "typing"},
		{method: "sendMessageDraft", target: tg.Target{ChatID: "123", ReplyToMessageID: 11, ChatType: "private"}, draftID: 77, text: "stream\n..."},
		{method: "sendMessageDraft", target: tg.Target{ChatID: "123", ReplyToMessageID: 11, ChatType: "private"}, draftID: 77, text: "stream delta\n..."},
		{method: "sendMessage", target: tg.Target{ChatID: "123", ReplyToMessageID: 11, ChatType: "private"}, text: "final reply"},
	}, api.allCalls())
}

func TestTelegramAdapter_IgnoresUnsupportedUpdateWithoutCallingAgent(t *testing.T) {
	responder := &genericResponderStub{}
	adapter := newTelegramAdapter(telegramAdapterConfig(), responder, &fakeTelegramAPI{})

	handled, err := adapter.DispatchUpdate(context.Background(), tg.Update{
		UpdateID:      8,
		EditedMessage: &tg.Message{Text: "edited", Chat: tg.Chat{ID: 123}},
	})

	require.NoError(t, err)
	require.False(t, handled)
	require.False(t, responder.called)
}

func TestTelegramAdapter_RejectsMissingDependencies(t *testing.T) {
	_, err := (*TelegramAdapter)(nil).DispatchUpdate(context.Background(), tg.Update{})
	require.EqualError(t, err, "telegram adapter is required")

	_, err = newTelegramAdapter(telegramAdapterConfig(), nil, &fakeTelegramAPI{}).DispatchUpdate(context.Background(), tg.Update{})
	require.EqualError(t, err, "telegram adapter is required")
}

func TestTelegramAdapter_ReturnsSessionResolutionError(t *testing.T) {
	responder := &genericResponderStub{getBindingErr: errTelegramTest}
	adapter := newTelegramAdapter(telegramAdapterConfig(), responder, &fakeTelegramAPI{})

	handled, err := adapter.DispatchUpdate(context.Background(), tg.Update{
		UpdateID: 7,
		Message: &tg.Message{
			MessageID: 11,
			Text:      "hello",
			Chat:      tg.Chat{ID: 123, Type: "private"},
			From:      &tg.User{ID: 9},
		},
	})

	require.ErrorIs(t, err, errTelegramTest)
	require.False(t, handled)
	require.False(t, responder.called)
}

func TestTelegramAdapter_ReturnsNormalizationError(t *testing.T) {
	handled, err := newTelegramAdapter(telegramAdapterConfig(), &genericResponderStub{}, &fakeTelegramAPI{}).
		DispatchUpdate(context.Background(), tg.Update{
			UpdateID: 7,
			Message:  &tg.Message{Text: "hello"},
		})

	require.ErrorIs(t, err, tg.ErrTelegramChatRequired)
	require.False(t, handled)
}

func TestTelegramAdapter_ReturnsSafeErrorWhenSenderFails(t *testing.T) {
	api := &fakeTelegramAPI{sendErr: errTelegramTest}
	responder := &genericResponderStub{
		createdSession: storage.Session{ID: genericCreatedSessionID},
		reply:          "final reply",
	}
	adapter := newTelegramAdapter(telegramAdapterConfig(), responder, api)

	handled, err := adapter.DispatchUpdate(context.Background(), tg.Update{
		UpdateID: 7,
		Message: &tg.Message{
			MessageID: 11,
			Text:      "hello",
			Chat:      tg.Chat{ID: -100, Type: "supergroup"},
			From:      &tg.User{ID: 9},
		},
	})

	require.ErrorIs(t, err, errTelegramTest)
	require.True(t, handled)
	require.True(t, responder.called)
}

func TestTelegramAdapter_UnknownPrivateSenderGetsPairingChallenge(t *testing.T) {
	api := &fakeTelegramAPI{}
	responder := &genericResponderStub{}
	adapter := newTelegramAdapter(config.GatewayConfig{PairingSecret: "pair-secret"}, responder, api)

	handled, err := adapter.DispatchUpdate(context.Background(), tg.Update{
		UpdateID: 7,
		Message: &tg.Message{
			MessageID: 11,
			Text:      "hello",
			Chat:      tg.Chat{ID: 123, Type: "private"},
			From:      &tg.User{ID: 9, FirstName: "Ada"},
		},
	})

	require.NoError(t, err)
	require.True(t, handled)
	require.False(t, responder.called)
	require.Len(t, api.callsOfMethod("sendMessage"), 1)
	require.Contains(t, api.callsOfMethod("sendMessage")[0].text, "morph gateway pairing approve telegram")
	requests, err := responder.ListGatewayPairingRequests(context.Background(), "telegram")
	require.NoError(t, err)
	require.Len(t, requests, 1)
	require.Equal(t, "9", requests[0].SenderID)
}

func TestTelegramAdapter_UnknownPrivateSenderRequiresPairingSecret(t *testing.T) {
	api := &fakeTelegramAPI{}
	responder := &genericResponderStub{}
	adapter := newTelegramAdapter(config.GatewayConfig{
		AuthToken: "auth-token",
		Telegram: config.GatewayTelegramConfig{
			BotToken: "bot-token",
		},
	}, responder, api)

	handled, err := adapter.DispatchUpdate(context.Background(), tg.Update{
		UpdateID: 7,
		Message: &tg.Message{
			MessageID: 11,
			Text:      "hello",
			Chat:      tg.Chat{ID: 123, Type: "private"},
			From:      &tg.User{ID: 9},
		},
	})

	require.ErrorIs(t, err, pairing.ErrSecretRequired)
	require.True(t, handled)
	require.False(t, responder.called)
	require.Empty(t, api.allCalls())
}

func TestTelegramAdapter_UnknownPrivateSenderReturnsChallengeSendError(t *testing.T) {
	api := &fakeTelegramAPI{sendErr: errTelegramTest}
	responder := &genericResponderStub{}
	adapter := newTelegramAdapter(config.GatewayConfig{PairingSecret: "pair-secret"}, responder, api)

	handled, err := adapter.DispatchUpdate(context.Background(), tg.Update{
		UpdateID: 7,
		Message: &tg.Message{
			MessageID: 11,
			Text:      "hello",
			Chat:      tg.Chat{ID: 123, Type: "private"},
			From:      &tg.User{ID: 9},
		},
	})

	require.ErrorIs(t, err, errTelegramTest)
	require.True(t, handled)
	require.False(t, responder.called)
}

func TestTelegramAdapter_ReturnsPairingStoreError(t *testing.T) {
	api := &fakeTelegramAPI{}
	responder := &genericResponderStub{pairingErr: errTelegramTest}
	adapter := newTelegramAdapter(config.GatewayConfig{PairingSecret: "pair-secret"}, responder, api)

	handled, err := adapter.DispatchUpdate(context.Background(), tg.Update{
		UpdateID: 7,
		Message: &tg.Message{
			MessageID: 11,
			Text:      "hello",
			Chat:      tg.Chat{ID: 123, Type: "private"},
			From:      &tg.User{ID: 9},
		},
	})

	require.ErrorIs(t, err, errTelegramTest)
	require.True(t, handled)
	require.False(t, responder.called)
	require.Empty(t, api.allCalls())
}

func TestTelegramAdapter_MissingSenderIDIsIgnored(t *testing.T) {
	api := &fakeTelegramAPI{}
	responder := &genericResponderStub{}
	adapter := newTelegramAdapter(config.GatewayConfig{PairingSecret: "pair-secret"}, responder, api)

	handled, err := adapter.DispatchUpdate(context.Background(), tg.Update{
		UpdateID: 7,
		Message: &tg.Message{
			MessageID: 11,
			Text:      "hello",
			Chat:      tg.Chat{ID: -100, Type: "supergroup"},
		},
	})

	require.NoError(t, err)
	require.True(t, handled)
	require.False(t, responder.called)
	require.Empty(t, api.allCalls())
}

func TestTelegramAdapter_UnknownGroupSenderIsIgnoredWithoutChallenge(t *testing.T) {
	api := &fakeTelegramAPI{}
	responder := &genericResponderStub{}
	adapter := newTelegramAdapter(config.GatewayConfig{PairingSecret: "pair-secret"}, responder, api)

	handled, err := adapter.DispatchUpdate(context.Background(), tg.Update{
		UpdateID: 7,
		Message: &tg.Message{
			MessageID: 11,
			Text:      "hello",
			Chat:      tg.Chat{ID: -100, Type: "supergroup"},
			From:      &tg.User{ID: 9},
		},
	})

	require.NoError(t, err)
	require.True(t, handled)
	require.False(t, responder.called)
	require.Empty(t, api.allCalls())
}

func TestTelegramAdapter_GlobalAllowlistAuthorizesGroupSender(t *testing.T) {
	setTelegramDraftID(t, 77)
	api := &fakeTelegramAPI{}
	responder := &genericResponderStub{reply: "final reply"}
	adapter := newTelegramAdapter(config.GatewayConfig{
		AllowedUsers: []string{"9"},
	}, responder, api)

	handled, err := adapter.DispatchUpdate(context.Background(), tg.Update{
		UpdateID: 7,
		Message: &tg.Message{
			MessageID: 11,
			Text:      "hello",
			Chat:      tg.Chat{ID: -100, Type: "supergroup"},
			From:      &tg.User{ID: 9},
		},
	})

	require.NoError(t, err)
	require.True(t, handled)
	require.True(t, responder.called)
}

func TestTelegramAdapter_ApprovedSenderAuthorizesGroupSender(t *testing.T) {
	api := &fakeTelegramAPI{}
	responder := &genericResponderStub{reply: "final reply"}
	require.NoError(t, responder.SaveGatewayPairedSender(context.Background(), pairing.ApprovedSender{
		Source:   "telegram",
		SenderID: "9",
	}))
	adapter := newTelegramAdapter(config.GatewayConfig{PairingSecret: "pair-secret"}, responder, api)

	handled, err := adapter.DispatchUpdate(context.Background(), tg.Update{
		UpdateID: 7,
		Message: &tg.Message{
			MessageID: 11,
			Text:      "hello",
			Chat:      tg.Chat{ID: -100, Type: "supergroup"},
			From:      &tg.User{ID: 9},
		},
	})

	require.NoError(t, err)
	require.True(t, handled)
	require.True(t, responder.called)
}

func TestHasAllowedSenderRejectsBlankSender(t *testing.T) {
	require.False(t, hasAllowedSender([]string{"9"}, " "))
}

func telegramAdapterConfig() config.GatewayConfig {
	return config.GatewayConfig{
		PairingSecret: "pair-secret",
		Telegram: config.GatewayTelegramConfig{
			AllowedUsers: []string{"9"},
		},
	}
}
