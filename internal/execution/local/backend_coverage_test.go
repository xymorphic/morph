package local

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
	"github.com/stretchr/testify/require"

	commandplan "github.com/xymorphic/morph/internal/command"
	processenv "github.com/xymorphic/morph/internal/environment/process"
	"github.com/xymorphic/morph/internal/execution"
	"github.com/xymorphic/morph/internal/guardrails"
)

type processManagerStub struct {
	startRequest processenv.StartRequest
	startSession string
	getSession   string
	getID        string
	readSession  string
	readRequest  processenv.ReadRequest
	stopSessions []string
	stopIDs      []string
	listSessions []string
	processes    []processenv.Info
	startInfo    processenv.Info
	getInfo      processenv.Info
	readOutput   processenv.Output
	stopInfo     processenv.Info
	startErr     error
	getErr       error
	readErr      error
	stopErr      error
}

func (s *processManagerStub) Start(
	_ context.Context,
	sessionID string,
	request processenv.StartRequest,
) (processenv.Info, error) {
	s.startSession = sessionID
	s.startRequest = request
	return s.startInfo, s.startErr
}

func (s *processManagerStub) Get(sessionID string, processID string) (processenv.Info, error) {
	s.getSession = sessionID
	s.getID = processID
	return s.getInfo, s.getErr
}

func (s *processManagerStub) Read(
	sessionID string,
	request processenv.ReadRequest,
) (processenv.Output, error) {
	s.readSession = sessionID
	s.readRequest = request
	return s.readOutput, s.readErr
}

func (s *processManagerStub) Stop(
	_ context.Context,
	sessionID string,
	processID string,
) (processenv.Info, error) {
	s.stopSessions = append(s.stopSessions, sessionID)
	s.stopIDs = append(s.stopIDs, processID)
	return s.stopInfo, s.stopErr
}

func (s *processManagerStub) List(sessionID string) []processenv.Info {
	s.listSessions = append(s.listSessions, sessionID)
	return append([]processenv.Info(nil), s.processes...)
}

func TestBackend_AcquireAndStatus(t *testing.T) {
	backend := New(guardrails.FilesystemPolicy{}, nil)
	require.NotNil(t, backend.processes)
	spec := getLocalCommandSpec(t, testLocalPlan(t, "printf", "hello"))

	status, err := backend.Acquire(context.Background(), spec)
	require.NoError(t, err)
	require.Equal(t, spec.Owner().Fingerprint(), status.ID)
	require.Equal(t, execution.BackendLocal, status.Backend)
	require.Equal(t, execution.EnvironmentReady, status.State)
	require.Len(t, backend.owners, 1)

	statuses, err := backend.Status(context.Background(), spec.Owner())
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	require.Equal(t, spec.Owner().Fingerprint(), statuses[0].ID)

	var nilBackend *Backend
	_, err = nilBackend.Status(context.Background(), spec.Owner())
	require.EqualError(t, err, "local execution backend is required")
	nilBackend.rememberOwner(spec.Owner())
}

