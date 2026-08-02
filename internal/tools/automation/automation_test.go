package automation

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/permissions"
	storage "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/internal/state/storememory"
	"github.com/xymorphic/morph/internal/tools"
	toolmocks "github.com/xymorphic/morph/internal/tools/mocks"
)

var testAutomationToolJobID = "auto_projectaprojectaproje"

func TestDefinition_AddCapturesContext(t *testing.T) {
	store := storememory.NewStore()
	service := &automationToolServiceStub{store: store}
	definition := Definition(&toolmocks.Runtime{
		AutomationServiceValue: service,
		AutomationServiceOK:    true,
	})
	ctx := tools.WithSessionID(context.Background(), "ses_projectaprojectaproje")

	result, err := definition.Handler.Invoke(ctx, tools.Call{Input: `{
		"action": "add",
		"capture_context": true,
		"job": {
			"id": "` + testAutomationToolJobID + `",
			"enabled": true,
			"schedule": {"kind": "every", "every": 3600000000000},
			"payload": {"kind": "prompt", "prompt": "summarize"}
		}
	}`})

	require.NoError(t, err)
	require.Empty(t, result.Error)
	var out output
	require.NoError(t, json.Unmarshal([]byte(result.Output), &out))
	require.Equal(t, "origin", out.Job.SessionTarget)
	require.Equal(t, "ses_projectaprojectaproje", out.Job.Payload.Metadata["origin_session_id"])
}

func TestDefinition_EnforcementDeniesBeforeAutomationMutation(t *testing.T) {
	store := storememory.NewStore()
	_, err := store.CreateJob(context.Background(), storage.AutomationJob{
		ID: testAutomationToolJobID, Name: "keep", Enabled: true,
	})
	require.NoError(t, err)
	service := &automationToolServiceStub{store: store}
	registry := tools.NewDefaultRegistry(tools.RegistryOptions{PermissionPolicy: permissions.Policy{
		Rules: []permissions.Rule{{
			Name: "deny automation deletes", Actions: []permissions.Action{permissions.ActionDelete}, Decision: permissions.DecisionDeny,
		}},
	}})
	require.NoError(t, registry.Register(Definition(&toolmocks.Runtime{
		AutomationServiceValue: service,
		AutomationServiceOK:    true,
	})))
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor: permissions.Actor{Kind: permissions.ActorLocalOwner}, Surface: permissions.SurfaceCLI,
	})

	result, err := registry.Invoke(ctx, tools.Call{
		Name: "automation", Input: `{"action":"remove","id":"` + testAutomationToolJobID + `"}`,
	})

	require.NoError(t, err)
	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, permissions.ErrorCodeDenied, toolErr.Code)
	jobs, err := store.ListJobs(context.Background(), storage.AutomationJobQuery{IDs: []string{testAutomationToolJobID}})
	require.NoError(t, err)
	require.Len(t, jobs.Jobs, 1)
}

