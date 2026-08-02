package automation

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/pkg/str"
)

const (
	defaultMaxTimerSleep             = time.Minute
	defaultStaleRunningAfter         = 10 * time.Minute
	defaultAutomationRunTimeout      = 30 * time.Minute
	defaultAutomationDeliveryTimeout = 30 * time.Second
	defaultAutomationRetryAttempts   = 1
	defaultAutomationRetryBackoff    = 30 * time.Second
	defaultAutomationRetryMaxDelay   = 5 * time.Minute
	defaultAutomationOneShotGrace    = 5 * time.Minute
	defaultAutomationCatchUpStagger  = 5 * time.Second
	defaultRunHistoryRetention       = 30 * 24 * time.Hour
	defaultRunHistoryCleanupLimit    = 500
)

var (
	errAutomationJobAlreadyRunning = errors.New("automation job is already running")
	errAutomationCapacityReached   = errors.New("automation max concurrent runs reached")
)

type Runner interface {
	RunAutomation(context.Context, Job) (RunResult, error)
}

type RunnerFunc func(context.Context, Job) (RunResult, error)

func (fn RunnerFunc) RunAutomation(ctx context.Context, job Job) (RunResult, error) {
	if fn == nil {
		return RunResult{}, errors.New("automation runner is required")
	}

	return fn(ctx, job)
}

type RunResult struct {
	Status    RunStatus
	Output    string
	SessionID string
	Model     string
	Provider  string
	Usage     Usage
}

type ServiceOptions struct {
	Store                      Store
	Runner                     Runner
	PermissionChecker          permissions.Checker
	PermissionApprover         permissions.Approver
	DeliverySink               DeliverySink
	Logger                     Logger
	Tracer                     Tracer
	HTTPClient                 HTTPClient
	Now                        func() time.Time
	Location                   *time.Location
	DefaultTimezone            string
	MaxTimerSleep              time.Duration
	StaleRunningAfter          time.Duration
	DisableAfterScheduleErrors int
	DefaultRunTimeout          time.Duration
	DefaultDeliveryTimeout     time.Duration
	DefaultRetryAttempts       int
	DefaultRetryBackoff        time.Duration
	DefaultRetryMaxDelay       time.Duration
	OneShotGrace               time.Duration
	CatchUpStagger             time.Duration
	MaxConcurrentRuns          int
	DisableRunHistoryCleanup   bool
	RunHistoryRetention        time.Duration
	RunHistoryCleanupLimit     int
}

type Status struct {
	Running      bool
	StartedAt    time.Time
	JobCount     int
	RunningCount int
	NextWakeAt   time.Time
}

type MaintenanceResult struct {
	OldRunsDeleted         int
	RunningMarkersRepaired int
	SchedulesRepaired      int
	InvalidJobsUpdated     int
}

type Service struct {
	store                      Store
	runner                     Runner
	permissionChecker          permissions.Checker
	permissionApprover         permissions.Approver
	deliverySink               DeliverySink
	logger                     Logger
	tracer                     Tracer
	httpClient                 HTTPClient
	now                        func() time.Time
	location                   *time.Location
	defaultTimezone            string
	maxTimerSleep              time.Duration
	staleRunningAfter          time.Duration
	disableAfterScheduleErrors int
	defaultRunTimeout          time.Duration
	defaultDeliveryTimeout     time.Duration
	defaultRetryAttempts       int
	defaultRetryBackoff        time.Duration
	defaultRetryMaxDelay       time.Duration
	oneShotGrace               time.Duration
	catchUpStagger             time.Duration
	maxConcurrentRuns          int
	runHistoryRetention        time.Duration
	runHistoryCleanupLimit     int

	mu        sync.Mutex
	started   bool
	startedAt time.Time
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	wake      chan struct{}
	running   map[string]struct{}
	nextWake  time.Time
}

