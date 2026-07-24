package permissions

import (
	"context"
	"slices"

	"github.com/wandxy/morph/internal/command"
)

type authorizationContextKey struct{}
type fullAccessContextKey struct{}
type presetContextKey struct{}
type authorizedOperationsContextKey struct{}
type decisionObserverContextKey struct{}

type DecisionObserver func(context.Context, Operation, Evaluation)

func WithContext(ctx context.Context, authorization AuthorizationContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}

	normalized, err := authorization.Normalize()
	if err != nil {
		return ctx
	}

	return context.WithValue(ctx, authorizationContextKey{}, normalized)
}

func FromContext(ctx context.Context) (AuthorizationContext, bool) {
	if ctx == nil {
		return AuthorizationContext{}, false
	}

	authorization, ok := ctx.Value(authorizationContextKey{}).(AuthorizationContext)
	if !ok {
		return AuthorizationContext{}, false
	}

	normalized, err := authorization.Normalize()
	if err != nil {
		return AuthorizationContext{}, false
	}

	return normalized, true
}

func WithFullAccess(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}

	return context.WithValue(ctx, fullAccessContextKey{}, true)
}

func HasFullAccess(ctx context.Context) bool {
	if ctx == nil {
		return false
	}

	enabled, _ := ctx.Value(fullAccessContextKey{}).(bool)
	return enabled
}

func WithPreset(ctx context.Context, preset Preset) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if !isValidPreset(preset) {
		return ctx
	}

	return context.WithValue(ctx, presetContextKey{}, preset)
}

func PresetFromContext(ctx context.Context) (Preset, bool) {
	if ctx == nil {
		return "", false
	}

	preset, ok := ctx.Value(presetContextKey{}).(Preset)
	return preset, ok && isValidPreset(preset)
}

func WithDecisionObserver(ctx context.Context, observer DecisionObserver) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if observer == nil {
		return ctx
	}

	return context.WithValue(ctx, decisionObserverContextKey{}, observer)
}

func ObserveDecision(ctx context.Context, operation Operation, evaluation Evaluation) {
	if ctx == nil {
		return
	}

	observer, _ := ctx.Value(decisionObserverContextKey{}).(DecisionObserver)
	if observer != nil {
		observer(ctx, operation, evaluation)
	}
}

func WithAuthorizedOperations(ctx context.Context, operations []Operation) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}

	normalized := make([]Operation, 0, len(operations))
	for _, operation := range operations {
		value, err := operation.Normalize()
		if err == nil {
			normalized = append(normalized, value)
		}
	}
	if len(normalized) == 0 {
		return ctx
	}

	return context.WithValue(ctx, authorizedOperationsContextKey{}, normalized)
}

func IsOperationAuthorized(ctx context.Context, operation Operation) bool {
	if ctx == nil {
		return false
	}

	operation, err := operation.Normalize()
	if err != nil {
		return false
	}

	authorized, _ := ctx.Value(authorizedOperationsContextKey{}).([]Operation)
	for _, candidate := range authorized {
		if candidate.Resource == operation.Resource &&
			candidate.Action == operation.Action &&
			candidate.Target == operation.Target &&
			candidate.TargetScope == operation.TargetScope &&
			isSameNetworkTarget(candidate.Network, operation.Network) &&
			isSameCommandTarget(candidate.Command, operation.Command) {
			return true
		}
	}

	return false
}

func IsExactOperationAuthorized(ctx context.Context, operation Operation) bool {
	if ctx == nil {
		return false
	}

	operation, err := operation.Normalize()
	if err != nil {
		return false
	}

	authorized, _ := ctx.Value(authorizedOperationsContextKey{}).([]Operation)
	for _, candidate := range authorized {
		if candidate.Tool == operation.Tool &&
			candidate.Resource == operation.Resource &&
			candidate.Action == operation.Action &&
			slices.Equal(candidate.Effects, operation.Effects) &&
			candidate.Target == operation.Target &&
			candidate.TargetScope == operation.TargetScope &&
			isSameNetworkTarget(candidate.Network, operation.Network) &&
			isSameCommandTarget(candidate.Command, operation.Command) &&
			candidate.OwnerID == operation.OwnerID &&
			candidate.OwnerRequired == operation.OwnerRequired {
			return true
		}
	}

	return false
}

func isSameCommandTarget(left *command.Target, right *command.Target) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}
