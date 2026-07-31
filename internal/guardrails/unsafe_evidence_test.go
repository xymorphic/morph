package guardrails

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/stretchr/testify/require"
)

func TestFileUnsafeEvidenceStore_RecordListGetAndPurge(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "unsafe-evidence")
	store := NewFileUnsafeEvidenceStore(dir)
	store.now = func() time.Time {
		return time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	}
	store.generateID = func(string, ...int) (string, error) {
		return "unsafe_ABCDEFGHIJKLMNOPQRSTU", nil
	}

	recorded, err := store.RecordUnsafeEvidence(context.Background(), UnsafeEvidence{
		SessionID: " session ",
		RunID:     " run ",
		Source:    " assistant ",
		Action:    " redacted ",
		Redacted:  true,
		Findings: []map[string]string{{
			"id": "credential",
		}},
		Original: "TOKEN=secret",
		Safe:     "TOKEN=***",
	})

	require.NoError(t, err)
	require.Equal(t, "unsafe_ABCDEFGHIJKLMNOPQRSTU", recorded.ID)
	require.Equal(t, "session", recorded.SessionID)
	require.Equal(t, "run", recorded.RunID)
	require.Equal(t, "assistant", recorded.Source)
	require.Equal(t, "redacted", recorded.Action)

	dirInfo, err := os.Stat(dir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())
	fileInfo, err := os.Stat(filepath.Join(dir, recorded.ID+".json"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fileInfo.Mode().Perm())

	list, err := store.LoadUnsafeEvidence(context.Background())
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, recorded.ID, list[0].ID)

	loaded, err := store.LoadUnsafeEvidenceByID(context.Background(), recorded.ID)
	require.NoError(t, err)
	require.Equal(t, "TOKEN=secret", loaded.Original)
	require.Equal(t, "TOKEN=***", loaded.Safe)

	count, err := store.PurgeUnsafeEvidence(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, count)

	list, err = store.LoadUnsafeEvidence(context.Background())
	require.NoError(t, err)
	require.Empty(t, list)
}

func TestFileUnsafeEvidenceStore_ListSortsAndIgnoresUnrelatedFiles(t *testing.T) {
	dir := t.TempDir()
	store := NewFileUnsafeEvidenceStore(dir)
	ids := []string{
		"unsafe_ABCDEFGHIJKLMNOPQRSTU",
		"unsafe_BCDEFGHIJKLMNOPQRSTUV",
	}
	index := 0
	store.generateID = func(string, ...int) (string, error) {
		id := ids[index]
		index++
		return id, nil
	}
	store.now = func() time.Time {
		return time.Date(2026, time.July, 30, 12, index, 0, 0, time.UTC)
	}

	_, err := store.RecordUnsafeEvidence(context.Background(), UnsafeEvidence{
		Source: "user", Action: "blocked", Original: "first",
	})
	require.NoError(t, err)
	_, err = store.RecordUnsafeEvidence(context.Background(), UnsafeEvidence{
		Source: "tool.web", Action: "blocked", Original: "second",
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.json"), []byte("{}"), 0o600))

	list, err := store.LoadUnsafeEvidence(context.Background())
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.Equal(t, ids, []string{list[0].ID, list[1].ID})

	count, err := store.PurgeUnsafeEvidence(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, count)
	_, err = os.Stat(filepath.Join(dir, "notes.json"))
	require.NoError(t, err)
}

func TestFileUnsafeEvidenceStore_RejectsInvalidInput(t *testing.T) {
	store := NewFileUnsafeEvidenceStore(t.TempDir())

	_, err := store.RecordUnsafeEvidence(context.Background(), UnsafeEvidence{Action: "blocked"})
	require.EqualError(t, err, "unsafe evidence source is required")

	_, err = store.RecordUnsafeEvidence(context.Background(), UnsafeEvidence{Source: "user"})
	require.EqualError(t, err, "unsafe evidence action is required")

	_, err = store.LoadUnsafeEvidenceByID(context.Background(), "../secret")
	require.EqualError(t, err, "unsafe evidence ID is invalid")

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = store.LoadUnsafeEvidence(cancelled)
	require.ErrorIs(t, err, context.Canceled)
}

func TestFileUnsafeEvidenceStore_RejectsCallerAssignedAndDuplicateIDs(t *testing.T) {
	store := NewFileUnsafeEvidenceStore(t.TempDir())
	store.generateID = func(string, ...int) (string, error) {
		return "unsafe_ABCDEFGHIJKLMNOPQRSTU", nil
	}

	_, err := store.RecordUnsafeEvidence(context.Background(), UnsafeEvidence{
		ID: "unsafe_BCDEFGHIJKLMNOPQRSTUV", Source: "user", Action: "blocked",
	})
	require.EqualError(t, err, "unsafe evidence ID is assigned by the store")

	_, err = store.RecordUnsafeEvidence(context.Background(), UnsafeEvidence{
		Source: "user", Action: "blocked", Original: "first",
	})
	require.NoError(t, err)
	_, err = store.RecordUnsafeEvidence(context.Background(), UnsafeEvidence{
		Source: "user", Action: "blocked", Original: "second",
	})
	require.EqualError(t, err, `unsafe evidence "unsafe_ABCDEFGHIJKLMNOPQRSTU" already exists`)

	loaded, err := store.LoadUnsafeEvidenceByID(
		context.Background(),
		"unsafe_ABCDEFGHIJKLMNOPQRSTU",
	)
	require.NoError(t, err)
	require.Equal(t, "first", loaded.Original)
}

func TestFileUnsafeEvidenceStore_ListSkipsMalformedAndSymlinkRecords(t *testing.T) {
	dir := t.TempDir()
	store := NewFileUnsafeEvidenceStore(dir)
	store.generateID = func(string, ...int) (string, error) {
		return "unsafe_ABCDEFGHIJKLMNOPQRSTU", nil
	}
	_, err := store.RecordUnsafeEvidence(context.Background(), UnsafeEvidence{
		Source: "user", Action: "blocked", Original: "valid",
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "unsafe_BCDEFGHIJKLMNOPQRSTUV.json"),
		[]byte("{"),
		0o600,
	))
	require.NoError(t, os.Symlink(
		filepath.Join(dir, "unsafe_ABCDEFGHIJKLMNOPQRSTU.json"),
		filepath.Join(dir, "unsafe_CDEFGHIJKLMNOPQRSTUVW.json"),
	))

	list, err := store.LoadUnsafeEvidence(context.Background())
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "valid", list[0].Original)

	_, err = store.LoadUnsafeEvidenceByID(
		context.Background(),
		"unsafe_CDEFGHIJKLMNOPQRSTUVW",
	)
	require.EqualError(
		t,
		err,
		`unsafe evidence "unsafe_CDEFGHIJKLMNOPQRSTUVW" is not a regular file`,
	)
}

