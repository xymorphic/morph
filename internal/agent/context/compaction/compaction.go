package compaction

import (
	"encoding/json"

	"github.com/xymorphic/morph/internal/constants"
	models "github.com/xymorphic/morph/internal/model"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
)

const (
	ActualSource    = "actual"
	AnchoredSource  = "anchored"
	EstimatedSource = "estimated"
)

// Anchor identifies the request prefix measured by the model provider.
type Anchor struct {
	PromptTokens int
	MessageCount int
}

// Estimate is a context budget reading used for warning and compaction decisions.
type Estimate struct {
	Source             string
	PromptTokens       int
	AnchorPromptTokens int
	AnchorMessageCount int
	DeltaPromptTokens  int
	ContextLimit       int
	TriggerThreshold   int
	WarnThreshold      int
}

// Triggered reports whether the estimate has crossed the compaction threshold.
func (e Estimate) Triggered() bool {
	return e.PromptTokens >= e.TriggerThreshold && e.TriggerThreshold > 0
}

// Warning reports whether the estimate has crossed the warning threshold.
func (e Estimate) Warning() bool {
	return e.PromptTokens >= e.WarnThreshold && e.WarnThreshold > 0
}

// Evaluator compares prompt token estimates against configured context thresholds.
type Evaluator struct {
	contextLimit     int
	triggerThreshold int
	warnThreshold    int
}

// NewEvaluator builds an Evaluator with defaults for unset limits or percentages.
func NewEvaluator(contextLimit int, triggerPercent, warnPercent float64) *Evaluator {
	if contextLimit <= 0 {
		contextLimit = constants.DefaultContextLength
	}
	if triggerPercent <= 0 {
		triggerPercent = constants.DefaultCompactionTrigger
	}
	if warnPercent <= 0 {
		warnPercent = constants.DefaultCompactionWarn
	}

	return &Evaluator{
		contextLimit:     contextLimit,
		triggerThreshold: int(float64(contextLimit) * triggerPercent),
		warnThreshold:    int(float64(contextLimit) * warnPercent),
	}
}

// EstimateTextRough estimates tokens from text length using a cheap character ratio.
func EstimateTextRough(text string) int {
	if text == "" {
		return 0
	}

	return len(text) / constants.RoughTokenCharRatio
}

// EstimateCharsFromTokensRough converts a token estimate back to a rough character budget.
func EstimateCharsFromTokensRough(tokens int) int {
	if tokens <= 0 {
		return 0
	}

	return tokens * constants.RoughTokenCharRatio
}

// EstimateRequestRough estimates prompt tokens from instructions, messages, and tool schema.
func EstimateRequestRough(req models.Request) int {
	payload := struct {
		Instructions string
		Messages     any
		Tools        any
	}{
		Instructions: req.Instructions,
		Messages:     req.Messages,
		Tools:        req.Tools,
	}

	return estimateJSONRough(payload, req.Instructions)
}

func estimateJSONRough(value any, fallback string) int {
	raw, err := json.Marshal(value)
	if err != nil {
		return EstimateTextRough(fallback)
	}

	return EstimateTextRough(string(raw))
}

// Evaluate anchors usage to the provider measurement and estimates only appended messages.
func (e *Evaluator) Evaluate(req models.Request, anchor Anchor) Estimate {
	if e == nil {
		e = NewEvaluator(0, 0, 0)
	}

	estimate := Estimate{
		ContextLimit:     e.contextLimit,
		TriggerThreshold: e.triggerThreshold,
		WarnThreshold:    e.warnThreshold,
	}
	if anchor.PromptTokens <= 0 || anchor.MessageCount < 0 || anchor.MessageCount > len(req.Messages) {
		estimate.Source = EstimatedSource
		estimate.PromptTokens = EstimateRequestRough(req)
		return estimate
	}

	estimate.AnchorPromptTokens = anchor.PromptTokens
	estimate.AnchorMessageCount = anchor.MessageCount
	if anchor.MessageCount == len(req.Messages) {
		estimate.Source = ActualSource
		estimate.PromptTokens = anchor.PromptTokens
		return estimate
	}

	estimate.Source = AnchoredSource
	estimate.DeltaPromptTokens = estimateMessagesRough(req.Messages[anchor.MessageCount:])
	estimate.PromptTokens = anchor.PromptTokens + estimate.DeltaPromptTokens
	return estimate
}

func estimateMessagesRough(messages []morphmsg.Message) int {
	return estimateJSONRough(messages, "")
}
