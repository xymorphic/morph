package permissions

import (
	"errors"
	"slices"

	commandplan "github.com/wandxy/morph/internal/command"
	"github.com/wandxy/morph/pkg/str"
)

type ActorKind string

const (
	ActorUnknown     ActorKind = "unknown"
	ActorLocalOwner  ActorKind = "local_owner"
	ActorGatewayUser ActorKind = "gateway_user"
	ActorAutomation  ActorKind = "automation"
	ActorSubagent    ActorKind = "subagent"
	ActorACPClient   ActorKind = "acp_client"
	ActorRPCClient   ActorKind = "rpc_client"
)

type Surface string

type SurfaceKind string

const (
	SurfaceKindUnknown    SurfaceKind = "unknown"
	SurfaceKindLocal      SurfaceKind = "local"
	SurfaceKindGateway    SurfaceKind = "gateway"
	SurfaceKindAutomation SurfaceKind = "automation"
	SurfaceKindRPC        SurfaceKind = "rpc"
	SurfaceKindACP        SurfaceKind = "acp"
)

const (
	SurfaceUnknown    Surface = "unknown"
	SurfaceCLI        Surface = "cli"
	SurfaceTUI        Surface = "tui"
	SurfaceTelegram   Surface = "telegram"
	SurfaceSlack      Surface = "slack"
	SurfaceHTTP       Surface = "http"
	SurfaceAutomation Surface = "automation"
	SurfaceRPC        Surface = "rpc"
	SurfaceACP        Surface = "acp"
)

type Resource string

const (
	ResourceUnknown       Resource = "unknown"
	ResourceFile          Resource = "file"
	ResourceProcess       Resource = "process"
	ResourceNetwork       Resource = "network"
	ResourceMemory        Resource = "memory"
	ResourceSession       Resource = "session"
	ResourceAutomation    Resource = "automation"
	ResourceGateway       Resource = "gateway"
	ResourceConfiguration Resource = "configuration"
	ResourceModel         Resource = "model"
	ResourceDaemon        Resource = "daemon"
	ResourcePlan          Resource = "plan"
	ResourceClock         Resource = "clock"
	ResourceMCP           Resource = "mcp"
	ResourceBrowser       Resource = "browser"
	ResourceDelegation    Resource = "delegation"
	ResourceExecuteCode   Resource = "execute_code"
	ResourceACP           Resource = "acp"
)

type Action string

const (
	ActionUnknown Action = "unknown"
	ActionRead    Action = "read"
	ActionSearch  Action = "search"
	ActionList    Action = "list"
	ActionCreate  Action = "create"
	ActionUpdate  Action = "update"
	ActionDelete  Action = "delete"
	ActionExecute Action = "execute"
	ActionStart   Action = "start"
	ActionStop    Action = "stop"
	ActionTrigger Action = "trigger"
	ActionManage  Action = "manage"
	ActionConnect Action = "connect"
)

type Effect string

const (
	EffectRead              Effect = "read"
	EffectWrite             Effect = "write"
	EffectExecution         Effect = "execution"
	EffectNetwork           Effect = "network"
	EffectDestructive       Effect = "destructive"
	EffectCredentialBearing Effect = "credential_bearing"
	EffectExternalSystem    Effect = "external_system"
	EffectPrivilegeChanging Effect = "privilege_changing"
	EffectIndirectExecution Effect = "indirect_execution"
)

type Decision string

const (
	DecisionAllow Decision = "allow"
	DecisionAsk   Decision = "ask"
	DecisionDeny  Decision = "deny"
)

type TargetScope string

const (
	TargetScopeUnknown   TargetScope = "unknown"
	TargetScopeWorkspace TargetScope = "workspace"
	TargetScopeExternal  TargetScope = "external"
)

type Actor struct {
	Kind ActorKind
	ID   string
}

type AuthorizationContext struct {
	Actor           Actor
	SurfaceKind     SurfaceKind
	Surface         Surface
	Profile         string
	SessionID       string
	RunID           string
	ParentActorKind ActorKind
	ParentActorID   string
	ParentRunID     string
	Scope           PermissionScope
}

