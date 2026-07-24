package types

import (
	"context"

	"github.com/wandxy/morph/internal/browser"
	planstore "github.com/wandxy/morph/internal/environment/planstore"
	"github.com/wandxy/morph/internal/environment/process"
	sesmsg "github.com/wandxy/morph/internal/environment/sessionmessages"
	sessrc "github.com/wandxy/morph/internal/environment/sessionsearch"
	"github.com/wandxy/morph/internal/guardrails"
	"github.com/wandxy/morph/internal/memory"
	"github.com/wandxy/morph/internal/memory/episodic"
	"github.com/wandxy/morph/internal/permissions"
	storage "github.com/wandxy/morph/internal/state/core"
)

// Runtime exposes environment services needed by tool implementations.
type Runtime interface {
	// Policy management
	FilePolicy() guardrails.FilesystemPolicy
	CommandPolicy() guardrails.CommandPolicy
	CommandShell() string
	CommandIdentityKey() []byte

	// Process management
	StartProcess(ctx context.Context, sessionID string, req process.StartRequest) (process.Info, error)
	GetProcess(sessionID string, processID string) (process.Info, error)
	ReadProcess(sessionID string, req process.ReadRequest) (process.Output, error)
	StopProcess(ctx context.Context, sessionID string, processID string) (process.Info, error)
	ListProcesses(sessionID string) []process.Info

	// Session management
	SearchSession(ctx context.Context, req sessrc.SessionSearchRequest) ([]sessrc.SessionSearchResult, error)
	AutomationService(ctx context.Context) (AutomationService, bool, error)
	BrowserService(ctx context.Context) (BrowserService, bool, error)

	// Memory management
	GetSessionMessages(ctx context.Context, req sesmsg.SessionMessagesRequest) (sesmsg.SessionMessagesResponse, error)
	SupportsMemorySearch(ctx context.Context) (bool, error)
	SearchMemory(ctx context.Context, query memory.SearchQuery) (memory.SearchResult, error)
	SupportsMemoryExtraction(ctx context.Context) (bool, error)
	ExtractEpisodes(ctx context.Context, req episodic.Request) (episodic.Result, error)
	SupportsMemoryWrite(ctx context.Context) (bool, error)
	RecordSemanticMemory(ctx context.Context, record memory.SemanticRecord) (memory.MemoryItem, error)
	RecordProceduralMemory(ctx context.Context, record memory.ProceduralRecord) (memory.MemoryItem, error)
	PromoteMemoryCandidate(ctx context.Context, req memory.PromotionRequest) (memory.LifecycleResult, error)
	UpdateMemory(ctx context.Context, req memory.UpdateRequest) (memory.UpdateResult, error)
	DeleteMemory(ctx context.Context, req memory.DeleteRequest) error

	// Plan management
	GetPlan(string) planstore.Plan
	ReplacePlan(string, planstore.Plan) (planstore.Plan, error)
	MergePlan(string, []planstore.PartialPlanStep, string, bool) (planstore.Plan, error)
	ClearPlan(string) planstore.Plan
	HydratePlan(string, planstore.Plan)
}

type BrowserService interface {
	ResolvePermissionInputs(context.Context, browser.Action, browser.ActionRequest) ([]permissions.EvaluationInput, error)
	Status() browser.Status
	Start(context.Context, browser.StartRequest) (browser.Session, error)
	Stop(context.Context, string) (browser.Session, error)
	Tabs(context.Context, string) ([]browser.Tab, error)
	Open(context.Context, browser.ActionRequest) (browser.Tab, error)
	Focus(context.Context, browser.ActionRequest) (browser.Tab, error)
	CloseTab(context.Context, browser.ActionRequest) (browser.Tab, error)
	Navigate(context.Context, browser.ActionRequest) (browser.Tab, error)
	Reload(context.Context, browser.ActionRequest) (browser.Tab, error)
	Snapshot(context.Context, browser.ActionRequest) (browser.Snapshot, error)
	Screenshot(context.Context, browser.ActionRequest) (browser.Artifact, error)
	PDF(context.Context, browser.ActionRequest) (browser.Artifact, error)
	Console(context.Context, browser.ActionRequest) ([]browser.ConsoleMessage, error)
	Click(context.Context, browser.ActionRequest) (browser.Tab, error)
	Type(context.Context, browser.ActionRequest) (browser.Tab, error)
	Press(context.Context, browser.ActionRequest) (browser.Tab, error)
	Scroll(context.Context, browser.ActionRequest) (browser.Tab, error)
	Select(context.Context, browser.ActionRequest) (browser.Tab, error)
	Upload(context.Context, browser.ActionRequest) (browser.Tab, error)
	Download(context.Context, browser.ActionRequest) (browser.Artifact, error)
	AcceptDialog(context.Context, browser.ActionRequest) (browser.Tab, error)
	DismissDialog(context.Context, browser.ActionRequest) (browser.Tab, error)
	Wait(context.Context, browser.ActionRequest) (browser.Tab, error)
	Back(context.Context, browser.ActionRequest) (browser.Tab, error)
	Forward(context.Context, browser.ActionRequest) (browser.Tab, error)
	ReadArtifact(context.Context, string) (browser.ArtifactContent, error)
	ExportArtifact(context.Context, browser.ArtifactExportRequest) (browser.Artifact, error)
}

type AutomationService interface {
	List(context.Context, storage.AutomationJobQuery) (storage.AutomationJobResult, error)
	Add(context.Context, storage.AutomationJob) (storage.AutomationJob, error)
	Update(context.Context, storage.AutomationJobPatch) (storage.AutomationJob, error)
	Remove(context.Context, string) error
	Run(context.Context, string) (storage.AutomationRun, error)
	Runs(context.Context, storage.AutomationRunQuery) (storage.AutomationRunResult, error)
}
