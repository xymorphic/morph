package search

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	state "github.com/xymorphic/morph/internal/state/core"
)

func TestRerankMemoryHits_DedupesAndKeepsStrongestCandidate(t *testing.T) {
	now := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	result, err := RerankMemoryHits(context.Background(), state.MemorySearchQuery{Text: "plan"}, []state.MemorySearchHit{
		{
			Item: state.MemoryItem{
				ID:        "mem_plan",
				Status:    state.MemoryStatusActive,
				Title:     "Plan",
				Text:      "weak",
				UpdatedAt: now,
			},
			Score: 1,
		},
		{
			Item: state.MemoryItem{
				ID:         "mem_plan",
				Status:     state.MemoryStatusActive,
				Title:      "Plan",
				Text:       "strong",
				Confidence: 1,
				SourceLinks: []state.MemorySourceLink{{
					SessionID:  "session",
					MessageIDs: []uint{1},
				}},
				UpdatedAt: now.Add(time.Minute),
			},
			Score: 3,
		},
	}, MemoryRerankOptions{})

	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	require.Equal(t, "strong", result.Hits[0].Item.Text)
	require.Equal(t, "mem_plan", result.Hits[0].Item.ID)
}

func TestRerankMemoryHits_RanksStatusConfidenceAndSourceQuality(t *testing.T) {
	now := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	hits := []state.MemorySearchHit{
		{
			Item: state.MemoryItem{
				ID:        "mem_broad",
				Status:    state.MemoryStatusActive,
				Text:      "plan",
				UpdatedAt: now,
			},
			Score: 1,
		},
		{
			Item: state.MemoryItem{
				ID:         "mem_confident",
				Status:     state.MemoryStatusActive,
				Text:       "plan",
				Confidence: 0.9,
				SourceLinks: []state.MemorySourceLink{{
					SessionID:     "session",
					MessageIDs:    []uint{1},
					Offsets:       []int{2},
					SummaryID:     "summary",
					CreatedBy:     "reflection",
					CreatedReason: "preference",
				}},
				UpdatedAt: now,
			},
			Score: 1,
		},
		{
			Item: state.MemoryItem{
				ID:        "mem_candidate",
				Status:    state.MemoryStatusCandidate,
				Text:      "plan",
				UpdatedAt: now,
			},
			Score: 1,
		},
		{
			Item: state.MemoryItem{
				ID:        "mem_superseded",
				Status:    state.MemoryStatusSuperseded,
				Text:      "plan",
				UpdatedAt: now,
			},
			Score: 1,
		},
	}

	result, err := RerankMemoryHits(context.Background(), state.MemorySearchQuery{
		Text: "plan",
		Statuses: []state.MemoryStatus{
			state.MemoryStatusActive,
			state.MemoryStatusCandidate,
			state.MemoryStatusSuperseded,
		},
	}, hits, MemoryRerankOptions{})

	require.NoError(t, err)
	require.Len(t, result.Hits, 4)
	require.Equal(t, []string{"mem_confident", "mem_broad", "mem_candidate", "mem_superseded"}, memoryHitIDs(result))
}

func TestRerankMemoryHits_DefaultsToDeterministicOrderAndLimit(t *testing.T) {
	now := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	result, err := RerankMemoryHits(context.Background(), state.MemorySearchQuery{Text: "plan"}, []state.MemorySearchHit{
		{Item: state.MemoryItem{ID: "mem_c", Status: state.MemoryStatusActive, Text: "plan", UpdatedAt: now}, Score: 1},
		{Item: state.MemoryItem{ID: "mem_a", Status: state.MemoryStatusActive, Text: "plan", UpdatedAt: now}, Score: 1},
		{Item: state.MemoryItem{ID: "mem_b", Status: state.MemoryStatusActive, Text: "plan", UpdatedAt: now}, Score: 1},
	}, MemoryRerankOptions{Limit: 2})

	require.NoError(t, err)
	require.Equal(t, []string{"mem_a", "mem_b"}, memoryHitIDs(result))
}

