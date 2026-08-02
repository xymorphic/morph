package sqlite

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sqliteDriver "gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/xymorphic/morph/internal/state/search/vectorstore"
	storagesqlite "github.com/xymorphic/morph/internal/state/storesqlite"
)

func TestStore_NewStoreValidationAndSchema(t *testing.T) {
	_, err := NewStore("")
	require.EqualError(t, err, "vector sqlite path is required")

	blockerPath := filepath.Join(t.TempDir(), "blocker")
	require.NoError(t, os.WriteFile(blockerPath, []byte("x"), 0o600))

	_, err = NewStore(filepath.Join(blockerPath, "vectors.db"))
	require.ErrorContains(t, err, "failed to create vector db directory")

	_, err = NewStore(t.TempDir())
	require.ErrorContains(t, err, "failed to open vector db")

	store, err := NewStore(filepath.Join(t.TempDir(), "vectors.db"))
	require.NoError(t, err)
	require.True(t, sqliteTableExists(t, store.db, recordsTable))
	require.True(t, sqliteTableExists(t, store.db, recordTagsTable))

	var version string
	require.NoError(t, store.db.Raw(`SELECT vec_version()`).Scan(&version).Error)
	require.NotEmpty(t, version)

	_, err = NewStoreFromDB(nil)
	require.EqualError(t, err, "vector db is required")
}

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
	})
	require.EqualError(t, err, "vector store is required")
}

func TestWithSQLiteBusyRetry_RetriesBusyErrors(t *testing.T) {
	attempts := 0
	err := withSQLiteBusyRetry(context.Background(), func() error {
		attempts++
		if attempts < 3 {
			return errors.New("failed to insert vector record: database is locked")
		}

		return nil
	})

	require.NoError(t, err)
	require.Equal(t, 3, attempts)
}

func TestWithSQLiteBusyRetry_DoesNotRetryNonBusyErrors(t *testing.T) {
	attempts := 0
	err := withSQLiteBusyRetry(context.Background(), func() error {
		attempts++
		return errors.New("failed to insert vector record: disk full")
	})

	require.EqualError(t, err, "failed to insert vector record: disk full")
	require.Equal(t, 1, attempts)
}

func TestStore_SharesSessionDatabase(t *testing.T) {
	db, err := gorm.Open(sqliteDriver.Open(filepath.Join(t.TempDir(), "morph.db")))
	require.NoError(t, err)

	_, err = storagesqlite.NewStoreFromDB(db)
	if err != nil && strings.Contains(err.Error(), "no such module: fts5") {
		t.Skip("sqlite fts5 module is unavailable")
	}
	require.NoError(t, err)
	store, err := NewStoreFromDB(db)
	require.NoError(t, err)

	require.True(t, sqliteTableExists(t, db, "sessions"))
	require.True(t, sqliteTableExists(t, store.db, recordsTable))

	require.NoError(t, store.Upsert(context.Background(), []Record{
		testRecord("vec-a", SourceKindSessionMessage, "msg-a", []float64{1, 0, 0}, time.Time{}),
	}))
	result, err := store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     3,
		QueryVector:    []float64{1, 0, 0},
		Limit:          1,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-a"}, matchIDs(result.Matches))
}

