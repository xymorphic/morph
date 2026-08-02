package observability

import (
	"context"

	"github.com/rs/zerolog"

	"github.com/xymorphic/morph/internal/memory"
	"github.com/xymorphic/morph/internal/trace"
	"github.com/xymorphic/morph/pkg/str"
)

// Observability adapts zerolog and trace.Session to the provider-local
// observability interfaces. It is a value type so callers can cheaply pass it
// into provider configuration.
type Observability struct {
	logger       *zerolog.Logger
	traceSession trace.Session
}

// New creates an adapter. Either argument may be nil; the corresponding sink is
// then disabled while the other remains available.
func New(logger *zerolog.Logger, traceSession trace.Session) memory.Observability {
	return Observability{logger: logger, traceSession: traceSession}
}

// Logger returns the memory.Logger adapter only when a concrete zerolog logger
// was configured.
func (o Observability) Logger() memory.Logger {
	if o.logger == nil {
		return nil
	}

	return logger{logger: o.logger}
}

// Tracer returns the memory.Tracer adapter only when a trace session exists.
func (o Observability) Tracer() memory.Tracer {
	if o.traceSession == nil {
		return nil
	}

	return tracer{traceSession: o.traceSession}
}

// logger keeps provider code independent from zerolog while preserving
// structured fields and log levels.
type logger struct {
	logger *zerolog.Logger
}

func (l logger) Debug(message string, fields map[string]any) {
	l.event(l.logger.Debug(), message, fields)
}

func (l logger) Info(message string, fields map[string]any) {
	l.event(l.logger.Info(), message, fields)
}

func (l logger) Warn(message string, fields map[string]any) {
	l.event(l.logger.Warn(), message, fields)
}

func (l logger) Error(message string, fields map[string]any) {
	l.event(l.logger.Error(), message, fields)
}

// event attaches all structured fields before writing the message. A nil event
// is ignored so disabled log levels remain cheap.
func (l logger) event(event *zerolog.Event, message string, fields map[string]any) {
	if event == nil {
		return
	}

	if len(fields) > 0 {
		event = event.Fields(fields)
	}
	event.Msg(message)
}

// tracer forwards memory events into a trace session. The context is accepted to
// satisfy the provider interface, but trace.Session owns its own persistence.
type tracer struct {
	traceSession trace.Session
}

func (t tracer) Record(_ context.Context, event string, payload any) {
	eventValue := str.String(event)
	if t.traceSession == nil || eventValue.Trim() == "" {
		return
	}
	payload, ok := trace.DecodePayload(event, payload)
	if !ok {
		payload = trace.PayloadFields(payload)
	}
	t.traceSession.Record(event, payload)
}
