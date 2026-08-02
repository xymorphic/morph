package command

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

func analyzePOSIX(ctx context.Context, plan *Plan, source string, nesting int) error {
	if nesting > MaxNesting {
		return errors.New("command nesting exceeds the analysis limit")
	}
	parser := syntax.NewParser(syntax.Variant(syntax.LangPOSIX))
	file, err := parser.Parse(strings.NewReader(source), "")
	if err != nil {
		return errors.New("invalid POSIX shell syntax")
	}

	nodeCount := 0
	functions := make(map[string]uint)
	syntax.Walk(file, func(node syntax.Node) bool {
		nodeCount++
		if declaration, ok := node.(*syntax.FuncDecl); ok && declaration.Name != nil {
			offset := declaration.Pos().Offset()
			if current, exists := functions[declaration.Name.Value]; !exists || offset < current {
				functions[declaration.Name.Value] = offset
			}
		}
		if binary, ok := node.(*syntax.BinaryCmd); ok &&
			(binary.Op == syntax.Pipe || binary.Op == syntax.PipeAll) {
			plan.HasPipeline = true
		}
		return nodeCount <= MaxNodes
	})
	if nodeCount > MaxNodes {
		return errors.New("command syntax tree exceeds the analysis limit")
	}

	pipelines := getPipelineAssignments(plan, file)
	var walkErr error
	syntax.Walk(file, func(node syntax.Node) bool {
		if err := ctx.Err(); err != nil {
			walkErr = err
			return false
		}
		switch value := node.(type) {
		case *syntax.CallExpr:
			walkErr = addCall(ctx, plan, value, functions, pipelines[value], nesting)
		case *syntax.Redirect:
			addRedirect(plan, value)
		}
		return walkErr == nil
	})
	return walkErr
}

func getPipelineAssignments(plan *Plan, file *syntax.File) map[*syntax.CallExpr]int {
	assignments := make(map[*syntax.CallExpr]int)
	syntax.Walk(file, func(node syntax.Node) bool {
		binary, ok := node.(*syntax.BinaryCmd)
		if !ok || binary.Op != syntax.Pipe && binary.Op != syntax.PipeAll {
			return true
		}
		plan.nextPipeline++
		pipeline := plan.nextPipeline
		syntax.Walk(binary, func(child syntax.Node) bool {
			call, ok := child.(*syntax.CallExpr)
			if ok {
				if _, assigned := assignments[call]; !assigned {
					assignments[call] = pipeline
				}
			}
			return true
		})
		return true
	})
	return assignments
}

