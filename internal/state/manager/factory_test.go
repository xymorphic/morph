package manager

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/constants"
	"github.com/wandxy/morph/internal/datadir"
	models "github.com/wandxy/morph/internal/model"
	modelprovider "github.com/wandxy/morph/internal/model/provider"
	"github.com/wandxy/morph/internal/profile"
	storage "github.com/wandxy/morph/internal/state/core"
	"github.com/wandxy/morph/internal/state/search"
	vectormemory "github.com/wandxy/morph/internal/state/search/vectorstore/memory"
	vectorsqlite "github.com/wandxy/morph/internal/state/search/vectorstore/sqlite"
	storagememory "github.com/wandxy/morph/internal/state/storememory"
	storagesqlite "github.com/wandxy/morph/internal/state/storesqlite"
	morphmsg "github.com/wandxy/morph/pkg/agent/message"
	agentsession "github.com/wandxy/morph/pkg/agent/session"
	"github.com/wandxy/morph/pkg/logutils"
)

func init() {
	logutils.SetOutput(io.Discard)
}

const e2eVectorEmbeddingModel = "text-embedding-e2e"

func TestOpenStore_ValidatesConfigAndBackend(t *testing.T) {
	store, err := OpenStore(nil)
	require.Nil(t, store)
	require.EqualError(t, err, "config is required")

	store, err = OpenStore(storeConfig("bogus"))
	require.Nil(t, store)
	require.EqualError(t, err, "storage backend must be one of: memory, sqlite")
}

func storeConfig(backend string) *config.Config {
	return &config.Config{Storage: config.StorageConfig{Backend: backend}}
}

func TestOpenStore_ReturnsMemoryStore(t *testing.T) {
	store, err := OpenStore(storeConfig("memory"))

	require.NoError(t, err)
	require.IsType(t, &storagememory.Store{}, store)
	_, ok := store.Session().(agentsession.InboxStore)
	require.True(t, ok)
}

func TestOpenStore_IgnoresIncompleteVectorConfigWhenDisabled(t *testing.T) {
	store, err := OpenStore(&config.Config{
		Storage: config.StorageConfig{Backend: "memory"},
		Search:  config.SearchConfig{Vector: config.SearchVectorConfig{RebuildBatchSize: -1}},
	})

	require.NoError(t, err)
	require.IsType(t, &storagememory.Store{}, store)
}

func TestOpenStore_ReturnsSQLiteStore(t *testing.T) {
	homeDir := t.TempDir()
	setProfileHome(t, homeDir)

	store, err := OpenStore(storeConfig("sqlite"))
	require.NoError(t, err)
	require.IsType(t, &storagesqlite.Store{}, store)
	require.FileExists(t, datadir.StateDBPath())
	require.Equal(t, filepath.Join(homeDir, "data", "state.db"), datadir.StateDBPath())
}

func TestOpenStore_DefaultsToSQLite(t *testing.T) {
	homeDir := t.TempDir()
	setProfileHome(t, homeDir)

	store, err := OpenStore(&config.Config{})
	require.NoError(t, err)
	require.IsType(t, &storagesqlite.Store{}, store)
	require.FileExists(t, datadir.StateDBPath())
	require.Equal(t, filepath.Join(homeDir, "data", "state.db"), datadir.StateDBPath())
}

func TestOpenStore_ReturnsSQLiteOpenError(t *testing.T) {
	homePath := filepath.Join(t.TempDir(), "morph-home")
	require.NoError(t, os.WriteFile(homePath, []byte("not-a-directory"), 0o600))
	setProfileHome(t, homePath)

	store, err := OpenStore(storeConfig("sqlite"))

	require.Nil(t, store)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to create sqlite db directory")
}

func TestOpenStore_ReturnsSQLiteStoreInitializationError(t *testing.T) {
	homeDir := t.TempDir()
	setProfileHome(t, homeDir)

	originalStore := newSQLiteStoreFromDB
	t.Cleanup(func() {
		newSQLiteStoreFromDB = originalStore
	})
	newSQLiteStoreFromDB = func(*gorm.DB) (*storagesqlite.Store, error) {
		return nil, errors.New("store init failed")
	}

	store, err := OpenStore(storeConfig("sqlite"))

	require.Nil(t, store)
	require.EqualError(t, err, "store init failed")
}

