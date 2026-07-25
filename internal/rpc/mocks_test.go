package rpc

import (
	"context"
	"errors"
	"io"

	agentapi "github.com/wandxy/morph/internal/agent"
	morphauth "github.com/wandxy/morph/internal/auth"
	"github.com/wandxy/morph/internal/automation"
	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/gateway"
	agentstub "github.com/wandxy/morph/internal/mocks/agentstub"
	"github.com/wandxy/morph/internal/permissions"
	morphpb "github.com/wandxy/morph/internal/rpc/proto"
	"github.com/wandxy/morph/internal/rpc/rpcmeta"
	"github.com/wandxy/morph/internal/trace"
	agent "github.com/wandxy/morph/pkg/agent"
	"github.com/wandxy/morph/pkg/gateway/pairing"
	"google.golang.org/grpc/metadata"
)

func allowedRPCPolicy() permissions.Policy {
	return permissions.Policy{
		Rules: []permissions.Rule{{
			Name:     "allow test RPC operations",
			Decision: permissions.DecisionAllow,
		}},
	}
}

func newAllowedService(api agentapi.ServiceAPI) *Service {
	return NewServiceWithOptions(api, ServiceOptions{PermissionPolicy: allowedRPCPolicy()})
}

func newAllowedServiceWithOptions(api agentapi.ServiceAPI, opts ServiceOptions) *Service {
	opts.PermissionPolicy = allowedRPCPolicy()
	return NewServiceWithOptions(api, opts)
}

func withTestPrincipal(ctx context.Context, source string) context.Context {
	return rpcmeta.WithAuthenticatedPrincipal(ctx, morphauth.Principal{
		IdentityID: "identity", OwnerID: "default", UserID: "identity",
		Roles: []string{morphauth.RoleOwner}, SessionID: "session", TokenID: "token",
		Source: source, IdentityGeneration: 1, AuthorizationRevision: 1,
	})
}

type respondStreamServerStub struct {
	ctx       context.Context
	events    []*morphpb.RespondEvent
	sendErrAt int
}

func (s *respondStreamServerStub) Send(event *morphpb.RespondEvent) error {
	if s.sendErrAt > 0 && len(s.events)+1 == s.sendErrAt {
		return errors.New("send failed")
	}

	s.events = append(s.events, event)
	return nil
}

func (s *respondStreamServerStub) SetHeader(metadata.MD) error  { return nil }
func (s *respondStreamServerStub) SendHeader(metadata.MD) error { return nil }
func (s *respondStreamServerStub) SetTrailer(metadata.MD)       {}
func (s *respondStreamServerStub) Context() context.Context {
	if s.ctx != nil {
		return s.ctx
	}

	return context.Background()
}
func (s *respondStreamServerStub) SendMsg(any) error { return nil }
func (s *respondStreamServerStub) RecvMsg(any) error { return io.EOF }

type gatewayRuntimeStub struct {
	status   gateway.Status
	startCfg config.GatewayConfig
	startCtx context.Context
	stopCtx  context.Context
	started  bool
	stopped  bool
	startErr error
	stopErr  error
}

func (s *gatewayRuntimeStub) Start(
	ctx context.Context,
	cfg config.GatewayConfig,
	_ gateway.AgentService,
) error {
	s.started = true
	s.startCtx = ctx
	s.startCfg = cfg
	if s.startErr != nil {
		return s.startErr
	}
	s.status = gateway.Status{
		State:        gateway.StateRunning,
		Address:      cfg.Address,
		Port:         cfg.Port,
		SlackMode:    cfg.Slack.Mode,
		TelegramMode: cfg.Telegram.Mode,
	}
	return nil
}

func (s *gatewayRuntimeStub) Stop(ctx context.Context) error {
	s.stopped = true
	s.stopCtx = ctx
	if s.stopErr != nil {
		return s.stopErr
	}
	s.status.State = gateway.StateStopped
	return nil
}

func (s *gatewayRuntimeStub) Status() gateway.Status {
	return s.status
}

type channelRespondStub struct {
	agentstub.AgentServiceStub
	channel string
	text    string
}

func (s *channelRespondStub) Respond(_ context.Context, _ string, opts agent.RespondOptions) (string, error) {
	if opts.OnEvent != nil {
		opts.OnEvent(agent.Event{
			Kind:    agent.EventKindTextDelta,
			Channel: s.channel,
			Text:    s.text,
		})
	}
	return "", nil
}

