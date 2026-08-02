package permissions

import (
	"errors"
	"net/url"
	"path"
	"strings"

	"github.com/xymorphic/morph/pkg/str"
)

type MCPRequest struct {
	ServerID  string
	Transport string
	Tool      string
	Target    string
}

func (r MCPRequest) Operation() (Operation, error) {
	r.ServerID = str.String(r.ServerID).Trim()
	r.Transport = str.String(r.Transport).Normalized()
	r.Tool = str.String(r.Tool).Trim()
	r.Target = normalizeExtensionTarget(r.Target)
	if r.ServerID == "" || r.Transport == "" || r.Tool == "" {
		return Operation{}, errors.New("MCP server, transport, and tool are required")
	}
	if r.Transport != "stdio" && r.Transport != "http" && r.Transport != "sse" && r.Transport != "streamable_http" {
		return Operation{}, errors.New("MCP transport must be one of: stdio, http, sse, streamable_http")
	}
	target := getStructuredTarget(url.Values{
		"server": {r.ServerID}, "transport": {r.Transport}, "tool": {r.Tool}, "target": {r.Target},
	})

	return Operation{
		Tool:     "mcp:" + r.ServerID + ":" + r.Tool,
		Resource: ResourceMCP,
		Action:   ActionExecute,
		Effects:  []Effect{EffectExecution, EffectExternalSystem, EffectNetwork},
		Target:   target,
	}.Normalize()
}

type BrowserRequest struct {
	Profile           string
	ProfileMode       string
	AttachmentScope   string
	AttachmentID      string
	TabTarget         string
	Action            string
	Network           *NetworkTarget
	FileTarget        string
	TargetScope       TargetScope
	OwnerID           string
	Personal          bool
	CredentialBearing bool
}

type ExtensionRequest struct {
	Tool     string
	Resource Resource
	Action   Action
	Effects  []Effect
	Target   string
}

func (r ExtensionRequest) Operation(authorization AuthorizationContext) (Operation, error) {
	authorization, err := authorization.Normalize()
	if err != nil {
		return Operation{}, err
	}
	if authorization.Actor.ID == "" || !authorization.Scope.Restricted {
		return Operation{}, errors.New("extension operation requires an authenticated actor and constrained scope")
	}
	if r.Resource != ResourceExecuteCode && r.Resource != ResourceACP {
		return Operation{}, errors.New("extension resource must be execute_code or acp")
	}
	if r.Resource == ResourceExecuteCode && authorization.Actor.Kind != ActorRPCClient {
		return Operation{}, errors.New("execute-code operation requires an RPC client actor")
	}
	if r.Resource == ResourceACP && authorization.Actor.Kind != ActorACPClient {
		return Operation{}, errors.New("ACP operation requires an ACP client actor")
	}
	operation, err := (Operation{
		Tool: str.String(r.Tool).Trim(), Resource: r.Resource, Action: r.Action,
		Effects: append([]Effect(nil), r.Effects...), Target: normalizeExtensionTarget(r.Target),
	}).Normalize()
	if err != nil {
		return Operation{}, err
	}
	if !authorization.Scope.Allows(operation) {
		return Operation{}, errors.New("extension operation exceeds its constrained scope")
	}

	return operation, nil
}

func (r BrowserRequest) Operations() ([]Operation, error) {
	r.Profile = str.String(r.Profile).Trim()
	r.TabTarget = str.String(r.TabTarget).Trim()
	if r.TabTarget != "" {
		r.TabTarget = normalizeExtensionTarget(r.TabTarget)
	}
	r.Action = str.String(r.Action).Normalized()
	r.FileTarget = str.String(r.FileTarget).Trim()
	if r.FileTarget != "" {
		r.FileTarget = normalizeExtensionTarget(r.FileTarget)
	}
	r.OwnerID = str.String(r.OwnerID).Trim()
	if r.Profile == "" || r.Action == "" {
		return nil, errors.New("browser profile and action are required")
	}
	targetValues := url.Values{
		"profile": {r.Profile}, "tab": {r.TabTarget},
		"action": {r.Action}, "mode": {getBrowserMode(r.Personal)},
	}
	if r.ProfileMode != "" {
		targetValues.Set("profile_mode", r.ProfileMode)
	}
	if r.AttachmentScope != "" {
		targetValues.Set("attachment_scope", r.AttachmentScope)
	}
	if r.AttachmentID != "" {
		targetValues.Set("attachment_id", r.AttachmentID)
	}
	target := getStructuredTarget(targetValues)
	action, effects, ownerRequired, err := getBrowserPermission(r.Action)
	if err != nil {
		return nil, err
	}
	if r.Personal || r.CredentialBearing {
		effects = append(effects, EffectCredentialBearing)
	}
	operation, err := (Operation{
		Tool:          "browser",
		Resource:      ResourceBrowser,
		Action:        action,
		Effects:       effects,
		Target:        target,
		OwnerID:       r.OwnerID,
		OwnerRequired: ownerRequired,
	}).Normalize()
	if err != nil {
		return nil, err
	}
	operations := []Operation{operation}
	if r.Network != nil {
		if !isBrowserNetworkClass(r.Action, r.Network.RequestClass) {
			return nil, errors.New("browser network target request class does not match the action")
		}
		networkAction := ActionRead
		networkEffects := []Effect{EffectRead, EffectNetwork, EffectExternalSystem}
		if r.Network.RequestClass == NetworkRequestWebSocket {
			networkAction = ActionConnect
			networkEffects = []Effect{EffectWrite, EffectNetwork, EffectExternalSystem}
		} else if r.Action == "download" {
			networkAction = ActionCreate
		} else if r.Action == "connect" {
			networkAction = ActionConnect
		} else if r.Network.Method != "GET" && r.Network.Method != "HEAD" && r.Network.Method != "OPTIONS" {
			networkAction = ActionUpdate
			networkEffects = []Effect{EffectWrite, EffectNetwork, EffectExternalSystem}
		}
		if r.Personal || r.CredentialBearing {
			networkEffects = append(networkEffects, EffectCredentialBearing)
		}
		networkOperation, normalizeErr := (Operation{
			Tool: "browser", Resource: ResourceNetwork, Action: networkAction,
			Effects: networkEffects, Network: r.Network,
			OwnerID: r.OwnerID, OwnerRequired: ownerRequired,
		}).Normalize()
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		operations = append(operations, networkOperation)
	}
	if requiresBrowserNetwork(r.Action) && r.Network == nil {
		return nil, errors.New("browser action requires a structured network target")
	}
	if r.Action == "upload" && r.FileTarget == "" {
		return nil, errors.New("browser upload requires a file target")
	}
	if r.FileTarget != "" {
		fileAction := ActionRead
		fileEffects := []Effect{EffectRead}
		if r.Action == "download" || r.Action == "screenshot" || r.Action == "pdf" {
			fileAction = ActionCreate
			fileEffects = []Effect{EffectWrite}
		}
		if r.Personal || r.CredentialBearing {
			fileEffects = append(fileEffects, EffectCredentialBearing)
		}
		fileOperation, normalizeErr := (Operation{
			Tool: "browser", Resource: ResourceFile, Action: fileAction, Effects: fileEffects,
			Target: r.FileTarget, TargetScope: r.TargetScope,
		}).Normalize()
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		operations = append(operations, fileOperation)
	}

	return operations, nil
}

