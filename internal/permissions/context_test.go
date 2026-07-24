package permissions

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContext_RoundTripsTrustedAuthorization(t *testing.T) {
	want := AuthorizationContext{
		Actor: Actor{Kind: ActorGatewayUser, ID: "123"}, SurfaceKind: SurfaceKindGateway, Surface: SurfaceTelegram,
	}

	var nilContext context.Context
	ctx := WithContext(nilContext, want)
	got, ok := FromContext(ctx)
	require.True(t, ok)
	require.Equal(t, want, got)
}

func TestContext_RejectsMissingOrInvalidAuthorization(t *testing.T) {
	var nilContext context.Context
	_, ok := FromContext(nilContext)
	require.False(t, ok)
	_, ok = FromContext(context.Background())
	require.False(t, ok)

	ctx := WithContext(context.Background(), AuthorizationContext{Actor: Actor{Kind: "owner"}, Surface: SurfaceCLI})
	_, ok = FromContext(ctx)
	require.False(t, ok)

	ctx = context.WithValue(context.Background(), authorizationContextKey{}, AuthorizationContext{
		Actor:   Actor{Kind: ActorLocalOwner},
		Surface: "terminal",
	})
	_, ok = FromContext(ctx)
	require.False(t, ok)
}

func TestContext_TracksFullAccessExecution(t *testing.T) {
	var nilContext context.Context
	require.False(t, HasFullAccess(nilContext))
	require.False(t, HasFullAccess(context.Background()))

	ctx := WithFullAccess(nilContext)

	require.True(t, HasFullAccess(ctx))
}

func TestContext_TracksPresetAndAuthorizedOperations(t *testing.T) {
	var nilContext context.Context
	_, ok := PresetFromContext(nilContext)
	require.False(t, ok)

	ctx := WithPreset(nilContext, PresetAskForApproval)
	preset, ok := PresetFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, PresetAskForApproval, preset)
	unchanged := WithPreset(ctx, "invalid")
	preset, ok = PresetFromContext(unchanged)
	require.True(t, ok)
	require.Equal(t, PresetAskForApproval, preset)

	operation := Operation{
		Resource:    ResourceFile,
		Action:      ActionUpdate,
		Target:      "../outside.txt",
		TargetScope: TargetScopeExternal,
	}
	ctx = WithAuthorizedOperations(ctx, []Operation{operation})
	require.True(t, IsOperationAuthorized(ctx, operation))
	require.False(t, IsOperationAuthorized(ctx, Operation{
		Resource:    ResourceFile,
		Action:      ActionRead,
		Target:      operation.Target,
		TargetScope: TargetScopeExternal,
	}))
	require.False(t, IsOperationAuthorized(nilContext, operation))
	require.False(t, IsOperationAuthorized(ctx, Operation{}))

	withoutOperations := WithAuthorizedOperations(nilContext, []Operation{{}})
	require.False(t, IsOperationAuthorized(withoutOperations, operation))
}

func TestIsExactOperationAuthorized_RequiresCompleteOperationMatch(t *testing.T) {
	operation := Operation{
		Tool: "browser", Resource: ResourceBrowser, Action: ActionUpdate,
		Effects: []Effect{EffectWrite, EffectExternalSystem}, Target: "profile/default/tab/one",
		OwnerID: "owner", OwnerRequired: true,
	}
	ctx := WithAuthorizedOperations(context.Background(), []Operation{operation})

	require.True(t, IsExactOperationAuthorized(ctx, operation))
	changed := operation
	changed.Effects = []Effect{EffectWrite}
	require.False(t, IsExactOperationAuthorized(ctx, changed))
	changed = operation
	changed.Tool = "another_tool"
	require.False(t, IsExactOperationAuthorized(ctx, changed))
	changed = operation
	changed.OwnerRequired = false
	require.False(t, IsExactOperationAuthorized(ctx, changed))
	var emptyContext context.Context
	require.False(t, IsExactOperationAuthorized(emptyContext, operation))
	require.False(t, IsExactOperationAuthorized(ctx, Operation{}))
}

func TestObserveDecision_NotifiesContextObserver(t *testing.T) {
	operation := Operation{Resource: ResourceNetwork, Action: ActionRead}
	evaluation := Evaluation{Decision: DecisionAllow, Rule: "allow network"}
	var observedOperation Operation
	var observedEvaluation Evaluation
	ctx := WithDecisionObserver(context.Background(), func(
		_ context.Context,
		operation Operation,
		evaluation Evaluation,
	) {
		observedOperation = operation
		observedEvaluation = evaluation
	})

	ObserveDecision(ctx, operation, evaluation)

	require.Equal(t, operation, observedOperation)
	require.Equal(t, evaluation, observedEvaluation)
}

func TestObserveDecision_HandlesMissingObserver(t *testing.T) {
	require.NotPanics(t, func() {
		ObserveDecision(context.Background(), Operation{}, Evaluation{})
	})
}
