package trace

import (
	"encoding/json"
	"strings"
	"time"

	models "github.com/wandxy/morph/internal/model"
	morphmsg "github.com/wandxy/morph/pkg/agent/message"
	"github.com/wandxy/morph/pkg/str"
)

// SessionFailedPayload is the trace payload for session failed.
type SessionFailedPayload struct {
	Error   string `json:"error,omitempty"`
	Message string `json:"message,omitempty"`
}

type SessionQueueEventPayload struct {
	QueueEntryID string `json:"queue_entry_id,omitempty"`
	RunID        string `json:"run_id,omitempty"`
	DeliveryMode string `json:"delivery_mode,omitempty"`
	Status       string `json:"status,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

// SafetyEventPayload is the trace payload for safety event.
type SafetyEventPayload struct {
	SessionID     string              `json:"session_id,omitempty"`
	Source        string              `json:"source,omitempty"`
	Action        string              `json:"action,omitempty"`
	ContentLength int                 `json:"content_length,omitempty"`
	Blocked       bool                `json:"blocked,omitempty"`
	Redacted      bool                `json:"redacted,omitempty"`
	Refusal       string              `json:"refusal,omitempty"`
	Findings      []map[string]string `json:"findings,omitempty"`
}

type PermissionDecisionPayload struct {
	ActorKind     string                          `json:"actor_kind,omitempty"`
	SurfaceKind   string                          `json:"surface_kind,omitempty"`
	Surface       string                          `json:"surface,omitempty"`
	Tool          string                          `json:"tool,omitempty"`
	Resource      string                          `json:"resource,omitempty"`
	Action        string                          `json:"action,omitempty"`
	Effects       []string                        `json:"effects,omitempty"`
	Decision      string                          `json:"decision,omitempty"`
	ReasonCode    string                          `json:"reason_code,omitempty"`
	Rule          string                          `json:"rule,omitempty"`
	Preset        string                          `json:"preset,omitempty"`
	OwnerRequired bool                            `json:"owner_required,omitempty"`
	Network       *PermissionNetworkTargetPayload `json:"network,omitempty"`
	Command       *PermissionCommandTargetPayload `json:"command,omitempty"`
}

type PermissionNetworkTargetPayload struct {
	Scheme       string `json:"scheme,omitempty"`
	Host         string `json:"host,omitempty"`
	Port         uint16 `json:"port,omitempty"`
	Path         string `json:"path,omitempty"`
	Method       string `json:"method,omitempty"`
	RequestClass string `json:"request_class,omitempty"`
	HasQuery     bool   `json:"has_query,omitempty"`
}

type PermissionCommandTargetPayload struct {
	Mode            string   `json:"mode,omitempty"`
	Executable      string   `json:"executable,omitempty"`
	InvocationCount int      `json:"invocation_count,omitempty"`
	RedirectCount   int      `json:"redirect_count,omitempty"`
	Complete        bool     `json:"complete"`
	Indirect        bool     `json:"indirect,omitempty"`
	DynamicReasons  []string `json:"dynamic_reasons,omitempty"`
}

type PermissionApprovalPayload struct {
	RequestID  string    `json:"request_id,omitempty"`
	Status     string    `json:"status,omitempty"`
	Scope      string    `json:"scope,omitempty"`
	Tool       string    `json:"tool,omitempty"`
	Resource   string    `json:"resource,omitempty"`
	Action     string    `json:"action,omitempty"`
	Effects    []string  `json:"effects,omitempty"`
	Summary    string    `json:"operation_summary,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	Operations []string  `json:"operations,omitempty"`
	ExpiresAt  time.Time `json:"expires_at,omitempty"`
}

// UserMessageAcceptedPayload is the trace payload for user message accepted.
type UserMessageAcceptedPayload struct {
	Message string `json:"message,omitempty"`
	Text    string `json:"text,omitempty"`
}

// ModelReasoningCompletedPayload is the trace payload for model reasoning completed.
type ModelReasoningCompletedPayload struct {
	DurationMS int64  `json:"duration_ms,omitempty"`
	Summary    string `json:"summary,omitempty"`
}

// FinalAssistantResponsePayload is the trace payload for final assistant response.
type FinalAssistantResponsePayload struct {
	Message string `json:"message,omitempty"`
	Text    string `json:"text,omitempty"`
}

// SummaryFallbackStartedPayload is the trace payload for summary fallback started.
type SummaryFallbackStartedPayload struct {
	RemainingIterations int `json:"remaining_iterations,omitempty"`
}

