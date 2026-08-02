package client

import (
	"context"
	"errors"
	"time"

	"github.com/xymorphic/morph/internal/automation"
	morphpb "github.com/xymorphic/morph/internal/rpc/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *AutomationService) Status(ctx context.Context) (automation.Status, error) {
	client, err := s.getClient()
	if err != nil {
		return automation.Status{}, err
	}

	prepareRPCConnection(s.reconnector)
	resp, err := client.Status(ctx, &morphpb.GetAutomationStatusRequest{})
	if err != nil {
		return automation.Status{}, err
	}

	return automation.Status{
		Running:      resp.GetRunning(),
		StartedAt:    protoTimestampToTime(resp.GetStartedAt()),
		JobCount:     int(resp.GetJobCount()),
		RunningCount: int(resp.GetRunningCount()),
		NextWakeAt:   protoTimestampToTime(resp.GetNextWakeAt()),
	}, nil
}

func (s *AutomationService) List(ctx context.Context, query automation.JobQuery) (automation.JobList, error) {
	client, err := s.getClient()
	if err != nil {
		return automation.JobList{}, err
	}

	prepareRPCConnection(s.reconnector)
	resp, err := client.ListJobs(ctx, &morphpb.ListAutomationJobsRequest{
		Ids:             append([]string(nil), query.IDs...),
		Enabled:         query.Enabled,
		Profile:         query.Profile,
		SessionTarget:   query.SessionTarget,
		Limit:           int32(query.Limit),
		IncludeDisabled: query.IncludeDisabled,
	})
	if err != nil {
		return automation.JobList{}, err
	}

	jobs := make([]automation.Job, 0, len(resp.GetJobs()))
	for _, job := range resp.GetJobs() {
		jobs = append(jobs, automationJobFromProto(job))
	}

	return automation.JobList{Jobs: jobs}, nil
}

func (s *AutomationService) Add(ctx context.Context, job automation.Job) (automation.Job, error) {
	client, err := s.getClient()
	if err != nil {
		return automation.Job{}, err
	}

	prepareRPCConnection(s.reconnector)
	resp, err := client.AddJob(ctx, automationAddJobRequest(job))
	if err != nil {
		return automation.Job{}, err
	}

	return automationJobFromProto(resp.GetJob()), nil
}

func automationAddJobRequest(job automation.Job) *morphpb.AddAutomationJobRequest {
	return &morphpb.AddAutomationJobRequest{
		Id:             job.ID,
		Name:           job.Name,
		Description:    job.Description,
		Enabled:        job.Enabled,
		CreatedAt:      timeToProto(job.CreatedAt),
		UpdatedAt:      timeToProto(job.UpdatedAt),
		Schedule:       automationScheduleToProto(job.Schedule),
		Payload:        automationPayloadToProto(job.Payload),
		Delivery:       automationDeliveryToProto(job.Delivery),
		Profile:        job.Profile,
		SessionTarget:  job.SessionTarget,
		DeleteAfterRun: job.DeleteAfterRun,
		State:          automationJobStateToProto(job.State),
	}
}

func (s *AutomationService) Update(ctx context.Context, patch automation.JobPatch) (automation.Job, error) {
	client, err := s.getClient()
	if err != nil {
		return automation.Job{}, err
	}

	req := &morphpb.UpdateAutomationJobRequest{
		Id:             patch.ID,
		Name:           patch.Name,
		Description:    patch.Description,
		Enabled:        patch.Enabled,
		Profile:        patch.Profile,
		SessionTarget:  patch.SessionTarget,
		DeleteAfterRun: patch.DeleteAfterRun,
	}
	if patch.Schedule != nil {
		req.Schedule = automationScheduleToProto(*patch.Schedule)
	}
	if patch.Payload != nil {
		req.Payload = automationPayloadToProto(*patch.Payload)
	}
	if patch.Delivery != nil {
		req.Delivery = automationDeliveryToProto(*patch.Delivery)
	}
	if patch.State != nil {
		req.State = automationJobStateToProto(*patch.State)
	}

	prepareRPCConnection(s.reconnector)
	resp, err := client.UpdateJob(ctx, req)
	if err != nil {
		return automation.Job{}, err
	}

	return automationJobFromProto(resp.GetJob()), nil
}

func (s *AutomationService) Remove(ctx context.Context, id string) error {
	client, err := s.getClient()
	if err != nil {
		return err
	}

	prepareRPCConnection(s.reconnector)
	_, err = client.RemoveJob(ctx, &morphpb.RemoveAutomationJobRequest{Id: id})
	return err
}

