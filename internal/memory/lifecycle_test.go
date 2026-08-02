package memory

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	storagememory "github.com/xymorphic/morph/internal/state/storememory"
	"github.com/xymorphic/morph/internal/trace"
)

func TestMemoryProvider_PromoteCandidateActivatesSafeHighConfidenceMemory(t *testing.T) {
	tracer := &fakeTracer{}
	guardrails := &fakeGuardrails{}
	provider := defaultMemoryTestProvider(t, Options{
		Guardrails:    guardrails,
		Observability: fakeObservability{tracer: tracer},
	})
	candidate := lifecycleCandidate("mem_candidate", KindSemantic, "Use focused tests.")

	_, err := provider.Upsert(context.Background(), candidate)
	require.NoError(t, err)
	validateBefore := guardrails.validateWriteCalls
	result, err := provider.PromoteCandidate(context.Background(), PromotionRequest{
		ID:     " mem_candidate ",
		Reason: "durable preference",
	})

	require.NoError(t, err)
	require.True(t, result.Decision.Approved)
	require.Equal(t, "approved", result.Decision.Reason)
	require.Equal(t, StatusActive, result.Item.Status)
	require.Equal(t, "promote", result.Item.Metadata[lifecycleMetadataAction])
	require.Equal(t, "durable preference", result.Item.Metadata[lifecycleMetadataReason])
	require.Equal(t, string(StatusCandidate), result.Item.Metadata[lifecycleMetadataPreviousStatus])
	require.Equal(t, promotionPolicyDefault, result.Item.Metadata[lifecycleMetadataDecisionPolicy])
	require.Equal(t, "approved", result.Item.Metadata[lifecycleMetadataDecisionOutcome])
	require.Equal(t, "approved", result.Item.Metadata[lifecycleMetadataDecisionReason])
	require.Equal(t, promotionConflictNone, result.Item.Metadata[lifecycleMetadataConflictState])
	require.False(t, result.Item.PromotionEvaluatedAt.IsZero())
	require.Equal(t, validateBefore+1, guardrails.validateWriteCalls)
	require.Contains(t, tracer.events, trace.EvtMemoryPromotionStarted)
	require.Contains(t, tracer.events, trace.EvtMemoryPromotionDecision)
	require.Contains(t, tracer.events, trace.EvtMemoryPromotionCompleted)
	require.Contains(t, tracer.events, trace.EvtMemoryPromotionFallback)

	search, err := provider.Search(context.Background(), SearchQuery{Text: "focused"})
	require.NoError(t, err)
	require.Equal(t, []string{"mem_candidate"}, lifecycleHitIDs(search.Hits))
}

func TestMemoryProvider_PromoteCandidateKeepsWeakOrRiskyCandidatesInactive(t *testing.T) {
	provider := defaultMemoryTestProvider(t, Options{})

	weak := lifecycleCandidate("mem_weak", KindSemantic, "Weak confidence.")
	weak.Confidence = 0.4
	_, err := provider.Upsert(context.Background(), weak)
	require.NoError(t, err)

	result, err := provider.PromoteCandidate(context.Background(), PromotionRequest{ID: "mem_weak"})
	require.NoError(t, err)
	require.False(t, result.Decision.Approved)
	require.Equal(t, "low_confidence", result.Decision.Reason)
	require.False(t, result.Item.PromotionEvaluatedAt.IsZero())

	search, err := provider.Search(context.Background(), SearchQuery{
		IDs:      []string{"mem_weak"},
		Statuses: []Status{StatusCandidate},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"mem_weak"}, lifecycleHitIDs(search.Hits))
	require.False(t, search.Hits[0].Item.PromotionEvaluatedAt.IsZero())

	risky := lifecycleCandidate("mem_risky", KindSemantic, "Risky candidate.")
	risky.Metadata["memory_granularity"] = "execution_detail"
	_, err = provider.Upsert(context.Background(), risky)
	require.NoError(t, err)

	result, err = provider.PromoteCandidate(context.Background(), PromotionRequest{ID: "mem_risky"})
	require.NoError(t, err)
	require.False(t, result.Decision.Approved)
	require.Equal(t, "execution_detail", result.Decision.Reason)
}

func TestMemoryProvider_PromoteCandidateGuardrailsAreHardGates(t *testing.T) {
	policy := &fakePromotionPolicy{decision: PromotionDecision{
		Approved:      true,
		Policy:        "unsafe_policy",
		Reason:        "approved_anyway",
		Confidence:    0.99,
		ConflictState: promotionConflictNone,
	}}
	provider := defaultMemoryTestProvider(t, Options{PromotionPolicy: policy})
	candidate := lifecycleCandidate("mem_candidate", KindSemantic, "Use focused tests.")
	_, err := provider.Upsert(context.Background(), candidate)
	require.NoError(t, err)
	provider.guardrails = &fakeGuardrails{writeErr: errors.New("unsafe memory")}

	result, err := provider.PromoteCandidate(context.Background(), PromotionRequest{ID: "mem_candidate"})

	require.NoError(t, err)
	require.False(t, result.Decision.Approved)
	require.Equal(t, "unsafe memory", result.Decision.Reason)
	require.False(t, result.Item.PromotionEvaluatedAt.IsZero())
	require.Len(t, policy.requests, 1)
	search, err := provider.Search(context.Background(), SearchQuery{
		IDs:      []string{"mem_candidate"},
		Statuses: []Status{StatusCandidate},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"mem_candidate"}, lifecycleHitIDs(search.Hits))
	require.False(t, search.Hits[0].Item.PromotionEvaluatedAt.IsZero())
}

