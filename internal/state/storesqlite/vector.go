package storesqlite

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/xymorphic/morph/internal/constants"
	base "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/internal/state/search"
	"github.com/xymorphic/morph/pkg/str"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const defaultVectorStoreRebuildBatchSize = constants.DefaultVectorStoreRebuildBatchSize
const defaultHybridRetrievalCandidateLimit = search.DefaultHybridRetrievalCandidateLimit
const defaultRerankCandidateLimit = search.DefaultRerankCandidateLimit
const maxHybridRetrievalCandidateLimit = search.MaxHybridRetrievalCandidateLimit

// searchCandidate is a merged lexical/vector candidate before final grouping.
type searchCandidate struct {
	search.CandidateMatch
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ID         uint
	Role       string
	Name       string
	Content    string
	ToolCalls  string
	ToolCallID string
}

// searchCandidateSet collects merged search candidates keyed by message row ID.
type searchCandidateSet = search.SearchCandidateSet[uint, *searchCandidate]

// CandidateMatchRef returns the mutable ranking metadata for this candidate.
func (candidate *searchCandidate) CandidateMatchRef() *search.CandidateMatch {
	if candidate == nil {
		return nil
	}

	return &candidate.CandidateMatch
}

// VectorStoreOptions aliases search.VectorStoreOptions at this package boundary.
type VectorStoreOptions = search.VectorStoreOptions

// vectorConfig holds normalized vector dependencies and operational limits.
type vectorConfig struct {
	search.VectorConfig
	batchSize int
}

type vectorInput = search.VectorInput

type vectorIndexStateModel struct {
	SourceID  string `gorm:"primaryKey;size:255"`
	SessionID string `gorm:"index;size:255;not null"`
	MessageID uint   `gorm:"index;not null"`
	Status    string `gorm:"index;size:16;not null"`
	Attempts  int    `gorm:"not null"`
	ErrorKind string `gorm:"size:64"`
	UpdatedAt time.Time
}

func (vectorIndexStateModel) TableName() string {
	return "session_vector_index_states"
}

func (records messageModels) getVectorIndexStates(chunking search.VectorChunkOptions) []vectorIndexStateModel {
	states := make([]vectorIndexStateModel, 0, len(records))
	now := time.Now().UTC()
	for _, record := range records {
		status := search.VectorIndexSkipped
		if len(search.VectorInputsFromIndexRows(messageModelToSearchRows(record), chunking)) > 0 {
			status = search.VectorIndexPending
		}
		states = append(states, vectorIndexStateModel{
			SourceID:  search.SourceIDForMessage(record.SessionID, record.ID),
			SessionID: record.SessionID,
			MessageID: record.ID,
			Status:    string(status),
			UpdatedAt: now,
		})
	}
	return states
}

func (s *Store) setVectorIndexResult(ctx context.Context, records messageModels, indexErr error) error {
	if s == nil || s.vectors == nil || len(records) == 0 {
		return nil
	}
	status := search.VectorIndexReady
	errorKind := ""
	if indexErr != nil {
		status = search.VectorIndexFailed
		errorKind = "vector_index_failed"
	}
	now := time.Now().UTC()
	states := make([]vectorIndexStateModel, 0, len(records))
	for _, record := range records {
		if len(search.VectorInputsFromIndexRows(messageModelToSearchRows(record), s.vectors.Chunking)) == 0 {
			continue
		}
		states = append(states, vectorIndexStateModel{
			SourceID:  search.SourceIDForMessage(record.SessionID, record.ID),
			SessionID: record.SessionID,
			MessageID: record.ID,
			Status:    string(status),
			Attempts:  1,
			ErrorKind: errorKind,
			UpdatedAt: now,
		})
	}
	if len(states) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "source_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"status":     status,
			"attempts":   gorm.Expr("attempts + 1"),
			"error_kind": errorKind,
			"updated_at": now,
		}),
	}).Create(&states).Error
}

func (s *Store) loadRetryableVectorSourceIDs(ctx context.Context, inputs []search.VectorInput) ([]string, error) {
	if s == nil || s.db == nil || len(inputs) == 0 {
		return nil, nil
	}

	sourceIDs := make([]string, 0, len(inputs))
	for _, input := range inputs {
		sourceIDs = append(sourceIDs, input.SourceID)
	}
	sourceIDs = base.UniqueStrings(sourceIDs)

	var retryable []string
	err := s.db.WithContext(ctx).
		Model(&vectorIndexStateModel{}).
		Where("source_id IN ? AND status IN ?", sourceIDs, []search.VectorIndexStatus{
			search.VectorIndexPending,
			search.VectorIndexFailed,
		}).
		Order("source_id asc").
		Pluck("source_id", &retryable).Error
	return retryable, err
}

