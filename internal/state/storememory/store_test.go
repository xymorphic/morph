package storememory

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	base "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/internal/state/search"
	vectormemory "github.com/xymorphic/morph/internal/state/search/vectorstore/memory"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/nanoid"
)

const DefaultSessionID = base.DefaultSessionID
const SessionIDPrefix = base.SessionIDPrefix

var (
	testSessionA       = nanoid.MustFromSeed(SessionIDPrefix, "project-a", "SessionTestSeedValue123")
	testSessionB       = nanoid.MustFromSeed(SessionIDPrefix, "project-b", "SessionTestSeedValue123")
	testSessionOne     = nanoid.MustFromSeed(SessionIDPrefix, "session-1", "SessionTestSeedValue123")
	testSessionOlder   = nanoid.MustFromSeed(SessionIDPrefix, "older", "SessionTestSeedValue123")
	testSessionNewer   = nanoid.MustFromSeed(SessionIDPrefix, "newer", "SessionTestSeedValue123")
	testSessionAlpha   = nanoid.MustFromSeed(SessionIDPrefix, "alpha", "SessionTestSeedValue123")
	testSessionZeta    = nanoid.MustFromSeed(SessionIDPrefix, "zeta", "SessionTestSeedValue123")
	testMissingSession = nanoid.MustFromSeed(SessionIDPrefix, "missing", "SessionTestSeedValue123")
)

func requireArchiveSession(t *testing.T, store *Store, sessionID string, archivedAt time.Time, expiresAt time.Time) Session {
	t.Helper()

	session, err := store.Archive(context.Background(), sessionID, base.SessionArchiveRequest{
		ArchivedAt: archivedAt,
		ExpiresAt:  expiresAt,
	})
	require.NoError(t, err)

	return session
}

func TestMemoryStore_SaveAndGet(t *testing.T) {
	store := NewStore()
	session := Session{ID: testSessionOne}

	require.Same(t, store, store.Session())
	require.NoError(t, store.Save(context.Background(), session))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionOne, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: time.Now().UTC()},
	}))

	loaded, ok, err := store.Get(context.Background(), testSessionOne, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, testSessionOne, loaded.ID)
	require.False(t, loaded.CreatedAt.IsZero())
	messages, err := store.GetMessages(context.Background(), testSessionOne, MessageQueryOptions{})
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, morphmsg.RoleUser, messages[0].Role)
	message, ok, err := store.GetMessage(context.Background(), testSessionOne, 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "hello", message.Content)
	require.False(t, loaded.UpdatedAt.IsZero())
}

func TestMemoryStore_PatchReasoningEffortOverride(t *testing.T) {
	store := NewStore()
	original := Session{
		ID:                       testSessionA,
		Title:                    "Original",
		EpisodicCheckpointOffset: 7,
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
}

func TestMemoryStore_PatchReasoningEffortOverrideSurvivesArchive(t *testing.T) {
	store := NewStore()
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	effort := "xhigh"
	_, err := store.Patch(context.Background(), testSessionA, SessionPatch{
		ReasoningEffortOverride: &effort,
	})
	require.NoError(t, err)
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{
		Role:    morphmsg.RoleUser,
		Content: "hello",
	}}))

	now := time.Now().UTC()
	archived := requireArchiveSession(t, store, testSessionA, now, now.Add(time.Hour))
	require.Equal(t, effort, archived.ReasoningEffortOverride)
	unarchived, err := store.Unarchive(context.Background(), testSessionA)
	require.NoError(t, err)
	require.Equal(t, effort, unarchived.ReasoningEffortOverride)
}

func TestMemoryStore_PatchReasoningEffortOverrideConcurrentSetters(t *testing.T) {
	store := NewStore()
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))

	values := []string{"low", "medium", "high", "xhigh"}
	start := make(chan struct{})
	errs := make(chan error, len(values))
	var wg sync.WaitGroup
	for _, value := range values {
		value := value
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := store.Patch(context.Background(), testSessionA, SessionPatch{
				ReasoningEffortOverride: &value,
			})
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	final := "none"
	patched, err := store.Patch(context.Background(), testSessionA, SessionPatch{
		ReasoningEffortOverride: &final,
	})
	require.NoError(t, err)
	require.Equal(t, final, patched.ReasoningEffortOverride)
}

func TestMemoryStore_AggregateCapabilities(t *testing.T) {
	store := NewStore()

	sessionStore := store.Session()
	require.Same(t, store, sessionStore)

	automationStore, ok := store.Automation()
	require.True(t, ok)
	require.Same(t, store, automationStore)

	memoryStore, ok := store.Memory()
	require.True(t, ok)
	require.Same(t, store, memoryStore)

	traceStore, ok := store.Trace()
	require.True(t, ok)
	require.Same(t, store, traceStore)

	var nilStore *Store
	require.Nil(t, nilStore.Session())
	automationStore, ok = nilStore.Automation()
	require.False(t, ok)
	require.Nil(t, automationStore)

	memoryStore, ok = nilStore.Memory()
	require.False(t, ok)
	require.Nil(t, memoryStore)

	traceStore, ok = nilStore.Trace()
	require.False(t, ok)
	require.Nil(t, traceStore)
}

func TestMemoryStore_UpdateCheckpointsUpdatesEpisodicOffset(t *testing.T) {
	store := NewStore()
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionOne}))

	offset := 1
	require.EqualError(t, store.UpdateCheckpoints(context.Background(), "ses_invalid", CheckpointPatch{
		EpisodicOffset: &offset,
	}), "session id must be a valid ses_ nanoid")

	offset = 12
	require.NoError(t, store.UpdateCheckpoints(context.Background(), testSessionOne, CheckpointPatch{
		EpisodicOffset: &offset,
	}))
	loaded, ok, err := store.Get(context.Background(), testSessionOne, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 12, loaded.EpisodicCheckpointOffset)

	offset = 4
	require.NoError(t, store.UpdateCheckpoints(context.Background(), testSessionOne, CheckpointPatch{
		EpisodicOffset: &offset,
	}))
	loaded, ok, err = store.Get(context.Background(), testSessionOne, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 12, loaded.EpisodicCheckpointOffset)

	offset = -1
	require.EqualError(t, store.UpdateCheckpoints(context.Background(), testSessionOne, CheckpointPatch{
		EpisodicOffset: &offset,
	}), "episodic checkpoint offset must be greater than or equal to zero")
	offset = 1
	require.EqualError(t, store.UpdateCheckpoints(context.Background(), testMissingSession, CheckpointPatch{
		EpisodicOffset: &offset,
	}), "session not found")
}

func TestMemoryStore_UpdateCheckpointsUpdatesReflectionOffset(t *testing.T) {
	store := NewStore()
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionOne}))

	offset := 12
	require.NoError(t, store.UpdateCheckpoints(context.Background(), testSessionOne, CheckpointPatch{
		ReflectionOffset: &offset,
	}))
	loaded, ok, err := store.Get(context.Background(), testSessionOne, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 12, loaded.ReflectionCheckpointOffset)

	offset = 4
	require.NoError(t, store.UpdateCheckpoints(context.Background(), testSessionOne, CheckpointPatch{
		ReflectionOffset: &offset,
	}))
	loaded, ok, err = store.Get(context.Background(), testSessionOne, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 12, loaded.ReflectionCheckpointOffset)

	offset = -1
	require.EqualError(t, store.UpdateCheckpoints(context.Background(), testSessionOne, CheckpointPatch{
		ReflectionOffset: &offset,
	}), "reflection checkpoint offset must be greater than or equal to zero")
	offset = 1
	require.EqualError(t, store.UpdateCheckpoints(context.Background(), testMissingSession, CheckpointPatch{
		ReflectionOffset: &offset,
	}), "session not found")
}

