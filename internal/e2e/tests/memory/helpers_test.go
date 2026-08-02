package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/config"
	morphdb "github.com/xymorphic/morph/internal/db"
	e2e "github.com/xymorphic/morph/internal/e2e"
	models "github.com/xymorphic/morph/internal/model"
	storage "github.com/xymorphic/morph/internal/state/core"
	statemanager "github.com/xymorphic/morph/internal/state/manager"
	"github.com/xymorphic/morph/internal/state/search"
	vectorsqlite "github.com/xymorphic/morph/internal/state/search/vectorstore/sqlite"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/str"
)

type liveMemoryStore interface {
	storage.Store
	storage.SessionStore
	storage.MemoryStore
}

type liveMemoryVectorIndex struct {
	lister         search.VectorRecordLister
	embeddingModel string
}

func loadProductionConfigForLiveMemoryE2E(t *testing.T, spec e2e.HarnessSpec) *config.Config {
	t.Helper()

	cfg, err := config.Load("", filepath.Join(getRepoRoot(t), "config.yaml"))
	require.NoError(t, err)

	cfg.FS.Roots = []string{spec.Isolation.WorkspaceDir}
	return cfg
}

func setLiveMemoryE2EConfig(cfg *config.Config, spec e2e.HarnessSpec) {
	enabled := true
	disabled := false
	stream := false
	maxRetries := 0

	cfg.Cap.Memory = &disabled
	cfg.Cap.Filesystem = &disabled
	cfg.Cap.Network = &disabled
	cfg.Cap.Exec = &disabled
	cfg.Cap.Browser = &disabled
	cfg.Models.Main.Stream = &stream
	cfg.Models.MaxRetries = &maxRetries
	cfg.Trace.Enabled = false
	cfg.Trace.Disk.Enabled = &disabled
	cfg.Trace.Database.Enabled = &disabled
	cfg.Memory.Enabled = &enabled
	cfg.Memory.Pinned.Enabled = &disabled
	cfg.Memory.Episodic.Enabled = &enabled
	cfg.Memory.Episodic.Interval = 250 * time.Millisecond
	cfg.Memory.Episodic.IdleAfter = 100 * time.Millisecond
	cfg.Memory.Episodic.MinMessages = 2
	cfg.Memory.Episodic.WindowSize = 20
	cfg.Memory.Episodic.MaxWindows = 1
	cfg.Memory.Episodic.MaxWindowChars = 6000
	cfg.Memory.Episodic.MaxWindowTokens = 1500
	cfg.Memory.Episodic.MaxRetries = 1
	cfg.Memory.Reflection.Enabled = &enabled
	cfg.Memory.Reflection.Interval = 250 * time.Millisecond
	cfg.Memory.Reflection.Limit = 5
	cfg.Memory.Reflection.RelatedLimit = 1
	cfg.Memory.Promotion.Enabled = &enabled
	cfg.Memory.Promotion.Interval = 250 * time.Millisecond
	cfg.Memory.Promotion.Limit = 5
	cfg.Session.DefaultIdleExpiry = time.Hour
	cfg.Session.ArchiveRetention = time.Hour
	cfg.Trace.Disk.Dir = spec.Isolation.TraceDir
	cfg.Normalize()
}

func loadLiveMemoryStore(
	t *testing.T,
	cfg *config.Config,
	rerankerClient models.Client,
) liveMemoryStore {
	t.Helper()

	inspectCfg := *cfg
	store, err := statemanager.OpenStoreWithRerankerClient(&inspectCfg, rerankerClient)
	require.NoError(t, err)

	memoryStore, ok := store.(liveMemoryStore)
	require.True(t, ok)
	t.Cleanup(func() {
		closer, ok := store.(interface{ Close() error })
		if ok {
			require.NoError(t, closer.Close())
		}
	})
	return memoryStore
}

func loadLiveMemoryStateManager(
	t *testing.T,
	cfg *config.Config,
	rerankerClient models.Client,
) *statemanager.Manager {
	t.Helper()

	inspectCfg := *cfg
	store, err := statemanager.OpenStoreWithRerankerClient(&inspectCfg, rerankerClient)
	require.NoError(t, err)

	manager, err := statemanager.NewManager(store, cfg.Session.DefaultIdleExpiry, cfg.Session.ArchiveRetention)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, manager.Close())
	})
	return manager
}

