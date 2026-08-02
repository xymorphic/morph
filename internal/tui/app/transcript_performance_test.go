package tui

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/agent"
	rpcclient "github.com/xymorphic/morph/internal/rpc/client"
)

func TestModel_HandleResponseEventBatchAppliesInOrderWithOneRenderAndResize(t *testing.T) {
	runModel := newModel()
	runModel.responding = true
	runModel.responseID = 7
	initialRenders := runModel.transcriptRenders
	initialResizes := runModel.transcriptResizes

	updated, cmd := runModel.handleResponseEventBatch(responseEventBatchMsg{
		ResponseID: 7,
		Messages: []tea.Msg{
			assistantTextDeltaMsg{Text: "hello "},
			assistantTextDeltaMsg{Text: "world"},
			toolInvocationStartedMsg{ID: "call_1", Name: "browser", Detail: "snapshot:Page"},
		},
	})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, "Morph: hello world", transcriptCellPlainText(runModel.live))
	require.Equal(t, "call_1", runModel.messages[0].(toolTranscriptCell).id)
	require.Equal(t, initialRenders+1, runModel.transcriptRenders)
	require.Equal(t, initialResizes+1, runModel.transcriptResizes)
}

func TestModel_HandleResponseEventBatchMatchesSequentialApplication(t *testing.T) {
	events := []tea.Msg{
		assistantTextDeltaMsg{Text: "hello "},
		toolInvocationStartedMsg{ID: "call_1", Name: "browser", Detail: "snapshot:Page"},
		assistantTextDeltaMsg{Text: "world"},
		toolInvocationCompletedMsg{ID: "call_1", Name: "browser", Detail: "snapshot:Page"},
	}
	sequential := newModel()
	sequential.responding = true
	sequential.responseID = 7
	for _, event := range events {
		sequential.applyTUIMessage(event)
	}

	batched := newModel()
	batched.responding = true
	batched.responseID = 7
	updated, _ := batched.handleResponseEventBatch(responseEventBatchMsg{
		ResponseID: 7,
		Messages:   events,
	})
	batched = updated.(model)

	require.Equal(t, transcriptCellPlainTexts(sequential.messages), transcriptCellPlainTexts(batched.messages))
	require.Equal(t, transcriptCellPlainText(sequential.live), transcriptCellPlainText(batched.live))
	require.Equal(t, sequential.renderTranscriptContent(), batched.renderTranscriptContent())
}

func TestModel_HandleResponseEventBatchPropagatesEventCommands(t *testing.T) {
	originalInterval := toolAnimationInterval
	toolAnimationInterval = time.Millisecond
	t.Cleanup(func() {
		toolAnimationInterval = originalInterval
	})

	runModel := newModel()
	runModel.responding = true
	runModel.responseID = 7

	updated, cmd := runModel.handleResponseEventBatch(responseEventBatchMsg{
		ResponseID: 7,
		Messages: []tea.Msg{
			toolInvocationStartedMsg{ID: "call_1", Name: "browser", Detail: "snapshot:Page"},
		},
	})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.True(t, runModel.toolAnimationActive)
	_, ok := cmd().(toolAnimationTickMsg)
	require.True(t, ok)
}

