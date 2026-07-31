package tui

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	rpcclient "github.com/wandxy/morph/internal/rpc/client"
	storage "github.com/wandxy/morph/internal/state/core"
)

func TestModel_UpdateHandlesChatsCommand(t *testing.T) {
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	originalNow := chatsNow
	chatsNow = func() time.Time { return now }
	t.Cleanup(func() {
		chatsNow = originalNow
	})

	client := &fakeTUIChatClient{sessions: []storage.Session{
		{ID: "ses_current", Title: "This is a chat title", UpdatedAt: now.Add(-3 * 24 * time.Hour)},
		{ID: "ses_other", Title: "Another chat title", UpdatedAt: now.Add(-4 * 24 * time.Hour)},
		{ID: "ses_long", Title: "Another title and more yet another one", UpdatedAt: now.Add(-90 * time.Minute)},
	}}
	runModel := newModelWithClient(client)
	runModel.width = 72
	runModel.applyAction(setSessionAction{ID: "ses_current", Title: "This is a chat title"})
	runModel.input.SetValue("/chats")

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	runModel = updated.(model)

	msg := chatsLoadedMessageFromBatch(t, cmd)
	require.Len(t, msg.Sessions, 3)
	require.NoError(t, msg.Err)

	updated, cmd = runModel.Update(msg)
	require.Nil(t, cmd)
	runModel = updated.(model)

	require.True(t, runModel.isCommandViewVisible())
	require.Equal(t, "Chats", runModel.commandView.TitleLeft)
	require.Equal(t, "enter to open · r to rename · d to archive · esc to close", runModel.commandView.TitleRight)
	require.Equal(t, commandViewKindChats, runModel.commandView.Kind)
	require.Equal(t, len(runModel.commandView.Chats), runModel.getCommandViewContentHeight())
	require.Len(t, runModel.commandView.Chats, 3)
	require.Zero(t, runModel.commandViewItemSelected)
	require.Equal(t, 1, client.listSessionCalls)

	content := stripANSI(runModel.View().Content)
	require.Contains(t, content, "Chats")
	require.Contains(t, content, " This is a chat title")
	require.NotContains(t, content, "current This is a chat title")
	require.Contains(t, content, "3d ago")
	require.Contains(t, content, "Another chat title")
	require.Contains(t, content, "4d ago")
	require.Contains(t, content, "Another title and more")
	require.Contains(t, content, "1h ago")
	require.NotContains(t, content, inputPrompt+"Ask Morph")
	require.Contains(t, runModel.renderCommandView(), "48;")
}

func TestLoadChatsCmdUsesBackgroundContextWhenNil(t *testing.T) {
	client := &fakeTUIChatClient{sessions: []storage.Session{{ID: "ses_1"}}}

	var nilContext context.Context
	msg, ok := loadChatsCmd(nilContext, client)().(chatsLoadedMsg)

	require.True(t, ok)
	require.NoError(t, msg.Err)
	require.Equal(t, []storage.Session{{ID: "ses_1"}}, msg.Sessions)
	require.Equal(t, 1, client.listSessionCalls)
}

func TestModel_UpdateHandlesArchiveCommand(t *testing.T) {
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	originalNow := chatsNow
	chatsNow = func() time.Time { return now }
	t.Cleanup(func() {
		chatsNow = originalNow
	})

	client := &fakeTUIChatClient{archivedSessions: []storage.Session{
		{ID: "ses_archived", Title: "Archived chat", Archived: true, UpdatedAt: now.Add(-24 * time.Hour)},
	}}
	runModel := newModelWithClient(client)
	runModel.width = 72
	runModel.input.SetValue("/archive")

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	runModel = updated.(model)

	msg := archivedChatsLoadedMessageFromBatch(t, cmd)
	require.Len(t, msg.Sessions, 1)
	require.NoError(t, msg.Err)

	updated, cmd = runModel.Update(msg)
	require.Nil(t, cmd)
	runModel = updated.(model)

	require.True(t, runModel.isCommandViewVisible())
	require.Equal(t, "Archive", runModel.commandView.TitleLeft)
	require.Equal(t, getArchiveCommandTitleRight(), runModel.commandView.TitleRight)
	require.Equal(t, commandViewKindArchive, runModel.commandView.Kind)
	require.Len(t, runModel.commandView.Chats, 1)
	require.Equal(t, 1, client.listArchivedCalls)

	content := stripANSI(runModel.View().Content)
	require.Contains(t, content, "Archived chat")
	require.Contains(t, content, "archived")
}

func TestModel_RenderArchiveCommandViewHidesBottomBorder(t *testing.T) {
	runModel := newModel()
	runModel.height = 16
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Archive",
		Kind:      commandViewKindArchive,
		Chats: []storage.Session{
			{ID: "ses_archived", Title: "Archived chat", Archived: true},
		},
	})

	lines := strings.Split(stripANSI(runModel.renderCommandView()), "\n")

	require.Len(t, lines, runModel.getCommandViewHeight())
	require.NotContains(t, lines[len(lines)-1], "╰")
	require.NotContains(t, lines[len(lines)-1], "╯")
}

func TestLoadArchiveCmdUsesBackgroundContextWhenNil(t *testing.T) {
	client := &fakeTUIChatClient{archivedSessions: []storage.Session{{ID: "ses_1", Archived: true}}}

	var nilContext context.Context
	msg, ok := loadArchiveCmd(nilContext, client)().(archivedChatsLoadedMsg)

	require.True(t, ok)
	require.NoError(t, msg.Err)
	require.Equal(t, []storage.Session{{ID: "ses_1", Archived: true}}, msg.Sessions)
	require.Equal(t, 1, client.listArchivedCalls)
}

func TestModel_UpdateChatsCommandMovesSelectionWithArrowKeys(t *testing.T) {
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	originalNow := chatsNow
	chatsNow = func() time.Time { return now }
	t.Cleanup(func() {
		chatsNow = originalNow
	})

	runModel := newModelWithClient(&fakeTUIChatClient{})
	runModel.height = 7
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Chats",
		Kind:      commandViewKindChats,
		Chats: []storage.Session{
			{ID: "ses_1", Title: "One", UpdatedAt: now},
			{ID: "ses_2", Title: "Two", UpdatedAt: now},
			{ID: "ses_3", Title: "Three", UpdatedAt: now},
			{ID: "ses_4", Title: "Four", UpdatedAt: now},
			{ID: "ses_5", Title: "Five", UpdatedAt: now},
			{ID: "ses_6", Title: "Six", UpdatedAt: now},
		},
	})

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 1, runModel.commandViewItemSelected)

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 2, runModel.commandViewItemSelected)

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 1, runModel.commandViewItemSelected)

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnd}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 5, runModel.commandViewItemSelected)
	require.Greater(t, runModel.commandViewOffset, 0)

	content := stripANSI(runModel.renderCommandView())
	require.Contains(t, content, "Six")
	require.NotContains(t, content, "One")
}

