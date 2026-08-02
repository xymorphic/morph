package local

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
	processenv "github.com/xymorphic/morph/internal/environment/process"
	"github.com/xymorphic/morph/internal/execution"
	"github.com/xymorphic/morph/internal/guardrails"
)

type Backend struct {
	policy    guardrails.FilesystemPolicy
	processes processenv.Manager
	mu        sync.Mutex
	owners    map[string]execution.Owner
}

var (
	readLocalTextFile = guardrails.ReadTextFile
	statLocalPath     = os.Stat
	makeLocalDirs     = os.MkdirAll
	writeLocalFile    = os.WriteFile
	parseLocalPatch   = gitdiff.Parse
	applyLocalPatch   = gitdiff.Apply
	walkLocalDir      = filepath.WalkDir
	getLocalRelative  = filepath.Rel
	openLocalFile     = os.Open
	getLocalFileInfo  = getFileInfo
)

func New(policy guardrails.FilesystemPolicy, processes processenv.Manager) *Backend {
	if processes == nil {
		processes = &processenv.DefaultManager{}
	}
	return &Backend{
		policy:    policy,
		processes: processes,
		owners:    map[string]execution.Owner{},
	}
}

func (b *Backend) Acquire(
	_ context.Context,
	spec execution.Spec,
) (execution.EnvironmentStatus, error) {
	b.rememberOwner(spec.Owner())
	exposure := spec.Exposure()
	return execution.EnvironmentStatus{
		ID:                 spec.Owner().Fingerprint(),
		Backend:            execution.BackendLocal,
		Scope:              exposure.Scope(),
		State:              execution.EnvironmentReady,
		WorkspaceIdentity:  exposure.WorkspaceIdentity(),
		WorkspaceMode:      exposure.WorkspaceMode(),
		Network:            exposure.Network(),
		SecurityGeneration: exposure.SecurityGeneration(),
	}, nil
}

func (b *Backend) Status(
	_ context.Context,
	owner execution.Owner,
) ([]execution.EnvironmentStatus, error) {
	if b == nil {
		return nil, errors.New("local execution backend is required")
	}
	return []execution.EnvironmentStatus{{
		ID:      owner.Fingerprint(),
		Backend: execution.BackendLocal,
		Scope:   execution.ScopeSession,
		State:   execution.EnvironmentReady,
	}}, nil
}

func (b *Backend) StartProcess(ctx context.Context, spec execution.Spec) (processenv.Info, error) {
	operation := spec.Operation().Process
	if operation == nil || operation.Action != execution.ProcessStart || operation.Plan == nil {
		return processenv.Info{}, errors.New("local process start specification is invalid")
	}
	b.rememberOwner(spec.Owner())
	return b.processes.Start(ctx, spec.Owner().EffectiveSessionID, processenv.StartRequest{
		Plan:              operation.Plan.Clone(),
		Label:             operation.Label,
		OutputBufferBytes: operation.OutputBufferBytes,
	})
}

func (b *Backend) GetProcess(_ context.Context, spec execution.Spec) (processenv.Info, error) {
	return b.processes.Get(spec.Owner().EffectiveSessionID, spec.Operation().Process.ProcessID)
}

func (b *Backend) ReadProcess(
	_ context.Context,
	spec execution.Spec,
	req processenv.ReadRequest,
) (processenv.Output, error) {
	return b.processes.Read(spec.Owner().EffectiveSessionID, req)
}

func (b *Backend) StopProcess(ctx context.Context, spec execution.Spec) (processenv.Info, error) {
	return b.processes.Stop(
		ctx,
		spec.Owner().EffectiveSessionID,
		spec.Operation().Process.ProcessID,
	)
}

func (b *Backend) ListProcesses(_ context.Context, spec execution.Spec) ([]processenv.Info, error) {
	return b.processes.List(spec.Owner().EffectiveSessionID), nil
}

