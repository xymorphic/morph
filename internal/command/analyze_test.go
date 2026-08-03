package command

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"
)

func TestAnalyze_DirectPreservesArgumentsAndResolvedExecutable(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "tool")
	require.NoError(t, os.WriteFile(executable, []byte("binary"), 0o700))
	setLookPath(t, func(string) (string, error) { return executable, nil })

	plan, err := Analyze(context.Background(), Request{
		Mode: ModeDirect, Command: "tool", Args: []string{"hello world", "&&", ""},
		CWD: t.TempDir(), WorkspaceRoot: t.TempDir(),
	})

	require.NoError(t, err)
	require.True(t, plan.Complete)
	require.Equal(t, ModeDirect, plan.Mode)
	require.Equal(t, executable, plan.Invocations[0].ResolvedPath)
	require.Equal(t, []string{"hello world", "&&", ""}, plan.Invocations[0].Arguments)
	require.NotEmpty(t, plan.Digest())

	cmd, err := plan.NewCommand(context.Background())
	require.NoError(t, err)
	require.Equal(t, executable, cmd.Path)
	require.Equal(t, []string{executable, "hello world", "&&", ""}, cmd.Args)
}

func TestAnalyze_DirectSupportsZeroArguments(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "tool")
	setLookPath(t, func(string) (string, error) { return executable, nil })

	plan, err := Analyze(context.Background(), Request{
		Command: "tool", CWD: t.TempDir(), WorkspaceRoot: t.TempDir(),
	})

	require.NoError(t, err)
	require.Empty(t, plan.Invocations[0].Arguments)
	require.Equal(t, ModeDirect, plan.Mode)
}

func TestAnalyze_DirectResolvesRelativeExecutableFromRequestedWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "bin", "tool")
	setLookPath(t, func(value string) (string, error) {
		require.Equal(t, executable, value)
		return value, nil
	})

	plan, err := Analyze(context.Background(), Request{
		Command: "./bin/tool", CWD: root, WorkspaceRoot: root,
	})

	require.NoError(t, err)
	require.Equal(t, executable, plan.Invocations[0].ResolvedPath)
}

func TestAnalyze_DirectMakesRelativeLookupResultAbsolute(t *testing.T) {
	setLookPath(t, func(string) (string, error) {
		return filepath.Join("relative", "tool"), nil
	})

	plan, err := Analyze(context.Background(), Request{Command: "tool"})

	require.NoError(t, err)
	require.True(t, filepath.IsAbs(plan.Invocations[0].ResolvedPath))
	require.True(t, strings.HasSuffix(
		plan.Invocations[0].ResolvedPath,
		filepath.Join("relative", "tool"),
	))
}

func TestAnalyze_DirectPreservesWindowsAbsoluteExecutableIdentity(t *testing.T) {
	setLookPath(t, func(value string) (string, error) {
		require.Equal(t, `C:\Tools\tool.exe`, value)
		return value, nil
	})

	plan, err := Analyze(context.Background(), Request{
		Command: `C:\Tools\tool.exe`, GOOS: "windows",
	})

	require.NoError(t, err)
	require.Equal(t, `C:\Tools\tool.exe`, plan.Invocations[0].ResolvedPath)
}

func TestAnalyze_DirectRejectsInvalidConstruction(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "script.cmd")
	tests := []struct {
		name    string
		request Request
		lookup  func(string) (string, error)
		message string
	}{
		{name: "missing command", request: Request{}, message: "command is required"},
		{name: "NUL command", request: Request{Command: "bad\x00name"}, message: "command contains a NUL byte"},
		{name: "NUL argument", request: Request{Command: "tool", Args: []string{"bad\x00arg"}}, message: "command argument contains a NUL byte"},
		{name: "missing executable", request: Request{Command: "missing"}, lookup: func(string) (string, error) {
			return "", errors.New("missing")
		}, message: "command executable was not found"},
		{name: "Windows script", request: Request{Command: "script", GOOS: "windows"}, lookup: func(string) (string, error) {
			return executable, nil
		}, message: "interpreter-dispatched scripts are not supported in direct mode"},
		{name: "invalid mode", request: Request{Mode: "cmd", Command: "tool"}, message: "command mode must be direct or posix_shell"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.lookup != nil {
				setLookPath(t, test.lookup)
			}
			_, err := Analyze(context.Background(), test.request)
			require.EqualError(t, err, test.message)
		})
	}
}

func TestAnalyze_DirectPreservesConfiguredLookupError(t *testing.T) {
	_, err := Analyze(context.Background(), Request{
		Command:               "missing",
		PreserveLookPathError: true,
		LookPath: func(string) (string, error) {
			return "", errors.New("command executable is absent from the sandbox contract")
		},
	})

	require.EqualError(t, err, "command executable is absent from the sandbox contract")
}

func TestAnalyze_PropagatesDependencyFailures(t *testing.T) {
	setGetWorkingDirectory(t, func() (string, error) {
		return "", errors.New("unavailable")
	})
	_, err := Analyze(context.Background(), Request{Command: "tool"})
	require.EqualError(t, err, "working directory could not be resolved")

	setGetWorkingDirectory(t, os.Getwd)
	setGetAbsolutePath(t, func(path string) (string, error) {
		if path == "bad-root" {
			return "", errors.New("unavailable")
		}
		return filepath.Abs(path)
	})
	_, err = Analyze(context.Background(), Request{
		Command: "tool", WorkspaceRoot: "bad-root",
	})
	require.EqualError(t, err, "workspace root could not be resolved")

	setLookPath(t, func(string) (string, error) {
		return "relative-tool", nil
	})
	setGetAbsolutePath(t, func(path string) (string, error) {
		if path == "relative-tool" {
			return "", errors.New("unavailable")
		}
		return filepath.Abs(path)
	})
	_, err = Analyze(context.Background(), Request{Command: "tool", WorkspaceRoot: "/"})
	require.EqualError(t, err, "command executable path could not be resolved")

	setGetAbsolutePath(t, filepath.Abs)
	_, err = Analyze(context.Background(), Request{
		Command: "tool", Environment: map[string]string{"BAD=KEY": "value"},
	})
	require.EqualError(t, err, "command environment contains an invalid entry")
}

