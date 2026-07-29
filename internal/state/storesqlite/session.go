package storesqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"gorm.io/gorm"

	base "github.com/wandxy/morph/internal/state/core"
	"github.com/wandxy/morph/internal/state/search"
	morphmsg "github.com/wandxy/morph/pkg/agent/message"
	"github.com/wandxy/morph/pkg/str"
)

const currentSessionStateKey = "current_session"
const sessionMessageSearchTable = "session_message_search"

// Session aliases base.Session at this package boundary.
type Session = base.Session

// MessageQueryOptions aliases base.MessageQueryOptions at this package boundary.
type MessageQueryOptions = base.MessageQueryOptions

// SearchMessageOptions aliases base.SearchMessageOptions at this package boundary.
type SearchMessageOptions = base.SearchMessageOptions

// SearchMessageResult aliases base.SearchMessageResult at this package boundary.
type SearchMessageResult = base.SearchMessageResult

// SessionSummary aliases base.SessionSummary at this package boundary.
type SessionSummary = base.SessionSummary

// SessionCompaction aliases base.SessionCompaction at this package boundary.
type SessionCompaction = base.SessionCompaction

// SessionCompactionStatus records whether session history has been compacted.
type SessionCompactionStatus = base.SessionCompactionStatus

// MessageRecord aliases base.MessageRecord at this package boundary.
type MessageRecord = base.MessageRecord

// CheckpointPatch aliases base.CheckpointPatch at this package boundary.
type CheckpointPatch = base.CheckpointPatch

type SessionPatch = base.SessionPatch

// sessionModel describes durable session-level state and compaction progress.
type sessionModel struct {
	ID                           string `gorm:"primaryKey"`
	Title                        string `gorm:"type:text"`
	TitleSource                  string
	OriginSource                 string
	OriginAccountID              string
	OriginConversationID         string
	OriginThreadID               string
	CreatedAt                    time.Time
	UpdatedAt                    time.Time
	Archived                     bool
	ArchivedAt                   time.Time `gorm:"index"`
	ExpiresAt                    time.Time `gorm:"index"`
	LastPromptTokens             int
	CompactionStatus             string
	CompactionRequestedAt        time.Time
	CompactionStartedAt          time.Time
	CompactionCompletedAt        time.Time
	CompactionFailedAt           time.Time
	CompactionLastError          string
	CompactionTargetMessageCount int
	CompactionTargetOffset       int
	EpisodicCheckpointOffset     int
	ReflectionCheckpointOffset   int
	ReasoningEffortOverride      *string `gorm:"type:text"`
}

// TableName returns the SQLite table used for active sessions.
func (sessionModel) TableName() string {
	return "sessions"
}

// stateModel describes small named session state values such as the current session.
type stateModel struct {
	Key       string `gorm:"primaryKey"`
	Value     string `gorm:"not null"`
	UpdatedAt time.Time
	CreatedAt time.Time
}

// TableName returns the SQLite table used for session state entries.
func (stateModel) TableName() string {
	return "session_state"
}

// summaryModel describes the latest generated summary for a session.
type summaryModel struct {
	SessionID          string `gorm:"primaryKey"`
	SourceEndOffset    int
	SourceMessageCount int
	UpdatedAt          time.Time
	SessionSummary     string `gorm:"type:text"`
	CurrentTask        string `gorm:"type:text"`
	Discoveries        string `gorm:"type:text"`
	OpenQuestions      string `gorm:"type:text"`
	NextActions        string `gorm:"type:text"`
}

// TableName returns the SQLite table used for session summaries.
func (summaryModel) TableName() string {
	return "session_summaries"
}

