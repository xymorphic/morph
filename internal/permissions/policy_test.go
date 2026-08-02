package permissions

import (
	"testing"

	"github.com/stretchr/testify/require"

	commandplan "github.com/wandxy/morph/internal/command"
)

func TestPolicy_NormalizeAppliesConservativeDefaults(t *testing.T) {
	policy := Policy{
		SurfaceDefaults: map[Surface]Decision{" CLI ": " ASK "},
		Rules: []Rule{{
			Name:           " owner writes ",
			Profiles:       []string{" work ", "work"},
			ActorKinds:     []ActorKind{" LOCAL_OWNER ", ActorLocalOwner, ""},
			ActorIDs:       []string{" 456 ", "123", "123", ""},
			SurfaceKinds:   []SurfaceKind{" LOCAL ", SurfaceKindLocal},
			Surfaces:       []Surface{" CLI ", SurfaceCLI},
			Tools:          []string{" write_file ", "write_file"},
			Resources:      []Resource{" FILE ", ResourceFile},
			Actions:        []Action{" UPDATE ", ActionUpdate},
			Effects:        []Effect{" WRITE ", EffectWrite},
			TargetPrefixes: []string{" workspace/ ", "workspace/", ""},
			Decision:       " ALLOW ",
			Reason:         " owner workspace write ",
		}},
	}

	policy.Normalize()
	require.Equal(t, DecisionDeny, policy.Default)
	require.Equal(t, map[Surface]Decision{SurfaceCLI: DecisionAsk}, policy.SurfaceDefaults)
	require.Equal(t, Rule{
		Name:           "owner writes",
		Profiles:       []string{"work"},
		ActorKinds:     []ActorKind{ActorLocalOwner},
		ActorIDs:       []string{"123", "456"},
		SurfaceKinds:   []SurfaceKind{SurfaceKindLocal},
		Surfaces:       []Surface{SurfaceCLI},
		Tools:          []string{"write_file"},
		Resources:      []Resource{ResourceFile},
		Actions:        []Action{ActionUpdate},
		Effects:        []Effect{EffectWrite},
		TargetPrefixes: []string{"workspace/"},
		Decision:       DecisionAllow,
		Reason:         "owner workspace write",
	}, policy.Rules[0])
	require.NoError(t, policy.Validate())
}

func TestPolicy_CommandRulesMatchTypedFactsOnly(t *testing.T) {
	policy := Policy{
		Default:             DecisionDeny,
		SurfaceKindDefaults: map[SurfaceKind]Decision{SurfaceKindLocal: DecisionDeny},
		Rules: []Rule{{
			Name:      "git status",
			Resources: []Resource{ResourceProcess},
			Actions:   []Action{ActionExecute},
			Commands: []commandplan.Selector{{
				Executable: "git", ArgumentPrefix: []string{"status"},
			}},
			Decision: DecisionAllow,
		}},
	}
	authorization := AuthorizationContext{
		Actor: Actor{Kind: ActorLocalOwner}, Surface: SurfaceCLI,
	}
	target := commandplan.Target{
		Mode: commandplan.ModeDirect, Executable: "git", ResolvedPath: "/usr/bin/git",
		Arguments: []string{"status", "--short"}, PlanDigest: "plan", Complete: true,
	}

	evaluation := policy.Evaluate(EvaluationInput{
		Authorization: authorization,
		Operation: Operation{
			Resource: ResourceProcess, Action: ActionExecute,
			Effects: []Effect{EffectExecution}, Command: &target,
		},
	})
	require.Equal(t, DecisionAllow, evaluation.Decision)

	target.Arguments = []string{"push"}
	evaluation = policy.Evaluate(EvaluationInput{
		Authorization: authorization,
		Operation: Operation{
			Resource: ResourceProcess, Action: ActionExecute,
			Effects: []Effect{EffectExecution}, Command: &target,
		},
	})
	require.Equal(t, DecisionDeny, evaluation.Decision)
}

