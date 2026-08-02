package memory

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/state/search/vectorstore"
)

func TestStore_NilStoreErrors(t *testing.T) {
	var store *Store

	err := store.Upsert(context.Background(), []Record{
		testRecord("vec-a", SourceKindSessionMessage, "msg-a", []float64{1}, time.Time{}),
	})
	require.EqualError(t, err, "vector store is required")

	err = store.Delete(context.Background(), DeleteRequest{
		SourceKind: SourceKindSessionMessage,
		SourceIDs:  []string{"msg-a"},
	})
	require.EqualError(t, err, "vector store is required")

	_, err = store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     1,
		QueryVector:    []float64{1},
		Limit:          1,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
		},
	})
	require.EqualError(t, err, "vector store is required")

	_, err = store.Metadata(context.Background())
	require.EqualError(t, err, "vector store is required")

	_, err = store.List(context.Background(), ListRequest{
		EmbeddingModel: "text-embedding-test",
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
		},
	})
	require.EqualError(t, err, "vector store is required")
}

func TestStore_UpsertSearchDeleteAndMetadata(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	records := []Record{
		testRecord("vec-a", SourceKindSessionMessage, "msg-a", []float64{1, 0, 0}, now),
		testRecord("vec-b", SourceKindSessionMessage, "msg-b", []float64{0.8, 0.2, 0}, now.Add(time.Second)),
		testRecord("vec-c", SourceKindMemoryItem, "mem-c", []float64{0, 1, 0}, now.Add(2*time.Second)),
	}
	records[0].SessionID = "ses-a"
	records[0].Role = "assistant"
	records[0].Tags = []string{"phase:one", "kind:alpha", "group:red"}
	records[1].SessionID = "ses-b"
	records[1].Role = "assistant"
	records[1].ToolName = "process"
	records[1].Tags = []string{"phase:one", "kind:beta", "group:blue"}
	records[2].Tags = []string{"phase:two", "kind:gamma"}

	require.NoError(t, store.Upsert(context.Background(), records))

	result, err := store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     3,
		QueryVector:    []float64{1, 0, 0},
		Limit:          2,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-a", "vec-b"}, matchIDs(result.Matches))
	require.Greater(t, result.Matches[0].Score, result.Matches[1].Score)
	require.Equal(t, []float64{1, 0, 0}, result.Matches[0].Record.Vector)
	require.Equal(t, now, result.Matches[0].Record.CreatedAt)

	result, err = store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     3,
		QueryVector:    []float64{1, 0, 0},
		Limit:          10,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
			SourceIDs:  []string{"msg-b"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-b"}, matchIDs(result.Matches))

	result, err = store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     3,
		QueryVector:    []float64{1, 0, 0},
		Limit:          10,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
			Tags:       []string{"phase:one"},
			TagGroups:  [][]string{{"kind:beta", "kind:missing"}, {"group:blue"}},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-b"}, matchIDs(result.Matches))
	require.Equal(t, []string{"group:blue", "kind:beta", "phase:one"}, result.Matches[0].Record.Tags)

	result, err = store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     3,
		QueryVector:    []float64{1, 0, 0},
		Limit:          10,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
			SessionID:  "ses-b",
			Role:       "assistant",
			ToolName:   "process",
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-b"}, matchIDs(result.Matches))

	result, err = store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     3,
		QueryVector:    []float64{1, 0, 0},
		Limit:          10,
		Filter: Filter{
			SourceKind:      SourceKindSessionMessage,
			IgnoreSessionID: "ses-a",
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-b"}, matchIDs(result.Matches))

	metadata, err := store.Metadata(context.Background())
	require.NoError(t, err)
	require.Equal(t, StoreMetadata{Models: []ModelMetadata{{
		Model:      "text-embedding-test",
		Dimensions: 3,
		Count:      3,
	}}}, metadata)

	require.NoError(t, store.Delete(context.Background(), DeleteRequest{
		SourceKind: SourceKindSessionMessage,
		SourceIDs:  []string{"msg-b"},
	}))

	result, err = store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     3,
		QueryVector:    []float64{1, 0, 0},
		Limit:          10,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-a"}, matchIDs(result.Matches))

	require.NoError(t, store.Delete(context.Background(), DeleteRequest{
		SourceKind: SourceKindSessionMessage,
		SessionID:  "ses-a",
	}))

	result, err = store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     3,
		QueryVector:    []float64{1, 0, 0},
		Limit:          10,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
		},
	})
	require.NoError(t, err)
	require.Empty(t, matchIDs(result.Matches))
}

