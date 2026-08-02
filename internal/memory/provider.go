package memory

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/xymorphic/morph/internal/constants"
	"github.com/xymorphic/morph/internal/memory/episodic"
	pinnedmemory "github.com/xymorphic/morph/internal/memory/pinned"
	models "github.com/xymorphic/morph/internal/model"
	statecore "github.com/xymorphic/morph/internal/state/core"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/str"
)

// ProviderDefaultMemory is the package-level provider default memory constant.
const ProviderDefaultMemory = constants.MemoryProviderDefault

// ErrUnknownProvider is returned when memory provider config names an unsupported provider.
var ErrUnknownProvider = errors.New("unknown memory provider")

// ErrUnknownBackend is returned when memory provider config names an unsupported backend.
var ErrUnknownBackend = errors.New("unknown memory backend")

// Options configures this package operation.
type Options struct {
	Guardrails             Guardrails
	Observability          Observability
	StateManager           StateManager
	StorageBackend         string
	MemoryBackend          string
	Pinned                 PinnedOptions
	EpisodicBackground     EpisodicBackgroundOptions
	ReflectionBackground   ReflectionBackgroundOptions
	PromotionBackground    PromotionBackgroundOptions
	ModelClient            models.Client
	Model                  string
	API                    string
	MaxOutputTokensEnabled *bool
	DebugRequests          bool
	ReflectionGenerator    ReflectionGenerator
	PromotionPolicy        PromotionPolicy
}

// PinnedOptions aliases pinnedmemory.Options at this package boundary.
type PinnedOptions = pinnedmemory.Options

// StateManager is the persistence and session-history contract the provider
// needs. It intentionally combines memory storage with session/trace access
// because episodic extraction and reflection both need source evidence, not just
// memory rows.
type StateManager interface {
	SearchMemory(context.Context, SearchQuery) (SearchResult, error)
	UpsertMemory(context.Context, MemoryItem) (MemoryItem, error)
	PatchMemory(context.Context, MemoryPatch) (MemoryItem, error)
	DeleteMemory(context.Context, DeleteRequest) error
	HardDeleteMemory(context.Context, DeleteRequest) error
	CurrentSession(context.Context) (string, error)
	CountMessages(context.Context, string, statecore.MessageQueryOptions) (int, error)
	GetMessages(context.Context, string, statecore.MessageQueryOptions) ([]morphmsg.Message, error)
	ListTraceEvents(context.Context, statecore.TraceQuery) (statecore.TraceResult, error)
	UpdateCheckpoints(context.Context, string, statecore.CheckpointPatch) error
}

// MemoryProvider is the default implementation that composes storage,
// guardrails, model-backed extractors, reflection, promotion, and background
// loops. The mutex protects observability reconfiguration; storage consistency
// is handled by the manager/store layer.
type MemoryProvider struct {
	mu                            sync.RWMutex
	manager                       StateManager
	guardrails                    Guardrails
	obs                           Observability
	pinned                        PinnedOptions
	episodicExtractor             *episodic.Service
	episodicBackground            EpisodicBackgroundOptions
	episodicBackgroundStartOnce   sync.Once
	reflectionBackground          ReflectionBackgroundOptions
	reflectionBackgroundStartOnce sync.Once
	promotionBackground           PromotionBackgroundOptions
	promotionBackgroundStartOnce  sync.Once
	reflectionGenerator           ReflectionGenerator
	promotionPolicy               PromotionPolicy
}

// NewProvider is the name/backend selector used by environment setup. Today
// both memory and SQLite backends share the same provider logic and differ only
// in the StateManager they pass in.
func NewProvider(name string, opts Options) (Provider, error) {
	nameValue := str.String(name)
	switch nameValue.Normalized() {
	case "", ProviderDefaultMemory:
		switch getEffectiveBackend(opts) {
		case "memory", "sqlite":
			return NewFromManager(opts.StateManager, opts)
		default:
			return nil, ErrUnknownBackend
		}
	default:
		return nil, ErrUnknownProvider
	}
}

func getEffectiveBackend(opts Options) string {
	memoryBackendValue := str.String(opts.MemoryBackend)
	if backend := memoryBackendValue.Normalized(); backend != "" {
		return backend
	}
	storageBackendValue := str.String(opts.StorageBackend)
	if backend := storageBackendValue.Normalized(); backend != "" {
		return backend
	}
	return constants.DefaultStorageBackend
}

