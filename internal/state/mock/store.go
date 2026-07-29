package mock

import (
	"context"
	"time"

	"github.com/wandxy/morph/internal/permissions"
	storage "github.com/wandxy/morph/internal/state/core"
	morphmsg "github.com/wandxy/morph/pkg/agent/message"
	"github.com/wandxy/morph/pkg/gateway/pairing"
)

// Store records state-store calls for tests.
type Store struct {
	GetFunc                   func(context.Context, string) (storage.Session, bool, error)
	GetWithOptionsFunc        func(context.Context, string, storage.SessionGetOptions) (storage.Session, bool, error)
	GetSummaryFunc            func(context.Context, string) (storage.SessionSummary, bool, error)
	ListFunc                  func(context.Context) ([]storage.Session, error)
	ListWithOptionsFunc       func(context.Context, storage.SessionListOptions) ([]storage.Session, error)
	RenameFunc                func(context.Context, storage.SessionRenameRequest) (storage.Session, error)
	SaveFunc                  func(context.Context, storage.Session) error
	PatchFunc                 func(context.Context, string, storage.SessionPatch) (storage.Session, error)
	SaveSummaryFunc           func(context.Context, storage.SessionSummary) error
	SaveGatewayBindingFunc    func(context.Context, storage.GatewayBinding) error
	DeleteFunc                func(context.Context, string) error
	DeleteSummaryFunc         func(context.Context, string) error
	GetGatewayBindingFunc     func(context.Context, string) (storage.GatewayBinding, bool, error)
	UpdateCheckpointsFunc     func(context.Context, string, storage.CheckpointPatch) error
	SetCurrentFunc            func(context.Context, string) error
	CurrentFunc               func(context.Context) (string, bool, error)
	ClearCurrentFunc          func(context.Context) error
	AppendMessagesFunc        func(context.Context, string, []morphmsg.Message) error
	CountMessagesFunc         func(context.Context, string, storage.MessageQueryOptions) (int, error)
	GetMessageFunc            func(context.Context, string, int) (morphmsg.Message, bool, error)
	GetMessagesFunc           func(context.Context, string, storage.MessageQueryOptions) ([]morphmsg.Message, error)
	GetMessagesByIDsFunc      func(context.Context, string, []uint) ([]storage.MessageRecord, error)
	GetMessageWindowFunc      func(context.Context, string, uint, int, int) ([]storage.MessageRecord, error)
	SearchMessagesFunc        func(context.Context, string, storage.SearchMessageOptions) ([]storage.SearchMessageResult, error)
	ClearMessagesFunc         func(context.Context, string) error
	ArchiveFunc               func(context.Context, string, storage.SessionArchiveRequest) (storage.Session, error)
	UnarchiveFunc             func(context.Context, string) (storage.Session, error)
	DeleteExpiredArchivesFunc func(context.Context, time.Time) error
	AutomationStore           storage.AutomationStore
	MemoryStore               storage.MemoryStore
	TraceStore                storage.TraceStore
	VectorSearchSupported     bool
}

func (s *Store) Session() storage.SessionStore {
	return s
}

func (s *Store) Automation() (storage.AutomationStore, bool) {
	if s == nil || s.AutomationStore == nil {
		return nil, false
	}

	return s.AutomationStore, true
}

func (s *Store) Permission() (permissions.ApprovalStore, bool) { return nil, false }

func (s *Store) Memory() (storage.MemoryStore, bool) {
	if s == nil || s.MemoryStore == nil {
		return nil, false
	}

	return s.MemoryStore, true
}

func (s *Store) Trace() (storage.TraceStore, bool) {
	if s == nil || s.TraceStore == nil {
		return nil, false
	}

	return s.TraceStore, true
}

func (s *Store) SupportsVectorSearch() bool {
	return s != nil && s.VectorSearchSupported
}

func (s *Store) Save(ctx context.Context, session storage.Session) error {
	if s.SaveFunc != nil {
		return s.SaveFunc(ctx, session)
	}

	return nil
}

func (s *Store) Patch(
	ctx context.Context,
	id string,
	patch storage.SessionPatch,
) (storage.Session, error) {
	if s.PatchFunc != nil {
		return s.PatchFunc(ctx, id, patch)
	}

	return storage.Session{}, nil
}

func (s *Store) Get(ctx context.Context, id string, opts storage.SessionGetOptions) (storage.Session, bool, error) {
	if s.GetWithOptionsFunc != nil {
		return s.GetWithOptionsFunc(ctx, id, opts)
	}
	if s.GetFunc != nil {
		return s.GetFunc(ctx, id)
	}

	return storage.Session{}, false, nil
}

func (s *Store) SaveSummary(ctx context.Context, summary storage.SessionSummary) error {
	if s.SaveSummaryFunc != nil {
		return s.SaveSummaryFunc(ctx, summary)
	}

	return nil
}

func (s *Store) GetSummary(ctx context.Context, sessionID string) (storage.SessionSummary, bool, error) {
	if s.GetSummaryFunc != nil {
		return s.GetSummaryFunc(ctx, sessionID)
	}

	return storage.SessionSummary{}, false, nil
}

func (s *Store) SaveGatewayBinding(ctx context.Context, binding storage.GatewayBinding) error {
	if s.SaveGatewayBindingFunc != nil {
		return s.SaveGatewayBindingFunc(ctx, binding)
	}

	return nil
}

func (s *Store) GetGatewayBinding(ctx context.Context, key string) (storage.GatewayBinding, bool, error) {
	if s.GetGatewayBindingFunc != nil {
		return s.GetGatewayBindingFunc(ctx, key)
	}

	return storage.GatewayBinding{}, false, nil
}

func (s *Store) SaveGatewayPairingRequest(context.Context, pairing.PendingRequest) error {
	return nil
}

