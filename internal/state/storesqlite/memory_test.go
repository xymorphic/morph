package storesqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	statememory "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/internal/state/search"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSQLiteMemoryStore_MigrationSearchWriteDeleteAndSourceLinks(t *testing.T) {
	db := openMemoryTestDB(t)
	store, err := NewStoreFromDB(db)
	require.NoError(t, err)
	require.True(t, db.Migrator().HasTable(&memoryItemModel{}))
	require.True(t, db.Migrator().HasTable(&memoryItemTagModel{}))
	require.True(t, hasSQLiteTable(t, db, memorySearchTable))
	require.True(t, db.Migrator().HasIndex(&memoryItemModel{}, "idx_memory_items_kind"))
	require.True(t, db.Migrator().HasIndex(&memoryItemModel{}, "idx_memory_items_status"))
	require.True(t, db.Migrator().HasIndex(&memoryItemModel{}, "idx_memory_items_kind_status"))
	require.True(t, db.Migrator().HasIndex(&memoryItemModel{}, "idx_memory_items_updated_at"))
	require.True(t, db.Migrator().HasIndex(&memoryItemModel{}, "idx_memory_items_source_session_id"))
	require.True(t, db.Migrator().HasIndex(&memoryItemModel{}, "idx_memory_items_reflected"))
	require.True(t, db.Migrator().HasIndex(&memoryItemModel{}, "idx_memory_items_promotion_evaluated_at"))
	require.True(t, db.Migrator().HasIndex(&memoryItemTagModel{}, "idx_memory_item_tags_tag"))

	createdAt := time.Date(2026, 4, 30, 9, 0, 0, 0, time.UTC)
	evaluatedAt := createdAt.Add(time.Hour)
	item, err := store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:                   "  mem_one  ",
		Kind:                 statememory.MemoryKindSemantic,
		Status:               statememory.MemoryStatusActive,
		Title:                "Go preference",
		Text:                 "Use focused tests",
		Tags:                 []string{"Go", "Style"},
		CreatedAt:            createdAt,
		PromotionEvaluatedAt: evaluatedAt,
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
	require.Equal(t, evaluatedAt, item.PromotionEvaluatedAt)
	var record memoryItemModel
	require.NoError(t, store.db.First(&record, "id = ?", item.ID).Error)
	require.Equal(t, "session", record.SourceSessionID)
	require.True(t, record.Reflected)
	require.NotNil(t, record.PromotionEvaluatedAt)
	require.Equal(t, evaluatedAt, record.PromotionEvaluatedAt.UTC())

	result, err := store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Text: "focused",
		Tags: []string{"go", "style"},
	})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	require.Greater(t, result.Hits[0].Score, 0.0)
	require.Equal(t, []uint{1}, result.Hits[0].Item.SourceLinks[0].MessageIDs)
	require.Equal(t, []int{2}, result.Hits[0].Item.SourceLinks[0].Offsets)
	require.Equal(t, "summary", result.Hits[0].Item.SourceLinks[0].SummaryID)
	require.True(t, result.Hits[0].Item.Reflected)

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Reflected: new(false),
	})
	require.NoError(t, err)
	require.Empty(t, result.Hits)

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		PromotionEvaluated:       new(true),
		PromotionEvaluatedAfter:  createdAt,
		PromotionEvaluatedBefore: createdAt.Add(2 * time.Hour),
	})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	require.Equal(t, evaluatedAt, result.Hits[0].Item.PromotionEvaluatedAt)

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Text:                     "focused",
		PromotionEvaluated:       new(true),
		PromotionEvaluatedBefore: createdAt,
	})
	require.NoError(t, err)
	require.Empty(t, result.Hits)

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
	require.Equal(t, []string{"mem_unevaluated"}, sqliteMemoryHitIDs(result.Hits))

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

func TestSQLiteMemoryStore_DefaultsToCandidateAndActiveOnlySearch(t *testing.T) {
	store, err := NewStoreFromDB(openMemoryTestDB(t))
	require.NoError(t, err)

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

func TestSQLiteMemoryStore_VectorSearchFiltersAndResolvesMatches(t *testing.T) {
	store, vectorStore := sqliteMemoryVectorUnitStore(t)
	require.NoError(t, store.db.Create(&memoryItemModel{
		ID:              "mem_keep",
		SourceSessionID: "session-a",
		Kind:            string(statememory.MemoryKindSemantic),
		Status:          string(statememory.MemoryStatusActive),
		Title:           "Keep",
		Text:            "Retention renewal workflow.",
		TagsJSON:        `["go"]`,
		MetadataJSON:    `{"source_session_id":"session-a"}`,
		SourceLinksJSON: `null`,
		CreatedAt:       time.Date(2026, 4, 30, 9, 0, 0, 0, time.UTC),
		UpdatedAt:       time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC),
	}).Error)
	require.NoError(t, store.db.Create(&memoryItemTagModel{MemoryID: "mem_keep", Tag: "go"}).Error)
	require.NoError(t, store.db.Create(&memoryItemModel{
		ID:              "mem_candidate",
		Kind:            string(statememory.MemoryKindSemantic),
		Status:          string(statememory.MemoryStatusCandidate),
		Title:           "Candidate",
		Text:            "Retention draft.",
		TagsJSON:        `["go"]`,
		MetadataJSON:    `null`,
		SourceLinksJSON: `null`,
		CreatedAt:       time.Date(2026, 4, 30, 9, 0, 0, 0, time.UTC),
		UpdatedAt:       time.Date(2026, 4, 30, 9, 30, 0, 0, time.UTC),
	}).Error)
	vectorStore.searchMatches = []search.VectorSearchMatch{
		{Record: search.VectorRecord{SourceID: "bad"}, Score: 1},
		{Record: search.VectorRecord{SourceID: search.StableMemoryItemID("mem_keep")}, Score: 0.9},
		{Record: search.VectorRecord{SourceID: search.StableMemoryItemID("mem_keep")}, Score: 0.8},
		{Record: search.VectorRecord{SourceID: search.StableMemoryItemID("mem_candidate")}, Score: 0.7},
		{Record: search.VectorRecord{SourceID: search.StableMemoryItemID("mem_missing")}, Score: 0.6},
	}

	hits, err := store.searchMemoryVector(context.Background(), statememory.MemorySearchQuery{
		Text:      "retention",
		IDs:       []string{"mem_keep", "mem_missing"},
		SessionID: "session-a",
		Tags:      []string{"go"},
	}, 10)

	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, "mem_keep", hits[0].Item.ID)
	require.Equal(t, 0.9, hits[0].Score)
	require.Len(t, vectorStore.searches, 1)
	require.Equal(t, []string{search.StableMemoryItemID("mem_keep")}, vectorStore.searches[0].Filter.SourceIDs)
	require.Equal(t, []string{
		"memory_session:session-a",
		"memory_status:active",
		"memory_tag:go",
	}, vectorStore.searches[0].Filter.Tags)
}

func TestSQLiteMemoryStore_MemoryVectorSearchEnabled(t *testing.T) {
	var nilStore *Store
	require.False(t, nilStore.memoryVectorSearchEnabled(statememory.MemorySearchQuery{Text: "needle"}))

	store := &Store{}
	require.False(t, store.memoryVectorSearchEnabled(statememory.MemorySearchQuery{Text: "needle"}))

	store, _ = sqliteMemoryVectorUnitStore(t)
	require.False(t, store.memoryVectorSearchEnabled(statememory.MemorySearchQuery{Text: " "}))
	require.True(t, store.memoryVectorSearchEnabled(statememory.MemorySearchQuery{Text: "needle"}))
}

