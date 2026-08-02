package agent

import (
	"context"
	"errors"

	storage "github.com/xymorphic/morph/internal/state/core"
)

func (a *Agent) AutomationStore(context.Context) (storage.AutomationStore, bool, error) {
	if a == nil {
		return nil, false, errors.New("agent is required")
	}
	if !a.initialized || a.stateMgr == nil {
		return nil, false, errors.New("environment has not been initialized")
	}

	store, ok := a.stateMgr.AutomationStore()
	return store, ok, nil
}
