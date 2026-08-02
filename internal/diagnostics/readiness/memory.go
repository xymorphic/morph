package readiness

import (
	"fmt"

	"github.com/xymorphic/morph/internal/config"
)

func buildMemoryGroup(cfg *config.Config) Group {
	if cfg == nil {
		return Group{Name: "memory", Checks: []Check{check("config", StatusFail, "config is required")}}
	}

	return Group{Name: "memory", Checks: []Check{
		buildMemoryStatusCheck(cfg),
		buildMemoryPinnedCheck(cfg),
		buildMemoryRetrievalCheck(cfg),
		buildMemoryFlushCheck(cfg),
		buildMemoryEpisodicCheck(cfg),
		buildMemoryReflectionCheck(cfg),
		buildMemoryPromotionCheck(cfg),
		buildMemoryWriteCheck(cfg),
	}}
}

func buildMemoryStatusCheck(cfg *config.Config) Check {
	message := fmt.Sprintf(
		"%s, provider=%q, backend=%q",
		formatEnabled(cfg.MemoryEnabled()),
		cfg.Memory.Provider,
		cfg.MemoryBackendEffective(),
	)
	if !cfg.MemoryEnabled() {
		return check("status", StatusWarn, message)
	}

	return check("status", StatusPass, message)
}

func buildMemoryPinnedCheck(cfg *config.Config) Check {
	return buildMemoryFeatureCheck(
		"pinned",
		cfg,
		cfg.MemoryPinnedEnabled(),
		fmt.Sprintf(
			"%s, maxChars=%d, maxItemChars=%d",
			formatEnabled(memoryFeatureEnabled(cfg, cfg.MemoryPinnedEnabled())),
			cfg.Memory.Pinned.MaxChars,
			cfg.Memory.Pinned.MaxItemChars,
		),
	)
}

func buildMemoryRetrievalCheck(cfg *config.Config) Check {
	return buildMemoryFeatureCheck(
		"retrieval",
		cfg,
		cfg.MemoryRetrievalEnabled(),
		formatEnabled(memoryFeatureEnabled(cfg, cfg.MemoryRetrievalEnabled())),
	)
}

func buildMemoryFlushCheck(cfg *config.Config) Check {
	return buildMemoryFeatureCheck(
		"flush",
		cfg,
		cfg.MemoryFlushEnabled(),
		fmt.Sprintf(
			"%s, maxCalls=%d, maxOutputTokens=%d, timeout=%s",
			formatEnabled(memoryFeatureEnabled(cfg, cfg.MemoryFlushEnabled())),
			cfg.Memory.Flush.MaxCalls,
			cfg.Memory.Flush.MaxOutputTokens,
			cfg.Memory.Flush.Timeout,
		),
	)
}

func buildMemoryEpisodicCheck(cfg *config.Config) Check {
	return buildMemoryFeatureCheck(
		"episodic",
		cfg,
		cfg.MemoryEpisodicEnabled(),
		fmt.Sprintf(
			"%s, interval=%s, idleAfter=%s, minMessages=%d",
			formatEnabled(memoryFeatureEnabled(cfg, cfg.MemoryEpisodicEnabled())),
			cfg.Memory.Episodic.Interval,
			cfg.Memory.Episodic.IdleAfter,
			cfg.Memory.Episodic.MinMessages,
		),
	)
}

func buildMemoryReflectionCheck(cfg *config.Config) Check {
	return buildMemoryFeatureCheck(
		"reflection",
		cfg,
		cfg.MemoryReflectionEnabled(),
		fmt.Sprintf(
			"%s, interval=%s, limit=%d, relatedLimit=%d",
			formatEnabled(memoryFeatureEnabled(cfg, cfg.MemoryReflectionEnabled())),
			cfg.Memory.Reflection.Interval,
			cfg.Memory.Reflection.Limit,
			cfg.Memory.Reflection.RelatedLimit,
		),
	)
}

func buildMemoryPromotionCheck(cfg *config.Config) Check {
	return buildMemoryFeatureCheck(
		"promotion",
		cfg,
		cfg.MemoryPromotionEnabled(),
		fmt.Sprintf(
			"%s, interval=%s, limit=%d",
			formatEnabled(memoryFeatureEnabled(cfg, cfg.MemoryPromotionEnabled())),
			cfg.Memory.Promotion.Interval,
			cfg.Memory.Promotion.Limit,
		),
	)
}

func buildMemoryWriteCheck(cfg *config.Config) Check {
	return buildMemoryFeatureCheck(
		"write",
		cfg,
		cfg.MemoryWriteEnabled(),
		formatEnabled(memoryFeatureEnabled(cfg, cfg.MemoryWriteEnabled())),
	)
}

func buildMemoryFeatureCheck(name string, cfg *config.Config, featureEnabled bool, message string) Check {
	if !memoryFeatureEnabled(cfg, featureEnabled) {
		return check(name, StatusWarn, message)
	}

	return check(name, StatusPass, message)
}

func memoryFeatureEnabled(cfg *config.Config, featureEnabled bool) bool {
	return cfg != nil && cfg.MemoryEnabled() && featureEnabled
}
