package manager

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/wandxy/morph/internal/permissions"
	storage "github.com/wandxy/morph/internal/state/core"
	"github.com/wandxy/morph/internal/state/search"
	morphmsg "github.com/wandxy/morph/pkg/agent/message"
	agentsession "github.com/wandxy/morph/pkg/agent/session"
	"github.com/wandxy/morph/pkg/gateway/pairing"
	"github.com/wandxy/morph/pkg/str"
)

// Manager manages manager.
type Manager struct {
	store             storage.Store
	defaultIdleExpiry time.Duration
	archiveRetention  time.Duration
	now               func() time.Time
	workerOnce        sync.Once
}

var generateSessionID = storage.NewSessionID

// NewManager returns a state manager backed by the supplied store.
func NewManager(store storage.Store, defaultIdleExpiry, archiveRetention time.Duration) (*Manager, error) {
	if store == nil {
		return nil, errors.New("store is required")
	}
	if store.Session() == nil {
		return nil, errors.New("session store is required")
	}

	if defaultIdleExpiry <= 0 {
		return nil, errors.New("session default idle expiry must be greater than zero")
	}

	if archiveRetention <= 0 {
		return nil, errors.New("session archive retention must be greater than zero")
	}

	return &Manager{
		store:             store,
		defaultIdleExpiry: defaultIdleExpiry,
		archiveRetention:  archiveRetention,
		now:               func() time.Time { return time.Now().UTC() },
	}, nil
}

func (m *Manager) Close() error {
	if m == nil || m.store == nil {
		return nil
	}

	closer, ok := m.store.(interface{ Close() error })
	if !ok {
		return nil
	}

	return closer.Close()
}

func (m *Manager) sessions() storage.SessionStore {
	return m.store.Session()
}

func (m *Manager) inbox() (agentsession.InboxStore, error) {
	if m == nil || m.store == nil {
		return nil, errors.New("state manager is required")
	}
	store, ok := m.sessions().(agentsession.InboxStore)
	if !ok {
		return nil, errors.New("session inbox is not supported")
	}
	return store, nil
}

func (m *Manager) SupportsSessionInbox() bool {
	if m == nil || m.store == nil {
		return false
	}
	_, ok := m.sessions().(agentsession.InboxStore)
	return ok
}

func (m *Manager) SubmitMessage(
	ctx context.Context,
	req agentsession.SubmitRequest,
) (agentsession.QueueEntry, error) {
	store, err := m.inbox()
	if err != nil {
		return agentsession.QueueEntry{}, err
	}
	if _, err := m.Resolve(ctx, req.SessionID); err != nil {
		return agentsession.QueueEntry{}, err
	}
	return store.SubmitMessage(ctx, req)
}

func (m *Manager) GetExecutionState(
	ctx context.Context,
	sessionID string,
) (agentsession.ExecutionState, error) {
	store, err := m.inbox()
	if err != nil {
		return agentsession.ExecutionState{}, err
	}
	return store.GetExecutionState(ctx, sessionID)
}

func (m *Manager) ListEvents(
	ctx context.Context,
	sessionID string,
	afterCursor int64,
	limit int,
) (agentsession.EventBatch, error) {
	store, err := m.inbox()
	if err != nil {
		return agentsession.EventBatch{}, err
	}
	return store.ListEvents(ctx, sessionID, afterCursor, limit)
}

func (m *Manager) EditQueueEntry(
	ctx context.Context,
	req agentsession.QueueEditRequest,
) (agentsession.QueueEntry, error) {
	store, err := m.inbox()
	if err != nil {
		return agentsession.QueueEntry{}, err
	}
	return store.EditQueueEntry(ctx, req)
}

func (m *Manager) CancelQueueEntry(
	ctx context.Context,
	req agentsession.QueueMutationRequest,
) (agentsession.QueueEntry, error) {
	store, err := m.inbox()
	if err != nil {
		return agentsession.QueueEntry{}, err
	}
	return store.CancelQueueEntry(ctx, req)
}

