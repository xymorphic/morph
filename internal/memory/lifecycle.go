package memory

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/xymorphic/morph/pkg/str"
)

const (
	// lifecycleMetadataAction records the last lifecycle operation that touched
	// the memory item. Promotion and deletion write this today.
	lifecycleMetadataAction = "lifecycle_action"

	// lifecycleMetadataReason stores a caller-supplied reason for the lifecycle
	// operation when one is provided.
	lifecycleMetadataReason = "lifecycle_reason"

	// lifecycleMetadataPreviousStatus records the status before the lifecycle
	// transition, so audits can explain the state movement.
	lifecycleMetadataPreviousStatus = "lifecycle_previous_status"

	// lifecycleMetadataAt is the provider-side timestamp for the lifecycle
	// decision. It is never model supplied.
	lifecycleMetadataAt = "lifecycle_at"

	// Promotion decision metadata is persisted on approved memories and on safely
	// rejected candidates. This makes autonomous decisions inspectable.
	lifecycleMetadataDecisionPolicy   = "promotion_policy"
	lifecycleMetadataDecisionReason   = "promotion_decision_reason"
	lifecycleMetadataDecisionOutcome  = "promotion_decision_outcome"
	lifecycleMetadataConflictState    = "promotion_conflict_state"
	lifecycleMetadataRelatedMemoryIDs = "promotion_related_memory_ids"
)

const (
	// promotionPolicyDefault names the built-in conservative policy.
	promotionPolicyDefault = "default_conservative"

	// Conflict states come from active same-kind related memories. Exact matches
	// are duplicates; any other related active memory is still treated as conflict
	// evidence by the default policy because autonomous promotion should be
	// conservative around semantic overlap.
	promotionConflictNone      = "none"
	promotionConflictRelated   = "related_active_memory"
	promotionConflictDuplicate = "duplicate"

	// Non-exact related memories only block default promotion when their search
	// score is strong enough to be useful conflict evidence. Exact duplicates are
	// blocked regardless of score.
	promotionRelatedConflictScoreThreshold    = 0.75
	promotionCrossKindDuplicateScoreThreshold = 0.9
	promotionDefaultConfidenceThreshold       = 0.75
	promotionPinnedConfidenceThreshold        = 0.9

	// Promotion is a governance pass, not a high-throughput indexing job, so the
	// default cadence and batch sizes stay intentionally modest.
	defaultPromotionBackgroundInterval = time.Minute
	defaultPromotionBackgroundLimit    = 10
	maxPromotionBackgroundLimit        = 50
	defaultPromotionEvaluatedRetention = 7 * 24 * time.Hour
)

type defaultPromotionPolicy struct{}

// EvaluatePromotion is the conservative built-in policy. It approves only
// candidate memories that have passed admission, guardrails, provenance checks,
// conflict checks, kind-specific constraints, and confidence thresholds.
func (defaultPromotionPolicy) EvaluatePromotion(_ context.Context, req PromotionPolicyRequest) (PromotionDecision, error) {
	decision := PromotionDecision{
		Policy:        promotionPolicyDefault,
		Confidence:    req.Candidate.Confidence,
		ConflictState: req.ConflictState,
	}

	if req.Candidate.Status != StatusCandidate {
		decision.Reason = "candidate_status_required"
		return decision, nil
	}
	if req.AdmissionResult != "" {
		decision.Reason = req.AdmissionResult
		return decision, nil
	}
	if req.GuardrailResult != "" {
		decision.Reason = req.GuardrailResult
		return decision, nil
	}
	if req.ConflictState != "" && req.ConflictState != promotionConflictNone {
		decision.Reason = req.ConflictState
		return decision, nil
	}
	if !hasCandidateProvenance(req.Candidate) {
		decision.Reason = "missing_provenance"
		return decision, nil
	}
	if req.Candidate.Kind == KindPinned && !req.Strict {
		decision.Reason = "pinned_memory_requires_strict_policy"
		return decision, nil
	}

	minConfidence := promotionDefaultConfidenceThreshold
	if req.Candidate.Kind == KindPinned {
		minConfidence = promotionPinnedConfidenceThreshold
	}
	if req.Candidate.Confidence < minConfidence {
		decision.Reason = "low_confidence"
		return decision, nil
	}

	decision.Approved = true
	decision.Reason = "approved"
	return decision, nil
}

