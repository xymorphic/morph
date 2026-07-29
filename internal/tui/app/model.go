package tui

import (
	"context"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/permissions"
	"github.com/wandxy/morph/internal/profile"
	rpcclient "github.com/wandxy/morph/internal/rpc/client"
	"github.com/wandxy/morph/internal/rpc/rpcmeta"
)

const (
	defaultWidth  = 80
	defaultHeight = 24

	transcriptComposerGapHeight = 1
	baseInputChromeHeight       = inputFrameChromeHeight + bottomStatusPanelHeight + transcriptComposerGapHeight
)

// model is the root Bubble Tea application state for the interactive shell.
type model struct {
	transcript       viewport.Model
	input            textarea.Model
	apiKeyInput      textinput.Model
	baseURLInput     textinput.Model
	modelFilterInput textinput.Model
	nameInput        textinput.Model
	renameInput      textinput.Model
	tuiState
	chatClient           rpcclient.ChatAPI
	sessionClient        rpcclient.SessionAPI
	modelClient          rpcclient.ModelAPI
	permissionClient     rpcclient.PermissionAPI
	browserClient        rpcclient.BrowserAPI
	artifactPathInput    textinput.Model
	pendingArtifact      browserArtifact
	artifactCopies       map[string]browserArtifactCopy
	timeline             sessionTimelineLoader
	title                sessionTitleLoader
	contextLoader        sessionContextLoader
	chatCtx              context.Context
	events               <-chan tea.Msg
	transcriptCache      *transcriptRenderCache
	transcriptBlockCache transcriptBlockCache
	transcriptLayout     transcriptLayoutState
	loadedTimeline       rpcclient.SessionTimeline
	timelineBaseCells    int
	timelinePageLoading  bool
	transcriptRenders    uint64
	transcriptResizes    uint64
}

// newModel builds the initial TUI state and sizes child components.
func newModel() model {
	return newModelWithClientContext(context.Background(), nil)
}

func newModelWithClient(client rpcclient.ChatAPI) model {
	return newModelWithClientContext(context.Background(), client)
}

func newModelWithClientContext(ctx context.Context, client rpcclient.ChatAPI) model {
	return newModelWithClientContextAndConfig(ctx, client, nil)
}

func newModelWithClientContextAndConfig(ctx context.Context, client rpcclient.ChatAPI, cfg *config.Config) model {
	if ctx == nil {
		ctx = context.Background()
	}
	if cfg == nil {
		cfg = config.NewDefaultConfig()
	}
	permissionPolicy := cfg.Permissions
	permissionPolicy.Normalize()
	permissionPreset := permissionPolicy.EffectivePreset()
	ctx = rpcmeta.WithOutgoingPermissionSurface(ctx, permissions.SurfaceTUI)
	ctx = rpcmeta.WithOutgoingPermissionPreset(ctx, permissionPreset)

	history, err := loadPromptHistory()
	userName, userNameSet, namePromptEnabled, userNameErr := loadProfileUserName()
	runtimeInfo := runtimeInfoFromConfig(cfg)
	activeProfile := profile.WithMetadataPaths(profile.Active())
	appModel := model{
		transcript:        newTranscript(),
		input:             newInputComposer(),
		apiKeyInput:       newProviderAPIKeyInput("API key"),
		baseURLInput:      newSetupBaseURLInput(),
		modelFilterInput:  newModelFilterInput(),
		nameInput:         newNameInput(),
		renameInput:       newChatRenameInput(),
		artifactPathInput: newArtifactPathInput(),
		tuiState:          newTUIState(history, cfg.TUIThinkingComposerEnabled()),
		chatClient:        client,
		chatCtx:           ctx,
		artifactCopies:    make(map[string]browserArtifactCopy),
		transcriptCache:   newTranscriptRenderCache(defaultTranscriptRenderCacheCapacity),
	}
	appModel.configEnvPath = activeProfile.EnvPath
	appModel.configPath = activeProfile.ConfigPath
	if userNameSet {
		appModel.userName = userName
	}
	appModel.namePromptEnabled = namePromptEnabled
	if userNameSet && appModel.profileModelSetupMissing() {
		_ = appModel.startProfileSetup(false)
	}
	appModel.runtimeInfo = runtimeInfo
	appModel.modelName = getRuntimeModelDisplayName(runtimeInfo.Provider, runtimeInfo.API, runtimeInfo.Model)
	appModel.permissionPolicy = permissionPolicy
	appModel.permissionPreset = permissionPreset
	appModel.fullAccess = permissionPreset == permissions.PresetFullAccess
	if sessions, ok := client.(rpcclient.SessionAPI); ok {
		appModel.sessionClient = sessions
	}
	if provider, ok := client.(interface{ SessionAPI() rpcclient.SessionAPI }); ok {
		appModel.sessionClient = provider.SessionAPI()
	}
	if models, ok := client.(rpcclient.ModelAPI); ok {
		appModel.modelClient = models
	}
	if provider, ok := client.(interface{ ModelAPI() rpcclient.ModelAPI }); ok {
		appModel.modelClient = provider.ModelAPI()
	}
	if permissions, ok := client.(rpcclient.PermissionAPI); ok {
		appModel.permissionClient = permissions
	}
	if provider, ok := client.(interface {
		PermissionAPI() rpcclient.PermissionAPI
	}); ok {
		appModel.permissionClient = provider.PermissionAPI()
	}
	if browserClient, ok := client.(rpcclient.BrowserAPI); ok {
		appModel.browserClient = browserClient
	}
	if provider, ok := client.(interface{ BrowserAPI() rpcclient.BrowserAPI }); ok {
		appModel.browserClient = provider.BrowserAPI()
	}
	if timeline, ok := appModel.sessionClient.(sessionTimelineLoader); ok {
		appModel.timeline = timeline
	}
	if title, ok := appModel.sessionClient.(sessionTitleLoader); ok {
		appModel.title = title
	}
	if contextLoader, ok := appModel.sessionClient.(sessionContextLoader); ok {
		appModel.contextLoader = contextLoader
	}
	if err != nil {
		setStatusTransient(&appModel.status, "prompt history unavailable")
	}
	if userNameErr != nil {
		setStatusTransient(&appModel.status, "user profile unavailable")
	}
	appModel.resize()
	appModel.setTranscriptContent()

	return appModel
}

// NewModelWithClientContextAndConfig returns a model with client context and config.
func NewModelWithClientContextAndConfig(
	ctx context.Context,
	client rpcclient.ChatAPI,
	cfg *config.Config,
) tea.Model {
	return newModelWithClientContextAndConfig(ctx, client, cfg)
}