func (c AuthorizationContext) Normalize() (AuthorizationContext, error) {
	c.Actor.Kind = ActorKind(str.String(c.Actor.Kind).Normalized())
	c.Actor.ID = str.String(c.Actor.ID).Trim()
	c.SurfaceKind = SurfaceKind(str.String(c.SurfaceKind).Normalized())
	c.Surface = Surface(str.String(c.Surface).Normalized())
	if c.SurfaceKind == "" {
		c.SurfaceKind = getSurfaceKind(c.Surface)
	}
	c.Profile = str.String(c.Profile).Trim()
	c.SessionID = str.String(c.SessionID).Trim()
	c.RunID = str.String(c.RunID).Trim()
	c.ParentActorKind = ActorKind(str.String(c.ParentActorKind).Normalized())
	c.ParentActorID = str.String(c.ParentActorID).Trim()
	c.ParentRunID = str.String(c.ParentRunID).Trim()
	scope, err := c.Scope.Normalize()
	if err != nil {
		return AuthorizationContext{}, err
	}
	c.Scope = scope

	if !isValidActorKind(c.Actor.Kind, false) {
		return AuthorizationContext{}, errors.New("permission actor kind is invalid")
	}
	if !isValidSurfaceKind(c.SurfaceKind, false) {
		return AuthorizationContext{}, errors.New("permission surface kind is invalid")
	}
	if !isValidSurface(c.Surface, false) {
		return AuthorizationContext{}, errors.New("permission surface is invalid")
	}
	knownKind := getSurfaceKind(c.Surface)
	if knownKind != SurfaceKindUnknown && knownKind != c.SurfaceKind {
		return AuthorizationContext{}, errors.New("permission surface does not match its kind")
	}
	if c.ParentActorKind != "" && !isValidActorKind(c.ParentActorKind, false) {
		return AuthorizationContext{}, errors.New("permission parent actor kind is invalid")
	}
	if c.ParentActorKind == "" && (c.ParentActorID != "" || c.ParentRunID != "") {
		return AuthorizationContext{}, errors.New("permission parent actor kind is required for parent identity")
	}

	return c, nil
}

type Operation struct {
	Tool          string
	Resource      Resource
	Action        Action
	Effects       []Effect
	Target        string
	TargetScope   TargetScope
	Network       *NetworkTarget
	Command       *commandplan.Target
	OwnerID       string
	OwnerRequired bool
}

func (o Operation) Normalize() (Operation, error) {
	o.Tool = str.String(o.Tool).Trim()
	o.Resource = Resource(str.String(o.Resource).Normalized())
	o.Action = Action(str.String(o.Action).Normalized())
	o.Target = str.String(o.Target).Trim()
	o.TargetScope = TargetScope(str.String(o.TargetScope).Normalized())
	if o.Network != nil {
		if o.Target != "" {
			return Operation{}, errors.New("permission operation cannot combine a network target and raw target")
		}
		if o.Resource != ResourceNetwork {
			return Operation{}, errors.New("permission network target requires the network resource")
		}
		network, err := o.Network.Normalize()
		if err != nil {
			return Operation{}, err
		}
		o.Network = &network
	}
	if o.Command != nil {
		if o.Target != "" {
			return Operation{}, errors.New("permission operation cannot combine a command target and raw target")
		}
		if o.Resource != ResourceProcess {
			return Operation{}, errors.New("permission command target requires the process resource")
		}
		if o.Action != ActionExecute && o.Action != ActionStart {
			return Operation{}, errors.New("permission command target requires execute or start")
		}
		command, err := o.Command.Normalize()
		if err != nil {
			return Operation{}, err
		}
		o.Command = &command
	}
	if o.Network != nil && o.Command != nil {
		return Operation{}, errors.New("permission operation cannot combine network and command targets")
	}
	o.OwnerID = str.String(o.OwnerID).Trim()
	o.Effects = normalizeEffects(o.Effects)

	if !isValidResource(o.Resource, false) {
		return Operation{}, errors.New("permission resource is invalid")
	}
	if !isValidAction(o.Action, false) {
		return Operation{}, errors.New("permission action is invalid")
	}
	for _, effect := range o.Effects {
		if !isValidEffect(effect) {
			return Operation{}, errors.New("permission effect is invalid")
		}
	}
	if !isValidTargetScope(o.TargetScope, true) {
		return Operation{}, errors.New("permission target scope is invalid")
	}

	return o, nil
}

