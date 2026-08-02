package episodic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xymorphic/morph/internal/constants"
	storage "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/internal/trace"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/str"
)

const (
	DefaultWindowSize      = 20
	MaxWindowSize          = 50
	DefaultMaxWindowChars  = 6000
	MaxWindowChars         = 20000
	DefaultMaxWindowTokens = DefaultMaxWindowChars / constants.RoughTokenCharRatio
	MaxWindowTokens        = MaxWindowChars / constants.RoughTokenCharRatio
	DefaultMaxWindows      = 5
	MaxWindows             = 50
	defaultTrigger         = "command"
)

const (
	defaultMaxTraceEventsPerWindow = 40
	maxTracePayloadChars           = 1200

	episodicSimilarScoreThreshold = 0.75
)

// Service extracts source-linked episodic memories from session message windows.
type Service struct {
	// manager loads session messages and updates background extraction checkpoints.
	manager StateManager
	// memory searches for existing memories and records proposed episodic memory items.
	memory MemoryRepository
	// extractor proposes curated episodic memory items from bounded message evidence.
	extractor candidateExtractor
	// nowFunc overrides the clock in tests and background scheduling.
	nowFunc func() time.Time
}

// NewService creates a new episodic memory extraction service.
func NewService(manager StateManager, repository MemoryRepository, extractor *LLMExtractor) (*Service, error) {
	if manager == nil {
		return nil, errors.New("state manager is required")
	}
	if repository == nil {
		return nil, errors.New("memory repository is required")
	}
	if extractor == nil {
		return nil, errors.New("memory episode extractor is required")
	}

	return &Service{manager: manager, memory: repository, extractor: extractor}, nil
}

// Extract extracts curated episodic memory items from a bounded message range.
//
// Extraction is source-window oriented. The service does not scan an entire
// transcript at once; it slices a session into bounded windows so model prompts
// stay small, provenance stays precise, and background extraction can checkpoint
// progress after each window.
func (s *Service) Extract(ctx context.Context, req Request) (Result, error) {
	started := s.now()
	if s == nil || s.manager == nil {
		return Result{}, errors.New("state manager is required")
	}
	if s.memory == nil {
		return Result{}, errors.New("memory repository is required")
	}
	if s.extractor == nil {
		return Result{}, errors.New("memory episode extractor is required")
	}

	normalized, err := s.normalizeRequest(ctx, req)
	if err != nil {
		recordFailure(req.Trace, normalized, err)
		return Result{}, err
	}

	traceField := map[string]any{
		"window_size":       normalized.WindowSize,
		"max_windows":       normalized.MaxWindows,
		"max_window_chars":  normalized.MaxWindowChars,
		"max_window_tokens": normalized.MaxWindowTokens,
	}
	recordTrace(req.Trace, trace.EvtMemoryExtractionStarted, getTracePayload(normalized, traceField))
	logExtraction("started to extract episodic memory candidates", normalized, traceField)

	result := Result{SessionID: normalized.SessionID}
	for start := normalized.OffsetStart; start < normalized.OffsetEnd; start += normalized.WindowSize {
		if normalized.MaxWindows > 0 && len(result.Windows) >= normalized.MaxWindows {
			break
		}

		end := min(start+normalized.WindowSize, normalized.OffsetEnd)
		window := sourceWindow{Start: start, End: end}
		windowResult, err := s.extractWindow(ctx, normalized, window)
		if err != nil {
			recordFailure(req.Trace, normalized.withRange(window.Start, window.End), err)
			return Result{}, err
		}

		result.Windows = append(result.Windows, windowResult)
		result.MessageCount += windowResult.MessageCount
		result.CandidateCount += windowResult.CandidateCount
		result.WriteCount += windowResult.WriteCount
		result.SkipCount += windowResult.SkipCount

		// Background extraction advances after every successfully processed
		// window. Foreground/tool extraction does not move this checkpoint because
		// callers may intentionally inspect arbitrary historical ranges.
		if normalized.Trigger == backgroundTrigger {
			offset := windowResult.OffsetEnd
			if err := s.manager.UpdateCheckpoints(ctx, normalized.SessionID, storage.CheckpointPatch{
				EpisodicOffset: &offset,
			}); err != nil {
				recordFailure(req.Trace, normalized.withRange(window.Start, window.End), err)
				return Result{}, err
			}
		}
	}

	duration := s.now().Sub(started)
	traceFields := map[string]any{
		"window_count":    len(result.Windows),
		"message_count":   result.MessageCount,
		"candidate_count": result.CandidateCount,
		"write_count":     result.WriteCount,
		"skip_count":      result.SkipCount,
		"duration_ms":     duration.Milliseconds(),
	}
	recordTrace(req.Trace, trace.EvtMemoryExtractionCompleted, getTracePayload(normalized, traceFields))
	logExtraction("completed episodic memory candidate extraction", normalized, traceFields)

	return result, nil
}