func TestPolicy_RawTargetPrefixesCannotAuthorizeCommandTargets(t *testing.T) {
	policy := Policy{
		Default:             DecisionDeny,
		SurfaceKindDefaults: map[SurfaceKind]Decision{SurfaceKindLocal: DecisionDeny},
		Rules: []Rule{{
			Name: "raw command", Resources: []Resource{ResourceProcess},
			Actions: []Action{ActionExecute}, TargetPrefixes: []string{"git status"},
			Decision: DecisionAllow,
		}},
	}
	target := commandplan.Target{
		Mode: commandplan.ModeDirect, Executable: "git", ResolvedPath: "/usr/bin/git",
		Arguments: []string{"status"}, PlanDigest: "plan", Complete: true,
	}

	evaluation := policy.Evaluate(EvaluationInput{
		Authorization: AuthorizationContext{Actor: Actor{Kind: ActorLocalOwner}, Surface: SurfaceCLI},
		Operation: Operation{
			Resource: ResourceProcess, Action: ActionExecute,
			Effects: []Effect{EffectExecution}, Command: &target,
		},
	})

	require.Equal(t, DecisionDeny, evaluation.Decision)
}

func TestPolicy_NormalizeHandlesNilPolicyAndSortsDistinctValues(t *testing.T) {
	require.NotPanics(t, func() {
		(*Policy)(nil).Normalize()
	})

	policy := Policy{Rules: []Rule{{
		Name:             "actors",
		ActorKinds:       []ActorKind{ActorSubagent, ActorLocalOwner},
		ParentActorKinds: []ActorKind{ActorGatewayUser, ActorLocalOwner},
		Decision:         DecisionAllow,
	}}}
	policy.Normalize()
	require.Equal(t, []ActorKind{ActorLocalOwner, ActorSubagent}, policy.Rules[0].ActorKinds)
	require.Equal(t, []ActorKind{ActorGatewayUser, ActorLocalOwner}, policy.Rules[0].ParentActorKinds)
	require.Equal(t, map[SurfaceKind]Decision{
		SurfaceKindLocal:      DecisionAsk,
		SurfaceKindGateway:    DecisionDeny,
		SurfaceKindAutomation: DecisionDeny,
		SurfaceKindRPC:        DecisionDeny,
		SurfaceKindACP:        DecisionDeny,
	}, policy.SurfaceKindDefaults)
	require.Empty(t, policy.SurfaceDefaults)
}

