package agent

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/environment"
	envbudget "github.com/wandxy/morph/internal/environment/budget"
	"github.com/wandxy/morph/internal/mocks"
	models "github.com/wandxy/morph/internal/model"
	"github.com/wandxy/morph/internal/permissions"
	storage "github.com/wandxy/morph/internal/state/core"
	statemanager "github.com/wandxy/morph/internal/state/manager"
	"github.com/wandxy/morph/internal/state/storememory"
	"github.com/wandxy/morph/internal/state/storesqlite"
	"github.com/wandxy/morph/internal/tools"
	"github.com/wandxy/morph/internal/trace"
	agentcore "github.com/wandxy/morph/pkg/agent"
	morphmsg "github.com/wandxy/morph/pkg/agent/message"
	agentsession "github.com/wandxy/morph/pkg/agent/session"
	"github.com/wandxy/morph/pkg/nanoid"
)

type sessionRunnerModelClient struct {
	mu       sync.Mutex
	requests []models.Request
	started  chan struct{}
	release  chan struct{}
	delta    string
}

type blockingSessionRunnerToolRegistry struct {
	*mocks.ToolRegistryStub
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *blockingSessionRunnerToolRegistry) Invoke(
	ctx context.Context,
	call tools.Call,
) (tools.Result, error) {
	r.once.Do(func() {
		close(r.started)
	})
	select {
	case <-ctx.Done():
		return tools.Result{}, ctx.Err()
	case <-r.release:
	}
	r.InvokeContext = ctx
	r.InvokeCall = call
	return tools.Result{Output: `{"ok":true}`}, nil
}

func (c *sessionRunnerModelClient) Complete(
	ctx context.Context,
	request models.Request,
) (*models.Response, error) {
	c.mu.Lock()
	call := len(c.requests)
	c.requests = append(c.requests, request)
	c.mu.Unlock()
	if call == 0 {
		close(c.started)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.release:
		}
	}
	return &models.Response{OutputText: "reply"}, nil
}

func (c *sessionRunnerModelClient) CompleteStream(
	ctx context.Context,
	request models.Request,
	onDelta func(models.StreamDelta),
) (*models.Response, error) {
	response, err := c.Complete(ctx, request)
	if err == nil && c.delta != "" {
		onDelta(models.StreamDelta{Channel: models.StreamChannelAssistant, Text: c.delta})
	}
	return response, err
}

func (c *sessionRunnerModelClient) requestCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.requests)
}

func (c *sessionRunnerModelClient) requestAt(index int) models.Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests[index]
}

func TestSessionRunner_ProcessesMemoryBackedQueue(t *testing.T) {
	originalOpen := OpenStateStore
	originalEnvironment := NewEnvironment
	t.Cleanup(func() {
		OpenStateStore = originalOpen
		NewEnvironment = originalEnvironment
	})

	store := storememory.NewStore()
	require.NoError(t, store.Save(context.Background(), storage.Session{
		ID:    storage.DefaultSessionID,
		Title: "Memory runner test",
	}))
	OpenStateStore = func(*config.Config, models.Client) (storage.Store, error) {
		return store, nil
	}
	NewEnvironment = func(context.Context, *config.Config) environment.Environment {
		return &mocks.EnvironmentStub{
			ToolRegistry:    &mocks.ToolRegistryStub{},
			IterationBudget: envbudget.New(3),
		}
	}
	stream := false
	release := make(chan struct{})
	close(release)
	client := &sessionRunnerModelClient{
		started: make(chan struct{}),
		release: release,
	}
	core := NewAgent(context.Background(), &config.Config{
		Platform: storage.SessionOriginSourceCLI,
		Models: config.ModelsConfig{Main: config.MainModelConfig{
			Name:            "gpt-5.5",
			Provider:        "openai",
			API:             models.APIOpenAIResponses,
			ContextLength:   8192,
			Stream:          &stream,
			ReasoningEffort: "high",
		}},
	}, client)
	require.NoError(t, core.Start(context.Background()))
	require.NoError(t, core.StartSessionRunner(context.Background()))
	t.Cleanup(func() {
		require.NoError(t, core.Close())
	})

	authorized := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor:     permissions.Actor{Kind: permissions.ActorLocalOwner},
		Surface:   permissions.SurfaceTUI,
		SessionID: storage.DefaultSessionID,
	})
	entry, err := core.EnqueueSessionMessage(authorized, agentsession.EnqueueRequest{
		SessionID:          storage.DefaultSessionID,
		Content:            "memory-backed",
		ClientSubmissionID: "memory-submission",
		DeliveryMode:       agentsession.DeliveryModeFollowUp,
		SteeringFallback:   agentsession.SteeringFallbackFollowUp,
	})
	require.NoError(t, err)

	state := waitForSessionRunnerState(t, core, authorized, func(state agentsession.ExecutionState) bool {
		for _, queued := range state.Queue {
			if queued.ID == entry.ID {
				return queued.Status == agentsession.QueueStatusCompleted
			}
		}
		return false
	})
	require.Nil(t, state.ActiveRun)
	require.Equal(t, 1, client.requestCount())
	request := client.requestAt(0)
	require.Equal(t, "openai", request.Provider)
	require.Equal(t, models.APIOpenAIResponses, request.API)
	require.Equal(t, "gpt-5.5", request.Model)
	require.Equal(t, &models.ReasoningOptions{Effort: "high", Summary: true}, request.Reasoning)

	messages, err := store.GetMessages(
		context.Background(),
		storage.DefaultSessionID,
		storage.MessageQueryOptions{},
	)
	require.NoError(t, err)
	require.Len(t, messages, 2)
	require.Equal(t, "memory-backed", messages[0].Content)
	require.Equal(t, "reply", messages[1].Content)
}

