package summary

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/agent/context/compaction"
	"github.com/xymorphic/morph/internal/config"
	instruct "github.com/xymorphic/morph/internal/instructions"
	"github.com/xymorphic/morph/internal/mocks"
	models "github.com/xymorphic/morph/internal/model"
	storage "github.com/xymorphic/morph/internal/state/core"
	storagemock "github.com/xymorphic/morph/internal/state/mock"
	"github.com/xymorphic/morph/internal/trace"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/logutils"
)

func init() {
	logutils.SetOutput(io.Discard)
}

func TestService_MaybeRefreshSummary_ReturnsWhenStateOrTraceIsNil(t *testing.T) {
	service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, &storagemock.Store{})
	var mem *State
	require.NoError(t, service.MaybeRefreshSummary(context.Background(), mem, RefreshInput{
		TraceSession: &mocks.TraceSessionStub{},
	}))

	mem = &State{}
	require.NoError(t, service.MaybeRefreshSummary(context.Background(), mem, RefreshInput{}))
}

func TestService_MaybeRefreshSummary_ReturnsErrorWhenServiceDependenciesMissing(t *testing.T) {
	mem := &State{}
	require.EqualError(t, (*Service)(nil).MaybeRefreshSummary(context.Background(), mem, RefreshInput{TraceSession: &mocks.TraceSessionStub{}}), "summary service is required")
	require.EqualError(t, (&Service{store: &storagemock.Store{}}).MaybeRefreshSummary(context.Background(), mem, RefreshInput{TraceSession: &mocks.TraceSessionStub{}}), "model client is required")
	require.EqualError(t, (&Service{modelClient: &mocks.ModelClientStub{}}).MaybeRefreshSummary(context.Background(), mem, RefreshInput{TraceSession: &mocks.TraceSessionStub{}}), "summary store is required")
}

func TestService_Load(t *testing.T) {
	store := &storagemock.Store{
		GetSummaryFunc: func(context.Context, string) (storage.SessionSummary, bool, error) {
			return storage.SessionSummary{
				SessionID:      storage.DefaultSessionID,
				SessionSummary: "Older work",
			}, true, nil
		},
	}
	service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, store)

	mem, err := service.Load(context.Background(), storage.DefaultSessionID)
	require.NoError(t, err)
	require.NotNil(t, mem)
	require.NotNil(t, mem.Current)
	require.Equal(t, "Older work", mem.Current.SessionSummary)
}

func TestService_Load_ReturnsErrors(t *testing.T) {
	_, err := (*Service)(nil).Load(context.Background(), storage.DefaultSessionID)
	require.EqualError(t, err, "summary service is required")

	_, err = (&Service{}).Load(context.Background(), storage.DefaultSessionID)
	require.EqualError(t, err, "summary store is required")

	service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, &storagemock.Store{
		GetSummaryFunc: func(context.Context, string) (storage.SessionSummary, bool, error) {
			return storage.SessionSummary{}, false, errors.New("load failed")
		},
	})
	_, err = service.Load(context.Background(), storage.DefaultSessionID)
	require.EqualError(t, err, "load failed")
}

func TestService_MaybeRefreshSummary_SkipsWhenCompactionIsDisabled(t *testing.T) {
	client := &mocks.ModelClientStub{}
	service := summaryTestService(summaryTestConfig(false), client, summaryTestStore(summaryTestHistory(10)))
	mem := &State{}
	err := service.MaybeRefreshSummary(context.Background(), mem, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    storage.DefaultSessionID,
		TraceSession: &mocks.TraceSessionStub{},
	})
	require.NoError(t, err)
	require.Zero(t, client.CallCount)
}

func TestService_MaybeRefreshSummary_DoesNotTransitionCompactionWhenRefreshIsNotNeeded(t *testing.T) {
	cases := []struct {
		name    string
		cfg     *config.Config
		history []morphmsg.Message
		memory  *State
		request models.Request
	}{
		{
			name:    "history too short",
			cfg:     summaryTestConfig(true),
			history: summaryTestHistory(summaryTestRecentTail()),
			memory:  &State{},
			request: summaryTriggerRequest(),
		},
		{
			name:    "estimate does not trigger",
			cfg:     summaryTestConfig(true),
			history: summaryTestHistory(10),
			memory:  &State{},
			request: models.Request{Instructions: "small"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := summaryTestStore(tc.history)
			service := summaryTestService(tc.cfg, &mocks.ModelClientStub{}, store)

			err := service.MaybeRefreshSummary(context.Background(), tc.memory, RefreshInput{
				Request:      tc.request,
				SessionID:    storage.DefaultSessionID,
				TraceSession: &mocks.TraceSessionStub{},
			})
			require.NoError(t, err)

			session, ok, err := store.Get(context.Background(), storage.DefaultSessionID, storage.SessionGetOptions{})
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, storage.SessionCompaction{}, session.Compaction)
		})
	}
}

func TestService_MaybeRefreshSummary_ReturnsCountMessagesError(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, &storagemock.Store{
		CountMessagesFunc: func(context.Context, string, storage.MessageQueryOptions) (int, error) {
			return 0, errors.New("count failed")
		},
	})

	err := service.MaybeRefreshSummary(context.Background(), &State{}, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    storage.DefaultSessionID,
		TraceSession: traceSession,
	})

	require.EqualError(t, err, "count failed")
	requireSummaryEvent(t, traceSession.Events, trace.EvtContextCompactionFailed)
}

func TestService_MaybeRefreshSummary_RecordsCompactionFailureWhenLoadingSessionFails(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	store := summaryTestStore(summaryTestHistory(10))
	store.GetFunc = func(context.Context, string) (storage.Session, bool, error) {
		return storage.Session{}, false, errors.New("get session failed")
	}
	service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, store)

	err := service.MaybeRefreshSummary(context.Background(), &State{}, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    storage.DefaultSessionID,
		TraceSession: traceSession,
	})

	require.EqualError(t, err, "get session failed")
	requireSummaryEvent(t, traceSession.Events, trace.EvtContextCompactionFailed)
}

func TestService_MaybeRefreshSummary_RecordsCompactionFailureWhenSessionIsMissing(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	store := summaryTestStore(summaryTestHistory(10))
	store.GetFunc = func(context.Context, string) (storage.Session, bool, error) {
		return storage.Session{}, false, nil
	}
	service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, store)

	err := service.MaybeRefreshSummary(context.Background(), &State{}, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    storage.DefaultSessionID,
		TraceSession: traceSession,
	})

	require.EqualError(t, err, "session not found")
	requireSummaryEvent(t, traceSession.Events, trace.EvtContextCompactionFailed)
}

func TestService_MaybeRefreshSummary_SkipsWhenHistoryIsTooShort(t *testing.T) {
	client := &mocks.ModelClientStub{}
	service := summaryTestService(summaryTestConfig(true), client, summaryTestStore(summaryTestHistory(summaryTestRecentTail())))
	mem := &State{}
	err := service.MaybeRefreshSummary(context.Background(), mem, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    storage.DefaultSessionID,
		TraceSession: &mocks.TraceSessionStub{},
	})
	require.NoError(t, err)
	require.Zero(t, client.CallCount)
}

func TestService_MaybeRefreshSummary_UsesConfiguredRecentSessionTail(t *testing.T) {
	client := &mocks.ModelClientStub{Responses: []*models.Response{{
		OutputText: `{
			"session_summary": "Configured tail",
			"current_task": "Keep going",
			"discoveries": [],
			"open_questions": [],
			"next_actions": []
		}`,
	}}}
	recentTail := 2
	cfg := summaryTestConfig(true)
	cfg.Compaction.RecentSessionTail = &recentTail
	service := summaryTestService(cfg, client, summaryTestStore(summaryTestHistory(5)))
	mem := &State{}

	err := service.MaybeRefreshSummary(context.Background(), mem, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    storage.DefaultSessionID,
		TraceSession: &mocks.TraceSessionStub{},
	})
	require.NoError(t, err)
	require.NotNil(t, mem.Current)
	require.Equal(t, 3, mem.Current.SourceEndOffset)
	require.Equal(t, 5, mem.Current.SourceMessageCount)
}

func TestService_MaybeRefreshSummary_SkipsWhenEstimateDoesNotTrigger(t *testing.T) {
	client := &mocks.ModelClientStub{}
	service := summaryTestService(summaryTestConfig(true), client, summaryTestStore(summaryTestHistory(10)))
	mem := &State{}
	err := service.MaybeRefreshSummary(context.Background(), mem, RefreshInput{
		Request:      models.Request{Instructions: "small"},
		SessionID:    storage.DefaultSessionID,
		TraceSession: &mocks.TraceSessionStub{},
	})
	require.NoError(t, err)
	require.Zero(t, client.CallCount)
}

func TestService_MaybeRefreshSummary_UsesAnchoredUsage(t *testing.T) {
	client := &mocks.ModelClientStub{}
	store := summaryTestStore(summaryTestHistory(10))
	cfg := summaryTestConfig(true)
	cfg.Models.Main.ContextLength = 1000
	service := summaryTestService(cfg, client, store)
	request := models.Request{
		Instructions: strings.Repeat("x", 3000),
		Messages: []morphmsg.Message{
			{Role: morphmsg.RoleUser, Content: "measured"},
			{Role: morphmsg.RoleTool, Content: "small tail"},
		},
	}

	err := service.MaybeRefreshSummary(context.Background(), &State{}, RefreshInput{
		Anchor:       compaction.Anchor{PromptTokens: 20, MessageCount: 1},
		Request:      request,
		SessionID:    storage.DefaultSessionID,
		TraceSession: &mocks.TraceSessionStub{},
	})

	require.NoError(t, err)
	require.Greater(t, compaction.EstimateRequestRough(request), 500)
	require.Zero(t, client.CallCount)
	session, ok, err := store.Get(context.Background(), storage.DefaultSessionID, storage.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, storage.SessionCompaction{}, session.Compaction)
}

func TestService_MaybeRefreshSummary_SkipsWhenSummaryAlreadyCoversHistory(t *testing.T) {
	client := &mocks.ModelClientStub{}
	service := summaryTestService(summaryTestConfig(true), client, &storagemock.Store{})
	mem := &State{Current: &SummaryState{
		SessionID:       storage.DefaultSessionID,
		SourceEndOffset: 2,
		SessionSummary:  "covered",
	}}
	err := service.MaybeRefreshSummary(context.Background(), mem, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    storage.DefaultSessionID,
		TraceSession: &mocks.TraceSessionStub{},
	})
	require.NoError(t, err)
	require.Zero(t, client.CallCount)
}

func TestService_MaybeRefreshSummary_SkipsWhenExistingSummaryAlreadyCoversTargetOffset(t *testing.T) {
	client := &mocks.ModelClientStub{}
	service := summaryTestService(summaryTestConfig(true), client, summaryTestStore(summaryTestHistory(10)))
	mem := &State{Current: &SummaryState{
		SessionID:       storage.DefaultSessionID,
		SourceEndOffset: 99,
	}}
	err := service.MaybeRefreshSummary(context.Background(), mem, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    storage.DefaultSessionID,
		TraceSession: &mocks.TraceSessionStub{},
	})
	require.NoError(t, err)
	require.Zero(t, client.CallCount)
}

