package tui

import (
	"time"

	rpcclient "github.com/xymorphic/morph/internal/rpc/client"
	storage "github.com/xymorphic/morph/internal/state/core"
	tuistate "github.com/xymorphic/morph/internal/tui/state"
	tuitranscript "github.com/xymorphic/morph/internal/tui/transcript"
	agentsession "github.com/xymorphic/morph/pkg/agent/session"
	"github.com/xymorphic/morph/pkg/str"
)

type tuiAction interface {
	apply(*tuiState)
}

type setViewportSizeAction struct {
	Width  int
	Height int
}

type appendTranscriptCellAction struct {
	Cell transcriptCell
}

type setTranscriptCellsAction struct {
	Cells []transcriptCell
}

type setLiveTranscriptCellAction struct {
	Cell transcriptCell
}

type clearTranscriptAction struct{}

type replaceTranscriptCellAction struct {
	Index int
	Cell  transcriptCell
}

type setSessionTitleAction struct {
	Title string
}

type setSessionAction struct {
	ID    string
	Title string
}

type setSessionContextAction struct {
	Context string
}

type setSessionReasoningAction struct {
	Settings agentsession.ReasoningSettings
}

type showCommandViewAction struct {
	TitleIcon        string
	TitleLeft        string
	TitleSubtext     string
	TitleRight       string
	AccentColor      string
	TitleRightColor  string
	Content          string
	Kind             string
	Chats            []storage.Session
	Models           []rpcclient.ModelOption
	Providers        []rpcclient.ProviderOption
	ModelProvider    string
	ModelAuthType    string
	PendingModelID   string
	PendingModelAPI  string
	EffortSessionID  string
	EffortModel      agentsession.ReasoningModelTuple
	Efforts          []agentsession.ReasoningEffort
	EffortReasoning  bool
	EffortAdjustable bool
}

type hideCommandViewAction struct{}

type setRespondingAction struct {
	Responding bool
	ResponseID int
}

type resetResponseStateAction struct{}

func (action setViewportSizeAction) apply(state *tuiState) {
	if state == nil {
		return
	}

	viewport := tuistate.NormalizeViewport(action.Width, action.Height)
	state.width = viewport.Width
	state.height = viewport.Height
}

func (action appendTranscriptCellAction) apply(state *tuiState) {
	if state == nil || action.Cell == nil || action.Cell.IsEmpty() {
		return
	}

	state.messages = append(state.messages, action.Cell)
	state.transcriptGeneration++
	state.showIntro = false
}

func (action setTranscriptCellsAction) apply(state *tuiState) {
	if state == nil {
		return
	}

	state.messages = cloneTranscriptCells(action.Cells)
	state.transcriptGeneration++
	state.showIntro = len(state.messages) == 0
}

func (action setLiveTranscriptCellAction) apply(state *tuiState) {
	if state == nil {
		return
	}

	state.live = action.Cell
}

func (clearTranscriptAction) apply(state *tuiState) {
	if state == nil {
		return
	}

	state.messages = nil
	state.live = nil
	state.transcriptGeneration++
	state.showIntro = false
	state.stream.Reset()
	state.reasoningStartedAt = time.Time{}
	state.reasoningMessageIndex = -1
	state.reasoningMessageIndices = nil
	state.thinkingCellSequence = 0
}

func (action replaceTranscriptCellAction) apply(state *tuiState) {
	if state == nil || action.Index < 0 || action.Index >= len(state.messages) {
		return
	}
	if action.Cell == nil || action.Cell.IsEmpty() {
		return
	}

	state.messages[action.Index] = action.Cell
	state.transcriptGeneration++
}

func (action setSessionTitleAction) apply(state *tuiState) {
	if state == nil {
		return
	}

	titleValue := str.String(action.Title)
	state.sessionTitle = titleValue.Trim()
	if state.sessionTitle == "" {
		state.sessionTitle = defaultSessionTitle
	}
}

func (action setSessionAction) apply(state *tuiState) {
	if state == nil {
		return
	}

	previousSessionID := state.sessionID
	iDValue := str.String(action.ID)
	state.sessionID = iDValue.Trim()
	if state.sessionID == "" {
		state.sessionID = defaultSessionID
	}
	if previousSessionID != state.sessionID {
		state.runtimeStateRetryKey = ""
		state.runtimeStateRetryAttempts = 0
	}
	setSessionTitleAction{Title: action.Title}.apply(state)
}

func (action setSessionContextAction) apply(state *tuiState) {
	if state == nil {
		return
	}

	contextValue := str.String(action.Context)
	state.context = contextValue.Trim()
}

