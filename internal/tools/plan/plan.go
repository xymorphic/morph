package plan

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/xymorphic/morph/pkg/logutils"
	"github.com/xymorphic/morph/pkg/str"

	envtypes "github.com/xymorphic/morph/internal/environment/types"
	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/internal/tools"
	"github.com/xymorphic/morph/internal/tools/common"
	"github.com/xymorphic/morph/internal/trace"
)

var log = logutils.Module("tool.plan")

type input struct {
	Steps          []map[string]any `json:"steps"`
	Merge          bool             `json:"merge"`
	Explanation    string           `json:"explanation"`
	ClearCompleted bool             `json:"clear_completed"`
}

// Definition returns the model-visible tool definition.
func Definition(runtime envtypes.Runtime) tools.Definition {
	return tools.Definition{
		Name: "plan_tool",
		Description: "Read or update the current session plan for multi-step work. Omit `steps` to read the current" +
			" plan without changing it. Provide `steps` to replace or merge plan items.",
		Groups:        []string{"core"},
		SemanticIndex: tools.SkipSemanticIndex(),
		Permission: permissions.Operation{
			Resource: permissions.ResourcePlan,
			Action:   permissions.ActionManage,
			Effects:  []permissions.Effect{permissions.EffectRead, permissions.EffectWrite},
		},
		ResolvePermission: resolvePermission,
		InputSchema: common.ObjectSchema(map[string]any{
			"steps": map[string]any{
				"type": "array",
				"description": "If omitted, the tool returns the current plan without changing it. " +
					"If provided, the tool replaces or merges plan steps in the current session plan.",
				"items": common.ObjectSchema(map[string]any{
					"id":      common.StringSchema("Stable step identifier."),
					"content": common.StringSchema("Human-readable step description."),
					"status": map[string]any{
						"type":        "string",
						"description": "Plan step status.",
						"enum": []string{
							envtypes.PlanStatusPending,
							envtypes.PlanStatusInProgress,
							envtypes.PlanStatusCompleted,
							envtypes.PlanStatusCancelled,
						},
					},
				}),
			},
			"merge":           common.BooleanSchema("When true, merge step updates by id instead of replacing the full plan."),
			"explanation":     common.StringSchema("Optional explanation for why the plan changed."),
			"clear_completed": common.BooleanSchema("When true, remove completed and cancelled steps after applying the update."),
		}),
		Handler: tools.HandlerFunc(func(ctx context.Context, call tools.Call) (tools.Result, error) {
			var req input

			if result := common.DecodeInput(call, &req); result.Error != "" {
				return result, nil
			}

			sessionID := tools.SessionIDFromContext(ctx)
			sessionIDValue := str.String(sessionID)
			if sessionIDValue.Trim() == "" {
				sessionID = "default"
			}

			log.Info().
				Str("tool", "plan_tool").
				Str("phase", "start").
				Str("session_id", sessionID).
				Bool("has_steps", req.Steps != nil).
				Int("steps_count", len(req.Steps)).
				Bool("merge", req.Merge).
				Bool("clear_completed", req.ClearCompleted).
				Msg("plan tool started")

			if req.Steps == nil {
				log.Debug().
					Str("tool", "plan_tool").
					Str("phase", "execute").
					Str("action", "read").
					Msg("plan read started")
				plan := runtime.GetPlan(sessionID)
				log.Info().
					Str("tool", "plan_tool").
					Str("phase", "complete").
					Int("plan_steps", len(plan.Steps)).
					Int("pending", summarizePlan(plan).Pending).
					Int("in_progress", summarizePlan(plan).InProgress).
					Int("completed", summarizePlan(plan).Completed).
					Int("cancelled", summarizePlan(plan).Cancelled).
					Msg("plan tool read completed")
				return encodePlanOutput(plan, nil)
			}

			var (
				before = runtime.GetPlan(sessionID)
				plan   envtypes.Plan
				err    error
			)

			if req.Merge {
				log.Debug().
					Str("tool", "plan_tool").
					Str("phase", "execute").
					Str("action", "merge").
					Msg("plan merge started")
				updates, validationErr := decodePartialPlanSteps(req.Steps)
				if validationErr != nil {
					log.Warn().
						Err(validationErr).
						Str("tool", "plan_tool").
						Str("phase", "error").
						Msg("plan update failed")
					return common.ToolError("invalid_input", validationErr.Error()), nil
				}
				plan, err = runtime.MergePlan(sessionID, updates, req.Explanation, req.ClearCompleted)
			} else {
				log.Debug().
					Str("tool", "plan_tool").
					Str("phase", "execute").
					Str("action", "replace").
					Msg("plan replace started")
				steps, validationErr := decodePlanSteps(req.Steps)
				if validationErr != nil {
					log.Warn().
						Err(validationErr).
						Str("tool", "plan_tool").
						Str("phase", "error").
						Msg("plan update failed")
					return common.ToolError("invalid_input", validationErr.Error()), nil
				}
				explanationValue := str.String(req.Explanation)
				plan = envtypes.Plan{
					Steps:       steps,
					Explanation: explanationValue.Trim(),
				}

				if req.ClearCompleted {
					filtered := make([]envtypes.PlanStep, 0, len(plan.Steps))
					for _, step := range plan.Steps {
						if step.Status == envtypes.PlanStatusCompleted || step.Status == envtypes.PlanStatusCancelled {
							continue
						}

						filtered = append(filtered, step)
					}
					plan.Steps = filtered
				}

				if err = envtypes.ValidatePlan(plan); err == nil {
					plan, err = runtime.ReplacePlan(sessionID, plan)
				}
			}

			if err != nil {
				log.Warn().
					Err(err).
					Str("tool", "plan_tool").
					Str("phase", "error").
					Msg("plan update failed")
				return common.ToolError("invalid_input", err.Error()), nil
			}

			changes := getPlanChanges(before, plan)
			recordPlanEvent(ctx, sessionID, plan, changes)

			if req.ClearCompleted && len(plan.Steps) == 0 {
				recordPlanCleared(ctx, sessionID, plan)
			}

			summary := summarizePlan(plan)
			log.Info().
				Str("tool", "plan_tool").
				Str("phase", "complete").
				Int("plan_steps", len(plan.Steps)).
				Int("pending", summary.Pending).
				Int("in_progress", summary.InProgress).
				Int("completed", summary.Completed).
				Int("cancelled", summary.Cancelled).
				Msg("plan tool update completed")

			return encodePlanOutput(plan, changes)
		}),
	}
}

