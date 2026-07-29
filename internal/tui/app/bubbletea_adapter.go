package tui

import (
	"errors"

	tea "charm.land/bubbletea/v2"

	"github.com/wandxy/morph/internal/permissions"
	"github.com/wandxy/morph/internal/rpc/rpcmeta"
)

// Init focuses the input composer when Bubble Tea starts the program.
func (m model) Init() tea.Cmd {
	return tea.Batch(
		inputHandledCmd(m.input.Focus()),
		m.statusExpireCmd(),
		m.loadStartupSessionTimeline(),
	)
}

// Update adapts Bubble Tea terminal messages into app-level TUI events.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if next, cmd, handled := m.handleLifecycleMsg(msg); handled {
		return next, cmd
	}
	if next, cmd, handled := m.handleAsyncMsg(msg); handled {
		return next, cmd
	}
	if next, cmd, handled := m.handleTerminalMsg(msg); handled {
		return next, cmd
	}

	return m.updateBubbleTeaChildren(msg)
}

func (m model) handleLifecycleMsg(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		next, cmd := m.handleAppEvent(viewportResizedEvent{Width: msg.Width, Height: msg.Height})
		return next, cmd, true
	case exitConfirmationExpiredMsg:
		return m.expireExitConfirmation(msg), nil, true
	case statusExpiredMsg:
		expireStatus(&m.status, msg)
		return m, nil, true
	case namePromptErrorExpiredMsg:
		return m.expireNamePromptError(msg), nil, true
	case toolAnimationTickMsg:
		next, cmd := m.updateToolAnimation()
		return next, cmd, true
	case streamingTranscriptFlushMsg:
		next, cmd := m.flushStreamingTranscript(msg)
		return next, cmd, true
	case thinkingComposerTickMsg:
		next, cmd := m.updateThinkingComposer()
		return next, cmd, true
	case transcriptSelectionAutoScrollTickMsg:
		next, cmd := m.updateTranscriptSelectionAutoScroll()
		return next, cmd, true
	case commandViewSelectionAutoScrollTickMsg:
		next, cmd := m.updateCommandViewSelectionAutoScroll()
		return next, cmd, true
	default:
		return m, nil, false
	}
}

