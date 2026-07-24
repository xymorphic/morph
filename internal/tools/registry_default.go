package tools

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"sort"
	"sync"

	"github.com/wandxy/morph/internal/command"
	"github.com/wandxy/morph/internal/permissions"
	"github.com/wandxy/morph/internal/trace"
	"github.com/wandxy/morph/pkg/str"
)

type RegistryOptions struct {
	PermissionPolicy permissions.Policy
	ApprovalService  permissions.Approver
}

// DefaultRegistry is the standard runtime tool registry.
type DefaultRegistry struct {
	mu          sync.RWMutex
	definitions map[string]Definition
	groups      map[string]Group
	permissions permissions.Engine
	approvals   permissions.Approver
}

// NewDefaultRegistry creates the standard runtime tool registry.
func NewDefaultRegistry(options ...RegistryOptions) *DefaultRegistry {
	var opts RegistryOptions
	if len(options) > 0 {
		opts = options[0]
	} else {
		opts.PermissionPolicy = permissions.Policy{
			Default:             permissions.DecisionAllow,
			SurfaceKindDefaults: map[permissions.SurfaceKind]permissions.Decision{},
		}
	}
	opts.PermissionPolicy.Normalize()

	return &DefaultRegistry{
		definitions: make(map[string]Definition),
		groups:      make(map[string]Group),
		permissions: permissions.NewEngine(opts.PermissionPolicy),
		approvals:   opts.ApprovalService,
	}
}

func (r *DefaultRegistry) SetApprovalService(service permissions.Approver) {
	if r == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.approvals = service
}