// messageModel describes active session messages in append order.
type messageModel struct {
	ID              uint   `gorm:"primaryKey"`
	SessionID       string `gorm:"uniqueIndex:idx_session_messages_session_sequence,priority:1"`
	Sequence        int    `gorm:"uniqueIndex:idx_session_messages_session_sequence,priority:2;not null"`
	Role            string
	Name            string
	Content         string
	SemanticContent string `gorm:"type:text"`
	ToolCalls       string `gorm:"type:text"`
	ToolCallID      string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type gatewayBindingModel struct {
	Key       string `gorm:"primaryKey"`
	SessionID string `gorm:"index;not null"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (gatewayBindingModel) TableName() string {
	return "gateway_bindings"
}

type gatewayPairingRequestModel struct {
	Source      string `gorm:"primaryKey"`
	SenderID    string `gorm:"primaryKey"`
	DisplayName string
	Metadata    string    `gorm:"type:text"`
	CreatedAt   time.Time `gorm:"autoCreateTime:false"`
	LastSeenAt  time.Time `gorm:"autoUpdateTime:false"`
	ExpiresAt   time.Time `gorm:"autoUpdateTime:false"`
}

func (gatewayPairingRequestModel) TableName() string {
	return "gateway_pairing_requests"
}

type gatewayPairedSenderModel struct {
	Source      string `gorm:"primaryKey"`
	SenderID    string `gorm:"primaryKey"`
	DisplayName string
	Metadata    string    `gorm:"type:text"`
	CreatedAt   time.Time `gorm:"autoCreateTime:false"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime:false"`
}

func (gatewayPairedSenderModel) TableName() string {
	return "gateway_paired_senders"
}

// messageModels is a typed slice for active message conversion helpers.
type messageModels []messageModel

// TableName returns the SQLite table used for active session messages.
func (messageModel) TableName() string {
	return "session_messages"
}

// searchSessionResultRow is the intermediate shape used to map ranked SQL rows to public search results.
type searchSessionResultRow struct {
	ID              uint
	SessionID       string
	Sequence        int
	Role            string
	Name            string
	Content         string
	ToolCalls       string
	ToolCallID      string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	MatchedText     string
	MatchedToolName string
	Score           float64
	BestScore       float64
	MatchCount      int
	LastMatchedAt   string
}

// Save upserts session metadata without modifying stored messages.
func (s *Store) Save(ctx context.Context, session Session) error {
	if s == nil || s.db == nil {
		return errors.New("store is required")
	}

	iDValue := str.String(session.ID)
	session.ID = iDValue.Trim()
	if err := base.ValidateSessionID(session.ID); err != nil {
		return err
	}

	var existing sessionModel

	if err := s.db.WithContext(ctx).First(&existing, "id = ?", session.ID).Error; err == nil {
		session.CreatedAt = existing.CreatedAt
		if existing.ReasoningEffortOverride != nil {
			session.ReasoningEffortOverride = *existing.ReasoningEffortOverride
		} else {
			session.ReasoningEffortOverride = ""
		}
		if !session.Archived && session.ArchivedAt.IsZero() && session.ExpiresAt.IsZero() {
			session.Archived = existing.Archived
			session.ArchivedAt = existing.ArchivedAt
			session.ExpiresAt = existing.ExpiresAt
		}

		if session.Compaction == (base.SessionCompaction{}) {
			session.Compaction = base.SessionCompaction{
				CompletedAt:        existing.CompactionCompletedAt,
				FailedAt:           existing.CompactionFailedAt,
				LastError:          existing.CompactionLastError,
				RequestedAt:        existing.CompactionRequestedAt,
				StartedAt:          existing.CompactionStartedAt,
				Status:             base.SessionCompactionStatus(existing.CompactionStatus),
				TargetMessageCount: existing.CompactionTargetMessageCount,
				TargetOffset:       existing.CompactionTargetOffset,
			}
		}
		if session.EpisodicCheckpointOffset == 0 {
			session.EpisodicCheckpointOffset = existing.EpisodicCheckpointOffset
		}
		if session.ReflectionCheckpointOffset == 0 {
			session.ReflectionCheckpointOffset = existing.ReflectionCheckpointOffset
		}
		titleValue := str.String(session.Title)
		if titleValue.Trim() == "" {
			session.Title = existing.Title
			session.TitleSource = existing.TitleSource
		}
		if session.Origin == (base.SessionOrigin{}) {
			session.Origin = base.SessionOrigin{
				Source:         existing.OriginSource,
				AccountID:      existing.OriginAccountID,
				ConversationID: existing.OriginConversationID,
				ThreadID:       existing.OriginThreadID,
			}
		}

		session.UpdatedAt = time.Now().UTC()
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	session.Title, session.TitleSource = base.NormalizeSessionTitleMetadata(session.Title, session.TitleSource)

	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now().UTC()
	} else {
		session.CreatedAt = session.CreatedAt.UTC()
	}

	if session.UpdatedAt.IsZero() {
		session.UpdatedAt = time.Now().UTC()
	} else {
		session.UpdatedAt = session.UpdatedAt.UTC()
	}
	if !session.ArchivedAt.IsZero() {
		session.ArchivedAt = session.ArchivedAt.UTC()
	}
	if !session.ExpiresAt.IsZero() {
		session.ExpiresAt = session.ExpiresAt.UTC()
	}
	accountIDValue := str.String(session.Origin.AccountID)
	conversationIDValue := str.String(session.Origin.ConversationID)
	sourceValue := str.String(session.Origin.Source)
	threadIDValue := str.String(session.Origin.ThreadID)
	record := sessionModel{
		Archived:                     session.Archived,
		ArchivedAt:                   session.ArchivedAt,
		CreatedAt:                    session.CreatedAt,
		CompactionCompletedAt:        session.Compaction.CompletedAt,
		CompactionFailedAt:           session.Compaction.FailedAt,
		CompactionLastError:          session.Compaction.LastError,
		CompactionRequestedAt:        session.Compaction.RequestedAt,
		CompactionStartedAt:          session.Compaction.StartedAt,
		CompactionStatus:             string(session.Compaction.Status),
		CompactionTargetMessageCount: session.Compaction.TargetMessageCount,
		CompactionTargetOffset:       session.Compaction.TargetOffset,
		EpisodicCheckpointOffset:     session.EpisodicCheckpointOffset,
		ExpiresAt:                    session.ExpiresAt,
		ID:                           session.ID,
		LastPromptTokens:             session.LastPromptTokens,
		OriginAccountID:              accountIDValue.Trim(),
		OriginConversationID:         conversationIDValue.Trim(),
		OriginSource:                 sourceValue.Trim(),
		OriginThreadID:               threadIDValue.Trim(),
		ReflectionCheckpointOffset:   session.ReflectionCheckpointOffset,
		ReasoningEffortOverride:      optionalString(session.ReasoningEffortOverride),
		Title:                        session.Title,
		TitleSource:                  session.TitleSource,
		UpdatedAt:                    session.UpdatedAt,
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.Save(&record).Error
	})
}

func (s *Store) Patch(ctx context.Context, id string, patch SessionPatch) (Session, error) {
	if s == nil || s.db == nil {
		return Session{}, errors.New("store is required")
	}

	idValue := str.String(id)
	id = idValue.Trim()
	if err := base.ValidateSessionID(id); err != nil {
		return Session{}, err
	}

	var result Session
	err := runSQLiteWriteWithRetry(ctx, func(writeCtx context.Context) error {
		return s.db.WithContext(writeCtx).Transaction(func(tx *gorm.DB) error {
			var record sessionModel
			if err := tx.First(&record, "id = ?", id).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return errors.New("session not found")
				}
				return err
			}

			if patch.ReasoningEffortOverride != nil {
				record.ReasoningEffortOverride = optionalString(*patch.ReasoningEffortOverride)
				record.UpdatedAt = time.Now().UTC()
				if err := tx.Model(&sessionModel{}).
					Where("id = ?", id).
					Updates(map[string]any{
						"reasoning_effort_override": record.ReasoningEffortOverride,
						"updated_at":                record.UpdatedAt,
					}).Error; err != nil {
					return err
				}
			}

			converted, err := sessionModelToSession(record)
			if err != nil {
				return err
			}
			result = converted
			return nil
		})
	})
	if err != nil {
		return Session{}, err
	}

	return result, nil
}

func (s *Store) UpdateCheckpoints(ctx context.Context, id string, patch CheckpointPatch) error {
	if s == nil || s.db == nil {
		return errors.New("store is required")
	}

	idValue := str.String(id)
	id = idValue.Trim()
	if err := base.ValidateSessionID(id); err != nil {
		return err
	}
	if patch.EpisodicOffset != nil && *patch.EpisodicOffset < 0 {
		return errors.New("episodic checkpoint offset must be greater than or equal to zero")
	}
	if patch.ReflectionOffset != nil && *patch.ReflectionOffset < 0 {
		return errors.New("reflection checkpoint offset must be greater than or equal to zero")
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		changed := false
		if patch.EpisodicOffset != nil {
			result := tx.
				Model(&sessionModel{}).
				Where("id = ? AND COALESCE(episodic_checkpoint_offset, 0) < ?", id, *patch.EpisodicOffset).
				Update("episodic_checkpoint_offset", *patch.EpisodicOffset)
			if result.Error != nil {
				return result.Error
			}
			changed = changed || result.RowsAffected > 0
		}
		if patch.ReflectionOffset != nil {
			result := tx.
				Model(&sessionModel{}).
				Where("id = ? AND COALESCE(reflection_checkpoint_offset, 0) < ?", id, *patch.ReflectionOffset).
				Update("reflection_checkpoint_offset", *patch.ReflectionOffset)
			if result.Error != nil {
				return result.Error
			}
			changed = changed || result.RowsAffected > 0
		}
		if changed {
			return nil
		}

		var count int64
		if err := tx.Model(&sessionModel{}).Where("id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return errors.New("session not found")
		}

		return nil
	})
}