func (m model) handleAsyncMsg(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case assistantTextDeltaMsg:
		next, cmd := m.handleAppEvent(applyTUIMessageEvent{Message: msg})
		return next, cmd, true
	case assistantResponseCompletedMsg:
		next, cmd := m.handleAppEvent(applyTUIMessageEvent{Message: msg})
		return next, cmd, true
	case reasoningCompletedMsg:
		next, cmd := m.handleAppEvent(applyTUIMessageEvent{Message: msg})
		return next, cmd, true
	case responseEventMsg:
		next, cmd := m.handleResponseEvent(msg)
		return next, cmd, true
	case responseEventBatchMsg:
		next, cmd := m.handleResponseEventBatch(msg)
		return next, cmd, true
	case responseEventsClosedMsg:
		cmd := m.handleResponseEventsClosed(msg)
		return m, cmd, true
	case responseCompletedMsg:
		cmd := m.handleResponseCompleted(msg)
		return m, cmd, true
	case responsePermissionApprovalsMsg:
		if !m.isActiveResponse(msg.ResponseID) {
			return m, nil, true
		}
		if msg.Err != nil {
			return m, tea.Batch(
				m.setStatus(
					"permission approvals unavailable: "+
						getUserFacingErrorMessage(msg.Err.Error()),
				),
				pollResponsePermissionApprovalsCmd(
					m.chatCtx,
					m.permissionClient,
					msg.ResponseID,
					m.getCurrentSessionID(),
				),
			), true
		}
		for _, request := range msg.Requests {
			m.updatePermissionApproval(permissionApprovalMessageFromRequest(request))
		}
		return m, pollResponsePermissionApprovalsCmd(
			m.chatCtx,
			m.permissionClient,
			msg.ResponseID,
			m.getCurrentSessionID(),
		), true
	case sessionTimelineLoadedMsg:
		m.chatSwitching = false
		m.sessionQueueStale = true
		m.cleanupBrowserArtifactCopies()
		m.transcriptCache.clear()
		next, cmd := m.handleAppEvent(hydrateTimelineEvent{Timeline: msg.Timeline})
		return next, tea.Batch(
			cmd,
			loadSessionRuntimeStateCmd(
				m.chatCtx,
				m.chatClient,
				m.modelClient,
				msg.Timeline.SessionID,
			),
		), true
	case olderSessionTimelineLoadedMsg:
		m.prependOlderSessionTimeline(msg.Timeline, msg.Options)
		return m, nil, true
	case olderSessionTimelineLoadFailedMsg:
		if msg.SessionID == m.getCurrentSessionID() {
			m.timelinePageLoading = false
			return m, m.setStatus("older transcript unavailable"), true
		}
		return m, nil, true
	case sessionExecutionStateLoadedMsg:
		cmd := m.applySessionExecutionState(msg)
		return m, cmd, true
	case reasoningEffortSetMsg:
		cmd := m.completeReasoningEffortSet(msg)
		return m, cmd, true
	case sessionExecutionStateLoadFailedMsg:
		m.sessionQueueStale = true
		return m, tea.Batch(
			m.setStatus("session queue unavailable; retrying"),
			retrySessionRuntimeStateLoadCmd(
				m.chatCtx,
				m.chatClient,
				m.modelClient,
				m.getCurrentSessionID(),
			),
		), true
	case sessionQueueEventMsg:
		cmd := m.applySessionQueueEvent(msg)
		return m, cmd, true
	case sessionQueueEventsClosedMsg:
		cmd := m.handleSessionQueueEventsClosed(msg)
		return m, cmd, true
	case sessionQueueMutationCompletedMsg:
		cmd := m.completeSessionQueueMutation(msg)
		return m, cmd, true
	case sessionInterruptCompletedMsg:
		cmd := m.completeSessionInterrupt(msg)
		return m, cmd, true
	case sessionTimelineLoadFailedMsg:
		m.chatSwitching = false
		cmd := m.setStatus("session timeline unavailable")
		return m, cmd, true
	case sessionTitleLoadedMsg:
		m.refreshSessionTitleFromSession(msg.Session)
		return m, nil, true
	case sessionTitleLoadFailedMsg:
		return m, nil, true
	case sessionContextLoadedMsg:
		m.refreshSessionContext(msg.Status)
		return m, nil, true
	case sessionContextLoadFailedMsg:
		return m, nil, true
	case sessionErrorMsg:
		next, cmd := m.handleAppEvent(applyTUIMessageEvent{Message: msg})
		return next, cmd, true
	case compactSessionCompletedMsg:
		cmd := m.completeCompactSession(msg)
		return m, cmd, true
	case newChatCompletedMsg:
		m.cleanupBrowserArtifactCopies()
		cmd := m.completeNewChat(msg)
		return m, cmd, true
	case chatsLoadedMsg:
		cmd := m.completeChatsCommand(msg)
		return m, cmd, true
	case archivedChatsLoadedMsg:
		cmd := m.completeArchiveCommand(msg)
		return m, cmd, true
	case providersLoadedMsg:
		cmd := m.completeProvidersCommand(msg)
		return m, cmd, true
	case modelsLoadedMsg:
		cmd := m.completeModelsCommand(msg)
		return m, cmd, true
	case chatArchivedMsg:
		next, cmd := m.updateCommandView(msg)
		return next, cmd, true
	case chatUnarchivedMsg:
		next, cmd := m.updateCommandView(msg)
		return next, cmd, true
	case chatRenamedMsg:
		next, cmd := m.updateCommandView(msg)
		return next, cmd, true
	case modelSelectedMsg:
		next, cmd := m.completeSelectModel(msg)
		return next, cmd, true
	case providerAPIKeySetMsg:
		next, cmd := m.completeProviderAPIKeySet(msg)
		return next, cmd, true
	case setupOAuthOutputMsg:
		next, cmd := m.updateSetupOAuthOutput(msg)
		return next, cmd, true
	case setupOAuthCompletedMsg:
		next, cmd := m.completeSetupOAuthLogin(msg)
		return next, cmd, true
	case setupModelOptionsLoadedMsg:
		next, cmd := m.completeSetupModelOptionsRefresh(msg)
		return next, cmd, true
	case setupModelPullProgressMsg:
		next, cmd := m.updateSetupModelPullProgress(msg)
		return next, cmd, true
	case setupModelPullCompletedMsg:
		next, cmd := m.completeSetupModelPull(msg)
		return next, cmd, true
	case setupModelPullClosedMsg:
		return m, nil, true
	case setupModelRuntimeSelectedMsg:
		next, cmd := m.completeSetupModelRuntimeSelection(msg)
		return next, cmd, true
	case toolInvocationStartedMsg:
		next, cmd := m.handleAppEvent(applyTUIMessageEvent{Message: msg})
		return next, cmd, true
	case toolInvocationCompletedMsg:
		next, cmd := m.handleAppEvent(applyTUIMessageEvent{Message: msg})
		return next, cmd, true
	case browserArtifactActionResultMsg:
		cmd := m.completeBrowserArtifactAction(msg)
		return m, cmd, true
	case browserArtifactExpiryMsg:
		m.expireBrowserArtifactCopy(msg)
		return m, nil, true
	case safetyEventMsg:
		next, cmd := m.handleAppEvent(applyTUIMessageEvent{Message: msg})
		return next, cmd, true
	case permissionApprovalMsg:
		next, cmd := m.handleAppEvent(applyTUIMessageEvent{Message: msg})
		return next, cmd, true
	case permissionResolutionCompletedMsg:
		if msg.Err != nil {
			failed := m.pendingApprovalMessages[msg.RequestID]
			failed.RequestID = msg.RequestID
			failed.Status = string(permissions.ApprovalFailed)
			failed.Reason = msg.Err.Error()
			m.updatePermissionApproval(failed)
			cmd := m.setStatus("approval failed: " + msg.Err.Error())
			return m, cmd, true
		}
		next, cmd := m.handleAppEvent(applyTUIMessageEvent{Message: permissionApprovalMsg{
			RequestID: msg.RequestID, Status: msg.Status, Scope: msg.Scope, Summary: msg.Summary,
			Reason: msg.Reason, Effects: msg.Effects, Operations: msg.Operations, ExpiresAt: msg.ExpiresAt,
		}})
		return next, cmd, true
	case permissionPresetPersistedMsg:
		if msg.Err != nil {
			cmd := m.setStatus("permission preset not saved: " + msg.Err.Error())
			return m, cmd, true
		}
		return m, nil, true
	default:
		return m, nil, false
	}
}