func TestMemoryProvider_PromoteCandidateExplainsDecisionAndUsesRelatedSearch(t *testing.T) {
	policy := &fakePromotionPolicy{decision: PromotionDecision{
		Approved:      true,
		Policy:        "test_policy",
		Reason:        "approved_by_test",
		Confidence:    0.93,
		ConflictState: promotionConflictRelated,
	}}
	manager := &recordingMemoryManager{
		searchResults: []SearchResult{
			{Hits: []SearchHit{{Item: lifecycleCandidate("mem_candidate", KindSemantic, "Use focused tests.")}}},
			{Hits: []SearchHit{{Item: MemoryItem{
				ID:         "mem_related",
				Kind:       KindSemantic,
				Status:     StatusActive,
				Text:       "Use focused tests for memory work.",
				Confidence: 1,
			}, Score: 0.91}}},
		},
	}
	provider := &MemoryProvider{manager: manager, promotionPolicy: policy}

	result, err := provider.PromoteCandidate(context.Background(), PromotionRequest{ID: "mem_candidate"})

	require.NoError(t, err)
	require.True(t, result.Decision.Approved)
	require.Len(t, policy.requests, 1)
	require.Equal(t, "mem_candidate", policy.requests[0].Candidate.ID)
	require.True(t, policy.requests[0].ReflectionEvidence)
	require.Equal(t, promotionConflictRelated, policy.requests[0].ConflictState)
	require.Len(t, manager.searchQueries, 2)
	require.Equal(t, "Use focused tests.", manager.searchQueries[1].Text)
	require.Empty(t, manager.searchQueries[1].Kinds)
	require.Equal(t, []Status{StatusActive}, manager.searchQueries[1].Statuses)
	require.Len(t, manager.upsertItems, 1)
	require.Equal(t, "mem_related", manager.upsertItems[0].Metadata[lifecycleMetadataRelatedMemoryIDs])
	require.Equal(t, "test_policy", manager.upsertItems[0].Metadata[lifecycleMetadataDecisionPolicy])
}

func TestMemoryProvider_PromoteCandidateRejectsSemanticDuplicates(t *testing.T) {
	provider := defaultMemoryTestProvider(t, Options{})
	active := lifecycleCandidate("mem_active", KindSemantic, "Use focused tests.")
	active.Status = StatusActive
	candidate := lifecycleCandidate("mem_candidate", KindSemantic, "Use focused tests.")

	_, err := provider.Upsert(context.Background(), active)
	require.NoError(t, err)
	_, err = provider.Upsert(context.Background(), candidate)
	require.NoError(t, err)

	result, err := provider.PromoteCandidate(context.Background(), PromotionRequest{ID: "mem_candidate"})

	require.NoError(t, err)
	require.False(t, result.Decision.Approved)
	require.Equal(t, promotionConflictDuplicate, result.Decision.Reason)
	require.Equal(t, promotionConflictDuplicate, result.Decision.ConflictState)
	require.Equal(t, []string{"mem_active"}, lifecycleItemIDs(result.Related))
}

func TestMemoryProvider_PromoteCandidateRejectsCrossKindDuplicates(t *testing.T) {
	provider := defaultMemoryTestProvider(t, Options{})
	active := lifecycleCandidate("mem_active_episode", KindEpisodic, "User prefers black as their color preference.")
	active.Status = StatusActive
	candidate := lifecycleCandidate("mem_candidate_semantic", KindSemantic, "User prefers black as their color preference.")

	_, err := provider.Upsert(context.Background(), active)
	require.NoError(t, err)
	_, err = provider.Upsert(context.Background(), candidate)
	require.NoError(t, err)

	result, err := provider.PromoteCandidate(context.Background(), PromotionRequest{ID: "mem_candidate_semantic"})

	require.NoError(t, err)
	require.False(t, result.Decision.Approved)
	require.Equal(t, promotionConflictDuplicate, result.Decision.Reason)
	require.Equal(t, promotionConflictDuplicate, result.Decision.ConflictState)
	require.Equal(t, []string{"mem_active_episode"}, lifecycleItemIDs(result.Related))
}

func TestMemoryProvider_PromoteCandidateRejectsCrossKindNearDuplicates(t *testing.T) {
	active := lifecycleCandidate("mem_active_episode", KindEpisodic, "User prefers black as their color preference.")
	active.Status = StatusActive
	candidate := lifecycleCandidate("mem_candidate_semantic", KindSemantic, "The user's color preference is black.")
	manager := &recordingMemoryManager{
		searchResults: []SearchResult{
			{Hits: []SearchHit{{Item: candidate}}},
			{Hits: []SearchHit{{Item: active, Score: promotionCrossKindDuplicateScoreThreshold}}},
		},
	}
	tracer := &fakeTracer{}
	provider := &MemoryProvider{manager: manager, obs: fakeObservability{tracer: tracer}}

	result, err := provider.PromoteCandidate(context.Background(), PromotionRequest{ID: "mem_candidate_semantic"})

	require.NoError(t, err)
	require.False(t, result.Decision.Approved)
	require.Equal(t, promotionConflictDuplicate, result.Decision.Reason)
	require.Equal(t, promotionConflictDuplicate, result.Decision.ConflictState)
	require.Equal(t, []string{"mem_active_episode"}, lifecycleItemIDs(result.Related))
}