func TestModel_UpdateChatsCommandCoversNavigationEdges(t *testing.T) {
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	runModel := newModelWithClient(&fakeTUIChatClient{})
	runModel.height = 12
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Chats",
		Kind:      commandViewKindChats,
		Chats: []storage.Session{
			{ID: "ses_1", Title: "One", UpdatedAt: now},
			{ID: "ses_2", Title: "Two", UpdatedAt: now},
			{ID: "ses_3", Title: "Three", UpdatedAt: now},
			{ID: "ses_4", Title: "Four", UpdatedAt: now},
			{ID: "ses_5", Title: "Five", UpdatedAt: now},
			{ID: "ses_6", Title: "Six", UpdatedAt: now},
		},
	})

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnd}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 5, runModel.commandViewItemSelected)

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyHome}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Zero(t, runModel.commandViewItemSelected)

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Greater(t, runModel.commandViewItemSelected, 0)

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Zero(t, runModel.commandViewItemSelected)

	updated, cmd = runModel.Update(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelDown}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 1, runModel.commandViewItemSelected)

	updated, cmd = runModel.Update(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelUp}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Zero(t, runModel.commandViewItemSelected)

	updated, cmd = runModel.Update(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseLeft}))
	require.Nil(t, cmd)
	require.Equal(t, runModel.commandViewItemSelected, updated.(model).commandViewItemSelected)

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	require.Nil(t, cmd)
	require.False(t, updated.(model).isCommandViewVisible())
}

func TestModel_UpdateChatsCommandIgnoresUnhandledMessagesAndEmptyList(t *testing.T) {
	runModel := newModel()
	runModel.showCommandView(commandViewPayload{TitleLeft: "Chats", Kind: commandViewKindChats})

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	require.Nil(t, cmd)
	require.Zero(t, updated.(model).commandViewItemSelected)

	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Chats",
		Kind:      commandViewKindChats,
		Chats:     []storage.Session{{ID: "ses_1", Title: "One"}},
	})
	updated, cmd = runModel.Update(tea.MouseClickMsg{})
	require.Nil(t, cmd)
	require.Zero(t, updated.(model).commandViewItemSelected)

	updated, cmd = runModel.updateChatsCommandView(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	require.Nil(t, cmd)
	require.Equal(t, runModel.commandViewItemSelected, updated.(model).commandViewItemSelected)

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'x'}))
	require.Nil(t, cmd)
	require.Zero(t, updated.(model).commandViewItemSelected)

	updated, cmd = runModel.updateChatsCommandView(statusExpiredMsg{})
	require.Nil(t, cmd)
	require.Zero(t, updated.(model).commandViewItemSelected)
}

func TestModel_UpdateChatsCommandSwitchesSelectedSession(t *testing.T) {
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	client := &fakeTUIChatClient{
		timeline: rpcclient.SessionTimeline{
			SessionID: "ses_other",
			Title:     "Other Chat",
		},
	}
	runModel := newModelWithClient(client)
	runModel.applyAction(setSessionAction{ID: "ses_current", Title: "Current Chat"})
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Chats",
		Kind:      commandViewKindChats,
		Chats: []storage.Session{
			{ID: "ses_current", Title: "Current Chat", UpdatedAt: now},
			{ID: "ses_other", Title: "Other Chat", UpdatedAt: now},
		},
	})

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	require.Nil(t, cmd)
	runModel = updated.(model)

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	runModel = updated.(model)

	require.False(t, runModel.isCommandViewVisible())
	require.True(t, runModel.chatSwitching)
	require.Equal(t, "switching chat", runModel.status.Text())

	blocked, blockedCmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'x'}))
	require.Nil(t, blockedCmd)
	blockedModel := blocked.(model)
	require.Empty(t, blockedModel.input.Value())
	require.True(t, blockedModel.chatSwitching)

	msg := chatSwitchTimelineMessageFromBatch(t, cmd)
	require.Equal(t, "ses_other", client.usedSessionID)
	require.Equal(t, "ses_other", client.timelineSessionID)
	require.Equal(t, "ses_other", msg.Timeline.SessionID)

	updated, cmd = runModel.Update(msg)
	require.NotNil(t, cmd)
	runModel = updated.(model)

	require.False(t, runModel.chatSwitching)
	require.Equal(t, "ses_other", runModel.getCurrentSessionID())
	require.Equal(t, "Other Chat", runModel.sessionTitle)
	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	_, ok = batch[0]().(sessionContextLoadedMsg)
	require.True(t, ok)
	require.Equal(t, "ses_other", client.contextSessionID)
}

func TestModel_ChatSwitchingDisablesComposerUpdatePaths(t *testing.T) {
	runModel := newModel()
	runModel.chatSwitching = true

	updated, cmd := runModel.handlePasteMsg(tea.PasteMsg{Content: "hello"})
	require.Nil(t, cmd)
	require.Empty(t, updated.(model).input.Value())

	updated, cmd = runModel.updateInputComposer(tea.KeyPressMsg(tea.Key{Code: 'x'}))
	require.Nil(t, cmd)
	require.Empty(t, updated.(model).input.Value())

	updated, cmd = runModel.updateBubbleTeaChildren(tea.KeyPressMsg(tea.Key{Code: 'x'}))
	require.Nil(t, cmd)
	require.Empty(t, updated.(model).input.Value())
}

func TestModel_UpdateChatsCommandSwitchSelectionFailures(t *testing.T) {
	runModel := newModel()
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Chats",
		Kind:      commandViewKindChats,
		Chats:     []storage.Session{{Title: "Missing ID"}},
	})

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.isCommandViewVisible())
	require.Equal(t, "chat switch unavailable", runModel.status.Text())

	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Chats",
		Kind:      commandViewKindChats,
		Chats:     []storage.Session{{ID: "ses_other", Title: "Other"}},
	})
	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.isCommandViewVisible())
	require.Equal(t, "chat switch unavailable", runModel.status.Text())
}

func TestModel_UpdateChatsCommandConfirmsAndCancelsArchive(t *testing.T) {
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	runModel := newModelWithClient(&fakeTUIChatClient{})
	runModel.applyAction(setSessionAction{ID: "ses_current", Title: "Current Chat"})
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Chats",
		Kind:      commandViewKindChats,
		Chats: []storage.Session{
			{ID: "ses_current", Title: "Current Chat", UpdatedAt: now},
			{ID: "ses_other", Title: "Other Chat", UpdatedAt: now},
		},
	})
	runModel.commandViewItemSelected = 1

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'd'}))
	require.NotNil(t, cmd)
	runModel = updated.(model)

	require.True(t, runModel.chatsArchiveConfirm)
	require.Equal(t, "enter to archive · esc to cancel", runModel.commandView.TitleRight)
	require.Equal(t, "press enter to archive chat", runModel.status.Text())

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	require.NotNil(t, cmd)
	runModel = updated.(model)

	require.True(t, runModel.isCommandViewVisible())
	require.False(t, runModel.chatsArchiveConfirm)
	require.Equal(t, "enter to open · r to rename · d to archive · esc to close", runModel.commandView.TitleRight)
	require.Equal(t, "chat archive cancelled", runModel.status.Text())
}

