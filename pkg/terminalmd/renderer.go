package terminalmd

import (
	"github.com/xymorphic/morph/pkg/str"
	"github.com/yuin/goldmark"
	emoji "github.com/yuin/goldmark-emoji"
	"github.com/yuin/goldmark/extension"
	goldmermaid "go.abhg.dev/goldmark/mermaid"
)

const defaultWidth = 80

// Style describes the small styling vocabulary terminalmd needs from a theme.
//
// The renderer keeps this separate from lipgloss.Style so callers can pass a
// plain data object, and so the package can decide exactly how style fields map
// to terminal escape sequences.
type Style struct {
	Foreground string
	Background string
	Bold       bool
	Italic     bool
	Underline  bool
	Strike     bool
}

// Theme groups the styles used by each markdown surface the renderer emits.
//
// These are intentionally semantic names rather than markdown-node names. For
// example, Muted is used for table labels and separators, while Text is used as
// the base style for emphasis and strikethrough.
type Theme struct {
	Text        Style
	Muted       Style
	Heading     Style
	Code        Style
	CodeBlock   Style
	Link        Style
	QuoteMarker Style
	TableBorder Style
}

// Options controls terminal markdown rendering.
//
// Width is measured in terminal cells, not bytes or runes. ANSI escape
// sequences are ignored when wrapping and measuring.
//
// EnableHyperlinks turns markdown links into OSC 8 terminal hyperlinks. Keep it
// disabled for logs or output streams where raw escape sequences are undesirable.
type Options struct {
	Width            int
	Theme            Theme
	SyntaxTheme      string
	SyntaxFormatter  string
	EnableHyperlinks bool
}

// Renderer converts Markdown to terminal-friendly plain text plus ANSI styling.
//
// Goldmark provides the parser and AST. terminalmd owns the terminal rendering
// choices because HTML-oriented markdown renderers tend to produce layouts that
// are awkward inside a transcript viewport.
type Renderer struct {
	opts Options
	md   goldmark.Markdown
}

// DefaultTheme returns a neutral 256-color theme that works on dark terminals.
func DefaultTheme() Theme {
	return Theme{
		Text:        Style{Foreground: "252"},
		Muted:       Style{Foreground: "244"},
		Heading:     Style{Foreground: "252", Bold: true},
		Code:        Style{Foreground: "252", Background: "235"},
		CodeBlock:   Style{Foreground: "252"},
		Link:        Style{Foreground: "39"},
		QuoteMarker: Style{Foreground: "244"},
		TableBorder: Style{Foreground: "244"},
	}
}

// NewRenderer creates a renderer with GitHub-Flavored Markdown and emoji
// support enabled.
func NewRenderer(opts Options) *Renderer {
	opts = normalizeOptions(opts)
	return &Renderer{
		opts: opts,
		md: goldmark.New(
			goldmark.WithExtensions(
				extension.GFM,
				emoji.Emoji,
				&goldmermaid.Extender{NoScript: true},
			),
		),
	}
}

// Render converts markdown into a terminal string.
//
// The render pipeline is:
// 1. normalize model-produced markdown-ish artifacts into parseable Markdown;
// 2. split out tables that need custom terminal layout;
// 3. parse all other blocks with Goldmark;
// 4. trim outer whitespace while preserving internal block spacing.
func (r *Renderer) Render(markdown string) (string, error) {
	if r == nil {
		r = NewRenderer(Options{})
	}
	markdownValue := str.String(markdown)
	markdown = markdownValue.Trim()
	if markdown == "" {
		return "", nil
	}

	markdown = normalizeCommonMarkdownArtifacts(markdown)
	blocks := r.renderWithTableBlocks(markdown)
	joinBlocksValue := str.String(joinBlocks(blocks))
	return joinBlocksValue.Trim(), nil
}

// normalizeOptions fills the small set of defaults needed for predictable
// rendering. The zero value of Options is valid.
func normalizeOptions(opts Options) Options {
	if opts.Width <= 0 {
		opts.Width = defaultWidth
	}
	if opts.Theme == (Theme{}) {
		opts.Theme = DefaultTheme()
	}
	if opts.SyntaxTheme == "" {
		opts.SyntaxTheme = "monokai"
	}
	if opts.SyntaxFormatter == "" {
		opts.SyntaxFormatter = "terminal256"
	}
	return opts
}

// renderWithTableBlocks walks the markdown line-by-line so terminalmd can render
// tables itself instead of relying on Goldmark's HTML-table semantics.
//
// Tables are detected only outside fenced code blocks. Everything else is
