package permissions

import (
	"errors"
	"slices"
	"strings"

	commandplan "github.com/wandxy/morph/internal/command"
	"github.com/wandxy/morph/pkg/str"
)

type PermissionScope struct {
	Restricted     bool
	Resources      []Resource
	Actions        []Action
	Effects        []Effect
	TargetPrefixes []string
	Network        []NetworkSelector
	Commands       []commandplan.Selector
}

func (s PermissionScope) Normalize() (PermissionScope, error) {
	if len(s.Resources) > 0 {
		s.Resources = normalizeValues(s.Resources, func(value Resource) Resource {
			return Resource(str.String(value).Normalized())
		})
	}
	if len(s.Actions) > 0 {
		s.Actions = normalizeValues(s.Actions, func(value Action) Action {
			return Action(str.String(value).Normalized())
		})
	}
	if len(s.Effects) > 0 {
		s.Effects = normalizeEffects(s.Effects)
	}
	if len(s.TargetPrefixes) > 0 {
		s.TargetPrefixes = normalizeStrings(s.TargetPrefixes)
	}
	if len(s.Network) > 0 {
		var err error
		s.Network, err = normalizeNetworkSelectors(s.Network)
		if err != nil {
			return PermissionScope{}, err
		}
	}
	if len(s.Commands) > 0 {
		var err error
		s.Commands, err = commandplan.NormalizeSelectors(s.Commands)
		if err != nil {
			return PermissionScope{}, err
		}
	}
	if len(s.Network) > 0 && len(s.TargetPrefixes) > 0 {
		return PermissionScope{}, errors.New("permission scope cannot combine network selectors and target prefixes")
	}
	for _, resource := range s.Resources {
		if !isValidResource(resource, false) {
			return PermissionScope{}, errors.New("permission scope contains an invalid resource")
		}
	}
	for _, action := range s.Actions {
		if !isValidAction(action, false) {
			return PermissionScope{}, errors.New("permission scope contains an invalid action")
		}
	}
	for _, effect := range s.Effects {
		if !isValidEffect(effect) {
			return PermissionScope{}, errors.New("permission scope contains an invalid effect")
		}
	}
	if !s.Restricted && (len(s.Resources) > 0 || len(s.Actions) > 0 || len(s.Effects) > 0 ||
		len(s.TargetPrefixes) > 0 || len(s.Network) > 0 || len(s.Commands) > 0) {
		return PermissionScope{}, errors.New("permission scope constraints require restricted mode")
	}

	return s, nil
}

func (s PermissionScope) Allows(operation Operation) bool {
	s, err := s.Normalize()
	if err != nil {
		return false
	}
	if !s.Restricted {
		return true
	}
	operation, err = operation.Normalize()
	if err != nil {
		return false
	}
	if len(s.Resources) == 0 || !slices.Contains(s.Resources, operation.Resource) {
		return false
	}
	if len(s.Actions) == 0 || !slices.Contains(s.Actions, operation.Action) {
		return false
	}
	for _, effect := range operation.Effects {
		if !slices.Contains(s.Effects, effect) {
			return false
		}
	}

	if operation.Network != nil {
		return len(s.TargetPrefixes) == 0 && len(s.Network) > 0 &&
			matchesNetworkSelectors(s.Network, operation.Network)
	}
	if operation.Command != nil {
		return len(s.Commands) > 0 && commandplan.MatchSelectors(s.Commands, *operation.Command)
	}

	return len(s.Network) == 0 && matchesTargetPrefix(s.TargetPrefixes, operation.Target)
}

func IntersectScopes(parent PermissionScope, delegated PermissionScope) (PermissionScope, error) {
	parent, err := parent.Normalize()
	if err != nil {
		return PermissionScope{}, err
	}
	delegated, err = delegated.Normalize()
	if err != nil {
		return PermissionScope{}, err
	}
	if !delegated.Restricted {
		return PermissionScope{}, errors.New("delegated permission scope must be restricted")
	}
	if !parent.Restricted {
		return delegated, nil
	}

	return PermissionScope{
		Restricted:     true,
		Resources:      intersectValues(parent.Resources, delegated.Resources),
		Actions:        intersectValues(parent.Actions, delegated.Actions),
		Effects:        intersectValues(parent.Effects, delegated.Effects),
		TargetPrefixes: intersectTargetPrefixes(parent.TargetPrefixes, delegated.TargetPrefixes),
		Network:        intersectNetworkSelectors(parent.Network, delegated.Network),
		Commands:       intersectCommandSelectors(parent.Commands, delegated.Commands),
	}, nil
}