func (s Service) extractWindow(
	ctx context.Context,
	req normalizedRequest,
	window sourceWindow,
) (WindowResult, error) {
	windowReq := req.withRange(window.Start, window.End)
	result := WindowResult{
		OffsetStart: window.Start,
		OffsetEnd:   window.End,
	}

	existing, err := s.memory.Search(ctx, storage.MemorySearchQuery{
		RerankerUseCase: storage.MemoryRerankerUseCaseEpisodicExtraction,
		Kinds:           []storage.MemoryKind{storage.MemoryKindEpisodic},
		Statuses:        []storage.MemoryStatus{storage.MemoryStatusCandidate, storage.MemoryStatusActive},
		// Treat the source window as complete once any active/candidate memory exists
		// for it, even if that window produced multiple candidate IDs.
		Tags:  []string{getSourceRangeTag(req.SessionID, window.Start, window.End)},
		Limit: 1,
	})
	if err != nil {
		return WindowResult{}, err
	}
	if len(existing.Hits) > 0 {
		// A source-window tag is the first duplicate guard. It prevents a
		// background job from reprocessing a window it has already turned into at
		// least one candidate.
		result.SkipCount = len(existing.Hits)
		for _, hit := range existing.Hits {
			iDValue := str.String(hit.Item.ID)
			id := iDValue.Trim()
			if id != "" {
				result.SkippedIDs = append(result.SkippedIDs, id)
			}
		}
		traceFields := map[string]any{"memory_ids": result.SkippedIDs, "checkpoint_state": "complete"}
		recordTrace(req.Trace, trace.EvtMemoryExtractionDuplicateSkipped, getTracePayload(windowReq, traceFields))
		logExtraction("skipped completed source window", windowReq, traceFields)
		return result, nil
	}

	messages, err := s.manager.GetMessages(ctx, req.SessionID, storage.MessageQueryOptions{
		Offset: window.Start,
		Limit:  window.End - window.Start,
	})
	if err != nil {
		return WindowResult{}, err
	}

	traceFields := map[string]any{"message_count": len(messages)}
	recordTrace(req.Trace, trace.EvtMemoryExtractionWindowLoaded, getTracePayload(windowReq, traceFields))
	logExtraction("loaded source message window", windowReq, traceFields)

	candidates, rejections, err := s.candidatesFromMessages(ctx, req, window, messages)
	if err != nil {
		return WindowResult{}, err
	}

	candidateCount := len(candidates)
	traceFields = map[string]any{"candidate_count": candidateCount}
	recordTrace(req.Trace, trace.EvtMemoryExtractionExtractorRequested, getTracePayload(windowReq, traceFields))
	recordTrace(req.Trace, trace.EvtMemoryExtractionCandidates, getTracePayload(windowReq, traceFields))
	traceFields = map[string]any{"message_count": len(messages), "candidate_count": candidateCount}
	logExtraction("received extractor candidate set", windowReq, traceFields)

	for _, rejection := range rejections {
		traceFields = map[string]any{"candidate_kind": rejection.Kind, "rejection_reason": rejection.Reason}
		recordTrace(req.Trace, trace.EvtMemoryExtractionCandidateRejected, getTracePayload(windowReq, traceFields))
		logExtraction("candidate_rejected", windowReq, traceFields)
	}

	for _, candidate := range candidates {
		traceFields = map[string]any{
			"memory_id":       candidate.ID,
			"candidate_kind":  candidate.Metadata["candidate_kind"],
			"confidence":      candidate.Confidence,
			"source_quality":  candidate.Metadata["source_quality"],
			"usefulness":      candidate.Metadata["usefulness"],
			"admission_state": candidate.Status,
		}
		recordTrace(req.Trace, trace.EvtMemoryExtractionCandidateGenerated, getTracePayload(windowReq, traceFields))
		recordTrace(req.Trace, trace.EvtMemoryExtractionConfidenceScored, getTracePayload(windowReq, traceFields))
		recordTrace(req.Trace, trace.EvtMemoryExtractionAdmissionMorphoff, getTracePayload(windowReq, traceFields))
		logExtraction("generated episodic candidate proposal", windowReq, traceFields)
	}

	result.MessageCount = len(messages)
	result.CandidateCount = candidateCount

	if len(candidates) == 0 {
		return result, nil
	}

	for _, candidate := range candidates {
		// The extractor and admission pipeline are not the final duplicate guard.
		// Overlapping windows can still produce the same memory, so we search
		// existing episodic candidate/active memory before writing.
		rejection, rejectionFields, err := s.checkEpisodicCandidateRedundancy(ctx, candidate)
		if err != nil {
			return WindowResult{}, err
		}
		if rejection != "" {
			result.SkipCount++
			traceFields = map[string]any{"candidate_kind": candidate.Metadata["candidate_kind"], "rejection_reason": rejection}
			for key, value := range rejectionFields {
				traceFields[key] = value
			}
			recordTrace(req.Trace, trace.EvtMemoryExtractionCandidateRejected, getTracePayload(windowReq, traceFields))
			logExtraction("rejected episodic candidate before write", windowReq, traceFields)
			continue
		}

		item, err := s.memory.RecordEpisode(ctx, EpisodeRecord{Item: candidate})
		if err != nil {
			return WindowResult{}, err
		}

		result.WriteCount++
		result.MemoryIDs = append(result.MemoryIDs, item.ID)

		traceFields = map[string]any{
			"memory_id":       item.ID,
			"candidate_kind":  item.Metadata["candidate_kind"],
			"confidence":      item.Confidence,
			"write_status":    "candidate",
			"admission_state": item.Status,
		}
		recordTrace(req.Trace, trace.EvtMemoryExtractionMemoryWritten, getTracePayload(windowReq, traceFields))
		logExtraction("wrote episodic candidate memory", windowReq, traceFields)
	}

	return result, nil
}

