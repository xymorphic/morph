package manager

import (
	"errors"

	"github.com/xymorphic/morph/internal/config"
	morphdb "github.com/xymorphic/morph/internal/db"
	models "github.com/xymorphic/morph/internal/model"
	storage "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/internal/state/search"
	vectormemory "github.com/xymorphic/morph/internal/state/search/vectorstore/memory"
	vectorsqlite "github.com/xymorphic/morph/internal/state/search/vectorstore/sqlite"
	storagememory "github.com/xymorphic/morph/internal/state/storememory"
	storagesqlite "github.com/xymorphic/morph/internal/state/storesqlite"
	"github.com/xymorphic/morph/pkg/str"
	"gorm.io/gorm"
)

var (
	newStoreEmbeddingProvider = defaultStoreEmbeddingProvider
	newSQLiteVectorStore      = func(db *gorm.DB) (search.VectorStore, error) {
		return vectorsqlite.NewStoreFromDB(db)
	}
	newMemoryVectorStore = func() search.VectorStore {
		return vectormemory.NewStore()
	}
	newSQLiteStoreFromDB = storagesqlite.NewStoreFromDB
)

// OpenStore opens a state store based on the configuration.
//
// It supports the following storage backends:
//
//   - sqlite: uses a SQLite database
//   - memory: uses a memory-based store
//
// The configuration is used to determine the storage backend to use.
//
// The function returns a Store interface that can persist state records.
func OpenStore(cfg *config.Config) (storage.Store, error) {
	return OpenStoreWithRerankerClient(cfg, nil)
}

// OpenStoreWithRerankerClient opens store with reranker client.
func OpenStoreWithRerankerClient(
	cfg *config.Config,
	rerankerClient models.Client,
) (storage.Store, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}

	cfg.Normalize()
	backendValue := str.String(cfg.Storage.Backend)
	switch backendValue.Normalized() {
	case "", "sqlite":
		if err := validateSearchVectorConfig(cfg); err != nil {
			return nil, err
		}
		provider, err := storeEmbeddingProvider(cfg)
		if err != nil {
			return nil, err
		}

		db, err := morphdb.Open(cfg)
		if err != nil {
			return nil, err
		}

		store, err := newSQLiteStoreFromDB(db)
		if err != nil {
			return nil, err
		}
		reranker, err := storeReranker(cfg, rerankerClient)
		if err != nil {
			return nil, err
		}
		if err := configureSQLiteStoreVectors(cfg, db, store, provider, reranker); err != nil {
			return nil, err
		}

		return store, nil
	case "memory":
		if err := validateSearchVectorConfig(cfg); err != nil {
			return nil, err
		}
		provider, err := storeEmbeddingProvider(cfg)
		if err != nil {
			return nil, err
		}

		store := storagememory.NewStore()
		reranker, err := storeReranker(cfg, rerankerClient)
		if err != nil {
			return nil, err
		}
		if err := configureMemoryStoreVectors(cfg, store, provider, reranker); err != nil {
			return nil, err
		}

		return store, nil
	default:
		return nil, errors.New("storage backend must be one of: memory, sqlite")
	}
}

func configureMemoryStoreVectors(
	cfg *config.Config,
	store *storagememory.Store,
	provider search.Embedder,
	reranker search.Reranker,
) error {
	if !cfg.Search.Vector.Enabled {
		return nil
	}
	if provider == nil {
		return errors.New("embedding provider is required")
	}

	vectorStore := newMemoryVectorStore()
	if vectorStore == nil {
		return errors.New("memory vector store is required")
	}

	return store.ConfigureVectorStore(search.VectorStoreOptions{
		Embedder:            provider,
		Reranker:            reranker,
		VectorStore:         vectorStore,
		EnableRerank:        storeSearchRerankEnabledOption(cfg),
		EmbeddingModel:      cfg.Models.Embedding.Name,
		MaxInputBytes:       cfg.Search.Vector.MaxInputBytes,
		MaxDocumentBytes:    cfg.Search.Vector.MaxDocumentBytes,
		RerankMaxCandidates: cfg.Reranker.MaxCandidates,
		Required:            cfg.Search.Vector.Required,
		Diagnostics:         cfg.Debug.Requests,
	})
}

func configureSQLiteStoreVectors(
	cfg *config.Config,
	db *gorm.DB,
	store *storagesqlite.Store,
	provider search.Embedder,
	reranker search.Reranker,
) error {
	if !cfg.Search.Vector.Enabled {
		return nil
	}
	if provider == nil {
		return errors.New("embedding provider is required")
	}

	vectorStore, err := newSQLiteVectorStore(db)
	if err != nil {
		return err
	}
	if vectorStore == nil {
		return errors.New("sqlite vector store is required")
	}

	return store.ConfigureVectorStore(storagesqlite.VectorStoreOptions{
		Embedder:            provider,
		Reranker:            reranker,
		VectorStore:         vectorStore,
		EnableRerank:        storeSearchRerankEnabledOption(cfg),
		EmbeddingModel:      cfg.Models.Embedding.Name,
		RebuildBatchSize:    cfg.Search.Vector.RebuildBatchSize,
		MaxInputBytes:       cfg.Search.Vector.MaxInputBytes,
		MaxDocumentBytes:    cfg.Search.Vector.MaxDocumentBytes,
		RerankMaxCandidates: cfg.Reranker.MaxCandidates,
		Required:            cfg.Search.Vector.Required,
		Diagnostics:         cfg.Debug.Requests,
	})
}

