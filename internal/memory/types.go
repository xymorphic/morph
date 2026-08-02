package memory

import (
	"context"
	"time"

	"github.com/xymorphic/morph/internal/memory/episodic"
	statecore "github.com/xymorphic/morph/internal/state/core"
)

// Kind classifies memory records by use and origin.
type Kind = statecore.MemoryKind

const (
	KindPinned     = statecore.MemoryKindPinned
	KindSemantic   = statecore.MemoryKindSemantic
	KindEpisodic   = statecore.MemoryKindEpisodic
	KindProcedural = statecore.MemoryKindProcedural
)

// Status tracks lifecycle state for memory records.
type Status = statecore.MemoryStatus

const (
	StatusCandidate  = statecore.MemoryStatusCandidate
	StatusActive     = statecore.MemoryStatusActive
	StatusSuperseded = statecore.MemoryStatusSuperseded
	StatusDeleted    = statecore.MemoryStatusDeleted
)

// SourceLink aliases statecore.MemorySourceLink at this package boundary.
type SourceLink = statecore.MemorySourceLink

// MemoryItem aliases statecore.MemoryItem at this package boundary.
type MemoryItem = statecore.MemoryItem

// MemoryPatch aliases statecore.MemoryPatch at this package boundary.
type MemoryPatch = statecore.MemoryPatch

// SearchQuery aliases statecore.MemorySearchQuery at this package boundary.
type SearchQuery = statecore.MemorySearchQuery

// SearchHit aliases statecore.MemorySearchHit at this package boundary.
type SearchHit = statecore.MemorySearchHit

// SearchResult aliases statecore.MemorySearchResult at this package boundary.
type SearchResult = statecore.MemorySearchResult

// DeleteRequest aliases statecore.MemoryDeleteRequest at this package boundary.
type DeleteRequest = statecore.MemoryDeleteRequest

const (
	RerankerUseCaseDefault            = statecore.MemoryRerankerUseCaseDefault
	RerankerUseCaseTurnRetrieval      = statecore.MemoryRerankerUseCaseTurnRetrieval
	RerankerUseCaseToolSearch         = statecore.MemoryRerankerUseCaseToolSearch
	RerankerUseCasePinned             = statecore.MemoryRerankerUseCasePinned
	RerankerUseCasePromotion          = statecore.MemoryRerankerUseCasePromotion
	RerankerUseCaseReflection         = statecore.MemoryRerankerUseCaseReflection
	RerankerUseCaseEpisodicExtraction = statecore.MemoryRerankerUseCaseEpisodicExtraction
)

// UpdateRequest describes a memory replacement request.
type UpdateRequest struct {
	ID          string
	Reason      string
	Replacement MemoryItem
}

// UpdateResult reports the previous memory, replacement, and lifecycle changes.
type UpdateResult struct {
	Previous    MemoryItem
	Replacement MemoryItem
	Lifecycle   LifecycleResult
}

// Capabilities describes the behavioral surface a memory provider exposes.
// Callers use it as feature negotiation instead of assuming every backend can
// search, reflect, promote, rerank, and emit traces.
type Capabilities struct {
	SupportsPinned              bool
	SupportsSearch              bool
	SupportsWrite               bool
	SupportsDelete              bool
	SupportsEpisodeRecording    bool
	SupportsSemanticRecording   bool
	SupportsProceduralRecording bool
	SupportsReflection          bool
	SupportsBM25                bool
	SupportsVectors             bool
	SupportsReranking           bool
	SupportsAudit               bool
	SupportsObservability       bool
}

// EpisodeRecord aliases episodic.EpisodeRecord at this package boundary.
type EpisodeRecord = episodic.EpisodeRecord

// ExtractionRequest aliases episodic.Request at this package boundary.
type ExtractionRequest = episodic.Request

// ExtractionResult aliases episodic.Result at this package boundary.
type ExtractionResult = episodic.Result

// ExtractionWindowResult aliases episodic.WindowResult at this package boundary.
type ExtractionWindowResult = episodic.WindowResult

// EpisodicBackgroundOptions aliases episodic.BackgroundOptions at this package boundary.
type EpisodicBackgroundOptions = episodic.BackgroundOptions

// TraceRecorder aliases episodic.TraceRecorder at this package boundary.
type TraceRecorder = episodic.TraceRecorder

// SemanticRecord wraps a promoted semantic memory item.
type SemanticRecord struct {
	Item MemoryItem
}

// ProceduralRecord wraps a promoted procedural memory item.
type ProceduralRecord struct {
	Item MemoryItem
}

// ReflectionRequest consolidates unreflected episodic memories for one session
// into higher-order candidates. Empty SessionID means "use the current session".
type ReflectionRequest struct {
	SessionID    string
	Limit        int
	RelatedLimit int
}

// ReflectionBackgroundOptions configures the periodic reflection loop. The loop
// calls the same Reflect method as manual invocations, so validation, dedupe,
// provenance, and writes stay centralized.
type ReflectionBackgroundOptions struct {
	Enabled      bool
	Interval     time.Duration
	Limit        int
	RelatedLimit int
}

