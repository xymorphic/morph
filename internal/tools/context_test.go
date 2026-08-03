package tools

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/agent/runcontext"
	storage "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/pkg/nanoid"
)

func TestSessionIDFromContextPrefersEffectiveRunContext(t *testing.T) {
	parentID := nanoid.MustFromSeed(storage.SessionIDPrefix, "parent", "ToolsContextTestSeed")
	childID := nanoid.MustFromSeed(storage.SessionIDPrefix, "child", "ToolsContextTestSeed")
	parent, err := runcontext.NewParent(parentID)
	require.NoError(t, err)
	child, err := parent.NewChild(runcontext.ChildOptions{
		ChildSessionID: childID,
		RunID:          "run_tools",
	})
	require.NoError(t, err)

	ctx := WithRunContext(WithSessionID(context.Background(), parentID), child)

	require.Equal(t, childID, SessionIDFromContext(ctx))
	runCtx, ok := RunContextFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, parentID, runCtx.Session.PublicID)
	require.Equal(t, childID, runCtx.Session.EffectiveID)
}

func TestSessionIDFromContextFallsBackToLegacySessionID(t *testing.T) {
	ctx := WithSessionID(context.Background(), "session-1")

	require.Equal(t, "session-1", SessionIDFromContext(ctx))
	_, ok := RunContextFromContext(ctx)
	require.False(t, ok)
}

func TestWithSessionID_HandlesNilContext(t *testing.T) {
	var nilContext context.Context
	ctx := WithSessionID(nilContext, "session-1")

	require.Equal(t, "session-1", SessionIDFromContext(ctx))
}

func TestWithRunContext_HandlesNilContext(t *testing.T) {
	runCtx, err := runcontext.NewParent(storage.DefaultSessionID)
	require.NoError(t, err)

	var nilContext context.Context
	ctx := WithRunContext(nilContext, runCtx)

	require.Equal(t, storage.DefaultSessionID, SessionIDFromContext(ctx))
}

func TestWithRunContext_IgnoresInvalidRunContext(t *testing.T) {
	ctx := WithRunContext(WithSessionID(context.Background(), "legacy"), runcontext.Context{
		Session: runcontext.Session{PublicID: "session-1"},
	})

	require.Equal(t, "legacy", SessionIDFromContext(ctx))
	_, ok := RunContextFromContext(ctx)
	require.False(t, ok)
}

func TestSessionIDFromContext_ReturnsEmptyForNilContext(t *testing.T) {
	var nilContext context.Context
	require.Empty(t, SessionIDFromContext(nilContext))
}

func TestTraceRecorderFromContext_ReturnsRecorder(t *testing.T) {
	recorder := &traceRecorderStub{}

	ctx := WithTraceRecorder(context.Background(), recorder)

	require.Same(t, recorder, TraceRecorderFromContext(ctx))
}

func TestWithTraceRecorder_HandlesNilContext(t *testing.T) {
	recorder := &traceRecorderStub{}

	var nilContext context.Context
	ctx := WithTraceRecorder(nilContext, recorder)

	require.Same(t, recorder, TraceRecorderFromContext(ctx))
}

func TestTraceRecorderFromContext_ReturnsNilForNilOrMissingRecorder(t *testing.T) {
	var nilContext context.Context
	require.Nil(t, TraceRecorderFromContext(nilContext))
	require.Nil(t, TraceRecorderFromContext(context.Background()))
}

func TestApprovalWaitDurationFromContext_ReturnsStoredPositiveDuration(t *testing.T) {
	duration := 250 * time.Millisecond
	ctx := withApprovalWaitDuration(context.Background(), duration)

	require.Equal(t, duration, ApprovalWaitDurationFromContext(ctx))
	require.Zero(t, ApprovalWaitDurationFromContext(withApprovalWaitDuration(context.Background(), 0)))
	var nilContext context.Context
	require.Zero(t, ApprovalWaitDurationFromContext(nilContext))
}

type traceRecorderStub struct{}

func (s *traceRecorderStub) Record(string, any) {}
