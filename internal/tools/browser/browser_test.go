package browser

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	browserdomain "github.com/xymorphic/morph/internal/browser"
	envtypes "github.com/xymorphic/morph/internal/environment/types"
	"github.com/xymorphic/morph/internal/guardrails"
	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/internal/tools"
	toolmocks "github.com/xymorphic/morph/internal/tools/mocks"
)

type browserServiceStub struct {
	envtypes.BrowserService
	resolvedAction    browserdomain.Action
	resolvedRequest   browserdomain.ActionRequest
	operations        []permissions.Operation
	permissionInputs  []permissions.EvaluationInput
	resolveErr        error
	status            browserdomain.Status
	navigated         browserdomain.ActionRequest
	dispatchedAction  browserdomain.Action
	dispatchedRequest browserdomain.ActionRequest
	exportRequest     browserdomain.ArtifactExportRequest
	dispatchErr       error
	statusCalls       int
}

func TestProjectSemanticContent_IndexesOnlyReadableBrowserResults(t *testing.T) {
	t.Run("snapshot", func(t *testing.T) {
		raw, err := json.Marshal(browserdomain.Snapshot{
			TabID: "tab_1", URL: "https://example.com", Title: "Example",
			Nodes: []browserdomain.SnapshotNode{{
				Ref: "ref_1", Role: "button", Name: "Continue", Description: "Submit form",
			}},
		})
		require.NoError(t, err)
		content := projectSemanticContent(tools.Call{
			Input: `{"action":"snapshot","session_id":"browser_1","tab_id":"tab_1"}`,
		}, tools.Result{Output: string(raw)})

		require.Contains(t, content, "url: https://example.com")
		require.Contains(t, content, "title: Example")
		require.Contains(t, content, "role: button")
		require.Contains(t, content, "name: Continue")
		require.NotContains(t, content, "tab_1")
		require.NotContains(t, content, "ref_1")
	})

	t.Run("console", func(t *testing.T) {
		raw, err := json.Marshal([]browserdomain.ConsoleMessage{{Level: browserdomain.ConsoleError, Text: "request failed"}})
		require.NoError(t, err)
		content := projectSemanticContent(tools.Call{
			Input: `{"action":"console","session_id":"browser_1","tab_id":"tab_1"}`,
		}, tools.Result{Output: string(raw)})

		require.Equal(t, "level: error\ntext: request failed", content)
	})

	t.Run("control action", func(t *testing.T) {
		content := projectSemanticContent(tools.Call{
			Input: `{"action":"navigate","session_id":"browser_1","tab_id":"tab_1","url":"https://example.com"}`,
		}, tools.Result{Output: `{"url":"https://example.com","title":"Example"}`})

		require.Empty(t, content)
	})
}

func (s *browserServiceStub) ResolvePermissionInputs(
	_ context.Context,
	action browserdomain.Action,
	request browserdomain.ActionRequest,
) ([]permissions.EvaluationInput, error) {
	s.resolvedAction = action
	s.resolvedRequest = request
	if s.permissionInputs != nil || s.resolveErr != nil {
		return s.permissionInputs, s.resolveErr
	}
	inputs := make([]permissions.EvaluationInput, len(s.operations))
	for index, operation := range s.operations {
		inputs[index] = permissions.EvaluationInput{Operation: operation}
	}
	return inputs, nil
}

func (s *browserServiceStub) Status() browserdomain.Status {
	s.statusCalls++
	return s.status
}

func (s *browserServiceStub) Navigate(
	_ context.Context,
	request browserdomain.ActionRequest,
) (browserdomain.Tab, error) {
	s.navigated = request
	s.dispatchedAction = browserdomain.ActionNavigate
	s.dispatchedRequest = request
	return browserdomain.Tab{ID: request.TabID, SessionID: request.SessionID, URL: request.URL}, nil
}

func (s *browserServiceStub) Start(
	_ context.Context,
	request browserdomain.StartRequest,
) (browserdomain.Session, error) {
	s.dispatchedAction = browserdomain.ActionStart
	s.dispatchedRequest = browserdomain.ActionRequest{Profile: request.Profile}
	return browserdomain.Session{ID: "session", Profile: request.Profile}, s.dispatchErr
}

func (s *browserServiceStub) Stop(_ context.Context, sessionID string) (browserdomain.Session, error) {
	s.dispatchedAction = browserdomain.ActionStop
	s.dispatchedRequest = browserdomain.ActionRequest{SessionID: sessionID}
	return browserdomain.Session{ID: "session"}, s.dispatchErr
}

func (s *browserServiceStub) Tabs(_ context.Context, sessionID string) ([]browserdomain.Tab, error) {
	s.dispatchedAction = browserdomain.ActionTabs
	s.dispatchedRequest = browserdomain.ActionRequest{SessionID: sessionID}
	return []browserdomain.Tab{{ID: "tab"}}, s.dispatchErr
}

