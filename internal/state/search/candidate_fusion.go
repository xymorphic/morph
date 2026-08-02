package search

import (
	"sort"
	"strings"
	"time"

	"github.com/xymorphic/morph/internal/constants"
	state "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/pkg/str"
)

const (
	DefaultHybridRetrievalCandidateLimit = constants.DefaultHybridRetrievalCandidateLimit
	MaxHybridRetrievalCandidateLimit     = constants.MaxHybridRetrievalCandidateLimit
	DefaultRerankCandidateLimit          = constants.DefaultRerankCandidateLimit
	ReciprocalRankFusionConstant         = constants.ReciprocalRankFusionConstant
)

// CandidateMatch represents one matched ranking metadata for a lexical, vector, or reranked search candidate result.
type CandidateMatch struct {
	SessionID       string
	MatchedText     string
	MatchedToolName string
	LexicalScore    float64
	RerankScore     float64
	VectorScore     float64
	FusedScore      float64
	LexicalRank     int
	VectorRank      int
	HasLexical      bool
	HasRerank       bool
	HasVector       bool
}

// SearchCandidate exposes mutable ranking metadata for shared candidate set helpers.
type SearchCandidate interface {
	CandidateMatchRef() *CandidateMatch
}

// SearchCandidateSet describes candidates keyed by their stable source identifier.
type SearchCandidateSet[K comparable, C SearchCandidate] map[K]C

// Merge folds vector candidates into the set, preserving lexical metadata when present.
func (candidates SearchCandidateSet[K, C]) Merge(vectorCandidates []C, keyForCandidate func(C) K) {
	for _, vectorCandidate := range vectorCandidates {
		vectorMatch := vectorCandidate.CandidateMatchRef()
		if vectorMatch == nil {
			continue
		}

		key := keyForCandidate(vectorCandidate)
		candidate, ok := candidates[key]
		if !ok {
			candidates[key] = vectorCandidate
			continue
		}

		match := candidate.CandidateMatchRef()
		if match == nil {
			continue
		}

		match.VectorScore = vectorMatch.VectorScore
		match.VectorRank = vectorMatch.VectorRank
		match.HasVector = true
		matchedTextValue := str.String(match.MatchedText)
		if matchedTextValue.Trim() == "" {
			match.MatchedText = vectorMatch.MatchedText
			match.MatchedToolName = vectorMatch.MatchedToolName
		}
	}
}

// Sorted computes fused scores and returns candidates ordered by the supplied comparator.
func (candidates SearchCandidateSet[K, C]) Sorted(less func(C, C) bool) []C {
	items := make([]C, 0, len(candidates))
	for _, candidate := range candidates {
		match := candidate.CandidateMatchRef()
		if match == nil {
			continue
		}

		match.FusedScore = FusedCandidateScore(
			match.HasLexical,
			match.LexicalRank,
			match.HasVector,
			match.VectorRank,
		)
		items = append(items, candidate)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return less(items[i], items[j])
	})

	return items
}

// HybridRetrievalCandidateLimit returns the lexical/vector candidate limit for a search request.
func HybridRetrievalCandidateLimit(opts state.SearchMessageOptions) int {
	limit := DefaultHybridRetrievalCandidateLimit
	if opts.MaxSessions > 0 && opts.MaxMessagesPerSession > 0 {
		limit = max(limit, opts.MaxSessions*opts.MaxMessagesPerSession)
	}
	if limit > MaxHybridRetrievalCandidateLimit {
		return MaxHybridRetrievalCandidateLimit
	}

	return limit
}

// FusedCandidateScore combines lexical and vector ranks with reciprocal rank fusion.
func FusedCandidateScore(
	hasLexical bool,
	lexicalRank int,
	hasVector bool,
	vectorRank int,
) float64 {
	var score float64
	if hasLexical && lexicalRank > 0 {
		score += 1 / float64(ReciprocalRankFusionConstant+lexicalRank)
	}
	if hasVector && vectorRank > 0 {
		score += 1 / float64(ReciprocalRankFusionConstant+vectorRank)
	}

	return score
}

// CandidateRankingScore returns the score used for final candidate ordering.
func CandidateRankingScore(hasRerank bool, rerankScore float64, fusedScore float64) float64 {
	if hasRerank {
		return rerankScore
	}

	return fusedScore
}

// CompareCandidateOrder orders candidates by score, recency, session ID, and message ID.
func CompareCandidateOrder(
	leftScore float64,
	rightScore float64,
	leftCreatedAt time.Time,
	rightCreatedAt time.Time,
	leftSessionID string,
	rightSessionID string,
	leftMessageID uint,
	rightMessageID uint,
) int {
	if leftScore != rightScore {
		if leftScore > rightScore {
			return -1
		}

		return 1
	}
	if !leftCreatedAt.Equal(rightCreatedAt) {
		if leftCreatedAt.After(rightCreatedAt) {
			return -1
		}

		return 1
	}
	if leftSessionID != rightSessionID {
		return strings.Compare(leftSessionID, rightSessionID)
	}
	if leftMessageID > rightMessageID {
		return -1
	}
	if leftMessageID < rightMessageID {
		return 1
	}

	return 0
}