func addCall(
	ctx context.Context,
	plan *Plan,
	call *syntax.CallExpr,
	functions map[string]uint,
	pipeline int,
	nesting int,
) error {
	pathResolutionUncertain := plan.pathOverridden || hasAssignment(call.Assigns, "PATH")
	if len(call.Assigns) > 0 {
		addDynamicReason(plan, ReasonEnvironment)
	}
	if len(call.Args) == 0 {
		return nil
	}
	words := make([]string, len(call.Args))
	static := make([]bool, len(call.Args))
	for index, word := range call.Args {
		words[index], static[index] = getStaticWord(word)
	}
	if !static[0] {
		plan.Invocations = append(plan.Invocations, Invocation{
			Mode:       ModePOSIXShell,
			Executable: "<dynamic>",
			Static:     false,
			Pipeline:   pipeline,
			Line:       call.Pos().Line(),
			Column:     call.Pos().Col(),
		})
		addDynamicReason(plan, ReasonDynamicExecutable)
		return nil
	}
	if declarationOffset, ok := functions[words[0]]; ok && declarationOffset < call.Pos().Offset() {
		addDynamicReason(plan, ReasonShellState)
	}

	arguments := words[1:]
	invocation := Invocation{
		Mode:       ModePOSIXShell,
		Executable: words[0],
		Arguments:  arguments,
		Static:     true,
		Pipeline:   pipeline,
		Line:       call.Pos().Line(),
		Column:     call.Pos().Col(),
	}
	for _, isStatic := range static[1:] {
		if !isStatic {
			invocation.Static = false
			addDynamicReason(plan, ReasonDynamicArgument)
			break
		}
	}
	if !pathResolutionUncertain {
		resolved, err := plan.lookPath(invocation.Executable)
		if err == nil && filepath.IsAbs(resolved) {
			invocation.ResolvedPath = filepath.Clean(resolved)
		}
	}

	base := strings.ToLower(filepath.Base(invocation.Executable))
	if base == "eval" || base == "." || base == "source" {
		addDynamicReason(plan, ReasonIndirectExecution)
	}
	if isStatefulShellBuiltin(base) {
		addDynamicReason(plan, ReasonShellState)
	}
	firstInvocation := len(plan.Invocations)
	switch base {
	case "command":
		result, ok := getCommandBuiltinChild(arguments)
		if !ok || result.query {
			plan.Invocations = append(plan.Invocations, invocation)
		} else {
			child := result.child
			invocation.Executable = child.Executable
			invocation.Arguments = child.Arguments
			invocation.Indirect = child.Indirect
			invocation.Static = invocation.Static && child.Static
			invocation.ResolvedPath = ""
			if result.defaultPath {
				addDynamicReason(plan, ReasonShellState)
			} else if !pathResolutionUncertain {
				invocation.ResolvedPath = child.ResolvedPath
			}
			plan.Invocations = append(plan.Invocations, invocation)
		}
	case "exec":
		child, ok := getExecBuiltinChild(arguments)
		if !ok {
			plan.Invocations = append(plan.Invocations, invocation)
			addDynamicReason(plan, ReasonDynamicExecutable)
		} else {
			invocation.Executable = child.Executable
			invocation.Arguments = child.Arguments
			invocation.Indirect = child.Indirect
			invocation.Static = invocation.Static && child.Static
			invocation.ResolvedPath = ""
			if !pathResolutionUncertain {
				invocation.ResolvedPath = child.ResolvedPath
			}
			plan.Invocations = append(plan.Invocations, invocation)
		}
	default:
		plan.Invocations = append(plan.Invocations, invocation)
		switch base {
		case "sudo":
			if targets, edit := getSudoEditTargets(arguments); edit {
				plan.Invocations[len(plan.Invocations)-1].Indirect = true
				for _, target := range targets {
					plan.Redirects = append(plan.Redirects, Redirect{
						Action: RedirectUpdate,
						Path:   target,
						Static: true,
						Line:   call.Pos().Line(),
						Column: call.Pos().Col(),
					})
				}
				addDynamicReason(plan, ReasonIndirectExecution)
			} else if child, ok := getWrapperChild(base, arguments, ModePOSIXShell); !ok {
				addDynamicReason(plan, ReasonDynamicExecutable)
			} else {
				child.Pipeline = pipeline
				child.Line = call.Pos().Line()
				child.Column = call.Pos().Col()
				if plan.pathOverridden || hasPathEnvironmentMutation(arguments) {
					child.ResolvedPath = ""
				}
				plan.Invocations = append(plan.Invocations, child)
			}
			if hasWrapperWorkingDirectoryChange(base, arguments) {
				addDynamicReason(plan, ReasonShellState)
			}
		case "env":
			child, ok := getWrapperChild(base, arguments, ModePOSIXShell)
			if !ok {
				addDynamicReason(plan, ReasonDynamicExecutable)
			} else {
				child.Pipeline = pipeline
				child.Line = call.Pos().Line()
				child.Column = call.Pos().Col()
				if plan.pathOverridden || hasPathEnvironmentMutation(arguments) {
					child.ResolvedPath = ""
				}
				plan.Invocations = append(plan.Invocations, child)
			}
			if hasEnvironmentMutation(arguments) {
				addDynamicReason(plan, ReasonEnvironment)
			}
			if hasWrapperWorkingDirectoryChange(base, arguments) {
				addDynamicReason(plan, ReasonShellState)
			}
		}
	}

	for index := firstInvocation; index < len(plan.Invocations); index++ {
		if isIndirectInvocation(plan.Invocations[index]) {
			plan.Invocations[index].Indirect = true
			addReason(&plan.DynamicReasons, ReasonIndirectExecution)
		}
	}
	if isDebuggerAttachment(invocation) {
		plan.DebuggerAttach = true
	}
	addExplicitInterpreterFiles(plan, invocation, call.Pos().Line(), call.Pos().Col())

	if nested, ok := getNestedShellSource(invocation); ok {
		if err := analyzePOSIX(ctx, plan, nested, nesting+1); err != nil {
			return err
		}
	} else {
		script, staticScript, opaqueSource := getInterpreterSource(invocation)
		if opaqueSource {
			addDynamicReason(plan, ReasonIndirectExecution)
		}
		if staticScript {
			plan.Redirects = append(plan.Redirects, Redirect{
				Action: RedirectRead,
				Path:   script,
				Static: true,
				Line:   call.Pos().Line(),
				Column: call.Pos().Col(),
			})
			addDynamicReason(plan, ReasonIndirectExecution)
		}
	}
	if (base == "." || base == "source") && len(arguments) > 0 && static[1] {
		plan.Redirects = append(plan.Redirects, Redirect{
			Action: RedirectRead,
			Path:   arguments[0],
			Static: true,
			Line:   call.Pos().Line(),
			Column: call.Pos().Col(),
		})
	}
	return nil
}