func TestMemoryStore_GetAndCountMessagesSupportRoleAndNameFilters(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionOne, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionOne, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: now},
		{Role: morphmsg.RoleTool, Name: "plan_tool", Content: "plan-1", ToolCallID: "call-1", CreatedAt: now},
		{Role: morphmsg.RoleTool, Name: "other_tool", Content: "other", ToolCallID: "call-2", CreatedAt: now},
		{Role: morphmsg.RoleTool, Name: "plan_tool", Content: "plan-2", ToolCallID: "call-3", CreatedAt: now},
	}))

	count, err := store.CountMessages(context.Background(), testSessionOne, MessageQueryOptions{
		Role: morphmsg.RoleTool,
		Name: "plan_tool",
	})
	require.NoError(t, err)
	require.Equal(t, 2, count)

	messages, err := store.GetMessages(context.Background(), testSessionOne, MessageQueryOptions{
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

func TestMemoryStore_GetMessagesByIDsReturnsTranscriptOrderedRecords(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionOne, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionOne, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "m1", CreatedAt: now},
		{Role: morphmsg.RoleAssistant, Content: "m2", CreatedAt: now.Add(time.Second)},
		{Role: morphmsg.RoleTool, Name: "process", ToolCallID: "call-1", Content: "m3", CreatedAt: now.Add(2 * time.Second)},
	}))

	records, err := store.GetMessagesByIDs(context.Background(), testSessionOne, []uint{3, 1})
	require.NoError(t, err)
	require.Len(t, records, 2)
	require.Equal(t, []uint{1, 3}, []uint{records[0].Message.ID, records[1].Message.ID})
	require.Equal(t, []int{0, 2}, []int{records[0].Offset, records[1].Offset})
}

func TestMemoryStore_GetMessageWindowReturnsBoundedAnchorContext(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionOne, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionOne, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "m1", CreatedAt: now},
		{Role: morphmsg.RoleAssistant, Content: "m2", CreatedAt: now.Add(time.Second)},
		{Role: morphmsg.RoleTool, Name: "process", ToolCallID: "call-1", Content: "m3", CreatedAt: now.Add(2 * time.Second)},
		{Role: morphmsg.RoleAssistant, Content: "m4", CreatedAt: now.Add(3 * time.Second)},
	}))

	records, err := store.GetMessageWindow(context.Background(), testSessionOne, 3, 1, 1)
	require.NoError(t, err)
	require.Len(t, records, 3)
	require.Equal(t, []int{1, 2, 3}, []int{records[0].Offset, records[1].Offset, records[2].Offset})
	require.Equal(t, []uint{2, 3, 4}, []uint{records[0].Message.ID, records[1].Message.ID, records[2].Message.ID})
}

func TestMemoryStore_SearchMessagesSupportsStructuredAndToolFilters(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionOne, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionOne, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "hello plain text", CreatedAt: now},
		{Role: morphmsg.RoleAssistant, ToolCalls: []morphmsg.ToolCall{
			{ID: "call-1", Name: "process", Input: `{"action":"start"}`},
			{ID: "call-2", Name: "search_files", Input: `{"pattern":"needle"}`},
		}, CreatedAt: now.Add(time.Second)},
		{Role: morphmsg.RoleTool, Name: "process", Content: `{"status":"running"}`, ToolCallID: "call-3", CreatedAt: now.Add(2 * time.Second)},
	}))

	results, err := store.SearchMessages(context.Background(), testSessionOne, base.SearchMessageOptions{
		Query: "needle",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, testSessionOne, results[0].SessionID)
	require.Equal(t, 1, results[0].MatchCount)
	require.Len(t, results[0].Messages, 1)
	require.Equal(t, morphmsg.RoleAssistant, results[0].Messages[0].Message.Role)
	require.Equal(t, morphmsg.ToolCallSearchText(morphmsg.ToolCall{
		ID:    "call-2",
		Name:  "search_files",
		Input: `{"pattern":"needle"}`,
	}), results[0].Messages[0].MatchedText)
	require.Equal(t, "search_files", results[0].Messages[0].MatchedToolName)

	results, err = store.SearchMessages(context.Background(), testSessionOne, base.SearchMessageOptions{
		Query:    "needle",
		ToolName: "process",
	})
	require.NoError(t, err)
	require.Empty(t, results)

	results, err = store.SearchMessages(context.Background(), testSessionOne, base.SearchMessageOptions{
		Query:    "running",
		ToolName: "process",
		Role:     morphmsg.RoleTool,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Len(t, results[0].Messages, 1)
	require.Equal(t, "process", results[0].Messages[0].Message.Name)
	require.Equal(t, "process", results[0].Messages[0].MatchedToolName)
}

func TestMemoryStore_SearchMessagesPrefersAssistantToolHitMetadataOverContent(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionOne, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionOne, []morphmsg.Message{{
		Role:    morphmsg.RoleAssistant,
		Content: "needle summary",
		ToolCalls: []morphmsg.ToolCall{
			{ID: "call-1", Name: "search_files", Input: `{"pattern":"needle"}`},
		},
		CreatedAt: now,
	}}))

	results, err := store.SearchMessages(context.Background(), testSessionOne, base.SearchMessageOptions{
		Query: "needle",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Len(t, results[0].Messages, 1)
	require.Equal(t, "search_files", results[0].Messages[0].MatchedToolName)
	require.Equal(t, morphmsg.ToolCallSearchText(morphmsg.ToolCall{
		ID:    "call-1",
		Name:  "search_files",
		Input: `{"pattern":"needle"}`,
	}), results[0].Messages[0].MatchedText)
}

func TestMemoryStore_SearchMessagesSupportsGroupedCrossSessionScope(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionB, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "origin needle", CreatedAt: now},
	}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionB, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "other needle", CreatedAt: now.Add(time.Second)},
	}))

	results, err := store.SearchMessages(context.Background(), "", base.SearchMessageOptions{
		IgnoreSessionID: testSessionA,
		Query:           "needle",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, testSessionB, results[0].SessionID)
	require.Equal(t, "other needle", results[0].Messages[0].MatchedText)

	results, err = store.SearchMessages(context.Background(), testSessionA, base.SearchMessageOptions{
		Query: "needle",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "origin needle", results[0].Messages[0].MatchedText)

	// search session with ignore session id
	// ignore directive has no effect when session id is provided
	results, err = store.SearchMessages(context.Background(), testSessionA, base.SearchMessageOptions{
		IgnoreSessionID: testSessionA,
		Query:           "needle",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "origin needle", results[0].Messages[0].MatchedText)
}

func TestMemoryStore_SearchMessagesSupportsMaxSessionsAndTieBreaks(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionB, UpdatedAt: now}))

	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "shared needle a", CreatedAt: now},
	}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionB, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "shared needle b", CreatedAt: now},
	}))

	results, err := store.SearchMessages(context.Background(), "", base.SearchMessageOptions{
		Query:       "needle",
		MaxSessions: 1,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, testSessionA, results[0].SessionID)
	require.Equal(t, "shared needle a", results[0].Messages[0].MatchedText)
}

func TestMemoryStore_SearchMessagesCrossSessionOrdersBySessionIDWhenMessageKeysTie(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionB, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{ID: 1, Role: morphmsg.RoleUser, Content: "shared needle a", CreatedAt: now},
	}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionB, []morphmsg.Message{
		{ID: 1, Role: morphmsg.RoleUser, Content: "shared needle b", CreatedAt: now},
	}))

	results, err := store.SearchMessages(context.Background(), "", base.SearchMessageOptions{Query: "needle"})
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, []string{testSessionA, testSessionB}, []string{results[0].SessionID, results[1].SessionID})
}

func TestMemoryStore_SearchMessagesGroupedEdgeCases(t *testing.T) {
	var nilStore *Store

	results, err := nilStore.SearchMessages(context.Background(), testSessionOne, base.SearchMessageOptions{Query: "hello"})
	require.EqualError(t, err, "store is required")
	require.Nil(t, results)

	store := NewStore()

	results, err = store.SearchMessages(context.Background(), "", base.SearchMessageOptions{Query: "hello"})
	require.NoError(t, err)
	require.Nil(t, results)

	results, err = store.SearchMessages(context.Background(), "ses_invalid", base.SearchMessageOptions{Query: "hello"})
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
	require.Nil(t, results)

	results, err = store.SearchMessages(context.Background(), "", base.SearchMessageOptions{
		IgnoreSessionID: "ses_invalid",
		Query:           "hello",
	})
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
	require.Nil(t, results)

	results, err = store.SearchMessages(context.Background(), testSessionOne, base.SearchMessageOptions{})
	require.NoError(t, err)
	require.Nil(t, results)
}

