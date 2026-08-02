package sqlite

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	sqlitevec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	morphdb "github.com/xymorphic/morph/internal/db"
	"github.com/xymorphic/morph/internal/state/search/vectorstore"
	"github.com/xymorphic/morph/pkg/str"
)

const (
	recordsTable    = "vector_records"
	recordTagsTable = "vector_record_tags"
)

const (
	sqliteBusyRetryAttempts = 5
	sqliteBusyRetryDelay    = 25 * time.Millisecond
)

// Record aliases vectorstore.Record at this package boundary.
type Record = vectorstore.Record

// DeleteRequest aliases vectorstore.DeleteRequest at this package boundary.
type DeleteRequest = vectorstore.DeleteRequest

// SearchRequest aliases vectorstore.SearchRequest at this package boundary.
type SearchRequest = vectorstore.SearchRequest

// SearchResult aliases vectorstore.SearchResult at this package boundary.
type SearchResult = vectorstore.SearchResult

// SearchMatch aliases vectorstore.SearchMatch at this package boundary.
type SearchMatch = vectorstore.SearchMatch

// ListRequest aliases vectorstore.ListRequest at this package boundary.
type ListRequest = vectorstore.ListRequest

// ListResult aliases vectorstore.ListResult at this package boundary.
type ListResult = vectorstore.ListResult

// Filter aliases vectorstore.Filter at this package boundary.
type Filter = vectorstore.Filter

// StoreMetadata aliases vectorstore.StoreMetadata at this package boundary.
type StoreMetadata = vectorstore.StoreMetadata

// ModelMetadata aliases vectorstore.ModelMetadata at this package boundary.
type ModelMetadata = vectorstore.ModelMetadata

// SourceKind classifies the domain object represented by a vector source ID.
type SourceKind = vectorstore.SourceKind

// SourceKindSessionMessage is the package-level source kind session message constant.
const SourceKindSessionMessage = vectorstore.SourceKindSessionMessage

// SourceKindMemoryItem is the package-level source kind memory item constant.
const SourceKindMemoryItem = vectorstore.SourceKindMemoryItem

func init() {
	sqlitevec.Auto()
}

// Store persists vector records in SQLite.
type Store struct {
	db *gorm.DB
}

// NewStore returns a store backed by the supplied dependencies.
func NewStore(path string) (*Store, error) {
	pathValue := str.String(path)
	path = pathValue.Trim()
	if path == "" {
		return nil, errors.New("vector sqlite path is required")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("failed to create vector db directory: %w", err)
	}

	db, err := morphdb.OpenSQLite(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open vector db: %w", err)
	}

	return NewStoreFromDB(db)
}

// NewStoreFromDB returns a store using an existing database handle.
func NewStoreFromDB(db *gorm.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("vector db is required")
	}

	db = db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	if err := ensureSQLiteStorage(db); err != nil {
		return nil, err
	}

	return &Store{db: db}, nil
}

func (s *Store) Upsert(ctx context.Context, records []Record) error {
	if s == nil || s.db == nil {
		return errors.New("vector store is required")
	}
	if len(records) == 0 {
		return nil
	}

	if ctx == nil {
		ctx = context.Background()
	}

	return withSQLiteBusyRetry(ctx, func() error {
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			ensuredDimensions := make(map[int]struct{}, len(records))
			for _, record := range records {
				if err := vectorstore.ValidateRecord(record); err != nil {
					return err
				}

				if _, ok := ensuredDimensions[record.Dimensions]; !ok {
					if err := ensureIndexTable(tx, record.Dimensions); err != nil {
						return err
					}

					ensuredDimensions[record.Dimensions] = struct{}{}
				}
				if err := upsertRecord(tx, record); err != nil {
					return err
				}
			}

			return nil
		})
	})
}

