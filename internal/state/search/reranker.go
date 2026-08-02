package search

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog"

	"github.com/xymorphic/morph/internal/constants"
	"github.com/xymorphic/morph/pkg/logutils"
	"github.com/xymorphic/morph/pkg/str"
)

var retrievalLog = logutils.Module("storage.retrieval")

const (
	RerankerNoop          = constants.RerankerNoop
	RerankerDeterministic = constants.RerankerDeterministic
	RerankerLLM           = constants.RerankerLLM
)

// Reranker orders candidate items for a query.
type Reranker interface {
	Name() string
	Rerank(context.Context, RerankRequest) (RerankResult, error)
}

// RerankRequest describes a rerank request.
type RerankRequest struct {
	Options    RerankOptions
	Query      string
	Caller     string
	TraceID    string
	SourceKind SourceKind
	Candidates []Candidate
}

// RerankOptions controls ranking directions, weights, and candidate limits.
type RerankOptions struct {
	LexicalDirection ScoreDirection
	VectorDirection  ScoreDirection
	FusedDirection   ScoreDirection
	MaxCandidates    int
	LexicalWeight    float64
	VectorWeight     float64
	FusedWeight      float64
	RecencyWeight    float64
}

// RerankResult contains the ordered output from a reranker.
type RerankResult struct {
	Reranker string
	Items    []RerankItem
}

// RerankItem represents one rerank item.
type RerankItem struct {
	CandidateID string
	Score       float64
}

// RerankWithFallback reranks candidates and falls back to original order on failure.
func RerankWithFallback(
	ctx context.Context,
	primary Reranker,
	fallback Reranker,
	req RerankRequest) (RerankResult, error) {
	if primary == nil {
		rerankDebugLogEvent(req, "fallback").Msg("rerank primary missing, using fallback")
		return rerankFallback(ctx, fallback, req)
	}
	if err := ValidateReranker(primary); err != nil {
		return RerankResult{}, err
	}

	if fallback != nil {
		if err := ValidateReranker(fallback); err != nil {
			return RerankResult{}, err
		}
	}

	result, err := primary.Rerank(ctx, req)
	if err != nil {
		rerankDebugLogEvent(req, "fallback").Err(err).Msg("rerank primary failed, using fallback")
		return rerankFallback(ctx, fallback, req)
	}
	candidates, err := limitCandidates(req.Candidates, req.Options.MaxCandidates)
	if err != nil {
		rerankDebugLogEvent(req, "fallback").Err(err).Msg("rerank result candidate bound failed, using fallback")
		return rerankFallback(ctx, fallback, req)
	}
	if err := ValidateRerankResult(candidates, result); err != nil {
		rerankDebugLogEvent(req, "fallback").Err(err).Msg("rerank primary result rejected, using fallback")
		return rerankFallback(ctx, fallback, req)
	}
	rerankerValue := str.String(result.Reranker)
	if rerankerValue.Trim() == "" {
		result.Reranker = primary.Name()
	}

	rerankTraceLogEvent(req, "primary").Int("result_count", len(result.Items)).Msg("rerank primary result accepted")

	return result, nil
}

// ValidateRerankResult checks that reranker output is complete and well-formed.
func ValidateRerankResult(candidates []Candidate, result RerankResult) error {
	if len(candidates) == 0 {
		if len(result.Items) != 0 {
			return errors.New("rerank result must be empty when candidates are empty")
		}

		return nil
	}
	if len(result.Items) == 0 {
		return errors.New("rerank result is empty")
	}
	if len(result.Items) > len(candidates) {
		return errors.New("rerank result cannot contain more items than candidates")
	}

	knownIDs := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		knownIDs[candidate.ID] = struct{}{}
	}

	seenIDs := make(map[string]struct{}, len(result.Items))
	for _, item := range result.Items {
		candidateIDValue := str.String(item.CandidateID)
		candidateID := candidateIDValue.Trim()
		if candidateID == "" {
			return errors.New("rerank item candidate id is required")
		}
		if candidateID != item.CandidateID {
			return errors.New("rerank item candidate id must be trimmed")
		}
		if !finite(item.Score) {
			return errors.New("rerank item score must be finite")
		}
		if _, ok := knownIDs[candidateID]; !ok {
			return fmt.Errorf("rerank item candidate id %q is unknown", candidateID)
		}
		if _, ok := seenIDs[candidateID]; ok {
			return fmt.Errorf("rerank item candidate id %q is duplicated", candidateID)
		}
		seenIDs[candidateID] = struct{}{}
	}

	return nil
}

func limitCandidates(candidates []Candidate, maxCandidates int) ([]Candidate, error) {
	if maxCandidates < 0 {
		return nil, errors.New("max candidates must be greater than or equal to zero")
	}
	if maxCandidates == 0 || len(candidates) <= maxCandidates {
		return candidates, nil
	}

	return candidates[:maxCandidates], nil
}

func rerankFallback(ctx context.Context, fallback Reranker, req RerankRequest) (RerankResult, error) {
	if fallback == nil {
		fallback = DeterministicReranker{}
	}
	if err := ValidateReranker(fallback); err != nil {
		return RerankResult{}, err
	}

	result, err := fallback.Rerank(ctx, req)
	rerankerValue2 := str.String(result.Reranker)
	if rerankerValue2.Trim() == "" {
		result.Reranker = fallback.Name()
	}

	return result, err
}

// ValidateReranker checks that a reranker behaves consistently for valid requests.
func ValidateReranker(reranker Reranker) error {
	if reranker == nil {
		return nil
	}

	nameValue := str.String(reranker.Name())
	switch nameValue.Normalized() {
	case RerankerNoop, RerankerDeterministic, RerankerLLM:
		return nil
	default:
		return errors.New("reranker must be one of: noop, deterministic, llm")
	}
}

func rerankTraceLogEvent(req RerankRequest, reranker string) *zerolog.Event {
	return rerankBaseLogEvent(retrievalLog.Trace(), req, reranker)
}

func rerankDebugLogEvent(req RerankRequest, reranker string) *zerolog.Event {
	return rerankBaseLogEvent(retrievalLog.Debug(), req, reranker)
}

func rerankBaseLogEvent(event *zerolog.Event, req RerankRequest, reranker string) *zerolog.Event {
	callerValue := str.String(req.Caller)
	traceIDValue := str.String(req.TraceID)
	sourceKindValue := str.String(string(req.SourceKind))
	event = event.
		Str("reranker", reranker).
		Str("caller", callerValue.Trim()).
		Str("trace_id", traceIDValue.Trim()).
		Str("source_kind", sourceKindValue.Trim()).
		Int("candidate_count", len(req.Candidates)).
		Int("max_candidates", req.Options.MaxCandidates)
	queryValue := str.String(req.Query)
	if query := queryValue.Trim(); query != "" {
		event = event.Int("query_chars", len([]rune(query)))
	}

	return event
}
