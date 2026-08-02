package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/constants"
	"github.com/xymorphic/morph/internal/mocks/gatewaysessionstub"
	storage "github.com/xymorphic/morph/internal/state/core"
	statemanager "github.com/xymorphic/morph/internal/state/manager"
	"github.com/xymorphic/morph/internal/state/storememory"
	"github.com/xymorphic/morph/pkg/gateway/bindings"
	"github.com/xymorphic/morph/pkg/nanoid"
)

var (
	existingSessionID = nanoid.MustFromSeed(storage.SessionIDPrefix, "existing-binding", "existing-binding-test")
	createdSessionID  = nanoid.MustFromSeed(storage.SessionIDPrefix, "created-binding", "created-binding-test")
)

func TestResolver_ReusesExistingBinding(t *testing.T) {
	service := &gatewaysessionstub.Service{
		Binding: storage.GatewayBinding{
			Key:       "generic::chat-1:",
			SessionID: existingSessionID,
		},
		BindingFound: true,
		Session:      storage.Session{ID: existingSessionID},
		SessionFound: true,
	}
	resolver := NewResolver(service)

	session, err := resolver.Resolve(context.Background(), keyFromString("generic::chat-1:"))

	require.NoError(t, err)
	require.Equal(t, existingSessionID, session.ID)
	require.False(t, service.Created)
}

func TestResolver_LoadsExistingBoundSession(t *testing.T) {
	expected := storage.Session{
		ID: existingSessionID,
		Origin: storage.SessionOrigin{
			Source:         storage.SessionOriginSourceTelegram,
			ConversationID: "-100",
		},
	}
	service := &gatewaysessionstub.Service{
		Binding: storage.GatewayBinding{
			Key:       "telegram::-100:",
			SessionID: existingSessionID,
		},
		BindingFound: true,
		Session:      expected,
		SessionFound: true,
	}

	session, err := NewResolver(service).Resolve(context.Background(), keyFromString("telegram::-100:"))

	require.NoError(t, err)
	require.Equal(t, expected, session)
	require.False(t, service.Created)
}

func TestResolver_ReplacesStaleBindingWhenSessionWasDeleted(t *testing.T) {
	createdAt := time.Date(2026, 6, 7, 20, 58, 0, 0, time.UTC)
	service := &gatewaysessionstub.Service{
		Binding: storage.GatewayBinding{
			Key:       "telegram::-100:42",
			SessionID: existingSessionID,
			CreatedAt: createdAt,
		},
		BindingFound:   true,
		CreatedSession: storage.Session{ID: createdSessionID},
	}

	session, err := NewResolver(service).Resolve(context.Background(), keyFromString("telegram::-100:42"))

	require.NoError(t, err)
	require.Equal(t, createdSessionID, session.ID)
	require.True(t, service.Created)
	require.Equal(t, storage.SessionOrigin{
		Source:         storage.SessionOriginSourceTelegram,
		ConversationID: "-100",
		ThreadID:       "42",
	}, service.CreateOrigin)
	require.Equal(t, storage.GatewayBinding{
		Key:       "telegram::-100:42",
		SessionID: createdSessionID,
		CreatedAt: createdAt,
		UpdatedAt: service.SavedBinding.UpdatedAt,
	}, service.SavedBinding)
	require.False(t, service.SavedBinding.UpdatedAt.IsZero())
}

func TestResolver_CreatesAndPersistsMissingBinding(t *testing.T) {
	service := &gatewaysessionstub.Service{
		CreatedSession: storage.Session{ID: createdSessionID},
	}
	resolver := NewResolver(service)

	session, err := resolver.Resolve(context.Background(), keyFromString("generic::chat-1:"))

	require.NoError(t, err)
	require.Equal(t, createdSessionID, session.ID)
	require.True(t, service.Created)
	require.Equal(t, storage.SessionOrigin{
		Source:         storage.SessionOriginSourceGeneric,
		ConversationID: "chat-1",
	}, service.CreateOrigin)
	require.Equal(t, storage.GatewayBinding{
		Key:       "generic::chat-1:",
		SessionID: createdSessionID,
		CreatedAt: service.SavedBinding.CreatedAt,
		UpdatedAt: service.SavedBinding.UpdatedAt,
	}, service.SavedBinding)
	require.False(t, service.SavedBinding.CreatedAt.IsZero())
	require.False(t, service.SavedBinding.UpdatedAt.IsZero())
}

func TestResolver_CreatesWithBasicSessionService(t *testing.T) {
	service := &gatewaysessionstub.BasicCreateService{
		CreatedSession: storage.Session{ID: createdSessionID},
	}

	session, err := NewResolver(service).Resolve(context.Background(), keyFromString("generic::chat-1:"))

	require.NoError(t, err)
	require.Equal(t, createdSessionID, session.ID)
	require.True(t, service.Created)
	require.Equal(t, storage.GatewayBinding{
		Key:       "generic::chat-1:",
		SessionID: createdSessionID,
		CreatedAt: service.SavedBinding.CreatedAt,
		UpdatedAt: service.SavedBinding.UpdatedAt,
	}, service.SavedBinding)
}

