package manager

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wandxy/morph/internal/permissions"
	storage "github.com/wandxy/morph/internal/state/core"
	storagemock "github.com/wandxy/morph/internal/state/mock"
	"github.com/wandxy/morph/internal/state/search"
	storagememory "github.com/wandxy/morph/internal/state/storememory"
	morphmsg "github.com/wandxy/morph/pkg/agent/message"
	"github.com/wandxy/morph/pkg/gateway/pairing"
	"github.com/wandxy/morph/pkg/nanoid"
)

var (
	testSessionA       = nanoid.MustFromSeed(storage.SessionIDPrefix, "project-a", "SessionTestSeedValue123")
	testMissingSession = nanoid.MustFromSeed(storage.SessionIDPrefix, "missing", "SessionTestSeedValue123")
	testAutomationJob  = nanoid.MustFromSeed(
		storage.AutomationJobIDPrefix,
		"daily-headlines",
		"AutomationJobSeedValue123",
	)
	testAutomationRun = nanoid.MustFromSeed(
		storage.AutomationRunIDPrefix,
		"daily-headlines-run",
		"AutomationRunSeedValue123",
	)
)

type storeWithoutSession struct{}

func (storeWithoutSession) Session() storage.SessionStore {
	return nil
}

func (storeWithoutSession) Memory() (storage.MemoryStore, bool) {
	return nil, false
}

func (storeWithoutSession) Automation() (storage.AutomationStore, bool) {
	return nil, false
}

func (storeWithoutSession) Permission() (permissions.ApprovalStore, bool) { return nil, false }

func (storeWithoutSession) Trace() (storage.TraceStore, bool) {
	return nil, false
}

func (storeWithoutSession) SupportsVectorSearch() bool {
	return false
}

type closableStore struct {
	*storagemock.Store
	closeErr error
	closed   bool
}

func (s *closableStore) Close() error {
	s.closed = true
	return s.closeErr
}

func TestManager_ResolveChatSessionCreatesDefault(t *testing.T) {
	manager, err := NewManager(storagememory.NewStore(), time.Hour, 24*time.Hour)
	require.NoError(t, err)

	session, err := manager.Resolve(context.Background(), "")

	require.NoError(t, err)
	require.Equal(t, storage.DefaultSessionID, session.ID)
}

func TestManager_Close(t *testing.T) {
	var nilManager *Manager
	require.NoError(t, nilManager.Close())
	require.NoError(t, (&Manager{}).Close())

	manager, err := NewManager(&storagemock.Store{}, time.Hour, 24*time.Hour)
	require.NoError(t, err)
	require.NoError(t, manager.Close())

	store := &closableStore{Store: &storagemock.Store{}}
	manager, err = NewManager(store, time.Hour, 24*time.Hour)
	require.NoError(t, err)
	require.NoError(t, manager.Close())
	require.True(t, store.closed)

	store = &closableStore{Store: &storagemock.Store{}, closeErr: errors.New("close failed")}
	manager, err = NewManager(store, time.Hour, 24*time.Hour)
	require.NoError(t, err)
	require.EqualError(t, manager.Close(), "close failed")
	require.True(t, store.closed)
}