func TestMemoryStore_SearchMessagesSupportsMaxMessagesPerSessionAndRoleFiltering(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionOne, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionOne, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "hello zero", CreatedAt: now},
		{Role: morphmsg.RoleAssistant, Content: "hello one", CreatedAt: now.Add(time.Second)},
		{Role: morphmsg.RoleUser, Content: "hello two", CreatedAt: now.Add(2 * time.Second)},
	}))

	results, err := store.SearchMessages(context.Background(), testSessionOne, base.SearchMessageOptions{
		Query:                 "hello",
		MaxMessagesPerSession: 1,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, 3, results[0].MatchCount)
	require.Equal(t, []string{"hello two"}, []string{
		results[0].Messages[0].MatchedText,
	})

	results, err = store.SearchMessages(context.Background(), testSessionOne, base.SearchMessageOptions{
		Query: "hello",
		Role:  morphmsg.RoleAssistant,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Len(t, results[0].Messages, 1)
	require.Equal(t, morphmsg.RoleAssistant, results[0].Messages[0].Message.Role)

	results, err = store.SearchMessages(context.Background(), testSessionOne, base.SearchMessageOptions{
		Query:    "hello",
		ToolName: "missing",
	})
	require.NoError(t, err)
	require.Empty(t, results)
}

func TestMemoryStore_SearchMessagesSupportsGroupedResultsAndCloning(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionOne, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionOne, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "needle zero", CreatedAt: now},
		{Role: morphmsg.RoleAssistant, Content: "needle one", CreatedAt: now.Add(time.Second)},
		{Role: morphmsg.RoleUser, Content: "needle two", CreatedAt: now.Add(2 * time.Second)},
	}))

	results, err := store.SearchMessages(context.Background(), testSessionOne, base.SearchMessageOptions{
		Query:                 "needle",
		MaxMessagesPerSession: 2,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, testSessionOne, results[0].SessionID)
	require.Equal(t, 3, results[0].MatchCount)
	require.Equal(t, now.Add(2*time.Second), results[0].LastMatchedAt)
	require.Equal(t, []string{"needle two", "needle one"}, []string{
		results[0].Messages[0].MatchedText,
		results[0].Messages[1].MatchedText,
	})

	results[0].Messages[0].MatchedText = "mutated"

	fresh, err := store.SearchMessages(context.Background(), testSessionOne, base.SearchMessageOptions{
		Query:                 "needle",
		MaxMessagesPerSession: 2,
	})
	require.NoError(t, err)
	require.Equal(t, "needle two", fresh[0].Messages[0].MatchedText)
}

func TestMemoryStore_SearchMessagesSupportsCrossSessionScope(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionB, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "needle alpha", CreatedAt: now},
	}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionB, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "needle beta", CreatedAt: now.Add(time.Second)},
	}))

	results, err := store.SearchMessages(context.Background(), "", base.SearchMessageOptions{
		Query:       "needle",
		MaxSessions: 1,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, testSessionB, results[0].SessionID)

	results, err = store.SearchMessages(context.Background(), "", base.SearchMessageOptions{
		IgnoreSessionID: testSessionA,
		Query:           "needle",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, testSessionB, results[0].SessionID)
}

func TestMemoryStore_SearchMessagesOrdersBySessionIDWhenLastMatchedAtTies(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionB, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "needle alpha", CreatedAt: now},
	}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionB, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "needle beta", CreatedAt: now},
	}))

	results, err := store.SearchMessages(context.Background(), "", base.SearchMessageOptions{Query: "needle"})
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, []string{testSessionA, testSessionB}, []string{
		results[0].SessionID,
		results[1].SessionID,
	})
}

func TestMemoryStore_SearchMessagesEdgeCases(t *testing.T) {
	var nilStore *Store

	results, err := nilStore.SearchMessages(context.Background(), testSessionOne, base.SearchMessageOptions{Query: "hello"})
	require.EqualError(t, err, "store is required")
	require.Nil(t, results)

	store := NewStore()

	results, err = store.SearchMessages(context.Background(), "", base.SearchMessageOptions{Query: "hello"})
	require.NoError(t, err)
	require.Nil(t, results)

	results, err = store.SearchMessages(context.Background(), "ses_invalid", base.SearchMessageOptions{Query: "hello"})
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
	require.Nil(t, results)

	results, err = store.SearchMessages(context.Background(), "", base.SearchMessageOptions{
		IgnoreSessionID: "ses_invalid",
		Query:           "hello",
	})
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
	require.Nil(t, results)

	results, err = store.SearchMessages(context.Background(), testSessionOne, base.SearchMessageOptions{})
	require.NoError(t, err)
	require.Nil(t, results)

	results, err = store.SearchMessages(context.Background(), testSessionOne, base.SearchMessageOptions{Query: "missing"})
	require.NoError(t, err)
	require.Nil(t, results)
}

func TestMemoryStore_SearchMessagesOrdersSessionMessagesByTimestampAndID(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionOne, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionOne, []morphmsg.Message{
		{ID: 1, Role: morphmsg.RoleUser, Content: "needle one", CreatedAt: now},
		{ID: 2, Role: morphmsg.RoleUser, Content: "needle two", CreatedAt: now},
	}))

	results, err := store.SearchMessages(context.Background(), testSessionOne, base.SearchMessageOptions{
		Query:                 "needle",
		MaxMessagesPerSession: 2,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, []uint{2, 1}, []uint{
		results[0].Messages[0].Message.ID,
		results[0].Messages[1].Message.ID,
	})
}

func TestMemoryStore_VectorSearchReturnsSemanticHits(t *testing.T) {
	store := newVectorMemoryStore(t, nil)
	now := time.Now().UTC()

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "The retention playbook focuses on renewal risk scoring.", CreatedAt: now},
	}))

	results, err := store.SearchMessages(context.Background(), testSessionA, base.SearchMessageOptions{
		Query: "customer cancellation prevention strategy",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, testSessionA, results[0].SessionID)
	require.Equal(t, "The retention playbook focuses on renewal risk scoring.", results[0].Messages[0].MatchedText)
}

func TestMemoryStore_VectorSearchMergesLexicalAndVectorCandidates(t *testing.T) {
	enabled := false
	store := newVectorMemoryStore(t, &enabled)
	now := time.Now().UTC()

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "needle plus retention context", CreatedAt: now},
	}))

	results, err := store.SearchMessages(context.Background(), testSessionA, base.SearchMessageOptions{
		Query: "needle",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, 1, results[0].MatchCount)
	require.Len(t, results[0].Messages, 1)
	require.Equal(t, "needle plus retention context", results[0].Messages[0].MatchedText)
}

func TestMemoryStore_VectorSearchAppliesScopeRoleAndToolFilters(t *testing.T) {
	store := newVectorMemoryStore(t, nil)
	now := time.Now().UTC()

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionB, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "The retention playbook is user-visible.", CreatedAt: now},
		{
			Role:            morphmsg.RoleTool,
			Name:            "session_search",
			Content:         "The retention playbook came from tool output.",
			SemanticContent: "The retention playbook came from tool output.",
			CreatedAt:       now.Add(time.Second),
		},
	}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionB, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "The retention playbook is in another session.", CreatedAt: now.Add(2 * time.Second)},
	}))

	results, err := store.SearchMessages(context.Background(), "", base.SearchMessageOptions{
		IgnoreSessionID: testSessionB,
		Query:           "customer cancellation prevention strategy",
		Role:            morphmsg.RoleTool,
		ToolName:        "session_search",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, testSessionA, results[0].SessionID)
	require.Len(t, results[0].Messages, 1)
	require.Equal(t, morphmsg.RoleTool, results[0].Messages[0].Message.Role)
	require.Equal(t, "session_search", results[0].Messages[0].MatchedToolName)
}

func TestMemoryStore_VectorLifecycleRemovesRows(t *testing.T) {
	t.Run("delete", func(t *testing.T) {
		store := newVectorMemoryStore(t, nil)
		now := time.Now().UTC()

		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
			{Role: morphmsg.RoleUser, Content: "The retention playbook should disappear.", CreatedAt: now},
		}))
		require.NoError(t, store.Delete(context.Background(), testSessionA))

		results, err := store.SearchMessages(context.Background(), "", base.SearchMessageOptions{
			Query: "customer cancellation prevention strategy",
		})
		require.NoError(t, err)
		require.Empty(t, results)
	})

	t.Run("clear", func(t *testing.T) {
		store := newVectorMemoryStore(t, nil)
		now := time.Now().UTC()

		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
			{Role: morphmsg.RoleUser, Content: "The retention playbook should be cleared.", CreatedAt: now},
		}))
		require.NoError(t, store.ClearMessages(context.Background(), testSessionA))

		results, err := store.SearchMessages(context.Background(), testSessionA, base.SearchMessageOptions{
			Query: "customer cancellation prevention strategy",
		})
		require.NoError(t, err)
		require.Empty(t, results)
	})

	t.Run("archive preserves vectors", func(t *testing.T) {
		store := newVectorMemoryStore(t, nil)
		now := time.Now().UTC()

		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
			{Role: morphmsg.RoleUser, Content: "The retention playbook should be archived.", CreatedAt: now},
		}))
		requireArchiveSession(t, store, testSessionA, now, now.Add(time.Hour))

		results, err := store.SearchMessages(context.Background(), "", base.SearchMessageOptions{
			Query: "customer cancellation prevention strategy",
		})
		require.NoError(t, err)
		require.Len(t, results, 1)
		require.Equal(t, testSessionA, results[0].SessionID)
	})

	t.Run("expired archive cleanup removes vectors", func(t *testing.T) {
		store := newVectorMemoryStore(t, nil)
		now := time.Now().UTC()

		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
			{Role: morphmsg.RoleUser, Content: "The retention playbook should expire.", CreatedAt: now},
		}))
		requireArchiveSession(t, store, testSessionA, now.Add(-2*time.Hour), now.Add(-time.Hour))
		require.NoError(t, store.DeleteExpiredArchives(context.Background(), now))

		results, err := store.SearchMessages(context.Background(), "", base.SearchMessageOptions{
			Query: "customer cancellation prevention strategy",
		})
		require.NoError(t, err)
		require.Empty(t, results)
	})
}

