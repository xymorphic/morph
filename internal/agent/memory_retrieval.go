package agent

import (
	"context"
	"strings"

	"github.com/xymorphic/morph/internal/constants"
	"github.com/xymorphic/morph/internal/guardrails"
	instruct "github.com/xymorphic/morph/internal/instructions"
	"github.com/xymorphic/morph/internal/memory"
	memoryobservability "github.com/xymorphic/morph/internal/memory/observability"
	"github.com/xymorphic/morph/internal/trace"
	"github.com/xymorphic/morph/pkg/str"
)

const (
	pinnedMemoryRetrievalLimit      = constants.AgentPinnedMemoryRetrievalLimit
	pinnedMemoryRetrievalItemChars  = constants.AgentPinnedMemoryRetrievalItemChars
	searchMemoryRetrievalLimit      = constants.AgentSearchMemoryRetrievalLimit
	searchMemoryRetrievalItemChars  = constants.AgentSearchMemoryRetrievalItemChars
	searchMemoryRetrievalMinScore   = constants.AgentSearchMemoryRetrievalMinScore
	memoryContextInstructionMaxChar = constants.AgentMemoryContextInstructionChars
)

var sanitizeMemoryPromptValue = guardrails.Sanitize

// retrieveMemoryInstruction loads pinned and searched memory, sanitizes it, and renders model instructions.
func (t *Turn) retrieveMemoryInstruction(
	ctx context.Context,
	userText string,
	traceSession trace.Session,
) instruct.Instruction {
	if t == nil || t.cfg == nil || !t.cfg.MemoryEnabled() || !t.cfg.MemoryRetrievalEnabled() {
		return instruct.Instruction{Name: instruct.MemoryContextInstructionName}
	}

	source, _ := t.env.(memoryProviderSource)
	if source == nil {
		source = t.memoryProviders
	}
	if source == nil {
		return instruct.Instruction{Name: instruct.MemoryContextInstructionName}
	}

	provider := source.MemoryProvider()
	if provider == nil {
		return instruct.Instruction{Name: instruct.MemoryContextInstructionName}
	}

	ctx = guardrails.WithUnsafeEvidenceRecorder(ctx, t.getUnsafeEvidenceRecorder())

	if err := provider.ConfigureObservability(memoryobservability.New(agentLog.Logger(), traceSession)); err != nil {
		recordMemoryRetrievalFailed(traceSession, provider.Name(), "configure_observability", err)
		return instruct.Instruction{Name: instruct.MemoryContextInstructionName}
	}

	caps, err := provider.Capabilities(ctx)
	if err != nil {
		recordMemoryRetrievalFailed(traceSession, provider.Name(), "capabilities", err)
		return instruct.Instruction{Name: instruct.MemoryContextInstructionName}
	}
	searchProvider, supportsSearchProvider := provider.(memory.SearchProvider)
	pinnedProvider, supportsPinnedProvider := provider.(memory.PinnedProvider)
	if (!caps.SupportsSearch || !supportsSearchProvider) && (!caps.SupportsPinned || !supportsPinnedProvider) {
		return instruct.Instruction{Name: instruct.MemoryContextInstructionName}
	}

	items := make([]memory.MemoryItem, 0, pinnedMemoryRetrievalLimit+searchMemoryRetrievalLimit)
	pinnedTraceItems := []trace.MemoryTraceItem(nil)
	searchTraceHits := []trace.MemoryTraceItem(nil)
	retrievedHitCount := 0
	filteredSearchHitCount := 0

	// Pinned memory is loaded independently of the user query because it
	// represents durable user or session facts that should always be nearby.
	if caps.SupportsPinned && supportsPinnedProvider {
		recordMemoryRetrievalEvent(traceSession, trace.EvtMemoryRetrievalStarted, trace.MemoryEventPayload{
			Provider:  provider.Name(),
			Operation: "load_pinned",
			Limit:     pinnedMemoryRetrievalLimit,
			MaxChars:  pinnedMemoryRetrievalItemChars,
		})

		pinned, err := pinnedProvider.LoadPinned(ctx, memory.SearchQuery{
			RerankerUseCase: memory.RerankerUseCasePinned,
			Statuses:        []memory.Status{memory.StatusActive},
			Limit:           pinnedMemoryRetrievalLimit,
			MaxChars:        pinnedMemoryRetrievalItemChars,
		})
		if err != nil {
			recordMemoryRetrievalFailed(traceSession, provider.Name(), "load_pinned", err)
		} else {
			pinnedTraceItems = memoryRetrievalTraceItems(pinned)
			retrievedHitCount += len(pinned)
			items = append(items, pinned...)
		}
	}

	// Search memory is query-sensitive and then filtered by minimum score so
	// weak vector matches do not dilute the prompt.
	if caps.SupportsSearch && supportsSearchProvider {
		recordMemoryRetrievalEvent(traceSession, trace.EvtMemoryRetrievalStarted, trace.MemoryEventPayload{
			Provider:  provider.Name(),
			Operation: "search",
			Limit:     searchMemoryRetrievalLimit,
			MaxChars:  searchMemoryRetrievalItemChars,
		})
		userTextValue := str.String(userText)
		query := memory.SearchQuery{
			Text:            userTextValue.Trim(),
			RerankerUseCase: memory.RerankerUseCaseTurnRetrieval,
			Kinds: []memory.Kind{
				memory.KindSemantic,
				memory.KindEpisodic,
				memory.KindProcedural,
			},
			Statuses: []memory.Status{memory.StatusActive},
			Limit:    searchMemoryRetrievalLimit,
			MaxChars: searchMemoryRetrievalItemChars,
		}
		result, err := searchProvider.Search(ctx, query)
		if err != nil {
			recordMemoryRetrievalFailed(traceSession, provider.Name(), "search", err)
		} else {
			filteredHits := filterSearchHitsForTurnMemory(result.Hits)
			filteredSearchHitCount = len(result.Hits) - len(filteredHits)
			retrievedHitCount += len(result.Hits)
			searchTraceHits = memoryRetrievalTraceHits(result.Hits)
			items = append(items, searchHitsToMemoryItems(filteredHits)...)
		}
	}

	// All retrieved memory is treated as prompt-visible untrusted content.
	items = sanitizeMemoryItemsForPromptWithEvidence(
		ctx,
		items,
		traceSession,
		t.getUnsafeEvidenceRecorder(),
	)

	recordMemoryRetrievalEvent(traceSession, trace.EvtMemoryRetrieved, trace.MemoryEventPayload{
		Provider:            provider.Name(),
		HitCount:            retrievedHitCount,
		InjectedCount:       len(items),
		PinnedItems:         pinnedTraceItems,
		SearchHits:          searchTraceHits,
		SearchMinScore:      searchMemoryRetrievalMinScore,
		SearchFilteredCount: filteredSearchHitCount,
		InjectedItems:       memoryRetrievalTraceItems(items),
	})

	return instruct.BuildMemoryContext(
		memoryItemsToContextItems(items),
		memoryContextInstructionMaxChar,
	)
}

