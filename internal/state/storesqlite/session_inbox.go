package storesqlite

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	base "github.com/wandxy/morph/internal/state/core"
	morphmsg "github.com/wandxy/morph/pkg/agent/message"
	agentsession "github.com/wandxy/morph/pkg/agent/session"
	"github.com/wandxy/morph/pkg/str"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	sessionEventRetentionAge   = 7 * 24 * time.Hour
	sessionEventRetentionCount = int64(10000)
	sessionStateTerminalLimit  = 256
	sessionRunnerStateID       = 1
)

type sessionRunnerStateModel struct {
	ID         int `gorm:"primaryKey"`
	Generation string
	UpdatedAt  time.Time
}

func (sessionRunnerStateModel) TableName() string {
	return "session_runner_state"
}

type sessionExecutionStateModel struct {
	SessionID         string `gorm:"primaryKey"`
	Cursor            int64
	NextQueueSequence int64
	RetainedFloor     int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func (sessionExecutionStateModel) TableName() string {
	return "session_execution_state"
}

type sessionQueueEntryModel struct {
	ID                    string `gorm:"primaryKey"`
	SessionID             string `gorm:"uniqueIndex:idx_session_queue_submission,priority:1;index:idx_session_queue_pending,priority:1"`
	ClientSubmissionID    string `gorm:"uniqueIndex:idx_session_queue_submission,priority:2"`
	Content               string `gorm:"type:text;not null"`
	Instruct              string `gorm:"type:text"`
	Stream                *bool
	TargetRunID           string `gorm:"index"`
	RequestedDeliveryMode string
	DeliveryMode          string `gorm:"index:idx_session_queue_pending,priority:3"`
	SteeringFallback      string
	Status                string `gorm:"index:idx_session_queue_pending,priority:2"`
	ActorKind             string
	ActorID               string
	SurfaceKind           string
	Surface               string
	Profile               string
	Sequence              int64 `gorm:"index:idx_session_queue_pending,priority:5"`
	Priority              int64 `gorm:"index:idx_session_queue_pending,priority:4"`
	CreatedAt             time.Time
	UpdatedAt             time.Time
	StartedAt             time.Time
	CompletedAt           time.Time
	LastError             string `gorm:"type:text"`
}

func (sessionQueueEntryModel) TableName() string {
	return "session_queue_entries"
}

type sessionRunModel struct {
	ID                string `gorm:"primaryKey"`
	SessionID         string `gorm:"index"`
	QueueEntryID      string `gorm:"index"`
	Generation        string
	Status            string `gorm:"index"`
	StartedAt         time.Time
	CompletedAt       time.Time
	UpdatedAt         time.Time
	Reason            string
	LastError         string `gorm:"type:text"`
	ReasoningProvider string
	ReasoningAPI      string
	ReasoningModel    string
	ReasoningEffort   string
	ReasoningSummary  bool
}

func (sessionRunModel) TableName() string {
	return "session_runs"
}

type sessionEventModel struct {
	ID        uint   `gorm:"primaryKey"`
	SessionID string `gorm:"uniqueIndex:idx_session_events_cursor,priority:1;index:idx_session_events_created,priority:1"`
	Cursor    int64  `gorm:"uniqueIndex:idx_session_events_cursor,priority:2"`
	Type      string
	QueueJSON string    `gorm:"type:text"`
	RunJSON   string    `gorm:"type:text"`
	CreatedAt time.Time `gorm:"index:idx_session_events_created,priority:2"`
}

func (sessionEventModel) TableName() string {
	return "session_events"
}

func (s *Store) SubmitMessage(
	ctx context.Context,
	req agentsession.SubmitRequest,
) (agentsession.QueueEntry, error) {
	if s == nil || s.db == nil {
		return agentsession.QueueEntry{}, errors.New("store is required")
	}

	req, err := normalizeSubmitRequest(req)
	if err != nil {
		return agentsession.QueueEntry{}, err
	}

	requestedDeliveryMode := req.DeliveryMode
	var entry agentsession.QueueEntry
	err = runSQLiteWriteWithRetry(ctx, func(writeCtx context.Context) error {
		effectiveDeliveryMode := requestedDeliveryMode
		targetRunID := ""

		return s.db.WithContext(writeCtx).Transaction(func(tx *gorm.DB) error {
			if err := requireSession(tx, req.SessionID); err != nil {
				return err
			}
			var existing sessionQueueEntryModel
			err = tx.Where(
				"session_id = ? AND client_submission_id = ?",
				req.SessionID,
				req.ClientSubmissionID,
			).First(&existing).Error
			if err == nil {
				entry = queueEntryModelToQueueEntry(existing)
				if !isSameSubmission(entry, req) {
					return errors.New("client submission id is already used by a different message")
				}
				return nil
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}

			if effectiveDeliveryMode == agentsession.DeliveryModeSteering {
				var active sessionRunModel
				err := tx.Where("session_id = ? AND status = ?", req.SessionID, agentsession.RunStatusRunning).
					First(&active).Error
				switch {
				case err == nil:
					targetRunID = active.ID
				case errors.Is(err, gorm.ErrRecordNotFound) &&
					req.SteeringFallback == agentsession.SteeringFallbackFollowUp:
					effectiveDeliveryMode = agentsession.DeliveryModeFollowUp
				case errors.Is(err, gorm.ErrRecordNotFound):
					return agentsession.ErrSteeringRequiresRun
				default:
					return err
				}
			}

			sequence, err := nextQueueSequence(tx, req.SessionID)
			if err != nil {
				return err
			}
			now := time.Now().UTC()
			entry = agentsession.QueueEntry{
				ID:                    req.ID,
				SessionID:             req.SessionID,
				Content:               req.Content,
				Instruct:              req.Instruct,
				Stream:                cloneInboxBool(req.Stream),
				ClientSubmissionID:    req.ClientSubmissionID,
				TargetRunID:           targetRunID,
				RequestedDeliveryMode: requestedDeliveryMode,
				DeliveryMode:          effectiveDeliveryMode,
				SteeringFallback:      req.SteeringFallback,
				Status:                agentsession.QueueStatusPending,
				Provenance:            req.Provenance,
				Sequence:              sequence,
				CreatedAt:             now,
				UpdatedAt:             now,
			}
			record := queueEntryToQueueEntryModel(entry)
			if err := tx.Create(&record).Error; err != nil {
				return err
			}
			if _, err := appendSessionEvent(tx, agentsession.Event{
				SessionID: req.SessionID,
				Type:      agentsession.EventTypeQueueEnqueued,
				Queue:     cloneQueueEntry(&entry),
				CreatedAt: now,
			}); err != nil {
				return err
			}

			return pruneSessionEvents(tx, req.SessionID, now)
		})
	})

	return entry, err
}

func (s *Store) GetExecutionState(
	ctx context.Context,
	sessionID string,
) (agentsession.ExecutionState, error) {
	if s == nil || s.db == nil {
		return agentsession.ExecutionState{}, errors.New("store is required")
	}
	sessionID, err := normalizeSessionID(sessionID)
	if err != nil {
		return agentsession.ExecutionState{}, err
	}

	var state agentsession.ExecutionState
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireSession(tx, sessionID); err != nil {
			return err
		}
		state.SessionID = sessionID

		var execution sessionExecutionStateModel
		err := tx.Where("session_id = ?", sessionID).First(&execution).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			state.Cursor = execution.Cursor
			state.RetainedCursorFloor = execution.RetainedFloor
		}

		var run sessionRunModel
		err = tx.Where("session_id = ? AND status = ?", sessionID, agentsession.RunStatusRunning).
			First(&run).Error
		if err == nil {
			value := runModelToActiveRun(run)
			state.ActiveRun = &value
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		nonterminalStatuses := []string{
			string(agentsession.QueueStatusPending),
			string(agentsession.QueueStatusActive),
		}
		var records []sessionQueueEntryModel
		if err := tx.Where("session_id = ? AND status IN ?", sessionID, nonterminalStatuses).
			Order("sequence ASC").
			Find(&records).Error; err != nil {
			return err
		}
		var terminalRecords []sessionQueueEntryModel
		if err := tx.Where("session_id = ? AND status NOT IN ?", sessionID, nonterminalStatuses).
			Order("sequence DESC").
			Limit(sessionStateTerminalLimit).
			Find(&terminalRecords).Error; err != nil {
			return err
		}
		records = append(records, terminalRecords...)
		slices.SortFunc(records, func(left, right sessionQueueEntryModel) int {
			return cmp.Compare(left.Sequence, right.Sequence)
		})
		state.Queue = queueEntryModelsToQueueEntries(records)
		for _, item := range state.Queue {
			if item.Status != agentsession.QueueStatusPending {
				continue
			}
			state.QueueDepth++
			if state.OldestPendingCreated.IsZero() || item.CreatedAt.Before(state.OldestPendingCreated) {
				state.OldestPendingCreated = item.CreatedAt
			}
		}

		return nil
	})

	return state, err
}

