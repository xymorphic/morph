package core

import (
	"context"
	"time"

	morphmsg "github.com/wandxy/morph/pkg/agent/message"
	agentsession "github.com/wandxy/morph/pkg/agent/session"
	"github.com/wandxy/morph/pkg/gateway/pairing"
	"github.com/wandxy/morph/pkg/nanoid"
)

// DefaultSessionID is the package-level default session id constant.
const DefaultSessionID = "default"

// SessionIDPrefix is the package-level session id prefix constant.
const SessionIDPrefix = "ses_"

// SessionTitleSourceGenerated is the package-level session title source generated constant.
const SessionTitleSourceGenerated = "generated"

// SessionTitleSourceManual is the package-level session title source manual constant.
const SessionTitleSourceManual = "manual"

const (
	SessionOriginSourceTerminal   = "terminal"
	SessionOriginSourceAutomation = "automation"
	SessionOriginSourceCLI        = "cli"
	SessionOriginSourceGeneric    = "generic"
	SessionOriginSourceGUI        = "gui"
	SessionOriginSourceSlack      = "slack"
	SessionOriginSourceTelegram   = "telegram"
	SessionOriginSourceTUI        = "tui"
)

// NewSessionID returns a newly generated session ID.
func NewSessionID() (string, error) {
	return nanoid.Generate(SessionIDPrefix)
}

// Session describes an active conversation session.
type Session struct {
	Compaction                 SessionCompaction
	Origin                     SessionOrigin
	ID                         string
	EpisodicCheckpointOffset   int
	LastPromptTokens           int
	ReflectionCheckpointOffset int
	Title                      string
	TitleSource                string
	Archived                   bool
	ArchivedAt                 time.Time
	ExpiresAt                  time.Time
	UpdatedAt                  time.Time
	CreatedAt                  time.Time
}

type SessionOrigin struct {
	Source         string
	AccountID      string
	ConversationID string
	ThreadID       string
}

type SessionCreateOptions struct {
	Origin SessionOrigin
}

// CheckpointPatch describes changes to apply to checkpoint state.
type CheckpointPatch struct {
	EpisodicOffset   *int
	ReflectionOffset *int
}

// SessionCompactionStatus records whether session history has been compacted.
type SessionCompactionStatus string

const (
	CompactionStatusPending   SessionCompactionStatus = "pending"
	CompactionStatusRunning   SessionCompactionStatus = "running"
	CompactionStatusSucceeded SessionCompactionStatus = "succeeded"
	CompactionStatusFailed    SessionCompactionStatus = "failed"
)

// SessionCompaction records compaction metadata for a session.
type SessionCompaction struct {
	CompletedAt        time.Time
	FailedAt           time.Time
	LastError          string
	RequestedAt        time.Time
	StartedAt          time.Time
	Status             SessionCompactionStatus
	TargetMessageCount int
	TargetOffset       int
}

// SessionArchiveRequest describes a session archive state transition.
type SessionArchiveRequest struct {
	ArchivedAt time.Time
	ExpiresAt  time.Time
}

// SessionGetOptions filters session lookup operations.
type SessionGetOptions struct {
	Archived *bool
}

// SessionListOptions filters session listing operations.
type SessionListOptions struct {
	Archived     *bool
	OriginSource string
}

// SessionRenameRequest describes a session title change.
type SessionRenameRequest struct {
	SessionID   string
	Title       string
	TitleSource string
	RenamedAt   time.Time
}

// SessionSummary summarizes session state.
type SessionSummary struct {
	SessionID          string
	SourceEndOffset    int
	SourceMessageCount int
	UpdatedAt          time.Time
	SessionSummary     string
	CurrentTask        string
	Discoveries        []string
	OpenQuestions      []string
	NextActions        []string
}

