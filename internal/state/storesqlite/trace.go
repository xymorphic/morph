package storesqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	base "github.com/xymorphic/morph/internal/state/core"
	morphtrace "github.com/xymorphic/morph/internal/trace"
	"github.com/xymorphic/morph/pkg/str"
)

type traceEventModel struct {
	ID          uint      `gorm:"primaryKey"`
	SessionID   string    `gorm:"uniqueIndex:idx_trace_session_sequence,priority:1;not null"`
	Sequence    int       `gorm:"uniqueIndex:idx_trace_session_sequence,priority:2;not null"`
	Type        string    `gorm:"index"`
	Timestamp   time.Time `gorm:"index"`
	PayloadJSON string    `gorm:"type:text"`
}

func (traceEventModel) TableName() string {
	return "trace_events"
}

func (s *Store) AppendTraceEvent(ctx context.Context, event base.TraceEvent) (base.TraceEvent, error) {
	if s == nil || s.db == nil {
		return base.TraceEvent{}, errors.New("store is required")
	}

	sessionIDValue := str.String(event.SessionID)
	event.SessionID = sessionIDValue.Trim()
	if err := base.ValidateSessionID(event.SessionID); err != nil {
		return base.TraceEvent{}, err
	}
	trimmedValueValue := str.String(event.Type)
	event.Type = trimmedValueValue.Trim()
	if event.Type == "" {
		return base.TraceEvent{}, errors.New("trace event type is required")
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	} else {
		event.Timestamp = event.Timestamp.UTC()
	}

	var record traceEventModel
	err := runSQLiteWriteWithRetry(ctx, func(writeCtx context.Context) error {
		return s.db.WithContext(writeCtx).Transaction(func(tx *gorm.DB) error {
			var last traceEventModel
			if err := tx.Where("session_id = ?", event.SessionID).
				Order("sequence DESC").
				Limit(1).
				Find(&last).Error; err != nil {
				return err
			}

			record = traceEventModel{
				SessionID:   event.SessionID,
				Sequence:    last.Sequence + 1,
				Type:        event.Type,
				Timestamp:   event.Timestamp,
				PayloadJSON: toJSONString(event.Payload),
			}
			if err := tx.Create(&record).Error; err != nil {
				return err
			}
			return nil
		})
	})
	if err != nil {
		return base.TraceEvent{}, err
	}

	return traceModelToEvent(record)
}

func (s *Store) ListTraceEvents(ctx context.Context, query base.TraceQuery) (base.TraceResult, error) {
	if s == nil || s.db == nil {
		return base.TraceResult{}, errors.New("store is required")
	}

	db := s.db.WithContext(ctx).Model(&traceEventModel{})
	sessionIDValue2 := str.String(query.SessionID)
	if sessionID := sessionIDValue2.Trim(); sessionID != "" {
		db = db.Where("session_id = ?", sessionID)
	}
	if types := base.NormalizeTraceTypes(query.Types); len(types) > 0 {
		db = db.Where("type IN ?", types)
	}
	if query.MinSequence > 0 {
		db = db.Where("sequence >= ?", query.MinSequence)
	}
	if query.Desc {
		db = db.Order("session_id DESC").Order("sequence DESC").Order("id DESC")
	} else {
		db = db.Order("session_id ASC").Order("sequence ASC").Order("id ASC")
	}
	if query.Offset > 0 {
		db = db.Offset(query.Offset)
	}
	if query.Limit > 0 {
		db = db.Limit(query.Limit)
	}

	var records []traceEventModel
	if err := db.Find(&records).Error; err != nil {
		return base.TraceResult{}, err
	}

	events := make([]base.TraceEvent, 0, len(records))
	for _, record := range records {
		event, err := traceModelToEvent(record)
		if err != nil {
			return base.TraceResult{}, err
		}
		events = append(events, event)
	}

	return base.TraceResult{Events: events}, nil
}

func (s *Store) PruneTraceEvents(ctx context.Context, sessionID string, maxEvents int) error {
	if s == nil || s.db == nil {
		return errors.New("store is required")
	}
	if maxEvents < 0 {
		return errors.New("max trace events must be greater than or equal to zero")
	}

	sessionIDValue3 := str.String(sessionID)
	sessionID = sessionIDValue3.Trim()
	if err := base.ValidateSessionID(sessionID); err != nil {
		return err
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&traceEventModel{}).Where("session_id = ?", sessionID).Count(&count).Error; err != nil {
			return err
		}
		if count <= int64(maxEvents) {
			return nil
		}

		deleteCount := count - int64(maxEvents)
		subquery := tx.Model(&traceEventModel{}).
			Select("id").
			Where("session_id = ?", sessionID).
			Order("sequence ASC").
			Limit(int(deleteCount))
		if err := tx.Where("id IN (?)", subquery).Delete(&traceEventModel{}).Error; err != nil {
			return fmt.Errorf("failed to prune trace events: %w", err)
		}
		return nil
	})
}

func traceModelToEvent(record traceEventModel) (base.TraceEvent, error) {
	event := base.TraceEvent{
		ID:        record.ID,
		SessionID: record.SessionID,
		Sequence:  record.Sequence,
		Type:      record.Type,
		Timestamp: record.Timestamp.UTC(),
	}
	if payload, ok := morphtrace.DecodePayloadJSON(record.Type, json.RawMessage(record.PayloadJSON)); ok {
		event.Payload = payload
		return event, nil
	}

	if err := fromJSONString(record.PayloadJSON, &event.Payload); err != nil {
		return base.TraceEvent{}, err
	}

	return event, nil
}