func TestRerankMemoryHits_UsesFakeRerankerAndFallback(t *testing.T) {
	hits := []state.MemorySearchHit{
		{Item: state.MemoryItem{ID: "mem_a", Status: state.MemoryStatusActive, Text: "plan"}, Score: 1},
		{Item: state.MemoryItem{ID: "mem_b", Status: state.MemoryStatusActive, Text: "plan"}, Score: 1},
	}

	result, err := RerankMemoryHits(context.Background(), state.MemorySearchQuery{Text: "plan"}, hits, MemoryRerankOptions{
		Reranker: fakeReranker{result: RerankResult{
			Reranker: RerankerLLM,
			Items: []RerankItem{
				{CandidateID: StableMemoryItemID("mem_b"), Score: 2},
				{CandidateID: StableMemoryItemID("mem_a"), Score: 1},
			},
		}, name: RerankerLLM},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"mem_b", "mem_a"}, memoryHitIDs(result))
	require.Equal(t, 2.0, result.Hits[0].Score)

	result, err = RerankMemoryHits(context.Background(), state.MemorySearchQuery{Text: "plan"}, hits, MemoryRerankOptions{
		Reranker: fakeReranker{result: RerankResult{
			Reranker: RerankerLLM,
			Items:    []RerankItem{{CandidateID: "missing", Score: 1}},
		}},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"mem_a", "mem_b"}, memoryHitIDs(result))
}

func TestGetMemoryRerankCaller_UsesQueryUseCase(t *testing.T) {
	require.Equal(t, state.MemoryRerankerUseCaseDefault, getMemoryRerankCaller(state.MemorySearchQuery{}))
	require.Equal(t, "memory_reflection", getMemoryRerankCaller(state.MemorySearchQuery{
		RerankerUseCase: " Memory_Reflection ",
	}))
}

func TestRerankMemoryHits_ReturnsRerankerErrors(t *testing.T) {
	result, err := RerankMemoryHits(context.Background(), state.MemorySearchQuery{Text: "plan"}, []state.MemorySearchHit{
		{Item: state.MemoryItem{ID: "mem_a", Status: state.MemoryStatusActive, Text: "plan"}, Score: 1},
	}, MemoryRerankOptions{
		Reranker: fakeReranker{name: "unknown"},
	})

	require.EqualError(t, err, "reranker must be one of: noop, deterministic, llm")
	require.Empty(t, result.Hits)
}

func TestRerankMemoryHits_EdgeCases(t *testing.T) {
	result, err := RerankMemoryHits(context.Background(), state.MemorySearchQuery{}, nil, MemoryRerankOptions{})
	require.NoError(t, err)
	require.Empty(t, result.Hits)

	result, err = RerankMemoryHits(context.Background(), state.MemorySearchQuery{}, []state.MemorySearchHit{
		{Item: state.MemoryItem{ID: "   ", Text: "blank id"}, Score: 1},
	}, MemoryRerankOptions{})
	require.NoError(t, err)
	require.Empty(t, result.Hits)

	result, err = RerankMemoryHits(context.Background(), state.MemorySearchQuery{}, []state.MemorySearchHit{
		{Item: state.MemoryItem{ID: "mem_nan", Status: state.MemoryStatusActive, Text: "nan", Confidence: math.NaN()}, Score: 1},
	}, MemoryRerankOptions{})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)

	require.Equal(t, 10, MemoryResultLimit(0))
	require.Equal(t, 5, MemoryResultLimit(5))
	require.Equal(t, DefaultRerankCandidateLimit, MemoryCandidateLimit(1))
	require.Equal(t, 120, MemoryCandidateLimit(30))
	require.Equal(t, MaxHybridRetrievalCandidateLimit, MemoryCandidateLimit(MaxHybridRetrievalCandidateLimit))
}

func TestFilterMemoryHitsForEvidence(t *testing.T) {
	hits := []state.MemorySearchHit{
		{
			Item:         state.MemoryItem{ID: "mem_lexical"},
			Score:        0.1,
			LexicalScore: 0.1,
		},
		{
			Item:        state.MemoryItem{ID: "mem_vector"},
			Score:       memoryVectorEvidenceThreshold,
			VectorScore: memoryVectorEvidenceThreshold,
		},
		{
			Item:        state.MemoryItem{ID: "mem_weak_vector"},
			Score:       memoryVectorEvidenceThreshold - 0.01,
			VectorScore: memoryVectorEvidenceThreshold - 0.01,
		},
		{
			Item:  state.MemoryItem{ID: "mem_no_evidence"},
			Score: 1,
		},
	}

	filtered := FilterMemoryHitsForEvidence(state.MemorySearchQuery{Text: "plan"}, hits)

	require.Equal(t, []string{"mem_lexical", "mem_vector"}, memoryHitIDs(state.MemorySearchResult{Hits: filtered}))
	require.Equal(t, hits, FilterMemoryHitsForEvidence(state.MemorySearchQuery{}, hits))
}