func TestAgent_SessionReasoningStateSetResetAndActiveSnapshot(t *testing.T) {
	ctx := context.Background()
	store := storememory.NewStore()
	require.NoError(t, store.Save(ctx, storage.Session{ID: storage.DefaultSessionID}))
	manager, err := statemanager.NewManager(store, time.Hour, time.Hour)
	require.NoError(t, err)
	core := &Agent{
		cfg: &config.Config{Models: config.ModelsConfig{Main: config.MainModelConfig{
			Name:            "gpt-5.5",
			Provider:        "openai",
			API:             models.APIOpenAIResponses,
			ReasoningEffort: "medium",
		}}},
		stateMgr:    manager,
		env:         &mocks.EnvironmentStub{},
		initialized: true,
	}
	authorized := permissions.WithContext(ctx, permissions.AuthorizationContext{
		Actor:     permissions.Actor{Kind: permissions.ActorLocalOwner},
		Surface:   permissions.SurfaceTUI,
		SessionID: storage.DefaultSessionID,
	})

	state, err := core.GetSessionExecutionState(authorized, storage.DefaultSessionID)
	require.NoError(t, err)
	require.True(t, state.Reasoning.Adjustable)
	require.Equal(t, agentsession.ReasoningEffort("medium"), state.Reasoning.EffectiveEffort)
	require.Equal(t, agentsession.ReasoningResolutionSourceProfileDefault, state.Reasoning.Source)

	expected := agentsession.ReasoningModelTuple{
		Provider: "openai",
		API:      models.APIOpenAIResponses,
		Model:    "gpt-5.5",
	}
	_, err = core.SetSessionReasoningEffort(
		authorized,
		agentsession.SetReasoningEffortRequest{
			SessionID:     storage.DefaultSessionID,
			ExpectedModel: expected,
			Effort:        "ultra",
		},
	)
	require.ErrorIs(t, err, agentsession.ErrReasoningUnsupported)
	persisted, err := manager.Resolve(ctx, storage.DefaultSessionID)
	require.NoError(t, err)
	require.Empty(t, persisted.ReasoningEffortOverride)

	settings, err := core.SetSessionReasoningEffort(
		authorized,
		agentsession.SetReasoningEffortRequest{
			SessionID:     storage.DefaultSessionID,
			ExpectedModel: expected,
			Effort:        "HIGH",
		},
	)
	require.NoError(t, err)
	require.Equal(t, agentsession.ReasoningEffort("high"), settings.SessionOverride)
	require.Equal(t, agentsession.ReasoningEffort("high"), settings.EffectiveEffort)

	core.setPendingModelSelection(models.Option{
		ID:       "gpt-5.4",
		Name:     "GPT-5.4",
		Provider: "openai",
		API:      models.APIOpenAIResponses,
	})
	_, err = core.SetSessionReasoningEffort(
		authorized,
		agentsession.SetReasoningEffortRequest{
			SessionID:     storage.DefaultSessionID,
			ExpectedModel: expected,
			Effort:        "low",
		},
	)
	require.ErrorIs(t, err, agentsession.ErrReasoningStaleTuple)
	require.Equal(t, "gpt-5.5", core.getReasoningClaimContext().Model.Model)
	core.modelSelectionMu.Lock()
	core.pendingModel = nil
	core.modelSelectionMu.Unlock()

	_, err = core.SetSessionReasoningEffort(
		authorized,
		agentsession.SetReasoningEffortRequest{
			SessionID: storage.DefaultSessionID,
			ExpectedModel: agentsession.ReasoningModelTuple{
				Provider: "openai",
				API:      models.APIOpenAIResponses,
				Model:    "stale-model",
			},
			Effort: "low",
		},
	)
	require.ErrorIs(t, err, agentsession.ErrReasoningStaleTuple)
	persisted, err = manager.Resolve(ctx, storage.DefaultSessionID)
	require.NoError(t, err)
	require.Equal(t, "high", persisted.ReasoningEffortOverride)

	_, err = store.ReconcileActiveRuns(ctx, "generation-reasoning-state")
	require.NoError(t, err)
	_, err = store.EnqueueMessage(ctx, agentsession.EnqueueRequest{
		ID:                 nanoid.MustFromSeed("qmsg_", "reasoning-state", "QueueSeed"),
		SessionID:          storage.DefaultSessionID,
		Content:            "reason",
		ClientSubmissionID: "reasoning-state",
		DeliveryMode:       agentsession.DeliveryModeFollowUp,
	})
	require.NoError(t, err)
	_, run, claimed, err := store.ClaimNextFollowUp(ctx, agentsession.ClaimRequest{
		SessionID:  storage.DefaultSessionID,
		RunID:      nanoid.MustFromSeed("run_", "reasoning-state", "RunSeed"),
		Generation: "generation-reasoning-state",
		Reasoning:  core.getReasoningClaimContext(),
	})
	require.NoError(t, err)
	require.True(t, claimed)

	state, err = core.GetSessionExecutionState(authorized, storage.DefaultSessionID)
	require.NoError(t, err)
	require.Equal(t, run.Reasoning, *state.Reasoning.ActiveRunSnapshot)

	settings, err = core.SetSessionReasoningEffort(
		authorized,
		agentsession.SetReasoningEffortRequest{
			SessionID:     storage.DefaultSessionID,
			ExpectedModel: expected,
			Reset:         true,
		},
	)
	require.NoError(t, err)
	require.Empty(t, settings.SessionOverride)
	require.Equal(t, agentsession.ReasoningEffort("medium"), settings.EffectiveEffort)
	require.Equal(t, agentsession.ReasoningEffort("high"), settings.ActiveRunSnapshot.Effort)
}