func TestDefinition_ResolvePermissionClassifiesActions(t *testing.T) {
	resolver := Definition(&toolmocks.Runtime{}).ResolvePermission
	tests := []struct {
		name    string
		input   string
		action  permissions.Action
		effects []permissions.Effect
		target  string
		owner   bool
	}{
		{name: "status", input: `{"action":"status"}`, action: permissions.ActionRead, effects: []permissions.Effect{permissions.EffectRead}},
		{name: "list", input: `{"action":"list"}`, action: permissions.ActionList, effects: []permissions.Effect{permissions.EffectRead}},
		{name: "runs", input: `{"action":"runs","run_query":{"job_id":"job-1"}}`, action: permissions.ActionList, effects: []permissions.Effect{permissions.EffectRead}, target: "job-1"},
		{name: "add", input: `{"action":"add","job":{"id":"job-1"}}`, action: permissions.ActionCreate, effects: []permissions.Effect{permissions.EffectExternalSystem, permissions.EffectWrite}, target: "job-1", owner: true},
		{name: "update", input: `{"action":"update","id":"job-1"}`, action: permissions.ActionUpdate, effects: []permissions.Effect{permissions.EffectExternalSystem, permissions.EffectWrite}, target: "job-1", owner: true},
		{name: "pause", input: `{"action":"pause","id":"job-1"}`, action: permissions.ActionUpdate, effects: []permissions.Effect{permissions.EffectExternalSystem, permissions.EffectWrite}, target: "job-1", owner: true},
		{name: "resume", input: `{"action":"resume","id":"job-1"}`, action: permissions.ActionUpdate, effects: []permissions.Effect{permissions.EffectExternalSystem, permissions.EffectWrite}, target: "job-1", owner: true},
		{name: "run", input: `{"action":"run","id":"job-1"}`, action: permissions.ActionTrigger, effects: []permissions.Effect{permissions.EffectExecution, permissions.EffectExternalSystem}, target: "job-1", owner: true},
		{name: "remove", input: `{"action":"remove","id":"job-1"}`, action: permissions.ActionDelete, effects: []permissions.Effect{permissions.EffectDestructive, permissions.EffectExternalSystem, permissions.EffectWrite}, target: "job-1", owner: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inputs, err := resolver(context.Background(), tools.Call{Input: test.input})

			require.NoError(t, err)
			require.Len(t, inputs, 1)
			require.Equal(t, permissions.ResourceAutomation, inputs[0].Operation.Resource)
			require.Equal(t, test.action, inputs[0].Operation.Action)
			require.Equal(t, test.effects, inputs[0].Operation.Effects)
			require.Equal(t, test.target, inputs[0].Operation.Target)
			require.Equal(t, test.owner, inputs[0].Operation.OwnerRequired)
		})
	}
}

func TestDefinition_ResolvePermissionRejectsInvalidAction(t *testing.T) {
	resolver := Definition(&toolmocks.Runtime{}).ResolvePermission

	inputs, err := resolver(context.Background(), tools.Call{Input: `{}`})
	require.EqualError(t, err, `unsupported action ""`)
	require.Nil(t, inputs)

	inputs, err = resolver(context.Background(), tools.Call{Input: `{"action":`})
	require.EqualError(t, err, "unexpected end of JSON input")
	require.Nil(t, inputs)

	inputs, err = resolver(context.Background(), tools.Call{})
	require.EqualError(t, err, `unsupported action ""`)
	require.Nil(t, inputs)
}

func TestDefinition_AddAcceptsStrictSchemaPlaceholders(t *testing.T) {
	store := storememory.NewStore()
	definition := Definition(&toolmocks.Runtime{
		AutomationServiceValue: &automationToolServiceStub{store: store},
		AutomationServiceOK:    true,
	})

	result, err := definition.Handler.Invoke(context.Background(), tools.Call{Input: `{
		"action":"add",
		"capture_context":false,
		"id":"",
		"include_disabled":false,
		"job":{
			"delete_after_run":false,
			"delivery":{
				"best_effort":false,
				"channel":"",
				"failure_after":0,
				"failure_cooldown":0,
				"failure_target":"",
				"mode":"local",
				"target":"",
				"thread_id":"",
				"webhook_url":""
			},
			"description":"Send the current Nigeria local time to this chat every 5 minutes.",
			"enabled":true,
			"id":"",
			"name":"Nigeria time every 5 minutes",
			"payload":{
				"base_url":"",
				"kind":"prompt",
				"max_iterations":3,
				"max_runtime":30000000000,
				"model":"",
				"no_timeout":false,
				"prompt":"Check the current time in Nigeria and send it here in Nigerian Pidgin. Keep it brief.",
				"provider":"",
				"retry_attempts":0,
				"retry_backoff":0,
				"retry_max_delay":0,
				"system_event":"",
				"tool_groups":["core"]
			},
			"profile":"default",
			"schedule":{"at":"","cron":"","every":300000000000,"kind":"every","timezone":""},
			"session_target":"main"
		},
		"query":{"enabled":false,"ids":[],"include_disabled":false,"limit":0,"profile":"","session_target":""},
		"run_query":{"ids":[],"job_id":"","limit":0,"status":[]}
	}`})

	require.NoError(t, err)
	require.Empty(t, result.Error)
	var out output
	require.NoError(t, json.Unmarshal([]byte(result.Output), &out))
	require.Equal(t, "Nigeria time every 5 minutes", out.Job.Name)
	require.Equal(t, "default", out.Job.Profile)
	require.Equal(t, "main", out.Job.SessionTarget)
	require.Equal(t, storage.AutomationDeliveryLocal, out.Job.Delivery.Mode)
	require.Equal(t, 5*time.Minute, out.Job.Schedule.Every)
	require.Equal(t, 30*time.Second, out.Job.Payload.MaxRuntime)
	require.Equal(t, []string{"core"}, out.Job.Payload.ToolGroups)
}

