package command

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
)

const (
	MaxSourceBytes = 64 * 1024
	MaxArguments   = 1024
	MaxNodes       = 10000
	MaxInvocations = 512
	MaxRedirects   = 512
	MaxNesting     = 16
)

var (
	lookPath            = exec.LookPath
	statPath            = os.Stat
	getWorkingDirectory = os.Getwd
	getAbsolutePath     = filepath.Abs
	loadEnvironment     = os.Environ
	readIdentityRandom  = rand.Read
	defaultIdentityKey  = newIdentityKey()
)

func Analyze(ctx context.Context, request Request) (Plan, error) {
	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	mode := Mode(strings.TrimSpace(strings.ToLower(string(request.Mode))))
	if mode == "" {
		mode = ModeDirect
	}
	if mode != ModeDirect && mode != ModePOSIXShell {
		return Plan{}, errors.New("command mode must be direct or posix_shell")
	}
	if len(request.Command) > MaxSourceBytes {
		return Plan{}, errors.New("command source exceeds the analysis limit")
	}
	if strings.IndexByte(request.Command, 0) >= 0 {
		return Plan{}, errors.New("command contains a NUL byte")
	}
	if len(request.Args) > MaxArguments {
		return Plan{}, errors.New("command argument count exceeds the analysis limit")
	}
	for _, argument := range request.Args {
		if strings.IndexByte(argument, 0) >= 0 {
			return Plan{}, errors.New("command argument contains a NUL byte")
		}
	}

	goos := request.GOOS
	if goos == "" {
		goos = goruntime.GOOS
	}
	identityKey := request.IdentityKey
	if len(identityKey) == 0 {
		identityKey = defaultIdentityKey
	}
	cwd, cwdIdentity, err := getCWDIdentity(request.CWD, request.WorkspaceRoot, identityKey)
	if err != nil {
		return Plan{}, err
	}
	environment, environmentDigest, environmentReasons, err := getEnvironmentForRequest(
		request.Environment,
		mode,
		goos,
		identityKey,
		request.CleanEnvironment,
		request.TrustedPATH,
	)
	if err != nil {
		return Plan{}, err
	}

	plan := Plan{
		Mode:                  mode,
		CWD:                   cwd,
		CWDIdentity:           cwdIdentity,
		EnvironmentDigest:     environmentDigest,
		Complete:              len(environmentReasons) == 0,
		DynamicReasons:        environmentReasons,
		source:                request.Command,
		environment:           environment,
		pathOverridden:        hasEnvironmentKey(request.Environment, "PATH", goos) && !request.TrustedPATH,
		preserveLookPathError: request.PreserveLookPathError,
		lookPath:              request.LookPath,
	}
	if plan.lookPath == nil {
		plan.lookPath = lookPath
	}

	switch mode {
	case ModeDirect:
		if err := analyzeDirect(ctx, &plan, request.Command, request.Args, goos); err != nil {
			return Plan{}, err
		}
	case ModePOSIXShell:
		if len(request.Args) != 0 {
			return Plan{}, errors.New("posix_shell mode does not accept direct arguments")
		}
		if strings.TrimSpace(request.Command) == "" {
			return Plan{}, errors.New("command is required")
		}
		plan.ShellPath = getShellPath(request.ShellPath, goos)
		if plan.ShellPath == "" {
			addDynamicReason(&plan, ReasonShellUnavailable)
		}
		if err := analyzePOSIX(ctx, &plan, request.Command, 0); err != nil {
			return Plan{}, err
		}
	}

	if len(plan.Invocations) == 0 {
		return Plan{}, errors.New("command plan has no executable invocation")
	}
	if len(plan.Invocations) > MaxInvocations {
		return Plan{}, errors.New("command invocation count exceeds the analysis limit")
	}
	if len(plan.Redirects) > MaxRedirects {
		return Plan{}, errors.New("command redirect count exceeds the analysis limit")
	}
	plan.digest = getPlanDigest(plan, identityKey)
	return plan, nil
}

func hasEnvironmentKey(environment map[string]string, expected string, goos string) bool {
	for key := range environment {
		if goos == "windows" {
			if strings.EqualFold(strings.TrimSpace(key), expected) {
				return true
			}
			continue
		}
		if strings.TrimSpace(key) == expected {
			return true
		}
	}
	return false
}

