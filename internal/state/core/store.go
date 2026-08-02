package core

import "github.com/xymorphic/morph/internal/permissions"

// Store defines the aggregate durable state store.
type Store interface {
	Session() SessionStore
	Automation() (AutomationStore, bool)
	Permission() (permissions.ApprovalStore, bool)
	Memory() (MemoryStore, bool)
	Trace() (TraceStore, bool)
	SupportsVectorSearch() bool
}