func TestBackend_DelegatesManagedProcesses(t *testing.T) {
	manager := &processManagerStub{
		startInfo: processenv.Info{
			ID: "started",
		},
		getInfo: processenv.Info{
			ID: "found",
		},
		readOutput: processenv.Output{
			Stdout: "output",
		},
		stopInfo: processenv.Info{
			ID: "stopped",
		},
		processes: []processenv.Info{
			{
				ID: "listed",
			},
		},
	}
	backend := New(guardrails.FilesystemPolicy{}, manager)
	plan := testLocalPlan(t, "printf", "hello")
	startSpec := getLocalProcessSpec(t, execution.ProcessOperation{
		Action:            execution.ProcessStart,
		Plan:              &plan,
		Label:             "worker",
		OutputBufferBytes: 2048,
	})

	info, err := backend.StartProcess(context.Background(), startSpec)
	require.NoError(t, err)
	require.Equal(t, "started", info.ID)
	require.Equal(t, "default", manager.startSession)
	require.Equal(t, "worker", manager.startRequest.Label)
	require.Equal(t, 2048, manager.startRequest.OutputBufferBytes)
	require.NotEmpty(t, manager.startRequest.Plan.Digest())

	_, err = backend.StartProcess(context.Background(), execution.Spec{})
	require.EqualError(t, err, "local process start specification is invalid")

	getSpec := getLocalProcessSpec(t, execution.ProcessOperation{
		Action:    execution.ProcessStatus,
		ProcessID: "process-1",
	})
	info, err = backend.GetProcess(context.Background(), getSpec)
	require.NoError(t, err)
	require.Equal(t, "found", info.ID)
	require.Equal(t, "process-1", manager.getID)

	stdoutCursor := 4
	request := processenv.ReadRequest{
		ProcessID:    "process-1",
		StdoutCursor: &stdoutCursor,
	}
	readSpec := getLocalProcessSpec(t, execution.ProcessOperation{
		Action:    execution.ProcessRead,
		ProcessID: "process-1",
	})
	output, err := backend.ReadProcess(context.Background(), readSpec, request)
	require.NoError(t, err)
	require.Equal(t, "output", output.Stdout)
	require.Equal(t, request, manager.readRequest)

	stopSpec := getLocalProcessSpec(t, execution.ProcessOperation{
		Action:    execution.ProcessStop,
		ProcessID: "process-1",
	})
	info, err = backend.StopProcess(context.Background(), stopSpec)
	require.NoError(t, err)
	require.Equal(t, "stopped", info.ID)
	require.Equal(t, "process-1", manager.stopIDs[0])

	listSpec := getLocalProcessSpec(t, execution.ProcessOperation{
		Action: execution.ProcessList,
	})
	listed, err := backend.ListProcesses(context.Background(), listSpec)
	require.NoError(t, err)
	require.Equal(t, "listed", listed[0].ID)
}

func TestBackend_RunCommands(t *testing.T) {
	backend := New(guardrails.FilesystemPolicy{}, &processManagerStub{})

	result, err := backend.Run(
		context.Background(),
		getLocalCommandSpec(t, testLocalPlan(t, "printf", "hello")),
	)
	require.NoError(t, err)
	require.Equal(t, 0, result.ExitCode)
	require.Equal(t, "hello", result.Stdout)
	require.GreaterOrEqual(t, result.Duration, time.Duration(0))

	result, err = backend.Run(
		context.Background(),
		getLocalCommandSpec(t, testLocalShellPlan(t, "printf failure >&2; exit 7")),
	)
	require.NoError(t, err)
	require.Equal(t, 7, result.ExitCode)
	require.Equal(t, "failure", result.Stderr)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err = backend.Run(
		ctx,
		getLocalCommandSpec(t, testLocalShellPlan(t, "sleep 5")),
	)
	require.NoError(t, err)
	require.True(t, result.Interrupted)
	require.Equal(t, -1, result.ExitCode)

	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	result, err = backend.Run(
		ctx,
		getLocalCommandSpec(t, testLocalShellPlan(t, "sleep 5")),
	)
	require.NoError(t, err)
	require.True(t, result.TimedOut)
	require.Equal(t, -1, result.ExitCode)

	_, err = backend.Run(context.Background(), execution.Spec{})
	require.EqualError(t, err, "local command specification is invalid")

	missing := testLocalPlan(t, "printf", "hello")
	missing.Invocations[0].ResolvedPath = filepath.Join(t.TempDir(), "missing")
	_, err = backend.Run(context.Background(), getLocalCommandSpec(t, missing))
	require.Error(t, err)

	invalid := testLocalPlan(t, "printf", "hello")
	invalid.Mode = "invalid"
	_, err = backend.Run(context.Background(), getLocalCommandSpec(t, invalid))
	require.EqualError(t, err, "command execution mode is invalid")

	originalWait := waitLocalCommand
	waitLocalCommand = func(command *exec.Cmd) error {
		_ = command.Wait()
		return errors.New("wait failed")
	}
	t.Cleanup(func() {
		waitLocalCommand = originalWait
	})
	_, err = backend.Run(
		context.Background(),
		getLocalCommandSpec(t, testLocalPlan(t, "printf", "hello")),
	)
	require.EqualError(t, err, "wait failed")

	terminateCommandProcess(nil)
	terminateCommandProcess(&exec.Cmd{})
}