func (s *browserServiceStub) Open(
	_ context.Context,
	request browserdomain.ActionRequest,
) (browserdomain.Tab, error) {
	return s.dispatchTab(browserdomain.ActionOpen, request)
}

func (s *browserServiceStub) Focus(
	_ context.Context,
	request browserdomain.ActionRequest,
) (browserdomain.Tab, error) {
	return s.dispatchTab(browserdomain.ActionFocus, request)
}

func (s *browserServiceStub) CloseTab(
	_ context.Context,
	request browserdomain.ActionRequest,
) (browserdomain.Tab, error) {
	return s.dispatchTab(browserdomain.ActionClose, request)
}

func (s *browserServiceStub) Reload(
	_ context.Context,
	request browserdomain.ActionRequest,
) (browserdomain.Tab, error) {
	return s.dispatchTab(browserdomain.ActionReload, request)
}

func (s *browserServiceStub) Snapshot(
	_ context.Context,
	request browserdomain.ActionRequest,
) (browserdomain.Snapshot, error) {
	s.dispatchedAction = browserdomain.ActionSnapshot
	s.dispatchedRequest = request
	return browserdomain.Snapshot{TabID: request.TabID}, s.dispatchErr
}

func (s *browserServiceStub) Screenshot(
	_ context.Context,
	request browserdomain.ActionRequest,
) (browserdomain.Artifact, error) {
	s.dispatchedAction = browserdomain.ActionScreenshot
	s.dispatchedRequest = request
	return browserdomain.Artifact{Handle: "artifact_screen", Kind: browserdomain.ArtifactScreenshot, Size: 3}, s.dispatchErr
}

func (s *browserServiceStub) PDF(
	_ context.Context,
	request browserdomain.ActionRequest,
) (browserdomain.Artifact, error) {
	s.dispatchedAction = browserdomain.ActionPDF
	s.dispatchedRequest = request
	return browserdomain.Artifact{Handle: "artifact_pdf", Kind: browserdomain.ArtifactPDF, Size: 3}, s.dispatchErr
}

func (s *browserServiceStub) Console(
	_ context.Context,
	request browserdomain.ActionRequest,
) ([]browserdomain.ConsoleMessage, error) {
	s.dispatchedAction = browserdomain.ActionConsole
	s.dispatchedRequest = request
	return []browserdomain.ConsoleMessage{{Level: browserdomain.ConsoleInfo, Text: "ready"}}, s.dispatchErr
}

func (s *browserServiceStub) Click(
	_ context.Context,
	request browserdomain.ActionRequest,
) (browserdomain.Tab, error) {
	return s.dispatchTab(browserdomain.ActionClick, request)
}

func (s *browserServiceStub) Type(
	_ context.Context,
	request browserdomain.ActionRequest,
) (browserdomain.Tab, error) {
	return s.dispatchTab(browserdomain.ActionType, request)
}

func (s *browserServiceStub) Press(
	_ context.Context,
	request browserdomain.ActionRequest,
) (browserdomain.Tab, error) {
	return s.dispatchTab(browserdomain.ActionPress, request)
}

func (s *browserServiceStub) Scroll(
	_ context.Context,
	request browserdomain.ActionRequest,
) (browserdomain.Tab, error) {
	return s.dispatchTab(browserdomain.ActionScroll, request)
}

func (s *browserServiceStub) Select(
	_ context.Context,
	request browserdomain.ActionRequest,
) (browserdomain.Tab, error) {
	return s.dispatchTab(browserdomain.ActionSelect, request)
}

func (s *browserServiceStub) Upload(
	_ context.Context,
	request browserdomain.ActionRequest,
) (browserdomain.Tab, error) {
	return s.dispatchTab(browserdomain.ActionUpload, request)
}

func (s *browserServiceStub) Download(
	_ context.Context,
	request browserdomain.ActionRequest,
) (browserdomain.Artifact, error) {
	s.dispatchedAction = browserdomain.ActionDownload
	s.dispatchedRequest = request
	return browserdomain.Artifact{Handle: "artifact_download", Kind: browserdomain.ArtifactDownload, Size: 8}, s.dispatchErr
}

func (s *browserServiceStub) ExportArtifact(
	_ context.Context,
	request browserdomain.ArtifactExportRequest,
) (browserdomain.Artifact, error) {
	s.dispatchedAction = browserdomain.ActionExportArtifact
	s.exportRequest = request
	return browserdomain.Artifact{
		Handle: request.Handle, Kind: browserdomain.ArtifactScreenshot, Name: "screenshot.png", MIMEType: "image/png",
		Size: 3,
	}, s.dispatchErr
}

func (s *browserServiceStub) AcceptDialog(
	_ context.Context,
	request browserdomain.ActionRequest,
) (browserdomain.Tab, error) {
	return s.dispatchTab(browserdomain.ActionAcceptDialog, request)
}

func (s *browserServiceStub) DismissDialog(
	_ context.Context,
	request browserdomain.ActionRequest,
) (browserdomain.Tab, error) {
	return s.dispatchTab(browserdomain.ActionDismissDialog, request)
}