func TestPolicy_ValidateRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name         string
		policy       Policy
		errorMessage string
	}{
		{
			name: "default",
			policy: Policy{
				Default: "prompt",
			},
			errorMessage: "permission default must be one of: allow, ask, deny",
		},
		{
			name: "surface kind",
			policy: Policy{
				SurfaceKindDefaults: map[SurfaceKind]Decision{"remote": DecisionAsk},
			},
			errorMessage: "permission surface kind default contains an invalid kind",
		},
		{
			name: "surface kind decision",
			policy: Policy{
				SurfaceKindDefaults: map[SurfaceKind]Decision{SurfaceKindLocal: "prompt"},
			},
			errorMessage: "permission surface kind default must be one of: allow, ask, deny",
		},
		{
			name: "surface",
			policy: Policy{
				SurfaceDefaults: map[Surface]Decision{"": DecisionAsk},
			},
			errorMessage: "permission surface default contains an invalid surface",
		},
		{
			name: "surface decision",
			policy: Policy{
				SurfaceDefaults: map[Surface]Decision{SurfaceCLI: "prompt"},
			},
			errorMessage: "permission surface default must be one of: allow, ask, deny",
		},
		{
			name: "rule name",
			policy: Policy{
				Rules: []Rule{
					{
						Decision: DecisionAllow,
					},
				},
			},
			errorMessage: "permission rule name is required",
		},
		{
			name: "rule decision",
			policy: Policy{
				Rules: []Rule{
					{
						Name:     "rule",
						Decision: "prompt",
					},
				},
			},
			errorMessage: "permission rule decision must be one of: allow, ask, deny",
		},
		{
			name: "rule actor",
			policy: Policy{
				Rules: []Rule{
					{
						Name:       "rule",
						Decision:   DecisionAllow,
						ActorKinds: []ActorKind{"owner"},
					},
				},
			},
			errorMessage: "permission rule contains an invalid actor",
		},
		{
			name: "rule parent actor",
			policy: Policy{
				Rules: []Rule{
					{
						Name:             "rule",
						Decision:         DecisionAllow,
						ParentActorKinds: []ActorKind{"owner"},
					},
				},
			},
			errorMessage: "permission rule contains an invalid parent actor",
		},
		{
			name: "rule surface kind",
			policy: Policy{
				Rules: []Rule{
					{
						Name:         "rule",
						Decision:     DecisionAllow,
						SurfaceKinds: []SurfaceKind{"remote"},
					},
				},
			},
			errorMessage: "permission rule contains an invalid surface kind",
		},
		{
			name: "rule resource",
			policy: Policy{
				Rules: []Rule{
					{
						Name:      "rule",
						Decision:  DecisionAllow,
						Resources: []Resource{"database"},
					},
				},
			},
			errorMessage: "permission rule contains an invalid resource",
		},
		{
			name: "rule action",
			policy: Policy{
				Rules: []Rule{
					{
						Name:     "rule",
						Decision: DecisionAllow,
						Actions:  []Action{"download"},
					},
				},
			},
			errorMessage: "permission rule contains an invalid action",
		},
		{
			name: "rule effect",
			policy: Policy{
				Rules: []Rule{
					{
						Name:     "rule",
						Decision: DecisionAllow,
						Effects:  []Effect{"unknown"},
					},
				},
			},
			errorMessage: "permission rule contains an invalid effect",
		},
		{
			name: "duplicate rule",
			policy: Policy{
				Rules: []Rule{
					{
						Name:     "rule",
						Decision: DecisionAllow,
					},
					{
						Name:     " rule ",
						Decision: DecisionDeny,
					},
				},
			},
			errorMessage: "permission rule names must be unique",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.EqualError(t, test.policy.Validate(), test.errorMessage)
		})
	}
}

func TestPolicy_EvaluateFullAccessBypassesPolicyApprovalAndOwnershipButNotHardDenial(t *testing.T) {
	policy := Policy{
		Preset: PresetFullAccess,
		Rules:  []Rule{{Name: "deny everything", Decision: DecisionDeny}},
	}
	input := EvaluationInput{
		Authorization: AuthorizationContext{Actor: Actor{Kind: ActorGatewayUser}, Surface: SurfaceSlack},
		Operation: Operation{
			Resource: ResourceConfiguration, Action: ActionUpdate,
			Effects: []Effect{EffectCredentialBearing}, OwnerRequired: true,
		},
		ApprovalReason: "confirmation normally required",
	}

	evaluation := policy.Evaluate(input)
	require.Equal(t, DecisionAllow, evaluation.Decision)
	require.Equal(t, ReasonFullAccess, evaluation.ReasonCode)
	require.Equal(t, PresetFullAccess, evaluation.Preset)
	require.Empty(t, evaluation.Rule)
	require.Empty(t, evaluation.Reason)

	input.HardDenyReason = "blocked by hard safety policy"
	evaluation = policy.Evaluate(input)
	require.Equal(t, DecisionDeny, evaluation.Decision)
	require.Equal(t, ReasonHardDeny, evaluation.ReasonCode)
	require.Equal(t, "blocked by hard safety policy", evaluation.Reason)
}

