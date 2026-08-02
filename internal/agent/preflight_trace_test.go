package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/agent/context/compaction"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/mocks"
	models "github.com/xymorphic/morph/internal/model"
	"github.com/xymorphic/morph/internal/trace"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
)

func TestPreflightCompactionTraceRecordsWarningAndTrigger(t *testing.T) {
	cfg := &config.Config{}
	enabled := true
	cfg.Compaction.Enabled = &enabled
	cfg.Models.Main.ContextLength = 100
	cfg.Compaction.TriggerPercent = 0.5
	cfg.Compaction.WarnPercent = 0.25
	traceSession := &mocks.TraceSessionStub{}

	request := models.Request{Messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "measured"}}}
	anchor := compaction.Anchor{PromptTokens: 60, MessageCount: 1}
	recordPreflightCompactionTrace(traceSession, cfg, request, anchor, true)

	require.Len(t, traceSession.Events, 3)
	require.Equal(t, trace.EvtContextPreflight, traceSession.Events[0].Type)
	require.Equal(t, trace.EvtContextCompactionTriggered, traceSession.Events[1].Type)
	require.Equal(t, trace.EvtContextCompactionWarning, traceSession.Events[2].Type)
	payload, ok := traceSession.Events[0].Payload.(trace.ContextEventPayload)
	require.True(t, ok)
	require.Equal(t, compaction.ActualSource, payload.Source)
	require.Equal(t, 60, payload.AnchorPromptTokens)
	require.Equal(t, 1, payload.AnchorMessageCount)
	require.Zero(t, payload.DeltaPromptTokens)

	traceSession.Events = nil
	request.Messages = append(request.Messages, morphmsg.Message{Role: morphmsg.RoleTool, Content: "appended"})
	recordPreflightCompactionTrace(traceSession, cfg, request, anchor, false)
	payload, ok = traceSession.Events[0].Payload.(trace.ContextEventPayload)
	require.True(t, ok)
	require.Equal(t, compaction.AnchoredSource, payload.Source)
	require.Equal(t, 60, payload.AnchorPromptTokens)
	require.Positive(t, payload.DeltaPromptTokens)

	disabled := false
	cfg.Compaction.Enabled = &disabled
	traceSession.Events = nil
	recordPreflightCompactionTrace(traceSession, cfg, request, anchor, true)
	require.Empty(t, traceSession.Events)
	require.True(t, isCompactionEnabled(nil))
	estimate := getCompactionEvaluator(nil).Evaluate(models.Request{}, compaction.Anchor{})
	require.Equal(t, 128000, estimate.ContextLimit)
	require.Equal(t, compaction.EstimatedSource, estimate.Source)
	require.Equal(t, compaction.EstimateRequestRough(models.Request{}), estimate.PromptTokens)

	cfg.Compaction.Enabled = &enabled
	traceSession.Events = nil
	recordPreflightCompactionTrace(traceSession, cfg, request, compaction.Anchor{PromptTokens: 30, MessageCount: 1}, false)
	require.Equal(t, []string{
		trace.EvtContextPreflight,
		trace.EvtContextCompactionWarning,
	}, []string{traceSession.Events[0].Type, traceSession.Events[1].Type})
}
