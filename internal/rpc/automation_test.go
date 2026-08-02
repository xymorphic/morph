package rpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/xymorphic/morph/internal/automation"
	morphpb "github.com/xymorphic/morph/internal/rpc/proto"
)

var (
	testRPCAutomationJobID = "auto_projectaprojectaproje"
	testRPCAutomationRunID = "autorun_projectaprojectaproj"
)

func TestAutomationService_StatusUsesAutomationService(t *testing.T) {
	api := &automationAPIStub{status: automation.Status{
		Running:    true,
		JobCount:   3,
		StartedAt:  time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC),
		NextWakeAt: time.Date(2026, 7, 5, 9, 0, 0, 0, time.UTC),
	}}
	svc := newAllowedServiceWithOptions(nil, ServiceOptions{Automation: api})
	automationService := NewAutomationService(svc)

	resp, err := automationService.Status(context.Background(), &morphpb.GetAutomationStatusRequest{})

	require.NoError(t, err)
	require.True(t, resp.GetRunning())
	require.Equal(t, int32(3), resp.GetJobCount())
	require.Equal(t, api.status.StartedAt, resp.GetStartedAt().AsTime())
}

func TestAutomationService_JobAndRunMethodsTranslateRequests(t *testing.T) {
	api := &automationAPIStub{}
	svc := newAllowedServiceWithOptions(nil, ServiceOptions{Automation: api})
	automationService := NewAutomationService(svc)
	enabled := true
	at := time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC)

	_, err := automationService.ListJobs(context.Background(), &morphpb.ListAutomationJobsRequest{
		Ids:             []string{testRPCAutomationJobID},
		Enabled:         &enabled,
		Profile:         "work",
		SessionTarget:   "main",
		Limit:           5,
		IncludeDisabled: true,
	})
	require.NoError(t, err)
	require.Equal(t, []string{testRPCAutomationJobID}, api.listQuery.IDs)
	require.NotNil(t, api.listQuery.Enabled)
	require.True(t, *api.listQuery.Enabled)
	require.Equal(t, "work", api.listQuery.Profile)
	require.Equal(t, "main", api.listQuery.SessionTarget)

	addResp, err := automationService.AddJob(context.Background(), &morphpb.AddAutomationJobRequest{
		Name:    "Daily",
		Enabled: true,
		Schedule: &morphpb.AutomationSchedule{
			Kind:       string(automation.ScheduleEvery),
			EveryNanos: int64(time.Hour),
		},
		Payload: &morphpb.AutomationPayload{Prompt: "summarize"},
	})
	require.NoError(t, err)
	require.Equal(t, testRPCAutomationJobID, addResp.GetJob().GetId())
	require.Equal(t, time.Hour, api.added.Schedule.Every)

	name := "Renamed"
	_, err = automationService.UpdateJob(context.Background(), &morphpb.UpdateAutomationJobRequest{
		Id:          testRPCAutomationJobID,
		Name:        &name,
		Description: &name,
		Schedule: &morphpb.AutomationSchedule{
			Kind:       string(automation.ScheduleAt),
			At:         timeToProto(at),
			EveryNanos: int64(time.Hour),
			Cron:       "* * * * *",
			Timezone:   "UTC",
		},
		Payload: &morphpb.AutomationPayload{
			Kind:               string(automation.PayloadPrompt),
			Prompt:             "summarize",
			SystemEvent:        "wake",
			Model:              "gpt-test",
			Provider:           "openai",
			BaseUrl:            "https://api.example.test/v1",
			NoTimeout:          true,
			MaxRuntimeNanos:    int64(time.Minute),
			MaxIterations:      2,
			RetryAttempts:      3,
			RetryBackoffNanos:  int64(time.Second),
			RetryMaxDelayNanos: int64(time.Minute),
			ToolGroups:         []string{"shell"},
			Metadata:           map[string]string{"origin": "telegram"},
		},
		Delivery: &morphpb.AutomationDelivery{
			Mode:                 string(automation.DeliveryWebhook),
			Channel:              "telegram",
			Target:               "123",
			ThreadId:             "thread-1",
			WebhookUrl:           "https://hook.example.test",
			BestEffort:           true,
			FailureTarget:        "ops",
			FailureAfter:         2,
			FailureCooldownNanos: int64(time.Hour),
		},
		State: &morphpb.AutomationJobState{
			NextRunAt:           timeToProto(at),
			RunningAt:           timeToProto(at),
			LastRunAt:           timeToProto(at),
			LastStatus:          string(automation.RunStatusOK),
			LastError:           "none",
			LastDurationNanos:   int64(time.Second),
			ConsecutiveErrors:   2,
			LastFailureNoticeAt: timeToProto(at),
		},
	})
	require.NoError(t, err)
	require.Equal(t, &name, api.patch.Name)
	require.Equal(t, automation.ScheduleAt, api.patch.Schedule.Kind)
	require.Equal(t, map[string]string{"origin": "telegram"}, api.patch.Payload.Metadata)
	require.Equal(t, automation.DeliveryWebhook, api.patch.Delivery.Mode)
	require.Equal(t, automation.RunStatusOK, api.patch.State.LastStatus)

	_, err = automationService.RunJob(context.Background(), &morphpb.RunAutomationJobRequest{Id: testRPCAutomationJobID})
	require.NoError(t, err)
	require.Equal(t, testRPCAutomationJobID, api.runID)

	_, err = automationService.ListRuns(context.Background(), &morphpb.ListAutomationRunsRequest{
		JobId:  testRPCAutomationJobID,
		Status: []string{string(automation.RunStatusOK)},
	})
	require.NoError(t, err)
	require.Equal(t, []automation.RunStatus{automation.RunStatusOK}, api.runQuery.Status)

	_, err = automationService.RemoveJob(context.Background(), &morphpb.RemoveAutomationJobRequest{Id: testRPCAutomationJobID})
	require.NoError(t, err)
	require.Equal(t, testRPCAutomationJobID, api.removedID)
}