func NewService(opts ServiceOptions) (*Service, error) {
	if opts.Store == nil {
		return nil, errors.New("automation store is required")
	}
	if opts.Runner == nil {
		return nil, errors.New("automation runner is required")
	}

	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	maxTimerSleep := opts.MaxTimerSleep
	if maxTimerSleep <= 0 {
		maxTimerSleep = defaultMaxTimerSleep
	}
	staleRunningAfter := opts.StaleRunningAfter
	if staleRunningAfter <= 0 {
		staleRunningAfter = defaultStaleRunningAfter
	}
	defaultRunTimeout := opts.DefaultRunTimeout
	if defaultRunTimeout <= 0 {
		defaultRunTimeout = defaultAutomationRunTimeout
	}
	defaultDeliveryTimeout := opts.DefaultDeliveryTimeout
	if defaultDeliveryTimeout <= 0 {
		defaultDeliveryTimeout = defaultAutomationDeliveryTimeout
	}
	defaultRetryAttempts := opts.DefaultRetryAttempts
	if defaultRetryAttempts <= 0 {
		defaultRetryAttempts = defaultAutomationRetryAttempts
	}
	defaultRetryBackoff := opts.DefaultRetryBackoff
	if defaultRetryBackoff <= 0 {
		defaultRetryBackoff = defaultAutomationRetryBackoff
	}
	defaultRetryMaxDelay := opts.DefaultRetryMaxDelay
	if defaultRetryMaxDelay <= 0 {
		defaultRetryMaxDelay = defaultAutomationRetryMaxDelay
	}
	oneShotGrace := opts.OneShotGrace
	if oneShotGrace <= 0 {
		oneShotGrace = defaultAutomationOneShotGrace
	}
	catchUpStagger := opts.CatchUpStagger
	if catchUpStagger <= 0 {
		catchUpStagger = defaultAutomationCatchUpStagger
	}
	runHistoryRetention := opts.RunHistoryRetention
	if opts.DisableRunHistoryCleanup {
		runHistoryRetention = 0
	} else if runHistoryRetention <= 0 {
		runHistoryRetention = defaultRunHistoryRetention
	}
	runHistoryCleanupLimit := opts.RunHistoryCleanupLimit
	if !opts.DisableRunHistoryCleanup && runHistoryCleanupLimit <= 0 {
		runHistoryCleanupLimit = defaultRunHistoryCleanupLimit
	}
	defaultTz := str.String(opts.DefaultTimezone)
	return &Service{
		store:                      opts.Store,
		runner:                     opts.Runner,
		permissionChecker:          opts.PermissionChecker,
		permissionApprover:         opts.PermissionApprover,
		deliverySink:               opts.DeliverySink,
		logger:                     opts.Logger,
		tracer:                     opts.Tracer,
		httpClient:                 opts.HTTPClient,
		now:                        now,
		location:                   opts.Location,
		defaultTimezone:            defaultTz.Trim(),
		maxTimerSleep:              maxTimerSleep,
		staleRunningAfter:          staleRunningAfter,
		disableAfterScheduleErrors: opts.DisableAfterScheduleErrors,
		defaultRunTimeout:          defaultRunTimeout,
		defaultDeliveryTimeout:     defaultDeliveryTimeout,
		defaultRetryAttempts:       defaultRetryAttempts,
		defaultRetryBackoff:        defaultRetryBackoff,
		defaultRetryMaxDelay:       defaultRetryMaxDelay,
		oneShotGrace:               oneShotGrace,
		catchUpStagger:             catchUpStagger,
		maxConcurrentRuns:          opts.MaxConcurrentRuns,
		runHistoryRetention:        runHistoryRetention,
		runHistoryCleanupLimit:     runHistoryCleanupLimit,
		wake:                       make(chan struct{}, 1),
		running:                    make(map[string]struct{}),
	}, nil
}

func (s *Service) Start(ctx context.Context) error {
	if s == nil {
		return errors.New("automation service is required")
	}

	if ctx == nil {
		ctx = context.Background()
	}

	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}
	serviceCtx, cancel := context.WithCancel(ctx)
	s.ctx = serviceCtx
	s.cancel = cancel
	s.done = make(chan struct{})
	s.started = true
	s.startedAt = s.getNow()
	s.mu.Unlock()

	if err := s.recoverStartup(serviceCtx); err != nil {
		cancel()
		s.mu.Lock()
		s.started = false
		s.cancel = nil
		s.ctx = nil
		close(s.done)
		s.done = nil
		s.mu.Unlock()
		return err
	}

	s.record(serviceCtx, "info", "automation scheduler started", automationEventSvcStarted, nil)
	go s.runLoop(serviceCtx, s.done)
	s.notifyWake()

	return nil
}

func (s *Service) Stop() error {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	cancel := s.cancel
	done := s.done
	s.started = false
	s.cancel = nil
	s.ctx = nil
	s.done = nil
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	s.record(context.Background(), "info", "automation scheduler stopped", automationEventSvcStopped, nil)

	return nil
}

func (s *Service) Status(ctx context.Context) (Status, error) {
	if s == nil {
		return Status{}, errors.New("automation service is required")
	}

	list, err := s.store.ListJobs(ctx, JobQuery{IncludeDisabled: true})
	if err != nil {
		return Status{}, err
	}

	s.mu.Lock()
	status := Status{
		Running:    s.started,
		StartedAt:  s.startedAt,
		JobCount:   len(list.Jobs),
		NextWakeAt: s.nextWake,
	}
	for _, job := range list.Jobs {
		if !job.State.RunningAt.IsZero() {
			status.RunningCount++
		}
	}
	s.mu.Unlock()

	return status, nil
}

func (s *Service) List(ctx context.Context, query JobQuery) (JobList, error) {
	if s == nil {
		return JobList{}, errors.New("automation service is required")
	}

	return s.store.ListJobs(ctx, query)
}

func (s *Service) Runs(ctx context.Context, query RunQuery) (RunList, error) {
	if s == nil {
		return RunList{}, errors.New("automation service is required")
	}

	return s.store.ListRuns(ctx, query)
}

