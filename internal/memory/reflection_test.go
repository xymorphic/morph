package memory

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	models "github.com/xymorphic/morph/internal/model"
	statecore "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/internal/trace"
)

func TestMemoryProvider_ReflectStoresGeneratedCandidates(t *testing.T) {
	generator := &fakeReflectionGenerator{result: ReflectionGenerationResult{Items: []MemoryItem{{
		Kind:       KindSemantic,
		Title:      "Commit message preference",
		Text:       "The user prefers concise commit messages.",
		Confidence: 0.91,
		Metadata: map[string]string{
			"memory_importance":  "high",
			"memory_granularity": "summary",
		},
	}}}}
	guardrails := &fakeGuardrails{}
	tracer := &fakeTracer{}
	provider := defaultMemoryTestProvider(t, Options{
		Guardrails:          guardrails,
		Observability:       fakeObservability{tracer: tracer},
		ReflectionGenerator: generator,
	})

	_, err := provider.Upsert(context.Background(), MemoryItem{
		ID:     "mem_episode_commit",
		Kind:   KindEpisodic,
		Status: StatusActive,
		Title:  "commit preference",
		Text:   "The user corrected commit message guidance.",
		SourceLinks: []SourceLink{{
			SessionID:  statecore.DefaultSessionID,
			MessageIDs: []uint{1},
			Offsets:    []int{0},
		}},
	})
	require.NoError(t, err)
	_, err = provider.Upsert(context.Background(), MemoryItem{
		ID:     "mem_related_commit",
		Kind:   KindSemantic,
		Status: StatusActive,
		Text:   "Existing commit preference memory.",
		Metadata: map[string]string{
			"source_session_id": statecore.DefaultSessionID,
		},
	})
	require.NoError(t, err)
	validateBefore := guardrails.validateWriteCalls
	safetyBefore := guardrails.safetyScanCalls

	result, err := provider.Reflect(context.Background(), ReflectionRequest{
		SessionID:    statecore.DefaultSessionID,
		Limit:        1,
		RelatedLimit: 1,
	})

	require.NoError(t, err)
	require.Equal(t, 1, result.SourceCount)
	require.Equal(t, 1, result.RelatedCount)
	require.Equal(t, 1, result.WriteCount)
	require.Len(t, result.Items, 1)
	require.Equal(t, KindSemantic, result.Items[0].Kind)
	require.Equal(t, StatusCandidate, result.Items[0].Status)
	require.True(t, result.Items[0].Reflected)
	require.Contains(t, result.Items[0].Tags, "reflection")
	require.Contains(t, result.Items[0].Tags, "reflection-source-mem_episode_commit")
	require.Equal(t, "mem_episode_commit", result.Items[0].Metadata["reflection_source_memory_ids"])
	require.Equal(t, statecore.DefaultSessionID, result.Items[0].SourceLinks[0].SessionID)
	require.Equal(t, validateBefore+1, guardrails.validateWriteCalls)
	require.Equal(t, safetyBefore+1, guardrails.safetyScanCalls)
	require.Len(t, generator.requests, 1)
	require.Len(t, generator.requests[0].Sources, 1)
	require.Len(t, generator.requests[0].Related, 1)
	require.Contains(t, tracer.events, trace.EvtMemoryReflectionStarted)
	require.Contains(t, tracer.events, trace.EvtMemoryReflectionSourceLoaded)
	require.Contains(t, tracer.events, trace.EvtMemoryReflectionRelatedLoaded)
	require.Contains(t, tracer.events, trace.EvtMemoryReflectionMemoryWritten)
	require.Contains(t, tracer.events, trace.EvtMemoryReflectionCompleted)
}

func TestMemoryProvider_ReflectSkipsAlreadyReflectedSources(t *testing.T) {
	generator := &fakeReflectionGenerator{result: ReflectionGenerationResult{Items: []MemoryItem{{
		Kind:  KindProcedural,
		Title: "Testing workflow",
		Text:  "Run focused memory package tests after memory changes.",
		Metadata: map[string]string{
			"memory_importance":  "high",
			"memory_granularity": "summary",
			"procedural_trigger": "Memory behavior changes.",
			"procedural_steps":   "Run focused memory package tests.",
		},
	}}}}
	provider := defaultMemoryTestProvider(t, Options{ReflectionGenerator: generator})

	_, err := provider.Upsert(context.Background(), MemoryItem{
		ID:     "mem_episode_testing",
		Kind:   KindEpisodic,
		Status: StatusActive,
		Text:   "The user asked to keep memory tests focused.",
		SourceLinks: []SourceLink{{
			SessionID: statecore.DefaultSessionID,
			Offsets:   []int{1},
		}},
	})
	require.NoError(t, err)

	first, err := provider.Reflect(context.Background(), ReflectionRequest{SessionID: statecore.DefaultSessionID})
	require.NoError(t, err)
	require.Equal(t, 1, first.WriteCount)

	second, err := provider.Reflect(context.Background(), ReflectionRequest{SessionID: statecore.DefaultSessionID})
	require.NoError(t, err)
	require.Zero(t, second.WriteCount)
	require.Zero(t, second.SourceCount)
	require.Len(t, generator.requests, 1)

	search, err := provider.Search(context.Background(), SearchQuery{
		IDs:       []string{"mem_episode_testing"},
		Statuses:  []Status{StatusActive},
		Reflected: new(true),
	})
	require.NoError(t, err)
	require.Len(t, search.Hits, 1)
	require.True(t, search.Hits[0].Item.Reflected)
}

func TestMemoryProvider_ReflectRejectsUnsafeCandidates(t *testing.T) {
	tracer := &fakeTracer{}
	generator := &fakeReflectionGenerator{result: ReflectionGenerationResult{Items: []MemoryItem{{
		Kind:  KindSemantic,
		Title: "Temporary detail",
		Text:  "The user briefly mentioned a temporary execution detail.",
		Metadata: map[string]string{
			"memory_importance":  "low",
			"memory_granularity": "summary",
		},
	}}}}
	provider := defaultMemoryTestProvider(t, Options{
		Observability:       fakeObservability{tracer: tracer},
		ReflectionGenerator: generator,
	})

	_, err := provider.Upsert(context.Background(), MemoryItem{
		ID:     "mem_episode_temporary",
		Kind:   KindEpisodic,
		Status: StatusCandidate,
		Text:   "A temporary detail appeared in conversation.",
		SourceLinks: []SourceLink{{
			SessionID: statecore.DefaultSessionID,
			Offsets:   []int{2},
		}},
	})
	require.NoError(t, err)

	result, err := provider.Reflect(context.Background(), ReflectionRequest{SessionID: statecore.DefaultSessionID})

	require.NoError(t, err)
	require.Zero(t, result.WriteCount)
	require.Empty(t, result.Items)
	require.Contains(t, tracer.events, trace.EvtMemoryReflectionCandidateRejected)
	require.Contains(t, tracer.events, trace.EvtMemoryReflectionCompleted)
}