func TestAutomationService_RejectsMissingServiceAndNilRequest(t *testing.T) {
	svc := newAllowedServiceWithOptions(nil, ServiceOptions{})
	automationService := NewAutomationService(svc)

	_, err := (*AutomationService)(nil).Status(context.Background(), &morphpb.GetAutomationStatusRequest{})
	require.Equal(t, codes.Internal, status.Code(err))

	_, err = (&AutomationService{}).Status(context.Background(), &morphpb.GetAutomationStatusRequest{})
	require.Equal(t, codes.Internal, status.Code(err))

	_, err = automationService.Status(context.Background(), &morphpb.GetAutomationStatusRequest{})
	require.Equal(t, codes.Internal, status.Code(err))

	svc = newAllowedServiceWithOptions(nil, ServiceOptions{Automation: &automationAPIStub{}})
	automationService = NewAutomationService(svc)
	require.Equal(t, codes.InvalidArgument, status.Code(automationService.checkRequest(nil)))

	_, err = automationService.Status(context.Background(), nil)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestAutomationService_MethodsPropagateAutomationErrors(t *testing.T) {
	expected := errors.New("automation failed")
	automationService := NewAutomationService(newAllowedServiceWithOptions(nil, ServiceOptions{
		Automation: &automationAPIStub{err: expected},
	}))
	ctx := context.Background()

	_, err := automationService.Status(ctx, &morphpb.GetAutomationStatusRequest{})
	require.Equal(t, codes.Internal, status.Code(err))

	_, err = automationService.ListJobs(ctx, &morphpb.ListAutomationJobsRequest{})
	require.Equal(t, codes.Internal, status.Code(err))

	_, err = automationService.AddJob(ctx, &morphpb.AddAutomationJobRequest{})
	require.Equal(t, codes.Internal, status.Code(err))

	_, err = automationService.UpdateJob(ctx, &morphpb.UpdateAutomationJobRequest{})
	require.Equal(t, codes.Internal, status.Code(err))

	_, err = automationService.RemoveJob(ctx, &morphpb.RemoveAutomationJobRequest{})
	require.Equal(t, codes.Internal, status.Code(err))

	_, err = automationService.RunJob(ctx, &morphpb.RunAutomationJobRequest{})
	require.Equal(t, codes.Internal, status.Code(err))

	_, err = automationService.ListRuns(ctx, &morphpb.ListAutomationRunsRequest{})
	require.Equal(t, codes.Internal, status.Code(err))
}

func TestAutomationService_MethodsRejectNilRequests(t *testing.T) {
	automationService := NewAutomationService(newAllowedServiceWithOptions(nil, ServiceOptions{
		Automation: &automationAPIStub{},
	}))
	ctx := context.Background()

	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "list jobs",
			run: func() error {
				_, err := automationService.ListJobs(ctx, nil)
				return err
			},
		},
		{
			name: "add job",
			run: func() error {
				_, err := automationService.AddJob(ctx, nil)
				return err
			},
		},
		{
			name: "update job",
			run: func() error {
				_, err := automationService.UpdateJob(ctx, nil)
				return err
			},
		},
		{
			name: "remove job",
			run: func() error {
				_, err := automationService.RemoveJob(ctx, nil)
				return err
			},
		},
		{
			name: "run job",
			run: func() error {
				_, err := automationService.RunJob(ctx, nil)
				return err
			},
		},
		{
			name: "list runs",
			run: func() error {
				_, err := automationService.ListRuns(ctx, nil)
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, codes.InvalidArgument, status.Code(test.run()))
		})
	}
}