func (s *Store) ListEvents(
	ctx context.Context,
	sessionID string,
	afterCursor int64,
	limit int,
) (agentsession.EventBatch, error) {
	if s == nil || s.db == nil {
		return agentsession.EventBatch{}, errors.New("store is required")
	}
	sessionID, err := normalizeSessionID(sessionID)
	if err != nil {
		return agentsession.EventBatch{}, err
	}
	if afterCursor < 0 {
		return agentsession.EventBatch{}, errors.New("after cursor must be greater than or equal to zero")
	}
	if limit <= 0 || limit > 256 {
		limit = 256
	}

	var batch agentsession.EventBatch
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireSession(tx, sessionID); err != nil {
			return err
		}

		var execution sessionExecutionStateModel
		err := tx.Where("session_id = ?", sessionID).First(&execution).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if afterCursor > 0 {
				return agentsession.ErrCursorBeyondSession
			}
			return nil
		}
		if err != nil {
			return err
		}
		if afterCursor > execution.Cursor {
			return agentsession.ErrCursorBeyondSession
		}
		if execution.RetainedFloor > 0 && afterCursor+1 < execution.RetainedFloor {
			return agentsession.ErrCursorExpired
		}

		var records []sessionEventModel
		if err := tx.Where("session_id = ? AND cursor > ?", sessionID, afterCursor).
			Order("cursor ASC").
			Limit(limit).
			Find(&records).Error; err != nil {
			return err
		}

		batch.Events = make([]agentsession.Event, 0, len(records))
		for _, record := range records {
			event, err := eventModelToSessionEvent(record)
			if err != nil {
				return err
			}
			batch.Events = append(batch.Events, event)
		}
		batch.Cursor = execution.Cursor
		batch.RetainedCursorFloor = execution.RetainedFloor
		return nil
	})

	return batch, err
}

func (s *Store) EditQueueEntry(
	ctx context.Context,
	req agentsession.QueueEditRequest,
) (agentsession.QueueEntry, error) {
	content := str.String(req.Content).Trim()
	if content == "" {
		return agentsession.QueueEntry{}, errors.New("message is required")
	}
	return s.updatePendingQueueEntry(ctx, req.SessionID, req.EntryID, func(_ *gorm.DB, record *sessionQueueEntryModel) error {
		record.Content = content
		record.UpdatedAt = time.Now().UTC()
		return nil
	}, agentsession.EventTypeQueueUpdated)
}