// Get loads one session by ID.
func (s *Store) Get(ctx context.Context, id string, opts base.SessionGetOptions) (Session, bool, error) {
	if s == nil || s.db == nil {
		return Session{}, false, errors.New("store is required")
	}

	idValue2 := str.String(id)
	id = idValue2.Trim()
	if id == "" {
		return Session{}, false, nil
	}

	if err := base.ValidateSessionID(id); err != nil {
		return Session{}, false, err
	}

	query := s.db.WithContext(ctx).Where("id = ?", id)
	if opts.Archived != nil {
		query = query.Where("archived = ?", *opts.Archived)
	}

	var record sessionModel
	if err := query.First(&record).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Session{}, false, nil
		}

		return Session{}, false, err
	}

	session, err := sessionModelToSession(record)
	if err != nil {
		return Session{}, false, err
	}

	return session, true, nil
}

// List returns sessions ordered by most recently updated.
func (s *Store) List(ctx context.Context, opts base.SessionListOptions) ([]Session, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store is required")
	}

	var records []sessionModel
	query := s.db.WithContext(ctx).Order("updated_at desc").Order("id asc")
	if opts.Archived != nil {
		query = query.Where("archived = ?", *opts.Archived)
	}
	originSource := str.String(opts.OriginSource)
	if source := originSource.Trim(); source != "" {
		query = query.Where("origin_source = ?", source)
	}
	if err := query.Find(&records).Error; err != nil {
		return nil, err
	}

	sessions := make([]Session, 0, len(records))
	for _, record := range records {
		session, err := sessionModelToSession(record)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}

	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].UpdatedAt.Equal(sessions[j].UpdatedAt) {
			return sessions[i].ID < sessions[j].ID
		}

		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})

	return sessions, nil
}

// Delete removes an active session and its messages, summaries, search rows, and vector rows.
func (s *Store) Delete(ctx context.Context, id string) error {
	if s == nil || s.db == nil {
		return errors.New("store is required")
	}

	idValue3 := str.String(id)
	id = idValue3.Trim()
	if err := base.ValidateSessionID(id); err != nil {
		return err
	}

	if id == base.DefaultSessionID {
		return errors.New("default session cannot be deleted")
	}

	deletedSessionIDs, err := s.deleteSessions(ctx, []string{id})
	if err != nil {
		return err
	}
	if len(deletedSessionIDs) == 0 {
		return errors.New("session not found")
	}

	return nil
}

func (s *Store) deleteSessions(ctx context.Context, ids []string) ([]string, error) {
	deletedSessionIDs := make([]string, 0, len(ids))
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, id := range ids {
			var session sessionModel

			if err := tx.First(&session, "id = ?", id).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				}

				return err
			}

			if err := tx.Where("session_id = ?", id).Delete(&messageModel{}).Error; err != nil {
				return err
			}
			if err := tx.Where("session_id = ?", id).Delete(&vectorIndexStateModel{}).Error; err != nil {
				return err
			}
			if err := deleteSearchRows(tx, id); err != nil {
				return err
			}

			if err := tx.Where("session_id = ?", id).Delete(&summaryModel{}).Error; err != nil {
				return err
			}
			if err := deleteSessionInbox(tx, id); err != nil {
				return err
			}

			if err := tx.Delete(&session).Error; err != nil {
				return err
			}

			if err := tx.Where("key = ? AND value = ?", currentSessionStateKey, id).
				Delete(&stateModel{}).Error; err != nil {
				return err
			}
			deletedSessionIDs = append(deletedSessionIDs, id)
		}

		return nil
	}); err != nil {
		return nil, err
	}

	for _, sessionID := range deletedSessionIDs {
		if err := s.deleteVectorRowsBySession(ctx, sessionID); err != nil {
			return deletedSessionIDs, s.handleVectorStoreError(err)
		}
	}

	return deletedSessionIDs, nil
}

func deleteSessionInbox(tx *gorm.DB, sessionID string) error {
	for _, model := range []any{
		&sessionEventModel{},
		&sessionRunModel{},
		&sessionQueueEntryModel{},
		&sessionExecutionStateModel{},
	} {
		if err := tx.Where("session_id = ?", sessionID).Delete(model).Error; err != nil {
			return err
		}
	}
	return nil
}

// AppendMessages appends messages to a session and updates lexical/vector search indexes.
func (s *Store) AppendMessages(ctx context.Context, id string, messages []morphmsg.Message) error {
	if s == nil || s.db == nil {
		return errors.New("store is required")
	}

	idValue4 := str.String(id)
	id = idValue4.Trim()
	if err := base.ValidateSessionID(id); err != nil {
		return err
	}

	if len(messages) == 0 {
		return nil
	}

	var records []messageModel
	err := runSQLiteWriteWithRetry(ctx, func(writeCtx context.Context) error {
		return s.db.WithContext(writeCtx).Transaction(func(tx *gorm.DB) error {
			var err error
			records, err = s.appendMessagesInTransaction(tx, id, messages)
			return err
		})
	})
	if err != nil {
		return err
	}

	s.indexPersistedMessages(ctx, records)

	return nil
}

func (s *Store) appendMessagesInTransaction(
	tx *gorm.DB,
	sessionID string,
	messages []morphmsg.Message,
) ([]messageModel, error) {
	var session sessionModel
	if err := tx.First(&session, "id = ?", sessionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("session not found")
		}
		return nil, err
	}

	var nextSequence int
	if err := tx.Model(&messageModel{}).Where("session_id = ?", sessionID).
		Select("COALESCE(MAX(sequence), -1) + 1").Scan(&nextSequence).Error; err != nil {
		return nil, err
	}

	records := messagesToMessageModelsWithOffset(sessionID, messages, nextSequence)
	if len(records) > 0 {
		if err := tx.Create(&records).Error; err != nil {
			return nil, err
		}
		if err := messageModels(records).searchRows().insert(tx); err != nil {
			return nil, err
		}
		if s.vectors != nil {
			states := messageModels(records).getVectorIndexStates(s.vectors.Chunking)
			if len(states) > 0 {
				if err := tx.Create(&states).Error; err != nil {
					return nil, err
				}
			}
		}
	}

	session.UpdatedAt = time.Now().UTC()
	if err := tx.Save(&session).Error; err != nil {
		return nil, err
	}

	return records, nil
}