func TestPolicy_EvaluateUsesHardDenyThenRulePrecedence(t *testing.T) {
	policy := Policy{Rules: []Rule{
		{Name: "allow owner", Profiles: []string{"work"}, ActorKinds: []ActorKind{ActorLocalOwner}, Decision: DecisionAllow, Reason: "owner allowed"},
		{Name: "ask writes", Effects: []Effect{EffectWrite}, Decision: DecisionAsk, Reason: "confirm write"},
		{Name: "deny config", Resources: []Resource{ResourceConfiguration}, Decision: DecisionDeny, Reason: "config blocked"},
	}}
	input := EvaluationInput{
		Authorization: AuthorizationContext{Actor: Actor{Kind: ActorLocalOwner}, Surface: SurfaceCLI, Profile: "work"},
		Operation: Operation{
			Tool:     "config",
			Resource: ResourceConfiguration,
			Action:   ActionUpdate,
			Effects:  []Effect{EffectWrite},
		},
	}

	evaluation := policy.Evaluate(input)
	require.Equal(t, DecisionDeny, evaluation.Decision)
	require.Equal(t, ReasonRuleMatched, evaluation.ReasonCode)
	require.Equal(t, "deny config", evaluation.Rule)
	require.Equal(t, "config blocked", evaluation.Reason)
	require.Equal(t, PresetCustom, evaluation.Preset)

	input.HardDenyReason = "hard safety policy"
	evaluation = policy.Evaluate(input)
	require.Equal(t, DecisionDeny, evaluation.Decision)
	require.Equal(t, ReasonHardDeny, evaluation.ReasonCode)
	require.Equal(t, "hard safety policy", evaluation.Reason)
	require.Empty(t, evaluation.Rule)
}

func TestPolicy_EvaluateMatchesSpecificGatewayActorID(t *testing.T) {
	policy := Policy{
		Preset:  PresetCustom,
		Default: DecisionDeny,
		Rules: []Rule{{
			Name:       "allow telegram sender",
			ActorKinds: []ActorKind{ActorGatewayUser},
			ActorIDs:   []string{"123456789"},
			Surfaces:   []Surface{SurfaceTelegram},
			Decision:   DecisionAllow,
		}},
	}
	operation := Operation{
		Resource: ResourceMemory,
		Action:   ActionSearch,
		Effects:  []Effect{EffectRead},
	}

	allowed := policy.Evaluate(EvaluationInput{
		Authorization: AuthorizationContext{
			Actor: Actor{Kind: ActorGatewayUser, ID: "123456789"}, Surface: SurfaceTelegram,
		},
		Operation: operation,
	})
	require.Equal(t, DecisionAllow, allowed.Decision)
	require.Equal(t, "allow telegram sender", allowed.Rule)

	denied := policy.Evaluate(EvaluationInput{
		Authorization: AuthorizationContext{
			Actor: Actor{Kind: ActorGatewayUser, ID: "987654321"}, Surface: SurfaceTelegram,
		},
		Operation: operation,
	})
	require.Equal(t, DecisionDeny, denied.Decision)
	require.Empty(t, denied.Rule)
}

func TestPolicy_EvaluateRequiresApprovalAfterAllowButPreservesDeny(t *testing.T) {
	policy := Policy{Rules: []Rule{
		{Name: "allow commands", Resources: []Resource{ResourceProcess}, Decision: DecisionAllow},
		{Name: "deny destructive", Effects: []Effect{EffectDestructive}, Decision: DecisionDeny},
	}}
	input := EvaluationInput{
		Authorization: AuthorizationContext{Actor: Actor{Kind: ActorLocalOwner}, Surface: SurfaceCLI},
		Operation: Operation{
			Resource: ResourceProcess,
			Action:   ActionExecute,
			Effects:  []Effect{EffectExecution},
		},
		ApprovalReason: "command policy requires approval",
	}

	evaluation := policy.Evaluate(input)
	require.Equal(t, DecisionAsk, evaluation.Decision)
	require.Equal(t, ReasonApprovalRequired, evaluation.ReasonCode)
	require.Equal(t, "command policy requires approval", evaluation.Reason)
	require.Equal(t, PresetCustom, evaluation.Preset)

	input.Operation.Effects = append(input.Operation.Effects, EffectDestructive)
	evaluation = policy.Evaluate(input)
	require.Equal(t, DecisionDeny, evaluation.Decision)
	require.Equal(t, "deny destructive", evaluation.Rule)
}

