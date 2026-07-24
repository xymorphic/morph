package permissions

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGetApprovalPrompt_UsesExplicitOperationsAndSeparatesLegacyReason(t *testing.T) {
	prompt := GetApprovalPrompt(
		"browser · approve 2 operations",
		[]string{"network", "write"},
		"Website access requires approval. Approve all 2 operations: legacy one; legacy two",
		[]string{" browser · update browser ", "browser · read network"},
		time.Date(2026, 7, 24, 12, 3, 0, 0, time.UTC),
	)

	require.Equal(t, "browser · approve 2 operations", prompt.Summary)
	require.Equal(t, []string{"network", "write"}, prompt.Effects)
	require.Equal(t, "Website access requires approval.", prompt.Reason)
	require.Equal(t, []string{"browser · update browser", "browser · read network"}, prompt.Operations)
}

func TestGetApprovalPrompt_FallsBackToLegacyOperations(t *testing.T) {
	prompt := GetApprovalPrompt(
		"browser · approve 2 operations",
		nil,
		"Website access requires approval. Approve all 2 operations: browser · update browser; browser · read network",
		nil,
		time.Time{},
	)

	require.Equal(t, "Website access requires approval.", prompt.Reason)
	require.Equal(t, []string{"browser · update browser", "browser · read network"}, prompt.Operations)
}

func TestGetApprovalChoices_RestrictsUnsafeAlwaysScope(t *testing.T) {
	safe := GetApprovalChoices([]string{string(EffectRead)})
	require.Equal(t, []rune{'y', 's', 'a', 'n'}, getApprovalChoiceKeys(safe))

	unsafe := GetApprovalChoices([]string{string(EffectExecution)})
	require.Equal(t, []rune{'y', 's', 'n'}, getApprovalChoiceKeys(unsafe))

	choice, ok := GetApprovalChoiceByKey(unsafe, 's')
	require.True(t, ok)
	require.True(t, choice.Approved)
	require.Equal(t, GrantSession, choice.Scope)
}

func TestGetApprovalExpiryText_RoundsUpToWholeMinutes(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)

	require.Empty(t, GetApprovalExpiryText(time.Time{}, now))
	require.Equal(t, "3m", GetApprovalExpiryText(now.Add(3*time.Minute), now))
	require.Equal(t, "3m", GetApprovalExpiryText(now.Add(2*time.Minute+time.Second), now))
	require.Equal(t, "1m", GetApprovalExpiryText(now.Add(time.Second), now))
	require.Equal(t, "expired", GetApprovalExpiryText(now, now))
}

func getApprovalChoiceKeys(choices []ApprovalChoice) []rune {
	keys := make([]rune, len(choices))
	for index, choice := range choices {
		keys[index] = choice.Key
	}

	return keys
}