func TestManager_MemoryStore(t *testing.T) {
	store := storagememory.NewStore()
	manager, err := NewManager(store, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	memoryStore, ok := manager.MemoryStore()
	require.True(t, ok)
	require.Same(t, store, memoryStore)

	manager, err = NewManager(&storagemock.Store{}, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	memoryStore, ok = manager.MemoryStore()
	require.False(t, ok)
	require.Nil(t, memoryStore)
}

func TestManager_AutomationStore(t *testing.T) {
	store := storagememory.NewStore()
	manager, err := NewManager(store, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	automationStore, ok := manager.AutomationStore()
	require.True(t, ok)
	require.Same(t, store, automationStore)

	manager, err = NewManager(&storagemock.Store{}, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	automationStore, ok = manager.AutomationStore()
	require.False(t, ok)
	require.Nil(t, automationStore)
}

func TestManager_GatewayBindingOperations(t *testing.T) {
	manager, err := NewManager(storagememory.NewStore(), time.Hour, 24*time.Hour)
	require.NoError(t, err)
	_, err = manager.Resolve(context.Background(), storage.DefaultSessionID)
	require.NoError(t, err)
	binding := storage.GatewayBinding{
		Key:       " telegram::123: ",
		SessionID: " " + storage.DefaultSessionID + " ",
	}

	require.NoError(t, manager.SaveGatewayBinding(context.Background(), binding))
	found, ok, err := manager.GetGatewayBinding(context.Background(), " telegram::123: ")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "telegram::123:", found.Key)
	require.Equal(t, storage.DefaultSessionID, found.SessionID)

	require.EqualError(t,
		(*Manager)(nil).SaveGatewayBinding(context.Background(), binding),
		"state manager is required",
	)
	_, _, err = (*Manager)(nil).GetGatewayBinding(context.Background(), "telegram::123:")
	require.EqualError(t, err, "state manager is required")
}

func TestManager_GatewayPairingOperations(t *testing.T) {
	manager, err := NewManager(storagememory.NewStore(), time.Hour, 24*time.Hour)
	require.NoError(t, err)
	request := pairing.PendingRequest{Source: "telegram", SenderID: "123"}
	sender := pairing.ApprovedSender{Source: "telegram", SenderID: "123"}

	require.NoError(t, manager.SaveGatewayPairingRequest(context.Background(), request))
	foundRequest, ok, err := manager.GetGatewayPairingRequest(context.Background(), "telegram", "123")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, request, foundRequest)
	requests, err := manager.ListGatewayPairingRequests(context.Background(), "telegram")
	require.NoError(t, err)
	require.Equal(t, []pairing.PendingRequest{request}, requests)
	require.NoError(t, manager.DeleteGatewayPairingRequest(context.Background(), "telegram", "123"))
	requests, err = manager.ListGatewayPairingRequests(context.Background(), "telegram")
	require.NoError(t, err)
	require.Empty(t, requests)

	require.NoError(t, manager.SaveGatewayPairingRequest(context.Background(), request))
	require.NoError(t, manager.ClearGatewayPairingRequests(context.Background(), "telegram"))
	requests, err = manager.ListGatewayPairingRequests(context.Background(), "telegram")
	require.NoError(t, err)
	require.Empty(t, requests)

	require.NoError(t, manager.SaveGatewayPairedSender(context.Background(), sender))
	foundSender, ok, err := manager.GetGatewayPairedSender(context.Background(), "telegram", "123")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, sender, foundSender)
	senders, err := manager.ListGatewayPairedSenders(context.Background(), "telegram")
	require.NoError(t, err)
	require.Equal(t, []pairing.ApprovedSender{sender}, senders)
	require.NoError(t, manager.DeleteGatewayPairedSender(context.Background(), "telegram", "123"))
	senders, err = manager.ListGatewayPairedSenders(context.Background(), "telegram")
	require.NoError(t, err)
	require.Empty(t, senders)

	require.EqualError(t,
		(*Manager)(nil).SaveGatewayPairingRequest(context.Background(), request),
		"state manager is required",
	)
	_, _, err = (*Manager)(nil).GetGatewayPairingRequest(context.Background(), "telegram", "123")
	require.EqualError(t, err, "state manager is required")
	_, err = (*Manager)(nil).ListGatewayPairingRequests(context.Background(), "telegram")
	require.EqualError(t, err, "state manager is required")
	require.EqualError(t,
		(*Manager)(nil).DeleteGatewayPairingRequest(context.Background(), "telegram", "123"),
		"state manager is required",
	)
	require.EqualError(t,
		(*Manager)(nil).ClearGatewayPairingRequests(context.Background(), "telegram"),
		"state manager is required",
	)
	require.EqualError(t,
		(*Manager)(nil).SaveGatewayPairedSender(context.Background(), sender),
		"state manager is required",
	)
	_, _, err = (*Manager)(nil).GetGatewayPairedSender(context.Background(), "telegram", "123")
	require.EqualError(t, err, "state manager is required")
	_, err = (*Manager)(nil).ListGatewayPairedSenders(context.Background(), "telegram")
	require.EqualError(t, err, "state manager is required")
	require.EqualError(t,
		(*Manager)(nil).DeleteGatewayPairedSender(context.Background(), "telegram", "123"),
		"state manager is required",
	)
}

func TestManager_UsesAggregateStoreCapabilities(t *testing.T) {
	automationStore := storagememory.NewStore()
	memoryStore := storagememory.NewStore()
	traceStore := storagememory.NewStore()
	manager, err := NewManager(&storagemock.Store{
		AutomationStore:       automationStore,
		MemoryStore:           memoryStore,
		TraceStore:            traceStore,
		VectorSearchSupported: true,
	}, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	gotAutomationStore, ok := manager.AutomationStore()
	require.True(t, ok)
	require.Same(t, automationStore, gotAutomationStore)

	gotMemoryStore, ok := manager.MemoryStore()
	require.True(t, ok)
	require.Same(t, memoryStore, gotMemoryStore)

	gotTraceStore, ok := manager.TraceStore()
	require.True(t, ok)
	require.Same(t, traceStore, gotTraceStore)
	require.True(t, manager.SupportsVectorSearch())
}

func TestManager_AutomationOperationsUseAutomationStore(t *testing.T) {
	ctx := context.Background()
	manager, err := NewManager(storagememory.NewStore(), time.Hour, 24*time.Hour)
	require.NoError(t, err)

	job, err := manager.CreateAutomationJob(ctx, storage.AutomationJob{
		ID:      testAutomationJob,
		Name:    "Daily headlines",
		Enabled: true,
		Payload: storage.AutomationPayload{Kind: storage.AutomationPayloadPrompt, Prompt: "Summarize headlines"},
	})
	require.NoError(t, err)
	require.Equal(t, testAutomationJob, job.ID)

	loaded, ok, err := manager.GetAutomationJob(ctx, testAutomationJob)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "Daily headlines", loaded.Name)

	name := "Daily local headlines"
	patched, err := manager.PatchAutomationJob(ctx, storage.AutomationJobPatch{
		ID:   testAutomationJob,
		Name: &name,
	})
	require.NoError(t, err)
	require.Equal(t, "Daily local headlines", patched.Name)

	jobs, err := manager.ListAutomationJobs(ctx, storage.AutomationJobQuery{})
	require.NoError(t, err)
	require.Len(t, jobs.Jobs, 1)
	require.Equal(t, testAutomationJob, jobs.Jobs[0].ID)

	run, err := manager.CreateAutomationRun(ctx, storage.AutomationRun{
		ID:    testAutomationRun,
		JobID: testAutomationJob,
	})
	require.NoError(t, err)
	require.Equal(t, storage.AutomationRunStatusRunning, run.Status)

	finished, err := manager.FinishAutomationRun(ctx, storage.AutomationRunPatch{
		ID:     testAutomationRun,
		Status: storage.AutomationRunStatusOK,
		Output: "done",
	})
	require.NoError(t, err)
	require.Equal(t, storage.AutomationRunStatusOK, finished.Status)
	require.Equal(t, "done", finished.Output)

	runs, err := manager.ListAutomationRuns(ctx, storage.AutomationRunQuery{JobID: testAutomationJob})
	require.NoError(t, err)
	require.Len(t, runs.Runs, 1)
	require.Equal(t, testAutomationRun, runs.Runs[0].ID)

	require.NoError(t, manager.DeleteAutomationJob(ctx, testAutomationJob))
	_, ok, err = manager.GetAutomationJob(ctx, testAutomationJob)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestManager_AutomationOperationsRequireAutomationStore(t *testing.T) {
	ctx := context.Background()
	manager, err := NewManager(&storagemock.Store{}, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	_, err = manager.CreateAutomationJob(ctx, storage.AutomationJob{})
	require.EqualError(t, err, "automation store is not supported")

	_, _, err = manager.GetAutomationJob(ctx, testAutomationJob)
	require.EqualError(t, err, "automation store is not supported")

	_, err = manager.ListAutomationJobs(ctx, storage.AutomationJobQuery{})
	require.EqualError(t, err, "automation store is not supported")

	_, err = manager.PatchAutomationJob(ctx, storage.AutomationJobPatch{ID: testAutomationJob})
	require.EqualError(t, err, "automation store is not supported")

	err = manager.DeleteAutomationJob(ctx, testAutomationJob)
	require.EqualError(t, err, "automation store is not supported")

	_, err = manager.CreateAutomationRun(ctx, storage.AutomationRun{})
	require.EqualError(t, err, "automation store is not supported")

	_, err = manager.FinishAutomationRun(ctx, storage.AutomationRunPatch{ID: testAutomationRun})
	require.EqualError(t, err, "automation store is not supported")

	_, err = manager.ListAutomationRuns(ctx, storage.AutomationRunQuery{})
	require.EqualError(t, err, "automation store is not supported")
}

func TestManager_MemoryOperationsUseMemoryStore(t *testing.T) {
	manager, err := NewManager(storagememory.NewStore(), time.Hour, 24*time.Hour)
	require.NoError(t, err)

	item, err := manager.UpsertMemory(context.Background(), storage.MemoryItem{
		Kind:   storage.MemoryKindSemantic,
		Status: storage.MemoryStatusActive,
		Text:   "remember manager owned memory",
	})
	require.NoError(t, err)
	require.NotEmpty(t, item.ID)

	result, err := manager.SearchMemory(context.Background(), storage.MemorySearchQuery{Text: "manager"})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	require.Equal(t, item.ID, result.Hits[0].Item.ID)

	reflected := true
	item, err = manager.PatchMemory(context.Background(), storage.MemoryPatch{
		ID:        item.ID,
		Reflected: &reflected,
	})
	require.NoError(t, err)
	require.True(t, item.Reflected)

	sessionResult, err := manager.ListSessionMemories(context.Background(), storage.SessionMemoryQuery{
		SessionID: storage.DefaultSessionID,
		Statuses:  []storage.MemoryStatus{storage.MemoryStatusActive},
	})
	require.NoError(t, err)
	require.Empty(t, sessionResult.Items)

	require.NoError(t, manager.DeleteMemory(context.Background(), storage.MemoryDeleteRequest{ID: item.ID}))

	result, err = manager.SearchMemory(context.Background(), storage.MemorySearchQuery{Text: "manager"})
	require.NoError(t, err)
	require.Empty(t, result.Hits)

	item, err = manager.UpsertMemory(context.Background(), storage.MemoryItem{
		Kind:   storage.MemoryKindSemantic,
		Status: storage.MemoryStatusCandidate,
		Text:   "hard delete manager owned memory",
	})
	require.NoError(t, err)
	require.NoError(t, manager.HardDeleteMemory(context.Background(), storage.MemoryDeleteRequest{ID: item.ID}))

	result, err = manager.SearchMemory(context.Background(), storage.MemorySearchQuery{
		IDs:      []string{item.ID},
		Statuses: []storage.MemoryStatus{storage.MemoryStatusCandidate, storage.MemoryStatusDeleted},
	})
	require.NoError(t, err)
	require.Empty(t, result.Hits)
}

func TestManager_MemoryOperationsRequireMemoryStore(t *testing.T) {
	manager, err := NewManager(&storagemock.Store{}, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	_, err = manager.SearchMemory(context.Background(), storage.MemorySearchQuery{})
	require.EqualError(t, err, "memory store is not supported")

	_, err = manager.ListSessionMemories(context.Background(), storage.SessionMemoryQuery{SessionID: storage.DefaultSessionID})
	require.EqualError(t, err, "memory store is not supported")

	_, err = manager.UpsertMemory(context.Background(), storage.MemoryItem{})
	require.EqualError(t, err, "memory store is not supported")

	_, err = manager.PatchMemory(context.Background(), storage.MemoryPatch{ID: "mem_123"})
	require.EqualError(t, err, "memory store is not supported")

	err = manager.DeleteMemory(context.Background(), storage.MemoryDeleteRequest{ID: "mem_123"})
	require.EqualError(t, err, "memory store is not supported")

	err = manager.HardDeleteMemory(context.Background(), storage.MemoryDeleteRequest{ID: "mem_123"})
	require.EqualError(t, err, "memory store is not supported")
}

func TestManager_TraceOperationsUseTraceStore(t *testing.T) {
	ctx := context.Background()
	manager, err := NewManager(storagememory.NewStore(), time.Hour, 24*time.Hour)
	require.NoError(t, err)

	event, err := manager.AppendTraceEvent(ctx, storage.TraceEvent{
		SessionID: storage.DefaultSessionID,
		Type:      "model.request",
		Payload:   map[string]any{"message": "hello"},
	})
	require.NoError(t, err)
	require.Equal(t, 1, event.Sequence)

	result, err := manager.ListTraceEvents(ctx, storage.TraceQuery{SessionID: storage.DefaultSessionID})
	require.NoError(t, err)
	require.Len(t, result.Events, 1)
	require.Equal(t, "model.request", result.Events[0].Type)

	require.NoError(t, manager.PruneTraceEvents(ctx, storage.DefaultSessionID, 0))
	result, err = manager.ListTraceEvents(ctx, storage.TraceQuery{SessionID: storage.DefaultSessionID})
	require.NoError(t, err)
	require.Empty(t, result.Events)
}

func TestManager_TraceOperationsRequireTraceStore(t *testing.T) {
	ctx := context.Background()
	manager, err := NewManager(&storagemock.Store{}, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	_, err = manager.AppendTraceEvent(ctx, storage.TraceEvent{SessionID: storage.DefaultSessionID, Type: "model.request"})
	require.EqualError(t, err, "trace store is not supported")

	_, err = manager.ListTraceEvents(ctx, storage.TraceQuery{SessionID: storage.DefaultSessionID})
	require.EqualError(t, err, "trace store is not supported")

	err = manager.PruneTraceEvents(ctx, storage.DefaultSessionID, 1)
	require.EqualError(t, err, "trace store is not supported")
}

func TestManager_RunMaintenancePreservesExpiredDefaultMessages(t *testing.T) {
	store := storagememory.NewStore()
	expiredAt := time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), storage.Session{
		ID:        storage.DefaultSessionID,
		CreatedAt: expiredAt.Add(-time.Hour),
		UpdatedAt: expiredAt,
	}))
	require.NoError(t, store.AppendMessages(context.Background(), storage.DefaultSessionID, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: expiredAt.Add(-time.Minute)},
	}))
	defaultSession, ok, err := store.Get(context.Background(), storage.DefaultSessionID, storage.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)

	manager, err := NewManager(store, time.Hour, 48*time.Hour)
	require.NoError(t, err)
	manager.now = func() time.Time { return defaultSession.UpdatedAt.Add(2 * time.Hour) }

	err = manager.runMaintenance(context.Background())
	require.NoError(t, err)

	liveMessages, err := store.GetMessages(context.Background(), storage.DefaultSessionID, storage.MessageQueryOptions{})
	require.NoError(t, err)
	require.Len(t, liveMessages, 1)
	require.Equal(t, "hello", liveMessages[0].Content)
}