func TestMemoryProvider_ReflectRejectsDuplicateCandidateFromStore(t *testing.T) {
	tracer := &fakeTracer{}
	generator := &fakeReflectionGenerator{result: ReflectionGenerationResult{Items: []MemoryItem{{
		Kind:       KindSemantic,
		Title:      "Commit message preference",
		Text:       "The user prefers concise commit messages.",
		Confidence: 0.95,
		Metadata: map[string]string{
			"memory_importance":  "high",
			"memory_granularity": "summary",
		},
	}}}}
	provider := defaultMemoryTestProvider(t, Options{
		Observability:       fakeObservability{tracer: tracer},
		ReflectionGenerator: generator,
	})

	_, err := provider.Upsert(context.Background(), MemoryItem{
		ID:     "mem_episode_duplicate",
		Kind:   KindEpisodic,
		Status: StatusActive,
		Text:   "The user corrected commit message guidance.",
		SourceLinks: []SourceLink{{
			SessionID: statecore.DefaultSessionID,
			Offsets:   []int{3},
		}},
	})
	require.NoError(t, err)
	_, err = provider.Upsert(context.Background(), MemoryItem{
		ID:        "mem_semantic_duplicate",
		Kind:      KindSemantic,
		Status:    StatusCandidate,
		Title:     "Commit message preference",
		Text:      "The user prefers concise commit messages.",
		Reflected: true,
		Metadata: map[string]string{
			"source_session_id": statecore.DefaultSessionID,
		},
	})
	require.NoError(t, err)

	result, err := provider.Reflect(context.Background(), ReflectionRequest{SessionID: statecore.DefaultSessionID})

	require.NoError(t, err)
	require.Equal(t, 1, result.SourceCount)
	require.Zero(t, result.WriteCount)
	require.Empty(t, result.Items)
	require.Contains(t, tracer.events, trace.EvtMemoryReflectionCandidateRejected)

	search, err := provider.Search(context.Background(), SearchQuery{
		Kinds:    []Kind{KindSemantic},
		Statuses: []Status{StatusCandidate},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"mem_semantic_duplicate"}, memoryHitIDs(search.Hits))

	source, err := provider.Search(context.Background(), SearchQuery{
		IDs:       []string{"mem_episode_duplicate"},
		Statuses:  []Status{StatusActive},
		Reflected: new(true),
	})
	require.NoError(t, err)
	require.Equal(t, []string{"mem_episode_duplicate"}, memoryHitIDs(source.Hits))
}

func TestMemoryProvider_ReflectRejectsDuplicateReflectionAcrossKinds(t *testing.T) {
	manager := &recordingMemoryManager{
		searchResults: []SearchResult{{Hits: []SearchHit{{
			Item: MemoryItem{
				ID:        "mem_existing_pinned",
				Kind:      KindPinned,
				Status:    StatusCandidate,
				Title:     "Commit message preference",
				Text:      "The user prefers concise commit messages.",
				Reflected: true,
			},
		}}}},
	}
	provider := &MemoryProvider{manager: manager}

	rejection, err := provider.checkReflectionCandidateRedundancy(context.Background(), MemoryItem{
		ID:     "mem_candidate_semantic",
		Kind:   KindSemantic,
		Status: StatusCandidate,
		Title:  "Commit message preference",
		Text:   "The user prefers concise commit messages.",
	}, nil)

	require.NoError(t, err)
	require.Equal(t, "duplicate_reflection_memory", rejection)
	require.Len(t, manager.searchQueries, 1)
	require.Empty(t, manager.searchQueries[0].Kinds)
	require.NotNil(t, manager.searchQueries[0].Reflected)
	require.True(t, *manager.searchQueries[0].Reflected)
}

func TestMemoryProvider_ReflectRejectsDuplicateCandidateInBatch(t *testing.T) {
	generator := &fakeReflectionGenerator{result: ReflectionGenerationResult{Items: []MemoryItem{
		reflectionCandidate(KindProcedural, "Focused test workflow"),
		reflectionCandidate(KindPinned, "Focused test workflow"),
	}}}
	provider := defaultMemoryTestProvider(t, Options{ReflectionGenerator: generator})

	_, err := provider.Upsert(context.Background(), MemoryItem{
		ID:     "mem_episode_batch_duplicate",
		Kind:   KindEpisodic,
		Status: StatusActive,
		Text:   "The user asked for focused test workflow memory.",
		SourceLinks: []SourceLink{{
			SessionID: statecore.DefaultSessionID,
			Offsets:   []int{4},
		}},
	})
	require.NoError(t, err)

	result, err := provider.Reflect(context.Background(), ReflectionRequest{SessionID: statecore.DefaultSessionID})

	require.NoError(t, err)
	require.Equal(t, 1, result.WriteCount)
	require.Len(t, result.Items, 1)
	require.Equal(t, KindProcedural, result.Items[0].Kind)
}

func TestMemoryProvider_CheckReflectionCandidateRedundancyRejectsSimilarMemory(t *testing.T) {
	manager := &recordingMemoryManager{searchResults: []SearchResult{{Hits: []SearchHit{{
		Item: MemoryItem{
			ID:        "mem_existing_similar",
			Kind:      KindPinned,
			Status:    StatusActive,
			Title:     "Commit guidance",
			Text:      "The user prefers commit messages to stay concise.",
			Reflected: true,
		},
		Score: reflectionSimilarScoreThreshold,
	}}}}}
	provider := &MemoryProvider{manager: manager}

	rejection, err := provider.checkReflectionCandidateRedundancy(context.Background(), MemoryItem{
		ID:     "mem_candidate_similar",
		Kind:   KindSemantic,
		Status: StatusCandidate,
		Title:  "Commit preference",
		Text:   "The user prefers concise commit messages.",
	}, nil)

	require.NoError(t, err)
	require.Equal(t, "similar_reflection_memory", rejection)
	require.Len(t, manager.searchQueries, 1)
	require.Empty(t, manager.searchQueries[0].Kinds)
	require.NotNil(t, manager.searchQueries[0].Reflected)
	require.True(t, *manager.searchQueries[0].Reflected)
}

func TestMemoryProvider_CheckReflectionCandidateRedundancyCoversNoopAndErrorBranches(t *testing.T) {
	t.Run("empty text skips store search", func(t *testing.T) {
		manager := &recordingMemoryManager{}
		provider := &MemoryProvider{manager: manager}

		rejection, err := provider.checkReflectionCandidateRedundancy(context.Background(), MemoryItem{}, nil)

		require.NoError(t, err)
		require.Empty(t, rejection)
		require.Empty(t, manager.searchQueries)
	})

	t.Run("store search error is returned", func(t *testing.T) {
		manager := &recordingMemoryManager{
			searchErrs: []error{errors.New("dedupe search failed")},
		}
		provider := &MemoryProvider{manager: manager}

		rejection, err := provider.checkReflectionCandidateRedundancy(context.Background(), MemoryItem{
			ID:    "mem_candidate",
			Title: "Candidate",
		}, nil)

		require.EqualError(t, err, "dedupe search failed")
		require.Empty(t, rejection)
	})

	t.Run("self and nonmatching hits are ignored", func(t *testing.T) {
		manager := &recordingMemoryManager{searchResults: []SearchResult{{Hits: []SearchHit{
			{
				Item: MemoryItem{
					ID:    "mem_candidate",
					Title: "Candidate",
					Text:  "Candidate reflection.",
				},
				Score: reflectionSimilarScoreThreshold,
			},
			{
				Item: MemoryItem{
					ID:    "mem_other",
					Title: "Different",
					Text:  "Different reflection.",
				},
				Score: reflectionSimilarScoreThreshold - 0.01,
			},
		}}}}
		provider := &MemoryProvider{manager: manager}

		rejection, err := provider.checkReflectionCandidateRedundancy(context.Background(), MemoryItem{
			ID:    "mem_candidate",
			Title: "Candidate",
			Text:  "Candidate reflection.",
		}, nil)

		require.NoError(t, err)
		require.Empty(t, rejection)
	})
}