func TestModel_CommandViewActionsClearArchiveConfirmation(t *testing.T) {
	runModel := newModel()
	runModel.chatsArchiveConfirm = true

	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Chats",
		Kind:      commandViewKindChats,
		Chats:     []storage.Session{{ID: "ses_other", Title: "Other"}},
	})

	require.False(t, runModel.chatsArchiveConfirm)

	runModel.chatsArchiveConfirm = true
	runModel = runModel.hideCommandView()

	require.False(t, runModel.chatsArchiveConfirm)
}

func TestModel_UpdateChatsCommandArchivesSelectedSession(t *testing.T) {
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	client := &fakeTUIChatClient{}
	runModel := newModelWithClient(client)
	runModel.applyAction(setSessionAction{ID: "ses_current", Title: "Current Chat"})
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Chats",
		Kind:      commandViewKindChats,
		Chats: []storage.Session{
			{ID: "ses_current", Title: "Current Chat", UpdatedAt: now},
			{ID: "ses_other", Title: "Other Chat", UpdatedAt: now},
			{ID: "ses_last", Title: "Last Chat", UpdatedAt: now},
		},
	})
	runModel.commandViewItemSelected = 1

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'd'}))
	require.NotNil(t, cmd)
	runModel = updated.(model)

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	runModel = updated.(model)

	require.False(t, runModel.chatsArchiveConfirm)
	require.Equal(t, "archiving chat", runModel.status.Text())

	msg := chatArchivedMessageFromBatch(t, cmd)
	require.Equal(t, "ses_other", msg.ID)
	require.NoError(t, msg.Err)
	require.Equal(t, "ses_other", client.archivedSessionID)
	require.Equal(t, 1, client.archiveSessionCalls)

	updated, _ = runModel.Update(msg)
	runModel = updated.(model)

	require.Equal(t, "chat archived", runModel.status.Text())
	require.Len(t, runModel.commandView.Chats, 2)
	require.Equal(t, "ses_current", runModel.commandView.Chats[0].ID)
	require.Equal(t, "ses_last", runModel.commandView.Chats[1].ID)
	require.Equal(t, 1, runModel.commandViewItemSelected)
}

func TestModel_UpdateChatsCommandKeepsSessionOnArchiveFailure(t *testing.T) {
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	expected := errors.New("archive failed")
	client := &fakeTUIChatClient{archiveSessionErr: expected}
	runModel := newModelWithClient(client)
	runModel.applyAction(setSessionAction{ID: "ses_current", Title: "Current Chat"})
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Chats",
		Kind:      commandViewKindChats,
		Chats: []storage.Session{
			{ID: "ses_current", Title: "Current Chat", UpdatedAt: now},
			{ID: "ses_other", Title: "Other Chat", UpdatedAt: now},
		},
	})
	runModel.commandViewItemSelected = 1

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'd'}))
	require.NotNil(t, cmd)
	runModel = updated.(model)

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	runModel = updated.(model)

	msg := chatArchivedMessageFromBatch(t, cmd)
	require.ErrorIs(t, msg.Err, expected)
	require.Equal(t, "ses_other", client.archivedSessionID)
	require.Equal(t, 1, client.archiveSessionCalls)

	updated, _ = runModel.Update(msg)
	runModel = updated.(model)

	require.Equal(t, "chat archive unavailable", runModel.status.Text())
	require.Len(t, runModel.commandView.Chats, 2)
	require.Equal(t, "ses_other", runModel.commandView.Chats[1].ID)
	require.Equal(t, 1, runModel.commandViewItemSelected)
}

func TestModel_UpdateChatsCommandRenamesSelectedSession(t *testing.T) {
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	client := &fakeTUIChatClient{}
	runModel := newModelWithClient(client)
	runModel.width = 72
	runModel.applyAction(setSessionAction{ID: "ses_current", Title: "Current Chat"})
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Chats",
		Kind:      commandViewKindChats,
		Chats: []storage.Session{
			{ID: "ses_current", Title: "Current Chat", UpdatedAt: now},
			{ID: "ses_other", Title: "Other Chat", UpdatedAt: now},
		},
	})
	runModel.commandViewItemSelected = 1

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'r'}))
	require.NotNil(t, cmd)
	runModel = updated.(model)

	require.True(t, runModel.chatsRenaming)
	require.Equal(t, "ses_other", runModel.chatsRenameSessionID)
	require.Equal(t, "Other Chat", runModel.renameInput.Value())
	require.Equal(t, "enter to save · esc to cancel", runModel.commandView.TitleRight)
	require.Contains(t, stripANSI(runModel.renderCommandView()), "Other Chat")

	runModel.renameInput.SetValue(" Renamed Chat ")
	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.chatsRenaming)
	require.Equal(t, "renaming chat", runModel.status.Text())

	msg := chatRenamedMessageFromBatch(t, cmd)
	require.Equal(t, "ses_other", msg.Session.ID)
	require.Equal(t, "Renamed Chat", msg.Session.Title)
	require.Equal(t, storage.SessionTitleSourceManual, msg.Session.TitleSource)
	require.Equal(t, "ses_other", client.renamedSessionID)
	require.Equal(t, "Renamed Chat", client.renamedSessionTitle)
	require.Equal(t, 1, client.renameSessionCalls)

	updated, cmd = runModel.Update(msg)
	runModel = updated.(model)

	require.False(t, runModel.chatsRenaming)
	require.Empty(t, runModel.chatsRenameSessionID)
	require.Equal(t, "chat renamed", runModel.status.Text())
	require.Equal(t, "Renamed Chat", runModel.commandView.Chats[1].Title)
	require.Equal(t, "enter to open · r to rename · d to archive · esc to close", runModel.commandView.TitleRight)
	_ = cmd
}

func TestModel_UpdateChatsCommandRenamesCurrentSessionTitle(t *testing.T) {
	client := &fakeTUIChatClient{renamedSession: storage.Session{
		ID:          "ses_current",
		Title:       "Manual Current",
		TitleSource: storage.SessionTitleSourceManual,
	}}
	runModel := newModelWithClient(client)
	runModel.applyAction(setSessionAction{ID: "ses_current", Title: "Current Chat"})
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Chats",
		Kind:      commandViewKindChats,
		Chats:     []storage.Session{{ID: "ses_current", Title: "Current Chat"}},
	})

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'r'}))
	runModel = updated.(model)
	runModel.renameInput.SetValue("Manual Current")
	require.NotNil(t, cmd)

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	runModel = updated.(model)
	msg := chatRenamedMessageFromBatch(t, cmd)
	updated, cmd = runModel.Update(msg)
	runModel = updated.(model)

	require.Equal(t, "Manual Current", runModel.sessionTitle)
	require.Equal(t, "Manual Current", runModel.commandView.Chats[0].Title)
	_ = cmd
}