func analyzeDirect(
	ctx context.Context,
	plan *Plan,
	command string,
	arguments []string,
	goos string,
) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return errors.New("command is required")
	}
	lookup := command
	if hasPathSeparator(command) && !isAbsoluteCommandPath(command) {
		lookup = filepath.Join(plan.CWD, command)
	}
	resolved, err := plan.lookPath(lookup)
	if err != nil {
		if plan.preserveLookPathError {
			return err
		}
		return errors.New("command executable was not found")
	}
	if !isAbsoluteCommandPath(resolved) {
		resolved, err = getAbsolutePath(resolved)
		if err != nil {
			return errors.New("command executable path could not be resolved")
		}
	}
	if goos == "windows" {
		switch strings.ToLower(filepath.Ext(resolved)) {
		case ".bat", ".cmd", ".ps1":
			return errors.New("interpreter-dispatched scripts are not supported in direct mode")
		}
	}

	invocation := Invocation{
		Mode:         ModeDirect,
		Executable:   command,
		ResolvedPath: cleanCommandPath(resolved, goos),
		Arguments:    slices.Clone(arguments),
		Static:       true,
	}
	if isIndirectInvocation(invocation) {
		invocation.Indirect = true
		addReason(&plan.DynamicReasons, ReasonIndirectExecution)
	}
	if isDebuggerAttachment(invocation) {
		plan.DebuggerAttach = true
	}
	plan.Invocations = []Invocation{invocation}
	addExplicitInterpreterFiles(plan, invocation, 0, 0)

	if source, ok := getNestedShellSource(invocation); ok {
		if len(source) > MaxSourceBytes {
			return errors.New("nested command source exceeds the analysis limit")
		}
		return analyzePOSIX(ctx, plan, source, 1)
	}
	script, staticScript, opaqueSource := getInterpreterSource(invocation)
	if opaqueSource {
		addDynamicReason(plan, ReasonIndirectExecution)
	}
	if staticScript {
		plan.Redirects = append(plan.Redirects, Redirect{
			Action: RedirectRead,
			Path:   script,
			Static: true,
		})
		addDynamicReason(plan, ReasonIndirectExecution)
	}
	return nil
}