func (m *Manager) PromoteQueueEntry(
	ctx context.Context,
	req agentsession.QueueMutationRequest,
) (agentsession.QueueEntry, error) {
	store, err := m.inbox()
	if err != nil {
		return agentsession.QueueEntry{}, err
	}
	return store.PromoteQueueEntry(ctx, req)
}

func (m *Manager) SteerQueueEntry(
	ctx context.Context,
	req agentsession.QueueMutationRequest,
) (agentsession.QueueEntry, error) {
	store, err := m.inbox()
	if err != nil {
		return agentsession.QueueEntry{}, err
	}
	return store.SteerQueueEntry(ctx, req)
}

func (m *Manager) ClaimNextFollowUp(
	ctx context.Context,
	req agentsession.ClaimRequest,
) (agentsession.QueueEntry, agentsession.ActiveRun, bool, error) {
	store, err := m.inbox()
	if err != nil {
		return agentsession.QueueEntry{}, agentsession.ActiveRun{}, false, err
	}
	return store.ClaimNextFollowUp(ctx, req)
}

func (m *Manager) ClaimSteering(
	ctx context.Context,
	req agentsession.SteeringClaimRequest,
) ([]agentsession.QueueEntry, error) {
	store, err := m.inbox()
	if err != nil {
		return nil, err
	}
	return store.ClaimSteering(ctx, req)
}

func (m *Manager) HasPendingSteering(
	ctx context.Context,
	req agentsession.SteeringClaimRequest,
) (bool, error) {
	store, err := m.inbox()
	if err != nil {
		return false, err
	}
	return store.HasPendingSteering(ctx, req)
}

func (m *Manager) FinishSessionRun(
	ctx context.Context,
	req agentsession.RunFinishRequest,
) (agentsession.ActiveRun, bool, error) {
	store, err := m.inbox()
	if err != nil {
		return agentsession.ActiveRun{}, false, err
	}
	return store.FinishSessionRun(ctx, req)
}

func (m *Manager) ReconcileActiveRuns(
	ctx context.Context,
	generation string,
) (agentsession.ReconcileResult, error) {
	store, err := m.inbox()
	if err != nil {
		return agentsession.ReconcileResult{}, err
	}
	return store.ReconcileActiveRuns(ctx, generation)
}

func (m *Manager) ListRunnableSessions(ctx context.Context) ([]string, error) {
	store, err := m.inbox()
	if err != nil {
		return nil, err
	}
	return store.ListRunnableSessions(ctx)
}

func (m *Manager) AutomationStore() (storage.AutomationStore, bool) {
	if m == nil || m.store == nil {
		return nil, false
	}

	return m.store.Automation()
}

func (m *Manager) PermissionStore() (permissions.ApprovalStore, bool) {
	if m == nil || m.store == nil {
		return nil, false
	}

	return m.store.Permission()
}

func (m *Manager) CreateAutomationJob(
	ctx context.Context,
	job storage.AutomationJob,
) (storage.AutomationJob, error) {
	store, ok := m.AutomationStore()
	if !ok {
		return storage.AutomationJob{}, errors.New("automation store is not supported")
	}

	return store.CreateJob(ctx, job)
}

func (m *Manager) GetAutomationJob(
	ctx context.Context,
	id string,
) (storage.AutomationJob, bool, error) {
	store, ok := m.AutomationStore()
	if !ok {
		return storage.AutomationJob{}, false, errors.New("automation store is not supported")
	}

	return store.GetJob(ctx, id)
}

func (m *Manager) ListAutomationJobs(
	ctx context.Context,
	query storage.AutomationJobQuery,
) (storage.AutomationJobResult, error) {
	store, ok := m.AutomationStore()
	if !ok {
		return storage.AutomationJobResult{}, errors.New("automation store is not supported")
	}

	return store.ListJobs(ctx, query)
}