func (r *DefaultRegistry) getApprovalService() permissions.Approver {
	if r == nil {
		return nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.approvals
}

// Register registers a new tool in the registry.
func (r *DefaultRegistry) Register(def Definition) error {
	if r == nil {
		return errors.New("tool registry is required")
	}

	nameValue := str.String(def.Name)
	def.Name = nameValue.Trim()
	if def.Name == "" {
		return errors.New("tool name is required")
	}

	if def.Handler == nil {
		return errors.New("tool handler is required")
	}
	if def.Permission.IsZero() && def.ResolvePermission == nil && def.PreparePermission == nil {
		return errors.New("tool permission declaration is required")
	}
	if err := validateSemanticIndexPolicy(def.SemanticIndex); err != nil {
		return err
	}
	def.Groups = normalizeNames(def.Groups)
	def.Platforms = normalizeNames(def.Platforms)
	if !def.Permission.IsZero() {
		if str.String(def.Permission.Tool).Trim() == "" {
			def.Permission.Tool = def.Name
		}
		permission, err := def.Permission.Normalize()
		if err != nil {
			return err
		}
		def.Permission = permission
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.definitions[def.Name]; exists {
		return errors.New("tool is already registered")
	}

	r.definitions[def.Name] = def
	return nil
}

func (r *DefaultRegistry) RegisterGroup(group Group) error {
	if r == nil {
		return errors.New("tool registry is required")
	}

	nameValue2 := str.String(group.Name)
	group.Name = nameValue2.Trim()
	if group.Name == "" {
		return errors.New("tool group name is required")
	}
	group.Tools = normalizeNames(group.Tools)
	group.Includes = normalizeNames(group.Includes)

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.groups[group.Name]; exists {
		return errors.New("tool group is already registered")
	}

	r.groups[group.Name] = group
	return nil
}

func (r *DefaultRegistry) Get(name string) (Definition, bool) {
	if r == nil {
		return Definition{}, false
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	nameValue3 := str.String(name)
	def, ok := r.definitions[nameValue3.Trim()]
	return def, ok
}

func (r *DefaultRegistry) GetGroup(name string) (Group, bool) {
	if r == nil {
		return Group{}, false
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	nameValue4 := str.String(name)
	group, ok := r.groups[nameValue4.Trim()]
	return group, ok
}

func (r *DefaultRegistry) List() Definitions {
	if r == nil {
		return nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	definitions := make(Definitions, 0, len(r.definitions))
	for _, def := range r.definitions {
		definitions = append(definitions, def)
	}

	sort.Slice(definitions, func(i, j int) bool {
		return definitions[i].Name < definitions[j].Name
	})

	return definitions
}

func (r *DefaultRegistry) ListGroups() []Group {
	if r == nil {
		return nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	groups := make([]Group, 0, len(r.groups))
	for _, group := range r.groups {
		groups = append(groups, group)
	}

	sort.Slice(groups, func(i, j int) bool {
		return groups[i].Name < groups[j].Name
	})

	return groups
}

func (r *DefaultRegistry) Resolve(opts Policy) (Definitions, error) {
	if r == nil {
		return nil, errors.New("tool registry is required")
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(opts.GroupNames) == 0 {
		return filterDefinitions(sortedDefinitions(r.definitions), opts), nil
	}

	selected := make(map[string]Definition)
	resolvedGroups := make(map[string]bool)
	for _, rawName := range normalizeNames(opts.GroupNames) {
		if err := r.resolveGroup(rawName, nil, resolvedGroups, selected); err != nil {
			return nil, err
		}
	}

	definitions := make(Definitions, 0, len(selected))
	for _, def := range selected {
		definitions = append(definitions, def)
	}
	sort.Slice(definitions, func(i, j int) bool {
		return definitions[i].Name < definitions[j].Name
	})

	return filterDefinitions(definitions, opts), nil
}

func (r *DefaultRegistry) Invoke(ctx context.Context, call Call) (Result, error) {
	def, ok := r.Get(call.Name)
	if !ok {
		return Result{Error: Error{Code: "tool_not_registered", Message: "tool is not registered"}.String()}, nil
	}
	ctx = permissions.WithDecisionObserver(ctx, r.recordPermissionDecision)
	var result Result
	var blocked bool
	ctx, result, blocked = r.checkPermissions(ctx, def, call)
	if blocked {
		return result, nil
	}
	ctx = permissions.WithPreset(ctx, r.permissions.Preset(ctx))
	if r.permissions.Preset(ctx) == permissions.PresetFullAccess {
		ctx = permissions.WithFullAccess(ctx)
	}

	result, err := def.Handler.Invoke(ctx, call)
	if err != nil {
		result.Error = Error{Code: "tool_invocation_failed", Message: err.Error()}.String()
		return result, nil
	}
	errorValue := str.String(result.Error)
	if errorValue.Trim() != "" {
		errorValue2 := str.String(result.Error)
		result.Error = normalizeResultError(errorValue2.Trim())
		return result, nil
	}
	if def.SemanticIndex.Mode == SemanticIndexProject {
		result.SemanticContent = str.String(def.SemanticIndex.Project(call, result)).Trim()
	}

	return result, nil
}

func validateSemanticIndexPolicy(policy SemanticIndexPolicy) error {
	switch policy.Mode {
	case SemanticIndexSkip:
		if policy.Project != nil {
			return errors.New("semantic index skip policy must not define a projector")
		}
	case SemanticIndexProject:
		if policy.Project == nil {
			return errors.New("semantic index projector is required")
		}
	default:
		if policy.Mode == SemanticIndexUnset {
			return errors.New("semantic index policy is required")
		}
		return errors.New("semantic index policy is invalid")
	}

	return nil
}

func (r *DefaultRegistry) checkPermissions(
	ctx context.Context,
	definition Definition,
	call Call,
) (context.Context, Result, bool) {
	inputs, prepared, err := getPermissionInputs(ctx, definition, call)
	if err != nil {
		code := "invalid_input"
		message := err.Error()
		if resolutionErr, ok := GetPermissionResolutionError(err); ok {
			if resolutionErr.Code != "" {
				code = resolutionErr.Code
			}
			message = resolutionErr.Message
		}
		return ctx, Result{Error: Error{Code: code, Message: message}.String()}, true
	}
	if len(inputs) == 0 {
		return ctx, Result{}, false
	}
	if prepared != nil {
		ctx = withPreparedCall(ctx, definition.Name, call.Input, prepared)
	}

	approvals := r.getApprovalService()
	var selected *permissions.DecisionError
	var firstAsk *permissions.DecisionError
	var preparedBatch permissions.BatchApproval
	authorized := make([]permissions.Operation, 0, len(inputs))
	askInputs := make([]permissions.EvaluationInput, 0, len(inputs))
	askedFingerprints := make(map[string]struct{}, len(inputs))
	authorization := getAuthorizationContext(ctx)
	propagateAuthorization := r.permissions.Preset(ctx) == permissions.PresetAskForApproval ||
		r.permissions.Preset(ctx) == permissions.PresetApproveForMe
	for _, input := range inputs {
		evaluation, checkErr := r.permissions.Check(ctx, input)
		permissions.ObserveDecision(ctx, input.Operation, evaluation)
		decisionErr, ok := permissions.GetDecisionError(checkErr)
		if !ok {
			if propagateAuthorization {
				authorized = append(authorized, input.Operation)
			}
			continue
		}
		if decisionErr.Code == permissions.ErrorCodeApprovalRequired {
			if firstAsk == nil {
				firstAsk = decisionErr
			}
			approvalReason := str.String(input.ApprovalReason).Trim()
			if approvalReason == "" {
				approvalReason = evaluation.Reason
			}
			input.ApprovalReason = approvalReason
			askInputs = append(askInputs, input)
			askedFingerprints[permissions.Fingerprint(authorization, input.Operation)] = struct{}{}
			continue
		}
		if selected == nil {
			selected = decisionErr
		}
	}
	if selected != nil {
		return ctx, Result{Error: Error{Code: selected.Code, Message: selected.Error()}.String()}, true
	}
	if len(askInputs) > 0 {
		if approvals == nil {
			selected = firstAsk
		} else {
			var approvalErr error
			batchApprovals, supportsAtomicApproval := approvals.(permissions.OperationBatchApprover)
			if supportsAtomicApproval {
				preparedBatch, approvalErr = batchApprovals.PrepareOperationBatch(ctx, askInputs, inputs)
			} else if len(inputs) == 1 {
				singleApproval, supportsPreparedApproval := approvals.(permissions.BatchApprover)
				if !supportsPreparedApproval {
					approvalErr = errors.New("approval service does not support prepared approvals")
				} else {
					preparedBatch, approvalErr = singleApproval.PrepareBatch(ctx, askInputs)
				}
			} else {
				approvalErr = errors.New("approval service does not support atomic operation batches")
			}
			if approvalErr != nil {
				if decisionErr, ok := permissions.GetDecisionError(approvalErr); ok {
					selected = decisionErr
				} else {
					selected = &permissions.DecisionError{
						Code: permissions.ErrorCodeDenied,
						Evaluation: permissions.Evaluation{
							Decision: permissions.DecisionDeny, Reason: approvalErr.Error(),
						},
					}
				}
			}
		}
	}
	if selected == nil && len(askInputs) > 0 {
		authorized = authorized[:0]
		for _, input := range inputs {
			recheck := r.permissions.Evaluate(ctx, input)
			permissions.ObserveDecision(ctx, input.Operation, recheck)
			if recheck.Decision == permissions.DecisionDeny {
				selected = &permissions.DecisionError{Code: permissions.ErrorCodeDenied, Evaluation: recheck}
				break
			}
			if recheck.Decision == permissions.DecisionAsk {
				if _, approved := askedFingerprints[permissions.Fingerprint(authorization, input.Operation)]; !approved {
					selected = &permissions.DecisionError{
						Code:       permissions.ErrorCodeApprovalRequired,
						Evaluation: recheck,
					}
					break
				}
			}
			authorized = append(authorized, input.Operation)
		}
	}
	if selected == nil && preparedBatch != nil {
		if err := preparedBatch.Commit(ctx); err != nil {
			selected = &permissions.DecisionError{
				Code: permissions.ErrorCodeDenied,
				Evaluation: permissions.Evaluation{
					Decision: permissions.DecisionDeny, Reason: err.Error(),
				},
			}
		}
	}
	if selected == nil {
		return permissions.WithAuthorizedOperations(ctx, authorized), Result{}, false
	}

	return ctx, Result{Error: Error{Code: selected.Code, Message: selected.Error()}.String()}, true
}

func getPermissionInputs(
	ctx context.Context,
	definition Definition,
	call Call,
) ([]permissions.EvaluationInput, any, error) {
	if definition.PreparePermission != nil {
		preparation, err := definition.PreparePermission(ctx, call)
		if err != nil {
			return nil, nil, err
		}
		if len(preparation.Inputs) == 0 {
			return nil, nil, errors.New("permission preparer returned no operations")
		}
		inputs, err := normalizePermissionInputs(definition.Name, preparation.Inputs)
		return inputs, preparation.Prepared, err
	}
	if definition.ResolvePermission == nil {
		if definition.Permission.IsZero() {
			return nil, nil, nil
		}

		inputs, err := normalizePermissionInputs(definition.Name, []permissions.EvaluationInput{{
			Operation: definition.Permission,
		}})
		return inputs, nil, err
	}

	inputs, err := definition.ResolvePermission(ctx, call)
	if err != nil {
		return nil, nil, err
	}
	if len(inputs) == 0 {
		return nil, nil, errors.New("permission resolver returned no operations")
	}

	inputs, err = normalizePermissionInputs(definition.Name, inputs)
	return inputs, nil, err
}

func normalizePermissionInputs(toolName string, inputs []permissions.EvaluationInput) ([]permissions.EvaluationInput, error) {
	normalized := make([]permissions.EvaluationInput, len(inputs))
	for index, input := range inputs {
		if str.String(input.Operation.Tool).Trim() == "" {
			input.Operation.Tool = toolName
		}
		operation, err := input.Operation.Normalize()
		if err != nil {
			return nil, err
		}
		input.Operation = operation
		normalized[index] = input
	}

	return normalized, nil
}

func (r *DefaultRegistry) recordPermissionDecision(
	ctx context.Context,
	operation permissions.Operation,
	evaluation permissions.Evaluation,
) {
	recorder := TraceRecorderFromContext(ctx)
	if recorder == nil {
		return
	}
	authorization := getAuthorizationContext(ctx)

	effects := make([]string, len(operation.Effects))
	for index, effect := range operation.Effects {
		effects[index] = string(effect)
	}
	recorder.Record(trace.EvtPermissionDecisionObserved, trace.PermissionDecisionPayload{
		ActorKind:     string(authorization.Actor.Kind),
		SurfaceKind:   string(authorization.SurfaceKind),
		Surface:       string(authorization.Surface),
		Tool:          operation.Tool,
		Resource:      string(operation.Resource),
		Action:        string(operation.Action),
		Effects:       effects,
		Decision:      string(evaluation.Decision),
		ReasonCode:    evaluation.ReasonCode,
		Rule:          evaluation.Rule,
		Preset:        string(evaluation.Preset),
		OwnerRequired: operation.OwnerRequired,
		Network: getPermissionNetworkTracePayload(
			operation.Network,
			slices.Contains(operation.Effects, permissions.EffectCredentialBearing),
		),
		Command: getPermissionCommandTracePayload(operation.Command),
	})
}

func getPermissionCommandTracePayload(
	target *command.Target,
) *trace.PermissionCommandTargetPayload {
	if target == nil {
		return nil
	}
	reasons := make([]string, len(target.DynamicReasons))
	for index, reason := range target.DynamicReasons {
		reasons[index] = string(reason)
	}
	return &trace.PermissionCommandTargetPayload{
		Mode:            string(target.Mode),
		Executable:      filepath.Base(target.Executable),
		InvocationCount: target.InvocationCount,
		RedirectCount:   target.RedirectCount,
		Complete:        target.Complete,
		Indirect:        target.Indirect,
		DynamicReasons:  reasons,
	}
}

func getPermissionNetworkTracePayload(
	target *permissions.NetworkTarget,
	credentialBearing bool,
) *trace.PermissionNetworkTargetPayload {
	if target == nil {
		return nil
	}

	payload := &trace.PermissionNetworkTargetPayload{
		Scheme:       target.Scheme,
		Host:         target.Host,
		Port:         target.Port,
		Path:         target.Path,
		Method:       target.Method,
		RequestClass: string(target.RequestClass),
		HasQuery:     target.QueryHash != "",
	}
	if credentialBearing {
		payload.Path = ""
	}
	return payload
}

func getAuthorizationContext(ctx context.Context) permissions.AuthorizationContext {
	if authorization, ok := permissions.FromContext(ctx); ok {
		return authorization
	}

	return permissions.AuthorizationContext{
		Actor:       permissions.Actor{Kind: permissions.ActorUnknown},
		SurfaceKind: permissions.SurfaceKindUnknown,
		Surface:     permissions.SurfaceUnknown,
	}
}

func (r *DefaultRegistry) resolveGroup(
	name string,
	stack []string,
	resolved map[string]bool,
	selected map[string]Definition,
) error {
	if resolved[name] {
		return nil
	}

	group, ok := r.groups[name]
	if !ok {
		return errors.New("tool group ('" + name + "') is not registered")
	}

	if slices.Contains(stack, name) {
		return errors.New("tool group ('" + name + "') cycle detected")
	}
	stack = append(stack, name)

	for _, included := range group.Includes {
		if err := r.resolveGroup(included, stack, resolved, selected); err != nil {
			return err
		}
	}

	for _, toolName := range group.Tools {
		def, ok := r.definitions[toolName]
		if !ok {
			return errors.New("tool ('" + toolName + "') referenced by group is not registered")
		}
		selected[toolName] = def
	}

	for _, def := range r.definitions {
		if slices.Contains(def.Groups, name) {
			selected[def.Name] = def
		}
	}

	resolved[name] = true
	return nil
}

func sortedDefinitions(definitions map[string]Definition) Definitions {
	list := make(Definitions, 0, len(definitions))
	for _, def := range definitions {
		list = append(list, def)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].Name < list[j].Name
	})
	return list
}

func filterDefinitions(definitions Definitions, opts Policy) Definitions {
	filtered := make(Definitions, 0, len(definitions))
	platformValue := str.String(opts.Platform)
	platform := platformValue.Trim()
	for _, def := range definitions {
		if !opts.Capabilities.Supports(def.Requires) {
			continue
		}
		if platform != "" && !matchesPlatform(def.Platforms, platform) {
			continue
		}

		filtered = append(filtered, def)
	}

	return filtered
}

func matchesPlatform(platforms []string, platform string) bool {
	if len(platforms) == 0 {
		return true
	}

	return slices.Contains(platforms, platform)
}

func normalizeNames(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		valueText := str.String(value).Trim()
		if valueText == "" {
			continue
		}
		if _, ok := seen[valueText]; ok {
			continue
		}
		seen[valueText] = struct{}{}
		normalized = append(normalized, valueText)
	}
	sort.Strings(normalized)
	return normalized
}

func normalizeResultError(raw string) string {
	var toolErr Error
	if err := json.Unmarshal([]byte(raw), &toolErr); err == nil {
		code := str.String(toolErr.Code)
		message := str.String(toolErr.Message)
		if code.Trim() != "" && message.Trim() != "" {
			return raw
		}
	}

	return Error{Code: "tool_failed", Message: raw}.String()
}