func (s *Service) RunMaintenance(ctx context.Context) (MaintenanceResult, error) {
	if s == nil {
		return MaintenanceResult{}, errors.New("automation service is required")
	}

	now := s.getNow()
	result := MaintenanceResult{}
	list, err := s.store.ListJobs(ctx, JobQuery{IncludeDisabled: true})
	if err != nil {
		return MaintenanceResult{}, err
	}
	for _, job := range list.Jobs {
		updated, changed, err := s.repairJobState(ctx, job, now)
		if err != nil {
			s.record(ctx, "error", "automation maintenance repair failed", automationEventFailed, map[string]any{
				"job_id": job.ID,
				"error":  err.Error(),
			})
			continue
		}
		if !changed {
			continue
		}
		if !job.State.RunningAt.IsZero() && updated.State.RunningAt.IsZero() {
			result.RunningMarkersRepaired++
		}
		if job.Enabled && job.State.NextRunAt.IsZero() && !updated.State.NextRunAt.IsZero() {
			result.SchedulesRepaired++
		}
		if !updated.Enabled && job.Enabled {
			result.InvalidJobsUpdated++
		}
	}
	if s.runHistoryRetention > 0 {
		deleted, err := s.store.DeleteRuns(ctx, RunDeleteQuery{
			StartedBefore: now.Add(-s.runHistoryRetention),
			Limit:         s.runHistoryCleanupLimit,
		})
		if err != nil {
			return MaintenanceResult{}, err
		}
		result.OldRunsDeleted = deleted
	}

	return result, nil
}

func (s *Service) Add(ctx context.Context, job Job) (Job, error) {
	if s == nil {
		return Job{}, errors.New("automation service is required")
	}

	prepared, err := s.prepareAddedJob(job, s.getNow())
	if err != nil {
		return Job{}, err
	}
	prepared.Authorization = getAutomationAuthorizationProvenance(ctx, s.getNow())

	if err := s.checkMutationPermission(ctx, permissions.Operation{
		Resource:      permissions.ResourceAutomation,
		Action:        permissions.ActionCreate,
		Effects:       []permissions.Effect{permissions.EffectExternalSystem, permissions.EffectWrite},
		Target:        prepared.ID,
		OwnerRequired: true,
	}); err != nil {
		return Job{}, err
	}

	created, err := s.store.CreateJob(ctx, prepared)
	if err != nil {
		return Job{}, err
	}
	s.notifyWake()

	return created, nil
}

func (s *Service) Update(ctx context.Context, patch JobPatch) (Job, error) {
	if s == nil {
		return Job{}, errors.New("automation service is required")
	}

	if err := ValidateJobID(patch.ID); err != nil {
		return Job{}, err
	}

	current, ok, err := s.store.GetJob(ctx, patch.ID)
	if err != nil {
		return Job{}, err
	}
	if !ok {
		return Job{}, errors.New("automation job not found")
	}

	patch, err = s.prepareAutomationJobPatch(current, patch, s.getNow())
	if err != nil {
		return Job{}, err
	}

	if err := s.checkMutationPermission(ctx, permissions.Operation{
		Resource:      permissions.ResourceAutomation,
		Action:        permissions.ActionUpdate,
		Effects:       []permissions.Effect{permissions.EffectExternalSystem, permissions.EffectWrite},
		Target:        patch.ID,
		OwnerRequired: true,
	}); err != nil {
		return Job{}, err
	}

	updated, err := s.store.PatchJob(ctx, patch)
	if err != nil {
		return Job{}, err
	}
	s.notifyWake()

	return updated, nil
}

func (s *Service) Remove(ctx context.Context, id string) error {
	if s == nil {
		return errors.New("automation service is required")
	}

	if err := s.checkMutationPermission(ctx, permissions.Operation{
		Resource: permissions.ResourceAutomation,
		Action:   permissions.ActionDelete,
		Effects: []permissions.Effect{
			permissions.EffectDestructive,
			permissions.EffectExternalSystem,
			permissions.EffectWrite,
		},
		Target:        id,
		OwnerRequired: true,
	}); err != nil {
		return err
	}

	if err := s.store.DeleteJob(ctx, id); err != nil {
		return err
	}

	s.notifyWake()

	return nil
}

func (s *Service) Run(ctx context.Context, id string) (Run, error) {
	if s == nil {
		return Run{}, errors.New("automation service is required")
	}

	if ctx == nil {
		ctx = context.Background()
	}

	job, ok, err := s.store.GetJob(ctx, id)
	if err != nil {
		return Run{}, err
	}
	if !ok {
		return Run{}, errors.New("automation job not found")
	}

	if err := s.checkMutationPermission(ctx, permissions.Operation{
		Resource: permissions.ResourceAutomation,
		Action:   permissions.ActionTrigger,
		Effects: []permissions.Effect{
			permissions.EffectExecution,
			permissions.EffectExternalSystem,
		},
		Target:        id,
		OwnerRequired: true,
	}); err != nil {
		return Run{}, err
	}

	if err := s.markJobRunning(ctx, job, s.getNow()); err != nil {
		if errors.Is(err, errAutomationJobAlreadyRunning) || errors.Is(err, errAutomationCapacityReached) {
			if err := s.waitRunSlot(ctx, job.ID); err != nil {
				return Run{}, err
			}
			if err := s.markJobRunning(ctx, job, s.getNow()); err != nil {
				return Run{}, err
			}
		} else {
			return Run{}, err
		}
	}

	loaded, ok, err := s.store.GetJob(ctx, id)
	if err != nil {
		return Run{}, err
	}
	if !ok {
		_ = s.clearJobRunning(ctx, job, errors.New("automation job not found"))
		s.clearRunningLocal(job.ID)
		return Run{}, errors.New("automation job not found")
	}

	return s.executeJob(ctx, loaded)
}

func (s *Service) checkMutationPermission(ctx context.Context, operation permissions.Operation) error {
	if s.permissionChecker == nil {
		return nil
	}

	_, err := s.permissionChecker.Check(ctx, permissions.EvaluationInput{Operation: operation})
	return err
}

