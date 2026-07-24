package permissions

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	commandplan "github.com/wandxy/morph/internal/command"
	"github.com/wandxy/morph/pkg/str"
)

const (
	ReasonHardDeny           = "hard_deny"
	ReasonRuleMatched        = "rule_matched"
	ReasonSurfaceDefault     = "surface_default"
	ReasonSurfaceKindDefault = "surface_kind_default"
	ReasonPolicyDefault      = "policy_default"
	ReasonApprovalRequired   = "approval_required"
	ReasonOwnerRequired      = "owner_required"
	ReasonFullAccess         = "full_access"
	ReasonScopeExceeded      = "scope_exceeded"
	ReasonBackgroundRule     = "background_rule_required"
)

type Policy struct {
	Preset              Preset                   `yaml:"preset"`
	Default             Decision                 `yaml:"default"`
	RequestRetention    time.Duration            `yaml:"requestRetention"`
	GrantRetention      time.Duration            `yaml:"grantRetention"`
	CleanupInterval     time.Duration            `yaml:"cleanupInterval"`
	CleanupBatchSize    int                      `yaml:"cleanupBatchSize"`
	ApprovalRateLimit   int                      `yaml:"approvalRateLimit"`
	ApprovalRateWindow  time.Duration            `yaml:"approvalRateWindow"`
	SurfaceKindDefaults map[SurfaceKind]Decision `yaml:"surfaceKinds"`
	SurfaceDefaults     map[Surface]Decision     `yaml:"surfaces"`
	Rules               []Rule                   `yaml:"rules"`
	presetRules         []Rule
}

type Rule struct {
	Name             string                 `yaml:"name"`
	Profiles         []string               `yaml:"profiles"`
	ActorKinds       []ActorKind            `yaml:"actors"`
	ActorIDs         []string               `yaml:"actorIds"`
	ParentActorKinds []ActorKind            `yaml:"parentActors"`
	SurfaceKinds     []SurfaceKind          `yaml:"surfaceKinds"`
	Surfaces         []Surface              `yaml:"surfaces"`
	Tools            []string               `yaml:"tools"`
	Resources        []Resource             `yaml:"resources"`
	Actions          []Action               `yaml:"actions"`
	Effects          []Effect               `yaml:"effects"`
	TargetScopes     []TargetScope          `yaml:"targetScopes"`
	TargetPrefixes   []string               `yaml:"targetPrefixes"`
	Network          []NetworkSelector      `yaml:"network"`
	Commands         []commandplan.Selector `yaml:"commands"`
	Decision         Decision               `yaml:"decision"`
	Reason           string                 `yaml:"reason"`
	toolRequired     bool
}

type EvaluationInput struct {
	Authorization      AuthorizationContext
	Operation          Operation
	HardDenyReason     string
	ApprovalSummary    string
	ApprovalReason     string
	approvalOperations []string
}

type Evaluation struct {
	Decision              Decision
	ReasonCode            string
	Reason                string
	Rule                  string
	Preset                Preset
	MatchedConfiguredRule bool
}

func (p *Policy) Normalize() {
	if p == nil {
		return
	}

	p.Preset = Preset(str.String(p.Preset).Normalized())
	if p.Preset == "" {
		p.Preset = PresetCustom
	}
	p.Default = Decision(str.String(p.Default).Normalized())
	if p.Default == "" {
		p.Default = DecisionDeny
	}
	if p.RequestRetention == 0 {
		p.RequestRetention = DefaultApprovalRequestRetention
	}
	if p.GrantRetention == 0 {
		p.GrantRetention = DefaultApprovalGrantRetention
	}
	if p.CleanupInterval == 0 {
		p.CleanupInterval = DefaultApprovalCleanupInterval
	}
	if p.CleanupBatchSize == 0 {
		p.CleanupBatchSize = DefaultApprovalCleanupBatchSize
	}
	if p.ApprovalRateLimit == 0 {
		p.ApprovalRateLimit = DefaultApprovalRateLimit
	}
	if p.ApprovalRateWindow == 0 {
		p.ApprovalRateWindow = DefaultApprovalRateWindow
	}

	if p.SurfaceKindDefaults == nil {
		p.SurfaceKindDefaults = getDefaultSurfaceKindDecisions()
	}
	normalizedKindDefaults := make(map[SurfaceKind]Decision, len(p.SurfaceKindDefaults))
	for kind, decision := range p.SurfaceKindDefaults {
		normalizedKind := SurfaceKind(str.String(kind).Normalized())
		normalizedDecision := Decision(str.String(decision).Normalized())
		normalizedKindDefaults[normalizedKind] = normalizedDecision
	}
	p.SurfaceKindDefaults = normalizedKindDefaults

	normalizedDefaults := make(map[Surface]Decision, len(p.SurfaceDefaults))
	for surface, decision := range p.SurfaceDefaults {
		normalizedSurface := Surface(str.String(surface).Normalized())
		normalizedDecision := Decision(str.String(decision).Normalized())
		normalizedDefaults[normalizedSurface] = normalizedDecision
	}
	p.SurfaceDefaults = normalizedDefaults

	normalizedRules := make([]Rule, len(p.Rules))
	copy(normalizedRules, p.Rules)
	for index := range normalizedRules {
		normalizedRules[index].normalize()
	}
	p.Rules = normalizedRules
}