func TestModel_StreamingTranscriptThrottleFlushesTrailingDelta(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	originalCurrentTime := currentTime
	originalInterval := streamingTranscriptRenderInterval
	currentTime = func() time.Time { return now }
	streamingTranscriptRenderInterval = time.Millisecond
	t.Cleanup(func() {
		currentTime = originalCurrentTime
		streamingTranscriptRenderInterval = originalInterval
	})

	runModel := newModel()
	runModel.responding = true
	runModel.responseID = 3

	updated, _ := runModel.handleResponseEventBatch(responseEventBatchMsg{
		ResponseID: 3,
		Messages:   []tea.Msg{assistantTextDeltaMsg{Text: "first"}},
	})
	runModel = updated.(model)
	firstRenders := runModel.transcriptRenders
	require.Contains(t, stripANSI(runModel.transcript.GetContent()), "first")

	updated, cmd := runModel.handleResponseEventBatch(responseEventBatchMsg{
		ResponseID: 3,
		Messages:   []tea.Msg{assistantTextDeltaMsg{Text: " second"}},
	})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, firstRenders, runModel.transcriptRenders)
	require.NotContains(t, stripANSI(runModel.transcript.GetContent()), "second")

	updated, duplicateCmd := runModel.handleResponseEventBatch(responseEventBatchMsg{
		ResponseID: 3,
		Messages:   []tea.Msg{assistantTextDeltaMsg{Text: " third"}},
	})
	require.Nil(t, duplicateCmd)
	runModel = updated.(model)
	require.Equal(t, firstRenders, runModel.transcriptRenders)

	flushMessage := cmd()
	flush, ok := flushMessage.(streamingTranscriptFlushMsg)
	require.True(t, ok)
	require.Equal(t, 3, flush.ResponseID)
	updated, flushCmd := runModel.flushStreamingTranscript(flush)
	require.Nil(t, flushCmd)
	runModel = updated.(model)
	require.Equal(t, firstRenders+1, runModel.transcriptRenders)
	require.Contains(t, stripANSI(runModel.transcript.GetContent()), "first second third")
	require.False(t, runModel.streamingFlushPending)
	require.False(t, runModel.streamingFlushDirty)
}

func TestModel_NonStreamingEventFlushesPendingStreamingRender(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	originalCurrentTime := currentTime
	originalInterval := streamingTranscriptRenderInterval
	currentTime = func() time.Time { return now }
	streamingTranscriptRenderInterval = time.Second
	t.Cleanup(func() {
		currentTime = originalCurrentTime
		streamingTranscriptRenderInterval = originalInterval
	})

	runModel := newModel()
	runModel.responding = true
	runModel.responseID = 3
	updated, _ := runModel.handleResponseEventBatch(responseEventBatchMsg{
		ResponseID: 3,
		Messages:   []tea.Msg{assistantTextDeltaMsg{Text: "first"}},
	})
	runModel = updated.(model)
	updated, _ = runModel.handleResponseEventBatch(responseEventBatchMsg{
		ResponseID: 3,
		Messages:   []tea.Msg{assistantTextDeltaMsg{Text: " second"}},
	})
	runModel = updated.(model)
	require.True(t, runModel.streamingFlushPending)

	updated, _ = runModel.handleResponseEventBatch(responseEventBatchMsg{
		ResponseID: 3,
		Messages: []tea.Msg{
			toolInvocationCompletedMsg{ID: "call_1", Name: "browser", Detail: "snapshot:Page"},
		},
	})
	runModel = updated.(model)
	require.Contains(t, stripANSI(runModel.transcript.GetContent()), "first second")
	require.False(t, runModel.streamingFlushPending)
	require.False(t, runModel.streamingFlushDirty)
}

func TestIsStreamingResponseBatch_ClassifiesOnlyNonEmptyDeltaBatches(t *testing.T) {
	require.False(t, isStreamingResponseBatch(nil))
	require.True(t, isStreamingResponseBatch([]tea.Msg{assistantTextDeltaMsg{Text: "delta"}}))
	require.False(t, isStreamingResponseBatch([]tea.Msg{
		assistantTextDeltaMsg{Text: "delta"},
		toolInvocationStartedMsg{ID: "call_1", Name: "browser"},
	}))
}

func TestModel_NonStreamingRenderResetsStreamingThrottle(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	originalCurrentTime := currentTime
	originalInterval := streamingTranscriptRenderInterval
	currentTime = func() time.Time { return now }
	streamingTranscriptRenderInterval = time.Second
	t.Cleanup(func() {
		currentTime = originalCurrentTime
		streamingTranscriptRenderInterval = originalInterval
	})

	runModel := newModel()
	runModel.responding = true
	runModel.responseID = 3
	updated, _ := runModel.handleResponseEventBatch(responseEventBatchMsg{
		ResponseID: 3,
		Messages:   []tea.Msg{assistantTextDeltaMsg{Text: "first"}},
	})
	runModel = updated.(model)

	now = now.Add(2 * time.Second)
	updated, _ = runModel.handleResponseEventBatch(responseEventBatchMsg{
		ResponseID: 3,
		Messages: []tea.Msg{
			toolInvocationStartedMsg{ID: "call_1", Name: "browser", Detail: "snapshot:Page"},
		},
	})
	runModel = updated.(model)
	toolEventRenders := runModel.transcriptRenders
	require.Equal(t, now, runModel.streamingRenderAt)

	now = now.Add(10 * time.Millisecond)
	updated, cmd := runModel.handleResponseEventBatch(responseEventBatchMsg{
		ResponseID: 3,
		Messages:   []tea.Msg{assistantTextDeltaMsg{Text: " second"}},
	})
	runModel = updated.(model)

	require.NotNil(t, cmd)
	require.Equal(t, toolEventRenders, runModel.transcriptRenders)
	require.True(t, runModel.streamingFlushPending)
}

