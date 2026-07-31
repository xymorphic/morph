package guardrails

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"github.com/wandxy/morph/internal/permissions"
	"github.com/wandxy/morph/pkg/nanoid"
	"github.com/wandxy/morph/pkg/str"
)

const (
	unsafeEvidenceIDPrefix      = "unsafe_"
	unsafeEvidenceLockFilename  = ".unsafe-evidence.lock"
	unsafeEvidenceLockRetryWait = 10 * time.Millisecond
	unsafeEvidenceRecordTimeout = 250 * time.Millisecond
)

type UnsafeEvidence struct {
	ID         string              `json:"id"`
	CapturedAt time.Time           `json:"captured_at"`
	SessionID  string              `json:"session_id,omitempty"`
	RunID      string              `json:"run_id,omitempty"`
	Source     string              `json:"source"`
	Action     string              `json:"action"`
	Blocked    bool                `json:"blocked,omitempty"`
	Redacted   bool                `json:"redacted,omitempty"`
	Findings   []map[string]string `json:"findings,omitempty"`
	Original   any                 `json:"original"`
	Safe       any                 `json:"safe,omitempty"`
}

type UnsafeEvidenceRecorder interface {
	RecordUnsafeEvidence(context.Context, UnsafeEvidence) (UnsafeEvidence, error)
}

type UnsafeEvidenceStore interface {
	UnsafeEvidenceRecorder
	LoadUnsafeEvidence(context.Context) ([]UnsafeEvidence, error)
	LoadUnsafeEvidenceByID(context.Context, string) (UnsafeEvidence, error)
	PurgeUnsafeEvidence(context.Context) (int, error)
}

type FileUnsafeEvidenceStore struct {
	dir        string
	now        func() time.Time
	generateID func(string, ...int) (string, error)
}

func NewFileUnsafeEvidenceStore(dir string) *FileUnsafeEvidenceStore {
	return &FileUnsafeEvidenceStore{
		dir:        strings.TrimSpace(dir),
		now:        time.Now,
		generateID: nanoid.Generate,
	}
}

func (s *FileUnsafeEvidenceStore) RecordUnsafeEvidence(
	ctx context.Context,
	evidence UnsafeEvidence,
) (UnsafeEvidence, error) {
	if err := checkUnsafeEvidenceContext(ctx); err != nil {
		return UnsafeEvidence{}, err
	}
	if err := s.checkReady(); err != nil {
		return UnsafeEvidence{}, err
	}
	if evidence.ID != "" {
		return UnsafeEvidence{}, errors.New("unsafe evidence ID is assigned by the store")
	}
	id, err := s.generateID(unsafeEvidenceIDPrefix)
	if err != nil {
		return UnsafeEvidence{}, err
	}
	evidence.ID = id
	if err := validateUnsafeEvidenceID(evidence.ID); err != nil {
		return UnsafeEvidence{}, err
	}
	if evidence.CapturedAt.IsZero() {
		evidence.CapturedAt = s.now().UTC()
	} else {
		evidence.CapturedAt = evidence.CapturedAt.UTC()
	}
	evidence.SessionID = str.String(evidence.SessionID).Trim()
	evidence.RunID = str.String(evidence.RunID).Trim()
	evidence.Source = str.String(evidence.Source).Trim()
	evidence.Action = str.String(evidence.Action).Trim()
	if evidence.Source == "" {
		return UnsafeEvidence{}, errors.New("unsafe evidence source is required")
	}
	if evidence.Action == "" {
		return UnsafeEvidence{}, errors.New("unsafe evidence action is required")
	}

	if err := s.prepareDir(); err != nil {
		return UnsafeEvidence{}, err
	}
	release, err := s.acquireLock(ctx)
	if err != nil {
		return UnsafeEvidence{}, err
	}
	defer release()

	raw, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return UnsafeEvidence{}, fmt.Errorf("marshal unsafe evidence: %w", err)
	}
	raw = append(raw, '\n')

	path := filepath.Join(s.dir, evidence.ID+".json")
	if _, err := os.Lstat(path); err == nil {
		return UnsafeEvidence{}, fmt.Errorf("unsafe evidence %q already exists", evidence.ID)
	} else if !os.IsNotExist(err) {
		return UnsafeEvidence{}, fmt.Errorf("check unsafe evidence target: %w", err)
	}
	tempPath := path + ".tmp"
	file, err := os.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return UnsafeEvidence{}, fmt.Errorf("create unsafe evidence: %w", err)
	}
	cleanup := true
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
		if cleanup {
			_ = os.Remove(tempPath)
		}
	}()
	if _, err := file.Write(raw); err != nil {
		return UnsafeEvidence{}, fmt.Errorf("write unsafe evidence: %w", err)
	}
	if err := file.Sync(); err != nil {
		return UnsafeEvidence{}, fmt.Errorf("sync unsafe evidence: %w", err)
	}
	if err := file.Close(); err != nil {
		return UnsafeEvidence{}, fmt.Errorf("close unsafe evidence: %w", err)
	}
	closed = true
	if err := os.Rename(tempPath, path); err != nil {
		return UnsafeEvidence{}, fmt.Errorf("store unsafe evidence: %w", err)
	}
	cleanup = false
	if err := syncUnsafeEvidenceDir(s.dir); err != nil {
		return UnsafeEvidence{}, err
	}
	return evidence, nil
}

