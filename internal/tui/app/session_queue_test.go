package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	rpcclient "github.com/wandxy/morph/internal/rpc/client"
	"github.com/wandxy/morph/internal/trace"
	agentsession "github.com/wandxy/morph/pkg/agent/session"
)

type sessionQueueTUIClient struct {
	submitted  []rpcclient.SubmitMessageOptions
	state      rpcclient.SessionExecutionState
	events     []rpcclient.SessionEvent
	editedID   string
	removedID  string
	promoted   string
	steered    string
	interrupts int
	err        error
}

func (c *sessionQueueTUIClient) SubmitMessage(
	_ context.Context,
	opts rpcclient.SubmitMessageOptions,
) (rpcclient.SessionQueueEntry, error) {
	c.submitted = append(c.submitted, opts)
	return rpcclient.SessionQueueEntry{
		ID: "qmsg_test", SessionID: opts.SessionID, Content: opts.Message,
		RequestedDeliveryMode: opts.DeliveryMode, DeliveryMode: opts.DeliveryMode,
		Status: agentsession.QueueStatusPending,
	}, c.err
}

func (c *sessionQueueTUIClient) State(
	context.Context,
	string,
) (rpcclient.SessionExecutionState, error) {
	return c.state, c.err
}

func (c *sessionQueueTUIClient) Observe(
	_ context.Context,
	_ string,
	_ int64,
	observe func(rpcclient.SessionEvent) error,
) error {
	for _, event := range c.events {
		if err := observe(event); err != nil {
			return err
		}
	}
	return c.err
}

func (c *sessionQueueTUIClient) EditQueuedMessage(
	_ context.Context,
	_ string,
	entryID string,
	message string,
) (rpcclient.SessionQueueEntry, error) {
	c.editedID = entryID
	return rpcclient.SessionQueueEntry{
		ID: entryID, Content: message, Status: agentsession.QueueStatusPending,
	}, c.err
}

func (c *sessionQueueTUIClient) RemoveQueuedMessage(
	_ context.Context,
	_ string,
	entryID string,
) (rpcclient.SessionQueueEntry, error) {
	c.removedID = entryID
	return rpcclient.SessionQueueEntry{
		ID: entryID, Status: agentsession.QueueStatusCancelled,
	}, c.err
}

func (c *sessionQueueTUIClient) PromoteQueuedMessage(
	_ context.Context,
	_ string,
	entryID string,
) (rpcclient.SessionQueueEntry, error) {
	c.promoted = entryID
	return rpcclient.SessionQueueEntry{
		ID: entryID, Priority: 1, Status: agentsession.QueueStatusPending,
	}, c.err
}

func (c *sessionQueueTUIClient) SteerQueuedMessage(
	_ context.Context,
	_ string,
	entryID string,
) (rpcclient.SessionQueueEntry, error) {
	c.steered = entryID
	return rpcclient.SessionQueueEntry{
		ID:                    entryID,
		RequestedDeliveryMode: agentsession.DeliveryModeSteering,
		DeliveryMode:          agentsession.DeliveryModeSteering,
		Status:                agentsession.QueueStatusPending,
	}, c.err
}

func (c *sessionQueueTUIClient) InterruptRun(
	context.Context,
	string,
) (rpcclient.SessionActiveRun, bool, error) {
	c.interrupts++
	return rpcclient.SessionActiveRun{}, true, c.err
}

func TestSessionQueueTUI_BusyEnterSubmitsFollowUpWithoutTranscriptEntry(t *testing.T) {
	client := &sessionQueueTUIClient{}
	runModel := newModelWithClient(client)
	runModel.responding = true
	runModel.input.SetValue("next task")

	cmd := runModel.submitPrompt()

	require.NotNil(t, cmd)
	runSessionQueueTestCmd(cmd)
	require.Len(t, client.submitted, 1)
	require.Equal(t, agentsession.DeliveryModeFollowUp, client.submitted[0].DeliveryMode)
	require.Empty(t, runModel.input.Value())
	require.Empty(t, runModel.messages)
}

func TestSessionQueueTUI_RendersAcceptedQueuedUserMessageExactlyOnce(t *testing.T) {
	runModel := newModelWithClient(&sessionQueueTUIClient{})
	runModel.sessionObserverSessionID = defaultSessionID
	runModel.sessionObserverID = 2
	runModel.sessionObserverEvents = make(chan tea.Msg)
	runModel.sessionExecutionState.ActiveRun = &rpcclient.SessionActiveRun{
		ID: "run_follow_up", QueueEntryID: "qmsg_follow_up",
		Status: agentsession.RunStatusRunning,
	}

	applyAccepted := func(sequence int64) {
		_ = runModel.applySessionQueueEvent(sessionQueueEventMsg{
			SessionID:  defaultSessionID,
			ObserverID: 2,
			Event: rpcclient.SessionEvent{Progress: &agentsession.ProgressEvent{
				RunID:        "run_follow_up",
				QueueEntryID: "qmsg_follow_up",
				Sequence:     sequence,
				TraceEvent: &agentsession.TraceEvent{
					Type:    trace.EvtUserMessageAccepted,
					Payload: map[string]any{"message": "queued follow-up"},
				},
			}},
		})
	}

	applyAccepted(1)
	applyAccepted(2)

	require.Equal(t, []string{"You: queued follow-up"}, transcriptCellPlainTexts(runModel.messages))
}

func TestSessionQueueTUI_FollowsStreamWhenNextQueuedRunStarts(t *testing.T) {
	runModel := newModelWithClient(&sessionQueueTUIClient{})
	runModel.height = 10
	runModel.resize()
	for index := 0; index < 30; index++ {
		runModel.messages = append(
			runModel.messages,
			systemTranscriptCell{text: fmt.Sprintf("Message %02d", index)},
		)
	}
	runModel.setTranscriptContent()
	require.True(t, runModel.isTranscriptAtAbsoluteBottom())
	require.False(t, runModel.responding)
	runModel.sessionObserverSessionID = defaultSessionID
	runModel.sessionObserverID = 2
	runModel.sessionObserverEvents = make(chan tea.Msg)

	_ = runModel.applySessionQueueEvent(sessionQueueEventMsg{
		SessionID:  defaultSessionID,
		ObserverID: 2,
		Event: rpcclient.SessionEvent{
			Cursor: 1,
			Run: &rpcclient.SessionActiveRun{
				ID:           "run_follow_up",
				QueueEntryID: "qmsg_follow_up",
				Status:       agentsession.RunStatusRunning,
			},
		},
	})
	_ = runModel.applySessionQueueEvent(sessionQueueEventMsg{
		SessionID:  defaultSessionID,
		ObserverID: 2,
		Event: rpcclient.SessionEvent{Progress: &agentsession.ProgressEvent{
			RunID:        "run_follow_up",
			QueueEntryID: "qmsg_follow_up",
			Sequence:     1,
			Text:         strings.Repeat("streamed response ", 40),
		}},
	})

	require.True(t, runModel.isTranscriptAtAbsoluteBottom())
	require.Contains(t, stripANSI(runModel.transcript.View()), "streamed response")
	require.Empty(t, runModel.renderJumpToBottom())
}