func TestModel_UpdateChatsCommandCancelsRename(t *testing.T) {
	runModel := newModelWithClient(&fakeTUIChatClient{})
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Chats",
		Kind:      commandViewKindChats,
		Chats:     []storage.Session{{ID: "ses_other", Title: "Other Chat"}},
	})

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'r'}))
	require.NotNil(t, cmd)
	runModel = updated.(model)

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	runModel = updated.(model)

	require.True(t, runModel.isCommandViewVisible())
	require.False(t, runModel.chatsRenaming)
	require.Equal(t, "chat rename cancelled", runModel.status.Text())
	require.Equal(t, "enter to open · r to rename · d to archive · esc to close", runModel.commandView.TitleRight)
	_ = cmd
}

func TestModel_UpdateChatsCommandEditsRenameInput(t *testing.T) {
	runModel := newModelWithClient(&fakeTUIChatClient{})
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Chats",
		Kind:      commandViewKindChats,
		Chats:     []storage.Session{{ID: "ses_other", Title: "Other"}},
	})

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'r'}))
	runModel = updated.(model)
	require.NotNil(t, cmd)

	runModel.renameInput.SetValue("")
	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'N', Text: "N"}))
	runModel = updated.(model)

	require.Equal(t, "N", runModel.renameInput.Value())
	require.True(t, runModel.chatsRenaming)
	_ = cmd
}

func TestModel_UpdateChatsCommandHandlesRenameValidationFailures(t *testing.T) {
	runModel := newModel()
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Chats",
		Kind:      commandViewKindChats,
		Chats:     []storage.Session{{Title: "Missing ID"}},
	})

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'r'}))
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.chatsRenaming)
	require.Equal(t, "chat rename unavailable", runModel.status.Text())

	runModel = newModelWithClient(&fakeTUIChatClient{})
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Chats",
		Kind:      commandViewKindChats,
		Chats:     []storage.Session{{ID: "ses_other", Title: "Other"}},
	})
	runModel.chatsRenaming = true
	runModel.chatsRenameSessionID = "ses_other"
	runModel.commandView.TitleRight = getChatsRenameTitleRight()
	runModel.renameInput.SetValue(" ")

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	runModel = updated.(model)
	require.NotNil(t, cmd)
	require.True(t, runModel.chatsRenaming)
	require.Equal(t, "chat rename unavailable", runModel.status.Text())

	runModel = newModel()
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Chats",
		Kind:      commandViewKindChats,
		Chats:     []storage.Session{{ID: "ses_other", Title: "Other"}},
	})
	runModel.chatsRenaming = true
	runModel.chatsRenameSessionID = "ses_other"
	runModel.renameInput.SetValue("New Title")

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	runModel = updated.(model)
	require.NotNil(t, cmd)
	require.Equal(t, "chat rename unavailable", runModel.status.Text())

	runModel = newModelWithClient(&fakeTUIChatClient{})
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Chats",
		Kind:      commandViewKindChats,
		Chats:     []storage.Session{{ID: "ses_other", Title: "Other"}},
	})
	runModel.chatsRenaming = true
	runModel.renameInput.SetValue("New Title")

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	runModel = updated.(model)
	require.NotNil(t, cmd)
	require.Equal(t, "chat rename unavailable", runModel.status.Text())
}

func TestModel_UpdateChatsCommandKeepsRenameOpenOnRenameFailure(t *testing.T) {
	expected := errors.New("rename failed")
	client := &fakeTUIChatClient{renameSessionErr: expected}
	runModel := newModelWithClient(client)
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Chats",
		Kind:      commandViewKindChats,
		Chats:     []storage.Session{{ID: "ses_other", Title: "Other"}},
	})

	updated, _ := runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'r'}))
	runModel = updated.(model)
	runModel.renameInput.SetValue("Renamed")
	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	runModel = updated.(model)
	msg := chatRenamedMessageFromBatch(t, cmd)
	require.ErrorIs(t, msg.Err, expected)

	updated, _ = runModel.Update(msg)
	runModel = updated.(model)

	require.True(t, runModel.chatsRenaming)
	require.Equal(t, "Renamed", runModel.renameInput.Value())
	require.Equal(t, "chat rename unavailable", runModel.status.Text())
	require.Equal(t, "Other", runModel.commandView.Chats[0].Title)
}

func TestModel_UpdateChatsCommandRejectsMalformedRenameCompletion(t *testing.T) {
	runModel := newModelWithClient(&fakeTUIChatClient{})
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Chats",
		Kind:      commandViewKindChats,
		Chats:     []storage.Session{{ID: "ses_other", Title: "Other"}},
	})

	updated, cmd := runModel.Update(chatRenamedMsg{Session: storage.Session{ID: " "}})
	runModel = updated.(model)

	require.Equal(t, "chat rename unavailable", runModel.status.Text())
	require.Equal(t, "Other", runModel.commandView.Chats[0].Title)
	_ = cmd
}

func TestModel_UpdateChatsCommandBlocksArchivingCurrentSession(t *testing.T) {
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	client := &fakeTUIChatClient{}
	runModel := newModelWithClient(client)
	runModel.applyAction(setSessionAction{ID: "ses_current", Title: "Current Chat"})
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Chats",
		Kind:      commandViewKindChats,
		Chats:     []storage.Session{{ID: "ses_current", Title: "Current Chat", UpdatedAt: now}},
	})

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'd'}))
	require.NotNil(t, cmd)
	runModel = updated.(model)

	require.False(t, runModel.chatsArchiveConfirm)
	require.Equal(t, "current chat cannot be archived", runModel.status.Text())
	require.Zero(t, client.archiveSessionCalls)

	runModel.chatsArchiveConfirm = true
	runModel.commandView.TitleRight = getChatsCommandTitleRight(true)
	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	runModel = updated.(model)

	require.False(t, runModel.chatsArchiveConfirm)
	require.Equal(t, "enter to open · r to rename · d to archive · esc to close", runModel.commandView.TitleRight)
	require.Equal(t, "current chat cannot be archived", runModel.status.Text())
	require.Zero(t, client.archiveSessionCalls)
}

func TestModel_UpdateChatsCommandHandlesArchiveValidationFailures(t *testing.T) {
	runModel := newModel()
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Chats",
		Kind:      commandViewKindChats,
		Chats:     []storage.Session{{Title: "Missing ID"}},
	})

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'd'}))
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.chatsArchiveConfirm)
	require.Equal(t, "chat archive unavailable", runModel.status.Text())

	runModel.chatsArchiveConfirm = true
	runModel.commandView.TitleRight = getChatsCommandTitleRight(true)
	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.chatsArchiveConfirm)
	require.Equal(t, "enter to open · r to rename · d to archive · esc to close", runModel.commandView.TitleRight)
	require.Equal(t, "chat archive unavailable", runModel.status.Text())

	runModel = newModel()
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Chats",
		Kind:      commandViewKindChats,
		Chats:     []storage.Session{{ID: "ses_other", Title: "Other"}},
	})
	runModel.chatsArchiveConfirm = true
	runModel.commandView.TitleRight = getChatsCommandTitleRight(true)

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.chatsArchiveConfirm)
	require.Equal(t, "enter to open · r to rename · d to archive · esc to close", runModel.commandView.TitleRight)
	require.Equal(t, "chat archive unavailable", runModel.status.Text())
}

