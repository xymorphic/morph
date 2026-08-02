package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/xymorphic/morph/internal/config"
	tg "github.com/xymorphic/morph/pkg/gateway/telegram"
	"github.com/xymorphic/morph/pkg/str"
)

const defaultTelegramAPIBase = "https://api.telegram.org"

var newTelegramDraftID = func() int64 {
	return time.Now().UnixNano()
}

var telegramTypingInterval = 4 * time.Second

type telegramAPI interface {
	GetUpdates(context.Context, int64) ([]tg.Update, error)
	SetWebhook(context.Context, string, string) error
	DeleteWebhook(context.Context) error
	SendMessage(context.Context, tg.Target, telegramText) (int64, error)
	EditMessageText(context.Context, tg.Target, int64, telegramText) error
	SendMessageDraft(context.Context, tg.Target, int64, telegramText) error
	SendChatAction(context.Context, tg.Target, string) error
}

type telegramText struct {
	Text      string
	ParseMode string
}

type telegramHTTPClient struct {
	client  *http.Client
	baseURL string
	token   string
}

func newTelegramHTTPClient(token string) *telegramHTTPClient {
	tokenValue := str.String(token)
	return &telegramHTTPClient{
		client:  http.DefaultClient,
		baseURL: defaultTelegramAPIBase,
		token:   tokenValue.Trim(),
	}
}

func (c *telegramHTTPClient) GetUpdates(ctx context.Context, offset int64) ([]tg.Update, error) {
	req := map[string]any{
		"offset":          offset,
		"timeout":         30,
		"allowed_updates": []string{"message", "edited_message", "callback_query"},
	}
	var updates []tg.Update
	if err := c.call(ctx, "getUpdates", req, &updates); err != nil {
		return nil, err
	}

	return updates, nil
}

func (c *telegramHTTPClient) SetWebhook(ctx context.Context, url string, secret string) error {
	urlValue := str.String(url)
	secretValue := str.String(secret)
	req := map[string]any{
		"url":          urlValue.Trim(),
		"secret_token": secretValue.Trim(),
	}
	return c.call(ctx, "setWebhook", req, nil)
}

func (c *telegramHTTPClient) DeleteWebhook(ctx context.Context) error {
	return c.call(ctx, "deleteWebhook", map[string]any{}, nil)
}

func (c *telegramHTTPClient) SendMessage(
	ctx context.Context,
	target tg.Target,
	message telegramText,
) (int64, error) {
	var result struct {
		MessageID int64 `json:"message_id"`
	}
	if err := c.call(ctx, "sendMessage", telegramSendRequest(target, message), &result); err != nil {
		return 0, err
	}

	return result.MessageID, nil
}

func (c *telegramHTTPClient) EditMessageText(
	ctx context.Context,
	target tg.Target,
	messageID int64,
	message telegramText,
) error {
	req := telegramSendRequest(target, message)
	req["message_id"] = messageID
	return c.call(ctx, "editMessageText", req, nil)
}

func (c *telegramHTTPClient) SendMessageDraft(
	ctx context.Context,
	target tg.Target,
	draftID int64,
	message telegramText,
) error {
	req := telegramSendRequest(target, message)
	req["draft_id"] = draftID
	return c.call(ctx, "sendMessageDraft", req, nil)
}

func (c *telegramHTTPClient) SendChatAction(ctx context.Context, target tg.Target, action string) error {
	req := telegramTargetRequest(target)
	req["action"] = action
	return c.call(ctx, "sendChatAction", req, nil)
}

func (c *telegramHTTPClient) call(ctx context.Context, method string, req any, out any) error {
	if c == nil {
		return errors.New("telegram client is required")
	}

	if c.client == nil {
		c.client = http.DefaultClient
	}
	baseURL := strings.TrimRight(c.baseURL, "/")
	if baseURL == "" {
		baseURL = defaultTelegramAPIBase
	}

	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("%s/bot%s/%s", baseURL, c.token, method),
		bytes.NewReader(body),
	)
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer func() { _ = httpResp.Body.Close() }()

	var apiResp struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
		ErrorCode   int             `json:"error_code"`
	}
	if err := json.NewDecoder(httpResp.Body).Decode(&apiResp); err != nil {
		return err
	}
	if httpResp.StatusCode == http.StatusConflict || apiResp.ErrorCode == http.StatusConflict {
		return telegramConflictError{description: apiResp.Description}
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		descriptionValue := str.String(apiResp.Description)
		if descriptionValue.Trim() != "" {
			return fmt.Errorf("telegram api http status %d: %s", httpResp.StatusCode, apiResp.Description)
		}
		return fmt.Errorf("telegram api http status %d", httpResp.StatusCode)
	}
	if !apiResp.OK {
		if apiResp.Description == "" {
			apiResp.Description = "telegram api returned ok=false"
		}
		return errors.New(apiResp.Description)
	}
	if out != nil {
		return json.Unmarshal(apiResp.Result, out)
	}

	return nil
}