func TestSessionQueueTUI_DoesNotFollowQueuedRunWhenAlreadyScrolled(t *testing.T) {
	runModel := newModelWithClient(&sessionQueueTUIClient{})
	runModel.height = 10
	runModel.resize()
	for index := 0; index < 30; index++ {
		runModel.messages = append(
			runModel.messages,
			systemTranscriptCell{text: fmt.Sprintf("Message %02d", index)},
		)
	}
	runModel.setTranscriptContent()
	runModel.sessionObserverSessionID = defaultSessionID
	runModel.sessionObserverID = 2
	runModel.sessionObserverEvents = make(chan tea.Msg)
	runModel.scrollTranscriptWithKey(tea.KeyPressMsg{Code: tea.KeyHome})
	offsetBefore := runModel.transcript.YOffset()

	_ = runModel.applySessionQueueEvent(sessionQueueEventMsg{
		SessionID:  defaultSessionID,
		ObserverID: 2,
		Event: rpcclient.SessionEvent{
			Cursor: 1,
			Run: &rpcclient.SessionActiveRun{
				ID:           "run_follow_up",
				QueueEntryID: "qmsg_follow_up",
				Status:       agentsession.RunStatusRunning,
			},
		},
	})
	_ = runModel.applySessionQueueEvent(sessionQueueEventMsg{
		SessionID:  defaultSessionID,
		ObserverID: 2,
		Event: rpcclient.SessionEvent{Progress: &agentsession.ProgressEvent{
			RunID:        "run_follow_up",
			QueueEntryID: "qmsg_follow_up",
			Sequence:     1,
			Text:         strings.Repeat("streamed response ", 40),
		}},
	})

	require.Equal(t, offsetBefore, runModel.transcript.YOffset())
	require.False(t, runModel.isTranscriptAtAbsoluteBottom())
	require.NotContains(t, stripANSI(runModel.transcript.View()), "streamed response")
	require.NotEmpty(t, runModel.renderJumpToBottom())
}

func TestSessionQueueTUI_DoesNotDuplicateOptimisticallyRenderedUserMessage(t *testing.T) {
	runModel := newModelWithClient(&sessionQueueTUIClient{})
	runModel.messages = []transcriptCell{userTranscriptCell{text: "initial prompt"}}
	runModel.sessionObserverSessionID = defaultSessionID
	runModel.sessionObserverID = 2
	runModel.sessionObserverEvents = make(chan tea.Msg)
	runModel.sessionExecutionState.ActiveRun = &rpcclient.SessionActiveRun{
		ID: "run_initial", QueueEntryID: "qmsg_initial",
		Status: agentsession.RunStatusRunning,
	}

	_ = runModel.applySessionQueueEvent(sessionQueueEventMsg{
		SessionID:  defaultSessionID,
		ObserverID: 2,
		Event: rpcclient.SessionEvent{Progress: &agentsession.ProgressEvent{
			RunID:        "run_initial",
			QueueEntryID: "qmsg_initial",
			Sequence:     1,
			TraceEvent: &agentsession.TraceEvent{
				Type:    trace.EvtUserMessageAccepted,
				Payload: map[string]any{"message": "initial prompt"},
			},
		}},
	})

	require.Equal(t, []string{"You: initial prompt"}, transcriptCellPlainTexts(runModel.messages))
}

func TestSessionQueueTUI_AllowsSameUserMessageInLaterTurn(t *testing.T) {
	runModel := newModelWithClient(&sessionQueueTUIClient{})
	runModel.messages = []transcriptCell{
		userTranscriptCell{text: "repeat"},
		assistantTranscriptCell{text: "first response"},
	}
	runModel.sessionObserverSessionID = defaultSessionID
	runModel.sessionObserverID = 2
	runModel.sessionObserverEvents = make(chan tea.Msg)
	runModel.sessionExecutionState.ActiveRun = &rpcclient.SessionActiveRun{
		ID: "run_repeat", QueueEntryID: "qmsg_repeat",
		Status: agentsession.RunStatusRunning,
	}

	_ = runModel.applySessionQueueEvent(sessionQueueEventMsg{
		SessionID:  defaultSessionID,
		ObserverID: 2,
		Event: rpcclient.SessionEvent{Progress: &agentsession.ProgressEvent{
			RunID:        "run_repeat",
			QueueEntryID: "qmsg_repeat",
			Sequence:     1,
			TraceEvent: &agentsession.TraceEvent{
				Type:    trace.EvtUserMessageAccepted,
				Payload: map[string]any{"message": "repeat"},
			},
		}},
	})

	require.Equal(t, []string{
		"You: repeat",
		"Morph: first response",
		"You: repeat",
	}, transcriptCellPlainTexts(runModel.messages))
}

func TestSessionQueueTUI_ObserverRendersLiveToolEventsForActiveFollowUp(t *testing.T) {
	runModel := newModelWithClient(&sessionQueueTUIClient{})
	runModel.sessionObserverSessionID = defaultSessionID
	runModel.sessionObserverID = 2
	runModel.sessionObserverEvents = make(chan tea.Msg)
	runModel.sessionExecutionState.ActiveRun = &rpcclient.SessionActiveRun{
		ID: "run_test", QueueEntryID: "qmsg_follow_up",
		Status: agentsession.RunStatusRunning,
	}

	_ = runModel.applySessionQueueEvent(sessionQueueEventMsg{
		SessionID:  defaultSessionID,
		ObserverID: 2,
		Event: rpcclient.SessionEvent{Progress: &agentsession.ProgressEvent{
			QueueEntryID: "qmsg_follow_up",
			Sequence:     1,
			TraceEvent: &agentsession.TraceEvent{
				Type: trace.EvtToolInvocationStarted,
				Payload: map[string]any{
					"id":   "call_1",
					"name": "read_file",
				},
			},
		}},
	})
	require.Equal(t, 1, runModel.responseRunningToolCount)

	_ = runModel.applySessionQueueEvent(sessionQueueEventMsg{
		SessionID:  defaultSessionID,
		ObserverID: 2,
		Event: rpcclient.SessionEvent{Progress: &agentsession.ProgressEvent{
			QueueEntryID: "qmsg_follow_up",
			Sequence:     2,
			TraceEvent: &agentsession.TraceEvent{
				Type: trace.EvtToolInvocationCompleted,
				Payload: map[string]any{
					"tool_call_id": "call_1",
					"name":         "read_file",
				},
			},
		}},
	})

	require.Zero(t, runModel.responseRunningToolCount)
	require.Contains(t, stripANSI(runModel.transcript.GetContent()), "read_file")
}

