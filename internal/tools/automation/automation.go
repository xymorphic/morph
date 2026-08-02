package automation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	envtypes "github.com/xymorphic/morph/internal/environment/types"
	"github.com/xymorphic/morph/internal/permissions"
	storage "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/internal/tools"
	"github.com/xymorphic/morph/internal/tools/common"
)

type input struct {
	Action          string                `json:"action"`
	ID              string                `json:"id,omitempty"`
	Job             storage.AutomationJob `json:"job,omitempty"`
	Query           jobQueryInput         `json:"query,omitempty"`
	RunQuery        runQueryInput         `json:"run_query,omitempty"`
	CaptureContext  bool                  `json:"capture_context,omitempty"`
	IncludeDisabled bool                  `json:"include_disabled,omitempty"`
	payloadUpdate   *payloadUpdateInput
	deliveryUpdate  *deliveryUpdateInput
}

type payloadUpdateInput struct {
	Kind          *storage.AutomationPayloadKind `json:"kind"`
	Prompt        *string                        `json:"prompt"`
	SystemEvent   *string                        `json:"system_event"`
	Model         *string                        `json:"model"`
	Provider      *string                        `json:"provider"`
	BaseURL       *string                        `json:"base_url"`
	NoTimeout     *bool                          `json:"no_timeout"`
	MaxRuntime    *time.Duration                 `json:"max_runtime"`
	MaxIterations *int                           `json:"max_iterations"`
	RetryAttempts *int                           `json:"retry_attempts"`
	RetryBackoff  *time.Duration                 `json:"retry_backoff"`
	RetryMaxDelay *time.Duration                 `json:"retry_max_delay"`
	ToolGroups    *[]string                      `json:"tool_groups"`
	Metadata      *map[string]string             `json:"metadata"`
}

type deliveryUpdateInput struct {
	Mode            *storage.AutomationDeliveryMode `json:"mode"`
	Channel         *string                         `json:"channel"`
	Target          *string                         `json:"target"`
	ThreadID        *string                         `json:"thread_id"`
	WebhookURL      *string                         `json:"webhook_url"`
	BestEffort      *bool                           `json:"best_effort"`
	FailureTarget   *string                         `json:"failure_target"`
	FailureAfter    *int                            `json:"failure_after"`
	FailureCooldown *time.Duration                  `json:"failure_cooldown"`
}

func (req *input) UnmarshalJSON(data []byte) error {
	normalized, err := normalizeToolInputJSON(data)
	if err != nil {
		return err
	}
	data = normalized

	type inputValue input
	var value inputValue
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var updates struct {
		Job struct {
			SessionTarget  *string              `json:"session_target"`
			DeleteAfterRun *bool                `json:"delete_after_run"`
			Payload        *payloadUpdateInput  `json:"payload"`
			Delivery       *deliveryUpdateInput `json:"delivery"`
		} `json:"job"`
	}
	if err := json.Unmarshal(data, &updates); err != nil {
		return err
	}

	*req = input(value)
	if updates.Job.SessionTarget != nil {
		req.Job.SessionTarget = *updates.Job.SessionTarget
	}
	if updates.Job.DeleteAfterRun != nil {
		req.Job.DeleteAfterRun = *updates.Job.DeleteAfterRun
	}
	req.payloadUpdate = updates.Job.Payload
	req.deliveryUpdate = updates.Job.Delivery
	if req.payloadUpdate != nil {
		req.Job.Payload = applyPayloadUpdate(req.Job.Payload, *req.payloadUpdate)
	}
	if req.deliveryUpdate != nil {
		req.Job.Delivery = applyDeliveryUpdate(req.Job.Delivery, *req.deliveryUpdate)
	}

	return nil
}

func normalizeToolInputJSON(data []byte) ([]byte, error) {
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}

	job, _ := value["job"].(map[string]any)
	schedule, _ := job["schedule"].(map[string]any)
	at, exists := schedule["at"]
	if !exists || at == nil {
		return data, nil
	}

	atText, ok := at.(string)
	if !ok {
		return nil, errors.New("job.schedule.at must be an RFC3339 string or null")
	}
	if atText == "" {
		delete(schedule, "at")
		return json.Marshal(value)
	}
	if _, err := time.Parse(time.RFC3339, atText); err != nil {
		return nil, errors.New("job.schedule.at must be an RFC3339 timestamp")
	}

	return data, nil
}

