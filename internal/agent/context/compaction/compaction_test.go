package compaction

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	models "github.com/xymorphic/morph/internal/model"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
)

func TestEstimateTextRough_ReturnsZeroWhenEmpty(t *testing.T) {
	require.Zero(t, EstimateTextRough(""))
}

func TestEstimateCharsFromTokensRough_ReturnsZeroWhenNonPositive(t *testing.T) {
	require.Zero(t, EstimateCharsFromTokensRough(0))
	require.Zero(t, EstimateCharsFromTokensRough(-1))
}

func TestEstimateCharsFromTokensRough_UsesFourCharsPerToken(t *testing.T) {
	require.Equal(t, 48, EstimateCharsFromTokensRough(12))
}

func TestEstimateRequestRough_IncludesInstructionsMessagesAndTools(t *testing.T) {
	req := models.Request{
		Instructions: "follow the instructions",
		Messages: []morphmsg.Message{{
			Role:    morphmsg.RoleUser,
			Content: "hello world",
		}},
		Tools: []models.ToolDefinition{{
			Name:        "search_files",
			Description: "Search files",
			InputSchema: map[string]any{"type": "object"},
		}},
	}

	estimate := EstimateRequestRough(req)
	require.Positive(t, estimate)
	require.Equal(t, estimate, EstimateRequestRough(req))
}

func TestEvaluator_UsesActualPromptTokensWhenAvailable(t *testing.T) {
	evaluator := NewEvaluator(100, 0.5, 0.9)
	req := models.Request{Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}}}

	estimate := evaluator.Evaluate(req, Anchor{PromptTokens: 77, MessageCount: 1})
	require.Equal(t, ActualSource, estimate.Source)
	require.Equal(t, 77, estimate.PromptTokens)
	require.Equal(t, 77, estimate.AnchorPromptTokens)
	require.Equal(t, 1, estimate.AnchorMessageCount)
	require.Zero(t, estimate.DeltaPromptTokens)
	require.Equal(t, 50, estimate.TriggerThreshold)
	require.Equal(t, 90, estimate.WarnThreshold)
	require.True(t, estimate.Triggered())
	require.False(t, estimate.Warning())
}

func TestEvaluator_FallsBackToEstimatedPromptTokens(t *testing.T) {
	evaluator := NewEvaluator(100, 0.5, 0.6)
	req := models.Request{
		Messages: []morphmsg.Message{{
			Role:    morphmsg.RoleUser,
			Content: "abcdefgh",
		}},
	}

	estimate := evaluator.Evaluate(req, Anchor{})
	require.Equal(t, EstimatedSource, estimate.Source)
	require.Equal(t, EstimateRequestRough(req), estimate.PromptTokens)
}

func TestEvaluator_AnchorsIncidentUsageAndEstimatesOnlyAppendedMessages(t *testing.T) {
	evaluator := NewEvaluator(128000, 0.85, 0.95)
	req := models.Request{
		Instructions: strings.Repeat("a", 500000),
		Messages: []morphmsg.Message{{
			Role:    morphmsg.RoleUser,
			Content: "provider measured this message",
		}, {
			Role:    morphmsg.RoleTool,
			Content: "small appended result",
		}},
	}

	estimate := evaluator.Evaluate(req, Anchor{PromptTokens: 65350, MessageCount: 1})
	require.Equal(t, AnchoredSource, estimate.Source)
	require.Equal(t, 65350, estimate.AnchorPromptTokens)
	require.Equal(t, 1, estimate.AnchorMessageCount)
	require.Positive(t, estimate.DeltaPromptTokens)
	require.Equal(t, 65350+estimate.DeltaPromptTokens, estimate.PromptTokens)
	require.Greater(t, EstimateRequestRough(req), estimate.TriggerThreshold)
	require.False(t, estimate.Triggered())

	req.Instructions = ""
	withoutInflatedInstructions := evaluator.Evaluate(req, Anchor{PromptTokens: 65350, MessageCount: 1})
	require.Equal(t, estimate.PromptTokens, withoutInflatedInstructions.PromptTokens)
	require.Equal(t, estimate.DeltaPromptTokens, withoutInflatedInstructions.DeltaPromptTokens)
}

func TestEvaluator_AnchoredUsageCanCrossTrigger(t *testing.T) {
	evaluator := NewEvaluator(128000, 0.85, 0.95)
	req := models.Request{Messages: []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "measured"},
		{Role: morphmsg.RoleTool, Content: strings.Repeat("x", 400)},
	}}

	estimate := evaluator.Evaluate(req, Anchor{PromptTokens: 108790, MessageCount: 1})
	require.Equal(t, AnchoredSource, estimate.Source)
	require.True(t, estimate.Triggered())
}

func TestEvaluator_AnchoredUsageReportsWarningAtBoundary(t *testing.T) {
	evaluator := NewEvaluator(1000, 0.9, 0.5)
	req := models.Request{Messages: []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "measured"},
		{Role: morphmsg.RoleTool, Content: "appended"},
	}}
	estimate := evaluator.Evaluate(req, Anchor{PromptTokens: 490, MessageCount: 1})
	require.GreaterOrEqual(t, estimate.PromptTokens, 500)
	require.Less(t, estimate.PromptTokens, 900)
	require.True(t, estimate.Warning())
	require.False(t, estimate.Triggered())
}

func TestEvaluator_InvalidAnchorFallsBackToFullEstimate(t *testing.T) {
	evaluator := NewEvaluator(100, 0.5, 0.7)
	req := models.Request{Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}}}

	estimate := evaluator.Evaluate(req, Anchor{PromptTokens: 10, MessageCount: 2})
	require.Equal(t, EstimatedSource, estimate.Source)
	require.Equal(t, EstimateRequestRough(req), estimate.PromptTokens)
	require.Zero(t, estimate.AnchorPromptTokens)
	require.Zero(t, estimate.DeltaPromptTokens)
}

func TestNewEvaluator_DefaultsInvalidInputs(t *testing.T) {
	evaluator := NewEvaluator(0, 0, 0)

	estimate := evaluator.Evaluate(models.Request{}, Anchor{})
	require.Equal(t, 128000, estimate.ContextLimit)
	require.Equal(t, 108800, estimate.TriggerThreshold)
	require.Equal(t, 121600, estimate.WarnThreshold)
}

func TestEstimateRequestRough_FallsBackToInstructionsWhenJSONMarshalFails(t *testing.T) {
	req := models.Request{
		Instructions: "hello",
		Tools: []models.ToolDefinition{{
			Name:        "bad_tool",
			Description: "contains unsupported schema value",
			InputSchema: map[string]any{"broken": func() {}},
		}},
	}

	require.Equal(t, EstimateTextRough("hello"), EstimateRequestRough(req))
}

func TestEvaluator_NilReceiverUsesDefaults(t *testing.T) {
	var evaluator *Evaluator

	estimate := evaluator.Evaluate(models.Request{Instructions: "hello"}, Anchor{})
	require.Equal(t, EstimatedSource, estimate.Source)
	require.Equal(t, EstimateRequestRough(models.Request{Instructions: "hello"}), estimate.PromptTokens)
	require.Equal(t, 128000, estimate.ContextLimit)
}

func TestEstimate_WarningAndTriggeredRequirePositiveThresholds(t *testing.T) {
	estimate := Estimate{PromptTokens: 10}
	require.False(t, estimate.Warning())
	require.False(t, estimate.Triggered())
}