func TestOpenStore_ConfiguresSQLiteVectorStore(t *testing.T) {
	homeDir := t.TempDir()
	setProfileHome(t, homeDir)

	provider := &storeTestEmbeddingProvider{}
	vectorStore := &storeTestVectorStore{}
	withStoreVectorHooks(t, provider, vectorStore, nil)

	rerankEnabled := false
	store, err := OpenStore(&config.Config{
		Storage: config.StorageConfig{Backend: "sqlite"},
		Models:  config.ModelsConfig{Embedding: config.EmbeddingModelConfig{Name: "text-embedding-test", Provider: "openai"}},
		Search: config.SearchConfig{
			EnableRerank: &rerankEnabled,
			Vector:       config.SearchVectorConfig{Enabled: true, Required: true, RebuildBatchSize: 7},
		},
	})
	require.NoError(t, err)
	require.IsType(t, &storagesqlite.Store{}, store)

	sessionID, err := storage.NewSessionID()
	require.NoError(t, err)
	require.NoError(t, store.Session().Save(context.Background(), storage.Session{ID: sessionID}))
	require.NoError(t, store.Session().AppendMessages(context.Background(), sessionID, []morphmsg.Message{{
		Role:    morphmsg.RoleUser,
		Content: "semantic indexing text",
	}}))

	require.Len(t, provider.requests, 1)
	require.Equal(t, "text-embedding-test", provider.requests[0].Model)
	require.Len(t, vectorStore.upserts, 1)
	require.Len(t, vectorStore.upserts[0], 1)
	require.Equal(t, "text-embedding-test", vectorStore.upserts[0][0].EmbeddingModel)
	require.Equal(t, search.SourceKindSessionMessage, vectorStore.upserts[0][0].SourceKind)
}

func TestOpenStore_ReturnsRerankerConstructionError(t *testing.T) {
	homeDir := t.TempDir()
	setProfileHome(t, homeDir)

	provider := &storeTestEmbeddingProvider{}
	vectorStore := &storeTestVectorStore{}
	withStoreVectorHooks(t, provider, vectorStore, nil)

	store, err := OpenStoreWithRerankerClient(&config.Config{
		Storage: config.StorageConfig{Backend: "sqlite"},
		Models:  config.ModelsConfig{Embedding: config.EmbeddingModelConfig{Name: "text-embedding-test", Provider: "openai"}},
		Search:  config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true}},
		Reranker: config.RerankerConfig{
			Type: search.RerankerLLM,
		},
	}, nil)

	require.Nil(t, store)
	require.EqualError(t, err, "reranker model client is required")
}

func TestOpenStore_ReturnsSQLiteVectorConfigurationError(t *testing.T) {
	homeDir := t.TempDir()
	setProfileHome(t, homeDir)

	originalProvider := newStoreEmbeddingProvider
	originalSQLiteStore := newSQLiteVectorStore
	t.Cleanup(func() {
		newStoreEmbeddingProvider = originalProvider
		newSQLiteVectorStore = originalSQLiteStore
	})
	newStoreEmbeddingProvider = func(*config.Config) (search.Embedder, error) {
		return nil, nil
	}
	newSQLiteVectorStore = func(*gorm.DB) (search.VectorStore, error) {
		return &storeTestVectorStore{}, nil
	}

	store, err := OpenStore(&config.Config{
		Storage: config.StorageConfig{Backend: "sqlite"},
		Models:  config.ModelsConfig{Embedding: config.EmbeddingModelConfig{Name: "text-embedding-test", Provider: "openai"}},
		Search:  config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true}},
	})

	require.Nil(t, store)
	require.EqualError(t, err, "embedding provider is required")
}

func TestOpenStore_ConfiguresMemoryVectorStore(t *testing.T) {
	provider := &storeTestEmbeddingProvider{}
	vectorStore := &storeTestVectorStore{}
	withStoreVectorHooks(t, provider, nil, vectorStore)

	store, err := OpenStore(&config.Config{
		Storage: config.StorageConfig{Backend: "memory"},
		Models: config.ModelsConfig{
			Main:      config.MainModelConfig{Provider: "openai"},
			Embedding: config.EmbeddingModelConfig{Name: "text-embedding-test"},
		},
		Search: config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true}},
	})

	require.NoError(t, err)
	require.IsType(t, &storagememory.Store{}, store)

	sessionID, err := storage.NewSessionID()
	require.NoError(t, err)
	require.NoError(t, store.Session().Save(context.Background(), storage.Session{ID: sessionID}))
	require.NoError(t, store.Session().AppendMessages(context.Background(), sessionID, []morphmsg.Message{{
		Role:    morphmsg.RoleUser,
		Content: "semantic indexing text",
	}}))

	require.Len(t, provider.requests, 1)
	require.Equal(t, "text-embedding-test", provider.requests[0].Model)
	require.Len(t, vectorStore.upserts, 1)
	require.Len(t, vectorStore.upserts[0], 1)
	require.Equal(t, "text-embedding-test", vectorStore.upserts[0][0].EmbeddingModel)
	require.Equal(t, search.SourceKindSessionMessage, vectorStore.upserts[0][0].SourceKind)
}

func TestOpenStore_ReturnsMemoryVectorConfigError(t *testing.T) {
	store, err := OpenStore(&config.Config{
		Storage: config.StorageConfig{Backend: "memory"},
		Models:  config.ModelsConfig{Embedding: config.EmbeddingModelConfig{Provider: "openai"}},
		Search:  config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true}},
	})

	require.Nil(t, store)
	require.EqualError(t, err, "embedding model is required")
}