type jobQueryInput struct {
	IDs             []string `json:"ids,omitempty"`
	Enabled         *bool    `json:"enabled,omitempty"`
	Profile         string   `json:"profile,omitempty"`
	SessionTarget   string   `json:"session_target,omitempty"`
	Limit           int      `json:"limit,omitempty"`
	IncludeDisabled bool     `json:"include_disabled,omitempty"`
}

type runQueryInput struct {
	JobID  string   `json:"job_id,omitempty"`
	IDs    []string `json:"ids,omitempty"`
	Status []string `json:"status,omitempty"`
	Limit  int      `json:"limit,omitempty"`
}

type output struct {
	Status string                  `json:"status"`
	Job    storage.AutomationJob   `json:"job"`
	Jobs   []storage.AutomationJob `json:"jobs,omitempty"`
	Run    storage.AutomationRun   `json:"run"`
	Runs   []storage.AutomationRun `json:"runs,omitempty"`
	Counts map[string]int          `json:"counts,omitempty"`
}

func Definition(runtime envtypes.Runtime) tools.Definition {
	return tools.Definition{
		Name:          "automation",
		Description:   "Owner-only automation control: status, list, add, update, pause, resume, run, remove, and runs.",
		Groups:        []string{"core"},
		SemanticIndex: tools.SkipSemanticIndex(),
		Permission: permissions.Operation{
			Resource:      permissions.ResourceAutomation,
			Action:        permissions.ActionManage,
			Effects:       []permissions.Effect{permissions.EffectRead, permissions.EffectWrite, permissions.EffectExternalSystem},
			OwnerRequired: true,
		},
		ResolvePermission: resolvePermission,
		InputSchema: common.ObjectSchema(map[string]any{
			"action": common.StringSchema(
				"Required. Action: status, list, add, update, pause, resume, run, remove, or runs. " +
					"Use status/list/runs for reads; use add/update/pause/resume/run/remove for mutations.",
			),
			"id": common.StringSchema(
				"Automation job id. Required for update, pause, resume, run, and remove. " +
					"Not used for status, list, add, or runs.",
			),
			"job": jobSchema(),
			"query": describedObjectSchema("Only used when action=list. Filters automation jobs.", map[string]any{
				"ids":              stringArraySchema("Automation job ids."),
				"enabled":          common.BooleanSchema("Filter by enabled state."),
				"profile":          common.StringSchema("Filter by profile."),
				"session_target":   common.StringSchema("Filter by session target."),
				"limit":            common.IntegerSchema("Maximum jobs to return."),
				"include_disabled": common.BooleanSchema("Include disabled jobs."),
			}),
			"run_query": describedObjectSchema("Only used when action=runs. Filters automation run history.", map[string]any{
				"job_id": common.StringSchema("Filter by automation job id. Usually set this for action=runs."),
				"ids":    stringArraySchema("Automation run ids."),
				"status": stringArraySchema("Run statuses."),
				"limit":  common.IntegerSchema("Maximum runs to return."),
			}),
			"capture_context": common.BooleanSchema(
				"Only used for action=add. When true, capture the active session as job origin metadata.",
			),
			"include_disabled": common.BooleanSchema(
				"Only used by action=status and action=list. Include disabled jobs in counts/listing.",
			),
		}, "action"),
		Handler: tools.HandlerFunc(func(ctx context.Context, call tools.Call) (tools.Result, error) {
			var req input
			if result := decodeInput(call, &req); result.Error != "" {
				return result, nil
			}
			service, err := getService(ctx, runtime)
			if err != nil {
				return common.ToolError("tool_error", err.Error()), nil
			}

			result, err := invoke(ctx, service, req)
			if err != nil {
				return common.ToolError("invalid_input", err.Error()), nil
			}

			return common.EncodeOutput(result)
		}),
	}
}