func (s *Store) Delete(ctx context.Context, req DeleteRequest) error {
	if s == nil || s.db == nil {
		return errors.New("vector store is required")
	}
	if err := vectorstore.ValidateDeleteRequest(req); err != nil {
		return err
	}

	sourceIDs := normalizeDeleteSourceIDs(req)
	sessionIDValue := str.String(req.SessionID)
	sessionID := sessionIDValue.Trim()

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		rows, err := vectorRowsForDelete(tx, req.SourceKind, sourceIDs, sessionID)
		if err != nil {
			return err
		}
		getRowIDsByDimension := make(map[int][]int64, len(rows))
		for _, row := range rows {
			getRowIDsByDimension[row.Dimensions] = append(getRowIDsByDimension[row.Dimensions], row.RowID)
		}
		for dimensions, getRowIDs := range getRowIDsByDimension {
			if err := deleteIndexRows(tx, dimensions, getRowIDs); err != nil {
				return err
			}
		}
		if len(rows) > 0 {
			if err := tx.Exec(
				`DELETE FROM `+recordTagsTable+` WHERE record_id IN ?`,
				getRecordIDs(rows),
			).Error; err != nil {
				return fmt.Errorf("failed to delete vector record tags: %w", err)
			}
		}
		if err := deleteVectorRecords(tx, req.SourceKind, sourceIDs, sessionID); err != nil {
			return fmt.Errorf("failed to delete vector records: %w", err)
		}

		return nil
	})
}

func (s *Store) Search(ctx context.Context, req SearchRequest) (SearchResult, error) {
	if s == nil || s.db == nil {
		return SearchResult{}, errors.New("vector store is required")
	}
	if err := vectorstore.ValidateSearchRequest(req); err != nil {
		return SearchResult{}, err
	}

	sourceKindValue := str.String(string(req.Filter.SourceKind))
	if sourceKindValue.Trim() == "" {
		return SearchResult{}, errors.New("vector search filter source kind is required")
	}
	if err := validateSearchSourceIDs(req.Filter.SourceIDs); err != nil {
		return SearchResult{}, err
	}

	queryBlob, err := serialize(req.QueryVector)
	if err != nil {
		return SearchResult{}, err
	}

	db := s.db.WithContext(ctx)
	exists, err := indexTableExists(db, req.Dimensions)
	if err != nil {
		return SearchResult{}, err
	}
	if !exists {
		return SearchResult{}, nil
	}

	var sqlText strings.Builder
	sqlText.WriteString(`SELECT
	rv.vector_rowid,
	rv.id,
	rv.source_kind,
	rv.source_id,
	rv.session_id,
	rv.role,
	rv.tool_name,
	rv.embedding_model,
	rv.dimensions,
	rv.content_hash,
	rv.vector,
	rv.created_at,
	rv.updated_at,
	vec.distance
FROM ` + getIndexTableName(req.Dimensions) + ` AS vec
JOIN ` + recordsTable + ` AS rv ON rv.vector_rowid = vec.rowid
WHERE vec.vector MATCH ?
	AND k = ?
	AND vec.source_kind = ?
	AND vec.embedding_model = ?`)
	embeddingModelValue := str.String(req.EmbeddingModel)
	args := []any{
		queryBlob,
		req.Limit,
		string(req.Filter.SourceKind), embeddingModelValue.
			Trim(),
	}
	if len(req.Filter.SourceIDs) == 1 {
		sqlText.WriteString(`
	AND vec.source_id = ?`)
		args = append(args, req.Filter.SourceIDs[0])
	} else if len(req.Filter.SourceIDs) > 1 {
		sqlText.WriteString(`
	AND (`)
		for idx, sourceID := range req.Filter.SourceIDs {
			if idx > 0 {
				sqlText.WriteString(` OR `)
			}
			sqlText.WriteString(`vec.source_id = ?`)
			args = append(args, sourceID)
		}
		sqlText.WriteString(`)`)
	}
	sessionIDValue2 := str.String(req.Filter.SessionID)
	if sessionID := sessionIDValue2.Trim(); sessionID != "" {
		sqlText.WriteString(`
	AND vec.session_id = ?`)
		args = append(args, sessionID)
	}
	ignoreSessionIDValue := str.String(req.Filter.IgnoreSessionID)
	if ignoreSessionID := ignoreSessionIDValue.Trim(); ignoreSessionID != "" {
		sqlText.WriteString(`
	AND vec.session_id <> ?`)
		args = append(args, ignoreSessionID)
	}
	roleValue := str.String(req.Filter.Role)
	if role := roleValue.Trim(); role != "" {
		sqlText.WriteString(`
	AND vec.role = ?`)
		args = append(args, role)
	}
	toolNameValue := str.String(req.Filter.ToolName)
	if toolName := toolNameValue.Trim(); toolName != "" {
		sqlText.WriteString(`
	AND vec.tool_name = ?`)
		args = append(args, toolName)
	}
	appendTagFilters(&sqlText, &args, req.Filter.Tags)
	appendTagGroupFilters(&sqlText, &args, req.Filter.TagGroups)
	sqlText.WriteString(`
ORDER BY vec.distance ASC, rv.id ASC`)

	var rows []searchRow
	if err := db.Raw(sqlText.String(), args...).Scan(&rows).Error; err != nil {
		return SearchResult{}, fmt.Errorf("failed to search vectors: %w", err)
	}

	matches := make([]SearchMatch, 0, len(rows))
	tagsByID, err := loadRecordTags(db, getRowIDs(rows))
	if err != nil {
		return SearchResult{}, err
	}
	for _, row := range rows {
		record, err := row.record()
		if err != nil {
			return SearchResult{}, err
		}
		record.Tags = tagsByID[record.ID]
		matches = append(matches, SearchMatch{
			Record: record,
			Score:  1 - row.Distance,
		})
	}

	return SearchResult{Matches: matches}, nil
}

