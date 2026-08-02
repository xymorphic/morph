package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/agent/runcontext"
	"github.com/xymorphic/morph/internal/config"
	envbudget "github.com/xymorphic/morph/internal/environment/budget"
	envtypes "github.com/xymorphic/morph/internal/environment/types"
	"github.com/xymorphic/morph/internal/guardrails"
	"github.com/xymorphic/morph/internal/memory"
	"github.com/xymorphic/morph/internal/mocks"
	models "github.com/xymorphic/morph/internal/model"
	"github.com/xymorphic/morph/internal/permissions"
	storage "github.com/xymorphic/morph/internal/state/core"
	morphtools "github.com/xymorphic/morph/internal/tools"
	"github.com/xymorphic/morph/internal/trace"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	agentprompt "github.com/xymorphic/morph/pkg/agent/prompt"
	agentsession "github.com/xymorphic/morph/pkg/agent/session"
	agenttool "github.com/xymorphic/morph/pkg/agent/tool"
	"github.com/xymorphic/morph/pkg/gateway/pairing"
)

type stateStoreStub struct {
	session         storage.Session
	sessions        map[string]storage.Session
	summaries       map[string]storage.SessionSummary
	current         string
	messages        []morphmsg.Message
	traceEvents     []storage.TraceEvent
	traceErr        error
	traceErrAt      int
	traceCalls      int
	traceAppendErr  error
	getCalls        atomic.Int32
	saveErr         error
	getErr          error
	listErr         error
	listOptions     storage.SessionListOptions
	currentErr      error
	countErr        error
	messagesErr     error
	summaryErr      error
	appendErr       error
	archive         storage.Session
	archiveErr      error
	unarchiveErr    error
	gatewayBinding  storage.GatewayBinding
	gatewayFound    bool
	permissionStore permissions.ApprovalStore
	gatewaySaveErr  error
	gatewayGetErr   error
	pairingRequests []pairing.PendingRequest
	pairedSenders   []pairing.ApprovedSender
	pairingErr      error
}

func (s *stateStoreStub) Save(_ context.Context, session storage.Session) error {
	if s.saveErr != nil {
		return s.saveErr
	}

	if s.sessions == nil {
		s.sessions = map[string]storage.Session{}
	}
	s.sessions[session.ID] = session
	if session.ID == s.session.ID || s.session.ID == "" {
		s.session = session
	}
	return nil
}

func (s *stateStoreStub) Patch(
	_ context.Context,
	id string,
	patch storage.SessionPatch,
) (storage.Session, error) {
	session, ok := s.sessions[id]
	if !ok {
		return storage.Session{}, errors.New("session not found")
	}
	if patch.ReasoningEffortOverride != nil {
		session.ReasoningEffortOverride = *patch.ReasoningEffortOverride
		s.sessions[id] = session
	}
	return session, nil
}

func (s *stateStoreStub) Get(_ context.Context, id string, _ storage.SessionGetOptions) (storage.Session, bool, error) {
	s.getCalls.Add(1)
	if s.getErr != nil {
		return storage.Session{}, false, s.getErr
	}
	if s.sessions != nil {
		session, ok := s.sessions[id]
		if ok {
			return session, true, nil
		}
	}
	if s.session.ID == "" || (id != "" && id != s.session.ID) {
		return storage.Session{}, false, nil
	}
	return s.session, true, nil
}

func (s *stateStoreStub) List(_ context.Context, opts storage.SessionListOptions) ([]storage.Session, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}

	s.listOptions = opts
	if len(s.sessions) > 0 {
		sessions := make([]storage.Session, 0, len(s.sessions))
		for _, session := range s.sessions {
			if opts.Archived != nil && session.Archived != *opts.Archived {
				continue
			}

			sessions = append(sessions, session)
		}

		return sessions, nil
	}
	if opts.Archived != nil && s.session.Archived != *opts.Archived {
		return nil, nil
	}
	return []storage.Session{s.session}, nil
}

func (s *stateStoreStub) Rename(_ context.Context, req storage.SessionRenameRequest) (storage.Session, error) {
	return storage.Session{ID: req.SessionID, Title: req.Title, TitleSource: req.TitleSource}, nil
}

func (s *stateStoreStub) Delete(context.Context, string) error { return nil }