func (s *Store) indexPersistedMessages(ctx context.Context, records []messageModel) {
	if s == nil || len(records) == 0 || s.vectors == nil {
		return
	}

	indexErr := s.indexVectors(ctx, records)
	if err := s.setVectorIndexResult(ctx, records, indexErr); err != nil {
		applySafeErrorLog(s.logVectorEvent("status update failed"), err).
			Int("message_count", len(records)).
			Msg("session vector index status update failed")
	}
	if indexErr != nil {
		applySafeErrorLog(s.logVectorEvent("online indexing failed"), indexErr).
			Str("session_id", records[0].SessionID).
			Int("message_count", len(records)).
			Msg("session vector indexing failed after message persistence")
	}
}

// GetMessages loads messages with role, name, order, offset, and limit filters.
func (s *Store) GetMessages(
	ctx context.Context,
	id string,
	opts MessageQueryOptions,
) ([]morphmsg.Message, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store is required")
	}

	if _, err := base.NormalizeMessageQueryOrder(opts.Order); err != nil {
		return nil, err
	}

	idValue5 := str.String(id)
	id = idValue5.Trim()
	if id == "" {
		return nil, nil
	}

	if err := base.ValidateSessionID(id); err != nil {
		return nil, err
	}

	offset := max(opts.Offset, 0)

	var records []messageModel
	query := applySessionMessageFilters(s.db.WithContext(ctx), id, opts).
		Order("sequence " + getMessageQueryOrder(opts)).
		Offset(offset)
	if opts.Limit > 0 {
		query = query.Limit(opts.Limit)
	}
	if err := query.Find(&records).Error; err != nil {
		return nil, err
	}

	return messageModels(records).messages(), nil
}

// GetMessagesByIDs loads active session messages by database row IDs.
func (s *Store) GetMessagesByIDs(
	ctx context.Context,
	id string,
	messageIDs []uint,
) ([]base.MessageRecord, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store is required")
	}

	idValue6 := str.String(id)
	id = idValue6.Trim()
	if id == "" || len(messageIDs) == 0 {
		return nil, nil
	}
	if err := base.ValidateSessionID(id); err != nil {
		return nil, err
	}

	var records []messageModel
	if err := s.db.WithContext(ctx).
		Where("session_id = ? AND id IN ?", id, messageIDs).
		Order("sequence asc").
		Find(&records).Error; err != nil {
		return nil, err
	}

	return messageModels(records).records(), nil
}

// GetMessageWindow returns messages surrounding an anchor message in sequence order.
func (s *Store) GetMessageWindow(
	ctx context.Context,
	id string,
	anchorMessageID uint,
	before int,
	after int,
) ([]base.MessageRecord, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store is required")
	}

	idValue7 := str.String(id)
	id = idValue7.Trim()
	if id == "" || anchorMessageID == 0 {
		return nil, nil
	}
	if err := base.ValidateSessionID(id); err != nil {
		return nil, err
	}
	if before < 0 || after < 0 {
		return nil, errors.New("before and after must be greater than or equal to zero")
	}

	var anchor messageModel
	if err := s.db.WithContext(ctx).
		Where("session_id = ? AND id = ?", id, anchorMessageID).
		First(&anchor).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}

		return nil, err
	}

	start := max(anchor.Sequence-before, 0)
	end := anchor.Sequence + after + 1

	var records []messageModel
	if err := s.db.WithContext(ctx).
		Where("session_id = ? AND sequence >= ? AND sequence < ?", id, start, end).
		Order("sequence asc").
		Find(&records).Error; err != nil {
		return nil, err
	}

	return messageModels(records).records(), nil
}

// CountMessages counts messages matching the supplied filters.
func (s *Store) CountMessages(
	ctx context.Context,
	id string,
	opts MessageQueryOptions,
) (int, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("store is required")
	}

	if _, err := base.NormalizeMessageQueryOrder(opts.Order); err != nil {
		return 0, err
	}

	idValue8 := str.String(id)
	id = idValue8.Trim()
	if id == "" {
		return 0, nil
	}

	if err := base.ValidateSessionID(id); err != nil {
		return 0, err
	}

	var count int64
	if err := applySessionMessageFilters(s.db.WithContext(ctx), id, opts).
		Count(&count).Error; err != nil {
		return 0, err
	}

	return int(count), nil
}

// SearchMessages runs BM25 search or hybrid BM25/vector search depending on configuration.
func (s *Store) SearchMessages(
	ctx context.Context,
	id string,
	opts base.SearchMessageOptions,
) ([]base.SearchMessageResult, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store is required")
	}

	idValue9 := str.String(id)
	id = idValue9.Trim()
	if id != "" {
		if err := base.ValidateSessionID(id); err != nil {
			return nil, err
		}
	} else {
		ignoreSessionID := str.String(opts.IgnoreSessionID)
		opts.IgnoreSessionID = ignoreSessionID.Trim()
	}
	if id == "" && opts.IgnoreSessionID != "" {
		if err := base.ValidateSessionID(opts.IgnoreSessionID); err != nil {
			return nil, err
		}
	}

	queryText := buildFTSSearchQuery(opts.Query)
	if queryText == "" {
		return nil, nil
	}

	if s.vectors != nil {
		return s.searchMessagesHybrid(ctx, id, opts, queryText)
	}

	s.logSearchEvent("lexical search started", id, opts).Msg("session search lexical search started")
	records, err := s.searchMessagesLexical(ctx, id, opts, queryText, 0, true)
	if err != nil {
		applySafeErrorLog(s.logSearchEvent("lexical search failed", id, opts), err).
			Msg("session search lexical search failed")
		return nil, err
	}

	results := searchMessageResultRowsToResults(records)
	s.logSearchEvent("lexical search completed", id, opts).
		Int("session_count", len(results)).
		Int("message_count", len(records)).
		Msg("session search lexical search completed")

	return results, nil
}