func TestMemoryStore_VectorSearchUsesConfiguredReranker(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()
	require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
		Embedder:            semanticTestEmbedder{},
		Reranker:            preferBillingTestReranker{},
		VectorStore:         vectormemory.NewStore(),
		EmbeddingModel:      "semantic-test",
		RerankMaxCandidates: 100,
		Required:            true,
	}))

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "The retention playbook is related.", CreatedAt: now},
		{Role: morphmsg.RoleUser, Content: "Billing invoice notes are unrelated.", CreatedAt: now.Add(time.Second)},
	}))

	results, err := store.SearchMessages(context.Background(), testSessionA, base.SearchMessageOptions{
		Query: "customer cancellation prevention strategy",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Len(t, results[0].Messages, 2)
	require.Equal(t, "Billing invoice notes are unrelated.", results[0].Messages[0].MatchedText)
}

func TestMatchedMessageHit_EdgeCases(t *testing.T) {
	_, ok := getMatchedMessageHit(testSessionOne, morphmsg.Message{
		Role:    morphmsg.RoleAssistant,
		Content: "",
	}, "needle", base.SearchMessageOptions{})
	require.False(t, ok)

	_, ok = getMatchedMessageHit(testSessionOne, morphmsg.Message{
		Role: morphmsg.RoleAssistant,
		ToolCalls: []morphmsg.ToolCall{
			{ID: "call-1", Name: "search_files", Input: `{"pattern":"needle"}`},
		},
	}, "needle", base.SearchMessageOptions{ToolName: "process"})
	require.False(t, ok)

	_, ok = getMatchedMessageHit(testSessionOne, morphmsg.Message{
		Role:    morphmsg.RoleTool,
		Name:    "process",
		Content: `{"status":"running"}`,
	}, "running", base.SearchMessageOptions{ToolName: "search_files"})
	require.False(t, ok)
}

func TestCloneSearchMessageHits_Empty(t *testing.T) {
	require.Nil(t, cloneSearchMessageHits(nil))
}

func newVectorMemoryStore(t *testing.T, enableRerank *bool) *Store {
	t.Helper()

	store := NewStore()
	require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
		Embedder:            semanticTestEmbedder{},
		Reranker:            search.DeterministicReranker{},
		VectorStore:         vectormemory.NewStore(),
		EnableRerank:        enableRerank,
		EmbeddingModel:      "semantic-test",
		RerankMaxCandidates: 100,
		Required:            true,
	}))

	return store
}

type semanticTestEmbedder struct{}

func (semanticTestEmbedder) Embed(
	_ context.Context,
	req search.EmbeddingRequest,
) (search.EmbeddingResult, error) {
	items := make([]search.Embedding, 0, len(req.Inputs))
	for _, input := range req.Inputs {
		vector := []float64{0.1, 0.9}
		text := strings.ToLower(input.Text)
		if strings.Contains(text, "retention") ||
			strings.Contains(text, "renewal") ||
			strings.Contains(text, "cancellation") ||
			strings.Contains(text, "prevention") {
			vector = []float64{1, 0}
		}
		items = append(items, search.Embedding{
			ID:          input.ID,
			ContentHash: search.VectorContentHash(input.Text),
			Vector:      vector,
		})
	}

	return search.EmbeddingResult{
		Model:      req.Model,
		Items:      items,
		Dimensions: 2,
	}, nil
}

type preferBillingTestReranker struct{}

func (preferBillingTestReranker) Name() string {
	return search.RerankerLLM
}

func (preferBillingTestReranker) Rerank(
	_ context.Context,
	req search.RerankRequest,
) (search.RerankResult, error) {
	var preferred []search.RerankItem
	var remaining []search.RerankItem
	for _, candidate := range req.Candidates {
		item := search.RerankItem{CandidateID: candidate.ID, Score: 0.1}
		if strings.Contains(strings.ToLower(candidate.Text), "billing") {
			item.Score = 1
			preferred = append(preferred, item)
			continue
		}
		remaining = append(remaining, item)
	}

	items := append(preferred, remaining...)
	return search.RerankResult{Reranker: search.RerankerLLM, Items: items}, nil
}

func TestMemoryStore_GetMessagesSupportsDescendingOrder(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionOne, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionOne, []morphmsg.Message{
		{Role: morphmsg.RoleTool, Name: "plan_tool", Content: "plan-1", ToolCallID: "call-1", CreatedAt: now},
		{Role: morphmsg.RoleTool, Name: "plan_tool", Content: "plan-2", ToolCallID: "call-2", CreatedAt: now},
	}))

	messages, err := store.GetMessages(context.Background(), testSessionOne, MessageQueryOptions{
		Role:  morphmsg.RoleTool,
		Name:  "plan_tool",
		Order: "desc",
		Limit: 1,
	})
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, "plan-2", messages[0].Content)
}

func TestMemoryStore_GetReturnsFalseWhenMissing(t *testing.T) {
	store := NewStore()

	loaded, ok, err := store.Get(context.Background(), testMissingSession, base.SessionGetOptions{})

	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, Session{}, loaded)
}

func TestMemoryStore_ListOrdersNewestFirst(t *testing.T) {
	store := NewStore()
	older := time.Now().UTC().Add(-time.Minute)
	newer := time.Now().UTC()

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionOlder, UpdatedAt: older}))
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionNewer, UpdatedAt: newer}))

	sessions, err := store.List(context.Background(), base.SessionListOptions{})

	require.NoError(t, err)
	require.Len(t, sessions, 2)
	require.Equal(t, testSessionNewer, sessions[0].ID)
	require.Equal(t, testSessionOlder, sessions[1].ID)
}

func TestMemoryStore_ListOrdersByIDWhenTimesMatch(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionZeta, UpdatedAt: now}))
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionAlpha, UpdatedAt: now}))

	sessions, err := store.List(context.Background(), base.SessionListOptions{})

	require.NoError(t, err)
	require.Len(t, sessions, 2)
	require.Equal(t, testSessionAlpha, sessions[0].ID)
	require.Equal(t, testSessionZeta, sessions[1].ID)
}

func TestMemoryStore_GetAndListFilterByArchiveState(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	archived := true
	live := false

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "archived", CreatedAt: now},
	}))
	requireArchiveSession(t, store, testSessionA, now, now.Add(time.Hour))
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

func TestMemoryStore_Delete(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()

	require.EqualError(t, store.Delete(context.Background(), ""), "session id is required")
	require.EqualError(t, store.Delete(context.Background(), DefaultSessionID), "default session cannot be deleted")
	require.EqualError(t, store.Delete(context.Background(), testMissingSession), "session not found")

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: now},
	}))
	require.NoError(t, store.SetCurrent(context.Background(), testSessionA))
	require.NoError(t, store.Delete(context.Background(), testSessionA))

	loaded, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, Session{}, loaded)
	messages, err := store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{})
	require.NoError(t, err)
	require.Nil(t, messages)
	current, ok, err := store.Current(context.Background())
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, current)
}

func TestMemoryStore_SaveRejectsMissingID(t *testing.T) {
	store := NewStore()

	require.EqualError(t, store.Save(context.Background(), Session{}), "session id is required")
}

func TestMemoryStore_SaveRejectsInvalidSessionID(t *testing.T) {
	store := NewStore()

	require.EqualError(t, store.Save(context.Background(), Session{ID: "ses_invalid"}), "session id must be a valid ses_ nanoid")
}

