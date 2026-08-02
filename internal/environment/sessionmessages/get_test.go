package sessionmessages

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	storage "github.com/xymorphic/morph/internal/state/core"
	statemanager "github.com/xymorphic/morph/internal/state/manager"
	storagemock "github.com/xymorphic/morph/internal/state/mock"
	memorystore "github.com/xymorphic/morph/internal/state/storememory"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/logutils"
	"github.com/xymorphic/morph/pkg/nanoid"
)

var testSessionID = nanoid.MustFromSeed(storage.SessionIDPrefix, "session-messages", "Seed")

func TestGet_RejectsNilManager(t *testing.T) {
	_, err := Get(context.Background(), nil, SessionMessagesRequest{
		MessageIDs: []uint{1},
	})

	require.EqualError(t, err, "state manager is required")
}

func TestGet_RejectsInvalidRequest(t *testing.T) {
	manager := newSessionMessagesTestManager(t)

	_, err := Get(context.Background(), manager, SessionMessagesRequest{})

	require.EqualError(t, err, "exactly one session message selector must be provided")
}

func TestGet_ReturnsCurrentSessionLookupError(t *testing.T) {
	manager, err := statemanager.NewManager(&storagemock.Store{
		CurrentFunc: func(context.Context) (string, bool, error) {
			return "", false, errors.New("current session failed")
		},
	}, time.Minute, time.Hour)
	require.NoError(t, err)

	_, err = Get(context.Background(), manager, SessionMessagesRequest{
		MessageIDs: []uint{1},
	})

	require.EqualError(t, err, "current session failed")
}

func TestGet_ReturnsMessagesByIDsInTranscriptOrder(t *testing.T) {
	manager := newSessionMessagesTestManager(t)

	response, err := Get(context.Background(), manager, SessionMessagesRequest{
		SessionID:  testSessionID,
		MessageIDs: []uint{4, 2},
	})
	require.NoError(t, err)
	require.Equal(t, testSessionID, response.SessionID)
	require.Len(t, response.Messages, 2)
	require.Equal(t, []uint{2, 4}, []uint{response.Messages[0].MessageID, response.Messages[1].MessageID})
	require.Equal(t, []int{1, 3}, []int{response.Messages[0].Offset, response.Messages[1].Offset})
}

func TestGet_UsesCurrentSessionWhenSessionIDIsOmitted(t *testing.T) {
	manager := newSessionMessagesTestManager(t)
	require.NoError(t, manager.UseSession(context.Background(), testSessionID))

	response, err := Get(context.Background(), manager, SessionMessagesRequest{
		MessageIDs: []uint{2},
	})
	require.NoError(t, err)
	require.Equal(t, testSessionID, response.SessionID)
	require.Len(t, response.Messages, 1)
	require.Equal(t, uint(2), response.Messages[0].MessageID)
}

func TestGet_UsesDefaultSessionWhenSessionIDIsOmittedAndNoCurrentSessionExists(t *testing.T) {
	manager, err := statemanager.NewManager(memorystore.NewStore(), time.Minute, time.Hour)
	require.NoError(t, err)
	_, err = manager.Resolve(context.Background(), "")
	require.NoError(t, err)
	require.NoError(t, manager.AppendMessages(context.Background(), storage.DefaultSessionID, []morphmsg.Message{
		{ID: 1, Role: morphmsg.RoleUser, Content: "default"},
	}))

	response, err := Get(context.Background(), manager, SessionMessagesRequest{
		MessageIDs: []uint{1},
	})
	require.NoError(t, err)
	require.Equal(t, storage.DefaultSessionID, response.SessionID)
	require.Len(t, response.Messages, 1)
	require.Equal(t, uint(1), response.Messages[0].MessageID)
}

func TestGet_ReturnsAnchorWindowAndTruncatesContent(t *testing.T) {
	manager := newSessionMessagesTestManager(t)

	response, err := Get(context.Background(), manager, SessionMessagesRequest{
		SessionID:       testSessionID,
		AnchorMessageID: 3,
		Before:          1,
		After:           1,
		MaxChars:        4,
	})
	logutils.PrettyPrint(response)
	require.NoError(t, err)
	require.True(t, response.Truncated)
	require.Len(t, response.Messages, 3)
	require.Equal(t, []int{1, 2, 3}, []int{response.Messages[0].Offset, response.Messages[1].Offset, response.Messages[2].Offset})
	require.Equal(t, "proc", response.Messages[1].Content)
	require.True(t, response.Messages[1].Truncated)
	require.Equal(t, "process", response.Messages[1].ToolName)
}