func (m *Manager) PatchAutomationJob(
	ctx context.Context,
	patch storage.AutomationJobPatch,
) (storage.AutomationJob, error) {
	store, ok := m.AutomationStore()
	if !ok {
		return storage.AutomationJob{}, errors.New("automation store is not supported")
	}

	return store.PatchJob(ctx, patch)
}

func (m *Manager) DeleteAutomationJob(ctx context.Context, id string) error {
	store, ok := m.AutomationStore()
	if !ok {
		return errors.New("automation store is not supported")
	}

	return store.DeleteJob(ctx, id)
}

func (m *Manager) CreateAutomationRun(
	ctx context.Context,
	run storage.AutomationRun,
) (storage.AutomationRun, error) {
	store, ok := m.AutomationStore()
	if !ok {
		return storage.AutomationRun{}, errors.New("automation store is not supported")
	}

	return store.CreateRun(ctx, run)
}

func (m *Manager) FinishAutomationRun(
	ctx context.Context,
	patch storage.AutomationRunPatch,
) (storage.AutomationRun, error) {
	store, ok := m.AutomationStore()
	if !ok {
		return storage.AutomationRun{}, errors.New("automation store is not supported")
	}

	return store.FinishRun(ctx, patch)
}

func (m *Manager) ListAutomationRuns(
	ctx context.Context,
	query storage.AutomationRunQuery,
) (storage.AutomationRunResult, error) {
	store, ok := m.AutomationStore()
	if !ok {
		return storage.AutomationRunResult{}, errors.New("automation store is not supported")
	}

	return store.ListRuns(ctx, query)
}

func (m *Manager) MemoryStore() (storage.MemoryStore, bool) {
	if m == nil || m.store == nil {
		return nil, false
	}

	return m.store.Memory()
}

func (m *Manager) SupportsVectorSearch() bool {
	if m == nil || m.store == nil {
		return false
	}

	return m.store.SupportsVectorSearch()
}

func (m *Manager) SearchMemory(
	ctx context.Context,
	query storage.MemorySearchQuery,
) (storage.MemorySearchResult, error) {
	store, ok := m.MemoryStore()
	if !ok {
		return storage.MemorySearchResult{}, errors.New("memory store is not supported")
	}

	return store.SearchMemory(ctx, query)
}

func (m *Manager) ListSessionMemories(
	ctx context.Context,
	query storage.SessionMemoryQuery,
) (storage.SessionMemoriesResult, error) {
	store, ok := m.MemoryStore()
	if !ok {
		return storage.SessionMemoriesResult{}, errors.New("memory store is not supported")
	}

	return store.ListSessionMemories(ctx, query)
}

func (m *Manager) UpsertMemory(
	ctx context.Context,
	item storage.MemoryItem,
) (storage.MemoryItem, error) {
	store, ok := m.MemoryStore()
	if !ok {
		return storage.MemoryItem{}, errors.New("memory store is not supported")
	}

	return store.UpsertMemory(ctx, item)
}

func (m *Manager) PatchMemory(
	ctx context.Context,
	patch storage.MemoryPatch,
) (storage.MemoryItem, error) {
	store, ok := m.MemoryStore()
	if !ok {
		return storage.MemoryItem{}, errors.New("memory store is not supported")
	}

	return store.PatchMemory(ctx, patch)
}

func (m *Manager) DeleteMemory(ctx context.Context, req storage.MemoryDeleteRequest) error {
	store, ok := m.MemoryStore()
	if !ok {
		return errors.New("memory store is not supported")
	}

	return store.DeleteMemory(ctx, req)
}

func (m *Manager) HardDeleteMemory(ctx context.Context, req storage.MemoryDeleteRequest) error {
	store, ok := m.MemoryStore()
	if !ok {
		return errors.New("memory store is not supported")
	}

	return store.HardDeleteMemory(ctx, req)
}

func (m *Manager) TraceStore() (storage.TraceStore, bool) {
	if m == nil || m.store == nil {
		return nil, false
	}

	return m.store.Trace()
}