func (s *stateStoreStub) UpdateCheckpoints(context.Context, string, storage.CheckpointPatch) error {
	return nil
}

func (s *stateStoreStub) SetCurrent(_ context.Context, id string) error {
	s.current = id
	return nil
}

func (s *stateStoreStub) Current(context.Context) (string, bool, error) {
	if s.currentErr != nil {
		return "", false, s.currentErr
	}
	if s.current != "" {
		return s.current, true, nil
	}

	return s.session.ID, s.session.ID != "", nil
}

func (s *stateStoreStub) ClearCurrent(context.Context) error {
	s.current = ""
	return nil
}

func (s *stateStoreStub) AppendMessages(context.Context, string, []morphmsg.Message) error {
	return s.appendErr
}

func (s *stateStoreStub) CountMessages(context.Context, string, storage.MessageQueryOptions) (int, error) {
	if s.countErr != nil {
		return 0, s.countErr
	}

	return len(s.messages), nil
}

func (s *stateStoreStub) GetMessage(
	context.Context,
	string,
	int,
) (morphmsg.Message, bool, error) {
	return morphmsg.Message{}, false, nil
}

func (s *stateStoreStub) GetMessages(
	_ context.Context,
	_ string,
	opts storage.MessageQueryOptions,
) ([]morphmsg.Message, error) {
	if s.messagesErr != nil {
		return nil, s.messagesErr
	}

	messages := append([]morphmsg.Message(nil), s.messages...)
	start := opts.Offset
	if start > len(messages) {
		start = len(messages)
	}
	end := len(messages)
	if opts.Limit > 0 && start+opts.Limit < end {
		end = start + opts.Limit
	}
	return messages[start:end], nil
}

func (s *stateStoreStub) GetMessagesByIDs(
	context.Context,
	string,
	[]uint,
) ([]storage.MessageRecord, error) {
	return nil, nil
}

func (s *stateStoreStub) GetMessageWindow(
	context.Context,
	string,
	uint,
	int,
	int,
) ([]storage.MessageRecord, error) {
	return nil, nil
}

func (s *stateStoreStub) SearchMessages(
	context.Context,
	string,
	storage.SearchMessageOptions,
) ([]storage.SearchMessageResult, error) {
	return nil, nil
}

func (s *stateStoreStub) ClearMessages(context.Context, string) error {
	return nil
}

func (s *stateStoreStub) SaveSummary(context.Context, storage.SessionSummary) error {
	return nil
}

func (s *stateStoreStub) GetSummary(
	_ context.Context,
	sessionID string,
) (storage.SessionSummary, bool, error) {
	if s.summaryErr != nil {
		return storage.SessionSummary{}, false, s.summaryErr
	}
	if s.summaries != nil {
		summary, ok := s.summaries[sessionID]
		return summary, ok, nil
	}

	return storage.SessionSummary{}, false, nil
}

func (s *stateStoreStub) DeleteSummary(context.Context, string) error { return nil }

func (s *stateStoreStub) SaveGatewayBinding(_ context.Context, binding storage.GatewayBinding) error {
	if s.gatewaySaveErr != nil {
		return s.gatewaySaveErr
	}

	s.gatewayBinding = binding
	s.gatewayFound = true
	return nil
}

func (s *stateStoreStub) GetGatewayBinding(_ context.Context, key string) (storage.GatewayBinding, bool, error) {
	if s.gatewayGetErr != nil {
		return storage.GatewayBinding{}, false, s.gatewayGetErr
	}
	if s.gatewayBinding.Key == key {
		return s.gatewayBinding, s.gatewayFound, nil
	}

	return storage.GatewayBinding{}, false, nil
}

func (s *stateStoreStub) SaveGatewayPairingRequest(_ context.Context, request pairing.PendingRequest) error {
	if s.pairingErr != nil {
		return s.pairingErr
	}

	s.pairingRequests = append(s.pairingRequests, request)
	return nil
}

func (s *stateStoreStub) GetGatewayPairingRequest(
	_ context.Context,
	source string,
	senderID string,
) (pairing.PendingRequest, bool, error) {
	if s.pairingErr != nil {
		return pairing.PendingRequest{}, false, s.pairingErr
	}

	for _, request := range s.pairingRequests {
		if request.Source == source && request.SenderID == senderID {
			return request, true, nil
		}
	}

	return pairing.PendingRequest{}, false, nil
}

