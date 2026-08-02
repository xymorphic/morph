package permissions

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/xymorphic/morph/internal/command"
)

func Fingerprint(authorization AuthorizationContext, operation Operation) string {
	values := []string{
		string(authorization.Actor.Kind),
		authorization.Actor.ID,
		string(authorization.ParentActorKind),
		authorization.ParentActorID,
		authorization.ParentRunID,
		authorization.Profile,
		string(authorization.SurfaceKind),
		string(authorization.Surface),
		operation.Tool,
		string(operation.Resource),
		string(operation.Action),
		strings.Join(effectsToStrings(operation.Effects), ","),
		operation.Target,
		string(operation.TargetScope),
		getNetworkTargetFingerprint(operation.Network),
		getCommandTargetFingerprint(operation.Command),
		operation.OwnerID,
		scopeFingerprint(authorization.Scope),
	}
	sum := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return hex.EncodeToString(sum[:])
}

func scopeFingerprint(scope PermissionScope) string {
	restricted := "unrestricted"
	if scope.Restricted {
		restricted = "restricted"
	}
	return strings.Join([]string{
		restricted,
		strings.Join(valuesToStrings(scope.Resources), ","),
		strings.Join(valuesToStrings(scope.Actions), ","),
		strings.Join(valuesToStrings(scope.Effects), ","),
		strings.Join(scope.TargetPrefixes, ","),
		getNetworkSelectorsFingerprint(scope.Network),
		getCommandSelectorsFingerprint(scope.Commands),
	}, "|")
}

func getCommandTargetFingerprint(target *command.Target) string {
	if target == nil {
		return ""
	}
	return target.Fingerprint()
}

func getCommandSelectorsFingerprint(selectors []command.Selector) string {
	values := make([]string, len(selectors))
	for index, selector := range selectors {
		values[index] = selector.Fingerprint()
	}
	return strings.Join(values, ",")
}

func getNetworkSelectorsFingerprint(selectors []NetworkSelector) string {
	values := make([]string, len(selectors))
	for index, selector := range selectors {
		values[index] = getNetworkSelectorFingerprint(selector)
	}

	return strings.Join(values, ",")
}

func valuesToStrings[T ~string](values []T) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}

	return result
}

func effectsToStrings(effects []Effect) []string {
	values := make([]string, len(effects))
	for index, effect := range effects {
		values[index] = string(effect)
	}

	return values
}
