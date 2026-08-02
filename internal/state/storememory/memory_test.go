package storememory

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	statememory "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/internal/state/search"
	vectormemory "github.com/xymorphic/morph/internal/state/search/vectorstore/memory"
)

func TestMemoryStore_SearchWriteDeleteAndSourceLinks(t *testing.T) {
	store := NewStore()
	createdAt := time.Date(2026, 4, 30, 9, 0, 0, 0, time.UTC)

	item, err := store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:                   "  mem_one  ",
		Kind:                 statememory.MemoryKindSemantic,
		Status:               statememory.MemoryStatusActive,
		Title:                "Go preference",
		Text:                 "Use focused tests",
		Tags:                 []string{"Go", "Style"},
		CreatedAt:            createdAt,
		PromotionEvaluatedAt: createdAt.Add(time.Hour),
		Reflected:            true,
		Metadata:             map[string]string{"project": "morph"},
		SourceLinks: []statememory.MemorySourceLink{{
			SessionID:     "session",
			MessageIDs:    []uint{1},
			Offsets:       []int{2},
			SummaryID:     "summary",
			CreatedBy:     "reflection",
			CreatedReason: "preference",
		}},
	})
	require.NoError(t, err)
	require.Equal(t, "mem_one", item.ID)
	require.Equal(t, createdAt, item.CreatedAt)
	require.Equal(t, createdAt.Add(time.Hour), item.PromotionEvaluatedAt)
	require.False(t, item.UpdatedAt.IsZero())

	result, err := store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Text: "focused",
		Tags: []string{"go", "style"},
	})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	require.Equal(t, 1.0, result.Hits[0].Score)
	require.Equal(t, []uint{1}, result.Hits[0].Item.SourceLinks[0].MessageIDs)
	require.Equal(t, []int{2}, result.Hits[0].Item.SourceLinks[0].Offsets)
	require.Equal(t, "summary", result.Hits[0].Item.SourceLinks[0].SummaryID)
	require.True(t, result.Hits[0].Item.Reflected)

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{Reflected: new(false)})
	require.NoError(t, err)
	require.Empty(t, result.Hits)

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		PromotionEvaluated:       new(true),
		PromotionEvaluatedAfter:  createdAt,
		PromotionEvaluatedBefore: createdAt.Add(2 * time.Hour),
	})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		PromotionEvaluated: new(false),
	})
	require.NoError(t, err)
	require.Empty(t, result.Hits)

	_, err = store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:     "mem_unevaluated",
		Kind:   statememory.MemoryKindSemantic,
		Status: statememory.MemoryStatusActive,
		Text:   "Unevaluated memory",
	})
	require.NoError(t, err)

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		PromotionEvaluated: new(false),
	})
	require.NoError(t, err)
	require.Equal(t, []string{"mem_unevaluated"}, memoryHitIDs(result.Hits))

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		IDs: []string{" mem_one "},
	})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	require.Equal(t, "mem_one", result.Hits[0].Item.ID)

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		IDs: []string{"mem_missing"},
	})
	require.NoError(t, err)
	require.Empty(t, result.Hits)

	require.NoError(t, store.DeleteMemory(context.Background(), statememory.MemoryDeleteRequest{ID: item.ID}))

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{Text: "focused"})
	require.NoError(t, err)
	require.Empty(t, result.Hits)

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Text:     "focused",
		Statuses: []statememory.MemoryStatus{statememory.MemoryStatusDeleted},
	})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	require.Equal(t, statememory.MemoryStatusDeleted, result.Hits[0].Item.Status)
}

func TestMemoryStore_SearchMemoryRelaxesQuestionAndPreferenceTerms(t *testing.T) {
	store := NewStore()

	_, err := store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:     "mem_color_preference",
		Kind:   statememory.MemoryKindSemantic,
		Status: statememory.MemoryStatusActive,
		Title:  "User color preference: blue",
		Text:   "The user prefers blue as their best color.",
	})
	require.NoError(t, err)
	_, err = store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:     "mem_color_clarification",
		Kind:   statememory.MemoryKindProcedural,
		Status: statememory.MemoryStatusActive,
		Title:  "Clarify ambiguous color requests",
		Text:   "Ask whether color requests are about appearance, preferences, or themes.",
	})
	require.NoError(t, err)
	_, err = store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:     "mem_color_only",
		Kind:   statememory.MemoryKindSemantic,
		Status: statememory.MemoryStatusActive,
		Title:  "Color palette",
		Text:   "Use green for status badges.",
	})
	require.NoError(t, err)

	result, err := store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Text:  "what is my preferred color blue",
		Limit: 3,
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.Hits)
	require.Equal(t, "mem_color_preference", result.Hits[0].Item.ID)

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Text:  "what is my prefrred color",
		Limit: 3,
	})
	require.NoError(t, err)
	require.Contains(t, memoryHitIDs(result.Hits), "mem_color_preference")

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Text:  "alpha beta gamma delta color",
		Limit: 3,
	})
	require.NoError(t, err)
	require.Empty(t, result.Hits)
}

func TestMemoryStore_HardDeleteMemoryRemovesRecordAndVector(t *testing.T) {
	store := NewStore()
	item, err := store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:     "mem_hard_delete",
		Kind:   statememory.MemoryKindSemantic,
		Status: statememory.MemoryStatusCandidate,
		Text:   "Hard deleted candidate should disappear.",
	})
	require.NoError(t, err)

	result, err := store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		IDs:      []string{item.ID},
		Statuses: []statememory.MemoryStatus{statememory.MemoryStatusCandidate},
	})
	require.NoError(t, err)
	require.Equal(t, []string{item.ID}, memoryHitIDs(result.Hits))

	require.NoError(t, store.HardDeleteMemory(context.Background(), statememory.MemoryDeleteRequest{ID: item.ID}))
	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		IDs:      []string{item.ID},
		Statuses: []statememory.MemoryStatus{statememory.MemoryStatusCandidate, statememory.MemoryStatusDeleted},
	})
	require.NoError(t, err)
	require.Empty(t, result.Hits)
}

