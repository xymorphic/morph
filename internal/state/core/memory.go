package core

import (
	"context"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/xymorphic/morph/pkg/str"
)

// MemoryKind classifies stored memories by use and origin.
type MemoryKind string

const (
	MemoryKindPinned     MemoryKind = "pinned"
	MemoryKindSemantic   MemoryKind = "semantic"
	MemoryKindEpisodic   MemoryKind = "episodic"
	MemoryKindProcedural MemoryKind = "procedural"
)

// MemoryStatus tracks lifecycle state for memory items.
type MemoryStatus string

const (
	MemoryStatusCandidate  MemoryStatus = "candidate"
	MemoryStatusActive     MemoryStatus = "active"
	MemoryStatusSuperseded MemoryStatus = "superseded"
	MemoryStatusDeleted    MemoryStatus = "deleted"
)

// MemorySourceLink describes memory source link.
type MemorySourceLink struct {
	SessionID         string
	MessageIDs        []uint
	Offsets           []int
	SummaryID         string
	CreatedBy         string
	CreatedReason     string
	SourceProfile     string
	SourcePersonality string
	ParentSessionID   string
	ChildSessionID    string
	RunID             string
	StateMode         string
	SourceTrigger     string
}

// MemoryItem represents one memory item.
type MemoryItem struct {
	ID                   string
	Kind                 MemoryKind
	Status               MemoryStatus
	Title                string
	Text                 string
	Tags                 []string
	Metadata             map[string]string
	SourceLinks          []MemorySourceLink
	Confidence           float64
	Reflected            bool
	CreatedAt            time.Time
	UpdatedAt            time.Time
	PromotionEvaluatedAt time.Time
}

// MemoryPatch describes changes to apply to memory state.
type MemoryPatch struct {
	ID                   string
	Kind                 *MemoryKind
	Status               *MemoryStatus
	Title                *string
	Text                 *string
	Tags                 *[]string
	Metadata             map[string]string
	SourceLinks          *[]MemorySourceLink
	Confidence           *float64
	Reflected            *bool
	PromotionEvaluatedAt *time.Time
}

func (item MemoryItem) GuardrailSource() string {
	iDValue := str.String(item.ID)
	if id := iDValue.Trim(); id != "" {
		return "memory:" + id
	}
	return "memory"
}

// MemorySearchQuery describes filters and limits for memory search lookup.
type MemorySearchQuery struct {
	Text                     string
	SessionID                string
	RerankerUseCase          string
	IDs                      []string
	Kinds                    []MemoryKind
	Statuses                 []MemoryStatus
	Tags                     []string
	Limit                    int
	MaxChars                 int
	Reflected                *bool
	PromotionEvaluated       *bool
	PromotionEvaluatedBefore time.Time
	PromotionEvaluatedAfter  time.Time
}

const (
	MemoryRerankerUseCaseDefault            = "memory_search"
	MemoryRerankerUseCaseTurnRetrieval      = "memory_retrieval"
	MemoryRerankerUseCaseToolSearch         = "memory_tool_search"
	MemoryRerankerUseCasePinned             = "memory_pinned"
	MemoryRerankerUseCasePromotion          = "memory_promotion"
	MemoryRerankerUseCaseReflection         = "memory_reflection"
	MemoryRerankerUseCaseEpisodicExtraction = "memory_episodic_extraction"
)

// SessionMemoryQuery describes filters and limits for session memory lookup.
type SessionMemoryQuery struct {
	SessionID string
	Kinds     []MemoryKind
	Statuses  []MemoryStatus
	Limit     int
}

// MemorySearchHit represents one matched memory search result.
type MemorySearchHit struct {
	Item         MemoryItem
	Score        float64
	LexicalScore float64
	VectorScore  float64
}

// MemorySearchResult contains memory hits matched by a search query.
type MemorySearchResult struct {
	Hits []MemorySearchHit
}

// SessionMemoriesResult contains memories linked to a session.
type SessionMemoriesResult struct {
	Items []MemoryItem
}

// MemoryDeleteRequest describes a memory delete request.
type MemoryDeleteRequest struct {
	ID     string
	Reason string
}

// MemoryStore persists and searches memory items.
type MemoryStore interface {
	SearchMemory(context.Context, MemorySearchQuery) (MemorySearchResult, error)
	ListSessionMemories(context.Context, SessionMemoryQuery) (SessionMemoriesResult, error)
	UpsertMemory(context.Context, MemoryItem) (MemoryItem, error)
	PatchMemory(context.Context, MemoryPatch) (MemoryItem, error)
	DeleteMemory(context.Context, MemoryDeleteRequest) error
	HardDeleteMemory(context.Context, MemoryDeleteRequest) error
}

func (item MemoryItem) Clone() MemoryItem {
	item.Tags = append([]string(nil), item.Tags...)
	if len(item.Metadata) > 0 {
		metadata := make(map[string]string, len(item.Metadata))
		maps.Copy(metadata, item.Metadata)
		item.Metadata = metadata
	}
	if len(item.SourceLinks) > 0 {
		links := make([]MemorySourceLink, 0, len(item.SourceLinks))
		for _, link := range item.SourceLinks {
			link.MessageIDs = append([]uint(nil), link.MessageIDs...)
			link.Offsets = append([]int(nil), link.Offsets...)
			links = append(links, link)
		}
		item.SourceLinks = links
	}
	return item
}

