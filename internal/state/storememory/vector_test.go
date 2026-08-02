package storememory

import (
	"context"
	"errors"
	"io"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	base "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/internal/state/search"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/logutils"
)

func init() {
	logutils.SetOutput(io.Discard)
}

func TestStore_ConfigureVectorStore(t *testing.T) {
	t.Run("rejects nil store", func(t *testing.T) {
		var nilStore *Store
		require.EqualError(t, nilStore.ConfigureVectorStore(search.VectorStoreOptions{}), "store is required")
		require.False(t, nilStore.SupportsVectorSearch())
	})

	t.Run("clears vector config when options are empty", func(t *testing.T) {
		store := NewStore()
		require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{}))
		require.Nil(t, store.vectors)
		require.False(t, store.SupportsVectorSearch())
	})

	t.Run("requires embedder", func(t *testing.T) {
		store := NewStore()
		require.EqualError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
			VectorStore:    &memoryTestVectorStore{},
			EmbeddingModel: "model",
		}), "vector store embedding provider is required")
	})

	t.Run("requires vector store", func(t *testing.T) {
		store := NewStore()
		require.EqualError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
			Embedder:       semanticTestEmbedder{},
			EmbeddingModel: "model",
		}), "vector store is required")
	})

	t.Run("requires embedding model", func(t *testing.T) {
		store := NewStore()
		require.EqualError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
			Embedder:    semanticTestEmbedder{},
			VectorStore: &memoryTestVectorStore{},
		}), "vector store embedding model is required")
	})

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
			store := NewStore()
			require.EqualError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
				Embedder:         semanticTestEmbedder{},
				VectorStore:      &memoryTestVectorStore{},
				EmbeddingModel:   "model",
				MaxInputBytes:    test.maxInputBytes,
				MaxDocumentBytes: test.maxDocumentBytes,
			}), test.err)
		})
	}

	t.Run("rejects negative rerank candidate limit", func(t *testing.T) {
		store := NewStore()
		require.EqualError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
			Embedder:            semanticTestEmbedder{},
			VectorStore:         &memoryTestVectorStore{},
			EmbeddingModel:      "model",
			RerankMaxCandidates: -1,
		}), "vector store rerank max candidates must be greater than or equal to zero")
	})

	t.Run("rejects invalid reranker", func(t *testing.T) {
		store := NewStore()
		require.EqualError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
			Embedder:       semanticTestEmbedder{},
			Reranker:       invalidNameReranker{},
			VectorStore:    &memoryTestVectorStore{},
			EmbeddingModel: "model",
		}), "reranker must be one of: noop, deterministic, llm")
	})

	t.Run("reports vector support when configured", func(t *testing.T) {
		store := NewStore()
		require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
			Embedder:       semanticTestEmbedder{},
			VectorStore:    &memoryTestVectorStore{},
			EmbeddingModel: "model",
		}))
		require.True(t, store.SupportsVectorSearch())
	})
}

func TestStore_SearchMessages(t *testing.T) {
	t.Run("returns embedder errors", func(t *testing.T) {
		store := NewStore()
		require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
			Embedder:       failingEmbedder{err: errors.New("embed failed")},
			VectorStore:    &memoryTestVectorStore{},
			EmbeddingModel: "model",
		}))

		results, err := store.SearchMessages(context.Background(), testSessionA, base.SearchMessageOptions{
			Query: "needle",
		})
		require.EqualError(t, err, "embed failed")
		require.Nil(t, results)
	})

	t.Run("returns embedding validation errors", func(t *testing.T) {
		store := NewStore()
		require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
			Embedder:       malformedEmbedder{},
			VectorStore:    &memoryTestVectorStore{},
			EmbeddingModel: "model",
		}))

		results, err := store.SearchMessages(context.Background(), testSessionA, base.SearchMessageOptions{
			Query: "needle",
		})
		require.EqualError(t, err, "embedding result count must match input count")
		require.Nil(t, results)
	})

	t.Run("returns vector store errors", func(t *testing.T) {
		store := NewStore()
		require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
			Embedder:       semanticTestEmbedder{},
			VectorStore:    &memoryTestVectorStore{searchErr: errors.New("search failed")},
			EmbeddingModel: "semantic-test",
		}))

		results, err := store.SearchMessages(context.Background(), testSessionA, base.SearchMessageOptions{
			Query: "needle",
		})
		require.EqualError(t, err, "search failed")
		require.Nil(t, results)
	})
}

