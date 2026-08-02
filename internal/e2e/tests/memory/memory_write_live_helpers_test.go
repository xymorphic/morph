package memory

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	e2e "github.com/xymorphic/morph/internal/e2e"
	storage "github.com/xymorphic/morph/internal/state/core"
)

func requireLiveMemoryToolCall(
	t *testing.T,
	ctx context.Context,
	harness *e2e.Harness,
	sessionID string,
	name string,
) {
	t.Helper()

	requireLiveMemoryToolCallCount(t, ctx, harness, sessionID, name, 1)
}

func requireLiveMemoryToolCallCount(
	t *testing.T,
	ctx context.Context,
	harness *e2e.Harness,
	sessionID string,
	name string,
	expected int,
) {
	t.Helper()

	messages, err := harness.Messages(ctx, sessionID)
	require.NoError(t, err)

	callCount := 0
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			if call.Name == name {
				callCount++
			}
		}
	}

	require.Equalf(t, expected, callCount, "expected %d %s tool calls; messages: %#v", expected, name, messages)
}

func requireLiveMemoryToolCallAtLeast(
	t *testing.T,
	ctx context.Context,
	harness *e2e.Harness,
	sessionID string,
	name string,
	minimum int,
) {
	t.Helper()

	messages, err := harness.Messages(ctx, sessionID)
	require.NoError(t, err)

	callCount := 0
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			if call.Name == name {
				callCount++
			}
		}
	}

	require.GreaterOrEqualf(t, callCount, minimum, "expected at least %d %s tool calls; messages: %#v", minimum, name, messages)
}

func waitForLiveMemoryAddSemanticMemory(
	t *testing.T,
	ctx context.Context,
	store liveMemoryStore,
	vectorIndex liveMemoryVectorIndex,
	sessionID string,
) storage.MemoryItem {
	t.Helper()

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		item, ok := getLiveSemanticMemoryContaining(
			ctx,
			t,
			store,
			sessionID,
			"cobalt-ridge",
			"status",
			"reports",
		)
		if ok && hasCurrentLiveMemoryVector(ctx, t, vectorIndex, item) {
			return item
		}

		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for memory_add semantic memory; memories: %s", getLiveMemoryDump(context.Background(), t, store, sessionID))
		case <-ticker.C:
		}
	}
}

func waitForLiveUpdatedSemanticMemory(
	t *testing.T,
	ctx context.Context,
	store liveMemoryStore,
	vectorIndex liveMemoryVectorIndex,
	sessionID string,
	previousID string,
) storage.MemoryItem {
	t.Helper()

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		item, ok := getLiveSemanticMemoryContaining(
			ctx,
			t,
			store,
			sessionID,
			"copper-harbor",
			"status",
			"reports",
		)
		if ok &&
			item.Metadata["supersedes_memory_id"] == previousID &&
			hasCurrentLiveMemoryVector(ctx, t, vectorIndex, item) {
			return item
		}

		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for memory_update semantic memory; memories: %s", getLiveMemoryDump(context.Background(), t, store, sessionID))
		case <-ticker.C:
		}
	}
}

func waitForLiveMemoryStatus(
	t *testing.T,
	ctx context.Context,
	store liveMemoryStore,
	memoryID string,
	status storage.MemoryStatus,
) storage.MemoryItem {
	t.Helper()

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		item, ok := getLiveMemoryByID(ctx, t, store, memoryID, status)
		if ok {
			return item
		}

		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for memory %s status %s", memoryID, status)
		case <-ticker.C:
		}
	}
}

func getLiveMemoryByID(
	ctx context.Context,
	t *testing.T,
	store liveMemoryStore,
	memoryID string,
	status storage.MemoryStatus,
) (storage.MemoryItem, bool) {
	t.Helper()

	result, err := store.SearchMemory(ctx, storage.MemorySearchQuery{
		Statuses: []storage.MemoryStatus{status},
		Limit:    50,
	})
	require.NoError(t, err)
	for _, hit := range result.Hits {
		if hit.Item.ID == memoryID {
			return hit.Item, true
		}
	}

	return storage.MemoryItem{}, false
}

func hasLiveActiveMemory(
	ctx context.Context,
	t *testing.T,
	store liveMemoryStore,
	sessionID string,
	required ...string,
) bool {
	t.Helper()

	_, ok := getLiveActiveMemoryContaining(ctx, t, store, sessionID, strings.Join(required, " "))
	return ok
}
