package terminalmd

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/xymorphic/morph/pkg/str"
	extast "github.com/yuin/goldmark/extension/ast"
)

func (r *Renderer) renderTableNode(table *extast.Table, source []byte) string {
	rows := make([][]string, 0)
	for section := table.FirstChild(); section != nil; section = section.NextSibling() {
		for row := section.FirstChild(); row != nil; row = row.NextSibling() {
			if row.Kind() != extast.KindTableRow && row.Kind() != extast.KindTableHeader {
				continue
			}

			values := make([]string, 0)
			for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
				values = append(values, r.renderInlineChildren(cell, source))
			}
			rows = append(rows, values)
		}
	}

	return r.renderTableRows(rows)
}

// renderTable parses raw markdown table lines and renders them with terminal
// layout rules.
func (r *Renderer) renderTable(lines []string) string {
	rows := parseMarkdownTable(lines)
	return r.renderTableRows(rows)
}

// renderTableRows renders parsed table rows.
//
// Compact box-drawn tables are preferred while they fit the configured width.
// When they do not fit, the same data is rendered as labeled rows, which avoids
// the broken horizontal overflow that plain markdown tables can cause in a TUI.
func (r *Renderer) renderTableRows(rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}

	for rowIndex := range rows {
		for columnIndex := range rows[rowIndex] {
			rows[rowIndex][columnIndex] = r.renderInlineMarkdown(rows[rowIndex][columnIndex])
		}
	}

	table := r.renderCompactTable(rows)
	if maxLineWidth(table) <= r.opts.Width || r.opts.Width <= 0 {
		return table
	}
	return r.renderTableAsLabeledRows(rows)
}

// renderCompactTable renders a simple box-drawn table.
//
// ANSI-styled cell content is measured with x/ansi so styled text does not break
// column alignment.
func (r *Renderer) renderCompactTable(rows [][]string) string {
	widths := tableColumnWidths(rows)
	if len(widths) == 0 {
		return ""
	}

	var builder strings.Builder
	writeBorder := func(left, middle, right string) {
		builder.WriteString(r.style(r.opts.Theme.TableBorder).Render(left))
		for index, width := range widths {
			builder.WriteString(r.style(r.opts.Theme.TableBorder).Render(strings.Repeat("─", width+2)))
			if index == len(widths)-1 {
				builder.WriteString(r.style(r.opts.Theme.TableBorder).Render(right))
			} else {
				builder.WriteString(r.style(r.opts.Theme.TableBorder).Render(middle))
			}
		}
		builder.WriteString("\n")
	}

	writeBorder("┌", "┬", "┐")
	for rowIndex, row := range rows {
		builder.WriteString(r.style(r.opts.Theme.TableBorder).Render("│"))
		for columnIndex, width := range widths {
			cell := ""
			if columnIndex < len(row) {
				cell = row[columnIndex]
			}
			builder.WriteString(" ")
			builder.WriteString(cell)
			builder.WriteString(strings.Repeat(" ", width-ansi.StringWidth(cell)+1))
			builder.WriteString(r.style(r.opts.Theme.TableBorder).Render("│"))
		}
		builder.WriteString("\n")
		if rowIndex == 0 && len(rows) > 1 {
			writeBorder("├", "┼", "┤")
		}
	}
	writeBorder("└", "┴", "┘")

	return strings.TrimRight(builder.String(), "\n")
}

// renderTableAsLabeledRows renders wide tables as repeated "Header: value"
// groups. This trades table shape for readability inside narrow transcript panes.
func (r *Renderer) renderTableAsLabeledRows(rows [][]string) string {
	if len(rows) < 2 {
		return ""
	}

	headers := rows[0]
	blocks := make([]string, 0, len(rows)-1)
	for _, row := range rows[1:] {
		lines := make([]string, 0, len(headers))
		for index, header := range headers {
			if index >= len(row) {
				continue
			}

			headerValue := str.String(header)
			header = headerValue.Trim()
			rowValue := str.String(row[index])
			value := rowValue.Trim()
			if header == "" || value == "" {
				continue
			}
			lines = append(lines, r.style(r.opts.Theme.Muted).Render(header+":")+" "+value)
		}
		if len(lines) > 0 {
			blocks = append(blocks, strings.Join(lines, "\n"))
		}
	}

	return strings.Join(blocks, "\n\n")
}

// highlightCode runs Chroma over a code block and returns ANSI-highlighted text.
//
// Empty string means "highlighting failed"; callers should fall back to plain
func isMarkdownTableStart(lines []string, index int) bool {
	if index+1 >= len(lines) {
		return false
	}

	return isMarkdownTableRow(lines[index]) && isMarkdownTableSeparator(lines[index+1])
}

// isMarkdownTableRow is intentionally stricter than "contains a pipe" to avoid
// turning prose with vertical bars into a table.
func isMarkdownTableRow(line string) bool {
	lineValue := str.String(line)
	trimmed := lineValue.Trim()
	return strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|") && strings.Count(trimmed, "|") >= 2
}

// isMarkdownTableSeparator validates a GFM separator row, including alignment
// markers such as :--- and ---:.
func isMarkdownTableSeparator(line string) bool {
	cells := splitMarkdownTableRow(line)
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		cellValue := str.String(cell)
		cell = cellValue.Trim()
		if len(cell) < 3 {
			return false
		}
		for _, char := range cell {
			if char != '-' && char != ':' {
				return false
			}
		}
	}

	return true
}

// parseMarkdownTable converts raw table lines into cells and skips the separator
// row. Inline formatting is handled later by renderTableRows.
func parseMarkdownTable(lines []string) [][]string {
	if len(lines) == 0 {
		return nil
	}

	rows := make([][]string, 0, len(lines))
	for index, line := range lines {
		if index == 1 && isMarkdownTableSeparator(line) {
			continue
		}

		rows = append(rows, splitMarkdownTableRow(line))
	}

	return rows
}

// splitMarkdownTableRow splits a table row while respecting inline code spans
// and escaped pipes. A simple strings.Split would corrupt cells like `a|b`.
func splitMarkdownTableRow(line string) []string {
	lineValue2 := str.String(line)
	line = lineValue2.Trim()
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")

	cells := make([]string, 0)
	var cell strings.Builder
	inCodeSpan := false
	escaped := false
	for _, char := range line {
		switch {
		case escaped:
			// Only escaped pipes are consumed here. Other escapes are preserved so
			// the inline markdown renderer can handle them correctly later.
			if char == '|' {
				cell.WriteRune(char)
			} else {
				cell.WriteRune('\\')
				cell.WriteRune(char)
			}
			escaped = false
		case char == '\\':
			escaped = true
		case char == '`':
			inCodeSpan = !inCodeSpan
			cell.WriteRune(char)
		case char == '|' && !inCodeSpan:
			cellValue := str.String(cell.String())
			cells = append(cells, cellValue.Trim())
			cell.Reset()
		default:
			cell.WriteRune(char)
		}
	}
	if escaped {
		cell.WriteRune('\\')
	}
	textValue := str.String(cell.String())
	cells = append(cells, textValue.Trim())

	return cells
}

// tableColumnWidths returns display widths for each column after inline styling.
func tableColumnWidths(rows [][]string) []int {
	maxColumns := 0
	for _, row := range rows {
		maxColumns = max(maxColumns, len(row))
	}
	if maxColumns == 0 {
		return nil
	}

	widths := make([]int, maxColumns)
	for _, row := range rows {
		for index, cell := range row {
			widths[index] = max(widths[index], ansi.StringWidth(cell))
		}
	}

	return widths
}