func (s *browserServiceStub) Wait(
	_ context.Context,
	request browserdomain.ActionRequest,
) (browserdomain.Tab, error) {
	return s.dispatchTab(browserdomain.ActionWait, request)
}

func (s *browserServiceStub) Back(
	_ context.Context,
	request browserdomain.ActionRequest,
) (browserdomain.Tab, error) {
	return s.dispatchTab(browserdomain.ActionBack, request)
}

func (s *browserServiceStub) Forward(
	_ context.Context,
	request browserdomain.ActionRequest,
) (browserdomain.Tab, error) {
	return s.dispatchTab(browserdomain.ActionForward, request)
}

func (s *browserServiceStub) dispatchTab(
	action browserdomain.Action,
	request browserdomain.ActionRequest,
) (browserdomain.Tab, error) {
	s.dispatchedAction = action
	s.dispatchedRequest = request
	return browserdomain.Tab{ID: request.TabID, SessionID: request.SessionID}, s.dispatchErr
}

func TestInputSchema_HasProviderCompatibleActionCatalog(t *testing.T) {
	schema := inputSchema()
	require.Equal(t, "object", schema["type"])
	require.Equal(t, false, schema["additionalProperties"])
	require.Equal(t, []string{"action"}, schema["required"])
	require.NotContains(t, schema, "oneOf")
	require.Len(t, browserdomain.SupportedActions(), len(requestSpecs))

	properties := schema["properties"].(map[string]any)
	actionSchema := properties["action"].(map[string]any)
	require.Equal(t, "string", actionSchema["type"])

	actions := make([]string, 0, len(requestSpecs))
	for _, action := range browserdomain.SupportedActions() {
		actions = append(actions, string(action))
		require.Contains(t, actionSchema["description"], string(action)+"[")
	}
	require.Equal(t, actions, actionSchema["enum"])

	expectedFields := map[string]struct{}{"action": {}}
	for action, spec := range requestSpecs {
		for _, field := range spec.allowed {
			expectedFields[field] = struct{}{}
		}
		_, ok := actionDispatchers[action]
		require.True(t, ok, "missing dispatcher for %s", action)
	}
	require.Len(t, properties, len(expectedFields))
	for field := range expectedFields {
		require.Contains(t, properties, field)
	}
	require.Len(t, actionDispatchers, len(requestSpecs))
	requireSchemaOmitsUnsupportedStrictKeywords(t, schema)
}

func requireSchemaOmitsUnsupportedStrictKeywords(t *testing.T, value any) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		for _, keyword := range []string{"oneOf", "allOf", "not", "const", "maxLength", "minimum", "maximum"} {
			require.NotContains(t, typed, keyword)
		}
		for _, child := range typed {
			requireSchemaOmitsUnsupportedStrictKeywords(t, child)
		}
	case []any:
		for _, child := range typed {
			requireSchemaOmitsUnsupportedStrictKeywords(t, child)
		}
	}
}

