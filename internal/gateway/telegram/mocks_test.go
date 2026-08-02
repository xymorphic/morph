package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/gateway/dispatch"
	storage "github.com/xymorphic/morph/internal/state/core"
	agentcore "github.com/xymorphic/morph/pkg/agent"
	"github.com/xymorphic/morph/pkg/gateway/pairing"
	gatewaytelegram "github.com/xymorphic/morph/pkg/gateway/telegram"
	gatewaytypes "github.com/xymorphic/morph/pkg/gateway/types"
	"github.com/xymorphic/morph/pkg/nanoid"
)

var genericCreatedSessionID = nanoid.MustFromSeed(
	storage.SessionIDPrefix,
	"telegram-created",
	"telegram-created-test",
)

type genericResponderStub struct {
	mu             sync.Mutex
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
	createErr      error
	bindingFound   bool
	pending        map[string]pairing.PendingRequest
	approved       map[string]pairing.ApprovedSender
	pairingErr     error
	called         bool
	calls          int
	created        bool
}

func (s *genericResponderStub) Respond(
	ctx context.Context,
	message string,
	opts agentcore.RespondOptions,
) (string, error) {
	s.mu.Lock()
	s.called = true
	s.calls++
	s.message = message
	s.options = opts
	s.respondContext = ctx
	s.contextErr = ctx.Err()
	reply := s.reply
	err := s.err
	s.mu.Unlock()
	if opts.OnEvent != nil {
		opts.OnEvent(agentcore.Event{Kind: agentcore.EventKindTextDelta, Channel: "reasoning", Text: "ignored"})
		opts.OnEvent(agentcore.Event{Kind: agentcore.EventKindTextDelta, Channel: "assistant", Text: "stream "})
		opts.OnEvent(agentcore.Event{Kind: agentcore.EventKindTrace})
		opts.OnEvent(agentcore.Event{Kind: agentcore.EventKindTextDelta, Channel: "assistant", Text: "delta"})
	}
	return reply, err
}

func (s *genericResponderStub) CreateSession(
	_ context.Context,
	_ string,
	opts ...storage.SessionCreateOptions,
) (storage.Session, error) {
	s.created = true
	if s.createErr != nil {
		return storage.Session{}, s.createErr
	}
	if s.createdSession.ID == "" {
		s.createdSession = storage.Session{ID: genericCreatedSessionID}
	}
	if len(opts) > 0 {
		s.createdSession.Origin = opts[0].Origin
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

func (s *genericResponderStub) SaveGatewayPairingRequest(
	_ context.Context,
	request pairing.PendingRequest,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pending == nil {
		s.pending = make(map[string]pairing.PendingRequest)
	}
	s.pending[pairingStubKey(request.Source, request.SenderID)] = request
	return nil
}

func (s *genericResponderStub) GetGatewayPairingRequest(
	_ context.Context,
	source string,
	senderID string,
) (pairing.PendingRequest, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pairingErr != nil {
		return pairing.PendingRequest{}, false, s.pairingErr
	}

	request, ok := s.pending[pairingStubKey(source, senderID)]
	return request, ok, nil
}

func (s *genericResponderStub) ListGatewayPairingRequests(
	_ context.Context,
	source string,
) ([]pairing.PendingRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pairingErr != nil {
		return nil, s.pairingErr
	}

	var requests []pairing.PendingRequest
	for _, request := range s.pending {
		if source == "" || request.Source == source {
			requests = append(requests, request)
		}
	}

	return requests, nil
}

func (s *genericResponderStub) DeleteGatewayPairingRequest(
	_ context.Context,
	source string,
	senderID string,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.pending, pairingStubKey(source, senderID))
	return nil
}

func (s *genericResponderStub) ClearGatewayPairingRequests(_ context.Context, source string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for key, request := range s.pending {
		if source == "" || request.Source == source {
			delete(s.pending, key)
		}
	}

	return nil
}

func (s *genericResponderStub) SaveGatewayPairedSender(
	_ context.Context,
	sender pairing.ApprovedSender,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.approved == nil {
		s.approved = make(map[string]pairing.ApprovedSender)
	}
	s.approved[pairingStubKey(sender.Source, sender.SenderID)] = sender
	return nil
}

func (s *genericResponderStub) GetGatewayPairedSender(
	_ context.Context,
	source string,
	senderID string,
) (pairing.ApprovedSender, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pairingErr != nil {
		return pairing.ApprovedSender{}, false, s.pairingErr
	}

	sender, ok := s.approved[pairingStubKey(source, senderID)]
	return sender, ok, nil
}

func (s *genericResponderStub) ListGatewayPairedSenders(
	_ context.Context,
	source string,
) ([]pairing.ApprovedSender, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pairingErr != nil {
		return nil, s.pairingErr
	}

	var senders []pairing.ApprovedSender
	for _, sender := range s.approved {
		if source == "" || sender.Source == source {
			senders = append(senders, sender)
		}
	}

	return senders, nil
}

func (s *genericResponderStub) DeleteGatewayPairedSender(
	_ context.Context,
	source string,
	senderID string,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.approved, pairingStubKey(source, senderID))
	return nil
}

