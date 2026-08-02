package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/config"
	tg "github.com/xymorphic/morph/pkg/gateway/telegram"
)

func TestTelegramHTTPClientSetWebhookSendsURLAndSecret(t *testing.T) {
	var method string
	var request map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.URL.Path
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer server.Close()
	client := &telegramHTTPClient{
		client:  server.Client(),
		baseURL: server.URL,
		token:   "telegram-token",
	}

	err := client.SetWebhook(context.Background(), " https://example.com/gateway/telegram/webhook ", " secret ")

	require.NoError(t, err)
	require.Equal(t, "/bottelegram-token/setWebhook", method)
	require.Equal(t, map[string]string{
		"url":          "https://example.com/gateway/telegram/webhook",
		"secret_token": "secret",
	}, request)
}

func TestTelegramHTTPClientDeleteWebhook(t *testing.T) {
	var method string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.URL.Path
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer server.Close()
	client := &telegramHTTPClient{
		client:  server.Client(),
		baseURL: server.URL,
		token:   "telegram-token",
	}

	err := client.DeleteWebhook(context.Background())

	require.NoError(t, err)
	require.Equal(t, "/bottelegram-token/deleteWebhook", method)
}

func TestTelegramHTTPClientReturnsTelegramHTTPErrorDescription(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":404,"description":"Not Found"}`))
	}))
	defer server.Close()
	client := &telegramHTTPClient{
		client:  server.Client(),
		baseURL: server.URL,
		token:   "telegram-token",
	}

	err := client.SetWebhook(context.Background(), "https://example.com/gateway/telegram/webhook", "secret")

	require.EqualError(t, err, "telegram api http status 404: Not Found")
}

func TestTelegramSender_StreamsPrivateChatWithNativeDraftsThenFinalMessage(t *testing.T) {
	setTelegramDraftID(t, 77)
	api := &fakeTelegramAPI{}
	sender := newTelegramSender(api)

	err := sender.StreamTurn(context.Background(), tg.Target{ChatID: "123", ChatType: "private"}, func(onDelta func(string)) (string, error) {
		onDelta("hello")
		onDelta(" world")
		return "final", nil
	})

	require.NoError(t, err)
	require.Equal(t, []telegramAPICall{
		{method: "sendMessageDraft", target: tg.Target{ChatID: "123", ChatType: "private"}, draftID: 77, text: "hello\n..."},
		{method: "sendMessageDraft", target: tg.Target{ChatID: "123", ChatType: "private"}, draftID: 77, text: "hello world\n..."},
		{method: "sendMessage", target: tg.Target{ChatID: "123", ChatType: "private"}, text: "final"},
	}, api.allCalls())
}

func TestTelegramSender_SendsMarkdownV2ParseModeForStreamingAndFinalText(t *testing.T) {
	setTelegramDraftID(t, 77)
	api := &fakeTelegramAPI{}
	sender := newTelegramSender(api)
	target := tg.Target{ChatID: "123", ChatType: "private"}

	err := sender.StreamTurn(context.Background(), target, func(onDelta func(string)) (string, error) {
		onDelta("**hello**")
		return "final!", nil
	})

	require.NoError(t, err)
	require.Equal(t, []telegramAPICall{
		{
			method:    "sendMessageDraft",
			target:    target,
			draftID:   77,
			text:      "*hello*\n\\.\\.\\.",
			parseMode: tg.ParseModeMarkdownV2,
		},
		{
			method:    "sendMessage",
			target:    target,
			text:      "final\\!",
			parseMode: tg.ParseModeMarkdownV2,
		},
	}, api.allCallsWithParseMode())
}

func TestTelegramSender_RetriesPlainTextWhenMarkdownV2ParsingFails(t *testing.T) {
	parseErr := errors.New("Bad Request: can't parse entities: markdown")
	api := &fakeTelegramAPI{sendErrs: []error{parseErr, nil}}
	sender := newTelegramSender(api)

	err := sender.SendFinal(context.Background(), tg.Target{ChatID: "123"}, "**final**")

	require.NoError(t, err)
	require.Equal(t, []telegramAPICall{
		{
			method:    "sendMessage",
			target:    tg.Target{ChatID: "123"},
			text:      "*final*",
			parseMode: tg.ParseModeMarkdownV2,
		},
		{method: "sendMessage", target: tg.Target{ChatID: "123"}, text: "final"},
	}, api.allCallsWithParseMode())
}

