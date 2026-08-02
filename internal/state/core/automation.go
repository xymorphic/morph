package core

import (
	"context"
	"errors"
	"maps"
	"strings"
	"time"

	"github.com/xymorphic/morph/pkg/nanoid"
	"github.com/xymorphic/morph/pkg/str"
)

const (
	// AutomationJobIDPrefix prefixes persisted automation job identifiers.
	AutomationJobIDPrefix = "auto_"
	// AutomationRunIDPrefix prefixes persisted automation run identifiers.
	AutomationRunIDPrefix = "autorun_"
)

// AutomationScheduleKind identifies how an automation job is scheduled.
type AutomationScheduleKind string

const (
	// AutomationScheduleAt runs a job once at an absolute time.
	AutomationScheduleAt AutomationScheduleKind = "at"
	// AutomationScheduleEvery runs a job repeatedly at a fixed interval.
	AutomationScheduleEvery AutomationScheduleKind = "every"
	// AutomationScheduleCron runs a job from a cron expression.
	AutomationScheduleCron AutomationScheduleKind = "cron"
)

// AutomationPayloadKind identifies the kind of work an automation run performs.
type AutomationPayloadKind string

const (
	// AutomationPayloadPrompt runs an agent turn from a stored prompt.
	AutomationPayloadPrompt AutomationPayloadKind = "prompt"
	// AutomationPayloadSystemEvent records a system event for future wake/reminder flows.
	AutomationPayloadSystemEvent AutomationPayloadKind = "system_event"
)

// AutomationDeliveryMode identifies where automation output should be delivered.
type AutomationDeliveryMode string

const (
	// AutomationDeliveryNone disables external delivery.
	AutomationDeliveryNone AutomationDeliveryMode = "none"
	// AutomationDeliveryLocal keeps delivery local to persisted run history.
	AutomationDeliveryLocal AutomationDeliveryMode = "local"
	// AutomationDeliveryOrigin delivers to the surface that created the job.
	AutomationDeliveryOrigin AutomationDeliveryMode = "origin"
	// AutomationDeliveryGateway delivers through a configured Morph gateway target.
	AutomationDeliveryGateway AutomationDeliveryMode = "gateway"
	// AutomationDeliveryWebhook delivers by POSTing to a configured webhook URL.
	AutomationDeliveryWebhook AutomationDeliveryMode = "webhook"
)

// AutomationRunStatus is the lifecycle status of one automation run.
type AutomationRunStatus string

const (
	// AutomationRunStatusRunning marks a run that has started and not finished.
	AutomationRunStatusRunning AutomationRunStatus = "running"
	// AutomationRunStatusOK marks a successful run.
	AutomationRunStatusOK AutomationRunStatus = "ok"
	// AutomationRunStatusError marks a run that failed during execution.
	AutomationRunStatusError AutomationRunStatus = "error"
	// AutomationRunStatusSkipped marks a run or scheduled occurrence that was skipped.
	AutomationRunStatusSkipped AutomationRunStatus = "skipped"
)

// AutomationDeliveryStatus is the recorded outcome of delivering run output.
type AutomationDeliveryStatus string

const (
	// AutomationDeliveryStatusDelivered means output reached the configured destination.
	AutomationDeliveryStatusDelivered AutomationDeliveryStatus = "delivered"
	// AutomationDeliveryStatusNotDelivered means delivery was requested and failed.
	AutomationDeliveryStatusNotDelivered AutomationDeliveryStatus = "not_delivered"
	// AutomationDeliveryStatusNotRequested means no delivery attempt was needed.
	AutomationDeliveryStatusNotRequested AutomationDeliveryStatus = "not_requested"
	// AutomationDeliveryStatusUnknown preserves compatibility for unknown delivery outcomes.
	AutomationDeliveryStatusUnknown AutomationDeliveryStatus = "unknown"
)

// AutomationSchedule stores the timing rule for an automation job.
type AutomationSchedule struct {
	Kind     AutomationScheduleKind `json:"kind,omitempty"`
	At       time.Time              `json:"at,omitempty"`
	Every    time.Duration          `json:"every,omitempty"`
	Cron     string                 `json:"cron,omitempty"`
	Timezone string                 `json:"timezone,omitempty"`
}