func TestBackend_ReadWriteAndFileInfo(t *testing.T) {
	root := t.TempDir()
	backend := New(
		guardrails.FilesystemPolicy{
			Roots: []string{root},
		},
		&processManagerStub{},
	)
	path := filepath.Join(root, "nested", "file.txt")
	writeSpec := getFilesystemSpec(t, path, execution.FilesystemOperation{
		Action: execution.FilesystemWrite,
		Data:   []byte("hello"),
	})

	info, err := backend.WriteFile(context.Background(), writeSpec, true)
	require.NoError(t, err)
	require.True(t, info.Created)
	require.Equal(t, path, info.Path)
	require.EqualValues(t, 5, info.Size)

	info, err = backend.WriteFile(context.Background(), writeSpec, false)
	require.NoError(t, err)
	require.False(t, info.Created)

	readSpec := getFilesystemSpec(t, path, execution.FilesystemOperation{
		Action: execution.FilesystemRead,
	})
	content, err := backend.ReadFile(context.Background(), readSpec, 10)
	require.NoError(t, err)
	require.Equal(t, "hello", string(content))
	_, err = backend.ReadFile(context.Background(), readSpec, 2)
	require.Error(t, err)

	directoryInfo, err := getFileInfo(root, "root")
	require.NoError(t, err)
	require.True(t, directoryInfo.IsDir)
	_, err = getFileInfo(filepath.Join(root, "missing"), "missing")
	require.Error(t, err)
}

func TestBackend_PatchFiles(t *testing.T) {
	root := t.TempDir()
	backend := New(
		guardrails.FilesystemPolicy{
			Roots: []string{root},
		},
		&processManagerStub{},
	)
	existing := filepath.Join(root, "existing.txt")
	require.NoError(t, os.WriteFile(existing, []byte("old\n"), 0o600))
	patch := "--- a/existing.txt\n+++ b/existing.txt\n@@ -1 +1 @@\n-old\n+new\n"
	spec := getFilesystemSpec(t, existing, execution.FilesystemOperation{
		Action: execution.FilesystemPatch,
		Data:   []byte(patch),
	})

	info, err := backend.PatchFile(context.Background(), spec)
	require.NoError(t, err)
	require.False(t, info.Created)
	content, err := os.ReadFile(existing)
	require.NoError(t, err)
	require.Equal(t, "new\n", string(content))

	created := filepath.Join(root, "created.txt")
	createPatch := "--- /dev/null\n+++ b/created.txt\n@@ -0,0 +1 @@\n+created\n"
	createSpec := getFilesystemSpec(t, created, execution.FilesystemOperation{
		Action: execution.FilesystemPatch,
		Data:   []byte(createPatch),
	})
	info, err = backend.PatchFile(context.Background(), createSpec)
	require.NoError(t, err)
	require.True(t, info.Created)

	invalidSpec := getFilesystemSpec(t, existing, execution.FilesystemOperation{
		Action: execution.FilesystemPatch,
		Data:   []byte("not a patch"),
	})
	_, err = backend.PatchFile(context.Background(), invalidSpec)
	require.Error(t, err)

	countSpec := getFilesystemSpec(t, existing, execution.FilesystemOperation{
		Action: execution.FilesystemPatch,
		Data:   []byte(patch),
		Paths:  []execution.PreparedPath{},
	})
	operation := countSpec.Operation().Filesystem
	operation.Paths = []execution.PreparedPath{
		getLocalPreparedPath(t, existing, execution.FilesystemPatch),
		getLocalPreparedPath(t, existing, execution.FilesystemPatch),
	}
	countSpec = getLocalFilesystemSpec(t, *operation)
	_, err = backend.PatchFile(context.Background(), countSpec)
	require.EqualError(t, err, "prepared patch path count does not match patch files")

	conflictSpec := getFilesystemSpec(t, existing, execution.FilesystemOperation{
		Action: execution.FilesystemPatch,
		Data:   []byte(patch),
	})
	_, err = backend.PatchFile(context.Background(), conflictSpec)
	require.ErrorIs(t, err, execution.ErrPatchConflict)
}

