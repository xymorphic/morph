package storememory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	base "github.com/xymorphic/morph/internal/state/core"
)

func TestMemoryStore_TraceAppendListAndPrune(t *testing.T) {
	ctx := context.Background()
	store := NewStore()
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	otherSessionID, err := base.NewSessionID()
	require.NoError(t, err)

	first, err := store.AppendTraceEvent(ctx, base.TraceEvent{
		SessionID: base.DefaultSessionID,
		Type:      "first",
		Timestamp: now,
		Payload:   map[string]any{"message": "one"},
	})
	require.NoError(t, err)
	second, err := store.AppendTraceEvent(ctx, base.TraceEvent{
		SessionID: base.DefaultSessionID,
		Type:      "second",
		Timestamp: now.Add(time.Second),
	})
	require.NoError(t, err)
	_, err = store.AppendTraceEvent(ctx, base.TraceEvent{
		SessionID: otherSessionID,
		Type:      "other",
		Timestamp: now.Add(2 * time.Second),
	})
	require.NoError(t, err)

	require.Equal(t, 1, first.Sequence)
	require.Equal(t, 2, second.Sequence)

	result, err := store.ListTraceEvents(ctx, base.TraceQuery{SessionID: base.DefaultSessionID})
	require.NoError(t, err)
	require.Equal(t, []string{"first", "second"}, traceEventTypes(result.Events))

	result, err = store.ListTraceEvents(ctx, base.TraceQuery{SessionID: base.DefaultSessionID, Types: []string{"second"}})
	require.NoError(t, err)
	require.Equal(t, []string{"second"}, traceEventTypes(result.Events))

	result, err = store.ListTraceEvents(ctx, base.TraceQuery{SessionID: base.DefaultSessionID, MinSequence: 2})
	require.NoError(t, err)
	require.Equal(t, []string{"second"}, traceEventTypes(result.Events))

	require.NoError(t, store.PruneTraceEvents(ctx, base.DefaultSessionID, 1))
	result, err = store.ListTraceEvents(ctx, base.TraceQuery{SessionID: base.DefaultSessionID})
	require.NoError(t, err)
	require.Equal(t, []string{"second"}, traceEventTypes(result.Events))

	result, err = store.ListTraceEvents(ctx, base.TraceQuery{SessionID: otherSessionID})
	require.NoError(t, err)
	require.Equal(t, []string{"other"}, traceEventTypes(result.Events))
}

func TestMemoryStore_TracePayloadsAreCloned(t *testing.T) {
	ctx := context.Background()
	store := NewStore()
	payload := map[string]any{"message": "original"}

	_, err := store.AppendTraceEvent(ctx, base.TraceEvent{
		SessionID: base.DefaultSessionID,
		Type:      "model.request",
		Payload:   payload,
	})
	require.NoError(t, err)

	payload["message"] = "mutated"
	result, err := store.ListTraceEvents(ctx, base.TraceQuery{SessionID: base.DefaultSessionID})
	require.NoError(t, err)
	require.Equal(t, "original", result.Events[0].Payload.(map[string]any)["message"])

	result.Events[0].Payload.(map[string]any)["message"] = "listed mutation"
	result, err = store.ListTraceEvents(ctx, base.TraceQuery{SessionID: base.DefaultSessionID})
	require.NoError(t, err)
	require.Equal(t, "original", result.Events[0].Payload.(map[string]any)["message"])
}

func TestMemoryStore_TraceValidation(t *testing.T) {
	ctx := context.Background()
	store := NewStore()

	var nilStore *Store
	_, err := nilStore.AppendTraceEvent(ctx, base.TraceEvent{})
	require.Error(t, err)

	_, err = store.AppendTraceEvent(ctx, base.TraceEvent{SessionID: "invalid", Type: "model.request"})
	require.Error(t, err)

	_, err = store.AppendTraceEvent(ctx, base.TraceEvent{SessionID: base.DefaultSessionID})
	require.Error(t, err)

	_, err = nilStore.ListTraceEvents(ctx, base.TraceQuery{})
	require.Error(t, err)

	err = nilStore.PruneTraceEvents(ctx, base.DefaultSessionID, 1)
	require.Error(t, err)

	err = store.PruneTraceEvents(ctx, base.DefaultSessionID, -1)
	require.Error(t, err)

	err = store.PruneTraceEvents(ctx, "invalid", 1)
	require.Error(t, err)
}

