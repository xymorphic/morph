package search

import (
	"strings"
	"unicode/utf8"

	"github.com/xymorphic/morph/internal/constants"
)

type VectorChunkOptions struct {
	MaxInputBytes    int
	MaxDocumentBytes int
}

func NormalizeVectorChunkOptions(options VectorChunkOptions) VectorChunkOptions {
	if options.MaxInputBytes <= 0 {
		options.MaxInputBytes = constants.DefaultVectorMaxInputBytes
	}
	if options.MaxDocumentBytes <= 0 {
		options.MaxDocumentBytes = constants.DefaultVectorMaxDocumentBytes
	}
	return options
}

func ChunkVectorText(text string, options VectorChunkOptions) ([]string, bool) {
	options = NormalizeVectorChunkOptions(options)
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, false
	}

	truncated := len(text) > options.MaxDocumentBytes
	if truncated {
		text = truncateUTF8(text, options.MaxDocumentBytes)
	}

	chunks := make([]string, 0, (len(text)/options.MaxInputBytes)+1)
	for text != "" {
		if len(text) <= options.MaxInputBytes {
			if chunk := strings.TrimSpace(text); chunk != "" {
				chunks = append(chunks, chunk)
			}
			break
		}

		limit := utf8Boundary(text, options.MaxInputBytes)
		if limit == 0 {
			_, limit = utf8.DecodeRuneInString(text)
		}
		cut := getPreferredChunkBoundary(text[:limit])
		if cut <= 0 {
			cut = limit
		}
		if chunk := strings.TrimSpace(text[:cut]); chunk != "" {
			chunks = append(chunks, chunk)
		}
		text = strings.TrimLeft(text[cut:], " \t\r\n")
	}

	return chunks, truncated
}

func getPreferredChunkBoundary(text string) int {
	for _, separator := range []string{"\n\n", "\n", "\r", "\t", " "} {
		if index := strings.LastIndex(text, separator); index >= 0 {
			return index + len(separator)
		}
	}
	return 0
}

func truncateUTF8(text string, maxBytes int) string {
	if len(text) <= maxBytes {
		return text
	}
	boundary := utf8Boundary(text, maxBytes)
	if boundary == 0 {
		_, boundary = utf8.DecodeRuneInString(text)
	}
	return text[:boundary]
}

func utf8Boundary(text string, maxBytes int) int {
	if maxBytes >= len(text) {
		return len(text)
	}
	if maxBytes <= 0 {
		return 0
	}
	for maxBytes > 0 && !utf8.RuneStart(text[maxBytes]) {
		maxBytes--
	}
	return maxBytes
}