func TestMemoryStore_SavePersistsSessionTitle(t *testing.T) {
	store := NewStore()

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

func TestMemoryStore_SavePersistsSessionOrigin(t *testing.T) {
	store := NewStore()
	origin := base.SessionOrigin{
		Source:         base.SessionOriginSourceSlack,
		AccountID:      "T123",
		ConversationID: "C456",
		ThreadID:       "1717618842.000100",
	}

	require.NoError(t, store.Save(context.Background(), Session{
		ID:     testSessionA,
		Origin: origin,
	}))

	session, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, origin, session.Origin)

	sessions, err := store.List(context.Background(), base.SessionListOptions{})
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	require.Equal(t, origin, sessions[0].Origin)

	sessions, err = store.List(context.Background(), base.SessionListOptions{
		OriginSource: base.SessionOriginSourceSlack,
	})
	require.NoError(t, err)
	require.Len(t, sessions, 1)

	sessions, err = store.List(context.Background(), base.SessionListOptions{
		OriginSource: base.SessionOriginSourceAutomation,
	})
	require.NoError(t, err)
	require.Empty(t, sessions)

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	session, ok, err = store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, origin, session.Origin)
}

func TestMemoryStore_RenameUpdatesSessionTitle(t *testing.T) {
	store := NewStore()
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

func TestMemoryStore_RenameValidatesInput(t *testing.T) {
	store := NewStore()

	_, err := (*Store)(nil).Rename(context.Background(), base.SessionRenameRequest{SessionID: testSessionA, Title: "Title"})
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

func TestMemoryStore_SavePreservesAndNormalizesArchiveMetadata(t *testing.T) {
	store := NewStore()
	location := time.FixedZone("UTC+2", 2*60*60)
	archivedAt := time.Date(2026, 4, 3, 12, 0, 0, 0, location)
	expiresAt := time.Date(2026, 4, 4, 12, 0, 0, 0, location)

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

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	session, ok, err = store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, session.Archived)
	require.Equal(t, archivedAt.UTC(), session.ArchivedAt)
	require.Equal(t, expiresAt.UTC(), session.ExpiresAt)
}

func TestMemoryStore_NilReceiverErrors(t *testing.T) {
	var store *Store

	require.EqualError(t, store.Save(context.Background(), Session{ID: "session-1"}), "store is required")
	require.EqualError(t, store.UpdateCheckpoints(context.Background(), "session-1", CheckpointPatch{}), "store is required")
	require.EqualError(t, store.Delete(context.Background(), "session-1"), "store is required")
	require.EqualError(t, store.AppendMessages(context.Background(), "session-1", []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}}), "store is required")

	loaded, ok, err := store.Get(context.Background(), "session-1", base.SessionGetOptions{})
	require.EqualError(t, err, "store is required")
	require.False(t, ok)
	require.Equal(t, Session{}, loaded)

	listed, err := store.List(context.Background(), base.SessionListOptions{})
	require.EqualError(t, err, "store is required")
	require.Nil(t, listed)

	session, err := store.Archive(context.Background(), DefaultSessionID, base.SessionArchiveRequest{ExpiresAt: time.Now().UTC()})
	require.EqualError(t, err, "store is required")
	require.Equal(t, Session{}, session)
	session, err = store.Unarchive(context.Background(), DefaultSessionID)
	require.EqualError(t, err, "store is required")
	require.Equal(t, Session{}, session)
	require.EqualError(t, store.DeleteExpiredArchives(context.Background(), time.Now().UTC()), "store is required")
	messages, err := store.GetMessages(context.Background(), DefaultSessionID, MessageQueryOptions{})
	require.EqualError(t, err, "store is required")
	require.Nil(t, messages)
	message, ok, err := store.GetMessage(context.Background(), DefaultSessionID, 0)
	require.EqualError(t, err, "store is required")
	require.False(t, ok)
	require.Equal(t, morphmsg.Message{}, message)
	count, err := store.CountMessages(context.Background(), DefaultSessionID, MessageQueryOptions{})
	require.EqualError(t, err, "store is required")
	require.Zero(t, count)

	require.EqualError(t, store.DeleteExpiredArchives(context.Background(), time.Now().UTC()), "store is required")
	require.EqualError(t, store.ClearMessages(context.Background(), DefaultSessionID), "store is required")
	require.EqualError(t, store.SetCurrent(context.Background(), DefaultSessionID), "store is required")
	require.EqualError(t, store.ClearCurrent(context.Background()), "store is required")

	current, ok, err := store.Current(context.Background())
	require.EqualError(t, err, "store is required")
	require.False(t, ok)
	require.Empty(t, current)
}

func TestMemoryStore_ArchiveLifecycleAndFiltering(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)

	require.NoError(t, store.Save(context.Background(), Session{ID: DefaultSessionID}))
	require.NoError(t, store.AppendMessages(context.Background(), DefaultSessionID, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "old", CreatedAt: now.Add(-2 * time.Hour)},
	}))

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleAssistant, Content: "new", CreatedAt: now},
	}))
	require.NoError(t, store.SetCurrent(context.Background(), testSessionA))

	requireArchiveSession(t, store, testSessionA, now, now.Add(time.Hour))

	session, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, session.Archived)
	require.Equal(t, now, session.ArchivedAt)
	require.Equal(t, now.Add(time.Hour), session.ExpiresAt)

	messages, err := store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{})
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, "new", messages[0].Content)

	message, ok, err := store.GetMessage(context.Background(), testSessionA, 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "new", message.Content)

	message, ok, err = store.GetMessage(context.Background(), testSessionA, 1)
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, morphmsg.Message{}, message)

	defaultSession, ok, err := store.Get(context.Background(), DefaultSessionID, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, DefaultSessionID, defaultSession.ID)

	defaultMessages, err := store.GetMessages(context.Background(), DefaultSessionID, MessageQueryOptions{})
	require.NoError(t, err)
	require.Len(t, defaultMessages, 1)
	require.Equal(t, "old", defaultMessages[0].Content)

	liveMessages, err := store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{})
	require.NoError(t, err)
	require.Len(t, liveMessages, 1)

	current, ok, err := store.Current(context.Background())
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, current)

	require.NoError(t, store.DeleteExpiredArchives(context.Background(), now))

	session, ok, err = store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, session.Archived)
}

func TestMemoryStore_UnarchiveRestoresSessionVisibility(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	summary := SessionSummary{
		SessionID:          testSessionA,
		SessionSummary:     "Summary",
		SourceEndOffset:    1,
		SourceMessageCount: 1,
		UpdatedAt:          now,
	}

	require.NoError(t, store.Save(context.Background(), Session{
		ID:                         testSessionA,
		Compaction:                 base.SessionCompaction{Status: base.CompactionStatusSucceeded, TargetOffset: 1},
		EpisodicCheckpointOffset:   2,
		ReflectionCheckpointOffset: 3,
		Title:                      "Project",
		TitleSource:                base.SessionTitleSourceManual,
		UpdatedAt:                  now,
	}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: now},
	}))
	require.NoError(t, store.SaveSummary(context.Background(), summary))
	requireArchiveSession(t, store, testSessionA, now, now.Add(time.Hour))

	sessions, err := store.List(context.Background(), base.SessionListOptions{})
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	require.True(t, sessions[0].Archived)

	session, err := store.Unarchive(context.Background(), testSessionA)
	require.NoError(t, err)
	require.False(t, session.Archived)
	require.True(t, session.ArchivedAt.IsZero())
	require.True(t, session.ExpiresAt.IsZero())
	require.Equal(t, "Project", session.Title)
	require.Equal(t, base.SessionCompaction{Status: base.CompactionStatusSucceeded, TargetOffset: 1}, session.Compaction)
	require.Equal(t, 2, session.EpisodicCheckpointOffset)
	require.Equal(t, 3, session.ReflectionCheckpointOffset)

	sessions, err = store.List(context.Background(), base.SessionListOptions{})
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	require.Equal(t, testSessionA, sessions[0].ID)

	messages, err := store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{})
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, "hello", messages[0].Content)

	loadedSummary, ok, err := store.GetSummary(context.Background(), testSessionA)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, summary.SessionSummary, loadedSummary.SessionSummary)
}

