package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	commandplan "github.com/xymorphic/morph/internal/command"
	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/internal/state/storememory"
	"github.com/xymorphic/morph/internal/trace"
)

type testDefaultRegistry struct {
	*DefaultRegistry
}

func newTestDefaultRegistry(options ...RegistryOptions) *testDefaultRegistry {
	return &testDefaultRegistry{DefaultRegistry: NewDefaultRegistry(options...)}
}

func (r *testDefaultRegistry) Register(definition Definition) error {
	if definition.Permission.IsZero() && definition.ResolvePermission == nil && definition.PreparePermission == nil {
		definition.Permission = permissions.Operation{
			Resource: permissions.ResourceClock,
			Action:   permissions.ActionRead,
		}
	}
	if definition.SemanticIndex.Mode == SemanticIndexUnset {
		definition.SemanticIndex = SkipSemanticIndex()
	}

	return r.DefaultRegistry.Register(definition)
}

func TestDefaultRegistry_InvokeCarriesPreparedValueIntoMatchingHandlerCall(t *testing.T) {
	registry := newTestDefaultRegistry(RegistryOptions{
		PermissionPolicy: permissions.Policy{Preset: permissions.PresetFullAccess},
	})
	call := Call{Name: "prepared", Input: `{"value":"one"}`}
	require.NoError(t, registry.Register(Definition{
		Name: "prepared",
		PreparePermission: func(context.Context, Call) (PermissionPreparation, error) {
			return PermissionPreparation{
				Inputs: []permissions.EvaluationInput{{Operation: permissions.Operation{
					Resource: permissions.ResourceClock, Action: permissions.ActionRead,
				}}},
				Prepared: "prepared-value",
			}, nil
		},
		Handler: HandlerFunc(func(ctx context.Context, received Call) (Result, error) {
			value, ok := GetPreparedCall(ctx, received)
			require.True(t, ok)
			require.Equal(t, "prepared-value", value)
			_, mismatched := GetPreparedCall(ctx, Call{Name: received.Name, Input: `{"value":"two"}`})
			require.False(t, mismatched)
			return Result{Output: "ok"}, nil
		}),
	}))

	result, err := registry.Invoke(context.Background(), call)

	require.NoError(t, err)
	require.Equal(t, "ok", result.Output)
}

func TestPreparedCall_BindsToolAndInput(t *testing.T) {
	call := Call{Name: "prepared", Input: `{"value":"one"}`}
	ctx := withPreparedCall(context.Background(), call.Name, call.Input, "value")

	value, ok := GetPreparedCall(ctx, call)
	require.True(t, ok)
	require.Equal(t, "value", value)

	_, ok = GetPreparedCall(ctx, Call{Name: "other", Input: call.Input})
	require.False(t, ok)

	var nilContext context.Context
	ctx = withPreparedCall(nilContext, call.Name, call.Input, "value")
	value, ok = GetPreparedCall(ctx, call)
	require.True(t, ok)
	require.Equal(t, "value", value)
	_, ok = GetPreparedCall(nilContext, call)
	require.False(t, ok)
	_, ok = GetPreparedCall(context.Background(), call)
	require.False(t, ok)
}

func TestDefaultRegistry_InvokeRejectsEmptyPreparedPermissionSet(t *testing.T) {
	registry := newTestDefaultRegistry()
	require.NoError(t, registry.Register(Definition{
		Name: "prepared",
		PreparePermission: func(context.Context, Call) (PermissionPreparation, error) {
			return PermissionPreparation{Prepared: "unused"}, nil
		},
		Handler: HandlerFunc(func(context.Context, Call) (Result, error) {
			t.Fatal("handler must not run without a permission operation")
			return Result{}, nil
		}),
	}))

	result, err := registry.Invoke(context.Background(), Call{Name: "prepared"})

	require.NoError(t, err)
	require.Contains(t, result.Error, "permission preparer returned no operations")
}

func TestDefaultRegistry_RegisterAndGet(t *testing.T) {
	registry := newTestDefaultRegistry()
	definition := Definition{
		Name: "echo",
		Handler: HandlerFunc(func(context.Context, Call) (Result, error) {
			return Result{Output: "ok"}, nil
		}),
	}

	require.NoError(t, registry.Register(definition))

	loaded, ok := registry.Get("echo")
	require.True(t, ok)
	require.Equal(t, "echo", loaded.Name)
	require.Nil(t, loaded.Groups)
}

func TestDefaultRegistry_InvokeCallsHand(t *testing.T) {
	registry := newTestDefaultRegistry()
	require.NoError(t, registry.Register(Definition{
		Name: "echo",
		Handler: HandlerFunc(func(_ context.Context, call Call) (Result, error) {
			return Result{Output: call.Input}, nil
		}),
	}))

	result, err := registry.Invoke(context.Background(), Call{Name: "echo", Input: "hello"})

	require.NoError(t, err)
	require.Equal(t, "hello", result.Output)
}

func TestDefaultRegistry_InvokeProjectsSuccessfulSemanticContent(t *testing.T) {
	registry := newTestDefaultRegistry()
	require.NoError(t, registry.Register(Definition{
		Name: "read",
		SemanticIndex: ProjectSemanticIndex(
			ProjectJSONFieldsForSemanticIndex("path", "content"),
		),
		Handler: HandlerFunc(func(context.Context, Call) (Result, error) {
			return Result{Output: `{"path":"notes.md","content":"useful text","bytes":11}`}, nil
		}),
	}))

	result, err := registry.Invoke(context.Background(), Call{Name: "read"})

	require.NoError(t, err)
	require.JSONEq(t, `{"path":"notes.md","content":"useful text","bytes":11}`, result.Output)
	require.Equal(t, "content: useful text\npath: notes.md", result.SemanticContent)
}

func TestDefaultRegistry_InvokeDoesNotProjectToolErrors(t *testing.T) {
	registry := newTestDefaultRegistry()
	projected := false
	require.NoError(t, registry.Register(Definition{
		Name: "read",
		SemanticIndex: ProjectSemanticIndex(func(Call, Result) string {
			projected = true
			return "unexpected"
		}),
		Handler: HandlerFunc(func(context.Context, Call) (Result, error) {
			return Result{Error: Error{Code: "failed", Message: "failed"}.String()}, nil
		}),
	}))

	result, err := registry.Invoke(context.Background(), Call{Name: "read"})

	require.NoError(t, err)
	require.NotEmpty(t, result.Error)
	require.Empty(t, result.SemanticContent)
	require.False(t, projected)
}

func TestDefaultRegistry_ListReturnsSortedDefinitions(t *testing.T) {
	registry := newTestDefaultRegistry()
	handler := HandlerFunc(func(context.Context, Call) (Result, error) {
		return Result{}, nil
	})

	require.NoError(t, registry.Register(Definition{Name: "zeta", Handler: handler}))
	require.NoError(t, registry.Register(Definition{Name: "alpha", Handler: handler}))

	definitions := registry.List()

	require.Len(t, definitions, 2)
	require.Equal(t, []string{"alpha", "zeta"}, definitions.Names())
}

func TestDefaultRegistry_RejectsInvalidDefinitions(t *testing.T) {
	registry := newTestDefaultRegistry()

	require.EqualError(t, registry.Register(Definition{}), "tool name is required")
	require.EqualError(t, registry.Register(Definition{Name: "echo"}), "tool handler is required")
	require.EqualError(t, registry.RegisterGroup(Group{}), "tool group name is required")
}

func TestDefaultRegistry_RegisterRequiresPermissionDeclaration(t *testing.T) {
	registry := NewDefaultRegistry()
	handler := HandlerFunc(func(context.Context, Call) (Result, error) { return Result{}, nil })

	require.EqualError(
		t,
		registry.Register(Definition{Name: "unprotected", Handler: handler}),
		"tool permission declaration is required",
	)
	require.NoError(t, registry.Register(Definition{
		Name:          "static",
		Handler:       handler,
		SemanticIndex: SkipSemanticIndex(),
		Permission: permissions.Operation{
			Resource: permissions.ResourceClock,
			Action:   permissions.ActionRead,
		},
	}))
	require.NoError(t, registry.Register(Definition{
		Name:          "dynamic",
		Handler:       handler,
		SemanticIndex: SkipSemanticIndex(),
		ResolvePermission: func(context.Context, Call) ([]permissions.EvaluationInput, error) {
			return []permissions.EvaluationInput{{Operation: permissions.Operation{
				Resource: permissions.ResourceClock,
				Action:   permissions.ActionRead,
			}}}, nil
		},
	}))
}

