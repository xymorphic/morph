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

func TestLiveMemorySearchToolFindsMemoryFromDifferentSessionWithoutPreTurnRetrieval(t *testing.T) {
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
	cfg.Memory.Retrieval.Enabled = &disabled
	cfg.Memory.Episodic.Enabled = &disabled
	cfg.Memory.Reflection.Enabled = &disabled
	cfg.Memory.Promotion.Enabled = &disabled
	cfg.Normalize()
	require.True(t, cfg.MemoryWriteEnabled())
	require.False(t, cfg.MemoryRetrievalEnabled())

	modelClient, summaryClient, err := e2e.NewLiveClients(cfg)
	require.NoError(t, err)
	recordingModelClient := &recordingLiveModelClient{client: modelClient}

	harness, err := e2e.NewHarness(ctx, e2e.HarnessOptions{
		Spec:          spec,
		Config:        cfg,
		ModelClient:   recordingModelClient,
		SummaryClient: summaryClient,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, harness.Close())
	})

	seed, err := harness.Send(ctx, e2e.RootChatRequest{
		Message: strings.Join([]string{
			"For future chats, please remember this user preference:",
			"when writing live e2e status reports, use the project codename cobalt-ridge.",
		}, " "),
	})
	require.NoError(t, err)
	require.NotEmpty(t, seed.SessionID)
	requireLiveMemoryToolCall(t, ctx, harness, seed.SessionID, "memory_add")

	store := loadLiveMemoryStore(t, cfg, summaryClient)
	vectorIndex := loadLiveMemoryVectorIndex(t, cfg)
	waitForLiveMemoryAddSemanticMemory(t, ctx, store, vectorIndex, seed.SessionID)

	recallSessionID, err := storage.NewSessionID()
	require.NoError(t, err)
	manager := loadLiveMemoryStateManager(t, cfg, summaryClient)
	_, err = manager.CreateSession(ctx, recallSessionID)
	require.NoError(t, err)

	requestOffset := len(recordingModelClient.Requests())
	recall, err := harness.Send(ctx, e2e.RootChatRequest{
		SessionID: recallSessionID,
		Message: strings.Join([]string{
			"In a different chat I asked you to remember a live e2e status-report codename.",
			"What codename should I use now?",
			"Answer with the codename if you can find it.",
		}, " "),
	})
	require.NoError(t, err)
	require.Equal(t, recallSessionID, recall.SessionID)
	require.NotEqual(t, seed.SessionID, recall.SessionID)

	recallRequests := recordingModelClient.Requests()[requestOffset:]
	require.NotEmpty(t, recallRequests)
	require.NotContains(t, recallRequests[0].Instructions, "# Memory Context")
	require.NotContains(t, strings.ToLower(recallRequests[0].Instructions), "cobalt-ridge")
	requireLiveMemoryToolCallAtLeast(t, ctx, harness, recall.SessionID, "memory_search", 1)
	require.Contains(t, strings.ToLower(recall.Reply), "cobalt-ridge")
}
