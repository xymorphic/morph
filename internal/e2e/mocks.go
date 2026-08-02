package e2e

import (
	"context"
	"errors"
	"net"
	"time"

	morphagent "github.com/xymorphic/morph/internal/agent"
	"github.com/xymorphic/morph/internal/execution"
	"github.com/xymorphic/morph/internal/permissions"
	rpcclient "github.com/xymorphic/morph/internal/rpc/client"
	storage "github.com/xymorphic/morph/internal/state/core"
	agent "github.com/xymorphic/morph/pkg/agent"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	agentsession "github.com/xymorphic/morph/pkg/agent/session"
	"github.com/xymorphic/morph/pkg/gateway/pairing"
)

type harnessAgentStub struct {
	reply        string
	respondErr   error
	current      string
	currentErr   error
	events       []agent.Event
	created      storage.Session
	createErr    error
	usedID       string
	useErr       error
	compact      agent.CompactSessionResult
	compactErr   error
	turnMessages []morphmsg.Message
}

func (s harnessAgentStub) Respond(_ context.Context, _ string, opts agent.RespondOptions) (string, error) {
	if s.respondErr != nil {
		return "", s.respondErr
	}

	if opts.OnEvent != nil {
		for _, event := range s.events {
			opts.OnEvent(event)
		}
	}
	return s.reply, nil
}

func (s harnessAgentStub) CurrentSession(context.Context) (storage.Session, error) {
	if s.currentErr != nil {
		return storage.Session{}, s.currentErr
	}

	return storage.Session{ID: s.current}, nil
}

func (s *harnessAgentStub) CreateSession(
	context.Context,
	string,
	...storage.SessionCreateOptions,
) (storage.Session, error) {
	if s.createErr != nil {
		return storage.Session{}, s.createErr
	}

	return s.created, nil
}

func (s *harnessAgentStub) Get(
	context.Context,
	string,
	storage.SessionGetOptions,
) (storage.Session, bool, error) {
	return storage.Session{}, false, nil
}

func (s *harnessAgentStub) UseSession(_ context.Context, id string) error {
	if s.useErr != nil {
		return s.useErr
	}

	s.usedID = id
	return nil
}

func (s *harnessAgentStub) CompactSession(context.Context, string) (agent.CompactSessionResult, error) {
	if s.compactErr != nil {
		return agent.CompactSessionResult{}, s.compactErr
	}

	return s.compact, nil
}

func (s *harnessAgentStub) TurnMessages() []morphmsg.Message {
	return s.turnMessages
}

type storageStoreStub struct {
	messages []morphmsg.Message
}