func loadLiveMemoryVectorIndex(t *testing.T, cfg *config.Config) liveMemoryVectorIndex {
	t.Helper()

	db, err := morphdb.Open(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
	})

	vectorStore, err := vectorsqlite.NewStoreFromDB(db)
	require.NoError(t, err)
	nameValue := str.String(cfg.Models.Embedding.Name)
	return liveMemoryVectorIndex{
		lister:         vectorStore,
		embeddingModel: nameValue.Trim(),
	}
}

func requireNoLiveMemoryToolUsage(
	t *testing.T,
	ctx context.Context,
	harness *e2e.Harness,
	sessionID string,
) {
	t.Helper()

	messages, err := harness.Messages(ctx, sessionID)
	require.NoError(t, err)
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			require.Falsef(t, isLiveMemoryToolName(call.Name), "unexpected memory tool call %q", call.Name)
		}
		if message.Role == morphmsg.RoleTool {
			require.Falsef(t, isLiveMemoryToolName(message.Name), "unexpected memory tool result %q", message.Name)
		}
	}
}

func isLiveMemoryToolName(name string) bool {
	nameValue2 := str.String(name)
	return strings.HasPrefix(nameValue2.Trim(), "memory_")
}

func waitForLiveProceduralMemory(
	t *testing.T,
	ctx context.Context,
	store liveMemoryStore,
	vectorIndex liveMemoryVectorIndex,
	sessionID string,
) storage.MemoryItem {
	t.Helper()

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		item, ok := getLiveProceduralMemory(ctx, t, store, sessionID)
		if ok &&
			!item.PromotionEvaluatedAt.IsZero() &&
			hasNoPendingLiveMemoryPromotion(ctx, t, store, sessionID) &&
			hasSessionEpisodicCheckpointComplete(t, ctx, store, sessionID) &&
			hasCurrentLiveMemoryVectors(ctx, t, store, vectorIndex, sessionID) {
			return item
		}

		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for active procedural memory; memories: %s", getLiveMemoryDump(context.Background(), t, store, sessionID))
		case <-ticker.C:
		}
	}
}

func waitForLiveSemanticMemoryContaining(
	t *testing.T,
	ctx context.Context,
	store liveMemoryStore,
	vectorIndex liveMemoryVectorIndex,
	sessionID string,
	required ...string,
) storage.MemoryItem {
	t.Helper()

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		item, ok := getLiveSemanticMemoryContaining(ctx, t, store, sessionID, required...)
		if ok &&
			!item.PromotionEvaluatedAt.IsZero() &&
			hasNoPendingLiveMemoryPromotion(ctx, t, store, sessionID) &&
			hasSessionEpisodicCheckpointComplete(t, ctx, store, sessionID) &&
			hasCurrentLiveMemoryVectors(ctx, t, store, vectorIndex, sessionID) {
			return item
		}

		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for active semantic memory; memories: %s", getLiveMemoryDump(context.Background(), t, store, sessionID))
		case <-ticker.C:
		}
	}
}

func hasNoPendingLiveMemoryPromotion(
	ctx context.Context,
	t *testing.T,
	store liveMemoryStore,
	sessionID string,
) bool {
	t.Helper()
	sessionIDValue := str.String(sessionID)
	result, err := store.SearchMemory(ctx, storage.MemorySearchQuery{
		SessionID: sessionIDValue.Trim(),
		Statuses: []storage.MemoryStatus{
			storage.MemoryStatusCandidate,
			storage.MemoryStatusActive,
		},
		Limit: 50,
	})
	require.NoError(t, err)
	for _, hit := range result.Hits {
		item := hit.Item
		if item.Status == storage.MemoryStatusCandidate && item.PromotionEvaluatedAt.IsZero() {
			return false
		}
	}

	return true
}

