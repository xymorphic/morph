package command

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSelector_MatchesTypedCommandFacts(t *testing.T) {
	complete := true
	target := Target{
		Mode: ModeDirect, Executable: "git", ResolvedPath: "/usr/bin/git",
		Arguments: []string{"status", "--short"}, PlanDigest: "plan", Complete: true,
	}

	require.True(t, (Selector{
		Executable: "git", ArgumentPrefix: []string{"status"}, RequireComplete: &complete,
	}).Matches(target))
	require.False(t, (Selector{Executable: "git", ExactArguments: []string{"status"}}).Matches(target))
	require.False(t, (Selector{Executable: "git", Modes: []Mode{ModePOSIXShell}}).Matches(target))
	require.False(t, (Selector{Executable: "git", ResolvedPath: "/tmp/git"}).Matches(target))
}

func TestSelector_BareExecutableDoesNotMatchExplicitPath(t *testing.T) {
	target := Target{
		Mode: ModeDirect, Executable: "/tmp/git", ResolvedPath: "/tmp/git",
		PlanDigest: "plan", Complete: true,
	}

	require.False(t, (Selector{Executable: "git"}).Matches(target))
	require.True(t, (Selector{Executable: "/tmp/git"}).Matches(target))
	require.True(t, (Selector{ResolvedPath: "/tmp/git"}).Matches(target))
}

func TestSelector_RequiresExplicitIndirectOptIn(t *testing.T) {
	target := Target{
		Mode: ModeDirect, Executable: "make", ResolvedPath: "/usr/bin/make",
		Indirect: true, PlanDigest: "plan", Complete: false,
	}

	require.False(t, (Selector{Executable: "make"}).Matches(target))
	require.True(t, (Selector{Executable: "make", AllowIndirect: true}).Matches(target))
}

func TestSelector_NormalizeRejectsAmbiguousSelectors(t *testing.T) {
	tests := []struct {
		name     string
		selector Selector
		message  string
	}{
		{name: "empty", selector: Selector{}, message: "command selector requires an executable or resolved path"},
		{name: "relative path", selector: Selector{ResolvedPath: "bin/git"}, message: "command selector resolved path must be absolute"},
		{name: "argument conflict", selector: Selector{
			Executable: "git", ExactArguments: []string{"status"}, ArgumentPrefix: []string{"status"},
		}, message: "command selector cannot combine exact arguments and an argument prefix"},
		{name: "mode", selector: Selector{Executable: "git", Modes: []Mode{"cmd"}}, message: "command selector mode must be direct or posix_shell"},
		{name: "NUL exact argument", selector: Selector{
			Executable: "git", ExactArguments: []string{"bad\x00argument"},
		}, message: "command selector argument contains a NUL byte"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.selector.Normalize()
			require.EqualError(t, err, test.message)
		})
	}
}

func TestIntersectSelectors_ProducesNarrowerSelector(t *testing.T) {
	intersection := IntersectSelectors(
		[]Selector{{Executable: "git", ArgumentPrefix: []string{"status"}, Modes: []Mode{ModeDirect, ModePOSIXShell}}},
		[]Selector{{Executable: "git", ExactArguments: []string{"status", "--short"}, Modes: []Mode{ModeDirect}}},
	)

	require.Equal(t, []Selector{{
		Executable:     "git",
		ExactArguments: []string{"status", "--short"},
		Modes:          []Mode{ModeDirect},
	}}, intersection)
}

func TestSelector_UsesPlatformPathAndExecutableCaseRules(t *testing.T) {
	original := selectorGOOS
	selectorGOOS = "windows"
	t.Cleanup(func() {
		selectorGOOS = original
	})
	target := Target{
		Mode: ModeDirect, Executable: "GIT.EXE", ResolvedPath: `C:\Tools\Git.exe`,
		PlanDigest: "plan", Complete: true,
	}

	require.True(t, (Selector{
		Executable:   "git.exe",
		ResolvedPath: `c:\tools\git.EXE`,
	}).Matches(target))
}