func (m model) handleTerminalMsg(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tea.PasteMsg:
		if m.shouldShowNamePrompt() {
			next, cmd := m.handleNamePromptPaste(msg)
			return next, cmd, true
		}
		if m.shouldShowProfileModelSetup() {
			next, cmd := m.handleProfileModelSetupPaste(msg)
			return next, cmd, true
		}
		if m.isCommandViewVisible() {
			next, cmd := m.updateCommandView(msg)
			return next, cmd, true
		}
		next, cmd := m.handlePasteMsg(msg)
		return next, cmd, true
	case tea.KeyPressMsg:
		if msg.Keystroke() == "ctrl+c" {
			next, cmd := m.confirmExit()
			return next, cmd, true
		}
		if m.shouldShowNamePrompt() {
			next, cmd := m.handleNamePromptKey(msg)
			return next, cmd, true
		}
		if m.shouldShowProfileModelSetup() {
			next, cmd := m.handleProfileModelSetupKey(msg)
			return next, cmd, true
		}
		if next, cmd, ok := m.handleKeyPressMsg(msg); ok {
			return next, cmd, true
		}
		next, cmd := m.updateInputComposer(msg)
		return next, cmd, true
	case tea.MouseWheelMsg:
		if m.shouldShowProfileModelSetup() {
			next, cmd := m.handleProfileModelSetupWheel(msg)
			return next, cmd, true
		}
		if m.isCommandViewVisible() {
			next, cmd := m.updateCommandView(msg)
			return next, cmd, true
		}
		if m.scrollCommandMenuWithMouse(msg) {
			return m, nil, true
		}
		next, cmd := m.updateTranscriptWithScrollTracking(msg)
		return next, cmd, true
	case tea.MouseClickMsg:
		if m.shouldShowProfileModelSetup() {
			next, cmd := m.handleProfileModelSetupClick(msg)
			return next, cmd, true
		}
		if m.isCommandViewVisible() {
			if m.isPermissionApprovalCommandView() || m.isEffortCommandView() {
				next, cmd := m.updateCommandView(msg)
				return next, cmd, true
			}
			if m.startCommandViewSelection(msg) {
				return m, nil, true
			}

			return m, nil, true
		}
		if cmd, ok := m.openTranscriptLinkAtMouse(msg); ok {
			return m, cmd, true
		}
		if m.clicksJumpToBottomIndicator(msg) {
			next, cmd := m.handleAppEvent(jumpTranscriptToBottomEvent{})
			return next, cmd, true
		}
		if cmd, handled := m.handleSessionQueueClick(msg); handled {
			return m, cmd, true
		}
		if m.startTranscriptSelection(msg) {
			return m, nil, true
		}
	case tea.MouseMotionMsg:
		if m.shouldShowProfileModelSetup() {
			return m, nil, true
		}
		if handled, cmd := m.updateCommandViewSelection(msg); handled {
			return m, cmd, true
		}
		if m.updateCommandMenuHover(msg) {
			return m, nil, true
		}
		if handled, cmd := m.updateTranscriptSelection(msg); handled {
			return m, cmd, true
		}
	case tea.MouseReleaseMsg:
		if m.shouldShowProfileModelSetup() {
			return m, nil, true
		}
		if handled, cmd := m.finishCommandViewSelection(msg); handled {
			return m, cmd, true
		}
		if handled, cmd := m.finishTranscriptSelection(msg); handled {
			return m, cmd, true
		}
	}

	return m, nil, false
}