// AutomationPayload stores the work request and execution policy for an automation job.
type AutomationPayload struct {
	Kind          AutomationPayloadKind `json:"kind,omitempty"`
	Prompt        string                `json:"prompt,omitempty"`
	SystemEvent   string                `json:"systemEvent,omitempty"`
	Model         string                `json:"model,omitempty"`
	Provider      string                `json:"provider,omitempty"`
	BaseURL       string                `json:"baseUrl,omitempty"`
	NoTimeout     bool                  `json:"noTimeout,omitempty"`
	MaxRuntime    time.Duration         `json:"maxRuntime,omitempty"`
	MaxIterations int                   `json:"maxIterations,omitempty"`
	RetryAttempts int                   `json:"retryAttempts,omitempty"`
	RetryBackoff  time.Duration         `json:"retryBackoff,omitempty"`
	RetryMaxDelay time.Duration         `json:"retryMaxDelay,omitempty"`
	ToolGroups    []string              `json:"toolGroups,omitempty"`
	Metadata      map[string]string     `json:"metadata,omitempty"`
}

// AutomationDelivery stores the delivery target and failure notification policy for a job.
type AutomationDelivery struct {
	Mode            AutomationDeliveryMode `json:"mode,omitempty"`
	Channel         string                 `json:"channel,omitempty"`
	Target          string                 `json:"target,omitempty"`
	ThreadID        string                 `json:"threadId,omitempty"`
	WebhookURL      string                 `json:"webhookUrl,omitempty"`
	BestEffort      bool                   `json:"bestEffort,omitempty"`
	FailureTarget   string                 `json:"failureTarget,omitempty"`
	FailureAfter    int                    `json:"failureAfter,omitempty"`
	FailureCooldown time.Duration          `json:"failureCooldown,omitempty"`
}

// AutomationJobState stores mutable scheduler state derived from job execution.
type AutomationJobState struct {
	NextRunAt           time.Time           `json:"nextRunAt,omitempty"`
	RunningAt           time.Time           `json:"runningAt,omitempty"`
	LastRunAt           time.Time           `json:"lastRunAt,omitempty"`
	LastStatus          AutomationRunStatus `json:"lastStatus,omitempty"`
	LastError           string              `json:"lastError,omitempty"`
	LastDuration        time.Duration       `json:"lastDuration,omitempty"`
	ConsecutiveErrors   int                 `json:"consecutiveErrors,omitempty"`
	LastFailureNoticeAt time.Time           `json:"lastFailureNoticeAt,omitempty"`
}

type AutomationAuthorizationProvenance struct {
	ActorKind   string    `json:"actorKind,omitempty"`
	ActorID     string    `json:"actorId,omitempty"`
	SurfaceKind string    `json:"surfaceKind,omitempty"`
	Surface     string    `json:"surface,omitempty"`
	Profile     string    `json:"profile,omitempty"`
	SessionID   string    `json:"sessionId,omitempty"`
	RunID       string    `json:"runId,omitempty"`
	CapturedAt  time.Time `json:"capturedAt,omitempty"`
}

// AutomationUsage stores model token usage reported by an automation run.
type AutomationUsage struct {
	InputTokens      int `json:"inputTokens,omitempty"`
	OutputTokens     int `json:"outputTokens,omitempty"`
	TotalTokens      int `json:"totalTokens,omitempty"`
	CacheReadTokens  int `json:"cacheReadTokens,omitempty"`
	CacheWriteTokens int `json:"cacheWriteTokens,omitempty"`
}

// AutomationJob is a persisted scheduled automation definition.
type AutomationJob struct {
	ID             string
	Name           string
	Description    string
	Enabled        bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Schedule       AutomationSchedule
	Payload        AutomationPayload
	Delivery       AutomationDelivery
	Profile        string
	SessionTarget  string
	DeleteAfterRun bool
	Authorization  AutomationAuthorizationProvenance
	State          AutomationJobState
}

