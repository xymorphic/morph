package agent

import (
	"context"

	"github.com/wandxy/morph/internal/guardrails"
)

type unsafeEvidenceRecorderSource interface {
	UnsafeEvidenceRecorder() guardrails.UnsafeEvidenceRecorder
}

func (t *Turn) getUnsafeEvidenceRecorder() guardrails.UnsafeEvidenceRecorder {
	if t == nil {
		return nil
	}
	source, _ := t.env.(unsafeEvidenceRecorderSource)
	if source == nil {
		return nil
	}
	return source.UnsafeEvidenceRecorder()
}

func (t *Turn) retainUnsafeEvidence(
	ctx context.Context,
	evidence guardrails.UnsafeEvidence,
) {
	if t == nil {
		return
	}
	if evidence.SessionID == "" {
		evidence.SessionID = t.sessionID
	}
	retainUnsafeEvidence(ctx, t.getUnsafeEvidenceRecorder(), evidence)
}

func retainUnsafeEvidence(
	ctx context.Context,
	recorder guardrails.UnsafeEvidenceRecorder,
	evidence guardrails.UnsafeEvidence,
) {
	guardrails.RetainUnsafeEvidence(ctx, recorder, evidence)
}