type commandBuiltinResult struct {
	child       Invocation
	query       bool
	defaultPath bool
}

func getCommandBuiltinChild(arguments []string) (commandBuiltinResult, bool) {
	index := 0
	defaultPath := false
	for index < len(arguments) {
		switch arguments[index] {
		case "--":
			index++
			goto child
		case "-p":
			defaultPath = true
			index++
		case "-v", "-V":
			return commandBuiltinResult{query: true, defaultPath: defaultPath}, true
		default:
			if strings.HasPrefix(arguments[index], "-") {
				return commandBuiltinResult{}, false
			}
			goto child
		}
	}

child:
	child, ok := getChildInvocation(arguments, index, ModePOSIXShell)
	return commandBuiltinResult{child: child, defaultPath: defaultPath}, ok
}

func getExecBuiltinChild(arguments []string) (Invocation, bool) {
	index := 0
	for index < len(arguments) {
		switch arguments[index] {
		case "--":
			index++
			return getChildInvocation(arguments, index, ModePOSIXShell)
		case "-a":
			index += 2
		case "-c", "-l":
			index++
		default:
			if strings.HasPrefix(arguments[index], "-") {
				return Invocation{}, false
			}
			return getChildInvocation(arguments, index, ModePOSIXShell)
		}
	}
	return Invocation{}, false
}

func getWrapperChild(wrapper string, arguments []string, mode Mode) (Invocation, bool) {
	index, ok := getWrapperChildIndex(wrapper, arguments)
	if !ok {
		return Invocation{}, false
	}
	return getChildInvocation(arguments, index, mode)
}

func getWrapperChildIndex(wrapper string, arguments []string) (int, bool) {
	index := 0
	for index < len(arguments) {
		argument := arguments[index]
		if argument == "--" {
			return index + 1, true
		}
		if (wrapper == "env" || wrapper == "sudo") && isEnvironmentAssignment(argument) {
			index++
			continue
		}
		if !strings.HasPrefix(argument, "-") || argument == "-" {
			return index, true
		}

		if wrapper == "env" &&
			(argument == "-S" || argument == "--split-string" ||
				strings.HasPrefix(argument, "--split-string=")) {
			return 0, false
		}
		consumesValue, known := getWrapperOption(wrapper, argument)
		if !known {
			return 0, false
		}
		index++
		if consumesValue && !strings.Contains(argument, "=") {
			if index >= len(arguments) {
				return 0, false
			}
			index++
		}
	}
	return index, true
}

func hasEnvironmentMutation(arguments []string) bool {
	for _, argument := range arguments {
		if isEnvironmentAssignment(argument) ||
			argument == "-i" || argument == "--ignore-environment" ||
			argument == "-u" || argument == "--unset" ||
			strings.HasPrefix(argument, "--unset=") {
			return true
		}
	}
	return false
}