func resolvePermission(_ context.Context, call tools.Call) ([]permissions.EvaluationInput, error) {
	raw := call.Input
	if strings.TrimSpace(raw) == "" {
		raw = "{}"
	}
	var req input
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		return nil, tools.NewPermissionResolutionError("invalid_input", err.Error())
	}

	action := strings.TrimSpace(req.Action)
	operation := permissions.Operation{
		Resource: permissions.ResourceAutomation,
	}
	switch action {
	case "status":
		operation.Action = permissions.ActionRead
		operation.Effects = []permissions.Effect{permissions.EffectRead}
	case "list", "runs":
		operation.Action = permissions.ActionList
		operation.Effects = []permissions.Effect{permissions.EffectRead}
		if action == "runs" {
			operation.Target = strings.TrimSpace(req.RunQuery.JobID)
		}
	case "add":
		operation.Action = permissions.ActionCreate
		operation.Effects = []permissions.Effect{permissions.EffectExternalSystem, permissions.EffectWrite}
		operation.Target = strings.TrimSpace(req.Job.ID)
		operation.OwnerRequired = true
	case "update", "pause", "resume":
		operation.Action = permissions.ActionUpdate
		operation.Effects = []permissions.Effect{permissions.EffectExternalSystem, permissions.EffectWrite}
		operation.Target = strings.TrimSpace(req.ID)
		operation.OwnerRequired = true
	case "run":
		operation.Action = permissions.ActionTrigger
		operation.Effects = []permissions.Effect{permissions.EffectExecution, permissions.EffectExternalSystem}
		operation.Target = strings.TrimSpace(req.ID)
		operation.OwnerRequired = true
	case "remove":
		operation.Action = permissions.ActionDelete
		operation.Effects = []permissions.Effect{
			permissions.EffectDestructive,
			permissions.EffectExternalSystem,
			permissions.EffectWrite,
		}
		operation.Target = strings.TrimSpace(req.ID)
		operation.OwnerRequired = true
	default:
		return nil, tools.NewPermissionResolutionError(
			"invalid_input",
			"unsupported action \""+action+"\"",
		)
	}

	return []permissions.EvaluationInput{{Operation: operation}}, nil
}

func decodeInput(call tools.Call, target any) tools.Result {
	if strings.TrimSpace(call.Input) == "" {
		call.Input = "{}"
	}
	if err := json.Unmarshal([]byte(call.Input), target); err != nil {
		return common.ToolError("invalid_input", err.Error())
	}

	return tools.Result{}
}

func getService(ctx context.Context, runtime envtypes.Runtime) (envtypes.AutomationService, error) {
	if runtime == nil {
		return nil, errors.New("automation runtime is required")
	}

	service, ok, err := runtime.AutomationService(ctx)
	if err != nil {
		return nil, err
	}
	if !ok || service == nil {
		return nil, errors.New("automation service is not supported")
	}

	return service, nil
}

func invoke(ctx context.Context, service envtypes.AutomationService, req input) (output, error) {
	if service == nil {
		return output{}, errors.New("automation service is required")
	}

	switch req.Action {
	case "status":
		return automationStatus(ctx, service, req)
	case "list":
		list, err := service.List(ctx, jobQueryFromInput(req.Query, req.IncludeDisabled))
		return output{Status: "ok", Jobs: list.Jobs}, err
	case "add":
		job := req.Job.Clone()
		if req.CaptureContext {
			job = captureContext(ctx, job)
		}
		if err := checkToolJob(job); err != nil {
			return output{}, err
		}
		created, err := service.Add(ctx, job)
		return output{Status: "ok", Job: created}, err
	case "update":
		current, err := loadAutomationJobForUpdate(ctx, service, req)
		if err != nil {
			return output{}, err
		}
		patch, err := patchFromInputWithCurrent(req, current)
		if err != nil {
			return output{}, err
		}
		updated, err := service.Update(ctx, patch)
		return output{Status: "ok", Job: updated}, err
	case "pause":
		enabled := false
		updated, err := service.Update(ctx, storage.AutomationJobPatch{ID: req.ID, Enabled: &enabled})
		return output{Status: "ok", Job: updated}, err
	case "resume":
		enabled := true
		updated, err := service.Update(ctx, storage.AutomationJobPatch{ID: req.ID, Enabled: &enabled})
		return output{Status: "ok", Job: updated}, err
	case "run":
		run, err := service.Run(ctx, req.ID)
		return output{Status: "ok", Run: run}, err
	case "remove":
		return output{Status: "ok"}, service.Remove(ctx, req.ID)
	case "runs":
		list, err := service.Runs(ctx, runQueryFromInput(req.RunQuery))
		return output{Status: "ok", Runs: list.Runs}, err
	default:
		return output{}, errors.New("unsupported automation action")
	}
}

func automationStatus(ctx context.Context, service envtypes.AutomationService, req input) (output, error) {
	list, err := service.List(ctx, storage.AutomationJobQuery{IncludeDisabled: true})
	if err != nil {
		return output{}, err
	}

	counts := map[string]int{"jobs": len(list.Jobs)}
	for _, job := range list.Jobs {
		if job.Enabled {
			counts["enabled"]++
		}
		if !job.State.RunningAt.IsZero() {
			counts["running"]++
		}
	}

	return output{Status: "ok", Counts: counts}, nil
}

