package provider_ollama

import (
	"strings"

	"github.com/xymorphic/morph/pkg/str"
)

func NormalizeModelID(value string) string {
	valueText := str.String(value).Trim()
	if provider, model, ok := strings.Cut(valueText, "/"); ok {
		providerValue := str.String(provider)
		if !strings.EqualFold(providerValue.Trim(), "ollama") {
			return valueText
		}
		modelValue := str.String(model)
		return modelValue.Trim()
	}

	return valueText
}

func NormalizeModelIDForComparison(value string) string {
	value = NormalizeModelID(value)
	if value == "" || strings.Contains(value, ":") {
		return value
	}

	return value + ":latest"
}

func ModelIDMatches(installed string, requested string) bool {
	return strings.EqualFold(
		NormalizeModelIDForComparison(installed),
		NormalizeModelIDForComparison(requested),
	)
}