func TestManager_RunMaintenancePreservesDefaultSessionCompactionMetadata(t *testing.T) {
	store := storagememory.NewStore()
	expiredAt := time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), storage.Session{
		ID: storage.DefaultSessionID,
		Compaction: storage.SessionCompaction{
			Status:             storage.CompactionStatusRunning,
			RequestedAt:        expiredAt.Add(-30 * time.Minute),
			StartedAt:          expiredAt.Add(-29 * time.Minute),
			TargetMessageCount: 9,
			TargetOffset:       1,
		},
		CreatedAt: expiredAt.Add(-time.Hour),
		UpdatedAt: expiredAt,
	}))
	require.NoError(t, store.AppendMessages(context.Background(), storage.DefaultSessionID, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: expiredAt.Add(-time.Minute)},
	}))
	defaultSession, ok, err := store.Get(context.Background(), storage.DefaultSessionID, storage.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)

	manager, err := NewManager(store, time.Hour, 48*time.Hour)
	require.NoError(t, err)
	manager.now = func() time.Time { return defaultSession.UpdatedAt.Add(2 * time.Hour) }

	require.NoError(t, manager.runMaintenance(context.Background()))

	session, ok, err := store.Get(context.Background(), storage.DefaultSessionID, storage.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, storage.SessionCompaction{
		Status:             storage.CompactionStatusRunning,
		RequestedAt:        expiredAt.Add(-30 * time.Minute),
		StartedAt:          expiredAt.Add(-29 * time.Minute),
		TargetMessageCount: 9,
		TargetOffset:       1,
	}, session.Compaction)
}

func TestManager_CurrentSessionDefaultsToDefault(t *testing.T) {
	manager, err := NewManager(storagememory.NewStore(), time.Hour, 24*time.Hour)
	require.NoError(t, err)

	current, err := manager.CurrentSession(context.Background())

	require.NoError(t, err)
	require.Equal(t, storage.DefaultSessionID, current)
}

