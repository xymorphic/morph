package agent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/xymorphic/morph/internal/constants"
	envtypes "github.com/xymorphic/morph/internal/environment/types"
	"github.com/xymorphic/morph/internal/trace"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	agentsession "github.com/xymorphic/morph/pkg/agent/session"
	"github.com/xymorphic/morph/pkg/str"
)

// Number of tool messages to retrieve at a time while searching for a plan.
const planHydrationPageSize = constants.PlanHydrationPageSize

// hydratePlanFromMessages attempts to hydrate the plan from a slice of messages.
// It returns true if a plan was found and hydrated in the environment, otherwise false.
// If not found, it hydrates an empty plan into the environment as a fallback.
func (t *Turn) hydratePlanFromMessages(messages []morphmsg.Message) bool {
	if t == nil || (t.plans == nil && t.env == nil) {
		return false
	}

	empty := envtypes.Plan{}

	for _, message := range messages {

		// Look for assistant "tool" messages with the plan tool name.
		if message.Role != morphmsg.RoleTool || message.Name != "plan_tool" {
			continue
		}

		// Attempt to decode a plan from the message content.
		plan, ok := decodeHydratedPlan(message.Content)
		if !ok {
			continue
		}

		t.hydratePlan(t.getStateSessionID(), plan)
		return true
	}

	// No plan found, hydrate empty.
	t.hydratePlan(t.getStateSessionID(), empty)
	return false
}

// renderPlanInstructions returns an instruction-formatted string representation for the active plan.
// Returns a Markdown string describing current active plan steps and optional plan explanation.
func (t *Turn) renderPlanInstructions() string {
	if t == nil {
		return ""
	}

	plan := t.currentPlan(t.getStateSessionID())

	// Filter for steps that are not completed/cancelled.
	activeSteps := make([]envtypes.PlanStep, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		if step.Status == envtypes.PlanStatusCompleted || step.Status == envtypes.PlanStatusCancelled {
			continue
		}

		activeSteps = append(activeSteps, step)
	}

	if len(activeSteps) == 0 {
		return ""
	}

	lines := []string{
		"# Plan Context",
		"",
		"## Active Plan",
	}
	for _, step := range activeSteps {
		lines = append(lines, "- ["+step.Status+"] "+step.Content)
	}
	explanation := str.String(plan.Explanation)
	if explanation := explanation.Trim(); explanation != "" {
		lines = append(lines, "", "## Plan Update Reason", "", explanation)
	}

	return strings.Join(lines, "\n")
}

// decodeHydratedPlan tries to decode a plan from JSON content, supporting both
// direct plan objects and tool envelope {"output": "<plan json>"}.
// Returns the plan and ok==true if successful.
func decodeHydratedPlan(content string) (envtypes.Plan, bool) {
	type toolMessageEnvelope struct {
		Output string `json:"output"`
	}

	var envelope toolMessageEnvelope
	if err := json.Unmarshal([]byte(content), &envelope); err == nil {
		output := str.String(envelope.Output)
		if output.Trim() != "" {
			if plan, ok := decodeHydratedPlanPayload(envelope.Output); ok {
				return plan, true
			}
		}
	}

	return decodeHydratedPlanPayload(content)
}

// decodeHydratedPlanPayload decodes a plan directly from a JSON object, expecting a "steps" array.
// If the plan is valid per ValidatePlan, returns the plan and ok==true.
func decodeHydratedPlanPayload(content string) (envtypes.Plan, bool) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return envtypes.Plan{}, false
	}

	stepsRaw, ok := raw["steps"]
	if !ok {
		return envtypes.Plan{}, false
	}

	var steps []envtypes.PlanStep
	if err := json.Unmarshal(stepsRaw, &steps); err != nil {
		return envtypes.Plan{}, false
	}

	explanation := ""
	if explanationRaw, ok := raw["explanation"]; ok {
		_ = json.Unmarshal(explanationRaw, &explanation)
	}
	explanationValue := str.String(explanation)
	plan := envtypes.Plan{
		Steps:       steps,
		Explanation: explanationValue.Trim(),
	}

	if err := envtypes.ValidatePlan(plan); err != nil {
		return envtypes.Plan{}, false
	}

	return plan, true
}

// hydratePlanFromHistory attempts to find and hydrate the most recent plan from tool messages
// in the session's message history (paginated to avoid large loads).
// Returns true if hydration occurred successfully, or false with error if not.
func (t *Turn) hydratePlanFromHistory(ctx context.Context, sessionID string) (bool, error) {
	offset := 0

	for {
		// Load a page of tool messages that might contain a plan.
		messages, err := t.sessionStore.GetMessages(ctx, sessionID, agentsession.MessageQuery{
			Role:   morphmsg.RoleTool,
			Name:   "plan_tool",
			Order:  agentsession.MessageOrderDesc,
			Limit:  planHydrationPageSize,
			Offset: offset,
		})
		if err != nil {
			return false, err
		}

		// If there are no more messages, hydrate empty and stop.
		if len(messages) == 0 {
			t.hydratePlan(sessionID, envtypes.Plan{})
			return false, nil
		}

		// If any plan is found in this batch, hydrate and return.
		if t.hydratePlanFromMessages(messages) {
			return true, nil
		}

		offset += len(messages)
	}
}

// summarizeHydratedPlan generates a summary statistic (counts of each status) for a given plan.
func summarizeHydratedPlan(plan envtypes.Plan) envtypes.PlanSummary {
	summary := envtypes.PlanSummary{
		Total: len(plan.Steps),
	}

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

// hydratedPlanStepsToTracePayload converts plan steps into a trace payload slice for tracing.
func hydratedPlanStepsToTracePayload(steps []envtypes.PlanStep) []trace.PlanStepPayload {
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

// hydratedPlanSummaryToTracePayload converts a plan summary into a trace-friendly struct.
func hydratedPlanSummaryToTracePayload(summary envtypes.PlanSummary) trace.PlanSummaryPayload {
	return trace.PlanSummaryPayload{
		Total:      summary.Total,
		Pending:    summary.Pending,
		InProgress: summary.InProgress,
		Completed:  summary.Completed,
		Cancelled:  summary.Cancelled,
	}
}

// getActiveHydratedPlanStepID returns the ID of the in-progress plan step, if any, else empty string.
func getActiveHydratedPlanStepID(plan envtypes.Plan) string {
	for _, step := range plan.Steps {
		if step.Status == envtypes.PlanStatusInProgress {
			return step.ID
		}
	}

	return ""
}
