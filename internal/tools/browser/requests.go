package browser

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	browserdomain "github.com/xymorphic/morph/internal/browser"
	envtypes "github.com/xymorphic/morph/internal/environment/types"
	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/internal/tools"
	"github.com/xymorphic/morph/internal/tools/common"
)

const (
	maxBrowserInputBytes  = 1 << 20
	maxBrowserIDLength    = 256
	maxBrowserURLLength   = 8192
	maxBrowserTextLength  = 65536
	maxBrowserValueLength = 8192
	maxBrowserKeyLength   = 128
	maxBrowserRefLength   = 128
)

type request struct {
	Action      browserdomain.Action        `json:"action"`
	Profile     string                      `json:"profile,omitempty"`
	SessionID   string                      `json:"session_id,omitempty"`
	TabID       string                      `json:"tab_id,omitempty"`
	URL         string                      `json:"url,omitempty"`
	Path        string                      `json:"path,omitempty"`
	Handle      string                      `json:"handle,omitempty"`
	Ref         string                      `json:"ref,omitempty"`
	Text        string                      `json:"text,omitempty"`
	Value       string                      `json:"value,omitempty"`
	Key         string                      `json:"key,omitempty"`
	X           int64                       `json:"x,omitempty"`
	Y           int64                       `json:"y,omitempty"`
	Limit       int                         `json:"limit,omitempty"`
	Condition   browserdomain.WaitCondition `json:"condition,omitempty"`
	TimeoutMS   int64                       `json:"timeout_ms,omitempty"`
	Replace     bool                        `json:"replace,omitempty"`
	FullPage    bool                        `json:"full_page,omitempty"`
	fileTarget  string
	targetScope permissions.TargetScope
}

type requestSpec struct {
	allowed  []string
	required []string
}

var requestSpecs = map[browserdomain.Action]requestSpec{
	browserdomain.ActionStatus:   {allowed: []string{"action"}, required: []string{"action"}},
	browserdomain.ActionProfiles: {allowed: []string{"action"}, required: []string{"action"}},
	browserdomain.ActionStart:    {allowed: []string{"action", "profile"}, required: []string{"action"}},
	browserdomain.ActionStop:     sessionRequestSpec(),
	browserdomain.ActionTabs:     sessionRequestSpec(),
	browserdomain.ActionOpen: {
		allowed: []string{"action", "session_id", "url"}, required: []string{"action", "session_id", "url"},
	},
	browserdomain.ActionFocus:    tabRequestSpec(),
	browserdomain.ActionClose:    tabRequestSpec(),
	browserdomain.ActionNavigate: tabValueRequestSpec("url"),
	browserdomain.ActionReload:   tabRequestSpec(),
	browserdomain.ActionSnapshot: tabRequestSpec(),
	browserdomain.ActionScreenshot: {
		allowed:  []string{"action", "session_id", "tab_id", "full_page"},
		required: []string{"action", "session_id", "tab_id"},
	},
	browserdomain.ActionPDF: tabRequestSpec(),
	browserdomain.ActionConsole: {
		allowed:  []string{"action", "session_id", "tab_id", "limit"},
		required: []string{"action", "session_id", "tab_id"},
	},
	browserdomain.ActionClick: tabValueRequestSpec("ref"),
	browserdomain.ActionType: {
		allowed:  []string{"action", "session_id", "tab_id", "ref", "text", "replace"},
		required: []string{"action", "session_id", "tab_id", "ref", "text"},
	},
	browserdomain.ActionPress: tabValueRequestSpec("key"),
	browserdomain.ActionScroll: {
		allowed:  []string{"action", "session_id", "tab_id", "x", "y"},
		required: []string{"action", "session_id", "tab_id", "y"},
	},
	browserdomain.ActionSelect:   tabValueRequestSpec("ref", "value"),
	browserdomain.ActionUpload:   tabValueRequestSpec("ref", "path"),
	browserdomain.ActionDownload: tabValueRequestSpec("ref"),
	browserdomain.ActionExportArtifact: {
		allowed: []string{"action", "handle", "path"}, required: []string{"action", "handle", "path"},
	},
	browserdomain.ActionAcceptDialog: {
		allowed:  []string{"action", "session_id", "tab_id", "ref", "text"},
		required: []string{"action", "session_id", "tab_id", "ref"},
	},
	browserdomain.ActionDismissDialog: tabValueRequestSpec("ref"),
	browserdomain.ActionWait: {
		allowed:  []string{"action", "session_id", "tab_id", "condition", "value", "ref", "timeout_ms"},
		required: []string{"action", "session_id", "tab_id", "condition"},
	},
	browserdomain.ActionBack:    tabRequestSpec(),
	browserdomain.ActionForward: tabRequestSpec(),
}