func TestPolicy_EvaluateRequiresTypedOptInForIndirectCommandExecution(t *testing.T) {
	target := commandplan.Target{
		Mode: commandplan.ModeDirect, Executable: "make", ResolvedPath: "/usr/bin/make",
		Indirect: true, PlanDigest: "plan", Complete: true,
	}
	input := EvaluationInput{
		Authorization: AuthorizationContext{
			Actor: Actor{Kind: ActorLocalOwner}, Surface: SurfaceCLI,
		},
		Operation: Operation{
			Resource: ResourceProcess, Action: ActionExecute,
			Effects: []Effect{EffectExecution, EffectIndirectExecution}, Command: &target,
		},
	}

	broad := Policy{Rules: []Rule{{
		Name: "broad process allow", Resources: []Resource{ResourceProcess}, Decision: DecisionAllow,
	}}}
	evaluation := broad.Evaluate(input)
	require.Equal(t, DecisionAsk, evaluation.Decision)
	require.Equal(t, ReasonApprovalRequired, evaluation.ReasonCode)
	require.Equal(t, "indirect command execution requires explicit permission", evaluation.Reason)

	explicit := Policy{Rules: []Rule{{
		Name: "allow make",
		Commands: []commandplan.Selector{{
			Executable: "make", AllowIndirect: true,
		}},
		Decision: DecisionAllow,
	}}}
	evaluation = explicit.Evaluate(input)
	require.Equal(t, DecisionAllow, evaluation.Decision)
	require.Equal(t, "allow make", evaluation.Rule)
}

func TestPolicy_CommandOnlyRuleDoesNotMatchOtherTargetKinds(t *testing.T) {
	policy := Policy{
		Default:             DecisionDeny,
		SurfaceKindDefaults: map[SurfaceKind]Decision{},
		Rules: []Rule{{
			Name: "allow git",
			Commands: []commandplan.Selector{{
				Executable: "git",
			}},
			Decision: DecisionAllow,
		}},
	}
	authorization := AuthorizationContext{
		Actor: Actor{Kind: ActorLocalOwner}, Surface: SurfaceCLI,
	}

	fileEvaluation := policy.Evaluate(EvaluationInput{
		Authorization: authorization,
		Operation: Operation{
			Resource: ResourceFile, Action: ActionRead, Effects: []Effect{EffectRead}, Target: "git",
		},
	})
	require.Equal(t, DecisionDeny, fileEvaluation.Decision)

	network := NetworkTarget{
		Scheme: "https", Host: "example.com", Port: 443, Path: "/", Method: "GET",
		RequestClass: NetworkRequestNavigation,
	}
	networkEvaluation := policy.Evaluate(EvaluationInput{
		Authorization: authorization,
		Operation: Operation{
			Resource: ResourceNetwork, Action: ActionRead,
			Effects: []Effect{EffectRead, EffectNetwork}, Network: &network,
		},
	})
	require.Equal(t, DecisionDeny, networkEvaluation.Decision)
}

func TestPolicy_ValidateRejectsMixedCommandAndOtherTargetSelectors(t *testing.T) {
	tests := []Rule{
		{
			Name: "command and prefix",
			Commands: []commandplan.Selector{{
				Executable: "git",
			}},
			TargetPrefixes: []string{"git"},
			Decision:       DecisionAllow,
		},
		{
			Name: "command and network",
			Commands: []commandplan.Selector{{
				Executable: "git",
			}},
			Network:  []NetworkSelector{{Host: "example.com"}},
			Decision: DecisionAllow,
		},
	}

	for _, rule := range tests {
		policy := Policy{Rules: []Rule{rule}}
		require.EqualError(
			t,
			policy.Validate(),
			"permission rule cannot combine command selectors with other target selectors",
		)
	}
}