func TestBackend_ListFiles(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "nested"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "nested", "deeper"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".hidden-dir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "visible.txt"), []byte("a"), 0o600))
	require.NoError(
		t,
		os.WriteFile(filepath.Join(root, "nested", "nested.txt"), []byte("b"), 0o600),
	)
	require.NoError(t, os.WriteFile(filepath.Join(root, ".hidden"), []byte("c"), 0o600))
	backend := New(guardrails.FilesystemPolicy{}, &processManagerStub{})

	nonRecursive := getFilesystemSpec(t, root, execution.FilesystemOperation{
		Action: execution.FilesystemList,
	})
	entries, err := backend.ListFiles(context.Background(), nonRecursive, 0)
	require.NoError(t, err)
	require.Equal(t, []string{"nested", "visible.txt"}, fileEntryPaths(entries))

	recursive := getFilesystemSpec(t, root, execution.FilesystemOperation{
		Action:        execution.FilesystemList,
		Recursive:     true,
		IncludeHidden: true,
	})
	entries, err = backend.ListFiles(context.Background(), recursive, 2)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	missing := getFilesystemSpec(
		t,
		filepath.Join(root, "missing"),
		execution.FilesystemOperation{
			Action: execution.FilesystemList,
		},
	)
	_, err = backend.ListFiles(context.Background(), missing, 10)
	require.Error(t, err)
}

func TestBackend_SearchFilesVariants(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "first.txt"), []byte("Needle\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "second.txt"), []byte("needle\n"), 0o600))
	backend := New(guardrails.FilesystemPolicy{}, &processManagerStub{})

	caseInsensitive := getFilesystemSpec(t, root, execution.FilesystemOperation{
		Action:    execution.FilesystemSearch,
		Query:     "needle",
		Recursive: true,
	})
	matches, err := backend.SearchFiles(context.Background(), caseInsensitive, 1)
	require.NoError(t, err)
	require.Len(t, matches, 1)

	singleFile := getFilesystemSpec(
		t,
		filepath.Join(root, "first.txt"),
		execution.FilesystemOperation{
			Action:        execution.FilesystemSearch,
			Query:         "Needle",
			CaseSensitive: true,
		},
	)
	matches, err = backend.SearchFiles(context.Background(), singleFile, 0)
	require.NoError(t, err)
	require.Len(t, matches, 1)
	require.Equal(t, "first.txt", matches[0].Path)

	invalidRegex := getFilesystemSpec(t, root, execution.FilesystemOperation{
		Action: execution.FilesystemSearch,
		Query:  "[",
	})
	_, err = backend.SearchFiles(context.Background(), invalidRegex, 0)
	require.Error(t, err)

	unmatched := getFilesystemSpec(
		t,
		filepath.Join(root, "first.txt"),
		execution.FilesystemOperation{
			Action: execution.FilesystemSearch,
			Query:  "absent",
		},
	)
	matches, err = backend.SearchFiles(context.Background(), unmatched, 0)
	require.NoError(t, err)
	require.Empty(t, matches)
}

