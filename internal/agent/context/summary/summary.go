package summary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/xymorphic/morph/pkg/logutils"
	"github.com/xymorphic/morph/pkg/str"

	ctxbuilder "github.com/xymorphic/morph/internal/agent/context"
	"github.com/xymorphic/morph/internal/agent/context/compaction"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/constants"
	instruct "github.com/xymorphic/morph/internal/instructions"
	models "github.com/xymorphic/morph/internal/model"
	storage "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/internal/trace"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
)

var log = logutils.Module("summary")

// SessionSummaryPlanner selects how forced persisted summaries choose the
// message range to compact.
type SessionSummaryPlanner string

// SessionSummaryPlannerRetainRecentTail summarizes all but the configured recent tail.
const SessionSummaryPlannerRetainRecentTail SessionSummaryPlanner = "retain_recent_tail"

// SummarizeSessionOptions controls forced persisted summary generation.
type SummarizeSessionOptions struct {
	Planner              SessionSummaryPlanner
	RetainedTailMessages *int
}

// SummaryState is the in-memory form of a persisted session summary.
type SummaryState struct {
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

// summaryPayload is the structured model response expected from the summary model.
type summaryPayload struct {
	SessionSummary string   `json:"session_summary"`
	CurrentTask    string   `json:"current_task"`
	Discoveries    []string `json:"discoveries"`
	OpenQuestions  []string `json:"open_questions"`
	NextActions    []string `json:"next_actions"`
}

// summaryStructuredOutput asks compatible providers for a strict summary JSON object.
var summaryStructuredOutput = &models.StructuredOutput{
	Name:        "session_summary",
	Description: "Structured morphoff summary for compacted conversation history.",
	Strict:      true,
	Schema: map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"session_summary": map[string]any{"type": "string"},
			"current_task":    map[string]any{"type": "string"},
			"discoveries": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
			"open_questions": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
			"next_actions": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
		},
		"required": []string{
			"session_summary",
			"current_task",
			"discoveries",
			"open_questions",
			"next_actions",
		},
	},
}

// refreshPlan describes the persisted prefix that a compaction pass should summarize.
type refreshPlan struct {
	RequestedAt        time.Time
	TargetMessageCount int
	TargetOffset       int
	Auto               bool
}

// recallPlan describes temporary recall windows that should be summarized and merged.
type recallPlan struct {
	RequestedAt        time.Time
	TargetMessageCount int
	TargetOffset       int
	Windows            []recallWindow
}

// recallWindow represents a half-open message offset range [StartOffset, EndOffset).
type recallWindow struct {
	StartOffset int
	EndOffset   int
}

var maxRecallWindowMessages = constants.RecallWindowMessages
var maxRecallWindowTokens = constants.RecallWindowTokens
var maxRecallMergeSummaries = constants.RecallMergeSummaries
var maxRecallMergeTokens = constants.RecallMergeTokens

// SummaryFromStorage converts a persisted summary into active summary state.
func SummaryFromStorage(summary storage.SessionSummary) *SummaryState {
	if summary.SessionID == "" || summary.SessionSummary == "" {
		return nil
	}

	cloned := storage.CloneSessionSummary(summary)
	return &SummaryState{
		SessionID:          cloned.SessionID,
		SourceEndOffset:    cloned.SourceEndOffset,
		SourceMessageCount: cloned.SourceMessageCount,
		UpdatedAt:          cloned.UpdatedAt,
		SessionSummary:     cloned.SessionSummary,
		CurrentTask:        cloned.CurrentTask,
		Discoveries:        cloned.Discoveries,
		OpenQuestions:      cloned.OpenQuestions,
		NextActions:        cloned.NextActions,
	}
}

// MaybeRefreshSummary evaluates whether automatic compaction should run for the
// current session state and refreshes summary state when the configured
// thresholds are met.
func (s *Service) MaybeRefreshSummary(ctx context.Context, state *State, input RefreshInput) error {
	return s.refreshSummaryState(ctx, state, input, false, refreshPlan{})
}

// refreshSummaryState runs the authoritative compaction flow for a session.
//
// In automatic mode it:
//   - counts live messages
//   - computes the retained-tail target
//   - skips work when the evaluator does not trigger
//   - transitions compaction state through pending/running/succeeded|failed
//   - generates and persists a summary covering the target prefix
//
// In forced mode it skips the evaluator and uses the supplied plan.
//
// Diagram:
//
//	Example: 12 live messages, retained tail = 3
//
//	+---- summarized and persisted ----+--- kept live -------------+
//	| m1 | m2 | m3 | m4 | m5 | m6 | m7 | m8 | m9 | m10 | m11 | m12 |
//	+----------------------------------+---------------------------+
//	^                                            ^                 ^
//	offset 0                                     targetOffset=9    totalCount=12
//
//	state flow
//	pending -> running -> refreshSummary(targetOffset) -> save summary -> succeeded
func (s *Service) refreshSummaryState(
	ctx context.Context,
	state *State,
	input RefreshInput,
	force bool,
	forcedPlan refreshPlan,
) error {
	if state == nil || input.TraceSession == nil {
		return nil
	}

	if s == nil {
		return errors.New("summary service is required")
	}

	if s.modelClient == nil {
		return errors.New("model client is required")
	}

	if s.store == nil {
		return errors.New("summary store is required")
	}

	auto := !force
	if !force && !s.compactionOn {
		return nil
	}

	totalCount, err := s.store.CountMessages(ctx, input.SessionID, storage.MessageQueryOptions{})
	if err != nil {
		input.TraceSession.Record(trace.EvtContextCompactionFailed, buildCompactionTracePayloadWithAuto(
			input.SessionID,
			storage.SessionCompaction{Status: storage.CompactionStatusFailed},
			err.Error(),
			auto),
		)
		return err
	}

	recentTail := s.recentTail
	if totalCount <= recentTail {
		return nil
	}

	// Automatic compaction always retains the configured recent tail. Forced
	// compaction receives its target from SummarizeSession.
	plan := forcedPlan
	if !force {
		targetOffset := totalCount - recentTail
		plan = refreshPlan{
			RequestedAt:        s.currentTime(),
			TargetMessageCount: totalCount,
			TargetOffset:       targetOffset,
			Auto:               true,
		}
	}

	existingSummaryEndOffset := 0
	if state.Current != nil && state.Current.SourceEndOffset > existingSummaryEndOffset {
		existingSummaryEndOffset = state.Current.SourceEndOffset
	}

	// If a previous compaction already wrote the summary but crashed before
	// marking the session succeeded, reconcile status instead of summarizing again.
	if state.Current != nil && state.Current.SourceEndOffset >= plan.TargetOffset {
		session, ok, err := s.store.Get(ctx, input.SessionID, storage.SessionGetOptions{})
		if err != nil {
			input.TraceSession.Record(trace.EvtContextCompactionFailed, buildCompactionTracePayloadWithAuto(input.SessionID, storage.SessionCompaction{
				Status:             storage.CompactionStatusFailed,
				TargetMessageCount: totalCount,
				TargetOffset:       plan.TargetOffset,
			}, err.Error(), plan.Auto))
			return err
		}
		if !ok {
			err = errors.New("session not found")
			input.TraceSession.Record(trace.EvtContextCompactionFailed, buildCompactionTracePayloadWithAuto(input.SessionID, storage.SessionCompaction{
				Status:             storage.CompactionStatusFailed,
				TargetMessageCount: totalCount,
				TargetOffset:       plan.TargetOffset,
			}, err.Error(), plan.Auto))
			return err
		}

		return s.reconcileCompactionSucceeded(ctx, &session, plan, input.TraceSession)
	}

	// In automatic mode, persisted history may be compactable but the actual
	// request may still be below threshold, so skip until the evaluator triggers.
	if !force {
		estimate := s.evaluator.Evaluate(input.Request, input.Anchor)
		if !estimate.Triggered() {
			return nil
		}
	}

	session, ok, err := s.store.Get(ctx, input.SessionID, storage.SessionGetOptions{})
	if err != nil {
		input.TraceSession.Record(trace.EvtContextCompactionFailed, buildCompactionTracePayloadWithAuto(input.SessionID, storage.SessionCompaction{
			Status:             storage.CompactionStatusFailed,
			TargetMessageCount: totalCount,
			TargetOffset:       plan.TargetOffset,
		}, err.Error(), plan.Auto))
		return err
	}
	if !ok {
		err = errors.New("session not found")
		input.TraceSession.Record(trace.EvtContextCompactionFailed, buildCompactionTracePayloadWithAuto(input.SessionID, storage.SessionCompaction{
			Status:             storage.CompactionStatusFailed,
			TargetMessageCount: totalCount,
			TargetOffset:       plan.TargetOffset,
		}, err.Error(), plan.Auto))
		return err
	}

	log.Info().
		Str("session_id", input.SessionID).
		Str("trigger_source", getCompactionTriggerSource(force)).
		Int("existing_summary_end_offset", existingSummaryEndOffset).
		Int("messages_to_summarize", max(plan.TargetOffset-existingSummaryEndOffset, 0)).
		Int("tail_messages_retained", recentTail).
		Int("target_offset", plan.TargetOffset).
		Int("total_messages", plan.TargetMessageCount).
		Msg("compaction plan created")

	// Persist status transitions before doing model work so live clients and
	// hydration can show in-progress compaction.
	if err := s.transitionCompactionPending(ctx, &session, plan, input.TraceSession); err != nil {
		input.TraceSession.Record(trace.EvtContextCompactionFailed, buildCompactionTracePayloadWithAuto(input.SessionID, storage.SessionCompaction{
			Status:             storage.CompactionStatusFailed,
			TargetMessageCount: plan.TargetMessageCount,
			TargetOffset:       plan.TargetOffset,
		}, err.Error(), plan.Auto))
		return err
	}

	if err := s.transitionCompactionRunning(ctx, &session, plan, input.TraceSession); err != nil {
		input.TraceSession.Record(trace.EvtContextCompactionFailed, buildCompactionTracePayloadWithAuto(input.SessionID, storage.SessionCompaction{
			RequestedAt:        plan.RequestedAt,
			Status:             storage.CompactionStatusFailed,
			TargetMessageCount: plan.TargetMessageCount,
			TargetOffset:       plan.TargetOffset,
		}, err.Error(), plan.Auto))
		return err
	}

	// refreshSummary performs the model call and writes the new summary. Any
	// failure after "running" is reflected into durable compaction status.
	if _, err := s.refreshSummary(ctx, state, input, plan, true); err != nil {
		if transErr := s.transitionCompactionFailed(ctx, &session, plan, err, input.TraceSession); transErr != nil {
			wrapped := fmt.Errorf("mark compaction failed: %w", transErr)
			input.TraceSession.Record(trace.EvtContextCompactionFailed, buildCompactionTracePayloadWithAuto(input.SessionID, storage.SessionCompaction{
				RequestedAt:        plan.RequestedAt,
				StartedAt:          session.Compaction.StartedAt,
				Status:             storage.CompactionStatusFailed,
				TargetMessageCount: plan.TargetMessageCount,
				TargetOffset:       plan.TargetOffset,
			}, wrapped.Error(), plan.Auto))
			return wrapped
		}

		return err
	}

	if err := s.transitionCompactionSucceeded(ctx, &session, plan, input.TraceSession); err != nil {
		input.TraceSession.Record(trace.EvtContextCompactionFailed, buildCompactionTracePayloadWithAuto(input.SessionID, storage.SessionCompaction{
			RequestedAt:        plan.RequestedAt,
			StartedAt:          session.Compaction.StartedAt,
			Status:             storage.CompactionStatusFailed,
			TargetMessageCount: plan.TargetMessageCount,
			TargetOffset:       plan.TargetOffset,
		}, err.Error(), plan.Auto))
		return err
	}

	return nil
}

