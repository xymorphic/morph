package memory

import (
	"context"

	statecore "github.com/xymorphic/morph/internal/state/core"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
)

type fakeLogger struct {
	debug []map[string]any
}

func (l *fakeLogger) Debug(_ string, fields map[string]any) {
	l.debug = append(l.debug, fields)
}

func (l *fakeLogger) Info(string, map[string]any)  {}
func (l *fakeLogger) Warn(string, map[string]any)  {}
func (l *fakeLogger) Error(string, map[string]any) {}

type fakeTracer struct {
	events   []string
	fields   []map[string]any
	payloads []any
}

func (t *fakeTracer) Record(_ context.Context, event string, payload any) {
	t.events = append(t.events, event)
	t.payloads = append(t.payloads, payload)
	fields, _ := payload.(map[string]any)
	t.fields = append(t.fields, fields)
}

type fakeObservability struct {
	logger *fakeLogger
	tracer *fakeTracer
}

func (o fakeObservability) Logger() Logger {
	if o.logger == nil {
		return nil
	}

	return o.logger
}

func (o fakeObservability) Tracer() Tracer {
	if o.tracer == nil {
		return nil
	}

	return o.tracer
}

type fakeGuardrails struct {
	validateSearchCalls int
	validateWriteCalls  int
	validateDeleteCalls int
	safetyScanCalls     int
	redactCalls         int
	searchErr           error
	writeErr            error
	safetyErr           error
	deleteErr           error
	redactErr           error
	redactText          string
}

func (g *fakeGuardrails) ValidateSearch(context.Context, SearchQuery) error {
	g.validateSearchCalls++
	return g.searchErr
}

func (g *fakeGuardrails) ValidateWrite(context.Context, MemoryItem) error {
	g.validateWriteCalls++
	return g.writeErr
}

func (g *fakeGuardrails) ValidateDelete(context.Context, DeleteRequest) error {
	g.validateDeleteCalls++
	return g.deleteErr
}

func (g *fakeGuardrails) SafetyScan(context.Context, MemoryItem) error {
	g.safetyScanCalls++
	return g.safetyErr
}

func (g *fakeGuardrails) Redact(_ context.Context, item MemoryItem) (MemoryItem, error) {
	g.redactCalls++
	if g.redactErr != nil {
		return MemoryItem{}, g.redactErr
	}
	if g.redactText != "" {
		item.Text = g.redactText
	}
	return item, nil
}

type fakeMemoryManager struct {
	searchResult  SearchResult
	searchErr     error
	upsertItem    MemoryItem
	upsertErr     error
	patchItem     MemoryItem
	patchErr      error
	deleteErr     error
	hardDeleteErr error
}

func (s fakeMemoryManager) SearchMemory(context.Context, SearchQuery) (SearchResult, error) {
	return s.searchResult, s.searchErr
}

func (s fakeMemoryManager) UpsertMemory(_ context.Context, item MemoryItem) (MemoryItem, error) {
	if s.upsertErr != nil {
		return MemoryItem{}, s.upsertErr
	}
	if s.upsertItem.ID != "" {
		return s.upsertItem, nil
	}

	return item, nil
}

func (s fakeMemoryManager) PatchMemory(_ context.Context, patch MemoryPatch) (MemoryItem, error) {
	if s.patchErr != nil {
		return MemoryItem{}, s.patchErr
	}
	if s.patchItem.ID != "" {
		return s.patchItem, nil
	}

	return MemoryItem{ID: patch.ID}, nil
}

func (s fakeMemoryManager) DeleteMemory(context.Context, DeleteRequest) error {
	return s.deleteErr
}

func (s fakeMemoryManager) HardDeleteMemory(context.Context, DeleteRequest) error {
	if s.hardDeleteErr != nil {
		return s.hardDeleteErr
	}

	return s.deleteErr
}

func (s fakeMemoryManager) CurrentSession(context.Context) (string, error) {
	return "", nil
}

func (s fakeMemoryManager) CountMessages(context.Context, string, statecore.MessageQueryOptions) (int, error) {
	return 0, nil
}

func (s fakeMemoryManager) GetMessages(context.Context, string, statecore.MessageQueryOptions) ([]morphmsg.Message, error) {
	return nil, nil
}

func (s fakeMemoryManager) ListTraceEvents(context.Context, statecore.TraceQuery) (statecore.TraceResult, error) {
	return statecore.TraceResult{}, nil
}

func (s fakeMemoryManager) UpdateCheckpoints(context.Context, string, statecore.CheckpointPatch) error {
	return nil
}