func TestDefinition_StatusIgnoresStrictSchemaJobPlaceholders(t *testing.T) {
	definition := Definition(&toolmocks.Runtime{
		AutomationServiceValue: &automationToolServiceStub{store: storememory.NewStore()},
		AutomationServiceOK:    true,
	})

	result, err := definition.Handler.Invoke(context.Background(), tools.Call{Input: `{
		"action":"status",
		"capture_context":false,
		"id":"",
		"include_disabled":true,
		"job":{
			"delivery":{"mode":"none"},
			"payload":{"kind":"prompt"},
			"schedule":{"at":null,"cron":null,"every":null,"kind":null,"timezone":null}
		},
		"query":{},
		"run_query":{}
	}`})

	require.NoError(t, err)
	require.Empty(t, result.Error)
}

func TestDefinition_ReturnsFieldLevelTimestampErrors(t *testing.T) {
	definition := Definition(&toolmocks.Runtime{
		AutomationServiceValue: &automationToolServiceStub{store: storememory.NewStore()},
		AutomationServiceOK:    true,
	})

	result, err := definition.Handler.Invoke(context.Background(), tools.Call{Input: `{
		"action":"add",
		"job":{
			"schedule":{"kind":"at","at":"tomorrow"},
			"payload":{"kind":"prompt","prompt":"notify"}
		}
	}`})

	require.NoError(t, err)
	require.Contains(t, result.Error, "job.schedule.at must be an RFC3339 timestamp")
}

func TestDefinition_UpdatePreservesUnspecifiedPayloadFields(t *testing.T) {
	store := storememory.NewStore()
	service := &automationToolServiceStub{store: store}
	ctx := context.Background()
	_, err := store.CreateJob(ctx, storage.AutomationJob{
		ID:      testAutomationToolJobID,
		Enabled: true,
		Schedule: storage.AutomationSchedule{
			Kind:  storage.AutomationScheduleEvery,
			Every: time.Hour,
		},
		Payload: storage.AutomationPayload{
			Kind:          storage.AutomationPayloadPrompt,
			Prompt:        "summarize",
			Model:         "old-model",
			Provider:      "openai",
			NoTimeout:     true,
			MaxRuntime:    time.Minute,
			MaxIterations: 4,
			RetryAttempts: 3,
			RetryBackoff:  time.Second,
			RetryMaxDelay: 10 * time.Second,
			ToolGroups:    []string{"core", "web"},
			Metadata:      map[string]string{"origin": "test"},
		},
		Delivery: storage.AutomationDelivery{
			Mode:            storage.AutomationDeliveryGateway,
			Channel:         "telegram",
			Target:          "old-target",
			ThreadID:        "topic",
			BestEffort:      true,
			FailureTarget:   "admin",
			FailureAfter:    3,
			FailureCooldown: time.Hour,
		},
	})
	require.NoError(t, err)
	definition := Definition(&toolmocks.Runtime{
		AutomationServiceValue: service,
		AutomationServiceOK:    true,
	})

	result, err := definition.Handler.Invoke(ctx, tools.Call{Input: `{
		"action": "update",
		"id": "` + testAutomationToolJobID + `",
		"job": {
			"payload": {
				"model": "new-model",
				"no_timeout": false,
				"max_runtime": 0,
				"tool_groups": []
			},
			"delivery": {"target": "new-target"}
		}
	}`})

	require.NoError(t, err)
	require.Empty(t, result.Error)
	var out output
	require.NoError(t, json.Unmarshal([]byte(result.Output), &out))
	require.Equal(t, storage.AutomationPayload{
		Kind:          storage.AutomationPayloadPrompt,
		Prompt:        "summarize",
		Model:         "new-model",
		Provider:      "openai",
		MaxIterations: 4,
		RetryAttempts: 3,
		RetryBackoff:  time.Second,
		RetryMaxDelay: 10 * time.Second,
		Metadata:      map[string]string{"origin": "test"},
	}, out.Job.Payload)
	require.Equal(t, storage.AutomationDelivery{
		Mode:            storage.AutomationDeliveryGateway,
		Channel:         "telegram",
		Target:          "new-target",
		ThreadID:        "topic",
		BestEffort:      true,
		FailureTarget:   "admin",
		FailureAfter:    3,
		FailureCooldown: time.Hour,
	}, out.Job.Delivery)
}

