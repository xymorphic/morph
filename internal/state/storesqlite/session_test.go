package storesqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	base "github.com/wandxy/morph/internal/state/core"
	morphmsg "github.com/wandxy/morph/pkg/agent/message"
	"github.com/wandxy/morph/pkg/nanoid"
)

const DefaultSessionID = base.DefaultSessionID
const SessionIDPrefix = base.SessionIDPrefix

var (
	testSessionA       = nanoid.MustFromSeed(SessionIDPrefix, "project-a", "SessionTestSeedValue123")
	testSessionB       = nanoid.MustFromSeed(SessionIDPrefix, "project-b", "SessionTestSeedValue123")
	testMissingSession = nanoid.MustFromSeed(SessionIDPrefix, "missing", "SessionTestSeedValue123")
	testSessionAlpha   = nanoid.MustFromSeed(SessionIDPrefix, "alpha", "SessionTestSeedValue123")
	testSessionZeta    = nanoid.MustFromSeed(SessionIDPrefix, "zeta", "SessionTestSeedValue123")
	testSessionZero    = nanoid.MustFromSeed(SessionIDPrefix, "project-zero", "SessionTestSeedValue123")
)

func TestSQLiteStore_SessionLifecycle(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	sessions, err := store.List(context.Background(), base.SessionListOptions{})
	require.NoError(t, err)
	require.Empty(t, sessions)

	session, ok, err := store.Get(context.Background(), "", base.SessionGetOptions{})
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, Session{}, session)

	session, ok, err = store.Get(context.Background(), testMissingSession, base.SessionGetOptions{})
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, Session{}, session)

	require.EqualError(t, store.Save(context.Background(), Session{}), "session id is required")

	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionB, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionB, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "first", CreatedAt: now},
		{
			Role:            morphmsg.RoleAssistant,
			Name:            "bot",
			Content:         "second",
			ToolCallID:      "call-1",
			SemanticContent: "semantic second",
			CreatedAt:       now.Add(time.Second),
		},
	}))
	require.NoError(t, store.Save(context.Background(), Session{
		ID:        testSessionA,
		UpdatedAt: now.Add(-time.Minute),
	}))

	loaded, ok, err := store.Get(context.Background(), testSessionB, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, testSessionB, loaded.ID)
	require.False(t, loaded.CreatedAt.IsZero())
	createdAt := loaded.CreatedAt
	messages, err := store.GetMessages(context.Background(), testSessionB, MessageQueryOptions{})
	require.NoError(t, err)
	require.Len(t, messages, 2)
	require.Equal(t, "first", messages[0].Content)
	require.Equal(t, "bot", messages[1].Name)
	require.Equal(t, "call-1", messages[1].ToolCallID)
	require.Equal(t, "semantic second", messages[1].SemanticContent)
	message, ok, err := store.GetMessage(context.Background(), testSessionB, 1)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "second", message.Content)
	require.Equal(t, "semantic second", message.SemanticContent)

	messages[0].Content = "mutated"
	loadedAgain, ok, err := store.Get(context.Background(), testSessionB, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, testSessionB, loadedAgain.ID)
	require.Equal(t, createdAt, loadedAgain.CreatedAt)
	messagesAgain, err := store.GetMessages(context.Background(), testSessionB, MessageQueryOptions{})
	require.NoError(t, err)
	require.Equal(t, "first", messagesAgain[0].Content)

	require.NoError(t, store.ClearMessages(context.Background(), testSessionB))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionB, []morphmsg.Message{
		{Role: morphmsg.RoleAssistant, Content: "replacement", CreatedAt: now.Add(2 * time.Second)},
	}))

	sessions, err = store.List(context.Background(), base.SessionListOptions{})
	require.NoError(t, err)
	require.Len(t, sessions, 2)
	require.Equal(t, testSessionB, sessions[0].ID)
	require.Equal(t, testSessionA, sessions[1].ID)
	messages, err = store.GetMessages(context.Background(), testSessionB, MessageQueryOptions{})
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, "replacement", messages[0].Content)

	var messageCount int64
	require.NoError(t, store.db.Model(&messageModel{}).Where("session_id = ?", testSessionB).Count(&messageCount).Error)
	require.EqualValues(t, 1, messageCount)

	current, ok, err := store.Current(context.Background())
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, current)

	require.EqualError(t, store.SetCurrent(context.Background(), ""), "session id is required")
	require.EqualError(t, store.SetCurrent(context.Background(), testMissingSession), "session not found")
	require.NoError(t, store.SetCurrent(context.Background(), testSessionB))

	current, ok, err = store.Current(context.Background())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, testSessionB, current)

	require.NoError(t, store.ClearCurrent(context.Background()))
	current, ok, err = store.Current(context.Background())
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, current)
	require.NoError(t, store.SetCurrent(context.Background(), testSessionB))

	require.NoError(t, store.db.Save(&stateModel{
		Key:       currentSessionStateKey,
		Value:     "   ",
		UpdatedAt: now,
	}).Error)

	current, ok, err = store.Current(context.Background())
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, current)
}

func TestSQLiteStore_PatchReasoningEffortOverride(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)
	original := Session{
		ID:                         testSessionA,
		Title:                      "Original",
		EpisodicCheckpointOffset:   7,
		ReflectionCheckpointOffset: 9,
	}
	require.NoError(t, store.Save(context.Background(), original))
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionB}))

	unknown := "future-effort"
	patched, err := store.Patch(context.Background(), testSessionA, SessionPatch{
		ReasoningEffortOverride: &unknown,
	})
	require.NoError(t, err)
	require.Equal(t, unknown, patched.ReasoningEffortOverride)
	require.Equal(t, "Original", patched.Title)
	require.Equal(t, 7, patched.EpisodicCheckpointOffset)
	require.Equal(t, 9, patched.ReflectionCheckpointOffset)

	unchanged, err := store.Patch(context.Background(), testSessionA, SessionPatch{})
	require.NoError(t, err)
	require.Equal(t, unknown, unchanged.ReasoningEffortOverride)

	require.NoError(t, store.Save(context.Background(), Session{
		ID:    testSessionA,
		Title: "Updated",
	}))
	loaded, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, unknown, loaded.ReasoningEffortOverride)
	require.Equal(t, "Updated", loaded.Title)

	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{
		Role:    morphmsg.RoleUser,
		Content: "hello",
	}}))
	now := time.Now().UTC()
	archived, err := store.Archive(context.Background(), testSessionA, base.SessionArchiveRequest{
		ArchivedAt: now,
		ExpiresAt:  now.Add(time.Hour),
	})
	require.NoError(t, err)
	require.Equal(t, unknown, archived.ReasoningEffortOverride)
	unarchived, err := store.Unarchive(context.Background(), testSessionA)
	require.NoError(t, err)
	require.Equal(t, unknown, unarchived.ReasoningEffortOverride)

	other, ok, err := store.Get(context.Background(), testSessionB, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Empty(t, other.ReasoningEffortOverride)

	empty := ""
	cleared, err := store.Patch(context.Background(), testSessionA, SessionPatch{
		ReasoningEffortOverride: &empty,
	})
	require.NoError(t, err)
	require.Empty(t, cleared.ReasoningEffortOverride)

	var record sessionModel
	require.NoError(t, store.db.First(&record, "id = ?", testSessionA).Error)
	require.Nil(t, record.ReasoningEffortOverride)
}

