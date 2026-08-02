package common

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/wandxy/morph/internal/constants"
	envtypes "github.com/wandxy/morph/internal/environment/types"
	"github.com/wandxy/morph/internal/guardrails"
	"github.com/wandxy/morph/internal/permissions"
	"github.com/wandxy/morph/internal/tools"
	"github.com/wandxy/morph/pkg/str"
)

func ResolveFilesystemPath(
	ctx context.Context,
	policy guardrails.FilesystemPolicy,
	path string,
) (guardrails.ResolvedPath, error) {
	if permissions.HasFullAccess(ctx) {
		return policy.ResolveUnrestricted(path)
	}

	return policy.Resolve(path)
}

func ResolveFilesystemPathForOperation(
	ctx context.Context,
	policy guardrails.FilesystemPolicy,
	path string,
	action permissions.Action,
) (guardrails.ResolvedPath, error) {
	target, targetScope := ResolveFilesystemPermissionTarget(policy, path)
	operation := permissions.Operation{
		Resource:    permissions.ResourceFile,
		Action:      action,
		Target:      target,
		TargetScope: targetScope,
	}

	preset, _ := permissions.PresetFromContext(ctx)
	isReadAction := action == permissions.ActionRead ||
		action == permissions.ActionList ||
		action == permissions.ActionSearch
	allowsExternalRead := preset == permissions.PresetAskForApproval && isReadAction
	allowsUnrestrictedPath := permissions.HasFullAccess(ctx) ||
		allowsExternalRead ||
		permissions.IsOperationAuthorized(ctx, operation)

	if allowsUnrestrictedPath {
		return policy.ResolveUnrestricted(path)
	}

	return policy.Resolve(path)
}

func ResolveFilesystemPermissionTarget(
	policy guardrails.FilesystemPolicy,
	path string,
) (string, permissions.TargetScope) {
	path = str.String(path).Trim()
	if path == "" {
		path = "."
	}
	target := filepath.ToSlash(filepath.Clean(path))
	if _, err := policy.Resolve(path); err == nil {
		return target, permissions.TargetScopeWorkspace
	}

	return target, permissions.TargetScopeExternal
}

func FilesystemPolicyFromRuntime(runtime envtypes.Runtime) guardrails.FilesystemPolicy {
	if runtime == nil {
		return guardrails.FilesystemPolicy{}
	}

	return runtime.FilePolicy()
}

const (
	MaxListEntries   = constants.ToolMaxListEntries
	MaxSearchResults = constants.ToolMaxSearchResults
	MaxReadBytes     = constants.ToolMaxReadBytes
	MaxOutputBytes   = constants.ToolMaxOutputBytes
	DefaultTimeout   = constants.ToolDefaultTimeout
	MaxTimeout       = constants.ToolMaxTimeout
)

// DecodeInput decodes raw tool-call arguments into input.
func DecodeInput(call tools.Call, target any) tools.Result {
	inputValue := str.String(call.Input)
	if inputValue.Trim() == "" {
		call.Input = "{}"
	}
	if err := json.Unmarshal([]byte(call.Input), target); err != nil {
		return ToolError("invalid_input", "invalid tool input")
	}

	return tools.Result{}
}

// EncodeOutput encodes a tool result as JSON text.
func EncodeOutput(value any) (tools.Result, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return tools.Result{}, err
	}

	return tools.Result{Output: string(raw)}, nil
}

// ToolError returns the error text from a recorded tool output.
func ToolError(code, message string) tools.Result {
	return tools.Result{Error: tools.Error{Code: code, Message: message}.String()}
}

// FileError returns a file-oriented tool error response.
func FileError(err error) tools.Result {
	if err == nil {
		return tools.Result{}
	}

	switch {
	case errors.Is(err, os.ErrNotExist):
		return ToolError("not_found", "path not found")
	case errors.Is(err, os.ErrPermission):
		return ToolError("access_denied", "access denied")
	case errors.Is(err, fs.ErrInvalid):
		return ToolError("invalid_input", "path must be a file")
	case strings.Contains(err.Error(), "outside allowed roots"):
		return ToolError("path_outside_roots", "path is outside allowed roots")
	case strings.Contains(err.Error(), "size limit"):
		return ToolError("too_large", err.Error())
	case strings.Contains(err.Error(), "not text"):
		return ToolError("not_text", "file is not text")
	case strings.Contains(err.Error(), "directory"):
		return ToolError("invalid_input", "path must be a file")
	default:
		return ToolError("internal_error", err.Error())
	}
}

func NewFilePermissionResolutionError(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "outside allowed roots") {
		return tools.NewPermissionResolutionError("path_outside_roots", "path is outside allowed roots")
	}
	return tools.NewPermissionResolutionError("invalid_path", err.Error())
}

// HiddenPath reports whether path should be hidden from filesystem tool listings.
func HiddenPath(path string) bool {
	for part := range strings.SplitSeq(filepath.ToSlash(path), "/") {
		if strings.HasPrefix(part, ".") && part != "." && part != ".." {
			return true
		}
	}

	return false
}

// NormalizedDisplayPath normalizes display path.
func NormalizedDisplayPath(path string) string {
	if path == "" {
		return "."
	}

	return filepath.ToSlash(path)
}

// TrimOutput truncates long tool output to a bounded display string.
func TrimOutput(value string, limit int) string {
	if len(value) <= limit {
		return value
	}

	return value[:limit]
}

// WithTimeoutSeconds adds a timeout_seconds schema field when limits are configured.
func WithTimeoutSeconds(value int) int {
	if value <= 0 {
		return DefaultTimeout
	}
	if value > MaxTimeout {
		return MaxTimeout
	}

	return value
}

// JoinStrings joins non-empty strings with sep.
func JoinStrings(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		partValue := str.String(part)
		part = partValue.Trim()
		if part == "" {
			continue
		}
		filtered = append(filtered, part)
	}

	return strings.Join(filtered, " ")
}

// ObjectSchema builds a JSON object schema for tool input.
func ObjectSchema(properties map[string]any, required ...string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	if required == nil {
		required = []string{}
	}

	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
		"required":             required,
	}

	return schema
}

// StringSchema builds a JSON string schema for tool input.
func StringSchema(description string) map[string]any {
	return map[string]any{
		"type":        "string",
		"description": description,
	}
}

// BooleanSchema builds a JSON boolean schema for tool input.
func BooleanSchema(description string) map[string]any {
	return map[string]any{
		"type":        "boolean",
		"description": description,
	}
}

// IntegerSchema builds a JSON integer schema for tool input.
func IntegerSchema(description string) map[string]any {
	return map[string]any{
		"type":        "integer",
		"description": description,
	}
}