func TestDefinition_UpdateReturnsCurrentJobLookupErrors(t *testing.T) {
	expected := errors.New("list failed")
	definition := Definition(&toolmocks.Runtime{
		AutomationServiceValue: &automationToolServiceStub{listErr: expected},
		AutomationServiceOK:    true,
	})

	result, err := definition.Handler.Invoke(context.Background(), tools.Call{Input: `{
		"action": "update",
		"id": "` + testAutomationToolJobID + `",
		"job": {"payload": {"model": "new-model"}}
	}`})

	require.NoError(t, err)
	require.Contains(t, result.Error, expected.Error())
}

func TestInvoke_StatusAndRun(t *testing.T) {
	store := storememory.NewStore()
	service := &automationToolServiceStub{store: store}
	ctx := context.Background()
	_, err := store.CreateJob(ctx, storage.AutomationJob{
		ID:      testAutomationToolJobID,
		Enabled: true,
		State: storage.AutomationJobState{
			RunningAt: time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC),
		},
	})
	require.NoError(t, err)

	status, err := invoke(ctx, service, input{Action: "status"})
	require.NoError(t, err)
	require.Equal(t, map[string]int{"enabled": 1, "jobs": 1, "running": 1}, status.Counts)

	service.run = storage.AutomationRun{
		ID:     "autorun_projectaprojectapr",
		JobID:  testAutomationToolJobID,
		Status: storage.AutomationRunStatusOK,
	}
	run, err := invoke(ctx, service, input{Action: "run", ID: testAutomationToolJobID})
	require.NoError(t, err)
	require.Equal(t, "ok", run.Status)
	require.Equal(t, testAutomationToolJobID, service.runID)
	require.Equal(t, storage.AutomationRunStatusOK, run.Run.Status)
}

