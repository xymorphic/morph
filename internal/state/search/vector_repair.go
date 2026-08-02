package search

import (
	"context"
	"fmt"
	"sort"

	"github.com/xymorphic/morph/internal/constants"
	state "github.com/xymorphic/morph/internal/state/core"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/str"
)

// DefaultVectorRepairBatchSize is the package-level default vector repair batch size constant.
const DefaultVectorRepairBatchSize = constants.DefaultVectorRepairBatchSize

// VectorRepairOptions controls selective or full vector repair for session messages.
type VectorRepairOptions struct {
	SessionID string
	Full      bool
	BatchSize int
}

// VectorRepairResult reports what vector repair inspected and rebuilt.
type VectorRepairResult struct {
	SessionsScanned    int
	MessagesScanned    int
	RowsScanned        int
	MissingRows        int
	StaleRows          int
	UnchangedRows      int
	RebuiltRows        int
	DeletedSources     int
	Batches            int
	AttemptedSources   int
	RecoveredSources   int
	StillFailedSources int
}

// Add merges another repair result into r.
func (r *VectorRepairResult) Add(other VectorRepairResult) {
	if r == nil {
		return
	}

	r.SessionsScanned += other.SessionsScanned
	r.MessagesScanned += other.MessagesScanned
	r.RowsScanned += other.RowsScanned
	r.MissingRows += other.MissingRows
	r.StaleRows += other.StaleRows
	r.UnchangedRows += other.UnchangedRows
	r.RebuiltRows += other.RebuiltRows
	r.DeletedSources += other.DeletedSources
	r.Batches += other.Batches
	r.AttemptedSources += other.AttemptedSources
	r.RecoveredSources += other.RecoveredSources
	r.StillFailedSources += other.StillFailedSources
}

// VectorRepairStore is implemented by stores that can repair session-message vector rows.
type VectorRepairStore interface {
	RepairVectorStore(context.Context, VectorRepairOptions) (VectorRepairResult, error)
}

// RequireVectorRecordLister adapts a vector store to the listing capability required by repair.
func RequireVectorRecordLister(store VectorStore) (VectorRecordLister, error) {
	lister, ok := store.(VectorRecordLister)
	if !ok || lister == nil {
		return nil, fmt.Errorf("vector store record listing is required")
	}

	return lister, nil
}

// DirtyVectorSources returns source IDs whose vector rows are missing, stale, extra, or forced.
func DirtyVectorSources(
	ctx context.Context,
	lister VectorRecordLister,
	model string,
	inputs []VectorInput,
	full bool,
) ([]string, VectorRepairResult, error) {
	var result VectorRepairResult
	if lister == nil {
		return nil, result, fmt.Errorf("vector store record listing is required")
	}
	if len(inputs) == 0 {
		return nil, result, nil
	}

	sourceIDs := make([]string, 0, len(inputs))
	expectedByID := make(map[string]VectorInput, len(inputs))
	for _, input := range inputs {
		sourceIDs = append(sourceIDs, input.SourceID)
		expectedByID[input.ID] = input
	}
	sourceIDs = state.UniqueStrings(sourceIDs)
	modelValue := str.String(model)
	list, err := lister.List(ctx, VectorListRequest{
		EmbeddingModel: modelValue.Trim(),
		Filter: VectorFilter{
			SourceKind: SourceKindSessionMessage,
			SourceIDs:  sourceIDs,
		},
	})
	if err != nil {
		return nil, result, err
	}

	recordsByID := make(map[string]VectorRecord, len(list.Records))
	for _, record := range list.Records {
		recordsByID[record.ID] = record
	}

	dirtySourceSet := make(map[string]struct{}, len(sourceIDs))
	for _, input := range inputs {
		record, ok := recordsByID[input.ID]
		if !ok {
			result.MissingRows++
			dirtySourceSet[input.SourceID] = struct{}{}
			continue
		}
		if IsVectorRecordStale(record, input.Text) {
			result.StaleRows++
			dirtySourceSet[input.SourceID] = struct{}{}
			continue
		}
		result.UnchangedRows++
	}

	for _, record := range list.Records {
		if _, ok := expectedByID[record.ID]; ok {
			continue
		}

		result.StaleRows++
		dirtySourceSet[record.SourceID] = struct{}{}
	}

	if full {
		for _, sourceID := range sourceIDs {
			dirtySourceSet[sourceID] = struct{}{}
		}
	}

	dirtySources := make([]string, 0, len(dirtySourceSet))
	for sourceID := range dirtySourceSet {
		dirtySources = append(dirtySources, sourceID)
	}
	sort.Strings(dirtySources)

	return dirtySources, result, nil
}

// MessagesBySourceID returns messages whose stable source IDs are present in sourceIDs.
func MessagesBySourceID(sessionID string, messages []morphmsg.Message, sourceIDs []string) []morphmsg.Message {
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

	selected := make([]morphmsg.Message, 0, len(messages))
	for _, message := range messages {
		if _, ok := sourceSet[SourceIDForMessage(sessionID, message.ID)]; ok {
			selected = append(selected, message)
		}
	}

	return selected
}