func TestSQLiteMemoryStore_SearchMemoryHybridErrorAndDegradePaths(t *testing.T) {
	store, _ := sqliteMemoryVectorUnitStore(t)
	require.NoError(t, store.db.Migrator().DropTable(&memoryItemModel{}))

	result, err := store.searchMemoryHybrid(context.Background(), statememory.MemorySearchQuery{})

	require.Error(t, err)
	require.Empty(t, result)

	store, vectorStore := sqliteMemoryVectorUnitStore(t)
	store.vectors.Required = false
	vectorStore.searchErr = errors.New("vector failed")

	result, err = store.searchMemoryHybrid(context.Background(), statememory.MemorySearchQuery{Text: "!!!"})

	require.NoError(t, err)
	require.Empty(t, result.Hits)

	store, vectorStore = sqliteMemoryVectorUnitStore(t)
	vectorStore.searchErr = errors.New("vector failed")

	result, err = store.searchMemoryHybrid(context.Background(), statememory.MemorySearchQuery{Text: "!!!"})

	require.EqualError(t, err, "vector failed")
	require.Empty(t, result)
}

func TestSQLiteMemoryStore_SearchMemoryUsesVectorWhenLexicalQueryHasNoTokens(t *testing.T) {
	store, vectorStore := sqliteMemoryVectorUnitStore(t)
	now := time.Date(2026, 4, 30, 9, 0, 0, 0, time.UTC)
	for _, record := range []memoryItemModel{
		{
			ID:              "mem_lexical",
			Kind:            string(statememory.MemoryKindSemantic),
			Status:          string(statememory.MemoryStatusActive),
			Title:           "Lexical",
			Text:            "Lexical only memory.",
			TagsJSON:        `null`,
			MetadataJSON:    `null`,
			SourceLinksJSON: `null`,
			CreatedAt:       now,
			UpdatedAt:       now,
		},
		{
			ID:              "mem_vector",
			Kind:            string(statememory.MemoryKindSemantic),
			Status:          string(statememory.MemoryStatusActive),
			Title:           "Vector",
			Text:            "Vector only memory.",
			TagsJSON:        `null`,
			MetadataJSON:    `null`,
			SourceLinksJSON: `null`,
			CreatedAt:       now,
			UpdatedAt:       now.Add(time.Minute),
		},
	} {
		require.NoError(t, store.db.Create(&record).Error)
	}
	vectorStore.searchMatches = []search.VectorSearchMatch{{
		Record: search.VectorRecord{SourceID: search.StableMemoryItemID("mem_vector")},
		Score:  0.99,
	}}

	result, err := store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Text:  "!!!",
		Kinds: []statememory.MemoryKind{statememory.MemoryKindSemantic},
		Limit: 2,
	})

	require.NoError(t, err)
	require.Equal(t, []string{"mem_vector"}, sqliteMemoryHitIDs(result.Hits))
	require.Len(t, vectorStore.searches, 1)
}

func TestSQLiteMemoryStore_VectorSearchPushesTagGroupsAndSkipsEmptyIDFilter(t *testing.T) {
	store, vectorStore := sqliteMemoryVectorUnitStore(t)
	require.NoError(t, store.db.Create(&memoryItemModel{
		ID:              "mem_semantic",
		Kind:            string(statememory.MemoryKindSemantic),
		Status:          string(statememory.MemoryStatusActive),
		Title:           "Semantic",
		Text:            "Retention workflow.",
		TagsJSON:        `null`,
		MetadataJSON:    `null`,
		SourceLinksJSON: `null`,
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}).Error)

	hits, err := store.searchMemoryVector(context.Background(), statememory.MemorySearchQuery{
		Text: "retention",
		Kinds: []statememory.MemoryKind{
			statememory.MemoryKindSemantic,
			statememory.MemoryKindProcedural,
		},
		Statuses: []statememory.MemoryStatus{
			statememory.MemoryStatusActive,
			statememory.MemoryStatusCandidate,
		},
	}, 10)

	require.NoError(t, err)
	require.Empty(t, hits)
	require.Len(t, vectorStore.searches, 1)
	require.Empty(t, vectorStore.searches[0].Filter.SourceIDs)
	require.Empty(t, vectorStore.searches[0].Filter.Tags)
	require.Equal(t, [][]string{
		{"memory_kind:procedural", "memory_kind:semantic"},
		{"memory_status:active", "memory_status:candidate"},
	}, vectorStore.searches[0].Filter.TagGroups)

	vectorStore.searchErr = errors.New("vector search should be skipped")
	hits, err = store.searchMemoryVector(context.Background(), statememory.MemorySearchQuery{
		Text: "retention",
		IDs:  []string{"mem_missing"},
	}, 10)

	require.NoError(t, err)
	require.Empty(t, hits)
	require.Len(t, vectorStore.searches, 1)
}

func TestSQLiteMemoryStore_VectorSearchValidationAndResolutionErrors(t *testing.T) {
	store, _ := sqliteMemoryVectorUnitStore(t)
	store.vectors.Provider = &sqliteTestEmbeddingProvider{err: errors.New("embed failed")}

	hits, err := store.searchMemoryVector(context.Background(), statememory.MemorySearchQuery{Text: "retention"}, 10)

	require.EqualError(t, err, "embed failed")
	require.Empty(t, hits)

	store, _ = sqliteMemoryVectorUnitStore(t)
	store.vectors.Provider = &sqliteTestEmbeddingProvider{dimensions: 3, mutate: func(result search.EmbeddingResult) search.EmbeddingResult {
		result.Items = nil
		return result
	}}

	hits, err = store.searchMemoryVector(context.Background(), statememory.MemorySearchQuery{Text: "retention"}, 10)

	require.EqualError(t, err, "embedding result count must match input count")
	require.Empty(t, hits)

	store, vectorStore := sqliteMemoryVectorUnitStore(t)
	vectorStore.searchErr = errors.New("search failed")

	hits, err = store.searchMemoryVector(context.Background(), statememory.MemorySearchQuery{Text: "retention"}, 10)

	require.EqualError(t, err, "search failed")
	require.Empty(t, hits)

	store, _ = sqliteMemoryVectorUnitStore(t)
	require.NoError(t, store.db.Migrator().DropTable(&memoryItemModel{}))
	hits, err = store.memoryVectorMatchesToHits(context.Background(), statememory.MemorySearchQuery{}, []search.VectorSearchMatch{{
		Record: search.VectorRecord{SourceID: search.StableMemoryItemID("mem_missing")},
		Score:  1,
	}})

	require.Error(t, err)
	require.Empty(t, hits)
	records, err := store.getMemoryModelsByID(context.Background(), nil)
	require.NoError(t, err)
	require.Nil(t, records)
}

