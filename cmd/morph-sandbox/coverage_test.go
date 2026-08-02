package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestMain_ReportsRunFailure(t *testing.T) {
	restoreSandboxGlobals(t)
	originalArgs := os.Args
	os.Args = []string{"morph-sandbox"}
	var output bytes.Buffer
	sandboxErrorOutput = &output
	exitCode := 0
	exitSandboxProcess = func(code int) {
		exitCode = code
	}
	t.Cleanup(func() {
		os.Args = originalArgs
	})

	main()

	require.Equal(t, 1, exitCode)
	require.Equal(t, "sandbox command is required\n", output.String())
}

func TestRun_DispatchesCommands(t *testing.T) {
	restoreSandboxGlobals(t)
	slept := false
	sleepSandbox = func(time.Duration) {
		slept = true
	}
	require.NoError(t, run([]string{"sleep-forever"}))
	require.True(t, slept)

	require.EqualError(t, run(nil), "sandbox command is required")
	require.EqualError(
		t,
		run([]string{"--control"}),
		"controlled command requires a limit and command",
	)
	require.EqualError(t, run([]string{"fs-read"}), "fs-read requires path and limit")
	require.EqualError(t, run([]string{"fs-write"}), "fs-write requires path, create-dirs, and limit")
	require.EqualError(
		t,
		run([]string{"fs-list"}),
		"fs-list requires path, recursive, include-hidden, and limit",
	)
	require.EqualError(
		t,
		run([]string{"fs-search"}),
		"fs-search requires path, pattern, case-sensitive, include-hidden, and limit",
	)
	require.EqualError(
		t,
		run([]string{"fs-patch"}),
		"fs-patch requires strip, input limit, and allowed paths",
	)
	require.Error(t, run([]string{"supervisor-start"}))
	require.EqualError(
		t,
		run([]string{"supervisor-launch"}),
		"supervisor launch requires token, output limit, and command",
	)
	require.EqualError(
		t,
		run([]string{"supervisor-status"}),
		"supervisor status requires token",
	)
	require.EqualError(
		t,
		run([]string{"supervisor-read"}),
		"supervisor read requires token",
	)
	require.EqualError(
		t,
		run([]string{"supervisor-stop"}),
		"supervisor stop requires token",
	)

	executed := false
	executeSandboxCommand = func(path string, args []string, environment []string) error {
		executed = true
		require.NotEmpty(t, path)
		require.Equal(t, []string{"true"}, args)
		require.NotEmpty(t, environment)
		return errors.New("exec stopped")
	}
	err := run([]string{"true"})
	require.EqualError(t, err, "exec stopped")
	require.True(t, executed)
}

func TestRunControlled(t *testing.T) {
	restoreSandboxGlobals(t)
	require.EqualError(t, runControlled(nil), "controlled command requires a limit and command")
	require.EqualError(
		t,
		runControlled([]string{"bad", "true"}),
		"control payload limit is invalid",
	)
	require.EqualError(
		t,
		runControlled([]string{"0", "true"}),
		"control payload limit is invalid",
	)

	sandboxInput = errorReader{}
	require.EqualError(t, runControlled([]string{"100", "true"}), "read failed")

	sandboxInput = controlFrame(t, []byte(`{"token":"secret"}`))
	require.EqualError(
		t,
		runControlled([]string{"10", "true"}),
		"control payload exceeds the limit",
	)

	truncated := new(bytes.Buffer)
	require.NoError(t, binary.Write(truncated, binary.BigEndian, uint32(10)))
	truncated.WriteString("short")
	sandboxInput = truncated
	require.Error(t, runControlled([]string{"100", "true"}))

	invalidFrame := controlFrame(t, []byte("invalid"))
	require.Len(t, invalidFrame.Bytes(), 11)
	sandboxInput = invalidFrame
	require.EqualError(t, runControlled([]string{"100", "true"}), "control payload is invalid")

	var gotEnvironment []string
	executeSandboxCommand = func(_ string, _ []string, environment []string) error {
		gotEnvironment = environment
		return nil
	}
	sandboxInput = controlFrame(t, []byte(`{"api-key":"secret"}`))
	require.NoError(t, runControlled([]string{"100", "true"}))
	require.Contains(t, gotEnvironment, "MORPH_SECRET_API_KEY=secret")
	require.Equal(t, "MORPH_SECRET_NAME_WITH_SYMBOLS_", secretEnvironmentName("name-with symbols!"))
}