// ConfigureVectorStore enables or disables vector indexing and hybrid search for this store.
func (s *Store) ConfigureVectorStore(opts VectorStoreOptions) error {
	if s == nil || s.db == nil {
		return errors.New("store is required")
	}

	embeddingModelValue := str.String(opts.EmbeddingModel)
	model := embeddingModelValue.Trim()
	if opts.Embedder == nil && opts.VectorStore == nil && model == "" {
		s.vectors = nil
		return nil
	}
	if opts.Embedder == nil {
		return errors.New("vector store embedding provider is required")
	}
	if opts.VectorStore == nil {
		return errors.New("vector store is required")
	}
	if model == "" {
		return errors.New("vector store embedding model is required")
	}
	if opts.MaxInputBytes < 0 || opts.MaxDocumentBytes < 0 {
		return errors.New("vector store chunk limits must be greater than or equal to zero")
	}
	chunking := search.NormalizeVectorChunkOptions(search.VectorChunkOptions{
		MaxInputBytes:    opts.MaxInputBytes,
		MaxDocumentBytes: opts.MaxDocumentBytes,
	})
	if chunking.MaxDocumentBytes < chunking.MaxInputBytes {
		return errors.New("vector store max document bytes must be greater than or equal to max input bytes")
	}
	batchSize := opts.RebuildBatchSize
	if batchSize < 0 {
		return errors.New("vector store rebuild batch size must be greater than or equal to zero")
	}
	if batchSize == 0 {
		batchSize = defaultVectorStoreRebuildBatchSize
	}
	rerankMax := opts.RerankMaxCandidates
	if rerankMax < 0 {
		return errors.New("vector store rerank max candidates must be greater than or equal to zero")
	}
	if rerankMax == 0 {
		rerankMax = defaultRerankCandidateLimit
	}
	rerankEnabled := true
	if opts.EnableRerank != nil {
		rerankEnabled = *opts.EnableRerank
	}

	if err := search.ValidateReranker(opts.Reranker); err != nil {
		return err
	}

	s.vectors = &vectorConfig{
		VectorConfig: search.VectorConfig{
			Provider:    opts.Embedder,
			Reranker:    opts.Reranker,
			Store:       opts.VectorStore,
			Model:       model,
			RerankMax:   rerankMax,
			Diagnostics: opts.Diagnostics,
			Rerank:      rerankEnabled,
			Required:    opts.Required,
			Chunking:    chunking,
		},
		batchSize: batchSize,
	}

	return nil
}

func (s *Store) SupportsVectorSearch() bool {
	return s != nil && s.vectors != nil
}

// rerankEnabled reports whether hybrid search should rerank merged candidates.
func (s *Store) rerankEnabled() bool {
	return s != nil && s.vectors != nil && s.vectors.Rerank
}

// rerankerName returns the configured reranker name or the deterministic fallback name.
func (s *Store) rerankerName() string {
	if s == nil || s.vectors == nil || s.vectors.Reranker == nil {
		return search.RerankerDeterministic
	}

	nameValue := str.String(s.vectors.Reranker.Name())
	return nameValue.Normalized()
}

// diagnosticsEnabled reports whether vector search diagnostics should be logged.
func (s *Store) diagnosticsEnabled() bool {
	return s != nil && s.vectors != nil && s.vectors.Diagnostics
}

// searchMessagesHybrid merges lexical and vector candidates, reranks them, and maps them to public results.
func (s *Store) searchMessagesHybrid(
	ctx context.Context,
	id string,
	opts base.SearchMessageOptions,
	queryText string,
) ([]base.SearchMessageResult, error) {

	candidateLimit := getHybridCandidateLimit(opts)
	lexicalRows, err := s.searchMessagesLexical(ctx, id, opts, queryText, candidateLimit, false)
	if err != nil {
		applySafeErrorLog(s.logSearchEvent("lexical search failed", id, opts), err).
			Msg("session search lexical search failed")
		return nil, err
	}
	candidates := lexicalRowsToSearchCandidates(lexicalRows)

	s.logSearchEvent("lexical candidates gathered", id, opts).
		Int("candidate_count", len(candidates)).
		Int("row_count", len(lexicalRows)).
		Msg("session search lexical candidates gathered")

	vectorRows, err := s.searchMessagesVector(ctx, id, opts, candidateLimit)
	if err != nil {
		applySafeErrorLog(s.logSearchEvent("vector search failed", id, opts), err).
			Msg("session search vector search failed")
		return nil, err
	}

	s.logSearchEvent("vector candidates gathered", id, opts).
		Int("candidate_count", len(vectorRows)).
		Msg("session search vector candidates gathered")

	beforeMerge := len(candidates)
	candidates.Merge(vectorRows, getSearchCandidateKey)
	if len(candidates) == 0 {
		s.logSearchEvent("no candidates", id, opts).Msg("session search returned no hybrid candidates")
		return nil, nil
	}

	s.logSearchEvent("hybrid candidates merged", id, opts).
		Int("lexical_candidate_count", beforeMerge).
		Int("vector_candidate_count", len(vectorRows)).
		Int("merged_candidate_count", len(candidates)).
		Msg("session search hybrid candidates merged")

	s.logCandidateDiagnostics("candidate merged", candidates.Sorted(isSearchCandidateLess))

	matchCounts, lastMatchedAt := getSearchCandidateSessionStats(candidates)
	reranked := candidates.Sorted(isSearchCandidateLess)
	if s.rerankEnabled() {
		reranked = s.rerankSearchCandidates(ctx, opts, candidates)
		s.logCandidateDiagnostics("candidate reranked", reranked)
	} else {
		s.logSearchEvent("rerank skipped", id, opts).
			Str("reranker", s.rerankerName()).
			Msg("session search rerank skipped")
	}
	rows := searchCandidateSliceToRankedSearchRows(reranked, opts, matchCounts, lastMatchedAt)
	results := searchMessageResultRowsToResults(rows)

	s.logSearchEvent("results ranked", id, opts).
		Int("session_count", len(results)).
		Int("message_count", len(rows)).
		Msg("session search hybrid results ranked")

	return results, nil
}