func TestAnalyze_RejectsShellInputAndPlanCardinalityLimits(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	_, err := Analyze(context.Background(), Request{
		Mode: ModePOSIXShell, Command: "printf ok", Args: []string{"unexpected"},
	})
	require.EqualError(t, err, "posix_shell mode does not accept direct arguments")
	_, err = Analyze(context.Background(), Request{Mode: ModePOSIXShell, Command: " \t "})
	require.EqualError(t, err, "command is required")
	_, err = Analyze(context.Background(), Request{Mode: ModePOSIXShell, Command: "VALUE=only"})
	require.EqualError(t, err, "command plan has no executable invocation")

	invocations := strings.Repeat("printf x;", MaxInvocations+1)
	_, err = Analyze(context.Background(), Request{Mode: ModePOSIXShell, Command: invocations})
	require.EqualError(t, err, "command invocation count exceeds the analysis limit")

	var redirects strings.Builder
	redirects.WriteString("printf x")
	for index := 0; index <= MaxRedirects; index++ {
		redirects.WriteString(" > file")
		redirects.WriteString(strconv.Itoa(index))
	}
	_, err = Analyze(context.Background(), Request{Mode: ModePOSIXShell, Command: redirects.String()})
	require.EqualError(t, err, "command redirect count exceeds the analysis limit")
}

func TestAnalyze_DirectClassifiesIndirectExecution(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	plan, err := Analyze(context.Background(), Request{
		Mode: ModeDirect, Command: "sh", Args: []string{"-c", "git status"},
	})

	require.NoError(t, err)
	require.True(t, plan.Complete)
	require.Equal(t, []string{"sh", "git"}, getExecutables(plan.Invocations))
	require.Equal(t, ModeDirect, plan.Invocations[0].Mode)
	require.Equal(t, ModePOSIXShell, plan.Invocations[1].Mode)
	require.True(t, plan.Invocations[0].Indirect)
	require.Contains(t, plan.DynamicReasons, ReasonIndirectExecution)
}

func TestAnalyze_DirectMarksOpaqueInterpreterAndScriptSourcesIncomplete(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	codePlan, err := Analyze(context.Background(), Request{
		Mode: ModeDirect, Command: "python3", Args: []string{"-c", "print('hello')"},
	})
	require.NoError(t, err)
	require.False(t, codePlan.Complete)
	require.Contains(t, codePlan.DynamicReasons, ReasonIndirectExecution)

	scriptPlan, err := Analyze(context.Background(), Request{
		Mode: ModeDirect, Command: "python3", Args: []string{"script.py"},
	})
	require.NoError(t, err)
	require.False(t, scriptPlan.Complete)
	require.Equal(t, []Redirect{{Action: RedirectRead, Path: "script.py", Static: true}}, scriptPlan.Redirects)
}

func TestAnalyze_DirectDoesNotTreatLongShellOptionAsCommandSource(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	plan, err := Analyze(context.Background(), Request{
		Mode: ModeDirect, Command: "bash", Args: []string{"--norc", "script.sh"},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"bash"}, getExecutables(plan.Invocations))
	require.Equal(t, []Redirect{{Action: RedirectRead, Path: "script.sh", Static: true}}, plan.Redirects)
}

func TestAnalyze_DirectTracksExplicitShellStartupFile(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	plan, err := Analyze(context.Background(), Request{
		Mode: ModeDirect, Command: "bash",
		Args: []string{"--rcfile", "./setup.sh", "-c", "printf done"},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"bash", "printf"}, getExecutables(plan.Invocations))
	require.Contains(t, plan.Redirects, Redirect{
		Action: RedirectRead, Path: "./setup.sh", Static: true,
	})
	require.False(t, plan.Complete)
}

func TestAnalyze_DirectFindsNestedShellSourceAfterOptionValues(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })
	tests := [][]string{
		{"-o", "errexit", "-c", "printf done"},
		{"+o", "errexit", "-c", "printf done"},
		{"-O", "extglob", "-c", "printf done"},
	}

	for _, arguments := range tests {
		plan, err := Analyze(context.Background(), Request{
			Mode: ModeDirect, Command: "bash", Args: arguments,
		})
		require.NoError(t, err)
		require.Equal(t, []string{"bash", "printf"}, getExecutables(plan.Invocations))
	}
}

func TestAnalyze_DirectDoesNotTreatLongInterpreterOptionAsCode(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	plan, err := Analyze(context.Background(), Request{
		Mode: ModeDirect, Command: "python3", Args: []string{"--check-hash-based-pycs", "always", "script.py"},
	})

	require.NoError(t, err)
	require.Equal(t, []Redirect{{Action: RedirectRead, Path: "script.py", Static: true}}, plan.Redirects)
}

func TestAnalyze_DirectClassifiesInterpreterCodeAndScriptSources(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })
	tests := []struct {
		name         string
		command      string
		arguments    []string
		wantRedirect string
	}{
		{name: "Python module", command: "python3", arguments: []string{"-m", "http.server"}},
		{name: "Node eval", command: "node", arguments: []string{"--eval", "console.log('ok')"}},
		{name: "Ruby eval", command: "ruby", arguments: []string{"-e", "puts 'ok'"}},
		{name: "Perl stdin", command: "perl", arguments: []string{"-"}},
		{name: "Node require then script", command: "node", arguments: []string{"--require", "setup", "script.js"}, wantRedirect: "script.js"},
		{name: "Unknown option", command: "python3", arguments: []string{"--unknown", "script.py"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, err := Analyze(context.Background(), Request{
				Mode: ModeDirect, Command: test.command, Args: test.arguments,
			})
			require.NoError(t, err)
			require.False(t, plan.Complete)
			require.Contains(t, plan.DynamicReasons, ReasonIndirectExecution)
			if test.wantRedirect == "" {
				require.Empty(t, plan.Redirects)
			} else {
				require.Equal(t, test.wantRedirect, plan.Redirects[0].Path)
			}
		})
	}
}