func TestExecCommand(t *testing.T) {
	restoreSandboxGlobals(t)
	_, lookupErr := exec.LookPath("definitely-missing-morph-command")
	require.Error(t, lookupErr)
	require.Error(t, execCommand([]string{"definitely-missing-morph-command"}, nil))

	executeSandboxCommand = func(path string, args []string, environment []string) error {
		require.NotEmpty(t, path)
		require.Equal(t, []string{"true"}, args)
		require.Equal(t, []string{"A=B"}, environment)
		return nil
	}
	require.NoError(t, execCommand([]string{"true"}, []string{"A=B"}))
}

func TestReadFile(t *testing.T) {
	restoreSandboxGlobals(t)
	path := filepath.Join(t.TempDir(), "file.txt")
	require.NoError(t, os.WriteFile(path, []byte("hello"), 0o600))

	require.EqualError(t, readFile(nil), "fs-read requires path and limit")
	require.EqualError(t, readFile([]string{path, "bad"}), "fs-read limit is invalid")
	require.EqualError(t, readFile([]string{path, "0"}), "fs-read limit is invalid")
	require.Error(t, readFile([]string{path + ".missing", "10"}))
	require.EqualError(t, readFile([]string{path, "2"}), "file exceeds the read limit")

	var output bytes.Buffer
	sandboxOutput = &output
	require.NoError(t, readFile([]string{path, "10"}))
	require.Equal(t, "hello", output.String())

	sandboxOutput = errorWriter{}
	require.EqualError(t, readFile([]string{path, "10"}), "write failed")

	statOpenedSandboxFile = func(*os.File) (os.FileInfo, error) {
		return nil, errors.New("stat failed")
	}
	require.EqualError(t, readFile([]string{path, "10"}), "stat failed")
}

func TestWriteFile(t *testing.T) {
	restoreSandboxGlobals(t)
	path := filepath.Join(t.TempDir(), "nested", "file.txt")
	require.EqualError(t, writeFile(nil), "fs-write requires path, create-dirs, and limit")
	require.EqualError(
		t,
		writeFile([]string{path, "bad", "10"}),
		"fs-write create-dirs is invalid",
	)
	require.EqualError(
		t,
		writeFile([]string{path, "true", "bad"}),
		"fs-write limit is invalid",
	)

	sandboxInput = strings.NewReader("too long")
	require.EqualError(
		t,
		writeFile([]string{path, "true", "2"}),
		"fs-write input exceeds the limit",
	)

	sandboxInput = strings.NewReader("hello")
	var output bytes.Buffer
	sandboxOutput = &output
	require.NoError(t, writeFile([]string{path, "true", "10"}))
	require.FileExists(t, path)
	require.Contains(t, output.String(), `"created":true`)

	sandboxInput = strings.NewReader("updated")
	output.Reset()
	require.NoError(t, writeFile([]string{path, "false", "10"}))
	require.Contains(t, output.String(), `"created":false`)

	sandboxInput = errorReader{}
	require.EqualError(t, writeFile([]string{path, "false", "10"}), "read failed")

	sandboxInput = strings.NewReader("data")
	require.Error(t, writeFile([]string{t.TempDir(), "false", "10"}))

	sandboxInput = strings.NewReader("data")
	sandboxOutput = errorWriter{}
	require.EqualError(t, writeFile([]string{path, "false", "10"}), "write failed")

	makeSandboxDirs = func(string, os.FileMode) error {
		return errors.New("mkdir failed")
	}
	sandboxInput = strings.NewReader("data")
	require.EqualError(t, writeFile([]string{path, "true", "10"}), "mkdir failed")

	makeSandboxDirs = os.MkdirAll
	writeSandboxFile = func(string, []byte, os.FileMode) error {
		return errors.New("file write failed")
	}
	sandboxInput = strings.NewReader("data")
	require.EqualError(t, writeFile([]string{path, "false", "10"}), "file write failed")

	writeSandboxFile = os.WriteFile
	statCalls := 0
	statSandboxPath = func(path string) (os.FileInfo, error) {
		statCalls++
		if statCalls == 1 {
			return os.Stat(path)
		}
		return nil, errors.New("stat failed")
	}
	sandboxInput = strings.NewReader("data")
	require.EqualError(t, writeFile([]string{path, "false", "10"}), "stat failed")
}