func TestSQLiteStore_MigratesReasoningEffortOverrideColumn(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "legacy.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`
		CREATE TABLE sessions (
			id text PRIMARY KEY,
			created_at datetime,
			updated_at datetime
		)
	`).Error)

	store, err := NewStoreFromDB(db)
	require.NoError(t, err)
	require.True(t, store.db.Migrator().HasColumn(&sessionModel{}, "ReasoningEffortOverride"))
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))

	effort := "high"
	_, err = store.Patch(context.Background(), testSessionA, SessionPatch{
		ReasoningEffortOverride: &effort,
	})
	require.NoError(t, err)

	loaded, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, effort, loaded.ReasoningEffortOverride)
}

func TestSQLiteStore_PatchReasoningEffortOverrideConcurrentSetters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.db")
	store, err := NewStore(path)
	require.NoError(t, err)
	secondStore, err := NewStore(path)
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))

	values := []string{"low", "medium", "high", "xhigh"}
	stores := []*Store{store, secondStore, store, secondStore}
	start := make(chan struct{})
	errs := make(chan error, len(values))
	var wg sync.WaitGroup
	for i, value := range values {
		value := value
		target := stores[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, patchErr := target.Patch(context.Background(), testSessionA, SessionPatch{
				ReasoningEffortOverride: &value,
			})
			errs <- patchErr
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for patchErr := range errs {
		require.NoError(t, patchErr)
	}

	final := "none"
	patched, err := secondStore.Patch(context.Background(), testSessionA, SessionPatch{
		ReasoningEffortOverride: &final,
	})
	require.NoError(t, err)
	require.Equal(t, final, patched.ReasoningEffortOverride)
}

func TestSQLiteStore_ConcurrentMessageAppendsKeepUniqueSequences(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "session.db")
	firstStore, err := NewStore(path)
	require.NoError(t, err)
	secondStore, err := NewStore(path)
	require.NoError(t, err)
	require.NoError(t, firstStore.Save(ctx, Session{ID: testSessionA}))

	stores := []*Store{firstStore, secondStore}
	errorsByAppend := make([]error, 12)
	var waitGroup sync.WaitGroup
	for index := range errorsByAppend {
		waitGroup.Go(func() {
			errorsByAppend[index] = stores[index%len(stores)].AppendMessages(ctx, testSessionA, []morphmsg.Message{{
				Role:    morphmsg.RoleTool,
				Content: fmt.Sprintf("result-%d", index),
			}})
		})
	}
	waitGroup.Wait()

	for _, appendErr := range errorsByAppend {
		require.NoError(t, appendErr)
	}
	var records []messageModel
	require.NoError(t, firstStore.db.Where("session_id = ?", testSessionA).Order("sequence").Find(&records).Error)
	require.Len(t, records, len(errorsByAppend))
	for index, record := range records {
		require.Equal(t, index, record.Sequence)
	}
}

func TestSQLiteStore_AppendMessagesRetriesRealLock(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "session.db")
	store, err := NewStore(path)
	require.NoError(t, err)
	locker, err := NewStore(path)
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, Session{ID: testSessionA}))
	require.NoError(t, store.db.Exec("PRAGMA busy_timeout = 1").Error)

	transaction := locker.db.Begin()
	require.NoError(t, transaction.Error)
	require.NoError(t, transaction.Exec("UPDATE sessions SET title = title WHERE id = ?", testSessionA).Error)
	released := make(chan error, 1)
	go func() {
		time.Sleep(10 * time.Millisecond)
		released <- transaction.Rollback().Error
	}()

	startedAt := time.Now()
	err = store.AppendMessages(ctx, testSessionA, []morphmsg.Message{{Role: morphmsg.RoleTool, Content: "result"}})

	require.NoError(t, err)
	require.NoError(t, <-released)
	require.GreaterOrEqual(t, time.Since(startedAt), 20*time.Millisecond)
	var records []messageModel
	require.NoError(t, store.db.Where("session_id = ?", testSessionA).Find(&records).Error)
	require.Len(t, records, 1)
	require.Equal(t, 0, records[0].Sequence)
}

func TestSQLiteStore_SavePersistsSessionOrigin(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	origin := base.SessionOrigin{
		Source:         base.SessionOriginSourceTelegram,
		ConversationID: "-100",
		ThreadID:       "42",
	}
	require.NoError(t, store.Save(context.Background(), Session{
		ID:     testSessionA,
		Origin: origin,
	}))

	loaded, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, origin, loaded.Origin)

	sessions, err := store.List(context.Background(), base.SessionListOptions{})
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	require.Equal(t, origin, sessions[0].Origin)

	sessions, err = store.List(context.Background(), base.SessionListOptions{
		OriginSource: base.SessionOriginSourceTelegram,
	})
	require.NoError(t, err)
	require.Len(t, sessions, 1)

	sessions, err = store.List(context.Background(), base.SessionListOptions{
		OriginSource: base.SessionOriginSourceAutomation,
	})
	require.NoError(t, err)
	require.Empty(t, sessions)

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	loaded, ok, err = store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, origin, loaded.Origin)
}

func TestSQLiteStore_GetAndListFilterByArchiveState(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	archived := true
	live := false

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "archived", CreatedAt: now},
	}))
	_, err = store.Archive(context.Background(), testSessionA, base.SessionArchiveRequest{
		ArchivedAt: now,
		ExpiresAt:  now.Add(time.Hour),
	})
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionB, UpdatedAt: now.Add(time.Minute)}))

	session, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{Archived: &archived})
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, session.Archived)

	session, ok, err = store.Get(context.Background(), testSessionA, base.SessionGetOptions{Archived: &live})
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, Session{}, session)

	session, ok, err = store.Get(context.Background(), testSessionB, base.SessionGetOptions{Archived: &live})
	require.NoError(t, err)
	require.True(t, ok)
	require.False(t, session.Archived)

	session, ok, err = store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, session.Archived)

	require.EqualError(t, store.SetCurrent(context.Background(), testSessionA), "session not found")

	sessions, err := store.List(context.Background(), base.SessionListOptions{Archived: &archived})
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	require.Equal(t, testSessionA, sessions[0].ID)

	sessions, err = store.List(context.Background(), base.SessionListOptions{Archived: &live})
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	require.Equal(t, testSessionB, sessions[0].ID)

	sessions, err = store.List(context.Background(), base.SessionListOptions{})
	require.NoError(t, err)
	require.Len(t, sessions, 2)
	require.ElementsMatch(t, []string{testSessionA, testSessionB}, []string{sessions[0].ID, sessions[1].ID})
}

func TestSQLiteStore_GetMessagesByIDsReturnsTranscriptOrderedRecords(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	now := time.Now().UTC()
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "m1", CreatedAt: now},
		{Role: morphmsg.RoleAssistant, Content: "m2", CreatedAt: now.Add(time.Second)},
		{Role: morphmsg.RoleTool, Name: "process", ToolCallID: "call-1", Content: "m3", CreatedAt: now.Add(2 * time.Second)},
	}))

	records, err := store.GetMessagesByIDs(context.Background(), testSessionA, []uint{3, 1})
	require.NoError(t, err)
	require.Len(t, records, 2)
	require.Equal(t, []uint{1, 3}, []uint{records[0].Message.ID, records[1].Message.ID})
	require.Equal(t, []int{0, 2}, []int{records[0].Offset, records[1].Offset})
}

func TestSQLiteStore_GetMessagesByIDs_ValidationAndErrors(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	now := time.Now().UTC()
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "m1", CreatedAt: now},
	}))

	records, err := store.GetMessagesByIDs(context.Background(), "", []uint{1})
	require.NoError(t, err)
	require.Nil(t, records)

	records, err = store.GetMessagesByIDs(context.Background(), testSessionA, nil)
	require.NoError(t, err)
	require.Nil(t, records)

	_, err = store.GetMessagesByIDs(context.Background(), "bad-session-id", []uint{1})
	require.ErrorContains(t, err, "session id")

	records, err = store.GetMessagesByIDs(context.Background(), testSessionA, []uint{99})
	require.NoError(t, err)
	require.Nil(t, records)

	boom := errors.New("query failed")
	require.NoError(t, store.db.Callback().Query().Before("gorm:query").Register("test:get-messages-by-ids-error", func(tx *gorm.DB) {
		_ = tx.AddError(boom)
	}))
	defer func() {
		require.NoError(t, store.db.Callback().Query().Remove("test:get-messages-by-ids-error"))
	}()

	_, err = store.GetMessagesByIDs(context.Background(), testSessionA, []uint{1})
	require.ErrorIs(t, err, boom)
}

func TestSQLiteStore_GetMessageWindowReturnsBoundedAnchorContext(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	now := time.Now().UTC()
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "m1", CreatedAt: now},
		{Role: morphmsg.RoleAssistant, Content: "m2", CreatedAt: now.Add(time.Second)},
		{Role: morphmsg.RoleTool, Name: "process", ToolCallID: "call-1", Content: "m3", CreatedAt: now.Add(2 * time.Second)},
		{Role: morphmsg.RoleAssistant, Content: "m4", CreatedAt: now.Add(3 * time.Second)},
	}))

	records, err := store.GetMessageWindow(context.Background(), testSessionA, 3, 1, 1)
	require.NoError(t, err)
	require.Len(t, records, 3)
	require.Equal(t, []int{1, 2, 3}, []int{records[0].Offset, records[1].Offset, records[2].Offset})
	require.Equal(t, []uint{2, 3, 4}, []uint{records[0].Message.ID, records[1].Message.ID, records[2].Message.ID})
}

func TestSQLiteStore_GetMessageWindow_ValidationNotFoundAndErrors(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	now := time.Now().UTC()
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "m1", CreatedAt: now},
		{Role: morphmsg.RoleAssistant, Content: "m2", CreatedAt: now.Add(time.Second)},
	}))

	records, err := store.GetMessageWindow(context.Background(), "", 1, 0, 0)
	require.NoError(t, err)
	require.Nil(t, records)

	records, err = store.GetMessageWindow(context.Background(), testSessionA, 0, 0, 0)
	require.NoError(t, err)
	require.Nil(t, records)

	_, err = store.GetMessageWindow(context.Background(), "bad-session-id", 1, 0, 0)
	require.ErrorContains(t, err, "session id")

	_, err = store.GetMessageWindow(context.Background(), testSessionA, 1, -1, 0)
	require.EqualError(t, err, "before and after must be greater than or equal to zero")

	records, err = store.GetMessageWindow(context.Background(), testSessionA, 99, 1, 1)
	require.NoError(t, err)
	require.Nil(t, records)

	boom := errors.New("anchor lookup failed")
	require.NoError(t, store.db.Callback().Query().Before("gorm:query").Register("test:get-message-window-anchor-error", func(tx *gorm.DB) {
		_ = tx.AddError(boom)
	}))

	_, err = store.GetMessageWindow(context.Background(), testSessionA, 1, 0, 0)
	require.ErrorIs(t, err, boom)
	require.NoError(t, store.db.Callback().Query().Remove("test:get-message-window-anchor-error"))

	queryCount := 0
	boom = errors.New("window lookup failed")
	require.NoError(t, store.db.Callback().Query().Before("gorm:query").Register("test:get-message-window-range-error", func(tx *gorm.DB) {
		queryCount++
		if queryCount == 2 {
			_ = tx.AddError(boom)
		}
	}))
	defer func() {
		require.NoError(t, store.db.Callback().Query().Remove("test:get-message-window-range-error"))
	}()

	_, err = store.GetMessageWindow(context.Background(), testSessionA, 1, 0, 1)
	require.ErrorIs(t, err, boom)
}

func TestSQLiteStore_MessageRoundTripPreservesAssistantToolCalls(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{
			Role:      morphmsg.RoleAssistant,
			ToolCalls: []morphmsg.ToolCall{{ID: "call-1", Name: "time", Input: `{"zone":"utc"}`}},
			CreatedAt: now,
		},
	}))

	messages, err := store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{})
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Empty(t, messages[0].Content)
	require.Equal(t, []morphmsg.ToolCall{{ID: "call-1", Name: "time", Input: `{"zone":"utc"}`}}, messages[0].ToolCalls)

	message, ok, err := store.GetMessage(context.Background(), testSessionA, 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Empty(t, message.Content)
	require.Equal(t, []morphmsg.ToolCall{{ID: "call-1", Name: "time", Input: `{"zone":"utc"}`}}, message.ToolCalls)
}

func TestSQLiteStore_GetPreservesZeroUpdatedAt(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	require.NoError(t, store.db.Exec("INSERT INTO sessions (id, updated_at, created_at) VALUES (?, ?, ?)", testSessionZero, time.Time{}, time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)).Error)

	session, ok, err := store.Get(context.Background(), testSessionZero, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, session.UpdatedAt.IsZero())

	sessions, err := store.List(context.Background(), base.SessionListOptions{})
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	require.Equal(t, testSessionZero, sessions[0].ID)
	require.True(t, sessions[0].UpdatedAt.IsZero())
}

func TestSQLiteStore_ListOrdersTiesByID(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.db.Exec("INSERT INTO sessions (id, updated_at, created_at) VALUES (?, ?, ?), (?, ?, ?)", testSessionZeta, now, now, testSessionAlpha, now, now).Error)

	sessions, err := store.List(context.Background(), base.SessionListOptions{})
	require.NoError(t, err)
	require.Len(t, sessions, 2)
	require.Equal(t, testSessionAlpha, sessions[0].ID)
	require.Equal(t, testSessionZeta, sessions[1].ID)
}

func TestSQLiteStore_Delete(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	require.EqualError(t, store.Delete(context.Background(), ""), "session id is required")
	require.EqualError(t, store.Delete(context.Background(), DefaultSessionID), "default session cannot be deleted")
	require.EqualError(t, store.Delete(context.Background(), testMissingSession), "session not found")
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: now},
	}))
	require.NoError(t, store.SetCurrent(context.Background(), testSessionA))

	require.NoError(t, store.Delete(context.Background(), testSessionA))

	session, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, Session{}, session)
	messages, err := store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{})
	require.NoError(t, err)
	require.Nil(t, messages)
	current, ok, err := store.Current(context.Background())
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, current)
}

func TestSQLiteStore_NilReceiverErrors(t *testing.T) {
	var store *Store

	require.EqualError(t, store.Save(context.Background(), Session{ID: testSessionA}), "store is required")
	require.EqualError(t, store.Delete(context.Background(), testSessionA), "store is required")
	require.EqualError(
		t,
		store.AppendMessages(
			context.Background(),
			testSessionA,
			[]morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
		),
		"store is required",
	)

	session, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.EqualError(t, err, "store is required")
	require.False(t, ok)
	require.Equal(t, Session{}, session)

	sessions, err := store.List(context.Background(), base.SessionListOptions{})
	require.EqualError(t, err, "store is required")
	require.Nil(t, sessions)

	messages, err := store.GetMessages(context.Background(), "", MessageQueryOptions{})
	require.EqualError(t, err, "store is required")
	require.Nil(t, messages)

	message, ok, err := store.GetMessage(context.Background(), "", 0)
	require.EqualError(t, err, "store is required")
	require.False(t, ok)
	require.Equal(t, morphmsg.Message{}, message)

	records, err := store.GetMessagesByIDs(context.Background(), testSessionA, []uint{1})
	require.EqualError(t, err, "store is required")
	require.Nil(t, records)
	records, err = store.GetMessageWindow(context.Background(), testSessionA, 1, 0, 0)
	require.EqualError(t, err, "store is required")
	require.Nil(t, records)

	count, err := store.CountMessages(context.Background(), DefaultSessionID, MessageQueryOptions{})
	require.EqualError(t, err, "store is required")
	require.Zero(t, count)

	require.EqualError(
		t,
		store.ClearMessages(context.Background(), testSessionA),
		"store is required",
	)
	require.EqualError(
		t,
		store.SaveSummary(
			context.Background(),
			SessionSummary{SessionID: testSessionA, SessionSummary: "summary"},
		),
		"store is required",
	)

	summary, ok, err := store.GetSummary(context.Background(), testSessionA)
	require.EqualError(t, err, "store is required")
	require.False(t, ok)
	require.Equal(t, SessionSummary{}, summary)

	require.EqualError(
		t,
		store.DeleteSummary(context.Background(), testSessionA),
		"store is required",
	)

	require.EqualError(
		t,
		store.DeleteExpiredArchives(context.Background(), time.Now().UTC()),
		"store is required",
	)
	require.EqualError(
		t,
		store.SetCurrent(context.Background(), DefaultSessionID),
		"store is required",
	)
	require.EqualError(t, store.ClearCurrent(context.Background()), "store is required")

	current, ok, err := store.Current(context.Background())
	require.EqualError(t, err, "store is required")
	require.False(t, ok)
	require.Empty(t, current)
}

func TestSQLiteStore_MessageEncodingHelpers(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)

	require.Nil(t, messagesToMessageModels("session-1", nil))
	require.Nil(t, messageModels(nil).messages())

	sessionRecords := messagesToMessageModels("session-1", []morphmsg.Message{
		{
			ID:         1,
			Role:       morphmsg.RoleUser,
			Name:       "user",
			Content:    "hello",
			ToolCallID: "call-1",
			CreatedAt:  now,
		},
	})
	require.Len(t, sessionRecords, 1)
	require.Equal(t, "session-1", sessionRecords[0].SessionID)
	require.Equal(t, 0, sessionRecords[0].Sequence)
	require.Equal(t, 1, int(sessionRecords[0].ID))

	decodedSession := messageModels(sessionRecords).messages()
	require.Len(t, decodedSession, 1)
	require.Equal(t, sessionRecords[0].ID, decodedSession[0].ID)
	require.Equal(t, morphmsg.RoleUser, decodedSession[0].Role)
	require.Equal(t, "user", decodedSession[0].Name)
	require.Equal(t, "hello", decodedSession[0].Content)
	require.Equal(t, "call-1", decodedSession[0].ToolCallID)
	require.Equal(t, 1, int(decodedSession[0].ID))
	require.Equal(t, now, decodedSession[0].CreatedAt)
}

func TestSQLiteStore_GetAndHelperFunctionsEdgeCases(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	session, ok, err := store.Get(context.Background(), "ses_invalid", base.SessionGetOptions{})
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
	require.False(t, ok)
	require.Equal(t, Session{}, session)

	require.Equal(t, base.MessageOrderAsc, getMessageQueryOrder(MessageQueryOptions{}))
	require.Equal(t, base.MessageOrderAsc, getMessageQueryOrder(MessageQueryOptions{Order: "bogus"}))

	message := morphmsg.Message{
		Role:    morphmsg.RoleTool,
		Content: `{"status":"running"}`,
	}
	require.Contains(t, morphmsg.MessageSearchText(message), "status running")
	require.Empty(t, morphmsg.MessageSearchText(morphmsg.Message{Role: morphmsg.RoleUser, Content: "plain"}))

	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.db.Callback().Query().After("gorm:after_query").Register("test:blank_session_id", func(tx *gorm.DB) {
		record, ok := tx.Statement.Dest.(*sessionModel)
		if ok {
			record.ID = "   "
		}
	}))
	t.Cleanup(func() {
		_ = store.db.Callback().Query().Remove("test:blank_session_id")
	})

	session, ok, err = store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.EqualError(t, err, "session id is required")
	require.False(t, ok)
	require.Equal(t, Session{}, session)
}

func TestSQLiteStore_GetMessagesPopulatesMessageIDs(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))

	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "hello"},
		{Role: morphmsg.RoleAssistant, Content: "world"},
	}))

	messages, err := store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{})
	require.NoError(t, err)
	require.Len(t, messages, 2)
	require.NotZero(t, messages[0].ID)
	require.NotZero(t, messages[1].ID)
	require.NotEqual(t, messages[0].ID, messages[1].ID)
}

func TestSQLiteStore_DecodeRecordHelpers(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)

	session, err := sessionModelToSession(sessionModel{ID: testSessionA, UpdatedAt: now})
	require.NoError(t, err)
	require.Equal(t, testSessionA, session.ID)
	require.True(t, session.CreatedAt.IsZero())

}

func TestSQLiteStore_ErrorPathsFromBrokenTables(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)

	t.Run("clear messages delete branch", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.db.Migrator().DropTable(&messageModel{}))

		err = store.ClearMessages(context.Background(), testSessionA)
		require.Error(t, err)
	})

	t.Run("save parent branch", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.db.Migrator().DropTable(&sessionModel{}))

		err = store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now})
		require.Error(t, err)
	})

	t.Run("get first branch", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.db.Migrator().DropTable(&sessionModel{}))

		_, _, err = store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
		require.Error(t, err)
	})

	t.Run("rename first branch", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.db.Migrator().DropTable(&sessionModel{}))

		_, err = store.Rename(context.Background(), base.SessionRenameRequest{SessionID: testSessionA, Title: "Title"})
		require.Error(t, err)
	})

	t.Run("rename update branch", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.db")
		store, err := NewStore(path)
		require.NoError(t, err)
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.Close())

		readonlyDB, err := gorm.Open(sqlite.Open("file:"+path+"?mode=ro"), &gorm.Config{})
		require.NoError(t, err)

		readonlyStore := &Store{db: readonlyDB}
		_, err = readonlyStore.Rename(context.Background(), base.SessionRenameRequest{
			SessionID: testSessionA,
			Title:     "Title",
		})
		require.Error(t, err)
	})

	t.Run("rename decode branch", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.db.Callback().Update().After("gorm:update").Register(
			"morph:test:corrupt_renamed_session",
			func(tx *gorm.DB) {
				record, ok := tx.Statement.Model.(*sessionModel)
				if ok {
					record.ID = " "
				}
			},
		))

		_, err = store.Rename(context.Background(), base.SessionRenameRequest{
			SessionID: testSessionA,
			Title:     "Title",
		})
		require.EqualError(t, err, "session id is required")
	})

	t.Run("get message branch", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.db.Create(&sessionModel{ID: testSessionA, UpdatedAt: now}).Error)
		require.NoError(t, store.db.Migrator().DropTable(&messageModel{}))

		_, err = store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{})
		require.Error(t, err)
	})

	t.Run("list records branch", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.db.Migrator().DropTable(&sessionModel{}))

		_, err = store.List(context.Background(), base.SessionListOptions{})
		require.Error(t, err)
	})

	t.Run("list messages branch", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.db.Create(&sessionModel{ID: testSessionA, UpdatedAt: now}).Error)
		require.NoError(t, store.db.Migrator().DropTable(&messageModel{}))

		_, err = store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{})
		require.Error(t, err)
	})

	t.Run("list decode branch", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.db.Exec("INSERT INTO sessions (id, updated_at, created_at) VALUES (?, ?, ?)", "   ", now, now).Error)

		_, err = store.List(context.Background(), base.SessionListOptions{})
		require.Error(t, err)
	})

	t.Run("append create branch", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.db.Exec("CREATE TRIGGER fail_session_message_insert BEFORE INSERT ON session_messages BEGIN SELECT RAISE(FAIL, 'boom'); END;").Error)

		err = store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: now}})
		require.Error(t, err)
	})

	t.Run("delete expired branch", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.db.Migrator().DropTable(&sessionModel{}))

		err = store.DeleteExpiredArchives(context.Background(), now)
		require.Error(t, err)
	})

	t.Run("set current get error", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.db.Migrator().DropTable(&sessionModel{}))

		err = store.SetCurrent(context.Background(), testSessionA)
		require.Error(t, err)
	})

	t.Run("current query error", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.db.Migrator().DropTable(&stateModel{}))

		_, _, err = store.Current(context.Background())
		require.Error(t, err)
	})
}

func TestSQLiteStore_SaveRoundTripsLastPromptTokens(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	now := time.Now().UTC()
	require.NoError(t, store.Save(context.Background(), Session{
		ID:               testSessionA,
		LastPromptTokens: 321,
		UpdatedAt:        now,
	}))

	session, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 321, session.LastPromptTokens)

	session.LastPromptTokens = 42
	require.NoError(t, store.Save(context.Background(), session))
	session, ok, err = store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 42, session.LastPromptTokens)

	session.LastPromptTokens = 0
	require.NoError(t, store.Save(context.Background(), session))
	session, ok, err = store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Zero(t, session.LastPromptTokens)
}

func TestSQLiteStore_SavePersistsArchiveTimesAsUTC(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	location := time.FixedZone("offset", 2*60*60)
	archivedAt := time.Date(2026, 5, 2, 10, 0, 0, 0, location)
	expiresAt := archivedAt.Add(time.Hour)
	require.NoError(t, store.Save(context.Background(), Session{
		ID:         testSessionA,
		Archived:   true,
		ArchivedAt: archivedAt,
		ExpiresAt:  expiresAt,
	}))

	session, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, session.Archived)
	require.Equal(t, archivedAt.UTC(), session.ArchivedAt)
	require.Equal(t, expiresAt.UTC(), session.ExpiresAt)
}

func TestSQLiteStore_UpdateCheckpointsUpdatesEpisodicOffset(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))

	offset := 12
	require.NoError(t, store.UpdateCheckpoints(context.Background(), testSessionA, CheckpointPatch{
		EpisodicOffset: &offset,
	}))
	session, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 12, session.EpisodicCheckpointOffset)

	offset = 4
	require.NoError(t, store.UpdateCheckpoints(context.Background(), testSessionA, CheckpointPatch{
		EpisodicOffset: &offset,
	}))
	session, ok, err = store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 12, session.EpisodicCheckpointOffset)

	require.NoError(t, store.db.Model(&sessionModel{}).
		Where("id = ?", testSessionA).
		Update("episodic_checkpoint_offset", nil).Error)
	offset = 22
	require.NoError(t, store.UpdateCheckpoints(context.Background(), testSessionA, CheckpointPatch{
		EpisodicOffset: &offset,
	}))
	session, ok, err = store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 22, session.EpisodicCheckpointOffset)

	offset = -1
	require.EqualError(t, store.UpdateCheckpoints(context.Background(), testSessionA, CheckpointPatch{
		EpisodicOffset: &offset,
	}), "episodic checkpoint offset must be greater than or equal to zero")
	offset = 1
	require.EqualError(t, store.UpdateCheckpoints(context.Background(), testMissingSession, CheckpointPatch{
		EpisodicOffset: &offset,
	}), "session not found")
}

func TestSQLiteStore_UpdateCheckpointsUpdatesReflectionOffset(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))

	offset := 12
	require.NoError(t, store.UpdateCheckpoints(context.Background(), testSessionA, CheckpointPatch{
		ReflectionOffset: &offset,
	}))
	session, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 12, session.ReflectionCheckpointOffset)

	offset = 4
	require.NoError(t, store.UpdateCheckpoints(context.Background(), testSessionA, CheckpointPatch{
		ReflectionOffset: &offset,
	}))
	session, ok, err = store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 12, session.ReflectionCheckpointOffset)

	require.NoError(t, store.db.Model(&sessionModel{}).
		Where("id = ?", testSessionA).
		Update("reflection_checkpoint_offset", nil).Error)
	offset = 22
	require.NoError(t, store.UpdateCheckpoints(context.Background(), testSessionA, CheckpointPatch{
		ReflectionOffset: &offset,
	}))
	session, ok, err = store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 22, session.ReflectionCheckpointOffset)

	offset = -1
	require.EqualError(t, store.UpdateCheckpoints(context.Background(), testSessionA, CheckpointPatch{
		ReflectionOffset: &offset,
	}), "reflection checkpoint offset must be greater than or equal to zero")
	offset = 1
	require.EqualError(t, store.UpdateCheckpoints(context.Background(), testMissingSession, CheckpointPatch{
		ReflectionOffset: &offset,
	}), "session not found")
}

func TestSQLiteStore_UpdateCheckpointsValidationAndDatabaseErrors(t *testing.T) {
	var nilStore *Store
	offset := 1
	require.EqualError(t, nilStore.UpdateCheckpoints(context.Background(), testSessionA, CheckpointPatch{
		EpisodicOffset: &offset,
	}), "store is required")

	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	require.EqualError(t, store.UpdateCheckpoints(context.Background(), "ses_invalid", CheckpointPatch{
		EpisodicOffset: &offset,
	}), "session id must be a valid ses_ nanoid")

	require.NoError(t, store.db.Migrator().DropTable(&sessionModel{}))
	err = store.UpdateCheckpoints(context.Background(), testSessionA, CheckpointPatch{
		EpisodicOffset: &offset,
	})
	require.Error(t, err)
}

func TestSQLiteStore_UpdateCheckpointsReturnsReflectionUpdateError(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)
	require.NoError(t, store.db.Migrator().DropTable(&sessionModel{}))

	offset := 1
	err = store.UpdateCheckpoints(context.Background(), testSessionA, CheckpointPatch{
		ReflectionOffset: &offset,
	})
	require.Error(t, err)
}

func TestSQLiteStore_UpdateCheckpointsReturnsCountError(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)
	require.NoError(t, store.db.Migrator().DropTable(&sessionModel{}))

	err = store.UpdateCheckpoints(context.Background(), testSessionA, CheckpointPatch{})
	require.Error(t, err)
}

func TestSQLiteStore_SaveRejectsInvalidSessionID(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	err = store.Save(context.Background(), Session{ID: "ses_invalid"})
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
}

func TestSQLiteStore_SavePersistsSessionTitle(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	require.NoError(t, store.Save(context.Background(), Session{
		ID:          testSessionA,
		Title:       "  Project Planning  ",
		TitleSource: base.SessionTitleSourceGenerated,
	}))

	session, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "Project Planning", session.Title)
	require.Equal(t, base.SessionTitleSourceGenerated, session.TitleSource)

	sessions, err := store.List(context.Background(), base.SessionListOptions{})
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	require.Equal(t, "Project Planning", sessions[0].Title)

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	session, ok, err = store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "Project Planning", session.Title)
	require.Equal(t, base.SessionTitleSourceGenerated, session.TitleSource)
}

func TestSQLiteStore_RenameUpdatesSessionTitle(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	createdAt := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	renamedAt := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{
		ID:          testSessionA,
		Title:       "Initial",
		TitleSource: base.SessionTitleSourceGenerated,
		CreatedAt:   createdAt,
		UpdatedAt:   createdAt,
	}))

	renamed, err := store.Rename(context.Background(), base.SessionRenameRequest{
		SessionID:   "  " + testSessionA + "  ",
		Title:       "  Project Planning  ",
		TitleSource: "  manual  ",
		RenamedAt:   renamedAt,
	})

	require.NoError(t, err)
	require.Equal(t, testSessionA, renamed.ID)
	require.Equal(t, "Project Planning", renamed.Title)
	require.Equal(t, base.SessionTitleSourceManual, renamed.TitleSource)
	require.Equal(t, renamedAt, renamed.UpdatedAt)
	require.Equal(t, createdAt, renamed.CreatedAt)

	session, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "Project Planning", session.Title)
	require.Equal(t, base.SessionTitleSourceManual, session.TitleSource)
	require.Equal(t, renamedAt, session.UpdatedAt)
	require.Equal(t, createdAt, session.CreatedAt)
}

func TestSQLiteStore_RenameValidatesInput(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	_, err = (*Store)(nil).Rename(context.Background(), base.SessionRenameRequest{SessionID: testSessionA, Title: "Title"})
	require.EqualError(t, err, "store is required")

	_, err = store.Rename(context.Background(), base.SessionRenameRequest{SessionID: "", Title: "Title"})
	require.EqualError(t, err, "session id is required")

	_, err = store.Rename(context.Background(), base.SessionRenameRequest{SessionID: "project-a", Title: "Title"})
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")

	_, err = store.Rename(context.Background(), base.SessionRenameRequest{SessionID: testSessionA, Title: " "})
	require.EqualError(t, err, "session title is required")

	_, err = store.Rename(context.Background(), base.SessionRenameRequest{SessionID: testSessionA, Title: "Title"})
	require.EqualError(t, err, "session not found")
}

func TestSQLiteStore_SavePreservesExistingCreatedAtAndAllowsPromptTokenOverwrite(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	originalCreatedAt := time.Now().UTC().Add(-time.Hour)
	updatedAt := time.Now().UTC()
	require.NoError(t, store.Save(context.Background(), Session{
		ID:               testSessionA,
		CreatedAt:        originalCreatedAt,
		LastPromptTokens: 123,
		UpdatedAt:        updatedAt,
	}))

	require.NoError(t, store.Save(context.Background(), Session{
		ID:               testSessionA,
		CreatedAt:        time.Now().UTC(),
		LastPromptTokens: 456,
		UpdatedAt:        updatedAt.Add(time.Minute),
	}))

	session, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, originalCreatedAt, session.CreatedAt)
	require.Equal(t, 456, session.LastPromptTokens)
}

func TestSQLiteStore_SaveRefreshesUpdatedAtOnUpdate(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	originalUpdatedAt := time.Now().UTC().Add(-time.Hour)
	require.NoError(t, store.Save(context.Background(), Session{
		ID:        testSessionA,
		UpdatedAt: originalUpdatedAt,
	}))

	require.NoError(t, store.Save(context.Background(), Session{
		ID:        testSessionA,
		UpdatedAt: originalUpdatedAt,
	}))

	session, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, session.UpdatedAt.After(originalUpdatedAt))
}

func TestSQLiteStore_SaveRoundTripsCompactionMetadata(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	now := time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{
		ID: testSessionA,
		Compaction: SessionCompaction{
			CompletedAt:        now.Add(3 * time.Minute),
			FailedAt:           now.Add(2 * time.Minute),
			LastError:          "failed before retry",
			RequestedAt:        now,
			StartedAt:          now.Add(time.Minute),
			Status:             base.CompactionStatusFailed,
			TargetMessageCount: 12,
			TargetOffset:       4,
		},
		UpdatedAt: now,
	}))

	session, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, base.CompactionStatusFailed, session.Compaction.Status)
	require.Equal(t, "failed before retry", session.Compaction.LastError)
	require.Equal(t, 12, session.Compaction.TargetMessageCount)
	require.Equal(t, 4, session.Compaction.TargetOffset)
	require.Equal(t, now, session.Compaction.RequestedAt)
	require.Equal(t, now.Add(time.Minute), session.Compaction.StartedAt)
	require.Equal(t, now.Add(2*time.Minute), session.Compaction.FailedAt)
	require.Equal(t, now.Add(3*time.Minute), session.Compaction.CompletedAt)

	sessions, err := store.List(context.Background(), base.SessionListOptions{})
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	require.Equal(t, session.Compaction, sessions[0].Compaction)
}

func TestSQLiteStore_SavePreservesExistingCompactionMetadataOnPartialUpdate(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	now := time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{
		ID: testSessionA,
		Compaction: SessionCompaction{
			LastError:          "failed before retry",
			RequestedAt:        now,
			Status:             base.CompactionStatusFailed,
			TargetMessageCount: 12,
			TargetOffset:       4,
		},
		UpdatedAt: now,
	}))

	require.NoError(t, store.Save(context.Background(), Session{
		ID:        testSessionA,
		UpdatedAt: now.Add(time.Minute),
	}))

	session, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, base.CompactionStatusFailed, session.Compaction.Status)
	require.Equal(t, "failed before retry", session.Compaction.LastError)
	require.Equal(t, 12, session.Compaction.TargetMessageCount)
	require.Equal(t, 4, session.Compaction.TargetOffset)
}

func TestSQLiteStore_ClearMessagesClearsCompactionMetadata(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	now := time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{
		ID: testSessionA,
		Compaction: SessionCompaction{
			Status:             base.CompactionStatusRunning,
			TargetMessageCount: 12,
			TargetOffset:       4,
		},
		UpdatedAt: now,
	}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: now}}))
	require.NoError(t, store.SaveSummary(context.Background(), SessionSummary{
		SessionID:      testSessionA,
		SessionSummary: "Older work",
	}))

	require.NoError(t, store.ClearMessages(context.Background(), testSessionA))

	session, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, SessionCompaction{}, session.Compaction)
}

func TestSQLiteStore_SaveTrimsIDOnCreate(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	require.NoError(t, store.Save(context.Background(), Session{ID: "  " + testSessionA + "  "}))

	session, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, testSessionA, session.ID)
	require.False(t, session.CreatedAt.IsZero())
	require.False(t, session.UpdatedAt.IsZero())
}

func TestSQLiteStore_GetRejectsInvalidSessionID(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	session, ok, err := store.Get(context.Background(), "ses_invalid", base.SessionGetOptions{})

	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
	require.False(t, ok)
	require.Equal(t, Session{}, session)
}

func TestSQLiteStore_AppendMessagesEdgeCases(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	require.EqualError(t, store.AppendMessages(context.Background(), "ses_invalid", []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}}), "session id must be a valid ses_ nanoid")
	require.EqualError(t, store.AppendMessages(context.Background(), testMissingSession, []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}}), "session not found")
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, nil))

	require.NoError(t, store.db.Migrator().DropTable(&messageModel{}))
	require.Error(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}}))
}

func TestSQLiteStore_GetMessagesEdgeCases(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	messages, err := store.GetMessages(context.Background(), "", MessageQueryOptions{})
	require.NoError(t, err)
	require.Nil(t, messages)

	messages, err = store.GetMessages(context.Background(), "ses_invalid", MessageQueryOptions{})
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
	require.Nil(t, messages)

	messages, err = store.GetMessages(context.Background(), "archive_invalid", MessageQueryOptions{})
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
	require.Nil(t, messages)
}

func TestSQLiteStore_GetMessagesRejectsInvalidOrder(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	messages, err := store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{Order: "sideways"})

	require.EqualError(t, err, "message order must be asc or desc")
	require.Nil(t, messages)
}

func TestSQLiteStore_CountMessagesRejectsInvalidOrder(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	count, err := store.CountMessages(context.Background(), testSessionA, MessageQueryOptions{Order: "sideways"})

	require.EqualError(t, err, "message order must be asc or desc")
	require.Zero(t, count)
}

func TestSQLiteStore_GetMessagesSupportsOffsetAndLimit(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "one", CreatedAt: now},
		{Role: morphmsg.RoleUser, Content: "two", CreatedAt: now},
		{Role: morphmsg.RoleUser, Content: "three", CreatedAt: now},
		{Role: morphmsg.RoleUser, Content: "four", CreatedAt: now},
	}))

	messages, err := store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{Offset: 1, Limit: 2})
	require.NoError(t, err)
	require.Len(t, messages, 2)
	require.Equal(t, "two", messages[0].Content)
	require.Equal(t, "three", messages[1].Content)

	messages, err = store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{Offset: -1, Limit: 2})
	require.NoError(t, err)
	require.Len(t, messages, 2)
	require.Equal(t, "one", messages[0].Content)
	require.Equal(t, "two", messages[1].Content)

	messages, err = store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{Offset: 99})
	require.NoError(t, err)
	require.Nil(t, messages)

	_, err = store.Archive(context.Background(), testSessionA, base.SessionArchiveRequest{
		ArchivedAt: now,
		ExpiresAt:  now.Add(time.Hour),
	})
	require.NoError(t, err)
	messages, err = store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{Offset: 0, Limit: 2})
	require.NoError(t, err)
	require.Len(t, messages, 2)
	require.Equal(t, "one", messages[0].Content)
	require.Equal(t, "two", messages[1].Content)

	messages, err = store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{})
	require.NoError(t, err)
	require.Len(t, messages, 4)
}

func TestSQLiteStore_GetAndCountMessagesSupportRoleAndNameFilters(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: now},
		{Role: morphmsg.RoleTool, Name: "plan_tool", Content: "plan-1", ToolCallID: "call-1", CreatedAt: now},
		{Role: morphmsg.RoleTool, Name: "other_tool", Content: "other", ToolCallID: "call-2", CreatedAt: now},
		{Role: morphmsg.RoleTool, Name: "plan_tool", Content: "plan-2", ToolCallID: "call-3", CreatedAt: now},
	}))

	count, err := store.CountMessages(context.Background(), testSessionA, MessageQueryOptions{
		Role: morphmsg.RoleTool,
		Name: "plan_tool",
	})
	require.NoError(t, err)
	require.Equal(t, 2, count)

	messages, err := store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{
		Role:   morphmsg.RoleTool,
		Name:   "plan_tool",
		Offset: 1,
		Limit:  1,
	})
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, "plan-2", messages[0].Content)
	require.Equal(t, "plan_tool", messages[0].Name)
}

func TestSQLiteStore_SearchMessagesSupportsStructuredAndToolFilters(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "hello plain text", CreatedAt: now},
		{Role: morphmsg.RoleAssistant, Content: "assistant summary", ToolCalls: []morphmsg.ToolCall{
			{ID: "call-1", Name: "process", Input: `{"action":"start"}`},
			{ID: "call-2", Name: "search_files", Input: `{"pattern":"needle"}`},
		}, CreatedAt: now.Add(time.Second)},
		{Role: morphmsg.RoleTool, Name: "process", Content: `{"status":"running"}`,
			ToolCallID: "call-3", CreatedAt: now.Add(2 * time.Second)},
	}))

	results, err := store.SearchMessages(context.Background(), testSessionA, SearchMessageOptions{
		Query: "needle",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, 1, results[0].MatchCount)
	require.Len(t, results[0].Messages, 1)
	require.Equal(t, morphmsg.RoleAssistant, results[0].Messages[0].Message.Role)
	require.Equal(t, "search_files", results[0].Messages[0].MatchedToolName)

	results, err = store.SearchMessages(context.Background(), testSessionA, SearchMessageOptions{
		Query: "summary",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Len(t, results[0].Messages, 1)
	require.Equal(t, morphmsg.RoleAssistant, results[0].Messages[0].Message.Role)
	require.Equal(t, "assistant summary", results[0].Messages[0].MatchedText)

	results, err = store.SearchMessages(context.Background(), testSessionA, SearchMessageOptions{
		Query:    "needle",
		ToolName: "process",
	})
	require.NoError(t, err)
	require.Empty(t, results)

	results, err = store.SearchMessages(context.Background(), testSessionA, SearchMessageOptions{
		Query:    "running",
		ToolName: "process",
		Role:     morphmsg.RoleTool,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Len(t, results[0].Messages, 1)
	require.Equal(t, "process", results[0].Messages[0].Message.Name)
	require.Equal(t, "call-3", results[0].Messages[0].Message.ToolCallID)
	require.Equal(t, "process", results[0].Messages[0].MatchedToolName)
}

func TestSQLiteStore_SearchMessagesUsesDerivedStructuredSearchText(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{
		Role: morphmsg.RoleAssistant,
		ToolCalls: []morphmsg.ToolCall{
			{ID: "call-1", Name: "process", Input: `{"action":"start"}`},
			{ID: "call-2", Name: "search_files", Input: `{"pattern":"needle"}`},
		},
		CreatedAt: now,
	}}))

	results, err := store.SearchMessages(context.Background(), testSessionA, SearchMessageOptions{
		Query: "tool_name process",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Len(t, results[0].Messages, 1)
	require.Equal(t, morphmsg.RoleAssistant, results[0].Messages[0].Message.Role)
	require.Equal(t, "process", results[0].Messages[0].MatchedToolName)

	results, err = store.SearchMessages(context.Background(), testSessionA, SearchMessageOptions{
		Query:    "tool_name process",
		ToolName: "process",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
}

func TestSQLiteStore_SearchMessagesSelectsBestDuplicateFTSRowByScore(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{
		Role:    morphmsg.RoleAssistant,
		Content: "needle needle needle durable ranking context",
		ToolCalls: []morphmsg.ToolCall{
			{ID: "call-1", Name: "lookup", Input: `{"pattern":"needle"}`},
		},
		CreatedAt: now,
	}}))

	results, err := store.SearchMessages(context.Background(), testSessionA, SearchMessageOptions{
		Query: "needle",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Len(t, results[0].Messages, 1)
	require.Equal(t, "needle needle needle durable ranking context", results[0].Messages[0].MatchedText)
	require.Empty(t, results[0].Messages[0].MatchedToolName)
}

func TestSQLiteStore_SearchMessagesSupportsCrossSessionScope(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionB, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "origin needle", CreatedAt: now},
	}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionB, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "other needle", CreatedAt: now.Add(time.Second)},
	}))

	results, err := store.SearchMessages(context.Background(), "", SearchMessageOptions{
		IgnoreSessionID: testSessionA,
		Query:           "needle",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, testSessionB, results[0].SessionID)
	require.Equal(t, "other needle", results[0].Messages[0].MatchedText)

	results, err = store.SearchMessages(context.Background(), testSessionA, SearchMessageOptions{
		Query: "needle",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "origin needle", results[0].Messages[0].MatchedText)

	results, err = store.SearchMessages(context.Background(), "", SearchMessageOptions{
		Query: "needle",
	})
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, []string{testSessionB, testSessionA}, []string{
		results[0].SessionID,
		results[1].SessionID,
	})
	require.Equal(t, "other needle", results[0].Messages[0].MatchedText)
	require.Equal(t, "origin needle", results[1].Messages[0].MatchedText)

	// search session with ignore session id
	// ignore directive has no effect when session id is provided
	results, err = store.SearchMessages(context.Background(), testSessionA, SearchMessageOptions{
		IgnoreSessionID: testSessionA,
		Query:           "needle",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "origin needle", results[0].Messages[0].MatchedText)
}

func TestSQLiteStore_SearchMessagesGroupedEdgeCases(t *testing.T) {
	var nilStore *Store

	results, err := nilStore.SearchMessages(context.Background(), testSessionA, SearchMessageOptions{Query: "hello"})
	require.EqualError(t, err, "store is required")
	require.Nil(t, results)

	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	results, err = store.SearchMessages(context.Background(), "", SearchMessageOptions{Query: "hello"})
	require.NoError(t, err)
	require.Nil(t, results)

	results, err = store.SearchMessages(context.Background(), "ses_invalid", SearchMessageOptions{Query: "hello"})
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
	require.Nil(t, results)

	results, err = store.SearchMessages(context.Background(), "", SearchMessageOptions{
		IgnoreSessionID: "ses_invalid",
		Query:           "hello",
	})
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
	require.Nil(t, results)

	results, err = store.SearchMessages(context.Background(), testSessionA, SearchMessageOptions{})
	require.NoError(t, err)
	require.Nil(t, results)
}

func TestSQLiteStore_SearchMessagesSupportsGroupedLimitsAndQueryErrors(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "hello zero", CreatedAt: now},
		{Role: morphmsg.RoleUser, Content: "hello one", CreatedAt: now.Add(time.Second)},
		{Role: morphmsg.RoleUser, Content: "hello two", CreatedAt: now.Add(2 * time.Second)},
	}))

	results, err := store.SearchMessages(context.Background(), testSessionA, SearchMessageOptions{
		Query:                 "hello",
		MaxMessagesPerSession: 2,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, 3, results[0].MatchCount)
	require.Equal(t, []string{"hello two", "hello one"}, []string{
		results[0].Messages[0].MatchedText,
		results[0].Messages[1].MatchedText,
	})

	results, err = store.SearchMessages(context.Background(), testSessionA, SearchMessageOptions{
		Query:    "hello",
		ToolName: "missing",
	})
	require.NoError(t, err)
	require.Nil(t, results)

	require.NoError(t, store.db.Migrator().DropTable(&messageModel{}))
	_, err = store.SearchMessages(context.Background(), testSessionA, SearchMessageOptions{Query: "hello"})
	require.Error(t, err)
}

func TestSQLiteStore_SearchMessagesSupportsGroupedResults(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "needle zero", CreatedAt: now},
		{Role: morphmsg.RoleAssistant, Content: "needle one", CreatedAt: now.Add(time.Second)},
		{Role: morphmsg.RoleUser, Content: "needle two", CreatedAt: now.Add(2 * time.Second)},
	}))

	results, err := store.SearchMessages(context.Background(), testSessionA, SearchMessageOptions{
		Query:                 "needle",
		MaxMessagesPerSession: 2,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, testSessionA, results[0].SessionID)
	require.Equal(t, 3, results[0].MatchCount)
	require.Equal(t, now.Add(2*time.Second), results[0].LastMatchedAt)
	require.Equal(t, []string{"needle two", "needle one"}, []string{
		results[0].Messages[0].MatchedText,
		results[0].Messages[1].MatchedText,
	})
}

func TestSQLiteStore_SearchMessagesRanksByRelevanceBeforeRecency(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionB, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "needle durable needle durable needle durable", CreatedAt: now},
		{Role: morphmsg.RoleUser, Content: "needle durable", CreatedAt: now.Add(time.Second)},
	}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionB, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "needle durable", CreatedAt: now.Add(2 * time.Second)},
	}))

	results, err := store.SearchMessages(context.Background(), testSessionA, SearchMessageOptions{
		Query:                 "needle durable",
		MaxMessagesPerSession: 1,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, 2, results[0].MatchCount)
	require.Equal(t, "needle durable needle durable needle durable", results[0].Messages[0].MatchedText)

	results, err = store.SearchMessages(context.Background(), "", SearchMessageOptions{
		Query:       "needle durable",
		MaxSessions: 1,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, testSessionA, results[0].SessionID)
	require.Equal(t, 2, results[0].MatchCount)
}

func TestSQLiteStore_SearchMessagesUsesRecencyWhenScoresTie(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionB, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "needle tie", CreatedAt: now},
		{Role: morphmsg.RoleUser, Content: "needle tie", CreatedAt: now.Add(time.Second)},
	}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionB, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "needle tie", CreatedAt: now.Add(2 * time.Second)},
	}))

	results, err := store.SearchMessages(context.Background(), testSessionA, SearchMessageOptions{
		Query:                 "needle tie",
		MaxMessagesPerSession: 1,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, 2, results[0].MatchCount)
	require.Len(t, results[0].Messages, 1)
	require.Equal(t, now.Add(time.Second), results[0].Messages[0].Message.CreatedAt)

	results, err = store.SearchMessages(context.Background(), "", SearchMessageOptions{
		Query:       "needle tie",
		MaxSessions: 1,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, testSessionB, results[0].SessionID)
	require.Equal(t, now.Add(2*time.Second), results[0].LastMatchedAt)
}

func TestSQLiteStore_SearchMessagesSupportsCrossSessionScopeAndOrdering(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionB, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "needle alpha", CreatedAt: now},
	}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionB, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "needle beta", CreatedAt: now},
	}))

	results, err := store.SearchMessages(context.Background(), "", SearchMessageOptions{
		Query:       "needle",
		MaxSessions: 1,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, testSessionA, results[0].SessionID)

	results, err = store.SearchMessages(context.Background(), "", SearchMessageOptions{
		IgnoreSessionID: testSessionA,
		Query:           "needle",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, testSessionB, results[0].SessionID)
}

func TestSQLiteStore_SearchMessagesEdgeCases(t *testing.T) {
	var nilStore *Store

	results, err := nilStore.SearchMessages(context.Background(), testSessionA, SearchMessageOptions{Query: "hello"})
	require.EqualError(t, err, "store is required")
	require.Nil(t, results)

	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	results, err = store.SearchMessages(context.Background(), "", SearchMessageOptions{Query: "hello"})
	require.NoError(t, err)
	require.Nil(t, results)

	results, err = store.SearchMessages(context.Background(), "ses_invalid", SearchMessageOptions{Query: "hello"})
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
	require.Nil(t, results)

	results, err = store.SearchMessages(context.Background(), "", SearchMessageOptions{
		IgnoreSessionID: "ses_invalid",
		Query:           "hello",
	})
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
	require.Nil(t, results)

	results, err = store.SearchMessages(context.Background(), testSessionA, SearchMessageOptions{})
	require.NoError(t, err)
	require.Nil(t, results)

	require.NoError(t, store.db.Migrator().DropTable(&messageModel{}))
	_, err = store.SearchMessages(context.Background(), testSessionA, SearchMessageOptions{Query: "hello"})
	require.Error(t, err)
}

func TestSQLiteStore_SearchMessagesSupportsRoleAndToolFilters(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleAssistant, ToolCalls: []morphmsg.ToolCall{
			{ID: "call-1", Name: "process", Input: `{"action":"start"}`},
		}, CreatedAt: now},
		{Role: morphmsg.RoleTool, Name: "process", Content: `{"status":"running"}`,
			ToolCallID: "call-1", CreatedAt: now.Add(time.Second)},
	}))

	results, err := store.SearchMessages(context.Background(), testSessionA, SearchMessageOptions{
		Query:    "running",
		Role:     morphmsg.RoleTool,
		ToolName: "process",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Len(t, results[0].Messages, 1)
	require.Equal(t, "process", results[0].Messages[0].MatchedToolName)
}

func TestSearchMessageResultTime_EdgeCases(t *testing.T) {
	require.True(t, getSearchSessionResultTime("").IsZero())
	require.True(t, getSearchSessionResultTime("not-a-time").IsZero())
}

func TestSQLiteStore_GetMessagesSupportsDescendingOrder(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleTool, Name: "plan_tool", Content: "plan-1", ToolCallID: "call-1", CreatedAt: now},
		{Role: morphmsg.RoleTool, Name: "plan_tool", Content: "plan-2", ToolCallID: "call-2", CreatedAt: now},
	}))

	messages, err := store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{
		Role:  morphmsg.RoleTool,
		Name:  "plan_tool",
		Order: "desc",
		Limit: 1,
	})
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, "plan-2", messages[0].Content)
}

func TestSQLiteStore_CountMessagesSupportsLiveAndArchivedQueries(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "one", CreatedAt: now},
		{Role: morphmsg.RoleUser, Content: "two", CreatedAt: now},
		{Role: morphmsg.RoleUser, Content: "three", CreatedAt: now},
	}))

	count, err := store.CountMessages(context.Background(), testSessionA, MessageQueryOptions{Offset: 1, Limit: 1})
	require.NoError(t, err)
	require.Equal(t, 3, count)

	_, err = store.Archive(context.Background(), testSessionA, base.SessionArchiveRequest{
		ArchivedAt: now,
		ExpiresAt:  now.Add(time.Hour),
	})
	require.NoError(t, err)

	count, err = store.CountMessages(context.Background(), testSessionA, MessageQueryOptions{Offset: 1, Limit: 1})
	require.NoError(t, err)
	require.Equal(t, 3, count)
}

func TestSQLiteStore_CountMessagesEdgeCases(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	count, err := store.CountMessages(context.Background(), "", MessageQueryOptions{})
	require.NoError(t, err)
	require.Zero(t, count)

	count, err = store.CountMessages(context.Background(), "ses_invalid", MessageQueryOptions{})
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
	require.Zero(t, count)

	count, err = store.CountMessages(context.Background(), "archive_invalid", MessageQueryOptions{})
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
	require.Zero(t, count)
}

func TestSQLiteStore_CountMessagesReturnsQueryErrors(t *testing.T) {
	now := time.Date(2026, 4, 2, 10, 0, 0, 0, time.UTC)

	t.Run("live query error", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.db.Migrator().DropTable(&messageModel{}))

		_, err = store.CountMessages(context.Background(), testSessionA, MessageQueryOptions{})
		require.Error(t, err)
	})

}

func TestSQLiteStore_GetMessageEdgeCases(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	message, ok, err := store.GetMessage(context.Background(), "", 0)
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, morphmsg.Message{}, message)

	message, ok, err = store.GetMessage(context.Background(), testSessionA, -1)
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, morphmsg.Message{}, message)

	message, ok, err = store.GetMessage(context.Background(), "ses_invalid", 0)
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
	require.False(t, ok)
	require.Equal(t, morphmsg.Message{}, message)

	message, ok, err = store.GetMessage(context.Background(), "archive_invalid", 0)
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
	require.False(t, ok)
	require.Equal(t, morphmsg.Message{}, message)

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	message, ok, err = store.GetMessage(context.Background(), testSessionA, 0)
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, morphmsg.Message{}, message)
}

func TestSQLiteStore_ClearMessagesEdgeCases(t *testing.T) {
	t.Run("live validation and missing", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)

		require.EqualError(t, store.ClearMessages(context.Background(), "ses_invalid"), "session id must be a valid ses_ nanoid")
		require.EqualError(t, store.ClearMessages(context.Background(), testMissingSession), "session not found")
	})

}

func TestSQLiteStore_DeleteErrorBranches(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)

	t.Run("first query error", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.db.Migrator().DropTable(&sessionModel{}))

		err = store.Delete(context.Background(), testSessionA)
		require.Error(t, err)
	})

	t.Run("message delete error", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: now}}))
		require.NoError(t, store.db.Exec("CREATE TRIGGER fail_session_message_delete BEFORE DELETE ON session_messages BEGIN SELECT RAISE(FAIL, 'boom'); END;").Error)

		err = store.Delete(context.Background(), testSessionA)
		require.Error(t, err)
	})

	t.Run("session delete error", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.db.Exec("CREATE TRIGGER fail_session_delete BEFORE DELETE ON sessions BEGIN SELECT RAISE(FAIL, 'boom'); END;").Error)

		err = store.Delete(context.Background(), testSessionA)
		require.Error(t, err)
	})

	t.Run("state delete error", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.SetCurrent(context.Background(), testSessionA))
		require.NoError(t, store.db.Migrator().DropTable(&stateModel{}))

		err = store.Delete(context.Background(), testSessionA)
		require.Error(t, err)
	})
}

func TestSQLiteStore_DeleteExpiredArchivesEdgeCases(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)

	t.Run("no expired archives", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: now}}))
		_, err = store.Archive(context.Background(), testSessionA, base.SessionArchiveRequest{
			ArchivedAt: now,
			ExpiresAt:  now.Add(time.Hour),
		})
		require.NoError(t, err)

		require.NoError(t, store.DeleteExpiredArchives(context.Background(), now))

		session, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
		require.NoError(t, err)
		require.True(t, ok)
		require.True(t, session.Archived)
	})

	t.Run("deletes expired archives", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: now}}))
		require.NoError(t, store.SaveSummary(context.Background(), SessionSummary{
			SessionID:      testSessionA,
			SessionSummary: "archived summary",
		}))
		require.NoError(t, store.SetCurrent(context.Background(), testSessionA))
		_, err = store.Archive(context.Background(), testSessionA, base.SessionArchiveRequest{
			ArchivedAt: now.Add(-2 * time.Hour),
			ExpiresAt:  now.Add(-time.Hour),
		})
		require.NoError(t, err)

		require.NoError(t, store.DeleteExpiredArchives(context.Background(), now))

		session, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
		require.NoError(t, err)
		require.False(t, ok)
		require.Equal(t, Session{}, session)

		messages, err := store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{})
		require.NoError(t, err)
		require.Nil(t, messages)

		_, ok, err = store.GetSummary(context.Background(), testSessionA)
		require.NoError(t, err)
		require.False(t, ok)

		current, ok, err := store.Current(context.Background())
		require.NoError(t, err)
		require.False(t, ok)
		require.Empty(t, current)
	})
}

func TestSQLiteStore_DeleteExpiredArchivesErrorBranches(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	setupExpiredArchive := func(t *testing.T) *Store {
		t.Helper()

		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{
			Role:      morphmsg.RoleUser,
			Content:   "hello",
			CreatedAt: now,
		}}))
		require.NoError(t, store.SaveSummary(context.Background(), SessionSummary{
			SessionID:      testSessionA,
			SessionSummary: "archived summary",
		}))
		require.NoError(t, store.SetCurrent(context.Background(), testSessionA))
		_, err = store.Archive(context.Background(), testSessionA, base.SessionArchiveRequest{
			ArchivedAt: now.Add(-2 * time.Hour),
			ExpiresAt:  now.Add(-time.Hour),
		})
		require.NoError(t, err)

		return store
	}

	t.Run("session id lookup error", func(t *testing.T) {
		store := setupExpiredArchive(t)
		require.NoError(t, store.db.Migrator().DropTable(&sessionModel{}))

		err := store.DeleteExpiredArchives(context.Background(), now)
		require.Error(t, err)
	})

	t.Run("message delete error", func(t *testing.T) {
		store := setupExpiredArchive(t)
		require.NoError(t, store.db.Exec("CREATE TRIGGER fail_expired_message_delete BEFORE DELETE ON session_messages BEGIN SELECT RAISE(FAIL, 'boom'); END;").Error)

		err := store.DeleteExpiredArchives(context.Background(), now)
		require.Error(t, err)
	})

	t.Run("search row delete error", func(t *testing.T) {
		store := setupExpiredArchive(t)
		require.NoError(t, store.db.Exec(`DROP TABLE `+sessionMessageSearchTable).Error)

		err := store.DeleteExpiredArchives(context.Background(), now)
		require.ErrorContains(t, err, "failed to delete session message search rows")
	})

	t.Run("summary delete error", func(t *testing.T) {
		store := setupExpiredArchive(t)
		require.NoError(t, store.db.Migrator().DropTable(&summaryModel{}))

		err := store.DeleteExpiredArchives(context.Background(), now)
		require.Error(t, err)
	})

	t.Run("state delete error", func(t *testing.T) {
		store := setupExpiredArchive(t)
		require.NoError(t, store.db.Migrator().DropTable(&stateModel{}))

		err := store.DeleteExpiredArchives(context.Background(), now)
		require.Error(t, err)
	})

	t.Run("session delete error", func(t *testing.T) {
		store := setupExpiredArchive(t)
		require.NoError(t, store.db.Exec("CREATE TRIGGER fail_expired_session_delete BEFORE DELETE ON sessions BEGIN SELECT RAISE(FAIL, 'boom'); END;").Error)

		err := store.DeleteExpiredArchives(context.Background(), now)
		require.Error(t, err)
	})
}

func TestSQLiteStore_ArchiveValidationAndErrorBranches(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)

	var nilStore *Store
	_, err := nilStore.Archive(context.Background(), testSessionA, base.SessionArchiveRequest{
		ArchivedAt: now,
		ExpiresAt:  now.Add(time.Hour),
	})
	require.EqualError(t, err, "store is required")

	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	_, err = store.Archive(context.Background(), "ses_invalid", base.SessionArchiveRequest{
		ArchivedAt: now,
		ExpiresAt:  now.Add(time.Hour),
	})
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")

	_, err = store.Archive(context.Background(), testMissingSession, base.SessionArchiveRequest{
		ArchivedAt: now,
		ExpiresAt:  now.Add(time.Hour),
	})
	require.EqualError(t, err, "session not found")

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	_, err = store.Archive(context.Background(), testSessionA, base.SessionArchiveRequest{
		ArchivedAt: now,
		ExpiresAt:  now.Add(time.Hour),
	})
	require.EqualError(t, err, "source session has no messages")

	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{
		Role:      morphmsg.RoleUser,
		Content:   "hello",
		CreatedAt: now,
	}}))
	_, err = store.Archive(context.Background(), testSessionA, base.SessionArchiveRequest{
		ArchivedAt: now,
	})
	require.EqualError(t, err, "archive expiry is required")
}

func TestSQLiteStore_ArchiveReturnsCountError(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.db.Migrator().DropTable(&messageModel{}))

	_, err = store.Archive(context.Background(), testSessionA, base.SessionArchiveRequest{
		ArchivedAt: now,
		ExpiresAt:  now.Add(time.Hour),
	})
	require.Error(t, err)
}

func TestSQLiteStore_ArchiveReturnsLookupError(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)
	require.NoError(t, store.db.Migrator().DropTable(&sessionModel{}))

	_, err = store.Archive(context.Background(), testSessionA, base.SessionArchiveRequest{
		ArchivedAt: now,
		ExpiresAt:  now.Add(time.Hour),
	})
	require.Error(t, err)
}

func TestSQLiteStore_ArchiveReturnsSessionDecodeError(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{
		Role:      morphmsg.RoleUser,
		Content:   "hello",
		CreatedAt: now,
	}}))
	require.NoError(t, store.db.Callback().Query().After("gorm:after_query").Register(
		"test:blank_session_id_for_archive",
		func(tx *gorm.DB) {
			record, ok := tx.Statement.Dest.(*sessionModel)
			if ok {
				record.ID = ""
			}
		},
	))

	_, err = store.Archive(context.Background(), testSessionA, base.SessionArchiveRequest{
		ArchivedAt: now,
		ExpiresAt:  now.Add(time.Hour),
	})
	require.EqualError(t, err, "session id is required")
}

func TestSQLiteStore_ArchiveReturnsSaveError(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{
		Role:      morphmsg.RoleUser,
		Content:   "hello",
		CreatedAt: now,
	}}))
	require.NoError(t, store.db.Exec("CREATE TRIGGER fail_archive_update BEFORE UPDATE ON sessions BEGIN SELECT RAISE(FAIL, 'boom'); END;").Error)

	_, err = store.Archive(context.Background(), testSessionA, base.SessionArchiveRequest{
		ArchivedAt: now,
		ExpiresAt:  now.Add(time.Hour),
	})
	require.Error(t, err)
}

func TestSQLiteStore_ArchiveReturnsStateDeleteError(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{
		Role:      morphmsg.RoleUser,
		Content:   "hello",
		CreatedAt: now,
	}}))
	require.NoError(t, store.db.Migrator().DropTable(&stateModel{}))

	_, err = store.Archive(context.Background(), testSessionA, base.SessionArchiveRequest{
		ArchivedAt: now,
		ExpiresAt:  now.Add(time.Hour),
	})
	require.Error(t, err)
}

func TestSQLiteStore_UnarchiveLifecycleAndErrors(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)

	var nilStore *Store
	_, err := nilStore.Unarchive(context.Background(), testSessionA)
	require.EqualError(t, err, "store is required")

	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	_, err = store.Unarchive(context.Background(), "ses_invalid")
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")

	_, err = store.Unarchive(context.Background(), testMissingSession)
	require.EqualError(t, err, "session not found")

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	_, err = store.Unarchive(context.Background(), testSessionA)
	require.EqualError(t, err, "session is not archived")

	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{
		Role:      morphmsg.RoleUser,
		Content:   "hello",
		CreatedAt: now,
	}}))
	_, err = store.Archive(context.Background(), testSessionA, base.SessionArchiveRequest{
		ArchivedAt: now,
		ExpiresAt:  now.Add(time.Hour),
	})
	require.NoError(t, err)

	unarchived, err := store.Unarchive(context.Background(), testSessionA)
	require.NoError(t, err)
	require.False(t, unarchived.Archived)
	require.True(t, unarchived.ArchivedAt.IsZero())
	require.True(t, unarchived.ExpiresAt.IsZero())

	session, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.False(t, session.Archived)
	require.True(t, session.ArchivedAt.IsZero())
	require.True(t, session.ExpiresAt.IsZero())

	messages, err := store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{})
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, "hello", messages[0].Content)
}

func TestSQLiteStore_UnarchiveReturnsLookupError(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)
	require.NoError(t, store.db.Migrator().DropTable(&sessionModel{}))

	_, err = store.Unarchive(context.Background(), testSessionA)
	require.Error(t, err)
}

func TestSQLiteStore_UnarchiveReturnsSessionDecodeError(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), Session{
		ID:         testSessionA,
		Archived:   true,
		ArchivedAt: now,
		ExpiresAt:  now.Add(time.Hour),
	}))
	require.NoError(t, store.db.Callback().Query().After("gorm:after_query").Register(
		"test:blank_session_id_for_unarchive",
		func(tx *gorm.DB) {
			record, ok := tx.Statement.Dest.(*sessionModel)
			if ok {
				record.ID = ""
			}
		},
	))

	_, err = store.Unarchive(context.Background(), testSessionA)
	require.EqualError(t, err, "session id is required")
}

func TestSQLiteStore_UnarchiveReturnsSaveError(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), Session{
		ID:         testSessionA,
		Archived:   true,
		ArchivedAt: now,
		ExpiresAt:  now.Add(time.Hour),
	}))
	require.NoError(t, store.db.Exec("CREATE TRIGGER fail_session_update BEFORE UPDATE ON sessions BEGIN SELECT RAISE(FAIL, 'boom'); END;").Error)

	_, err = store.Unarchive(context.Background(), testSessionA)
	require.Error(t, err)
}

func TestSQLiteStore_DecodeToolCallsRejectsInvalidJSON(t *testing.T) {
	require.Nil(t, jsonToToolCalls("{invalid"))
}

func TestSQLiteStore_SaveReturnsTransactionSaveError(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)
	require.NoError(t, store.db.Exec("CREATE TRIGGER fail_session_save BEFORE INSERT ON sessions BEGIN SELECT RAISE(FAIL, 'boom'); END;").Error)

	err = store.Save(context.Background(), Session{ID: testSessionA})
	require.Error(t, err)
}

func TestSQLiteStore_AppendMessagesReturnsLookupErrorWhenSessionsTableIsUnavailable(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)
	require.NoError(t, store.db.Migrator().DropTable(&sessionModel{}))

	err = store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}})
	require.Error(t, err)
}

func TestSQLiteStore_GetMessageReturnsQueryErrors(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)

	t.Run("live query error", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.db.Migrator().DropTable(&messageModel{}))

		_, _, err = store.GetMessage(context.Background(), testSessionA, 0)
		require.Error(t, err)
	})

}

func TestSQLiteStore_ClearMessagesReturnsMissingAndLookupErrors(t *testing.T) {
	t.Run("missing session", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)

		err = store.ClearMessages(context.Background(), testMissingSession)
		require.EqualError(t, err, "session not found")
	})

	t.Run("live lookup error", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.db.Migrator().DropTable(&sessionModel{}))

		err = store.ClearMessages(context.Background(), testSessionA)
		require.Error(t, err)
	})
}

func TestSQLiteStore_SummaryRoundTripAndCleanup(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	now := time.Date(2026, 4, 2, 10, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))

	summary := SessionSummary{
		SessionID:          testSessionA,
		SourceEndOffset:    4,
		SourceMessageCount: 12,
		UpdatedAt:          now,
		SessionSummary:     "Older work",
		CurrentTask:        "Finish phase 3",
		Discoveries:        []string{"one"},
		OpenQuestions:      []string{"two"},
		NextActions:        []string{"three"},
	}
	require.NoError(t, store.SaveSummary(context.Background(), summary))

	loaded, ok, err := store.GetSummary(context.Background(), testSessionA)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, summary.SessionSummary, loaded.SessionSummary)
	require.Equal(t, summary.CurrentTask, loaded.CurrentTask)
	require.Equal(t, summary.Discoveries, loaded.Discoveries)

	require.NoError(t, store.DeleteSummary(context.Background(), testSessionA))
	_, ok, err = store.GetSummary(context.Background(), testSessionA)
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, store.SaveSummary(context.Background(), summary))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: now}}))
	_, err = store.Archive(context.Background(), testSessionA, base.SessionArchiveRequest{
		ArchivedAt: now,
		ExpiresAt:  now.Add(time.Hour),
	})
	require.NoError(t, err)

	_, ok, err = store.GetSummary(context.Background(), testSessionA)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestSQLiteStore_SummaryErrors(t *testing.T) {
	now := time.Date(2026, 4, 2, 10, 0, 0, 0, time.UTC)

	t.Run("save summary validation and write errors", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)

		require.EqualError(t, store.SaveSummary(context.Background(), SessionSummary{}), "session id is required")
		require.EqualError(t, store.SaveSummary(context.Background(), SessionSummary{
			SessionID:      testMissingSession,
			SessionSummary: "summary",
		}), "session not found")

		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.db.Migrator().DropTable(&summaryModel{}))

		err = store.SaveSummary(context.Background(), SessionSummary{
			SessionID:      testSessionA,
			SessionSummary: "summary",
		})
		require.Error(t, err)
	})

	t.Run("save summary session lookup error", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.db.Migrator().DropTable(&sessionModel{}))

		err = store.SaveSummary(context.Background(), SessionSummary{
			SessionID:      testSessionA,
			SessionSummary: "summary",
		})
		require.Error(t, err)
	})

	t.Run("get summary validation and read errors", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)

		summary, ok, err := store.GetSummary(context.Background(), "")
		require.NoError(t, err)
		require.False(t, ok)
		require.Equal(t, SessionSummary{}, summary)

		summary, ok, err = store.GetSummary(context.Background(), "ses_invalid")
		require.EqualError(t, err, "session id must be a valid ses_ nanoid")
		require.False(t, ok)
		require.Equal(t, SessionSummary{}, summary)

		summary, ok, err = store.GetSummary(context.Background(), testMissingSession)
		require.NoError(t, err)
		require.False(t, ok)
		require.Equal(t, SessionSummary{}, summary)

		require.NoError(t, store.db.Migrator().DropTable(&summaryModel{}))
		_, _, err = store.GetSummary(context.Background(), testSessionA)
		require.Error(t, err)
	})

	t.Run("get summary decode error", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.db.Create(&summaryModel{
			SessionID:          testSessionA,
			SourceEndOffset:    3,
			SourceMessageCount: 1,
			UpdatedAt:          now,
			SessionSummary:     "summary",
		}).Error)

		_, _, err = store.GetSummary(context.Background(), testSessionA)
		require.EqualError(t, err, "summary source end offset cannot exceed source message count")
	})

	t.Run("delete summary validation and delete errors", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)

		require.EqualError(t, store.DeleteSummary(context.Background(), ""), "session id is required")
		require.EqualError(t, store.DeleteSummary(context.Background(), "ses_invalid"), "session id must be a valid ses_ nanoid")

		require.NoError(t, store.db.Migrator().DropTable(&summaryModel{}))
		err = store.DeleteSummary(context.Background(), testSessionA)
		require.Error(t, err)
	})
}

func TestSQLiteStore_SummaryDeleteCleanupErrors(t *testing.T) {
	now := time.Date(2026, 4, 2, 10, 0, 0, 0, time.UTC)

	t.Run("delete session summary cleanup error", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.db.Migrator().DropTable(&summaryModel{}))

		err = store.Delete(context.Background(), testSessionA)
		require.Error(t, err)
	})

	t.Run("clear messages summary cleanup error", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.db.Migrator().DropTable(&summaryModel{}))

		err = store.ClearMessages(context.Background(), testSessionA)
		require.Error(t, err)
	})
}

func TestSQLiteStore_StringEncodingHelpers(t *testing.T) {
	require.Equal(t, "", stringsToJSON(nil))
	require.Equal(t, `["one","two"]`, stringsToJSON([]string{"one", "two"}))

	require.Nil(t, jsonToStrings(""))
	require.Nil(t, jsonToStrings("not-json"))
	require.Equal(t, []string{"one", "two"}, jsonToStrings(`["one","two"]`))
}

func TestSQLiteStore_SearchIndexHelpers(t *testing.T) {
	t.Run("ensure search index validates db and surfaces create errors", func(t *testing.T) {
		require.EqualError(t, ensureSearchIndex(nil), "session db is required")
	})

	t.Run("ensure search index and store creation fail on readonly databases", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.db")

		writableDB, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
		require.NoError(t, err)
		require.NoError(t, writableDB.AutoMigrate(
			&sessionModel{},
			&stateModel{},
			&summaryModel{},
			&messageModel{},
		))
		require.NoError(t, ensureMemoryStorage(writableDB))

		readonlyDB, err := gorm.Open(sqlite.Open("file:"+path+"?mode=ro"), &gorm.Config{})
		require.NoError(t, err)

		err = ensureSearchIndex(readonlyDB)
		require.ErrorContains(t, err, "failed to create session message search index")
	})

	t.Run("insert and delete search rows handle no-op and query errors", func(t *testing.T) {
		require.NoError(t, searchRows{{MessageID: 1}}.insert(nil))

		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)

		require.NoError(t, searchRows(nil).insert(store.db))
		require.NoError(t, deleteSearchRows(nil, testSessionA))

		require.NoError(t, store.db.Exec(`DROP TABLE `+sessionMessageSearchTable).Error)

		err = searchRows{{
			MessageID: 1,
			SessionID: testSessionA,
			Role:      "user",
			Body:      "hello",
		}}.insert(store.db)
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to insert session message search row")

		err = deleteSearchRows(store.db, testSessionA)
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to delete session message search rows")
	})

	t.Run("message model search row conversions cover remaining branches", func(t *testing.T) {
		require.Nil(t, messageModels(nil).searchRows())

		rows := messageModels([]messageModel{
			{
				ID:        1,
				SessionID: testSessionA,
				Role:      string(morphmsg.RoleUser),
				Content:   "plain text",
			},
			{
				ID:              2,
				SessionID:       testSessionA,
				Role:            string(morphmsg.RoleTool),
				Name:            "process",
				Content:         "running",
				SemanticContent: "stdout: running",
			},
		}).searchRows()
		require.Len(t, rows, 2)
		require.Equal(t, "plain text", rows[0].SemanticBody)
		require.Equal(t, "stdout: running", rows[1].SemanticBody)

		require.Nil(t, messageModelToSearchRows(messageModel{
			ID:        3,
			SessionID: testSessionA,
			Role:      string(morphmsg.RoleAssistant),
		}))

		require.Nil(t, messageModelToSearchRows(messageModel{
			ID:        4,
			SessionID: testSessionA,
			Role:      string(morphmsg.RoleTool),
			Name:      "process",
		}))

		require.Nil(t, messageModelToSearchRows(messageModel{
			ID:        5,
			SessionID: testSessionA,
			Role:      string(morphmsg.RoleUser),
		}))

		rows = messageModelToSearchRows(messageModel{
			ID:        6,
			SessionID: testSessionA,
			Role:      string(morphmsg.RoleAssistant),
			Content:   "assistant fallback",
		})
		require.Len(t, rows, 1)
		require.Equal(t, "assistant fallback", rows[0].Body)

		rows = messageModelToSearchRows(messageModel{
			ID:        7,
			SessionID: testSessionA,
			Role:      string(morphmsg.RoleAssistant),
			Content:   "search fallback",
		})
		require.Len(t, rows, 1)
		require.Equal(t, "search fallback", rows[0].Body)

		rows = messageModelToSearchRows(messageModel{
			ID:        8,
			SessionID: testSessionA,
			Role:      string(morphmsg.RoleAssistant),
			ToolCalls: `[{}]`,
		})
		require.Empty(t, rows)

		rows = messageModelToSearchRows(messageModel{
			ID:        9,
			SessionID: testSessionA,
			Role:      string(morphmsg.RoleAssistant),
			Content:   "assistant summary",
			ToolCalls: `[{"id":"call-1","name":"process","input":"{\"action\":\"start\"}"}]`,
		})
		require.Len(t, rows, 2)
		require.Equal(t, "assistant summary", rows[0].Body)
		require.Equal(t, "assistant summary", rows[0].SemanticBody)
		require.Equal(t, "process", rows[1].ToolName)
		require.Empty(t, rows[1].SemanticBody)
	})

	t.Run("search tokenization drops punctuation-only segments", func(t *testing.T) {
		require.Nil(t, getSearchTokens("... ---"))
	})
}

func TestSQLiteStore_FTSErrorPathsFromBrokenIndex(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)

	t.Run("append messages returns fts insert errors", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.db.Exec(`DROP TABLE `+sessionMessageSearchTable).Error)

		err = store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
			{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: now},
		})
		require.ErrorContains(t, err, "failed to insert session message search row")
	})

	t.Run("delete returns fts delete errors", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
			{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: now},
		}))
		require.NoError(t, store.db.Exec(`DROP TABLE `+sessionMessageSearchTable).Error)

		err = store.Delete(context.Background(), testSessionA)
		require.ErrorContains(t, err, "failed to delete session message search rows")
	})

	t.Run("clear messages returns fts delete errors", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
			{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: now},
		}))
		require.NoError(t, store.db.Exec(`DROP TABLE `+sessionMessageSearchTable).Error)

		err = store.ClearMessages(context.Background(), testSessionA)
		require.ErrorContains(t, err, "failed to delete session message search rows")
	})
}

func TestSQLiteStore_VectorIndexStateErrors(t *testing.T) {
	t.Run("append rolls back when initial state cannot be persisted", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
		require.NoError(t, store.ConfigureVectorStore(VectorStoreOptions{
			Embedder:       &sqliteTestEmbeddingProvider{},
			VectorStore:    &sqliteTestVectorStore{},
			EmbeddingModel: "text-embedding-test",
		}))
		require.NoError(t, store.db.Migrator().DropTable(&vectorIndexStateModel{}))

		err = store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{
			Role: morphmsg.RoleUser, Content: "hello",
		}})

		require.Error(t, err)
		messages, loadErr := store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{})
		require.NoError(t, loadErr)
		require.Empty(t, messages)
	})

	t.Run("append remains successful when only the final state update fails", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
		provider := &sqliteTestEmbeddingProvider{}
		provider.afterEmbed = func() {
			require.NoError(t, store.db.Migrator().DropTable(&vectorIndexStateModel{}))
		}
		require.NoError(t, store.ConfigureVectorStore(VectorStoreOptions{
			Embedder:       provider,
			VectorStore:    &sqliteTestVectorStore{},
			EmbeddingModel: "text-embedding-test",
		}))

		err = store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{
			Role: morphmsg.RoleUser, Content: "hello",
		}})

		require.NoError(t, err)
		messages, loadErr := store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{})
		require.NoError(t, loadErr)
		require.Len(t, messages, 1)
	})

	for _, operation := range []struct {
		name string
		run  func(*Store) error
	}{
		{name: "delete session", run: func(store *Store) error {
			return store.Delete(context.Background(), testSessionA)
		}},
		{name: "clear messages", run: func(store *Store) error {
			return store.ClearMessages(context.Background(), testSessionA)
		}},
	} {
		t.Run(operation.name+" reports state cleanup errors", func(t *testing.T) {
			store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
			require.NoError(t, err)
			require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
			require.NoError(t, store.db.Migrator().DropTable(&vectorIndexStateModel{}))

			require.Error(t, operation.run(store))
		})
	}
}