// AutomationJobPatch updates selected automation job fields.
type AutomationJobPatch struct {
	ID             string
	Name           *string
	Description    *string
	Enabled        *bool
	Schedule       *AutomationSchedule
	Payload        *AutomationPayload
	Delivery       *AutomationDelivery
	Profile        *string
	SessionTarget  *string
	DeleteAfterRun *bool
	State          *AutomationJobState
}

// AutomationJobQuery filters automation job listings.
type AutomationJobQuery struct {
	IDs             []string
	Enabled         *bool
	Profile         string
	SessionTarget   string
	Limit           int
	IncludeDisabled bool
}

// AutomationJobResult contains matching automation jobs.
type AutomationJobResult struct {
	Jobs []AutomationJob
}

// AutomationRun is a persisted execution record for one automation job run.
type AutomationRun struct {
	ID             string
	JobID          string
	Status         AutomationRunStatus
	StartedAt      time.Time
	EndedAt        time.Time
	Duration       time.Duration
	Output         string
	Error          string
	SessionID      string
	DeliveryStatus AutomationDeliveryStatus
	DeliveryError  string
	Model          string
	Provider       string
	Usage          AutomationUsage
}

// AutomationRunPatch finalizes or updates selected automation run fields.
type AutomationRunPatch struct {
	ID             string
	Status         AutomationRunStatus
	EndedAt        time.Time
	Output         string
	Error          string
	SessionID      string
	DeliveryStatus AutomationDeliveryStatus
	DeliveryError  string
	Model          string
	Provider       string
	Usage          *AutomationUsage
}

// AutomationRunQuery filters automation run listings.
type AutomationRunQuery struct {
	JobID  string
	IDs    []string
	Status []AutomationRunStatus
	Limit  int
}

// AutomationRunResult contains matching automation runs.
type AutomationRunResult struct {
	Runs []AutomationRun
}

// AutomationRunDeleteQuery selects automation runs for maintenance deletion.
type AutomationRunDeleteQuery struct {
	JobID         string
	IDs           []string
	StartedBefore time.Time
	Status        []AutomationRunStatus
	Limit         int
}

// AutomationStore persists automation jobs and run history.
type AutomationStore interface {
	CreateJob(context.Context, AutomationJob) (AutomationJob, error)
	GetJob(context.Context, string) (AutomationJob, bool, error)
	ListJobs(context.Context, AutomationJobQuery) (AutomationJobResult, error)
	PatchJob(context.Context, AutomationJobPatch) (AutomationJob, error)
	DeleteJob(context.Context, string) error
	CreateRun(context.Context, AutomationRun) (AutomationRun, error)
	FinishRun(context.Context, AutomationRunPatch) (AutomationRun, error)
	ListRuns(context.Context, AutomationRunQuery) (AutomationRunResult, error)
	DeleteRuns(context.Context, AutomationRunDeleteQuery) (int, error)
}

// ValidateAutomationJobID verifies that id is a valid automation job identifier.
func ValidateAutomationJobID(id string) error {
	jobID := str.String(id)
	trimmedID := jobID.Trim()
	if trimmedID == "" {
		return errors.New("automation job id is required")
	}
	if !strings.HasPrefix(trimmedID, AutomationJobIDPrefix) || nanoid.ValidateID(trimmedID) != nil {
		return errors.New("automation job id must be a valid auto_ nanoid")
	}
	return nil
}

// ValidateAutomationRunID verifies that id is a valid automation run identifier.
func ValidateAutomationRunID(id string) error {
	runID := str.String(id)
	trimmedID := runID.Trim()
	if trimmedID == "" {
		return errors.New("automation run id is required")
	}
	if !strings.HasPrefix(trimmedID, AutomationRunIDPrefix) || nanoid.ValidateID(trimmedID) != nil {
		return errors.New("automation run id must be a valid autorun_ nanoid")
	}
	return nil
}

