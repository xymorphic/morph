package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	rpcclient "github.com/wandxy/morph/internal/rpc/client"
	agentsession "github.com/wandxy/morph/pkg/agent/session"
)

func TestSlashCommandDefinitionsIncludeEffort(t *testing.T) {
	for _, command := range slashCommandDefinitions {
		if command.Name == "effort" {
			require.Contains(t, command.Description, "reasoning effort")
			return
		}
	}
	t.Fatal("/effort command is not registered")
}

func TestHandleEffortCommandOpensOrderedStatusSelector(t *testing.T) {
	runModel := newReasoningEffortTestModel()
	active := agentsession.ReasoningSnapshot{
		Provider: "openai", API: "responses", Model: "gpt-5.5", Effort: "low",
	}
	runModel.reasoning.SessionOverride = "high"
	runModel.reasoning.EffectiveEffort = "high"
	runModel.reasoning.Source = agentsession.ReasoningResolutionSourceSessionOverride
	runModel.reasoning.Fallback = agentsession.ReasoningFallbackSessionOverrideUnsupported
	runModel.reasoning.DormantEffort = "xhigh"
	runModel.reasoning.ActiveRunSnapshot = &active

	require.Nil(t, runModel.handleEffortCommand(""))
	require.True(t, runModel.isEffortCommandView())
	require.Equal(t, []agentsession.ReasoningEffort{"none", "low", "medium", "high"}, runModel.commandView.Efforts)
	require.Equal(t, "ses_effort", runModel.commandView.EffortSessionID)
	require.Equal(t, runModel.reasoning.Model, runModel.commandView.EffortModel)

	content := stripANSI(runModel.renderEffortCommandViewContent(commandViewContent{
		Width: 160, Height: 8,
	}))
	require.Less(t, strings.Index(content, "none"), strings.Index(content, "low"))
	require.Less(t, strings.Index(content, "low"), strings.Index(content, "medium"))
	require.Contains(t, content, "override")
	require.Contains(t, content, "next turn")
	require.Contains(t, content, "current turn")
	require.Contains(t, content, "fallback session_override_unsupported")
	require.Contains(t, content, "dormant override xhigh")
}

func TestEffortSelectorLabelsDormantProfileDefault(t *testing.T) {
	runModel := newReasoningEffortTestModel()
	runModel.reasoning.ProfileDefault = "xhigh"
	runModel.reasoning.DormantEffort = "xhigh"
	runModel.reasoning.Fallback = agentsession.ReasoningFallbackProfileDefaultUnsupported

	require.Nil(t, runModel.handleEffortCommand(""))
	content := stripANSI(runModel.renderEffortCommandViewContent(commandViewContent{
		Width: 120, Height: 6,
	}))

	require.Contains(t, content, "dormant profile default xhigh")
	require.NotContains(t, content, "dormant override xhigh")
}

func TestHandleEffortCommandSetsCanonicalValueAndResetAliases(t *testing.T) {
	for _, test := range []struct {
		name     string
		argument string
		effort   string
		reset    bool
	}{
		{name: "case insensitive", argument: "HIGH", effort: "high"},
		{name: "default", argument: "default", reset: true},
		{name: "reset", argument: "RESET", reset: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeTUIChatClient{}
			runModel := newReasoningEffortTestModel()
			runModel.sessionClient = client

			msg := effortSetMessageFromCommand(t, runModel.handleEffortCommand(test.argument))

			require.Equal(t, "ses_effort", client.reasoningOptions.SessionID)
			require.Equal(t, "openai", client.reasoningOptions.ExpectedProvider)
			require.Equal(t, "responses", client.reasoningOptions.ExpectedAPI)
			require.Equal(t, "gpt-5.5", client.reasoningOptions.ExpectedModel)
			require.Equal(t, test.effort, client.reasoningOptions.Effort)
			require.Equal(t, test.reset, client.reasoningOptions.Reset)
			require.Equal(t, "ses_effort", msg.SessionID)
		})
	}
}

