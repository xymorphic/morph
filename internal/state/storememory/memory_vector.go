package storememory

import (
	"context"
	"fmt"
	"sort"
	"strings"

	statememory "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/internal/state/search"
	"github.com/xymorphic/morph/pkg/str"
)

func (s *Store) memoryVectorSearchEnabled(query statememory.MemorySearchQuery) bool {
	textValue := str.String(query.Text)
	return s != nil && s.vectors != nil && textValue.Trim() != ""
}

func (s *Store) searchMemoryHybrid(
	ctx context.Context,
	query statememory.MemorySearchQuery,
) (statememory.MemorySearchResult, error) {
	resultLimit := search.MemoryResultLimit(query.Limit)
	candidateLimit := search.MemoryCandidateLimit(resultLimit)

	lexicalHits, reranker := s.memoryLexicalHits(query, candidateLimit)
	vectorHits, err := s.searchMemoryVector(ctx, query, candidateLimit)
	if err != nil {
		if requiredErr := s.handleVectorStoreError(err); requiredErr != nil {
			return statememory.MemorySearchResult{}, requiredErr
		}

		vectorHits = nil
	}

	hits := search.FilterMemoryHitsForEvidence(query, mergeMemoryHits(lexicalHits, vectorHits))
	return search.RerankMemoryHits(ctx, query, hits, search.MemoryRerankOptions{
		Reranker:      reranker,
		MaxCandidates: candidateLimit,
		Limit:         resultLimit,
	})
}

func (s *Store) memoryLexicalHits(
	query statememory.MemorySearchQuery,
	limit int,
) ([]statememory.MemorySearchHit, search.Reranker) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	hits := make([]statememory.MemorySearchHit, 0, len(s.memoryItems))
	for _, item := range s.memoryItems {
		if !statememory.CheckMemoryMatchesQuery(item, query) {
			continue
		}

		score := statememory.GetSimpleMemoryScore(item, query.Text)
		hits = append(hits, statememory.MemorySearchHit{
			Item:         item.Clone(),
			Score:        score,
			LexicalScore: score,
		})
	}
	sortMemoryHits(hits)
	if len(hits) > limit {
		hits = hits[:limit]
	}

	return hits, s.memoryReranker
}