func (o Operation) IsZero() bool {
	return str.String(o.Tool).Trim() == "" && o.Resource == "" && o.Action == "" && len(o.Effects) == 0 &&
		str.String(o.Target).Trim() == "" && (o.TargetScope == "" || o.TargetScope == TargetScopeUnknown) &&
		o.Network == nil &&
		o.Command == nil &&
		str.String(o.OwnerID).Trim() == "" && !o.OwnerRequired
}

func normalizeEffects(values []Effect) []Effect {
	result := make([]Effect, 0, len(values))
	for _, value := range values {
		effect := Effect(str.String(value).Normalized())
		if effect == "" || slices.Contains(result, effect) {
			continue
		}
		result = append(result, effect)
	}
	slices.Sort(result)
	return result
}

func isValidActorKind(value ActorKind, allowUnknown bool) bool {
	valid := []ActorKind{
		ActorLocalOwner,
		ActorGatewayUser,
		ActorAutomation,
		ActorSubagent,
		ActorACPClient,
		ActorRPCClient}
	return slices.Contains(valid, value) || allowUnknown && value == ActorUnknown
}

func isValidSurface(value Surface, allowUnknown bool) bool {
	value = Surface(str.String(value).Normalized())
	return value != "" && (value != SurfaceUnknown || allowUnknown)
}

func isValidSurfaceKind(value SurfaceKind, allowUnknown bool) bool {
	valid := []SurfaceKind{
		SurfaceKindLocal,
		SurfaceKindGateway,
		SurfaceKindAutomation,
		SurfaceKindRPC,
		SurfaceKindACP}
	return slices.Contains(valid, value) || allowUnknown && value == SurfaceKindUnknown
}

func isValidTargetScope(value TargetScope, allowUnknown bool) bool {
	valid := []TargetScope{TargetScopeWorkspace, TargetScopeExternal}
	return slices.Contains(valid, value) || allowUnknown && (value == "" || value == TargetScopeUnknown)
}

func getSurfaceKind(surface Surface) SurfaceKind {
	switch surface {
	case SurfaceCLI, SurfaceTUI:
		return SurfaceKindLocal
	case SurfaceTelegram, SurfaceSlack, SurfaceHTTP:
		return SurfaceKindGateway
	case SurfaceAutomation:
		return SurfaceKindAutomation
	case SurfaceRPC:
		return SurfaceKindRPC
	case SurfaceACP:
		return SurfaceKindACP
	default:
		return SurfaceKindUnknown
	}
}

func isValidResource(value Resource, allowUnknown bool) bool {
	valid := []Resource{
		ResourceFile,
		ResourceProcess,
		ResourceNetwork,
		ResourceMemory,
		ResourceSession,
		ResourceAutomation,
		ResourceGateway,
		ResourceConfiguration,
		ResourceModel,
		ResourceDaemon,
		ResourcePlan,
		ResourceClock,
		ResourceMCP,
		ResourceBrowser,
		ResourceDelegation,
		ResourceExecuteCode,
		ResourceACP}
	return slices.Contains(valid, value) || allowUnknown && value == ResourceUnknown
}

func isValidAction(value Action, allowUnknown bool) bool {
	valid := []Action{
		ActionRead,
		ActionSearch,
		ActionList,
		ActionCreate,
		ActionUpdate,
		ActionDelete,
		ActionExecute,
		ActionStart,
		ActionStop,
		ActionTrigger,
		ActionManage,
		ActionConnect}
	return slices.Contains(valid, value) || allowUnknown && value == ActionUnknown
}

func isValidEffect(value Effect) bool {
	return slices.Contains([]Effect{
		EffectRead,
		EffectWrite,
		EffectExecution,
		EffectNetwork,
		EffectDestructive,
		EffectCredentialBearing,
		EffectExternalSystem,
		EffectPrivilegeChanging,
		EffectIndirectExecution},
		value)
}

func isValidDecision(value Decision) bool {
	return slices.Contains([]Decision{DecisionAllow, DecisionAsk, DecisionDeny}, value)
}
