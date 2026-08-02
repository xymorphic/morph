package terminalmd

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/xymorphic/morph/pkg/str"
	goldast "github.com/yuin/goldmark/ast"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
	goldmermaid "go.abhg.dev/goldmark/mermaid"
)

func (r *Renderer) renderWithTableBlocks(markdown string) []string {
	lines := strings.Split(markdown, "\n")
	blocks := make([]string, 0)
	chunk := make([]string, 0)
	inFence := false

	// Flush the pending non-table markdown chunk through the Goldmark block
	// renderer. Empty chunks are ignored so multiple blank lines do not create
	// empty transcript cells.
	flushChunk := func() {
		joinValue := str.String(strings.Join(chunk, "\n"))
		text := joinValue.Trim()
		chunk = chunk[:0]
		if text == "" {
			return
		}
		blocks = append(blocks, r.renderGoldmarkBlocks(text)...)
	}

	for index := 0; index < len(lines); index++ {
		line := lines[index]
		// Fence tracking prevents table-looking text inside code blocks from
		// being consumed by the custom table parser.
		if isFenceLine(line) {
			inFence = !inFence
			chunk = append(chunk, line)
			continue
		}
		// Models often introduce a Mermaid diagram as plain copyable text
		// instead of a fenced ```mermaid block. Detect that shape before
		// Goldmark parses it as normal paragraphs.
		if !inFence && IsMermaidDiagramStart(line) {
			flushChunk()
			diagramLines := []string{line}
			index++
			linesValue := str.String(lines[index])
			for index < len(lines) && linesValue.Trim() != "" {
				diagramLines = append(diagramLines, lines[index])
				index++
			}
			index--
			blocks = append(blocks, r.renderMermaidDiagram(strings.Join(diagramLines, "\n"), ""))
			continue
		}
		// A table is header row + separator row + optional body rows. Once found,
		// it is removed from the normal markdown chunk and rendered as a terminal
		// table or labeled rows.
		if !inFence && isMarkdownTableStart(lines, index) {
			flushChunk()
			tableLines := []string{line, lines[index+1]}
			index += 2
			for index < len(lines) && isMarkdownTableRow(lines[index]) {
				tableLines = append(tableLines, lines[index])
				index++
			}
			index--
			blocks = append(blocks, r.renderTable(tableLines))
			continue
		}
		chunk = append(chunk, line)
	}

	flushChunk()
	return blocks
}

// renderGoldmarkBlocks parses a markdown chunk and renders each top-level block.
func (r *Renderer) renderGoldmarkBlocks(markdown string) []string {
	source := []byte(markdown)
	document := r.md.Parser().Parse(text.NewReader(source))
	blocks := make([]string, 0)
	for child := document.FirstChild(); child != nil; child = child.NextSibling() {
		block := r.renderBlock(child, source, "")
		blockValue := str.String(block)
		if blockValue.Trim() != "" {
			blocks = append(blocks, block)
		}
	}

	return blocks
}

// renderBlock renders a single Goldmark block node into terminal text.
//
// The indent parameter is already-cell-based text that should prefix every
// wrapped line. List and quote renderers use it to build hanging indentation.
func (r *Renderer) renderBlock(node goldast.Node, source []byte, indent string) string {
	switch n := node.(type) {
	case *goldast.Heading:
		return r.renderHeading(n, source, indent)
	case *goldast.Paragraph:
		text := r.renderInlineChildren(n, source)
		lines := wrapANSI(text, r.opts.Width, indent, indent)
		return strings.Join(lines, "\n")
	case *goldast.TextBlock:
		// Goldmark can put list item text into TextBlock nodes. Run it back
		// through inline parsing so markdown artifacts like **bold** inside
		// bullet rows are still rendered instead of shown literally.
		lines := wrapANSI(r.renderInlineMarkdown(string(n.Lines().Value(source))), r.opts.Width, indent, indent)
		return strings.Join(lines, "\n")
	case *goldast.List:
		return r.renderList(n, source, indent)
	case *goldast.Blockquote:
		return r.renderBlockquote(n, source, indent)
	case *goldast.FencedCodeBlock:
		return r.renderCode(n.Language(source), n.Lines().Value(source), indent)
	case *goldast.CodeBlock:
		return r.renderCode(nil, n.Lines().Value(source), indent)
	case *goldmermaid.Block:
		return r.renderMermaidDiagram(string(n.Lines().Value(source)), indent)
	case *goldast.HTMLBlock:
		// Raw HTML has no native terminal equivalent here. Preserve visible text
		// and common line breaks instead of leaking tags into the transcript.
		text := stripHTMLTags(string(getHTMLBlockText(n, source)))
		lines := wrapANSI(text, r.opts.Width, indent, indent)
		return strings.Join(lines, "\n")
	case *goldast.ThematicBreak:
		return indent + r.style(r.opts.Theme.Muted).Render(strings.Repeat("-", max(8, min(r.opts.Width, 48))))
	case *extast.Table:
		return r.renderTableNode(n, source)
	default:
		// Unknown block containers are rendered by recursively rendering their
		// children. This keeps the renderer permissive as Goldmark adds or
		// exposes new container nodes.
		blocks := make([]string, 0)
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			block := r.renderBlock(child, source, indent)
			blockValue := str.String(block)
			if blockValue.Trim() != "" {
				blocks = append(blocks, block)
			}
		}
		return joinBlocks(blocks)
	}
}

