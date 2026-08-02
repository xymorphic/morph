package telegram

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/config"
	storage "github.com/xymorphic/morph/internal/state/core"
	tg "github.com/xymorphic/morph/pkg/gateway/telegram"
)

func TestStartPolling_DispatchesUpdatesAndAdvancesOffset(t *testing.T) {
	setTelegramDraftID(t, 77)
	ctx, cancel := context.WithCancel(context.Background())
	api := &fakeTelegramAPI{
		updates: [][]tg.Update{
			{
				{
					UpdateID: 10,
					Message: &tg.Message{
						MessageID: 1,
						Text:      "hello",
						Chat:      tg.Chat{ID: 123, Type: "private"},
					},
				},
				{UpdateID: 11, EditedMessage: &tg.Message{Text: "ignored"}},
			},
			nil,
		},
		onGet: func(offset int64) {
			if offset == 12 {
				cancel()
			}
		},
	}
	responder := &genericResponderStub{
		createdSession: storage.Session{ID: genericCreatedSessionID},
		reply:          "reply",
	}

	err := startTelegramPolling(ctx, telegramPollingConfig(), responder, api)

	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return responder.called
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, []telegramAPICall{
		{method: "getUpdates", offset: 0},
		{method: "sendChatAction", target: tg.Target{ChatID: "123", ReplyToMessageID: 1, ChatType: "private"}, action: "typing"},
		{method: "sendMessageDraft", target: tg.Target{ChatID: "123", ReplyToMessageID: 1, ChatType: "private"}, draftID: 77, text: "stream\n..."},
		{method: "sendMessageDraft", target: tg.Target{ChatID: "123", ReplyToMessageID: 1, ChatType: "private"}, draftID: 77, text: "stream delta\n..."},
		{method: "sendMessage", target: tg.Target{ChatID: "123", ReplyToMessageID: 1, ChatType: "private"}, text: "reply"},
		{method: "getUpdates", offset: 12},
	}, api.allCalls())
}

func TestStartPolling_UsesConfiguredAPIFactory(t *testing.T) {
	origNewTelegramAPI := newTelegramAPI
	t.Cleanup(func() { newTelegramAPI = origNewTelegramAPI })
	ctx, cancel := context.WithCancel(context.Background())
	api := &fakeTelegramAPI{onGet: func(int64) { cancel() }}
	newTelegramAPI = func(cfg config.GatewayTelegramConfig) telegramAPI {
		require.Equal(t, "token", cfg.BotToken)
		return api
	}

	cfg := telegramPollingConfig()
	cfg.Telegram.BotToken = "token"
	err := StartPolling(ctx, cfg, &genericResponderStub{})

	require.NoError(t, err)
	require.Equal(t, []telegramAPICall{{method: "getUpdates", offset: 0}}, api.allCalls())
}

func TestNewTelegramAPIDefaultFactoryBuildsHTTPClient(t *testing.T) {
	client, ok := newTelegramAPI(config.GatewayTelegramConfig{BotToken: " token "}).(*telegramHTTPClient)

	require.True(t, ok)
	require.Equal(t, "token", client.token)
}

func TestStartPolling_StopsImmediatelyWhenContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := startTelegramPolling(ctx, telegramPollingConfig(), &genericResponderStub{}, &fakeTelegramAPI{})

	require.NoError(t, err)
}

func TestStartPolling_AdvancesOffsetAfterDispatchError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	api := &fakeTelegramAPI{
		updates: [][]tg.Update{
			{
				{
					UpdateID: 10,
					Message: &tg.Message{
						MessageID: 1,
						Text:      "hello",
						Chat:      tg.Chat{ID: 123, Type: "private"},
					},
				},
			},
			nil,
		},
		onGet: func(offset int64) {
			if offset == 11 {
				cancel()
			}
		},
	}

	err := startTelegramPolling(ctx, telegramPollingConfig(), &genericResponderStub{getBindingErr: errTelegramTest}, api)

	require.NoError(t, err)
	require.Equal(t, []telegramAPICall{
		{method: "getUpdates", offset: 0},
		{method: "getUpdates", offset: 11},
	}, api.allCalls())
}

func TestStartPolling_StopsDuringRetryDelay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	api := &fakeTelegramAPI{
		getErr: errTelegramTest,
		onGet:  func(int64) { cancel() },
	}

	err := startTelegramPolling(ctx, telegramPollingConfig(), &genericResponderStub{}, api)

	require.NoError(t, err)
}

func TestStartPolling_RetriesTransientErrors(t *testing.T) {
	origRetryDelay := telegramPollingRetryDelay
	telegramPollingRetryDelay = time.Millisecond
	t.Cleanup(func() { telegramPollingRetryDelay = origRetryDelay })
	ctx, cancel := context.WithCancel(context.Background())
	getCalls := 0
	api := &fakeTelegramAPI{
		getErrs: []error{errTelegramTest, nil},
		onGet: func(offset int64) {
			getCalls++
			if offset == 0 && getCalls == 2 {
				cancel()
			}
		},
	}

	err := startTelegramPolling(ctx, telegramPollingConfig(), &genericResponderStub{}, api)

	require.NoError(t, err)
	require.Len(t, api.callsOfMethod("getUpdates"), 2)
}

func TestStartPolling_ReturnsConflictError(t *testing.T) {
	api := &fakeTelegramAPI{getErr: telegramConflictError{description: "other poller"}}

	err := startTelegramPolling(context.Background(), telegramPollingConfig(), &genericResponderStub{}, api)

	var conflict telegramConflictError
	require.ErrorAs(t, err, &conflict)
	require.EqualError(t, err, "telegram polling conflict: other poller")
}

func TestStartPolling_RejectsMissingAPIClient(t *testing.T) {
	err := startTelegramPolling(context.Background(), telegramPollingConfig(), &genericResponderStub{}, nil)

	require.EqualError(t, err, "telegram api client is required")
}

func TestStartPolling_WaitsWhenDisabledOrWebhookMode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := startTelegramPolling(ctx, config.GatewayConfig{}, nil, &fakeTelegramAPI{})

	require.NoError(t, err)

	err = startTelegramPolling(ctx, config.GatewayConfig{
		Telegram: config.GatewayTelegramConfig{
			Enabled: true,
			Mode:    config.GatewayTelegramModeWebhook,
		},
	}, nil, &fakeTelegramAPI{})

	require.NoError(t, err)
}

func telegramPollingConfig() config.GatewayConfig {
	return config.GatewayConfig{
		PairingSecret: "pair-secret",
		Telegram: config.GatewayTelegramConfig{
			Enabled:      true,
			Mode:         config.GatewayTelegramModePolling,
			AllowedUsers: []string{"123"},
		},
	}
}