func (s *AutomationService) Run(ctx context.Context, id string) (automation.Run, error) {
	client, err := s.getClient()
	if err != nil {
		return automation.Run{}, err
	}

	prepareRPCConnection(s.reconnector)
	resp, err := client.RunJob(ctx, &morphpb.RunAutomationJobRequest{Id: id})
	if err != nil {
		return automation.Run{}, err
	}

	return automationRunFromProto(resp.GetRun()), nil
}

func (s *AutomationService) Runs(ctx context.Context, query automation.RunQuery) (automation.RunList, error) {
	client, err := s.getClient()
	if err != nil {
		return automation.RunList{}, err
	}

	statuses := make([]string, 0, len(query.Status))
	for _, status := range query.Status {
		statuses = append(statuses, string(status))
	}
	prepareRPCConnection(s.reconnector)
	resp, err := client.ListRuns(ctx, &morphpb.ListAutomationRunsRequest{
		JobId:  query.JobID,
		Ids:    append([]string(nil), query.IDs...),
		Status: statuses,
		Limit:  int32(query.Limit),
	})
	if err != nil {
		return automation.RunList{}, err
	}

	runs := make([]automation.Run, 0, len(resp.GetRuns()))
	for _, run := range resp.GetRuns() {
		runs = append(runs, automationRunFromProto(run))
	}

	return automation.RunList{Runs: runs}, nil
}

func (s *AutomationService) getClient() (morphpb.AutomationServiceClient, error) {
	if s == nil || s.client == nil {
		return nil, errors.New("automation service client is required")
	}

	return s.client, nil
}

func automationJobFromProto(job *morphpb.AutomationJob) automation.Job {
	if job == nil {
		return automation.Job{}
	}

	return automation.Job{
		ID:             job.GetId(),
		Name:           job.GetName(),
		Description:    job.GetDescription(),
		Enabled:        job.GetEnabled(),
		CreatedAt:      protoTimestampToTime(job.GetCreatedAt()),
		UpdatedAt:      protoTimestampToTime(job.GetUpdatedAt()),
		Schedule:       automationScheduleFromProto(job.GetSchedule()),
		Payload:        automationPayloadFromProto(job.GetPayload()),
		Delivery:       automationDeliveryFromProto(job.GetDelivery()),
		Profile:        job.GetProfile(),
		SessionTarget:  job.GetSessionTarget(),
		DeleteAfterRun: job.GetDeleteAfterRun(),
		State:          automationJobStateFromProto(job.GetState()),
	}
}

func automationScheduleToProto(schedule automation.Schedule) *morphpb.AutomationSchedule {
	return &morphpb.AutomationSchedule{
		Kind:       string(schedule.Kind),
		At:         timeToProto(schedule.At),
		EveryNanos: int64(schedule.Every),
		Cron:       schedule.Cron,
		Timezone:   schedule.Timezone,
	}
}

func automationScheduleFromProto(schedule *morphpb.AutomationSchedule) automation.Schedule {
	if schedule == nil {
		return automation.Schedule{}
	}

	return automation.Schedule{
		Kind:     automation.ScheduleKind(schedule.GetKind()),
		At:       protoTimestampToTime(schedule.GetAt()),
		Every:    time.Duration(schedule.GetEveryNanos()),
		Cron:     schedule.GetCron(),
		Timezone: schedule.GetTimezone(),
	}
}

func automationPayloadToProto(payload automation.Payload) *morphpb.AutomationPayload {
	return &morphpb.AutomationPayload{
		Kind:               string(payload.Kind),
		Prompt:             payload.Prompt,
		SystemEvent:        payload.SystemEvent,
		Model:              payload.Model,
		Provider:           payload.Provider,
		BaseUrl:            payload.BaseURL,
		NoTimeout:          payload.NoTimeout,
		MaxRuntimeNanos:    int64(payload.MaxRuntime),
		MaxIterations:      int32(payload.MaxIterations),
		RetryAttempts:      int32(payload.RetryAttempts),
		RetryBackoffNanos:  int64(payload.RetryBackoff),
		RetryMaxDelayNanos: int64(payload.RetryMaxDelay),
		ToolGroups:         append([]string(nil), payload.ToolGroups...),
		Metadata:           cloneStringMap(payload.Metadata),
	}
}

func automationPayloadFromProto(payload *morphpb.AutomationPayload) automation.Payload {
	if payload == nil {
		return automation.Payload{}
	}

	return automation.Payload{
		Kind:          automation.PayloadKind(payload.GetKind()),
		Prompt:        payload.GetPrompt(),
		SystemEvent:   payload.GetSystemEvent(),
		Model:         payload.GetModel(),
		Provider:      payload.GetProvider(),
		BaseURL:       payload.GetBaseUrl(),
		NoTimeout:     payload.GetNoTimeout(),
		MaxRuntime:    time.Duration(payload.GetMaxRuntimeNanos()),
		MaxIterations: int(payload.GetMaxIterations()),
		RetryAttempts: int(payload.GetRetryAttempts()),
		RetryBackoff:  time.Duration(payload.GetRetryBackoffNanos()),
		RetryMaxDelay: time.Duration(payload.GetRetryMaxDelayNanos()),
		ToolGroups:    append([]string(nil), payload.GetToolGroups()...),
		Metadata:      cloneStringMap(payload.GetMetadata()),
	}
}