func TestService_MaybeRefreshSummary_ReconcilesStaleRunningStateWhenSummaryAlreadyCoversTarget(t *testing.T) {
	store := summaryTestStore(summaryTestHistory(10))
	session, ok, err := store.Get(context.Background(), storage.DefaultSessionID, storage.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	session.Compaction = storage.SessionCompaction{
		RequestedAt:        time.Date(2026, 4, 3, 9, 0, 0, 0, time.UTC),
		StartedAt:          time.Date(2026, 4, 3, 9, 1, 0, 0, time.UTC),
		Status:             storage.CompactionStatusRunning,
		TargetMessageCount: 10,
		TargetOffset:       2,
	}
	require.NoError(t, store.Save(context.Background(), session))

	service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, store)
	mem := &State{Current: &SummaryState{
		SessionID:          storage.DefaultSessionID,
		SourceEndOffset:    99,
		SourceMessageCount: 99,
		SessionSummary:     "covered",
	}}
	traceSession := &mocks.TraceSessionStub{}

	err = service.MaybeRefreshSummary(context.Background(), mem, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    storage.DefaultSessionID,
		TraceSession: traceSession,
	})
	require.NoError(t, err)

	session, ok, err = store.Get(context.Background(), storage.DefaultSessionID, storage.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, storage.CompactionStatusSucceeded, session.Compaction.Status)
	require.Equal(t, 2, session.Compaction.TargetOffset)
	require.Equal(t, 10, session.Compaction.TargetMessageCount)
	requireSummaryEvent(t, traceSession.Events, trace.EvtContextCompactionSucceeded)
}

func TestService_MaybeRefreshSummary_ReconcilesCoveredSummaryWithoutTriggeringEstimate(t *testing.T) {
	store := summaryTestStore(summaryTestHistory(10))
	session, ok, err := store.Get(context.Background(), storage.DefaultSessionID, storage.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	session.Compaction = storage.SessionCompaction{
		RequestedAt:        time.Date(2026, 4, 3, 9, 0, 0, 0, time.UTC),
		StartedAt:          time.Date(2026, 4, 3, 9, 1, 0, 0, time.UTC),
		Status:             storage.CompactionStatusRunning,
		TargetMessageCount: 10,
		TargetOffset:       2,
	}
	require.NoError(t, store.Save(context.Background(), session))

	client := &mocks.ModelClientStub{}
	service := summaryTestService(summaryTestConfig(true), client, store)
	mem := &State{Current: &SummaryState{
		SessionID:          storage.DefaultSessionID,
		SourceEndOffset:    99,
		SourceMessageCount: 99,
		SessionSummary:     "covered",
	}}
	traceSession := &mocks.TraceSessionStub{}

	err = service.MaybeRefreshSummary(context.Background(), mem, RefreshInput{
		Request:      models.Request{Instructions: "small"},
		SessionID:    storage.DefaultSessionID,
		TraceSession: traceSession,
	})
	require.NoError(t, err)
	require.Zero(t, client.CallCount)

	session, ok, err = store.Get(context.Background(), storage.DefaultSessionID, storage.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, storage.CompactionStatusSucceeded, session.Compaction.Status)
	require.Equal(t, 2, session.Compaction.TargetOffset)
	require.Equal(t, 10, session.Compaction.TargetMessageCount)
	requireSummaryEvent(t, traceSession.Events, trace.EvtContextCompactionSucceeded)
}

func TestService_MaybeRefreshSummary_RecordsCompactionFailureWhenCoveredSummarySessionLoadFails(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, &storagemock.Store{
		CountMessagesFunc: func(context.Context, string, storage.MessageQueryOptions) (int, error) {
			return 10, nil
		},
		GetFunc: func(context.Context, string) (storage.Session, bool, error) {
			return storage.Session{}, false, errors.New("get covered session failed")
		},
	})

	err := service.MaybeRefreshSummary(context.Background(), &State{Current: &SummaryState{
		SessionID:       storage.DefaultSessionID,
		SourceEndOffset: 99,
		SessionSummary:  "covered",
	}}, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    storage.DefaultSessionID,
		TraceSession: traceSession,
	})

	require.EqualError(t, err, "get covered session failed")
	requireSummaryEvent(t, traceSession.Events, trace.EvtContextCompactionFailed)
}

func TestService_MaybeRefreshSummary_RecordsCompactionFailureWhenCoveredSummarySessionIsMissing(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, &storagemock.Store{
		CountMessagesFunc: func(context.Context, string, storage.MessageQueryOptions) (int, error) {
			return 10, nil
		},
		GetFunc: func(context.Context, string) (storage.Session, bool, error) {
			return storage.Session{}, false, nil
		},
	})

	err := service.MaybeRefreshSummary(context.Background(), &State{Current: &SummaryState{
		SessionID:       storage.DefaultSessionID,
		SourceEndOffset: 99,
		SessionSummary:  "covered",
	}}, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    storage.DefaultSessionID,
		TraceSession: traceSession,
	})

	require.EqualError(t, err, "session not found")
	requireSummaryEvent(t, traceSession.Events, trace.EvtContextCompactionFailed)
}

func TestService_MaybeRefreshSummary_RecordsFailureWhenModelCallFails(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	mem := &State{}
	service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{Err: errors.New("summary failed")}, summaryTestStore(summaryTestHistory(10)))
	err := service.MaybeRefreshSummary(context.Background(), mem, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    storage.DefaultSessionID,
		TraceSession: traceSession,
	})
	require.EqualError(t, err, "summary failed")

	requireSummaryEvent(t, traceSession.Events, trace.EvtSummaryFailed)
}

func TestService_MaybeRefreshSummary_RecordsFailureWhenLoadingSummaryMessagesFails(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	store := summaryTestStore(summaryTestHistory(10))
	store.GetMessagesFunc = func(context.Context, string, storage.MessageQueryOptions) ([]morphmsg.Message, error) {
		return nil, errors.New("get messages failed")
	}
	service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, store)

	err := service.MaybeRefreshSummary(context.Background(), &State{}, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    storage.DefaultSessionID,
		TraceSession: traceSession,
	})

	require.EqualError(t, err, "get messages failed")
	requireSummaryEvent(t, traceSession.Events, trace.EvtSummaryFailed)
}

func TestService_MaybeRefreshSummary_RecordsFailureWhenModelReturnsNil(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	mem := &State{}
	service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{Responses: []*models.Response{nil}}, summaryTestStore(summaryTestHistory(10)))
	err := service.MaybeRefreshSummary(context.Background(), mem, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    storage.DefaultSessionID,
		TraceSession: traceSession,
	})
	require.EqualError(t, err, "model response is required")

	requireSummaryEvent(t, traceSession.Events, trace.EvtSummaryFailed)
}

func TestService_MaybeRefreshSummary_RecordsFailureWhenSummaryRequestsTools(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	mem := &State{}
	service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{Responses: []*models.Response{{RequiresToolCalls: true}}}, summaryTestStore(summaryTestHistory(10)))
	err := service.MaybeRefreshSummary(context.Background(), mem, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    storage.DefaultSessionID,
		TraceSession: traceSession,
	})
	require.EqualError(t, err, "summary requested tool calls")

	requireSummaryEvent(t, traceSession.Events, trace.EvtSummaryFailed)
}

func TestService_MaybeRefreshSummary_FallsBackWhenStructuredOutputRequestFails(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	mem := &State{}
	client := &mocks.ModelClientStub{
		Errors: []error{errors.New("structured outputs unsupported")},
		Responses: []*models.Response{
			nil,
			{
				OutputText: `{
				"session_summary": "Older work",
				"current_task": "Fix tests",
				"discoveries": ["one"],
				"open_questions": ["two"],
				"next_actions": ["three"]
			}`,
			}},
	}
	service := summaryTestService(summaryTestConfig(true), client, summaryTestStore(summaryTestHistory(10)))

	err := service.MaybeRefreshSummary(context.Background(), mem, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    storage.DefaultSessionID,
		TraceSession: traceSession,
	})
	require.NoError(t, err)
	require.Len(t, client.Requests, 2)
	require.NotNil(t, client.Requests[0].StructuredOutput)
	require.Nil(t, client.Requests[1].StructuredOutput)
	require.NotNil(t, mem.Current)
	require.Equal(t, "Older work", mem.Current.SessionSummary)
}

func TestService_MaybeRefreshSummary_UsesSummaryModelWhenConfigured(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	mem := &State{}
	client := &mocks.ModelClientStub{
		Errors: []error{errors.New("structured outputs unsupported")},
		Responses: []*models.Response{
			nil,
			{
				OutputText: `{
				"session_summary": "Older work",
				"current_task": "Fix tests",
				"discoveries": ["one"],
				"open_questions": ["two"],
				"next_actions": ["three"]
			}`,
			},
		},
	}
	cfg := summaryTestConfig(true)
	cfg.Models.Main.Name = "openai/gpt-4o-mini"
	cfg.Models.Summary.Name = "anthropic/claude-3.5-haiku"
	service := NewService(cfg, client, nil, summaryTestStore(summaryTestHistory(10)))

	err := service.MaybeRefreshSummary(context.Background(), mem, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    storage.DefaultSessionID,
		TraceSession: traceSession,
	})
	require.NoError(t, err)
	require.Len(t, client.Requests, 2)
	require.Equal(t, "anthropic/claude-3.5-haiku", client.Requests[0].Model)
	require.Equal(t, "anthropic/claude-3.5-haiku", client.Requests[1].Model)
}

func TestService_generateSummaryResponse_ValidationAndFallbackPaths(t *testing.T) {
	t.Run("nil_service", func(t *testing.T) {
		resp, err := (*Service)(nil).generateSummaryResponse(context.Background(), models.Request{})
		require.Nil(t, resp)
		require.EqualError(t, err, "model client is required")
	})

	t.Run("nil_client", func(t *testing.T) {
		service := summaryTestService(summaryTestConfig(true), nil, summaryTestStore(summaryTestHistory(10)))
		resp, err := service.generateSummaryResponse(context.Background(), models.Request{})
		require.Nil(t, resp)
		require.EqualError(t, err, "model client is required")
	})

	t.Run("returns_original_error_without_structured_output", func(t *testing.T) {
		service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{Err: errors.New("chat failed")}, summaryTestStore(summaryTestHistory(10)))
		resp, err := service.generateSummaryResponse(context.Background(), models.Request{})
		require.Nil(t, resp)
		require.EqualError(t, err, "chat failed")
	})

	t.Run("uses_summary_client_not_main", func(t *testing.T) {
		main := &mocks.ModelClientStub{Responses: []*models.Response{{OutputText: "from-main"}}}
		summary := &mocks.ModelClientStub{Responses: []*models.Response{{OutputText: "from-summary"}}}
		cfg := summaryTestConfig(true)
		service := NewService(cfg, main, summary, summaryTestStore(summaryTestHistory(10)))
		resp, err := service.generateSummaryResponse(context.Background(), models.Request{})
		require.NoError(t, err)
		require.Equal(t, "from-summary", resp.OutputText)
		require.Empty(t, main.Requests)
		require.Len(t, summary.Requests, 1)
	})
}

func TestService_MaybeRefreshSummary_RecordsFailureWhenModelReturnsInvalidSummary(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	mem := &State{}
	service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{Responses: []*models.Response{{OutputText: "{"}}}, summaryTestStore(summaryTestHistory(10)))
	err := service.MaybeRefreshSummary(context.Background(), mem, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    storage.DefaultSessionID,
		TraceSession: traceSession,
	})
	require.NoError(t, err)

	require.NotNil(t, mem.Current)
	require.Equal(t, "{", mem.Current.SessionSummary)
	require.Empty(t, mem.Current.CurrentTask)
	require.Nil(t, mem.Current.Discoveries)
	requireSummaryEvent(t, traceSession.Events, trace.EvtSummaryParseFailed)
	requireSummaryEventAbsent(t, traceSession.Events, trace.EvtSummaryFailed)
}