func (m *Manager) AppendTraceEvent(ctx context.Context, event storage.TraceEvent) (storage.TraceEvent, error) {
	store, ok := m.TraceStore()
	if !ok {
		return storage.TraceEvent{}, storage.ErrTraceStoreUnsupported
	}

	return store.AppendTraceEvent(ctx, event)
}

func (m *Manager) ListTraceEvents(ctx context.Context, query storage.TraceQuery) (storage.TraceResult, error) {
	store, ok := m.TraceStore()
	if !ok {
		return storage.TraceResult{}, storage.ErrTraceStoreUnsupported
	}

	return store.ListTraceEvents(ctx, query)
}

func (m *Manager) PruneTraceEvents(ctx context.Context, sessionID string, maxEvents int) error {
	store, ok := m.TraceStore()
	if !ok {
		return storage.ErrTraceStoreUnsupported
	}

	return store.PruneTraceEvents(ctx, sessionID, maxEvents)
}

func (m *Manager) Resolve(ctx context.Context, id string) (storage.Session, error) {
	if m == nil {
		return storage.Session{}, errors.New("state manager is required")
	}

	now := m.now().UTC()
	idValue := str.String(id)
	id = idValue.Trim()
	if id == "" {
		id = storage.DefaultSessionID
	}

	if id == storage.DefaultSessionID {
		return m.resolveDefaultSession(ctx, now)
	}

	if err := storage.ValidateSessionID(id); err != nil {
		return storage.Session{}, err
	}

	return m.getActiveSession(ctx, id)
}

func (m *Manager) runMaintenance(ctx context.Context) error {
	if m == nil {
		return errors.New("state manager is required")
	}

	now := m.now().UTC()
	if err := m.sessions().DeleteExpiredArchives(ctx, now); err != nil {
		return err
	}

	return nil
}

func (m *Manager) Start(ctx context.Context) error {
	return m.startMaintenanceWorker(ctx, time.Minute)
}

func (m *Manager) startMaintenanceWorker(ctx context.Context, interval time.Duration) error {
	if m == nil {
		return errors.New("state manager is required")
	}

	if interval <= 0 {
		interval = time.Minute
	}

	if ctx == nil || ctx.Err() != nil {
		ctx = context.Background()
	}

	if _, err := m.resolveDefaultSession(ctx, m.now().UTC()); err != nil {
		return err
	}

	if err := m.runMaintenance(ctx); err != nil {
		return err
	}

	m.workerOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					_ = m.runMaintenance(ctx)
				}
			}
		}()
	})

	return nil
}

func (m *Manager) AppendMessages(ctx context.Context, id string, messages []morphmsg.Message) error {
	if m == nil {
		return errors.New("state manager is required")
	}

	idValue2 := str.String(id)
	id = idValue2.Trim()
	if id == "" {
		return errors.New("session id is required")
	}
	if id == storage.DefaultSessionID {
		if _, err := m.resolveDefaultSession(ctx, m.now().UTC()); err != nil {
			return err
		}
	} else if err := m.checkSessionActive(ctx, id); err != nil {
		return err
	}

	return m.sessions().AppendMessages(ctx, id, storage.CloneMessages(messages))
}

func (m *Manager) Save(ctx context.Context, session storage.Session) error {
	if m == nil {
		return errors.New("state manager is required")
	}

	return m.sessions().Save(ctx, session)
}

func (m *Manager) PatchSession(
	ctx context.Context,
	id string,
	patch storage.SessionPatch,
) (storage.Session, error) {
	if m == nil {
		return storage.Session{}, errors.New("state manager is required")
	}

	idValue := str.String(id)
	id = idValue.Trim()
	if err := storage.ValidateSessionID(id); err != nil {
		return storage.Session{}, err
	}

	return m.sessions().Patch(ctx, id, patch)
}