type GatewayBinding struct {
	Key       string
	SessionID string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type DeliveryMode = agentsession.DeliveryMode
type SteeringFallback = agentsession.SteeringFallback
type QueueStatus = agentsession.QueueStatus
type RunStatus = agentsession.RunStatus
type SessionQueueProvenance = agentsession.Provenance
type SessionQueueEntry = agentsession.QueueEntry
type SessionActiveRun = agentsession.ActiveRun
type SessionExecutionState = agentsession.ExecutionState
type SessionEvent = agentsession.Event
type SessionEventBatch = agentsession.EventBatch
type SessionSubmitRequest = agentsession.SubmitRequest
type SessionQueueEditRequest = agentsession.QueueEditRequest
type SessionQueueMutationRequest = agentsession.QueueMutationRequest
type SessionClaimRequest = agentsession.ClaimRequest
type SessionSteeringClaimRequest = agentsession.SteeringClaimRequest
type SessionRunFinishRequest = agentsession.RunFinishRequest
type SessionReconcileResult = agentsession.ReconcileResult

// SessionMetadataStore defines persisted session metadata operations.
type SessionMetadataStore interface {
	Save(ctx context.Context, session Session) error
	Get(ctx context.Context, id string, opts SessionGetOptions) (Session, bool, error)
	List(ctx context.Context, opts SessionListOptions) ([]Session, error)
	Rename(ctx context.Context, req SessionRenameRequest) (Session, error)
	Delete(ctx context.Context, id string) error
	UpdateCheckpoints(ctx context.Context, id string, patch CheckpointPatch) error
	Archive(ctx context.Context, id string, req SessionArchiveRequest) (Session, error)
	Unarchive(ctx context.Context, id string) (Session, error)
	DeleteExpiredArchives(ctx context.Context, now time.Time) error
}

// CurrentSessionStore defines current session tracking operations.
type CurrentSessionStore interface {
	SetCurrent(ctx context.Context, id string) error
	Current(ctx context.Context) (string, bool, error)
	ClearCurrent(ctx context.Context) error
}

// SessionMessageStore defines persisted session message operations.
type SessionMessageStore interface {
	AppendMessages(ctx context.Context, id string, messages []morphmsg.Message) error
	CountMessages(ctx context.Context, id string, opts MessageQueryOptions) (int, error)
	GetMessage(ctx context.Context, id string, index int) (morphmsg.Message, bool, error)
	GetMessages(ctx context.Context, id string, opts MessageQueryOptions) ([]morphmsg.Message, error)
	GetMessagesByIDs(ctx context.Context, id string, messageIDs []uint) ([]MessageRecord, error)
	GetMessageWindow(ctx context.Context, id string, anchorMessageID uint, before int, after int) ([]MessageRecord, error)
	SearchMessages(ctx context.Context, id string, opts SearchMessageOptions) ([]SearchMessageResult, error)
	ClearMessages(ctx context.Context, id string) error
}

// SessionSummaryStore defines persisted session summary operations.
type SessionSummaryStore interface {
	SaveSummary(ctx context.Context, summary SessionSummary) error
	GetSummary(ctx context.Context, sessionID string) (SessionSummary, bool, error)
	DeleteSummary(ctx context.Context, sessionID string) error
}

type SessionInboxStore = agentsession.InboxStore

type GatewayBindingStore interface {
	SaveGatewayBinding(ctx context.Context, binding GatewayBinding) error
	GetGatewayBinding(ctx context.Context, key string) (GatewayBinding, bool, error)
}

type GatewayPairingStore interface {
	SaveGatewayPairingRequest(context.Context, pairing.PendingRequest) error
	GetGatewayPairingRequest(context.Context, string, string) (pairing.PendingRequest, bool, error)
	ListGatewayPairingRequests(context.Context, string) ([]pairing.PendingRequest, error)
	DeleteGatewayPairingRequest(context.Context, string, string) error
	ClearGatewayPairingRequests(context.Context, string) error
	SaveGatewayPairedSender(context.Context, pairing.ApprovedSender) error
	GetGatewayPairedSender(context.Context, string, string) (pairing.ApprovedSender, bool, error)
	ListGatewayPairedSenders(context.Context, string) ([]pairing.ApprovedSender, error)
	DeleteGatewayPairedSender(context.Context, string, string) error
}

// SessionStore defines the persistence operations for conversation sessions.
type SessionStore interface {
	SessionMetadataStore
	CurrentSessionStore
	SessionMessageStore
	SessionSummaryStore
	GatewayBindingStore
	GatewayPairingStore
}
