package storesqlite

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	base "github.com/xymorphic/morph/internal/state/core"
)

func TestSQLiteStore_TraceAppendListAndPrune(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)

	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
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
	otherSessionID, err := base.NewSessionID()
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
	require.Equal(t, []string{"first", "second"}, sqliteTraceEventTypes(result.Events))
	require.Equal(t, map[string]any{"message": "one"}, result.Events[0].Payload)

	result, err = store.ListTraceEvents(ctx, base.TraceQuery{SessionID: base.DefaultSessionID, Desc: true, Limit: 1})
	require.NoError(t, err)
	require.Equal(t, []string{"second"}, sqliteTraceEventTypes(result.Events))

	result, err = store.ListTraceEvents(ctx, base.TraceQuery{SessionID: base.DefaultSessionID, MinSequence: 2})
	require.NoError(t, err)
	require.Equal(t, []string{"second"}, sqliteTraceEventTypes(result.Events))

	require.NoError(t, store.PruneTraceEvents(ctx, base.DefaultSessionID, 1))
	result, err = store.ListTraceEvents(ctx, base.TraceQuery{SessionID: base.DefaultSessionID})
	require.NoError(t, err)
	require.Equal(t, []string{"second"}, sqliteTraceEventTypes(result.Events))

	result, err = store.ListTraceEvents(ctx, base.TraceQuery{SessionID: otherSessionID})
	require.NoError(t, err)
	require.Equal(t, []string{"other"}, sqliteTraceEventTypes(result.Events))
}

func TestSQLiteStore_ConcurrentTraceAppendsKeepUniqueSequences(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	firstStore, err := NewStore(path)
	require.NoError(t, err)
	secondStore, err := NewStore(path)
	require.NoError(t, err)

	stores := []*Store{firstStore, secondStore}
	errorsByAppend := make([]error, 12)
	var waitGroup sync.WaitGroup
	for index := range errorsByAppend {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			_, errorsByAppend[index] = stores[index%len(stores)].AppendTraceEvent(ctx, base.TraceEvent{
				SessionID: base.DefaultSessionID,
				Type:      "concurrent",
			})
		}()
	}
	waitGroup.Wait()

	for _, appendErr := range errorsByAppend {
		require.NoError(t, appendErr)
	}
	result, err := firstStore.ListTraceEvents(ctx, base.TraceQuery{SessionID: base.DefaultSessionID})
	require.NoError(t, err)
	require.Len(t, result.Events, len(errorsByAppend))
	for index, event := range result.Events {
		require.Equal(t, index+1, event.Sequence)
	}
}

func TestSQLiteStore_AppendTraceEventRetriesRealLock(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := NewStore(path)
	require.NoError(t, err)
	locker, err := NewStore(path)
	require.NoError(t, err)
	require.NoError(t, store.db.Exec("PRAGMA busy_timeout = 1").Error)

	transaction := locker.db.Begin()
	require.NoError(t, transaction.Error)
	require.NoError(t, transaction.Exec("INSERT INTO trace_events(session_id, sequence, type) VALUES (?, ?, ?)", "locker", 1, "lock").Error)
	released := make(chan error, 1)
	go func() {
		time.Sleep(10 * time.Millisecond)
		released <- transaction.Rollback().Error
	}()

	startedAt := time.Now()
	event, err := store.AppendTraceEvent(ctx, base.TraceEvent{SessionID: base.DefaultSessionID, Type: "result"})

	require.NoError(t, err)
	require.NoError(t, <-released)
	require.GreaterOrEqual(t, time.Since(startedAt), 20*time.Millisecond)
	require.Equal(t, 1, event.Sequence)
}

func TestSQLiteStore_TraceValidation(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)

	var nilStore *Store
	_, err = nilStore.AppendTraceEvent(ctx, base.TraceEvent{})
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

func TestSQLiteStore_TraceListFiltersAndPagination(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)

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

	result, err := store.ListTraceEvents(ctx, base.TraceQuery{Types: []string{" second "}})
	require.NoError(t, err)
	require.Equal(t, []string{"second"}, sqliteTraceEventTypes(result.Events))

	result, err = store.ListTraceEvents(ctx, base.TraceQuery{Desc: true, Offset: 1, Limit: 2})
	require.NoError(t, err)
	require.Equal(t, []string{"second", "first"}, sqliteTraceEventTypes(result.Events))
}

func TestSQLiteStore_TraceAppendDefaultsTimestamp(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)

	event, err := store.AppendTraceEvent(ctx, base.TraceEvent{
		SessionID: base.DefaultSessionID,
		Type:      "model.request",
	})
	require.NoError(t, err)
	require.False(t, event.Timestamp.IsZero())
	require.Equal(t, time.UTC, event.Timestamp.Location())
}