// searchMessagesLexical returns BM25-ranked message rows with optional early limiting.
func (s *Store) searchMessagesLexical(
	ctx context.Context,
	id string,
	opts base.SearchMessageOptions,
	queryText string,
	rowLimit int,
	applyLimits bool,
) ([]searchSessionResultRow, error) {
	args := []any{queryText}
	var sql strings.Builder
	sql.WriteString(`
-- Read matching FTS rows and compute BM25; lower scores are more relevant.
WITH raw_hits AS (
	SELECT
		rowid AS search_rowid,
		CAST(message_id AS INTEGER) AS message_id,
		session_id,
		body AS matched_text,
		tool_name AS matched_tool_name,
		bm25(`)
	sql.WriteString(sessionMessageSearchTable)
	sql.WriteString(`) AS score
	FROM `)
	sql.WriteString(sessionMessageSearchTable)
	sql.WriteString(`
	WHERE body MATCH ?`)
	if id != "" {
		sql.WriteString(` AND session_id = ?`)
		args = append(args, id)
	} else if opts.IgnoreSessionID != "" {
		sql.WriteString(` AND session_id <> ?`)
		args = append(args, opts.IgnoreSessionID)
	}
	roleValue := str.String(string(opts.Role))
	if role := roleValue.Trim(); role != "" {
		sql.WriteString(` AND role = ?`)
		args = append(args, role)
	}
	if toolName := normalizeSearchValue(opts.ToolName); toolName != "" {
		sql.WriteString(` AND tool_name = ?`)
		args = append(args, toolName)
	}
	sql.WriteString(`
),
-- Collapse duplicate indexed rows for the same message to the best hit.
ranked_hits AS (
	SELECT
		message_id,
		session_id,
		matched_text,
		matched_tool_name,
		score,
		ROW_NUMBER() OVER (
			PARTITION BY message_id
			ORDER BY score ASC, CASE WHEN matched_tool_name <> '' THEN 0 ELSE 1 END, search_rowid ASC
		) AS hit_rank
	FROM raw_hits
),

-- Join the selected FTS hits back to durable message rows.
message_hits AS (
	SELECT
		m.id,
		m.session_id,
		m.sequence,
		m.role,
		m.name,
		m.content,
		m.tool_calls,
		m.tool_call_id,
		m.created_at,
		m.updated_at,
		hits.matched_text,
		hits.matched_tool_name,
		hits.score
	FROM session_messages AS m
	JOIN ranked_hits AS hits ON hits.message_id = m.id AND hits.hit_rank = 1
),

-- Aggregate per-session metadata used for grouping and session ranking.
session_groups AS (
	SELECT
		session_id,
		COUNT(*) AS match_count,
		MIN(score) AS best_score,
		strftime('%Y-%m-%dT%H:%M:%fZ', MAX(created_at)) AS last_matched_at
	FROM message_hits
	GROUP BY session_id
),

-- Rank sessions by best message relevance, then latest matching message.
ranked_sessions AS (
	SELECT
		session_id,
		match_count,
		best_score,
		last_matched_at,
		ROW_NUMBER() OVER (
			ORDER BY best_score ASC, last_matched_at DESC, session_id ASC
		) AS session_rank
	FROM session_groups
),

-- Rank messages inside each selected session by relevance, then recency.
ranked_session_hits AS (
	SELECT
		mh.id,
		mh.session_id,
		mh.sequence,
		mh.role,
		mh.name,
		mh.content,
		mh.tool_calls,
		mh.tool_call_id,
		mh.created_at,
		mh.updated_at,
		mh.matched_text,
		mh.matched_tool_name,
		mh.score,
		rs.match_count,
		rs.best_score,
		rs.last_matched_at,
		ROW_NUMBER() OVER (
			PARTITION BY mh.session_id
			ORDER BY mh.score ASC, mh.created_at DESC, mh.id DESC
	) AS message_rank
	FROM message_hits AS mh
	JOIN ranked_sessions AS rs ON rs.session_id = mh.session_id`)
	if applyLimits && opts.MaxSessions > 0 {
		sql.WriteString(`
	WHERE rs.session_rank <= ?`)
		args = append(args, opts.MaxSessions)
	}
	sql.WriteString(`
)

-- Emit ranked rows for the Go result mapper.
SELECT
	id,
	session_id,
	sequence,
	role,
	name,
	content,
	tool_calls,
	tool_call_id,
	created_at,
	updated_at,
	matched_text,
	matched_tool_name,
	score,
	match_count,
	best_score,
	last_matched_at
FROM ranked_session_hits`)
	if applyLimits && opts.MaxMessagesPerSession > 0 {
		sql.WriteString(`
WHERE message_rank <= ?`)
		args = append(args, opts.MaxMessagesPerSession)
	}
	sql.WriteString(`
ORDER BY best_score ASC, last_matched_at DESC, session_id ASC, score ASC, created_at DESC, id DESC`)
	if rowLimit > 0 {
		sql.WriteString(`
LIMIT ?`)
		args = append(args, rowLimit)
	}

	var records []searchSessionResultRow
	if err := s.db.WithContext(ctx).Raw(sql.String(), args...).Scan(&records).Error; err != nil {
		return nil, err
	}

	return records, nil
}

func applySessionMessageFilters(query *gorm.DB, id string, opts MessageQueryOptions) *gorm.DB {
	query = query.Model(&messageModel{}).Where("session_id = ?", id)
	roleValue2 := str.String(string(opts.Role))
	if role := roleValue2.Trim(); role != "" {
		query = query.Where("role = ?", role)
	}
	nameValue := str.String(opts.Name)
	if name := nameValue.Trim(); name != "" {
		query = query.Where("name = ?", name)
	}
	return query
}

func getMessageQueryOrder(opts MessageQueryOptions) string {
	order, err := base.NormalizeMessageQueryOrder(opts.Order)
	if err != nil {
		return base.MessageOrderAsc
	}

	return order
}

// GetMessage loads one message by its sequence index.
func (s *Store) GetMessage(
	ctx context.Context,
	id string,
	index int,
) (morphmsg.Message, bool, error) {
	if s == nil || s.db == nil {
		return morphmsg.Message{}, false, errors.New("store is required")
	}

	idValue10 := str.String(id)
	id = idValue10.Trim()
	if id == "" || index < 0 {
		return morphmsg.Message{}, false, nil
	}

	if err := base.ValidateSessionID(id); err != nil {
		return morphmsg.Message{}, false, err
	}

	var record messageModel
	if err := s.db.WithContext(ctx).Where("session_id = ? AND sequence = ?", id, index).
		First(&record).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return morphmsg.Message{}, false, nil
		}

		return morphmsg.Message{}, false, err
	}

	return messageModels([]messageModel{record}).messages()[0], true, nil
}