func TestStore_AppendMessages(t *testing.T) {
	t.Run("tracks ready and skipped semantic sources independently", func(t *testing.T) {
		store := NewStore()
		vectorStore := &memoryTestVectorStore{}
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
		require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
			Embedder:       semanticTestEmbedder{},
			VectorStore:    vectorStore,
			EmbeddingModel: "semantic-test",
		}))

		require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
			{Role: morphmsg.RoleUser, Content: "indexed prose"},
			{Role: morphmsg.RoleTool, Name: "plan_tool", Content: `{"status":"complete"}`},
		}))

		store.mu.RLock()
		ready := store.vectorStates[search.SourceIDForMessage(testSessionA, 1)]
		skipped := store.vectorStates[search.SourceIDForMessage(testSessionA, 2)]
		store.mu.RUnlock()
		require.Equal(t, search.VectorIndexReady, ready.Status)
		require.Equal(t, 1, ready.Attempts)
		require.Equal(t, search.VectorIndexSkipped, skipped.Status)
		require.Zero(t, skipped.Attempts)
	})

	t.Run("does not fail persisted messages when required vector upsert fails", func(t *testing.T) {
		store := NewStore()
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
		require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
			Embedder:       semanticTestEmbedder{},
			VectorStore:    &memoryTestVectorStore{upsertErr: errors.New("upsert failed")},
			EmbeddingModel: "semantic-test",
			Required:       true,
		}))

		err := store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{
			Role:    morphmsg.RoleUser,
			Content: "needle",
		}})
		require.NoError(t, err)
		store.mu.RLock()
		state := store.vectorStates[search.SourceIDForMessage(testSessionA, 1)]
		store.mu.RUnlock()
		require.Equal(t, search.VectorIndexFailed, state.Status)
		require.Equal(t, 1, state.Attempts)
	})

	t.Run("marks a source failed when one rune exceeds the byte limit", func(t *testing.T) {
		store := NewStore()
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
		require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
			Embedder:         semanticTestEmbedder{},
			VectorStore:      &memoryTestVectorStore{},
			EmbeddingModel:   "semantic-test",
			MaxInputBytes:    1,
			MaxDocumentBytes: 1,
		}))

		require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{
			Role:    morphmsg.RoleUser,
			Content: "🙂",
		}}))

		store.mu.RLock()
		defer store.mu.RUnlock()
		state := store.vectorStates[search.SourceIDForMessage(testSessionA, 1)]
		require.Equal(t, search.VectorIndexFailed, state.Status)
		require.Equal(t, 1, state.Attempts)
	})
}

func TestStore_GetRetryableVectorSourceIDsHandlesEmptyInputs(t *testing.T) {
	require.Nil(t, (*Store)(nil).getRetryableVectorSourceIDs([]search.VectorInput{{SourceID: "source"}}))
	require.Nil(t, NewStore().getRetryableVectorSourceIDs(nil))
}

func TestStore_ClearMessages(t *testing.T) {
	t.Run("returns required vector delete errors", func(t *testing.T) {
		store := NewStore()
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
		require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
			Embedder:       semanticTestEmbedder{},
			VectorStore:    &memoryTestVectorStore{deleteErr: errors.New("delete failed")},
			EmbeddingModel: "semantic-test",
			Required:       true,
		}))
		require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{
			Role:    morphmsg.RoleUser,
			Content: "needle",
		}}))

		err := store.ClearMessages(context.Background(), testSessionA)
		require.EqualError(t, err, "delete failed")

		store.mu.RLock()
		defer store.mu.RUnlock()
		require.Empty(t, store.vectorStates)
		require.Empty(t, store.messages[testSessionA])
	})
}

func TestFindMessageByID(t *testing.T) {
	t.Run("returns false when id is missing", func(t *testing.T) {
		_, ok := getMessageByID([]morphmsg.Message{{ID: 1}}, 2)
		require.False(t, ok)
	})
}

func TestMessageMatchesSearchOptions(t *testing.T) {
	t.Run("rejects mismatched session id", func(t *testing.T) {
		require.False(t, checkMessageMatchesSearchOptions(testSessionA, morphmsg.Message{Role: morphmsg.RoleUser}, testSessionB, base.SearchMessageOptions{}))
	})

	t.Run("rejects ignored session id", func(t *testing.T) {
		require.False(t, checkMessageMatchesSearchOptions(testSessionA, morphmsg.Message{Role: morphmsg.RoleUser}, "", base.SearchMessageOptions{IgnoreSessionID: testSessionA}))
	})

	t.Run("rejects mismatched role", func(t *testing.T) {
		require.False(t, checkMessageMatchesSearchOptions(testSessionA, morphmsg.Message{Role: morphmsg.RoleUser}, "", base.SearchMessageOptions{Role: morphmsg.RoleAssistant}))
	})
}