func TestMemoryProvider_PromoteCandidateIgnoresReflectionSourceDuplicate(t *testing.T) {
	source := lifecycleCandidate("mem_episode", KindEpisodic, "When reviewing daemon logs, group lines by subsystem before proposing fixes.")
	source.Status = StatusActive
	candidate := lifecycleCandidate("mem_candidate_procedural", KindProcedural, "When reviewing daemon logs, group lines by subsystem before proposing fixes.")
	manager := &recordingMemoryManager{
		searchResults: []SearchResult{
			{Hits: []SearchHit{{Item: candidate}}},
			{Hits: []SearchHit{{Item: source, Score: 1}}},
		},
	}
	tracer := &fakeTracer{}
	provider := &MemoryProvider{manager: manager, obs: fakeObservability{tracer: tracer}}

	result, err := provider.PromoteCandidate(context.Background(), PromotionRequest{ID: "mem_candidate_procedural"})

	require.NoError(t, err)
	require.True(t, result.Decision.Approved)
	require.Equal(t, promotionConflictNone, result.Decision.ConflictState)
	require.Empty(t, result.Related)
	require.Len(t, manager.upsertItems, 1)
	require.Equal(t, StatusActive, manager.upsertItems[0].Status)
}

func TestMemoryProvider_PromoteCandidateRejectsUnrelatedDuplicateWhenReflectionSourceAlsoRelated(t *testing.T) {
	source := lifecycleCandidate("mem_episode", KindEpisodic, "When reviewing daemon logs, group lines by subsystem before proposing fixes.")
	source.Status = StatusActive
	duplicate := lifecycleCandidate("mem_unrelated_duplicate", KindProcedural, "When reviewing daemon logs, group lines by subsystem before proposing fixes.")
	duplicate.Status = StatusActive
	candidate := lifecycleCandidate("mem_candidate_procedural", KindProcedural, "When reviewing daemon logs, group lines by subsystem before proposing fixes.")
	manager := &recordingMemoryManager{
		searchResults: []SearchResult{
			{Hits: []SearchHit{{Item: candidate}}},
			{Hits: []SearchHit{
				{Item: source, Score: 1},
				{Item: duplicate, Score: 1},
			}},
		},
	}
	provider := &MemoryProvider{manager: manager}

	result, err := provider.PromoteCandidate(context.Background(), PromotionRequest{ID: "mem_candidate_procedural"})

	require.NoError(t, err)
	require.False(t, result.Decision.Approved)
	require.Equal(t, promotionConflictDuplicate, result.Decision.Reason)
	require.Equal(t, promotionConflictDuplicate, result.Decision.ConflictState)
	require.Equal(t, []string{"mem_unrelated_duplicate"}, lifecycleItemIDs(result.Related))
}

func TestMemoryProvider_PromoteCandidateAllowsCrossKindRelatedMemoryBelowDuplicateThreshold(t *testing.T) {
	active := lifecycleCandidate("mem_active_episode", KindEpisodic, "The user asked for a daemon log review workflow.")
	active.Status = StatusActive
	candidate := lifecycleCandidate("mem_candidate_procedural", KindProcedural, "When reviewing daemon logs, group lines by subsystem before proposing fixes.")
	manager := &recordingMemoryManager{
		searchResults: []SearchResult{
			{Hits: []SearchHit{{Item: candidate}}},
			{Hits: []SearchHit{{Item: active, Score: promotionCrossKindDuplicateScoreThreshold - 0.01}}},
		},
	}
	provider := &MemoryProvider{manager: manager}

	result, err := provider.PromoteCandidate(context.Background(), PromotionRequest{ID: "mem_candidate_procedural"})

	require.NoError(t, err)
	require.True(t, result.Decision.Approved)
	require.Equal(t, promotionConflictNone, result.Decision.ConflictState)
	require.Len(t, manager.upsertItems, 1)
	require.Equal(t, StatusActive, manager.upsertItems[0].Status)
}

func TestMemoryProvider_PromoteCandidateIgnoresInactiveRelatedMemories(t *testing.T) {
	candidate := lifecycleCandidate("mem_candidate_semantic", KindSemantic, "User prefers black as their color preference.")
	candidateDuplicate := lifecycleCandidate("mem_candidate_duplicate", KindEpisodic, "User prefers black as their color preference.")
	deletedDuplicate := lifecycleCandidate("mem_deleted_duplicate", KindSemantic, "User prefers black as their color preference.")
	deletedDuplicate.Status = StatusDeleted
	supersededDuplicate := lifecycleCandidate("mem_superseded_duplicate", KindSemantic, "User prefers black as their color preference.")
	supersededDuplicate.Status = StatusSuperseded
	manager := &recordingMemoryManager{
		searchResults: []SearchResult{
			{Hits: []SearchHit{{Item: candidate}}},
			{Hits: []SearchHit{
				{Item: candidateDuplicate, Score: 1},
				{Item: deletedDuplicate, Score: 1},
				{Item: supersededDuplicate, Score: 1},
			}},
		},
	}
	provider := &MemoryProvider{manager: manager}

	result, err := provider.PromoteCandidate(context.Background(), PromotionRequest{ID: "mem_candidate_semantic"})

	require.NoError(t, err)
	require.True(t, result.Decision.Approved)
	require.Equal(t, promotionConflictNone, result.Decision.ConflictState)
	require.Len(t, manager.upsertItems, 1)
	require.Equal(t, StatusActive, manager.upsertItems[0].Status)
}

