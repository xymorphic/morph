package observability

import (
	"bytes"
	"context"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/trace"
)

type recordingTraceSession struct {
	events  []string
	payload []any
}

func (s *recordingTraceSession) ID() string {
	return "trace_123"
}

func (s *recordingTraceSession) Record(event string, payload any) {
	s.events = append(s.events, event)
	s.payload = append(s.payload, payload)
}

func (s *recordingTraceSession) Close() {}

func TestObservability_ReturnsNilForMissingRuntimeValues(t *testing.T) {
	obs := New(nil, nil)

	require.Nil(t, obs.Logger())
	require.Nil(t, obs.Tracer())
}

func TestLogger_EmitsMessagesWithFields(t *testing.T) {
	var buf bytes.Buffer
	zlog := zerolog.New(&buf)
	memoryLogger := New(&zlog, nil).Logger()

	memoryLogger.Debug("debug message", map[string]any{"provider": "memory"})
	memoryLogger.Info("info message", nil)
	memoryLogger.Warn("warn message", map[string]any{"operation": "search"})
	memoryLogger.Error("error message", map[string]any{"failed": true})

	output := buf.String()
	require.Contains(t, output, "debug message")
	require.Contains(t, output, `"provider":"memory"`)
	require.Contains(t, output, "info message")
	require.Contains(t, output, "warn message")
	require.Contains(t, output, "error message")

	loggerAdapter := memoryLogger.(logger)
	loggerAdapter.event(nil, "ignored", map[string]any{"ignored": true})
	require.NotContains(t, buf.String(), "ignored")
}

func TestTracer_RecordsEvents(t *testing.T) {
	traceSession := &recordingTraceSession{}
	memoryTracer := New(nil, traceSession).Tracer()

	memoryTracer.Record(context.Background(), "memory.search.completed", map[string]any{
		"result_count": 1,
	})
	memoryTracer.Record(context.Background(), "   ", map[string]any{
		"result_count": 2,
	})
	tracer{}.Record(context.Background(), "memory.search.skipped", nil)

	require.Equal(t, []string{"memory.search.completed"}, traceSession.events)
	require.Equal(t, []any{trace.MemoryEventPayload{ResultCount: 1}}, traceSession.payload)
}