func TestBackend_CloseLifecycle(t *testing.T) {
	running := processenv.Info{
		ID:     "running",
		Status: processenv.StatusRunning,
	}
	exited := processenv.Info{
		ID:     "exited",
		Status: processenv.StatusExited,
	}
	manager := &processManagerStub{
		processes: []processenv.Info{running, exited},
		stopErr:   errors.New("stop failed"),
	}
	backend := New(guardrails.FilesystemPolicy{}, manager)
	owner := getLocalOwner()
	backend.rememberOwner(owner)

	err := backend.CloseOwner(context.Background(), owner)
	require.EqualError(t, err, "stop failed")
	require.Equal(t, []string{"running"}, manager.stopIDs)
	require.Empty(t, backend.owners)

	manager.stopIDs = nil
	backend.rememberOwner(owner)
	other := owner
	other.Profile = "other"
	other.ActorID = "other"
	backend.rememberOwner(other)
	err = backend.CloseSession(context.Background(), "default", "default", false)
	require.EqualError(t, err, "stop failed")
	require.Len(t, backend.owners, 1)

	manager.stopErr = nil
	err = backend.Close(context.Background())
	require.NoError(t, err)
	require.Empty(t, backend.owners)
	require.NoError(t, backend.Reconcile(context.Background()))

	var nilBackend *Backend
	require.NoError(t, nilBackend.CloseOwner(context.Background(), owner))
	require.NoError(t, nilBackend.CloseSession(context.Background(), "default", "default", false))
}

func TestGetHostPath_ValidatesSpecification(t *testing.T) {
	_, err := getHostPath(execution.Spec{})
	require.EqualError(t, err, "filesystem execution specification is required")

	path := getLocalPreparedPath(t, "relative.txt", execution.FilesystemRead)
	spec := getLocalFilesystemSpec(t, execution.FilesystemOperation{
		Action: execution.FilesystemRead,
		Path:   path,
	})
	_, err = getHostPath(spec)
	require.EqualError(t, err, "local execution path must be absolute")

	absolute := filepath.Join(t.TempDir(), "file.txt")
	path, err = execution.NewPreparedPath(execution.PreparedPathInput{
		LogicalPath:        absolute,
		ContainerPath:      absolute,
		Mode:               execution.MountReadWrite,
		Action:             execution.FilesystemRead,
		SecurityGeneration: "generation",
	})
	require.NoError(t, err)
	spec = getLocalFilesystemSpec(t, execution.FilesystemOperation{
		Action: execution.FilesystemRead,
		Path:   path,
	})
	hostPath, err := getHostPath(spec)
	require.NoError(t, err)
	require.Equal(t, absolute, hostPath)
}