// ContextEventPayload is the trace payload for context event.
type ContextEventPayload struct {
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

// SummaryEventPayload is the trace payload for summary event.
type SummaryEventPayload struct {
	SessionID          string    `json:"session_id,omitempty"`
	SourceEndOffset    int       `json:"source_end_offset,omitempty"`
	SourceMessageCount int       `json:"source_message_count,omitempty"`
	UpdatedAt          time.Time `json:"updated_at,omitempty"`
	Error              string    `json:"error,omitempty"`
}

// CompactionEventPayload is the trace payload for compaction event.
type CompactionEventPayload struct {
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

// WorkspaceRulesTruncatedPayload is the trace payload for workspace rules truncated.
type WorkspaceRulesTruncatedPayload struct {
	OriginalLength   int    `json:"original_length,omitempty"`
	TruncatedLength  int    `json:"truncated_length,omitempty"`
	MaxContentLength int    `json:"max_content_length,omitempty"`
	Marker           string `json:"marker,omitempty"`
}

// PlanEventPayload is the trace payload for plan event.
type PlanEventPayload struct {
	SessionID    string             `json:"session_id,omitempty"`
	Steps        []PlanStepPayload  `json:"steps,omitempty"`
	Summary      PlanSummaryPayload `json:"summary,omitempty"`
	ActiveStepID string             `json:"active_step_id,omitempty"`
	Explanation  string             `json:"explanation,omitempty"`
	Source       string             `json:"source,omitempty"`
	Changes      []PlanToolChange   `json:"changes,omitempty"`
}

// PlanStepPayload is the trace payload for plan step.
type PlanStepPayload struct {
	ID      string `json:"id,omitempty"`
	Content string `json:"content,omitempty"`
	Status  string `json:"status,omitempty"`
}

// PlanSummaryPayload is the trace payload for plan summary.
type PlanSummaryPayload struct {
	Total      int `json:"total,omitempty"`
	Pending    int `json:"pending,omitempty"`
	InProgress int `json:"in_progress,omitempty"`
	Completed  int `json:"completed,omitempty"`
	Cancelled  int `json:"cancelled,omitempty"`
}

// MemoryEventPayload is the trace payload for memory event.
type MemoryEventPayload struct {
	SessionID                string            `json:"session_id,omitempty"`
	MemoryID                 string            `json:"memory_id,omitempty"`
	ItemID                   string            `json:"item_id,omitempty"`
	Provider                 string            `json:"provider,omitempty"`
	Source                   string            `json:"source,omitempty"`
	Status                   string            `json:"status,omitempty"`
	Kind                     string            `json:"kind,omitempty"`
	Action                   string            `json:"action,omitempty"`
	CandidateKind            string            `json:"candidate_kind,omitempty"`
	RejectionReason          string            `json:"rejection_reason,omitempty"`
	SourceQuality            string            `json:"source_quality,omitempty"`
	Usefulness               string            `json:"usefulness,omitempty"`
	AdmissionState           string            `json:"admission_state,omitempty"`
	WriteStatus              string            `json:"write_status,omitempty"`
	MatchType                string            `json:"match_type,omitempty"`
	CandidateMemoryID        string            `json:"candidate_memory_id,omitempty"`
	CandidateTitle           string            `json:"candidate_title,omitempty"`
	RelatedMemoryID          string            `json:"related_memory_id,omitempty"`
	RelatedMemoryKind        string            `json:"related_memory_kind,omitempty"`
	RelatedMemoryStatus      string            `json:"related_memory_status,omitempty"`
	RelatedCandidateKind     string            `json:"related_candidate_kind,omitempty"`
	RelatedTitle             string            `json:"related_title,omitempty"`
	Trigger                  string            `json:"trigger,omitempty"`
	Reason                   string            `json:"reason,omitempty"`
	Error                    string            `json:"error,omitempty"`
	Operation                string            `json:"operation,omitempty"`
	Policy                   string            `json:"policy,omitempty"`
	ConflictState            string            `json:"conflict_state,omitempty"`
	Fallback                 string            `json:"fallback,omitempty"`
	ReplacementMemoryID      string            `json:"replacement_memory_id,omitempty"`
	ReplacementStatus        string            `json:"replacement_status,omitempty"`
	SupersededMemoryKind     string            `json:"superseded_memory_kind,omitempty"`
	SourceKind               string            `json:"source_kind,omitempty"`
	SourceState              string            `json:"source_state,omitempty"`
	Tool                     string            `json:"tool,omitempty"`
	ToolCallID               string            `json:"tool_call_id,omitempty"`
	TriggerReason            string            `json:"trigger_reason,omitempty"`
	RunID                    string            `json:"run_id,omitempty"`
	BackgroundRunID          string            `json:"background_run_id,omitempty"`
	CheckpointID             string            `json:"checkpoint_id,omitempty"`
	SummaryID                string            `json:"summary_id,omitempty"`
	Title                    string            `json:"title,omitempty"`
	Text                     string            `json:"text,omitempty"`
	MaxCalls                 int               `json:"max_calls,omitempty"`
	MaxWindows               int               `json:"max_windows,omitempty"`
	MaxWindowChars           int               `json:"max_window_chars,omitempty"`
	MaxWindowTokens          int               `json:"max_window_tokens,omitempty"`
	ToolCount                int               `json:"tool_count,omitempty"`
	ToolCalls                int               `json:"tool_calls,omitempty"`
	MaxChars                 int               `json:"max_chars,omitempty"`
	QueryChars               int               `json:"query_chars,omitempty"`
	KindCount                int               `json:"kind_count,omitempty"`
	StatusCount              int               `json:"status_count,omitempty"`
	HitCount                 int               `json:"hit_count,omitempty"`
	InjectedCount            int               `json:"injected_count,omitempty"`
	ResultCount              int               `json:"result_count,omitempty"`
	RelatedCount             int               `json:"related_count,omitempty"`
	RelatedLimit             int               `json:"related_limit,omitempty"`
	SourceCount              int               `json:"source_count,omitempty"`
	CandidateCount           int               `json:"candidate_count,omitempty"`
	Limit                    int               `json:"limit,omitempty"`
	MessageCount             int               `json:"message_count,omitempty"`
	WindowIndex              int               `json:"window_index,omitempty"`
	WindowSize               int               `json:"window_size,omitempty"`
	WindowCount              int               `json:"window_count,omitempty"`
	OffsetStart              int               `json:"offset_start,omitempty"`
	OffsetEnd                int               `json:"offset_end,omitempty"`
	WindowStartOffset        int               `json:"window_start_offset,omitempty"`
	WindowEndOffset          int               `json:"window_end_offset,omitempty"`
	SourceEndOffset          int               `json:"source_end_offset,omitempty"`
	SourceMessageCount       int               `json:"source_message_count,omitempty"`
	EpisodicCheckpointOffset int               `json:"episodic_checkpoint_offset,omitempty"`
	Attempt                  int               `json:"attempt,omitempty"`
	RetryCount               int               `json:"retry_count,omitempty"`
	WriteCount               int               `json:"write_count,omitempty"`
	SkipCount                int               `json:"skip_count,omitempty"`
	FailureCount             int               `json:"failure_count,omitempty"`
	DurationMS               int64             `json:"duration_ms,omitempty"`
	RetentionMS              int64             `json:"retention_ms,omitempty"`
	SearchMinScore           float64           `json:"search_min_score,omitempty"`
	SearchFilteredCount      int               `json:"search_filtered_count,omitempty"`
	Confidence               float64           `json:"confidence,omitempty"`
	RelatedTopScore          float64           `json:"related_top_score,omitempty"`
	RelatedScore             float64           `json:"related_score,omitempty"`
	CandidateTextChars       int               `json:"candidate_text_chars,omitempty"`
	Eligible                 *bool             `json:"eligible,omitempty"`
	Approved                 *bool             `json:"approved,omitempty"`
	ReplacementApproved      *bool             `json:"replacement_approved,omitempty"`
	MemoryIDs                []string          `json:"memory_ids,omitempty"`
	RelatedMemoryIDs         []string          `json:"related_memory_ids,omitempty"`
	PinnedItems              []MemoryTraceItem `json:"pinned_items,omitempty"`
	SearchHits               []MemoryTraceItem `json:"search_hits,omitempty"`
	InjectedItems            []MemoryTraceItem `json:"injected_items,omitempty"`
	StartedAt                time.Time         `json:"started_at,omitempty"`
	CompletedAt              time.Time         `json:"completed_at,omitempty"`
}

// MemoryTraceItem represents one memory trace item.
type MemoryTraceItem struct {
	ID           string  `json:"id,omitempty"`
	Kind         string  `json:"kind,omitempty"`
	Status       string  `json:"status,omitempty"`
	Title        string  `json:"title,omitempty"`
	TextChars    int     `json:"text_chars,omitempty"`
	Confidence   float64 `json:"confidence,omitempty"`
	Reflected    bool    `json:"reflected,omitempty"`
	SourceCount  int     `json:"source_count,omitempty"`
	Score        float64 `json:"score,omitempty"`
	LexicalScore float64 `json:"lexical_score,omitempty"`
	VectorScore  float64 `json:"vector_score,omitempty"`
}

// PlanToolOperation classifies plan-tool trace operations.
type PlanToolOperation string

const (
	PlanToolOperationRead           PlanToolOperation = "read"
	PlanToolOperationUpdate         PlanToolOperation = "update"
	PlanToolOperationClearCompleted PlanToolOperation = "clear_completed"
)

// PlanToolState describes plan tool state.
type PlanToolState struct {
	Operation      PlanToolOperation `json:"operation,omitempty"`
	ChangedCount   int               `json:"changed_count,omitempty"`
	TotalCount     int               `json:"total_count,omitempty"`
	CompletedCount int               `json:"completed_count,omitempty"`
	Changes        []PlanToolChange  `json:"changes,omitempty"`
}

// PlanToolChange describes plan tool change.
type PlanToolChange struct {
	Index  int      `json:"index,omitempty"`
	ID     string   `json:"id,omitempty"`
	Action string   `json:"action,omitempty"`
	Fields []string `json:"fields,omitempty"`
}

// ProcessToolOperation classifies process-tool trace operations.
type ProcessToolOperation string

const (
	ProcessToolOperationStart  ProcessToolOperation = "start"
	ProcessToolOperationStatus ProcessToolOperation = "status"
	ProcessToolOperationRead   ProcessToolOperation = "read"
	ProcessToolOperationStop   ProcessToolOperation = "stop"
	ProcessToolOperationList   ProcessToolOperation = "list"
)

// ProcessToolState describes process tool state.
type ProcessToolState struct {
	Operation   ProcessToolOperation `json:"operation,omitempty"`
	ProcessID   string               `json:"process_id,omitempty"`
	Command     string               `json:"command,omitempty"`
	Status      string               `json:"status,omitempty"`
	ExitCode    *int                 `json:"exit_code,omitempty"`
	StdoutBytes int                  `json:"stdout_bytes,omitempty"`
	StderrBytes int                  `json:"stderr_bytes,omitempty"`
	Count       int                  `json:"count,omitempty"`
	ErrorCode   string               `json:"error_code,omitempty"`
	Error       string               `json:"error,omitempty"`
}

// ToolInvocationStartedPayload is the trace payload for tool invocation started.
type ToolInvocationStartedPayload struct {
	ID           string            `json:"id,omitempty"`
	Name         string            `json:"name,omitempty"`
	Input        string            `json:"input,omitempty"`
	Detail       string            `json:"detail,omitempty"`
	PlanState    *PlanToolState    `json:"plan_state,omitempty"`
	ProcessState *ProcessToolState `json:"process_state,omitempty"`
}

// ToolInvocationCompletedPayload is the trace payload for tool invocation completed.
type ToolInvocationCompletedPayload struct {
	ToolCallID               string            `json:"tool_call_id,omitempty"`
	Name                     string            `json:"name,omitempty"`
	Content                  string            `json:"content,omitempty"`
	Detail                   string            `json:"detail,omitempty"`
	Failed                   bool              `json:"failed,omitempty"`
	SemanticProjectionStatus string            `json:"semantic_projection_status,omitempty"`
	SemanticContentBytes     int               `json:"semantic_content_bytes,omitempty"`
	PlanState                *PlanToolState    `json:"plan_state,omitempty"`
	ProcessState             *ProcessToolState `json:"process_state,omitempty"`
}

// DecodePayload converts a stored trace payload into its concrete event payload type.
func DecodePayload(eventType string, payload any) (any, bool) {
	eventTypeValue := str.String(eventType)
	switch eventTypeValue.Trim() {
	case EvtChatStarted:
		return decodePayloadAs[Metadata](payload)
	case EvtSessionFailed:
		return decodePayloadAs[SessionFailedPayload](payload)
	case EvtSessionQueueEnqueued,
		EvtSessionQueueClaimed,
		EvtSessionSteeringDelivered,
		EvtSessionQueueCompleted,
		EvtSessionQueueInterrupted,
		EvtSessionQueueFailed,
		EvtSessionQueueCancelled:
		return decodePayloadAs[SessionQueueEventPayload](payload)
	case EvtInputSafetyBlocked,
		EvtOutputSafetyApplied,
		EvtToolOutputSafetyApplied,
		EvtLoadedContentSafetyBlocked,
		EvtMemorySafetyBlocked:
		return decodePayloadAs[SafetyEventPayload](payload)
	case EvtUserMessageAccepted:
		return decodePayloadAs[UserMessageAcceptedPayload](payload)
	case EvtModelRequest:
		return decodePayloadAs[models.Request](payload)
	case EvtModelResponse:
		return decodePayloadAs[models.Response](payload)
	case EvtModelReasoningCompleted:
		return decodePayloadAs[ModelReasoningCompletedPayload](payload)
	case EvtFinalAssistantResponse:
		return decodePayloadAs[FinalAssistantResponsePayload](payload)
	case EvtToolInvocationStarted:
		return ToolInvocationStartedPayloadFrom(payload)
	case EvtToolInvocationCompleted:
		return ToolInvocationCompletedPayloadFrom(payload)
	case EvtPermissionDecisionObserved:
		return decodePayloadAs[PermissionDecisionPayload](payload)
	case EvtPermissionApprovalChanged:
		return decodePayloadAs[PermissionApprovalPayload](payload)
	case EvtSummaryFallbackStarted:
		return decodePayloadAs[SummaryFallbackStartedPayload](payload)
	case EvtContextPreflight,
		EvtContextPostflightUsage,
		EvtContextCompactionTriggered,
		EvtContextCompactionWarning:
		return decodePayloadAs[ContextEventPayload](payload)
	case EvtContextCompactionPending,
		EvtContextCompactionRunning,
		EvtContextCompactionSucceeded,
		EvtContextCompactionFailed:
		return decodePayloadAs[CompactionEventPayload](payload)
	case EvtSummaryRequested,
		EvtSummarySaved,
		EvtSummaryFailed,
		EvtSummaryParseFailed,
		EvtSummaryApplied,
		EvtRecallSummaryRequested,
		EvtRecallSummarySaved,
		EvtRecallSummaryFailed:
		return decodePayloadAs[SummaryEventPayload](payload)
	case EvtMemoryRetrievalStarted,
		EvtMemoryRetrieved,
		EvtMemoryRetrievalFailed,
		EvtMemoryFlushStarted,
		EvtMemoryFlushModelRequested,
		EvtMemoryFlushWriteRequested,
		EvtMemoryFlushSkipped,
		EvtMemoryFlushFailed,
		EvtMemoryFlushTimeout,
		EvtMemoryFlushCompleted,
		EvtMemoryExtractionStarted,
		EvtMemoryExtractionWindowLoaded,
		EvtMemoryExtractionExtractorRequested,
		EvtMemoryExtractionCandidates,
		EvtMemoryExtractionCandidateGenerated,
		EvtMemoryExtractionCandidateRejected,
		EvtMemoryExtractionConfidenceScored,
		EvtMemoryExtractionAdmissionMorphoff,
		EvtMemoryExtractionMemoryWritten,
		EvtMemoryExtractionDuplicateSkipped,
		EvtMemoryExtractionFailed,
		EvtMemoryExtractionCompleted,
		EvtMemoryEpisodicBackgroundScheduled,
		EvtMemoryEpisodicBackgroundEligibilityChecked,
		EvtMemoryEpisodicBackgroundWindowCheckpoint,
		EvtMemoryEpisodicBackgroundExtractionAttempt,
		EvtMemoryEpisodicBackgroundRetry,
		EvtMemoryEpisodicBackgroundFailed,
		EvtMemoryEpisodicBackgroundCompleted,
		EvtMemoryReflectionStarted,
		EvtMemoryReflectionSourceLoaded,
		EvtMemoryReflectionRelatedLoaded,
		EvtMemoryReflectionCandidateGenerated,
		EvtMemoryReflectionCandidateRejected,
		EvtMemoryReflectionMemoryWritten,
		EvtMemoryReflectionFailed,
		EvtMemoryReflectionCompleted,
		EvtMemoryPromotionStarted,
		EvtMemoryPromotionDecision,
		EvtMemoryPromotionCompleted,
		EvtMemoryPromotionFailed,
		EvtMemoryPromotionFallback,
		EvtMemoryPromotionBackgroundCompleted,
		EvtMemoryPromotionBackgroundFailed,
		EvtMemoryPromotionCleanupCompleted,
		EvtMemoryPromotionCleanupFailed,
		EvtMemoryPromotionCleanupSkipped:
		return decodePayloadAs[MemoryEventPayload](payload)
	case EvtWorkspaceRulesTruncated:
		return decodePayloadAs[WorkspaceRulesTruncatedPayload](payload)
	case EvtPlanUpdated,
		EvtPlanCleared,
		EvtPlanHydrated:
		return decodePayloadAs[PlanEventPayload](payload)
	default:
		eventTypeValue2 := str.String(eventType)
		if strings.HasPrefix(eventTypeValue2.Trim(), "memory.") {
			return decodePayloadAs[MemoryEventPayload](payload)
		}
		return nil, false
	}
}

// DecodePayloadJSON decodes raw JSON into the concrete payload for eventType.
func DecodePayloadJSON(eventType string, payload json.RawMessage) (any, bool) {
	if len(payload) == 0 {
		return DecodePayload(eventType, nil)
	}

	return DecodePayload(eventType, payload)
}

// ToolInvocationStartedPayloadFrom builds a trace payload from a model tool call.
func ToolInvocationStartedPayloadFrom(payload any) (ToolInvocationStartedPayload, bool) {
	switch value := payload.(type) {
	case ToolInvocationStartedPayload:
		return value, value.ID != "" || value.Name != ""
	case models.ToolCall:
		iDValue := str.String(value.ID)
		nameValue := str.String(value.Name)
		iDValue2 := str.String(value.ID)
		nameValue2 := str.String(value.Name)
		return ToolInvocationStartedPayload{
			ID:    iDValue.Trim(),
			Name:  nameValue.Trim(),
			Input: value.Input,
		}, iDValue2.Trim() != "" || nameValue2.Trim() != ""
	case morphmsg.ToolCall:
		iDValue3 := str.String(value.ID)
		nameValue3 := str.String(value.Name)
		iDValue4 := str.String(value.ID)
		nameValue4 := str.String(value.Name)
		return ToolInvocationStartedPayload{
			ID:    iDValue3.Trim(),
			Name:  nameValue3.Trim(),
			Input: value.Input,
		}, iDValue4.Trim() != "" || nameValue4.Trim() != ""
	}

	fields := PayloadFields(payload)
	if len(fields) == 0 {
		return ToolInvocationStartedPayload{}, false
	}

	result := ToolInvocationStartedPayload{
		ID:           PayloadString(fields, "id", "ID", "tool_call_id", "ToolCallID"),
		Name:         PayloadString(fields, "name", "Name", "tool"),
		Input:        PayloadString(fields, "input", "Input"),
		Detail:       PayloadString(fields, "detail", "Detail"),
		PlanState:    planToolStateFromAny(fields["plan_state"]),
		ProcessState: processToolStateFromAny(fields["process_state"]),
	}

	return result, result.ID != "" || result.Name != ""
}

// ToolInvocationCompletedPayloadFrom builds a trace payload from a tool response message.
func ToolInvocationCompletedPayloadFrom(payload any) (ToolInvocationCompletedPayload, bool) {
	switch value := payload.(type) {
	case ToolInvocationCompletedPayload:
		value.Failed = value.Failed || ToolInvocationFailed(value.Content)
		return value, value.ToolCallID != "" || value.Name != ""
	case morphmsg.Message:
		toolCallIDValue := str.String(value.ToolCallID)
		nameValue5 := str.String(value.Name)
		toolCallIDValue2 := str.String(value.ToolCallID)
		nameValue6 := str.String(value.Name)
		semanticStatus := "skipped"
		if value.SemanticContent != "" {
			semanticStatus = "projected"
		}
		return ToolInvocationCompletedPayload{
			ToolCallID:               toolCallIDValue.Trim(),
			Name:                     nameValue5.Trim(),
			Content:                  value.Content,
			Failed:                   ToolInvocationFailed(value.Content),
			SemanticProjectionStatus: semanticStatus,
			SemanticContentBytes:     len(value.SemanticContent),
		}, toolCallIDValue2.Trim() != "" || nameValue6.Trim() != ""
	}

	fields := PayloadFields(payload)
	if len(fields) == 0 {
		return ToolInvocationCompletedPayload{}, false
	}

	result := ToolInvocationCompletedPayload{
		ToolCallID: PayloadString(fields, "tool_call_id", "ToolCallID", "id", "ID"),
		Name:       PayloadString(fields, "name", "Name", "tool"),
		Content:    PayloadString(fields, "content", "Content"),
		Detail:     PayloadString(fields, "detail", "Detail"),
		Failed:     payloadBool(fields, "failed", "Failed"),
		SemanticProjectionStatus: PayloadString(
			fields,
			"semantic_projection_status",
			"SemanticProjectionStatus",
		),
		SemanticContentBytes: payloadInteger(fields, "semantic_content_bytes", "SemanticContentBytes"),
		PlanState:            planToolStateFromAny(fields["plan_state"]),
		ProcessState:         processToolStateFromAny(fields["process_state"]),
	}
	result.Failed = result.Failed || ToolInvocationFailed(result.Content)

	return result, result.ToolCallID != "" || result.Name != ""
}

func ToolInvocationFailed(content string) bool {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &fields); err != nil {
		return false
	}
	raw, ok := fields["error"]
	if !ok {
		return false
	}
	rawText := strings.TrimSpace(string(raw))
	if rawText == "" || rawText == "null" {
		return false
	}
	var message string
	if err := json.Unmarshal(raw, &message); err == nil {
		return strings.TrimSpace(message) != ""
	}

	return true
}

// PlanToolInputState extracts plan state from a plan tool input payload.
func PlanToolInputState(input string) *PlanToolState {
	fields := map[string]any{}
	inputValue := str.String(input)
	if err := json.Unmarshal([]byte(inputValue.Trim()), &fields); err != nil {
		return nil
	}
	steps, hasSteps := fields["steps"]
	if !hasSteps || steps == nil {
		return &PlanToolState{Operation: PlanToolOperationRead}
	}

	operation := PlanToolOperationUpdate
	if clearCompleted, _ := fields["clear_completed"].(bool); clearCompleted {
		operation = PlanToolOperationClearCompleted
	}

	return &PlanToolState{
		Operation:    operation,
		ChangedCount: len(anySlice(fields["steps"])),
	}
}

// PlanToolOutputState extracts plan state from a plan tool output payload.
func PlanToolOutputState(output string) *PlanToolState {
	fields := map[string]any{}
	outputValue := str.String(output)
	if err := json.Unmarshal([]byte(outputValue.Trim()), &fields); err != nil {
		return nil
	}
	fields = unwrapPlanToolOutputFields(fields)

	summary, _ := fields["summary"].(map[string]any)
	if len(summary) == 0 && fields["changes"] == nil {
		return nil
	}

	return &PlanToolState{
		TotalCount:     payloadInt(summary["total"]),
		CompletedCount: payloadInt(summary["completed"]),
		Changes:        planToolChangesFromAny(fields["changes"]),
	}
}

// ProcessToolInputState extracts process state from a process tool input payload.
func ProcessToolInputState(input string) *ProcessToolState {
	fields := map[string]any{}
	inputValue2 := str.String(input)
	if err := json.Unmarshal([]byte(inputValue2.Trim()), &fields); err != nil {
		return nil
	}
	payloadStringValue := str.String(PayloadString(fields, "action"))
	operation := ProcessToolOperation(payloadStringValue.Normalized())
	switch operation {
	case ProcessToolOperationStart:
		command := formatProcessCommand(PayloadString(fields, "command"), payloadStringSlice(fields["args"]))
		return &ProcessToolState{Operation: operation, Command: command}
	case ProcessToolOperationStatus, ProcessToolOperationRead, ProcessToolOperationStop:
		return &ProcessToolState{Operation: operation, ProcessID: PayloadString(fields, "process_id")}
	case ProcessToolOperationList:
		return &ProcessToolState{Operation: operation}
	default:
		return nil
	}
}

// ProcessToolOutputState extracts process state from a process tool output payload.
func ProcessToolOutputState(output string) *ProcessToolState {
	fields := map[string]any{}
	outputValue2 := str.String(output)
	if err := json.Unmarshal([]byte(outputValue2.Trim()), &fields); err != nil {
		return nil
	}

	if state := processToolErrorState(fields["error"]); state != nil {
		return state
	}
	fields = unwrapToolOutputFields(fields)

	if rawProcesses, ok := fields["processes"]; ok {
		return &ProcessToolState{
			Operation: ProcessToolOperationList,
			Count:     len(anySlice(rawProcesses)),
		}
	}

	processFields := PayloadFields(fields["process"])
	if len(processFields) == 0 {
		return nil
	}

	state := &ProcessToolState{
		ProcessID: PayloadString(processFields, "id", "ID"),
		Command: formatProcessCommand(
			PayloadString(processFields, "command", "Command"),
			payloadStringSlice(processFields["args"])),
		Status:      PayloadString(processFields, "status", "Status"),
		ExitCode:    payloadIntPtr(processFields["exit_code"]),
		StdoutBytes: payloadInt(processFields["stdout_bytes"]),
		StderrBytes: payloadInt(processFields["stderr_bytes"]),
	}

	if _, hasOutput := fields["output"]; hasOutput {
		state.Operation = ProcessToolOperationRead
		outputFields := PayloadFields(fields["output"])
		state.StdoutBytes = payloadInt(outputFields["stdout_bytes"])
		state.StderrBytes = payloadInt(outputFields["stderr_bytes"])
	}

	return state
}

func unwrapToolOutputFields(fields map[string]any) map[string]any {
	if len(fields) == 0 || fields["process"] != nil || fields["processes"] != nil {
		return fields
	}

	output, ok := fields["output"].(string)
	outputValue3 := str.String(output)
	if !ok || outputValue3.Trim() == "" {
		return fields
	}

	unwrapped := map[string]any{}
	outputValue4 := str.String(output)
	if err := json.Unmarshal([]byte(outputValue4.Trim()), &unwrapped); err != nil {
		return fields
	}
	if len(unwrapped) == 0 {
		return fields
	}

	return unwrapped
}

func unwrapPlanToolOutputFields(fields map[string]any) map[string]any {
	if len(fields) == 0 || fields["summary"] != nil || fields["changes"] != nil {
		return fields
	}

	output, ok := fields["output"].(string)
	outputValue5 := str.String(output)
	if !ok || outputValue5.Trim() == "" {
		return fields
	}

	unwrapped := map[string]any{}
	outputValue6 := str.String(output)
	if err := json.Unmarshal([]byte(outputValue6.Trim()), &unwrapped); err != nil {
		return fields
	}
	if len(unwrapped) == 0 {
		return fields
	}

	return unwrapped
}

// PayloadFields returns structured fields for a trace payload.
func PayloadFields(payload any) map[string]any {
	if payload == nil {
		return nil
	}
	if fields, ok := payload.(map[string]any); ok {
		return fields
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil
	}

	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil
	}

	return fields
}