func TestDefaultRegistry_RegisterRequiresSemanticIndexPolicy(t *testing.T) {
	registry := NewDefaultRegistry()
	definition := Definition{
		Name: "echo",
		Permission: permissions.Operation{
			Resource: permissions.ResourceClock,
			Action:   permissions.ActionRead,
		},
		Handler: HandlerFunc(func(context.Context, Call) (Result, error) { return Result{}, nil }),
	}

	require.EqualError(t, registry.Register(definition), "semantic index policy is required")
	definition.SemanticIndex = ProjectSemanticIndex(nil)
	require.EqualError(t, registry.Register(definition), "semantic index projector is required")
	definition.SemanticIndex = SemanticIndexPolicy{Mode: SemanticIndexMode("invalid")}
	require.EqualError(t, registry.Register(definition), "semantic index policy is invalid")
	definition.SemanticIndex = SemanticIndexPolicy{
		Mode:    SemanticIndexSkip,
		Project: func(Call, Result) string { return "unexpected" },
	}
	require.EqualError(t, registry.Register(definition), "semantic index skip policy must not define a projector")
}

func TestProjectJSONFieldsForSemanticIndex_ProjectsNestedScalarsDeterministically(t *testing.T) {
	project := ProjectJSONFieldsForSemanticIndex("title", "score", "active")

	content := project(Call{}, Result{Output: `{
		"items":[{"score":2,"title":" Second "},{"active":true,"ignored":"value"}],
		"title":"First"
	}`})

	require.Equal(t, "score: 2\ntitle: Second\nactive: true\ntitle: First", content)
	require.Empty(t, project(Call{}, Result{Output: "not-json"}))
}

func TestDefaultRegistry_RejectsDuplicateTools(t *testing.T) {
	registry := newTestDefaultRegistry()
	handler := HandlerFunc(func(context.Context, Call) (Result, error) {
		return Result{}, nil
	})

	require.NoError(t, registry.Register(Definition{Name: "echo", Handler: handler}))
	require.EqualError(t, registry.Register(Definition{Name: "echo", Handler: handler}), "tool is already registered")
}

func TestDefaultRegistry_RejectsDuplicateToolGroups(t *testing.T) {
	registry := newTestDefaultRegistry()
	require.NoError(t, registry.RegisterGroup(Group{Name: "core"}))
	require.EqualError(t, registry.RegisterGroup(Group{Name: "core"}), "tool group is already registered")
}

func TestDefaultRegistry_InvokeReturnsNormalizedUnknownToolError(t *testing.T) {
	registry := newTestDefaultRegistry()

	result, err := registry.Invoke(context.Background(), Call{Name: "missing"})

	require.NoError(t, err)
	require.Equal(t, Error{Code: "tool_not_registered", Message: "tool is not registered"}.String(), result.Error)
}

func TestDefaultRegistry_MutationsRejectNilRegistry(t *testing.T) {
	var registry *DefaultRegistry
	handler := HandlerFunc(func(context.Context, Call) (Result, error) {
		return Result{}, nil
	})

	require.EqualError(t, registry.Register(Definition{Name: "echo", Handler: handler}), "tool registry is required")
	require.EqualError(t, registry.RegisterGroup(Group{Name: "core"}), "tool registry is required")
}

func TestDefaultRegistry_GetHandlesNilRegistry(t *testing.T) {
	var registry *DefaultRegistry

	definition, ok := registry.Get("echo")
	require.False(t, ok)
	require.Equal(t, Definition{}, definition)

	group, ok := registry.GetGroup("core")
	require.False(t, ok)
	require.Equal(t, Group{}, group)
}

func TestDefaultRegistry_ListHandlesNilRegistry(t *testing.T) {
	var registry *DefaultRegistry

	require.Nil(t, registry.List())
	require.Nil(t, registry.ListGroups())
}

func TestDefaultRegistry_GetTrimsName(t *testing.T) {
	registry := newTestDefaultRegistry()
	require.NoError(t, registry.Register(Definition{
		Name: "echo",
		Handler: HandlerFunc(func(context.Context, Call) (Result, error) {
			return Result{}, nil
		}),
	}))

	definition, ok := registry.Get("  echo  ")

	require.True(t, ok)
	require.Equal(t, "echo", definition.Name)
}

func TestDefaultRegistry_RegisterAndGetGroup(t *testing.T) {
	registry := newTestDefaultRegistry()
	require.NoError(t, registry.RegisterGroup(Group{
		Name:     " core ",
		Tools:    []string{" echo ", "", "echo"},
		Includes: []string{" shared ", "shared", " "},
	}))

	group, ok := registry.GetGroup("core")

	require.True(t, ok)
	require.Equal(t, "core", group.Name)
	require.Equal(t, []string{"echo"}, group.Tools)
	require.Equal(t, []string{"shared"}, group.Includes)
}

func TestDefaultRegistry_ListGroupsReturnsSortedGroups(t *testing.T) {
	registry := newTestDefaultRegistry()
	require.NoError(t, registry.RegisterGroup(Group{Name: "zeta"}))
	require.NoError(t, registry.RegisterGroup(Group{Name: "alpha"}))

	groups := registry.ListGroups()

	require.Equal(t, []Group{{Name: "alpha"}, {Name: "zeta"}}, groups)
}

func TestDefaultRegistry_ResolveReturnsAllToolsWhenNoGroupsRequested(t *testing.T) {
	registry := newTestDefaultRegistry()
	handler := HandlerFunc(func(context.Context, Call) (Result, error) { return Result{}, nil })
	require.NoError(t, registry.Register(Definition{Name: "beta", Handler: handler}))
	require.NoError(t, registry.Register(Definition{Name: "alpha", Handler: handler}))

	definitions, err := registry.Resolve(Policy{})

	require.NoError(t, err)
	require.Equal(t, []string{"alpha", "beta"}, definitions.Names())
}

func TestDefaultRegistry_ResolveByGroupMembership(t *testing.T) {
	registry := newTestDefaultRegistry()
	handler := HandlerFunc(func(context.Context, Call) (Result, error) { return Result{}, nil })
	require.NoError(t, registry.Register(Definition{Name: "echo", Groups: []string{"core"}, Handler: handler}))
	require.NoError(t, registry.Register(Definition{Name: "search", Handler: handler}))
	require.NoError(t, registry.RegisterGroup(Group{Name: "core"}))

	definitions, err := registry.Resolve(Policy{GroupNames: []string{"core"}})

	require.NoError(t, err)
	require.Len(t, definitions, 1)
	require.Equal(t, "echo", definitions[0].Name)
}

func TestDefaultRegistry_ResolveThroughIncludedGroups(t *testing.T) {
	registry := newTestDefaultRegistry()
	handler := HandlerFunc(func(context.Context, Call) (Result, error) { return Result{}, nil })
	require.NoError(t, registry.Register(Definition{Name: "echo", Groups: []string{"core"}, Handler: handler}))
	require.NoError(t, registry.RegisterGroup(Group{Name: "core"}))
	require.NoError(t, registry.RegisterGroup(Group{Name: "coding", Includes: []string{"core"}}))

	definitions, err := registry.Resolve(Policy{GroupNames: []string{"coding"}})

	require.NoError(t, err)
	require.Len(t, definitions, 1)
	require.Equal(t, "echo", definitions[0].Name)
}

func TestDefaultRegistry_ResolveDeduplicatesTools(t *testing.T) {
	registry := newTestDefaultRegistry()
	handler := HandlerFunc(func(context.Context, Call) (Result, error) { return Result{}, nil })
	require.NoError(t, registry.Register(Definition{Name: "echo", Groups: []string{"core"}, Handler: handler}))
	require.NoError(t, registry.RegisterGroup(Group{Name: "core"}))
	require.NoError(t, registry.RegisterGroup(Group{Name: "coding", Tools: []string{"echo"}, Includes: []string{"core"}}))

	definitions, err := registry.Resolve(Policy{GroupNames: []string{"coding"}})

	require.NoError(t, err)
	require.Len(t, definitions, 1)
	require.Equal(t, "echo", definitions[0].Name)
}

func TestDefaultRegistry_ResolveSharedIncludedGroupOnceAndSortsTools(t *testing.T) {
	registry := newTestDefaultRegistry()
	handler := HandlerFunc(func(context.Context, Call) (Result, error) { return Result{}, nil })
	require.NoError(t, registry.Register(Definition{Name: "zeta", Handler: handler}))
	require.NoError(t, registry.Register(Definition{Name: "alpha", Handler: handler}))
	require.NoError(t, registry.RegisterGroup(Group{Name: "shared", Tools: []string{"zeta", "alpha"}}))
	require.NoError(t, registry.RegisterGroup(Group{Name: "first", Includes: []string{"shared"}}))
	require.NoError(t, registry.RegisterGroup(Group{Name: "second", Includes: []string{"shared"}}))

	definitions, err := registry.Resolve(Policy{GroupNames: []string{"second", "first"}})

	require.NoError(t, err)
	require.Equal(t, []string{"alpha", "zeta"}, definitions.Names())
}