func (s Service) checkEpisodicCandidateRedundancy(
	ctx context.Context,
	item storage.MemoryItem,
) (string, map[string]any, error) {
	text := getEpisodicSearchText(item)
	if text == "" {
		return "", nil, nil
	}

	// This dedupe pass is intentionally before RecordEpisode. It catches
	// duplicate candidates generated from overlapping windows or repeated tool
	// invocations, including candidates whose source-range tag differs.
	result, err := s.memory.Search(ctx, storage.MemorySearchQuery{
		Text:            text,
		RerankerUseCase: storage.MemoryRerankerUseCaseEpisodicExtraction,
		Kinds:           []storage.MemoryKind{storage.MemoryKindEpisodic},
		Statuses:        []storage.MemoryStatus{storage.MemoryStatusCandidate, storage.MemoryStatusActive},
		Limit:           5,
	})
	if err != nil {
		return "", nil, err
	}

	for _, hit := range result.Hits {
		related := hit.Item
		iDValue2 := str.String(related.ID)
		iDValue3 := str.String(item.ID)
		if iDValue2.Trim() == iDValue3.Trim() {
			continue
		}
		fields := getEpisodicRejectionTraceFields(item, related, hit.Score)
		if normalizeEpisodicText(related) == normalizeEpisodicText(item) {
			fields["match_type"] = "exact_normalized_text"
			return "duplicate_episodic_memory", fields, nil
		}
		if hasSameEpisodeCandidateKind(related, item) && hit.Score >= episodicSimilarScoreThreshold {
			fields["match_type"] = "same_candidate_kind_above_score_threshold"
			fields["similar_score_threshold"] = episodicSimilarScoreThreshold
			return "similar_episodic_memory", fields, nil
		}
	}

	return "", nil, nil
}

