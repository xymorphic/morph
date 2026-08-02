package inspect

import (
	"context"

	"github.com/xymorphic/morph/internal/config"
	storage "github.com/xymorphic/morph/internal/state/core"
	statemanager "github.com/xymorphic/morph/internal/state/manager"
)

var (
	openStateStore  = statemanager.OpenStore
	newStateManager = statemanager.NewManager
)

// ConfigureStateProvider wires state-backed trace inspection into app.
func ConfigureStateProvider(cfg *config.Config, app *App) error {
	if cfg == nil || app == nil {
		return nil
	}

	stateCfg := configToTraceViewerStateConfig(cfg)
	store, err := openStateStore(&stateCfg)
	if err != nil {
		return err
	}

	manager, err := newStateManager(store, stateCfg.Session.DefaultIdleExpiry, stateCfg.Session.ArchiveRetention)
	if err != nil {
		return err
	}

	app.SetMemoryProvider(stateProvider{manager: manager})
	return nil
}

func configToTraceViewerStateConfig(cfg *config.Config) config.Config {
	stateCfg := *cfg
	stateCfg.Search.Vector.Enabled = false
	disableReranker := false
	stateCfg.Reranker.Enabled = &disableReranker
	return stateCfg
}

type stateProvider struct {
	manager *statemanager.Manager
}

func (p stateProvider) ListSessionMemories(ctx context.Context, sessionID string) ([]storage.MemoryItem, error) {
	result, err := p.manager.ListSessionMemories(ctx, storage.SessionMemoryQuery{
		SessionID: sessionID,
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
	})
	if err != nil {
		return nil, err
	}

	memories := make([]storage.MemoryItem, 0, len(result.Items))
	for _, item := range result.Items {
		memories = append(memories, item.Clone())
	}

	return memories, nil
}