type telegramSender struct {
	api        telegramAPI
	minEditGap time.Duration
}

func newTelegramSender(api telegramAPI) *telegramSender {
	return &telegramSender{api: api, minEditGap: 250 * time.Millisecond}
}

func SendFinal(
	ctx context.Context,
	cfg config.GatewayTelegramConfig,
	target tg.Target,
	text string,
) error {
	return newTelegramSender(newTelegramHTTPClient(cfg.BotToken)).SendFinal(ctx, target, text)
}

func (s *telegramSender) SendFinal(ctx context.Context, target tg.Target, text string) error {
	if s == nil || s.api == nil {
		return errors.New("telegram sender is required")
	}

	for _, chunk := range tg.ChunkText(text, tg.MessageTextLimit) {
		if _, err := s.sendMessage(ctx, target, chunk); err != nil {
			return err
		}
	}

	return nil
}

func (s *telegramSender) StreamTurn(
	ctx context.Context,
	target tg.Target,
	run func(func(string)) (string, error),
) error {
	if s == nil || s.api == nil {
		return errors.New("telegram sender is required")
	}

	streamer := s.streamerForTarget(target)
	if err := streamer.Start(ctx); err != nil {
		streamer = finalOnlyTelegramStreamer{sender: s, target: target}
		_ = streamer.Start(ctx)
	}

	streamFailed := false
	reply, err := run(func(delta string) {
		if streamFailed || delta == "" {
			return
		}

		if err := streamer.Append(ctx, delta); err != nil {
			streamFailed = true
			streamer = finalOnlyTelegramStreamer{sender: s, target: target}
			_ = streamer.Start(ctx)
		}
	})
	if err != nil {
		return err
	}

	return streamer.Finish(ctx, reply)
}

func (s *telegramSender) StartTyping(ctx context.Context, target tg.Target) func() {
	if s == nil || s.api == nil {
		return func() {}
	}

	typingCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	_ = s.api.SendChatAction(typingCtx, target, "typing")
	go func() {
		defer close(done)
		ticker := time.NewTicker(telegramTypingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-typingCtx.Done():
				return
			case <-ticker.C:
				_ = s.api.SendChatAction(typingCtx, target, "typing")
			}
		}
	}()

	return func() {
		cancel()
		<-done
	}
}

func (s *telegramSender) streamerForTarget(target tg.Target) telegramStreamer {
	if tg.SupportsNativeDraft(target) {
		return &nativeTelegramStreamer{sender: s, target: target}
	}

	return &simulatedTelegramStreamer{sender: s, target: target}
}

type telegramStreamer interface {
	Start(context.Context) error
	Append(context.Context, string) error
	Finish(context.Context, string) error
}

type nativeTelegramStreamer struct {
	sender  *telegramSender
	target  tg.Target
	draftID int64
	text    string
}

func (s *nativeTelegramStreamer) Start(context.Context) error {
	s.draftID = newTelegramDraftID()
	if s.draftID == 0 {
		s.draftID = 1
	}

	return nil
}

func (s *nativeTelegramStreamer) Append(ctx context.Context, delta string) error {
	s.text += delta
	return s.sender.sendMessageDraft(ctx, s.target, s.draftID, tg.WithCursor(s.text))
}

func (s *nativeTelegramStreamer) Finish(ctx context.Context, reply string) error {
	return s.sender.SendFinal(ctx, s.target, reply)
}

type simulatedTelegramStreamer struct {
	sender     *telegramSender
	target     tg.Target
	messageID  int64
	text       string
	lastEdit   string
	lastEditAt time.Time
}

func (s *simulatedTelegramStreamer) Start(ctx context.Context) error {
	messageID, err := s.sender.sendMessage(ctx, s.target, tg.DraftCursor)
	if err != nil {
		return err
	}
	s.messageID = messageID
	return nil
}

