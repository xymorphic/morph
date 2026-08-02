package agentstub

import (
	"context"

	agentapi "github.com/wandxy/morph/internal/agent"
	"github.com/wandxy/morph/internal/execution"
	models "github.com/wandxy/morph/internal/model"
	rpcclient "github.com/wandxy/morph/internal/rpc/client"
	storage "github.com/wandxy/morph/internal/state/core"
	"github.com/wandxy/morph/internal/state/search"
	agent "github.com/wandxy/morph/pkg/agent"
	morphmsg "github.com/wandxy/morph/pkg/agent/message"
	agentsession "github.com/wandxy/morph/pkg/agent/session"
	"github.com/wandxy/morph/pkg/gateway/pairing"
	"github.com/wandxy/morph/pkg/str"
)

// AgentServiceStub is a test stub for agent service.
type AgentServiceStub struct {
	ChatInput             string
	RespondContext        context.Context
	RespondOptions        agent.RespondOptions
	Reply                 string
	Deltas                []string
	Events                []agent.Event
	RespondErr            error
	Err                   error
	CloseErr              error
	Closed                bool
	CreatedSession        storage.Session
	CreatedSessionID      string
	CreatedSessionOrigin  storage.SessionOrigin
	SavedGatewayBinding   storage.GatewayBinding
	GatewayBinding        storage.GatewayBinding
	GatewayBindingFound   bool
	PairingRequests       []pairing.PendingRequest
	PairedSenders         []pairing.ApprovedSender
	ApprovedPairing       rpcclient.GatewayPairedSender
	PairingApproved       bool
	PairingSource         string
	PairingCode           string
	RevokedPairingSource  string
	RevokedPairingSender  string
	ClearedPairingSource  string
	GatewayStatusResult   rpcclient.GatewayStatus
	GatewayStatusErr      error
	GatewayStarted        bool
	GatewayStopped        bool
	GatewayRestarted      bool
	CreateSessionOptions  rpcclient.CreateSessionOptions
	Sessions              []storage.Session
	ArchivedSessions      []storage.Session
	ListOptions           storage.SessionListOptions
	UsedSessionID         string
	UseSessionErr         error
	ArchivedSessionID     string
	ArchiveSessionErr     error
	UnarchivedSessionID   string
	UnarchivedSession     storage.Session
	UnarchiveSessionErr   error
	RenamedSessionID      string
	RenamedSessionTitle   string
	RenamedSession        storage.Session
	RenameSessionErr      error
	CurrentSessionResult  storage.Session
	CompactResult         rpcclient.CompactSessionResult
	RepairOptions         search.VectorRepairOptions
	RepairResult          search.VectorRepairResult
	SummaryResult         storage.SessionSummary
	StatusResult          rpcclient.ContextStatus
	TimelineOptions       agentapi.SessionTimelineOptions
	TimelineResult        agentapi.SessionTimeline
	ProviderList          agentapi.ProviderList
	RuntimeModelResult    rpcclient.ModelRuntime
	ModelList             agentapi.ModelList
	ModelListOptions      agentapi.ModelListOptions
	SelectedModel         models.Option
	SelectedModelID       string
	SelectedModelOptions  agentapi.ModelSelectOptions
	SelectModelErr        error
	ProviderAPIKey        string
	ProviderAPIKeyID      string
	SetProviderAPIKeyErr  error
	AutomationStoreValue  storage.AutomationStore
	AutomationStoreOK     bool
	AutomationStoreErr    error
	EnqueuedMessage       rpcclient.EnqueueMessageOptions
	QueueEntry            rpcclient.SessionQueueEntry
	ExecutionState        rpcclient.SessionExecutionState
	SetReasoningOptions   rpcclient.SetReasoningEffortOptions
	ReasoningSettings     agentsession.ReasoningSettings
	SessionEvents         []rpcclient.SessionEvent
	InterruptedRun        rpcclient.SessionActiveRun
	RunTransitioned       bool
	EditedEntryID         string
	EditedEntryContent    string
	RemovedEntryID        string
	PromotedEntryID       string
	SteeredEntryID        string
	ExecutionEnvironments []execution.EnvironmentDetails
}

