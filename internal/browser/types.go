package browser

import (
	"context"
	"errors"
	"time"

	"github.com/xymorphic/morph/internal/permissions"
)

type Action string

const (
	ActionStatus         Action = "status"
	ActionStart          Action = "start"
	ActionStop           Action = "stop"
	ActionConnect        Action = "connect"
	ActionProfiles       Action = "profiles"
	ActionTabs           Action = "tabs"
	ActionOpen           Action = "open"
	ActionFocus          Action = "focus"
	ActionClose          Action = "close"
	ActionNavigate       Action = "navigate"
	ActionReload         Action = "reload"
	ActionSnapshot       Action = "snapshot"
	ActionScreenshot     Action = "screenshot"
	ActionPDF            Action = "pdf"
	ActionConsole        Action = "console"
	ActionClick          Action = "click"
	ActionType           Action = "type"
	ActionPress          Action = "press"
	ActionScroll         Action = "scroll"
	ActionSelect         Action = "select"
	ActionUpload         Action = "upload"
	ActionDownload       Action = "download"
	ActionExportArtifact Action = "export_artifact"
	ActionAcceptDialog   Action = "accept_dialog"
	ActionDismissDialog  Action = "dismiss_dialog"
	ActionWait           Action = "wait"
	ActionBack           Action = "back"
	ActionForward        Action = "forward"
)

func SupportedActions() []Action {
	return []Action{
		ActionStatus, ActionProfiles, ActionStart, ActionStop, ActionTabs, ActionOpen, ActionFocus, ActionClose,
		ActionNavigate, ActionReload, ActionSnapshot, ActionScreenshot, ActionPDF, ActionConsole, ActionClick,
		ActionType, ActionPress, ActionScroll, ActionSelect, ActionUpload, ActionDownload, ActionAcceptDialog,
		ActionExportArtifact, ActionDismissDialog, ActionWait, ActionBack, ActionForward,
	}
}

type ErrorCode string

const (
	ErrorInvalidRequest ErrorCode = "invalid_request"
	ErrorUnavailable    ErrorCode = "browser_unavailable"
	ErrorStartFailed    ErrorCode = "browser_start_failed"
	ErrorHealthFailed   ErrorCode = "browser_health_failed"
	ErrorNotFound       ErrorCode = "browser_not_found"
	ErrorOwnership      ErrorCode = "browser_ownership"
	ErrorClosed         ErrorCode = "browser_closed"
	ErrorNotReady       ErrorCode = "browser_not_ready"
	ErrorStaleReference ErrorCode = "browser_stale_reference"
	ErrorTimeout        ErrorCode = "browser_timeout"
	ErrorCancelled      ErrorCode = "browser_cancelled"
)

type Error struct {
	Code      ErrorCode
	Operation Action
	Retryable bool
	Err       error
}

func (e *Error) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}

	return e.Err.Error()
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}

	return e.Err
}

func GetError(err error) (*Error, bool) {
	var browserErr *Error
	ok := errors.As(err, &browserErr)
	return browserErr, ok
}

type SessionState string

const (
	SessionStarting SessionState = "starting"
	SessionReady    SessionState = "ready"
	SessionStopping SessionState = "stopping"
	SessionFailed   SessionState = "failed"
	SessionStopped  SessionState = "stopped"
)

type Owner struct {
	Actor     permissions.Actor
	Profile   string
	SessionID string
	RunID     string
}

type Session struct {
	ID          string       `json:"id"`
	Profile     string       `json:"profile"`
	ProfileMode string       `json:"profile_mode"`
	State       SessionState `json:"state"`
	Owner       Owner        `json:"-"`
	CreatedAt   time.Time    `json:"created_at"`
	LastActive  time.Time    `json:"last_active"`
	Error       string       `json:"error,omitempty"`
	Warning     string       `json:"warning,omitempty"`
}

type Profile struct {
	Name      string `json:"name"`
	Mode      string `json:"mode"`
	Default   bool   `json:"default"`
	Available bool   `json:"available"`
	Warning   string `json:"warning,omitempty"`
}

