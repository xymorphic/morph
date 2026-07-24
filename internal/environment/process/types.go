package process

import (
	"context"
	"time"

	commandplan "github.com/wandxy/morph/internal/command"
)

// Manager starts, reads, lists, and stops managed processes.
type Manager interface {
	Start(context.Context, string, StartRequest) (Info, error)
	Get(string, string) (Info, error)
	Read(string, ReadRequest) (Output, error)
	Stop(context.Context, string, string) (Info, error)
	List(string) []Info
}

const (
	StatusRunning = "running"
	StatusExited  = "exited"
	StatusFailed  = "failed"
	StatusStopped = "stopped"
)

// StartRequest describes a start request.
type StartRequest struct {
	Plan              commandplan.Plan
	Label             string
	OutputBufferBytes int
}

// ReadRequest describes a read request.
type ReadRequest struct {
	ProcessID    string
	StdoutCursor *int
	StderrCursor *int
}

// Info describes info returned to callers.
type Info struct {
	ID              string     `json:"id"`
	Label           string     `json:"label,omitempty"`
	Command         string     `json:"command"`
	Args            []string   `json:"args,omitempty"`
	CWD             string     `json:"cwd,omitempty"`
	Status          string     `json:"status"`
	ExitCode        *int       `json:"exit_code,omitempty"`
	StartedAt       time.Time  `json:"started_at"`
	EndedAt         *time.Time `json:"ended_at,omitempty"`
	StdoutBytes     int        `json:"stdout_bytes"`
	StderrBytes     int        `json:"stderr_bytes"`
	StdoutTruncated bool       `json:"stdout_truncated,omitempty"`
	StderrTruncated bool       `json:"stderr_truncated,omitempty"`
}

// Output contains output from output.
type Output struct {
	Stdout              string `json:"stdout"`
	Stderr              string `json:"stderr"`
	StdoutBytes         int    `json:"stdout_bytes"`
	StderrBytes         int    `json:"stderr_bytes"`
	NextStdoutCursor    int    `json:"next_stdout_cursor"`
	NextStderrCursor    int    `json:"next_stderr_cursor"`
	StdoutTruncated     bool   `json:"stdout_truncated,omitempty"`
	StderrTruncated     bool   `json:"stderr_truncated,omitempty"`
	StdoutCursorExpired bool   `json:"stdout_cursor_expired,omitempty"`
	StderrCursorExpired bool   `json:"stderr_cursor_expired,omitempty"`
}