func (p Policy) Validate() error {
	p.Normalize()
	if !isValidPreset(p.Preset) {
		return errors.New("permission preset must be one of: ask, approve, full_access, custom")
	}
	if !isValidDecision(p.Default) {
		return errors.New("permission default must be one of: allow, ask, deny")
	}
	if p.RequestRetention < 0 {
		return errors.New("permission request retention must be greater than or equal to zero")
	}
	if p.GrantRetention < 0 {
		return errors.New("permission grant retention must be greater than or equal to zero")
	}
	if p.CleanupInterval <= 0 {
		return errors.New("permission cleanup interval must be greater than zero")
	}
	if p.CleanupBatchSize <= 0 {
		return errors.New("permission cleanup batch size must be greater than zero")
	}
	if p.ApprovalRateLimit <= 0 {
		return errors.New("permission approval rate limit must be greater than zero")
	}
	if p.ApprovalRateWindow <= 0 {
		return errors.New("permission approval rate window must be greater than zero")
	}
	for kind, decision := range p.SurfaceKindDefaults {
		if !isValidSurfaceKind(kind, false) {
			return errors.New("permission surface kind default contains an invalid kind")
		}
		if !isValidDecision(decision) {
			return errors.New("permission surface kind default must be one of: allow, ask, deny")
		}
	}
	for surface, decision := range p.SurfaceDefaults {
		if !isValidSurface(surface, false) {
			return errors.New("permission surface default contains an invalid surface")
		}
		if !isValidDecision(decision) {
			return errors.New("permission surface default must be one of: allow, ask, deny")
		}
	}

	names := make(map[string]struct{}, len(p.Rules))
	for _, rule := range p.Rules {
		if err := rule.validate(); err != nil {
			return err
		}
		if _, exists := names[rule.Name]; exists {
			return errors.New("permission rule names must be unique")
		}

		names[rule.Name] = struct{}{}
	}

	return nil
}

