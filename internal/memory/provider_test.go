package memory

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	models "github.com/xymorphic/morph/internal/model"
	"github.com/xymorphic/morph/internal/profile"
	statecore "github.com/xymorphic/morph/internal/state/core"
	statemanager "github.com/xymorphic/morph/internal/state/manager"
	storagememory "github.com/xymorphic/morph/internal/state/storememory"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/logutils"
)

func init() {
	logutils.SetOutput(io.Discard)
}

func TestNewProvider_ReturnsConfiguredProvider(t *testing.T) {
	provider, err := NewProvider("", Options{StateManager: newMemoryTestManager(t, storagememory.NewStore())})
	require.NoError(t, err)
	require.IsType(t, &MemoryProvider{}, provider)
	require.Equal(t, ProviderDefaultMemory, provider.Name())

	provider, err = NewProvider(" default-memory ", Options{StateManager: newMemoryTestManager(t, storagememory.NewStore())})
	require.NoError(t, err)
	require.IsType(t, &MemoryProvider{}, provider)
	require.Equal(t, ProviderDefaultMemory, provider.Name())
	require.NoError(t, provider.Close())
}

func TestNewProvider_DefaultMemoryBackendResolution(t *testing.T) {
	provider, err := NewProvider("default-memory", Options{
		StorageBackend: "memory",
		StateManager:   newMemoryTestManager(t, storagememory.NewStore()),
	})
	require.NoError(t, err)
	require.IsType(t, &MemoryProvider{}, provider)

	writer := provider.(WriteProvider)
	searcher := provider.(SearchProvider)
	_, err = writer.Upsert(t.Context(), MemoryItem{Status: StatusActive, Text: "ephemeral memory"})
	require.NoError(t, err)
	result, err := searcher.Search(t.Context(), SearchQuery{Text: "ephemeral"})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
}

func TestNewProvider_ExplicitMemoryBackendOverridesStorageBackend(t *testing.T) {
	provider, err := NewProvider("default-memory", Options{
		StorageBackend: "sqlite",
		MemoryBackend:  " memory ",
		StateManager:   newMemoryTestManager(t, storagememory.NewStore()),
	})

	require.NoError(t, err)
	require.IsType(t, &MemoryProvider{}, provider)
}

func TestNewProvider_DefaultMemoryRequiresStore(t *testing.T) {
	provider, err := NewProvider("default-memory", Options{StorageBackend: "sqlite"})
	require.EqualError(t, err, "state manager is required")
	require.Nil(t, provider)
}

func TestNewProvider_ReturnsUnknownMemoryBackendError(t *testing.T) {
	provider, err := NewProvider("default-memory", Options{StorageBackend: "other"})
	require.ErrorIs(t, err, ErrUnknownBackend)
	require.Nil(t, provider)
}

func TestNewProvider_ReturnsUnknownProviderError(t *testing.T) {
	provider, err := NewProvider("other", Options{})
	require.ErrorIs(t, err, ErrUnknownProvider)
	require.Nil(t, provider)
}

func TestNewProvider_ReturnsUnknownProviderForNoop(t *testing.T) {
	provider, err := NewProvider("noop", Options{StateManager: newMemoryTestManager(t, storagememory.NewStore())})
	require.ErrorIs(t, err, ErrUnknownProvider)
	require.Nil(t, provider)
}

func TestNewProvider_ReturnsUnknownProviderForMemoryAlias(t *testing.T) {
	provider, err := NewProvider("memory", Options{StateManager: newMemoryTestManager(t, storagememory.NewStore())})
	require.ErrorIs(t, err, ErrUnknownProvider)
	require.Nil(t, provider)
}

func TestDefaultMemoryProvider_NewProviderFromManagerValidation(t *testing.T) {
	provider, err := NewFromManager(nil, Options{})

	require.Nil(t, provider)
	require.EqualError(t, err, "state manager is required")

	provider, err = NewFromManager(newMemoryTestManager(t, storagememory.NewStore()), Options{
		ModelClient: episodicModelClientStub(),
	})
	require.Nil(t, provider)
	require.EqualError(t, err, "memory episode extractor model is required")
}

func TestDefaultMemoryProvider_NewFromManagerKeepsConfiguredReflectionGenerator(t *testing.T) {
	generator := &fakeReflectionGenerator{}
	provider, err := NewFromManager(newMemoryTestManager(t, storagememory.NewStore()), Options{
		ModelClient:          episodicModelClientStub(),
		Model:                "test-model",
		ReflectionGenerator:  generator,
		ReflectionBackground: ReflectionBackgroundOptions{Limit: maxReflectionLimit + 1},
		PromotionBackground:  PromotionBackgroundOptions{Limit: maxPromotionBackgroundLimit + 1},
	})

	require.NoError(t, err)
	require.NotNil(t, provider.episodicExtractor)
	require.Same(t, generator, provider.reflectionGenerator)
	require.Equal(t, maxReflectionLimit, provider.reflectionBackground.Limit)
	require.Equal(t, maxPromotionBackgroundLimit, provider.promotionBackground.Limit)
	require.Equal(t, defaultPromotionEvaluatedRetention, provider.promotionBackground.EvaluatedRetention)
}