func getHTMLBlockText(node *goldast.HTMLBlock, source []byte) []byte {
	lines := node.Lines().Value(source)
	text := make([]byte, len(lines))
	copy(text, lines)
	if node.HasClosure() {
		text = append(text, node.ClosureLine.Value(source)...)
	}

	return text
}

// renderHeading gives terminal headings a visible hierarchy instead of showing
// raw Markdown marker characters. The first level gets a distinctive transcript
// mark and stronger styling, while lower levels stay compact and scannable.
func (r *Renderer) renderHeading(node *goldast.Heading, source []byte, indent string) string {
	text := r.renderInlineChildren(node, source)
	if text == "" {
		return ""
	}

	style := r.opts.Theme.Heading
	style.Bold = true

	if node.Level == 1 {
		marker := r.style(style).Render("● ")
		style.Italic = true
		style.Underline = true
		return indent + marker + r.style(style).Render(text)
	}

	return indent + r.style(style).Render(text)
}

// renderList renders ordered and unordered lists with terminal-friendly markers.
//
// Unordered list markers are normalized to a bullet regardless of whether the
// source used -, *, +, or a model-generated Unicode bullet.
func (r *Renderer) renderList(list *goldast.List, source []byte, indent string) string {
	return r.renderListWithDepth(list, source, indent, 0)
}

func (r *Renderer) renderListWithDepth(list *goldast.List, source []byte, indent string, depth int) string {
	lines := make([]string, 0)
	number := list.Start
	for item := list.FirstChild(); item != nil; item = item.NextSibling() {
		prefix := unorderedListPrefix(depth)
		if list.IsOrdered() {
			prefix = strconv.Itoa(number) + ". "
			number++
		}
		itemLines := r.renderListItem(item, source, indent, prefix, depth)
		lines = append(lines, itemLines...)
	}

	return strings.Join(lines, "\n")
}

func unorderedListPrefix(depth int) string {
	switch depth % 3 {
	case 1:
		return "◦ "
	case 2:
		return "▪ "
	default:
		return "• "
	}
}

// renderListItem renders one list item with hanging indentation.
//
// The first content line gets the marker prefix. All wrapped continuation lines
// get spaces equal to the marker width, so long bullet items align below their
// text rather than below the bullet.
func (r *Renderer) renderListItem(node goldast.Node, source []byte, indent string, prefix string, depth int) []string {
	contentPrefix := indent + prefix
	continuation := indent + strings.Repeat(" ", ansi.StringWidth(prefix))
	lines := make([]string, 0)
	firstParagraph := true

	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		// GFM task list checkboxes may appear as their own node before the
		// paragraph, depending on how Goldmark shapes the item. When that
		// happens, replace the normal bullet prefix with [ ] or [x].
		if checkbox, ok := child.(*extast.TaskCheckBox); ok {
			prefix = "[ ] "
			if checkbox.IsChecked {
				prefix = "[x] "
			}
			contentPrefix = indent + prefix
			continuation = indent + strings.Repeat(" ", ansi.StringWidth(prefix))
			continue
		}

		switch n := child.(type) {
		case *goldast.Paragraph:
			// Some task checkboxes appear inside the paragraph instead. Detect
			// those too, then remove the duplicated marker from the inline text.
			if taskPrefix, ok := getTaskPrefix(n); ok {
				prefix = taskPrefix
				contentPrefix = indent + prefix
				continuation = indent + strings.Repeat(" ", ansi.StringWidth(prefix))
			}
			text := trimLeadingTaskMarker(r.renderInlineChildren(n, source))
			first := continuation
			if firstParagraph {
				first = contentPrefix
			}
			lines = append(lines, wrapANSI(text, r.opts.Width, first, continuation)...)
			firstParagraph = false
		case *goldast.TextBlock:
			// TextBlock needs inline markdown rendering for model-output bullets
			// such as "• **headline**" that Goldmark parses as a list item with a
			// text block.
			first := continuation
			if firstParagraph {
				first = contentPrefix
			}
			text := r.renderInlineMarkdown(string(n.Lines().Value(source)))
			lines = append(lines, wrapANSI(text, r.opts.Width, first, continuation)...)
			firstParagraph = false
		case *goldast.List:
			// Nested lists start at the continuation column of the parent item.
			nested := r.renderListWithDepth(n, source, continuation, depth+1)
			lines = append(lines, strings.Split(nested, "\n")...)
			firstParagraph = false
		default:
			block := r.renderBlock(n, source, continuation)
			blockValue := str.String(block)
			if blockValue.Trim() != "" {
				lines = append(lines, strings.Split(block, "\n")...)
			}
		}
	}

	if len(lines) == 0 {
		return []string{contentPrefix}
	}
	return lines
}

// renderBlockquote prefixes each rendered quote line with a styled vertical bar.
func (r *Renderer) renderBlockquote(node goldast.Node, source []byte, indent string) string {
	blocks := make([]string, 0)
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		block := r.renderBlock(child, source, "")
		blockValue := str.String(block)
		if blockValue.Trim() != "" {
			blocks = append(blocks, block)
		}
	}

	prefix := indent + r.style(r.opts.Theme.QuoteMarker).Render("│ ")
	lines := make([]string, 0)
	for _, line := range strings.Split(joinBlocks(blocks), "\n") {
		lines = append(lines, prefix+line)
	}

	return strings.Join(lines, "\n")
}

// renderCode renders fenced and indented code blocks.
//
// Chroma is used when it can tokenize and format the code. If highlighting
