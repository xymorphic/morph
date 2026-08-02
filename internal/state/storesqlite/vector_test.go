package storesqlite

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	base "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/internal/state/search"
	storagevectorsqlite "github.com/xymorphic/morph/internal/state/search/vectorstore/sqlite"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/logutils"
)

func TestSQLiteStore_SearchMessagesSupportsVectorOnlyResults(t *testing.T) {
	store, vectorStore := sqliteVectorStoreTestStore(t)

	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{
		Role:      morphmsg.RoleUser,
		Content:   "semantic sqlite storage details",
		CreatedAt: now,
	}}))

	vectorStore.searchMatches = []search.VectorSearchMatch{{
		Record: vectorStore.upserts[0][0],
		Score:  0.91,
	}}

	results, err := store.SearchMessages(context.Background(), "", SearchMessageOptions{Query: "database upgrade"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, testSessionA, results[0].SessionID)
	require.Equal(t, "semantic sqlite storage details", results[0].Messages[0].MatchedText)
	require.Len(t, vectorStore.searches, 1)
	require.Equal(t, search.SourceKindSessionMessage, vectorStore.searches[0].Filter.SourceKind)
	require.Empty(t, vectorStore.searches[0].Filter.SourceIDs)
}

func TestSQLiteStore_SearchMessagesHybridMergesLexicalAndVectorEvidence(t *testing.T) {
	store, vectorStore := sqliteVectorStoreTestStore(t)

	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "needle exact lexical", CreatedAt: now},
		{Role: morphmsg.RoleUser, Content: "semantic database details", CreatedAt: now.Add(time.Second)},
	}))
	require.Len(t, vectorStore.upserts, 1)
	require.Len(t, vectorStore.upserts[0], 2)

	vectorStore.searchMatches = []search.VectorSearchMatch{{
		Record: vectorStore.upserts[0][1],
		Score:  0.95,
	}}

	results, err := store.SearchMessages(context.Background(), testSessionA, SearchMessageOptions{
		Query: "needle",
	})
	logutils.PrettyPrint(results)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, 2, results[0].MatchCount)
	require.Equal(t, []string{
		"semantic database details",
		"needle exact lexical",
	}, sqliteSearchMatchedTexts(results[0].Messages))
}

func TestSQLiteStore_SearchMessagesHybridRanksSessionsByFusedScoreBeforeRecency(t *testing.T) {
	store, vectorStore := sqliteVectorStoreTestStore(t)

	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionB, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "older semantic context", CreatedAt: now},
	}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionB, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "newer semantic context", CreatedAt: now.Add(time.Second)},
	}))

	vectorStore.searchMatches = []search.VectorSearchMatch{
		{Record: vectorStore.upserts[0][0], Score: 0.99},
		{Record: vectorStore.upserts[1][0], Score: 0.90},
	}

	results, err := store.SearchMessages(context.Background(), "", SearchMessageOptions{
		Query:       "meaningful context",
		MaxSessions: 1,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, testSessionA, results[0].SessionID)
	logutils.PrettyPrint(results)
}

func TestSQLiteStore_SearchMessagesRerankerChangesOrderBeforeLimits(t *testing.T) {
	store, vectorStore := sqliteVectorStoreTestStoreWithReranker(t, search.DeterministicReranker{}, 0)

	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionB, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "older semantic context", CreatedAt: now},
	}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionB, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "newer semantic context", CreatedAt: now.Add(time.Second)},
	}))

	sessionARecord := vectorStore.upserts[0][0]
	sessionBRecord := vectorStore.upserts[1][0]
	vectorStore.searchMatches = []search.VectorSearchMatch{
		{Record: sessionARecord, Score: 0.10},
		{Record: sessionBRecord, Score: 0.99},
	}

	results, err := store.SearchMessages(context.Background(), "", SearchMessageOptions{
		Query:       "database upgrade",
		MaxSessions: 1,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, testSessionB, results[0].SessionID)
}

func TestSQLiteStore_SearchMessagesRerankerCanBeDisabled(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)
	vectorStore := &sqliteTestVectorStore{}
	enableRerank := false
	require.NoError(t, store.ConfigureVectorStore(VectorStoreOptions{
		Embedder:       &sqliteTestEmbeddingProvider{dimensions: 3},
		Reranker:       search.DeterministicReranker{},
		VectorStore:    vectorStore,
		EnableRerank:   &enableRerank,
		EmbeddingModel: "text-embedding-test",
	}))

	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionB, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "older semantic context", CreatedAt: now},
	}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionB, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "newer semantic context", CreatedAt: now.Add(time.Second)},
	}))

	sessionARecord := vectorStore.upserts[0][0]
	sessionBRecord := vectorStore.upserts[1][0]
	vectorStore.searchMatches = []search.VectorSearchMatch{
		{Record: sessionARecord, Score: 0.99},
		{Record: sessionBRecord, Score: 0.90},
	}

	results, err := store.SearchMessages(context.Background(), "", SearchMessageOptions{
		Query:       "database upgrade",
		MaxSessions: 1,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, testSessionA, results[0].SessionID)
}

func TestSQLiteStore_SearchMessagesDiagnosticsAreInternal(t *testing.T) {
	originalLevel := zerolog.GlobalLevel()
	t.Cleanup(func() {
		zerolog.SetGlobalLevel(originalLevel)
		logutils.SetOutput(nil)
	})
	zerolog.SetGlobalLevel(zerolog.DebugLevel)

	store, vectorStore := sqliteVectorStoreTestStore(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{
		Role:      morphmsg.RoleUser,
		Content:   "semantic context private-token-123",
		CreatedAt: now,
	}}))
	vectorStore.searchMatches = []search.VectorSearchMatch{{Record: vectorStore.upserts[0][0], Score: 0.99}}

	var output bytes.Buffer
	logutils.SetOutput(&output)
	results, err := store.SearchMessages(context.Background(), "", SearchMessageOptions{
		Query:                 "semantic context private-token-123",
		MaxMessagesPerSession: 1,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.NotContains(t, output.String(), "session search ranking diagnostic")

	require.NoError(t, store.ConfigureVectorStore(VectorStoreOptions{
		Embedder:       &sqliteTestEmbeddingProvider{dimensions: 3},
		Reranker:       search.DeterministicReranker{},
		VectorStore:    vectorStore,
		EmbeddingModel: "text-embedding-test",
		Diagnostics:    true,
	}))
	output.Reset()
	results, err = store.SearchMessages(context.Background(), "", SearchMessageOptions{
		Query:                 "semantic context private-token-123",
		MaxMessagesPerSession: 1,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	store.logCandidateDiagnostics("candidate merged", []*searchCandidate{{
		CandidateMatch: search.CandidateMatch{
			SessionID:       testSessionA,
			MatchedToolName: "process",
		},
		ID: 1,
	}})

	logOutput := strings.ToLower(output.String())
	require.Contains(t, logOutput, "session search ranking diagnostic")
	require.Contains(t, logOutput, "lexical_score")
	require.Contains(t, logOutput, "vector_score")
	require.Contains(t, logOutput, "fused_score")
	require.Contains(t, logOutput, "rerank_rank")
	require.Contains(t, logOutput, "matched_tool_name")
	require.Contains(t, logOutput, "max_messages_per_session")
	require.NotContains(t, logOutput, "private-token-123")
	require.NotContains(t, logOutput, "semantic context")
}

func TestSQLiteStore_ConfigureVectorStoreRejectsUnknownReranker(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	err = store.ConfigureVectorStore(VectorStoreOptions{
		Embedder:       &sqliteTestEmbeddingProvider{dimensions: 3},
		Reranker:       &sqliteTestReranker{},
		VectorStore:    &sqliteTestVectorStore{},
		EmbeddingModel: "text-embedding-test",
	})
	require.EqualError(t, err, "reranker must be one of: noop, deterministic, llm")
}

func TestSQLiteStore_SearchMessagesRerankerBoundsCandidateInput(t *testing.T) {
	store, vectorStore := sqliteVectorStoreTestStoreWithReranker(t, search.DeterministicReranker{}, 1)

	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "first semantic context", CreatedAt: now},
		{Role: morphmsg.RoleUser, Content: "second semantic context", CreatedAt: now.Add(time.Second)},
	}))

	vectorStore.searchMatches = []search.VectorSearchMatch{
		{Record: vectorStore.upserts[0][0], Score: 0.99},
		{Record: vectorStore.upserts[0][1], Score: 0.98},
	}

	results, err := store.SearchMessages(context.Background(), "", SearchMessageOptions{Query: "semantic context"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, 2, results[0].MatchCount)
	require.Len(t, results[0].Messages, 1)
}