func TestListFiles(t *testing.T) {
	restoreSandboxGlobals(t)
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "nested", "deeper"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".hidden-dir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "visible.txt"), []byte("a"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".hidden"), []byte("b"), 0o600))

	require.EqualError(
		t,
		listFiles(nil),
		"fs-list requires path, recursive, include-hidden, and limit",
	)
	require.Error(t, listFiles([]string{root, "bad", "false", "10"}))
	require.Error(t, listFiles([]string{root, "false", "bad", "10"}))
	require.EqualError(
		t,
		listFiles([]string{root, "false", "false", "0"}),
		"fs-list limit is invalid",
	)
	require.Error(t, listFiles([]string{filepath.Join(root, "missing"), "false", "false", "10"}))

	var output bytes.Buffer
	sandboxOutput = &output
	require.NoError(t, listFiles([]string{root, "false", "false", "10"}))
	require.Contains(t, output.String(), `"path":"visible.txt"`)
	require.NotContains(t, output.String(), ".hidden")

	output.Reset()
	require.NoError(t, listFiles([]string{root, "true", "true", "1"}))
	var entries []fileEntry
	require.NoError(t, json.Unmarshal(output.Bytes(), &entries))
	require.Len(t, entries, 1)

	sandboxOutput = errorWriter{}
	require.EqualError(
		t,
		listFiles([]string{root, "true", "true", "10"}),
		"write failed",
	)

	walkSandboxDir = func(root string, visit fs.WalkDirFunc) error {
		require.NoError(t, visit(root, nil, nil))
		getSandboxRelative = func(string, string) (string, error) {
			return "", errors.New("relative failed")
		}
		return visit(filepath.Join(root, "entry"), sandboxDirEntry{}, nil)
	}
	_, err := captureListFiles(t, []string{root, "true", "true", "10"})
	require.EqualError(t, err, "relative failed")

	getSandboxRelative = filepath.Rel
	walkSandboxDir = func(root string, visit fs.WalkDirFunc) error {
		require.NoError(t, visit(root, nil, nil))
		return visit(
			filepath.Join(root, "entry"),
			sandboxDirEntry{
				infoErr: errors.New("info failed"),
			},
			nil,
		)
	}
	_, err = captureListFiles(t, []string{root, "true", "true", "10"})
	require.EqualError(t, err, "info failed")

	walkSandboxDir = func(root string, visit fs.WalkDirFunc) error {
		require.NoError(t, visit(root, nil, nil))
		return visit(
			filepath.Join(root, "nested", "file.txt"),
			sandboxDirEntry{},
			nil,
		)
	}
	_, err = captureListFiles(t, []string{root, "false", "true", "10"})
	require.NoError(t, err)
}

func TestSearchFiles(t *testing.T) {
	restoreSandboxGlobals(t)
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".hidden-dir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "match.txt"), []byte("Needle\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "none.txt"), []byte("nothing\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".hidden"), []byte("Needle\n"), 0o600))

	require.EqualError(
		t,
		searchFiles(nil),
		"fs-search requires path, pattern, case-sensitive, include-hidden, and limit",
	)
	require.Error(t, searchFiles([]string{root, "x", "bad", "false", "10"}))
	require.Error(t, searchFiles([]string{root, "x", "false", "bad", "10"}))
	require.EqualError(
		t,
		searchFiles([]string{root, "x", "false", "false", "0"}),
		"fs-search limit is invalid",
	)
	require.Error(t, searchFiles([]string{root, "[", "true", "false", "10"}))
	require.Error(
		t,
		searchFiles([]string{filepath.Join(root, "missing"), "x", "true", "false", "10"}),
	)

	var output bytes.Buffer
	sandboxOutput = &output
	require.NoError(t, searchFiles([]string{root, "needle", "false", "false", "10"}))
	var matches []searchMatch
	require.NoError(t, json.Unmarshal(output.Bytes(), &matches))
	require.Len(t, matches, 1)
	require.Equal(t, 1, matches[0].Column)

	output.Reset()
	require.NoError(t, searchFiles([]string{root, "Needle", "true", "true", "1"}))
	require.NoError(t, json.Unmarshal(output.Bytes(), &matches))
	require.Len(t, matches, 1)

	sandboxOutput = errorWriter{}
	require.EqualError(
		t,
		searchFiles([]string{root, "Needle", "true", "true", "10"}),
		"write failed",
	)

	openSandboxFile = func(string) (*os.File, error) {
		return nil, errors.New("open failed")
	}
	sandboxOutput = &bytes.Buffer{}
	require.NoError(t, searchFiles([]string{root, "Needle", "true", "true", "10"}))
}

