package tui

import (
	"time"

	rpcclient "github.com/wandxy/morph/internal/rpc/client"
	storage "github.com/wandxy/morph/internal/state/core"
	tuistate "github.com/wandxy/morph/internal/tui/state"
	tuitranscript "github.com/wandxy/morph/internal/tui/transcript"
	"github.com/wandxy/morph/pkg/str"
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

type showCommandViewAction struct {
	TitleIcon       string
	TitleLeft       string
	TitleSubtext    string
	TitleRight      string
	AccentColor     string
	TitleRightColor string
	Content         string
	Height          int
	Kind            string
	Chats           []storage.Session
	Models          []rpcclient.ModelOption
	Providers       []rpcclient.ProviderOption
	ModelProvider   string
	ModelAuthType   string
	PendingModelID  string
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

	iDValue := str.String(action.ID)
	state.sessionID = iDValue.Trim()
	if state.sessionID == "" {
		state.sessionID = defaultSessionID
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
	state.commandView = commandViewState{
		Visible:         true,
		Kind:            kindValue.Trim(),
		TitleIcon:       titleIconValue.Trim(),
		TitleLeft:       titleLeftValue.Trim(),
		TitleSubtext:    titleSubtextValue.Trim(),
		TitleRight:      titleRightValue.Trim(),
		AccentColor:     accentColorValue.Trim(),
		TitleRightColor: titleRightColorValue.Trim(),
		Content:         contentValue.Trim(),
		Height:          max(action.Height, 0),
		Chats:           append([]storage.Session(nil), action.Chats...),
		Models:          append([]rpcclient.ModelOption(nil), action.Models...),
		Providers:       append([]rpcclient.ProviderOption(nil), action.Providers...),
		ModelProvider:   modelProviderValue.Trim(),
		ModelAuthType:   modelAuthTypeValue.Trim(),
		PendingModelID:  pendingModelIDValue.Trim(),
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
