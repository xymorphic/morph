package storesqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	statememory "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/internal/state/search"
	"github.com/xymorphic/morph/pkg/nanoid"
	"github.com/xymorphic/morph/pkg/str"
	"gorm.io/gorm"
)

const memorySearchTable = "memory_items_fts"

type memoryItemModel struct {
	ID                   string     `gorm:"column:id;primaryKey"`
	SourceSessionID      string     `gorm:"column:source_session_id;not null;default:'';index:idx_memory_items_source_session_id"`
	Kind                 string     `gorm:"column:kind;not null;default:'';index:idx_memory_items_kind;index:idx_memory_items_kind_status,priority:1"`
	Status               string     `gorm:"column:status;not null;index:idx_memory_items_status;index:idx_memory_items_kind_status,priority:2"`
	Title                string     `gorm:"column:title;not null;default:''"`
	Text                 string     `gorm:"column:text;not null;default:''"`
	TagsJSON             string     `gorm:"column:tags_json;type:TEXT;not null;default:'null'"`
	MetadataJSON         string     `gorm:"column:metadata_json;type:TEXT;not null;default:'null'"`
	SourceLinksJSON      string     `gorm:"column:source_links_json;type:TEXT;not null;default:'null'"`
	Confidence           float64    `gorm:"column:confidence;not null;default:0"`
	Reflected            bool       `gorm:"column:reflected;not null;default:false;index:idx_memory_items_reflected"`
	CreatedAt            time.Time  `gorm:"column:created_at;autoCreateTime:false"`
	UpdatedAt            time.Time  `gorm:"column:updated_at;autoUpdateTime:false;index:idx_memory_items_updated_at"`
	PromotionEvaluatedAt *time.Time `gorm:"column:promotion_evaluated_at;index:idx_memory_items_promotion_evaluated_at"`
}

func (memoryItemModel) TableName() string {
	return "memory_items"
}

type memoryItemTagModel struct {
	MemoryID string `gorm:"column:memory_id;primaryKey"`
	Tag      string `gorm:"column:tag;primaryKey;index:idx_memory_item_tags_tag"`
}

func (memoryItemTagModel) TableName() string {
	return "memory_item_tags"
}

type memorySearchRecord struct {
	ID                   string     `gorm:"column:id"`
	SourceSessionID      string     `gorm:"column:source_session_id"`
	Kind                 string     `gorm:"column:kind"`
	Status               string     `gorm:"column:status"`
	Title                string     `gorm:"column:title"`
	Text                 string     `gorm:"column:text"`
	TagsJSON             string     `gorm:"column:tags_json"`
	MetadataJSON         string     `gorm:"column:metadata_json"`
	SourceLinksJSON      string     `gorm:"column:source_links_json"`
	Confidence           float64    `gorm:"column:confidence"`
	Reflected            bool       `gorm:"column:reflected"`
	CreatedAt            time.Time  `gorm:"column:created_at"`
	UpdatedAt            time.Time  `gorm:"column:updated_at"`
	PromotionEvaluatedAt *time.Time `gorm:"column:promotion_evaluated_at"`
	Score                float64    `gorm:"column:score"`
}

func (record memorySearchRecord) model() memoryItemModel {
	return memoryItemModel{
		ID:                   record.ID,
		SourceSessionID:      record.SourceSessionID,
		Kind:                 record.Kind,
		Status:               record.Status,
		Title:                record.Title,
		Text:                 record.Text,
		TagsJSON:             record.TagsJSON,
		MetadataJSON:         record.MetadataJSON,
		SourceLinksJSON:      record.SourceLinksJSON,
		Confidence:           record.Confidence,
		Reflected:            record.Reflected,
		CreatedAt:            record.CreatedAt,
		UpdatedAt:            record.UpdatedAt,
		PromotionEvaluatedAt: record.PromotionEvaluatedAt,
	}
}