func TestService_MaybeRefreshSummary_RecordsFailureWhenModelReturnsEmptySummary(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	mem := &State{}
	service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{
		Responses: []*models.Response{{OutputText: "```json\n \n```"}},
	}, summaryTestStore(summaryTestHistory(10)))

	err := service.MaybeRefreshSummary(context.Background(), mem, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    storage.DefaultSessionID,
		TraceSession: traceSession,
	})

	require.EqualError(t, err, "summary response is empty")
	require.Nil(t, mem.Current)
	requireSummaryEvent(t, traceSession.Events, trace.EvtSummaryFailed)
	requireSummaryEventAbsent(t, traceSession.Events, trace.EvtSummaryParseFailed)
}

func TestService_MaybeRefreshSummary_RecordsFailureWhenFallbackSummaryConstructionFails(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	mem := &State{}
	service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{
		Responses: []*models.Response{{OutputText: "not-json"}},
	}, summaryTestStore(summaryTestHistory(10)))

	err := service.MaybeRefreshSummary(context.Background(), mem, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    "", // empty session ID should be rejected
		TraceSession: traceSession,
	})

	require.EqualError(t, err, "session summary is required")
	require.Nil(t, mem.Current)
	requireSummaryEvent(t, traceSession.Events, trace.EvtSummaryParseFailed)
	requireSummaryEvent(t, traceSession.Events, trace.EvtSummaryFailed)
}

func TestService_MaybeRefreshSummary_RecordsFailureWhenSavingSummaryFails(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	mem := &State{
		Current: &SummaryState{
			SessionID:          storage.DefaultSessionID,
			SourceEndOffset:    1,
			SourceMessageCount: 9,
			UpdatedAt:          time.Date(2026, 4, 2, 8, 0, 0, 0, time.UTC),
			SessionSummary:     "Earlier work",
		},
	}
	store := summaryTestStore(summaryTestHistory(10))
	store.SaveSummaryFunc = func(context.Context, storage.SessionSummary) error {
		return errors.New("save summary failed")
	}
	service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{Responses: []*models.Response{{OutputText: `
		{"session_summary":"Older work","current_task":"","discoveries":[],"open_questions":[],"next_actions":[]}`}}}, store)
	err := service.MaybeRefreshSummary(context.Background(), mem, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    storage.DefaultSessionID,
		TraceSession: traceSession,
	})
	require.EqualError(t, err, "save summary failed")

	require.NotNil(t, mem.Current)
	require.Equal(t, "Earlier work", mem.Current.SessionSummary)
	require.Equal(t, 1, mem.Current.SourceEndOffset)
	requireSummaryEvent(t, traceSession.Events, trace.EvtSummaryFailed)
}

func TestService_MaybeRefreshSummary_RecordsParseFailureBeforeFallbackSaveFailure(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	mem := &State{
		Current: &SummaryState{
			SessionID:          storage.DefaultSessionID,
			SourceEndOffset:    1,
			SourceMessageCount: 9,
			UpdatedAt:          time.Date(2026, 4, 2, 8, 0, 0, 0, time.UTC),
			SessionSummary:     "Earlier work",
		},
	}
	store := summaryTestStore(summaryTestHistory(10))
	store.SaveSummaryFunc = func(context.Context, storage.SessionSummary) error {
		return errors.New("save summary failed")
	}
	service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{
		Responses: []*models.Response{{OutputText: "## Summary\nKeep moving"}},
	}, store)

	err := service.MaybeRefreshSummary(context.Background(), mem, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    storage.DefaultSessionID,
		TraceSession: traceSession,
	})

	require.EqualError(t, err, "save summary failed")
	require.NotNil(t, mem.Current)
	require.Equal(t, "Earlier work", mem.Current.SessionSummary)
	requireSummaryEvent(t, traceSession.Events, trace.EvtSummaryParseFailed)
	requireSummaryEvent(t, traceSession.Events, trace.EvtSummaryFailed)
	requireSummaryEventAbsent(t, traceSession.Events, trace.EvtSummarySaved)
}

func TestService_MaybeRefreshSummary_RecordsCompactionFailureWhenPendingTransitionFails(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	store := summaryTestStore(summaryTestHistory(10))
	store.SaveFunc = func(context.Context, storage.Session) error {
		return errors.New("save pending failed")
	}

	service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, store)
	err := service.MaybeRefreshSummary(context.Background(), &State{}, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    storage.DefaultSessionID,
		TraceSession: traceSession,
	})
	require.EqualError(t, err, "save pending failed")
	requireSummaryEvent(t, traceSession.Events, trace.EvtContextCompactionFailed)
}

func TestService_MaybeRefreshSummary_RecordsCompactionFailureWhenLifecycleSaveFails(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	store := summaryTestStore(summaryTestHistory(10))
	saveCalls := 0
	store.SaveFunc = func(_ context.Context, saved storage.Session) error {
		saveCalls++
		if saveCalls == 2 {
			return errors.New("save running failed")
		}
		return nil
	}

	service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, store)
	err := service.MaybeRefreshSummary(context.Background(), &State{}, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    storage.DefaultSessionID,
		TraceSession: traceSession,
	})
	require.EqualError(t, err, "save running failed")
	requireSummaryEvent(t, traceSession.Events, trace.EvtContextCompactionPending)
	requireSummaryEvent(t, traceSession.Events, trace.EvtContextCompactionFailed)
}

func TestService_MaybeRefreshSummary_ReturnsWrappedErrorWhenMarkingCompactionFailedFails(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	store := summaryTestStore(summaryTestHistory(10))
	saveCalls := 0
	store.SaveFunc = func(_ context.Context, saved storage.Session) error {
		saveCalls++
		if saveCalls == 3 {
			return errors.New("save failed state failed")
		}
		return nil
	}

	service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{Err: errors.New("summary failed")}, store)
	err := service.MaybeRefreshSummary(context.Background(), &State{}, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    storage.DefaultSessionID,
		TraceSession: traceSession,
	})
	require.EqualError(t, err, "mark compaction failed: save failed state failed")
	requireSummaryEvent(t, traceSession.Events, trace.EvtContextCompactionFailed)
}

func TestService_MaybeRefreshSummary_RecordsCompactionFailureWhenSucceededTransitionFails(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	store := summaryTestStore(summaryTestHistory(10))
	saveCalls := 0
	store.SaveFunc = func(_ context.Context, saved storage.Session) error {
		saveCalls++
		if saveCalls == 3 {
			return errors.New("save succeeded failed")
		}
		return nil
	}

	service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{
		Responses: []*models.Response{{
			OutputText: `{"session_summary":"Older work","current_task":"","discoveries":[],"open_questions":[],"next_actions":[]}`,
		}},
	}, store)
	err := service.MaybeRefreshSummary(context.Background(), &State{}, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    storage.DefaultSessionID,
		TraceSession: traceSession,
	})
	require.EqualError(t, err, "save succeeded failed")
	requireSummaryEvent(t, traceSession.Events, trace.EvtContextCompactionFailed)
}

func TestService_MaybeRefreshSummary_SavesSummaryAndRecordsTrace(t *testing.T) {
	requestedAt := time.Date(2026, 4, 2, 9, 0, 0, 0, time.UTC)
	traceSession := &mocks.TraceSessionStub{}
	client := &mocks.ModelClientStub{
		Responses: []*models.Response{{
			OutputText: `{"session_summary":"Older work","current_task":"Fix tests",
            "discoveries":["one"],"open_questions":["two"],"next_actions":["three"]}`,
		}},
	}
	mem := &State{
		Current: &SummaryState{
			SessionID:          storage.DefaultSessionID,
			SourceEndOffset:    1,
			SourceMessageCount: 9,
			UpdatedAt:          requestedAt.Add(-time.Hour),
			SessionSummary:     "Earlier work",
			CurrentTask:        "Continue",
			Discoveries:        []string{"prior"},
		},
	}

	var saved storage.SessionSummary
	store := summaryTestStore(summaryTestHistory(10))
	store.SaveSummaryFunc = func(_ context.Context, summary storage.SessionSummary) error {
		saved = summary
		return nil
	}
	service := summaryTestService(summaryTestConfig(true), client, store)
	service.now = func() time.Time { return requestedAt }
	err := service.MaybeRefreshSummary(context.Background(), mem, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    storage.DefaultSessionID,
		TraceSession: traceSession,
	})
	require.NoError(t, err)

	require.Equal(t, 1, client.CallCount)
	require.Len(t, client.Requests, 1)
	require.Contains(t, client.Requests[0].Instructions, instruct.BuildSessionSummary().String())
	require.Contains(t, client.Requests[0].Instructions, "# Session Summary\n\nEarlier work")
	require.Len(t, client.Requests[0].Messages, 1)
	require.Equal(t, "history", client.Requests[0].Messages[0].Content)

	require.Equal(t, storage.DefaultSessionID, saved.SessionID)
	require.Equal(t, 2, saved.SourceEndOffset)
	require.Equal(t, 10, saved.SourceMessageCount)
	require.Equal(t, requestedAt, saved.UpdatedAt)
	require.Equal(t, "Older work", saved.SessionSummary)
	require.Equal(t, "Fix tests", saved.CurrentTask)
	require.Equal(t, []string{"one"}, saved.Discoveries)
	require.Equal(t, []string{"two"}, saved.OpenQuestions)
	require.Equal(t, []string{"three"}, saved.NextActions)

	require.NotNil(t, mem.Current)
	require.Equal(t, saved.SessionSummary, mem.Current.SessionSummary)
	session, ok, err := store.Get(context.Background(), storage.DefaultSessionID, storage.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, storage.CompactionStatusSucceeded, session.Compaction.Status)
	require.Equal(t, 2, session.Compaction.TargetOffset)
	require.Equal(t, 10, session.Compaction.TargetMessageCount)
	require.Empty(t, session.Compaction.LastError)
	requireSummaryEvent(t, traceSession.Events, trace.EvtSummaryRequested)
	requireSummaryEvent(t, traceSession.Events, trace.EvtSummarySaved)
	requireSummaryEvent(t, traceSession.Events, trace.EvtContextCompactionPending)
	requireSummaryEvent(t, traceSession.Events, trace.EvtContextCompactionRunning)
	payload := requireCompactionSummaryEvent(t, traceSession.Events, trace.EvtContextCompactionSucceeded)
	require.True(t, payload.Auto)
}

func TestService_MaybeRefreshSummary_SanitizesToolCallGroups(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	client := &mocks.ModelClientStub{
		Responses: []*models.Response{{
			OutputText: `{"session_summary":"Older work","current_task":"",
				"discoveries":[],"open_questions":[],"next_actions":[]}`,
		}},
	}
	history := summaryTestHistory(10)
	history[0] = morphmsg.Message{
		Role: morphmsg.RoleAssistant,
		ToolCalls: []morphmsg.ToolCall{{
			ID:   "call-1",
			Name: "time",
		}},
	}
	history[1] = morphmsg.Message{Role: morphmsg.RoleUser, Content: "next turn"}
	service := summaryTestService(summaryTestConfig(true), client, summaryTestStore(history))

	err := service.MaybeRefreshSummary(context.Background(), &State{}, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    storage.DefaultSessionID,
		TraceSession: traceSession,
	})
	require.NoError(t, err)
	require.Len(t, client.Requests, 1)
	require.Len(t, client.Requests[0].Messages, 3)
	require.Equal(t, morphmsg.RoleAssistant, client.Requests[0].Messages[0].Role)
	require.Equal(t, morphmsg.RoleTool, client.Requests[0].Messages[1].Role)
	require.Equal(t, "call-1", client.Requests[0].Messages[1].ToolCallID)
	require.Contains(t, client.Requests[0].Messages[1].Content, "Tool result unavailable")
	require.Equal(t, morphmsg.RoleUser, client.Requests[0].Messages[2].Role)
}