// NewFromManager builds the provider around an already configured state
// manager. Model-backed features are only installed when a model client exists;
// this keeps the provider usable in tests and simple local modes where only
// search/write are needed.
func NewFromManager(manager StateManager, opts Options) (*MemoryProvider, error) {
	if manager == nil {
		return nil, errors.New("state manager is required")
	}

	provider := &MemoryProvider{
		manager:              manager,
		guardrails:           opts.Guardrails,
		obs:                  opts.Observability,
		pinned:               pinnedmemory.NormalizeOptions(opts.Pinned),
		episodicBackground:   episodic.NormalizeBackgroundOptions(opts.EpisodicBackground),
		reflectionBackground: normalizeReflectionBackgroundOptions(opts.ReflectionBackground),
		promotionBackground:  normalizePromotionBackgroundOptions(opts.PromotionBackground),
		reflectionGenerator:  opts.ReflectionGenerator,
		promotionPolicy:      opts.PromotionPolicy,
	}

	if opts.ModelClient != nil {
		extractor, err := episodic.NewLLMExtractor(episodic.LLMExtractorOptions{
			Client:                 opts.ModelClient,
			Model:                  opts.Model,
			API:                    opts.API,
			MaxOutputTokensEnabled: opts.MaxOutputTokensEnabled,
			DebugRequests:          opts.DebugRequests,
		})
		if err != nil {
			return nil, err
		}

		service, err := episodic.NewService(manager, provider, extractor)
		if err != nil {
			return nil, err
		}
		provider.episodicExtractor = service

		if provider.reflectionGenerator == nil {
			generator, err := NewLLMReflectionGenerator(LLMReflectionGeneratorOptions{
				Client:                 opts.ModelClient,
				Model:                  opts.Model,
				API:                    opts.API,
				MaxOutputTokensEnabled: opts.MaxOutputTokensEnabled,
				DebugRequests:          opts.DebugRequests,
			})
			if err != nil {
				return nil, err
			}
			provider.reflectionGenerator = generator
		}
	}

	return provider, nil
}

func (p *MemoryProvider) Name() string {
	return ProviderDefaultMemory
}

func (p *MemoryProvider) Capabilities(context.Context) (Capabilities, error) {
	return Capabilities{
		SupportsPinned:              true,
		SupportsSearch:              true,
		SupportsWrite:               true,
		SupportsDelete:              true,
		SupportsEpisodeRecording:    true,
		SupportsSemanticRecording:   true,
		SupportsProceduralRecording: true,
		SupportsReflection:          p != nil && p.reflectionGenerator != nil,
		SupportsVectors:             p != nil && hasVectorSearchSupport(p.manager),
		SupportsReranking:           true,
		SupportsAudit:               p != nil && p.manager != nil,
		SupportsObservability:       true,
	}, nil
}

func hasVectorSearchSupport(manager StateManager) bool {
	vectorManager, ok := manager.(interface{ SupportsVectorSearch() bool })
	return ok && vectorManager.SupportsVectorSearch()
}

func (p *MemoryProvider) ConfigureObservability(obs Observability) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.obs = obs
	return nil
}

func (p *MemoryProvider) Close() error {
	return nil
}

func (p *MemoryProvider) LoadPinned(ctx context.Context, query SearchQuery) ([]MemoryItem, error) {
	if p == nil || p.manager == nil {
		return nil, errors.New("memory provider is required")
	}
	if !pinnedmemory.Enabled(p.pinned) {
		return nil, nil
	}
	if err := validateSearch(ctx, p.guardrails, query); err != nil {
		return nil, err
	}

	fileItems, err := pinnedmemory.LoadFile()
	if err != nil {
		return nil, err
	}

	dbItems, err := p.loadStorePinned(ctx, query)
	if err != nil {
		return nil, err
	}

	// File-pinned and store-pinned memories are prepared together so limit,
	// safety scanning, and redaction behave the same regardless of source.
	items := append(fileItems, dbItems...)
	items, err = pinnedmemory.PrepareItems(ctx, items, query, p.pinned, p.safetyScanPinnedItem, p.redactPinnedItem)
	if err != nil {
		return nil, err
	}
	if query.Limit > 0 && len(items) > query.Limit {
		items = items[:query.Limit]
	}

	fields := buildObservationFields(p.Name(), "load_pinned", map[string]any{"result_count": len(items)})
	logDebugAndTrace(ctx, p.observability(), "pinned memory loaded", "memory.pinned.loaded", fields)

	return items, nil
}