// SaveSummary summarizes or replaces a session state.
func (s *Store) SaveSummary(ctx context.Context, summary SessionSummary) error {
	if s == nil || s.db == nil {
		return errors.New("store is required")
	}

	normalized, err := base.NormalizeSessionSummary(summary)
	if err != nil {
		return err
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var session sessionModel

		if err := tx.First(&session, "id = ?", normalized.SessionID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("session not found")
			}

			return err
		}

		record := summaryModel{
			SessionID:          normalized.SessionID,
			SourceEndOffset:    normalized.SourceEndOffset,
			SourceMessageCount: normalized.SourceMessageCount,
			UpdatedAt:          normalized.UpdatedAt,
			SessionSummary:     normalized.SessionSummary,
			CurrentTask:        normalized.CurrentTask,
			Discoveries:        stringsToJSON(normalized.Discoveries),
			OpenQuestions:      stringsToJSON(normalized.OpenQuestions),
			NextActions:        stringsToJSON(normalized.NextActions),
		}

		return tx.Save(&record).Error
	})
}

// GetSummary loads the current summary for a session.
func (s *Store) GetSummary(ctx context.Context, sessionID string) (SessionSummary, bool, error) {
	if s == nil || s.db == nil {
		return SessionSummary{}, false, errors.New("store is required")
	}

	sessionIDValue := str.String(sessionID)
	sessionID = sessionIDValue.Trim()
	if sessionID == "" {
		return SessionSummary{}, false, nil
	}

	if err := base.ValidateSessionID(sessionID); err != nil {
		return SessionSummary{}, false, err
	}

	var record summaryModel
	if err := s.db.WithContext(ctx).First(&record, "session_id = ?", sessionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return SessionSummary{}, false, nil
		}

		return SessionSummary{}, false, err
	}

	summary, err := summaryModelToSessionSummary(record)
	if err != nil {
		return SessionSummary{}, false, err
	}

	return summary, true, nil
}

// DeleteSummary removes a session summary if it exists.
func (s *Store) DeleteSummary(ctx context.Context, sessionID string) error {
	if s == nil || s.db == nil {
		return errors.New("store is required")
	}

	sessionIDValue2 := str.String(sessionID)
	sessionID = sessionIDValue2.Trim()
	if err := base.ValidateSessionID(sessionID); err != nil {
		return err
	}

	return s.db.WithContext(ctx).Where("session_id = ?", sessionID).Delete(&summaryModel{}).Error
}

func (s *Store) Archive(ctx context.Context, id string, req base.SessionArchiveRequest) (Session, error) {
	if s == nil || s.db == nil {
		return Session{}, errors.New("store is required")
	}

	idValue11 := str.String(id)
	id = idValue11.Trim()
	if err := base.ValidateSessionID(id); err != nil {
		return Session{}, err
	}

	var session Session
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record sessionModel
		if err := tx.First(&record, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("session not found")
			}

			return err
		}

		var count int64
		if err := tx.Model(&messageModel{}).Where("session_id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return errors.New("source session has no messages")
		}

		loaded, err := sessionModelToSession(record)
		if err != nil {
			return err
		}
		archived, err := base.MarkSessionArchived(loaded, req.ArchivedAt, req.ExpiresAt)
		if err != nil {
			return err
		}

		record.Archived = archived.Archived
		record.ArchivedAt = archived.ArchivedAt
		record.ExpiresAt = archived.ExpiresAt
		session = archived

		if err := tx.Save(&record).Error; err != nil {
			return err
		}

		return tx.Where("key = ? AND value = ?", currentSessionStateKey, id).Delete(&stateModel{}).Error
	}); err != nil {
		return Session{}, err
	}

	return session, nil
}

func (s *Store) Rename(ctx context.Context, req base.SessionRenameRequest) (Session, error) {
	if s == nil || s.db == nil {
		return Session{}, errors.New("store is required")
	}

	sessionIDValue3 := str.String(req.SessionID)
	req.SessionID = sessionIDValue3.Trim()
	if err := base.ValidateSessionID(req.SessionID); err != nil {
		return Session{}, err
	}

	title, titleSource := base.NormalizeSessionTitleMetadata(req.Title, req.TitleSource)
	if title == "" {
		return Session{}, errors.New("session title is required")
	}

	renamedAt := req.RenamedAt.UTC()
	if renamedAt.IsZero() {
		renamedAt = time.Now().UTC()
	}

	var session Session
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record sessionModel
		if err := tx.First(&record, "id = ?", req.SessionID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("session not found")
			}

			return err
		}

		record.Title = title
		record.TitleSource = titleSource
		record.UpdatedAt = renamedAt

		if err := tx.Model(&record).UpdateColumns(map[string]any{
			"title":        title,
			"title_source": titleSource,
			"updated_at":   renamedAt,
		}).Error; err != nil {
			return err
		}

		loaded, err := sessionModelToSession(record)
		if err != nil {
			return err
		}

		session = loaded
		return nil
	}); err != nil {
		return Session{}, err
	}

	return session, nil
}

// ClearMessages removes all messages for a session.
func (s *Store) ClearMessages(ctx context.Context, id string) error {
	if s == nil || s.db == nil {
		return errors.New("store is required")
	}

	idValue12 := str.String(id)
	id = idValue12.Trim()
	if err := base.ValidateSessionID(id); err != nil {
		return err
	}

	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var session sessionModel
		if err := tx.First(&session, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("session not found")
			}

			return err
		}

		if err := tx.Where("session_id = ?", id).Delete(&messageModel{}).Error; err != nil {
			return err
		}
		if err := tx.Where("session_id = ?", id).Delete(&vectorIndexStateModel{}).Error; err != nil {
			return err
		}
		if err := deleteSearchRows(tx, id); err != nil {
			return err
		}

		if err := tx.Where("session_id = ?", id).Delete(&summaryModel{}).Error; err != nil {
			return err
		}

		session.CompactionCompletedAt = time.Time{}
		session.CompactionFailedAt = time.Time{}
		session.CompactionLastError = ""
		session.CompactionRequestedAt = time.Time{}
		session.CompactionStartedAt = time.Time{}
		session.CompactionStatus = ""
		session.CompactionTargetMessageCount = 0
		session.CompactionTargetOffset = 0
		session.UpdatedAt = time.Now().UTC()

		return tx.Save(&session).Error
	}); err != nil {
		return err
	}

	if err := s.deleteVectorRowsBySession(ctx, id); err != nil {
		return s.handleVectorStoreError(err)
	}

	return nil
}

// DeleteExpiredArchives removes archived sessions whose expiration time is at or before now.
func (s *Store) DeleteExpiredArchives(ctx context.Context, now time.Time) error {
	if s == nil || s.db == nil {
		return errors.New("store is required")
	}

	now = now.UTC()
	var expiredSessionIDs []string
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&sessionModel{}).
			Where("archived = ? AND expires_at <= ?", true, now).
			Pluck("id", &expiredSessionIDs).Error; err != nil {
			return err
		}

		return nil
	}); err != nil {
		return err
	}

	_, err := s.deleteSessions(ctx, expiredSessionIDs)
	return err
}

