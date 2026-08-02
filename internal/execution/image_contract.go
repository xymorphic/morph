package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
)

type ImageContract struct {
	Version       string            `json:"version"`
	GOOS          string            `json:"goos"`
	Architecture  string            `json:"architecture"`
	Architectures []string          `json:"architectures,omitempty"`
	User          string            `json:"user"`
	Shell         string            `json:"shell"`
	PATH          []string          `json:"path"`
	Executables   map[string]string `json:"executables"`
	Helper        string            `json:"helper"`
	WorkspacePath string            `json:"workspace_path"`
	HomePath      string            `json:"home_path"`
	TemporaryPath string            `json:"temporary_path"`
	ControlPath   string            `json:"control_path"`
}

func (c ImageContract) Normalize() (ImageContract, error) {
	c.Version = strings.TrimSpace(c.Version)
	c.GOOS = strings.TrimSpace(strings.ToLower(c.GOOS))
	c.Architecture = strings.TrimSpace(strings.ToLower(c.Architecture))
	for index := range c.Architectures {
		c.Architectures[index] = strings.TrimSpace(strings.ToLower(c.Architectures[index]))
	}
	slices.Sort(c.Architectures)
	c.Architectures = slices.Compact(c.Architectures)
	c.User = strings.TrimSpace(c.User)
	c.Shell = filepath.ToSlash(filepath.Clean(strings.TrimSpace(c.Shell)))
	c.Helper = filepath.ToSlash(filepath.Clean(strings.TrimSpace(c.Helper)))
	c.WorkspacePath = filepath.ToSlash(filepath.Clean(strings.TrimSpace(c.WorkspacePath)))
	c.HomePath = filepath.ToSlash(filepath.Clean(strings.TrimSpace(c.HomePath)))
	c.TemporaryPath = filepath.ToSlash(filepath.Clean(strings.TrimSpace(c.TemporaryPath)))
	c.ControlPath = filepath.ToSlash(filepath.Clean(strings.TrimSpace(c.ControlPath)))
	c.PATH = slices.Clone(c.PATH)
	c.Executables = cloneStringMap(c.Executables)
	for index := range c.PATH {
		c.PATH[index] = filepath.ToSlash(filepath.Clean(strings.TrimSpace(c.PATH[index])))
	}
	if c.Version == "" ||
		c.GOOS != "linux" ||
		c.Architecture == "" && len(c.Architectures) == 0 ||
		c.User == "" ||
		!isAbsoluteContractPath(c.Shell) || !isAbsoluteContractPath(c.Helper) ||
		!isAbsoluteContractPath(c.WorkspacePath) || !isAbsoluteContractPath(c.HomePath) ||
		!isAbsoluteContractPath(c.TemporaryPath) || !isAbsoluteContractPath(c.ControlPath) ||
		len(c.PATH) == 0 {
		return ImageContract{}, errors.New("sandbox image contract is incomplete")
	}
	for _, path := range c.PATH {
		if !isAbsoluteContractPath(path) {
			return ImageContract{}, errors.New("sandbox image PATH contains a non-absolute entry")
		}
	}
	for name, path := range c.Executables {
		name = strings.TrimSpace(name)
		if name == "" || !isAbsoluteContractPath(path) {
			return ImageContract{}, errors.New("sandbox image executable identity is invalid")
		}
		if filepath.Base(path) != name {
			return ImageContract{}, errors.New(
				"sandbox image executable name does not match its path",
			)
		}
	}
	return c, nil
}

func (c ImageContract) SupportsArchitecture(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	return c.Architecture == value || slices.Contains(c.Architectures, value)
}

func (c ImageContract) Digest() string {
	normalized, err := c.Normalize()
	if err != nil {
		return ""
	}
	raw, _ := json.Marshal(normalized)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func isAbsoluteContractPath(path string) bool {
	return strings.HasPrefix(path, "/") && path != "/"
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[strings.TrimSpace(key)] = filepath.ToSlash(filepath.Clean(strings.TrimSpace(value)))
	}
	return clone
}