func TestHandleEffortCommandRejectsUnavailableAndDuplicateRequests(t *testing.T) {
	runModel := newReasoningEffortTestModel()
	client := &fakeTUIChatClient{}
	runModel.sessionClient = client

	require.NotNil(t, runModel.handleEffortCommand("unsupported"))
	require.Zero(t, client.reasoningCalls)

	runModel.effortSavingSessionID = "ses_effort"
	require.NotNil(t, runModel.handleEffortCommand("high"))
	require.Zero(t, client.reasoningCalls)

	runModel.effortSavingSessionID = ""
	runModel.reasoning.Adjustable = false
	require.NotNil(t, runModel.handleEffortCommand("high"))
	require.Zero(t, client.reasoningCalls)

	runModel.reasoning.Reasoning = false
	require.Nil(t, runModel.handleEffortCommand(""))
	require.True(t, runModel.isEffortCommandView())
	require.Contains(
		t,
		runModel.renderEffortCommandViewContent(commandViewContent{Width: 80, Height: 4}),
		"not applicable",
	)

	runModel = newReasoningEffortTestModel()
	require.NotNil(t, runModel.handleEffortCommand("high"))
	require.Empty(t, runModel.effortSavingSessionID)
}

func TestHandleEffortCommandAllowsResetForDormantOverride(t *testing.T) {
	runModel := newReasoningEffortTestModel()
	client := &fakeTUIChatClient{}
	runModel.sessionClient = client
	runModel.reasoning.Reasoning = false
	runModel.reasoning.Adjustable = false
	runModel.reasoning.SessionOverride = "future-level"
	runModel.reasoning.DormantEffort = "future-level"

	msg := effortSetMessageFromCommand(t, runModel.handleEffortCommand("reset"))

	require.True(t, client.reasoningOptions.Reset)
	require.Empty(t, client.reasoningOptions.Effort)
	require.Equal(t, "ses_effort", msg.SessionID)
}

func TestEffortStatusShowsDormantOverrideForUnavailableModel(t *testing.T) {
	runModel := newReasoningEffortTestModel()
	runModel.reasoning.Reasoning = false
	runModel.reasoning.Adjustable = false
	runModel.reasoning.SessionOverride = "high"
	runModel.reasoning.DormantEffort = "high"
	runModel.reasoning.Fallback = agentsession.ReasoningFallbackSessionOverrideUnsupported

	require.Nil(t, runModel.handleEffortCommand(""))
	content := runModel.renderEffortCommandViewContent(commandViewContent{
		Width: 80, Height: 5,
	})

	require.Contains(t, content, "not applicable")
	require.Contains(t, content, "Stored override: high")
	require.Contains(t, content, "/effort reset")
	require.Contains(t, content, "session_override_unsupported")
}

func TestEffortSelectorSupportsKeyboardMouseAndEscape(t *testing.T) {
	runModel := newReasoningEffortTestModel()
	client := &fakeTUIChatClient{}
	runModel.sessionClient = client
	runModel.startEffortCommand()

	updated, _ := runModel.updateEffortCommandView(tea.KeyPressMsg{Code: tea.KeyDown})
	runModel = updated.(model)
	require.Equal(t, 1, runModel.commandViewItemSelected)

	mouse := tea.MouseClickMsg(tea.Mouse{
		X:      runModel.getCommandViewContentLeft(),
		Y:      runModel.getCommandViewContentTop() + 2,
		Button: tea.MouseLeft,
	})
	updated, cmd := runModel.updateEffortCommandView(mouse)
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, 2, runModel.commandViewItemSelected)

	updated, _, handled := runModel.handleKeyPressMsg(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.True(t, handled)
	require.False(t, updated.(model).isCommandViewVisible())
}