func (s *storageStoreStub) Save(context.Context, storage.Session) error { return nil }
func (s *storageStoreStub) Patch(
	context.Context,
	string,
	storage.SessionPatch,
) (storage.Session, error) {
	return storage.Session{}, nil
}
func (s *storageStoreStub) Get(context.Context, string, storage.SessionGetOptions) (storage.Session, bool, error) {
	return storage.Session{}, false, nil
}
func (s *storageStoreStub) List(context.Context, storage.SessionListOptions) ([]storage.Session, error) {
	return nil, nil
}
func (s *storageStoreStub) Delete(context.Context, string) error { return nil }
func (s *storageStoreStub) Rename(context.Context, storage.SessionRenameRequest) (storage.Session, error) {
	return storage.Session{}, nil
}
func (s *storageStoreStub) UpdateCheckpoints(context.Context, string, storage.CheckpointPatch) error {
	return nil
}
func (s *storageStoreStub) AppendMessages(context.Context, string, []morphmsg.Message) error {
	return nil
}
func (s *storageStoreStub) GetMessages(context.Context, string, storage.MessageQueryOptions) ([]morphmsg.Message, error) {
	return s.messages, nil
}
func (s *storageStoreStub) SearchMessages(context.Context, string, storage.SearchMessageOptions) ([]storage.SearchMessageResult, error) {
	return nil, nil
}
func (s *storageStoreStub) CountMessages(context.Context, string, storage.MessageQueryOptions) (int, error) {
	return 0, nil
}
func (s *storageStoreStub) GetMessage(context.Context, string, int) (morphmsg.Message, bool, error) {
	return morphmsg.Message{}, false, nil
}
func (s *storageStoreStub) GetMessagesByIDs(context.Context, string, []uint) ([]storage.MessageRecord, error) {
	return nil, nil
}
func (s *storageStoreStub) GetMessageWindow(context.Context, string, uint, int, int) ([]storage.MessageRecord, error) {
	return nil, nil
}
func (s *storageStoreStub) SaveSummary(context.Context, storage.SessionSummary) error { return nil }
func (s *storageStoreStub) GetSummary(context.Context, string) (storage.SessionSummary, bool, error) {
	return storage.SessionSummary{}, false, nil
}
func (s *storageStoreStub) DeleteSummary(context.Context, string) error { return nil }
func (s *storageStoreStub) SaveGatewayBinding(context.Context, storage.GatewayBinding) error {
	return nil
}
func (s *storageStoreStub) GetGatewayBinding(context.Context, string) (storage.GatewayBinding, bool, error) {
	return storage.GatewayBinding{}, false, nil
}
func (s *storageStoreStub) SaveGatewayPairingRequest(context.Context, pairing.PendingRequest) error {
	return nil
}
func (s *storageStoreStub) GetGatewayPairingRequest(
	context.Context,
	string,
	string,
) (pairing.PendingRequest, bool, error) {
	return pairing.PendingRequest{}, false, nil
}
func (s *storageStoreStub) ListGatewayPairingRequests(context.Context, string) ([]pairing.PendingRequest, error) {
	return nil, nil
}
func (s *storageStoreStub) DeleteGatewayPairingRequest(context.Context, string, string) error {
	return nil
}
func (s *storageStoreStub) ClearGatewayPairingRequests(context.Context, string) error {
	return nil
}
func (s *storageStoreStub) SaveGatewayPairedSender(context.Context, pairing.ApprovedSender) error {
	return nil
}
func (s *storageStoreStub) GetGatewayPairedSender(
	context.Context,
	string,
	string,
) (pairing.ApprovedSender, bool, error) {
	return pairing.ApprovedSender{}, false, nil
}
func (s *storageStoreStub) ListGatewayPairedSenders(context.Context, string) ([]pairing.ApprovedSender, error) {
	return nil, nil
}
func (s *storageStoreStub) DeleteGatewayPairedSender(context.Context, string, string) error {
	return nil
}
func (s *storageStoreStub) Session() storage.SessionStore { return s }
func (s *storageStoreStub) Automation() (storage.AutomationStore, bool) {
	return nil, false
}
func (s *storageStoreStub) Permission() (permissions.ApprovalStore, bool) { return nil, false }
func (s *storageStoreStub) Memory() (storage.MemoryStore, bool)           { return nil, false }
func (s *storageStoreStub) Trace() (storage.TraceStore, bool)             { return nil, false }
func (s *storageStoreStub) SupportsVectorSearch() bool                    { return false }
func (s *storageStoreStub) Archive(context.Context, string, storage.SessionArchiveRequest) (storage.Session, error) {
	return storage.Session{}, nil
}
func (s *storageStoreStub) Unarchive(context.Context, string) (storage.Session, error) {
	return storage.Session{}, nil
}
func (s *storageStoreStub) DeleteExpiredArchives(context.Context, time.Time) error {
	return nil
}
func (s *storageStoreStub) ClearMessages(context.Context, string) error {
	return nil
}
func (s *storageStoreStub) SetCurrent(context.Context, string) error { return nil }
func (s *storageStoreStub) Current(context.Context) (string, bool, error) {
	return "", false, nil
}
func (s *storageStoreStub) ClearCurrent(context.Context) error { return nil }

type stubAddr string

func (a stubAddr) Network() string { return string(a) }
func (a stubAddr) String() string  { return string(a) }

type stubListener struct {
	addr net.Addr
}

func (l stubListener) Accept() (net.Conn, error) { return nil, errors.New("accept unsupported") }
func (l stubListener) Close() error              { return nil }
func (l stubListener) Addr() net.Addr            { return l.addr }

type rpcAdapterClientStub struct {
	reply      string
	currentErr error
}

func (s rpcAdapterClientStub) EnqueueMessage(
	context.Context,
	rpcclient.EnqueueMessageOptions,
) (rpcclient.SessionQueueEntry, error) {
	return rpcclient.SessionQueueEntry{ID: "que_stub"}, nil
}