// lexicalRowsToSearchCandidates converts ranked lexical rows into mergeable candidates.
func lexicalRowsToSearchCandidates(rows []searchSessionResultRow) searchCandidateSet {
	candidates := make(searchCandidateSet, len(rows))
	for idx, row := range rows {
		candidates[row.ID] = &searchCandidate{
			CandidateMatch: search.CandidateMatch{
				SessionID:       row.SessionID,
				MatchedText:     row.MatchedText,
				MatchedToolName: row.MatchedToolName,
				LexicalScore:    row.Score,
				LexicalRank:     idx + 1,
				HasLexical:      true,
			},
			CreatedAt:  row.CreatedAt,
			UpdatedAt:  row.UpdatedAt,
			ID:         row.ID,
			Role:       row.Role,
			Name:       row.Name,
			Content:    row.Content,
			ToolCalls:  row.ToolCalls,
			ToolCallID: row.ToolCallID,
		}
	}

	return candidates
}

// searchCandidatesToRankedSearchRows ranks candidates and returns rows shaped for public search results.
func searchCandidatesToRankedSearchRows(
	candidates searchCandidateSet,
	opts base.SearchMessageOptions,
) []searchSessionResultRow {
	return searchCandidateSliceToRankedSearchRows(candidates.Sorted(isSearchCandidateLess), opts, nil, nil)
}

// searchCandidateSliceToRankedSearchRows groups ranked candidates by session and applies result limits.
func searchCandidateSliceToRankedSearchRows(
	candidates []*searchCandidate,
	opts base.SearchMessageOptions,
	matchCounts map[string]int,
	lastMatchedAt map[string]time.Time,
) []searchSessionResultRow {
	groups := make(map[string][]*searchCandidate)
	for _, candidate := range candidates {
		groups[candidate.SessionID] = append(groups[candidate.SessionID], candidate)
	}

	sessions := make([]string, 0, len(groups))
	bestScoreBySession := make(map[string]float64, len(groups))
	lastMatchedBySession := make(map[string]time.Time, len(groups))
	for sessionID, sessionCandidates := range groups {
		slices.SortStableFunc(sessionCandidates, compareCandidatesWithinSession)
		bestScoreBySession[sessionID] = getSearchCandidateRankingScore(sessionCandidates[0])
		for _, candidate := range sessionCandidates {
			if candidate.CreatedAt.After(lastMatchedBySession[sessionID]) {
				lastMatchedBySession[sessionID] = candidate.CreatedAt
			}
		}
		if lastMatchedAt[sessionID].After(lastMatchedBySession[sessionID]) {
			lastMatchedBySession[sessionID] = lastMatchedAt[sessionID]
		}
		sessions = append(sessions, sessionID)
	}

	slices.SortStableFunc(sessions, func(left string, right string) int {
		return compareRankedSessions(left, right, bestScoreBySession, lastMatchedBySession)
	})
	if opts.MaxSessions > 0 && len(sessions) > opts.MaxSessions {
		sessions = sessions[:opts.MaxSessions]
	}

	rows := make([]searchSessionResultRow, 0, len(candidates))
	for _, sessionID := range sessions {
		sessionCandidates := groups[sessionID]
		if opts.MaxMessagesPerSession > 0 && len(sessionCandidates) > opts.MaxMessagesPerSession {
			sessionCandidates = sessionCandidates[:opts.MaxMessagesPerSession]
		}
		lastMatchedAt := ""
		if !lastMatchedBySession[sessionID].IsZero() {
			lastMatchedAt = lastMatchedBySession[sessionID].UTC().Format(time.RFC3339Nano)
		}
		for _, candidate := range sessionCandidates {
			rows = append(rows, searchSessionResultRow{
				CreatedAt:       candidate.CreatedAt,
				UpdatedAt:       candidate.UpdatedAt,
				ID:              candidate.ID,
				SessionID:       candidate.SessionID,
				Sequence:        0,
				Role:            candidate.Role,
				Name:            candidate.Name,
				Content:         candidate.Content,
				ToolCalls:       candidate.ToolCalls,
				ToolCallID:      candidate.ToolCallID,
				MatchedText:     candidate.MatchedText,
				MatchedToolName: candidate.MatchedToolName,
				Score:           getSearchCandidateRankingScore(candidate),
				BestScore:       bestScoreBySession[sessionID],
				MatchCount:      getSearchCandidateMatchCount(sessionID, groups, matchCounts),
				LastMatchedAt:   lastMatchedAt,
			})
		}
	}

	return rows
}

// compareCandidatesWithinSession orders candidates inside a single session.
func compareCandidatesWithinSession(left *searchCandidate, right *searchCandidate) int {
	leftScore := getSearchCandidateRankingScore(left)
	rightScore := getSearchCandidateRankingScore(right)
	if leftScore != rightScore {
		if leftScore > rightScore {
			return -1
		}

		return 1
	}
	if !left.CreatedAt.Equal(right.CreatedAt) {
		if left.CreatedAt.After(right.CreatedAt) {
			return -1
		}

		return 1
	}
	if left.ID > right.ID {
		return -1
	}
	if left.ID < right.ID {
		return 1
	}
	return 0
}