// ApplyMemoryPatch applies memory patch.
func ApplyMemoryPatch(item MemoryItem, patch MemoryPatch, updatedAt time.Time) MemoryItem {
	if patch.Kind != nil {
		item.Kind = *patch.Kind
	}
	if patch.Status != nil {
		item.Status = *patch.Status
	}
	if patch.Title != nil {
		item.Title = *patch.Title
	}
	if patch.Text != nil {
		item.Text = *patch.Text
	}
	if patch.Tags != nil {
		item.Tags = append([]string(nil), (*patch.Tags)...)
	}
	if len(patch.Metadata) > 0 {
		if item.Metadata == nil {
			item.Metadata = make(map[string]string, len(patch.Metadata))
		}
		for key, value := range patch.Metadata {
			keyValue := str.String(key)
			if key = keyValue.Trim(); key != "" {
				item.Metadata[key] = value
			}
		}
	}
	if patch.SourceLinks != nil {
		item.SourceLinks = append([]MemorySourceLink(nil), (*patch.SourceLinks)...)
	}
	if patch.Confidence != nil {
		item.Confidence = *patch.Confidence
	}
	if patch.Reflected != nil {
		item.Reflected = *patch.Reflected
	}
	if patch.PromotionEvaluatedAt != nil {
		item.PromotionEvaluatedAt = patch.PromotionEvaluatedAt.UTC()
	}
	item.UpdatedAt = updatedAt.UTC()

	return item.Clone()
}

// NormalizeMemoryTags normalizes memory tags.
func NormalizeMemoryTags(tags []string) []string {
	seen := make(map[string]struct{}, len(tags))
	results := make([]string, 0, len(tags))
	for _, tag := range tags {
		tagValue := str.String(tag)
		tag = tagValue.Normalized()
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		results = append(results, tag)
	}
	sort.Strings(results)
	return results
}

// NormalizeMemoryIDs normalizes memory i ds.
func NormalizeMemoryIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	results := make([]string, 0, len(ids))
	for _, id := range ids {
		idValue := str.String(id)
		id = idValue.Trim()
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		results = append(results, id)
	}
	sort.Strings(results)
	return results
}

// CheckMemoryMatchesQuery checks memory matches query.
func CheckMemoryMatchesQuery(item MemoryItem, query MemorySearchQuery) bool {
	sessionIDValue := str.String(query.SessionID)
	if sessionID := sessionIDValue.Trim(); sessionID != "" &&
		!CheckMemoryBelongsToSession(item, sessionID) {
		return false
	}
	iDValue2 := str.String(item.ID)
	if ids := NormalizeMemoryIDs(query.IDs); len(ids) > 0 &&
		!slices.Contains(ids, iDValue2.Trim()) {
		return false
	}

	if len(query.Kinds) > 0 &&
		!slices.Contains(query.Kinds, item.Kind) {
		return false
	}

	if len(query.Statuses) > 0 {
		if !slices.Contains(query.Statuses, item.Status) {
			return false
		}
	} else if item.Status != MemoryStatusActive {
		return false
	}

	if len(query.Tags) > 0 &&
		!HasAllMemoryTags(item.Tags, query.Tags) {
		return false
	}

	if query.Reflected != nil &&
		item.Reflected != *query.Reflected {
		return false
	}

	if query.PromotionEvaluated != nil {
		evaluated := !item.PromotionEvaluatedAt.IsZero()
		if evaluated != *query.PromotionEvaluated {
			return false
		}
	}

	if !query.PromotionEvaluatedBefore.IsZero() {
		if item.PromotionEvaluatedAt.IsZero() ||
			!item.PromotionEvaluatedAt.Before(query.PromotionEvaluatedBefore) {
			return false
		}
	}

	if !query.PromotionEvaluatedAfter.IsZero() {
		if item.PromotionEvaluatedAt.IsZero() ||
			!item.PromotionEvaluatedAt.After(query.PromotionEvaluatedAfter) {
			return false
		}
	}

	if checkMemoryItemMatchesTextQuery(item, query.Text) {
		return true
	}

	return false
}

// CheckMemoryMatchesSessionQuery checks memory matches session query.
func CheckMemoryMatchesSessionQuery(item MemoryItem, query SessionMemoryQuery) bool {
	sessionIDValue2 := str.String(query.SessionID)
	sessionID := sessionIDValue2.Trim()
	if sessionID == "" || !CheckMemoryBelongsToSession(item, sessionID) {
		return false
	}
	if len(query.Kinds) > 0 && !slices.Contains(query.Kinds, item.Kind) {
		return false
	}
	if len(query.Statuses) > 0 {
		return slices.Contains(query.Statuses, item.Status)
	}

	return item.Status == MemoryStatusActive
}