func sessionRequestSpec() requestSpec {
	return requestSpec{allowed: []string{"action", "session_id"}, required: []string{"action", "session_id"}}
}

func tabRequestSpec() requestSpec {
	return requestSpec{
		allowed: []string{"action", "session_id", "tab_id"}, required: []string{"action", "session_id", "tab_id"},
	}
}

func tabValueRequestSpec(fields ...string) requestSpec {
	spec := tabRequestSpec()
	spec.allowed = append(spec.allowed, fields...)
	spec.required = append(spec.required, fields[0])
	if len(fields) > 1 {
		spec.required = append(spec.required, fields[1:]...)
	}
	return spec
}

func decodeRequest(raw string) (request, error) {
	if len(raw) > maxBrowserInputBytes {
		return request{}, errors.New("browser input exceeds the maximum size")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &fields); err != nil {
		return request{}, errors.New("browser input must be valid JSON")
	}
	for name, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			delete(fields, name)
		}
	}
	var action browserdomain.Action
	if err := json.Unmarshal(fields["action"], &action); err != nil || action == "" {
		return request{}, errors.New("browser action is required")
	}
	spec, ok := requestSpecs[action]
	if !ok {
		return request{}, errors.New("browser action is not supported")
	}
	allowed := make(map[string]struct{}, len(spec.allowed))
	for _, name := range spec.allowed {
		allowed[name] = struct{}{}
	}
	for name := range fields {
		if _, ok := allowed[name]; !ok {
			return request{}, fmt.Errorf("browser field %q is not valid for action %q", name, action)
		}
	}
	for _, name := range spec.required {
		if _, ok := fields[name]; !ok {
			return request{}, fmt.Errorf("browser field %q is required for action %q", name, action)
		}
	}

	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	var decoded request
	if err := decoder.Decode(&decoded); err != nil {
		return request{}, errors.New("browser input has an invalid field type")
	}
	if decoded.TimeoutMS < 0 || decoded.TimeoutMS > int64((2*time.Minute)/time.Millisecond) {
		return request{}, errors.New("browser timeout_ms must be between zero and 120000")
	}
	if decoded.X < -100000 || decoded.X > 100000 || decoded.Y < -100000 || decoded.Y > 100000 {
		return request{}, errors.New("browser scroll offsets must be between -100000 and 100000")
	}
	if decoded.Limit < 0 || decoded.Limit > 200 {
		return request{}, errors.New("browser limit must be between zero and 200")
	}
	if err := checkStringLengths(decoded); err != nil {
		return request{}, err
	}
	for _, name := range getNonEmptyFields(decoded.Action) {
		if strings.TrimSpace(getStringField(decoded, name)) == "" {
			return request{}, fmt.Errorf("browser field %q must not be empty for action %q", name, decoded.Action)
		}
	}
	if decoded.Action == browserdomain.ActionWait {
		switch decoded.Condition {
		case browserdomain.WaitLoad:
		case browserdomain.WaitText, browserdomain.WaitURL:
			if strings.TrimSpace(decoded.Value) == "" {
				return request{}, errors.New("browser wait value is required for text and URL conditions")
			}
		case browserdomain.WaitVisible:
			if strings.TrimSpace(decoded.Ref) == "" {
				return request{}, errors.New("browser wait ref is required for the visible condition")
			}
		default:
			return request{}, errors.New("browser wait condition must be one of: load, text, url, visible")
		}
	}
	return decoded, nil
}

func checkStringLengths(value request) error {
	fields := []struct {
		name  string
		value string
		limit int
	}{
		{name: "profile", value: value.Profile, limit: maxBrowserIDLength},
		{name: "session_id", value: value.SessionID, limit: maxBrowserIDLength},
		{name: "tab_id", value: value.TabID, limit: maxBrowserIDLength},
		{name: "url", value: value.URL, limit: maxBrowserURLLength},
		{name: "path", value: value.Path, limit: maxBrowserURLLength},
		{name: "handle", value: value.Handle, limit: maxBrowserIDLength},
		{name: "ref", value: value.Ref, limit: maxBrowserRefLength},
		{name: "text", value: value.Text, limit: maxBrowserTextLength},
		{name: "value", value: value.Value, limit: maxBrowserValueLength},
		{name: "key", value: value.Key, limit: maxBrowserKeyLength},
	}
	for _, field := range fields {
		if len(field.value) > field.limit {
			return fmt.Errorf("browser field %q exceeds the maximum length", field.name)
		}
	}
	return nil
}