func TestSQLiteStore_SearchMessagesHybridPushesAndAppliesFilters(t *testing.T) {
	store, vectorStore := sqliteVectorStoreTestStore(t)

	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{
		Role:            morphmsg.RoleTool,
		Name:            "process",
		Content:         `{"stdout":"started","process_id":"ignored"}`,
		SemanticContent: "stdout: started",
		CreatedAt:       now,
	}}))
	require.Len(t, vectorStore.upserts[0], 1)

	toolRecord := vectorStore.upserts[0][0]
	vectorStore.searchMatches = []search.VectorSearchMatch{
		{Record: toolRecord, Score: 0.99},
	}

	results, err := store.SearchMessages(context.Background(), testSessionA, SearchMessageOptions{
		Query:    "start",
		ToolName: "process",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Len(t, results[0].Messages, 1)
	require.Equal(t, "process", results[0].Messages[0].MatchedToolName)
	require.Len(t, vectorStore.searches, 1)
	require.Equal(t, testSessionA, vectorStore.searches[0].Filter.SessionID)
	require.Equal(t, "process", vectorStore.searches[0].Filter.ToolName)
	require.Empty(t, vectorStore.searches[0].Filter.SourceIDs)
}

func TestSQLiteStore_SearchMessagesHybridPushesIgnoredSessionFilter(t *testing.T) {
	store, vectorStore := sqliteVectorStoreTestStore(t)

	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionB, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "ignored semantic context", CreatedAt: now},
	}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionB, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "kept semantic context", CreatedAt: now.Add(time.Second)},
	}))

	vectorStore.searchMatches = []search.VectorSearchMatch{
		{Record: vectorStore.upserts[0][0], Score: 0.99},
		{Record: vectorStore.upserts[1][0], Score: 0.98},
	}

	results, err := store.SearchMessages(context.Background(), "", SearchMessageOptions{
		Query:           "semantic",
		IgnoreSessionID: testSessionA,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, testSessionB, results[0].SessionID)
	require.Len(t, vectorStore.searches, 1)
	require.Equal(t, testSessionA, vectorStore.searches[0].Filter.IgnoreSessionID)
	require.Empty(t, vectorStore.searches[0].Filter.SourceIDs)
}

func TestSQLiteStore_SearchMessagesHybridCollapsesDuplicateVectorRows(t *testing.T) {
	store, vectorStore := sqliteVectorStoreTestStore(t)

	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{
		Role:            morphmsg.RoleTool,
		Name:            "search",
		Content:         `{"matches":"full transcript remains available"}`,
		SemanticContent: strings.Repeat("semantic chunk content ", 200),
		CreatedAt:       now,
	}}))
	require.GreaterOrEqual(t, len(vectorStore.upserts[0]), 2)

	vectorStore.searchMatches = []search.VectorSearchMatch{
		{Record: vectorStore.upserts[0][1], Score: 0.99},
		{Record: vectorStore.upserts[0][0], Score: 0.98},
	}

	results, err := store.SearchMessages(context.Background(), "", SearchMessageOptions{Query: "semantic"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, 1, results[0].MatchCount)
	require.Len(t, results[0].Messages, 1)
	require.Equal(t, "search", results[0].Messages[0].MatchedToolName)
}

func TestSQLiteStore_SearchMessagesHybridReturnsVectorError(t *testing.T) {
	store, vectorStore := sqliteVectorStoreTestStore(t)
	vectorErr := errors.New("vector unavailable")

	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{
		Role:      morphmsg.RoleUser,
		Content:   "needle lexical match",
		CreatedAt: now,
	}}))
	vectorStore.searchErr = vectorErr

	_, err := store.SearchMessages(context.Background(), testSessionA, SearchMessageOptions{Query: "needle"})
	require.ErrorIs(t, err, vectorErr)
}

func TestSQLiteStore_SearchMessagesHybridEdgeCases(t *testing.T) {
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	t.Run("returns nil when lexical and vector candidates are empty", func(t *testing.T) {
		store, _ := sqliteVectorStoreTestStore(t)
		results, err := store.SearchMessages(context.Background(), "", SearchMessageOptions{Query: "missing"})
		require.NoError(t, err)
		require.Nil(t, results)
	})

	t.Run("passes structured filters to vector search", func(t *testing.T) {
		store, vectorStore := sqliteVectorStoreTestStore(t)
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
			{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: now},
		}))

		results, err := store.SearchMessages(context.Background(), testSessionA, SearchMessageOptions{
			Query: "semantic",
			Role:  morphmsg.RoleTool,
		})
		require.NoError(t, err)
		require.Nil(t, results)
		require.Len(t, vectorStore.searches, 1)
		require.Equal(t, testSessionA, vectorStore.searches[0].Filter.SessionID)
		require.Equal(t, string(morphmsg.RoleTool), vectorStore.searches[0].Filter.Role)
		require.Empty(t, vectorStore.searches[0].Filter.SourceIDs)
	})

	t.Run("returns query embedding error", func(t *testing.T) {
		embedErr := errors.New("embed failed")
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.ConfigureVectorStore(VectorStoreOptions{
			Embedder:       &sqliteTestEmbeddingProvider{err: embedErr},
			VectorStore:    &sqliteTestVectorStore{},
			EmbeddingModel: "text-embedding-test",
		}))
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
			{Role: morphmsg.RoleUser, Content: "needle lexical match", CreatedAt: now},
		}))

		_, err = store.SearchMessages(context.Background(), testSessionA, SearchMessageOptions{Query: "needle"})
		require.ErrorIs(t, err, embedErr)
	})

	t.Run("returns malformed query embedding error", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		provider := &sqliteTestEmbeddingProvider{
			mutate: func(result search.EmbeddingResult) search.EmbeddingResult {
				result.Items[0].ContentHash = "wrong"
				return result
			},
			dimensions: 3,
		}
		require.NoError(t, store.ConfigureVectorStore(VectorStoreOptions{
			Embedder:       provider,
			VectorStore:    &sqliteTestVectorStore{},
			EmbeddingModel: "text-embedding-test",
		}))
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
			{Role: morphmsg.RoleUser, Content: "needle lexical match", CreatedAt: now},
		}))

		_, err = store.SearchMessages(context.Background(), testSessionA, SearchMessageOptions{Query: "needle"})
		require.EqualError(t, err, "embedding content hash must match input text")
	})

	t.Run("returns lexical errors before vector search", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.ConfigureVectorStore(VectorStoreOptions{
			Embedder:       &sqliteTestEmbeddingProvider{dimensions: 3},
			VectorStore:    &sqliteTestVectorStore{},
			EmbeddingModel: "text-embedding-test",
		}))
		require.NoError(t, store.db.Exec(`DROP TABLE `+sessionMessageSearchTable).Error)

		_, err = store.SearchMessages(context.Background(), testSessionA, SearchMessageOptions{Query: "needle"})
		require.Error(t, err)
	})
}

