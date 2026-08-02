package memory

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	statecore "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/internal/trace"
	"github.com/xymorphic/morph/pkg/str"
)

const (
	defaultReflectionLimit        = 10
	maxReflectionLimit            = 50
	defaultReflectionRelatedLimit = 3
	maxReflectionRelatedLimit     = 10

	defaultReflectionBackgroundInterval = time.Minute

	reflectionSimilarScoreThreshold = 0.75
)

// Reflect consolidates unreflected episodic memories into new candidates.
//
// The shape of this method is important:
//  1. load source episodic memories that have not yet been reflected
//  2. load related durable/context memories to help the generator avoid repeats
//  3. ask the generator for candidates
//  4. normalize, validate, dedupe, and write candidates
//  5. mark only the source memories as reflected
//
// Reflection can produce more reflected memories later, but duplicate rejection
// prevents the system from writing the same insight repeatedly.
func (p *MemoryProvider) Reflect(ctx context.Context, req ReflectionRequest) (ReflectionResult, error) {
	started := time.Now().UTC()
	if p == nil || p.manager == nil {
		return ReflectionResult{}, errors.New("memory provider is required")
	}
	if p.reflectionGenerator == nil {
		return ReflectionResult{}, errors.New("memory reflection is not configured")
	}

	normalized, err := p.normalizeReflectionRequest(ctx, req)
	if err != nil {
		p.recordReflectionFailure(ctx, ReflectionResult{}, err)
		return ReflectionResult{}, err
	}

	fields := buildObservationFields(p.Name(), "reflect", map[string]any{
		"session_id":    normalized.SessionID,
		"limit":         normalized.Limit,
		"related_limit": normalized.RelatedLimit,
	})
	logDebugAndTrace(ctx, p.observability(), "memory reflection started to consolidate episodic sources", trace.EvtMemoryReflectionStarted, fields)

	result := ReflectionResult{SessionID: normalized.SessionID}
	sources, err := p.loadReflectionSources(ctx, normalized)
	if err != nil {
		p.recordReflectionFailure(ctx, result, err)
		return ReflectionResult{}, err
	}
	result.SourceCount = len(sources)

	fields = buildObservationFields(p.Name(), "reflect", map[string]any{
		"session_id":   normalized.SessionID,
		"source_count": result.SourceCount,
		"source_kind":  string(KindEpisodic),
		"source_state": "unreflected_candidate_or_active",
	})
	logDebugAndTrace(ctx, p.observability(), "memory reflection loaded unreflected episodic sources", trace.EvtMemoryReflectionSourceLoaded, fields)

	if len(sources) == 0 {
		p.recordReflectionCompleted(ctx, result, started)
		return result, nil
	}

	related, err := p.loadReflectionRelated(ctx, sources, normalized)
	if err != nil {
		p.recordReflectionFailure(ctx, result, err)
		return ReflectionResult{}, err
	}
	result.RelatedCount = len(related)

	fields = buildObservationFields(p.Name(), "reflect", map[string]any{
		"session_id":    normalized.SessionID,
		"related_count": result.RelatedCount,
		"bounded_limit": normalized.RelatedLimit,
		"source_count":  result.SourceCount,
		"relationship":  "context_for_reflection_and_dedupe",
	})
	logDebugAndTrace(ctx, p.observability(), "memory reflection loaded related context memories", trace.EvtMemoryReflectionRelatedLoaded, fields)

	generated, err := p.reflectionGenerator.GenerateReflectionCandidates(ctx, ReflectionGenerationRequest{
		SessionID: normalized.SessionID,
		Sources:   cloneMemoryItems(sources),
		Related:   cloneMemoryItems(related),
		Limit:     normalized.Limit,
	})
	if err != nil {
		p.recordReflectionFailure(ctx, result, err)
		return ReflectionResult{}, err
	}

	sourceLinks := getSourceLinks(sources)
	sourceIDs := getMemoryIDs(sources)
	written := make([]MemoryItem, 0, normalized.Limit)
	for _, candidate := range limitReflectionItems(generated.Items, normalized.Limit) {
		// Model output is treated as a proposal. The provider overwrites IDs,
		// source links, reflection metadata, tags, and status so provenance is
		// trustworthy even when the model omits or invents fields.
		item, ok, rejection := prepareReflectionCandidate(
			candidate,
			normalized.SessionID,
			sourceLinks,
			sourceIDs,
		)
		if !ok {
			p.recordReflectionRejection(ctx, normalized.SessionID, rejection)
			continue
		}

		// Check duplicates after provider normalization. This catches both
		// same-batch duplicates and already-written reflected memories across
		// memory kinds.
		rejection, err = p.checkReflectionCandidateRedundancy(ctx, item, written)
		if err != nil {
			p.recordReflectionFailure(ctx, result, err)
			return ReflectionResult{}, err
		}
		if rejection != "" {
			p.recordReflectionRejection(ctx, normalized.SessionID, rejection)
			continue
		}

		fields = buildObservationFields(p.Name(), "reflect", map[string]any{
			"session_id":  normalized.SessionID,
			"memory_id":   item.ID,
			"memory_kind": string(item.Kind),
			"confidence":  item.Confidence,
		})
		logDebugAndTrace(ctx, p.observability(), "memory reflection candidate generated", trace.EvtMemoryReflectionCandidateGenerated, fields)

		item, err = p.recordReflectionCandidate(ctx, item)
		if err != nil {
			p.recordReflectionFailure(ctx, result, err)
			return ReflectionResult{}, err
		}

		result.WriteCount++
		result.Items = append(result.Items, item.Clone())
		written = append(written, item.Clone())

		fields = buildObservationFields(p.Name(), "reflect", map[string]any{
			"session_id":   normalized.SessionID,
			"memory_id":    item.ID,
			"memory_kind":  string(item.Kind),
			"write_status": string(item.Status),
		})
		logDebugAndTrace(ctx, p.observability(), "memory reflection candidate written", trace.EvtMemoryReflectionMemoryWritten, fields)
	}

	if err := p.markReflectionSourcesReflected(ctx, sources); err != nil {
		p.recordReflectionFailure(ctx, result, err)
		return ReflectionResult{}, err
	}

	p.recordReflectionCompleted(ctx, result, started)
	return result, nil
}

