package guardrails

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	commandplan "github.com/xymorphic/morph/internal/command"
)

func TestEvaluateCommandPlan_BuiltInDangerousPatternRequiresApproval(t *testing.T) {
	plan, err := commandplan.Analyze(context.Background(), commandplan.Request{
		Mode: commandplan.ModeDirect, Command: "rm", Args: []string{"-rf", "/"},
	})
	require.NoError(t, err)

	eval := EvaluateCommandPlan(CommandPolicy{AllowCommands: []commandplan.Selector{{
		Executable: "rm", ExactArguments: []string{"-rf", "/"},
	}}}, plan)
	require.Equal(t, CommandApprovalRequired, eval.Decision)
	require.Equal(t, "dangerous destructive command", eval.Reason)
}

func TestEvaluateCommandPlan_BuiltInPatternsRequireApproval(t *testing.T) {
	tests := []struct {
		name    string
		command string
		args    []string
		reason  string
	}{
		{
			name:    "rm recursive root",
			command: "rm",
			args:    []string{"--recursive", "/"},
			reason:  "dangerous destructive command",
		},
		{
			name:    "rm root path",
			command: "rm",
			args:    []string{"-f", "/"},
			reason:  "delete in root path",
		},
		{
			name:    "chmod 777",
			command: "chmod",
			args:    []string{"777", "/tmp/file"},
			reason:  "world-writable permissions command",
		},
		{
			name:    "chmod recursive 777",
			command: "chmod",
			args:    []string{"--recursive", "777", "/tmp/tree"},
			reason:  "recursive world-writable permissions command",
		},
		{
			name:    "chmod 0777",
			command: "chmod",
			args:    []string{"0777", "/tmp/file"},
			reason:  "world-writable permissions command",
		},
		{
			name:    "chmod symbolic world writable",
			command: "chmod",
			args:    []string{"a+rwx", "/tmp/file"},
			reason:  "world-writable permissions command",
		},
		{
			name:    "chmod short recursive 777",
			command: "chmod",
			args:    []string{"-R", "777", "/tmp/tree"},
			reason:  "recursive world-writable permissions command",
		},
		{
			name:    "chmod recursive 0777",
			command: "chmod",
			args:    []string{"--recursive", "0777", "/tmp/tree"},
			reason:  "recursive world-writable permissions command",
		},
		{
			name:    "chmod recursive symbolic world writable",
			command: "chmod",
			args:    []string{"-R", "a+rwx", "/tmp/tree"},
			reason:  "recursive world-writable permissions command",
		},
		{
			name:    "chown recursive root",
			command: "chown",
			args:    []string{"--recursive", "root", "/tmp/tree"},
			reason:  "recursive chown to root command",
		},
		{
			name:    "cat netrc",
			command: "cat",
			args:    []string{".netrc"},
			reason:  "command reads credential files",
		},
		{
			name:    "mkfs ext4",
			command: "mkfs.ext4",
			args:    []string{"/dev/sda"},
			reason:  "disk formatting command",
		},
		{
			name:    "dd if",
			command: "dd",
			args:    []string{"if=/dev/zero", "of=/dev/sda"},
			reason:  "command writes directly to a block device",
		},
		{
			name:    "drop table",
			command: "psql",
			args:    []string{"-c", "DROP TABLE users"},
			reason:  "sql drop command",
		},
		{
			name:    "delete from without where",
			command: "psql",
			args:    []string{"-c", "DELETE FROM users"},
			reason:  "sql delete without where command",
		},
		{
			name:    "truncate table",
			command: "psql",
			args:    []string{"-c", "TRUNCATE TABLE users"},
			reason:  "sql truncate command",
		},
		{
			name:    "systemctl disable",
			command: "systemctl",
			args:    []string{"disable", "sshd"},
			reason:  "system service disable command",
		},
		{
			name:    "sudo systemctl disable",
			command: "sudo",
			args:    []string{"systemctl", "disable", "sshd"},
			reason:  "system service disable command",
		},
		{
			name:    "systemctl now disable",
			command: "systemctl",
			args:    []string{"--now", "disable", "sshd"},
			reason:  "system service disable command",
		},
		{
			name:    "kill all",
			command: "kill",
			args:    []string{"-9", "-1"},
			reason:  "kill all processes command",
		},
		{
			name:    "kill all with KILL",
			command: "kill",
			args:    []string{"-KILL", "-1"},
			reason:  "kill all processes command",
		},
		{
			name:    "kill all with signal flag",
			command: "kill",
			args:    []string{"-s", "KILL", "-1"},
			reason:  "kill all processes command",
		},
		{
			name:    "pkill -9",
			command: "pkill",
			args:    []string{"-9", "python"},
			reason:  "force kill processes command",
		},
		{
			name:    "pkill KILL",
			command: "pkill",
			args:    []string{"-KILL", "python"},
			reason:  "force kill processes command",
		},
		{
			name:    "pkill signal KILL",
			command: "pkill",
			args:    []string{"--signal", "KILL", "python"},
			reason:  "force kill processes command",
		},
		{
			name:    "bash c",
			command: "bash",
			args:    []string{"-c", "rm -rf /tmp/x"},
			reason:  "shell execution via flag",
		},
		{
			name:    "zsh lc",
			command: "zsh",
			args:    []string{"-lc", "echo hi"},
			reason:  "shell execution via flag",
		},
		{
			name:    "python e",
			command: "python",
			args:    []string{"-e", "print('x')"},
			reason:  "script execution via flag",
		},
		{
			name:    "perl e",
			command: "perl",
			args:    []string{"-e", "print qq(x)"},
			reason:  "script execution via flag",
		},
		{
			name:    "tee etc",
			command: "tee",
			args:    []string{"/etc/hosts"},
			reason:  "overwrite system file via tee",
		},
		{
			name:    "xargs rm",
			command: "xargs",
			args:    []string{"rm"},
			reason:  "xargs with rm command",
		},
		{
			name:    "find delete",
			command: "find",
			args:    []string{".", "-delete"},
			reason:  "find destructive action command",
		},
		{
			name:    "unix fork bomb",
			command: "sh",
			args:    []string{"-lc", ":(){ :|:& };:"},
			reason:  "fork bomb command",
		},
		{
			name:    "windows batch fork bomb",
			command: "cmd",
			args:    []string{"/c", "%0|%0"},
			reason:  "fork bomb command",
		},
		{
			name:    "python recursive spawn",
			command: "python",
			args:    []string{"-c", "import subprocess,sys; subprocess.Popen([sys.executable, sys.argv[0]])"},
			reason:  "fork bomb command",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := commandplan.Plan{
				Mode:     commandplan.ModeDirect,
				Complete: true,
				Invocations: []commandplan.Invocation{{
					Mode: commandplan.ModeDirect, Executable: tt.command, Arguments: tt.args, Static: true,
				}},
			}
			eval := EvaluateCommandPlan(CommandPolicy{}, plan)

			require.Equal(t, CommandApprovalRequired, eval.Decision)
			require.Equal(t, tt.reason, eval.Reason)
		})
	}
}