func TestAgent_SetSessionReasoningEffortRejectsInvalidAndUnavailable(t *testing.T) {
	store := storememory.NewStore()
	require.NoError(t, store.Save(context.Background(), storage.Session{
		ID:                      storage.DefaultSessionID,
		ReasoningEffortOverride: "high",
	}))
	manager, err := statemanager.NewManager(store, time.Hour, time.Hour)
	require.NoError(t, err)
	core := &Agent{
		cfg: &config.Config{Models: config.ModelsConfig{Main: config.MainModelConfig{
			Name:     "gpt-4o",
			Provider: "openai",
			API:      models.APIOpenAIResponses,
		}}},
		stateMgr:    manager,
		env:         &mocks.EnvironmentStub{},
		initialized: true,
	}
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor:     permissions.Actor{Kind: permissions.ActorLocalOwner},
		Surface:   permissions.SurfaceTUI,
		SessionID: storage.DefaultSessionID,
	})
	expected := agentsession.ReasoningModelTuple{
		Provider: "openai",
		API:      models.APIOpenAIResponses,
		Model:    "gpt-4o",
	}

	_, err = core.SetSessionReasoningEffort(ctx, agentsession.SetReasoningEffortRequest{
		SessionID: storage.DefaultSessionID, ExpectedModel: expected,
	})
	require.ErrorIs(t, err, agentsession.ErrReasoningInvalid)
	_, err = core.SetSessionReasoningEffort(ctx, agentsession.SetReasoningEffortRequest{
		SessionID: storage.DefaultSessionID, ExpectedModel: expected, Effort: "low", Reset: true,
	})
	require.ErrorIs(t, err, agentsession.ErrReasoningInvalid)
	_, err = core.SetSessionReasoningEffort(ctx, agentsession.SetReasoningEffortRequest{
		SessionID: storage.DefaultSessionID, ExpectedModel: expected, Effort: "low",
	})
	require.ErrorIs(t, err, agentsession.ErrReasoningUnavailable)

	settings, err := core.SetSessionReasoningEffort(ctx, agentsession.SetReasoningEffortRequest{
		SessionID: storage.DefaultSessionID, ExpectedModel: expected, Reset: true,
	})
	require.NoError(t, err)
	require.Empty(t, settings.SessionOverride)
	persisted, err := manager.Resolve(ctx, storage.DefaultSessionID)
	require.NoError(t, err)
	require.Empty(t, persisted.ReasoningEffortOverride)
}

func TestSessionRunner_AcceptanceOutlivesObserverAndInterruptPreservesFollowUps(t *testing.T) {
	originalOpen := OpenStateStore
	originalEnvironment := NewEnvironment
	t.Cleanup(func() {
		OpenStateStore = originalOpen
		NewEnvironment = originalEnvironment
	})

	store, err := storesqlite.NewStore(t.TempDir() + "/session.db")
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), storage.Session{
		ID:    storage.DefaultSessionID,
		Title: "Runner test",
	}))
	OpenStateStore = func(*config.Config, models.Client) (storage.Store, error) {
		return store, nil
	}
	NewEnvironment = func(context.Context, *config.Config) environment.Environment {
		return &mocks.EnvironmentStub{
			ToolRegistry:    &mocks.ToolRegistryStub{},
			IterationBudget: envbudget.New(3),
		}
	}
	stream := false
	client := &sessionRunnerModelClient{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	core := NewAgent(context.Background(), &config.Config{
		Platform: storage.SessionOriginSourceCLI,
		Models: config.ModelsConfig{Main: config.MainModelConfig{
			Name:          "model",
			API:           models.APIOpenAIResponses,
			ContextLength: 8192,
			Stream:        &stream,
		}},
	}, client)
	require.NoError(t, core.Start(context.Background()))
	require.NoError(t, core.StartSessionRunner(context.Background()))
	t.Cleanup(func() {
		require.NoError(t, core.Close())
	})

	authorized := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor:     permissions.Actor{Kind: permissions.ActorLocalOwner},
		Surface:   permissions.SurfaceTUI,
		SessionID: storage.DefaultSessionID,
	})
	first, err := core.EnqueueSessionMessage(authorized, agentsession.EnqueueRequest{
		SessionID:          storage.DefaultSessionID,
		Content:            "first",
		ClientSubmissionID: "submission-1",
		DeliveryMode:       agentsession.DeliveryModeFollowUp,
		SteeringFallback:   agentsession.SteeringFallbackFollowUp,
	})
	require.NoError(t, err)

	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("session runner did not start")
	}
	state := waitForSessionRunnerState(t, core, authorized, func(state agentsession.ExecutionState) bool {
		return state.ActiveRun != nil && state.ActiveRun.QueueEntryID == first.ID
	})

	observerContext, cancelObserver := context.WithCancel(authorized)
	observerDone := make(chan error, 1)
	go func() {
		observerDone <- core.ObserveSessionEvents(
			observerContext,
			storage.DefaultSessionID,
			state.Cursor,
			func(agentsession.Event) error { return nil },
		)
	}()
	cancelObserver()
	require.ErrorIs(t, <-observerDone, context.Canceled)
	state, err = core.GetSessionExecutionState(authorized, storage.DefaultSessionID)
	require.NoError(t, err)
	require.NotNil(t, state.ActiveRun)

	second, err := core.EnqueueSessionMessage(authorized, agentsession.EnqueueRequest{
		SessionID:          storage.DefaultSessionID,
		Content:            "second",
		ClientSubmissionID: "submission-2",
		DeliveryMode:       agentsession.DeliveryModeFollowUp,
		SteeringFallback:   agentsession.SteeringFallbackFollowUp,
	})
	require.NoError(t, err)
	third, err := core.EnqueueSessionMessage(authorized, agentsession.EnqueueRequest{
		SessionID:          storage.DefaultSessionID,
		Content:            "third",
		ClientSubmissionID: "submission-3",
		DeliveryMode:       agentsession.DeliveryModeFollowUp,
		SteeringFallback:   agentsession.SteeringFallbackFollowUp,
	})
	require.NoError(t, err)
	second, err = core.EditSessionQueueEntry(authorized, agentsession.QueueEditRequest{
		SessionID: storage.DefaultSessionID,
		EntryID:   second.ID,
		Content:   "revised second",
	})
	require.NoError(t, err)
	require.Equal(t, "revised second", second.Content)
	second, err = core.PromoteSessionQueueEntry(authorized, agentsession.QueueMutationRequest{
		SessionID: storage.DefaultSessionID,
		EntryID:   second.ID,
	})
	require.NoError(t, err)
	require.Positive(t, second.Priority)
	third, err = core.CancelSessionQueueEntry(authorized, agentsession.QueueMutationRequest{
		SessionID: storage.DefaultSessionID,
		EntryID:   third.ID,
	})
	require.NoError(t, err)
	require.Equal(t, agentsession.QueueStatusCancelled, third.Status)

	_, transitioned, err := core.InterruptSessionRun(authorized, storage.DefaultSessionID)
	require.NoError(t, err)
	require.True(t, transitioned)

	state = waitForSessionRunnerState(t, core, authorized, func(state agentsession.ExecutionState) bool {
		for _, entry := range state.Queue {
			if entry.ID == second.ID {
				return entry.Status == agentsession.QueueStatusCompleted
			}
		}
		return false
	})
	require.Nil(t, state.ActiveRun)
	require.Equal(t, 2, client.requestCount())
	secondRequest := client.requestAt(1)
	require.NotEmpty(t, secondRequest.Messages)
	require.Equal(t, "revised second", secondRequest.Messages[len(secondRequest.Messages)-1].Content)
	for _, entry := range state.Queue {
		if entry.ID == first.ID {
			require.Equal(t, agentsession.QueueStatusInterrupted, entry.Status)
		}
		if entry.ID == third.ID {
			require.Equal(t, agentsession.QueueStatusCancelled, entry.Status)
		}
	}
	traceResult, err := store.ListTraceEvents(context.Background(), storage.TraceQuery{
		SessionID: storage.DefaultSessionID,
		Types: []string{
			trace.EvtSessionQueueEnqueued,
			trace.EvtSessionQueueClaimed,
			trace.EvtSessionQueueInterrupted,
			trace.EvtSessionQueueCompleted,
			trace.EvtSessionQueueCancelled,
		},
	})
	require.NoError(t, err)
	eventTypes := make([]string, len(traceResult.Events))
	for index, event := range traceResult.Events {
		eventTypes[index] = event.Type
	}
	require.Subset(t, eventTypes, []string{
		trace.EvtSessionQueueEnqueued,
		trace.EvtSessionQueueClaimed,
		trace.EvtSessionQueueInterrupted,
		trace.EvtSessionQueueCompleted,
		trace.EvtSessionQueueCancelled,
	})
}