func TestOpenStore_ReturnsMemoryVectorConfigurationError(t *testing.T) {
	originalProvider := newStoreEmbeddingProvider
	t.Cleanup(func() {
		newStoreEmbeddingProvider = originalProvider
	})
	newStoreEmbeddingProvider = func(*config.Config) (search.Embedder, error) {
		return nil, nil
	}

	store, err := OpenStore(&config.Config{
		Storage: config.StorageConfig{Backend: "memory"},
		Models:  config.ModelsConfig{Embedding: config.EmbeddingModelConfig{Name: "text-embedding-test", Provider: "openai"}},
		Search:  config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true}},
	})

	require.Nil(t, store)
	require.EqualError(t, err, "embedding provider is required")
}

func TestOpenStore_ReturnsMemoryEmbeddingProviderError(t *testing.T) {
	originalProvider := newStoreEmbeddingProvider
	t.Cleanup(func() {
		newStoreEmbeddingProvider = originalProvider
	})
	newStoreEmbeddingProvider = func(*config.Config) (search.Embedder, error) {
		return nil, errors.New("embedding provider failed")
	}

	store, err := OpenStore(&config.Config{
		Storage: config.StorageConfig{Backend: "memory"},
		Models:  config.ModelsConfig{Embedding: config.EmbeddingModelConfig{Name: "text-embedding-test", Provider: "openai"}},
		Search:  config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true}},
	})

	require.Nil(t, store)
	require.EqualError(t, err, "embedding provider failed")
}

func TestOpenStore_ReturnsMemoryRerankerConstructionError(t *testing.T) {
	provider := &storeTestEmbeddingProvider{}
	vectorStore := &storeTestVectorStore{}
	withStoreVectorHooks(t, provider, nil, vectorStore)

	store, err := OpenStoreWithRerankerClient(&config.Config{
		Storage: config.StorageConfig{Backend: "memory"},
		Models:  config.ModelsConfig{Embedding: config.EmbeddingModelConfig{Name: "text-embedding-test", Provider: "openai"}},
		Search:  config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true}},
		Reranker: config.RerankerConfig{
			Type: search.RerankerLLM,
		},
	}, nil)

	require.Nil(t, store)
	require.EqualError(t, err, "reranker model client is required")
}

func TestOpenStore_ValidatesVectorConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Config
		err  string
	}{
		{
			name: "missing model",
			cfg: config.Config{
				Storage: config.StorageConfig{Backend: "sqlite"},
				Models:  config.ModelsConfig{Embedding: config.EmbeddingModelConfig{Provider: "openai"}},
				Search:  config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true}},
			},
			err: "embedding model is required",
		},
		{
			name: "negative batch size",
			cfg: config.Config{
				Storage: config.StorageConfig{Backend: "sqlite"},
				Models:  config.ModelsConfig{Embedding: config.EmbeddingModelConfig{Name: "text-embedding-test", Provider: "openai"}},
				Search:  config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true, RebuildBatchSize: -1}},
			},
			err: "vector rebuild batch size must be non-negative",
		},
		{
			name: "negative max input bytes",
			cfg: config.Config{
				Storage: config.StorageConfig{Backend: "sqlite"},
				Models:  config.ModelsConfig{Embedding: config.EmbeddingModelConfig{Name: "text-embedding-test", Provider: "openai"}},
				Search:  config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true, MaxInputBytes: -1}},
			},
			err: "vector max input bytes must be non-negative",
		},
		{
			name: "negative max document bytes",
			cfg: config.Config{
				Storage: config.StorageConfig{Backend: "sqlite"},
				Models:  config.ModelsConfig{Embedding: config.EmbeddingModelConfig{Name: "text-embedding-test", Provider: "openai"}},
				Search:  config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true, MaxDocumentBytes: -1}},
			},
			err: "vector max document bytes must be non-negative",
		},
		{
			name: "document limit below input limit",
			cfg: config.Config{
				Storage: config.StorageConfig{Backend: "sqlite"},
				Models:  config.ModelsConfig{Embedding: config.EmbeddingModelConfig{Name: "text-embedding-test", Provider: "openai"}},
				Search: config.SearchConfig{Vector: config.SearchVectorConfig{
					Enabled: true, MaxInputBytes: 2048, MaxDocumentBytes: 1024,
				}},
			},
			err: "vector max document bytes must be greater than or equal to max input bytes",
		},
		{
			name: "missing api key",
			cfg: config.Config{
				Storage: config.StorageConfig{Backend: "sqlite"},
				Models: config.ModelsConfig{
					Main:      config.MainModelConfig{Provider: "openai"},
					Embedding: config.EmbeddingModelConfig{Name: "text-embedding-test"},
				},
				Search: config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true}},
			},
			err: `embedding API key is required for provider "openai"; set a provider API key, provider env var, or role apiKey`,
		},
		{
			name: "unsupported provider",
			cfg: config.Config{
				Storage: config.StorageConfig{Backend: "sqlite"},
				Models:  config.ModelsConfig{Embedding: config.EmbeddingModelConfig{Name: "text-embedding-test", Provider: "unsupported"}},
				Search:  config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true}},
			},
			err: "embedding provider must be one of: anthropic, github-copilot, ollama, openai, openai-codex, openrouter",
		},
		{
			name: "negative reranker max candidates",
			cfg: config.Config{
				Storage:  config.StorageConfig{Backend: "sqlite"},
				Models:   config.ModelsConfig{Embedding: config.EmbeddingModelConfig{Name: "text-embedding-test", Provider: "openai"}},
				Search:   config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true}},
				Reranker: config.RerankerConfig{MaxCandidates: -1},
			},
			err: "reranker max candidates must be non-negative",
		},
		{
			name: "negative reranker max candidate text chars",
			cfg: config.Config{
				Storage:  config.StorageConfig{Backend: "sqlite"},
				Models:   config.ModelsConfig{Embedding: config.EmbeddingModelConfig{Name: "text-embedding-test", Provider: "openai"}},
				Search:   config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true}},
				Reranker: config.RerankerConfig{MaxCandidateTextChars: -1},
			},
			err: "reranker max candidate text chars must be non-negative",
		},
		{
			name: "negative reranker max output tokens",
			cfg: config.Config{
				Storage:  config.StorageConfig{Backend: "sqlite"},
				Models:   config.ModelsConfig{Embedding: config.EmbeddingModelConfig{Name: "text-embedding-test", Provider: "openai"}},
				Search:   config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true}},
				Reranker: config.RerankerConfig{MaxOutputTokens: -1},
			},
			err: "reranker max output tokens must be non-negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			homeDir := t.TempDir()
			setProfileHome(t, homeDir)

			store, err := OpenStore(&tt.cfg)

			require.Nil(t, store)
			require.EqualError(t, err, tt.err)
		})
	}
}