func (p Policy) Evaluate(input EvaluationInput) Evaluation {
	p.Normalize()
	policyErr := p.Validate()
	p = p.Effective()
	preset := p.EffectivePreset()
	if !isValidDecision(p.Default) {
		p.Default = DecisionDeny
	}

	if policyErr != nil {
		return Evaluation{Decision: DecisionDeny, ReasonCode: ReasonPolicyDefault, Preset: preset}
	}
	authorization, authorizationErr := input.Authorization.Normalize()
	operation, operationErr := input.Operation.Normalize()
	if authorizationErr != nil {
		authorization = AuthorizationContext{
			Actor: Actor{Kind: ActorUnknown}, SurfaceKind: SurfaceKindUnknown, Surface: SurfaceUnknown,
		}
	}
	if operationErr != nil {
		operation = Operation{Resource: ResourceUnknown, Action: ActionUnknown}
	}
	if authorization.Scope.Restricted && !authorization.Scope.Allows(operation) {
		return Evaluation{
			Decision:   DecisionDeny,
			ReasonCode: ReasonScopeExceeded,
			Reason:     "operation exceeds the actor's delegated scope",
			Preset:     preset,
		}
	}
	if preset == PresetFullAccess && operation.Network != nil &&
		operation.Network.RequestClass == NetworkRequestBackground {
		if rule, ok := getMatchingRule(p.Rules, authorization, operation); ok {
			return Evaluation{
				Decision:              rule.Decision,
				ReasonCode:            ReasonRuleMatched,
				Reason:                rule.Reason,
				Rule:                  rule.Name,
				Preset:                preset,
				MatchedConfiguredRule: true,
			}
		}
	}
	if preset == PresetFullAccess {
		return Evaluation{Decision: DecisionAllow, ReasonCode: ReasonFullAccess, Preset: preset}
	}
	if reason := str.String(input.HardDenyReason).Trim(); reason != "" {
		return Evaluation{Decision: DecisionDeny, ReasonCode: ReasonHardDeny, Reason: reason, Preset: preset}
	}

	var evaluation Evaluation
	var matchedRule Rule
	if rule, ok := getMatchingRule(p.Rules, authorization, operation); ok {
		matchedRule = rule
		evaluation = Evaluation{
			Decision:              rule.Decision,
			ReasonCode:            ReasonRuleMatched,
			Reason:                rule.Reason,
			Rule:                  rule.Name,
			Preset:                preset,
			MatchedConfiguredRule: true,
		}
	} else if rule, ok := getMatchingRule(p.presetRules, authorization, operation); ok {
		matchedRule = rule
		evaluation = Evaluation{
			Decision:   rule.Decision,
			ReasonCode: ReasonRuleMatched,
			Reason:     rule.Reason,
			Rule:       rule.Name,
			Preset:     preset,
		}
	} else if decision, ok := p.SurfaceDefaults[authorization.Surface]; ok && isValidDecision(decision) {
		evaluation = Evaluation{Decision: decision, ReasonCode: ReasonSurfaceDefault, Preset: preset}
	} else if decision, ok := p.SurfaceKindDefaults[authorization.SurfaceKind]; ok && isValidDecision(decision) {
		evaluation = Evaluation{Decision: decision, ReasonCode: ReasonSurfaceKindDefault, Preset: preset}
	} else {
		evaluation = Evaluation{Decision: p.Default, ReasonCode: ReasonPolicyDefault, Preset: preset}
	}
	if operation.OwnerRequired && authorization.Actor.Kind != ActorLocalOwner &&
		(operation.OwnerID == "" || authorization.Actor.ID != operation.OwnerID) &&
		(evaluation.Decision != DecisionAllow || evaluation.ReasonCode != ReasonRuleMatched) {
		return Evaluation{
			Decision:   DecisionDeny,
			ReasonCode: ReasonOwnerRequired,
			Reason:     "operation requires its owner",
			Preset:     preset,
		}
	}
	if evaluation.Decision == DecisionAllow &&
		slices.Contains(operation.Effects, EffectIndirectExecution) &&
		(!evaluation.MatchedConfiguredRule || len(matchedRule.Commands) == 0) {
		return Evaluation{
			Decision:   DecisionAsk,
			ReasonCode: ReasonApprovalRequired,
			Reason:     "indirect command execution requires explicit permission",
			Preset:     preset,
		}
	}

	if evaluation.Decision != DecisionDeny {
		if reason := str.String(input.ApprovalReason).Trim(); reason != "" {
			return Evaluation{
				Decision:   DecisionAsk,
				ReasonCode: ReasonApprovalRequired,
				Reason:     reason,
				Preset:     preset,
			}
		}
	}

	return evaluation
}

func getMatchingRule(rules []Rule, authorization AuthorizationContext, operation Operation) (Rule, bool) {
	var selected Rule
	selectedPriority := -1
	selectedSpecificity := -1
	for _, rule := range rules {
		if !isEligibleBackgroundRule(rule, operation) {
			continue
		}
		if !rule.matches(authorization, operation) {
			continue
		}

		priority := getDecisionPriority(rule.Decision)
		specificity := rule.specificity()
		if priority > selectedPriority || priority == selectedPriority && specificity > selectedSpecificity ||
			priority == selectedPriority && specificity == selectedSpecificity && rule.Name < selected.Name {
			selected = rule
			selectedPriority = priority
			selectedSpecificity = specificity
		}
	}

	return selected, selectedPriority >= 0
}

func isEligibleBackgroundRule(rule Rule, operation Operation) bool {
	if operation.Network == nil || operation.Network.RequestClass != NetworkRequestBackground {
		return true
	}
	if rule.Decision != DecisionAllow {
		return true
	}
	if !slices.Contains(rule.Resources, ResourceNetwork) || !slices.Contains(rule.Actions, ActionConnect) {
		return false
	}
	for _, selector := range rule.Network {
		if selector.Scheme == "" && selector.Host != "" && selector.Port != 0 && selector.Method == "CONNECT" &&
			selector.RequestClass == NetworkRequestBackground && selector.Matches(*operation.Network) {
			return true
		}
	}
	return false
}