func hasPathEnvironmentMutation(arguments []string) bool {
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		name, _, ok := strings.Cut(argument, "=")
		if ok && name == "PATH" {
			return true
		}
		if argument == "-i" || argument == "--ignore-environment" {
			return true
		}
		if (argument == "-u" || argument == "--unset") && index+1 < len(arguments) &&
			arguments[index+1] == "PATH" {
			return true
		}
		if strings.TrimPrefix(argument, "--unset=") == "PATH" &&
			strings.HasPrefix(argument, "--unset=") {
			return true
		}
	}
	return false
}

func hasAssignment(assignments []*syntax.Assign, expected string) bool {
	for _, assignment := range assignments {
		if assignment != nil && assignment.Name != nil && assignment.Name.Value == expected {
			return true
		}
	}
	return false
}

func hasWrapperWorkingDirectoryChange(wrapper string, arguments []string) bool {
	for _, argument := range arguments {
		if wrapper == "env" &&
			(argument == "-C" || argument == "--chdir" || strings.HasPrefix(argument, "--chdir=")) {
			return true
		}
		if wrapper == "sudo" &&
			(argument == "-D" || argument == "--chdir" || argument == "-R" || argument == "--chroot" ||
				strings.HasPrefix(argument, "--chdir=") || strings.HasPrefix(argument, "--chroot=")) {
			return true
		}
	}
	return false
}

func getWrapperOption(wrapper string, option string) (bool, bool) {
	switch wrapper {
	case "env":
		switch option {
		case "-i", "--ignore-environment", "-0", "--null", "-v", "--debug":
			return false, true
		case "-u", "--unset", "-C", "--chdir", "-S", "--split-string":
			return true, true
		}
		for _, prefix := range []string{"--unset=", "--chdir=", "--split-string="} {
			if strings.HasPrefix(option, prefix) {
				return true, true
			}
		}
	case "sudo":
		switch option {
		case "-A", "--askpass", "-b", "--background", "-E", "--preserve-env", "-e", "--edit",
			"-H", "--set-home", "-K", "--remove-timestamp", "-k", "--reset-timestamp",
			"-n", "--non-interactive", "-P", "--preserve-groups", "-S", "--stdin",
			"-V", "--version", "-v", "--validate":
			return false, true
		case "-a", "--auth-type", "-C", "--close-from", "-D", "--chdir", "-g", "--group",
			"-h", "--host", "-p", "--prompt", "-R", "--chroot", "-r", "--role",
			"-t", "--type", "-T", "--command-timeout", "-U", "--other-user", "-u", "--user":
			return true, true
		}
		for _, prefix := range []string{
			"--auth-type=", "--close-from=", "--chdir=", "--group=", "--host=", "--prompt=",
			"--chroot=", "--role=", "--type=", "--command-timeout=", "--other-user=", "--user=",
		} {
			if strings.HasPrefix(option, prefix) {
				return true, true
			}
		}
	}
	return false, false
}

func getSudoEditTargets(arguments []string) ([]string, bool) {
	edit := false
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "--" {
			if !edit {
				return nil, false
			}
			return slices.Clone(arguments[index+1:]), true
		}
		if !strings.HasPrefix(argument, "-") || argument == "-" {
			if !edit {
				return nil, false
			}
			return slices.Clone(arguments[index:]), true
		}
		if argument == "-e" || argument == "--edit" {
			edit = true
			continue
		}
		consumes, known := getWrapperOption("sudo", argument)
		if !known {
			return nil, edit
		}
		if consumes && !strings.Contains(argument, "=") {
			index++
			if index >= len(arguments) {
				return nil, edit
			}
		}
	}
	return nil, edit
}

func getChildInvocation(arguments []string, index int, mode Mode) (Invocation, bool) {
	if index >= len(arguments) || arguments[index] == "" {
		return Invocation{}, false
	}
	child := Invocation{
		Mode:       mode,
		Executable: arguments[index],
		Arguments:  append([]string(nil), arguments[index+1:]...),
		Static:     true,
	}
	if resolved, err := lookPath(child.Executable); err == nil && filepath.IsAbs(resolved) {
		child.ResolvedPath = filepath.Clean(resolved)
	}
	child.Indirect = isIndirectInvocation(child)
	return child, true
}