func TestPolicy_EvaluateEnforcesOwnerRequirement(t *testing.T) {
	policy := Policy{
		Default: DecisionAllow, SurfaceKindDefaults: map[SurfaceKind]Decision{},
	}
	operation := Operation{
		Resource: ResourceAutomation, Action: ActionUpdate, OwnerRequired: true, OwnerID: "owner-1",
	}

	evaluation := policy.Evaluate(EvaluationInput{
		Authorization: AuthorizationContext{Actor: Actor{Kind: ActorGatewayUser, ID: "other"}, Surface: SurfaceSlack},
		Operation:     operation,
	})
	require.Equal(t, DecisionDeny, evaluation.Decision)
	require.Equal(t, ReasonOwnerRequired, evaluation.ReasonCode)

	evaluation = policy.Evaluate(EvaluationInput{
		Authorization: AuthorizationContext{Actor: Actor{Kind: ActorGatewayUser, ID: "owner-1"}, Surface: SurfaceSlack},
		Operation:     operation,
	})
	require.Equal(t, DecisionAllow, evaluation.Decision)

	evaluation = policy.Evaluate(EvaluationInput{
		Authorization: AuthorizationContext{Actor: Actor{Kind: ActorLocalOwner}, Surface: SurfaceCLI},
		Operation: Operation{
			Resource:      ResourceAutomation,
			Action:        ActionCreate,
			OwnerRequired: true,
		},
	})
	require.Equal(t, DecisionAllow, evaluation.Decision)
}

func TestPolicy_EvaluateUsesSpecificRuleWithinDecisionClass(t *testing.T) {
	policy := Policy{Rules: []Rule{
		{Name: "broad ask", Effects: []Effect{EffectWrite}, Decision: DecisionAsk},
		{Name: "specific ask", ActorKinds: []ActorKind{ActorLocalOwner}, Tools: []string{"write_file"}, Resources: []Resource{ResourceFile}, Effects: []Effect{EffectWrite}, Decision: DecisionAsk},
		{Name: "same specificity later", ActorKinds: []ActorKind{ActorLocalOwner}, Tools: []string{"write_file"}, Resources: []Resource{ResourceFile}, Effects: []Effect{EffectWrite}, Decision: DecisionAsk},
	}}

	evaluation := policy.Evaluate(EvaluationInput{
		Authorization: AuthorizationContext{Actor: Actor{Kind: ActorLocalOwner}, Surface: SurfaceCLI},
		Operation: Operation{
			Tool:     "write_file",
			Resource: ResourceFile,
			Action:   ActionUpdate,
			Effects:  []Effect{EffectRead, EffectWrite},
		},
	})
	require.Equal(t, "same specificity later", evaluation.Rule)
}

func TestPolicy_EvaluateMatchesTargetAndFallsBackToDefaults(t *testing.T) {
	policy := Policy{
		Default:         DecisionDeny,
		SurfaceDefaults: map[Surface]Decision{SurfaceCLI: DecisionAsk},
		Rules: []Rule{{
			Name:           "workspace read",
			Resources:      []Resource{ResourceFile},
			Actions:        []Action{ActionRead},
			TargetPrefixes: []string{"workspace/", "project/"},
			Decision:       DecisionAllow,
		}},
	}
	authorization := AuthorizationContext{Actor: Actor{Kind: ActorLocalOwner}, Surface: SurfaceCLI}

	evaluation := policy.Evaluate(EvaluationInput{
		Authorization: authorization,
		Operation: Operation{
			Resource: ResourceFile,
			Action:   ActionRead,
			Effects:  []Effect{EffectRead},
			Target:   "workspace/main.go",
		},
	})
	require.Equal(t, DecisionAllow, evaluation.Decision)
	require.Equal(t, "workspace read", evaluation.Rule)

	evaluation = policy.Evaluate(EvaluationInput{
		Authorization: authorization,
		Operation: Operation{
			Resource: ResourceFile,
			Action:   ActionRead,
			Effects:  []Effect{EffectRead},
			Target:   "outside/main.go",
		},
	})
	require.Equal(t, DecisionAsk, evaluation.Decision)
	require.Equal(t, ReasonSurfaceDefault, evaluation.ReasonCode)

	evaluation = policy.Evaluate(EvaluationInput{
		Authorization: AuthorizationContext{Actor: Actor{Kind: ActorGatewayUser}, Surface: SurfaceSlack},
		Operation: Operation{
			Resource: ResourceFile,
			Action:   ActionRead,
			Effects:  []Effect{EffectRead},
		},
	})
	require.Equal(t, DecisionDeny, evaluation.Decision)
	require.Equal(t, ReasonSurfaceKindDefault, evaluation.ReasonCode)
}