func anySlice(value any) []any {
	switch items := value.(type) {
	case []any:
		return items
	default:
		return nil
	}
}

// PayloadString returns a concise display string for a trace payload.
func PayloadString(fields map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := fields[key]
		if !ok {
			continue
		}
		text, ok := value.(string)
		if !ok {
			continue
		}
		textValue := str.String(text)
		if text = textValue.Trim(); text != "" {
			return text
		}
	}

	return ""
}

func payloadBool(fields map[string]any, keys ...string) bool {
	for _, key := range keys {
		if value, ok := fields[key].(bool); ok {
			return value
		}
	}

	return false
}

func payloadInteger(fields map[string]any, keys ...string) int {
	for _, key := range keys {
		if value, ok := fields[key]; ok {
			return payloadInt(value)
		}
	}
	return 0
}

func decodePayloadAs[T any](payload any) (T, bool) {
	if payload == nil {
		var empty T
		return empty, true
	}
	if value, ok := payload.(T); ok {
		return value, true
	}

	data, err := json.Marshal(payload)
	if err != nil {
		var empty T
		return empty, false
	}

	var result T
	if err := json.Unmarshal(data, &result); err != nil {
		var empty T
		return empty, false
	}

	return result, true
}

func planToolStateFromAny(value any) *PlanToolState {
	fields := PayloadFields(value)
	if len(fields) == 0 {
		return nil
	}

	return &PlanToolState{
		Operation:      PlanToolOperation(PayloadString(fields, "operation", "Operation")),
		ChangedCount:   payloadInt(fields["changed_count"]),
		TotalCount:     payloadInt(fields["total_count"]),
		CompletedCount: payloadInt(fields["completed_count"]),
		Changes:        planToolChangesFromAny(fields["changes"]),
	}
}