func TestGet_ReturnsAssistantToolCallsAndTruncatesInputs(t *testing.T) {
	manager := newSessionMessagesTestManager(t)
	offsetStart := 4
	offsetEnd := 5

	response, err := Get(context.Background(), manager, SessionMessagesRequest{
		SessionID:   testSessionID,
		OffsetStart: &offsetStart,
		OffsetEnd:   &offsetEnd,
		MaxChars:    5,
	})
	require.NoError(t, err)
	require.True(t, response.Truncated)
	require.Len(t, response.Messages, 1)
	require.Empty(t, response.Messages[0].Content)
	require.Len(t, response.Messages[0].ToolCalls, 2)
	require.Equal(t, "search_files", response.Messages[0].ToolCalls[0].Name)
	require.Equal(t, `{"pat`, response.Messages[0].ToolCalls[0].Input)
	require.True(t, response.Messages[0].ToolCalls[0].Truncated)
	require.True(t, response.Messages[0].Truncated)
}

func TestGet_PreservesExactStoredContentWhenNotTruncated(t *testing.T) {
	manager, err := statemanager.NewManager(memorystore.NewStore(), time.Minute, time.Hour)
	require.NoError(t, err)
	require.NoError(t, manager.Save(context.Background(), memorystore.Session{ID: testSessionID}))
	require.NoError(t, manager.AppendMessages(context.Background(), testSessionID, []morphmsg.Message{
		{
			ID:        7,
			Role:      morphmsg.RoleAssistant,
			Content:   "  keep exact spacing  \n",
			CreatedAt: time.Now().UTC(),
			ToolCalls: []morphmsg.ToolCall{
				{ID: "call-7", Name: "write_file", Input: "  {\"path\":\" spaced \"}  \n"},
			},
		},
	}))

	response, err := Get(context.Background(), manager, SessionMessagesRequest{
		SessionID:  testSessionID,
		MessageIDs: []uint{7},
	})
	require.NoError(t, err)
	require.Len(t, response.Messages, 1)
	require.Equal(t, "  keep exact spacing  \n", response.Messages[0].Content)
	require.Len(t, response.Messages[0].ToolCalls, 1)
	require.Equal(t, "  {\"path\":\" spaced \"}  \n", response.Messages[0].ToolCalls[0].Input)
	require.False(t, response.Truncated)
	require.False(t, response.Messages[0].Truncated)
	require.False(t, response.Messages[0].ToolCalls[0].Truncated)
}

func TestGet_ReturnsOffsetRange(t *testing.T) {
	manager := newSessionMessagesTestManager(t)
	offsetStart := 0
	offsetEnd := 2

	response, err := Get(context.Background(), manager, SessionMessagesRequest{
		SessionID:   testSessionID,
		OffsetStart: &offsetStart,
		OffsetEnd:   &offsetEnd,
	})
	require.NoError(t, err)
	require.Len(t, response.Messages, 2)
	require.Equal(t, []uint{1, 2}, []uint{response.Messages[0].MessageID, response.Messages[1].MessageID})
	require.Equal(t, []int{0, 1}, []int{response.Messages[0].Offset, response.Messages[1].Offset})
}

func TestGet_ReturnsOffsetRangeError(t *testing.T) {
	offsetStart := 0
	offsetEnd := 2
	manager, err := statemanager.NewManager(&storagemock.Store{
		GetMessagesFunc: func(context.Context, string, storage.MessageQueryOptions) ([]morphmsg.Message, error) {
			return nil, errors.New("offset range failed")
		},
	}, time.Minute, time.Hour)
	require.NoError(t, err)

	_, err = Get(context.Background(), manager, SessionMessagesRequest{
		SessionID:   testSessionID,
		OffsetStart: &offsetStart,
		OffsetEnd:   &offsetEnd,
	})

	require.EqualError(t, err, "offset range failed")
}

func TestGet_UsesDirectMessageIDLookupInsteadOfFullTranscriptLoad(t *testing.T) {
	manager, err := statemanager.NewManager(&storagemock.Store{
		GetMessagesFunc: func(context.Context, string, storage.MessageQueryOptions) ([]morphmsg.Message, error) {
			return nil, errors.New("full transcript load should not be used")
		},
		GetMessagesByIDsFunc: func(_ context.Context, id string, messageIDs []uint) ([]storage.MessageRecord, error) {
			require.Equal(t, testSessionID, id)
			require.Equal(t, []uint{7}, messageIDs)
			return []storage.MessageRecord{{
				Offset:  3,
				Message: morphmsg.Message{ID: 7, Role: morphmsg.RoleAssistant, Content: "exact"},
			}}, nil
		},
	}, time.Minute, time.Hour)
	require.NoError(t, err)

	response, err := Get(context.Background(), manager, SessionMessagesRequest{
		SessionID:  testSessionID,
		MessageIDs: []uint{7},
	})
	require.NoError(t, err)
	require.Len(t, response.Messages, 1)
	require.Equal(t, "exact", response.Messages[0].Content)
}

