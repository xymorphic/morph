package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	envtypes "github.com/xymorphic/morph/internal/environment/types"
	"github.com/xymorphic/morph/internal/trace"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	agentsession "github.com/xymorphic/morph/pkg/agent/session"
)

func TestPlanHelpersDecodeRenderAndSummarize(t *testing.T) {
	plan := envtypes.Plan{
		Explanation: "because",
		Steps: []envtypes.PlanStep{
			{ID: "one", Content: "first", Status: envtypes.PlanStatusPending},
			{ID: "two", Content: "second", Status: envtypes.PlanStatusInProgress},
			{ID: "three", Content: "third", Status: envtypes.PlanStatusCompleted},
			{ID: "four", Content: "fourth", Status: envtypes.PlanStatusCancelled},
		},
	}
	payload := `{"output":"{\"explanation\":\"because\",\"steps\":[{\"id\":\"one\",\"content\":\"first\",\"status\":\"pending\"},{\"id\":\"two\",\"content\":\"second\",\"status\":\"in_progress\"}]}"}`

	decoded, ok := decodeHydratedPlan(payload)
	require.True(t, ok)
	require.Equal(t, "because", decoded.Explanation)
	require.Len(t, decoded.Steps, 2)

	_, ok = decodeHydratedPlan(`{"steps":"bad"}`)
	require.False(t, ok)
	_, ok = decodeHydratedPlan(`{"output":"{}"}`)
	require.False(t, ok)
	_, ok = decodeHydratedPlan(`{`)
	require.False(t, ok)
	_, ok = decodeHydratedPlan(`{"steps":[{"id":"","content":"","status":"bad"}]}`)
	require.False(t, ok)

	summary := summarizeHydratedPlan(plan)
	require.Equal(t, envtypes.PlanSummary{
		Total:      4,
		Pending:    1,
		InProgress: 1,
		Completed:  1,
		Cancelled:  1,
	}, summary)
	require.Equal(t, "two", getActiveHydratedPlanStepID(plan))
	require.Empty(t, getActiveHydratedPlanStepID(envtypes.Plan{}))
	require.Equal(t, trace.PlanSummaryPayload{
		Total:      4,
		Pending:    1,
		InProgress: 1,
		Completed:  1,
		Cancelled:  1,
	}, hydratedPlanSummaryToTracePayload(summary))
	require.Equal(t, []trace.PlanStepPayload{
		{ID: "one", Content: "first", Status: "pending"},
		{ID: "two", Content: "second", Status: "in_progress"},
		{ID: "three", Content: "third", Status: "completed"},
		{ID: "four", Content: "fourth", Status: "cancelled"},
	}, hydratedPlanStepsToTracePayload(plan.Steps))
	require.Nil(t, hydratedPlanStepsToTracePayload(nil))

	turn := &Turn{plans: &planStoreStub{plan: plan}, sessionID: "session-1"}
	rendered := turn.renderPlanInstructions()
	require.Equal(t, "# Plan Context\n\n## Active Plan\n- [pending] first\n- [in_progress] second\n\n## Plan Update Reason\n\nbecause", rendered)
	require.NotContains(t, rendered, "third")
	require.Empty(t, (*Turn)(nil).renderPlanInstructions())
	require.False(t, (*Turn)(nil).hydratePlanFromMessages(nil))
}

func TestTurn_HydratePlanFromMessagesAndHistory(t *testing.T) {
	plans := &planStoreStub{}
	turn := &Turn{plans: plans, sessionID: "session-1"}

	ok := turn.hydratePlanFromMessages([]morphmsg.Message{
		{Role: morphmsg.RoleTool, Name: "other", Content: "{}"},
		{Role: morphmsg.RoleTool, Name: "plan_tool", Content: `{"steps":[{"id":"one","content":"first","status":"in_progress"}]}`},
	})

	require.True(t, ok)
	require.Equal(t, "session-1", plans.sessionID)
	require.Len(t, plans.plan.Steps, 1)

	store := &sessionStoreStub{
		messagesByOffset: map[int][]morphmsg.Message{
			0: {{Role: morphmsg.RoleTool, Name: "plan_tool", Content: `{"bad":true}`}},
			1: {{Role: morphmsg.RoleTool, Name: "plan_tool", Content: `{"steps":[{"id":"two","content":"second","status":"in_progress"}]}`}},
		},
	}
	turn = &Turn{plans: plans, sessionStore: store}
	ok, err := turn.hydratePlanFromHistory(context.Background(), "session-2")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, agentsession.DefaultID, plans.sessionID)
	require.Equal(t, "two", plans.plan.Steps[0].ID)

	turn = &Turn{sessionStore: &sessionStoreStub{err: context.Canceled}}
	ok, err = turn.hydratePlanFromHistory(context.Background(), "session-2")
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, ok)
}