func TestStore_UpsertSearchDeleteAndMetadata(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "vectors.db"))
	require.NoError(t, err)

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
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

	require.True(t, sqliteTableExists(t, store.db, getIndexTableName(3)))

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
			SourceIDs:  []string{"msg-a", "msg-b"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-a", "vec-b"}, matchIDs(result.Matches))

	result, err = store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     3,
		QueryVector:    []float64{1, 0, 0},
		Limit:          10,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
			SessionID:  "ses-b",
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-b"}, matchIDs(result.Matches))
	require.Equal(t, "ses-b", result.Matches[0].Record.SessionID)

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

	result, err = store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     3,
		QueryVector:    []float64{1, 0, 0},
		Limit:          10,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
			Role:       "assistant",
			ToolName:   "process",
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-b"}, matchIDs(result.Matches))
	require.Equal(t, "process", result.Matches[0].Record.ToolName)

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

	require.NoError(t, store.Upsert(context.Background(), []Record{
		testRecord("vec-b", SourceKindSessionMessage, "msg-b", []float64{0.8, 0.2, 0}, now.Add(time.Second)),
	}))
	require.NoError(t, store.Delete(context.Background(), DeleteRequest{
		SourceKind: SourceKindSessionMessage,
		SourceIDs:  []string{"msg-a", "msg-b"},
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
	require.Empty(t, result.Matches)
}

func TestStore_ListFiltersAndTags(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "vectors.db"))
	require.NoError(t, err)

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	records := []Record{
		testRecord("vec-a", SourceKindSessionMessage, "msg-a", []float64{1, 0, 0}, now),
		testRecord("vec-b", SourceKindSessionMessage, "msg-b", []float64{0, 1, 0}, now.Add(time.Second)),
		testRecord("vec-c", SourceKindMemoryItem, "mem-c", []float64{0, 0, 1}, now.Add(2*time.Second)),
	}
	records[0].SessionID = "ses-a"
	records[0].Role = "assistant"
	records[0].Tags = []string{"phase:one", "kind:alpha", "group:red"}
	records[1].SessionID = "ses-b"
	records[1].Role = "assistant"
	records[1].ToolName = "process"
	records[1].Tags = []string{"phase:one", "kind:beta", "group:blue"}
	records[2].SessionID = "ses-a"
	records[2].Tags = []string{"phase:two", "kind:gamma"}
	require.NoError(t, store.Upsert(context.Background(), records))

	result, err := store.List(context.Background(), ListRequest{
		EmbeddingModel: "text-embedding-test",
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
			SourceIDs:  []string{"msg-a"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-a"}, recordListIDs(result.Records))
	require.Equal(t, []string{"group:red", "kind:alpha", "phase:one"}, result.Records[0].Tags)

	result, err = store.List(context.Background(), ListRequest{
		EmbeddingModel: "text-embedding-test",
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
			SourceIDs:  []string{"msg-a", "msg-b"},
			Tags:       []string{"phase:one"},
			TagGroups:  [][]string{{"kind:beta", "kind:missing"}, {"group:blue"}},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-b"}, recordListIDs(result.Records))

	result, err = store.List(context.Background(), ListRequest{
		EmbeddingModel: "text-embedding-test",
		Filter: Filter{
			SourceKind:      SourceKindSessionMessage,
			IgnoreSessionID: "ses-a",
			Role:            "assistant",
			ToolName:        "process",
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-b"}, recordListIDs(result.Records))
	require.Equal(t, "ses-b", result.Records[0].SessionID)
	require.Equal(t, "process", result.Records[0].ToolName)

	result, err = store.List(context.Background(), ListRequest{
		EmbeddingModel: "text-embedding-test",
		Filter: Filter{
			SessionID: "ses-a",
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-c", "vec-a"}, recordListIDs(result.Records))
}

func TestStore_SearchMissingDimensionDoesNotCreateIndexTable(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "vectors.db"))
	require.NoError(t, err)

	result, err := store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     3,
		QueryVector:    []float64{1, 0, 0},
		Limit:          10,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
		},
	})
	require.NoError(t, err)
	require.Empty(t, result.Matches)
	require.False(t, sqliteTableExists(t, store.db, getIndexTableName(3)))
}

func TestStore_SearchReturnsSQLError(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "vectors.db"))
	require.NoError(t, err)

	require.NoError(t, store.Upsert(context.Background(), []Record{
		testRecord("vec-a", SourceKindSessionMessage, "msg-a", []float64{1, 0, 0}, time.Time{}),
	}))
	require.NoError(t, store.db.Exec(`DROP TABLE `+recordsTable).Error)

	_, err = store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     3,
		QueryVector:    []float64{1, 0, 0},
		Limit:          1,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
		},
	})
	require.ErrorContains(t, err, "failed to search vectors")
}

func TestStore_SearchReturnsTagLoadError(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "vectors.db"))
	require.NoError(t, err)

	require.NoError(t, store.Upsert(context.Background(), []Record{
		testRecord("vec-a", SourceKindSessionMessage, "msg-a", []float64{1, 0, 0}, time.Time{}),
	}))
	require.NoError(t, store.db.Exec(`DROP TABLE `+recordTagsTable).Error)

	_, err = store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     3,
		QueryVector:    []float64{1, 0, 0},
		Limit:          1,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
		},
	})
	require.ErrorContains(t, err, "failed to load vector record tags")
}