func TestPatchValidation(t *testing.T) {
	workspace := "/workspace/file.txt"
	tests := []struct {
		name    string
		patch   string
		strip   int
		allowed []string
		err     string
	}{
		{
			name:    "mixed roots",
			patch:   "--- a/file.txt\n+++ b/file.txt\n",
			strip:   1,
			allowed: []string{workspace, "/other/file.txt"},
			err:     "fs-patch cannot span workspace and additional mounts",
		},
		{
			name:    "strip",
			patch:   "--- file.txt\n+++ file.txt\n",
			strip:   1,
			allowed: []string{workspace},
			err:     "fs-patch path cannot be stripped",
		},
		{
			name:    "escape",
			patch:   "--- a/../file.txt\n+++ b/../file.txt\n",
			strip:   1,
			allowed: []string{workspace},
			err:     "fs-patch path escapes the authorized root",
		},
		{
			name:    "unauthorized",
			patch:   "--- a/other.txt\n+++ b/other.txt\n",
			strip:   1,
			allowed: []string{workspace},
			err:     "fs-patch path was not authorized",
		},
		{
			name:    "no paths",
			patch:   "text only\n--- /dev/null\n+++ /dev/null\n",
			strip:   0,
			allowed: []string{workspace},
			err:     "fs-patch contains no authorized paths",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := validatePatchPaths([]byte(test.patch), test.strip, test.allowed)
			require.EqualError(t, err, test.err)
		})
	}

	workingDirectory, err := validatePatchPaths(
		[]byte("--- a/file.txt\n+++ b/file.txt\n"),
		1,
		[]string{workspace},
	)
	require.NoError(t, err)
	require.Equal(t, "/workspace", workingDirectory)
	require.True(t, isHidden("nested/.secret/file"))
	require.False(t, isHidden("nested/visible/file"))
}

func TestPatchFiles(t *testing.T) {
	restoreSandboxGlobals(t)
	require.EqualError(
		t,
		patchFiles(nil),
		"fs-patch requires strip, input limit, and allowed paths",
	)
	require.EqualError(
		t,
		patchFiles([]string{"bad", "10", "value"}),
		"fs-patch strip is invalid",
	)
	require.EqualError(
		t,
		patchFiles([]string{"0", "bad", "value"}),
		"fs-patch limit is invalid",
	)
	require.EqualError(
		t,
		patchFiles([]string{"0", "10", "%%%"}),
		"fs-patch allowed paths are invalid",
	)
	encodedEmpty := base64.RawURLEncoding.EncodeToString([]byte("[]"))
	require.EqualError(
		t,
		patchFiles([]string{"0", "10", encodedEmpty}),
		"fs-patch allowed paths are invalid",
	)

	allowed := base64.RawURLEncoding.EncodeToString([]byte(`["/workspace/file.txt"]`))
	sandboxInput = strings.NewReader(strings.Repeat("x", 20))
	require.EqualError(
		t,
		patchFiles([]string{"1", "10", allowed}),
		"fs-patch input exceeds the limit",
	)
	sandboxInput = errorReader{}
	require.EqualError(t, patchFiles([]string{"1", "100", allowed}), "read failed")
	sandboxInput = strings.NewReader("invalid")
	require.EqualError(
		t,
		patchFiles([]string{"1", "100", allowed}),
		"fs-patch contains no authorized paths",
	)

	newSandboxCommand = func(string, ...string) *exec.Cmd {
		return exec.Command("/usr/bin/true")
	}
	target := filepath.Join(t.TempDir(), "file.txt")
	encodedTarget, err := json.Marshal([]string{target})
	require.NoError(t, err)
	allowedTarget := base64.RawURLEncoding.EncodeToString(encodedTarget)
	sandboxInput = strings.NewReader("--- " + target + "\n+++ " + target + "\n")
	require.NoError(t, patchFiles([]string{"1", "1000", allowedTarget}))
}