func resolvePermission(ctx context.Context, call tools.Call) ([]permissions.EvaluationInput, error) {
	raw := strings.TrimSpace(call.Input)
	if raw == "" {
		raw = "{}"
	}

	var req input
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		return nil, tools.NewPermissionResolutionError("invalid_input", "invalid tool input")
	}

	action := permissions.ActionRead
	effects := []permissions.Effect{permissions.EffectRead}
	if req.Steps != nil {
		action = permissions.ActionUpdate
		effects = append(effects, permissions.EffectWrite)
	}

	target := str.String(tools.SessionIDFromContext(ctx)).Trim()
	if target == "" {
		target = "default"
	}

	return []permissions.EvaluationInput{{Operation: permissions.Operation{
		Resource: permissions.ResourcePlan,
		Action:   action,
		Effects:  effects,
		Target:   target,
	}}}, nil
}

func decodePlanSteps(rawSteps []map[string]any) ([]envtypes.PlanStep, error) {
	seen := make(map[string]struct{}, len(rawSteps))
	steps := make([]envtypes.PlanStep, 0, len(rawSteps))

	for _, item := range rawSteps {
		id, _ := item["id"].(string)
		content, _ := item["content"].(string)
		status, _ := item["status"].(string)
		idValue := str.String(id)
		id = idValue.Trim()
		contentValue := str.String(content)
		content = contentValue.Trim()
		statusValue := str.String(status)
		status = statusValue.Trim()

		if id == "" {
			return nil, errInvalidPlan("step id is required")
		}
		if _, ok := seen[id]; ok {
			return nil, errInvalidPlan("step ids must be unique")
		}
		seen[id] = struct{}{}

		if content == "" {
			return nil, errInvalidPlan("step content is required")
		}
		if !envtypes.ValidPlanStatus(status) {
			return nil, errInvalidPlan("step status is invalid")
		}

		steps = append(steps, envtypes.PlanStep{
			ID:      id,
			Content: content,
			Status:  status,
		})
	}

	return steps, envtypes.ValidatePlan(envtypes.Plan{Steps: steps})
}