type Tab struct {
	ID         string `json:"id"`
	SessionID  string `json:"session_id"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	Active     bool   `json:"active"`
	Generation uint64 `json:"generation"`
}

type SnapshotNode struct {
	Ref         string            `json:"ref,omitempty"`
	Role        string            `json:"role"`
	Name        string            `json:"name,omitempty"`
	Value       string            `json:"value,omitempty"`
	Description string            `json:"description,omitempty"`
	Disabled    bool              `json:"disabled,omitempty"`
	Properties  map[string]string `json:"properties,omitempty"`
}

type Snapshot struct {
	TabID      string         `json:"tab_id"`
	URL        string         `json:"url"`
	Title      string         `json:"title"`
	Generation uint64         `json:"generation"`
	Nodes      []SnapshotNode `json:"nodes"`
	Truncated  bool           `json:"truncated,omitempty"`
}

type WaitCondition string

const (
	WaitLoad    WaitCondition = "load"
	WaitText    WaitCondition = "text"
	WaitURL     WaitCondition = "url"
	WaitVisible WaitCondition = "visible"
)

type ActionRequest struct {
	Profile     string
	SessionID   string
	TabID       string
	URL         string
	Path        string
	Handle      string
	FileTarget  string
	Ref         string
	Text        string
	Value       string
	Key         string
	X           int64
	Y           int64
	Limit       int
	Condition   WaitCondition
	Timeout     time.Duration
	Replace     bool
	FullPage    bool
	TargetScope permissions.TargetScope
}

type BackendTab struct {
	ID               string
	BrowserContextID string
	Title            string
	URL              string
	Active           bool
}

type BackendSnapshotNode struct {
	BackendNodeID int64
	Sensitive     bool
	Role          string
	Name          string
	Value         string
	Description   string
	Disabled      bool
	Properties    map[string]string
}

type BackendSnapshot struct {
	URL   string
	Title string
	Nodes []BackendSnapshotNode
}

type ArtifactKind string

const (
	ArtifactScreenshot ArtifactKind = "screenshot"
	ArtifactPDF        ArtifactKind = "pdf"
	ArtifactDownload   ArtifactKind = "download"
)

type Artifact struct {
	Handle    string               `json:"handle"`
	Kind      ArtifactKind         `json:"kind"`
	Name      string               `json:"name"`
	MIMEType  string               `json:"mime_type"`
	Size      int64                `json:"size"`
	Profile   string               `json:"profile"`
	SessionID string               `json:"session_id"`
	RunID     string               `json:"run_id,omitempty"`
	Source    string               `json:"source"`
	Effects   []permissions.Effect `json:"effects"`
	Sensitive bool                 `json:"sensitive"`
	CreatedAt time.Time            `json:"created_at"`
	ExpiresAt time.Time            `json:"expires_at"`
}

type ArtifactContent struct {
	Artifact Artifact `json:"artifact"`
	Data     []byte   `json:"-"`
}

type ArtifactExportRequest struct {
	Handle      string
	Path        string
	FileTarget  string
	TargetScope permissions.TargetScope
}

type BackendArtifact struct {
	Kind      ArtifactKind
	Name      string
	MIMEType  string
	SourceURL string
	Data      []byte `json:"-"`
}

type ConsoleLevel string

const (
	ConsoleDebug ConsoleLevel = "debug"
	ConsoleInfo  ConsoleLevel = "info"
	ConsoleWarn  ConsoleLevel = "warn"
	ConsoleError ConsoleLevel = "error"
)

type ConsoleMessage struct {
	Level     ConsoleLevel `json:"level"`
	Text      string       `json:"text"`
	Timestamp time.Time    `json:"timestamp"`
}

type StartRequest struct {
	Profile string
}

type Status struct {
	Enabled  bool      `json:"enabled"`
	Profiles []Profile `json:"profiles"`
	Sessions []Session `json:"sessions"`
}

type LaunchOptions struct {
	Executable       string
	Mode             string
	DataDir          string
	DownloadRoot     string
	CDPEndpoint      string
	ProxyURL         string
	ProxyUser        string
	ProxySecret      string
	AttachmentScope  string
	BrowserContextID string
	TargetIDs        []string
	Timeout          time.Duration
	transportPermits *transportPermitLedger
}

type Backend interface {
	Start(context.Context, LaunchOptions) (BackendSession, error)
}

type BackendSession interface {
	Health(context.Context) error
	Close(context.Context) error
}

type InteractiveBackendSession interface {
	ListTabs(context.Context) ([]BackendTab, error)
	OpenTab(context.Context, string) (BackendTab, error)
	FocusTab(context.Context, string) error
	CloseTab(context.Context, string) error
	Navigate(context.Context, string, string) (BackendTab, error)
	Back(context.Context, string) (BackendTab, error)
	Forward(context.Context, string) (BackendTab, error)
	Reload(context.Context, string) (BackendTab, error)
	Snapshot(context.Context, string) (BackendSnapshot, error)
	Click(context.Context, string, int64) error
	Type(context.Context, string, int64, string, bool) error
	Press(context.Context, string, string) error
	Scroll(context.Context, string, int64, int64) error
	Select(context.Context, string, int64, string) error
	Wait(context.Context, string, WaitCondition, string, int64) error
}

type RichBackendSession interface {
	Screenshot(context.Context, string, bool) (BackendArtifact, error)
	PDF(context.Context, string) (BackendArtifact, error)
	Console(context.Context, string, int) ([]ConsoleMessage, error)
	Upload(context.Context, string, int64, string) error
	Download(context.Context, string, int64, int64) (BackendArtifact, error)
	RespondToDialog(context.Context, string, int64, bool, string) error
}

type NetworkRequestAuthorizer func(context.Context, permissions.NetworkTarget) error

type NetworkAuthorizingBackendSession interface {
	SetNetworkAuthorizer(string, NetworkRequestAuthorizer) func()
}

type NetworkSettlingBackendSession interface {
	WaitForNetworkIdle(context.Context, string, time.Duration) error
}