func TestSQLiteStore_HybridSearchHelpers(t *testing.T) {
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	t.Run("merges vector evidence and bounds candidate collection", func(t *testing.T) {
		candidates := searchCandidateSet{
			1: {
				CandidateMatch: search.CandidateMatch{
					SessionID:   testSessionA,
					LexicalRank: 1,
					HasLexical:  true,
				},
				ID: 1,
			},
		}
		candidates.Merge([]*searchCandidate{
			nil,
			{
				CandidateMatch: search.CandidateMatch{
					MatchedText:     "vector text",
					MatchedToolName: "tool",
					VectorRank:      1,
					HasVector:       true,
				},
				ID: 1,
			},
		}, getSearchCandidateKey)
		require.True(t, candidates[1].HasVector)
		require.Equal(t, "vector text", candidates[1].MatchedText)
		require.Equal(t, "tool", candidates[1].MatchedToolName)
		require.Equal(t, defaultHybridRetrievalCandidateLimit, getHybridCandidateLimit(SearchMessageOptions{}))
		require.Equal(t, 120, getHybridCandidateLimit(SearchMessageOptions{
			MaxSessions:           12,
			MaxMessagesPerSession: 10,
		}))
		require.Equal(t, maxHybridRetrievalCandidateLimit, getHybridCandidateLimit(SearchMessageOptions{
			MaxSessions:           maxHybridRetrievalCandidateLimit,
			MaxMessagesPerSession: maxHybridRetrievalCandidateLimit,
		}))
	})

	t.Run("keeps top ranked message per session when message limit is one", func(t *testing.T) {
		rows := searchCandidatesToRankedSearchRows(searchCandidateSet{
			1: {
				CandidateMatch: search.CandidateMatch{
					SessionID:   testSessionA,
					MatchedText: "older",
					VectorRank:  2,
					HasVector:   true,
				},
				ID:        1,
				Content:   "older",
				CreatedAt: now,
			},
			2: {
				CandidateMatch: search.CandidateMatch{
					SessionID:   testSessionA,
					MatchedText: "newer",
					VectorRank:  1,
					HasVector:   true,
				},
				ID:        2,
				Content:   "newer",
				CreatedAt: now.Add(time.Second),
			},
		}, SearchMessageOptions{MaxMessagesPerSession: 1})
		require.Len(t, rows, 1)
		require.Equal(t, uint(2), rows[0].ID)
	})

	t.Run("orders same-session messages by score before recency", func(t *testing.T) {
		rows := searchCandidateSliceToRankedSearchRows([]*searchCandidate{
			{
				CandidateMatch: search.CandidateMatch{SessionID: testSessionA, MatchedText: "better", FusedScore: 2},
				ID:             1,
				CreatedAt:      now,
			},
			{
				CandidateMatch: search.CandidateMatch{SessionID: testSessionA, MatchedText: "worse", FusedScore: 1},
				ID:             2,
				CreatedAt:      now.Add(time.Second),
			},
		}, SearchMessageOptions{}, nil, nil)
		require.Equal(t, []uint{1, 2}, []uint{rows[0].ID, rows[1].ID})
	})

	t.Run("orders same-session score ties by older message after newer", func(t *testing.T) {
		rows := searchCandidateSliceToRankedSearchRows([]*searchCandidate{
			{
				CandidateMatch: search.CandidateMatch{SessionID: testSessionA, MatchedText: "older", FusedScore: 1},
				ID:             1,
				CreatedAt:      now,
			},
			{
				CandidateMatch: search.CandidateMatch{SessionID: testSessionA, MatchedText: "newer", FusedScore: 1},
				ID:             2,
				CreatedAt:      now.Add(time.Second),
			},
		}, SearchMessageOptions{}, nil, nil)
		require.Equal(t, []uint{2, 1}, []uint{rows[0].ID, rows[1].ID})
	})

	t.Run("orders tied sessions by session id", func(t *testing.T) {
		rows := searchCandidatesToRankedSearchRows(searchCandidateSet{
			1: {
				CandidateMatch: search.CandidateMatch{SessionID: testSessionA, MatchedText: "left", VectorRank: 1, HasVector: true},
				ID:             1,
				CreatedAt:      now,
			},
			2: {
				CandidateMatch: search.CandidateMatch{SessionID: testSessionB, MatchedText: "right", VectorRank: 1, HasVector: true},
				ID:             2,
				CreatedAt:      now,
			},
		}, SearchMessageOptions{})
		require.Equal(t, []string{testSessionA, testSessionB}, []string{rows[0].SessionID, rows[1].SessionID})
	})

	t.Run("orders tied sessions by reverse session id when left is greater", func(t *testing.T) {
		rows := searchCandidateSliceToRankedSearchRows([]*searchCandidate{
			{
				CandidateMatch: search.CandidateMatch{SessionID: testSessionB, MatchedText: "right", FusedScore: 1},
				ID:             1,
				CreatedAt:      now,
			},
			{
				CandidateMatch: search.CandidateMatch{SessionID: testSessionA, MatchedText: "left", FusedScore: 1},
				ID:             2,
				CreatedAt:      now,
			},
		}, SearchMessageOptions{}, nil, nil)
		require.Equal(t, []string{testSessionA, testSessionB}, []string{rows[0].SessionID, rows[1].SessionID})
	})

	t.Run("orders tied messages in the same session by newest id", func(t *testing.T) {
		rows := searchCandidatesToRankedSearchRows(searchCandidateSet{
			1: {
				CandidateMatch: search.CandidateMatch{SessionID: testSessionA, MatchedText: "left", VectorRank: 1, HasVector: true},
				ID:             1,
				CreatedAt:      now,
			},
			2: {
				CandidateMatch: search.CandidateMatch{SessionID: testSessionA, MatchedText: "right", VectorRank: 1, HasVector: true},
				ID:             2,
				CreatedAt:      now,
			},
		}, SearchMessageOptions{})
		require.Equal(t, uint(2), rows[0].ID)
	})

	t.Run("keeps equal same-session messages stable", func(t *testing.T) {
		rows := searchCandidateSliceToRankedSearchRows([]*searchCandidate{
			{
				CandidateMatch: search.CandidateMatch{SessionID: testSessionA, MatchedText: "left", FusedScore: 1},
				ID:             1,
				CreatedAt:      now,
			},
			{
				CandidateMatch: search.CandidateMatch{SessionID: testSessionA, MatchedText: "right", FusedScore: 1},
				ID:             1,
				CreatedAt:      now,
			},
		}, SearchMessageOptions{}, nil, nil)
		require.Equal(t, []string{"left", "right"}, []string{rows[0].MatchedText, rows[1].MatchedText})
	})

	t.Run("orders tied sessions by newest matching message", func(t *testing.T) {
		rows := searchCandidatesToRankedSearchRows(searchCandidateSet{
			1: {
				CandidateMatch: search.CandidateMatch{SessionID: testSessionA, MatchedText: "left", VectorRank: 1, HasVector: true},
				ID:             1,
				CreatedAt:      now,
			},
			2: {
				CandidateMatch: search.CandidateMatch{SessionID: testSessionB, MatchedText: "right", VectorRank: 1, HasVector: true},
				ID:             2,
				CreatedAt:      now.Add(time.Second),
			},
		}, SearchMessageOptions{})
		require.Equal(t, testSessionB, rows[0].SessionID)
	})

	t.Run("orders sessions by score before recency", func(t *testing.T) {
		rows := searchCandidateSliceToRankedSearchRows([]*searchCandidate{
			{
				CandidateMatch: search.CandidateMatch{SessionID: testSessionA, MatchedText: "higher score", FusedScore: 2},
				ID:             1,
				CreatedAt:      now,
			},
			{
				CandidateMatch: search.CandidateMatch{SessionID: testSessionB, MatchedText: "newer", FusedScore: 1},
				ID:             2,
				CreatedAt:      now.Add(time.Second),
			},
		}, SearchMessageOptions{}, nil, nil)
		require.Equal(t, testSessionA, rows[0].SessionID)
	})

	t.Run("uses supplied last matched timestamp when newer than candidates", func(t *testing.T) {
		rows := searchCandidateSliceToRankedSearchRows([]*searchCandidate{{
			CandidateMatch: search.CandidateMatch{SessionID: testSessionA, MatchedText: "left", FusedScore: 1},
			ID:             1,
			CreatedAt:      now,
		}}, SearchMessageOptions{}, map[string]int{testSessionA: 3}, map[string]time.Time{
			testSessionA: now.Add(time.Hour),
		})
		require.Equal(t, 3, rows[0].MatchCount)
		require.Equal(t, now.Add(time.Hour), getSearchSessionResultTime(rows[0].LastMatchedAt))
	})

	t.Run("compareSearchCandidates covers id and equality ties", func(t *testing.T) {
		left := &searchCandidate{CandidateMatch: search.CandidateMatch{SessionID: testSessionA, FusedScore: 1}, ID: 1}
		right := &searchCandidate{CandidateMatch: search.CandidateMatch{SessionID: testSessionA, FusedScore: 1}, ID: 2}
		require.Equal(t, -1, compareSearchCandidates(&searchCandidate{CandidateMatch: search.CandidateMatch{FusedScore: 2}}, left))
		require.Equal(t, 1, compareSearchCandidates(left, &searchCandidate{CandidateMatch: search.CandidateMatch{FusedScore: 2}}))
		require.Equal(t, -1, compareSearchCandidates(right, left))
		require.Equal(t, 1, compareSearchCandidates(left, right))
		require.Equal(t, 0, compareSearchCandidates(left, left))
	})

	t.Run("ranking comparators cover all tie-break directions", func(t *testing.T) {
		require.Equal(t, -1, compareCandidatesWithinSession(
			&searchCandidate{CandidateMatch: search.CandidateMatch{FusedScore: 2}, ID: 1},
			&searchCandidate{CandidateMatch: search.CandidateMatch{FusedScore: 1}, ID: 2},
		))
		require.Equal(t, 1, compareCandidatesWithinSession(
			&searchCandidate{CandidateMatch: search.CandidateMatch{FusedScore: 1}, ID: 1},
			&searchCandidate{CandidateMatch: search.CandidateMatch{FusedScore: 2}, ID: 2},
		))
		require.Equal(t, -1, compareCandidatesWithinSession(
			&searchCandidate{CandidateMatch: search.CandidateMatch{FusedScore: 1}, ID: 1, CreatedAt: now.Add(time.Second)},
			&searchCandidate{CandidateMatch: search.CandidateMatch{FusedScore: 1}, ID: 2, CreatedAt: now},
		))
		require.Equal(t, 1, compareCandidatesWithinSession(
			&searchCandidate{CandidateMatch: search.CandidateMatch{FusedScore: 1}, ID: 1, CreatedAt: now},
			&searchCandidate{CandidateMatch: search.CandidateMatch{FusedScore: 1}, ID: 2, CreatedAt: now.Add(time.Second)},
		))
		require.Equal(t, -1, compareCandidatesWithinSession(
			&searchCandidate{CandidateMatch: search.CandidateMatch{FusedScore: 1}, ID: 2, CreatedAt: now},
			&searchCandidate{CandidateMatch: search.CandidateMatch{FusedScore: 1}, ID: 1, CreatedAt: now},
		))
		require.Equal(t, 1, compareCandidatesWithinSession(
			&searchCandidate{CandidateMatch: search.CandidateMatch{FusedScore: 1}, ID: 1, CreatedAt: now},
			&searchCandidate{CandidateMatch: search.CandidateMatch{FusedScore: 1}, ID: 2, CreatedAt: now},
		))
		require.Equal(t, 0, compareCandidatesWithinSession(
			&searchCandidate{CandidateMatch: search.CandidateMatch{FusedScore: 1}, ID: 1, CreatedAt: now},
			&searchCandidate{CandidateMatch: search.CandidateMatch{FusedScore: 1}, ID: 1, CreatedAt: now},
		))

		bestScores := map[string]float64{testSessionA: 2, testSessionB: 1}
		lastMatched := map[string]time.Time{testSessionA: now, testSessionB: now}
		require.Equal(t, -1, compareRankedSessions(testSessionA, testSessionB, bestScores, lastMatched))
		require.Equal(t, 1, compareRankedSessions(testSessionB, testSessionA, bestScores, lastMatched))

		bestScores = map[string]float64{testSessionA: 1, testSessionB: 1}
		lastMatched = map[string]time.Time{testSessionA: now.Add(time.Second), testSessionB: now}
		require.Equal(t, -1, compareRankedSessions(testSessionA, testSessionB, bestScores, lastMatched))
		require.Equal(t, 1, compareRankedSessions(testSessionB, testSessionA, bestScores, lastMatched))

		lastMatched = map[string]time.Time{testSessionA: now, testSessionB: now}
		require.Equal(t, -1, compareRankedSessions(testSessionA, testSessionB, bestScores, lastMatched))
		require.Equal(t, 1, compareRankedSessions(testSessionB, testSessionA, bestScores, lastMatched))
		require.Equal(t, 0, compareRankedSessions(testSessionA, testSessionA, bestScores, lastMatched))
	})

	t.Run("rerank helper skips empty candidates and falls back to content text", func(t *testing.T) {
		var store *Store
		items := store.rerankSearchCandidates(context.Background(), SearchMessageOptions{}, searchCandidateSet{})
		require.Nil(t, items)

		items = store.rerankSearchCandidates(context.Background(), SearchMessageOptions{}, searchCandidateSet{
			1: {
				CandidateMatch: search.CandidateMatch{SessionID: testSessionA, FusedScore: 1},
				ID:             1,
			},
		})
		require.Len(t, items, 1)

		candidate := searchCandidateToRetrievalCandidate(&searchCandidate{
			CandidateMatch: search.CandidateMatch{SessionID: testSessionA, FusedScore: 1},
			ID:             1,
			Content:        "content fallback",
		})
		require.Equal(t, "content fallback", candidate.Text)
	})

	t.Run("parses session message source ids and rejects unrelated ids", func(t *testing.T) {
		_, ok := sourceIDToMessageRef("memory_item:mem:1")
		require.False(t, ok)
		_, ok = sourceIDToMessageRef("session_message")
		require.False(t, ok)
		_, ok = sourceIDToMessageRef("session_message:ses_test:")
		require.False(t, ok)
		_, ok = sourceIDToMessageRef("session_message:ses_test:abc")
		require.False(t, ok)
		ref, ok := sourceIDToMessageRef("session_message:ses_test:42")
		require.True(t, ok)
		require.Equal(t, messageRef{SessionID: "ses_test", MessageID: 42}, ref)
	})

	t.Run("rejects vector matches outside session and role filters", func(t *testing.T) {
		require.False(t, checkVectorRecordMatchesOptions(messageModel{SessionID: testSessionB}, testSessionA, SearchMessageOptions{}))
		require.False(t, checkVectorRecordMatchesOptions(messageModel{SessionID: testSessionA}, "", SearchMessageOptions{
			IgnoreSessionID: testSessionA,
		}))
		require.False(t, checkVectorRecordMatchesOptions(messageModel{
			SessionID: testSessionA,
			Role:      string(morphmsg.RoleUser),
		}, "", SearchMessageOptions{Role: morphmsg.RoleAssistant}))
	})

	t.Run("rejects vector row ids that cannot map to a search row", func(t *testing.T) {
		_, ok := vectorRecordToSearchRow(messageModel{}, "bad", search.VectorChunkOptions{})
		require.False(t, ok)
		_, ok = vectorRecordToSearchRow(messageModel{
			ID:        1,
			SessionID: testSessionA,
			Role:      string(morphmsg.RoleUser),
			Content:   "hello",
		}, "bad", search.VectorChunkOptions{})
		require.False(t, ok)
		_, ok = vectorRecordToSearchRow(messageModel{
			ID:        1,
			SessionID: testSessionA,
			Role:      string(morphmsg.RoleUser),
			Content:   "hello",
		}, "session_message:ses_test:1:row:abc", search.VectorChunkOptions{})
		require.False(t, ok)
	})
}