// CompactSession generates and persists the authoritative session summary using
// the default retained-tail compaction behavior.
func (s *Service) CompactSession(
	ctx context.Context,
	session storage.Session,
	traceSession traceRecorder,
) (*SummaryState, error) {
	return s.SummarizeSession(ctx, session, SummarizeSessionOptions{}, traceSession)
}

// RecallSessionSummary generates a zero-tail summary for the session and
// returns it without persisting summary or compaction state.
//
// It uses any existing authoritative summary as a starting point, plans bounded
// recall windows over the unsummarized remainder, summarizes those windows, and
// merges them into one recall result.
//
// Diagram:
//
//	Example: authoritative summary already covers m1..m4
//
//	already covered [0,4):
//	+----+----+----+----+
//	| m1 | m2 | m3 | m4 |
//	+----+----+----+----+
//
//	recall target [4,12):
//	+----+----+----+----+----+-----+-----+-----+
//	| m5 | m6 | m7 | m8 | m9 | m10 | m11 | m12 |
//	+----+----+----+----+----+-----+-----+-----+
//
//	planned newest-first windows:
//	window 1 [8,12):
//	+----+-----+-----+-----+
//	| m9 | m10 | m11 | m12 |
//	+----+-----+-----+-----+
//
//	window 2 [6,8):
//	+----+----+
//	| m7 | m8 |
//	+----+----+
//
//	window 3 [4,6):
//	+----+----+
//	| m5 | m6 |
//	+----+----+
//
//	then:
//	window summaries -> merged recall summary
func (s *Service) RecallSessionSummary(
	ctx context.Context,
	session storage.Session,
	traceSession traceRecorder,
) (*SummaryState, error) {
	if s == nil {
		return nil, errors.New("summary service is required")
	}

	if s.modelClient == nil {
		return nil, errors.New("model client is required")
	}

	if s.store == nil {
		return nil, errors.New("summary store is required")
	}

	if traceSession == nil {
		return nil, errors.New("trace session is required")
	}

	state, err := s.Load(ctx, session.ID)
	if err != nil {
		return nil, err
	}

	totalCount, err := s.store.CountMessages(ctx, session.ID, storage.MessageQueryOptions{})
	if err != nil {
		traceSession.Record(trace.EvtContextCompactionFailed, buildCompactionTracePayload(
			session.ID,
			storage.SessionCompaction{Status: storage.CompactionStatusFailed},
			err.Error()),
		)
		return nil, err
	}

	plan, err := s.planRecallSummary(ctx, session.ID, state, totalCount)
	if err != nil {
		traceSession.Record(trace.EvtContextCompactionFailed, buildCompactionTracePayload(
			session.ID,
			storage.SessionCompaction{Status: storage.CompactionStatusFailed},
			err.Error()),
		)
		return nil, err
	}

	summary, err := s.refreshRecallSummary(ctx, state, RefreshInput{
		SessionID:    session.ID,
		TraceSession: traceSession,
	}, plan)
	if err != nil {
		return nil, err
	}

	return summary, nil
}

// SummarizeSession generates and persists the authoritative session summary
// using the provided planner and retained-tail options.
//
// This is the configurable persisted-summary entry point. Unlike
// RecallSessionSummary, it updates the stored summary and compaction state.
func (s *Service) SummarizeSession(
	ctx context.Context,
	session storage.Session,
	opts SummarizeSessionOptions,
	traceSession traceRecorder,
) (*SummaryState, error) {
	if s == nil {
		return nil, errors.New("summary service is required")
	}

	if s.modelClient == nil {
		return nil, errors.New("model client is required")
	}

	if s.store == nil {
		return nil, errors.New("summary store is required")
	}

	if traceSession == nil {
		return nil, errors.New("trace session is required")
	}

	state, err := s.Load(ctx, session.ID)
	if err != nil {
		return nil, err
	}

	totalCount, err := s.store.CountMessages(ctx, session.ID, storage.MessageQueryOptions{})
	if err != nil {
		traceSession.Record(trace.EvtContextCompactionFailed, buildCompactionTracePayload(
			session.ID,
			storage.SessionCompaction{Status: storage.CompactionStatusFailed},
			err.Error()),
		)
		return nil, err
	}

	plan, err := s.summarizeSessionPlan(totalCount, opts)
	if err != nil {
		traceSession.Record(trace.EvtContextCompactionFailed, buildCompactionTracePayload(
			session.ID,
			storage.SessionCompaction{Status: storage.CompactionStatusFailed},
			err.Error()),
		)
		return nil, err
	}

	if err := s.refreshSummaryState(ctx, state, RefreshInput{
		SessionID:    session.ID,
		TraceSession: traceSession,
	}, true, plan); err != nil {
		return nil, err
	}

	return state.Current, nil
}

