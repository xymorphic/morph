package episodic

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	storage "github.com/xymorphic/morph/internal/state/core"
	statemanager "github.com/xymorphic/morph/internal/state/manager"
	statemock "github.com/xymorphic/morph/internal/state/mock"
	storememory "github.com/xymorphic/morph/internal/state/storememory"
	"github.com/xymorphic/morph/internal/trace"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
)

func TestService_RunBackgroundProcessesEligibleSessionsWithBoundedWindows(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	messages := []morphmsg.Message{
		{ID: 1, Role: morphmsg.RoleUser, Content: "Remember this completed task."},
		{ID: 2, Role: morphmsg.RoleAssistant, Content: "Done."},
	}
	store := &statemock.Store{
		GetFunc: func(context.Context, string) (storage.Session, bool, error) {
			return storage.Session{ID: storage.DefaultSessionID, UpdatedAt: now.Add(-time.Hour)}, true, nil
		},
		ListFunc: func(context.Context) ([]storage.Session, error) {
			return []storage.Session{{ID: storage.DefaultSessionID, UpdatedAt: now.Add(-time.Hour)}}, nil
		},
		CountMessagesFunc: func(context.Context, string, storage.MessageQueryOptions) (int, error) {
			return len(messages), nil
		},
		GetMessagesFunc: func(_ context.Context, _ string, opts storage.MessageQueryOptions) ([]morphmsg.Message, error) {
			end := min(opts.Offset+opts.Limit, len(messages))
			return messages[opts.Offset:end], nil
		},
	}
	manager := testManager(t, store)
	provider := &memoryProviderStub{}
	service := newTestService(t, manager, provider)
	service.nowFunc = func() time.Time { return now }
	recorder := &recordingTrace{}

	result, err := service.RunBackground(ctx, BackgroundRequest{
		RunID: "run-test",
		Options: BackgroundOptions{
			Enabled:     true,
			IdleAfter:   time.Minute,
			MinMessages: 2,
			WindowSize:  1,
			MaxWindows:  1,
		},
		Trace: recorder,
	})

	require.NoError(t, err)
	require.Equal(t, "run-test", result.RunID)
	require.Equal(t, 1, result.CheckedCount)
	require.Equal(t, 1, result.Eligible)
	require.Equal(t, 1, result.WriteCount)
	require.Len(t, result.Sessions[0].Extraction.Windows, 1)
	require.Contains(t, traceEventNames(recorder), trace.EvtMemoryEpisodicBackgroundScheduled)
	require.Contains(t, traceEventNames(recorder), trace.EvtMemoryEpisodicBackgroundEligibilityChecked)
	require.Contains(t, traceEventNames(recorder), trace.EvtMemoryEpisodicBackgroundExtractionAttempt)
	require.Contains(t, traceEventNames(recorder), trace.EvtMemoryEpisodicBackgroundWindowCheckpoint)
	require.Contains(t, traceEventNames(recorder), trace.EvtMemoryEpisodicBackgroundCompleted)
}