// compareRankedSessions orders result sessions by best score, recency, and session ID.
func compareRankedSessions(
	left string,
	right string,
	bestScoreBySession map[string]float64,
	lastMatchedBySession map[string]time.Time,
) int {
	if bestScoreBySession[left] != bestScoreBySession[right] {
		if bestScoreBySession[left] > bestScoreBySession[right] {
			return -1
		}

		return 1
	}
	if !lastMatchedBySession[left].Equal(lastMatchedBySession[right]) {
		if lastMatchedBySession[left].After(lastMatchedBySession[right]) {
			return -1
		}

		return 1
	}
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}

	return 0
}

// getSearchCandidateKey returns the durable message row ID used to merge candidates.
func getSearchCandidateKey(candidate *searchCandidate) uint {
	if candidate == nil {
		return 0
	}

	return candidate.ID
}

// isSearchCandidateLess reports whether left should sort before right.
func isSearchCandidateLess(left *searchCandidate, right *searchCandidate) bool {
	return compareSearchCandidates(left, right) < 0
}

// getSearchCandidateSessionStats returns per-session candidate counts and latest timestamps.
func getSearchCandidateSessionStats(candidates searchCandidateSet) (map[string]int, map[string]time.Time) {
	matchCounts := make(map[string]int)
	lastMatchedAt := make(map[string]time.Time)
	for _, candidate := range candidates {
		matchCounts[candidate.SessionID]++
		if candidate.CreatedAt.After(lastMatchedAt[candidate.SessionID]) {
			lastMatchedAt[candidate.SessionID] = candidate.CreatedAt
		}
	}

	return matchCounts, lastMatchedAt
}

// getSearchCandidateMatchCount returns the original match count for a result session.
func getSearchCandidateMatchCount(
	sessionID string,
	groups map[string][]*searchCandidate,
	matchCounts map[string]int,
) int {
	if matchCounts[sessionID] > 0 {
		return matchCounts[sessionID]
	}

	return len(groups[sessionID])
}

// compareSearchCandidates orders candidates by score, recency, session ID, and message ID.
func compareSearchCandidates(left *searchCandidate, right *searchCandidate) int {
	return search.CompareCandidateOrder(
		getSearchCandidateRankingScore(left),
		getSearchCandidateRankingScore(right),
		left.CreatedAt,
		right.CreatedAt,
		left.SessionID,
		right.SessionID,
		left.ID,
		right.ID,
	)
}

// getSearchCandidateRankingScore returns the final score used for ordering a candidate.
func getSearchCandidateRankingScore(candidate *searchCandidate) float64 {
	return search.CandidateRankingScore(candidate.HasRerank, candidate.RerankScore, candidate.FusedScore)
}

// getHybridCandidateLimit returns the shared lexical/vector candidate limit.
func getHybridCandidateLimit(opts base.SearchMessageOptions) int {
	return search.HybridRetrievalCandidateLimit(opts)
}

// rerankSearchCandidates converts merged search candidates to the shared retrieval reranker contract.
func (s *Store) rerankSearchCandidates(
	ctx context.Context,
	opts base.SearchMessageOptions,
	candidates searchCandidateSet,
) []*searchCandidate {
	items := candidates.Sorted(isSearchCandidateLess)
	if len(items) == 0 {
		return nil
	}

	maxCandidates := defaultRerankCandidateLimit
	reranker := search.Reranker(search.DeterministicReranker{})
	rerankerName := search.RerankerDeterministic
	if s != nil && s.vectors != nil {
		maxCandidates = s.vectors.RerankMax
		rerankerName = s.rerankerName()
		if s.vectors.Reranker != nil {
			reranker = s.vectors.Reranker
		}
	}
	if maxCandidates > 0 && len(items) > maxCandidates {
		items = items[:maxCandidates]
	}
	s.logSearchEvent("rerank started", "", opts).
		Str("configured_reranker", rerankerName).
		Int("candidate_count", len(items)).
		Int("max_candidates", maxCandidates).
		Msg("session search rerank started")

	retrievalCandidates := make([]search.Candidate, 0, len(items))
	searchCandidateByID := make(map[string]*searchCandidate, len(items))
	for _, candidate := range items {
		retrievalCandidate := searchCandidateToRetrievalCandidate(candidate)
		retrievalCandidates = append(retrievalCandidates, retrievalCandidate)
		searchCandidateByID[retrievalCandidate.ID] = candidate
	}
	queryValue := str.String(opts.Query)
	result, err := search.RerankWithFallback(ctx, reranker, search.DeterministicReranker{}, search.RerankRequest{
		Query:      queryValue.Trim(),
		Caller:     "session_search",
		SourceKind: search.SourceKindSessionMessage,
		Candidates: retrievalCandidates,
		Options: search.RerankOptions{
			LexicalDirection: search.ScoreLowerIsBetter,
			VectorDirection:  search.ScoreHigherIsBetter,
			FusedDirection:   search.ScoreHigherIsBetter,
		},
	})
	if err != nil {
		s.logSearchEvent("rerank fallback failed", "", opts).
			Err(err).
			Str("configured_reranker", rerankerName).
			Int("candidate_count", len(items)).
			Msg("session search rerank fallback failed")
		return items
	}

	reranked := make([]*searchCandidate, 0, len(result.Items))
	for _, item := range result.Items {
		candidate := searchCandidateByID[item.CandidateID]
		candidate.RerankScore = item.Score
		candidate.HasRerank = true
		reranked = append(reranked, candidate)
	}

	s.logSearchEvent("rerank completed", "", opts).
		Str("configured_reranker", rerankerName).
		Str("reranker", getSearchRerankResultName(result, rerankerName)).
		Int("candidate_count", len(items)).
		Int("result_count", len(reranked)).
		Msg("session search rerank completed")

	return reranked
}