func TestResolver_ReturnsExistingSessionLoadError(t *testing.T) {
	expected := errors.New("session lookup failed")
	service := &gatewaysessionstub.Service{
		Binding: storage.GatewayBinding{
			Key:       "generic::chat-1:",
			SessionID: existingSessionID,
		},
		BindingFound: true,
		SessionErr:   expected,
	}

	_, err := NewResolver(service).Resolve(context.Background(), keyFromString("generic::chat-1:"))

	require.ErrorIs(t, err, expected)
	require.False(t, service.Created)
}

func TestResolver_DifferentSourcesProduceDifferentSessions(t *testing.T) {
	service := gatewaysessionstub.NewMapBackedService()
	resolver := NewResolver(service)
	genericKey, err := bindings.Generic("same")
	require.NoError(t, err)
	telegramKey, err := bindings.Telegram("same", "")
	require.NoError(t, err)

	genericSession, err := resolver.Resolve(context.Background(), genericKey)
	require.NoError(t, err)
	telegramSession, err := resolver.Resolve(context.Background(), telegramKey)
	require.NoError(t, err)

	require.NotEqual(t, genericSession.ID, telegramSession.ID)
}

func TestResolver_SameKeyResolvesSameSession(t *testing.T) {
	service := gatewaysessionstub.NewMapBackedService()
	resolver := NewResolver(service)
	key, err := bindings.Generic("same")
	require.NoError(t, err)

	first, err := resolver.Resolve(context.Background(), key)
	require.NoError(t, err)
	second, err := resolver.Resolve(context.Background(), key)
	require.NoError(t, err)

	require.Equal(t, first.ID, second.ID)
}

func TestResolver_KeepsCurrentSessionUnchanged(t *testing.T) {
	store := storememory.NewStore()
	manager, err := statemanager.NewManager(
		store,
		constants.DefaultSessionIdleExpiry,
		constants.DefaultArchiveRetention,
	)
	require.NoError(t, err)
	require.NoError(t, manager.Start(context.Background()))

	before, err := manager.CurrentSession(context.Background())
	require.NoError(t, err)

	service := &gatewaysessionstub.StateManagerService{Manager: manager}
	key, err := bindings.Generic("chat-1")
	require.NoError(t, err)

	session, err := NewResolver(service).Resolve(context.Background(), key)
	require.NoError(t, err)

	after, err := manager.CurrentSession(context.Background())
	require.NoError(t, err)
	require.Equal(t, storage.DefaultSessionID, before)
	require.Equal(t, before, after)
	require.NotEqual(t, after, session.ID)
}

func TestResolver_ReturnsStoreErrorBeforeCreatingSession(t *testing.T) {
	expected := errors.New("binding store unavailable")
	service := &gatewaysessionstub.Service{GetErr: expected}
	resolver := NewResolver(service)

	_, err := resolver.Resolve(context.Background(), keyFromString("generic::chat-1:"))

	require.ErrorIs(t, err, expected)
	require.False(t, service.Created)
}

func TestResolver_RejectsMissingServiceAndKey(t *testing.T) {
	_, err := (*Resolver)(nil).Resolve(context.Background(), keyFromString("generic::chat-1:"))
	require.EqualError(t, err, "gateway session resolver service is required")

	_, err = NewResolver(nil).Resolve(context.Background(), keyFromString("generic::chat-1:"))
	require.EqualError(t, err, "gateway session resolver service is required")

	_, err = NewResolver(&gatewaysessionstub.Service{}).Resolve(context.Background(), keyFromString(" "))
	require.EqualError(t, err, "gateway binding key is required")
}

func TestResolver_ReturnsCreateAndSaveErrors(t *testing.T) {
	for _, tt := range []struct {
		name    string
		service *gatewaysessionstub.Service
		err     error
	}{
		{
			name:    "create",
			service: &gatewaysessionstub.Service{CreateErr: errors.New("create failed")},
			err:     errors.New("create failed"),
		},
		{
			name: "save",
			service: &gatewaysessionstub.Service{
				CreatedSession: storage.Session{ID: createdSessionID},
				SaveErr:        errors.New("save failed"),
			},
			err: errors.New("save failed"),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewResolver(tt.service).Resolve(context.Background(), keyFromString("generic::chat-1:"))
			require.EqualError(t, err, tt.err.Error())
		})
	}
}

func keyFromString(value string) bindings.Key {
	return bindings.Key(value)
}