func TestMemoryStore_DefaultsToCandidateAndActiveOnlySearch(t *testing.T) {
	store := NewStore()

	item, err := store.UpsertMemory(context.Background(), statememory.MemoryItem{Text: "candidate"})
	require.NoError(t, err)
	require.NotEmpty(t, item.ID)
	require.Equal(t, statememory.MemoryStatusCandidate, item.Status)

	result, err := store.SearchMemory(context.Background(), statememory.MemorySearchQuery{Text: "candidate"})
	require.NoError(t, err)
	require.Empty(t, result.Hits)

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Text:     "candidate",
		Statuses: []statememory.MemoryStatus{statememory.MemoryStatusCandidate},
	})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
}

func TestMemoryStore_VectorDisabledUsesLexicalSearch(t *testing.T) {
	store := NewStore()
	_, err := store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:     "mem_lexical",
		Kind:   statememory.MemoryKindSemantic,
		Status: statememory.MemoryStatusActive,
		Text:   "Use focused tests for memory work.",
	})
	require.NoError(t, err)

	result, err := store.SearchMemory(context.Background(), statememory.MemorySearchQuery{Text: "focused"})

	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	require.Equal(t, "mem_lexical", result.Hits[0].Item.ID)
}

func TestMemoryStore_MemoryVectorSearchEnabled(t *testing.T) {
	var nilStore *Store
	require.False(t, nilStore.memoryVectorSearchEnabled(statememory.MemorySearchQuery{Text: "needle"}))

	store := NewStore()
	require.False(t, store.memoryVectorSearchEnabled(statememory.MemorySearchQuery{Text: "needle"}))

	require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
		Embedder:       semanticTestEmbedder{},
		VectorStore:    &memoryTestVectorStore{},
		EmbeddingModel: "semantic-test",
	}))
	require.False(t, store.memoryVectorSearchEnabled(statememory.MemorySearchQuery{Text: " "}))
	require.True(t, store.memoryVectorSearchEnabled(statememory.MemorySearchQuery{Text: "needle"}))
}

func TestMemoryStore_VectorSearchMergesWithLexicalCandidates(t *testing.T) {
	store := NewStore()
	require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
		Embedder:       semanticTestEmbedder{},
		VectorStore:    vectormemory.NewStore(),
		EmbeddingModel: "semantic-test",
	}))
	for _, item := range []statememory.MemoryItem{
		{
			ID:     "mem_lexical",
			Kind:   statememory.MemoryKindSemantic,
			Status: statememory.MemoryStatusActive,
			Text:   "Use focused cancellation tests for memory work.",
		},
		{
			ID:     "mem_vector",
			Kind:   statememory.MemoryKindSemantic,
			Status: statememory.MemoryStatusActive,
			Text:   "Renewal risk scoring playbook.",
		},
	} {
		_, err := store.UpsertMemory(context.Background(), item)
		require.NoError(t, err)
	}

	result, err := store.SearchMemory(context.Background(), statememory.MemorySearchQuery{Text: "focused cancellation"})

	require.NoError(t, err)
	require.ElementsMatch(t, []string{"mem_lexical", "mem_vector"}, memoryHitIDs(result.Hits))
}

func TestMemoryStore_VectorSearchPushesTagFilters(t *testing.T) {
	vectorStore := &memoryTestVectorStore{}
	store := NewStore()
	require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
		Embedder:       semanticTestEmbedder{},
		VectorStore:    vectorStore,
		EmbeddingModel: "semantic-test",
	}))
	for _, item := range []statememory.MemoryItem{
		{
			ID:     "mem_semantic",
			Kind:   statememory.MemoryKindSemantic,
			Status: statememory.MemoryStatusActive,
			Tags:   []string{"go"},
			Text:   "Retention renewal cancellation prevention workflow.",
		},
		{
			ID:     "mem_procedural",
			Kind:   statememory.MemoryKindProcedural,
			Status: statememory.MemoryStatusActive,
			Tags:   []string{"go"},
			Text:   "Retention renewal cancellation prevention procedure.",
		},
	} {
		_, err := store.UpsertMemory(context.Background(), item)
		require.NoError(t, err)
	}

	_, err := store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Text:  "retention",
		Kinds: []statememory.MemoryKind{statememory.MemoryKindSemantic},
		Tags:  []string{"go"},
		Limit: 1,
	})

	require.NoError(t, err)
	require.Len(t, vectorStore.searchRequests, 1)
	require.Empty(t, vectorStore.searchRequests[0].Filter.SourceIDs)
	require.Equal(t, []string{
		"memory_kind:semantic",
		"memory_status:active",
		"memory_tag:go",
	}, vectorStore.searchRequests[0].Filter.Tags)
}

