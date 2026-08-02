package guardrails

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"

	coreguardrails "github.com/xymorphic/morph/internal/guardrails"
	"github.com/xymorphic/morph/internal/memory"
)

// Guardrails adapts the core guardrail utilities to the memory provider
// interface. The implementation is intentionally conservative: writes are
// safety scanned, reads are redacted, and structural validation currently stays
// in the provider/store-specific code.
type Guardrails struct {
	redactor coreguardrails.Redactor
}

// New returns default memory guardrails. A custom redactor can be injected in
// tests or alternate runtime setups; nil selects the shared core redactor.
func New(redactor coreguardrails.Redactor) memory.Guardrails {
	if redactor == nil {
		redactor = coreguardrails.NewRedactor()
	}
	return Guardrails{redactor: redactor}
}

// ValidateSearch currently allows all structurally valid provider queries. Query
// shape validation lives closer to each search implementation because supported
// filters differ by store capability.
func (g Guardrails) ValidateSearch(context.Context, memory.SearchQuery) error {
	return nil
}

// ValidateWrite is paired with SafetyScan by the provider. Field-level memory
// validation is handled by candidate builders and lifecycle code.
func (g Guardrails) ValidateWrite(context.Context, memory.MemoryItem) error {
	return nil
}

// ValidateDelete is a hook for future policy checks such as protected pinned
// records. ID validation is still enforced by the provider.
func (g Guardrails) ValidateDelete(context.Context, memory.DeleteRequest) error {
	return nil
}

// SafetyScan checks the text that would become durable memory. GuardrailSource
// includes the memory ID when available, which makes blocked-content reports
// easier to connect back to storage.
func (g Guardrails) SafetyScan(ctx context.Context, item memory.MemoryItem) error {
	scanned := coreguardrails.SafetyScan(
		strings.Join([]string{item.Title, item.Text}, "\n"),
		item.GuardrailSource(),
	)
	if scanned.Blocked {
		coreguardrails.RetainUnsafeEvidence(
			ctx,
			coreguardrails.UnsafeEvidenceRecorderFromContext(ctx),
			coreguardrails.UnsafeEvidence{
				Source:   item.GuardrailSource(),
				Action:   "blocked",
				Blocked:  true,
				Findings: coreguardrails.SafetyFindingLogFields(scanned.Findings),
				Original: item,
			},
		)
		return errors.New("memory item failed safety scan")
	}
	return nil
}

// Redact sanitizes all prompt-facing string fields while preserving the memory
// shape. It returns a copy so callers do not accidentally mutate canonical
// stored memory.
func (g Guardrails) Redact(ctx context.Context, item memory.MemoryItem) (memory.MemoryItem, error) {
	original := item
	item.Title = sanitizeString(g.redactor, item.Title)
	item.Text = sanitizeString(g.redactor, item.Text)
	item.Tags = sanitizeStrings(g.redactor, item.Tags)
	if len(item.Metadata) > 0 {
		metadata := make(map[string]string, len(item.Metadata))
		for key, value := range item.Metadata {
			metadata[key] = sanitizeString(g.redactor, value)
		}
		item.Metadata = metadata
	}
	if item.Title != original.Title ||
		item.Text != original.Text ||
		!slices.Equal(item.Tags, original.Tags) ||
		!maps.Equal(item.Metadata, original.Metadata) {
		coreguardrails.RetainUnsafeEvidence(
			ctx,
			coreguardrails.UnsafeEvidenceRecorderFromContext(ctx),
			coreguardrails.UnsafeEvidence{
				Source:   item.GuardrailSource(),
				Action:   "redacted",
				Redacted: true,
				Original: original,
				Safe:     item,
			},
		)
	}
	return item, nil
}

func sanitizeString(redactor coreguardrails.Redactor, value string) string {
	if redactor == nil {
		redactor = coreguardrails.NewRedactor()
	}
	sanitized, ok := redactor.Sanitize(value).(string)
	if !ok {
		return value
	}
	return sanitized
}

func sanitizeStrings(redactor coreguardrails.Redactor, values []string) []string {
	if len(values) == 0 {
		return nil
	}

	sanitized := make([]string, 0, len(values))
	for _, value := range values {
		sanitized = append(sanitized, sanitizeString(redactor, value))
	}

	return sanitized
}