// summarizeSessionPlan computes the persisted compaction target for a forced summary request.
func (s *Service) summarizeSessionPlan(totalCount int, opts SummarizeSessionOptions) (refreshPlan, error) {
	planner := opts.Planner
	if planner == "" {
		planner = SessionSummaryPlannerRetainRecentTail
	}

	switch planner {
	case SessionSummaryPlannerRetainRecentTail:
		retainedTailMessages := s.recentTail
		if opts.RetainedTailMessages != nil {
			retainedTailMessages = *opts.RetainedTailMessages
		}

		if retainedTailMessages < 0 {
			return refreshPlan{}, errors.New("retained tail messages must be greater than or equal to zero")
		}
		if totalCount <= retainedTailMessages {
			return refreshPlan{}, errors.New("session history is too short to compact")
		}

		return refreshPlan{
			RequestedAt:        s.currentTime(),
			TargetMessageCount: totalCount,
			TargetOffset:       totalCount - retainedTailMessages,
		}, nil
	default:
		return refreshPlan{}, fmt.Errorf("unknown session summary planner: %s", planner)
	}
}

// planRecallSummary builds a recall-summary plan over the currently
// unsummarized portion of the session.
//
// If the existing authoritative summary already covers the full live session,
// it returns a plan with no windows so refreshRecallSummary can reuse that
// summary without recomputing anything.
//
// Diagram:
//
//	Example A: work still remains
//
//	covered by existing summary [0,3):
//	+----+----+----+
//	| m1 | m2 | m3 |
//	+----+----+----+
//
//	still needs recall planning [3,10):
//	+----+----+----+----+----+----+-----+
//	| m4 | m5 | m6 | m7 | m8 | m9 | m10 |
//	+----+----+----+----+----+----+-----+
//
//	Example B: full existing summary can be reused
//
//	already fully covered [0,10):
//	+----+----+----+----+----+----+----+----+----+-----+
//	| m1 | m2 | m3 | m4 | m5 | m6 | m7 | m8 | m9 | m10 |
//	+----+----+----+----+----+----+----+----+----+-----+
//
//	startOffset == totalCount == 10 -> no windows, reuse existing summary
func (s *Service) planRecallSummary(
	ctx context.Context,
	sessionID string,
	state *State,
	totalCount int) (recallPlan, error) {
	if totalCount <= 0 {
		return recallPlan{}, errors.New("session history is too short to compact")
	}

	startOffset := 0
	if state != nil && state.Current != nil && state.Current.SourceEndOffset > startOffset {
		startOffset = state.Current.SourceEndOffset
	}

	if startOffset >= totalCount {
		if state != nil && state.Current != nil &&
			state.Current.SourceEndOffset == totalCount &&
			state.Current.SourceMessageCount == totalCount {
			return recallPlan{
				RequestedAt:        s.currentTime(),
				TargetMessageCount: totalCount,
				TargetOffset:       totalCount,
			}, nil
		}

		return recallPlan{}, errors.New("session history is too short to compact")
	}

	windows, err := s.planRecallWindows(ctx, sessionID, state, startOffset, totalCount)
	if err != nil {
		return recallPlan{}, err
	}

	return recallPlan{
		RequestedAt:        s.currentTime(),
		TargetMessageCount: totalCount,
		TargetOffset:       totalCount,
		Windows:            windows,
	}, nil
}

// planRecallWindows partitions the unsummarized recall range into bounded
// windows, planning from newest messages backward.
//
// Each window is constrained by:
//   - maxRecallWindowMessages
//   - maxRecallWindowTokens using rough prompt estimation
//
// The planner first takes the largest message-count candidate ending at the
// current tail, then moves the left edge rightward until the token estimate
// fits. That makes the window as recent and as large as possible under both
// caps.
//
// Diagram:
//
//	Example: startOffset=2, currentEnd=10, maxRecallWindowMessages=4
//
//	already covered [0,2):
//	+----+----+
//	| m1 | m2 |
//	+----+----+
//
//	still open [2,10):
//	+----+----+----+----+----+----+----+-----+
//	| m3 | m4 | m5 | m6 | m7 | m8 | m9 | m10 |
//	+----+----+----+----+----+----+----+-----+
//
//	initial message-count candidate:
//	candidate [6,10):
//	+----+----+----+-----+
//	| m7 | m8 | m9 | m10 |
//	+----+----+----+-----+
//
//	if token estimate is too large, shrink from the left:
//	accepted window [7,10):
//	+----+----+-----+
//	| m8 | m9 | m10 |
//	+----+----+-----+
//
//	then emit [7,10) and continue planning [2,7)
func (s *Service) planRecallWindows(
	ctx context.Context,
	sessionID string,
	state *State,
	startOffset int,
	targetOffset int,
) ([]recallWindow, error) {
	if targetOffset <= startOffset {
		return nil, errors.New("session history is too short to compact")
	}

	baseInstructions := buildRecallChunkInstructions(state, 1, 1).String()
	currentEnd := targetOffset
	windows := make([]recallWindow, 0, max((targetOffset-startOffset+maxRecallWindowMessages-1)/maxRecallWindowMessages, 1))

	c := 0
	for currentEnd > startOffset {
		c++
		maxCount := min(maxRecallWindowMessages, currentEnd-startOffset)
		candidateStart := max(currentEnd-maxCount, startOffset)
		messages, err := s.store.GetMessages(ctx, sessionID, storage.MessageQueryOptions{
			Offset: candidateStart,
			Limit:  currentEnd - candidateStart,
		})
		if err != nil {
			return nil, err
		}
		if len(messages) == 0 {
			return nil, errors.New("recall window messages are required")
		}

		windowStartIndex := len(messages) - 1
		for idx := len(messages) - 2; idx >= 0; idx-- {
			candidate := messages[idx:]
			if estimateSummaryTokens(baseInstructions, candidate) > maxRecallWindowTokens {
				break
			}
			windowStartIndex = idx
		}

		windowStart := candidateStart + windowStartIndex
		windows = append(windows, recallWindow{
			StartOffset: windowStart,
			EndOffset:   currentEnd,
		})
		currentEnd = windowStart
	}

	return windows, nil
}

// refreshRecallSummary executes a recall-summary plan and returns a
// non-persisted recall summary.
//
// It records recall-specific trace events, reuses a full existing summary when
// the plan has no windows, summarizes each planned window otherwise, and then
// synthesizes the intermediate results into one final summary.
//
// Diagram:
//
//	+-------------------- recall plan --------------------+
//	| windows = [] and full summary still matches live    |
//	+-----------------------------------------------------+
//	                         |
//	                         v
//	              clone existing summary and return
//
//	+-------------------- recall plan --------------------+
//	| windows = [w1, w2, w3]                              |
//	+-----------------------------------------------------+
//	                         |
//	                         v
//	     summarize w1 -> summarize w2 -> summarize w3 -> merge -> return
func (s *Service) refreshRecallSummary(
	ctx context.Context,
	state *State,
	input RefreshInput,
	plan recallPlan,
) (*SummaryState, error) {
	payload := buildSummaryTracePayload(input.SessionID, plan.TargetOffset, plan.TargetMessageCount, plan.RequestedAt)
	input.TraceSession.Record(trace.EvtRecallSummaryRequested, payload)

	if len(plan.Windows) == 0 {
		if state != nil && state.Current != nil &&
			state.Current.SourceEndOffset == plan.TargetOffset &&
			state.Current.SourceMessageCount == plan.TargetMessageCount {
			summary := cloneSummaryState(state.Current)
			input.TraceSession.Record(trace.EvtRecallSummarySaved, buildSummaryTracePayload(
				summary.SessionID,
				summary.SourceEndOffset,
				summary.SourceMessageCount,
				summary.UpdatedAt,
			))
			return summary, nil
		}

		err := errors.New("recall windows are required")
		input.TraceSession.Record(trace.EvtRecallSummaryFailed, summaryTracePayloadWithError(payload, err.Error()))
		return nil, err
	}

	chunkSummaries := make([]*SummaryState, 0, len(plan.Windows))
	for idx, window := range plan.Windows {
		summary, err := s.summarizeRecallWindow(ctx, state, input.SessionID, plan, window, idx+1, len(plan.Windows))
		if err != nil {
			input.TraceSession.Record(trace.EvtRecallSummaryFailed, summaryTracePayloadWithError(payload, err.Error()))
			return nil, err
		}
		chunkSummaries = append(chunkSummaries, summary)
	}

	finalSummary, err := s.synthesizeRecallSummaries(ctx, state, input.SessionID, plan, chunkSummaries)
	if err != nil {
		input.TraceSession.Record(trace.EvtRecallSummaryFailed, summaryTracePayloadWithError(payload, err.Error()))
		return nil, err
	}

	if state != nil {
		state.Current = cloneSummaryState(finalSummary)
	}

	input.TraceSession.Record(trace.EvtRecallSummarySaved, buildSummaryTracePayload(
		finalSummary.SessionID,
		finalSummary.SourceEndOffset,
		finalSummary.SourceMessageCount,
		finalSummary.UpdatedAt,
	))

	return finalSummary, nil
}