func TestOpenStore_AcceptsOllamaEmbeddingProvider(t *testing.T) {
	homeDir := t.TempDir()
	setProfileHome(t, homeDir)

	originalProvider := newStoreEmbeddingProvider
	t.Cleanup(func() {
		newStoreEmbeddingProvider = originalProvider
	})

	newStoreEmbeddingProvider = func(*config.Config) (search.Embedder, error) {
		return &storeTestEmbeddingProvider{}, nil
	}

	store, err := OpenStore(&config.Config{
		Storage: config.StorageConfig{Backend: "memory"},
		Models: config.ModelsConfig{
			Embedding: config.EmbeddingModelConfig{
				Name:     constants.DefaultOllamaEmbeddingModel,
				Provider: constants.ModelProviderOllama,
			},
		},
		Search: config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true}},
	})

	require.NoError(t, err)
	require.IsType(t, &storagememory.Store{}, store)
}

func TestOpenStore_ValidatesVectorStoreFactories(t *testing.T) {
	t.Run("sqlite vector store error", func(t *testing.T) {
		homeDir := t.TempDir()
		setProfileHome(t, homeDir)

		originalProvider := newStoreEmbeddingProvider
		originalSQLiteStore := newSQLiteVectorStore
		t.Cleanup(func() {
			newStoreEmbeddingProvider = originalProvider
			newSQLiteVectorStore = originalSQLiteStore
		})

		newStoreEmbeddingProvider = func(*config.Config) (search.Embedder, error) {
			return &storeTestEmbeddingProvider{}, nil
		}
		newSQLiteVectorStore = func(*gorm.DB) (search.VectorStore, error) {
			return nil, errors.New("vector factory failed")
		}

		store, err := OpenStore(&config.Config{
			Storage: config.StorageConfig{Backend: "sqlite"},
			Models: config.ModelsConfig{
				Providers: map[string]config.ProviderModelConfig{"openai": {APIKey: "key"}},
				Embedding: config.EmbeddingModelConfig{Name: "text-embedding-test", Provider: "openai"},
			},
			Search: config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true}},
		})

		require.Nil(t, store)
		require.EqualError(t, err, "vector factory failed")
	})

	t.Run("sqlite vector store is required", func(t *testing.T) {
		homeDir := t.TempDir()
		setProfileHome(t, homeDir)

		originalProvider := newStoreEmbeddingProvider
		originalSQLiteStore := newSQLiteVectorStore
		t.Cleanup(func() {
			newStoreEmbeddingProvider = originalProvider
			newSQLiteVectorStore = originalSQLiteStore
		})

		newStoreEmbeddingProvider = func(*config.Config) (search.Embedder, error) {
			return &storeTestEmbeddingProvider{}, nil
		}
		newSQLiteVectorStore = func(*gorm.DB) (search.VectorStore, error) {
			return nil, nil
		}

		store, err := OpenStore(&config.Config{
			Storage: config.StorageConfig{Backend: "sqlite"},
			Models: config.ModelsConfig{
				Providers: map[string]config.ProviderModelConfig{"openai": {APIKey: "key"}},
				Embedding: config.EmbeddingModelConfig{Name: "text-embedding-test", Provider: "openai"},
			},
			Search: config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true}},
		})

		require.Nil(t, store)
		require.EqualError(t, err, "sqlite vector store is required")
	})

	t.Run("memory vector store is required", func(t *testing.T) {
		originalProvider := newStoreEmbeddingProvider
		originalMemoryStore := newMemoryVectorStore
		t.Cleanup(func() {
			newStoreEmbeddingProvider = originalProvider
			newMemoryVectorStore = originalMemoryStore
		})

		newStoreEmbeddingProvider = func(*config.Config) (search.Embedder, error) {
			return &storeTestEmbeddingProvider{}, nil
		}
		newMemoryVectorStore = func() search.VectorStore {
			return nil
		}

		store, err := OpenStore(&config.Config{
			Storage: config.StorageConfig{Backend: "memory"},
			Models:  config.ModelsConfig{Embedding: config.EmbeddingModelConfig{Name: "text-embedding-test", Provider: "openai"}},
			Search:  config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true}},
		})

		require.Nil(t, store)
		require.EqualError(t, err, "memory vector store is required")
	})
}