func (s *Store) CancelQueueEntry(
	ctx context.Context,
	req agentsession.QueueMutationRequest,
) (agentsession.QueueEntry, error) {
	return s.updatePendingQueueEntry(ctx, req.SessionID, req.EntryID, func(_ *gorm.DB, record *sessionQueueEntryModel) error {
		now := time.Now().UTC()
		record.Status = string(agentsession.QueueStatusCancelled)
		record.CompletedAt = now
		record.UpdatedAt = now
		return nil
	}, agentsession.EventTypeQueueCancelled)
}

func (s *Store) PromoteQueueEntry(
	ctx context.Context,
	req agentsession.QueueMutationRequest,
) (agentsession.QueueEntry, error) {
	return s.updatePendingQueueEntry(ctx, req.SessionID, req.EntryID, func(tx *gorm.DB, record *sessionQueueEntryModel) error {
		var priority int64
		if err := tx.Model(&sessionQueueEntryModel{}).
			Where("session_id = ?", record.SessionID).
			Select("COALESCE(MAX(priority), 0) + 1").
			Scan(&priority).Error; err != nil {
			return err
		}
		record.Priority = priority
		record.UpdatedAt = time.Now().UTC()
		return nil
	}, agentsession.EventTypeQueueUpdated)
}

func (s *Store) SteerQueueEntry(
	ctx context.Context,
	req agentsession.QueueMutationRequest,
) (agentsession.QueueEntry, error) {
	return s.updatePendingQueueEntry(ctx, req.SessionID, req.EntryID, func(tx *gorm.DB, record *sessionQueueEntryModel) error {
		record.RequestedDeliveryMode = string(agentsession.DeliveryModeSteering)
		record.SteeringFallback = string(agentsession.SteeringFallbackFollowUp)
		record.TargetRunID = ""
		record.DeliveryMode = string(agentsession.DeliveryModeFollowUp)

		var active sessionRunModel
		err := tx.Where(
			"session_id = ? AND status = ?",
			record.SessionID,
			agentsession.RunStatusRunning,
		).First(&active).Error
		switch {
		case err == nil:
			record.TargetRunID = active.ID
			record.DeliveryMode = string(agentsession.DeliveryModeSteering)
		case errors.Is(err, gorm.ErrRecordNotFound):
		default:
			return err
		}
		record.UpdatedAt = time.Now().UTC()
		return nil
	}, agentsession.EventTypeQueueUpdated)
}

func (s *Store) updatePendingQueueEntry(
	ctx context.Context,
	sessionID string,
	entryID string,
	update func(*gorm.DB, *sessionQueueEntryModel) error,
	eventType agentsession.EventType,
) (agentsession.QueueEntry, error) {
	if s == nil || s.db == nil {
		return agentsession.QueueEntry{}, errors.New("store is required")
	}
	sessionID, err := normalizeSessionID(sessionID)
	if err != nil {
		return agentsession.QueueEntry{}, err
	}
	entryID = str.String(entryID).Trim()
	if entryID == "" {
		return agentsession.QueueEntry{}, errors.New("queue entry id is required")
	}

	var entry agentsession.QueueEntry
	err = runSQLiteWriteWithRetry(ctx, func(writeCtx context.Context) error {
		return s.db.WithContext(writeCtx).Transaction(func(tx *gorm.DB) error {
			var record sessionQueueEntryModel
			if err := tx.Where(
				"id = ? AND session_id = ? AND status = ?",
				entryID,
				sessionID,
				agentsession.QueueStatusPending,
			).First(&record).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return errors.New("pending queue entry not found")
				}
				return err
			}
			if err := update(tx, &record); err != nil {
				return err
			}
			if err := tx.Save(&record).Error; err != nil {
				return err
			}
			entry = queueEntryModelToQueueEntry(record)
			if _, err := appendSessionEvent(tx, agentsession.Event{
				SessionID: sessionID,
				Type:      eventType,
				Queue:     cloneQueueEntry(&entry),
				CreatedAt: record.UpdatedAt,
			}); err != nil {
				return err
			}
			return pruneSessionEvents(tx, sessionID, record.UpdatedAt)
		})
	})

	return entry, err
}