func (s *Store) Unarchive(ctx context.Context, id string) (Session, error) {
	if s == nil || s.db == nil {
		return Session{}, errors.New("store is required")
	}

	idValue13 := str.String(id)
	id = idValue13.Trim()
	if err := base.ValidateSessionID(id); err != nil {
		return Session{}, err
	}

	var session Session
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record sessionModel
		if err := tx.First(&record, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("session not found")
			}

			return err
		}

		loaded, err := sessionModelToSession(record)
		if err != nil {
			return err
		}
		unarchived, err := base.ClearSessionArchive(loaded)
		if err != nil {
			return err
		}

		record.Archived = false
		record.ArchivedAt = time.Time{}
		record.ExpiresAt = time.Time{}
		session = unarchived

		return tx.Save(&record).Error
	}); err != nil {
		return Session{}, err
	}

	return session, nil
}

// SetCurrent marks an existing session as the current session.
func (s *Store) SetCurrent(ctx context.Context, id string) error {
	if s == nil || s.db == nil {
		return errors.New("store is required")
	}

	idValue14 := str.String(id)
	id = idValue14.Trim()
	if err := base.ValidateSessionID(id); err != nil {
		return err
	}

	active := false
	_, ok, err := s.Get(ctx, id, base.SessionGetOptions{Archived: &active})
	if err != nil {
		return err
	}

	if !ok {
		return errors.New("session not found")
	}

	record := stateModel{
		Key:       currentSessionStateKey,
		Value:     id,
		UpdatedAt: time.Now().UTC(),
	}

	return s.db.WithContext(ctx).Save(&record).Error
}

// Current returns the current session ID if one is set.
func (s *Store) Current(ctx context.Context) (string, bool, error) {
	if s == nil || s.db == nil {
		return "", false, errors.New("store is required")
	}

	var record stateModel
	if err := s.db.WithContext(ctx).First(&record, "key = ?", currentSessionStateKey).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", false, nil
		}

		return "", false, err
	}
	value := str.String(record.Value).Trim()
	if value == "" {
		return "", false, nil
	}

	return value, true, nil
}

func (s *Store) ClearCurrent(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("store is required")
	}

	return s.db.WithContext(ctx).Where("key = ?", currentSessionStateKey).Delete(&stateModel{}).Error
}

func summaryModelToSessionSummary(record summaryModel) (SessionSummary, error) {
	return base.NormalizeSessionSummary(SessionSummary{
		SessionID:          record.SessionID,
		SourceEndOffset:    record.SourceEndOffset,
		SourceMessageCount: record.SourceMessageCount,
		UpdatedAt:          record.UpdatedAt,
		SessionSummary:     record.SessionSummary,
		CurrentTask:        record.CurrentTask,
		Discoveries:        jsonToStrings(record.Discoveries),
		OpenQuestions:      jsonToStrings(record.OpenQuestions),
		NextActions:        jsonToStrings(record.NextActions),
	})
}

func sessionModelToSession(record sessionModel) (Session, error) {
	iDValue2 := str.String(record.ID)
	id := iDValue2.Trim()
	if id == "" {
		return Session{}, errors.New("session id is required")
	}

	title, titleSource := base.NormalizeSessionTitleMetadata(record.Title, record.TitleSource)
	session := Session{
		Archived:   record.Archived,
		ArchivedAt: record.ArchivedAt,
		CreatedAt:  record.CreatedAt,
		Compaction: base.SessionCompaction{
			CompletedAt:        record.CompactionCompletedAt,
			FailedAt:           record.CompactionFailedAt,
			LastError:          record.CompactionLastError,
			RequestedAt:        record.CompactionRequestedAt,
			StartedAt:          record.CompactionStartedAt,
			Status:             base.SessionCompactionStatus(record.CompactionStatus),
			TargetMessageCount: record.CompactionTargetMessageCount,
			TargetOffset:       record.CompactionTargetOffset,
		},
		EpisodicCheckpointOffset: record.EpisodicCheckpointOffset,
		ExpiresAt:                record.ExpiresAt,
		ID:                       id,
		LastPromptTokens:         record.LastPromptTokens,
		Origin: base.SessionOrigin{
			AccountID:      record.OriginAccountID,
			ConversationID: record.OriginConversationID,
			Source:         record.OriginSource,
			ThreadID:       record.OriginThreadID,
		},
		ReflectionCheckpointOffset: record.ReflectionCheckpointOffset,
		ReasoningEffortOverride:    stringValue(record.ReasoningEffortOverride),
		Title:                      title,
		TitleSource:                titleSource,
	}

	if !record.CreatedAt.IsZero() {
		session.CreatedAt = record.CreatedAt.UTC()
	}

	if !record.UpdatedAt.IsZero() {
		session.UpdatedAt = record.UpdatedAt.UTC()
	}
	if !record.ArchivedAt.IsZero() {
		session.ArchivedAt = record.ArchivedAt.UTC()
	}
	if !record.ExpiresAt.IsZero() {
		session.ExpiresAt = record.ExpiresAt.UTC()
	}

	return session, nil
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}

	return &value
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}

func messagesToMessageModels(sessionID string, messages []morphmsg.Message) []messageModel {
	return messagesToMessageModelsWithOffset(sessionID, messages, 0)
}

func messagesToMessageModelsWithOffset(sessionID string, messages []morphmsg.Message, offset int) []messageModel {
	if len(messages) == 0 {
		return nil
	}

	records := make([]messageModel, 0, len(messages))
	for i, message := range messages {
		records = append(records, messageModel{
			ID:              message.ID,
			SessionID:       sessionID,
			Sequence:        offset + i,
			Role:            string(message.Role),
			Name:            message.Name,
			Content:         message.Content,
			SemanticContent: message.SemanticContent,
			ToolCalls:       toolCallsToJSON(message.ToolCalls),
			ToolCallID:      message.ToolCallID,
			CreatedAt:       message.CreatedAt,
		})
	}

	return records
}

// messages converts active message models to domain messages.
func (records messageModels) messages() []morphmsg.Message {
	if len(records) == 0 {
		return nil
	}

	messages := make([]morphmsg.Message, 0, len(records))
	for _, record := range records {
		messages = append(messages, morphmsg.Message{
			ID:              record.ID,
			Role:            morphmsg.Role(record.Role),
			Name:            record.Name,
			Content:         record.Content,
			SemanticContent: record.SemanticContent,
			ToolCalls:       jsonToToolCalls(record.ToolCalls),
			ToolCallID:      record.ToolCallID,
			CreatedAt:       record.CreatedAt,
		})
	}

	return messages
}

