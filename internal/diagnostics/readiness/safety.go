package readiness

import (
	"fmt"

	"github.com/xymorphic/morph/internal/config"
)

func buildSafetyGroup(cfg *config.Config) Group {
	if cfg == nil {
		return Group{Name: "safety", Checks: []Check{check("config", StatusFail, "config is required")}}
	}

	return Group{
		Name: "safety",
		Checks: []Check{check(
			"policy",
			StatusPass,
			fmt.Sprintf(
				"input=%s, output=%s, pii=%s",
				formatEnabled(cfg.InputSafetyEnabled()),
				formatEnabled(cfg.OutputSafetyEnabled()),
				formatEnabled(cfg.OutputPIIRedactionEnabled()),
			),
		)},
	}
}

func formatEnabled(enabled bool) string {
	if enabled {
		return "enabled"
	}

	return "disabled"
}