func TestLexicalScore(t *testing.T) {
	t.Run("returns zero when query is missing", func(t *testing.T) {
		require.Equal(t, float64(0), getLexicalScore("body", "missing"))
	})
}

func TestSearchRerankResultName(t *testing.T) {
	t.Run("uses fallback when result name is empty", func(t *testing.T) {
		require.Equal(t, "fallback", getSearchRerankResultName(search.RerankResult{}, "fallback"))
	})
}

func TestStore_RerankerName(t *testing.T) {
	t.Run("defaults to deterministic", func(t *testing.T) {
		require.Equal(t, search.RerankerDeterministic, (*Store)(nil).rerankerName())
	})
}

func TestStore_DiagnosticsEnabled(t *testing.T) {
	t.Run("defaults to false", func(t *testing.T) {
		require.False(t, (*Store)(nil).diagnosticsEnabled())
	})
}

func TestSearchResultsFromCandidates(t *testing.T) {
	now := time.Now().UTC()
	older := now.Add(-time.Minute)

	t.Run("applies result limits", func(t *testing.T) {
		results := searchCandidatesToSearchResults([]*searchCandidate{{
			CandidateMatch: search.CandidateMatch{
				SessionID:   testSessionA,
				VectorRank:  1,
				HasVector:   true,
				VectorScore: 1,
			},
			Message: morphmsg.Message{ID: 1, Role: morphmsg.RoleUser, Content: "body a", CreatedAt: now},
		}, {
			CandidateMatch: search.CandidateMatch{
				SessionID:   testSessionB,
				VectorRank:  2,
				HasVector:   true,
				VectorScore: 0.8,
			},
			Message: morphmsg.Message{ID: 2, Role: morphmsg.RoleUser, Content: "body b", CreatedAt: older},
		}}, base.SearchMessageOptions{
			MaxSessions:           1,
			MaxMessagesPerSession: 1,
		})
		require.Len(t, results, 1)
		require.Equal(t, testSessionA, results[0].SessionID)
		require.Equal(t, 1, results[0].MatchCount)
	})

	t.Run("result ordering prefers newest session match when scores tie", func(t *testing.T) {
		results := searchCandidatesToSearchResults([]*searchCandidate{{
			CandidateMatch: search.CandidateMatch{SessionID: testSessionB, FusedScore: 1},
			Message:        morphmsg.Message{ID: 1, Role: morphmsg.RoleUser, Content: "newer", CreatedAt: now},
		}, {
			CandidateMatch: search.CandidateMatch{SessionID: testSessionA, FusedScore: 1},
			Message:        morphmsg.Message{ID: 1, Role: morphmsg.RoleUser, Content: "older", CreatedAt: older},
		}}, base.SearchMessageOptions{})
		require.Equal(t, testSessionB, results[0].SessionID)
	})

	t.Run("result ordering uses session id as final tie break", func(t *testing.T) {
		results := searchCandidatesToSearchResults([]*searchCandidate{{
			CandidateMatch: search.CandidateMatch{SessionID: testSessionA, FusedScore: 1},
			Message:        morphmsg.Message{ID: 1, Role: morphmsg.RoleUser, Content: "a", CreatedAt: now},
		}, {
			CandidateMatch: search.CandidateMatch{SessionID: testSessionB, FusedScore: 1},
			Message:        morphmsg.Message{ID: 1, Role: morphmsg.RoleUser, Content: "b", CreatedAt: now},
		}}, base.SearchMessageOptions{})
		require.Equal(t, testSessionA, results[0].SessionID)
	})

	t.Run("message limit preserves total match count", func(t *testing.T) {
		results := searchCandidatesToSearchResults([]*searchCandidate{{
			CandidateMatch: search.CandidateMatch{SessionID: testSessionA, FusedScore: 2},
			Message:        morphmsg.Message{ID: 1, Role: morphmsg.RoleUser, Content: "newer", CreatedAt: now},
		}, {
			CandidateMatch: search.CandidateMatch{SessionID: testSessionA, FusedScore: 1},
			Message:        morphmsg.Message{ID: 2, Role: morphmsg.RoleUser, Content: "older", CreatedAt: older},
		}}, base.SearchMessageOptions{MaxMessagesPerSession: 1})
		require.Len(t, results[0].Messages, 1)
		require.Equal(t, 2, results[0].MatchCount)
	})

}

