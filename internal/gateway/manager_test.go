package gateway

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/config"
)

func TestManager_StartDisabledKeepsDisabledState(t *testing.T) {
	manager := NewManager(Options{})

	require.NoError(t, manager.Start(context.Background(), config.GatewayConfig{}, nil))

	status := manager.Status()
	require.Equal(t, StateDisabled, status.State)
}

func TestManager_WaitBeforeStartIsAlreadyDone(t *testing.T) {
	manager := NewManager(Options{})

	select {
	case err := <-manager.Wait():
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("gateway wait blocked before start")
	}
	require.Equal(t, StateStopped, manager.Status().State)
}

func TestManager_StopBeforeStartDoesNothing(t *testing.T) {
	manager := NewManager(Options{})

	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, manager.Stop(stopCtx))
	require.Equal(t, StateStopped, manager.Status().State)
}

func TestManager_StartsAndStopsHTTP(t *testing.T) {
	manager := NewManager(Options{ShutdownTimeout: time.Second})
	cfg := testGatewayConfig()

	require.NoError(t, manager.Start(context.Background(), cfg, nil))
	require.Equal(t, StateRunning, manager.Status().State)

	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, manager.Stop(stopCtx))
	require.Equal(t, StateStopped, manager.Status().State)
}

func TestManager_StartIsIdempotentWhileRunning(t *testing.T) {
	listenCount := 0
	manager := NewManager(Options{
		Listen: func(network string, address string) (net.Listener, error) {
			listenCount++
			return net.Listen(network, address)
		},
	})
	cfg := testGatewayConfig()

	require.NoError(t, manager.Start(context.Background(), cfg, nil))
	require.NoError(t, manager.Start(context.Background(), cfg, nil))
	require.Equal(t, 1, listenCount)

	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, manager.Stop(stopCtx))
}

func TestManager_ReturnsListenError(t *testing.T) {
	manager := NewManager(Options{
		Listen: func(string, string) (net.Listener, error) {
			return nil, errors.New("listen failed")
		},
	})
	cfg := testGatewayConfig()

	err := manager.Start(context.Background(), cfg, nil)

	require.EqualError(t, err, "listen failed")
	status := manager.Status()
	require.Equal(t, StateFailed, status.State)
	require.Equal(t, "listen failed", status.LastError)
}

func TestManager_RedactsConfiguredSecretsFromStatusErrors(t *testing.T) {
	cfg := testGatewayConfig()
	cfg.AuthToken = "gateway-token"
	cfg.Telegram.BotToken = "123456789:ABCDEFGHIJKLMNOPQRSTUVWXYZ123456"
	cfg.Slack.AppToken = "xapp-secret-token"
	manager := NewManager(Options{
		Listen: func(string, string) (net.Listener, error) {
			return nil, errors.New("failed with gateway-token and xapp-secret-token and 123456789:ABCDEFGHIJKLMNOPQRSTUVWXYZ123456")
		},
	})

	err := manager.Start(context.Background(), cfg, nil)

	require.Error(t, err)
	status := manager.Status()
	require.Equal(t, StateFailed, status.State)
	require.Contains(t, status.LastError, "[REDACTED]")
	require.NotContains(t, status.LastError, "gateway-token")
	require.NotContains(t, status.LastError, "xapp-secret-token")
	require.NotContains(t, status.LastError, "ABCDEFGHIJKLMNOPQRSTUVWXYZ123456")
	require.Empty(t, sanitizeStatusError(cfg, nil))
}

func TestManager_ReportsHTTPServeError(t *testing.T) {
	serveErr := errors.New("serve failed")
	server := &fakeHTTPServer{serveErr: serveErr}
	manager := NewManager(Options{
		NewHTTPServer: func(config.GatewayConfig, AgentService) HTTPServer {
			return server
		},
	})
	cfg := testGatewayConfig()

	require.NoError(t, manager.Start(context.Background(), cfg, nil))

	select {
	case err := <-manager.Wait():
		require.EqualError(t, err, "gateway http: serve failed")
	case <-time.After(time.Second):
		t.Fatal("gateway did not report serve error")
	}
	require.True(t, server.closed)
	require.Equal(t, StateFailed, manager.Status().State)
}

