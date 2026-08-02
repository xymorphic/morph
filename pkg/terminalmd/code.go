package terminalmd

import (
	"bytes"
	"strings"

	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	chromastyles "github.com/alecthomas/chroma/v2/styles"
	"github.com/xymorphic/morph/pkg/str"
)

func (r *Renderer) renderCode(language []byte, source []byte, indent string) string {
	code := strings.TrimRight(string(source), "\n")
	if code == "" {
		return ""
	}
	languageValue := str.String(string(language))
	languageName := languageValue.Trim()
	if isMermaidLanguage(languageName) {
		return r.renderMermaidDiagram(code, indent)
	}

	rendered := r.highlightCode(languageName, code)
	if rendered == "" {
		rendered = r.style(r.opts.Theme.CodeBlock).Render(code)
	}

	lines := strings.Split(strings.TrimRight(rendered, "\n"), "\n")
	for index, line := range lines {
		lines[index] = indent + line
	}

	return strings.Join(lines, "\n")
}

// renderInlineChildren renders all inline children of a node and trims only the
func (r *Renderer) highlightCode(language string, code string) string {
	lexer := lexers.Get(language)
	if lexer == nil {
		lexer = lexers.Analyse(code)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}

	style := chromastyles.Get(r.opts.SyntaxTheme)
	if style == nil {
		style = chromastyles.Fallback
	}

	formatter := formatters.Get(r.opts.SyntaxFormatter)
	if formatter == nil {
		formatter = formatters.Fallback
	}

	iterator, err := lexer.Tokenise(nil, code)
	if err != nil {
		return ""
	}

	var buffer bytes.Buffer
	if err := formatter.Format(&buffer, style, iterator); err != nil {
		return ""
	}

	return strings.TrimRight(buffer.String(), "\n")
}