// records converts active message models to message records with sequence offsets.
func (records messageModels) records() []base.MessageRecord {
	if len(records) == 0 {
		return nil
	}

	messages := records.messages()
	messageRecords := make([]base.MessageRecord, 0, len(records))
	for idx, record := range records {
		messageRecords = append(messageRecords, base.MessageRecord{
			Offset:  record.Sequence,
			Message: messages[idx],
		})
	}

	return messageRecords
}

func searchMessageResultRowsToResults(records []searchSessionResultRow) []base.SearchMessageResult {
	if len(records) == 0 {
		return nil
	}

	results := make([]base.SearchMessageResult, 0)
	indexBySessionID := make(map[string]int, len(records))
	for _, record := range records {
		index, ok := indexBySessionID[record.SessionID]
		if !ok {
			results = append(results, base.SearchMessageResult{
				SessionID:     record.SessionID,
				LastMatchedAt: getSearchSessionResultTime(record.LastMatchedAt),
				MatchCount:    record.MatchCount,
				Messages:      make([]base.SearchMessageHit, 0),
			})
			index = len(results) - 1
			indexBySessionID[record.SessionID] = index
		}
		matchedTextValue := str.String(record.MatchedText)
		matchedToolNameValue := str.String(record.MatchedToolName)
		results[index].Messages = append(results[index].Messages, base.SearchMessageHit{
			SessionID: record.SessionID,
			Message: morphmsg.Message{
				ID:         record.ID,
				Role:       morphmsg.Role(record.Role),
				Name:       record.Name,
				Content:    record.Content,
				ToolCalls:  jsonToToolCalls(record.ToolCalls),
				ToolCallID: record.ToolCallID,
				CreatedAt:  record.CreatedAt,
			},
			MatchedText:     matchedTextValue.Trim(),
			MatchedToolName: matchedToolNameValue.Trim(),
		})
	}

	return results
}

func getSearchSessionResultTime(value string) time.Time {
	value2 := str.String(value)
	value = value2.Trim()
	if value == "" {
		return time.Time{}
	}

	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}

	return parsed.UTC()
}

func toolCallsToJSON(toolCalls []morphmsg.ToolCall) string {
	if len(toolCalls) == 0 {
		return ""
	}

	raw, _ := json.Marshal(toolCalls)

	return string(raw)
}

func stringsToJSON(values []string) string {
	if len(values) == 0 {
		return ""
	}

	raw, _ := json.Marshal(values)

	return string(raw)
}

func jsonToStrings(value string) []string {
	value3 := str.String(value)
	value = value3.Trim()
	if value == "" {
		return nil
	}

	var values []string
	if err := json.Unmarshal([]byte(value), &values); err != nil {
		return nil
	}

	return values
}

func jsonToToolCalls(value string) []morphmsg.ToolCall {
	value4 := str.String(value)
	value = value4.Trim()
	if value == "" {
		return nil
	}

	var toolCalls []morphmsg.ToolCall
	if err := json.Unmarshal([]byte(value), &toolCalls); err != nil {
		return nil
	}

	return toolCalls
}

// searchRow is one FTS row and the matching unit used for vector search.
type searchRow = search.MessageIndexRow

// searchRows is a typed slice for FTS/vector searchable message rows.
type searchRows []searchRow

func ensureSearchIndex(db *gorm.DB) error {
	if db == nil {
		return errors.New("session db is required")
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS ` + sessionMessageSearchTable + ` USING fts5(
	message_id UNINDEXED,
	session_id UNINDEXED,
	role UNINDEXED,
	tool_name UNINDEXED,
	body,
	tokenize='unicode61'
)`).Error; err != nil {
			return fmt.Errorf("failed to create session message search index: %w", err)
		}

		return nil
	})
}

// insert writes search rows to the FTS table.
func (rows searchRows) insert(tx *gorm.DB) error {
	if tx == nil || len(rows) == 0 {
		return nil
	}

	for _, row := range rows {
		if err := tx.Exec(
			`INSERT INTO `+sessionMessageSearchTable+` (message_id, session_id, role, tool_name, body) VALUES (?, ?, ?, ?, ?)`,
			row.MessageID,
			row.SessionID,
			row.Role,
			row.ToolName,
			row.Body,
		).Error; err != nil {
			return fmt.Errorf("failed to insert session message search row: %w", err)
		}
	}

	return nil
}

func deleteSearchRows(tx *gorm.DB, sessionID string) error {
	if tx == nil {
		return nil
	}

	if err := tx.Exec(`DELETE FROM `+sessionMessageSearchTable+` WHERE session_id = ?`, sessionID).Error; err != nil {
		return fmt.Errorf("failed to delete session message search rows: %w", err)
	}

	return nil
}

// searchRows derives FTS/vector searchable rows from active message models.
func (records messageModels) searchRows() searchRows {
	if len(records) == 0 {
		return nil
	}

	rows := make(searchRows, 0, len(records))
	for _, record := range records {
		rows = append(rows, messageModelToSearchRows(record)...)
	}

	return rows
}

func messageModelToSearchRows(record messageModel) searchRows {
	roleValue3 := str.String(record.Role)
	rows := search.MessageIndexRowsFromMessage(record.SessionID, morphmsg.Message{
		ID:              record.ID,
		Role:            morphmsg.Role(roleValue3.Trim()),
		Content:         record.Content,
		SemanticContent: record.SemanticContent,
		Name:            record.Name,
		ToolCallID:      record.ToolCallID,
		ToolCalls:       jsonToToolCalls(record.ToolCalls),
		CreatedAt:       record.CreatedAt,
	})
	for idx := range rows {
		rows[idx].UpdatedAt = record.UpdatedAt
	}

	return searchRows(rows)
}

func buildFTSSearchQuery(query string) string {
	tokens := getSearchTokens(query)
	if len(tokens) == 0 {
		return ""
	}

	quoted := make([]string, 0, len(tokens))
	for _, token := range tokens {
		quoted = append(quoted, `"`+token+`"`)
	}

	return strings.Join(quoted, " AND ")
}

func getSearchTokens(query string) []string {
	queryValue := str.String(query)
	fields := strings.FieldsFunc(queryValue.Trim(), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if len(fields) == 0 {
		return nil
	}

	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		tokens = append(tokens, normalizeSearchValue(field))
	}

	return tokens
}
