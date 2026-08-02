package plan_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/agent/runcontext"
	"github.com/xymorphic/morph/internal/environment"
	envtypes "github.com/xymorphic/morph/internal/environment/types"
	"github.com/xymorphic/morph/internal/guardrails"
	"github.com/xymorphic/morph/internal/permissions"
	storage "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/internal/tools"
	nativemocks "github.com/xymorphic/morph/internal/tools/mocks"
	plantool "github.com/xymorphic/morph/internal/tools/plan"
	"github.com/xymorphic/morph/internal/trace"
	"github.com/xymorphic/morph/pkg/nanoid"
)

func TestPlanTool_ResolvesReadAndUpdatePermissions(t *testing.T) {
	resolver := plantool.Definition(nil).ResolvePermission
	require.NotNil(t, resolver)

	tests := []struct {
		name      string
		input     string
		sessionID string
		want      permissions.Operation
	}{
		{
			name:      "read current plan",
			input:     " ",
			sessionID: "session-1",
			want: permissions.Operation{
				Resource: permissions.ResourcePlan,
				Action:   permissions.ActionRead,
				Effects:  []permissions.Effect{permissions.EffectRead},
				Target:   "session-1",
			},
		},
		{
			name:  "update default plan",
			input: `{"steps":[]}`,
			want: permissions.Operation{
				Resource: permissions.ResourcePlan,
				Action:   permissions.ActionUpdate,
				Effects:  []permissions.Effect{permissions.EffectRead, permissions.EffectWrite},
				Target:   "default",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := tools.WithSessionID(context.Background(), test.sessionID)
			inputs, err := resolver(ctx, tools.Call{Name: "plan_tool", Input: test.input})

			require.NoError(t, err)
			require.Equal(t, []permissions.EvaluationInput{{Operation: test.want}}, inputs)
		})
	}
}

func TestPlanTool_ResolverRejectsMalformedInput(t *testing.T) {
	resolver := plantool.Definition(nil).ResolvePermission

	inputs, err := resolver(context.Background(), tools.Call{Input: `{"steps":`})

	require.Nil(t, inputs)
	resolutionErr, ok := tools.GetPermissionResolutionError(err)
	require.True(t, ok)
	require.Equal(t, "invalid_input", resolutionErr.Code)
	require.Equal(t, "invalid tool input", resolutionErr.Message)
}

func TestPlanTool_ReadEmptyPlan(t *testing.T) {
	registry := registerPlanRuntime(t, t.TempDir(), guardrails.CommandPolicy{})

	result, err := registry.Invoke(tools.WithSessionID(context.Background(), "session-1"), tools.Call{Name: "plan_tool", Input: `{}`})
	require.NoError(t, err)

	payload := decodePlanOutputForTest(t, result.Output)
	require.Empty(t, payload.Steps)
	require.Equal(t, envtypes.PlanSummary{}, payload.Summary)
	require.Empty(t, payload.ActiveStepID)
}

func TestPlanTool_ReplacePlan(t *testing.T) {
	registry := registerPlanRuntime(t, t.TempDir(), guardrails.CommandPolicy{})

	result, err := registry.Invoke(tools.WithSessionID(context.Background(), "session-1"), tools.Call{
		Name: "plan_tool",
		Input: `{"steps":[{"id":"step-1","content":"Implement feature","status":"in_progress"},` +
			`{"id":"step-2","content":"Write tests","status":"pending"}],"explanation":"starting work"}`,
	})
	require.NoError(t, err)

	payload := decodePlanOutputForTest(t, result.Output)
	require.Len(t, payload.Steps, 2)
	require.Equal(t, "step-1", payload.ActiveStepID)
	require.Equal(t, "starting work", payload.Explanation)
	require.Equal(t, 1, payload.Summary.InProgress)
}