func TestSQLiteStore_HybridVectorCandidateErrorPaths(t *testing.T) {
	store, vectorStore := sqliteVectorStoreTestStore(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: now},
	}))
	vectorStore.searchMatches = []search.VectorSearchMatch{{
		Record: vectorStore.upserts[0][0],
		Score:  1,
	}}

	require.NoError(t, store.db.Exec(`DROP TABLE session_messages`).Error)
	_, err := store.searchMessagesVector(context.Background(), testSessionA, SearchMessageOptions{Query: "hello"}, defaultHybridRetrievalCandidateLimit)
	require.Error(t, err)

	store, vectorStore = sqliteVectorStoreTestStore(t)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: now},
	}))
	ref, ok := sourceIDToMessageRef(vectorStore.upserts[0][0].SourceID)
	require.True(t, ok)
	require.Equal(t, testSessionA, ref.SessionID)

	matches := []search.VectorSearchMatch{{
		Record: search.VectorRecord{
			ID:       "bad",
			SourceID: "bad",
		},
		Score: 1,
	}}
	candidates, err := store.vectorMatchesToCandidates(context.Background(), "", SearchMessageOptions{}, matches)
	require.NoError(t, err)
	require.Nil(t, candidates)

	matches = []search.VectorSearchMatch{
		{Record: vectorStore.upserts[0][0], Score: 1},
		{Record: search.VectorRecord{ID: "bad", SourceID: "bad"}, Score: 0.9},
	}
	candidates, err = store.vectorMatchesToCandidates(context.Background(), "", SearchMessageOptions{}, matches)
	require.NoError(t, err)
	require.Len(t, candidates, 1)

	stale := vectorStore.upserts[0][0]
	stale.ContentHash = search.VectorContentHash("different body")
	candidates, err = store.vectorMatchesToCandidates(context.Background(), "", SearchMessageOptions{}, []search.VectorSearchMatch{{
		Record: stale,
		Score:  1,
	}})
	require.NoError(t, err)
	require.Empty(t, candidates)

	_, err = store.messagesByRef(context.Background(), messageRefs{{SessionID: testSessionA, MessageID: 1}})
	require.NoError(t, err)
	records, err := store.messagesByRef(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, records)
	records, err = store.messagesByRef(context.Background(), messageRefs{
		{SessionID: testSessionA, MessageID: 1},
		{SessionID: testSessionA, MessageID: 1},
	})
	require.NoError(t, err)
	require.Len(t, records, 1)
	records, err = store.messagesByRef(context.Background(), messageRefs{{SessionID: testSessionA, MessageID: 999}})
	require.NoError(t, err)
	require.Empty(t, records)
	require.NoError(t, store.db.Exec(`DROP TABLE session_messages`).Error)
	_, err = store.messagesByRef(context.Background(), messageRefs{{SessionID: testSessionA, MessageID: 1}})
	require.Error(t, err)

	_, err = store.vectorMatchesToCandidates(context.Background(), "", SearchMessageOptions{}, matches[:1])
	require.Error(t, err)
}

