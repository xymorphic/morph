package jsonterms

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/xymorphic/morph/pkg/str"
)

func Terms(raw string, prefix ...string) string {
	termPrefix := ""
	if len(prefix) > 0 {
		termPrefix = prefix[0]
	}
	builder := newTermBuilder()
	jsonToTerms(builder, termPrefix, raw)
	return builder.String()
}

type termBuilder struct {
	order []string
	seen  map[string]struct{}
}

func newTermBuilder() *termBuilder {
	return &termBuilder{seen: make(map[string]struct{})}
}

func (b *termBuilder) add(parts ...string) {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		part = normalizeScalar(part)
		if part == "" {
			continue
		}
		filtered = append(filtered, part)
	}
	if len(filtered) == 0 {
		return
	}

	term := strings.Join(filtered, " ")
	if _, ok := b.seen[term]; ok {
		return
	}

	b.seen[term] = struct{}{}
	b.order = append(b.order, term)
}

func (b *termBuilder) String() string {
	return strings.Join(b.order, "\n")
}

func jsonToTerms(builder *termBuilder, prefix string, raw string) {
	rawValue := str.String(raw)
	raw = rawValue.Trim()
	if raw == "" {
		return
	}

	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return
	}

	addValueTerms(builder, prefix, value)
}

func addValueTerms(builder *termBuilder, prefix string, value any) {
	switch current := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(current))
		for key := range current {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		for _, key := range keys {
			nextPrefix := key
			if prefix != "" {
				nextPrefix = prefix + "." + key
			}
			addValueTerms(builder, nextPrefix, current[key])
		}
	case []any:
		for _, item := range current {
			addValueTerms(builder, prefix, item)
		}
	case string:
		currentValue := str.String(current)
		current = currentValue.Trim()
		if current == "" {
			return
		}
		if looksLikeJSON(current) {
			var nested any
			if err := json.Unmarshal([]byte(current), &nested); err == nil {
				addValueTerms(builder, prefix, nested)
				return
			}
		}
		builder.add(prefix, current)
		builder.add(current)
	case bool:
		builder.add(prefix, fmt.Sprintf("%t", current))
		builder.add(fmt.Sprintf("%t", current))
	case float64:
		term := normalizeScalar(fmt.Sprintf("%v", current))
		builder.add(prefix, term)
		builder.add(term)
	default:
		if value == nil {
			return
		}
		term := normalizeScalar(fmt.Sprintf("%v", value))
		builder.add(prefix, term)
		builder.add(term)
	}
}

func normalizeScalar(value string) string {
	valueText := str.String(value).Normalized()
	if valueText == "" {
		return ""
	}
	return strings.Join(strings.Fields(valueText), " ")
}

func looksLikeJSON(value string) bool {
	value2 := str.String(value)
	value = value2.Trim()
	if value == "" {
		return false
	}
	switch value[0] {
	case '{', '[', '"':
		return true
	default:
		return false
	}
}
