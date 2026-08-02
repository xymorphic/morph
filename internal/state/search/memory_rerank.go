package search

import (
	"context"
	"slices"
	"strings"

	state "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/pkg/str"
)

// MemoryRerankOptions controls memory rerank.
type MemoryRerankOptions struct {
	Reranker      Reranker
	MaxCandidates int
	Limit         int
}

const memoryVectorEvidenceThreshold = 0.75

// FilterMemoryHitsForEvidence keeps memory hits suitable for evidence in a model prompt.
func FilterMemoryHitsForEvidence(
	query state.MemorySearchQuery,
	hits []state.MemorySearchHit,
) []state.MemorySearchHit {
	textValue := str.String(query.Text)
	if textValue.Trim() == "" {
		return hits
	}

	filtered := make([]state.MemorySearchHit, 0, len(hits))
	for _, hit := range hits {
		if hit.LexicalScore > 0 || hit.VectorScore >= memoryVectorEvidenceThreshold {
			filtered = append(filtered, hit)
		}
	}

	return filtered
}

// RerankMemoryHits reranks memory hits for the supplied query.
func RerankMemoryHits(
	ctx context.Context,
	query state.MemorySearchQuery,
	hits []state.MemorySearchHit,
	opts MemoryRerankOptions,
) (state.MemorySearchResult, error) {
	hits = dedupeMemoryHits(hits)
	if len(hits) == 0 {
		return state.MemorySearchResult{}, nil
	}

	candidates := make([]Candidate, 0, len(hits))
	hitByCandidateID := make(map[string]state.MemorySearchHit, len(hits))
	for _, hit := range hits {
		candidate := memoryCandidate(query, hit)
		candidates = append(candidates, candidate)
		hitByCandidateID[candidate.ID] = hit
	}

	reranker := opts.Reranker
	if reranker == nil {
		reranker = DeterministicReranker{}
	}

	result, err := RerankWithFallback(ctx, reranker, DeterministicReranker{}, RerankRequest{
		Query:      query.Text,
		Caller:     getMemoryRerankCaller(query),
		SourceKind: SourceKindMemoryItem,
		Candidates: candidates,
		Options: RerankOptions{
			MaxCandidates: opts.MaxCandidates,
			LexicalWeight: 0.65,
			FusedWeight:   0.25,
			RecencyWeight: 0.10,
		},
	})
	if err != nil {
		return state.MemorySearchResult{}, err
	}

	reranked := make([]state.MemorySearchHit, 0, len(result.Items))
	for _, item := range result.Items {
		hit := hitByCandidateID[item.CandidateID]
		hit.Score = item.Score
		reranked = append(reranked, hit)
	}
	if opts.Limit > 0 && len(reranked) > opts.Limit {
		reranked = reranked[:opts.Limit]
	}

	return state.MemorySearchResult{Hits: reranked}, nil
}

func getMemoryRerankCaller(query state.MemorySearchQuery) string {
	rerankerUseCaseValue := str.String(query.RerankerUseCase)
	if caller := rerankerUseCaseValue.Normalized(); caller != "" {
		return caller
	}

	return state.MemoryRerankerUseCaseDefault
}

// MemoryResultLimit returns the configured memory result limit with defaults applied.
func MemoryResultLimit(limit int) int {
	if limit <= 0 {
		return 10
	}

	return limit
}

// MemoryCandidateLimit returns the configured memory candidate limit with defaults applied.
func MemoryCandidateLimit(resultLimit int) int {
	resultLimit = MemoryResultLimit(resultLimit)
	candidateLimit := resultLimit * 4
	if candidateLimit < DefaultRerankCandidateLimit {
		return DefaultRerankCandidateLimit
	}
	if candidateLimit > MaxHybridRetrievalCandidateLimit {
		return MaxHybridRetrievalCandidateLimit
	}

	return candidateLimit
}

func dedupeMemoryHits(hits []state.MemorySearchHit) []state.MemorySearchHit {
	if len(hits) == 0 {
		return nil
	}

	byID := make(map[string]state.MemorySearchHit, len(hits))
	for _, hit := range hits {
		iDValue := str.String(hit.Item.ID)
		id := iDValue.Trim()
		if id == "" {
			continue
		}

		current, ok := byID[id]
		if !ok || compareMemoryHits(hit, current) < 0 {
			byID[id] = hit
		}
	}

	candidates := make([]Candidate, 0, len(byID))
	hitByCandidateID := make(map[string]state.MemorySearchHit, len(byID))
	for _, hit := range byID {
		candidate := memoryCandidate(state.MemorySearchQuery{}, hit)
		candidates = append(candidates, candidate)
		hitByCandidateID[candidate.ID] = hit
	}
	SortCandidates(candidates)

	deduped := make([]state.MemorySearchHit, 0, len(candidates))
	for _, candidate := range candidates {
		deduped = append(deduped, hitByCandidateID[candidate.ID])
	}

	return deduped
}