func TestBackend_FilesystemFailures(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file.txt")
	require.NoError(t, os.WriteFile(path, []byte("old\n"), 0o600))
	backend := New(guardrails.FilesystemPolicy{}, &processManagerStub{})

	t.Run("invalid specifications", func(t *testing.T) {
		_, err := backend.ReadFile(context.Background(), execution.Spec{}, 10)
		require.EqualError(t, err, "filesystem execution specification is required")
		_, err = backend.WriteFile(context.Background(), execution.Spec{}, false)
		require.EqualError(t, err, "filesystem execution specification is required")
		_, err = backend.ListFiles(context.Background(), execution.Spec{}, 10)
		require.EqualError(t, err, "filesystem execution specification is required")
		_, err = backend.SearchFiles(context.Background(), execution.Spec{}, 10)
		require.EqualError(t, err, "filesystem execution specification is required")
	})

	t.Run("write mkdir", func(t *testing.T) {
		restoreLocalFilesystemDependencies(t)
		makeLocalDirs = func(string, os.FileMode) error {
			return errors.New("mkdir failed")
		}
		spec := getFilesystemSpec(t, path, execution.FilesystemOperation{
			Action: execution.FilesystemWrite,
			Data:   []byte("data"),
		})
		_, err := backend.WriteFile(context.Background(), spec, true)
		require.EqualError(t, err, "mkdir failed")
	})

	t.Run("write file", func(t *testing.T) {
		restoreLocalFilesystemDependencies(t)
		writeLocalFile = func(string, []byte, os.FileMode) error {
			return errors.New("write failed")
		}
		spec := getFilesystemSpec(t, path, execution.FilesystemOperation{
			Action: execution.FilesystemWrite,
			Data:   []byte("data"),
		})
		_, err := backend.WriteFile(context.Background(), spec, false)
		require.EqualError(t, err, "write failed")
	})

	t.Run("write info", func(t *testing.T) {
		restoreLocalFilesystemDependencies(t)
		getLocalFileInfo = func(string, string) (execution.FileInfo, error) {
			return execution.FileInfo{}, errors.New("info failed")
		}
		spec := getFilesystemSpec(t, path, execution.FilesystemOperation{
			Action: execution.FilesystemWrite,
			Data:   []byte("data"),
		})
		_, err := backend.WriteFile(context.Background(), spec, false)
		require.EqualError(t, err, "info failed")
	})

	t.Run("patch parse", func(t *testing.T) {
		restoreLocalFilesystemDependencies(t)
		parseLocalPatch = func(io.Reader) ([]*gitdiff.File, string, error) {
			return nil, "", errors.New("parse failed")
		}
		spec := getFilesystemSpec(t, path, execution.FilesystemOperation{
			Action: execution.FilesystemPatch,
			Data:   []byte("patch"),
		})
		_, err := backend.PatchFile(context.Background(), spec)
		require.EqualError(t, err, "parse failed")
	})

	t.Run("patch read", func(t *testing.T) {
		restoreLocalFilesystemDependencies(t)
		readLocalTextFile = func(string, int64) ([]byte, error) {
			return nil, errors.New("read failed")
		}
		spec := getFilesystemSpec(t, path, execution.FilesystemOperation{
			Action: execution.FilesystemPatch,
			Data: []byte(
				"--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new\n",
			),
		})
		_, err := backend.PatchFile(context.Background(), spec)
		require.EqualError(t, err, "read failed")
	})

	t.Run("patch apply", func(t *testing.T) {
		restoreLocalFilesystemDependencies(t)
		applyLocalPatch = func(io.Writer, io.ReaderAt, *gitdiff.File) error {
			return errors.New("apply failed")
		}
		spec := getFilesystemSpec(t, path, execution.FilesystemOperation{
			Action: execution.FilesystemPatch,
			Data: []byte(
				"--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new\n",
			),
		})
		_, err := backend.PatchFile(context.Background(), spec)
		require.EqualError(t, err, "apply failed")
	})

	for _, test := range []struct {
		name string
		set  func()
	}{
		{
			name: "patch mkdir",
			set: func() {
				makeLocalDirs = func(string, os.FileMode) error {
					return errors.New("mkdir failed")
				}
			},
		},
		{
			name: "patch write",
			set: func() {
				writeLocalFile = func(string, []byte, os.FileMode) error {
					return errors.New("write failed")
				}
			},
		},
		{
			name: "patch info",
			set: func() {
				getLocalFileInfo = func(string, string) (execution.FileInfo, error) {
					return execution.FileInfo{}, errors.New("info failed")
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			restoreLocalFilesystemDependencies(t)
			test.set()
			target := filepath.Join(t.TempDir(), "new.txt")
			spec := getFilesystemSpec(t, target, execution.FilesystemOperation{
				Action: execution.FilesystemPatch,
				Data: []byte(
					"--- /dev/null\n+++ b/new.txt\n@@ -0,0 +1 @@\n+new\n",
				),
			})
			_, err := backend.PatchFile(context.Background(), spec)
			require.EqualError(t, err, test.name[6:]+" failed")
		})
	}

	t.Run("list walk and info", func(t *testing.T) {
		restoreLocalFilesystemDependencies(t)
		walkLocalDir = func(
			root string,
			visit fs.WalkDirFunc,
		) error {
			require.Equal(t, path, root)
			require.EqualError(t, visit(root, nil, errors.New("walk failed")), "walk failed")
			return visit(
				filepath.Join(root, "entry"),
				dirEntryStub{
					infoErr: errors.New("info failed"),
				},
				nil,
			)
		}
		spec := getFilesystemSpec(t, path, execution.FilesystemOperation{
			Action: execution.FilesystemList,
		})
		_, err := backend.ListFiles(context.Background(), spec, 10)
		require.EqualError(t, err, "info failed")
	})

	t.Run("search walk", func(t *testing.T) {
		restoreLocalFilesystemDependencies(t)
		walkLocalDir = func(
			root string,
			visit fs.WalkDirFunc,
		) error {
			return visit(root, nil, errors.New("walk failed"))
		}
		spec := getFilesystemSpec(t, path, execution.FilesystemOperation{
			Action: execution.FilesystemSearch,
			Query:  "value",
		})
		_, err := backend.SearchFiles(context.Background(), spec, 10)
		require.EqualError(t, err, "walk failed")
	})

	t.Run("search relative", func(t *testing.T) {
		restoreLocalFilesystemDependencies(t)
		getLocalRelative = func(string, string) (string, error) {
			return "", errors.New("relative failed")
		}
		spec := getFilesystemSpec(t, path, execution.FilesystemOperation{
			Action: execution.FilesystemSearch,
			Query:  "value",
		})
		_, err := backend.SearchFiles(context.Background(), spec, 10)
		require.EqualError(t, err, "relative failed")
	})

	t.Run("search open", func(t *testing.T) {
		restoreLocalFilesystemDependencies(t)
		openLocalFile = func(string) (*os.File, error) {
			return nil, errors.New("open failed")
		}
		spec := getFilesystemSpec(t, path, execution.FilesystemOperation{
			Action: execution.FilesystemSearch,
			Query:  "value",
		})
		matches, err := backend.SearchFiles(context.Background(), spec, 10)
		require.NoError(t, err)
		require.Empty(t, matches)
	})
}

func TestBackend_SearchFilesSkipsAndReportsScannerFailures(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "nested", "deeper"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".hidden-dir"), 0o755))
	require.NoError(
		t,
		os.WriteFile(filepath.Join(root, "unmatched.txt"), []byte("nothing\n"), 0o600),
	)
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(root, "too-long.txt"),
			[]byte(strings.Repeat("x", (4<<20)+1)),
			0o600,
		),
	)
	backend := New(guardrails.FilesystemPolicy{}, &processManagerStub{})
	nonRecursive := getFilesystemSpec(t, root, execution.FilesystemOperation{
		Action: execution.FilesystemSearch,
		Query:  "missing",
	})
	_, err := backend.SearchFiles(context.Background(), nonRecursive, 0)
	require.Error(t, err)
}