func (s *AgentServiceStub) Respond(ctx context.Context, msg string, opts agent.RespondOptions) (string, error) {
	s.ChatInput = msg
	s.RespondContext = ctx
	s.RespondOptions = opts
	if opts.OnEvent != nil {
		events := s.Events
		if len(events) == 0 {
			deltas := s.Deltas
			if len(deltas) == 0 && s.Reply != "" {
				deltas = []string{s.Reply}
			}
			events = make([]agent.Event, 0, len(deltas))
			for _, delta := range deltas {
				events = append(events, agent.Event{
					Kind:    agent.EventKindTextDelta,
					Channel: "assistant",
					Text:    delta,
				})
			}
		}
		for _, event := range events {
			opts.OnEvent(event)
		}
	}
	if s.RespondErr != nil {
		return s.Reply, s.RespondErr
	}
	return s.Reply, s.Err
}

func (s *AgentServiceStub) EnqueueMessage(
	ctx context.Context,
	opts rpcclient.EnqueueMessageOptions,
) (rpcclient.SessionQueueEntry, error) {
	s.ChatInput = opts.Message
	s.RespondContext = ctx
	s.RespondOptions.SessionID = opts.SessionID
	s.EnqueuedMessage = opts
	if s.RespondErr != nil {
		return rpcclient.SessionQueueEntry{}, s.RespondErr
	}
	if s.Err != nil {
		return rpcclient.SessionQueueEntry{}, s.Err
	}
	entry := s.QueueEntry
	if entry.ID == "" {
		entry = rpcclient.SessionQueueEntry{
			ID:                    "que_stub",
			SessionID:             opts.SessionID,
			Content:               opts.Message,
			RequestedDeliveryMode: opts.DeliveryMode,
			DeliveryMode:          opts.DeliveryMode,
			Status:                agentsession.QueueStatusCompleted,
		}
	}
	s.QueueEntry = entry
	return entry, nil
}

func (s *AgentServiceStub) State(context.Context, string) (rpcclient.SessionExecutionState, error) {
	if len(s.ExecutionState.Queue) == 0 && s.QueueEntry.ID != "" {
		s.ExecutionState.Queue = []rpcclient.SessionQueueEntry{s.QueueEntry}
	}
	return s.ExecutionState, s.Err
}

func (s *AgentServiceStub) SetReasoningEffort(
	_ context.Context,
	opts rpcclient.SetReasoningEffortOptions,
) (agentsession.ReasoningSettings, error) {
	s.SetReasoningOptions = opts
	return s.ReasoningSettings, s.Err
}

func (s *AgentServiceStub) Observe(
	_ context.Context,
	_ string,
	_ int64,
	observe func(rpcclient.SessionEvent) error,
) error {
	for _, event := range s.SessionEvents {
		if err := observe(event); err != nil {
			return err
		}
	}
	return s.Err
}

func (s *AgentServiceStub) EditQueuedMessage(
	_ context.Context,
	_ string,
	entryID string,
	content string,
) (rpcclient.SessionQueueEntry, error) {
	s.EditedEntryID = entryID
	s.EditedEntryContent = content
	return s.QueueEntry, s.Err
}

func (s *AgentServiceStub) RemoveQueuedMessage(
	_ context.Context,
	_ string,
	entryID string,
) (rpcclient.SessionQueueEntry, error) {
	s.RemovedEntryID = entryID
	return s.QueueEntry, s.Err
}

func (s *AgentServiceStub) PromoteQueuedMessage(
	_ context.Context,
	_ string,
	entryID string,
) (rpcclient.SessionQueueEntry, error) {
	s.PromotedEntryID = entryID
	return s.QueueEntry, s.Err
}