func TestSelector_DirectSelectorDoesNotMatchNestedShellInvocation(t *testing.T) {
	target := Target{
		Mode: ModePOSIXShell, Executable: "git", ResolvedPath: "/usr/bin/git",
		Arguments: []string{"status"}, PlanDigest: "plan", Complete: true,
	}

	require.False(t, (Selector{
		Executable: "git", ArgumentPrefix: []string{"status"},
	}).Matches(target))
	require.True(t, (Selector{
		Executable: "git", ArgumentPrefix: []string{"status"}, Modes: []Mode{ModePOSIXShell},
	}).Matches(target))
}

func TestSelector_NormalizeSelectorsDeduplicatesAndMatches(t *testing.T) {
	selectors, err := NormalizeSelectors([]Selector{
		{
			Executable: "git", ArgumentPrefix: []string{"status"},
			Modes: []Mode{ModePOSIXShell, ModeDirect},
		},
		{
			Executable: " git ", ArgumentPrefix: []string{"status"},
			Modes: []Mode{ModeDirect, ModePOSIXShell},
		},
		{Executable: "make", AllowIndirect: true},
	})
	require.NoError(t, err)
	require.Len(t, selectors, 2)
	require.NotEqual(t, selectors[0].Fingerprint(), selectors[1].Fingerprint())

	target := Target{
		Mode: ModeDirect, Executable: "git", ResolvedPath: "/usr/bin/git",
		Arguments: []string{"status", "--short"}, PlanDigest: "plan", Complete: true,
	}
	require.True(t, MatchSelectors(selectors, target))
	require.False(t, MatchSelectors(selectors, Target{
		Mode: ModeDirect, Executable: "printf", ResolvedPath: "/usr/bin/printf",
		PlanDigest: "plan", Complete: true,
	}))
}

func TestSelector_FingerprintDistinguishesArgumentConstraintKinds(t *testing.T) {
	unconstrained := (Selector{Executable: "git"}).Fingerprint()
	exactlyZero := (Selector{Executable: "git", ExactArguments: []string{}}).Fingerprint()
	anyPrefix := (Selector{Executable: "git", ArgumentPrefix: []string{}}).Fingerprint()

	require.NotEqual(t, unconstrained, exactlyZero)
	require.NotEqual(t, unconstrained, anyPrefix)
	require.NotEqual(t, exactlyZero, anyPrefix)
}

func TestSelectorAndTargetFingerprintsLengthPrefixArguments(t *testing.T) {
	leftSelector := Selector{Executable: "printf", ExactArguments: []string{"a\x1fb"}}
	rightSelector := Selector{Executable: "printf", ExactArguments: []string{"a", "b"}}
	require.NotEqual(t, leftSelector.Fingerprint(), rightSelector.Fingerprint())

	leftTarget := Target{
		Mode: ModeDirect, Executable: "printf", ResolvedPath: "/usr/bin/printf",
		Arguments: []string{"a\x1fb"}, PlanDigest: "plan",
	}
	rightTarget := leftTarget
	rightTarget.Arguments = []string{"a", "b"}
	require.NotEqual(t, leftTarget.Fingerprint(), rightTarget.Fingerprint())
}

func TestNormalizeDenySelectors_DefaultsToBothExecutionModes(t *testing.T) {
	selectors, err := NormalizeDenySelectors([]Selector{{Executable: "git"}})
	require.NoError(t, err)
	require.Equal(t, []Mode{ModeDirect, ModePOSIXShell}, selectors[0].Modes)
}

func TestTarget_NormalizeFingerprintAndEquality(t *testing.T) {
	target := Target{
		Mode: ModeDirect, Executable: "git", ResolvedPath: "/usr/bin/git",
		Arguments: []string{"status"}, PlanDigest: "plan", Complete: true,
		DynamicReasons:  []DynamicReason{ReasonEnvironment, ReasonEnvironment},
		InvocationCount: 1,
	}
	normalized, err := target.Normalize()
	require.NoError(t, err)
	require.Equal(t, []DynamicReason{ReasonEnvironment}, normalized.DynamicReasons)
	require.True(t, normalized.Equal(normalized))
	require.NotEmpty(t, normalized.Fingerprint())

	changed := normalized
	changed.Arguments = []string{"push"}
	require.False(t, normalized.Equal(changed))
	require.NotEqual(t, normalized.Fingerprint(), changed.Fingerprint())

	invalid := normalized
	invalid.DynamicReasons = []DynamicReason{"unknown"}
	_, err = invalid.Normalize()
	require.EqualError(t, err, "command target contains an invalid dynamic reason")
}