func TestCompareSearchCandidates(t *testing.T) {
	now := time.Now().UTC()
	older := now.Add(-time.Minute)

	t.Run("orders message ids descending", func(t *testing.T) {
		left := &searchCandidate{CandidateMatch: search.CandidateMatch{SessionID: testSessionA}, Message: morphmsg.Message{ID: 1, CreatedAt: now}}
		right := &searchCandidate{CandidateMatch: search.CandidateMatch{SessionID: testSessionA}, Message: morphmsg.Message{ID: 2, CreatedAt: now}}
		require.Greater(t, compareSearchCandidates(left, right), 0)
		require.Less(t, compareSearchCandidates(right, left), 0)
	})

	t.Run("covers score time session and equality ties", func(t *testing.T) {
		require.Equal(t, 0, compareSearchCandidates(
			&searchCandidate{CandidateMatch: search.CandidateMatch{SessionID: testSessionA}, Message: morphmsg.Message{ID: 1, CreatedAt: now}},
			&searchCandidate{CandidateMatch: search.CandidateMatch{SessionID: testSessionA}, Message: morphmsg.Message{ID: 1, CreatedAt: now}},
		))
		require.Less(t, compareSearchCandidates(
			&searchCandidate{CandidateMatch: search.CandidateMatch{SessionID: testSessionA, FusedScore: 2}, Message: morphmsg.Message{ID: 1, CreatedAt: now}},
			&searchCandidate{CandidateMatch: search.CandidateMatch{SessionID: testSessionA, FusedScore: 1}, Message: morphmsg.Message{ID: 2, CreatedAt: now}},
		), 0)
		require.Greater(t, compareSearchCandidates(
			&searchCandidate{CandidateMatch: search.CandidateMatch{SessionID: testSessionA, FusedScore: 1}, Message: morphmsg.Message{ID: 1, CreatedAt: now}},
			&searchCandidate{CandidateMatch: search.CandidateMatch{SessionID: testSessionA, FusedScore: 2}, Message: morphmsg.Message{ID: 2, CreatedAt: now}},
		), 0)
		require.Greater(t, compareSearchCandidates(
			&searchCandidate{CandidateMatch: search.CandidateMatch{SessionID: testSessionA}, Message: morphmsg.Message{ID: 1, CreatedAt: older}},
			&searchCandidate{CandidateMatch: search.CandidateMatch{SessionID: testSessionA}, Message: morphmsg.Message{ID: 2, CreatedAt: now}},
		), 0)
		require.Less(t, compareSearchCandidates(
			&searchCandidate{CandidateMatch: search.CandidateMatch{SessionID: testSessionA}, Message: morphmsg.Message{ID: 1, CreatedAt: now}},
			&searchCandidate{CandidateMatch: search.CandidateMatch{SessionID: testSessionA}, Message: morphmsg.Message{ID: 2, CreatedAt: older}},
		), 0)
		require.Less(t, compareSearchCandidates(
			&searchCandidate{CandidateMatch: search.CandidateMatch{SessionID: testSessionA}, Message: morphmsg.Message{ID: 1, CreatedAt: now}},
			&searchCandidate{CandidateMatch: search.CandidateMatch{SessionID: testSessionB}, Message: morphmsg.Message{ID: 1, CreatedAt: now}},
		), 0)
		require.Greater(t, compareSearchCandidates(
			&searchCandidate{CandidateMatch: search.CandidateMatch{SessionID: testSessionB}, Message: morphmsg.Message{ID: 1, CreatedAt: now}},
			&searchCandidate{CandidateMatch: search.CandidateMatch{SessionID: testSessionA}, Message: morphmsg.Message{ID: 1, CreatedAt: now}},
		), 0)
	})
}

func TestRetrievalCandidateFromSearchCandidate(t *testing.T) {
	t.Run("falls back to message content", func(t *testing.T) {
		candidate := searchCandidateToRetrievalCandidate(&searchCandidate{
			CandidateMatch: search.CandidateMatch{SessionID: testSessionA},
			Message:        morphmsg.Message{ID: 3, Role: morphmsg.RoleUser, Content: "fallback text", CreatedAt: time.Now().UTC()},
		})
		require.Equal(t, "fallback text", candidate.Text)
	})
}

