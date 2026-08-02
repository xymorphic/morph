package permissions

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEngine_CheckEnforcesDefaultDenial(t *testing.T) {
	engine := NewEngine(Policy{Default: DecisionDeny})

	evaluation, err := engine.Check(context.Background(), EvaluationInput{
		Operation: Operation{Resource: ResourceFile, Action: ActionUpdate},
	})

	require.Error(t, err)
	require.Equal(t, DecisionDeny, evaluation.Decision)
	require.Equal(t, PresetCustom, evaluation.Preset)
}

func TestEngine_CheckEnforcesDecisions(t *testing.T) {
	ctx := WithContext(context.Background(), AuthorizationContext{
		Actor: Actor{
			Kind: ActorLocalOwner,
		},
		Surface: SurfaceCLI,
	})
	tests := []struct {
		name     string
		decision Decision
		code     string
		message  string
	}{
		{
			name:     "deny",
			decision: DecisionDeny,
			code:     ErrorCodeDenied,
			message:  "blocked by policy",
		},
		{
			name:     "ask",
			decision: DecisionAsk,
			code:     ErrorCodeApprovalRequired,
			message:  "confirm this action",
		},
		{name: "allow", decision: DecisionAllow},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine := NewEngine(Policy{
				Rules: []Rule{
					{
						Name:       "decision",
						ActorKinds: []ActorKind{ActorLocalOwner},
						Decision:   test.decision,
						Reason:     test.message,
					},
				},
			})

			evaluation, err := engine.Check(ctx, EvaluationInput{
				Authorization: AuthorizationContext{Actor: Actor{Kind: ActorGatewayUser}, Surface: SurfaceSlack},
				Operation:     Operation{Resource: ResourceFile, Action: ActionUpdate},
			})

			require.Equal(t, test.decision, evaluation.Decision)
			if test.code == "" {
				require.NoError(t, err)
				return
			}
			decisionErr, ok := GetDecisionError(err)
			require.True(t, ok)
			require.Equal(t, test.code, decisionErr.Code)
			require.EqualError(t, err, test.message)
		})
	}
}

func TestEngine_CheckFullAccessBypassesPolicyButNotHardDenials(t *testing.T) {
	engine := NewEngine(Policy{
		Preset: PresetFullAccess,
		Rules:  []Rule{{Name: "deny everything", Decision: DecisionDeny}},
	})
	require.Equal(t, PresetFullAccess, engine.Preset(context.Background()))
	input := EvaluationInput{Operation: Operation{Resource: ResourceFile, Action: ActionUpdate}}

	evaluation, err := engine.Check(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, DecisionAllow, evaluation.Decision)
	require.Equal(t, ReasonFullAccess, evaluation.ReasonCode)
	require.Equal(t, PresetFullAccess, evaluation.Preset)

	input.HardDenyReason = "hard safety policy"
	_, err = engine.Check(context.Background(), input)
	require.Error(t, err)
	decisionErr, ok := GetDecisionError(err)
	require.True(t, ok)
	require.Equal(t, ErrorCodeDenied, decisionErr.Code)
}

func TestDecisionError_UsesSafeFallbackMessages(t *testing.T) {
	require.Empty(t, (*DecisionError)(nil).Error())
	require.Equal(t, "approval required", (&DecisionError{Code: ErrorCodeApprovalRequired}).Error())
	require.Equal(t, "permission denied", (&DecisionError{Code: ErrorCodeDenied}).Error())

	decisionErr, ok := GetDecisionError(errors.New("other"))
	require.False(t, ok)
	require.Nil(t, decisionErr)
}
