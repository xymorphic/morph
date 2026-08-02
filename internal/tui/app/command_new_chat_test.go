package tui

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	rpcclient "github.com/xymorphic/morph/internal/rpc/client"
	storage "github.com/xymorphic/morph/internal/state/core"
)

func TestModel_UpdateHandlesNewChatCommand(t *testing.T) {
	home := t.TempDir()
	setActiveTestProfile(t, home)
	client := &fakeTUIChatClient{
		createdSession: storage.Session{ID: "session-new", Title: "Fresh Thread"},
		contextStatus:  rpcclient.ContextStatus{SessionID: "session-new"},
	}
	runModel := newModelWithClient(client)
	runModel.messages = []transcriptCell{assistantTranscriptCell{text: "old transcript"}}
	runModel.live = assistantTranscriptCell{text: "streaming"}
	runModel.stream.Add("streaming")
	runModel.input.SetValue("/new-chat")
	runModel.setTranscriptContent()
	oldCacheKey := getTranscriptCellRenderCacheKeyForModel(&runModel, assistantTranscriptCell{text: "old transcript"})
	_, oldCellCached := runModel.transcriptCache.get(oldCacheKey)
	require.True(t, oldCellCached)

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, "creating new chat", runModel.status.Text())
	require.Empty(t, runModel.input.Value())

	msg := newChatMessageFromBatch(t, cmd)
	updated, cmd = runModel.Update(msg)

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 1, client.createSessionCalls)
	require.Empty(t, client.createSessionID)
	require.Equal(t, storage.SessionOriginSourceTUI, client.createSessionOptions.OriginSource)
	require.Equal(t, "session-new", runModel.sessionID)
	require.Equal(t, "Fresh Thread", runModel.sessionTitle)
	require.Empty(t, runModel.messages)
	require.Empty(t, runModel.live)
	require.Empty(t, runModel.stream.Render())
	require.False(t, runModel.responding)
	require.False(t, runModel.showIntro)
	require.Equal(t, "new chat created", runModel.status.Text())
	_, oldCellCached = runModel.transcriptCache.get(oldCacheKey)
	require.False(t, oldCellCached)

	_ = sessionContextLoadedMessageFromBatch(t, cmd)
	require.Equal(t, "session-new", client.contextSessionID)

	rememberedID, err := loadLastSessionID()
	require.NoError(t, err)
	require.Equal(t, "session-new", rememberedID)
}

func TestModel_UpdateNewChatCancelsActiveResponse(t *testing.T) {
	cancelled := false
	client := &fakeTUIChatClient{
		createdSession: storage.Session{ID: "session-new"},
		contextStatus:  rpcclient.ContextStatus{SessionID: "session-new"},
	}
	runModel := newModelWithClient(client)
	runModel.responding = true
	runModel.responseCancel = func() { cancelled = true }
	runModel.events = make(chan tea.Msg)
	runModel.responseTranscriptFollow = true
	runModel.responseTranscriptScrolled = true
	runModel.responseRunningToolCount = 2
	runModel.thinkingComposerActive = true
	runModel.toolAnimationActive = true
	runModel.input.SetValue("/new-chat")

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)

	msg := newChatMessageFromBatch(t, cmd)
	updated, _ = runModel.Update(msg)

	runModel = updated.(model)
	require.True(t, cancelled)
	require.False(t, runModel.responding)
	require.Nil(t, runModel.responseCancel)
	require.Nil(t, runModel.events)
	require.False(t, runModel.responseTranscriptFollow)
	require.False(t, runModel.responseTranscriptScrolled)
	require.Zero(t, runModel.responseRunningToolCount)
	require.False(t, runModel.thinkingComposerActive)
	require.False(t, runModel.toolAnimationActive)
	require.Equal(t, "session-new", runModel.sessionID)
}

func TestModel_UpdateUsesSessionIDForUntitledNewChat(t *testing.T) {
	client := &fakeTUIChatClient{
		createdSession: storage.Session{ID: "session-new"},
		contextStatus:  rpcclient.ContextStatus{SessionID: "session-new"},
	}
	runModel := newModelWithClient(client)
	runModel.input.SetValue("/new-chat")

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)

	msg := newChatMessageFromBatch(t, cmd)
	updated, _ = runModel.Update(msg)

	runModel = updated.(model)
	require.Equal(t, "session-new", runModel.sessionID)
	require.Equal(t, "session-new", runModel.sessionTitle)
}

func TestModel_UpdateReportsNewChatFailure(t *testing.T) {
	expectedErr := errors.New("create failed")
	runModel := newModelWithClient(&fakeTUIChatClient{createSessionErr: expectedErr})
	runModel.messages = []transcriptCell{assistantTranscriptCell{text: "old transcript"}}
	runModel.input.SetValue("/new-chat")

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)

	msg := newChatMessageFromBatch(t, cmd)
	updated, cmd = runModel.Update(msg)

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, "new chat failed", runModel.status.Text())
	require.Equal(t, []string{"Morph: old transcript"}, transcriptCellPlainTexts(runModel.messages))
}

func TestModel_UpdateReportsNewChatFailureForEmptySessionID(t *testing.T) {
	runModel := newModelWithClient(&fakeTUIChatClient{createdSession: storage.Session{Title: "Missing ID"}})
	runModel.input.SetValue("/new-chat")

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runModel = updated.(model)

	msg := newChatMessageFromBatch(t, cmd)
	updated, _ = runModel.Update(msg)

	runModel = updated.(model)
	require.Equal(t, "new chat failed", runModel.status.Text())
	require.Equal(t, defaultSessionID, runModel.sessionID)
}

func TestModel_UpdateReportsNewChatUnavailable(t *testing.T) {
	runModel := newModel()
	runModel.input.SetValue("/new-chat")

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, "new chat unavailable", runModel.status.Text())
}

func newChatMessageFromBatch(t *testing.T, cmd tea.Cmd) newChatCompletedMsg {
	t.Helper()

	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	require.Len(t, batch, 2)

	msg, ok := batch[1]().(newChatCompletedMsg)
	require.True(t, ok)

	return msg
}

func sessionContextLoadedMessageFromBatch(t *testing.T, cmd tea.Cmd) sessionContextLoadedMsg {
	t.Helper()

	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(batch), 2)

	msg, ok := batch[1]().(sessionContextLoadedMsg)
	require.True(t, ok)

	return msg
}