func TestMemoryProvider_CapabilitiesConfigureObservabilityAndClose(t *testing.T) {
	provider := defaultMemoryTestProvider(t, Options{})

	caps, err := provider.Capabilities(context.Background())
	require.NoError(t, err)
	require.True(t, caps.SupportsPinned)
	require.True(t, caps.SupportsSearch)
	require.True(t, caps.SupportsWrite)
	require.True(t, caps.SupportsDelete)
	require.True(t, caps.SupportsEpisodeRecording)
	require.True(t, caps.SupportsSemanticRecording)
	require.False(t, caps.SupportsVectors)
	require.True(t, caps.SupportsReranking)
	require.True(t, caps.SupportsObservability)

	tracer := &fakeTracer{}
	require.NoError(t, provider.ConfigureObservability(fakeObservability{tracer: tracer}))
	_, err = provider.Search(context.Background(), SearchQuery{})
	require.NoError(t, err)
	require.Contains(t, tracer.events, "memory.search.started")
	require.Contains(t, tracer.events, "memory.search.completed")
	require.NoError(t, provider.Close())
}

func TestMemoryProvider_CapabilitiesReportsVectorSupport(t *testing.T) {
	provider := &MemoryProvider{manager: vectorCapabilityMemoryManager{enabled: true}}

	caps, err := provider.Capabilities(context.Background())

	require.NoError(t, err)
	require.True(t, caps.SupportsVectors)

	var missing *MemoryProvider
	caps, err = missing.Capabilities(context.Background())
	require.NoError(t, err)
	require.False(t, caps.SupportsVectors)
}

func TestMemoryProvider_RecordEpisodeStoresEpisodicMemory(t *testing.T) {
	provider := defaultMemoryTestProvider(t, Options{})

	item, err := provider.RecordEpisode(context.Background(), EpisodeRecord{Item: MemoryItem{
		ID:     "mem_episode_test",
		Status: StatusCandidate,
		Text:   "User chose pnpm for installs.",
	}})

	require.NoError(t, err)
	require.Equal(t, KindEpisodic, item.Kind)
	require.Equal(t, StatusCandidate, item.Status)

	result, err := provider.Search(context.Background(), SearchQuery{
		Kinds:    []Kind{KindEpisodic},
		Statuses: []Status{StatusCandidate},
		Limit:    1,
	})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	require.Equal(t, "mem_episode_test", result.Hits[0].Item.ID)

	item, err = provider.RecordEpisode(context.Background(), EpisodeRecord{Item: MemoryItem{
		ID:   "mem_episode_default_status",
		Text: "Use focused tests.",
	}})
	require.NoError(t, err)
	require.Equal(t, KindEpisodic, item.Kind)
	require.Equal(t, StatusActive, item.Status)
}

func TestMemoryProvider_ExtractEpisodes(t *testing.T) {
	ctx := context.Background()
	store := storagememory.NewStore()
	manager := newMemoryTestManager(t, store)
	provider, err := NewFromManager(manager, Options{
		ModelClient: episodicModelClientStub(),
		Model:       "test-model",
	})
	require.NoError(t, err)

	require.NoError(t, manager.Save(ctx, statecore.Session{ID: statecore.DefaultSessionID}))
	require.NoError(t, manager.AppendMessages(ctx, statecore.DefaultSessionID, []morphmsg.Message{{
		Role:    morphmsg.RoleUser,
		Content: "Remember provider-owned extraction.",
	}}))

	result, err := provider.ExtractEpisodes(ctx, ExtractionRequest{
		SessionID:      statecore.DefaultSessionID,
		WindowSize:     1,
		MaxWindowChars: 1000,
	})

	require.NoError(t, err)
	require.Equal(t, 1, result.WriteCount)
	require.Equal(t, 1, result.MessageCount)
}