func TestInvoke_ManageJobAndRunHistory(t *testing.T) {
	store := storememory.NewStore()
	service := &automationToolServiceStub{store: store}
	ctx := context.Background()
	_, err := store.CreateJob(ctx, storage.AutomationJob{
		ID:       testAutomationToolJobID,
		Name:     "Daily",
		Enabled:  true,
		Schedule: storage.AutomationSchedule{Kind: storage.AutomationScheduleEvery, Every: time.Hour},
		Payload:  storage.AutomationPayload{Kind: storage.AutomationPayloadPrompt, Prompt: "summarize"},
	})
	require.NoError(t, err)
	run, err := store.CreateRun(ctx, storage.AutomationRun{JobID: testAutomationToolJobID})
	require.NoError(t, err)

	list, err := invoke(ctx, service, input{Action: "list", Query: jobQueryInput{IncludeDisabled: true}})
	require.NoError(t, err)
	require.Len(t, list.Jobs, 1)
	require.Equal(t, testAutomationToolJobID, list.Jobs[0].ID)

	updated, err := invoke(ctx, service, input{
		Action: "update",
		ID:     testAutomationToolJobID,
		Job: storage.AutomationJob{
			Name: "Renamed",
			Schedule: storage.AutomationSchedule{
				Kind:  storage.AutomationScheduleEvery,
				Every: 2 * time.Hour,
			},
			Payload: storage.AutomationPayload{
				Kind:   storage.AutomationPayloadPrompt,
				Prompt: "next",
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "Renamed", updated.Job.Name)
	require.Equal(t, 2*time.Hour, updated.Job.Schedule.Every)

	paused, err := invoke(ctx, service, input{Action: "pause", ID: testAutomationToolJobID})
	require.NoError(t, err)
	require.False(t, paused.Job.Enabled)

	resumed, err := invoke(ctx, service, input{Action: "resume", ID: testAutomationToolJobID})
	require.NoError(t, err)
	require.True(t, resumed.Job.Enabled)

	runs, err := invoke(ctx, service, input{
		Action:   "runs",
		RunQuery: runQueryInput{JobID: testAutomationToolJobID, Status: []string{string(storage.AutomationRunStatusRunning)}},
	})
	require.NoError(t, err)
	require.Equal(t, []storage.AutomationRun{run}, runs.Runs)

	removed, err := invoke(ctx, service, input{Action: "remove", ID: testAutomationToolJobID})
	require.NoError(t, err)
	require.Equal(t, "ok", removed.Status)
	_, ok, err := store.GetJob(ctx, testAutomationToolJobID)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestDefinition_ReturnsToolErrorWhenServiceUnsupported(t *testing.T) {
	definition := Definition(&toolmocks.Runtime{})

	result, err := definition.Handler.Invoke(context.Background(), tools.Call{Input: `{"action":"status"}`})

	require.NoError(t, err)
	require.Contains(t, result.Error, "automation service is not supported")
}

func TestDefinition_ReturnsDecodeAndRuntimeErrors(t *testing.T) {
	definition := Definition(&toolmocks.Runtime{})

	result, err := definition.Handler.Invoke(context.Background(), tools.Call{Input: `{`})
	require.NoError(t, err)
	require.NotEmpty(t, result.Error)

	expected := errors.New("service failed")
	definition = Definition(&toolmocks.Runtime{AutomationServiceErr: expected})
	result, err = definition.Handler.Invoke(context.Background(), tools.Call{Input: `{"action":"status"}`})
	require.NoError(t, err)
	require.Contains(t, result.Error, expected.Error())

	definition = Definition(nil)
	result, err = definition.Handler.Invoke(context.Background(), tools.Call{Input: `{"action":"status"}`})
	require.NoError(t, err)
	require.Contains(t, result.Error, "automation runtime is required")

	definition = Definition(&toolmocks.Runtime{
		AutomationServiceValue: &automationToolServiceStub{store: storememory.NewStore()},
		AutomationServiceOK:    true,
	})
	result, err = definition.Handler.Invoke(context.Background(), tools.Call{Input: `{"action":"missing"}`})
	require.NoError(t, err)
	require.Contains(t, result.Error, "unsupported automation action")
}

func TestDefinition_RunUsesAutomationService(t *testing.T) {
	service := &automationToolServiceStub{
		store: storememory.NewStore(),
		run: storage.AutomationRun{
			ID:     "autorun_projectaprojectapr",
			JobID:  testAutomationToolJobID,
			Status: storage.AutomationRunStatusOK,
		},
	}
	definition := Definition(&toolmocks.Runtime{
		AutomationServiceValue: service,
		AutomationServiceOK:    true,
	})

	result, err := definition.Handler.Invoke(context.Background(), tools.Call{Input: `{
		"action": "run",
		"id": "` + testAutomationToolJobID + `"
	}`})

	require.NoError(t, err)
	require.Empty(t, result.Error)
	require.Equal(t, testAutomationToolJobID, service.runID)
	var out output
	require.NoError(t, json.Unmarshal([]byte(result.Output), &out))
	require.Equal(t, "ok", out.Status)
	require.Equal(t, storage.AutomationRunStatusOK, out.Run.Status)
}

func TestDefinition_RunRequiresAutomationService(t *testing.T) {
	definition := Definition(&toolmocks.Runtime{})

	result, err := definition.Handler.Invoke(context.Background(), tools.Call{Input: `{
		"action": "run",
		"id": "` + testAutomationToolJobID + `"
	}`})

	require.NoError(t, err)
	require.Contains(t, result.Error, "automation service is not supported")
}

func TestGetService_RejectsMissingRuntimeAndPropagatesErrors(t *testing.T) {
	_, err := getService(context.Background(), nil)
	require.EqualError(t, err, "automation runtime is required")

	expected := errors.New("service failed")
	_, err = getService(context.Background(), &toolmocks.Runtime{
		AutomationServiceErr: expected,
	})
	require.ErrorIs(t, err, expected)
}

func TestDefinition_SchemaDescribesConditionalRequirements(t *testing.T) {
	definition := Definition(&toolmocks.Runtime{})
	properties := definition.InputSchema["properties"].(map[string]any)
	job := properties["job"].(map[string]any)["properties"].(map[string]any)
	schedule := job["schedule"].(map[string]any)["properties"].(map[string]any)
	payload := job["payload"].(map[string]any)["properties"].(map[string]any)
	delivery := job["delivery"].(map[string]any)["properties"].(map[string]any)

	require.Contains(t, properties["id"].(map[string]any)["description"], "Required for update, pause, resume, run, and remove")
	require.Contains(t, properties["query"].(map[string]any)["description"], "Only used when action=list")
	require.Contains(t, properties["run_query"].(map[string]any)["description"], "Only used when action=runs")
	require.Contains(t, schedule["kind"].(map[string]any)["description"], "If kind=at, at is required")
	require.Contains(t, payload["kind"].(map[string]any)["description"], "If kind=system_event, system_event is required")
	require.Contains(t, delivery["webhook_url"].(map[string]any)["description"], "Required when delivery.mode=webhook")
}

func TestInvoke_RejectsUnsupportedAction(t *testing.T) {
	_, err := invoke(context.Background(), &automationToolServiceStub{store: storememory.NewStore()}, input{Action: "missing"})
	require.EqualError(t, err, "unsupported automation action")
}

func TestInvoke_PropagatesServiceErrors(t *testing.T) {
	expected := errors.New("store failed")

	_, err := invoke(context.Background(), &automationToolServiceStub{listErr: expected}, input{Action: "status"})
	require.ErrorIs(t, err, expected)

	_, err = invoke(context.Background(), nil, input{Action: "run", ID: testAutomationToolJobID})
	require.EqualError(t, err, "automation service is required")

	service := &automationToolServiceStub{store: storememory.NewStore(), runErr: expected}
	_, err = invoke(context.Background(), service, input{Action: "run", ID: testAutomationToolJobID})
	require.ErrorIs(t, err, expected)
}

func TestCaptureContext_HandlesMissingSessionAndExistingMetadata(t *testing.T) {
	job := storage.AutomationJob{SessionTarget: "main"}
	require.Equal(t, job, captureContext(context.Background(), job))

	ctx := tools.WithSessionID(context.Background(), "ses_projectaprojectaproje")
	job = storage.AutomationJob{
		SessionTarget: "main",
		Payload: storage.AutomationPayload{
			Metadata: map[string]string{"existing": "true"},
		},
	}
	captured := captureContext(ctx, job)
	require.Equal(t, "main", captured.SessionTarget)
	require.Equal(t, "true", captured.Payload.Metadata["existing"])
	require.Equal(t, "ses_projectaprojectaproje", captured.Payload.Metadata["origin_session_id"])
}

func TestInvoke_RejectsInvalidNestedJob(t *testing.T) {
	tests := []struct {
		name  string
		input input
		err   string
	}{
		{
			name: "add missing schedule kind",
			input: input{
				Action: "add",
				Job: storage.AutomationJob{
					Enabled: true,
					Payload: storage.AutomationPayload{
						Kind:   storage.AutomationPayloadPrompt,
						Prompt: "summarize",
					},
				},
			},
			err: "automation schedule kind is required",
		},
		{
			name: "add missing one-shot time",
			input: input{
				Action: "add",
				Job: storage.AutomationJob{
					Schedule: storage.AutomationSchedule{Kind: storage.AutomationScheduleAt},
					Payload: storage.AutomationPayload{
						Kind:   storage.AutomationPayloadPrompt,
						Prompt: "summarize",
					},
				},
			},
			err: "automation one-shot schedule time is required",
		},
		{
			name: "update missing prompt",
			input: input{
				Action: "update",
				ID:     testAutomationToolJobID,
				Job: storage.AutomationJob{
					Payload: storage.AutomationPayload{Kind: storage.AutomationPayloadPrompt},
				},
			},
			err: "automation prompt payload is required",
		},
		{
			name: "update missing system event",
			input: input{
				Action: "update",
				ID:     testAutomationToolJobID,
				Job: storage.AutomationJob{
					Payload: storage.AutomationPayload{Kind: storage.AutomationPayloadSystemEvent},
				},
			},
			err: "automation system event payload is required",
		},
		{
			name: "update unsupported payload kind",
			input: input{
				Action: "update",
				ID:     testAutomationToolJobID,
				Job: storage.AutomationJob{
					Payload: storage.AutomationPayload{Kind: storage.AutomationPayloadKind("unknown")},
				},
			},
			err: "unsupported automation payload kind",
		},
		{
			name: "update missing every interval",
			input: input{
				Action: "update",
				ID:     testAutomationToolJobID,
				Job: storage.AutomationJob{
					Schedule: storage.AutomationSchedule{Kind: storage.AutomationScheduleEvery},
				},
			},
			err: "automation interval schedule must be greater than zero",
		},
		{
			name: "update missing cron expression",
			input: input{
				Action: "update",
				ID:     testAutomationToolJobID,
				Job: storage.AutomationJob{
					Schedule: storage.AutomationSchedule{Kind: storage.AutomationScheduleCron},
				},
			},
			err: "automation cron schedule expression is required",
		},
		{
			name: "add origin delivery without a gateway route",
			input: input{
				Action: "add",
				Job: storage.AutomationJob{
					Schedule: storage.AutomationSchedule{Kind: storage.AutomationScheduleEvery, Every: time.Minute},
					Payload:  storage.AutomationPayload{Kind: storage.AutomationPayloadPrompt, Prompt: "notify"},
					Delivery: storage.AutomationDelivery{Mode: storage.AutomationDeliveryOrigin},
				},
			},
			err: "automation origin delivery requires a Slack or Telegram channel and target; TUI sessions should use local delivery with an explicit session target",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := invoke(context.Background(), &automationToolServiceStub{store: storememory.NewStore()}, test.input)
			require.EqualError(t, err, test.err)
		})
	}
}

func TestPatchFromInput_CoversOptionalBranches(t *testing.T) {
	at := time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC)
	patch, err := patchFromInput(input{
		ID: testAutomationToolJobID,
		Job: storage.AutomationJob{
			Schedule: storage.AutomationSchedule{
				Kind: storage.AutomationScheduleAt,
				At:   at,
			},
			Payload: storage.AutomationPayload{
				Kind:        storage.AutomationPayloadSystemEvent,
				SystemEvent: "wake",
			},
			Delivery: storage.AutomationDelivery{
				Mode: storage.AutomationDeliveryLocal,
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, at, patch.Schedule.At)
	require.Equal(t, "wake", patch.Payload.SystemEvent)
	require.Equal(t, storage.AutomationDeliveryLocal, patch.Delivery.Mode)
}

func TestInputUnmarshal_RejectsInvalidTypedFields(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		field string
	}{
		{name: "top-level field", raw: `{"action":1}`, field: "action"},
		{name: "partial update field", raw: `{"job":{"payload":{"max_runtime":"later"}}}`, field: "max_runtime"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var req input
			err := json.Unmarshal([]byte(test.raw), &req)
			require.Error(t, err)
			require.Contains(t, err.Error(), test.field)
		})
	}
}

func TestNormalizeToolInputJSON_ValidatesScheduleAt(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{name: "malformed JSON", input: `{`, wantErr: "unexpected end of JSON input"},
		{name: "non-string timestamp", input: `{"job":{"schedule":{"at":1}}}`, wantErr: "job.schedule.at must be an RFC3339 string or null"},
		{name: "valid timestamp", input: `{"job":{"schedule":{"at":"2026-07-17T12:00:00Z"}}}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := normalizeToolInputJSON([]byte(test.input))
			if test.wantErr != "" {
				require.EqualError(t, err, test.wantErr)
				require.Nil(t, result)
				return
			}
			require.NoError(t, err)
			require.JSONEq(t, test.input, string(result))
		})
	}
}

func TestDecodeInput_DefaultsEmptyInputAndReportsInvalidJSON(t *testing.T) {
	var req input
	require.Empty(t, decodeInput(tools.Call{}, &req).Error)
	require.Equal(t, "", req.Action)

	result := decodeInput(tools.Call{Input: `{`}, &req)
	var toolErr tools.Error
	require.NoError(t, json.Unmarshal([]byte(result.Error), &toolErr))
	require.Equal(t, "invalid_input", toolErr.Code)
	require.Equal(t, "unexpected end of JSON input", toolErr.Message)
}

func TestPatchFromInputWithCurrent_RejectsInvalidPartialPayload(t *testing.T) {
	kind := storage.AutomationPayloadPrompt
	prompt := ""
	_, err := patchFromInputWithCurrent(input{
		ID: testAutomationToolJobID,
		payloadUpdate: &payloadUpdateInput{
			Kind:   &kind,
			Prompt: &prompt,
		},
	}, storage.AutomationJob{Payload: storage.AutomationPayload{
		Kind:   storage.AutomationPayloadPrompt,
		Prompt: "existing",
	}})
	require.EqualError(t, err, "automation prompt payload is required")
}

func TestLoadAutomationJobForUpdate_ReturnsNotFound(t *testing.T) {
	_, err := loadAutomationJobForUpdate(
		context.Background(),
		&automationToolServiceStub{store: storememory.NewStore()},
		input{ID: testAutomationToolJobID, payloadUpdate: &payloadUpdateInput{}},
	)
	require.EqualError(t, err, "automation job not found")
}

func TestApplyPayloadUpdate_UpdatesSystemEventAndMetadata(t *testing.T) {
	systemEvent := "wake"
	metadata := map[string]string{"source": "test"}
	payload := applyPayloadUpdate(storage.AutomationPayload{
		Kind:   storage.AutomationPayloadPrompt,
		Prompt: "summarize",
	}, payloadUpdateInput{
		SystemEvent: &systemEvent,
		Metadata:    &metadata,
	})

	require.Equal(t, storage.AutomationPayloadSystemEvent, payload.Kind)
	require.Empty(t, payload.Prompt)
	require.Equal(t, "wake", payload.SystemEvent)
	require.Equal(t, metadata, payload.Metadata)
	metadata["source"] = "changed"
	require.Equal(t, "test", payload.Metadata["source"])
}

func TestApplyDeliveryUpdate_ClearsFieldsForNewMode(t *testing.T) {
	base := storage.AutomationDelivery{
		Channel: "telegram", Target: "chat", ThreadID: "thread", WebhookURL: "https://example.com/hook",
	}
	tests := []struct {
		name string
		mode storage.AutomationDeliveryMode
		want storage.AutomationDelivery
	}{
		{
			name: "gateway",
			mode: storage.AutomationDeliveryGateway,
			want: storage.AutomationDelivery{Mode: storage.AutomationDeliveryGateway, Channel: "telegram", Target: "chat", ThreadID: "thread"},
		},
		{
			name: "webhook",
			mode: storage.AutomationDeliveryWebhook,
			want: storage.AutomationDelivery{Mode: storage.AutomationDeliveryWebhook, WebhookURL: "https://example.com/hook"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := applyDeliveryUpdate(base, deliveryUpdateInput{Mode: &test.mode})
			require.Equal(t, test.want, actual)
		})
	}
}

func TestCloneStringMap_HandlesEmptyAndPopulatedMaps(t *testing.T) {
	require.Nil(t, cloneStringMap(nil))

	source := map[string]string{"key": "value"}
	cloned := cloneStringMap(source)
	require.Equal(t, source, cloned)
	source["key"] = "changed"
	require.Equal(t, "value", cloned["key"])
}