func TestSQLiteStore_VectorStoreAppendAndRebuild(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	provider := &sqliteTestEmbeddingProvider{dimensions: 3}
	vectorStore := &sqliteTestVectorStore{}
	require.NoError(t, store.ConfigureVectorStore(VectorStoreOptions{
		Embedder:       provider,
		VectorStore:    vectorStore,
		EmbeddingModel: "text-embedding-test",
	}))

	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{
		Role:    morphmsg.RoleAssistant,
		Content: "assistant summary",
		ToolCalls: []morphmsg.ToolCall{
			{ID: "call-1", Name: "process", Input: `{"action":"start"}`},
			{ID: "call-2", Name: "search", Input: `{"query":"needle"}`},
		},
		CreatedAt: now,
	}}))

	require.Len(t, provider.requests, 1)
	require.Equal(t, "text-embedding-test", provider.requests[0].Model)
	require.Len(t, provider.requests[0].Inputs, 1)
	require.Equal(t, "assistant summary", provider.requests[0].Inputs[0].Text)

	require.Len(t, vectorStore.upserts, 1)
	require.Len(t, vectorStore.upserts[0], 1)
	sourceID := vectorStore.upserts[0][0].SourceID
	require.Equal(t, search.SourceKindSessionMessage, vectorStore.upserts[0][0].SourceKind)
	require.Equal(t, sourceID+":row:1:chunk:1", vectorStore.upserts[0][0].ID)
	require.Equal(t, testSessionA, vectorStore.upserts[0][0].SessionID)
	require.Equal(t, string(morphmsg.RoleAssistant), vectorStore.upserts[0][0].Role)
	require.Empty(t, vectorStore.upserts[0][0].ToolName)

	require.NoError(t, store.RebuildVectorStore(context.Background(), testSessionA))
	require.Len(t, vectorStore.deletes, 1)
	require.Equal(t, []string{sourceID}, vectorStore.deletes[0].SourceIDs)
	require.Len(t, vectorStore.upserts, 2)
	require.Equal(t, sqliteTestRecordIDs(vectorStore.upserts[0]), sqliteTestRecordIDs(vectorStore.upserts[1]))
	require.Equal(t, vectorStore.upserts[0][0].ContentHash, vectorStore.upserts[1][0].ContentHash)
}

