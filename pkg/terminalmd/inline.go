package terminalmd

import (
	"strings"

	"github.com/xymorphic/morph/pkg/str"
	emojiast "github.com/yuin/goldmark-emoji/ast"
	goldast "github.com/yuin/goldmark/ast"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

func (r *Renderer) renderInlineChildren(node goldast.Node, source []byte) string {
	var builder strings.Builder
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		builder.WriteString(r.renderInline(child, source))
	}
	textValue := str.String(builder.String())
	return textValue.Trim()
}

// renderInline maps Goldmark inline nodes to terminal text.
//
// This is where inline markdown semantics become terminal semantics: emphasis
// becomes ANSI style, links may become OSC 8 hyperlinks, images become alt text,
// and raw HTML is reduced to visible text.
func (r *Renderer) renderInline(node goldast.Node, source []byte) string {
	switch n := node.(type) {
	case *goldast.Text:
		text := unescapeMarkdownText(string(n.Segment.Value(source)))
		if n.HardLineBreak() {
			return text + "\n"
		}
		if n.SoftLineBreak() {
			return text + " "
		}
		return text
	case *goldast.String:
		return unescapeMarkdownText(string(n.Value))
	case *goldast.CodeSpan:
		return r.style(r.opts.Theme.Code).Render(r.renderInlineChildren(n, source))
	case *goldast.Emphasis:
		text := r.renderInlineChildren(n, source)
		style := r.opts.Theme.Text
		if n.Level >= 2 {
			style.Bold = true
		} else {
			style.Italic = true
		}
		return r.style(style).Render(text)
	case *goldast.Link:
		text := r.renderInlineChildren(n, source)
		if text == "" {
			text = string(n.Destination)
		}
		rendered := r.style(r.opts.Theme.Link).Render(text)
		return r.renderHyperlink(rendered, string(n.Destination))
	case *goldast.Image:
		// Terminals cannot display markdown images here, so use alt text when it
		// exists and fall back to the destination for otherwise-empty images.
		text := r.renderInlineChildren(n, source)
		if text != "" {
			return text
		}
		return string(n.Destination)
	case *goldast.AutoLink:
		text := string(n.Label(source))
		rendered := r.style(r.opts.Theme.Link).Render(text)
		return r.renderHyperlink(rendered, text)
	case *goldast.RawHTML:
		// Inline HTML should not leak markup characters into assistant output.
		return stripHTMLTags(string(n.Segments.Value(source)))
	case *extast.Strikethrough:
		style := r.opts.Theme.Text
		style.Strike = true
		return r.style(style).Render(r.renderInlineChildren(n, source))
	case *emojiast.Emoji:
		if n.Value != nil {
			return string(n.Value.Unicode)
		}
		return ":" + string(n.ShortName) + ":"
	case *extast.TaskCheckBox:
		if n.IsChecked {
			return "[x] "
		}
		return "[ ] "
	default:
		return r.renderInlineChildren(node, source)
	}
}

// renderInlineMarkdown parses a short markdown fragment and returns only its
// inline content. It is used for table cells and TextBlock nodes that need inline
// emphasis/link/code handling without becoming a full block.
func (r *Renderer) renderInlineMarkdown(markdown string) string {
	source := []byte(markdown)
	document := r.md.Parser().Parse(text.NewReader(source))
	renderInlineChildrenValue := str.String(r.renderInlineChildren(document, source))
	return renderInlineChildrenValue.Trim()
}