func TestManager_ReportsUnexpectedHTTPServeStop(t *testing.T) {
	manager := NewManager(Options{
		NewHTTPServer: func(config.GatewayConfig, AgentService) HTTPServer {
			return &fakeHTTPServer{serveErr: http.ErrServerClosed}
		},
	})
	cfg := testGatewayConfig()

	require.NoError(t, manager.Start(context.Background(), cfg, nil))

	select {
	case err := <-manager.Wait():
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("gateway did not report stopped HTTP server")
	}
	require.Equal(t, StateStopped, manager.Status().State)
}

func TestManager_StartsSlackSocketAndTelegramPolling(t *testing.T) {
	slackStarted := make(chan struct{})
	telegramStarted := make(chan struct{})
	manager := NewManager(Options{
		StartSlackSocket: func(ctx context.Context, cfg config.GatewayConfig, service AgentService) error {
			require.True(t, cfg.Slack.Enabled)
			require.Equal(t, config.GatewaySlackModeSocket, cfg.Slack.Mode)
			close(slackStarted)
			<-ctx.Done()
			return nil
		},
		StartTelegramPolling: func(ctx context.Context, cfg config.GatewayConfig, service AgentService) error {
			close(telegramStarted)
			<-ctx.Done()
			return nil
		},
	})
	cfg := testGatewayConfig()
	cfg.Slack.Enabled = true
	cfg.Slack.Mode = config.GatewaySlackModeSocket
	cfg.Telegram.Enabled = true
	cfg.Telegram.Mode = config.GatewayTelegramModePolling

	require.NoError(t, manager.Start(context.Background(), cfg, nil))
	require.Eventually(t, func() bool {
		select {
		case <-slackStarted:
		default:
			return false
		}
		select {
		case <-telegramStarted:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)

	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, manager.Stop(stopCtx))
}

func TestManager_StopTreatsComponentContextCancellationAsCleanShutdown(t *testing.T) {
	manager := NewManager(Options{
		StartSlackSocket: func(ctx context.Context, cfg config.GatewayConfig, service AgentService) error {
			<-ctx.Done()
			return ctx.Err()
		},
	})
	cfg := testGatewayConfig()
	cfg.Slack.Enabled = true
	cfg.Slack.Mode = config.GatewaySlackModeSocket

	require.NoError(t, manager.Start(context.Background(), cfg, nil))
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, manager.Stop(stopCtx))
	require.Equal(t, StateStopped, manager.Status().State)

	select {
	case err := <-manager.Wait():
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("gateway wait blocked after stop")
	}
}

func TestRunComponents_TreatsLateContextCancellationAsCleanShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	componentStarted := make(chan struct{})

	err := make(chan error, 1)
	go func() {
		err <- runComponents(ctx, []component{
			{
				name: "late cancel",
				run: func(context.Context) error {
					close(componentStarted)
					<-release
					return context.Canceled
				},
			},
		})
	}()

	select {
	case <-componentStarted:
	case <-time.After(time.Second):
		t.Fatal("component did not start")
	}
	cancel()
	close(release)

	select {
	case err := <-err:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("runComponents did not stop")
	}
}

func TestRunComponents_ReturnsStopErrorAfterContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stopErr := errors.New("stop failed")
	cancel()

	err := runComponents(ctx, []component{
		{
			name: "stopped component",
			run: func(ctx context.Context) error {
				<-ctx.Done()
				return nil
			},
			stop: func(context.Context) error {
				return stopErr
			},
		},
	})

	require.ErrorIs(t, err, stopErr)
}

func TestManager_StopReturnsContextErrorWhenComponentDoesNotStop(t *testing.T) {
	release := make(chan struct{})
	manager := NewManager(Options{
		StartSlackSocket: func(context.Context, config.GatewayConfig, AgentService) error {
			<-release
			return nil
		},
	})
	cfg := testGatewayConfig()
	cfg.Slack.Enabled = true
	cfg.Slack.Mode = config.GatewaySlackModeSocket

	require.NoError(t, manager.Start(context.Background(), cfg, nil))
	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	require.ErrorIs(t, manager.Stop(stopCtx), context.DeadlineExceeded)
	close(release)
	select {
	case <-manager.Wait():
	case <-time.After(time.Second):
		t.Fatal("gateway did not stop after stuck component was released")
	}
}

func TestComponentErrorUnwrapsCause(t *testing.T) {
	cause := errors.New("cause")

	require.ErrorIs(t, componentError{name: "component", err: cause}, cause)
}