func TestOpenStore_VectorSearchEndToEnd(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			store, lister := openStoreWithE2EVectorSearch(t, backend, &e2eVectorEmbeddingProvider{}, false)
			sessionID := newStoreTestSessionID(t)

			require.NoError(t, store.Session().Save(ctx, storage.Session{ID: sessionID}))
			require.NoError(t, store.Session().AppendMessages(ctx, sessionID, []morphmsg.Message{
				{Role: morphmsg.RoleUser, Content: "semantic target document", CreatedAt: time.Now().UTC()},
				{Role: morphmsg.RoleUser, Content: "different reference document", CreatedAt: time.Now().UTC().Add(time.Second)},
			}))
			requireVectorRecordCount(t, lister, 2)

			results, err := store.Session().SearchMessages(ctx, sessionID, storage.SearchMessageOptions{
				Query:                 "related idea",
				MaxSessions:           1,
				MaxMessagesPerSession: 1,
			})

			require.NoError(t, err)
			require.Len(t, results, 1)
			require.Len(t, results[0].Messages, 1)
			require.Equal(t, "semantic target document", results[0].Messages[0].Message.Content)
		})
	}
}

func TestOpenStore_VectorRowsLifecycleEndToEnd(t *testing.T) {
	operations := map[string]func(context.Context, storage.Store, string) error{
		"delete": func(ctx context.Context, store storage.Store, sessionID string) error {
			return store.Session().Delete(ctx, sessionID)
		},
		"clear": func(ctx context.Context, store storage.Store, sessionID string) error {
			return store.Session().ClearMessages(ctx, sessionID)
		},
		"archive": func(ctx context.Context, store storage.Store, sessionID string) error {
			_, err := store.Session().Archive(ctx, sessionID, storage.SessionArchiveRequest{
				ExpiresAt: time.Now().UTC().Add(time.Hour),
			})
			return err
		},
	}

	for _, backend := range []string{"memory", "sqlite"} {
		for name, operation := range operations {
			t.Run(backend+" "+name, func(t *testing.T) {
				ctx := context.Background()
				store, lister := openStoreWithE2EVectorSearch(t, backend, &e2eVectorEmbeddingProvider{}, false)
				sessionID := newStoreTestSessionID(t)

				require.NoError(t, store.Session().Save(ctx, storage.Session{ID: sessionID}))
				require.NoError(t, store.Session().AppendMessages(ctx, sessionID, []morphmsg.Message{{
					Role:      morphmsg.RoleUser,
					Content:   "semantic target document",
					CreatedAt: time.Now().UTC(),
				}}))
				requireVectorRecordCount(t, lister, 1)

				require.NoError(t, operation(ctx, store, sessionID))
				expectedRecords := 0
				if name == "archive" {
					expectedRecords = 1
				}
				requireVectorRecordCount(t, lister, expectedRecords)
			})
		}
	}
}

func TestOpenStore_BM25SearchWhenVectorDisabled(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			if backend == "sqlite" {
				setProfileHome(t, t.TempDir())
			}

			store, err := OpenStore(e2eVectorStoreConfig(backend, false, false))
			require.NoError(t, err)

			sessionID := newStoreTestSessionID(t)
			require.NoError(t, store.Session().Save(ctx, storage.Session{ID: sessionID}))
			require.NoError(t, store.Session().AppendMessages(ctx, sessionID, []morphmsg.Message{{
				Role:      morphmsg.RoleUser,
				Content:   "needle lexical result",
				CreatedAt: time.Now().UTC(),
			}}))

			results, err := store.Session().SearchMessages(ctx, sessionID, storage.SearchMessageOptions{Query: "needle"})

			require.NoError(t, err)
			require.Len(t, results, 1)
			require.Len(t, results[0].Messages, 1)
			require.Equal(t, "needle lexical result", results[0].Messages[0].Message.Content)
		})
	}
}