func intersectCommandSelectors(left []commandplan.Selector, right []commandplan.Selector) []commandplan.Selector {
	if len(left) == 0 || len(right) == 0 {
		return nil
	}
	return commandplan.IntersectSelectors(left, right)
}

func intersectNetworkSelectors(left []NetworkSelector, right []NetworkSelector) []NetworkSelector {
	if len(left) == 0 || len(right) == 0 {
		return nil
	}

	result := make([]NetworkSelector, 0)
	for _, leftSelector := range left {
		for _, rightSelector := range right {
			selector, ok := intersectNetworkSelector(leftSelector, rightSelector)
			if ok && !slices.Contains(result, selector) {
				result = append(result, selector)
			}
		}
	}
	slices.SortFunc(result, func(leftValue, rightValue NetworkSelector) int {
		return strings.Compare(getNetworkSelectorFingerprint(leftValue), getNetworkSelectorFingerprint(rightValue))
	})

	return result
}

func intersectNetworkSelector(left NetworkSelector, right NetworkSelector) (NetworkSelector, bool) {
	var result NetworkSelector
	var ok bool
	result.Scheme, ok = intersectOptionalValue(left.Scheme, right.Scheme)
	if !ok {
		return NetworkSelector{}, false
	}
	result.Host, ok = intersectOptionalValue(left.Host, right.Host)
	if !ok {
		return NetworkSelector{}, false
	}
	result.Port, ok = intersectOptionalValue(left.Port, right.Port)
	if !ok {
		return NetworkSelector{}, false
	}
	result.Method, ok = intersectOptionalValue(left.Method, right.Method)
	if !ok {
		return NetworkSelector{}, false
	}
	result.RequestClass, ok = intersectOptionalValue(left.RequestClass, right.RequestClass)
	if !ok {
		return NetworkSelector{}, false
	}
	result.PathPrefix, ok = intersectNetworkPathPrefix(left.PathPrefix, right.PathPrefix)
	if !ok {
		return NetworkSelector{}, false
	}

	normalized, err := result.Normalize()
	return normalized, err == nil
}

func intersectOptionalValue[T comparable](left T, right T) (T, bool) {
	var zero T
	switch {
	case left == zero:
		return right, true
	case right == zero:
		return left, true
	case left == right:
		return left, true
	default:
		return zero, false
	}
}

func intersectNetworkPathPrefix(left string, right string) (string, bool) {
	switch {
	case left == "":
		return right, true
	case right == "":
		return left, true
	case matchesNetworkPathPrefix(left, right):
		return right, true
	case matchesNetworkPathPrefix(right, left):
		return left, true
	default:
		return "", false
	}
}

func DelegateAuthorization(
	parent AuthorizationContext,
	childID string,
	runID string,
	delegated PermissionScope,
) (AuthorizationContext, error) {
	parent, err := parent.Normalize()
	if err != nil {
		return AuthorizationContext{}, err
	}
	childID = str.String(childID).Trim()
	runID = str.String(runID).Trim()
	if childID == "" || runID == "" {
		return AuthorizationContext{}, errors.New("delegated actor and run ids are required")
	}
	scope, err := IntersectScopes(parent.Scope, delegated)
	if err != nil {
		return AuthorizationContext{}, err
	}

	return AuthorizationContext{
		Actor:           Actor{Kind: ActorSubagent, ID: childID},
		SurfaceKind:     parent.SurfaceKind,
		Surface:         parent.Surface,
		Profile:         parent.Profile,
		SessionID:       parent.SessionID,
		RunID:           runID,
		ParentActorKind: parent.Actor.Kind,
		ParentActorID:   parent.Actor.ID,
		ParentRunID:     parent.RunID,
		Scope:           scope,
	}.Normalize()
}

func intersectValues[T comparable](left []T, right []T) []T {
	result := make([]T, 0)
	for _, value := range left {
		if slices.Contains(right, value) {
			result = append(result, value)
		}
	}

	return result
}

func intersectTargetPrefixes(left []string, right []string) []string {
	if len(left) == 0 {
		return append([]string(nil), right...)
	}
	if len(right) == 0 {
		return append([]string(nil), left...)
	}

	result := make([]string, 0)
	for _, leftPrefix := range left {
		for _, rightPrefix := range right {
			prefix := ""
			switch {
			case strings.HasPrefix(leftPrefix, rightPrefix):
				prefix = leftPrefix
			case strings.HasPrefix(rightPrefix, leftPrefix):
				prefix = rightPrefix
			}
			if prefix != "" && !slices.Contains(result, prefix) {
				result = append(result, prefix)
			}
		}
	}
	slices.Sort(result)

	return result
}
