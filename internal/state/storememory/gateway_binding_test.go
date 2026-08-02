package storememory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	base "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/pkg/nanoid"
)

func TestGatewayBinding_SaveAndGet(t *testing.T) {
	store := NewStore()
	sessionID := nanoid.MustFromSeed(base.SessionIDPrefix, "memory-binding", "memory-binding-test")
	require.NoError(t, store.Save(context.Background(), base.Session{ID: sessionID}))

	err := store.SaveGatewayBinding(context.Background(), base.GatewayBinding{
		Key:       "generic::chat-1:",
		SessionID: sessionID,
	})
	require.NoError(t, err)

	binding, ok, err := store.GetGatewayBinding(context.Background(), "generic::chat-1:")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "generic::chat-1:", binding.Key)
	require.Equal(t, sessionID, binding.SessionID)
	require.False(t, binding.CreatedAt.IsZero())
	require.False(t, binding.UpdatedAt.IsZero())
}

func TestGatewayBinding_SaveUpdatesBindingAndPreservesCreatedAt(t *testing.T) {
	store := NewStore()
	firstSessionID := nanoid.MustFromSeed(base.SessionIDPrefix, "memory-binding-one", "memory-binding-one-test")
	secondSessionID := nanoid.MustFromSeed(base.SessionIDPrefix, "memory-binding-two", "memory-binding-two-test")
	require.NoError(t, store.Save(context.Background(), base.Session{ID: firstSessionID}))
	require.NoError(t, store.Save(context.Background(), base.Session{ID: secondSessionID}))

	createdAt := time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC)
	firstUpdatedAt := time.Date(2026, 6, 6, 10, 1, 0, 0, time.UTC)
	require.NoError(t, store.SaveGatewayBinding(context.Background(), base.GatewayBinding{
		Key:       "generic::chat-1:",
		SessionID: firstSessionID,
		CreatedAt: createdAt,
		UpdatedAt: firstUpdatedAt,
	}))

	secondUpdatedAt := time.Date(2026, 6, 6, 10, 2, 0, 0, time.UTC)
	require.NoError(t, store.SaveGatewayBinding(context.Background(), base.GatewayBinding{
		Key:       " generic::chat-1: ",
		SessionID: secondSessionID,
		UpdatedAt: secondUpdatedAt,
	}))

	binding, ok, err := store.GetGatewayBinding(context.Background(), "generic::chat-1:")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "generic::chat-1:", binding.Key)
	require.Equal(t, secondSessionID, binding.SessionID)
	require.Equal(t, createdAt, binding.CreatedAt)
	require.Equal(t, secondUpdatedAt, binding.UpdatedAt)
}

func TestGatewayBinding_SaveRejectsInvalidInput(t *testing.T) {
	store := NewStore()

	require.EqualError(t,
		(*Store)(nil).SaveGatewayBinding(context.Background(), base.GatewayBinding{}),
		"store is required",
	)
	require.EqualError(t,
		store.SaveGatewayBinding(context.Background(), base.GatewayBinding{Key: " "}),
		"gateway binding key is required",
	)
	require.EqualError(t,
		store.SaveGatewayBinding(context.Background(), base.GatewayBinding{
			Key:       "generic::chat-1:",
			SessionID: "invalid",
		}),
		"session id must be a valid ses_ nanoid",
	)
}

func TestGatewayBinding_SaveRejectsMissingSession(t *testing.T) {
	store := NewStore()

	err := store.SaveGatewayBinding(context.Background(), base.GatewayBinding{
		Key:       "generic::missing:",
		SessionID: nanoid.MustFromSeed(base.SessionIDPrefix, "missing-binding", "missing-binding-test"),
	})

	require.EqualError(t, err, "session not found")
}

func TestGatewayBinding_GetMissingReturnsFalse(t *testing.T) {
	store := NewStore()

	_, ok, err := store.GetGatewayBinding(context.Background(), "generic::missing:")

	require.NoError(t, err)
	require.False(t, ok)
}

func TestGatewayBinding_GetRejectsInvalidInput(t *testing.T) {
	store := NewStore()

	_, _, err := (*Store)(nil).GetGatewayBinding(context.Background(), "generic::chat-1:")
	require.EqualError(t, err, "store is required")

	_, _, err = store.GetGatewayBinding(context.Background(), " ")
	require.EqualError(t, err, "gateway binding key is required")
}