func TestMemoryStore_VectorSearchPushesTagGroupsForOrFilters(t *testing.T) {
	vectorStore := &memoryTestVectorStore{}
	store := NewStore()
	require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
		Embedder:       semanticTestEmbedder{},
		VectorStore:    vectorStore,
		EmbeddingModel: "semantic-test",
		Required:       true,
	}))
	_, err := store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:     "mem_semantic",
		Kind:   statememory.MemoryKindSemantic,
		Status: statememory.MemoryStatusActive,
		Text:   "Retention renewal cancellation prevention workflow.",
	})
	require.NoError(t, err)

	result, err := store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Text: "unrelated",
		Kinds: []statememory.MemoryKind{
			statememory.MemoryKindSemantic,
			statememory.MemoryKindProcedural,
		},
		Limit: 1,
	})

	require.NoError(t, err)
	require.Empty(t, result.Hits)
	require.Len(t, vectorStore.searchRequests, 1)
	require.Empty(t, vectorStore.searchRequests[0].Filter.SourceIDs)
	require.Equal(t, []string{"memory_status:active"}, vectorStore.searchRequests[0].Filter.Tags)
	require.Equal(t, [][]string{{
		"memory_kind:procedural",
		"memory_kind:semantic",
	}}, vectorStore.searchRequests[0].Filter.TagGroups)

	_, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Text: "unrelated",
		Statuses: []statememory.MemoryStatus{
			statememory.MemoryStatusActive,
			statememory.MemoryStatusCandidate,
		},
		Limit: 1,
	})

	require.NoError(t, err)
	require.Len(t, vectorStore.searchRequests, 2)
	require.Empty(t, vectorStore.searchRequests[1].Filter.SourceIDs)
	require.Empty(t, vectorStore.searchRequests[1].Filter.Tags)
	require.Equal(t, [][]string{{
		"memory_status:active",
		"memory_status:candidate",
	}}, vectorStore.searchRequests[1].Filter.TagGroups)
}

func TestMemoryStore_VectorSearchSourceIDFiltersAndMatchResolution(t *testing.T) {
	vectorStore := &memoryTestVectorStore{}
	store := NewStore()
	require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
		Embedder:       semanticTestEmbedder{},
		VectorStore:    vectorStore,
		EmbeddingModel: "semantic-test",
	}))
	for _, item := range []statememory.MemoryItem{
		{
			ID:     "mem_keep",
			Kind:   statememory.MemoryKindSemantic,
			Status: statememory.MemoryStatusActive,
			Title:  "Keep",
			Text:   "Retention renewal cancellation prevention workflow.",
		},
		{
			ID:     "mem_candidate",
			Kind:   statememory.MemoryKindSemantic,
			Status: statememory.MemoryStatusCandidate,
			Title:  "Candidate",
			Text:   "Retention renewal cancellation prevention draft.",
		},
	} {
		_, err := store.UpsertMemory(context.Background(), item)
		require.NoError(t, err)
	}
	vectorStore.searchResult = search.VectorSearchResult{Matches: []search.VectorSearchMatch{
		{Record: search.VectorRecord{SourceID: "bad"}, Score: 1},
		{Record: search.VectorRecord{SourceID: search.StableMemoryItemID("mem_keep")}, Score: 0.9},
		{Record: search.VectorRecord{SourceID: search.StableMemoryItemID("mem_keep")}, Score: 0.8},
		{Record: search.VectorRecord{SourceID: search.StableMemoryItemID("mem_candidate")}, Score: 0.7},
		{Record: search.VectorRecord{SourceID: search.StableMemoryItemID("mem_missing")}, Score: 0.6},
	}}

	result, err := store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Text: "retention",
		IDs:  []string{"mem_keep", "mem_missing"},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"mem_keep"}, memoryHitIDs(result.Hits))
	require.Len(t, vectorStore.searchRequests, 1)
	require.Equal(t, []string{
		search.StableMemoryItemID("mem_keep"),
	}, vectorStore.searchRequests[0].Filter.SourceIDs)
}

func TestMemoryStore_VectorSearchSkipsWhenIDFilterMatchesNoSources(t *testing.T) {
	vectorStore := &memoryTestVectorStore{searchErr: errors.New("vector search should be skipped")}
	store := NewStore()
	require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
		Embedder:       semanticTestEmbedder{},
		VectorStore:    vectorStore,
		EmbeddingModel: "semantic-test",
		Required:       true,
	}))

	result, err := store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Text: "retention",
		IDs:  []string{"mem_missing"},
	})

	require.NoError(t, err)
	require.Empty(t, result.Hits)
	require.Empty(t, vectorStore.searchRequests)
}

func TestMemoryStore_VectorRefreshesStaleEmbeddingsWhenTextChanges(t *testing.T) {
	vectorStore := &memoryTestVectorStore{}
	store := NewStore()
	require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
		Embedder:       semanticTestEmbedder{},
		VectorStore:    vectorStore,
		EmbeddingModel: "semantic-test",
	}))

	_, err := store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:     "mem_refresh",
		Status: statememory.MemoryStatusActive,
		Text:   "Old text.",
	})
	require.NoError(t, err)
	_, err = store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:     "mem_refresh",
		Status: statememory.MemoryStatusActive,
		Text:   "Retention renewal cancellation prevention workflow.",
	})
	require.NoError(t, err)

	require.Len(t, vectorStore.upsertRecords, 2)
	require.NotEqual(t, vectorStore.upsertRecords[0].ContentHash, vectorStore.upsertRecords[1].ContentHash)
	require.Equal(t, search.StableMemoryItemID("mem_refresh"), vectorStore.upsertRecords[1].SourceID)
	require.Contains(t, vectorStore.upsertRecords[1].Tags, "memory_status:active")
	require.Contains(t, vectorStore.upsertRecords[1].Tags, "memory_reflected:false")
}

