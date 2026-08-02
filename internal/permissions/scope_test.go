package permissions

import (
	"testing"

	"github.com/stretchr/testify/require"

	commandplan "github.com/xymorphic/morph/internal/command"
)

func TestPermissionScope_AllowsOnlyDeclaredOperations(t *testing.T) {
	scope := PermissionScope{
		Restricted:     true,
		Resources:      []Resource{ResourceFile},
		Actions:        []Action{ActionRead},
		Effects:        []Effect{EffectRead},
		TargetPrefixes: []string{"workspace/"},
	}

	require.True(t, scope.Allows(Operation{
		Resource: ResourceFile, Action: ActionRead, Effects: []Effect{EffectRead}, Target: "workspace/a.txt",
	}))
	require.False(t, scope.Allows(Operation{
		Resource: ResourceFile, Action: ActionUpdate, Effects: []Effect{EffectWrite}, Target: "workspace/a.txt",
	}))
	require.False(t, scope.Allows(Operation{
		Resource: ResourceFile, Action: ActionRead, Effects: []Effect{EffectRead}, Target: "private/a.txt",
	}))
	require.True(t, (PermissionScope{}).Allows(Operation{Resource: ResourceFile, Action: ActionRead}))
	require.False(t, (PermissionScope{Restricted: true}).Allows(Operation{Resource: ResourceFile, Action: ActionRead}))
	require.False(t, (PermissionScope{Restricted: true, Resources: []Resource{"bad"}}).Allows(Operation{}))
	require.False(t, scope.Allows(Operation{Resource: ResourceFile, Action: "bad"}))
	require.False(t, scope.Allows(Operation{
		Resource: ResourceFile, Action: ActionRead, Effects: []Effect{EffectWrite}, Target: "workspace/a.txt",
	}))
}

func TestPermissionScope_AllowsTypedCommandsAndFileTargetsInOneScope(t *testing.T) {
	scope := PermissionScope{
		Restricted: true,
		Resources:  []Resource{ResourceProcess, ResourceFile},
		Actions:    []Action{ActionExecute, ActionUpdate},
		Effects:    []Effect{EffectExecution, EffectWrite},
		Commands:   []commandplan.Selector{{Executable: "git", ArgumentPrefix: []string{"status"}}},
		TargetPrefixes: []string{
			"workspace/",
		},
	}
	command := commandplan.Target{
		Mode: commandplan.ModeDirect, Executable: "git", ResolvedPath: "/usr/bin/git",
		Arguments: []string{"status", "--short"}, PlanDigest: "plan", Complete: true,
	}

	require.True(t, scope.Allows(Operation{
		Resource: ResourceProcess, Action: ActionExecute, Effects: []Effect{EffectExecution}, Command: &command,
	}))
	require.True(t, scope.Allows(Operation{
		Resource: ResourceFile, Action: ActionUpdate, Effects: []Effect{EffectWrite}, Target: "workspace/out.txt",
	}))
	command.Arguments = []string{"push"}
	require.False(t, scope.Allows(Operation{
		Resource: ResourceProcess, Action: ActionExecute, Effects: []Effect{EffectExecution}, Command: &command,
	}))
}

func TestIntersectScopes_CommandSelectorsCannotWidenThroughEmptyBoundary(t *testing.T) {
	selector := commandplan.Selector{Executable: "git", ArgumentPrefix: []string{"status"}}
	base := PermissionScope{
		Restricted: true, Resources: []Resource{ResourceProcess}, Actions: []Action{ActionExecute},
		Effects: []Effect{EffectExecution},
	}
	withCommand := base
	withCommand.Commands = []commandplan.Selector{selector}

	intersection, err := IntersectScopes(base, withCommand)
	require.NoError(t, err)
	require.Empty(t, intersection.Commands)

	intersection, err = IntersectScopes(withCommand, base)
	require.NoError(t, err)
	require.Empty(t, intersection.Commands)
}

func TestPermissionScope_NormalizeRejectsInvalidConstraints(t *testing.T) {
	tests := []struct {
		name  string
		scope PermissionScope
		want  string
	}{
		{name: "action", scope: PermissionScope{Restricted: true, Actions: []Action{"bad"}}, want: "permission scope contains an invalid action"},
		{name: "effect", scope: PermissionScope{Restricted: true, Effects: []Effect{"bad"}}, want: "permission scope contains an invalid effect"},
		{name: "unrestricted constraints", scope: PermissionScope{Resources: []Resource{ResourceFile}}, want: "permission scope constraints require restricted mode"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.scope.Normalize()
			require.EqualError(t, err, test.want)
		})
	}
}