func (s *AgentServiceStub) SteerQueuedMessage(
	_ context.Context,
	_ string,
	entryID string,
) (rpcclient.SessionQueueEntry, error) {
	s.SteeredEntryID = entryID
	return s.QueueEntry, s.Err
}

func (s *AgentServiceStub) InterruptRun(
	context.Context,
	string,
) (rpcclient.SessionActiveRun, bool, error) {
	return s.InterruptedRun, s.RunTransitioned, s.Err
}

func (s *AgentServiceStub) EnqueueSessionMessage(
	ctx context.Context,
	req agentsession.EnqueueRequest,
) (agentsession.QueueEntry, error) {
	return s.EnqueueMessage(ctx, rpcclient.EnqueueMessageOptions{
		SessionID:          req.SessionID,
		Message:            req.Content,
		Instruct:           req.Instruct,
		Stream:             req.Stream,
		ClientSubmissionID: req.ClientSubmissionID,
		DeliveryMode:       req.DeliveryMode,
		SteeringFallback:   req.SteeringFallback,
	})
}

func (s *AgentServiceStub) GetSessionExecutionState(
	ctx context.Context,
	sessionID string,
) (agentsession.ExecutionState, error) {
	return s.State(ctx, sessionID)
}

func (s *AgentServiceStub) SetSessionReasoningEffort(
	ctx context.Context,
	req agentsession.SetReasoningEffortRequest,
) (agentsession.ReasoningSettings, error) {
	return s.SetReasoningEffort(ctx, rpcclient.SetReasoningEffortOptions{
		SessionID:        req.SessionID,
		ExpectedProvider: req.ExpectedModel.Provider,
		ExpectedAPI:      req.ExpectedModel.API,
		ExpectedModel:    req.ExpectedModel.Model,
		Effort:           string(req.Effort),
		Reset:            req.Reset,
	})
}

func (s *AgentServiceStub) ObserveSessionEvents(
	ctx context.Context,
	sessionID string,
	cursor int64,
	observe func(agentsession.Event) error,
) error {
	return s.Observe(ctx, sessionID, cursor, observe)
}

func (s *AgentServiceStub) EditSessionQueueEntry(
	ctx context.Context,
	req agentsession.QueueEditRequest,
) (agentsession.QueueEntry, error) {
	return s.EditQueuedMessage(ctx, req.SessionID, req.EntryID, req.Content)
}

func (s *AgentServiceStub) CancelSessionQueueEntry(
	ctx context.Context,
	req agentsession.QueueMutationRequest,
) (agentsession.QueueEntry, error) {
	return s.RemoveQueuedMessage(ctx, req.SessionID, req.EntryID)
}

func (s *AgentServiceStub) PromoteSessionQueueEntry(
	ctx context.Context,
	req agentsession.QueueMutationRequest,
) (agentsession.QueueEntry, error) {
	return s.PromoteQueuedMessage(ctx, req.SessionID, req.EntryID)
}

func (s *AgentServiceStub) SteerSessionQueueEntry(
	ctx context.Context,
	req agentsession.QueueMutationRequest,
) (agentsession.QueueEntry, error) {
	return s.SteerQueuedMessage(ctx, req.SessionID, req.EntryID)
}

func (s *AgentServiceStub) InterruptSessionRun(
	ctx context.Context,
	sessionID string,
) (agentsession.ActiveRun, bool, error) {
	return s.InterruptRun(ctx, sessionID)
}

func (s *AgentServiceStub) SessionAPI() rpcclient.SessionAPI {
	return s
}

func (s *AgentServiceStub) ListExecutionEnvironments(context.Context, string) ([]execution.EnvironmentDetails, error) {
	return s.ExecutionEnvironments, s.Err
}

func (s *AgentServiceStub) ExplainExecutionEnvironment(
	_ context.Context,
	_ string,
	id string,
) (execution.EnvironmentDetails, error) {
	for _, item := range s.ExecutionEnvironments {
		if item.Status.ID == id {
			return item, s.Err
		}
	}
	return execution.EnvironmentDetails{}, s.Err
}