func TestModel_UpdateChatsCommandRemovesLastArchivedSession(t *testing.T) {
	runModel := newModelWithClient(&fakeTUIChatClient{})
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Chats",
		Kind:      commandViewKindChats,
		Chats:     []storage.Session{{ID: "ses_other", Title: "Other"}},
	})
	runModel.commandViewItemSelected = 0
	runModel.commandViewOffset = 4

	updated, cmd := runModel.Update(chatArchivedMsg{ID: "ses_other"})
	runModel = updated.(model)

	require.Equal(t, "chat archived", runModel.status.Text())
	require.Empty(t, runModel.commandView.Chats)
	require.Zero(t, runModel.commandViewItemSelected)
	require.Zero(t, runModel.commandViewOffset)
	_ = cmd
}

func TestModel_ChatMutationShrinksCommandView(t *testing.T) {
	tests := []struct {
		name   string
		kind   string
		update tea.Msg
	}{
		{
			name:   "archive",
			kind:   commandViewKindChats,
			update: chatArchivedMsg{ID: "ses_two"},
		},
		{
			name:   "restore",
			kind:   commandViewKindArchive,
			update: chatUnarchivedMsg{Session: storage.Session{ID: "ses_two"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runModel := newModelWithClient(&fakeTUIChatClient{})
			runModel.width = 80
			runModel.height = 20
			runModel.showCommandView(commandViewPayload{
				Kind: test.kind,
				Chats: []storage.Session{
					{ID: "ses_one", Title: "One"},
					{ID: "ses_two", Title: "Two", Archived: test.kind == commandViewKindArchive},
				},
			})
			commandHeight := runModel.getCommandViewHeight()
			transcriptHeight := runModel.transcript.Height()

			updated, _ := runModel.Update(test.update)
			runModel = updated.(model)

			require.Len(t, runModel.commandView.Chats, 1)
			require.Less(t, runModel.getCommandViewHeight(), commandHeight)
			require.Greater(t, runModel.transcript.Height(), transcriptHeight)
		})
	}
}

func TestModel_UpdateChatsCommandRejectsMalformedArchiveCompletion(t *testing.T) {
	runModel := newModelWithClient(&fakeTUIChatClient{})
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Chats",
		Kind:      commandViewKindChats,
		Chats:     []storage.Session{{ID: "ses_other", Title: "Other"}},
	})

	updated, cmd := runModel.Update(chatArchivedMsg{ID: " "})
	runModel = updated.(model)

	require.Equal(t, "chat archive unavailable", runModel.status.Text())
	require.Len(t, runModel.commandView.Chats, 1)
	require.Equal(t, "ses_other", runModel.commandView.Chats[0].ID)
	_ = cmd
}

func TestArchiveChatSessionCmdHandlesValidationAndNilClient(t *testing.T) {
	require.Nil(t, archiveChatSessionCmd(context.Background(), nil, "ses_other"))

	msg := archiveChatSessionCmd(context.Background(), &fakeTUIChatClient{}, " ")()
	archived, ok := msg.(chatArchivedMsg)
	require.True(t, ok)
	require.EqualError(t, archived.Err, "chat id is required")
	require.Empty(t, archived.ID)

	client := &fakeTUIChatClient{}
	var nilContext context.Context
	msg = archiveChatSessionCmd(nilContext, client, " ses_other ")()
	archived, ok = msg.(chatArchivedMsg)
	require.True(t, ok)
	require.NoError(t, archived.Err)
	require.Equal(t, "ses_other", archived.ID)
	require.Equal(t, "ses_other", client.archivedSessionID)
	require.Equal(t, 1, client.archiveSessionCalls)
}

func TestRenameChatSessionCmdHandlesValidationAndNilClient(t *testing.T) {
	require.Nil(t, renameChatSessionCmd(context.Background(), nil, "ses_other", "Title"))

	msg := renameChatSessionCmd(context.Background(), &fakeTUIChatClient{}, " ", "Title")()
	renamed, ok := msg.(chatRenamedMsg)
	require.True(t, ok)
	require.EqualError(t, renamed.Err, "chat id is required")

	msg = renameChatSessionCmd(context.Background(), &fakeTUIChatClient{}, "ses_other", " ")()
	renamed, ok = msg.(chatRenamedMsg)
	require.True(t, ok)
	require.EqualError(t, renamed.Err, "chat title is required")

	client := &fakeTUIChatClient{}
	var nilContext context.Context
	msg = renameChatSessionCmd(nilContext, client, " ses_other ", " New Title ")()
	renamed, ok = msg.(chatRenamedMsg)
	require.True(t, ok)
	require.NoError(t, renamed.Err)
	require.Equal(t, "ses_other", renamed.Session.ID)
	require.Equal(t, "New Title", renamed.Session.Title)
	require.Equal(t, "ses_other", client.renamedSessionID)
	require.Equal(t, "New Title", client.renamedSessionTitle)
	require.Equal(t, 1, client.renameSessionCalls)
}

func TestModel_UpdateChatsCommandCancelsActiveResponseBeforeSwitching(t *testing.T) {
	cancelled := atomic.Bool{}
	runModel := newModelWithClient(&fakeTUIChatClient{
		timeline: rpcclient.SessionTimeline{SessionID: "ses_other"},
	})
	runModel.responseCancel = func() { cancelled.Store(true) }
	runModel.responding = true
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Chats",
		Kind:      commandViewKindChats,
		Chats:     []storage.Session{{ID: "ses_other", Title: "Other"}},
	})

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	runModel = updated.(model)

	require.True(t, cancelled.Load())
	require.False(t, runModel.responding)
	require.True(t, runModel.chatSwitching)
}

func TestModel_UpdateChatsCommandSelectingCurrentSessionClosesView(t *testing.T) {
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	client := &fakeTUIChatClient{}
	runModel := newModelWithClient(client)
	runModel.applyAction(setSessionAction{ID: "ses_current", Title: "Current Chat"})
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Chats",
		Kind:      commandViewKindChats,
		Chats: []storage.Session{
			{ID: "ses_current", Title: "Current Chat", UpdatedAt: now},
			{ID: "ses_other", Title: "Other Chat", UpdatedAt: now},
		},
	})

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.Nil(t, cmd)
	runModel = updated.(model)

	require.False(t, runModel.isCommandViewVisible())
	require.Zero(t, client.useSessionCalls)
	require.Zero(t, client.timelineCalls)
}