func (s *Store) ClaimNextFollowUp(
	ctx context.Context,
	req agentsession.ClaimRequest,
) (agentsession.QueueEntry, agentsession.ActiveRun, bool, error) {
	if s == nil || s.db == nil {
		return agentsession.QueueEntry{}, agentsession.ActiveRun{}, false, errors.New("store is required")
	}
	req.SessionID = str.String(req.SessionID).Trim()
	req.RunID = str.String(req.RunID).Trim()
	req.Generation = str.String(req.Generation).Trim()
	if err := base.ValidateSessionID(req.SessionID); err != nil {
		return agentsession.QueueEntry{}, agentsession.ActiveRun{}, false, err
	}
	if req.RunID == "" || req.Generation == "" {
		return agentsession.QueueEntry{}, agentsession.ActiveRun{}, false, errors.New("run id and generation are required")
	}

	var entry agentsession.QueueEntry
	var run agentsession.ActiveRun
	claimed := false
	err := runSQLiteWriteWithRetry(ctx, func(writeCtx context.Context) error {
		entry = agentsession.QueueEntry{}
		run = agentsession.ActiveRun{}
		claimed = false

		return s.db.WithContext(writeCtx).Transaction(func(tx *gorm.DB) error {
			var runnerState sessionRunnerStateModel
			err := tx.Where("id = ? AND generation = ?", sessionRunnerStateID, req.Generation).
				First(&runnerState).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return agentsession.ErrStaleRunnerGeneration
			}
			if err != nil {
				return err
			}
			if err := requireSession(tx, req.SessionID); err != nil {
				return err
			}
			var sessionRecord sessionModel
			if err := tx.Select("id", "reasoning_effort_override").
				Where("id = ?", req.SessionID).
				First(&sessionRecord).Error; err != nil {
				return err
			}

			var activeCount int64
			if err := tx.Model(&sessionRunModel{}).
				Where("session_id = ? AND status = ?", req.SessionID, agentsession.RunStatusRunning).
				Count(&activeCount).Error; err != nil {
				return err
			}
			if activeCount > 0 {
				return nil
			}

			var queueRecord sessionQueueEntryModel
			err = tx.Where(
				"session_id = ? AND status = ? AND delivery_mode = ?",
				req.SessionID,
				agentsession.QueueStatusPending,
				agentsession.DeliveryModeFollowUp,
			).Order("priority DESC").Order("sequence ASC").First(&queueRecord).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			if err != nil {
				return err
			}

			now := time.Now().UTC()
			queueRecord.Status = string(agentsession.QueueStatusActive)
			queueRecord.StartedAt = now
			queueRecord.UpdatedAt = now
			if err := tx.Save(&queueRecord).Error; err != nil {
				return err
			}
			reasoning := agentsession.ResolveReasoningSnapshot(
				req.Reasoning,
				reasoningOverrideValue(sessionRecord.ReasoningEffortOverride),
			)
			runRecord := sessionRunModel{
				ID:                req.RunID,
				SessionID:         req.SessionID,
				QueueEntryID:      queueRecord.ID,
				Generation:        req.Generation,
				Status:            string(agentsession.RunStatusRunning),
				StartedAt:         now,
				UpdatedAt:         now,
				ReasoningProvider: reasoning.Provider,
				ReasoningAPI:      reasoning.API,
				ReasoningModel:    reasoning.Model,
				ReasoningEffort:   string(reasoning.Effort),
				ReasoningSummary:  reasoning.Summary,
			}
			if err := tx.Create(&runRecord).Error; err != nil {
				return err
			}

			entry = queueEntryModelToQueueEntry(queueRecord)
			run = runModelToActiveRun(runRecord)
			if _, err := appendSessionEvent(tx, agentsession.Event{
				SessionID: req.SessionID,
				Type:      agentsession.EventTypeQueueClaimed,
				Queue:     cloneQueueEntry(&entry),
				CreatedAt: now,
			}); err != nil {
				return err
			}
			if _, err := appendSessionEvent(tx, agentsession.Event{
				SessionID: req.SessionID,
				Type:      agentsession.EventTypeRunStarted,
				Run:       cloneActiveRun(&run),
				CreatedAt: now,
			}); err != nil {
				return err
			}
			claimed = true
			return pruneSessionEvents(tx, req.SessionID, now)
		})
	})

	return entry, run, claimed, err
}

func (s *Store) ClaimSteering(
	ctx context.Context,
	req agentsession.SteeringClaimRequest,
) ([]agentsession.QueueEntry, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store is required")
	}
	req.SessionID = str.String(req.SessionID).Trim()
	req.RunID = str.String(req.RunID).Trim()
	req.Generation = str.String(req.Generation).Trim()
	if err := base.ValidateSessionID(req.SessionID); err != nil {
		return nil, err
	}
	if req.RunID == "" || req.Generation == "" {
		return nil, errors.New("run id and generation are required")
	}

	var entries []agentsession.QueueEntry
	var messageRecords []messageModel
	err := runSQLiteWriteWithRetry(ctx, func(writeCtx context.Context) error {
		entries = nil
		messageRecords = nil

		return s.db.WithContext(writeCtx).Transaction(func(tx *gorm.DB) error {
			if err := requireSession(tx, req.SessionID); err != nil {
				return err
			}
			var run sessionRunModel
			if err := tx.Where(
				"id = ? AND session_id = ? AND generation = ? AND status = ?",
				req.RunID,
				req.SessionID,
				req.Generation,
				agentsession.RunStatusRunning,
			).First(&run).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil
				}
				return err
			}

			var records []sessionQueueEntryModel
			if err := tx.Where(
				"session_id = ? AND target_run_id = ? AND status = ? AND delivery_mode = ?",
				req.SessionID,
				req.RunID,
				agentsession.QueueStatusPending,
				agentsession.DeliveryModeSteering,
			).Order("sequence ASC").Find(&records).Error; err != nil {
				return err
			}
			if len(records) == 0 {
				return nil
			}

			now := time.Now().UTC()
			messages := make([]morphmsg.Message, 0, len(records))
			for index := range records {
				userMessage, err := morphmsg.NewMessage(morphmsg.RoleUser, records[index].Content)
				if err != nil {
					return err
				}
				messages = append(messages, userMessage)
				records[index].Status = string(agentsession.QueueStatusDelivered)
				records[index].StartedAt = now
				records[index].CompletedAt = now
				records[index].UpdatedAt = now
				if err := tx.Save(&records[index]).Error; err != nil {
					return err
				}
			}

			var err error
			messageRecords, err = s.appendMessagesInTransaction(tx, req.SessionID, messages)
			if err != nil {
				return err
			}
			entries = queueEntryModelsToQueueEntries(records)
			for index := range entries {
				if _, err := appendSessionEvent(tx, agentsession.Event{
					SessionID: req.SessionID,
					Type:      agentsession.EventTypeSteeringSent,
					Queue:     cloneQueueEntry(&entries[index]),
					Run:       cloneActiveRunValue(runModelToActiveRun(run)),
					CreatedAt: now,
				}); err != nil {
					return err
				}
			}

			return pruneSessionEvents(tx, req.SessionID, now)
		})
	})
	if err != nil {
		return nil, err
	}
	s.indexPersistedMessages(ctx, messageRecords)

	return entries, nil
}