func TestDefaultRegistry_ResolveRejectsNilRegistry(t *testing.T) {
	var registry *DefaultRegistry

	definitions, err := registry.Resolve(Policy{})

	require.EqualError(t, err, "tool registry is required")
	require.Nil(t, definitions)
}

func TestDefaultRegistry_ResolveRejectsMissingReferencedTool(t *testing.T) {
	registry := newTestDefaultRegistry()
	require.NoError(t, registry.RegisterGroup(Group{Name: "core", Tools: []string{"missing"}}))

	_, err := registry.Resolve(Policy{GroupNames: []string{"core"}})

	require.EqualError(t, err, "tool ('missing') referenced by group is not registered")
}

func TestDefaultRegistry_ResolveRejectsMissingIncludedGroup(t *testing.T) {
	registry := newTestDefaultRegistry()
	require.NoError(t, registry.RegisterGroup(Group{Name: "core", Includes: []string{"missing"}}))

	_, err := registry.Resolve(Policy{GroupNames: []string{"core"}})

	require.EqualError(t, err, "tool group ('missing') is not registered")
}

func TestDefaultRegistry_ResolveRejectsGroupCycles(t *testing.T) {
	registry := newTestDefaultRegistry()
	require.NoError(t, registry.RegisterGroup(Group{Name: "core", Includes: []string{"coding"}}))
	require.NoError(t, registry.RegisterGroup(Group{Name: "coding", Includes: []string{"core"}}))

	_, err := registry.Resolve(Policy{GroupNames: []string{"core"}})

	require.EqualError(t, err, "tool group ('core') cycle detected")
}

func TestDefaultRegistry_ResolveFiltersByCapabilities(t *testing.T) {
	registry := newTestDefaultRegistry()
	handler := HandlerFunc(func(context.Context, Call) (Result, error) { return Result{}, nil })
	require.NoError(t, registry.Register(Definition{Name: "read_file", Requires: Capabilities{Filesystem: true}, Handler: handler}))

	definitions, err := registry.Resolve(Policy{})
	require.NoError(t, err)
	require.Empty(t, definitions)

	definitions, err = registry.Resolve(Policy{Capabilities: Capabilities{Filesystem: true}})
	require.NoError(t, err)
	require.Len(t, definitions, 1)
	require.Equal(t, "read_file", definitions[0].Name)
}

func TestDefaultRegistry_ResolveFiltersByPlatform(t *testing.T) {
	registry := newTestDefaultRegistry()
	handler := HandlerFunc(func(context.Context, Call) (Result, error) { return Result{}, nil })
	require.NoError(t, registry.Register(Definition{Name: "browser", Platforms: []string{"desktop"}, Handler: handler}))
	require.NoError(t, registry.Register(Definition{Name: "time", Handler: handler}))

	definitions, err := registry.Resolve(Policy{Platform: "desktop"})
	require.NoError(t, err)
	require.Len(t, definitions, 2)

	definitions, err = registry.Resolve(Policy{Platform: "slack"})
	require.NoError(t, err)
	require.Len(t, definitions, 1)
	require.Equal(t, "time", definitions[0].Name)
}

func TestDefaultRegistry_InvokeNormalizesHandlerErrors(t *testing.T) {
	registry := newTestDefaultRegistry()
	require.NoError(t, registry.Register(Definition{
		Name: "echo",
		Handler: HandlerFunc(func(context.Context, Call) (Result, error) {
			return Result{}, context.DeadlineExceeded
		}),
	}))

	result, err := registry.Invoke(context.Background(), Call{Name: "echo"})

	require.NoError(t, err)
	require.Equal(t, Error{Code: "tool_invocation_failed", Message: context.DeadlineExceeded.Error()}.String(), result.Error)
}

func TestDefaultRegistry_InvokeNormalizesResultErrors(t *testing.T) {
	registry := newTestDefaultRegistry()
	require.NoError(t, registry.Register(Definition{
		Name: "echo",
		Handler: HandlerFunc(func(context.Context, Call) (Result, error) {
			return Result{Error: "failed"}, nil
		}),
	}))

	result, err := registry.Invoke(context.Background(), Call{Name: "echo"})

	require.NoError(t, err)
	require.Equal(t, Error{Code: "tool_failed", Message: "failed"}.String(), result.Error)
}

func TestDefaultRegistry_InvokePreservesStructuredResultErrors(t *testing.T) {
	registry := newTestDefaultRegistry()
	expected := Error{
		Code:      "rate_limited",
		Message:   "retry later",
		Retryable: true,
	}
	raw, err := json.Marshal(expected)
	require.NoError(t, err)

	require.NoError(t, registry.Register(Definition{
		Name: "echo",
		Handler: HandlerFunc(func(context.Context, Call) (Result, error) {
			return Result{Error: string(raw)}, nil
		}),
	}))

	result, err := registry.Invoke(context.Background(), Call{Name: "echo"})

	require.NoError(t, err)
	require.Equal(t, string(raw), result.Error)
}

func TestDefaultRegistry_RegisterNormalizesPermissionMetadata(t *testing.T) {
	registry := newTestDefaultRegistry()
	require.NoError(t, registry.Register(Definition{
		Name: "write",
		Permission: permissions.Operation{
			Resource: " FILE ",
			Action:   " UPDATE ",
			Effects:  []permissions.Effect{" WRITE ", permissions.EffectWrite},
		},
		Handler: HandlerFunc(func(context.Context, Call) (Result, error) { return Result{}, nil }),
	}))

	definition, ok := registry.Get("write")
	require.True(t, ok)
	require.Equal(t, permissions.Operation{
		Tool:     "write",
		Resource: permissions.ResourceFile,
		Action:   permissions.ActionUpdate,
		Effects:  []permissions.Effect{permissions.EffectWrite},
	}, definition.Permission)
}

func TestDefaultRegistry_RegisterRejectsInvalidPermissionMetadata(t *testing.T) {
	registry := newTestDefaultRegistry()
	err := registry.Register(Definition{
		Name:       "write",
		Permission: permissions.Operation{Resource: "database", Action: permissions.ActionUpdate},
		Handler:    HandlerFunc(func(context.Context, Call) (Result, error) { return Result{}, nil }),
	})

	require.EqualError(t, err, "permission resource is invalid")
}

func TestDefaultRegistry_InvokeEnforcesPermissionDecision(t *testing.T) {
	called := false
	registry := newTestDefaultRegistry(RegistryOptions{PermissionPolicy: permissions.Policy{
		Default: permissions.DecisionDeny,
		Rules: []permissions.Rule{{
			Name:       "ask owner writes",
			ActorKinds: []permissions.ActorKind{permissions.ActorLocalOwner},
			Effects:    []permissions.Effect{permissions.EffectWrite},
			Decision:   permissions.DecisionAsk,
		}},
	}})
	require.NoError(t, registry.Register(Definition{
		Name: "write",
		Permission: permissions.Operation{
			Resource: permissions.ResourceFile,
			Action:   permissions.ActionUpdate,
			Effects:  []permissions.Effect{permissions.EffectWrite},
		},
		Handler: HandlerFunc(func(context.Context, Call) (Result, error) {
			called = true
			return Result{Output: "written"}, nil
		}),
	}))
	recorder := &permissionTraceRecorder{}
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor:   permissions.Actor{Kind: permissions.ActorLocalOwner},
		Surface: permissions.SurfaceCLI,
	})
	ctx = WithTraceRecorder(ctx, recorder)

	result, err := registry.Invoke(ctx, Call{
		Name:  "write",
		Input: `{"actor":"local_owner","surface":"cli"}`,
	})

	require.NoError(t, err)
	require.False(t, called)
	var toolErr Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, permissions.ErrorCodeApprovalRequired, toolErr.Code)
	require.Equal(t, []permissionTraceEvent{{
		eventType: trace.EvtPermissionDecisionObserved,
		payload: trace.PermissionDecisionPayload{
			ActorKind:     string(permissions.ActorLocalOwner),
			SurfaceKind:   string(permissions.SurfaceKindLocal),
			Surface:       string(permissions.SurfaceCLI),
			Tool:          "write",
			Resource:      string(permissions.ResourceFile),
			Action:        string(permissions.ActionUpdate),
			Effects:       []string{string(permissions.EffectWrite)},
			Decision:      string(permissions.DecisionAsk),
			ReasonCode:    permissions.ReasonRuleMatched,
			Rule:          "ask owner writes",
			Preset:        string(permissions.PresetCustom),
			OwnerRequired: false,
		},
	}}, recorder.events)
}