func (s *stateStoreStub) ListGatewayPairingRequests(_ context.Context, source string) ([]pairing.PendingRequest, error) {
	if s.pairingErr != nil {
		return nil, s.pairingErr
	}

	var requests []pairing.PendingRequest
	for _, request := range s.pairingRequests {
		if source == "" || request.Source == source {
			requests = append(requests, request)
		}
	}

	return requests, nil
}

func (s *stateStoreStub) DeleteGatewayPairingRequest(_ context.Context, source string, senderID string) error {
	if s.pairingErr != nil {
		return s.pairingErr
	}

	var kept []pairing.PendingRequest
	for _, request := range s.pairingRequests {
		if request.Source == source && request.SenderID == senderID {
			continue
		}

		kept = append(kept, request)
	}
	s.pairingRequests = kept
	return nil
}

func (s *stateStoreStub) ClearGatewayPairingRequests(_ context.Context, source string) error {
	if s.pairingErr != nil {
		return s.pairingErr
	}

	var kept []pairing.PendingRequest
	for _, request := range s.pairingRequests {
		if source == "" || request.Source == source {
			continue
		}

		kept = append(kept, request)
	}
	s.pairingRequests = kept
	return nil
}

func (s *stateStoreStub) SaveGatewayPairedSender(_ context.Context, sender pairing.ApprovedSender) error {
	if s.pairingErr != nil {
		return s.pairingErr
	}

	s.pairedSenders = append(s.pairedSenders, sender)
	return nil
}

func (s *stateStoreStub) GetGatewayPairedSender(
	_ context.Context,
	source string,
	senderID string,
) (pairing.ApprovedSender, bool, error) {
	if s.pairingErr != nil {
		return pairing.ApprovedSender{}, false, s.pairingErr
	}

	for _, sender := range s.pairedSenders {
		if sender.Source == source && sender.SenderID == senderID {
			return sender, true, nil
		}
	}

	return pairing.ApprovedSender{}, false, nil
}

func (s *stateStoreStub) ListGatewayPairedSenders(_ context.Context, source string) ([]pairing.ApprovedSender, error) {
	if s.pairingErr != nil {
		return nil, s.pairingErr
	}

	var senders []pairing.ApprovedSender
	for _, sender := range s.pairedSenders {
		if source == "" || sender.Source == source {
			senders = append(senders, sender)
		}
	}

	return senders, nil
}

func (s *stateStoreStub) DeleteGatewayPairedSender(_ context.Context, source string, senderID string) error {
	if s.pairingErr != nil {
		return s.pairingErr
	}

	var kept []pairing.ApprovedSender
	for _, sender := range s.pairedSenders {
		if sender.Source == source && sender.SenderID == senderID {
			continue
		}

		kept = append(kept, sender)
	}
	s.pairedSenders = kept
	return nil
}

func (s *stateStoreStub) Session() storage.SessionStore { return s }

func (s *stateStoreStub) Automation() (storage.AutomationStore, bool) { return nil, false }

func (s *stateStoreStub) Permission() (permissions.ApprovalStore, bool) {
	return s.permissionStore, s.permissionStore != nil
}

func (s *stateStoreStub) Memory() (storage.MemoryStore, bool) { return nil, false }

func (s *stateStoreStub) Trace() (storage.TraceStore, bool) { return s, true }

func (s *stateStoreStub) SupportsVectorSearch() bool { return false }

func (s *stateStoreStub) Archive(_ context.Context, id string, req storage.SessionArchiveRequest) (storage.Session, error) {
	s.archive = storage.Session{ID: id, Archived: true, ArchivedAt: req.ArchivedAt, ExpiresAt: req.ExpiresAt}
	return storage.Session{ID: id, Archived: true, ArchivedAt: req.ArchivedAt, ExpiresAt: req.ExpiresAt}, s.archiveErr
}

func (s *stateStoreStub) Unarchive(_ context.Context, id string) (storage.Session, error) {
	if s.unarchiveErr != nil {
		return storage.Session{}, s.unarchiveErr
	}

	return storage.Session{ID: id}, nil
}