func (b *Backend) ReadFile(_ context.Context, spec execution.Spec, limit int) ([]byte, error) {
	path, err := getHostPath(spec)
	if err != nil {
		return nil, err
	}
	return readLocalTextFile(path, int64(limit))
}

func (b *Backend) WriteFile(
	_ context.Context,
	spec execution.Spec,
	createDirs bool,
) (execution.FileInfo, error) {
	operation := spec.Operation().Filesystem
	path, err := getHostPath(spec)
	if err != nil {
		return execution.FileInfo{}, err
	}
	_, statErr := statLocalPath(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if createDirs {
		if err := makeLocalDirs(filepath.Dir(path), 0o755); err != nil {
			return execution.FileInfo{}, err
		}
	}
	if err := writeLocalFile(path, operation.Data, 0o644); err != nil {
		return execution.FileInfo{}, err
	}
	info, err := getLocalFileInfo(path, operation.Path.LogicalPath())
	info.Created = created
	return info, err
}

func (b *Backend) PatchFile(_ context.Context, spec execution.Spec) (execution.FileInfo, error) {
	operation := spec.Operation().Filesystem
	files, _, err := parseLocalPatch(strings.NewReader(string(operation.Data)))
	if err != nil {
		return execution.FileInfo{}, err
	}
	paths := operation.Paths
	if len(paths) == 0 {
		paths = []execution.PreparedPath{operation.Path}
	}
	if len(files) != len(paths) {
		return execution.FileInfo{}, errors.New(
			"prepared patch path count does not match patch files",
		)
	}
	var last execution.FileInfo
	for index, file := range files {
		path := paths[index].HostSourceIdentity()
		var source []byte
		if !file.IsNew && file.OldName != "/dev/null" {
			source, err = readLocalTextFile(path, 4<<20)
			if err != nil {
				return execution.FileInfo{}, err
			}
		}
		var destination bytes.Buffer
		if err := applyLocalPatch(&destination, bytes.NewReader(source), file); err != nil {
			if errors.Is(err, &gitdiff.Conflict{}) {
				return execution.FileInfo{}, fmt.Errorf(
					"%w: hunk does not apply cleanly",
					execution.ErrPatchConflict,
				)
			}
			return execution.FileInfo{}, err
		}
		if err := makeLocalDirs(filepath.Dir(path), 0o755); err != nil {
			return execution.FileInfo{}, err
		}
		if err := writeLocalFile(path, destination.Bytes(), 0o644); err != nil {
			return execution.FileInfo{}, err
		}
		last, err = getLocalFileInfo(path, paths[index].LogicalPath())
		if err != nil {
			return execution.FileInfo{}, err
		}
		last.Created = file.IsNew || file.OldName == "/dev/null"
	}
	return last, nil
}

func (b *Backend) ListFiles(
	_ context.Context,
	spec execution.Spec,
	limit int,
) ([]execution.FileEntry, error) {
	operation := spec.Operation().Filesystem
	root, err := getHostPath(spec)
	if err != nil {
		return nil, err
	}
	entries := make([]execution.FileEntry, 0)
	err = walkLocalDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, _ := getLocalRelative(root, path)
		if !operation.IncludeHidden && isHiddenPath(relative) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !operation.Recursive && filepath.Dir(relative) != "." {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		entries = append(
			entries,
			execution.FileEntry{
				Path:  filepath.ToSlash(relative),
				Size:  info.Size(),
				IsDir: entry.IsDir(),
			},
		)
		if limit > 0 && len(entries) >= limit {
			return filepath.SkipAll
		}
		return nil
	})
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, err
}