func (s *Service) checkExecutionPermission(ctx context.Context, job Job) error {
	if s.permissionChecker == nil {
		return nil
	}

	input := permissions.EvaluationInput{Operation: permissions.Operation{
		Resource: permissions.ResourceAutomation,
		Action:   permissions.ActionExecute,
		Effects:  []permissions.Effect{permissions.EffectExecution, permissions.EffectExternalSystem},
		Target:   job.ID,
	}}
	_, err := s.permissionChecker.Check(ctx, input)
	decisionErr, ok := permissions.GetDecisionError(err)
	if err == nil || !ok || decisionErr.Code != permissions.ErrorCodeApprovalRequired || s.permissionApprover == nil {
		return err
	}

	return s.permissionApprover.Authorize(ctx, input)
}

func getAutomationAuthorizationProvenance(ctx context.Context, capturedAt time.Time) AuthorizationProvenance {
	authorization, ok := permissions.FromContext(ctx)
	if !ok {
		return AuthorizationProvenance{}
	}

	return AuthorizationProvenance{
		ActorKind:   string(authorization.Actor.Kind),
		ActorID:     authorization.Actor.ID,
		SurfaceKind: string(authorization.SurfaceKind),
		Surface:     string(authorization.Surface),
		Profile:     authorization.Profile,
		SessionID:   authorization.SessionID,
		RunID:       authorization.RunID,
		CapturedAt:  capturedAt.UTC(),
	}
}

func getAutomationExecutionContext(ctx context.Context, job Job, runID string) context.Context {
	provenance := job.Authorization

	return permissions.WithContext(ctx, permissions.AuthorizationContext{
		Actor:           permissions.Actor{Kind: permissions.ActorAutomation, ID: job.ID},
		Surface:         permissions.SurfaceAutomation,
		Profile:         job.Profile,
		SessionID:       job.SessionTarget,
		RunID:           runID,
		ParentActorKind: permissions.ActorKind(provenance.ActorKind),
		ParentActorID:   provenance.ActorID,
		ParentRunID:     provenance.RunID,
	})
}

func (s *Service) runLoop(ctx context.Context, done chan struct{}) {
	defer close(done)

	for {
		s.executeDueJobs(ctx)
		sleep := s.nextSleep(ctx)
		timer := time.NewTimer(sleep)
		select {
		case <-ctx.Done():
			stopTimer(timer)
			return
		case <-s.wake:
			stopTimer(timer)
		case <-timer.C:
		}
	}
}

func (s *Service) executeDueJobs(ctx context.Context) {
	now := s.getNow()
	list, err := s.store.ListJobs(ctx, JobQuery{})
	if err != nil {
		s.record(ctx, "error", "automation scheduler failed to list jobs", automationEventFailed, map[string]any{
			"error": err.Error(),
		})
		return
	}

	for _, job := range list.Jobs {
		if ctx.Err() != nil {
			return
		}
		if !s.hasRunCapacity() {
			return
		}

		if job.State.RunningAt.IsZero() && job.State.NextRunAt.IsZero() {
			repaired, err := s.repairJobSchedule(ctx, job, now, false, false)
			if err != nil {
				s.handleScheduleError(ctx, job, err, now)
				continue
			}
			job = repaired
		}
		if job.State.RunningAt.IsZero() &&
			!job.State.NextRunAt.IsZero() &&
			!job.State.NextRunAt.After(now) {
			if err := s.markJobRunning(ctx, job, now); err != nil {
				s.record(ctx, "warn", "automation scheduler skipped running job", automationEventSkipped, map[string]any{
					"job_id": job.ID,
					"error":  err.Error(),
				})
				if errors.Is(err, errAutomationCapacityReached) {
					return
				}
				continue
			}

			go func(job Job) {
				_, _ = s.executeJob(ctx, job)
			}(job)
		}
	}
}

