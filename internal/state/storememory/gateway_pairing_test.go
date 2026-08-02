package storememory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/pkg/gateway/pairing"
)

func TestGatewayPairing_SaveListDeleteRequestAndSender(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	request := pairing.PendingRequest{
		Source:      " telegram ",
		SenderID:    " 123 ",
		DisplayName: " Ada ",
		Metadata:    map[string]string{" chat ": " 123 "},
		CreatedAt:   now,
		LastSeenAt:  now,
		ExpiresAt:   now.Add(time.Hour),
	}

	require.NoError(t, store.SaveGatewayPairingRequest(context.Background(), request))
	found, ok, err := store.GetGatewayPairingRequest(context.Background(), "telegram", "123")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "telegram", found.Source)
	require.Equal(t, "123", found.SenderID)
	require.Equal(t, map[string]string{"chat": "123"}, found.Metadata)
	found.Metadata["chat"] = "mutated"

	requests, err := store.ListGatewayPairingRequests(context.Background(), "telegram")
	require.NoError(t, err)
	require.Equal(t, "123", requests[0].Metadata["chat"])
	require.NoError(t, store.DeleteGatewayPairingRequest(context.Background(), "telegram", "123"))
	_, ok, err = store.GetGatewayPairingRequest(context.Background(), "telegram", "123")
	require.NoError(t, err)
	require.False(t, ok)

	sender := pairing.ApprovedSender{
		Source:      "telegram",
		SenderID:    "123",
		DisplayName: "Ada",
		Metadata:    map[string]string{"chat": "123"},
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	require.NoError(t, store.SaveGatewayPairedSender(context.Background(), sender))
	approved, ok, err := store.GetGatewayPairedSender(context.Background(), "telegram", "123")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, sender, approved)
	senders, err := store.ListGatewayPairedSenders(context.Background(), "")
	require.NoError(t, err)
	require.Equal(t, []pairing.ApprovedSender{sender}, senders)
	require.NoError(t, store.DeleteGatewayPairedSender(context.Background(), "telegram", "123"))
	_, ok, err = store.GetGatewayPairedSender(context.Background(), "telegram", "123")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestGatewayPairing_ClearRequestsBySource(t *testing.T) {
	store := NewStore()
	require.NoError(t, store.SaveGatewayPairingRequest(context.Background(), pairing.PendingRequest{
		Source:   "telegram",
		SenderID: "1",
	}))
	require.NoError(t, store.SaveGatewayPairingRequest(context.Background(), pairing.PendingRequest{
		Source:   "slack",
		SenderID: "1",
	}))

	require.NoError(t, store.ClearGatewayPairingRequests(context.Background(), "telegram"))

	requests, err := store.ListGatewayPairingRequests(context.Background(), "")
	require.NoError(t, err)
	require.Len(t, requests, 1)
	require.Equal(t, "slack", requests[0].Source)
}

func TestGatewayPairing_RejectsInvalidInput(t *testing.T) {
	store := NewStore()

	require.EqualError(t,
		(*Store)(nil).SaveGatewayPairingRequest(context.Background(), pairing.PendingRequest{}),
		"store is required")
	require.EqualError(t,
		store.SaveGatewayPairingRequest(context.Background(), pairing.PendingRequest{SenderID: "1"}),
		"gateway pairing source is required")
	require.EqualError(t,
		store.SaveGatewayPairingRequest(context.Background(), pairing.PendingRequest{Source: "telegram"}),
		"gateway pairing sender id is required")
	_, _, err := store.GetGatewayPairingRequest(context.Background(), "", "1")
	require.EqualError(t, err, "gateway pairing source is required")
	_, _, err = store.GetGatewayPairedSender(context.Background(), "telegram", "")
	require.EqualError(t, err, "gateway pairing sender id is required")
}
