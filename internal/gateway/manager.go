package gateway

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/guardrails"
	"github.com/xymorphic/morph/pkg/str"
)

type State string

const (
	StateDisabled State = "disabled"
	StateStopped  State = "stopped"
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateStopping State = "stopping"
	StateFailed   State = "failed"
)

type Status struct {
	State        State
	Address      string
	Port         int
	SlackMode    string
	TelegramMode string
	LastError    string
}

type Manager struct {
	mu      sync.Mutex
	opts    Options
	cancel  context.CancelFunc
	done    chan error
	state   State
	status  Status
	running bool
}

func NewManager(opts Options) *Manager {
	opts = setDefaultOptions(opts)

	return &Manager{
		opts:  opts,
		state: StateStopped,
	}
}

func (m *Manager) Start(ctx context.Context, cfg config.GatewayConfig, service AgentService) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return nil
	}
	m.state = StateStarting
	m.status = statusFromConfig(cfg, StateStarting, nil)
	m.mu.Unlock()

	if !cfg.Enabled {
		m.mu.Lock()
		m.state = StateDisabled
		m.status = statusFromConfig(cfg, StateDisabled, nil)
		m.mu.Unlock()
		return nil
	}

	components, err := newComponents(cfg, m.opts, service)
	if err != nil {
		m.setFailed(cfg, err)
		return err
	}

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	m.mu.Lock()
	m.cancel = cancel
	m.done = done
	m.running = true
	m.state = StateRunning
	m.status = statusFromConfig(cfg, StateRunning, nil)
	m.mu.Unlock()

	go func() {
		err := runComponents(runCtx, components)
		m.finish(cfg, err)
		done <- err
		close(done)
	}()

	return nil
}

func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return nil
	}
	cancel := m.cancel
	done := m.done
	m.state = StateStopping
	m.status.State = StateStopping
	m.mu.Unlock()

	cancel()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) Wait() <-chan error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.done != nil {
		return m.done
	}

	done := make(chan error)
	close(done)
	return done
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()

	status := m.status
	if status.State == "" {
		status.State = m.state
	}

	return status
}

func (m *Manager) setFailed(cfg config.GatewayConfig, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.state = StateFailed
	m.status = statusFromConfig(cfg, StateFailed, err)
}

func (m *Manager) finish(cfg config.GatewayConfig, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cancel = nil
	m.running = false
	if err != nil {
		m.state = StateFailed
		m.status = statusFromConfig(cfg, StateFailed, err)
		return
	}

	m.state = StateStopped
	m.status = statusFromConfig(cfg, StateStopped, nil)
}

func statusFromConfig(cfg config.GatewayConfig, state State, err error) Status {
	status := Status{
		State:        state,
		Address:      cfg.Address,
		Port:         cfg.Port,
		SlackMode:    cfg.Slack.Mode,
		TelegramMode: cfg.Telegram.Mode,
	}
	if err != nil {
		status.LastError = sanitizeStatusError(cfg, err)
	}

	return status
}

func sanitizeStatusError(cfg config.GatewayConfig, err error) string {
	if err == nil {
		return ""
	}

	message := err.Error()
	for _, secret := range []string{
		cfg.AuthToken,
		cfg.PairingSecret,
		cfg.Telegram.BotToken,
		cfg.Telegram.WebhookSecret,
		cfg.Slack.BotToken,
		cfg.Slack.AppToken,
		cfg.Slack.SigningSecret,
	} {
		secretValue := str.String(secret)
		secret = secretValue.Trim()
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[REDACTED]")
		}
	}

	sanitized, ok := guardrails.NewRedactor().Sanitize(message).(string)
	if ok {
		return sanitized
	}

	return message
}

type componentError struct {
	name string
	err  error
}

func (e componentError) Error() string {
	return e.name + ": " + e.err.Error()
}

func (e componentError) Unwrap() error {
	return e.err
}

type componentResult struct {
	name string
	err  error
}

func runComponents(ctx context.Context, components []component) error {
	if len(components) == 0 {
		<-ctx.Done()
		return nil
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	done := make(chan componentResult, len(components))
	var wg sync.WaitGroup
	for _, item := range components {
		item := item
		wg.Go(func() {
			if item.run == nil {
				<-runCtx.Done()
				return
			}

			done <- componentResult{name: item.name, err: item.run(runCtx)}
		})
	}

	select {
	case result := <-done:
		stoppedByContext := runCtx.Err() != nil
		cancel()
		_ = stopComponents(context.Background(), components)
		wg.Wait()
		if result.err != nil && (!stoppedByContext || !errors.Is(result.err, context.Canceled)) {
			return componentError(result)
		}

		return nil
	case <-ctx.Done():
		cancel()
		err := stopComponents(context.Background(), components)
		wg.Wait()
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}

		return nil
	}
}

func stopComponents(ctx context.Context, components []component) error {
	var result error
	for index := len(components) - 1; index >= 0; index-- {
		if components[index].stop == nil {
			continue
		}

		if err := components[index].stop(ctx); err != nil && result == nil {
			result = err
		}
	}

	return result
}