func decodePartialPlanSteps(rawSteps []map[string]any) ([]envtypes.PartialPlanStep, error) {
	seen := make(map[string]struct{}, len(rawSteps))
	steps := make([]envtypes.PartialPlanStep, 0, len(rawSteps))

	for _, item := range rawSteps {
		id, _ := item["id"].(string)
		idValue2 := str.String(id)
		id = idValue2.Trim()
		if id == "" {
			return nil, errInvalidPlan("step id is required")
		}
		if _, ok := seen[id]; ok {
			return nil, errInvalidPlan("step ids must be unique")
		}
		seen[id] = struct{}{}

		update := envtypes.PartialPlanStep{ID: id}

		if contentValue, ok := item["content"]; ok {
			content, contentOK := contentValue.(string)
			contentValue2 := str.String(content)
			if !contentOK || contentValue2.Trim() == "" {
				return nil, errInvalidPlan("step content is required")
			}
			contentValue3 := str.String(content)
			trimmed := contentValue3.Trim()
			update.Content = &trimmed
		}

		if statusValue, ok := item["status"]; ok {
			status, statusOK := statusValue.(string)
			statusValue2 := str.String(status)
			status = statusValue2.Trim()
			if !statusOK || !envtypes.ValidPlanStatus(status) {
				return nil, errInvalidPlan("step status is invalid")
			}
			update.Status = &status
		}

		steps = append(steps, update)
	}

	return steps, nil
}

func encodePlanOutput(plan envtypes.Plan, changes []trace.PlanToolChange) (tools.Result, error) {
	summary := summarizePlan(plan)
	activeStepID := ""

	for _, step := range plan.Steps {
		if step.Status == envtypes.PlanStatusInProgress {
			activeStepID = step.ID
			break
		}
	}
	explanationValue2 := str.String(plan.Explanation)
	output := map[string]any{
		"steps":          plan.Steps,
		"summary":        summary,
		"active_step_id": activeStepID,
		"explanation":    explanationValue2.Trim(),
	}
	if len(changes) > 0 {
		output["changes"] = changes
	}

	return common.EncodeOutput(output)
}

func summarizePlan(plan envtypes.Plan) envtypes.PlanSummary {
	summary := envtypes.PlanSummary{Total: len(plan.Steps)}

	for _, step := range plan.Steps {
		switch step.Status {
		case envtypes.PlanStatusPending:
			summary.Pending++
		case envtypes.PlanStatusInProgress:
			summary.InProgress++
		case envtypes.PlanStatusCompleted:
			summary.Completed++
		case envtypes.PlanStatusCancelled:
			summary.Cancelled++
		}
	}

	return summary
}