func (s *FileUnsafeEvidenceStore) LoadUnsafeEvidence(ctx context.Context) ([]UnsafeEvidence, error) {
	if err := checkUnsafeEvidenceContext(ctx); err != nil {
		return nil, err
	}
	if err := s.checkReady(); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(s.dir); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("inspect unsafe evidence directory: %w", err)
	}
	if err := s.prepareDir(); err != nil {
		return nil, err
	}
	release, err := s.acquireLock(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("list unsafe evidence: %w", err)
	}

	evidence := make([]UnsafeEvidence, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if validateUnsafeEvidenceID(id) != nil {
			continue
		}
		item, err := s.load(id)
		if err != nil {
			log.Warn().Err(err).Str("file", entry.Name()).Msg("Skipping malformed unsafe evidence record")
			continue
		}
		evidence = append(evidence, item)
	}
	sort.Slice(evidence, func(i, j int) bool {
		if evidence[i].CapturedAt.Equal(evidence[j].CapturedAt) {
			return evidence[i].ID < evidence[j].ID
		}
		return evidence[i].CapturedAt.Before(evidence[j].CapturedAt)
	})
	return evidence, nil
}

func (s *FileUnsafeEvidenceStore) LoadUnsafeEvidenceByID(
	ctx context.Context,
	id string,
) (UnsafeEvidence, error) {
	if err := checkUnsafeEvidenceContext(ctx); err != nil {
		return UnsafeEvidence{}, err
	}
	if err := s.checkReady(); err != nil {
		return UnsafeEvidence{}, err
	}
	if err := validateUnsafeEvidenceID(id); err != nil {
		return UnsafeEvidence{}, err
	}
	if _, err := os.Lstat(s.dir); os.IsNotExist(err) {
		return UnsafeEvidence{}, fmt.Errorf("unsafe evidence %q not found", strings.TrimSpace(id))
	} else if err != nil {
		return UnsafeEvidence{}, fmt.Errorf("inspect unsafe evidence directory: %w", err)
	}
	if err := s.prepareDir(); err != nil {
		return UnsafeEvidence{}, err
	}
	release, err := s.acquireLock(ctx)
	if err != nil {
		return UnsafeEvidence{}, err
	}
	defer release()

	return s.load(strings.TrimSpace(id))
}

func (s *FileUnsafeEvidenceStore) PurgeUnsafeEvidence(ctx context.Context) (int, error) {
	if err := checkUnsafeEvidenceContext(ctx); err != nil {
		return 0, err
	}
	if err := s.checkReady(); err != nil {
		return 0, err
	}
	if _, err := os.Lstat(s.dir); os.IsNotExist(err) {
		return 0, nil
	} else if err != nil {
		return 0, fmt.Errorf("inspect unsafe evidence directory: %w", err)
	}
	if err := s.prepareDir(); err != nil {
		return 0, err
	}
	release, err := s.acquireLock(ctx)
	if err != nil {
		return 0, err
	}
	defer release()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return 0, fmt.Errorf("list unsafe evidence: %w", err)
	}

	removed := 0
	removedAny := false
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !isUnsafeEvidenceArtifact(entry.Name()) {
			continue
		}
		if err := os.Remove(filepath.Join(s.dir, entry.Name())); err != nil {
			return removed, fmt.Errorf("remove unsafe evidence %q: %w", entry.Name(), err)
		}
		removedAny = true
		if strings.HasSuffix(entry.Name(), ".json") {
			removed++
		}
	}
	if removedAny {
		if err := syncUnsafeEvidenceDir(s.dir); err != nil {
			return removed, err
		}
	}
	return removed, nil
}

