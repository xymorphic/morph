package tui

import tuitranscript "github.com/xymorphic/morph/internal/tui/transcript"

type renderedTranscriptDocument = tuitranscript.RenderedDocument

func newRenderedTranscriptDocument(content string) renderedTranscriptDocument {
	return tuitranscript.NewRenderedDocument(content)
}