func getLocalOwner() execution.Owner {
	return execution.Owner{
		Profile:            "default",
		ActorKind:          "local_owner",
		ActorID:            "actor",
		Surface:            "cli",
		PublicSessionID:    "default",
		EffectiveSessionID: "default",
	}
}

func getLocalExposure(t *testing.T) execution.Exposure {
	t.Helper()
	exposure, err := execution.NewExposure(execution.ExposureInput{
		Backend:               execution.BackendLocal,
		Scope:                 execution.ScopeSession,
		WorkspaceIdentity:     "default:session:default",
		WorkspaceMode:         execution.WorkspaceReadWrite,
		Network:               execution.NetworkBridge,
		SecurityGeneration:    "generation",
		EnvironmentIdleExpiry: time.Minute,
		Limits: execution.Limits{
			Runtime:   time.Minute,
			StopGrace: time.Second,
		},
	})
	require.NoError(t, err)
	return exposure
}

func getLocalCommandSpec(t *testing.T, plan commandplan.Plan) execution.Spec {
	t.Helper()
	spec, err := execution.NewSpec(
		getLocalOwner(),
		getLocalExposure(t),
		execution.Operation{
			Kind:    execution.OperationCommand,
			Command: &plan,
		},
	)
	require.NoError(t, err)
	return spec
}

func getLocalProcessSpec(t *testing.T, operation execution.ProcessOperation) execution.Spec {
	t.Helper()
	spec, err := execution.NewSpec(
		getLocalOwner(),
		getLocalExposure(t),
		execution.Operation{
			Kind:    execution.OperationProcess,
			Process: &operation,
		},
	)
	require.NoError(t, err)
	return spec
}