func TestMemoryProvider_ReflectPropagatesFailureWithoutWriting(t *testing.T) {
	generator := &fakeReflectionGenerator{err: errors.New("model failed")}
	tracer := &fakeTracer{}
	provider := defaultMemoryTestProvider(t, Options{
		Observability:       fakeObservability{tracer: tracer},
		ReflectionGenerator: generator,
	})
	_, err := provider.Upsert(context.Background(), MemoryItem{
		ID:     "mem_episode_failure",
		Kind:   KindEpisodic,
		Status: StatusActive,
		Text:   "Reflection source.",
		SourceLinks: []SourceLink{{
			SessionID: statecore.DefaultSessionID,
			Offsets:   []int{3},
		}},
	})
	require.NoError(t, err)

	result, err := provider.Reflect(context.Background(), ReflectionRequest{SessionID: statecore.DefaultSessionID})

	require.EqualError(t, err, "model failed")
	require.Empty(t, result)
	search, searchErr := provider.Search(context.Background(), SearchQuery{
		Kinds:    []Kind{KindSemantic, KindProcedural, KindPinned},
		Statuses: []Status{StatusCandidate},
	})
	require.NoError(t, searchErr)
	require.Empty(t, search.Hits)
	require.Contains(t, tracer.events, trace.EvtMemoryReflectionFailed)
}

func TestMemoryProvider_ReflectKeepsPinnedCandidatesInactive(t *testing.T) {
	generator := &fakeReflectionGenerator{result: ReflectionGenerationResult{Items: []MemoryItem{{
		Kind:  KindPinned,
		Title: "Pinned preference candidate",
		Text:  "The user has a durable project preference.",
		Metadata: map[string]string{
			"memory_importance":  "high",
			"memory_granularity": "summary",
		},
	}}}}
	provider := defaultMemoryTestProvider(t, Options{ReflectionGenerator: generator})

	_, err := provider.Upsert(context.Background(), MemoryItem{
		ID:     "mem_episode_pinned",
		Kind:   KindEpisodic,
		Status: StatusActive,
		Text:   "The user gave durable project preference evidence.",
		SourceLinks: []SourceLink{{
			SessionID: statecore.DefaultSessionID,
			Offsets:   []int{4},
		}},
	})
	require.NoError(t, err)

	result, err := provider.Reflect(context.Background(), ReflectionRequest{SessionID: statecore.DefaultSessionID})

	require.NoError(t, err)
	require.Equal(t, 1, result.WriteCount)
	require.Equal(t, KindPinned, result.Items[0].Kind)
	require.Equal(t, StatusCandidate, result.Items[0].Status)
}

func TestMemoryProvider_ReflectBoundsGeneratedCandidates(t *testing.T) {
	generator := &fakeReflectionGenerator{result: ReflectionGenerationResult{Items: []MemoryItem{
		reflectionCandidate(KindSemantic, "First"),
		reflectionCandidate(KindProcedural, "Second"),
		reflectionCandidate(KindSemantic, "Third"),
	}}}
	provider := defaultMemoryTestProvider(t, Options{ReflectionGenerator: generator})

	_, err := provider.Upsert(context.Background(), MemoryItem{
		ID:     "mem_episode_limit",
		Kind:   KindEpisodic,
		Status: StatusActive,
		Text:   "The user gave several durable memory signals.",
		SourceLinks: []SourceLink{{
			SessionID: statecore.DefaultSessionID,
			Offsets:   []int{5},
		}},
	})
	require.NoError(t, err)

	result, err := provider.Reflect(context.Background(), ReflectionRequest{
		SessionID: statecore.DefaultSessionID,
		Limit:     2,
	})

	require.NoError(t, err)
	require.Equal(t, 2, result.WriteCount)
	require.Len(t, result.Items, 2)
}

func TestMemoryProvider_ReflectOverridesGeneratorProvenance(t *testing.T) {
	generator := &fakeReflectionGenerator{result: ReflectionGenerationResult{Items: []MemoryItem{{
		Kind:  KindSemantic,
		Title: "Preference",
		Text:  "The user prefers source-linked memory.",
		Metadata: map[string]string{
			"memory_importance":             "high",
			"memory_granularity":            "summary",
			"source_session_id":             "untrusted",
			"reflection_source_memory_ids":  "mem_untrusted",
			"reflection_origin":             "untrusted",
			"candidate_specific_confidence": "explicit",
		},
		SourceLinks: []SourceLink{{
			SessionID: "untrusted",
			Offsets:   []int{99},
		}},
	}}}}
	provider := defaultMemoryTestProvider(t, Options{ReflectionGenerator: generator})

	_, err := provider.Upsert(context.Background(), MemoryItem{
		ID:     "mem_episode_provenance",
		Kind:   KindEpisodic,
		Status: StatusActive,
		Text:   "The source evidence has trusted provenance.",
		SourceLinks: []SourceLink{{
			SessionID: statecore.DefaultSessionID,
			Offsets:   []int{6},
		}},
	})
	require.NoError(t, err)

	result, err := provider.Reflect(context.Background(), ReflectionRequest{SessionID: statecore.DefaultSessionID})

	require.NoError(t, err)
	require.Equal(t, 1, result.WriteCount)
	require.NotEqual(t, "untrusted", result.Items[0].SourceLinks[0].SessionID)
	require.NotEqual(t, []int{99}, result.Items[0].SourceLinks[0].Offsets)
	require.Equal(t, statecore.DefaultSessionID, result.Items[0].SourceLinks[0].SessionID)
	require.Equal(t, []int{6}, result.Items[0].SourceLinks[0].Offsets)
	require.NotEqual(t, "untrusted", result.Items[0].Metadata["source_session_id"])
	require.NotEqual(t, "mem_untrusted", result.Items[0].Metadata["reflection_source_memory_ids"])
	require.NotEqual(t, "untrusted", result.Items[0].Metadata["reflection_origin"])
	require.Equal(t, statecore.DefaultSessionID, result.Items[0].Metadata["source_session_id"])
	require.Equal(t, "mem_episode_provenance", result.Items[0].Metadata["reflection_source_memory_ids"])
	require.Equal(t, "episodic", result.Items[0].Metadata["reflection_origin"])
	require.Equal(t, "explicit", result.Items[0].Metadata["candidate_specific_confidence"])
}

func TestMemoryProvider_MarkReflectionSourceDoesNotOverwritePromotedMemory(t *testing.T) {
	provider := defaultMemoryTestProvider(t, Options{})
	source := reflectionSource("mem_episode_race", 4)
	source.Status = StatusCandidate
	source.Confidence = 0.9

	_, err := provider.Upsert(context.Background(), source)
	require.NoError(t, err)
	stale := source.Clone()

	promoted, err := provider.PromoteCandidate(context.Background(), PromotionRequest{ID: source.ID})
	require.NoError(t, err)
	require.Equal(t, StatusActive, promoted.Item.Status)
	require.False(t, promoted.Item.PromotionEvaluatedAt.IsZero())

	err = provider.markReflectionSourcesReflected(context.Background(), []MemoryItem{stale})
	require.NoError(t, err)

	result, err := provider.Search(context.Background(), SearchQuery{IDs: []string{source.ID}})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	require.Equal(t, StatusActive, result.Hits[0].Item.Status)
	require.True(t, result.Hits[0].Item.Reflected)
	require.False(t, result.Hits[0].Item.PromotionEvaluatedAt.IsZero())
}

