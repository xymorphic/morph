package memory

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	e2e "github.com/xymorphic/morph/internal/e2e"
	livememory "github.com/xymorphic/morph/internal/memory"
	models "github.com/xymorphic/morph/internal/model"
	"github.com/xymorphic/morph/internal/profile"
	storage "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/pkg/str"
)

func TestLiveSemanticMemoryCreatedByReflectionAndPromotion(t *testing.T) {
	envValue := str.String(os.Getenv("MORPH_E2E_LIVE"))
	if envValue.Trim() != "1" {
		t.Skip("set MORPH_E2E_LIVE=1 to run live LLM e2e tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	home := t.TempDir()
	setProfileHome(t, home)

	spec := e2e.DefaultSpec(home)
	cfg := loadProductionConfigForLiveMemoryE2E(t, spec)
	setLiveMemoryE2EConfig(cfg, spec)
	require.True(t, cfg.Search.Vector.Enabled)
	require.True(t, cfg.Search.Vector.Required)
	require.True(t, *cfg.Search.EnableRerank)
	require.True(t, *cfg.Reranker.Enabled)

	modelClient, summaryClient, err := e2e.NewLiveClients(cfg)
	require.NoError(t, err)
	recordingModelClient := &recordingLiveModelClient{client: modelClient}

	manager := loadLiveMemoryStateManager(t, cfg, summaryClient)
	_, found, err := manager.Get(ctx, storage.DefaultSessionID, storage.SessionGetOptions{})
	require.NoError(t, err)
	if !found {
		_, err = manager.CreateSession(ctx, storage.DefaultSessionID)
		require.NoError(t, err)
	}
	provider, err := livememory.NewFromManager(manager, livememory.Options{
		StateManager:  manager,
		ModelClient:   recordingModelClient,
		Model:         cfg.SummaryModelEffective(),
		API:           cfg.SummaryModelAPIEffective(),
		DebugRequests: cfg.Debug.Requests,
		ReflectionBackground: livememory.ReflectionBackgroundOptions{
			Enabled:      true,
			Interval:     cfg.Memory.Reflection.Interval,
			Limit:        cfg.Memory.Reflection.Limit,
			RelatedLimit: cfg.Memory.Reflection.RelatedLimit,
		},
		PromotionBackground: livememory.PromotionBackgroundOptions{
			Enabled:  true,
			Interval: cfg.Memory.Promotion.Interval,
			Limit:    cfg.Memory.Promotion.Limit,
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, provider.Close())
	})

	_, err = provider.Upsert(ctx, storage.MemoryItem{
		ID:     "mem_episodic_semantic_reflection_source",
		Kind:   storage.MemoryKindEpisodic,
		Status: storage.MemoryStatusActive,
		Title:  "User Preferred Project Codename",
		Text: strings.Join([]string{
			"The user has a stable profile fact for future status reports.",
			"When writing status reports, the user's preferred project codename is ember-lake.",
		}, " "),
		Tags: []string{"episodic", "curated", "preference"},
		Metadata: map[string]string{
			"candidate_kind":    "user_correction",
			"memory_importance": "high",
			"source_quality":    "high",
			"source_session_id": storage.DefaultSessionID,
			"usefulness":        "high",
		},
		SourceLinks: []storage.MemorySourceLink{{
			SessionID:     storage.DefaultSessionID,
			MessageIDs:    []uint{1},
			Offsets:       []int{0},
			CreatedBy:     "live_e2e_seed",
			CreatedReason: "reflection_source_memory",
		}},
		Confidence: 1,
	})
	require.NoError(t, err)

	_, err = provider.Upsert(ctx, storage.MemoryItem{
		ID:     "mem_episodic_semantic_reflection_confirmation",
		Kind:   storage.MemoryKindEpisodic,
		Status: storage.MemoryStatusActive,
		Title:  "Project Codename Preference Confirmation",
		Text: strings.Join([]string{
			"The user confirmed the same durable status-report preference.",
			"The stable fact is that status reports should use the project codename ember-lake.",
		}, " "),
		Tags: []string{"episodic", "curated", "preference"},
		Metadata: map[string]string{
			"candidate_kind":    "user_correction",
			"memory_importance": "high",
			"source_quality":    "high",
			"source_session_id": storage.DefaultSessionID,
			"usefulness":        "high",
		},
		SourceLinks: []storage.MemorySourceLink{{
			SessionID:     storage.DefaultSessionID,
			MessageIDs:    []uint{2},
			Offsets:       []int{1},
			CreatedBy:     "live_e2e_seed",
			CreatedReason: "reflection_source_memory",
		}},
		Confidence: 1,
	})
	require.NoError(t, err)

	vectorIndex := loadLiveMemoryVectorIndex(t, cfg)
	reflection, err := provider.Reflect(ctx, livememory.ReflectionRequest{
		SessionID:    storage.DefaultSessionID,
		Limit:        cfg.Memory.Reflection.Limit,
		RelatedLimit: cfg.Memory.Reflection.RelatedLimit,
	})
	require.NoError(t, err)
	if reflection.WriteCount == 0 && recordingModelClient.lastResponse != nil {
		t.Logf("reflection model response: %s", recordingModelClient.lastResponse.OutputText)
	}
	require.Greater(t, reflection.WriteCount, 0)

	promoted, err := provider.RunPromotionBackground(ctx, livememory.PromotionBackgroundOptions{
		Limit:  cfg.Memory.Promotion.Limit,
		Reason: "live_e2e_semantic_reflection",
	})
	require.NoError(t, err)
	require.Greater(t, promoted, 0)

	store := loadLiveMemoryStore(t, cfg, summaryClient)
	item := waitForLiveSemanticMemoryContaining(
		t,
		ctx,
		store,
		vectorIndex,
		storage.DefaultSessionID,
		"codename",
		"status",
		"ember",
		"lake",
	)
	require.Equal(t, storage.MemoryKindSemantic, item.Kind)
	require.Equal(t, storage.MemoryStatusActive, item.Status)
	require.True(t, item.Reflected)
	require.False(t, item.PromotionEvaluatedAt.IsZero())
	require.Equal(t, "episodic", item.Metadata["reflection_origin"])
	require.NotEmpty(t, item.Metadata["reflection_source_memory_ids"])
	require.Equal(t, "approved", item.Metadata["promotion_decision_outcome"])
	require.Equal(t, "approved", item.Metadata["promotion_decision_reason"])
}

func setProfileHome(t *testing.T, home string) {
	t.Helper()

	original := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(original)
	})
	profile.SetActive(profile.Profile{Name: "test", HomeDir: home})
}

type recordingLiveModelClient struct {
	client       models.Client
	lastResponse *models.Response
	requests     []models.Request
}

func (c *recordingLiveModelClient) Complete(ctx context.Context, req models.Request) (*models.Response, error) {
	c.requests = append(c.requests, req)

	resp, err := c.client.Complete(ctx, req)
	if resp != nil {
		c.lastResponse = resp
	}

	return resp, err
}

func (c *recordingLiveModelClient) CompleteStream(
	ctx context.Context,
	req models.Request,
	onTextDelta func(models.StreamDelta),
) (*models.Response, error) {
	c.requests = append(c.requests, req)

	resp, err := c.client.CompleteStream(ctx, req, onTextDelta)
	if resp != nil {
		c.lastResponse = resp
	}

	return resp, err
}

func (c *recordingLiveModelClient) Requests() []models.Request {
	return append([]models.Request(nil), c.requests...)
}
