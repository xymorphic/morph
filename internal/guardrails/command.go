package guardrails

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	commandplan "github.com/xymorphic/morph/internal/command"
)

// CommandPolicy defines command policy settings.
type CommandPolicy struct {
	AllowCommands []commandplan.Selector
	AskCommands   []commandplan.Selector
	DenyCommands  []commandplan.Selector
	invalid       bool
}

// CommandDecision is the result of evaluating a shell command against policy.
type CommandDecision string

const (
	CommandAllowed          CommandDecision = "allowed"
	CommandApprovalRequired CommandDecision = "approval_required"
	CommandDenied           CommandDecision = "denied"
)

// CommandEvaluation describes command evaluation.
type CommandEvaluation struct {
	Decision CommandDecision
	Rule     string
	Reason   string
}

type dangerousCommandPattern struct {
	reason  string
	pattern *regexp.Regexp
	match   func(string) bool
}

var builtInApprovalPatterns = []dangerousCommandPattern{
	{
		reason:  "dangerous destructive command",
		pattern: regexp.MustCompile(`^rm (?:-[^ ]*[rR][^ ]*|--recursive)(?: .*?)?/$`),
	},
	{
		reason:  "delete in root path",
		pattern: regexp.MustCompile(`^rm(?: .+)? /$`),
	},
	{
		reason:  "world-writable permissions command",
		pattern: regexp.MustCompile(`^chmod (?:777|0777|a\+rwx)( |$)`),
	},
	{
		reason:  "recursive world-writable permissions command",
		pattern: regexp.MustCompile(`^chmod (?:-[^ ]*R|--recursive\b).*(?:\b777\b|\b0777\b|a\+rwx\b)`),
	},
	{
		reason:  "recursive chown to root command",
		pattern: regexp.MustCompile(`^chown (?:-[^ ]*R|--recursive\b).* root\b`),
	},
	{
		reason:  "privileged shutdown command",
		pattern: regexp.MustCompile(`^sudo (reboot|shutdown|poweroff)( |$)`),
	},
	{
		reason:  "disk formatting command",
		pattern: regexp.MustCompile(`^mkfs(\.[^ ]+)?( |$)`),
	},
	{
		reason:  "shutdown command",
		pattern: regexp.MustCompile(`^(shutdown|reboot|poweroff|halt)( |$)`),
	},
	{
		reason:  "disk copy command",
		pattern: regexp.MustCompile(`^dd( |$).*?\bif=`),
	},
	{
		reason:  "sql drop command",
		pattern: regexp.MustCompile(`\bDROP (TABLE|DATABASE)\b`),
	},
	{
		reason: "sql delete without where command",
		match: func(joined string) bool {
			return deleteFromPattern.MatchString(joined) && !wherePattern.MatchString(joined)
		},
	},
	{
		reason:  "sql truncate command",
		pattern: regexp.MustCompile(`\bTRUNCATE TABLE\b`),
	},
	{
		reason:  "system service disable command",
		pattern: regexp.MustCompile(`^(?:sudo )?systemctl(?: --[^ ]+)* (stop|disable|mask)( |$)`),
	},
	{
		reason:  "kill all processes command",
		pattern: regexp.MustCompile(`^kill (?:-9|-KILL|-s KILL) -1$`),
	},
	{
		reason:  "force kill processes command",
		pattern: regexp.MustCompile(`^pkill (?:-9|-KILL|--signal(?:=| )9|--signal(?:=| )KILL)\b`),
	},
	{
		reason: "fork bomb command",
		match:  isForkBombCommand,
	},
	{
		reason:  "overwrite system file via tee",
		pattern: regexp.MustCompile(`\btee\b.*(/etc/|/dev/sd|\.ssh/|\.morph/\.env)`),
	},
	{
		reason:  "xargs with rm command",
		pattern: regexp.MustCompile(`\bxargs\b.*\brm\b`),
	},
	{
		reason:  "shell execution via flag",
		pattern: regexp.MustCompile(`^(bash|sh|zsh|ksh) -[^ ]*c( |$)`),
	},
	{
		reason:  "script execution via flag",
		pattern: regexp.MustCompile(`^(python[23]?|perl|ruby|node) -(c|e)( |$)`),
	},
	{
		reason:  "find destructive action command",
		pattern: regexp.MustCompile(`^find\b.*(\-exec rm\b|\-delete\b)`),
	},
	{
		reason:  "credential exfiltration command",
		pattern: regexp.MustCompile(`\bcat (\.env|\.netrc)\b`),
	},
}