func TestPlanTool_UsesChildSessionIDForChildState(t *testing.T) {
	parentID := nanoid.MustFromSeed(storage.SessionIDPrefix, "parent", "PlanToolLineageTestSeed")
	childID := nanoid.MustFromSeed(storage.SessionIDPrefix, "child", "PlanToolLineageTestSeed")
	parent, err := runcontext.NewParent(parentID)
	require.NoError(t, err)
	child, err := parent.NewChild(runcontext.ChildOptions{
		ChildSessionID: childID,
		RunID:          "run_plan",
	})
	require.NoError(t, err)

	registry := registerPlanRuntime(t, t.TempDir(), guardrails.CommandPolicy{})
	_, err = registry.Invoke(tools.WithSessionID(context.Background(), parentID), tools.Call{
		Name:  "plan_tool",
		Input: `{"steps":[{"id":"parent","content":"Parent work","status":"in_progress"}]}`,
	})
	require.NoError(t, err)
	_, err = registry.Invoke(tools.WithRunContext(context.Background(), child), tools.Call{
		Name:  "plan_tool",
		Input: `{"steps":[{"id":"child","content":"Child work","status":"in_progress"}]}`,
	})
	require.NoError(t, err)

	parentResult, err := registry.Invoke(
		tools.WithSessionID(context.Background(), parentID),
		tools.Call{Name: "plan_tool", Input: `{}`},
	)
	require.NoError(t, err)
	childResult, err := registry.Invoke(
		tools.WithRunContext(context.Background(), child),
		tools.Call{Name: "plan_tool", Input: `{}`},
	)
	require.NoError(t, err)

	require.Equal(t, "parent", decodePlanOutputForTest(t, parentResult.Output).Steps[0].ID)
	require.Equal(t, "child", decodePlanOutputForTest(t, childResult.Output).Steps[0].ID)
}

func TestPlanTool_MergeStatusOnlyUpdate(t *testing.T) {
	registry := registerPlanRuntime(t, t.TempDir(), guardrails.CommandPolicy{})
	_, err := registry.Invoke(tools.WithSessionID(context.Background(), "session-1"), tools.Call{
		Name:  "plan_tool",
		Input: `{"steps":[{"id":"step-1","content":"Implement feature","status":"in_progress"},{"id":"step-2","content":"Write tests","status":"pending"}]}`,
	})
	require.NoError(t, err)

	result, err := registry.Invoke(tools.WithSessionID(context.Background(), "session-1"), tools.Call{
		Name:  "plan_tool",
		Input: `{"merge":true,"steps":[{"id":"step-1","status":"completed"},{"id":"step-2","status":"in_progress"}],"explanation":"shift active work"}`,
	})
	require.NoError(t, err)

	payload := decodePlanOutputForTest(t, result.Output)
	require.Equal(t, "step-2", payload.ActiveStepID)
	require.Equal(t, "completed", payload.Steps[0].Status)
	require.Equal(t, "in_progress", payload.Steps[1].Status)
	require.Equal(t, []trace.PlanToolChange{
		{Index: 1, ID: "step-1", Action: "completed", Fields: []string{"status"}},
		{Index: 2, ID: "step-2", Action: "updated", Fields: []string{"status"}},
	}, payload.Changes)
}

func TestPlanTool_MergeContentOnlyUpdateAndAppend(t *testing.T) {
	registry := registerPlanRuntime(t, t.TempDir(), guardrails.CommandPolicy{})
	_, err := registry.Invoke(tools.WithSessionID(context.Background(), "session-1"), tools.Call{
		Name:  "plan_tool",
		Input: `{"steps":[{"id":"step-1","content":"Implement feature","status":"in_progress"}]}`,
	})
	require.NoError(t, err)

	result, err := registry.Invoke(tools.WithSessionID(context.Background(), "session-1"), tools.Call{
		Name:  "plan_tool",
		Input: `{"merge":true,"steps":[{"id":"step-1","content":"Implement feature thoroughly"},{"id":"step-2","content":"Write tests","status":"pending"}]}`,
	})
	require.NoError(t, err)

	payload := decodePlanOutputForTest(t, result.Output)
	require.Equal(t, "Implement feature thoroughly", payload.Steps[0].Content)
	require.Equal(t, "in_progress", payload.Steps[0].Status)
	require.Equal(t, "step-2", payload.Steps[1].ID)
	require.Equal(t, []trace.PlanToolChange{
		{Index: 1, ID: "step-1", Action: "updated", Fields: []string{"content"}},
		{Index: 2, ID: "step-2", Action: "added"},
	}, payload.Changes)
}