func compareMemoryHits(left state.MemorySearchHit, right state.MemorySearchHit) int {
	leftCandidate := memoryCandidate(state.MemorySearchQuery{}, left)
	rightCandidate := memoryCandidate(state.MemorySearchQuery{}, right)
	if leftCandidate.FusedScore != rightCandidate.FusedScore {
		if leftCandidate.FusedScore > rightCandidate.FusedScore {
			return -1
		}

		return 1
	}
	if !left.Item.UpdatedAt.Equal(right.Item.UpdatedAt) {
		if left.Item.UpdatedAt.After(right.Item.UpdatedAt) {
			return -1
		}

		return 1
	}
	if left.Item.ID < right.Item.ID {
		return -1
	}
	if left.Item.ID > right.Item.ID {
		return 1
	}
	return 0
}

func memoryCandidate(query state.MemorySearchQuery, hit state.MemorySearchHit) Candidate {
	item := hit.Item
	lexicalScore := hit.LexicalScore
	vectorScore := hit.VectorScore
	if lexicalScore == 0 && vectorScore == 0 {
		lexicalScore = hit.Score
	}

	return Candidate{
		ID:           StableMemoryItemID(item.ID),
		SourceKind:   SourceKindMemoryItem,
		MemoryID:     item.ID,
		Text:         getMemoryCandidateText(item),
		LexicalScore: lexicalScore,
		VectorScore:  vectorScore,
		FusedScore:   getMemoryCandidateFusedScore(query, hit),
		CreatedAt:    item.CreatedAt,
		UpdatedAt:    item.UpdatedAt,
		Metadata: map[string]string{
			"kind":   string(item.Kind),
			"status": string(item.Status),
		},
	}
}

func getMemoryCandidateText(item state.MemoryItem) string {
	joinValue := str.String(strings.Join([]string{item.Title, item.Text}, "\n"))
	text := joinValue.Trim()
	if text != "" {
		return text
	}
	iDValue2 := str.String(item.ID)
	return iDValue2.Trim()
}

func getMemoryCandidateFusedScore(query state.MemorySearchQuery, hit state.MemorySearchHit) float64 {
	item := hit.Item
	score := hit.Score
	score += memoryKindBoost(item.Kind, query.Kinds)
	score += memoryStatusBoost(item.Status)
	score += memoryConfidenceBoost(item.Confidence)
	score += memorySourceQualityBoost(item.SourceLinks)
	return score
}

func memoryKindBoost(kind state.MemoryKind, queryKinds []state.MemoryKind) float64 {
	if len(queryKinds) > 0 {
		if slices.Contains(queryKinds, kind) {
			return 0.20
		}

		return 0
	}

	switch kind {
	case state.MemoryKindPinned:
		return 0.08
	case state.MemoryKindProcedural:
		return 0.06
	case state.MemoryKindSemantic:
		return 0.04
	case state.MemoryKindEpisodic:
		return 0.02
	default:
		return 0
	}
}

func memoryStatusBoost(status state.MemoryStatus) float64 {
	switch status {
	case state.MemoryStatusActive:
		return 0.30
	case state.MemoryStatusCandidate:
		return 0.05
	case state.MemoryStatusSuperseded:
		return -0.20
	case state.MemoryStatusDeleted:
		return -1
	default:
		return 0
	}
}

func memoryConfidenceBoost(confidence float64) float64 {
	if confidence <= 0 || !finite(confidence) {
		return 0
	}

	if confidence > 1 {
		confidence = 1
	}
	return confidence * 0.20
}

func memorySourceQualityBoost(links []state.MemorySourceLink) float64 {
	if len(links) == 0 {
		return 0
	}

	score := 0.0
	for _, link := range links {
		sessionIDValue := str.String(link.SessionID)
		if sessionIDValue.Trim() != "" {
			score += 0.04
		}
		if len(link.MessageIDs) > 0 {
			score += 0.04
		}
		if len(link.Offsets) > 0 {
			score += 0.02
		}
		summaryIDValue := str.String(link.SummaryID)
		if summaryIDValue.Trim() != "" {
			score += 0.02
		}
		createdByValue := str.String(link.CreatedBy)
		createdReasonValue := str.String(link.CreatedReason)
		if createdByValue.Trim() != "" || createdReasonValue.Trim() != "" {
			score += 0.02
		}
	}
	if score > 0.25 {
		return 0.25
	}
	return score
}