func TestSQLiteMemoryStore_VectorIndexAndDeleteHelpers(t *testing.T) {
	t.Run("skip when disabled empty or missing id", func(t *testing.T) {
		var nilStore *Store
		require.NoError(t, nilStore.indexMemoryVector(context.Background(), statememory.MemoryItem{ID: "mem_nil", Text: "text"}))
		require.NoError(t, nilStore.deleteMemoryVector(context.Background(), "mem_nil"))

		store := &Store{}
		require.NoError(t, store.indexMemoryVector(context.Background(), statememory.MemoryItem{ID: "mem_disabled", Text: "text"}))
		require.NoError(t, store.deleteMemoryVector(context.Background(), "mem_disabled"))

		store, _ = sqliteMemoryVectorUnitStore(t)
		require.NoError(t, store.indexMemoryVector(context.Background(), statememory.MemoryItem{ID: "mem_empty"}))
		require.NoError(t, store.deleteMemoryVector(context.Background(), " "))
	})

	t.Run("indexes tags and propagates errors", func(t *testing.T) {
		store, vectorStore := sqliteMemoryVectorUnitStore(t)
		err := store.indexMemoryVector(context.Background(), statememory.MemoryItem{
			ID:     "mem_index",
			Kind:   statememory.MemoryKindSemantic,
			Status: statememory.MemoryStatusActive,
			Title:  "Title",
			Text:   "Retention renewal workflow.",
			Tags:   []string{"Go"},
			Metadata: map[string]string{
				"source_session_id": "session-a",
			},
			Reflected: true,
		})
		require.NoError(t, err)
		require.Len(t, vectorStore.upserts, 1)
		require.Equal(t, search.StableMemoryItemID("mem_index"), vectorStore.upserts[0][0].SourceID)
		require.Equal(t, "session-a", vectorStore.upserts[0][0].SessionID)
		require.Equal(t, []string{
			"memory_kind:semantic",
			"memory_reflected:true",
			"memory_session:session-a",
			"memory_status:active",
			"memory_tag:go",
		}, vectorStore.upserts[0][0].Tags)

		store.vectors.Provider = &sqliteTestEmbeddingProvider{err: errors.New("embed failed")}
		err = store.indexMemoryVector(context.Background(), statememory.MemoryItem{ID: "mem_embed", Text: "text"})
		require.EqualError(t, err, "embed failed")

		store.vectors.Provider = &sqliteTestEmbeddingProvider{dimensions: 3, mutate: func(result search.EmbeddingResult) search.EmbeddingResult {
			result.Items = nil
			return result
		}}
		err = store.indexMemoryVector(context.Background(), statememory.MemoryItem{ID: "mem_malformed", Text: "text"})
		require.EqualError(t, err, "embedding result count must match input count")

		store.vectors.Provider = &sqliteTestEmbeddingProvider{dimensions: 3}
		vectorStore.upsertErr = errors.New("upsert failed")
		err = store.indexMemoryVector(context.Background(), statememory.MemoryItem{ID: "mem_upsert", Text: "text"})
		require.EqualError(t, err, "upsert failed")

		vectorStore.upsertErr = nil
		vectorStore.deleteErr = errors.New("delete failed")
		err = store.deleteMemoryVector(context.Background(), "mem_delete")
		require.EqualError(t, err, "delete failed")
	})
}

func TestSQLiteMemoryVectorConversionMergeAndSortHelpers(t *testing.T) {
	hits, err := memorySearchRecordsToSearchHits([]memorySearchRecord{{
		ID:              "mem_ok",
		Kind:            string(statememory.MemoryKindSemantic),
		Status:          string(statememory.MemoryStatusActive),
		TagsJSON:        `["go"]`,
		MetadataJSON:    `null`,
		SourceLinksJSON: `null`,
		Score:           0.7,
	}})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, "mem_ok", hits[0].Item.ID)
	require.Equal(t, 0.7, hits[0].Score)

	_, err = memorySearchRecordsToSearchHits([]memorySearchRecord{{
		ID:              "mem_bad",
		TagsJSON:        "{",
		MetadataJSON:    "null",
		SourceLinksJSON: "null",
	}})
	require.Error(t, err)

	now := time.Date(2026, 4, 30, 9, 0, 0, 0, time.UTC)
	lexical := []statememory.MemorySearchHit{
		{Item: statememory.MemoryItem{ID: "mem_a", UpdatedAt: now}, Score: 0.5, LexicalScore: 0.5},
		{Item: statememory.MemoryItem{ID: "mem_b", UpdatedAt: now.Add(time.Minute)}, Score: 0.4, LexicalScore: 0.4},
	}
	vector := []statememory.MemorySearchHit{
		{Item: statememory.MemoryItem{ID: "mem_a", UpdatedAt: now}, Score: 0.9, VectorScore: 0.9},
		{Item: statememory.MemoryItem{ID: "mem_c", UpdatedAt: now}, Score: 0.9, VectorScore: 0.9},
		{Item: statememory.MemoryItem{}, Score: 1},
	}

	merged := mergeMemoryHits(lexical, vector)

	require.Equal(t, []string{"mem_a", "mem_c", "mem_b"}, sqliteMemoryHitIDs(merged))
	require.Equal(t, 0.9, merged[0].Score)
	require.Equal(t, 0.5, merged[0].LexicalScore)
	require.Equal(t, 0.9, merged[0].VectorScore)

	hits = []statememory.MemorySearchHit{
		{Item: statememory.MemoryItem{ID: "mem_b", UpdatedAt: now}, Score: 1},
		{Item: statememory.MemoryItem{ID: "mem_c", UpdatedAt: now.Add(time.Minute)}, Score: 1},
		{Item: statememory.MemoryItem{ID: "mem_a", UpdatedAt: now}, Score: 1},
	}
	sortMemoryHits(hits)
	require.Equal(t, []string{"mem_c", "mem_a", "mem_b"}, sqliteMemoryHitIDs(hits))
}

func TestSQLiteMemoryVectorHelpers(t *testing.T) {
	reflected := false
	query := statememory.MemorySearchQuery{
		SessionID: " session ",
		Kinds: []statememory.MemoryKind{
			statememory.MemoryKindSemantic,
			statememory.MemoryKindProcedural,
		},
		Statuses: []statememory.MemoryStatus{
			statememory.MemoryStatusActive,
			statememory.MemoryStatusCandidate,
		},
		Tags:      []string{" Go ", "style"},
		Reflected: &reflected,
	}

	require.Equal(t, []string{
		"memory_reflected:false",
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
	require.False(t, checkMemoryQueryNeedsSourceIDFilter(statememory.MemorySearchQuery{Kinds: []statememory.MemoryKind{statememory.MemoryKindSemantic}}))

	item := statememory.MemoryItem{
		Kind:   statememory.MemoryKindSemantic,
		Status: statememory.MemoryStatusActive,
		Title:  "Title",
		Text:   "Body",
		Tags:   []string{"Go"},
		SourceLinks: []statememory.MemorySourceLink{{
			SessionID: "source-session",
		}},
	}
	require.Equal(t, "Title\nBody", getMemoryVectorText(item))
	require.Equal(t, "source-session", search.MemoryVectorSessionID(item))
	require.Equal(t, []string{
		"memory_kind:semantic",
		"memory_reflected:false",
		"memory_session:source-session",
		"memory_status:active",
		"memory_tag:go",
	}, search.MemoryVectorTags(item))

	item.Metadata = map[string]string{"source_session_id": "metadata-session"}
	require.Equal(t, "metadata-session", search.MemoryVectorSessionID(item))
	require.Empty(t, search.MemoryVectorSessionID(statememory.MemoryItem{}))
}

func TestSQLiteMemoryStore_ListSessionMemoriesFiltersOrdersLimitsAndClones(t *testing.T) {
	store, err := NewStoreFromDB(openMemoryTestDB(t))
	require.NoError(t, err)

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

	require.NoError(t, store.db.Model(&memoryItemModel{}).Where("id = ?", "mem_source_old").Update("updated_at", time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)).Error)
	require.NoError(t, store.db.Model(&memoryItemModel{}).Where("id = ?", "mem_metadata_new").Update("updated_at", time.Date(2026, 4, 30, 11, 0, 0, 0, time.UTC)).Error)
	require.NoError(t, store.db.Model(&memoryItemModel{}).Where("id = ?", "mem_candidate").Update("updated_at", time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)).Error)

	result, err := store.ListSessionMemories(context.Background(), statememory.SessionMemoryQuery{
		SessionID: statememory.DefaultSessionID,
		Kinds:     []statememory.MemoryKind{statememory.MemoryKindEpisodic},
		Statuses:  []statememory.MemoryStatus{statememory.MemoryStatusActive},
		Limit:     1,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"mem_metadata_new"}, sqliteMemoryItemIDs(result.Items))

	result, err = store.ListSessionMemories(context.Background(), statememory.SessionMemoryQuery{
		SessionID: statememory.DefaultSessionID,
		Statuses:  []statememory.MemoryStatus{statememory.MemoryStatusActive, statememory.MemoryStatusCandidate},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"mem_candidate", "mem_metadata_new", "mem_source_old"}, sqliteMemoryItemIDs(result.Items))
	result.Items[2].SourceLinks[0].Offsets[0] = 99

	result, err = store.ListSessionMemories(context.Background(), statememory.SessionMemoryQuery{
		SessionID: statememory.DefaultSessionID,
		Statuses:  []statememory.MemoryStatus{statememory.MemoryStatusActive},
	})
	require.NoError(t, err)
	require.Equal(t, []int{1}, result.Items[1].SourceLinks[0].Offsets)
}