func (s *AgentServiceStub) ModelAPI() rpcclient.ModelAPI {
	return s
}

func (s *AgentServiceStub) GatewayAPI() rpcclient.GatewayAPI {
	return s
}

func (s *AgentServiceStub) AutomationStore(context.Context) (storage.AutomationStore, bool, error) {
	return s.AutomationStoreValue, s.AutomationStoreOK, s.AutomationStoreErr
}

func (s *AgentServiceStub) ListProviders(context.Context) (agentapi.ProviderList, error) {
	return s.ProviderList, s.Err
}

func (s *AgentServiceStub) RuntimeModel(context.Context) (rpcclient.ModelRuntime, error) {
	return s.RuntimeModelResult, s.Err
}

func (s *AgentServiceStub) ListModels(_ context.Context, opts ...agentapi.ModelListOptions) (agentapi.ModelList, error) {
	if len(opts) > 0 {
		s.ModelListOptions = opts[0]
	}

	return s.ModelList, s.Err
}

func (s *AgentServiceStub) SelectModel(_ context.Context, id string, opts ...agentapi.ModelSelectOptions) (models.Option, error) {
	s.SelectedModelID = id
	if len(opts) > 0 {
		s.SelectedModelOptions = opts[0]
	}
	if s.SelectModelErr != nil {
		return models.Option{}, s.SelectModelErr
	}
	iDValue := str.String(s.SelectedModel.ID)
	if iDValue.Trim() != "" {
		return s.SelectedModel, s.Err
	}

	return models.Option{ID: id, Current: true}, s.Err
}

func (s *AgentServiceStub) SetProviderAPIKey(_ context.Context, provider string, apiKey string) error {
	s.ProviderAPIKeyID = provider
	s.ProviderAPIKey = apiKey

	return s.SetProviderAPIKeyErr
}

func (s *AgentServiceStub) Create(ctx context.Context, id string) (storage.Session, error) {
	return s.CreateSession(ctx, id)
}

func (s *AgentServiceStub) CreateSession(
	_ context.Context,
	id string,
	opts ...storage.SessionCreateOptions,
) (storage.Session, error) {
	s.CreatedSessionID = id
	if len(opts) > 0 {
		s.CreatedSessionOrigin = opts[0].Origin
		if s.CreatedSession.Origin == (storage.SessionOrigin{}) {
			s.CreatedSession.Origin = opts[0].Origin
		}
	}

	return s.CreatedSession, s.Err
}

func (s *AgentServiceStub) SaveGatewayBinding(_ context.Context, binding storage.GatewayBinding) error {
	s.SavedGatewayBinding = binding
	return s.Err
}

func (s *AgentServiceStub) GetGatewayBinding(_ context.Context, key string) (storage.GatewayBinding, bool, error) {
	keyValue := str.String(s.GatewayBinding.Key)
	if keyValue.Trim() == "" {
		s.GatewayBinding.Key = key
	}

	return s.GatewayBinding, s.GatewayBindingFound, s.Err
}

func (s *AgentServiceStub) SaveGatewayPairingRequest(_ context.Context, request pairing.PendingRequest) error {
	s.PairingRequests = append(s.PairingRequests, request)
	return s.Err
}

func (s *AgentServiceStub) GetGatewayPairingRequest(
	_ context.Context,
	source string,
	senderID string,
) (pairing.PendingRequest, bool, error) {
	for _, request := range s.PairingRequests {
		if request.Source == source && request.SenderID == senderID {
			return request, true, s.Err
		}
	}

	return pairing.PendingRequest{}, false, s.Err
}

func (s *AgentServiceStub) ListGatewayPairingRequests(
	_ context.Context,
	source string,
) ([]pairing.PendingRequest, error) {
	sourceValue := str.String(source)
	if sourceValue.Trim() == "" {
		return s.PairingRequests, s.Err
	}

	var requests []pairing.PendingRequest
	for _, request := range s.PairingRequests {
		if request.Source == source {
			requests = append(requests, request)
		}
	}

	return requests, s.Err
}