func processToolStateFromAny(value any) *ProcessToolState {
	fields := PayloadFields(value)
	if len(fields) == 0 {
		return nil
	}

	return &ProcessToolState{
		Operation:   ProcessToolOperation(PayloadString(fields, "operation", "Operation")),
		ProcessID:   PayloadString(fields, "process_id", "ProcessID"),
		Command:     PayloadString(fields, "command", "Command"),
		Status:      PayloadString(fields, "status", "Status"),
		ExitCode:    payloadIntPtr(fields["exit_code"]),
		StdoutBytes: payloadInt(fields["stdout_bytes"]),
		StderrBytes: payloadInt(fields["stderr_bytes"]),
		Count:       payloadInt(fields["count"]),
		ErrorCode:   PayloadString(fields, "error_code", "ErrorCode"),
		Error:       PayloadString(fields, "error", "Error"),
	}
}

func processToolErrorState(value any) *ProcessToolState {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		typedValue := str.String(typed)
		if message := typedValue.Trim(); message != "" {
			return &ProcessToolState{Status: "failed", Error: message}
		}
		return nil
	default:
		fields := PayloadFields(typed)
		if len(fields) == 0 {
			return nil
		}
		message := PayloadString(fields, "message", "Message")
		code := PayloadString(fields, "code", "Code")
		if message == "" && code == "" {
			return nil
		}
		return &ProcessToolState{Status: "failed", ErrorCode: code, Error: message}
	}
}

