package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/gateway/dispatch"
	"github.com/xymorphic/morph/pkg/gateway/httpjson"
	tg "github.com/xymorphic/morph/pkg/gateway/telegram"
	gatewaytypes "github.com/xymorphic/morph/pkg/gateway/types"
	"github.com/xymorphic/morph/pkg/str"
)

const (
	WebhookPath         = "/gateway/telegram/webhook"
	maxWebhookBodyBytes = 1 << 20
)

func SetWebhook(ctx context.Context, cfg config.GatewayTelegramConfig, url string) error {
	urlValue := str.String(url)
	url = urlValue.Trim()
	botTokenValue := str.String(cfg.BotToken)
	botToken := botTokenValue.Trim()
	webhookSecretValue := str.String(cfg.WebhookSecret)
	webhookSecret := webhookSecretValue.Trim()
	if botToken == "" {
		return errors.New("gateway telegram bot token is required")
	}
	cfg.BotToken = botToken
	if url == "" {
		return newTelegramAPI(cfg).DeleteWebhook(ctx)
	}
	if webhookSecret == "" {
		return errors.New("gateway telegram webhook secret is required")
	}

	cfg.WebhookSecret = webhookSecret
	return newTelegramAPI(cfg).SetWebhook(ctx, url, webhookSecret)
}

func HandleWebhook(cfg config.GatewayConfig, service Service, dispatcher *dispatch.Dispatcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			httpjson.WriteError(w, http.StatusMethodNotAllowed, gatewaytypes.ErrorCodeBadRequest, "method not allowed")
			return
		}
		if !cfg.Telegram.Enabled || cfg.Telegram.Mode != config.GatewayTelegramModeWebhook {
			httpjson.WriteError(w, http.StatusNotFound, gatewaytypes.ErrorCodeBadRequest, "telegram webhook is disabled")
			return
		}
		if err := tg.CheckWebhookSecret(r.Header.Get(tg.WebhookSecretHeader), cfg.Telegram.WebhookSecret); err != nil {
			httpjson.WriteError(w, http.StatusUnauthorized, gatewaytypes.ErrorCodeUnauthorized, "unauthorized")
			return
		}
		if service == nil {
			httpjson.WriteError(w, http.StatusInternalServerError, gatewaytypes.ErrorCodeInternalError,
				"gateway request failed")
			return
		}
		if dispatcher == nil {
			httpjson.WriteError(w, http.StatusInternalServerError, gatewaytypes.ErrorCodeInternalError,
				"gateway request failed")
			return
		}

		var update tg.Update
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxWebhookBodyBytes)).Decode(&update); err != nil {
			httpjson.WriteError(w, http.StatusBadRequest, gatewaytypes.ErrorCodeBadRequest, "invalid request")
			return
		}

		_, err := dispatcher.Enqueue(dispatch.Job{
			ID:          webhookUpdateID(update),
			MaxAttempts: 1,
			Run: func(ctx context.Context) error {
				return dispatchWebhookUpdate(ctx, cfg, service, update)
			},
		})
		if err != nil {
			log.Warn().Err(err).Int64("telegram_update_id", update.UpdateID).Msg("Telegram webhook enqueue failed")
			status := http.StatusInternalServerError
			if errors.Is(err, dispatch.ErrQueueFull) || errors.Is(err, dispatch.ErrDispatcherClosed) {
				status = http.StatusServiceUnavailable
			}
			httpjson.WriteError(w, status, gatewaytypes.ErrorCodeInternalError, "gateway request failed")
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	}
}

func dispatchWebhookUpdate(
	ctx context.Context,
	cfg config.GatewayConfig,
	service Service,
	update tg.Update,
) error {
	if _, err := newTelegramAdapter(cfg, service, newTelegramAPI(cfg.Telegram)).DispatchUpdate(ctx, update); err != nil {
		log.Warn().Err(err).Int64("telegram_update_id", update.UpdateID).Msg("Telegram webhook dispatch failed")
		return err
	}

	return nil
}

func webhookUpdateID(update tg.Update) string {
	return "telegram:update:" + strconv.FormatInt(update.UpdateID, 10)
}
