package docker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/wandxy/morph/internal/execution"
	"github.com/wandxy/morph/internal/guardrails"
)

func (b *Backend) ReadFile(ctx context.Context, spec execution.Spec, limit int) ([]byte, error) {
	operation, err := getFilesystemOperation(spec, execution.FilesystemRead)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || int64(limit) > spec.Exposure().Limits().OutputBytes {
		limit = int(spec.Exposure().Limits().OutputBytes)
	}
	result, err := b.execute(ctx, spec, "filesystem", []string{
		"fs-read", operation.Path.ContainerPath(), strconv.Itoa(limit),
	}, nil)
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, getFilesystemError(result)
	}
	content := []byte(result.Stdout)
	if len(content) > limit {
		return nil, errors.New("file exceeds the read limit")
	}
	if guardrails.IsBinary(content) {
		return nil, errors.New("file is not text")
	}
	return content, nil
}

func (b *Backend) WriteFile(
	ctx context.Context,
	spec execution.Spec,
	createDirs bool,
) (execution.FileInfo, error) {
	operation, err := getFilesystemOperation(spec, execution.FilesystemWrite)
	if err != nil {
		return execution.FileInfo{}, err
	}
	result, err := b.execute(ctx, spec, "filesystem", []string{
		"fs-write",
		operation.Path.ContainerPath(),
		strconv.FormatBool(createDirs),
		strconv.FormatInt(spec.Exposure().Limits().ControlInputBytes, 10),
	}, operation.Data)
	if err != nil {
		return execution.FileInfo{}, err
	}
	if result.ExitCode != 0 {
		return execution.FileInfo{}, getFilesystemError(result)
	}
	var payload struct {
		Path    string `json:"path"`
		Size    int64  `json:"size"`
		Mode    uint32 `json:"mode"`
		Created bool   `json:"created"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil {
		return execution.FileInfo{}, err
	}
	return execution.FileInfo{
		Path:    operation.Path.LogicalPath(),
		Size:    payload.Size,
		Mode:    payload.Mode,
		Created: payload.Created,
	}, nil
}

func (b *Backend) PatchFile(ctx context.Context, spec execution.Spec) (execution.FileInfo, error) {
	operation, err := getFilesystemOperation(spec, execution.FilesystemPatch)
	if err != nil {
		return execution.FileInfo{}, err
	}
	paths := operation.Paths
	if len(paths) == 0 {
		paths = []execution.PreparedPath{operation.Path}
	}
	allowed := make([]string, 0, len(paths))
	for _, path := range paths {
		allowed = append(allowed, path.ContainerPath())
	}
	rawAllowed, _ := json.Marshal(allowed)
	result, err := b.execute(ctx, spec, "filesystem", []string{
		"fs-patch",
		strconv.Itoa(operation.Strip),
		strconv.FormatInt(spec.Exposure().Limits().ControlInputBytes, 10),
		base64.RawURLEncoding.EncodeToString(rawAllowed),
	}, operation.Data)
	if err != nil {
		return execution.FileInfo{}, err
	}
	if result.ExitCode != 0 {
		message := strings.TrimSpace(result.Stdout + "\n" + result.Stderr)
		lowerMessage := strings.ToLower(message)
		if strings.Contains(lowerMessage, "failed") ||
			strings.Contains(lowerMessage, "reversed") ||
			strings.Contains(lowerMessage, "previously applied") {
			return execution.FileInfo{}, fmt.Errorf(
				"%w: %s",
				execution.ErrPatchConflict,
				message,
			)
		}
		return execution.FileInfo{}, getFilesystemError(result)
	}
	return execution.FileInfo{Path: operation.Path.LogicalPath()}, nil
}

func (b *Backend) ListFiles(
	ctx context.Context,
	spec execution.Spec,
	limit int,
) ([]execution.FileEntry, error) {
	operation, err := getFilesystemOperation(spec, execution.FilesystemList)
	if err != nil {
		return nil, err
	}
	result, err := b.execute(ctx, spec, "filesystem", []string{
		"fs-list",
		operation.Path.ContainerPath(),
		strconv.FormatBool(operation.Recursive),
		strconv.FormatBool(operation.IncludeHidden),
		strconv.Itoa(limit),
	}, nil)
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, getFilesystemError(result)
	}
	var entries []execution.FileEntry
	if err := json.Unmarshal([]byte(result.Stdout), &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func (b *Backend) SearchFiles(
	ctx context.Context,
	spec execution.Spec,
	limit int,
) ([]execution.SearchMatch, error) {
	operation, err := getFilesystemOperation(spec, execution.FilesystemSearch)
	if err != nil {
		return nil, err
	}
	result, err := b.execute(ctx, spec, "filesystem", []string{
		"fs-search",
		operation.Path.ContainerPath(),
		operation.Query,
		strconv.FormatBool(operation.CaseSensitive),
		strconv.FormatBool(operation.IncludeHidden),
		strconv.Itoa(limit),
	}, nil)
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, getFilesystemError(result)
	}
	var matches []execution.SearchMatch
	if err := json.Unmarshal([]byte(result.Stdout), &matches); err != nil {
		return nil, err
	}
	return matches, nil
}

func getFilesystemOperation(
	spec execution.Spec,
	action execution.FilesystemAction,
) (*execution.FilesystemOperation, error) {
	operation := spec.Operation().Filesystem
	if operation == nil || operation.Action != action {
		return nil, errors.New("docker filesystem execution specification is invalid")
	}
	return operation, nil
}

func getFilesystemError(result execution.CommandResult) error {
	message := result.Stderr
	if message == "" {
		message = "Docker filesystem helper failed"
	}
	return errors.New(message)
}
