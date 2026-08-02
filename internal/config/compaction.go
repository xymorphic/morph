package config

import "github.com/xymorphic/morph/internal/constants"

// CompactionConfig controls automatic session-summary compaction thresholds.
type CompactionConfig struct {
	Enabled           *bool   `yaml:"enabled"`
	TriggerPercent    float64 `yaml:"triggerPercent"`
	WarnPercent       float64 `yaml:"warnPercent"`
	RecentSessionTail *int    `yaml:"recentSessionTail"`
}

func (c *Config) CompactionEnabled() bool {
	if c == nil {
		return constants.DefaultProfileCompactionEnabled
	}

	c.normalizeFields()
	return getBoolValueDefault(c.Compaction.Enabled, constants.DefaultProfileCompactionEnabled)
}

func (c *Config) CompactionRecentSessionTailEffective() int {
	if c == nil || c.Compaction.RecentSessionTail == nil {
		return constants.RecentSessionTail
	}

	if *c.Compaction.RecentSessionTail < 0 {
		return 0
	}

	return *c.Compaction.RecentSessionTail
}