// getSearchRerankResultName returns the reranker reported by a result or the configured fallback.
func getSearchRerankResultName(result search.RerankResult, fallback string) string {
	rerankerValue := str.String(result.Reranker)
	if name := rerankerValue.Normalized(); name != "" {
		return name
	}
	fallbackValue := str.String(fallback)
	return fallbackValue.Normalized()
}

// searchCandidateToRetrievalCandidate converts a search candidate to the reranker contract.
func searchCandidateToRetrievalCandidate(candidate *searchCandidate) search.Candidate {
	matchedTextValue := str.String(candidate.MatchedText)
	text := matchedTextValue.Trim()
	if text == "" {
		contentValue := str.String(candidate.Content)
		text = contentValue.Trim()
	}

	return search.Candidate{
		CreatedAt:    candidate.CreatedAt,
		UpdatedAt:    candidate.UpdatedAt,
		ID:           messageToSourceID(candidate.SessionID, candidate.ID),
		SourceKind:   search.SourceKindSessionMessage,
		SessionID:    candidate.SessionID,
		Text:         text,
		LexicalScore: candidate.LexicalScore,
		VectorScore:  candidate.VectorScore,
		FusedScore:   candidate.FusedScore,
		MessageID:    candidate.ID,
	}
}

// searchMessagesVector embeds the query and asks the vector store for session-message candidates.
func (s *Store) searchMessagesVector(
	ctx context.Context,
	id string,
	opts base.SearchMessageOptions,
	candidateLimit int,
) ([]*searchCandidate, error) {
	modelValue := str.String(s.vectors.Model)
	queryValue2 := str.String(opts.Query)
	embeddingReq := search.EmbeddingRequest{
		Model:        modelValue.Trim(),
		Relationship: "query_vector_for_session_message_retrieval",
		Target:       "session_message_vectors",
		Inputs: []search.EmbeddingInput{{
			ID:         "query",
			Text:       queryValue2.Trim(),
			SourceKind: search.SourceKindSessionMessage,
		}},
	}

	s.logSearchEvent("query embedding started", id, opts).
		Str("source_kind", string(search.SourceKindSessionMessage)).
		Str("embedding_model", embeddingReq.Model).
		Msg("session search query embedding started for vector retrieval")

	embedding, err := s.vectors.Provider.Embed(ctx, embeddingReq)
	if err != nil {
		applySafeErrorLog(s.logSearchEvent("query embedding failed", id, opts), err).
			Msg("session search query embedding failed")
		return nil, err
	}
	if err := search.ValidateEmbeddingResult(embeddingReq, embedding); err != nil {
		applySafeErrorLog(s.logSearchEvent("query embedding validation failed", id, opts), err).
			Msg("session search query embedding validation failed")
		return nil, err
	}
	modelValue2 := str.String(embedding.Model)
	s.logSearchEvent("query embedding completed", id, opts).
		Int("dimensions", embedding.Dimensions).
		Str("source_kind", string(search.SourceKindSessionMessage)).
		Str("embedding_model", modelValue2.Trim()).
		Msg("session search query embedding completed for vector retrieval")
	modelValue3 := str.String(s.vectors.Model)
	roleValue := str.String(string(opts.Role))
	searchReq := search.VectorSearchRequest{
		EmbeddingModel: modelValue3.Trim(),
		Dimensions:     embedding.Dimensions,
		QueryVector:    embedding.Items[0].Vector,
		Limit:          candidateLimit,
		Filter: search.VectorFilter{
			SourceKind:      search.SourceKindSessionMessage,
			SessionID:       id,
			IgnoreSessionID: opts.IgnoreSessionID,
			Role:            roleValue.Trim(),
			ToolName:        normalizeSearchValue(opts.ToolName),
		},
	}

	s.logSearchEvent("vector search started", id, opts).
		Int("limit", candidateLimit).
		Int("dimensions", embedding.Dimensions).
		Str("source_kind", string(search.SourceKindSessionMessage)).
		Str("target", "session_message_vectors").
		Str("embedding_model", searchReq.EmbeddingModel).
		Msg("session search vector retrieval started for similar messages")

	result, err := s.vectors.Store.Search(ctx, searchReq)
	if err != nil {
		applySafeErrorLog(s.logSearchEvent("vector search failed", id, opts), err).
			Msg("session search vector retrieval failed")
		return nil, err
	}

	s.logSearchEvent("vector search completed", id, opts).
		Int("match_count", len(result.Matches)).
		Int("limit", candidateLimit).
		Int("dimensions", embedding.Dimensions).
		Msg("session search vector retrieval completed for similar messages")

	candidates, err := s.vectorMatchesToCandidates(ctx, id, opts, result.Matches)
	if err != nil {
		applySafeErrorLog(s.logSearchEvent("vector matches resolve failed", id, opts), err).
			Msg("session search vector matches resolve failed")
		return nil, err
	}

	s.logSearchEvent("vector matches resolved", id, opts).
		Int("match_count", len(result.Matches)).
		Int("candidate_count", len(candidates)).
		Msg("session search vector matches resolved")

	return candidates, nil
}