func TestOpenStore_VectorErrorsDoNotFailMessagePersistence(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		for _, required := range []bool{false, true} {
			name := backend + "_best_effort"
			if required {
				name = backend + "_required"
			}

			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				provider := &e2eVectorEmbeddingProvider{err: errors.New("embedding failed")}
				store, _ := openStoreWithE2EVectorSearch(t, backend, provider, required)
				sessionID := newStoreTestSessionID(t)

				require.NoError(t, store.Session().Save(ctx, storage.Session{ID: sessionID}))
				err := store.Session().AppendMessages(ctx, sessionID, []morphmsg.Message{{
					Role:      morphmsg.RoleUser,
					Content:   "semantic target document",
					CreatedAt: time.Now().UTC(),
				}})

				require.Len(t, provider.requests, 1)
				require.NoError(t, err)
			})
		}
	}
}

func TestStoreReranker_SelectsConfiguredReranker(t *testing.T) {
	disabled := false

	tests := []struct {
		name     string
		cfg      config.Config
		client   models.Client
		wantName string
		wantType any
		wantErr  string
		wantNil  bool
	}{
		{
			name:    "vector disabled",
			cfg:     config.Config{},
			wantNil: true,
		},
		{
			name: "deterministic default",
			cfg: config.Config{
				Search: config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true}},
			},
			wantName: search.RerankerDeterministic,
			wantType: search.DeterministicReranker{},
		},
		{
			name: search.RerankerNoop,
			cfg: config.Config{
				Search:   config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true}},
				Reranker: config.RerankerConfig{Type: search.RerankerNoop},
			},
			wantName: search.RerankerNoop,
			wantType: search.NoopReranker{},
		},
		{
			name: "globally disabled llm does not require client",
			cfg: config.Config{
				Search:   config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true}},
				Reranker: config.RerankerConfig{Enabled: &disabled, Type: search.RerankerLLM},
			},
			wantNil: true,
		},
		{
			name: "search disabled llm does not require client",
			cfg: config.Config{
				Search:   config.SearchConfig{EnableRerank: &disabled, Vector: config.SearchVectorConfig{Enabled: true}},
				Reranker: config.RerankerConfig{Type: search.RerankerLLM},
			},
			wantNil: true,
		},
		{
			name: "llm requires client",
			cfg: config.Config{
				Search:   config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true}},
				Reranker: config.RerankerConfig{Type: search.RerankerLLM},
			},
			wantErr: "reranker model client is required",
		},
		{
			name: search.RerankerLLM,
			cfg: config.Config{
				Models:   config.ModelsConfig{Main: config.MainModelConfig{Name: "openai/gpt-4o-mini", API: modelprovider.APIOpenAICompletions}},
				Search:   config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true}},
				Reranker: config.RerankerConfig{Type: search.RerankerLLM, Model: "openai/gpt-4o-mini", MaxCandidates: 3, MaxCandidateTextChars: 40, MaxOutputTokens: 50},
			},
			client:   &storeTestModelClient{},
			wantName: search.RerankerLLM,
			wantType: search.LLMReranker{},
		},
		{
			name: "use case override",
			cfg: config.Config{
				Models: config.ModelsConfig{
					Main: config.MainModelConfig{
						Name: "openai/gpt-4o-mini",
						API:  modelprovider.APIOpenAICompletions,
					},
				},
				Search: config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true}},
				Reranker: config.RerankerConfig{
					Type: search.RerankerDeterministic,
					Overrides: map[string]config.RerankerOverrideConfig{
						"memory_reflection": {
							Type:                  search.RerankerLLM,
							Model:                 "openai/gpt-4o-mini",
							MaxCandidates:         managerTestIntPtr(3),
							MaxCandidateTextChars: managerTestIntPtr(40),
							MaxOutputTokens:       managerTestIntPtr(50),
						},
					},
				},
			},
			client:   &storeTestModelClient{},
			wantName: search.RerankerDeterministic,
			wantType: search.UseCaseReranker{},
		},
		{
			name: "use case override construction error",
			cfg: config.Config{
				Search: config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true}},
				Reranker: config.RerankerConfig{
					Type: search.RerankerDeterministic,
					Overrides: map[string]config.RerankerOverrideConfig{
						"memory_reflection": {Type: search.RerankerLLM},
					},
				},
			},
			wantErr: "reranker model client is required",
		},
		{
			name: "invalid reranker",
			cfg: config.Config{
				Search:   config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true}},
				Reranker: config.RerankerConfig{Type: "invalid"},
			},
			wantErr: "reranker type must be one of: deterministic, noop, llm",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reranker, err := storeReranker(&tt.cfg, tt.client)
			if tt.wantErr != "" {
				require.Nil(t, reranker)
				require.EqualError(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			if tt.wantNil {
				require.Nil(t, reranker)
				return
			}
			require.IsType(t, tt.wantType, reranker)
			require.Equal(t, tt.wantName, reranker.Name())
		})
	}
}

func TestConfiguredRerankerOverrides_ReturnsNilWithoutOverrides(t *testing.T) {
	overrides, err := configuredRerankerOverrides(&config.Config{}, nil)

	require.NoError(t, err)
	require.Nil(t, overrides)
}