func (m *Manager) Get(ctx context.Context, id string, opts storage.SessionGetOptions) (storage.Session, bool, error) {
	if m == nil {
		return storage.Session{}, false, errors.New("state manager is required")
	}

	idValue3 := str.String(id)
	return m.sessions().Get(ctx, idValue3.Trim(), opts)
}

func (m *Manager) GetMessages(
	ctx context.Context,
	id string,
	opts storage.MessageQueryOptions,
) ([]morphmsg.Message, error) {
	if m == nil {
		return nil, errors.New("state manager is required")
	}

	idValue4 := str.String(id)
	return m.sessions().GetMessages(ctx, idValue4.Trim(), opts)
}

func (m *Manager) GetMessagesByIDs(
	ctx context.Context,
	id string,
	messageIDs []uint,
) ([]storage.MessageRecord, error) {
	if m == nil {
		return nil, errors.New("state manager is required")
	}

	idValue5 := str.String(id)
	return m.sessions().GetMessagesByIDs(ctx, idValue5.Trim(), messageIDs)
}

func (m *Manager) GetMessageWindow(
	ctx context.Context,
	id string,
	anchorMessageID uint,
	before int,
	after int,
) ([]storage.MessageRecord, error) {
	if m == nil {
		return nil, errors.New("state manager is required")
	}

	idValue6 := str.String(id)
	return m.sessions().GetMessageWindow(ctx, idValue6.Trim(), anchorMessageID, before, after)
}

func (m *Manager) SearchMessages(
	ctx context.Context,
	id string,
	opts storage.SearchMessageOptions,
) ([]storage.SearchMessageResult, error) {
	if m == nil {
		return nil, errors.New("state manager is required")
	}

	ignoreSessionIDValue := str.String(opts.IgnoreSessionID)
	opts.IgnoreSessionID = ignoreSessionIDValue.Trim()
	idValue7 := str.String(id)
	return m.sessions().SearchMessages(ctx, idValue7.Trim(), opts)
}

func (m *Manager) RepairVectorStore(
	ctx context.Context,
	opts search.VectorRepairOptions,
) (search.VectorRepairResult, error) {
	if m == nil {
		return search.VectorRepairResult{}, errors.New("state manager is required")
	}

	repairStore, ok := m.store.(search.VectorRepairStore)
	if !ok {
		return search.VectorRepairResult{}, errors.New("session vector repair is not supported")
	}
	sessionIDValue := str.String(opts.SessionID)
	opts.SessionID = sessionIDValue.Trim()
	return repairStore.RepairVectorStore(ctx, opts)
}

func (m *Manager) CountMessages(ctx context.Context, id string, opts storage.MessageQueryOptions) (int, error) {
	if m == nil {
		return 0, errors.New("state manager is required")
	}

	idValue8 := str.String(id)
	return m.sessions().CountMessages(ctx, idValue8.Trim(), opts)
}

func (m *Manager) UpdateCheckpoints(ctx context.Context, id string, patch storage.CheckpointPatch) error {
	if m == nil {
		return errors.New("state manager is required")
	}

	idValue9 := str.String(id)
	return m.sessions().UpdateCheckpoints(ctx, idValue9.Trim(), patch)
}

func (m *Manager) GetMessage(
	ctx context.Context,
	id string,
	index int,
) (morphmsg.Message, bool, error) {
	if m == nil {
		return morphmsg.Message{}, false, errors.New("state manager is required")
	}

	idValue10 := str.String(id)
	return m.sessions().GetMessage(ctx, idValue10.Trim(), index)
}

func (m *Manager) SaveSummary(ctx context.Context, summary storage.SessionSummary) error {
	if m == nil {
		return errors.New("state manager is required")
	}

	return m.sessions().SaveSummary(ctx, summary)
}

func (m *Manager) GetSummary(ctx context.Context, sessionID string) (storage.SessionSummary, bool, error) {
	if m == nil {
		return storage.SessionSummary{}, false, errors.New("state manager is required")
	}

	sessionIDValue2 := str.String(sessionID)
	return m.sessions().GetSummary(ctx, sessionIDValue2.Trim())
}