func TestSQLiteMemoryStore_ListSessionMemoriesUsesSourceSessionColumn(t *testing.T) {
	store, err := NewStoreFromDB(openMemoryTestDB(t))
	require.NoError(t, err)

	require.NoError(t, store.db.Create(&memoryItemModel{
		ID:              "mem_indexed",
		SourceSessionID: statememory.DefaultSessionID,
		Kind:            string(statememory.MemoryKindEpisodic),
		Status:          string(statememory.MemoryStatusActive),
		Title:           "indexed",
		TagsJSON:        "null",
		MetadataJSON:    "null",
		SourceLinksJSON: "null",
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}).Error)

	result, err := store.ListSessionMemories(context.Background(), statememory.SessionMemoryQuery{
		SessionID: statememory.DefaultSessionID,
	})

	require.NoError(t, err)
	require.Equal(t, []string{"mem_indexed"}, sqliteMemoryItemIDs(result.Items))
}

func TestSQLiteMemoryStore_ListSessionMemoriesReturnsDecodeErrors(t *testing.T) {
	store, err := NewStoreFromDB(openMemoryTestDB(t))
	require.NoError(t, err)

	require.NoError(t, store.db.Create(&memoryItemModel{
		ID:              "mem_invalid",
		SourceSessionID: statememory.DefaultSessionID,
		Kind:            string(statememory.MemoryKindEpisodic),
		Status:          string(statememory.MemoryStatusActive),
		Title:           "invalid",
		TagsJSON:        "{",
		MetadataJSON:    "null",
		SourceLinksJSON: "null",
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}).Error)

	_, err = store.ListSessionMemories(context.Background(), statememory.SessionMemoryQuery{
		SessionID: statememory.DefaultSessionID,
	})
	require.Error(t, err)
}

func TestSQLiteMemoryStore_SearchWithNoFTSTokensReturnsNoHits(t *testing.T) {
	store, err := NewStoreFromDB(openMemoryTestDB(t))
	require.NoError(t, err)

	_, err = store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:     "mem_symbols",
		Status: statememory.MemoryStatusActive,
		Text:   "symbols",
	})
	require.NoError(t, err)

	result, err := store.SearchMemory(context.Background(), statememory.MemorySearchQuery{Text: "!!!"})
	require.NoError(t, err)
	require.Empty(t, result.Hits)
}

func TestSQLiteMemoryStore_UpdatePreservesCreatedAtAndReplacesTags(t *testing.T) {
	store, err := NewStoreFromDB(openMemoryTestDB(t))
	require.NoError(t, err)

	createdAt := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	first, err := store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:        "mem_update",
		Status:    statememory.MemoryStatusActive,
		Title:     "First title",
		Text:      "old text",
		Tags:      []string{"old"},
		CreatedAt: createdAt,
	})
	require.NoError(t, err)
	require.Equal(t, createdAt, first.CreatedAt)

	second, err := store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:     "mem_update",
		Status: statememory.MemoryStatusActive,
		Title:  "Second title",
		Text:   "new text",
		Tags:   []string{"new"},
	})
	require.NoError(t, err)
	require.Equal(t, createdAt, second.CreatedAt)
	require.True(t, second.UpdatedAt.After(second.CreatedAt))

	result, err := store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Text: "second",
		Tags: []string{"new"},
	})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{Tags: []string{"old"}})
	require.NoError(t, err)
	require.Empty(t, result.Hits)

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{Text: "old"})
	require.NoError(t, err)
	require.Empty(t, result.Hits)

	require.NoError(t, store.db.Create(&memoryItemModel{
		ID:              "mem_zero_created",
		Status:          string(statememory.MemoryStatusActive),
		TagsJSON:        "null",
		MetadataJSON:    "null",
		SourceLinksJSON: "null",
	}).Error)
	zeroCreated, err := store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:     "mem_zero_created",
		Status: statememory.MemoryStatusActive,
		Text:   "zero created",
	})
	require.NoError(t, err)
	require.False(t, zeroCreated.CreatedAt.IsZero())
}

func TestSQLiteMemoryStore_PatchMemoryUpdatesOnlyRequestedFields(t *testing.T) {
	store, err := NewStoreFromDB(openMemoryTestDB(t))
	require.NoError(t, err)

	evaluatedAt := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	item, err := store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:                   "mem_patch",
		Kind:                 statememory.MemoryKindEpisodic,
		Status:               statememory.MemoryStatusCandidate,
		Title:                "Original title",
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

	var record memoryItemModel
	require.NoError(t, store.db.First(&record, "id = ?", item.ID).Error)
	require.Equal(t, string(statememory.MemoryStatusActive), record.Status)
	require.True(t, record.Reflected)
	require.Equal(t, statememory.DefaultSessionID, record.SourceSessionID)
	require.Nil(t, record.PromotionEvaluatedAt)

	result, err := store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Text: "durable",
		Tags: []string{"patch"},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"mem_patch"}, sqliteMemoryHitIDs(result.Hits))

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{Text: "Original"})
	require.NoError(t, err)
	require.Empty(t, result.Hits)

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{PromotionEvaluated: new(false)})
	require.NoError(t, err)
	require.Equal(t, []string{"mem_patch"}, sqliteMemoryHitIDs(result.Hits))

	sessionResult, err := store.ListSessionMemories(context.Background(), statememory.SessionMemoryQuery{
		SessionID: statememory.DefaultSessionID,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"mem_patch"}, sqliteMemoryItemIDs(sessionResult.Items))
}