func TestSupervisorStateAndLaunch(t *testing.T) {
	restoreSandboxGlobals(t)
	supervisorRoot = t.TempDir()
	state := supervisorState{
		Token:      "token",
		PID:        os.Getpid(),
		StartTicks: "ticks",
		StartedAt:  time.Now().UTC(),
		Command:    "command",
	}
	require.NoError(t, writeSupervisorState(state))
	loaded, err := readSupervisorState("token")
	require.NoError(t, err)
	require.Equal(t, state.Token, loaded.Token)
	require.Equal(
		t,
		filepath.Join(supervisorRoot, "token.stdout"),
		supervisorPath("token", "stdout"),
	)
	require.Equal(t, supervisorRoot, supervisorDirectory())

	var output bytes.Buffer
	sandboxOutput = &output
	require.EqualError(
		t,
		supervisorLaunch(nil),
		"supervisor launch requires token, output limit, and command",
	)
	require.EqualError(
		t,
		supervisorLaunch([]string{"token", "bad", "true"}),
		"supervisor output limit is invalid",
	)

	require.NoError(
		t,
		supervisorLaunch([]string{"token", "100", "/bin/sh", "-c", "printf output"}),
	)
	loaded, err = readSupervisorState("token")
	require.NoError(t, err)
	require.NotNil(t, loaded.EndedAt)
	require.Equal(t, 0, *loaded.ExitCode)

	state.Token = "failure"
	state.EndedAt = nil
	state.ExitCode = nil
	require.NoError(t, writeSupervisorState(state))
	require.NoError(
		t,
		supervisorLaunch([]string{"failure", "100", "/bin/sh", "-c", "exit 7"}),
	)
	loaded, err = readSupervisorState("failure")
	require.NoError(t, err)
	require.Equal(t, 7, *loaded.ExitCode)

	state.Token = "missing-command"
	state.EndedAt = nil
	state.ExitCode = nil
	require.NoError(t, writeSupervisorState(state))
	require.NoError(
		t,
		supervisorLaunch([]string{"missing-command", "100", "/missing/command"}),
	)
	loaded, err = readSupervisorState("missing-command")
	require.NoError(t, err)
	require.Equal(t, -1, *loaded.ExitCode)

	sleepSupervisor = func(time.Duration) {}
	err = supervisorLaunch([]string{"absent", "100", "/bin/true"})
	require.Error(t, err)
}