func TestStore_UpsertReplacesExistingVectorAndDimension(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "vectors.db"))
	require.NoError(t, err)

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Upsert(context.Background(), []Record{
		testRecord("vec-a", SourceKindSessionMessage, "msg-a", []float64{1, 0, 0}, now),
		testRecord("vec-b", SourceKindSessionMessage, "msg-b", []float64{0.8, 0.2, 0}, now),
	}))
	replacement := testRecord("vec-a", SourceKindSessionMessage, "msg-a", []float64{0, 1}, now.Add(time.Minute))
	replacement.ContentHash = vectorstore.ContentHash("replacement")
	require.NoError(t, store.Upsert(context.Background(), []Record{replacement}))

	result, err := store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     3,
		QueryVector:    []float64{1, 0, 0},
		Limit:          10,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-b"}, matchIDs(result.Matches))

	result, err = store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     2,
		QueryVector:    []float64{0, 1},
		Limit:          10,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-a"}, matchIDs(result.Matches))
	require.Equal(t, vectorstore.ContentHash("replacement"), result.Matches[0].Record.ContentHash)
	require.True(t, vectorstore.IsRecordStale(result.Matches[0].Record, "current text"))
}

func TestStore_ValidationErrors(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "vectors.db"))
	require.NoError(t, err)

	require.NoError(t, store.Upsert(context.Background(), nil))

	err = store.Upsert(context.Background(), []Record{{}})
	require.EqualError(t, err, "vector id is required")

	err = store.Delete(context.Background(), DeleteRequest{})
	require.EqualError(t, err, "source kind is required")

	_, err = store.Search(context.Background(), SearchRequest{})
	require.EqualError(t, err, "vector search embedding model is required")

	_, err = store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     3,
		QueryVector:    []float64{1, 0, 0},
		Limit:          1,
	})
	require.EqualError(t, err, "vector search filter source kind is required")

	_, err = store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     3,
		QueryVector:    []float64{1, 0, 0},
		Limit:          1,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
			SourceIDs:  []string{""},
		},
	})
	require.EqualError(t, err, "vector search filter source id is required")

	_, err = store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     3,
		QueryVector:    []float64{1, 0, 0},
		Limit:          1,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
			SourceIDs:  []string{" msg-a "},
		},
	})
	require.EqualError(t, err, "vector search filter source id must be trimmed")

	overflow := testRecord("vec-overflow", SourceKindSessionMessage, "msg-overflow", []float64{1, 0, 0}, time.Time{})
	overflow.Vector[0] = 2 * float64(math.MaxFloat32)
	err = store.Upsert(context.Background(), []Record{overflow})
	require.EqualError(t, err, "vector value exceeds float32 range")
}

func TestStore_MetadataError(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "vectors.db"))
	require.NoError(t, err)

	require.NoError(t, store.db.Exec(`DROP TABLE `+recordsTable).Error)

	_, err = store.Metadata(context.Background())
	require.ErrorContains(t, err, "failed to load vector metadata")
}