// summarizeRecallWindow summarizes one bounded recall window.
//
// If the structured message window still exceeds the recall token budget, it
// falls back to summarizeOversizedRecallWindow, which renders the window to
// text, chunks it, summarizes each chunk, and merges the chunk summaries back
// into a single window summary.
func (s *Service) summarizeRecallWindow(
	ctx context.Context,
	state *State,
	sessionID string,
	plan recallPlan,
	window recallWindow,
	windowIndex int,
	windowCount int,
) (*SummaryState, error) {
	messages, err := s.store.GetMessages(ctx, sessionID, storage.MessageQueryOptions{
		Offset: window.StartOffset,
		Limit:  window.EndOffset - window.StartOffset,
	})
	if err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		return nil, errors.New("recall window messages are required")
	}

	instructions := buildRecallChunkInstructions(state, windowIndex, windowCount).String()
	if estimateSummaryTokens(instructions, messages) > maxRecallWindowTokens {
		return s.summarizeOversizedRecallWindow(
			ctx,
			state,
			sessionID,
			plan,
			window,
			windowIndex,
			windowCount,
			messages,
		)
	}

	resp, err := s.generateSummaryResponse(ctx, models.Request{
		Model:            s.summaryModel,
		API:              s.api,
		Instructions:     instructions,
		Messages:         morphmsg.CloneMessages(messages),
		StructuredOutput: summaryStructuredOutput,
		DebugRequests:    s.debugRequests,
	})
	if err != nil {
		return nil, err
	}

	return parseSummaryResponse(
		sessionID,
		window.EndOffset,
		plan.TargetMessageCount,
		resp,
		plan.RequestedAt,
	)
}

// summarizeOversizedRecallWindow handles the case where even a bounded recall
// window is too large to summarize in one request.
//
// It renders the window to plain text, splits that text into deterministic
// chunks, summarizes each chunk separately, and then synthesizes those chunk
// summaries into one summary representing the original window.
//
// Diagram:
//
//	Example: one recall window contains 3 very large messages
//
//	oversized window [7,10):
//	+-----------+-----------+------------+
//	| m8 (huge) | m9 (huge) | m10 (huge) |
//	+-----------+-----------+------------+
//	                         |
//	                         v
//	            renderRecallWindowPrompt(messages)
//	                         |
//	                         v
//	+--------- chunk 1 ---------+ +--------- chunk 2 ---------+ +--- chunk 3 ---+
//	| first text slice          | | middle text slice         | | final slice   |
//	+---------------------------+ +---------------------------+ +---------------+
//	             |                             |                         |
//	             v                             v                         v
//	          sum 1                         sum 2                     sum 3
//	             \                             |                         /
//	              \                            |                        /
//	               +------ synthesizeSummaryStates(...) ---------------+
//	                                      |
//	                                      v
//	                            one summary for the window
func (s *Service) summarizeOversizedRecallWindow(
	ctx context.Context,
	state *State,
	sessionID string,
	plan recallPlan,
	window recallWindow,
	windowIndex int,
	windowCount int,
	messages []morphmsg.Message,
) (*SummaryState, error) {
	chunks := splitRecallWindowChunks(renderRecallWindowPrompt(messages), getMaxRecallWindowChunkChars())
	if len(chunks) == 0 {
		return nil, errors.New("recall window chunks are required")
	}

	chunkSummaries := make([]*SummaryState, 0, len(chunks))
	for idx, chunk := range chunks {
		resp, err := s.generateSummaryResponse(ctx, models.Request{
			Model:            s.summaryModel,
			API:              s.api,
			Instructions:     buildRecallChunkTextInstructions(state, windowIndex, windowCount, idx+1, len(chunks)).String(),
			Messages:         []morphmsg.Message{{Role: morphmsg.RoleUser, Content: chunk}},
			StructuredOutput: summaryStructuredOutput,
			DebugRequests:    s.debugRequests,
		})
		if err != nil {
			return nil, err
		}

		summary, err := parseSummaryResponse(
			sessionID,
			window.EndOffset,
			plan.TargetMessageCount,
			resp,
			plan.RequestedAt,
		)
		if err != nil {
			return nil, err
		}

		chunkSummaries = append(chunkSummaries, summary)
	}

	return s.synthesizeSummaryStates(
		ctx,
		state,
		sessionID,
		window.EndOffset,
		plan.TargetMessageCount,
		plan.RequestedAt,
		chunkSummaries,
		func(batchIndex int, batchCount int) instruct.Instructions {
			return buildRecallSynthesisInstructions(state, batchIndex, batchCount)
		},
	)
}

// synthesizeRecallSummaries merges the per-window recall summaries into one
// final recall summary that covers the full recall target range.
func (s *Service) synthesizeRecallSummaries(
	ctx context.Context,
	state *State,
	sessionID string,
	plan recallPlan,
	summaries []*SummaryState,
) (*SummaryState, error) {
	return s.synthesizeSummaryStates(
		ctx,
		state,
		sessionID,
		plan.TargetOffset,
		plan.TargetMessageCount,
		plan.RequestedAt,
		summaries,
		func(batchIndex int, batchCount int) instruct.Instructions {
			return buildRecallSynthesisInstructions(state, batchIndex, batchCount)
		},
	)
}

// synthesizeSummaryStates reduces multiple intermediate summaries into one
// summary by repeatedly batching and summarizing summary-of-summary payloads.
//
// This is shared by:
//   - oversized recall-window chunk merging
//   - multi-window recall summary merging
//
// Diagram:
//
//	Example: 5 summaries remain, maxRecallMergeSummaries = 3, but the token
//	budget only allows 2 summaries in the first two merge requests.
//
//	batch 1:
//	+----+----+
//	| S1 | S2 |
//	+----+----+
//	   |
//	   v
//	  M1
//
//	batch 2:
//	+----+----+
//	| S3 | S4 |
//	+----+----+
//	   |
//	   v
//	  M2
//
//	batch 3:
//	+----+
//	| S5 |
//	+----+
//	   |
//	   v
//	  M3
//
//	second merge pass:
//	now only 3 merged summaries remain, and they fit in one request:
//
//	+----+----+----+
//	| M1 | M2 | M3 |
//	+----+----+----+
//	   |
//	   v
//	 FINAL
func (s *Service) synthesizeSummaryStates(
	ctx context.Context,
	state *State,
	sessionID string,
	sourceEndOffset int,
	sourceMessageCount int,
	requestedAt time.Time,
	summaries []*SummaryState,
	instructions func(batchIndex int, batchCount int) instruct.Instructions,
) (*SummaryState, error) {
	current := cloneSummaryStates(summaries)
	if len(current) == 0 {
		return nil, errors.New("recall chunk summaries are required")
	}

	for len(current) > 1 {
		batches := getPlannedRecallSummaryBatches(state, current)
		next := make([]*SummaryState, 0, len(batches))
		for idx, batch := range batches {
			// Each batch is summarized as text because the model is merging
			// summary records, not original conversation messages.
			resp, err := s.generateSummaryResponse(ctx, models.Request{
				Model:        s.summaryModel,
				API:          s.api,
				Instructions: instructions(idx+1, len(batches)).String(),
				Messages: []morphmsg.Message{
					{Role: morphmsg.RoleUser, Content: renderRecallSummaryBatch(batch)},
				},
				StructuredOutput: summaryStructuredOutput,
				DebugRequests:    s.debugRequests,
			})
			if err != nil {
				return nil, err
			}

			summary, err := parseSummaryResponse(
				sessionID,
				sourceEndOffset,
				sourceMessageCount,
				resp,
				requestedAt,
			)
			if err != nil {
				return nil, err
			}

			next = append(next, summary)
		}
		current = next
	}

	finalSummary := cloneSummaryState(current[0])
	finalSummary.SourceEndOffset = sourceEndOffset
	finalSummary.SourceMessageCount = sourceMessageCount
	finalSummary.UpdatedAt = requestedAt
	return finalSummary, nil
}