func TestMemoryStore_UnarchiveValidation(t *testing.T) {
	store := NewStore()

	_, err := store.Unarchive(context.Background(), "")
	require.EqualError(t, err, "session id is required")

	_, err = store.Unarchive(context.Background(), "ses_invalid")
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")

	_, err = store.Unarchive(context.Background(), testMissingSession)
	require.EqualError(t, err, "session not found")

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	_, err = store.Unarchive(context.Background(), testSessionA)
	require.EqualError(t, err, "session is not archived")

	var nilStore *Store
	_, err = nilStore.Unarchive(context.Background(), testSessionA)
	require.EqualError(t, err, "store is required")
}

func TestMemoryStore_DeleteExpiredArchivesDeletesExpiredArchivedSessions(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "preserved", CreatedAt: now},
	}))
	requireArchiveSession(t, store, testSessionA, now.Add(-2*time.Hour), now.Add(-time.Hour))
	store.currentSession = testSessionA

	require.NoError(t, store.DeleteExpiredArchives(context.Background(), now))

	session, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, Session{}, session)

	sessions, err := store.List(context.Background(), base.SessionListOptions{})
	require.NoError(t, err)
	require.Empty(t, sessions)
	current, ok, err := store.Current(context.Background())
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, current)

	messages, err := store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{})
	require.NoError(t, err)
	require.Nil(t, messages)

	require.NoError(t, store.DeleteExpiredArchives(context.Background(), now))
	session, ok, err = store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, Session{}, session)
}

func TestMemoryStore_SetCurrentAndCloneMessages(t *testing.T) {
	store := NewStore()
	message := morphmsg.Message{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)}
	session := Session{ID: testSessionA}

	require.NoError(t, store.Save(context.Background(), session))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{message}))
	message.Content = "mutated-after-save"

	loaded, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	createdAt := loaded.CreatedAt
	messages, err := store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{})
	require.NoError(t, err)
	require.Equal(t, "hello", messages[0].Content)

	require.EqualError(t, store.SetCurrent(context.Background(), ""), "session id is required")
	require.EqualError(t, store.SetCurrent(context.Background(), testMissingSession), "session not found")
	require.NoError(t, store.SetCurrent(context.Background(), testSessionA))

	current, ok, err := store.Current(context.Background())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, testSessionA, current)

	require.NoError(t, store.ClearCurrent(context.Background()))
	current, ok, err = store.Current(context.Background())
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, current)
	require.NoError(t, store.SetCurrent(context.Background(), testSessionA))

	requireArchiveSession(t, store, testSessionA, time.Now().UTC(), time.Now().UTC().Add(time.Hour))
	require.EqualError(t, store.SetCurrent(context.Background(), testSessionA), "session not found")

	current, ok, err = store.Current(context.Background())
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, current)

	messages[0].Content = "mutated-after-get"
	loadedAgain, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, createdAt, loadedAgain.CreatedAt)
	messagesAgain, err := store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{})
	require.NoError(t, err)
	require.Equal(t, "hello", messagesAgain[0].Content)
	messageAgain, ok, err := store.GetMessage(context.Background(), testSessionA, 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "hello", messageAgain.Content)
}

func TestMemoryStore_HelperFunctions(t *testing.T) {
	require.Nil(t, reverseMessages(nil))
	require.Equal(t, base.MessageOrderAsc, getMessageQueryOrder(MessageQueryOptions{}))
	require.Equal(t, base.MessageOrderAsc, getMessageQueryOrder(MessageQueryOptions{Order: "bogus"}))

	searchText, toolName := morphmsg.SearchableMessageText(morphmsg.Message{
		Role:    morphmsg.RoleAssistant,
		Content: "assistant plain text",
	}, "")
	require.Equal(t, "assistant plain text", searchText)
	require.Empty(t, toolName)

	searchText, toolName = morphmsg.SearchableMessageText(morphmsg.Message{
		Role:    morphmsg.RoleAssistant,
		Content: "assistant plain text",
	}, "process")
	require.Empty(t, searchText)
	require.Empty(t, toolName)

	searchText, toolName = morphmsg.SearchableMessageText(morphmsg.Message{
		Role:      morphmsg.RoleAssistant,
		ToolCalls: []morphmsg.ToolCall{{ID: "call-1", Name: "process", Input: `{"action":"start"}`}},
	}, "process")
	require.Contains(t, searchText, "tool_name process")
	require.Equal(t, "process", toolName)

	searchText, toolName = morphmsg.SearchableMessageText(morphmsg.Message{
		Role:      morphmsg.RoleAssistant,
		ToolCalls: []morphmsg.ToolCall{{ID: "call-1", Name: "process", Input: `{"action":"start"}`}},
	}, "search_files")
	require.Empty(t, searchText)
	require.Empty(t, toolName)

	searchText, toolName = morphmsg.SearchableMessageText(morphmsg.Message{
		Role:      morphmsg.RoleAssistant,
		ToolCalls: []morphmsg.ToolCall{{ID: "call-1", Name: "process", Input: `{"action":"start"}`}},
	}, "")
	require.Contains(t, searchText, "tool_name process")
	require.Equal(t, "process", toolName)

	searchText, toolName = morphmsg.SearchableMessageText(morphmsg.Message{
		Role:    morphmsg.RoleTool,
		Name:    "process",
		Content: `{"status":"running"}`,
	}, "process")
	require.Contains(t, searchText, "status running")
	require.Equal(t, "process", toolName)

	searchText, toolName = morphmsg.SearchableMessageText(morphmsg.Message{
		Role:    morphmsg.RoleTool,
		Name:    "process",
		Content: `{"status":"running"}`,
	}, "")
	require.Contains(t, searchText, "status running")
	require.Equal(t, "process", toolName)

	searchText, toolName = morphmsg.SearchableMessageText(morphmsg.Message{
		Role:    morphmsg.RoleTool,
		Name:    "process",
		Content: `{"status":"running"}`,
	}, "search_files")
	require.Empty(t, searchText)
	require.Empty(t, toolName)

	searchText, toolName = morphmsg.SearchableMessageText(morphmsg.Message{
		Role:    morphmsg.RoleUser,
		Content: "user plain text",
	}, "")
	require.Equal(t, "user plain text", searchText)
	require.Empty(t, toolName)

	searchText, toolName = morphmsg.SearchableMessageText(morphmsg.Message{
		Role:    morphmsg.RoleUser,
		Content: "user plain text",
	}, "process")
	require.Empty(t, searchText)
	require.Empty(t, toolName)
}

func TestMemoryStore_ArchiveValidation(t *testing.T) {
	store := NewStore()
	expiresAt := time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)

	_, err := store.Archive(context.Background(), "", base.SessionArchiveRequest{})
	require.EqualError(t, err, "session id is required")
	_, err = store.Archive(context.Background(), "project-a", base.SessionArchiveRequest{})
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")

	_, err = store.Archive(context.Background(), DefaultSessionID, base.SessionArchiveRequest{
		ArchivedAt: time.Date(2026, 3, 31, 11, 0, 0, 0, time.UTC),
		ExpiresAt:  expiresAt,
	})
	require.EqualError(t, err, "session not found")

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	_, err = store.Archive(context.Background(), testSessionA, base.SessionArchiveRequest{
		ArchivedAt: time.Date(2026, 3, 31, 11, 0, 0, 0, time.UTC),
		ExpiresAt:  expiresAt,
	})
	require.EqualError(t, err, "source session has no messages")
}

func TestMemoryStore_SessionArchiveLifecycle(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	expiresAt := now.Add(time.Hour)

	require.NoError(t, store.Save(context.Background(), Session{
		ID:          testSessionA,
		Title:       "Notes",
		TitleSource: base.SessionTitleSourceManual,
		UpdatedAt:   now,
	}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "preserve me", CreatedAt: now},
	}))

	archived, err := store.Archive(context.Background(), "  "+testSessionA+"  ", base.SessionArchiveRequest{
		ArchivedAt: now,
		ExpiresAt:  expiresAt,
	})
	require.NoError(t, err)
	require.True(t, archived.Archived)
	require.Equal(t, testSessionA, archived.ID)
	require.Equal(t, now, archived.ArchivedAt)
	require.Equal(t, expiresAt, archived.ExpiresAt)

	_, ok, err := store.Get(context.Background(), testSessionB, base.SessionGetOptions{})
	require.NoError(t, err)
	require.False(t, ok)
	loaded, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, loaded.Archived)
	require.Equal(t, "Notes", loaded.Title)
	require.Equal(t, base.SessionTitleSourceManual, loaded.TitleSource)

	listed, err := store.List(context.Background(), base.SessionListOptions{})
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, testSessionA, listed[0].ID)
	require.True(t, listed[0].Archived)

	messages, err := store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{})
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, "preserve me", messages[0].Content)
	count, err := store.CountMessages(context.Background(), testSessionA, MessageQueryOptions{})
	require.NoError(t, err)
	require.Equal(t, 1, count)
	message, ok, err := store.GetMessage(context.Background(), testSessionA, 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "preserve me", message.Content)

	unarchived, err := store.Unarchive(context.Background(), testSessionA)
	require.NoError(t, err)
	require.False(t, unarchived.Archived)
	loaded, ok, err = store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.False(t, loaded.Archived)
}

