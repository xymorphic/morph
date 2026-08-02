package storememory

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	base "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/pkg/str"
)

func (s *Store) AppendTraceEvent(_ context.Context, event base.TraceEvent) (base.TraceEvent, error) {
	if s == nil {
		return base.TraceEvent{}, errors.New("store is required")
	}

	sessionIDValue := str.String(event.SessionID)
	event.SessionID = sessionIDValue.Trim()
	if err := base.ValidateSessionID(event.SessionID); err != nil {
		return base.TraceEvent{}, err
	}
	trimmedValueValue := str.String(event.Type)
	event.Type = trimmedValueValue.Trim()
	if event.Type == "" {
		return base.TraceEvent{}, errors.New("trace event type is required")
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	} else {
		event.Timestamp = event.Timestamp.UTC()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.traceEvents == nil {
		s.traceEvents = make(map[string][]base.TraceEvent)
	}
	if s.traceSequences == nil {
		s.traceSequences = make(map[string]int)
	}
	s.nextTraceID++
	s.traceSequences[event.SessionID]++
	event.ID = s.nextTraceID
	event.Sequence = s.traceSequences[event.SessionID]
	s.traceEvents[event.SessionID] = append(s.traceEvents[event.SessionID], base.CloneTraceEvent(event))

	return base.CloneTraceEvent(event), nil
}

func (s *Store) ListTraceEvents(_ context.Context, query base.TraceQuery) (base.TraceResult, error) {
	if s == nil {
		return base.TraceResult{}, errors.New("store is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	events := make([]base.TraceEvent, 0)
	sessionIDValue2 := str.String(query.SessionID)
	if sessionID := sessionIDValue2.Trim(); sessionID != "" {
		for _, event := range s.traceEvents[sessionID] {
			if base.TraceEventMatchesQuery(event, query) {
				events = append(events, base.CloneTraceEvent(event))
			}
		}
	} else {
		for _, sessionEvents := range s.traceEvents {
			for _, event := range sessionEvents {
				if base.TraceEventMatchesQuery(event, query) {
					events = append(events, base.CloneTraceEvent(event))
				}
			}
		}
	}

	slices.SortFunc(events, func(a, b base.TraceEvent) int {
		if a.SessionID != b.SessionID {
			if query.Desc {
				return strings.Compare(b.SessionID, a.SessionID)
			}

			return strings.Compare(a.SessionID, b.SessionID)
		}
		if a.Sequence != b.Sequence {
			if query.Desc {
				return b.Sequence - a.Sequence
			}

			return a.Sequence - b.Sequence
		}

		return int(a.ID) - int(b.ID)
	})

	offset := query.Offset
	if offset < 0 {
		offset = 0
	}
	if offset >= len(events) {
		return base.TraceResult{}, nil
	}
	events = events[offset:]
	if query.Limit > 0 && len(events) > query.Limit {
		events = events[:query.Limit]
	}

	return base.TraceResult{Events: events}, nil
}

func (s *Store) PruneTraceEvents(_ context.Context, sessionID string, maxEvents int) error {
	if s == nil {
		return errors.New("store is required")
	}
	if maxEvents < 0 {
		return errors.New("max trace events must be greater than or equal to zero")
	}

	sessionIDValue3 := str.String(sessionID)
	sessionID = sessionIDValue3.Trim()
	if err := base.ValidateSessionID(sessionID); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	events := s.traceEvents[sessionID]
	if len(events) <= maxEvents {
		return nil
	}
	s.traceEvents[sessionID] = append([]base.TraceEvent(nil), events[len(events)-maxEvents:]...)
	return nil
}