func TestMemoryProvider_ReflectAssignsFreshCandidateIDs(t *testing.T) {
	generator := &fakeReflectionGenerator{result: ReflectionGenerationResult{Items: []MemoryItem{{
		ID:    "mem_existing_active",
		Kind:  KindSemantic,
		Title: "Fresh candidate",
		Text:  "The reflected candidate should not overwrite existing memory.",
		Metadata: map[string]string{
			"memory_importance":  "high",
			"memory_granularity": "summary",
		},
	}}}}
	provider := defaultMemoryTestProvider(t, Options{ReflectionGenerator: generator})

	_, err := provider.Upsert(context.Background(), MemoryItem{
		ID:     "mem_existing_active",
		Kind:   KindSemantic,
		Status: StatusActive,
		Text:   "Existing active memory.",
	})
	require.NoError(t, err)
	_, err = provider.Upsert(context.Background(), MemoryItem{
		ID:     "mem_episode_id",
		Kind:   KindEpisodic,
		Status: StatusActive,
		Text:   "The source evidence should generate a new candidate.",
		SourceLinks: []SourceLink{{
			SessionID: statecore.DefaultSessionID,
			Offsets:   []int{7},
		}},
	})
	require.NoError(t, err)

	result, err := provider.Reflect(context.Background(), ReflectionRequest{SessionID: statecore.DefaultSessionID})

	require.NoError(t, err)
	require.Equal(t, 1, result.WriteCount)
	require.NotEqual(t, "mem_existing_active", result.Items[0].ID)

	existing, err := provider.Search(context.Background(), SearchQuery{
		IDs:      []string{"mem_existing_active"},
		Statuses: []Status{StatusActive},
	})
	require.NoError(t, err)
	require.Len(t, existing.Hits, 1)
	require.Equal(t, "Existing active memory.", existing.Hits[0].Item.Text)
}

func TestMemoryProvider_ReflectValidationAndFailureBranches(t *testing.T) {
	var missing *MemoryProvider
	result, err := missing.Reflect(context.Background(), ReflectionRequest{})
	require.EqualError(t, err, "memory provider is required")
	require.Empty(t, result)

	provider := defaultMemoryTestProvider(t, Options{})
	result, err = provider.Reflect(context.Background(), ReflectionRequest{})
	require.EqualError(t, err, "memory reflection is not configured")
	require.Empty(t, result)

	provider = &MemoryProvider{}
	result, err = provider.Reflect(context.Background(), ReflectionRequest{})
	require.EqualError(t, err, "memory provider is required")
	require.Empty(t, result)

	manager := &recordingMemoryManager{fakeMemoryManager: fakeMemoryManager{searchErr: errors.New("search failed")}}
	provider = &MemoryProvider{manager: manager, reflectionGenerator: &fakeReflectionGenerator{}}
	result, err = provider.Reflect(context.Background(), ReflectionRequest{SessionID: statecore.DefaultSessionID})
	require.EqualError(t, err, "search failed")
	require.Empty(t, result)

	provider = &MemoryProvider{
		manager:             &recordingMemoryManager{currentSessionErr: errors.New("session failed")},
		reflectionGenerator: &fakeReflectionGenerator{},
	}
	result, err = provider.Reflect(context.Background(), ReflectionRequest{})
	require.EqualError(t, err, "session failed")
	require.Empty(t, result)
}

func TestMemoryProvider_ReflectReturnsWhenNoSources(t *testing.T) {
	generator := &fakeReflectionGenerator{}
	provider := defaultMemoryTestProvider(t, Options{ReflectionGenerator: generator})

	result, err := provider.Reflect(context.Background(), ReflectionRequest{SessionID: statecore.DefaultSessionID})

	require.NoError(t, err)
	require.Zero(t, result.SourceCount)
	require.Zero(t, result.WriteCount)
	require.Empty(t, result.Items)
	require.Empty(t, generator.requests)
}