var (
	deleteFromPattern = regexp.MustCompile(`\bDELETE FROM\b`)
	wherePattern      = regexp.MustCompile(`\bWHERE\b`)
	forkBombPatterns  = []*regexp.Regexp{
		regexp.MustCompile(`:\(\)\s*\{\s*:\|:\s*&\s*\};:`),
		regexp.MustCompile(`\b\w+\(\)\s*\{\s*\w+\|\w+\s*&\s*\};\s*\w+`),
		regexp.MustCompile(`%0\|%0`),
		regexp.MustCompile(`\b(start|call)\s+.*%0\b`),
		regexp.MustCompile(`\bfor /l\b.*\bstart\b.*\bcmd\b`),
		regexp.MustCompile(`\bpython(?:\d+(?:\.\d+)?)?\b.*\b(subprocess\.(Popen|run)|os\.(fork|spawn|system)|multiprocessing\.Process)\b.*(__file__|sys\.argv\[0\]|python)`),
		regexp.MustCompile(`\b(node|ruby|perl|php)\b.*\b(spawn|fork|exec|system)\b.*(process\.argv|__FILE__|\$0|argv)`),
	}
)

func (p CommandPolicy) Normalize() CommandPolicy {
	var err error
	if p.AllowCommands, err = commandplan.NormalizeSelectors(p.AllowCommands); err != nil {
		p.invalid = true
	}
	if p.AskCommands, err = commandplan.NormalizeSelectors(p.AskCommands); err != nil {
		p.invalid = true
	}
	if p.DenyCommands, err = commandplan.NormalizeDenySelectors(p.DenyCommands); err != nil {
		p.invalid = true
	}
	return p
}

func EvaluateCommandPlan(policy CommandPolicy, plan commandplan.Plan) CommandEvaluation {
	policy = policy.Normalize()
	if policy.invalid {
		return CommandEvaluation{Decision: CommandDenied, Reason: "command policy contains an invalid typed selector"}
	}
	if len(plan.Invocations) == 0 {
		return CommandEvaluation{Decision: CommandDenied, Reason: "command plan has no executable invocation"}
	}

	for _, invocation := range plan.Invocations {
		if rule := matchCommandSelector(policy.DenyCommands, plan.Target(invocation)); rule != "" {
			return CommandEvaluation{Decision: CommandDenied, Rule: rule, Reason: "matched typed deny rule"}
		}
	}
	if reason := getStructuralApprovalReason(plan); reason != "" {
		return CommandEvaluation{Decision: CommandApprovalRequired, Reason: reason}
	}
	for _, invocation := range plan.Invocations {
		if rule := matchCommandSelector(policy.AskCommands, plan.Target(invocation)); rule != "" {
			return CommandEvaluation{
				Decision: CommandApprovalRequired,
				Rule:     rule,
				Reason:   "matched typed approval rule",
			}
		}
	}
	if rule, ok := matchAllCommandSelectors(policy.AllowCommands, plan); ok {
		return CommandEvaluation{Decision: CommandAllowed, Rule: rule}
	}

	return CommandEvaluation{Decision: CommandAllowed}
}

func matchCommandSelector(selectors []commandplan.Selector, target commandplan.Target) string {
	for _, selector := range selectors {
		if selector.Matches(target) {
			sum := sha256.Sum256([]byte(selector.Fingerprint()))
			return "command-selector:" + hex.EncodeToString(sum[:6])
		}
	}
	return ""
}

func matchAllCommandSelectors(selectors []commandplan.Selector, plan commandplan.Plan) (string, bool) {
	if len(selectors) == 0 {
		return "", false
	}
	var matched string
	for _, invocation := range plan.Invocations {
		rule := matchCommandSelector(selectors, plan.Target(invocation))
		if rule == "" {
			return "", false
		}
		if matched == "" {
			matched = rule
		}
	}
	return matched, true
}

func getStructuralApprovalReason(plan commandplan.Plan) string {
	if plan.DebuggerAttach {
		return "command attempts to attach to Morph's managed browser"
	}
	if hasDownloadToShellPipeline(plan) {
		return "downloaded content is piped to a shell"
	}
	if !plan.Complete {
		return "command structure is incomplete and requires explicit approval"
	}
	for _, redirect := range plan.Redirects {
		path := redirect.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(plan.CWD, path)
		}
		path = filepath.ToSlash(filepath.Clean(path))
		if path == "/dev/null" {
			continue
		}
		if redirect.Action == commandplan.RedirectRead {
			if isSensitiveReadPath(path) {
				return "command reads credentials from a protected path"
			}
			continue
		}
		if path == "/etc" || strings.HasPrefix(path, "/etc/") ||
			strings.HasPrefix(path, "/dev/") ||
			strings.Contains(path, "/.ssh/") ||
			strings.Contains(path, "/.morph/") {
			return "command redirects data to a protected path"
		}
	}
	for _, invocation := range plan.Invocations {
		tokens := append([]string{filepath.Base(invocation.Executable)}, invocation.Arguments...)
		if reason := builtInApprovalRequiredForInvocation(tokens); reason != "" {
			return reason
		}
	}
	return ""
}

