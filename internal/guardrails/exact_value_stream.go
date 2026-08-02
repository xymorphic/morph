package guardrails

import (
	"bytes"
	"sort"
	"sync"
)

var exactValueReplacement = []byte("[REDACTED]")

type ExactValueStream struct {
	mu      sync.Mutex
	values  [][]byte
	pending []byte
}

func NewExactValueStream(values ...string) *ExactValueStream {
	stream := &ExactValueStream{}
	for _, value := range values {
		stream.Register(value)
	}
	return stream
}

func (s *ExactValueStream) Register(value string) {
	if s == nil || value == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	secret := []byte(value)
	for _, existing := range s.values {
		if bytes.Equal(existing, secret) {
			return
		}
	}
	s.values = append(s.values, bytes.Clone(secret))
	sort.Slice(s.values, func(i, j int) bool {
		return len(s.values[i]) > len(s.values[j])
	})
}

func (s *ExactValueStream) Redact(chunk []byte, final bool) []byte {
	if s == nil {
		return bytes.Clone(chunk)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	input := make([]byte, 0, len(s.pending)+len(chunk))
	input = append(input, s.pending...)
	input = append(input, chunk...)
	s.pending = nil

	if len(s.values) == 0 {
		return input
	}

	output := make([]byte, 0, len(input))
	for index := 0; index < len(input); {
		if value := s.getMatch(input[index:]); value != nil {
			output = append(output, exactValueReplacement...)
			index += len(value)
			continue
		}
		if !final && s.isPossiblePrefix(input[index:]) {
			s.pending = append(s.pending, input[index:]...)
			break
		}
		output = append(output, input[index])
		index++
	}

	return output
}

func (s *ExactValueStream) Flush() []byte {
	return s.Redact(nil, true)
}

func (s *ExactValueStream) SnapshotTail() []byte {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return nil
	}
	return bytes.Clone(exactValueReplacement)
}

func (s *ExactValueStream) getMatch(input []byte) []byte {
	for _, value := range s.values {
		if len(input) >= len(value) && bytes.Equal(input[:len(value)], value) {
			return value
		}
	}
	return nil
}

func (s *ExactValueStream) isPossiblePrefix(input []byte) bool {
	for _, value := range s.values {
		if len(input) < len(value) && bytes.Equal(input, value[:len(input)]) {
			return true
		}
	}
	return false
}
