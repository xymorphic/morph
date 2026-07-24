package process

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/wandxy/morph/internal/constants"
	"github.com/wandxy/morph/pkg/str"
)

// DefaultOutputBufferBytes is the package-level default output buffer bytes constant.
const DefaultOutputBufferBytes = constants.DefaultProcessOutputBufferBytes

// DefaultMaxTracked is the package-level default max tracked constant.
const DefaultMaxTracked = constants.DefaultProcessMaxTracked

// DefaultStopGracePeriod is the package-level default stop grace period constant.
const DefaultStopGracePeriod = constants.DefaultProcessStopGracePeriod

// DefaultManager manages default.
type DefaultManager struct {
	mu              sync.Mutex
	processes       map[string]map[string]*trackedProcess
	order           map[string][]string
	stale           map[string]map[string]struct{}
	nextID          map[string]uint64
	MaxTracked      int
	StopGracePeriod time.Duration
}

type trackedProcess struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	stdout  *recentBuffer
	stderr  *recentBuffer
	info    Info
	waitErr error
	done    chan struct{}
}

type recentBuffer struct {
	mu          sync.Mutex
	limit       int
	data        []byte
	windowStart int
	truncated   bool
	totalBytes  int
}

func (s *DefaultManager) Start(ctx context.Context, sessionID string, req StartRequest) (Info, error) {
	if s == nil {
		return Info{}, errors.New("process manager is required")
	}

	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	sessionID = normalizeProcessSessionID(sessionID)
	labelValue := str.String(req.Label)
	label := labelValue.Trim()

	cmd, err := req.Plan.NewCommand(context.Background())
	if err != nil {
		return Info{}, err
	}
	configureCommand(cmd)

	limit := req.OutputBufferBytes
	if limit <= 0 {
		limit = DefaultOutputBufferBytes
	}

	stdout := &recentBuffer{limit: limit}
	stderr := &recentBuffer{limit: limit}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return Info{}, err
	}

	startedAt := time.Now().UTC()

	s.mu.Lock()
	if s.processes == nil {
		s.processes = make(map[string]map[string]*trackedProcess)
	}
	if s.order == nil {
		s.order = make(map[string][]string)
	}
	if s.stale == nil {
		s.stale = make(map[string]map[string]struct{})
	}
	if s.processes[sessionID] == nil {
		s.processes[sessionID] = make(map[string]*trackedProcess)
	}
	if s.stale[sessionID] == nil {
		s.stale[sessionID] = make(map[string]struct{})
	}
	s.cleanupLocked(sessionID)
	if label != "" && s.hasProcessLabelLocked(sessionID, label) {
		s.mu.Unlock()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		return Info{}, errors.New("process label already exists")
	}
	if limit := s.maxTracked(); limit > 0 && len(s.processes[sessionID]) >= limit {
		s.mu.Unlock()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		return Info{}, errors.New("process manager is at capacity")
	}

	processID := s.nextProcessIDLocked(sessionID)
	process := &trackedProcess{
		cmd:    cmd,
		stdout: stdout,
		stderr: stderr,
		done:   make(chan struct{}),
		info: Info{
			ID:        processID,
			Label:     label,
			Command:   req.Plan.Summary(),
			CWD:       req.Plan.CWD,
			Status:    StatusRunning,
			StartedAt: startedAt,
		},
	}
	delete(s.stale[sessionID], processID)
	s.processes[sessionID][processID] = process
	s.order[sessionID] = append(s.order[sessionID], processID)
	s.mu.Unlock()

	go s.wait(process)

	return process.snapshot(), nil
}

func (s *DefaultManager) Get(sessionID string, processID string) (Info, error) {
	process, err := s.lookup(sessionID, processID)
	if err != nil {
		return Info{}, err
	}

	return process.snapshot(), nil
}

func (s *DefaultManager) Read(sessionID string, req ReadRequest) (Output, error) {
	process, err := s.lookup(sessionID, req.ProcessID)
	if err != nil {
		return Output{}, err
	}

	return process.output(req), nil
}

