package execution

import (
	"context"
	"errors"
	"time"

	processenv "github.com/xymorphic/morph/internal/environment/process"
)

var ErrPatchConflict = errors.New("patch conflict")

type EnvironmentState string

const (
	EnvironmentAbsent       EnvironmentState = "absent"
	EnvironmentProvisioning EnvironmentState = "provisioning"
	EnvironmentReady        EnvironmentState = "ready"
	EnvironmentRunning      EnvironmentState = "running"
	EnvironmentDraining     EnvironmentState = "draining"
	EnvironmentStopped      EnvironmentState = "stopped"
	EnvironmentUnhealthy    EnvironmentState = "unhealthy"
)

type EnvironmentStatus struct {
	ID                   string
	Backend              Backend
	Scope                Scope
	State                EnvironmentState
	WorkspaceIdentity    string
	WorkspaceMode        WorkspaceMode
	Network              NetworkMode
	ImageDigest          string
	SecurityGeneration   string
	DaemonIncarnation    string
	ContainerIncarnation string
	ParticipantCount     int
	ProcessCount         int
	UpdatedAt            time.Time
	FailureCode          string
}

type EnvironmentDetails struct {
	Status           EnvironmentStatus
	Mounts           []Mount
	SecretReferences []string
	Limits           Limits
	PolicyHash       string
	ImageContract    string
}

type CommandResult struct {
	ExitCode    int
	Stdout      string
	Stderr      string
	TimedOut    bool
	OOMKilled   bool
	Interrupted bool
	Duration    time.Duration
}

type FileInfo struct {
	Path    string
	Size    int64
	Mode    uint32
	IsDir   bool
	Created bool
}

type FileEntry struct {
	Path  string
	Size  int64
	IsDir bool
}

type SearchMatch struct {
	Path   string
	Line   int
	Column int
	Text   string
}

type Service interface {
	Acquire(context.Context, Spec) (EnvironmentStatus, error)
	Status(context.Context, Owner) ([]EnvironmentStatus, error)
	Run(context.Context, Spec) (CommandResult, error)
	StartProcess(context.Context, Spec) (processenv.Info, error)
	GetProcess(context.Context, Spec) (processenv.Info, error)
	ReadProcess(context.Context, Spec, processenv.ReadRequest) (processenv.Output, error)
	StopProcess(context.Context, Spec) (processenv.Info, error)
	ListProcesses(context.Context, Spec) ([]processenv.Info, error)
	ReadFile(context.Context, Spec, int) ([]byte, error)
	WriteFile(context.Context, Spec, bool) (FileInfo, error)
	PatchFile(context.Context, Spec) (FileInfo, error)
	ListFiles(context.Context, Spec, int) ([]FileEntry, error)
	SearchFiles(context.Context, Spec, int) ([]SearchMatch, error)
	CloseOwner(context.Context, Owner) error
	CloseSession(context.Context, string, string, bool) error
	Reconcile(context.Context) error
	Close(context.Context) error
}