// loadStorePinned intentionally clears query text. Pinned retrieval is a direct
// context-load path; semantic search of general memories happens through Search.
func (p *MemoryProvider) loadStorePinned(ctx context.Context, query SearchQuery) ([]MemoryItem, error) {
	storeQuery := query
	storeQuery.Text = ""
	storeQuery.RerankerUseCase = statecore.MemoryRerankerUseCasePinned
	storeQuery.Kinds = []Kind{KindPinned}
	storeQuery.Statuses = []Status{StatusActive}

	result, err := p.manager.SearchMemory(ctx, storeQuery)
	if err != nil {
		return nil, err
	}

	items := make([]MemoryItem, 0, len(result.Hits))
	for _, hit := range result.Hits {
		items = append(items, hit.Item.Clone())
	}

	return items, nil
}

func (p *MemoryProvider) safetyScanPinnedItem(ctx context.Context, item MemoryItem) error {
	if p.guardrails == nil {
		return nil
	}

	return p.guardrails.SafetyScan(ctx, item)
}

func (p *MemoryProvider) redactPinnedItem(ctx context.Context, item MemoryItem) (MemoryItem, error) {
	return redactItem(ctx, p.guardrails, item)
}

func (p *MemoryProvider) Search(ctx context.Context, query SearchQuery) (SearchResult, error) {
	if p == nil || p.manager == nil {
		return SearchResult{}, errors.New("memory provider is required")
	}
	if err := validateSearch(ctx, p.guardrails, query); err != nil {
		return SearchResult{}, err
	}

	obs := p.observability()
	textValue := str.String(query.Text)
	startFields := buildObservationFields(p.Name(), "search", map[string]any{
		"query_chars":  len([]rune(textValue.Trim())),
		"kind_count":   len(query.Kinds),
		"status_count": len(query.Statuses),
		"limit":        query.Limit,
		"max_chars":    query.MaxChars,
	})
	logDebugAndTrace(ctx, obs, "memory search started for retrieval", "memory.search.started", startFields)

	// The manager may perform lexical search, vector search, reranking, or a
	// hybrid of those. The provider logs only the high-level search boundary and
	// applies prompt-facing redaction after storage returns canonical items.
	result, err := p.manager.SearchMemory(ctx, query)
	if err != nil {
		return SearchResult{}, err
	}

	hits := make([]SearchHit, 0, len(result.Hits))
	for _, hit := range result.Hits {
		redacted, err := redactItem(ctx, p.guardrails, hit.Item)
		if err != nil {
			return SearchResult{}, err
		}

		if query.MaxChars > 0 && len([]rune(redacted.Text)) > query.MaxChars {
			redacted.Text = string([]rune(redacted.Text)[:query.MaxChars])
		}

		hits = append(hits, SearchHit{
			Item:         redacted.Clone(),
			Score:        hit.Score,
			LexicalScore: hit.LexicalScore,
			VectorScore:  hit.VectorScore,
		})
	}

	fields := buildObservationFields(p.Name(), "search", map[string]any{"result_count": len(hits)})
	logDebugAndTrace(ctx, obs, "memory search completed for retrieval", "memory.search.completed", fields)

	return SearchResult{Hits: hits}, nil
}

func (p *MemoryProvider) Upsert(ctx context.Context, item MemoryItem) (MemoryItem, error) {
	if p == nil || p.manager == nil {
		return MemoryItem{}, errors.New("memory provider is required")
	}
	if err := validateWrite(ctx, p.guardrails, item); err != nil {
		return MemoryItem{}, err
	}

	item, err := p.manager.UpsertMemory(ctx, item)
	if err != nil {
		return MemoryItem{}, err
	}

	obs := p.observability()
	fields := buildObservationFields(p.Name(), "upsert", map[string]any{"memory_id": item.ID})
	logDebugAndTrace(ctx, obs, "memory item upserted", "memory.upsert.completed", fields)

	return item.Clone(), nil
}