func (action setSessionReasoningAction) apply(state *tuiState) {
	if state == nil {
		return
	}
	action.Settings.SupportedEfforts = append(
		[]agentsession.ReasoningEffort(nil),
		action.Settings.SupportedEfforts...,
	)
	if action.Settings.ActiveRunSnapshot != nil {
		snapshot := *action.Settings.ActiveRunSnapshot
		action.Settings.ActiveRunSnapshot = &snapshot
	}
	state.reasoning = action.Settings
}

func (action showCommandViewAction) apply(state *tuiState) {
	if state == nil {
		return
	}

	kindValue := str.String(action.Kind)
	titleIconValue := str.String(action.TitleIcon)
	titleLeftValue := str.String(action.TitleLeft)
	titleSubtextValue := str.String(action.TitleSubtext)
	titleRightValue := str.String(action.TitleRight)
	accentColorValue := str.String(action.AccentColor)
	titleRightColorValue := str.String(action.TitleRightColor)
	contentValue := str.String(action.Content)
	modelProviderValue := str.String(action.ModelProvider)
	modelAuthTypeValue := str.String(action.ModelAuthType)
	pendingModelIDValue := str.String(action.PendingModelID)
	pendingModelAPIValue := str.String(action.PendingModelAPI)
	state.commandView = commandViewState{
		Visible:          true,
		Kind:             kindValue.Trim(),
		TitleIcon:        titleIconValue.Trim(),
		TitleLeft:        titleLeftValue.Trim(),
		TitleSubtext:     titleSubtextValue.Trim(),
		TitleRight:       titleRightValue.Trim(),
		AccentColor:      accentColorValue.Trim(),
		TitleRightColor:  titleRightColorValue.Trim(),
		Content:          contentValue.Trim(),
		Chats:            append([]storage.Session(nil), action.Chats...),
		Models:           append([]rpcclient.ModelOption(nil), action.Models...),
		Providers:        append([]rpcclient.ProviderOption(nil), action.Providers...),
		ModelProvider:    modelProviderValue.Trim(),
		ModelAuthType:    modelAuthTypeValue.Trim(),
		PendingModelID:   pendingModelIDValue.Trim(),
		PendingModelAPI:  pendingModelAPIValue.Trim(),
		EffortSessionID:  str.String(action.EffortSessionID).Trim(),
		EffortModel:      action.EffortModel,
		Efforts:          append([]agentsession.ReasoningEffort(nil), action.Efforts...),
		EffortReasoning:  action.EffortReasoning,
		EffortAdjustable: action.EffortAdjustable,
	}
	state.commandViewOffset = 0
	state.commandViewItemSelected = 0
	state.commandViewSelection = commandViewSelection{}
	state.chatsArchiveConfirm = false
	state.chatsRenaming = false
	state.chatsRenameSessionID = ""
}

func (hideCommandViewAction) apply(state *tuiState) {
	if state == nil {
		return
	}

	state.commandView = commandViewState{}
	state.commandViewOffset = 0
	state.commandViewItemSelected = 0
	state.commandViewSelection = commandViewSelection{}
	state.chatsArchiveConfirm = false
	state.chatsRenaming = false
	state.chatsRenameSessionID = ""
}

func (action setRespondingAction) apply(state *tuiState) {
	if state == nil {
		return
	}

	state.responding = action.Responding
	state.responseID = action.ResponseID
}

func (resetResponseStateAction) apply(state *tuiState) {
	if state == nil {
		return
	}

	state.responding = false
	state.responseTranscriptFollow = false
	state.responseTranscriptScrolled = false
	state.responseStartedAt = time.Time{}
	state.responseRunningToolCount = 0
	state.thinkingComposerActive = false
	state.toolAnimationActive = false
	state.transcriptRenderSuppressed = false
	state.transcriptRenderPending = false
	state.transcriptResizePending = false
	state.streamingRenderAt = time.Time{}
	state.streamingFlushPending = false
	state.streamingFlushDirty = false
	state.responseEventStreamActive = false
	state.pendingResponseCompletion = nil
	state.responseCancel = nil
}

func (m *model) resetResponseState() {
	m.applyAction(resetResponseStateAction{})
	m.events = nil
}

func (m *model) applyAction(action tuiAction) {
	if action == nil {
		return
	}

	action.apply(&m.tuiState)
}

func cloneTranscriptCells(cells []transcriptCell) []transcriptCell {
	return tuitranscript.CloneCells(cells)
}