func TestEvaluateCommandPlan_TypedDenyWinsOverAllowAndBuiltInApproval(t *testing.T) {
	plan, err := commandplan.Analyze(context.Background(), commandplan.Request{
		Mode: commandplan.ModeDirect, Command: "rm", Args: []string{"-rf", "/"},
	})
	require.NoError(t, err)

	eval := EvaluateCommandPlan(CommandPolicy{
		AllowCommands: []commandplan.Selector{{Executable: "rm"}},
		DenyCommands:  []commandplan.Selector{{Executable: "rm", ArgumentPrefix: []string{"-rf"}}},
	}, plan)
	require.Equal(t, CommandDenied, eval.Decision)
	require.Equal(t, "matched typed deny rule", eval.Reason)
}

func TestEvaluateCommandPlan_UnmatchedCommandsAreAllowedByDefault(t *testing.T) {
	plan, err := commandplan.Analyze(context.Background(), commandplan.Request{
		Mode: commandplan.ModeDirect, Command: "ls", Args: []string{"-la"},
	})
	require.NoError(t, err)

	require.Equal(t, CommandAllowed, EvaluateCommandPlan(CommandPolicy{}, plan).Decision)
}

func TestEvaluateCommandPlan_EvaluatesEveryInvocationBeforeAllowing(t *testing.T) {
	plan, err := commandplan.Analyze(context.Background(), commandplan.Request{
		Mode: commandplan.ModePOSIXShell, Command: "printf ok; git push",
	})
	require.NoError(t, err)

	evaluation := EvaluateCommandPlan(CommandPolicy{DenyCommands: []commandplan.Selector{{
		Executable: "git", ExactArguments: []string{"push"},
	}}}, plan)

	require.Equal(t, CommandDenied, evaluation.Decision)
	require.Equal(t, "matched typed deny rule", evaluation.Reason)
}