func TestAnalyze_DirectRejectsInvalidNestedShellSource(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	_, err := Analyze(context.Background(), Request{
		Mode: ModeDirect, Command: "sh", Args: []string{"-c", "if"},
	})

	require.EqualError(t, err, "invalid POSIX shell syntax")
}

func TestAnalyze_DirectRejectsOversizedNestedShellSource(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	_, err := Analyze(context.Background(), Request{
		Mode: ModeDirect, Command: "sh", Args: []string{"-c", strings.Repeat("x", MaxSourceBytes+1)},
	})

	require.EqualError(t, err, "nested command source exceeds the analysis limit")
}

func TestAnalyze_POSIXTracksPipelineRelationships(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	plan, err := Analyze(context.Background(), Request{
		Mode:    ModePOSIXShell,
		Command: "curl https://example.com | cat; printf x | sh",
	})

	require.NoError(t, err)
	require.Positive(t, plan.Invocations[0].Pipeline)
	require.Equal(t, plan.Invocations[0].Pipeline, plan.Invocations[1].Pipeline)
	require.Positive(t, plan.Invocations[2].Pipeline)
	require.Equal(t, plan.Invocations[2].Pipeline, plan.Invocations[3].Pipeline)
	require.NotEqual(t, plan.Invocations[0].Pipeline, plan.Invocations[2].Pipeline)
}

func TestAnalyze_POSIXHandlesWrappersBuiltinsAndNestedSource(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	plan, err := Analyze(context.Background(), Request{
		Mode:    ModePOSIXShell,
		Command: `command git status; env FOO=bar make test; sh -c 'printf nested' ignored`,
	})

	require.NoError(t, err)
	require.Equal(t, []string{"git", "env", "make", "sh", "printf"}, getExecutables(plan.Invocations))
	require.True(t, plan.Invocations[2].Indirect)
	require.True(t, plan.Invocations[3].Indirect)
	require.Contains(t, plan.DynamicReasons, ReasonIndirectExecution)
}

func TestAnalyze_POSIXResolvesKnownWrapperAndBuiltinChildren(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	plan, err := Analyze(context.Background(), Request{
		Mode: ModePOSIXShell,
		Command: `command -p git status; exec -a custom sh -c 'printf done'; ` +
			`env -u OLD FOO=bar make test; sudo --user root git status`,
	})

	require.NoError(t, err)
	require.Equal(
		t,
		[]string{"git", "sh", "printf", "env", "make", "sudo", "git"},
		getExecutables(plan.Invocations),
	)
	require.Equal(t, []string{"status"}, plan.Invocations[0].Arguments)
	require.Empty(t, plan.Invocations[0].ResolvedPath)
	require.Equal(t, []string{"-c", "printf done"}, plan.Invocations[1].Arguments)
	require.True(t, plan.Invocations[4].Indirect)
	require.Contains(t, plan.DynamicReasons, ReasonShellState)
	require.False(t, plan.Complete)
}

func TestAnalyze_POSIXHandlesEnvironmentAndPrivilegeWrapperSemantics(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	plan, err := Analyze(context.Background(), Request{
		Mode: ModePOSIXShell,
		Command: "env FOO=bar git status; env --chdir=/tmp git status; " +
			"sudo --chdir=/tmp FOO=bar git status",
	})

	require.NoError(t, err)
	require.Equal(
		t,
		[]string{"env", "git", "env", "git", "sudo", "git"},
		getExecutables(plan.Invocations),
	)
	require.Contains(t, plan.DynamicReasons, ReasonEnvironment)
	require.Contains(t, plan.DynamicReasons, ReasonShellState)
	require.True(t, plan.Invocations[4].Indirect)
	require.False(t, plan.Complete)
}

func TestAnalyze_POSIXTreatsEnvironmentSplitStringAsDynamicExecutable(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	plan, err := Analyze(context.Background(), Request{
		Mode: ModePOSIXShell, Command: `env -S "git status" harmless`,
	})

	require.NoError(t, err)
	require.Equal(t, []string{"env"}, getExecutables(plan.Invocations))
	require.Contains(t, plan.DynamicReasons, ReasonDynamicExecutable)
	require.False(t, plan.Complete)
}

func TestAnalyze_POSIXTracksSudoEditAsFileUpdate(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	plan, err := Analyze(context.Background(), Request{
		Mode: ModePOSIXShell, Command: "sudo --user root --edit -- /etc/hosts",
	})

	require.NoError(t, err)
	require.Equal(t, []string{"sudo"}, getExecutables(plan.Invocations))
	require.Contains(t, plan.Redirects, Redirect{
		Action: RedirectUpdate, Path: "/etc/hosts", Static: true, Line: 1, Column: 1,
	})
	require.False(t, plan.Complete)
}

func TestAnalyze_POSIXTracksInterpreterScriptAsFileRead(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	plan, err := Analyze(context.Background(), Request{
		Mode: ModePOSIXShell, Command: "python3 ./script.py",
	})

	require.NoError(t, err)
	require.Contains(t, plan.Redirects, Redirect{
		Action: RedirectRead, Path: "./script.py", Static: true, Line: 1, Column: 1,
	})
	require.False(t, plan.Complete)
}

func TestAnalyze_POSIXRejectsInvalidNestedShellSource(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	_, err := Analyze(context.Background(), Request{
		Mode: ModePOSIXShell, Command: "sh -c if",
	})

	require.EqualError(t, err, "invalid POSIX shell syntax")
}

func TestAnalyze_POSIXKeepsCommandQueriesAndMarksAmbiguousWrappersIncomplete(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	query, err := Analyze(context.Background(), Request{
		Mode: ModePOSIXShell, Command: "command -v git",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"command"}, getExecutables(query.Invocations))

	ambiguous, err := Analyze(context.Background(), Request{
		Mode: ModePOSIXShell, Command: "env --unknown git status",
	})
	require.NoError(t, err)
	require.False(t, ambiguous.Complete)
	require.Equal(t, []string{"env"}, getExecutables(ambiguous.Invocations))
	require.Contains(t, ambiguous.DynamicReasons, ReasonDynamicExecutable)
}