func TestStore_SearchTagFilters(t *testing.T) {
	store := NewStore()
	records := []Record{
		testRecord("vec-a", SourceKindSessionMessage, "msg-a", []float64{1, 0}, time.Time{}),
		testRecord("vec-b", SourceKindSessionMessage, "msg-b", []float64{0.9, 0.1}, time.Time{}),
		testRecord("vec-c", SourceKindSessionMessage, "msg-c", []float64{0, 1}, time.Time{}),
	}
	records[0].Tags = []string{"phase:one", "kind:alpha", "group:red"}
	records[1].Tags = []string{"phase:one", "kind:beta", "group:blue"}
	records[2].Tags = []string{"phase:two", "kind:beta", "group:red"}
	require.NoError(t, store.Upsert(context.Background(), records))

	result, err := store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     2,
		QueryVector:    []float64{1, 0},
		Limit:          10,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
			Tags:       []string{"phase:one"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-a", "vec-b"}, matchIDs(result.Matches))

	result, err = store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     2,
		QueryVector:    []float64{1, 0},
		Limit:          10,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
			Tags:       []string{"phase:missing"},
		},
	})
	require.NoError(t, err)
	require.Empty(t, result.Matches)

	result, err = store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     2,
		QueryVector:    []float64{1, 0},
		Limit:          10,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
			TagGroups:  [][]string{{"kind:beta", "kind:missing"}, {"group:red"}},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-c"}, matchIDs(result.Matches))

	result, err = store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     2,
		QueryVector:    []float64{1, 0},
		Limit:          10,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
			TagGroups:  [][]string{{"kind:missing"}},
		},
	})
	require.NoError(t, err)
	require.Empty(t, result.Matches)
}

func TestStore_ZeroValueStoreInitializesOnUpsert(t *testing.T) {
	store := &Store{}

	require.NoError(t, store.Upsert(context.Background(), nil))
	require.NoError(t, store.Upsert(context.Background(), []Record{
		testRecord("vec-a", SourceKindSessionMessage, "msg-a", []float64{1, 0}, time.Time{}),
	}))

	result, err := store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     2,
		QueryVector:    []float64{1, 0},
		Limit:          1,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-a"}, matchIDs(result.Matches))
}

func TestStore_UpsertReplacesExistingRecordAndCopiesVectors(t *testing.T) {
	store := NewStore()
	record := testRecord("vec-a", SourceKindSessionMessage, "msg-a", []float64{1, 0}, time.Time{})
	require.NoError(t, store.Upsert(context.Background(), []Record{record}))

	record.Vector[0] = 0
	updated := testRecord("vec-a", SourceKindSessionMessage, "msg-a", []float64{0, 1}, time.Time{})
	require.NoError(t, store.Upsert(context.Background(), []Record{updated}))
	updated.Vector[1] = 0

	result, err := store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     2,
		QueryVector:    []float64{0, 1},
		Limit:          1,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
		},
	})
	require.NoError(t, err)
	require.Len(t, result.Matches, 1)
	require.Equal(t, []float64{0, 1}, result.Matches[0].Record.Vector)

	result.Matches[0].Record.Vector[1] = 0
	result, err = store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     2,
		QueryVector:    []float64{0, 1},
		Limit:          1,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
		},
	})
	require.NoError(t, err)
	require.Equal(t, []float64{0, 1}, result.Matches[0].Record.Vector)
}