func planToolChangesFromAny(value any) []PlanToolChange {
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return nil
	}

	changes := make([]PlanToolChange, 0, len(items))
	for _, item := range items {
		fields := PayloadFields(item)
		if len(fields) == 0 {
			continue
		}
		change := PlanToolChange{
			Index:  payloadInt(fields["index"]),
			ID:     PayloadString(fields, "id", "ID"),
			Action: PayloadString(fields, "action", "Action"),
			Fields: payloadStringSlice(fields["fields"]),
		}
		if change.Index == 0 && change.ID == "" && change.Action == "" {
			continue
		}
		changes = append(changes, change)
	}
	if len(changes) == 0 {
		return nil
	}

	return changes
}

func formatProcessCommand(command string, args []string) string {
	commandValue := str.String(command)
	command = commandValue.Trim()
	if command == "" {
		return ""
	}
	if len(args) == 0 {
		return command
	}

	parts := append([]string{command}, args...)
	for index, part := range parts {
		parts[index] = shellQuotePayloadPart(part)
	}

	return strings.Join(parts, " ")
}

func shellQuotePayloadPart(value string) string {
	if value == "" {
		return "''"
	}
	if strings.ContainsAny(value, " \t\n\"'\\$&|;()<>*?![]{}") {
		return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
	}

	return value
}

func payloadStringSlice(value any) []string {
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return nil
	}

	result := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			continue
		}
		textValue2 := str.String(text)
		if text = textValue2.Trim(); text != "" {
			result = append(result, text)
		}
	}
	if len(result) == 0 {
		return nil
	}

	return result
}

func payloadIntPtr(value any) *int {
	parsed, ok := payloadIntOK(value)
	if !ok {
		return nil
	}

	return &parsed
}

func payloadInt(value any) int {
	parsed, ok := payloadIntOK(value)
	if !ok {
		return 0
	}

	return parsed
}

func payloadIntOK(value any) (int, bool) {
	switch typed := value.(type) {
	case nil:
		return 0, false
	case float64:
		return int(typed), true
	case float32:
		return int(typed), true
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case int32:
		return int(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return int(parsed), true
	default:
		return 0, false
	}
}