func TestStore_ListErrors(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "vectors.db"))
	require.NoError(t, err)

	_, err = store.List(context.Background(), ListRequest{})
	require.EqualError(t, err, "vector list embedding model is required")

	require.NoError(t, store.db.Exec(`DROP TABLE `+recordsTable).Error)
	_, err = store.List(context.Background(), ListRequest{
		EmbeddingModel: "text-embedding-test",
	})
	require.ErrorContains(t, err, "failed to list vectors")

	store, err = NewStore(filepath.Join(t.TempDir(), "vectors.db"))
	require.NoError(t, err)
	require.NoError(t, store.Upsert(context.Background(), []Record{
		testRecord("vec-a", SourceKindSessionMessage, "msg-a", []float64{1}, time.Time{}),
	}))
	require.NoError(t, store.db.Exec(`DROP TABLE `+recordTagsTable).Error)
	_, err = store.List(context.Background(), ListRequest{
		EmbeddingModel: "text-embedding-test",
	})
	require.ErrorContains(t, err, "failed to load vector record tags")

	store, err = NewStore(filepath.Join(t.TempDir(), "vectors.db"))
	require.NoError(t, err)
	require.NoError(t, store.Upsert(context.Background(), []Record{
		testRecord("vec-b", SourceKindSessionMessage, "msg-b", []float64{1}, time.Time{}),
	}))
	require.NoError(t, store.db.Exec(`UPDATE `+recordsTable+` SET vector = ? WHERE id = ?`, []byte{1, 2}, "vec-b").Error)
	_, err = store.List(context.Background(), ListRequest{
		EmbeddingModel: "text-embedding-test",
	})
	require.EqualError(t, err, "vector blob length must match dimensions")
}

func TestStore_BrokenDatabaseErrors(t *testing.T) {
	db, err := gorm.Open(sqliteDriver.Open(filepath.Join(t.TempDir(), "vectors.db")))
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	_, err = NewStoreFromDB(db)
	require.ErrorContains(t, err, "sqlite vector extension is required")

	store, err := NewStore(filepath.Join(t.TempDir(), "vectors.db"))
	require.NoError(t, err)
	require.NoError(t, store.db.Exec(`DROP TABLE `+recordsTable).Error)

	err = store.Delete(context.Background(), DeleteRequest{
		SourceKind: SourceKindSessionMessage,
		SourceIDs:  []string{"msg-a"},
	})
	require.ErrorContains(t, err, "failed to load vector record refs")

	err = store.Upsert(context.Background(), []Record{
		testRecord("vec-a", SourceKindSessionMessage, "msg-a", []float64{1, 0, 0}, time.Time{}),
	})
	require.ErrorContains(t, err, "failed to load vector record ref")
}

