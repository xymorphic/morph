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
	"github.com/xymorphic/morph/pkg/str"
)

func TestLiveEpisodicMemoryCreatedByBackgroundProcess(t *testing.T) {
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
	require.NotNil(t, cfg.Cap.Memory)
	require.False(t, *cfg.Cap.Memory)
	require.True(t, cfg.Search.Vector.Enabled)
	require.True(t, cfg.Search.Vector.Required)
	require.True(t, *cfg.Search.EnableRerank)
	require.True(t, *cfg.Reranker.Enabled)

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
		Message: strings.Join([]string{
			"Record this as an important project milestone:",
			"the team confirmed that live e2e memory tests must exercise the real LLM,",
			"vector retrieval, reranking, background episodic extraction, reflection, and promotion.",
			"Reply briefly without using tools.",
		}, " "),
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.SessionID)
	requireNoLiveMemoryToolUsage(t, ctx, harness, result.SessionID)

	store := loadLiveMemoryStore(t, cfg, summaryClient)
	item := waitForLiveBackgroundEpisodicMemory(t, ctx, store, result.SessionID)
	require.Equal(t, storage.MemoryKindEpisodic, item.Kind)
	require.Equal(t, storage.MemoryStatusActive, item.Status)
	require.NotEmpty(t, item.Text)
	require.Equal(t, "background", getMemorySourceCreatedBy(item))
	require.True(t, item.Reflected)
	require.False(t, item.PromotionEvaluatedAt.IsZero())
	require.NotZero(t, getSessionEpisodicCheckpoint(t, ctx, store, result.SessionID))
	require.True(t, hasSessionEpisodicCheckpointComplete(t, ctx, store, result.SessionID))
}