func TestMemoryProvider_ExtractEpisodesPassesMaxOutputTokensOption(t *testing.T) {
	ctx := context.Background()
	store := storagememory.NewStore()
	manager := newMemoryTestManager(t, store)
	client := episodicModelClientStub()
	maxOutputTokens := false
	provider, err := NewFromManager(manager, Options{
		ModelClient:            client,
		Model:                  "test-model",
		MaxOutputTokensEnabled: &maxOutputTokens,
	})
	require.NoError(t, err)

	require.NoError(t, manager.Save(ctx, statecore.Session{ID: statecore.DefaultSessionID}))
	require.NoError(t, manager.AppendMessages(ctx, statecore.DefaultSessionID, []morphmsg.Message{{
		Role:    morphmsg.RoleUser,
		Content: "Remember provider-owned extraction.",
	}}))

	_, err = provider.ExtractEpisodes(ctx, ExtractionRequest{
		SessionID:      statecore.DefaultSessionID,
		WindowSize:     1,
		MaxWindowChars: 1000,
	})

	require.NoError(t, err)
	require.Len(t, client.requests, 1)
	require.NotNil(t, client.requests[0].StructuredOutput)
	require.Zero(t, client.requests[0].MaxOutputTokens)

	client.response = &models.Response{OutputText: `{
		"candidates": [{
			"kind": "semantic",
			"title": "Structured output option",
			"text": "Memory provider passes the structured output option to reflection.",
			"tags": ["memory"],
			"confidence": 0.8,
			"procedural": {
				"trigger": "",
				"steps": [],
				"constraints": [],
				"examples": [],
				"expected_behavior": ""
			},
			"metadata": [{"key": "memory_importance", "value": "medium"}]
		}]
	}`}
	reflection, err := provider.reflectionGenerator.GenerateReflectionCandidates(ctx, ReflectionGenerationRequest{
		SessionID: statecore.DefaultSessionID,
		Sources: []MemoryItem{{
			ID:   "mem_episode_structured_output",
			Kind: KindEpisodic,
			Text: "Memory provider passes structured output options to model-backed features.",
		}},
		Limit: 1,
	})
	require.NoError(t, err)
	require.Len(t, reflection.Items, 1)
	require.Len(t, client.requests, 2)
	require.NotNil(t, client.requests[1].StructuredOutput)
	require.Zero(t, client.requests[1].MaxOutputTokens)
}

func TestMemoryProvider_ExtractEpisodesRequiresExtractor(t *testing.T) {
	var provider *MemoryProvider

	result, err := provider.ExtractEpisodes(context.Background(), ExtractionRequest{})
	require.EqualError(t, err, "memory extraction is not configured")
	require.Empty(t, result)
}

func TestMemoryProvider_StartBackground(t *testing.T) {
	provider := defaultMemoryTestProvider(t, Options{})
	require.NoError(t, provider.StartBackground(context.Background()))
	var nilContext context.Context
	require.NoError(t, provider.StartBackground(nilContext))
	require.Len(t, provider.backgroundStarters(), 3)

	var missing *MemoryProvider
	require.EqualError(t, missing.StartBackground(context.Background()), "memory provider is required")

	provider = &MemoryProvider{episodicBackground: EpisodicBackgroundOptions{Enabled: true}}
	require.EqualError(t, provider.StartBackground(context.Background()), "memory extraction is not configured")

	provider = &MemoryProvider{reflectionBackground: ReflectionBackgroundOptions{Enabled: true}}
	require.EqualError(t, provider.StartBackground(context.Background()), "memory reflection is not configured")
}

func TestMemoryProvider_StartBackgroundRunsEpisodicLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := storagememory.NewStore()
	manager := newMemoryTestManager(t, store)
	tracer := &fakeTracer{}
	provider, err := NewFromManager(manager, Options{
		Observability: fakeObservability{tracer: tracer},
		ModelClient:   episodicModelClientStub(),
		Model:         "test-model",
		EpisodicBackground: EpisodicBackgroundOptions{
			Enabled:     true,
			Interval:    time.Nanosecond,
			IdleAfter:   time.Nanosecond,
			MinMessages: 1,
			WindowSize:  1,
			MaxWindows:  1,
		},
	})
	require.NoError(t, err)
	require.NoError(t, manager.Save(ctx, statecore.Session{ID: statecore.DefaultSessionID}))
	require.NoError(t, manager.AppendMessages(ctx, statecore.DefaultSessionID, []morphmsg.Message{{
		Role:    morphmsg.RoleUser,
		Content: "background loop",
	}}))

	require.NoError(t, provider.StartBackground(ctx))
	require.Eventually(t, func() bool {
		if slices.Contains(tracer.events, "memory.episodic_background.completed") {
			cancel()
			return true
		}

		return false
	}, time.Second, time.Millisecond)

	doneCtx, doneCancel := context.WithCancel(context.Background())
	doneCancel()
	provider.runEpisodicRecordingBackgroundLoop(doneCtx, EpisodicBackgroundOptions{Interval: time.Nanosecond})
}