// PromoteCandidate runs one candidate through the lifecycle promotion gate.
//
// This is intentionally narrow:
// - only candidate memory can be loaded
// - related active memory is fetched for dedupe/conflict context
// - deterministic admission and guardrails run before the policy result is used
// - the method stores either an active memory or a rejected candidate decision
//
// Reflection does not call this directly. The provider-owned background runner
// or a future governed internal/admin flow should call it when candidates are
// ready for evaluation.
func (p *MemoryProvider) PromoteCandidate(ctx context.Context, req PromotionRequest) (LifecycleResult, error) {
	started := time.Now().UTC()
	if p == nil || p.manager == nil {
		return LifecycleResult{}, errors.New("memory provider is required")
	}
	iDValue := str.String(req.ID)
	memoryID := iDValue.Trim()
	if memoryID == "" {
		return LifecycleResult{}, errors.New("candidate memory id is required")
	}

	p.recordPromotionStarted(ctx, memoryID, "promote")

	// Promotion never re-promotes active memory or revives deleted memory. The
	// status filter makes that lifecycle boundary explicit at the storage query.
	candidate, err := p.loadMemoryByID(ctx, memoryID, []Status{StatusCandidate})
	if err != nil {
		p.recordPromotionFailure(ctx, memoryID, err)
		return LifecycleResult{}, err
	}

	// Related search is where semantic/vector retrieval can help promotion. The
	// conflict decision below is deterministic, but the candidate set can be found
	// by the active memory backend's search capabilities.
	relatedHits, err := p.relatedPromotionMemories(ctx, candidate)
	if err != nil {
		p.recordPromotionFailure(ctx, memoryID, err)
		return LifecycleResult{}, err
	}
	related := getPromotionRelatedItems(relatedHits)

	// Admission rejects obviously unsuitable candidates from model-provided
	// metadata. Guardrails run only after admission passes, avoiding extra work on
	// candidates already rejected by deterministic admission.
	admissionResult := checkCandidateAdmissionRejection(candidate)
	guardrailResult := ""
	if admissionResult == "" {
		if err := validateWrite(ctx, p.guardrails, candidate); err != nil {
			guardrailResult = err.Error()
		}
	}
	reasonValue :=

		// The policy receives clones so policy implementations cannot mutate the
		// loaded candidate or related memories through shared map/slice state.

		str.String(req.Reason)
	decision, err := p.promotionPolicyOrDefault().EvaluatePromotion(ctx, PromotionPolicyRequest{
		Candidate:          candidate.Clone(),
		Related:            cloneMemoryItems(related),
		Reason:             reasonValue.Trim(),
		Strict:             req.Strict,
		AdmissionResult:    admissionResult,
		GuardrailResult:    guardrailResult,
		ReflectionEvidence: hasReflectionEvidence(candidate),
		ConflictState:      checkPromotionConflictState(candidate, relatedHits),
	})
	if p.promotionPolicy == nil {
		p.recordPromotionFallback(ctx, memoryID)
	}
	if err != nil {
		p.recordPromotionFailure(ctx, memoryID, err)
		return LifecycleResult{}, err
	}

	// Admission and guardrails are hard gates. Even if a custom policy approves a
	// candidate, these provider-owned checks still win.
	decision = enforcePromotionHardGates(decision, admissionResult, guardrailResult)
	p.recordPromotionDecision(ctx, memoryID, decision, relatedHits)

	if !decision.Approved {
		denied := candidate.Clone()
		denied.PromotionEvaluatedAt = started

		// Guardrail failures still get an evaluation timestamp so background
		// promotion does not keep reprocessing them. Other denials also record the
		// explainable decision metadata used by inspection and retry suppression.
		if guardrailResult == "" {
			denied.Metadata = buildLifecycleMetadata(denied.Metadata, "promote", req.Reason, denied.Status)
			writePromotionDecisionMetadata(denied.Metadata, decision, related)
		}
		if _, err := p.manager.UpsertMemory(ctx, denied); err != nil {
			p.recordPromotionFailure(ctx, memoryID, err)
			return LifecycleResult{}, err
		}

		p.recordPromotionCompleted(ctx, memoryID, decision, started)
		return LifecycleResult{
			Item:     denied.Clone(),
			Related:  cloneMemoryItems(related),
			Decision: decision,
		}, nil
	}

	// Approval keeps the candidate content intact and only changes lifecycle
	// status plus provider-owned audit/decision metadata.
	promoted := candidate.Clone()
	previousStatus := promoted.Status
	promoted.Status = StatusActive
	promoted.PromotionEvaluatedAt = started
	promoted.Metadata = buildLifecycleMetadata(promoted.Metadata, "promote", req.Reason, previousStatus)
	writePromotionDecisionMetadata(promoted.Metadata, decision, related)

	promoted, err = p.manager.UpsertMemory(ctx, promoted)
	if err != nil {
		p.recordPromotionFailure(ctx, memoryID, err)
		return LifecycleResult{}, err
	}

	p.recordPromotionCompleted(ctx, memoryID, decision, started)
	return LifecycleResult{
		Item:     promoted.Clone(),
		Related:  cloneMemoryItems(related),
		Decision: decision,
	}, nil
}