func TestStore_TagPersistenceErrors(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "vectors.db"))
	require.NoError(t, err)
	require.NoError(t, store.Upsert(context.Background(), []Record{
		testRecord("vec-a", SourceKindSessionMessage, "msg-a", []float64{1}, time.Time{}),
	}))

	require.NoError(t, store.db.Exec(`DROP TABLE `+recordTagsTable).Error)
	err = store.Delete(context.Background(), DeleteRequest{
		SourceKind: SourceKindSessionMessage,
		SourceIDs:  []string{"msg-a"},
	})
	require.ErrorContains(t, err, "failed to delete vector record tags")
	require.NoError(t, ensureSQLiteStorage(store.db))
	result, err := store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     1,
		QueryVector:    []float64{1},
		Limit:          1,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
			SourceIDs:  []string{"msg-a"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"vec-a"}, matchIDs(result.Matches))

	store, err = NewStore(filepath.Join(t.TempDir(), "vectors.db"))
	require.NoError(t, err)
	require.NoError(t, store.db.Exec(`DROP TABLE `+recordTagsTable).Error)
	err = store.Upsert(context.Background(), []Record{
		testRecord("vec-b", SourceKindSessionMessage, "msg-b", []float64{1}, time.Time{}),
	})
	require.ErrorContains(t, err, "failed to delete vector record tags")
	require.NoError(t, ensureSQLiteStorage(store.db))
	result, err = store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     1,
		QueryVector:    []float64{1},
		Limit:          1,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
			SourceIDs:  []string{"msg-b"},
		},
	})
	require.NoError(t, err)
	require.Empty(t, result.Matches)

	db, err := gorm.Open(sqliteDriver.Open(filepath.Join(t.TempDir(), "vectors.db")))
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE `+recordTagsTable+` (record_id TEXT NOT NULL)`).Error)
	err = replaceRecordTags(db, "vec-c", []string{"phase:one"})
	require.ErrorContains(t, err, "failed to insert vector record tag")
}

func TestStore_ReadOnlyDatabaseErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vectors.db")
	store, err := NewStore(path)
	require.NoError(t, err)
	require.NoError(t, store.Upsert(context.Background(), []Record{
		testRecord("vec-a", SourceKindSessionMessage, "msg-a", []float64{1, 0, 0}, time.Time{}),
	}))

	readOnlyDB, err := gorm.Open(sqliteDriver.Open("file:" + path + "?mode=ro"))
	require.NoError(t, err)
	readOnlyStore := &Store{db: readOnlyDB}

	err = readOnlyStore.Delete(context.Background(), DeleteRequest{
		SourceKind: SourceKindSessionMessage,
		SourceIDs:  []string{"missing"},
	})
	require.ErrorContains(t, err, "failed to delete vector records")

	err = readOnlyStore.Delete(context.Background(), DeleteRequest{
		SourceKind: SourceKindSessionMessage,
		SourceIDs:  []string{"msg-a"},
	})
	require.ErrorContains(t, err, "failed to delete vector index row")

	err = readOnlyStore.Upsert(context.Background(), []Record{
		testRecord("vec-b", SourceKindSessionMessage, "msg-b", []float64{1, 0}, time.Time{}),
	})
	require.ErrorContains(t, err, "failed to create vector index table")
}

func TestStore_SearchRecordDecodeError(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "vectors.db"))
	require.NoError(t, err)
	require.NoError(t, store.Upsert(context.Background(), []Record{
		testRecord("vec-a", SourceKindSessionMessage, "msg-a", []float64{1, 0, 0}, time.Time{}),
	}))
	require.NoError(t, store.db.Exec(`UPDATE `+recordsTable+` SET vector = ? WHERE id = ?`, []byte{1, 2}, "vec-a").Error)

	_, err = store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     3,
		QueryVector:    []float64{1, 0, 0},
		Limit:          1,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
		},
	})
	require.EqualError(t, err, "vector blob length must match dimensions")
}

func TestStore_SearchValidationHelperErrors(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "vectors.db"))
	require.NoError(t, err)

	_, err = store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     3,
		QueryVector:    []float64{2 * float64(math.MaxFloat32), 0, 0},
		Limit:          1,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
		},
	})
	require.EqualError(t, err, "vector value exceeds float32 range")

	sqlDB, err := store.db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	_, err = store.Search(context.Background(), SearchRequest{
		EmbeddingModel: "text-embedding-test",
		Dimensions:     3,
		QueryVector:    []float64{1, 0, 0},
		Limit:          1,
		Filter: Filter{
			SourceKind: SourceKindSessionMessage,
		},
	})
	require.ErrorContains(t, err, "failed to check vector index table")
}

func TestStore_MalformedRecordTableErrors(t *testing.T) {
	db, err := gorm.Open(sqliteDriver.Open(filepath.Join(t.TempDir(), "vectors.db")))
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE `+recordsTable+` (
	vector_rowid INTEGER PRIMARY KEY AUTOINCREMENT,
	id TEXT NOT NULL UNIQUE,
	dimensions INTEGER NOT NULL
)`).Error)

	err = upsertRecord(db, testRecord("vec-insert", SourceKindSessionMessage, "msg-insert", []float64{1}, time.Time{}))
	require.ErrorContains(t, err, "failed to insert vector record")

	require.NoError(t, db.Exec(`INSERT INTO `+recordsTable+` (id, dimensions) VALUES (?, ?)`, "vec-update", 0).Error)
	err = upsertRecord(db, testRecord("vec-update", SourceKindSessionMessage, "msg-update", []float64{1}, time.Time{}))
	require.ErrorContains(t, err, "failed to update vector record")
}