func TestTelegramSender_RetriesPlainTextWhenSimulatedEditMarkdownV2ParsingFails(t *testing.T) {
	parseErr := errors.New("Bad Request: can't parse entities")
	api := &fakeTelegramAPI{editErrs: []error{parseErr, nil}}
	sender := newTelegramSender(api)
	sender.minEditGap = 0
	streamer := &simulatedTelegramStreamer{
		sender:    sender,
		target:    tg.Target{ChatID: "-100", ChatType: "group"},
		messageID: 1,
	}

	err := streamer.Append(context.Background(), "**partial**")

	require.NoError(t, err)
	require.Equal(t, []telegramAPICall{
		{
			method:    "editMessageText",
			target:    tg.Target{ChatID: "-100", ChatType: "group"},
			messageID: 1,
			text:      "*partial*\n\\.\\.\\.",
			parseMode: tg.ParseModeMarkdownV2,
		},
		{
			method:    "editMessageText",
			target:    tg.Target{ChatID: "-100", ChatType: "group"},
			messageID: 1,
			text:      "partial\n...",
		},
	}, api.allCallsWithParseMode())
}

func TestTelegramSender_RetriesPlainTextWhenDraftMarkdownV2ParsingFails(t *testing.T) {
	setTelegramDraftID(t, 77)
	parseErr := errors.New("Bad Request: can't parse entities")
	api := &fakeTelegramAPI{draftErrs: []error{parseErr, nil}}
	sender := newTelegramSender(api)
	target := tg.Target{ChatID: "123", ChatType: "private"}

	err := sender.StreamTurn(context.Background(), target, func(onDelta func(string)) (string, error) {
		onDelta("**partial**")
		return "final", nil
	})

	require.NoError(t, err)
	require.Equal(t, []telegramAPICall{
		{
			method:    "sendMessageDraft",
			target:    target,
			draftID:   77,
			text:      "*partial*\n\\.\\.\\.",
			parseMode: tg.ParseModeMarkdownV2,
		},
		{method: "sendMessageDraft", target: target, draftID: 77, text: "partial\n..."},
		{
			method:    "sendMessage",
			target:    target,
			text:      "final",
			parseMode: tg.ParseModeMarkdownV2,
		},
	}, api.allCallsWithParseMode())
}

func TestTelegramSender_DetectsTelegramParseErrors(t *testing.T) {
	require.False(t, isTelegramParseError(errTelegramTest))
	require.True(t, isTelegramParseError(errors.New("Bad Request: can't parse entities")))
	require.True(t, isTelegramParseError(errors.New("Bad Request: markdown parse failed")))
}

func TestTelegramSender_StreamsGroupTopicWithPlaceholderAndEdits(t *testing.T) {
	api := &fakeTelegramAPI{}
	sender := newTelegramSender(api)
	sender.minEditGap = 0
	target := tg.Target{ChatID: "-100", ThreadID: "42", ReplyToMessageID: 9, ChatType: "supergroup"}

	err := sender.StreamTurn(context.Background(), target, func(onDelta func(string)) (string, error) {
		onDelta("hello")
		onDelta(" world")
		return "final", nil
	})

	require.NoError(t, err)
	require.Equal(t, []telegramAPICall{
		{method: "sendMessage", target: target, text: "..."},
		{method: "editMessageText", target: target, messageID: 1, text: "hello\n..."},
		{method: "editMessageText", target: target, messageID: 1, text: "hello world\n..."},
		{method: "editMessageText", target: target, messageID: 1, text: "final"},
	}, api.allCalls())
	require.Equal(t, int64(42), telegramSendRequest(target, telegramText{Text: "final"})["message_thread_id"])
	require.Equal(t, map[string]any{"message_id": int64(9)}, telegramSendRequest(target, telegramText{Text: "final"})["reply_parameters"])
}