func (s *Store) searchMemoryVector(
	ctx context.Context,
	query statememory.MemorySearchQuery,
	limit int,
) ([]statememory.MemorySearchHit, error) {
	modelValue := str.String(s.vectors.Model)
	textValue2 := str.String(query.Text)
	req := search.EmbeddingRequest{
		Model:        modelValue.Trim(),
		Relationship: "query_vector_for_memory_item_retrieval",
		Target:       "memory_item_vectors",
		Inputs: []search.EmbeddingInput{{
			ID:         "query",
			Text:       textValue2.Trim(),
			SourceKind: search.SourceKindMemoryItem,
		}},
	}

	embedding, err := s.vectors.Provider.Embed(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := search.ValidateEmbeddingResult(req, embedding); err != nil {
		return nil, err
	}

	sourceIDs, filtered := s.memoryVectorSourceIDs(query, limit)
	if filtered && len(sourceIDs) == 0 {
		return nil, nil
	}
	filterTags := getMemoryVectorFilterTags(query)
	filterTagGroups := getMemoryVectorFilterTagGroups(query)
	modelValue2 := str.String(s.vectors.Model)
	result, err := s.vectors.Store.Search(ctx, search.VectorSearchRequest{
		EmbeddingModel: modelValue2.Trim(),
		Dimensions:     embedding.Dimensions,
		QueryVector:    embedding.Items[0].Vector,
		Limit:          limit,
		Filter: search.VectorFilter{
			SourceKind: search.SourceKindMemoryItem,
			SourceIDs:  sourceIDs,
			Tags:       filterTags,
			TagGroups:  filterTagGroups,
		},
	})
	if err != nil {
		return nil, err
	}

	return s.memoryVectorMatchesToHits(query, result.Matches), nil
}

func (s *Store) memoryVectorSourceIDs(query statememory.MemorySearchQuery, limit int) ([]string, bool) {
	if !checkMemoryQueryNeedsSourceIDFilter(query) {
		return nil, false
	}

	filterQuery := query
	filterQuery.Text = ""

	s.mu.RLock()
	defer s.mu.RUnlock()

	sourceIDs := make([]string, 0, len(s.memoryItems))
	for _, item := range s.memoryItems {
		if !statememory.CheckMemoryMatchesQuery(item, filterQuery) {
			continue
		}

		sourceIDs = append(sourceIDs, search.StableMemoryItemID(item.ID))
	}
	sort.Strings(sourceIDs)
	if len(sourceIDs) > limit {
		sourceIDs = sourceIDs[:limit]
	}
	return sourceIDs, true
}

func (s *Store) memoryVectorMatchesToHits(
	query statememory.MemorySearchQuery,
	matches []search.VectorSearchMatch,
) []statememory.MemorySearchHit {
	if len(matches) == 0 {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	filterQuery := query
	filterQuery.Text = ""
	hits := make([]statememory.MemorySearchHit, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		memoryID, ok := search.MemoryIDFromSourceID(match.Record.SourceID)
		if !ok {
			continue
		}
		if _, ok := seen[memoryID]; ok {
			continue
		}
		item, ok := s.memoryItems[memoryID]
		if !ok || !statememory.CheckMemoryMatchesQuery(item, filterQuery) {
			continue
		}
		hits = append(hits, statememory.MemorySearchHit{
			Item:        item.Clone(),
			Score:       match.Score,
			VectorScore: match.Score,
		})
		seen[memoryID] = struct{}{}
	}

	return hits
}

func (s *Store) indexMemoryVector(ctx context.Context, item statememory.MemoryItem) error {
	if s == nil || s.vectors == nil {
		return nil
	}

	text := getMemoryVectorText(item)
	if text == "" {
		return nil
	}
	modelValue3 := str.String(s.vectors.Model)
	req := search.EmbeddingRequest{
		Model:        modelValue3.Trim(),
		Relationship: "memory_item_to_memory_vector_index",
		Target:       "memory_item_vectors",
		Inputs: []search.EmbeddingInput{{
			ID:         search.StableMemoryItemID(item.ID),
			Text:       text,
			SourceKind: search.SourceKindMemoryItem,
		}},
	}

	result, err := s.vectors.Provider.Embed(ctx, req)
	if err != nil {
		return err
	}
	if err := search.ValidateEmbeddingResult(req, result); err != nil {
		return err
	}

	embedding := result.Items[0]
	return s.vectors.Store.Upsert(ctx, []search.VectorRecord{{
		ID:             embedding.ID,
		SourceKind:     search.SourceKindMemoryItem,
		SourceID:       search.StableMemoryItemID(item.ID),
		SessionID:      search.MemoryVectorSessionID(item),
		Tags:           search.MemoryVectorTags(item),
		EmbeddingModel: result.Model,
		ContentHash:    embedding.ContentHash,
		Vector:         embedding.Vector,
		Dimensions:     result.Dimensions,
		CreatedAt:      item.CreatedAt,
		UpdatedAt:      item.UpdatedAt,
	}})
}

func (s *Store) deleteMemoryVector(ctx context.Context, memoryID string) error {
	if s == nil || s.vectors == nil {
		return nil
	}

	memoryIDValue := str.String(memoryID)
	memoryID = memoryIDValue.Trim()
	if memoryID == "" {
		return nil
	}

	return s.vectors.Store.Delete(ctx, search.VectorDeleteRequest{
		SourceKind: search.SourceKindMemoryItem,
		SourceIDs:  []string{search.StableMemoryItemID(memoryID)},
	})
}

func mergeMemoryHits(
	lexicalHits []statememory.MemorySearchHit,
	vectorHits []statememory.MemorySearchHit,
) []statememory.MemorySearchHit {
	byID := make(map[string]statememory.MemorySearchHit, len(lexicalHits)+len(vectorHits))
	for _, hit := range lexicalHits {
		iDValue := str.String(hit.Item.ID)
		id := iDValue.Trim()
		if id != "" {
			byID[id] = hit
		}
	}
	for _, hit := range vectorHits {
		iDValue2 := str.String(hit.Item.ID)
		id := iDValue2.Trim()
		if id == "" {
			continue
		}
		existing, ok := byID[id]
		if !ok {
			byID[id] = hit
			continue
		}
		byID[id] = mergeMemoryHitEvidence(existing, hit)
	}

	hits := make([]statememory.MemorySearchHit, 0, len(byID))
	for _, hit := range byID {
		hits = append(hits, hit)
	}
	sortMemoryHits(hits)
	return hits
}

func mergeMemoryHitEvidence(
	existing statememory.MemorySearchHit,
	next statememory.MemorySearchHit,
) statememory.MemorySearchHit {
	if next.Score > existing.Score {
		existing.Score = next.Score
	}
	if next.LexicalScore > existing.LexicalScore {
		existing.LexicalScore = next.LexicalScore
	}
	if next.VectorScore > existing.VectorScore {
		existing.VectorScore = next.VectorScore
	}
	return existing
}

func sortMemoryHits(hits []statememory.MemorySearchHit) {
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if !hits[i].Item.UpdatedAt.Equal(hits[j].Item.UpdatedAt) {
			return hits[i].Item.UpdatedAt.After(hits[j].Item.UpdatedAt)
		}

		return hits[i].Item.ID < hits[j].Item.ID
	})
}