func (s *Service) executeJob(ctx context.Context, job Job) (Run, error) {
	started := s.getNow()

	run, err := s.store.CreateRun(ctx, Run{
		JobID:     job.ID,
		Status:    RunStatusRunning,
		StartedAt: started,
	})
	if err != nil {
		_ = s.clearJobRunning(ctx, job, err)
		s.clearRunningLocal(job.ID)
		return Run{}, err
	}

	s.record(
		ctx, "info",
		"automation job started",
		automationEventStarted,
		map[string]any{
			"job_id": job.ID,
			"run_id": run.ID,
		},
	)

	executionContext := getAutomationExecutionContext(ctx, job, run.ID)
	runErr := s.checkExecutionPermission(executionContext, job)
	result := RunResult{}
	if runErr == nil {
		result, runErr = s.runAutomationWithRetry(executionContext, job)
	}
	finished := s.getNow()

	status := result.Status
	if status == "" {
		status = RunStatusOK
	}
	if runErr != nil {
		status = RunStatusError
	}

	deliveryResult, deliveryErr := s.deliverRunWithRetry(
		ctx,
		job,
		run.ID,
		status,
		result,
		runErr,
		finished,
	)

	usage := result.Usage
	patch := RunPatch{
		ID:             run.ID,
		Status:         status,
		EndedAt:        finished,
		Output:         result.Output,
		SessionID:      result.SessionID,
		DeliveryStatus: deliveryResult.Status,
		DeliveryError:  deliveryResult.Error,
		Model:          result.Model,
		Provider:       result.Provider,
		Usage:          &usage,
	}
	if runErr != nil {
		patch.Error = runErr.Error()
	}

	finishedRun, finishErr := s.store.FinishRun(ctx, patch)
	if finishErr != nil {
		_ = s.clearJobRunning(ctx, job, finishErr)
		s.clearRunningLocal(job.ID)
		return Run{}, finishErr
	}

	updatedJob, jobErr := s.finishJobRun(
		ctx,
		job,
		finishedRun,
		runErr,
		deliveryResult.FailureNoticeSentAt,
	)

	s.clearRunningLocal(job.ID)
	s.notifyWake()

	fields := map[string]any{
		"job_id":      job.ID,
		"run_id":      finishedRun.ID,
		"status":      string(finishedRun.Status),
		"delivery":    string(finishedRun.DeliveryStatus),
		"duration_ms": finishedRun.Duration.Milliseconds(),
	}

	if runErr != nil {
		fields["error"] = runErr.Error()
		s.record(
			ctx, "error",
			"automation job failed",
			automationEventFailed,
			fields,
		)
		return finishedRun, runErr
	}

	if jobErr != nil {
		fields["error"] = jobErr.Error()
		s.record(
			ctx, "error",
			"automation job state update failed",
			automationEventFailed,
			fields,
		)
		return finishedRun, jobErr
	}

	if deliveryErr != nil && !normalizeDelivery(updatedJob.Delivery).BestEffort {
		fields["error"] = deliveryErr.Error()
		s.record(
			ctx, "error",
			"automation job delivery failed",
			automationEventFailed,
			fields,
		)
		return finishedRun, deliveryErr
	}

	if updatedJob.DeleteAfterRun && finishedRun.Status == RunStatusOK {
		if err := s.store.DeleteJob(ctx, updatedJob.ID); err != nil {
			return finishedRun, err
		}
	}

	s.record(
		ctx, "info",
		"automation job finished",
		automationEventFinished,
		fields,
	)

	return finishedRun, nil
}

func (s *Service) deliverRunWithRetry(
	ctx context.Context,
	job Job,
	runID string,
	status RunStatus,
	result RunResult,
	runErr error,
	now time.Time,
) (DeliveryResult, error) {
	attempts := s.getRetryAttempts(job)
	var deliveryResult DeliveryResult
	var deliveryErr error
	for attempt := 1; ; attempt++ {
		s.record(ctx, "debug", "automation delivery attempt started", automationEventDeliveryStarted, map[string]any{
			"job_id":  job.ID,
			"run_id":  runID,
			"attempt": attempt,
		})
		deliveryCtx, cancel := s.contextWithDeliveryTimeout(ctx)
		deliveryResult, deliveryErr = s.deliverRun(deliveryCtx, job, runID, status, result, runErr, now)
		cancel()
		if deliveryErr == nil || attempt == attempts || ctx.Err() != nil {
			fields := map[string]any{
				"job_id":   job.ID,
				"run_id":   runID,
				"attempt":  attempt,
				"status":   string(deliveryResult.Status),
				"delivery": string(normalizeDelivery(job.Delivery).Mode),
			}
			if deliveryErr != nil {
				fields["error"] = deliveryErr.Error()
				s.record(ctx, "warn", "automation delivery attempt failed", automationEventDeliveryDone, fields)
			} else {
				s.record(ctx, "debug", "automation delivery attempt finished", automationEventDeliveryDone, fields)
			}
			return deliveryResult, deliveryErr
		}
		delay := s.getRetryDelay(job, attempt)
		s.record(ctx, "warn", "automation delivery retry scheduled", automationEventBackoff, map[string]any{
			"job_id":   job.ID,
			"run_id":   runID,
			"attempt":  attempt,
			"delay_ms": delay.Milliseconds(),
		})
		if sleepErr := s.sleep(ctx, delay); sleepErr != nil {
			return deliveryResult, sleepErr
		}
	}
}

func (s *Service) contextWithDeliveryTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}

	return context.WithTimeout(ctx, s.defaultDeliveryTimeout)
}

func (s *Service) runAutomationWithRetry(ctx context.Context, job Job) (RunResult, error) {
	attempts := s.getRetryAttempts(job)
	var result RunResult
	var err error
	for attempt := 1; ; attempt++ {
		runCtx, cancel := s.contextWithRunTimeout(ctx, job)
		result, err = s.runner.RunAutomation(runCtx, job.Clone())
		cancel()
		if err == nil || attempt == attempts || ctx.Err() != nil {
			return result, err
		}
		if sleepErr := s.sleep(ctx, s.getRetryDelay(job, attempt)); sleepErr != nil {
			return result, sleepErr
		}
	}
}

func (s *Service) contextWithRunTimeout(ctx context.Context, job Job) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if job.Payload.NoTimeout {
		return ctx, func() {}
	}
	timeout := job.Payload.MaxRuntime
	if timeout <= 0 {
		timeout = s.defaultRunTimeout
	}
	if timeout <= 0 {
		return ctx, func() {}
	}

	return context.WithTimeout(ctx, timeout)
}