func (s *Store) SearchMemory(ctx context.Context, query statememory.MemorySearchQuery) (statememory.MemorySearchResult, error) {
	if s == nil || s.db == nil {
		return statememory.MemorySearchResult{}, errors.New("store is required")
	}

	if s.memoryVectorSearchEnabled(query) {
		return s.searchMemoryHybrid(ctx, query)
	}

	resultLimit := search.MemoryResultLimit(query.Limit)
	candidateLimit := search.MemoryCandidateLimit(resultLimit)

	records, err := s.searchMemoryRecords(ctx, query, candidateLimit)
	if err != nil {
		return statememory.MemorySearchResult{}, err
	}

	hits := make([]statememory.MemorySearchHit, 0, len(records))
	for _, record := range records {
		item, err := memoryModelToMemoryItem(record.model())
		if err != nil {
			return statememory.MemorySearchResult{}, err
		}

		hits = append(hits, statememory.MemorySearchHit{
			Item:         item.Clone(),
			Score:        record.Score,
			LexicalScore: record.Score,
		})
	}

	return search.RerankMemoryHits(ctx, query, hits, search.MemoryRerankOptions{
		Reranker:      s.memoryReranker,
		MaxCandidates: candidateLimit,
		Limit:         resultLimit,
	})
}

func (s *Store) ListSessionMemories(ctx context.Context, query statememory.SessionMemoryQuery) (statememory.SessionMemoriesResult, error) {
	if s == nil || s.db == nil {
		return statememory.SessionMemoriesResult{}, errors.New("store is required")
	}

	sessionIDValue := str.String(query.SessionID)
	sessionID := sessionIDValue.Trim()
	if err := statememory.ValidateSessionID(sessionID); err != nil {
		return statememory.SessionMemoriesResult{}, err
	}

	statuses := query.Statuses
	if len(statuses) == 0 {
		statuses = []statememory.MemoryStatus{statememory.MemoryStatusActive}
	}

	db := s.db.WithContext(ctx).Model(&memoryItemModel{}).Where("source_session_id = ?", sessionID)
	if len(query.Kinds) > 0 {
		db = db.Where("kind IN ?", statememory.MemoryKindsToStrings(query.Kinds))
	}
	if len(statuses) > 0 {
		db = db.Where("status IN ?", statememory.MemoryStatusesToStrings(statuses))
	}
	if query.Limit > 0 {
		db = db.Limit(query.Limit)
	}

	var records []memoryItemModel
	if err := db.Order("updated_at DESC").Order("id ASC").Find(&records).Error; err != nil {
		return statememory.SessionMemoriesResult{}, err
	}

	items := make([]statememory.MemoryItem, 0, len(records))
	for _, record := range records {
		item, err := memoryModelToMemoryItem(record)
		if err != nil {
			return statememory.SessionMemoriesResult{}, err
		}
		items = append(items, item.Clone())
	}

	return statememory.SessionMemoriesResult{Items: items}, nil
}

func (s *Store) UpsertMemory(ctx context.Context, item statememory.MemoryItem) (statememory.MemoryItem, error) {
	if s == nil || s.db == nil {
		return statememory.MemoryItem{}, errors.New("store is required")
	}

	item = item.Clone()
	now := time.Now().UTC()
	iDValue := str.String(item.ID)
	item.ID = iDValue.Trim()
	if item.ID == "" {
		item.ID = nanoid.MustGenerate("mem_")
	}
	if item.Status == "" {
		item.Status = statememory.MemoryStatusCandidate
	}

	var existing memoryItemModel
	err := s.db.WithContext(ctx).
		Select("created_at").
		First(&existing, "id = ?", item.ID).
		Error
	switch {
	case err == nil:
		item.CreatedAt = existing.CreatedAt.UTC()
	case errors.Is(err, gorm.ErrRecordNotFound):
		if item.CreatedAt.IsZero() {
			item.CreatedAt = now
		} else {
			item.CreatedAt = item.CreatedAt.UTC()
		}
	default:
		return statememory.MemoryItem{}, err
	}

	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now

	record := memoryItemToMemoryModel(item)

	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&record).Error; err != nil {
			return err
		}
		if err := tx.Where("memory_id = ?", item.ID).Delete(&memoryItemTagModel{}).Error; err != nil {
			return err
		}

		for _, tag := range statememory.NormalizeMemoryTags(item.Tags) {
			if err := tx.Create(&memoryItemTagModel{MemoryID: item.ID, Tag: tag}).Error; err != nil {
				return err
			}
		}
		if err := replaceMemorySearchRow(tx, item); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return statememory.MemoryItem{}, err
	}

	if err := s.handleVectorStoreError(s.syncMemoryVector(ctx, item)); err != nil {
		return statememory.MemoryItem{}, err
	}

	return item.Clone(), nil
}