func TestSQLiteStore_TraceModelToEventRejectsInvalidPayload(t *testing.T) {
	_, err := traceModelToEvent(traceEventModel{
		SessionID:   base.DefaultSessionID,
		Sequence:    1,
		Type:        "model.request",
		Timestamp:   time.Now().UTC(),
		PayloadJSON: "{",
	})

	require.Error(t, err)
}

func TestSQLiteStore_TraceAppendDatabaseErrors(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)

	queryErr := errors.New("trace sequence lookup failed")
	require.NoError(t, store.db.Callback().Query().Before("gorm:query").Register("test:trace-append-query-error", func(tx *gorm.DB) {
		if callbackTable(tx) == "trace_events" {
			_ = tx.AddError(queryErr)
		}
	}))
	_, err = store.AppendTraceEvent(ctx, base.TraceEvent{SessionID: base.DefaultSessionID, Type: "model.request"})
	require.ErrorIs(t, err, queryErr)
	require.NoError(t, store.db.Callback().Query().Remove("test:trace-append-query-error"))

	createErr := errors.New("trace create failed")
	require.NoError(t, store.db.Callback().Create().Before("gorm:create").Register("test:trace-create-error", func(tx *gorm.DB) {
		if callbackTable(tx) == "trace_events" {
			_ = tx.AddError(createErr)
		}
	}))
	_, err = store.AppendTraceEvent(ctx, base.TraceEvent{SessionID: base.DefaultSessionID, Type: "model.request"})
	require.ErrorIs(t, err, createErr)
	require.NoError(t, store.db.Callback().Create().Remove("test:trace-create-error"))
}

func TestSQLiteStore_TraceListDatabaseErrors(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)

	queryErr := errors.New("trace list failed")
	require.NoError(t, store.db.Callback().Query().Before("gorm:query").Register("test:trace-list-error", func(tx *gorm.DB) {
		if callbackTable(tx) == "trace_events" {
			_ = tx.AddError(queryErr)
		}
	}))
	_, err = store.ListTraceEvents(ctx, base.TraceQuery{})
	require.ErrorIs(t, err, queryErr)
	require.NoError(t, store.db.Callback().Query().Remove("test:trace-list-error"))

	require.NoError(t, store.db.Create(&traceEventModel{
		SessionID:   base.DefaultSessionID,
		Sequence:    1,
		Type:        "model.request",
		Timestamp:   time.Now().UTC(),
		PayloadJSON: "{",
	}).Error)
	_, err = store.ListTraceEvents(ctx, base.TraceQuery{SessionID: base.DefaultSessionID})
	require.Error(t, err)
}

func TestSQLiteStore_TracePruneDatabaseErrorsAndNoop(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)

	_, err = store.AppendTraceEvent(ctx, base.TraceEvent{SessionID: base.DefaultSessionID, Type: "model.request"})
	require.NoError(t, err)
	require.NoError(t, store.PruneTraceEvents(ctx, base.DefaultSessionID, 2))

	queryErr := errors.New("trace count failed")
	require.NoError(t, store.db.Callback().Query().Before("gorm:query").Register("test:trace-prune-count-error", func(tx *gorm.DB) {
		if callbackTable(tx) == "trace_events" {
			_ = tx.AddError(queryErr)
		}
	}))
	err = store.PruneTraceEvents(ctx, base.DefaultSessionID, 1)
	require.ErrorIs(t, err, queryErr)
	require.NoError(t, store.db.Callback().Query().Remove("test:trace-prune-count-error"))

	_, err = store.AppendTraceEvent(ctx, base.TraceEvent{SessionID: base.DefaultSessionID, Type: "model.response"})
	require.NoError(t, err)
	deleteErr := errors.New("trace delete failed")
	require.NoError(t, store.db.Callback().Delete().Before("gorm:delete").Register("test:trace-prune-delete-error", func(tx *gorm.DB) {
		if callbackTable(tx) == "trace_events" {
			_ = tx.AddError(deleteErr)
		}
	}))
	err = store.PruneTraceEvents(ctx, base.DefaultSessionID, 1)
	require.ErrorContains(t, err, "failed to prune trace events")
	require.ErrorIs(t, err, deleteErr)
	require.NoError(t, store.db.Callback().Delete().Remove("test:trace-prune-delete-error"))
}

func sqliteTraceEventTypes(events []base.TraceEvent) []string {
	values := make([]string, 0, len(events))
	for _, event := range events {
		values = append(values, event.Type)
	}

	return values
}