func automationDeliveryToProto(delivery automation.Delivery) *morphpb.AutomationDelivery {
	return &morphpb.AutomationDelivery{
		Mode:                 string(delivery.Mode),
		Channel:              delivery.Channel,
		Target:               delivery.Target,
		ThreadId:             delivery.ThreadID,
		WebhookUrl:           delivery.WebhookURL,
		BestEffort:           delivery.BestEffort,
		FailureTarget:        delivery.FailureTarget,
		FailureAfter:         int32(delivery.FailureAfter),
		FailureCooldownNanos: int64(delivery.FailureCooldown),
	}
}

func automationDeliveryFromProto(delivery *morphpb.AutomationDelivery) automation.Delivery {
	if delivery == nil {
		return automation.Delivery{}
	}

	return automation.Delivery{
		Mode:            automation.DeliveryMode(delivery.GetMode()),
		Channel:         delivery.GetChannel(),
		Target:          delivery.GetTarget(),
		ThreadID:        delivery.GetThreadId(),
		WebhookURL:      delivery.GetWebhookUrl(),
		BestEffort:      delivery.GetBestEffort(),
		FailureTarget:   delivery.GetFailureTarget(),
		FailureAfter:    int(delivery.GetFailureAfter()),
		FailureCooldown: time.Duration(delivery.GetFailureCooldownNanos()),
	}
}

func automationJobStateToProto(state automation.JobState) *morphpb.AutomationJobState {
	return &morphpb.AutomationJobState{
		NextRunAt:           timeToProto(state.NextRunAt),
		RunningAt:           timeToProto(state.RunningAt),
		LastRunAt:           timeToProto(state.LastRunAt),
		LastStatus:          string(state.LastStatus),
		LastError:           state.LastError,
		LastDurationNanos:   int64(state.LastDuration),
		ConsecutiveErrors:   int32(state.ConsecutiveErrors),
		LastFailureNoticeAt: timeToProto(state.LastFailureNoticeAt),
	}
}

func automationJobStateFromProto(state *morphpb.AutomationJobState) automation.JobState {
	if state == nil {
		return automation.JobState{}
	}

	return automation.JobState{
		NextRunAt:           protoTimestampToTime(state.GetNextRunAt()),
		RunningAt:           protoTimestampToTime(state.GetRunningAt()),
		LastRunAt:           protoTimestampToTime(state.GetLastRunAt()),
		LastStatus:          automation.RunStatus(state.GetLastStatus()),
		LastError:           state.GetLastError(),
		LastDuration:        time.Duration(state.GetLastDurationNanos()),
		ConsecutiveErrors:   int(state.GetConsecutiveErrors()),
		LastFailureNoticeAt: protoTimestampToTime(state.GetLastFailureNoticeAt()),
	}
}

func automationRunFromProto(run *morphpb.AutomationRun) automation.Run {
	if run == nil {
		return automation.Run{}
	}

	return automation.Run{
		ID:             run.GetId(),
		JobID:          run.GetJobId(),
		Status:         automation.RunStatus(run.GetStatus()),
		StartedAt:      protoTimestampToTime(run.GetStartedAt()),
		EndedAt:        protoTimestampToTime(run.GetEndedAt()),
		Duration:       time.Duration(run.GetDurationNanos()),
		Output:         run.GetOutput(),
		Error:          run.GetError(),
		SessionID:      run.GetSessionId(),
		DeliveryStatus: automation.DeliveryStatus(run.GetDeliveryStatus()),
		DeliveryError:  run.GetDeliveryError(),
		Model:          run.GetModel(),
		Provider:       run.GetProvider(),
		Usage:          automationUsageFromProto(run.GetUsage()),
	}
}

func automationUsageFromProto(usage *morphpb.AutomationUsage) automation.Usage {
	if usage == nil {
		return automation.Usage{}
	}

	return automation.Usage{
		InputTokens:      int(usage.GetInputTokens()),
		OutputTokens:     int(usage.GetOutputTokens()),
		TotalTokens:      int(usage.GetTotalTokens()),
		CacheReadTokens:  int(usage.GetCacheReadTokens()),
		CacheWriteTokens: int(usage.GetCacheWriteTokens()),
	}
}

func timeToProto(value time.Time) *timestamppb.Timestamp {
	if value.IsZero() {
		return nil
	}

	return timestamppb.New(value.UTC())
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