func TestIntersectScopes_NarrowsParentAndRejectsUnrestrictedDelegation(t *testing.T) {
	parent := PermissionScope{
		Restricted: true, Resources: []Resource{ResourceFile, ResourceProcess},
		Actions: []Action{ActionRead, ActionExecute}, Effects: []Effect{EffectRead, EffectExecution},
		TargetPrefixes: []string{"workspace/"},
	}
	delegated := PermissionScope{
		Restricted: true, Resources: []Resource{ResourceFile, ResourceNetwork},
		Actions: []Action{ActionRead}, Effects: []Effect{EffectRead},
		TargetPrefixes: []string{"workspace/docs/"},
	}

	intersection, err := IntersectScopes(parent, delegated)
	require.NoError(t, err)
	require.Equal(t, []Resource{ResourceFile}, intersection.Resources)
	require.Equal(t, []Action{ActionRead}, intersection.Actions)
	require.Equal(t, []Effect{EffectRead}, intersection.Effects)
	require.Equal(t, []string{"workspace/docs/"}, intersection.TargetPrefixes)

	intersection, err = IntersectScopes(PermissionScope{}, delegated)
	require.NoError(t, err)
	require.Equal(t, delegated, intersection)

	_, err = IntersectScopes(parent, PermissionScope{})
	require.EqualError(t, err, "delegated permission scope must be restricted")
	_, err = IntersectScopes(PermissionScope{Resources: []Resource{ResourceFile}}, delegated)
	require.EqualError(t, err, "permission scope constraints require restricted mode")
	_, err = IntersectScopes(PermissionScope{}, PermissionScope{Restricted: true, Actions: []Action{"bad"}})
	require.EqualError(t, err, "permission scope contains an invalid action")

	withoutParentPrefix, err := IntersectScopes(
		PermissionScope{Restricted: true, Resources: []Resource{ResourceFile}}, delegated,
	)
	require.NoError(t, err)
	require.Equal(t, delegated.TargetPrefixes, withoutParentPrefix.TargetPrefixes)
	withoutDelegatedPrefix, err := IntersectScopes(
		parent,
		PermissionScope{
			Restricted: true, Resources: []Resource{ResourceFile}, Actions: []Action{ActionRead}, Effects: []Effect{EffectRead},
		},
	)
	require.NoError(t, err)
	require.Equal(t, parent.TargetPrefixes, withoutDelegatedPrefix.TargetPrefixes)
	reversed, err := IntersectScopes(
		PermissionScope{
			Restricted: true, Resources: []Resource{ResourceFile}, Actions: []Action{ActionRead},
			Effects: []Effect{EffectRead}, TargetPrefixes: []string{"workspace/docs/"},
		},
		PermissionScope{
			Restricted: true, Resources: []Resource{ResourceFile}, Actions: []Action{ActionRead},
			Effects: []Effect{EffectRead}, TargetPrefixes: []string{"workspace/"},
		},
	)
	require.NoError(t, err)
	require.Equal(t, []string{"workspace/docs/"}, reversed.TargetPrefixes)
}

func TestDelegateAuthorization_BindsLineageAndScope(t *testing.T) {
	parent := AuthorizationContext{
		Actor: Actor{Kind: ActorLocalOwner, ID: "owner"}, Surface: SurfaceTUI,
		Profile: "default", SessionID: "session", RunID: "parent-run",
	}
	scope := PermissionScope{
		Restricted: true, Resources: []Resource{ResourceFile}, Actions: []Action{ActionRead},
		Effects: []Effect{EffectRead},
	}

	child, err := DelegateAuthorization(parent, "child", "child-run", scope)
	require.NoError(t, err)
	require.Equal(t, Actor{Kind: ActorSubagent, ID: "child"}, child.Actor)
	require.Equal(t, ActorLocalOwner, child.ParentActorKind)
	require.Equal(t, "owner", child.ParentActorID)
	require.Equal(t, "parent-run", child.ParentRunID)
	require.Equal(t, scope, child.Scope)

	_, err = DelegateAuthorization(parent, "", "child-run", scope)
	require.EqualError(t, err, "delegated actor and run ids are required")
	_, err = DelegateAuthorization(AuthorizationContext{}, "child", "run", scope)
	require.EqualError(t, err, "permission actor kind is invalid")
	_, err = DelegateAuthorization(parent, "child", "run", PermissionScope{})
	require.EqualError(t, err, "delegated permission scope must be restricted")
}

func TestPolicy_RestrictedScopeCannotBeBypassedByFullAccess(t *testing.T) {
	policy := Policy{Preset: PresetFullAccess}
	authorization := AuthorizationContext{
		Actor: Actor{Kind: ActorSubagent, ID: "child"}, Surface: SurfaceTUI,
		Scope: PermissionScope{
			Restricted: true, Resources: []Resource{ResourceFile}, Actions: []Action{ActionRead},
			Effects: []Effect{EffectRead},
		},
	}

	evaluation := policy.Evaluate(EvaluationInput{
		Authorization: authorization,
		Operation:     Operation{Resource: ResourceProcess, Action: ActionExecute, Effects: []Effect{EffectExecution}},
	})
	require.Equal(t, DecisionDeny, evaluation.Decision)
	require.Equal(t, ReasonScopeExceeded, evaluation.ReasonCode)
}

func TestAuthorizationContext_RejectsOrphanedParentIdentity(t *testing.T) {
	_, err := (AuthorizationContext{
		Actor: Actor{Kind: ActorLocalOwner}, Surface: SurfaceCLI, ParentActorID: "owner",
	}).Normalize()
	require.EqualError(t, err, "permission parent actor kind is required for parent identity")
}