func normalizeReflectionBackgroundOptions(opts ReflectionBackgroundOptions) ReflectionBackgroundOptions {
	if opts.Interval <= 0 {
		opts.Interval = defaultReflectionBackgroundInterval
	}
	if opts.Limit <= 0 {
		opts.Limit = defaultReflectionLimit
	}
	if opts.Limit > maxReflectionLimit {
		opts.Limit = maxReflectionLimit
	}
	if opts.RelatedLimit <= 0 {
		opts.RelatedLimit = defaultReflectionRelatedLimit
	}
	if opts.RelatedLimit > maxReflectionRelatedLimit {
		opts.RelatedLimit = maxReflectionRelatedLimit
	}

	return opts
}

func (p *MemoryProvider) startReflectionBackground(ctx context.Context) error {
	if !p.reflectionBackground.Enabled {
		return nil
	}
	if p.reflectionGenerator == nil {
		return errors.New("memory reflection is not configured")
	}

	opts := normalizeReflectionBackgroundOptions(p.reflectionBackground)
	p.reflectionBackgroundStartOnce.Do(func() {
		go p.runReflectionBackgroundLoop(ctx, opts)
	})

	return nil
}

func (p *MemoryProvider) runReflectionBackgroundLoop(ctx context.Context, opts ReflectionBackgroundOptions) {
	ticker := time.NewTicker(opts.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = p.RunReflectionBackground(ctx, opts)
		}
	}
}

