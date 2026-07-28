package tui

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	rpcclient "github.com/wandxy/morph/internal/rpc/client"
	storage "github.com/wandxy/morph/internal/state/core"
)

func TestModel_UpdateHandlesCompactCommand(t *testing.T) {
	client := &fakeTUIChatClient{
		compactResult: rpcclient.CompactSessionResult{
			SessionID:          "default",
			SourceMessageCount: 12,
		},
	}
	runModel := newModelWithClient(client)
	runModel.input.SetValue("/compact")

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, "compaction started", runModel.status.Text())
	require.True(t, runModel.manualCompactionActive)
	require.Empty(t, runModel.input.Value())
	require.Equal(t, []string{"Manual compaction started"}, transcriptCellPlainTexts(runModel.messages))
	require.Contains(t, stripANSI(runModel.View().Content), "Manual Compaction")

	msg := compactSessionMessageFromBatch(t, cmd)
	updated, cmd = runModel.Update(msg)

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 1, client.compactCalls)
	require.Equal(t, defaultSessionID, client.compactID)
	require.Equal(t, "session compacted", runModel.status.Text())
	require.False(t, runModel.manualCompactionActive)
	require.Equal(t, []string{"Manual compaction completed"}, transcriptCellPlainTexts(runModel.messages))
	require.Contains(t, stripANSI(runModel.View().Content), "Manual Compaction")
}

func TestModel_UpdateDisablesInputDuringCompactCommand(t *testing.T) {
	runModel := newModelWithClient(&fakeTUIChatClient{})
	runModel.input.SetValue("/compact")

	updated, _ := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	runModel = updated.(model)

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Empty(t, runModel.input.Value())
	require.True(t, runModel.manualCompactionActive)
}

func TestModel_UpdateSubmitsSelectedCommandMenuItem(t *testing.T) {
	client := &fakeTUIChatClient{
		compactResult: rpcclient.CompactSessionResult{
			SessionID:          "default",
			SourceMessageCount: 4,
		},
	}
	runModel := newModelWithClient(client)
	runModel.input.SetValue("/")
	runModel.updateCommandMenuForInput("/")
	require.True(t, runModel.scrollCommandMenu(1))
	require.True(t, runModel.scrollCommandMenu(1))
	require.True(t, runModel.scrollCommandMenu(1))

	updated, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, "compaction started", runModel.status.Text())
	require.Empty(t, runModel.input.Value())

	msg := compactSessionMessageFromBatch(t, cmd)
	_, _ = runModel.Update(msg)

	require.Equal(t, 1, client.compactCalls)
	require.Equal(t, defaultSessionID, client.compactID)
}

func TestModel_UpdateHandlesCompactCommandForCurrentSessionID(t *testing.T) {
	client := &fakeTUIChatClient{
		compactResult: rpcclient.CompactSessionResult{
			SessionID:          "project-a",
			SourceMessageCount: 7,
		},
	}
	runModel := newModelWithClient(client)
	runModel.refreshSessionTitleFromSession(storage.Session{ID: "project-a", Title: "Project A"})
	runModel.input.SetValue("/compact")

	_, cmd := runModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	msg := compactSessionMessageFromBatch(t, cmd)
	_, _ = runModel.Update(msg)

	require.Equal(t, 1, client.compactCalls)
	require.Equal(t, "project-a", client.compactID)
}

func TestModel_UpdateReportsCompactCommandFailure(t *testing.T) {
	expectedErr := errors.New("summary failed")
	runModel := newModelWithClient(&fakeTUIChatClient{compactErr: expectedErr})
	runModel.input.SetValue("/compact")

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	require.NotNil(t, cmd)
	runModel = updated.(model)

	msg := compactSessionMessageFromBatch(t, cmd)
	updated, cmd = runModel.Update(msg)

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, "compaction failed", runModel.status.Text())
	require.False(t, runModel.manualCompactionActive)
	require.Equal(t, []string{"Manual compaction failed: summary failed"}, transcriptCellPlainTexts(runModel.messages))
	require.Contains(t, stripANSI(runModel.View().Content), "Failed Manual Compaction")
	require.Contains(t, stripANSI(runModel.View().Content), "└ summary failed")
}

func TestModel_UpdateReportsCompactCommandUnavailable(t *testing.T) {
	runModel := newModel()
	runModel.input.SetValue("/compact")

	updated, cmd := runModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, "compaction unavailable", runModel.status.Text())
}

func compactSessionMessageFromBatch(t *testing.T, cmd tea.Cmd) compactSessionCompletedMsg {
	t.Helper()

	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	require.Len(t, batch, 3)

	msg, ok := batch[2]().(compactSessionCompletedMsg)
	require.True(t, ok)

	return msg
}