func (s *Store) Metadata(ctx context.Context) (StoreMetadata, error) {
	if s == nil || s.db == nil {
		return StoreMetadata{}, errors.New("vector store is required")
	}

	var models []ModelMetadata
	if err := s.db.WithContext(ctx).Raw(`SELECT
	embedding_model AS model,
	dimensions,
	COUNT(*) AS count
FROM ` + recordsTable + `
GROUP BY embedding_model, dimensions
ORDER BY embedding_model ASC, dimensions ASC`).Scan(&models).Error; err != nil {
		return StoreMetadata{}, fmt.Errorf("failed to load vector metadata: %w", err)
	}

	return StoreMetadata{Models: models}, nil
}

func (s *Store) List(ctx context.Context, req ListRequest) (ListResult, error) {
	if s == nil || s.db == nil {
		return ListResult{}, errors.New("vector store is required")
	}
	if err := vectorstore.ValidateListRequest(req); err != nil {
		return ListResult{}, err
	}

	sqlText := `SELECT
	vector_rowid,
	id,
	source_kind,
	source_id,
	session_id,
	role,
	tool_name,
	embedding_model,
	dimensions,
	content_hash,
	vector,
	created_at,
	updated_at
FROM ` + recordsTable + ` AS rv
WHERE embedding_model = ?`
	embeddingModelValue2 := str.String(req.EmbeddingModel)
	args := []any{embeddingModelValue2.Trim()}
	sourceKindValue2 := str.String(string(req.Filter.SourceKind))
	if sourceKind := sourceKindValue2.Trim(); sourceKind != "" {
		sqlText += `
	AND source_kind = ?`
		args = append(args, sourceKind)
	}
	if len(req.Filter.SourceIDs) == 1 {
		sqlText += `
	AND source_id = ?`
		args = append(args, req.Filter.SourceIDs[0])
	} else if len(req.Filter.SourceIDs) > 1 {
		sqlText += `
	AND source_id IN ?`
		args = append(args, req.Filter.SourceIDs)
	}
	sessionIDValue3 := str.String(req.Filter.SessionID)
	if sessionID := sessionIDValue3.Trim(); sessionID != "" {
		sqlText += `
	AND session_id = ?`
		args = append(args, sessionID)
	}
	ignoreSessionIDValue2 := str.String(req.Filter.IgnoreSessionID)
	if ignoreSessionID := ignoreSessionIDValue2.Trim(); ignoreSessionID != "" {
		sqlText += `
	AND session_id <> ?`
		args = append(args, ignoreSessionID)
	}
	roleValue2 := str.String(req.Filter.Role)
	if role := roleValue2.Trim(); role != "" {
		sqlText += `
	AND role = ?`
		args = append(args, role)
	}
	toolNameValue2 := str.String(req.Filter.ToolName)
	if toolName := toolNameValue2.Trim(); toolName != "" {
		sqlText += `
	AND tool_name = ?`
		args = append(args, toolName)
	}
	appendTagFiltersString(&sqlText, &args, req.Filter.Tags)
	appendTagGroupFiltersString(&sqlText, &args, req.Filter.TagGroups)
	sqlText += `
ORDER BY session_id ASC, source_id ASC, id ASC`

	var rows []searchRow
	if err := s.db.WithContext(ctx).Raw(sqlText, args...).Scan(&rows).Error; err != nil {
		return ListResult{}, fmt.Errorf("failed to list vectors: %w", err)
	}

	records := make([]Record, 0, len(rows))
	tagsByID, err := loadRecordTags(s.db.WithContext(ctx), getRowIDs(rows))
	if err != nil {
		return ListResult{}, err
	}
	for _, row := range rows {
		record, err := row.record()
		if err != nil {
			return ListResult{}, err
		}
		record.Tags = tagsByID[record.ID]
		records = append(records, record)
	}

	return ListResult{Records: records}, nil
}