func TestAutomationProtoConverters_HandleNilValues(t *testing.T) {
	require.Equal(t, automation.Job{}, automationJobFromProto(nil))
	require.Equal(t, automation.Schedule{}, automationScheduleFromProto(nil))
	require.Equal(t, automation.Payload{}, automationPayloadFromProto(nil))
	require.Equal(t, automation.Delivery{}, automationDeliveryFromProto(nil))
	require.Equal(t, automation.JobState{}, automationJobStateFromProto(nil))
	require.Equal(t, automation.Job{}, automationJobFromAddRequest(nil))
	require.Zero(t, protoTimestampToTime(nil))
	var timestamp *timestamppb.Timestamp
	require.Zero(t, protoTimestampToTime(timestamp))
	require.Nil(t, cloneStringMap(nil))
	require.Equal(t, map[string]string{"a": "b"}, cloneStringMap(map[string]string{"a": "b"}))
}

func TestAutomationProtoConverters_ConvertJobFromProto(t *testing.T) {
	at := time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC)
	job := automationJobFromProto(&morphpb.AutomationJob{
		Id:             testRPCAutomationJobID,
		Name:           "Daily",
		Description:    "Summaries",
		Enabled:        true,
		CreatedAt:      timeToProto(at),
		UpdatedAt:      timeToProto(at),
		Schedule:       &morphpb.AutomationSchedule{Kind: string(automation.ScheduleEvery), EveryNanos: int64(time.Hour)},
		Payload:        &morphpb.AutomationPayload{Prompt: "summarize"},
		Delivery:       &morphpb.AutomationDelivery{Mode: string(automation.DeliveryLocal)},
		Profile:        "work",
		SessionTarget:  "main",
		DeleteAfterRun: true,
		State:          &morphpb.AutomationJobState{LastStatus: string(automation.RunStatusOK)},
	})

	require.Equal(t, testRPCAutomationJobID, job.ID)
	require.Equal(t, "Daily", job.Name)
	require.Equal(t, time.Hour, job.Schedule.Every)
	require.Equal(t, "summarize", job.Payload.Prompt)
	require.Equal(t, automation.DeliveryLocal, job.Delivery.Mode)
	require.True(t, job.DeleteAfterRun)
	require.Equal(t, automation.RunStatusOK, job.State.LastStatus)
}

func valueOrZero(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}
