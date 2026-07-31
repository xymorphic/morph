package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wandxy/morph/internal/guardrails"
	"github.com/wandxy/morph/internal/permissions"
)

type unsafeEvidenceRecorderStub struct {
	evidence      []guardrails.UnsafeEvidence
	contextErrors []error
	err           error
}

func (r *unsafeEvidenceRecorderStub) RecordUnsafeEvidence(
	ctx context.Context,
	evidence guardrails.UnsafeEvidence,
) (guardrails.UnsafeEvidence, error) {
	r.contextErrors = append(r.contextErrors, ctx.Err())
	r.evidence = append(r.evidence, evidence)
	return evidence, r.err
}

func TestRetainUnsafeEvidenceAddsAuthorizationProvenanceAfterCancellation(t *testing.T) {
	recorder := &unsafeEvidenceRecorderStub{}
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor:     permissions.Actor{Kind: permissions.ActorLocalOwner},
		Surface:   permissions.SurfaceTUI,
		SessionID: "session-from-context",
		RunID:     "run-from-context",
	})
	ctx, cancel := context.WithCancel(ctx)
	cancel()

	retainUnsafeEvidence(ctx, recorder, guardrails.UnsafeEvidence{
		Source: "user", Action: "blocked", Original: "unsafe",
	})

	require.Len(t, recorder.evidence, 1)
	require.Equal(t, "session-from-context", recorder.evidence[0].SessionID)
	require.Equal(t, "run-from-context", recorder.evidence[0].RunID)
	require.NoError(t, recorder.contextErrors[0])
}

func TestRetainUnsafeEvidencePreservesExplicitProvenance(t *testing.T) {
	recorder := &unsafeEvidenceRecorderStub{}
	ctx := permissions.WithContext(context.Background(), permissions.AuthorizationContext{
		Actor:     permissions.Actor{Kind: permissions.ActorLocalOwner},
		Surface:   permissions.SurfaceTUI,
		SessionID: "session-from-context",
		RunID:     "run-from-context",
	})

	retainUnsafeEvidence(ctx, recorder, guardrails.UnsafeEvidence{
		SessionID: "explicit-session",
		RunID:     "explicit-run",
		Source:    "user",
		Action:    "blocked",
		Original:  "unsafe",
	})

	require.Len(t, recorder.evidence, 1)
	require.Equal(t, "explicit-session", recorder.evidence[0].SessionID)
	require.Equal(t, "explicit-run", recorder.evidence[0].RunID)
}