func TestSessionRunner_DeliversSteeringThroughRunnerToolBoundary(t *testing.T) {
	originalOpen := OpenStateStore
	originalEnvironment := NewEnvironment
	t.Cleanup(func() {
		OpenStateStore = originalOpen
		NewEnvironment = originalEnvironment
	})

	store, err := storesqlite.NewStore(t.TempDir() + "/session.db")
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), storage.Session{
		ID:    storage.DefaultSessionID,
		Title: "Steering boundary test",
	}))
	OpenStateStore = func(*config.Config, models.Client) (storage.Store, error) {
		return store, nil
	}
	registry := &blockingSessionRunnerToolRegistry{
		ToolRegistryStub: &mocks.ToolRegistryStub{
			Definitions: tools.Definitions{{Name: "time"}},
		},
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	NewEnvironment = func(context.Context, *config.Config) environment.Environment {
		return &mocks.EnvironmentStub{
			ToolRegistry:    registry,
			IterationBudget: envbudget.New(3),
		}
	}

	stream := false
	client := &mocks.ModelClientStub{Responses: []*models.Response{
		{
			RequiresToolCalls: true,
			ToolCalls: []models.ToolCall{{
				ID: "call_time", Name: "time", Input: "{}",
			}},
		},
		{OutputText: "corrected"},
	}}
	core := NewAgent(context.Background(), &config.Config{
		Platform: storage.SessionOriginSourceCLI,
		Models: config.ModelsConfig{Main: config.MainModelConfig{
			Name:          "model",
			API:           models.APIOpenAIResponses,
			ContextLength: 8192,
			Stream:        &stream,
		}},
	}, client)
	require.NoError(t, core.Start(context.Background()))
	require.NoError(t, core.StartSessionRunner(context.Background()))
	t.Cleanup(func() {
		require.NoError(t, core.Close())
	})

	authorized := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor:     permissions.Actor{Kind: permissions.ActorLocalOwner},
		Surface:   permissions.SurfaceTUI,
		SessionID: storage.DefaultSessionID,
	})
	primary, err := core.EnqueueSessionMessage(authorized, agentsession.EnqueueRequest{
		SessionID:          storage.DefaultSessionID,
		Content:            "what time is it?",
		ClientSubmissionID: "submission-run",
		DeliveryMode:       agentsession.DeliveryModeFollowUp,
		SteeringFallback:   agentsession.SteeringFallbackFollowUp,
	})
	require.NoError(t, err)
	select {
	case <-registry.started:
	case <-time.After(time.Second):
		t.Fatal("tool call did not start")
	}

	steering, err := core.EnqueueSessionMessage(authorized, agentsession.EnqueueRequest{
		SessionID:          storage.DefaultSessionID,
		Content:            "use UTC instead",
		ClientSubmissionID: "submission-steering",
		DeliveryMode:       agentsession.DeliveryModeSteering,
		SteeringFallback:   agentsession.SteeringFallbackReject,
	})
	require.NoError(t, err)
	require.NotEmpty(t, steering.TargetRunID)
	observerState, err := core.GetSessionExecutionState(authorized, storage.DefaultSessionID)
	require.NoError(t, err)
	errObservationComplete := errors.New("observation complete")
	var observed []string
	observationDone := make(chan error, 1)
	go func() {
		observationDone <- core.ObserveSessionEvents(
			authorized,
			storage.DefaultSessionID,
			observerState.Cursor,
			func(event agentsession.Event) error {
				if event.Progress != nil && event.Progress.TraceEvent != nil {
					observed = append(observed, event.Progress.TraceEvent.Type)
				}
				if event.Queue != nil &&
					event.Queue.ID == primary.ID &&
					event.Queue.Status == agentsession.QueueStatusCompleted {
					observed = append(observed, "terminal")
					return errObservationComplete
				}
				return nil
			},
		)
	}()
	close(registry.release)

	state := waitForSessionRunnerState(t, core, authorized, func(state agentsession.ExecutionState) bool {
		return state.ActiveRun == nil &&
			getSessionRunnerQueueEntry(t, state.Queue, steering.ID).Status ==
				agentsession.QueueStatusDelivered
	})
	require.Nil(t, state.ActiveRun)
	require.Len(t, client.Requests, 2)
	secondRequest := client.Requests[1].Messages
	require.GreaterOrEqual(t, len(secondRequest), 4)
	require.Equal(t, morphmsg.RoleTool, secondRequest[len(secondRequest)-2].Role)
	require.Equal(t, "use UTC instead", secondRequest[len(secondRequest)-1].Content)
	var progressTypes []string
	for _, progress := range state.Progress {
		if progress.TraceEvent != nil {
			progressTypes = append(progressTypes, progress.TraceEvent.Type)
		}
	}
	require.Contains(t, progressTypes, trace.EvtToolInvocationStarted)
	require.Contains(t, progressTypes, trace.EvtToolInvocationCompleted)
	require.ErrorIs(t, <-observationDone, errObservationComplete)
	require.Contains(t, observed, trace.EvtToolInvocationCompleted)
	require.Contains(t, observed, "terminal")
	require.Less(
		t,
		slices.Index(observed, trace.EvtToolInvocationCompleted),
		slices.Index(observed, "terminal"),
	)
}

