package memory

import (
	"context"
	"errors"

	"github.com/xymorphic/morph/pkg/str"
)

const (
	supersededByMemoryIDMetadataKey = "superseded_by_memory_id"
	supersedesMemoryIDMetadataKey   = "supersedes_memory_id"
)

func (p *MemoryProvider) Update(ctx context.Context, req UpdateRequest) (UpdateResult, error) {
	if p == nil || p.manager == nil {
		return UpdateResult{}, errors.New("memory provider is required")
	}

	iDValue := str.String(req.ID)
	memoryID := iDValue.Trim()
	if memoryID == "" {
		return UpdateResult{}, errors.New("memory id is required")
	}

	previous, err := p.loadMemoryByID(ctx, memoryID, []Status{StatusActive})
	if err != nil {
		return UpdateResult{}, err
	}

	replacement := req.Replacement.Clone()
	if replacement.Metadata == nil {
		replacement.Metadata = make(map[string]string)
	}
	replacement.Metadata[supersedesMemoryIDMetadataKey] = previous.ID
	metadataValue := str.String(previous.Metadata[MemoryMetadataSourceSessionID])
	if sessionID := metadataValue.Trim(); sessionID != "" {
		metadataValue2 := str.String(replacement.Metadata[MemoryMetadataSourceSessionID])
		if metadataValue2.Trim() == "" {
			replacement.Metadata[MemoryMetadataSourceSessionID] = sessionID
		}
	}

	var candidate MemoryItem
	switch replacement.Kind {
	case KindSemantic:
		candidate, err = p.RecordSemanticMemory(ctx, SemanticRecord{Item: replacement})
	case KindProcedural:
		candidate, err = p.RecordProceduralMemory(ctx, ProceduralRecord{Item: replacement})
	default:
		return UpdateResult{}, errors.New("replacement memory kind must be semantic or procedural")
	}
	if err != nil {
		return UpdateResult{}, err
	}

	superseded, err := p.supersedeMemory(ctx, previous, candidate.ID, req.Reason)
	if err != nil {
		return UpdateResult{}, err
	}

	lifecycle, err := p.PromoteCandidate(ctx, PromotionRequest{
		ID:     candidate.ID,
		Reason: getUpdatePromotionReason(req.Reason),
	})
	if err != nil {
		_ = p.restoreMemory(ctx, previous)
		return UpdateResult{}, err
	}
	if !lifecycle.Decision.Approved {
		_ = p.restoreMemory(ctx, previous)
		return UpdateResult{
			Previous:    previous.Clone(),
			Replacement: lifecycle.Item.Clone(),
			Lifecycle:   lifecycle,
		}, nil
	}

	fields := buildObservationFields(p.Name(), "update", map[string]any{
		"memory_id":              previous.ID,
		"replacement_memory_id":  lifecycle.Item.ID,
		"replacement_approved":   lifecycle.Decision.Approved,
		"replacement_status":     lifecycle.Item.Status,
		"superseded_memory_kind": previous.Kind,
	})
	logDebugAndTrace(ctx, p.observability(), "memory update completed", "memory.update.completed", fields)

	return UpdateResult{
		Previous:    superseded.Clone(),
		Replacement: lifecycle.Item.Clone(),
		Lifecycle:   lifecycle,
	}, nil
}

func (p *MemoryProvider) supersedeMemory(
	ctx context.Context,
	item MemoryItem,
	replacementID string,
	reason string,
) (MemoryItem, error) {
	status := StatusSuperseded
	metadata := buildLifecycleMetadata(item.Metadata, "supersede", reason, item.Status)
	replacementIDValue := str.String(replacementID)
	metadata[supersededByMemoryIDMetadataKey] = replacementIDValue.Trim()

	return p.manager.PatchMemory(ctx, MemoryPatch{
		ID:       item.ID,
		Status:   &status,
		Metadata: metadata,
	})
}

func (p *MemoryProvider) restoreMemory(ctx context.Context, item MemoryItem) error {
	_, err := p.manager.UpsertMemory(ctx, item.Clone())
	return err
}

func getUpdatePromotionReason(reason string) string {
	reasonValue := str.String(reason)
	reason = reasonValue.Trim()
	if reason == "" {
		return "tool_memory_update"
	}

	return reason
}