func TestConfiguredRerankerOverrides_ReturnsConstructionError(t *testing.T) {
	overrides, err := configuredRerankerOverrides(&config.Config{
		Reranker: config.RerankerConfig{
			Overrides: map[string]config.RerankerOverrideConfig{
				"memory_reflection": {Type: search.RerankerLLM},
			},
		},
	}, nil)

	require.Nil(t, overrides)
	require.EqualError(t, err, "reranker model client is required")
}

func TestConfiguredReranker_UsesRerankerModelAPI(t *testing.T) {
	client := &storeTestModelClient{}
	cfg := &config.Config{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderModelConfig{"github-copilot": {APIKey: "key"}},
			Main:      config.MainModelConfig{Name: "gpt-4.1", Provider: "github-copilot", API: modelprovider.APIOpenAICompletions},
			Summary:   config.SummaryModelConfig{Name: "gpt-4.1", Provider: "github-copilot", API: modelprovider.APIOpenAICompletions},
		},
		Reranker: config.RerankerConfig{Type: search.RerankerLLM, Model: "claude-sonnet-4.5"},
	}

	reranker, err := configuredReranker(cfg, client, config.RerankerOverrideConfig{})
	require.NoError(t, err)

	_, err = reranker.Rerank(context.Background(), search.RerankRequest{
		Query: "query",
		Candidates: []search.Candidate{{
			ID:           "candidate",
			SourceKind:   search.SourceKindMemoryItem,
			MemoryID:     "memory",
			Text:         "candidate text",
			LexicalScore: 1,
			VectorScore:  1,
			FusedScore:   1,
		}},
	})

	require.NoError(t, err)
	require.Len(t, client.requests, 1)
	require.Equal(t, modelprovider.APIAnthropicMessages, client.requests[0].API)
	require.Equal(t, "claude-sonnet-4.5", client.requests[0].Model)
}

func TestOpenStore_DefaultVectorStores(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "vectors.db")), &gorm.Config{})
	require.NoError(t, err)

	sqliteStore, err := newSQLiteVectorStore(db)
	require.NoError(t, err)
	require.NotNil(t, sqliteStore)
	require.NotNil(t, newMemoryVectorStore())
}

func TestDefaultStoreEmbeddingProviderReturnsProvider(t *testing.T) {
	provider, err := defaultStoreEmbeddingProvider(&config.Config{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderModelConfig{"openai": {APIKey: "key"}},
			Embedding: config.EmbeddingModelConfig{Name: "text-embedding-test", Provider: "openai"},
		},
	})

	require.NoError(t, err)
	require.NotNil(t, provider)
}

func TestStoreRerankEnabledDefaults(t *testing.T) {
	require.True(t, storeRerankEnabled(nil))

	var cfg config.Config
	require.Nil(t, storeSearchRerankEnabledOption(nil))
	require.Nil(t, storeSearchRerankEnabledOption(&cfg))
}

func withStoreVectorHooks(
	t *testing.T,
	provider search.Embedder,
	sqliteStore search.VectorStore,
	memoryStore search.VectorStore,
) {
	t.Helper()

	originalProvider := newStoreEmbeddingProvider
	originalSQLiteStore := newSQLiteVectorStore
	originalMemoryStore := newMemoryVectorStore
	t.Cleanup(func() {
		newStoreEmbeddingProvider = originalProvider
		newSQLiteVectorStore = originalSQLiteStore
		newMemoryVectorStore = originalMemoryStore
	})

	if provider != nil {
		newStoreEmbeddingProvider = func(*config.Config) (search.Embedder, error) {
			return provider, nil
		}
	}
	if sqliteStore != nil {
		newSQLiteVectorStore = func(*gorm.DB) (search.VectorStore, error) {
			return sqliteStore, nil
		}
	}
	if memoryStore != nil {
		newMemoryVectorStore = func() search.VectorStore {
			return memoryStore
		}
	}
}

type storeTestEmbeddingProvider struct {
	requests []search.EmbeddingRequest
}

type storeTestModelClient struct {
	requests []models.Request
}

func (c *storeTestModelClient) Complete(_ context.Context, req models.Request) (*models.Response, error) {
	c.requests = append(c.requests, req)
	return &models.Response{OutputText: `{"items":[]}`}, nil
}

func (c *storeTestModelClient) CompleteStream(
	_ context.Context,
	req models.Request,
	_ func(models.StreamDelta),
) (*models.Response, error) {
	c.requests = append(c.requests, req)
	return &models.Response{OutputText: `{"items":[]}`}, nil
}

func (p *storeTestEmbeddingProvider) Embed(
	_ context.Context,
	req search.EmbeddingRequest,
) (search.EmbeddingResult, error) {
	p.requests = append(p.requests, req)

	items := make([]search.Embedding, 0, len(req.Inputs))
	for idx, input := range req.Inputs {
		items = append(items, search.Embedding{
			ID:          input.ID,
			ContentHash: search.VectorContentHash(input.Text),
			Vector:      []float64{1, float64(idx + 1)},
		})
	}

	return search.EmbeddingResult{
		Model:      req.Model,
		Items:      items,
		Dimensions: 2,
	}, nil
}