func TestSessionRunner_StartConsumesPendingWorkFromReopenedStore(t *testing.T) {
	originalOpen := OpenStateStore
	originalEnvironment := NewEnvironment
	t.Cleanup(func() {
		OpenStateStore = originalOpen
		NewEnvironment = originalEnvironment
	})

	path := t.TempDir() + "/session.db"
	store, err := storesqlite.NewStore(path)
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), storage.Session{
		ID:    storage.DefaultSessionID,
		Title: "Restart recovery test",
	}))
	abandoned, err := store.EnqueueMessage(context.Background(), agentsession.EnqueueRequest{
		ID:                 "qmsg_abandoned",
		SessionID:          storage.DefaultSessionID,
		Content:            "abandoned work",
		ClientSubmissionID: "submission-abandoned",
		DeliveryMode:       agentsession.DeliveryModeFollowUp,
		SteeringFallback:   agentsession.SteeringFallbackFollowUp,
	})
	require.NoError(t, err)
	_, err = store.ReconcileActiveRuns(context.Background(), "old-generation")
	require.NoError(t, err)
	_, abandonedRun, claimed, err := store.ClaimNextFollowUp(
		context.Background(),
		agentsession.ClaimRequest{
			SessionID:  storage.DefaultSessionID,
			RunID:      "run_abandoned",
			Generation: "old-generation",
		},
	)
	require.NoError(t, err)
	require.True(t, claimed)
	require.Equal(t, abandoned.ID, abandonedRun.QueueEntryID)
	entry, err := store.EnqueueMessage(context.Background(), agentsession.EnqueueRequest{
		ID:                 "qmsg_restart",
		SessionID:          storage.DefaultSessionID,
		Content:            "resume pending work",
		ClientSubmissionID: "submission-restart",
		DeliveryMode:       agentsession.DeliveryModeFollowUp,
		SteeringFallback:   agentsession.SteeringFallbackFollowUp,
	})
	require.NoError(t, err)
	OpenStateStore = func(*config.Config, models.Client) (storage.Store, error) {
		return store, nil
	}
	NewEnvironment = func(context.Context, *config.Config) environment.Environment {
		return &mocks.EnvironmentStub{
			ToolRegistry:    &mocks.ToolRegistryStub{},
			IterationBudget: envbudget.New(3),
		}
	}

	stream := false
	client := &sessionRunnerModelClient{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	core := NewAgent(context.Background(), &config.Config{
		Platform: storage.SessionOriginSourceCLI,
		Models: config.ModelsConfig{Main: config.MainModelConfig{
			Name:          "model",
			API:           models.APIOpenAIResponses,
			ContextLength: 8192,
			Stream:        &stream,
		}},
	}, client)
	require.NoError(t, core.Start(context.Background()))
	require.NoError(t, core.StartSessionRunner(context.Background()))
	t.Cleanup(func() {
		require.NoError(t, core.Close())
	})

	authorized := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor:     permissions.Actor{Kind: permissions.ActorLocalOwner},
		Surface:   permissions.SurfaceTUI,
		SessionID: storage.DefaultSessionID,
	})
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("reopened pending work did not start")
	}
	state := waitForSessionRunnerState(t, core, authorized, func(state agentsession.ExecutionState) bool {
		return state.ActiveRun != nil && state.ActiveRun.QueueEntryID == entry.ID
	})
	require.Equal(t, agentsession.QueueStatusActive, getSessionRunnerQueueEntry(t, state.Queue, entry.ID).Status)
	require.Equal(
		t,
		agentsession.QueueStatusInterrupted,
		getSessionRunnerQueueEntry(t, state.Queue, abandoned.ID).Status,
	)

	close(client.release)
	waitForSessionRunnerState(t, core, authorized, func(state agentsession.ExecutionState) bool {
		return state.ActiveRun == nil &&
			getSessionRunnerQueueEntry(t, state.Queue, entry.ID).Status ==
				agentsession.QueueStatusCompleted
	})
	traceResult, err := store.ListTraceEvents(context.Background(), storage.TraceQuery{
		SessionID: storage.DefaultSessionID,
		Types:     []string{trace.EvtSessionQueueInterrupted},
	})
	require.NoError(t, err)
	require.Len(t, traceResult.Events, 1)
}