func TestSQLiteStore_VectorStoreDeletesSessionVectors(t *testing.T) {
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	t.Run("clear messages deletes vectors", func(t *testing.T) {
		store, vectorStore := sqliteVectorStoreTestStore(t)
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
			{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: now},
		}))

		require.NoError(t, store.ClearMessages(context.Background(), testSessionA))

		require.Equal(t, []search.VectorDeleteRequest{{
			SourceKind: search.SourceKindSessionMessage,
			SessionID:  testSessionA,
		}}, vectorStore.deletes)

		var stateCount int64
		require.NoError(t, store.db.Model(&vectorIndexStateModel{}).Where("session_id = ?", testSessionA).Count(&stateCount).Error)
		require.Zero(t, stateCount)
	})

	t.Run("delete session deletes vectors", func(t *testing.T) {
		store, vectorStore := sqliteVectorStoreTestStore(t)
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
			{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: now},
		}))

		require.NoError(t, store.Delete(context.Background(), testSessionA))

		require.Equal(t, []search.VectorDeleteRequest{{
			SourceKind: search.SourceKindSessionMessage,
			SessionID:  testSessionA,
		}}, vectorStore.deletes)

		var stateCount int64
		require.NoError(t, store.db.Model(&vectorIndexStateModel{}).Where("session_id = ?", testSessionA).Count(&stateCount).Error)
		require.Zero(t, stateCount)
	})

	t.Run("archive session keeps source vectors", func(t *testing.T) {
		store, vectorStore := sqliteVectorStoreTestStore(t)
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
			{Role: morphmsg.RoleUser, Content: "hello", CreatedAt: now},
		}))

		_, err := store.Archive(context.Background(), testSessionA, base.SessionArchiveRequest{
			ArchivedAt: now,
			ExpiresAt:  now.Add(time.Hour),
		})
		require.NoError(t, err)

		require.Empty(t, vectorStore.deletes)
	})
}

func TestSQLiteStore_VectorStoreUsesSharedSQLiteVectorStore(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "morph.db")))
	require.NoError(t, err)

	store, err := NewStoreFromDB(db)
	require.NoError(t, err)
	vectorStore, err := storagevectorsqlite.NewStoreFromDB(db)
	require.NoError(t, err)
	provider := &sqliteTestEmbeddingProvider{dimensions: 3}
	require.NoError(t, store.ConfigureVectorStore(VectorStoreOptions{
		Embedder:       provider,
		VectorStore:    vectorStore,
		EmbeddingModel: "text-embedding-test",
	}))

	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "shared sqlite vector", CreatedAt: now},
	}))

	embedRes, err := provider.Embed(context.Background(), search.EmbeddingRequest{
		Model: "text-embedding-test",
		Inputs: []search.EmbeddingInput{{
			ID:         "query",
			Text:       "shared sqlite vector",
			SourceKind: search.SourceKindSessionMessage,
		}},
	})
	require.NoError(t, err)

	result, err := vectorStore.Search(context.Background(), storagevectorsqlite.SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     3,
		QueryVector:    embedRes.Items[0].Vector,
		Limit:          1,
		Filter: storagevectorsqlite.Filter{
			SourceKind: storagevectorsqlite.SourceKindSessionMessage,
		},
	})
	require.NoError(t, err)
	require.Len(t, result.Matches, 1)
	require.Equal(t, search.SourceKindSessionMessage, result.Matches[0].Record.SourceKind)
	require.Equal(t, search.VectorContentHash("shared sqlite vector"), result.Matches[0].Record.ContentHash)

	require.NoError(t, store.ClearMessages(context.Background(), testSessionA))
	result, err = vectorStore.Search(context.Background(), storagevectorsqlite.SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     3,
		QueryVector:    embedRes.Items[0].Vector,
		Limit:          1,
		Filter: storagevectorsqlite.Filter{
			SourceKind: storagevectorsqlite.SourceKindSessionMessage,
		},
	})
	require.NoError(t, err)
	require.Empty(t, result.Matches)
}

func TestSQLiteStore_VectorStoreBestEffortAndRequiredErrors(t *testing.T) {
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	embedErr := errors.New("embed failed")

	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.ConfigureVectorStore(VectorStoreOptions{
		Embedder:       &sqliteTestEmbeddingProvider{err: embedErr},
		VectorStore:    &sqliteTestVectorStore{},
		EmbeddingModel: "text-embedding-test",
	}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "best effort", CreatedAt: now},
	}))

	requiredStore, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)
	require.NoError(t, requiredStore.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, requiredStore.ConfigureVectorStore(VectorStoreOptions{
		Embedder:       &sqliteTestEmbeddingProvider{err: embedErr},
		VectorStore:    &sqliteTestVectorStore{},
		EmbeddingModel: "text-embedding-test",
		Required:       true,
	}))
	err = requiredStore.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "required", CreatedAt: now},
	})
	require.NoError(t, err)
	var state vectorIndexStateModel
	require.NoError(t, requiredStore.db.First(&state, "session_id = ?", testSessionA).Error)
	require.Equal(t, string(search.VectorIndexFailed), state.Status)
	require.Equal(t, 1, state.Attempts)
}

func TestSQLiteStore_AppendMessagesTracksReadyAndSkippedSemanticSourcesIndependently(t *testing.T) {
	store, _ := sqliteVectorStoreTestStore(t)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "indexed prose"},
		{Role: morphmsg.RoleTool, Name: "plan_tool", Content: `{"status":"complete"}`},
	}))

	var states []vectorIndexStateModel
	require.NoError(t, store.db.Order("message_id asc").Find(&states).Error)
	require.Len(t, states, 2)
	require.Equal(t, string(search.VectorIndexReady), states[0].Status)
	require.Equal(t, 1, states[0].Attempts)
	require.Equal(t, string(search.VectorIndexSkipped), states[1].Status)
	require.Zero(t, states[1].Attempts)
}

func TestSQLiteStore_AppendMessagesMarksSourceFailedWhenOneRuneExceedsByteLimit(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
	require.NoError(t, store.ConfigureVectorStore(VectorStoreOptions{
		Embedder:         &sqliteTestEmbeddingProvider{},
		VectorStore:      &sqliteTestVectorStore{},
		EmbeddingModel:   "text-embedding-test",
		MaxInputBytes:    1,
		MaxDocumentBytes: 1,
	}))

	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{
		Role:    morphmsg.RoleUser,
		Content: "🙂",
	}}))

	var state vectorIndexStateModel
	require.NoError(t, store.db.First(&state, "session_id = ?", testSessionA).Error)
	require.Equal(t, string(search.VectorIndexFailed), state.Status)
	require.Equal(t, 1, state.Attempts)
}

func TestSQLiteStore_LoadRetryableVectorSourceIDsHandlesEmptyInputs(t *testing.T) {
	values, err := (*Store)(nil).loadRetryableVectorSourceIDs(context.Background(), []search.VectorInput{{SourceID: "source"}})
	require.NoError(t, err)
	require.Nil(t, values)

	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)
	values, err = store.loadRetryableVectorSourceIDs(context.Background(), nil)
	require.NoError(t, err)
	require.Nil(t, values)
}