func TestService_MaybeRefreshSummary_MarksCompactionFailedAndRetries(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	store := summaryTestStore(summaryTestHistory(10))
	client := &mocks.ModelClientStub{Err: errors.New("summary failed")}
	service := summaryTestService(summaryTestConfig(true), client, store)
	mem := &State{}

	err := service.MaybeRefreshSummary(context.Background(), mem, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    storage.DefaultSessionID,
		TraceSession: traceSession,
	})
	require.EqualError(t, err, "summary failed")

	session, ok, err := store.Get(context.Background(), storage.DefaultSessionID, storage.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, storage.CompactionStatusFailed, session.Compaction.Status)
	require.Equal(t, "summary failed", session.Compaction.LastError)
	require.Equal(t, 2, session.Compaction.TargetOffset)
	require.Equal(t, 10, session.Compaction.TargetMessageCount)

	client.Err = nil
	client.Responses = []*models.Response{{
		OutputText: `{"session_summary":"Older work","current_task":"","discoveries":[],"open_questions":[],"next_actions":[]}`,
	}}

	err = service.MaybeRefreshSummary(context.Background(), mem, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    storage.DefaultSessionID,
		TraceSession: &mocks.TraceSessionStub{},
	})
	require.NoError(t, err)

	session, ok, err = store.Get(context.Background(), storage.DefaultSessionID, storage.SessionGetOptions{})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, storage.CompactionStatusSucceeded, session.Compaction.Status)
	require.Empty(t, session.Compaction.LastError)
	require.False(t, session.Compaction.CompletedAt.IsZero())

	requireSummaryEvent(t, traceSession.Events, trace.EvtContextCompactionPending)
	requireSummaryEvent(t, traceSession.Events, trace.EvtContextCompactionRunning)
	requireSummaryEvent(t, traceSession.Events, trace.EvtContextCompactionFailed)
}

func TestService_MaybeRefreshSummary_FallsBackWhenClockReturnsZero(t *testing.T) {
	mem := &State{}
	store := summaryTestStore(summaryTestHistory(10))
	service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{
		Responses: []*models.Response{{
			OutputText: `{"session_summary":"Older work","current_task":"","discoveries":[],"open_questions":[],"next_actions":[]}`,
		}},
	}, store)
	service.now = func() time.Time { return time.Time{} }

	require.NoError(t, service.MaybeRefreshSummary(context.Background(), mem, RefreshInput{
		Request:      summaryTriggerRequest(),
		SessionID:    storage.DefaultSessionID,
		TraceSession: &mocks.TraceSessionStub{},
	}))

	require.NotNil(t, mem.Current)
	require.False(t, mem.Current.UpdatedAt.IsZero())
}

func TestService_CompactSession_ReturnsValidationErrors(t *testing.T) {
	sess := storage.Session{ID: storage.DefaultSessionID}
	traceSession := &mocks.TraceSessionStub{}

	t.Run("nil_service", func(t *testing.T) {
		_, err := (*Service)(nil).CompactSession(context.Background(), sess, traceSession)
		require.EqualError(t, err, "summary service is required")
	})

	t.Run("nil_model_client", func(t *testing.T) {
		svc := NewService(summaryTestConfig(true), nil, nil, &storagemock.Store{})
		_, err := svc.CompactSession(context.Background(), sess, traceSession)
		require.EqualError(t, err, "model client is required")
	})

	t.Run("nil_summary_store", func(t *testing.T) {
		svc := NewService(summaryTestConfig(true), &mocks.ModelClientStub{}, nil, nil)
		_, err := svc.CompactSession(context.Background(), sess, traceSession)
		require.EqualError(t, err, "summary store is required")
	})

	t.Run("nil_trace_session", func(t *testing.T) {
		svc := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, summaryTestStore(summaryTestHistory(10)))
		_, err := svc.CompactSession(context.Background(), sess, nil)
		require.EqualError(t, err, "trace session is required")
	})
}

func TestService_CompactSession_ReturnsLoadError(t *testing.T) {
	store := summaryTestStore(summaryTestHistory(10))
	store.GetSummaryFunc = func(context.Context, string) (storage.SessionSummary, bool, error) {
		return storage.SessionSummary{}, false, errors.New("load summary failed")
	}
	svc := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, store)
	traceSession := &mocks.TraceSessionStub{}

	_, err := svc.CompactSession(context.Background(), storage.Session{ID: storage.DefaultSessionID}, traceSession)
	require.EqualError(t, err, "load summary failed")
}

func TestService_CompactSession_ReturnsCountMessagesError(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	store := summaryTestStore(summaryTestHistory(10))
	store.CountMessagesFunc = func(context.Context, string, storage.MessageQueryOptions) (int, error) {
		return 0, errors.New("count failed")
	}
	svc := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, store)

	_, err := svc.CompactSession(context.Background(), storage.Session{ID: storage.DefaultSessionID}, traceSession)
	require.EqualError(t, err, "count failed")
	requireSummaryEvent(t, traceSession.Events, trace.EvtContextCompactionFailed)
}

func TestService_CompactSession_ReturnsHistoryTooShort(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	svc := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, summaryTestStore(summaryTestHistory(summaryTestRecentTail())))

	_, err := svc.CompactSession(context.Background(), storage.Session{ID: storage.DefaultSessionID}, traceSession)
	require.EqualError(t, err, "session history is too short to compact")
	requireSummaryEvent(t, traceSession.Events, trace.EvtContextCompactionFailed)
}

func TestService_CompactSession_ReturnsRefreshError(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	store := summaryTestStore(summaryTestHistory(10))
	svc := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{Err: errors.New("chat failed")}, store)

	_, err := svc.CompactSession(context.Background(), storage.Session{ID: storage.DefaultSessionID}, traceSession)
	require.EqualError(t, err, "chat failed")
}

func TestService_CompactSession_ReturnsSummary(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	store := summaryTestStore(summaryTestHistory(10))
	client := &mocks.ModelClientStub{
		Responses: []*models.Response{{
			OutputText: `{"session_summary":"Compacted","current_task":"t","discoveries":["d"],"open_questions":["q"],"next_actions":["n"]}`,
		}},
	}
	svc := summaryTestService(summaryTestConfig(true), client, store)

	out, err := svc.CompactSession(context.Background(), storage.Session{
		ID:               storage.DefaultSessionID,
		LastPromptTokens: 50,
	}, traceSession)

	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, storage.DefaultSessionID, out.SessionID)
	require.Equal(t, "Compacted", out.SessionSummary)
	require.Equal(t, 2, out.SourceEndOffset)
	require.Equal(t, 10, out.SourceMessageCount)
	require.Equal(t, 1, client.CallCount)
	requireSummaryEvent(t, traceSession.Events, trace.EvtSummaryRequested)
	requireSummaryEvent(t, traceSession.Events, trace.EvtSummarySaved)
	payload := requireCompactionSummaryEvent(t, traceSession.Events, trace.EvtContextCompactionSucceeded)
	require.False(t, payload.Auto)
}

func TestService_CompactSession_ReconcilesWhenExistingSummaryCoversTarget(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	store := summaryTestStore(summaryTestHistory(12))
	store.GetSummaryFunc = func(context.Context, string) (storage.SessionSummary, bool, error) {
		return storage.SessionSummary{
			SessionID:          storage.DefaultSessionID,
			SourceEndOffset:    4,
			SourceMessageCount: 12,
			SessionSummary:     "Existing summary",
		}, true, nil
	}
	client := &mocks.ModelClientStub{Err: errors.New("should not call model")}
	svc := summaryTestService(summaryTestConfig(true), client, store)

	out, err := svc.CompactSession(context.Background(), storage.Session{
		ID:               storage.DefaultSessionID,
		LastPromptTokens: 50,
	}, traceSession)

	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, "Existing summary", out.SessionSummary)
	require.Equal(t, 4, out.SourceEndOffset)
	require.Equal(t, 0, client.CallCount)
	requireSummaryEvent(t, traceSession.Events, trace.EvtContextCompactionSucceeded)
}

func TestService_SummarizeSession_UsesConfiguredRetainedTail(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	store := summaryTestStore(summaryTestHistory(10))
	client := &mocks.ModelClientStub{
		Responses: []*models.Response{{
			OutputText: `{"session_summary":"Compacted","current_task":"t","discoveries":["d"],"open_questions":["q"],"next_actions":["n"]}`,
		}},
	}
	svc := summaryTestService(summaryTestConfig(true), client, store)
	retainedTailMessages := 2

	out, err := svc.SummarizeSession(context.Background(), storage.Session{
		ID:               storage.DefaultSessionID,
		LastPromptTokens: 50,
	}, SummarizeSessionOptions{
		Planner:              SessionSummaryPlannerRetainRecentTail,
		RetainedTailMessages: &retainedTailMessages,
	}, traceSession)

	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, 8, out.SourceEndOffset)
	require.Equal(t, 10, out.SourceMessageCount)
	require.Equal(t, 1, client.CallCount)
	requireSummaryEvent(t, traceSession.Events, trace.EvtSummaryRequested)
	requireSummaryEvent(t, traceSession.Events, trace.EvtSummarySaved)
}

func TestService_RecallSessionSummary_UsesZeroTail(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	store := summaryTestStore(summaryTestHistory(10))
	storedSummary := storage.SessionSummary{
		SessionID:          storage.DefaultSessionID,
		SourceEndOffset:    2,
		SourceMessageCount: 10,
		SessionSummary:     "Earlier work",
	}
	store.GetSummaryFunc = func(context.Context, string) (storage.SessionSummary, bool, error) {
		return storedSummary, true, nil
	}
	store.SaveSummaryFunc = func(context.Context, storage.SessionSummary) error {
		return errors.New("should not save authoritative summary")
	}
	client := &mocks.ModelClientStub{
		Responses: []*models.Response{{
			OutputText: `{"session_summary":"Compacted","current_task":"t","discoveries":["d"],"open_questions":["q"],"next_actions":["n"]}`,
		}},
	}
	svc := summaryTestService(summaryTestConfig(true), client, store)

	out, err := svc.RecallSessionSummary(context.Background(), storage.Session{
		ID:               storage.DefaultSessionID,
		LastPromptTokens: 50,
	}, traceSession)

	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, 10, out.SourceEndOffset)
	require.Equal(t, 10, out.SourceMessageCount)
	require.Equal(t, 1, client.CallCount)
	logutils.PrettyPrint(out)

	summary, ok, err := store.GetSummary(context.Background(), storage.DefaultSessionID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, storedSummary.SessionSummary, summary.SessionSummary)
	require.Equal(t, storedSummary.SourceEndOffset, summary.SourceEndOffset)
	requireSummaryEvent(t, traceSession.Events, trace.EvtRecallSummaryRequested)
	requireSummaryEvent(t, traceSession.Events, trace.EvtRecallSummarySaved)
}

func TestService_RecallSessionSummary_ReturnsValidationErrors(t *testing.T) {
	sess := storage.Session{ID: storage.DefaultSessionID}
	traceSession := &mocks.TraceSessionStub{}

	t.Run("nil_service", func(t *testing.T) {
		_, err := (*Service)(nil).RecallSessionSummary(context.Background(), sess, traceSession)
		require.EqualError(t, err, "summary service is required")
	})

	t.Run("nil_model_client", func(t *testing.T) {
		svc := NewService(summaryTestConfig(true), nil, nil, &storagemock.Store{})
		_, err := svc.RecallSessionSummary(context.Background(), sess, traceSession)
		require.EqualError(t, err, "model client is required")
	})

	t.Run("nil_summary_store", func(t *testing.T) {
		svc := NewService(summaryTestConfig(true), &mocks.ModelClientStub{}, nil, nil)
		_, err := svc.RecallSessionSummary(context.Background(), sess, traceSession)
		require.EqualError(t, err, "summary store is required")
	})

	t.Run("nil_trace_session", func(t *testing.T) {
		svc := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, summaryTestStore(summaryTestHistory(10)))
		_, err := svc.RecallSessionSummary(context.Background(), sess, nil)
		require.EqualError(t, err, "trace session is required")
	})
}

