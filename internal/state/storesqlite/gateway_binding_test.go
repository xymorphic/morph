package storesqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	base "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/pkg/nanoid"
)

func TestGatewayBinding_SaveAndGet(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	sessionID := nanoid.MustFromSeed(base.SessionIDPrefix, "sqlite-binding", "sqlite-binding-test")
	require.NoError(t, store.Save(context.Background(), base.Session{ID: sessionID}))

	err = store.SaveGatewayBinding(context.Background(), base.GatewayBinding{
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
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	firstSessionID := nanoid.MustFromSeed(base.SessionIDPrefix, "sqlite-binding-one", "sqlite-binding-one-test")
	secondSessionID := nanoid.MustFromSeed(base.SessionIDPrefix, "sqlite-binding-two", "sqlite-binding-two-test")
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
		Key:       "generic::chat-1:",
		SessionID: secondSessionID,
		UpdatedAt: secondUpdatedAt,
	}))

	binding, ok, err := store.GetGatewayBinding(context.Background(), "generic::chat-1:")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, secondSessionID, binding.SessionID)
	require.Equal(t, createdAt, binding.CreatedAt)
	require.False(t, binding.UpdatedAt.IsZero())
	require.NotEqual(t, firstUpdatedAt, binding.UpdatedAt)
}

func TestGatewayBinding_SaveRejectsInvalidInput(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	require.EqualError(t,
		(*Store)(nil).SaveGatewayBinding(context.Background(), base.GatewayBinding{}),
		"store is required",
	)
	require.EqualError(t,
		(&Store{}).SaveGatewayBinding(context.Background(), base.GatewayBinding{}),
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
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	err = store.SaveGatewayBinding(context.Background(), base.GatewayBinding{
		Key:       "generic::missing:",
		SessionID: nanoid.MustFromSeed(base.SessionIDPrefix, "missing-binding", "missing-binding-test"),
	})

	require.EqualError(t, err, "session not found")
}

func TestGatewayBinding_SaveReturnsSessionLookupError(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	expected := errors.New("session lookup failed")
	require.NoError(t, store.db.Callback().Query().Before("gorm:query").Register(
		"test:gateway-binding-session-query-error",
		func(tx *gorm.DB) {
			if callbackTable(tx) == "sessions" {
				_ = tx.AddError(expected)
			}
		},
	))

	err = store.SaveGatewayBinding(context.Background(), base.GatewayBinding{
		Key:       "generic::chat-1:",
		SessionID: nanoid.MustFromSeed(base.SessionIDPrefix, "sqlite-binding", "sqlite-binding-test"),
	})

	require.ErrorIs(t, err, expected)
}

func TestGatewayBinding_SaveReturnsBindingLookupError(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	sessionID := nanoid.MustFromSeed(base.SessionIDPrefix, "sqlite-binding", "sqlite-binding-test")
	require.NoError(t, store.Save(context.Background(), base.Session{ID: sessionID}))

	expected := errors.New("binding lookup failed")
	require.NoError(t, store.db.Callback().Query().Before("gorm:query").Register(
		"test:gateway-binding-query-error",
		func(tx *gorm.DB) {
			if callbackTable(tx) == "gateway_bindings" {
				_ = tx.AddError(expected)
			}
		},
	))

	err = store.SaveGatewayBinding(context.Background(), base.GatewayBinding{
		Key:       "generic::chat-1:",
		SessionID: sessionID,
	})

	require.ErrorIs(t, err, expected)
}

func TestGatewayBinding_GetMissingReturnsFalse(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	_, ok, err := store.GetGatewayBinding(context.Background(), "generic::missing:")

	require.NoError(t, err)
	require.False(t, ok)
}

func TestGatewayBinding_GetRejectsInvalidInput(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	_, _, err = (*Store)(nil).GetGatewayBinding(context.Background(), "generic::chat-1:")
	require.EqualError(t, err, "store is required")

	_, _, err = (&Store{}).GetGatewayBinding(context.Background(), "generic::chat-1:")
	require.EqualError(t, err, "store is required")

	_, _, err = store.GetGatewayBinding(context.Background(), " ")
	require.EqualError(t, err, "gateway binding key is required")
}

func TestGatewayBinding_GetReturnsQueryError(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.db"))
	require.NoError(t, err)

	expected := errors.New("binding lookup failed")
	require.NoError(t, store.db.Callback().Query().Before("gorm:query").Register(
		"test:gateway-binding-get-query-error",
		func(tx *gorm.DB) {
			if callbackTable(tx) == "gateway_bindings" {
				_ = tx.AddError(expected)
			}
		},
	))

	_, _, err = store.GetGatewayBinding(context.Background(), "generic::chat-1:")

	require.ErrorIs(t, err, expected)
}