func validateSearchVectorConfig(cfg *config.Config) error {
	if !cfg.Search.Vector.Enabled {
		return nil
	}

	if cfg.Models.Embedding.Name == "" {
		return errors.New("embedding model is required")
	}

	if cfg.Search.Vector.RebuildBatchSize < 0 {
		return errors.New("vector rebuild batch size must be non-negative")
	}
	if cfg.Search.Vector.MaxInputBytes < 0 {
		return errors.New("vector max input bytes must be non-negative")
	}
	if cfg.Search.Vector.MaxDocumentBytes < 0 {
		return errors.New("vector max document bytes must be non-negative")
	}
	chunking := search.NormalizeVectorChunkOptions(search.VectorChunkOptions{
		MaxInputBytes:    cfg.Search.Vector.MaxInputBytes,
		MaxDocumentBytes: cfg.Search.Vector.MaxDocumentBytes,
	})
	if chunking.MaxDocumentBytes < chunking.MaxInputBytes {
		return errors.New("vector max document bytes must be greater than or equal to max input bytes")
	}

	if cfg.Reranker.MaxCandidates < 0 {
		return errors.New("reranker max candidates must be non-negative")
	}

	if cfg.Reranker.MaxCandidateTextChars < 0 {
		return errors.New("reranker max candidate text chars must be non-negative")
	}

	if cfg.Reranker.MaxOutputTokens < 0 {
		return errors.New("reranker max output tokens must be non-negative")
	}

	return nil
}

func storeEmbeddingProvider(cfg *config.Config) (search.Embedder, error) {
	if !cfg.Search.Vector.Enabled {
		return nil, nil
	}

	return newStoreEmbeddingProvider(cfg)
}

func defaultStoreEmbeddingProvider(cfg *config.Config) (search.Embedder, error) {
	auth, err := cfg.ResolveEmbeddingModelAuth()
	if err != nil {
		return nil, err
	}
	chunking := search.NormalizeVectorChunkOptions(search.VectorChunkOptions{
		MaxInputBytes:    cfg.Search.Vector.MaxInputBytes,
		MaxDocumentBytes: cfg.Search.Vector.MaxDocumentBytes,
	})

	return search.NewEmbeddingProvider(search.EmbeddingProviderOptions{
		Provider:          auth.Provider,
		API:               auth.API,
		APIKey:            auth.APIKey,
		EndpointURL:       auth.BaseURL,
		MaxInputTextBytes: chunking.MaxInputBytes,
	})
}

func storeReranker(
	cfg *config.Config,
	client models.Client,
) (search.Reranker, error) {
	if !cfg.Search.Vector.Enabled {
		return nil, nil
	}

	if !storeRerankEnabled(cfg) {
		return nil, nil
	}

	defaultReranker, err := configuredReranker(cfg, client, config.RerankerOverrideConfig{})
	if err != nil {
		return nil, err
	}

	overrides, err := configuredRerankerOverrides(cfg, client)
	if err != nil {
		return nil, err
	}
	if len(overrides) == 0 {
		return defaultReranker, nil
	}

	return search.UseCaseReranker{
		Default:  defaultReranker,
		Override: overrides,
		Fallback: search.DeterministicReranker{},
	}, nil
}

func configuredRerankerOverrides(cfg *config.Config, client models.Client) (map[string]search.Reranker, error) {
	if len(cfg.Reranker.Overrides) == 0 {
		return nil, nil
	}

	overrides := make(map[string]search.Reranker, len(cfg.Reranker.Overrides))
	for useCase, override := range cfg.Reranker.Overrides {
		reranker, err := configuredReranker(cfg, client, override)
		if err != nil {
			return nil, err
		}
		useCaseValue := str.String(useCase)
		overrides[useCaseValue.Normalized()] = reranker
	}

	return overrides, nil
}

func configuredReranker(
	cfg *config.Config,
	client models.Client,
	override config.RerankerOverrideConfig,
) (search.Reranker, error) {
	reranker := cfg.RerankerOverrideEffective(override)

	switch reranker.Type {
	case search.RerankerDeterministic:
		return search.DeterministicReranker{}, nil
	case search.RerankerNoop:
		return search.NoopReranker{}, nil
	case search.RerankerLLM:
		if client == nil {
			return nil, errors.New("reranker model client is required")
		}

		return search.NewLLMReranker(search.LLMRerankerOptions{
			Fallback:                 search.DeterministicReranker{},
			Client:                   client,
			Model:                    reranker.Model,
			API:                      cfg.RerankerModelAPIEffectiveForModel(reranker.Model),
			MaxCandidates:            reranker.MaxCandidates,
			MaxCandidatesSet:         reranker.MaxCandidatesSet,
			MaxCandidateTextChars:    reranker.MaxCandidateTextChars,
			MaxCandidateTextCharsSet: reranker.MaxCandidateTextCharsSet,
			MaxOutputTokens:          int64(reranker.MaxOutputTokens),
			Enabled:                  true,
			DebugRequests:            cfg.Debug.Requests,
		}), nil
	default:
		return nil, errors.New("reranker type must be one of: deterministic, noop, llm")
	}
}

func storeRerankEnabled(cfg *config.Config) bool {
	if cfg == nil {
		return true
	}
	if cfg.Reranker.Enabled != nil && !*cfg.Reranker.Enabled {
		return false
	}
	if cfg.Search.EnableRerank == nil {
		return true
	}

	return *cfg.Search.EnableRerank
}

func storeSearchRerankEnabledOption(cfg *config.Config) *bool {
	if cfg == nil || (cfg.Reranker.Enabled == nil && cfg.Search.EnableRerank == nil) {
		return nil
	}

	enabled := storeRerankEnabled(cfg)
	return &enabled
}