func TestWaitForResponseEvent_DrainsBufferedEventsInOrderThroughClose(t *testing.T) {
	events := make(chan tea.Msg, 3)
	events <- assistantTextDeltaMsg{Text: "one"}
	events <- assistantTextDeltaMsg{Text: "two"}
	events <- toolInvocationStartedMsg{ID: "call_1", Name: "browser"}
	close(events)

	message := waitForResponseEvent(11, events)()
	batch, ok := message.(responseEventBatchMsg)
	require.True(t, ok)
	require.True(t, batch.Closed)
	require.Equal(t, 11, batch.ResponseID)
	require.Equal(t, []tea.Msg{
		assistantTextDeltaMsg{Text: "one"},
		assistantTextDeltaMsg{Text: "two"},
		toolInvocationStartedMsg{ID: "call_1", Name: "browser"},
	}, batch.Messages)
}

func TestWaitForResponseEvent_BoundsBatchSize(t *testing.T) {
	events := make(chan tea.Msg, responseEventBatchLimit+1)
	for index := 0; index < responseEventBatchLimit+1; index++ {
		events <- assistantTextDeltaMsg{Text: fmt.Sprintf("%d", index)}
	}

	message := waitForResponseEvent(11, events)()
	batch, ok := message.(responseEventBatchMsg)
	require.True(t, ok)
	require.Len(t, batch.Messages, responseEventBatchLimit)
	require.False(t, batch.Closed)
	require.Equal(t, assistantTextDeltaMsg{Text: "0"}, batch.Messages[0])
	require.Equal(t, assistantTextDeltaMsg{Text: "63"}, batch.Messages[responseEventBatchLimit-1])
	require.Len(t, events, 1)
}

func TestModel_UpdateRoutesBatchedEventsAndTrailingFlush(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	originalCurrentTime := currentTime
	originalInterval := streamingTranscriptRenderInterval
	currentTime = func() time.Time { return now }
	streamingTranscriptRenderInterval = time.Second
	t.Cleanup(func() {
		currentTime = originalCurrentTime
		streamingTranscriptRenderInterval = originalInterval
	})

	runModel := newModel()
	runModel.responding = true
	runModel.responseID = 3
	updated, _ := runModel.Update(responseEventBatchMsg{
		ResponseID: 3,
		Messages:   []tea.Msg{assistantTextDeltaMsg{Text: "first"}},
	})
	runModel = updated.(model)
	firstRenders := runModel.transcriptRenders

	updated, cmd := runModel.Update(responseEventBatchMsg{
		ResponseID: 3,
		Messages:   []tea.Msg{assistantTextDeltaMsg{Text: " second"}},
	})
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, firstRenders, runModel.transcriptRenders)

	updated, flushCmd := runModel.Update(streamingTranscriptFlushMsg{ResponseID: 3})
	require.Nil(t, flushCmd)
	runModel = updated.(model)
	require.Equal(t, firstRenders+1, runModel.transcriptRenders)
	require.Contains(t, stripANSI(runModel.transcript.GetContent()), "first second")
}