// vectorMatchesToCandidates resolves vector hits back to durable messages and searchable rows.
func (s *Store) vectorMatchesToCandidates(
	ctx context.Context,
	id string,
	opts base.SearchMessageOptions,
	matches []search.VectorSearchMatch,
) ([]*searchCandidate, error) {
	if len(matches) == 0 {
		return nil, nil
	}

	refs := make(messageRefs, 0, len(matches))
	for _, match := range matches {
		ref, ok := sourceIDToMessageRef(match.Record.SourceID)
		if !ok {
			continue
		}
		refs = append(refs, ref)
	}
	if len(refs) == 0 {
		return nil, nil
	}

	records, err := s.messagesByRef(ctx, refs)
	if err != nil {
		return nil, err
	}

	candidates := make([]*searchCandidate, 0, len(matches))
	seen := map[uint]struct{}{}
	for idx, match := range matches {
		ref, ok := sourceIDToMessageRef(match.Record.SourceID)
		if !ok {
			continue
		}
		record, ok := records.get(ref)
		if !ok || !checkVectorRecordMatchesOptions(record, id, opts) {
			continue
		}
		if _, ok := seen[record.ID]; ok {
			continue
		}
		row, ok := vectorRecordToSearchRow(record, match.Record.ID, s.vectors.Chunking)
		if !ok || search.IsVectorRecordStale(match.Record, row.Body) || !checkSearchRowMatchesOptions(row, opts) {
			continue
		}

		candidates = append(candidates, &searchCandidate{
			CandidateMatch: search.CandidateMatch{
				SessionID:       record.SessionID,
				MatchedText:     row.Body,
				MatchedToolName: row.ToolName,
				VectorScore:     match.Score,
				VectorRank:      idx + 1,
				HasVector:       true,
			},
			CreatedAt:  record.CreatedAt,
			UpdatedAt:  record.UpdatedAt,
			ID:         record.ID,
			Role:       record.Role,
			Name:       record.Name,
			Content:    record.Content,
			ToolCalls:  record.ToolCalls,
			ToolCallID: record.ToolCallID,
		})
		seen[record.ID] = struct{}{}
	}

	return candidates, nil
}

// messagesByRef loads active messages for unique session/message references.
func (s *Store) messagesByRef(ctx context.Context, refs messageRefs) (messageLookup, error) {
	refs = refs.unique()
	records := make(messageLookup, len(refs))
	if len(refs) == 0 {
		return records, nil
	}

	where, args := refs.tupleCondition()
	var rows []messageModel
	if err := s.db.WithContext(ctx).Where("(session_id, id) IN ("+where+")", args...).Find(&rows).Error; err != nil {
		return nil, err
	}

	records = make(messageLookup, len(rows))
	for _, row := range rows {
		records.set(messageRef{SessionID: row.SessionID, MessageID: row.ID}, row)
	}

	return records, nil
}

// messageRef identifies a message row within a session.
type messageRef struct {
	SessionID string
	MessageID uint
}

// messageRefs is a typed slice for building tuple queries by message reference.
type messageRefs []messageRef

// messageLookup describes messages keyed by session/message reference.
type messageLookup map[string]messageModel

// unique removes duplicate message references while preserving first occurrence order.
func (refs messageRefs) unique() messageRefs {
	seen := make(map[string]struct{}, len(refs))
	unique := make(messageRefs, 0, len(refs))
	for _, ref := range refs {
		key := ref.key()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, ref)
	}

	return unique
}

// tupleCondition builds a SQLite row-value placeholder list for session/message references.
func (refs messageRefs) tupleCondition() (string, []any) {
	var where strings.Builder
	args := make([]any, 0, len(refs)*2)
	for _, ref := range refs {
		if where.Len() > 0 {
			where.WriteString(", ")
		}
		where.WriteString("(?, ?)")
		args = append(args, ref.SessionID, ref.MessageID)
	}

	return where.String(), args
}

// key returns a stable map key for this message reference.
func (r messageRef) key() string {
	return fmt.Sprintf("%s:%d", r.SessionID, r.MessageID)
}

// get returns the message for a reference.
func (lookup messageLookup) get(ref messageRef) (messageModel, bool) {
	record, ok := lookup[ref.key()]
	return record, ok
}

// set describes a message for a reference.
func (lookup messageLookup) set(ref messageRef, record messageModel) {
	lookup[ref.key()] = record
}

// sourceIDToMessageRef parses a vector source ID into a SQLite message reference.
func sourceIDToMessageRef(sourceID string) (messageRef, bool) {
	sessionID, messageID, ok := search.MessageRefFromSourceID(sourceID)
	if !ok {
		return messageRef{}, false
	}

	return messageRef{SessionID: sessionID, MessageID: messageID}, true
}