func isEnvironmentAssignment(value string) bool {
	name, _, ok := strings.Cut(value, "=")
	if !ok || name == "" {
		return false
	}
	for index, r := range name {
		if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') &&
			(index == 0 || r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func isStatefulShellBuiltin(executable string) bool {
	return slices.Contains(
		[]string{"alias", "cd", "export", "set", "trap", "umask", "unalias", "unset"},
		executable,
	)
}

func addRedirect(plan *Plan, redirect *syntax.Redirect) {
	if redirect.Op == syntax.Hdoc ||
		redirect.Op == syntax.DashHdoc ||
		redirect.Op == syntax.WordHdoc ||
		redirect.Op == syntax.DplIn ||
		redirect.Op == syntax.DplOut {
		if redirect.Op == syntax.Hdoc || redirect.Op == syntax.DashHdoc ||
			redirect.Op == syntax.WordHdoc {
			addDynamicReason(plan, ReasonIndirectExecution)
		}
		return
	}
	path, static := getStaticWord(redirect.Word)
	actions := []RedirectAction{RedirectUpdate}
	switch redirect.Op {
	case syntax.RdrIn:
		actions = []RedirectAction{RedirectRead}
	case syntax.RdrOut, syntax.RdrClob, syntax.RdrAll, syntax.RdrAllClob:
		actions = []RedirectAction{RedirectCreate, RedirectUpdate}
	case syntax.AppOut, syntax.AppClob, syntax.AppAll, syntax.AppAllClob:
		actions = []RedirectAction{RedirectCreate, RedirectUpdate}
	case syntax.RdrInOut:
		actions = []RedirectAction{RedirectRead, RedirectCreate, RedirectUpdate}
	}
	for _, action := range actions {
		plan.Redirects = append(plan.Redirects, Redirect{
			Action: action,
			Path:   path,
			Static: static,
			Line:   redirect.Pos().Line(),
			Column: redirect.Pos().Col(),
		})
	}
	if !static {
		addDynamicReason(plan, ReasonDynamicRedirect)
	}
}

func getStaticWord(word *syntax.Word) (string, bool) {
	if word == nil {
		return "", false
	}
	var builder strings.Builder
	for _, part := range word.Parts {
		value, ok := getStaticWordPart(part, false)
		if !ok {
			return builder.String(), false
		}
		builder.WriteString(value)
	}
	return builder.String(), true
}

func getStaticWordPart(part syntax.WordPart, quoted bool) (string, bool) {
	switch value := part.(type) {
	case *syntax.Lit:
		if !quoted &&
			(strings.ContainsAny(value.Value, "*?[") || strings.HasPrefix(value.Value, "~")) {
			return value.Value, false
		}
		return value.Value, true
	case *syntax.SglQuoted:
		return value.Value, !value.Dollar
	case *syntax.DblQuoted:
		if value.Dollar {
			return "", false
		}
		var builder strings.Builder
		for _, child := range value.Parts {
			partValue, ok := getStaticWordPart(child, true)
			if !ok {
				return builder.String(), false
			}
			builder.WriteString(partValue)
		}
		return builder.String(), true
	default:
		return "", false
	}
}

func getNestedShellSource(invocation Invocation) (string, bool) {
	switch strings.ToLower(filepath.Base(invocation.Executable)) {
	case "sh", "bash", "zsh", "ksh":
		for index := 0; index < len(invocation.Arguments); index++ {
			argument := invocation.Arguments[index]
			if argument == "--" ||
				!strings.HasPrefix(argument, "-") && !strings.HasPrefix(argument, "+") {
				return "", false
			}
			if strings.HasPrefix(argument, "--") {
				if argument == "--rcfile" {
					index++
				}
				continue
			}
			if argument == "-o" || argument == "+o" || argument == "-O" {
				index++
				continue
			}
			if !strings.Contains(strings.TrimPrefix(argument, "-"), "c") {
				continue
			}
			if index+1 >= len(invocation.Arguments) {
				return "", false
			}
			source := invocation.Arguments[index+1]
			return source, source != ""
		}
	default:
	}
	return "", false
}

func addExplicitInterpreterFiles(
	plan *Plan,
	invocation Invocation,
	line uint,
	column uint,
) {
	if strings.ToLower(filepath.Base(invocation.Executable)) != "bash" {
		return
	}
	for index, argument := range invocation.Arguments {
		var path string
		switch {
		case argument == "--rcfile" && index+1 < len(invocation.Arguments):
			path = invocation.Arguments[index+1]
		case strings.HasPrefix(argument, "--rcfile="):
			path = strings.TrimPrefix(argument, "--rcfile=")
		}
		if path == "" {
			continue
		}
		plan.Redirects = append(plan.Redirects, Redirect{
			Action: RedirectRead,
			Path:   path,
			Static: true,
			Line:   line,
			Column: column,
		})
		addDynamicReason(plan, ReasonIndirectExecution)
	}
}

func getInterpreterSource(invocation Invocation) (string, bool, bool) {
	base := strings.ToLower(filepath.Base(invocation.Executable))
	switch base {
	case "sh", "bash", "zsh", "ksh", "python", "python2", "python3", "node", "ruby", "perl":
	default:
		return "", false, false
	}
	if len(invocation.Arguments) == 0 {
		return "", false, true
	}
	for index := 0; index < len(invocation.Arguments); index++ {
		argument := invocation.Arguments[index]
		if argument == "--" {
			if index+1 < len(invocation.Arguments) && invocation.Arguments[index+1] != "" {
				return invocation.Arguments[index+1], true, false
			}
			return "", false, true
		}
		if argument == "-" {
			return "", false, true
		}
		if !strings.HasPrefix(argument, "-") {
			return argument, true, false
		}
		if isInterpreterCodeOption(base, argument) {
			return "", false, true
		}
		consumes, known := getInterpreterOption(base, argument)
		if !known {
			return "", false, true
		}
		if consumes && !strings.Contains(argument, "=") {
			index++
			if index >= len(invocation.Arguments) {
				return "", false, true
			}
			continue
		}
	}
	return "", false, true
}

func isInterpreterCodeOption(base string, option string) bool {
	switch base {
	case "sh", "bash", "zsh", "ksh":
		return !strings.HasPrefix(option, "--") &&
			strings.Contains(strings.TrimPrefix(option, "-"), "c")
	case "python", "python2", "python3":
		return option == "-c" || option == "-m"
	case "node":
		return option == "-e" || option == "--eval" || strings.HasPrefix(option, "--eval=") ||
			option == "-p" || option == "--print" || strings.HasPrefix(option, "--print=")
	case "ruby", "perl":
		return strings.HasPrefix(option, "-e")
	default:
		return false
	}
}

func getInterpreterOption(base string, option string) (bool, bool) {
	if !strings.HasPrefix(option, "--") && len(option) > 1 {
		switch base {
		case "sh", "bash", "zsh", "ksh":
			if option == "-o" || option == "+o" || option == "-O" {
				return true, true
			}
			return false, true
		case "python", "python2", "python3":
			return option == "-W" || option == "-X", true
		case "node":
			return option == "-r", true
		case "ruby", "perl":
			return option == "-I" || option == "-r", true
		}
	}

	switch base {
	case "sh", "bash", "zsh", "ksh":
		switch option {
		case "--norc", "--noprofile", "--posix", "--restricted", "--verbose":
			return false, true
		case "--rcfile":
			return true, true
		}
	case "python", "python2", "python3":
		switch {
		case option == "--check-hash-based-pycs":
			return true, true
		case strings.HasPrefix(option, "--check-hash-based-pycs="):
			return false, true
		}
	case "node":
		switch {
		case option == "--require":
			return true, true
		case strings.HasPrefix(option, "--require="):
			return false, true
		}
	}
	return false, false
}

func isDebuggerAttachment(invocation Invocation) bool {
	for _, argument := range invocation.Arguments {
		lower := strings.ToLower(argument)
		if strings.Contains(lower, "devtoolsactiveport") ||
			strings.Contains(lower, "/devtools/browser/") ||
			strings.Contains(lower, "/json/version") &&
				(strings.Contains(lower, "127.0.0.1") ||
					strings.Contains(lower, "localhost") ||
					strings.Contains(lower, "[::1]")) {
			return true
		}
	}
	return false
}
