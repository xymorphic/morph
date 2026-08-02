package rpc

import (
	"context"
	"maps"
	"reflect"
	"time"

	"github.com/xymorphic/morph/internal/automation"
	"github.com/xymorphic/morph/internal/permissions"
	morphpb "github.com/xymorphic/morph/internal/rpc/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type AutomationService struct {
	morphpb.UnimplementedAutomationServiceServer
	service *Service
}

func NewAutomationService(service *Service) *AutomationService {
	return &AutomationService{service: service}
}

func (s *AutomationService) Status(
	ctx context.Context,
	req *morphpb.GetAutomationStatusRequest,
) (*morphpb.GetAutomationStatusResponse, error) {
	if err := s.checkRequest(req); err != nil {
		return nil, err
	}

	statusValue, err := s.service.automation.Status(ctx)
	if err != nil {
		return nil, getGRPCError(err)
	}

	return &morphpb.GetAutomationStatusResponse{
		Running:      statusValue.Running,
		StartedAt:    timeToProto(statusValue.StartedAt),
		JobCount:     int32(statusValue.JobCount),
		RunningCount: int32(statusValue.RunningCount),
		NextWakeAt:   timeToProto(statusValue.NextWakeAt),
	}, nil
}

func (s *AutomationService) ListJobs(
	ctx context.Context,
	req *morphpb.ListAutomationJobsRequest,
) (*morphpb.ListAutomationJobsResponse, error) {
	if err := s.checkRequest(req); err != nil {
		return nil, err
	}

	list, err := s.service.automation.List(ctx, automation.JobQuery{
		IDs:             append([]string(nil), req.GetIds()...),
		Enabled:         req.Enabled,
		Profile:         req.GetProfile(),
		SessionTarget:   req.GetSessionTarget(),
		Limit:           int(req.GetLimit()),
		IncludeDisabled: req.GetIncludeDisabled(),
	})
	if err != nil {
		return nil, getGRPCError(err)
	}

	items := make([]*morphpb.AutomationJob, 0, len(list.Jobs))
	for _, job := range list.Jobs {
		items = append(items, automationJobToProto(job))
	}

	return &morphpb.ListAutomationJobsResponse{Jobs: items}, nil
}

func (s *AutomationService) AddJob(
	ctx context.Context,
	req *morphpb.AddAutomationJobRequest,
) (*morphpb.AddAutomationJobResponse, error) {
	if err := s.checkRequest(req); err != nil {
		return nil, err
	}

	ctx = s.service.getPermissionContext(ctx)
	if err := s.service.checkPermission(ctx, automationPermissionOperation(
		permissions.ActionCreate,
		[]permissions.Effect{permissions.EffectExternalSystem, permissions.EffectWrite},
		"",
	)); err != nil {
		return nil, err
	}

	job, err := s.service.automation.Add(ctx, automationJobFromAddRequest(req))
	if err != nil {
		return nil, getGRPCError(err)
	}

	return &morphpb.AddAutomationJobResponse{Job: automationJobToProto(job)}, nil
}

func (s *AutomationService) UpdateJob(
	ctx context.Context,
	req *morphpb.UpdateAutomationJobRequest,
) (*morphpb.UpdateAutomationJobResponse, error) {
	if err := s.checkRequest(req); err != nil {
		return nil, err
	}

	ctx = s.service.getPermissionContext(ctx)
	if err := s.service.checkPermission(ctx, automationPermissionOperation(
		permissions.ActionUpdate,
		[]permissions.Effect{permissions.EffectExternalSystem, permissions.EffectWrite},
		req.GetId(),
	)); err != nil {
		return nil, err
	}

	patch := automation.JobPatch{
		ID:             req.GetId(),
		Name:           req.Name,
		Description:    req.Description,
		Enabled:        req.Enabled,
		Profile:        req.Profile,
		SessionTarget:  req.SessionTarget,
		DeleteAfterRun: req.DeleteAfterRun,
	}
	if req.GetSchedule() != nil {
		schedule := automationScheduleFromProto(req.GetSchedule())
		patch.Schedule = &schedule
	}
	if req.GetPayload() != nil {
		payload := automationPayloadFromProto(req.GetPayload())
		patch.Payload = &payload
	}
	if req.GetDelivery() != nil {
		delivery := automationDeliveryFromProto(req.GetDelivery())
		patch.Delivery = &delivery
	}
	if req.GetState() != nil {
		state := automationJobStateFromProto(req.GetState())
		patch.State = &state
	}

	job, err := s.service.automation.Update(ctx, patch)
	if err != nil {
		return nil, getGRPCError(err)
	}

	return &morphpb.UpdateAutomationJobResponse{Job: automationJobToProto(job)}, nil
}

func automationPermissionOperation(
	action permissions.Action,
	effects []permissions.Effect,
	target string,
) permissions.Operation {
	return permissions.Operation{
		Resource:      permissions.ResourceAutomation,
		Action:        action,
		Effects:       effects,
		Target:        target,
		OwnerRequired: true,
	}
}

func (s *AutomationService) RemoveJob(
	ctx context.Context,
	req *morphpb.RemoveAutomationJobRequest,
) (*morphpb.RemoveAutomationJobResponse, error) {
	if err := s.checkRequest(req); err != nil {
		return nil, err
	}

	ctx = s.service.getPermissionContext(ctx)
	if err := s.service.checkPermission(ctx, automationPermissionOperation(
		permissions.ActionDelete,
		[]permissions.Effect{
			permissions.EffectDestructive,
			permissions.EffectExternalSystem,
			permissions.EffectWrite,
		},
		req.GetId(),
	)); err != nil {
		return nil, err
	}

	id := req.GetId()
	if err := s.service.automation.Remove(ctx, id); err != nil {
		return nil, getGRPCError(err)
	}

	return &morphpb.RemoveAutomationJobResponse{Id: id}, nil
}