func TestMemoryStore_MessageEdgeCases(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)

	require.EqualError(t, store.AppendMessages(context.Background(), "", []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: now}}), "session id is required")
	require.NoError(t, store.AppendMessages(context.Background(), testMissingSession, nil))
	require.EqualError(t, store.AppendMessages(context.Background(), testMissingSession, []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: now}}), "session not found")

	messages, err := store.GetMessages(context.Background(), "", MessageQueryOptions{})
	require.NoError(t, err)
	require.Nil(t, messages)

	message, ok, err := store.GetMessage(context.Background(), "", 0)
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, morphmsg.Message{}, message)

	message, ok, err = store.GetMessage(context.Background(), testMissingSession, -1)
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, morphmsg.Message{}, message)

	require.EqualError(t, store.ClearMessages(context.Background(), ""), "session id is required")
	require.EqualError(t, store.ClearMessages(context.Background(), testMissingSession), "session not found")
	require.EqualError(t, store.ClearMessages(context.Background(), testSessionA), "session not found")

	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: now}}))
	requireArchiveSession(t, store, testSessionA, now, now.Add(time.Hour))
	require.NoError(t, store.ClearMessages(context.Background(), testSessionA))

	archivedMessages, err := store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{})
	require.NoError(t, err)
	require.Nil(t, archivedMessages)
}

func TestMemoryStore_MessageQueryEdgeCases(t *testing.T) {
	var nilStore *Store

	records, err := nilStore.GetMessagesByIDs(context.Background(), testSessionA, []uint{1})
	require.EqualError(t, err, "store is required")
	require.Nil(t, records)

	window, err := nilStore.GetMessageWindow(context.Background(), testSessionA, 1, 0, 0)
	require.EqualError(t, err, "store is required")
	require.Nil(t, window)

	store := NewStore()
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{
		ID:                         testSessionA,
		EpisodicCheckpointOffset:   5,
		ReflectionCheckpointOffset: 7,
		UpdatedAt:                  now,
	}))

	lowerEpisodic := 4
	lowerReflection := 6
	require.NoError(t, store.UpdateCheckpoints(context.Background(), testSessionA, CheckpointPatch{
		EpisodicOffset:   &lowerEpisodic,
		ReflectionOffset: &lowerReflection,
	}))
	session, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 5, session.EpisodicCheckpointOffset)
	require.Equal(t, 7, session.ReflectionCheckpointOffset)

	messages, err := store.GetMessages(context.Background(), "ses_invalid", MessageQueryOptions{})
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
	require.Nil(t, messages)

	messages, err = store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{})
	require.NoError(t, err)
	require.Nil(t, messages)

	count, err := store.CountMessages(context.Background(), testSessionA, MessageQueryOptions{})
	require.NoError(t, err)
	require.Zero(t, count)

	count, err = store.CountMessages(context.Background(), testMissingSession, MessageQueryOptions{})
	require.NoError(t, err)
	require.Zero(t, count)

	message, ok, err := store.GetMessage(context.Background(), testSessionA, 0)
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, morphmsg.Message{}, message)

	message, ok, err = store.GetMessage(context.Background(), testMissingSession, 0)
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, morphmsg.Message{}, message)

	records, err = store.GetMessagesByIDs(context.Background(), "", []uint{1})
	require.NoError(t, err)
	require.Nil(t, records)

	records, err = store.GetMessagesByIDs(context.Background(), "ses_invalid", []uint{1})
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
	require.Nil(t, records)

	records, err = store.GetMessagesByIDs(context.Background(), testSessionA, nil)
	require.NoError(t, err)
	require.Nil(t, records)

	records, err = store.GetMessagesByIDs(context.Background(), testSessionA, []uint{0})
	require.NoError(t, err)
	require.Nil(t, records)

	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "one", CreatedAt: now},
		{Role: morphmsg.RoleAssistant, Content: "two", CreatedAt: now.Add(time.Second)},
	}))

	records, err = store.GetMessagesByIDs(context.Background(), testSessionA, []uint{2})
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, uint(2), records[0].Message.ID)
	require.Equal(t, 1, records[0].Offset)

	records, err = store.GetMessagesByIDs(context.Background(), testSessionA, []uint{99})
	require.NoError(t, err)
	require.Empty(t, records)

	window, err = store.GetMessageWindow(context.Background(), "", 1, 0, 0)
	require.NoError(t, err)
	require.Nil(t, window)

	window, err = store.GetMessageWindow(context.Background(), testSessionA, 0, 0, 0)
	require.NoError(t, err)
	require.Nil(t, window)

	window, err = store.GetMessageWindow(context.Background(), "ses_invalid", 1, 0, 0)
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
	require.Nil(t, window)

	window, err = store.GetMessageWindow(context.Background(), testSessionA, 1, -1, 0)
	require.EqualError(t, err, "before and after must be greater than or equal to zero")
	require.Nil(t, window)

	window, err = store.GetMessageWindow(context.Background(), testSessionA, 99, 1, 1)
	require.NoError(t, err)
	require.Nil(t, window)
}

func TestMemoryStore_SaveUpdatesLastPromptTokens(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()

	require.NoError(t, store.Save(context.Background(), Session{
		ID:               testSessionA,
		LastPromptTokens: 123,
		UpdatedAt:        now,
	}))
	require.NoError(t, store.Save(context.Background(), Session{
		ID:               testSessionA,
		LastPromptTokens: 0,
		UpdatedAt:        now.Add(time.Minute),
	}))

	session, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Zero(t, session.LastPromptTokens)
}

func TestMemoryStore_SaveTrimsIDBeforeExistingSessionLookup(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()

	require.NoError(t, store.Save(context.Background(), Session{
		ID:               testSessionA,
		LastPromptTokens: 123,
		UpdatedAt:        now,
	}))
	require.NoError(t, store.Save(context.Background(), Session{
		ID:               "  " + testSessionA + "  ",
		LastPromptTokens: 0,
		UpdatedAt:        now.Add(time.Minute),
	}))

	session, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Zero(t, session.LastPromptTokens)
}