func (s *stateStoreStub) DeleteExpiredArchives(context.Context, time.Time) error {
	return nil
}

func (s *stateStoreStub) AppendTraceEvent(context.Context, storage.TraceEvent) (storage.TraceEvent, error) {
	if s.traceAppendErr != nil {
		return storage.TraceEvent{}, s.traceAppendErr
	}

	return storage.TraceEvent{}, nil
}

func (s *stateStoreStub) ListTraceEvents(
	_ context.Context,
	query storage.TraceQuery,
) (storage.TraceResult, error) {
	s.traceCalls++
	if s.traceErrAt > 0 && s.traceCalls == s.traceErrAt {
		return storage.TraceResult{}, s.traceErr
	}
	if s.traceErr != nil && s.traceErrAt == 0 {
		return storage.TraceResult{}, s.traceErr
	}
	events := append([]storage.TraceEvent(nil), s.traceEvents...)
	if query.Desc {
		reverseTraceEvents(events)
	}
	filtered := make([]storage.TraceEvent, 0, len(events))
	for _, event := range events {
		if storage.TraceEventMatchesQuery(event, query) {
			filtered = append(filtered, event)
		}
	}
	if query.Limit > 0 && len(filtered) > query.Limit {
		filtered = filtered[:query.Limit]
	}
	return storage.TraceResult{Events: filtered}, nil
}

func (s *stateStoreStub) PruneTraceEvents(context.Context, string, int) error { return nil }

var _ storage.Store = (*stateStoreStub)(nil)
var _ storage.TraceStore = (*stateStoreStub)(nil)

type sessionStoreStub struct {
	messagesByOffset map[int][]morphmsg.Message
	appendedMessages []morphmsg.Message
	err              error
	errAtGet         int
	getCalls         int
	resolveErr       error
	appendErr        error
	appendErrAt      int
	appendCalls      int
	updateErr        error
	sessionID        string
}

func (s *sessionStoreStub) Resolve(context.Context, string) (agentsession.Session, error) {
	if s.resolveErr != nil {
		return agentsession.Session{}, s.resolveErr
	}
	if s.sessionID != "" {
		return agentsession.Session{ID: s.sessionID}, nil
	}

	return agentsession.Session{ID: agentsession.DefaultID}, nil
}

func (s *sessionStoreStub) GetMessages(
	_ context.Context,
	_ string,
	query agentsession.MessageQuery,
) ([]morphmsg.Message, error) {
	s.getCalls++
	if s.errAtGet > 0 && s.getCalls == s.errAtGet {
		return nil, s.err
	}
	if s.err != nil && s.errAtGet == 0 {
		return nil, s.err
	}
	return s.messagesByOffset[query.Offset], nil
}

func (s *sessionStoreStub) AppendMessages(_ context.Context, _ string, messages []morphmsg.Message) error {
	s.appendCalls++
	if s.appendErrAt > 0 && s.appendCalls != s.appendErrAt {
		return nil
	}
	if s.appendErr == nil {
		s.appendedMessages = append(s.appendedMessages, messages...)
	}
	return s.appendErr
}

func (s *sessionStoreStub) UpdateLastPromptTokens(context.Context, string, int) error {
	return s.updateErr
}

type sessionManagerStub struct {
	ResolveFunc                func(context.Context, string) (storage.Session, error)
	GetMessagesFunc            func(context.Context, string, storage.MessageQueryOptions) ([]morphmsg.Message, error)
	AppendMessagesFunc         func(context.Context, string, []morphmsg.Message) error
	UpdateLastPromptTokensFunc func(context.Context, string, int) error
	AppendTraceEventFunc       func(context.Context, storage.TraceEvent) (storage.TraceEvent, error)
}

func (s *sessionManagerStub) Resolve(ctx context.Context, id string) (storage.Session, error) {
	return s.ResolveFunc(ctx, id)
}

func (s *sessionManagerStub) GetMessages(
	ctx context.Context,
	id string,
	query storage.MessageQueryOptions,
) ([]morphmsg.Message, error) {
	return s.GetMessagesFunc(ctx, id, query)
}

func (s *sessionManagerStub) AppendMessages(ctx context.Context, id string, messages []morphmsg.Message) error {
	return s.AppendMessagesFunc(ctx, id, messages)
}