func TestSupervisorStart(t *testing.T) {
	restoreSandboxGlobals(t)
	supervisorRoot = t.TempDir()
	sandboxInput = strings.NewReader(`{"command":[]}`)
	require.EqualError(t, supervisorStart(), "supervisor command is required")

	readSandboxToken = func(value []byte) (int, error) {
		return 0, errors.New("token failed")
	}
	sandboxInput = strings.NewReader(`{"command":["/bin/true"],"output_bytes":100}`)
	require.EqualError(t, supervisorStart(), "token failed")

	readSandboxToken = func(value []byte) (int, error) {
		for index := range value {
			value[index] = byte(index)
		}
		return len(value), nil
	}
	getProcessStartTicks = func(int) (string, error) {
		return "ticks", nil
	}
	var output bytes.Buffer
	sandboxOutput = &output
	sandboxInput = strings.NewReader(
		`{"command":["/bin/true"],"output_bytes":100,"label":"job"}`,
	)
	require.NoError(t, supervisorStart())
	var state supervisorState
	require.NoError(t, json.Unmarshal(output.Bytes(), &state))
	require.Equal(t, "job", state.Label)
	require.Equal(t, "ticks", state.StartTicks)

	getProcessStartTicks = func(int) (string, error) {
		return "", errors.New("ticks failed")
	}
	killed := false
	killSupervisorProcess = func(int, syscall.Signal) error {
		killed = true
		return nil
	}
	sandboxInput = strings.NewReader(`{"command":["/bin/true"],"output_bytes":100}`)
	require.EqualError(t, supervisorStart(), "ticks failed")
	require.True(t, killed)

	makeSandboxDirs = func(string, os.FileMode) error {
		return errors.New("mkdir failed")
	}
	getProcessStartTicks = func(int) (string, error) {
		return "ticks", nil
	}
	sandboxInput = strings.NewReader(`{"command":["/bin/true"],"output_bytes":100}`)
	require.EqualError(t, supervisorStart(), "mkdir failed")

	makeSandboxDirs = os.MkdirAll
	openSupervisorOutput = func(string, int, os.FileMode) (*os.File, error) {
		return nil, errors.New("stdout open failed")
	}
	sandboxInput = strings.NewReader(`{"command":["/bin/true"],"output_bytes":100}`)
	require.EqualError(t, supervisorStart(), "stdout open failed")

	openCalls := 0
	openSupervisorOutput = func(
		path string,
		flag int,
		mode os.FileMode,
	) (*os.File, error) {
		openCalls++
		if openCalls == 2 {
			return nil, errors.New("stderr open failed")
		}
		return os.OpenFile(path, flag, mode)
	}
	sandboxInput = strings.NewReader(`{"command":["/bin/true"],"output_bytes":100}`)
	require.EqualError(t, supervisorStart(), "stderr open failed")

	openSupervisorOutput = os.OpenFile
	sandboxInput = strings.NewReader(`{"command":["/bin/true"]}`)
	require.EqualError(t, supervisorStart(), "supervisor output limit is required")

	startSandboxCommand = func(*exec.Cmd) error {
		return errors.New("start failed")
	}
	sandboxInput = strings.NewReader(`{"command":["/bin/true"],"output_bytes":100}`)
	require.EqualError(t, supervisorStart(), "start failed")

	startSandboxCommand = func(command *exec.Cmd) error {
		return command.Start()
	}
	writeSandboxFile = func(string, []byte, os.FileMode) error {
		return errors.New("state write failed")
	}
	killed = false
	sandboxInput = strings.NewReader(`{"command":["/bin/true"],"output_bytes":100}`)
	require.EqualError(t, supervisorStart(), "state write failed")
	require.True(t, killed)
}