func TestTelegramSender_CoalescesSimulatedStreamingEdits(t *testing.T) {
	api := &fakeTelegramAPI{}
	sender := newTelegramSender(api)
	sender.minEditGap = time.Hour
	target := tg.Target{ChatID: "-100", ChatType: "group"}

	err := sender.StreamTurn(context.Background(), target, func(onDelta func(string)) (string, error) {
		onDelta("a")
		onDelta("b")
		return "ab", nil
	})

	require.NoError(t, err)
	require.Equal(t, []telegramAPICall{
		{method: "sendMessage", target: target, text: "..."},
		{method: "editMessageText", target: target, messageID: 1, text: "a\n..."},
		{method: "editMessageText", target: target, messageID: 1, text: "ab"},
	}, api.allCalls())
}

func TestTelegramSender_SkipsDuplicateSimulatedEdits(t *testing.T) {
	api := &fakeTelegramAPI{}
	sender := newTelegramSender(api)
	sender.minEditGap = 0
	streamer := &simulatedTelegramStreamer{sender: sender, target: tg.Target{ChatID: "-100"}, messageID: 1}

	require.NoError(t, streamer.Append(context.Background(), "same"))
	require.NoError(t, streamer.Append(context.Background(), ""))

	require.Equal(t, []telegramAPICall{
		{method: "editMessageText", target: tg.Target{ChatID: "-100"}, messageID: 1, text: "same\n..."},
	}, api.allCalls())
}

func TestTelegramSender_IgnoresNotModifiedEditError(t *testing.T) {
	api := &fakeTelegramAPI{editErrs: []error{errors.New("Bad Request: message is not modified"), nil}}
	sender := newTelegramSender(api)
	sender.minEditGap = 0
	target := tg.Target{ChatID: "-100", ChatType: "group"}

	err := sender.StreamTurn(context.Background(), target, func(onDelta func(string)) (string, error) {
		onDelta("same")
		return "final", nil
	})

	require.NoError(t, err)
	require.Equal(t, []telegramAPICall{
		{method: "sendMessage", target: target, text: "..."},
		{method: "editMessageText", target: target, messageID: 1, text: "same\n..."},
		{method: "editMessageText", target: target, messageID: 1, text: "final"},
	}, api.allCalls())
}

func TestTelegramSender_FallsBackWhenSimulatedStreamingStartFails(t *testing.T) {
	api := &fakeTelegramAPI{sendErrs: []error{errTelegramTest, nil}}
	sender := newTelegramSender(api)
	target := tg.Target{ChatID: "-100", ChatType: "group"}

	err := sender.StreamTurn(context.Background(), target, func(onDelta func(string)) (string, error) {
		onDelta("partial")
		return "final", nil
	})

	require.NoError(t, err)
	require.Equal(t, []telegramAPICall{
		{method: "sendMessage", target: target, text: "..."},
		{method: "sendMessage", target: target, text: "final"},
	}, api.allCalls())
}

func TestTelegramSender_FallsBackWhenSimulatedEditFails(t *testing.T) {
	api := &fakeTelegramAPI{editErr: errTelegramTest}
	sender := newTelegramSender(api)
	sender.minEditGap = 0
	target := tg.Target{ChatID: "-100", ChatType: "group"}

	err := sender.StreamTurn(context.Background(), target, func(onDelta func(string)) (string, error) {
		onDelta("partial")
		return "final", nil
	})

	require.NoError(t, err)
	require.Equal(t, []telegramAPICall{
		{method: "sendMessage", target: target, text: "..."},
		{method: "editMessageText", target: target, messageID: 1, text: "partial\n..."},
		{method: "sendMessage", target: target, text: "final"},
	}, api.allCalls())
}

func TestTelegramSender_FallsBackToFinalMessageWhenStreamingFails(t *testing.T) {
	setTelegramDraftID(t, 77)
	api := &fakeTelegramAPI{draftErr: errTelegramTest}
	sender := newTelegramSender(api)
	target := tg.Target{ChatID: "123", ChatType: "private"}

	err := sender.StreamTurn(context.Background(), target, func(onDelta func(string)) (string, error) {
		onDelta("partial")
		onDelta("ignored")
		return "final", nil
	})

	require.NoError(t, err)
	require.Equal(t, []telegramAPICall{
		{method: "sendMessageDraft", target: target, draftID: 77, text: "partial\n..."},
		{method: "sendMessage", target: target, text: "final"},
	}, api.allCalls())
}