func getEpisodicRejectionTraceFields(
	candidate storage.MemoryItem,
	related storage.MemoryItem,
	score float64,
) map[string]any {
	iDValue4 := str.String(candidate.ID)
	titleValue := str.String(candidate.Title)
	textValue := str.String(candidate.Text)
	iDValue5 := str.String(related.ID)
	metadataValue := str.String(related.Metadata["candidate_kind"])
	titleValue2 := str.String(related.Title)
	return map[string]any{
		"candidate_memory_id":    iDValue4.Trim(),
		"candidate_title":        truncateRunes(titleValue.Trim(), 120),
		"candidate_text_chars":   len([]rune(textValue.Trim())),
		"related_memory_id":      iDValue5.Trim(),
		"related_memory_kind":    string(related.Kind),
		"related_memory_status":  string(related.Status),
		"related_candidate_kind": metadataValue.Trim(),
		"related_title":          truncateRunes(titleValue2.Trim(), 120),
		"related_score":          score,
	}
}

func (s Service) normalizeRequest(ctx context.Context, req Request) (normalizedRequest, error) {
	sessionIDValue := str.String(req.SessionID)
	sessionID := sessionIDValue.Trim()
	if sessionID == "" {
		currentSessionID, err := s.manager.CurrentSession(ctx)
		if err != nil {
			return normalizedRequest{}, err
		}
		sessionID = currentSessionID
	}

	count, err := s.manager.CountMessages(ctx, sessionID, storage.MessageQueryOptions{})
	if err != nil {
		return normalizedRequest{}, err
	}

	start := 0
	if req.OffsetStart != nil {
		start = *req.OffsetStart
	}
	end := count
	if req.OffsetEnd != nil {
		end = *req.OffsetEnd
	}
	if start < 0 {
		return normalizedRequest{}, errors.New("offset_start must be greater than or equal to zero")
	}
	if end < start || (req.OffsetStart != nil && req.OffsetEnd != nil && end == start) {
		return normalizedRequest{}, errors.New("offset_end must be greater than offset_start")
	}
	if end > count {
		end = count
	}

	windowSize := req.WindowSize
	if windowSize <= 0 {
		windowSize = DefaultWindowSize
	}
	if windowSize > MaxWindowSize {
		windowSize = MaxWindowSize
	}
	if req.MaxWindows < 0 {
		return normalizedRequest{}, errors.New("max_windows must be greater than or equal to zero")
	}

	maxChars := req.MaxWindowChars
	if maxChars <= 0 {
		maxChars = DefaultMaxWindowChars
	}
	if maxChars > MaxWindowChars {
		maxChars = MaxWindowChars
	}
	if req.MaxWindowTokens < 0 {
		return normalizedRequest{}, errors.New("max_window_tokens must be greater than or equal to zero")
	}

	maxTokens := req.MaxWindowTokens
	if maxTokens <= 0 {
		maxTokens = DefaultMaxWindowTokens
	}
	if maxTokens > MaxWindowTokens {
		maxTokens = MaxWindowTokens
	}
	triggerValue := str.String(req.Trigger)
	trigger := triggerValue.Trim()
	if trigger == "" {
		trigger = defaultTrigger
	}

	return normalizedRequest{
		SessionID:       sessionID,
		OffsetStart:     start,
		OffsetEnd:       end,
		WindowSize:      windowSize,
		MaxWindows:      req.MaxWindows,
		MaxWindowChars:  maxChars,
		MaxWindowTokens: maxTokens,
		Trigger:         trigger,
		Trace:           req.Trace,
	}, nil
}

func (s *Service) now() time.Time {
	if s != nil && s.nowFunc != nil {
		return s.nowFunc().UTC()
	}

	return time.Now().UTC()
}

func (r normalizedRequest) withRange(start int, end int) normalizedRequest {
	r.OffsetStart = start
	r.OffsetEnd = end
	return r
}