func getNonEmptyFields(action browserdomain.Action) []string {
	switch action {
	case browserdomain.ActionStop, browserdomain.ActionTabs:
		return []string{"session_id"}
	case browserdomain.ActionOpen:
		return []string{"session_id", "url"}
	case browserdomain.ActionFocus, browserdomain.ActionClose, browserdomain.ActionReload,
		browserdomain.ActionSnapshot, browserdomain.ActionScreenshot, browserdomain.ActionPDF,
		browserdomain.ActionConsole, browserdomain.ActionBack, browserdomain.ActionForward:
		return []string{"session_id", "tab_id"}
	case browserdomain.ActionNavigate:
		return []string{"session_id", "tab_id", "url"}
	case browserdomain.ActionClick:
		return []string{"session_id", "tab_id", "ref"}
	case browserdomain.ActionType, browserdomain.ActionSelect:
		return []string{"session_id", "tab_id", "ref"}
	case browserdomain.ActionUpload:
		return []string{"session_id", "tab_id", "ref", "path"}
	case browserdomain.ActionDownload, browserdomain.ActionAcceptDialog, browserdomain.ActionDismissDialog:
		return []string{"session_id", "tab_id", "ref"}
	case browserdomain.ActionExportArtifact:
		return []string{"handle", "path"}
	case browserdomain.ActionPress:
		return []string{"session_id", "tab_id", "key"}
	case browserdomain.ActionScroll, browserdomain.ActionWait:
		return []string{"session_id", "tab_id"}
	default:
		return nil
	}
}

func getStringField(value request, name string) string {
	switch name {
	case "session_id":
		return value.SessionID
	case "tab_id":
		return value.TabID
	case "url":
		return value.URL
	case "ref":
		return value.Ref
	case "path":
		return value.Path
	case "handle":
		return value.Handle
	case "key":
		return value.Key
	default:
		return ""
	}
}

func actionRequestFromRequest(r request) browserdomain.ActionRequest {
	return browserdomain.ActionRequest{
		Profile: r.Profile, SessionID: r.SessionID, TabID: r.TabID, URL: r.URL, Ref: r.Ref,
		Path: r.Path, Handle: r.Handle, FileTarget: r.fileTarget, TargetScope: r.targetScope,
		Text: r.Text, Value: r.Value, Key: r.Key, X: r.X, Y: r.Y, Limit: r.Limit,
		Condition: r.Condition, Timeout: time.Duration(r.TimeoutMS) * time.Millisecond,
		Replace: r.Replace, FullPage: r.FullPage,
	}
}

func prepareRequest(runtime envtypes.Runtime, value request) (request, error) {
	if value.Action != browserdomain.ActionUpload && value.Action != browserdomain.ActionExportArtifact {
		return value, nil
	}
	policy := common.FilesystemPolicyFromRuntime(runtime)
	resolved, err := policy.ResolveUnrestricted(value.Path)
	if err != nil {
		return request{}, getFilePreparationError(value.Action, err)
	}
	if value.Action == browserdomain.ActionExportArtifact {
		value.Path, err = browserdomain.ResolveArtifactExportPath(resolved.Absolute)
		if err != nil {
			return request{}, getFilePreparationError(value.Action, err)
		}
		value.fileTarget = filepath.ToSlash(value.Path)
		value.targetScope = permissions.TargetScopeExternal
		if isBrowserExportWithinCanonicalRoot(resolved.Root, value.Path) {
			value.targetScope = permissions.TargetScopeWorkspace
		}
		return value, nil
	}
	info, err := os.Lstat(resolved.Absolute)
	if err != nil {
		return request{}, getFilePreparationError(value.Action, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return request{}, errors.New("browser upload source must not be a symbolic link or junction")
	}
	canonical, err := filepath.EvalSymlinks(resolved.Absolute)
	if err != nil {
		return request{}, getUploadPreparationError(err)
	}
	value.Path = canonical
	value.fileTarget = filepath.ToSlash(canonical)
	value.targetScope = permissions.TargetScopeExternal
	if _, err := policy.Resolve(resolved.Absolute); err == nil {
		value.targetScope = permissions.TargetScopeWorkspace
	}
	return value, nil
}

func isBrowserExportWithinCanonicalRoot(root string, path string) bool {
	if root == "" {
		return false
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(canonicalRoot, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func getUploadPreparationError(err error) error {
	if os.IsNotExist(err) {
		return tools.NewPermissionResolutionError("browser_upload_not_found", "browser upload source was not found")
	}
	if os.IsPermission(err) {
		return tools.NewPermissionResolutionError("browser_upload_unavailable", "browser upload source is not accessible")
	}
	return tools.NewPermissionResolutionError("browser_upload_invalid", "browser upload source is invalid")
}

func getFilePreparationError(action browserdomain.Action, err error) error {
	if action == browserdomain.ActionUpload {
		return getUploadPreparationError(err)
	}
	return tools.NewPermissionResolutionError("browser_export_invalid", "browser export destination is invalid")
}