func TestManager_StartResolvesDefaultSession(t *testing.T) {
	store := storagememory.NewStore()
	manager, err := NewManager(store, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	err = manager.Start(context.Background())
	require.NoError(t, err)

	session, ok, err := store.Get(context.Background(), storage.DefaultSessionID, storage.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, storage.DefaultSessionID, session.ID)
}

func TestManager_UseSessionUsesResolvedDefaultSession(t *testing.T) {
	store := storagememory.NewStore()
	manager, err := NewManager(store, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	require.NoError(t, manager.Start(context.Background()))
	err = manager.UseSession(context.Background(), storage.DefaultSessionID)
	require.NoError(t, err)

	current, ok, err := store.Current(context.Background())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, storage.DefaultSessionID, current)
}

func TestNewManager_ValidationAndNilManagerErrors(t *testing.T) {
	_, err := NewManager(nil, time.Hour, 24*time.Hour)
	require.EqualError(t, err, "store is required")

	_, err = NewManager(storeWithoutSession{}, time.Hour, 24*time.Hour)
	require.EqualError(t, err, "session store is required")

	_, err = NewManager(storagememory.NewStore(), 0, 24*time.Hour)
	require.EqualError(t, err, "session default idle expiry must be greater than zero")

	_, err = NewManager(storagememory.NewStore(), time.Hour, 0)
	require.EqualError(t, err, "session archive retention must be greater than zero")

	var manager *Manager

	_, err = manager.Resolve(context.Background(), "")
	require.EqualError(t, err, "state manager is required")
	require.EqualError(t, manager.runMaintenance(context.Background()), "state manager is required")
	require.EqualError(t, manager.Start(context.Background()), "state manager is required")

	require.EqualError(t, manager.AppendMessages(context.Background(), storage.DefaultSessionID, nil), "state manager is required")
	_, err = manager.CountMessages(context.Background(), storage.DefaultSessionID, storage.MessageQueryOptions{})
	require.EqualError(t, err, "state manager is required")
	_, err = manager.SearchMessages(context.Background(), storage.DefaultSessionID, storage.SearchMessageOptions{})
	require.EqualError(t, err, "state manager is required")
	require.EqualError(t, manager.Save(context.Background(), storage.Session{}), "state manager is required")
	_, _, err = manager.Get(context.Background(), storage.DefaultSessionID, storage.SessionGetOptions{})
	require.EqualError(t, err, "state manager is required")
	_, _, err = manager.GetMessage(context.Background(), storage.DefaultSessionID, 0)
	require.EqualError(t, err, "state manager is required")
	_, err = manager.GetMessages(context.Background(), storage.DefaultSessionID, storage.MessageQueryOptions{})
	require.EqualError(t, err, "state manager is required")
	_, err = manager.GetMessagesByIDs(context.Background(), storage.DefaultSessionID, []uint{1})
	require.EqualError(t, err, "state manager is required")
	_, err = manager.GetMessageWindow(context.Background(), storage.DefaultSessionID, 1, 1, 1)
	require.EqualError(t, err, "state manager is required")
	require.EqualError(t, manager.UpdateLastPromptTokens(context.Background(), storage.DefaultSessionID, 1), "state manager is required")

	_, err = manager.CreateSession(context.Background(), testSessionA)
	require.EqualError(t, err, "state manager is required")

	_, err = manager.ListSessions(context.Background())
	require.EqualError(t, err, "state manager is required")

	require.EqualError(t, manager.UseSession(context.Background(), storage.DefaultSessionID), "state manager is required")
	require.EqualError(t, manager.DeleteSession(context.Background(), storage.DefaultSessionID), "state manager is required")
	require.EqualError(t, manager.ArchiveSession(context.Background(), testSessionA), "state manager is required")
	_, err = manager.RenameSession(context.Background(), testSessionA, "Title")
	require.EqualError(t, err, "state manager is required")

	current, err := manager.CurrentSession(context.Background())
	require.EqualError(t, err, "state manager is required")
	require.Empty(t, current)

	memoryStore, ok := manager.MemoryStore()
	require.False(t, ok)
	require.Nil(t, memoryStore)

	automationStore, ok := manager.AutomationStore()
	require.False(t, ok)
	require.Nil(t, automationStore)

	traceStore, ok := manager.TraceStore()
	require.False(t, ok)
	require.Nil(t, traceStore)
	require.False(t, manager.SupportsVectorSearch())
}

func TestManager_CountMessages_ForwardsToStore(t *testing.T) {
	var capturedID string
	var capturedOpts storage.MessageQueryOptions
	manager, err := NewManager(&storagemock.Store{
		CountMessagesFunc: func(_ context.Context, id string, opts storage.MessageQueryOptions) (int, error) {
			capturedID = id
			capturedOpts = opts
			return 7, nil
		},
	}, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	count, err := manager.CountMessages(context.Background(), "  "+testSessionA+"  ", storage.MessageQueryOptions{
		Limit:  2,
		Offset: 3,
	})
	require.NoError(t, err)
	require.Equal(t, 7, count)
	require.Equal(t, testSessionA, capturedID)
	require.Equal(t, storage.MessageQueryOptions{Limit: 2, Offset: 3}, capturedOpts)
}

func TestManager_SearchMessages_ForwardsToStore(t *testing.T) {
	var capturedID string
	var capturedOpts storage.SearchMessageOptions
	manager, err := NewManager(&storagemock.Store{
		SearchMessagesFunc: func(_ context.Context, id string, opts storage.SearchMessageOptions) ([]storage.SearchMessageResult, error) {
			capturedID = id
			capturedOpts = opts
			return []storage.SearchMessageResult{{SessionID: testSessionA, MatchCount: 1}}, nil
		},
	}, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	results, err := manager.SearchMessages(context.Background(), "  "+testSessionA+"  ", storage.SearchMessageOptions{
		IgnoreSessionID:       "  " + storage.DefaultSessionID + "  ",
		Query:                 "hello",
		ToolName:              "process",
		MaxSessions:           2,
		MaxMessagesPerSession: 3,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, testSessionA, capturedID)
	require.Equal(t, testSessionA, results[0].SessionID)
	require.Equal(t, storage.SearchMessageOptions{
		IgnoreSessionID:       storage.DefaultSessionID,
		Query:                 "hello",
		ToolName:              "process",
		MaxSessions:           2,
		MaxMessagesPerSession: 3,
	}, capturedOpts)
}

func TestManager_GetMessagesByIDs_ForwardsToStore(t *testing.T) {
	var capturedID string
	var capturedMessageIDs []uint
	manager, err := NewManager(&storagemock.Store{
		GetMessagesByIDsFunc: func(_ context.Context, id string, messageIDs []uint) ([]storage.MessageRecord, error) {
			capturedID = id
			capturedMessageIDs = append([]uint(nil), messageIDs...)
			return []storage.MessageRecord{{Offset: 3, Message: morphmsg.Message{ID: 7}}}, nil
		},
	}, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	records, err := manager.GetMessagesByIDs(context.Background(), "  "+testSessionA+"  ", []uint{7, 9})
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, testSessionA, capturedID)
	require.Equal(t, []uint{7, 9}, capturedMessageIDs)
}

func TestManager_GetMessageWindow_ForwardsToStore(t *testing.T) {
	var capturedID string
	var capturedAnchor uint
	var capturedBefore int
	var capturedAfter int
	manager, err := NewManager(&storagemock.Store{
		GetMessageWindowFunc: func(_ context.Context, id string, anchorMessageID uint, before int, after int) ([]storage.MessageRecord, error) {
			capturedID = id
			capturedAnchor = anchorMessageID
			capturedBefore = before
			capturedAfter = after
			return []storage.MessageRecord{{Offset: 2, Message: morphmsg.Message{ID: anchorMessageID}}}, nil
		},
	}, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	records, err := manager.GetMessageWindow(context.Background(), "  "+testSessionA+"  ", 7, 1, 2)
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, testSessionA, capturedID)
	require.Equal(t, uint(7), capturedAnchor)
	require.Equal(t, 1, capturedBefore)
	require.Equal(t, 2, capturedAfter)
}

func TestManager_RepairVectorStore_ForwardsToStore(t *testing.T) {
	store := &repairVectorStoreStub{
		result: search.VectorRepairResult{SessionsScanned: 1, RebuiltRows: 2},
	}
	manager, err := NewManager(store, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	result, err := manager.RepairVectorStore(context.Background(), search.VectorRepairOptions{
		SessionID: "  " + testSessionA + "  ",
		Full:      true,
	})

	require.NoError(t, err)
	require.Equal(t, search.VectorRepairResult{SessionsScanned: 1, RebuiltRows: 2}, result)
	require.Equal(t, testSessionA, store.opts.SessionID)
	require.True(t, store.opts.Full)
}

func TestManager_RepairVectorStore_ReturnsUnsupportedStoreError(t *testing.T) {
	var nilManager *Manager
	result, err := nilManager.RepairVectorStore(context.Background(), search.VectorRepairOptions{})

	require.EqualError(t, err, "state manager is required")
	require.Equal(t, search.VectorRepairResult{}, result)

	manager, err := NewManager(&storagemock.Store{}, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	result, err = manager.RepairVectorStore(context.Background(), search.VectorRepairOptions{})

	require.EqualError(t, err, "session vector repair is not supported")
	require.Equal(t, search.VectorRepairResult{}, result)
}

func TestManager_Save_ForwardsToStore(t *testing.T) {
	expected := storage.Session{
		ID: testSessionA,
		Compaction: storage.SessionCompaction{
			Status:       storage.CompactionStatusSucceeded,
			TargetOffset: 3,
		},
		LastPromptTokens: 42,
	}

	var captured storage.Session
	manager, err := NewManager(&storagemock.Store{
		SaveFunc: func(_ context.Context, session storage.Session) error {
			captured = session
			return nil
		},
	}, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	require.NoError(t, manager.Save(context.Background(), expected))
	require.Equal(t, expected, captured)
}

func TestManager_Save_ReturnsStoreError(t *testing.T) {
	manager, err := NewManager(&storagemock.Store{
		SaveFunc: func(context.Context, storage.Session) error {
			return errors.New("save failed")
		},
	}, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	err = manager.Save(context.Background(), storage.Session{ID: testSessionA})
	require.EqualError(t, err, "save failed")
}

func TestManager_Get_TrimsIDAndReturnsStoreResult(t *testing.T) {
	expected := storage.Session{
		ID:               testSessionA,
		LastPromptTokens: 9,
	}

	var capturedID string
	manager, err := NewManager(&storagemock.Store{
		GetFunc: func(_ context.Context, id string) (storage.Session, bool, error) {
			capturedID = id
			return expected, true, nil
		},
	}, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	session, ok, err := manager.Get(context.Background(), "  "+testSessionA+"  ", storage.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, expected, session)
	require.Equal(t, testSessionA, capturedID)
}

func TestManager_Get_ForwardsSessionGetOptions(t *testing.T) {
	archived := true
	expected := storage.Session{ID: testSessionA, Archived: true}

	var captured storage.SessionGetOptions
	manager, err := NewManager(&storagemock.Store{
		GetWithOptionsFunc: func(
			_ context.Context,
			id string,
			opts storage.SessionGetOptions,
		) (storage.Session, bool, error) {
			require.Equal(t, testSessionA, id)
			captured = opts

			return expected, true, nil
		},
	}, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	session, ok, err := manager.Get(
		context.Background(),
		"  "+testSessionA+"  ",
		storage.SessionGetOptions{Archived: &archived},
	)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, expected, session)
	require.NotNil(t, captured.Archived)
	require.True(t, *captured.Archived)
}

func TestManager_Get_ReturnsStoreError(t *testing.T) {
	manager, err := NewManager(&storagemock.Store{
		GetFunc: func(context.Context, string) (storage.Session, bool, error) {
			return storage.Session{}, false, errors.New("get failed")
		},
	}, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	_, _, err = manager.Get(context.Background(), testSessionA, storage.SessionGetOptions{})
	require.EqualError(t, err, "get failed")
}

func TestManager_CreateSaveListAndResolveNonDefaultSession(t *testing.T) {
	store := storagememory.NewStore()
	manager, err := NewManager(store, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	created, err := manager.CreateSession(context.Background(), testSessionA)
	require.NoError(t, err)
	require.Equal(t, testSessionA, created.ID)
	require.False(t, created.CreatedAt.IsZero())

	require.EqualError(t, manager.AppendMessages(context.Background(), "", nil), "session id is required")
	require.NoError(t, manager.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)},
	}))

	resolved, err := manager.Resolve(context.Background(), testSessionA)
	require.NoError(t, err)
	require.Equal(t, testSessionA, resolved.ID)
	messages, err := manager.GetMessages(context.Background(), testSessionA, storage.MessageQueryOptions{})
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, "hello", messages[0].Content)
	message, ok, err := manager.GetMessage(context.Background(), testSessionA, 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "hello", message.Content)

	sessions, err := manager.ListSessions(context.Background())
	require.NoError(t, err)
	require.Len(t, sessions, 2)
	require.Equal(t, storage.DefaultSessionID, sessions[0].ID)
	require.Equal(t, testSessionA, sessions[1].ID)
}

func TestManager_ListSessionsRequestsActiveSessions(t *testing.T) {
	var captured storage.SessionListOptions
	manager, err := NewManager(&storagemock.Store{
		GetFunc: func(context.Context, string) (storage.Session, bool, error) {
			return storage.Session{ID: storage.DefaultSessionID}, true, nil
		},
		ListWithOptionsFunc: func(_ context.Context, opts storage.SessionListOptions) ([]storage.Session, error) {
			captured = opts
			return []storage.Session{{ID: testSessionA}}, nil
		},
	}, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	sessions, err := manager.ListSessions(context.Background())

	require.NoError(t, err)
	require.Len(t, sessions, 1)
	require.Equal(t, testSessionA, sessions[0].ID)
	require.NotNil(t, captured.Archived)
	require.False(t, *captured.Archived)
}

func TestManager_ListSessionsRequestsArchivedSessions(t *testing.T) {
	var captured storage.SessionListOptions
	defaultResolved := false
	manager, err := NewManager(&storagemock.Store{
		GetFunc: func(context.Context, string) (storage.Session, bool, error) {
			defaultResolved = true
			return storage.Session{}, false, nil
		},
		ListWithOptionsFunc: func(_ context.Context, opts storage.SessionListOptions) ([]storage.Session, error) {
			captured = opts
			return []storage.Session{{ID: testSessionA, Archived: true}}, nil
		},
	}, time.Hour, 24*time.Hour)
	require.NoError(t, err)
	archived := true

	sessions, err := manager.ListSessions(context.Background(), storage.SessionListOptions{Archived: &archived})

	require.NoError(t, err)
	require.Len(t, sessions, 1)
	require.Equal(t, testSessionA, sessions[0].ID)
	require.NotNil(t, captured.Archived)
	require.True(t, *captured.Archived)
	require.False(t, defaultResolved)

	_, err = (*Manager)(nil).ListSessions(context.Background(), storage.SessionListOptions{Archived: &archived})
	require.EqualError(t, err, "state manager is required")
}

func TestManager_ListSessionsWithNilArchiveOptionDoesNotCreateDefaultSession(t *testing.T) {
	defaultResolved := false
	manager, err := NewManager(&storagemock.Store{
		GetFunc: func(context.Context, string) (storage.Session, bool, error) {
			defaultResolved = true
			return storage.Session{}, false, nil
		},
		ListWithOptionsFunc: func(context.Context, storage.SessionListOptions) ([]storage.Session, error) {
			return []storage.Session{{ID: testSessionA}}, nil
		},
	}, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	sessions, err := manager.ListSessions(context.Background(), storage.SessionListOptions{})

	require.NoError(t, err)
	require.Len(t, sessions, 1)
	require.False(t, defaultResolved)
}

func TestManager_AppendMessagesCreatesDefaultSession(t *testing.T) {
	store := storagememory.NewStore()
	manager, err := NewManager(store, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	require.NoError(t, manager.AppendMessages(context.Background(), storage.DefaultSessionID, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "hello default", CreatedAt: time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)},
	}))

	session, ok, err := store.Get(context.Background(), storage.DefaultSessionID, storage.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, storage.DefaultSessionID, session.ID)

	messages, err := store.GetMessages(context.Background(), storage.DefaultSessionID, storage.MessageQueryOptions{})
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, "hello default", messages[0].Content)
}

func TestManager_RenameSessionUpdatesTitle(t *testing.T) {
	store := storagememory.NewStore()
	manager, err := NewManager(store, time.Hour, 24*time.Hour)
	require.NoError(t, err)
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }

	_, err = manager.CreateSession(context.Background(), testSessionA)
	require.NoError(t, err)

	renamed, err := manager.RenameSession(context.Background(), "  "+testSessionA+"  ", "  Project Planning  ")

	require.NoError(t, err)
	require.Equal(t, testSessionA, renamed.ID)
	require.Equal(t, "Project Planning", renamed.Title)
	require.Equal(t, storage.SessionTitleSourceManual, renamed.TitleSource)
	require.False(t, renamed.UpdatedAt.IsZero())

	persisted, ok, err := store.Get(context.Background(), testSessionA, storage.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "Project Planning", persisted.Title)
	require.Equal(t, storage.SessionTitleSourceManual, persisted.TitleSource)
}

func TestManager_RenameSessionCreatesDefaultSession(t *testing.T) {
	store := storagememory.NewStore()
	manager, err := NewManager(store, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	renamed, err := manager.RenameSession(context.Background(), storage.DefaultSessionID, "Default Planning")

	require.NoError(t, err)
	require.Equal(t, storage.DefaultSessionID, renamed.ID)
	require.Equal(t, "Default Planning", renamed.Title)
	require.Equal(t, storage.SessionTitleSourceManual, renamed.TitleSource)
}

func TestManager_CreateUseAndResolveErrors(t *testing.T) {
	store := storagememory.NewStore()
	manager, err := NewManager(store, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	created, err := manager.CreateSession(context.Background(), "")
	require.NoError(t, err)
	require.NoError(t, storage.ValidateSessionID(created.ID))

	_, err = manager.CreateSession(context.Background(), "project-a")
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")

	_, err = manager.CreateSession(context.Background(), testSessionA)
	require.NoError(t, err)

	_, err = manager.CreateSession(context.Background(), testSessionA)
	require.EqualError(t, err, "session already exists")

	_, err = manager.Resolve(context.Background(), testMissingSession)
	require.EqualError(t, err, "session not found")

	_, err = manager.Resolve(context.Background(), "project-a")
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")

	require.EqualError(t, manager.UseSession(context.Background(), "project-a"), "session id must be a valid ses_ nanoid")
	require.EqualError(t, manager.UseSession(context.Background(), testMissingSession), "session not found")

	manager, err = NewManager(&storagemock.Store{
		GetFunc: func(context.Context, string) (storage.Session, bool, error) {
			return storage.Session{ID: testSessionA, Archived: true}, true, nil
		},
	}, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	_, err = manager.Resolve(context.Background(), testSessionA)
	require.EqualError(t, err, "session is archived")

	manager, err = NewManager(&storagemock.Store{
		GetFunc: func(context.Context, string) (storage.Session, bool, error) {
			return storage.Session{}, false, errors.New("get failed")
		},
	}, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	require.EqualError(t, manager.UseSession(context.Background(), testSessionA), "get failed")
}

func TestManager_CreateSessionWithOptionsPersistsOrigin(t *testing.T) {
	store := storagememory.NewStore()
	manager, err := NewManager(store, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	created, err := manager.CreateSessionWithOptions(context.Background(), testSessionA, storage.SessionCreateOptions{
		Origin: storage.SessionOrigin{
			Source:         " telegram ",
			ConversationID: " -100 ",
			ThreadID:       " 42 ",
		},
	})
	require.NoError(t, err)

	require.Equal(t, storage.SessionOrigin{
		Source:         storage.SessionOriginSourceTelegram,
		ConversationID: "-100",
		ThreadID:       "42",
	}, created.Origin)
	loaded, ok, err := store.Get(context.Background(), testSessionA, storage.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, created.Origin, loaded.Origin)
}

func TestManager_RenameSessionValidatesInputAndStoreErrors(t *testing.T) {
	manager, err := NewManager(storagememory.NewStore(), time.Hour, 24*time.Hour)
	require.NoError(t, err)

	_, err = manager.RenameSession(context.Background(), "", "Title")
	require.EqualError(t, err, "session id is required")

	_, err = manager.RenameSession(context.Background(), testSessionA, " ")
	require.EqualError(t, err, "session title is required")

	_, err = manager.RenameSession(context.Background(), "project-a", "Title")
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")

	_, err = manager.RenameSession(context.Background(), testSessionA, "Title")
	require.EqualError(t, err, "session not found")

	manager, err = NewManager(&storagemock.Store{
		GetFunc: func(context.Context, string) (storage.Session, bool, error) {
			return storage.Session{ID: testSessionA}, true, nil
		},
		RenameFunc: func(context.Context, storage.SessionRenameRequest) (storage.Session, error) {
			return storage.Session{}, errors.New("rename failed")
		},
	}, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	_, err = manager.RenameSession(context.Background(), testSessionA, "Title")
	require.EqualError(t, err, "rename failed")

	manager, err = NewManager(&storagemock.Store{
		GetFunc: func(context.Context, string) (storage.Session, bool, error) {
			return storage.Session{}, false, nil
		},
		SaveFunc: func(context.Context, storage.Session) error {
			return errors.New("create default failed")
		},
	}, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	_, err = manager.RenameSession(context.Background(), storage.DefaultSessionID, "Title")
	require.EqualError(t, err, "create default failed")

	var captured storage.SessionRenameRequest
	manager, err = NewManager(&storagemock.Store{
		GetFunc: func(context.Context, string) (storage.Session, bool, error) {
			return storage.Session{ID: testSessionA}, true, nil
		},
		RenameFunc: func(_ context.Context, req storage.SessionRenameRequest) (storage.Session, error) {
			captured = req
			return storage.Session{ID: req.SessionID, Title: req.Title, TitleSource: req.TitleSource}, nil
		},
	}, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	renamed, err := manager.RenameSession(context.Background(), "  "+testSessionA+"  ", "  Title  ")
	require.NoError(t, err)
	require.Equal(t, testSessionA, renamed.ID)
	require.Equal(t, "Title", renamed.Title)
	require.Equal(t, testSessionA, captured.SessionID)
	require.Equal(t, "Title", captured.Title)
	require.Equal(t, storage.SessionTitleSourceManual, captured.TitleSource)
	require.False(t, captured.RenamedAt.IsZero())
}

func TestManager_ArchivedSessionsRejectActiveOperationsAndUnarchiveRestores(t *testing.T) {
	store := storagememory.NewStore()
	manager, err := NewManager(store, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	_, err = manager.CreateSession(context.Background(), testSessionA)
	require.NoError(t, err)
	require.NoError(t, manager.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)},
	}))
	require.NoError(t, manager.ArchiveSession(context.Background(), testSessionA))

	_, err = manager.Resolve(context.Background(), testSessionA)
	require.EqualError(t, err, "session is archived")
	require.EqualError(t, manager.UseSession(context.Background(), testSessionA), "session is archived")
	require.EqualError(t, manager.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "after archive"},
	}), "session is archived")
	_, err = manager.RenameSession(context.Background(), testSessionA, "Archived Rename")
	require.EqualError(t, err, "session is archived")

	restored, err := manager.UnarchiveSession(context.Background(), "  "+testSessionA+"  ")
	require.NoError(t, err)
	require.Equal(t, testSessionA, restored.ID)
	require.False(t, restored.Archived)

	require.NoError(t, manager.UseSession(context.Background(), testSessionA))
	renamed, err := manager.RenameSession(context.Background(), testSessionA, "Restored")
	require.NoError(t, err)
	require.Equal(t, "Restored", renamed.Title)
	require.NoError(t, manager.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleAssistant, Content: "restored"},
	}))

	messages, err := manager.GetMessages(context.Background(), testSessionA, storage.MessageQueryOptions{})
	require.NoError(t, err)
	require.Len(t, messages, 2)
	require.Equal(t, "restored", messages[1].Content)
}

func TestManager_DeleteSessionRemovesArchivedSession(t *testing.T) {
	store := storagememory.NewStore()
	manager, err := NewManager(store, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	_, err = manager.CreateSession(context.Background(), testSessionA)
	require.NoError(t, err)
	require.NoError(t, manager.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)},
	}))
	_, err = store.Archive(context.Background(), testSessionA, storage.SessionArchiveRequest{
		ArchivedAt: time.Date(2026, 3, 30, 13, 0, 0, 0, time.UTC),
		ExpiresAt:  time.Date(2026, 4, 1, 13, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.NoError(t, manager.DeleteSession(context.Background(), testSessionA))

	_, ok, err := store.Get(context.Background(), testSessionA, storage.SessionGetOptions{})
	require.NoError(t, err)
	require.False(t, ok)
	current, err := manager.CurrentSession(context.Background())
	require.NoError(t, err)
	require.Equal(t, storage.DefaultSessionID, current)
}

func TestManager_ArchiveSessionMarksSessionArchived(t *testing.T) {
	now := time.Date(2026, 3, 30, 14, 0, 0, 0, time.UTC)
	store := storagememory.NewStore()
	manager, err := NewManager(store, time.Hour, 48*time.Hour)
	require.NoError(t, err)
	manager.now = func() time.Time { return now }

	_, err = manager.CreateSession(context.Background(), testSessionA)
	require.NoError(t, err)
	require.NoError(t, manager.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "archive this", CreatedAt: now.Add(-time.Minute)},
	}))
	require.NoError(t, manager.UseSession(context.Background(), testSessionA))

	require.NoError(t, manager.ArchiveSession(context.Background(), testSessionA))

	session, ok, err := store.Get(context.Background(), testSessionA, storage.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, session.Archived)
	messages, err := store.GetMessages(context.Background(), testSessionA, storage.MessageQueryOptions{})
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, "archive this", messages[0].Content)
	current, err := manager.CurrentSession(context.Background())
	require.NoError(t, err)
	require.Equal(t, storage.DefaultSessionID, current)
}

