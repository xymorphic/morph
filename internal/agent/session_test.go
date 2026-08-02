package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	agentsession "github.com/xymorphic/morph/pkg/agent/session"

	storage "github.com/xymorphic/morph/internal/state/core"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
)

func TestNewSessionStoreImplementsAgentSessionInterfaces(t *testing.T) {
	var store any = NewSessionStore(&sessionManagerStub{})

	_, ok := store.(agentsession.Store)
	require.True(t, ok)

	_, ok = store.(agentsession.TraceRecorder)
	require.True(t, ok)
}

func TestSessionStore_ReturnsErrorWhenManagerMissing(t *testing.T) {
	store := NewSessionStore(nil)

	_, err := store.Resolve(context.Background(), "")
	require.ErrorIs(t, err, errSessionManagerRequired)
	_, err = store.GetMessages(context.Background(), "", agentsession.MessageQuery{})
	require.ErrorIs(t, err, errSessionManagerRequired)
	require.ErrorIs(t, store.AppendMessages(context.Background(), "", nil), errSessionManagerRequired)
	require.ErrorIs(t, store.UpdateLastPromptTokens(context.Background(), "", 1), errSessionManagerRequired)
	_, err = store.AppendTraceEvent(context.Background(), agentsession.TraceEvent{})
	require.ErrorIs(t, err, errSessionManagerRequired)
}

func TestSessionStoreResolveConvertsSessionShape(t *testing.T) {
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	store := NewSessionStore(&sessionManagerStub{
		ResolveFunc: func(_ context.Context, id string) (storage.Session, error) {
			require.Equal(t, "ses_test", id)
			return storage.Session{
				CreatedAt:                  now,
				Compaction:                 storage.SessionCompaction{Status: storage.CompactionStatusSucceeded, TargetOffset: 7},
				ID:                         id,
				EpisodicCheckpointOffset:   3,
				LastPromptTokens:           99,
				ReflectionCheckpointOffset: 4,
				Title:                      "Title",
				TitleSource:                storage.SessionTitleSourceGenerated,
				UpdatedAt:                  now.Add(time.Minute),
			}, nil
		},
	})

	session, err := store.Resolve(context.Background(), "ses_test")
	require.NoError(t, err)
	require.Equal(t, agentsession.Session{
		CreatedAt:                  now,
		Compaction:                 agentsession.Compaction{Status: agentsession.CompactionStatusSucceeded, TargetOffset: 7},
		ID:                         "ses_test",
		EpisodicCheckpointOffset:   3,
		LastPromptTokens:           99,
		ReflectionCheckpointOffset: 4,
		Title:                      "Title",
		TitleSource:                storage.SessionTitleSourceGenerated,
		UpdatedAt:                  now.Add(time.Minute),
	}, session)
}

func TestSessionStoreGetMessagesConvertsQuery(t *testing.T) {
	store := NewSessionStore(&sessionManagerStub{
		GetMessagesFunc: func(_ context.Context, id string, query storage.MessageQueryOptions) ([]morphmsg.Message, error) {
			require.Equal(t, "default", id)
			require.Equal(t, storage.MessageQueryOptions{
				Limit:  5,
				Name:   "tool",
				Order:  storage.MessageOrderDesc,
				Offset: 2,
				Role:   morphmsg.RoleAssistant,
			}, query)

			return []morphmsg.Message{{Role: morphmsg.RoleAssistant, Content: "hello"}}, nil
		},
	})

	messages, err := store.GetMessages(context.Background(), agentsession.DefaultID, agentsession.MessageQuery{
		Limit:  5,
		Name:   "tool",
		Order:  agentsession.MessageOrderDesc,
		Offset: 2,
		Role:   morphmsg.RoleAssistant,
	})
	require.NoError(t, err)
	require.Equal(t, []morphmsg.Message{{Role: morphmsg.RoleAssistant, Content: "hello"}}, messages)
}

func TestSessionStoreAppendMessagesDelegates(t *testing.T) {
	expected := []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}}
	store := NewSessionStore(&sessionManagerStub{
		AppendMessagesFunc: func(_ context.Context, id string, messages []morphmsg.Message) error {
			require.Equal(t, "default", id)
			require.Equal(t, expected, messages)
			return nil
		},
	})

	require.NoError(t, store.AppendMessages(context.Background(), agentsession.DefaultID, expected))
}

func TestSessionStoreUpdateLastPromptTokensDelegates(t *testing.T) {
	store := NewSessionStore(&sessionManagerStub{
		UpdateLastPromptTokensFunc: func(_ context.Context, id string, tokens int) error {
			require.Equal(t, "default", id)
			require.Equal(t, 123, tokens)
			return nil
		},
	})

	require.NoError(t, store.UpdateLastPromptTokens(context.Background(), agentsession.DefaultID, 123))
}

func TestSessionStoreAppendTraceEventConvertsTraceShape(t *testing.T) {
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	store := NewSessionStore(&sessionManagerStub{
		AppendTraceEventFunc: func(_ context.Context, event storage.TraceEvent) (storage.TraceEvent, error) {
			require.Equal(t, storage.TraceEvent{
				SessionID: "default",
				Type:      "model.request",
				Timestamp: now,
				Payload:   map[string]any{"ok": true},
			}, event)

			event.ID = 9
			event.Sequence = 3
			return event, nil
		},
	})

	event, err := store.AppendTraceEvent(context.Background(), agentsession.TraceEvent{
		SessionID: "default",
		Type:      "model.request",
		Timestamp: now,
		Payload:   map[string]any{"ok": true},
	})
	require.NoError(t, err)
	require.Equal(t, agentsession.TraceEvent{
		ID:        9,
		SessionID: "default",
		Sequence:  3,
		Type:      "model.request",
		Timestamp: now,
		Payload:   map[string]any{"ok": true},
	}, event)
}