func (s *sessionManagerStub) UpdateLastPromptTokens(ctx context.Context, id string, tokens int) error {
	return s.UpdateLastPromptTokensFunc(ctx, id, tokens)
}

func (s *sessionManagerStub) AppendTraceEvent(
	ctx context.Context,
	event storage.TraceEvent,
) (storage.TraceEvent, error) {
	return s.AppendTraceEventFunc(ctx, event)
}

type planStoreStub struct {
	sessionID string
	plan      envtypes.Plan
}

func (s *planStoreStub) CurrentPlan(string) envtypes.Plan {
	return s.plan
}

func (s *planStoreStub) HydratePlan(sessionID string, plan envtypes.Plan) {
	s.sessionID = sessionID
	s.plan = plan
}

type memoryProviderStub struct {
	name string
}

func (s *memoryProviderStub) Name() string {
	return s.name
}

func (s *memoryProviderStub) Capabilities(context.Context) (memory.Capabilities, error) {
	return memory.Capabilities{}, nil
}

func (s *memoryProviderStub) ConfigureObservability(memory.Observability) error {
	return nil
}

func (s *memoryProviderStub) Close() error {
	return nil
}

type retrievalMemoryProviderStub struct {
	memoryProviderStub
	configureErr    error
	capabilitiesErr error
	pinnedErr       error
	searchErr       error
	noSupport       bool
	pinned          []memory.MemoryItem
	search          memory.SearchResult
	pinnedRecorder  guardrails.UnsafeEvidenceRecorder
	searchRecorder  guardrails.UnsafeEvidenceRecorder
}

func (s *retrievalMemoryProviderStub) Name() string {
	return "memory"
}

func (s *retrievalMemoryProviderStub) Capabilities(context.Context) (memory.Capabilities, error) {
	if s.capabilitiesErr != nil {
		return memory.Capabilities{}, s.capabilitiesErr
	}
	if s.noSupport {
		return memory.Capabilities{}, nil
	}

	return memory.Capabilities{SupportsPinned: true, SupportsSearch: true}, nil
}

func (s *retrievalMemoryProviderStub) ConfigureObservability(memory.Observability) error {
	return s.configureErr
}

func (s *retrievalMemoryProviderStub) LoadPinned(
	ctx context.Context,
	_ memory.SearchQuery,
) ([]memory.MemoryItem, error) {
	s.pinnedRecorder = guardrails.UnsafeEvidenceRecorderFromContext(ctx)
	if s.pinnedErr != nil {
		return nil, s.pinnedErr
	}

	return s.pinned, nil
}

func (s *retrievalMemoryProviderStub) Search(
	ctx context.Context,
	_ memory.SearchQuery,
) (memory.SearchResult, error) {
	s.searchRecorder = guardrails.UnsafeEvidenceRecorderFromContext(ctx)
	if s.searchErr != nil {
		return memory.SearchResult{}, s.searchErr
	}

	return s.search, nil
}

type toolGroupRegistryStub struct {
	groups      []agenttool.Group
	definitions []agenttool.Definition
	resolveErr  error
	invoke      func(context.Context, agenttool.Call) morphmsg.Message
}

func (s *toolGroupRegistryStub) Resolve(agenttool.Policy) ([]agenttool.Definition, error) {
	if s.resolveErr != nil {
		return nil, s.resolveErr
	}

	return s.definitions, nil
}

func (s *toolGroupRegistryStub) Invoke(ctx context.Context, call agenttool.Call) morphmsg.Message {
	if s.invoke != nil {
		return s.invoke(ctx, call)
	}

	return morphmsg.Message{}
}

func (s *toolGroupRegistryStub) ListGroups() []agenttool.Group {
	return s.groups
}

type memoryFlushToolRegistryStub struct {
	definitions []agenttool.Definition
	resolveErr  error
}

func (s *memoryFlushToolRegistryStub) Resolve(agenttool.Policy) ([]agenttool.Definition, error) {
	if s.resolveErr != nil {
		return nil, s.resolveErr
	}

	return s.definitions, nil
}

func (s *memoryFlushToolRegistryStub) Invoke(context.Context, agenttool.Call) morphmsg.Message {
	return morphmsg.Message{}
}