func (s *Store) PatchMemory(ctx context.Context, patch statememory.MemoryPatch) (statememory.MemoryItem, error) {
	if s == nil || s.db == nil {
		return statememory.MemoryItem{}, errors.New("store is required")
	}

	iDValue2 := str.String(patch.ID)
	patch.ID = iDValue2.Trim()
	if patch.ID == "" {
		return statememory.MemoryItem{}, errors.New("memory id is required")
	}

	now := time.Now().UTC()
	var item statememory.MemoryItem
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record memoryItemModel
		if err := tx.First(&record, "id = ?", patch.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("memory item not found")
			}

			return err
		}

		current, err := memoryModelToMemoryItem(record)
		if err != nil {
			return err
		}
		item = statememory.ApplyMemoryPatch(current, patch, now)
		updates := getMemoryPatchUpdates(patch, item)
		if err := tx.Model(&memoryItemModel{}).Where("id = ?", item.ID).Updates(updates).Error; err != nil {
			return err
		}
		if patch.Tags != nil {
			if err := tx.Where("memory_id = ?", item.ID).Delete(&memoryItemTagModel{}).Error; err != nil {
				return err
			}

			for _, tag := range statememory.NormalizeMemoryTags(item.Tags) {
				if err := tx.Create(&memoryItemTagModel{MemoryID: item.ID, Tag: tag}).Error; err != nil {
					return err
				}
			}
		}
		if checkMemoryPatchNeedsSearchRow(patch) {
			if err := replaceMemorySearchRow(tx, item); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return statememory.MemoryItem{}, err
	}

	if err := s.handleVectorStoreError(s.syncMemoryVector(ctx, item)); err != nil {
		return statememory.MemoryItem{}, err
	}

	return item.Clone(), nil
}

func (s *Store) syncMemoryVector(ctx context.Context, item statememory.MemoryItem) error {
	if item.Status == statememory.MemoryStatusDeleted {
		return s.deleteMemoryVector(ctx, item.ID)
	}

	return s.indexMemoryVector(ctx, item)
}

func (s *Store) DeleteMemory(ctx context.Context, req statememory.MemoryDeleteRequest) error {
	if s == nil || s.db == nil {
		return errors.New("store is required")
	}

	iDValue3 := str.String(req.ID)
	id := iDValue3.Trim()
	if id == "" {
		return errors.New("memory id is required")
	}

	result := s.db.WithContext(ctx).
		Model(&memoryItemModel{}).
		Where("id = ?", id).
		Updates(map[string]any{"status": string(statememory.MemoryStatusDeleted), "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return nil
	}

	return s.handleVectorStoreError(s.deleteMemoryVector(ctx, id))
}

func (s *Store) HardDeleteMemory(ctx context.Context, req statememory.MemoryDeleteRequest) error {
	if s == nil || s.db == nil {
		return errors.New("store is required")
	}

	iDValue4 := str.String(req.ID)
	id := iDValue4.Trim()
	if id == "" {
		return errors.New("memory id is required")
	}

	rowsAffected := int64(0)
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("memory_id = ?", id).Delete(&memoryItemTagModel{}).Error; err != nil {
			return err
		}
		if err := deleteMemorySearchRow(tx, id); err != nil {
			return err
		}

		result := tx.Where("id = ?", id).Delete(&memoryItemModel{})
		if result.Error != nil {
			return result.Error
		}
		rowsAffected = result.RowsAffected
		return nil
	}); err != nil {
		return err
	}
	if rowsAffected == 0 {
		return nil
	}

	return s.handleVectorStoreError(s.deleteMemoryVector(ctx, id))
}