func TestMemoryProvider_ReflectionHelpersCoverFallbacksAndErrors(t *testing.T) {
	t.Run("background options default and clamp", func(t *testing.T) {
		defaulted := normalizeReflectionBackgroundOptions(ReflectionBackgroundOptions{})
		require.Equal(t, defaultReflectionBackgroundInterval, defaulted.Interval)
		require.Equal(t, defaultReflectionLimit, defaulted.Limit)
		require.Equal(t, defaultReflectionRelatedLimit, defaulted.RelatedLimit)

		clamped := normalizeReflectionBackgroundOptions(ReflectionBackgroundOptions{
			Interval:     time.Second,
			Limit:        maxReflectionLimit + 1,
			RelatedLimit: maxReflectionRelatedLimit + 1,
		})
		require.Equal(t, time.Second, clamped.Interval)
		require.Equal(t, maxReflectionLimit, clamped.Limit)
		require.Equal(t, maxReflectionRelatedLimit, clamped.RelatedLimit)
	})

	t.Run("run background requires provider", func(t *testing.T) {
		var missing *MemoryProvider
		result, err := missing.RunReflectionBackground(context.Background(), ReflectionBackgroundOptions{})
		require.EqualError(t, err, "memory provider is required")
		require.Empty(t, result)

		result, err = (&MemoryProvider{}).RunReflectionBackground(context.Background(), ReflectionBackgroundOptions{})
		require.EqualError(t, err, "memory provider is required")
		require.Empty(t, result)

		result, err = (&MemoryProvider{manager: fakeMemoryManager{}}).RunReflectionBackground(
			context.Background(),
			ReflectionBackgroundOptions{},
		)
		require.EqualError(t, err, "session listing is required")
		require.Empty(t, result)
	})

	t.Run("run background returns list sessions error", func(t *testing.T) {
		manager := &recordingMemoryManager{listSessionsErr: errors.New("list failed")}
		provider := &MemoryProvider{manager: manager}

		result, err := provider.RunReflectionBackground(context.Background(), ReflectionBackgroundOptions{})

		require.EqualError(t, err, "list failed")
		require.Empty(t, result)
	})

	t.Run("run background reflects every listed session", func(t *testing.T) {
		generator := &fakeReflectionGenerator{result: ReflectionGenerationResult{Items: []MemoryItem{
			reflectionCandidate(KindProcedural, "Review daemon logs"),
		}}}
		manager := &recordingMemoryManager{
			sessions: []statecore.Session{
				{ID: "default", EpisodicCheckpointOffset: 3},
				{ID: "ses_workflow", EpisodicCheckpointOffset: 4},
			},
			messageCounts: map[string]int{
				"default":      3,
				"ses_workflow": 4,
			},
			searchResults: []SearchResult{
				{Hits: []SearchHit{{Item: reflectionSource("mem_default_source", 1)}}},
				{},
				{},
				{Hits: []SearchHit{{Item: func() MemoryItem {
					item := reflectionSource("mem_workflow_source", 2)
					item.SourceLinks[0].SessionID = "ses_workflow"
					return item
				}()}}},
				{},
				{},
			},
		}
		provider := &MemoryProvider{
			manager:             manager,
			reflectionGenerator: generator,
		}

		result, err := provider.RunReflectionBackground(context.Background(), ReflectionBackgroundOptions{
			Limit:        5,
			RelatedLimit: 2,
		})

		require.NoError(t, err)
		require.Equal(t, 2, result.SourceCount)
		require.Equal(t, 2, result.WriteCount)
		require.Len(t, result.Items, 2)
		require.Len(t, generator.requests, 2)
		require.Equal(t, "default", generator.requests[0].SessionID)
		require.Equal(t, "ses_workflow", generator.requests[1].SessionID)
		require.Len(t, manager.patches, 2)
		require.Equal(t, "mem_default_source", manager.patches[0].ID)
		require.Equal(t, "mem_workflow_source", manager.patches[1].ID)
		require.Equal(t, 3, manager.reflectionCheckpoints["default"])
		require.Equal(t, 4, manager.reflectionCheckpoints["ses_workflow"])
	})

	t.Run("run background waits for episodic checkpoint when no unreflected sources exist", func(t *testing.T) {
		generator := &fakeReflectionGenerator{}
		manager := &recordingMemoryManager{
			sessions: []statecore.Session{{
				ID:                       "ses_active",
				EpisodicCheckpointOffset: 0,
			}},
			messageCounts: map[string]int{"ses_active": 4},
			searchResults: []SearchResult{{}},
		}
		provider := &MemoryProvider{
			manager:             manager,
			reflectionGenerator: generator,
		}

		result, err := provider.RunReflectionBackground(context.Background(), ReflectionBackgroundOptions{})

		require.NoError(t, err)
		require.Zero(t, result.SourceCount)
		require.Empty(t, generator.requests)
		require.Len(t, manager.searchQueries, 1)
		require.Equal(t, 1, manager.searchQueries[0].Limit)
		require.NotNil(t, manager.searchQueries[0].Reflected)
		require.False(t, *manager.searchQueries[0].Reflected)
		require.Empty(t, manager.reflectionCheckpoints)
	})

	t.Run("run background skips checkpointed session with no unreflected sources", func(t *testing.T) {
		generator := &fakeReflectionGenerator{}
		manager := &recordingMemoryManager{
			sessions: []statecore.Session{{
				ID:                         "default",
				EpisodicCheckpointOffset:   4,
				ReflectionCheckpointOffset: 4,
			}},
			messageCounts: map[string]int{"default": 4},
			searchResults: []SearchResult{{}},
		}
		provider := &MemoryProvider{
			manager:             manager,
			reflectionGenerator: generator,
		}

		result, err := provider.RunReflectionBackground(context.Background(), ReflectionBackgroundOptions{})

		require.NoError(t, err)
		require.Zero(t, result.SourceCount)
		require.Empty(t, generator.requests)
		require.Len(t, manager.searchQueries, 1)
		require.Equal(t, 1, manager.searchQueries[0].Limit)
		require.NotNil(t, manager.searchQueries[0].Reflected)
		require.False(t, *manager.searchQueries[0].Reflected)
		require.Empty(t, manager.reflectionCheckpoints)
	})

	t.Run("run background reflects checkpointed session with unreflected tool source", func(t *testing.T) {
		generator := &fakeReflectionGenerator{result: ReflectionGenerationResult{Items: []MemoryItem{
			reflectionCandidate(KindProcedural, "Remembered tool workflow"),
		}}}
		manager := &recordingMemoryManager{
			sessions: []statecore.Session{{
				ID:                         "default",
				EpisodicCheckpointOffset:   4,
				ReflectionCheckpointOffset: 4,
			}},
			messageCounts: map[string]int{"default": 4},
			searchResults: []SearchResult{
				{Hits: []SearchHit{{Item: reflectionSource("mem_tool_source", 1)}}},
				{Hits: []SearchHit{{Item: reflectionSource("mem_tool_source", 1)}}},
				{},
				{},
			},
		}
		provider := &MemoryProvider{
			manager:             manager,
			reflectionGenerator: generator,
		}

		result, err := provider.RunReflectionBackground(context.Background(), ReflectionBackgroundOptions{})

		require.NoError(t, err)
		require.Equal(t, 1, result.SourceCount)
		require.Equal(t, 1, result.WriteCount)
		require.Len(t, generator.requests, 1)
		require.Equal(t, 4, manager.reflectionCheckpoints["default"])
	})

	t.Run("related search error", func(t *testing.T) {
		provider := &MemoryProvider{manager: fakeMemoryManager{searchErr: errors.New("related search failed")}}
		_, err := provider.loadReflectionRelated(context.Background(), []MemoryItem{{
			Title: "Related lookup",
		}}, normalizedReflectionRequest{RelatedLimit: 1})
		require.EqualError(t, err, "related search failed")
	})

	t.Run("related search skips empty and duplicate hits", func(t *testing.T) {
		manager := &recordingMemoryManager{
			searchResults: []SearchResult{
				{Hits: []SearchHit{
					{Item: MemoryItem{}},
					{Item: MemoryItem{ID: "mem_related", Text: "Related memory"}},
				}},
				{Hits: []SearchHit{{Item: MemoryItem{ID: "mem_related", Text: "Duplicate related memory"}}}},
			},
		}
		provider := &MemoryProvider{manager: manager}
		related, err := provider.loadReflectionRelated(context.Background(), []MemoryItem{
			{},
			{Title: "first"},
			{Title: "second"},
		}, normalizedReflectionRequest{RelatedLimit: 2})
		require.NoError(t, err)
		require.Len(t, related, 1)
		require.Equal(t, "mem_related", related[0].ID)
	})

	t.Run("mark reflected patches sources without full upsert", func(t *testing.T) {
		manager := &recordingMemoryManager{}
		provider := &MemoryProvider{manager: manager}
		err := provider.markReflectionSourcesReflected(context.Background(), []MemoryItem{{
			ID:     "mem_source",
			Status: StatusCandidate,
		}})
		require.NoError(t, err)
		require.Empty(t, manager.upsertItems)
		require.Len(t, manager.patches, 1)
		require.Equal(t, "mem_source", manager.patches[0].ID)
		require.NotNil(t, manager.patches[0].Reflected)
		require.True(t, *manager.patches[0].Reflected)
	})

	t.Run("mark reflected returns patch error", func(t *testing.T) {
		provider := &MemoryProvider{manager: &recordingMemoryManager{patchErrs: []error{errors.New("mark failed")}}}
		err := provider.markReflectionSourcesReflected(context.Background(), []MemoryItem{{ID: "mem_source"}})
		require.EqualError(t, err, "mark failed")
	})

	t.Run("pure helper fallbacks", func(t *testing.T) {
		require.Empty(t, getReflectionSearchText(MemoryItem{}))
		require.Len(t, []rune(getReflectionSearchText(MemoryItem{Text: strings.Repeat("x", 260)})), 240)
		require.Empty(t, getReflectionSourceTag(""))
		require.Equal(t, []MemoryItem{{ID: "mem_a"}}, limitReflectionItems([]MemoryItem{{ID: "mem_a"}}, 0))
		require.Equal(t, []string{"alpha", "beta"}, normalizeMemoryTags([]string{"Beta", "", "alpha", "beta"}))
	})

	t.Run("source links fall back to metadata session", func(t *testing.T) {
		links := getSourceLinks([]MemoryItem{{
			Metadata: map[string]string{"source_session_id": statecore.DefaultSessionID},
		}})
		require.Equal(t, statecore.DefaultSessionID, links[0].SessionID)
	})

	t.Run("normalize request requires session", func(t *testing.T) {
		normalized, err := (&MemoryProvider{manager: fakeMemoryManager{}}).normalizeReflectionRequest(
			context.Background(),
			ReflectionRequest{},
		)
		require.EqualError(t, err, "reflection session id is required")
		require.Empty(t, normalized)
	})

	t.Run("normalize request trims and clamps limits", func(t *testing.T) {
		normalized, err := (&MemoryProvider{}).normalizeReflectionRequest(context.Background(), ReflectionRequest{
			SessionID:    " session ",
			Limit:        100,
			RelatedLimit: 100,
		})
		require.NoError(t, err)
		require.Equal(t, "session", normalized.SessionID)
		require.Equal(t, maxReflectionLimit, normalized.Limit)
		require.Equal(t, maxReflectionRelatedLimit, normalized.RelatedLimit)
	})

	t.Run("prepare candidate can use source links without session", func(t *testing.T) {
		item, ok, rejection := prepareReflectionCandidate(MemoryItem{
			Kind:  KindSemantic,
			Title: "No session fallback",
			Text:  "A reflected candidate without request session still uses source links.",
		}, "", []SourceLink{{SessionID: statecore.DefaultSessionID}}, []string{""})
		require.True(t, ok)
		require.Empty(t, rejection)
		require.Equal(t, statecore.DefaultSessionID, item.SourceLinks[0].SessionID)
	})

	t.Run("matching reflection candidate handles empty and misses", func(t *testing.T) {
		require.False(t, hasDuplicateReflectionCandidate(MemoryItem{}, []MemoryItem{{
			Title: "Existing",
		}}))
		require.False(t, hasDuplicateReflectionCandidate(MemoryItem{Title: "New"}, []MemoryItem{{
			Title: "Existing",
		}}))
	})
}

