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

func TestLiveMemoryDeleteToolDeletesActiveSemanticMemory(t *testing.T) {
	envValue := str.String(os.Getenv("MORPH_E2E_LIVE"))
	if envValue.Trim() != "1" {
		t.Skip("set MORPH_E2E_LIVE=1 to run live LLM e2e tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
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

	seed, err := harness.Send(ctx, e2e.RootChatRequest{
		SessionID: storage.DefaultSessionID,
		Message: strings.Join([]string{
			"For future chats, please remember this user preference:",
			"when writing live e2e status reports, use the project codename cobalt-ridge.",
		}, " "),
	})
	require.NoError(t, err)
	require.NotEmpty(t, seed.SessionID)
	requireLiveMemoryToolCallCount(t, ctx, harness, seed.SessionID, "memory_add", 1)

	store := loadLiveMemoryStore(t, cfg, summaryClient)
	vectorIndex := loadLiveMemoryVectorIndex(t, cfg)
	item := waitForLiveMemoryAddSemanticMemory(t, ctx, store, vectorIndex, seed.SessionID)

	deleted, err := harness.Send(ctx, e2e.RootChatRequest{
		SessionID: seed.SessionID,
		Message:   "Please forget the remembered live e2e status-report codename preference. I do not want that project codename preference retained anymore.",
	})
	require.NoError(t, err)
	require.Equal(t, seed.SessionID, deleted.SessionID)
	requireLiveMemoryToolCallCount(t, ctx, harness, deleted.SessionID, "memory_add", 1)
	requireLiveMemoryToolCallCount(t, ctx, harness, deleted.SessionID, "memory_delete", 1)

	deletedItem := waitForLiveMemoryStatus(t, ctx, store, item.ID, storage.MemoryStatusDeleted)
	require.Equal(t, storage.MemoryStatusDeleted, deletedItem.Status)
	require.False(t, hasLiveActiveMemory(ctx, t, store, deleted.SessionID, "cobalt-ridge"))
}