func TestSelector_MatchesRejectsInvalidAndMismatchedTargets(t *testing.T) {
	complete := true
	baseTarget := Target{
		Mode: ModeDirect, Executable: "git", ResolvedPath: "/usr/bin/git",
		Arguments: []string{"status"}, PlanDigest: "plan", Complete: true,
	}
	tests := []struct {
		name     string
		selector Selector
		target   Target
	}{
		{name: "invalid selector", selector: Selector{}, target: baseTarget},
		{name: "invalid target", selector: Selector{Executable: "git"}, target: Target{}},
		{name: "executable", selector: Selector{Executable: "make"}, target: baseTarget},
		{name: "resolved path", selector: Selector{ResolvedPath: "/tmp/git"}, target: baseTarget},
		{name: "completeness", selector: Selector{
			Executable: "git", RequireComplete: &complete,
		}, target: func() Target {
			target := baseTarget
			target.Complete = false
			return target
		}()},
		{name: "exact arguments", selector: Selector{
			Executable: "git", ExactArguments: []string{"push"},
		}, target: baseTarget},
		{name: "long prefix", selector: Selector{
			Executable: "git", ArgumentPrefix: []string{"status", "--short"},
		}, target: baseTarget},
		{name: "different prefix", selector: Selector{
			Executable: "git", ArgumentPrefix: []string{"push"},
		}, target: baseTarget},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.False(t, test.selector.Matches(test.target))
		})
	}
}

func TestIntersectSelectors_HandlesEmptyInvalidAndIncompatibleSelectors(t *testing.T) {
	require.Equal(t, []Selector{{
		Executable: "git", Modes: []Mode{ModeDirect},
	}}, IntersectSelectors(nil, []Selector{{Executable: "git"}}))
	require.Equal(t, []Selector{{
		Executable: "git", Modes: []Mode{ModeDirect},
	}}, IntersectSelectors([]Selector{{Executable: "git"}}, nil))
	require.Empty(t, IntersectSelectors(
		[]Selector{{Executable: "git"}},
		[]Selector{{Executable: "make"}},
	))
	require.Empty(t, IntersectSelectors(
		[]Selector{{Executable: "git", ResolvedPath: "/usr/bin/git"}},
		[]Selector{{Executable: "git", ResolvedPath: "/tmp/git"}},
	))
	require.Empty(t, IntersectSelectors(
		[]Selector{{Executable: "git", Modes: []Mode{ModeDirect}}},
		[]Selector{{Executable: "git", Modes: []Mode{ModePOSIXShell}}},
	))
	require.Empty(t, IntersectSelectors(
		[]Selector{{Executable: "git", ExactArguments: []string{"status"}}},
		[]Selector{{Executable: "git", ExactArguments: []string{"push"}}},
	))
	require.Empty(t, IntersectSelectors(
		[]Selector{{Executable: "git", ExactArguments: []string{"status"}}},
		[]Selector{{Executable: "git", ArgumentPrefix: []string{"push"}}},
	))
	require.Empty(t, IntersectSelectors(
		[]Selector{{}},
		[]Selector{{Executable: "git"}},
	))
}

