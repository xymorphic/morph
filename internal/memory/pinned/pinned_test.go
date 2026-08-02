package pinned

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/profile"
	state "github.com/xymorphic/morph/internal/state/core"
)

type fakeDirEntry struct {
	name string
}

func (entry fakeDirEntry) Name() string {
	return entry.name
}

func (fakeDirEntry) IsDir() bool {
	return false
}

func (fakeDirEntry) Type() fs.FileMode {
	return 0
}

func (fakeDirEntry) Info() (fs.FileInfo, error) {
	return nil, nil
}

func TestAutoFileUsesProfileHome(t *testing.T) {
	dir := t.TempDir()
	setProfileHome(t, dir)
	file := filepath.Join(dir, "memory.md")
	require.NoError(t, os.WriteFile(file, []byte("remember this"), 0o600))

	got, ok, err := AutoFile()
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, file, got)
}

func TestAutoFileReturnsProfileHomeStatError(t *testing.T) {
	dir := t.TempDir()
	setProfileHome(t, dir)
	statErr := errors.New("stat failed")
	previous := stat
	t.Cleanup(func() {
		stat = previous
	})
	stat = func(string) (os.FileInfo, error) {
		return nil, statErr
	}

	got, ok, err := AutoFile()
	require.ErrorIs(t, err, statErr)
	require.Contains(t, err.Error(), "stat profile home")
	require.False(t, ok)
	require.Empty(t, got)
}

func TestAutoFileFromRoot(t *testing.T) {
	t.Run("empty root returns no file", func(t *testing.T) {
		got, ok := mustAutoFileFromRoot(t, "")
		require.False(t, ok)
		require.Empty(t, got)
	})

	t.Run("missing root returns no file", func(t *testing.T) {
		got, ok := mustAutoFileFromRoot(t, filepath.Join(t.TempDir(), "missing"))
		require.False(t, ok)
		require.Empty(t, got)
	})

	t.Run("file root returns no file", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "not-root")
		require.NoError(t, os.WriteFile(file, []byte("not a dir"), 0o600))
		got, ok := mustAutoFileFromRoot(t, file)
		require.False(t, ok)
		require.Empty(t, got)
	})

	t.Run("stat error is returned", func(t *testing.T) {
		statErr := errors.New("stat failed")
		previous := stat
		t.Cleanup(func() {
			stat = previous
		})
		stat = func(string) (os.FileInfo, error) {
			return nil, statErr
		}

		got, ok, err := getAutoFileFromRoot(t.TempDir())
		require.ErrorIs(t, err, statErr)
		require.Contains(t, err.Error(), "stat profile home")
		require.False(t, ok)
		require.Empty(t, got)
	})

	t.Run("read directory error is returned", func(t *testing.T) {
		readErr := errors.New("read failed")
		previous := readDir
		t.Cleanup(func() {
			readDir = previous
		})
		readDir = func(string) ([]os.DirEntry, error) {
			return nil, readErr
		}

		got, ok, err := getAutoFileFromRoot(t.TempDir())
		require.ErrorIs(t, err, readErr)
		require.Contains(t, err.Error(), "read profile home")
		require.False(t, ok)
		require.Empty(t, got)
	})

	t.Run("directory named memory file is ignored", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(dir, "memory.md"), 0o700))
		got, ok := mustAutoFileFromRoot(t, dir)
		require.False(t, ok)
		require.Empty(t, got)
	})

	t.Run("memory file match is case insensitive", func(t *testing.T) {
		dir := t.TempDir()
		file := filepath.Join(dir, "MEMORY.md")
		require.NoError(t, os.WriteFile(file, []byte("remember this"), 0o600))
		got, ok := mustAutoFileFromRoot(t, dir)
		require.True(t, ok)
		require.Equal(t, file, got)
	})
}

func TestNormalizeOptions(t *testing.T) {
	enabled := true

	opts := NormalizeOptions(Options{
		Enabled:      &enabled,
		MaxChars:     -1,
		MaxItemChars: 0,
	})

	require.Same(t, &enabled, opts.Enabled)
	require.Equal(t, defaultMaxChars, opts.MaxChars)
	require.Equal(t, defaultMaxItemChars, opts.MaxItemChars)
}

func TestEnabled(t *testing.T) {
	enabled := true
	disabled := false

	require.True(t, Enabled(Options{}))
	require.True(t, Enabled(Options{Enabled: &enabled}))
	require.False(t, Enabled(Options{Enabled: &disabled}))
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	setProfileHome(t, dir)
	file := filepath.Join(dir, "memory.md")
	require.NoError(t, os.WriteFile(file, []byte("from file"), 0o600))

	items, err := LoadFile()

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, state.MemoryKindPinned, items[0].Kind)
	require.Equal(t, state.MemoryStatusActive, items[0].Status)
	require.Equal(t, "memory.md", items[0].Title)
	require.Equal(t, "from file", items[0].Text)
	require.Equal(t, map[string]string{"source": "file", "path": file}, items[0].Metadata)
}

func TestLoadFileUsesActiveProfile(t *testing.T) {
	workHome := t.TempDir()
	personalHome := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workHome, "memory.md"), []byte("work memory"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(personalHome, "memory.md"), []byte("personal memory"), 0o600))

	setProfileHome(t, workHome)
	items, err := LoadFile()
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "work memory", items[0].Text)

	setProfileHome(t, personalHome)
	items, err = LoadFile()
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "personal memory", items[0].Text)
}

func TestLoadFileReturnsAutoFileError(t *testing.T) {
	dir := t.TempDir()
	setProfileHome(t, dir)
	statErr := errors.New("stat failed")
	previous := stat
	t.Cleanup(func() {
		stat = previous
	})
	stat = func(string) (os.FileInfo, error) {
		return nil, statErr
	}

	items, err := LoadFile()

	require.ErrorIs(t, err, statErr)
	require.Empty(t, items)
}