// getPlannedRecallSummaryBatches groups intermediate summaries into bounded merge
// batches for synthesizeSummaryStates.
//
// Each batch is limited by:
//   - maxRecallMergeSummaries
//   - maxRecallMergeTokens using rough prompt estimation
//
// Diagram:
//
//	Example: 5 summaries remain, maxRecallMergeSummaries = 3, but the token
//	budget only allows 2 summaries in the first two merge requests.
//
//	input summaries:
//	+----+----+----+----+----+
//	| S1 | S2 | S3 | S4 | S5 |
//	+----+----+----+----+----+
//
//	first pass batching:
//	+----+----+    +----+----+    +----+
//	| S1 | S2 |    | S3 | S4 |    | S5 |
//	+----+----+    +----+----+    +----+
//
//	output:
//	[][]*SummaryState{
//	  {S1, S2},
//	  {S3, S4},
//	  {S5},
//	}
func getPlannedRecallSummaryBatches(state *State, summaries []*SummaryState) [][]*SummaryState {
	batches := make([][]*SummaryState, 0, max((len(summaries)+maxRecallMergeSummaries-1)/maxRecallMergeSummaries, 1))
	remaining := cloneSummaryStates(summaries)
	instructions := buildRecallSynthesisInstructions(state, 1, 1).String()

	for len(remaining) > 0 {
		batchSize := 1
		for batchSize < len(remaining) && batchSize < maxRecallMergeSummaries {
			candidateSize := batchSize + 1
			candidate := remaining[:candidateSize]
			if estimateSummaryTokens(instructions, []morphmsg.Message{{
				Role:    morphmsg.RoleUser,
				Content: renderRecallSummaryBatch(candidate),
			}}) > maxRecallMergeTokens {
				break
			}
			batchSize = candidateSize
		}

		batches = append(batches, cloneSummaryStates(remaining[:batchSize]))
		remaining = remaining[batchSize:]
	}

	return batches
}

// buildRecallChunkInstructions builds the prompt for summarizing one recall window.
func buildRecallChunkInstructions(state *State, windowIndex int, windowCount int) instruct.Instructions {
	instructions := instruct.BuildRecallSessionSummaryWindow(windowIndex, windowCount)

	if state == nil {
		return instructions
	}

	if summaryInstructions, ok := state.RenderSummaryInstructions(); ok {
		return instruct.New(summaryInstructions).Append(instructions...)
	}

	return instructions
}

// buildRecallSynthesisInstructions builds the prompt for merging recall summaries.
func buildRecallSynthesisInstructions(state *State, batchIndex int, batchCount int) instruct.Instructions {
	instructions := instruct.BuildRecallSessionSummarySynthesis(batchIndex, batchCount)

	if state == nil {
		return instructions
	}

	if summaryInstructions, ok := state.RenderSummaryInstructions(); ok {
		return instruct.New(summaryInstructions).Append(instructions...)
	}

	return instructions
}

// buildRecallChunkTextInstructions builds the prompt for one oversized-window text chunk.
func buildRecallChunkTextInstructions(
	state *State,
	windowIndex int,
	windowCount int,
	chunkIndex int,
	chunkCount int,
) instruct.Instructions {
	instructions := instruct.BuildRecallSessionSummaryChunk(windowIndex, windowCount, chunkIndex, chunkCount)

	if state == nil {
		return instructions
	}

	if summaryInstructions, ok := state.RenderSummaryInstructions(); ok {
		return instruct.New(summaryInstructions).Append(instructions...)
	}

	return instructions
}

// renderRecallWindowPrompt converts structured messages into a plain-text
// transcript block for oversized-window chunking.
//
// The output preserves role, optional name, optional tool call id, message
// content, and tool-call details so the chunk summarizer still sees a readable
// transcript after window rendering.
//
// Diagram:
//
//	Message 1
//	Role: user
//	Content:
//	Find the earlier deployment note.
//
//	---
//
//	Message 2
//	Role: assistant
//	Name: planner
//	Tool Calls:
//	Name: search_files
//	Input: {"query":"deployment note"}
func renderRecallWindowPrompt(messages []morphmsg.Message) string {
	if len(messages) == 0 {
		return ""
	}

	sections := make([]string, 0, len(messages))
	for idx, message := range messages {
		section := fmt.Sprintf("Message %d\nRole: %s", idx+1, message.Role)
		nameValue := str.String(message.Name)
		if name := nameValue.Trim(); name != "" {
			section += "\nName: " + name
		}
		toolCallIDValue := str.String(message.ToolCallID)
		if toolCallID := toolCallIDValue.Trim(); toolCallID != "" {
			section += "\nTool Call ID: " + toolCallID
		}
		contentValue := str.String(message.Content)
		if content := contentValue.Trim(); content != "" {
			section += "\nContent:\n" + content
		}
		if len(message.ToolCalls) > 0 {
			toolLines := make([]string, 0, len(message.ToolCalls))
			for _, toolCall := range message.ToolCalls {
				nameValue2 := str.String(toolCall.Name)
				line := "Name: " + nameValue2.Trim()
				inputValue := str.String(toolCall.Input)
				if input := inputValue.Trim(); input != "" {
					line += "\nInput: " + input
				}
				toolLines = append(toolLines, line)
			}
			section += "\nTool Calls:\n" + strings.Join(toolLines, "\n\n")
		}
		sections = append(sections, section)
	}

	return strings.Join(sections, "\n\n---\n\n")
}

// splitRecallWindowChunks splits rendered recall text into deterministic
// character-bounded chunks.
//
// This is a fallback for windows that remain too large after message-window
// planning. Chunking is done on rune boundaries so multi-byte characters are
// not split mid-sequence.
func splitRecallWindowChunks(content string, chunkChars int) []string {
	contentValue2 := str.String(content)
	content = contentValue2.Trim()
	if content == "" {
		return nil
	}

	if chunkChars <= 0 {
		return []string{content}
	}

	runes := []rune(content)
	chunks := make([]string, 0, (len(runes)+chunkChars-1)/chunkChars)
	for start := 0; start < len(runes); start += chunkChars {
		end := min(start+chunkChars, len(runes))
		trimmedValue := str.String(string(runes[start:end]))
		chunk := trimmedValue.Trim()
		if chunk == "" {
			continue
		}
		chunks = append(chunks, chunk)
	}

	return chunks
}

// getMaxRecallWindowChunkChars converts the recall token budget into a coarse
// character budget for oversized-window chunking.
//
// The heuristic follows the shared rough estimate used elsewhere in compaction:
// roughly 4 characters per token, with a minimum chunk size of 1 character.
func getMaxRecallWindowChunkChars() int {
	return max(compaction.EstimateCharsFromTokensRough(maxRecallWindowTokens), 1)
}

// renderRecallSummaryBatch serializes intermediate summaries into a single user
// message so the model can synthesize them into a higher-level summary.
func renderRecallSummaryBatch(summaries []*SummaryState) string {
	sections := make([]string, 0, len(summaries)+1)
	sections = append(sections, "Recall window summaries are ordered from most recent to oldest.")
	for idx, summary := range summaries {
		raw, _ := json.Marshal(summaryPayload{
			SessionSummary: summary.SessionSummary,
			CurrentTask:    summary.CurrentTask,
			Discoveries:    summary.Discoveries,
			OpenQuestions:  summary.OpenQuestions,
			NextActions:    summary.NextActions,
		})
		sections = append(sections, fmt.Sprintf("Recall Window Summary %d:\n%s", idx+1, string(raw)))
	}

	return strings.Join(sections, "\n\n")
}