func TestMemoryStore_VectorIndexAndDeleteHelpers(t *testing.T) {
	t.Run("skip when disabled empty or missing id", func(t *testing.T) {
		var nilStore *Store
		require.NoError(t, nilStore.indexMemoryVector(context.Background(), statememory.MemoryItem{ID: "mem_nil", Text: "text"}))
		require.NoError(t, nilStore.deleteMemoryVector(context.Background(), "mem_nil"))

		store := NewStore()
		require.NoError(t, store.indexMemoryVector(context.Background(), statememory.MemoryItem{ID: "mem_disabled", Text: "text"}))
		require.NoError(t, store.deleteMemoryVector(context.Background(), "mem_disabled"))

		require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
			Embedder:       semanticTestEmbedder{},
			VectorStore:    &memoryTestVectorStore{},
			EmbeddingModel: "semantic-test",
		}))
		require.NoError(t, store.indexMemoryVector(context.Background(), statememory.MemoryItem{ID: "mem_empty"}))
		require.NoError(t, store.deleteMemoryVector(context.Background(), " "))
	})

	t.Run("returns embed validation upsert and delete errors", func(t *testing.T) {
		store := NewStore()
		require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
			Embedder:       failingEmbedder{err: errors.New("embed failed")},
			VectorStore:    &memoryTestVectorStore{},
			EmbeddingModel: "semantic-test",
			Required:       true,
		}))
		err := store.indexMemoryVector(context.Background(), statememory.MemoryItem{ID: "mem_embed", Text: "text"})
		require.EqualError(t, err, "embed failed")

		store = NewStore()
		require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
			Embedder:       malformedEmbedder{},
			VectorStore:    &memoryTestVectorStore{},
			EmbeddingModel: "semantic-test",
			Required:       true,
		}))
		err = store.indexMemoryVector(context.Background(), statememory.MemoryItem{ID: "mem_malformed", Text: "text"})
		require.EqualError(t, err, "embedding result model must match request model")

		store = NewStore()
		require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
			Embedder:       semanticTestEmbedder{},
			VectorStore:    &memoryTestVectorStore{upsertErr: errors.New("upsert failed")},
			EmbeddingModel: "semantic-test",
			Required:       true,
		}))
		err = store.indexMemoryVector(context.Background(), statememory.MemoryItem{ID: "mem_upsert", Text: "text"})
		require.EqualError(t, err, "upsert failed")

		store = NewStore()
		require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
			Embedder:       semanticTestEmbedder{},
			VectorStore:    &memoryTestVectorStore{deleteErr: errors.New("delete failed")},
			EmbeddingModel: "semantic-test",
			Required:       true,
		}))
		err = store.deleteMemoryVector(context.Background(), "mem_delete")
		require.EqualError(t, err, "delete failed")
	})
}

func TestMemoryStore_VectorFailuresDegradeWhenOptional(t *testing.T) {
	store := NewStore()
	require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
		Embedder:       failingEmbedder{err: errors.New("embed failed")},
		VectorStore:    &memoryTestVectorStore{},
		EmbeddingModel: "semantic-test",
	}))
	_, err := store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:     "mem_lexical",
		Status: statememory.MemoryStatusActive,
		Text:   "Use focused tests for memory work.",
	})
	require.NoError(t, err)

	result, err := store.SearchMemory(context.Background(), statememory.MemorySearchQuery{Text: "focused"})

	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	require.Equal(t, "mem_lexical", result.Hits[0].Item.ID)
}

func TestMemoryStore_VectorFailuresReturnWhenRequired(t *testing.T) {
	store := NewStore()
	require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
		Embedder:       semanticTestEmbedder{},
		VectorStore:    &memoryTestVectorStore{searchErr: errors.New("vector failed")},
		EmbeddingModel: "semantic-test",
		Required:       true,
	}))
	_, err := store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:     "mem_lexical",
		Status: statememory.MemoryStatusActive,
		Text:   "Use focused tests for memory work.",
	})
	require.NoError(t, err)

	result, err := store.SearchMemory(context.Background(), statememory.MemorySearchQuery{Text: "focused"})

	require.EqualError(t, err, "vector failed")
	require.Empty(t, result)
}

func TestMemoryStore_VectorSearchValidationAndSourceIDErrors(t *testing.T) {
	store := NewStore()
	require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
		Embedder:       malformedEmbedder{},
		VectorStore:    &memoryTestVectorStore{},
		EmbeddingModel: "semantic-test",
		Required:       true,
	}))

	hits, err := store.searchMemoryVector(context.Background(), statememory.MemorySearchQuery{Text: "needle"}, 10)

	require.EqualError(t, err, "embedding result model must match request model")
	require.Empty(t, hits)

	store = NewStore()
	require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
		Embedder:       semanticTestEmbedder{},
		VectorStore:    &memoryTestVectorStore{searchErr: errors.New("search failed")},
		EmbeddingModel: "semantic-test",
		Required:       true,
	}))

	hits, err = store.searchMemoryVector(context.Background(), statememory.MemorySearchQuery{Text: "needle"}, 10)

	require.EqualError(t, err, "search failed")
	require.Empty(t, hits)
}

