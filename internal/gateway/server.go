package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/gateway/dispatch"
	slackprovider "github.com/xymorphic/morph/internal/gateway/slack"
	telegramprovider "github.com/xymorphic/morph/internal/gateway/telegram"
)

type HTTPServer interface {
	Serve(net.Listener) error
	Shutdown(context.Context) error
	Close() error
}

type Options struct {
	Listen               func(network string, address string) (net.Listener, error)
	NewHTTPServer        func(config.GatewayConfig, AgentService) HTTPServer
	StartSlackSocket     func(context.Context, config.GatewayConfig, AgentService) error
	StartTelegramPolling func(context.Context, config.GatewayConfig, AgentService) error
	ShutdownTimeout      time.Duration
}

func setDefaultOptions(opts Options) Options {
	if opts.Listen == nil {
		opts.Listen = net.Listen
	}

	if opts.NewHTTPServer == nil {
		opts.NewHTTPServer = newHTTPServer
	}

	if opts.StartSlackSocket == nil {
		opts.StartSlackSocket = func(ctx context.Context, cfg config.GatewayConfig, service AgentService) error {
			slackService, ok := service.(slackprovider.Service)
			if !ok {
				return errors.New("slack gateway service is required")
			}

			return slackprovider.StartSocket(ctx, cfg, slackService)
		}
	}

	if opts.StartTelegramPolling == nil {
		opts.StartTelegramPolling = func(ctx context.Context, cfg config.GatewayConfig, service AgentService) error {
			telegramService, ok := service.(telegramprovider.Service)
			if !ok {
				return errors.New("telegram gateway service is required")
			}

			return telegramprovider.StartPolling(ctx, cfg, telegramService)
		}
	}

	if opts.ShutdownTimeout <= 0 {
		opts.ShutdownTimeout = 5 * time.Second
	}

	return opts
}

type component struct {
	name string
	run  func(context.Context) error
	stop func(context.Context) error
}

func newComponents(cfg config.GatewayConfig, opts Options, service AgentService) ([]component, error) {
	var components []component
	if gatewayHTTPEnabled(cfg) {
		server := opts.NewHTTPServer(cfg, service)
		address := fmt.Sprintf("%s:%d", cfg.Address, cfg.Port)
		lis, err := opts.Listen("tcp", address)
		if err != nil {
			return nil, err
		}
		if tcpAddr, ok := lis.Addr().(*net.TCPAddr); ok {
			cfg.Port = tcpAddr.Port
		}
		components = append(components, newHTTPComponent(cfg, server, lis, opts.ShutdownTimeout))
	}

	if cfg.Slack.Enabled && cfg.Slack.Mode == config.GatewaySlackModeSocket {
		components = append(components, component{
			name: "slack socket",
			run: func(ctx context.Context) error {
				return opts.StartSlackSocket(ctx, cfg, service)
			},
		})
	}

	if cfg.Telegram.Enabled && cfg.Telegram.Mode == config.GatewayTelegramModePolling {
		components = append(components, component{
			name: "telegram polling",
			run: func(ctx context.Context) error {
				return opts.StartTelegramPolling(ctx, cfg, service)
			},
		})
	}

	return components, nil
}

func gatewayHTTPEnabled(cfg config.GatewayConfig) bool {
	return cfg.Enabled
}

func newHTTPComponent(
	_ config.GatewayConfig,
	server HTTPServer,
	lis net.Listener,
	shutdownTimeout time.Duration,
) component {
	return component{
		name: "gateway http",
		run: func(context.Context) error {
			err := server.Serve(lis)
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}

			return err
		},
		stop: func(context.Context) error {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()
			if err := server.Shutdown(shutdownCtx); err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					log.Warn().Msg("Gateway HTTP graceful shutdown timed out, forcing close")
				}
				_ = server.Close()
				return err
			}

			return nil
		},
	}
}

func newHTTPServer(cfg config.GatewayConfig, service AgentService) HTTPServer {
	dispatchCtx, cancel := context.WithCancel(context.Background())
	dispatcher := dispatch.New(dispatch.Options{})
	dispatcher.Start(dispatchCtx)
	server := &http.Server{Handler: newHTTPHandlerWithDispatcher(cfg, service, dispatcher)}
	return &gatewayHTTPServer{
		server:     server,
		cancel:     cancel,
		dispatcher: dispatcher,
	}
}

func waitForComponentStop[T any](ctx context.Context, _ T) error {
	<-ctx.Done()
	return nil
}

type gatewayHTTPServer struct {
	server     *http.Server
	cancel     context.CancelFunc
	dispatcher *dispatch.Dispatcher
}

func (s *gatewayHTTPServer) Serve(lis net.Listener) error {
	return s.server.Serve(lis)
}

func (s *gatewayHTTPServer) Shutdown(ctx context.Context) error {
	err := s.server.Shutdown(ctx)
	if dispatchErr := s.dispatcher.Shutdown(ctx); dispatchErr != nil && err == nil {
		err = dispatchErr
	}
	s.cancel()

	return err
}

func (s *gatewayHTTPServer) Close() error {
	err := s.server.Close()
	s.cancel()
	s.dispatcher.Close()
	return err
}