func (m *Manager) SaveGatewayBinding(ctx context.Context, binding storage.GatewayBinding) error {
	if m == nil {
		return errors.New("state manager is required")
	}

	keyValue := str.String(binding.Key)
	binding.Key = keyValue.Trim()
	sessionIDValue3 := str.String(binding.SessionID)
	binding.SessionID = sessionIDValue3.Trim()
	return m.sessions().SaveGatewayBinding(ctx, binding)
}

func (m *Manager) GetGatewayBinding(ctx context.Context, key string) (storage.GatewayBinding, bool, error) {
	if m == nil {
		return storage.GatewayBinding{}, false, errors.New("state manager is required")
	}

	keyValue2 := str.String(key)
	return m.sessions().GetGatewayBinding(ctx, keyValue2.Trim())
}

func (m *Manager) SaveGatewayPairingRequest(ctx context.Context, request pairing.PendingRequest) error {
	if m == nil {
		return errors.New("state manager is required")
	}

	return m.sessions().SaveGatewayPairingRequest(ctx, request)
}

func (m *Manager) GetGatewayPairingRequest(
	ctx context.Context,
	source string,
	senderID string,
) (pairing.PendingRequest, bool, error) {
	if m == nil {
		return pairing.PendingRequest{}, false, errors.New("state manager is required")
	}

	return m.sessions().GetGatewayPairingRequest(ctx, source, senderID)
}

func (m *Manager) ListGatewayPairingRequests(
	ctx context.Context,
	source string,
) ([]pairing.PendingRequest, error) {
	if m == nil {
		return nil, errors.New("state manager is required")
	}

	return m.sessions().ListGatewayPairingRequests(ctx, source)
}

func (m *Manager) DeleteGatewayPairingRequest(ctx context.Context, source string, senderID string) error {
	if m == nil {
		return errors.New("state manager is required")
	}

	return m.sessions().DeleteGatewayPairingRequest(ctx, source, senderID)
}

func (m *Manager) ClearGatewayPairingRequests(ctx context.Context, source string) error {
	if m == nil {
		return errors.New("state manager is required")
	}

	return m.sessions().ClearGatewayPairingRequests(ctx, source)
}

func (m *Manager) SaveGatewayPairedSender(ctx context.Context, sender pairing.ApprovedSender) error {
	if m == nil {
		return errors.New("state manager is required")
	}

	return m.sessions().SaveGatewayPairedSender(ctx, sender)
}

func (m *Manager) GetGatewayPairedSender(
	ctx context.Context,
	source string,
	senderID string,
) (pairing.ApprovedSender, bool, error) {
	if m == nil {
		return pairing.ApprovedSender{}, false, errors.New("state manager is required")
	}

	return m.sessions().GetGatewayPairedSender(ctx, source, senderID)
}

func (m *Manager) ListGatewayPairedSenders(ctx context.Context, source string) ([]pairing.ApprovedSender, error) {
	if m == nil {
		return nil, errors.New("state manager is required")
	}

	return m.sessions().ListGatewayPairedSenders(ctx, source)
}

func (m *Manager) DeleteGatewayPairedSender(ctx context.Context, source string, senderID string) error {
	if m == nil {
		return errors.New("state manager is required")
	}

	return m.sessions().DeleteGatewayPairedSender(ctx, source, senderID)
}

func (m *Manager) DeleteSummary(ctx context.Context, sessionID string) error {
	if m == nil {
		return errors.New("state manager is required")
	}

	sessionIDValue4 := str.String(sessionID)
	return m.sessions().DeleteSummary(ctx, sessionIDValue4.Trim())
}