func hasCurrentLiveMemoryVectors(
	ctx context.Context,
	t *testing.T,
	store liveMemoryStore,
	vectorIndex liveMemoryVectorIndex,
	sessionID string,
) bool {
	t.Helper()

	items := loadLiveSessionMemoryItems(ctx, t, store, sessionID)
	if len(items) == 0 {
		return false
	}

	sourceIDs := make([]string, 0, len(items))
	expectedHashes := make(map[string]string, len(items))
	expectedTags := make(map[string][]string, len(items))
	for _, item := range items {
		joinValue := str.String(strings.Join([]string{item.Title, item.Text}, "\n"))
		text := joinValue.Trim()
		if text == "" {
			continue
		}

		sourceID := search.StableMemoryItemID(item.ID)
		sourceIDs = append(sourceIDs, sourceID)
		expectedHashes[sourceID] = search.VectorContentHash(text)
		expectedTags[sourceID] = getLiveMemoryVectorTags(item)
	}
	if len(sourceIDs) == 0 {
		return true
	}

	result, err := vectorIndex.lister.List(ctx, search.VectorListRequest{
		EmbeddingModel: vectorIndex.embeddingModel,
		Filter: search.VectorFilter{
			SourceKind: search.SourceKindMemoryItem,
			SourceIDs:  sourceIDs,
		},
	})
	require.NoError(t, err)

	recordsBySourceID := make(map[string]search.VectorRecord, len(result.Records))
	for _, record := range result.Records {
		recordsBySourceID[record.SourceID] = record
	}
	for _, sourceID := range sourceIDs {
		record, ok := recordsBySourceID[sourceID]
		if !ok ||
			record.ContentHash != expectedHashes[sourceID] ||
			!hasLiveMemoryVectorTags(record.Tags, expectedTags[sourceID]) {
			return false
		}
	}

	return true
}

func getLiveMemoryVectorTags(item storage.MemoryItem) []string {
	return search.MemoryVectorTags(item)
}

func hasLiveMemoryVectorTags(actual []string, expected []string) bool {
	actualSet := make(map[string]struct{}, len(actual))
	for _, tag := range search.NormalizeVectorTags(actual) {
		actualSet[tag] = struct{}{}
	}
	for _, tag := range expected {
		if _, ok := actualSet[tag]; !ok {
			return false
		}
	}

	return true
}

func loadLiveSessionMemoryItems(
	ctx context.Context,
	t *testing.T,
	store liveMemoryStore,
	sessionID string,
) []storage.MemoryItem {
	t.Helper()
	sessionIDValue2 := str.String(sessionID)
	result, err := store.SearchMemory(ctx, storage.MemorySearchQuery{
		SessionID: sessionIDValue2.Trim(),
		Statuses: []storage.MemoryStatus{
			storage.MemoryStatusCandidate,
			storage.MemoryStatusActive,
		},
		Limit: 50,
	})
	require.NoError(t, err)

	items := make([]storage.MemoryItem, 0, len(result.Hits))
	for _, hit := range result.Hits {
		items = append(items, hit.Item)
	}

	return items
}

func getLiveProceduralMemory(
	ctx context.Context,
	t *testing.T,
	store liveMemoryStore,
	sessionID string,
) (storage.MemoryItem, bool) {
	t.Helper()
	sessionIDValue3 := str.String(sessionID)
	result, err := store.SearchMemory(ctx, storage.MemorySearchQuery{
		SessionID: sessionIDValue3.Trim(),
		Kinds:     []storage.MemoryKind{storage.MemoryKindProcedural},
		Statuses:  []storage.MemoryStatus{storage.MemoryStatusActive},
		Limit:     10,
	})
	require.NoError(t, err)
	for _, hit := range result.Hits {
		if hasLiveDaemonLogReviewMemoryText(hit.Item) {
			return hit.Item, true
		}
	}

	return storage.MemoryItem{}, false
}

func getLiveSemanticMemoryContaining(
	ctx context.Context,
	t *testing.T,
	store liveMemoryStore,
	sessionID string,
	required ...string,
) (storage.MemoryItem, bool) {
	t.Helper()
	sessionIDValue4 := str.String(sessionID)
	result, err := store.SearchMemory(ctx, storage.MemorySearchQuery{
		SessionID: sessionIDValue4.Trim(),
		Kinds:     []storage.MemoryKind{storage.MemoryKindSemantic},
		Statuses:  []storage.MemoryStatus{storage.MemoryStatusActive},
		Limit:     10,
	})
	require.NoError(t, err)
	for _, hit := range result.Hits {
		if hasLiveMemoryText(hit.Item, required...) {
			return hit.Item, true
		}
	}

	return storage.MemoryItem{}, false
}