func TestStore_ExistingRecordIndexDeleteErrors(t *testing.T) {
	db, err := gorm.Open(sqliteDriver.Open(filepath.Join(t.TempDir(), "vectors.db")))
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE `+recordsTable+` (
	vector_rowid INTEGER PRIMARY KEY AUTOINCREMENT,
	id TEXT NOT NULL UNIQUE,
	source_kind TEXT NOT NULL,
	source_id TEXT NOT NULL,
	embedding_model TEXT NOT NULL,
	dimensions INTEGER NOT NULL,
	content_hash TEXT NOT NULL,
	vector BLOB NOT NULL,
	created_at DATETIME NOT NULL,
	updated_at DATETIME NOT NULL
)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO `+recordsTable+` (
		id,
		source_kind,
		source_id,
		embedding_model,
		dimensions,
		content_hash,
		vector,
		created_at,
		updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"vec-different-dimension",
		string(SourceKindSessionMessage),
		"msg-a",
		"text-embedding-test",
		99,
		vectorstore.ContentHash("vec-different-dimension"),
		[]byte{0, 0, 0, 0},
		time.Now().UTC(),
		time.Now().UTC(),
	).Error)
	err = upsertRecord(db, testRecord("vec-different-dimension", SourceKindSessionMessage, "msg-a", []float64{1}, time.Time{}))
	require.ErrorContains(t, err, "failed to delete vector index row")

	require.NoError(t, db.Exec(`INSERT INTO `+recordsTable+` (
		id,
		source_kind,
		source_id,
		embedding_model,
		dimensions,
		content_hash,
		vector,
		created_at,
		updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"vec-same-dimension",
		string(SourceKindSessionMessage),
		"msg-b",
		"text-embedding-test",
		1,
		vectorstore.ContentHash("vec-same-dimension"),
		[]byte{0, 0, 0, 0},
		time.Now().UTC(),
		time.Now().UTC(),
	).Error)
	err = upsertRecord(db, testRecord("vec-same-dimension", SourceKindSessionMessage, "msg-b", []float64{1}, time.Time{}))
	require.ErrorContains(t, err, "failed to delete vector index row")
}

func TestStore_EnsureStorageErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vectors.db")
	db, err := gorm.Open(sqliteDriver.Open(path))
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	readOnlyDB, err := gorm.Open(sqliteDriver.Open("file:" + path + "?mode=ro"))
	require.NoError(t, err)
	err = ensureSQLiteStorage(readOnlyDB)
	require.ErrorContains(t, err, "failed to create vector records table")

	malformedPath := filepath.Join(t.TempDir(), "malformed.db")
	malformedDB, err := gorm.Open(sqliteDriver.Open(malformedPath))
	require.NoError(t, err)
	require.NoError(t, malformedDB.Exec(`CREATE TABLE `+recordsTable+` (id TEXT)`).Error)
	malformedSQLDB, err := malformedDB.DB()
	require.NoError(t, err)
	require.NoError(t, malformedSQLDB.Close())

	readOnlyMalformedDB, err := gorm.Open(sqliteDriver.Open("file:" + malformedPath + "?mode=ro"))
	require.NoError(t, err)
	err = ensureSQLiteStorage(readOnlyMalformedDB)
	require.ErrorContains(t, err, "failed to create vector record tags table")

	indexConflictDB, err := gorm.Open(sqliteDriver.Open(filepath.Join(t.TempDir(), "index-conflict.db")))
	require.NoError(t, err)
	require.NoError(t, indexConflictDB.Exec(`CREATE TABLE idx_vectors_source (id TEXT)`).Error)
	err = ensureSQLiteStorage(indexConflictDB)
	require.ErrorContains(t, err, "failed to create vector records index")
}

func TestStore_InsertIndexError(t *testing.T) {
	db, err := gorm.Open(sqliteDriver.Open(filepath.Join(t.TempDir(), "vectors.db")))
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE `+recordsTable+` (
	vector_rowid INTEGER PRIMARY KEY AUTOINCREMENT,
	id TEXT NOT NULL UNIQUE,
	source_kind TEXT NOT NULL,
	source_id TEXT NOT NULL,
	session_id TEXT NOT NULL DEFAULT '',
	role TEXT NOT NULL DEFAULT '',
	tool_name TEXT NOT NULL DEFAULT '',
	embedding_model TEXT NOT NULL,
	dimensions INTEGER NOT NULL,
	content_hash TEXT NOT NULL,
	vector BLOB NOT NULL,
	created_at DATETIME NOT NULL,
	updated_at DATETIME NOT NULL
)`).Error)

	err = upsertRecord(db, testRecord("vec-a", SourceKindSessionMessage, "msg-a", []float64{1}, time.Time{}))
	require.ErrorContains(t, err, "failed to insert vector index row")
}