// filterSearchHitsForTurnMemory keeps only search hits strong enough for turn context injection.
func filterSearchHitsForTurnMemory(hits []memory.SearchHit) []memory.SearchHit {
	filtered := make([]memory.SearchHit, 0, len(hits))
	for _, hit := range hits {
		if hit.Score < searchMemoryRetrievalMinScore {
			continue
		}

		filtered = append(filtered, hit)
	}

	return filtered
}

// recordMemoryRetrievalEvent records memory retrieval trace events when tracing is enabled.
func recordMemoryRetrievalEvent(traceSession trace.Session, event string, payload trace.MemoryEventPayload) {
	if traceSession == nil {
		return
	}

	traceSession.Record(event, payload)
}

// recordMemoryRetrievalFailed records and logs recoverable memory retrieval failures.
func recordMemoryRetrievalFailed(
	traceSession trace.Session,
	providerName string,
	operation string,
	err error,
) {
	if traceSession != nil {
		providerNameValue := str.String(providerName)
		operationValue := str.String(operation)
		traceSession.Record(trace.EvtMemoryRetrievalFailed, trace.MemoryEventPayload{
			Provider:  providerNameValue.Trim(),
			Operation: operationValue.Trim(),
			Error:     err.Error(),
		})
	}
	providerNameValue2 := str.String(providerName)
	operationValue2 := str.String(operation)
	agentLog.Warn().
		Str("provider", providerNameValue2.Trim()).
		Str("operation", operationValue2.Trim()).
		Err(err).
		Msg("memory retrieval failed")
}

// searchHitsToMemoryItems unwraps memory items from ranked search hits.
func searchHitsToMemoryItems(hits []memory.SearchHit) []memory.MemoryItem {
	items := make([]memory.MemoryItem, 0, len(hits))
	for _, hit := range hits {
		items = append(items, hit.Item)
	}

	return items
}

// memoryRetrievalTraceHits renders search hits with score metadata for trace payloads.
func memoryRetrievalTraceHits(hits []memory.SearchHit) []trace.MemoryTraceItem {
	items := make([]trace.MemoryTraceItem, 0, len(hits))
	for _, hit := range hits {
		item := memoryRetrievalTraceItem(hit.Item)
		item.Score = hit.Score
		item.LexicalScore = hit.LexicalScore
		item.VectorScore = hit.VectorScore
		items = append(items, item)
	}

	return items
}

// memoryRetrievalTraceItems renders memory items for trace payloads.
func memoryRetrievalTraceItems(memoryItems []memory.MemoryItem) []trace.MemoryTraceItem {
	items := make([]trace.MemoryTraceItem, 0, len(memoryItems))
	for _, item := range memoryItems {
		items = append(items, memoryRetrievalTraceItem(item))
	}

	return items
}