func (s *AgentServiceStub) DeleteGatewayPairingRequest(_ context.Context, source string, senderID string) error {
	var kept []pairing.PendingRequest
	for _, request := range s.PairingRequests {
		if request.Source == source && request.SenderID == senderID {
			continue
		}

		kept = append(kept, request)
	}
	s.PairingRequests = kept
	return s.Err
}

func (s *AgentServiceStub) ClearGatewayPairingRequests(_ context.Context, source string) error {
	s.ClearedPairingSource = source
	var kept []pairing.PendingRequest
	for _, request := range s.PairingRequests {
		sourceValue2 := str.String(source)
		if sourceValue2.Trim() == "" || request.Source == source {
			continue
		}
		kept = append(kept, request)
	}
	s.PairingRequests = kept
	return s.Err
}

func (s *AgentServiceStub) SaveGatewayPairedSender(_ context.Context, sender pairing.ApprovedSender) error {
	s.PairedSenders = append(s.PairedSenders, sender)
	return s.Err
}

func (s *AgentServiceStub) GetGatewayPairedSender(
	_ context.Context,
	source string,
	senderID string,
) (pairing.ApprovedSender, bool, error) {
	for _, sender := range s.PairedSenders {
		if sender.Source == source && sender.SenderID == senderID {
			return sender, true, s.Err
		}
	}

	return pairing.ApprovedSender{}, false, s.Err
}

func (s *AgentServiceStub) ListGatewayPairedSenders(_ context.Context, source string) ([]pairing.ApprovedSender, error) {
	sourceValue3 := str.String(source)
	if sourceValue3.Trim() == "" {
		return s.PairedSenders, s.Err
	}

	var senders []pairing.ApprovedSender
	for _, sender := range s.PairedSenders {
		if sender.Source == source {
			senders = append(senders, sender)
		}
	}

	return senders, s.Err
}

func (s *AgentServiceStub) DeleteGatewayPairedSender(_ context.Context, source string, senderID string) error {
	s.RevokedPairingSource = source
	s.RevokedPairingSender = senderID
	var kept []pairing.ApprovedSender
	for _, sender := range s.PairedSenders {
		if sender.Source == source && sender.SenderID == senderID {
			continue
		}

		kept = append(kept, sender)
	}
	s.PairedSenders = kept
	return s.Err
}

func (s *AgentServiceStub) ListPairings(context.Context, string) (rpcclient.GatewayPairingList, error) {
	pending := make([]rpcclient.GatewayPairingRequest, 0, len(s.PairingRequests))
	for _, request := range s.PairingRequests {
		pending = append(pending, rpcclient.GatewayPairingRequest{
			Source:      request.Source,
			SenderID:    request.SenderID,
			DisplayName: request.DisplayName,
			CreatedAt:   request.CreatedAt,
			LastSeenAt:  request.LastSeenAt,
			ExpiresAt:   request.ExpiresAt,
		})
	}
	approved := make([]rpcclient.GatewayPairedSender, 0, len(s.PairedSenders))
	for _, sender := range s.PairedSenders {
		approved = append(approved, rpcclient.GatewayPairedSender{
			Source:      sender.Source,
			SenderID:    sender.SenderID,
			DisplayName: sender.DisplayName,
			CreatedAt:   sender.CreatedAt,
			UpdatedAt:   sender.UpdatedAt,
		})
	}

	return rpcclient.GatewayPairingList{Pending: pending, Approved: approved}, s.Err
}

func (s *AgentServiceStub) GatewayStatus(context.Context) (rpcclient.GatewayStatus, error) {
	if s.GatewayStatusErr != nil {
		return rpcclient.GatewayStatus{}, s.GatewayStatusErr
	}

	return s.GatewayStatusResult, s.Err
}

