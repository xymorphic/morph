package storememory

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	base "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/internal/state/search"
	messages "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/str"
)

type searchCandidate struct {
	search.CandidateMatch
	Message messages.Message
}

type searchCandidateSet = search.SearchCandidateSet[string, *searchCandidate]

func (candidate *searchCandidate) CandidateMatchRef() *search.CandidateMatch {
	if candidate == nil {
		return nil
	}

	return &candidate.CandidateMatch
}

func (s *Store) ConfigureVectorStore(opts search.VectorStoreOptions) error {
	if s == nil {
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

	rerankMax := opts.RerankMaxCandidates
	if rerankMax < 0 {
		return errors.New("vector store rerank max candidates must be greater than or equal to zero")
	}

	if rerankMax == 0 {
		rerankMax = search.DefaultRerankCandidateLimit
	}

	rerankEnabled := true
	if opts.EnableRerank != nil {
		rerankEnabled = *opts.EnableRerank
	}

	if err := search.ValidateReranker(opts.Reranker); err != nil {
		return err
	}

	s.vectors = &search.VectorConfig{
		Provider:    opts.Embedder,
		Reranker:    opts.Reranker,
		Store:       opts.VectorStore,
		Model:       model,
		RerankMax:   rerankMax,
		Diagnostics: opts.Diagnostics,
		Rerank:      rerankEnabled,
		Required:    opts.Required,
		Chunking:    chunking,
	}

	return nil
}

func (s *Store) SupportsVectorSearch() bool {
	return s != nil && s.vectors != nil
}

func getInitialVectorIndexState(
	sessionID string,
	message messages.Message,
	chunking search.VectorChunkOptions,
	now time.Time,
) search.VectorIndexState {
	status := search.VectorIndexSkipped
	if len(search.VectorInputsFromIndexRows(
		search.MessageIndexRowsFromMessage(sessionID, message),
		chunking,
	)) > 0 {
		status = search.VectorIndexPending
	}
	return search.VectorIndexState{
		SourceID:  search.SourceIDForMessage(sessionID, message.ID),
		SessionID: sessionID,
		MessageID: message.ID,
		Status:    status,
		UpdatedAt: now,
	}
}

func (s *Store) setVectorIndexResult(sessionID string, values []messages.Message, indexErr error) {
	if s == nil || s.vectors == nil || len(values) == 0 {
		return
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, message := range values {
		sourceID := search.SourceIDForMessage(sessionID, message.ID)
		state, ok := s.vectorStates[sourceID]
		if !ok {
			state = getInitialVectorIndexState(sessionID, message, s.vectors.Chunking, now)
		}
		if state.Status == search.VectorIndexSkipped {
			continue
		}
		state.Attempts++
		state.UpdatedAt = now
		if indexErr == nil {
			state.Status = search.VectorIndexReady
			state.ErrorKind = ""
		} else {
			state.Status = search.VectorIndexFailed
			state.ErrorKind = "vector_index_failed"
		}
		s.vectorStates[sourceID] = state
	}
}

func (s *Store) getRetryableVectorSourceIDs(inputs []search.VectorInput) []string {
	if s == nil || len(inputs) == 0 {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	retryable := make([]string, 0)
	for _, input := range inputs {
		state, ok := s.vectorStates[input.SourceID]
		if ok && (state.Status == search.VectorIndexPending || state.Status == search.VectorIndexFailed) {
			retryable = append(retryable, input.SourceID)
		}
	}

	return base.UniqueStrings(retryable)
}

func (s *Store) searchMessagesHybrid(
	ctx context.Context,
	id string,
	opts base.SearchMessageOptions,
	query string,
) ([]base.SearchMessageResult, error) {
	candidateLimit := search.HybridRetrievalCandidateLimit(opts)
	lexicalCandidates := s.searchMessagesLexicalCandidates(id, opts, query, candidateLimit)

	s.logSearchEvent("lexical candidates gathered", id, opts).
		Int("candidate_count", len(lexicalCandidates)).
		Msg("session search lexical candidates gathered")

	vectorCandidates, err := s.searchMessagesVector(ctx, id, opts, candidateLimit)
	if err != nil {
		applySafeErrorLog(s.logSearchEvent("vector search failed", id, opts), err).
			Msg("session search vector search failed")
		return nil, err
	}

	s.logSearchEvent("vector candidates gathered", id, opts).
		Int("candidate_count", len(vectorCandidates)).
		Msg("session search vector candidates gathered")

	candidates := lexicalCandidates
	beforeMerge := len(candidates)
	candidates.Merge(vectorCandidates, getSearchCandidateKey)
	if len(candidates) == 0 {
		s.logSearchEvent("no candidates", id, opts).Msg("session search returned no hybrid candidates")
		return nil, nil
	}

	s.logSearchEvent("hybrid candidates merged", id, opts).
		Int("lexical_candidate_count", beforeMerge).
		Int("vector_candidate_count", len(vectorCandidates)).
		Int("merged_candidate_count", len(candidates)).
		Msg("session search hybrid candidates merged")

	s.logCandidateDiagnostics("candidate merged", candidates.Sorted(isSearchCandidateLess))

	ranked := candidates.Sorted(isSearchCandidateLess)
	if s.rerankEnabled() {
		ranked = s.rerankSearchCandidates(ctx, opts, candidates)
		s.logCandidateDiagnostics("candidate reranked", ranked)
	} else {
		s.logSearchEvent("rerank skipped", id, opts).
			Str("reranker", s.rerankerName()).
			Msg("session search rerank skipped")
	}

	results := searchCandidatesToSearchResults(ranked, opts)

	s.logSearchEvent("results ranked", id, opts).
		Int("session_count", len(results)).
		Int("message_count", getSearchResultMessageCount(results)).
		Msg("session search hybrid results ranked")

	return results, nil
}

func (s *Store) searchMessagesLexicalCandidates(
	id string,
	opts base.SearchMessageOptions,
	query string,
	limit int,
) searchCandidateSet {
	s.mu.RLock()
	defer s.mu.RUnlock()

	candidates := make(searchCandidateSet)
	addHits := func(sessionID string, msgs []messages.Message) {
		hits := getMatchingMessageHits(sessionID, msgs, query, opts)
		sort.Slice(hits, func(i, j int) bool {
			if hits[i].Message.CreatedAt.Equal(hits[j].Message.CreatedAt) {
				return hits[i].Message.ID > hits[j].Message.ID
			}

			return hits[i].Message.CreatedAt.After(hits[j].Message.CreatedAt)
		})
		for _, hit := range hits {
			if limit > 0 && len(candidates) >= limit {
				return
			}

			candidates[search.SourceIDForMessage(sessionID, hit.Message.ID)] = &searchCandidate{
				CandidateMatch: search.CandidateMatch{
					SessionID:       sessionID,
					MatchedText:     hit.MatchedText,
					MatchedToolName: hit.MatchedToolName,
					LexicalScore:    getLexicalScore(hit.MatchedText, query),
					LexicalRank:     len(candidates) + 1,
					HasLexical:      true,
				},
				Message: cloneMessages([]messages.Message{hit.Message})[0],
			}
		}
	}

	if id != "" {
		addHits(id, s.messages[id])
		return candidates
	}

	sessionIDs := make([]string, 0, len(s.messages))
	for sessionID := range s.messages {
		if sessionID != opts.IgnoreSessionID {
			sessionIDs = append(sessionIDs, sessionID)
		}
	}
	sort.Strings(sessionIDs)
	for _, sessionID := range sessionIDs {
		addHits(sessionID, s.messages[sessionID])
		if limit > 0 && len(candidates) >= limit {
			break
		}
	}

	return candidates
}

func (s *Store) searchMessagesVector(
	ctx context.Context,
	id string,
	opts base.SearchMessageOptions,
	candidateLimit int,
) ([]*searchCandidate, error) {
	queryValue := str.String(opts.Query)
	req := search.EmbeddingRequest{
		Model:        s.vectors.Model,
		Relationship: "query_vector_for_session_message_retrieval",
		Target:       "session_message_vectors",
		Inputs: []search.EmbeddingInput{{
			ID:         "query",
			Text:       queryValue.Trim(),
			SourceKind: search.SourceKindSessionMessage,
		}},
	}

	s.logSearchEvent("query embedding started", id, opts).
		Str("source_kind", string(search.SourceKindSessionMessage)).
		Str("embedding_model", req.Model).
		Msg("session search query embedding started for vector retrieval")

	embedding, err := s.vectors.Provider.Embed(ctx, req)
	if err != nil {
		applySafeErrorLog(s.logSearchEvent("query embedding failed", id, opts), err).
			Msg("session search query embedding failed")
		return nil, err
	}
	if err := search.ValidateEmbeddingResult(req, embedding); err != nil {
		applySafeErrorLog(s.logSearchEvent("query embedding validation failed", id, opts), err).
			Msg("session search query embedding validation failed")
		return nil, err
	}
	modelValue := str.String(embedding.Model)
	s.logSearchEvent("query embedding completed", id, opts).
		Int("dimensions", embedding.Dimensions).
		Str("source_kind", string(search.SourceKindSessionMessage)).
		Str("embedding_model", modelValue.Trim()).
		Msg("session search query embedding completed for vector retrieval")

	s.logSearchEvent("vector search started", id, opts).
		Int("limit", candidateLimit).
		Int("dimensions", embedding.Dimensions).
		Str("source_kind", string(search.SourceKindSessionMessage)).
		Str("target", "session_message_vectors").
		Str("embedding_model", s.vectors.Model).
		Msg("session search vector retrieval started for similar messages")
	roleValue := str.String(string(opts.Role))
	result, err := s.vectors.Store.Search(ctx, search.VectorSearchRequest{
		EmbeddingModel: s.vectors.Model,
		Dimensions:     embedding.Dimensions,
		QueryVector:    embedding.Items[0].Vector,
		Limit:          candidateLimit,
		Filter: search.VectorFilter{
			SourceKind:      search.SourceKindSessionMessage,
			SessionID:       id,
			IgnoreSessionID: opts.IgnoreSessionID,
			Role:            roleValue.Trim(),
			ToolName:        base.NormalizeMatchValue(opts.ToolName),
		},
	})
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

	candidates := s.vectorMatchesToCandidates(id, opts, result.Matches)

	s.logSearchEvent("vector matches resolved", id, opts).
		Int("match_count", len(result.Matches)).
		Int("candidate_count", len(candidates)).
		Msg("session search vector matches resolved")

	return candidates, nil
}

func (s *Store) vectorMatchesToCandidates(
	id string,
	opts base.SearchMessageOptions,
	matches []search.VectorSearchMatch,
) []*searchCandidate {
	if len(matches) == 0 {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	candidates := make([]*searchCandidate, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	chunking := search.NormalizeVectorChunkOptions(search.VectorChunkOptions{})
	if s.vectors != nil {
		chunking = s.vectors.Chunking
	}
	for idx, match := range matches {
		sessionID, messageID, ok := search.MessageRefFromSourceID(match.Record.SourceID)
		if !ok {
			continue
		}
		if _, ok := seen[match.Record.SourceID]; ok {
			continue
		}
		message, ok := getMessageByID(s.messages[sessionID], messageID)
		if !ok || !checkMessageMatchesSearchOptions(sessionID, message, id, opts) {
			continue
		}
		row, ok := search.MessageIndexRowForVectorRecord(
			search.MessageIndexRowsFromMessage(sessionID, message),
			match.Record.ID,
			chunking,
		)
		if !ok || search.IsVectorRecordStale(match.Record, row.Body) ||
			!search.MessageIndexRowMatchesSearchOptions(row, opts) {
			continue
		}

		candidates = append(candidates, &searchCandidate{
			CandidateMatch: search.CandidateMatch{
				SessionID:       sessionID,
				MatchedText:     row.Body,
				MatchedToolName: row.ToolName,
				VectorScore:     match.Score,
				VectorRank:      idx + 1,
				HasVector:       true,
			},
			Message: cloneMessages([]messages.Message{message})[0],
		})
		seen[match.Record.SourceID] = struct{}{}
	}

	return candidates
}

func getSearchCandidateKey(candidate *searchCandidate) string {
	if candidate == nil {
		return ""
	}

	return search.SourceIDForMessage(candidate.SessionID, candidate.Message.ID)
}

func isSearchCandidateLess(left *searchCandidate, right *searchCandidate) bool {
	return compareSearchCandidates(left, right) < 0
}

func (s *Store) rerankSearchCandidates(
	ctx context.Context,
	opts base.SearchMessageOptions,
	candidates searchCandidateSet,
) []*searchCandidate {
	items := candidates.Sorted(isSearchCandidateLess)
	if len(items) == 0 {
		return nil
	}

	maxCandidates := s.vectors.RerankMax
	if maxCandidates > 0 && len(items) > maxCandidates {
		items = items[:maxCandidates]
	}

	reranker := s.vectors.Reranker
	if reranker == nil {
		reranker = search.DeterministicReranker{}
	}
	rerankerName := s.rerankerName()

	s.logSearchEvent("rerank started", "", opts).
		Str("configured_reranker", rerankerName).
		Int("candidate_count", len(items)).
		Int("max_candidates", maxCandidates).
		Msg("session search rerank started")

	retrievalCandidates := make([]search.Candidate, 0, len(items))
	candidateByID := make(map[string]*searchCandidate, len(items))
	for _, candidate := range items {
		item := searchCandidateToRetrievalCandidate(candidate)
		retrievalCandidates = append(retrievalCandidates, item)
		candidateByID[item.ID] = candidate
	}
	queryValue2 := str.String(opts.Query)
	result, err := search.RerankWithFallback(ctx, reranker, search.DeterministicReranker{}, search.RerankRequest{
		Query:      queryValue2.Trim(),
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
		candidate := candidateByID[item.CandidateID]
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

func searchCandidateToRetrievalCandidate(candidate *searchCandidate) search.Candidate {
	matchedTextValue := str.String(candidate.MatchedText)
	text := matchedTextValue.Trim()
	if text == "" {
		contentValue := str.String(candidate.Message.Content)
		text = contentValue.Trim()
	}

	return search.Candidate{
		CreatedAt:    candidate.Message.CreatedAt,
		UpdatedAt:    candidate.Message.CreatedAt,
		ID:           search.SourceIDForMessage(candidate.SessionID, candidate.Message.ID),
		SourceKind:   search.SourceKindSessionMessage,
		SessionID:    candidate.SessionID,
		Text:         text,
		LexicalScore: candidate.LexicalScore,
		VectorScore:  candidate.VectorScore,
		FusedScore:   candidate.FusedScore,
		MessageID:    candidate.Message.ID,
	}
}

func searchCandidatesToSearchResults(
	candidates []*searchCandidate,
	opts base.SearchMessageOptions,
) []base.SearchMessageResult {
	groups := make(map[string][]*searchCandidate)
	for _, candidate := range candidates {
		groups[candidate.SessionID] = append(groups[candidate.SessionID], candidate)
	}

	sessionIDs := make([]string, 0, len(groups))
	for sessionID := range groups {
		sessionIDs = append(sessionIDs, sessionID)
		sort.SliceStable(groups[sessionID], func(i, j int) bool {
			return compareSearchCandidates(groups[sessionID][i], groups[sessionID][j]) < 0
		})
	}
	sort.SliceStable(sessionIDs, func(i, j int) bool {
		left := groups[sessionIDs[i]][0]
		right := groups[sessionIDs[j]][0]
		leftScore := search.CandidateRankingScore(left.HasRerank, left.RerankScore, left.FusedScore)
		rightScore := search.CandidateRankingScore(right.HasRerank, right.RerankScore, right.FusedScore)
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		if !left.Message.CreatedAt.Equal(right.Message.CreatedAt) {
			return left.Message.CreatedAt.After(right.Message.CreatedAt)
		}
		return sessionIDs[i] < sessionIDs[j]
	})
	if opts.MaxSessions > 0 && len(sessionIDs) > opts.MaxSessions {
		sessionIDs = sessionIDs[:opts.MaxSessions]
	}

	results := make([]base.SearchMessageResult, 0, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		sessionCandidates := groups[sessionID]
		matchCount := len(sessionCandidates)
		if opts.MaxMessagesPerSession > 0 && len(sessionCandidates) > opts.MaxMessagesPerSession {
			sessionCandidates = sessionCandidates[:opts.MaxMessagesPerSession]
		}

		hits := make([]base.SearchMessageHit, 0, len(sessionCandidates))
		var lastMatchedAt time.Time
		for _, candidate := range sessionCandidates {
			if candidate.Message.CreatedAt.After(lastMatchedAt) {
				lastMatchedAt = candidate.Message.CreatedAt
			}
			hits = append(hits, base.SearchMessageHit{
				SessionID:       candidate.SessionID,
				Message:         cloneMessages([]messages.Message{candidate.Message})[0],
				MatchedText:     candidate.MatchedText,
				MatchedToolName: candidate.MatchedToolName,
			})
		}

		results = append(results, base.SearchMessageResult{
			SessionID:     sessionID,
			LastMatchedAt: lastMatchedAt,
			MatchCount:    matchCount,
			Messages:      hits,
		})
	}

	return cloneSearchMessageResults(results)
}

func (s *Store) indexVectors(ctx context.Context, sessionID string, messages []messages.Message) error {
	records, err := s.vectorRecordsForMessages(ctx, sessionID, messages)
	if err != nil || len(records) == 0 {
		return err
	}

	return s.upsertVectorRecords(ctx, records)
}

func (s *Store) vectorRecordsForMessages(
	ctx context.Context,
	sessionID string,
	messages []messages.Message,
) ([]search.VectorRecord, error) {
	if s == nil || s.vectors == nil || len(messages) == 0 {
		return nil, nil
	}

	rows := make([]search.MessageIndexRow, 0, len(messages))
	for _, message := range messages {
		rows = append(rows, search.MessageIndexRowsFromMessage(sessionID, message)...)
	}
	inputs := search.VectorInputsFromIndexRows(rows, s.vectors.Chunking)
	if len(inputs) == 0 {
		return nil, nil
	}
	diagnostics := search.GetVectorInputDiagnostics(rows, inputs, s.vectors.Chunking)
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
	modelValue2 := str.String(req.Model)
	addVectorInputDiagnostics(s.logVectorEvent("embedding started"), diagnostics).
		Int("input_count", len(req.Inputs)).
		Int("message_count", len(messages)).
		Int("row_count", len(inputs)).
		Int("max_input_bytes", s.vectors.Chunking.MaxInputBytes).
		Int("max_document_bytes", s.vectors.Chunking.MaxDocumentBytes).
		Int("largest_input_bytes", search.GetMaxVectorInputBytes(inputs)).
		Int("attempt_increment", 1).
		Str("index_status", string(search.VectorIndexPending)).
		Str("embedding_model", modelValue2.Trim()).
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
	modelValue3 := str.String(result.Model)
	addVectorInputDiagnostics(s.logVectorEvent("embedding completed"), diagnostics).
		Int("input_count", len(req.Inputs)).
		Int("message_count", len(messages)).
		Int("row_count", len(inputs)).
		Int("dimensions", result.Dimensions).
		Int("max_input_bytes", s.vectors.Chunking.MaxInputBytes).
		Int("attempt_increment", 1).
		Str("index_status", string(search.VectorIndexReady)).
		Str("embedding_model", modelValue3.Trim()).
		Str("source_kind", string(search.SourceKindSessionMessage)).
		Msg("session vector indexing embedding completed for message rows")

	inputByID := make(map[string]search.VectorInput, len(inputs))
	for _, input := range inputs {
		inputByID[input.ID] = input
	}

	records := make([]search.VectorRecord, 0, len(result.Items))
	for _, item := range result.Items {
		input := inputByID[item.ID]
		records = append(records, search.VectorRecord{
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

	return records, nil
}

func (s *Store) upsertVectorRecords(ctx context.Context, records []search.VectorRecord) error {
	if s == nil || s.vectors == nil || len(records) == 0 {
		return nil
	}

	model := records[0].EmbeddingModel
	dimensions := records[0].Dimensions
	modelValue4 := str.String(model)
	s.logVectorEvent("upsert started").
		Int("record_count", len(records)).
		Str("embedding_model", modelValue4.Trim()).
		Int("dimensions", dimensions).
		Str("target", "session_message_vectors").
		Msg("session vector index upsert started for message rows")

	if err := s.vectors.Store.Upsert(ctx, records); err != nil {
		applySafeErrorLog(s.logVectorEvent("upsert failed"), err).
			Int("record_count", len(records)).
			Msg("session vector upsert failed")
		return err
	}
	modelValue5 := str.String(model)
	s.logVectorEvent("upsert completed").
		Int("record_count", len(records)).
		Str("embedding_model", modelValue5.Trim()).
		Int("dimensions", dimensions).
		Str("target", "session_message_vectors").
		Msg("session vector index upsert completed for message rows")

	return nil
}

func (s *Store) deleteVectorRows(ctx context.Context, sourceIDs []string) error {
	if s == nil || s.vectors == nil || len(sourceIDs) == 0 {
		return nil
	}

	sourceIDs = base.UniqueStrings(sourceIDs)
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

func (s *Store) handleVectorStoreError(err error) error {
	if err == nil || s == nil || s.vectors == nil || !s.vectors.Required {
		return nil
	}

	return err
}

func getMessageByID(msgs []messages.Message, messageID uint) (messages.Message, bool) {
	for _, message := range msgs {
		if message.ID == messageID {
			return message, true
		}
	}

	return messages.Message{}, false
}

func checkMessageMatchesSearchOptions(
	sessionID string,
	message messages.Message,
	id string,
	opts base.SearchMessageOptions,
) bool {
	if id != "" && sessionID != id {
		return false
	}
	if opts.IgnoreSessionID != "" && sessionID == opts.IgnoreSessionID {
		return false
	}
	if opts.Role != "" && message.Role != opts.Role {
		return false
	}

	return true
}

func compareSearchCandidates(left *searchCandidate, right *searchCandidate) int {
	return search.CompareCandidateOrder(
		search.CandidateRankingScore(left.HasRerank, left.RerankScore, left.FusedScore),
		search.CandidateRankingScore(right.HasRerank, right.RerankScore, right.FusedScore),
		left.Message.CreatedAt,
		right.Message.CreatedAt,
		left.SessionID,
		right.SessionID,
		left.Message.ID,
		right.Message.ID,
	)
}

func getLexicalScore(text string, query string) float64 {
	count := strings.Count(strings.ToLower(text), strings.ToLower(query))
	if count <= 0 {
		return 0
	}

	return -float64(count)
}

func (s *Store) rerankEnabled() bool {
	return s != nil && s.vectors != nil && s.vectors.Rerank
}

func (s *Store) rerankerName() string {
	if s == nil || s.vectors == nil || s.vectors.Reranker == nil {
		return search.RerankerDeterministic
	}

	nameValue := str.String(s.vectors.Reranker.Name())
	return nameValue.Normalized()
}

func (s *Store) diagnosticsEnabled() bool {
	return s != nil && s.vectors != nil && s.vectors.Diagnostics
}

func getSearchRerankResultName(result search.RerankResult, fallback string) string {
	rerankerValue := str.String(result.Reranker)
	if name := rerankerValue.Normalized(); name != "" {
		return name
	}
	fallbackValue := str.String(fallback)
	return fallbackValue.Normalized()
}

func getSearchResultMessageCount(results []base.SearchMessageResult) int {
	var count int
	for _, result := range results {
		count += len(result.Messages)
	}

	return count
}
