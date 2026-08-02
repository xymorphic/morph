package daemon

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	morphauth "github.com/xymorphic/morph/internal/auth"
)

func TestPruneRPCAuthState_StopsAfterShortBatch(t *testing.T) {
	store := &pruneStoreStub{results: []int{rpcAuthPruneLimit, rpcAuthPruneLimit - 1}}
	runPruneRPCAuthState(t, store)
	require.Equal(t, 2, store.callCount())
}

func TestPruneRPCAuthState_CapsFullBatches(t *testing.T) {
	store := &pruneStoreStub{
		results: make([]int, rpcAuthPruneMaximumBatches),
	}
	for index := range store.results {
		store.results[index] = rpcAuthPruneLimit
	}
	runPruneRPCAuthState(t, store)
	require.Equal(t, rpcAuthPruneMaximumBatches, store.callCount())
}

func TestPruneRPCAuthState_StopsAfterError(t *testing.T) {
	store := &pruneStoreStub{
		results: []int{rpcAuthPruneLimit},
		errAt:   2,
	}
	runPruneRPCAuthState(t, store)
	require.Equal(t, 2, store.callCount())
}

func TestPruneRPCAuthState_UsesBatchedStore(t *testing.T) {
	store := &batchPruneStoreStub{}
	pruneRPCAuthStateOnce(context.Background(), store)

	require.Equal(t, 1, store.calls)
	require.Equal(t, rpcAuthPruneLimit, store.limit)
	require.Equal(t, rpcAuthPruneMaximumBatches, store.maximumBatches)
}

func TestPruneRPCAuthState_StopsAfterBatchedStoreError(t *testing.T) {
	store := &batchPruneStoreStub{
		err: errors.New("batch prune failed"),
	}
	pruneRPCAuthStateOnce(context.Background(), store)
	require.Equal(t, 1, store.calls)
}

func TestPruneRPCAuthState_StopsWhenDaemonContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := &pruneStoreStub{}
	done := make(chan struct{})
	go func() {
		pruneRPCAuthState(ctx, store)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for cancelled RPC auth pruning")
	}
	require.Equal(t, 1, store.callCount())
}

func TestPruneRPCAuthState_RunsAgainAtInterval(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := &pruneStoreStub{
		results:  []int{0, 0},
		cancel:   cancel,
		cancelAt: 2,
	}
	done := make(chan struct{})
	go func() {
		pruneRPCAuthStateAtInterval(ctx, store, time.Millisecond)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("timed out waiting for periodic RPC auth pruning")
	}
	require.Equal(t, 2, store.callCount())
}

func runPruneRPCAuthState(t *testing.T, store *pruneStoreStub) {
	t.Helper()
	pruneRPCAuthStateOnce(context.Background(), store)
}

type pruneStoreStub struct {
	morphauth.Store

	mu       sync.Mutex
	results  []int
	errAt    int
	calls    int
	cancel   context.CancelFunc
	cancelAt int
}

func (s *pruneStoreStub) Prune(
	_ context.Context,
	_ morphauth.PruneOptions,
) (morphauth.PruneResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.cancelAt == s.calls {
		s.cancel()
	}
	if s.errAt == s.calls {
		return morphauth.PruneResult{}, errors.New("prune failed")
	}
	if s.calls > len(s.results) {
		return morphauth.PruneResult{}, nil
	}
	result := s.results[s.calls-1]

	return morphauth.PruneResult{Tokens: result}, nil
}

func (s *pruneStoreStub) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type batchPruneStoreStub struct {
	morphauth.Store

	calls          int
	limit          int
	maximumBatches int
	err            error
}

func (s *batchPruneStoreStub) PruneBatches(
	_ context.Context,
	options morphauth.PruneOptions,
	maximumBatches int,
) (morphauth.PruneResult, error) {
	s.calls++
	s.limit = options.Limit
	s.maximumBatches = maximumBatches
	return morphauth.PruneResult{}, s.err
}