// CheckMemoryBelongsToSession checks memory belongs to session.
func CheckMemoryBelongsToSession(item MemoryItem, sessionID string) bool {
	sessionIDValue3 := str.String(sessionID)
	sessionID = sessionIDValue3.Trim()
	if sessionID == "" {
		return false
	}

	for _, link := range item.SourceLinks {
		sessionIDValue4 := str.String(link.SessionID)
		if sessionIDValue4.Trim() == sessionID {
			return true
		}
	}
	metadataValue := str.String(item.Metadata["source_session_id"])
	return metadataValue.Trim() == sessionID
}

// GetSimpleMemoryScore returns a lightweight score for memory ranking.
func GetSimpleMemoryScore(item MemoryItem, query string) float64 {
	queryValue := str.String(query)
	query = queryValue.Normalized()
	if query == "" {
		return 0
	}

	score := 0.0
	if strings.Contains(strings.ToLower(item.Title), query) {
		score += 2
	}
	if strings.Contains(strings.ToLower(item.Text), query) {
		score++
	}
	if score > 0 {
		return score
	}

	return GetMemorySearchCoverageScore(item, query)
}

func checkMemoryItemMatchesTextQuery(item MemoryItem, query string) bool {
	queryValue2 := str.String(query)
	query = queryValue2.Normalized()
	if query == "" {
		return true
	}

	text := strings.ToLower(getMemorySearchText(item))
	if strings.Contains(text, query) {
		return true
	}

	return CheckMemorySearchCoveragePasses(
		GetMemorySearchCoverageScore(item, query),
		len(SearchTokens(query)),
	)
}

func GetMemorySearchCoverageScore(item MemoryItem, query string) float64 {
	return GetMemorySearchTextCoverageScore(getMemorySearchText(item), query)
}

func GetMemorySearchTextCoverageScore(text string, query string) float64 {
	tokens := SearchTokens(query)
	if len(tokens) == 0 {
		return 0
	}

	score := 0.0
	text = strings.ToLower(text)
	for _, token := range tokens {
		if checkMemorySearchTokenMatches(text, token) {
			score++
		}
	}

	return score / float64(len(tokens))
}

func CheckMemorySearchCoveragePasses(score float64, tokenCount int) bool {
	if tokenCount == 0 {
		return false
	}
	if tokenCount == 1 {
		return score > 0
	}

	return score >= 0.25
}

func checkMemorySearchTokenMatches(text string, token string) bool {
	if strings.Contains(text, token) {
		return true
	}

	prefix := GetMemorySearchTokenPrefix(token)
	return prefix != token && strings.Contains(text, prefix)
}

func getMemorySearchText(item MemoryItem) string {
	parts := []string{
		item.Title,
		item.Text,
		string(item.Kind),
		strings.Join(item.Tags, " "),
	}
	if len(item.Metadata) > 0 {
		keys := make([]string, 0, len(item.Metadata))
		for key := range item.Metadata {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			parts = append(parts, key, item.Metadata[key])
		}
	}

	return strings.Join(parts, " ")
}

func GetMemorySearchTokenPrefix(token string) string {
	tokenValue := str.String(token)
	token = tokenValue.Normalized()
	runes := []rune(token)
	if len(runes) <= 4 {
		return token
	}
	if len(runes) <= 6 {
		return string(runes[:4])
	}

	return string(runes[:5])
}

func SearchTokens(query string) []string {
	queryValue3 := str.String(query)
	fields := strings.FieldsFunc(queryValue3.Trim(), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if len(fields) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(fields))
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		fieldValue := str.String(field)
		field = fieldValue.Normalized()
		if len([]rune(field)) < 3 {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		tokens = append(tokens, field)
	}

	return tokens
}

// HasAllMemoryTags reports whether tags contain every required tag.
func HasAllMemoryTags(itemTags []string, queryTags []string) bool {
	tags := make(map[string]struct{}, len(itemTags))
	for _, tag := range itemTags {
		tagValue := str.String(tag)
		tags[tagValue.Normalized()] = struct{}{}
	}
	for _, tag := range queryTags {
		tagValue2 := str.String(tag)
		if _, ok := tags[tagValue2.Normalized()]; !ok {
			return false
		}
	}

	return true
}

// MemoryKindsToStrings converts memory kinds to their string values.
func MemoryKindsToStrings(kinds []MemoryKind) []string {
	values := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		kindValue := str.String(string(kind))
		value := kindValue.Trim()
		if value != "" {
			values = append(values, value)
		}
	}

	return values
}

// MemoryStatusesToStrings converts memory statuses to their string values.
func MemoryStatusesToStrings(statuses []MemoryStatus) []string {
	values := make([]string, 0, len(statuses))
	for _, status := range statuses {
		statusValue := str.String(string(status))
		value := statusValue.Trim()
		if value != "" {
			values = append(values, value)
		}
	}

	return values
}

// MemoryValueToLikePattern escapes a memory value for SQL LIKE matching.
func MemoryValueToLikePattern(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + replacer.Replace(value) + "%"
}
