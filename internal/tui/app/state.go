package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/permissions"
	rpcclient "github.com/wandxy/morph/internal/rpc/client"
	storage "github.com/wandxy/morph/internal/state/core"
	agentsession "github.com/wandxy/morph/pkg/agent/session"
)

type tuiState struct {
	width                      int
	height                     int
	status                     statusModel
	sessionID                  string
	sessionTitle               string
	userName                   string
	namePromptEnabled          bool
	setupNamePromptActive      bool
	setupDismissible           bool
	setupOAuthPending          bool
	setupOAuthProvider         string
	setupOAuthCancel           context.CancelFunc
	setupPullCancel            context.CancelFunc
	setupPullEvents            <-chan tea.Msg
	namePromptError            string
	namePromptErrorStartedAt   time.Time
	modelName                  string
	runtimeInfo                runtimeInfo
	context                    string
	fullAccess                 bool
	permissionPolicy           permissions.Policy
	permissionPreset           permissions.Preset
	permissionPresetConfirm    bool
	messages                   []transcriptCell
	live                       transcriptCell
	transcriptGeneration       uint64
	showIntro                  bool
	stream                     markdownStreamCollector
	reasoningStartedAt         time.Time
	reasoningMessageIndex      int
	reasoningMessageIndices    []int
	thinkingCellSequence       uint64
	history                    []string
	historyAt                  int
	draft                      string
	responding                 bool
	responseID                 int
	responseCancel             context.CancelFunc
	responseTranscriptFollow   bool
	responseTranscriptScrolled bool
	responseStartMessageIndex  int
	responseStartedAt          time.Time
	responseRunningToolCount   int
	responseEventStreamActive  bool
	pendingResponseCompletion  *responseCompletedMsg
	sessionExecutionState      rpcclient.SessionExecutionState
	sessionProgressSequences   map[string]int64
	sessionDeferredProgress    []agentsession.ProgressEvent
	renderedUserQueueEntries   map[string]bool
	sessionQueueStale          bool
	sessionObserverCancel      context.CancelFunc
	sessionObserverEvents      <-chan tea.Msg
	sessionObserverSessionID   string
	sessionObserverID          uint64
	sessionQueueFocused        bool
	sessionQueueSelected       int
	sessionQueueEditingEntryID string
	sessionQueueComposerDraft  string
	sessionQueueEditSaving     bool
	toolAnimationFrame         int
	toolAnimationActive        bool
	transcriptRenderSuppressed bool
	transcriptRenderPending    bool
	transcriptResizePending    bool
	streamingRenderAt          time.Time
	streamingFlushPending      bool
	streamingFlushDirty        bool
	thinkingComposerFrame      int
	thinkingComposerActive     bool
	thinkingComposerEnabled    bool
	manualCompactionActive     bool
	manualCompactionIndex      int
	chatSwitching              bool
	commandMenuOffset          int
	commandMenuSelected        int
	commandMenuPrefix          string
	commandView                commandViewState
	commandViewOffset          int
	commandViewSelection       commandViewSelection
	commandViewItemSelected    int
	setupModelStep             string
	setupAuthMethod            string
	setupProviders             []rpcclient.ProviderOption
	setupModels                []rpcclient.ModelOption
	setupModelProvider         string
	setupModelBaseURL          string
	setupProviderAPIKey        string
	setupPendingModelID        string
	setupNoticeTitle           string
	setupNoticeMessage         string
	setupNoticeHint            string
	setupNoticeAction          string
	setupItemSelected          int
	setupOffset                int
	configEnvPath              string
	configPath                 string
	setupSavedConfig           *config.Config
	chatsArchiveConfirm        bool
	chatsRenaming              bool
	chatsRenameSessionID       string
	exitAt                     time.Time
	allowShell                 bool
	selection                  transcriptSelection
	transcriptWindow           transcriptWindowState
	pendingApprovalID          string
	pendingApprovalOrder       []string
	pendingApprovalMessages    map[string]permissionApprovalMsg
	approvalMessageIndices     map[string]int
}

type commandViewState struct {
	Visible         bool
	Kind            string
	TitleIcon       string
	TitleLeft       string
	TitleSubtext    string
	TitleRight      string
	AccentColor     string
	TitleRightColor string
	Content         string
	Height          int
	Chats           []storage.Session
	Models          []rpcclient.ModelOption
	Providers       []rpcclient.ProviderOption
	ModelProvider   string
	ModelAuthType   string
	PendingModelID  string
}

type commandViewSelection struct {
	active   bool
	dragging bool
	content  string
	start    transcriptSelectionPoint
	end      transcriptSelectionPoint
	mouse    tea.Mouse
	scroll   int
	ticking  bool
}

func newTUIState(history []string, thinkingComposerEnabled bool) tuiState {
	return tuiState{
		width:                    defaultWidth,
		height:                   defaultHeight,
		status:                   newStatusModel(),
		sessionID:                defaultSessionID,
		sessionTitle:             defaultSessionTitle,
		runtimeInfo:              defaultRuntimeInfo(),
		showIntro:                true,
		reasoningMessageIndex:    -1,
		manualCompactionIndex:    -1,
		history:                  history,
		historyAt:                len(history),
		thinkingComposerEnabled:  thinkingComposerEnabled,
		responseTranscriptFollow: false,
		approvalMessageIndices:   make(map[string]int),
		pendingApprovalMessages:  make(map[string]permissionApprovalMsg),
	}
}