// parseSummaryResponse parses a model response into SummaryState, first using
// the structured JSON path and then falling back to plain-text summary capture
// when JSON parsing fails for non-empty output.
func parseSummaryResponse(
	sessionID string,
	sourceEndOffset int,
	sourceMessageCount int,
	resp *models.Response,
	requestedAt time.Time,
) (*SummaryState, error) {
	if resp == nil {
		return nil, errors.New("model response is required")
	}
	if resp.RequiresToolCalls {
		return nil, errors.New("summary requested tool calls")
	}

	summary, err := parseSummary(sessionID, sourceEndOffset, sourceMessageCount, resp.OutputText, requestedAt)
	if err == nil {
		return summary, nil
	}
	if errors.Is(err, errSummaryResponseEmpty) {
		return nil, err
	}

	summary, fallbackErr := buildFallbackSummary(sessionID, sourceEndOffset, sourceMessageCount, resp.OutputText, requestedAt)
	if fallbackErr != nil {
		return nil, fallbackErr
	}

	return summary, nil
}

// estimateSummaryTokens estimates token usage for a summary request.
func estimateSummaryTokens(instructions string, messages []morphmsg.Message) int {
	return compaction.EstimateRequestRough(models.Request{
		Instructions: instructions,
		Messages:     messages,
	})
}

// cloneSummaryState returns a detached copy of summary state.
func cloneSummaryState(summary *SummaryState) *SummaryState {
	if summary == nil {
		return nil
	}

	return SummaryFromStorage(storage.SessionSummary{
		SessionID:          summary.SessionID,
		SourceEndOffset:    summary.SourceEndOffset,
		SourceMessageCount: summary.SourceMessageCount,
		UpdatedAt:          summary.UpdatedAt,
		SessionSummary:     summary.SessionSummary,
		CurrentTask:        summary.CurrentTask,
		Discoveries:        summary.Discoveries,
		OpenQuestions:      summary.OpenQuestions,
		NextActions:        summary.NextActions,
	})
}

// cloneSummaryStates returns detached copies of summary state pointers.
func cloneSummaryStates(summaries []*SummaryState) []*SummaryState {
	if len(summaries) == 0 {
		return nil
	}

	cloned := make([]*SummaryState, 0, len(summaries))
	for _, summary := range summaries {
		cloned = append(cloned, cloneSummaryState(summary))
	}

	return cloned
}

// refreshSummary generates the authoritative compaction summary for the target
// prefix and optionally persists it.
//
// It:
//   - gathers the unsummarized prefix between the existing summary end and the
//     planned target offset
//   - requests a structured summary from the model
//   - falls back to plain text when structured parsing fails
//   - persists the result when persist=true
//   - updates state.Current
//
// Diagram:
//
//	Example: an older persisted summary already covers m1..m3
//
//	already in stored summary [0,3):
//	+----+----+----+
//	| m1 | m2 | m3 |
//	+----+----+----+
//
//	fetched and summarized now [3,8):
//	+----+----+----+----+----+
//	| m4 | m5 | m6 | m7 | m8 |
//	+----+----+----+----+----+
//
//	kept live outside the persisted summary target [8,10):
//	+----+-----+
//	| m9 | m10 |
//	+----+-----+
//
//	refreshSummary fetches m4..m8, generates one summary covering m1..m8,
//	and leaves m9..m10 outside the persisted summary target.
func (s *Service) refreshSummary(
	ctx context.Context,
	state *State,
	input RefreshInput,
	plan refreshPlan,
	persist bool,
) (*SummaryState, error) {
	payload := buildSummaryTracePayload(input.SessionID, plan.TargetOffset, plan.TargetMessageCount, plan.RequestedAt)
	input.TraceSession.Record(trace.EvtSummaryRequested, payload)

	summaryMessages := make([]morphmsg.Message, 0, plan.TargetOffset)
	instructions := instruct.BuildSessionSummary()
	if summaryInstructions, ok := state.RenderSummaryInstructions(); ok {
		instructions = instruct.New(summaryInstructions).Append(instructions...)
	}

	startOffset := 0
	if state.Current != nil && state.Current.SourceEndOffset > startOffset {
		startOffset = state.Current.SourceEndOffset
	}

	limit := plan.TargetOffset - startOffset
	if limit > 0 {
		messages, err := s.store.GetMessages(ctx, input.SessionID, storage.MessageQueryOptions{
			Limit:  limit,
			Offset: startOffset,
		})
		if err != nil {
			failedPayload := summaryTracePayloadWithError(payload, err.Error())
			input.TraceSession.Record(trace.EvtSummaryFailed, failedPayload)
			return nil, err
		}
		summaryMessages = append(summaryMessages, morphmsg.CloneMessages(messages)...)
	}
	summaryMessages = ctxbuilder.New().Build(ctxbuilder.Input{SessionHistory: summaryMessages})

	log.Debug().
		Str("session_id", input.SessionID).
		Int("start_offset", startOffset).
		Int("end_offset", plan.TargetOffset).
		Int("existing_summary_end_offset", startOffset).
		Int("messages_to_summarize", limit).
		Int("summary_messages", len(summaryMessages)).
		Msg("generating compaction summary")

	request := models.Request{
		Model:            s.summaryModel,
		API:              s.api,
		Instructions:     instructions.String(),
		Messages:         summaryMessages,
		Tools:            nil,
		StructuredOutput: summaryStructuredOutput,
		DebugRequests:    s.debugRequests,
	}
	resp, err := s.generateSummaryResponse(ctx, request)
	if err != nil {
		payload := summaryTracePayloadWithError(payload, err.Error())
		input.TraceSession.Record(trace.EvtSummaryFailed, payload)
		return nil, err
	}

	if resp == nil {
		err = errors.New("model response is required")
		payload := summaryTracePayloadWithError(payload, err.Error())
		input.TraceSession.Record(trace.EvtSummaryFailed, payload)
		return nil, err
	}

	if resp.RequiresToolCalls {
		err = errors.New("summary requested tool calls")
		payload := summaryTracePayloadWithError(payload, err.Error())
		input.TraceSession.Record(trace.EvtSummaryFailed, payload)
		return nil, err
	}

	summaryParsePath := "json"
	summary, err := parseSummary(
		input.SessionID,
		plan.TargetOffset,
		plan.TargetMessageCount,
		resp.OutputText,
		plan.RequestedAt,
	)
	if err != nil {
		if errors.Is(err, errSummaryResponseEmpty) {
			payload := summaryTracePayloadWithError(payload, err.Error())
			input.TraceSession.Record(trace.EvtSummaryFailed, payload)
			return nil, err
		}

		summaryParsePath = "plain_text_fallback"
		log.Warn().Str("session_id", input.SessionID).Err(err).Msg("structured summary parse failed, using fallback")

		input.TraceSession.Record(trace.EvtSummaryParseFailed, summaryTracePayloadWithError(payload, err.Error()))

		summary, err = buildFallbackSummary(
			input.SessionID,
			plan.TargetOffset,
			plan.TargetMessageCount,
			resp.OutputText,
			plan.RequestedAt,
		)
		if err != nil {
			payload := summaryTracePayloadWithError(payload, err.Error())
			input.TraceSession.Record(trace.EvtSummaryFailed, payload)
			return nil, err
		}
	}

	summaryRecord := storage.CloneSessionSummary(storage.SessionSummary{
		SessionID:          summary.SessionID,
		SourceEndOffset:    summary.SourceEndOffset,
		SourceMessageCount: summary.SourceMessageCount,
		UpdatedAt:          summary.UpdatedAt,
		SessionSummary:     summary.SessionSummary,
		CurrentTask:        summary.CurrentTask,
		Discoveries:        summary.Discoveries,
		OpenQuestions:      summary.OpenQuestions,
		NextActions:        summary.NextActions,
	})

	if persist {
		if err := s.store.SaveSummary(ctx, summaryRecord); err != nil {
			payload := summaryTracePayloadWithError(payload, err.Error())
			input.TraceSession.Record(trace.EvtSummaryFailed, payload)
			return nil, err
		}
	}

	state.Current = summary

	log.Info().
		Str("session_id", state.Current.SessionID).
		Str("summary_parse_path", summaryParsePath).
		Int("summarized_from_offset", startOffset).
		Int("source_end_offset", state.Current.SourceEndOffset).
		Int("source_message_count", state.Current.SourceMessageCount).
		Int("messages_summarized", max(state.Current.SourceEndOffset-startOffset, 0)).
		Int("tail_messages_retained", max(state.Current.SourceMessageCount-state.Current.SourceEndOffset, 0)).
		Msg("compaction summary saved")

	input.TraceSession.Record(trace.EvtSummarySaved, buildSummaryTracePayload(
		state.Current.SessionID,
		state.Current.SourceEndOffset,
		state.Current.SourceMessageCount,
		state.Current.UpdatedAt,
	))

	return state.Current, nil
}