func TestPlanTool_ClearCompletedRemovesTerminalSteps(t *testing.T) {
	registry := registerPlanRuntime(t, t.TempDir(), guardrails.CommandPolicy{})

	result, err := registry.Invoke(tools.WithSessionID(context.Background(), "session-1"), tools.Call{
		Name:  "plan_tool",
		Input: `{"steps":[{"id":"step-1","content":"Done","status":"completed"},{"id":"step-2","content":"Active","status":"in_progress"}],"clear_completed":true}`,
	})
	require.NoError(t, err)

	payload := decodePlanOutputForTest(t, result.Output)
	require.Len(t, payload.Steps, 1)
	require.Equal(t, "step-2", payload.Steps[0].ID)
}

func TestPlanTool_RejectsInvalidWrites(t *testing.T) {
	registry := registerPlanRuntime(t, t.TempDir(), guardrails.CommandPolicy{})

	testCases := []string{
		`{"steps":[{"id":"dup","content":"A","status":"in_progress"},{"id":"dup","content":"B","status":"pending"}]}`,
		`{"steps":[{"id":"step-1","content":"","status":"in_progress"}]}`,
		`{"steps":[{"id":"step-1","content":"A","status":"bad"}]}`,
		`{"steps":[{"id":"step-1","content":"A","status":"pending"},{"id":"step-2","content":"B","status":"pending"}]}`,
		`{"merge":true,"steps":[{"id":"step-1","status":"bad"}]}`,
	}

	for _, input := range testCases {
		result, err := registry.Invoke(tools.WithSessionID(context.Background(), "session-1"), tools.Call{Name: "plan_tool", Input: input})
		require.NoError(t, err)
		require.Contains(t, result.Error, `"code":"invalid_input"`)
	}
}

func TestPlanTool_RejectsMalformedJSON(t *testing.T) {
	registry := registerPlanRuntime(t, t.TempDir(), guardrails.CommandPolicy{})
	result, err := registry.Invoke(tools.WithSessionID(context.Background(), "session-1"), tools.Call{Name: "plan_tool", Input: `{"steps":`})
	require.NoError(t, err)
	require.Contains(t, result.Error, `"code":"invalid_input"`)
}

func TestPlanTool_ReturnsInvalidInputWhenMergeDependencyFails(t *testing.T) {
	registry := newPlanFailureRegistry(t, t.TempDir(), guardrails.CommandPolicy{}, errors.New("merge failed"), nil)

	result, err := registry.Invoke(tools.WithSessionID(context.Background(), "session-1"), tools.Call{
		Name:  "plan_tool",
		Input: `{"merge":true,"steps":[{"id":"step-1","content":"Implement feature","status":"in_progress"}]}`,
	})
	require.NoError(t, err)
	requireInvalidInputError(t, result, "merge failed")
}

func TestPlanTool_ReturnsInvalidInputWhenReplaceDependencyFails(t *testing.T) {
	registry := newPlanFailureRegistry(t, t.TempDir(), guardrails.CommandPolicy{}, nil, errors.New("replace failed"))

	result, err := registry.Invoke(tools.WithSessionID(context.Background(), "session-1"), tools.Call{
		Name:  "plan_tool",
		Input: `{"steps":[{"id":"step-1","content":"Implement feature","status":"in_progress"}]}`,
	})
	require.NoError(t, err)
	requireInvalidInputError(t, result, "replace failed")
}

