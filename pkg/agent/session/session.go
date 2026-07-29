package session

import (
	"context"
	"errors"
	"time"

	"github.com/wandxy/morph/pkg/agent/message"
)

const (
	DefaultID = "default"

	MessageOrderAsc  = "asc"
	MessageOrderDesc = "desc"
)

var (
	ErrCursorExpired         = errors.New("session event cursor expired")
	ErrCursorBeyondSession   = errors.New("after cursor is beyond the session cursor")
	ErrProgressExpired       = errors.New("session progress expired")
	ErrStaleRunnerGeneration = errors.New("stale session runner generation")
	ErrSteeringRequiresRun   = errors.New("steering requires an active run")
	ErrReasoningStaleTuple   = errors.New("reasoning model selection is stale")
	ErrReasoningUnsupported  = errors.New("reasoning effort is unsupported")
	ErrReasoningUnavailable  = errors.New("reasoning effort is not adjustable")
	ErrReasoningInvalid      = errors.New("reasoning effort request is invalid")
)

type CompactionStatus string

const (
	CompactionStatusPending   CompactionStatus = "pending"
	CompactionStatusRunning   CompactionStatus = "running"
	CompactionStatusSucceeded CompactionStatus = "succeeded"
	CompactionStatusFailed    CompactionStatus = "failed"
)

type Session struct {
	Compaction                 Compaction
	Origin                     Origin
	ID                         string
	ReasoningEffortOverride    string
	EpisodicCheckpointOffset   int
	LastPromptTokens           int
	ReflectionCheckpointOffset int
	Title                      string
	TitleSource                string
	Archived                   bool
	ArchivedAt                 time.Time
	CreatedAt                  time.Time
	UpdatedAt                  time.Time
	ExpiresAt                  time.Time
}

type Origin struct {
	Source         string
	AccountID      string
	ConversationID string
	ThreadID       string
}

type Compaction struct {
	CompletedAt        time.Time
	FailedAt           time.Time
	LastError          string
	RequestedAt        time.Time
	StartedAt          time.Time
	Status             CompactionStatus
	TargetMessageCount int
	TargetOffset       int
}

type MessageQuery struct {
	Limit  int
	Name   string
	Order  string
	Offset int
	Role   message.Role
}

type Store interface {
	Resolve(context.Context, string) (Session, error)
	GetMessages(context.Context, string, MessageQuery) ([]message.Message, error)
	AppendMessages(context.Context, string, []message.Message) error
	UpdateLastPromptTokens(context.Context, string, int) error
}

type DeliveryMode string

const (
	DeliveryModeFollowUp DeliveryMode = "follow_up"
	DeliveryModeSteering DeliveryMode = "steering"
)

type SteeringFallback string

const (
	SteeringFallbackReject   SteeringFallback = "reject"
	SteeringFallbackFollowUp SteeringFallback = "follow_up"
)

type QueueStatus string

const (
	QueueStatusPending     QueueStatus = "pending"
	QueueStatusActive      QueueStatus = "active"
	QueueStatusDelivered   QueueStatus = "delivered"
	QueueStatusCompleted   QueueStatus = "completed"
	QueueStatusInterrupted QueueStatus = "interrupted"
	QueueStatusFailed      QueueStatus = "failed"
	QueueStatusCancelled   QueueStatus = "cancelled"
)

type RunStatus string

const (
	RunStatusRunning     RunStatus = "running"
	RunStatusCompleted   RunStatus = "completed"
	RunStatusInterrupted RunStatus = "interrupted"
	RunStatusFailed      RunStatus = "failed"
	RunStatusCancelled   RunStatus = "cancelled"
)

type Provenance struct {
	ActorKind   string
	ActorID     string
	SurfaceKind string
	Surface     string
	Profile     string
}

type QueueEntry struct {
	ID                    string
	SessionID             string
	Content               string
	Instruct              string
	Stream                *bool
	ClientSubmissionID    string
	TargetRunID           string
	RequestedDeliveryMode DeliveryMode
	DeliveryMode          DeliveryMode
	SteeringFallback      SteeringFallback
	Status                QueueStatus
	Provenance            Provenance
	Sequence              int64
	Priority              int64
	CreatedAt             time.Time
	UpdatedAt             time.Time
	StartedAt             time.Time
	CompletedAt           time.Time
	LastError             string
}