func TestPolicy_EvaluateAppliesGatewayDefaultsAndExactOverridesToExtensibleSurfaces(t *testing.T) {
	discord := Surface("discord")
	policy := Policy{
		SurfaceDefaults: map[Surface]Decision{discord: DecisionAllow},
	}
	authorization := AuthorizationContext{
		Actor:       Actor{Kind: ActorGatewayUser, ID: "user-1"},
		SurfaceKind: SurfaceKindGateway,
		Surface:     discord,
	}
	operation := Operation{
		Resource: ResourceFile,
		Action:   ActionRead,
		Effects:  []Effect{EffectRead},
	}

	evaluation := policy.Evaluate(EvaluationInput{Authorization: authorization, Operation: operation})
	require.Equal(t, DecisionAllow, evaluation.Decision)
	require.Equal(t, ReasonSurfaceDefault, evaluation.ReasonCode)

	delete(policy.SurfaceDefaults, discord)
	evaluation = policy.Evaluate(EvaluationInput{Authorization: authorization, Operation: operation})
	require.Equal(t, DecisionDeny, evaluation.Decision)
	require.Equal(t, ReasonSurfaceKindDefault, evaluation.ReasonCode)

	policy.Rules = []Rule{{
		Name:         "allow gateway reads",
		SurfaceKinds: []SurfaceKind{SurfaceKindGateway},
		Effects:      []Effect{EffectRead},
		Decision:     DecisionAllow,
	}}
	evaluation = policy.Evaluate(EvaluationInput{Authorization: authorization, Operation: operation})
	require.Equal(t, DecisionAllow, evaluation.Decision)
	require.Equal(t, "allow gateway reads", evaluation.Rule)
}

func TestPolicy_EvaluateInvalidInputUsesUnknownDefaults(t *testing.T) {
	policy := Policy{Default: DecisionAsk}
	evaluation := policy.Evaluate(EvaluationInput{
		Authorization: AuthorizationContext{Actor: Actor{Kind: "root"}, Surface: "terminal"},
		Operation:     Operation{Resource: "database", Action: "query"},
	})
	require.Equal(t, DecisionAsk, evaluation.Decision)
	require.Equal(t, ReasonPolicyDefault, evaluation.ReasonCode)
}

func TestPolicy_EvaluateInvalidPolicyFailsSafe(t *testing.T) {
	policy := Policy{
		Default:         "permit",
		SurfaceDefaults: map[Surface]Decision{SurfaceCLI: "permit"},
		Rules: []Rule{{
			Name:     "invalid allow",
			Decision: "permit",
		}},
	}

	evaluation := policy.Evaluate(EvaluationInput{
		Authorization: AuthorizationContext{Actor: Actor{Kind: ActorLocalOwner}, Surface: SurfaceCLI},
		Operation: Operation{
			Resource: ResourceFile,
			Action:   ActionUpdate,
			Effects:  []Effect{EffectWrite},
		},
	})
	require.Equal(t, DecisionDeny, evaluation.Decision)
	require.Equal(t, ReasonPolicyDefault, evaluation.ReasonCode)
	require.Equal(t, PresetCustom, evaluation.Preset)
}