func (s *Store) HasPendingSteering(
	ctx context.Context,
	req agentsession.SteeringClaimRequest,
) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("store is required")
	}
	req.SessionID = str.String(req.SessionID).Trim()
	req.RunID = str.String(req.RunID).Trim()
	req.Generation = str.String(req.Generation).Trim()
	if err := base.ValidateSessionID(req.SessionID); err != nil {
		return false, err
	}
	if req.RunID == "" || req.Generation == "" {
		return false, errors.New("run id and generation are required")
	}

	var pending bool
	err := s.db.WithContext(ctx).Raw(
		`SELECT EXISTS (
			SELECT 1
			FROM session_queue_entries
			JOIN session_runs ON session_runs.id = session_queue_entries.target_run_id
			JOIN sessions ON sessions.id = session_queue_entries.session_id
			WHERE session_queue_entries.session_id = ?
				AND session_queue_entries.target_run_id = ?
				AND session_queue_entries.status = ?
				AND session_queue_entries.delivery_mode = ?
				AND session_runs.generation = ?
				AND session_runs.status = ?
				AND sessions.archived = FALSE
			LIMIT 1
		)`,
		req.SessionID,
		req.RunID,
		agentsession.QueueStatusPending,
		agentsession.DeliveryModeSteering,
		req.Generation,
		agentsession.RunStatusRunning,
	).Scan(&pending).Error
	return pending, err
}

func (s *Store) FinishSessionRun(
	ctx context.Context,
	req agentsession.RunFinishRequest,
) (agentsession.ActiveRun, bool, error) {
	if s == nil || s.db == nil {
		return agentsession.ActiveRun{}, false, errors.New("store is required")
	}
	if err := validateRunFinishRequest(req); err != nil {
		return agentsession.ActiveRun{}, false, err
	}

	var run agentsession.ActiveRun
	transitioned := false
	err := runSQLiteWriteWithRetry(ctx, func(writeCtx context.Context) error {
		run = agentsession.ActiveRun{}
		transitioned = false

		return s.db.WithContext(writeCtx).Transaction(func(tx *gorm.DB) error {
			var record sessionRunModel
			err := tx.Where(
				"id = ? AND session_id = ? AND generation = ?",
				req.RunID,
				req.SessionID,
				req.Generation,
			).First(&record).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil
				}
				return err
			}
			if agentsession.RunStatus(record.Status) != agentsession.RunStatusRunning {
				run = runModelToActiveRun(record)
				return nil
			}

			now := time.Now().UTC()
			record.Status = string(req.Status)
			record.CompletedAt = now
			record.UpdatedAt = now
			record.Reason = str.String(req.Reason).Trim()
			record.LastError = str.String(req.LastError).Trim()
			if err := tx.Save(&record).Error; err != nil {
				return err
			}
			queueError := record.LastError
			if queueError == "" {
				queueError = record.Reason
			}
			if err := finishQueueEntry(tx, record, req.Status, now, queueError); err != nil {
				return err
			}
			if err := appendFinishedQueueEvent(tx, record, now); err != nil {
				return err
			}
			if err := resolvePendingSteering(tx, record, now); err != nil {
				return err
			}

			run = runModelToActiveRun(record)
			if _, err := appendSessionEvent(tx, agentsession.Event{
				SessionID: req.SessionID,
				Type:      runStatusToEventType(req.Status),
				Run:       cloneActiveRun(&run),
				CreatedAt: now,
			}); err != nil {
				return err
			}
			transitioned = true
			return pruneSessionEvents(tx, req.SessionID, now)
		})
	})

	return run, transitioned, err
}

func (s *Store) ReconcileActiveRuns(
	ctx context.Context,
	currentGeneration string,
) (agentsession.ReconcileResult, error) {
	if s == nil || s.db == nil {
		return agentsession.ReconcileResult{}, errors.New("store is required")
	}
	currentGeneration = str.String(currentGeneration).Trim()
	if currentGeneration == "" {
		return agentsession.ReconcileResult{}, errors.New("generation is required")
	}

	result := agentsession.ReconcileResult{}
	err := runSQLiteWriteWithRetry(ctx, func(writeCtx context.Context) error {
		result = agentsession.ReconcileResult{}

		return s.db.WithContext(writeCtx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "id"}},
				DoUpdates: clause.AssignmentColumns([]string{"generation", "updated_at"}),
			}).Create(&sessionRunnerStateModel{
				ID:         sessionRunnerStateID,
				Generation: currentGeneration,
				UpdatedAt:  time.Now().UTC(),
			}).Error; err != nil {
				return err
			}

			var records []sessionRunModel
			if err := tx.Where(
				"status = ? AND generation <> ?",
				agentsession.RunStatusRunning,
				currentGeneration,
			).Find(&records).Error; err != nil {
				return err
			}
			seen := make(map[string]struct{})
			for index := range records {
				now := time.Now().UTC()
				records[index].Status = string(agentsession.RunStatusInterrupted)
				records[index].Reason = "daemon_restart"
				records[index].CompletedAt = now
				records[index].UpdatedAt = now
				if err := tx.Save(&records[index]).Error; err != nil {
					return err
				}
				if err := finishQueueEntry(
					tx,
					records[index],
					agentsession.RunStatusInterrupted,
					now,
					"daemon_restart",
				); err != nil {
					return err
				}
				if err := appendFinishedQueueEvent(tx, records[index], now); err != nil {
					return err
				}
				if err := resolvePendingSteering(tx, records[index], now); err != nil {
					return err
				}
				run := runModelToActiveRun(records[index])
				if _, err := appendSessionEvent(tx, agentsession.Event{
					SessionID: run.SessionID,
					Type:      agentsession.EventTypeRunInterrupted,
					Run:       cloneActiveRun(&run),
					CreatedAt: now,
				}); err != nil {
					return err
				}
				if err := pruneSessionEvents(tx, run.SessionID, now); err != nil {
					return err
				}
				seen[run.SessionID] = struct{}{}
				result.Runs = append(result.Runs, run)
				result.RunCount++
			}
			for sessionID := range seen {
				result.SessionIDs = append(result.SessionIDs, sessionID)
			}
			slices.Sort(result.SessionIDs)
			return nil
		})
	})

	return result, err
}