func TestModel_ClosedResponseBatchPreservesEventsBeforeCompletion(t *testing.T) {
	runModel := newModelWithClient(&fakeTUIChatClient{})
	runModel.responding = true
	runModel.responseID = 11
	runModel.responseEventStreamActive = true
	runModel.pendingResponseCompletion = &responseCompletedMsg{
		ResponseID: 11,
		Text:       "final response",
	}
	initialRenders := runModel.transcriptRenders
	initialResizes := runModel.transcriptResizes

	updated, cmd := runModel.handleResponseEventBatch(responseEventBatchMsg{
		ResponseID: 11,
		Messages: []tea.Msg{
			toolInvocationStartedMsg{ID: "call_1", Name: "browser", Detail: "snapshot:Page"},
			toolInvocationCompletedMsg{ID: "call_1", Name: "browser", Detail: "snapshot:Page"},
		},
		Closed: true,
	})

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.False(t, runModel.responding)
	require.Nil(t, runModel.pendingResponseCompletion)
	require.Equal(t, initialRenders+1, runModel.transcriptRenders)
	require.Equal(t, initialResizes+1, runModel.transcriptResizes)
	require.Equal(t, []string{
		"Tool Browser:\nid: call_1\ndetail: snapshot:Page\nstatus: completed",
		"Morph: final response",
	}, transcriptCellPlainTexts(runModel.messages))
}

func TestModel_ClosedResponseBatchWaitsForLaterCompletion(t *testing.T) {
	runModel := newModel()
	runModel.responding = true
	runModel.responseID = 11
	runModel.responseEventStreamActive = true

	updated, _ := runModel.handleResponseEventBatch(responseEventBatchMsg{
		ResponseID: 11,
		Messages:   []tea.Msg{assistantTextDeltaMsg{Text: "streamed"}},
		Closed:     true,
	})
	runModel = updated.(model)
	require.True(t, runModel.responding)
	require.False(t, runModel.responseEventStreamActive)

	updated, _ = runModel.Update(responseCompletedMsg{ResponseID: 11, Text: "final"})
	runModel = updated.(model)
	require.False(t, runModel.responding)
	require.Equal(t, "Morph: final", runModel.messages[len(runModel.messages)-1].PlainText())
}

func TestModel_CancelResponseDrainsSupersededEventChannel(t *testing.T) {
	events := make(chan tea.Msg, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(events)
		for index := 0; index < 40; index++ {
			events <- assistantTextDeltaMsg{Text: fmt.Sprintf("%d", index)}
		}
	}()

	runModel := newModel()
	runModel.responding = true
	runModel.responseID = 1
	runModel.responseCancel = func() {}
	runModel.events = events
	runModel.cancelResponseAndDrainEvents()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("superseded response producer did not exit")
	}
	require.Nil(t, runModel.events)
}

func TestModel_StartResponseCancelsAndDrainsSupersededEvents(t *testing.T) {
	oldEvents := make(chan tea.Msg, 1)
	producerDone := make(chan struct{})
	go func() {
		defer close(producerDone)
		defer close(oldEvents)
		for index := 0; index < 40; index++ {
			oldEvents <- assistantTextDeltaMsg{Text: fmt.Sprintf("%d", index)}
		}
	}()

	cancelled := false
	client := &fakeTUIChatClient{
		reply: "done",
		events: []rpcclient.Event{
			{Kind: agent.EventKindTextDelta, Text: "new response"},
		},
	}
	runModel := newModelWithClient(client)
	runModel.chatCtx = nil
	runModel.responseCancel = func() { cancelled = true }
	runModel.events = oldEvents

	cmd := runModel.startResponse("next", true)

	require.NotNil(t, cmd)
	require.True(t, cancelled)
	require.NotNil(t, runModel.events)
	require.NotEqual(t, oldEvents, runModel.events)
	select {
	case <-producerDone:
	case <-time.After(time.Second):
		t.Fatal("superseded response producer did not exit")
	}

	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	require.Len(t, batch, 3)
	require.Equal(t, responseCompletedMsg{ResponseID: runModel.responseID}, batch[1]())
	closed, ok := batch[2]().(responseEventsClosedMsg)
	require.True(t, ok)
	require.Equal(t, runModel.responseID, closed.ResponseID)
	runModel.responseCancel()
}