func getDefaultSurfaceKindDecisions() map[SurfaceKind]Decision {
	return map[SurfaceKind]Decision{
		SurfaceKindLocal:      DecisionAsk,
		SurfaceKindGateway:    DecisionDeny,
		SurfaceKindAutomation: DecisionDeny,
		SurfaceKindRPC:        DecisionDeny,
		SurfaceKindACP:        DecisionDeny,
	}
}

func (r *Rule) normalize() {
	r.Name = str.String(r.Name).Trim()
	r.Profiles = normalizeStrings(r.Profiles)
	r.ActorKinds = normalizeValues(r.ActorKinds, func(value ActorKind) ActorKind {
		return ActorKind(str.String(value).Normalized())
	})
	r.ActorIDs = normalizeStrings(r.ActorIDs)
	if len(r.ParentActorKinds) > 0 {
		r.ParentActorKinds = normalizeValues(r.ParentActorKinds, func(value ActorKind) ActorKind {
			return ActorKind(str.String(value).Normalized())
		})
	}
	r.SurfaceKinds = normalizeValues(r.SurfaceKinds, func(value SurfaceKind) SurfaceKind {
		return SurfaceKind(str.String(value).Normalized())
	})
	r.Surfaces = normalizeValues(r.Surfaces, func(value Surface) Surface {
		return Surface(str.String(value).Normalized())
	})
	r.Tools = normalizeStrings(r.Tools)
	r.Resources = normalizeValues(r.Resources, func(value Resource) Resource {
		return Resource(str.String(value).Normalized())
	})
	r.Actions = normalizeValues(r.Actions, func(value Action) Action {
		return Action(str.String(value).Normalized())
	})
	r.Effects = normalizeEffects(r.Effects)
	if len(r.TargetScopes) > 0 {
		r.TargetScopes = normalizeValues(r.TargetScopes, func(value TargetScope) TargetScope {
			return TargetScope(str.String(value).Normalized())
		})
	}
	r.TargetPrefixes = normalizeStrings(r.TargetPrefixes)
	r.Decision = Decision(str.String(r.Decision).Normalized())
	if len(r.Network) > 0 {
		if normalized, err := normalizeNetworkSelectors(r.Network); err == nil {
			r.Network = normalized
		}
	}
	if len(r.Commands) > 0 {
		normalize := commandplan.NormalizeSelectors
		if r.Decision == DecisionDeny {
			normalize = commandplan.NormalizeDenySelectors
		}
		if normalized, err := normalize(r.Commands); err == nil {
			r.Commands = normalized
		}
	}
	r.Reason = str.String(r.Reason).Trim()
}

func (r Rule) validate() error {
	if r.Name == "" {
		return errors.New("permission rule name is required")
	}
	if !isValidDecision(r.Decision) {
		return errors.New("permission rule decision must be one of: allow, ask, deny")
	}
	if len(r.Network) > 0 && len(r.TargetPrefixes) > 0 {
		return errors.New("permission rule cannot combine network selectors and target prefixes")
	}
	if len(r.Commands) > 0 && (len(r.Network) > 0 || len(r.TargetPrefixes) > 0) {
		return errors.New("permission rule cannot combine command selectors with other target selectors")
	}
	for _, value := range r.ActorKinds {
		if !isValidActorKind(value, false) {
			return errors.New("permission rule contains an invalid actor")
		}
	}
	for _, value := range r.ParentActorKinds {
		if !isValidActorKind(value, false) {
			return errors.New("permission rule contains an invalid parent actor")
		}
	}
	for _, value := range r.SurfaceKinds {
		if !isValidSurfaceKind(value, false) {
			return errors.New("permission rule contains an invalid surface kind")
		}
	}
	for _, value := range r.Resources {
		if !isValidResource(value, false) {
			return errors.New("permission rule contains an invalid resource")
		}
	}
	for _, value := range r.Actions {
		if !isValidAction(value, false) {
			return errors.New("permission rule contains an invalid action")
		}
	}
	for _, value := range r.Effects {
		if !isValidEffect(value) {
			return errors.New("permission rule contains an invalid effect")
		}
	}
	for _, value := range r.TargetScopes {
		if !isValidTargetScope(value, false) {
			return errors.New("permission rule contains an invalid target scope")
		}
	}
	if _, err := normalizeNetworkSelectors(r.Network); err != nil {
		return err
	}
	for index, selector := range r.Commands {
		normalizeCommands := commandplan.NormalizeSelectors
		if r.Decision == DecisionDeny {
			normalizeCommands = commandplan.NormalizeDenySelectors
		}
		if _, err := normalizeCommands([]commandplan.Selector{selector}); err != nil {
			return fmt.Errorf("permission rule %q commands[%d]: %w", r.Name, index, err)
		}
	}
	if len(r.Commands) > 0 {
		if len(r.Resources) > 0 && !slices.Contains(r.Resources, ResourceProcess) {
			return errors.New("permission command selectors require the process resource")
		}
		if len(r.Actions) > 0 && !slices.Contains(r.Actions, ActionExecute) &&
			!slices.Contains(r.Actions, ActionStart) {
			return errors.New("permission command selectors require execute or start")
		}
	}

	return nil
}

