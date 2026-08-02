package sessionsearch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	storage "github.com/xymorphic/morph/internal/state/core"
	statemanager "github.com/xymorphic/morph/internal/state/manager"
	storagemock "github.com/xymorphic/morph/internal/state/mock"
	memorystore "github.com/xymorphic/morph/internal/state/storememory"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/nanoid"
)

var sessionSearchTestSessionID = nanoid.MustFromSeed(storage.SessionIDPrefix, "session-search", "EnvironmentSearchTestSeed")

func TestSearch_FindsAssistantToolCallsAndPlainText(t *testing.T) {
	store := memorystore.NewStore()
	manager, err := statemanager.NewManager(store, time.Minute, time.Hour)
	require.NoError(t, err)
	require.NoError(t, manager.Save(context.Background(), memorystore.Session{ID: sessionSearchTestSessionID}))

	now := time.Now().UTC()
	require.NoError(t, manager.AppendMessages(context.Background(), sessionSearchTestSessionID, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "hello plain text", CreatedAt: now},
		{Role: morphmsg.RoleAssistant, ToolCalls: []morphmsg.ToolCall{{ID: "call-1", Name: "search_files", Input: `{"pattern":"needle"}`}}, CreatedAt: now.Add(time.Second)},
	}))

	results, err := Search(context.Background(), manager, SessionSearchRequest{
		SessionID: sessionSearchTestSessionID,
		Query:     "needle",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, sessionSearchTestSessionID, results[0].SessionID)
	require.Equal(t, 1, results[0].MatchCount)
	require.Len(t, results[0].Messages, 1)
	require.Equal(t, "assistant", results[0].Messages[0].Role)
	require.Equal(t, "search_files", results[0].Messages[0].ToolName)

	results, err = Search(context.Background(), manager, SessionSearchRequest{
		SessionID: sessionSearchTestSessionID,
		Query:     "plain",
		Role:      "user",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Len(t, results[0].Messages, 1)
	require.Equal(t, "user", results[0].Messages[0].Role)
}

func TestSearch_AssistantToolNameFilterDoesNotMatchOtherToolCallPayloads(t *testing.T) {
	store := memorystore.NewStore()
	manager, err := statemanager.NewManager(store, time.Minute, time.Hour)
	require.NoError(t, err)
	require.NoError(t, manager.Save(context.Background(), memorystore.Session{ID: sessionSearchTestSessionID}))

	require.NoError(t, manager.AppendMessages(context.Background(), sessionSearchTestSessionID, []morphmsg.Message{
		{
			Role: morphmsg.RoleAssistant,
			ToolCalls: []morphmsg.ToolCall{
				{ID: "call-1", Name: "process", Input: `{"action":"start"}`},
				{ID: "call-2", Name: "search_files", Input: `{"pattern":"needle"}`},
			},
			CreatedAt: time.Now().UTC(),
		},
	}))

	results, err := Search(context.Background(), manager, SessionSearchRequest{
		SessionID: sessionSearchTestSessionID,
		Query:     "needle",
		ToolName:  "process",
	})
	require.NoError(t, err)
	require.Empty(t, results)
}

func TestSearch_FiltersAndClampsResults(t *testing.T) {
	store := memorystore.NewStore()
	manager, err := statemanager.NewManager(store, time.Minute, time.Hour)
	require.NoError(t, err)

	now := time.Now().UTC()
	for i := 0; i < 25; i++ {
		sessionID := nanoid.MustFromSeed(storage.SessionIDPrefix, fmt.Sprintf("session-search-%d", i), "EnvironmentSearchTestSeed")
		require.NoError(t, manager.Save(context.Background(), memorystore.Session{ID: sessionID}))
		require.NoError(t, manager.AppendMessages(context.Background(), sessionID, []morphmsg.Message{{
			Role:       morphmsg.RoleTool,
			Name:       "process",
			Content:    `{"status":"running"}`,
			ToolCallID: "call-1",
			CreatedAt:  now.Add(time.Duration(i) * time.Second),
		}}))
	}

	results, err := Search(context.Background(), manager, SessionSearchRequest{
		Query:      "running",
		ToolName:   "process",
		MaxResults: 100,
	})
	require.NoError(t, err)
	require.Len(t, results, maxSessionSearchResults)
	require.True(t, results[0].Messages[0].CreatedAt > results[len(results)-1].Messages[0].CreatedAt)
}

func TestSearch_BuildsRuneSafeSnippet(t *testing.T) {
	store := memorystore.NewStore()
	manager, err := statemanager.NewManager(store, time.Minute, time.Hour)
	require.NoError(t, err)
	require.NoError(t, manager.Save(context.Background(), memorystore.Session{ID: sessionSearchTestSessionID}))
	require.NoError(t, manager.AppendMessages(context.Background(), sessionSearchTestSessionID, []morphmsg.Message{{
		Role:      morphmsg.RoleUser,
		Content:   "AéB needle C",
		CreatedAt: time.Now().UTC(),
	}}))

	results, err := Search(context.Background(), manager, SessionSearchRequest{
		SessionID: sessionSearchTestSessionID,
		Query:     "needle",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "AéB needle C", results[0].Messages[0].Snippet)
}

func TestSearch_ReportsMatchIndexFromOriginalText(t *testing.T) {
	store := memorystore.NewStore()
	manager, err := statemanager.NewManager(store, time.Minute, time.Hour)
	require.NoError(t, err)
	require.NoError(t, manager.Save(context.Background(), memorystore.Session{ID: sessionSearchTestSessionID}))

	text := "İx needle"
	require.NoError(t, manager.AppendMessages(context.Background(), sessionSearchTestSessionID, []morphmsg.Message{{
		Role:      morphmsg.RoleUser,
		Content:   text,
		CreatedAt: time.Now().UTC(),
	}}))

	results, err := Search(context.Background(), manager, SessionSearchRequest{
		SessionID: sessionSearchTestSessionID,
		Query:     "needle",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, strings.Index(text, "needle"), results[0].Messages[0].MatchIndex)
	require.Equal(t, text, results[0].Messages[0].Snippet)
}

func TestSearch_ValidatesManagerAndQuery(t *testing.T) {
	_, err := Search(context.Background(), nil, SessionSearchRequest{Query: "x"})
	require.EqualError(t, err, "state manager is required")

	store := memorystore.NewStore()
	manager, createErr := statemanager.NewManager(store, time.Minute, time.Hour)
	require.NoError(t, createErr)
	require.NoError(t, manager.Save(context.Background(), memorystore.Session{ID: sessionSearchTestSessionID}))

	_, err = Search(context.Background(), manager, SessionSearchRequest{SessionID: sessionSearchTestSessionID})
	require.EqualError(t, err, "query is required")
}

func TestSearch_OmitsOriginSessionWhenSessionIDIsBlank(t *testing.T) {
	store := memorystore.NewStore()
	manager, err := statemanager.NewManager(store, time.Minute, time.Hour)
	require.NoError(t, err)
	require.NoError(t, manager.Save(context.Background(), memorystore.Session{ID: storage.DefaultSessionID}))
	require.NoError(t, manager.Save(context.Background(), memorystore.Session{ID: sessionSearchTestSessionID}))
	require.NoError(t, manager.AppendMessages(context.Background(), storage.DefaultSessionID, []morphmsg.Message{{
		Role:      morphmsg.RoleUser,
		Content:   "other needle",
		CreatedAt: time.Now().UTC(),
	}}))
	require.NoError(t, manager.AppendMessages(context.Background(), sessionSearchTestSessionID, []morphmsg.Message{{
		Role:      morphmsg.RoleUser,
		Content:   "origin needle",
		CreatedAt: time.Now().UTC(),
	}}))

	results, err := Search(context.Background(), manager, SessionSearchRequest{
		IgnoreSessionID: sessionSearchTestSessionID,
		Query:           "needle",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "other needle", results[0].Messages[0].Snippet)
}

func TestSearch_ReturnsStoreErrorsAndSkipsEmptyDerivedSearchText(t *testing.T) {
	store := memorystore.NewStore()
	manager, err := statemanager.NewManager(store, time.Minute, time.Hour)
	require.NoError(t, err)

	_, err = Search(context.Background(), manager, SessionSearchRequest{
		SessionID: "ses_invalid",
		Query:     "needle",
	})
	require.Error(t, err)

	require.NoError(t, manager.Save(context.Background(), memorystore.Session{ID: sessionSearchTestSessionID}))
	require.NoError(t, manager.AppendMessages(context.Background(), sessionSearchTestSessionID, []morphmsg.Message{{
		Role:      morphmsg.RoleAssistant,
		ToolCalls: []morphmsg.ToolCall{{ID: "call-1", Name: "process", Input: "{bad json"}},
		CreatedAt: time.Now().UTC(),
	}}))

	results, err := Search(context.Background(), manager, SessionSearchRequest{
		SessionID: sessionSearchTestSessionID,
		Query:     "needle",
	})
	require.NoError(t, err)
	require.Empty(t, results)
}

func TestSearch_ForwardsCanonicalSearchOptions(t *testing.T) {
	now := time.Now().UTC()
	mockManager, err := statemanager.NewManager(&storagemock.Store{
		SearchMessagesFunc: func(_ context.Context, id string, opts storage.SearchMessageOptions) ([]storage.SearchMessageResult, error) {
			require.Empty(t, id)
			require.Equal(t, storage.DefaultSessionID, opts.IgnoreSessionID)
			require.Equal(t, "needle", opts.Query)
			require.Equal(t, morphmsg.RoleAssistant, opts.Role)
			require.Equal(t, "process", opts.ToolName)
			require.Equal(t, 2, opts.MaxSessions)
			require.Equal(t, maxSessionMatchedMessages, opts.MaxMessagesPerSession)

			return []storage.SearchMessageResult{{
				SessionID:  sessionSearchTestSessionID,
				MatchCount: 1,
				Messages: []storage.SearchMessageHit{{
					SessionID: sessionSearchTestSessionID,
					Message: morphmsg.Message{
						ID:   200,
						Role: morphmsg.RoleAssistant,
						ToolCalls: []morphmsg.ToolCall{{
							ID:    "call-1",
							Name:  "process",
							Input: `{"pattern":"needle"}`,
						}},
						CreatedAt: now,
					},
					MatchedText:     "tool process\ninput pattern needle",
					MatchedToolName: "process",
				}},
			}}, nil
		},
		GetFunc: func(_ context.Context, id string) (storage.Session, bool, error) {
			require.Equal(t, sessionSearchTestSessionID, id)
			return storage.Session{ID: id, CreatedAt: now.Add(-time.Minute), UpdatedAt: now}, true, nil
		},
		GetSummaryFunc: func(_ context.Context, id string) (storage.SessionSummary, bool, error) {
			require.Equal(t, sessionSearchTestSessionID, id)
			return storage.SessionSummary{SessionID: id, SessionSummary: "summary"}, true, nil
		},
	}, time.Minute, time.Hour)
	require.NoError(t, err)

	results, err := Search(context.Background(), mockManager, SessionSearchRequest{
		IgnoreSessionID: storage.DefaultSessionID,
		Query:           "needle",
		Role:            "assistant",
		ToolName:        "process",
		MaxResults:      2,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, sessionSearchTestSessionID, results[0].SessionID)
	require.Equal(t, "summary", results[0].SessionSummary)
	require.Len(t, results[0].Messages, 1)
	require.Equal(t, "assistant", results[0].Messages[0].Role)
}

func TestSearch_ShapesResultsFromStorageMatchedMetadata(t *testing.T) {
	mockManager, err := statemanager.NewManager(&storagemock.Store{
		SearchMessagesFunc: func(_ context.Context, _ string, _ storage.SearchMessageOptions) ([]storage.SearchMessageResult, error) {
			now := time.Date(2026, time.April, 20, 15, 4, 5, 0, time.UTC)
			matchedText := strings.Repeat("x", 140) + "needle" + strings.Repeat("y", 140)
			return []storage.SearchMessageResult{{
				SessionID:  sessionSearchTestSessionID,
				MatchCount: 1,
				Messages: []storage.SearchMessageHit{{
					SessionID: sessionSearchTestSessionID,
					Message: morphmsg.Message{
						ID:        1,
						Role:      morphmsg.RoleAssistant,
						Content:   "original message without query text",
						CreatedAt: now,
					},
					MatchedText:     matchedText,
					MatchedToolName: "search_files",
				}},
			}}, nil
		},
		GetFunc: func(_ context.Context, id string) (storage.Session, bool, error) {
			now := time.Date(2026, time.April, 20, 15, 4, 5, 0, time.UTC)
			return storage.Session{ID: id, CreatedAt: now, UpdatedAt: now}, true, nil
		},
		GetSummaryFunc: func(_ context.Context, id string) (storage.SessionSummary, bool, error) {
			return storage.SessionSummary{SessionID: id}, false, nil
		},
	}, time.Minute, time.Hour)
	require.NoError(t, err)

	results, err := Search(context.Background(), mockManager, SessionSearchRequest{
		SessionID: sessionSearchTestSessionID,
		Query:     "needle",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Len(t, results[0].Messages, 1)

	match := results[0].Messages[0]
	require.Equal(t, "assistant", match.Role)
	require.Equal(t, "search_files", match.ToolName)
	require.Equal(t, "2026-04-20T15:04:05Z", match.CreatedAt)
	require.Equal(t, 140, match.MatchIndex)
	require.Equal(t, 286, match.FullTextBytes)
	require.Contains(t, match.Snippet, "needle")
	require.NotContains(t, match.Snippet, "original message without query text")
	require.True(t, strings.HasPrefix(match.Snippet, "..."))
	require.True(t, strings.HasSuffix(match.Snippet, "..."))
}

func TestSearch_SkipsEmptyHitsAndMissingSessions(t *testing.T) {
	now := time.Now().UTC()
	mockManager, err := statemanager.NewManager(&storagemock.Store{
		SearchMessagesFunc: func(_ context.Context, _ string, _ storage.SearchMessageOptions) ([]storage.SearchMessageResult, error) {
			return []storage.SearchMessageResult{
				{
					SessionID:  "ses_empty",
					MatchCount: 1,
					Messages: []storage.SearchMessageHit{{
						SessionID:   "ses_empty",
						Message:     morphmsg.Message{ID: 1, Role: morphmsg.RoleUser, CreatedAt: now},
						MatchedText: "   ",
					}},
				},
				{
					SessionID:  "ses_missing",
					MatchCount: 1,
					Messages: []storage.SearchMessageHit{{
						SessionID:   "ses_missing",
						Message:     morphmsg.Message{ID: 2, Role: morphmsg.RoleUser, CreatedAt: now},
						MatchedText: "needle missing",
					}},
				},
				{
					SessionID:  sessionSearchTestSessionID,
					MatchCount: 1,
					Messages: []storage.SearchMessageHit{{
						SessionID:   sessionSearchTestSessionID,
						Message:     morphmsg.Message{ID: 3, Role: morphmsg.RoleUser, CreatedAt: now},
						MatchedText: "needle found",
					}},
				},
			}, nil
		},
		GetFunc: func(_ context.Context, id string) (storage.Session, bool, error) {
			if id == "ses_missing" || id == "ses_empty" {
				return storage.Session{}, false, nil
			}

			return storage.Session{ID: id}, true, nil
		},
		GetSummaryFunc: func(_ context.Context, id string) (storage.SessionSummary, bool, error) {
			return storage.SessionSummary{SessionID: id}, false, nil
		},
	}, time.Minute, time.Hour)
	require.NoError(t, err)

	results, err := Search(context.Background(), mockManager, SessionSearchRequest{
		Query: "needle",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, sessionSearchTestSessionID, results[0].SessionID)
	require.Empty(t, results[0].SessionCreated)
	require.Empty(t, results[0].SessionUpdated)
	require.Equal(t, 1, results[0].MatchCount)
	require.Equal(t, "needle found", results[0].Messages[0].Snippet)
}

func TestSearch_SkipsEmptyMatchedMessagesInGroupedResult(t *testing.T) {
	now := time.Now().UTC()
	mockManager, err := statemanager.NewManager(&storagemock.Store{
		SearchMessagesFunc: func(_ context.Context, _ string, _ storage.SearchMessageOptions) ([]storage.SearchMessageResult, error) {
			return []storage.SearchMessageResult{{
				SessionID:  sessionSearchTestSessionID,
				MatchCount: 1,
				Messages: []storage.SearchMessageHit{{
					SessionID:   sessionSearchTestSessionID,
					Message:     morphmsg.Message{ID: 1, Role: morphmsg.RoleUser, CreatedAt: now},
					MatchedText: "   ",
				}},
			}}, nil
		},
		GetFunc: func(_ context.Context, id string) (storage.Session, bool, error) {
			return storage.Session{ID: id, CreatedAt: now, UpdatedAt: now}, true, nil
		},
		GetSummaryFunc: func(_ context.Context, id string) (storage.SessionSummary, bool, error) {
			return storage.SessionSummary{SessionID: id}, false, nil
		},
	}, time.Minute, time.Hour)
	require.NoError(t, err)

	results, err := Search(context.Background(), mockManager, SessionSearchRequest{
		Query: "needle",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Empty(t, results[0].Messages)
}

func TestSearch_ReturnsSessionLookupErrors(t *testing.T) {
	now := time.Now().UTC()

	t.Run("get", func(t *testing.T) {
		mockManager, err := statemanager.NewManager(&storagemock.Store{
			SearchMessagesFunc: func(_ context.Context, _ string, _ storage.SearchMessageOptions) ([]storage.SearchMessageResult, error) {
				return []storage.SearchMessageResult{{
					SessionID:  sessionSearchTestSessionID,
					MatchCount: 1,
					Messages: []storage.SearchMessageHit{{
						SessionID:   sessionSearchTestSessionID,
						Message:     morphmsg.Message{ID: 1, Role: morphmsg.RoleUser, CreatedAt: now},
						MatchedText: "needle",
					}},
				}}, nil
			},
			GetFunc: func(_ context.Context, _ string) (storage.Session, bool, error) {
				return storage.Session{}, false, errors.New("get failed")
			},
		}, time.Minute, time.Hour)
		require.NoError(t, err)

		_, err = Search(context.Background(), mockManager, SessionSearchRequest{Query: "needle"})
		require.EqualError(t, err, "get failed")
	})

	t.Run("summary", func(t *testing.T) {
		mockManager, err := statemanager.NewManager(&storagemock.Store{
			SearchMessagesFunc: func(_ context.Context, _ string, _ storage.SearchMessageOptions) ([]storage.SearchMessageResult, error) {
				return []storage.SearchMessageResult{{
					SessionID:  sessionSearchTestSessionID,
					MatchCount: 1,
					Messages: []storage.SearchMessageHit{{
						SessionID:   sessionSearchTestSessionID,
						Message:     morphmsg.Message{ID: 1, Role: morphmsg.RoleUser, CreatedAt: now},
						MatchedText: "needle",
					}},
				}}, nil
			},
			GetFunc: func(_ context.Context, id string) (storage.Session, bool, error) {
				return storage.Session{ID: id, CreatedAt: now, UpdatedAt: now}, true, nil
			},
			GetSummaryFunc: func(_ context.Context, _ string) (storage.SessionSummary, bool, error) {
				return storage.SessionSummary{}, false, errors.New("summary failed")
			},
		}, time.Minute, time.Hour)
		require.NoError(t, err)

		_, err = Search(context.Background(), mockManager, SessionSearchRequest{Query: "needle"})
		require.EqualError(t, err, "summary failed")
	})
}

func TestSearch_LimitsAndSortsGroupedResultsDeterministically(t *testing.T) {
	now := time.Now().UTC()
	sessionA := nanoid.MustFromSeed(storage.SessionIDPrefix, "session-a", "EnvironmentSearchTestSeed")

	mockManager, err := statemanager.NewManager(&storagemock.Store{
		SearchMessagesFunc: func(_ context.Context, _ string, opts storage.SearchMessageOptions) ([]storage.SearchMessageResult, error) {
			require.Equal(t, 1, opts.MaxSessions)
			require.Equal(t, maxSessionMatchedMessages, opts.MaxMessagesPerSession)

			return []storage.SearchMessageResult{{
				SessionID:     sessionA,
				LastMatchedAt: now,
				MatchCount:    4,
				Messages: []storage.SearchMessageHit{
					{
						SessionID:   sessionA,
						Message:     morphmsg.Message{ID: 30, Role: morphmsg.RoleUser, CreatedAt: now},
						MatchedText: "needle A tie higher id",
					},
					{
						SessionID:   sessionA,
						Message:     morphmsg.Message{ID: 20, Role: morphmsg.RoleUser, CreatedAt: now},
						MatchedText: "needle A newest",
					},
					{
						SessionID:   sessionA,
						Message:     morphmsg.Message{ID: 15, Role: morphmsg.RoleUser, CreatedAt: now.Add(-time.Second)},
						MatchedText: "needle A older",
					},
				},
			}}, nil
		},
		GetFunc: func(_ context.Context, id string) (storage.Session, bool, error) {
			return storage.Session{ID: id, CreatedAt: now, UpdatedAt: now}, true, nil
		},
		GetSummaryFunc: func(_ context.Context, id string) (storage.SessionSummary, bool, error) {
			return storage.SessionSummary{SessionID: id}, false, nil
		},
	}, time.Minute, time.Hour)
	require.NoError(t, err)

	results, err := Search(context.Background(), mockManager, SessionSearchRequest{
		Query:      "needle",
		MaxResults: 1,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, sessionA, results[0].SessionID)
	require.Equal(t, 4, results[0].MatchCount)
	require.Equal(t, []uint{30, 20, 15}, []uint{
		results[0].Messages[0].MessageID,
		results[0].Messages[1].MessageID,
		results[0].Messages[2].MessageID,
	})
}

func TestCaseInsensitiveMatchIndex_AndSnippetAround_EdgeCases(t *testing.T) {
	index, length := getCaseInsensitiveMatchIndex("hello", "")
	require.Equal(t, -1, index)
	require.Zero(t, length)

	index, length = getCaseInsensitiveMatchIndex("hello", "zzz")
	require.Equal(t, -1, index)
	require.Zero(t, length)

	index, length = getCaseInsensitiveMatchIndex("Hello World", "world")
	require.Equal(t, strings.Index("Hello World", "World"), index)
	require.Equal(t, len("World"), length)

	require.Empty(t, getSnippetAround("", 0, 0, 10))
	require.Empty(t, getSnippetAround("hello", 0, 0, 0))

	zeroLenText := strings.Repeat("a", 30)
	zeroLenSnippet := getSnippetAround(zeroLenText, 10, 0, 10)
	require.NotEmpty(t, zeroLenSnippet)

	invalid := string([]byte{'a', 0xff, 'b'})
	require.Equal(t, "ab", getSnippetAround(invalid, 0, 1, 10))

	longText := strings.Repeat("a", 120) + "needle" + strings.Repeat("b", 120)
	snippet := getSnippetAround(longText, 120, len("needle"), 20)
	require.Contains(t, snippet, "needle")
	require.True(t, strings.HasPrefix(snippet, "..."))
	require.True(t, strings.HasSuffix(snippet, "..."))

	endText := strings.Repeat("a", 40) + "needle"
	endSnippet := getSnippetAround(endText, strings.Index(endText, "needle"), len("needle"), 20)
	require.Contains(t, endSnippet, "needle")
	require.True(t, strings.HasPrefix(endSnippet, "..."))
	require.False(t, strings.HasSuffix(endSnippet, "..."))
}

func TestFormatSearchTime_Zero(t *testing.T) {
	require.Empty(t, formatSearchTime(time.Time{}))
}