func TestService_RunBackgroundSkipsIneligibleSessions(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	store := storememory.NewStore()
	manager := testManager(t, store)
	provider := testProvider(t, store)
	service := newTestService(t, manager, provider)
	service.nowFunc = func() time.Time { return now }

	require.NoError(t, manager.Save(ctx, storage.Session{
		ID:        storage.DefaultSessionID,
		UpdatedAt: now,
	}))
	require.NoError(t, manager.AppendMessages(ctx, storage.DefaultSessionID, []morphmsg.Message{
		{ID: 1, Role: morphmsg.RoleUser, Content: "too fresh"},
	}))

	result, err := service.RunBackground(ctx, BackgroundRequest{
		Options: BackgroundOptions{
			Enabled:     true,
			IdleAfter:   time.Minute,
			MinMessages: 2,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, result.CheckedCount)
	require.Zero(t, result.Eligible)
	require.Zero(t, result.WriteCount)
	require.Equal(t, "insufficient_messages", result.Sessions[0].Reason)
}

func TestService_RunBackgroundSkipsExistingSourceRangeBeforeLoadingMessages(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	loads := 0
	store := &statemock.Store{
		GetFunc: func(context.Context, string) (storage.Session, bool, error) {
			return storage.Session{ID: storage.DefaultSessionID, UpdatedAt: now.Add(-time.Hour)}, true, nil
		},
		ListFunc: func(context.Context) ([]storage.Session, error) {
			return []storage.Session{{ID: storage.DefaultSessionID, UpdatedAt: now.Add(-time.Hour)}}, nil
		},
		CountMessagesFunc: func(context.Context, string, storage.MessageQueryOptions) (int, error) {
			return 1, nil
		},
		GetMessagesFunc: func(context.Context, string, storage.MessageQueryOptions) ([]morphmsg.Message, error) {
			loads++
			return nil, errors.New("existing episode window should not load messages")
		},
	}
	manager := testManager(t, store)
	provider := &memoryProviderStub{
		searchResult: storage.MemorySearchResult{
			Hits: []storage.MemorySearchHit{{Item: storage.MemoryItem{ID: "existing-memory"}}},
		},
	}
	service := newTestService(t, manager, provider)
	service.nowFunc = func() time.Time { return now }

	result, err := service.RunBackground(ctx, BackgroundRequest{
		Options: BackgroundOptions{
			Enabled:     true,
			IdleAfter:   time.Minute,
			MinMessages: 1,
			WindowSize:  1,
			MaxWindows:  1,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, result.SkipCount)
	require.Zero(t, loads)
	require.Empty(t, provider.searchQuery.IDs)
	require.Equal(t, []string{getSourceRangeTag(storage.DefaultSessionID, 0, 1)}, provider.searchQuery.Tags)
}

func TestService_RunBackgroundResumesFromSessionCheckpoint(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	messages := []morphmsg.Message{
		{ID: 1, Role: morphmsg.RoleUser, Content: "already processed"},
		{ID: 2, Role: morphmsg.RoleAssistant, Content: "also processed"},
		{ID: 3, Role: morphmsg.RoleUser, Content: "remember resume here"},
		{ID: 4, Role: morphmsg.RoleAssistant, Content: "next run"},
	}
	var offsets []int
	var checkpoints []int
	store := &statemock.Store{
		GetFunc: func(context.Context, string) (storage.Session, bool, error) {
			return storage.Session{
				ID:                       storage.DefaultSessionID,
				EpisodicCheckpointOffset: 2,
				UpdatedAt:                now.Add(-time.Hour),
			}, true, nil
		},
		ListFunc: func(context.Context) ([]storage.Session, error) {
			return []storage.Session{{
				ID:                       storage.DefaultSessionID,
				EpisodicCheckpointOffset: 2,
				UpdatedAt:                now.Add(-time.Hour),
			}}, nil
		},
		CountMessagesFunc: func(context.Context, string, storage.MessageQueryOptions) (int, error) {
			return len(messages), nil
		},
		GetMessagesFunc: func(_ context.Context, _ string, opts storage.MessageQueryOptions) ([]morphmsg.Message, error) {
			offsets = append(offsets, opts.Offset)
			end := min(opts.Offset+opts.Limit, len(messages))
			return messages[opts.Offset:end], nil
		},
		UpdateCheckpointsFunc: func(_ context.Context, _ string, patch storage.CheckpointPatch) error {
			if patch.EpisodicOffset != nil {
				checkpoints = append(checkpoints, *patch.EpisodicOffset)
			}
			return nil
		},
	}
	manager := testManager(t, store)
	service := newTestService(t, manager, &memoryProviderStub{})
	service.nowFunc = func() time.Time { return now }

	result, err := service.RunBackground(ctx, BackgroundRequest{
		Options: BackgroundOptions{
			Enabled:     true,
			IdleAfter:   time.Minute,
			MinMessages: 1,
			WindowSize:  1,
			MaxWindows:  1,
		},
	})

	require.NoError(t, err)
	require.Equal(t, []int{2}, offsets)
	require.Equal(t, []int{3}, checkpoints)
	require.Equal(t, 1, result.WriteCount)
	require.Equal(t, 2, result.Sessions[0].Extraction.Windows[0].OffsetStart)
	require.Equal(t, 3, result.Sessions[0].Extraction.Windows[0].OffsetEnd)
}

func TestService_RunBackgroundSkipsCompletedCheckpoint(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	loads := 0
	store := &statemock.Store{
		GetFunc: func(context.Context, string) (storage.Session, bool, error) {
			return storage.Session{
				ID:                       storage.DefaultSessionID,
				EpisodicCheckpointOffset: 2,
				UpdatedAt:                now.Add(-time.Hour),
			}, true, nil
		},
		ListFunc: func(context.Context) ([]storage.Session, error) {
			return []storage.Session{{
				ID:                       storage.DefaultSessionID,
				EpisodicCheckpointOffset: 2,
				UpdatedAt:                now.Add(-time.Hour),
			}}, nil
		},
		CountMessagesFunc: func(context.Context, string, storage.MessageQueryOptions) (int, error) {
			return 2, nil
		},
		GetMessagesFunc: func(context.Context, string, storage.MessageQueryOptions) ([]morphmsg.Message, error) {
			loads++
			return nil, nil
		},
	}
	service := newTestService(t, testManager(t, store), &memoryProviderStub{})
	service.nowFunc = func() time.Time { return now }

	result, err := service.RunBackground(ctx, BackgroundRequest{
		Options: BackgroundOptions{
			Enabled:     true,
			IdleAfter:   time.Minute,
			MinMessages: 1,
		},
	})

	require.NoError(t, err)
	require.Zero(t, loads)
	require.Zero(t, result.Eligible)
	require.Equal(t, "checkpoint_complete", result.Sessions[0].Reason)
}

func TestService_RunBackgroundRetriesFailedWindows(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	loads := 0
	store := &statemock.Store{
		GetFunc: func(context.Context, string) (storage.Session, bool, error) {
			return storage.Session{ID: storage.DefaultSessionID, UpdatedAt: now.Add(-time.Hour)}, true, nil
		},
		ListFunc: func(context.Context) ([]storage.Session, error) {
			return []storage.Session{{ID: storage.DefaultSessionID, UpdatedAt: now.Add(-time.Hour)}}, nil
		},
		CountMessagesFunc: func(context.Context, string, storage.MessageQueryOptions) (int, error) {
			return 1, nil
		},
		GetMessagesFunc: func(context.Context, string, storage.MessageQueryOptions) ([]morphmsg.Message, error) {
			loads++
			if loads == 1 {
				return nil, errors.New("temporary load failure")
			}
			return []morphmsg.Message{{ID: 1, Role: morphmsg.RoleUser, Content: "remember retry works"}}, nil
		},
	}
	manager := testManager(t, store)
	provider := &memoryProviderStub{}
	service := newTestService(t, manager, provider)
	service.nowFunc = func() time.Time { return now }
	recorder := &recordingTrace{}

	result, err := service.RunBackground(ctx, BackgroundRequest{
		Options: BackgroundOptions{
			Enabled:     true,
			IdleAfter:   time.Minute,
			MinMessages: 1,
			WindowSize:  1,
			MaxWindows:  1,
			MaxRetries:  1,
		},
		Trace: recorder,
	})

	require.NoError(t, err)
	require.Equal(t, 1, result.RetryCount)
	require.Equal(t, 1, result.WriteCount)
	require.Contains(t, traceEventNames(recorder), trace.EvtMemoryEpisodicBackgroundRetry)
}

func TestService_RunBackgroundReturnsFailedSessionOnCountError(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	store := &statemock.Store{
		GetFunc: func(context.Context, string) (storage.Session, bool, error) {
			return storage.Session{ID: storage.DefaultSessionID, UpdatedAt: now.Add(-time.Hour)}, true, nil
		},
		ListFunc: func(context.Context) ([]storage.Session, error) {
			return []storage.Session{{ID: storage.DefaultSessionID, UpdatedAt: now.Add(-time.Hour)}}, nil
		},
		CountMessagesFunc: func(context.Context, string, storage.MessageQueryOptions) (int, error) {
			return 0, errors.New("count failed")
		},
	}
	service := newTestService(t, testManager(t, store), &memoryProviderStub{})
	service.nowFunc = func() time.Time { return now }
	recorder := &recordingTrace{}

	result, err := service.RunBackground(ctx, BackgroundRequest{
		Options: BackgroundOptions{Enabled: true, MinMessages: 1},
		Trace:   recorder,
	})

	require.NoError(t, err)
	require.Equal(t, 1, result.FailureCount)
	require.Equal(t, "count failed", result.Sessions[0].Error)
	require.Contains(t, traceEventNames(recorder), trace.EvtMemoryEpisodicBackgroundFailed)
}

func TestService_RunBackgroundRecordsFailureAfterRetries(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	store := &statemock.Store{
		GetFunc: func(context.Context, string) (storage.Session, bool, error) {
			return storage.Session{ID: storage.DefaultSessionID, UpdatedAt: now.Add(-time.Hour)}, true, nil
		},
		ListFunc: func(context.Context) ([]storage.Session, error) {
			return []storage.Session{{ID: storage.DefaultSessionID, UpdatedAt: now.Add(-time.Hour)}}, nil
		},
		CountMessagesFunc: func(context.Context, string, storage.MessageQueryOptions) (int, error) {
			return 1, nil
		},
		GetMessagesFunc: func(context.Context, string, storage.MessageQueryOptions) ([]morphmsg.Message, error) {
			return nil, errors.New("load failed")
		},
	}
	service := newTestService(t, testManager(t, store), &memoryProviderStub{})
	service.nowFunc = func() time.Time { return now }
	recorder := &recordingTrace{}

	result, err := service.RunBackground(ctx, BackgroundRequest{
		Options: BackgroundOptions{
			Enabled:     true,
			IdleAfter:   time.Minute,
			MinMessages: 1,
			WindowSize:  1,
			MaxWindows:  1,
			MaxRetries:  1,
		},
		Trace: recorder,
	})

	require.NoError(t, err)
	require.Equal(t, 1, result.FailureCount)
	require.Equal(t, 1, result.RetryCount)
	require.Equal(t, "load failed", result.Sessions[0].Error)
	require.Contains(t, traceEventNames(recorder), trace.EvtMemoryEpisodicBackgroundFailed)
}

func TestService_RunBackgroundReturnsDependencyAndListErrors(t *testing.T) {
	ctx := context.Background()
	_, err := (*Service)(nil).RunBackground(ctx, BackgroundRequest{})
	require.EqualError(t, err, "state manager is required")

	_, err = (&Service{manager: &statemanager.Manager{}}).RunBackground(ctx, BackgroundRequest{})
	require.EqualError(t, err, "memory repository is required")

	_, err = (&Service{
		manager: sourceManagerStub{},
		memory:  &memoryProviderStub{},
	}).RunBackground(ctx, BackgroundRequest{})
	require.EqualError(t, err, "session listing is required")

	listErr := errors.New("list failed")
	store := &statemock.Store{
		GetFunc: func(context.Context, string) (storage.Session, bool, error) {
			return storage.Session{ID: storage.DefaultSessionID}, true, nil
		},
		ListFunc: func(context.Context) ([]storage.Session, error) {
			return nil, listErr
		},
	}
	manager := testManager(t, store)
	service := newTestService(t, manager, &memoryProviderStub{})

	_, err = service.RunBackground(ctx, BackgroundRequest{Options: BackgroundOptions{Enabled: true}})
	require.ErrorIs(t, err, listErr)
}

func TestNormalizeBackgroundOptions(t *testing.T) {
	opts := NormalizeBackgroundOptions(BackgroundOptions{
		WindowSize:      MaxWindowSize + 1,
		MaxWindows:      MaxWindows + 1,
		MaxWindowChars:  MaxWindowChars + 1,
		MaxWindowTokens: MaxWindowTokens + 1,
	})

	require.Equal(t, DefaultBackgroundInterval, opts.Interval)
	require.Equal(t, DefaultBackgroundIdleAfter, opts.IdleAfter)
	require.Equal(t, DefaultBackgroundMinMessages, opts.MinMessages)
	require.Equal(t, MaxWindowSize, opts.WindowSize)
	require.Equal(t, MaxWindows, opts.MaxWindows)
	require.Equal(t, MaxWindowChars, opts.MaxWindowChars)
	require.Equal(t, MaxWindowTokens, opts.MaxWindowTokens)
	require.Equal(t, DefaultBackgroundMaxRetries, opts.MaxRetries)

	opts = NormalizeBackgroundOptions(BackgroundOptions{MaxRetries: -1})
	require.Equal(t, DefaultBackgroundMaxRetries, opts.MaxRetries)
}

func TestBackgroundEligible(t *testing.T) {
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	opts := BackgroundOptions{MinMessages: 2, IdleAfter: time.Minute}

	eligible, reason := isSessionEligible(now, storage.Session{}, 2, 0, opts)
	require.False(t, eligible)
	require.Equal(t, "missing_session_id", reason)

	eligible, reason = isSessionEligible(now, storage.Session{ID: "session"}, 1, 0, opts)
	require.False(t, eligible)
	require.Equal(t, "insufficient_messages", reason)

	eligible, reason = isSessionEligible(now, storage.Session{ID: "session"}, 2, 2, opts)
	require.False(t, eligible)
	require.Equal(t, "checkpoint_complete", reason)

	eligible, reason = isSessionEligible(now, storage.Session{ID: "session"}, 2, 0, opts)
	require.True(t, eligible)
	require.Equal(t, "eligible", reason)

	eligible, reason = isSessionEligible(now, storage.Session{
		ID:        "session",
		UpdatedAt: now.Add(-30 * time.Second),
	}, 2, 0, opts)
	require.False(t, eligible)
	require.Equal(t, "session_not_idle", reason)
}

func TestNormalizedCheckpointOffset(t *testing.T) {
	require.Zero(t, normalizeCheckpointOffset(-1, 10))
	require.Equal(t, 10, normalizeCheckpointOffset(12, 10))
	require.Equal(t, 4, normalizeCheckpointOffset(4, 10))
}

type sourceManagerStub struct{}

func (sourceManagerStub) CurrentSession(context.Context) (string, error) {
	return storage.DefaultSessionID, nil
}

func (sourceManagerStub) CountMessages(context.Context, string, storage.MessageQueryOptions) (int, error) {
	return 0, nil
}

func (sourceManagerStub) GetMessages(context.Context, string, storage.MessageQueryOptions) ([]morphmsg.Message, error) {
	return nil, nil
}

func (sourceManagerStub) ListTraceEvents(context.Context, storage.TraceQuery) (storage.TraceResult, error) {
	return storage.TraceResult{}, nil
}

func (sourceManagerStub) UpdateCheckpoints(context.Context, string, storage.CheckpointPatch) error {
	return nil
}
