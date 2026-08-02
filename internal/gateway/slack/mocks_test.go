package slack

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	storage "github.com/xymorphic/morph/internal/state/core"
	agentcore "github.com/xymorphic/morph/pkg/agent"
	"github.com/xymorphic/morph/pkg/gateway/pairing"
	pkgslack "github.com/xymorphic/morph/pkg/gateway/slack"
)

var errSlackTest = errors.New("slack test error")

type slackServiceStub struct {
	mu sync.Mutex

	sessions        map[string]storage.Session
	bindings        map[string]storage.GatewayBinding
	pendingRequests map[string]pairing.PendingRequest
	approvedSenders map[string]pairing.ApprovedSender

	createdSession storage.Session
	reply          string
	respondErr     error
	createErr      error
	saveBindingErr error
	getPairedErr   error
	savePairingErr error

	respondCalls   int
	respondContext context.Context
	lastMessage    string
	lastOptions    agentcore.RespondOptions
}

func newSlackServiceStub() *slackServiceStub {
	return &slackServiceStub{
		sessions:        map[string]storage.Session{},
		bindings:        map[string]storage.GatewayBinding{},
		pendingRequests: map[string]pairing.PendingRequest{},
		approvedSenders: map[string]pairing.ApprovedSender{},
		createdSession:  storage.Session{ID: "ses_slack_test"},
		reply:           "final reply",
	}
}

func (s *slackServiceStub) Respond(
	ctx context.Context,
	message string,
	opts agentcore.RespondOptions,
) (string, error) {
	s.mu.Lock()
	s.respondCalls++
	s.respondContext = ctx
	s.lastMessage = message
	s.lastOptions = opts
	s.mu.Unlock()

	if opts.OnEvent != nil {
		opts.OnEvent(agentcore.Event{Kind: agentcore.EventKindTextDelta, Channel: "assistant", Text: "stream "})
		opts.OnEvent(agentcore.Event{Kind: agentcore.EventKindTextDelta, Channel: "assistant", Text: "delta"})
	}
	if s.respondErr != nil {
		return "", s.respondErr
	}

	return s.reply, nil
}