func TestSessionRunner_StopsAfterGenerationBecomesStale(t *testing.T) {
	originalOpen := OpenStateStore
	originalEnvironment := NewEnvironment
	t.Cleanup(func() {
		OpenStateStore = originalOpen
		NewEnvironment = originalEnvironment
	})

	store, err := storesqlite.NewStore(t.TempDir() + "/session.db")
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), storage.Session{
		ID: storage.DefaultSessionID,
	}))
	_, err = store.ReconcileActiveRuns(context.Background(), "current-generation")
	require.NoError(t, err)
	OpenStateStore = func(*config.Config, models.Client) (storage.Store, error) {
		return store, nil
	}
	NewEnvironment = func(context.Context, *config.Config) environment.Environment {
		return &mocks.EnvironmentStub{
			ToolRegistry:    &mocks.ToolRegistryStub{},
			IterationBudget: envbudget.New(3),
		}
	}

	stream := false
	core := NewAgent(context.Background(), &config.Config{
		Platform: storage.SessionOriginSourceCLI,
		Models: config.ModelsConfig{Main: config.MainModelConfig{
			Name:          "model",
			API:           models.APIOpenAIResponses,
			ContextLength: 8192,
			Stream:        &stream,
		}},
	}, &mocks.ModelClientStub{})
	require.NoError(t, core.Start(context.Background()))
	t.Cleanup(func() {
		require.NoError(t, core.Close())
	})
	core.runnerGeneration = "stale-generation"
	runner := &sessionRunner{
		agent:     core,
		sessionID: storage.DefaultSessionID,
		wake:      make(chan struct{}, 1),
	}
	runner.wake <- struct{}{}
	done := make(chan struct{})
	go func() {
		runner.run(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stale runner did not stop")
	}
}

func TestSessionRunner_SteerQueueEntryDelegatesAndWakesRunner(t *testing.T) {
	store := storememory.NewStore()
	require.NoError(t, store.Save(context.Background(), storage.Session{
		ID: storage.DefaultSessionID,
	}))
	manager, err := statemanager.NewManager(store, time.Hour, time.Hour)
	require.NoError(t, err)
	core := &Agent{
		initialized: true,
		env:         &mocks.EnvironmentStub{},
		stateMgr:    manager,
	}
	entry, err := core.EnqueueSessionMessage(
		context.Background(),
		agentsession.EnqueueRequest{
			SessionID:          storage.DefaultSessionID,
			Content:            "change direction",
			ClientSubmissionID: "submission-steer",
			DeliveryMode:       agentsession.DeliveryModeFollowUp,
			SteeringFallback:   agentsession.SteeringFallbackFollowUp,
		},
	)
	require.NoError(t, err)

	runnerContext, cancelRunner := context.WithCancel(context.Background())
	t.Cleanup(cancelRunner)
	runner := &sessionRunner{
		agent:     core,
		sessionID: storage.DefaultSessionID,
		wake:      make(chan struct{}, 1),
	}
	core.runnerCtx = runnerContext
	core.sessionRunners = map[string]*sessionRunner{
		storage.DefaultSessionID: runner,
	}

	steering, err := core.SteerSessionQueueEntry(
		context.Background(),
		agentsession.QueueMutationRequest{
			SessionID: storage.DefaultSessionID,
			EntryID:   entry.ID,
		},
	)

	require.NoError(t, err)
	require.Equal(t, entry.ID, steering.ID)
	require.Equal(t, agentsession.DeliveryModeSteering, steering.RequestedDeliveryMode)
	select {
	case <-runner.wake:
	case <-time.After(time.Second):
		t.Fatal("steering mutation did not wake the session runner")
	}
}

func TestSessionRunner_TwoObserversReceiveLiveProgressWithoutOwningRun(t *testing.T) {
	originalOpen := OpenStateStore
	originalEnvironment := NewEnvironment
	t.Cleanup(func() {
		OpenStateStore = originalOpen
		NewEnvironment = originalEnvironment
	})

	store, err := storesqlite.NewStore(t.TempDir() + "/session.db")
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), storage.Session{
		ID:    storage.DefaultSessionID,
		Title: "Progress test",
	}))
	OpenStateStore = func(*config.Config, models.Client) (storage.Store, error) {
		return store, nil
	}
	NewEnvironment = func(context.Context, *config.Config) environment.Environment {
		return &mocks.EnvironmentStub{
			ToolRegistry:    &mocks.ToolRegistryStub{},
			IterationBudget: envbudget.New(3),
		}
	}
	stream := true
	client := &sessionRunnerModelClient{
		started: make(chan struct{}),
		release: make(chan struct{}),
		delta:   "live reply",
	}
	core := NewAgent(context.Background(), &config.Config{
		Platform: storage.SessionOriginSourceCLI,
		Models: config.ModelsConfig{Main: config.MainModelConfig{
			Name:          "model",
			API:           models.APIOpenAIResponses,
			ContextLength: 8192,
			Stream:        &stream,
		}},
	}, client)
	require.NoError(t, core.Start(context.Background()))
	require.NoError(t, core.StartSessionRunner(context.Background()))
	t.Cleanup(func() {
		require.NoError(t, core.Close())
	})

	authorized := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor:     permissions.Actor{Kind: permissions.ActorLocalOwner},
		Surface:   permissions.SurfaceTUI,
		SessionID: storage.DefaultSessionID,
	})
	_, err = core.EnqueueSessionMessage(authorized, agentsession.EnqueueRequest{
		SessionID:          storage.DefaultSessionID,
		Content:            "stream",
		ClientSubmissionID: "submission-progress",
		DeliveryMode:       agentsession.DeliveryModeFollowUp,
		SteeringFallback:   agentsession.SteeringFallbackFollowUp,
	})
	require.NoError(t, err)
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("session runner did not start")
	}
	state := waitForSessionRunnerState(t, core, authorized, func(state agentsession.ExecutionState) bool {
		return state.ActiveRun != nil
	})

	observerContext, cancelObservers := context.WithCancel(authorized)
	t.Cleanup(cancelObservers)
	progress := []chan string{make(chan string, 1), make(chan string, 1)}
	observerErrors := make(chan error, len(progress))
	for index := range progress {
		go func() {
			observerErrors <- core.ObserveSessionEvents(
				observerContext,
				storage.DefaultSessionID,
				state.Cursor,
				func(event agentsession.Event) error {
					if event.Progress != nil && event.Progress.Text != "" {
						progress[index] <- event.Progress.Text
					}
					return nil
				},
			)
		}()
	}
	waitForSessionProgressObservers(t, core, storage.DefaultSessionID, len(progress))
	close(client.release)

	for _, observed := range progress {
		select {
		case text := <-observed:
			require.Equal(t, "live reply", text)
		case <-time.After(time.Second):
			t.Fatal("observer did not receive live progress")
		}
	}
	cancelObservers()
	for range progress {
		require.ErrorIs(t, <-observerErrors, context.Canceled)
	}

	state = waitForSessionRunnerState(t, core, authorized, func(state agentsession.ExecutionState) bool {
		return state.ActiveRun == nil
	})
	var textProgress *agentsession.ProgressEvent
	for index := range state.Progress {
		if state.Progress[index].Text == "live reply" {
			textProgress = &state.Progress[index]
			break
		}
	}
	require.NotNil(t, textProgress)
	require.NotEmpty(t, textProgress.RunID)
	require.NotEmpty(t, textProgress.QueueEntryID)
	require.Equal(t, agentcore.EventKindTextDelta, textProgress.Kind)
	require.Equal(t, string(models.StreamChannelAssistant), textProgress.Channel)
	require.Positive(t, textProgress.Sequence)
}