func TestMemoryProvider_PromoteCandidateRejectsRelatedActiveMemory(t *testing.T) {
	tracer := &fakeTracer{}
	active := lifecycleCandidate("mem_active", KindSemantic, "Use focused tests for memory work.")
	active.Status = StatusActive
	candidate := lifecycleCandidate("mem_candidate", KindSemantic, "Use focused tests.")
	manager := &recordingMemoryManager{
		searchResults: []SearchResult{
			{Hits: []SearchHit{{Item: candidate}}},
			{Hits: []SearchHit{{Item: active, Score: 0.91}}},
		},
	}
	provider := &MemoryProvider{
		manager: manager,
		obs:     fakeObservability{tracer: tracer},
	}

	result, err := provider.PromoteCandidate(context.Background(), PromotionRequest{ID: "mem_candidate"})

	require.NoError(t, err)
	require.False(t, result.Decision.Approved)
	require.Equal(t, promotionConflictRelated, result.Decision.Reason)
	require.Equal(t, promotionConflictRelated, result.Decision.ConflictState)
	require.Equal(t, []string{"mem_active"}, lifecycleItemIDs(result.Related))
	require.Len(t, manager.upsertItems, 1)
	require.Equal(t, "rejected", manager.upsertItems[0].Metadata[lifecycleMetadataDecisionOutcome])
	require.False(t, manager.upsertItems[0].PromotionEvaluatedAt.IsZero())

	fields := traceFieldsForEvent(t, tracer, trace.EvtMemoryPromotionDecision)
	require.Equal(t, 1, fields["related_count"])
	require.Equal(t, []string{"mem_active"}, fields["related_memory_ids"])
	require.Equal(t, 0.91, fields["related_top_score"])
}

func TestMemoryProvider_PromoteCandidateIgnoresLowScoreRelatedActiveMemory(t *testing.T) {
	active := lifecycleCandidate("mem_active", KindSemantic, "Use focused tests for memory work.")
	active.Status = StatusActive
	candidate := lifecycleCandidate("mem_candidate", KindSemantic, "Use focused tests.")
	manager := &recordingMemoryManager{
		searchResults: []SearchResult{
			{Hits: []SearchHit{{Item: candidate}}},
			{Hits: []SearchHit{{Item: active, Score: 0.2}}},
		},
	}
	provider := &MemoryProvider{manager: manager}

	result, err := provider.PromoteCandidate(context.Background(), PromotionRequest{ID: "mem_candidate"})

	require.NoError(t, err)
	require.True(t, result.Decision.Approved)
	require.Equal(t, promotionConflictNone, result.Decision.ConflictState)
	require.Len(t, manager.upsertItems, 1)
	require.Equal(t, StatusActive, manager.upsertItems[0].Status)
}

func TestMemoryProvider_DeleteHidesMemoryFromNormalRetrieval(t *testing.T) {
	provider := defaultMemoryTestProvider(t, Options{})
	item := lifecycleCandidate("mem_delete", KindSemantic, "Use pnpm for package installs.")
	item.Status = StatusActive
	_, err := provider.Upsert(context.Background(), item)
	require.NoError(t, err)

	search, err := provider.Search(context.Background(), SearchQuery{Text: "pnpm"})
	require.NoError(t, err)
	require.Equal(t, []string{"mem_delete"}, lifecycleHitIDs(search.Hits))

	require.NoError(t, provider.Delete(context.Background(), DeleteRequest{ID: "mem_delete", Reason: "obsolete"}))
	search, err = provider.Search(context.Background(), SearchQuery{Text: "pnpm"})
	require.NoError(t, err)
	require.Empty(t, search.Hits)

	search, err = provider.Search(context.Background(), SearchQuery{
		Text:     "pnpm",
		Statuses: []Status{StatusDeleted},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"mem_delete"}, lifecycleHitIDs(search.Hits))
	require.Equal(t, "delete", search.Hits[0].Item.Metadata[lifecycleMetadataAction])
	require.Equal(t, "obsolete", search.Hits[0].Item.Metadata[lifecycleMetadataReason])
	require.Equal(t, string(StatusActive), search.Hits[0].Item.Metadata[lifecycleMetadataPreviousStatus])
}

func TestMemoryProvider_PromoteCandidateFailureLeavesStateUnchanged(t *testing.T) {
	candidate := lifecycleCandidate("mem_candidate", KindSemantic, "Use focused tests.")
	storeManager := newMemoryTestManager(t, storagememory.NewStore())
	_, err := storeManager.UpsertMemory(context.Background(), candidate)
	require.NoError(t, err)
	manager := &failingUpsertStateManager{
		StateManager: storeManager,
		err:          errors.New("upsert failed"),
	}
	provider := &MemoryProvider{manager: manager}

	result, err := provider.PromoteCandidate(context.Background(), PromotionRequest{ID: "mem_candidate"})

	require.EqualError(t, err, "upsert failed")
	require.Empty(t, result)
	require.Equal(t, 1, manager.calls)
	search, err := storeManager.SearchMemory(context.Background(), SearchQuery{
		IDs:      []string{"mem_candidate"},
		Statuses: []Status{StatusCandidate},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"mem_candidate"}, lifecycleHitIDs(search.Hits))
}

func TestMemoryProvider_PinnedPromotionRequiresStrictApproval(t *testing.T) {
	provider := defaultMemoryTestProvider(t, Options{})
	pinned := lifecycleCandidate("mem_pinned", KindPinned, "Always include project constraints.")
	pinned.Confidence = 0.95
	_, err := provider.Upsert(context.Background(), pinned)
	require.NoError(t, err)

	result, err := provider.PromoteCandidate(context.Background(), PromotionRequest{ID: "mem_pinned"})
	require.NoError(t, err)
	require.False(t, result.Decision.Approved)
	require.Equal(t, "pinned_memory_requires_strict_policy", result.Decision.Reason)

	result, err = provider.PromoteCandidate(context.Background(), PromotionRequest{
		ID:     "mem_pinned",
		Strict: true,
	})
	require.NoError(t, err)
	require.True(t, result.Decision.Approved)
	require.Equal(t, StatusActive, result.Item.Status)
}