// normalizePromotionBackgroundOptions fills conservative defaults for a single
// background promotion pass or loop.
func normalizePromotionBackgroundOptions(opts PromotionBackgroundOptions) PromotionBackgroundOptions {
	if opts.Interval <= 0 {
		opts.Interval = defaultPromotionBackgroundInterval
	}
	if opts.Limit <= 0 {
		opts.Limit = defaultPromotionBackgroundLimit
	}
	if opts.Limit > maxPromotionBackgroundLimit {
		opts.Limit = maxPromotionBackgroundLimit
	}
	reasonValue2 := str.String(opts.Reason)
	if reasonValue2.Trim() == "" {
		opts.Reason = "background_promotion"
	}
	if opts.EvaluatedRetention == 0 {
		opts.EvaluatedRetention = defaultPromotionEvaluatedRetention
	}

	return opts
}

// startPromotionBackground starts the provider-owned promotion loop at most once
// for a provider instance.
func (p *MemoryProvider) startPromotionBackground(ctx context.Context) error {
	if !p.promotionBackground.Enabled {
		return nil
	}

	opts := normalizePromotionBackgroundOptions(p.promotionBackground)
	p.promotionBackgroundStartOnce.Do(func() {
		go p.runPromotionBackgroundLoop(ctx, opts)
	})

	return nil
}

// runPromotionBackgroundLoop ticks until the provider background context is
// cancelled. Per-run errors are intentionally swallowed here because promotion
// failures are already emitted through the candidate promotion path, and a
// background loop should not crash the provider.
func (p *MemoryProvider) runPromotionBackgroundLoop(ctx context.Context, opts PromotionBackgroundOptions) {
	ticker := time.NewTicker(opts.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = p.RunPromotionBackground(ctx, opts)
			_, _ = p.RunPromotionCleanup(ctx, opts)
		}
	}
}

// RunPromotionBackground performs one bounded background promotion pass. It is
// separate from the ticker loop so tests, admin flows, or future schedulers can
// trigger a single pass without starting a goroutine.
func (p *MemoryProvider) RunPromotionBackground(
	ctx context.Context,
	opts PromotionBackgroundOptions,
) (int, error) {
	started := time.Now().UTC()
	if p == nil || p.manager == nil {
		return 0, errors.New("memory provider is required")
	}

	opts = normalizePromotionBackgroundOptions(opts)

	unevaluated := false
	result, err := p.manager.SearchMemory(ctx, SearchQuery{
		RerankerUseCase:    RerankerUseCasePromotion,
		Statuses:           []Status{StatusCandidate},
		PromotionEvaluated: &unevaluated,
		Limit:              opts.Limit,
	})
	if err != nil {
		p.recordPromotionBackgroundFailed(ctx, err, 0, opts, started)
		return 0, err
	}

	count := 0
	for _, hit := range result.Hits {
		item := hit.Item.Clone()
		iDValue2 :=

			// Empty IDs are unusable for lifecycle operations. Evaluated candidates are
			// excluded by the storage query above.
			str.String(item.ID)
		if iDValue2.Trim() == "" {
			continue
		}
		if count >= opts.Limit {
			break
		}
		if _, err := p.PromoteCandidate(ctx, PromotionRequest{
			ID:     item.ID,
			Reason: opts.Reason,
		}); err != nil {
			p.recordPromotionBackgroundFailed(ctx, err, count, opts, started)
			return count, err
		}
		count++
	}

	p.recordPromotionBackgroundCompleted(ctx, count, opts, started)
	return count, nil
}