func TestMemoryVectorHelpers(t *testing.T) {
	reflected := true
	query := statememory.MemorySearchQuery{
		SessionID: " session ",
		Kinds: []statememory.MemoryKind{
			statememory.MemoryKindSemantic,
			statememory.MemoryKindProcedural,
		},
		Statuses: []statememory.MemoryStatus{
			statememory.MemoryStatusCandidate,
			statememory.MemoryStatusActive,
		},
		Tags:      []string{" Go ", "style"},
		Reflected: &reflected,
	}

	require.Equal(t, []string{
		"memory_reflected:true",
		"memory_session:session",
		"memory_tag:go",
		"memory_tag:style",
	}, getMemoryVectorFilterTags(query))
	require.Equal(t, [][]string{
		{"memory_kind:procedural", "memory_kind:semantic"},
		{"memory_status:active", "memory_status:candidate"},
	}, getMemoryVectorFilterTagGroups(query))
	require.Equal(t, []string{"memory_status:active"}, getMemoryVectorFilterTags(statememory.MemorySearchQuery{}))
	require.Equal(t, []string{
		"memory_kind:semantic",
		"memory_status:candidate",
	}, getMemoryVectorFilterTags(statememory.MemorySearchQuery{
		Kinds:    []statememory.MemoryKind{statememory.MemoryKindSemantic},
		Statuses: []statememory.MemoryStatus{statememory.MemoryStatusCandidate},
	}))
	require.True(t, checkMemoryQueryNeedsSourceIDFilter(statememory.MemorySearchQuery{IDs: []string{"mem_a"}}))
	require.True(t, checkMemoryQueryNeedsSourceIDFilter(statememory.MemorySearchQuery{PromotionEvaluated: new(false)}))
	require.False(t, checkMemoryQueryNeedsSourceIDFilter(statememory.MemorySearchQuery{Tags: []string{"go"}}))

	item := statememory.MemoryItem{
		Kind:      statememory.MemoryKindSemantic,
		Status:    statememory.MemoryStatusActive,
		Title:     "Title",
		Text:      "Body",
		Tags:      []string{"Go"},
		Reflected: true,
		SourceLinks: []statememory.MemorySourceLink{{
			SessionID: "source-session",
		}},
	}
	require.Equal(t, "Title\nBody", getMemoryVectorText(item))
	require.Equal(t, "source-session", search.MemoryVectorSessionID(item))
	require.Equal(t, []string{
		"memory_kind:semantic",
		"memory_reflected:true",
		"memory_session:source-session",
		"memory_status:active",
		"memory_tag:go",
	}, search.MemoryVectorTags(item))

	item.Metadata = map[string]string{"source_session_id": "metadata-session"}
	require.Equal(t, "metadata-session", search.MemoryVectorSessionID(item))
	require.Empty(t, search.MemoryVectorSessionID(statememory.MemoryItem{}))
}

func TestMemoryVectorMergeAndSortHelpers(t *testing.T) {
	now := time.Date(2026, 4, 30, 9, 0, 0, 0, time.UTC)
	merged := mergeMemoryHits(
		[]statememory.MemorySearchHit{
			{Item: statememory.MemoryItem{ID: "mem_a", UpdatedAt: now}, Score: 0.5, LexicalScore: 0.5},
			{Item: statememory.MemoryItem{ID: "mem_b", UpdatedAt: now.Add(time.Minute)}, Score: 0.4, LexicalScore: 0.4},
		},
		[]statememory.MemorySearchHit{
			{Item: statememory.MemoryItem{ID: "mem_a", UpdatedAt: now}, Score: 0.9, VectorScore: 0.9},
			{Item: statememory.MemoryItem{ID: "mem_c", UpdatedAt: now}, Score: 0.9, VectorScore: 0.9},
			{Item: statememory.MemoryItem{}, Score: 1},
		},
	)

	require.Equal(t, []string{"mem_a", "mem_c", "mem_b"}, memoryHitIDs(merged))
	require.Equal(t, 0.9, merged[0].Score)
	require.Equal(t, 0.5, merged[0].LexicalScore)
	require.Equal(t, 0.9, merged[0].VectorScore)

	hits := []statememory.MemorySearchHit{
		{Item: statememory.MemoryItem{ID: "mem_b", UpdatedAt: now}, Score: 1},
		{Item: statememory.MemoryItem{ID: "mem_c", UpdatedAt: now.Add(time.Minute)}, Score: 1},
		{Item: statememory.MemoryItem{ID: "mem_a", UpdatedAt: now}, Score: 1},
	}
	sortMemoryHits(hits)
	require.Equal(t, []string{"mem_c", "mem_a", "mem_b"}, memoryHitIDs(hits))
}

func memoryHitIDs(hits []statememory.MemorySearchHit) []string {
	ids := make([]string, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.Item.ID)
	}

	return ids
}

func TestMemoryStore_ListSessionMemoriesFiltersOrdersLimitsAndClones(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)

	for _, item := range []statememory.MemoryItem{
		{
			ID:     "mem_source_old",
			Kind:   statememory.MemoryKindEpisodic,
			Status: statememory.MemoryStatusActive,
			SourceLinks: []statememory.MemorySourceLink{{
				SessionID: statememory.DefaultSessionID,
				Offsets:   []int{1},
			}},
		},
		{
			ID:       "mem_metadata_new",
			Kind:     statememory.MemoryKindEpisodic,
			Status:   statememory.MemoryStatusActive,
			Metadata: map[string]string{"source_session_id": statememory.DefaultSessionID},
		},
		{
			ID:       "mem_candidate",
			Kind:     statememory.MemoryKindEpisodic,
			Status:   statememory.MemoryStatusCandidate,
			Metadata: map[string]string{"source_session_id": statememory.DefaultSessionID},
		},
		{
			ID:       "mem_other",
			Kind:     statememory.MemoryKindEpisodic,
			Status:   statememory.MemoryStatusActive,
			Metadata: map[string]string{"source_session_id": "other"},
		},
	} {
		_, err := store.UpsertMemory(context.Background(), item)
		require.NoError(t, err)
	}

	store.mu.Lock()
	for id, item := range store.memoryItems {
		switch id {
		case "mem_source_old":
			item.UpdatedAt = now
		case "mem_metadata_new":
			item.UpdatedAt = now.Add(time.Hour)
		case "mem_candidate":
			item.UpdatedAt = now.Add(2 * time.Hour)
		}
		store.memoryItems[id] = item
	}
	store.mu.Unlock()

	result, err := store.ListSessionMemories(context.Background(), statememory.SessionMemoryQuery{
		SessionID: statememory.DefaultSessionID,
		Kinds:     []statememory.MemoryKind{statememory.MemoryKindEpisodic},
		Statuses:  []statememory.MemoryStatus{statememory.MemoryStatusActive},
		Limit:     1,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"mem_metadata_new"}, memoryItemIDs(result.Items))

	result, err = store.ListSessionMemories(context.Background(), statememory.SessionMemoryQuery{
		SessionID: statememory.DefaultSessionID,
		Statuses:  []statememory.MemoryStatus{statememory.MemoryStatusActive, statememory.MemoryStatusCandidate},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"mem_candidate", "mem_metadata_new", "mem_source_old"}, memoryItemIDs(result.Items))
	result.Items[2].SourceLinks[0].Offsets[0] = 99
	require.Equal(t, []int{1}, store.memoryItems["mem_source_old"].SourceLinks[0].Offsets)
}