func TestAnalyze_POSIXHandlesCommandExecAndWrapperEdgeCases(t *testing.T) {
	setLookPath(t, func(name string) (string, error) {
		if name == "relative" {
			return "relative", nil
		}
		return "/bin/" + name, nil
	})
	tests := []struct {
		name            string
		source          string
		wantExecutables []string
		wantComplete    bool
	}{
		{name: "command separator", source: "command -- git status", wantExecutables: []string{"git"}, wantComplete: true},
		{name: "command unknown option", source: "command -x git status", wantExecutables: []string{"command"}, wantComplete: true},
		{name: "exec separator", source: "exec -- git status", wantExecutables: []string{"git"}, wantComplete: true},
		{name: "exec missing alias value", source: "exec -a", wantExecutables: []string{"exec"}, wantComplete: false},
		{name: "exec unknown option", source: "exec --unknown git", wantExecutables: []string{"exec"}, wantComplete: false},
		{name: "environment separator", source: "env -- git status", wantExecutables: []string{"env", "git"}, wantComplete: true},
		{name: "environment missing option value", source: "env --unset", wantExecutables: []string{"env"}, wantComplete: false},
		{name: "sudo missing option value", source: "sudo --user", wantExecutables: []string{"sudo"}, wantComplete: false},
		{name: "relative lookup result", source: "relative argument", wantExecutables: []string{"relative"}, wantComplete: true},
		{
			name: "function declaration", source: "f() { printf body; }; f",
			wantExecutables: []string{"printf", "f"}, wantComplete: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, err := Analyze(context.Background(), Request{Mode: ModePOSIXShell, Command: test.source})
			require.NoError(t, err)
			require.Equal(t, test.wantExecutables, getExecutables(plan.Invocations))
			require.Equal(t, test.wantComplete, plan.Complete)
		})
	}
}

func TestAnalyze_POSIXMarksEvalAndHereDocumentIncomplete(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	for _, source := range []string{
		`eval "git status"`,
		"sh <<'EOF'\nprintf hidden\nEOF",
	} {
		plan, err := Analyze(context.Background(), Request{Mode: ModePOSIXShell, Command: source})
		require.NoError(t, err)
		require.False(t, plan.Complete)
		require.Contains(t, plan.DynamicReasons, ReasonIndirectExecution)
	}
}

func TestAnalyze_POSIXClassifiesRedirectionKinds(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	plan, err := Analyze(context.Background(), Request{
		Mode: ModePOSIXShell,
		Command: "cat < input; printf x > output; printf y >> output; cat <> state; " +
			"printf z 2>&1",
	})

	require.NoError(t, err)
	require.Contains(t, plan.Redirects, Redirect{
		Action: RedirectRead, Path: "input", Static: true, Line: 1, Column: 5,
	})
	require.Contains(t, plan.Redirects, Redirect{
		Action: RedirectCreate, Path: "output", Static: true, Line: 1, Column: 23,
	})
	require.Contains(t, plan.Redirects, Redirect{
		Action: RedirectUpdate, Path: "output", Static: true, Line: 1, Column: 23,
	})
	require.Contains(t, plan.Redirects, Redirect{
		Action: RedirectCreate, Path: "output", Static: true, Line: 1, Column: 42,
	})
	require.Contains(t, plan.Redirects, Redirect{
		Action: RedirectUpdate, Path: "output", Static: true, Line: 1, Column: 42,
	})
	require.Contains(t, plan.Redirects, Redirect{
		Action: RedirectRead, Path: "state", Static: true, Line: 1, Column: 57,
	})
	require.Contains(t, plan.Redirects, Redirect{
		Action: RedirectCreate, Path: "state", Static: true, Line: 1, Column: 57,
	})
	require.Contains(t, plan.Redirects, Redirect{
		Action: RedirectUpdate, Path: "state", Static: true, Line: 1, Column: 57,
	})
	require.Len(t, plan.Redirects, 8)
}

func TestPOSIXHelpers_ClassifyWrapperInterpreterAndStaticInputs(t *testing.T) {
	result, ok := getCommandBuiltinChild([]string{"-V", "git"})
	require.Empty(t, result.child)
	require.True(t, result.query)
	require.False(t, result.defaultPath)
	require.True(t, ok)
	result, ok = getCommandBuiltinChild([]string{"-x", "git"})
	require.False(t, result.query)
	require.False(t, result.defaultPath)
	require.False(t, ok)
	_, ok = getExecBuiltinChild([]string{"-a"})
	require.False(t, ok)
	_, ok = getExecBuiltinChild([]string{"-x", "git"})
	require.False(t, ok)
	child, ok := getExecBuiltinChild([]string{"-c", "-l", "git"})
	require.True(t, ok)
	require.Equal(t, "git", child.Executable)

	index, ok := getWrapperChildIndex("env", []string{"-i", "-u", "OLD", "KEY=value", "git"})
	require.True(t, ok)
	require.Equal(t, 4, index)
	index, ok = getWrapperChildIndex("sudo", []string{"--user=root", "git"})
	require.True(t, ok)
	require.Equal(t, 1, index)
	index, ok = getWrapperChildIndex("env", []string{"--unset=OLD", "git"})
	require.True(t, ok)
	require.Equal(t, 1, index)
	index, ok = getWrapperChildIndex("env", nil)
	require.True(t, ok)
	require.Zero(t, index)
	_, ok = getWrapperChild("env", []string{"--unknown", "git"}, ModeDirect)
	require.False(t, ok)
	_, ok = getChildInvocation(nil, 0, ModeDirect)
	require.False(t, ok)
	_, ok = getChildInvocation([]string{""}, 0, ModeDirect)
	require.False(t, ok)

	require.True(t, isEnvironmentAssignment("_NAME=value"))
	require.False(t, isEnvironmentAssignment("9NAME=value"))
	require.False(t, isEnvironmentAssignment("NAME-WITH-DASH=value"))
	require.False(t, isEnvironmentAssignment("NAME"))
	require.True(t, hasEnvironmentMutation([]string{"--unset=OLD"}))
	require.False(t, hasEnvironmentMutation([]string{"--debug"}))
	require.True(t, hasWrapperWorkingDirectoryChange("sudo", []string{"--chroot=/safe", "git"}))
	require.False(t, hasWrapperWorkingDirectoryChange("sudo", []string{"--user=root", "git"}))

	word, static := getStaticWord(nil)
	require.Empty(t, word)
	require.False(t, static)
	word, static = getStaticWordPart(&syntax.DblQuoted{Dollar: true}, false)
	require.Empty(t, word)
	require.False(t, static)
	require.True(t, isInterpreterCodeOption("python3", "-m"))
	require.True(t, isInterpreterCodeOption("node", "--print=value"))
	require.True(t, isInterpreterCodeOption("ruby", "-eputs"))
	require.False(t, isInterpreterCodeOption("unknown", "-e"))
	consumes, known := getInterpreterOption("bash", "--rcfile")
	require.True(t, consumes)
	require.True(t, known)
	consumes, known = getInterpreterOption("python3", "--check-hash-based-pycs=always")
	require.False(t, consumes)
	require.True(t, known)
	consumes, known = getInterpreterOption("node", "--require=setup")
	require.False(t, consumes)
	require.True(t, known)
	consumes, known = getWrapperOption("sudo", "-V")
	require.False(t, consumes)
	require.True(t, known)
	_, known = getInterpreterOption("node", "--unknown")
	require.False(t, known)

	targets, edit := getSudoEditTargets([]string{"--", "git"})
	require.False(t, edit)
	require.Empty(t, targets)
	require.Equal(t, []string{"/etc/hosts"}, getSudoEditTargetsOrFail(
		t,
		[]string{"--edit", "/etc/hosts"},
	))
	targets, edit = getSudoEditTargets([]string{"--unknown"})
	require.False(t, edit)
	require.Empty(t, targets)
	targets, edit = getSudoEditTargets([]string{"--edit", "--unknown"})
	require.True(t, edit)
	require.Empty(t, targets)
	targets, edit = getSudoEditTargets([]string{"--edit"})
	require.True(t, edit)
	require.Empty(t, targets)
}