func (b *Backend) SearchFiles(
	_ context.Context,
	spec execution.Spec,
	limit int,
) ([]execution.SearchMatch, error) {
	operation := spec.Operation().Filesystem
	root, err := getHostPath(spec)
	if err != nil {
		return nil, err
	}
	matches := make([]execution.SearchMatch, 0)
	displayRoot := root
	if info, statErr := statLocalPath(root); statErr == nil && !info.IsDir() {
		displayRoot = filepath.Dir(root)
	}
	pattern := operation.Query
	if !operation.CaseSensitive {
		pattern = "(?i)" + pattern
	}
	expression, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	err = walkLocalDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, relErr := getLocalRelative(displayRoot, path)
		if relErr != nil {
			return relErr
		}
		if !operation.IncludeHidden && isHiddenPath(relative) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if path != root && !operation.Recursive {
				return filepath.SkipDir
			}
			return nil
		}
		file, openErr := openLocalFile(path)
		if openErr != nil {
			return nil
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64*1024), 4<<20)
		lineNumber := 0
		for scanner.Scan() {
			lineNumber++
			location := expression.FindStringIndex(scanner.Text())
			if location == nil {
				continue
			}
			matches = append(
				matches,
				execution.SearchMatch{
					Path:   filepath.ToSlash(relative),
					Line:   lineNumber,
					Column: location[0] + 1,
					Text:   scanner.Text(),
				},
			)
			if limit > 0 && len(matches) >= limit {
				return filepath.SkipAll
			}
		}
		scanErr := scanner.Err()
		closeErr := file.Close()
		return errors.Join(scanErr, closeErr)
	})
	return matches, err
}

func isHiddenPath(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if strings.HasPrefix(part, ".") && part != "." && part != ".." {
			return true
		}
	}
	return false
}

func (b *Backend) CloseOwner(ctx context.Context, owner execution.Owner) error {
	if b == nil || b.processes == nil {
		return nil
	}
	var joined error
	for _, process := range b.processes.List(owner.EffectiveSessionID) {
		if process.Status == processenv.StatusRunning {
			_, err := b.processes.Stop(ctx, owner.EffectiveSessionID, process.ID)
			joined = errors.Join(joined, err)
		}
	}
	b.mu.Lock()
	delete(b.owners, owner.Fingerprint())
	b.mu.Unlock()
	return joined
}

func (b *Backend) CloseSession(
	ctx context.Context,
	profile string,
	sessionID string,
	_ bool,
) error {
	if b == nil || b.processes == nil {
		return nil
	}
	var joined error
	for _, process := range b.processes.List(sessionID) {
		if process.Status == processenv.StatusRunning {
			_, err := b.processes.Stop(ctx, sessionID, process.ID)
			joined = errors.Join(joined, err)
		}
	}
	b.mu.Lock()
	for key, owner := range b.owners {
		if owner.Profile == profile && owner.EffectiveSessionID == sessionID {
			delete(b.owners, key)
		}
	}
	b.mu.Unlock()
	return joined
}

func (b *Backend) Reconcile(context.Context) error { return nil }

func (b *Backend) Close(ctx context.Context) error {
	b.mu.Lock()
	owners := make([]execution.Owner, 0, len(b.owners))
	for _, owner := range b.owners {
		owners = append(owners, owner)
	}
	b.mu.Unlock()
	var joined error
	for _, owner := range owners {
		joined = errors.Join(joined, b.CloseOwner(ctx, owner))
	}
	return joined
}

func (b *Backend) rememberOwner(owner execution.Owner) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.owners[owner.Fingerprint()] = owner
	b.mu.Unlock()
}

func getHostPath(spec execution.Spec) (string, error) {
	operation := spec.Operation().Filesystem
	if operation == nil {
		return "", errors.New("filesystem execution specification is required")
	}
	path := operation.Path.HostSourceIdentity()
	if path == "" {
		path = operation.Path.LogicalPath()
	}
	if !filepath.IsAbs(path) {
		return "", errors.New("local execution path must be absolute")
	}
	return filepath.Clean(path), nil
}

func getFileInfo(path string, logical string) (execution.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return execution.FileInfo{}, err
	}
	return execution.FileInfo{
		Path:  logical,
		Size:  info.Size(),
		Mode:  uint32(info.Mode().Perm()),
		IsDir: info.IsDir(),
	}, nil
}
