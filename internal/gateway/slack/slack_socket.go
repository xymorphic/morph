package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/websocket"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/gateway/dispatch"
	slack "github.com/xymorphic/morph/pkg/gateway/slack"
	"github.com/xymorphic/morph/pkg/str"
)

const (
	defaultSocketDispatcherShutdownTimeout = 5 * time.Second
	defaultSocketReconnectBaseDelay        = time.Second
	defaultSocketReconnectMaxDelay         = 30 * time.Second
)

var sleepSlackSocketReconnect = sleepSocketReconnect

type SocketClient interface {
	Run(context.Context, func(context.Context, slack.SocketEnvelope) error) error
}

type socketClient struct {
	appToken string
	http     *http.Client
	baseURL  string
	dial     func(string) (socketConn, error)
}

func StartSocket(ctx context.Context, cfg config.GatewayConfig, service Service) error {
	return StartSocketWithClient(ctx, cfg, service, newSocketClient(cfg.Slack.AppToken))
}

func StartSocketWithClient(ctx context.Context, cfg config.GatewayConfig, service Service, client SocketClient) error {
	if !cfg.Slack.Enabled || cfg.Slack.Mode != config.GatewaySlackModeSocket {
		<-ctx.Done()
		return nil
	}
	if service == nil {
		return errors.New("slack gateway service is required")
	}
	if client == nil {
		return errors.New("slack socket client is required")
	}

	dispatcher := dispatch.New(dispatch.Options{})
	dispatcher.Start(ctx)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultSocketDispatcherShutdownTimeout)
		defer cancel()
		_ = dispatcher.Shutdown(shutdownCtx)
	}()

	return startSocketWithClient(ctx, cfg, service, client, dispatcher)
}

func startSocketWithClient(
	ctx context.Context,
	cfg config.GatewayConfig,
	service Service,
	client SocketClient,
	dispatcher *dispatch.Dispatcher,
) error {
	if dispatcher == nil {
		return errors.New("slack socket dispatcher is required")
	}

	reconnectAttempt := 0
	for {
		err := client.Run(ctx, func(ctx context.Context, envelope slack.SocketEnvelope) error {
			inbound, ok, err := slack.NormalizeSocketEnvelope(envelope)
			if err != nil {
				return err
			}
			if !ok {
				log.Debug().
					Str("slack_socket_type", envelope.Type).
					Str("slack_socket_envelope_id", envelope.EnvelopeID).
					Msg("Slack socket envelope ignored")
				return nil
			}

			log.Debug().
				Str("slack_event_id", inbound.EventID).
				Str("slack_channel_id", inbound.ChannelID).
				Str("slack_sender_id", inbound.SenderID).
				Str("slack_channel_type", inbound.Target.ChannelType).
				Msg("Slack socket inbound message normalized")

			_, err = enqueueSlackInbound(cfg, service, dispatcher, inbound)
			return err
		})
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			log.Warn().Err(err).Msg("Slack socket disconnected")
			reconnectAttempt++
		} else {
			reconnectAttempt = 0
		}
		if !sleepSlackSocketReconnect(ctx, socketReconnectDelay(reconnectAttempt)) {
			return nil
		}
	}
}

func newSocketClient(appToken string) *socketClient {
	appTokenValue := str.String(appToken)
	return &socketClient{
		appToken: appTokenValue.Trim(),
		http:     http.DefaultClient,
		baseURL:  defaultSlackAPIBase,
		dial: func(url string) (socketConn, error) {
			conn, err := websocket.Dial(url, "", "https://slack.com/")
			if err != nil {
				return nil, err
			}

			return websocketSocketConn{conn: conn}, nil
		},
	}
}

func (c *socketClient) Run(ctx context.Context, handler func(context.Context, slack.SocketEnvelope) error) error {
	url, err := c.openConnection(ctx)
	if err != nil {
		return err
	}
	conn, err := c.dial(url)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	stopContextClose := context.AfterFunc(ctx, func() {
		_ = conn.Close()
	})
	defer stopContextClose()

	for {
		message, err := conn.Receive()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}

			return err
		}
		if len(message) == 0 {
			continue
		}
		if len(bytes.TrimSpace(message)) == 0 {
			continue
		}
		var envelope slack.SocketEnvelope
		if err := json.Unmarshal(message, &envelope); err != nil {
			return err
		}
		log.Debug().
			Str("slack_socket_type", envelope.Type).
			Str("slack_socket_envelope_id", envelope.EnvelopeID).
			Int("slack_socket_payload_bytes", len(envelope.Payload)).
			Msg("Slack socket envelope received")
		if envelope.EnvelopeID != "" {
			ack, _ := json.Marshal(slack.SocketAck{EnvelopeID: envelope.EnvelopeID})
			if err := conn.Send(ack); err != nil {
				return err
			}
		}
		if handler != nil {
			if err := handler(ctx, envelope); err != nil {
				return err
			}
		}
	}
}

type socketConn interface {
	Receive() ([]byte, error)
	Send([]byte) error
	Close() error
}

type websocketSocketConn struct {
	conn *websocket.Conn
}

func (c websocketSocketConn) Receive() ([]byte, error) {
	var message []byte
	if err := websocket.Message.Receive(c.conn, &message); err != nil {
		return nil, err
	}

	return message, nil
}

func (c websocketSocketConn) Send(message []byte) error {
	return websocket.Message.Send(c.conn, string(message))
}

func (c websocketSocketConn) Close() error {
	return c.conn.Close()
}

func (c *socketClient) openConnection(ctx context.Context) (string, error) {
	if c.http == nil {
		c.http = http.DefaultClient
	}
	baseURL := strings.TrimRight(c.baseURL, "/")
	if baseURL == "" {
		baseURL = defaultSlackAPIBase
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/apps.connections.open", bytes.NewReader(nil))
	if err != nil {
		return "", err
	}
	appTokenValue2 := str.String(c.appToken)
	req.Header.Set("Authorization", "Bearer "+appTokenValue2.Trim())
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var body struct {
		OK    bool   `json:"ok"`
		URL   string `json:"url"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", errors.New("slack socket open failed")
	}
	if !body.OK {
		if body.Error == "" {
			body.Error = "slack socket open returned ok=false"
		}
		return "", errors.New(body.Error)
	}
	uRLValue := str.String(body.URL)
	if uRLValue.Trim() == "" {
		return "", errors.New("slack socket URL is required")
	}

	return body.URL, nil
}

func sleepSocketReconnect(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func socketReconnectDelay(attempt int) time.Duration {
	if attempt <= 1 {
		return defaultSocketReconnectBaseDelay
	}

	delay := defaultSocketReconnectBaseDelay
	for range attempt - 1 {
		delay *= 2
		if delay >= defaultSocketReconnectMaxDelay {
			return defaultSocketReconnectMaxDelay
		}
	}

	return delay
}