func TestTelegramSender_UsesFallbackDraftIDWhenGeneratedIDIsZero(t *testing.T) {
	setTelegramDraftID(t, 0)
	api := &fakeTelegramAPI{}
	sender := newTelegramSender(api)

	err := sender.StreamTurn(context.Background(), tg.Target{ChatID: "123", ChatType: "private"}, func(onDelta func(string)) (string, error) {
		onDelta("partial")
		return "final", nil
	})

	require.NoError(t, err)
	require.Equal(t, int64(1), api.callsOfMethod("sendMessageDraft")[0].draftID)
}

func TestTelegramSender_ChunksFinalDelivery(t *testing.T) {
	api := &fakeTelegramAPI{}
	sender := newTelegramSender(api)
	text := strings.Repeat("x", tg.MessageTextLimit) + "y"

	err := sender.SendFinal(context.Background(), tg.Target{ChatID: "123"}, text)

	require.NoError(t, err)
	calls := api.callsOfMethod("sendMessage")
	require.Len(t, calls, 2)
	require.Len(t, []rune(calls[0].text), tg.MessageTextLimit)
	require.Equal(t, "y", calls[1].text)
}

func TestSendFinal_UsesConfiguredTelegramClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := SendFinal(
		ctx,
		config.GatewayTelegramConfig{BotToken: "telegram-token"},
		tg.Target{ChatID: "123"},
		"message",
	)

	require.ErrorIs(t, err, context.Canceled)
}

func TestTelegramSender_SimulatedFinalSendsRemainingChunks(t *testing.T) {
	api := &fakeTelegramAPI{}
	sender := newTelegramSender(api)
	target := tg.Target{ChatID: "-100", ChatType: "group"}
	text := strings.Repeat("x", tg.MessageTextLimit) + "y"

	err := sender.StreamTurn(context.Background(), target, func(func(string)) (string, error) {
		return text, nil
	})

	require.NoError(t, err)
	require.Equal(t, []telegramAPICall{
		{method: "sendMessage", target: target, text: "..."},
		{method: "editMessageText", target: target, messageID: 1, text: strings.Repeat("x", tg.MessageTextLimit)},
		{method: "sendMessage", target: target, text: "y"},
	}, api.allCalls())
}

func TestTelegramSender_SimulatedFinalHandlesEmptyReplyAndEditError(t *testing.T) {
	api := &fakeTelegramAPI{editErr: errTelegramTest}
	sender := newTelegramSender(api)
	streamer := &simulatedTelegramStreamer{sender: sender, target: tg.Target{ChatID: "-100"}, messageID: 1}

	require.NoError(t, streamer.Finish(context.Background(), " "))
	require.Empty(t, api.allCalls())
	require.ErrorIs(t, streamer.Finish(context.Background(), "final"), errTelegramTest)
	require.Equal(t, []telegramAPICall{
		{method: "editMessageText", target: tg.Target{ChatID: "-100"}, messageID: 1, text: "final"},
	}, api.allCalls())
}

func TestTelegramSender_SimulatedFinalReturnsRemainingChunkSendError(t *testing.T) {
	api := &fakeTelegramAPI{sendErr: errTelegramTest}
	sender := newTelegramSender(api)
	streamer := &simulatedTelegramStreamer{sender: sender, target: tg.Target{ChatID: "-100"}, messageID: 1}
	text := strings.Repeat("x", tg.MessageTextLimit) + "y"

	err := streamer.Finish(context.Background(), text)

	require.ErrorIs(t, err, errTelegramTest)
}

func TestTelegramSender_PropagatesRunAndFinalErrors(t *testing.T) {
	runErr := errors.New("run failed")
	sender := newTelegramSender(&fakeTelegramAPI{})

	err := sender.StreamTurn(context.Background(), tg.Target{ChatID: "123", ChatType: "private"}, func(func(string)) (string, error) {
		return "", runErr
	})

	require.ErrorIs(t, err, runErr)

	err = newTelegramSender(&fakeTelegramAPI{sendErr: errTelegramTest}).
		SendFinal(context.Background(), tg.Target{ChatID: "123"}, "final")

	require.ErrorIs(t, err, errTelegramTest)
}