func TestStore_SearchMessagesLexicalCandidates(t *testing.T) {
	now := time.Now().UTC()

	t.Run("session scoped lexical candidates respect limit", func(t *testing.T) {
		store := NewStore()
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionB}))
		require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
			{ID: 1, Role: morphmsg.RoleUser, Content: "needle one", CreatedAt: now},
			{ID: 2, Role: morphmsg.RoleUser, Content: "needle two", CreatedAt: now},
		}))
		require.NoError(t, store.AppendMessages(context.Background(), testSessionB, []morphmsg.Message{
			{ID: 3, Role: morphmsg.RoleUser, Content: "needle three", CreatedAt: now},
		}))

		candidates := store.searchMessagesLexicalCandidates(testSessionA, base.SearchMessageOptions{Query: "needle"}, "needle", 1)
		require.Len(t, candidates, 1)
		require.Contains(t, candidates, search.SourceIDForMessage(testSessionA, 2))
	})

	t.Run("cross session lexical candidates respect limit", func(t *testing.T) {
		store := NewStore()
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionB}))
		require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
			{ID: 1, Role: morphmsg.RoleUser, Content: "needle one", CreatedAt: now},
			{ID: 2, Role: morphmsg.RoleUser, Content: "needle two", CreatedAt: now},
		}))
		require.NoError(t, store.AppendMessages(context.Background(), testSessionB, []morphmsg.Message{
			{ID: 3, Role: morphmsg.RoleUser, Content: "needle three", CreatedAt: now},
		}))

		candidates := store.searchMessagesLexicalCandidates("", base.SearchMessageOptions{Query: "needle"}, "needle", 1)
		require.Len(t, candidates, 1)

		candidates = store.searchMessagesLexicalCandidates("", base.SearchMessageOptions{Query: "needle"}, "needle", 2)
		require.Len(t, candidates, 2)
	})

	t.Run("lexical candidates prefer newer messages", func(t *testing.T) {
		store := NewStore()
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
		require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{
			{ID: 1, Role: morphmsg.RoleUser, Content: "needle older", CreatedAt: now.Add(-time.Minute)},
			{ID: 2, Role: morphmsg.RoleUser, Content: "needle newer", CreatedAt: now},
		}))

		candidates := store.searchMessagesLexicalCandidates(testSessionA, base.SearchMessageOptions{Query: "needle"}, "needle", 1)
		require.Len(t, candidates, 1)
		require.Contains(t, candidates, search.SourceIDForMessage(testSessionA, 2))
		require.NotContains(t, candidates, search.SourceIDForMessage(testSessionA, 1))
	})
}

func TestStore_VectorMatchesToCandidates(t *testing.T) {
	now := time.Now().UTC()
	newStore := func(t *testing.T) *Store {
		t.Helper()

		store := NewStore()
		require.NoError(t, store.Save(context.Background(), Session{ID: testSessionA}))
		require.NoError(t, store.AppendMessages(context.Background(), testSessionA, []morphmsg.Message{{
			ID:        1,
			Role:      morphmsg.RoleUser,
			Content:   "body",
			CreatedAt: now,
		}}))

		return store
	}

	t.Run("skips malformed source ids", func(t *testing.T) {
		candidates := newStore(t).vectorMatchesToCandidates(testSessionA, base.SearchMessageOptions{}, []search.VectorSearchMatch{
			{Record: search.VectorRecord{SourceID: "invalid"}},
		})
		require.Empty(t, candidates)
	})

	t.Run("skips missing messages", func(t *testing.T) {
		candidates := newStore(t).vectorMatchesToCandidates(testSessionA, base.SearchMessageOptions{}, []search.VectorSearchMatch{{
			Record: search.VectorRecord{
				ID:       search.StableSessionMessageID(testSessionA, 99) + ":row:1:chunk:1",
				SourceID: search.StableSessionMessageID(testSessionA, 99),
			},
		}})
		require.Empty(t, candidates)
	})

	t.Run("skips invalid vector row ids", func(t *testing.T) {
		candidates := newStore(t).vectorMatchesToCandidates(testSessionA, base.SearchMessageOptions{}, []search.VectorSearchMatch{{
			Record: search.VectorRecord{
				ID:       search.StableSessionMessageID(testSessionA, 1) + ":row:99:chunk:1",
				SourceID: search.StableSessionMessageID(testSessionA, 1),
			},
		}})
		require.Empty(t, candidates)
	})

	t.Run("skips stale vector records", func(t *testing.T) {
		candidates := newStore(t).vectorMatchesToCandidates(testSessionA, base.SearchMessageOptions{}, []search.VectorSearchMatch{{
			Record: search.VectorRecord{
				ID:          search.StableSessionMessageID(testSessionA, 1) + ":row:1:chunk:1",
				SourceID:    search.StableSessionMessageID(testSessionA, 1),
				ContentHash: search.VectorContentHash("different body"),
			},
		}})
		require.Empty(t, candidates)
	})

	t.Run("deduplicates repeated message matches", func(t *testing.T) {
		candidates := newStore(t).vectorMatchesToCandidates(testSessionA, base.SearchMessageOptions{}, []search.VectorSearchMatch{
			{Record: search.VectorRecord{
				ID:          search.StableSessionMessageID(testSessionA, 1) + ":row:1:chunk:1",
				SourceID:    search.StableSessionMessageID(testSessionA, 1),
				ContentHash: search.VectorContentHash("body"),
			}},
			{Record: search.VectorRecord{
				ID:          search.StableSessionMessageID(testSessionA, 1) + ":row:1:chunk:1",
				SourceID:    search.StableSessionMessageID(testSessionA, 1),
				ContentHash: search.VectorContentHash("body"),
			}},
		})
		require.Len(t, candidates, 1)
	})
}