func (s *Store) ListRunnableSessions(ctx context.Context) ([]string, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store is required")
	}
	var sessionIDs []string
	err := s.db.WithContext(ctx).Model(&sessionQueueEntryModel{}).
		Distinct("session_queue_entries.session_id").
		Joins("JOIN sessions ON sessions.id = session_queue_entries.session_id AND sessions.archived = ?", false).
		Joins(
			"LEFT JOIN session_runs ON session_runs.session_id = session_queue_entries.session_id AND session_runs.status = ?",
			agentsession.RunStatusRunning,
		).
		Where(
			"session_queue_entries.status = ? AND session_queue_entries.delivery_mode = ? AND session_runs.id IS NULL",
			agentsession.QueueStatusPending,
			agentsession.DeliveryModeFollowUp,
		).
		Order("session_queue_entries.session_id ASC").
		Pluck("session_queue_entries.session_id", &sessionIDs).Error

	return sessionIDs, err
}

func normalizeSubmitRequest(req agentsession.SubmitRequest) (agentsession.SubmitRequest, error) {
	req.ID = str.String(req.ID).Trim()
	req.SessionID = str.String(req.SessionID).Trim()
	req.Content = str.String(req.Content).Trim()
	req.Instruct = str.String(req.Instruct).Trim()
	req.ClientSubmissionID = str.String(req.ClientSubmissionID).Trim()
	req.DeliveryMode = agentsession.DeliveryMode(str.String(req.DeliveryMode).Normalized())
	req.SteeringFallback = agentsession.SteeringFallback(str.String(req.SteeringFallback).Normalized())
	req.Provenance.ActorKind = str.String(req.Provenance.ActorKind).Trim()
	req.Provenance.ActorID = str.String(req.Provenance.ActorID).Trim()
	req.Provenance.SurfaceKind = str.String(req.Provenance.SurfaceKind).Trim()
	req.Provenance.Surface = str.String(req.Provenance.Surface).Trim()
	req.Provenance.Profile = str.String(req.Provenance.Profile).Trim()
	if req.ID == "" {
		return req, errors.New("queue entry id is required")
	}
	if err := base.ValidateSessionID(req.SessionID); err != nil {
		return req, err
	}
	if req.Content == "" {
		return req, errors.New("message is required")
	}
	if req.ClientSubmissionID == "" {
		return req, errors.New("client submission id is required")
	}
	if req.DeliveryMode == "" {
		req.DeliveryMode = agentsession.DeliveryModeFollowUp
	}
	if req.SteeringFallback == "" {
		req.SteeringFallback = agentsession.SteeringFallbackFollowUp
	}
	if req.DeliveryMode != agentsession.DeliveryModeFollowUp &&
		req.DeliveryMode != agentsession.DeliveryModeSteering {
		return req, errors.New("delivery mode is invalid")
	}
	if req.SteeringFallback != agentsession.SteeringFallbackReject &&
		req.SteeringFallback != agentsession.SteeringFallbackFollowUp {
		return req, errors.New("steering fallback is invalid")
	}

	return req, nil
}

func normalizeSessionID(sessionID string) (string, error) {
	sessionID = str.String(sessionID).Trim()
	if err := base.ValidateSessionID(sessionID); err != nil {
		return "", err
	}
	return sessionID, nil
}

func requireSession(db *gorm.DB, sessionID string) error {
	var count int64
	if err := db.Model(&sessionModel{}).Where("id = ? AND archived = ?", sessionID, false).
		Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return errors.New("session not found")
	}
	return nil
}

func ensureExecutionState(tx *gorm.DB, sessionID string) error {
	now := time.Now().UTC()
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&sessionExecutionStateModel{
		SessionID: sessionID,
		CreatedAt: now,
		UpdatedAt: now,
	}).Error
}

func nextQueueSequence(tx *gorm.DB, sessionID string) (int64, error) {
	if err := ensureExecutionState(tx, sessionID); err != nil {
		return 0, err
	}
	var state sessionExecutionStateModel
	if err := tx.Raw(
		`UPDATE session_execution_state
		 SET next_queue_sequence = next_queue_sequence + 1, updated_at = ?
		 WHERE session_id = ?
		 RETURNING *`,
		time.Now().UTC(),
		sessionID,
	).Scan(&state).Error; err != nil {
		return 0, err
	}
	return state.NextQueueSequence, nil
}

func appendSessionEvent(tx *gorm.DB, event agentsession.Event) (agentsession.Event, error) {
	if err := ensureExecutionState(tx, event.SessionID); err != nil {
		return agentsession.Event{}, err
	}
	var state sessionExecutionStateModel
	if err := tx.Raw(
		`UPDATE session_execution_state
		 SET cursor = cursor + 1, updated_at = ?
		 WHERE session_id = ?
		 RETURNING *`,
		event.CreatedAt,
		event.SessionID,
	).Scan(&state).Error; err != nil {
		return agentsession.Event{}, err
	}
	event.Cursor = state.Cursor
	queueJSON, err := json.Marshal(event.Queue)
	if err != nil {
		return agentsession.Event{}, err
	}
	runJSON, err := json.Marshal(event.Run)
	if err != nil {
		return agentsession.Event{}, err
	}
	record := sessionEventModel{
		SessionID: event.SessionID,
		Cursor:    event.Cursor,
		Type:      string(event.Type),
		QueueJSON: string(queueJSON),
		RunJSON:   string(runJSON),
		CreatedAt: event.CreatedAt,
	}
	if err := tx.Create(&record).Error; err != nil {
		return agentsession.Event{}, err
	}
	if state.RetainedFloor == 0 {
		if err := tx.Model(&sessionExecutionStateModel{}).
			Where("session_id = ?", event.SessionID).
			Update("retained_floor", event.Cursor).Error; err != nil {
			return agentsession.Event{}, err
		}
	}

	return event, nil
}

