package slack

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/gateway/dispatch"
	"github.com/xymorphic/morph/pkg/gateway/httpjson"
	slack "github.com/xymorphic/morph/pkg/gateway/slack"
	gatewaytypes "github.com/xymorphic/morph/pkg/gateway/types"
)

const (
	WebhookPath             = "/gateway/slack/webhook"
	maxEventsBodyBytes      = 1 << 20
	slackHTTPMaxJobAttempts = 1
)

var newSlackAPI = func(cfg config.GatewaySlackConfig) API {
	return NewHTTPClient(cfg.BotToken)
}

func HandleWebhook(cfg config.GatewayConfig, service Service, dispatcher *dispatch.Dispatcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			httpjson.WriteError(w, http.StatusMethodNotAllowed, gatewaytypes.ErrorCodeBadRequest, "method not allowed")
			return
		}
		if !cfg.Slack.Enabled || cfg.Slack.Mode != config.GatewaySlackModeHTTP {
			httpjson.WriteError(w, http.StatusNotFound, gatewaytypes.ErrorCodeBadRequest, "slack events are disabled")
			return
		}

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxEventsBodyBytes))
		if err != nil {
			httpjson.WriteError(w, http.StatusBadRequest, gatewaytypes.ErrorCodeBadRequest, "invalid request")
			return
		}
		verifier := slack.SignatureVerifier{Secret: cfg.Slack.SigningSecret}
		if err := verifier.Check(
			r.Header.Get(slack.TimestampHeader),
			r.Header.Get(slack.SignatureHeader),
			body,
		); err != nil {
			httpjson.WriteError(w, http.StatusUnauthorized, gatewaytypes.ErrorCodeUnauthorized, "unauthorized")
			return
		}

		req, err := slack.DecodeEventsRequest(body)
		if err != nil {
			httpjson.WriteError(w, http.StatusBadRequest, gatewaytypes.ErrorCodeBadRequest, "invalid request")
			return
		}
		if req.Type == slack.EventTypeURLVerification {
			httpjson.Write(w, http.StatusOK, map[string]string{"challenge": req.Challenge})
			return
		}
		inbound, ok, err := slack.NormalizeEventsRequest(req)
		if err != nil {
			httpjson.WriteError(w, http.StatusBadRequest, gatewaytypes.ErrorCodeBadRequest, "invalid request")
			return
		}
		if !ok {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok\n"))
			return
		}
		if service == nil || dispatcher == nil {
			httpjson.WriteError(w, http.StatusInternalServerError, gatewaytypes.ErrorCodeInternalError,
				"gateway request failed")
			return
		}

		_, err = enqueueSlackInbound(cfg, service, dispatcher, inbound)
		if err != nil {
			log.Warn().Err(err).Str("slack_event_id", inbound.EventID).Msg("Slack events enqueue failed")
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

func enqueueSlackInbound(
	cfg config.GatewayConfig,
	service Service,
	dispatcher *dispatch.Dispatcher,
	inbound slack.InboundMessage,
) (bool, error) {
	return dispatcher.Enqueue(dispatch.Job{
		ID:          getSlackJobID(inbound),
		MaxAttempts: slackHTTPMaxJobAttempts,
		Run: func(ctx context.Context) error {
			return dispatchInbound(ctx, cfg, service, inbound)
		},
	})
}

func dispatchInbound(ctx context.Context, cfg config.GatewayConfig, service Service, inbound slack.InboundMessage) error {
	if _, err := NewAdapter(cfg, service, newSlackAPI(cfg.Slack)).DispatchInbound(ctx, inbound); err != nil {
		log.Warn().Err(err).Str("slack_event_id", inbound.EventID).Msg("Slack gateway dispatch failed")
		return err
	}

	return nil
}

func getSlackJobID(inbound slack.InboundMessage) string {
	if inbound.EventID != "" {
		return "slack:event:" + inbound.EventID
	}
	if inbound.SocketID != "" {
		return "slack:socket:" + inbound.SocketID
	}

	return "slack:message:" + inbound.TeamID + ":" + inbound.ChannelID + ":" + inbound.MessageTS
}
