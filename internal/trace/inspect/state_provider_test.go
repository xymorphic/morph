package inspect

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/profile"
	storage "github.com/xymorphic/morph/internal/state/core"
	statemanager "github.com/xymorphic/morph/internal/state/manager"
	statemock "github.com/xymorphic/morph/internal/state/mock"
	morphtrace "github.com/xymorphic/morph/internal/trace"
)

func TestConfigureStateProvider_NoopsWhenMissingInputs(t *testing.T) {
	require.NoError(t, ConfigureStateProvider(nil, nil))

	cfg := &config.Config{}
	require.NoError(t, ConfigureStateProvider(cfg, nil))
}

func TestConfigureStateProvider_ReturnsStoreOpenError(t *testing.T) {
	dir := t.TempDir()
	writeTraceSession(t, dir, storage.DefaultSessionID)
	app := NewApp(dir)
	cfg := &config.Config{Storage: config.StorageConfig{Backend: "bad"}}

	err := ConfigureStateProvider(cfg, app)

	require.Error(t, err)
	require.Contains(t, err.Error(), "storage backend")
}

func TestConfigureStateProvider_ReturnsManagerSetupError(t *testing.T) {
	originalNewStateManager := newStateManager
	t.Cleanup(func() { newStateManager = originalNewStateManager })
	expected := errors.New("manager setup failed")
	newStateManager = func(
		storage.Store,
		time.Duration,
		time.Duration,
	) (*statemanager.Manager, error) {
		return nil, expected
	}

	home := t.TempDir()
	setProfileHome(t, home)
	dir := t.TempDir()
	writeTraceSession(t, dir, storage.DefaultSessionID)
	app := NewApp(dir)
	cfg := &config.Config{Storage: config.StorageConfig{Backend: "memory"}}

	err := ConfigureStateProvider(cfg, app)

	require.ErrorIs(t, err, expected)
}

func TestConfigureStateProvider_AttachesStateProvider(t *testing.T) {
	home := t.TempDir()
	setProfileHome(t, home)
	dir := t.TempDir()
	writeTraceSession(t, dir, storage.DefaultSessionID)
	app := NewApp(dir)
	cfg := &config.Config{Storage: config.StorageConfig{Backend: "memory"}}

	require.NoError(t, ConfigureStateProvider(cfg, app))

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+storage.DefaultSessionID, nil)
	resp := httptest.NewRecorder()
	app.Handler().ServeHTTP(resp, req)
	require.Equal(t, http.StatusOK, resp.Code)

	var payload struct {
		Memories struct {
			Source string `json:"source"`
		} `json:"memories"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &payload))
	require.Equal(t, "state", payload.Memories.Source)
}

func TestConfigureStateProvider_AttachesStateProviderWhenMemoryDisabled(t *testing.T) {
	home := t.TempDir()
	setProfileHome(t, home)
	dir := t.TempDir()
	writeTraceSession(t, dir, storage.DefaultSessionID)
	app := NewApp(dir)
	disabled := false
	cfg := &config.Config{
		Storage: config.StorageConfig{Backend: "memory"},
		Memory:  config.MemoryConfig{Enabled: &disabled},
	}

	require.NoError(t, ConfigureStateProvider(cfg, app))

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+storage.DefaultSessionID, nil)
	resp := httptest.NewRecorder()
	app.Handler().ServeHTTP(resp, req)
	require.Equal(t, http.StatusOK, resp.Code)

	var payload struct {
		Memories struct {
			Source string `json:"source"`
		} `json:"memories"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &payload))
	require.Equal(t, "state", payload.Memories.Source)
}

func TestTraceViewerStateConfig_DisablesSearchDependencies(t *testing.T) {
	rerankerEnabled := true
	cfg := &config.Config{
		Search: config.SearchConfig{
			Vector: config.SearchVectorConfig{Enabled: true},
		},
		Reranker: config.RerankerConfig{
			Enabled: &rerankerEnabled,
			Type:    "llm",
		},
	}

	stateCfg := configToTraceViewerStateConfig(cfg)

	require.False(t, stateCfg.Search.Vector.Enabled)
	require.NotNil(t, stateCfg.Reranker.Enabled)
	require.False(t, *stateCfg.Reranker.Enabled)
	require.True(t, cfg.Search.Vector.Enabled)
	require.True(t, *cfg.Reranker.Enabled)
}