func builtInApprovalRequiredForInvocation(tokens []string) string {
	if len(tokens) == 0 {
		return ""
	}
	executable := strings.ToLower(filepath.Base(tokens[0]))
	if executable == "dd" && hasBlockDeviceOutput(tokens[1:]) {
		return "command writes directly to a block device"
	}
	if isCredentialReadInvocation(executable, tokens[1:]) {
		return "command reads credential files"
	}
	joined := strings.Join(tokens, " ")
	for _, candidate := range builtInApprovalPatterns {
		if !isDangerousPatternApplicable(candidate.reason, executable) {
			continue
		}
		if candidate.match != nil && candidate.match(joined) ||
			candidate.pattern != nil && candidate.pattern.MatchString(joined) {
			return candidate.reason
		}
	}
	return ""
}

func hasBlockDeviceOutput(arguments []string) bool {
	for _, argument := range arguments {
		target, ok := strings.CutPrefix(argument, "of=")
		if ok && isBlockDevicePath(filepath.ToSlash(filepath.Clean(target))) {
			return true
		}
	}
	return false
}

func isBlockDevicePath(path string) bool {
	return strings.HasPrefix(path, "/dev/sd") ||
		strings.HasPrefix(path, "/dev/hd") ||
		strings.HasPrefix(path, "/dev/vd") ||
		strings.HasPrefix(path, "/dev/nvme") ||
		strings.HasPrefix(path, "/dev/mmcblk") ||
		strings.HasPrefix(path, "/dev/disk") ||
		strings.HasPrefix(path, "/dev/rdisk") ||
		strings.HasPrefix(path, "/dev/mapper/") ||
		strings.HasPrefix(strings.ToLower(path), "//./physicaldrive")
}

func isCredentialReadInvocation(executable string, arguments []string) bool {
	if !slices.Contains([]string{"cat", "head", "tail"}, executable) {
		return false
	}
	for _, argument := range arguments {
		if strings.HasPrefix(argument, "-") {
			continue
		}
		if isSensitiveReadPath(filepath.ToSlash(filepath.Clean(argument))) {
			return true
		}
	}
	return false
}

func isSensitiveReadPath(path string) bool {
	base := filepath.Base(path)
	return base == ".env" ||
		base == ".netrc" ||
		strings.Contains(path, "/.ssh/") ||
		strings.Contains(path, "/.morph/credentials") ||
		strings.Contains(path, "/.morph/auth")
}

func isDangerousPatternApplicable(reason string, executable string) bool {
	switch reason {
	case "dangerous destructive command", "delete in root path":
		return executable == "rm"
	case "world-writable permissions command", "recursive world-writable permissions command":
		return executable == "chmod"
	case "recursive chown to root command":
		return executable == "chown"
	case "privileged shutdown command":
		return executable == "sudo"
	case "disk formatting command":
		return strings.HasPrefix(executable, "mkfs")
	case "shutdown command":
		return slices.Contains([]string{"shutdown", "reboot", "poweroff", "halt"}, executable)
	case "disk copy command":
		return executable == "dd"
	case "sql drop command", "sql delete without where command", "sql truncate command":
		return slices.Contains([]string{"psql", "mysql", "sqlite", "sqlite3"}, executable)
	case "system service disable command":
		return executable == "systemctl" || executable == "sudo"
	case "kill all processes command":
		return executable == "kill"
	case "force kill processes command":
		return executable == "pkill"
	case "fork bomb command":
		return slices.Contains(
			[]string{"sh", "bash", "zsh", "ksh", "cmd", "python", "python2", "python3", "node", "ruby", "perl", "php"},
			executable,
		)
	case "overwrite system file via tee":
		return executable == "tee"
	case "xargs with rm command":
		return executable == "xargs"
	case "shell execution via flag":
		return slices.Contains([]string{"sh", "bash", "zsh", "ksh"}, executable)
	case "script execution via flag":
		return slices.Contains([]string{"python", "python2", "python3", "perl", "ruby", "node"}, executable)
	case "find destructive action command":
		return executable == "find"
	case "credential exfiltration command":
		return executable == "cat"
	default:
		return true
	}
}

func hasDownloadToShellPipeline(plan commandplan.Plan) bool {
	if !plan.HasPipeline {
		return false
	}
	downloads := make(map[int]struct{})
	for _, invocation := range plan.Invocations {
		if invocation.Pipeline > 0 &&
			slices.Contains([]string{"curl", "wget"}, strings.ToLower(filepath.Base(invocation.Executable))) {
			downloads[invocation.Pipeline] = struct{}{}
		}
	}
	for _, invocation := range plan.Invocations {
		if _, ok := downloads[invocation.Pipeline]; ok &&
			slices.Contains([]string{"sh", "bash", "zsh", "ksh"}, strings.ToLower(filepath.Base(invocation.Executable))) {
			return true
		}
	}
	return false
}

func isForkBombCommand(joined string) bool {
	for _, pattern := range forkBombPatterns {
		if pattern.MatchString(joined) {
			return true
		}
	}

	return false
}
