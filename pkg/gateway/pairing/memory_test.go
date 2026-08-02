package pairing

import (
	"context"

	"github.com/xymorphic/morph/pkg/str"
)

type memoryStore struct {
	pending          map[string]PendingRequest
	approved         map[string]ApprovedSender
	savePendingErr   error
	getPendingErr    error
	listPendingErr   error
	deletePendingErr error
	saveApprovedErr  error
	getApprovedErr   error
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		pending:  make(map[string]PendingRequest),
		approved: make(map[string]ApprovedSender),
	}
}

func (s *memoryStore) SaveGatewayPairingRequest(_ context.Context, request PendingRequest) error {
	if s.savePendingErr != nil {
		return s.savePendingErr
	}

	s.pending[testKey(request.Source, request.SenderID)] = request
	return nil
}

func (s *memoryStore) GetGatewayPairingRequest(
	_ context.Context,
	source string,
	senderID string,
) (PendingRequest, bool, error) {
	if s.getPendingErr != nil {
		return PendingRequest{}, false, s.getPendingErr
	}

	request, ok := s.pending[testKey(source, senderID)]
	return request, ok, nil
}

func (s *memoryStore) ListGatewayPairingRequests(_ context.Context, source string) ([]PendingRequest, error) {
	if s.listPendingErr != nil {
		return nil, s.listPendingErr
	}

	var requests []PendingRequest
	for _, request := range s.pending {
		sourceValue := str.String(source)
		if sourceValue.Trim() == "" || request.Source == source {
			requests = append(requests, request)
		}
	}

	return requests, nil
}

func (s *memoryStore) DeleteGatewayPairingRequest(_ context.Context, source string, senderID string) error {
	if s.deletePendingErr != nil {
		return s.deletePendingErr
	}

	delete(s.pending, testKey(source, senderID))
	return nil
}

func (s *memoryStore) ClearGatewayPairingRequests(_ context.Context, source string) error {
	for key, request := range s.pending {
		sourceValue2 := str.String(source)
		if sourceValue2.Trim() == "" || request.Source == source {
			delete(s.pending, key)
		}
	}

	return nil
}

func (s *memoryStore) SaveGatewayPairedSender(_ context.Context, sender ApprovedSender) error {
	if s.saveApprovedErr != nil {
		return s.saveApprovedErr
	}

	s.approved[testKey(sender.Source, sender.SenderID)] = sender
	return nil
}

func (s *memoryStore) GetGatewayPairedSender(
	_ context.Context,
	source string,
	senderID string,
) (ApprovedSender, bool, error) {
	if s.getApprovedErr != nil {
		return ApprovedSender{}, false, s.getApprovedErr
	}

	sender, ok := s.approved[testKey(source, senderID)]
	return sender, ok, nil
}

func (s *memoryStore) ListGatewayPairedSenders(_ context.Context, source string) ([]ApprovedSender, error) {
	var senders []ApprovedSender
	for _, sender := range s.approved {
		sourceValue3 := str.String(source)
		if sourceValue3.Trim() == "" || sender.Source == source {
			senders = append(senders, sender)
		}
	}

	return senders, nil
}

func (s *memoryStore) DeleteGatewayPairedSender(_ context.Context, source string, senderID string) error {
	delete(s.approved, testKey(source, senderID))
	return nil
}

func testKey(source string, senderID string) string {
	sourceValue4 := str.String(source)
	senderIDValue := str.String(senderID)
	return sourceValue4.Trim() + "\x00" + senderIDValue.Trim()
}