func TestMemoryProvider_RunPromotionBackgroundPromotesCandidatesIndependently(t *testing.T) {
	provider := defaultMemoryTestProvider(t, Options{})
	eligible := lifecycleCandidate("mem_eligible", KindSemantic, "Use focused tests.")
	weak := lifecycleCandidate("mem_weak", KindSemantic, "Store launch rituals in the morphbook.")
	weak.Confidence = 0.1
	evaluated := lifecycleCandidate("mem_evaluated", KindSemantic, "Already reviewed.")
	evaluated.PromotionEvaluatedAt = lifecycleEvaluationTime()

	for _, item := range []MemoryItem{eligible, weak, evaluated} {
		_, err := provider.Upsert(context.Background(), item)
		require.NoError(t, err)
	}

	count, err := provider.RunPromotionBackground(context.Background(), PromotionBackgroundOptions{
		Limit:  10,
		Reason: "test_background",
	})

	require.NoError(t, err)
	require.Equal(t, 2, count)

	active, err := provider.Search(context.Background(), SearchQuery{
		IDs: []string{"mem_eligible"},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"mem_eligible"}, lifecycleHitIDs(active.Hits))
	require.Equal(t, "test_background", active.Hits[0].Item.Metadata[lifecycleMetadataReason])

	candidates, err := provider.Search(context.Background(), SearchQuery{
		IDs:      []string{"mem_weak", "mem_evaluated"},
		Statuses: []Status{StatusCandidate},
	})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"mem_evaluated", "mem_weak"}, lifecycleHitIDs(candidates.Hits))
	byID := lifecycleHitsByID(candidates.Hits)
	require.Equal(t, "rejected", byID["mem_weak"].Metadata[lifecycleMetadataDecisionOutcome])
	require.Equal(t, "low_confidence", byID["mem_weak"].Metadata[lifecycleMetadataDecisionReason])
	require.False(t, byID["mem_weak"].PromotionEvaluatedAt.IsZero())

	count, err = provider.RunPromotionBackground(context.Background(), PromotionBackgroundOptions{Limit: 10})
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestMemoryProvider_RunPromotionBackgroundUsesEvaluationFilterAndLimit(t *testing.T) {
	eligible := lifecycleCandidate("mem_eligible", KindSemantic, "Use focused tests.")
	second := lifecycleCandidate("mem_second", KindSemantic, "Use focused reviews.")
	manager := &recordingMemoryManager{
		searchResults: []SearchResult{
			{Hits: []SearchHit{{Item: MemoryItem{}}, {Item: eligible}, {Item: second}}},
			{Hits: []SearchHit{{Item: eligible}}},
			{},
		},
	}
	tracer := &fakeTracer{}
	provider := &MemoryProvider{manager: manager, obs: fakeObservability{tracer: tracer}}

	count, err := provider.RunPromotionBackground(context.Background(), PromotionBackgroundOptions{Limit: 1})

	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Len(t, manager.searchQueries, 3)
	require.Equal(t, 1, manager.searchQueries[0].Limit)
	require.NotNil(t, manager.searchQueries[0].PromotionEvaluated)
	require.False(t, *manager.searchQueries[0].PromotionEvaluated)
	require.Len(t, manager.upsertItems, 1)
	require.Equal(t, "mem_eligible", manager.upsertItems[0].ID)
	require.Equal(t, StatusActive, manager.upsertItems[0].Status)
	require.False(t, manager.upsertItems[0].PromotionEvaluatedAt.IsZero())
	fields := traceFieldsForEvent(t, tracer, trace.EvtMemoryPromotionBackgroundCompleted)
	require.Equal(t, 1, fields["result_count"])
	require.Equal(t, 1, fields["limit"])
}

func TestMemoryProvider_RunPromotionBackgroundReturnsErrorsAndCapsLimits(t *testing.T) {
	var missing *MemoryProvider
	count, err := missing.RunPromotionBackground(context.Background(), PromotionBackgroundOptions{})
	require.EqualError(t, err, "memory provider is required")
	require.Zero(t, count)

	provider := &MemoryProvider{}
	count, err = provider.RunPromotionBackground(context.Background(), PromotionBackgroundOptions{})
	require.EqualError(t, err, "memory provider is required")
	require.Zero(t, count)

	tracer := &fakeTracer{}
	provider = &MemoryProvider{
		manager: fakeMemoryManager{searchErr: errors.New("candidate search failed")},
		obs:     fakeObservability{tracer: tracer},
	}
	count, err = provider.RunPromotionBackground(context.Background(), PromotionBackgroundOptions{})
	require.EqualError(t, err, "candidate search failed")
	require.Zero(t, count)
	fields := traceFieldsForEvent(t, tracer, trace.EvtMemoryPromotionBackgroundFailed)
	require.Equal(t, "candidate search failed", fields["error"])

	manager := &recordingMemoryManager{
		searchResults: []SearchResult{{Hits: []SearchHit{{
			Item: lifecycleCandidate("mem_candidate", KindSemantic, "Use focused tests."),
		}}}},
		searchErrs: []error{nil, errors.New("candidate load failed")},
	}
	provider = &MemoryProvider{manager: manager}
	count, err = provider.RunPromotionBackground(context.Background(), PromotionBackgroundOptions{Limit: 1})
	require.EqualError(t, err, "candidate load failed")
	require.Zero(t, count)

	manager = &recordingMemoryManager{}
	provider = &MemoryProvider{manager: manager}
	count, err = provider.RunPromotionBackground(context.Background(), PromotionBackgroundOptions{Limit: 1000})
	require.NoError(t, err)
	require.Zero(t, count)
	require.Len(t, manager.searchQueries, 1)
	require.Equal(t, maxPromotionBackgroundLimit, manager.searchQueries[0].Limit)
	require.NotNil(t, manager.searchQueries[0].PromotionEvaluated)
	require.False(t, *manager.searchQueries[0].PromotionEvaluated)
}