func (s *AutomationService) RunJob(
	ctx context.Context,
	req *morphpb.RunAutomationJobRequest,
) (*morphpb.RunAutomationJobResponse, error) {
	if err := s.checkRequest(req); err != nil {
		return nil, err
	}

	ctx = s.service.getPermissionContext(ctx)
	if err := s.service.checkPermission(ctx, automationPermissionOperation(
		permissions.ActionTrigger,
		[]permissions.Effect{permissions.EffectExecution, permissions.EffectExternalSystem},
		req.GetId(),
	)); err != nil {
		return nil, err
	}

	run, err := s.service.automation.Run(ctx, req.GetId())
	if err != nil {
		return nil, getGRPCError(err)
	}

	return &morphpb.RunAutomationJobResponse{Run: automationRunToProto(run)}, nil
}

func (s *AutomationService) ListRuns(
	ctx context.Context,
	req *morphpb.ListAutomationRunsRequest,
) (*morphpb.ListAutomationRunsResponse, error) {
	if err := s.checkRequest(req); err != nil {
		return nil, err
	}

	statuses := make([]automation.RunStatus, 0, len(req.GetStatus()))
	for _, statusValue := range req.GetStatus() {
		statuses = append(statuses, automation.RunStatus(statusValue))
	}
	list, err := s.service.automation.Runs(ctx, automation.RunQuery{
		JobID:  req.GetJobId(),
		IDs:    append([]string(nil), req.GetIds()...),
		Status: statuses,
		Limit:  int(req.GetLimit()),
	})
	if err != nil {
		return nil, getGRPCError(err)
	}

	items := make([]*morphpb.AutomationRun, 0, len(list.Runs))
	for _, run := range list.Runs {
		items = append(items, automationRunToProto(run))
	}

	return &morphpb.ListAutomationRunsResponse{Runs: items}, nil
}

func (s *AutomationService) checkRequest(req any) error {
	if s == nil || s.service == nil {
		return status.Error(codes.Internal, "service is required")
	}
	if s.service.automation == nil {
		return status.Error(codes.Internal, "automation service is required")
	}
	if req == nil {
		return status.Error(codes.InvalidArgument, "automation request is required")
	}

	value := reflect.ValueOf(req)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if value.IsNil() {
			return status.Error(codes.InvalidArgument, "automation request is required")
		}
	}

	return nil
}

func automationJobToProto(job automation.Job) *morphpb.AutomationJob {
	return &morphpb.AutomationJob{
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

func automationJobFromAddRequest(req *morphpb.AddAutomationJobRequest) automation.Job {
	if req == nil {
		return automation.Job{}
	}

	return automation.Job{
		ID:             req.GetId(),
		Name:           req.GetName(),
		Description:    req.GetDescription(),
		Enabled:        req.GetEnabled(),
		CreatedAt:      protoTimestampToTime(req.GetCreatedAt()),
		UpdatedAt:      protoTimestampToTime(req.GetUpdatedAt()),
		Schedule:       automationScheduleFromProto(req.GetSchedule()),
		Payload:        automationPayloadFromProto(req.GetPayload()),
		Delivery:       automationDeliveryFromProto(req.GetDelivery()),
		Profile:        req.GetProfile(),
		SessionTarget:  req.GetSessionTarget(),
		DeleteAfterRun: req.GetDeleteAfterRun(),
		State:          automationJobStateFromProto(req.GetState()),
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

func automationRunToProto(run automation.Run) *morphpb.AutomationRun {
	return &morphpb.AutomationRun{
		Id:             run.ID,
		JobId:          run.JobID,
		Status:         string(run.Status),
		StartedAt:      timeToProto(run.StartedAt),
		EndedAt:        timeToProto(run.EndedAt),
		DurationNanos:  int64(run.Duration),
		Output:         run.Output,
		Error:          run.Error,
		SessionId:      run.SessionID,
		DeliveryStatus: string(run.DeliveryStatus),
		DeliveryError:  run.DeliveryError,
		Model:          run.Model,
		Provider:       run.Provider,
		Usage:          automationUsageToProto(run.Usage),
	}
}

func automationUsageToProto(usage automation.Usage) *morphpb.AutomationUsage {
	return &morphpb.AutomationUsage{
		InputTokens:      int32(usage.InputTokens),
		OutputTokens:     int32(usage.OutputTokens),
		TotalTokens:      int32(usage.TotalTokens),
		CacheReadTokens:  int32(usage.CacheReadTokens),
		CacheWriteTokens: int32(usage.CacheWriteTokens),
	}
}

func timeToProto(value time.Time) *timestamppb.Timestamp {
	if value.IsZero() {
		return nil
	}

	return timestamppb.New(value.UTC())
}

func protoTimestampToTime(value interface{ AsTime() time.Time }) time.Time {
	if timestamp, ok := any(value).(*timestamppb.Timestamp); ok && timestamp == nil {
		return time.Time{}
	}
	if value == nil {
		return time.Time{}
	}

	return value.AsTime().UTC()
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}

	cloned := make(map[string]string, len(values))
	maps.Copy(cloned, values)

	return cloned
}