func (s *simulatedTelegramStreamer) Append(ctx context.Context, delta string) error {
	s.text += delta
	next := tg.WithCursor(s.text)
	if next == s.lastEdit {
		return nil
	}
	if !s.lastEditAt.IsZero() && time.Since(s.lastEditAt) < s.sender.minEditGap {
		return nil
	}
	if err := s.sender.editMessageText(ctx, s.target, s.messageID, next); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "message is not modified") {
			return nil
		}

		return err
	}
	s.lastEdit = next
	s.lastEditAt = time.Now()
	return nil
}

func (s *simulatedTelegramStreamer) Finish(ctx context.Context, reply string) error {
	for index, chunk := range tg.ChunkText(reply, tg.MessageTextLimit) {
		if index == 0 && s.messageID != 0 {
			if err := s.sender.editMessageText(ctx, s.target, s.messageID, chunk); err != nil {
				return err
			}

			continue
		}
		if _, err := s.sender.sendMessage(ctx, s.target, chunk); err != nil {
			return err
		}
	}

	return nil
}

type finalOnlyTelegramStreamer struct {
	sender *telegramSender
	target tg.Target
}

func (s finalOnlyTelegramStreamer) Start(context.Context) error          { return nil }
func (s finalOnlyTelegramStreamer) Append(context.Context, string) error { return nil }
func (s finalOnlyTelegramStreamer) Finish(ctx context.Context, reply string) error {
	return s.sender.SendFinal(ctx, s.target, reply)
}

func (s *telegramSender) sendMessage(ctx context.Context, target tg.Target, text string) (int64, error) {
	message := telegramFormattedText(text)
	messageID, err := s.api.SendMessage(ctx, target, message)
	if err == nil || !isTelegramParseError(err) {
		return messageID, err
	}

	return s.api.SendMessage(ctx, target, telegramPlainText(text))
}

func (s *telegramSender) editMessageText(
	ctx context.Context,
	target tg.Target,
	messageID int64,
	text string,
) error {
	message := telegramFormattedText(text)
	err := s.api.EditMessageText(ctx, target, messageID, message)
	if err == nil || !isTelegramParseError(err) {
		return err
	}

	return s.api.EditMessageText(ctx, target, messageID, telegramPlainText(text))
}

func (s *telegramSender) sendMessageDraft(
	ctx context.Context,
	target tg.Target,
	draftID int64,
	text string,
) error {
	message := telegramFormattedText(text)
	err := s.api.SendMessageDraft(ctx, target, draftID, message)
	if err == nil || !isTelegramParseError(err) {
		return err
	}

	return s.api.SendMessageDraft(ctx, target, draftID, telegramPlainText(text))
}

func telegramFormattedText(text string) telegramText {
	return telegramText{Text: tg.FormatMarkdownV2(text), ParseMode: tg.ParseModeMarkdownV2}
}

func telegramPlainText(text string) telegramText {
	return telegramText{Text: tg.PlainTextFromMarkdownV2(text)}
}

func isTelegramParseError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "parse") || strings.Contains(message, "markdown")
}

func telegramSendRequest(target tg.Target, message telegramText) map[string]any {
	req := telegramTargetRequest(target)
	req["text"] = message.Text
	parseModeValue := str.String(message.ParseMode)
	if parseModeValue.Trim() != "" {
		parseModeValue2 := str.String(message.ParseMode)
		req["parse_mode"] = parseModeValue2.Trim()
	}
	if target.ReplyToMessageID != 0 {
		req["reply_parameters"] = map[string]any{"message_id": target.ReplyToMessageID}
	}

	return req
}

func telegramTargetRequest(target tg.Target) map[string]any {
	req := map[string]any{
		"chat_id": target.ChatID,
	}
	if target.ThreadID != "" {
		req["message_thread_id"] = telegramThreadIDValue(target.ThreadID)
	}

	return req
}

func telegramThreadIDValue(threadID string) any {
	threadIDValue := str.String(threadID)
	value, err := strconv.ParseInt(threadIDValue.Trim(), 10, 64)
	if err != nil {
		threadIDValue2 := str.String(threadID)
		return threadIDValue2.Trim()
	}

	return value
}

type telegramConflictError struct {
	description string
}

func (e telegramConflictError) Error() string {
	if e.description == "" {
		return "telegram polling conflict"
	}

	return "telegram polling conflict: " + e.description
}