func waitForLiveBackgroundEpisodicMemory(
	t *testing.T,
	ctx context.Context,
	store liveMemoryStore,
	sessionID string,
) storage.MemoryItem {
	t.Helper()

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		item, ok := getLiveBackgroundEpisodicMemory(ctx, t, store, sessionID)
		if ok &&
			hasSessionEpisodicCheckpointComplete(t, ctx, store, sessionID) &&
			item.Reflected &&
			!item.PromotionEvaluatedAt.IsZero() {
			return item
		}

		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for background episodic memory; memories: %s", getLiveMemoryDump(context.Background(), t, store, sessionID))
		case <-ticker.C:
		}
	}
}

func getLiveBackgroundEpisodicMemory(
	ctx context.Context,
	t *testing.T,
	store liveMemoryStore,
	sessionID string,
) (storage.MemoryItem, bool) {
	t.Helper()
	sessionIDValue5 := str.String(sessionID)
	result, err := store.SearchMemory(ctx, storage.MemorySearchQuery{
		SessionID: sessionIDValue5.Trim(),
		Kinds:     []storage.MemoryKind{storage.MemoryKindEpisodic},
		Statuses:  []storage.MemoryStatus{storage.MemoryStatusCandidate, storage.MemoryStatusActive},
		Limit:     10,
	})
	require.NoError(t, err)
	for _, hit := range result.Hits {
		if getMemorySourceCreatedBy(hit.Item) == "background" {
			return hit.Item, true
		}
	}

	return storage.MemoryItem{}, false
}

func hasLiveDaemonLogReviewMemoryText(item storage.MemoryItem) bool {
	return hasLiveMemoryText(item, "daemon", "log", "review")
}

func hasLiveMemoryText(item storage.MemoryItem, required ...string) bool {
	text := strings.ToLower(strings.Join([]string{
		item.Title,
		item.Text,
		item.Metadata["procedural_trigger"],
		item.Metadata["procedural_steps"],
		item.Metadata["procedural_expected_behavior"],
	}, " "))

	for _, value := range required {
		valueText := str.String(value)
		if !strings.Contains(text, valueText.Normalized()) {
			return false
		}
	}

	return true
}

func getLiveMemoryDump(ctx context.Context, t *testing.T, store liveMemoryStore, sessionID string) string {
	t.Helper()
	sessionIDValue6 := str.String(sessionID)
	result, err := store.SearchMemory(ctx, storage.MemorySearchQuery{
		SessionID: sessionIDValue6.Trim(),
		Statuses:  []storage.MemoryStatus{storage.MemoryStatusCandidate, storage.MemoryStatusActive},
		Limit:     20,
	})
	if err != nil {
		return err.Error()
	}

	parts := make([]string, 0, len(result.Hits))
	for _, hit := range result.Hits {
		item := hit.Item
		parts = append(parts, string(item.Kind)+":"+string(item.Status)+":"+item.Title+":"+item.Text)
	}

	return strings.Join(parts, " | ")
}

func getMemorySourceCreatedBy(item storage.MemoryItem) string {
	for _, link := range item.SourceLinks {
		createdByValue := str.String(link.CreatedBy)
		if createdBy := createdByValue.Trim(); createdBy != "" {
			return createdBy
		}
	}

	return ""
}

func getSessionEpisodicCheckpoint(
	t *testing.T,
	ctx context.Context,
	store liveMemoryStore,
	sessionID string,
) int {
	t.Helper()
	sessionIDValue7 := str.String(sessionID)
	session, ok, err := store.Get(ctx, sessionIDValue7.Trim(), storage.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	return session.EpisodicCheckpointOffset
}

func hasSessionEpisodicCheckpointComplete(
	t *testing.T,
	ctx context.Context,
	store liveMemoryStore,
	sessionID string,
) bool {
	t.Helper()
	sessionIDValue8 := str.String(sessionID)
	sessionID = sessionIDValue8.Trim()
	checkpoint := getSessionEpisodicCheckpoint(t, ctx, store, sessionID)
	count, err := store.CountMessages(ctx, sessionID, storage.MessageQueryOptions{})
	require.NoError(t, err)
	return checkpoint >= count
}

func getRepoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if hasRepoRootFiles(dir) {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root not found")
		}
		dir = parent
	}
}

func hasRepoRootFiles(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "config.yaml")); err != nil {
		return false
	}

	return true
}
