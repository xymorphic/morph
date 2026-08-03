package fileedit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/gofrs/flock"
)

type Snapshot struct {
	Path   string
	Data   []byte
	Mode   os.FileMode
	Exists bool
	Digest [sha256.Size]byte
}

type Editor struct {
	Executable string
	Arguments  []string
}

type EditOptions struct {
	Path        string
	DefaultData []byte
	Editor      string
	Validate    func(string) error
	RunEditor   func(context.Context, Editor, string) error
	Retry       func(error, string) bool
}

type EditResult struct {
	Changed       bool
	CandidatePath string
}

func ReadSnapshot(path string, defaultData []byte) (Snapshot, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return Snapshot{}, errors.New("file path is required")
	}

	info, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		return Snapshot{}, fmt.Errorf("stat %s: %w", path, err)
	}

	mode := os.FileMode(0o600)
	exists := err == nil
	data := bytes.Clone(defaultData)
	if exists {
		if !info.Mode().IsRegular() {
			return Snapshot{}, fmt.Errorf("%s is not a regular file", path)
		}
		mode = info.Mode().Perm()
		data, err = os.ReadFile(path)
		if err != nil {
			return Snapshot{}, fmt.Errorf("read %s: %w", path, err)
		}
	}

	return Snapshot{
		Path:   path,
		Data:   data,
		Mode:   mode,
		Exists: exists,
		Digest: sha256.Sum256(data),
	}, nil
}

func ReplaceIfUnchanged(snapshot Snapshot, data []byte) (bool, error) {
	dir := filepath.Dir(snapshot.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, fmt.Errorf("create file directory: %w", err)
	}
	lock := flock.New(snapshot.Path + ".lock")
	if err := lock.Lock(); err != nil {
		return false, fmt.Errorf("lock %s: %w", snapshot.Path, err)
	}
	defer func() { _ = lock.Unlock() }()

	current, err := ReadSnapshot(snapshot.Path, snapshot.Data)
	if err != nil {
		return false, err
	}
	if current.Exists != snapshot.Exists || current.Digest != snapshot.Digest {
		return false, fmt.Errorf("%s changed while it was being edited", snapshot.Path)
	}
	if bytes.Equal(snapshot.Data, data) {
		return false, nil
	}

	temp, err := os.CreateTemp(dir, ".morph-replace-*")
	if err != nil {
		return false, fmt.Errorf("create replacement file: %w", err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()

	mode := snapshot.Mode
	if mode == 0 {
		mode = 0o600
	}
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return false, fmt.Errorf("set replacement permissions: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return false, fmt.Errorf("write replacement file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return false, fmt.Errorf("sync replacement file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return false, fmt.Errorf("close replacement file: %w", err)
	}
	if err := activateReplacement(tempPath, snapshot.Path); err != nil {
		return false, fmt.Errorf("activate replacement file: %w", err)
	}

	return true, nil
}

func EditFile(ctx context.Context, options EditOptions) (EditResult, error) {
	snapshot, err := ReadSnapshot(options.Path, options.DefaultData)
	if err != nil {
		return EditResult{}, err
	}
	editor, err := ResolveEditor(options.Editor)
	if err != nil {
		return EditResult{}, err
	}

	dir := filepath.Dir(snapshot.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return EditResult{}, fmt.Errorf("create edit directory: %w", err)
	}
	temp, err := os.CreateTemp(dir, ".morph-edit-*")
	if err != nil {
		return EditResult{}, fmt.Errorf("create edit candidate: %w", err)
	}
	candidatePath := temp.Name()
	removeCandidate := false
	defer func() {
		if removeCandidate {
			_ = os.Remove(candidatePath)
		}
	}()

	if err := temp.Chmod(snapshot.Mode); err != nil {
		_ = temp.Close()
		return EditResult{CandidatePath: candidatePath}, fmt.Errorf("set candidate permissions: %w", err)
	}
	if _, err := temp.Write(snapshot.Data); err != nil {
		_ = temp.Close()
		return EditResult{CandidatePath: candidatePath}, fmt.Errorf("write edit candidate: %w", err)
	}
	if err := temp.Close(); err != nil {
		return EditResult{CandidatePath: candidatePath}, fmt.Errorf("close edit candidate: %w", err)
	}

	runEditor := options.RunEditor
	if runEditor == nil {
		runEditor = RunEditor
	}
	var candidate []byte
	for {
		if err := runEditor(ctx, editor, candidatePath); err != nil {
			return EditResult{CandidatePath: candidatePath}, err
		}
		candidate, err = os.ReadFile(candidatePath)
		if err != nil {
			return EditResult{CandidatePath: candidatePath}, fmt.Errorf("read edit candidate: %w", err)
		}
		if options.Validate == nil {
			break
		}
		if err := options.Validate(candidatePath); err != nil {
			if options.Retry != nil && options.Retry(err, candidatePath) {
				continue
			}
			return EditResult{CandidatePath: candidatePath}, fmt.Errorf("candidate validation failed: %w", err)
		}
		break
	}

	changed, err := ReplaceIfUnchanged(snapshot, candidate)
	if err != nil {
		return EditResult{CandidatePath: candidatePath}, err
	}
	removeCandidate = true
	return EditResult{Changed: changed}, nil
}

func ResolveEditor(value string) (Editor, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = strings.TrimSpace(os.Getenv("VISUAL"))
	}
	if value == "" {
		value = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	if value == "" {
		if runtime.GOOS == "windows" {
			value = "notepad.exe"
		} else {
			value = "vi"
		}
	}

	parts, err := splitCommand(value)
	if err != nil {
		return Editor{}, fmt.Errorf("parse editor command: %w", err)
	}
	if len(parts) == 0 {
		return Editor{}, errors.New("editor command is required")
	}
	executable, err := exec.LookPath(parts[0])
	if err != nil {
		return Editor{}, fmt.Errorf("editor %q is not available", parts[0])
	}

	return Editor{Executable: executable, Arguments: parts[1:]}, nil
}

func RunEditor(ctx context.Context, editor Editor, path string) error {
	arguments := append(append([]string{}, editor.Arguments...), path)
	command := exec.CommandContext(ctx, editor.Executable, arguments...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("run editor: %w", err)
	}
	return nil
}

func splitCommand(value string) ([]string, error) {
	var parts []string
	var current strings.Builder
	var quote rune
	flush := func() {
		if current.Len() > 0 {
			parts = append(parts, current.String())
			current.Reset()
		}
	}

	runes := []rune(value)
	for index := 0; index < len(runes); index++ {
		char := runes[index]
		if char == '\\' && quote != '\'' && index+1 < len(runes) {
			next := runes[index+1]
			if next == '\\' || next == '"' || next == '\'' || (quote == 0 && strings.ContainsRune(" \t\r\n", next)) {
				current.WriteRune(next)
				index++
				continue
			}
		}
		if quote != 0 {
			if char == quote {
				quote = 0
			} else {
				current.WriteRune(char)
			}
			continue
		}
		switch char {
		case '\'', '"':
			quote = char
		case ' ', '\t', '\r', '\n':
			flush()
		default:
			current.WriteRune(char)
		}
	}
	if len(runes) > 0 && runes[len(runes)-1] == '\\' && quote != '\'' {
		return nil, errors.New("editor command has a trailing escape")
	}
	if quote != 0 {
		return nil, errors.New("editor command has an unterminated quote")
	}
	flush()
	return parts, nil
}