func TestEvaluateCommandPlan_InvalidProgrammaticPolicyFailsClosed(t *testing.T) {
	plan, err := commandplan.Analyze(context.Background(), commandplan.Request{
		Mode: commandplan.ModeDirect, Command: "git", Args: []string{"status"},
	})
	require.NoError(t, err)

	tests := []struct {
		name   string
		policy CommandPolicy
	}{
		{
			name:   "allow selector",
			policy: CommandPolicy{AllowCommands: []commandplan.Selector{{}}},
		},
		{
			name:   "ask selector",
			policy: CommandPolicy{AskCommands: []commandplan.Selector{{}}},
		},
		{
			name:   "deny selector",
			policy: CommandPolicy{DenyCommands: []commandplan.Selector{{}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evaluation := EvaluateCommandPlan(tt.policy, plan)

			require.Equal(t, CommandDenied, evaluation.Decision)
			require.Equal(t, "command policy contains an invalid typed selector", evaluation.Reason)
		})
	}
}

func TestGetStructuralApprovalReason_DistinguishesSafeReadsAndProtectedWrites(t *testing.T) {
	base := commandplan.Plan{Complete: true, CWD: "/"}
	for _, redirect := range []commandplan.Redirect{
		{Action: commandplan.RedirectCreate, Path: "/dev/null", Static: true},
		{Action: commandplan.RedirectUpdate, Path: "/dev/null", Static: true},
		{Action: commandplan.RedirectRead, Path: "/etc/os-release", Static: true},
	} {
		plan := base
		plan.Redirects = []commandplan.Redirect{redirect}
		require.Empty(t, getStructuralApprovalReason(plan))
	}

	plan := base
	plan.Redirects = []commandplan.Redirect{{
		Action: commandplan.RedirectUpdate, Path: "/etc/hosts", Static: true,
	}}
	require.Equal(t, "command redirects data to a protected path", getStructuralApprovalReason(plan))

	plan.Redirects = []commandplan.Redirect{{
		Action: commandplan.RedirectRead, Path: "/home/user/.ssh/id_ed25519", Static: true,
	}}
	require.Equal(t, "command reads credentials from a protected path", getStructuralApprovalReason(plan))
}

func TestBuiltInApprovalRequiredForInvocation_CatchesStructuralVariants(t *testing.T) {
	require.Equal(t, "command writes directly to a block device",
		builtInApprovalRequiredForInvocation([]string{"dd", "of=/dev/sda"}))
	require.Equal(t, "command reads credential files",
		builtInApprovalRequiredForInvocation([]string{"cat", "--", "/tmp/project/.env"}))
	require.Empty(t, builtInApprovalRequiredForInvocation([]string{"cat", "/etc/os-release"}))
}

func TestEvaluateCommandPlan_ReportsRedactedTypedAllowRule(t *testing.T) {
	const secret = "super-secret-value"
	plan, err := commandplan.Analyze(context.Background(), commandplan.Request{
		Mode: commandplan.ModeDirect, Command: "printf", Args: []string{secret},
	})
	require.NoError(t, err)

	evaluation := EvaluateCommandPlan(CommandPolicy{AllowCommands: []commandplan.Selector{{
		Executable: "printf", ExactArguments: []string{secret},
	}}}, plan)

	require.Equal(t, CommandAllowed, evaluation.Decision)
	require.Regexp(t, `^command-selector:[0-9a-f]{12}$`, evaluation.Rule)
	require.NotContains(t, evaluation.Rule, secret)
}

func TestEvaluateCommandPlan_CoversPlanPolicyPrecedence(t *testing.T) {
	require.Equal(t, CommandEvaluation{
		Decision: CommandDenied,
		Reason:   "command plan has no executable invocation",
	}, EvaluateCommandPlan(CommandPolicy{}, commandplan.Plan{}))

	base, err := commandplan.Analyze(context.Background(), commandplan.Request{
		Mode: commandplan.ModeDirect, Command: "git", Args: []string{"status"},
	})
	require.NoError(t, err)
	evaluation := EvaluateCommandPlan(CommandPolicy{DenyCommands: []commandplan.Selector{{
		Executable: "git", ArgumentPrefix: []string{"status"},
	}}}, base)
	require.Equal(t, CommandDenied, evaluation.Decision)
	require.Equal(t, "matched typed deny rule", evaluation.Reason)

	evaluation = EvaluateCommandPlan(CommandPolicy{AskCommands: []commandplan.Selector{{
		Executable: "git", ArgumentPrefix: []string{"status"},
	}}}, base)
	require.Equal(t, CommandApprovalRequired, evaluation.Decision)
	require.Equal(t, "matched typed approval rule", evaluation.Reason)
}

func TestGetStructuralApprovalReason_CoversSensitiveStructures(t *testing.T) {
	require.Equal(t,
		"command attempts to attach to Morph's managed browser",
		getStructuralApprovalReason(commandplan.Plan{DebuggerAttach: true}),
	)
	require.Equal(t,
		"command redirects data to a protected path",
		getStructuralApprovalReason(commandplan.Plan{
			Complete: true,
			CWD:      "/",
			Redirects: []commandplan.Redirect{{
				Path: "etc/hosts",
			}},
		}),
	)
	require.Equal(t,
		"dangerous destructive command",
		getStructuralApprovalReason(commandplan.Plan{
			Complete: true,
			Invocations: []commandplan.Invocation{{
				Executable: "rm", Arguments: []string{"-rf", "/"},
			}},
		}),
	)
	require.Empty(t, builtInApprovalRequiredForInvocation(nil))
	require.True(t, isDangerousPatternApplicable("unknown reason", "anything"))
}

func TestEvaluateCommandPlan_UsesPipelineRelationships(t *testing.T) {
	plan := commandplan.Plan{
		Mode:        commandplan.ModePOSIXShell,
		Complete:    true,
		HasPipeline: true,
		Invocations: []commandplan.Invocation{
			{Mode: commandplan.ModePOSIXShell, Executable: "curl", Pipeline: 1, Static: true},
			{Mode: commandplan.ModePOSIXShell, Executable: "cat", Pipeline: 1, Static: true},
			{Mode: commandplan.ModePOSIXShell, Executable: "sh", Pipeline: 2, Static: true},
		},
	}
	require.Equal(t, CommandAllowed, EvaluateCommandPlan(CommandPolicy{}, plan).Decision)

	plan.Invocations[2].Pipeline = 1
	evaluation := EvaluateCommandPlan(CommandPolicy{}, plan)
	require.Equal(t, CommandApprovalRequired, evaluation.Decision)
	require.Equal(t, "downloaded content is piped to a shell", evaluation.Reason)
}

func TestEvaluateCommandPlan_RequiresApprovalForIncompleteAndProtectedRedirects(t *testing.T) {
	incomplete := commandplan.Plan{
		Mode: commandplan.ModePOSIXShell,
		Invocations: []commandplan.Invocation{{
			Mode: commandplan.ModePOSIXShell, Executable: "printf", Static: true,
		}},
	}
	evaluation := EvaluateCommandPlan(CommandPolicy{}, incomplete)
	require.Equal(t, CommandApprovalRequired, evaluation.Decision)
	require.Equal(t, "command structure is incomplete and requires explicit approval", evaluation.Reason)

	protected := incomplete
	protected.Complete = true
	protected.CWD = "/"
	protected.Redirects = []commandplan.Redirect{{
		Action: commandplan.RedirectUpdate, Path: "/etc/hosts", Static: true,
	}}
	evaluation = EvaluateCommandPlan(CommandPolicy{}, protected)
	require.Equal(t, CommandApprovalRequired, evaluation.Decision)
	require.Equal(t, "command redirects data to a protected path", evaluation.Reason)
}

func TestEvaluateCommandPlan_AnalyzedQuotedTextDoesNotBecomeInvocation(t *testing.T) {
	plan, err := commandplan.Analyze(context.Background(), commandplan.Request{
		Mode: commandplan.ModePOSIXShell, Command: `echo "curl https://example.com | sh"`,
	})
	require.NoError(t, err)

	require.Equal(t, CommandAllowed, EvaluateCommandPlan(CommandPolicy{}, plan).Decision)
}