// checkVectorRecordMatchesOptions reports whether a resolved message satisfies vector search filters.
func checkVectorRecordMatchesOptions(record messageModel, id string, opts base.SearchMessageOptions) bool {
	if id != "" && record.SessionID != id {
		return false
	}
	if opts.IgnoreSessionID != "" && record.SessionID == opts.IgnoreSessionID {
		return false
	}

	roleValue2 := str.String(string(opts.Role))
	if role := roleValue2.Trim(); role != "" && record.Role != role {
		return false
	}

	return true
}

// vectorRecordToSearchRow returns the searchable row represented by a vector record ID.
func vectorRecordToSearchRow(
	record messageModel,
	vectorID string,
	chunking search.VectorChunkOptions,
) (searchRow, bool) {
	return search.MessageIndexRowForVectorRecord(
		[]search.MessageIndexRow(messageModelToSearchRows(record)),
		vectorID,
		chunking,
	)
}

// checkSearchRowMatchesOptions reports whether a searchable row satisfies non-text search filters.
func checkSearchRowMatchesOptions(row searchRow, opts base.SearchMessageOptions) bool {
	return search.MessageIndexRowMatchesSearchOptions(row, opts)
}

// indexVectors embeds searchable message rows and upserts the resulting vector records.
func (s *Store) indexVectors(ctx context.Context, records []messageModel) error {
	recordsToUpsert, err := s.vectorRecordsForMessages(ctx, records)
	if err != nil || len(recordsToUpsert) == 0 {
		return err
	}

	return s.upsertVectorRecords(ctx, recordsToUpsert)
}

// vectorRecordsForMessages embeds message search rows and builds vector records for storage.
func (s *Store) vectorRecordsForMessages(
	ctx context.Context,
	records []messageModel,
) ([]search.VectorRecord, error) {
	if s == nil || s.vectors == nil || len(records) == 0 {
		return nil, nil
	}

	rows := messageModels(records).searchRows()
	inputs := rows.vectorInputs(s.vectors.Chunking)
	if len(inputs) == 0 {
		return nil, nil
	}
	diagnostics := search.GetVectorInputDiagnostics([]search.MessageIndexRow(rows), inputs, s.vectors.Chunking)
	if err := search.CheckVectorInputSizes(inputs, s.vectors.Chunking.MaxInputBytes); err != nil {
		return nil, err
	}

	embeddingInputs := make([]search.EmbeddingInput, 0, len(inputs))
	for _, input := range inputs {
		embeddingInputs = append(embeddingInputs, search.EmbeddingInput{
			ID:         input.ID,
			Text:       input.Text,
			SourceKind: search.SourceKindSessionMessage,
		})
	}

	req := search.EmbeddingRequest{
		Model:        s.vectors.Model,
		Relationship: "message_rows_to_session_vector_index",
		Target:       "session_message_vectors",
		Inputs:       embeddingInputs,
	}
	modelValue4 := str.String(req.Model)
	addVectorInputDiagnostics(s.logVectorEvent("embedding started"), diagnostics).
		Int("input_count", len(req.Inputs)).
		Int("message_count", len(records)).
		Int("row_count", len(inputs)).
		Int("max_input_bytes", s.vectors.Chunking.MaxInputBytes).
		Int("max_document_bytes", s.vectors.Chunking.MaxDocumentBytes).
		Int("largest_input_bytes", search.GetMaxVectorInputBytes(inputs)).
		Int("attempt_increment", 1).
		Str("index_status", string(search.VectorIndexPending)).
		Str("embedding_model", modelValue4.Trim()).
		Str("source_kind", string(search.SourceKindSessionMessage)).
		Msg("session vector indexing embedding started for message rows")

	result, err := s.vectors.Provider.Embed(ctx, req)
	if err != nil {
		addVectorInputDiagnostics(applySafeErrorLog(s.logVectorEvent("embedding failed"), err), diagnostics).
			Int("attempt_increment", 1).
			Str("index_status", string(search.VectorIndexFailed)).
			Msg("session vector embedding failed")
		return nil, err
	}
	if err := search.ValidateEmbeddingResult(req, result); err != nil {
		addVectorInputDiagnostics(
			applySafeErrorLog(s.logVectorEvent("embedding validation failed"), err),
			diagnostics,
		).
			Int("attempt_increment", 1).
			Str("index_status", string(search.VectorIndexFailed)).
			Msg("session vector embedding validation failed")
		return nil, err
	}
	modelValue5 := str.String(result.Model)
	addVectorInputDiagnostics(s.logVectorEvent("embedding completed"), diagnostics).
		Int("input_count", len(req.Inputs)).
		Int("message_count", len(records)).
		Int("row_count", len(inputs)).
		Int("dimensions", result.Dimensions).
		Int("max_input_bytes", s.vectors.Chunking.MaxInputBytes).
		Int("attempt_increment", 1).
		Str("index_status", string(search.VectorIndexReady)).
		Str("embedding_model", modelValue5.Trim()).
		Str("source_kind", string(search.SourceKindSessionMessage)).
		Msg("session vector indexing embedding completed for message rows")

	inputByID := make(map[string]vectorInput, len(inputs))
	for _, input := range inputs {
		inputByID[input.ID] = input
	}

	recordsToUpsert := make([]search.VectorRecord, 0, len(result.Items))
	for _, item := range result.Items {
		input := inputByID[item.ID]
		recordsToUpsert = append(recordsToUpsert, search.VectorRecord{
			CreatedAt:      input.CreatedAt,
			UpdatedAt:      input.UpdatedAt,
			ID:             item.ID,
			SourceKind:     search.SourceKindSessionMessage,
			SourceID:       input.SourceID,
			SessionID:      input.SessionID,
			Role:           input.Role,
			ToolName:       input.ToolName,
			EmbeddingModel: result.Model,
			ContentHash:    item.ContentHash,
			Vector:         item.Vector,
			Dimensions:     result.Dimensions,
		})
	}

	return recordsToUpsert, nil
}