// HasAutomationRunDeleteFilter reports whether query narrows a run deletion.
func HasAutomationRunDeleteFilter(query AutomationRunDeleteQuery) bool {
	jobID := str.String(query.JobID)
	return jobID.Trim() != "" ||
		len(query.IDs) > 0 ||
		!query.StartedBefore.IsZero() ||
		len(query.Status) > 0
}

// AutomationRunStatusSet returns a membership set for non-empty run statuses.
func AutomationRunStatusSet(statuses []AutomationRunStatus) map[AutomationRunStatus]struct{} {
	values := make(map[AutomationRunStatus]struct{}, len(statuses))
	for _, status := range statuses {
		if status != "" {
			values[status] = struct{}{}
		}
	}

	return values
}

// AutomationRunStatusesToStrings returns non-empty run statuses as strings.
func AutomationRunStatusesToStrings(statuses []AutomationRunStatus) []string {
	values := make([]string, 0, len(statuses))
	for _, status := range statuses {
		if status != "" {
			values = append(values, string(status))
		}
	}

	return values
}

// Clone returns a copy of job with mutable nested payload fields detached.
func (job AutomationJob) Clone() AutomationJob {
	job.Payload = job.Payload.Clone()
	return job
}

// Clone returns a copy of payload with mutable slices and maps detached.
func (payload AutomationPayload) Clone() AutomationPayload {
	if len(payload.ToolGroups) > 0 {
		payload.ToolGroups = append([]string(nil), payload.ToolGroups...)
	}
	if len(payload.Metadata) > 0 {
		metadata := make(map[string]string, len(payload.Metadata))
		maps.Copy(metadata, payload.Metadata)
		payload.Metadata = metadata
	}
	return payload
}

// Clone returns a copy of run.
func (run AutomationRun) Clone() AutomationRun {
	return run
}

// ApplyAutomationJobPatch applies patch to job and updates the job timestamp.
func ApplyAutomationJobPatch(job AutomationJob, patch AutomationJobPatch, updatedAt time.Time) AutomationJob {
	if patch.Name != nil {
		job.Name = *patch.Name
	}
	if patch.Description != nil {
		job.Description = *patch.Description
	}
	if patch.Enabled != nil {
		job.Enabled = *patch.Enabled
	}
	if patch.Schedule != nil {
		job.Schedule = *patch.Schedule
	}
	if patch.Payload != nil {
		job.Payload = patch.Payload.Clone()
	}
	if patch.Delivery != nil {
		job.Delivery = *patch.Delivery
	}
	if patch.Profile != nil {
		job.Profile = *patch.Profile
	}
	if patch.SessionTarget != nil {
		job.SessionTarget = *patch.SessionTarget
	}
	if patch.DeleteAfterRun != nil {
		job.DeleteAfterRun = *patch.DeleteAfterRun
	}
	if patch.State != nil {
		job.State = *patch.State
	}
	job.UpdatedAt = updatedAt.UTC()
	return job.Clone()
}

// ApplyAutomationRunPatch applies patch to run and derives completion metadata.
func ApplyAutomationRunPatch(run AutomationRun, patch AutomationRunPatch, now time.Time) AutomationRun {
	if patch.Status != "" {
		run.Status = patch.Status
	}
	if patch.EndedAt.IsZero() {
		run.EndedAt = now.UTC()
	} else {
		run.EndedAt = patch.EndedAt.UTC()
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = run.EndedAt
	}
	if run.Duration <= 0 && !run.EndedAt.IsZero() {
		run.Duration = run.EndedAt.Sub(run.StartedAt)
	}
	run.Output = patch.Output
	run.Error = patch.Error
	sessionID := str.String(patch.SessionID)
	run.SessionID = sessionID.Trim()
	if patch.DeliveryStatus != "" {
		run.DeliveryStatus = patch.DeliveryStatus
	}
	run.DeliveryError = patch.DeliveryError
	model := str.String(patch.Model)
	provider := str.String(patch.Provider)
	run.Model = model.Trim()
	run.Provider = provider.Trim()
	if patch.Usage != nil {
		run.Usage = *patch.Usage
	}
	return run.Clone()
}