func TestFileUnsafeEvidenceStore_PurgeRemovesCrashArtifacts(t *testing.T) {
	dir := t.TempDir()
	store := NewFileUnsafeEvidenceStore(dir)
	tempPath := filepath.Join(dir, "unsafe_ABCDEFGHIJKLMNOPQRSTU.json.tmp")
	require.NoError(t, os.WriteFile(tempPath, []byte("partial"), 0o600))

	count, err := store.PurgeUnsafeEvidence(context.Background())
	require.NoError(t, err)
	require.Zero(t, count)
	_, err = os.Stat(tempPath)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestFileUnsafeEvidenceStore_RejectsSymlinkDirectory(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	require.NoError(t, os.Mkdir(target, 0o700))
	dir := filepath.Join(root, "unsafe-evidence")
	require.NoError(t, os.Symlink(target, dir))
	store := NewFileUnsafeEvidenceStore(dir)

	_, err := store.RecordUnsafeEvidence(context.Background(), UnsafeEvidence{
		Source: "user", Action: "blocked", Original: "unsafe",
	})
	require.EqualError(t, err, "unsafe evidence directory must be a regular directory")
}

func TestFileUnsafeEvidenceStore_RecordsConcurrently(t *testing.T) {
	store := NewFileUnsafeEvidenceStore(t.TempDir())
	const count = 16
	var wait sync.WaitGroup
	errors := make(chan error, count)
	for index := range count {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := store.RecordUnsafeEvidence(context.Background(), UnsafeEvidence{
				Source:   "user",
				Action:   "blocked",
				Original: fmt.Sprintf("record-%d", index),
			})
			errors <- err
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}

	list, err := store.LoadUnsafeEvidence(context.Background())
	require.NoError(t, err)
	require.Len(t, list, count)
}

func TestRetainUnsafeEvidenceUsesBoundedLockWait(t *testing.T) {
	dir := t.TempDir()
	store := NewFileUnsafeEvidenceStore(dir)
	lock := flock.New(filepath.Join(dir, unsafeEvidenceLockFilename))
	locked, err := lock.TryLock()
	require.NoError(t, err)
	require.True(t, locked)
	t.Cleanup(func() {
		require.NoError(t, lock.Unlock())
	})

	startedAt := time.Now()
	RetainUnsafeEvidence(context.Background(), store, UnsafeEvidence{
		Source: "user", Action: "blocked", Original: "unsafe",
	})

	require.Less(t, time.Since(startedAt), time.Second)
}

func TestUnsafeEvidenceRecorderContext_RoundTrips(t *testing.T) {
	store := NewFileUnsafeEvidenceStore(t.TempDir())
	ctx := WithUnsafeEvidenceRecorder(context.Background(), store)

	require.Same(t, store, UnsafeEvidenceRecorderFromContext(ctx))
}