func (s *Service) recoverStartup(ctx context.Context) error {
	now := s.getNow()
	list, err := s.store.ListJobs(ctx, JobQuery{IncludeDisabled: true})
	if err != nil {
		return err
	}

	catchUpOffset := time.Duration(0)
	for _, job := range list.Jobs {
		if !job.State.RunningAt.IsZero() && now.Sub(job.State.RunningAt) >= s.staleRunningAfter {
			job.State.RunningAt = time.Time{}
			updated, err := s.patchJobState(ctx, job.ID, job.State)
			if err != nil {
				return err
			}
			job = updated
		}
		if !job.Enabled {
			continue
		}
		repaired, err := s.repairStartupJobSchedule(ctx, job, now, catchUpOffset)
		if err != nil {
			s.handleScheduleError(ctx, job, err, now)
			continue
		}
		if checkRecentOneShotCatchUp(job, now, s.oneShotGrace) {
			job = repaired
			catchUpOffset += s.catchUpStagger
		}
	}

	if _, err := s.RunMaintenance(ctx); err != nil {
		return err
	}

	return nil
}

func (s *Service) repairStartupJobSchedule(ctx context.Context, job Job, now time.Time, catchUpOffset time.Duration) (Job, error) {
	if checkRecentOneShotCatchUp(job, now, s.oneShotGrace) {
		state := job.State
		state.RunningAt = time.Time{}
		state.NextRunAt = now.Add(catchUpOffset).UTC()
		return s.patchJobState(ctx, job.ID, state)
	}

	return s.repairJobSchedule(ctx, job, now, true, false)
}

func (s *Service) deliverRun(
	ctx context.Context,
	job Job,
	runID string,
	status RunStatus,
	result RunResult,
	runErr error,
	now time.Time,
) (DeliveryResult, error) {
	delivery := normalizeDelivery(job.Delivery)
	if runErr != nil {
		return s.deliverFailureNotice(ctx, job, runID, status, result, runErr, delivery, now)
	}
	if status != RunStatusOK {
		return DeliveryResult{Status: DeliveryStatusNotRequested}, nil
	}

	return s.deliver(ctx, job, runID, status, result, nil, delivery, getDeliveryTarget(job, delivery), now, false)
}

func (s *Service) deliverFailureNotice(
	ctx context.Context,
	job Job,
	runID string,
	status RunStatus,
	result RunResult,
	runErr error,
	delivery Delivery,
	now time.Time,
) (DeliveryResult, error) {
	if !checkFailureNoticeDue(job, delivery, now) {
		return DeliveryResult{Status: DeliveryStatusNotRequested}, nil
	}

	return s.deliver(ctx, job, runID, status, result, runErr, delivery, getFailureDeliveryTarget(job, delivery), now, true)
}

func (s *Service) deliver(
	ctx context.Context,
	job Job,
	runID string,
	status RunStatus,
	result RunResult,
	runErr error,
	delivery Delivery,
	target DeliveryTarget,
	now time.Time,
	failureNotice bool,
) (DeliveryResult, error) {
	if delivery.Mode == "" || delivery.Mode == DeliveryNone {
		return DeliveryResult{Status: DeliveryStatusNotRequested}, nil
	}

	req := newDeliveryRequest(job, runID, status, result, target, runErr)
	switch delivery.Mode {
	case DeliveryLocal:
	case DeliveryOrigin, DeliveryGateway:
		if s.deliverySink == nil {
			return DeliveryResult{
				Status: DeliveryStatusNotDelivered,
				Error:  "automation delivery sink is required",
			}, errors.New("automation delivery sink is required")
		}
		if err := s.deliverySink.DeliverAutomation(ctx, req); err != nil {
			return DeliveryResult{Status: DeliveryStatusNotDelivered, Error: err.Error()}, err
		}
	case DeliveryWebhook:
		if err := deliverWebhook(ctx, s.httpClient, delivery.WebhookURL, req); err != nil {
			return DeliveryResult{Status: DeliveryStatusNotDelivered, Error: err.Error()}, err
		}
	default:
		err := errors.New("unsupported automation delivery mode")
		return DeliveryResult{Status: DeliveryStatusNotDelivered, Error: err.Error()}, err
	}

	delivered := DeliveryResult{Status: DeliveryStatusDelivered}
	if failureNotice {
		delivered.FailureNoticeSentAt = now.UTC()
	}

	return delivered, nil
}

func (s *Service) repairJobSchedule(
	ctx context.Context,
	job Job,
	now time.Time,
	skipMissed bool,
	force bool,
) (Job, error) {
	if !job.Enabled {
		return job.Clone(), nil
	}
	if !force && !job.State.NextRunAt.IsZero() && job.State.NextRunAt.After(now) {
		return job.Clone(), nil
	}
	if skipMissed && !job.State.NextRunAt.IsZero() && !job.State.NextRunAt.After(now) {
		return s.skipMissedJob(ctx, job, now)
	}

	prepared, err := s.prepareJobSchedule(job, now)
	if err != nil {
		return Job{}, err
	}

	return s.patchJobState(ctx, job.ID, prepared.State)
}

func (s *Service) prepareJobSchedule(job Job, now time.Time) (Job, error) {
	if !job.Enabled {
		return job.Clone(), nil
	}

	evaluation, err := EvaluateJob(job, NextRunOptions{
		Now:             now,
		LastRunAt:       job.State.LastRunAt,
		Location:        s.location,
		DefaultTimezone: s.defaultTimezone,
	})
	if err != nil {
		return Job{}, err
	}

	return evaluation.Job, nil
}