func TestSQLiteMemoryStore_PatchMemoryDeletesVectorForDeletedStatus(t *testing.T) {
	store, err := NewStoreFromDB(openMemoryTestDB(t))
	require.NoError(t, err)
	vectorStore := &sqliteTestVectorStore{}
	require.NoError(t, store.ConfigureVectorStore(VectorStoreOptions{
		Embedder:       &sqliteTestEmbeddingProvider{dimensions: 3},
		VectorStore:    vectorStore,
		EmbeddingModel: "text-embedding-test",
		Required:       true,
	}))

	_, err = store.UpsertMemory(context.Background(), statememory.MemoryItem{
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

	require.Len(t, vectorStore.upserts, 1)
	require.Len(t, vectorStore.deletes, 1)
	require.Equal(t, search.SourceKindMemoryItem, vectorStore.deletes[0].SourceKind)
	require.Equal(t, []string{search.StableMemoryItemID("mem_delete_vector")}, vectorStore.deletes[0].SourceIDs)
}

func TestSQLiteMemoryStore_PatchMemoryUpdatesKindConfidenceAndPropagatesVectorError(t *testing.T) {
	store, err := NewStoreFromDB(openMemoryTestDB(t))
	require.NoError(t, err)
	vectorStore := &sqliteTestVectorStore{}
	require.NoError(t, store.ConfigureVectorStore(VectorStoreOptions{
		Embedder:       &sqliteTestEmbeddingProvider{dimensions: 3},
		VectorStore:    vectorStore,
		EmbeddingModel: "text-embedding-test",
		Required:       true,
	}))

	_, err = store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:         "mem_patch_kind",
		Kind:       statememory.MemoryKindEpisodic,
		Status:     statememory.MemoryStatusActive,
		Text:       "Patch vector memory",
		Confidence: 0.2,
	})
	require.NoError(t, err)

	kind := statememory.MemoryKindProcedural
	confidence := 0.95
	vectorStore.upsertErr = errors.New("patch vector failed")
	_, err = store.PatchMemory(context.Background(), statememory.MemoryPatch{
		ID:         "mem_patch_kind",
		Kind:       &kind,
		Confidence: &confidence,
	})
	require.EqualError(t, err, "patch vector failed")

	var record memoryItemModel
	require.NoError(t, store.db.First(&record, "id = ?", "mem_patch_kind").Error)
	require.Equal(t, string(statememory.MemoryKindProcedural), record.Kind)
	require.Equal(t, 0.95, record.Confidence)
}

func TestSQLiteMemoryStore_SearchFiltersKindsStatusesTagsAndLimit(t *testing.T) {
	store, err := NewStoreFromDB(openMemoryTestDB(t))
	require.NoError(t, err)

	now := time.Date(2026, 4, 30, 8, 0, 0, 0, time.UTC)
	items := []statememory.MemoryItem{
		{
			ID:                   "mem_a",
			Kind:                 statememory.MemoryKindSemantic,
			Status:               statememory.MemoryStatusActive,
			Title:                "Plan plan preference",
			Text:                 "Use phased plans",
			Tags:                 []string{"plan", "go"},
			Metadata:             map[string]string{"source_session_id": statememory.DefaultSessionID},
			Reflected:            true,
			UpdatedAt:            now,
			PromotionEvaluatedAt: now.Add(time.Hour),
		},
		{
			ID:        "mem_b",
			Kind:      statememory.MemoryKindProcedural,
			Status:    statememory.MemoryStatusActive,
			Title:     "Plan procedure",
			Text:      "Review before commit",
			Tags:      []string{"plan", "workflow"},
			Metadata:  map[string]string{"source_session_id": "other"},
			UpdatedAt: now.Add(time.Minute),
		},
		{
			ID:        "mem_c",
			Kind:      statememory.MemoryKindSemantic,
			Status:    statememory.MemoryStatusSuperseded,
			Title:     "Old plan preference",
			Text:      "Superseded text",
			Tags:      []string{"plan"},
			Metadata:  map[string]string{"source_session_id": "other"},
			UpdatedAt: now.Add(2 * time.Minute),
		},
	}
	for _, item := range items {
		_, err := store.UpsertMemory(context.Background(), item)
		require.NoError(t, err)
	}

	result, err := store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Text:      "plan",
		SessionID: statememory.DefaultSessionID,
		Kinds:     []statememory.MemoryKind{statememory.MemoryKindSemantic},
		Statuses:  []statememory.MemoryStatus{statememory.MemoryStatusActive, statememory.MemoryStatusSuperseded},
		Tags:      []string{"plan"},
		Limit:     1,
	})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	require.Equal(t, "mem_a", result.Hits[0].Item.ID)

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		IDs: []string{" mem_b "},
	})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	require.Equal(t, "mem_b", result.Hits[0].Item.ID)

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Text: "plan",
		IDs:  []string{"mem_b"},
	})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	require.Equal(t, "mem_b", result.Hits[0].Item.ID)

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Text:               "plan",
		Reflected:          new(true),
		PromotionEvaluated: new(true),
	})
	require.NoError(t, err)
	require.Equal(t, []string{"mem_a"}, sqliteMemoryHitIDs(result.Hits))

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Text:                     "plan",
		PromotionEvaluated:       new(true),
		PromotionEvaluatedAfter:  now,
		PromotionEvaluatedBefore: now.Add(2 * time.Hour),
	})
	require.NoError(t, err)
	require.Equal(t, []string{"mem_a"}, sqliteMemoryHitIDs(result.Hits))

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Text:               "plan",
		PromotionEvaluated: new(false),
	})
	require.NoError(t, err)
	require.Equal(t, []string{"mem_b"}, sqliteMemoryHitIDs(result.Hits))

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		IDs: []string{"mem_missing"},
	})
	require.NoError(t, err)
	require.Empty(t, result.Hits)

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Kinds: []statememory.MemoryKind{statememory.MemoryKindProcedural},
	})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	require.Equal(t, "mem_b", result.Hits[0].Item.ID)

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		SessionID: statememory.DefaultSessionID,
		Kinds:     []statememory.MemoryKind{statememory.MemoryKindSemantic},
		Statuses:  []statememory.MemoryStatus{statememory.MemoryStatusActive, statememory.MemoryStatusSuperseded},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"mem_a"}, sqliteMemoryHitIDs(result.Hits))
}

func TestSQLiteMemoryStore_FTSIndexesMemoryFields(t *testing.T) {
	store, err := NewStoreFromDB(openMemoryTestDB(t))
	require.NoError(t, err)

	items := []statememory.MemoryItem{
		{
			ID:     "mem_title",
			Kind:   statememory.MemoryKindSemantic,
			Status: statememory.MemoryStatusActive,
			Title:  "Hydra planning rule",
			Text:   "Ordinary body",
		},
		{
			ID:     "mem_tag",
			Kind:   statememory.MemoryKindEpisodic,
			Status: statememory.MemoryStatusActive,
			Title:  "Meeting note",
			Text:   "No special body",
			Tags:   []string{"lexicaltag"},
		},
		{
			ID:       "mem_metadata",
			Kind:     statememory.MemoryKindProcedural,
			Status:   statememory.MemoryStatusActive,
			Title:    "Workflow",
			Text:     "Steps",
			Metadata: map[string]string{"workspace": "northstar"},
		},
	}
	for _, item := range items {
		_, err := store.UpsertMemory(context.Background(), item)
		require.NoError(t, err)
	}

	assertMemorySearchIDs(t, store, "hydra", []string{"mem_title"})
	assertMemorySearchIDs(t, store, "lexicaltag", []string{"mem_tag"})
	assertMemorySearchIDs(t, store, "northstar", []string{"mem_metadata"})
	assertMemorySearchIDs(t, store, "episodic", []string{"mem_tag"})
}