func TestSwitchChatSessionCmdHandlesFailures(t *testing.T) {
	require.Nil(t, switchChatSessionCmd(context.Background(), nil, "ses_1"))

	var nilContext context.Context
	msg := switchChatSessionCmd(nilContext, &fakeTUIChatClient{}, " ")()
	failed, ok := msg.(sessionTimelineLoadFailedMsg)
	require.True(t, ok)
	require.EqualError(t, failed.Err, "chat id is required")

	expected := errors.New("use failed")
	useClient := &fakeTUIChatClient{useSessionErr: expected}
	msg = switchChatSessionCmd(context.Background(), useClient, "ses_1")()
	failed, ok = msg.(sessionTimelineLoadFailedMsg)
	require.True(t, ok)
	require.ErrorIs(t, failed.Err, expected)
	require.Equal(t, 1, useClient.useSessionCalls)
	require.Equal(t, "ses_1", useClient.usedSessionID)
	require.Zero(t, useClient.timelineCalls)

	expected = errors.New("timeline failed")
	timelineClient := &fakeTUIChatClient{timelineErr: expected}
	msg = switchChatSessionCmd(context.Background(), timelineClient, "ses_1")()
	failed, ok = msg.(sessionTimelineLoadFailedMsg)
	require.True(t, ok)
	require.ErrorIs(t, failed.Err, expected)
	require.Equal(t, 1, timelineClient.useSessionCalls)
	require.Equal(t, "ses_1", timelineClient.usedSessionID)
	require.Equal(t, 1, timelineClient.timelineCalls)
	require.Equal(t, "ses_1", timelineClient.timelineSessionID)
}

func TestRenderSelectedChatsCommandRowFillsWidth(t *testing.T) {
	source := " Current Chat              1m ago "
	row := renderSelectedChatsCommandRow(source, 34)

	require.Equal(t, 34, lipgloss.Width(row))
	require.Contains(t, row, "48;")
	require.Contains(t, stripANSI(row), " Current Chat              1m ago ")

	fallback := renderSelectedChatsCommandRowWithForeground("Current Chat", 16, " ")
	explicit := renderSelectedChatsCommandRowWithForeground(
		"Current Chat",
		16,
		defaultTUITheme.JumpToBottomForeground,
	)
	require.Equal(t, explicit, fallback)
}

func TestRenderChatsCommandViewContentMutesUnselectedRowsAndUsesSelectedForeground(t *testing.T) {
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	originalNow := chatsNow
	chatsNow = func() time.Time { return now }
	t.Cleanup(func() {
		chatsNow = originalNow
	})

	runModel := newModel()
	runModel.applyAction(setSessionAction{ID: "ses_current", Title: "Current Chat"})
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Chats",
		Kind:      commandViewKindChats,
		Chats: []storage.Session{
			{ID: "ses_current", Title: "Current Chat", UpdatedAt: now},
			{ID: "ses_other", Title: "Other Chat", UpdatedAt: now},
		},
	})

	rendered := runModel.renderChatsCommandViewContent(commandViewContent{Width: 34, Height: 2})
	plain := stripANSI(rendered)
	currentRow := renderChatsCommandRow(
		storage.Session{ID: "ses_current", Title: "Current Chat", UpdatedAt: now},
		runModel.getCurrentSessionID(),
		34,
		now,
	)
	otherRow := renderChatsCommandRow(
		storage.Session{ID: "ses_other", Title: "Other Chat", UpdatedAt: now},
		runModel.getCurrentSessionID(),
		34,
		now,
	)
	expectedSelectedCurrent := renderSelectedChatsCommandRowWithForeground(
		currentRow,
		34,
		defaultTUITheme.JumpToBottomForeground,
	)
	expectedMutedOther := lipgloss.NewStyle().
		Foreground(lipgloss.Color(defaultTUITheme.MutedText)).
		Render(otherRow)

	require.Equal(t, defaultTUITheme.JumpToBottomForeground, getChatsCommandRowForeground(false))
	require.Equal(t, defaultTUITheme.JumpToBottomForeground, getChatsCommandRowForeground(true))
	require.Contains(t, rendered, expectedSelectedCurrent)
	require.Contains(t, rendered, expectedMutedOther)
	require.Contains(t, plain, "Current Chat")
	require.Contains(t, plain, "Other Chat")
	require.NotContains(t, plain, "current Current Chat")

	runModel.commandViewItemSelected = 1
	rendered = runModel.renderChatsCommandViewContent(commandViewContent{Width: 34, Height: 2})
	expectedSelectedOther := renderSelectedChatsCommandRowWithForeground(
		otherRow,
		34,
		defaultTUITheme.JumpToBottomForeground,
	)

	require.Contains(t, rendered, expectedSelectedOther)
}

func TestModel_UpdateChatsCommandHandlesErrors(t *testing.T) {
	expected := errors.New("list failed")
	runModel := newModelWithClient(&fakeTUIChatClient{listSessionsErr: expected})
	runModel.input.SetValue("/chats")

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	runModel = updated.(model)

	msg := chatsLoadedMessageFromBatch(t, cmd)
	updated, cmd = runModel.Update(msg)
	require.NotNil(t, cmd)
	runModel = updated.(model)

	require.False(t, runModel.isCommandViewVisible())
	require.Equal(t, "chats unavailable", runModel.status.Text())
}

func TestModel_UpdateChatsCommandRequiresSessionClient(t *testing.T) {
	runModel := newModel()
	runModel.input.SetValue("/chats")

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	runModel = updated.(model)

	require.False(t, runModel.isCommandViewVisible())
	require.Equal(t, "chats unavailable", runModel.status.Text())
}

func TestModel_UpdateArchiveCommandHandlesErrors(t *testing.T) {
	expected := errors.New("list failed")
	runModel := newModelWithClient(&fakeTUIChatClient{listArchivedErr: expected})
	runModel.input.SetValue("/archive")

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	runModel = updated.(model)

	msg := archivedChatsLoadedMessageFromBatch(t, cmd)
	updated, cmd = runModel.Update(msg)
	require.NotNil(t, cmd)
	runModel = updated.(model)

	require.False(t, runModel.isCommandViewVisible())
	require.Equal(t, "archive unavailable", runModel.status.Text())
}

func TestModel_UpdateArchiveCommandRequiresSessionClient(t *testing.T) {
	runModel := newModel()
	runModel.input.SetValue("/archive")

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	runModel = updated.(model)

	require.False(t, runModel.isCommandViewVisible())
	require.Equal(t, "archive unavailable", runModel.status.Text())
}

func TestModel_UpdateArchiveCommandRestoresSelectedSession(t *testing.T) {
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	client := &fakeTUIChatClient{
		unarchivedSession: storage.Session{ID: "ses_archived", Title: "Archived chat"},
	}
	runModel := newModelWithClient(client)
	runModel.showCommandView(commandViewPayload{
		TitleLeft:  "Archive",
		TitleRight: getArchiveCommandTitleRight(),
		Kind:       commandViewKindArchive,
		Chats: []storage.Session{
			{ID: "ses_archived", Title: "Archived chat", Archived: true, UpdatedAt: now},
			{ID: "ses_other", Title: "Other archived chat", Archived: true, UpdatedAt: now},
		},
	})

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	runModel = updated.(model)

	msg := chatUnarchivedMessageFromBatch(t, cmd)
	require.NoError(t, msg.Err)
	require.Equal(t, "ses_archived", client.unarchivedSessionID)
	require.Equal(t, 1, client.unarchiveCalls)

	updated, cmd = runModel.Update(msg)
	require.NotNil(t, cmd)
	runModel = updated.(model)

	require.Equal(t, "chat restored", runModel.status.Text())
	require.Len(t, runModel.commandView.Chats, 1)
	require.Equal(t, "ses_other", runModel.commandView.Chats[0].ID)
	require.Zero(t, runModel.commandViewItemSelected)
}

