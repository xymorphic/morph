package main

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type fileEntry struct {
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"is_dir"`
}

type searchMatch struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
	Text   string `json:"text"`
}

type supervisorRequest struct {
	Command     []string `json:"command"`
	CWD         string   `json:"cwd"`
	Env         []string `json:"env"`
	Label       string   `json:"label"`
	OutputBytes int64    `json:"output_bytes"`
}

type supervisorState struct {
	Token      string     `json:"token"`
	PID        int        `json:"pid"`
	StartTicks string     `json:"start_ticks"`
	StartedAt  time.Time  `json:"started_at"`
	EndedAt    *time.Time `json:"ended_at,omitempty"`
	ExitCode   *int       `json:"exit_code,omitempty"`
	Stopped    bool       `json:"stopped,omitempty"`
	Label      string     `json:"label,omitempty"`
	Command    string     `json:"command"`
	CWD        string     `json:"cwd,omitempty"`
}

var (
	sandboxInput           io.Reader = os.Stdin
	sandboxOutput          io.Writer = os.Stdout
	sandboxErrorOutput     io.Writer = os.Stderr
	executeSandboxCommand            = syscall.Exec
	exitSandboxProcess               = os.Exit
	sleepSandbox                     = time.Sleep
	readProcessStat                  = os.ReadFile
	readSandboxToken                 = rand.Read
	supervisorRoot                   = "/run/morph/processes"
	newSandboxCommand                = exec.Command
	startSandboxCommand              = func(command *exec.Cmd) error { return command.Start() }
	getProcessStartTicks             = processStartTicks
	checkSupervisorProcess           = isSupervisorProcess
	checkProcessGroup                = processGroupExists
	killSupervisorProcess            = syscall.Kill
	sleepSupervisor                  = time.Sleep
	statOpenedSandboxFile            = func(file *os.File) (os.FileInfo, error) { return file.Stat() }
	statSandboxPath                  = os.Stat
	makeSandboxDirs                  = os.MkdirAll
	writeSandboxFile                 = os.WriteFile
	walkSandboxDir                   = filepath.WalkDir
	getSandboxRelative               = filepath.Rel
	openSandboxFile                  = os.Open
	openSupervisorOutput             = os.OpenFile
	readSupervisorFile               = os.ReadFile
	renameSupervisorFile             = os.Rename
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(sandboxErrorOutput, err)
		exitSandboxProcess(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("sandbox command is required")
	}
	switch args[0] {
	case "--control":
		return runControlled(args[1:])
	case "fs-read":
		return readFile(args[1:])
	case "fs-write":
		return writeFile(args[1:])
	case "fs-list":
		return listFiles(args[1:])
	case "fs-search":
		return searchFiles(args[1:])
	case "fs-patch":
		return patchFiles(args[1:])
	case "supervisor-start":
		return supervisorStart()
	case "supervisor-launch":
		return supervisorLaunch(args[1:])
	case "supervisor-status":
		return supervisorStatus(args[1:])
	case "supervisor-read":
		return supervisorRead(args[1:])
	case "supervisor-stop":
		return supervisorStop(args[1:])
	case "sleep-forever":
		return sleepForever()
	default:
		return execCommand(args, os.Environ())
	}
}

func sleepForever() error {
	sleepSandbox(100 * 365 * 24 * time.Hour)
	return nil
}

func runControlled(args []string) error {
	if len(args) < 2 {
		return errors.New("controlled command requires a limit and command")
	}
	limit, err := strconv.ParseUint(args[0], 10, 32)
	if err != nil || limit == 0 {
		return errors.New("control payload limit is invalid")
	}
	var size uint32
	if err := binary.Read(sandboxInput, binary.BigEndian, &size); err != nil {
		return err
	}
	if uint64(size) > limit {
		return errors.New("control payload exceeds the limit")
	}
	raw := make([]byte, size)
	if _, err := io.ReadFull(sandboxInput, raw); err != nil {
		return err
	}
	values := map[string]string{}
	if err := json.Unmarshal(raw, &values); err != nil {
		return errors.New("control payload is invalid")
	}
	environment := os.Environ()
	for name, value := range values {
		environment = append(environment, secretEnvironmentName(name)+"="+value)
	}
	return execCommand(args[1:], environment)
}

func secretEnvironmentName(name string) string {
	name = strings.ToUpper(name)
	var value strings.Builder
	value.WriteString("MORPH_SECRET_")
	for _, character := range name {
		if character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' ||
			character == '_' {
			value.WriteRune(character)
		} else {
			value.WriteByte('_')
		}
	}
	return value.String()
}

func execCommand(args []string, environment []string) error {
	path, err := exec.LookPath(args[0])
	if err != nil {
		return err
	}
	return executeSandboxCommand(path, args, environment)
}

func readFile(args []string) error {
	if len(args) != 2 {
		return errors.New("fs-read requires path and limit")
	}
	limit, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil || limit <= 0 {
		return errors.New("fs-read limit is invalid")
	}
	file, err := os.Open(args[0])
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	info, err := statOpenedSandboxFile(file)
	if err != nil {
		return err
	}
	if info.Size() > limit {
		return errors.New("file exceeds the read limit")
	}
	_, err = io.Copy(sandboxOutput, file)
	return err
}

func writeFile(args []string) error {
	if len(args) != 3 {
		return errors.New("fs-write requires path, create-dirs, and limit")
	}
	createDirs, err := strconv.ParseBool(args[1])
	if err != nil {
		return errors.New("fs-write create-dirs is invalid")
	}
	limit, err := strconv.ParseInt(args[2], 10, 64)
	if err != nil || limit <= 0 {
		return errors.New("fs-write limit is invalid")
	}
	if createDirs {
		if err := makeSandboxDirs(filepath.Dir(args[0]), 0o755); err != nil {
			return err
		}
	}
	_, statErr := statSandboxPath(args[0])
	created := errors.Is(statErr, os.ErrNotExist)
	data, err := io.ReadAll(io.LimitReader(sandboxInput, limit+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > limit {
		return errors.New("fs-write input exceeds the limit")
	}
	if err := writeSandboxFile(args[0], data, 0o644); err != nil {
		return err
	}
	info, err := statSandboxPath(args[0])
	if err != nil {
		return err
	}
	return json.NewEncoder(sandboxOutput).Encode(map[string]any{
		"path": args[0], "size": info.Size(), "mode": uint32(info.Mode().Perm()), "created": created,
	})
}

func listFiles(args []string) error {
	if len(args) != 4 {
		return errors.New("fs-list requires path, recursive, include-hidden, and limit")
	}
	recursive, err := strconv.ParseBool(args[1])
	if err != nil {
		return err
	}
	includeHidden, err := strconv.ParseBool(args[2])
	if err != nil {
		return err
	}
	limit, err := strconv.Atoi(args[3])
	if err != nil || limit <= 0 {
		return errors.New("fs-list limit is invalid")
	}
	root := filepath.Clean(args[0])
	entries := make([]fileEntry, 0, limit)
	err = walkSandboxDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := getSandboxRelative(root, path)
		if err != nil {
			return err
		}
		if !includeHidden && isHidden(relative) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !recursive && filepath.Dir(relative) != "." {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		entries = append(
			entries,
			fileEntry{
				Path:  filepath.ToSlash(relative),
				Size:  info.Size(),
				IsDir: entry.IsDir(),
			},
		)
		if len(entries) >= limit {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return json.NewEncoder(sandboxOutput).Encode(entries)
}

func searchFiles(args []string) error {
	if len(args) != 5 {
		return errors.New(
			"fs-search requires path, pattern, case-sensitive, include-hidden, and limit",
		)
	}
	caseSensitive, err := strconv.ParseBool(args[2])
	if err != nil {
		return err
	}
	includeHidden, err := strconv.ParseBool(args[3])
	if err != nil {
		return err
	}
	limit, err := strconv.Atoi(args[4])
	if err != nil || limit <= 0 {
		return errors.New("fs-search limit is invalid")
	}
	pattern := args[1]
	if !caseSensitive {
		pattern = "(?i)" + pattern
	}
	expression, err := regexp.Compile(pattern)
	if err != nil {
		return err
	}
	root := filepath.Clean(args[0])
	matches := make([]searchMatch, 0, limit)
	err = walkSandboxDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		relative, err := getSandboxRelative(root, path)
		if err != nil || !includeHidden && isHidden(relative) {
			return err
		}
		file, err := openSandboxFile(path)
		if err != nil {
			return nil
		}
		scanner := bufio.NewScanner(file)
		line := 0
		for scanner.Scan() {
			line++
			location := expression.FindStringIndex(scanner.Text())
			if location == nil {
				continue
			}
			matches = append(matches, searchMatch{
				Path: filepath.ToSlash(
					relative,
				),
				Line:   line,
				Column: location[0] + 1,
				Text:   scanner.Text(),
			})
			if len(matches) >= limit {
				return filepath.SkipAll
			}
		}
		scanErr := scanner.Err()
		closeErr := file.Close()
		return errors.Join(scanErr, closeErr)
	})
	if err != nil {
		return err
	}
	return json.NewEncoder(sandboxOutput).Encode(matches)
}

func patchFiles(args []string) error {
	if len(args) != 3 {
		return errors.New("fs-patch requires strip, input limit, and allowed paths")
	}
	strip, err := strconv.Atoi(args[0])
	if err != nil || strip < 0 {
		return errors.New("fs-patch strip is invalid")
	}
	limit, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil || limit <= 0 {
		return errors.New("fs-patch limit is invalid")
	}
	rawAllowed, err := base64.RawURLEncoding.DecodeString(args[2])
	if err != nil {
		return errors.New("fs-patch allowed paths are invalid")
	}
	var allowed []string
	if err := json.Unmarshal(rawAllowed, &allowed); err != nil || len(allowed) == 0 {
		return errors.New("fs-patch allowed paths are invalid")
	}
	data, err := io.ReadAll(io.LimitReader(sandboxInput, limit+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > limit {
		return errors.New("fs-patch input exceeds the limit")
	}
	workingDirectory, err := validatePatchPaths(data, strip, allowed)
	if err != nil {
		return err
	}
	command := newSandboxCommand("patch", "--batch", "--forward", "-p"+strconv.Itoa(strip))
	command.Dir = workingDirectory
	command.Stdin = strings.NewReader(string(data))
	command.Stdout = sandboxOutput
	command.Stderr = sandboxErrorOutput
	return command.Run()
}

func validatePatchPaths(data []byte, strip int, allowed []string) (string, error) {
	allowedPaths := make(map[string]struct{}, len(allowed))
	workingDirectory := "/workspace"
	for _, path := range allowed {
		cleaned := filepath.Clean(path)
		allowedPaths[cleaned] = struct{}{}
		if cleaned != "/workspace" && !strings.HasPrefix(cleaned, "/workspace/") {
			workingDirectory = "/"
		}
	}
	if workingDirectory == "/" {
		for path := range allowedPaths {
			if path == "/workspace" || strings.HasPrefix(path, "/workspace/") {
				return "", errors.New("fs-patch cannot span workspace and additional mounts")
			}
		}
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	seen := false
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "--- ") && !strings.HasPrefix(line, "+++ ") {
			continue
		}
		name := strings.Fields(strings.TrimSpace(line[4:]))
		if len(name) == 0 || name[0] == "/dev/null" {
			continue
		}
		parts := strings.Split(filepath.ToSlash(name[0]), "/")
		if len(parts) <= strip {
			return "", errors.New("fs-patch path cannot be stripped")
		}
		relative := filepath.FromSlash(strings.Join(parts[strip:], "/"))
		if filepath.IsAbs(relative) || relative == ".." ||
			strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return "", errors.New("fs-patch path escapes the authorized root")
		}
		path := filepath.Clean(filepath.Join(workingDirectory, relative))
		if _, ok := allowedPaths[path]; !ok {
			return "", errors.New("fs-patch path was not authorized")
		}
		seen = true
	}
	if !seen {
		return "", errors.New("fs-patch contains no authorized paths")
	}
	return workingDirectory, nil
}

func isHidden(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if strings.HasPrefix(part, ".") && part != "." && part != ".." {
			return true
		}
	}
	return false
}

func supervisorStart() error {
	var request supervisorRequest
	if err := json.NewDecoder(io.LimitReader(sandboxInput, 1<<20)).Decode(&request); err != nil {
		return err
	}
	if len(request.Command) == 0 {
		return errors.New("supervisor command is required")
	}
	token, err := randomToken()
	if err != nil {
		return err
	}
	if err := makeSandboxDirs(supervisorDirectory(), 0o700); err != nil {
		return err
	}
	stdout, err := openSupervisorOutput(
		supervisorPath(token, "stdout"),
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
		0o600,
	)
	if err != nil {
		return err
	}
	defer func() { _ = stdout.Close() }()
	stderr, err := openSupervisorOutput(
		supervisorPath(token, "stderr"),
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
		0o600,
	)
	if err != nil {
		return err
	}
	defer func() { _ = stderr.Close() }()
	if request.OutputBytes <= 0 {
		return errors.New("supervisor output limit is required")
	}
	command := newSandboxCommand(
		os.Args[0],
		append(
			[]string{"supervisor-launch", token, strconv.FormatInt(request.OutputBytes, 10)},
			request.Command...)...)
	command.Dir = request.CWD
	command.Env = append(os.Environ(), request.Env...)
	command.Stdout = stdout
	command.Stderr = stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := startSandboxCommand(command); err != nil {
		return err
	}
	startTicks, err := getProcessStartTicks(command.Process.Pid)
	if err != nil {
		_ = killSupervisorProcess(-command.Process.Pid, syscall.SIGKILL)
		return err
	}
	state := supervisorState{
		Token:      token,
		PID:        command.Process.Pid,
		StartedAt:  time.Now().UTC(),
		Label:      request.Label,
		StartTicks: startTicks,
		Command:    strings.Join(request.Command, " "),
		CWD:        request.CWD,
	}
	if err := writeSupervisorState(state); err != nil {
		_ = killSupervisorProcess(-command.Process.Pid, syscall.SIGKILL)
		return err
	}
	return json.NewEncoder(sandboxOutput).Encode(state)
}

func supervisorLaunch(args []string) error {
	if len(args) < 3 {
		return errors.New("supervisor launch requires token, output limit, and command")
	}
	token := args[0]
	limit, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil || limit <= 0 {
		return errors.New("supervisor output limit is invalid")
	}
	command := newSandboxCommand(args[2], args[3:]...)
	command.Stdin = nil
	command.Stdout = &limitedWriter{
		writer:    sandboxOutput,
		remaining: limit,
	}
	command.Stderr = &limitedWriter{
		writer:    sandboxErrorOutput,
		remaining: limit,
	}
	err = command.Run()
	exitCode := 0
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			exitCode = exitError.ExitCode()
		} else {
			exitCode = -1
		}
	}
	var state supervisorState
	var readErr error
	for attempt := 0; attempt < 100; attempt++ {
		state, readErr = readSupervisorState(token)
		if readErr == nil {
			break
		}
		sleepSupervisor(10 * time.Millisecond)
	}
	if readErr != nil {
		return readErr
	}
	endedAt := time.Now().UTC()
	state.EndedAt = &endedAt
	state.ExitCode = &exitCode
	return writeSupervisorState(state)
}

func supervisorStatus(args []string) error {
	if len(args) != 1 {
		return errors.New("supervisor status requires token")
	}
	state, err := readSupervisorState(args[0])
	if err != nil {
		return err
	}
	if state.EndedAt == nil && !checkSupervisorProcess(state) {
		endedAt := time.Now().UTC()
		exitCode := -1
		state.EndedAt = &endedAt
		state.ExitCode = &exitCode
		_ = writeSupervisorState(state)
	}
	return json.NewEncoder(sandboxOutput).Encode(state)
}

func supervisorRead(args []string) error {
	if len(args) != 1 {
		return errors.New("supervisor read requires token")
	}
	stdout, err := readSupervisorFile(supervisorPath(args[0], "stdout"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	stderr, err := readSupervisorFile(supervisorPath(args[0], "stderr"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return json.NewEncoder(sandboxOutput).Encode(map[string]string{
		"stdout": string(stdout), "stderr": string(stderr),
	})
}

func supervisorStop(args []string) error {
	if len(args) != 1 {
		return errors.New("supervisor stop requires token")
	}
	state, err := readSupervisorState(args[0])
	if err != nil {
		return err
	}
	if state.EndedAt == nil && !checkSupervisorProcess(state) {
		endedAt := time.Now().UTC()
		exitCode := -1
		state.EndedAt = &endedAt
		state.ExitCode = &exitCode
		if err := writeSupervisorState(state); err != nil {
			return err
		}
	} else if state.EndedAt == nil {
		_ = killSupervisorProcess(-state.PID, syscall.SIGTERM)
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) && checkSupervisorProcess(state) {
			sleepSupervisor(25 * time.Millisecond)
		}
		if checkSupervisorProcess(state) {
			_ = killSupervisorProcess(-state.PID, syscall.SIGKILL)
		}
		deadline = time.Now().Add(time.Second)
		for time.Now().Before(deadline) && checkProcessGroup(state.PID) {
			sleepSupervisor(25 * time.Millisecond)
		}
		if checkProcessGroup(state.PID) {
			return errors.New("supervisor could not prove the process group terminal")
		}
		endedAt := time.Now().UTC()
		exitCode := -1
		state.EndedAt = &endedAt
		state.ExitCode = &exitCode
		state.Stopped = true
		if err := writeSupervisorState(state); err != nil {
			return err
		}
	}
	return json.NewEncoder(sandboxOutput).Encode(state)
}

func processGroupExists(pid int) bool {
	return pid > 0 && syscall.Kill(-pid, 0) == nil
}

type limitedWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *limitedWriter) Write(value []byte) (int, error) {
	original := len(value)
	if w.remaining <= 0 {
		return original, nil
	}
	write := value
	if int64(len(write)) > w.remaining {
		write = write[:w.remaining]
	}
	n, err := w.writer.Write(write)
	w.remaining -= int64(n)
	if err != nil {
		return n, err
	}
	return original, nil
}

func isSupervisorProcess(state supervisorState) bool {
	if state.PID <= 0 || state.StartTicks == "" || syscall.Kill(state.PID, 0) != nil {
		return false
	}
	ticks, err := processStartTicks(state.PID)
	return err == nil && ticks == state.StartTicks
}

func processStartTicks(pid int) (string, error) {
	raw, err := readProcessStat(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", err
	}
	closeParen := strings.LastIndexByte(string(raw), ')')
	if closeParen < 0 {
		return "", errors.New("process identity is invalid")
	}
	fields := strings.Fields(string(raw[closeParen+1:]))
	if len(fields) <= 19 {
		return "", errors.New("process identity is incomplete")
	}
	return fields[19], nil
}

func supervisorDirectory() string {
	return supervisorRoot
}

func supervisorPath(token string, suffix string) string {
	return filepath.Join(supervisorDirectory(), token+"."+suffix)
}

func writeSupervisorState(state supervisorState) error {
	raw, _ := json.Marshal(state)
	temporary := supervisorPath(state.Token, "json.tmp")
	if err := writeSandboxFile(temporary, raw, 0o600); err != nil {
		return err
	}
	return renameSupervisorFile(temporary, supervisorPath(state.Token, "json"))
}

func readSupervisorState(token string) (supervisorState, error) {
	raw, err := readSupervisorFile(supervisorPath(token, "json"))
	if err != nil {
		return supervisorState{}, err
	}
	var state supervisorState
	if err := json.Unmarshal(raw, &state); err != nil {
		return supervisorState{}, err
	}
	return state, nil
}

func randomToken() (string, error) {
	value := make([]byte, 24)
	if _, err := readSandboxToken(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
