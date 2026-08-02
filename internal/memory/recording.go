package memory

import (
	"context"
	"errors"
	"strings"

	"github.com/xymorphic/morph/pkg/nanoid"
	"github.com/xymorphic/morph/pkg/str"
)

// RecordSemanticMemory records a candidate that captures a durable fact,
// preference, correction, or relationship. It does not activate the memory; the
// promotion lifecycle decides that later.
func (p *MemoryProvider) RecordSemanticMemory(ctx context.Context, record SemanticRecord) (MemoryItem, error) {
	item := record.Item
	item.Kind = KindSemantic

	return p.recordMemoryCandidate(ctx, item)
}

// RecordProceduralMemory records a candidate that captures a reusable process
// or instruction. Like semantic memory, it must carry provenance and starts as a
// candidate.
func (p *MemoryProvider) RecordProceduralMemory(
	ctx context.Context,
	record ProceduralRecord,
) (MemoryItem, error) {
	item := record.Item
	item.Kind = KindProcedural

	return p.recordMemoryCandidate(ctx, item)
}

// recordMemoryCandidate is the shared non-episodic recording path. The provider
// stamps kind-aware IDs, validates provenance/admission metadata, applies
// guardrails, and only then writes to storage.
func (p *MemoryProvider) recordMemoryCandidate(ctx context.Context, item MemoryItem) (MemoryItem, error) {
	if p == nil || p.manager == nil {
		return MemoryItem{}, errors.New("memory provider is required")
	}

	item = prepareMemoryCandidate(item)
	if err := validateMemoryCandidate(item); err != nil {
		return MemoryItem{}, err
	}
	if err := validateWrite(ctx, p.guardrails, item); err != nil {
		return MemoryItem{}, err
	}

	item, err := p.manager.UpsertMemory(ctx, item)
	if err != nil {
		return MemoryItem{}, err
	}

	fields := buildObservationFields(p.Name(), "record_candidate", map[string]any{"memory_id": item.ID})
	logDebugAndTrace(ctx, p.observability(), "memory candidate recorded", "memory.candidate_write.completed", fields)

	return item.Clone(), nil
}

// prepareMemoryCandidate normalizes provider-owned defaults without mutating
// the caller's item. The clone matters because metadata maps and source-link
// slices are shared mutable data otherwise.
func prepareMemoryCandidate(item MemoryItem) MemoryItem {
	item = item.Clone()
	iDValue := str.String(item.ID)
	item.ID = iDValue.Trim()
	if item.Status == "" {
		item.Status = StatusCandidate
	}
	if item.Metadata == nil {
		item.Metadata = make(map[string]string)
	}
	if item.ID == "" {
		item.ID = getKindAwareMemoryID(item.Kind)
	}

	return item
}

// validateMemoryCandidate enforces the minimum shape for explicit semantic and
// procedural candidates. Episodic/reflection candidates have their own builders
// because they derive provenance differently.
func validateMemoryCandidate(item MemoryItem) error {
	switch item.Kind {
	case KindSemantic, KindProcedural:
	default:
		return errors.New("memory candidate kind must be semantic or procedural")
	}
	if item.Status != StatusCandidate {
		return errors.New("memory candidate must be stored as candidate")
	}
	titleValue := str.String(item.Title)
	textValue := str.String(item.Text)
	if titleValue.Trim() == "" && textValue.Trim() == "" {
		return errors.New("memory candidate text or title is required")
	}
	if !hasCandidateProvenance(item) {
		return errors.New("memory candidate source provenance is required")
	}
	if reason := checkCandidateAdmissionRejection(item); reason != "" {
		return errors.New(reason)
	}

	return nil
}

// checkCandidateAdmissionRejection applies deterministic admission hints that
// the model or extractor attached as metadata. Low-importance or execution-only
// details should not enter the candidate lifecycle.
func checkCandidateAdmissionRejection(item MemoryItem) string {
	metadataValue := str.String(item.Metadata["memory_importance"])
	switch metadataValue.Normalized() {
	case "low":
		return "low_importance_candidate"
	}
	metadataValue2 := str.String(item.Metadata["memory_granularity"])
	switch metadataValue2.Normalized() {
	case "execution_detail":
		return "execution_detail"
	}

	return ""
}

// hasCandidateProvenance verifies that a candidate can be traced back to a
// session, message range, summary, or explicit source-session metadata.
func hasCandidateProvenance(item MemoryItem) bool {
	return HasSourceProvenance(item)
}

// getKindAwareMemoryID makes IDs self-describing in logs and database
// inspection while preserving nanoid uniqueness.
func getKindAwareMemoryID(kind Kind) string {
	return getKindAwareMemoryIDPrefix(kind) + strings.TrimPrefix(nanoid.MustGenerate("mem_"), "mem_")
}

func getKindAwareMemoryIDPrefix(kind Kind) string {
	kindValue := str.String(string(kind))
	kindPart := kindValue.Trim()
	if kindPart == "" {
		kindPart = "unknown"
	}
	return "mem_" + kindPart + "_"
}
