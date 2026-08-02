package agent

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	storage "github.com/xymorphic/morph/internal/state/core"
	statemanager "github.com/xymorphic/morph/internal/state/manager"
)

func TestConfig_StateHooksUseProductionDefaults(t *testing.T) {
	require.Equal(
		t,
		reflect.ValueOf(statemanager.OpenStoreWithRerankerClient).Pointer(),
		reflect.ValueOf(OpenStateStore).Pointer(),
	)

	store := &stateStoreStub{session: storage.Session{ID: storage.DefaultSessionID, Title: "Default"}}
	manager, err := NewStateManager(store, time.Hour, time.Hour)
	require.NoError(t, err)

	session, err := manager.Resolve(context.Background(), storage.DefaultSessionID)
	require.NoError(t, err)
	require.Equal(t, storage.DefaultSessionID, session.ID)
	require.Equal(t, "Default", session.Title)
}