// upsertVectorRecords writes vector records through the configured vector store.
func (s *Store) upsertVectorRecords(ctx context.Context, records []search.VectorRecord) error {
	if s == nil || s.vectors == nil || len(records) == 0 {
		return nil
	}

	model := records[0].EmbeddingModel
	dimensions := records[0].Dimensions
	modelValue6 := str.String(model)
	s.logVectorEvent("upsert started").
		Int("record_count", len(records)).
		Str("embedding_model", modelValue6.Trim()).
		Int("dimensions", dimensions).
		Str("target", "session_message_vectors").
		Msg("session vector index upsert started for message rows")
	if err := s.vectors.Store.Upsert(ctx, records); err != nil {
		applySafeErrorLog(s.logVectorEvent("upsert failed"), err).
			Int("record_count", len(records)).
			Msg("session vector upsert failed")
		return err
	}
	modelValue7 := str.String(model)
	s.logVectorEvent("upsert completed").
		Int("record_count", len(records)).
		Str("embedding_model", modelValue7.Trim()).
		Int("dimensions", dimensions).
		Str("target", "session_message_vectors").
		Msg("session vector index upsert completed for message rows")

	return nil
}

// deleteVectorRows removes vector records for one or more session-message source IDs.
func (s *Store) deleteVectorRows(ctx context.Context, sourceIDs []string) error {
	if s == nil || s.vectors == nil || len(sourceIDs) == 0 {
		return nil
	}

	sourceIDs = getUniqueStrings(sourceIDs)
	if len(sourceIDs) == 0 {
		return nil
	}
	req := search.VectorDeleteRequest{
		SourceKind: search.SourceKindSessionMessage,
		SourceIDs:  sourceIDs,
	}

	s.logVectorEvent("delete started").
		Int("source_id_count", len(sourceIDs)).
		Str("source_kind", string(req.SourceKind)).
		Msg("session vector delete started")

	if err := s.vectors.Store.Delete(ctx, req); err != nil {
		applySafeErrorLog(s.logVectorEvent("delete failed"), err).
			Int("source_id_count", len(sourceIDs)).
			Str("source_kind", string(req.SourceKind)).
			Msg("session vector delete failed")
		return err
	}

	s.logVectorEvent("delete completed").
		Int("source_id_count", len(sourceIDs)).
		Str("source_kind", string(req.SourceKind)).
		Msg("session vector delete completed")

	return nil
}

func (s *Store) deleteVectorRowsBySession(ctx context.Context, sessionID string) error {
	sessionIDValue := str.String(sessionID)
	sessionID = sessionIDValue.Trim()
	if s == nil || s.vectors == nil || sessionID == "" {
		return nil
	}

	req := search.VectorDeleteRequest{
		SourceKind: search.SourceKindSessionMessage,
		SessionID:  sessionID,
	}

	s.logVectorEvent("delete started").
		Str("session_id", sessionID).
		Str("source_kind", string(req.SourceKind)).
		Msg("session vector delete started")

	if err := s.vectors.Store.Delete(ctx, req); err != nil {
		applySafeErrorLog(s.logVectorEvent("delete failed"), err).
			Str("session_id", sessionID).
			Str("source_kind", string(req.SourceKind)).
			Msg("session vector delete failed")
		return err
	}

	s.logVectorEvent("delete completed").
		Str("session_id", sessionID).
		Str("source_kind", string(req.SourceKind)).
		Msg("session vector delete completed")

	return nil
}

// handleVectorStoreError applies best-effort versus required vector indexing semantics.
func (s *Store) handleVectorStoreError(err error) error {
	if err == nil || s == nil || s.vectors == nil || !s.vectors.Required {
		return nil
	}

	return err
}

// vectorInputs maps search rows to stable embedding inputs.
func (rows searchRows) vectorInputs(chunking search.VectorChunkOptions) []vectorInput {
	return search.VectorInputsFromIndexRows([]search.MessageIndexRow(rows), chunking)
}

// sourceIDs returns stable vector source IDs for active message models.
func (records messageModels) sourceIDs() []string {
	if len(records) == 0 {
		return nil
	}

	sourceIDs := make([]string, 0, len(records))
	for _, record := range records {
		sourceIDs = append(sourceIDs, messageToSourceID(record.SessionID, record.ID))
	}

	return sourceIDs
}

// messageToSourceID returns the vector source ID for a message row.
func messageToSourceID(sessionID string, messageID uint) string {
	return search.SourceIDForMessage(sessionID, messageID)
}

// getUniqueStrings returns non-empty strings without duplicates.
func getUniqueStrings(values []string) []string {
	return base.UniqueStrings(values)
}

// normalizeSearchValue canonicalizes a search filter value.
func normalizeSearchValue(value string) string {
	return base.NormalizeMatchValue(value)
}