func captureContext(ctx context.Context, job storage.AutomationJob) storage.AutomationJob {
	sessionID := tools.SessionIDFromContext(ctx)
	if sessionID == "" {
		return job
	}
	if job.Payload.Metadata == nil {
		job.Payload.Metadata = map[string]string{}
	}
	job.Payload.Metadata["origin_session_id"] = sessionID
	if job.SessionTarget == "" {
		job.SessionTarget = "origin"
	}

	return job
}

func patchFromInput(req input) (storage.AutomationJobPatch, error) {
	return patchFromInputWithCurrent(req, storage.AutomationJob{})
}

func patchFromInputWithCurrent(
	req input,
	current storage.AutomationJob,
) (storage.AutomationJobPatch, error) {
	patch := storage.AutomationJobPatch{
		ID:            req.ID,
		Name:          stringPtr(req.Job.Name),
		Description:   stringPtr(req.Job.Description),
		Profile:       stringPtr(req.Job.Profile),
		SessionTarget: stringPtr(req.Job.SessionTarget),
	}
	if req.Job.Schedule.Kind != "" {
		if err := checkToolSchedule(req.Job.Schedule); err != nil {
			return storage.AutomationJobPatch{}, err
		}

		patch.Schedule = &req.Job.Schedule
	}
	if req.payloadUpdate != nil {
		payload := applyPayloadUpdate(current.Payload, *req.payloadUpdate)
		if err := checkToolPayload(payload); err != nil {
			return storage.AutomationJobPatch{}, err
		}
		patch.Payload = &payload
	} else if req.Job.Payload.Kind != "" || req.Job.Payload.Prompt != "" || req.Job.Payload.SystemEvent != "" {
		if err := checkToolPayload(req.Job.Payload); err != nil {
			return storage.AutomationJobPatch{}, err
		}

		patch.Payload = &req.Job.Payload
	}
	if req.deliveryUpdate != nil {
		delivery := applyDeliveryUpdate(current.Delivery, *req.deliveryUpdate)
		patch.Delivery = &delivery
	} else if req.Job.Delivery.Mode != "" {
		patch.Delivery = &req.Job.Delivery
	}

	return patch, nil
}

func loadAutomationJobForUpdate(
	ctx context.Context,
	service envtypes.AutomationService,
	req input,
) (storage.AutomationJob, error) {
	if req.payloadUpdate == nil && req.deliveryUpdate == nil {
		return storage.AutomationJob{}, nil
	}

	list, err := service.List(ctx, storage.AutomationJobQuery{
		IDs:             []string{req.ID},
		Limit:           1,
		IncludeDisabled: true,
	})
	if err != nil {
		return storage.AutomationJob{}, err
	}
	for _, job := range list.Jobs {
		if job.ID == req.ID {
			return job.Clone(), nil
		}
	}

	return storage.AutomationJob{}, errors.New("automation job not found")
}

func applyPayloadUpdate(
	payload storage.AutomationPayload,
	update payloadUpdateInput,
) storage.AutomationPayload {
	payload = payload.Clone()
	if update.Kind != nil {
		payload.Kind = *update.Kind
	}
	if update.Prompt != nil && (*update.Prompt != "" || payload.Kind == storage.AutomationPayloadPrompt) {
		payload.Kind = storage.AutomationPayloadPrompt
		payload.Prompt = *update.Prompt
		payload.SystemEvent = ""
	}
	if update.SystemEvent != nil && (*update.SystemEvent != "" || payload.Kind == storage.AutomationPayloadSystemEvent) {
		payload.Kind = storage.AutomationPayloadSystemEvent
		payload.Prompt = ""
		payload.SystemEvent = *update.SystemEvent
	}
	if update.Model != nil {
		payload.Model = *update.Model
	}
	if update.Provider != nil {
		payload.Provider = *update.Provider
	}
	if update.BaseURL != nil {
		payload.BaseURL = *update.BaseURL
	}
	if update.NoTimeout != nil {
		payload.NoTimeout = *update.NoTimeout
	}
	if update.MaxRuntime != nil {
		payload.MaxRuntime = *update.MaxRuntime
	}
	if update.MaxIterations != nil {
		payload.MaxIterations = *update.MaxIterations
	}
	if update.RetryAttempts != nil {
		payload.RetryAttempts = *update.RetryAttempts
	}
	if update.RetryBackoff != nil {
		payload.RetryBackoff = *update.RetryBackoff
	}
	if update.RetryMaxDelay != nil {
		payload.RetryMaxDelay = *update.RetryMaxDelay
	}
	if update.ToolGroups != nil {
		payload.ToolGroups = append([]string(nil), (*update.ToolGroups)...)
	}
	if update.Metadata != nil {
		payload.Metadata = cloneStringMap(*update.Metadata)
	}

	return payload
}