func TestService_RecallSessionSummary_ReturnsLoadError(t *testing.T) {
	store := summaryTestStore(summaryTestHistory(10))
	store.GetSummaryFunc = func(context.Context, string) (storage.SessionSummary, bool, error) {
		return storage.SessionSummary{}, false, errors.New("load summary failed")
	}
	svc := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, store)
	traceSession := &mocks.TraceSessionStub{}

	_, err := svc.RecallSessionSummary(context.Background(), storage.Session{ID: storage.DefaultSessionID}, traceSession)
	require.EqualError(t, err, "load summary failed")
}

func TestService_RecallSessionSummary_ReturnsCountMessagesError(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	store := summaryTestStore(summaryTestHistory(10))
	store.CountMessagesFunc = func(context.Context, string, storage.MessageQueryOptions) (int, error) {
		return 0, errors.New("count failed")
	}
	svc := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, store)

	_, err := svc.RecallSessionSummary(context.Background(), storage.Session{ID: storage.DefaultSessionID}, traceSession)
	require.EqualError(t, err, "count failed")
	requireSummaryEvent(t, traceSession.Events, trace.EvtContextCompactionFailed)
}

func TestService_RecallSessionSummary_ReturnsHistoryTooShort(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	svc := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, summaryTestStore(summaryTestHistory(0)))

	_, err := svc.RecallSessionSummary(context.Background(), storage.Session{ID: storage.DefaultSessionID}, traceSession)
	require.EqualError(t, err, "session history is too short to compact")
	requireSummaryEvent(t, traceSession.Events, trace.EvtContextCompactionFailed)
}

func TestService_RecallSessionSummary_ReturnsRefreshError(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	store := summaryTestStore(summaryTestHistory(10))
	svc := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{Err: errors.New("chat failed")}, store)

	_, err := svc.RecallSessionSummary(context.Background(), storage.Session{ID: storage.DefaultSessionID}, traceSession)
	require.EqualError(t, err, "chat failed")
}

func TestService_RecallSessionSummary_WindowsLargeRecallByMessageCap(t *testing.T) {
	setRecallLimitsForTest(t, 2, 10000, 8, 10000)

	traceSession := &mocks.TraceSessionStub{}
	history := []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "m1"},
		{Role: morphmsg.RoleUser, Content: "m2"},
		{Role: morphmsg.RoleUser, Content: "m3"},
		{Role: morphmsg.RoleUser, Content: "m4"},
		{Role: morphmsg.RoleUser, Content: "m5"},
		{Role: morphmsg.RoleUser, Content: "m6"},
	}
	client := &mocks.ModelClientStub{
		Responses: []*models.Response{
			{OutputText: `{"session_summary":"recent","current_task":"t1","discoveries":["d1"],"open_questions":["q1"],"next_actions":["n1"]}`},
			{OutputText: `{"session_summary":"middle","current_task":"t2","discoveries":["d2"],"open_questions":["q2"],"next_actions":["n2"]}`},
			{OutputText: `{"session_summary":"older","current_task":"t3","discoveries":["d3"],"open_questions":["q3"],"next_actions":["n3"]}`},
			{OutputText: `{"session_summary":"final","current_task":"tf","discoveries":["df"],"open_questions":["qf"],"next_actions":["nf"]}`},
		},
	}
	svc := summaryTestService(summaryTestConfig(true), client, summaryTestStore(history))

	out, err := svc.RecallSessionSummary(context.Background(), storage.Session{ID: storage.DefaultSessionID}, traceSession)
	require.NoError(t, err)
	require.Equal(t, "final", out.SessionSummary)
	require.Equal(t, 6, out.SourceEndOffset)
	require.Equal(t, 6, out.SourceMessageCount)
	require.Len(t, client.Requests, 4)
	require.Equal(t, []string{"m5", "m6"}, []string{
		client.Requests[0].Messages[0].Content,
		client.Requests[0].Messages[1].Content,
	})
	require.Equal(t, []string{"m3", "m4"}, []string{
		client.Requests[1].Messages[0].Content,
		client.Requests[1].Messages[1].Content,
	})
	require.Equal(t, []string{"m1", "m2"}, []string{
		client.Requests[2].Messages[0].Content,
		client.Requests[2].Messages[1].Content,
	})
	require.Len(t, client.Requests[3].Messages, 1)
	require.Contains(t, client.Requests[3].Messages[0].Content, "Recall Window Summary 1")
	require.Contains(t, client.Requests[3].Messages[0].Content, "Recall Window Summary 3")
	requireSummaryEvent(t, traceSession.Events, trace.EvtRecallSummaryRequested)
	requireSummaryEvent(t, traceSession.Events, trace.EvtRecallSummarySaved)
}

func TestService_RecallSessionSummary_WindowsLargeRecallByTokenBudget(t *testing.T) {
	setRecallLimitsForTest(t, 4, 260, 8, 10000)

	traceSession := &mocks.TraceSessionStub{}
	history := []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: strings.Repeat("a", 400)},
		{Role: morphmsg.RoleUser, Content: strings.Repeat("b", 400)},
		{Role: morphmsg.RoleUser, Content: strings.Repeat("c", 400)},
	}
	client := &mocks.ModelClientStub{
		Responses: []*models.Response{
			{OutputText: `{"session_summary":"recent","current_task":"t1","discoveries":["d1"],"open_questions":["q1"],"next_actions":["n1"]}`},
			{OutputText: `{"session_summary":"middle","current_task":"t2","discoveries":["d2"],"open_questions":["q2"],"next_actions":["n2"]}`},
			{OutputText: `{"session_summary":"older","current_task":"t3","discoveries":["d3"],"open_questions":["q3"],"next_actions":["n3"]}`},
			{OutputText: `{"session_summary":"final","current_task":"tf","discoveries":["df"],"open_questions":["qf"],"next_actions":["nf"]}`},
		},
	}
	svc := summaryTestService(summaryTestConfig(true), client, summaryTestStore(history))

	out, err := svc.RecallSessionSummary(context.Background(), storage.Session{ID: storage.DefaultSessionID}, traceSession)
	require.NoError(t, err)
	require.Equal(t, "final", out.SessionSummary)
	require.Len(t, client.Requests, 4)
	require.Len(t, client.Requests[0].Messages, 1)
	require.Len(t, client.Requests[1].Messages, 1)
	require.Len(t, client.Requests[2].Messages, 1)
}

func TestService_RecallSessionSummary_ChunksOversizedSingleRecallWindow(t *testing.T) {
	setRecallLimitsForTest(t, 4, 120, 8, 10000)

	traceSession := &mocks.TraceSessionStub{}
	history := []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: strings.Repeat("oversized ", 120)},
	}
	responses := make([]*models.Response, 0, 10)
	for idx := 0; idx < 9; idx++ {
		responses = append(responses, &models.Response{
			OutputText: `{"session_summary":"chunk-summary","current_task":"t","discoveries":["d"],"open_questions":["q"],"next_actions":["n"]}`,
		})
	}
	responses = append(responses, &models.Response{
		OutputText: `{"session_summary":"final","current_task":"tf","discoveries":["df"],"open_questions":["qf"],"next_actions":["nf"]}`,
	})
	client := &mocks.ModelClientStub{
		Responses: responses,
	}
	svc := summaryTestService(summaryTestConfig(true), client, summaryTestStore(history))

	out, err := svc.RecallSessionSummary(context.Background(), storage.Session{ID: storage.DefaultSessionID}, traceSession)
	require.NoError(t, err)
	require.Equal(t, "chunk-summary", out.SessionSummary)
	require.GreaterOrEqual(t, len(client.Requests), 3)
	require.Len(t, client.Requests[0].Messages, 1)
	require.Len(t, client.Requests[1].Messages, 1)
	require.Len(t, client.Requests[2].Messages, 1)
	require.Contains(t, client.Requests[0].Instructions, "Recall Session Summary Chunk")
	require.Contains(t, client.Requests[1].Instructions, "Recall Session Summary Chunk")
	require.Contains(t, client.Requests[2].Instructions, "Recall Session Summary Chunk")
	finalRequest := client.Requests[len(client.Requests)-1]
	require.Contains(t, finalRequest.Messages[0].Content, "Recall Window Summary 1")
	require.Contains(t, finalRequest.Messages[0].Content, "Recall Window Summary 2")
	require.Contains(t, finalRequest.Messages[0].Content, "Recall Window Summary 3")
}

func TestService_RecallSessionSummary_BatchesRecallSynthesis(t *testing.T) {
	setRecallLimitsForTest(t, 1, 10000, 2, 10000)

	traceSession := &mocks.TraceSessionStub{}
	history := []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "m1"},
		{Role: morphmsg.RoleUser, Content: "m2"},
		{Role: morphmsg.RoleUser, Content: "m3"},
	}
	client := &mocks.ModelClientStub{
		Responses: []*models.Response{
			{OutputText: `{"session_summary":"recent","current_task":"t1","discoveries":["d1"],"open_questions":["q1"],"next_actions":["n1"]}`},
			{OutputText: `{"session_summary":"middle","current_task":"t2","discoveries":["d2"],"open_questions":["q2"],"next_actions":["n2"]}`},
			{OutputText: `{"session_summary":"older","current_task":"t3","discoveries":["d3"],"open_questions":["q3"],"next_actions":["n3"]}`},
			{OutputText: `{"session_summary":"merge-one","current_task":"tm1","discoveries":["dm1"],"open_questions":["qm1"],"next_actions":["nm1"]}`},
			{OutputText: `{"session_summary":"merge-two","current_task":"tm2","discoveries":["dm2"],"open_questions":["qm2"],"next_actions":["nm2"]}`},
			{OutputText: `{"session_summary":"final","current_task":"tf","discoveries":["df"],"open_questions":["qf"],"next_actions":["nf"]}`},
		},
	}
	svc := summaryTestService(summaryTestConfig(true), client, summaryTestStore(history))

	out, err := svc.RecallSessionSummary(context.Background(), storage.Session{ID: storage.DefaultSessionID}, traceSession)
	require.NoError(t, err)
	require.Equal(t, "final", out.SessionSummary)
	require.Len(t, client.Requests, 6)
	require.Equal(t, 2, strings.Count(client.Requests[3].Messages[0].Content, "Recall Window Summary"))
	require.Equal(t, 1, strings.Count(client.Requests[4].Messages[0].Content, "Recall Window Summary"))
	require.Equal(t, 2, strings.Count(client.Requests[5].Messages[0].Content, "Recall Window Summary"))
}

func TestService_planRecallSummary_ErrorsWhenHistoryIsEmpty(t *testing.T) {
	svc := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, summaryTestStore(nil))

	_, err := svc.planRecallSummary(context.Background(), storage.DefaultSessionID, &State{}, 0)
	require.EqualError(t, err, "session history is too short to compact")
}

func TestService_planRecallSummary_ReturnsAlreadyCompleteSummaryWithoutWindows(t *testing.T) {
	svc := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, summaryTestStore(summaryTestHistory(3)))

	plan, err := svc.planRecallSummary(context.Background(), storage.DefaultSessionID, &State{
		Current: &SummaryState{
			SessionID:          storage.DefaultSessionID,
			SourceEndOffset:    3,
			SourceMessageCount: 3,
			SessionSummary:     "done",
		},
	}, 3)
	require.NoError(t, err)
	require.Empty(t, plan.Windows)
	require.Equal(t, 3, plan.TargetOffset)
	require.Equal(t, 3, plan.TargetMessageCount)
}

