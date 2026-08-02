package search

import (
	"fmt"

	statememory "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/pkg/str"
)

// MemoryVectorTags builds vector tags for a memory item.
func MemoryVectorTags(item statememory.MemoryItem) []string {
	tags := make([]string, 0, 4+len(item.Tags))
	kindValue := str.String(string(item.Kind))
	if kind := kindValue.Trim(); kind != "" {
		tags = append(tags, MemoryVectorTag("memory_kind", kind))
	}
	statusValue := str.String(string(item.Status))
	if status := statusValue.Trim(); status != "" {
		tags = append(tags, MemoryVectorTag("memory_status", status))
	}
	if sessionID := MemoryVectorSessionID(item); sessionID != "" {
		tags = append(tags, MemoryVectorTag("memory_session", sessionID))
	}
	tags = append(tags, MemoryVectorTag("memory_reflected", fmt.Sprint(item.Reflected)))
	for _, tag := range item.Tags {
		tagValue := str.String(tag)
		if tag = tagValue.Trim(); tag != "" {
			tags = append(tags, MemoryVectorTag("memory_tag", tag))
		}
	}

	return NormalizeVectorTags(tags)
}

// MemoryVectorSessionID extracts the session ID associated with memory vector tags.
func MemoryVectorSessionID(item statememory.MemoryItem) string {
	metadataValue := str.String(item.Metadata["source_session_id"])
	if sessionID := metadataValue.Trim(); sessionID != "" {
		return sessionID
	}
	for _, link := range item.SourceLinks {
		sessionIDValue := str.String(link.SessionID)
		if sessionID := sessionIDValue.Trim(); sessionID != "" {
			return sessionID
		}
	}

	return ""
}

// MemoryVectorTag builds one key/value vector tag.
func MemoryVectorTag(key string, value string) string {
	keyValue := str.String(key)
	valueText := str.String(value)
	return keyValue.Trim() + ":" + valueText.Trim()
}