func TestManager_CurrentSessionUsesStoredSelection(t *testing.T) {
	store := storagememory.NewStore()
	manager, err := NewManager(store, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	_, err = manager.CreateSession(context.Background(), testSessionA)
	require.NoError(t, err)
	require.NoError(t, manager.UseSession(context.Background(), testSessionA))

	current, err := manager.CurrentSession(context.Background())
	require.NoError(t, err)
	require.Equal(t, testSessionA, current)
}

func TestManager_ResolveDefaultSessionKeepsActiveMessagesBeforeExpiry(t *testing.T) {
	store := storagememory.NewStore()
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), storage.Session{ID: storage.DefaultSessionID, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), storage.DefaultSessionID, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "still-active", CreatedAt: now.Add(-5 * time.Minute)},
	}))
	require.NoError(t, store.Save(context.Background(), storage.Session{ID: storage.DefaultSessionID, UpdatedAt: now}))

	manager, err := NewManager(store, time.Hour, 24*time.Hour)
	require.NoError(t, err)
	manager.now = func() time.Time { return now.Add(30 * time.Minute) }

	session, err := manager.Resolve(context.Background(), "")
	require.NoError(t, err)
	require.Equal(t, storage.DefaultSessionID, session.ID)
	messages, err := manager.GetMessages(context.Background(), storage.DefaultSessionID, storage.MessageQueryOptions{})
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, "still-active", messages[0].Content)

}