func (s *Service) skipMissedJob(ctx context.Context, job Job, now time.Time) (Job, error) {
	state := job.State
	state.RunningAt = time.Time{}
	state.LastRunAt = now.UTC()
	state.LastStatus = RunStatusSkipped
	state.LastError = "missed scheduled run skipped during startup"
	state.LastDuration = 0
	if job.Schedule.Kind == ScheduleAt {
		state.NextRunAt = time.Time{}
	} else {
		result, err := NextRun(job.Schedule, NextRunOptions{
			Now:             now,
			LastRunAt:       now,
			Location:        s.location,
			DefaultTimezone: s.defaultTimezone,
		})
		if err != nil {
			return Job{}, err
		}
		state.NextRunAt = result.NextRunAt
	}

	updated, err := s.patchJobState(ctx, job.ID, state)
	if err != nil {
		return Job{}, err
	}
	s.record(ctx, "info", "automation job skipped during startup recovery", automationEventSkipped, map[string]any{
		"job_id": job.ID,
	})

	return updated, nil
}

func (s *Service) markJobRunning(ctx context.Context, job Job, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.running[job.ID]; ok {
		return errAutomationJobAlreadyRunning
	}
	if s.maxConcurrentRuns > 0 && len(s.running) >= s.maxConcurrentRuns {
		return errAutomationCapacityReached
	}
	loaded, ok, err := s.store.GetJob(ctx, job.ID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("automation job not found")
	}
	if !loaded.State.RunningAt.IsZero() {
		return errAutomationJobAlreadyRunning
	}
	loaded.State.RunningAt = now.UTC()
	if _, err := s.patchJobState(ctx, loaded.ID, loaded.State); err != nil {
		return err
	}
	s.running[job.ID] = struct{}{}

	return nil
}

func (s *Service) finishJobRun(
	ctx context.Context,
	job Job,
	run Run,
	runErr error,
	failureNoticeSentAt time.Time,
) (Job, error) {
	loaded, ok, err := s.store.GetJob(ctx, job.ID)
	if err != nil {
		return Job{}, err
	}
	if !ok {
		return Job{}, errors.New("automation job not found")
	}
	state := loaded.State
	state.RunningAt = time.Time{}
	state.LastRunAt = run.EndedAt
	state.LastStatus = run.Status
	state.LastDuration = run.Duration
	if runErr != nil {
		state.LastError = runErr.Error()
		state.ConsecutiveErrors++
	} else {
		state.LastError = ""
		state.ConsecutiveErrors = 0
		state.LastFailureNoticeAt = time.Time{}
	}
	if !failureNoticeSentAt.IsZero() {
		state.LastFailureNoticeAt = failureNoticeSentAt.UTC()
	}
	if runErr != nil {
		failedJob := loaded
		failedJob.State = state
		backoff := s.getFailureBackoff(failedJob)
		state.NextRunAt = run.EndedAt.Add(backoff).UTC()
		s.record(ctx, "warn", "automation failure backoff scheduled", automationEventBackoff, map[string]any{
			"job_id":             loaded.ID,
			"run_id":             run.ID,
			"consecutive_errors": state.ConsecutiveErrors,
			"delay_ms":           backoff.Milliseconds(),
			"next_run_at":        state.NextRunAt,
		})
		return s.patchJobState(ctx, loaded.ID, state)
	}
	result, err := NextRun(loaded.Schedule, NextRunOptions{
		Now:             run.EndedAt,
		LastRunAt:       run.EndedAt,
		Location:        s.location,
		DefaultTimezone: s.defaultTimezone,
	})
	if err != nil {
		return s.patchJobState(ctx, loaded.ID, state)
	}
	if result.Done {
		state.NextRunAt = time.Time{}
	} else {
		state.NextRunAt = result.NextRunAt
	}

	return s.patchJobState(ctx, loaded.ID, state)
}

func (s *Service) clearJobRunning(ctx context.Context, job Job, cause error) error {
	state := job.State
	if loaded, ok, err := s.store.GetJob(ctx, job.ID); err != nil {
		return err
	} else if ok {
		state = loaded.State
	}
	state.RunningAt = time.Time{}
	if cause != nil {
		state.LastError = cause.Error()
	}

	_, err := s.patchJobState(ctx, job.ID, state)
	return err
}

func (s *Service) handleScheduleError(ctx context.Context, job Job, scheduleErr error, now time.Time) {
	updated := ApplyScheduleError(job, scheduleErr, ScheduleErrorOptions{
		Now:          now,
		DisableAfter: s.disableAfterScheduleErrors,
	})
	if _, err := s.store.PatchJob(ctx, JobPatch{
		ID:      job.ID,
		Enabled: &updated.Enabled,
		State:   &updated.State,
	}); err != nil {
		s.record(ctx, "error", "automation schedule error patch failed", automationEventFailed, map[string]any{
			"job_id": job.ID,
			"error":  err.Error(),
		})
		return
	}
	s.record(ctx, "warn", "automation schedule evaluation failed", automationEventFailed, map[string]any{
		"job_id": job.ID,
		"error":  scheduleErr.Error(),
	})
}

func (s *Service) patchJobState(ctx context.Context, id string, state JobState) (Job, error) {
	return s.store.PatchJob(ctx, JobPatch{
		ID:    id,
		State: &state,
	})
}

