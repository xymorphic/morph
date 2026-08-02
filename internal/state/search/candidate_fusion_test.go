package search

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	state "github.com/xymorphic/morph/internal/state/core"
)

func TestSearchSharedRankingAndFilters(t *testing.T) {
	require.False(t, MessageIndexRowMatchesSearchOptions(MessageIndexRow{ToolName: "process"}, state.SearchMessageOptions{ToolName: "search_files"}))
	require.True(t, MessageIndexRowMatchesSearchOptions(MessageIndexRow{ToolName: "process"}, state.SearchMessageOptions{ToolName: " process "}))

	require.Equal(t, DefaultHybridRetrievalCandidateLimit, HybridRetrievalCandidateLimit(state.SearchMessageOptions{}))
	require.Equal(t, 120, HybridRetrievalCandidateLimit(state.SearchMessageOptions{
		MaxSessions:           12,
		MaxMessagesPerSession: 10,
	}))
	require.Equal(t, MaxHybridRetrievalCandidateLimit, HybridRetrievalCandidateLimit(state.SearchMessageOptions{
		MaxSessions:           MaxHybridRetrievalCandidateLimit,
		MaxMessagesPerSession: MaxHybridRetrievalCandidateLimit,
	}))

	require.Equal(t, float64(0), FusedCandidateScore(false, 0, false, 0))
	require.Greater(t, FusedCandidateScore(true, 1, true, 2), float64(0))
	require.Equal(t, 9.0, CandidateRankingScore(true, 9, 1))
	require.Equal(t, 1.0, CandidateRankingScore(false, 9, 1))

	now := time.Now().UTC()
	older := now.Add(-time.Minute)
	require.Equal(t, -1, CompareCandidateOrder(2, 1, now, now, "a", "a", 1, 1))
	require.Equal(t, 1, CompareCandidateOrder(1, 2, now, now, "a", "a", 1, 1))
	require.Equal(t, -1, CompareCandidateOrder(1, 1, now, older, "a", "a", 1, 1))
	require.Equal(t, 1, CompareCandidateOrder(1, 1, older, now, "a", "a", 1, 1))
	require.Equal(t, -1, CompareCandidateOrder(1, 1, now, now, "a", "b", 1, 1))
	require.Equal(t, 1, CompareCandidateOrder(1, 1, now, now, "b", "a", 1, 1))
	require.Equal(t, -1, CompareCandidateOrder(1, 1, now, now, "a", "a", 2, 1))
	require.Equal(t, 1, CompareCandidateOrder(1, 1, now, now, "a", "a", 1, 2))
	require.Equal(t, 0, CompareCandidateOrder(1, 1, now, now, "a", "a", 1, 1))
}

func TestSearchCandidateSet_MergeAndSorted(t *testing.T) {
	candidates := SearchCandidateSet[string, *testSearchCandidate]{
		"lexical": {
			match: &CandidateMatch{
				SessionID:   "ses_a",
				LexicalRank: 1,
				HasLexical:  true,
			},
			id: "lexical",
		},
		"empty": {id: "empty"},
		"keep": {
			id: "keep",
			match: &CandidateMatch{
				MatchedText: "lexical text",
			},
		},
	}

	candidates.Merge([]*testSearchCandidate{
		nil,
		{id: "ignored"},
		{
			id: "lexical",
			match: &CandidateMatch{
				MatchedText:     "vector text",
				MatchedToolName: "tool",
				VectorScore:     0.7,
				VectorRank:      2,
			},
		},
		{
			id: "vector",
			match: &CandidateMatch{
				SessionID:  "ses_b",
				VectorRank: 1,
				HasVector:  true,
			},
		},
		{
			id: "empty",
			match: &CandidateMatch{
				VectorRank: 3,
			},
		},
		{
			id: "keep",
			match: &CandidateMatch{
				MatchedText: "vector replacement",
				VectorRank:  4,
			},
		},
	}, func(candidate *testSearchCandidate) string {
		if candidate == nil {
			return ""
		}

		return candidate.id
	})

	require.Len(t, candidates, 4)
	require.True(t, candidates["lexical"].match.HasVector)
	require.Equal(t, 0.7, candidates["lexical"].match.VectorScore)
	require.Equal(t, "vector text", candidates["lexical"].match.MatchedText)
	require.Equal(t, "lexical text", candidates["keep"].match.MatchedText)
	require.Contains(t, candidates, "vector")
	require.NotContains(t, candidates, "ignored")

	items := candidates.Sorted(func(left *testSearchCandidate, right *testSearchCandidate) bool {
		return left.id < right.id
	})
	require.Len(t, items, 3)
	require.Equal(t, "keep", items[0].id)
	require.Greater(t, candidates["lexical"].match.FusedScore, 0.0)
	require.Greater(t, candidates["vector"].match.FusedScore, 0.0)
}

type testSearchCandidate struct {
	match *CandidateMatch
	id    string
}

func (c *testSearchCandidate) CandidateMatchRef() *CandidateMatch {
	if c == nil {
		return nil
	}

	return c.match
}
