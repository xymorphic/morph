package search

import "github.com/xymorphic/morph/internal/state/search/vectorstore"

// SourceKind classifies the domain object represented by a vector source ID.
type SourceKind = vectorstore.SourceKind

const (
	SourceKindSessionMessage = vectorstore.SourceKindSessionMessage
	SourceKindMemoryItem     = vectorstore.SourceKindMemoryItem
)

// VectorStore aliases vectorstore.Store at this package boundary.
type VectorStore = vectorstore.Store

// VectorRecordLister aliases vectorstore.RecordLister at this package boundary.
type VectorRecordLister = vectorstore.RecordLister

// VectorRecord aliases vectorstore.Record at this package boundary.
type VectorRecord = vectorstore.Record

// VectorDeleteRequest aliases vectorstore.DeleteRequest at this package boundary.
type VectorDeleteRequest = vectorstore.DeleteRequest

// VectorSearchRequest aliases vectorstore.SearchRequest at this package boundary.
type VectorSearchRequest = vectorstore.SearchRequest

// VectorFilter aliases vectorstore.Filter at this package boundary.
type VectorFilter = vectorstore.Filter

// VectorSearchResult aliases vectorstore.SearchResult at this package boundary.
type VectorSearchResult = vectorstore.SearchResult

// VectorListRequest aliases vectorstore.ListRequest at this package boundary.
type VectorListRequest = vectorstore.ListRequest

// VectorListResult aliases vectorstore.ListResult at this package boundary.
type VectorListResult = vectorstore.ListResult

// VectorSearchMatch aliases vectorstore.SearchMatch at this package boundary.
type VectorSearchMatch = vectorstore.SearchMatch

// VectorStoreMetadata aliases vectorstore.StoreMetadata at this package boundary.
type VectorStoreMetadata = vectorstore.StoreMetadata

// VectorModelMetadata aliases vectorstore.ModelMetadata at this package boundary.
type VectorModelMetadata = vectorstore.ModelMetadata

// ValidateVectorRecord validates records accepted by the vector store alias package.
var ValidateVectorRecord = vectorstore.ValidateRecord

// ValidateVectorSearchRequest validates search requests accepted by the vector store alias package.
var ValidateVectorSearchRequest = vectorstore.ValidateSearchRequest

// ValidateVectorListRequest validates list requests accepted by the vector store alias package.
var ValidateVectorListRequest = vectorstore.ValidateListRequest

// ValidateVectorDeleteRequest validates delete requests accepted by the vector store alias package.
var ValidateVectorDeleteRequest = vectorstore.ValidateDeleteRequest

// NormalizeVectorTags normalizes vector tags through the underlying vector store package.
var NormalizeVectorTags = vectorstore.NormalizeTags

// NormalizeVectorTagGroups normalizes vector tag groups through the underlying vector store package.
var NormalizeVectorTagGroups = vectorstore.NormalizeTagGroups

// VectorContentHash returns a stable hash for vectorized content.
func VectorContentHash(text string) string {
	return vectorstore.ContentHash(text)
}

// IsVectorRecordStale reports whether vector metadata differs from the expected model or content hash.
func IsVectorRecordStale(record VectorRecord, text string) bool {
	return vectorstore.IsRecordStale(record, text)
}