func applyDeliveryUpdate(
	delivery storage.AutomationDelivery,
	update deliveryUpdateInput,
) storage.AutomationDelivery {
	if update.Mode != nil {
		delivery.Mode = *update.Mode
		switch delivery.Mode {
		case storage.AutomationDeliveryNone, storage.AutomationDeliveryLocal:
			delivery.Channel = ""
			delivery.Target = ""
			delivery.ThreadID = ""
			delivery.WebhookURL = ""
		case storage.AutomationDeliveryOrigin, storage.AutomationDeliveryGateway:
			delivery.WebhookURL = ""
		case storage.AutomationDeliveryWebhook:
			delivery.Channel = ""
			delivery.Target = ""
			delivery.ThreadID = ""
		}
	}
	if update.Channel != nil {
		delivery.Channel = *update.Channel
	}
	if update.Target != nil {
		delivery.Target = *update.Target
	}
	if update.ThreadID != nil {
		delivery.ThreadID = *update.ThreadID
	}
	if update.WebhookURL != nil {
		delivery.WebhookURL = *update.WebhookURL
	}
	if update.BestEffort != nil {
		delivery.BestEffort = *update.BestEffort
	}
	if update.FailureTarget != nil {
		delivery.FailureTarget = *update.FailureTarget
	}
	if update.FailureAfter != nil {
		delivery.FailureAfter = *update.FailureAfter
	}
	if update.FailureCooldown != nil {
		delivery.FailureCooldown = *update.FailureCooldown
	}

	return delivery
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}

	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}

	return cloned
}

func jobQueryFromInput(input jobQueryInput, includeDisabled bool) storage.AutomationJobQuery {
	return storage.AutomationJobQuery{
		IDs:             append([]string(nil), input.IDs...),
		Enabled:         input.Enabled,
		Profile:         input.Profile,
		SessionTarget:   input.SessionTarget,
		Limit:           input.Limit,
		IncludeDisabled: input.IncludeDisabled || includeDisabled,
	}
}

func runQueryFromInput(input runQueryInput) storage.AutomationRunQuery {
	statuses := make([]storage.AutomationRunStatus, 0, len(input.Status))
	for _, value := range input.Status {
		statuses = append(statuses, storage.AutomationRunStatus(value))
	}

	return storage.AutomationRunQuery{
		JobID:  input.JobID,
		IDs:    append([]string(nil), input.IDs...),
		Status: statuses,
		Limit:  input.Limit,
	}
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}

	return &value
}

func checkToolJob(job storage.AutomationJob) error {
	if err := checkToolSchedule(job.Schedule); err != nil {
		return err
	}
	if job.Delivery.Mode == storage.AutomationDeliveryOrigin &&
		(job.Delivery.Channel == "" || job.Delivery.Target == "") {
		return errors.New(
			"automation origin delivery requires a Slack or Telegram channel and target; " +
				"TUI sessions should use local delivery with an explicit session target",
		)
	}

	return checkToolPayload(job.Payload)
}

func checkToolSchedule(schedule storage.AutomationSchedule) error {
	switch schedule.Kind {
	case storage.AutomationScheduleAt:
		if schedule.At.IsZero() {
			return errors.New("automation one-shot schedule time is required")
		}
	case storage.AutomationScheduleEvery:
		if schedule.Every <= 0 {
			return errors.New("automation interval schedule must be greater than zero")
		}
	case storage.AutomationScheduleCron:
		if schedule.Cron == "" {
			return errors.New("automation cron schedule expression is required")
		}
	default:
		return errors.New("automation schedule kind is required")
	}

	return nil
}

func checkToolPayload(payload storage.AutomationPayload) error {
	switch payload.Kind {
	case "", storage.AutomationPayloadPrompt:
		if payload.Prompt == "" {
			return errors.New("automation prompt payload is required")
		}
	case storage.AutomationPayloadSystemEvent:
		if payload.SystemEvent == "" {
			return errors.New("automation system event payload is required")
		}
	default:
		return errors.New("unsupported automation payload kind")
	}

	return nil
}

