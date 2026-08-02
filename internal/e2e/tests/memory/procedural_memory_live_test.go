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
	"github.com/xymorphic/morph/pkg/logutils"
	"github.com/xymorphic/morph/pkg/str"
)

func TestLiveProceduralMemoryCreatedFromProductionConfig(t *testing.T) {
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
			"Please remember this future operating rule for daemon-log reviews.",
			"When I ask you to review daemon logs, first group events by subsystem,",
			"then identify anomalies, then explain the timeline, then propose fixes.",
			"Always cite concrete timestamps and do not jump to fixes before explaining the timeline.",
			"Reply briefly that you will remember this workflow.",
		}, " "),
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.SessionID)

	store := loadLiveMemoryStore(t, cfg, summaryClient)
	vectorIndex := loadLiveMemoryVectorIndex(t, cfg)
	item := waitForLiveProceduralMemory(t, ctx, store, vectorIndex, result.SessionID)
	logutils.PrettyPrint(item)
	require.Equal(t, storage.MemoryKindProcedural, item.Kind)
	require.Equal(t, storage.MemoryStatusActive, item.Status)
	require.False(t, item.PromotionEvaluatedAt.IsZero())
	require.NotEmpty(t, item.Metadata["procedural_trigger"])
	require.NotEmpty(t, item.Metadata["procedural_steps"])
	memoryText := strings.ToLower(strings.Join([]string{
		item.Title,
		item.Text,
		item.Metadata["procedural_trigger"],
		item.Metadata["procedural_steps"],
	}, " "))
	require.Contains(t, memoryText, "daemon")
	require.Contains(t, memoryText, "log")
	require.Contains(t, memoryText, "review")
}