func (s *FileUnsafeEvidenceStore) load(id string) (UnsafeEvidence, error) {
	path := filepath.Join(s.dir, id+".json")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return UnsafeEvidence{}, fmt.Errorf("unsafe evidence %q not found", id)
	}
	if err != nil {
		return UnsafeEvidence{}, fmt.Errorf("inspect unsafe evidence %q: %w", id, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return UnsafeEvidence{}, fmt.Errorf("unsafe evidence %q is not a regular file", id)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return UnsafeEvidence{}, fmt.Errorf("read unsafe evidence %q: %w", id, err)
	}
	var evidence UnsafeEvidence
	if err := json.Unmarshal(raw, &evidence); err != nil {
		return UnsafeEvidence{}, fmt.Errorf("decode unsafe evidence %q: %w", id, err)
	}
	if evidence.ID != id {
		return UnsafeEvidence{}, fmt.Errorf("unsafe evidence %q has mismatched ID", id)
	}
	return evidence, nil
}

func (s *FileUnsafeEvidenceStore) checkReady() error {
	if s == nil || s.dir == "" {
		return errors.New("unsafe evidence directory is required")
	}
	return nil
}

func (s *FileUnsafeEvidenceStore) prepareDir() error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create unsafe evidence directory: %w", err)
	}
	info, err := os.Lstat(s.dir)
	if err != nil {
		return fmt.Errorf("inspect unsafe evidence directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("unsafe evidence directory must be a regular directory")
	}
	if err := os.Chmod(s.dir, 0o700); err != nil {
		return fmt.Errorf("secure unsafe evidence directory: %w", err)
	}
	return nil
}

func (s *FileUnsafeEvidenceStore) acquireLock(ctx context.Context) (func(), error) {
	lockPath := filepath.Join(s.dir, unsafeEvidenceLockFilename)
	lock := flock.New(lockPath)
	locked, err := lock.TryLockContext(ctx, unsafeEvidenceLockRetryWait)
	if err != nil {
		return nil, fmt.Errorf("acquire unsafe evidence lock: %w", err)
	}
	if !locked {
		return nil, errors.New("acquire unsafe evidence lock: context canceled")
	}
	if err := os.Chmod(lockPath, 0o600); err != nil {
		_ = lock.Unlock()
		return nil, fmt.Errorf("secure unsafe evidence lock: %w", err)
	}
	return func() {
		if err := lock.Unlock(); err != nil {
			log.Warn().Err(err).Msg("Failed to release unsafe evidence lock")
		}
	}, nil
}

func isUnsafeEvidenceArtifact(name string) bool {
	id := strings.TrimSuffix(name, ".json")
	if id != name && validateUnsafeEvidenceID(id) == nil {
		return true
	}
	id = strings.TrimSuffix(name, ".json.tmp")
	return id != name && validateUnsafeEvidenceID(id) == nil
}

func syncUnsafeEvidenceDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	file, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open unsafe evidence directory: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync unsafe evidence directory: %w", err)
	}
	return nil
}

func validateUnsafeEvidenceID(id string) error {
	id = strings.TrimSpace(id)
	if err := nanoid.ValidateID(id); err != nil || !strings.HasPrefix(id, unsafeEvidenceIDPrefix) {
		return errors.New("unsafe evidence ID is invalid")
	}
	return nil
}

func checkUnsafeEvidenceContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context is required")
	}
	return ctx.Err()
}

type unsafeEvidenceRecorderContextKey struct{}

func WithUnsafeEvidenceRecorder(
	ctx context.Context,
	recorder UnsafeEvidenceRecorder,
) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if recorder == nil {
		return ctx
	}
	return context.WithValue(ctx, unsafeEvidenceRecorderContextKey{}, recorder)
}

func UnsafeEvidenceRecorderFromContext(ctx context.Context) UnsafeEvidenceRecorder {
	if ctx == nil {
		return nil
	}
	recorder, _ := ctx.Value(unsafeEvidenceRecorderContextKey{}).(UnsafeEvidenceRecorder)
	return recorder
}

func RetainUnsafeEvidence(
	ctx context.Context,
	recorder UnsafeEvidenceRecorder,
	evidence UnsafeEvidence,
) {
	if recorder == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if authorization, ok := permissions.FromContext(ctx); ok {
		if evidence.SessionID == "" {
			evidence.SessionID = authorization.SessionID
		}
		if evidence.RunID == "" {
			evidence.RunID = authorization.RunID
		}
	}
	recordCtx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		unsafeEvidenceRecordTimeout,
	)
	defer cancel()
	if _, err := recorder.RecordUnsafeEvidence(recordCtx, evidence); err != nil {
		log.Warn().
			Err(err).
			Str("source", evidence.Source).
			Str("action", evidence.Action).
			Msg("Failed to retain unsafe evidence")
	}
}