func jobSchema() map[string]any {
	return common.ObjectSchema(map[string]any{
		"id":          common.StringSchema("Optional for action=add. Ignored for action=update; use top-level id there."),
		"name":        common.StringSchema("Human-readable job name."),
		"description": common.StringSchema("Human-readable job description."),
		"enabled":     common.BooleanSchema("Whether the job is enabled."),
		"profile":     common.StringSchema("Profile to run with."),
		"session_target": common.StringSchema(
			"Session target: isolated, main, current, origin, or session:<id>. " +
				"For TUI-created jobs, use session:<current-session-id> with local delivery.",
		),
		"delete_after_run": common.BooleanSchema("Delete after a successful run."),
		"schedule": common.ObjectSchema(map[string]any{
			"kind": common.StringSchema(
				"Schedule kind. Required when setting schedule. Use at, every, or cron. " +
					"If kind=at, at is required. If kind=every, every is required. If kind=cron, cron is required.",
			),
			"at": common.StringSchema(
				"RFC3339 timestamp. Required when schedule.kind=at; ignored for every/cron schedules.",
			),
			"every": common.IntegerSchema(
				"Interval duration in nanoseconds. Required and must be greater than zero when schedule.kind=every.",
			),
			"cron": common.StringSchema(
				"Cron expression. Required when schedule.kind=cron.",
			),
			"timezone": common.StringSchema("Optional IANA timezone name for cron schedules."),
		}),
		"payload": common.ObjectSchema(map[string]any{
			"kind": common.StringSchema(
				"Payload kind. Use prompt or system_event. Defaults to prompt when omitted. " +
					"If kind=prompt or omitted, prompt is required. If kind=system_event, system_event is required.",
			),
			"prompt": common.StringSchema(
				"Prompt for agent-turn jobs. Required when payload.kind=prompt or payload.kind is omitted.",
			),
			"system_event": common.StringSchema(
				"System event value. Required when payload.kind=system_event.",
			),
			"model":           common.StringSchema("Model override."),
			"provider":        common.StringSchema("Provider override."),
			"base_url":        common.StringSchema("Provider base URL override. Use with provider/model overrides when needed."),
			"no_timeout":      common.BooleanSchema("Disable run timeout. When true, max_runtime is ignored."),
			"max_runtime":     common.IntegerSchema("Max runtime in nanoseconds. Ignored when no_timeout=true."),
			"max_iterations":  common.IntegerSchema("Max agent iterations."),
			"retry_attempts":  common.IntegerSchema("Retry attempts."),
			"retry_backoff":   common.IntegerSchema("Initial retry backoff in nanoseconds."),
			"retry_max_delay": common.IntegerSchema("Maximum retry delay in nanoseconds."),
			"tool_groups":     stringArraySchema("Allowed tool groups."),
			"metadata": map[string]any{
				"type":                 "object",
				"additionalProperties": map[string]any{"type": "string"},
			},
		}),
		"delivery": common.ObjectSchema(map[string]any{
			"mode": common.StringSchema(
				"Delivery mode: none, local, origin, gateway, or webhook. " +
					"Use local for TUI sessions. If mode=webhook, webhook_url is required. " +
					"If mode=gateway or origin, a Slack or Telegram channel and target are required.",
			),
			"channel":     common.StringSchema("Delivery channel. Usually required for gateway/origin delivery."),
			"target":      common.StringSchema("Delivery target. Usually required for gateway/origin delivery."),
			"thread_id":   common.StringSchema("Delivery thread id."),
			"webhook_url": common.StringSchema("Webhook URL. Required when delivery.mode=webhook."),
			"best_effort": common.BooleanSchema("Do not fail the run if delivery fails."),
			"failure_target": common.StringSchema(
				"Failure-notice target. Used only when failure_after is greater than zero.",
			),
			"failure_after": common.IntegerSchema(
				"Consecutive failures before notifying. Set greater than zero to enable failure notices.",
			),
			"failure_cooldown": common.IntegerSchema(
				"Failure notification cooldown in nanoseconds. Used only when failure_after is greater than zero.",
			),
		}),
	})
}

func describedObjectSchema(description string, properties map[string]any, required ...string) map[string]any {
	schema := common.ObjectSchema(properties, required...)
	schema["description"] = description
	return schema
}

func stringArraySchema(description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": description,
		"items":       map[string]any{"type": "string"},
	}
}