// memoryRetrievalTraceItem reduces a memory item to safe trace metadata.
func memoryRetrievalTraceItem(item memory.MemoryItem) trace.MemoryTraceItem {
	iDValue := str.String(item.ID)
	textValue := str.String(item.Text)
	return trace.MemoryTraceItem{
		ID:          iDValue.Trim(),
		Kind:        string(item.Kind),
		Status:      string(item.Status),
		Title:       truncateMemoryTraceText(item.Title),
		TextChars:   len([]rune(textValue.Trim())),
		Confidence:  item.Confidence,
		Reflected:   item.Reflected,
		SourceCount: len(item.SourceLinks),
	}
}

// truncateMemoryTraceText caps memory trace titles to a compact display length.
func truncateMemoryTraceText(value string) string {
	valueText := str.String(value).Trim()
	runes := []rune(valueText)
	if len(runes) <= 120 {
		return valueText
	}
	return string(runes[:120])
}

// sanitizeMemoryItemsForPrompt filters blocked memory and returns prompt-safe memory items.
func sanitizeMemoryItemsForPrompt(input []memory.MemoryItem, traceSession trace.Session) []memory.MemoryItem {
	return sanitizeMemoryItemsForPromptWithEvidence(
		context.Background(),
		input,
		traceSession,
		nil,
	)
}

func sanitizeMemoryItemsForPromptWithEvidence(
	ctx context.Context,
	input []memory.MemoryItem,
	traceSession trace.Session,
	recorder guardrails.UnsafeEvidenceRecorder,
) []memory.MemoryItem {
	items := make([]memory.MemoryItem, 0, len(input))
	for _, item := range input {
		sanitized, ok := sanitizeMemoryItemForPrompt(
			ctx,
			item,
			traceSession,
			recorder,
		)
		if !ok {
			continue
		}
		items = append(items, sanitized)
	}

	return items
}

// sanitizeMemoryItemForPromptWithTrace sanitizes and safety-scans one memory item.
func sanitizeMemoryItemForPromptWithTrace(item memory.MemoryItem, traceSession trace.Session) (memory.MemoryItem, bool) {
	return sanitizeMemoryItemForPrompt(
		context.Background(),
		item,
		traceSession,
		nil,
	)
}

func sanitizeMemoryItemForPrompt(
	ctx context.Context,
	item memory.MemoryItem,
	traceSession trace.Session,
	recorder guardrails.UnsafeEvidenceRecorder,
) (memory.MemoryItem, bool) {
	if item.Status != memory.StatusActive {
		return memory.MemoryItem{}, false
	}

	original := item
	item.Title = getMemoryPromptText(item.Title)
	item.Text = getMemoryPromptText(item.Text)
	redacted := item.Title != original.Title || item.Text != original.Text
	content := strings.Join([]string{item.Title, item.Text}, "\n")
	titleValue := str.String(item.Title)
	textValue2 := str.String(item.Text)
	if titleValue.Trim() == "" && textValue2.Trim() == "" {
		return memory.MemoryItem{}, false
	}

	// Scan the final title/text pair because either field may contain content
	// from past tool output or user-provided text.
	scanned := guardrails.SafetyScan(
		content,
		item.GuardrailSource(),
	)
	if scanned.Blocked {
		recordMemorySafetyBlocked(traceSession, item.GuardrailSource(), content, scanned.Findings)
		guardrails.RetainUnsafeEvidence(ctx, recorder, guardrails.UnsafeEvidence{
			Source:   item.GuardrailSource(),
			Action:   "blocked",
			Blocked:  true,
			Redacted: redacted,
			Findings: guardrails.SafetyFindingLogFields(scanned.Findings),
			Original: original,
		})
		return memory.MemoryItem{}, false
	}
	if redacted {
		guardrails.RetainUnsafeEvidence(ctx, recorder, guardrails.UnsafeEvidence{
			Source:   item.GuardrailSource(),
			Action:   "redacted",
			Redacted: true,
			Original: original,
			Safe:     item,
		})
	}

	return item, true
}

// recordMemorySafetyBlocked records blocked memory content as a safety trace.
func recordMemorySafetyBlocked(
	traceSession trace.Session,
	source string,
	content string,
	findings []guardrails.SafetyFinding,
) {
	if traceSession == nil {
		return
	}

	traceSession.Record(trace.EvtMemorySafetyBlocked, trace.SafetyEventPayload{
		Source:        source,
		Action:        "blocked",
		ContentLength: len([]rune(content)),
		Blocked:       true,
		Findings:      guardrails.SafetyFindingLogFields(findings),
	})
}

// getMemoryPromptText redacts PII and normalizes memory text before prompt injection.
func getMemoryPromptText(value string) string {
	sanitized, ok := sanitizeMemoryPromptValue(value).(string)
	if !ok {
		value2 := str.String(value)
		return value2.Trim()
	}
	sanitizedValue := str.String(sanitized)
	return sanitizedValue.Trim()
}

// memoryItemsToContextItems converts memory records into instruction-rendering items.
func memoryItemsToContextItems(items []memory.MemoryItem) []instruct.MemoryContextItem {
	contextItems := make([]instruct.MemoryContextItem, 0, len(items))
	for _, item := range items {
		contextItems = append(contextItems, instruct.MemoryContextItem{
			Kind:  string(item.Kind),
			Title: item.Title,
			Text:  item.Text,
		})
	}

	return contextItems
}