func TestMemoryProvider_RunPromotionCleanupDeletesOnlyExpiredEvaluatedCandidates(t *testing.T) {
	now := time.Now().UTC()
	oldEvaluated := lifecycleCandidate("mem_old", KindSemantic, "Old rejected candidate.")
	oldEvaluated.PromotionEvaluatedAt = now.Add(-48 * time.Hour)
	recentEvaluated := lifecycleCandidate("mem_recent", KindSemantic, "Recent rejected candidate.")
	recentEvaluated.PromotionEvaluatedAt = now.Add(-time.Hour)
	unevaluated := lifecycleCandidate("mem_unevaluated", KindSemantic, "Unevaluated candidate.")
	active := lifecycleCandidate("mem_active", KindSemantic, "Promoted memory.")
	active.Status = StatusActive
	active.PromotionEvaluatedAt = oldEvaluated.PromotionEvaluatedAt
	manager := &recordingMemoryManager{
		searchResults: []SearchResult{{
			Hits: []SearchHit{
				{Item: MemoryItem{}},
				{Item: oldEvaluated},
				{Item: recentEvaluated},
				{Item: unevaluated},
				{Item: active},
			},
		}},
	}
	tracer := &fakeTracer{}
	provider := &MemoryProvider{manager: manager, obs: fakeObservability{tracer: tracer}}

	count, err := provider.RunPromotionCleanup(context.Background(), PromotionBackgroundOptions{
		Limit:              10,
		EvaluatedRetention: 24 * time.Hour,
	})

	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Len(t, manager.searchQueries, 1)
	require.Equal(t, []Status{StatusCandidate}, manager.searchQueries[0].Statuses)
	require.NotNil(t, manager.searchQueries[0].PromotionEvaluated)
	require.True(t, *manager.searchQueries[0].PromotionEvaluated)
	require.False(t, manager.searchQueries[0].PromotionEvaluatedBefore.IsZero())
	require.Equal(t, []DeleteRequest{{
		ID:     "mem_old",
		Reason: "promotion_evaluated_candidate_retention",
	}}, manager.hardDeleteRequests)
	fields := traceFieldsForEvent(t, tracer, trace.EvtMemoryPromotionCleanupCompleted)
	require.Equal(t, 1, fields["result_count"])
	require.Equal(t, 10, fields["limit"])
	require.Equal(t, int64((24 * time.Hour).Milliseconds()), fields["retention_ms"])
}

func TestMemoryProvider_RunPromotionCleanupCapsReturnedCandidatesAtLimit(t *testing.T) {
	now := time.Now().UTC()
	first := lifecycleCandidate("mem_first", KindSemantic, "First old rejected candidate.")
	first.PromotionEvaluatedAt = now.Add(-48 * time.Hour)
	second := lifecycleCandidate("mem_second", KindSemantic, "Second old rejected candidate.")
	second.PromotionEvaluatedAt = now.Add(-48 * time.Hour)
	manager := &recordingMemoryManager{
		searchResults: []SearchResult{{
			Hits: []SearchHit{
				{Item: first},
				{Item: second},
			},
		}},
	}
	provider := &MemoryProvider{manager: manager}

	count, err := provider.RunPromotionCleanup(context.Background(), PromotionBackgroundOptions{
		Limit:              1,
		EvaluatedRetention: 24 * time.Hour,
	})

	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Equal(t, 1, manager.searchQueries[0].Limit)
	require.Equal(t, []DeleteRequest{{
		ID:     "mem_first",
		Reason: "promotion_evaluated_candidate_retention",
	}}, manager.hardDeleteRequests)
}

func TestMemoryProvider_RunPromotionCleanupSkipsDisabledRetentionAndReturnsErrors(t *testing.T) {
	var missing *MemoryProvider
	count, err := missing.RunPromotionCleanup(context.Background(), PromotionBackgroundOptions{})
	require.EqualError(t, err, "memory provider is required")
	require.Zero(t, count)

	manager := &recordingMemoryManager{}
	tracer := &fakeTracer{}
	provider := &MemoryProvider{manager: manager, obs: fakeObservability{tracer: tracer}}
	count, err = provider.RunPromotionCleanup(context.Background(), PromotionBackgroundOptions{
		EvaluatedRetention: -time.Hour,
	})
	require.NoError(t, err)
	require.Zero(t, count)
	require.Empty(t, manager.searchQueries)
	fields := traceFieldsForEvent(t, tracer, trace.EvtMemoryPromotionCleanupSkipped)
	require.Equal(t, "retention_disabled", fields["reason"])

	manager = &recordingMemoryManager{searchErrs: []error{errors.New("cleanup search failed")}}
	tracer = &fakeTracer{}
	provider = &MemoryProvider{manager: manager, obs: fakeObservability{tracer: tracer}}
	count, err = provider.RunPromotionCleanup(context.Background(), PromotionBackgroundOptions{
		EvaluatedRetention: time.Hour,
	})
	require.EqualError(t, err, "cleanup search failed")
	require.Zero(t, count)
	fields = traceFieldsForEvent(t, tracer, trace.EvtMemoryPromotionCleanupFailed)
	require.Equal(t, "cleanup search failed", fields["error"])

	oldEvaluated := lifecycleCandidate("mem_old", KindSemantic, "Old rejected candidate.")
	oldEvaluated.PromotionEvaluatedAt = time.Now().UTC().Add(-48 * time.Hour)
	manager = &recordingMemoryManager{
		searchResults:  []SearchResult{{Hits: []SearchHit{{Item: oldEvaluated}}}},
		hardDeleteErrs: []error{errors.New("cleanup delete failed")},
	}
	tracer = &fakeTracer{}
	provider = &MemoryProvider{manager: manager, obs: fakeObservability{tracer: tracer}}
	count, err = provider.RunPromotionCleanup(context.Background(), PromotionBackgroundOptions{
		EvaluatedRetention: time.Hour,
	})
	require.EqualError(t, err, "cleanup delete failed")
	require.Zero(t, count)
	fields = traceFieldsForEvent(t, tracer, trace.EvtMemoryPromotionCleanupFailed)
	require.Equal(t, "cleanup delete failed", fields["error"])
}

