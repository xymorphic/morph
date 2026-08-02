package memory

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"slices"
	"strings"
	"sync"

	"github.com/coder/hnsw"

	"github.com/xymorphic/morph/internal/state/search/vectorstore"
	"github.com/xymorphic/morph/pkg/str"
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

// Store keeps vector records in process memory.
type Store struct {
	mu      sync.RWMutex
	records map[string]Record
	indexes map[indexKey]*hnsw.Graph[string]
}

// NewStore returns a store backed by the supplied dependencies.
func NewStore() *Store {
	return &Store{
		records: make(map[string]Record),
		indexes: make(map[indexKey]*hnsw.Graph[string]),
	}
}

func (s *Store) Upsert(_ context.Context, records []Record) error {
	if s == nil {
		return errors.New("vector store is required")
	}
	if len(records) == 0 {
		return nil
	}

	cloned := make([]Record, 0, len(records))
	for _, record := range records {
		if err := vectorstore.ValidateRecord(record); err != nil {
			return err
		}
		if _, err := float32Vector(record.Vector); err != nil {
			return err
		}

		cloned = append(cloned, cloneRecord(record))
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.records == nil {
		s.records = make(map[string]Record)
	}
	if s.indexes == nil {
		s.indexes = make(map[indexKey]*hnsw.Graph[string])
	}
	for _, record := range cloned {
		if existing, ok := s.records[record.ID]; ok {
			s.removeFromIndex(existing)
		}
		s.records[record.ID] = record
		s.addToIndex(record)
	}

	return nil
}

func (s *Store) Delete(_ context.Context, req DeleteRequest) error {
	if s == nil {
		return errors.New("vector store is required")
	}
	if err := vectorstore.ValidateDeleteRequest(req); err != nil {
		return err
	}

	sourceIDs := sourceIDsToSet(req.SourceIDs)
	sessionIDValue := str.String(req.SessionID)
	sessionID := sessionIDValue.Trim()
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, record := range s.records {
		if record.SourceKind != req.SourceKind {
			continue
		}

		if len(sourceIDs) > 0 {
			if _, ok := sourceIDs[record.SourceID]; !ok {
				continue
			}
		}
		if sessionID != "" && record.SessionID != sessionID {
			continue
		}

		delete(s.records, id)
		s.removeFromIndex(record)
	}

	return nil
}

func (s *Store) Search(_ context.Context, req SearchRequest) (SearchResult, error) {
	if s == nil {
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
	queryVector, err := float32Vector(req.QueryVector)
	if err != nil {
		return SearchResult{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	embeddingModelValue := str.String(req.EmbeddingModel)
	sessionIDValue2 := str.String(req.Filter.SessionID)
	ignoreSessionIDValue := str.String(req.Filter.IgnoreSessionID)
	roleValue := str.String(req.Filter.Role)
	toolNameValue := str.String(req.Filter.ToolName)
	filter := searchFilter{
		embeddingModel:  embeddingModelValue.Trim(),
		dimensions:      req.Dimensions,
		sourceKind:      req.Filter.SourceKind,
		sourceIDs:       sourceIDsToSet(req.Filter.SourceIDs),
		sessionID:       sessionIDValue2.Trim(),
		ignoreSessionID: ignoreSessionIDValue.Trim(),
		role:            roleValue.Trim(),
		toolName:        toolNameValue.Trim(),
		tags:            tagsToSet(req.Filter.Tags),
		tagGroups:       tagGroups(req.Filter.TagGroups),
	}

	graph, records := s.searchGraph(filter)
	if graph == nil || graph.Len() == 0 {
		return SearchResult{}, nil
	}

	nodes := graph.Search(queryVector, min(req.Limit, graph.Len()))
	matches := make([]SearchMatch, 0, len(nodes))
	for _, node := range nodes {
		record := records[node.Key]
		matches = append(matches, SearchMatch{
			Record: cloneRecord(record),
			Score:  scoreFromDistance(cosineDistance(queryVector, node.Value)),
		})
	}
	slices.SortStableFunc(matches, compareMatches)
	return SearchResult{Matches: matches}, nil
}

func (s *Store) Metadata(context.Context) (StoreMetadata, error) {
	if s == nil {
		return StoreMetadata{}, errors.New("vector store is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	counts := make(map[string]ModelMetadata)
	for _, record := range s.records {
		key := getModelMetadataKey(record.EmbeddingModel, record.Dimensions)
		metadata := counts[key]
		metadata.Model = record.EmbeddingModel
		metadata.Dimensions = record.Dimensions
		metadata.Count++
		counts[key] = metadata
	}

	models := make([]ModelMetadata, 0, len(counts))
	for _, metadata := range counts {
		models = append(models, metadata)
	}
	slices.SortStableFunc(models, compareModelMetadata)

	return StoreMetadata{Models: models}, nil
}

func (s *Store) List(_ context.Context, req ListRequest) (ListResult, error) {
	if s == nil {
		return ListResult{}, errors.New("vector store is required")
	}
	if err := vectorstore.ValidateListRequest(req); err != nil {
		return ListResult{}, err
	}

	embeddingModelValue2 := str.String(req.EmbeddingModel)
	sessionIDValue3 := str.String(req.Filter.SessionID)
	ignoreSessionIDValue2 := str.String(req.Filter.IgnoreSessionID)
	roleValue2 := str.String(req.Filter.Role)
	toolNameValue2 := str.String(req.Filter.ToolName)
	filter := searchFilter{
		embeddingModel:  embeddingModelValue2.Trim(),
		sourceKind:      req.Filter.SourceKind,
		sourceIDs:       sourceIDsToSet(req.Filter.SourceIDs),
		sessionID:       sessionIDValue3.Trim(),
		ignoreSessionID: ignoreSessionIDValue2.Trim(),
		role:            roleValue2.Trim(),
		toolName:        toolNameValue2.Trim(),
		tags:            tagsToSet(req.Filter.Tags),
		tagGroups:       tagGroups(req.Filter.TagGroups),
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	records := make([]Record, 0)
	for _, record := range s.records {
		if checkRecordMatchesList(record, filter) {
			records = append(records, cloneRecord(record))
		}
	}
	slices.SortStableFunc(records, compareRecords)

	return ListResult{Records: records}, nil
}

type searchFilter struct {
	embeddingModel  string
	dimensions      int
	sourceKind      SourceKind
	sourceIDs       map[string]struct{}
	sessionID       string
	ignoreSessionID string
	role            string
	toolName        string
	tags            map[string]struct{}
	tagGroups       []map[string]struct{}
}

type indexKey struct {
	model      string
	dimensions int
	sourceKind SourceKind
}

func (f searchFilter) indexKey() indexKey {
	return indexKey{
		model:      f.embeddingModel,
		dimensions: f.dimensions,
		sourceKind: f.sourceKind,
	}
}

func (f searchFilter) usesOnlyIndexFilters() bool {
	return len(f.sourceIDs) == 0 &&
		f.sessionID == "" &&
		f.ignoreSessionID == "" &&
		f.role == "" &&
		f.toolName == "" &&
		len(f.tags) == 0 &&
		len(f.tagGroups) == 0
}

func (s *Store) searchGraph(filter searchFilter) (*hnsw.Graph[string], map[string]Record) {
	if filter.usesOnlyIndexFilters() {
		return s.indexes[filter.indexKey()], s.records
	}

	records := make([]Record, 0)
	for _, record := range s.records {
		if recordMatchesSearch(record, filter) {
			records = append(records, record)
		}
	}
	if len(records) == 0 {
		return nil, nil
	}
	slices.SortStableFunc(records, compareRecords)

	graph := newGraph()
	recordsByID := make(map[string]Record, len(records))
	for _, record := range records {
		vector, _ := float32Vector(record.Vector)
		recordsByID[record.ID] = record
		graph.Add(hnsw.MakeNode(record.ID, vector))
	}

	return graph, recordsByID
}

func recordMatchesSearch(record Record, filter searchFilter) bool {
	if record.EmbeddingModel != filter.embeddingModel {
		return false
	}
	if record.Dimensions != filter.dimensions {
		return false
	}
	if record.SourceKind != filter.sourceKind {
		return false
	}

	if len(filter.sourceIDs) > 0 {
		if _, ok := filter.sourceIDs[record.SourceID]; !ok {
			return false
		}
	}
	if filter.sessionID != "" && record.SessionID != filter.sessionID {
		return false
	}
	if filter.ignoreSessionID != "" && record.SessionID == filter.ignoreSessionID {
		return false
	}
	if filter.role != "" && record.Role != filter.role {
		return false
	}
	if filter.toolName != "" && record.ToolName != filter.toolName {
		return false
	}
	if !recordHasTags(record, filter.tags) {
		return false
	}
	if !recordHasTagGroups(record, filter.tagGroups) {
		return false
	}
	return true
}

func checkRecordMatchesList(record Record, filter searchFilter) bool {
	if record.EmbeddingModel != filter.embeddingModel {
		return false
	}
	if filter.sourceKind != "" && record.SourceKind != filter.sourceKind {
		return false
	}

	if len(filter.sourceIDs) > 0 {
		if _, ok := filter.sourceIDs[record.SourceID]; !ok {
			return false
		}
	}
	if filter.sessionID != "" && record.SessionID != filter.sessionID {
		return false
	}
	if filter.ignoreSessionID != "" && record.SessionID == filter.ignoreSessionID {
		return false
	}
	if filter.role != "" && record.Role != filter.role {
		return false
	}
	if filter.toolName != "" && record.ToolName != filter.toolName {
		return false
	}
	if !recordHasTags(record, filter.tags) {
		return false
	}
	if !recordHasTagGroups(record, filter.tagGroups) {
		return false
	}

	return true
}

func (s *Store) addToIndex(record Record) {
	vector, _ := float32Vector(record.Vector)
	embeddingModelValue3 := str.String(record.EmbeddingModel)
	key := indexKey{
		model:      embeddingModelValue3.Trim(),
		dimensions: record.Dimensions,
		sourceKind: record.SourceKind,
	}
	graph := s.indexes[key]
	if graph == nil {
		graph = newGraph()
		s.indexes[key] = graph
	}
	graph.Add(hnsw.MakeNode(record.ID, vector))
}

func (s *Store) removeFromIndex(record Record) {
	embeddingModelValue4 := str.String(record.EmbeddingModel)
	key := indexKey{
		model:      embeddingModelValue4.Trim(),
		dimensions: record.Dimensions,
		sourceKind: record.SourceKind,
	}
	graph := s.indexes[key]
	if graph == nil {
		return
	}
	graph.Delete(record.ID)
	if graph.Len() == 0 {
		delete(s.indexes, key)
	}
}

func validateSearchSourceIDs(sourceIDs []string) error {
	for _, sourceID := range sourceIDs {
		sourceIDValue := str.String(sourceID)
		trimmed := sourceIDValue.Trim()
		if trimmed == "" {
			return errors.New("vector search filter source id is required")
		}
		if trimmed != sourceID {
			return errors.New("vector search filter source id must be trimmed")
		}
	}

	return nil
}

func sourceIDsToSet(sourceIDs []string) map[string]struct{} {
	set := make(map[string]struct{}, len(sourceIDs))
	for _, sourceID := range sourceIDs {
		sourceIDValue2 := str.String(sourceID)
		sourceID = sourceIDValue2.Trim()
		if sourceID != "" {
			set[sourceID] = struct{}{}
		}
	}

	return set
}

func tagsToSet(tags []string) map[string]struct{} {
	tags = vectorstore.NormalizeTags(tags)
	set := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		set[tag] = struct{}{}
	}

	return set
}

func tagGroups(groups [][]string) []map[string]struct{} {
	groups = vectorstore.NormalizeTagGroups(groups)
	normalized := make([]map[string]struct{}, 0, len(groups))
	for _, group := range groups {
		normalized = append(normalized, tagsToSet(group))
	}

	return normalized
}

func recordHasTags(record Record, tags map[string]struct{}) bool {
	if len(tags) == 0 {
		return true
	}

	recordTags := tagsToSet(record.Tags)
	for tag := range tags {
		if _, ok := recordTags[tag]; !ok {
			return false
		}
	}

	return true
}

func recordHasTagGroups(record Record, groups []map[string]struct{}) bool {
	if len(groups) == 0 {
		return true
	}

	recordTags := tagsToSet(record.Tags)
	for _, group := range groups {
		matched := false
		for tag := range group {
			if _, ok := recordTags[tag]; ok {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	return true
}

func newGraph() *hnsw.Graph[string] {
	graph := hnsw.NewGraph[string]()
	graph.Distance = cosineDistance
	graph.Rng = rand.New(rand.NewSource(1))

	return graph
}

func float32Vector(values []float64) ([]float32, error) {
	vector := make([]float32, len(values))
	for idx, value := range values {
		converted := float32(value)
		if math.IsInf(float64(converted), 0) {
			return nil, errors.New("vector value exceeds float32 range")
		}
		vector[idx] = converted
	}

	return vector, nil
}

func cosineDistance(left []float32, right []float32) float32 {
	distance := hnsw.CosineDistance(left, right)
	if math.IsNaN(float64(distance)) || math.IsInf(float64(distance), 0) {
		return 1
	}

	return distance
}

func scoreFromDistance(distance float32) float64 {
	return 1 - float64(distance)
}

func compareMatches(left SearchMatch, right SearchMatch) int {
	if left.Score > right.Score {
		return -1
	}
	if left.Score < right.Score {
		return 1
	}

	return strings.Compare(left.Record.ID, right.Record.ID)
}

func compareModelMetadata(left ModelMetadata, right ModelMetadata) int {
	if left.Model != right.Model {
		return strings.Compare(left.Model, right.Model)
	}
	if left.Dimensions < right.Dimensions {
		return -1
	}
	if left.Dimensions > right.Dimensions {
		return 1
	}

	return 0
}

func compareRecords(left Record, right Record) int {
	return strings.Compare(left.ID, right.ID)
}

func cloneRecord(record Record) Record {
	record.Vector = append([]float64(nil), record.Vector...)
	tags := record.Tags
	record.Tags = nil
	if normalized := vectorstore.NormalizeTags(tags); len(normalized) > 0 {
		record.Tags = normalized
	}
	return record
}

func getModelMetadataKey(model string, dimensions int) string {
	modelValue := str.String(model)
	return fmt.Sprintf("%s:%d", modelValue.Trim(), dimensions)
}

var _ vectorstore.Store = (*Store)(nil)