func pairingStubKey(source string, senderID string) string {
	return source + "\x00" + senderID
}

func (s *genericResponderStub) wasCalled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.called
}

func (s *genericResponderStub) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.calls
}

func (s *genericResponderStub) receivedMessage() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.message
}

type telegramAPICall struct {
	method    string
	target    gatewaytelegram.Target
	messageID int64
	draftID   int64
	text      string
	parseMode string
	action    string
	offset    int64
	url       string
	secret    string
}

type fakeTelegramAPI struct {
	mu          sync.Mutex
	calls       []telegramAPICall
	updates     [][]gatewaytelegram.Update
	getErr      error
	getErrs     []error
	sendErr     error
	sendErrs    []error
	editErr     error
	editErrs    []error
	draftErr    error
	draftErrs   []error
	actionErr   error
	actionErrs  []error
	webhookErr  error
	nextMessage int64
	onGet       func(int64)
	onCall      func(telegramAPICall)
}

func (a *fakeTelegramAPI) GetUpdates(
	_ context.Context,
	offset int64,
) ([]gatewaytelegram.Update, error) {
	a.mu.Lock()
	a.calls = append(a.calls, telegramAPICall{method: "getUpdates", offset: offset})
	index := len(a.callsOfMethodLocked("getUpdates")) - 1
	if a.onGet != nil {
		a.onGet(offset)
	}
	if err := a.nextErrorLocked("getUpdates"); err != nil {
		a.mu.Unlock()
		return nil, err
	}
	if index >= len(a.updates) {
		a.mu.Unlock()
		return nil, nil
	}
	updates := append([]gatewaytelegram.Update(nil), a.updates[index]...)
	a.mu.Unlock()
	return updates, nil
}

func (a *fakeTelegramAPI) SetWebhook(_ context.Context, url string, secret string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	call := telegramAPICall{method: "setWebhook", url: url, secret: secret}
	a.calls = append(a.calls, call)
	if a.onCall != nil {
		a.onCall(call)
	}

	return a.webhookErr
}

func (a *fakeTelegramAPI) DeleteWebhook(_ context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	call := telegramAPICall{method: "deleteWebhook"}
	a.calls = append(a.calls, call)
	if a.onCall != nil {
		a.onCall(call)
	}

	return a.webhookErr
}

func (a *fakeTelegramAPI) SendMessage(
	_ context.Context,
	target gatewaytelegram.Target,
	message telegramText,
) (int64, error) {
	return a.recordMessageCall(
		"sendMessage",
		target,
		0,
		0,
		message,
		"",
		a.nextError("sendMessage"),
	)
}

func (a *fakeTelegramAPI) EditMessageText(
	_ context.Context,
	target gatewaytelegram.Target,
	messageID int64,
	message telegramText,
) error {
	_, err := a.recordMessageCall(
		"editMessageText",
		target,
		messageID,
		0,
		message,
		"",
		a.nextError("editMessageText"),
	)
	return err
}

func (a *fakeTelegramAPI) SendMessageDraft(
	_ context.Context,
	target gatewaytelegram.Target,
	draftID int64,
	message telegramText,
) error {
	_, err := a.recordMessageCall(
		"sendMessageDraft",
		target,
		0,
		draftID,
		message,
		"",
		a.nextError("sendMessageDraft"),
	)
	return err
}