type recordingMemoryManager struct {
	fakeMemoryManager
	searchResults         []SearchResult
	searchErrs            []error
	searchQueries         []SearchQuery
	upsertErrs            []error
	upsertItems           []MemoryItem
	upsertErr             error
	patchErrs             []error
	patches               []MemoryPatch
	hardDeleteErrs        []error
	hardDeleteRequests    []DeleteRequest
	currentSessionErr     error
	messageCounts         map[string]int
	sessions              []statecore.Session
	listSessionsErr       error
	reflectionCheckpoints map[string]int
}

func (m *recordingMemoryManager) SearchMemory(ctx context.Context, query SearchQuery) (SearchResult, error) {
	m.searchQueries = append(m.searchQueries, query)
	if len(m.searchErrs) > 0 {
		err := m.searchErrs[0]
		m.searchErrs = m.searchErrs[1:]
		if err != nil {
			return SearchResult{}, err
		}
	}
	if len(m.searchResults) > 0 {
		result := m.searchResults[0]
		m.searchResults = m.searchResults[1:]
		return result, nil
	}

	return m.fakeMemoryManager.SearchMemory(ctx, query)
}

func (m *recordingMemoryManager) UpsertMemory(_ context.Context, item MemoryItem) (MemoryItem, error) {
	m.upsertItems = append(m.upsertItems, item.Clone())
	if len(m.upsertErrs) > 0 {
		err := m.upsertErrs[0]
		m.upsertErrs = m.upsertErrs[1:]
		if err != nil {
			return MemoryItem{}, err
		}
	}
	if m.upsertErr != nil {
		return MemoryItem{}, m.upsertErr
	}
	if m.upsertItem.ID != "" {
		return m.upsertItem, nil
	}

	return item, nil
}

func (m *recordingMemoryManager) PatchMemory(_ context.Context, patch MemoryPatch) (MemoryItem, error) {
	m.patches = append(m.patches, patch)
	if len(m.patchErrs) > 0 {
		err := m.patchErrs[0]
		m.patchErrs = m.patchErrs[1:]
		if err != nil {
			return MemoryItem{}, err
		}
	}
	return MemoryItem{ID: patch.ID}, nil
}

func (m *recordingMemoryManager) HardDeleteMemory(_ context.Context, req DeleteRequest) error {
	m.hardDeleteRequests = append(m.hardDeleteRequests, req)
	if len(m.hardDeleteErrs) > 0 {
		err := m.hardDeleteErrs[0]
		m.hardDeleteErrs = m.hardDeleteErrs[1:]
		if err != nil {
			return err
		}
	}

	return m.fakeMemoryManager.HardDeleteMemory(context.Background(), req)
}

func (m *recordingMemoryManager) CurrentSession(ctx context.Context) (string, error) {
	if m.currentSessionErr != nil {
		return "", m.currentSessionErr
	}

	return m.fakeMemoryManager.CurrentSession(ctx)
}

func (m *recordingMemoryManager) CountMessages(ctx context.Context, id string, opts statecore.MessageQueryOptions) (int, error) {
	if m.messageCounts != nil {
		return m.messageCounts[id], nil
	}

	return m.fakeMemoryManager.CountMessages(ctx, id, opts)
}

func (m *recordingMemoryManager) ListSessions(context.Context, ...statecore.SessionListOptions) ([]statecore.Session, error) {
	if m.listSessionsErr != nil {
		return nil, m.listSessionsErr
	}

	return append([]statecore.Session(nil), m.sessions...), nil
}

func (m *recordingMemoryManager) UpdateCheckpoints(_ context.Context, id string, patch statecore.CheckpointPatch) error {
	if patch.ReflectionOffset != nil {
		if m.reflectionCheckpoints == nil {
			m.reflectionCheckpoints = make(map[string]int)
		}
		if *patch.ReflectionOffset > m.reflectionCheckpoints[id] {
			m.reflectionCheckpoints[id] = *patch.ReflectionOffset
		}
	}

	return nil
}

type vectorCapabilityMemoryManager struct {
	fakeMemoryManager
	enabled bool
}

func (m vectorCapabilityMemoryManager) SupportsVectorSearch() bool {
	return m.enabled
}

type fakeReflectionGenerator struct {
	requests []ReflectionGenerationRequest
	result   ReflectionGenerationResult
	err      error
}

func (g *fakeReflectionGenerator) GenerateReflectionCandidates(
	_ context.Context,
	req ReflectionGenerationRequest,
) (ReflectionGenerationResult, error) {
	g.requests = append(g.requests, req)
	if g.err != nil {
		return ReflectionGenerationResult{}, g.err
	}
	return g.result, nil
}

type fakePromotionPolicy struct {
	requests []PromotionPolicyRequest
	decision PromotionDecision
	err      error
}

func (p *fakePromotionPolicy) EvaluatePromotion(
	_ context.Context,
	req PromotionPolicyRequest,
) (PromotionDecision, error) {
	p.requests = append(p.requests, req)
	if p.err != nil {
		return PromotionDecision{}, p.err
	}
	return p.decision, nil
}