func TestSessionRunner_OrdinaryAgentStartDoesNotReconcileOrConsumeInbox(t *testing.T) {
	originalOpen := OpenStateStore
	originalEnvironment := NewEnvironment
	t.Cleanup(func() {
		OpenStateStore = originalOpen
		NewEnvironment = originalEnvironment
	})

	path := t.TempDir() + "/session.db"
	ownerStore, err := storesqlite.NewStore(path)
	require.NoError(t, err)
	ordinaryStore, err := storesqlite.NewStore(path)
	require.NoError(t, err)
	require.NoError(t, ownerStore.Save(context.Background(), storage.Session{
		ID:    storage.DefaultSessionID,
		Title: "Runner ownership test",
	}))
	stores := []storage.Store{ownerStore, ordinaryStore}
	var storeIndex int
	OpenStateStore = func(*config.Config, models.Client) (storage.Store, error) {
		store := stores[storeIndex]
		storeIndex++
		return store, nil
	}
	NewEnvironment = func(context.Context, *config.Config) environment.Environment {
		return &mocks.EnvironmentStub{
			ToolRegistry:    &mocks.ToolRegistryStub{},
			IterationBudget: envbudget.New(3),
		}
	}

	stream := false
	client := &sessionRunnerModelClient{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	cfg := &config.Config{
		Platform: storage.SessionOriginSourceCLI,
		Models: config.ModelsConfig{Main: config.MainModelConfig{
			Name:          "model",
			API:           models.APIOpenAIResponses,
			ContextLength: 8192,
			Stream:        &stream,
		}},
	}
	owner := NewAgent(context.Background(), cfg, client)
	require.NoError(t, owner.Start(context.Background()))
	require.NoError(t, owner.StartSessionRunner(context.Background()))
	t.Cleanup(func() {
		close(client.release)
		require.NoError(t, owner.Close())
	})

	authorized := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor:     permissions.Actor{Kind: permissions.ActorLocalOwner},
		Surface:   permissions.SurfaceTUI,
		SessionID: storage.DefaultSessionID,
	})
	entry, err := owner.EnqueueSessionMessage(authorized, agentsession.EnqueueRequest{
		SessionID:          storage.DefaultSessionID,
		Content:            "first",
		ClientSubmissionID: "submission-owner",
		DeliveryMode:       agentsession.DeliveryModeFollowUp,
		SteeringFallback:   agentsession.SteeringFallbackFollowUp,
	})
	require.NoError(t, err)
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("session runner did not start")
	}
	waitForSessionRunnerState(t, owner, authorized, func(state agentsession.ExecutionState) bool {
		return state.ActiveRun != nil && state.ActiveRun.QueueEntryID == entry.ID
	})

	ordinary := NewAgent(context.Background(), cfg, &mocks.ModelClientStub{})
	require.NoError(t, ordinary.Start(context.Background()))
	t.Cleanup(func() {
		require.NoError(t, ordinary.Close())
	})

	state, err := owner.GetSessionExecutionState(authorized, storage.DefaultSessionID)
	require.NoError(t, err)
	require.NotNil(t, state.ActiveRun)
	require.Equal(t, entry.ID, state.ActiveRun.QueueEntryID)
}