func TestSessionQueueTUI_DefersNextRunProgressUntilRunBecomesActive(t *testing.T) {
	runModel := newModelWithClient(&sessionQueueTUIClient{})
	runModel.sessionObserverSessionID = defaultSessionID
	runModel.sessionObserverID = 2
	runModel.sessionObserverEvents = make(chan tea.Msg)
	runModel.sessionExecutionState.ActiveRun = &rpcclient.SessionActiveRun{
		ID: "run_first", QueueEntryID: "qmsg_first",
		Status: agentsession.RunStatusRunning,
	}

	_ = runModel.applySessionQueueEvent(sessionQueueEventMsg{
		SessionID:  defaultSessionID,
		ObserverID: 2,
		Event: rpcclient.SessionEvent{Progress: &agentsession.ProgressEvent{
			QueueEntryID: "qmsg_next",
			Sequence:     1,
			TraceEvent: &agentsession.TraceEvent{
				Type: trace.EvtToolInvocationStarted,
				Payload: map[string]any{
					"id":   "call_next",
					"name": "web_search",
				},
			},
		}},
	})
	require.Zero(t, runModel.responseRunningToolCount)
	require.Len(t, runModel.sessionDeferredProgress, 1)

	_ = runModel.applySessionQueueEvent(sessionQueueEventMsg{
		SessionID:  defaultSessionID,
		ObserverID: 2,
		Event: rpcclient.SessionEvent{
			Cursor: 1,
			Run: &rpcclient.SessionActiveRun{
				ID: "run_next", QueueEntryID: "qmsg_next",
				Status: agentsession.RunStatusRunning,
			},
		},
	})

	require.Equal(t, 1, runModel.responseRunningToolCount)
	require.Empty(t, runModel.sessionDeferredProgress)
	require.Contains(t, stripANSI(runModel.transcript.GetContent()), "web_search")
}

func TestSessionQueueTUI_SameSessionStateReloadPreservesDeferredProgress(t *testing.T) {
	runModel := newModelWithClient(&sessionQueueTUIClient{})
	runModel.sessionObserverSessionID = defaultSessionID
	runModel.sessionObserverID = 2
	runModel.sessionObserverEvents = make(chan tea.Msg)
	runModel.sessionExecutionState.ActiveRun = &rpcclient.SessionActiveRun{
		ID: "run_first", QueueEntryID: "qmsg_first",
		Status: agentsession.RunStatusRunning,
	}
	progress := agentsession.ProgressEvent{
		RunID:        "run_next",
		QueueEntryID: "qmsg_next",
		Sequence:     1,
		TraceEvent: &agentsession.TraceEvent{
			Type: trace.EvtToolInvocationStarted,
			Payload: map[string]any{
				"id":   "call_next",
				"name": "web_search",
			},
		},
	}

	_ = runModel.applySessionQueueEvent(sessionQueueEventMsg{
		SessionID:  defaultSessionID,
		ObserverID: 2,
		Event:      rpcclient.SessionEvent{Progress: &progress},
	})
	require.Len(t, runModel.sessionDeferredProgress, 1)

	cmd := runModel.applySessionExecutionState(sessionExecutionStateLoadedMsg{
		State: rpcclient.SessionExecutionState{
			SessionID: defaultSessionID,
			ActiveRun: &rpcclient.SessionActiveRun{
				ID: "run_next", QueueEntryID: "qmsg_next",
				Status: agentsession.RunStatusRunning,
			},
			Progress: []agentsession.ProgressEvent{progress},
		},
	})

	require.NotNil(t, cmd)
	t.Cleanup(runModel.sessionObserverCancel)
	require.Equal(t, 1, runModel.responseRunningToolCount)
	require.Empty(t, runModel.sessionDeferredProgress)
	require.Contains(t, stripANSI(runModel.transcript.GetContent()), "web_search")
}

func TestSessionQueueTUI_ResponseCompletionWaitsForObservedToolCompletion(t *testing.T) {
	runModel := newModelWithClient(&sessionQueueTUIClient{})
	runModel.responding = true
	runModel.responseID = 7
	runModel.responseEventStreamActive = false
	runModel.sessionObserverSessionID = defaultSessionID
	runModel.sessionObserverID = 2
	runModel.sessionObserverEvents = make(chan tea.Msg)
	runModel.sessionExecutionState = rpcclient.SessionExecutionState{
		SessionID: defaultSessionID,
		ActiveRun: &rpcclient.SessionActiveRun{
			ID: "run_test", QueueEntryID: "qmsg_test",
			Status: agentsession.RunStatusRunning,
		},
		Queue: []rpcclient.SessionQueueEntry{{
			ID: "qmsg_test", Status: agentsession.QueueStatusActive,
		}},
	}
	runModel.applyTUIMessage(toolInvocationStartedMsg{
		ID: "call_1", Name: "read_file",
	})

	cmd := runModel.handleResponseCompleted(responseCompletedMsg{
		ResponseID: 7, QueueEntryID: "qmsg_test",
	})
	require.Nil(t, cmd)
	require.True(t, runModel.responding)

	_ = runModel.applySessionQueueEvent(sessionQueueEventMsg{
		SessionID:  defaultSessionID,
		ObserverID: 2,
		Event: rpcclient.SessionEvent{Progress: &agentsession.ProgressEvent{
			QueueEntryID: "qmsg_test",
			Sequence:     1,
			TraceEvent: &agentsession.TraceEvent{
				Type: trace.EvtToolInvocationCompleted,
				Payload: map[string]any{
					"tool_call_id": "call_1",
					"name":         "read_file",
				},
			},
		}},
	})
	require.True(t, runModel.responding)
	require.Zero(t, runModel.responseRunningToolCount)

	_ = runModel.applySessionQueueEvent(sessionQueueEventMsg{
		SessionID:  defaultSessionID,
		ObserverID: 2,
		Event: rpcclient.SessionEvent{
			Cursor: 1,
			Queue: &rpcclient.SessionQueueEntry{
				ID: "qmsg_test", Status: agentsession.QueueStatusCompleted,
			},
		},
	})

	require.False(t, runModel.responding)
	require.Nil(t, runModel.pendingResponseCompletion)
}