func TestPrepareReflectionCandidateOverridesProviderOwnedFields(t *testing.T) {
	item, ok, rejection := prepareReflectionCandidate(
		MemoryItem{
			Kind:  KindSemantic,
			Title: "Preference",
			Text:  "The user prefers source-linked memory.",
			Metadata: map[string]string{
				"source_session_id":             "untrusted",
				"reflection_source_memory_ids":  "mem_untrusted",
				"reflection_origin":             "untrusted",
				"memory_importance":             "high",
				"memory_granularity":            "summary",
				"candidate_specific_confidence": "explicit",
			},
			SourceLinks: []SourceLink{{SessionID: "untrusted"}},
		},
		statecore.DefaultSessionID,
		[]SourceLink{{SessionID: statecore.DefaultSessionID, Offsets: []int{11}}},
		[]string{"mem_source"},
	)
	require.True(t, ok)
	require.Empty(t, rejection)
	require.Equal(t, statecore.DefaultSessionID, item.Metadata["source_session_id"])
	require.Equal(t, "mem_source", item.Metadata["reflection_source_memory_ids"])
	require.Equal(t, "episodic", item.Metadata["reflection_origin"])
	require.Equal(t, "explicit", item.Metadata["candidate_specific_confidence"])
	require.Equal(t, statecore.DefaultSessionID, item.SourceLinks[0].SessionID)
	require.Equal(t, []int{11}, item.SourceLinks[0].Offsets)
}

func TestMemoryProvider_ReflectReturnsCandidateWriteErrors(t *testing.T) {
	generator := &fakeReflectionGenerator{result: ReflectionGenerationResult{Items: []MemoryItem{
		reflectionCandidate(KindSemantic, "Write failure"),
	}}}
	provider := defaultMemoryTestProvider(t, Options{ReflectionGenerator: generator})
	_, err := provider.Upsert(context.Background(), MemoryItem{
		ID:     "mem_episode_write_guardrail",
		Kind:   KindEpisodic,
		Status: StatusActive,
		Text:   "Reflection source.",
		SourceLinks: []SourceLink{{
			SessionID: statecore.DefaultSessionID,
			Offsets:   []int{8},
		}},
	})
	require.NoError(t, err)
	provider.guardrails = &fakeGuardrails{writeErr: errors.New("write rejected")}

	result, err := provider.Reflect(context.Background(), ReflectionRequest{SessionID: statecore.DefaultSessionID})
	require.EqualError(t, err, "write rejected")
	require.Empty(t, result)

	manager := &recordingMemoryManager{
		fakeMemoryManager: fakeMemoryManager{searchResult: SearchResult{Hits: []SearchHit{{
			Item: MemoryItem{
				ID:     "mem_episode_upsert",
				Kind:   KindEpisodic,
				Status: StatusActive,
				Text:   "Reflection source.",
				SourceLinks: []SourceLink{{
					SessionID: statecore.DefaultSessionID,
					Offsets:   []int{9},
				}},
			},
		}}}},
		upsertErr: errors.New("upsert failed"),
	}
	provider = &MemoryProvider{
		manager:             manager,
		reflectionGenerator: generator,
	}

	result, err = provider.Reflect(context.Background(), ReflectionRequest{SessionID: statecore.DefaultSessionID})
	require.EqualError(t, err, "upsert failed")
	require.Empty(t, result)
}

func TestMemoryProvider_ReflectReturnsRelatedAndMarkErrors(t *testing.T) {
	source := reflectionSource("mem_episode_error", 10)
	manager := &recordingMemoryManager{
		searchResults: []SearchResult{{Hits: []SearchHit{{Item: source}}}},
		searchErrs:    []error{nil, errors.New("related failed")},
	}
	provider := &MemoryProvider{
		manager:             manager,
		reflectionGenerator: &fakeReflectionGenerator{},
	}

	result, err := provider.Reflect(context.Background(), ReflectionRequest{SessionID: statecore.DefaultSessionID})
	require.EqualError(t, err, "related failed")
	require.Empty(t, result)

	manager = &recordingMemoryManager{
		searchResults: []SearchResult{
			{Hits: []SearchHit{{Item: source}}},
			{},
		},
		patchErrs: []error{errors.New("mark failed")},
	}
	provider = &MemoryProvider{
		manager: manager,
		reflectionGenerator: &fakeReflectionGenerator{result: ReflectionGenerationResult{
			Items: []MemoryItem{reflectionCandidate(KindSemantic, "Mark failure")},
		}},
	}

	result, err = provider.Reflect(context.Background(), ReflectionRequest{SessionID: statecore.DefaultSessionID})
	require.EqualError(t, err, "mark failed")
	require.Empty(t, result)
	require.Len(t, manager.upsertItems, 1)
	require.Len(t, manager.patches, 1)
}

func TestMemoryProvider_ReflectReturnsCandidateDedupeErrors(t *testing.T) {
	source := reflectionSource("mem_episode_dedupe_error", 12)
	manager := &recordingMemoryManager{
		searchResults: []SearchResult{
			{Hits: []SearchHit{{Item: source}}},
			{},
		},
		searchErrs: []error{nil, nil, errors.New("dedupe failed")},
	}
	provider := &MemoryProvider{
		manager: manager,
		reflectionGenerator: &fakeReflectionGenerator{result: ReflectionGenerationResult{
			Items: []MemoryItem{reflectionCandidate(KindSemantic, "Dedupe failure")},
		}},
	}

	result, err := provider.Reflect(context.Background(), ReflectionRequest{SessionID: statecore.DefaultSessionID})

	require.EqualError(t, err, "dedupe failed")
	require.Empty(t, result)
	require.Empty(t, manager.upsertItems)
	require.Empty(t, manager.patches)
}

