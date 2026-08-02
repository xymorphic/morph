package guardrails

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/xymorphic/morph/pkg/str"
)

var (
	statPath = os.Stat
	readPath = os.ReadFile
)

// FilesystemPolicy defines filesystem policy settings.
type FilesystemPolicy struct {
	Roots []string
}

// ResolvedPath describes resolved path.
type ResolvedPath struct {
	Root     string
	Absolute string
	Relative string
}

// NormalizeRoots normalizes roots.
func NormalizeRoots(roots []string) []string {
	if len(roots) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(roots))
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		rootValue := str.String(root)
		root = rootValue.Trim()
		if root == "" {
			continue
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			abs = root
		}
		abs = filepath.Clean(abs)
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		out = append(out, abs)
	}

	return out
}

func (p FilesystemPolicy) Normalize() FilesystemPolicy {
	p.Roots = NormalizeRoots(p.Roots)
	return p
}

// Resolve normalizes a user-supplied path, selects an allowed root for it, and
// returns the absolute path plus its root-relative form. It rejects paths that
// escape the configured roots.
func (p FilesystemPolicy) Resolve(path string) (ResolvedPath, error) {
	p = p.Normalize()
	if len(p.Roots) == 0 {
		return ResolvedPath{}, errors.New("access denied")
	}
	pathValue := str.String(path)
	path = pathValue.Trim()
	if path == "" {
		path = "."
	}

	var candidate string
	if filepath.IsAbs(path) {
		candidate = filepath.Clean(path)
	} else {
		candidate = firstRootCandidate(p.Roots, path)
	}

	for _, root := range p.Roots {
		if isWithinRoot(root, candidate) {
			rel, err := filepath.Rel(root, candidate)
			if err != nil {
				return ResolvedPath{}, err
			}
			if rel == "." {
				rel = ""
			}

			return ResolvedPath{
				Root:     root,
				Absolute: candidate,
				Relative: filepath.ToSlash(rel),
			}, nil
		}
	}

	return ResolvedPath{}, errors.New("path is outside allowed roots")
}

func (p FilesystemPolicy) ResolveUnrestricted(path string) (ResolvedPath, error) {
	if resolved, err := p.Resolve(path); err == nil {
		return resolved, nil
	}

	p = p.Normalize()
	path = str.String(path).Trim()
	if path == "" {
		path = "."
	}

	candidate := filepath.Clean(path)
	if !filepath.IsAbs(candidate) {
		if len(p.Roots) > 0 {
			candidate = filepath.Clean(filepath.Join(p.Roots[0], candidate))
		} else {
			absolute, err := filepath.Abs(candidate)
			if err != nil {
				return ResolvedPath{}, err
			}
			candidate = filepath.Clean(absolute)
		}
	}

	return ResolvedPath{
		Absolute: candidate,
		Relative: filepath.ToSlash(candidate),
	}, nil
}

func firstRootCandidate(roots []string, path string) string {
	candidate := filepath.Clean(filepath.Join(roots[0], path))
	for _, root := range roots {
		next := filepath.Clean(filepath.Join(root, path))
		if _, err := os.Stat(next); err == nil {
			return next
		}
	}

	return candidate
}

func (p FilesystemPolicy) EnsureWithin(path string) error {
	_, err := p.Resolve(path)
	return err
}

// ReadTextFile reads a UTF-8 text file after applying filesystem policy checks.
func ReadTextFile(path string, maxBytes int64) ([]byte, error) {
	info, err := statPath(path)
	if err != nil {
		return nil, err
	}

	if info.IsDir() {
		return nil, fs.ErrInvalid
	}
	if maxBytes > 0 && info.Size() > maxBytes {
		return nil, errors.New("file exceeds size limit")
	}

	content, err := readPath(path)
	if err != nil {
		return nil, err
	}

	if maxBytes > 0 && int64(len(content)) > maxBytes {
		return nil, errors.New("file exceeds size limit")
	}

	if IsBinary(content) {
		return nil, errors.New("file is not text")
	}

	return content, nil
}

// IsBinary reports whether data appears to contain binary content.
func IsBinary(content []byte) bool {
	if len(content) == 0 {
		return false
	}

	if hasNULBytes(content) {
		return true
	}

	return !utf8.Valid(content)
}

func hasNULBytes(content []byte) bool {
	return slices.Contains(content, 0)
}

func isWithinRoot(root, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	if root == candidate {
		return true
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}

	return rel == "." || (!strings.HasPrefix(rel, "..") && rel != "..")
}