func getBrowserPermission(action string) (Action, []Effect, bool, error) {
	switch action {
	case "status", "snapshot", "console", "wait":
		return ActionRead, []Effect{EffectRead}, false, nil
	case "profiles", "tabs":
		return ActionList, []Effect{EffectRead}, false, nil
	case "focus", "scroll":
		return ActionUpdate, []Effect{EffectWrite}, false, nil
	case "start":
		return ActionStart, []Effect{EffectExecution}, true, nil
	case "stop":
		return ActionStop, []Effect{EffectWrite, EffectExecution, EffectDestructive}, true, nil
	case "open", "navigate", "back", "forward", "reload":
		return ActionUpdate, []Effect{EffectRead, EffectWrite, EffectNetwork, EffectExternalSystem}, false, nil
	case "click", "type", "press", "select", "accept_dialog", "dismiss_dialog":
		return ActionUpdate, []Effect{EffectWrite, EffectNetwork, EffectExternalSystem}, false, nil
	case "close":
		return ActionDelete, []Effect{EffectWrite, EffectDestructive}, true, nil
	case "screenshot", "pdf", "download":
		return ActionCreate, []Effect{EffectRead, EffectWrite}, false, nil
	case "upload":
		return ActionUpdate, []Effect{EffectRead, EffectWrite, EffectExternalSystem}, false, nil
	case "connect":
		return ActionConnect, []Effect{EffectNetwork, EffectExternalSystem}, true, nil
	default:
		return ActionUnknown, nil, false, errors.New("browser action is invalid")
	}
}

func requiresBrowserNetwork(action string) bool {
	return action == "open" || action == "navigate" || action == "download" || action == "connect"
}

func isBrowserNetworkClass(action string, requestClass NetworkRequestClass) bool {
	switch action {
	case "open", "navigate", "reload", "back", "forward":
		return requestClass == NetworkRequestNavigation || requestClass == NetworkRequestRedirect ||
			requestClass == NetworkRequestSubresource || requestClass == NetworkRequestWebSocket
	case "download":
		return requestClass == NetworkRequestDownload || requestClass == NetworkRequestRedirect
	case "connect":
		return requestClass == NetworkRequestCDP
	default:
		return true
	}
}

func NewConstrainedExtensionAuthorization(
	kind ActorKind,
	id string,
	surface Surface,
	profile string,
	sessionID string,
	runID string,
	scope PermissionScope,
) (AuthorizationContext, error) {
	if kind != ActorACPClient && kind != ActorRPCClient {
		return AuthorizationContext{}, errors.New("extension actor must be an ACP or RPC client")
	}
	if kind == ActorACPClient && surface != SurfaceACP || kind == ActorRPCClient && surface != SurfaceRPC {
		return AuthorizationContext{}, errors.New("extension actor does not match its surface")
	}

	id = str.String(id).Trim()
	if id == "" {
		return AuthorizationContext{}, errors.New("extension actor id is required")
	}
	scope, err := scope.Normalize()
	if err != nil {
		return AuthorizationContext{}, err
	}
	if !scope.Restricted {
		return AuthorizationContext{}, errors.New("extension permission scope must be restricted")
	}

	return AuthorizationContext{
		Actor:     Actor{Kind: kind, ID: id},
		Surface:   surface,
		Profile:   str.String(profile).Trim(),
		SessionID: str.String(sessionID).Trim(),
		RunID:     str.String(runID).Trim(),
		Scope:     scope,
	}.Normalize()
}

func normalizeExtensionTarget(raw string) string {
	raw = str.String(raw).Trim()
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return path.Clean(strings.ReplaceAll(raw, "\\", "/"))
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Fragment = ""
	parsed.RawQuery = parsed.Query().Encode()
	parsed.Path = path.Clean(parsed.Path)
	if parsed.Path == "." || parsed.Path == "/" {
		parsed.Path = ""
	}

	return parsed.String()
}

func getStructuredTarget(values url.Values) string {
	return values.Encode()
}

func getBrowserMode(personal bool) string {
	if personal {
		return "personal"
	}

	return "isolated"
}