type ActiveRun struct {
	ID           string
	SessionID    string
	QueueEntryID string
	Generation   string
	Status       RunStatus
	StartedAt    time.Time
	CompletedAt  time.Time
	UpdatedAt    time.Time
	Reason       string
	LastError    string
	Reasoning    ReasoningSnapshot
}

type ExecutionState struct {
	SessionID            string
	ActiveRun            *ActiveRun
	Queue                []QueueEntry
	Cursor               int64
	RetainedCursorFloor  int64
	QueueDepth           int
	OldestPendingCreated time.Time
	Progress             []ProgressEvent
	Reasoning            ReasoningSettings
}

type EventType string

const (
	EventTypeQueueEnqueued  EventType = "queue_enqueued"
	EventTypeQueueUpdated   EventType = "queue_updated"
	EventTypeQueueCancelled EventType = "queue_cancelled"
	EventTypeQueueClaimed   EventType = "queue_claimed"
	EventTypeSteeringSent   EventType = "steering_delivered"
	EventTypeRunStarted     EventType = "run_started"
	EventTypeRunCompleted   EventType = "run_completed"
	EventTypeRunInterrupted EventType = "run_interrupted"
	EventTypeRunFailed      EventType = "run_failed"
	EventTypeRunCancelled   EventType = "run_cancelled"
)

type Event struct {
	SessionID string
	Type      EventType
	Cursor    int64
	CreatedAt time.Time
	Queue     *QueueEntry
	Run       *ActiveRun
	Progress  *ProgressEvent
}

type ProgressEvent struct {
	RunID        string
	QueueEntryID string
	Kind         string
	Channel      string
	Text         string
	Sequence     int64
	TraceEvent   *TraceEvent
}

type EventBatch struct {
	Events              []Event
	Cursor              int64
	RetainedCursorFloor int64
}

type SubmitRequest struct {
	ID                 string
	SessionID          string
	Content            string
	Instruct           string
	Stream             *bool
	ClientSubmissionID string
	DeliveryMode       DeliveryMode
	SteeringFallback   SteeringFallback
	Provenance         Provenance
}

type QueueEditRequest struct {
	SessionID string
	EntryID   string
	Content   string
}

type QueueMutationRequest struct {
	SessionID string
	EntryID   string
}

type ClaimRequest struct {
	SessionID  string
	RunID      string
	Generation string
	Reasoning  ReasoningClaimContext
}

type SetReasoningEffortRequest struct {
	SessionID     string
	ExpectedModel ReasoningModelTuple
	Effort        ReasoningEffort
	Reset         bool
}

type SteeringClaimRequest struct {
	SessionID  string
	RunID      string
	Generation string
}

type RunFinishRequest struct {
	SessionID  string
	RunID      string
	Generation string
	Status     RunStatus
	Reason     string
	LastError  string
}

type ReconcileResult struct {
	SessionIDs []string
	Runs       []ActiveRun
	RunCount   int
}

type InboxStore interface {
	SubmitMessage(context.Context, SubmitRequest) (QueueEntry, error)
	GetExecutionState(context.Context, string) (ExecutionState, error)
	ListEvents(context.Context, string, int64, int) (EventBatch, error)
	EditQueueEntry(context.Context, QueueEditRequest) (QueueEntry, error)
	CancelQueueEntry(context.Context, QueueMutationRequest) (QueueEntry, error)
	PromoteQueueEntry(context.Context, QueueMutationRequest) (QueueEntry, error)
	SteerQueueEntry(context.Context, QueueMutationRequest) (QueueEntry, error)
	ClaimNextFollowUp(context.Context, ClaimRequest) (QueueEntry, ActiveRun, bool, error)
	HasPendingSteering(context.Context, SteeringClaimRequest) (bool, error)
	ClaimSteering(context.Context, SteeringClaimRequest) ([]QueueEntry, error)
	FinishSessionRun(context.Context, RunFinishRequest) (ActiveRun, bool, error)
	ReconcileActiveRuns(context.Context, string) (ReconcileResult, error)
	ListRunnableSessions(context.Context) ([]string, error)
}

type TraceEvent struct {
	ID        uint
	SessionID string
	Sequence  int
	Type      string
	Timestamp time.Time
	Payload   any
}

type TraceRecorder interface {
	AppendTraceEvent(context.Context, TraceEvent) (TraceEvent, error)
}