func (s *AgentServiceStub) Start(context.Context) (rpcclient.GatewayStatus, error) {
	s.GatewayStarted = true
	if s.GatewayStatusErr != nil {
		return rpcclient.GatewayStatus{}, s.GatewayStatusErr
	}

	return s.GatewayStatusResult, s.Err
}

func (s *AgentServiceStub) Stop(context.Context) (rpcclient.GatewayStatus, error) {
	s.GatewayStopped = true
	if s.GatewayStatusErr != nil {
		return rpcclient.GatewayStatus{}, s.GatewayStatusErr
	}

	return s.GatewayStatusResult, s.Err
}

func (s *AgentServiceStub) Restart(context.Context) (rpcclient.GatewayStatus, error) {
	s.GatewayRestarted = true
	if s.GatewayStatusErr != nil {
		return rpcclient.GatewayStatus{}, s.GatewayStatusErr
	}

	return s.GatewayStatusResult, s.Err
}

func (s *AgentServiceStub) ApprovePairing(
	_ context.Context,
	source string,
	code string,
) (rpcclient.GatewayPairedSender, bool, error) {
	s.PairingSource = source
	s.PairingCode = code
	return s.ApprovedPairing, s.PairingApproved, s.Err
}

func (s *AgentServiceStub) RevokePairing(_ context.Context, source string, senderID string) error {
	s.RevokedPairingSource = source
	s.RevokedPairingSender = senderID
	return s.Err
}

func (s *AgentServiceStub) ClearPendingPairings(_ context.Context, source string) error {
	s.ClearedPairingSource = source
	return s.Err
}

func (s *AgentServiceStub) CreateWithOptions(
	ctx context.Context,
	opts rpcclient.CreateSessionOptions,
) (storage.Session, error) {
	return s.CreateSessionWithOptions(ctx, opts)
}

func (s *AgentServiceStub) CreateSessionWithOptions(
	_ context.Context,
	opts rpcclient.CreateSessionOptions,
) (storage.Session, error) {
	s.CreateSessionOptions = opts
	s.CreatedSessionID = opts.ID
	return s.CreatedSession, s.Err
}

func (s *AgentServiceStub) List(ctx context.Context, opts ...rpcclient.SessionListOptions) ([]storage.Session, error) {
	return s.ListSessions(ctx, opts...)
}

func (s *AgentServiceStub) ListSessions(_ context.Context, opts ...storage.SessionListOptions) ([]storage.Session, error) {
	if len(opts) > 0 {
		s.ListOptions = opts[0]
		if opts[0].Archived != nil && *opts[0].Archived {
			return s.ArchivedSessions, s.Err
		}
	}
	return s.Sessions, s.Err
}

func (s *AgentServiceStub) Use(ctx context.Context, id string) error {
	return s.UseSession(ctx, id)
}

func (s *AgentServiceStub) UseSession(_ context.Context, id string) error {
	s.UsedSessionID = id
	if s.UseSessionErr != nil {
		return s.UseSessionErr
	}
	return s.Err
}

func (s *AgentServiceStub) Archive(ctx context.Context, id string) error {
	return s.ArchiveSession(ctx, id)
}

func (s *AgentServiceStub) ArchiveSession(_ context.Context, id string) error {
	s.ArchivedSessionID = id
	if s.ArchiveSessionErr != nil {
		return s.ArchiveSessionErr
	}
	return s.Err
}

func (s *AgentServiceStub) Unarchive(ctx context.Context, id string) (storage.Session, error) {
	return s.UnarchiveSession(ctx, id)
}

func (s *AgentServiceStub) UnarchiveSession(_ context.Context, id string) (storage.Session, error) {
	s.UnarchivedSessionID = id
	if s.UnarchiveSessionErr != nil {
		return storage.Session{}, s.UnarchiveSessionErr
	}
	iDValue2 := str.String(s.UnarchivedSession.ID)
	if iDValue2.Trim() != "" {
		return s.UnarchivedSession, s.Err
	}
	return storage.Session{ID: id}, s.Err
}