func TestSQLiteMemoryStore_SearchMemoryRelaxesQuestionAndPreferenceTerms(t *testing.T) {
	store, err := NewStoreFromDB(openMemoryTestDB(t))
	require.NoError(t, err)

	_, err = store.UpsertMemory(context.Background(), statememory.MemoryItem{
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
	require.Contains(t, sqliteMemoryHitIDs(result.Hits), "mem_color_preference")

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Text:  "alpha beta gamma delta color",
		Limit: 3,
	})
	require.NoError(t, err)
	require.Empty(t, result.Hits)

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Text: "go",
	})
	require.NoError(t, err)
	require.Empty(t, result.Hits)

	result, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Text: "!!!",
	})
	require.NoError(t, err)
	require.Empty(t, result.Hits)
	require.Empty(t, buildRelaxedMemoryFTSSearchQuery("!!"))

	ranked := getCoverageRankedMemorySearchRecords([]memorySearchRecord{
		{ID: "mem_b", Title: "preferred color", Score: 1},
		{ID: "mem_a", Title: "preferred color blue", Score: 1},
		{ID: "mem_weak", Title: "color", Score: 1},
	}, "preferred color blue shade tone", 0)
	require.Equal(t, []string{"mem_a", "mem_b"}, sqliteMemorySearchRecordIDs(ranked))
	require.InDelta(t, 1.6, ranked[0].Score, 0.0001)
	require.InDelta(t, 1.4, ranked[1].Score, 0.0001)

	ranked = getCoverageRankedMemorySearchRecords([]memorySearchRecord{
		{ID: "mem_old", Title: "preferred color", Score: 1, UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		{ID: "mem_new", Title: "preferred color", Score: 1, UpdatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
		{ID: "mem_best", Title: "preferred color", Score: 2, UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
	}, "preferred color", 1)
	require.Equal(t, []string{"mem_best"}, sqliteMemorySearchRecordIDs(ranked))

	ranked = getCoverageRankedMemorySearchRecords([]memorySearchRecord{
		{ID: "mem_old", Title: "preferred color", Score: 1, UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		{ID: "mem_new", Title: "preferred color", Score: 1, UpdatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
	}, "preferred color", 0)
	require.Equal(t, []string{"mem_new", "mem_old"}, sqliteMemorySearchRecordIDs(ranked))

	ranked = getCoverageRankedMemorySearchRecords([]memorySearchRecord{
		{ID: "mem_b", Title: "preferred color", Score: 1},
		{ID: "mem_a", Title: "preferred color", Score: 1},
	}, "preferred color", 0)
	require.Equal(t, []string{"mem_a", "mem_b"}, sqliteMemorySearchRecordIDs(ranked))

	require.Empty(t, getCoverageRankedMemorySearchRecords(nil, "preferred color blue", 10))

	require.Empty(t, getCoverageRankedMemorySearchRecords([]memorySearchRecord{
		{ID: "mem_weak", Title: "color", Score: 1},
	}, "alpha beta gamma delta color", 10))
}

func TestSQLiteMemoryStore_HardDeleteMemoryRemovesRecordSearchRowsTagsAndVector(t *testing.T) {
	store, err := NewStoreFromDB(openMemoryTestDB(t))
	require.NoError(t, err)
	item, err := store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:     "mem_hard_delete",
		Kind:   statememory.MemoryKindSemantic,
		Status: statememory.MemoryStatusCandidate,
		Text:   "Hard deleted sqlite candidate should disappear.",
		Tags:   []string{"cleanup"},
	})
	require.NoError(t, err)

	require.NoError(t, store.HardDeleteMemory(context.Background(), statememory.MemoryDeleteRequest{ID: item.ID}))
	result, err := store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		IDs:      []string{item.ID},
		Statuses: []statememory.MemoryStatus{statememory.MemoryStatusCandidate, statememory.MemoryStatusDeleted},
	})
	require.NoError(t, err)
	require.Empty(t, result.Hits)

	var itemCount int64
	require.NoError(t, store.db.Model(&memoryItemModel{}).Where("id = ?", item.ID).Count(&itemCount).Error)
	require.Zero(t, itemCount)
	var tagCount int64
	require.NoError(t, store.db.Model(&memoryItemTagModel{}).Where("memory_id = ?", item.ID).Count(&tagCount).Error)
	require.Zero(t, tagCount)
}

func TestSQLiteMemoryStore_BM25RanksByLexicalRelevanceBeforeRecency(t *testing.T) {
	store, err := NewStoreFromDB(openMemoryTestDB(t))
	require.NoError(t, err)

	_, err = store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:     "mem_strong",
		Kind:   statememory.MemoryKindSemantic,
		Status: statememory.MemoryStatusActive,
		Title:  "Needle needle needle",
		Text:   "needle needle needle needle",
	})
	require.NoError(t, err)

	_, err = store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:     "mem_recent",
		Kind:   statememory.MemoryKindSemantic,
		Status: statememory.MemoryStatusActive,
		Title:  "Needle",
		Text:   "plain",
	})
	require.NoError(t, err)

	result, err := store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Text:  "needle",
		Limit: 1,
	})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	require.Equal(t, "mem_strong", result.Hits[0].Item.ID)
}

func TestSQLiteMemoryStore_ReranksBeforeLimiting(t *testing.T) {
	store, err := NewStoreFromDB(openMemoryTestDB(t))
	require.NoError(t, err)

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
	require.Equal(t, 1.0, result.Hits[0].Item.Confidence)
}

func TestSQLiteMemoryStore_ValidationAndDatabaseErrors(t *testing.T) {
	var nilStore *Store
	_, err := nilStore.SearchMemory(context.Background(), statememory.MemorySearchQuery{})
	require.EqualError(t, err, "store is required")
	_, err = nilStore.ListSessionMemories(context.Background(), statememory.SessionMemoryQuery{})
	require.EqualError(t, err, "store is required")
	_, err = (&Store{}).UpsertMemory(context.Background(), statememory.MemoryItem{})
	require.EqualError(t, err, "store is required")
	_, err = (&Store{}).PatchMemory(context.Background(), statememory.MemoryPatch{})
	require.EqualError(t, err, "store is required")
	require.EqualError(t, nilStore.DeleteMemory(context.Background(), statememory.MemoryDeleteRequest{}), "store is required")
	require.EqualError(t, nilStore.HardDeleteMemory(context.Background(), statememory.MemoryDeleteRequest{}), "store is required")

	store, err := NewStoreFromDB(openMemoryTestDB(t))
	require.NoError(t, err)
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

	searchErr := errors.New("memory search failed")
	require.NoError(t, store.db.Callback().Query().Before("gorm:query").Register("test:memory-search-error", func(tx *gorm.DB) {
		_ = tx.AddError(searchErr)
	}))
	_, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{})
	require.ErrorIs(t, err, searchErr)
	_, err = store.ListSessionMemories(context.Background(), statememory.SessionMemoryQuery{SessionID: statememory.DefaultSessionID})
	require.ErrorIs(t, err, searchErr)
	require.NoError(t, store.db.Callback().Query().Remove("test:memory-search-error"))

	upsertErr := errors.New("memory existing lookup failed")
	require.NoError(t, store.db.Callback().Query().Before("gorm:query").Register("test:memory-upsert-query-error", func(tx *gorm.DB) {
		_ = tx.AddError(upsertErr)
	}))
	_, err = store.UpsertMemory(context.Background(), statememory.MemoryItem{ID: "mem_error"})
	require.ErrorIs(t, err, upsertErr)
	require.NoError(t, store.db.Callback().Query().Remove("test:memory-upsert-query-error"))

	deleteErr := errors.New("memory delete failed")
	require.NoError(t, store.db.Callback().Update().Before("gorm:update").Register("test:memory-delete-error", func(tx *gorm.DB) {
		_ = tx.AddError(deleteErr)
	}))
	err = store.DeleteMemory(context.Background(), statememory.MemoryDeleteRequest{ID: "mem_error"})
	require.ErrorIs(t, err, deleteErr)
	require.NoError(t, store.db.Callback().Update().Remove("test:memory-delete-error"))

	hardDeleteErr := errors.New("memory hard delete failed")
	require.NoError(t, store.db.Callback().Delete().Before("gorm:delete").Register("test:memory-hard-delete-error", func(tx *gorm.DB) {
		if callbackTable(tx) == "memory_items" {
			_ = tx.AddError(hardDeleteErr)
		}
	}))
	err = store.HardDeleteMemory(context.Background(), statememory.MemoryDeleteRequest{ID: "mem_error"})
	require.ErrorIs(t, err, hardDeleteErr)
	require.NoError(t, store.db.Callback().Delete().Remove("test:memory-hard-delete-error"))

	tagDeleteErr := errors.New("memory hard delete tag failed")
	require.NoError(t, store.db.Callback().Delete().Before("gorm:delete").Register("test:memory-hard-delete-tag-error", func(tx *gorm.DB) {
		if callbackTable(tx) == "memory_item_tags" {
			_ = tx.AddError(tagDeleteErr)
		}
	}))
	err = store.HardDeleteMemory(context.Background(), statememory.MemoryDeleteRequest{ID: "mem_error"})
	require.ErrorIs(t, err, tagDeleteErr)
	require.NoError(t, store.db.Callback().Delete().Remove("test:memory-hard-delete-tag-error"))

	require.NoError(t, deleteMemorySearchRow(nil, "mem"))
}

