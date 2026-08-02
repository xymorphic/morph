package docker

import (
	"sync"

	"github.com/xymorphic/morph/internal/guardrails"
)

type boundedWriter struct {
	mu        sync.Mutex
	limit     int
	data      []byte
	total     int
	truncated bool
	redactor  *guardrails.ExactValueStream
}

func newBoundedWriter(limit int, values []string) *boundedWriter {
	redactor := guardrails.NewExactValueStream()
	for _, value := range values {
		redactor.Register(value)
	}
	return &boundedWriter{
		limit:    limit,
		redactor: redactor,
	}
}

func (w *boundedWriter) Write(value []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	original := len(value)
	w.total += original
	redacted := w.redactor.Redact(value, false)
	if w.limit <= 0 {
		w.limit = 1 << 20
	}
	remaining := w.limit - len(w.data)
	if remaining > 0 {
		if len(redacted) > remaining {
			redacted = redacted[:remaining]
			w.truncated = true
		}
		w.data = append(w.data, redacted...)
	} else if len(redacted) > 0 {
		w.truncated = true
	}
	return original, nil
}

func (w *boundedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushLocked()
	return string(w.data)
}

func (w *boundedWriter) flushLocked() {
	if tail := w.redactor.Flush(); len(tail) > 0 && len(w.data) < w.limit {
		remaining := w.limit - len(w.data)
		if len(tail) > remaining {
			tail = tail[:remaining]
			w.truncated = true
		}
		w.data = append(w.data, tail...)
	}
}

func (w *boundedWriter) Total() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.total
}

func (w *boundedWriter) Truncated() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.truncated
}

func (w *boundedWriter) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	result := append([]byte(nil), w.data...)
	tail := w.redactor.SnapshotTail()
	remaining := w.limit - len(result)
	if len(tail) > remaining {
		tail = tail[:max(remaining, 0)]
	}
	return append(result, tail...)
}