func (m *Manager) UpdateLastPromptTokens(ctx context.Context, id string, promptTokens int) error {
	if m == nil {
		return errors.New("state manager is required")
	}
	if promptTokens <= 0 {
		return nil
	}

	idValue11 := str.String(id)
	id = idValue11.Trim()
	if id == "" {
		return errors.New("session id is required")
	}

	session, ok, err := m.sessions().Get(ctx, id, storage.SessionGetOptions{})
	if err != nil {
		return err
	}

	if !ok {
		return errors.New("session not found")
	}

	session.LastPromptTokens = promptTokens
	return m.sessions().Save(ctx, session)
}

func (m *Manager) CreateSession(
	ctx context.Context,
	id string,
	opts ...storage.SessionCreateOptions,
) (storage.Session, error) {
	return m.CreateSessionWithOptions(ctx, id, getSessionCreateOptions(opts...))
}

func (m *Manager) CreateSessionWithOptions(
	ctx context.Context,
	id string,
	opts storage.SessionCreateOptions,
) (storage.Session, error) {
	if m == nil {
		return storage.Session{}, errors.New("state manager is required")
	}

	idValue12 := str.String(id)
	id = idValue12.Trim()
	if id == "" {
		generatedID, err := generateSessionID()
		if err != nil {
			return storage.Session{}, err
		}
		id = generatedID
	} else if err := storage.ValidateSessionID(id); err != nil {
		return storage.Session{}, err
	}

	if _, ok, err := m.sessions().Get(ctx, id, storage.SessionGetOptions{}); err != nil {
		return storage.Session{}, err
	} else if ok {
		return storage.Session{}, errors.New("session already exists")
	}

	now := m.now().UTC()
	session := storage.Session{
		CreatedAt: now,
		ID:        id,
		Origin:    normalizeSessionOrigin(opts.Origin),
		UpdatedAt: now,
	}

	if err := m.sessions().Save(ctx, session); err != nil {
		return storage.Session{}, err
	}

	return session, nil
}

func normalizeSessionOrigin(origin storage.SessionOrigin) storage.SessionOrigin {
	sourceValue := str.String(origin.Source)
	accountIDValue := str.String(origin.AccountID)
	conversationIDValue := str.String(origin.ConversationID)
	threadIDValue := str.String(origin.ThreadID)
	return storage.SessionOrigin{
		Source:         sourceValue.Trim(),
		AccountID:      accountIDValue.Trim(),
		ConversationID: conversationIDValue.Trim(),
		ThreadID:       threadIDValue.Trim(),
	}
}

func getSessionCreateOptions(opts ...storage.SessionCreateOptions) storage.SessionCreateOptions {
	if len(opts) == 0 {
		return storage.SessionCreateOptions{}
	}

	return opts[0]
}

func (m *Manager) ListSessions(ctx context.Context, opts ...storage.SessionListOptions) ([]storage.Session, error) {
	if m == nil {
		return nil, errors.New("state manager is required")
	}

	listOpts := getSessionListOptions(opts...)
	if listOpts.Archived != nil && !*listOpts.Archived {
		if _, err := m.resolveDefaultSession(ctx, m.now().UTC()); err != nil {
			return nil, err
		}
	}

	return m.sessions().List(ctx, listOpts)
}

func getSessionListOptions(opts ...storage.SessionListOptions) storage.SessionListOptions {
	if len(opts) == 0 {
		active := false
		return storage.SessionListOptions{Archived: &active}
	}

	return opts[0]
}

func (m *Manager) DeleteSession(ctx context.Context, id string) error {
	if m == nil {
		return errors.New("state manager is required")
	}

	idValue13 := str.String(id)
	id = idValue13.Trim()
	if id != "" {
		if err := storage.ValidateSessionID(id); err != nil {
			return err
		}
	}

	return m.sessions().Delete(ctx, id)
}

func (m *Manager) ArchiveSession(ctx context.Context, id string) error {
	if m == nil {
		return errors.New("state manager is required")
	}

	idValue14 := str.String(id)
	id = idValue14.Trim()
	if id == "" {
		return errors.New("session id is required")
	}
	if id == storage.DefaultSessionID {
		return errors.New("default session cannot be archived")
	}
	if err := storage.ValidateSessionID(id); err != nil {
		return err
	}

	now := m.now().UTC()
	_, err := m.sessions().Archive(ctx, id, storage.SessionArchiveRequest{
		ArchivedAt: now,
		ExpiresAt:  now.Add(m.archiveRetention),
	})
	return err
}