func (s *Store) GetGatewayPairingRequest(
	context.Context,
	string,
	string,
) (pairing.PendingRequest, bool, error) {
	return pairing.PendingRequest{}, false, nil
}

func (s *Store) ListGatewayPairingRequests(context.Context, string) ([]pairing.PendingRequest, error) {
	return nil, nil
}

func (s *Store) DeleteGatewayPairingRequest(context.Context, string, string) error {
	return nil
}

func (s *Store) ClearGatewayPairingRequests(context.Context, string) error {
	return nil
}

func (s *Store) SaveGatewayPairedSender(context.Context, pairing.ApprovedSender) error {
	return nil
}

func (s *Store) GetGatewayPairedSender(context.Context, string, string) (pairing.ApprovedSender, bool, error) {
	return pairing.ApprovedSender{}, false, nil
}

func (s *Store) ListGatewayPairedSenders(context.Context, string) ([]pairing.ApprovedSender, error) {
	return nil, nil
}

func (s *Store) DeleteGatewayPairedSender(context.Context, string, string) error {
	return nil
}

func (s *Store) DeleteSummary(ctx context.Context, sessionID string) error {
	if s.DeleteSummaryFunc != nil {
		return s.DeleteSummaryFunc(ctx, sessionID)
	}

	return nil
}

func (s *Store) List(ctx context.Context, opts storage.SessionListOptions) ([]storage.Session, error) {
	if s.ListWithOptionsFunc != nil {
		return s.ListWithOptionsFunc(ctx, opts)
	}
	if s.ListFunc != nil {
		return s.ListFunc(ctx)
	}

	return nil, nil
}

func (s *Store) Rename(ctx context.Context, req storage.SessionRenameRequest) (storage.Session, error) {
	if s.RenameFunc != nil {
		return s.RenameFunc(ctx, req)
	}

	return storage.Session{}, nil
}

func (s *Store) Delete(ctx context.Context, id string) error {
	if s.DeleteFunc != nil {
		return s.DeleteFunc(ctx, id)
	}

	return nil
}

func (s *Store) UpdateCheckpoints(ctx context.Context, id string, patch storage.CheckpointPatch) error {
	if s.UpdateCheckpointsFunc != nil {
		return s.UpdateCheckpointsFunc(ctx, id, patch)
	}

	return nil
}

func (s *Store) Archive(ctx context.Context, id string, req storage.SessionArchiveRequest) (storage.Session, error) {
	if s.ArchiveFunc != nil {
		return s.ArchiveFunc(ctx, id, req)
	}

	return storage.Session{}, nil
}

func (s *Store) Unarchive(ctx context.Context, id string) (storage.Session, error) {
	if s.UnarchiveFunc != nil {
		return s.UnarchiveFunc(ctx, id)
	}

	return storage.Session{}, nil
}

func (s *Store) DeleteExpiredArchives(ctx context.Context, now time.Time) error {
	if s.DeleteExpiredArchivesFunc != nil {
		return s.DeleteExpiredArchivesFunc(ctx, now)
	}

	return nil
}

func (s *Store) SetCurrent(ctx context.Context, id string) error {
	if s.SetCurrentFunc != nil {
		return s.SetCurrentFunc(ctx, id)
	}

	return nil
}

func (s *Store) Current(ctx context.Context) (string, bool, error) {
	if s.CurrentFunc != nil {
		return s.CurrentFunc(ctx)
	}

	return "", false, nil
}

func (s *Store) ClearCurrent(ctx context.Context) error {
	if s.ClearCurrentFunc != nil {
		return s.ClearCurrentFunc(ctx)
	}

	return nil
}

func (s *Store) AppendMessages(ctx context.Context, id string, messages []morphmsg.Message) error {
	if s.AppendMessagesFunc != nil {
		return s.AppendMessagesFunc(ctx, id, messages)
	}

	return nil
}

func (s *Store) CountMessages(ctx context.Context, id string, opts storage.MessageQueryOptions) (int, error) {
	if s.CountMessagesFunc != nil {
		return s.CountMessagesFunc(ctx, id, opts)
	}

	return 0, nil
}

func (s *Store) GetMessage(ctx context.Context, id string, index int) (morphmsg.Message, bool, error) {
	if s.GetMessageFunc != nil {
		return s.GetMessageFunc(ctx, id, index)
	}

	return morphmsg.Message{}, false, nil
}

func (s *Store) GetMessages(ctx context.Context, id string, opts storage.MessageQueryOptions) ([]morphmsg.Message, error) {
	if s.GetMessagesFunc != nil {
		return s.GetMessagesFunc(ctx, id, opts)
	}

	return nil, nil
}

func (s *Store) GetMessagesByIDs(ctx context.Context, id string, messageIDs []uint) ([]storage.MessageRecord, error) {
	if s.GetMessagesByIDsFunc != nil {
		return s.GetMessagesByIDsFunc(ctx, id, messageIDs)
	}

	return nil, nil
}

func (s *Store) GetMessageWindow(ctx context.Context, id string, anchorMessageID uint, before int, after int) ([]storage.MessageRecord, error) {
	if s.GetMessageWindowFunc != nil {
		return s.GetMessageWindowFunc(ctx, id, anchorMessageID, before, after)
	}

	return nil, nil
}

func (s *Store) SearchMessages(ctx context.Context, id string, opts storage.SearchMessageOptions) ([]storage.SearchMessageResult, error) {
	if s.SearchMessagesFunc != nil {
		return s.SearchMessagesFunc(ctx, id, opts)
	}

	return nil, nil
}

func (s *Store) ClearMessages(ctx context.Context, id string) error {
	if s.ClearMessagesFunc != nil {
		return s.ClearMessagesFunc(ctx, id)
	}

	return nil
}
