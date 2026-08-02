package search

import (
	"context"

	"github.com/xymorphic/morph/pkg/str"
)

// UseCaseReranker reranks use case candidates.
type UseCaseReranker struct {
	Default  Reranker
	Override map[string]Reranker
	Fallback Reranker
}

func (r UseCaseReranker) Name() string {
	return getRerankerOrDefault(r.Default).Name()
}

func (r UseCaseReranker) Rerank(ctx context.Context, req RerankRequest) (RerankResult, error) {
	reranker := getRerankerOrDefault(r.Default)
	if override := r.getOverride(req.Caller); override != nil {
		reranker = override
	}

	return RerankWithFallback(ctx, reranker, getRerankerOrDefault(r.Fallback), req)
}

func (r UseCaseReranker) getOverride(useCase string) Reranker {
	useCaseValue := str.String(useCase)
	useCase = useCaseValue.Normalized()
	if useCase == "" || len(r.Override) == 0 {
		return nil
	}

	return r.Override[useCase]
}

func getRerankerOrDefault(reranker Reranker) Reranker {
	if reranker == nil {
		return DeterministicReranker{}
	}

	return reranker
}