func (s *Store) searchMemoryRecords(
	ctx context.Context,
	query statememory.MemorySearchQuery,
	limit int,
) ([]memorySearchRecord, error) {
	statuses := query.Statuses
	if len(statuses) == 0 {
		statuses = []statememory.MemoryStatus{statememory.MemoryStatusActive}
	}
	textValue := str.String(query.Text)
	text := textValue.Trim()
	if text != "" {
		strictQuery := buildFTSSearchQuery(text)
		if strictQuery == "" {
			return nil, nil
		}

		records, err := s.searchMemoryRecordsLexical(ctx, query, statuses, strictQuery, limit)
		if err != nil || len(records) > 0 {
			return records, err
		}

		relaxedQuery := buildRelaxedMemoryFTSSearchQuery(text)
		if relaxedQuery == "" || relaxedQuery == strictQuery {
			return nil, nil
		}

		records, err = s.searchMemoryRecordsLexical(ctx, query, statuses, relaxedQuery, limit)
		if err != nil {
			return nil, err
		}

		return getCoverageRankedMemorySearchRecords(records, text, limit), nil
	}

	db := s.db.WithContext(ctx).Model(&memoryItemModel{})
	sessionIDValue2 := str.String(query.SessionID)
	if sessionID := sessionIDValue2.Trim(); sessionID != "" {
		db = db.Where("source_session_id = ?", sessionID)
	}
	if ids := statememory.NormalizeMemoryIDs(query.IDs); len(ids) > 0 {
		db = db.Where("id IN ?", ids)
	}
	if len(query.Kinds) > 0 {
		db = db.Where("kind IN ?", statememory.MemoryKindsToStrings(query.Kinds))
	}
	if len(statuses) > 0 {
		db = db.Where("status IN ?", statememory.MemoryStatusesToStrings(statuses))
	}
	if tags := statememory.NormalizeMemoryTags(query.Tags); len(tags) > 0 {
		subquery := s.db.WithContext(ctx).
			Model(&memoryItemTagModel{}).
			Select("memory_id").
			Where("tag IN ?", tags).
			Group("memory_id").
			Having("COUNT(DISTINCT tag) = ?", len(tags))
		db = db.Where("id IN (?)", subquery)
	}
	if query.Reflected != nil {
		db = db.Where("reflected = ?", *query.Reflected)
	}
	db = applyPromotionEvaluationFilters(db, query, "")

	var records []memoryItemModel
	if err := db.Order("updated_at DESC").Order("id ASC").Limit(limit).Find(&records).Error; err != nil {
		return nil, err
	}

	searchRecords := make([]memorySearchRecord, 0, len(records))
	for _, record := range records {
		searchRecords = append(searchRecords, memoryModelToSearchRecord(record, 0))
	}

	return searchRecords, nil
}

func (s *Store) searchMemoryRecordsLexical(
	ctx context.Context,
	query statememory.MemorySearchQuery,
	statuses []statememory.MemoryStatus,
	queryText string,
	limit int,
) ([]memorySearchRecord, error) {
	args := []any{queryText}
	var sql strings.Builder

	sql.WriteString(`
WITH fts_hits AS (
	SELECT
		memory_id,
		bm25(`)
	sql.WriteString(memorySearchTable)
	sql.WriteString(`, 0.0, 5.0, 3.0, 1.0, 1.0, 1.0) AS rank
	FROM `)
	sql.WriteString(memorySearchTable)
	sql.WriteString(`
	WHERE `)
	sql.WriteString(memorySearchTable)
	sql.WriteString(` MATCH ?
)
SELECT
	m.id,
	m.source_session_id,
	m.kind,
	m.status,
	m.title,
	m.text,
	m.tags_json,
	m.metadata_json,
	m.source_links_json,
	m.confidence,
	m.reflected,
	m.created_at,
	m.updated_at,
	m.promotion_evaluated_at,
	-hits.rank AS score
FROM memory_items AS m
JOIN fts_hits AS hits ON hits.memory_id = m.id
WHERE 1 = 1`)
	sessionIDValue3 := str.String(query.SessionID)
	if sessionID := sessionIDValue3.Trim(); sessionID != "" {
		sql.WriteString(`
	AND m.source_session_id = ?`)
		args = append(args, sessionID)
	}
	if ids := statememory.NormalizeMemoryIDs(query.IDs); len(ids) > 0 {
		sql.WriteString(`
	AND m.id IN ?`)
		args = append(args, ids)
	}
	if len(query.Kinds) > 0 {
		sql.WriteString(`
	AND m.kind IN ?`)
		args = append(args, statememory.MemoryKindsToStrings(query.Kinds))
	}
	if len(statuses) > 0 {
		sql.WriteString(`
	AND m.status IN ?`)
		args = append(args, statememory.MemoryStatusesToStrings(statuses))
	}
	if tags := statememory.NormalizeMemoryTags(query.Tags); len(tags) > 0 {
		sql.WriteString(`
	AND m.id IN (
		SELECT memory_id
		FROM memory_item_tags
		WHERE tag IN ?
		GROUP BY memory_id
		HAVING COUNT(DISTINCT tag) = ?
	)`)
		args = append(args, tags, len(tags))
	}
	if query.Reflected != nil {
		sql.WriteString(`
	AND m.reflected = ?`)
		args = append(args, *query.Reflected)
	}
	appendPromotionEvaluationSQL(&sql, &args, query, "m.")
	sql.WriteString(`
ORDER BY hits.rank ASC, m.updated_at DESC, m.id ASC
LIMIT ?`)
	args = append(args, limit)

	var records []memorySearchRecord
	if err := s.db.WithContext(ctx).Raw(sql.String(), args...).Scan(&records).Error; err != nil {
		return nil, err
	}
	return records, nil
}