func TestValidateReflectionCandidateRejectsMalformedCandidates(t *testing.T) {
	valid := MemoryItem{
		Kind:   KindSemantic,
		Status: StatusCandidate,
		Text:   "Durable memory.",
		Metadata: map[string]string{
			"source_session_id": statecore.DefaultSessionID,
		},
	}

	tests := []struct {
		name string
		item MemoryItem
		err  string
	}{
		{
			name: "kind",
			item: func() MemoryItem {
				item := valid
				item.Kind = "other"
				return item
			}(),
			err: "reflection candidate kind must be pinned, semantic, episodic, or procedural",
		},
		{
			name: "status",
			item: func() MemoryItem {
				item := valid
				item.Status = StatusActive
				return item
			}(),
			err: "reflection candidate must be stored as candidate",
		},
		{
			name: "content",
			item: MemoryItem{Kind: KindSemantic, Status: StatusCandidate, Metadata: map[string]string{
				"source_session_id": statecore.DefaultSessionID,
			}},
			err: "reflection candidate text or title is required",
		},
		{
			name: "provenance",
			item: MemoryItem{Kind: KindSemantic, Status: StatusCandidate, Text: "Durable memory."},
			err:  "reflection candidate source provenance is required",
		},
		{
			name: "admission",
			item: func() MemoryItem {
				item := valid
				item.Metadata = map[string]string{
					"source_session_id":  statecore.DefaultSessionID,
					"memory_granularity": "execution_detail",
				}
				return item
			}(),
			err: "execution_detail",
		},
		{
			name: "procedural trigger",
			item: MemoryItem{
				Kind:   KindProcedural,
				Status: StatusCandidate,
				Text:   "When reviewing daemon logs, follow the user's workflow.",
				Metadata: map[string]string{
					"source_session_id": statecore.DefaultSessionID,
					"procedural_steps":  "Group lines by subsystem, identify anomalies, explain timeline, propose fixes.",
				},
			},
			err: "procedural_trigger_required",
		},
		{
			name: "procedural steps",
			item: MemoryItem{
				Kind:   KindProcedural,
				Status: StatusCandidate,
				Text:   "When reviewing daemon logs, follow the user's workflow.",
				Metadata: map[string]string{
					"source_session_id":   statecore.DefaultSessionID,
					"procedural_trigger":  "User asks to review daemon logs.",
					"procedural_examples": "model requests, memory extraction, promotion, embeddings, tools",
				},
			},
			err: "procedural_steps_required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.EqualError(t, validateReflectionCandidate(tt.item), tt.err)
		})
	}
}

func TestValidateReflectionCandidateAllowsActionableProceduralCandidates(t *testing.T) {
	item := MemoryItem{
		Kind:   KindProcedural,
		Status: StatusCandidate,
		Text:   "When reviewing daemon logs, first group lines by subsystem, then identify anomalies, explain the timeline, and propose fixes.",
		Metadata: map[string]string{
			"source_session_id":  statecore.DefaultSessionID,
			"procedural_trigger": "User asks to review daemon logs.",
			"procedural_steps":   "Group lines by subsystem; identify anomalies; explain timeline; propose fixes.",
		},
	}

	require.NoError(t, validateReflectionCandidate(item))
}

func TestLLMReflectionGenerator_GeneratesCandidatesWithMockedModel(t *testing.T) {
	client := &memoryModelClientStub{
		response: &models.Response{OutputText: `{
			"candidates": [{
				"kind": "procedural",
				"title": "Memory test workflow",
				"text": "Run focused memory package tests after changing memory behavior.",
				"tags": ["testing"],
				"confidence": 0.82,
				"procedural": {
					"trigger": "Memory behavior changes.",
					"steps": ["Run focused memory package tests."],
					"constraints": ["Keep the test scope focused."],
					"examples": ["internal/memory"],
					"expected_behavior": "Catch memory behavior regressions before continuing."
				},
				"metadata": [
					{"key": "memory_importance", "value": "high"},
					{"key": "memory_granularity", "value": "summary"},
					{"key": "custom_metadata", "value": "preserved"}
				]
			}]
		}`},
	}
	generator, err := NewLLMReflectionGenerator(LLMReflectionGeneratorOptions{
		Client: client,
		Model:  "test-model",
	})
	require.NoError(t, err)

	result, err := generator.GenerateReflectionCandidates(context.Background(), ReflectionGenerationRequest{
		SessionID: statecore.DefaultSessionID,
		Sources: []MemoryItem{{
			ID:   "mem_episode_model",
			Kind: KindEpisodic,
			Text: "The user asked for memory behavior tests.",
		}},
		Limit: 1,
	})

	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	require.Equal(t, KindProcedural, result.Items[0].Kind)
	require.Equal(t, StatusCandidate, result.Items[0].Status)
	require.Equal(t, "high", result.Items[0].Metadata["memory_importance"])
	require.Equal(t, "preserved", result.Items[0].Metadata["custom_metadata"])
	require.Equal(t, "Memory behavior changes.", result.Items[0].Metadata["procedural_trigger"])
	require.Equal(t, "Run focused memory package tests.", result.Items[0].Metadata["procedural_steps"])
	require.Equal(t, "Keep the test scope focused.", result.Items[0].Metadata["procedural_constraints"])
	require.Equal(t, "internal/memory", result.Items[0].Metadata["procedural_examples"])
	require.Equal(t, "Catch memory behavior regressions before continuing.", result.Items[0].Metadata["procedural_expected_behavior"])
	require.Len(t, client.requests, 1)
	require.Equal(t, "test-model", client.requests[0].Model)
	require.NotNil(t, client.requests[0].StructuredOutput)
	require.Contains(t, client.requests[0].Instructions, "key/value entries")
	require.Contains(t, client.requests[0].Instructions, "Use semantic for stable user facts")
	require.Contains(t, client.requests[0].Instructions, "multiple episodic sources corroborate a stable user fact")
	require.Contains(t, client.requests[0].Instructions, "Use procedural only for reusable workflows")
	require.Contains(t, client.requests[0].Instructions, "Use pinned only when the source explicitly requests")
	require.Contains(t, client.requests[0].Instructions, "Procedural candidates must be written as reusable instructions")
	require.Contains(t, client.requests[0].Instructions, "procedural.expected_behavior")
	require.Contains(t, client.requests[0].Instructions, "procedural.trigger")
	require.Contains(t, client.requests[0].Instructions, "procedural.steps")
	require.Contains(t, client.requests[0].Instructions, "Tags must be short machine labels")
	require.Contains(t, client.requests[0].Instructions, "lowercase kebab-case tags")
	require.Contains(t, client.requests[0].Instructions, "repeat the full title")
}

func TestLLMReflectionGenerator_CanSkipMaxOutputTokens(t *testing.T) {
	maxOutputTokens := false
	client := &memoryModelClientStub{
		response: &models.Response{OutputText: `{"candidates":[]}`},
	}
	generator, err := NewLLMReflectionGenerator(LLMReflectionGeneratorOptions{
		Client:                 client,
		Model:                  "test-model",
		MaxOutputTokensEnabled: &maxOutputTokens,
	})
	require.NoError(t, err)
	require.Zero(t, generator.options.MaxOutputTokens)

	result, err := generator.GenerateReflectionCandidates(context.Background(), ReflectionGenerationRequest{
		SessionID: statecore.DefaultSessionID,
		Sources: []MemoryItem{{
			ID:   "mem_episode_model",
			Kind: KindEpisodic,
			Text: "The user asked for memory behavior tests.",
		}},
		Limit: 1,
	})

	require.NoError(t, err)
	require.Empty(t, result.Items)
	require.Len(t, client.requests, 1)
	require.Zero(t, client.requests[0].MaxOutputTokens)
}

func TestReflectionModelResponseToGenerationResultUsesTypedProceduralFieldsForKind(t *testing.T) {
	result, err := reflectionModelResponseToGenerationResult(&models.Response{OutputText: `{
		"candidates": [{
			"kind": "episodic",
			"title": "Daemon log review workflow",
			"text": "When reviewing daemon logs, follow the user's workflow.",
			"tags": ["daemon_logs"],
			"confidence": 1,
			"procedural": {
				"trigger": "User asks to review daemon logs.",
				"steps": ["Group lines by subsystem.", "Explain timeline before fixes."],
				"constraints": [],
				"examples": [],
				"expected_behavior": ""
			},
			"metadata": [
				{"key": "memory_importance", "value": "high"},
				{"key": "memory_granularity", "value": "summary"}
			]
		}]
	}`})

	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	require.Equal(t, KindProcedural, result.Items[0].Kind)
	require.Equal(t, "User asks to review daemon logs.", result.Items[0].Metadata["procedural_trigger"])
	require.Equal(t, "Group lines by subsystem.; Explain timeline before fixes.", result.Items[0].Metadata["procedural_steps"])
}