func TestModel_UpdateArchiveCommandMovesSelectionAndClearsSelection(t *testing.T) {
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	runModel := newModelWithClient(&fakeTUIChatClient{})
	runModel.height = 12
	runModel.commandViewSelection.active = true
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Archive",
		Kind:      commandViewKindArchive,
		Chats: []storage.Session{
			{ID: "ses_1", Title: "One", Archived: true, UpdatedAt: now},
			{ID: "ses_2", Title: "Two", Archived: true, UpdatedAt: now},
			{ID: "ses_3", Title: "Three", Archived: true, UpdatedAt: now},
		},
	})

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 1, runModel.commandViewItemSelected)
	require.False(t, runModel.commandViewSelection.active)

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnd}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 2, runModel.commandViewItemSelected)

	updated, cmd = runModel.Update(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelUp}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 1, runModel.commandViewItemSelected)
}

func TestModel_UpdateArchiveCommandCoversNavigationEdges(t *testing.T) {
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	runModel := newModelWithClient(&fakeTUIChatClient{})
	runModel.height = 8
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Archive",
		Kind:      commandViewKindArchive,
		Chats: []storage.Session{
			{ID: "ses_1", Title: "One", Archived: true, UpdatedAt: now},
			{ID: "ses_2", Title: "Two", Archived: true, UpdatedAt: now},
			{ID: "ses_3", Title: "Three", Archived: true, UpdatedAt: now},
			{ID: "ses_4", Title: "Four", Archived: true, UpdatedAt: now},
		},
	})
	runModel.commandViewItemSelected = 2

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyHome}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Zero(t, runModel.commandViewItemSelected)

	runModel.commandViewItemSelected = 1
	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Zero(t, runModel.commandViewItemSelected)

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Greater(t, runModel.commandViewItemSelected, 0)

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Zero(t, runModel.commandViewItemSelected)

	updated, cmd = runModel.Update(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelDown}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 1, runModel.commandViewItemSelected)

	updated, cmd = runModel.Update(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseLeft}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 1, runModel.commandViewItemSelected)

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'x'}))
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 1, runModel.commandViewItemSelected)

	updated, cmd = runModel.updateArchiveCommandView(statusExpiredMsg{})
	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 1, runModel.commandViewItemSelected)
}

func TestModel_UpdateArchiveCommandIgnoresInputWhenEmpty(t *testing.T) {
	runModel := newModelWithClient(&fakeTUIChatClient{})
	runModel.showCommandView(commandViewPayload{TitleLeft: "Archive", Kind: commandViewKindArchive})

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.Nil(t, cmd)
	runModel = updated.(model)

	require.True(t, runModel.isCommandViewVisible())
	require.Empty(t, runModel.commandView.Chats)
}

func TestModel_UpdateArchiveCommandRestoreHandlesFailures(t *testing.T) {
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	runModel := newModelWithClient(&fakeTUIChatClient{unarchiveSessionErr: errors.New("restore failed")})
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Archive",
		Kind:      commandViewKindArchive,
		Chats: []storage.Session{
			{ID: "ses_archived", Title: "Archived chat", Archived: true, UpdatedAt: now},
		},
	})

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: 'u'}))
	require.NotNil(t, cmd)
	runModel = updated.(model)

	msg := chatUnarchivedMessageFromBatch(t, cmd)
	updated, cmd = runModel.Update(msg)
	require.NotNil(t, cmd)
	runModel = updated.(model)

	require.Equal(t, "chat restore unavailable", runModel.status.Text())
	require.Len(t, runModel.commandView.Chats, 1)
}

func TestModel_UpdateArchiveCommandRestoreHandlesValidationFailures(t *testing.T) {
	runModel := newModel()
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Archive",
		Kind:      commandViewKindArchive,
		Chats:     []storage.Session{{Title: "Missing ID", Archived: true}},
	})

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, "chat restore unavailable", runModel.status.Text())

	runModel = newModel()
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Archive",
		Kind:      commandViewKindArchive,
		Chats:     []storage.Session{{ID: "ses_archived", Title: "Archived", Archived: true}},
	})

	updated, cmd = runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, "chat restore unavailable", runModel.status.Text())
}

func TestModel_UpdateArchiveCommandRemovesLastRestoredSession(t *testing.T) {
	runModel := newModelWithClient(&fakeTUIChatClient{})
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Archive",
		Kind:      commandViewKindArchive,
		Chats:     []storage.Session{{ID: "ses_archived", Title: "Archived", Archived: true}},
	})
	runModel.commandViewOffset = 4

	updated, cmd := runModel.Update(chatUnarchivedMsg{Session: storage.Session{ID: "ses_archived"}})
	runModel = updated.(model)

	require.Equal(t, "chat restored", runModel.status.Text())
	require.Empty(t, runModel.commandView.Chats)
	require.Zero(t, runModel.commandViewItemSelected)
	require.Zero(t, runModel.commandViewOffset)
	_ = cmd
}

func TestModel_UpdateArchiveCommandRejectsMalformedRestoreCompletion(t *testing.T) {
	runModel := newModelWithClient(&fakeTUIChatClient{})
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Archive",
		Kind:      commandViewKindArchive,
		Chats:     []storage.Session{{ID: "ses_archived", Title: "Archived", Archived: true}},
	})

	updated, cmd := runModel.Update(chatUnarchivedMsg{Session: storage.Session{ID: " "}})
	runModel = updated.(model)

	require.Equal(t, "chat restore unavailable", runModel.status.Text())
	require.Len(t, runModel.commandView.Chats, 1)
	require.Equal(t, "ses_archived", runModel.commandView.Chats[0].ID)
	_ = cmd
}

func TestUnarchiveChatSessionCmdHandlesValidationAndNilClient(t *testing.T) {
	require.Nil(t, unarchiveChatSessionCmd(context.Background(), nil, "ses_archived"))

	msg := unarchiveChatSessionCmd(context.Background(), &fakeTUIChatClient{}, " ")()
	unarchived, ok := msg.(chatUnarchivedMsg)
	require.True(t, ok)
	require.EqualError(t, unarchived.Err, "chat id is required")
	require.Empty(t, unarchived.Session.ID)

	client := &fakeTUIChatClient{}
	var nilContext context.Context
	msg = unarchiveChatSessionCmd(nilContext, client, " ses_archived ")()
	unarchived, ok = msg.(chatUnarchivedMsg)
	require.True(t, ok)
	require.NoError(t, unarchived.Err)
	require.Equal(t, "ses_archived", unarchived.Session.ID)
	require.Equal(t, "ses_archived", client.unarchivedSessionID)
	require.Equal(t, 1, client.unarchiveCalls)
}

