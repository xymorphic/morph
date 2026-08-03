package fileedit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReplaceIfUnchanged_ReplacesFileAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("old\n"), 0o640))
	snapshot, err := ReadSnapshot(path, nil)
	require.NoError(t, err)

	changed, err := ReplaceIfUnchanged(snapshot, []byte("new\n"))

	require.NoError(t, err)
	require.True(t, changed)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "new\n", string(data))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o640), info.Mode().Perm())
}

func TestReplaceIfUnchanged_RejectsConcurrentChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("old\n"), 0o600))
	snapshot, err := ReadSnapshot(path, nil)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("other\n"), 0o600))

	changed, err := ReplaceIfUnchanged(snapshot, []byte("new\n"))

	require.False(t, changed)
	require.EqualError(t, err, path+" changed while it was being edited")
	data, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	require.Equal(t, "other\n", string(data))
}

func TestReadSnapshot_RejectsSymlinkTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.yaml")
	link := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(target, []byte("secret\n"), 0o600))
	require.NoError(t, os.Symlink(target, link))

	_, err := ReadSnapshot(link, nil)

	require.EqualError(t, err, link+" is not a regular file")
}

func TestReplaceIfUnchanged_RejectsSymlinkCreatedAfterSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	target := filepath.Join(dir, "target.yaml")
	require.NoError(t, os.WriteFile(path, []byte("old\n"), 0o600))
	require.NoError(t, os.WriteFile(target, []byte("target\n"), 0o600))
	snapshot, err := ReadSnapshot(path, nil)
	require.NoError(t, err)
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.Symlink(target, path))

	changed, err := ReplaceIfUnchanged(snapshot, []byte("new\n"))

	require.False(t, changed)
	require.EqualError(t, err, path+" is not a regular file")
	data, readErr := os.ReadFile(target)
	require.NoError(t, readErr)
	require.Equal(t, "target\n", string(data))
}

func TestEditFile_ValidatesBeforeReplacing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("old\n"), 0o600))

	result, err := EditFile(context.Background(), EditOptions{
		Path:   path,
		Editor: os.Args[0],
		RunEditor: func(_ context.Context, _ Editor, candidate string) error {
			return os.WriteFile(candidate, []byte("invalid\n"), 0o600)
		},
		Validate: func(string) error { return errors.New("invalid candidate") },
	})

	require.EqualError(t, err, "candidate validation failed: invalid candidate")
	require.NotEmpty(t, result.CandidatePath)
	t.Cleanup(func() { _ = os.Remove(result.CandidatePath) })
	data, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	require.Equal(t, "old\n", string(data))
	_, statErr := os.Stat(result.CandidatePath)
	require.NoError(t, statErr)
}

func TestEditFile_ReplacesValidatedCandidate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("old\n"), 0o600))

	result, err := EditFile(context.Background(), EditOptions{
		Path:   path,
		Editor: os.Args[0],
		RunEditor: func(_ context.Context, _ Editor, candidate string) error {
			return os.WriteFile(candidate, []byte("new\n"), 0o600)
		},
		Validate: func(string) error { return nil },
	})

	require.NoError(t, err)
	require.True(t, result.Changed)
	require.Empty(t, result.CandidatePath)
	data, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	require.Equal(t, "new\n", string(data))
}

func TestSplitCommand_ParsesQuotedArguments(t *testing.T) {
	parts, err := splitCommand(`code --wait "profile config"`)

	require.NoError(t, err)
	require.Equal(t, []string{"code", "--wait", "profile config"}, parts)
}

func TestSplitCommand_RejectsMalformedQuotes(t *testing.T) {
	_, err := splitCommand(`code "unfinished`)

	require.EqualError(t, err, "editor command has an unterminated quote")
}

func TestSplitCommand_PreservesWindowsPathSeparators(t *testing.T) {
	parts, err := splitCommand(`"C:\Program Files\Editor\editor.exe" --wait`)

	require.NoError(t, err)
	require.Equal(t, []string{`C:\Program Files\Editor\editor.exe`, "--wait"}, parts)
}
