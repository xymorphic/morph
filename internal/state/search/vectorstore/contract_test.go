package vectorstore_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/state/search/vectorstore"
	vectormemory "github.com/xymorphic/morph/internal/state/search/vectorstore/memory"
	vectorsqlite "github.com/xymorphic/morph/internal/state/search/vectorstore/sqlite"
)

func TestVectorStoreContract(t *testing.T) {
	tests := []struct {
		newStore func(t *testing.T) vectorstore.Store
		name     string
	}{
		{
			name: "memory",
			newStore: func(t *testing.T) vectorstore.Store {
				t.Helper()

				return vectormemory.NewStore()
			},
		},
		{
			name: "sqlite",
			newStore: func(t *testing.T) vectorstore.Store {
				t.Helper()

				store, err := vectorsqlite.NewStore(filepath.Join(t.TempDir(), "vectors.db"))
				require.NoError(t, err)

				return store
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+"/upsert search delete and metadata", func(t *testing.T) {
			assertVectorStoreUpsertSearchDeleteAndMetadata(t, tt.newStore(t))
		})
		t.Run(tt.name+"/replace record and isolate vectors", func(t *testing.T) {
			assertVectorStoreReplaceAndCopyIsolation(t, tt.newStore(t))
		})
		t.Run(tt.name+"/filter model dimensions source and metadata", func(t *testing.T) {
			assertVectorStoreFiltersAndMetadata(t, tt.newStore(t))
		})
		t.Run(tt.name+"/validation errors", func(t *testing.T) {
			assertVectorStoreValidationErrors(t, tt.newStore(t))
		})
		t.Run(tt.name+"/empty search for missing index", func(t *testing.T) {
			assertVectorStoreSearchMissingIndex(t, tt.newStore(t))
		})
	}
}

func assertVectorStoreUpsertSearchDeleteAndMetadata(t *testing.T, store vectorstore.Store) {
	t.Helper()

	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	records := []vectorstore.Record{
		testRecord("vec-a", vectorstore.SourceKindSessionMessage, "msg-a", []float64{1, 0, 0}, now),
		testRecord("vec-b", vectorstore.SourceKindSessionMessage, "msg-b", []float64{0.8, 0.2, 0}, now.Add(time.Second)),
		testRecord("vec-c", vectorstore.SourceKindMemoryItem, "mem-c", []float64{0, 1, 0}, now.Add(2*time.Second)),
	}
	records[0].SessionID = "ses-a"
	records[0].Role = "assistant"
	records[1].SessionID = "ses-b"
	records[1].Role = "assistant"
	records[1].ToolName = "process"

	require.NoError(t, store.Upsert(context.Background(), records))

	result, err := store.Search(context.Background(), vectorstore.SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     3,
		QueryVector:    []float64{1, 0, 0},
		Limit:          2,
		Filter: vectorstore.Filter{
			SourceKind: vectorstore.SourceKindSessionMessage,
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-a", "vec-b"}, matchIDs(result.Matches))
	require.Greater(t, result.Matches[0].Score, result.Matches[1].Score)
	require.Equal(t, []float64{1, 0, 0}, result.Matches[0].Record.Vector)
	require.Equal(t, now, result.Matches[0].Record.CreatedAt)

	result, err = store.Search(context.Background(), vectorstore.SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     3,
		QueryVector:    []float64{1, 0, 0},
		Limit:          10,
		Filter: vectorstore.Filter{
			SourceKind: vectorstore.SourceKindSessionMessage,
			SourceIDs:  []string{"msg-b"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-b"}, matchIDs(result.Matches))

	result, err = store.Search(context.Background(), vectorstore.SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     3,
		QueryVector:    []float64{1, 0, 0},
		Limit:          10,
		Filter: vectorstore.Filter{
			SourceKind: vectorstore.SourceKindSessionMessage,
			SessionID:  "ses-b",
			Role:       "assistant",
			ToolName:   "process",
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-b"}, matchIDs(result.Matches))

	result, err = store.Search(context.Background(), vectorstore.SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     3,
		QueryVector:    []float64{1, 0, 0},
		Limit:          10,
		Filter: vectorstore.Filter{
			SourceKind:      vectorstore.SourceKindSessionMessage,
			IgnoreSessionID: "ses-a",
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-b"}, matchIDs(result.Matches))

	metadata, err := store.Metadata(context.Background())
	require.NoError(t, err)
	require.Equal(t, vectorstore.StoreMetadata{Models: []vectorstore.ModelMetadata{{
		Model:      "text-embedding-test",
		Dimensions: 3,
		Count:      3,
	}}}, metadata)

	require.NoError(t, store.Delete(context.Background(), vectorstore.DeleteRequest{
		SourceKind: vectorstore.SourceKindSessionMessage,
		SourceIDs:  []string{"msg-b"},
	}))
	result, err = store.Search(context.Background(), vectorstore.SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     3,
		QueryVector:    []float64{1, 0, 0},
		Limit:          10,
		Filter: vectorstore.Filter{
			SourceKind: vectorstore.SourceKindSessionMessage,
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-a"}, matchIDs(result.Matches))
}

func assertVectorStoreReplaceAndCopyIsolation(t *testing.T, store vectorstore.Store) {
	t.Helper()

	record := testRecord("vec-a", vectorstore.SourceKindSessionMessage, "msg-a", []float64{1, 0}, time.Time{})
	require.NoError(t, store.Upsert(context.Background(), []vectorstore.Record{record}))
	record.Vector[0] = 0

	updated := testRecord("vec-a", vectorstore.SourceKindSessionMessage, "msg-a", []float64{0, 1}, time.Time{})
	require.NoError(t, store.Upsert(context.Background(), []vectorstore.Record{updated}))
	updated.Vector[1] = 0

	result, err := store.Search(context.Background(), vectorstore.SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     2,
		QueryVector:    []float64{0, 1},
		Limit:          1,
		Filter: vectorstore.Filter{
			SourceKind: vectorstore.SourceKindSessionMessage,
		},
	})
	require.NoError(t, err)
	require.Len(t, result.Matches, 1)
	require.Equal(t, []float64{0, 1}, result.Matches[0].Record.Vector)

	result.Matches[0].Record.Vector[1] = 0
	result, err = store.Search(context.Background(), vectorstore.SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     2,
		QueryVector:    []float64{0, 1},
		Limit:          1,
		Filter: vectorstore.Filter{
			SourceKind: vectorstore.SourceKindSessionMessage,
		},
	})
	require.NoError(t, err)
	require.Equal(t, []float64{0, 1}, result.Matches[0].Record.Vector)
}

func assertVectorStoreFiltersAndMetadata(t *testing.T, store vectorstore.Store) {
	t.Helper()

	records := []vectorstore.Record{
		testRecord("vec-b", vectorstore.SourceKindSessionMessage, "msg-b", []float64{1, 0}, time.Time{}),
		testRecord("vec-a", vectorstore.SourceKindSessionMessage, "msg-a", []float64{1, 0}, time.Time{}),
		testRecordWithModel("vec-other-model", vectorstore.SourceKindSessionMessage, "msg-c", "other-model", []float64{1, 0}),
		testRecord("vec-other-dim", vectorstore.SourceKindSessionMessage, "msg-d", []float64{1, 0, 0}, time.Time{}),
		testRecord("vec-memory", vectorstore.SourceKindMemoryItem, "mem-a", []float64{1, 0}, time.Time{}),
	}
	records[0].Tags = []string{"scope:project", "kind:message"}
	records[1].Tags = []string{"scope:other", "kind:message"}
	require.NoError(t, store.Upsert(context.Background(), records))

	result, err := store.Search(context.Background(), vectorstore.SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     2,
		QueryVector:    []float64{1, 0},
		Limit:          10,
		Filter: vectorstore.Filter{
			SourceKind: vectorstore.SourceKindSessionMessage,
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-a", "vec-b"}, matchIDs(result.Matches))

	result, err = store.Search(context.Background(), vectorstore.SearchRequest{
		EmbeddingModel: "other-model",
		Dimensions:     2,
		QueryVector:    []float64{1, 0},
		Limit:          10,
		Filter: vectorstore.Filter{
			SourceKind: vectorstore.SourceKindSessionMessage,
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-other-model"}, matchIDs(result.Matches))

	result, err = store.Search(context.Background(), vectorstore.SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     2,
		QueryVector:    []float64{1, 0},
		Limit:          10,
		Filter: vectorstore.Filter{
			SourceKind: vectorstore.SourceKindMemoryItem,
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-memory"}, matchIDs(result.Matches))

	result, err = store.Search(context.Background(), vectorstore.SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     2,
		QueryVector:    []float64{1, 0},
		Limit:          10,
		Filter: vectorstore.Filter{
			SourceKind: vectorstore.SourceKindSessionMessage,
			Tags:       []string{"kind:message", "scope:project"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-b"}, matchIDs(result.Matches))
	require.Equal(t, []string{"kind:message", "scope:project"}, result.Matches[0].Record.Tags)

	result, err = store.Search(context.Background(), vectorstore.SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     2,
		QueryVector:    []float64{1, 0},
		Limit:          10,
		Filter: vectorstore.Filter{
			SourceKind: vectorstore.SourceKindSessionMessage,
			Tags:       []string{"kind:message"},
			TagGroups:  [][]string{{"scope:missing", "scope:other"}},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-a"}, matchIDs(result.Matches))

	result, err = store.Search(context.Background(), vectorstore.SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     2,
		QueryVector:    []float64{1, 0},
		Limit:          10,
		Filter: vectorstore.Filter{
			SourceKind: vectorstore.SourceKindSessionMessage,
			TagGroups: [][]string{
				{"kind:missing", "kind:message"},
				{"scope:missing", "scope:project"},
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-b"}, matchIDs(result.Matches))

	result, err = store.Search(context.Background(), vectorstore.SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     2,
		QueryVector:    []float64{1, 0},
		Limit:          10,
		Filter: vectorstore.Filter{
			SourceKind: vectorstore.SourceKindSessionMessage,
			TagGroups: [][]string{
				{"kind:message"},
				{"scope:missing"},
			},
		},
	})
	require.NoError(t, err)
	require.Empty(t, result.Matches)

	metadata, err := store.Metadata(context.Background())
	require.NoError(t, err)
	require.Equal(t, vectorstore.StoreMetadata{Models: []vectorstore.ModelMetadata{
		{Model: "other-model", Dimensions: 2, Count: 1},
		{Model: "text-embedding-test", Dimensions: 2, Count: 3},
		{Model: "text-embedding-test", Dimensions: 3, Count: 1},
	}}, metadata)
}

func assertVectorStoreValidationErrors(t *testing.T, store vectorstore.Store) {
	t.Helper()

	err := store.Upsert(context.Background(), []vectorstore.Record{{}})
	require.EqualError(t, err, "vector id is required")

	err = store.Delete(context.Background(), vectorstore.DeleteRequest{})
	require.EqualError(t, err, "source kind is required")

	_, err = store.Search(context.Background(), vectorstore.SearchRequest{})
	require.EqualError(t, err, "vector search embedding model is required")

	_, err = store.Search(context.Background(), vectorstore.SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     1,
		QueryVector:    []float64{1},
		Limit:          1,
	})
	require.EqualError(t, err, "vector search filter source kind is required")

	_, err = store.Search(context.Background(), vectorstore.SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     1,
		QueryVector:    []float64{1},
		Limit:          1,
		Filter: vectorstore.Filter{
			SourceKind: vectorstore.SourceKindSessionMessage,
			SourceIDs:  []string{""},
		},
	})
	require.EqualError(t, err, "vector search filter source id is required")
}

func assertVectorStoreSearchMissingIndex(t *testing.T, store vectorstore.Store) {
	t.Helper()

	result, err := store.Search(context.Background(), vectorstore.SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     3,
		QueryVector:    []float64{1, 0, 0},
		Limit:          10,
		Filter: vectorstore.Filter{
			SourceKind: vectorstore.SourceKindSessionMessage,
		},
	})
	require.NoError(t, err)
	require.Empty(t, result.Matches)
}

func testRecord(
	id string,
	sourceKind vectorstore.SourceKind,
	sourceID string,
	vector []float64,
	at time.Time,
) vectorstore.Record {
	return vectorstore.Record{
		CreatedAt:      at,
		UpdatedAt:      at,
		ID:             id,
		SourceKind:     sourceKind,
		SourceID:       sourceID,
		SessionID:      "ses-test",
		Role:           "user",
		EmbeddingModel: "text-embedding-test",
		Dimensions:     len(vector),
		ContentHash:    vectorstore.ContentHash(id),
		Vector:         append([]float64(nil), vector...),
	}
}

func testRecordWithModel(
	id string,
	sourceKind vectorstore.SourceKind,
	sourceID string,
	model string,
	vector []float64,
) vectorstore.Record {
	record := testRecord(id, sourceKind, sourceID, vector, time.Time{})
	record.EmbeddingModel = model

	return record
}

func matchIDs(matches []vectorstore.SearchMatch) []string {
	ids := make([]string, 0, len(matches))
	for _, match := range matches {
		ids = append(ids, match.Record.ID)
	}

	return ids
}