func TestManager_ResolveChatSessionDoesNotRunMaintenance(t *testing.T) {
	store := storagememory.NewStore()
	expiredAt := time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), storage.Session{ID: storage.DefaultSessionID, UpdatedAt: expiredAt}))
	require.NoError(t, store.AppendMessages(context.Background(), storage.DefaultSessionID, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: expiredAt.Add(-time.Minute)},
	}))
	require.NoError(t, store.Save(context.Background(), storage.Session{ID: storage.DefaultSessionID, UpdatedAt: expiredAt}))

	manager, err := NewManager(store, time.Hour, 48*time.Hour)
	require.NoError(t, err)
	manager.now = func() time.Time { return expiredAt.Add(2 * time.Hour) }

	session, err := manager.Resolve(context.Background(), "")
	require.NoError(t, err)
	require.Equal(t, storage.DefaultSessionID, session.ID)

	messages, err := store.GetMessages(context.Background(), storage.DefaultSessionID, storage.MessageQueryOptions{})
	require.NoError(t, err)
	require.Len(t, messages, 1)

}

func TestManager_StartRunsMaintenance(t *testing.T) {
	store := storagememory.NewStore()
	expiredAt := time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), storage.Session{
		ID:        storage.DefaultSessionID,
		CreatedAt: expiredAt.Add(-time.Hour),
		UpdatedAt: expiredAt,
	}))
	require.NoError(t, store.AppendMessages(context.Background(), storage.DefaultSessionID, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: expiredAt.Add(-time.Minute)},
	}))
	defaultSession, ok, err := store.Get(context.Background(), storage.DefaultSessionID, storage.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)

	manager, err := NewManager(store, time.Hour, 48*time.Hour)
	require.NoError(t, err)
	manager.now = func() time.Time { return defaultSession.UpdatedAt.Add(2 * time.Hour) }

	ctx := t.Context()

	require.NoError(t, manager.startMaintenanceWorker(ctx, 5*time.Millisecond))

	messages, err := store.GetMessages(context.Background(), storage.DefaultSessionID, storage.MessageQueryOptions{})
	require.NoError(t, err)
	require.Len(t, messages, 1)

}