func TestSessionRunner_SerializesQueuedAndDirectTurns(t *testing.T) {
	originalOpen := OpenStateStore
	originalEnvironment := NewEnvironment
	t.Cleanup(func() {
		OpenStateStore = originalOpen
		NewEnvironment = originalEnvironment
	})

	store, err := storesqlite.NewStore(t.TempDir() + "/session.db")
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), storage.Session{
		ID:    storage.DefaultSessionID,
		Title: "Turn serialization test",
	}))
	OpenStateStore = func(*config.Config, models.Client) (storage.Store, error) {
		return store, nil
	}
	NewEnvironment = func(context.Context, *config.Config) environment.Environment {
		return &mocks.EnvironmentStub{
			ToolRegistry:    &mocks.ToolRegistryStub{},
			IterationBudget: envbudget.New(3),
		}
	}

	stream := false
	client := &sessionRunnerModelClient{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	core := NewAgent(context.Background(), &config.Config{
		Platform: storage.SessionOriginSourceCLI,
		Models: config.ModelsConfig{Main: config.MainModelConfig{
			Name:          "model",
			API:           models.APIOpenAIResponses,
			ContextLength: 8192,
			Stream:        &stream,
		}},
	}, client)
	require.NoError(t, core.Start(context.Background()))
	require.NoError(t, core.StartSessionRunner(context.Background()))
	t.Cleanup(func() {
		require.NoError(t, core.Close())
	})

	authorized := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor:     permissions.Actor{Kind: permissions.ActorLocalOwner},
		Surface:   permissions.SurfaceTUI,
		SessionID: storage.DefaultSessionID,
	})

	directDone := make(chan error, 1)
	go func() {
		_, respondErr := core.Respond(authorized, "gateway", agentcore.RespondOptions{
			SessionID: storage.DefaultSessionID,
		})
		directDone <- respondErr
	}()
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("direct turn did not start")
	}

	entry, err := core.EnqueueSessionMessage(authorized, agentsession.EnqueueRequest{
		SessionID:          storage.DefaultSessionID,
		Content:            "queued",
		ClientSubmissionID: "submission-queued",
		DeliveryMode:       agentsession.DeliveryModeFollowUp,
		SteeringFallback:   agentsession.SteeringFallbackFollowUp,
	})
	require.NoError(t, err)
	require.Never(t, func() bool {
		return client.requestCount() > 1
	}, 100*time.Millisecond, 10*time.Millisecond)
	state, err := core.GetSessionExecutionState(authorized, storage.DefaultSessionID)
	require.NoError(t, err)
	require.Nil(t, state.ActiveRun)
	require.Equal(
		t,
		agentsession.QueueStatusPending,
		getSessionRunnerQueueEntry(t, state.Queue, entry.ID).Status,
	)

	close(client.release)
	require.NoError(t, <-directDone)
	require.Eventually(t, func() bool {
		return client.requestCount() == 2
	}, time.Second, 10*time.Millisecond)
	waitForSessionRunnerState(t, core, authorized, func(state agentsession.ExecutionState) bool {
		queued := getSessionRunnerQueueEntry(t, state.Queue, entry.ID)
		return queued.Status == agentsession.QueueStatusCompleted
	})
}

func TestSessionRunner_ProgressWakeReplaysEveryBufferedEvent(t *testing.T) {
	core := &Agent{}
	wake, unsubscribe := core.subscribeSessionProgress(storage.DefaultSessionID)
	defer unsubscribe()

	for index := range 100 {
		core.publishSessionProgress(
			storage.DefaultSessionID,
			"run_test",
			"qmsg_test",
			agentcore.Event{Kind: agentcore.EventKindTextDelta, Text: string(rune('a' + index%26))},
		)
	}
	select {
	case <-wake:
	case <-time.After(time.Second):
		t.Fatal("progress observer was not notified")
	}
	progress, err := core.getSessionProgressAfter(storage.DefaultSessionID, 0)
	require.NoError(t, err)
	require.Len(t, progress, 100)
	require.Equal(t, int64(1), progress[0].Sequence)
	require.Equal(t, int64(100), progress[len(progress)-1].Sequence)

	core.publishSessionProgress(
		storage.DefaultSessionID,
		"run_next",
		"qmsg_next",
		agentcore.Event{Kind: agentcore.EventKindTextDelta, Text: "next"},
	)
	progress, err = core.getSessionProgressAfter(storage.DefaultSessionID, 100)
	require.NoError(t, err)
	require.Len(t, progress, 1)
	require.Equal(t, int64(101), progress[0].Sequence)
	require.Equal(t, "qmsg_next", progress[0].QueueEntryID)

	progress, err = core.getSessionProgressAfter(storage.DefaultSessionID, 50)
	require.NoError(t, err)
	require.Len(t, progress, 51)
	require.Equal(t, int64(51), progress[0].Sequence)
}

func TestSessionRunner_ProgressPreservesTraceEvents(t *testing.T) {
	core := &Agent{}
	core.publishSessionProgress(
		storage.DefaultSessionID,
		"run_test",
		"qmsg_test",
		agentcore.Event{
			Kind: agentcore.EventKindTrace,
			TraceEvent: &trace.Event{
				SessionID: storage.DefaultSessionID,
				Type:      trace.EvtToolInvocationStarted,
				Payload: map[string]any{
					"id":   "call_1",
					"name": "read_file",
				},
			},
		},
	)

	progress, err := core.getSessionProgressAfter(storage.DefaultSessionID, 0)

	require.NoError(t, err)
	require.Len(t, progress, 1)
	require.Equal(t, agentcore.EventKindTrace, progress[0].Kind)
	require.Equal(t, trace.EvtToolInvocationStarted, progress[0].TraceEvent.Type)
	require.Equal(t, "call_1", progress[0].TraceEvent.Payload.(map[string]any)["id"])
}

func TestSessionRunner_ProgressExpiryRequiresRehydration(t *testing.T) {
	core := &Agent{
		progressHistory: map[string][]agentsession.ProgressEvent{
			storage.DefaultSessionID: {
				{Sequence: 5},
			},
		},
	}

	_, err := core.getSessionProgressAfter(storage.DefaultSessionID, 1)
	require.ErrorIs(t, err, agentsession.ErrProgressExpired)
}

func waitForSessionProgressObservers(t *testing.T, core *Agent, sessionID string, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		core.progressMu.Lock()
		current := len(core.progressObservers[sessionID])
		core.progressMu.Unlock()
		if current == count {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d session progress observers", count)
		}
		time.Sleep(time.Millisecond)
	}
}

func getSessionRunnerQueueEntry(
	t *testing.T,
	entries []agentsession.QueueEntry,
	entryID string,
) agentsession.QueueEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.ID == entryID {
			return entry
		}
	}
	t.Fatalf("queue entry %q not found", entryID)
	return agentsession.QueueEntry{}
}

func waitForSessionRunnerState(
	t *testing.T,
	core *Agent,
	ctx context.Context,
	ready func(agentsession.ExecutionState) bool,
) agentsession.ExecutionState {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		state, err := core.GetSessionExecutionState(ctx, storage.DefaultSessionID)
		require.NoError(t, err)
		if ready(state) {
			return state
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for session state")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
