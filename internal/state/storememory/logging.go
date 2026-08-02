package storememory

import (
	"context"
	"errors"
	"strings"

	"github.com/rs/zerolog"

	base "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/internal/state/search"
	"github.com/xymorphic/morph/pkg/logutils"
	"github.com/xymorphic/morph/pkg/str"
)

var sessionSearchLog = logutils.Module("state.memory")

func (s *Store) logSearchEvent(_ string, id string, opts base.SearchMessageOptions) *zerolog.Event {
	queryValue := str.String(opts.Query)
	event := sessionSearchLog.Debug().
		Int("query_chars", len([]rune(queryValue.Trim())))
	idValue := str.String(id)
	if id = idValue.Trim(); id != "" {
		event = event.Str("session_id", id)
	}
	if opts.IgnoreSessionID != "" {
		event = event.Str("ignore_session_id", opts.IgnoreSessionID)
	}
	if opts.Role != "" {
		roleValue := str.String(string(opts.Role))
		event = event.Str("role", roleValue.Trim())
	}
	if toolName := base.NormalizeMatchValue(opts.ToolName); toolName != "" {
		event = event.Str("tool_name", toolName)
	}
	if opts.MaxSessions > 0 {
		event = event.Int("max_sessions", opts.MaxSessions)
	}
	if opts.MaxMessagesPerSession > 0 {
		event = event.Int("max_messages_per_session", opts.MaxMessagesPerSession)
	}

	return event
}

func (s *Store) logVectorEvent(_ string) *zerolog.Event {
	return sessionSearchLog.Debug()
}

func addVectorInputDiagnostics(event *zerolog.Event, diagnostics search.VectorInputDiagnostics) *zerolog.Event {
	return event.
		Strs("source_ids", diagnostics.SourceIDs).
		Strs("tool_names", diagnostics.ToolNames).
		Int("source_count", len(diagnostics.SourceIDs)).
		Int("chunk_count", diagnostics.ChunkCount).
		Int("truncated_source_count", diagnostics.TruncatedSourceCount)
}

func applySafeErrorLog(event *zerolog.Event, err error) *zerolog.Event {
	if err == nil {
		return event
	}

	return event.Str("error_kind", getSafeErrorKind(err))
}

func getSafeErrorKind(err error) string {
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
	case strings.Contains(value, "validation"):
		return "validation_failed"
	case strings.Contains(value, "not found"):
		return "not_found"
	case strings.Contains(value, "required"):
		return "missing_required_value"
	case strings.Contains(value, "timeout"):
		return "timeout"
	default:
		return "operation_failed"
	}
}

func (s *Store) logCandidateDiagnostics(stage string, candidates []*searchCandidate) {
	if !s.diagnosticsEnabled() {
		return
	}

	for rank, candidate := range candidates {
		stageValue := str.String(stage)
		event := sessionSearchLog.Debug().
			Str("stage", stageValue.Trim()).
			Str("session_id", candidate.SessionID).
			Uint("message_id", candidate.Message.ID).
			Float64("lexical_score", candidate.LexicalScore).
			Float64("vector_score", candidate.VectorScore).
			Float64("fused_score", candidate.FusedScore).
			Int("lexical_rank", candidate.LexicalRank).
			Int("vector_rank", candidate.VectorRank).
			Int("rank", rank+1)
		if candidate.HasRerank {
			event = event.Float64("rerank_score", candidate.RerankScore).Int("rerank_rank", rank+1)
		}
		if candidate.MatchedToolName != "" {
			event = event.Str("matched_tool_name", candidate.MatchedToolName)
		}
		event.Msg("session search ranking diagnostic")
	}
}