func TestStore_SearchFiltersModelDimensionsAndOrdersDeterministically(t *testing.T) {
	store := NewStore()
	require.NoError(t, store.Upsert(context.Background(), []Record{
		testRecord("vec-b", SourceKindSessionMessage, "msg-b", []float64{1, 0}, time.Time{}),
		testRecord("vec-a", SourceKindSessionMessage, "msg-a", []float64{1, 0}, time.Time{}),
		testRecordWithModel("vec-other-model", SourceKindSessionMessage, "msg-c", "other-model", []float64{1, 0}),
		testRecord("vec-other-dim", SourceKindSessionMessage, "msg-d", []float64{1, 0, 0}, time.Time{}),
		testRecordWithSourceKind("vec-other-kind", SourceKindMemoryItem, "mem-a", []float64{1, 0}),
	}))

	result, err := store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     2,
		QueryVector:    []float64{1, 0},
		Limit:          10,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-a", "vec-b"}, matchIDs(result.Matches))

	result, err = store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "other-model",
		Dimensions:     2,
		QueryVector:    []float64{1, 0},
		Limit:          10,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-other-model"}, matchIDs(result.Matches))

	result, err = store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     2,
		QueryVector:    []float64{1, 0},
		Limit:          10,
		Filter: Filter{
			SourceKind: SourceKindMemoryItem,
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-other-kind"}, matchIDs(result.Matches))
}

func TestStore_SearchReturnsEmptyForMissingIndexOrFilteredRecords(t *testing.T) {
	store := NewStore()

	result, err := store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     2,
		QueryVector:    []float64{1, 0},
		Limit:          1,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
		},
	})
	require.NoError(t, err)
	require.Empty(t, result.Matches)

	require.NoError(t, store.Upsert(context.Background(), []Record{
		testRecord("vec-a", SourceKindSessionMessage, "msg-a", []float64{1, 0}, time.Time{}),
	}))
	result, err = store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     2,
		QueryVector:    []float64{1, 0},
		Limit:          1,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
			Role:       "assistant",
		},
	})
	require.NoError(t, err)
	require.Empty(t, result.Matches)
}

func TestStore_MetadataOrdersMultipleModelsAndDimensions(t *testing.T) {
	store := NewStore()
	require.NoError(t, store.Upsert(context.Background(), []Record{
		testRecordWithModel("vec-b-3", SourceKindSessionMessage, "msg-b-3", "model-b", []float64{1, 0, 0}),
		testRecordWithModel("vec-a-3", SourceKindSessionMessage, "msg-a-3", "model-a", []float64{1, 0, 0}),
		testRecordWithModel("vec-a-2", SourceKindSessionMessage, "msg-a-2", "model-a", []float64{1, 0}),
	}))

	metadata, err := store.Metadata(context.Background())
	require.NoError(t, err)
	require.Equal(t, StoreMetadata{Models: []ModelMetadata{
		{Model: "model-a", Dimensions: 2, Count: 1},
		{Model: "model-a", Dimensions: 3, Count: 1},
		{Model: "model-b", Dimensions: 3, Count: 1},
	}}, metadata)
}

