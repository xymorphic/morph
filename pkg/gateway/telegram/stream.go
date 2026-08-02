package telegram

import "github.com/xymorphic/morph/pkg/str"

const (
	MessageTextLimit = 4096
	DraftCursor      = "..."
)

func ChunkText(text string, limit int) []string {
	textValue := str.String(text)
	text = textValue.Trim()
	if text == "" {
		return nil
	}
	if limit <= 0 {
		limit = MessageTextLimit
	}

	runes := []rune(text)
	chunks := make([]string, 0, len(runes)/limit+1)
	for len(runes) > 0 {
		n := min(len(runes), limit)
		chunks = append(chunks, string(runes[:n]))
		runes = runes[n:]
	}

	return chunks
}

func SupportsNativeDraft(target Target) bool {
	threadIDValue := str.String(target.ThreadID)
	return target.ChatType == "private" && threadIDValue.Trim() == ""
}

func WithCursor(text string) string {
	textValue2 := str.String(text)
	text = textValue2.Trim()
	if text == "" {
		return DraftCursor
	}

	return text + "\n" + DraftCursor
}
