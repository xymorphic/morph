package environment

import (
	"context"

	"github.com/xymorphic/morph/internal/memory"
	"github.com/xymorphic/morph/internal/tools"
)

type memoryProviderWithoutSearch struct{}

func (memoryProviderWithoutSearch) Name() string {
	return "without-search"
}

func (memoryProviderWithoutSearch) Capabilities(context.Context) (memory.Capabilities, error) {
	return memory.Capabilities{SupportsSearch: true}, nil
}

func (memoryProviderWithoutSearch) ConfigureObservability(memory.Observability) error {
	return nil
}

func (memoryProviderWithoutSearch) Close() error {
	return nil
}

type memoryProviderWithObservabilityError struct {
	err error
}

func (memoryProviderWithObservabilityError) Name() string {
	return "observability-error"
}

func (memoryProviderWithObservabilityError) Capabilities(context.Context) (memory.Capabilities, error) {
	return memory.Capabilities{}, nil
}

func (p memoryProviderWithObservabilityError) ConfigureObservability(memory.Observability) error {
	return p.err
}

func (memoryProviderWithObservabilityError) Close() error {
	return nil
}

type memoryBackgroundProviderWithStartError struct {
	memoryProviderWithoutSearch
	err error
}

func (p memoryBackgroundProviderWithStartError) StartBackground(context.Context) error {
	return p.err
}

type memorySearchProviderStub struct {
	caps         memory.Capabilities
	capsErr      error
	searchQuery  memory.SearchQuery
	searchResult memory.SearchResult
	searchErr    error
}

func (p memorySearchProviderStub) Name() string {
	return "search"
}

func (p memorySearchProviderStub) Capabilities(context.Context) (memory.Capabilities, error) {
	return p.caps, p.capsErr
}

func (p memorySearchProviderStub) ConfigureObservability(memory.Observability) error {
	return nil
}

func (p memorySearchProviderStub) Close() error {
	return nil
}

func (p *memorySearchProviderStub) Search(_ context.Context, query memory.SearchQuery) (memory.SearchResult, error) {
	p.searchQuery = query
	return p.searchResult, p.searchErr
}

type memoryExtractionProviderStub struct {
	memorySearchProviderStub
	extractResult memory.ExtractionResult
	extractErr    error
}

func (p *memoryExtractionProviderStub) Upsert(context.Context, memory.MemoryItem) (memory.MemoryItem, error) {
	return memory.MemoryItem{}, nil
}

func (p *memoryExtractionProviderStub) Delete(context.Context, memory.DeleteRequest) error {
	return nil
}

func (p *memoryExtractionProviderStub) RecordEpisode(context.Context, memory.EpisodeRecord) (memory.MemoryItem, error) {
	return memory.MemoryItem{}, nil
}

func (p *memoryExtractionProviderStub) ExtractEpisodes(context.Context, memory.ExtractionRequest) (memory.ExtractionResult, error) {
	return p.extractResult, p.extractErr
}

type memoryWriteProviderStub struct {
	memoryExtractionProviderStub
	semanticRecord   memory.SemanticRecord
	proceduralRecord memory.ProceduralRecord
	promotionRequest memory.PromotionRequest
	updateRequest    memory.UpdateRequest
	deleteRequest    memory.DeleteRequest
}

func (p *memoryWriteProviderStub) RecordSemanticMemory(
	_ context.Context,
	record memory.SemanticRecord,
) (memory.MemoryItem, error) {
	p.semanticRecord = record
	return record.Item, nil
}

func (p *memoryWriteProviderStub) RecordProceduralMemory(
	_ context.Context,
	record memory.ProceduralRecord,
) (memory.MemoryItem, error) {
	p.proceduralRecord = record
	return record.Item, nil
}

func (p *memoryWriteProviderStub) PromoteCandidate(
	_ context.Context,
	req memory.PromotionRequest,
) (memory.LifecycleResult, error) {
	p.promotionRequest = req
	return memory.LifecycleResult{Item: memory.MemoryItem{ID: req.ID, Status: memory.StatusActive}}, nil
}

func (p *memoryWriteProviderStub) Update(
	_ context.Context,
	req memory.UpdateRequest,
) (memory.UpdateResult, error) {
	p.updateRequest = req
	return memory.UpdateResult{
		Previous:    memory.MemoryItem{ID: req.ID, Status: memory.StatusSuperseded},
		Replacement: req.Replacement,
		Lifecycle: memory.LifecycleResult{
			Item:     req.Replacement,
			Decision: memory.PromotionDecision{Approved: true},
		},
	}, nil
}

func (p *memoryWriteProviderStub) Delete(_ context.Context, req memory.DeleteRequest) error {
	p.deleteRequest = req
	return nil
}

type sequentialCapabilityMemoryProviderStub struct {
	memoryExtractionProviderStub
	capsSequence []memory.Capabilities
	errSequence  []error
	calls        int
}

func (p *sequentialCapabilityMemoryProviderStub) Capabilities(context.Context) (memory.Capabilities, error) {
	idx := p.calls
	p.calls++
	if idx < len(p.errSequence) && p.errSequence[idx] != nil {
		return memory.Capabilities{}, p.errSequence[idx]
	}
	if idx < len(p.capsSequence) {
		return p.capsSequence[idx], nil
	}
	if len(p.capsSequence) > 0 {
		return p.capsSequence[len(p.capsSequence)-1], nil
	}
	return memory.Capabilities{}, nil
}

type failingRegistry struct {
	err error
}

func (r failingRegistry) Register(tools.Definition) error {
	return r.err
}

func (failingRegistry) Get(string) (tools.Definition, bool) {
	return tools.Definition{}, false
}

func (failingRegistry) RegisterGroup(tools.Group) error {
	return nil
}

func (failingRegistry) GetGroup(string) (tools.Group, bool) {
	return tools.Group{}, false
}

func (failingRegistry) List() tools.Definitions {
	return nil
}

func (failingRegistry) ListGroups() []tools.Group {
	return nil
}

func (failingRegistry) Resolve(tools.Policy) (tools.Definitions, error) {
	return nil, nil
}

func (failingRegistry) Invoke(context.Context, tools.Call) (tools.Result, error) {
	return tools.Result{}, nil
}

type failingGroupRegistry struct {
	err error
}

func (failingGroupRegistry) Register(tools.Definition) error {
	return nil
}

func (failingGroupRegistry) Get(string) (tools.Definition, bool) {
	return tools.Definition{}, false
}

func (r failingGroupRegistry) RegisterGroup(tools.Group) error {
	return r.err
}

func (failingGroupRegistry) GetGroup(string) (tools.Group, bool) {
	return tools.Group{}, false
}

func (failingGroupRegistry) List() tools.Definitions {
	return nil
}

func (failingGroupRegistry) ListGroups() []tools.Group {
	return nil
}

func (failingGroupRegistry) Resolve(tools.Policy) (tools.Definitions, error) {
	return nil, nil
}

func (failingGroupRegistry) Invoke(context.Context, tools.Call) (tools.Result, error) {
	return tools.Result{}, nil
}