func TestSessionQueueTUI_FastTerminalStateKeepsOrderingEntryID(t *testing.T) {
	client := &sessionQueueTUIClient{
		state: rpcclient.SessionExecutionState{
			SessionID: defaultSessionID,
			Queue: []rpcclient.SessionQueueEntry{{
				ID: "qmsg_test", Status: agentsession.QueueStatusCompleted,
			}},
		},
	}
	events := make(chan tea.Msg)

	completed := respondToPromptCmd(
		client,
		7,
		context.Background(),
		defaultSessionID,
		"fast response",
		"",
		events,
	)()

	require.Equal(t, responseCompletedMsg{
		ResponseID: 7, QueueEntryID: "qmsg_test",
	}, completed)
}

func TestSessionQueueTUI_StateReloadAcceptsRestartedProgressSequence(t *testing.T) {
	runModel := newModelWithClient(&sessionQueueTUIClient{})
	runModel.sessionObserverSessionID = defaultSessionID
	runModel.sessionProgressSequences = map[string]int64{
		"run_before_restart": 12,
	}

	cmd := runModel.applySessionExecutionState(sessionExecutionStateLoadedMsg{
		State: rpcclient.SessionExecutionState{
			SessionID: defaultSessionID,
			ActiveRun: &rpcclient.SessionActiveRun{
				ID: "run_after_restart", QueueEntryID: "qmsg_after_restart",
				Status: agentsession.RunStatusRunning,
			},
			Progress: []agentsession.ProgressEvent{{
				RunID:        "run_after_restart",
				QueueEntryID: "qmsg_after_restart",
				Sequence:     1,
				TraceEvent: &agentsession.TraceEvent{
					Type: trace.EvtToolInvocationStarted,
					Payload: map[string]any{
						"id":   "call_after_restart",
						"name": "web_search",
					},
				},
			}},
		},
	})
	require.NotNil(t, cmd)
	t.Cleanup(runModel.sessionObserverCancel)
	require.Equal(t, int64(1), runModel.sessionProgressSequences["run_after_restart"])
	require.Equal(t, 1, runModel.responseRunningToolCount)
	require.Contains(t, stripANSI(runModel.transcript.GetContent()), "web_search")
}

func TestSessionQueueTUI_DelayedStateDoesNotReplayAppliedProgress(t *testing.T) {
	runModel := newModelWithClient(&sessionQueueTUIClient{})
	runModel.sessionObserverSessionID = defaultSessionID
	runModel.sessionProgressSequences = map[string]int64{"run_test": 12}

	cmd := runModel.applySessionExecutionState(sessionExecutionStateLoadedMsg{
		State: rpcclient.SessionExecutionState{
			SessionID: defaultSessionID,
			ActiveRun: &rpcclient.SessionActiveRun{
				ID: "run_test", QueueEntryID: "qmsg_test",
				Status: agentsession.RunStatusRunning,
			},
			Progress: []agentsession.ProgressEvent{{
				RunID:        "run_test",
				QueueEntryID: "qmsg_test",
				Sequence:     10,
				TraceEvent: &agentsession.TraceEvent{
					Type: trace.EvtToolInvocationStarted,
					Payload: map[string]any{
						"id":   "call_old",
						"name": "read_file",
					},
				},
			}},
		},
	})

	require.NotNil(t, cmd)
	t.Cleanup(runModel.sessionObserverCancel)
	require.Equal(t, int64(12), runModel.sessionProgressSequences["run_test"])
	require.Zero(t, runModel.responseRunningToolCount)
	require.NotContains(t, stripANSI(runModel.transcript.GetContent()), "read_file")
}

func TestSessionQueueTUI_RendersOnlyPendingSteeringAndFollowUpMessages(t *testing.T) {
	client := &sessionQueueTUIClient{}
	runModel := newModelWithClient(client)
	state := rpcclient.SessionExecutionState{
		SessionID: defaultSessionID,
		ActiveRun: &rpcclient.SessionActiveRun{
			ID: "run_test", QueueEntryID: "qmsg_active",
			Status: agentsession.RunStatusRunning,
		},
		Queue: []rpcclient.SessionQueueEntry{
			{
				ID: "qmsg_active", Content: "working", Sequence: 1,
				DeliveryMode: agentsession.DeliveryModeFollowUp,
				Status:       agentsession.QueueStatusActive,
			},
			{
				ID: "qmsg_steer", Content: "use UTC", Sequence: 2,
				RequestedDeliveryMode: agentsession.DeliveryModeSteering,
				DeliveryMode:          agentsession.DeliveryModeSteering,
				Status:                agentsession.QueueStatusPending,
			},
			{
				ID: "qmsg_follow", Content: "then summarize", Sequence: 3,
				RequestedDeliveryMode: agentsession.DeliveryModeFollowUp,
				DeliveryMode:          agentsession.DeliveryModeFollowUp,
				Status:                agentsession.QueueStatusPending,
			},
		},
	}

	_ = runModel.applySessionExecutionState(sessionExecutionStateLoadedMsg{State: state})
	t.Cleanup(runModel.sessionObserverCancel)
	rendered := runModel.renderSessionQueue()
	plain := stripANSI(rendered)

	require.Contains(t, plain, "Queue 2 messages")
	require.NotContains(t, plain, "working")
	require.Contains(t, plain, "↳ use UTC")
	require.Contains(t, plain, "○ then summarize")
	require.Equal(t, 2, strings.Count(plain, sessionQueueActions))
	require.NotContains(t, plain, "reconnecting")
}