func TestSQLiteStore_VectorStoreConfigurationValidation(t *testing.T) {
	var nilStore *Store
	require.EqualError(t, nilStore.ConfigureVectorStore(VectorStoreOptions{}), "store is required")

	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	require.EqualError(t, store.ConfigureVectorStore(VectorStoreOptions{
		VectorStore:    &sqliteTestVectorStore{},
		EmbeddingModel: "text-embedding-test",
	}), "vector store embedding provider is required")
	require.EqualError(t, store.ConfigureVectorStore(VectorStoreOptions{
		Embedder:       &sqliteTestEmbeddingProvider{dimensions: 3},
		EmbeddingModel: "text-embedding-test",
	}), "vector store is required")
	require.EqualError(t, store.ConfigureVectorStore(VectorStoreOptions{
		Embedder:    &sqliteTestEmbeddingProvider{dimensions: 3},
		VectorStore: &sqliteTestVectorStore{},
	}), "vector store embedding model is required")
	for _, test := range []struct {
		name             string
		maxInputBytes    int
		maxDocumentBytes int
		err              string
	}{
		{name: "negative max input bytes", maxInputBytes: -1, err: "vector store chunk limits must be greater than or equal to zero"},
		{name: "negative max document bytes", maxDocumentBytes: -1, err: "vector store chunk limits must be greater than or equal to zero"},
		{
			name: "document limit below input limit", maxInputBytes: 2048, maxDocumentBytes: 1024,
			err: "vector store max document bytes must be greater than or equal to max input bytes",
		},
	} {
		t.Run("rejects "+test.name, func(t *testing.T) {
			require.EqualError(t, store.ConfigureVectorStore(VectorStoreOptions{
				Embedder:         &sqliteTestEmbeddingProvider{dimensions: 3},
				VectorStore:      &sqliteTestVectorStore{},
				EmbeddingModel:   "text-embedding-test",
				MaxInputBytes:    test.maxInputBytes,
				MaxDocumentBytes: test.maxDocumentBytes,
			}), test.err)
		})
	}
	require.EqualError(t, store.ConfigureVectorStore(VectorStoreOptions{
		Embedder:         &sqliteTestEmbeddingProvider{dimensions: 3},
		VectorStore:      &sqliteTestVectorStore{},
		EmbeddingModel:   "text-embedding-test",
		RebuildBatchSize: -1,
	}), "vector store rebuild batch size must be greater than or equal to zero")
	require.EqualError(t, store.ConfigureVectorStore(VectorStoreOptions{
		Embedder:            &sqliteTestEmbeddingProvider{dimensions: 3},
		VectorStore:         &sqliteTestVectorStore{},
		EmbeddingModel:      "text-embedding-test",
		RerankMaxCandidates: -1,
	}), "vector store rerank max candidates must be greater than or equal to zero")
	require.NoError(t, store.ConfigureVectorStore(VectorStoreOptions{}))

	require.NoError(t, store.ConfigureVectorStore(VectorStoreOptions{
		Embedder:       &sqliteTestEmbeddingProvider{dimensions: 3},
		VectorStore:    &sqliteTestVectorStore{},
		EmbeddingModel: "text-embedding-test",
	}))
	require.EqualError(t, store.RebuildVectorStore(context.Background(), testMissingSession), "session not found")
}

func TestSQLiteStore_VectorStoreMutationDatabaseErrors(t *testing.T) {
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	t.Run("delete returns message id query error", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.db.Exec(`DROP TABLE session_messages`).Error)

		err = store.Delete(context.Background(), testSessionA)
		require.Error(t, err)
	})

	t.Run("clear returns message delete error", func(t *testing.T) {
		store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
		require.NoError(t, err)
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
			{Role: morphmsg.RoleUser, Content: "clear db error", CreatedAt: now},
		}))

		deleteErr := errors.New("delete message failed")
		err = store.db.Callback().Delete().
			Before("gorm:delete").
			Register("test:delete_message_error", func(tx *gorm.DB) {
				if tx.Statement != nil && tx.Statement.Schema != nil && tx.Statement.Schema.Table == "session_messages" {
					_ = tx.AddError(deleteErr)
				}
			})
		require.NoError(t, err)

		require.ErrorIs(t, store.ClearMessages(context.Background(), testSessionA), deleteErr)
	})
}

func TestSQLiteStore_VectorStorePostMutationErrors(t *testing.T) {
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	deleteErr := errors.New("delete failed")

	t.Run("delete returns required vector delete error", func(t *testing.T) {
		store, vectorStore := sqliteVectorStoreTestStore(t)
		require.NoError(t, store.ConfigureVectorStore(VectorStoreOptions{
			Embedder:       &sqliteTestEmbeddingProvider{dimensions: 3},
			VectorStore:    vectorStore,
			EmbeddingModel: "text-embedding-test",
			Required:       true,
		}))
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
			{Role: morphmsg.RoleUser, Content: "delete vector", CreatedAt: now},
		}))

		vectorStore.deleteErr = deleteErr
		require.ErrorIs(t, store.Delete(context.Background(), testSessionA), deleteErr)
	})

	t.Run("clear returns required vector delete error", func(t *testing.T) {
		store, vectorStore := sqliteVectorStoreTestStore(t)
		require.NoError(t, store.ConfigureVectorStore(VectorStoreOptions{
			Embedder:       &sqliteTestEmbeddingProvider{dimensions: 3},
			VectorStore:    vectorStore,
			EmbeddingModel: "text-embedding-test",
			Required:       true,
		}))
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
		require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
			{Role: morphmsg.RoleUser, Content: "clear vector", CreatedAt: now},
		}))

		vectorStore.deleteErr = deleteErr
		require.ErrorIs(t, store.ClearMessages(context.Background(), testSessionA), deleteErr)
	})
}

func TestSQLiteStore_VectorStoreHelperBranches(t *testing.T) {
	require.Nil(t, searchRows(nil).vectorInputs(search.VectorChunkOptions{}))
	require.Nil(t, messageModels(nil).sourceIDs())
	require.Nil(t, getUniqueStrings(nil))
	require.Equal(t, []string{"one", "two"}, getUniqueStrings([]string{" one ", "", "two", "one"}))

	store, vectorStore := sqliteVectorStoreTestStore(t)
	require.NoError(t, store.deleteVectorRows(context.Background(), []string{" ", ""}))
	require.Empty(t, vectorStore.deletes)
}

func TestSQLiteStore_VectorStoreSkipsEmptySearchRows(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	provider := &sqliteTestEmbeddingProvider{dimensions: 3}
	require.NoError(t, store.ConfigureVectorStore(VectorStoreOptions{
		Embedder:       provider,
		VectorStore:    &sqliteTestVectorStore{},
		EmbeddingModel: "text-embedding-test",
	}))

	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, CreatedAt: now},
	}))
	require.Empty(t, provider.requests)
}