func (r Rule) matches(authorization AuthorizationContext, operation Operation) bool {
	return matchesValue(r.Profiles, authorization.Profile) &&
		matchesValue(r.ActorKinds, authorization.Actor.Kind) &&
		matchesValue(r.ActorIDs, authorization.Actor.ID) &&
		matchesValue(r.ParentActorKinds, authorization.ParentActorKind) &&
		matchesValue(r.SurfaceKinds, authorization.SurfaceKind) &&
		matchesValue(r.Surfaces, authorization.Surface) &&
		matchesValue(r.Tools, operation.Tool) &&
		matchesValue(r.Resources, operation.Resource) &&
		matchesValue(r.Actions, operation.Action) &&
		containsAllEffects(operation.Effects, r.Effects) &&
		matchesValue(r.TargetScopes, operation.TargetScope) &&
		(!r.toolRequired || operation.Tool != "") &&
		matchesOperationTarget(r, operation)
}

func (r Rule) specificity() int {
	toolRequired := 0
	if r.toolRequired {
		toolRequired = 1
	}

	return len(r.Profiles) + len(r.ActorKinds) + len(r.ActorIDs) + len(r.ParentActorKinds) + len(r.SurfaceKinds) + len(r.Surfaces) +
		len(r.Tools) + len(r.Resources) + len(r.Actions) + len(r.Effects) + len(r.TargetScopes) +
		len(r.TargetPrefixes) + getNetworkSelectorSpecificity(r.Network) + len(r.Commands) + toolRequired
}

func getDecisionPriority(decision Decision) int {
	switch decision {
	case DecisionDeny:
		return 3
	case DecisionAsk:
		return 2
	case DecisionAllow:
		return 1
	default:
		return 0
	}
}

func normalizeValues[T ~string](values []T, normalize func(T) T) []T {
	result := make([]T, 0, len(values))
	var zero T
	for _, value := range values {
		value = normalize(value)
		if value == zero || slices.Contains(result, value) {
			continue
		}
		result = append(result, value)
	}
	slices.SortFunc(result, func(left, right T) int {
		return strings.Compare(string(left), string(right))
	})
	return result
}

func normalizeStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = str.String(value).Trim()
		if value == "" || slices.Contains(result, value) {
			continue
		}
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}

func matchesValue[T comparable](allowed []T, value T) bool {
	return len(allowed) == 0 || slices.Contains(allowed, value)
}

func containsAllEffects(values []Effect, required []Effect) bool {
	for _, value := range required {
		if !slices.Contains(values, value) {
			return false
		}
	}

	return true
}

func matchesTargetPrefix(prefixes []string, target string) bool {
	if len(prefixes) == 0 {
		return true
	}

	for _, prefix := range prefixes {
		if strings.HasPrefix(target, prefix) {
			return true
		}
	}

	return false
}

func matchesOperationTarget(rule Rule, operation Operation) bool {
	if operation.Network != nil {
		if len(rule.Commands) > 0 && len(rule.Network) == 0 {
			return false
		}
		return len(rule.TargetPrefixes) == 0 && matchesNetworkSelectors(rule.Network, operation.Network)
	}
	if operation.Command != nil {
		if len(rule.Commands) == 0 && (len(rule.TargetPrefixes) > 0 || len(rule.Network) > 0) {
			return false
		}
		return commandplan.MatchSelectors(rule.Commands, *operation.Command)
	}
	if len(rule.Commands) > 0 && len(rule.TargetPrefixes) == 0 {
		return false
	}

	return len(rule.Network) == 0 && matchesTargetPrefix(rule.TargetPrefixes, operation.Target)
}