func TestIntersectSelectors_CombinesIndependentConstraints(t *testing.T) {
	complete := true
	tests := []struct {
		name  string
		left  Selector
		right Selector
		want  Selector
	}{
		{
			name:  "executable and path",
			left:  Selector{Executable: "git"},
			right: Selector{ResolvedPath: "/usr/bin/git"},
			want: Selector{
				Executable: "git", ResolvedPath: "/usr/bin/git", Modes: []Mode{ModeDirect},
			},
		},
		{
			name:  "path and executable",
			left:  Selector{ResolvedPath: "/usr/bin/git"},
			right: Selector{Executable: "git"},
			want: Selector{
				Executable: "git", ResolvedPath: "/usr/bin/git", Modes: []Mode{ModeDirect},
			},
		},
		{
			name: "matching path and completeness",
			left: Selector{
				Executable: "git", ResolvedPath: "/usr/bin/git", RequireComplete: &complete,
			},
			right: Selector{
				Executable: "git", ResolvedPath: "/usr/bin/git", RequireComplete: &complete,
			},
			want: Selector{
				Executable: "git", ResolvedPath: "/usr/bin/git",
				Modes: []Mode{ModeDirect}, RequireComplete: &complete,
			},
		},
		{
			name:  "right exact arguments",
			left:  Selector{Executable: "git", ArgumentPrefix: []string{"status"}},
			right: Selector{Executable: "git", ExactArguments: []string{"status", "--short"}},
			want: Selector{
				Executable: "git", ExactArguments: []string{"status", "--short"},
				Modes: []Mode{ModeDirect},
			},
		},
		{
			name:  "right prefix is narrower",
			left:  Selector{Executable: "git", ArgumentPrefix: []string{"status"}},
			right: Selector{Executable: "git", ArgumentPrefix: []string{"status", "--short"}},
			want: Selector{
				Executable: "git", ArgumentPrefix: []string{"status", "--short"},
				Modes: []Mode{ModeDirect},
			},
		},
		{
			name:  "left prefix is narrower",
			left:  Selector{Executable: "git", ArgumentPrefix: []string{"status", "--short"}},
			right: Selector{Executable: "git", ArgumentPrefix: []string{"status"}},
			want: Selector{
				Executable: "git", ArgumentPrefix: []string{"status", "--short"},
				Modes: []Mode{ModeDirect},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, ok := intersectSelector(test.left, test.right)
			require.True(t, ok)
			require.Equal(t, test.want, actual)
		})
	}
}

func TestIntersectSelectors_UsesWindowsExecutableCaseRules(t *testing.T) {
	original := selectorGOOS
	selectorGOOS = "windows"
	t.Cleanup(func() {
		selectorGOOS = original
	})

	actual, ok := intersectSelector(
		Selector{Executable: "GIT.EXE"},
		Selector{Executable: "git.exe"},
	)

	require.True(t, ok)
	require.Equal(t, "GIT.EXE", actual.Executable)
}