func (a *fakeTelegramAPI) SendChatAction(
	_ context.Context,
	target gatewaytelegram.Target,
	action string,
) error {
	_, err := a.recordMessageCall(
		"sendChatAction",
		target,
		0,
		0,
		telegramText{},
		action,
		a.nextError("sendChatAction"),
	)
	return err
}

func (a *fakeTelegramAPI) nextError(method string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	return a.nextErrorLocked(method)
}

func (a *fakeTelegramAPI) nextErrorLocked(method string) error {
	switch method {
	case "getUpdates":
		if len(a.getErrs) > 0 {
			err := a.getErrs[0]
			a.getErrs = a.getErrs[1:]
			return err
		}
		return a.getErr
	case "sendMessage":
		if len(a.sendErrs) > 0 {
			err := a.sendErrs[0]
			a.sendErrs = a.sendErrs[1:]
			return err
		}
		return a.sendErr
	case "editMessageText":
		if len(a.editErrs) > 0 {
			err := a.editErrs[0]
			a.editErrs = a.editErrs[1:]
			return err
		}
		return a.editErr
	case "sendMessageDraft":
		if len(a.draftErrs) > 0 {
			err := a.draftErrs[0]
			a.draftErrs = a.draftErrs[1:]
			return err
		}
		return a.draftErr
	case "sendChatAction":
		if len(a.actionErrs) > 0 {
			err := a.actionErrs[0]
			a.actionErrs = a.actionErrs[1:]
			return err
		}
		return a.actionErr
	default:
		return nil
	}
}

func (a *fakeTelegramAPI) recordMessageCall(
	method string,
	target gatewaytelegram.Target,
	messageID int64,
	draftID int64,
	message telegramText,
	action string,
	err error,
) (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	call := telegramAPICall{
		method:    method,
		target:    target,
		messageID: messageID,
		draftID:   draftID,
		text:      message.Text,
		parseMode: message.ParseMode,
		action:    action,
	}
	a.calls = append(a.calls, call)
	if a.onCall != nil {
		a.onCall(call)
	}
	if err != nil {
		return 0, err
	}
	a.nextMessage++
	return a.nextMessage, nil
}

func (a *fakeTelegramAPI) callsOfMethod(method string) []telegramAPICall {
	a.mu.Lock()
	defer a.mu.Unlock()

	return withoutTelegramParseMode(a.callsOfMethodLocked(method))
}

func (a *fakeTelegramAPI) callsOfMethodLocked(method string) []telegramAPICall {
	var calls []telegramAPICall
	for _, call := range a.calls {
		if call.method == method {
			calls = append(calls, call)
		}
	}

	return calls
}

func (a *fakeTelegramAPI) allCalls() []telegramAPICall {
	a.mu.Lock()
	defer a.mu.Unlock()

	return withoutTelegramParseMode(a.calls)
}

func (a *fakeTelegramAPI) allCallsWithParseMode() []telegramAPICall {
	a.mu.Lock()
	defer a.mu.Unlock()

	return append([]telegramAPICall(nil), a.calls...)
}

func withoutTelegramParseMode(calls []telegramAPICall) []telegramAPICall {
	cloned := append([]telegramAPICall(nil), calls...)
	for i := range cloned {
		cloned[i].parseMode = ""
		cloned[i].text = gatewaytelegram.PlainTextFromMarkdownV2(cloned[i].text)
	}

	return cloned
}

func newWebhookHandler(cfg config.GatewayConfig, service Service) http.Handler {
	return newWebhookHandlerWithDispatchContext(context.Background(), cfg, service)
}

func newWebhookHandlerWithDispatchContext(
	dispatchCtx context.Context,
	cfg config.GatewayConfig,
	service Service,
) http.Handler {
	mux := http.NewServeMux()
	dispatcher := dispatch.New(dispatch.Options{})
	dispatcher.Start(dispatchCtx)
	mux.HandleFunc(WebhookPath, HandleWebhook(cfg, service, dispatcher))
	return mux
}

func decodeGatewayResponse(
	t requireTestingT,
	recorder *httptest.ResponseRecorder,
) gatewaytypes.RespondResponse {
	var response gatewaytypes.RespondResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return response
}

type requireTestingT interface {
	Fatalf(format string, args ...any)
}

var errTelegramTest = errors.New("telegram test error")