func TestPlanTool_DefaultsToDefaultSessionWhenContextHasNoSessionID(t *testing.T) {
	registry := registerPlanRuntime(t, t.TempDir(), guardrails.CommandPolicy{})

	result, err := registry.Invoke(context.Background(), tools.Call{
		Name:  "plan_tool",
		Input: `{"steps":[{"id":"step-1","content":"Implement feature","status":"in_progress"}]}`,
	})
	require.NoError(t, err)

	payload := decodePlanOutputForTest(t, result.Output)
	require.Equal(t, "step-1", payload.ActiveStepID)

	result, err = registry.Invoke(context.Background(), tools.Call{Name: "plan_tool", Input: `{}`})
	require.NoError(t, err)
	payload = decodePlanOutputForTest(t, result.Output)
	require.Len(t, payload.Steps, 1)
	require.Equal(t, "step-1", payload.Steps[0].ID)

	result, err = registry.Invoke(tools.WithSessionID(context.Background(), "session-1"), tools.Call{Name: "plan_tool", Input: `{}`})
	require.NoError(t, err)
	payload = decodePlanOutputForTest(t, result.Output)
	require.Empty(t, payload.Steps)
}

func TestPlanTool_RecordsPlanUpdatedAndClearedEvents(t *testing.T) {
	registry := registerPlanRuntime(t, t.TempDir(), guardrails.CommandPolicy{})
	traceSession := &nativemocks.TraceRecorder{}
	ctx := tools.WithTraceRecorder(tools.WithSessionID(context.Background(), "session-1"), traceSession)

	result, err := registry.Invoke(ctx, tools.Call{
		Name:  "plan_tool",
		Input: `{"steps":[{"id":"step-1","content":"Done","status":"completed"}],"clear_completed":true,"explanation":"cleanup"}`,
	})
	require.NoError(t, err)

	payload := decodePlanOutputForTest(t, result.Output)
	require.Empty(t, payload.Steps)
	require.Len(t, traceSession.Events, 3)
	require.Equal(t, trace.EvtPermissionDecisionObserved, traceSession.Events[0].Type)
	require.Equal(t, trace.EvtPlanUpdated, traceSession.Events[1].Type)
	require.Equal(t, trace.EvtPlanCleared, traceSession.Events[2].Type)
	updatedPayload, ok := traceSession.Events[1].Payload.(trace.PlanEventPayload)
	require.True(t, ok)
	require.Empty(t, updatedPayload.Changes)
}

func registerPlanRuntime(t *testing.T, root string, policy guardrails.CommandPolicy) tools.Registry {
	t.Helper()
	registry := tools.NewDefaultRegistry()
	runtime := environment.NewRuntime([]string{root}, policy, nil)
	require.NoError(t, registry.RegisterGroup(tools.Group{Name: "core"}))
	require.NoError(t, registry.Register(plantool.Definition(runtime)))
	return registry
}

func newPlanFailureRegistry(t *testing.T, root string, policy guardrails.CommandPolicy, mergeErr, replaceErr error) tools.Registry {
	t.Helper()
	registry := tools.NewDefaultRegistry()
	runtime := &nativemocks.FailingPlanRuntime{
		Runtime:    environment.NewRuntime([]string{root}, policy, nil),
		MergeErr:   mergeErr,
		ReplaceErr: replaceErr,
	}
	require.NoError(t, registry.RegisterGroup(tools.Group{Name: "core"}))
	require.NoError(t, registry.Register(plantool.Definition(runtime)))
	return registry
}

func requireInvalidInputError(t *testing.T, result tools.Result, message string) {
	t.Helper()
	require.Contains(t, result.Error, `"code":"invalid_input"`)
	require.Contains(t, result.Error, `"message":"`+message+`"`)
}

type planToolOutput struct {
	Steps        []envtypes.PlanStep    `json:"steps"`
	Summary      envtypes.PlanSummary   `json:"summary"`
	ActiveStepID string                 `json:"active_step_id"`
	Explanation  string                 `json:"explanation"`
	Changes      []trace.PlanToolChange `json:"changes"`
}

func decodePlanOutputForTest(t *testing.T, raw string) planToolOutput {
	t.Helper()
	var payload planToolOutput
	require.NoError(t, json.Unmarshal([]byte(raw), &payload))
	return payload
}
