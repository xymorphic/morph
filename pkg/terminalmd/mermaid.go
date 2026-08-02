package terminalmd

import (
	"strings"

	"github.com/xymorphic/morph/pkg/str"
)

func isMermaidLanguage(language string) bool {
	languageValue := str.String(language)
	return strings.EqualFold(languageValue.Trim(), "mermaid")
}

func IsMermaidDiagramStart(line string) bool {
	lineValue := str.String(line)
	trimmed := lineValue.Trim()
	if trimmed == "" {
		return false
	}

	firstField := trimmed
	if fields := strings.Fields(trimmed); len(fields) > 0 {
		firstField = fields[0]
	}

	switch strings.ToLower(firstField) {
	case "flowchart",
		"graph",
		"sequencediagram",
		"classdiagram",
		"classdiagram-v2",
		"statediagram",
		"statediagram-v2",
		"erdiagram",
		"journey",
		"gantt",
		"pie",
		"quadrantchart",
		"requirementdiagram",
		"gitgraph",
		"mindmap",
		"timeline",
		"sankey-beta",
		"xychart-beta",
		"block-beta",
		"packet-beta",
		"architecture-beta":
		return true
	default:
		return false
	}
}

func (r *Renderer) renderMermaidDiagram(source string, indent string) string {
	source = strings.TrimRight(source, "\n")
	if source == "" {
		return ""
	}

	label := r.style(r.opts.Theme.Muted).Render("Mermaid source (visual render unavailable)")
	body := r.style(r.opts.Theme.CodeBlock).Render(source)
	lines := strings.Split(body, "\n")
	rendered := make([]string, 0, len(lines)+1)
	rendered = append(rendered, indent+label)
	for _, line := range lines {
		rendered = append(rendered, indent+line)
	}

	return strings.Join(rendered, "\n")
}