func getPlanChanges(before envtypes.Plan, after envtypes.Plan) []trace.PlanToolChange {
	beforeByID := make(map[string]envtypes.PlanStep, len(before.Steps))
	beforeIndexByID := make(map[string]int, len(before.Steps))
	afterByID := make(map[string]envtypes.PlanStep, len(after.Steps))
	afterIndexByID := make(map[string]int, len(after.Steps))

	for index, step := range before.Steps {
		beforeByID[step.ID] = step
		beforeIndexByID[step.ID] = index + 1
	}
	for index, step := range after.Steps {
		afterByID[step.ID] = step
		afterIndexByID[step.ID] = index + 1
	}

	changes := make([]trace.PlanToolChange, 0)
	for _, step := range after.Steps {
		previous, existed := beforeByID[step.ID]
		if !existed {
			changes = append(changes, trace.PlanToolChange{
				Index:  afterIndexByID[step.ID],
				ID:     step.ID,
				Action: "added",
			})
			continue
		}
		if previous.Content == step.Content && previous.Status == step.Status {
			continue
		}

		action := "updated"
		switch step.Status {
		case envtypes.PlanStatusCompleted:
			if previous.Status != step.Status {
				action = "completed"
			}
		case envtypes.PlanStatusCancelled:
			if previous.Status != step.Status {
				action = "cancelled"
			}
		}
		changes = append(changes, trace.PlanToolChange{
			Index:  afterIndexByID[step.ID],
			ID:     step.ID,
			Action: action,
			Fields: getPlanStepChangedFields(previous, step),
		})
	}
	for _, step := range before.Steps {
		if _, ok := afterByID[step.ID]; ok {
			continue
		}

		changes = append(changes, trace.PlanToolChange{
			Index:  beforeIndexByID[step.ID],
			ID:     step.ID,
			Action: "removed",
		})
	}
	if len(changes) == 0 {
		return nil
	}

	return changes
}

func getPlanStepChangedFields(before envtypes.PlanStep, after envtypes.PlanStep) []string {
	fields := make([]string, 0, 2)
	if before.Status != after.Status {
		fields = append(fields, "status")
	}
	if before.Content != after.Content {
		fields = append(fields, "content")
	}

	return fields
}

func recordPlanEvent(ctx context.Context, sessionID string, plan envtypes.Plan, changes []trace.PlanToolChange) {
	recorder := tools.TraceRecorderFromContext(ctx)
	if recorder == nil {
		return
	}
	explanationValue3 := str.String(plan.Explanation)
	recorder.Record(trace.EvtPlanUpdated, trace.PlanEventPayload{
		SessionID:    sessionID,
		Steps:        planStepsToTracePayload(plan.Steps),
		Summary:      planSummaryToTracePayload(summarizePlan(plan)),
		ActiveStepID: getActivePlanStepID(plan),
		Explanation:  explanationValue3.Trim(),
		Changes:      append([]trace.PlanToolChange(nil), changes...),
	})
}

func recordPlanCleared(ctx context.Context, sessionID string, plan envtypes.Plan) {
	recorder := tools.TraceRecorderFromContext(ctx)
	if recorder == nil {
		return
	}
	explanationValue4 := str.String(plan.Explanation)
	recorder.Record(trace.EvtPlanCleared, trace.PlanEventPayload{
		SessionID:    sessionID,
		Steps:        planStepsToTracePayload(plan.Steps),
		Summary:      planSummaryToTracePayload(summarizePlan(plan)),
		ActiveStepID: "",
		Explanation:  explanationValue4.Trim(),
	})
}

func planStepsToTracePayload(steps []envtypes.PlanStep) []trace.PlanStepPayload {
	if len(steps) == 0 {
		return nil
	}

	payload := make([]trace.PlanStepPayload, 0, len(steps))
	for _, step := range steps {
		payload = append(payload, trace.PlanStepPayload{
			ID:      step.ID,
			Content: step.Content,
			Status:  string(step.Status),
		})
	}

	return payload
}

func planSummaryToTracePayload(summary envtypes.PlanSummary) trace.PlanSummaryPayload {
	return trace.PlanSummaryPayload{
		Total:      summary.Total,
		Pending:    summary.Pending,
		InProgress: summary.InProgress,
		Completed:  summary.Completed,
		Cancelled:  summary.Cancelled,
	}
}

func getActivePlanStepID(plan envtypes.Plan) string {
	for _, step := range plan.Steps {
		if step.Status == envtypes.PlanStatusInProgress {
			return step.ID
		}
	}

	return ""
}

type invalidPlanError string

func (e invalidPlanError) Error() string {
	return string(e)
}

func errInvalidPlan(message string) error {
	return invalidPlanError(message)
}