func (s *AgentServiceStub) Rename(ctx context.Context, id string, title string) (storage.Session, error) {
	return s.RenameSession(ctx, id, title)
}

func (s *AgentServiceStub) RenameSession(_ context.Context, id string, title string) (storage.Session, error) {
	s.RenamedSessionID = id
	s.RenamedSessionTitle = title
	if s.RenameSessionErr != nil {
		return storage.Session{}, s.RenameSessionErr
	}
	iDValue3 := str.String(s.RenamedSession.ID)
	if iDValue3.Trim() != "" {
		return s.RenamedSession, s.Err
	}
	return storage.Session{
		ID:          id,
		Title:       title,
		TitleSource: storage.SessionTitleSourceManual,
	}, s.Err
}

func (s *AgentServiceStub) Current(ctx context.Context) (storage.Session, error) {
	return s.CurrentSession(ctx)
}

func (s *AgentServiceStub) CurrentSession(context.Context) (storage.Session, error) {
	iDValue4 := str.String(s.CurrentSessionResult.ID)
	titleValue := str.String(s.CurrentSessionResult.Title)
	if iDValue4.Trim() != "" || titleValue.Trim() != "" {
		return s.CurrentSessionResult, s.Err
	}

	return storage.Session{}, s.Err
}

func (s *AgentServiceStub) RecallSessionSummary(context.Context, string) (storage.SessionSummary, error) {
	return s.SummaryResult, s.Err
}

func (s *AgentServiceStub) Compact(ctx context.Context, id string) (rpcclient.CompactSessionResult, error) {
	return s.CompactSession(ctx, id)
}

func (s *AgentServiceStub) CompactSession(context.Context, string) (rpcclient.CompactSessionResult, error) {
	return s.CompactResult, s.Err
}

func (s *AgentServiceStub) Repair(
	ctx context.Context,
	opts search.VectorRepairOptions,
) (search.VectorRepairResult, error) {
	return s.RepairSession(ctx, opts)
}

func (s *AgentServiceStub) RepairSession(
	_ context.Context,
	opts search.VectorRepairOptions,
) (search.VectorRepairResult, error) {
	s.RepairOptions = opts
	return s.RepairResult, s.Err
}

func (s *AgentServiceStub) Status(ctx context.Context, id string) (rpcclient.ContextStatus, error) {
	return s.GetSessionStatus(ctx, id)
}

func (s *AgentServiceStub) GetSessionStatus(context.Context, string) (rpcclient.ContextStatus, error) {
	return s.StatusResult, s.Err
}

func (s *AgentServiceStub) ContextStatus(context.Context, string) (agent.ContextStatus, error) {
	return agent.ContextStatus(s.StatusResult), s.Err
}

func (s *AgentServiceStub) Timeline(
	ctx context.Context,
	opts agentapi.SessionTimelineOptions,
) (agentapi.SessionTimeline, error) {
	return s.GetSessionTimeline(ctx, opts)
}

func (s *AgentServiceStub) GetSessionTimeline(
	_ context.Context,
	opts agentapi.SessionTimelineOptions,
) (agentapi.SessionTimeline, error) {
	s.TimelineOptions = opts
	if len(s.TimelineResult.Messages) == 0 && s.Reply != "" {
		message, err := morphmsg.NewMessage(morphmsg.RoleAssistant, s.Reply)
		if err != nil {
			return agentapi.SessionTimeline{}, err
		}
		s.TimelineResult.Messages = []agentapi.SessionTimelineMessage{{Message: message}}
	}
	return s.TimelineResult, s.Err
}

func (s *AgentServiceStub) Close() error {
	s.Closed = true
	return s.CloseErr
}

// AgentRunnerStub is a test stub for agent runner.
type AgentRunnerStub struct {
	AgentServiceStub
	StartFunc func(context.Context) error
}

func (s *AgentRunnerStub) Start(ctx context.Context) error {
	if s.StartFunc != nil {
		return s.StartFunc(ctx)
	}

	return nil
}