func TestPolicy_EvaluateDoesNotMutateConfiguredRules(t *testing.T) {
	policy := Policy{Rules: []Rule{{
		Name:     " owner read ",
		Profiles: []string{" work "},
		Tools:    []string{" read_file "},
		Decision: " ALLOW ",
	}}}

	evaluation := policy.Evaluate(EvaluationInput{
		Authorization: AuthorizationContext{
			Actor:   Actor{Kind: ActorLocalOwner},
			Surface: SurfaceCLI,
			Profile: "work",
		},
		Operation: Operation{
			Tool:     "read_file",
			Resource: ResourceFile,
			Action:   ActionRead,
		},
	})

	require.Equal(t, DecisionAllow, evaluation.Decision)
	require.Equal(t, " owner read ", policy.Rules[0].Name)
	require.Equal(t, []string{" work "}, policy.Rules[0].Profiles)
	require.Equal(t, []string{" read_file "}, policy.Rules[0].Tools)
	require.Equal(t, Decision(" ALLOW "), policy.Rules[0].Decision)
}

func TestPolicyHelpers_HandleNonMatchesAndUnknownDecision(t *testing.T) {
	rule := Rule{
		Profiles:     []string{"work"},
		ActorKinds:   []ActorKind{ActorLocalOwner},
		SurfaceKinds: []SurfaceKind{SurfaceKindLocal},
		Surfaces:     []Surface{SurfaceCLI},
		Tools:        []string{"write_file"},
		Resources:    []Resource{ResourceFile},
		Actions:      []Action{ActionUpdate},
		Effects:      []Effect{EffectWrite},
		Decision:     DecisionAllow,
	}
	base := AuthorizationContext{
		Actor: Actor{Kind: ActorLocalOwner}, SurfaceKind: SurfaceKindLocal, Surface: SurfaceCLI, Profile: "work",
	}
	operation := Operation{
		Tool:     "write_file",
		Resource: ResourceFile,
		Action:   ActionUpdate,
		Effects:  []Effect{EffectWrite},
	}
	require.True(t, rule.matches(base, operation))

	variations := []struct {
		authorization AuthorizationContext
		operation     Operation
	}{
		{
			authorization: AuthorizationContext{
				Actor: Actor{
					Kind: ActorLocalOwner,
				},
				SurfaceKind: SurfaceKindLocal,
				Surface:     SurfaceCLI,
				Profile:     "other",
			},
			operation: operation,
		},
		{
			authorization: AuthorizationContext{
				Actor: Actor{
					Kind: ActorGatewayUser,
				},
				SurfaceKind: SurfaceKindLocal,
				Surface:     SurfaceCLI,
				Profile:     "work",
			},
			operation: operation,
		},
		{
			authorization: AuthorizationContext{
				Actor: Actor{
					Kind: ActorLocalOwner,
				},
				SurfaceKind: SurfaceKindGateway,
				Surface:     SurfaceSlack,
				Profile:     "work",
			},
			operation: operation,
		},
		{
			authorization: base,
			operation: Operation{
				Tool:     "patch",
				Resource: ResourceFile,
				Action:   ActionUpdate,
				Effects:  []Effect{EffectWrite},
			},
		},
		{
			authorization: base,
			operation: Operation{
				Tool:     "write_file",
				Resource: ResourceMemory,
				Action:   ActionUpdate,
				Effects:  []Effect{EffectWrite},
			},
		},
		{
			authorization: base,
			operation: Operation{
				Tool:     "write_file",
				Resource: ResourceFile,
				Action:   ActionRead,
				Effects:  []Effect{EffectWrite},
			},
		},
		{
			authorization: base,
			operation: Operation{
				Tool:     "write_file",
				Resource: ResourceFile,
				Action:   ActionUpdate,
				Effects:  []Effect{EffectRead},
			},
		},
	}
	for _, variation := range variations {
		require.False(t, rule.matches(variation.authorization, variation.operation))
	}

	require.Equal(t, 0, getDecisionPriority("unknown"))
	require.False(t, matchesTargetPrefix([]string{"workspace/"}, "outside/file"))
}