func TestDefaultRegistry_InvokeRecordsRedactedCommandFacts(t *testing.T) {
	registry := newTestDefaultRegistry(RegistryOptions{PermissionPolicy: permissions.Policy{
		Default:             permissions.DecisionAllow,
		SurfaceKindDefaults: map[permissions.SurfaceKind]permissions.Decision{},
	}})
	target := commandplan.Target{
		Mode: commandplan.ModeDirect, Executable: "/private/secret/git",
		ResolvedPath: "/private/secret/git", Arguments: []string{"secret"},
		PlanDigest: "plan", Complete: false,
		DynamicReasons: []commandplan.DynamicReason{
			commandplan.ReasonDynamicArgument,
		},
		InvocationCount: 2,
		RedirectCount:   1,
	}
	require.NoError(t, registry.Register(Definition{
		Name: "run_command",
		ResolvePermission: func(context.Context, Call) ([]permissions.EvaluationInput, error) {
			return []permissions.EvaluationInput{{Operation: permissions.Operation{
				Resource: permissions.ResourceProcess, Action: permissions.ActionExecute,
				Effects: []permissions.Effect{permissions.EffectExecution}, Command: &target,
			}}}, nil
		},
		Handler: HandlerFunc(func(context.Context, Call) (Result, error) {
			return Result{Output: "done"}, nil
		}),
	}))
	recorder := &permissionTraceRecorder{}
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{Kind: permissions.ActorLocalOwner}, Surface: permissions.SurfaceCLI,
	})
	ctx = WithTraceRecorder(ctx, recorder)

	result, err := registry.Invoke(ctx, Call{Name: "run_command"})

	require.NoError(t, err)
	require.Equal(t, "done", result.Output)
	require.Len(t, recorder.events, 1)
	require.Equal(t, &trace.PermissionCommandTargetPayload{
		Mode:            string(commandplan.ModeDirect),
		Executable:      "git",
		InvocationCount: 2,
		RedirectCount:   1,
		Complete:        false,
		DynamicReasons:  []string{string(commandplan.ReasonDynamicArgument)},
	}, recorder.events[0].payload.Command)
}

func TestDefaultRegistry_InvokeRecordsUnknownActor(t *testing.T) {
	registry := NewDefaultRegistry(RegistryOptions{PermissionPolicy: permissions.Policy{
		Default: permissions.DecisionDeny,
	}})
	handler := HandlerFunc(func(context.Context, Call) (Result, error) { return Result{Output: "ok"}, nil })
	require.NoError(t, registry.Register(Definition{
		Name:          "write",
		Permission:    permissions.Operation{Resource: permissions.ResourceFile, Action: permissions.ActionUpdate},
		SemanticIndex: SkipSemanticIndex(),
		Handler:       handler,
	}))
	recorder := &permissionTraceRecorder{}
	ctx := WithTraceRecorder(context.Background(), recorder)

	_, err := registry.Invoke(ctx, Call{Name: "write"})
	require.NoError(t, err)

	require.Len(t, recorder.events, 1)
	payload := recorder.events[0].payload
	require.Equal(t, string(permissions.ActorUnknown), payload.ActorKind)
	require.Equal(t, string(permissions.SurfaceKindUnknown), payload.SurfaceKind)
	require.Equal(t, string(permissions.SurfaceUnknown), payload.Surface)
	require.Equal(t, string(permissions.DecisionDeny), payload.Decision)
	require.Equal(t, permissions.ReasonPolicyDefault, payload.ReasonCode)
}

func TestDefaultRegistry_InvokeRecordsSafeStructuredNetworkTarget(t *testing.T) {
	target, err := permissions.NetworkTargetFromURL(
		"https://example.com/news?token=secret", "GET", permissions.NetworkRequestNavigation,
	)
	require.NoError(t, err)
	registry := newTestDefaultRegistry(RegistryOptions{PermissionPolicy: permissions.Policy{
		Default: permissions.DecisionAllow,
	}})
	require.NoError(t, registry.Register(Definition{
		Name: "network_read",
		Permission: permissions.Operation{
			Resource: permissions.ResourceNetwork, Action: permissions.ActionRead,
			Effects: []permissions.Effect{permissions.EffectRead, permissions.EffectNetwork}, Network: &target,
		},
		Handler: HandlerFunc(func(context.Context, Call) (Result, error) {
			return Result{Output: "ok"}, nil
		}),
	}))
	recorder := &permissionTraceRecorder{}
	ctx := WithTraceRecorder(context.Background(), recorder)

	result, err := registry.Invoke(ctx, Call{Name: "network_read"})

	require.NoError(t, err)
	require.Equal(t, "ok", result.Output)
	require.Len(t, recorder.events, 1)
	payload := recorder.events[0].payload
	require.Equal(t, &trace.PermissionNetworkTargetPayload{
		Scheme: "https", Host: "example.com", Port: 443, Path: "/news",
		Method: "GET", RequestClass: "navigation", HasQuery: true,
	}, payload.Network)
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "secret")
	require.NotContains(t, string(raw), target.QueryHash)
}

func TestGetPermissionNetworkTracePayload_OmitsCredentialBearingPath(t *testing.T) {
	target, err := permissions.NetworkTargetFromURL(
		"https://example.com/users/private-id?token=secret", "POST", permissions.NetworkRequestSubresource,
	)
	require.NoError(t, err)

	payload := getPermissionNetworkTracePayload(&target, true)

	require.Equal(t, "example.com", payload.Host)
	require.Empty(t, payload.Path)
	require.True(t, payload.HasQuery)
}

func TestDefaultRegistry_InvokeClassifiedToolWithoutRecorder(t *testing.T) {
	registry := newTestDefaultRegistry()
	require.NoError(t, registry.Register(Definition{
		Name:       "read",
		Permission: permissions.Operation{Resource: permissions.ResourceFile, Action: permissions.ActionRead},
		Handler: HandlerFunc(func(context.Context, Call) (Result, error) {
			return Result{Output: "contents"}, nil
		}),
	}))

	result, err := registry.Invoke(context.Background(), Call{Name: "read"})

	require.NoError(t, err)
	require.Equal(t, "contents", result.Output)
}

func TestDefaultRegistry_InvokeEnforcesDenyAndAskBeforeHandler(t *testing.T) {
	tests := []struct {
		name     string
		decision permissions.Decision
		code     string
	}{
		{
			name:     "deny",
			decision: permissions.DecisionDeny,
			code:     permissions.ErrorCodeDenied,
		},
		{
			name:     "ask",
			decision: permissions.DecisionAsk,
			code:     permissions.ErrorCodeApprovalRequired,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			registry := newTestDefaultRegistry(RegistryOptions{PermissionPolicy: permissions.Policy{
				Rules: []permissions.Rule{
					{
						Name:     test.name,
						Decision: test.decision,
						Reason:   test.name + " reason",
					},
				},
			}})
			require.NoError(t, registry.Register(Definition{
				Name:       "write",
				Permission: permissions.Operation{Resource: permissions.ResourceFile, Action: permissions.ActionUpdate},
				Handler: HandlerFunc(func(context.Context, Call) (Result, error) {
					called = true
					return Result{Output: "written"}, nil
				}),
			}))
			ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
				Actor: permissions.Actor{Kind: permissions.ActorLocalOwner}, Surface: permissions.SurfaceCLI,
			})

			result, err := registry.Invoke(ctx, Call{Name: "write"})

			require.NoError(t, err)
			require.False(t, called)
			var toolErr Error
			require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
			require.Equal(t, test.code, toolErr.Code)
			require.Equal(t, test.name+" reason", toolErr.Message)
		})
	}
}

func TestDefaultRegistry_InvokeEnforceAllowsHandlerAndRecordsResolvedOperation(t *testing.T) {
	called := false
	registry := newTestDefaultRegistry(RegistryOptions{PermissionPolicy: permissions.Policy{
		Rules: []permissions.Rule{{
			Name: "allow target", Actions: []permissions.Action{permissions.ActionDelete},
			TargetPrefixes: []string{"memory-"}, Decision: permissions.DecisionAllow,
		}},
	}})
	require.NoError(t, registry.Register(Definition{
		Name:       "memory",
		Permission: permissions.Operation{Resource: permissions.ResourceMemory, Action: permissions.ActionManage},
		ResolvePermission: func(context.Context, Call) ([]permissions.EvaluationInput, error) {
			return []permissions.EvaluationInput{{Operation: permissions.Operation{
				Resource: permissions.ResourceMemory,
				Action:   permissions.ActionDelete,
				Effects:  []permissions.Effect{permissions.EffectDestructive},
				Target:   "memory-1",
			}}}, nil
		},
		Handler: HandlerFunc(func(context.Context, Call) (Result, error) {
			called = true
			return Result{Output: "deleted"}, nil
		}),
	}))
	recorder := &permissionTraceRecorder{}
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{Kind: permissions.ActorLocalOwner}, Surface: permissions.SurfaceCLI,
	})
	ctx = WithTraceRecorder(ctx, recorder)

	result, err := registry.Invoke(ctx, Call{Name: "memory"})

	require.NoError(t, err)
	require.True(t, called)
	require.Equal(t, "deleted", result.Output)
	require.Len(t, recorder.events, 1)
	require.Equal(t, string(permissions.ActionDelete), recorder.events[0].payload.Action)
	require.Equal(t, []string{string(permissions.EffectDestructive)}, recorder.events[0].payload.Effects)
	require.Equal(t, string(permissions.PresetCustom), recorder.events[0].payload.Preset)
}

