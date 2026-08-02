package slack

import (
	"strings"

	"github.com/xymorphic/morph/pkg/str"
)

const MarkdownTextLimit = 12000

type PostMessageRequest struct {
	Channel  string `json:"channel"`
	Text     string `json:"text"`
	ThreadTS string `json:"thread_ts,omitempty"`
}

type StartStreamRequest struct {
	Channel         string  `json:"channel"`
	ThreadTS        string  `json:"thread_ts"`
	Chunks          []Chunk `json:"chunks,omitempty"`
	RecipientUserID string  `json:"recipient_user_id,omitempty"`
	RecipientTeamID string  `json:"recipient_team_id,omitempty"`
}

type AppendStreamRequest struct {
	Channel string  `json:"channel"`
	TS      string  `json:"ts"`
	Chunks  []Chunk `json:"chunks,omitempty"`
}

type StopStreamRequest struct {
	Channel string  `json:"channel"`
	TS      string  `json:"ts"`
	Chunks  []Chunk `json:"chunks,omitempty"`
}

type Stream struct {
	ChannelID string
	TS        string
}

type Chunk struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

func MarkdownTextChunk(text string) Chunk {
	return Chunk{Type: "markdown_text", Text: text}
}

func FencedCodeChunk(text string) Chunk {
	return MarkdownTextChunk("```\n" + ensureTrailingNewline(text) + "```")
}

func ensureTrailingNewline(text string) string {
	if strings.HasSuffix(text, "\n") {
		return text
	}

	return text + "\n"
}

func ChunkMarkdown(text string, limit int) []string {
	textValue := str.String(text)
	text = textValue.Trim()
	if text == "" {
		return nil
	}
	if limit <= 0 {
		limit = MarkdownTextLimit
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
