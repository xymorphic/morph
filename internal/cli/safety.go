package cli

import (
	"fmt"

	"github.com/xymorphic/morph/internal/config"
)

// SafetySummary formats safety findings for CLI output.
func SafetySummary(cfg *config.Config) string {
	return fmt.Sprintf(
		"input=%s, output=%s, pii=%s",
		safetyStateLabel(cfg.InputSafetyEnabled()),
		safetyStateLabel(cfg.OutputSafetyEnabled()),
		safetyStateLabel(cfg.OutputPIIRedactionEnabled()),
	)
}

func safetyStateLabel(enabled bool) string {
	if enabled {
		return "enabled"
	}

	return "disabled"
}
