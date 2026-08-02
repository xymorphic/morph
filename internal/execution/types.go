package execution

import "time"

type Backend string

const (
	BackendLocal  Backend = "local"
	BackendDocker Backend = "docker"
)

type Scope string

const (
	ScopeSession Scope = "session"
	ScopeShared  Scope = "shared"
)

type WorkspaceMode string

const (
	WorkspaceNone      WorkspaceMode = "none"
	WorkspaceReadOnly  WorkspaceMode = "ro"
	WorkspaceReadWrite WorkspaceMode = "rw"
)

type MountMode string

const (
	MountReadOnly  MountMode = "ro"
	MountReadWrite MountMode = "rw"
)

type NetworkMode string

const (
	NetworkNone   NetworkMode = "none"
	NetworkBridge NetworkMode = "bridge"
)

type OperationKind string

const (
	OperationCommand    OperationKind = "command"
	OperationProcess    OperationKind = "process"
	OperationFilesystem OperationKind = "filesystem"
)

type ProcessAction string

const (
	ProcessStart  ProcessAction = "start"
	ProcessStatus ProcessAction = "status"
	ProcessRead   ProcessAction = "read"
	ProcessStop   ProcessAction = "stop"
	ProcessList   ProcessAction = "list"
)

type FilesystemAction string

const (
	FilesystemRead   FilesystemAction = "read"
	FilesystemWrite  FilesystemAction = "write"
	FilesystemPatch  FilesystemAction = "patch"
	FilesystemList   FilesystemAction = "list"
	FilesystemSearch FilesystemAction = "search"
)

type Limits struct {
	MemoryBytes       int64         `json:"memory_bytes"`
	CPUMilli          int64         `json:"cpu_milli"`
	PIDs              int64         `json:"pids"`
	OpenFiles         int64         `json:"open_files"`
	TemporaryBytes    int64         `json:"temporary_bytes"`
	OutputBytes       int64         `json:"output_bytes"`
	ControlInputBytes int64         `json:"control_input_bytes"`
	Runtime           time.Duration `json:"runtime"`
	StopGrace         time.Duration `json:"stop_grace"`
}

type Mount struct {
	Name           string    `json:"name"`
	SourceIdentity string    `json:"source_identity"`
	Target         string    `json:"target"`
	Mode           MountMode `json:"mode"`
	Create         bool      `json:"create,omitempty"`
	Purpose        string    `json:"purpose,omitempty"`
}

type ExposureInput struct {
	Backend                 Backend
	Scope                   Scope
	WorkspaceIdentity       string
	WorkspaceMode           WorkspaceMode
	Mounts                  []Mount
	Network                 NetworkMode
	SecretReferences        []string
	ImageDigest             string
	ImageContractDigest     string
	PolicyHash              string
	SecurityGeneration      string
	Limits                  Limits
	EnvironmentIdleExpiry   time.Duration
	SharedDisabledRetention time.Duration
}