type traceRespondStub struct {
	agentstub.AgentServiceStub
	traceEvent trace.Event
}

func (s *traceRespondStub) Respond(_ context.Context, _ string, opts agent.RespondOptions) (string, error) {
	if opts.OnEvent != nil {
		opts.OnEvent(agent.Event{Kind: agent.EventKindTrace, TraceEvent: &s.traceEvent})
		opts.OnEvent(agent.Event{Kind: agent.EventKindTextDelta, Channel: "assistant", Text: "safe"})
	}
	return "safe", nil
}

type traceSequenceRespondStub struct {
	agentstub.AgentServiceStub
	reply       string
	deltas      []agent.Event
	traceEvents []trace.Event
}

func (s *traceSequenceRespondStub) Respond(_ context.Context, _ string, opts agent.RespondOptions) (string, error) {
	for _, delta := range s.deltas {
		if opts.OnEvent != nil {
			opts.OnEvent(delta)
		}
	}
	for _, event := range s.traceEvents {
		if opts.OnEvent != nil {
			opts.OnEvent(agent.Event{Kind: agent.EventKindTrace, TraceEvent: &event})
		}
	}

	return s.reply, nil
}

type bufferedReplyStub struct {
	agentstub.AgentServiceStub
	reply             string
	capturedSessionID string
}

func (s *bufferedReplyStub) Respond(_ context.Context, _ string, opts agent.RespondOptions) (string, error) {
	s.capturedSessionID = opts.SessionID
	return s.reply, nil
}

type serviceAPIWithoutPairingStore struct {
	agentapi.ServiceAPI
}

type gatewayPairingStoreStub struct {
	*agentstub.AgentServiceStub
	listPendingErr error
	listPairedErr  error
}

func (s *gatewayPairingStoreStub) ListGatewayPairingRequests(
	context.Context,
	string,
) ([]pairing.PendingRequest, error) {
	return nil, s.listPendingErr
}

func (s *gatewayPairingStoreStub) ListGatewayPairedSenders(
	context.Context,
	string,
) ([]pairing.ApprovedSender, error) {
	return nil, s.listPairedErr
}

type automationAPIStub struct {
	status    automation.Status
	listQuery automation.JobQuery
	added     automation.Job
	patch     automation.JobPatch
	removedID string
	runID     string
	run       automation.Run
	runQuery  automation.RunQuery
	err       error
}

func (s *automationAPIStub) Status(context.Context) (automation.Status, error) {
	if s.err != nil {
		return automation.Status{}, s.err
	}

	return s.status, nil
}

func (s *automationAPIStub) List(_ context.Context, query automation.JobQuery) (automation.JobList, error) {
	if s.err != nil {
		return automation.JobList{}, s.err
	}

	s.listQuery = query
	return automation.JobList{Jobs: []automation.Job{s.added}}, nil
}

func (s *automationAPIStub) Add(_ context.Context, job automation.Job) (automation.Job, error) {
	if s.err != nil {
		return automation.Job{}, s.err
	}

	s.added = job
	if s.added.ID == "" {
		s.added.ID = testRPCAutomationJobID
	}
	return s.added, nil
}

func (s *automationAPIStub) Update(_ context.Context, patch automation.JobPatch) (automation.Job, error) {
	if s.err != nil {
		return automation.Job{}, s.err
	}

	s.patch = patch
	return automation.Job{ID: patch.ID, Name: valueOrZero(patch.Name)}, nil
}

func (s *automationAPIStub) Remove(_ context.Context, id string) error {
	if s.err != nil {
		return s.err
	}

	s.removedID = id
	return nil
}

func (s *automationAPIStub) Run(_ context.Context, id string) (automation.Run, error) {
	if s.err != nil {
		return automation.Run{}, s.err
	}

	s.runID = id
	if s.run.ID == "" {
		s.run = automation.Run{ID: testRPCAutomationRunID, JobID: id, Status: automation.RunStatusOK}
	}
	return s.run, nil
}

func (s *automationAPIStub) Runs(_ context.Context, query automation.RunQuery) (automation.RunList, error) {
	if s.err != nil {
		return automation.RunList{}, s.err
	}

	s.runQuery = query
	return automation.RunList{Runs: []automation.Run{s.run}}, nil
}