func checkMemoryQueryNeedsSourceIDFilter(query statememory.MemorySearchQuery) bool {
	return len(query.IDs) > 0 ||
		query.PromotionEvaluated != nil ||
		!query.PromotionEvaluatedBefore.IsZero() ||
		!query.PromotionEvaluatedAfter.IsZero()
}

func getMemoryVectorFilterTags(query statememory.MemorySearchQuery) []string {
	tags := make([]string, 0, 4+len(query.Tags))
	sessionIDValue := str.String(query.SessionID)
	if sessionID := sessionIDValue.Trim(); sessionID != "" {
		tags = append(tags, search.MemoryVectorTag("memory_session", sessionID))
	}
	if len(query.Kinds) == 1 {
		tags = append(tags, search.MemoryVectorTag("memory_kind", string(query.Kinds[0])))
	}
	if len(query.Statuses) == 0 {
		tags = append(tags, search.MemoryVectorTag("memory_status", string(statememory.MemoryStatusActive)))
	} else if len(query.Statuses) == 1 {
		tags = append(tags, search.MemoryVectorTag("memory_status", string(query.Statuses[0])))
	}
	for _, tag := range query.Tags {
		tagValue := str.String(tag)
		if tag = tagValue.Trim(); tag != "" {
			tags = append(tags, search.MemoryVectorTag("memory_tag", tag))
		}
	}
	if query.Reflected != nil {
		tags = append(tags, search.MemoryVectorTag("memory_reflected", fmt.Sprint(*query.Reflected)))
	}

	return search.NormalizeVectorTags(tags)
}

func getMemoryVectorFilterTagGroups(query statememory.MemorySearchQuery) [][]string {
	groups := make([][]string, 0, 2)
	if len(query.Kinds) > 1 {
		group := make([]string, 0, len(query.Kinds))
		for _, kind := range query.Kinds {
			kindValue := str.String(string(kind))
			if value := kindValue.Trim(); value != "" {
				group = append(group, search.MemoryVectorTag("memory_kind", value))
			}
		}
		groups = append(groups, group)
	}
	if len(query.Statuses) > 1 {
		group := make([]string, 0, len(query.Statuses))
		for _, status := range query.Statuses {
			statusValue := str.String(string(status))
			if value := statusValue.Trim(); value != "" {
				group = append(group, search.MemoryVectorTag("memory_status", value))
			}
		}
		groups = append(groups, group)
	}

	return search.NormalizeVectorTagGroups(groups)
}

func getMemoryVectorText(item statememory.MemoryItem) string {
	joinValue := str.String(strings.Join([]string{item.Title, item.Text}, "\n"))
	return joinValue.Trim()
}