func TestDefaultRegistry_InvokeFullAccessBypassesPolicyAndApproval(t *testing.T) {
	called := false
	registry := newTestDefaultRegistry(RegistryOptions{PermissionPolicy: permissions.Policy{
		Preset: permissions.PresetFullAccess,
		Rules:  []permissions.Rule{{Name: "deny writes", Decision: permissions.DecisionDeny}},
	}})
	require.NoError(t, registry.Register(Definition{
		Name:       "write",
		Permission: permissions.Operation{Resource: permissions.ResourceFile, Action: permissions.ActionUpdate},
		ResolvePermission: func(context.Context, Call) ([]permissions.EvaluationInput, error) {
			return []permissions.EvaluationInput{{
				Operation:      permissions.Operation{Resource: permissions.ResourceFile, Action: permissions.ActionUpdate},
				ApprovalReason: "normally requires approval",
			}}, nil
		},
		Handler: HandlerFunc(func(ctx context.Context, _ Call) (Result, error) {
			require.True(t, permissions.HasFullAccess(ctx))
			called = true
			return Result{Output: "written"}, nil
		}),
	}))
	recorder := &permissionTraceRecorder{}
	ctx := WithTraceRecorder(context.Background(), recorder)

	result, err := registry.Invoke(ctx, Call{Name: "write"})

	require.NoError(t, err)
	require.True(t, called)
	require.Equal(t, "written", result.Output)
	require.Len(t, recorder.events, 1)
	require.Equal(t, string(permissions.DecisionAllow), recorder.events[0].payload.Decision)
	require.Equal(t, permissions.ReasonFullAccess, recorder.events[0].payload.ReasonCode)
	require.Equal(t, string(permissions.PresetFullAccess), recorder.events[0].payload.Preset)
}

func TestDefaultRegistry_InvokeFullAccessPreservesHardDenial(t *testing.T) {
	called := false
	registry := newTestDefaultRegistry(RegistryOptions{
		PermissionPolicy: permissions.Policy{Preset: permissions.PresetFullAccess},
	})
	require.NoError(t, registry.Register(Definition{
		Name:       "write",
		Permission: permissions.Operation{Resource: permissions.ResourceFile, Action: permissions.ActionUpdate},
		ResolvePermission: func(context.Context, Call) ([]permissions.EvaluationInput, error) {
			return []permissions.EvaluationInput{{
				Operation:      permissions.Operation{Resource: permissions.ResourceFile, Action: permissions.ActionUpdate},
				HardDenyReason: "blocked by filesystem safety policy",
			}}, nil
		},
		Handler: HandlerFunc(func(context.Context, Call) (Result, error) {
			called = true
			return Result{}, nil
		}),
	}))

	result, err := registry.Invoke(context.Background(), Call{Name: "write"})

	require.NoError(t, err)
	require.False(t, called)
	require.Contains(t, result.Error, "permission_denied")
}

func TestDefaultRegistry_InvokeDenyOverridesAskAcrossResolvedOperations(t *testing.T) {
	called := false
	registry := newTestDefaultRegistry(RegistryOptions{PermissionPolicy: permissions.Policy{
		Rules: []permissions.Rule{
			{Name: "ask writes", Actions: []permissions.Action{permissions.ActionUpdate}, Decision: permissions.DecisionAsk},
			{Name: "deny deletes", Actions: []permissions.Action{permissions.ActionDelete}, Decision: permissions.DecisionDeny},
		},
	}})
	require.NoError(t, registry.Register(Definition{
		Name:       "patch",
		Permission: permissions.Operation{Resource: permissions.ResourceFile, Action: permissions.ActionManage},
		ResolvePermission: func(context.Context, Call) ([]permissions.EvaluationInput, error) {
			return []permissions.EvaluationInput{
				{Operation: permissions.Operation{Resource: permissions.ResourceFile, Action: permissions.ActionUpdate}},
				{Operation: permissions.Operation{Resource: permissions.ResourceFile, Action: permissions.ActionDelete}},
			}, nil
		},
		Handler: HandlerFunc(func(context.Context, Call) (Result, error) {
			called = true
			return Result{}, nil
		}),
	}))
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{Kind: permissions.ActorLocalOwner}, Surface: permissions.SurfaceCLI,
	})

	result, err := registry.Invoke(ctx, Call{Name: "patch"})

	require.NoError(t, err)
	require.False(t, called)
	var toolErr Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, permissions.ErrorCodeDenied, toolErr.Code)
}

func TestDefaultRegistry_InvokeRejectsInvalidPermissionResolution(t *testing.T) {
	tests := []struct {
		name     string
		resolver PermissionResolver
		code     string
		message  string
	}{
		{
			name: "typed error",
			resolver: func(context.Context, Call) ([]permissions.EvaluationInput, error) {
				return nil, NewPermissionResolutionError("path_outside_roots", "outside allowed roots")
			},
			code: "path_outside_roots", message: "outside allowed roots",
		},
		{
			name: "plain error",
			resolver: func(context.Context, Call) ([]permissions.EvaluationInput, error) {
				return nil, errors.New("bad target")
			},
			code: "invalid_input", message: "bad target",
		},
		{
			name: "no operations",
			resolver: func(context.Context, Call) ([]permissions.EvaluationInput, error) {
				return nil, nil
			},
			code: "invalid_input", message: "permission resolver returned no operations",
		},
		{
			name: "invalid operation",
			resolver: func(context.Context, Call) ([]permissions.EvaluationInput, error) {
				return []permissions.EvaluationInput{{Operation: permissions.Operation{Resource: "database"}}}, nil
			},
			code: "invalid_input", message: "permission resource is invalid",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			registry := newTestDefaultRegistry()
			require.NoError(t, registry.Register(Definition{
				Name: "target", Permission: permissions.Operation{Resource: permissions.ResourceFile, Action: permissions.ActionUpdate},
				ResolvePermission: test.resolver,
				Handler: HandlerFunc(func(context.Context, Call) (Result, error) {
					called = true
					return Result{}, nil
				}),
			}))

			result, err := registry.Invoke(context.Background(), Call{Name: "target"})

			require.NoError(t, err)
			require.False(t, called)
			var toolErr Error
			require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
			require.Equal(t, test.code, toolErr.Code)
			require.Equal(t, test.message, toolErr.Message)
		})
	}
}

func TestDefaultRegistry_InvokeWaitsForAllowOnceAndRunsHandlerExactlyOnce(t *testing.T) {
	store := storememory.NewStore()
	approvals, err := permissions.NewApprovalService(store, permissions.ApprovalOptions{RequestTTL: time.Second})
	require.NoError(t, err)
	registry := newTestDefaultRegistry(RegistryOptions{
		PermissionPolicy: permissions.Policy{
			SurfaceDefaults: map[permissions.Surface]permissions.Decision{
				permissions.SurfaceTUI: permissions.DecisionAsk,
			},
		},
	})
	registry.SetApprovalService(approvals)
	invocations := 0
	require.NoError(t, registry.Register(Definition{
		Name: "run_command",
		Permission: permissions.Operation{
			Resource: permissions.ResourceProcess,
			Action:   permissions.ActionExecute,
			Effects:  []permissions.Effect{permissions.EffectExecution},
			Target:   "fixed target",
		},
		Handler: HandlerFunc(func(context.Context, Call) (Result, error) {
			invocations++
			return Result{Output: "executed"}, nil
		}),
	}))
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor:     permissions.Actor{Kind: permissions.ActorLocalOwner, ID: "owner"},
		Surface:   permissions.SurfaceTUI,
		SessionID: "session",
	})
	resultCh := make(chan Result, 1)
	go func() {
		result, _ := registry.Invoke(ctx, Call{Name: "run_command"})
		resultCh <- result
	}()
	var request permissions.ApprovalRequest
	require.Eventually(t, func() bool {
		requests, listErr := approvals.List(
			context.Background(),
			permissions.ApprovalQuery{Status: permissions.ApprovalPending},
		)
		if listErr != nil || len(requests) == 0 {
			return false
		}
		request = requests[0]
		return true
	}, time.Second, time.Millisecond)
	_, err = approvals.Resolve(context.Background(), request.ID, true, permissions.GrantOnce)
	require.NoError(t, err)
	require.Equal(t, "executed", (<-resultCh).Output)
	require.Equal(t, 1, invocations)

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	result, err := registry.Invoke(cancelled, Call{Name: "run_command"})
	require.NoError(t, err)
	require.Contains(t, result.Error, permissions.ErrorCodeDenied)
	require.Equal(t, 1, invocations)
}