func getLocalFilesystemSpec(
	t *testing.T,
	operation execution.FilesystemOperation,
) execution.Spec {
	t.Helper()
	spec, err := execution.NewSpec(
		getLocalOwner(),
		getLocalExposure(t),
		execution.Operation{
			Kind:       execution.OperationFilesystem,
			Filesystem: &operation,
		},
	)
	require.NoError(t, err)
	return spec
}

func getLocalPreparedPath(
	t *testing.T,
	path string,
	action execution.FilesystemAction,
) execution.PreparedPath {
	t.Helper()
	prepared, err := execution.NewPreparedPath(execution.PreparedPathInput{
		LogicalPath:        path,
		HostSourceIdentity: path,
		ContainerPath:      filepath.ToSlash(filepath.Join("/workspace", filepath.Base(path))),
		Mode:               execution.MountReadWrite,
		Action:             action,
		SecurityGeneration: "generation",
	})
	require.NoError(t, err)
	return prepared
}

func testLocalPlan(t *testing.T, command string, arguments ...string) commandplan.Plan {
	t.Helper()
	resolved, err := filepath.Abs(filepath.Join("/usr/bin", command))
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		resolved, err = os.Executable()
		require.NoError(t, err)
		if command == "printf" {
			resolved = "/usr/bin/printf"
		}
	}
	plan, err := commandplan.Analyze(context.Background(), commandplan.Request{
		Mode:             commandplan.ModeDirect,
		Command:          command,
		Args:             arguments,
		CWD:              t.TempDir(),
		Environment:      map[string]string{"PATH": "/usr/bin:/bin"},
		CleanEnvironment: true,
		LookPath: func(string) (string, error) {
			return resolved, nil
		},
	})
	require.NoError(t, err)
	return plan
}

func testLocalShellPlan(t *testing.T, source string) commandplan.Plan {
	t.Helper()
	plan, err := commandplan.Analyze(context.Background(), commandplan.Request{
		Mode:             commandplan.ModePOSIXShell,
		Command:          source,
		CWD:              t.TempDir(),
		Environment:      map[string]string{"PATH": "/usr/bin:/bin"},
		CleanEnvironment: true,
		ShellPath:        "/bin/sh",
		LookPath: func(name string) (string, error) {
			return filepath.Join("/usr/bin", name), nil
		},
	})
	require.NoError(t, err)
	return plan
}

func fileEntryPaths(entries []execution.FileEntry) []string {
	paths := make([]string, len(entries))
	for index, entry := range entries {
		paths[index] = entry.Path
	}
	return paths
}

type dirEntryStub struct {
	infoErr error
}

func (dirEntryStub) Name() string {
	return "entry"
}

func (dirEntryStub) IsDir() bool {
	return false
}

func (dirEntryStub) Type() os.FileMode {
	return 0
}

func (entry dirEntryStub) Info() (os.FileInfo, error) {
	return nil, entry.infoErr
}

func restoreLocalFilesystemDependencies(t *testing.T) {
	t.Helper()
	originalRead := readLocalTextFile
	originalStat := statLocalPath
	originalMkdir := makeLocalDirs
	originalWrite := writeLocalFile
	originalParse := parseLocalPatch
	originalApply := applyLocalPatch
	originalWalk := walkLocalDir
	originalRelative := getLocalRelative
	originalOpen := openLocalFile
	originalInfo := getLocalFileInfo
	t.Cleanup(func() {
		readLocalTextFile = originalRead
		statLocalPath = originalStat
		makeLocalDirs = originalMkdir
		writeLocalFile = originalWrite
		parseLocalPatch = originalParse
		applyLocalPatch = originalApply
		walkLocalDir = originalWalk
		getLocalRelative = originalRelative
		openLocalFile = originalOpen
		getLocalFileInfo = originalInfo
	})
}
