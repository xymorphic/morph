package agent

import (
	"context"

	models "github.com/wandxy/morph/internal/model"
	storage "github.com/wandxy/morph/internal/state/core"
	"github.com/wandxy/morph/internal/state/search"
	agentcore "github.com/wandxy/morph/pkg/agent"
	agentsession "github.com/wandxy/morph/pkg/agent/session"
)

type ModelList struct {
	Provider string
	AuthType string
	Models   []models.Option
}

type ModelListOptions struct {
	Provider string
}

type ModelSelectOptions struct {
	Provider string
}

type ProviderList struct {
	Providers []models.ProviderOption
}

// ServiceAPI is the agent service surface consumed by RPC, CLI, and TUI adapters.
type ServiceAPI interface {
	Respond(context.Context, string, agentcore.RespondOptions) (string, error)
	ListProviders(context.Context) (ProviderList, error)
	ListModels(context.Context, ...ModelListOptions) (ModelList, error)
	SelectModel(context.Context, string, ...ModelSelectOptions) (models.Option, error)
	SetProviderAPIKey(context.Context, string, string) error
	CreateSession(context.Context, string, ...storage.SessionCreateOptions) (storage.Session, error)
	SaveGatewayBinding(context.Context, storage.GatewayBinding) error
	GetGatewayBinding(context.Context, string) (storage.GatewayBinding, bool, error)
	ListSessions(context.Context, ...storage.SessionListOptions) ([]storage.Session, error)
	UseSession(context.Context, string) error
	ArchiveSession(context.Context, string) error
	UnarchiveSession(context.Context, string) (storage.Session, error)
	RenameSession(context.Context, string, string) (storage.Session, error)
	CurrentSession(context.Context) (storage.Session, error)
	RecallSessionSummary(context.Context, string) (storage.SessionSummary, error)
	CompactSession(context.Context, string) (agentcore.CompactSessionResult, error)
	RepairSession(context.Context, search.VectorRepairOptions) (search.VectorRepairResult, error)
	ContextStatus(context.Context, string) (agentcore.ContextStatus, error)
	GetSessionTimeline(context.Context, SessionTimelineOptions) (SessionTimeline, error)
	AutomationStore(context.Context) (storage.AutomationStore, bool, error)
}

type SessionQueueAPI interface {
	SubmitSessionMessage(context.Context, agentsession.SubmitRequest) (agentsession.QueueEntry, error)
	GetSessionExecutionState(context.Context, string) (agentsession.ExecutionState, error)
	ObserveSessionEvents(context.Context, string, int64, func(agentsession.Event) error) error
	EditSessionQueueEntry(context.Context, agentsession.QueueEditRequest) (agentsession.QueueEntry, error)
	CancelSessionQueueEntry(context.Context, agentsession.QueueMutationRequest) (agentsession.QueueEntry, error)
	PromoteSessionQueueEntry(context.Context, agentsession.QueueMutationRequest) (agentsession.QueueEntry, error)
	InterruptSessionRun(context.Context, string) (agentsession.ActiveRun, bool, error)
}
