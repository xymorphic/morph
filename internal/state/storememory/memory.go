package storememory

import (
	"context"
	"errors"
	"sort"
	"time"

	statememory "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/internal/state/search"
	"github.com/xymorphic/morph/pkg/nanoid"
	"github.com/xymorphic/morph/pkg/str"
)

func (s *Store) SearchMemory(ctx context.Context, query statememory.MemorySearchQuery) (statememory.MemorySearchResult, error) {
	if s == nil {
		return statememory.MemorySearchResult{}, errors.New("store is required")
	}

	if s.memoryVectorSearchEnabled(query) {
		return s.searchMemoryHybrid(ctx, query)
	}

	s.mu.RLock()

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

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if !hits[i].Item.UpdatedAt.Equal(hits[j].Item.UpdatedAt) {
			return hits[i].Item.UpdatedAt.After(hits[j].Item.UpdatedAt)
		}

		return hits[i].Item.ID < hits[j].Item.ID
	})

	resultLimit := search.MemoryResultLimit(query.Limit)
	candidateLimit := search.MemoryCandidateLimit(resultLimit)
	if len(hits) > candidateLimit {
		hits = hits[:candidateLimit]
	}
	reranker := s.memoryReranker
	s.mu.RUnlock()

	return search.RerankMemoryHits(ctx, query, hits, search.MemoryRerankOptions{
		Reranker:      reranker,
		MaxCandidates: candidateLimit,
		Limit:         resultLimit,
	})
}

func (s *Store) ListSessionMemories(_ context.Context, query statememory.SessionMemoryQuery) (statememory.SessionMemoriesResult, error) {
	if s == nil {
		return statememory.SessionMemoriesResult{}, errors.New("store is required")
	}
	if err := statememory.ValidateSessionID(query.SessionID); err != nil {
		return statememory.SessionMemoriesResult{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]statememory.MemoryItem, 0, len(s.memoryItems))
	for _, item := range s.memoryItems {
		if statememory.CheckMemoryMatchesSessionQuery(item, query) {
			items = append(items, item.Clone())
		}
	}

	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		}

		return items[i].ID < items[j].ID
	})

	if query.Limit > 0 && len(items) > query.Limit {
		items = items[:query.Limit]
	}

	return statememory.SessionMemoriesResult{Items: items}, nil
}

func (s *Store) UpsertMemory(ctx context.Context, item statememory.MemoryItem) (statememory.MemoryItem, error) {
	if s == nil {
		return statememory.MemoryItem{}, errors.New("store is required")
	}

	s.mu.Lock()

	if s.memoryItems == nil {
		s.memoryItems = make(map[string]statememory.MemoryItem)
	}

	now := time.Now().UTC()
	item = item.Clone()
	iDValue := str.String(item.ID)
	item.ID = iDValue.Trim()
	if item.ID == "" {
		item.ID = nanoid.MustGenerate("mem_")
	}
	if item.Status == "" {
		item.Status = statememory.MemoryStatusCandidate
	}
	if existing, ok := s.memoryItems[item.ID]; ok {
		item.CreatedAt = existing.CreatedAt
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	} else {
		item.CreatedAt = item.CreatedAt.UTC()
	}
	item.UpdatedAt = now

	s.memoryItems[item.ID] = item.Clone()
	s.mu.Unlock()

	if err := s.handleVectorStoreError(s.syncMemoryVector(ctx, item)); err != nil {
		return statememory.MemoryItem{}, err
	}

	return item.Clone(), nil
}

func (s *Store) PatchMemory(ctx context.Context, patch statememory.MemoryPatch) (statememory.MemoryItem, error) {
	if s == nil {
		return statememory.MemoryItem{}, errors.New("store is required")
	}

	iDValue2 := str.String(patch.ID)
	id := iDValue2.Trim()
	if id == "" {
		return statememory.MemoryItem{}, errors.New("memory id is required")
	}

	s.mu.Lock()

	item, ok := s.memoryItems[id]
	if !ok {
		s.mu.Unlock()
		return statememory.MemoryItem{}, errors.New("memory item not found")
	}
	item = statememory.ApplyMemoryPatch(item.Clone(), patch, time.Now().UTC())
	s.memoryItems[id] = item.Clone()
	s.mu.Unlock()

	if err := s.handleVectorStoreError(s.syncMemoryVector(ctx, item)); err != nil {
		return statememory.MemoryItem{}, err
	}

	return item.Clone(), nil
}

func (s *Store) syncMemoryVector(ctx context.Context, item statememory.MemoryItem) error {
	if item.Status == statememory.MemoryStatusDeleted {
		return s.deleteMemoryVector(ctx, item.ID)
	}

	return s.indexMemoryVector(ctx, item)
}

func (s *Store) DeleteMemory(ctx context.Context, req statememory.MemoryDeleteRequest) error {
	if s == nil {
		return errors.New("store is required")
	}

	iDValue3 := str.String(req.ID)
	id := iDValue3.Trim()
	if id == "" {
		return errors.New("memory id is required")
	}

	s.mu.Lock()

	item, ok := s.memoryItems[id]
	if !ok {
		s.mu.Unlock()
		return nil
	}
	item.Status = statememory.MemoryStatusDeleted
	item.UpdatedAt = time.Now().UTC()
	s.memoryItems[id] = item
	s.mu.Unlock()

	return s.handleVectorStoreError(s.deleteMemoryVector(ctx, id))
}

func (s *Store) HardDeleteMemory(ctx context.Context, req statememory.MemoryDeleteRequest) error {
	if s == nil {
		return errors.New("store is required")
	}

	iDValue4 := str.String(req.ID)
	id := iDValue4.Trim()
	if id == "" {
		return errors.New("memory id is required")
	}

	s.mu.Lock()
	delete(s.memoryItems, id)
	s.mu.Unlock()

	return s.handleVectorStoreError(s.deleteMemoryVector(ctx, id))
}
