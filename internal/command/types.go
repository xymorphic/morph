package command

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

type Mode string

const (
	ModeDirect     Mode = "direct"
	ModePOSIXShell Mode = "posix_shell"
)

type DynamicReason string

const (
	ReasonDynamicExecutable DynamicReason = "dynamic_executable"
	ReasonDynamicArgument   DynamicReason = "dynamic_argument"
	ReasonDynamicRedirect   DynamicReason = "dynamic_redirect"
	ReasonIndirectExecution DynamicReason = "indirect_execution"
	ReasonEnvironment       DynamicReason = "execution_environment"
	ReasonShellState        DynamicReason = "shell_state"
	ReasonShellUnavailable  DynamicReason = "shell_unavailable"
)

type RedirectAction string

const (
	RedirectRead   RedirectAction = "read"
	RedirectCreate RedirectAction = "create"
	RedirectUpdate RedirectAction = "update"
)

type Invocation struct {
	Mode         Mode
	Executable   string
	ResolvedPath string
	Arguments    []string
	Static       bool
	Indirect     bool
	Pipeline     int
	Line         uint
	Column       uint
}

type Redirect struct {
	Action RedirectAction
	Path   string
	Static bool
	Line   uint
	Column uint
}

type Plan struct {
	Mode                  Mode
	ShellPath             string
	CWD                   string
	CWDIdentity           string
	EnvironmentDigest     string
	Invocations           []Invocation
	Redirects             []Redirect
	Complete              bool
	DynamicReasons        []DynamicReason
	HasPipeline           bool
	DebuggerAttach        bool
	digest                string
	source                string
	environment           []string
	pathOverridden        bool
	preserveLookPathError bool
	nextPipeline          int
	lookPath              func(string) (string, error)
}

type Request struct {
	Mode                  Mode
	Command               string
	Args                  []string
	CWD                   string
	WorkspaceRoot         string
	Environment           map[string]string
	IdentityKey           []byte
	ShellPath             string
	GOOS                  string
	LookPath              func(string) (string, error)
	CleanEnvironment      bool
	TrustedPATH           bool
	PreserveLookPathError bool
}

type Target struct {
	Mode              Mode
	Executable        string
	ResolvedPath      string
	Arguments         []string
	Indirect          bool
	PlanDigest        string
	CWDIdentity       string
	EnvironmentDigest string
	Complete          bool
	DynamicReasons    []DynamicReason
	InvocationCount   int
	RedirectCount     int
}

func (p Plan) Digest() string {
	return p.digest
}

func (p Plan) Clone() Plan {
	clone := p
	clone.Invocations = slices.Clone(p.Invocations)
	for index := range clone.Invocations {
		clone.Invocations[index].Arguments = slices.Clone(p.Invocations[index].Arguments)
	}
	clone.Redirects = slices.Clone(p.Redirects)
	clone.DynamicReasons = slices.Clone(p.DynamicReasons)
	clone.environment = slices.Clone(p.environment)
	return clone
}

func (p Plan) ExecutionCommand() (string, []string, string, []string, error) {
	switch p.Mode {
	case ModeDirect:
		if len(p.Invocations) == 0 || strings.TrimSpace(p.Invocations[0].Executable) == "" {
			return "", nil, "", nil, errors.New("direct command plan has no executable")
		}
		return p.Invocations[0].Executable, slices.Clone(
				p.Invocations[0].Arguments,
			), p.CWD, slices.Clone(
				p.environment,
			), nil
	case ModePOSIXShell:
		if p.ShellPath == "" {
			return "", nil, "", nil, errors.New("POSIX shell is unavailable")
		}
		return p.ShellPath, []string{"-c", p.source}, p.CWD, slices.Clone(p.environment), nil
	default:
		return "", nil, "", nil, errors.New("command execution mode is invalid")
	}
}

func (p Plan) Target(invocation Invocation) Target {
	mode := invocation.Mode
	if mode == "" {
		mode = p.Mode
	}
	return Target{
		Mode:              mode,
		Executable:        invocation.Executable,
		ResolvedPath:      invocation.ResolvedPath,
		Arguments:         slices.Clone(invocation.Arguments),
		Indirect:          invocation.Indirect,
		PlanDigest:        p.digest,
		CWDIdentity:       p.CWDIdentity,
		EnvironmentDigest: p.EnvironmentDigest,
		Complete:          p.Complete,
		DynamicReasons:    slices.Clone(p.DynamicReasons),
		InvocationCount:   len(p.Invocations),
		RedirectCount:     len(p.Redirects),
	}
}

