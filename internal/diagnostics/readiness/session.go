package readiness

import (
	"fmt"

	"github.com/xymorphic/morph/internal/config"
)

func buildSessionGroup(cfg *config.Config) Group {
	if cfg == nil {
		return Group{Name: "session", Checks: []Check{check("config", StatusFail, "config is required")}}
	}

	return Group{Name: "session", Checks: []Check{buildCompactionCheck(cfg)}}
}

func buildCompactionCheck(cfg *config.Config) Check {
	message := fmt.Sprintf(
		"%s, triggerPercent=%.2f, warnPercent=%.2f, recentSessionTail=%d",
		formatEnabled(cfg.CompactionEnabled()),
		cfg.Compaction.TriggerPercent,
		cfg.Compaction.WarnPercent,
		cfg.CompactionRecentSessionTailEffective(),
	)
	if !cfg.CompactionEnabled() {
		return check("compaction", StatusWarn, message)
	}

	return check("compaction", StatusPass, message)
}