func TestService_planRecallSummary_ErrorsWhenStartOffsetCoversHistoryButSummaryIsNotFullRecall(t *testing.T) {
	svc := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, summaryTestStore(summaryTestHistory(3)))

	_, err := svc.planRecallSummary(context.Background(), storage.DefaultSessionID, &State{
		Current: &SummaryState{
			SessionID:          storage.DefaultSessionID,
			SourceEndOffset:    3,
			SourceMessageCount: 2,
			SessionSummary:     "partial",
		},
	}, 3)
	require.EqualError(t, err, "session history is too short to compact")
}

func TestService_planRecallSummary_PropagatesWindowPlanningError(t *testing.T) {
	store := summaryTestStore(summaryTestHistory(3))
	store.GetMessagesFunc = func(context.Context, string, storage.MessageQueryOptions) ([]morphmsg.Message, error) {
		return nil, errors.New("get messages failed")
	}
	svc := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, store)

	_, err := svc.planRecallSummary(context.Background(), storage.DefaultSessionID, &State{}, 3)
	require.EqualError(t, err, "get messages failed")
}

func TestService_planRecallWindows_Errors(t *testing.T) {
	t.Run("invalid_range", func(t *testing.T) {
		svc := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, summaryTestStore(summaryTestHistory(2)))
		_, err := svc.planRecallWindows(context.Background(), storage.DefaultSessionID, nil, 2, 2)
		require.EqualError(t, err, "session history is too short to compact")
	})

	t.Run("store_error", func(t *testing.T) {
		store := summaryTestStore(summaryTestHistory(2))
		store.GetMessagesFunc = func(context.Context, string, storage.MessageQueryOptions) ([]morphmsg.Message, error) {
			return nil, errors.New("get messages failed")
		}
		svc := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, store)
		_, err := svc.planRecallWindows(context.Background(), storage.DefaultSessionID, nil, 0, 2)
		require.EqualError(t, err, "get messages failed")
	})

	t.Run("empty_messages", func(t *testing.T) {
		store := summaryTestStore(summaryTestHistory(2))
		store.GetMessagesFunc = func(context.Context, string, storage.MessageQueryOptions) ([]morphmsg.Message, error) {
			return nil, nil
		}
		svc := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, store)
		_, err := svc.planRecallWindows(context.Background(), storage.DefaultSessionID, nil, 0, 2)
		require.EqualError(t, err, "recall window messages are required")
	})
}

func TestService_refreshRecallSummary_ZeroWindowPaths(t *testing.T) {
	t.Run("returns_existing_full_recall_summary", func(t *testing.T) {
		svc := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, summaryTestStore(nil))
		traceSession := &mocks.TraceSessionStub{}
		mem := &State{Current: &SummaryState{
			SessionID:          storage.DefaultSessionID,
			SourceEndOffset:    4,
			SourceMessageCount: 4,
			UpdatedAt:          time.Date(2026, 4, 21, 15, 0, 0, 0, time.UTC),
			SessionSummary:     "existing",
		}}

		out, err := svc.refreshRecallSummary(context.Background(), mem, RefreshInput{
			SessionID:    storage.DefaultSessionID,
			TraceSession: traceSession,
		}, recallPlan{
			RequestedAt:        time.Date(2026, 4, 21, 15, 1, 0, 0, time.UTC),
			TargetMessageCount: 4,
			TargetOffset:       4,
		})
		require.NoError(t, err)
		require.NotSame(t, mem.Current, out)
		require.Equal(t, "existing", out.SessionSummary)
		requireSummaryEvent(t, traceSession.Events, trace.EvtRecallSummaryRequested)
		requireSummaryEvent(t, traceSession.Events, trace.EvtRecallSummarySaved)
	})

	t.Run("errors_without_windows_or_full_summary", func(t *testing.T) {
		svc := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, summaryTestStore(nil))
		traceSession := &mocks.TraceSessionStub{}

		_, err := svc.refreshRecallSummary(context.Background(), &State{}, RefreshInput{
			SessionID:    storage.DefaultSessionID,
			TraceSession: traceSession,
		}, recallPlan{
			RequestedAt:        time.Date(2026, 4, 21, 15, 2, 0, 0, time.UTC),
			TargetMessageCount: 4,
			TargetOffset:       4,
		})
		require.EqualError(t, err, "recall windows are required")
		requireSummaryEvent(t, traceSession.Events, trace.EvtRecallSummaryFailed)
	})
}

func TestService_refreshRecallSummary_LeavesStateNilWhenInputStateIsNil(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	client := &mocks.ModelClientStub{
		Responses: []*models.Response{{OutputText: `{"session_summary":"final","current_task":"t","discoveries":["d"],"open_questions":["q"],"next_actions":["n"]}`}},
	}
	svc := summaryTestService(summaryTestConfig(true), client, summaryTestStore([]morphmsg.Message{{Role: morphmsg.RoleUser, Content: "m1"}}))

	out, err := svc.refreshRecallSummary(context.Background(), nil, RefreshInput{
		SessionID:    storage.DefaultSessionID,
		TraceSession: traceSession,
	}, recallPlan{
		RequestedAt:        time.Date(2026, 4, 21, 15, 3, 0, 0, time.UTC),
		TargetMessageCount: 1,
		TargetOffset:       1,
		Windows:            []recallWindow{{StartOffset: 0, EndOffset: 1}},
	})
	require.NoError(t, err)
	require.Equal(t, "final", out.SessionSummary)
}

func TestService_refreshRecallSummary_PropagatesSynthesisError(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	client := &mocks.ModelClientStub{
		Responses: []*models.Response{
			{OutputText: `{"session_summary":"one","current_task":"t1","discoveries":["d1"],"open_questions":["q1"],"next_actions":["n1"]}`},
			{OutputText: `{"session_summary":"two","current_task":"t2","discoveries":["d2"],"open_questions":["q2"],"next_actions":["n2"]}`},
			{OutputText: "```"},
		},
	}
	store := summaryTestStore([]morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "m1"},
		{Role: morphmsg.RoleUser, Content: "m2"},
	})
	svc := summaryTestService(summaryTestConfig(true), client, store)

	_, err := svc.refreshRecallSummary(context.Background(), &State{}, RefreshInput{
		SessionID:    storage.DefaultSessionID,
		TraceSession: traceSession,
	}, recallPlan{
		RequestedAt:        time.Date(2026, 4, 21, 15, 5, 0, 0, time.UTC),
		TargetMessageCount: 2,
		TargetOffset:       2,
		Windows: []recallWindow{
			{StartOffset: 1, EndOffset: 2},
			{StartOffset: 0, EndOffset: 1},
		},
	})
	require.EqualError(t, err, "summary response is empty")
	requireSummaryEvent(t, traceSession.Events, trace.EvtRecallSummaryFailed)
}

func TestService_summarizeRecallWindow_Errors(t *testing.T) {
	t.Run("store_error", func(t *testing.T) {
		store := summaryTestStore(summaryTestHistory(1))
		store.GetMessagesFunc = func(context.Context, string, storage.MessageQueryOptions) ([]morphmsg.Message, error) {
			return nil, errors.New("get messages failed")
		}
		svc := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, store)
		_, err := svc.summarizeRecallWindow(context.Background(), nil, storage.DefaultSessionID, recallPlan{}, recallWindow{StartOffset: 0, EndOffset: 1}, 1, 1)
		require.EqualError(t, err, "get messages failed")
	})

	t.Run("empty_messages", func(t *testing.T) {
		store := summaryTestStore(summaryTestHistory(1))
		store.GetMessagesFunc = func(context.Context, string, storage.MessageQueryOptions) ([]morphmsg.Message, error) {
			return nil, nil
		}
		svc := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, store)
		_, err := svc.summarizeRecallWindow(context.Background(), nil, storage.DefaultSessionID, recallPlan{}, recallWindow{StartOffset: 0, EndOffset: 1}, 1, 1)
		require.EqualError(t, err, "recall window messages are required")
	})
}

func TestService_summarizeOversizedRecallWindow_ErrorsWhenChunksAreEmpty(t *testing.T) {
	svc := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, summaryTestStore(nil))

	_, err := svc.summarizeOversizedRecallWindow(context.Background(), nil, storage.DefaultSessionID, recallPlan{}, recallWindow{}, 1, 1, nil)
	require.EqualError(t, err, "recall window chunks are required")
}

func TestService_summarizeOversizedRecallWindow_PropagatesChunkErrors(t *testing.T) {
	t.Run("generate_summary_response", func(t *testing.T) {
		setRecallLimitsForTest(t, 4, 120, 8, 10000)
		svc := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{Err: errors.New("chunk failed")}, summaryTestStore(nil))
		_, err := svc.summarizeOversizedRecallWindow(context.Background(), nil, storage.DefaultSessionID, recallPlan{
			RequestedAt:        time.Date(2026, 4, 21, 15, 6, 0, 0, time.UTC),
			TargetMessageCount: 1,
		}, recallWindow{EndOffset: 1}, 1, 1, []morphmsg.Message{{
			Role:    morphmsg.RoleUser,
			Content: strings.Repeat("oversized ", 120),
		}})
		require.EqualError(t, err, "chunk failed")
	})

	t.Run("parse_summary_response", func(t *testing.T) {
		setRecallLimitsForTest(t, 4, 120, 8, 10000)
		svc := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{Responses: []*models.Response{
			{OutputText: "```"},
		}}, summaryTestStore(nil))
		_, err := svc.summarizeOversizedRecallWindow(context.Background(), nil, storage.DefaultSessionID, recallPlan{
			RequestedAt:        time.Date(2026, 4, 21, 15, 7, 0, 0, time.UTC),
			TargetMessageCount: 1,
		}, recallWindow{EndOffset: 1}, 1, 1, []morphmsg.Message{{
			Role:    morphmsg.RoleUser,
			Content: strings.Repeat("oversized ", 120),
		}})
		require.EqualError(t, err, "summary response is empty")
	})
}

func TestService_synthesizeSummaryStates_ErrorsWhenSummariesAreEmpty(t *testing.T) {
	svc := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, summaryTestStore(nil))

	_, err := svc.synthesizeSummaryStates(context.Background(), nil, storage.DefaultSessionID, 1, 1, time.Now().UTC(), nil, func(int, int) instruct.Instructions {
		return buildRecallSynthesisInstructions(nil, 1, 1)
	})
	require.EqualError(t, err, "recall chunk summaries are required")
}

func TestService_synthesizeSummaryStates_PropagatesErrors(t *testing.T) {
	summaries := []*SummaryState{
		{SessionID: storage.DefaultSessionID, SessionSummary: "one"},
		{SessionID: storage.DefaultSessionID, SessionSummary: "two"},
	}

	t.Run("generate_summary_response", func(t *testing.T) {
		svc := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{Err: errors.New("merge failed")}, summaryTestStore(nil))
		_, err := svc.synthesizeSummaryStates(context.Background(), nil, storage.DefaultSessionID, 2, 2, time.Now().UTC(), summaries, func(int, int) instruct.Instructions {
			return buildRecallSynthesisInstructions(nil, 1, 1)
		})
		require.EqualError(t, err, "merge failed")
	})

	t.Run("parse_summary_response", func(t *testing.T) {
		svc := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{Responses: []*models.Response{
			{OutputText: "```"},
		}}, summaryTestStore(nil))
		_, err := svc.synthesizeSummaryStates(context.Background(), nil, storage.DefaultSessionID, 2, 2, time.Now().UTC(), summaries, func(int, int) instruct.Instructions {
			return buildRecallSynthesisInstructions(nil, 1, 1)
		})
		require.EqualError(t, err, "summary response is empty")
	})
}