func TestSupervisorStatusReadAndStop(t *testing.T) {
	restoreSandboxGlobals(t)
	supervisorRoot = t.TempDir()
	state := supervisorState{
		Token:      "token",
		PID:        123,
		StartTicks: "ticks",
		StartedAt:  time.Now().UTC(),
		Command:    "command",
	}
	require.NoError(t, writeSupervisorState(state))
	var output bytes.Buffer
	sandboxOutput = &output
	checkSupervisorProcess = func(supervisorState) bool {
		return false
	}
	require.NoError(t, supervisorStatus([]string{"token"}))
	var updated supervisorState
	require.NoError(t, json.Unmarshal(output.Bytes(), &updated))
	require.NotNil(t, updated.EndedAt)

	output.Reset()
	require.NoError(t, supervisorStatus([]string{"token"}))
	require.Error(t, supervisorStatus([]string{"missing"}))

	require.NoError(t, os.WriteFile(supervisorPath("token", "stdout"), []byte("out"), 0o600))
	require.NoError(t, os.WriteFile(supervisorPath("token", "stderr"), []byte("err"), 0o600))
	output.Reset()
	require.NoError(t, supervisorRead([]string{"token"}))
	require.JSONEq(t, `{"stdout":"out","stderr":"err"}`, output.String())
	output.Reset()
	require.NoError(t, supervisorRead([]string{"absent"}))
	require.JSONEq(t, `{"stdout":"","stderr":""}`, output.String())

	readSupervisorFile = func(path string) ([]byte, error) {
		if strings.HasSuffix(path, ".stdout") {
			return nil, errors.New("stdout failed")
		}
		return nil, os.ErrNotExist
	}
	require.EqualError(t, supervisorRead([]string{"token"}), "stdout failed")
	readSupervisorFile = func(path string) ([]byte, error) {
		if strings.HasSuffix(path, ".stderr") {
			return nil, errors.New("stderr failed")
		}
		return nil, os.ErrNotExist
	}
	require.EqualError(t, supervisorRead([]string{"token"}), "stderr failed")
	readSupervisorFile = os.ReadFile

	output.Reset()
	require.NoError(t, supervisorStop([]string{"token"}))
	require.Error(t, supervisorStop([]string{"missing"}))

	state.Token = "dead"
	require.NoError(t, writeSupervisorState(state))
	output.Reset()
	require.NoError(t, supervisorStop([]string{"dead"}))
	loaded, err := readSupervisorState("dead")
	require.NoError(t, err)
	require.Equal(t, -1, *loaded.ExitCode)

	state.Token = "live"
	require.NoError(t, writeSupervisorState(state))
	checks := 0
	checkSupervisorProcess = func(supervisorState) bool {
		checks++
		return checks == 1
	}
	checkProcessGroup = func(int) bool {
		return false
	}
	sleepSupervisor = func(time.Duration) {}
	killed := false
	killSupervisorProcess = func(int, syscall.Signal) error {
		killed = true
		return nil
	}
	output.Reset()
	require.NoError(t, supervisorStop([]string{"live"}))
	require.True(t, killed)
	loaded, err = readSupervisorState("live")
	require.NoError(t, err)
	require.True(t, loaded.Stopped)

	state.Token = "kill"
	state.EndedAt = nil
	state.ExitCode = nil
	require.NoError(t, writeSupervisorState(state))
	checks = 0
	checkSupervisorProcess = func(supervisorState) bool {
		checks++
		return checks == 1 || checks == 2 || checks == 4
	}
	groups := 0
	checkProcessGroup = func(int) bool {
		groups++
		return groups == 1
	}
	output.Reset()
	require.NoError(t, supervisorStop([]string{"kill"}))

	state.Token = "group-alive"
	require.NoError(t, writeSupervisorState(state))
	checks = 0
	checkSupervisorProcess = func(supervisorState) bool {
		checks++
		return checks == 1
	}
	groups = 0
	checkProcessGroup = func(int) bool {
		groups++
		return groups == 2
	}
	require.EqualError(
		t,
		supervisorStop([]string{"group-alive"}),
		"supervisor could not prove the process group terminal",
	)

	writeSandboxFile = os.WriteFile
	state.Token = "dead-write"
	require.NoError(t, writeSupervisorState(state))
	writeSandboxFile = func(string, []byte, os.FileMode) error {
		return errors.New("state write failed")
	}
	checkSupervisorProcess = func(supervisorState) bool {
		return false
	}
	require.EqualError(t, supervisorStop([]string{"dead-write"}), "state write failed")

	writeSandboxFile = os.WriteFile
	state.Token = "live-write"
	require.NoError(t, writeSupervisorState(state))
	writeSandboxFile = func(string, []byte, os.FileMode) error {
		return errors.New("state write failed")
	}
	checks = 0
	checkSupervisorProcess = func(supervisorState) bool {
		checks++
		return checks == 1
	}
	checkProcessGroup = func(int) bool {
		return false
	}
	require.EqualError(t, supervisorStop([]string{"live-write"}), "state write failed")
}

func TestSupervisorHelpers(t *testing.T) {
	restoreSandboxGlobals(t)
	writer := &limitedWriter{
		writer:    &bytes.Buffer{},
		remaining: 0,
	}
	n, err := writer.Write([]byte("ignored"))
	require.NoError(t, err)
	require.Equal(t, len("ignored"), n)

	var output bytes.Buffer
	writer = &limitedWriter{
		writer:    &output,
		remaining: 3,
	}
	n, err = writer.Write([]byte("abcdef"))
	require.NoError(t, err)
	require.Equal(t, 6, n)
	require.Equal(t, "abc", output.String())

	writer = &limitedWriter{
		writer:    errorWriter{},
		remaining: 3,
	}
	_, err = writer.Write([]byte("abcdef"))
	require.EqualError(t, err, "write failed")

	require.False(t, processGroupExists(0))
	require.False(t, isSupervisorProcess(supervisorState{}))
	readProcessStat = func(string) ([]byte, error) {
		return nil, errors.New("read failed")
	}
	_, err = processStartTicks(123)
	require.EqualError(t, err, "read failed")
	readProcessStat = func(string) ([]byte, error) {
		return []byte("invalid"), nil
	}
	_, err = processStartTicks(123)
	require.EqualError(t, err, "process identity is invalid")
	readProcessStat = func(string) ([]byte, error) {
		return []byte("123 (command) S 1"), nil
	}
	_, err = processStartTicks(123)
	require.EqualError(t, err, "process identity is incomplete")
	readProcessStat = func(string) ([]byte, error) {
		return []byte(
			"123 (command) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 ticks",
		), nil
	}
	ticks, err := processStartTicks(123)
	require.NoError(t, err)
	require.Equal(t, "ticks", ticks)
	require.True(t, isSupervisorProcess(supervisorState{
		PID:        os.Getpid(),
		StartTicks: "ticks",
	}))

	readSandboxToken = func([]byte) (int, error) {
		return 0, errors.New("entropy failed")
	}
	_, err = randomToken()
	require.EqualError(t, err, "entropy failed")
	readSandboxToken = func(value []byte) (int, error) {
		for index := range value {
			value[index] = byte(index)
		}
		return len(value), nil
	}
	token, err := randomToken()
	require.NoError(t, err)
	require.Len(t, token, 32)

	supervisorRoot = t.TempDir()
	require.NoError(t, os.WriteFile(supervisorPath("invalid", "json"), []byte("{"), 0o600))
	_, err = readSupervisorState("invalid")
	require.Error(t, err)
	_, err = readSupervisorState("missing")
	require.Error(t, err)

	blocked := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blocked, []byte("value"), 0o600))
	supervisorRoot = blocked
	require.Error(t, writeSupervisorState(supervisorState{
		Token: "token",
	}))
}