func ensureSQLiteStorage(db *gorm.DB) error {
	if db == nil {
		return errors.New("vector db is required")
	}
	if err := db.Exec(`SELECT vec_version()`).Error; err != nil {
		return fmt.Errorf("sqlite vector extension is required: %w", err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`CREATE TABLE IF NOT EXISTS ` + recordsTable + ` (
	vector_rowid INTEGER PRIMARY KEY AUTOINCREMENT,
	id TEXT NOT NULL UNIQUE,
	source_kind TEXT NOT NULL,
	source_id TEXT NOT NULL,
	session_id TEXT NOT NULL DEFAULT '',
	role TEXT NOT NULL DEFAULT '',
	tool_name TEXT NOT NULL DEFAULT '',
	embedding_model TEXT NOT NULL,
	dimensions INTEGER NOT NULL,
	content_hash TEXT NOT NULL,
	vector BLOB NOT NULL,
	created_at DATETIME NOT NULL,
	updated_at DATETIME NOT NULL
)`).Error; err != nil {
			return fmt.Errorf("failed to create vector records table: %w", err)
		}
		if err := tx.Exec(`CREATE TABLE IF NOT EXISTS ` + recordTagsTable + ` (
	record_id TEXT NOT NULL,
	tag TEXT NOT NULL,
	PRIMARY KEY (record_id, tag),
	FOREIGN KEY (record_id) REFERENCES ` + recordsTable + ` (id) ON DELETE CASCADE
)`).Error; err != nil {
			return fmt.Errorf("failed to create vector record tags table: %w", err)
		}

		indexes := []string{
			`CREATE INDEX IF NOT EXISTS idx_vectors_source ON ` + recordsTable + ` (source_kind, source_id)`,
			`CREATE INDEX IF NOT EXISTS idx_vectors_session ON ` + recordsTable + ` (source_kind, session_id)`,
			`CREATE INDEX IF NOT EXISTS idx_vectors_session_role_tool ON ` + recordsTable + ` (source_kind, session_id, role, tool_name)`,
			`CREATE INDEX IF NOT EXISTS idx_vectors_model_dimensions ON ` + recordsTable + ` (embedding_model, dimensions)`,
			`CREATE INDEX IF NOT EXISTS idx_vectors_content_hash ON ` + recordsTable + ` (content_hash)`,
			`CREATE INDEX IF NOT EXISTS idx_vector_record_tags_tag ON ` + recordTagsTable + ` (tag, record_id)`,
		}
		for _, statement := range indexes {
			if err := tx.Exec(statement).Error; err != nil {
				return fmt.Errorf("failed to create vector records index: %w", err)
			}
		}

		return nil
	}); err != nil {
		return err
	}

	return nil
}

