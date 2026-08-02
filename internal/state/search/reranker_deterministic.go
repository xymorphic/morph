package search

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/xymorphic/morph/internal/constants"
)

const (
	defaultRerankLexicalWeight = constants.DefaultRerankLexicalWeight
	defaultRerankVectorWeight  = constants.DefaultRerankVectorWeight
	defaultRerankFusedWeight   = constants.DefaultRerankFusedWeight
)

// DeterministicReranker reranks deterministic candidates.
type DeterministicReranker struct{}

func (DeterministicReranker) Name() string {
	return RerankerDeterministic
}

func (DeterministicReranker) Rerank(_ context.Context, req RerankRequest) (RerankResult, error) {
	name := DeterministicReranker{}.Name()
	rerankTraceLogEvent(req, name).Int("candidate_count", len(req.Candidates)).Msg("rerank started")

	candidates, err := limitCandidates(req.Candidates, req.Options.MaxCandidates)
	if err != nil {
		rerankTraceLogEvent(req, name).Err(err).Msg("rerank candidate bound failed")
		return RerankResult{}, err
	}
	if len(candidates) == 0 {
		rerankTraceLogEvent(req, name).Msg("rerank skipped without candidates")
		return RerankResult{Reranker: name}, nil
	}
	for _, candidate := range candidates {
		if err := ValidateCandidate(candidate); err != nil {
			rerankTraceLogEvent(req, name).Err(err).Msg("rerank candidate validation failed")
			return RerankResult{}, err
		}
	}

	weights, err := normalizeRerankWeights(req.Options)
	if err != nil {
		rerankTraceLogEvent(req, name).Err(err).Msg("rerank weight normalization failed")
		return RerankResult{}, err
	}

	lexicalScores := make([]float64, 0, len(candidates))
	vectorScores := make([]float64, 0, len(candidates))
	fusedScores := make([]float64, 0, len(candidates))
	recencyScores := make([]float64, 0, len(candidates))
	for _, candidate := range candidates {
		lexicalScores = append(lexicalScores, candidate.LexicalScore)
		vectorScores = append(vectorScores, candidate.VectorScore)
		fusedScores = append(fusedScores, candidate.FusedScore)
		recencyScores = append(recencyScores, getCandidateRecencyScore(candidate))
	}

	normalizedLexical, _ := NormalizeScores(lexicalScores, weights.LexicalDirection)
	normalizedVector, _ := NormalizeScores(vectorScores, weights.VectorDirection)
	normalizedFused, _ := NormalizeScores(fusedScores, weights.FusedDirection)
	normalizedRecency, _ := NormalizeScores(recencyScores, ScoreHigherIsBetter)

	items := make([]RerankItem, 0, len(candidates))
	for idx, candidate := range candidates {
		score := weights.LexicalWeight*normalizedLexical[idx] +
			weights.VectorWeight*normalizedVector[idx] +
			weights.FusedWeight*normalizedFused[idx] +
			weights.RecencyWeight*normalizedRecency[idx]
		items = append(items, RerankItem{
			CandidateID: candidate.ID,
			Score:       score,
		})
	}

	sort.SliceStable(items, func(i int, j int) bool {
		left := items[i]
		right := items[j]
		if left.Score != right.Score {
			return left.Score > right.Score
		}

		return left.CandidateID < right.CandidateID
	})

	rerankTraceLogEvent(req, name).
		Int("bounded_candidate_count", len(candidates)).
		Int("result_count", len(items)).
		Float64("lexical_weight", weights.LexicalWeight).
		Float64("vector_weight", weights.VectorWeight).
		Float64("fused_weight", weights.FusedWeight).
		Float64("recency_weight", weights.RecencyWeight).
		Msg("rerank completed")

	return RerankResult{Reranker: name, Items: items}, nil
}

func normalizeRerankWeights(opts RerankOptions) (RerankOptions, error) {
	lexicalDirection, err := normalizeScoreDirection(opts.LexicalDirection)
	if err != nil {
		return RerankOptions{}, err
	}
	vectorDirection, err := normalizeScoreDirection(opts.VectorDirection)
	if err != nil {
		return RerankOptions{}, err
	}
	fusedDirection, err := normalizeScoreDirection(opts.FusedDirection)
	if err != nil {
		return RerankOptions{}, err
	}

	weights := RerankOptions{
		LexicalDirection: lexicalDirection,
		VectorDirection:  vectorDirection,
		FusedDirection:   fusedDirection,
		LexicalWeight:    getNonNegativeWeight(opts.LexicalWeight),
		VectorWeight:     getNonNegativeWeight(opts.VectorWeight),
		FusedWeight:      getNonNegativeWeight(opts.FusedWeight),
		RecencyWeight:    getNonNegativeWeight(opts.RecencyWeight),
	}
	total := weights.LexicalWeight + weights.VectorWeight + weights.FusedWeight + weights.RecencyWeight
	if total == 0 {
		weights.LexicalWeight = defaultRerankLexicalWeight
		weights.VectorWeight = defaultRerankVectorWeight
		weights.FusedWeight = defaultRerankFusedWeight
		total = 1
	}

	weights.LexicalWeight = weights.LexicalWeight / total
	weights.VectorWeight = weights.VectorWeight / total
	weights.FusedWeight = weights.FusedWeight / total
	weights.RecencyWeight = weights.RecencyWeight / total

	return weights, nil
}

func normalizeScoreDirection(direction ScoreDirection) (ScoreDirection, error) {
	if direction == ScoreHigherIsBetter {
		return ScoreHigherIsBetter, nil
	}
	if direction == ScoreLowerIsBetter {
		return ScoreLowerIsBetter, nil
	}

	return ScoreHigherIsBetter, errors.New("score direction is not supported")
}

func getNonNegativeWeight(value float64) float64 {
	if value < 0 || !finite(value) {
		return 0
	}

	return value
}

func getCandidateRecencyScore(candidate Candidate) float64 {
	value := candidate.UpdatedAt
	if value.IsZero() {
		value = candidate.CreatedAt
	}
	if value.IsZero() {
		return 0
	}

	return float64(value.UTC().UnixNano()) / float64(time.Second)
}