func TestLoadFileSkipsMissingAndEmptyFile(t *testing.T) {
	dir := t.TempDir()
	setProfileHome(t, dir)

	items, err := LoadFile()
	require.NoError(t, err)
	require.Empty(t, items)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "memory.md"), []byte(" \n\t "), 0o600))

	items, err = LoadFile()
	require.NoError(t, err)
	require.Empty(t, items)
}

func TestLoadFileReturnsReadError(t *testing.T) {
	dir := t.TempDir()
	setProfileHome(t, dir)

	previousReadDir := readDir
	t.Cleanup(func() {
		readDir = previousReadDir
	})
	readDir = func(string) ([]os.DirEntry, error) {
		return []os.DirEntry{fakeDirEntry{name: "memory.md"}}, nil
	}

	items, err := LoadFile()
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to read pinned memory file")
	require.Empty(t, items)
}

func TestPrepareItems(t *testing.T) {
	var scanned int
	var redacted int
	items, err := PrepareItems(
		context.Background(),
		[]state.MemoryItem{
			{Kind: state.MemoryKindPinned, Status: state.MemoryStatusCandidate, Title: "candidate", Text: "skip"},
			{Kind: state.MemoryKindPinned, Status: state.MemoryStatusActive, Title: " ", Text: " "},
			{Kind: state.MemoryKindPinned, Status: state.MemoryStatusActive, Title: "keep", Text: "secret"},
		},
		state.MemorySearchQuery{MaxChars: 6},
		Options{MaxChars: 20, MaxItemChars: 20},
		func(context.Context, state.MemoryItem) error {
			scanned++
			return nil
		},
		func(_ context.Context, item state.MemoryItem) (state.MemoryItem, error) {
			redacted++
			if item.Text == "secret" {
				item.Text = "ok"
			}
			return item, nil
		},
	)

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "keep", items[0].Title)
	require.Equal(t, "ok", items[0].Text)
	require.Equal(t, 2, scanned)
	require.Equal(t, 2, redacted)
}

func TestPrepareItemsReturnsNilWithoutItems(t *testing.T) {
	items, err := PrepareItems(
		context.Background(),
		nil,
		state.MemorySearchQuery{},
		Options{MaxChars: 10, MaxItemChars: 10},
		nil,
		nil,
	)

	require.NoError(t, err)
	require.Empty(t, items)
}

func TestPrepareItemsBudgets(t *testing.T) {
	items, err := PrepareItems(
		context.Background(),
		[]state.MemoryItem{
			{Kind: state.MemoryKindPinned, Status: state.MemoryStatusActive, Title: "a", Text: "bcdef"},
			{Kind: state.MemoryKindPinned, Status: state.MemoryStatusActive, Title: "x", Text: "y"},
		},
		state.MemorySearchQuery{},
		Options{MaxChars: 3, MaxItemChars: 10},
		nil,
		nil,
	)

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "a", items[0].Title)
	require.Equal(t, "bc", items[0].Text)
}

func TestPrepareItemsReturnsErrors(t *testing.T) {
	scanErr := errors.New("unsafe")
	items, err := PrepareItems(
		context.Background(),
		[]state.MemoryItem{{Status: state.MemoryStatusActive, Text: "unsafe"}},
		state.MemorySearchQuery{},
		Options{MaxChars: 10, MaxItemChars: 10},
		func(context.Context, state.MemoryItem) error {
			return scanErr
		},
		nil,
	)
	require.ErrorIs(t, err, scanErr)
	require.Empty(t, items)

	redactErr := errors.New("redact failed")
	items, err = PrepareItems(
		context.Background(),
		[]state.MemoryItem{{Status: state.MemoryStatusActive, Text: "secret"}},
		state.MemorySearchQuery{},
		Options{MaxChars: 10, MaxItemChars: 10},
		nil,
		func(context.Context, state.MemoryItem) (state.MemoryItem, error) {
			return state.MemoryItem{}, redactErr
		},
	)
	require.ErrorIs(t, err, redactErr)
	require.Empty(t, items)
}

func TestFileMemoryID(t *testing.T) {
	require.Equal(t, "pinned_file:/tmp/memory.md", getFileMemoryID(" /tmp/memory.md "))
}

func TestCountItemChars(t *testing.T) {
	require.Equal(t, 4, getItemCharCount(state.MemoryItem{Title: "go", Text: "✓x"}))
}

func TestTruncateItem(t *testing.T) {
	item := truncateItem(state.MemoryItem{Title: "title", Text: "body"}, 0)
	require.Empty(t, item.Title)
	require.Empty(t, item.Text)

	item = truncateItem(state.MemoryItem{Title: "title", Text: "body"}, 3)
	require.Equal(t, "tit", item.Title)
	require.Empty(t, item.Text)

	item = truncateItem(state.MemoryItem{Title: "go", Text: "abcdef"}, 5)
	require.Equal(t, "go", item.Title)
	require.Equal(t, "abc", item.Text)
}

func TestTruncateRunes(t *testing.T) {
	require.Empty(t, truncateRunes("hello", 0))
	require.Equal(t, "hé", truncateRunes("héllo", 2))
	require.Equal(t, "short", truncateRunes("short", 10))
}

func mustAutoFileFromRoot(t *testing.T, root string) (string, bool) {
	t.Helper()

	file, ok, err := getAutoFileFromRoot(root)
	require.NoError(t, err)
	return file, ok
}

func setProfileHome(t *testing.T, home string) {
	t.Helper()

	original := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(original)
	})
	profile.SetActive(profile.Profile{Name: "test", HomeDir: home})
}