func TestPlanRecallSummaryBatches_RespectsMergeTokenBudget(t *testing.T) {
	setRecallLimitsForTest(t, 4, 10000, 8, 120)

	summaries := []*SummaryState{
		{SessionID: storage.DefaultSessionID, SessionSummary: strings.Repeat("a", 200)},
		{SessionID: storage.DefaultSessionID, SessionSummary: strings.Repeat("b", 200)},
	}

	batches := getPlannedRecallSummaryBatches(nil, summaries)
	require.Len(t, batches, 2)
	require.Len(t, batches[0], 1)
	require.Len(t, batches[1], 1)
}

func TestRecallInstructionBuilders_IncludeExistingSummaryWhenPresent(t *testing.T) {
	mem := &State{Current: &SummaryState{
		SessionID:      storage.DefaultSessionID,
		SessionSummary: "Existing summary",
		CurrentTask:    "Current task",
	}}

	require.Contains(t, buildRecallChunkInstructions(mem, 1, 2).String(), "Existing summary")
	require.Contains(t, buildRecallSynthesisInstructions(mem, 1, 2).String(), "Existing summary")
	require.Contains(t, buildRecallChunkTextInstructions(mem, 1, 2, 1, 3).String(), "Existing summary")
}

func TestRecallInstructionBuilders_WorkWithoutExistingSummary(t *testing.T) {
	require.Equal(t, instruct.BuildRecallSessionSummaryWindow(1, 2).String(), buildRecallChunkInstructions(nil, 1, 2).String())
	require.Equal(t, instruct.BuildRecallSessionSummarySynthesis(1, 2).String(), buildRecallSynthesisInstructions(nil, 1, 2).String())
	require.Equal(t, instruct.BuildRecallSessionSummaryChunk(1, 2, 1, 3).String(), buildRecallChunkTextInstructions(nil, 1, 2, 1, 3).String())
}

func TestRenderRecallWindowPrompt(t *testing.T) {
	require.Empty(t, renderRecallWindowPrompt(nil))

	prompt := renderRecallWindowPrompt([]morphmsg.Message{{
		Role:       morphmsg.RoleAssistant,
		Name:       "assistant-name",
		ToolCallID: "tool-call-id",
		Content:    "assistant content",
		ToolCalls: []morphmsg.ToolCall{{
			Name:  "search_files",
			Input: `{"pattern":"needle"}`,
		}},
	}})
	require.Contains(t, prompt, "Name: assistant-name")
	require.Contains(t, prompt, "Tool Call ID: tool-call-id")
	require.Contains(t, prompt, "assistant content")
	require.Contains(t, prompt, "search_files")
	require.Contains(t, prompt, `{"pattern":"needle"}`)
}

func TestSplitRecallWindowChunks(t *testing.T) {
	require.Nil(t, splitRecallWindowChunks("   ", 10))
	require.Equal(t, []string{"abc"}, splitRecallWindowChunks("abc", 0))
	require.Equal(t, []string{"ab", "cd", "ef"}, splitRecallWindowChunks("abcdef", 2))
	require.Nil(t, splitRecallWindowChunks("   ", 1))
	require.Equal(t, []string{"a", "b"}, splitRecallWindowChunks("a   b", 1))
}

func TestParseSummaryResponse(t *testing.T) {
	now := time.Date(2026, 4, 21, 15, 4, 0, 0, time.UTC)

	t.Run("nil_response", func(t *testing.T) {
		_, err := parseSummaryResponse(storage.DefaultSessionID, 1, 1, nil, now)
		require.EqualError(t, err, "model response is required")
	})

	t.Run("tool_calls", func(t *testing.T) {
		_, err := parseSummaryResponse(storage.DefaultSessionID, 1, 1, &models.Response{RequiresToolCalls: true}, now)
		require.EqualError(t, err, "summary requested tool calls")
	})

	t.Run("empty_output", func(t *testing.T) {
		_, err := parseSummaryResponse(storage.DefaultSessionID, 1, 1, &models.Response{OutputText: "   "}, now)
		require.EqualError(t, err, "summary response is empty")
	})

	t.Run("fallback_success", func(t *testing.T) {
		out, err := parseSummaryResponse(storage.DefaultSessionID, 1, 1, &models.Response{OutputText: "plain text summary"}, now)
		require.NoError(t, err)
		require.Equal(t, "plain text summary", out.SessionSummary)
	})

	t.Run("fallback_error", func(t *testing.T) {
		_, err := parseSummaryResponse("", 1, 1, &models.Response{OutputText: "not json"}, now)
		require.EqualError(t, err, "session summary is required")
	})
}

func TestCloneSummaryHelpers(t *testing.T) {
	require.Nil(t, cloneSummaryState(nil))
	require.Nil(t, cloneSummaryStates(nil))

	summary := &SummaryState{
		SessionID:      storage.DefaultSessionID,
		SessionSummary: "summary",
		Discoveries:    []string{"d1"},
	}
	cloned := cloneSummaryState(summary)
	require.NotSame(t, summary, cloned)
	require.Equal(t, summary.SessionSummary, cloned.SessionSummary)

	clonedList := cloneSummaryStates([]*SummaryState{summary})
	require.Len(t, clonedList, 1)
	require.NotSame(t, summary, clonedList[0])
}

func TestService_SummarizeSession_ValidatesPlannerOptions(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	store := summaryTestStore(summaryTestHistory(10))
	svc := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, store)
	session := storage.Session{ID: storage.DefaultSessionID}

	t.Run("unknown planner", func(t *testing.T) {
		_, err := svc.SummarizeSession(context.Background(), session, SummarizeSessionOptions{
			Planner: "unknown",
		}, traceSession)
		require.EqualError(t, err, "unknown session summary planner: unknown")
	})

	t.Run("negative retained tail", func(t *testing.T) {
		retainedTailMessages := -1
		_, err := svc.SummarizeSession(context.Background(), session, SummarizeSessionOptions{
			RetainedTailMessages: &retainedTailMessages,
		}, traceSession)
		require.EqualError(t, err, "retained tail messages must be greater than or equal to zero")
	})
}

func TestService_TransitionCompactionPending(t *testing.T) {
	plan := refreshPlan{
		RequestedAt:        time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC),
		TargetMessageCount: 10,
		TargetOffset:       2,
	}

	t.Run("nil session", func(t *testing.T) {
		service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, &storagemock.Store{})
		err := service.transitionCompactionPending(context.Background(), nil, plan, &mocks.TraceSessionStub{})
		require.EqualError(t, err, "session is required")
	})

	t.Run("save error", func(t *testing.T) {
		service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, &storagemock.Store{
			SaveFunc: func(context.Context, storage.Session) error {
				return errors.New("save failed")
			},
		})
		err := service.transitionCompactionPending(context.Background(), &storage.Session{ID: storage.DefaultSessionID}, plan, &mocks.TraceSessionStub{})
		require.EqualError(t, err, "save failed")
	})
}

func TestService_TransitionCompactionRunning(t *testing.T) {
	plan := refreshPlan{
		RequestedAt:        time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC),
		TargetMessageCount: 10,
		TargetOffset:       2,
	}

	t.Run("nil session", func(t *testing.T) {
		service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, &storagemock.Store{})
		err := service.transitionCompactionRunning(context.Background(), nil, plan, &mocks.TraceSessionStub{})
		require.EqualError(t, err, "session is required")
	})
}

func TestService_TransitionCompactionSucceeded(t *testing.T) {
	plan := refreshPlan{
		RequestedAt:        time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC),
		TargetMessageCount: 10,
		TargetOffset:       2,
	}

	t.Run("nil session", func(t *testing.T) {
		service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, &storagemock.Store{})
		err := service.transitionCompactionSucceeded(context.Background(), nil, plan, &mocks.TraceSessionStub{})
		require.EqualError(t, err, "session is required")
	})

	t.Run("save error", func(t *testing.T) {
		service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, &storagemock.Store{
			SaveFunc: func(context.Context, storage.Session) error {
				return errors.New("save failed")
			},
		})
		session := &storage.Session{
			ID: storage.DefaultSessionID,
			Compaction: storage.SessionCompaction{
				LastError: "old",
				FailedAt:  time.Date(2026, 4, 3, 9, 0, 0, 0, time.UTC),
			},
		}
		err := service.transitionCompactionSucceeded(context.Background(), session, plan, &mocks.TraceSessionStub{})
		require.EqualError(t, err, "save failed")
	})

	t.Run("clears stale prompt tokens", func(t *testing.T) {
		var saved storage.Session
		service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, &storagemock.Store{
			SaveFunc: func(_ context.Context, session storage.Session) error {
				saved = session
				return nil
			},
		})
		session := &storage.Session{
			ID:               storage.DefaultSessionID,
			LastPromptTokens: 80520,
		}

		require.NoError(t, service.transitionCompactionSucceeded(context.Background(), session, plan, &mocks.TraceSessionStub{}))
		require.Zero(t, saved.LastPromptTokens)
		require.Zero(t, session.LastPromptTokens)
	})
}

func TestService_ReconcileCompactionSucceeded(t *testing.T) {
	plan := refreshPlan{
		RequestedAt:        time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC),
		TargetMessageCount: 10,
		TargetOffset:       2,
	}

	t.Run("nil session", func(t *testing.T) {
		service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, &storagemock.Store{})
		err := service.reconcileCompactionSucceeded(context.Background(), nil, plan, &mocks.TraceSessionStub{})
		require.EqualError(t, err, "session is required")
	})

	t.Run("already reconciled", func(t *testing.T) {
		traceSession := &mocks.TraceSessionStub{}
		service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, &storagemock.Store{})
		session := &storage.Session{
			ID: storage.DefaultSessionID,
			Compaction: storage.SessionCompaction{
				Status:             storage.CompactionStatusSucceeded,
				TargetMessageCount: 10,
				TargetOffset:       2,
			},
		}
		require.NoError(t, service.reconcileCompactionSucceeded(context.Background(), session, plan, traceSession))
		require.Empty(t, traceSession.Events)
	})

	t.Run("save error records failure", func(t *testing.T) {
		traceSession := &mocks.TraceSessionStub{}
		service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, &storagemock.Store{
			SaveFunc: func(context.Context, storage.Session) error {
				return errors.New("save failed")
			},
		})
		session := &storage.Session{
			ID: storage.DefaultSessionID,
			Compaction: storage.SessionCompaction{
				RequestedAt: time.Date(2026, 4, 3, 9, 0, 0, 0, time.UTC),
				StartedAt:   time.Date(2026, 4, 3, 9, 1, 0, 0, time.UTC),
			},
		}
		err := service.reconcileCompactionSucceeded(context.Background(), session, plan, traceSession)
		require.EqualError(t, err, "save failed")
		requireSummaryEvent(t, traceSession.Events, trace.EvtContextCompactionFailed)
	})
}

func TestService_TransitionCompactionFailed(t *testing.T) {
	plan := refreshPlan{
		RequestedAt:        time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC),
		TargetMessageCount: 10,
		TargetOffset:       2,
	}

	t.Run("nil session", func(t *testing.T) {
		service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, &storagemock.Store{})
		err := service.transitionCompactionFailed(context.Background(), nil, plan, errors.New("summary failed"), &mocks.TraceSessionStub{})
		require.EqualError(t, err, "session is required")
	})

	t.Run("save error", func(t *testing.T) {
		service := summaryTestService(summaryTestConfig(true), &mocks.ModelClientStub{}, &storagemock.Store{
			SaveFunc: func(context.Context, storage.Session) error {
				return errors.New("save failed")
			},
		})
		session := &storage.Session{ID: storage.DefaultSessionID}
		err := service.transitionCompactionFailed(context.Background(), session, plan, errors.New(" summary failed "), &mocks.TraceSessionStub{})
		require.EqualError(t, err, "save failed")
	})
}