func (m model) handleResponseEvent(msg responseEventMsg) (tea.Model, tea.Cmd) {
	return m.handleResponseEventBatch(responseEventBatchMsg{
		ResponseID: msg.ResponseID,
		Messages:   []tea.Msg{msg.Message},
	})
}

func (m model) handleResponseEventBatch(msg responseEventBatchMsg) (tea.Model, tea.Cmd) {
	if !m.isActiveResponse(msg.ResponseID) {
		return m, nil
	}

	m.beginTranscriptUpdate()
	commands := make([]tea.Cmd, 0, len(msg.Messages)+2)
	for _, event := range msg.Messages {
		if _, ok := event.(sessionErrorMsg); ok {
			continue
		}

		next, cmd := m.handleAppEvent(applyTUIMessageEvent{Message: event})
		m = next
		if cmd != nil {
			commands = append(commands, cmd)
		}
	}
	completionPending := msg.Closed && m.pendingResponseCompletion != nil
	if completionPending {
		m.transcriptRenderSuppressed = false
		m.transcriptRenderPending = false
		m.transcriptResizePending = false
	} else {
		var flushCmd tea.Cmd
		if isStreamingResponseBatch(msg.Messages) {
			flushCmd = m.finishStreamingTranscriptUpdate(msg.ResponseID)
		} else {
			m.finishTranscriptUpdate()
		}
		if flushCmd != nil {
			commands = append(commands, flushCmd)
		}
	}

	if msg.Closed {
		if cmd := m.handleResponseEventsClosed(responseEventsClosedMsg{ResponseID: msg.ResponseID}); cmd != nil {
			commands = append(commands, cmd)
		}
	} else if m.events != nil {
		commands = append(commands, waitForResponseEvent(msg.ResponseID, m.events))
	}

	return m, tea.Batch(commands...)
}

func isStreamingResponseBatch(messages []tea.Msg) bool {
	if len(messages) == 0 {
		return false
	}

	for _, message := range messages {
		if _, ok := message.(assistantTextDeltaMsg); !ok {
			return false
		}
	}

	return true
}

func (m model) handlePasteMsg(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	if m.manualCompactionActive || m.chatSwitching {
		return m, nil
	}

	previousValue := m.input.Value()
	msg.Content = normalizeComposerPaste(msg.Content)
	m.resizeInputForValue(m.input.Value() + msg.Content)
	m.input, _ = m.input.Update(msg)
	m.updateCommandMenuForInput(m.input.Value())
	m.resizeAfterInputValueChange(previousValue)

	return m, nil
}

func (m model) handleKeyPressMsg(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	switch msg.Keystroke() {
	case "ctrl+c":
		next, cmd := m.confirmExit()
		return next, cmd, true
	}
	if m.isCommandViewVisible() {
		if msg.Keystroke() == "esc" {
			if m.isPermissionApprovalCommandView() {
				return m, m.setStatus("select an approval option or press n to deny"), true
			}
			if m.isChatsCommandView() && (m.chatsArchiveConfirm || m.chatsRenaming) {
				next, cmd := m.updateCommandView(msg)
				return next, cmd, true
			}

			return m.hideCommandView(), nil, true
		}
		if msg.Keystroke() == "ctrl+y" {
			return m, m.copyCommandView(), true
		}

		next, cmd := m.updateCommandView(msg)
		return next, cmd, true
	}
	if m.manualCompactionActive {
		return m, nil, true
	}
	if m.chatSwitching {
		return m, nil, true
	}
	if next, cmd, handled := m.handleSessionQueueKeyPress(msg); handled {
		return next, cmd, true
	}

	switch msg.Keystroke() {
	case "ctrl+y":
		next, cmd := m.handleAppEvent(copyTranscriptEvent{})
		return next, cmd, true
	case "esc":
		cmd := m.cancelActiveResponse()
		return m, cmd, true
	case "ctrl+p":
		next, cmd := m.handleAppEvent(showPreviousPromptEvent{})
		return next, cmd, true
	case "ctrl+n":
		next, cmd := m.handleAppEvent(showNextPromptEvent{})
		return next, cmd, true
	case "shift+enter", "alt+enter", "ctrl+j":
		next, cmd := m.handleAppEvent(insertInputNewlineEvent{})
		return next, cmd, true
	case "ctrl+end":
		next, cmd := m.handleAppEvent(jumpTranscriptToBottomEvent{})
		return next, cmd, true
	case "enter":
		next, cmd := m.handleAppEvent(submitComposerEvent{})
		return next, cmd, true
	}
	if isInputLineDeleteKey(msg) {
		next, cmd := m.handleAppEvent(deleteInputLineEvent{})
		return next, cmd, true
	}
	if m.shouldUseHistoryKey(msg) {
		switch msg.Key().Code {
		case tea.KeyUp:
			if m.scrollCommandMenu(-1) {
				return m, nil, true
			}
			next, cmd := m.handleAppEvent(showPreviousPromptEvent{})
			return next, cmd, true
		case tea.KeyDown:
			if m.scrollCommandMenu(1) {
				return m, nil, true
			}
			next, cmd := m.handleAppEvent(showNextPromptEvent{})
			return next, cmd, true
		}
	}
	if m.scrollTranscriptWithKey(msg) {
		return m, m.loadOlderTimelinePageIfNeeded(), true
	}

	return m, nil, false
}