func TestStore_IndexVectors(t *testing.T) {
	t.Run("skips empty inputs", func(t *testing.T) {
		store := NewStore()
		require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
			Embedder:       failingEmbedder{err: errors.New("should not embed")},
			VectorStore:    &memoryTestVectorStore{},
			EmbeddingModel: "model",
			Required:       true,
		}))

		require.NoError(t, store.indexVectors(context.Background(), testSessionA, []morphmsg.Message{{
			Role: morphmsg.RoleUser,
		}}))
		require.NoError(t, (*Store)(nil).indexVectors(context.Background(), testSessionA, []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "body"}}))
	})

	t.Run("returns embedder errors", func(t *testing.T) {
		store := NewStore()
		require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
			Embedder:       failingEmbedder{err: errors.New("embed failed")},
			VectorStore:    &memoryTestVectorStore{},
			EmbeddingModel: "model",
			Required:       true,
		}))

		require.EqualError(t, store.indexVectors(context.Background(), testSessionA, []morphmsg.Message{{
			Role:    morphmsg.RoleUser,
			Content: "body",
		}}), "embed failed")
	})

	t.Run("returns embedding validation errors", func(t *testing.T) {
		store := NewStore()
		require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
			Embedder:       malformedEmbedder{},
			VectorStore:    &memoryTestVectorStore{},
			EmbeddingModel: "model",
			Required:       true,
		}))

		require.EqualError(t, store.indexVectors(context.Background(), testSessionA, []morphmsg.Message{{
			Role:    morphmsg.RoleUser,
			Content: "body",
		}}), "embedding result count must match input count")
	})
}

func TestStore_DeleteVectorRows(t *testing.T) {
	t.Run("skips empty inputs", func(t *testing.T) {
		store := NewStore()
		require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
			Embedder:       semanticTestEmbedder{},
			VectorStore:    &memoryTestVectorStore{},
			EmbeddingModel: "semantic-test",
			Required:       true,
		}))

		require.NoError(t, store.deleteVectorRows(context.Background(), nil))
		require.NoError(t, store.deleteVectorRows(context.Background(), []string{" ", ""}))
		require.NoError(t, (*Store)(nil).deleteVectorRows(context.Background(), []string{"x"}))
	})
}

func TestStore_UpsertVectorRecordsSkipsEmptyInputs(t *testing.T) {
	require.NoError(t, (*Store)(nil).upsertVectorRecords(context.Background(), []search.VectorRecord{{
		ID: "row",
	}}))

	store := NewStore()
	require.NoError(t, store.upsertVectorRecords(context.Background(), []search.VectorRecord{{ID: "row"}}))

	require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
		Embedder:       semanticTestEmbedder{},
		VectorStore:    &memoryTestVectorStore{},
		EmbeddingModel: "semantic-test",
		Required:       true,
	}))
	require.NoError(t, store.upsertVectorRecords(context.Background(), nil))
}

func TestSearchCandidateSet_Merge(t *testing.T) {
	t.Run("ignores nil candidates and adds vector only candidates", func(t *testing.T) {
		candidates := searchCandidateSet{}
		require.Empty(t, getSearchCandidateKey(nil))
		candidates.Merge([]*searchCandidate{nil, {
			CandidateMatch: search.CandidateMatch{SessionID: testSessionA},
			Message:        morphmsg.Message{ID: 1, Role: morphmsg.RoleUser, Content: "vector"},
		}}, getSearchCandidateKey)
		require.Len(t, candidates, 1)
	})

	t.Run("adds vector evidence to lexical candidate", func(t *testing.T) {
		candidates := searchCandidateSet{
			search.SourceIDForMessage(testSessionA, 1): {
				CandidateMatch: search.CandidateMatch{SessionID: testSessionA},
				Message:        morphmsg.Message{ID: 1, Role: morphmsg.RoleUser, Content: "lexical"},
			},
		}
		candidates.Merge([]*searchCandidate{{
			CandidateMatch: search.CandidateMatch{
				SessionID:       testSessionA,
				MatchedText:     "vector text",
				MatchedToolName: "tool",
				VectorRank:      1,
				HasVector:       true,
			},
			Message: morphmsg.Message{ID: 1, Role: morphmsg.RoleUser, Content: "vector"},
		}}, getSearchCandidateKey)
		require.Equal(t, "vector text", candidates[search.SourceIDForMessage(testSessionA, 1)].MatchedText)
	})
}