func TestDecodeRequest_RejectsMalformedAmbiguousAndOutOfRangeInputs(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "not JSON", input: "{"},
		{name: "missing action", input: `{}`},
		{name: "unsupported action", input: `{"action":"evaluate"}`},
		{name: "wrong action field", input: `{"action":"status","url":"https://example.com"}`},
		{name: "unknown null field", input: `{"action":"status","unknown":null}`},
		{name: "missing field", input: `{"action":"navigate","session_id":"session","tab_id":"tab"}`},
		{name: "null identity", input: `{"action":"focus","session_id":null,"tab_id":"tab"}`},
		{name: "blank identity", input: `{"action":"focus","session_id":" ","tab_id":"tab"}`},
		{name: "wrong type", input: `{"action":"scroll","session_id":"session","tab_id":"tab","y":"down"}`},
		{name: "large scroll", input: `{"action":"scroll","session_id":"session","tab_id":"tab","y":100001}`},
		{name: "small scroll", input: `{"action":"scroll","session_id":"session","tab_id":"tab","y":-100001}`},
		{name: "negative timeout", input: `{"action":"wait","session_id":"session","tab_id":"tab","condition":"load","timeout_ms":-1}`},
		{name: "large timeout", input: `{"action":"wait","session_id":"session","tab_id":"tab","condition":"load","timeout_ms":120001}`},
		{name: "wait without value", input: `{"action":"wait","session_id":"session","tab_id":"tab","condition":"text"}`},
		{name: "visible without ref", input: `{"action":"wait","session_id":"session","tab_id":"tab","condition":"visible"}`},
		{name: "unknown wait", input: `{"action":"wait","session_id":"session","tab_id":"tab","condition":"idle"}`},
		{name: "oversized input", input: strings.Repeat(" ", maxBrowserInputBytes+1)},
		{name: "oversized URL", input: `{"action":"open","session_id":"session","url":"` +
			strings.Repeat("x", maxBrowserURLLength+1) + `"}`},
		{name: "oversized text", input: `{"action":"type","session_id":"session","tab_id":"tab","ref":"g1e1","text":"` +
			strings.Repeat("x", maxBrowserTextLength+1) + `"}`},
		{name: "large console limit", input: `{"action":"console","session_id":"session","tab_id":"tab","limit":201}`},
		{name: "wrong rich field", input: `{"action":"pdf","session_id":"session","tab_id":"tab","full_page":true}`},
		{name: "missing export handle", input: `{"action":"export_artifact","path":"saved.png"}`},
		{name: "missing export path", input: `{"action":"export_artifact","handle":"artifact_1"}`},
		{name: "foreign export field", input: `{"action":"export_artifact","handle":"artifact_1","path":"saved.png","session_id":"session"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeRequest(test.input)
			require.Error(t, err)
		})
	}
}

func TestDefinition_ExportsArtifactToCanonicalAuthorizedPath(t *testing.T) {
	root := t.TempDir()
	service := &browserServiceStub{operations: []permissions.Operation{
		{Tool: "browser", Resource: permissions.ResourceBrowser, Action: permissions.ActionRead},
		{Tool: "browser", Resource: permissions.ResourceFile, Action: permissions.ActionCreate},
	}}
	runtime := &toolmocks.Runtime{
		BrowserServiceValue: service, BrowserServiceOK: true,
		FilePolicyValue: guardrails.FilesystemPolicy{Roots: []string{root}},
	}
	definition := Definition(runtime)
	call := tools.Call{Name: toolName, Input: `{
		"action":"export_artifact","handle":"artifact_1","path":"saved.png"
	}`}

	inputs, err := definition.ResolvePermission(context.Background(), call)
	require.NoError(t, err)
	require.Len(t, inputs, 2)
	destination, err := browserdomain.ResolveArtifactExportPath(filepath.Join(root, "saved.png"))
	require.NoError(t, err)
	require.Equal(t, browserdomain.ActionExportArtifact, service.resolvedAction)
	require.Equal(t, "artifact_1", service.resolvedRequest.Handle)
	require.Equal(t, destination, service.resolvedRequest.Path)
	require.Equal(t, filepath.ToSlash(destination), service.resolvedRequest.FileTarget)
	require.Equal(t, permissions.TargetScopeWorkspace, service.resolvedRequest.TargetScope)

	result, err := definition.Handler.Invoke(context.Background(), call)
	require.NoError(t, err)
	require.Empty(t, result.Error)
	require.Equal(t, browserdomain.ActionExportArtifact, service.dispatchedAction)
	require.Equal(t, destination, service.exportRequest.Path)
	require.Equal(t, filepath.ToSlash(destination), service.exportRequest.FileTarget)
	require.JSONEq(t, `{
		"handle":"artifact_1","kind":"screenshot","name":"screenshot.png","mime_type":"image/png","size":3,
		"created_at":"0001-01-01T00:00:00Z","expires_at":"0001-01-01T00:00:00Z",
		"saved_to":"`+destination+`"
	}`, result.Output)
}

func TestDefinition_ExportArtifactClassifiesCanonicalSymlinkDestinationAsExternal(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	link := filepath.Join(root, "external-link")
	if err := os.Symlink(external, link); err != nil {
		t.Skip("symbolic links unavailable")
	}
	service := &browserServiceStub{operations: []permissions.Operation{
		{Tool: "browser", Resource: permissions.ResourceBrowser, Action: permissions.ActionRead},
		{Tool: "browser", Resource: permissions.ResourceFile, Action: permissions.ActionCreate},
	}}
	runtime := &toolmocks.Runtime{
		BrowserServiceValue: service, BrowserServiceOK: true,
		FilePolicyValue: guardrails.FilesystemPolicy{Roots: []string{root}},
	}
	definition := Definition(runtime)
	call := tools.Call{Name: toolName, Input: `{
		"action":"export_artifact","handle":"artifact_1","path":"external-link/saved.png"
	}`}

	_, err := definition.ResolvePermission(context.Background(), call)
	require.NoError(t, err)
	canonicalExternal, err := filepath.EvalSymlinks(external)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(canonicalExternal, "saved.png"), service.resolvedRequest.Path)
	require.Equal(t, permissions.TargetScopeExternal, service.resolvedRequest.TargetScope)
}

func TestDecodeRequest_IgnoresNullableFieldsFromStrictSchema(t *testing.T) {
	decoded, err := decodeRequest(`{
		"action":"status",
		"profile":null,
		"session_id":null,
		"tab_id":null,
		"url":null,
		"path":null,
		"ref":null,
		"text":null,
		"value":null,
		"key":null,
		"x":null,
		"y":null,
		"limit":null,
		"condition":null,
		"timeout_ms":null,
		"replace":null,
		"full_page":null
	}`)
	require.NoError(t, err)
	require.Equal(t, browserdomain.ActionStatus, decoded.Action)
}

func TestDefinition_RichActionsUseCanonicalFileTargetsAndReturnOnlyArtifactMetadata(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "upload.txt")
	require.NoError(t, os.WriteFile(source, []byte("approved"), 0o600))
	service := &browserServiceStub{operations: []permissions.Operation{{
		Tool: "browser", Resource: permissions.ResourceFile, Action: permissions.ActionRead,
	}}}
	runtime := &toolmocks.Runtime{
		BrowserServiceValue: service, BrowserServiceOK: true,
		FilePolicyValue: guardrails.FilesystemPolicy{Roots: []string{root}},
	}
	definition := Definition(runtime)
	call := tools.Call{Name: toolName, Input: `{
		"action":"upload","session_id":"session","tab_id":"tab","ref":"r1","path":"upload.txt"
	}`}
	inputs, err := definition.ResolvePermission(context.Background(), call)
	require.NoError(t, err)
	require.Len(t, inputs, 1)
	canonicalSource, err := filepath.EvalSymlinks(source)
	require.NoError(t, err)
	require.Equal(t, filepath.ToSlash(canonicalSource), service.resolvedRequest.FileTarget)
	require.Equal(t, permissions.TargetScopeWorkspace, service.resolvedRequest.TargetScope)
	require.Equal(t, canonicalSource, service.resolvedRequest.Path)
	result, err := definition.Handler.Invoke(context.Background(), call)
	require.NoError(t, err)
	require.Empty(t, result.Error)
	require.Equal(t, browserdomain.ActionUpload, service.dispatchedAction)
	require.Equal(t, canonicalSource, service.dispatchedRequest.Path)

	result, err = definition.Handler.Invoke(context.Background(), tools.Call{
		Name: toolName, Input: `{"action":"screenshot","session_id":"session","tab_id":"tab","full_page":true}`,
	})
	require.NoError(t, err)
	require.NotContains(t, result.Output, "png")
	require.JSONEq(t, `{
		"handle":"artifact_screen","kind":"screenshot","name":"","mime_type":"","size":3,
		"created_at":"0001-01-01T00:00:00Z","expires_at":"0001-01-01T00:00:00Z"
	}`, result.Output)
	require.NotContains(t, result.Output, "session_id")
	require.NotContains(t, result.Output, "source")
	require.NotContains(t, result.Output, "sensitive")
	require.True(t, service.dispatchedRequest.FullPage)
}

func TestPrepareRequest_ReturnsStableUploadErrorsWithoutExposingPaths(t *testing.T) {
	runtime := &toolmocks.Runtime{FilePolicyValue: guardrails.FilesystemPolicy{Roots: []string{t.TempDir()}}}
	secretPath := filepath.Join(t.TempDir(), "private-token.txt")
	_, err := prepareRequest(runtime, request{Action: browserdomain.ActionUpload, Path: secretPath})
	resolutionErr, ok := tools.GetPermissionResolutionError(err)
	require.True(t, ok)
	require.Equal(t, "browser_upload_not_found", resolutionErr.Code)
	require.NotContains(t, resolutionErr.Message, secretPath)

	for _, test := range []struct {
		err  error
		code string
	}{
		{err: os.ErrPermission, code: "browser_upload_unavailable"},
		{err: errors.New("invalid /private/path"), code: "browser_upload_invalid"},
	} {
		resolutionErr, ok = tools.GetPermissionResolutionError(getUploadPreparationError(test.err))
		require.True(t, ok)
		require.Equal(t, test.code, resolutionErr.Code)
		require.NotContains(t, resolutionErr.Message, "/private/path")
	}
}

func TestDefinition_ResolvesPermissionsAndDispatchesTypedAction(t *testing.T) {
	service := &browserServiceStub{operations: []permissions.Operation{
		{Tool: "browser", Resource: permissions.ResourceBrowser, Action: permissions.ActionUpdate},
		{Tool: "browser", Resource: permissions.ResourceNetwork, Action: permissions.ActionRead},
	}}
	runtime := &toolmocks.Runtime{BrowserServiceValue: service, BrowserServiceOK: true}
	definition := Definition(runtime)
	require.Equal(t, toolName, definition.Name)
	require.True(t, definition.Requires.Browser)
	require.False(t, definition.ParallelSafe)
	call := tools.Call{Name: toolName, Input: `{
		"action":"navigate",
		"session_id":"session-1",
		"tab_id":"tab-1",
		"url":"https://example.com/news"
	}`}

	inputs, err := definition.ResolvePermission(context.Background(), call)
	require.NoError(t, err)
	require.Len(t, inputs, 2)
	require.Equal(t, browserdomain.ActionNavigate, service.resolvedAction)
	require.Equal(t, "tab-1", service.resolvedRequest.TabID)

	result, err := definition.Handler.Invoke(context.Background(), call)
	require.NoError(t, err)
	require.Empty(t, result.Error)
	require.Equal(t, "https://example.com/news", service.navigated.URL)
	var tab browserdomain.Tab
	require.NoError(t, json.Unmarshal([]byte(result.Output), &tab))
	require.Equal(t, "tab-1", tab.ID)
}

func TestDefinition_PreservesPersonalBrowserApprovalWarning(t *testing.T) {
	service := &browserServiceStub{permissionInputs: []permissions.EvaluationInput{{
		ApprovalReason: "Attached browsers do not use Morph's managed egress proxy. " +
			"Personal browser attachment exposes signed-in sessions, cookies, and page data.",
		ApprovalSummary: "Attach to signed-in browser profile personal",
		Operation: permissions.Operation{
			Tool: "browser", Resource: permissions.ResourceBrowser, Action: permissions.ActionConnect,
			Effects: []permissions.Effect{
				permissions.EffectNetwork,
				permissions.EffectCredentialBearing,
				permissions.EffectExternalSystem,
			},
		},
	}}}
	definition := Definition(&toolmocks.Runtime{BrowserServiceValue: service, BrowserServiceOK: true})

	inputs, err := definition.ResolvePermission(context.Background(), tools.Call{
		Name: toolName, Input: `{"action":"start","profile":"personal"}`,
	})
	require.NoError(t, err)
	require.Len(t, inputs, 1)
	require.Contains(t, inputs[0].ApprovalReason, "do not use Morph's managed egress proxy")
	require.Contains(t, inputs[0].ApprovalReason, "signed-in sessions")
	require.Contains(t, inputs[0].Operation.Effects, permissions.EffectCredentialBearing)
}

func TestDefinition_UsesPresetAndRulePolicyForUnattendedSurfaces(t *testing.T) {
	operation := permissions.Operation{
		Tool: "browser", Resource: permissions.ResourceBrowser, Action: permissions.ActionRead,
		Effects: []permissions.Effect{permissions.EffectRead}, Target: "status",
	}
	call := tools.Call{Name: toolName, Input: `{"action":"status"}`}
	tests := []struct {
		name    string
		policy  permissions.Policy
		context permissions.AuthorizationContext
		allowed bool
	}{
		{
			name:   "approve denies automation by default",
			policy: permissions.Policy{Preset: permissions.PresetApproveForMe},
			context: permissions.AuthorizationContext{
				Actor:       permissions.Actor{Kind: permissions.ActorAutomation, ID: "auto_news"},
				SurfaceKind: permissions.SurfaceKindAutomation, Surface: permissions.SurfaceAutomation,
			},
		},
		{
			name:   "approve denies gateway by default",
			policy: permissions.Policy{Preset: permissions.PresetApproveForMe},
			context: permissions.AuthorizationContext{
				Actor:       permissions.Actor{Kind: permissions.ActorGatewayUser, ID: "user"},
				SurfaceKind: permissions.SurfaceKindGateway, Surface: permissions.SurfaceSlack,
			},
		},
		{
			name: "narrow rule enhances approve for one automation",
			policy: permissions.Policy{Preset: permissions.PresetApproveForMe, Rules: []permissions.Rule{{
				Name: "allow automation browser status", ActorKinds: []permissions.ActorKind{permissions.ActorAutomation},
				ActorIDs: []string{"auto_news"}, Surfaces: []permissions.Surface{permissions.SurfaceAutomation},
				Tools: []string{"browser"}, Resources: []permissions.Resource{permissions.ResourceBrowser},
				Actions: []permissions.Action{permissions.ActionRead}, Decision: permissions.DecisionAllow,
			}}},
			context: permissions.AuthorizationContext{
				Actor:       permissions.Actor{Kind: permissions.ActorAutomation, ID: "auto_news"},
				SurfaceKind: permissions.SurfaceKindAutomation, Surface: permissions.SurfaceAutomation,
			},
			allowed: true,
		},
		{
			name:   "full access permits automation",
			policy: permissions.Policy{Preset: permissions.PresetFullAccess},
			context: permissions.AuthorizationContext{
				Actor:       permissions.Actor{Kind: permissions.ActorAutomation, ID: "auto_news"},
				SurfaceKind: permissions.SurfaceKindAutomation, Surface: permissions.SurfaceAutomation,
			},
			allowed: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &browserServiceStub{operations: []permissions.Operation{operation}}
			registry := tools.NewDefaultRegistry(tools.RegistryOptions{PermissionPolicy: test.policy})
			require.NoError(t, registry.Register(Definition(&toolmocks.Runtime{
				BrowserServiceValue: service, BrowserServiceOK: true,
			})))
			ctx := permissions.WithContext(context.Background(), test.context)

			result, err := registry.Invoke(ctx, call)

			require.NoError(t, err)
			if test.allowed {
				require.Empty(t, result.Error)
				require.Equal(t, 1, service.statusCalls)
				return
			}
			require.Contains(t, result.Error, permissions.ErrorCodeDenied)
			require.Zero(t, service.statusCalls)
		})
	}
}

func TestDefinition_StatusDoesNotExposeBackendFailureDetails(t *testing.T) {
	service := &browserServiceStub{status: browserdomain.Status{Sessions: []browserdomain.Session{{
		ID: "browser-1", Error: "secret endpoint failed",
	}}, Profiles: []browserdomain.Profile{{Name: "isolated", Available: true}}}}
	runtime := &toolmocks.Runtime{BrowserServiceValue: service, BrowserServiceOK: true}
	definition := Definition(runtime)

	result, err := definition.Handler.Invoke(context.Background(), tools.Call{
		Name: toolName, Input: `{"action":"status"}`,
	})
	require.NoError(t, err)
	require.NotContains(t, result.Output, "secret endpoint")
	require.Equal(t, "secret endpoint failed", service.status.Sessions[0].Error)

	result, err = definition.Handler.Invoke(context.Background(), tools.Call{
		Name: toolName, Input: `{"action":"profiles"}`,
	})
	require.NoError(t, err)
	require.JSONEq(t, `[{"name":"isolated","mode":"","default":false,"available":true}]`, result.Output)
}

func TestSafeBrowserResultsRemoveURLSecrets(t *testing.T) {
	tab := getSafeTab(browserdomain.Tab{URL: "https://user:password@example.com/private?token=secret#fragment"})
	require.Equal(t, "https://example.com/private", tab.URL)
	require.Equal(t, "about:blank", getSafeURL("about:blank"))
	require.Empty(t, getSafeURL("not a URL"))

	tabs := getSafeTabs([]browserdomain.Tab{{URL: "https://example.com/path?secret=yes"}})
	require.Equal(t, "https://example.com/path", tabs[0].URL)
	snapshot := getSafeSnapshot(browserdomain.Snapshot{URL: "https://example.com/page?secret=yes"})
	require.Equal(t, "https://example.com/page", snapshot.URL)
	session := getSafeSession(browserdomain.Session{
		Error: "secret backend detail", Warning: "Personal browser attachment is unsafe.",
	})
	require.Empty(t, session.Error)
	require.Equal(t, "Personal browser attachment is unsafe.", session.Warning)
}

func TestDefinition_DispatchesEverySupportedAction(t *testing.T) {
	tests := []struct {
		action browserdomain.Action
		input  string
	}{
		{action: browserdomain.ActionStart, input: `{"action":"start","profile":"isolated"}`},
		{action: browserdomain.ActionStop, input: `{"action":"stop","session_id":"session"}`},
		{action: browserdomain.ActionTabs, input: `{"action":"tabs","session_id":"session"}`},
		{action: browserdomain.ActionOpen, input: `{"action":"open","session_id":"session","url":"https://example.com"}`},
		{action: browserdomain.ActionFocus, input: `{"action":"focus","session_id":"session","tab_id":"tab"}`},
		{action: browserdomain.ActionClose, input: `{"action":"close","session_id":"session","tab_id":"tab"}`},
		{action: browserdomain.ActionReload, input: `{"action":"reload","session_id":"session","tab_id":"tab"}`},
		{action: browserdomain.ActionSnapshot, input: `{"action":"snapshot","session_id":"session","tab_id":"tab"}`},
		{action: browserdomain.ActionClick, input: `{"action":"click","session_id":"session","tab_id":"tab","ref":"g1e1"}`},
		{action: browserdomain.ActionType, input: `{"action":"type","session_id":"session","tab_id":"tab","ref":"g1e1","text":"hello"}`},
		{action: browserdomain.ActionPress, input: `{"action":"press","session_id":"session","tab_id":"tab","key":"Enter"}`},
		{action: browserdomain.ActionScroll, input: `{"action":"scroll","session_id":"session","tab_id":"tab","y":10}`},
		{action: browserdomain.ActionSelect, input: `{"action":"select","session_id":"session","tab_id":"tab","ref":"g1e1","value":"one"}`},
		{action: browserdomain.ActionWait, input: `{"action":"wait","session_id":"session","tab_id":"tab","condition":"load"}`},
		{action: browserdomain.ActionBack, input: `{"action":"back","session_id":"session","tab_id":"tab"}`},
		{action: browserdomain.ActionForward, input: `{"action":"forward","session_id":"session","tab_id":"tab"}`},
	}
	service := &browserServiceStub{}
	definition := Definition(&toolmocks.Runtime{BrowserServiceValue: service, BrowserServiceOK: true})
	for _, test := range tests {
		t.Run(string(test.action), func(t *testing.T) {
			service.dispatchedAction = ""
			service.dispatchedRequest = browserdomain.ActionRequest{}
			result, err := definition.Handler.Invoke(context.Background(), tools.Call{Input: test.input})
			require.NoError(t, err)
			require.Empty(t, result.Error)
			require.Equal(t, test.action, service.dispatchedAction)
			switch test.action {
			case browserdomain.ActionStart:
				require.Equal(t, "isolated", service.dispatchedRequest.Profile)
			case browserdomain.ActionType:
				require.Equal(t, "g1e1", service.dispatchedRequest.Ref)
				require.Equal(t, "hello", service.dispatchedRequest.Text)
			case browserdomain.ActionPress:
				require.Equal(t, "Enter", service.dispatchedRequest.Key)
			case browserdomain.ActionScroll:
				require.Equal(t, int64(10), service.dispatchedRequest.Y)
			case browserdomain.ActionSelect:
				require.Equal(t, "g1e1", service.dispatchedRequest.Ref)
				require.Equal(t, "one", service.dispatchedRequest.Value)
			case browserdomain.ActionWait:
				require.Equal(t, browserdomain.WaitLoad, service.dispatchedRequest.Condition)
			default:
				require.Equal(t, "session", service.dispatchedRequest.SessionID)
			}
		})
	}
}

func TestDefinition_ReportsRuntimeAndBrowserFailures(t *testing.T) {
	definition := Definition(nil)
	_, err := definition.ResolvePermission(context.Background(), tools.Call{Input: `{"action":"status"}`})
	require.EqualError(t, err, "browser runtime is unavailable")

	definition = Definition(&toolmocks.Runtime{})
	_, err = definition.ResolvePermission(context.Background(), tools.Call{Input: `{"action":"status"}`})
	require.EqualError(t, err, "browser service is unavailable")

	runtimeErr := errors.New("runtime failed")
	definition = Definition(&toolmocks.Runtime{BrowserServiceErr: runtimeErr})
	_, err = definition.ResolvePermission(context.Background(), tools.Call{Input: `{"action":"status"}`})
	require.ErrorIs(t, err, runtimeErr)
	_, err = definition.Handler.Invoke(context.Background(), tools.Call{Input: `{"action":"status"}`})
	require.ErrorIs(t, err, runtimeErr)

	service := &browserServiceStub{}
	definition = Definition(&toolmocks.Runtime{BrowserServiceValue: service, BrowserServiceOK: true})
	_, err = definition.ResolvePermission(context.Background(), tools.Call{Input: `{"action":`})
	require.Error(t, err)
	_, err = definition.Handler.Invoke(context.Background(), tools.Call{Input: `{"action":`})
	require.Error(t, err)
	service.resolveErr = errors.New("resolution failed")
	_, err = definition.ResolvePermission(context.Background(), tools.Call{Input: `{"action":"status"}`})
	require.EqualError(t, err, "resolution failed")

	service = &browserServiceStub{dispatchErr: &browserdomain.Error{
		Code: browserdomain.ErrorTimeout, Err: errors.New("timeout at /private/secret?token=value"), Retryable: true,
	}}
	definition = Definition(&toolmocks.Runtime{BrowserServiceValue: service, BrowserServiceOK: true})
	result, err := definition.Handler.Invoke(context.Background(), tools.Call{
		Input: `{"action":"stop","session_id":"session"}`,
	})
	require.NoError(t, err)
	require.Contains(t, result.Error, `"code":"browser_timeout"`)
	require.Contains(t, result.Error, `"retryable":true`)
	require.NotContains(t, result.Error, "/private/secret")
	require.NotContains(t, result.Error, "token=value")

	service.dispatchErr = &permissions.DecisionError{
		Code:       permissions.ErrorCodeDenied,
		Evaluation: permissions.Evaluation{Decision: permissions.DecisionDeny, Reason: "blocked"},
	}
	result, err = definition.Handler.Invoke(context.Background(), tools.Call{
		Input: `{"action":"stop","session_id":"session"}`,
	})
	require.NoError(t, err)
	require.Contains(t, result.Error, `"code":"permission_denied"`)

	service.dispatchErr = errors.New("backend broke at /private/secret?token=value")
	result, err = definition.Handler.Invoke(context.Background(), tools.Call{
		Input: `{"action":"stop","session_id":"session"}`,
	})
	require.NoError(t, err)
	require.Contains(t, result.Error, `"code":"browser_failed"`)
	require.NotContains(t, result.Error, "/private/secret")
	require.NotContains(t, result.Error, "token=value")

	for _, code := range []browserdomain.ErrorCode{
		browserdomain.ErrorInvalidRequest, browserdomain.ErrorUnavailable, browserdomain.ErrorStartFailed,
		browserdomain.ErrorHealthFailed, browserdomain.ErrorNotFound, browserdomain.ErrorOwnership,
		browserdomain.ErrorClosed, browserdomain.ErrorNotReady, browserdomain.ErrorStaleReference,
		browserdomain.ErrorTimeout, browserdomain.ErrorCancelled, "unknown",
	} {
		require.NotEmpty(t, getSafeBrowserErrorMessage(code))
	}

	_, err = dispatch(context.Background(), service, request{Action: "unsupported"})
	require.EqualError(t, err, "browser action is not supported")
	require.Empty(t, getSafeURL("%"))
}
