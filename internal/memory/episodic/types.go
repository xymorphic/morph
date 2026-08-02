package episodic

import (
	"context"

	models "github.com/xymorphic/morph/internal/model"
	storage "github.com/xymorphic/morph/internal/state/core"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
)

// Request configures episodic extraction for a session or bounded message range.
type Request struct {
	// SessionID identifies the session to extract from. Empty means the current session.
	SessionID string
	// OffsetStart optionally sets the inclusive message offset where extraction starts.
	OffsetStart *int
	// OffsetEnd optionally sets the exclusive message offset where extraction stops.
	OffsetEnd *int
	// WindowSize sets how many messages are loaded per extraction window.
	WindowSize int
	// MaxWindows limits how many windows are processed in one request. Zero means no explicit request limit.
	MaxWindows int
	// MaxWindowChars bounds the character budget for each proposed memory item body.
	MaxWindowChars int
	// MaxWindowTokens bounds the estimated token budget for each proposed memory item body.
	MaxWindowTokens int
	// Trigger describes what initiated extraction, such as command or background.
	Trigger string
	// Trace records extraction events when provided.
	Trace TraceRecorder
}

// Result summarizes all windows processed by an extraction request.
type Result struct {
	// SessionID identifies the session that was processed.
	SessionID string `json:"session_id"`
	// Windows contains per-window extraction results.
	Windows []WindowResult `json:"windows,omitempty"`
	// MessageCount is the total number of loaded messages.
	MessageCount int `json:"message_count"`
	// CandidateCount is the number of proposed episodic memory items produced before persistence.
	CandidateCount int `json:"candidate_count"`
	// WriteCount is the number of memory items written.
	WriteCount int `json:"write_count"`
	// SkipCount is the number of duplicate or already processed memory items skipped.
	SkipCount int `json:"skip_count"`
}

// WindowResult summarizes extraction work for one bounded range of source messages.
type WindowResult struct {
	// MemoryIDs contains IDs of memory items written for this window.
	MemoryIDs []string `json:"memory_ids,omitempty"`
	// SkippedIDs contains IDs of existing memory items that caused this window to be skipped.
	SkippedIDs []string `json:"skipped_ids,omitempty"`
	// OffsetStart is the inclusive source message offset for this window.
	OffsetStart int `json:"offset_start"`
	// OffsetEnd is the exclusive source message offset for this window.
	OffsetEnd int `json:"offset_end"`
	// MessageCount is the number of messages loaded for this window.
	MessageCount int `json:"message_count"`
	// CandidateCount is the number of proposed episodic memory items produced for this window.
	CandidateCount int `json:"candidate_count"`
	// WriteCount is the number of proposed items written as memory records for this window.
	WriteCount int `json:"write_count"`
	// SkipCount is the number of existing memory records skipped for this window.
	SkipCount int `json:"skip_count"`
}

// EpisodeRecord wraps an episodic memory item for persistence.
type EpisodeRecord struct {
	// Item is the memory item to record as episodic memory.
	Item storage.MemoryItem
}

// StateManager is the session/message state dependency required by episodic extraction.
type StateManager interface {
	CurrentSession(context.Context) (string, error)
	CountMessages(context.Context, string, storage.MessageQueryOptions) (int, error)
	GetMessages(context.Context, string, storage.MessageQueryOptions) ([]morphmsg.Message, error)
	ListTraceEvents(context.Context, storage.TraceQuery) (storage.TraceResult, error)
	UpdateCheckpoints(context.Context, string, storage.CheckpointPatch) error
}

// MemoryRepository supports existing-memory lookup and episode recording for extraction.
type MemoryRepository interface {
	Search(context.Context, storage.MemorySearchQuery) (storage.MemorySearchResult, error)
	RecordEpisode(context.Context, EpisodeRecord) (storage.MemoryItem, error)
}

// TraceRecorder records extraction trace events when provided by the caller.
type TraceRecorder interface {
	Record(string, any)
}

// normalizedRequest is the validated internal form used while processing windows.
type normalizedRequest struct {
	// SessionID identifies the resolved source session.
	SessionID string
	// OffsetStart is the inclusive normalized source offset.
	OffsetStart int
	// OffsetEnd is the exclusive normalized source offset.
	OffsetEnd int
	// WindowSize is the bounded message count per window.
	WindowSize int
	// MaxWindows limits processed windows for this request.
	MaxWindows int
	// MaxWindowChars bounds proposed memory text by characters.
	MaxWindowChars int
	// MaxWindowTokens bounds proposed memory text by estimated tokens.
	MaxWindowTokens int
	// Trigger identifies the extraction initiator.
	Trigger string
	// Trace records extraction events when provided.
	Trace TraceRecorder
}