func buildRelaxedMemoryFTSSearchQuery(text string) string {
	tokens := statememory.SearchTokens(text)
	if len(tokens) == 0 {
		return ""
	}

	terms := make([]string, 0, len(tokens))
	for _, token := range tokens {
		prefix := statememory.GetMemorySearchTokenPrefix(token)
		if prefix != "" {
			terms = append(terms, prefix+`*`)
		}
	}

	return strings.Join(terms, " OR ")
}

func getCoverageRankedMemorySearchRecords(records []memorySearchRecord, query string, limit int) []memorySearchRecord {
	if len(records) == 0 {
		return nil
	}

	tokenCount := len(statememory.SearchTokens(query))
	ranked := make([]memorySearchRecord, 0, len(records))
	for _, record := range records {
		coverage := statememory.GetMemorySearchTextCoverageScore(getMemorySearchRecordText(record), query)
		if !statememory.CheckMemorySearchCoveragePasses(coverage, tokenCount) {
			continue
		}
		record.Score += coverage
		ranked = append(ranked, record)
	}
	if len(ranked) == 0 {
		return nil
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		leftCoverage := statememory.GetMemorySearchTextCoverageScore(getMemorySearchRecordText(ranked[i]), query)
		rightCoverage := statememory.GetMemorySearchTextCoverageScore(getMemorySearchRecordText(ranked[j]), query)
		if leftCoverage != rightCoverage {
			return leftCoverage > rightCoverage
		}
		if ranked[i].Score != ranked[j].Score {
			return ranked[i].Score > ranked[j].Score
		}
		if !ranked[i].UpdatedAt.Equal(ranked[j].UpdatedAt) {
			return ranked[i].UpdatedAt.After(ranked[j].UpdatedAt)
		}

		return ranked[i].ID < ranked[j].ID
	})
	if limit > 0 && len(ranked) > limit {
		return ranked[:limit]
	}

	return ranked
}

func getMemorySearchRecordText(record memorySearchRecord) string {
	return strings.Join([]string{
		record.Title,
		record.Text,
		record.Kind,
		record.TagsJSON,
		record.MetadataJSON,
	}, " ")
}

func ensureMemoryStorage(db *gorm.DB) error {
	if db == nil {
		return errors.New("memory db is required")
	}

	if err := db.AutoMigrate(&memoryItemModel{}, &memoryItemTagModel{}); err != nil {
		return fmt.Errorf("failed to migrate memory db: %w", err)
	}

	if err := ensureMemorySearchIndex(db); err != nil {
		return err
	}

	return nil
}

func applyPromotionEvaluationFilters(
	db *gorm.DB,
	query statememory.MemorySearchQuery,
	prefix string,
) *gorm.DB {
	column := prefix + "promotion_evaluated_at"
	if query.PromotionEvaluated != nil {
		if *query.PromotionEvaluated {
			db = db.Where(column + " IS NOT NULL")
		} else {
			db = db.Where(column + " IS NULL")
		}
	}
	if !query.PromotionEvaluatedBefore.IsZero() {
		db = db.Where(column+" IS NOT NULL AND "+column+" < ?", query.PromotionEvaluatedBefore.UTC())
	}
	if !query.PromotionEvaluatedAfter.IsZero() {
		db = db.Where(column+" IS NOT NULL AND "+column+" > ?", query.PromotionEvaluatedAfter.UTC())
	}

	return db
}