func TestPOSIXHelpers_ClassifyInterpreterSources(t *testing.T) {
	tests := []struct {
		name       string
		invocation Invocation
		script     string
		static     bool
		opaque     bool
	}{
		{name: "not interpreter", invocation: Invocation{Executable: "git"}},
		{name: "no source", invocation: Invocation{Executable: "python3"}, opaque: true},
		{name: "separator script", invocation: Invocation{
			Executable: "python3", Arguments: []string{"--", "script.py"},
		}, script: "script.py", static: true},
		{name: "separator without script", invocation: Invocation{
			Executable: "python3", Arguments: []string{"--"},
		}, opaque: true},
		{name: "standard input", invocation: Invocation{
			Executable: "python3", Arguments: []string{"-"},
		}, opaque: true},
		{name: "script", invocation: Invocation{
			Executable: "python3", Arguments: []string{"script.py"},
		}, script: "script.py", static: true},
		{name: "inline code", invocation: Invocation{
			Executable: "python3", Arguments: []string{"-c", "print('ok')"},
		}, opaque: true},
		{name: "unknown option", invocation: Invocation{
			Executable: "python3", Arguments: []string{"--unknown"},
		}, opaque: true},
		{name: "option missing value", invocation: Invocation{
			Executable: "node", Arguments: []string{"--require"},
		}, opaque: true},
		{name: "options without source", invocation: Invocation{
			Executable: "bash", Arguments: []string{"--norc"},
		}, opaque: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script, static, opaque := getInterpreterSource(test.invocation)
			require.Equal(t, test.script, script)
			require.Equal(t, test.static, static)
			require.Equal(t, test.opaque, opaque)
		})
	}
}

func TestPOSIXHelpers_ClassifyInterpreterOptions(t *testing.T) {
	tests := []struct {
		base     string
		option   string
		consumes bool
		known    bool
	}{
		{base: "bash", option: "-o", consumes: true, known: true},
		{base: "bash", option: "-x", known: true},
		{base: "bash", option: "--norc", known: true},
		{base: "python3", option: "-W", consumes: true, known: true},
		{base: "python3", option: "--check-hash-based-pycs", consumes: true, known: true},
		{base: "node", option: "-r", consumes: true, known: true},
		{base: "node", option: "--require", consumes: true, known: true},
		{base: "ruby", option: "-I", consumes: true, known: true},
		{base: "perl", option: "-r", consumes: true, known: true},
		{base: "unknown", option: "-x"},
	}

	for _, test := range tests {
		consumes, known := getInterpreterOption(test.base, test.option)
		require.Equal(t, test.consumes, consumes, "%s %s", test.base, test.option)
		require.Equal(t, test.known, known, "%s %s", test.base, test.option)
	}
}

func TestPOSIXHelpers_ClassifyNestedShellSources(t *testing.T) {
	source, ok := getNestedShellSource(Invocation{
		Executable: "bash", Arguments: []string{"--rcfile", "setup.sh", "-xc", "printf done"},
	})
	require.True(t, ok)
	require.Equal(t, "printf done", source)

	for _, invocation := range []Invocation{
		{Executable: "git", Arguments: []string{"-c", "printf done"}},
		{Executable: "bash", Arguments: []string{"--", "script.sh"}},
		{Executable: "bash", Arguments: []string{"-x", "script.sh"}},
		{Executable: "bash", Arguments: []string{"-c"}},
	} {
		_, ok := getNestedShellSource(invocation)
		require.False(t, ok)
	}
}

func TestPOSIXHelpers_TracksBashRCFileAssignment(t *testing.T) {
	var plan Plan

	addExplicitInterpreterFiles(
		&plan,
		Invocation{Executable: "bash", Arguments: []string{"--rcfile=setup.sh"}},
		2,
		3,
	)

	require.Equal(t, []Redirect{{
		Action: RedirectRead, Path: "setup.sh", Static: true, Line: 2, Column: 3,
	}}, plan.Redirects)
	require.Contains(t, plan.DynamicReasons, ReasonIndirectExecution)
}