func (s rpcAdapterClientStub) State(
	context.Context,
	string,
) (rpcclient.SessionExecutionState, error) {
	return rpcclient.SessionExecutionState{
		Queue: []rpcclient.SessionQueueEntry{{
			ID:     "que_stub",
			Status: agentsession.QueueStatusCompleted,
		}},
	}, nil
}

func (s rpcAdapterClientStub) SetReasoningEffort(
	context.Context,
	rpcclient.SetReasoningEffortOptions,
) (agentsession.ReasoningSettings, error) {
	return agentsession.ReasoningSettings{}, nil
}

func (s rpcAdapterClientStub) Observe(
	context.Context,
	string,
	int64,
	func(rpcclient.SessionEvent) error,
) error {
	return nil
}

func (s rpcAdapterClientStub) EditQueuedMessage(
	context.Context,
	string,
	string,
	string,
) (rpcclient.SessionQueueEntry, error) {
	return rpcclient.SessionQueueEntry{}, nil
}

func (s rpcAdapterClientStub) RemoveQueuedMessage(
	context.Context,
	string,
	string,
) (rpcclient.SessionQueueEntry, error) {
	return rpcclient.SessionQueueEntry{}, nil
}

func (s rpcAdapterClientStub) PromoteQueuedMessage(
	context.Context,
	string,
	string,
) (rpcclient.SessionQueueEntry, error) {
	return rpcclient.SessionQueueEntry{}, nil
}

func (s rpcAdapterClientStub) SteerQueuedMessage(
	context.Context,
	string,
	string,
) (rpcclient.SessionQueueEntry, error) {
	return rpcclient.SessionQueueEntry{}, nil
}

func (s rpcAdapterClientStub) InterruptRun(
	context.Context,
	string,
) (rpcclient.SessionActiveRun, bool, error) {
	return rpcclient.SessionActiveRun{}, false, nil
}

func (s rpcAdapterClientStub) SessionAPI() rpcclient.SessionAPI {
	return s
}

func (s rpcAdapterClientStub) ListExecutionEnvironments(context.Context, string) ([]execution.EnvironmentDetails, error) {
	return nil, nil
}

func (s rpcAdapterClientStub) ExplainExecutionEnvironment(context.Context, string, string) (execution.EnvironmentDetails, error) {
	return execution.EnvironmentDetails{}, nil
}

func (s rpcAdapterClientStub) Current(context.Context) (storage.Session, error) {
	if s.currentErr != nil {
		return storage.Session{}, s.currentErr
	}

	return storage.Session{ID: "default"}, nil
}

func (s rpcAdapterClientStub) Create(context.Context, string) (storage.Session, error) {
	return storage.Session{}, nil
}

func (s rpcAdapterClientStub) CreateWithOptions(context.Context, rpcclient.CreateSessionOptions) (storage.Session, error) {
	return storage.Session{}, nil
}

func (s rpcAdapterClientStub) List(context.Context, ...rpcclient.SessionListOptions) ([]storage.Session, error) {
	return nil, nil
}

func (s rpcAdapterClientStub) Use(context.Context, string) error {
	return nil
}

func (s rpcAdapterClientStub) Archive(context.Context, string) error {
	return nil
}

func (s rpcAdapterClientStub) Unarchive(context.Context, string) (storage.Session, error) {
	return storage.Session{}, nil
}

func (s rpcAdapterClientStub) Rename(context.Context, string, string) (storage.Session, error) {
	return storage.Session{}, nil
}

func (s rpcAdapterClientStub) Compact(context.Context, string) (rpcclient.CompactSessionResult, error) {
	return rpcclient.CompactSessionResult{}, nil
}

func (s rpcAdapterClientStub) Repair(
	context.Context,
	rpcclient.RepairSessionOptions,
) (rpcclient.RepairSessionResult, error) {
	return rpcclient.RepairSessionResult{}, nil
}

func (s rpcAdapterClientStub) Status(context.Context, string) (rpcclient.ContextStatus, error) {
	return rpcclient.ContextStatus{}, nil
}

func (s rpcAdapterClientStub) Timeline(
	context.Context,
	rpcclient.SessionTimelineOptions,
) (rpcclient.SessionTimeline, error) {
	message, _ := morphmsg.NewMessage(morphmsg.RoleAssistant, s.reply)
	return rpcclient.SessionTimeline{
		Messages: []morphagent.SessionTimelineMessage{{Message: message}},
	}, nil
}

func (s rpcAdapterClientStub) Close() error {
	return nil
}