func TestMemoryStore_SavePreservesExistingCreatedAtAndAllowsPromptTokenOverwrite(t *testing.T) {
	store := NewStore()
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

func TestMemoryStore_SaveRefreshesUpdatedAtOnUpdate(t *testing.T) {
	store := NewStore()
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

func TestMemoryStore_SaveRoundTripsCompactionMetadata(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC)

	require.NoError(t, store.Save(context.Background(), Session{
		ID: testSessionA,
		Compaction: base.SessionCompaction{
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

func TestMemoryStore_SavePreservesExistingCompactionMetadataOnPartialUpdate(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC)

	require.NoError(t, store.Save(context.Background(), Session{
		ID: testSessionA,
		Compaction: base.SessionCompaction{
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

func TestMemoryStore_ClearsCompactionMetadataWhenLiveHistoryIsCleared(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC)

	require.NoError(t, store.Save(context.Background(), Session{
		ID: testSessionA,
		Compaction: base.SessionCompaction{
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
	require.Equal(t, base.SessionCompaction{}, session.Compaction)
}

func TestMemoryStore_ArchiveRejectsDefaultSessionAndPreservesMetadata(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC)

	require.NoError(t, store.Save(context.Background(), Session{
		ID: DefaultSessionID,
		Compaction: base.SessionCompaction{
			Status:             base.CompactionStatusSucceeded,
			TargetMessageCount: 12,
			TargetOffset:       4,
		},
		UpdatedAt: now,
	}))
	require.NoError(t, store.AppendMessages(context.Background(), DefaultSessionID, []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: now}}))
	require.NoError(t, store.SaveSummary(context.Background(), SessionSummary{
		SessionID:      DefaultSessionID,
		SessionSummary: "Older work",
	}))

	_, err := store.Archive(context.Background(), DefaultSessionID, base.SessionArchiveRequest{
		ArchivedAt: now,
		ExpiresAt:  now.Add(time.Hour),
	})
	require.EqualError(t, err, "default session cannot be archived")

	session, ok, err := store.Get(context.Background(), DefaultSessionID, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, base.SessionCompaction{
		Status:             base.CompactionStatusSucceeded,
		TargetMessageCount: 12,
		TargetOffset:       4,
	}, session.Compaction)

	messages, err := store.GetMessages(context.Background(), DefaultSessionID, MessageQueryOptions{})
	require.NoError(t, err)
	require.Len(t, messages, 1)

	_, ok, err = store.GetSummary(context.Background(), DefaultSessionID)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestMemoryStore_ArchiveRejectsDefaultSessionAndPreservesTitle(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC)

	require.NoError(t, store.Save(context.Background(), Session{
		ID:          DefaultSessionID,
		Title:       "Default Research",
		TitleSource: base.SessionTitleSourceGenerated,
		UpdatedAt:   now,
	}))
	require.NoError(t, store.AppendMessages(context.Background(), DefaultSessionID, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: now},
	}))

	_, err := store.Archive(context.Background(), DefaultSessionID, base.SessionArchiveRequest{
		ArchivedAt: now,
		ExpiresAt:  now.Add(time.Hour),
	})
	require.EqualError(t, err, "default session cannot be archived")

	session, ok, err := store.Get(context.Background(), DefaultSessionID, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "Default Research", session.Title)
	require.Equal(t, base.SessionTitleSourceGenerated, session.TitleSource)
}

func TestMemoryStore_ArchiveMarksNonDefaultSessionAndPreservesTitle(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC)

	require.NoError(t, store.Save(context.Background(), Session{
		ID:          testSessionA,
		Title:       "Project Research",
		TitleSource: base.SessionTitleSourceGenerated,
		UpdatedAt:   now,
	}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: now},
	}))

	requireArchiveSession(t, store, testSessionA, now, now.Add(time.Hour))

	session, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, session.Archived)
	require.Equal(t, now, session.ArchivedAt)
	require.Equal(t, now.Add(time.Hour), session.ExpiresAt)
	require.Equal(t, "Project Research", session.Title)
	require.Equal(t, base.SessionTitleSourceGenerated, session.TitleSource)
}

func TestMemoryStore_SaveTrimsIDOnCreate(t *testing.T) {
	store := NewStore()

	require.NoError(t, store.Save(context.Background(), Session{ID: "  " + testSessionA + "  "}))

	session, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, testSessionA, session.ID)
	require.False(t, session.CreatedAt.IsZero())
	require.False(t, session.UpdatedAt.IsZero())
}

func TestMemoryStore_GetMessagesRejectsInvalidLiveID(t *testing.T) {
	store := NewStore()

	messages, err := store.GetMessages(context.Background(), "ses_invalid", MessageQueryOptions{})

	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
	require.Nil(t, messages)
}

func TestMemoryStore_GetMessagesRejectsInvalidOrder(t *testing.T) {
	store := NewStore()

	messages, err := store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{Order: "sideways"})

	require.EqualError(t, err, "message order must be asc or desc")
	require.Nil(t, messages)
}

func TestMemoryStore_CountMessagesRejectsInvalidOrder(t *testing.T) {
	store := NewStore()

	count, err := store.CountMessages(context.Background(), testSessionA, MessageQueryOptions{Order: "sideways"})

	require.EqualError(t, err, "message order must be asc or desc")
	require.Zero(t, count)
}

func TestMemoryStore_GetMessagesUsesSessionIDForArchivedLookup(t *testing.T) {
	store := NewStore()
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleAssistant, Content: "archived"},
	}))
	requireArchiveSession(t, store, testSessionA, time.Time{}, time.Now().UTC().Add(time.Hour))

	messages, err := store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{})

	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, "archived", messages[0].Content)
}

func TestMemoryStore_GetMessagesSupportsOffsetAndLimit(t *testing.T) {
	store := NewStore()
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

	requireArchiveSession(t, store, testSessionA, now, now.Add(time.Hour))
	messages, err = store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{Offset: 0, Limit: 2})
	require.NoError(t, err)
	require.Len(t, messages, 2)
	require.Equal(t, "one", messages[0].Content)
	require.Equal(t, "two", messages[1].Content)

	messages, err = store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{})
	require.NoError(t, err)
	require.Len(t, messages, 4)
}

func TestMemoryStore_CountMessagesSupportsLiveAndArchivedQueries(t *testing.T) {
	store := NewStore()
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

	requireArchiveSession(t, store, testSessionA, now, now.Add(time.Hour))

	count, err = store.CountMessages(context.Background(), testSessionA, MessageQueryOptions{Offset: 1, Limit: 1})
	require.NoError(t, err)
	require.Equal(t, 3, count)
}

func TestMemoryStore_CountMessagesEdgeCases(t *testing.T) {
	store := NewStore()

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

func TestMemoryStore_GetMessageRejectsInvalidLiveID(t *testing.T) {
	store := NewStore()

	message, ok, err := store.GetMessage(context.Background(), "ses_invalid", 0)

	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
	require.False(t, ok)
	require.Equal(t, morphmsg.Message{}, message)
}

func TestMemoryStore_GetMessageUsesSessionIDForArchivedLookup(t *testing.T) {
	store := NewStore()
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleAssistant, Content: "archived"},
	}))
	requireArchiveSession(t, store, testSessionA, time.Time{}, time.Now().UTC().Add(time.Hour))

	message, ok, err := store.GetMessage(context.Background(), testSessionA, 0)

	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "archived", message.Content)
}

func TestMemoryStore_ArchiveMessageLookupsRejectInvalidIDs(t *testing.T) {
	store := NewStore()

	messages, err := store.GetMessages(context.Background(), "archive_invalid", MessageQueryOptions{})
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
	require.Nil(t, messages)

	message, ok, err := store.GetMessage(context.Background(), "archive_invalid", 0)
	require.EqualError(t, err, "session id must be a valid ses_ nanoid")
	require.False(t, ok)
	require.Equal(t, morphmsg.Message{}, message)

	require.EqualError(t, store.ClearMessages(context.Background(), "archive_invalid"), "session id must be a valid ses_ nanoid")
}

func TestMemoryStore_ClearMessagesClearsLiveMessagesAndRefreshesUpdatedAt(t *testing.T) {
	store := NewStore()
	originalUpdatedAt := time.Now().UTC().Add(-time.Hour)

	require.NoError(t, store.Save(context.Background(), Session{
		ID:        testSessionA,
		UpdatedAt: originalUpdatedAt,
	}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: time.Now().UTC()},
	}))

	require.NoError(t, store.ClearMessages(context.Background(), testSessionA))

	messages, err := store.GetMessages(context.Background(), testSessionA, MessageQueryOptions{})
	require.NoError(t, err)
	require.Nil(t, messages)

	session, ok, err := store.Get(context.Background(), testSessionA, base.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, session.UpdatedAt.After(originalUpdatedAt))
}

func TestMemoryStore_SummaryRoundTripAndCleanup(t *testing.T) {
	store := NewStore()
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

	loaded.Discoveries[0] = "changed"
	loadedAgain, ok, err := store.GetSummary(context.Background(), testSessionA)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "one", loadedAgain.Discoveries[0])

	require.NoError(t, store.DeleteSummary(context.Background(), testSessionA))
	_, ok, err = store.GetSummary(context.Background(), testSessionA)
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, store.SaveSummary(context.Background(), summary))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: now}}))
	requireArchiveSession(t, store, testSessionA, now, now.Add(time.Hour))

	_, ok, err = store.GetSummary(context.Background(), testSessionA)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestMemoryStore_SummaryErrors(t *testing.T) {
	var nilStore *Store

	require.EqualError(t, nilStore.SaveSummary(context.Background(), SessionSummary{}), "store is required")

	summary, ok, err := nilStore.GetSummary(context.Background(), testSessionA)
	require.EqualError(t, err, "store is required")
	require.False(t, ok)
	require.Equal(t, SessionSummary{}, summary)

	require.EqualError(t, nilStore.DeleteSummary(context.Background(), testSessionA), "store is required")

	store := NewStore()

	require.EqualError(t, store.SaveSummary(context.Background(), SessionSummary{}), "session id is required")
	require.EqualError(t, store.SaveSummary(context.Background(), SessionSummary{
		SessionID:      testMissingSession,
		SessionSummary: "summary",
	}), "session not found")

	summary, ok, err = store.GetSummary(context.Background(), "")
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

	require.EqualError(t, store.DeleteSummary(context.Background(), ""), "session id is required")
	require.EqualError(t, store.DeleteSummary(context.Background(), "ses_invalid"), "session id must be a valid ses_ nanoid")
}
