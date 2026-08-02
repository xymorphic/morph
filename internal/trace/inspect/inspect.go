package inspect

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	models "github.com/xymorphic/morph/internal/model"
	storage "github.com/xymorphic/morph/internal/state/core"
	morphtrace "github.com/xymorphic/morph/internal/trace"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/str"
)

var (
	statPath      = os.Stat
	readDirectory = os.ReadDir
	openPath      = func(path string) (io.ReadCloser, error) { return os.Open(path) }
	newScanner    = bufio.NewScanner
)

// Store loads trace inspection data from JSONL trace files.
type Store struct {
	directory string
}

// SessionSummary summarizes session state.
type SessionSummary struct {
	ID          string    `json:"id"`
	Path        string    `json:"path"`
	StartedAt   time.Time `json:"started_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	AgentName   string    `json:"agent_name,omitempty"`
	Model       string    `json:"model,omitempty"`
	API         string    `json:"api,omitempty"`
	EventCount  int       `json:"event_count"`
	FinalStatus string    `json:"final_status"`
	LoadError   string    `json:"load_error,omitempty"`
}

// SessionDetail contains the timeline, memory view, and warnings for one trace session.
type SessionDetail struct {
	Summary   SessionSummary     `json:"summary"`
	Timeline  []TimelineEvent    `json:"timeline"`
	Memories  *SessionMemoryView `json:"memories,omitempty"`
	Warnings  []string           `json:"warnings,omitempty"`
	LoadError string             `json:"load_error,omitempty"`
}

// SessionMemoryView is the trace-inspection view for session memory.
type SessionMemoryView struct {
	Source    string       `json:"source"`
	Items     []MemoryView `json:"items,omitempty"`
	LoadError string       `json:"load_error,omitempty"`
}

// MemoryView is the trace-inspection view for memory.
type MemoryView struct {
	ID          string             `json:"id"`
	Kind        string             `json:"kind"`
	Status      string             `json:"status"`
	Title       string             `json:"title,omitempty"`
	Text        string             `json:"text,omitempty"`
	Tags        []string           `json:"tags,omitempty"`
	Metadata    map[string]string  `json:"metadata,omitempty"`
	SourceLinks []MemorySourceView `json:"source_links,omitempty"`
	Confidence  float64            `json:"confidence"`
	CreatedAt   time.Time          `json:"created_at,omitempty"`
	UpdatedAt   time.Time          `json:"updated_at,omitempty"`
}

// MemorySourceView is the trace-inspection view for memory source.
type MemorySourceView struct {
	SessionID     string `json:"session_id,omitempty"`
	MessageIDs    []uint `json:"message_ids,omitempty"`
	Offsets       []int  `json:"offsets,omitempty"`
	SummaryID     string `json:"summary_id,omitempty"`
	CreatedBy     string `json:"created_by,omitempty"`
	CreatedReason string `json:"created_reason,omitempty"`
}

// SessionMemoryProvider loads memory linked to a trace-inspected session.
type SessionMemoryProvider interface {
	ListSessionMemories(context.Context, string) ([]storage.MemoryItem, error)
}

func memoryItemToMemoryView(item storage.MemoryItem) MemoryView {
	links := make([]MemorySourceView, 0, len(item.SourceLinks))
	for _, link := range item.SourceLinks {
		links = append(links, MemorySourceView{
			SessionID:     link.SessionID,
			MessageIDs:    append([]uint(nil), link.MessageIDs...),
			Offsets:       append([]int(nil), link.Offsets...),
			SummaryID:     link.SummaryID,
			CreatedBy:     link.CreatedBy,
			CreatedReason: link.CreatedReason,
		})
	}

	metadata := make(map[string]string, len(item.Metadata))
	for key, value := range item.Metadata {
		metadata[key] = value
	}

	return MemoryView{
		ID:          item.ID,
		Kind:        string(item.Kind),
		Status:      string(item.Status),
		Title:       item.Title,
		Text:        item.Text,
		Tags:        append([]string(nil), item.Tags...),
		Metadata:    metadata,
		SourceLinks: links,
		Confidence:  item.Confidence,
		CreatedAt:   item.CreatedAt,
		UpdatedAt:   item.UpdatedAt,
	}
}

// TimelineEvent represents a timeline event.
type TimelineEvent struct {
	Index             int                  `json:"index"`
	Type              string               `json:"type"`
	Timestamp         time.Time            `json:"timestamp"`
	Raw               string               `json:"raw"`
	UserMessage       *UserMessageView     `json:"user_message,omitempty"`
	ModelRequest      *ModelRequestView    `json:"model_request,omitempty"`
	ModelResponse     *ModelResponseView   `json:"model_response,omitempty"`
	ToolInvocation    *ToolInvocationView  `json:"tool_invocation,omitempty"`
	FinalResponse     *FinalResponseView   `json:"final_response,omitempty"`
	Failure           *FailureView         `json:"failure,omitempty"`
	SummaryFallback   *SummaryFallbackView `json:"summary_fallback,omitempty"`
	StartedMetadata   *StartedMetadataView `json:"started_metadata,omitempty"`
	ContextEvent      *ContextEventView    `json:"context_event,omitempty"`
	SummaryEvent      *SummaryEventView    `json:"summary_event,omitempty"`
	CompactionEvent   *CompactionEventView `json:"compaction_event,omitempty"`
	WorkspaceRules    *WorkspaceRulesView  `json:"workspace_rules,omitempty"`
	PlanEvent         *PlanEventView       `json:"plan_event,omitempty"`
	SafetyEvent       *SafetyEventView     `json:"safety_event,omitempty"`
	GenericPayloadRaw string               `json:"generic_payload_raw,omitempty"`
}

// StartedMetadataView is the trace-inspection view for started metadata.
type StartedMetadataView struct {
	AgentName string `json:"agent_name,omitempty"`
	Model     string `json:"model,omitempty"`
	API       string `json:"api,omitempty"`
	Source    string `json:"source,omitempty"`
	TraceDir  string `json:"trace_dir,omitempty"`
}

// UserMessageView is the trace-inspection view for user message.
type UserMessageView struct {
	Message string `json:"message"`
}

// ModelRequestView is the trace-inspection view for model request.
type ModelRequestView struct {
	Sequence        int              `json:"sequence"`
	Model           string           `json:"model,omitempty"`
	API             string           `json:"api,omitempty"`
	Instructions    string           `json:"instructions,omitempty"`
	MaxOutputTokens int64            `json:"max_output_tokens"`
	Temperature     float64          `json:"temperature"`
	DebugRequests   bool             `json:"debug_requests"`
	Context         RequestMetrics   `json:"context"`
	Messages        []MessageView    `json:"messages,omitempty"`
	Tools           []ToolDefinition `json:"tools,omitempty"`
}

// RequestMetrics records request metrics for display or diagnostics.
type RequestMetrics struct {
	InstructionChars int `json:"instruction_chars"`
	MessageCount     int `json:"message_count"`
	MessageChars     int `json:"message_chars"`
	ToolCount        int `json:"tool_count"`
	ToolCallCount    int `json:"tool_call_count"`
}

// MessageView is the trace-inspection view for message.
type MessageView struct {
	Role         string         `json:"role,omitempty"`
	Name         string         `json:"name,omitempty"`
	Content      string         `json:"content,omitempty"`
	ContentChars int            `json:"content_chars"`
	CreatedAt    time.Time      `json:"created_at,omitempty"`
	ToolCallID   string         `json:"tool_call_id,omitempty"`
	ToolCalls    []ToolCallView `json:"tool_calls,omitempty"`
}

// ToolDefinition describes a model-visible tool definition.
type ToolDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ModelResponseView is the trace-inspection view for model response.
type ModelResponseView struct {
	Sequence          int            `json:"sequence"`
	ID                string         `json:"id,omitempty"`
	Model             string         `json:"model,omitempty"`
	OutputText        string         `json:"output_text,omitempty"`
	RequiresToolCalls bool           `json:"requires_tool_calls"`
	ToolCalls         []ToolCallView `json:"tool_calls,omitempty"`
}

// ToolCallView is the trace-inspection view for tool call.
type ToolCallView struct {
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Input string `json:"input,omitempty"`
}

// ToolInvocationView is the trace-inspection view for tool invocation.
type ToolInvocationView struct {
	Phase      string `json:"phase"`
	ID         string `json:"id,omitempty"`
	Name       string `json:"name,omitempty"`
	Input      string `json:"input,omitempty"`
	Content    string `json:"content,omitempty"`
	PairIndex  *int   `json:"pair_index,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// FinalResponseView is the trace-inspection view for final response.
type FinalResponseView struct {
	Message string `json:"message"`
}

// FailureView is the trace-inspection view for failure.
type FailureView struct {
	Error string `json:"error"`
}

// SummaryFallbackView is the trace-inspection view for summary fallback.
type SummaryFallbackView struct {
	Payload string `json:"payload,omitempty"`
}

// ContextEventView is the trace-inspection view for context event.
type ContextEventView struct {
	Source             string `json:"source,omitempty"`
	PromptTokens       int    `json:"prompt_tokens,omitempty"`
	AnchorPromptTokens int    `json:"anchor_prompt_tokens,omitempty"`
	AnchorMessageCount int    `json:"anchor_message_count,omitempty"`
	DeltaPromptTokens  int    `json:"delta_prompt_tokens,omitempty"`
	CompletionTokens   int    `json:"completion_tokens,omitempty"`
	TotalTokens        int    `json:"total_tokens,omitempty"`
	ContextLimit       int    `json:"context_limit,omitempty"`
	TriggerThreshold   int    `json:"trigger_threshold,omitempty"`
	WarnThreshold      int    `json:"warn_threshold,omitempty"`
}

// SummaryEventView is the trace-inspection view for summary event.
type SummaryEventView struct {
	SessionID          string    `json:"session_id,omitempty"`
	SourceEndOffset    int       `json:"source_end_offset,omitempty"`
	SourceMessageCount int       `json:"source_message_count,omitempty"`
	UpdatedAt          time.Time `json:"updated_at,omitempty"`
	Error              string    `json:"error,omitempty"`
}

// CompactionEventView is the trace-inspection view for compaction event.
type CompactionEventView struct {
	SessionID          string    `json:"session_id,omitempty"`
	Status             string    `json:"status,omitempty"`
	Auto               bool      `json:"auto,omitempty"`
	TargetMessageCount int       `json:"target_message_count,omitempty"`
	TargetOffset       int       `json:"target_offset,omitempty"`
	RequestedAt        time.Time `json:"requested_at,omitempty"`
	StartedAt          time.Time `json:"started_at,omitempty"`
	CompletedAt        time.Time `json:"completed_at,omitempty"`
	FailedAt           time.Time `json:"failed_at,omitempty"`
	Error              string    `json:"error,omitempty"`
}

// WorkspaceRulesView is the trace-inspection view for workspace rules.
type WorkspaceRulesView struct {
	OriginalLength   int    `json:"original_length,omitempty"`
	TruncatedLength  int    `json:"truncated_length,omitempty"`
	MaxContentLength int    `json:"max_content_length,omitempty"`
	Marker           string `json:"marker,omitempty"`
}

// PlanEventView is the trace-inspection view for plan event.
type PlanEventView struct {
	SessionID    string          `json:"session_id,omitempty"`
	Steps        []PlanStepView  `json:"steps,omitempty"`
	Summary      PlanSummaryView `json:"summary"`
	ActiveStepID string          `json:"active_step_id,omitempty"`
	Explanation  string          `json:"explanation,omitempty"`
	Source       string          `json:"source,omitempty"`
}

// PlanStepView is the trace-inspection view for plan step.
type PlanStepView struct {
	ID      string `json:"id,omitempty"`
	Content string `json:"content,omitempty"`
	Status  string `json:"status,omitempty"`
}

// PlanSummaryView is the trace-inspection view for plan summary.
type PlanSummaryView struct {
	Total      int `json:"total"`
	Pending    int `json:"pending"`
	InProgress int `json:"in_progress"`
	Completed  int `json:"completed"`
	Cancelled  int `json:"cancelled"`
}

// SafetyEventView is the trace-inspection view for safety event.
type SafetyEventView struct {
	SessionID     string              `json:"session_id,omitempty"`
	Source        string              `json:"source,omitempty"`
	Action        string              `json:"action,omitempty"`
	ContentLength int                 `json:"content_length"`
	Blocked       bool                `json:"blocked"`
	Redacted      bool                `json:"redacted"`
	Refusal       string              `json:"refusal,omitempty"`
	Findings      []map[string]string `json:"findings,omitempty"`
}

type rawEvent struct {
	SessionID string          `json:"session_id"`
	Type      string          `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// NewStore returns a store backed by the supplied dependencies.
func NewStore(directory string) *Store {
	directoryValue := str.String(directory)
	return &Store{directory: directoryValue.Trim()}
}

func (s *Store) Validate() error {
	if s == nil {
		return errors.New("trace directory is required")
	}

	directory := str.String(s.directory)
	if directory.Trim() == "" {
		return errors.New("trace directory is required")
	}

	info, err := statPath(s.directory)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("trace directory %q does not exist", s.directory)
		}

		return fmt.Errorf("failed to access trace directory %q: %w", s.directory, err)
	}

	if !info.IsDir() {
		return fmt.Errorf("trace directory %q is not a directory", s.directory)
	}

	return nil
}

func (s *Store) ListSessions() ([]SessionSummary, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}

	entries, err := readDirectory(s.directory)
	if err != nil {
		return nil, fmt.Errorf("failed to read trace directory %q: %w", s.directory, err)
	}

	summaries := make([]SessionSummary, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}

		path := filepath.Join(s.directory, entry.Name())
		detail, err := LoadSessionFile(path)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, detail.Summary)
	}

	slices.SortFunc(summaries, func(a, b SessionSummary) int {
		if !a.UpdatedAt.Equal(b.UpdatedAt) {
			if a.UpdatedAt.After(b.UpdatedAt) {
				return -1
			}

			return 1
		}

		return strings.Compare(b.ID, a.ID)
	})

	return summaries, nil
}

func (s *Store) GetSession(id string) (SessionDetail, error) {
	if err := s.Validate(); err != nil {
		return SessionDetail{}, err
	}

	path, err := getSessionPath(s.directory, id)
	if err != nil {
		return SessionDetail{}, os.ErrNotExist
	}

	if _, err := statPath(path); err != nil {
		if os.IsNotExist(err) {
			return SessionDetail{}, os.ErrNotExist
		}

		return SessionDetail{}, err
	}

	return LoadSessionFile(path)
}

func getSessionPath(directory, id string) (string, error) {
	directoryValue2 := str.String(directory)
	directory = directoryValue2.Trim()
	idValue := str.String(id)
	id = idValue.Trim()
	if directory == "" || id == "" {
		return "", os.ErrNotExist
	}

	return morphtrace.ResolveTraceFilePath(directory, id)
}

// LoadSessionFile loads session file.
func LoadSessionFile(path string) (SessionDetail, error) {
	pathValue := str.String(path)
	path = pathValue.Trim()
	if path == "" {
		return SessionDetail{}, errors.New("trace session path is required")
	}

	fileStem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	logicalID := morphtrace.SessionIDFromTraceFilename(fileStem)
	detail := SessionDetail{
		Summary: SessionSummary{
			ID:          logicalID,
			Path:        path,
			FinalStatus: "incomplete",
		},
	}

	info, err := statPath(path)
	if err != nil {
		return SessionDetail{}, err
	}
	detail.Summary.StartedAt = info.ModTime().UTC()

	file, err := openPath(path)
	if err != nil {
		return SessionDetail{}, err
	}
	defer func() { _ = file.Close() }()

	scanner := newScanner(file)
	buffer := make([]byte, 0, 64*1024)
	scanner.Buffer(buffer, 8*1024*1024)

	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var event rawEvent
		if err := json.Unmarshal(line, &event); err != nil {
			detail.LoadError = fmt.Sprintf("failed to parse line %d: %v", lineNo, err)
			detail.Summary.LoadError = detail.LoadError
			detail.Summary.FinalStatus = "load_error"
			if detail.Summary.UpdatedAt.IsZero() {
				detail.Summary.UpdatedAt = info.ModTime().UTC()
			}

			return detail, nil
		}

		detail.Summary.EventCount++
		if !event.Timestamp.IsZero() && (detail.Summary.StartedAt.IsZero() ||
			event.Timestamp.Before(detail.Summary.StartedAt)) {
			detail.Summary.StartedAt = event.Timestamp
		}
		if !event.Timestamp.IsZero() && (detail.Summary.UpdatedAt.IsZero() ||
			event.Timestamp.After(detail.Summary.UpdatedAt)) {
			detail.Summary.UpdatedAt = event.Timestamp
		}
		if event.SessionID != "" && event.SessionID != logicalID {
			detail.Warnings = append(detail.Warnings, fmt.Sprintf("event session id %q does not match file id %q",
				event.SessionID, logicalID))
		}

		timelineEvent := TimelineEvent{
			Index:     len(detail.Timeline),
			Type:      event.Type,
			Timestamp: event.Timestamp,
			Raw:       compactJSON(line),
		}
		applyEvent(&detail, &timelineEvent, event)
		detail.Timeline = append(detail.Timeline, timelineEvent)
	}

	if err := scanner.Err(); err != nil {
		return SessionDetail{}, err
	}

	if detail.Summary.UpdatedAt.IsZero() {
		detail.Summary.UpdatedAt = info.ModTime().UTC()
	}

	pairToolInvocations(detail.Timeline)
	numberInteractions(detail.Timeline)
	return detail, nil
}

func applyEvent(detail *SessionDetail, timelineEvent *TimelineEvent, event rawEvent) {
	typedPayload, payloadOK := morphtrace.DecodePayloadJSON(event.Type, event.Payload)

	switch event.Type {
	case morphtrace.EvtChatStarted:
		if payload, ok := typedPayload.(morphtrace.Metadata); payloadOK && ok {
			detail.Summary.AgentName = payload.AgentName
			detail.Summary.Model = payload.Model
			detail.Summary.API = payload.API
			timelineEvent.StartedMetadata = &StartedMetadataView{
				AgentName: payload.AgentName,
				Model:     payload.Model,
				API:       payload.API,
				Source:    payload.Source,
				TraceDir:  payload.TraceDir,
			}

			return
		}
	case morphtrace.EvtUserMessageAccepted:
		if payload, ok := typedPayload.(morphtrace.UserMessageAcceptedPayload); payloadOK && ok {
			timelineEvent.UserMessage = &UserMessageView{Message: firstNonEmpty(payload.Message, payload.Text)}
			detail.Summary.FinalStatus = "in_progress"
			return
		}
	case morphtrace.EvtModelRequest:
		if payload, ok := typedPayload.(models.Request); payloadOK && ok {
			timelineEvent.ModelRequest = buildRequestView(payload)
			if detail.Summary.Model == "" {
				detail.Summary.Model = payload.Model
			}
			if detail.Summary.API == "" {
				detail.Summary.API = payload.API
			}

			return
		}
	case morphtrace.EvtModelResponse:
		if payload, ok := typedPayload.(models.Response); payloadOK && ok {
			timelineEvent.ModelResponse = buildSessionMessagesResponseView(payload)
			return
		}
	case morphtrace.EvtToolInvocationStarted:
		if payload, ok := typedPayload.(morphtrace.ToolInvocationStartedPayload); payloadOK && ok {
			timelineEvent.ToolInvocation = &ToolInvocationView{
				Phase: "started",
				ID:    payload.ID,
				Name:  payload.Name,
				Input: payload.Input,
			}

			return
		}
	case morphtrace.EvtToolInvocationCompleted:
		if payload, ok := typedPayload.(morphtrace.ToolInvocationCompletedPayload); payloadOK && ok {
			timelineEvent.ToolInvocation = &ToolInvocationView{
				Phase:      "completed",
				Name:       payload.Name,
				Content:    payload.Content,
				ToolCallID: payload.ToolCallID,
				ID:         payload.ToolCallID,
			}

			return
		}
	case morphtrace.EvtFinalAssistantResponse:
		if payload, ok := typedPayload.(morphtrace.FinalAssistantResponsePayload); payloadOK && ok {
			timelineEvent.FinalResponse = &FinalResponseView{Message: firstNonEmpty(payload.Message, payload.Text)}
			detail.Summary.FinalStatus = "completed"
			return
		}
	case morphtrace.EvtSessionFailed:
		if payload, ok := typedPayload.(morphtrace.SessionFailedPayload); payloadOK && ok {
			timelineEvent.Failure = &FailureView{Error: firstNonEmpty(payload.Error, payload.Message)}
			detail.Summary.FinalStatus = "failed"
			return
		}
	case morphtrace.EvtSummaryFallbackStarted:
		timelineEvent.SummaryFallback = &SummaryFallbackView{Payload: compactJSON(event.Payload)}
		return
	case morphtrace.EvtContextPreflight, morphtrace.EvtContextCompactionTriggered,
		morphtrace.EvtContextCompactionWarning, morphtrace.EvtContextPostflightUsage:
		if payload, ok := typedPayload.(morphtrace.ContextEventPayload); payloadOK && ok {
			timelineEvent.ContextEvent = &ContextEventView{
				Source:             payload.Source,
				PromptTokens:       payload.PromptTokens,
				AnchorPromptTokens: payload.AnchorPromptTokens,
				AnchorMessageCount: payload.AnchorMessageCount,
				DeltaPromptTokens:  payload.DeltaPromptTokens,
				CompletionTokens:   payload.CompletionTokens,
				TotalTokens:        payload.TotalTokens,
				ContextLimit:       payload.ContextLimit,
				TriggerThreshold:   payload.TriggerThreshold,
				WarnThreshold:      payload.WarnThreshold,
			}

			return
		}
	case morphtrace.EvtSummaryRequested, morphtrace.EvtSummarySaved, morphtrace.EvtSummaryFailed,
		morphtrace.EvtSummaryParseFailed, morphtrace.EvtSummaryApplied,
		morphtrace.EvtRecallSummaryRequested, morphtrace.EvtRecallSummarySaved, morphtrace.EvtRecallSummaryFailed:
		if payload, ok := typedPayload.(morphtrace.SummaryEventPayload); payloadOK && ok {
			errorValue := str.String(payload.Error)
			timelineEvent.SummaryEvent = &SummaryEventView{
				SessionID:          payload.SessionID,
				SourceEndOffset:    payload.SourceEndOffset,
				SourceMessageCount: payload.SourceMessageCount,
				UpdatedAt:          payload.UpdatedAt,
				Error:              errorValue.Trim(),
			}

			return
		}
	case morphtrace.EvtContextCompactionPending, morphtrace.EvtContextCompactionRunning,
		morphtrace.EvtContextCompactionSucceeded, morphtrace.EvtContextCompactionFailed:
		if payload, ok := typedPayload.(morphtrace.CompactionEventPayload); payloadOK && ok {
			errorValue2 := str.String(payload.Error)
			timelineEvent.CompactionEvent = &CompactionEventView{
				SessionID:          payload.SessionID,
				Status:             payload.Status,
				Auto:               payload.Auto,
				TargetMessageCount: payload.TargetMessageCount,
				TargetOffset:       payload.TargetOffset,
				RequestedAt:        payload.RequestedAt,
				StartedAt:          payload.StartedAt,
				CompletedAt:        payload.CompletedAt,
				FailedAt:           payload.FailedAt,
				Error:              errorValue2.Trim(),
			}

			return
		}
	case morphtrace.EvtWorkspaceRulesTruncated:
		if payload, ok := typedPayload.(morphtrace.WorkspaceRulesTruncatedPayload); payloadOK && ok {
			timelineEvent.WorkspaceRules = &WorkspaceRulesView{
				OriginalLength:   payload.OriginalLength,
				TruncatedLength:  payload.TruncatedLength,
				MaxContentLength: payload.MaxContentLength,
				Marker:           payload.Marker,
			}
			return
		}
	case morphtrace.EvtPlanUpdated, morphtrace.EvtPlanCleared, morphtrace.EvtPlanHydrated:
		if payload, ok := typedPayload.(morphtrace.PlanEventPayload); payloadOK && ok {
			explanationValue := str.String(payload.Explanation)
			sourceValue := str.String(payload.Source)
			timelineEvent.PlanEvent = &PlanEventView{
				SessionID:    payload.SessionID,
				Steps:        planStepViewsFromPayload(payload.Steps),
				Summary:      planSummaryViewFromPayload(payload.Summary),
				ActiveStepID: payload.ActiveStepID,
				Explanation:  explanationValue.Trim(),
				Source:       sourceValue.Trim(),
			}
			return
		}
	case morphtrace.EvtInputSafetyBlocked, morphtrace.EvtOutputSafetyApplied,
		morphtrace.EvtToolOutputSafetyApplied, morphtrace.EvtLoadedContentSafetyBlocked,
		morphtrace.EvtMemorySafetyBlocked:
		if payload, ok := typedPayload.(morphtrace.SafetyEventPayload); payloadOK && ok {
			sessionIDValue := str.String(payload.SessionID)
			sourceValue2 := str.String(payload.Source)
			actionValue := str.String(payload.Action)
			refusalValue := str.String(payload.Refusal)
			timelineEvent.SafetyEvent = &SafetyEventView{
				SessionID:     sessionIDValue.Trim(),
				Source:        sourceValue2.Trim(),
				Action:        actionValue.Trim(),
				ContentLength: payload.ContentLength,
				Blocked:       payload.Blocked,
				Redacted:      payload.Redacted,
				Refusal:       refusalValue.Trim(),
				Findings:      payload.Findings,
			}
			return
		}
	}

	timelineEvent.GenericPayloadRaw = compactJSON(event.Payload)
}

func buildRequestView(payload models.Request) *ModelRequestView {
	messages := make([]MessageView, 0, len(payload.Messages))
	metrics := RequestMetrics{
		InstructionChars: len(payload.Instructions),
		MessageCount:     len(payload.Messages),
		ToolCount:        len(payload.Tools),
	}

	for _, message := range payload.Messages {
		messageView := MessageView{
			Role:         string(message.Role),
			Name:         message.Name,
			Content:      message.Content,
			ContentChars: len(message.Content),
			CreatedAt:    message.CreatedAt,
			ToolCallID:   message.ToolCallID,
			ToolCalls:    toolCallsToToolCallViews(message.ToolCalls),
		}
		metrics.MessageChars += len(message.Content)
		metrics.ToolCallCount += len(message.ToolCalls)
		messages = append(messages, messageView)
	}

	tools := make([]ToolDefinition, 0, len(payload.Tools))
	for _, tool := range payload.Tools {
		tools = append(tools, ToolDefinition{
			Name:        tool.Name,
			Description: tool.Description,
		})
	}

	return &ModelRequestView{
		Model:           payload.Model,
		API:             payload.API,
		Instructions:    payload.Instructions,
		MaxOutputTokens: payload.MaxOutputTokens,
		Temperature:     payload.Temperature,
		DebugRequests:   payload.DebugRequests,
		Context:         metrics,
		Messages:        messages,
		Tools:           tools,
	}
}

func buildSessionMessagesResponseView(payload models.Response) *ModelResponseView {
	return &ModelResponseView{
		ID:                payload.ID,
		Model:             payload.Model,
		OutputText:        payload.OutputText,
		RequiresToolCalls: payload.RequiresToolCalls,
		ToolCalls:         buildToolCallViews(payload.ToolCalls),
	}
}

func planStepViewsFromPayload(steps []morphtrace.PlanStepPayload) []PlanStepView {
	if len(steps) == 0 {
		return nil
	}

	views := make([]PlanStepView, 0, len(steps))
	for _, step := range steps {
		views = append(views, PlanStepView{
			ID:      step.ID,
			Content: step.Content,
			Status:  step.Status,
		})
	}

	return views
}

func planSummaryViewFromPayload(summary morphtrace.PlanSummaryPayload) PlanSummaryView {
	return PlanSummaryView{
		Total:      summary.Total,
		Pending:    summary.Pending,
		InProgress: summary.InProgress,
		Completed:  summary.Completed,
		Cancelled:  summary.Cancelled,
	}
}

func buildToolCallViews(toolCalls []models.ToolCall) []ToolCallView {
	if len(toolCalls) == 0 {
		return nil
	}

	views := make([]ToolCallView, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		views = append(views, ToolCallView{
			ID:    toolCall.ID,
			Name:  toolCall.Name,
			Input: toolCall.Input,
		})
	}

	return views
}

func toolCallsToToolCallViews(toolCalls []morphmsg.ToolCall) []ToolCallView {
	if len(toolCalls) == 0 {
		return nil
	}

	views := make([]ToolCallView, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		views = append(views, ToolCallView{
			ID:    toolCall.ID,
			Name:  toolCall.Name,
			Input: toolCall.Input,
		})
	}

	return views
}

func pairToolInvocations(timeline []TimelineEvent) {
	starts := map[string]int{}
	for index := range timeline {
		toolInvocation := timeline[index].ToolInvocation
		if toolInvocation == nil {
			continue
		}
		toolInvocationID := str.String(toolInvocation.ID)
		id := toolInvocationID.Trim()
		if id == "" {
			continue
		}
		switch toolInvocation.Phase {
		case "started":
			starts[id] = index
		case "completed":
			startIndex, ok := starts[id]
			if !ok {
				continue
			}
			timeline[index].ToolInvocation.PairIndex = new(startIndex)
			timeline[startIndex].ToolInvocation.PairIndex = new(index)
			delete(starts, id)
		}
	}
}

func numberInteractions(timeline []TimelineEvent) {
	requests := 0
	responses := 0
	for index := range timeline {
		if timeline[index].ModelRequest != nil {
			requests++
			timeline[index].ModelRequest.Sequence = requests
		}
		if timeline[index].ModelResponse != nil {
			responses++
			timeline[index].ModelResponse.Sequence = responses
		}
	}
}

func compactJSON(value []byte) string {
	if len(bytes.TrimSpace(value)) == 0 {
		return ""
	}

	var out bytes.Buffer
	if err := json.Compact(&out, value); err != nil {
		value := str.String(string(value))
		return value.Trim()
	}

	return out.String()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value2 := str.String(value)
		value = value2.Trim()
		if value != "" {
			return value
		}
	}

	return ""
}