func TestStateSessionMemoryProvider_ListSessionMemoriesUsesStateQueryAndClones(t *testing.T) {
	sessionResult := storage.SessionMemoriesResult{
		Items: []storage.MemoryItem{
			{
				ID:     "source-link",
				Kind:   storage.MemoryKindEpisodic,
				Status: storage.MemoryStatusCandidate,
				SourceLinks: []storage.MemorySourceLink{{
					SessionID: " " + storage.DefaultSessionID + " ",
					Offsets:   []int{1},
				}},
			},
			{
				ID:       "metadata",
				Kind:     storage.MemoryKindEpisodic,
				Status:   storage.MemoryStatusActive,
				Metadata: map[string]string{"source_session_id": storage.DefaultSessionID},
			},
		},
	}
	store := &memorySearchStore{result: sessionResult}
	manager, err := statemanager.NewManager(
		store,
		time.Hour,
		time.Hour,
	)
	require.NoError(t, err)

	items, err := stateProvider{manager: manager}.ListSessionMemories(
		context.Background(),
		storage.DefaultSessionID,
	)

	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, storage.SessionMemoryQuery{
		SessionID: storage.DefaultSessionID,
		Kinds: []storage.MemoryKind{
			storage.MemoryKindEpisodic,
			storage.MemoryKindSemantic,
			storage.MemoryKindProcedural,
			storage.MemoryKindPinned,
		},
		Statuses: []storage.MemoryStatus{
			storage.MemoryStatusCandidate,
			storage.MemoryStatusActive,
			storage.MemoryStatusSuperseded,
		},
		Limit: 200,
	}, store.query)
	require.Equal(t, "source-link", items[0].ID)
	require.Equal(t, "metadata", items[1].ID)
	items[0].SourceLinks[0].Offsets[0] = 99
	require.Equal(t, []int{1}, sessionResult.Items[0].SourceLinks[0].Offsets)
}

func TestStateSessionMemoryProvider_ReturnsListErrors(t *testing.T) {
	expected := errors.New("memory list failed")
	manager, err := statemanager.NewManager(
		&memorySearchErrorStore{searchErr: expected},
		time.Hour,
		time.Hour,
	)
	require.NoError(t, err)

	_, err = stateProvider{manager: manager}.ListSessionMemories(
		context.Background(),
		storage.DefaultSessionID,
	)

	require.ErrorIs(t, err, expected)
}

func writeTraceSession(t *testing.T, dir, id string) {
	t.Helper()

	path := filepath.Join(dir, id+".jsonl")
	file, err := os.Create(path)
	require.NoError(t, err)
	defer func() { _ = file.Close() }()

	require.NoError(t, json.NewEncoder(file).Encode(morphtrace.Event{
		SessionID: id,
		Type:      morphtrace.EvtChatStarted,
		Timestamp: time.Now().UTC(),
	}))
}

type memorySearchErrorStore struct {
	statemock.Store
	searchErr error
}

type memorySearchStore struct {
	statemock.Store
	result storage.SessionMemoriesResult
	query  storage.SessionMemoryQuery
}

func (s *memorySearchStore) Memory() (storage.MemoryStore, bool) {
	return s, true
}

func (s memorySearchStore) SearchMemory(
	context.Context,
	storage.MemorySearchQuery,
) (storage.MemorySearchResult, error) {
	return storage.MemorySearchResult{}, nil
}

func (s *memorySearchStore) ListSessionMemories(
	_ context.Context,
	query storage.SessionMemoryQuery,
) (storage.SessionMemoriesResult, error) {
	s.query = query
	return storage.SessionMemoriesResult{Items: cloneMemoryItems(s.result.Items)}, nil
}

func (s memorySearchStore) UpsertMemory(
	context.Context,
	storage.MemoryItem,
) (storage.MemoryItem, error) {
	return storage.MemoryItem{}, nil
}

func (s memorySearchStore) PatchMemory(
	context.Context,
	storage.MemoryPatch,
) (storage.MemoryItem, error) {
	return storage.MemoryItem{}, nil
}

func (s memorySearchStore) DeleteMemory(
	context.Context,
	storage.MemoryDeleteRequest,
) error {
	return nil
}

func (s memorySearchStore) HardDeleteMemory(
	context.Context,
	storage.MemoryDeleteRequest,
) error {
	return nil
}

func (s *memorySearchErrorStore) Memory() (storage.MemoryStore, bool) {
	return s, true
}

func (s memorySearchErrorStore) SearchMemory(
	context.Context,
	storage.MemorySearchQuery,
) (storage.MemorySearchResult, error) {
	return storage.MemorySearchResult{}, nil
}

func (s memorySearchErrorStore) ListSessionMemories(
	context.Context,
	storage.SessionMemoryQuery,
) (storage.SessionMemoriesResult, error) {
	return storage.SessionMemoriesResult{}, s.searchErr
}

func (s memorySearchErrorStore) UpsertMemory(
	context.Context,
	storage.MemoryItem,
) (storage.MemoryItem, error) {
	return storage.MemoryItem{}, nil
}

func (s memorySearchErrorStore) PatchMemory(
	context.Context,
	storage.MemoryPatch,
) (storage.MemoryItem, error) {
	return storage.MemoryItem{}, nil
}

func (s memorySearchErrorStore) DeleteMemory(
	context.Context,
	storage.MemoryDeleteRequest,
) error {
	return nil
}

func (s memorySearchErrorStore) HardDeleteMemory(
	context.Context,
	storage.MemoryDeleteRequest,
) error {
	return nil
}

func cloneMemoryItems(items []storage.MemoryItem) []storage.MemoryItem {
	cloned := make([]storage.MemoryItem, 0, len(items))
	for _, item := range items {
		cloned = append(cloned, item.Clone())
	}

	return cloned
}

func setProfileHome(t *testing.T, home string) {
	t.Helper()

	original := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(original)
	})
	profile.SetActive(profile.Profile{Name: "test", HomeDir: home})
}