func TestModel_StreamingTranscriptFlushIgnoresStaleResponse(t *testing.T) {
	runModel := newModel()
	runModel.responding = true
	runModel.responseID = 4
	runModel.streamingFlushPending = true
	runModel.streamingFlushDirty = true
	runModel.transcriptRenderPending = true
	initialRenders := runModel.transcriptRenders

	updated, cmd := runModel.flushStreamingTranscript(streamingTranscriptFlushMsg{ResponseID: 3})

	require.Nil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, initialRenders, runModel.transcriptRenders)
	require.True(t, runModel.streamingFlushPending)
	require.True(t, runModel.streamingFlushDirty)
	require.True(t, runModel.transcriptRenderPending)
}

func TestModel_ToolAnimationSkipsUnchangedLayoutResize(t *testing.T) {
	runModel := newModel()
	runModel.messages = []transcriptCell{
		toolTranscriptCell{id: "call_1", action: "Browser", detail: "snapshot:Page", startedAt: time.Now()},
	}
	runModel.setTranscriptContent()
	initialResizes := runModel.transcriptResizes

	updated, cmd := runModel.updateToolAnimation()
	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, initialResizes, runModel.transcriptResizes)
	require.Equal(t, 1, runModel.toolAnimationFrame)
}

func TestModel_ToolAnimationResizesAfterLayoutChange(t *testing.T) {
	runModel := newModel()
	runModel.messages = []transcriptCell{
		toolTranscriptCell{id: "call_1", action: "Browser", detail: "snapshot:Page", startedAt: time.Now()},
	}
	runModel.width++
	initialResizes := runModel.transcriptResizes

	updated, cmd := runModel.updateToolAnimation()

	require.NotNil(t, cmd)
	runModel = updated.(model)
	require.Equal(t, initialResizes+1, runModel.transcriptResizes)
}

func TestModel_ResizeRenderResetsStreamingThrottle(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	originalCurrentTime := currentTime
	originalInterval := streamingTranscriptRenderInterval
	currentTime = func() time.Time { return now }
	streamingTranscriptRenderInterval = time.Second
	t.Cleanup(func() {
		currentTime = originalCurrentTime
		streamingTranscriptRenderInterval = originalInterval
	})

	runModel := newModel()
	runModel.responding = true
	runModel.responseID = 3
	updated, _ := runModel.handleResponseEventBatch(responseEventBatchMsg{
		ResponseID: 3,
		Messages:   []tea.Msg{assistantTextDeltaMsg{Text: "first"}},
	})
	runModel = updated.(model)

	now = now.Add(2 * time.Second)
	runModel.refreshTranscriptContentAfterResize()
	resizeRenders := runModel.transcriptRenders
	require.Equal(t, now, runModel.streamingRenderAt)

	now = now.Add(10 * time.Millisecond)
	updated, cmd := runModel.handleResponseEventBatch(responseEventBatchMsg{
		ResponseID: 3,
		Messages:   []tea.Msg{assistantTextDeltaMsg{Text: " second"}},
	})
	runModel = updated.(model)
	require.NotNil(t, cmd)
	require.Equal(t, resizeRenders, runModel.transcriptRenders)
}

func TestModel_ResponseUpdateRefreshesChangedLayoutBeforeRender(t *testing.T) {
	runModel := newModel()
	runModel.responding = true
	runModel.messages = []transcriptCell{assistantTranscriptCell{text: "updated layout"}}
	runModel.width += 10
	initialResizes := runModel.transcriptResizes

	runModel.setTranscriptContentForResponseUpdateNow()

	require.Equal(t, initialResizes+1, runModel.transcriptResizes)
	require.Equal(t, runModel.getTranscriptLayoutState(), runModel.transcriptLayout)
	require.Contains(t, stripANSI(runModel.transcript.GetContent()), "updated layout")
}

func TestModel_ClearCommandClearsTranscriptRenderCache(t *testing.T) {
	runModel := newModel()
	runModel.messages = []transcriptCell{assistantTranscriptCell{text: "cached"}}
	runModel.setTranscriptContent()
	require.NotZero(t, runModel.transcriptCache.len())

	runModel.handleSlashCommand(composerInput{Name: "clear"})

	require.Zero(t, runModel.transcriptCache.len())
}