func TestSessionQueueTUI_HidesPanelWhenOnlyTheActiveRunRemains(t *testing.T) {
	runModel := newModelWithClient(&sessionQueueTUIClient{})
	runModel.sessionExecutionState = rpcclient.SessionExecutionState{
		SessionID: defaultSessionID,
		ActiveRun: &rpcclient.SessionActiveRun{
			ID: "run_test", QueueEntryID: "qmsg_active",
			Status: agentsession.RunStatusRunning,
		},
		Queue: []rpcclient.SessionQueueEntry{{
			ID: "qmsg_active", Content: "working",
			Status: agentsession.QueueStatusActive,
		}},
	}

	require.Empty(t, runModel.renderSessionQueue())
	require.Zero(t, runModel.getSessionQueueHeight())
}

func runSessionQueueTestCmd(cmd tea.Cmd) {
	message := cmd()
	batch, ok := message.(tea.BatchMsg)
	if !ok {
		return
	}
	for _, child := range batch {
		if child != nil {
			runSessionQueueTestCmd(child)
		}
	}
}

func TestSessionQueueTUI_PreservesDraftWhenMutationFails(t *testing.T) {
	runModel := newModelWithClient(&sessionQueueTUIClient{})
	runModel.input.SetValue("")

	cmd := runModel.completeSessionQueueMutation(sessionQueueMutationCompletedMsg{
		Action: "edit",
		Draft:  "revised message",
		Err:    errors.New("conflict"),
	})

	require.NotNil(t, cmd)
	require.Equal(t, "revised message", runModel.input.Value())
	require.Equal(t, "queue edit failed", runModel.status.Text())
}

func TestSessionQueueTUI_BlocksQueueMutationWhileObserverStateIsStale(t *testing.T) {
	client := &sessionQueueTUIClient{}
	runModel := newModelWithClient(client)
	runModel.sessionQueueStale = true

	cmd := runModel.handleQueueCommand("remove qmsg_test")

	require.NotNil(t, cmd)
	require.Empty(t, client.removedID)
	require.Equal(t, "queue state is stale; refreshing", runModel.status.Text())
}

func TestSessionQueueTUI_BlocksFocusedKeyboardMutationWhileStateIsStale(t *testing.T) {
	client := &sessionQueueTUIClient{}
	runModel := newModelWithClient(client)
	runModel.sessionQueueStale = true
	runModel.sessionQueueFocused = true
	runModel.sessionExecutionState.Queue = []rpcclient.SessionQueueEntry{{
		ID: "qmsg_test", Status: agentsession.QueueStatusPending,
	}}

	next, cmd, handled := runModel.handleSessionQueueKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.True(t, handled)
	runModel = next.(model)
	require.NotNil(t, cmd)
	require.Empty(t, client.promoted)
	require.Equal(t, "queue state is stale; refreshing", runModel.status.Text())
}

func TestSessionQueueTUI_RestoresFailedSubmissionAlongsideNewDraft(t *testing.T) {
	runModel := newModelWithClient(&sessionQueueTUIClient{})
	runModel.input.SetValue("new draft")

	cmd := runModel.completeSessionQueueMutation(sessionQueueMutationCompletedMsg{
		Action: "submit",
		Draft:  "failed submission",
		Err:    errors.New("unavailable"),
	})

	require.NotNil(t, cmd)
	require.Equal(t, "failed submission\nnew draft", runModel.input.Value())
	require.Equal(t, "queue submit failed", runModel.status.Text())
}

func TestSessionQueueTUI_IgnoresEventsFromReplacedObserver(t *testing.T) {
	client := &sessionQueueTUIClient{}
	runModel := newModelWithClient(client)
	runModel.sessionExecutionState = rpcclient.SessionExecutionState{
		SessionID: defaultSessionID,
		Cursor:    4,
	}
	runModel.sessionObserverSessionID = defaultSessionID
	runModel.sessionObserverID = 2
	runModel.sessionObserverEvents = make(chan tea.Msg)
	runModel.sessionQueueStale = false

	cmd := runModel.applySessionQueueEvent(sessionQueueEventMsg{
		SessionID:  defaultSessionID,
		ObserverID: 1,
		Event: rpcclient.SessionEvent{
			Cursor: 5,
			Queue: &rpcclient.SessionQueueEntry{
				ID: "qmsg_old", Status: agentsession.QueueStatusPending,
			},
		},
	})
	require.Nil(t, cmd)
	require.Equal(t, int64(4), runModel.sessionExecutionState.Cursor)
	require.Empty(t, runModel.sessionExecutionState.Queue)

	cmd = runModel.handleSessionQueueEventsClosed(sessionQueueEventsClosedMsg{
		SessionID:  defaultSessionID,
		ObserverID: 1,
		Err:        errors.New("old observer failed"),
	})
	require.Nil(t, cmd)
	require.False(t, runModel.sessionQueueStale)
}

func TestSessionQueueTUI_AppliesCurrentObserverEventAndMarksFailedObserverStale(t *testing.T) {
	client := &sessionQueueTUIClient{}
	runModel := newModelWithClient(client)
	runModel.sessionExecutionState = rpcclient.SessionExecutionState{
		SessionID: defaultSessionID,
		Cursor:    4,
	}
	runModel.sessionObserverSessionID = defaultSessionID
	runModel.sessionObserverID = 2
	runModel.sessionObserverEvents = make(chan tea.Msg)

	cmd := runModel.applySessionQueueEvent(sessionQueueEventMsg{
		SessionID:  defaultSessionID,
		ObserverID: 2,
		Event: rpcclient.SessionEvent{
			Cursor: 5,
			Queue: &rpcclient.SessionQueueEntry{
				ID: "qmsg_new", Status: agentsession.QueueStatusPending,
			},
			Run: &rpcclient.SessionActiveRun{
				ID: "run_new", QueueEntryID: "qmsg_new",
				Status: agentsession.RunStatusRunning,
			},
		},
	})
	require.NotNil(t, cmd)
	require.Equal(t, int64(5), runModel.sessionExecutionState.Cursor)
	require.Equal(t, 1, runModel.sessionExecutionState.QueueDepth)
	require.Equal(t, "qmsg_new", runModel.sessionExecutionState.Queue[0].ID)
	require.Equal(t, "run_new", runModel.sessionExecutionState.ActiveRun.ID)

	cmd = runModel.handleSessionQueueEventsClosed(sessionQueueEventsClosedMsg{
		SessionID:  defaultSessionID,
		ObserverID: 2,
		Err:        errors.New("connection lost"),
	})
	require.NotNil(t, cmd)
	require.True(t, runModel.sessionQueueStale)
	require.Equal(t, "session queue reconnecting", runModel.status.Text())
}