func (p *MemoryProvider) RunReflectionBackground(
	ctx context.Context,
	opts ReflectionBackgroundOptions,
) (ReflectionResult, error) {
	if p == nil || p.manager == nil {
		return ReflectionResult{}, errors.New("memory provider is required")
	}

	manager, ok := p.manager.(reflectionBackgroundStateManager)
	if !ok {
		return ReflectionResult{}, errors.New("session listing is required")
	}

	opts = normalizeReflectionBackgroundOptions(opts)
	sessions, err := manager.ListSessions(ctx)
	if err != nil {
		return ReflectionResult{}, err
	}

	var firstErr error
	result := ReflectionResult{}
	for _, session := range sessions {
		iDValue := str.String(session.ID)
		sessionID := iDValue.Trim()
		if sessionID == "" {
			continue
		}

		messageCount, err := p.manager.CountMessages(ctx, sessionID, statecore.MessageQueryOptions{})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}

		shouldReflect, err := p.shouldReflectSession(
			ctx,
			sessionID,
			session,
			messageCount,
		)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !shouldReflect {
			continue
		}

		sessionResult, err := p.Reflect(ctx, ReflectionRequest{
			SessionID:    sessionID,
			Limit:        opts.Limit,
			RelatedLimit: opts.RelatedLimit,
		})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}

		result.SourceCount += sessionResult.SourceCount
		result.RelatedCount += sessionResult.RelatedCount
		result.WriteCount += sessionResult.WriteCount
		result.Items = append(result.Items, cloneMemoryItems(sessionResult.Items)...)

		offset := messageCount
		if err := p.manager.UpdateCheckpoints(ctx, sessionID, statecore.CheckpointPatch{
			ReflectionOffset: &offset,
		}); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	return result, firstErr
}

type normalizedReflectionRequest struct {
	SessionID    string
	Limit        int
	RelatedLimit int
}

type reflectionBackgroundStateManager interface {
	StateManager
	ListSessions(context.Context, ...statecore.SessionListOptions) ([]statecore.Session, error)
}

func (p *MemoryProvider) isReflectionCheckpointComplete(
	ctx context.Context,
	sessionID string,
	checkpointOffset int,
	messageCount int,
) (bool, error) {
	if checkpointOffset < messageCount {
		return false, nil
	}

	hasUnreflectedSources, err := p.hasUnreflectedReflectionSources(ctx, sessionID)
	if err != nil {
		return false, err
	}

	return !hasUnreflectedSources, nil
}

func (p *MemoryProvider) shouldReflectSession(
	ctx context.Context,
	sessionID string,
	session statecore.Session,
	messageCount int,
) (bool, error) {
	if session.EpisodicCheckpointOffset < messageCount {
		return p.hasUnreflectedReflectionSources(ctx, sessionID)
	}

	complete, err := p.isReflectionCheckpointComplete(
		ctx,
		sessionID,
		session.ReflectionCheckpointOffset,
		messageCount,
	)
	if err != nil {
		return false, err
	}

	return !complete, nil
}

func (p *MemoryProvider) hasUnreflectedReflectionSources(ctx context.Context, sessionID string) (bool, error) {
	unreflected := false
	sessionIDValue := str.String(sessionID)
	result, err := p.manager.SearchMemory(ctx, SearchQuery{
		SessionID:       sessionIDValue.Trim(),
		RerankerUseCase: RerankerUseCaseReflection,
		Kinds:           []Kind{KindEpisodic},
		Statuses:        []Status{StatusCandidate, StatusActive},
		Limit:           1,
		Reflected:       &unreflected,
	})
	if err != nil {
		return false, err
	}

	return len(result.Hits) > 0, nil
}