func TestCompareMemoryHits(t *testing.T) {
	now := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	left := state.MemorySearchHit{
		Item:  state.MemoryItem{ID: "mem_a", Status: state.MemoryStatusActive, Text: "plan", UpdatedAt: now},
		Score: 2,
	}
	right := state.MemorySearchHit{
		Item:  state.MemoryItem{ID: "mem_b", Status: state.MemoryStatusActive, Text: "plan", UpdatedAt: now},
		Score: 1,
	}

	require.Equal(t, -1, compareMemoryHits(left, right))
	require.Equal(t, 1, compareMemoryHits(right, left))

	left.Score = 1
	left.Item.UpdatedAt = now.Add(time.Minute)
	require.Equal(t, -1, compareMemoryHits(left, right))
	require.Equal(t, 1, compareMemoryHits(right, left))

	right.Item.UpdatedAt = left.Item.UpdatedAt
	require.Equal(t, -1, compareMemoryHits(left, right))
	require.Equal(t, 1, compareMemoryHits(right, left))
	require.Equal(t, 0, compareMemoryHits(left, left))
}

func TestMemoryCandidateTextFallsBackToID(t *testing.T) {
	require.Equal(t, "mem_empty", getMemoryCandidateText(state.MemoryItem{ID: " mem_empty "}))
}

func TestMemoryKindBoost(t *testing.T) {
	require.Equal(t, 0.20, memoryKindBoost(state.MemoryKindSemantic, []state.MemoryKind{state.MemoryKindSemantic}))
	require.Zero(t, memoryKindBoost(state.MemoryKindSemantic, []state.MemoryKind{state.MemoryKindProcedural}))
	require.Equal(t, 0.08, memoryKindBoost(state.MemoryKindPinned, nil))
	require.Equal(t, 0.06, memoryKindBoost(state.MemoryKindProcedural, nil))
	require.Equal(t, 0.04, memoryKindBoost(state.MemoryKindSemantic, nil))
	require.Equal(t, 0.02, memoryKindBoost(state.MemoryKindEpisodic, nil))
	require.Zero(t, memoryKindBoost(state.MemoryKind("unknown"), nil))
}

func TestMemoryStatusBoost(t *testing.T) {
	require.Equal(t, 0.30, memoryStatusBoost(state.MemoryStatusActive))
	require.Equal(t, 0.05, memoryStatusBoost(state.MemoryStatusCandidate))
	require.Equal(t, -0.20, memoryStatusBoost(state.MemoryStatusSuperseded))
	require.Equal(t, -1.0, memoryStatusBoost(state.MemoryStatusDeleted))
	require.Zero(t, memoryStatusBoost(state.MemoryStatus("unknown")))
}

func TestMemoryConfidenceBoost(t *testing.T) {
	require.Zero(t, memoryConfidenceBoost(0))
	require.Zero(t, memoryConfidenceBoost(math.NaN()))
	require.Equal(t, 0.20, memoryConfidenceBoost(2))
	require.Equal(t, 0.10, memoryConfidenceBoost(0.5))
}

func TestMemorySourceQualityBoostCapsScore(t *testing.T) {
	links := make([]state.MemorySourceLink, 4)
	for idx := range links {
		links[idx] = state.MemorySourceLink{
			SessionID:     "session",
			MessageIDs:    []uint{1},
			Offsets:       []int{2},
			SummaryID:     "summary",
			CreatedBy:     "reflection",
			CreatedReason: "preference",
		}
	}

	require.Equal(t, 0.25, memorySourceQualityBoost(links))
}

func memoryHitIDs(result state.MemorySearchResult) []string {
	ids := make([]string, 0, len(result.Hits))
	for _, hit := range result.Hits {
		ids = append(ids, hit.Item.ID)
	}

	return ids
}
