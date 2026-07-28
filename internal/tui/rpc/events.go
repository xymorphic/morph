package rpc

// ResponseEvent represents a response event.
type ResponseEvent struct {
	ResponseID int
	Message    any
}

// ResponseEventsClosed describes response events closed.
type ResponseEventsClosed struct {
	ResponseID int
}

// ResponseCompleted describes response completed.
type ResponseCompleted struct {
	ResponseID   int
	QueueEntryID string
	Text         string
	Err          error
}