func (p *MemoryProvider) normalizeReflectionRequest(ctx context.Context, req ReflectionRequest) (normalizedReflectionRequest, error) {
	sessionIDValue2 := str.String(req.SessionID)
	sessionID := sessionIDValue2.Trim()
	if sessionID == "" {
		currentSessionID, err := p.manager.CurrentSession(ctx)
		if err != nil {
			return normalizedReflectionRequest{}, err
		}
		currentSessionIDValue := str.String(currentSessionID)
		sessionID = currentSessionIDValue.Trim()
	}
	if sessionID == "" {
		return normalizedReflectionRequest{}, errors.New("reflection session id is required")
	}

	limit := req.Limit
	if limit <= 0 {
		limit = defaultReflectionLimit
	}
	if limit > maxReflectionLimit {
		limit = maxReflectionLimit
	}

	relatedLimit := req.RelatedLimit
	if relatedLimit <= 0 {
		relatedLimit = defaultReflectionRelatedLimit
	}
	if relatedLimit > maxReflectionRelatedLimit {
		relatedLimit = maxReflectionRelatedLimit
	}

	return normalizedReflectionRequest{SessionID: sessionID, Limit: limit, RelatedLimit: relatedLimit}, nil
}

func (p *MemoryProvider) loadReflectionSources(
	ctx context.Context,
	req normalizedReflectionRequest,
) ([]MemoryItem, error) {
	unreflected := false
	// Only unreflected episodic memory is used as source evidence. Reflected
	// candidates can later become sources themselves, but not until they are
	// written and then selected by a future pass.
	result, err := p.manager.SearchMemory(ctx, SearchQuery{
		SessionID:       req.SessionID,
		RerankerUseCase: RerankerUseCaseReflection,
		Kinds:           []Kind{KindEpisodic},
		Statuses:        []Status{StatusCandidate, StatusActive},
		Limit:           req.Limit,
		Reflected:       &unreflected,
	})
	if err != nil {
		return nil, err
	}

	items := make([]MemoryItem, 0, len(result.Hits))
	for _, hit := range result.Hits {
		items = append(items, hit.Item.Clone())
	}

	return items, nil
}

func (p *MemoryProvider) loadReflectionRelated(
	ctx context.Context,
	sources []MemoryItem,
	req normalizedReflectionRequest,
) ([]MemoryItem, error) {
	seen := make(map[string]struct{})
	items := make([]MemoryItem, 0, len(sources)*req.RelatedLimit)
	for _, source := range sources {
		text := getReflectionSearchText(source)
		if text == "" {
			continue
		}

		// Related memories are context, not sources. They help the generator and
		// duplicate checks understand what the system already knows.
		result, err := p.manager.SearchMemory(ctx, SearchQuery{
			Text:            text,
			RerankerUseCase: RerankerUseCaseReflection,
			Kinds:           []Kind{KindPinned, KindSemantic, KindProcedural},
			Statuses:        []Status{StatusCandidate, StatusActive},
			Limit:           req.RelatedLimit,
		})
		if err != nil {
			return nil, err
		}

		for _, hit := range result.Hits {
			iDValue2 := str.String(hit.Item.ID)
			id := iDValue2.Trim()
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			items = append(items, hit.Item.Clone())
		}
	}

	return items, nil
}

func (p *MemoryProvider) checkReflectionCandidateRedundancy(
	ctx context.Context,
	item MemoryItem,
	written []MemoryItem,
) (string, error) {
	if hasDuplicateReflectionCandidate(item, written) {
		return "duplicate_reflection_candidate", nil
	}

	text := getReflectionSearchText(item)
	if text == "" {
		return "", nil
	}

	reflected := true
	// Search across reflected memory of any kind. A semantic reflection and a
	// pinned reflection can be duplicates even though their final use differs.
	result, err := p.manager.SearchMemory(ctx, SearchQuery{
		Text:            text,
		RerankerUseCase: RerankerUseCaseReflection,
		Statuses:        []Status{StatusCandidate, StatusActive},
		Limit:           5,
		Reflected:       &reflected,
	})
	if err != nil {
		return "", err
	}

	for _, hit := range result.Hits {
		related := hit.Item
		iDValue3 := str.String(related.ID)
		iDValue4 := str.String(item.ID)
		if iDValue3.Trim() == iDValue4.Trim() {
			continue
		}
		switch {
		case normalizeLifecycleText(related) == normalizeLifecycleText(item):
			return "duplicate_reflection_memory", nil
		case hit.Score >= reflectionSimilarScoreThreshold:
			return "similar_reflection_memory", nil
		}
	}

	return "", nil
}