func TestMemoryProvider_StartBackgroundRunsPromotionLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	provider := defaultMemoryTestProvider(t, Options{
		PromotionBackground: PromotionBackgroundOptions{
			Enabled:  true,
			Interval: time.Nanosecond,
			Limit:    2,
		},
	})
	_, err := provider.Upsert(ctx, lifecycleCandidate("mem_candidate", KindSemantic, "Use focused tests."))
	require.NoError(t, err)

	require.NoError(t, provider.StartBackground(ctx))
	require.Eventually(t, func() bool {
		result, err := provider.Search(context.Background(), SearchQuery{
			IDs: []string{"mem_candidate"},
		})
		if err != nil || len(result.Hits) != 1 {
			return false
		}
		if result.Hits[0].Item.Status == StatusActive {
			cancel()
			return true
		}
		return false
	}, time.Second, time.Millisecond)

	doneCtx, doneCancel := context.WithCancel(context.Background())
	doneCancel()
	provider.runPromotionBackgroundLoop(doneCtx, PromotionBackgroundOptions{Interval: time.Nanosecond})
}

func TestMemoryProvider_StartBackgroundRunsReflectionLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := storagememory.NewStore()
	manager := newMemoryTestManager(t, store)
	require.NoError(t, manager.Save(ctx, statecore.Session{ID: statecore.DefaultSessionID}))

	generator := &fakeReflectionGenerator{result: ReflectionGenerationResult{Items: []MemoryItem{{
		Kind:       KindSemantic,
		Title:      "Background reflection",
		Text:       "The user prefers background reflection to run independently.",
		Confidence: 0.9,
		Metadata: map[string]string{
			"memory_importance":  "high",
			"memory_granularity": "summary",
		},
	}}}}
	provider, err := NewFromManager(manager, Options{
		ReflectionGenerator: generator,
		ReflectionBackground: ReflectionBackgroundOptions{
			Enabled:      true,
			Interval:     time.Nanosecond,
			Limit:        4,
			RelatedLimit: 2,
		},
	})
	require.NoError(t, err)

	_, err = provider.Upsert(ctx, MemoryItem{
		ID:     "mem_episode_reflect_loop",
		Kind:   KindEpisodic,
		Status: StatusActive,
		Text:   "Reflection should process this episodic memory.",
		SourceLinks: []SourceLink{{
			SessionID: statecore.DefaultSessionID,
			Offsets:   []int{1},
		}},
	})
	require.NoError(t, err)

	require.NoError(t, provider.StartBackground(ctx))
	require.Eventually(t, func() bool {
		if len(generator.requests) == 0 {
			return false
		}

		result, err := provider.Search(context.Background(), SearchQuery{
			Tags:     []string{"reflection-source-mem_episode_reflect_loop"},
			Statuses: []Status{StatusCandidate},
		})
		if err != nil || len(result.Hits) != 1 {
			return false
		}
		cancel()
		return true
	}, time.Second, time.Millisecond)
	require.Equal(t, 4, generator.requests[0].Limit)

	doneCtx, doneCancel := context.WithCancel(context.Background())
	doneCancel()
	provider.runReflectionBackgroundLoop(doneCtx, ReflectionBackgroundOptions{Interval: time.Nanosecond})
}

func episodicModelClientStub() *memoryModelClientStub {
	return &memoryModelClientStub{
		response: &models.Response{OutputText: `{
			"candidates": [{
				"kind": "outcome",
				"title": "Provider extraction",
				"text": "Provider-owned extraction captured the completed memory event.",
				"confidence": 0.8,
				"metadata": {"outcome_status": "success"}
			}],
			"rejections": []
		}`},
	}
}

type memoryModelClientStub struct {
	requests []models.Request
	response *models.Response
	err      error
}

func (s *memoryModelClientStub) Complete(_ context.Context, req models.Request) (*models.Response, error) {
	s.requests = append(s.requests, req)
	if s.err != nil {
		return nil, s.err
	}
	return s.response, nil
}

func (s *memoryModelClientStub) CompleteStream(
	context.Context,
	models.Request,
	func(models.StreamDelta),
) (*models.Response, error) {
	return nil, errors.New("streaming is not supported")
}

func TestBackgroundContext(t *testing.T) {
	var nilContext context.Context
	require.NotNil(t, getBackgroundContext(nilContext))

	type contextKey string
	ctx := context.WithValue(context.Background(), contextKey("key"), "value")
	require.Same(t, ctx, getBackgroundContext(ctx))
}

func TestProviderTraceRecorder_RecordForwardsPayload(t *testing.T) {
	tracer := &fakeTracer{}
	recorder := providerTraceRecorder{
		ctx: context.Background(),
		obs: fakeObservability{tracer: tracer},
	}

	recorder.Record("memory.custom", "payload")

	require.Equal(t, []string{"memory.custom"}, tracer.events)
	require.Equal(t, "payload", tracer.payloads[0])
}

