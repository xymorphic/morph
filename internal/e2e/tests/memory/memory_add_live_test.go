package memory

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	e2e "github.com/xymorphic/morph/internal/e2e"
	storage "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/internal/state/search"
	"github.com/xymorphic/morph/pkg/str"
)

func TestLiveMemoryAddToolCreatesActiveSemanticMemory(t *testing.T) {
	envValue := str.String(os.Getenv("MORPH_E2E_LIVE"))
	if envValue.Trim() != "1" {
		t.Skip("set MORPH_E2E_LIVE=1 to run live LLM e2e tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	home := t.TempDir()
	spec := e2e.DefaultSpec(home)
	cfg := loadProductionConfigForLiveMemoryE2E(t, spec)
	setLiveMemoryE2EConfig(cfg, spec)
	enabled := true
	disabled := false
	cfg.Cap.Memory = &enabled
	cfg.Memory.Write.Enabled = &enabled
	cfg.Memory.Episodic.Enabled = &disabled
	cfg.Memory.Reflection.Enabled = &disabled
	cfg.Memory.Promotion.Enabled = &disabled
	cfg.Normalize()
	require.True(t, cfg.MemoryWriteEnabled())
	require.NotNil(t, cfg.Cap.Memory)
	require.True(t, *cfg.Cap.Memory)

	modelClient, summaryClient, err := e2e.NewLiveClients(cfg)
	require.NoError(t, err)

	harness, err := e2e.NewHarness(ctx, e2e.HarnessOptions{
		Spec:          spec,
		Config:        cfg,
		ModelClient:   modelClient,
		SummaryClient: summaryClient,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, harness.Close())
	})

	result, err := harness.Send(ctx, e2e.RootChatRequest{
		SessionID: storage.DefaultSessionID,
		Message: strings.Join([]string{
			"For future chats, please remember this user preference:",
			"when writing live e2e status reports, use the project codename cobalt-ridge.",
		}, " "),
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.SessionID)

	requireLiveMemoryToolCall(t, ctx, harness, result.SessionID, "memory_add")

	store := loadLiveMemoryStore(t, cfg, summaryClient)
	vectorIndex := loadLiveMemoryVectorIndex(t, cfg)
	item := waitForLiveMemoryAddSemanticMemory(t, ctx, store, vectorIndex, result.SessionID)
	require.Equal(t, storage.MemoryKindSemantic, item.Kind)
	require.Equal(t, storage.MemoryStatusActive, item.Status)
	require.False(t, item.PromotionEvaluatedAt.IsZero())
	require.Equal(t, result.SessionID, search.MemoryVectorSessionID(item))
	require.Equal(t, "approved", item.Metadata["promotion_decision_reason"])
	require.Contains(t, strings.ToLower(getLiveMemorySearchableText(item)), "cobalt-ridge")
}