func TestMemoryStore_TraceListAllSessionsPaginationAndDesc(t *testing.T) {
	ctx := context.Background()
	store := NewStore()
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	otherSessionID, err := base.NewSessionID()
	require.NoError(t, err)

	for _, event := range []base.TraceEvent{
		{SessionID: base.DefaultSessionID, Type: "first", Timestamp: now},
		{SessionID: base.DefaultSessionID, Type: "second", Timestamp: now.Add(time.Second)},
		{SessionID: otherSessionID, Type: "other", Timestamp: now.Add(2 * time.Second)},
	} {
		_, err := store.AppendTraceEvent(ctx, event)
		require.NoError(t, err)
	}

	result, err := store.ListTraceEvents(ctx, base.TraceQuery{Desc: true, Offset: 1, Limit: 2})
	require.NoError(t, err)
	require.Equal(t, []string{"second", "first"}, traceEventTypes(result.Events))

	result, err = store.ListTraceEvents(ctx, base.TraceQuery{Offset: 10})
	require.NoError(t, err)
	require.Empty(t, result.Events)

	result, err = store.ListTraceEvents(ctx, base.TraceQuery{Offset: -1, Limit: 1})
	require.NoError(t, err)
	require.Equal(t, []string{"first"}, traceEventTypes(result.Events))
}

func TestMemoryStore_TraceAppendDefaultsTimestamp(t *testing.T) {
	ctx := context.Background()
	store := NewStore()

	event, err := store.AppendTraceEvent(ctx, base.TraceEvent{
		SessionID: base.DefaultSessionID,
		Type:      "model.request",
	})
	require.NoError(t, err)
	require.False(t, event.Timestamp.IsZero())
	require.Equal(t, time.UTC, event.Timestamp.Location())
}

func TestMemoryStore_TraceAppendInitializesZeroValueMaps(t *testing.T) {
	ctx := context.Background()
	store := &Store{}

	event, err := store.AppendTraceEvent(ctx, base.TraceEvent{
		SessionID: base.DefaultSessionID,
		Type:      "model.request",
	})
	require.NoError(t, err)
	require.Equal(t, uint(1), event.ID)
	require.Equal(t, 1, event.Sequence)

	result, err := store.ListTraceEvents(ctx, base.TraceQuery{SessionID: base.DefaultSessionID})
	require.NoError(t, err)
	require.Equal(t, []string{"model.request"}, traceEventTypes(result.Events))
}

func TestMemoryStore_TracePruneNoopsWhenUnderLimit(t *testing.T) {
	ctx := context.Background()
	store := NewStore()

	_, err := store.AppendTraceEvent(ctx, base.TraceEvent{
		SessionID: base.DefaultSessionID,
		Type:      "model.request",
	})
	require.NoError(t, err)

	require.NoError(t, store.PruneTraceEvents(ctx, base.DefaultSessionID, 2))

	result, err := store.ListTraceEvents(ctx, base.TraceQuery{SessionID: base.DefaultSessionID})
	require.NoError(t, err)
	require.Equal(t, []string{"model.request"}, traceEventTypes(result.Events))
}

func TestMemoryStore_TraceListOrdersByIDWhenSequenceMatches(t *testing.T) {
	ctx := context.Background()
	store := NewStore()
	store.traceEvents[base.DefaultSessionID] = []base.TraceEvent{
		{ID: 2, SessionID: base.DefaultSessionID, Sequence: 1, Type: "second-id"},
		{ID: 1, SessionID: base.DefaultSessionID, Sequence: 1, Type: "first-id"},
	}

	result, err := store.ListTraceEvents(ctx, base.TraceQuery{SessionID: base.DefaultSessionID})
	require.NoError(t, err)
	require.Equal(t, []string{"first-id", "second-id"}, traceEventTypes(result.Events))
}

func traceEventTypes(events []base.TraceEvent) []string {
	values := make([]string, 0, len(events))
	for _, event := range events {
		values = append(values, event.Type)
	}

	return values
}