type storeTestVectorStore struct {
	upserts [][]search.VectorRecord
}

func (s *storeTestVectorStore) Upsert(_ context.Context, records []search.VectorRecord) error {
	cloned := make([]search.VectorRecord, len(records))
	copy(cloned, records)
	s.upserts = append(s.upserts, cloned)
	return nil
}

func (s *storeTestVectorStore) Delete(context.Context, search.VectorDeleteRequest) error {
	return nil
}

func (s *storeTestVectorStore) Search(
	context.Context,
	search.VectorSearchRequest,
) (search.VectorSearchResult, error) {
	return search.VectorSearchResult{}, nil
}

func (s *storeTestVectorStore) Metadata(context.Context) (search.VectorStoreMetadata, error) {
	return search.VectorStoreMetadata{}, nil
}

func openStoreWithE2EVectorSearch(
	t *testing.T,
	backend string,
	provider search.Embedder,
	required bool,
) (storage.Store, search.VectorRecordLister) {
	t.Helper()

	if backend == "sqlite" {
		setProfileHome(t, t.TempDir())
	}

	originalProvider := newStoreEmbeddingProvider
	originalSQLiteStore := newSQLiteVectorStore
	originalMemoryStore := newMemoryVectorStore
	t.Cleanup(func() {
		newStoreEmbeddingProvider = originalProvider
		newSQLiteVectorStore = originalSQLiteStore
		newMemoryVectorStore = originalMemoryStore
	})

	var lister search.VectorRecordLister
	newStoreEmbeddingProvider = func(*config.Config) (search.Embedder, error) {
		return provider, nil
	}
	newMemoryVectorStore = func() search.VectorStore {
		store := vectormemory.NewStore()
		lister = store
		return store
	}
	newSQLiteVectorStore = func(db *gorm.DB) (search.VectorStore, error) {
		store, err := vectorsqlite.NewStoreFromDB(db)
		if err != nil {
			return nil, err
		}
		lister = store
		return store, nil
	}

	store, err := OpenStore(e2eVectorStoreConfig(backend, true, required))
	require.NoError(t, err)
	require.NotNil(t, store)
	require.NotNil(t, lister)

	return store, lister
}

func e2eVectorStoreConfig(backend string, vectorEnabled bool, required bool) *config.Config {
	rerankEnabled := false
	return &config.Config{
		Storage: config.StorageConfig{Backend: backend},
		Models: config.ModelsConfig{
			Embedding: config.EmbeddingModelConfig{Name: e2eVectorEmbeddingModel, Provider: "openai"},
		},
		Search: config.SearchConfig{
			EnableRerank: &rerankEnabled,
			Vector: config.SearchVectorConfig{
				Enabled:  vectorEnabled,
				Required: required,
			},
		},
	}
}

func newStoreTestSessionID(t *testing.T) string {
	t.Helper()

	sessionID, err := storage.NewSessionID()
	require.NoError(t, err)

	return sessionID
}

func requireVectorRecordCount(t *testing.T, lister search.VectorRecordLister, count int) {
	t.Helper()

	list, err := lister.List(context.Background(), search.VectorListRequest{
		EmbeddingModel: e2eVectorEmbeddingModel,
		Filter: search.VectorFilter{
			SourceKind: search.SourceKindSessionMessage,
		},
	})
	require.NoError(t, err)
	require.Len(t, list.Records, count)
}

type e2eVectorEmbeddingProvider struct {
	requests []search.EmbeddingRequest
	err      error
}

func (p *e2eVectorEmbeddingProvider) Embed(
	_ context.Context,
	req search.EmbeddingRequest,
) (search.EmbeddingResult, error) {
	p.requests = append(p.requests, req)
	if p.err != nil {
		return search.EmbeddingResult{}, p.err
	}

	items := make([]search.Embedding, 0, len(req.Inputs))
	for _, input := range req.Inputs {
		items = append(items, search.Embedding{
			ID:          input.ID,
			ContentHash: search.VectorContentHash(input.Text),
			Vector:      e2eVectorForText(input.Text),
		})
	}

	return search.EmbeddingResult{
		Model:      req.Model,
		Items:      items,
		Dimensions: 3,
	}, nil
}

func e2eVectorForText(text string) []float64 {
	text = strings.ToLower(text)
	switch {
	case strings.Contains(text, "semantic target"), strings.Contains(text, "related idea"):
		return []float64{1, 0, 0}
	case strings.Contains(text, "different reference"):
		return []float64{0, 1, 0}
	default:
		return []float64{0, 0, 1}
	}
}

func setProfileHome(t *testing.T, home string) {
	t.Helper()

	original := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(original)
	})
	profile.SetActive(profile.Profile{Name: "test", HomeDir: home})
}

func managerTestIntPtr(value int) *int {
	return &value
}