func TestState_RecordSummaryApplied_ReturnsWhenUnavailable(t *testing.T) {
	(&State{}).RecordSummaryApplied(nil)
	(&State{}).RecordSummaryApplied(&mocks.TraceSessionStub{})
	(*State)(nil).RecordSummaryApplied(&mocks.TraceSessionStub{})
}

func TestState_RecordSummaryApplied_SkipsBlankSummary(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	mem := &State{Current: &SummaryState{SessionID: storage.DefaultSessionID, SessionSummary: "   "}}

	mem.RecordSummaryApplied(traceSession)
	require.Empty(t, traceSession.Events)
}

func TestState_RecordSummaryApplied_RecordsEvent(t *testing.T) {
	traceSession := &mocks.TraceSessionStub{}
	updatedAt := time.Date(2026, 4, 2, 9, 0, 0, 0, time.UTC)
	mem := &State{Current: &SummaryState{
		SessionID:          storage.DefaultSessionID,
		SourceEndOffset:    2,
		SourceMessageCount: 10,
		UpdatedAt:          updatedAt,
		SessionSummary:     "Older work",
	}}

	mem.RecordSummaryApplied(traceSession)

	require.Len(t, traceSession.Events, 1)
	require.Equal(t, trace.EvtSummaryApplied, traceSession.Events[0].Type)
	require.Equal(t, trace.SummaryEventPayload{
		SessionID:          storage.DefaultSessionID,
		SourceEndOffset:    2,
		SourceMessageCount: 10,
		UpdatedAt:          updatedAt,
	}, traceSession.Events[0].Payload)
}

func TestSummaryFromStorage_ReturnsNilWhenRequiredFieldsMissing(t *testing.T) {
	require.Nil(t, SummaryFromStorage(storage.SessionSummary{}))
	require.Nil(t, SummaryFromStorage(storage.SessionSummary{SessionID: "ses_test", SessionSummary: ""}))
}

func TestSummaryFromStorage_ClonesStorageSummary(t *testing.T) {
	stored := storage.SessionSummary{
		SessionID:          "ses_test",
		SourceEndOffset:    2,
		SourceMessageCount: 5,
		UpdatedAt:          time.Now().UTC(),
		SessionSummary:     "Older work",
		CurrentTask:        "Fix tests",
		Discoveries:        []string{"one"},
		OpenQuestions:      []string{"two"},
		NextActions:        []string{"three"},
	}

	loaded := SummaryFromStorage(stored)
	require.NotNil(t, loaded)
	require.Equal(t, stored.SessionID, loaded.SessionID)
	require.Equal(t, stored.SessionSummary, loaded.SessionSummary)

	stored.Discoveries[0] = "changed"
	require.Equal(t, "one", loaded.Discoveries[0])
}

func TestParseSummary_RejectsEmptyRaw(t *testing.T) {
	summary, err := parseSummary(storage.DefaultSessionID, 1, 2, "   ", time.Now().UTC())
	require.Nil(t, summary)
	require.EqualError(t, err, "summary response is empty")
}

func TestParseSummary_RejectsInvalidJSON(t *testing.T) {
	summary, err := parseSummary(storage.DefaultSessionID, 1, 2, "{", time.Now().UTC())
	require.Nil(t, summary)
	require.Error(t, err)
}

func TestFallbackSummary_UsesRawTextAsSessionSummary(t *testing.T) {
	summary, err := buildFallbackSummary(storage.DefaultSessionID, 1, 2, "## Summary\nKeep moving", time.Now().UTC())
	require.NoError(t, err)
	require.NotNil(t, summary)
	require.Equal(t, "## Summary\nKeep moving", summary.SessionSummary)
	require.Empty(t, summary.CurrentTask)
	require.Nil(t, summary.Discoveries)
	require.Nil(t, summary.OpenQuestions)
	require.Nil(t, summary.NextActions)
}

func TestFallbackSummary_StripsMarkdownFenceBeforeUsingRawText(t *testing.T) {
	summary, err := buildFallbackSummary(storage.DefaultSessionID, 1, 2, "```json\n## Summary\nKeep moving\n```", time.Now().UTC())
	require.NoError(t, err)
	require.NotNil(t, summary)
	require.Equal(t, "## Summary\nKeep moving", summary.SessionSummary)
}

func TestFallbackSummary_RejectsEmptyRaw(t *testing.T) {
	summary, err := buildFallbackSummary(storage.DefaultSessionID, 1, 2, "```json\n \n```", time.Now().UTC())
	require.Nil(t, summary)
	require.EqualError(t, err, "summary response is empty")
}

func TestFallbackSummary_RejectsMissingSessionID(t *testing.T) {
	summary, err := buildFallbackSummary("", 1, 2, "plain text", time.Now().UTC())
	require.Nil(t, summary)
	require.EqualError(t, err, "session summary is required")
}

func TestParseSummary_RejectsMissingSessionSummary(t *testing.T) {
	summary, err := parseSummary(
		storage.DefaultSessionID,
		1,
		2,
		`{"session_summary":"","current_task":"","discoveries":[],"open_questions":[],"next_actions":[]}`,
		time.Now().UTC(),
	)
	require.Nil(t, summary)
	require.EqualError(t, err, "session summary is required")
}

func TestParseSummary_StripsMarkdownFenceBeforeParsing(t *testing.T) {
	summary, err := parseSummary(
		storage.DefaultSessionID,
		1,
		2,
		"```json\n{\"session_summary\":\"done\",\"current_task\":\"next\",\"discoveries\":[\"one\"],\"open_questions\":[\"two\"],\"next_actions\":[\"three\"]}\n```",
		time.Now().UTC(),
	)
	require.NoError(t, err)
	require.NotNil(t, summary)
	require.Equal(t, "done", summary.SessionSummary)
	require.Equal(t, "next", summary.CurrentTask)
}

func TestStripMarkdownFence_HandlesFenceVariants(t *testing.T) {
	require.Equal(t, `{"a":1}`, stripMarkdownFence("```json\n{\"a\":1}\n```"))
	require.Equal(t, `{"a":1}`, stripMarkdownFence("```JSON\n{\"a\":1}\n```"))
	require.Equal(t, `{"a":1}`, stripMarkdownFence("```\n{\"a\":1}\n```"))
	require.Equal(t, `plain`, stripMarkdownFence("plain"))
}

func TestRenderSummaryList_TrimsEmptyValues(t *testing.T) {
	require.Equal(t, "", renderSummaryList("Discoveries", nil))
	require.Equal(t, "", renderSummaryList("Discoveries", []string{" ", "\t"}))
	require.Equal(t, "# Discoveries\n\n- one\n- two", renderSummaryList("Discoveries", []string{" one ", "", "two"}))
}

func TestSummaryCompactionEnabled_DefaultsAndUsesConfiguredValue(t *testing.T) {
	require.True(t, isSummaryCompactionEnabled(nil))
	require.True(t, isSummaryCompactionEnabled(&config.Config{}))

	require.False(t, isSummaryCompactionEnabled(&config.Config{Compaction: config.CompactionConfig{Enabled: new(false)}}))
}

func TestSummaryCompactionEvaluator_UsesConfigValues(t *testing.T) {
	require.NotNil(t, getSummaryCompactionEvaluator(nil))
	require.NotNil(t, getSummaryCompactionEvaluator(&config.Config{
		Models:     config.ModelsConfig{Main: config.MainModelConfig{ContextLength: 100}},
		Compaction: config.CompactionConfig{TriggerPercent: 0.5, WarnPercent: 0.8},
	}))
}

func summaryTestConfig(enabled bool) *config.Config {
	return &config.Config{
		Name: "Test Agent",
		Models: config.ModelsConfig{
			Main: config.MainModelConfig{Name: "test-model", ContextLength: 100},
		},
		Compaction: config.CompactionConfig{Enabled: &enabled, TriggerPercent: 0.5, WarnPercent: 0.8},
	}
}

func summaryTestRecentTail() int {
	return summaryTestConfig(true).CompactionRecentSessionTailEffective()
}

func summaryTriggerRequest() models.Request {
	return models.Request{
		Instructions: "summary",
		Messages: []morphmsg.Message{{
			Role:    morphmsg.RoleUser,
			Content: "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz",
		}},
	}
}

func summaryTestHistory(count int) []morphmsg.Message {
	history := make([]morphmsg.Message, 0, count)
	for range count {
		history = append(history, morphmsg.Message{
			Role:      morphmsg.RoleUser,
			Content:   "history",
			CreatedAt: time.Now().UTC(),
		})
	}

	return history
}

func summaryTestService(cfg *config.Config, client models.Client, store SummaryStore) *Service {
	return NewService(cfg, client, nil, store)
}

func setRecallLimitsForTest(t *testing.T, windowMessages, windowTokens, mergeSummaries, mergeTokens int) {
	t.Helper()

	originalWindowMessages := maxRecallWindowMessages
	originalWindowTokens := maxRecallWindowTokens
	originalMergeSummaries := maxRecallMergeSummaries
	originalMergeTokens := maxRecallMergeTokens

	maxRecallWindowMessages = windowMessages
	maxRecallWindowTokens = windowTokens
	maxRecallMergeSummaries = mergeSummaries
	maxRecallMergeTokens = mergeTokens

	t.Cleanup(func() {
		maxRecallWindowMessages = originalWindowMessages
		maxRecallWindowTokens = originalWindowTokens
		maxRecallMergeSummaries = originalMergeSummaries
		maxRecallMergeTokens = originalMergeTokens
	})
}

func summaryTestStore(history []morphmsg.Message) *storagemock.Store {
	session := storage.Session{ID: storage.DefaultSessionID}
	return &storagemock.Store{
		GetFunc: func(context.Context, string) (storage.Session, bool, error) {
			return session, true, nil
		},
		SaveFunc: func(_ context.Context, saved storage.Session) error {
			session = saved
			return nil
		},
		CountMessagesFunc: func(context.Context, string, storage.MessageQueryOptions) (int, error) {
			return len(history), nil
		},
		GetMessagesFunc: func(_ context.Context, _ string, opts storage.MessageQueryOptions) ([]morphmsg.Message, error) {
			offset := max(opts.Offset, 0)
			if offset >= len(history) {
				return nil, nil
			}
			end := len(history)
			if opts.Limit > 0 && offset+opts.Limit < end {
				end = offset + opts.Limit
			}
			return append([]morphmsg.Message(nil), history[offset:end]...), nil
		},
	}
}

func requireSummaryEvent(t *testing.T, events []trace.Event, eventType string) {
	t.Helper()
	for _, event := range events {
		if event.Type == eventType {
			return
		}
	}

	require.Fail(t, "missing trace event", eventType)
}

func requireCompactionSummaryEvent(t *testing.T, events []trace.Event, eventType string) trace.CompactionEventPayload {
	t.Helper()
	for _, event := range events {
		if event.Type != eventType {
			continue
		}

		payload, ok := event.Payload.(trace.CompactionEventPayload)
		require.True(t, ok)
		return payload
	}

	require.Fail(t, "missing trace event", eventType)
	return trace.CompactionEventPayload{}
}

func requireSummaryEventAbsent(t *testing.T, events []trace.Event, eventType string) {
	t.Helper()
	for _, event := range events {
		if event.Type == eventType {
			require.Fail(t, "unexpected trace event", eventType)
		}
	}
}