func (s *slackServiceStub) CreateSession(
	ctx context.Context,
	id string,
	opts ...storage.SessionCreateOptions,
) (storage.Session, error) {
	_ = ctx
	_ = id

	if s.createErr != nil {
		return storage.Session{}, s.createErr
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	session := s.createdSession
	if len(opts) > 0 {
		session.Origin = opts[0].Origin
	}
	s.sessions[session.ID] = session
	return session, nil
}

func (s *slackServiceStub) Get(ctx context.Context, id string, opts storage.SessionGetOptions) (storage.Session, bool, error) {
	_ = ctx
	_ = opts

	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[id]
	return session, ok, nil
}

func (s *slackServiceStub) SaveGatewayBinding(ctx context.Context, binding storage.GatewayBinding) error {
	_ = ctx

	if s.saveBindingErr != nil {
		return s.saveBindingErr
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.bindings[binding.Key] = binding
	return nil
}

func (s *slackServiceStub) GetGatewayBinding(
	ctx context.Context,
	key string,
) (storage.GatewayBinding, bool, error) {
	_ = ctx

	s.mu.Lock()
	defer s.mu.Unlock()

	binding, ok := s.bindings[key]
	return binding, ok, nil
}

func (s *slackServiceStub) SaveGatewayPairingRequest(ctx context.Context, request pairing.PendingRequest) error {
	_ = ctx

	if s.savePairingErr != nil {
		return s.savePairingErr
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.pendingRequests[pairingKey(request.Source, request.SenderID)] = request
	return nil
}

func (s *slackServiceStub) GetGatewayPairingRequest(
	ctx context.Context,
	source string,
	senderID string,
) (pairing.PendingRequest, bool, error) {
	_ = ctx

	s.mu.Lock()
	defer s.mu.Unlock()

	request, ok := s.pendingRequests[pairingKey(source, senderID)]
	return request, ok, nil
}

func (s *slackServiceStub) ListGatewayPairingRequests(
	ctx context.Context,
	source string,
) ([]pairing.PendingRequest, error) {
	_ = ctx

	s.mu.Lock()
	defer s.mu.Unlock()

	var requests []pairing.PendingRequest
	for _, request := range s.pendingRequests {
		if source == "" || request.Source == source {
			requests = append(requests, request)
		}
	}

	return requests, nil
}

func (s *slackServiceStub) DeleteGatewayPairingRequest(ctx context.Context, source string, senderID string) error {
	_ = ctx

	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.pendingRequests, pairingKey(source, senderID))
	return nil
}

func (s *slackServiceStub) ClearGatewayPairingRequests(ctx context.Context, source string) error {
	_ = ctx

	s.mu.Lock()
	defer s.mu.Unlock()

	for key, request := range s.pendingRequests {
		if source == "" || request.Source == source {
			delete(s.pendingRequests, key)
		}
	}

	return nil
}

func (s *slackServiceStub) SaveGatewayPairedSender(ctx context.Context, sender pairing.ApprovedSender) error {
	_ = ctx

	s.mu.Lock()
	defer s.mu.Unlock()

	s.approvedSenders[pairingKey(sender.Source, sender.SenderID)] = sender
	return nil
}

func (s *slackServiceStub) GetGatewayPairedSender(
	ctx context.Context,
	source string,
	senderID string,
) (pairing.ApprovedSender, bool, error) {
	_ = ctx

	if s.getPairedErr != nil {
		return pairing.ApprovedSender{}, false, s.getPairedErr
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	sender, ok := s.approvedSenders[pairingKey(source, senderID)]
	return sender, ok, nil
}

func (s *slackServiceStub) ListGatewayPairedSenders(ctx context.Context, source string) ([]pairing.ApprovedSender, error) {
	_ = ctx

	s.mu.Lock()
	defer s.mu.Unlock()

	var senders []pairing.ApprovedSender
	for _, sender := range s.approvedSenders {
		if source == "" || sender.Source == source {
			senders = append(senders, sender)
		}
	}

	return senders, nil
}

func (s *slackServiceStub) DeleteGatewayPairedSender(ctx context.Context, source string, senderID string) error {
	_ = ctx

	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.approvedSenders, pairingKey(source, senderID))
	return nil
}

func (s *slackServiceStub) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.respondCalls
}

func (s *slackServiceStub) binding(key string) (storage.GatewayBinding, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	binding, ok := s.bindings[key]
	return binding, ok
}

type slackAPICall struct {
	method string
	target pkgslack.Target
	stream pkgslack.Stream
	chunks []pkgslack.Chunk
	text   string
}

type fakeSlackAPI struct {
	mu sync.Mutex

	calls []slackAPICall

	postErr   error
	startErr  error
	appendErr error
	stopErr   error

	appendErrAfter    int
	appendErrAfterErr error
	appendCount       int
	appendDelay       time.Duration
	stream            pkgslack.Stream
}

func (a *fakeSlackAPI) PostMessage(ctx context.Context, target pkgslack.Target, text string) (string, error) {
	_ = ctx

	a.mu.Lock()
	defer a.mu.Unlock()

	a.calls = append(a.calls, slackAPICall{method: "postMessage", target: target, text: text})
	if a.postErr != nil {
		return "", a.postErr
	}
	return "post-ts", nil
}

func (a *fakeSlackAPI) StartStream(ctx context.Context, target pkgslack.Target, text string) (pkgslack.Stream, error) {
	_ = ctx

	a.mu.Lock()
	defer a.mu.Unlock()

	a.calls = append(a.calls, slackAPICall{method: "startStream", target: target, text: text})
	if a.startErr != nil {
		return pkgslack.Stream{}, a.startErr
	}
	if a.stream.TS == "" {
		a.stream = pkgslack.Stream{ChannelID: target.ChannelID, TS: "stream-ts"}
	}
	return a.stream, nil
}

func (a *fakeSlackAPI) AppendStream(ctx context.Context, stream pkgslack.Stream, chunks []pkgslack.Chunk) error {
	_ = ctx

	if a.appendDelay > 0 {
		time.Sleep(a.appendDelay)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.calls = append(a.calls, slackAPICall{
		method: "appendStream",
		stream: stream,
		chunks: getSlackRecordedChunks(chunks),
		text:   getSlackChunkText(chunks),
	})
	a.appendCount++
	if a.appendErrAfter > 0 && a.appendCount > a.appendErrAfter {
		if a.appendErrAfterErr != nil {
			return a.appendErrAfterErr
		}

		return errSlackTest
	}
	return a.appendErr
}

func getSlackRecordedChunks(chunks []pkgslack.Chunk) []pkgslack.Chunk {
	if len(chunks) == 1 && chunks[0].Type == "markdown_text" {
		return nil
	}

	return append([]pkgslack.Chunk(nil), chunks...)
}

func getSlackChunkText(chunks []pkgslack.Chunk) string {
	if len(chunks) != 1 {
		return ""
	}

	return chunks[0].Text
}

func (a *fakeSlackAPI) StopStream(ctx context.Context, stream pkgslack.Stream, text string) error {
	_ = ctx

	a.mu.Lock()
	defer a.mu.Unlock()

	a.calls = append(a.calls, slackAPICall{method: "stopStream", stream: stream, text: text})
	return a.stopErr
}

func (a *fakeSlackAPI) allCalls() []slackAPICall {
	a.mu.Lock()
	defer a.mu.Unlock()

	return append([]slackAPICall(nil), a.calls...)
}

func (a *fakeSlackAPI) callMethods() []string {
	a.mu.Lock()
	defer a.mu.Unlock()

	methods := make([]string, 0, len(a.calls))
	for _, call := range a.calls {
		methods = append(methods, call.method)
	}

	return methods
}

func pairingKey(source string, senderID string) string {
	return source + ":" + senderID
}

func approvedSlackSender(senderID string) pairing.ApprovedSender {
	return pairing.ApprovedSender{
		Source:    "slack",
		SenderID:  senderID,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
}

type fakeSocketConn struct {
	mu         sync.Mutex
	once       sync.Once
	messages   [][]byte
	writes     strings.Builder
	readErr    error
	writeErr   error
	emptyReads int
	closedCh   chan struct{}
	closed     bool
}

func newFakeSocketConn(message []byte) *fakeSocketConn {
	return &fakeSocketConn{
		messages: [][]byte{message},
		closedCh: make(chan struct{}),
	}
}

func newFakeSocketConnFrames(frames ...[]byte) *fakeSocketConn {
	return &fakeSocketConn{
		messages: frames,
		closedCh: make(chan struct{}),
	}
}

func (c *fakeSocketConn) Receive() ([]byte, error) {
	c.mu.Lock()

	if c.closed {
		c.mu.Unlock()
		return nil, errSlackTest
	}
	if c.readErr != nil {
		c.mu.Unlock()
		return nil, c.readErr
	}
	if c.emptyReads > 0 {
		c.emptyReads--
		c.mu.Unlock()
		return nil, nil
	}
	if len(c.messages) > 0 {
		message := c.messages[0]
		c.messages = c.messages[1:]
		c.mu.Unlock()
		return message, nil
	}
	c.mu.Unlock()
	<-c.closedCh
	return nil, errSlackTest
}

func (c *fakeSocketConn) Send(message []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.writeErr != nil {
		return c.writeErr
	}
	_, err := c.writes.Write(message)
	return err
}

func (c *fakeSocketConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.once.Do(func() {
		c.closed = true
		close(c.closedCh)
	})
	return nil
}

func (c *fakeSocketConn) writeString() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.writes.String()
}

func slackSocketEnvelope(t *testing.T) pkgslack.SocketEnvelope {
	t.Helper()

	var envelope pkgslack.SocketEnvelope
	require.NoError(t, json.Unmarshal(slackSocketEnvelopeBytes(t), &envelope))
	return envelope
}

func slackSocketEnvelopeBytes(t *testing.T) []byte {
	t.Helper()

	return []byte(`{
		"envelope_id":"env1",
		"type":"events_api",
		"payload":{
			"type":"event_callback",
			"team_id":"T1",
			"event_id":"Ev1",
			"event":{"type":"message","channel":"D1","channel_type":"im","user":"U1","text":"hello","ts":"100.1"}
		}
	}`)
}