func (m *Manager) UnarchiveSession(ctx context.Context, id string) (storage.Session, error) {
	if m == nil {
		return storage.Session{}, errors.New("state manager is required")
	}

	idValue15 := str.String(id)
	id = idValue15.Trim()
	if id == "" {
		return storage.Session{}, errors.New("session id is required")
	}
	if err := storage.ValidateSessionID(id); err != nil {
		return storage.Session{}, err
	}

	return m.sessions().Unarchive(ctx, id)
}

func (m *Manager) RenameSession(ctx context.Context, id string, title string) (storage.Session, error) {
	if m == nil {
		return storage.Session{}, errors.New("state manager is required")
	}

	idValue16 := str.String(id)
	id = idValue16.Trim()
	if id == "" {
		return storage.Session{}, errors.New("session id is required")
	}

	title = storage.NormalizeSessionTitle(title)
	if title == "" {
		return storage.Session{}, errors.New("session title is required")
	}

	if id == storage.DefaultSessionID {
		if _, err := m.resolveDefaultSession(ctx, m.now().UTC()); err != nil {
			return storage.Session{}, err
		}
	} else if err := storage.ValidateSessionID(id); err != nil {
		return storage.Session{}, err
	} else if err := m.checkSessionActive(ctx, id); err != nil {
		return storage.Session{}, err
	}

	return m.sessions().Rename(ctx, storage.SessionRenameRequest{
		SessionID:   id,
		Title:       title,
		TitleSource: storage.SessionTitleSourceManual,
		RenamedAt:   m.now().UTC(),
	})
}

func (m *Manager) UseSession(ctx context.Context, id string) error {
	if m == nil {
		return errors.New("state manager is required")
	}

	idValue17 := str.String(id)
	id = idValue17.Trim()
	if id == storage.DefaultSessionID {
		if _, err := m.resolveDefaultSession(ctx, m.now().UTC()); err != nil {
			return err
		}
	} else if err := storage.ValidateSessionID(id); err != nil {
		return err
	} else if err := m.checkSessionActive(ctx, id); err != nil {
		return err
	}

	return m.sessions().SetCurrent(ctx, id)
}

func (m *Manager) checkSessionActive(ctx context.Context, id string) error {
	idValue18 := str.String(id)
	_, err := m.getActiveSession(ctx, idValue18.Trim())
	return err
}

func (m *Manager) getActiveSession(ctx context.Context, id string) (storage.Session, error) {
	session, ok, err := m.sessions().Get(ctx, id, storage.SessionGetOptions{})
	if err != nil {
		return storage.Session{}, err
	}
	if !ok {
		return storage.Session{}, errors.New("session not found")
	}
	if session.Archived {
		return storage.Session{}, errors.New("session is archived")
	}

	return session, nil
}

func (m *Manager) CurrentSession(ctx context.Context) (string, error) {
	if m == nil {
		return "", errors.New("state manager is required")
	}

	id, ok, err := m.sessions().Current(ctx)
	if err != nil {
		return "", err
	}

	if ok {
		return id, nil
	}

	return storage.DefaultSessionID, nil
}

func (m *Manager) resolveDefaultSession(ctx context.Context, now time.Time) (storage.Session, error) {
	session, ok, err := m.sessions().Get(ctx, storage.DefaultSessionID, storage.SessionGetOptions{})
	if err != nil {
		return storage.Session{}, err
	}

	if !ok {
		session = storage.Session{ID: storage.DefaultSessionID, UpdatedAt: now}
		if err := m.sessions().Save(ctx, session); err != nil {
			return storage.Session{}, err
		}

		return session, nil
	}

	return session, nil
}