func TestMemoryStore_ListSessionMemoriesDefaultsToActiveStatus(t *testing.T) {
	store := NewStore()
	for _, item := range []statememory.MemoryItem{
		{
			ID:     "mem_active_b",
			Status: statememory.MemoryStatusActive,
			Metadata: map[string]string{
				"source_session_id": statememory.DefaultSessionID,
			},
		},
		{
			ID:     "mem_active_a",
			Status: statememory.MemoryStatusActive,
			Metadata: map[string]string{
				"source_session_id": statememory.DefaultSessionID,
			},
		},
		{
			ID:     "mem_candidate",
			Status: statememory.MemoryStatusCandidate,
			Metadata: map[string]string{
				"source_session_id": statememory.DefaultSessionID,
			},
		},
	} {
		_, err := store.UpsertMemory(context.Background(), item)
		require.NoError(t, err)
	}

	store.mu.Lock()
	for id, item := range store.memoryItems {
		if id == "mem_active_a" || id == "mem_active_b" {
			item.UpdatedAt = time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
			store.memoryItems[id] = item
		}
	}
	store.mu.Unlock()

	result, err := store.ListSessionMemories(context.Background(), statememory.SessionMemoryQuery{
		SessionID: statememory.DefaultSessionID,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"mem_active_a", "mem_active_b"}, memoryItemIDs(result.Items))
}

func TestMemoryStore_SearchOrdersAndLimitsResults(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)

	for _, item := range []statememory.MemoryItem{
		{ID: "mem_low", Status: statememory.MemoryStatusActive, Text: "plan", Metadata: map[string]string{"source_session_id": statememory.DefaultSessionID}},
		{ID: "mem_high_old", Status: statememory.MemoryStatusActive, Title: "plan", Text: "plan", Metadata: map[string]string{"source_session_id": statememory.DefaultSessionID}},
		{ID: "mem_high_new", Status: statememory.MemoryStatusActive, Title: "plan", Text: "plan", Metadata: map[string]string{"source_session_id": statememory.DefaultSessionID}},
		{ID: "mem_high_same_b", Status: statememory.MemoryStatusActive, Title: "plan", Text: "plan", Metadata: map[string]string{"source_session_id": statememory.DefaultSessionID}},
		{ID: "mem_high_same_a", Status: statememory.MemoryStatusActive, Title: "plan", Text: "plan", Metadata: map[string]string{"source_session_id": statememory.DefaultSessionID}},
		{ID: "mem_other_session", Status: statememory.MemoryStatusActive, Title: "plan", Text: "plan", Metadata: map[string]string{"source_session_id": "other"}},
	} {
		_, err := store.UpsertMemory(context.Background(), item)
		require.NoError(t, err)
	}

	store.mu.Lock()
	for id, item := range store.memoryItems {
		switch id {
		case "mem_low":
			item.UpdatedAt = now.Add(4 * time.Hour)
		case "mem_high_new":
			item.UpdatedAt = now.Add(3 * time.Hour)
		case "mem_high_old":
			item.UpdatedAt = now
		case "mem_high_same_a", "mem_high_same_b":
			item.UpdatedAt = now.Add(time.Hour)
		}
		store.memoryItems[id] = item
	}
	store.mu.Unlock()

	result, err := store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Text:      "plan",
		SessionID: statememory.DefaultSessionID,
		Limit:     4,
	})
	require.NoError(t, err)
	require.Len(t, result.Hits, 4)
	require.Equal(t, []string{
		"mem_high_new",
		"mem_high_same_a",
		"mem_high_same_b",
		"mem_high_old",
	}, []string{
		result.Hits[0].Item.ID,
		result.Hits[1].Item.ID,
		result.Hits[2].Item.ID,
		result.Hits[3].Item.ID,
	})
}

func TestMemoryStore_SearchTrimsCandidateSetBeforeReranking(t *testing.T) {
	store := NewStore()
	for idx := 0; idx <= search.DefaultRerankCandidateLimit; idx++ {
		_, err := store.UpsertMemory(context.Background(), statememory.MemoryItem{
			ID:     fmt.Sprintf("mem_%03d", idx),
			Status: statememory.MemoryStatusActive,
			Text:   "plan",
		})
		require.NoError(t, err)
	}

	result, err := store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Text:  "plan",
		Limit: 1,
	})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
}

func TestMemoryStore_ReranksBeforeLimiting(t *testing.T) {
	store := NewStore()

	for _, item := range []statememory.MemoryItem{
		{
			ID:     "mem_broad",
			Status: statememory.MemoryStatusActive,
			Text:   "plan",
		},
		{
			ID:         "mem_confident",
			Status:     statememory.MemoryStatusActive,
			Text:       "plan",
			Confidence: 1,
		},
		{
			ID:     "mem_other",
			Status: statememory.MemoryStatusActive,
			Text:   "plan",
		},
	} {
		_, err := store.UpsertMemory(context.Background(), item)
		require.NoError(t, err)
	}

	result, err := store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Text:  "plan",
		Limit: 1,
	})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	require.Equal(t, "mem_confident", result.Hits[0].Item.ID)
}

