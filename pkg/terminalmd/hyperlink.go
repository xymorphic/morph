package terminalmd

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/xymorphic/morph/pkg/str"
)

func (r *Renderer) renderHyperlink(text string, destination string) string {
	destination = sanitizeHyperlinkDestination(destination)
	if !r.opts.EnableHyperlinks || destination == "" || !isTerminalHyperlinkDestination(destination) {
		return text
	}

	return ansi.SetHyperlink(destination) + text + ansi.ResetHyperlink()
}

// normalizeCommonMarkdownArtifacts converts common model-produced markdown-like
// text into standard markdown before parsing.
//
// Models often emit Unicode bullets directly ("• item"). CommonMark does not
// treat those as list markers, so normalize them to "- item" and let the list
func sanitizeHyperlinkDestination(destination string) string {
	destinationValue := str.String(destination)
	destination = destinationValue.Trim()
	if destination == "" {
		return ""
	}

	var builder strings.Builder
	builder.Grow(len(destination))
	for _, char := range destination {
		switch char {
		case '\x1b', '\a', '\n', '\r':
			continue
		default:
			builder.WriteRune(char)
		}
	}

	return builder.String()
}

// isTerminalHyperlinkDestination allows only URL schemes that are useful and
// expected in terminals. Relative paths still render as styled link text, but do
// not become OSC 8 hyperlinks.
func isTerminalHyperlinkDestination(destination string) bool {
	lower := strings.ToLower(destination)
	return strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "mailto:")
}

// wrapANSI wraps styled text to a terminal-cell width.
//
// firstPrefix is used only for the first rendered line. restPrefix is used for
// subsequent wrapped lines and for explicit line breaks. Prefix widths are
