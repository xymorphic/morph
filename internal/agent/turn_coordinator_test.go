package agent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/environment"
	envbudget "github.com/xymorphic/morph/internal/environment/budget"
	"github.com/xymorphic/morph/internal/mocks"
	models "github.com/xymorphic/morph/internal/model"
	storage "github.com/xymorphic/morph/internal/state/core"
	statemanager "github.com/xymorphic/morph/internal/state/manager"
	agentcore "github.com/xymorphic/morph/pkg/agent"
)

func TestTurnCoordinator_AcquireSerializesMatchingSessions(t *testing.T) {
	coordinator := NewTurnCoordinator()
	releaseFirst, err := coordinator.Acquire(context.Background(), "profile", "default")
	require.NoError(t, err)

	acquired := make(chan func(), 1)
	go func() {
		release, acquireErr := coordinator.Acquire(context.Background(), "profile", "default")
		if acquireErr == nil {
			acquired <- release
		}
	}()

	require.Eventually(t, func() bool {
		value := coordinator.(*turnCoordinator)
		value.mu.Lock()
		defer value.mu.Unlock()

		gate := value.gates[getTurnCoordinationKey("profile", "default")]
		return gate != nil && gate.refs == 2
	}, time.Second, time.Millisecond)
	select {
	case <-acquired:
		t.Fatal("matching session acquired before the active turn released")
	default:
	}

	releaseFirst()
	select {
	case release := <-acquired:
		release()
	case <-time.After(time.Second):
		t.Fatal("matching session did not acquire after release")
	}
}

func TestTurnCoordinator_AcquireAllowsDifferentSessions(t *testing.T) {
	coordinator := NewTurnCoordinator()
	releaseFirst, err := coordinator.Acquire(context.Background(), "profile", "default")
	require.NoError(t, err)
	defer releaseFirst()

	releaseSecond, err := coordinator.Acquire(context.Background(), "profile", "ses_other")
	require.NoError(t, err)
	releaseSecond()

	releaseOtherProfile, err := coordinator.Acquire(context.Background(), "other-profile", "default")
	require.NoError(t, err)
	releaseOtherProfile()
}

func TestTurnCoordinator_AcquireReturnsContextErrorWhileWaiting(t *testing.T) {
	coordinator := NewTurnCoordinator()
	release, err := coordinator.Acquire(context.Background(), "profile", "default")
	require.NoError(t, err)
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = coordinator.Acquire(ctx, "profile", "default")
	require.ErrorIs(t, err, context.Canceled)
}

func TestTurnCoordinator_AcquireStopsWaitingWhenContextIsCancelled(t *testing.T) {
	coordinator := NewTurnCoordinator()
	release, err := coordinator.Acquire(context.Background(), "profile", "default")
	require.NoError(t, err)
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, acquireErr := coordinator.Acquire(ctx, "profile", "default")
		result <- acquireErr
	}()
	coordinatorValue := coordinator.(*turnCoordinator)
	require.Eventually(t, func() bool {
		coordinatorValue.mu.Lock()
		defer coordinatorValue.mu.Unlock()

		gate := coordinatorValue.gates[getTurnCoordinationKey("profile", "default")]
		return gate != nil && gate.refs == 2
	}, time.Second, time.Millisecond)
	cancel()

	require.ErrorIs(t, <-result, context.Canceled)
}

func TestTurnCoordinator_AcquireAcceptsNilContextAndReleaseIsIdempotent(t *testing.T) {
	coordinator := NewTurnCoordinator()
	var nilContext context.Context
	release, err := coordinator.Acquire(nilContext, "profile", "default")
	require.NoError(t, err)

	release()
	release()
}

func TestAgent_SetTurnCoordinatorUsesDefaults(t *testing.T) {
	var nilAgent *Agent
	nilAgent.SetTurnCoordinator(nil, "ignored")

	value := &Agent{}
	value.SetTurnCoordinator(nil, " profile ")

	require.Same(t, defaultTurnCoordinator, value.turnCoordinator)
	require.Equal(t, "profile", value.turnScope)
}

func TestAgent_RespondValidatesCoordinationFailures(t *testing.T) {
	resolveErr := errors.New("resolve failed")
	resolveStore := &stateStoreStub{getErr: resolveErr}
	resolveAgent := newCoordinationTestAgent(t, resolveStore)
	resolveAgent.turnCoordinator = nil

	_, err := resolveAgent.Respond(context.Background(), "hello", agentcore.RespondOptions{})
	require.ErrorIs(t, err, resolveErr)

	acquireErr := errors.New("acquire failed")
	coordinator := &turnCoordinatorStub{err: acquireErr}
	acquireAgent := newCoordinationTestAgent(t, &stateStoreStub{})
	acquireAgent.turnCoordinator = coordinator

	_, err = acquireAgent.Respond(context.Background(), "hello", agentcore.RespondOptions{})
	require.ErrorIs(t, err, acquireErr)
	require.Equal(t, storage.DefaultSessionID, coordinator.sessionID)

	_, err = acquireAgent.Respond(context.Background(), "hello", agentcore.RespondOptions{SessionID: "invalid"})
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
}