func TestIntersectSelectors_RejectsConflictingConstraints(t *testing.T) {
	complete := true
	incomplete := false
	tests := []struct {
		name  string
		left  Selector
		right Selector
	}{
		{
			name:  "complete",
			left:  Selector{Executable: "git", RequireComplete: &complete},
			right: Selector{Executable: "git", RequireComplete: &incomplete},
		},
		{
			name:  "left exact outside prefix",
			left:  Selector{Executable: "git", ExactArguments: []string{"push"}},
			right: Selector{Executable: "git", ArgumentPrefix: []string{"status"}},
		},
		{
			name:  "right exact outside prefix",
			left:  Selector{Executable: "git", ArgumentPrefix: []string{"status"}},
			right: Selector{Executable: "git", ExactArguments: []string{"push"}},
		},
		{
			name:  "unrelated prefixes",
			left:  Selector{Executable: "git", ArgumentPrefix: []string{"status"}},
			right: Selector{Executable: "git", ArgumentPrefix: []string{"push"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, ok := intersectSelector(test.left, test.right)
			require.False(t, ok)
		})
	}
}

func TestSelector_CollectionAndFingerprintValidation(t *testing.T) {
	require.Empty(t, (Selector{}).Fingerprint())
	complete := true
	require.NotEmpty(t, (Selector{
		Executable: "git", RequireComplete: &complete,
	}).Fingerprint())
	require.True(t, MatchSelectors(nil, Target{}))

	_, err := NormalizeSelectors([]Selector{{}})
	require.EqualError(t, err, "command selector requires an executable or resolved path")
}

func TestIntersectSelectors_CombinesExactAndOptionalCompleteness(t *testing.T) {
	complete := true
	actual, ok := intersectSelector(
		Selector{
			Executable: "git", ExactArguments: []string{"status"}, RequireComplete: &complete,
		},
		Selector{
			Executable: "git", ArgumentPrefix: []string{"status"},
		},
	)

	require.True(t, ok)
	require.Equal(t, []string{"status"}, actual.ExactArguments)
	require.NotNil(t, actual.RequireComplete)
	require.True(t, *actual.RequireComplete)

	actual, ok = intersectSelector(
		Selector{Executable: "git", ExactArguments: []string{"status"}},
		Selector{Executable: "git", ExactArguments: []string{"status"}},
	)
	require.True(t, ok)
	require.Equal(t, []string{"status"}, actual.ExactArguments)

	require.Equal(t, &complete, intersectComplete(&complete, nil))
}

func TestPlan_TargetNewCommandAndSummaryContracts(t *testing.T) {
	plan := Plan{
		Mode: ModeDirect, CWD: "/workspace", CWDIdentity: "workspace:.",
		EnvironmentDigest: "environment", Complete: true, digest: "plan",
		environment: []string{"PATH=/usr/bin"},
		Invocations: []Invocation{{
			Executable: "git", ResolvedPath: "/usr/bin/git", Arguments: []string{"status"},
		}},
		Redirects: []Redirect{{Action: RedirectCreate, Path: "out"}},
	}
	target := plan.Target(plan.Invocations[0])
	require.Equal(t, ModeDirect, target.Mode)
	require.Equal(t, "plan", target.PlanDigest)
	require.Equal(t, 1, target.InvocationCount)
	require.Equal(t, 1, target.RedirectCount)
	require.Equal(t, "direct · git", plan.Summary())

	cmd, err := plan.NewCommand(context.Background())
	require.NoError(t, err)
	require.Equal(t, "/usr/bin/git", cmd.Path)
	require.Equal(t, []string{"/usr/bin/git", "status"}, cmd.Args)
	require.Equal(t, "/workspace", cmd.Dir)
	require.Equal(t, []string{"PATH=/usr/bin"}, cmd.Env)

	shell := plan
	shell.Mode = ModePOSIXShell
	shell.ShellPath = "/bin/sh"
	shell.source = "git status"
	shell.Invocations = append(shell.Invocations, Invocation{Executable: "printf"})
	require.Equal(t, "posix_shell · git and 1 more", shell.Summary())
	cmd, err = shell.NewCommand(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"/bin/sh", "-c", "git status"}, cmd.Args)
}

func TestPlan_NewCommandAndTargetValidationFailures(t *testing.T) {
	tests := []struct {
		name    string
		plan    Plan
		message string
	}{
		{name: "no invocation", plan: Plan{}, message: "command plan has no executable invocation"},
		{name: "missing direct path", plan: Plan{
			Mode: ModeDirect, Invocations: []Invocation{{Executable: "git"}},
		}, message: "direct command plan has no resolved executable"},
		{name: "missing shell", plan: Plan{
			Mode: ModePOSIXShell, Invocations: []Invocation{{Executable: "git"}},
		}, message: "POSIX shell is unavailable"},
		{name: "invalid mode", plan: Plan{
			Mode: "invalid", Invocations: []Invocation{{Executable: "git"}},
		}, message: "command execution mode is invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.plan.NewCommand(context.Background())
			require.EqualError(t, err, test.message)
		})
	}
	require.Equal(t, "direct command", (Plan{Mode: ModeDirect}).Summary())

	valid := Target{
		Mode: ModeDirect, Executable: "git", ResolvedPath: "/usr/bin/git",
		PlanDigest: "plan",
	}
	cases := []struct {
		name    string
		target  Target
		message string
	}{
		{name: "mode", target: func() Target {
			target := valid
			target.Mode = "invalid"
			return target
		}(), message: "command target mode is invalid"},
		{name: "executable", target: func() Target {
			target := valid
			target.Executable = ""
			return target
		}(), message: "command target executable is required"},
		{name: "path", target: func() Target {
			target := valid
			target.ResolvedPath = "git"
			return target
		}(), message: "direct command target requires an absolute resolved path"},
		{name: "digest", target: func() Target {
			target := valid
			target.PlanDigest = ""
			return target
		}(), message: "command target plan digest is required"},
		{name: "argument", target: func() Target {
			target := valid
			target.Arguments = []string{"bad\x00argument"}
			return target
		}(), message: "command target argument contains a NUL byte"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.target.Normalize()
			require.EqualError(t, err, test.message)
		})
	}
}