func TestAnalyze_POSIXMarksInlineEnvironmentAndSourcedFilesIncomplete(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	plan, err := Analyze(context.Background(), Request{
		Mode:    ModePOSIXShell,
		Command: `PATH=/tmp git status; . ./setup.sh`,
	})

	require.NoError(t, err)
	require.False(t, plan.Complete)
	require.Contains(t, plan.DynamicReasons, ReasonEnvironment)
	require.Contains(t, plan.DynamicReasons, ReasonIndirectExecution)
	require.Empty(t, plan.Invocations[0].ResolvedPath)
	require.Contains(t, plan.Redirects, Redirect{
		Action: RedirectRead, Path: "./setup.sh", Static: true, Line: 1, Column: 23,
	})
}

func TestAnalyze_POSIXMarksGlobbingAndStateChangesIncomplete(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	plan, err := Analyze(context.Background(), Request{
		Mode:    ModePOSIXShell,
		Command: `printf "%s" "*.go"; printf "%s" *.go; cd subdir; git status`,
	})

	require.NoError(t, err)
	require.False(t, plan.Complete)
	require.True(t, plan.Invocations[0].Static)
	require.False(t, plan.Invocations[1].Static)
	require.Contains(t, plan.DynamicReasons, ReasonDynamicArgument)
	require.Contains(t, plan.DynamicReasons, ReasonShellState)
}

func TestAnalyze_POSIXRemovesShellStartupHooks(t *testing.T) {
	t.Setenv("ENV", "/tmp/env-startup")
	t.Setenv("BASH_ENV", "/tmp/bash-startup")
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	plan, err := Analyze(context.Background(), Request{
		Mode: ModePOSIXShell, Command: "git status",
	})

	require.NoError(t, err)
	require.NotContains(t, plan.environment, "ENV=/tmp/env-startup")
	require.NotContains(t, plan.environment, "BASH_ENV=/tmp/bash-startup")
}

func TestAnalyze_PathOverrideMakesPlanIncomplete(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	plan, err := Analyze(context.Background(), Request{
		Command: "git", Environment: map[string]string{"PATH": "/tmp/bin"},
	})

	require.NoError(t, err)
	require.False(t, plan.Complete)
	require.Contains(t, plan.DynamicReasons, ReasonEnvironment)
	require.Equal(t, "/bin/git", plan.Invocations[0].ResolvedPath)
}

func TestAnalyze_POSIXPathOverrideDoesNotClaimResolvedExecutable(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	plan, err := Analyze(context.Background(), Request{
		Mode:        ModePOSIXShell,
		Command:     "git status",
		Environment: map[string]string{"PATH": "/tmp/bin"},
	})

	require.NoError(t, err)
	require.False(t, plan.Complete)
	require.Contains(t, plan.DynamicReasons, ReasonEnvironment)
	require.Empty(t, plan.Invocations[0].ResolvedPath)
}

func TestAnalyze_POSIXEnvironmentWrappersDoNotClaimChangedPathResolution(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	plan, err := Analyze(context.Background(), Request{
		Mode: ModePOSIXShell,
		Command: "env PATH=/tmp git status; env -i git status; " +
			"env -u PATH git status; env --unset=PATH git status",
	})

	require.NoError(t, err)
	require.Equal(
		t,
		[]string{"env", "git", "env", "git", "env", "git", "env", "git"},
		getExecutables(plan.Invocations),
	)
	for _, index := range []int{1, 3, 5, 7} {
		require.Empty(t, plan.Invocations[index].ResolvedPath)
	}
}

func TestAnalyze_POSIXRequestPathOverrideAppliesToWrapperChild(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	plan, err := Analyze(context.Background(), Request{
		Mode:        ModePOSIXShell,
		Command:     "sudo git status",
		Environment: map[string]string{"PATH": "/tmp/bin"},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"sudo", "git"}, getExecutables(plan.Invocations))
	require.Empty(t, plan.Invocations[1].ResolvedPath)
}

func TestHasEnvironmentKey_NormalizesWindowsKeysOnly(t *testing.T) {
	require.True(t, hasEnvironmentKey(map[string]string{" Path ": "value"}, "PATH", "windows"))
	require.False(t, hasEnvironmentKey(map[string]string{" Path ": "value"}, "PATH", "linux"))
	require.False(t, hasEnvironmentKey(map[string]string{"HOME": "value"}, "PATH", "windows"))
}

func TestAnalyze_InheritedLoaderHooksMakePlanIncomplete(t *testing.T) {
	t.Setenv("LD_PRELOAD", "/tmp/morph-test-loader.so")
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	plan, err := Analyze(context.Background(), Request{Command: "git"})

	require.NoError(t, err)
	require.False(t, plan.Complete)
	require.Contains(t, plan.DynamicReasons, ReasonEnvironment)
}

func TestAnalyze_BindsWorkingDirectoryAndEnvironmentIdentity(t *testing.T) {
	root := t.TempDir()
	identityKey := []byte("0123456789abcdef0123456789abcdef")
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	left, err := Analyze(context.Background(), Request{
		Command: "git", WorkspaceRoot: root, CWD: filepath.Join(root, "one"),
		Environment: map[string]string{"MORPH_VALUE": "one"}, IdentityKey: identityKey,
	})
	require.NoError(t, err)
	right, err := Analyze(context.Background(), Request{
		Command: "git", WorkspaceRoot: root, CWD: filepath.Join(root, "two"),
		Environment: map[string]string{"MORPH_VALUE": "two"}, IdentityKey: identityKey,
	})
	require.NoError(t, err)

	require.Equal(t, left.CWDIdentity, right.CWDIdentity)
	require.Regexp(t, `^workspace:[0-9a-f]{64}$`, left.CWDIdentity)
	require.NotEqual(t, left.EnvironmentDigest, right.EnvironmentDigest)
	require.NotEqual(t, left.Digest(), right.Digest())
	require.NotContains(t, left.EnvironmentDigest, "one")
}