func (s *DefaultManager) Stop(ctx context.Context, sessionID string, processID string) (Info, error) {
	process, err := s.lookup(sessionID, processID)
	if err != nil {
		return Info{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	process.mu.Lock()
	cmd := process.cmd
	status := process.info.Status
	if cmd != nil && cmd.Process != nil && status == StatusRunning {
		process.info.Status = StatusStopped
	}
	process.mu.Unlock()

	if cmd == nil || cmd.Process == nil || status != StatusRunning {
		return process.snapshot(), nil
	}

	terminateCommandGracefully(cmd)
	select {
	case <-process.done:
		return process.snapshot(), nil
	case <-time.After(s.stopGracePeriod()):
	case <-ctx.Done():
		return process.snapshot(), ctx.Err()
	}

	terminateCommand(cmd)
	select {
	case <-process.done:
	case <-time.After(s.stopGracePeriod()):
	case <-ctx.Done():
		return process.snapshot(), ctx.Err()
	}

	return process.snapshot(), nil
}

func (s *DefaultManager) List(sessionID string) []Info {
	if s == nil {
		return nil
	}

	sessionID = normalizeProcessSessionID(sessionID)

	s.mu.Lock()
	order := append([]string(nil), s.order[sessionID]...)
	processes := make([]*trackedProcess, 0, len(order))
	for _, processID := range order {
		if process := s.processes[sessionID][processID]; process != nil {
			processes = append(processes, process)
		}
	}
	s.mu.Unlock()

	infos := make([]Info, 0, len(processes))
	for _, process := range processes {
		infos = append(infos, process.snapshot())
	}

	return infos
}

func (s *DefaultManager) wait(process *trackedProcess) {
	err := process.cmd.Wait()

	process.mu.Lock()
	process.waitErr = err
	endedAt := time.Now().UTC()
	process.info.EndedAt = &endedAt
	process.info.StdoutBytes = process.stdout.total()
	process.info.StderrBytes = process.stderr.total()
	process.info.StdoutTruncated = process.stdout.wasTruncated()
	process.info.StderrTruncated = process.stderr.wasTruncated()

	if err == nil {
		exitCode := 0
		process.info.ExitCode = &exitCode
		process.info.Status = StatusExited
		process.mu.Unlock()
		if process.done != nil {
			close(process.done)
		}
		return
	}

	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode := exitErr.ExitCode()
		process.info.ExitCode = &exitCode
		if process.info.Status == StatusStopped {
			process.mu.Unlock()
			if process.done != nil {
				close(process.done)
			}
			return
		}
		process.info.Status = StatusExited
		process.mu.Unlock()
		if process.done != nil {
			close(process.done)
		}
		return
	}

	process.info.Status = StatusFailed
	process.mu.Unlock()
	if process.done != nil {
		close(process.done)
	}
}

func (s *DefaultManager) lookup(sessionID string, processID string) (*trackedProcess, error) {
	if s == nil {
		return nil, errors.New("process manager is required")
	}

	sessionID = normalizeProcessSessionID(sessionID)
	processIDValue := str.String(processID)
	processID = processIDValue.Trim()
	if processID == "" {
		return nil, errors.New("process id is required")
	}

	s.mu.Lock()
	process := s.processes[sessionID][processID]
	_, stale := s.stale[sessionID][processID]
	if process == nil {
		process = s.lookupByLabelLocked(sessionID, processID)
	}
	if stale && process == nil {
		delete(s.stale[sessionID], processID)
	}
	s.mu.Unlock()

	if process == nil {
		if stale {
			return nil, errors.New("process is no longer retained")
		}

		return nil, errors.New("process not found")
	}

	return process, nil
}

func (s *DefaultManager) hasProcessLabelLocked(sessionID string, label string) bool {
	return s.lookupByLabelLocked(sessionID, label) != nil
}

func (s *DefaultManager) lookupByLabelLocked(sessionID string, label string) *trackedProcess {
	labelValue2 := str.String(label)
	label = labelValue2.Trim()
	if label == "" {
		return nil
	}

	for _, process := range s.processes[sessionID] {
		if process == nil {
			continue
		}

		process.mu.Lock()
		matches := process.info.Label == label
		process.mu.Unlock()
		if matches {
			return process
		}
	}

	return nil
}

func (s *DefaultManager) nextProcessIDLocked(sessionID string) string {
	if s.nextID == nil {
		s.nextID = make(map[string]uint64)
	}
	s.nextID[sessionID]++
	id := s.nextID[sessionID]
	return "proc_" + strconv.FormatUint(id, 10)
}

func (s *DefaultManager) cleanupLocked(sessionID string) {
	processes := s.processes[sessionID]
	if len(processes) == 0 {
		return
	}
	if s.stale == nil {
		s.stale = make(map[string]map[string]struct{})
	}

	order := s.order[sessionID][:0]
	for _, processID := range s.order[sessionID] {
		process := processes[processID]
		if process == nil {
			continue
		}
		if process.finished() {
			delete(processes, processID)
			if s.stale[sessionID] == nil {
				s.stale[sessionID] = make(map[string]struct{})
			}
			s.stale[sessionID][processID] = struct{}{}
			continue
		}
		order = append(order, processID)
	}
	s.order[sessionID] = order
}

func (s *DefaultManager) maxTracked() int {
	if s == nil || s.MaxTracked <= 0 {
		return DefaultMaxTracked
	}

	return s.MaxTracked
}

func (s *DefaultManager) stopGracePeriod() time.Duration {
	if s == nil || s.StopGracePeriod <= 0 {
		return DefaultStopGracePeriod
	}

	return s.StopGracePeriod
}

func (p *trackedProcess) snapshot() Info {
	p.mu.Lock()
	defer p.mu.Unlock()

	info := p.info
	info.Args = append([]string(nil), p.info.Args...)
	info.StdoutBytes = p.stdout.total()
	info.StderrBytes = p.stderr.total()
	info.StdoutTruncated = p.stdout.wasTruncated()
	info.StderrTruncated = p.stderr.wasTruncated()
	if p.info.EndedAt != nil {
		endedAt := *p.info.EndedAt
		info.EndedAt = &endedAt
	}
	if p.info.ExitCode != nil {
		exitCode := *p.info.ExitCode
		info.ExitCode = &exitCode
	}

	return info
}

func (p *trackedProcess) output(req ReadRequest) Output {
	stdout, stdoutCursor, stdoutExpired := p.stdout.readSince(req.StdoutCursor)
	stderr, stderrCursor, stderrExpired := p.stderr.readSince(req.StderrCursor)

	return Output{
		Stdout:              string(stdout),
		Stderr:              string(stderr),
		StdoutBytes:         p.stdout.total(),
		StderrBytes:         p.stderr.total(),
		NextStdoutCursor:    stdoutCursor,
		NextStderrCursor:    stderrCursor,
		StdoutTruncated:     p.stdout.wasTruncated(),
		StderrTruncated:     p.stderr.wasTruncated(),
		StdoutCursorExpired: stdoutExpired,
		StderrCursorExpired: stderrExpired,
	}
}

func (p *trackedProcess) finished() bool {
	if p == nil {
		return true
	}

	if p.done != nil {
		select {
		case <-p.done:
			return true
		default:
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	return p.info.Status != StatusRunning
}

func (b *recentBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.totalBytes += len(data)
	if b.limit <= 0 {
		b.data = append(b.data, data...)
		return len(data), nil
	}

	b.data = append(b.data, data...)
	if len(b.data) > b.limit {
		dropped := len(b.data) - b.limit
		b.truncated = true
		b.windowStart += dropped
		b.data = append([]byte(nil), b.data[dropped:]...)
	}

	return len(data), nil
}

// readSince reads the data since the given cursor.
// It returns the data, the total bytes, and whether the cursor is expired.
func (b *recentBuffer) readSince(cursor *int) ([]byte, int, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if cursor == nil {
		return trimToValidUTF8Window(b.data), b.totalBytes, false
	}

	start := max(*cursor, 0)
	if start < b.windowStart {
		return trimToValidUTF8Window(b.data), b.totalBytes, true
	}
	if start >= b.totalBytes {
		return nil, b.totalBytes, false
	}

	offset := max(start-b.windowStart, 0)
	if offset > len(b.data) {
		return nil, b.totalBytes, false
	}

	return trimToValidUTF8Window(b.data[offset:]), b.totalBytes, false
}

func (b *recentBuffer) string() string {
	data, _, _ := b.readSince(nil)
	return string(data)
}

func trimToValidUTF8Window(data []byte) []byte {
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r != utf8.RuneError || size > 1 {
			break
		}
		data = data[1:]
	}

	for len(data) > 0 && !utf8.Valid(data) {
		data = data[:len(data)-1]
	}

	return append([]byte(nil), data...)
}

func (b *recentBuffer) total() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.totalBytes
}

func (b *recentBuffer) wasTruncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.truncated
}

func normalizeProcessSessionID(sessionID string) string {
	sessionIDValue := str.String(sessionID)
	sessionID = sessionIDValue.Trim()
	if sessionID == "" {
		return "default"
	}
	return sessionID
}