// generateSummaryResponse sends a summary request through the summary client.
//
// It first tries the structured-output form. If that request fails and the
// caller asked for structured output, it retries once without the structured
// schema so callers still have a plain-text fallback path.
func (s *Service) generateSummaryResponse(ctx context.Context, request models.Request) (*models.Response, error) {
	if s == nil || s.summaryClient == nil {
		return nil, errors.New("model client is required")
	}

	summaryLog.Info().
		Str("provider", s.summaryProvider).
		Str("api", request.API).
		Str("model", request.Model).
		Int("message_count", len(request.Messages)).
		Bool("structured_output_request", request.StructuredOutput != nil).
		Bool("debug_requests", request.DebugRequests).
		Msg("compaction summary model request started")

	resp, err := s.summaryClient.Complete(ctx, request)
	if err == nil {
		event := summaryLog.Info().
			Str("provider", s.summaryProvider).
			Str("api", request.API).
			Str("model", request.Model).
			Bool("structured_output_request", request.StructuredOutput != nil)
		if resp != nil {
			event = event.
				Str("response_model", resp.Model).
				Int("prompt_tokens", resp.PromptTokens).
				Int("completion_tokens", resp.CompletionTokens).
				Int("total_tokens", resp.TotalTokens)
		} else {
			event = event.Str("error_kind", "missing_response")
		}
		event.Msg("compaction summary model request completed")
		return resp, nil
	}

	if request.StructuredOutput == nil {
		summaryLog.Warn().
			Err(err).
			Str("provider", s.summaryProvider).
			Str("api", request.API).
			Str("model", request.Model).
			Str("error_kind", getSummaryModelErrorKind(err)).
			Bool("structured_output_request", false).
			Msg("compaction summary model request failed")
		return nil, err
	}

	log.Warn().
		Err(err).
		Str("provider", s.summaryProvider).
		Str("api", request.API).
		Str("model", request.Model).
		Str("error_kind", getSummaryModelErrorKind(err)).
		Msg("structured summary request failed, retrying without structured output")

	fallback := request
	fallback.StructuredOutput = nil
	summaryLog.Info().
		Str("provider", s.summaryProvider).
		Str("api", fallback.API).
		Str("model", fallback.Model).
		Int("message_count", len(fallback.Messages)).
		Bool("structured_output_request", false).
		Bool("debug_requests", fallback.DebugRequests).
		Msg("compaction summary model retry started")

	resp, err = s.summaryClient.Complete(ctx, fallback)
	if err != nil {
		summaryLog.Warn().
			Err(err).
			Str("provider", s.summaryProvider).
			Str("api", fallback.API).
			Str("model", fallback.Model).
			Str("error_kind", getSummaryModelErrorKind(err)).
			Bool("structured_output_request", false).
			Msg("compaction summary model retry failed")
		return nil, err
	}

	event := summaryLog.Info().
		Str("provider", s.summaryProvider).
		Str("api", fallback.API).
		Str("model", fallback.Model).
		Bool("structured_output_request", false)
	if resp != nil {
		event = event.
			Str("response_model", resp.Model).
			Int("prompt_tokens", resp.PromptTokens).
			Int("completion_tokens", resp.CompletionTokens).
			Int("total_tokens", resp.TotalTokens)
	} else {
		event = event.Str("error_kind", "missing_response")
	}
	event.Msg("compaction summary model request completed after unstructured retry")

	return resp, nil
}

// getSummaryModelErrorKind classifies summary model failures for logs.
func getSummaryModelErrorKind(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "context_canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}

	value := strings.ToLower(err.Error())
	switch {
	case strings.Contains(value, "response is required"):
		return "missing_response"
	case strings.Contains(value, "json"):
		return "decode_failed"
	case strings.Contains(value, "timeout"):
		return "timeout"
	default:
		return "operation_failed"
	}
}

// transitionCompactionPending persists and traces the pending compaction state.
func (s *Service) transitionCompactionPending(
	ctx context.Context,
	session *storage.Session,
	plan refreshPlan,
	recorder traceRecorder,
) error {
	if session == nil {
		return errors.New("session is required")
	}

	session.Compaction = storage.SessionCompaction{
		RequestedAt:        plan.RequestedAt,
		Status:             storage.CompactionStatusPending,
		TargetMessageCount: plan.TargetMessageCount,
		TargetOffset:       plan.TargetOffset,
	}

	if err := s.store.Save(ctx, *session); err != nil {
		return err
	}

	recorder.Record(
		trace.EvtContextCompactionPending,
		buildCompactionTracePayloadWithAuto(session.ID, session.Compaction, "", plan.Auto))
	return nil
}

// transitionCompactionRunning persists and traces the running compaction state.
func (s *Service) transitionCompactionRunning(
	ctx context.Context,
	session *storage.Session,
	plan refreshPlan,
	recorder traceRecorder,
) error {
	if session == nil {
		return errors.New("session is required")
	}

	session.Compaction.StartedAt = s.currentTime()
	session.Compaction.Status = storage.CompactionStatusRunning
	session.Compaction.TargetMessageCount = plan.TargetMessageCount
	session.Compaction.TargetOffset = plan.TargetOffset

	if err := s.store.Save(ctx, *session); err != nil {
		return err
	}

	recorder.Record(
		trace.EvtContextCompactionRunning,
		buildCompactionTracePayloadWithAuto(session.ID, session.Compaction, "", plan.Auto))

	log.Debug().
		Str("session_id", session.ID).
		Int("target_offset", plan.TargetOffset).
		Int("target_message_count", plan.TargetMessageCount).
		Msg("compaction running")

	return nil
}

// transitionCompactionSucceeded persists and traces successful compaction state.
func (s *Service) transitionCompactionSucceeded(
	ctx context.Context,
	session *storage.Session,
	plan refreshPlan,
	recorder traceRecorder,
) error {
	if session == nil {
		return errors.New("session is required")
	}

	session.Compaction.CompletedAt = s.currentTime()
	session.Compaction.FailedAt = time.Time{}
	session.Compaction.LastError = ""
	session.Compaction.Status = storage.CompactionStatusSucceeded
	session.Compaction.TargetMessageCount = plan.TargetMessageCount
	session.Compaction.TargetOffset = plan.TargetOffset
	session.LastPromptTokens = 0

	if err := s.store.Save(ctx, *session); err != nil {
		return err
	}

	recorder.Record(trace.EvtContextCompactionSucceeded,
		buildCompactionTracePayloadWithAuto(session.ID, session.Compaction, "", plan.Auto))

	log.Info().
		Str("session_id", session.ID).
		Int("target_offset", plan.TargetOffset).
		Int("target_message_count", plan.TargetMessageCount).
		Msg("compaction completed")

	return nil
}

// reconcileCompactionSucceeded repairs durable compaction status after a completed summary.
func (s *Service) reconcileCompactionSucceeded(
	ctx context.Context,
	session *storage.Session,
	plan refreshPlan,
	recorder traceRecorder,
) error {
	if session == nil {
		return errors.New("session is required")
	}

	if session.Compaction.Status == storage.CompactionStatusSucceeded &&
		session.Compaction.TargetOffset >= plan.TargetOffset &&
		session.Compaction.TargetMessageCount >= plan.TargetMessageCount {
		return nil
	}

	if err := s.transitionCompactionSucceeded(ctx, session, plan, recorder); err != nil {
		recorder.Record(trace.EvtContextCompactionFailed, buildCompactionTracePayloadWithAuto(session.ID, storage.SessionCompaction{
			RequestedAt:        session.Compaction.RequestedAt,
			StartedAt:          session.Compaction.StartedAt,
			Status:             storage.CompactionStatusFailed,
			TargetMessageCount: plan.TargetMessageCount,
			TargetOffset:       plan.TargetOffset,
		}, err.Error(), plan.Auto))
		return err
	}

	return nil
}