func TestDefaultMemoryProvider_SearchWriteDeleteAndObservability(t *testing.T) {
	guardrails := &fakeGuardrails{redactText: "redacted"}
	logger := &fakeLogger{}
	tracer := &fakeTracer{}
	provider := defaultMemoryTestProvider(t, Options{
		Guardrails:    guardrails,
		Observability: fakeObservability{logger: logger, tracer: tracer},
	})

	item, err := provider.Upsert(context.Background(), MemoryItem{
		Kind:   KindSemantic,
		Status: StatusActive,
		Title:  "Go preference",
		Text:   "Use focused tests",
		Tags:   []string{"go"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, item.ID)
	require.Equal(t, 1, guardrails.validateWriteCalls)
	require.Equal(t, 1, guardrails.safetyScanCalls)
	require.Contains(t, tracer.events, "memory.upsert.completed")

	result, err := provider.Search(context.Background(), SearchQuery{
		Text:     "focused",
		Kinds:    []Kind{KindSemantic},
		Statuses: []Status{StatusActive},
		Tags:     []string{"go"},
		Limit:    5,
	})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	require.Equal(t, "redacted", result.Hits[0].Item.Text)
	require.Greater(t, result.Hits[0].Score, 0.0)
	require.Equal(t, 1, guardrails.redactCalls)
	require.NotEmpty(t, logger.debug)
	require.Contains(t, tracer.events, "memory.search.started")
	require.Contains(t, tracer.events, "memory.search.completed")
	require.Equal(
		t,
		map[string]any{"provider": ProviderDefaultMemory, "operation": "upsert", "memory_id": item.ID},
		logger.debug[0],
	)
	require.Equal(t, logger.debug[0], tracer.fields[0])
	require.Equal(
		t,
		map[string]any{
			"provider":     ProviderDefaultMemory,
			"operation":    "search",
			"query_chars":  7,
			"kind_count":   1,
			"status_count": 1,
			"limit":        5,
			"max_chars":    0,
		},
		logger.debug[1],
	)
	require.Equal(t, logger.debug[1], tracer.fields[1])
	require.Equal(
		t,
		map[string]any{"provider": ProviderDefaultMemory, "operation": "search", "result_count": 1},
		logger.debug[2],
	)
	require.Equal(t, logger.debug[2], tracer.fields[2])

	require.NoError(t, provider.Delete(context.Background(), DeleteRequest{ID: item.ID}))
	require.EqualError(t, provider.Delete(context.Background(), DeleteRequest{ID: "missing"}), "memory item not found")

	result, err = provider.Search(context.Background(), SearchQuery{Text: "focused"})
	require.NoError(t, err)
	require.Empty(t, result.Hits)

	result, err = provider.Search(context.Background(), SearchQuery{Text: "focused", Statuses: []Status{StatusDeleted}})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	require.Equal(t, StatusDeleted, result.Hits[0].Item.Status)
	require.Equal(t, "delete", result.Hits[0].Item.Metadata[lifecycleMetadataAction])
	require.Equal(t, string(StatusActive), result.Hits[0].Item.Metadata[lifecycleMetadataPreviousStatus])
}

func TestDefaultMemoryProvider_SearchFiltersStatusesTagsKindsAndText(t *testing.T) {
	provider := defaultMemoryTestProvider(t, Options{})

	_, err := provider.Upsert(context.Background(), MemoryItem{
		ID:     "mem_active",
		Kind:   KindSemantic,
		Status: StatusActive,
		Title:  "Alpha title",
		Text:   "body",
		Tags:   []string{"Go", "Style"},
	})
	require.NoError(t, err)
	_, err = provider.Upsert(context.Background(), MemoryItem{
		ID:     "mem_candidate",
		Kind:   KindProcedural,
		Status: StatusCandidate,
		Text:   "alpha body",
		Tags:   []string{"go"},
	})
	require.NoError(t, err)
	_, err = provider.Upsert(context.Background(), MemoryItem{
		ID:     "mem_superseded",
		Kind:   KindSemantic,
		Status: StatusSuperseded,
		Text:   "alpha body",
		Tags:   []string{"go"},
	})
	require.NoError(t, err)

	result, err := provider.Search(context.Background(), SearchQuery{Text: "alpha", Tags: []string{"go"}})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	require.Equal(t, "mem_active", result.Hits[0].Item.ID)

	result, err = provider.Search(context.Background(), SearchQuery{
		Text:     "alpha",
		Kinds:    []Kind{KindSemantic},
		Tags:     []string{"go", "style"},
		Statuses: []Status{StatusActive},
	})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	require.Equal(t, "mem_active", result.Hits[0].Item.ID)

	result, err = provider.Search(context.Background(), SearchQuery{
		Text:     "alpha",
		Statuses: []Status{StatusCandidate},
	})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	require.Equal(t, "mem_candidate", result.Hits[0].Item.ID)

	result, err = provider.Search(context.Background(), SearchQuery{
		Text:     "alpha",
		Statuses: []Status{StatusSuperseded},
	})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	require.Equal(t, "mem_superseded", result.Hits[0].Item.ID)

	result, err = provider.Search(context.Background(), SearchQuery{Text: "alpha", Tags: []string{"missing"}})
	require.NoError(t, err)
	require.Empty(t, result.Hits)

	result, err = provider.Search(context.Background(), SearchQuery{Text: "missing"})
	require.NoError(t, err)
	require.Empty(t, result.Hits)
}

func TestDefaultMemoryProvider_SearchRanksBeforeLimiting(t *testing.T) {
	provider := defaultMemoryTestProvider(t, Options{})

	_, err := provider.Upsert(context.Background(), MemoryItem{
		ID:     "mem_text_only",
		Kind:   KindSemantic,
		Status: StatusActive,
		Title:  "Other",
		Text:   "alpha in body",
	})
	require.NoError(t, err)
	_, err = provider.Upsert(context.Background(), MemoryItem{
		ID:     "mem_title_and_text",
		Kind:   KindSemantic,
		Status: StatusActive,
		Title:  "alpha title",
		Text:   "alpha in body",
	})
	require.NoError(t, err)

	result, err := provider.Search(context.Background(), SearchQuery{Text: "alpha", Limit: 1})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	require.Equal(t, "mem_title_and_text", result.Hits[0].Item.ID)
	require.Greater(t, result.Hits[0].Score, 0.0)
}

func TestDefaultMemoryProvider_SourceLinksRoundTripAndCloneIsolation(t *testing.T) {
	provider := defaultMemoryTestProvider(t, Options{})
	item := MemoryItem{
		ID:       "  mem_clone  ",
		Tags:     []string{"one"},
		Metadata: map[string]string{"key": "value"},
		SourceLinks: []SourceLink{{
			SessionID:     "session",
			MessageIDs:    []uint{1},
			Offsets:       []int{2},
			SummaryID:     "summary",
			CreatedBy:     "reflection",
			CreatedReason: "preference",
		}},
	}

	stored, err := provider.Upsert(context.Background(), item)
	require.NoError(t, err)
	require.Equal(t, "mem_clone", stored.ID)
	require.Equal(t, StatusCandidate, stored.Status)
	require.False(t, stored.CreatedAt.IsZero())
	require.False(t, stored.UpdatedAt.IsZero())

	item.Tags[0] = "changed"
	item.Metadata["key"] = "changed"
	item.SourceLinks[0].MessageIDs[0] = 99
	item.SourceLinks[0].Offsets[0] = 99
	stored.Tags[0] = "changed"
	stored.Metadata["key"] = "changed"
	stored.SourceLinks[0].MessageIDs[0] = 99

	result, err := provider.Search(context.Background(), SearchQuery{})
	require.NoError(t, err)
	require.Empty(t, result.Hits)

	result, err = provider.Search(context.Background(), SearchQuery{Statuses: []Status{StatusCandidate}})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	require.Equal(t, []string{"one"}, result.Hits[0].Item.Tags)
	require.Equal(t, map[string]string{"key": "value"}, result.Hits[0].Item.Metadata)
	require.Equal(t, []uint{1}, result.Hits[0].Item.SourceLinks[0].MessageIDs)
	require.Equal(t, []int{2}, result.Hits[0].Item.SourceLinks[0].Offsets)
	require.Equal(t, "summary", result.Hits[0].Item.SourceLinks[0].SummaryID)
	require.Equal(t, "reflection", result.Hits[0].Item.SourceLinks[0].CreatedBy)
	require.Equal(t, "preference", result.Hits[0].Item.SourceLinks[0].CreatedReason)
}

func TestDefaultMemoryProvider_UpsertReplacesTagsAndUpdatesExistingRecord(t *testing.T) {
	provider := defaultMemoryTestProvider(t, Options{})
	createdAt := time.Date(2026, 4, 30, 9, 0, 0, 0, time.UTC)

	stored, err := provider.Upsert(context.Background(), MemoryItem{
		ID:        "mem_update",
		Status:    StatusActive,
		Text:      "Use npm",
		Tags:      []string{"node"},
		CreatedAt: createdAt,
	})
	require.NoError(t, err)
	require.Equal(t, createdAt, stored.CreatedAt)

	updated, err := provider.Upsert(context.Background(), MemoryItem{
		ID:     "mem_update",
		Status: StatusActive,
		Text:   "Use pnpm",
		Tags:   []string{"package"},
	})
	require.NoError(t, err)
	require.Equal(t, createdAt, updated.CreatedAt)
	require.True(t, updated.UpdatedAt.After(updated.CreatedAt))

	result, err := provider.Search(context.Background(), SearchQuery{Text: "pnpm", Tags: []string{"package"}})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	require.Equal(t, createdAt, result.Hits[0].Item.CreatedAt)
	require.Equal(t, updated.UpdatedAt, result.Hits[0].Item.UpdatedAt)

	result, err = provider.Search(context.Background(), SearchQuery{Text: "pnpm", Tags: []string{"node"}})
	require.NoError(t, err)
	require.Empty(t, result.Hits)
}

func TestMemoryProvider_ReturnsProviderRequiredErrors(t *testing.T) {
	var provider *MemoryProvider

	_, err := provider.LoadPinned(context.Background(), SearchQuery{})
	require.EqualError(t, err, "memory provider is required")

	_, err = provider.Search(context.Background(), SearchQuery{})
	require.EqualError(t, err, "memory provider is required")

	_, err = provider.Upsert(context.Background(), MemoryItem{Text: "hello"})
	require.EqualError(t, err, "memory provider is required")

	_, err = provider.RecordSemanticMemory(context.Background(), SemanticRecord{Item: MemoryItem{Text: "hello"}})
	require.EqualError(t, err, "memory provider is required")

	err = provider.Delete(context.Background(), DeleteRequest{ID: "mem_123"})
	require.EqualError(t, err, "memory provider is required")
}

func TestMemoryProvider_PropagatesManagerErrors(t *testing.T) {
	managerErr := errors.New("manager failed")

	provider := &MemoryProvider{manager: fakeMemoryManager{searchErr: managerErr}}
	_, err := provider.LoadPinned(context.Background(), SearchQuery{})
	require.ErrorIs(t, err, managerErr)

	_, err = provider.Search(context.Background(), SearchQuery{})
	require.ErrorIs(t, err, managerErr)

	provider = &MemoryProvider{manager: fakeMemoryManager{upsertErr: managerErr}}
	_, err = provider.Upsert(context.Background(), MemoryItem{Text: "hello"})
	require.ErrorIs(t, err, managerErr)
	_, err = provider.RecordSemanticMemory(context.Background(), SemanticRecord{Item: MemoryItem{
		Kind: KindSemantic,
		Text: "hello",
		Metadata: map[string]string{
			"source_session_id": "default",
		},
	}})
	require.ErrorIs(t, err, managerErr)

	provider = &MemoryProvider{manager: fakeMemoryManager{searchErr: managerErr}}
	err = provider.Delete(context.Background(), DeleteRequest{ID: "mem_123"})
	require.ErrorIs(t, err, managerErr)

	provider = &MemoryProvider{manager: fakeMemoryManager{
		searchResult: SearchResult{Hits: []SearchHit{{Item: MemoryItem{ID: "mem_123", Status: StatusActive}}}},
		upsertErr:    managerErr,
	}}
	err = provider.Delete(context.Background(), DeleteRequest{ID: "mem_123"})
	require.ErrorIs(t, err, managerErr)
}

func TestMemoryProvider_SearchTruncatesResultText(t *testing.T) {
	provider := &MemoryProvider{
		manager: fakeMemoryManager{
			searchResult: SearchResult{Hits: []SearchHit{{
				Item:  MemoryItem{ID: "mem_123", Text: "abcdef"},
				Score: 1,
			}}},
		},
	}

	result, err := provider.Search(context.Background(), SearchQuery{MaxChars: 4})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	require.Equal(t, "abcd", result.Hits[0].Item.Text)
}

func TestMemoryProvider_LoadPinned(t *testing.T) {
	dir := t.TempDir()
	setProfileHome(t, dir)
	file := filepath.Join(dir, "memory.md")
	require.NoError(t, os.WriteFile(file, []byte("from file"), 0o600))

	provider := defaultMemoryTestProvider(t, Options{})

	_, err := provider.Upsert(context.Background(), MemoryItem{Kind: KindPinned, Status: StatusActive, Text: "from db"})
	require.NoError(t, err)
	_, err = provider.Upsert(context.Background(), MemoryItem{Kind: KindSemantic, Status: StatusActive, Text: "semantic remember"})
	require.NoError(t, err)

	items, err := provider.LoadPinned(context.Background(), SearchQuery{Text: "remember"})
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, KindPinned, items[0].Kind)
	require.Equal(t, "memory.md", items[0].Title)
	require.Equal(t, "from file", items[0].Text)
	require.Equal(t, map[string]string{"source": "file", "path": file}, items[0].Metadata)
	require.Equal(t, KindPinned, items[1].Kind)
	require.Equal(t, "from db", items[1].Text)
}

func TestMemoryProvider_LoadPinnedDisabled(t *testing.T) {
	enabled := false
	provider := defaultMemoryTestProvider(t, Options{
		Guardrails: &fakeGuardrails{searchErr: errors.New("search blocked"), safetyErr: errors.New("unsafe")},
		Pinned: PinnedOptions{
			Enabled: &enabled,
		},
	})

	items, err := provider.LoadPinned(context.Background(), SearchQuery{})
	require.NoError(t, err)
	require.Empty(t, items)
}

func TestMemoryProvider_LoadPinnedReturnsFileLoadError(t *testing.T) {
	dir := t.TempDir()
	setProfileHome(t, dir)
	require.NoError(t, os.Symlink(filepath.Join(dir, "missing.md"), filepath.Join(dir, "memory.md")))

	provider := defaultMemoryTestProvider(t, Options{})

	items, err := provider.LoadPinned(context.Background(), SearchQuery{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to read pinned memory file")
	require.Empty(t, items)
}

func TestMemoryProvider_LoadPinnedAppliesItemAndTotalCharLimits(t *testing.T) {
	dir := t.TempDir()
	setProfileHome(t, dir)
	file := filepath.Join(dir, "memory.md")
	require.NoError(t, os.WriteFile(file, []byte("abcdef"), 0o600))

	provider := defaultMemoryTestProvider(t, Options{
		Pinned: PinnedOptions{
			MaxChars:     5,
			MaxItemChars: 4,
		},
	})

	items, err := provider.LoadPinned(context.Background(), SearchQuery{})
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "memo", items[0].Title)
	require.Empty(t, items[0].Text)
}

func TestMemoryProvider_LoadPinnedAppliesQueryLimitAfterMerge(t *testing.T) {
	dir := t.TempDir()
	setProfileHome(t, dir)
	file := filepath.Join(dir, "memory.md")
	require.NoError(t, os.WriteFile(file, []byte("from file"), 0o600))

	provider := defaultMemoryTestProvider(t, Options{})
	_, err := provider.Upsert(context.Background(), MemoryItem{Kind: KindPinned, Status: StatusActive, Text: "from db"})
	require.NoError(t, err)

	items, err := provider.LoadPinned(context.Background(), SearchQuery{Limit: 1})
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "from file", items[0].Text)
}

func TestMemoryProvider_LoadPinnedUsesQueryCharLimitWhenSmaller(t *testing.T) {
	dir := t.TempDir()
	setProfileHome(t, dir)
	file := filepath.Join(dir, "memory.md")
	require.NoError(t, os.WriteFile(file, []byte("abcdef"), 0o600))

	provider := defaultMemoryTestProvider(t, Options{
		Pinned: PinnedOptions{
			MaxChars:     100,
			MaxItemChars: 100,
		},
	})

	items, err := provider.LoadPinned(context.Background(), SearchQuery{MaxChars: 3})
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "mem", items[0].Title)
	require.Empty(t, items[0].Text)
}