func TestAnalyze_KeysEnvironmentAndPlanIdentity(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })
	request := Request{
		Command: "git", Args: []string{"status"}, Environment: map[string]string{"MORPH_VALUE": "secret"},
	}

	left := request
	left.IdentityKey = []byte("0123456789abcdef0123456789abcdef")
	leftPlan, err := Analyze(context.Background(), left)
	require.NoError(t, err)
	right := request
	right.IdentityKey = []byte("abcdef0123456789abcdef0123456789")
	rightPlan, err := Analyze(context.Background(), right)
	require.NoError(t, err)

	require.NotEqual(t, leftPlan.EnvironmentDigest, rightPlan.EnvironmentDigest)
	require.NotEqual(t, leftPlan.Digest(), rightPlan.Digest())
	require.NotContains(t, leftPlan.EnvironmentDigest, "secret")
	require.NotContains(t, leftPlan.Digest(), "secret")
}

func TestAnalyze_RejectsBoundViolations(t *testing.T) {
	_, err := Analyze(context.Background(), Request{
		Command: strings.Repeat("x", MaxSourceBytes+1),
	})
	require.EqualError(t, err, "command source exceeds the analysis limit")

	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })
	_, err = Analyze(context.Background(), Request{
		Command: "printf",
		Args:    make([]string, MaxArguments+1),
	})
	require.EqualError(t, err, "command argument count exceeds the analysis limit")
}

func TestAnalyze_POSIXDiscoversInvocationsRedirectsAndQuotedData(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	plan, err := Analyze(context.Background(), Request{
		Mode:    ModePOSIXShell,
		Command: `echo "git status" && git status | tee status.txt`,
	})

	require.NoError(t, err)
	require.True(t, plan.Complete)
	require.True(t, plan.HasPipeline)
	require.Equal(t, []string{"echo", "git", "tee"}, getExecutables(plan.Invocations))
	require.Equal(t, []string{"git status"}, plan.Invocations[0].Arguments)
	require.Empty(t, plan.Redirects)
	require.NotContains(t, getExecutables(plan.Invocations), "status")
}

func TestAnalyze_POSIXDiscoversNestedCommandsAndRedirects(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	plan, err := Analyze(context.Background(), Request{
		Mode:    ModePOSIXShell,
		Command: `echo "$(git status)" && sh -c 'printf done > result.txt' < input.txt`,
	})

	require.NoError(t, err)
	require.False(t, plan.Complete)
	require.ElementsMatch(t, []string{"echo", "git", "sh", "printf"}, getExecutables(plan.Invocations))
	require.Equal(t, []Redirect{
		{Action: RedirectCreate, Path: "result.txt", Static: true, Line: 1, Column: 13},
		{Action: RedirectUpdate, Path: "result.txt", Static: true, Line: 1, Column: 13},
		{Action: RedirectRead, Path: "input.txt", Static: true, Line: 1, Column: 58},
	}, plan.Redirects)
	require.Contains(t, plan.DynamicReasons, ReasonDynamicArgument)
	require.Contains(t, plan.DynamicReasons, ReasonIndirectExecution)
}

func TestAnalyze_POSIXClassifiesDynamicAndDebuggerInput(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	plan, err := Analyze(context.Background(), Request{
		Mode:    ModePOSIXShell,
		Command: `"$COMMAND" status; curl http://127.0.0.1:9222/json/version > "$OUTPUT"`,
	})

	require.NoError(t, err)
	require.False(t, plan.Complete)
	require.True(t, plan.DebuggerAttach)
	require.Equal(t, "<dynamic>", plan.Invocations[0].Executable)
	require.False(t, plan.Redirects[0].Static)
	require.Contains(t, plan.DynamicReasons, ReasonDynamicExecutable)
	require.Contains(t, plan.DynamicReasons, ReasonDynamicRedirect)
}

func TestAnalyze_DirectClassifiesDebuggerAttachment(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	plan, err := Analyze(context.Background(), Request{
		Mode: ModeDirect, Command: "node",
		Args: []string{"-e", `fetch("http://localhost:9222/json/version")`},
	})

	require.NoError(t, err)
	require.True(t, plan.DebuggerAttach)
}

func TestAnalyze_POSIXDoesNotHideInvocationShadowedByLaterFunction(t *testing.T) {
	setLookPath(t, func(name string) (string, error) { return "/bin/" + name, nil })

	plan, err := Analyze(context.Background(), Request{
		Mode: ModePOSIXShell, Command: "git status; git() { :; }",
	})

	require.NoError(t, err)
	require.Contains(t, getExecutables(plan.Invocations), "git")
	require.Equal(t, []string{"status"}, plan.Invocations[0].Arguments)
}

func TestAnalyze_POSIXRejectsInvalidOrUnsupportedSource(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		message string
	}{
		{name: "syntax", source: "if", message: "invalid POSIX shell syntax"},
		{name: "process substitution", source: "cat <(echo hi)", message: "invalid POSIX shell syntax"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Analyze(context.Background(), Request{Mode: ModePOSIXShell, Command: test.source})
			require.EqualError(t, err, test.message)
		})
	}
}

func TestAnalyze_POSIXRejectsNestingNodeAndWalkLimits(t *testing.T) {
	err := analyzePOSIX(context.Background(), &Plan{}, "printf ok", MaxNesting+1)
	require.EqualError(t, err, "command nesting exceeds the analysis limit")

	_, err = Analyze(context.Background(), Request{
		Mode: ModePOSIXShell, Command: strings.Repeat(":;", MaxNodes/2+1),
	})
	require.EqualError(t, err, "command syntax tree exceeds the analysis limit")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = analyzePOSIX(ctx, &Plan{}, "printf ok", 0)
	require.ErrorIs(t, err, context.Canceled)
}

func TestAnalyze_POSIXWithoutShellIsIncomplete(t *testing.T) {
	setStatPath(t, func(string) (os.FileInfo, error) { return nil, os.ErrNotExist })

	plan, err := Analyze(context.Background(), Request{
		Mode: ModePOSIXShell, Command: "git status", GOOS: "windows",
	})

	require.NoError(t, err)
	require.False(t, plan.Complete)
	require.Empty(t, plan.ShellPath)
	require.Contains(t, plan.DynamicReasons, ReasonShellUnavailable)
	_, err = plan.NewCommand(context.Background())
	require.EqualError(t, err, "POSIX shell is unavailable")
}