func TestSQLiteStore_VectorStoreRejectsInvalidEmbeddings(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	provider := &sqliteTestEmbeddingProvider{
		mutate: func(result search.EmbeddingResult) search.EmbeddingResult {
			result.Items[0].ContentHash = "wrong"
			return result
		},
		dimensions: 3,
	}
	require.NoError(t, store.ConfigureVectorStore(VectorStoreOptions{
		Embedder:       provider,
		VectorStore:    &sqliteTestVectorStore{},
		EmbeddingModel: "text-embedding-test",
		Required:       true,
	}))

	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA, UpdatedAt: now}))
	err = store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "bad embedding", CreatedAt: now},
	})
	require.NoError(t, err)
}

func sqliteVectorStoreTestStore(t *testing.T) (*Store, *sqliteTestVectorStore) {
	t.Helper()

	return sqliteVectorStoreTestStoreWithReranker(t, nil, 0)
}

func sqliteVectorStoreTestStoreWithReranker(
	t *testing.T,
	reranker search.Reranker,
	rerankMaxCandidates int,
) (*Store, *sqliteTestVectorStore) {
	t.Helper()

	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)
	vectorStore := &sqliteTestVectorStore{}
	require.NoError(t, store.ConfigureVectorStore(VectorStoreOptions{
		Embedder:            &sqliteTestEmbeddingProvider{dimensions: 3},
		Reranker:            reranker,
		VectorStore:         vectorStore,
		EmbeddingModel:      "text-embedding-test",
		RerankMaxCandidates: rerankMaxCandidates,
	}))

	return store, vectorStore
}

func sqliteTestRecordIDs(records []search.VectorRecord) []string {
	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.ID)
	}

	return ids
}

func sqliteSearchMatchedTexts(hits []base.SearchMessageHit) []string {
	texts := make([]string, 0, len(hits))
	for _, hit := range hits {
		texts = append(texts, hit.MatchedText)
	}

	return texts
}

type sqliteTestEmbeddingProvider struct {
	err        error
	afterEmbed func()
	mutate     func(search.EmbeddingResult) search.EmbeddingResult
	requests   []search.EmbeddingRequest
	dimensions int
}

func (p *sqliteTestEmbeddingProvider) Embed(_ context.Context, req search.EmbeddingRequest) (search.EmbeddingResult, error) {
	p.requests = append(p.requests, req)
	if p.afterEmbed != nil {
		p.afterEmbed()
	}
	if p.err != nil {
		return search.EmbeddingResult{}, p.err
	}
	if p.dimensions == 0 {
		p.dimensions = 3
	}

	items := make([]search.Embedding, 0, len(req.Inputs))
	for _, input := range req.Inputs {
		vector := make([]float64, p.dimensions)
		for idx := range vector {
			vector[idx] = float64(len(input.Text) + idx)
		}
		items = append(items, search.Embedding{
			ID:          input.ID,
			ContentHash: search.VectorContentHash(input.Text),
			Vector:      vector,
		})
	}

	result := search.EmbeddingResult{
		Model:      req.Model,
		Dimensions: p.dimensions,
		Items:      items,
	}
	if p.mutate != nil {
		result = p.mutate(result)
	}

	return result, nil
}

type sqliteTestReranker struct {
	err      error
	rerank   func(search.RerankRequest) search.RerankResult
	result   search.RerankResult
	requests []search.RerankRequest
}

func (r *sqliteTestReranker) Name() string {
	return "test"
}

func (r *sqliteTestReranker) Rerank(_ context.Context, req search.RerankRequest) (search.RerankResult, error) {
	r.requests = append(r.requests, req)
	if r.err != nil {
		return search.RerankResult{}, r.err
	}
	if r.rerank != nil {
		return r.rerank(req), nil
	}

	return r.result, nil
}

type sqliteTestVectorStore struct {
	err           error
	deleteErr     error
	listErr       error
	searchErr     error
	upsertErr     error
	searchMatches []search.VectorSearchMatch
	searches      []search.VectorSearchRequest
	lists         []search.VectorListRequest
	upserts       [][]search.VectorRecord
	deletes       []search.VectorDeleteRequest
	records       map[string]search.VectorRecord
}

func (s *sqliteTestVectorStore) Upsert(_ context.Context, records []search.VectorRecord) error {
	if s.upsertErr != nil {
		return s.upsertErr
	}
	if s.err != nil {
		return s.err
	}

	s.upserts = append(s.upserts, append([]search.VectorRecord(nil), records...))
	if s.records == nil {
		s.records = make(map[string]search.VectorRecord, len(records))
	}
	for _, record := range records {
		s.records[record.ID] = record
	}

	return nil
}

func (s *sqliteTestVectorStore) Delete(_ context.Context, req search.VectorDeleteRequest) error {
	s.deletes = append(s.deletes, req)
	if s.deleteErr != nil {
		return s.deleteErr
	}
	if s.err != nil {
		return s.err
	}
	sourceIDs := make(map[string]struct{}, len(req.SourceIDs))
	for _, sourceID := range req.SourceIDs {
		sourceIDs[sourceID] = struct{}{}
	}
	for id, record := range s.records {
		if record.SourceKind != req.SourceKind {
			continue
		}

		if _, ok := sourceIDs[record.SourceID]; ok {
			delete(s.records, id)
		}
	}

	return nil
}

func (s *sqliteTestVectorStore) Search(_ context.Context, req search.VectorSearchRequest) (search.VectorSearchResult, error) {
	s.searches = append(s.searches, req)
	if s.searchErr != nil {
		return search.VectorSearchResult{}, s.searchErr
	}
	if s.err != nil {
		return search.VectorSearchResult{}, s.err
	}

	return search.VectorSearchResult{Matches: append([]search.VectorSearchMatch(nil), s.searchMatches...)}, nil
}

func (s *sqliteTestVectorStore) List(_ context.Context, req search.VectorListRequest) (search.VectorListResult, error) {
	s.lists = append(s.lists, req)
	if err := search.ValidateVectorListRequest(req); err != nil {
		return search.VectorListResult{}, err
	}
	if s.listErr != nil {
		return search.VectorListResult{}, s.listErr
	}
	if s.err != nil {
		return search.VectorListResult{}, s.err
	}

	sourceIDs := make(map[string]struct{}, len(req.Filter.SourceIDs))
	for _, sourceID := range req.Filter.SourceIDs {
		sourceIDs[sourceID] = struct{}{}
	}

	records := make([]search.VectorRecord, 0, len(s.records))
	for _, record := range s.records {
		if record.EmbeddingModel != req.EmbeddingModel {
			continue
		}
		if req.Filter.SourceKind != "" && record.SourceKind != req.Filter.SourceKind {
			continue
		}

		if len(sourceIDs) > 0 {
			if _, ok := sourceIDs[record.SourceID]; !ok {
				continue
			}
		}
		if req.Filter.SessionID != "" && record.SessionID != req.Filter.SessionID {
			continue
		}
		if req.Filter.IgnoreSessionID != "" && record.SessionID == req.Filter.IgnoreSessionID {
			continue
		}
		if req.Filter.Role != "" && record.Role != req.Filter.Role {
			continue
		}
		if req.Filter.ToolName != "" && record.ToolName != req.Filter.ToolName {
			continue
		}
		records = append(records, record)
	}
	sort.SliceStable(records, func(i, j int) bool {
		return records[i].ID < records[j].ID
	})

	return search.VectorListResult{Records: records}, nil
}

func (s *sqliteTestVectorStore) Metadata(context.Context) (search.VectorStoreMetadata, error) {
	return search.VectorStoreMetadata{}, nil
}
