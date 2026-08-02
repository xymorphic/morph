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

func TestLiveMemoryRetrievedInLaterTurn(t *testing.T) {
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

	seed, err := harness.Send(ctx, e2e.RootChatRequest{
		Message: strings.Join([]string{
			"Please remember this durable workflow for daemon-log reviews.",
			"When I ask you to review daemon logs, start the analysis with the exact heading Subsystem Timeline First.",
			"Then group events by subsystem, identify anomalies, explain the timeline with concrete timestamps, and only then propose fixes.",
			"Reply briefly that you will remember this workflow.",
		}, " "),
	})
	require.NoError(t, err)
	require.NotEmpty(t, seed.SessionID)

	store := loadLiveMemoryStore(t, cfg, summaryClient)
	vectorIndex := loadLiveMemoryVectorIndex(t, cfg)
	waitForLiveActiveMemoryContaining(t, ctx, store, vectorIndex, seed.SessionID, "subsystem timeline first")

	review, err := harness.Send(ctx, e2e.RootChatRequest{
		SessionID: seed.SessionID,
		Message: strings.Join([]string{
			"Review this daemon log excerpt and recommend next steps.",
			"Use any remembered daemon-log review workflow available to you, but do not ask me to restate it.",
			"2026-05-10T19:42:01Z api gateway returned 502 for /v1/chat.",
			"2026-05-10T19:42:04Z worker queue depth rose above 900.",
			"2026-05-10T19:42:06Z database pool wait exceeded 2s.",
			"2026-05-10T19:42:09Z api retry storm began.",
		}, " "),
	})
	require.NoError(t, err)
	require.Contains(t, strings.ToLower(review.Reply), "subsystem timeline first")
	require.Contains(t, strings.ToLower(review.Reply), "api")
	require.Contains(t, strings.ToLower(review.Reply), "database")
	require.Contains(t, strings.ToLower(review.Reply), "worker")
}

func waitForLiveActiveMemoryContaining(
	t *testing.T,
	ctx context.Context,
	store liveMemoryStore,
	vectorIndex liveMemoryVectorIndex,
	sessionID string,
	needle string,
) storage.MemoryItem {
	t.Helper()

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		item, ok := getLiveActiveMemoryContaining(ctx, t, store, sessionID, needle)
		if ok &&
			hasSessionEpisodicCheckpointComplete(t, ctx, store, sessionID) &&
			hasCurrentLiveMemoryVector(ctx, t, vectorIndex, item) {
			return item
		}

		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for active memory containing %q; memories: %s", needle, getLiveMemoryDump(context.Background(), t, store, sessionID))
		case <-ticker.C:
		}
	}
}

func getLiveActiveMemoryContaining(
	ctx context.Context,
	t *testing.T,
	store liveMemoryStore,
	sessionID string,
	needle string,
) (storage.MemoryItem, bool) {
	t.Helper()
	sessionIDValue := str.String(sessionID)
	result, err := store.SearchMemory(ctx, storage.MemorySearchQuery{
		SessionID: sessionIDValue.Trim(),
		Statuses:  []storage.MemoryStatus{storage.MemoryStatusActive},
		Limit:     20,
	})
	require.NoError(t, err)
	needleValue := str.String(needle)
	needle = needleValue.Normalized()
	for _, hit := range result.Hits {
		if strings.Contains(getLiveMemorySearchableText(hit.Item), needle) {
			return hit.Item, true
		}
	}

	return storage.MemoryItem{}, false
}

func getLiveMemorySearchableText(item storage.MemoryItem) string {
	values := []string{
		item.Title,
		item.Text,
		item.Metadata["procedural_trigger"],
		item.Metadata["procedural_steps"],
		item.Metadata["procedural_constraints"],
		item.Metadata["procedural_examples"],
		item.Metadata["procedural_expected_behavior"],
	}
	return strings.ToLower(strings.Join(values, " "))
}

func hasCurrentLiveMemoryVector(
	ctx context.Context,
	t *testing.T,
	vectorIndex liveMemoryVectorIndex,
	item storage.MemoryItem,
) bool {
	t.Helper()
	joinValue := str.String(strings.Join([]string{item.Title, item.Text}, "\n"))
	text := joinValue.Trim()
	if text == "" {
		return true
	}

	sourceID := search.StableMemoryItemID(item.ID)
	result, err := vectorIndex.lister.List(ctx, search.VectorListRequest{
		EmbeddingModel: vectorIndex.embeddingModel,
		Filter: search.VectorFilter{
			SourceKind: search.SourceKindMemoryItem,
			SourceIDs:  []string{sourceID},
		},
	})
	require.NoError(t, err)
	if len(result.Records) == 0 {
		return false
	}

	expectedHash := search.VectorContentHash(text)
	expectedTags := getLiveMemoryVectorTags(item)
	for _, record := range result.Records {
		if record.SourceID == sourceID &&
			record.ContentHash == expectedHash &&
			hasLiveMemoryVectorTags(record.Tags, expectedTags) {
			return true
		}
	}

	return false
}