func TestAnalyze_RejectsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Analyze(ctx, Request{Command: "tool"})

	require.ErrorIs(t, err, context.Canceled)
}

func TestGetEnvironment_ValidatesOverridesAndWindowsKeys(t *testing.T) {
	tests := []map[string]string{
		{"": "value"},
		{"BAD=KEY": "value"},
		{"BAD\x00KEY": "value"},
		{"KEY": "bad\x00value"},
	}
	for _, environment := range tests {
		_, _, _, err := getEnvironment(environment, ModeDirect, "linux", []byte("identity"))
		require.EqualError(t, err, "command environment contains an invalid entry")
	}

	environment, digest, reasons, err := getEnvironment(
		map[string]string{"Path": `C:\Tools`, "PATH": `C:\SafeTools`},
		ModeDirect,
		"windows",
		[]byte("identity"),
	)
	require.NoError(t, err)
	require.NotEmpty(t, digest)
	require.Contains(t, reasons, ReasonEnvironment)
	pathEntries := 0
	for _, entry := range environment {
		if strings.EqualFold(strings.SplitN(entry, "=", 2)[0], "PATH") {
			pathEntries++
		}
	}
	require.Equal(t, 1, pathEntries)
}

func TestGetEnvironment_TrustedPATHDoesNotMakePlanIncomplete(t *testing.T) {
	for _, mode := range []Mode{ModeDirect, ModePOSIXShell} {
		t.Run(string(mode), func(t *testing.T) {
			plan, err := Analyze(context.Background(), Request{
				Mode:             mode,
				Command:          "pwd",
				Environment:      map[string]string{"PATH": "/usr/bin:/bin"},
				CleanEnvironment: true,
				TrustedPATH:      true,
				ShellPath:        "/bin/sh",
				LookPath: func(string) (string, error) {
					return "/bin/pwd", nil
				},
			})

			require.NoError(t, err)
			require.True(t, plan.Complete)
			require.Empty(t, plan.DynamicReasons)
			require.Equal(t, "/bin/pwd", plan.Invocations[0].ResolvedPath)
		})
	}
}

func TestGetEnvironment_IgnoresMalformedInheritedEntry(t *testing.T) {
	setLoadEnvironment(t, func() []string {
		return []string{"MALFORMED", "PATH=/usr/bin"}
	})

	environment, _, _, err := getEnvironment(nil, ModeDirect, "linux", []byte("identity"))

	require.NoError(t, err)
	require.Equal(t, []string{"PATH=/usr/bin"}, environment)
}

func TestGetCWDIdentity_ClassifiesWorkspaceAndExternalPaths(t *testing.T) {
	root := t.TempDir()
	key := []byte("0123456789abcdef0123456789abcdef")
	cwd, identity, err := getCWDIdentity("", root, key)
	require.NoError(t, err)
	require.Equal(t, root, cwd)
	require.Regexp(t, `^workspace:[0-9a-f]{64}$`, identity)

	cwd, nestedIdentity, err := getCWDIdentity("nested", root, key)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "nested"), cwd)
	require.Equal(t, identity, nestedIdentity)

	external := t.TempDir()
	cwd, identity, err = getCWDIdentity(external, root, key)
	require.NoError(t, err)
	require.Equal(t, external, cwd)
	require.Regexp(t, `^external:[0-9a-f]{64}$`, identity)
}

func TestGetCWDIdentity_ReportsWorkingDirectoryResolutionFailure(t *testing.T) {
	root := t.TempDir()
	setGetAbsolutePath(t, func(path string) (string, error) {
		if strings.HasSuffix(path, "unavailable") {
			return "", errors.New("unavailable")
		}
		return filepath.Abs(path)
	})

	_, _, err := getCWDIdentity("unavailable", root, []byte("identity"))

	require.EqualError(t, err, "working directory could not be resolved")
}

func TestGetShellPath_ValidatesConfiguredExecutable(t *testing.T) {
	require.Empty(t, getShellPath("", "windows"))
	require.Empty(t, getShellPath("relative/sh", "linux"))

	directory := t.TempDir()
	require.Empty(t, getShellPath(directory, "linux"))

	executable := filepath.Join(t.TempDir(), "sh")
	require.NoError(t, os.WriteFile(executable, []byte("#!/bin/sh\n"), 0o700))
	require.Equal(t, executable, getShellPath(" "+executable+" ", "linux"))
}

func TestNewIdentityKey_FailsClosedWhenSecureRandomnessIsUnavailable(t *testing.T) {
	original := readIdentityRandom
	readIdentityRandom = func([]byte) (int, error) {
		return 0, errors.New("unavailable")
	}
	t.Cleanup(func() { readIdentityRandom = original })

	require.Panics(t, func() {
		newIdentityKey()
	})
}

func setLookPath(t *testing.T, replacement func(string) (string, error)) {
	t.Helper()
	original := lookPath
	lookPath = replacement
	t.Cleanup(func() { lookPath = original })
}

func setStatPath(t *testing.T, replacement func(string) (os.FileInfo, error)) {
	t.Helper()
	original := statPath
	statPath = replacement
	t.Cleanup(func() { statPath = original })
}

func setGetWorkingDirectory(t *testing.T, replacement func() (string, error)) {
	t.Helper()
	original := getWorkingDirectory
	getWorkingDirectory = replacement
	t.Cleanup(func() { getWorkingDirectory = original })
}

func setGetAbsolutePath(t *testing.T, replacement func(string) (string, error)) {
	t.Helper()
	original := getAbsolutePath
	getAbsolutePath = replacement
	t.Cleanup(func() { getAbsolutePath = original })
}

func setLoadEnvironment(t *testing.T, replacement func() []string) {
	t.Helper()
	original := loadEnvironment
	loadEnvironment = replacement
	t.Cleanup(func() { loadEnvironment = original })
}

func getExecutables(invocations []Invocation) []string {
	values := make([]string, len(invocations))
	for index, invocation := range invocations {
		values[index] = invocation.Executable
	}
	return values
}

func getSudoEditTargetsOrFail(t *testing.T, arguments []string) []string {
	t.Helper()
	targets, edit := getSudoEditTargets(arguments)
	require.True(t, edit)
	return targets
}