func (s Service) candidatesFromMessages(
	ctx context.Context,
	req normalizedRequest,
	window sourceWindow,
	messages []morphmsg.Message,
) ([]storage.MemoryItem, []candidateRejection, error) {
	evidence := getEvidenceFromMessages(window, messages)
	if len(evidence.Lines) == 0 {
		return nil, []candidateRejection{{Kind: "window", Reason: "empty_window"}}, nil
	}
	if s.extractor == nil {
		return nil, nil, errors.New("memory episode extractor is required")
	}

	// Trace evidence augments the transcript with task-level context such as
	// tool calls and outcomes. It is optional because not every store supports
	// trace queries.
	traceEvents, err := s.loadTraceEvidence(ctx, req.SessionID)
	if err != nil {
		return nil, nil, err
	}
	evidence.TraceRefs = getTraceEvidenceRefs(traceEvents)
	result, err := s.extractor.ExtractCandidates(ctx, CandidateRequest{
		SessionID:   req.SessionID,
		Start:       window.Start,
		End:         window.End,
		Messages:    evidence.Lines,
		TraceEvents: traceEvents,
		MaxChars:    req.getWindowCharLimit(),
	})
	if err != nil {
		return nil, nil, err
	}

	items := make([]storage.MemoryItem, 0, len(result.Candidates))
	rejections := append([]candidateRejection(nil), result.Rejections...)
	seen := make(map[string]struct{}, len(result.Candidates))
	for _, candidate := range result.Candidates {
		item, ok := episodeCandidateToMemoryItem(req, window, evidence, candidate)
		if !ok {
			rejections = append(rejections, candidateRejection{Kind: candidate.Kind, Reason: "empty_candidate"})
			continue
		}
		if _, ok := seen[item.ID]; ok {
			// Candidate IDs are deterministic over source window, kind, content,
			// metadata, and source links. A repeated ID in one model response means
			// the model produced the same candidate twice.
			rejections = append(rejections, candidateRejection{Kind: candidate.Kind, Reason: "duplicate_candidate"})
			continue
		}

		seen[item.ID] = struct{}{}
		items = append(items, item)
	}
	items, rejections = admitCandidateItems(items, rejections)

	if len(items) == 0 && len(rejections) == 0 {
		rejections = append(rejections, candidateRejection{Kind: "window", Reason: "no_curated_candidate"})
	}

	return items, rejections, nil
}

func (s Service) loadTraceEvidence(ctx context.Context, sessionID string) ([]taskTraceEvidence, error) {
	if s.manager == nil {
		return nil, nil
	}

	result, err := s.manager.ListTraceEvents(ctx, storage.TraceQuery{
		SessionID: sessionID,
		Types:     trace.EpisodicMemoryTraceEventTypes(),
		Limit:     defaultMaxTraceEventsPerWindow,
	})
	if err != nil {
		if errors.Is(err, storage.ErrTraceStoreUnsupported) {
			return nil, nil
		}

		return nil, err
	}

	traces := make([]taskTraceEvidence, 0, len(result.Events))
	for _, event := range result.Events {
		if !trace.IsEpisodicMemoryTraceEventType(event.Type) {
			continue
		}

		trimmedValueValue := str.String(event.Type)
		traces = append(traces, taskTraceEvidence{
			Ref:       getTraceEventRef(event),
			Type:      trimmedValueValue.Trim(),
			Timestamp: getTraceEventTimestamp(event),
			Payload:   tracePayloadToText(event.Payload),
		})
	}

	return traces, nil
}

func getTraceEventRef(event storage.TraceEvent) string {
	if event.Sequence > 0 {
		return "trace:" + strconv.Itoa(event.Sequence)
	}
	if event.ID > 0 {
		return "trace_id:" + strconv.FormatUint(uint64(event.ID), 10)
	}

	return "trace:unknown"
}

func getTraceEventTimestamp(event storage.TraceEvent) string {
	if event.Timestamp.IsZero() {
		return ""
	}

	return event.Timestamp.UTC().Format(time.RFC3339Nano)
}