func TestSessionQueueTUI_QueueCommandsUseRequestedEntry(t *testing.T) {
	client := &sessionQueueTUIClient{}
	runModel := newModelWithClient(client)
	runModel.sessionQueueStale = false

	message := runModel.handleQueueCommand("edit qmsg_edit revised")()
	edited := message.(sessionQueueMutationCompletedMsg)
	require.NoError(t, edited.Err)
	require.Equal(t, "qmsg_edit", client.editedID)
	require.Equal(t, "revised", edited.Entry.Content)

	message = runModel.handleQueueCommand("remove qmsg_remove")()
	removed := message.(sessionQueueMutationCompletedMsg)
	require.NoError(t, removed.Err)
	require.Equal(t, "qmsg_remove", client.removedID)

	message = runModel.handleQueueCommand("promote qmsg_promote")()
	promoted := message.(sessionQueueMutationCompletedMsg)
	require.NoError(t, promoted.Err)
	require.Equal(t, "qmsg_promote", client.promoted)

	message = runModel.handleQueueCommand("steer qmsg_steer")()
	queueSteered := message.(sessionQueueMutationCompletedMsg)
	require.NoError(t, queueSteered.Err)
	require.Equal(t, "qmsg_steer", client.steered)

	message = runModel.submitSteeringMessage("use UTC")()
	steered := message.(sessionQueueMutationCompletedMsg)
	require.NoError(t, steered.Err)
	require.Equal(t, agentsession.DeliveryModeSteering, client.submitted[len(client.submitted)-1].DeliveryMode)
}

func TestSessionQueueTUI_RetriesStateHydrationAndInterruptsActiveRun(t *testing.T) {
	client := &sessionQueueTUIClient{
		state: rpcclient.SessionExecutionState{
			SessionID: defaultSessionID,
			Cursor:    4,
		},
	}
	runModel := newModelWithClient(client)

	retry := retrySessionExecutionStateLoadCmd(
		context.Background(),
		client,
		defaultSessionID,
	)
	loaded, ok := retry().(sessionExecutionStateLoadedMsg)
	require.True(t, ok)
	require.Equal(t, int64(4), loaded.State.Cursor)

	interrupted := runModel.requestSessionInterrupt()().(sessionInterruptCompletedMsg)
	require.NoError(t, interrupted.Err)
	require.True(t, interrupted.Transitioned)
	require.Equal(t, 1, client.interrupts)
	require.NotNil(t, runModel.completeSessionInterrupt(interrupted))
	require.Equal(t, "run interrupted", runModel.status.Text())
}

func TestSessionQueueTUI_InterruptKeepsPendingMessagesVisible(t *testing.T) {
	runModel := newModelWithClient(&sessionQueueTUIClient{})
	runModel.sessionExecutionState = rpcclient.SessionExecutionState{
		SessionID: defaultSessionID,
		ActiveRun: &rpcclient.SessionActiveRun{
			ID:           "run_active",
			QueueEntryID: "qmsg_active",
			Status:       agentsession.RunStatusRunning,
		},
		Queue: []rpcclient.SessionQueueEntry{
			{
				ID: "qmsg_active", Content: "active",
				Status: agentsession.QueueStatusActive,
			},
			{
				ID: "qmsg_pending", Content: "still pending",
				Status: agentsession.QueueStatusPending,
			},
		},
		QueueDepth: 1,
	}

	cmd := runModel.completeSessionInterrupt(sessionInterruptCompletedMsg{
		Transitioned: true,
	})

	require.NotNil(t, cmd)
	require.Contains(t, stripANSI(runModel.renderSessionQueue()), "still pending")
	require.Equal(t, "run interrupted", runModel.status.Text())
}

func TestSessionQueueTUI_CancelsStateHydrationRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	retry := retrySessionExecutionStateLoadCmd(
		ctx,
		&sessionQueueTUIClient{},
		defaultSessionID,
	)
	require.Nil(t, retry())
}