func TestTelegramSender_StartTypingSendsRepeatedChatActionsUntilStopped(t *testing.T) {
	origTypingInterval := telegramTypingInterval
	telegramTypingInterval = 10 * time.Millisecond
	t.Cleanup(func() { telegramTypingInterval = origTypingInterval })
	api := &fakeTelegramAPI{}
	sender := newTelegramSender(api)
	target := tg.Target{ChatID: "123", ThreadID: "42", ChatType: "private"}

	stop := sender.StartTyping(context.Background(), target)

	require.Eventually(t, func() bool {
		return len(api.callsOfMethod("sendChatAction")) >= 2
	}, time.Second, time.Millisecond)
	stop()
	actionCalls := api.callsOfMethod("sendChatAction")
	require.NotEmpty(t, actionCalls)
	for _, call := range actionCalls {
		require.Equal(t, telegramAPICall{method: "sendChatAction", target: target, action: "typing"}, call)
	}
	countAfterStop := len(actionCalls)
	time.Sleep(3 * telegramTypingInterval)
	require.Len(t, api.callsOfMethod("sendChatAction"), countAfterStop)
}

func TestTelegramSender_RejectsMissingSender(t *testing.T) {
	require.EqualError(t, (*telegramSender)(nil).SendFinal(context.Background(), tg.Target{}, "text"), "telegram sender is required")
	require.EqualError(t, newTelegramSender(nil).SendFinal(context.Background(), tg.Target{}, "text"), "telegram sender is required")

	err := (*telegramSender)(nil).StreamTurn(context.Background(), tg.Target{}, func(func(string)) (string, error) {
		return "", nil
	})
	require.EqualError(t, err, "telegram sender is required")

	require.NotPanics(t, func() {
		(*telegramSender)(nil).StartTyping(context.Background(), tg.Target{})()
		newTelegramSender(nil).StartTyping(context.Background(), tg.Target{})()
	})
}

func TestTelegramHTTPClient_SendsRequestPayloadAndDecodesMessageID(t *testing.T) {
	var path string
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":44}}`))
	}))
	defer server.Close()
	client := newTelegramHTTPClient("token")
	client.baseURL = server.URL

	messageID, err := client.SendMessage(context.Background(), tg.Target{
		ChatID:           "-100",
		ThreadID:         "42",
		ReplyToMessageID: 9,
	}, telegramText{Text: "hello", ParseMode: tg.ParseModeMarkdownV2})

	require.NoError(t, err)
	require.Equal(t, int64(44), messageID)
	require.Equal(t, "/bottoken/sendMessage", path)
	require.Equal(t, map[string]any{
		"chat_id":           "-100",
		"text":              "hello",
		"parse_mode":        tg.ParseModeMarkdownV2,
		"message_thread_id": float64(42),
		"reply_parameters": map[string]any{
			"message_id": float64(9),
		},
	}, payload)
}

func TestTelegramHTTPClient_SendsDraftIDForNativeStreaming(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/bottoken/sendMessageDraft", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer server.Close()
	client := newTelegramHTTPClient("token")
	client.baseURL = server.URL

	err := client.SendMessageDraft(context.Background(), tg.Target{
		ChatID:   "123",
		ThreadID: "42",
	}, 77, telegramText{Text: "partial", ParseMode: tg.ParseModeMarkdownV2})

	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"chat_id":           "123",
		"text":              "partial",
		"parse_mode":        tg.ParseModeMarkdownV2,
		"message_thread_id": float64(42),
		"draft_id":          float64(77),
	}, payload)
}

func TestTelegramHTTPClient_SendsChatAction(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/bottoken/sendChatAction", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer server.Close()
	client := newTelegramHTTPClient("token")
	client.baseURL = server.URL

	err := client.SendChatAction(context.Background(), tg.Target{
		ChatID:   "-100",
		ThreadID: "42",
	}, "typing")

	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"chat_id":           "-100",
		"message_thread_id": float64(42),
		"action":            "typing",
	}, payload)
}