func TestStore_List(t *testing.T) {
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)

	t.Run("filters records by source metadata", func(t *testing.T) {
		store := NewStore()
		records := []Record{
			testRecord("vec-a", SourceKindSessionMessage, "msg-a", []float64{1, 0}, now),
			testRecord("vec-b", SourceKindSessionMessage, "msg-b", []float64{0, 1}, now),
			testRecordWithModel("vec-other-model", SourceKindSessionMessage, "msg-c", "other-model", []float64{1, 0}),
			testRecordWithSourceKind("vec-other-kind", SourceKindMemoryItem, "mem-a", []float64{1, 0}),
		}
		records[0].SessionID = "ses-a"
		records[0].Role = "user"
		records[0].Tags = []string{"phase:one", "kind:alpha", "group:red"}
		records[1].SessionID = "ses-b"
		records[1].Role = "assistant"
		records[1].ToolName = "search"
		records[1].Tags = []string{"phase:one", "kind:beta", "group:blue"}
		require.NoError(t, store.Upsert(context.Background(), records))

		result, err := store.List(context.Background(), ListRequest{
			EmbeddingModel: "text-embedding-test",
			Filter: Filter{
				SourceKind: SourceKindSessionMessage,
				SourceIDs:  []string{"msg-b"},
				SessionID:  "ses-b",
				Role:       "assistant",
				ToolName:   "search",
			},
		})
		require.NoError(t, err)
		expected := records[1]
		expected.Tags = []string{"group:blue", "kind:beta", "phase:one"}
		require.Equal(t, []Record{expected}, result.Records)
	})

	t.Run("honors ignored session id", func(t *testing.T) {
		store := NewStore()
		records := []Record{
			testRecord("vec-a", SourceKindSessionMessage, "msg-a", []float64{1, 0}, now),
			testRecord("vec-b", SourceKindSessionMessage, "msg-b", []float64{0, 1}, now),
		}
		records[0].SessionID = "ses-a"
		records[1].SessionID = "ses-b"
		require.NoError(t, store.Upsert(context.Background(), records))

		result, err := store.List(context.Background(), ListRequest{
			EmbeddingModel: "text-embedding-test",
			Filter: Filter{
				SourceKind:      SourceKindSessionMessage,
				IgnoreSessionID: "ses-a",
			},
		})
		require.NoError(t, err)
		require.Equal(t, []Record{records[1]}, result.Records)
	})

	t.Run("excludes records on source metadata mismatch", func(t *testing.T) {
		store := NewStore()
		record := testRecord("vec-a", SourceKindSessionMessage, "msg-a", []float64{1, 0}, now)
		record.SessionID = "ses-a"
		record.Role = "assistant"
		record.ToolName = "search"
		require.NoError(t, store.Upsert(context.Background(), []Record{record}))

		tests := []struct {
			name   string
			filter Filter
		}{
			{
				name: "session",
				filter: Filter{
					SourceKind: SourceKindSessionMessage,
					SessionID:  "ses-b",
				},
			},
			{
				name: "role",
				filter: Filter{
					SourceKind: SourceKindSessionMessage,
					Role:       "user",
				},
			},
			{
				name: "tool",
				filter: Filter{
					SourceKind: SourceKindSessionMessage,
					ToolName:   "other-tool",
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				result, err := store.List(context.Background(), ListRequest{
					EmbeddingModel: "text-embedding-test",
					Filter:         tt.filter,
				})
				require.NoError(t, err)
				require.Empty(t, result.Records)
			})
		}
	})

	t.Run("filters records by tags and tag groups", func(t *testing.T) {
		store := NewStore()
		records := []Record{
			testRecord("vec-a", SourceKindSessionMessage, "msg-a", []float64{1, 0}, now),
			testRecord("vec-b", SourceKindSessionMessage, "msg-b", []float64{0, 1}, now),
			testRecord("vec-c", SourceKindSessionMessage, "msg-c", []float64{0.5, 0.5}, now),
		}
		records[0].Tags = []string{"phase:one", "kind:alpha", "group:red"}
		records[1].Tags = []string{"phase:one", "kind:beta", "group:blue"}
		records[2].Tags = []string{"phase:two", "kind:beta", "group:red"}
		require.NoError(t, store.Upsert(context.Background(), records))

		result, err := store.List(context.Background(), ListRequest{
			EmbeddingModel: "text-embedding-test",
			Filter: Filter{
				SourceKind: SourceKindSessionMessage,
				Tags:       []string{"phase:one"},
				TagGroups:  [][]string{{"kind:beta", "kind:missing"}, {"group:blue"}},
			},
		})
		require.NoError(t, err)
		require.Equal(t, []string{"vec-b"}, getRecordIDs(result.Records))
		require.Equal(t, []string{"group:blue", "kind:beta", "phase:one"}, result.Records[0].Tags)

		result, err = store.List(context.Background(), ListRequest{
			EmbeddingModel: "text-embedding-test",
			Filter: Filter{
				SourceKind: SourceKindSessionMessage,
				Tags:       []string{"phase:missing"},
			},
		})
		require.NoError(t, err)
		require.Empty(t, result.Records)

		result, err = store.List(context.Background(), ListRequest{
			EmbeddingModel: "text-embedding-test",
			Filter: Filter{
				SourceKind: SourceKindSessionMessage,
				TagGroups:  [][]string{{"kind:missing"}},
			},
		})
		require.NoError(t, err)
		require.Empty(t, result.Records)
	})

	t.Run("validates request", func(t *testing.T) {
		store := NewStore()
		_, err := store.List(context.Background(), ListRequest{})
		require.EqualError(t, err, "vector list embedding model is required")
	})
}

func TestStore_HelperBranches(t *testing.T) {
	record := testRecord("vec-a", SourceKindSessionMessage, "msg-a", []float64{1, 0}, time.Time{})
	filter := searchFilter{
		embeddingModel: "other-model",
		dimensions:     record.Dimensions,
		sourceKind:     record.SourceKind,
	}
	require.False(t, recordMatchesSearch(record, filter))

	filter.embeddingModel = record.EmbeddingModel
	filter.dimensions = 3
	require.False(t, recordMatchesSearch(record, filter))

	filter.dimensions = record.Dimensions
	filter.toolName = "missing-tool"
	require.False(t, recordMatchesSearch(record, filter))

	store := NewStore()
	store.removeFromIndex(record)
}

