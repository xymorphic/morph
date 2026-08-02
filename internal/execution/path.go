package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
)

type PreparedPathInput struct {
	LogicalPath        string
	HostSourceIdentity string
	ContainerPath      string
	Grant              string
	Mode               MountMode
	Action             FilesystemAction
	SecurityGeneration string
}

type PreparedPath struct {
	input  PreparedPathInput
	digest string
}

func NewPreparedPath(input PreparedPathInput) (PreparedPath, error) {
	input.LogicalPath = filepath.ToSlash(filepath.Clean(strings.TrimSpace(input.LogicalPath)))
	input.HostSourceIdentity = strings.TrimSpace(input.HostSourceIdentity)
	input.ContainerPath = filepath.ToSlash(filepath.Clean(strings.TrimSpace(input.ContainerPath)))
	input.Grant = strings.TrimSpace(strings.ToLower(input.Grant))
	input.Mode = MountMode(strings.TrimSpace(strings.ToLower(string(input.Mode))))
	input.Action = FilesystemAction(strings.TrimSpace(strings.ToLower(string(input.Action))))
	input.SecurityGeneration = strings.TrimSpace(input.SecurityGeneration)

	if input.LogicalPath == "." || input.LogicalPath == "" {
		return PreparedPath{}, errors.New("execution logical path is required")
	}
	if input.ContainerPath == "." || input.ContainerPath == "" ||
		!strings.HasPrefix(input.ContainerPath, "/") {
		return PreparedPath{}, errors.New("execution container path must be absolute")
	}
	if input.Mode != MountReadOnly && input.Mode != MountReadWrite {
		return PreparedPath{}, errors.New("execution path mode is invalid")
	}
	if input.SecurityGeneration == "" {
		return PreparedPath{}, errors.New("execution path security generation is required")
	}
	if !isFilesystemAction(input.Action) {
		return PreparedPath{}, errors.New("execution path action is invalid")
	}
	if input.Mode == MountReadOnly &&
		(input.Action == FilesystemWrite || input.Action == FilesystemPatch) {
		return PreparedPath{}, errors.New("execution path is read-only")
	}

	sum := sha256.Sum256([]byte(strings.Join([]string{
		input.LogicalPath,
		input.HostSourceIdentity,
		input.ContainerPath,
		input.Grant,
		string(input.Mode),
		string(input.Action),
		input.SecurityGeneration,
	}, "\x00")))
	return PreparedPath{
		input:  input,
		digest: hex.EncodeToString(sum[:]),
	}, nil
}

func (p PreparedPath) LogicalPath() string        { return p.input.LogicalPath }
func (p PreparedPath) HostSourceIdentity() string { return p.input.HostSourceIdentity }
func (p PreparedPath) ContainerPath() string      { return p.input.ContainerPath }
func (p PreparedPath) Grant() string              { return p.input.Grant }
func (p PreparedPath) Mode() MountMode            { return p.input.Mode }
func (p PreparedPath) Action() FilesystemAction   { return p.input.Action }
func (p PreparedPath) SecurityGeneration() string { return p.input.SecurityGeneration }
func (p PreparedPath) Digest() string             { return p.digest }

func isFilesystemAction(action FilesystemAction) bool {
	switch action {
	case FilesystemRead, FilesystemWrite, FilesystemPatch, FilesystemList, FilesystemSearch:
		return true
	default:
		return false
	}
}
