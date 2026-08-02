package cli

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/config"
)

func TestSafetySummary_FormatsSafetyModes(t *testing.T) {
	inputSafety := false
	outputSafety := true
	piiSafety := true

	got := SafetySummary(&config.Config{
		Safety: config.SafetyConfig{
			Input:  &inputSafety,
			Output: &outputSafety,
			PII:    &piiSafety,
		},
	})

	require.Equal(t, "input=disabled, output=enabled, pii=enabled", got)
}

func TestSafetySummary_UsesDefaultsForNilConfig(t *testing.T) {
	require.Equal(t, "input=enabled, output=enabled, pii=enabled", SafetySummary(nil))
}