func (p *MemoryProvider) RunPromotionCleanup(ctx context.Context, opts PromotionBackgroundOptions) (int, error) {
	started := time.Now().UTC()
	if p == nil || p.manager == nil {
		return 0, errors.New("memory provider is required")
	}

	opts = normalizePromotionBackgroundOptions(opts)
	if opts.EvaluatedRetention <= 0 {
		p.recordPromotionCleanupSkipped(ctx, opts)
		return 0, nil
	}

	cutoff := time.Now().UTC().Add(-opts.EvaluatedRetention)
	evaluated := true
	result, err := p.manager.SearchMemory(ctx, SearchQuery{
		Statuses:                 []Status{StatusCandidate},
		PromotionEvaluated:       &evaluated,
		PromotionEvaluatedBefore: cutoff,
		Limit:                    opts.Limit,
	})
	if err != nil {
		p.recordPromotionCleanupFailed(ctx, err, 0, opts, started)
		return 0, err
	}

	count := 0
	for _, hit := range result.Hits {
		item := hit.Item.Clone()
		iDValue3 := str.String(item.ID)
		if iDValue3.Trim() == "" {
			continue
		}
		if item.Status != StatusCandidate || item.PromotionEvaluatedAt.IsZero() {
			continue
		}
		if !item.PromotionEvaluatedAt.Before(cutoff) {
			continue
		}
		if count >= opts.Limit {
			break
		}
		if err := p.manager.HardDeleteMemory(ctx, DeleteRequest{
			ID:     item.ID,
			Reason: "promotion_evaluated_candidate_retention",
		}); err != nil {
			p.recordPromotionCleanupFailed(ctx, err, count, opts, started)
			return count, err
		}
		count++
	}

	p.recordPromotionCleanupCompleted(ctx, count, opts, started)
	return count, nil
}

// promotionPolicyOrDefault keeps promotion usable without custom policy
// configuration.
func (p *MemoryProvider) promotionPolicyOrDefault() PromotionPolicy {
	if p != nil && p.promotionPolicy != nil {
		return p.promotionPolicy
	}

	return defaultPromotionPolicy{}
}

// enforcePromotionHardGates prevents custom policies from bypassing provider
// admission and guardrail decisions.
func enforcePromotionHardGates(
	decision PromotionDecision,
	admissionResult string,
	guardrailResult string,
) PromotionDecision {
	if admissionResult != "" {
		decision.Approved = false
		decision.Reason = admissionResult
		return decision
	}

	if guardrailResult != "" {
		decision.Approved = false
		decision.Reason = guardrailResult
	}
	return decision
}

// loadMemoryByID reads one persisted memory item with an optional status scope.
func (p *MemoryProvider) loadMemoryByID(
	ctx context.Context,
	id string,
	statuses []Status,
) (MemoryItem, error) {
	idValue := str.String(id)
	result, err := p.manager.SearchMemory(ctx, SearchQuery{
		RerankerUseCase: RerankerUseCasePromotion,
		IDs:             []string{idValue.Trim()},
		Statuses:        statuses,
		Limit:           1,
	})
	if err != nil {
		return MemoryItem{}, err
	}
	if len(result.Hits) == 0 {
		return MemoryItem{}, errors.New("memory item not found")
	}

	return result.Hits[0].Item.Clone(), nil
}

// relatedPromotionMemories loads active memories that may duplicate or conflict
// with the candidate. The query uses normal memory search, so vector search
// participates when it is enabled by the backing store.
func (p *MemoryProvider) relatedPromotionMemories(
	ctx context.Context,
	candidate MemoryItem,
) ([]SearchHit, error) {
	text := getPromotionSearchText(candidate)
	if text == "" {
		return nil, nil
	}

	result, err := p.manager.SearchMemory(ctx, SearchQuery{
		Text:            text,
		RerankerUseCase: RerankerUseCasePromotion,
		Statuses:        []Status{StatusActive},
		Limit:           5,
	})
	if err != nil {
		return nil, err
	}

	hits := make([]SearchHit, 0, len(result.Hits))
	reflectionSourceIDs := getReflectionSourceMemoryIDs(candidate)
	for _, hit := range result.Hits {
		item := hit.Item.Clone()
		iDValue4 := str.String(item.ID)
		id := iDValue4.Trim()
		iDValue5 := str.String(candidate.ID)
		if id == iDValue5.Trim() {
			continue
		}
		if _, ok := reflectionSourceIDs[id]; ok {
			continue
		}
		hit.Item = item
		hits = append(hits, hit)
	}

	return hits, nil
}

