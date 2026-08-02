package trace

import (
	"slices"

	"github.com/xymorphic/morph/pkg/str"
)

const (
	EvtChatStarted                                = "chat.started"
	EvtSessionFailed                              = "session.failed"
	EvtInputSafetyBlocked                         = "input.safety.blocked"
	EvtOutputSafetyApplied                        = "output.safety.applied"
	EvtToolOutputSafetyApplied                    = "tool.output.safety.applied"
	EvtLoadedContentSafetyBlocked                 = "loaded_content.safety.blocked"
	EvtUserMessageAccepted                        = "user.message.accepted"
	EvtModelRequest                               = "model.request"
	EvtModelResponse                              = "model.response"
	EvtModelReasoningCompleted                    = "model.reasoning.completed"
	EvtFinalAssistantResponse                     = "final.assistant.response"
	EvtToolInvocationStarted                      = "tool.invocation.started"
	EvtToolInvocationCompleted                    = "tool.invocation.completed"
	EvtPermissionDecisionObserved                 = "permission.decision.observed"
	EvtPermissionApprovalChanged                  = "permission.approval.changed"
	EvtSummaryFallbackStarted                     = "summary.fallback.started"
	EvtContextPreflight                           = "context.preflight"
	EvtContextPostflightUsage                     = "context.postflight.usage_recorded"
	EvtContextCompactionTriggered                 = "context.compaction.triggered"
	EvtContextCompactionWarning                   = "context.compaction.warning"
	EvtContextCompactionPending                   = "context.compaction.pending"
	EvtContextCompactionRunning                   = "context.compaction.running"
	EvtContextCompactionSucceeded                 = "context.compaction.succeeded"
	EvtContextCompactionFailed                    = "context.compaction.failed"
	EvtSummaryRequested                           = "context.summary.requested"
	EvtSummarySaved                               = "context.summary.saved"
	EvtSummaryFailed                              = "context.summary.failed"
	EvtSummaryParseFailed                         = "context.summary.parse_failed"
	EvtSummaryApplied                             = "context.summary.applied"
	EvtRecallSummaryRequested                     = "context.recall_summary.requested"
	EvtRecallSummarySaved                         = "context.recall_summary.saved"
	EvtRecallSummaryFailed                        = "context.recall_summary.failed"
	EvtMemoryRetrievalStarted                     = "memory.search.started"
	EvtMemoryRetrieved                            = "memory.retrieved"
	EvtMemoryRetrievalFailed                      = "memory.search.failed"
	EvtMemorySafetyBlocked                        = "memory.safety.blocked"
	EvtMemoryFlushStarted                         = "memory.flush.started"
	EvtMemoryFlushModelRequested                  = "memory.flush.model_requested"
	EvtMemoryFlushWriteRequested                  = "memory.flush.write_requested"
	EvtMemoryFlushSkipped                         = "memory.flush.skipped"
	EvtMemoryFlushFailed                          = "memory.flush.failed"
	EvtMemoryFlushTimeout                         = "memory.flush.timeout"
	EvtMemoryFlushCompleted                       = "memory.flush.completed"
	EvtMemoryExtractionStarted                    = "memory.extraction.started"
	EvtMemoryExtractionWindowLoaded               = "memory.extraction.window_loaded"
	EvtMemoryExtractionExtractorRequested         = "memory.extraction.extractor_requested"
	EvtMemoryExtractionCandidates                 = "memory.extraction.candidates"
	EvtMemoryExtractionCandidateGenerated         = "memory.extraction.candidate_generated"
	EvtMemoryExtractionCandidateRejected          = "memory.extraction.candidate_rejected"
	EvtMemoryExtractionConfidenceScored           = "memory.extraction.confidence_scored"
	EvtMemoryExtractionAdmissionMorphoff          = "memory.extraction.admission_morphoff"
	EvtMemoryExtractionMemoryWritten              = "memory.extraction.memory_written"
	EvtMemoryExtractionDuplicateSkipped           = "memory.extraction.duplicate_skipped"
	EvtMemoryExtractionFailed                     = "memory.extraction.failed"
	EvtMemoryExtractionCompleted                  = "memory.extraction.completed"
	EvtMemoryEpisodicBackgroundScheduled          = "memory.episodic_background.scheduled"
	EvtMemoryEpisodicBackgroundEligibilityChecked = "memory.episodic_background.eligibility_checked"
	EvtMemoryEpisodicBackgroundWindowCheckpoint   = "memory.episodic_background.window_checkpoint"
	EvtMemoryEpisodicBackgroundExtractionAttempt  = "memory.episodic_background.extraction_attempt"
	EvtMemoryEpisodicBackgroundRetry              = "memory.episodic_background.retry"
	EvtMemoryEpisodicBackgroundFailed             = "memory.episodic_background.failed"
	EvtMemoryEpisodicBackgroundCompleted          = "memory.episodic_background.completed"
	EvtMemoryReflectionStarted                    = "memory.reflection.started"
	EvtMemoryReflectionSourceLoaded               = "memory.reflection.source_loaded"
	EvtMemoryReflectionRelatedLoaded              = "memory.reflection.related_loaded"
	EvtMemoryReflectionCandidateGenerated         = "memory.reflection.candidate_generated"
	EvtMemoryReflectionCandidateRejected          = "memory.reflection.candidate_rejected"
	EvtMemoryReflectionMemoryWritten              = "memory.reflection.memory_written"
	EvtMemoryReflectionFailed                     = "memory.reflection.failed"
	EvtMemoryReflectionCompleted                  = "memory.reflection.completed"
	EvtMemoryPromotionStarted                     = "memory.promotion.started"
	EvtMemoryPromotionDecision                    = "memory.promotion.decision"
	EvtMemoryPromotionCompleted                   = "memory.promotion.completed"
	EvtMemoryPromotionFailed                      = "memory.promotion.failed"
	EvtMemoryPromotionFallback                    = "memory.promotion.fallback"
	EvtMemoryPromotionBackgroundCompleted         = "memory.promotion_background.completed"
	EvtMemoryPromotionBackgroundFailed            = "memory.promotion_background.failed"
	EvtMemoryPromotionCleanupCompleted            = "memory.promotion_cleanup.completed"
	EvtMemoryPromotionCleanupFailed               = "memory.promotion_cleanup.failed"
	EvtMemoryPromotionCleanupSkipped              = "memory.promotion_cleanup.skipped"
	EvtWorkspaceRulesTruncated                    = "workspace.rules.truncated"
	EvtPlanUpdated                                = "plan.updated"
	EvtPlanCleared                                = "plan.cleared"
	EvtPlanHydrated                               = "plan.hydrated"
	EvtSessionQueueEnqueued                       = "session.queue.enqueued"
	EvtSessionQueueClaimed                        = "session.queue.claimed"
	EvtSessionSteeringDelivered                   = "session.steering.delivered"
	EvtSessionQueueCompleted                      = "session.queue.completed"
	EvtSessionQueueInterrupted                    = "session.queue.interrupted"
	EvtSessionQueueFailed                         = "session.queue.failed"
	EvtSessionQueueCancelled                      = "session.queue.cancelled"
	EvtExecutionStarted                           = "execution.started"
	EvtExecutionCompleted                         = "execution.completed"
	EvtExecutionFailed                            = "execution.failed"
	EvtExecutionEnvironmentAcquiring              = "execution.environment.acquiring"
	EvtExecutionEnvironmentReady                  = "execution.environment.ready"
	EvtExecutionEnvironmentRecreating             = "execution.environment.recreating"
	EvtExecutionEnvironmentRecreated              = "execution.environment.recreated"
)

