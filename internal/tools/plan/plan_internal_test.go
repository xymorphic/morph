package plan

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	envtypes "github.com/xymorphic/morph/internal/environment/types"
	"github.com/xymorphic/morph/internal/trace"
)

func TestDecodePlanSteps_RejectsMissingID(t *testing.T) {
	_, err := decodePlanSteps([]map[string]any{{
		"content": "Write tests",
		"status":  envtypes.PlanStatusPending,
	}})
	require.EqualError(t, err, "step id is required")
}

func TestDecodePartialPlanSteps_RejectsInvalidUpdates(t *testing.T) {
	testCases := []struct {
		name  string
		steps []map[string]any
		want  string
	}{
		{name: "missing id", steps: []map[string]any{{}}, want: "step id is required"},
		{
			name: "duplicate id",
			steps: []map[string]any{
				{"id": "step-1"},
				{"id": "step-1"},
			},
			want: "step ids must be unique",
		},
		{name: "non-string content", steps: []map[string]any{{"id": "step-1", "content": 1}}, want: "step content is required"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := decodePartialPlanSteps(testCase.steps)
			require.EqualError(t, err, testCase.want)
		})
	}
}

func TestPlanHelpers_CoverChangeAndPayloadVariants(t *testing.T) {
	before := envtypes.Plan{Steps: []envtypes.PlanStep{
		{ID: "same", Content: "Same", Status: envtypes.PlanStatusPending},
		{ID: "cancel", Content: "Cancel", Status: envtypes.PlanStatusInProgress},
		{ID: "remove", Content: "Remove", Status: envtypes.PlanStatusPending},
	}}
	after := envtypes.Plan{Steps: []envtypes.PlanStep{
		{ID: "same", Content: "Same", Status: envtypes.PlanStatusPending},
		{ID: "cancel", Content: "Cancelled", Status: envtypes.PlanStatusCancelled},
		{ID: "add", Content: "Added", Status: envtypes.PlanStatusInProgress},
	}}

	changes := getPlanChanges(before, after)
	require.Equal(t, []trace.PlanToolChange{
		{Index: 2, ID: "cancel", Action: "cancelled", Fields: []string{"status", "content"}},
		{Index: 3, ID: "add", Action: "added"},
		{Index: 3, ID: "remove", Action: "removed"},
	}, changes)
	require.Nil(t, getPlanChanges(before, before))

	summary := summarizePlan(envtypes.Plan{Steps: []envtypes.PlanStep{
		{Status: envtypes.PlanStatusPending},
		{Status: envtypes.PlanStatusInProgress},
		{Status: envtypes.PlanStatusCompleted},
		{Status: envtypes.PlanStatusCancelled},
	}})
	require.Equal(t, envtypes.PlanSummary{Total: 4, Pending: 1, InProgress: 1, Completed: 1, Cancelled: 1}, summary)
	require.Equal(t, "add", getActivePlanStepID(after))
	require.Empty(t, getActivePlanStepID(envtypes.Plan{}))
	require.Nil(t, planStepsToTracePayload(nil))
	require.Equal(t, []trace.PlanStepPayload{{ID: "add", Content: "Added", Status: "in_progress"}},
		planStepsToTracePayload(after.Steps[2:]))
	require.Equal(t, trace.PlanSummaryPayload{Total: 4, Pending: 1, InProgress: 1, Completed: 1, Cancelled: 1},
		planSummaryToTracePayload(summary))
}

func TestRecordPlanEvents_IgnoreMissingRecorder(t *testing.T) {
	plan := envtypes.Plan{Steps: []envtypes.PlanStep{{ID: "step-1", Content: "Work", Status: envtypes.PlanStatusPending}}}

	require.NotPanics(t, func() {
		recordPlanEvent(context.Background(), "session-1", plan, nil)
		recordPlanCleared(context.Background(), "session-1", plan)
	})
}

func TestEncodePlanOutput_IncludesChanges(t *testing.T) {
	result, err := encodePlanOutput(
		envtypes.Plan{Steps: []envtypes.PlanStep{{ID: "step-1", Content: "Work", Status: envtypes.PlanStatusInProgress}}},
		[]trace.PlanToolChange{{Index: 1, ID: "step-1", Action: "added"}},
	)
	require.NoError(t, err)
	require.Contains(t, result.Output, `"changes"`)
	require.Contains(t, result.Output, `"active_step_id":"step-1"`)
}