func TestGet_ReturnsMessageIDLookupError(t *testing.T) {
	manager, err := statemanager.NewManager(&storagemock.Store{
		GetMessagesByIDsFunc: func(context.Context, string, []uint) ([]storage.MessageRecord, error) {
			return nil, errors.New("message id lookup failed")
		},
	}, time.Minute, time.Hour)
	require.NoError(t, err)

	_, err = Get(context.Background(), manager, SessionMessagesRequest{
		SessionID:  testSessionID,
		MessageIDs: []uint{7},
	})

	require.EqualError(t, err, "message id lookup failed")
}

func TestGet_UsesDirectAnchorWindowLookupInsteadOfFullTranscriptLoad(t *testing.T) {
	manager, err := statemanager.NewManager(&storagemock.Store{
		GetMessagesFunc: func(context.Context, string, storage.MessageQueryOptions) ([]morphmsg.Message, error) {
			return nil, errors.New("full transcript load should not be used")
		},
		GetMessageWindowFunc: func(_ context.Context, id string, anchorMessageID uint, before int, after int) ([]storage.MessageRecord, error) {
			require.Equal(t, testSessionID, id)
			require.Equal(t, uint(9), anchorMessageID)
			require.Equal(t, 1, before)
			require.Equal(t, 1, after)
			return []storage.MessageRecord{{
				Offset:  4,
				Message: morphmsg.Message{ID: 9, Role: morphmsg.RoleUser, Content: "anchor"},
			}}, nil
		},
	}, time.Minute, time.Hour)
	require.NoError(t, err)

	response, err := Get(context.Background(), manager, SessionMessagesRequest{
		SessionID:       testSessionID,
		AnchorMessageID: 9,
		Before:          1,
		After:           1,
	})
	require.NoError(t, err)
	require.Len(t, response.Messages, 1)
	require.Equal(t, "anchor", response.Messages[0].Content)
}

func TestGet_ReturnsAnchorWindowLookupError(t *testing.T) {
	manager, err := statemanager.NewManager(&storagemock.Store{
		GetMessageWindowFunc: func(context.Context, string, uint, int, int) ([]storage.MessageRecord, error) {
			return nil, errors.New("anchor lookup failed")
		},
	}, time.Minute, time.Hour)
	require.NoError(t, err)

	_, err = Get(context.Background(), manager, SessionMessagesRequest{
		SessionID:       testSessionID,
		AnchorMessageID: 9,
		Before:          1,
		After:           1,
	})

	require.EqualError(t, err, "anchor lookup failed")
}

func TestTruncateMessageContent_HandlesUTF8AndBounds(t *testing.T) {
	content, truncated := truncateMessageContent("ok", 5)
	require.Equal(t, "ok", content)
	require.False(t, truncated)

	content, truncated = truncateMessageContent("abcdef", 3)
	require.Equal(t, "abc", content)
	require.True(t, truncated)

	content, truncated = truncateMessageContent("bad\xfftext", 0)
	require.Equal(t, "badtext", content)
	require.False(t, truncated)
}

func newSessionMessagesTestManager(t *testing.T) *statemanager.Manager {
	t.Helper()

	store := memorystore.NewStore()
	manager, err := statemanager.NewManager(store, time.Minute, time.Hour)
	require.NoError(t, err)
	require.NoError(t, manager.Save(context.Background(), memorystore.Session{ID: testSessionID}))
	require.NoError(t, manager.AppendMessages(context.Background(), testSessionID, []morphmsg.Message{
		{Role: morphmsg.RoleUser, Content: "alpha", CreatedAt: time.Now().UTC()},
		{Role: morphmsg.RoleAssistant, Content: "beta", CreatedAt: time.Now().UTC().Add(time.Second)},
		{Role: morphmsg.RoleTool, Name: "process", ToolCallID: "call-1", Content: "process-running", CreatedAt: time.Now().UTC().Add(2 * time.Second)},
		{Role: morphmsg.RoleAssistant, Content: "delta", CreatedAt: time.Now().UTC().Add(3 * time.Second)},
		{
			Role:      morphmsg.RoleAssistant,
			Content:   "",
			CreatedAt: time.Now().UTC().Add(4 * time.Second),
			ToolCalls: []morphmsg.ToolCall{
				{ID: "call-2", Name: "search_files", Input: `{"pattern":"needle"}`},
				{ID: "call-3", Name: "read_file", Input: `{"path":"README.md"}`},
			},
		},
	}))

	return manager
}