func TestSQLiteMemoryStore_HardDeleteMemoryReturnsSearchRowDeleteError(t *testing.T) {
	store, err := NewStoreFromDB(openMemoryTestDB(t))
	require.NoError(t, err)
	_, err = store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:     "mem_search_row_error",
		Status: statememory.MemoryStatusCandidate,
		Text:   "search row delete error",
	})
	require.NoError(t, err)
	require.NoError(t, store.db.Exec(`DROP TABLE `+memorySearchTable).Error)

	err = store.HardDeleteMemory(context.Background(), statememory.MemoryDeleteRequest{ID: "mem_search_row_error"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to delete memory search row")
}

func TestSQLiteMemoryStore_PatchTransactionErrors(t *testing.T) {
	store, err := NewStoreFromDB(openMemoryTestDB(t))
	require.NoError(t, err)

	_, err = store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:     "mem_patch_error",
		Status: statememory.MemoryStatusActive,
		Tags:   []string{"old"},
		Text:   "patch error",
	})
	require.NoError(t, err)

	queryErr := errors.New("memory patch query failed")
	require.NoError(t, store.db.Callback().Query().Before("gorm:query").Register("test:memory-patch-query-error", func(tx *gorm.DB) {
		_ = tx.AddError(queryErr)
	}))
	_, err = store.PatchMemory(context.Background(), statememory.MemoryPatch{ID: "mem_patch_error"})
	require.ErrorIs(t, err, queryErr)
	require.NoError(t, store.db.Callback().Query().Remove("test:memory-patch-query-error"))

	require.NoError(t, store.db.Model(&memoryItemModel{}).
		Where("id = ?", "mem_patch_error").
		Update("tags_json", "not-json").
		Error)
	_, err = store.PatchMemory(context.Background(), statememory.MemoryPatch{ID: "mem_patch_error"})
	require.Error(t, err)
	require.NoError(t, store.db.Model(&memoryItemModel{}).
		Where("id = ?", "mem_patch_error").
		Update("tags_json", "[]").
		Error)

	updateErr := errors.New("memory patch update failed")
	require.NoError(t, store.db.Callback().Update().Before("gorm:update").Register("test:memory-patch-update-error", func(tx *gorm.DB) {
		_ = tx.AddError(updateErr)
	}))
	reflected := true
	_, err = store.PatchMemory(context.Background(), statememory.MemoryPatch{
		ID:        "mem_patch_error",
		Reflected: &reflected,
	})
	require.ErrorIs(t, err, updateErr)
	require.NoError(t, store.db.Callback().Update().Remove("test:memory-patch-update-error"))

	tagDeleteErr := errors.New("memory patch tag delete failed")
	require.NoError(t, store.db.Callback().Delete().Before("gorm:delete").Register("test:memory-patch-tag-delete-error", func(tx *gorm.DB) {
		if callbackTable(tx) == "memory_item_tags" {
			_ = tx.AddError(tagDeleteErr)
		}
	}))
	tags := []string{"new"}
	_, err = store.PatchMemory(context.Background(), statememory.MemoryPatch{
		ID:   "mem_patch_error",
		Tags: &tags,
	})
	require.ErrorIs(t, err, tagDeleteErr)
	require.NoError(t, store.db.Callback().Delete().Remove("test:memory-patch-tag-delete-error"))

	tagCreateErr := errors.New("memory patch tag create failed")
	require.NoError(t, store.db.Callback().Create().Before("gorm:create").Register("test:memory-patch-tag-create-error", func(tx *gorm.DB) {
		if callbackTable(tx) == "memory_item_tags" {
			_ = tx.AddError(tagCreateErr)
		}
	}))
	_, err = store.PatchMemory(context.Background(), statememory.MemoryPatch{
		ID:   "mem_patch_error",
		Tags: &tags,
	})
	require.ErrorIs(t, err, tagCreateErr)
	require.NoError(t, store.db.Callback().Create().Remove("test:memory-patch-tag-create-error"))

	require.NoError(t, store.db.Exec("DROP TABLE "+memorySearchTable).Error)
	title := "Missing FTS"
	_, err = store.PatchMemory(context.Background(), statememory.MemoryPatch{
		ID:    "mem_patch_error",
		Title: &title,
	})
	require.ErrorContains(t, err, "failed to delete memory search row")
}

func TestSQLiteMemoryStore_UpsertTransactionErrors(t *testing.T) {
	store, err := NewStoreFromDB(openMemoryTestDB(t))
	require.NoError(t, err)

	saveErr := errors.New("memory save failed")
	require.NoError(t, store.db.Callback().Create().Before("gorm:create").Register("test:memory-save-error", func(tx *gorm.DB) {
		if callbackTable(tx) == "memory_items" {
			_ = tx.AddError(saveErr)
		}
	}))
	_, err = store.UpsertMemory(context.Background(), statememory.MemoryItem{ID: "mem_save_error"})
	require.ErrorIs(t, err, saveErr)
	require.NoError(t, store.db.Callback().Create().Remove("test:memory-save-error"))

	tagDeleteErr := errors.New("memory tag delete failed")
	require.NoError(t, store.db.Callback().Delete().Before("gorm:delete").Register("test:memory-tag-delete-error", func(tx *gorm.DB) {
		if callbackTable(tx) == "memory_item_tags" {
			_ = tx.AddError(tagDeleteErr)
		}
	}))
	_, err = store.UpsertMemory(context.Background(), statememory.MemoryItem{ID: "mem_tag_delete", Tags: []string{"tag"}})
	require.ErrorIs(t, err, tagDeleteErr)
	require.NoError(t, store.db.Callback().Delete().Remove("test:memory-tag-delete-error"))

	tagCreateErr := errors.New("memory tag create failed")
	require.NoError(t, store.db.Callback().Create().Before("gorm:create").Register("test:memory-tag-create-error", func(tx *gorm.DB) {
		if callbackTable(tx) == "memory_item_tags" {
			_ = tx.AddError(tagCreateErr)
		}
	}))
	_, err = store.UpsertMemory(context.Background(), statememory.MemoryItem{ID: "mem_tag_create", Tags: []string{"tag"}})
	require.ErrorIs(t, err, tagCreateErr)
	require.NoError(t, store.db.Callback().Create().Remove("test:memory-tag-create-error"))
}