func TestMemoryStore_UpdatePreservesCreatedAtAndClonesItems(t *testing.T) {
	store := &Store{}
	createdAt := time.Date(2026, 4, 30, 11, 0, 0, 0, time.UTC)

	first, err := store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:        "mem_update",
		Status:    statememory.MemoryStatusActive,
		Title:     "first",
		Tags:      []string{"old"},
		Metadata:  map[string]string{"project": "morph"},
		CreatedAt: createdAt,
		SourceLinks: []statememory.MemorySourceLink{{
			MessageIDs: []uint{1},
			Offsets:    []int{2},
		}},
	})
	require.NoError(t, err)
	require.Equal(t, createdAt, first.CreatedAt)

	first.Tags[0] = "mutated"
	first.Metadata["project"] = "mutated"
	first.SourceLinks[0].MessageIDs[0] = 99
	first.SourceLinks[0].Offsets[0] = 99

	second, err := store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:     "mem_update",
		Status: statememory.MemoryStatusActive,
		Title:  "second",
		Tags:   []string{"new"},
	})
	require.NoError(t, err)
	require.Equal(t, createdAt, second.CreatedAt)
	require.True(t, second.UpdatedAt.After(second.CreatedAt))

	result, err := store.SearchMemory(context.Background(), statememory.MemorySearchQuery{Text: "second"})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	require.Equal(t, []string{"new"}, result.Hits[0].Item.Tags)
	require.Empty(t, result.Hits[0].Item.Metadata)
	require.Empty(t, result.Hits[0].Item.SourceLinks)
	require.NotEqual(t, "mutated", result.Hits[0].Item.Title)

	result.Hits[0].Item.Tags[0] = "mutated"
	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{Text: "second"})
	require.NoError(t, err)
	require.Equal(t, []string{"new"}, result.Hits[0].Item.Tags)
}

func TestMemoryStore_PatchMemoryUpdatesOnlyRequestedFields(t *testing.T) {
	store := NewStore()
	evaluatedAt := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	item, err := store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:                   "mem_patch",
		Kind:                 statememory.MemoryKindEpisodic,
		Status:               statememory.MemoryStatusCandidate,
		Title:                "Original",
		Text:                 "Original text",
		Tags:                 []string{"old"},
		Metadata:             map[string]string{"preserved": "yes"},
		Confidence:           0.4,
		PromotionEvaluatedAt: evaluatedAt,
		SourceLinks: []statememory.MemorySourceLink{{
			SessionID: "old-session",
			Offsets:   []int{1},
		}},
	})
	require.NoError(t, err)

	reflected := true
	status := statememory.MemoryStatusActive
	title := "Patched title"
	text := "Patched text about durable updates"
	tags := []string{"New", "Patch"}
	links := []statememory.MemorySourceLink{{
		SessionID: statememory.DefaultSessionID,
		Offsets:   []int{2},
	}}
	clearedEvaluation := time.Time{}
	patched, err := store.PatchMemory(context.Background(), statememory.MemoryPatch{
		ID:                   item.ID,
		Status:               &status,
		Title:                &title,
		Text:                 &text,
		Tags:                 &tags,
		SourceLinks:          &links,
		Reflected:            &reflected,
		Metadata:             map[string]string{"source_session_id": statememory.DefaultSessionID},
		PromotionEvaluatedAt: &clearedEvaluation,
	})
	require.NoError(t, err)
	require.Equal(t, statememory.MemoryStatusActive, patched.Status)
	require.True(t, patched.Reflected)
	require.Equal(t, "Patched title", patched.Title)
	require.Equal(t, "Patched text about durable updates", patched.Text)
	require.Equal(t, []string{"New", "Patch"}, patched.Tags)
	require.Equal(t, []int{2}, patched.SourceLinks[0].Offsets)
	require.Equal(t, 0.4, patched.Confidence)
	require.Equal(t, "yes", patched.Metadata["preserved"])
	require.Equal(t, statememory.DefaultSessionID, patched.Metadata["source_session_id"])
	require.True(t, patched.PromotionEvaluatedAt.IsZero())
	require.Equal(t, item.CreatedAt, patched.CreatedAt)
	require.True(t, patched.UpdatedAt.After(item.UpdatedAt))

	result, err := store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Text: "durable",
		Tags: []string{"patch"},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"mem_patch"}, memoryHitIDs(result.Hits))

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{Text: "Original"})
	require.NoError(t, err)
	require.Empty(t, result.Hits)

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{PromotionEvaluated: new(false)})
	require.NoError(t, err)
	require.Equal(t, []string{"mem_patch"}, memoryHitIDs(result.Hits))

	sessionResult, err := store.ListSessionMemories(context.Background(), statememory.SessionMemoryQuery{
		SessionID: statememory.DefaultSessionID,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"mem_patch"}, memoryItemIDs(sessionResult.Items))
}

func TestMemoryStore_PatchMemoryDeletesVectorForDeletedStatus(t *testing.T) {
	store := NewStore()
	vectorStore := &memoryTestVectorStore{}
	require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
		Embedder:       semanticTestEmbedder{},
		VectorStore:    vectorStore,
		EmbeddingModel: "semantic-test",
		Required:       true,
	}))

	_, err := store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:     "mem_delete_vector",
		Status: statememory.MemoryStatusActive,
		Text:   "Vector-backed memory",
	})
	require.NoError(t, err)

	status := statememory.MemoryStatusDeleted
	_, err = store.PatchMemory(context.Background(), statememory.MemoryPatch{
		ID:     "mem_delete_vector",
		Status: &status,
	})
	require.NoError(t, err)

	require.Len(t, vectorStore.upsertRecords, 1)
	require.Len(t, vectorStore.deleteRequests, 1)
	require.Equal(t, search.SourceKindMemoryItem, vectorStore.deleteRequests[0].SourceKind)
	require.Equal(t, []string{search.StableMemoryItemID("mem_delete_vector")}, vectorStore.deleteRequests[0].SourceIDs)
}