func TestMemoryProvider_LifecycleValidationAndPolicyErrors(t *testing.T) {
	var missing *MemoryProvider
	_, err := missing.PromoteCandidate(context.Background(), PromotionRequest{ID: "mem"})
	require.EqualError(t, err, "memory provider is required")

	provider := defaultMemoryTestProvider(t, Options{})
	_, err = provider.PromoteCandidate(context.Background(), PromotionRequest{})
	require.EqualError(t, err, "candidate memory id is required")

	policy := &fakePromotionPolicy{err: errors.New("policy failed")}
	manager := &recordingMemoryManager{
		searchResults: []SearchResult{
			{Hits: []SearchHit{{Item: lifecycleCandidate("mem_candidate", KindSemantic, "Use focused tests.")}}},
			{},
		},
	}
	provider = &MemoryProvider{manager: manager, promotionPolicy: policy}
	_, err = provider.PromoteCandidate(context.Background(), PromotionRequest{ID: "mem_candidate"})
	require.EqualError(t, err, "policy failed")
}

func TestDefaultPromotionPolicyDecisionBranches(t *testing.T) {
	policy := defaultPromotionPolicy{}
	base := lifecycleCandidate("mem_candidate", KindSemantic, "Use focused tests.")

	tests := []struct {
		name     string
		request  PromotionPolicyRequest
		approved bool
		reason   string
	}{
		{
			name: "status",
			request: func() PromotionPolicyRequest {
				item := base
				item.Status = StatusActive
				return PromotionPolicyRequest{Candidate: item}
			}(),
			reason: "candidate_status_required",
		},
		{
			name:    "admission",
			request: PromotionPolicyRequest{Candidate: base, AdmissionResult: "execution_detail"},
			reason:  "execution_detail",
		},
		{
			name:    "guardrail",
			request: PromotionPolicyRequest{Candidate: base, GuardrailResult: "unsafe"},
			reason:  "unsafe",
		},
		{
			name:    "conflict",
			request: PromotionPolicyRequest{Candidate: base, ConflictState: promotionConflictDuplicate},
			reason:  promotionConflictDuplicate,
		},
		{
			name:    "related",
			request: PromotionPolicyRequest{Candidate: base, ConflictState: promotionConflictRelated},
			reason:  promotionConflictRelated,
		},
		{
			name: "provenance",
			request: func() PromotionPolicyRequest {
				item := base
				item.Metadata = nil
				item.SourceLinks = nil
				return PromotionPolicyRequest{Candidate: item}
			}(),
			reason: "missing_provenance",
		},
		{
			name: "approved",
			request: PromotionPolicyRequest{
				Candidate:     base,
				ConflictState: promotionConflictNone,
			},
			approved: true,
			reason:   "approved",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, err := policy.EvaluatePromotion(context.Background(), tt.request)
			require.NoError(t, err)
			require.Equal(t, tt.approved, decision.Approved)
			require.Equal(t, tt.reason, decision.Reason)
		})
	}
}

func TestEnforcePromotionHardGates(t *testing.T) {
	decision := enforcePromotionHardGates(PromotionDecision{
		Approved: true,
		Reason:   "approved",
	}, "low_importance_candidate", "")
	require.False(t, decision.Approved)
	require.Equal(t, "low_importance_candidate", decision.Reason)

	decision = enforcePromotionHardGates(PromotionDecision{
		Approved: true,
		Reason:   "approved",
	}, "", "unsafe")
	require.False(t, decision.Approved)
	require.Equal(t, "unsafe", decision.Reason)
}

