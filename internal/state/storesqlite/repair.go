package storesqlite

import (
	"context"
	"errors"
	"sort"

	"gorm.io/gorm"

	state "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/internal/state/search"
	"github.com/xymorphic/morph/pkg/str"
)

// RebuildVectorStore refreshes all vector rows for one active session in batches.
func (s *Store) RebuildVectorStore(ctx context.Context, id string) error {
	idValue := str.String(id)
	id = idValue.Trim()
	if err := state.ValidateSessionID(id); err != nil {
		return err
	}

	_, err := s.RepairVectorStore(ctx, search.VectorRepairOptions{
		SessionID: id,
		Full:      true,
	})
	return err
}

// RepairVectorStore repairs missing, stale, or extra vector rows for active session messages.
func (s *Store) RepairVectorStore(ctx context.Context, opts search.VectorRepairOptions) (search.VectorRepairResult, error) {
	if s == nil || s.db == nil {
		return search.VectorRepairResult{}, errors.New("store is required")
	}
	if s.vectors == nil {
		return search.VectorRepairResult{}, nil
	}

	sessionIDValue := str.String(opts.SessionID)
	sessionID := sessionIDValue.Trim()
	if sessionID != "" {
		if err := state.ValidateSessionID(sessionID); err != nil {
			return search.VectorRepairResult{}, err
		}
	}

	batchSize := opts.BatchSize
	if batchSize < 0 {
		return search.VectorRepairResult{}, errors.New("vector repair batch size must be greater than or equal to zero")
	}
	if batchSize == 0 {
		batchSize = s.vectors.batchSize
	}

	lister, err := search.RequireVectorRecordLister(s.vectors.Store)
	if err != nil {
		return search.VectorRepairResult{}, err
	}

	sessionIDs, err := s.repairSessionIDs(ctx, sessionID)
	if err != nil {
		return search.VectorRepairResult{}, err
	}

	var result search.VectorRepairResult
	result.SessionsScanned = len(sessionIDs)
	for _, id := range sessionIDs {
		lastSequence := -1
		for {
			var records []messageModel
			if err := s.db.WithContext(ctx).
				Where("session_id = ? AND sequence > ?", id, lastSequence).
				Order("sequence asc").
				Limit(batchSize).
				Find(&records).Error; err != nil {
				return result, err
			}
			if len(records) == 0 {
				break
			}

			batchResult, err := s.repairVectorBatch(ctx, lister, records, opts.Full)
			result.Add(batchResult)
			if err != nil {
				if requiredErr := s.handleVectorStoreError(err); requiredErr != nil {
					return result, requiredErr
				}
			}

			lastSequence = records[len(records)-1].Sequence
		}
	}

	return result, nil
}

// repairSessionIDs returns the active sessions that should be scanned during vector repair.
func (s *Store) repairSessionIDs(ctx context.Context, sessionID string) ([]string, error) {
	if sessionID != "" {
		var session sessionModel
		if err := s.db.WithContext(ctx).First(&session, "id = ?", sessionID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, errors.New("session not found")
			}

			return nil, err
		}
		return []string{sessionID}, nil
	}

	var sessions []sessionModel
	if err := s.db.WithContext(ctx).Order("id asc").Find(&sessions).Error; err != nil {
		return nil, err
	}

	sessionIDs := make([]string, 0, len(sessions))
	for _, session := range sessions {
		sessionIDs = append(sessionIDs, session.ID)
	}

	return sessionIDs, nil
}

// repairVectorBatch compares one message batch with vector storage and rebuilds dirty sources.
func (s *Store) repairVectorBatch(
	ctx context.Context,
	lister search.VectorRecordLister,
	records []messageModel,
	full bool,
) (search.VectorRepairResult, error) {
	var result search.VectorRepairResult
	if len(records) == 0 {
		return result, nil
	}

	inputs := messageModels(records).searchRows().vectorInputs(s.vectors.Chunking)
	result.MessagesScanned = len(records)
	result.RowsScanned = len(inputs)
	if len(inputs) == 0 {
		return result, nil
	}

	dirtySources, batchResult, err := search.DirtyVectorSources(ctx, lister, s.vectors.Model, inputs, full)
	result.Add(batchResult)
	if err != nil {
		return result, err
	}
	retryableSources, err := s.loadRetryableVectorSourceIDs(ctx, inputs)
	if err != nil {
		return result, err
	}
	dirtySources = state.UniqueStrings(append(dirtySources, retryableSources...))
	sort.Strings(dirtySources)
	if len(dirtySources) == 0 {
		return result, nil
	}

	dirtyRecords := getMessageModelsBySourceID(records, dirtySources)
	result.AttemptedSources = len(dirtySources)
	recordsToUpsert, err := s.vectorRecordsForMessages(ctx, dirtyRecords)
	if err != nil {
		result.StillFailedSources = len(dirtySources)
		_ = s.setVectorIndexResult(ctx, dirtyRecords, err)
		return result, err
	}

	deleteErr := s.deleteVectorRows(ctx, dirtySources)
	upsertErr := s.upsertVectorRecords(ctx, recordsToUpsert)
	repairErr := deleteErr
	if repairErr == nil {
		repairErr = upsertErr
	}
	if statusErr := s.setVectorIndexResult(ctx, dirtyRecords, repairErr); repairErr == nil {
		repairErr = statusErr
	}
	result.DeletedSources = len(dirtySources)
	result.RebuiltRows = len(messageModels(dirtyRecords).searchRows().vectorInputs(s.vectors.Chunking))
	result.Batches = 1
	if repairErr != nil {
		result.StillFailedSources = len(dirtySources)
		return result, repairErr
	}
	result.RecoveredSources = len(dirtySources)

	return result, nil
}

// getMessageModelsBySourceID returns message records whose vector source IDs are marked dirty.
func getMessageModelsBySourceID(records []messageModel, sourceIDs []string) []messageModel {
	sourceSet := make(map[string]struct{}, len(sourceIDs))
	for _, sourceID := range sourceIDs {
		sourceIDValue := str.String(sourceID)
		sourceID = sourceIDValue.Trim()
		if sourceID != "" {
			sourceSet[sourceID] = struct{}{}
		}
	}
	if len(sourceSet) == 0 {
		return nil
	}

	selected := make([]messageModel, 0, len(records))
	for _, record := range records {
		if _, ok := sourceSet[messageToSourceID(record.SessionID, record.ID)]; ok {
			selected = append(selected, record)
		}
	}

	return selected
}
