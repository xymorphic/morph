package agent

import (
	"github.com/xymorphic/morph/internal/agent/context/compaction"
	"github.com/xymorphic/morph/internal/config"
	models "github.com/xymorphic/morph/internal/model"
	"github.com/xymorphic/morph/internal/trace"
)

// isCompactionEnabled reports whether context budget traces should consider compaction.
func isCompactionEnabled(cfg *config.Config) bool {
	if cfg == nil || cfg.Compaction.Enabled == nil {
		return true
	}

	return *cfg.Compaction.Enabled
}

// getCompactionEvaluator creates the evaluator used for cheap preflight estimates.
func getCompactionEvaluator(cfg *config.Config) *compaction.Evaluator {
	if cfg == nil {
		return compaction.NewEvaluator(0, 0, 0)
	}

	return compaction.NewEvaluator(
		cfg.Models.Main.ContextLength,
		cfg.Compaction.TriggerPercent,
		cfg.Compaction.WarnPercent,
	)
}

// recordPreflightCompactionTrace records estimated context usage before each model request.
func recordPreflightCompactionTrace(
	traceSession trace.Session,
	cfg *config.Config,
	request models.Request,
	anchor compaction.Anchor,
	canCompact bool,
) {
	if !isCompactionEnabled(cfg) {
		return
	}

	estimate := getCompactionEvaluator(cfg).Evaluate(request, anchor)
	payload := trace.ContextEventPayload{
		Source:             estimate.Source,
		PromptTokens:       estimate.PromptTokens,
		AnchorPromptTokens: estimate.AnchorPromptTokens,
		AnchorMessageCount: estimate.AnchorMessageCount,
		DeltaPromptTokens:  estimate.DeltaPromptTokens,
		ContextLimit:       estimate.ContextLimit,
		TriggerThreshold:   estimate.TriggerThreshold,
		WarnThreshold:      estimate.WarnThreshold,
	}

	traceSession.Record(trace.EvtContextPreflight, payload)

	// Trigger only when persisted history can actually be summarized. In-flight
	// tool output can exceed the budget, but there may be nothing durable to compact yet.
	if estimate.Triggered() && canCompact {
		agentLog.Info().
			Str("source", estimate.Source).
			Int("prompt_tokens", estimate.PromptTokens).
			Int("trigger_threshold", estimate.TriggerThreshold).
			Int("context_limit", estimate.ContextLimit).
			Msg("preflight context estimate exceeded compaction threshold")
		traceSession.Record(trace.EvtContextCompactionTriggered, payload)
	}

	// Warnings remain useful even when compaction cannot run yet; they explain
	// why the next turn may compact once more history is persisted.
	if estimate.Warning() && (!estimate.Triggered() || !canCompact) {
		agentLog.Warn().
			Int("prompt_tokens", estimate.PromptTokens).
			Int("warn_threshold", estimate.WarnThreshold).
			Int("context_limit", estimate.ContextLimit).
			Msg("context approaching compaction threshold")
	}
	if estimate.Warning() {
		traceSession.Record(trace.EvtContextCompactionWarning, payload)
	}
}