func (s *Service) nextSleep(ctx context.Context) time.Duration {
	now := s.getNow()
	sleep := s.maxTimerSleep
	nextWake := now.Add(sleep)
	if !s.hasRunCapacity() {
		s.mu.Lock()
		s.nextWake = nextWake.UTC()
		s.mu.Unlock()

		return sleep
	}

	list, err := s.store.ListJobs(ctx, JobQuery{})
	if err == nil {
		for _, job := range list.Jobs {
			if !job.State.RunningAt.IsZero() || job.State.NextRunAt.IsZero() {
				continue
			}

			until := job.State.NextRunAt.Sub(now)
			if until <= 0 {
				sleep = 0
				nextWake = now
				break
			}
			if until < sleep {
				sleep = until
				nextWake = job.State.NextRunAt
			}
		}
	}

	s.mu.Lock()
	s.nextWake = nextWake.UTC()
	s.mu.Unlock()

	return sleep
}

func (s *Service) getNow() time.Time {
	if s == nil || s.now == nil {
		return time.Now().UTC()
	}

	return s.now().UTC()
}

func (s *Service) notifyWake() {
	if s == nil {
		return
	}

	select {
	case s.wake <- struct{}{}:
	default:
		select {
		case <-s.wake:
		default:
		}
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}

func (s *Service) waitRunSlot(ctx context.Context, jobID string) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if s.checkJobRunSlotAvailable(ctx, jobID) {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Service) checkJobRunSlotAvailable(ctx context.Context, jobID string) bool {
	s.mu.Lock()
	localRunning := false
	if _, ok := s.running[jobID]; ok {
		localRunning = true
	}
	hasCapacity := s.maxConcurrentRuns <= 0 || len(s.running) < s.maxConcurrentRuns
	s.mu.Unlock()
	if localRunning || !hasCapacity {
		return false
	}

	job, ok, err := s.store.GetJob(ctx, jobID)
	return err == nil && ok && job.State.RunningAt.IsZero()
}

func (s *Service) hasRunCapacity() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.maxConcurrentRuns <= 0 || len(s.running) < s.maxConcurrentRuns
}

func (s *Service) clearRunningLocal(jobID string) {
	s.mu.Lock()
	delete(s.running, jobID)
	s.mu.Unlock()
}

func (s *Service) getRetryAttempts(job Job) int {
	if job.Payload.RetryAttempts > 0 {
		return job.Payload.RetryAttempts
	}
	if s.defaultRetryAttempts > 0 {
		return s.defaultRetryAttempts
	}

	return 1
}

func (s *Service) getRetryDelay(job Job, attempt int) time.Duration {
	backoff := job.Payload.RetryBackoff
	if backoff <= 0 {
		backoff = s.defaultRetryBackoff
	}
	if backoff <= 0 {
		return 0
	}
	delay := backoff
	for i := 1; i < attempt; i++ {
		delay *= 2
	}
	maxDelay := job.Payload.RetryMaxDelay
	if maxDelay <= 0 {
		maxDelay = s.defaultRetryMaxDelay
	}
	if maxDelay > 0 && delay > maxDelay {
		return maxDelay
	}
	return delay
}

func (s *Service) getFailureBackoff(job Job) time.Duration {
	errorsCount := job.State.ConsecutiveErrors
	if errorsCount <= 0 {
		errorsCount = 1
	}
	return s.getRetryDelay(job, errorsCount)
}

func (s *Service) sleep(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer stopTimer(timer)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *Service) repairJobState(ctx context.Context, job Job, now time.Time) (Job, bool, error) {
	updated := job.Clone()
	changed := false
	if !updated.State.RunningAt.IsZero() && now.Sub(updated.State.RunningAt) >= s.staleRunningAfter {
		updated.State.RunningAt = time.Time{}
		changed = true
	}
	if updated.Enabled && updated.State.NextRunAt.IsZero() {
		prepared, err := s.prepareJobSchedule(updated, now)
		if err != nil {
			failed := ApplyScheduleError(updated, err, ScheduleErrorOptions{
				Now:          now,
				DisableAfter: s.disableAfterScheduleErrors,
			})
			patched, patchErr := s.store.PatchJob(ctx, JobPatch{
				ID:      updated.ID,
				Enabled: &failed.Enabled,
				State:   &failed.State,
			})
			return patched, true, patchErr
		}
		if prepared.State == updated.State {
			return updated, changed, nil
		}
		updated.State = prepared.State
		changed = true
	}
	if !changed {
		return updated, false, nil
	}
	patched, err := s.patchJobState(ctx, updated.ID, updated.State)
	return patched, true, err
}

func checkRecentOneShotCatchUp(job Job, now time.Time, grace time.Duration) bool {
	if !job.Enabled || job.Schedule.Kind != ScheduleAt || job.State.NextRunAt.IsZero() || job.State.NextRunAt.After(now) {
		return false
	}
	if grace <= 0 {
		return false
	}

	return !job.State.NextRunAt.Add(grace).Before(now)
}

func checkJobPatchNeedsScheduleRepair(patch JobPatch) bool {
	return patch.Schedule != nil ||
		patch.Enabled != nil ||
		patch.State != nil
}

func stopTimer(timer *time.Timer) {
	if timer == nil {
		return
	}

	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}
