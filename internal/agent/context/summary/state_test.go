package summary

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/config"
	modelprovider "github.com/xymorphic/morph/internal/model/provider"
	storage "github.com/xymorphic/morph/internal/state/core"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
)

func TestNewService_LogsWhenSummaryProviderAndAPIDifferFromMain(t *testing.T) {
	cfg := &config.Config{
		Name: "t",
		Models: config.ModelsConfig{
			Main: config.MainModelConfig{
				Name:          "openai/gpt-4o-mini",
				Provider:      "openrouter",
				API:           modelprovider.APIOpenAICompletions,
				ContextLength: 100,
			},
			Summary: config.SummaryModelConfig{Provider: "openai", API: modelprovider.APIOpenAIResponses},
		},
	}
	cfg.Normalize()

	svc := NewService(cfg, nil, nil, nil)
	require.NotNil(t, svc)
}

func TestState_Summary_ReturnsZeroValueWithoutCurrentSummary(t *testing.T) {
	require.Equal(t, storage.SessionSummary{}, (*State)(nil).Summary())
	require.Equal(t, storage.SessionSummary{}, (&State{}).Summary())
}

func TestState_Summary_ClonesCurrentSummary(t *testing.T) {
	state := &State{
		Current: &SummaryState{
			SessionID:          "ses_test",
			SourceEndOffset:    2,
			SourceMessageCount: 5,
			UpdatedAt:          time.Now().UTC(),
			SessionSummary:     "Older work",
			CurrentTask:        "Fix tests",
			Discoveries:        []string{"one"},
			OpenQuestions:      []string{"two"},
			NextActions:        []string{"three"},
		},
	}

	stored := state.Summary()
	require.Equal(t, "ses_test", stored.SessionID)
	require.Equal(t, "Older work", stored.SessionSummary)

	state.Current.Discoveries[0] = "changed"
	require.Equal(t, "one", stored.Discoveries[0])
}

func TestState_RenderSummaryInstructions(t *testing.T) {
	state := &State{
		Current: &SummaryState{
			SessionSummary: "Older work",
			CurrentTask:    "Fix tests",
			Discoveries:    []string{"one"},
			OpenQuestions:  []string{"two"},
			NextActions:    []string{"three"},
		},
	}

	message, ok := state.RenderSummaryInstructions()
	require.True(t, ok)
	require.Contains(t, message, "# Session Summary\n\nOlder work")
	require.Contains(t, message, "# Current Task\n\nFix tests")
	require.Contains(t, message, "# Discoveries\n\n- one")
	require.Contains(t, message, "# Open Questions\n\n- two")
	require.Contains(t, message, "# Next Actions\n\n- three")
}

func TestState_RenderSummaryInstructions_ReturnsFalseWhenUnavailable(t *testing.T) {
	message, ok := (*State)(nil).RenderSummaryInstructions()
	require.False(t, ok)
	require.Empty(t, message)

	message, ok = (&State{}).RenderSummaryInstructions()
	require.False(t, ok)
	require.Empty(t, message)

	message, ok = (&State{Current: &SummaryState{SessionSummary: "   "}}).RenderSummaryInstructions()
	require.False(t, ok)
	require.Empty(t, message)
}

func TestState_Recall(t *testing.T) {
	history := []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "old"},
		{Role: morphmsg.RoleAssistant, Content: "recent"},
	}

	recall := (&State{Current: &SummaryState{
		SourceEndOffset: 1,
		SessionSummary:  "Older work",
	}}).Recall(history)

	require.Empty(t, recall.PrefixMessages)
	require.Equal(t, history, recall.SessionHistory)
}

func TestState_Recall_DefaultsForNilAndPreservesHistoryWithSummary(t *testing.T) {
	history := []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "history"}}

	recall := (*State)(nil).Recall(history)
	require.Empty(t, recall.PrefixMessages)
	require.Equal(t, history, recall.SessionHistory)

	recall = (&State{Current: &SummaryState{SourceEndOffset: 99, SessionSummary: "Older work"}}).Recall(history)
	require.Empty(t, recall.PrefixMessages)
	require.Equal(t, history, recall.SessionHistory)
}