type environmentToolRegistryStub struct {
	invoke func(context.Context, morphtools.Call) (morphtools.Result, error)
}

func (s *environmentToolRegistryStub) GetGroup(string) (morphtools.Group, bool) {
	return morphtools.Group{}, false
}

func (s *environmentToolRegistryStub) List() morphtools.Definitions {
	return nil
}

func (s *environmentToolRegistryStub) ListGroups() []morphtools.Group {
	return nil
}

func (s *environmentToolRegistryStub) Resolve(morphtools.Policy) (morphtools.Definitions, error) {
	return nil, nil
}

func (s *environmentToolRegistryStub) Invoke(
	ctx context.Context,
	call morphtools.Call,
) (morphtools.Result, error) {
	return s.invoke(ctx, call)
}

type turnPromptProviderStub struct {
	instructions agentprompt.Instructions
	err          error
}

func (s *turnPromptProviderStub) LoadBaseInstructions(
	context.Context,
	agentprompt.RunContext,
) (agentprompt.Instructions, error) {
	return s.instructions, s.err
}

func (s *turnPromptProviderStub) BuildEnvironmentInstruction(
	context.Context,
	agentprompt.EnvironmentInput,
) (agentprompt.Instruction, error) {
	return agentprompt.Instruction{}, nil
}

type turnRuntimeSourceStub struct {
	traceSession    trace.Session
	iterationBudget envbudget.IterationBudget
	plan            envtypes.Plan
	hydrated        envtypes.Plan
}

func (s *turnRuntimeSourceStub) NewTraceSessionForRun(runcontext.Context) trace.Session {
	return s.traceSession
}

func (s *turnRuntimeSourceStub) NewIterationBudget() envbudget.IterationBudget {
	return s.iterationBudget
}

func (s *turnRuntimeSourceStub) CurrentPlan(string) envtypes.Plan {
	return s.plan
}

func (s *turnRuntimeSourceStub) HydratePlan(_ string, plan envtypes.Plan) {
	s.hydrated = plan
}

type badTurnEnvironment struct{}

func (badTurnEnvironment) Tools(string)      {}
func (badTurnEnvironment) ToolPolicy(string) {}

func newTurnRunTestSubject(
	client models.Client,
	sessionStore *sessionStoreStub,
	registry *toolGroupRegistryStub,
	budget envbudget.IterationBudget,
) *Turn {
	if sessionStore == nil {
		sessionStore = &sessionStoreStub{messagesByOffset: map[int][]morphmsg.Message{}}
	}
	if registry == nil {
		registry = &toolGroupRegistryStub{}
	}
	if budget.Remaining() < 0 {
		budget = envbudget.New(1)
	}
	env := &mocks.EnvironmentStub{IterationBudget: budget}
	return NewTurnWithSessionStore(
		&config.Config{Models: config.ModelsConfig{Main: config.MainModelConfig{Name: "model", API: models.APIOpenAIResponses}}},
		client,
		nil,
		&stateStoreStub{},
		sessionStore,
		nil,
		registry,
		agenttool.Policy{},
		&turnPromptProviderStub{},
		env,
		env,
		env,
		env,
		env,
		env,
		nil,
	)
}

func toolExecutionTestContent(t *testing.T, message morphmsg.Message) map[string]any {
	t.Helper()

	var content map[string]any
	require.NoError(t, json.Unmarshal([]byte(message.Content), &content))
	return content
}

func toolExecutionTestMessage(toolCall models.ToolCall, content string) morphmsg.Message {
	return morphmsg.Message{
		Role:       morphmsg.RoleTool,
		Name:       toolCall.Name,
		ToolCallID: toolCall.ID,
		Content:    content,
	}
}

func toolExecutionTestMessageIDs(messages []morphmsg.Message) []string {
	ids := make([]string, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.ToolCallID)
	}

	return ids
}

func memoryRetrievalTestEventTypes(events []trace.Event) []string {
	result := make([]string, 0, len(events))
	for _, event := range events {
		result = append(result, event.Type)
	}

	return result
}

func agentTestSessionIDs(sessions []storage.Session) []string {
	ids := make([]string, 0, len(sessions))
	for _, session := range sessions {
		ids = append(ids, session.ID)
	}

	return ids
}
