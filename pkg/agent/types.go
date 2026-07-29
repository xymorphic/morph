package agent

import "time"

const (
	EventKindTextDelta = "text_delta"
	EventKindTrace     = "trace_event"
)

type Event struct {
	Kind       string
	Channel    string
	Text       string
	TraceEvent any
}

type CompactSessionResult struct {
	SessionID            string
	SourceEndOffset      int
	SourceMessageCount   int
	UpdatedAt            time.Time
	CurrentContextLength int
	TotalContextLength   int
}

type ContextStatus struct {
	SessionID        string
	Offset           int
	Size             int
	Length           int
	Used             int
	Remaining        int
	UsedPct          float64
	RemainingPct     float64
	UsageSource      string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	CompactionStatus string
}