func TestMemoryStore_MemoryVectorCoverageEdges(t *testing.T) {
	t.Run("upsert returns required vector errors", func(t *testing.T) {
		store := NewStore()
		require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
			Embedder:       semanticTestEmbedder{},
			VectorStore:    &memoryTestVectorStore{upsertErr: errors.New("upsert failed")},
			EmbeddingModel: "semantic-test",
			Required:       true,
		}))

		item, err := store.UpsertMemory(context.Background(), statememory.MemoryItem{
			ID:     "mem_required_vector",
			Status: statememory.MemoryStatusActive,
			Text:   "Vector-backed memory",
		})

		require.EqualError(t, err, "upsert failed")
		require.Equal(t, statememory.MemoryItem{}, item)
	})

	t.Run("patch returns required vector errors", func(t *testing.T) {
		vectorStore := &memoryTestVectorStore{}
		store := NewStore()
		require.NoError(t, store.ConfigureVectorStore(search.VectorStoreOptions{
			Embedder:       semanticTestEmbedder{},
			VectorStore:    vectorStore,
			EmbeddingModel: "semantic-test",
			Required:       true,
		}))
		_, err := store.UpsertMemory(context.Background(), statememory.MemoryItem{
			ID:     "mem_patch_vector",
			Status: statememory.MemoryStatusActive,
			Text:   "Original vector memory",
		})
		require.NoError(t, err)

		vectorStore.upsertErr = errors.New("patch upsert failed")
		text := "Updated vector memory"
		item, err := store.PatchMemory(context.Background(), statememory.MemoryPatch{
			ID:   "mem_patch_vector",
			Text: &text,
		})

		require.EqualError(t, err, "patch upsert failed")
		require.Equal(t, statememory.MemoryItem{}, item)
	})

	t.Run("helper branches", func(t *testing.T) {
		store := NewStore()
		now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
		for _, item := range []statememory.MemoryItem{
			{ID: "mem_b", Status: statememory.MemoryStatusActive, Text: "plan", UpdatedAt: now},
			{ID: "mem_a", Status: statememory.MemoryStatusActive, Text: "plan", UpdatedAt: now.Add(time.Second)},
		} {
			_, err := store.UpsertMemory(context.Background(), item)
			require.NoError(t, err)
		}

		hits, reranker := store.memoryLexicalHits(statememory.MemorySearchQuery{Text: "plan"}, 1)
		require.Len(t, hits, 1)
		require.Equal(t, "mem_a", hits[0].Item.ID)
		require.Nil(t, reranker)

		sourceIDs, filtered := store.memoryVectorSourceIDs(statememory.MemorySearchQuery{
			IDs: []string{"mem_a", "mem_b"},
		}, 1)
		require.True(t, filtered)
		require.Len(t, sourceIDs, 1)

		merged := mergeMemoryHitEvidence(
			statememory.MemorySearchHit{Score: 0.1, LexicalScore: 0.1, VectorScore: 0.1},
			statememory.MemorySearchHit{Score: 0.2, LexicalScore: 0.3, VectorScore: 0.4},
		)
		require.Equal(t, 0.2, merged.Score)
		require.Equal(t, 0.3, merged.LexicalScore)
		require.Equal(t, 0.4, merged.VectorScore)
	})
}

func TestMemoryStore_NilReceiverAndValidationErrors(t *testing.T) {
	var nilStore *Store

	_, err := nilStore.SearchMemory(context.Background(), statememory.MemorySearchQuery{})
	require.EqualError(t, err, "store is required")

	_, err = nilStore.ListSessionMemories(context.Background(), statememory.SessionMemoryQuery{})
	require.EqualError(t, err, "store is required")

	_, err = nilStore.UpsertMemory(context.Background(), statememory.MemoryItem{})
	require.EqualError(t, err, "store is required")

	_, err = nilStore.PatchMemory(context.Background(), statememory.MemoryPatch{})
	require.EqualError(t, err, "store is required")

	err = nilStore.DeleteMemory(context.Background(), statememory.MemoryDeleteRequest{ID: "mem"})
	require.EqualError(t, err, "store is required")
	err = nilStore.HardDeleteMemory(context.Background(), statememory.MemoryDeleteRequest{ID: "mem"})
	require.EqualError(t, err, "store is required")

	store := NewStore()
	_, err = store.ListSessionMemories(context.Background(), statememory.SessionMemoryQuery{})
	require.EqualError(t, err, "session id is required")
	_, err = store.PatchMemory(context.Background(), statememory.MemoryPatch{})
	require.EqualError(t, err, "memory id is required")
	_, err = store.PatchMemory(context.Background(), statememory.MemoryPatch{ID: "missing"})
	require.EqualError(t, err, "memory item not found")
	require.EqualError(t, store.DeleteMemory(context.Background(), statememory.MemoryDeleteRequest{}), "memory id is required")
	require.NoError(t, store.DeleteMemory(context.Background(), statememory.MemoryDeleteRequest{ID: "missing"}))
	require.EqualError(t, store.HardDeleteMemory(context.Background(), statememory.MemoryDeleteRequest{}), "memory id is required")
	require.NoError(t, store.HardDeleteMemory(context.Background(), statememory.MemoryDeleteRequest{ID: "missing"}))
}

func memoryItemIDs(items []statememory.MemoryItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}

	return ids
}