func controlFrame(t *testing.T, payload []byte) *bytes.Buffer {
	t.Helper()
	buffer := new(bytes.Buffer)
	require.NoError(t, binary.Write(buffer, binary.BigEndian, uint32(len(payload))))
	buffer.Write(payload)
	return buffer
}

type sandboxDirEntry struct {
	infoErr error
}

func (sandboxDirEntry) Name() string {
	return "entry"
}

func (sandboxDirEntry) IsDir() bool {
	return false
}

func (sandboxDirEntry) Type() os.FileMode {
	return 0
}

func (entry sandboxDirEntry) Info() (os.FileInfo, error) {
	return nil, entry.infoErr
}

func captureListFiles(t *testing.T, args []string) (string, error) {
	t.Helper()
	var output bytes.Buffer
	sandboxOutput = &output
	err := listFiles(args)
	return output.String(), err
}

func restoreSandboxGlobals(t *testing.T) {
	t.Helper()
	originalInput := sandboxInput
	originalOutput := sandboxOutput
	originalErrorOutput := sandboxErrorOutput
	originalExecute := executeSandboxCommand
	originalExit := exitSandboxProcess
	originalSandboxSleep := sleepSandbox
	originalReadStat := readProcessStat
	originalReadToken := readSandboxToken
	originalRoot := supervisorRoot
	originalCommand := newSandboxCommand
	originalTicks := getProcessStartTicks
	originalCheck := checkSupervisorProcess
	originalGroup := checkProcessGroup
	originalKill := killSupervisorProcess
	originalSleep := sleepSupervisor
	originalStatOpened := statOpenedSandboxFile
	originalStatPath := statSandboxPath
	originalMkdir := makeSandboxDirs
	originalWrite := writeSandboxFile
	originalWalk := walkSandboxDir
	originalRelative := getSandboxRelative
	originalOpen := openSandboxFile
	originalOpenOutput := openSupervisorOutput
	originalReadFile := readSupervisorFile
	originalRename := renameSupervisorFile
	originalStart := startSandboxCommand
	t.Cleanup(func() {
		sandboxInput = originalInput
		sandboxOutput = originalOutput
		sandboxErrorOutput = originalErrorOutput
		executeSandboxCommand = originalExecute
		exitSandboxProcess = originalExit
		sleepSandbox = originalSandboxSleep
		readProcessStat = originalReadStat
		readSandboxToken = originalReadToken
		supervisorRoot = originalRoot
		newSandboxCommand = originalCommand
		getProcessStartTicks = originalTicks
		checkSupervisorProcess = originalCheck
		checkProcessGroup = originalGroup
		killSupervisorProcess = originalKill
		sleepSupervisor = originalSleep
		statOpenedSandboxFile = originalStatOpened
		statSandboxPath = originalStatPath
		makeSandboxDirs = originalMkdir
		writeSandboxFile = originalWrite
		walkSandboxDir = originalWalk
		getSandboxRelative = originalRelative
		openSandboxFile = originalOpen
		openSupervisorOutput = originalOpenOutput
		readSupervisorFile = originalReadFile
		renameSupervisorFile = originalRename
		startSandboxCommand = originalStart
	})
}