func (p Plan) NewCommand(ctx context.Context) (*exec.Cmd, error) {
	if len(p.Invocations) == 0 {
		return nil, errors.New("command plan has no executable invocation")
	}

	var cmd *exec.Cmd
	switch p.Mode {
	case ModeDirect:
		invocation := p.Invocations[0]
		if invocation.ResolvedPath == "" {
			return nil, errors.New("direct command plan has no resolved executable")
		}
		cmd = exec.CommandContext(ctx, invocation.ResolvedPath, invocation.Arguments...)
	case ModePOSIXShell:
		if p.ShellPath == "" {
			return nil, errors.New("POSIX shell is unavailable")
		}
		cmd = exec.CommandContext(ctx, p.ShellPath, "-c", p.source)
	default:
		return nil, errors.New("command execution mode is invalid")
	}

	cmd.Dir = p.CWD
	cmd.Env = slices.Clone(p.environment)
	return cmd, nil
}

func (p Plan) Summary() string {
	if len(p.Invocations) == 0 {
		return string(p.Mode) + " command"
	}
	if len(p.Invocations) == 1 {
		return string(p.Mode) + " · " + filepath.Base(p.Invocations[0].Executable)
	}
	return string(p.Mode) + " · " + filepath.Base(p.Invocations[0].Executable) + " and " +
		strconv.Itoa(len(p.Invocations)-1) + " more"
}

func (t Target) Normalize() (Target, error) {
	t.Mode = Mode(strings.TrimSpace(strings.ToLower(string(t.Mode))))
	t.Executable = strings.TrimSpace(t.Executable)
	t.ResolvedPath = strings.TrimSpace(t.ResolvedPath)
	t.Arguments = slices.Clone(t.Arguments)
	t.PlanDigest = strings.TrimSpace(t.PlanDigest)
	t.CWDIdentity = strings.TrimSpace(t.CWDIdentity)
	t.EnvironmentDigest = strings.TrimSpace(t.EnvironmentDigest)
	t.DynamicReasons = slices.Clone(t.DynamicReasons)

	if t.Mode != ModeDirect && t.Mode != ModePOSIXShell {
		return Target{}, errors.New("command target mode is invalid")
	}
	if t.Executable == "" {
		return Target{}, errors.New("command target executable is required")
	}
	if t.Mode == ModeDirect && !isAbsoluteCommandPath(t.ResolvedPath) {
		return Target{}, errors.New("direct command target requires an absolute resolved path")
	}
	if t.PlanDigest == "" {
		return Target{}, errors.New("command target plan digest is required")
	}
	for _, argument := range t.Arguments {
		if strings.IndexByte(argument, 0) >= 0 {
			return Target{}, errors.New("command target argument contains a NUL byte")
		}
	}
	normalizedReasons := make([]DynamicReason, 0, len(t.DynamicReasons))
	for _, reason := range t.DynamicReasons {
		if !isValidDynamicReason(reason) {
			return Target{}, errors.New("command target contains an invalid dynamic reason")
		}
		if !slices.Contains(normalizedReasons, reason) {
			normalizedReasons = append(normalizedReasons, reason)
		}
	}
	t.DynamicReasons = normalizedReasons

	return t, nil
}

func (t Target) Fingerprint() string {
	values := []string{
		string(t.Mode),
		t.Executable,
		t.ResolvedPath,
		encodeStringList(t.Arguments),
		boolString(t.Indirect),
		t.PlanDigest,
		t.CWDIdentity,
		t.EnvironmentDigest,
		boolString(t.Complete),
		strings.Join(dynamicReasonsToStrings(t.DynamicReasons), ","),
		strconv.Itoa(t.InvocationCount),
		strconv.Itoa(t.RedirectCount),
	}
	sum := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return hex.EncodeToString(sum[:])
}

func (t Target) Equal(other Target) bool {
	return t.Mode == other.Mode &&
		t.Executable == other.Executable &&
		t.ResolvedPath == other.ResolvedPath &&
		slices.Equal(t.Arguments, other.Arguments) &&
		t.Indirect == other.Indirect &&
		t.PlanDigest == other.PlanDigest &&
		t.CWDIdentity == other.CWDIdentity &&
		t.EnvironmentDigest == other.EnvironmentDigest &&
		t.Complete == other.Complete &&
		slices.Equal(t.DynamicReasons, other.DynamicReasons) &&
		t.InvocationCount == other.InvocationCount &&
		t.RedirectCount == other.RedirectCount
}

func isValidDynamicReason(reason DynamicReason) bool {
	return slices.Contains([]DynamicReason{
		ReasonDynamicExecutable,
		ReasonDynamicArgument,
		ReasonDynamicRedirect,
		ReasonIndirectExecution,
		ReasonEnvironment,
		ReasonShellState,
		ReasonShellUnavailable,
	}, reason)
}

func dynamicReasonsToStrings(reasons []DynamicReason) []string {
	values := make([]string, len(reasons))
	for index, reason := range reasons {
		values[index] = string(reason)
	}
	return values
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func encodeStringList(values []string) string {
	var result strings.Builder
	result.WriteString(strconv.Itoa(len(values)))
	result.WriteByte('#')
	for _, value := range values {
		result.WriteString(strconv.Itoa(len(value)))
		result.WriteByte(':')
		result.WriteString(value)
	}
	return result.String()
}