func upsertRecord(tx *gorm.DB, record Record) error {
	blob, err := serialize(record.Vector)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	createdAt := record.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := record.UpdatedAt.UTC()
	if updatedAt.IsZero() {
		updatedAt = now
	}

	existing, ok, err := recordRef(tx, record.ID)
	if err != nil {
		return err
	}
	var rowID int64
	if ok {
		rowID = existing.RowID
		if err := deleteIndexRow(tx, existing.Dimensions, existing.RowID); err != nil {
			return err
		}
		sessionIDValue4 := str.String(record.SessionID)
		roleValue3 := str.String(record.Role)
		toolNameValue3 := str.String(record.ToolName)
		if err := tx.Exec(`UPDATE `+recordsTable+` SET
	source_kind = ?,
	source_id = ?,
	session_id = ?,
	role = ?,
	tool_name = ?,
	embedding_model = ?,
	dimensions = ?,
	content_hash = ?,
	vector = ?,
	updated_at = ?
WHERE id = ?`,
			string(record.SourceKind),
			record.SourceID, sessionIDValue4.
				Trim(), roleValue3.
				Trim(), toolNameValue3.
				Trim(), record.EmbeddingModel,
			record.Dimensions,
			record.ContentHash,
			blob,
			updatedAt,
			record.ID,
		).Error; err != nil {
			return fmt.Errorf("failed to update vector record: %w", err)
		}
	} else {
		sessionIDValue5 := str.String(record.SessionID)
		roleValue4 := str.String(record.Role)
		toolNameValue4 := str.String(record.ToolName)
		if err := tx.Raw(`INSERT INTO `+recordsTable+` (
	id,
	source_kind,
	source_id,
	session_id,
	role,
	tool_name,
	embedding_model,
	dimensions,
	content_hash,
	vector,
	created_at,
	updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING vector_rowid`,
			record.ID,
			string(record.SourceKind),
			record.SourceID, sessionIDValue5.
				Trim(), roleValue4.
				Trim(), toolNameValue4.
				Trim(), record.EmbeddingModel,
			record.Dimensions,
			record.ContentHash,
			blob,
			createdAt,
			updatedAt,
		).Scan(&rowID).Error; err != nil {
			return fmt.Errorf("failed to insert vector record: %w", err)
		}
	}
	sessionIDValue6 := str.String(record.SessionID)
	roleValue5 := str.String(record.Role)
	toolNameValue5 := str.String(record.ToolName)
	if err := tx.Exec(
		`INSERT INTO `+
			getIndexTableName(record.Dimensions)+
			` (
				rowid,
				vector,
				source_kind,
				source_id,
				session_id,
				role,
				tool_name,
				embedding_model
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		rowID,
		blob,
		string(record.SourceKind),
		record.SourceID, sessionIDValue6.
			Trim(), roleValue5.
			Trim(), toolNameValue5.
			Trim(), record.EmbeddingModel,
	).Error; err != nil {
		return fmt.Errorf("failed to insert vector index row: %w", err)
	}
	if err := replaceRecordTags(tx, record.ID, record.Tags); err != nil {
		return err
	}

	return nil
}

func ensureIndexTable(db *gorm.DB, dimensions int) error {
	if dimensions <= 0 {
		return errors.New("vector dimensions must be greater than zero")
	}

	exists, err := indexTableExists(db, dimensions)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	if err := db.Exec(`CREATE VIRTUAL TABLE ` + getIndexTableName(dimensions) + ` USING vec0(
	vector float[` + fmt.Sprintf("%d", dimensions) + `] distance_metric=cosine,
	source_kind TEXT,
	source_id TEXT,
	session_id TEXT,
	role TEXT,
	tool_name TEXT,
	embedding_model TEXT
)`).Error; err != nil {
		return fmt.Errorf("failed to create vector index table: %w", err)
	}

	return nil
}

func deleteIndexRow(tx *gorm.DB, dimensions int, rowID int64) error {
	if dimensions <= 0 || rowID <= 0 {
		return nil
	}

	return deleteIndexRows(tx, dimensions, []int64{rowID})
}

func deleteIndexRows(tx *gorm.DB, dimensions int, getRowIDs []int64) error {
	if dimensions <= 0 || len(getRowIDs) == 0 {
		return nil
	}
	if err := tx.Exec(`DELETE FROM `+getIndexTableName(dimensions)+` WHERE rowid IN ?`, getRowIDs).Error; err != nil {
		return fmt.Errorf("failed to delete vector index row: %w", err)
	}

	return nil
}

func recordRef(tx *gorm.DB, id string) (recordRefRow, bool, error) {
	var row recordRefRow
	err := tx.Raw(
		`SELECT vector_rowid, dimensions FROM `+recordsTable+` WHERE id = ?`,
		id,
	).Scan(&row).Error
	if err != nil {
		return recordRefRow{}, false, fmt.Errorf("failed to load vector record ref: %w", err)
	}
	if row.RowID == 0 {
		return recordRefRow{}, false, nil
	}

	return row, true, nil
}

func vectorRowsForDelete(
	tx *gorm.DB,
	sourceKind SourceKind,
	sourceIDs []string,
	sessionID string,
) ([]recordRefRow, error) {
	var rows []recordRefRow
	query := tx.Table(recordsTable).Select("id, vector_rowid, dimensions").Where("source_kind = ?", string(sourceKind))
	if len(sourceIDs) > 0 {
		query = query.Where("source_id IN ?", sourceIDs)
	}
	if sessionID != "" {
		query = query.Where("session_id = ?", sessionID)
	}

	if err := query.Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to load vector record refs: %w", err)
	}

	return rows, nil
}

func deleteVectorRecords(
	tx *gorm.DB,
	sourceKind SourceKind,
	sourceIDs []string,
	sessionID string,
) error {
	query := tx.Exec(
		getDeleteVectorRecordsSQL(sourceIDs, sessionID),
		getDeleteVectorRecordsArgs(sourceKind, sourceIDs, sessionID)...,
	)

	return query.Error
}

func getDeleteVectorRecordsSQL(sourceIDs []string, sessionID string) string {
	sqlText := `DELETE FROM ` + recordsTable + ` WHERE source_kind = ?`
	if len(sourceIDs) > 0 {
		sqlText += ` AND source_id IN ?`
	}
	if sessionID != "" {
		sqlText += ` AND session_id = ?`
	}

	return sqlText
}

func getDeleteVectorRecordsArgs(sourceKind SourceKind, sourceIDs []string, sessionID string) []any {
	args := []any{string(sourceKind)}
	if len(sourceIDs) > 0 {
		args = append(args, sourceIDs)
	}
	if sessionID != "" {
		args = append(args, sessionID)
	}

	return args
}

func replaceRecordTags(tx *gorm.DB, recordID string, tags []string) error {
	if err := tx.Exec(`DELETE FROM `+recordTagsTable+` WHERE record_id = ?`, recordID).Error; err != nil {
		return fmt.Errorf("failed to delete vector record tags: %w", err)
	}

	for _, tag := range vectorstore.NormalizeTags(tags) {
		if err := tx.Exec(
			`INSERT INTO `+recordTagsTable+` (record_id, tag) VALUES (?, ?)`,
			recordID,
			tag,
		).Error; err != nil {
			return fmt.Errorf("failed to insert vector record tag: %w", err)
		}
	}

	return nil
}

func appendTagFilters(sqlText *strings.Builder, args *[]any, tags []string) {
	for idx, tag := range vectorstore.NormalizeTags(tags) {
		_, _ = fmt.Fprintf(sqlText, `
	AND EXISTS (
		SELECT 1 FROM %s AS vrt%d
		WHERE vrt%d.record_id = rv.id AND vrt%d.tag = ?
	)`, recordTagsTable, idx, idx, idx)
		*args = append(*args, tag)
	}
}

func appendTagFiltersString(sqlText *string, args *[]any, tags []string) {
	var builder strings.Builder
	builder.WriteString(*sqlText)
	appendTagFilters(&builder, args, tags)
	*sqlText = builder.String()
}

func appendTagGroupFilters(sqlText *strings.Builder, args *[]any, groups [][]string) {
	for groupIdx, group := range vectorstore.NormalizeTagGroups(groups) {
		_, _ = fmt.Fprintf(sqlText, `
	AND EXISTS (
		SELECT 1 FROM %s AS vrg%d
		WHERE vrg%d.record_id = rv.id AND vrg%d.tag IN (`, recordTagsTable, groupIdx, groupIdx, groupIdx)
		for tagIdx, tag := range group {
			if tagIdx > 0 {
				sqlText.WriteString(`, `)
			}
			sqlText.WriteString(`?`)
			*args = append(*args, tag)
		}
		sqlText.WriteString(`)
	)`)
	}
}

func appendTagGroupFiltersString(sqlText *string, args *[]any, groups [][]string) {
	var builder strings.Builder
	builder.WriteString(*sqlText)
	appendTagGroupFilters(&builder, args, groups)
	*sqlText = builder.String()
}

func loadRecordTags(db *gorm.DB, getRecordIDs []string) (map[string][]string, error) {
	tagsByID := make(map[string][]string, len(getRecordIDs))
	if len(getRecordIDs) == 0 {
		return tagsByID, nil
	}

	var rows []recordTagRow
	if err := db.Raw(
		`SELECT record_id, tag FROM `+recordTagsTable+` WHERE record_id IN ? ORDER BY record_id ASC, tag ASC`,
		getRecordIDs,
	).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to load vector record tags: %w", err)
	}
	for _, row := range rows {
		tagsByID[row.RecordID] = append(tagsByID[row.RecordID], row.Tag)
	}

	return tagsByID, nil
}

func getRowIDs(rows []searchRow) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		iDValue := str.String(row.ID)
		if iDValue.Trim() != "" {
			ids = append(ids, row.ID)
		}
	}

	return ids
}

func getRecordIDs(rows []recordRefRow) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		iDValue2 := str.String(row.ID)
		if iDValue2.Trim() != "" {
			ids = append(ids, row.ID)
		}
	}

	return ids
}

func normalizeDeleteSourceIDs(req DeleteRequest) []string {
	sourceIDs := make([]string, 0, len(req.SourceIDs))
	seen := make(map[string]struct{}, len(req.SourceIDs))
	for _, sourceID := range req.SourceIDs {
		sourceIDValue := str.String(sourceID)
		sourceID = sourceIDValue.Trim()
		if sourceID != "" {
			if _, ok := seen[sourceID]; ok {
				continue
			}

			seen[sourceID] = struct{}{}
			sourceIDs = append(sourceIDs, sourceID)
		}
	}

	return sourceIDs
}

func serialize(values []float64) ([]byte, error) {
	blob := make([]byte, len(values)*4)
	for idx, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, errors.New("vector value must be finite")
		}

		converted := float32(value)
		if math.IsInf(float64(converted), 0) {
			return nil, errors.New("vector value exceeds float32 range")
		}
		binary.LittleEndian.PutUint32(blob[idx*4:idx*4+4], math.Float32bits(converted))
	}

	return blob, nil
}

func deserialize(blob []byte, dimensions int) ([]float64, error) {
	if dimensions <= 0 {
		return nil, errors.New("vector dimensions must be greater than zero")
	}
	if len(blob) != dimensions*4 {
		return nil, errors.New("vector blob length must match dimensions")
	}

	vector := make([]float64, dimensions)
	for idx := range vector {
		bits := binary.LittleEndian.Uint32(blob[idx*4 : idx*4+4])
		value := math.Float32frombits(bits)
		vector[idx] = float64(value)
	}

	return vector, nil
}

func getIndexTableName(dimensions int) string {
	return fmt.Sprintf("vector_index_%d", dimensions)
}

func indexTableExists(db *gorm.DB, dimensions int) (bool, error) {
	if dimensions <= 0 {
		return false, errors.New("vector dimensions must be greater than zero")
	}

	var count int
	if err := db.Raw(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`,
		getIndexTableName(dimensions),
	).Scan(&count).Error; err != nil {
		return false, fmt.Errorf("failed to check vector index table: %w", err)
	}

	return count > 0, nil
}