var episodicMemoryTraceEventTypes = []string{
	EvtSessionFailed,
	EvtToolInvocationStarted,
	EvtToolInvocationCompleted,
	EvtPermissionDecisionObserved,
	EvtPermissionApprovalChanged,
	EvtContextCompactionTriggered,
	EvtContextCompactionWarning,
	EvtContextCompactionPending,
	EvtContextCompactionRunning,
	EvtContextCompactionSucceeded,
	EvtContextCompactionFailed,
	EvtSummaryFallbackStarted,
	EvtSummaryRequested,
	EvtSummarySaved,
	EvtSummaryFailed,
	EvtSummaryParseFailed,
	EvtSummaryApplied,
	EvtMemoryFlushStarted,
	EvtMemoryFlushModelRequested,
	EvtMemoryFlushWriteRequested,
	EvtMemoryFlushSkipped,
	EvtMemoryFlushFailed,
	EvtMemoryFlushTimeout,
	EvtMemoryFlushCompleted,
	EvtWorkspaceRulesTruncated,
	EvtPlanUpdated,
	EvtPlanCleared,
	EvtPlanHydrated,
}

var allTraceEventTypes = []string{
	EvtChatStarted,
	EvtSessionFailed,
	EvtInputSafetyBlocked,
	EvtOutputSafetyApplied,
	EvtToolOutputSafetyApplied,
	EvtLoadedContentSafetyBlocked,
	EvtUserMessageAccepted,
	EvtModelRequest,
	EvtModelResponse,
	EvtModelReasoningCompleted,
	EvtFinalAssistantResponse,
	EvtToolInvocationStarted,
	EvtToolInvocationCompleted,
	EvtPermissionDecisionObserved,
	EvtPermissionApprovalChanged,
	EvtSummaryFallbackStarted,
	EvtContextPreflight,
	EvtContextPostflightUsage,
	EvtContextCompactionTriggered,
	EvtContextCompactionWarning,
	EvtContextCompactionPending,
	EvtContextCompactionRunning,
	EvtContextCompactionSucceeded,
	EvtContextCompactionFailed,
	EvtSummaryRequested,
	EvtSummarySaved,
	EvtSummaryFailed,
	EvtSummaryParseFailed,
	EvtSummaryApplied,
	EvtRecallSummaryRequested,
	EvtRecallSummarySaved,
	EvtRecallSummaryFailed,
	EvtMemoryRetrievalStarted,
	EvtMemoryRetrieved,
	EvtMemoryRetrievalFailed,
	EvtMemorySafetyBlocked,
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
	EvtMemoryPromotionCleanupSkipped,
	EvtWorkspaceRulesTruncated,
	EvtPlanUpdated,
	EvtPlanCleared,
	EvtPlanHydrated,
	EvtSessionQueueEnqueued,
	EvtSessionQueueClaimed,
	EvtSessionSteeringDelivered,
	EvtSessionQueueCompleted,
	EvtSessionQueueInterrupted,
	EvtSessionQueueFailed,
	EvtSessionQueueCancelled,
	EvtExecutionStarted,
	EvtExecutionCompleted,
	EvtExecutionFailed,
	EvtExecutionEnvironmentAcquiring,
	EvtExecutionEnvironmentReady,
	EvtExecutionEnvironmentRecreating,
	EvtExecutionEnvironmentRecreated,
}

// AllTraceEventTypes returns every known trace event type.
func AllTraceEventTypes() []string {
	return append([]string(nil), allTraceEventTypes...)
}

// EpisodicMemoryTraceEventTypes returns trace event types emitted by episodic memory workflows.
func EpisodicMemoryTraceEventTypes() []string {
	return append([]string(nil), episodicMemoryTraceEventTypes...)
}

// IsEpisodicMemoryTraceEventType reports whether eventType belongs to episodic memory workflows.
func IsEpisodicMemoryTraceEventType(eventType string) bool {
	eventTypeValue := str.String(eventType)
	eventType = eventTypeValue.Trim()
	return slices.Contains(episodicMemoryTraceEventTypes, eventType)
}