func TestEffortSelectorSupportsBoundaryKeysAndWheel(t *testing.T) {
	runModel := newReasoningEffortTestModel()
	runModel.startEffortCommand()

	updated, _ := runModel.updateEffortCommandView(tea.KeyPressMsg{Code: tea.KeyEnd})
	runModel = updated.(model)
	require.Equal(t, len(runModel.commandView.Efforts), runModel.commandViewItemSelected)

	updated, _ = runModel.updateEffortCommandView(tea.MouseWheelMsg(tea.Mouse{
		Button: tea.MouseWheelUp,
	}))
	runModel = updated.(model)
	require.Equal(t, len(runModel.commandView.Efforts)-1, runModel.commandViewItemSelected)

	updated, _ = runModel.updateEffortCommandView(tea.KeyPressMsg{Code: tea.KeyHome})
	runModel = updated.(model)
	require.Zero(t, runModel.commandViewItemSelected)

	runModel.commandView.EffortAdjustable = false
	updated, cmd := runModel.updateEffortCommandView(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Nil(t, cmd)
	require.Zero(t, updated.(model).commandViewItemSelected)
}

func TestCompleteReasoningEffortSetUpdatesLabelAndExplainsActiveRun(t *testing.T) {
	runModel := newReasoningEffortTestModel()
	active := agentsession.ReasoningSnapshot{
		Provider: "openai", API: "responses", Model: "gpt-5.5", Effort: "low",
	}
	settings := runModel.reasoning
	settings.SessionOverride = "high"
	settings.EffectiveEffort = "high"
	settings.ActiveRunSnapshot = &active
	runModel.effortSavingSessionID = "ses_effort"

	cmd := runModel.completeReasoningEffortSet(reasoningEffortSetMsg{
		SessionID: "ses_effort",
		Settings:  settings,
	})

	require.Empty(t, runModel.effortSavingSessionID)
	require.Equal(t, agentsession.ReasoningEffort("high"), runModel.reasoning.EffectiveEffort)
	require.Equal(t, "GPT-5.5 (high)", runModel.getModelLabel())
	require.NotNil(t, cmd)
	require.Contains(t, runModel.status.Text(), "current turn remains low")
	require.Contains(t, runModel.status.Text(), "high applies next turn")
}

func TestCompleteReasoningEffortSetIgnoresSupersededSessionResponse(t *testing.T) {
	runModel := newReasoningEffortTestModel()
	runModel.effortSavingSessionID = "ses_current"

	cmd := runModel.completeReasoningEffortSet(reasoningEffortSetMsg{
		SessionID: "ses_previous",
		Settings:  runModel.reasoning,
	})

	require.Nil(t, cmd)
	require.Equal(t, "ses_current", runModel.effortSavingSessionID)
	require.Empty(t, runModel.reasoning.EffectiveEffort)
}

func TestCompleteReasoningEffortSetDoesNotApplyLateOrStaleResults(t *testing.T) {
	runModel := newReasoningEffortTestModel()
	runModel.effortSavingSessionID = "ses_effort"
	changed := runModel.reasoning
	changed.Model.Model = "gpt-5.6"
	changed.EffectiveEffort = "high"

	cmd := runModel.completeReasoningEffortSet(reasoningEffortSetMsg{
		SessionID: "ses_effort",
		Settings:  changed,
	})
	require.NotNil(t, cmd)
	require.Empty(t, runModel.reasoning.EffectiveEffort)

	runModel = newReasoningEffortTestModel()
	runModel.effortSavingSessionID = "ses_effort"
	cmd = runModel.completeReasoningEffortSet(reasoningEffortSetMsg{
		SessionID: "ses_effort",
		Err:       agentsession.ErrReasoningStaleTuple,
	})
	require.NotNil(t, cmd)
	require.Empty(t, runModel.reasoning.SessionOverride)

	runModel = newReasoningEffortTestModel()
	runModel.effortSavingSessionID = "ses_effort"
	runModel.modelRestartPending = true
	cmd = runModel.completeReasoningEffortSet(reasoningEffortSetMsg{
		SessionID: "ses_effort",
		Settings:  runModel.reasoning,
	})
	require.NotNil(t, cmd)
	require.Empty(t, runModel.reasoning.EffectiveEffort)
}

func TestCompleteReasoningEffortSetHandlesErrorsAndSessionSwitch(t *testing.T) {
	runModel := newReasoningEffortTestModel()
	runModel.effortSavingSessionID = "ses_effort"

	cmd := runModel.completeReasoningEffortSet(reasoningEffortSetMsg{
		SessionID: "ses_effort",
		Err:       errors.New("permission denied"),
	})

	require.NotNil(t, cmd)
	require.Contains(t, runModel.status.Text(), "reasoning effort unchanged")
	require.Empty(t, runModel.effortSavingSessionID)
	require.Empty(t, runModel.reasoning.EffectiveEffort)

	runModel = newReasoningEffortTestModel()
	runModel.effortSavingSessionID = "ses_effort"
	runModel.applyAction(setSessionAction{ID: "ses_other"})

	require.Nil(t, runModel.completeReasoningEffortSet(reasoningEffortSetMsg{
		SessionID: "ses_effort",
		Settings:  runModel.reasoning,
	}))
	require.Empty(t, runModel.effortSavingSessionID)
}

func TestCompleteReasoningEffortSetRefreshesOpenSelector(t *testing.T) {
	runModel := newReasoningEffortTestModel()
	runModel.startEffortCommand()
	runModel.effortSavingSessionID = "ses_effort"
	settings := runModel.reasoning
	settings.SupportedEfforts = []agentsession.ReasoningEffort{"low", "high"}
	settings.SessionOverride = "high"
	settings.EffectiveEffort = "high"
	settings.Source = agentsession.ReasoningResolutionSourceSessionOverride

	cmd := runModel.completeReasoningEffortSet(reasoningEffortSetMsg{
		SessionID: "ses_effort",
		Settings:  settings,
	})

	require.NotNil(t, cmd)
	require.Equal(t, settings.SupportedEfforts, runModel.commandView.Efforts)
	require.Equal(t, 2, runModel.commandViewItemSelected)
	require.Contains(t, runModel.commandView.TitleSubtext, "effective high")
	require.Equal(t, "GPT-5.5 (high)", runModel.getModelLabel())
}

func TestModelLabelIsSharedAndSuppressesMismatchedOrRestartingEffort(t *testing.T) {
	runModel := newReasoningEffortTestModel()
	runModel.reasoning.EffectiveEffort = "high"

	require.Equal(t, "GPT-5.5 (high)", runModel.getModelLabel())
	require.Equal(t, "GPT-5.5 (high)", getBottomStatusPanel(100, runModel).ModelName)
	require.Equal(t, "GPT-5.5 (high)", getHeaderInfoRows(runModel)[6].value)

	runModel.modelRestartPending = true
	require.Equal(t, "GPT-5.5", runModel.getModelLabel())

	runModel.modelRestartPending = false
	runModel.runtimeInfo.Model = "gpt-5.6"
	require.Equal(t, "GPT-5.5", runModel.getModelLabel())

	runModel.runtimeInfo.Model = "gpt-5.5"
	runModel.runtimeInfo.API = "chat-completions"
	require.Equal(t, "GPT-5.5", runModel.getModelLabel())
}

func TestBottomStatusPanelTruncatesEffortWithModelWithoutDisplacingPriorityCells(t *testing.T) {
	panel := bottomStatusPanel{
		Width:            42,
		ContentWidth:     42,
		ModelName:        "A very long model display name (xhigh)",
		Context:          "42%",
		Thinking:         true,
		PermissionLabel:  "Custom",
		PermissionPreset: "custom",
	}

	rendered := defaultBottomStatusPanelRenderer.Render(panel)
	plain := stripANSI(rendered)

	require.Contains(t, plain, "Thinking")
	require.Contains(t, plain, "Custom")
	require.Contains(t, plain, "42%")
	require.LessOrEqual(t, lipgloss.Width(rendered), panel.Width)
}

func TestApplySelectedModelImmediatelySuppressesOldEffortAndTracksAPI(t *testing.T) {
	runModel := newReasoningEffortTestModel()
	runModel.reasoning.EffectiveEffort = "high"
	runModel.runtimeStateRetryKey = "old"
	runModel.runtimeStateRetryAttempts = 3

	runModel.applySelectedModelToRuntime(rpcclient.ModelOption{
		ID: "gpt-5.6", Name: "GPT-5.6", Provider: "openai", API: "responses-v2",
	})

	require.True(t, runModel.modelRestartPending)
	require.Empty(t, runModel.reasoning.EffectiveEffort)
	require.Equal(t, "responses-v2", runModel.runtimeInfo.API)
	require.Equal(t, "GPT-5.6", runModel.getModelLabel())
	require.Empty(t, runModel.runtimeStateRetryKey)
	require.Zero(t, runModel.runtimeStateRetryAttempts)
}

func TestApplySessionExecutionStateHydratesMatchingReasoningAndIgnoresLateSession(t *testing.T) {
	runModel := newReasoningEffortTestModel()
	state := rpcclient.SessionExecutionState{
		SessionID: "ses_effort",
		Reasoning: runModel.reasoning,
	}
	state.Reasoning.EffectiveEffort = "medium"

	require.NotNil(t, runModel.applySessionExecutionState(sessionExecutionStateLoadedMsg{
		State: state,
		Runtime: rpcclient.ModelRuntime{
			Provider: "openai", API: "responses", Model: "gpt-5.5",
		},
		RuntimeLoaded: true,
	}))
	require.Equal(t, agentsession.ReasoningEffort("medium"), runModel.reasoning.EffectiveEffort)

	state.SessionID = "ses_old"
	state.Reasoning.EffectiveEffort = "high"
	require.Nil(t, runModel.applySessionExecutionState(sessionExecutionStateLoadedMsg{State: state}))
	require.Equal(t, agentsession.ReasoningEffort("medium"), runModel.reasoning.EffectiveEffort)
}

func TestApplySessionExecutionStateSuppressesRuntimeTupleMismatch(t *testing.T) {
	runModel := newReasoningEffortTestModel()
	state := rpcclient.SessionExecutionState{
		SessionID: "ses_effort",
		ActiveRun: &rpcclient.SessionActiveRun{
			ID: "run_current", SessionID: "ses_effort",
		},
		Cursor:    9,
		Reasoning: runModel.reasoning,
	}
	state.Reasoning.EffectiveEffort = "high"

	cmd := runModel.applySessionExecutionState(sessionExecutionStateLoadedMsg{
		State: state,
		Runtime: rpcclient.ModelRuntime{
			Provider: "openai", API: "responses", Model: "gpt-5.6",
		},
		RuntimeLoaded: true,
	})

	require.NotNil(t, cmd)
	require.Empty(t, runModel.reasoning.EffectiveEffort)
	require.Equal(t, 1, runModel.runtimeStateRetryAttempts)
	require.Equal(t, int64(9), runModel.sessionExecutionState.Cursor)
	require.Equal(t, "run_current", runModel.getActiveSessionRunID())
	require.Equal(t, "ses_effort", runModel.sessionObserverSessionID)

	for range sessionRuntimeStateRetryLimit {
		runModel.applySessionExecutionState(sessionExecutionStateLoadedMsg{
			State: state,
			Runtime: rpcclient.ModelRuntime{
				Provider: "openai", API: "responses", Model: "gpt-5.6",
			},
			RuntimeLoaded: true,
		})
	}
	require.Equal(t, sessionRuntimeStateRetryLimit, runModel.runtimeStateRetryAttempts)

	runModel.applySessionExecutionState(sessionExecutionStateLoadedMsg{
		State: state,
		Runtime: rpcclient.ModelRuntime{
			Provider: "openai", API: "responses", Model: "gpt-5.5",
		},
		RuntimeLoaded: true,
	})
	require.Zero(t, runModel.runtimeStateRetryAttempts)
	require.Empty(t, runModel.runtimeStateRetryKey)
}

func TestApplySessionExecutionStateAcceptsLegacyStateWithoutReasoningTuple(t *testing.T) {
	runModel := newReasoningEffortTestModel()
	runModel.runtimeStateRetryKey = "stale"
	runModel.runtimeStateRetryAttempts = 3
	state := rpcclient.SessionExecutionState{
		SessionID: "ses_effort",
		ActiveRun: &rpcclient.SessionActiveRun{
			ID: "run_legacy", SessionID: "ses_effort",
		},
		Cursor: 12,
	}

	cmd := runModel.applySessionExecutionState(sessionExecutionStateLoadedMsg{
		State: state,
		Runtime: rpcclient.ModelRuntime{
			Provider: "openai", API: "responses", Model: "gpt-5.5",
		},
		RuntimeLoaded: true,
	})

	require.NotNil(t, cmd)
	require.Equal(t, int64(12), runModel.sessionExecutionState.Cursor)
	require.Equal(t, "run_legacy", runModel.getActiveSessionRunID())
	require.Equal(t, "ses_effort", runModel.sessionObserverSessionID)
	require.Empty(t, runModel.reasoning.Model.Model)
	require.Empty(t, runModel.reasoning.EffectiveEffort)
	require.Zero(t, runModel.runtimeStateRetryAttempts)
	require.Empty(t, runModel.runtimeStateRetryKey)
}

func TestSessionQueueRunEventsKeepActiveReasoningSnapshotCurrent(t *testing.T) {
	runModel := newReasoningEffortTestModel()
	runModel.sessionObserverSessionID = "ses_effort"
	runModel.sessionObserverID = 7
	runModel.sessionExecutionState.Cursor = 1
	runModel.sessionObserverEvents = make(chan tea.Msg, 1)
	run := rpcclient.SessionActiveRun{
		SessionID: "ses_effort",
		Status:    agentsession.RunStatusRunning,
		Reasoning: agentsession.ReasoningSnapshot{
			Provider: "openai", API: "responses", Model: "gpt-5.5", Effort: "low",
		},
	}

	runModel.applySessionQueueEvent(sessionQueueEventMsg{
		SessionID:  "ses_effort",
		ObserverID: 7,
		Event: rpcclient.SessionEvent{
			Cursor: 2,
			Run:    &run,
		},
	})
	require.NotNil(t, runModel.reasoning.ActiveRunSnapshot)
	require.Equal(t, agentsession.ReasoningEffort("low"), runModel.reasoning.ActiveRunSnapshot.Effort)

	run.Status = agentsession.RunStatusCompleted
	runModel.applySessionQueueEvent(sessionQueueEventMsg{
		SessionID:  "ses_effort",
		ObserverID: 7,
		Event: rpcclient.SessionEvent{
			Cursor: 3,
			Run:    &run,
		},
	})
	require.Nil(t, runModel.reasoning.ActiveRunSnapshot)
}

func TestIsStaleReasoningEffortErrorHandlesWrappedAndRemoteErrors(t *testing.T) {
	require.True(t, isStaleReasoningEffortError(
		errors.New("rpc failed: reasoning model selection is stale"),
	))
	require.True(t, isStaleReasoningEffortError(
		errors.Join(errors.New("wrapped"), agentsession.ErrReasoningStaleTuple),
	))
}

func newReasoningEffortTestModel() model {
	runModel := newModel()
	runModel.applyAction(setSessionAction{ID: "ses_effort"})
	runModel.runtimeInfo.Provider = "openai"
	runModel.runtimeInfo.API = "responses"
	runModel.runtimeInfo.Model = "gpt-5.5"
	runModel.modelName = "GPT-5.5"
	runModel.reasoning = agentsession.ReasoningSettings{
		Model: agentsession.ReasoningModelTuple{
			Provider: "openai", API: "responses", Model: "gpt-5.5", DisplayName: "GPT-5.5",
		},
		SupportedEfforts: []agentsession.ReasoningEffort{"none", "low", "medium", "high"},
		Reasoning:        true,
		Adjustable:       true,
	}
	return runModel
}

func effortSetMessageFromCommand(t *testing.T, cmd tea.Cmd) reasoningEffortSetMsg {
	t.Helper()
	require.NotNil(t, cmd)
	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	for _, command := range batch {
		if command == nil {
			continue
		}
		if msg, ok := command().(reasoningEffortSetMsg); ok {
			return msg
		}
	}
	t.Fatal("reasoning effort command not found")
	return reasoningEffortSetMsg{}
}