func TestMemoryProvider_LoadPinnedSafetyScansAndRedacts(t *testing.T) {
	dir := t.TempDir()
	setProfileHome(t, dir)
	file := filepath.Join(dir, "memory.md")
	require.NoError(t, os.WriteFile(file, []byte("secret text"), 0o600))
	guardrails := &fakeGuardrails{redactText: "redacted"}

	provider := defaultMemoryTestProvider(t, Options{
		Guardrails: guardrails,
	})

	items, err := provider.LoadPinned(context.Background(), SearchQuery{})
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "redacted", items[0].Text)
	require.Equal(t, 1, guardrails.validateSearchCalls)
	require.Equal(t, 1, guardrails.safetyScanCalls)
	require.Equal(t, 1, guardrails.redactCalls)
}

func TestMemoryProvider_LoadPinnedReturnsSafetyScanError(t *testing.T) {
	dir := t.TempDir()
	setProfileHome(t, dir)
	file := filepath.Join(dir, "memory.md")
	require.NoError(t, os.WriteFile(file, []byte("unsafe"), 0o600))
	safetyErr := errors.New("unsafe pinned memory")

	provider := defaultMemoryTestProvider(t, Options{
		Guardrails: &fakeGuardrails{safetyErr: safetyErr},
	})

	items, err := provider.LoadPinned(context.Background(), SearchQuery{})
	require.ErrorIs(t, err, safetyErr)
	require.Empty(t, items)
}

func defaultMemoryTestProvider(t *testing.T, opts Options) *MemoryProvider {
	t.Helper()

	provider, err := NewFromManager(newMemoryTestManager(t, storagememory.NewStore()), opts)
	require.NoError(t, err)

	return provider
}

func setProfileHome(t *testing.T, home string) {
	t.Helper()

	original := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(original)
	})
	profile.SetActive(profile.Profile{Name: "test", HomeDir: home})
}

func newMemoryTestManager(t *testing.T, store *storagememory.Store) *statemanager.Manager {
	t.Helper()

	manager, err := statemanager.NewManager(store, time.Hour, 24*time.Hour)
	require.NoError(t, err)

	return manager
}