func newCoordinationTestAgent(t *testing.T, store storage.Store) *Agent {
	t.Helper()

	manager, err := statemanager.NewManager(store, time.Hour, time.Hour)
	require.NoError(t, err)
	stream := false
	return &Agent{
		cfg: &config.Config{Models: config.ModelsConfig{Main: config.MainModelConfig{
			Name:   "model",
			API:    models.APIOpenAIResponses,
			Stream: &stream,
		}}},
		modelClient: &mocks.ModelClientStub{},
		initialized: true,
		stateMgr:    manager,
		env: &mocks.EnvironmentStub{
			ToolRegistry:    &mocks.ToolRegistryStub{},
			IterationBudget: envbudget.New(1),
		},
	}
}

type turnCoordinatorStub struct {
	err       error
	sessionID string
}

func (c *turnCoordinatorStub) Acquire(_ context.Context, _ string, sessionID string) (func(), error) {
	c.sessionID = sessionID
	if c.err != nil {
		return nil, c.err
	}
	return func() {}, nil
}

func TestAgent_RespondSerializesMatchingSessionsAcrossAgents(t *testing.T) {
	originalOpen := OpenStateStore
	originalNewEnvironment := NewEnvironment
	t.Cleanup(func() {
		OpenStateStore = originalOpen
		NewEnvironment = originalNewEnvironment
	})

	store := &stateStoreStub{}
	OpenStateStore = func(*config.Config, models.Client) (storage.Store, error) {
		return store, nil
	}
	NewEnvironment = func(context.Context, *config.Config) environment.Environment {
		return &mocks.EnvironmentStub{
			ToolRegistry:    &mocks.ToolRegistryStub{},
			IterationBudget: envbudget.New(1),
		}
	}

	stream := false
	cfg := &config.Config{
		Models: config.ModelsConfig{Main: config.MainModelConfig{
			Name:   "model",
			API:    models.APIOpenAIResponses,
			Stream: &stream,
		}},
	}
	client := &coordinatedModelClient{
		entered: make(chan int32, 2),
		release: make(chan struct{}),
	}
	coordinator := NewTurnCoordinator()
	first := NewAgent(context.Background(), cfg, client)
	second := NewAgent(context.Background(), cfg, client)
	first.SetTurnCoordinator(coordinator, "profile")
	second.SetTurnCoordinator(coordinator, "profile")
	require.NoError(t, first.Start(context.Background()))
	require.NoError(t, second.Start(context.Background()))

	results := make(chan error, 2)
	go func() {
		_, err := first.Respond(context.Background(), "first", agentcore.RespondOptions{})
		results <- err
	}()
	require.Equal(t, int32(1), <-client.entered)
	getCallsBeforeSecond := store.getCalls.Load()

	go func() {
		_, err := second.Respond(context.Background(), "second", agentcore.RespondOptions{})
		results <- err
	}()
	require.Eventually(t, func() bool {
		value := coordinator.(*turnCoordinator)
		value.mu.Lock()
		defer value.mu.Unlock()

		gate := value.gates[getTurnCoordinationKey("profile", storage.DefaultSessionID)]
		return gate != nil && gate.refs == 2
	}, time.Second, time.Millisecond)
	require.Equal(t, getCallsBeforeSecond, store.getCalls.Load())
	select {
	case <-client.entered:
		t.Fatal("second agent entered the model before the first turn released")
	default:
	}

	close(client.release)
	require.Equal(t, int32(2), <-client.entered)
	require.NoError(t, <-results)
	require.NoError(t, <-results)
	require.Greater(t, store.getCalls.Load(), getCallsBeforeSecond)
}

type coordinatedModelClient struct {
	calls   atomic.Int32
	entered chan int32
	release chan struct{}
}

func (c *coordinatedModelClient) Complete(context.Context, models.Request) (*models.Response, error) {
	call := c.calls.Add(1)
	c.entered <- call
	if call == 1 {
		<-c.release
	}
	return &models.Response{OutputText: "ok"}, nil
}

func (c *coordinatedModelClient) CompleteStream(
	ctx context.Context,
	req models.Request,
	_ func(models.StreamDelta),
) (*models.Response, error) {
	return c.Complete(ctx, req)
}
