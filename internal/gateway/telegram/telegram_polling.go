package telegram

import (
	"context"
	"errors"
	"time"

	"github.com/xymorphic/morph/internal/config"
)

var telegramPollingRetryDelay = time.Second

var newTelegramAPI = func(cfg config.GatewayTelegramConfig) telegramAPI {
	return newTelegramHTTPClient(cfg.BotToken)
}

func StartPolling(ctx context.Context, cfg config.GatewayConfig, service Service) error {
	return startTelegramPolling(ctx, cfg, service, newTelegramAPI(cfg.Telegram))
}

func startTelegramPolling(
	ctx context.Context,
	cfg config.GatewayConfig,
	service Service,
	api telegramAPI,
) error {
	if !cfg.Telegram.Enabled || cfg.Telegram.Mode != config.GatewayTelegramModePolling {
		<-ctx.Done()
		return nil
	}
	if api == nil {
		return errors.New("telegram api client is required")
	}

	adapter := newTelegramAdapter(cfg, service, api)
	var offset int64
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		updates, err := api.GetUpdates(ctx, offset)
		if err != nil {
			if _, ok := errors.AsType[telegramConflictError](err); ok {
				return err
			}
			if !sleepTelegramPollingRetry(ctx) {
				return nil
			}

			continue
		}

		for _, update := range updates {
			if _, err := adapter.DispatchUpdate(ctx, update); err != nil {
				log.Warn().Err(err).Int64("telegram_update_id", update.UpdateID).Msg("Telegram update failed")
			}
			if update.UpdateID >= offset {
				offset = update.UpdateID + 1
			}
		}
	}
}

func sleepTelegramPollingRetry(ctx context.Context) bool {
	timer := time.NewTimer(telegramPollingRetryDelay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