func (m model) resolvePermissionApproval(approved bool, scope permissions.GrantScope) tea.Cmd {
	requestID := m.pendingApprovalID
	client := m.permissionClient
	ctx := m.chatCtx
	return func() tea.Msg {
		if client == nil {
			return permissionResolutionCompletedMsg{RequestID: requestID, Err: errors.New("permission service is unavailable")}
		}

		ctx = rpcmeta.WithOutgoingPermissionSurface(ctx, permissions.SurfaceTUI)
		request, err := client.ResolveApprovalRequest(ctx, requestID, approved, scope)
		return permissionResolutionCompletedMsg{
			RequestID:  requestID,
			Status:     string(request.Status),
			Scope:      string(request.Scope),
			Summary:    request.Summary,
			Reason:     request.Reason,
			Effects:    effectsToStrings(request.Effects),
			Operations: append([]string(nil), request.Operations...),
			ExpiresAt:  request.ExpiresAt,
			Err:        err,
		}
	}
}

func effectsToStrings(effects []permissions.Effect) []string {
	values := make([]string, len(effects))
	for index, effect := range effects {
		values[index] = string(effect)
	}

	return values
}

func (m model) updateBubbleTeaChildren(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.chatSwitching {
		return m, nil
	}

	previousValue := m.input.Value()

	var (
		cmds []tea.Cmd
		cmd  tea.Cmd
	)

	m.input, cmd = m.input.Update(msg)
	m.updateCommandMenuForInput(m.input.Value())
	cmds = append(cmds, inputHandledCmdForMsg(msg, cmd))

	m.transcript, cmd = m.transcript.Update(msg)
	cmds = append(cmds, cmd)

	m.resizeAfterInputValueChange(previousValue)

	return m, tea.Batch(cmds...)
}

func (m model) updateInputComposer(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.chatSwitching {
		return m, nil
	}

	previousValue := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.updateCommandMenuForInput(m.input.Value())
	m.resizeAfterInputValueChange(previousValue)

	return m, inputHandledCmd(cmd)
}

func (m *model) resizeAfterInputValueChange(previousValue string) {
	previousMenuHeight := getCommandMenuHeightForValue(previousValue)
	nextMenuHeight := m.getCommandMenuHeight()
	position := m.getTranscriptWindowPosition()
	previousTranscriptHeight := m.transcript.Height()
	shouldKeepHeaderVisible := m.transcriptWindow.startBlock == 0 && m.transcriptWindow.startLine == 0 &&
		m.transcript.YOffset() == 0 && nextMenuHeight > previousMenuHeight

	m.resize()
	if m.transcript.Height() != previousTranscriptHeight {
		m.refreshTranscriptContentAtPosition(position)
	}
	if shouldKeepHeaderVisible {
		m.transcript.GotoTop()
	}
}

func inputHandledCmd(cmd tea.Cmd) tea.Cmd {
	if cmd != nil {
		return cmd
	}

	return func() tea.Msg {
		return nil
	}
}

func inputHandledCmdForMsg(msg tea.Msg, cmd tea.Cmd) tea.Cmd {
	switch msg.(type) {
	case tea.KeyPressMsg, tea.PasteMsg:
		return inputHandledCmd(cmd)
	default:
		return cmd
	}
}
