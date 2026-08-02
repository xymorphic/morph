package readiness

import (
	"fmt"

	"github.com/xymorphic/morph/internal/config"
)

func buildSearchGroup(cfg *config.Config) Group {
	if cfg == nil {
		return Group{Name: "search", Checks: []Check{check("config", StatusFail, "config is required")}}
	}

	return Group{Name: "search", Checks: []Check{
		check("search", StatusPass, "enabled"),
		buildVectorSearchCheck(cfg),
		buildSearchRerankCheck(cfg),
	}}
}

func buildVectorSearchCheck(cfg *config.Config) Check {
	message := fmt.Sprintf(
		"status=%s, required=%t, rebuildBatchSize=%d, embedding=%s",
		formatEnabled(cfg.Search.Vector.Enabled),
		cfg.Search.Vector.Required,
		cfg.Search.Vector.RebuildBatchSize,
		formatSearchEmbedding(cfg),
	)
	if !cfg.Search.Vector.Enabled {
		return check("vector", StatusWarn, message)
	}

	status := StatusPass
	var actions []Action
	if cfg.Search.Vector.Required {
		if _, err := cfg.ResolveEmbeddingModelAuth(); err != nil {
			status = StatusFail
			message = fmt.Sprintf(
				"status=enabled, required=true, auth=missing for provider %q, rebuildBatchSize=%d, embedding=%q/%q",
				cfg.ModelEmbeddingProviderEffective(),
				cfg.Search.Vector.RebuildBatchSize,
				cfg.ModelEmbeddingProviderEffective(),
				cfg.Models.Embedding.Name,
			)
			if isMissingAuthError(err) {
				actions = append(actions, providerAPIKeyActions(cfg.ModelEmbeddingProviderEffective())...)
			}
		}
	}

	return check("vector", status, message, actions...)
}

func formatSearchEmbedding(cfg *config.Config) string {
	if cfg == nil || cfg.Models.Embedding.Name == "" {
		return "not configured"
	}

	return fmt.Sprintf("%q/%q", cfg.ModelEmbeddingProviderEffective(), cfg.Models.Embedding.Name)
}

func buildSearchRerankCheck(cfg *config.Config) Check {
	enabled := searchRerankEnabled(cfg)
	reranker := cfg.RerankerOverrideEffective(config.RerankerOverrideConfig{})
	message := fmt.Sprintf(
		"status=%s, type=%q, maxCandidates=%d, maxCandidateTextChars=%d, maxOutputTokens=%d",
		formatEnabled(enabled),
		reranker.Type,
		reranker.MaxCandidates,
		reranker.MaxCandidateTextChars,
		reranker.MaxOutputTokens,
	)
	if !enabled {
		return check("rerank", StatusWarn, message)
	}

	return check("rerank", StatusPass, message)
}

func searchRerankEnabled(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}

	enabled := true
	if cfg.Reranker.Enabled != nil {
		enabled = *cfg.Reranker.Enabled
	}
	if cfg.Search.EnableRerank != nil && !*cfg.Search.EnableRerank {
		enabled = false
	}

	return enabled
}