func appendPromotionEvaluationSQL(
	sql *strings.Builder,
	args *[]any,
	query statememory.MemorySearchQuery,
	prefix string,
) {
	column := prefix + "promotion_evaluated_at"
	if query.PromotionEvaluated != nil {
		if *query.PromotionEvaluated {
			sql.WriteString(`
	AND `)
			sql.WriteString(column)
			sql.WriteString(` IS NOT NULL`)
		} else {
			sql.WriteString(`
	AND `)
			sql.WriteString(column)
			sql.WriteString(` IS NULL`)
		}
	}
	if !query.PromotionEvaluatedBefore.IsZero() {
		sql.WriteString(`
	AND `)
		sql.WriteString(column)
		sql.WriteString(` IS NOT NULL AND `)
		sql.WriteString(column)
		sql.WriteString(` < ?`)
		*args = append(*args, query.PromotionEvaluatedBefore.UTC())
	}
	if !query.PromotionEvaluatedAfter.IsZero() {
		sql.WriteString(`
	AND `)
		sql.WriteString(column)
		sql.WriteString(` IS NOT NULL AND `)
		sql.WriteString(column)
		sql.WriteString(` > ?`)
		*args = append(*args, query.PromotionEvaluatedAfter.UTC())
	}
}

func ensureMemorySearchIndex(db *gorm.DB) error {
	if db == nil {
		return errors.New("memory db is required")
	}

	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS ` + memorySearchTable + ` USING fts5(
	memory_id UNINDEXED,
	title,
	text,
	kind,
	tags,
	metadata,
	tokenize='unicode61'
)`).Error; err != nil {
			return fmt.Errorf("failed to create memory search index: %w", err)
		}

		return nil
	}); err != nil {
		return err
	}

	return nil
}

func memoryItemToMemoryModel(item statememory.MemoryItem) memoryItemModel {
	promotionEvaluatedAt := getMemoryPromotionEvaluatedAt(item)
	return memoryItemModel{
		ID:                   item.ID,
		SourceSessionID:      getMemorySourceSessionID(item),
		Kind:                 string(item.Kind),
		Status:               string(item.Status),
		Title:                item.Title,
		Text:                 item.Text,
		TagsJSON:             toJSONString(item.Tags),
		MetadataJSON:         toJSONString(item.Metadata),
		SourceLinksJSON:      toJSONString(item.SourceLinks),
		Confidence:           item.Confidence,
		Reflected:            item.Reflected,
		CreatedAt:            item.CreatedAt,
		UpdatedAt:            item.UpdatedAt,
		PromotionEvaluatedAt: promotionEvaluatedAt,
	}
}

func getMemoryPromotionEvaluatedAt(item statememory.MemoryItem) *time.Time {
	if item.PromotionEvaluatedAt.IsZero() {
		return nil
	}

	evaluatedAt := item.PromotionEvaluatedAt.UTC()
	return &evaluatedAt
}

func getMemoryPatchUpdates(patch statememory.MemoryPatch, item statememory.MemoryItem) map[string]any {
	updates := map[string]any{"updated_at": item.UpdatedAt}
	if patch.Kind != nil {
		updates["kind"] = string(item.Kind)
	}
	if patch.Status != nil {
		updates["status"] = string(item.Status)
	}
	if patch.Title != nil {
		updates["title"] = item.Title
	}
	if patch.Text != nil {
		updates["text"] = item.Text
	}
	if patch.Tags != nil {
		updates["tags_json"] = toJSONString(item.Tags)
	}
	if len(patch.Metadata) > 0 {
		updates["metadata_json"] = toJSONString(item.Metadata)
		updates["source_session_id"] = getMemorySourceSessionID(item)
	}
	if patch.SourceLinks != nil {
		updates["source_links_json"] = toJSONString(item.SourceLinks)
		updates["source_session_id"] = getMemorySourceSessionID(item)
	}
	if patch.Confidence != nil {
		updates["confidence"] = item.Confidence
	}
	if patch.Reflected != nil {
		updates["reflected"] = item.Reflected
	}
	if patch.PromotionEvaluatedAt != nil {
		updates["promotion_evaluated_at"] = getMemoryPromotionEvaluatedAt(item)
	}

	return updates
}

func checkMemoryPatchNeedsSearchRow(patch statememory.MemoryPatch) bool {
	return patch.Kind != nil ||
		patch.Title != nil ||
		patch.Text != nil ||
		patch.Tags != nil ||
		len(patch.Metadata) > 0
}

func getMemorySourceSessionID(item statememory.MemoryItem) string {
	metadataValue := str.String(item.Metadata["source_session_id"])
	if sessionID := metadataValue.Trim(); sessionID != "" {
		return sessionID
	}

	for _, link := range item.SourceLinks {
		sessionIDValue4 := str.String(link.SessionID)
		if sessionID := sessionIDValue4.Trim(); sessionID != "" {
			return sessionID
		}
	}

	return ""
}

func memoryModelToMemoryItem(record memoryItemModel) (statememory.MemoryItem, error) {
	item := statememory.MemoryItem{
		ID:         record.ID,
		Kind:       statememory.MemoryKind(record.Kind),
		Status:     statememory.MemoryStatus(record.Status),
		Title:      record.Title,
		Text:       record.Text,
		Confidence: record.Confidence,
		Reflected:  record.Reflected,
		CreatedAt:  record.CreatedAt.UTC(),
		UpdatedAt:  record.UpdatedAt.UTC(),
	}
	if record.PromotionEvaluatedAt != nil {
		item.PromotionEvaluatedAt = record.PromotionEvaluatedAt.UTC()
	}
	if err := fromJSONString(record.TagsJSON, &item.Tags); err != nil {
		return statememory.MemoryItem{}, err
	}
	if err := fromJSONString(record.MetadataJSON, &item.Metadata); err != nil {
		return statememory.MemoryItem{}, err
	}
	if err := fromJSONString(record.SourceLinksJSON, &item.SourceLinks); err != nil {
		return statememory.MemoryItem{}, err
	}
	return item, nil
}

func toJSONString(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "null"
	}
	return string(data)
}

func memoryModelToSearchRecord(record memoryItemModel, score float64) memorySearchRecord {
	return memorySearchRecord{
		ID:                   record.ID,
		SourceSessionID:      record.SourceSessionID,
		Kind:                 record.Kind,
		Status:               record.Status,
		Title:                record.Title,
		Text:                 record.Text,
		TagsJSON:             record.TagsJSON,
		MetadataJSON:         record.MetadataJSON,
		SourceLinksJSON:      record.SourceLinksJSON,
		Confidence:           record.Confidence,
		Reflected:            record.Reflected,
		CreatedAt:            record.CreatedAt,
		UpdatedAt:            record.UpdatedAt,
		PromotionEvaluatedAt: record.PromotionEvaluatedAt,
		Score:                score,
	}
}

func replaceMemorySearchRow(tx *gorm.DB, item statememory.MemoryItem) error {
	if tx == nil {
		return nil
	}

	if err := deleteMemorySearchRow(tx, item.ID); err != nil {
		return err
	}

	if err := tx.Exec(
		`INSERT INTO `+memorySearchTable+` (memory_id, title, text, kind, tags, metadata) VALUES (?, ?, ?, ?, ?, ?)`,
		item.ID,
		item.Title,
		item.Text,
		string(item.Kind),
		strings.Join(statememory.NormalizeMemoryTags(item.Tags), " "),
		getMemoryMetadataSearchText(item.Metadata),
	).Error; err != nil {
		return fmt.Errorf("failed to insert memory search row: %w", err)
	}

	return nil
}

func deleteMemorySearchRow(tx *gorm.DB, memoryID string) error {
	if tx == nil {
		return nil
	}

	if err := tx.Exec(`DELETE FROM `+memorySearchTable+` WHERE memory_id = ?`, memoryID).Error; err != nil {
		return fmt.Errorf("failed to delete memory search row: %w", err)
	}

	return nil
}

func getMemoryMetadataSearchText(metadata map[string]string) string {
	if len(metadata) == 0 {
		return ""
	}

	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	values := make([]string, 0, len(metadata)*2)
	for _, key := range keys {
		values = append(values, key, metadata[key])
	}

	return strings.Join(values, " ")
}

func fromJSONString(raw string, target any) error {
	rawValue := str.String(raw)
	raw = rawValue.Trim()
	if raw == "" {
		raw = "null"
	}
	return json.Unmarshal([]byte(raw), target)
}