func TestRenderChatsCommandContentAlignsActivityColumn(t *testing.T) {
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	content := renderChatsCommandContent([]storage.Session{
		{ID: "ses_current", Title: "Current Chat", UpdatedAt: now.Add(-time.Minute)},
		{ID: "ses_old", Title: "Older Chat", UpdatedAt: now.Add(-48 * time.Hour)},
	}, "ses_current", 34, now)

	lines := strings.Split(content, "\n")
	require.Len(t, lines, 2)
	require.Equal(t, 34, lipgloss.Width(lines[0]))
	require.Equal(t, 34, lipgloss.Width(lines[1]))
	require.True(t, strings.HasPrefix(lines[0], " Current Chat"))
	require.True(t, strings.HasSuffix(lines[0], "1m ago "))
	require.True(t, strings.HasPrefix(lines[1], " Older Chat"))
	require.True(t, strings.HasSuffix(lines[1], "2d ago "))
}

func TestRenderChatsCommandContentHandlesEmptyAndNarrowRows(t *testing.T) {
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

	require.Equal(t, "No chats yet.", renderChatsCommandContent(nil, "", 34, now))
	require.Empty(t, renderChatsCommandRow(storage.Session{Title: "Very long title", UpdatedAt: now}, "", 1, now))
	require.Equal(t, "Very long title", truncateChatsCommandRow("Very long title", 0))
	require.Equal(t, "Very…", truncateChatsCommandRow("Very long title", 5))
}

func TestRenderChatsCommandViewContentHandlesEmptyAndCurrentUnselected(t *testing.T) {
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	originalNow := chatsNow
	chatsNow = func() time.Time { return now }
	t.Cleanup(func() {
		chatsNow = originalNow
	})

	runModel := newModel()
	require.Equal(t, "No chats yet.", runModel.renderChatsCommandViewContent(commandViewContent{}))
	runModel.showCommandView(commandViewPayload{Kind: commandViewKindArchive})
	require.Equal(t, "No archived chats.", runModel.renderSessionListCommandViewContent(commandViewContent{}))

	runModel.applyAction(setSessionAction{ID: "ses_current", Title: "Current Chat"})
	runModel.showCommandView(commandViewPayload{
		TitleLeft: "Chats",
		Kind:      commandViewKindChats,
		Chats: []storage.Session{
			{ID: "ses_current", Title: "Current Chat", UpdatedAt: now},
			{ID: "ses_other", Title: "Other Chat", UpdatedAt: now},
		},
	})
	runModel.commandViewItemSelected = 1

	content := stripANSI(runModel.renderChatsCommandViewContent(commandViewContent{Width: 34, Height: 3}))

	require.Contains(t, content, "Current Chat")
	require.Contains(t, content, "Other Chat")
}

func TestRenderChatsCommandContentMovesCurrentSessionToTop(t *testing.T) {
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	content := renderChatsCommandContent([]storage.Session{
		{ID: "ses_old", Title: "Older Chat", UpdatedAt: now.Add(-48 * time.Hour)},
		{ID: "ses_current", Title: "Current Chat", UpdatedAt: now.Add(-time.Minute)},
		{ID: "ses_new", Title: "Newer Chat", UpdatedAt: now},
	}, "ses_current", 34, now)

	lines := strings.Split(content, "\n")

	require.Len(t, lines, 3)
	require.Contains(t, lines[0], "Current Chat")
	require.Contains(t, lines[1], "Older Chat")
	require.Contains(t, lines[2], "Newer Chat")
	require.NotContains(t, content, "current Current Chat")
}

func TestOrderChatsCommandSessionsReturnsCopyForKnownEdgeCases(t *testing.T) {
	sessions := []storage.Session{
		{ID: "ses_current", Title: "Current"},
		{ID: "ses_other", Title: "Other"},
	}

	blank := orderChatsCommandSessions(sessions, "")
	missing := orderChatsCommandSessions(sessions, "missing")
	first := orderChatsCommandSessions(sessions, "ses_current")

	blank[0].Title = "mutated"
	require.Equal(t, "Current", sessions[0].Title)
	require.Equal(t, []storage.Session{{ID: "ses_current", Title: "Current"}, {ID: "ses_other", Title: "Other"}}, missing)
	require.Equal(t, []storage.Session{{ID: "ses_current", Title: "Current"}, {ID: "ses_other", Title: "Other"}}, first)
}

func TestGetChatsCommandViewOffsetForSelectionHandlesAboveViewport(t *testing.T) {
	require.Equal(t, 1, getChatsCommandViewOffsetForSelection(1, 3, 2, 6))
}

func TestFormatChatSessionActivity(t *testing.T) {
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

	require.Equal(t, "unknown", formatChatSessionActivity(time.Time{}, now))
	require.Equal(t, "just now", formatChatSessionActivity(now.Add(-30*time.Second), now))
	require.Equal(t, "5m ago", formatChatSessionActivity(now.Add(-5*time.Minute), now))
	require.Equal(t, "3h ago", formatChatSessionActivity(now.Add(-3*time.Hour), now))
	require.Equal(t, "4d ago", formatChatSessionActivity(now.Add(-4*24*time.Hour), now))
	require.Equal(t, "2mo ago", formatChatSessionActivity(now.Add(-65*24*time.Hour), now))
	require.Equal(t, "1y ago", formatChatSessionActivity(now.Add(-400*24*time.Hour), now))
	require.Equal(t, "just now", formatChatSessionActivity(now.Add(time.Hour), now))
	require.Equal(t, "just now", formatChatSessionActivity(chatsNow(), time.Time{}))
}

func chatsLoadedMessageFromBatch(t *testing.T, cmd tea.Cmd) chatsLoadedMsg {
	t.Helper()

	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	require.Len(t, batch, 2)

	msg, ok := batch[1]().(chatsLoadedMsg)
	require.True(t, ok)

	return msg
}

func archivedChatsLoadedMessageFromBatch(t *testing.T, cmd tea.Cmd) archivedChatsLoadedMsg {
	t.Helper()

	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	require.Len(t, batch, 2)

	msg, ok := batch[1]().(archivedChatsLoadedMsg)
	require.True(t, ok)

	return msg
}

func chatUnarchivedMessageFromBatch(t *testing.T, cmd tea.Cmd) chatUnarchivedMsg {
	t.Helper()

	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	require.Len(t, batch, 2)

	msg, ok := batch[1]().(chatUnarchivedMsg)
	require.True(t, ok)

	return msg
}

func chatSwitchTimelineMessageFromBatch(t *testing.T, cmd tea.Cmd) sessionTimelineLoadedMsg {
	t.Helper()

	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	require.Len(t, batch, 2)

	msg, ok := batch[1]().(sessionTimelineLoadedMsg)
	require.True(t, ok)

	return msg
}

func chatArchivedMessageFromBatch(t *testing.T, cmd tea.Cmd) chatArchivedMsg {
	t.Helper()

	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	require.Len(t, batch, 2)

	for _, batchCmd := range batch {
		msg, ok := batchCmd().(chatArchivedMsg)
		if ok {
			return msg
		}
	}

	require.Fail(t, "archive completion message not found")
	return chatArchivedMsg{}
}

func chatRenamedMessageFromBatch(t *testing.T, cmd tea.Cmd) chatRenamedMsg {
	t.Helper()

	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	require.Len(t, batch, 2)

	msg, ok := batch[1]().(chatRenamedMsg)
	require.True(t, ok)

	return msg
}