func TestSessionQueueTUI_KeyboardActionsSelectSteerPromoteAndRemove(t *testing.T) {
	client := &sessionQueueTUIClient{}
	runModel := newModelWithClient(client)
	runModel.sessionQueueStale = false
	runModel.sessionExecutionState = rpcclient.SessionExecutionState{
		SessionID:  defaultSessionID,
		QueueDepth: 2,
		Queue: []rpcclient.SessionQueueEntry{
			{
				ID: "qmsg_first", Content: "first", Sequence: 1,
				Status: agentsession.QueueStatusPending,
			},
			{
				ID: "qmsg_second", Content: "second", Sequence: 2,
				Status: agentsession.QueueStatusPending,
			},
		},
	}

	next, cmd, handled := runModel.handleSessionQueueKeyPress(tea.KeyPressMsg{
		Code: 'q', Mod: tea.ModCtrl,
	})
	require.True(t, handled)
	require.Nil(t, cmd)
	runModel = next.(model)
	require.True(t, runModel.sessionQueueFocused)

	next, _, handled = runModel.handleSessionQueueKeyPress(tea.KeyPressMsg{Code: tea.KeyDown})
	require.True(t, handled)
	runModel = next.(model)
	require.Equal(t, 1, runModel.sessionQueueSelected)
	require.Contains(t, stripANSI(runModel.renderSessionQueue()), "› ○ second")

	next, cmd, handled = runModel.handleSessionQueueKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.True(t, handled)
	runModel = next.(model)
	require.NotNil(t, cmd)
	mutation := cmd().(sessionQueueMutationCompletedMsg)
	require.NoError(t, mutation.Err)
	require.Equal(t, "qmsg_second", client.promoted)

	_, cmd, handled = runModel.handleSessionQueueKeyPress(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.True(t, handled)
	require.NotNil(t, cmd)
	mutation = cmd().(sessionQueueMutationCompletedMsg)
	require.NoError(t, mutation.Err)
	require.Equal(t, "qmsg_second", client.steered)

	next, cmd, handled = runModel.handleSessionQueueKeyPress(tea.KeyPressMsg{Code: 'x', Text: "x"})
	require.True(t, handled)
	require.NotNil(t, cmd)
	mutation = cmd().(sessionQueueMutationCompletedMsg)
	require.NoError(t, mutation.Err)
	require.Equal(t, "qmsg_second", client.removedID)
	require.IsType(t, model{}, next)
}

func TestSessionQueueTUI_PromotionKeepsTheSameMessageSelected(t *testing.T) {
	runModel := newModelWithClient(&sessionQueueTUIClient{})
	runModel.sessionQueueStale = false
	runModel.sessionQueueFocused = true
	runModel.sessionQueueSelected = 1
	runModel.sessionExecutionState = rpcclient.SessionExecutionState{
		SessionID:  defaultSessionID,
		QueueDepth: 2,
		Queue: []rpcclient.SessionQueueEntry{
			{
				ID: "qmsg_first", Content: "first", Sequence: 1,
				Status: agentsession.QueueStatusPending,
			},
			{
				ID: "qmsg_second", Content: "second", Sequence: 2,
				Status: agentsession.QueueStatusPending,
			},
		},
	}

	_ = runModel.completeSessionQueueMutation(sessionQueueMutationCompletedMsg{
		Action: "promote",
		Entry: rpcclient.SessionQueueEntry{
			ID: "qmsg_second", Content: "second", Sequence: 2, Priority: 1,
			Status: agentsession.QueueStatusPending,
		},
	})

	require.Equal(t, "qmsg_second", runModel.getSelectedSessionQueueEntryID())
	require.Zero(t, runModel.sessionQueueSelected)
}

func TestSessionQueueTUI_EditActionPreservesComposerDraft(t *testing.T) {
	client := &sessionQueueTUIClient{}
	runModel := newModelWithClient(client)
	runModel.sessionQueueStale = false
	runModel.sessionExecutionState = rpcclient.SessionExecutionState{
		SessionID:  defaultSessionID,
		QueueDepth: 1,
		Queue: []rpcclient.SessionQueueEntry{{
			ID: "qmsg_edit", Content: "queued text", Sequence: 1,
			Status: agentsession.QueueStatusPending,
		}},
	}
	runModel.input.SetValue("composer draft")
	runModel.sessionQueueFocused = true

	next, cmd, handled := runModel.handleSessionQueueKeyPress(tea.KeyPressMsg{Code: 'e', Text: "e"})
	require.True(t, handled)
	runModel = next.(model)
	require.NotNil(t, cmd)
	require.Equal(t, "qmsg_edit", runModel.sessionQueueEditingEntryID)
	require.Equal(t, "queued text", runModel.input.Value())
	require.Equal(t, "composer draft", runModel.sessionQueueComposerDraft)

	runModel.input.SetValue("revised queued text")
	cmd = runModel.submitPrompt()
	require.NotNil(t, cmd)
	mutation := cmd().(sessionQueueMutationCompletedMsg)
	require.NoError(t, mutation.Err)
	require.Equal(t, "qmsg_edit", client.editedID)
	require.Equal(t, "revised queued text", mutation.Entry.Content)

	_ = runModel.completeSessionQueueMutation(mutation)
	require.Empty(t, runModel.sessionQueueEditingEntryID)
	require.Equal(t, "composer draft", runModel.input.Value())
}

func TestSessionQueueTUI_MouseEditActionTargetsItsRow(t *testing.T) {
	client := &sessionQueueTUIClient{}
	runModel := newModelWithClient(client)
	runModel.width = 72
	runModel.height = 24
	runModel.sessionQueueStale = false
	runModel.sessionExecutionState = rpcclient.SessionExecutionState{
		SessionID:  defaultSessionID,
		QueueDepth: 1,
		Queue: []rpcclient.SessionQueueEntry{{
			ID: "qmsg_edit", Content: "queued text", Sequence: 1,
			Status: agentsession.QueueStatusPending,
		}},
	}

	layout := runModel.getTUILayout(runModel.input.Height())
	actionStart := layout.SessionQueue.X + layout.SessionQueue.Width -
		1 - lipgloss.Width(sessionQueueActions)
	cmd, handled := runModel.handleSessionQueueClick(tea.MouseClickMsg(tea.Mouse{
		X:      actionStart + 6,
		Y:      layout.SessionQueue.Y + 1,
		Button: tea.MouseLeft,
	}))

	require.True(t, handled)
	require.NotNil(t, cmd)
	require.Equal(t, "qmsg_edit", runModel.sessionQueueEditingEntryID)
	require.Equal(t, "queued text", runModel.input.Value())
}

func TestSessionQueueTUI_MousePromoteActionTargetsItsRow(t *testing.T) {
	client := &sessionQueueTUIClient{}
	runModel := newModelWithClient(client)
	runModel.width = 72
	runModel.height = 24
	runModel.sessionQueueStale = false
	runModel.sessionExecutionState = rpcclient.SessionExecutionState{
		SessionID:  defaultSessionID,
		QueueDepth: 1,
		Queue: []rpcclient.SessionQueueEntry{{
			ID: "qmsg_promote", Content: "queued text", Sequence: 1,
			Status: agentsession.QueueStatusPending,
		}},
	}

	layout := runModel.getTUILayout(runModel.input.Height())
	actionStart := layout.SessionQueue.X + layout.SessionQueue.Width -
		1 - lipgloss.Width(sessionQueueActions)
	cmd, handled := runModel.handleSessionQueueClick(tea.MouseClickMsg(tea.Mouse{
		X:      actionStart + 3,
		Y:      layout.SessionQueue.Y + 1,
		Button: tea.MouseLeft,
	}))

	require.True(t, handled)
	require.NotNil(t, cmd)
	mutation := cmd().(sessionQueueMutationCompletedMsg)
	require.NoError(t, mutation.Err)
	require.Equal(t, "qmsg_promote", client.promoted)
}

func TestSessionQueueTUI_MouseSteerActionTargetsItsRow(t *testing.T) {
	client := &sessionQueueTUIClient{}
	runModel := newModelWithClient(client)
	runModel.width = 72
	runModel.height = 24
	runModel.sessionQueueStale = false
	runModel.sessionExecutionState = rpcclient.SessionExecutionState{
		SessionID:  defaultSessionID,
		QueueDepth: 1,
		Queue: []rpcclient.SessionQueueEntry{{
			ID: "qmsg_steer", Content: "queued text", Sequence: 1,
			Status: agentsession.QueueStatusPending,
		}},
	}

	layout := runModel.getTUILayout(runModel.input.Height())
	actionStart := layout.SessionQueue.X + layout.SessionQueue.Width -
		1 - lipgloss.Width(sessionQueueActions)
	cmd, handled := runModel.handleSessionQueueClick(tea.MouseClickMsg(tea.Mouse{
		X:      actionStart,
		Y:      layout.SessionQueue.Y + 1,
		Button: tea.MouseLeft,
	}))

	require.True(t, handled)
	require.NotNil(t, cmd)
	mutation := cmd().(sessionQueueMutationCompletedMsg)
	require.NoError(t, mutation.Err)
	require.Equal(t, "qmsg_steer", client.steered)
}

func TestSessionQueueTUI_MouseRemoveActionTargetsItsRow(t *testing.T) {
	client := &sessionQueueTUIClient{}
	runModel := newModelWithClient(client)
	runModel.width = 72
	runModel.height = 24
	runModel.sessionQueueStale = false
	runModel.sessionExecutionState = rpcclient.SessionExecutionState{
		SessionID:  defaultSessionID,
		QueueDepth: 1,
		Queue: []rpcclient.SessionQueueEntry{{
			ID: "qmsg_remove", Content: "queued text", Sequence: 1,
			Status: agentsession.QueueStatusPending,
		}},
	}

	layout := runModel.getTUILayout(runModel.input.Height())
	actionStart := layout.SessionQueue.X + layout.SessionQueue.Width -
		1 - lipgloss.Width(sessionQueueActions)
	cmd, handled := runModel.handleSessionQueueClick(tea.MouseClickMsg(tea.Mouse{
		X:      actionStart + 9,
		Y:      layout.SessionQueue.Y + 1,
		Button: tea.MouseLeft,
	}))

	require.True(t, handled)
	require.NotNil(t, cmd)
	mutation := cmd().(sessionQueueMutationCompletedMsg)
	require.NoError(t, mutation.Err)
	require.Equal(t, "qmsg_remove", client.removedID)
}

func TestSessionQueueTUI_LayoutPlacesQueueBetweenJumpPanelAndComposer(t *testing.T) {
	runModel := newModelWithClient(&sessionQueueTUIClient{})
	runModel.width = 60
	runModel.height = 24
	runModel.sessionExecutionState = rpcclient.SessionExecutionState{
		SessionID:  defaultSessionID,
		QueueDepth: 1,
		Queue: []rpcclient.SessionQueueEntry{{
			ID: "qmsg_test", Content: strings.Repeat("wide message ", 12), Sequence: 1,
			Status: agentsession.QueueStatusPending,
		}},
	}

	layout := runModel.getTUILayout(runModel.input.Height())
	require.Positive(t, layout.SessionQueue.Height)
	require.Equal(t, layout.JumpToBottom.Y+layout.JumpToBottom.Height, layout.SessionQueue.Y)
	require.Equal(t, layout.SessionQueue.Y+layout.SessionQueue.Height, layout.Composer.Y)

	for _, line := range strings.Split(runModel.renderSessionQueue(), "\n") {
		require.Equal(t, runModel.getMainPaneWidth(), lipgloss.Width(line))
	}
}

func TestSessionQueueTUI_ViewKeepsPreviousComposerStatusBar(t *testing.T) {
	tests := []struct {
		name  string
		state rpcclient.SessionExecutionState
	}{
		{name: "without queued messages"},
		{
			name: "with queue panel visible",
			state: rpcclient.SessionExecutionState{
				SessionID:  defaultSessionID,
				QueueDepth: 1,
				Queue: []rpcclient.SessionQueueEntry{{
					ID: "qmsg_test", Content: "queued text", Sequence: 1,
					Status: agentsession.QueueStatusPending,
				}},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runModel := newModelWithClient(&sessionQueueTUIClient{})
			runModel.width = 100
			runModel.height = 24
			runModel.sessionExecutionState = test.state
			runModel.resize()

			content := stripANSI(runModel.View().Content)
			lines := strings.Split(content, "\n")

			require.Len(t, lines, runModel.height)
			require.Equal(t, stripANSI(runModel.renderBottomStatusPanel()), lines[len(lines)-1])
		})
	}
}

func TestSessionQueueTUI_ReportsTerminalRunFailures(t *testing.T) {
	queueTests := []struct {
		name  string
		entry rpcclient.SessionQueueEntry
		want  string
	}{
		{
			name:  "failed with provider error",
			entry: rpcclient.SessionQueueEntry{Status: agentsession.QueueStatusFailed, LastError: "provider unavailable"},
			want:  "provider unavailable",
		},
		{
			name:  "failed without detail",
			entry: rpcclient.SessionQueueEntry{Status: agentsession.QueueStatusFailed},
			want:  "session run failed",
		},
		{
			name:  "interrupted",
			entry: rpcclient.SessionQueueEntry{Status: agentsession.QueueStatusInterrupted, LastError: "user_interrupt"},
			want:  "session run interrupted: user_interrupt",
		},
		{
			name:  "cancelled",
			entry: rpcclient.SessionQueueEntry{Status: agentsession.QueueStatusCancelled},
			want:  "session run cancelled",
		},
		{
			name:  "completed",
			entry: rpcclient.SessionQueueEntry{Status: agentsession.QueueStatusCompleted},
		},
	}
	for _, test := range queueTests {
		t.Run("queue "+test.name, func(t *testing.T) {
			err := getSessionQueueTerminalError(test.entry)
			if test.want == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, test.want)
		})
	}

	runTests := []struct {
		name string
		run  rpcclient.SessionActiveRun
		want string
	}{
		{
			name: "failed with provider error",
			run:  rpcclient.SessionActiveRun{Status: agentsession.RunStatusFailed, LastError: "provider unavailable"},
			want: "provider unavailable",
		},
		{
			name: "failed without detail",
			run:  rpcclient.SessionActiveRun{Status: agentsession.RunStatusFailed},
			want: "session run failed",
		},
		{
			name: "interrupted with reason",
			run:  rpcclient.SessionActiveRun{Status: agentsession.RunStatusInterrupted, Reason: "daemon_restart"},
			want: "session run interrupted: daemon_restart",
		},
		{
			name: "interrupted without reason",
			run:  rpcclient.SessionActiveRun{Status: agentsession.RunStatusInterrupted},
			want: "session run interrupted",
		},
		{
			name: "cancelled",
			run:  rpcclient.SessionActiveRun{Status: agentsession.RunStatusCancelled},
			want: "session run cancelled",
		},
		{
			name: "completed",
			run:  rpcclient.SessionActiveRun{Status: agentsession.RunStatusCompleted},
		},
	}
	for _, test := range runTests {
		t.Run("run "+test.name, func(t *testing.T) {
			err := getSessionRunTerminalError(test.run)
			if test.want == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, test.want)
		})
	}
}
