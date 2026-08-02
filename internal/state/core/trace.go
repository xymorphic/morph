package core

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"time"

	"github.com/xymorphic/morph/pkg/str"
)

// ErrTraceStoreUnsupported is returned when a store does not persist trace events.
var ErrTraceStoreUnsupported = errors.New("trace store is not supported")

// TraceEvent represents a trace event.
type TraceEvent struct {
	ID        uint
	SessionID string
	Sequence  int
	Type      string
	Timestamp time.Time
	Payload   any
}

// TraceQuery describes filters and limits for trace lookup.
type TraceQuery struct {
	SessionID   string
	Types       []string
	Limit       int
	Offset      int
	MinSequence int
	Desc        bool
}

// TraceResult contains trace events returned by a query.
type TraceResult struct {
	Events []TraceEvent
}

// TraceStore persists and queries trace events.
type TraceStore interface {
	AppendTraceEvent(context.Context, TraceEvent) (TraceEvent, error)
	ListTraceEvents(context.Context, TraceQuery) (TraceResult, error)
	PruneTraceEvents(context.Context, string, int) error
}

// CloneTraceEvent clones clone trace event.
func CloneTraceEvent(event TraceEvent) TraceEvent {
	event.Payload = cloneTracePayload(event.Payload)
	return event
}

func cloneTracePayload(payload any) any {
	if payload == nil {
		return nil
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil
	}

	payloadType := reflect.TypeOf(payload)
	cloned := reflect.New(payloadType)
	if err := json.Unmarshal(data, cloned.Interface()); err != nil {
		return nil
	}

	return cloned.Elem().Interface()
}

// TraceEventMatchesQuery reports whether event satisfies query filters.
func TraceEventMatchesQuery(event TraceEvent, query TraceQuery) bool {
	sessionIDValue := str.String(query.SessionID)
	if sessionID := sessionIDValue.Trim(); sessionID != "" && event.SessionID != sessionID {
		return false
	}
	if types := NormalizeTraceTypes(query.Types); len(types) > 0 && !slices.Contains(types, event.Type) {
		return false
	}
	if query.MinSequence > 0 && event.Sequence < query.MinSequence {
		return false
	}

	return true
}

// NormalizeTraceTypes normalizes trace types.
func NormalizeTraceTypes(types []string) []string {
	seen := make(map[string]struct{}, len(types))
	results := make([]string, 0, len(types))
	for _, eventType := range types {
		eventTypeValue := str.String(eventType)
		eventType = eventTypeValue.Trim()
		if eventType == "" {
			continue
		}
		if _, ok := seen[eventType]; ok {
			continue
		}
		seen[eventType] = struct{}{}
		results = append(results, eventType)
	}

	return results
}