func TestDefaultRegistry_InvokePropagatesApprovalReason(t *testing.T) {
	tests := []struct {
		name           string
		approvalReason string
		want           string
	}{
		{name: "policy reason", want: "policy requires confirmation"},
		{
			name:           "resolver reason",
			approvalReason: "command requires confirmation",
			want:           "command requires confirmation",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			approver := &approvalRecorder{}
			registry := newTestDefaultRegistry(RegistryOptions{
				PermissionPolicy: permissions.Policy{
					Rules: []permissions.Rule{{
						Name: "ask for confirmation", Decision: permissions.DecisionAsk,
						Reason: "policy requires confirmation",
					}},
				},
				ApprovalService: approver,
			})
			require.NoError(t, registry.Register(Definition{
				Name: "write_file",
				ResolvePermission: func(context.Context, Call) ([]permissions.EvaluationInput, error) {
					return []permissions.EvaluationInput{{
						ApprovalReason: test.approvalReason,
						Operation: permissions.Operation{
							Resource: permissions.ResourceFile,
							Action:   permissions.ActionUpdate,
							Effects:  []permissions.Effect{permissions.EffectWrite},
						},
					}}, nil
				},
				Handler: HandlerFunc(func(context.Context, Call) (Result, error) {
					return Result{Output: "written"}, nil
				}),
			}))
			ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
				Actor: permissions.Actor{Kind: permissions.ActorLocalOwner}, Surface: permissions.SurfaceTUI,
			})

			result, err := registry.Invoke(ctx, Call{Name: "write_file"})

			require.NoError(t, err)
			require.Equal(t, "written", result.Output)
			require.Equal(t, test.want, approver.input.ApprovalReason)
		})
	}
}

func TestDefaultRegistry_InvokePropagatesPresetAuthorization(t *testing.T) {
	operation := permissions.Operation{
		Tool: "read_file", Resource: permissions.ResourceFile, Action: permissions.ActionRead,
		Effects: []permissions.Effect{permissions.EffectRead},
	}
	registry := newTestDefaultRegistry(RegistryOptions{PermissionPolicy: permissions.Policy{Preset: permissions.PresetAskForApproval}})
	require.NoError(t, registry.Register(Definition{
		Name: "read_file",
		ResolvePermission: func(context.Context, Call) ([]permissions.EvaluationInput, error) {
			return []permissions.EvaluationInput{{Operation: operation}}, nil
		},
		Handler: HandlerFunc(func(ctx context.Context, _ Call) (Result, error) {
			require.True(t, permissions.IsOperationAuthorized(ctx, operation))
			return Result{Output: "read"}, nil
		}),
	}))
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{Kind: permissions.ActorLocalOwner}, Surface: permissions.SurfaceTUI,
	})

	result, err := registry.Invoke(ctx, Call{Name: "read_file"})

	require.NoError(t, err)
	require.Equal(t, "read", result.Output)
}

func TestDefaultRegistry_InvokePropagatesApprovedPresetOperation(t *testing.T) {
	operation := permissions.Operation{
		Tool: "web_extract", Resource: permissions.ResourceNetwork, Action: permissions.ActionRead,
		Effects: []permissions.Effect{permissions.EffectRead, permissions.EffectNetwork},
	}
	approver := &approvalRecorder{}
	registry := newTestDefaultRegistry(RegistryOptions{
		PermissionPolicy: permissions.Policy{Preset: permissions.PresetAskForApproval},
		ApprovalService:  approver,
	})
	require.NoError(t, registry.Register(Definition{
		Name: "web_extract",
		ResolvePermission: func(context.Context, Call) ([]permissions.EvaluationInput, error) {
			return []permissions.EvaluationInput{{Operation: operation}}, nil
		},
		Handler: HandlerFunc(func(ctx context.Context, _ Call) (Result, error) {
			require.True(t, permissions.IsOperationAuthorized(ctx, operation))
			return Result{Output: "extracted"}, nil
		}),
	}))
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{Kind: permissions.ActorLocalOwner}, Surface: permissions.SurfaceTUI,
	})

	result, err := registry.Invoke(ctx, Call{Name: "web_extract"})

	require.NoError(t, err)
	require.Equal(t, "extracted", result.Output)
	require.ElementsMatch(t, operation.Effects, approver.input.Operation.Effects)
	require.Equal(t, operation.Tool, approver.input.Operation.Tool)
	require.Equal(t, operation.Resource, approver.input.Operation.Resource)
	require.Equal(t, operation.Action, approver.input.Operation.Action)
}

func TestDefaultRegistry_InvokeRechecksPolicyAfterApproval(t *testing.T) {
	operation := permissions.Operation{
		Tool: "run_command", Resource: permissions.ResourceProcess, Action: permissions.ActionExecute,
		Effects: []permissions.Effect{permissions.EffectExecution},
	}
	registry := newTestDefaultRegistry(RegistryOptions{PermissionPolicy: permissions.Policy{
		Rules: []permissions.Rule{{Name: "ask", Decision: permissions.DecisionAsk}},
	}})
	registry.SetApprovalService(&approvalRecorder{authorize: func(permissions.EvaluationInput) error {
		registry.permissions = permissions.NewEngine(permissions.Policy{
			Rules: []permissions.Rule{{Name: "deny", Decision: permissions.DecisionDeny}},
		})
		return nil
	}})
	require.NoError(t, registry.Register(Definition{
		Name: "run_command",
		ResolvePermission: func(context.Context, Call) ([]permissions.EvaluationInput, error) {
			return []permissions.EvaluationInput{{Operation: operation}}, nil
		},
		Handler: HandlerFunc(func(context.Context, Call) (Result, error) {
			t.Fatal("handler must not run after the policy changes to deny")
			return Result{}, nil
		}),
	}))
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{Kind: permissions.ActorLocalOwner}, Surface: permissions.SurfaceTUI,
	})

	result, err := registry.Invoke(ctx, Call{Name: "run_command"})

	require.NoError(t, err)
	require.Contains(t, result.Error, permissions.ErrorCodeDenied)
}

func TestDefaultRegistry_InvokeUsesApprovalDecisionError(t *testing.T) {
	approver := &approvalRecorder{err: &permissions.DecisionError{
		Code: permissions.ErrorCodeDenied,
		Evaluation: permissions.Evaluation{
			Decision: permissions.DecisionDeny,
			Reason:   "approval denied",
		},
	}}
	registry := newTestDefaultRegistry(RegistryOptions{
		PermissionPolicy: permissions.Policy{Rules: []permissions.Rule{{Name: "ask", Decision: permissions.DecisionAsk}}},
		ApprovalService:  approver,
	})
	require.NoError(t, registry.Register(Definition{
		Name: "write_file",
		ResolvePermission: func(context.Context, Call) ([]permissions.EvaluationInput, error) {
			return []permissions.EvaluationInput{{Operation: permissions.Operation{
				Resource: permissions.ResourceFile, Action: permissions.ActionUpdate,
			}}}, nil
		},
		Handler: HandlerFunc(func(context.Context, Call) (Result, error) {
			t.Fatal("handler must not run when approval is denied")
			return Result{}, nil
		}),
	}))
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{Kind: permissions.ActorLocalOwner}, Surface: permissions.SurfaceTUI,
	})

	result, err := registry.Invoke(ctx, Call{Name: "write_file"})

	require.NoError(t, err)
	require.Contains(t, result.Error, "approval denied")
}

func TestDefaultRegistry_InvokeFindsTerminalBatchDenialBeforePrompting(t *testing.T) {
	approver := &batchApprovalRecorder{}
	registry := newTestDefaultRegistry(RegistryOptions{
		PermissionPolicy: permissions.Policy{Rules: []permissions.Rule{
			{Name: "ask browser", Resources: []permissions.Resource{permissions.ResourceBrowser}, Decision: permissions.DecisionAsk},
			{Name: "deny network", Resources: []permissions.Resource{permissions.ResourceNetwork}, Decision: permissions.DecisionDeny},
		}},
		ApprovalService: approver,
	})
	require.NoError(t, registry.Register(Definition{
		Name: "browser",
		ResolvePermission: func(context.Context, Call) ([]permissions.EvaluationInput, error) {
			return browserPermissionInputs(), nil
		},
		Handler: HandlerFunc(func(context.Context, Call) (Result, error) {
			t.Fatal("handler must not run when one operation is denied")
			return Result{}, nil
		}),
	}))
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{Kind: permissions.ActorLocalOwner}, Surface: permissions.SurfaceTUI,
	})

	result, err := registry.Invoke(ctx, Call{Name: "browser"})

	require.NoError(t, err)
	require.Contains(t, result.Error, permissions.ErrorCodeDenied)
	require.Zero(t, approver.batchCalls)
	require.Zero(t, approver.singleCalls)
}

