package rpc

import (
	"context"

	agentapi "github.com/xymorphic/morph/internal/agent"
	morphauth "github.com/xymorphic/morph/internal/auth"
	"github.com/xymorphic/morph/internal/automation"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/gateway"
	agentstub "github.com/xymorphic/morph/internal/mocks/agentstub"
	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/internal/rpc/rpcmeta"
	"github.com/xymorphic/morph/pkg/gateway/pairing"
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
		Roles: []string{morphauth.RoleOwner}, RootAuthorization: true,
		SessionID: "session", TokenID: "token",
		Source: source, IdentityGeneration: 1, AuthorizationRevision: 1,
	})
}

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