func tracePayloadToText(payload any) string {
	if payload == nil {
		return ""
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return truncateRunes(string(data), maxTracePayloadChars)
}

func getEpisodicSearchText(item storage.MemoryItem) string {
	textValue2 := str.String(item.Text)
	text := textValue2.Trim()
	if text == "" {
		titleValue3 := str.String(item.Title)
		text = titleValue3.Trim()
	}
	if len([]rune(text)) > 240 {
		text = string([]rune(text)[:240])
	}
	return text
}

func normalizeEpisodicText(item storage.MemoryItem) string {
	trimmedValue := str.String(item.Title + "\n" + item.Text)
	return strings.Join(strings.Fields(trimmedValue.Normalized()), " ")
}

func hasSameEpisodeCandidateKind(a storage.MemoryItem, b storage.MemoryItem) bool {
	metadataValue2 := str.String(a.Metadata["candidate_kind"])
	metadataValue3 := str.String(b.Metadata["candidate_kind"])
	return metadataValue2.Trim() == metadataValue3.Trim()
}

func getTraceEvidenceRefs(events []taskTraceEvidence) []string {
	refs := make([]string, 0, len(events))
	for _, event := range events {
		refValue := str.String(event.Ref)
		if ref := refValue.Trim(); ref != "" {
			refs = append(refs, ref)
		}
	}

	return refs
}

func getEvidenceFromMessages(window sourceWindow, messages []morphmsg.Message) messageEvidence {
	messageIDs := make([]uint, 0, len(messages))
	offsets := make([]int, 0, len(messages))
	lines := make([]string, 0, len(messages))
	for idx, message := range messages {
		line := messageToLine(message)
		if line == "" {
			continue
		}
		messageIDs = append(messageIDs, message.ID)
		offsets = append(offsets, window.Start+idx)
		lines = append(lines, line)
	}

	text := strings.Join(lines, "\n")
	return messageEvidence{
		MessageIDs: messageIDs,
		Offsets:    offsets,
		Lines:      lines,
		Text:       text,
		LowerText:  strings.ToLower(text),
	}
}

func episodeCandidateToMemoryItem(
	req normalizedRequest,
	window sourceWindow,
	evidence messageEvidence,
	candidate episodeCandidate,
) (storage.MemoryItem, bool) {
	kindValue := str.String(candidate.Kind)
	candidate.Kind = kindValue.Trim()
	if !isValidEpisodeCandidateKind(candidate.Kind) {
		return storage.MemoryItem{}, false
	}
	truncateRunesValue := str.String(truncateRunes(candidate.Text, req.getWindowCharLimit()))
	text := truncateRunesValue.Trim()
	titleValue4 := str.String(candidate.Title)
	title := titleValue4.Trim()
	if title == "" && text == "" {
		return storage.MemoryItem{}, false
	}

	metadata := map[string]string{
		"source_session_id": req.SessionID,
		"source_start":      strconv.Itoa(window.Start),
		"source_end":        strconv.Itoa(window.End),
		"trigger":           req.Trigger,
		"candidate_kind":    candidate.Kind,
		"source_quality":    getSourceQuality(evidence),
		"usefulness":        getUsefulness(candidate.Kind),
		"recency":           "source_window",
		"uncertainty":       getUncertainty(candidate.Confidence),
	}

	// Model metadata is allowed to enrich provider metadata but not remove
	// provenance. Empty values are ignored so storage stays compact and searchable.
	if len(evidence.TraceRefs) > 0 {
		metadata["available_trace_event_count"] = strconv.Itoa(len(evidence.TraceRefs))
	}
	for key, value := range candidate.Metadata {
		if value := normalizeMetadataValue(value); value != "" {
			metadata[key] = value
		}
	}

	sourceLinks := candidate.SourceLinks
	if len(sourceLinks) == 0 {
		sourceLinks = []storage.MemorySourceLink{{
			SessionID:     req.SessionID,
			MessageIDs:    evidence.MessageIDs,
			Offsets:       evidence.Offsets,
			CreatedBy:     req.Trigger,
			CreatedReason: "curated_episodic_memory_extraction",
		}}
	}
	sourceTag := getSourceRangeTag(req.SessionID, window.Start, window.End)
	// The stable ID gives repeated extraction of the same evidence the same
	// candidate identity, while source tags let background jobs skip completed
	// windows cheaply.
	return storage.MemoryItem{
		ID:          getCandidateMemoryID(req, window, candidate.Kind, title, text, metadata, sourceLinks),
		Kind:        storage.MemoryKindEpisodic,
		Status:      storage.MemoryStatusCandidate,
		Title:       title,
		Text:        text,
		Tags:        []string{"episodic", "curated", candidate.Kind, sourceTag},
		Metadata:    metadata,
		SourceLinks: sourceLinks,
		Confidence:  clampConfidence(candidate.Confidence),
	}, true
}

func normalizeMetadataValue(value string) string {
	valueText := str.String(value)
	return valueText.Trim()
}

func getSourceQuality(evidence messageEvidence) string {
	if len(evidence.MessageIDs) > 0 && len(evidence.Offsets) > 0 {
		return "high"
	}

	return "medium"
}

func getUncertainty(confidence float64) string {
	if confidence >= 0.80 {
		return "low"
	}
	if confidence >= 0.65 {
		return "medium"
	}

	return "high"
}

func clampConfidence(confidence float64) float64 {
	if confidence < 0 {
		return 0
	}
	if confidence > 1 {
		return 1
	}

	return confidence
}

func (r normalizedRequest) getWindowCharLimit() int {
	limit := r.MaxWindowChars
	tokenChars := r.MaxWindowTokens * constants.RoughTokenCharRatio
	if limit <= 0 || tokenChars < limit {
		limit = tokenChars
	}
	return limit
}

func messageToLine(message morphmsg.Message) string {
	parts := make([]string, 0, 2+len(message.ToolCalls))
	sanitizeUTF8Value := str.String(sanitizeUTF8(message.Content))
	if content := sanitizeUTF8Value.Trim(); content != "" {
		parts = append(parts, content)
	}
	for _, call := range message.ToolCalls {
		nameValue := str.String(call.Name)
		name := nameValue.Trim()
		sanitizeUTF8Value2 := str.String(sanitizeUTF8(call.Input))
		input := sanitizeUTF8Value2.Trim()
		if name == "" && input == "" {
			continue
		}
		trimmedValue2 := str.String("tool_call " + name + ": " + input)
		parts = append(parts, trimmedValue2.Trim())
	}
	if len(parts) == 0 {
		return ""
	}
	roleValue := str.String(string(message.Role))
	role := roleValue.Trim()
	if role == "" {
		role = "message"
	}
	nameValue2 := str.String(message.Name)
	if toolName := nameValue2.Trim(); message.Role == morphmsg.RoleTool && toolName != "" {
		role += ":" + toolName
	}
	return role + ": " + strings.Join(parts, " ")
}

func getCandidateMemoryID(
	req normalizedRequest,
	window sourceWindow,
	kind string,
	title string,
	text string,
	metadata map[string]string,
	sourceLinks []storage.MemorySourceLink,
) string {
	return getEpisodicMemoryIDPrefix() + getSourceRangeHash(
		getCandidateMemoryIDSource(req.SessionID, kind, title, text, metadata, sourceLinks),
		window.Start,
		window.End,
	)
}

func getEpisodicMemoryIDPrefix() string {
	return "mem_" + string(storage.MemoryKindEpisodic) + "_"
}

func getCandidateMemoryIDSource(
	sessionID string,
	kind string,
	title string,
	text string,
	metadata map[string]string,
	sourceLinks []storage.MemorySourceLink,
) string {
	sessionIDValue2 := str.String(sessionID)
	kindValue2 := str.String(kind)
	parts := []string{sessionIDValue2.
		Trim(), kindValue2.
		Trim(), normalizeMemoryIDText(title),
		normalizeMemoryIDText(text),
	}
	for _, key := range getEpisodeIdentityMetadataKeys() {
		metadataValue4 := str.String(metadata[key])
		if value := metadataValue4.Trim(); value != "" {
			parts = append(parts, key+"="+normalizeMemoryIDText(value))
		}
	}
	for _, link := range sourceLinks {
		sessionIDValue3 := str.String(link.SessionID)
		parts = append(parts, sessionIDValue3.Trim())
		parts = append(parts, uintSliceToMemoryIDText(link.MessageIDs))
		parts = append(parts, intSliceToMemoryIDText(link.Offsets))
	}

	return strings.Join(parts, "\n")
}

func normalizeMemoryIDText(value string) string {
	value2 := str.String(value)
	return strings.Join(strings.Fields(value2.Normalized()), " ")
}

func uintSliceToMemoryIDText(values []uint) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.FormatUint(uint64(value), 10))
	}

	return strings.Join(parts, ",")
}

func intSliceToMemoryIDText(values []int) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.Itoa(value))
	}

	return strings.Join(parts, ",")
}

func getSourceRangeTag(sessionID string, start int, end int) string {
	return "source-range-" + getSourceRangeHash(sessionID, start, end)
}

func getSourceRangeHash(id string, start int, end int) string {
	idValue := str.String(id)
	sum := sha256.Sum256(fmt.Appendf(nil, "%s:%d:%d", idValue.Trim(), start, end))
	return hex.EncodeToString(sum[:8])
}

func truncateRunes(value string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}

	runes := []rune(value)
	if len(runes) <= maxChars {
		return value
	}
	return string(runes[:maxChars])
}

func sanitizeUTF8(value string) string {
	if utf8.ValidString(value) {
		return value
	}

	return strings.ToValidUTF8(value, "")
}