func (p *MemoryProvider) Delete(ctx context.Context, req DeleteRequest) error {
	if p == nil || p.manager == nil {
		return errors.New("memory provider is required")
	}
	if err := validateDelete(ctx, p.guardrails, req); err != nil {
		return err
	}

	iDValue := str.String(req.ID)
	memoryID := iDValue.Trim()
	if memoryID == "" {
		return errors.New("memory id is required")
	}

	// Deletion is represented as a lifecycle state transition rather than a
	// blind store delete so audits can explain what happened and vector cleanup
	// can still be driven by the store's normal upsert/delete synchronization.
	item, err := p.loadMemoryByID(ctx, memoryID, []Status{StatusActive, StatusCandidate, StatusSuperseded})
	if err != nil {
		return err
	}
	previousStatus := item.Status
	item.Status = StatusDeleted
	item.Metadata = buildLifecycleMetadata(item.Metadata, "delete", req.Reason, previousStatus)

	if _, err := p.manager.UpsertMemory(ctx, item); err != nil {
		return err
	}

	fields := buildObservationFields(p.Name(), "delete", map[string]any{"memory_id": memoryID})
	traceRecord(ctx, p.observability(), "memory.delete.completed", fields)
	return nil
}

// RecordEpisode is the write path used by the episodic extraction service after
// it has already built provenance, candidate metadata, and deterministic IDs.
func (p *MemoryProvider) RecordEpisode(ctx context.Context, record EpisodeRecord) (MemoryItem, error) {
	item := record.Item.Clone()
	item.Kind = KindEpisodic
	if item.Status == "" {
		item.Status = StatusActive
	}
	return p.Upsert(ctx, item)
}

// ExtractEpisodes delegates to the model-backed episodic service. Keeping this
// method on the provider lets tools call extraction without knowing how the
// extractor is wired.
func (p *MemoryProvider) ExtractEpisodes(ctx context.Context, req ExtractionRequest) (ExtractionResult, error) {
	if p == nil || p.episodicExtractor == nil {
		return ExtractionResult{}, errors.New("memory extraction is not configured")
	}

	return p.episodicExtractor.Extract(ctx, req)
}

// StartBackground enables the provider-owned periodic jobs. Each job uses a
// sync.Once so repeated calls from setup paths are harmless.
func (p *MemoryProvider) StartBackground(ctx context.Context) error {
	if p == nil {
		return errors.New("memory provider is required")
	}

	ctx = getBackgroundContext(ctx)
	for _, start := range p.backgroundStarters() {
		if err := start(ctx); err != nil {
			return err
		}
	}

	return nil
}

func (p *MemoryProvider) backgroundStarters() []func(context.Context) error {
	return []func(context.Context) error{
		p.startEpisodicRecordingBackground,
		p.startReflectionBackground,
		p.startPromotionBackground,
	}
}

func (p *MemoryProvider) startEpisodicRecordingBackground(ctx context.Context) error {
	if !p.episodicBackground.Enabled {
		return nil
	}
	if p.episodicExtractor == nil {
		return errors.New("memory extraction is not configured")
	}

	opts := episodic.NormalizeBackgroundOptions(p.episodicBackground)
	p.episodicBackgroundStartOnce.Do(func() {
		go p.runEpisodicRecordingBackgroundLoop(ctx, opts)
	})

	return nil
}

// getBackgroundContext normalizes nil context for background startup. Callers still
// control cancellation by passing a real context.
func getBackgroundContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}

	return ctx
}

func (p *MemoryProvider) runEpisodicRecordingBackgroundLoop(ctx context.Context, opts EpisodicBackgroundOptions) {
	ticker := time.NewTicker(opts.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = p.episodicExtractor.RunBackground(ctx, episodic.BackgroundRequest{
				Options: opts,
				Trace: providerTraceRecorder{
					ctx: ctx,
					obs: p.observability(),
				},
			})
		}
	}
}

func (p *MemoryProvider) observability() Observability {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.obs
}

// providerTraceRecorder adapts package-level trace callbacks from the episodic
// service into the provider's configured observability sink.
type providerTraceRecorder struct {
	ctx context.Context
	obs Observability
}

func (r providerTraceRecorder) Record(event string, payload any) {
	traceRecord(r.ctx, r.obs, event, payload)
}