func TestManager_ErrorBranchesAndWorkerTick(t *testing.T) {
	t.Run("resolve non-default get error", func(t *testing.T) {
		manager, err := NewManager(&storagemock.Store{
			GetFunc: func(context.Context, string) (storage.Session, bool, error) {
				return storage.Session{}, false, errors.New("get failed")
			},
		}, time.Hour, 24*time.Hour)
		require.NoError(t, err)

		_, err = manager.Resolve(context.Background(), testSessionA)
		require.EqualError(t, err, "get failed")
	})

	t.Run("run maintenance delete error", func(t *testing.T) {
		manager, err := NewManager(&storagemock.Store{
			DeleteExpiredArchivesFunc: func(context.Context, time.Time) error {
				return errors.New("delete failed")
			},
		}, time.Hour, 24*time.Hour)
		require.NoError(t, err)

		err = manager.runMaintenance(context.Background())
		require.EqualError(t, err, "delete failed")
	})

	t.Run("run maintenance nil manager", func(t *testing.T) {
		var manager *Manager

		require.EqualError(t, manager.runMaintenance(context.Background()), "state manager is required")
	})

	t.Run("start worker defaults interval", func(t *testing.T) {
		var deleteCalls atomic.Int32
		manager, err := NewManager(&storagemock.Store{
			DeleteExpiredArchivesFunc: func(context.Context, time.Time) error {
				deleteCalls.Add(1)
				return nil
			},
		}, time.Hour, 24*time.Hour)
		require.NoError(t, err)

		require.NoError(t, manager.startMaintenanceWorker(context.TODO(), 0))
		require.EqualValues(t, 1, deleteCalls.Load())
	})

	t.Run("start worker runs ticker maintenance", func(t *testing.T) {
		var deleteCalls atomic.Int32
		manager, err := NewManager(&storagemock.Store{
			DeleteExpiredArchivesFunc: func(context.Context, time.Time) error {
				deleteCalls.Add(1)
				return nil
			},
		}, time.Hour, 24*time.Hour)
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		require.NoError(t, manager.startMaintenanceWorker(ctx, 5*time.Millisecond))
		require.Eventually(t, func() bool {
			return deleteCalls.Load() >= 2
		}, time.Second, 10*time.Millisecond)
	})

	t.Run("start worker returns maintenance error", func(t *testing.T) {
		manager, err := NewManager(&storagemock.Store{
			DeleteExpiredArchivesFunc: func(context.Context, time.Time) error {
				return errors.New("delete failed")
			},
		}, time.Hour, 24*time.Hour)
		require.NoError(t, err)

		err = manager.startMaintenanceWorker(context.TODO(), time.Second)
		require.EqualError(t, err, "delete failed")
	})

	t.Run("start worker returns default resolution error", func(t *testing.T) {
		manager, err := NewManager(&storagemock.Store{
			GetFunc: func(context.Context, string) (storage.Session, bool, error) {
				return storage.Session{}, false, errors.New("get failed")
			},
		}, time.Hour, 24*time.Hour)
		require.NoError(t, err)

		err = manager.startMaintenanceWorker(context.TODO(), time.Second)
		require.EqualError(t, err, "get failed")
	})

	t.Run("start worker uses background when context is canceled", func(t *testing.T) {
		var captured context.Context
		manager, err := NewManager(&storagemock.Store{
			GetFunc: func(ctx context.Context, _ string) (storage.Session, bool, error) {
				captured = ctx
				return storage.Session{}, false, nil
			},
		}, time.Hour, 24*time.Hour)
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		require.NoError(t, manager.startMaintenanceWorker(ctx, time.Second))
		require.NotNil(t, captured)
		require.NoError(t, captured.Err())
	})

	t.Run("create session get error", func(t *testing.T) {
		manager, err := NewManager(&storagemock.Store{
			GetFunc: func(context.Context, string) (storage.Session, bool, error) {
				return storage.Session{}, false, errors.New("get failed")
			},
		}, time.Hour, 24*time.Hour)
		require.NoError(t, err)

		_, err = manager.CreateSession(context.Background(), testSessionA)
		require.EqualError(t, err, "get failed")
	})

	t.Run("create session generated id error", func(t *testing.T) {
		originalGenerateSessionID := generateSessionID
		t.Cleanup(func() {
			generateSessionID = originalGenerateSessionID
		})
		generateSessionID = func() (string, error) {
			return "", errors.New("generate failed")
		}

		manager, err := NewManager(&storagemock.Store{}, time.Hour, 24*time.Hour)
		require.NoError(t, err)

		_, err = manager.CreateSession(context.Background(), "")
		require.EqualError(t, err, "generate failed")
	})

	t.Run("create session save error", func(t *testing.T) {
		manager, err := NewManager(&storagemock.Store{
			SaveFunc: func(context.Context, storage.Session) error {
				return errors.New("save failed")
			},
		}, time.Hour, 24*time.Hour)
		require.NoError(t, err)

		_, err = manager.CreateSession(context.Background(), testSessionA)
		require.EqualError(t, err, "save failed")
	})

	t.Run("list sessions resolve default error", func(t *testing.T) {
		manager, err := NewManager(&storagemock.Store{
			GetFunc: func(context.Context, string) (storage.Session, bool, error) {
				return storage.Session{}, false, errors.New("get failed")
			},
		}, time.Hour, 24*time.Hour)
		require.NoError(t, err)

		_, err = manager.ListSessions(context.Background())
		require.EqualError(t, err, "get failed")
	})

	t.Run("use default resolve error", func(t *testing.T) {
		manager, err := NewManager(&storagemock.Store{
			GetFunc: func(context.Context, string) (storage.Session, bool, error) {
				return storage.Session{}, false, errors.New("get failed")
			},
		}, time.Hour, 24*time.Hour)
		require.NoError(t, err)

		err = manager.UseSession(context.Background(), storage.DefaultSessionID)
		require.EqualError(t, err, "get failed")
	})

	t.Run("append default resolve error", func(t *testing.T) {
		manager, err := NewManager(&storagemock.Store{
			GetFunc: func(context.Context, string) (storage.Session, bool, error) {
				return storage.Session{}, false, errors.New("get failed")
			},
		}, time.Hour, 24*time.Hour)
		require.NoError(t, err)

		err = manager.AppendMessages(context.Background(), storage.DefaultSessionID, []morphmsg.Message{
			{Role: morphmsg.RoleUser, Content: "hello"},
		})
		require.EqualError(t, err, "get failed")
	})

	t.Run("delete session validation and errors", func(t *testing.T) {
		manager, err := NewManager(storagememory.NewStore(), time.Hour, 24*time.Hour)
		require.NoError(t, err)

		require.EqualError(t, manager.DeleteSession(context.Background(), ""), "session id is required")
		require.EqualError(t, manager.DeleteSession(context.Background(), storage.DefaultSessionID), "default session cannot be deleted")
		require.EqualError(t, manager.DeleteSession(context.Background(), testSessionA), "session not found")

		manager, err = NewManager(&storagemock.Store{
			DeleteFunc: func(context.Context, string) error {
				return errors.New("delete failed")
			},
		}, time.Hour, 24*time.Hour)
		require.NoError(t, err)

		require.EqualError(t, manager.DeleteSession(context.Background(), testSessionA), "delete failed")

		require.EqualError(t, manager.DeleteSession(context.Background(), "project-a"), "session id must be a valid ses_ nanoid")
	})

	t.Run("archive session validation and errors", func(t *testing.T) {
		manager, err := NewManager(storagememory.NewStore(), time.Hour, 24*time.Hour)
		require.NoError(t, err)

		require.EqualError(t, manager.ArchiveSession(context.Background(), ""), "session id is required")
		require.EqualError(t, manager.ArchiveSession(context.Background(), storage.DefaultSessionID), "default session cannot be archived")
		require.EqualError(t, manager.ArchiveSession(context.Background(), "project-a"), "session id must be a valid ses_ nanoid")

		manager, err = NewManager(&storagemock.Store{
			ArchiveFunc: func(context.Context, string, storage.SessionArchiveRequest) (storage.Session, error) {
				return storage.Session{}, errors.New("archive failed")
			},
		}, time.Hour, 24*time.Hour)
		require.NoError(t, err)

		require.EqualError(t, manager.ArchiveSession(context.Background(), testSessionA), "archive failed")
	})

	t.Run("unarchive session validation and errors", func(t *testing.T) {
		var nilManager *Manager
		_, err := nilManager.UnarchiveSession(context.Background(), testSessionA)
		require.EqualError(t, err, "state manager is required")

		manager, err := NewManager(storagememory.NewStore(), time.Hour, 24*time.Hour)
		require.NoError(t, err)

		_, err = manager.UnarchiveSession(context.Background(), "")
		require.EqualError(t, err, "session id is required")
		_, err = manager.UnarchiveSession(context.Background(), "project-a")
		require.EqualError(t, err, "session id must be a valid ses_ nanoid")
		_, err = manager.UnarchiveSession(context.Background(), testMissingSession)
		require.EqualError(t, err, "session not found")

		manager, err = NewManager(&storagemock.Store{
			UnarchiveFunc: func(context.Context, string) (storage.Session, error) {
				return storage.Session{}, errors.New("unarchive failed")
			},
		}, time.Hour, 24*time.Hour)
		require.NoError(t, err)

		_, err = manager.UnarchiveSession(context.Background(), testSessionA)
		require.EqualError(t, err, "unarchive failed")
	})

	t.Run("current session error", func(t *testing.T) {
		manager, err := NewManager(&storagemock.Store{
			CurrentFunc: func(context.Context) (string, bool, error) {
				return "", false, errors.New("current failed")
			},
		}, time.Hour, 24*time.Hour)
		require.NoError(t, err)

		_, err = manager.CurrentSession(context.Background())
		require.EqualError(t, err, "current failed")
	})

	t.Run("resolve default save error", func(t *testing.T) {
		manager, err := NewManager(&storagemock.Store{
			SaveFunc: func(context.Context, storage.Session) error {
				return errors.New("save failed")
			},
		}, time.Hour, 24*time.Hour)
		require.NoError(t, err)

		_, err = manager.resolveDefaultSession(context.Background(), time.Now().UTC())
		require.EqualError(t, err, "save failed")
	})

}