func hasPathSeparator(value string) bool {
	return strings.ContainsRune(value, filepath.Separator) || strings.ContainsAny(value, `/\`)
}

func cleanCommandPath(value string, goos string) string {
	if goos == "windows" {
		return strings.ReplaceAll(filepath.ToSlash(value), "/", `\`)
	}
	return filepath.Clean(value)
}

func getCWDIdentity(cwd string, workspaceRoot string, identityKey []byte) (string, string, error) {
	if strings.TrimSpace(workspaceRoot) == "" {
		workspaceRoot = cwd
	}
	if strings.TrimSpace(workspaceRoot) == "" {
		var err error
		workspaceRoot, err = getWorkingDirectory()
		if err != nil {
			return "", "", errors.New("working directory could not be resolved")
		}
	}
	workspaceRoot, err := getAbsolutePath(workspaceRoot)
	if err != nil {
		return "", "", errors.New("workspace root could not be resolved")
	}
	if strings.TrimSpace(cwd) == "" {
		cwd = workspaceRoot
	} else if !filepath.IsAbs(cwd) {
		cwd = filepath.Join(workspaceRoot, cwd)
	}
	cwd, err = getAbsolutePath(cwd)
	if err != nil {
		return "", "", errors.New("working directory could not be resolved")
	}
	cwd = filepath.Clean(cwd)
	workspaceRoot = filepath.Clean(workspaceRoot)

	relative, err := filepath.Rel(workspaceRoot, cwd)
	if err == nil && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return cwd, "workspace:" + getPathIdentity(workspaceRoot, identityKey), nil
	}
	return cwd, "external:" + getPathIdentity(cwd, identityKey), nil
}

func getPathIdentity(value string, identityKey []byte) string {
	digest := hmac.New(sha256.New, identityKey)
	_, _ = digest.Write([]byte("morph/command-path/v1\x00"))
	_, _ = digest.Write([]byte(filepath.Clean(value)))
	return hex.EncodeToString(digest.Sum(nil))
}

func getEnvironment(
	overrides map[string]string,
	mode Mode,
	goos string,
	identityKey []byte,
) ([]string, string, []DynamicReason, error) {
	return getEnvironmentForRequest(overrides, mode, goos, identityKey, false, false)
}

func getEnvironmentForRequest(
	overrides map[string]string,
	mode Mode,
	goos string,
	identityKey []byte,
	clean bool,
	trustedPATH bool,
) ([]string, string, []DynamicReason, error) {
	values := make(map[string]string)
	names := make(map[string]string)
	reasons := make([]DynamicReason, 0)
	normalizeKey := func(key string) string {
		if goos == "windows" {
			return strings.ToUpper(key)
		}
		return key
	}
	if !clean {
		for _, entry := range loadEnvironment() {
			key, value, ok := strings.Cut(entry, "=")
			if !ok {
				continue
			}
			normalized := normalizeKey(key)
			values[normalized] = value
			names[normalized] = key
		}
	}

	for key, value := range overrides {
		key = strings.TrimSpace(key)
		if key == "" || strings.Contains(key, "=") || strings.IndexByte(key, 0) >= 0 ||
			strings.IndexByte(value, 0) >= 0 {
			return nil, "", nil, errors.New("command environment contains an invalid entry")
		}
		normalized := normalizeKey(key)
		values[normalized] = value
		names[normalized] = key
		if normalized == normalizeKey("PATH") && !trustedPATH {
			addReason(&reasons, ReasonEnvironment)
		}
	}
	if mode == ModePOSIXShell {
		for _, key := range []string{"ENV", "BASH_ENV"} {
			delete(values, normalizeKey(key))
			delete(names, normalizeKey(key))
		}
	}
	for key := range values {
		if key != normalizeKey("PATH") && isExecutionEnvironmentVariable(key) {
			addReason(&reasons, ReasonEnvironment)
		}
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	environment := make([]string, 0, len(keys))
	canonical := make([]string, 0, len(keys))
	for _, key := range keys {
		environment = append(environment, names[key]+"="+values[key])
		canonical = append(canonical, key+"="+values[key])
	}
	digest := hmac.New(sha256.New, identityKey)
	_, _ = digest.Write([]byte("morph/command-environment/v1\x00"))
	_, _ = digest.Write([]byte(strings.Join(canonical, "\x00")))
	return environment, hex.EncodeToString(digest.Sum(nil)), reasons, nil
}

func getShellPath(configured string, goos string) string {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		if goos == "windows" {
			return ""
		}
		configured = "/bin/sh"
	}
	if !filepath.IsAbs(configured) {
		return ""
	}
	info, err := statPath(configured)
	if err != nil || info.IsDir() {
		return ""
	}
	return filepath.Clean(configured)
}

func isExecutionEnvironmentVariable(key string) bool {
	key = strings.ToUpper(key)
	return key == "PATH" || key == "ENV" || key == "BASH_ENV" || key == "LD_PRELOAD" ||
		strings.HasPrefix(key, "DYLD_")
}

func isIndirectInvocation(invocation Invocation) bool {
	executable := strings.ToLower(filepath.Base(invocation.Executable))
	switch executable {
	case "make", "xargs", "npm", "npx", "yarn", "pnpm", "bun", "cargo", "go", "sudo":
		return true
	case "sh", "bash", "zsh", "ksh", "python", "python2", "python3", "node", "ruby", "perl":
		return true
	default:
		return false
	}
}

func addDynamicReason(plan *Plan, reason DynamicReason) {
	if !slices.Contains(plan.DynamicReasons, reason) {
		plan.DynamicReasons = append(plan.DynamicReasons, reason)
	}
	plan.Complete = false
}

func addReason(reasons *[]DynamicReason, reason DynamicReason) {
	if !slices.Contains(*reasons, reason) {
		*reasons = append(*reasons, reason)
	}
}

func getPlanDigest(plan Plan, identityKey []byte) string {
	values := []string{
		string(plan.Mode),
		plan.ShellPath,
		plan.CWDIdentity,
		plan.EnvironmentDigest,
		boolString(plan.Complete),
		boolString(plan.HasPipeline),
		boolString(plan.DebuggerAttach),
	}
	sourceHash := sha256.Sum256([]byte(plan.source))
	values = append(values, hex.EncodeToString(sourceHash[:]))
	for _, reason := range plan.DynamicReasons {
		values = append(values, "reason:"+string(reason))
	}
	for _, invocation := range plan.Invocations {
		values = append(values, strings.Join([]string{
			"invocation",
			string(invocation.Mode),
			invocation.Executable,
			invocation.ResolvedPath,
			encodeStringList(invocation.Arguments),
			boolString(invocation.Static),
			boolString(invocation.Indirect),
			strconv.Itoa(invocation.Pipeline),
		}, "\x00"))
	}
	for _, redirect := range plan.Redirects {
		values = append(values, strings.Join([]string{
			"redirect",
			string(redirect.Action),
			redirect.Path,
			boolString(redirect.Static),
		}, "\x00"))
	}
	digest := hmac.New(sha256.New, identityKey)
	_, _ = digest.Write([]byte("morph/command-plan/v1\x00"))
	_, _ = digest.Write([]byte(strings.Join(values, "\x00")))
	return hex.EncodeToString(digest.Sum(nil))
}

func newIdentityKey() []byte {
	key := make([]byte, 32)
	if _, err := readIdentityRandom(key); err != nil {
		panic(err)
	}
	return key
}