// sourceWindow identifies the inclusive/exclusive message offsets for one window.
type sourceWindow struct {
	// Start is the inclusive message offset.
	Start int
	// End is the exclusive message offset.
	End int
}

// episodeCandidate is a proposed episodic memory item returned by the episode extractor.
type episodeCandidate struct {
	// Kind classifies the proposed memory item, such as decision, outcome, or task_trace.
	Kind string
	// Title is a short summary for the proposed memory item.
	Title string
	// Text is the concise, outcome-oriented episode body.
	Text string
	// Confidence is the extractor's confidence in the proposed memory item.
	Confidence float64
	// Metadata describes metadata attached to structured details for the proposed memory item records.
	Metadata map[string]string
	// SourceLinks optionally overrides the source links attached to the memory item.
	SourceLinks []storage.MemorySourceLink
}

// candidateRejection explains why a window or proposed memory item was rejected.
type candidateRejection struct {
	// Kind identifies the rejected proposed item or window category.
	Kind string `json:"kind"`
	// Reason explains why the proposed item was rejected.
	Reason string `json:"reason"`
}

// messageEvidence contains normalized source message evidence sent to the extractor.
type messageEvidence struct {
	// MessageIDs contains source message database IDs.
	MessageIDs []uint
	// Offsets contains source message offsets within the session.
	Offsets []int
	// Lines contains normalized role-prefixed evidence lines.
	Lines []string
	// Text contains all evidence lines joined by newlines.
	Text string
	// LowerText contains Text lowercased for local scoring helpers.
	LowerText string
	// TraceRefs identifies trace events used as task evidence.
	TraceRefs []string
}

// candidateExtractor proposes curated episodic memory items from message evidence.
type candidateExtractor interface {
	ExtractCandidates(context.Context, CandidateRequest) (CandidateResult, error)
}

// CandidateRequest is the bounded LLM input used to propose episodic memory items.
type CandidateRequest struct {
	// SessionID identifies the source session.
	SessionID string `json:"session_id"`
	// Start is the inclusive source message offset.
	Start int `json:"start"`
	// End is the exclusive source message offset.
	End int `json:"end"`
	// Messages contains normalized role-prefixed evidence lines.
	Messages []string `json:"messages"`
	// TraceEvents contains bounded task trace evidence from the same source session.
	TraceEvents []taskTraceEvidence `json:"trace_events,omitempty"`
	// MaxChars is the maximum desired proposed memory text size.
	MaxChars int `json:"max_chars"`
}

// taskTraceEvidence is a compact trace event representation sent to the extractor.
type taskTraceEvidence struct {
	// Ref is the stable source reference for this trace event within the session.
	Ref string `json:"ref"`
	// Type is the trace event type.
	Type string `json:"type"`
	// Timestamp is the UTC event timestamp.
	Timestamp string `json:"timestamp,omitempty"`
	// Payload contains a bounded JSON rendering of the sanitized trace payload.
	Payload string `json:"payload,omitempty"`
}

// CandidateResult is the structured LLM output for one extraction window.
type CandidateResult struct {
	// Candidates contains proposed episodic memory items that may be persisted.
	Candidates []episodeCandidate `json:"candidates"`
	// Rejections explains low-signal or invalid proposed memory items.
	Rejections []candidateRejection `json:"rejections"`
}

// LLMExtractorOptions configures the LLM-backed episodic memory proposal extractor.
type LLMExtractorOptions struct {
	// Client is the model client used to request structured memory proposals.
	Client models.Client
	// Model is the model name used for extraction.
	Model string
	// API selects the model API.
	API string
	// MaxOutputTokens bounds the structured extraction response.
	MaxOutputTokens int64
	// MaxOutputTokensEnabled controls whether MaxOutputTokens is sent to the provider.
	MaxOutputTokensEnabled *bool
	// DebugRequests enables model request debugging when supported.
	DebugRequests bool
}

// LLMExtractor proposes curated episodic memory items using a model client.
type LLMExtractor struct {
	// options contains the model client and request settings.
	options LLMExtractorOptions
}