// ReflectionResult contains promoted memories and lifecycle changes from reflection.
type ReflectionResult struct {
	SessionID    string
	SourceCount  int
	RelatedCount int
	WriteCount   int
	Items        []MemoryItem
}

// ReflectionGenerationRequest is the LLM-facing prompt payload. The provider
// gives the generator source memories and nearby related memories, but keeps
// responsibility for candidate validation and storage.
type ReflectionGenerationRequest struct {
	SessionID string
	Sources   []MemoryItem
	Related   []MemoryItem
	Limit     int
}

// ReflectionGenerationResult contains generated reflection candidates and trace metadata.
type ReflectionGenerationResult struct {
	Items []MemoryItem
}

// ReflectionGenerator proposes candidates only. It should not perform storage
// writes or lifecycle decisions; the provider owns those responsibilities.
type ReflectionGenerator interface {
	GenerateReflectionCandidates(context.Context, ReflectionGenerationRequest) (ReflectionGenerationResult, error)
}

// PromotionRequest evaluates one candidate for activation. Strict is reserved
// for flows that intentionally allow sensitive transitions, such as promoting a
// pinned candidate that background promotion must reject.
type PromotionRequest struct {
	ID     string
	Reason string
	Strict bool
}

// PromotionBackgroundOptions controls autonomous promotion of unevaluated
// candidates. A candidate with PromotionEvaluatedAt set is intentionally skipped
// so rejected candidates do not churn forever.
type PromotionBackgroundOptions struct {
	Enabled            bool
	Interval           time.Duration
	Limit              int
	Reason             string
	EvaluatedRetention time.Duration
}

// LifecycleResult records memory status changes from a promotion or replacement.
type LifecycleResult struct {
	Item     MemoryItem
	Related  []MemoryItem
	Decision PromotionDecision
}

// PromotionPolicyRequest gives a policy all available evidence without giving
// it direct access to storage. Provider-owned hard gates are passed in as
// strings so a custom policy can explain them, but cannot bypass them.
type PromotionPolicyRequest struct {
	Candidate          MemoryItem
	Related            []MemoryItem
	Reason             string
	Strict             bool
	AdmissionResult    string
	GuardrailResult    string
	ReflectionEvidence bool
	ConflictState      string
}

// PromotionDecision is persisted as audit metadata whenever a candidate is
// evaluated. The simple shape keeps CLI output, trace inspection, and database
// debugging readable.
type PromotionDecision struct {
	Approved      bool
	Policy        string
	Reason        string
	Confidence    float64
	ConflictState string
}

// PromotionPolicy makes the final promote/reject recommendation. Policies
// should be pure decision functions; writes and metadata stay inside provider
// lifecycle code.
type PromotionPolicy interface {
	EvaluatePromotion(context.Context, PromotionPolicyRequest) (PromotionDecision, error)
}

// Provider is the root interface common to all memory implementations. Feature
// interfaces below are split so callers can depend on only the behavior they
// need.
type Provider interface {
	Name() string
	Capabilities(context.Context) (Capabilities, error)
	ConfigureObservability(Observability) error
	Close() error
}

// PinnedProvider loads memory intended for direct prompt-context injection.
type PinnedProvider interface {
	LoadPinned(context.Context, SearchQuery) ([]MemoryItem, error)
}

// SearchProvider retrieves existing memories through the provider guardrail and
// redaction boundary.
type SearchProvider interface {
	Search(context.Context, SearchQuery) (SearchResult, error)
}

// WriteProvider records or deletes memory through provider validation.
type WriteProvider interface {
	Upsert(context.Context, MemoryItem) (MemoryItem, error)
	Delete(context.Context, DeleteRequest) error
}

// UpdateProvider applies memory updates and lifecycle transitions.
type UpdateProvider interface {
	Update(context.Context, UpdateRequest) (UpdateResult, error)
}

// EpisodeProvider records one already-curated episodic candidate.
type EpisodeProvider interface {
	RecordEpisode(context.Context, EpisodeRecord) (MemoryItem, error)
}

// SemanticProvider records explicit semantic candidates.
type SemanticProvider interface {
	RecordSemanticMemory(context.Context, SemanticRecord) (MemoryItem, error)
}

// ProceduralProvider records explicit procedural candidates.
type ProceduralProvider interface {
	RecordProceduralMemory(context.Context, ProceduralRecord) (MemoryItem, error)
}

// ExtractionProvider runs model-backed episodic extraction over message windows.
type ExtractionProvider interface {
	ExtractEpisodes(context.Context, ExtractionRequest) (ExtractionResult, error)
}

// BackgroundProvider starts provider-owned background loops.
type BackgroundProvider interface {
	StartBackground(context.Context) error
}

// ReflectionProvider consolidates episodic evidence into derived candidates.
type ReflectionProvider interface {
	Reflect(context.Context, ReflectionRequest) (ReflectionResult, error)
}

// LifecycleProvider governs candidate activation.
type LifecycleProvider interface {
	PromoteCandidate(context.Context, PromotionRequest) (LifecycleResult, error)
}
