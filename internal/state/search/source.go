package search

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/xymorphic/morph/internal/state/search/vectorstore"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/str"
)

// StableSessionMessageID returns the stable vector source ID for a session message.
func StableSessionMessageID(sessionID string, messageID uint) string {
	sessionIDValue := str.String(sessionID)
	return fmt.Sprintf("%s:%s:%d", SourceKindSessionMessage, sessionIDValue.Trim(), messageID)
}

// StableMemoryItemID returns the stable vector source ID for a memory item.
func StableMemoryItemID(memoryID string) string {
	memoryIDValue := str.String(memoryID)
	return fmt.Sprintf("%s:%s", SourceKindMemoryItem, memoryIDValue.Trim())
}

// MemoryIDFromSourceID extracts a memory ID from a vector source ID.
func MemoryIDFromSourceID(sourceID string) (string, bool) {
	value, ok := strings.CutPrefix(sourceID, string(SourceKindMemoryItem)+":")
	if !ok {
		return "", false
	}
	valueText := str.String(value).Trim()
	if valueText == "" {
		return "", false
	}

	return valueText, true
}

// SourceIDForMessage returns the vector source ID for a session message.
func SourceIDForMessage(sessionID string, messageID uint) string {
	sessionIDValue2 := str.String(sessionID)
	return StableSessionMessageID(sessionIDValue2.Trim(), messageID)
}

// SourceIDsFromMessages returns vector source IDs for session messages.
func SourceIDsFromMessages(sessionID string, messages []morphmsg.Message) []string {
	if len(messages) == 0 {
		return nil
	}

	sourceIDs := make([]string, 0, len(messages))
	for _, message := range messages {
		sourceIDs = append(sourceIDs, SourceIDForMessage(sessionID, message.ID))
	}

	return sourceIDs
}

// MessageRefFromSourceID extracts a session/message reference from a vector source ID.
func MessageRefFromSourceID(sourceID string) (string, uint, bool) {
	value, ok := strings.CutPrefix(sourceID, string(SourceKindSessionMessage)+":")
	if !ok {
		return "", 0, false
	}
	idx := strings.LastIndex(value, ":")
	if idx <= 0 || idx == len(value)-1 {
		return "", 0, false
	}
	messageID, err := strconv.ParseUint(value[idx+1:], 10, 64)
	if err != nil || messageID == 0 {
		return "", 0, false
	}

	return value[:idx], uint(messageID), true
}

func validateOptionalSourceKind(sourceKind SourceKind, field string) error {
	return vectorstore.ValidateOptionalSourceKind(sourceKind, field)
}
