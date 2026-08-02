package gateway

import (
	"context"
	"net"
	"net/http"

	"github.com/xymorphic/morph/internal/config"
	storage "github.com/xymorphic/morph/internal/state/core"
	agentcore "github.com/xymorphic/morph/pkg/agent"
	"github.com/xymorphic/morph/pkg/gateway/pairing"
	"github.com/xymorphic/morph/pkg/nanoid"
)

var (
	genericCreatedSessionID  = nanoid.MustFromSeed(storage.SessionIDPrefix, "generic-created", "generic-created-test")
	genericExistingSessionID = nanoid.MustFromSeed(storage.SessionIDPrefix, "generic-existing", "generic-existing-test")
)

type genericResponderStub struct {
	message        string
	options        agentcore.RespondOptions
	binding        storage.GatewayBinding
	savedBinding   storage.GatewayBinding
	createdSession storage.Session
	reply          string
	err            error
	respondContext context.Context
	contextErr     error
	getBindingErr  error
	saveBindingErr error
	bindingFound   bool
	called         bool
	created        bool
}

func (s *genericResponderStub) Respond(
	ctx context.Context,
	message string,
	opts agentcore.RespondOptions,
) (string, error) {
	s.called = true
	s.message = message
	s.options = opts
	s.respondContext = ctx
	s.contextErr = ctx.Err()
	if opts.OnEvent != nil {
		opts.OnEvent(agentcore.Event{Kind: agentcore.EventKindTextDelta, Channel: "reasoning", Text: "ignored"})
		opts.OnEvent(agentcore.Event{Kind: agentcore.EventKindTextDelta, Channel: "assistant", Text: "stream "})
		opts.OnEvent(agentcore.Event{Kind: agentcore.EventKindTrace})
		opts.OnEvent(agentcore.Event{Kind: agentcore.EventKindTextDelta, Channel: "assistant", Text: "delta"})
	}
	return s.reply, s.err
}

func (s *genericResponderStub) CreateSession(
	context.Context,
	string,
	...storage.SessionCreateOptions,
) (storage.Session, error) {
	s.created = true
	if s.createdSession.ID == "" {
		s.createdSession = storage.Session{ID: genericCreatedSessionID}
	}

	return s.createdSession, nil
}

func (s *genericResponderStub) Get(
	context.Context,
	string,
	storage.SessionGetOptions,
) (storage.Session, bool, error) {
	if s.bindingFound {
		return storage.Session{ID: s.binding.SessionID}, true, nil
	}

	return storage.Session{}, false, nil
}

func (s *genericResponderStub) SaveGatewayBinding(
	_ context.Context,
	binding storage.GatewayBinding,
) error {
	s.savedBinding = binding
	return s.saveBindingErr
}

func (s *genericResponderStub) GetGatewayBinding(
	context.Context,
	string,
) (storage.GatewayBinding, bool, error) {
	return s.binding, s.bindingFound, s.getBindingErr
}

func (s *genericResponderStub) SaveGatewayPairingRequest(context.Context, pairing.PendingRequest) error {
	return nil
}

func (s *genericResponderStub) GetGatewayPairingRequest(
	context.Context,
	string,
	string,
) (pairing.PendingRequest, bool, error) {
	return pairing.PendingRequest{}, false, nil
}

func (s *genericResponderStub) ListGatewayPairingRequests(context.Context, string) ([]pairing.PendingRequest, error) {
	return nil, nil
}

func (s *genericResponderStub) DeleteGatewayPairingRequest(context.Context, string, string) error {
	return nil
}

func (s *genericResponderStub) ClearGatewayPairingRequests(context.Context, string) error {
	return nil
}

func (s *genericResponderStub) SaveGatewayPairedSender(context.Context, pairing.ApprovedSender) error {
	return nil
}

func (s *genericResponderStub) GetGatewayPairedSender(
	context.Context,
	string,
	string,
) (pairing.ApprovedSender, bool, error) {
	return pairing.ApprovedSender{}, false, nil
}

func (s *genericResponderStub) ListGatewayPairedSenders(context.Context, string) ([]pairing.ApprovedSender, error) {
	return nil, nil
}

func (s *genericResponderStub) DeleteGatewayPairedSender(context.Context, string, string) error {
	return nil
}

type fakeHTTPServer struct {
	serveErr   error
	closed     bool
	onShutdown func()
	done       chan struct{}
}

func (s *fakeHTTPServer) Serve(net.Listener) error {
	if s.serveErr != nil {
		return s.serveErr
	}

	<-s.getDone()
	return http.ErrServerClosed
}

func (s *fakeHTTPServer) Shutdown(context.Context) error {
	if s.onShutdown != nil {
		s.onShutdown()
	}
	return s.Close()
}

func (s *fakeHTTPServer) Close() error {
	if !s.closed {
		s.closed = true
		close(s.getDone())
	}
	return nil
}

func (s *fakeHTTPServer) getDone() chan struct{} {
	if s.done == nil {
		s.done = make(chan struct{})
	}

	return s.done
}

func testGatewayConfig() config.GatewayConfig {
	return config.GatewayConfig{
		Enabled:   true,
		Address:   "127.0.0.1",
		Port:      0,
		AuthToken: "token",
		Telegram: config.GatewayTelegramConfig{
			Mode:     config.GatewayTelegramModePolling,
			BotToken: "telegram-token",
		},
		Slack: config.GatewaySlackConfig{
			Mode:     config.GatewaySlackModeSocket,
			BotToken: "slack-token",
			AppToken: "app-token",
		},
	}
}