func TestSearchCandidatesToSearchResults_LimitsSessions(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	results := searchCandidatesToSearchResults([]*searchCandidate{
		{
			CandidateMatch: search.CandidateMatch{SessionID: testSessionA, FusedScore: 0.9},
			Message:        morphmsg.Message{ID: 1, Role: morphmsg.RoleUser, Content: "first", CreatedAt: now},
		},
		{
			CandidateMatch: search.CandidateMatch{SessionID: testSessionB, FusedScore: 0.8},
			Message:        morphmsg.Message{ID: 1, Role: morphmsg.RoleUser, Content: "second", CreatedAt: now.Add(time.Second)},
		},
	}, base.SearchMessageOptions{MaxSessions: 1})

	require.Len(t, results, 1)
	require.Equal(t, testSessionA, results[0].SessionID)
}

func TestStore_RerankSearchCandidates(t *testing.T) {
	t.Run("falls back when configured reranker fails", func(t *testing.T) {
		candidates := searchCandidateSet{
			search.SourceIDForMessage(testSessionA, 1): {
				CandidateMatch: search.CandidateMatch{SessionID: testSessionA},
				Message:        morphmsg.Message{ID: 1, Role: morphmsg.RoleUser, Content: "lexical"},
			},
		}
		store := NewStore()
		require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
			Embedder:       semanticTestEmbedder{},
			Reranker:       failingReranker{},
			VectorStore:    &memoryTestVectorStore{},
			EmbeddingModel: "semantic-test",
		}))

		items := store.rerankSearchCandidates(context.Background(), base.SearchMessageOptions{Query: "needle"}, candidates)
		require.Len(t, items, 1)
	})

	t.Run("defaults to deterministic and respects max candidates", func(t *testing.T) {
		store := NewStore()
		require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
			Embedder:       semanticTestEmbedder{},
			VectorStore:    &memoryTestVectorStore{},
			EmbeddingModel: "semantic-test",
		}))
		store.vectors.Reranker = nil
		store.vectors.RerankMax = 1
		candidates := searchCandidateSet{
			search.SourceIDForMessage(testSessionA, 1): {
				CandidateMatch: search.CandidateMatch{SessionID: testSessionA},
				Message:        morphmsg.Message{ID: 1, Role: morphmsg.RoleUser, Content: "first"},
			},
			search.SourceIDForMessage(testSessionA, 2): {
				CandidateMatch: search.CandidateMatch{
					SessionID:  testSessionA,
					VectorRank: 2,
					HasVector:  true,
				},
				Message: morphmsg.Message{ID: 2, Role: morphmsg.RoleUser, Content: "second"},
			},
		}

		items := store.rerankSearchCandidates(context.Background(), base.SearchMessageOptions{Query: "needle"}, candidates)
		require.Len(t, items, 1)
	})

	t.Run("skips empty candidates", func(t *testing.T) {
		store := NewStore()
		require.Nil(t, store.rerankSearchCandidates(context.Background(), base.SearchMessageOptions{}, searchCandidateSet{}))
	})

	t.Run("returns sorted items when fallback fails", func(t *testing.T) {
		store := NewStore()
		require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
			Embedder:       semanticTestEmbedder{},
			VectorStore:    &memoryTestVectorStore{},
			EmbeddingModel: "semantic-test",
		}))
		badCandidates := searchCandidateSet{
			search.SourceIDForMessage(testSessionA, 3): {
				CandidateMatch: search.CandidateMatch{
					SessionID:    testSessionA,
					LexicalScore: math.NaN(),
					LexicalRank:  1,
					HasLexical:   true,
				},
				Message: morphmsg.Message{ID: 3, Role: morphmsg.RoleUser, Content: "bad"},
			},
		}

		items := store.rerankSearchCandidates(context.Background(), base.SearchMessageOptions{Query: "needle"}, badCandidates)
		require.Len(t, items, 1)
	})
}