func pruneSessionEvents(tx *gorm.DB, sessionID string, now time.Time) error {
	var state sessionExecutionStateModel
	if err := tx.Where("session_id = ?", sessionID).First(&state).Error; err != nil {
		return err
	}
	maxDeletedCursor := state.Cursor - sessionEventRetentionCount
	if maxDeletedCursor > 0 {
		if err := tx.Where(
			"session_id = ? AND cursor <= ? AND created_at < ?",
			sessionID,
			maxDeletedCursor,
			now.Add(-sessionEventRetentionAge),
		).Delete(&sessionEventModel{}).Error; err != nil {
			return err
		}
	}

	var floor int64
	if err := tx.Model(&sessionEventModel{}).
		Where("session_id = ?", sessionID).
		Select("COALESCE(MIN(cursor), 0)").
		Scan(&floor).Error; err != nil {
		return err
	}
	return tx.Model(&sessionExecutionStateModel{}).
		Where("session_id = ?", sessionID).
		Update("retained_floor", floor).Error
}

func queueEntryToQueueEntryModel(entry agentsession.QueueEntry) sessionQueueEntryModel {
	return sessionQueueEntryModel{
		ID:                    entry.ID,
		SessionID:             entry.SessionID,
		ClientSubmissionID:    entry.ClientSubmissionID,
		Content:               entry.Content,
		Instruct:              entry.Instruct,
		Stream:                cloneInboxBool(entry.Stream),
		TargetRunID:           entry.TargetRunID,
		RequestedDeliveryMode: string(entry.RequestedDeliveryMode),
		DeliveryMode:          string(entry.DeliveryMode),
		SteeringFallback:      string(entry.SteeringFallback),
		Status:                string(entry.Status),
		ActorKind:             entry.Provenance.ActorKind,
		ActorID:               entry.Provenance.ActorID,
		SurfaceKind:           entry.Provenance.SurfaceKind,
		Surface:               entry.Provenance.Surface,
		Profile:               entry.Provenance.Profile,
		Sequence:              entry.Sequence,
		Priority:              entry.Priority,
		CreatedAt:             entry.CreatedAt,
		UpdatedAt:             entry.UpdatedAt,
		StartedAt:             entry.StartedAt,
		CompletedAt:           entry.CompletedAt,
		LastError:             entry.LastError,
	}
}

func queueEntryModelToQueueEntry(record sessionQueueEntryModel) agentsession.QueueEntry {
	requestedDeliveryMode := agentsession.DeliveryMode(record.RequestedDeliveryMode)
	if requestedDeliveryMode == "" {
		requestedDeliveryMode = agentsession.DeliveryMode(record.DeliveryMode)
	}
	return agentsession.QueueEntry{
		ID:                    record.ID,
		SessionID:             record.SessionID,
		Content:               record.Content,
		Instruct:              record.Instruct,
		Stream:                cloneInboxBool(record.Stream),
		ClientSubmissionID:    record.ClientSubmissionID,
		TargetRunID:           record.TargetRunID,
		RequestedDeliveryMode: requestedDeliveryMode,
		DeliveryMode:          agentsession.DeliveryMode(record.DeliveryMode),
		SteeringFallback:      agentsession.SteeringFallback(record.SteeringFallback),
		Status:                agentsession.QueueStatus(record.Status),
		Provenance: agentsession.Provenance{
			ActorKind:   record.ActorKind,
			ActorID:     record.ActorID,
			SurfaceKind: record.SurfaceKind,
			Surface:     record.Surface,
			Profile:     record.Profile,
		},
		Sequence:    record.Sequence,
		Priority:    record.Priority,
		CreatedAt:   record.CreatedAt,
		UpdatedAt:   record.UpdatedAt,
		StartedAt:   record.StartedAt,
		CompletedAt: record.CompletedAt,
		LastError:   record.LastError,
	}
}

func queueEntryModelsToQueueEntries(records []sessionQueueEntryModel) []agentsession.QueueEntry {
	entries := make([]agentsession.QueueEntry, len(records))
	for index, record := range records {
		entries[index] = queueEntryModelToQueueEntry(record)
	}
	return entries
}

func runModelToActiveRun(record sessionRunModel) agentsession.ActiveRun {
	return agentsession.ActiveRun{
		ID:           record.ID,
		SessionID:    record.SessionID,
		QueueEntryID: record.QueueEntryID,
		Generation:   record.Generation,
		Status:       agentsession.RunStatus(record.Status),
		StartedAt:    record.StartedAt,
		CompletedAt:  record.CompletedAt,
		UpdatedAt:    record.UpdatedAt,
		Reason:       record.Reason,
		LastError:    record.LastError,
		Reasoning: agentsession.ReasoningSnapshot{
			Provider: record.ReasoningProvider,
			API:      record.ReasoningAPI,
			Model:    record.ReasoningModel,
			Effort:   agentsession.ReasoningEffort(record.ReasoningEffort),
			Summary:  record.ReasoningSummary,
		},
	}
}

func reasoningOverrideValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func isSameSubmission(entry agentsession.QueueEntry, req agentsession.SubmitRequest) bool {
	return entry.SessionID == req.SessionID &&
		entry.Content == req.Content &&
		entry.Instruct == req.Instruct &&
		equalInboxBool(entry.Stream, req.Stream) &&
		entry.ClientSubmissionID == req.ClientSubmissionID &&
		entry.RequestedDeliveryMode == req.DeliveryMode &&
		entry.SteeringFallback == req.SteeringFallback
}

func cloneInboxBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func equalInboxBool(left *bool, right *bool) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func eventModelToSessionEvent(record sessionEventModel) (agentsession.Event, error) {
	event := agentsession.Event{
		SessionID: record.SessionID,
		Type:      agentsession.EventType(record.Type),
		Cursor:    record.Cursor,
		CreatedAt: record.CreatedAt,
	}
	if value := strings.TrimSpace(record.QueueJSON); value != "" && value != "null" {
		var queue agentsession.QueueEntry
		if err := json.Unmarshal([]byte(value), &queue); err != nil {
			return agentsession.Event{}, fmt.Errorf("decode session queue event: %w", err)
		}
		event.Queue = &queue
	}
	if value := strings.TrimSpace(record.RunJSON); value != "" && value != "null" {
		var run agentsession.ActiveRun
		if err := json.Unmarshal([]byte(value), &run); err != nil {
			return agentsession.Event{}, fmt.Errorf("decode session run event: %w", err)
		}
		event.Run = &run
	}
	return event, nil
}

func validateRunFinishRequest(req agentsession.RunFinishRequest) error {
	if err := base.ValidateSessionID(str.String(req.SessionID).Trim()); err != nil {
		return err
	}
	if str.String(req.RunID).Trim() == "" || str.String(req.Generation).Trim() == "" {
		return errors.New("run id and generation are required")
	}
	switch req.Status {
	case agentsession.RunStatusCompleted,
		agentsession.RunStatusInterrupted,
		agentsession.RunStatusFailed,
		agentsession.RunStatusCancelled:
		return nil
	default:
		return errors.New("terminal run status is required")
	}
}

func finishQueueEntry(
	tx *gorm.DB,
	run sessionRunModel,
	status agentsession.RunStatus,
	now time.Time,
	lastError string,
) error {
	queueStatus := agentsession.QueueStatusCompleted
	switch status {
	case agentsession.RunStatusFailed:
		queueStatus = agentsession.QueueStatusFailed
	case agentsession.RunStatusInterrupted:
		queueStatus = agentsession.QueueStatusInterrupted
	case agentsession.RunStatusCancelled:
		queueStatus = agentsession.QueueStatusCancelled
	}
	return tx.Model(&sessionQueueEntryModel{}).
		Where("id = ? AND session_id = ? AND status = ?", run.QueueEntryID, run.SessionID, agentsession.QueueStatusActive).
		Updates(map[string]any{
			"status":       queueStatus,
			"completed_at": now,
			"updated_at":   now,
			"last_error":   lastError,
		}).Error
}

func appendFinishedQueueEvent(tx *gorm.DB, run sessionRunModel, now time.Time) error {
	var record sessionQueueEntryModel
	if err := tx.Where("id = ? AND session_id = ?", run.QueueEntryID, run.SessionID).
		First(&record).Error; err != nil {
		return err
	}
	entry := queueEntryModelToQueueEntry(record)
	eventType := agentsession.EventTypeQueueUpdated
	if entry.Status == agentsession.QueueStatusCancelled {
		eventType = agentsession.EventTypeQueueCancelled
	}
	_, err := appendSessionEvent(tx, agentsession.Event{
		SessionID: run.SessionID,
		Type:      eventType,
		Queue:     cloneQueueEntry(&entry),
		CreatedAt: now,
	})
	return err
}

func resolvePendingSteering(tx *gorm.DB, run sessionRunModel, now time.Time) error {
	var records []sessionQueueEntryModel
	if err := tx.Where(
		"session_id = ? AND target_run_id = ? AND status = ? AND delivery_mode = ?",
		run.SessionID,
		run.ID,
		agentsession.QueueStatusPending,
		agentsession.DeliveryModeSteering,
	).Order("sequence ASC").Find(&records).Error; err != nil {
		return err
	}
	for index := range records {
		eventType := agentsession.EventTypeQueueUpdated
		if agentsession.SteeringFallback(records[index].SteeringFallback) ==
			agentsession.SteeringFallbackFollowUp {
			records[index].DeliveryMode = string(agentsession.DeliveryModeFollowUp)
			records[index].TargetRunID = ""
		} else {
			records[index].Status = string(agentsession.QueueStatusCancelled)
			records[index].CompletedAt = now
			records[index].LastError = "target run completed before steering delivery"
			eventType = agentsession.EventTypeQueueCancelled
		}
		records[index].UpdatedAt = now
		if err := tx.Save(&records[index]).Error; err != nil {
			return err
		}
		entry := queueEntryModelToQueueEntry(records[index])
		if _, err := appendSessionEvent(tx, agentsession.Event{
			SessionID: run.SessionID,
			Type:      eventType,
			Queue:     cloneQueueEntry(&entry),
			CreatedAt: now,
		}); err != nil {
			return err
		}
	}
	return nil
}

func runStatusToEventType(status agentsession.RunStatus) agentsession.EventType {
	switch status {
	case agentsession.RunStatusCompleted:
		return agentsession.EventTypeRunCompleted
	case agentsession.RunStatusInterrupted:
		return agentsession.EventTypeRunInterrupted
	case agentsession.RunStatusCancelled:
		return agentsession.EventTypeRunCancelled
	default:
		return agentsession.EventTypeRunFailed
	}
}

func cloneQueueEntry(entry *agentsession.QueueEntry) *agentsession.QueueEntry {
	if entry == nil {
		return nil
	}
	value := *entry
	return &value
}

func cloneActiveRun(run *agentsession.ActiveRun) *agentsession.ActiveRun {
	if run == nil {
		return nil
	}
	value := *run
	return &value
}

func cloneActiveRunValue(run agentsession.ActiveRun) *agentsession.ActiveRun {
	return cloneActiveRun(&run)
}