// getPromotionSearchText prefers title because it is usually the shortest summary
// of a candidate. It falls back to text and caps length to keep related lookup
// bounded.
func getPromotionSearchText(item MemoryItem) string {
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

// checkPromotionConflictState classifies active related memories. Exact
// normalized matches are duplicates even across memory kinds, because a semantic
// reflection should not activate if it simply repeats an active episodic source.
// Non-identical same-kind memories block promotion at the normal related-memory
// threshold. Non-identical cross-kind memories only block promotion at a stricter
// near-duplicate threshold, so reflection can still turn an episode into a
// genuinely distinct semantic or procedural memory.
func checkPromotionConflictState(candidate MemoryItem, related []SearchHit) string {
	hasRelated := false
	for _, hit := range related {
		item := hit.Item
		if item.Status != StatusActive {
			continue
		}
		if normalizeLifecycleText(candidate) == normalizeLifecycleText(item) {
			return promotionConflictDuplicate
		}
		if candidate.Kind != item.Kind && hit.Score >= promotionCrossKindDuplicateScoreThreshold {
			return promotionConflictDuplicate
		}
		if candidate.Kind == item.Kind && hit.Score >= promotionRelatedConflictScoreThreshold {
			hasRelated = true
		}
	}
	if hasRelated {
		return promotionConflictRelated
	}

	return promotionConflictNone
}

func getReflectionSourceMemoryIDs(item MemoryItem) map[string]struct{} {
	ids := make(map[string]struct{})
	for id := range strings.SplitSeq(item.Metadata["reflection_source_memory_ids"], ",") {
		idValue2 := str.String(id)
		id = idValue2.Trim()
		if id != "" {
			ids[id] = struct{}{}
		}
	}

	return ids
}

func getPromotionRelatedItems(hits []SearchHit) []MemoryItem {
	items := make([]MemoryItem, 0, len(hits))
	for _, hit := range hits {
		items = append(items, hit.Item.Clone())
	}

	return items
}

// normalizeLifecycleText gives duplicate checks a stable comparison string
// without relying on model-provided dedupe keys.
func normalizeLifecycleText(item MemoryItem) string {
	titleValue2 := str.String(item.Title)
	title := titleValue2.Trim()
	textValue2 := str.String(item.Text)
	text := textValue2.Trim()
	combined := strings.Join([]string{title, text}, "\n")
	normalized := strings.ToLower(combined)
	fields := strings.Fields(normalized)
	return strings.Join(fields, " ")
}

// hasReflectionEvidence tells the promotion policy whether a candidate came
// from reflected episodic evidence. It accepts both the explicit boolean and
// provider metadata because older candidates may only have metadata.
func hasReflectionEvidence(item MemoryItem) bool {
	if item.Reflected {
		return true
	}

	metadataValue := str.String(item.Metadata["reflection_source_memory_ids"])
	metadataValue2 := str.String(item.Metadata["reflection_origin"])
	return metadataValue.Trim() != "" || metadataValue2.
		Trim() != ""
}

// buildLifecycleMetadata writes provider-owned lifecycle audit metadata while
// preserving existing metadata.
func buildLifecycleMetadata(
	metadata map[string]string,
	action string,
	reason string,
	previous Status,
) map[string]string {
	next := cloneMetadata(metadata)
	if next == nil {
		next = make(map[string]string)
	}
	actionValue := str.String(action)
	next[lifecycleMetadataAction] = actionValue.Trim()
	next[lifecycleMetadataPreviousStatus] = string(previous)
	next[lifecycleMetadataAt] = time.Now().UTC().Format(time.RFC3339Nano)
	reasonValue3 := str.String(reason)
	if reason = reasonValue3.Trim(); reason != "" {
		next[lifecycleMetadataReason] = reason
	}
	return next
}

// writePromotionDecisionMetadata stores the policy result on active memories and
// safely rejected candidates. This is the inspection surface for why a memory was
// or was not activated.
func writePromotionDecisionMetadata(
	metadata map[string]string,
	decision PromotionDecision,
	related []MemoryItem,
) {
	metadata[lifecycleMetadataDecisionPolicy] = decision.Policy
	if decision.Approved {
		metadata[lifecycleMetadataDecisionOutcome] = "approved"
	} else {
		metadata[lifecycleMetadataDecisionOutcome] = "rejected"
	}
	metadata[lifecycleMetadataDecisionReason] = decision.Reason
	metadata[lifecycleMetadataConflictState] = decision.ConflictState
	if ids := getMemoryIDs(related); len(ids) > 0 {
		metadata[lifecycleMetadataRelatedMemoryIDs] = strings.Join(ids, ",")
	}
}