func TestMemoryProvider_LifecycleFailureBranches(t *testing.T) {
	tracer := &fakeTracer{}
	provider := &MemoryProvider{
		manager: fakeMemoryManager{searchErr: errors.New("load failed")},
		obs:     fakeObservability{tracer: tracer},
	}
	_, err := provider.PromoteCandidate(context.Background(), PromotionRequest{ID: "mem_missing"})
	require.EqualError(t, err, "load failed")
	require.Contains(t, tracer.events, trace.EvtMemoryPromotionFailed)

	provider = &MemoryProvider{
		manager: &recordingMemoryManager{
			searchResults: []SearchResult{
				{Hits: []SearchHit{{Item: lifecycleCandidate("mem_candidate", KindSemantic, "Use focused tests.")}}},
			},
			searchErrs: []error{nil, errors.New("related failed")},
		},
		obs: fakeObservability{tracer: tracer},
	}
	_, err = provider.PromoteCandidate(context.Background(), PromotionRequest{ID: "mem_candidate"})
	require.EqualError(t, err, "related failed")

	provider = &MemoryProvider{
		manager: &recordingMemoryManager{
			searchResults: []SearchResult{
				{Hits: []SearchHit{{Item: func() MemoryItem {
					item := lifecycleCandidate("mem_candidate", KindSemantic, "")
					item.Confidence = 0.1
					return item
				}()}}},
			},
		},
	}
	result, err := provider.PromoteCandidate(context.Background(), PromotionRequest{ID: "mem_candidate"})
	require.NoError(t, err)
	require.False(t, result.Decision.Approved)

	provider = &MemoryProvider{
		manager: &recordingMemoryManager{
			searchResults: []SearchResult{
				{Hits: []SearchHit{{Item: func() MemoryItem {
					item := lifecycleCandidate("mem_candidate", KindSemantic, "Weak candidate.")
					item.Confidence = 0.1
					return item
				}()}}},
				{},
			},
			upsertErr: errors.New("denial write failed"),
		},
	}
	_, err = provider.PromoteCandidate(context.Background(), PromotionRequest{ID: "mem_candidate"})
	require.EqualError(t, err, "denial write failed")

	provider = &MemoryProvider{
		manager: &recordingMemoryManager{
			searchResults: []SearchResult{
				{Hits: []SearchHit{{Item: lifecycleCandidate("mem_candidate", KindSemantic, "Use focused tests.")}}},
				{},
			},
		},
		guardrails: &fakeGuardrails{writeErr: errors.New("guardrail failed")},
	}
	result, err = provider.PromoteCandidate(context.Background(), PromotionRequest{ID: "mem_candidate"})
	require.NoError(t, err)
	require.False(t, result.Decision.Approved)
	require.Equal(t, "guardrail failed", result.Decision.Reason)

}

func TestLifecycleHelpersCoverFallbacks(t *testing.T) {
	require.Empty(t, getPromotionSearchText(MemoryItem{}))
	require.Len(t, []rune(getPromotionSearchText(MemoryItem{Title: strings.Repeat("x", 260)})), 240)
	require.Equal(t, promotionConflictNone, checkPromotionConflictState(
		lifecycleCandidate("mem_candidate", KindSemantic, "Use focused tests."),
		[]SearchHit{{Item: func() MemoryItem {
			item := lifecycleCandidate("mem_related", KindProcedural, "Use focused tests differently.")
			item.Status = StatusActive
			return item
		}(), Score: promotionCrossKindDuplicateScoreThreshold - 0.01}},
	))
	require.Equal(t, promotionConflictDuplicate, checkPromotionConflictState(
		lifecycleCandidate("mem_candidate", KindSemantic, "Use focused tests."),
		[]SearchHit{{Item: func() MemoryItem {
			item := lifecycleCandidate("mem_related", KindProcedural, "Use focused tests.")
			item.Status = StatusActive
			return item
		}(), Score: 1}},
	))
	require.False(t, hasReflectionEvidence(MemoryItem{}))

	manager := &recordingMemoryManager{searchResults: []SearchResult{{Hits: []SearchHit{
		{Item: lifecycleCandidate("mem_candidate", KindSemantic, "Use focused tests.")},
		{Item: lifecycleCandidate("mem_related", KindSemantic, "Use focused tests.")},
	}}}}
	provider := &MemoryProvider{manager: manager}
	related, err := provider.relatedPromotionMemories(
		context.Background(),
		lifecycleCandidate("mem_candidate", KindSemantic, "Use focused tests."),
	)
	require.NoError(t, err)
	require.Equal(t, []string{"mem_related"}, lifecycleItemIDs(getPromotionRelatedItems(related)))

	metadata := make(map[string]string)
	writePromotionDecisionMetadata(metadata, PromotionDecision{
		Policy:        "test_policy",
		Reason:        "rejected",
		ConflictState: promotionConflictDuplicate,
	}, nil)
	require.Equal(t, "rejected", metadata[lifecycleMetadataDecisionOutcome])
}

func lifecycleCandidate(id string, kind Kind, text string) MemoryItem {
	return MemoryItem{
		ID:         id,
		Kind:       kind,
		Status:     StatusCandidate,
		Text:       text,
		Confidence: 0.92,
		Metadata: map[string]string{
			"source_session_id":            "session-test",
			"memory_importance":            "high",
			"memory_granularity":           "summary",
			"reflection_source_memory_ids": "mem_episode",
			"reflection_origin":            "episodic",
		},
		SourceLinks: []SourceLink{{
			SessionID:     "session-test",
			MessageIDs:    []uint{1},
			CreatedBy:     "test",
			CreatedReason: "lifecycle_test",
		}},
		Reflected: true,
	}
}

func lifecycleEvaluationTime() time.Time {
	return time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
}

func lifecycleHitIDs(hits []SearchHit) []string {
	ids := make([]string, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.Item.ID)
	}

	return ids
}

func lifecycleItemIDs(items []MemoryItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}

	return ids
}

func lifecycleHitsByID(hits []SearchHit) map[string]MemoryItem {
	items := make(map[string]MemoryItem, len(hits))
	for _, hit := range hits {
		items[hit.Item.ID] = hit.Item
	}

	return items
}

func traceFieldsForEvent(t *testing.T, tracer *fakeTracer, event string) map[string]any {
	t.Helper()
	for idx, recorded := range tracer.events {
		if recorded == event {
			return tracer.fields[idx]
		}
	}
	require.Failf(t, "trace event not recorded", "event %q not found", event)
	return nil
}

type failingUpsertStateManager struct {
	StateManager
	err   error
	calls int
}

func (m *failingUpsertStateManager) UpsertMemory(context.Context, MemoryItem) (MemoryItem, error) {
	m.calls++
	return MemoryItem{}, m.err
}