func TestSQLiteMemoryStore_UpsertVectorError(t *testing.T) {
	store, err := NewStoreFromDB(openMemoryTestDB(t))
	require.NoError(t, err)
	vectorStore := &sqliteTestVectorStore{upsertErr: errors.New("upsert vector failed")}
	require.NoError(t, store.ConfigureVectorStore(VectorStoreOptions{
		Embedder:       &sqliteTestEmbeddingProvider{dimensions: 3},
		VectorStore:    vectorStore,
		EmbeddingModel: "text-embedding-test",
		Required:       true,
	}))

	_, err = store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:     "mem_vector_error",
		Status: statememory.MemoryStatusActive,
		Text:   "vector error",
	})
	require.EqualError(t, err, "upsert vector failed")
}

func TestSQLiteMemoryStore_FTSErrors(t *testing.T) {
	store, err := NewStoreFromDB(openMemoryTestDB(t))
	require.NoError(t, err)
	require.NoError(t, store.db.Exec("DROP TABLE "+memorySearchTable).Error)

	_, err = store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:     "mem_missing_fts",
		Status: statememory.MemoryStatusActive,
		Text:   "missing fts",
	})
	require.ErrorContains(t, err, "failed to delete memory search row")

	_, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{Text: "missing"})
	require.Error(t, err)
}

func TestSQLiteMemoryStore_FTSInsertErrors(t *testing.T) {
	store, err := NewStoreFromDB(openMemoryTestDB(t))
	require.NoError(t, err)
	require.NoError(t, store.db.Exec("DROP TABLE "+memorySearchTable).Error)
	require.NoError(t, store.db.Exec("CREATE TABLE "+memorySearchTable+" (memory_id TEXT)").Error)

	_, err = store.UpsertMemory(context.Background(), statememory.MemoryItem{
		ID:     "mem_bad_fts",
		Status: statememory.MemoryStatusActive,
		Text:   "bad fts",
	})
	require.ErrorContains(t, err, "failed to insert memory search row")
	require.NoError(t, replaceMemorySearchRow(nil, statememory.MemoryItem{}))
}

func TestSQLiteMemoryStore_MalformedJSONReturnsDecodeErrors(t *testing.T) {
	store, err := NewStoreFromDB(openMemoryTestDB(t))
	require.NoError(t, err)

	now := time.Now().UTC()
	require.NoError(t, store.db.Create(&memoryItemModel{
		ID:              "mem_bad_json",
		Status:          string(statememory.MemoryStatusActive),
		TagsJSON:        "not-json",
		MetadataJSON:    "null",
		SourceLinksJSON: "null",
		CreatedAt:       now,
		UpdatedAt:       now,
	}).Error)

	_, err = store.SearchMemory(context.Background(), statememory.MemorySearchQuery{})
	require.Error(t, err)

	_, err = memoryModelToMemoryItem(memoryItemModel{
		TagsJSON:        "[]",
		MetadataJSON:    "not-json",
		SourceLinksJSON: "null",
	})
	require.Error(t, err)

	_, err = memoryModelToMemoryItem(memoryItemModel{
		TagsJSON:        "[]",
		MetadataJSON:    "null",
		SourceLinksJSON: "not-json",
	})
	require.Error(t, err)
}

func TestSQLiteMemoryStore_StorageAndJSONHelpers(t *testing.T) {
	require.EqualError(t, ensureMemoryStorage(nil), "memory db is required")
	require.EqualError(t, ensureMemorySearchIndex(nil), "memory db is required")

	emptyPath := filepath.Join(t.TempDir(), "empty.db")
	require.NoError(t, os.WriteFile(emptyPath, nil, 0o600))
	readonlyEmptyDB, err := gorm.Open(sqlite.Open("file:"+emptyPath+"?mode=ro"), &gorm.Config{})
	require.NoError(t, err)
	err = ensureMemoryStorage(readonlyEmptyDB)
	require.ErrorContains(t, err, "failed to migrate memory db")

	readonlyMemoryPath := filepath.Join(t.TempDir(), "memory.db")
	writableMemoryDB, err := gorm.Open(sqlite.Open(readonlyMemoryPath), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, writableMemoryDB.AutoMigrate(&memoryItemModel{}, &memoryItemTagModel{}))
	writableSQLDB, err := writableMemoryDB.DB()
	require.NoError(t, err)
	require.NoError(t, writableSQLDB.Close())

	readonlyMemoryDB, err := gorm.Open(sqlite.Open("file:"+readonlyMemoryPath+"?mode=ro"), &gorm.Config{})
	require.NoError(t, err)
	err = ensureMemoryStorage(readonlyMemoryDB)
	require.ErrorContains(t, err, "failed to create memory search index")

	require.Equal(t, "null", toJSONString(make(chan int)))

	var metadata map[string]string
	require.NoError(t, fromJSONString("", &metadata))
	require.Nil(t, metadata)
}

func openMemoryTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "memory.db")), &gorm.Config{})
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, sqlDB.Close())
	})

	return db
}

func sqliteMemoryVectorUnitStore(t *testing.T) (*Store, *sqliteTestVectorStore) {
	t.Helper()

	db := openMemoryTestDB(t)
	require.NoError(t, db.AutoMigrate(&memoryItemModel{}, &memoryItemTagModel{}))
	vectorStore := &sqliteTestVectorStore{}

	return &Store{
		db: db,
		vectors: &vectorConfig{
			VectorConfig: search.VectorConfig{
				Provider: &sqliteTestEmbeddingProvider{dimensions: 3},
				Store:    vectorStore,
				Model:    "text-embedding-test",
				Required: true,
			},
		},
	}, vectorStore
}

func callbackTable(tx *gorm.DB) string {
	if tx == nil || tx.Statement == nil {
		return ""
	}
	if tx.Statement.Table != "" {
		return tx.Statement.Table
	}
	if tx.Statement.Schema != nil {
		return tx.Statement.Schema.Table
	}

	return ""
}

func hasSQLiteTable(t *testing.T, db *gorm.DB, name string) bool {
	t.Helper()

	var count int64
	require.NoError(t, db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&count).Error)

	return count > 0
}

func assertMemorySearchIDs(t *testing.T, store *Store, query string, expected []string) {
	t.Helper()

	result, err := store.SearchMemory(context.Background(), statememory.MemorySearchQuery{
		Text:  query,
		Limit: len(expected),
	})
	require.NoError(t, err)
	require.Len(t, result.Hits, len(expected))

	actual := make([]string, 0, len(result.Hits))
	for _, hit := range result.Hits {
		actual = append(actual, hit.Item.ID)
	}
	require.Equal(t, expected, actual)
}

func sqliteMemoryItemIDs(items []statememory.MemoryItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}

	return ids
}

func sqliteMemoryHitIDs(hits []statememory.MemorySearchHit) []string {
	ids := make([]string, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.Item.ID)
	}

	return ids
}

func sqliteMemorySearchRecordIDs(records []memorySearchRecord) []string {
	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.ID)
	}

	return ids
}