func TestManager_UpdateLastPromptTokens(t *testing.T) {
	var saved storage.Session

	manager, err := NewManager(&storagemock.Store{
		GetFunc: func(context.Context, string) (storage.Session, bool, error) {
			return storage.Session{ID: testSessionA, UpdatedAt: time.Now().UTC()}, true, nil
		},
		SaveFunc: func(_ context.Context, session storage.Session) error {
			saved = session
			return nil
		},
	}, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	require.NoError(t, manager.UpdateLastPromptTokens(context.Background(), testSessionA, 42))
	require.Equal(t, 42, saved.LastPromptTokens)

	require.NoError(t, manager.UpdateLastPromptTokens(context.Background(), testSessionA, 0))
	require.NoError(t, manager.UpdateLastPromptTokens(context.Background(), testSessionA, -1))
}

func TestManager_UpdateCheckpoints_ForwardsToStore(t *testing.T) {
	var gotID string
	var gotPatch storage.CheckpointPatch

	manager, err := NewManager(&storagemock.Store{
		UpdateCheckpointsFunc: func(_ context.Context, id string, patch storage.CheckpointPatch) error {
			gotID = id
			gotPatch = patch
			return nil
		},
	}, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	offset := 42
	require.NoError(t, manager.UpdateCheckpoints(context.Background(), "  "+testSessionA+"  ", storage.CheckpointPatch{
		ReflectionOffset: &offset,
	}))
	require.Equal(t, testSessionA, gotID)
	require.NotNil(t, gotPatch.ReflectionOffset)
	require.Equal(t, 42, *gotPatch.ReflectionOffset)

	var nilManager *Manager
	require.EqualError(t, nilManager.UpdateCheckpoints(context.Background(), testSessionA, storage.CheckpointPatch{}), "state manager is required")
}

func TestManager_PatchSession_ForwardsFocusedPatch(t *testing.T) {
	effort := "high"
	store := &storagemock.Store{
		PatchFunc: func(_ context.Context, id string, patch storage.SessionPatch) (storage.Session, error) {
			require.Equal(t, testSessionA, id)
			require.NotNil(t, patch.ReasoningEffortOverride)
			require.Equal(t, effort, *patch.ReasoningEffortOverride)
			return storage.Session{ID: id, ReasoningEffortOverride: effort}, nil
		},
	}
	manager, err := NewManager(store, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	patched, err := manager.PatchSession(
		context.Background(),
		"  "+testSessionA+"  ",
		storage.SessionPatch{ReasoningEffortOverride: &effort},
	)
	require.NoError(t, err)
	require.Equal(t, effort, patched.ReasoningEffortOverride)

	var nilManager *Manager
	_, err = nilManager.PatchSession(context.Background(), testSessionA, storage.SessionPatch{})
	require.EqualError(t, err, "state manager is required")
}

func TestManager_UpdateLastPromptTokensReturnsSaveError(t *testing.T) {
	manager, err := NewManager(&storagemock.Store{
		GetFunc: func(context.Context, string) (storage.Session, bool, error) {
			return storage.Session{ID: testSessionA, UpdatedAt: time.Now().UTC()}, true, nil
		},
		SaveFunc: func(context.Context, storage.Session) error {
			return errors.New("save failed")
		},
	}, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	err = manager.UpdateLastPromptTokens(context.Background(), testSessionA, 42)
	require.EqualError(t, err, "save failed")
}

func TestManager_SummaryLifecycleMethods(t *testing.T) {
	t.Run("nil manager", func(t *testing.T) {
		var manager *Manager

		require.EqualError(t, manager.SaveSummary(context.Background(), storage.SessionSummary{}), "state manager is required")

		summary, ok, err := manager.GetSummary(context.Background(), testSessionA)
		require.EqualError(t, err, "state manager is required")
		require.False(t, ok)
		require.Equal(t, storage.SessionSummary{}, summary)

		require.EqualError(t, manager.DeleteSummary(context.Background(), testSessionA), "state manager is required")
	})

	t.Run("save summary forwards to store", func(t *testing.T) {
		expected := storage.SessionSummary{
			SessionID:      testSessionA,
			SessionSummary: "summary",
		}

		var captured storage.SessionSummary
		manager, err := NewManager(&storagemock.Store{
			SaveSummaryFunc: func(_ context.Context, summary storage.SessionSummary) error {
				captured = summary
				return nil
			},
		}, time.Hour, 24*time.Hour)
		require.NoError(t, err)

		require.NoError(t, manager.SaveSummary(context.Background(), expected))
		require.Equal(t, expected, captured)
	})

	t.Run("save summary returns store error", func(t *testing.T) {
		manager, err := NewManager(&storagemock.Store{
			SaveSummaryFunc: func(context.Context, storage.SessionSummary) error {
				return errors.New("save summary failed")
			},
		}, time.Hour, 24*time.Hour)
		require.NoError(t, err)

		err = manager.SaveSummary(context.Background(), storage.SessionSummary{})
		require.EqualError(t, err, "save summary failed")
	})

	t.Run("get summary trims session id and returns result", func(t *testing.T) {
		expected := storage.SessionSummary{
			SessionID:      testSessionA,
			SessionSummary: "summary",
		}

		var capturedID string
		manager, err := NewManager(&storagemock.Store{
			GetSummaryFunc: func(_ context.Context, sessionID string) (storage.SessionSummary, bool, error) {
				capturedID = sessionID
				return expected, true, nil
			},
		}, time.Hour, 24*time.Hour)
		require.NoError(t, err)

		summary, ok, err := manager.GetSummary(context.Background(), "  "+testSessionA+"  ")
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, expected, summary)
		require.Equal(t, testSessionA, capturedID)
	})

	t.Run("get summary returns store error", func(t *testing.T) {
		manager, err := NewManager(&storagemock.Store{
			GetSummaryFunc: func(context.Context, string) (storage.SessionSummary, bool, error) {
				return storage.SessionSummary{}, false, errors.New("get summary failed")
			},
		}, time.Hour, 24*time.Hour)
		require.NoError(t, err)

		summary, ok, err := manager.GetSummary(context.Background(), testSessionA)
		require.EqualError(t, err, "get summary failed")
		require.False(t, ok)
		require.Equal(t, storage.SessionSummary{}, summary)
	})

	t.Run("delete summary trims session id", func(t *testing.T) {
		var capturedID string
		manager, err := NewManager(&storagemock.Store{
			DeleteSummaryFunc: func(_ context.Context, sessionID string) error {
				capturedID = sessionID
				return nil
			},
		}, time.Hour, 24*time.Hour)
		require.NoError(t, err)

		require.NoError(t, manager.DeleteSummary(context.Background(), "  "+testSessionA+"  "))
		require.Equal(t, testSessionA, capturedID)
	})

	t.Run("delete summary returns store error", func(t *testing.T) {
		manager, err := NewManager(&storagemock.Store{
			DeleteSummaryFunc: func(context.Context, string) error {
				return errors.New("delete summary failed")
			},
		}, time.Hour, 24*time.Hour)
		require.NoError(t, err)

		err = manager.DeleteSummary(context.Background(), testSessionA)
		require.EqualError(t, err, "delete summary failed")
	})
}

func TestManager_UpdateLastPromptTokens_Errors(t *testing.T) {
	t.Run("empty id", func(t *testing.T) {
		manager, err := NewManager(storagememory.NewStore(), time.Hour, 24*time.Hour)
		require.NoError(t, err)

		err = manager.UpdateLastPromptTokens(context.Background(), "", 42)
		require.EqualError(t, err, "session id is required")
	})

	t.Run("get error", func(t *testing.T) {
		manager, err := NewManager(&storagemock.Store{
			GetFunc: func(context.Context, string) (storage.Session, bool, error) {
				return storage.Session{}, false, errors.New("get failed")
			},
		}, time.Hour, 24*time.Hour)
		require.NoError(t, err)

		err = manager.UpdateLastPromptTokens(context.Background(), testSessionA, 42)
		require.EqualError(t, err, "get failed")
	})

	t.Run("session not found", func(t *testing.T) {
		manager, err := NewManager(&storagemock.Store{}, time.Hour, 24*time.Hour)
		require.NoError(t, err)

		err = manager.UpdateLastPromptTokens(context.Background(), testSessionA, 42)
		require.EqualError(t, err, "session not found")
	})
}

type repairVectorStoreStub struct {
	storagemock.Store
	opts   search.VectorRepairOptions
	result search.VectorRepairResult
	err    error
}

func (s *repairVectorStoreStub) RepairVectorStore(
	_ context.Context,
	opts search.VectorRepairOptions,
) (search.VectorRepairResult, error) {
	s.opts = opts
	return s.result, s.err
}