func TestTelegramHTTPClient_DecodesUpdatesAndSendsEdit(t *testing.T) {
	var paths []string
	var editPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/bottoken/getUpdates":
			_, _ = w.Write([]byte(`{"ok":true,"result":[{"update_id":8,"message":{"message_id":2,"text":"hi","chat":{"id":123}}}]}`))
		case "/bottoken/editMessageText":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&editPayload))
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := newTelegramHTTPClient("token")
	client.baseURL = server.URL

	updates, err := client.GetUpdates(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, []tg.Update{{
		UpdateID: 8,
		Message: &tg.Message{
			MessageID: 2,
			Text:      "hi",
			Chat:      tg.Chat{ID: 123},
		},
	}}, updates)

	err = client.EditMessageText(
		context.Background(),
		tg.Target{ChatID: "123"},
		2,
		telegramText{Text: "edited", ParseMode: tg.ParseModeMarkdownV2},
	)
	require.NoError(t, err)
	require.Equal(t, []string{"/bottoken/getUpdates", "/bottoken/editMessageText"}, paths)
	require.Equal(t, map[string]any{
		"chat_id":    "123",
		"text":       "edited",
		"parse_mode": tg.ParseModeMarkdownV2,
		"message_id": float64(2),
	}, editPayload)
}

func TestTelegramHTTPClient_ReturnsSendMessageErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"description":"send failed"}`))
	}))
	defer server.Close()
	client := newTelegramHTTPClient("token")
	client.baseURL = server.URL

	_, err := client.SendMessage(
		context.Background(),
		tg.Target{ChatID: "123"},
		telegramText{Text: "hello"},
	)

	require.EqualError(t, err, "send failed")
}

func TestTelegramHTTPClient_ReturnsClientAndDecodeErrors(t *testing.T) {
	require.EqualError(t, (*telegramHTTPClient)(nil).call(context.Background(), "sendMessage", map[string]any{}, nil),
		"telegram client is required")

	client := &telegramHTTPClient{client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errTelegramTest
	})}}
	err := client.call(context.Background(), "sendMessage", map[string]any{}, nil)
	require.ErrorIs(t, err, errTelegramTest)

	err = (&telegramHTTPClient{}).call(context.Background(), "sendMessage", make(chan int), nil)
	require.EqualError(t, err, "json: unsupported type: chan int")

	err = (&telegramHTTPClient{baseURL: "http://[::1"}).call(context.Background(), "sendMessage", map[string]any{}, nil)
	require.Contains(t, err.Error(), "missing ']' in host")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer server.Close()
	client = &telegramHTTPClient{baseURL: server.URL}

	err = client.call(context.Background(), "sendMessage", map[string]any{}, nil)

	require.Contains(t, err.Error(), "invalid character")
}

func TestTelegramHTTPClient_ReturnsDefaultDescriptionForOkFalse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false}`))
	}))
	defer server.Close()
	client := newTelegramHTTPClient("token")
	client.baseURL = server.URL

	err := client.call(context.Background(), "sendMessage", map[string]any{}, nil)

	require.EqualError(t, err, "telegram api returned ok=false")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestTelegramSendRequestKeepsNonnumericThreadID(t *testing.T) {
	require.Equal(t, "topic", telegramSendRequest(
		tg.Target{ChatID: "123", ThreadID: "topic"},
		telegramText{Text: "text"},
	)["message_thread_id"])
}

func TestTelegramConflictErrorWithoutDescription(t *testing.T) {
	require.EqualError(t, telegramConflictError{}, "telegram polling conflict")
}

func TestTelegramHTTPClient_ReturnsProviderErrors(t *testing.T) {
	for _, tt := range []struct {
		name     string
		status   int
		body     string
		conflict bool
		message  string
	}{
		{name: "conflict", status: http.StatusConflict, body: `{"ok":false,"error_code":409,"description":"terminated by other getUpdates"}`, conflict: true},
		{name: "http status", status: http.StatusBadGateway, body: `{"ok":false,"description":"bad gateway"}`, message: "telegram api http status 502: bad gateway"},
		{name: "ok false", status: http.StatusOK, body: `{"ok":false,"description":"invalid token"}`, message: "invalid token"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			client := newTelegramHTTPClient("token")
			client.baseURL = server.URL

			_, err := client.GetUpdates(context.Background(), 0)

			if tt.conflict {
				var conflict telegramConflictError
				require.ErrorAs(t, err, &conflict)
				return
			}
			require.EqualError(t, err, tt.message)
		})
	}
}

func setTelegramDraftID(t *testing.T, id int64) {
	t.Helper()

	origNewTelegramDraftID := newTelegramDraftID
	newTelegramDraftID = func() int64 { return id }
	t.Cleanup(func() { newTelegramDraftID = origNewTelegramDraftID })
}