// transitionCompactionFailed persists and traces failed compaction state.
func (s *Service) transitionCompactionFailed(
	ctx context.Context,
	session *storage.Session,
	plan refreshPlan,
	cause error,
	traceRecorder traceRecorder,
) error {
	if session == nil {
		return errors.New("session is required")
	}

	session.Compaction.CompletedAt = time.Time{}
	session.Compaction.FailedAt = s.currentTime()
	errorValue := str.String(cause.Error())
	session.Compaction.LastError = errorValue.Trim()
	session.Compaction.Status = storage.CompactionStatusFailed
	session.Compaction.TargetMessageCount = plan.TargetMessageCount
	session.Compaction.TargetOffset = plan.TargetOffset

	if err := s.store.Save(ctx, *session); err != nil {
		return err
	}

	log.Error().Str("session_id", session.ID).Str("cause", session.Compaction.LastError).Msg("compaction failed")

	traceRecorder.Record(trace.EvtContextCompactionFailed, buildCompactionTracePayloadWithAuto(
		session.ID,
		session.Compaction,
		session.Compaction.LastError,
		plan.Auto,
	))

	log.Warn().
		Str("session_id", session.ID).
		Int("target_offset", plan.TargetOffset).
		Int("target_message_count", plan.TargetMessageCount).
		Str("error", session.Compaction.LastError).
		Msg("compaction failed")

	return nil
}

// getCompactionTriggerSource renders a stable source label for logs and traces.
func getCompactionTriggerSource(force bool) string {
	if force {
		return "manual"
	}

	return "preflight_threshold_exceeded"
}

// currentTime returns the service clock in UTC.
func (s *Service) currentTime() time.Time {
	if s != nil && s.now != nil {
		now := s.now()
		if !now.IsZero() {
			return now.UTC()
		}
	}

	return time.Now().UTC()
}

// RecordSummaryApplied emits a trace event when summary context is applied to a request.
func (m *State) RecordSummaryApplied(traceSession trace.Session) {
	if m == nil || traceSession == nil || m.Current == nil {
		return
	}

	sessionSummaryValue := str.String(m.Current.SessionSummary)
	if sessionSummaryValue.Trim() == "" {
		return
	}

	traceSession.Record(trace.EvtSummaryApplied, buildSummaryTracePayload(
		m.Current.SessionID,
		m.Current.SourceEndOffset,
		m.Current.SourceMessageCount,
		m.Current.UpdatedAt,
	),
	)
}

// parseSummary parses structured summary JSON into SummaryState.
func parseSummary(
	sessionID string,
	sourceEndOffset,
	sourceMessageCount int,
	raw string,
	updatedAt time.Time,
) (*SummaryState, error) {
	raw = normalizeSummaryText(raw)
	if raw == "" {
		return nil, errSummaryResponseEmpty
	}

	var payload summaryPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, err
	}

	summary := SummaryFromStorage(storage.SessionSummary{
		SessionID:          sessionID,
		SourceEndOffset:    sourceEndOffset,
		SourceMessageCount: sourceMessageCount,
		UpdatedAt:          updatedAt,
		SessionSummary:     payload.SessionSummary,
		CurrentTask:        payload.CurrentTask,
		Discoveries:        payload.Discoveries,
		OpenQuestions:      payload.OpenQuestions,
		NextActions:        payload.NextActions,
	})
	if summary == nil {
		return nil, errors.New("session summary is required")
	}

	return summary, nil
}

var errSummaryResponseEmpty = errors.New("summary response is empty")

// buildFallbackSummary treats non-empty unstructured model output as the summary text.
func buildFallbackSummary(
	sessionID string,
	sourceEndOffset,
	sourceMessageCount int,
	raw string,
	updatedAt time.Time,
) (*SummaryState, error) {
	raw = normalizeSummaryText(raw)
	if raw == "" {
		return nil, errSummaryResponseEmpty
	}

	summary := SummaryFromStorage(storage.SessionSummary{
		SessionID:          sessionID,
		SourceEndOffset:    sourceEndOffset,
		SourceMessageCount: sourceMessageCount,
		UpdatedAt:          updatedAt,
		SessionSummary:     raw,
	})
	if summary == nil {
		return nil, errors.New("session summary is required")
	}

	return summary, nil
}

// normalizeSummaryText trims whitespace and Markdown fences.
func normalizeSummaryText(raw string) string {
	stripMarkdownFenceValue := str.String(stripMarkdownFence(raw))
	return stripMarkdownFenceValue.Trim()
}

// stripMarkdownFence removes one surrounding Markdown code fence if present.
func stripMarkdownFence(raw string) string {
	rawValue := str.String(raw)
	raw = rawValue.Trim()
	if !strings.HasPrefix(raw, "```") {
		return raw
	}

	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```JSON")
	raw = strings.TrimPrefix(raw, "```")
	rawValue2 := str.String(raw)
	raw = strings.TrimSuffix(rawValue2.Trim(), "```")
	rawValue3 := str.String(raw)
	return rawValue3.Trim()
}

// buildSummaryTracePayload creates the common summary trace payload.
func buildSummaryTracePayload(
	sessionID string,
	sourceEndOffset,
	sourceMessageCount int,
	updatedAt time.Time,
) trace.SummaryEventPayload {
	return trace.SummaryEventPayload{
		SessionID:          sessionID,
		SourceEndOffset:    sourceEndOffset,
		SourceMessageCount: sourceMessageCount,
		UpdatedAt:          updatedAt,
	}
}

// summaryTracePayloadWithError attaches an error to a summary trace payload.
func summaryTracePayloadWithError(base trace.SummaryEventPayload, failure string) trace.SummaryEventPayload {
	merged := base
	failureValue := str.String(failure)
	merged.Error = failureValue.Trim()
	return merged
}

// buildCompactionTracePayload creates a manual compaction trace payload.
func buildCompactionTracePayload(sessionID string, state storage.SessionCompaction, failure string) trace.CompactionEventPayload {
	return buildCompactionTracePayloadWithAuto(sessionID, state, failure, false)
}

// buildCompactionTracePayloadWithAuto creates a compaction trace payload with source metadata.
func buildCompactionTracePayloadWithAuto(
	sessionID string,
	state storage.SessionCompaction,
	failure string,
	auto bool,
) trace.CompactionEventPayload {
	payload := trace.CompactionEventPayload{
		SessionID:          sessionID,
		Status:             string(state.Status),
		Auto:               auto,
		TargetMessageCount: state.TargetMessageCount,
		TargetOffset:       state.TargetOffset,
	}
	if !state.RequestedAt.IsZero() {
		payload.RequestedAt = state.RequestedAt
	}
	if !state.StartedAt.IsZero() {
		payload.StartedAt = state.StartedAt
	}
	if !state.CompletedAt.IsZero() {
		payload.CompletedAt = state.CompletedAt
	}
	if !state.FailedAt.IsZero() {
		payload.FailedAt = state.FailedAt
	}
	failureValue2 := str.String(failure)
	if failureValue2.Trim() != "" {
		failureValue3 := str.String(failure)
		payload.Error = failureValue3.Trim()
	}

	return payload
}

// renderSummaryList renders one optional list section in summary instructions.
func renderSummaryList(title string, values []string) string {
	lines := make([]string, 0, len(values))
	for _, value := range values {
		valueText := str.String(value).Trim()
		if valueText == "" {
			continue
		}

		lines = append(lines, "- "+valueText)
	}

	if len(lines) == 0 {
		return ""
	}

	return "# " + title + "\n\n" + strings.Join(lines, "\n")
}

// isSummaryCompactionEnabled reports whether summary compaction is enabled.
func isSummaryCompactionEnabled(cfg *config.Config) bool {
	if cfg == nil || cfg.Compaction.Enabled == nil {
		return true
	}

	return *cfg.Compaction.Enabled
}

// getSummaryCompactionEvaluator builds the evaluator used by summary compaction.
func getSummaryCompactionEvaluator(cfg *config.Config) *compaction.Evaluator {
	if cfg == nil {
		return compaction.NewEvaluator(0, 0, 0)
	}

	return compaction.NewEvaluator(
		cfg.Models.Main.ContextLength,
		cfg.Compaction.TriggerPercent,
		cfg.Compaction.WarnPercent,
	)
}

// getSummaryRecentSessionTail reads the effective retained-tail setting.
func getSummaryRecentSessionTail(cfg *config.Config) int {
	return cfg.CompactionRecentSessionTailEffective()
}