func validateSearchSourceIDs(sourceIDs []string) error {
	for _, sourceID := range sourceIDs {
		sourceIDValue2 := str.String(sourceID)
		trimmed := sourceIDValue2.Trim()
		if trimmed == "" {
			return errors.New("vector search filter source id is required")
		}
		if trimmed != sourceID {
			return errors.New("vector search filter source id must be trimmed")
		}
	}

	return nil
}

func withSQLiteBusyRetry(ctx context.Context, fn func() error) error {
	if ctx == nil {
		ctx = context.Background()
	}

	var err error
	for attempt := 0; attempt <= sqliteBusyRetryAttempts; attempt++ {
		if err = fn(); !isSQLiteBusyError(err) {
			return err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if attempt == sqliteBusyRetryAttempts {
			return err
		}

		delay := sqliteBusyRetryDelay * time.Duration(1<<attempt)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}

	return err
}

func isSQLiteBusyError(err error) bool {
	if err == nil {
		return false
	}

	value := strings.ToLower(err.Error())
	return strings.Contains(value, "database is locked") ||
		strings.Contains(value, "database table is locked") ||
		strings.Contains(value, "sqlite_busy") ||
		strings.Contains(value, "sqlite_locked")
}

type recordRefRow struct {
	ID         string `gorm:"column:id"`
	RowID      int64  `gorm:"column:vector_rowid"`
	Dimensions int    `gorm:"column:dimensions"`
}

type recordTagRow struct {
	RecordID string `gorm:"column:record_id"`
	Tag      string `gorm:"column:tag"`
}

type searchRow struct {
	CreatedAt      time.Time `gorm:"column:created_at"`
	UpdatedAt      time.Time `gorm:"column:updated_at"`
	Vector         []byte    `gorm:"column:vector"`
	ID             string    `gorm:"column:id"`
	SourceKind     string    `gorm:"column:source_kind"`
	SourceID       string    `gorm:"column:source_id"`
	SessionID      string    `gorm:"column:session_id"`
	Role           string    `gorm:"column:role"`
	ToolName       string    `gorm:"column:tool_name"`
	EmbeddingModel string    `gorm:"column:embedding_model"`
	ContentHash    string    `gorm:"column:content_hash"`
	RowID          int64     `gorm:"column:vector_rowid"`
	Dimensions     int       `gorm:"column:dimensions"`
	Distance       float64   `gorm:"column:distance"`
}

func (r searchRow) record() (Record, error) {
	vector, err := deserialize(r.Vector, r.Dimensions)
	if err != nil {
		return Record{}, err
	}

	return Record{
		CreatedAt:      r.CreatedAt,
		UpdatedAt:      r.UpdatedAt,
		ID:             r.ID,
		SourceKind:     SourceKind(r.SourceKind),
		SourceID:       r.SourceID,
		SessionID:      r.SessionID,
		Role:           r.Role,
		ToolName:       r.ToolName,
		EmbeddingModel: r.EmbeddingModel,
		ContentHash:    r.ContentHash,
		Vector:         vector,
		Dimensions:     r.Dimensions,
	}, nil
}

var _ vectorstore.Store = (*Store)(nil)
var _ vectorstore.RecordLister = (*Store)(nil)