func TestSafeErrorKind(t *testing.T) {
	t.Run("classifies common errors", func(t *testing.T) {
		require.Equal(t, "", getSafeErrorKind(nil))
		require.Equal(t, "context_canceled", getSafeErrorKind(context.Canceled))
		require.Equal(t, "timeout", getSafeErrorKind(context.DeadlineExceeded))
		require.Equal(t, "validation_failed", getSafeErrorKind(errors.New("validation failed")))
		require.Equal(t, "not_found", getSafeErrorKind(errors.New("not found")))
		require.Equal(t, "missing_required_value", getSafeErrorKind(errors.New("name is required")))
		require.Equal(t, "timeout", getSafeErrorKind(errors.New("request timeout")))
		require.Equal(t, "operation_failed", getSafeErrorKind(errors.New("boom")))
	})
}

func TestStore_LogSearchEvent(t *testing.T) {
	t.Run("records search option metadata", func(t *testing.T) {
		store := NewStore()
		_ = store.logSearchEvent("test", testSessionA, base.SearchMessageOptions{
			IgnoreSessionID:       testSessionB,
			MaxMessagesPerSession: 2,
			MaxSessions:           3,
			Query:                 "needle",
			Role:                  morphmsg.RoleUser,
			ToolName:              "search files",
		})
	})
}

func TestStore_LogCandidateDiagnostics(t *testing.T) {
	t.Run("logs ranked candidates", func(t *testing.T) {
		store := NewStore()
		store.logCandidateDiagnostics("ignored", nil)
		require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
			Embedder:       semanticTestEmbedder{},
			VectorStore:    &memoryTestVectorStore{},
			EmbeddingModel: "semantic-test",
			Diagnostics:    true,
		}))

		store.logCandidateDiagnostics("candidate merged", []*searchCandidate{{
			CandidateMatch: search.CandidateMatch{
				SessionID:       testSessionA,
				MatchedToolName: "session_search",
				LexicalRank:     1,
				VectorRank:      2,
				HasRerank:       true,
			},
			Message: morphmsg.Message{ID: 1},
		}})
	})
}

func TestLogSafeError(t *testing.T) {
	t.Run("preserves log events", func(t *testing.T) {
		store := NewStore()
		require.NotNil(t, applySafeErrorLog(store.logVectorEvent("test"), nil))
		require.NotNil(t, applySafeErrorLog(store.logVectorEvent("test"), errors.New("boom")))
	})
}

type invalidNameReranker struct{}

func (invalidNameReranker) Name() string {
	return "invalid"
}

type failingReranker struct{}

func (failingReranker) Name() string {
	return search.RerankerLLM
}

func (failingReranker) Rerank(
	context.Context,
	search.RerankRequest,
) (search.RerankResult, error) {
	return search.RerankResult{}, errors.New("rerank failed")
}

func (invalidNameReranker) Rerank(
	context.Context,
	search.RerankRequest,
) (search.RerankResult, error) {
	return search.RerankResult{}, nil
}

type failingEmbedder struct {
	err error
}

func (e failingEmbedder) Embed(
	context.Context,
	search.EmbeddingRequest,
) (search.EmbeddingResult, error) {
	return search.EmbeddingResult{}, e.err
}

type malformedEmbedder struct{}

func (malformedEmbedder) Embed(
	context.Context,
	search.EmbeddingRequest,
) (search.EmbeddingResult, error) {
	return search.EmbeddingResult{Model: "model", Dimensions: 2}, nil
}

type memoryTestVectorStore struct {
	upsertErr      error
	deleteErr      error
	searchErr      error
	upsertRecords  []search.VectorRecord
	deleteRequests []search.VectorDeleteRequest
	searchRequests []search.VectorSearchRequest
	searchResult   search.VectorSearchResult
}

func (s *memoryTestVectorStore) Upsert(_ context.Context, records []search.VectorRecord) error {
	s.upsertRecords = append(s.upsertRecords, records...)
	return s.upsertErr
}

func (s *memoryTestVectorStore) Delete(_ context.Context, req search.VectorDeleteRequest) error {
	s.deleteRequests = append(s.deleteRequests, req)
	return s.deleteErr
}

func (s *memoryTestVectorStore) Search(
	_ context.Context,
	req search.VectorSearchRequest,
) (search.VectorSearchResult, error) {
	s.searchRequests = append(s.searchRequests, req)
	if s.searchErr != nil {
		return search.VectorSearchResult{}, s.searchErr
	}
	return s.searchResult, nil
}

func (s *memoryTestVectorStore) Metadata(context.Context) (search.VectorStoreMetadata, error) {
	return search.VectorStoreMetadata{}, nil
}
