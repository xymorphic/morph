package storememory

import (
	"sync"

	"github.com/wandxy/morph/internal/permissions"
	base "github.com/wandxy/morph/internal/state/core"
	"github.com/wandxy/morph/internal/state/search"
	morphmsg "github.com/wandxy/morph/pkg/agent/message"
	"github.com/wandxy/morph/pkg/gateway/pairing"
)

// Store keeps sessions, messages, memory, and traces in process memory.
type Store struct {
	vectors          *search.VectorConfig
	memoryReranker   search.Reranker
	mu               sync.RWMutex
	sessions         map[string]Session
	messages         map[string][]morphmsg.Message
	summaries        map[string]SessionSummary
	gatewayBindings  map[string]base.GatewayBinding
	pairingRequests  map[string]pairing.PendingRequest
	pairedSenders    map[string]pairing.ApprovedSender
	automationJobs   map[string]base.AutomationJob
	automationRuns   map[string]base.AutomationRun
	memoryItems      map[string]base.MemoryItem
	traceEvents      map[string][]base.TraceEvent
	traceSequences   map[string]int
	approvalRequests map[string]permissions.ApprovalRequest
	approvalGrants   map[string]permissions.ApprovalGrant
	vectorStates     map[string]search.VectorIndexState
	sessionInboxes   map[string]*sessionInboxState
	sessionRunIDs    map[string]struct{}
	runnerGeneration string
	currentSession   string
	nextMessageID    uint
	nextTraceID      uint
}

// NewStore returns a store backed by the supplied dependencies.
func NewStore() *Store {
	return &Store{
		sessions:         make(map[string]Session),
		messages:         make(map[string][]morphmsg.Message),
		summaries:        make(map[string]SessionSummary),
		gatewayBindings:  make(map[string]base.GatewayBinding),
		pairingRequests:  make(map[string]pairing.PendingRequest),
		pairedSenders:    make(map[string]pairing.ApprovedSender),
		automationJobs:   make(map[string]base.AutomationJob),
		automationRuns:   make(map[string]base.AutomationRun),
		memoryItems:      make(map[string]base.MemoryItem),
		traceEvents:      make(map[string][]base.TraceEvent),
		traceSequences:   make(map[string]int),
		approvalRequests: make(map[string]permissions.ApprovalRequest),
		approvalGrants:   make(map[string]permissions.ApprovalGrant),
		vectorStates:     make(map[string]search.VectorIndexState),
		sessionInboxes:   make(map[string]*sessionInboxState),
		sessionRunIDs:    make(map[string]struct{}),
	}
}

func (s *Store) Permission() (permissions.ApprovalStore, bool) {
	if s == nil {
		return nil, false
	}

	return s, true
}

func (s *Store) Session() base.SessionStore {
	return s
}

func (s *Store) Automation() (base.AutomationStore, bool) {
	if s == nil {
		return nil, false
	}

	return s, true
}

func (s *Store) Memory() (base.MemoryStore, bool) {
	if s == nil {
		return nil, false
	}

	return s, true
}

func (s *Store) Trace() (base.TraceStore, bool) {
	if s == nil {
		return nil, false
	}

	return s, true
}