func TestDefaultRegistry_InvokeApprovesMultiOperationBatchOnce(t *testing.T) {
	approver := &batchApprovalRecorder{}
	registry := newTestDefaultRegistry(RegistryOptions{
		PermissionPolicy: permissions.Policy{Rules: []permissions.Rule{
			{Name: "ask browser", Resources: []permissions.Resource{permissions.ResourceBrowser}, Decision: permissions.DecisionAsk},
			{Name: "ask network", Resources: []permissions.Resource{permissions.ResourceNetwork}, Decision: permissions.DecisionAsk},
		}},
		ApprovalService: approver,
	})
	require.NoError(t, registry.Register(Definition{
		Name: "browser",
		ResolvePermission: func(context.Context, Call) ([]permissions.EvaluationInput, error) {
			return browserPermissionInputs(), nil
		},
		Handler: HandlerFunc(func(ctx context.Context, _ Call) (Result, error) {
			for _, input := range browserPermissionInputs() {
				require.True(t, permissions.IsOperationAuthorized(ctx, input.Operation))
			}
			return Result{Output: "navigated"}, nil
		}),
	}))
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{Kind: permissions.ActorLocalOwner}, Surface: permissions.SurfaceTUI,
	})

	result, err := registry.Invoke(ctx, Call{Name: "browser"})

	require.NoError(t, err)
	require.Equal(t, "navigated", result.Output)
	require.Equal(t, 1, approver.batchCalls)
	require.Equal(t, 1, approver.commitCalls)
	require.Zero(t, approver.singleCalls)
	require.Len(t, approver.inputs, 2)
}

func TestDefaultRegistry_InvokeCommitsSingleOperationApprovalAfterRecheck(t *testing.T) {
	approver := &batchApprovalRecorder{}
	registry := newTestDefaultRegistry(RegistryOptions{
		PermissionPolicy: permissions.Policy{Rules: []permissions.Rule{{
			Name: "ask file", Resources: []permissions.Resource{permissions.ResourceFile},
			Decision: permissions.DecisionAsk,
		}}},
		ApprovalService: approver,
	})
	require.NoError(t, registry.Register(Definition{
		Name: "write_file",
		Permission: permissions.Operation{
			Resource: permissions.ResourceFile, Action: permissions.ActionUpdate,
		},
		Handler: HandlerFunc(func(context.Context, Call) (Result, error) {
			return Result{Output: "written"}, nil
		}),
	}))
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{Kind: permissions.ActorLocalOwner}, Surface: permissions.SurfaceTUI,
	})

	result, err := registry.Invoke(ctx, Call{Name: "write_file"})

	require.NoError(t, err)
	require.Equal(t, "written", result.Output)
	require.Equal(t, 1, approver.batchCalls)
	require.Equal(t, 1, approver.commitCalls)
	require.Zero(t, approver.singleCalls)
}

func TestDefaultRegistry_InvokeDoesNotConsumeSingleOperationApprovalBeforeFailedRecheck(t *testing.T) {
	approver := &batchApprovalRecorder{}
	registry := newTestDefaultRegistry(RegistryOptions{
		PermissionPolicy: permissions.Policy{Rules: []permissions.Rule{{
			Name: "ask file", Resources: []permissions.Resource{permissions.ResourceFile},
			Decision: permissions.DecisionAsk,
		}}},
		ApprovalService: approver,
	})
	approver.prepare = func() {
		registry.permissions = permissions.NewEngine(permissions.Policy{Rules: []permissions.Rule{{
			Name: "deny file", Resources: []permissions.Resource{permissions.ResourceFile},
			Decision: permissions.DecisionDeny,
		}}})
	}
	require.NoError(t, registry.Register(Definition{
		Name: "write_file",
		Permission: permissions.Operation{
			Resource: permissions.ResourceFile, Action: permissions.ActionUpdate,
		},
		Handler: HandlerFunc(func(context.Context, Call) (Result, error) {
			t.Fatal("handler must not run after final recheck denies")
			return Result{}, nil
		}),
	}))
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{Kind: permissions.ActorLocalOwner}, Surface: permissions.SurfaceTUI,
	})

	result, err := registry.Invoke(ctx, Call{Name: "write_file"})

	require.NoError(t, err)
	require.Contains(t, result.Error, permissions.ErrorCodeDenied)
	require.Equal(t, 1, approver.batchCalls)
	require.Zero(t, approver.commitCalls)
}

func TestDefaultRegistry_InvokeBindsAllowedOperationsIntoApprovalBatch(t *testing.T) {
	approver := &batchApprovalRecorder{}
	registry := newTestDefaultRegistry(RegistryOptions{
		PermissionPolicy: permissions.Policy{Rules: []permissions.Rule{
			{Name: "ask browser", Resources: []permissions.Resource{permissions.ResourceBrowser}, Decision: permissions.DecisionAsk},
			{Name: "allow network", Resources: []permissions.Resource{permissions.ResourceNetwork}, Decision: permissions.DecisionAllow},
		}},
		ApprovalService: approver,
	})
	require.NoError(t, registry.Register(Definition{
		Name: "browser",
		ResolvePermission: func(context.Context, Call) ([]permissions.EvaluationInput, error) {
			return browserPermissionInputs(), nil
		},
		Handler: HandlerFunc(func(context.Context, Call) (Result, error) {
			return Result{Output: "ok"}, nil
		}),
	}))
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{Kind: permissions.ActorLocalOwner}, Surface: permissions.SurfaceTUI,
	})

	result, err := registry.Invoke(ctx, Call{Name: "browser"})

	require.NoError(t, err)
	require.Equal(t, "ok", result.Output)
	require.Len(t, approver.asked, 1)
	require.Len(t, approver.inputs, 2)
}

func TestDefaultRegistry_InvokeRechecksAllowedBatchOperationsAfterApproval(t *testing.T) {
	approver := &batchApprovalRecorder{}
	registry := newTestDefaultRegistry(RegistryOptions{
		PermissionPolicy: permissions.Policy{Rules: []permissions.Rule{
			{Name: "ask browser", Resources: []permissions.Resource{permissions.ResourceBrowser}, Decision: permissions.DecisionAsk},
			{Name: "allow network", Resources: []permissions.Resource{permissions.ResourceNetwork}, Decision: permissions.DecisionAllow},
		}},
		ApprovalService: approver,
	})
	approver.prepare = func() {
		registry.permissions = permissions.NewEngine(permissions.Policy{Rules: []permissions.Rule{
			{Name: "allow browser", Resources: []permissions.Resource{permissions.ResourceBrowser}, Decision: permissions.DecisionAllow},
			{Name: "deny network", Resources: []permissions.Resource{permissions.ResourceNetwork}, Decision: permissions.DecisionDeny},
		}})
	}
	require.NoError(t, registry.Register(Definition{
		Name: "browser",
		ResolvePermission: func(context.Context, Call) ([]permissions.EvaluationInput, error) {
			return browserPermissionInputs(), nil
		},
		Handler: HandlerFunc(func(context.Context, Call) (Result, error) {
			t.Fatal("handler must not run when an allowed batch operation changes to deny")
			return Result{}, nil
		}),
	}))
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{Kind: permissions.ActorLocalOwner}, Surface: permissions.SurfaceTUI,
	})

	result, err := registry.Invoke(ctx, Call{Name: "browser"})

	require.NoError(t, err)
	require.Contains(t, result.Error, permissions.ErrorCodeDenied)
	require.Zero(t, approver.commitCalls)
}