func TestStore_ValidationErrors(t *testing.T) {
	store := NewStore()

	err := store.Upsert(context.Background(), []Record{{}})
	require.EqualError(t, err, "vector id is required")

	err = store.Upsert(context.Background(), []Record{
		testRecord("vec-a", SourceKindSessionMessage, "msg-a", []float64{math.MaxFloat64}, time.Time{}),
	})
	require.EqualError(t, err, "vector value exceeds float32 range")

	err = store.Delete(context.Background(), DeleteRequest{})
	require.EqualError(t, err, "source kind is required")

	_, err = store.Search(context.Background(), SearchRequest{})
	require.EqualError(t, err, "vector search embedding model is required")

	_, err = store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     1,
		QueryVector:    []float64{1},
		Limit:          1,
	})
	require.EqualError(t, err, "vector search filter source kind is required")

	_, err = store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     1,
		QueryVector:    []float64{1},
		Limit:          1,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
			SourceIDs:  []string{""},
		},
	})
	require.EqualError(t, err, "vector search filter source id is required")

	_, err = store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     1,
		QueryVector:    []float64{1},
		Limit:          1,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
			SourceIDs:  []string{" msg-a "},
		},
	})
	require.EqualError(t, err, "vector search filter source id must be trimmed")

	_, err = store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     1,
		QueryVector:    []float64{math.MaxFloat64},
		Limit:          1,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
		},
	})
	require.EqualError(t, err, "vector value exceeds float32 range")
}

func TestStore_ZeroVectorScoresAsZero(t *testing.T) {
	store := NewStore()
	require.NoError(t, store.Upsert(context.Background(), []Record{
		testRecord("vec-a", SourceKindSessionMessage, "msg-a", []float64{0, 0}, time.Time{}),
	}))

	result, err := store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     2,
		QueryVector:    []float64{1, 0},
		Limit:          1,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
		},
	})
	require.NoError(t, err)
	require.Equal(t, 0.0, result.Matches[0].Score)
}

func testRecord(id string, sourceKind SourceKind, sourceID string, vector []float64, at time.Time) Record {
	return Record{
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

func testRecordWithModel(id string, sourceKind SourceKind, sourceID string, model string, vector []float64) Record {
	record := testRecord(id, sourceKind, sourceID, vector, time.Time{})
	record.EmbeddingModel = model
	return record
}

func testRecordWithSourceKind(id string, sourceKind SourceKind, sourceID string, vector []float64) Record {
	return testRecord(id, sourceKind, sourceID, vector, time.Time{})
}

func matchIDs(matches []SearchMatch) []string {
	ids := make([]string, 0, len(matches))
	for _, match := range matches {
		ids = append(ids, match.Record.ID)
	}

	return ids
}

func getRecordIDs(records []Record) []string {
	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.ID)
	}

	return ids
}

func TestVectorScoringHelpers(t *testing.T) {
	require.Equal(t, float32(0), cosineDistance([]float32{1, 0}, []float32{1, 0}))
	require.Equal(t, float32(1), cosineDistance([]float32{1, 0}, []float32{0, 1}))
	require.Equal(t, float32(1), cosineDistance([]float32{0, 0}, []float32{1, 0}))
	require.Equal(t, 0.5, scoreFromDistance(0.5))
	require.Equal(t, -1, compareMatches(SearchMatch{Record: Record{ID: "b"}, Score: 2}, SearchMatch{Record: Record{ID: "a"}, Score: 1}))
	require.Equal(t, 1, compareMatches(SearchMatch{Record: Record{ID: "a"}, Score: 1}, SearchMatch{Record: Record{ID: "b"}, Score: 2}))
	require.Equal(t, -1, compareMatches(SearchMatch{Record: Record{ID: "a"}, Score: 1}, SearchMatch{Record: Record{ID: "b"}, Score: 1}))
	require.Equal(t, -1, compareModelMetadata(ModelMetadata{Model: "a", Dimensions: 3}, ModelMetadata{Model: "b", Dimensions: 1}))
	require.Equal(t, -1, compareModelMetadata(ModelMetadata{Model: "a", Dimensions: 1}, ModelMetadata{Model: "a", Dimensions: 2}))
	require.Equal(t, 1, compareModelMetadata(ModelMetadata{Model: "a", Dimensions: 2}, ModelMetadata{Model: "a", Dimensions: 1}))
	require.Equal(t, 0, compareModelMetadata(ModelMetadata{Model: "a", Dimensions: 1}, ModelMetadata{Model: "a", Dimensions: 1}))
	require.Equal(t, -1, compareRecords(Record{ID: "a"}, Record{ID: "b"}))
}
