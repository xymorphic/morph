package tools

import (
	"context"

	"github.com/xymorphic/morph/internal/agent/runcontext"
)

// TraceRecorder records tool events emitted during execution.
type TraceRecorder interface {
	Record(string, any)
}

type sessionIDContextKey struct{}
type traceRecorderContextKey struct{}

// WithSessionID describes the active session id on ctx.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, sessionIDContextKey{}, sessionID)
}

// WithRunContext describes the active run context on ctx supplied to prompts.
func WithRunContext(ctx context.Context, runCtx runcontext.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, err := runCtx.Normalize()
	if err != nil {
		return ctx
	}

	ctx = runcontext.WithContext(ctx, runCtx)
	return WithSessionID(ctx, runCtx.StateSessionID())
}

// SessionIDFromContext returns the active session ID stored on ctx.
func SessionIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if runCtx, ok := runcontext.FromContext(ctx); ok {
		return runCtx.StateSessionID()
	}

	sessionID, _ := ctx.Value(sessionIDContextKey{}).(string)
	return sessionID
}

// RunContextFromContext runs context from context.
func RunContextFromContext(ctx context.Context) (runcontext.Context, bool) {
	return runcontext.FromContext(ctx)
}

// WithTraceRecorder describes a trace recorder on ctx.
func WithTraceRecorder(ctx context.Context, recorder TraceRecorder) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, traceRecorderContextKey{}, recorder)
}

// TraceRecorderFromContext returns the trace recorder stored on ctx.
func TraceRecorderFromContext(ctx context.Context) TraceRecorder {
	if ctx == nil {
		return nil
	}

	recorder, _ := ctx.Value(traceRecorderContextKey{}).(TraceRecorder)
	return recorder
}