func TestStore_HelperErrors(t *testing.T) {
	err := ensureSQLiteStorage(nil)
	require.EqualError(t, err, "vector db is required")

	err = ensureIndexTable(nil, 0)
	require.EqualError(t, err, "vector dimensions must be greater than zero")

	err = deleteIndexRow(nil, 0, 0)
	require.NoError(t, err)

	err = deleteIndexRows(nil, 0, []int64{1})
	require.NoError(t, err)

	store, err := NewStore(filepath.Join(t.TempDir(), "vectors.db"))
	require.NoError(t, err)

	err = deleteIndexRow(store.db, 99, 1)
	require.ErrorContains(t, err, "failed to delete vector index row")

	_, err = indexTableExists(nil, 0)
	require.EqualError(t, err, "vector dimensions must be greater than zero")

	_, err = indexTableExists(store.db, 99)
	require.NoError(t, err)

	sqlDB, err := store.db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	_, err = indexTableExists(store.db, 99)
	require.ErrorContains(t, err, "failed to check vector index table")

	err = ensureIndexTable(store.db, 99)
	require.ErrorContains(t, err, "failed to check vector index table")

	_, err = serialize([]float64{math.NaN()})
	require.EqualError(t, err, "vector value must be finite")

	_, err = serialize([]float64{math.Inf(1)})
	require.EqualError(t, err, "vector value must be finite")

	_, err = deserialize([]byte{1, 2, 3}, 1)
	require.EqualError(t, err, "vector blob length must match dimensions")

	_, err = deserialize(nil, 0)
	require.EqualError(t, err, "vector dimensions must be greater than zero")

	_, err = searchRow{Vector: []byte{1}, Dimensions: 1}.record()
	require.EqualError(t, err, "vector blob length must match dimensions")

	sourceIDs := normalizeDeleteSourceIDs(DeleteRequest{
		SourceIDs: []string{" msg-a ", "msg-a", "", "msg-b"},
	})
	require.Equal(t, []string{"msg-a", "msg-b"}, sourceIDs)
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

func matchIDs(matches []SearchMatch) []string {
	ids := make([]string, 0, len(matches))
	for _, match := range matches {
		ids = append(ids, match.Record.ID)
	}

	return ids
}

func recordListIDs(records []Record) []string {
	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.ID)
	}

	return ids
}

func sqliteTableExists(t *testing.T, db *gorm.DB, name string) bool {
	t.Helper()

	var count int
	require.NoError(t, db.Raw(
		`SELECT COUNT(*) FROM sqlite_master WHERE type IN ('table', 'virtual table') AND name = ?`,
		name,
	).Scan(&count).Error)

	return count > 0
}