func hasDuplicateReflectionCandidate(item MemoryItem, existing []MemoryItem) bool {
	normalized := normalizeLifecycleText(item)
	if normalized == "" {
		return false
	}

	for _, candidate := range existing {
		if normalizeLifecycleText(candidate) == normalized {
			return true
		}
	}

	return false
}

func getReflectionSearchText(item MemoryItem) string {
	titleValue := str.String(item.Title)
	text := titleValue.Trim()
	if text == "" {
		textValue := str.String(item.Text)
		text = textValue.Trim()
	}
	if len([]rune(text)) > 240 {
		text = string([]rune(text)[:240])
	}
	return text
}

func prepareReflectionCandidate(
	item MemoryItem,
	sessionID string,
	sourceLinks []SourceLink,
	sourceIDs []string,
) (MemoryItem, bool, string) {
	item = item.Clone()

	// Reflection candidates get a fresh kind-aware ID because model-provided IDs
	// are not trusted. Source provenance is reconstructed from the input sources.
	item.ID = ""
	if item.Status == "" {
		item.Status = StatusCandidate
	}
	if item.Metadata == nil {
		item.Metadata = make(map[string]string)
	}
	sessionIDValue3 := str.String(sessionID)
	if sessionID = sessionIDValue3.Trim(); sessionID != "" {
		item.Metadata["source_session_id"] = sessionID
	}
	item.Metadata["reflection_source_memory_ids"] = strings.Join(sourceIDs, ",")
	item.Metadata["reflection_origin"] = "episodic"
	item.Reflected = true

	item.SourceLinks = cloneSourceLinks(sourceLinks)
	item.Tags = append(item.Tags, "reflection")
	for _, id := range sourceIDs {
		if tag := getReflectionSourceTag(id); tag != "" {
			item.Tags = append(item.Tags, tag)
		}
	}
	item.Tags = normalizeMemoryTags(item.Tags)

	item.ID = getKindAwareMemoryID(item.Kind)
	if err := validateReflectionCandidate(item); err != nil {
		return MemoryItem{}, false, err.Error()
	}

	return item, true, ""
}

func (p *MemoryProvider) recordReflectionCandidate(ctx context.Context, item MemoryItem) (MemoryItem, error) {
	if err := validateWrite(ctx, p.guardrails, item); err != nil {
		return MemoryItem{}, err
	}

	item, err := p.manager.UpsertMemory(ctx, item)
	if err != nil {
		return MemoryItem{}, err
	}

	return item.Clone(), nil
}

func (p *MemoryProvider) markReflectionSourcesReflected(ctx context.Context, sources []MemoryItem) error {
	for _, source := range sources {
		reflected := true
		// Patch only the reflected flag. A full upsert could overwrite lifecycle
		// fields if promotion updated the source while reflection was running.
		if _, err := p.manager.PatchMemory(ctx, MemoryPatch{
			ID:        source.ID,
			Reflected: &reflected,
		}); err != nil {
			return err
		}
	}

	return nil
}

func validateReflectionCandidate(item MemoryItem) error {
	switch item.Kind {
	case KindPinned, KindSemantic, KindEpisodic, KindProcedural:
	default:
		return errors.New("reflection candidate kind must be pinned, semantic, episodic, or procedural")
	}
	if item.Status != StatusCandidate {
		return errors.New("reflection candidate must be stored as candidate")
	}
	titleValue2 := str.String(item.Title)
	textValue2 := str.String(item.Text)
	if titleValue2.Trim() == "" && textValue2.Trim() == "" {
		return errors.New("reflection candidate text or title is required")
	}
	if !hasCandidateProvenance(item) {
		return errors.New("reflection candidate source provenance is required")
	}
	if reason := checkCandidateAdmissionRejection(item); reason != "" {
		return errors.New(reason)
	}
	if item.Kind == KindProcedural {
		if reason := checkProceduralReflectionMetadata(item); reason != "" {
			return errors.New(reason)
		}
	}

	return nil
}