func TestReflectionModelResponseToGenerationResultKeepsSemanticKindWhenOnlyExpectedBehaviorIsSet(t *testing.T) {
	result, err := reflectionModelResponseToGenerationResult(&models.Response{OutputText: `{
		"candidates": [{
			"kind": "semantic",
			"title": "User project codename preference",
			"text": "The user's preferred project codename for status reports is ember-lake.",
			"tags": ["preference", "codename"],
			"confidence": 1,
			"procedural": {
				"trigger": "",
				"steps": [],
				"constraints": [],
				"examples": [],
				"expected_behavior": "Use ember-lake in status reports."
			},
			"metadata": [
				{"key": "memory_importance", "value": "high"},
				{"key": "memory_granularity", "value": "summary"}
			]
		}]
	}`})

	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	require.Equal(t, KindSemantic, result.Items[0].Kind)
	require.Empty(t, result.Items[0].Metadata["procedural_expected_behavior"])
}

func TestLLMReflectionGenerator_StructuredOutputUsesMetadataEntries(t *testing.T) {
	output := getReflectionStructuredOutput()
	properties := output.Schema["properties"].(map[string]any)
	candidates := properties["candidates"].(map[string]any)
	candidateItems := candidates["items"].(map[string]any)
	candidateProperties := candidateItems["properties"].(map[string]any)
	metadata := candidateProperties["metadata"].(map[string]any)
	metadataItems := metadata["items"].(map[string]any)
	procedural := candidateProperties["procedural"].(map[string]any)
	proceduralProperties := procedural["properties"].(map[string]any)

	require.Equal(t, "array", metadata["type"])
	require.False(t, metadataItems["additionalProperties"].(bool))
	require.ElementsMatch(t, []string{"key", "value"}, metadataItems["required"])
	require.Equal(t, "object", procedural["type"])
	require.False(t, procedural["additionalProperties"].(bool))
	require.Equal(t, "string", proceduralProperties["trigger"].(map[string]any)["type"])
	require.Equal(t, "array", proceduralProperties["steps"].(map[string]any)["type"])
	require.Equal(t, "string", proceduralProperties["steps"].(map[string]any)["items"].(map[string]any)["type"])
	require.ElementsMatch(t, []string{"trigger", "steps", "constraints", "examples", "expected_behavior"}, procedural["required"])
}

func TestReflectionMetadataEntriesToMapSkipsBlankKeys(t *testing.T) {
	metadata := reflectionMetadataEntriesToMap([]reflectionModelMetadataEntry{
		{Key: " memory_importance ", Value: "high"},
		{Key: " ", Value: "ignored"},
		{Key: "", Value: "ignored"},
	})

	require.Equal(t, map[string]string{"memory_importance": "high"}, metadata)
	require.Nil(t, reflectionMetadataEntriesToMap(nil))
	require.Nil(t, reflectionMetadataEntriesToMap([]reflectionModelMetadataEntry{{Key: " "}}))
}

func TestSetProceduralReflectionMetadataNormalizesTypedFields(t *testing.T) {
	metadata := setProceduralReflectionMetadata(map[string]string{
		"memory_importance":  "high",
		"procedural_trigger": "stale trigger",
	}, reflectionModelProcedural{
		Trigger:          " User asks for daemon log review. ",
		Steps:            []string{" Group by subsystem. ", "", "Explain timeline."},
		Constraints:      []string{"Before fixes, explain timeline.", " "},
		Examples:         []string{"memory extraction", " promotion "},
		ExpectedBehavior: " Follow the workflow every time. ",
	})

	require.Equal(t, map[string]string{
		"memory_importance":            "high",
		"procedural_trigger":           "User asks for daemon log review.",
		"procedural_steps":             "Group by subsystem.; Explain timeline.",
		"procedural_constraints":       "Before fixes, explain timeline.",
		"procedural_examples":          "memory extraction; promotion",
		"procedural_expected_behavior": "Follow the workflow every time.",
	}, metadata)
}

func TestSetProceduralReflectionMetadataSkipsEmptyTypedFields(t *testing.T) {
	require.Nil(t, setProceduralReflectionMetadata(nil, reflectionModelProcedural{}))

	metadata := setProceduralReflectionMetadata(map[string]string{
		"memory_importance": "high",
	}, reflectionModelProcedural{
		Steps: []string{" ", ""},
	})

	require.Equal(t, map[string]string{"memory_importance": "high"}, metadata)
}

func TestLLMReflectionGenerator_ValidationAndModelErrors(t *testing.T) {
	generator, err := NewLLMReflectionGenerator(LLMReflectionGeneratorOptions{})
	require.EqualError(t, err, "memory reflection model client is required")
	require.Nil(t, generator)

	generator, err = NewLLMReflectionGenerator(LLMReflectionGeneratorOptions{
		Client: &memoryModelClientStub{},
	})
	require.EqualError(t, err, "memory reflection model is required")
	require.Nil(t, generator)

	var missing *LLMReflectionGenerator
	result, err := missing.GenerateReflectionCandidates(context.Background(), ReflectionGenerationRequest{})
	require.EqualError(t, err, "memory reflection model client is required")
	require.Empty(t, result)

	client := &memoryModelClientStub{err: errors.New("model failed")}
	generator, err = NewLLMReflectionGenerator(LLMReflectionGeneratorOptions{
		Client: client,
		Model:  "test-model",
	})
	require.NoError(t, err)
	result, err = generator.GenerateReflectionCandidates(context.Background(), ReflectionGenerationRequest{})
	require.EqualError(t, err, "model failed")
	require.Empty(t, result)
}

func TestParseReflectionModelResponseHandlesFencedJSONAndErrors(t *testing.T) {
	for _, raw := range []string{
		`{"candidates":[]}`,
		"```json\n{\"candidates\":[]}\n```",
		"```JSON\n{\"candidates\":[]}\n```",
		"```\n{\"candidates\":[]}\n```",
	} {
		result, err := reflectionModelResponseToGenerationResult(&models.Response{OutputText: raw})
		require.NoError(t, err)
		require.Empty(t, result.Items)
	}

	result, err := reflectionModelResponseToGenerationResult(nil)
	require.EqualError(t, err, "memory reflection response is required")
	require.Empty(t, result)

	result, err = reflectionModelResponseToGenerationResult(&models.Response{OutputText: `{"candidates":`})
	require.Error(t, err)
	require.Empty(t, result)
}

func reflectionCandidate(kind Kind, title string) MemoryItem {
	item := MemoryItem{
		Kind:  kind,
		Title: title,
		Text:  "When " + strings.ToLower(title) + " applies, run the remembered workflow.",
		Metadata: map[string]string{
			"memory_importance":  "high",
			"memory_granularity": "summary",
		},
	}
	if kind == KindProcedural {
		item.Metadata["procedural_trigger"] = title + " applies."
		item.Metadata["procedural_steps"] = "Run the remembered workflow."
	}

	return item
}

func reflectionSource(id string, offset int) MemoryItem {
	return MemoryItem{
		ID:     id,
		Kind:   KindEpisodic,
		Status: StatusActive,
		Text:   "Reflection source.",
		SourceLinks: []SourceLink{{
			SessionID: statecore.DefaultSessionID,
			Offsets:   []int{offset},
		}},
	}
}

func memoryHitIDs(hits []SearchHit) []string {
	ids := make([]string, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.Item.ID)
	}

	return ids
}