func TestDefaultRegistry_InvokeDoesNotAuthorizeNewAskAfterBatchApproval(t *testing.T) {
	approver := &batchApprovalRecorder{}
	registry := newTestDefaultRegistry(RegistryOptions{
		PermissionPolicy: permissions.Policy{Rules: []permissions.Rule{
			{Name: "ask browser", Resources: []permissions.Resource{permissions.ResourceBrowser}, Decision: permissions.DecisionAsk},
			{Name: "allow network", Resources: []permissions.Resource{permissions.ResourceNetwork}, Decision: permissions.DecisionAllow},
		}},
		ApprovalService: approver,
	})
	approver.prepare = func() {
		registry.permissions = permissions.NewEngine(permissions.Policy{Rules: []permissions.Rule{
			{Name: "allow browser", Resources: []permissions.Resource{permissions.ResourceBrowser}, Decision: permissions.DecisionAllow},
			{Name: "ask network", Resources: []permissions.Resource{permissions.ResourceNetwork}, Decision: permissions.DecisionAsk},
		}})
	}
	require.NoError(t, registry.Register(Definition{
		Name: "browser",
		ResolvePermission: func(context.Context, Call) ([]permissions.EvaluationInput, error) {
			return browserPermissionInputs(), nil
		},
		Handler: HandlerFunc(func(context.Context, Call) (Result, error) {
			t.Fatal("handler must not run when a new operation requires approval")
			return Result{}, nil
		}),
	}))
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{Kind: permissions.ActorLocalOwner}, Surface: permissions.SurfaceTUI,
	})

	result, err := registry.Invoke(ctx, Call{Name: "browser"})

	require.NoError(t, err)
	require.Contains(t, result.Error, permissions.ErrorCodeApprovalRequired)
	require.Zero(t, approver.commitCalls)
}

func TestDefaultRegistry_InvokeDoesNotRunHandlerWhenBatchCommitFails(t *testing.T) {
	approver := &batchApprovalRecorder{commitErr: errors.New("approval grant already consumed")}
	registry := newTestDefaultRegistry(RegistryOptions{
		PermissionPolicy: permissions.Policy{Rules: []permissions.Rule{
			{Name: "ask browser", Resources: []permissions.Resource{permissions.ResourceBrowser}, Decision: permissions.DecisionAsk},
			{Name: "ask network", Resources: []permissions.Resource{permissions.ResourceNetwork}, Decision: permissions.DecisionAsk},
		}},
		ApprovalService: approver,
	})
	require.NoError(t, registry.Register(Definition{
		Name: "browser",
		ResolvePermission: func(context.Context, Call) ([]permissions.EvaluationInput, error) {
			return browserPermissionInputs(), nil
		},
		Handler: HandlerFunc(func(context.Context, Call) (Result, error) {
			t.Fatal("handler must not run when the prepared approval cannot commit")
			return Result{}, nil
		}),
	}))
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{Kind: permissions.ActorLocalOwner}, Surface: permissions.SurfaceTUI,
	})

	result, err := registry.Invoke(ctx, Call{Name: "browser"})
	require.NoError(t, err)
	require.Contains(t, result.Error, "approval grant already consumed")
	require.Equal(t, 1, approver.batchCalls)
	require.Equal(t, 1, approver.commitCalls)
}

func TestDefaultRegistry_InvokeRejectsMultiOperationApprovalWithoutAtomicApprover(t *testing.T) {
	approver := &approvalRecorder{}
	registry := newTestDefaultRegistry(RegistryOptions{
		PermissionPolicy: permissions.Policy{Rules: []permissions.Rule{
			{Name: "ask browser", Resources: []permissions.Resource{permissions.ResourceBrowser}, Decision: permissions.DecisionAsk},
			{Name: "ask network", Resources: []permissions.Resource{permissions.ResourceNetwork}, Decision: permissions.DecisionAsk},
		}},
		ApprovalService: approver,
	})
	require.NoError(t, registry.Register(Definition{
		Name: "browser",
		ResolvePermission: func(context.Context, Call) ([]permissions.EvaluationInput, error) {
			return browserPermissionInputs(), nil
		},
		Handler: HandlerFunc(func(context.Context, Call) (Result, error) {
			t.Fatal("handler must not run without atomic batch approval")
			return Result{}, nil
		}),
	}))
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{Kind: permissions.ActorLocalOwner}, Surface: permissions.SurfaceTUI,
	})

	result, err := registry.Invoke(ctx, Call{Name: "browser"})

	require.NoError(t, err)
	require.Contains(t, result.Error, "approval service does not support atomic operation batches")
	require.Equal(t, 0, approver.calls)
}

func TestDefaultRegistry_InvokeRejectsSingleOperationApprovalWithoutPreparedApprover(t *testing.T) {
	approver := &nonPreparedApprovalRecorder{}
	registry := newTestDefaultRegistry(RegistryOptions{
		PermissionPolicy: permissions.Policy{Rules: []permissions.Rule{{
			Name: "ask file", Resources: []permissions.Resource{permissions.ResourceFile},
			Decision: permissions.DecisionAsk,
		}}},
		ApprovalService: approver,
	})
	require.NoError(t, registry.Register(Definition{
		Name: "write_file",
		Permission: permissions.Operation{
			Resource: permissions.ResourceFile, Action: permissions.ActionUpdate,
		},
		Handler: HandlerFunc(func(context.Context, Call) (Result, error) {
			t.Fatal("handler must not run without a prepared approval")
			return Result{}, nil
		}),
	}))
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{Kind: permissions.ActorLocalOwner}, Surface: permissions.SurfaceTUI,
	})

	result, err := registry.Invoke(ctx, Call{Name: "write_file"})

	require.NoError(t, err)
	require.Contains(t, result.Error, "approval service does not support prepared approvals")
	require.Zero(t, approver.calls)
}

func browserPermissionInputs() []permissions.EvaluationInput {
	return []permissions.EvaluationInput{
		{Operation: permissions.Operation{
			Tool: "browser", Resource: permissions.ResourceBrowser, Action: permissions.ActionUpdate,
			Effects: []permissions.Effect{permissions.EffectWrite},
		}},
		{Operation: permissions.Operation{
			Tool: "browser", Resource: permissions.ResourceNetwork, Action: permissions.ActionRead,
			Effects: []permissions.Effect{permissions.EffectRead, permissions.EffectNetwork},
		}},
	}
}

func TestDefaultRegistry_SetApprovalServiceHandlesNilRegistry(t *testing.T) {
	var registry *DefaultRegistry
	registry.SetApprovalService(nil)
	require.Nil(t, registry.getApprovalService())
}

type permissionTraceEvent struct {
	eventType string
	payload   trace.PermissionDecisionPayload
}

type permissionTraceRecorder struct {
	events []permissionTraceEvent
}

func (r *permissionTraceRecorder) Record(eventType string, payload any) {
	r.events = append(r.events, permissionTraceEvent{
		eventType: eventType,
		payload:   payload.(trace.PermissionDecisionPayload),
	})
}

type approvalRecorder struct {
	input     permissions.EvaluationInput
	err       error
	authorize func(permissions.EvaluationInput) error
	calls     int
}

type batchApprovalRecorder struct {
	asked       []permissions.EvaluationInput
	inputs      []permissions.EvaluationInput
	prepare     func()
	singleCalls int
	batchCalls  int
	commitCalls int
	commitErr   error
}

func (r *batchApprovalRecorder) Authorize(_ context.Context, input permissions.EvaluationInput) error {
	r.singleCalls++
	r.inputs = []permissions.EvaluationInput{input}
	return nil
}

func (r *batchApprovalRecorder) PrepareBatch(
	_ context.Context,
	inputs []permissions.EvaluationInput,
) (permissions.BatchApproval, error) {
	return r.PrepareOperationBatch(context.Background(), inputs, inputs)
}

func (r *batchApprovalRecorder) PrepareOperationBatch(
	_ context.Context,
	asked []permissions.EvaluationInput,
	inputs []permissions.EvaluationInput,
) (permissions.BatchApproval, error) {
	r.batchCalls++
	r.asked = append([]permissions.EvaluationInput(nil), asked...)
	r.inputs = append([]permissions.EvaluationInput(nil), inputs...)
	if r.prepare != nil {
		r.prepare()
	}
	return batchApprovalFunc(func(context.Context) error {
		r.commitCalls++
		return r.commitErr
	}), nil
}

type batchApprovalFunc func(context.Context) error

func (f batchApprovalFunc) Commit(ctx context.Context) error {
	return f(ctx)
}

func (r *approvalRecorder) Authorize(_ context.Context, input permissions.EvaluationInput) error {
	r.calls++
	r.input = input
	if r.authorize != nil {
		return r.authorize(input)
	}
	return r.err
}

func (r *approvalRecorder) PrepareBatch(
	ctx context.Context,
	inputs []permissions.EvaluationInput,
) (permissions.BatchApproval, error) {
	if len(inputs) != 1 {
		return nil, errors.New("approval recorder expects one operation")
	}
	if err := r.Authorize(ctx, inputs[0]); err != nil {
		return nil, err
	}
	return batchApprovalFunc(func(context.Context) error { return nil }), nil
}

type nonPreparedApprovalRecorder struct {
	calls int
}

func (r *nonPreparedApprovalRecorder) Authorize(context.Context, permissions.EvaluationInput) error {
	r.calls++
	return nil
}