func checkProceduralReflectionMetadata(item MemoryItem) string {
	metadataValue := str.String(item.Metadata["procedural_trigger"])
	if metadataValue.Trim() == "" {
		return "procedural_trigger_required"
	}
	metadataValue2 := str.String(item.Metadata["procedural_steps"])
	if metadataValue2.Trim() == "" {
		return "procedural_steps_required"
	}

	return ""
}

func getSourceLinks(sources []MemoryItem) []SourceLink {
	links := make([]SourceLink, 0, len(sources))
	for _, source := range sources {
		links = append(links, source.SourceLinks...)
		if len(source.SourceLinks) == 0 {
			metadataValue3 := str.String(source.Metadata["source_session_id"])
			if sessionID := metadataValue3.Trim(); sessionID != "" {
				links = append(links, SourceLink{
					SessionID:     sessionID,
					CreatedBy:     "reflection",
					CreatedReason: "reflection_source_memory",
				})
			}
		}
	}

	return cloneSourceLinks(links)
}

func limitReflectionItems(items []MemoryItem, limit int) []MemoryItem {
	if limit <= 0 || len(items) <= limit {
		return items
	}

	return items[:limit]
}

func cloneSourceLinks(links []SourceLink) []SourceLink {
	cloned := make([]SourceLink, 0, len(links))
	for _, link := range links {
		link.MessageIDs = append([]uint(nil), link.MessageIDs...)
		link.Offsets = append([]int(nil), link.Offsets...)
		cloned = append(cloned, link)
	}

	return cloned
}

func cloneMemoryItems(items []MemoryItem) []MemoryItem {
	cloned := make([]MemoryItem, 0, len(items))
	for _, item := range items {
		cloned = append(cloned, item.Clone())
	}

	return cloned
}

func getMemoryIDs(items []MemoryItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		iDValue5 := str.String(item.ID)
		if id := iDValue5.Trim(); id != "" {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

func getReflectionSourceTag(id string) string {
	idValue := str.String(id)
	id = idValue.Trim()
	if id == "" {
		return ""
	}
	return "reflection-source-" + id
}

func (p *MemoryProvider) recordReflectionRejection(ctx context.Context, sessionID string, reason string) {
	fields := buildObservationFields(p.Name(), "reflect", map[string]any{
		"session_id":        sessionID,
		"rejection_reason":  reason,
		"admission_outcome": "rejected",
	})
	logDebugAndTrace(ctx, p.observability(), "memory reflection candidate rejected", trace.EvtMemoryReflectionCandidateRejected, fields)
}

func (p *MemoryProvider) recordReflectionFailure(ctx context.Context, result ReflectionResult, err error) {
	fields := buildObservationFields(p.Name(), "reflect", map[string]any{
		"session_id":    result.SessionID,
		"source_count":  result.SourceCount,
		"related_count": result.RelatedCount,
		"write_count":   result.WriteCount,
		"error":         err.Error(),
	})
	logDebugAndTrace(ctx, p.observability(), "memory reflection failed", trace.EvtMemoryReflectionFailed, fields)
}

func (p *MemoryProvider) recordReflectionCompleted(ctx context.Context, result ReflectionResult, started time.Time) {
	fields := buildObservationFields(p.Name(), "reflect", map[string]any{
		"session_id":    result.SessionID,
		"source_count":  result.SourceCount,
		"related_count": result.RelatedCount,
		"write_count":   result.WriteCount,
		"duration_ms":   time.Since(started).Milliseconds(),
	})
	logDebugAndTrace(ctx, p.observability(), "memory reflection completed", trace.EvtMemoryReflectionCompleted, fields)
}

func normalizeMemoryTags(tags []string) []string {
	normalized := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		tagValue := str.String(tag)
		tag = tagValue.Normalized()
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		normalized = append(normalized, tag)
	}
	slices.Sort(normalized)
	return normalized
}
