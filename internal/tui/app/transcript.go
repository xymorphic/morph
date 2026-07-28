package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/wandxy/morph/internal/permissions"
	"github.com/wandxy/morph/pkg/str"
)

// newTranscript creates the scrollable conversation viewport.
func newTranscript() viewport.Model {
	transcript := viewport.New()
	transcript.SoftWrap = true

	return transcript
}

// renderTranscript draws the conversation viewport.
func (m model) renderTranscript() string {
	if m.shouldShowNamePrompt() {
		return m.renderNamePrompt()
	}
	if m.shouldShowProfileModelSetup() {
		return m.renderProfileModelSetup()
	}

	return lipgloss.NewStyle().
		Width(m.getMainPaneWidth()).
		Height(max(m.transcript.Height(), 1)).
		Render(m.transcript.View())
}

func (m *model) setTranscriptContent() {
	if m.isTranscriptSelectionDragActive() {
		return
	}

	m.clearTranscriptSelection()
	m.renderTranscriptWindowIntoViewport(transcriptWindowTail)
	m.transcript.GotoBottom()
}

func (m *model) setTranscriptContentForActiveTurn() {
	if m.isTranscriptSelectionDragActive() {
		return
	}

	startBlock := m.transcriptWindow.startBlock
	startLine := m.transcriptWindow.startLine
	m.clearTranscriptSelection()
	m.transcriptWindow.startBlock = startBlock
	m.transcriptWindow.startLine = startLine
	m.renderTranscriptIntoViewport()
}

func (m *model) renderTranscriptIntoViewport() {
	offset := m.transcript.YOffset()
	prepended := m.renderTranscriptWindowIntoViewport(transcriptWindowCurrent)
	m.transcript.SetYOffset(offset + prepended)
}

func (m *model) refreshTranscriptContentAfterResize() {
	m.refreshTranscriptContentAtPosition(m.getTranscriptWindowPosition())
}

func (m *model) refreshTranscriptContentAtPosition(position transcriptWindowPosition) {
	if m.isTranscriptSelectionDragActive() {
		return
	}

	m.clearTranscriptSelection()
	if position.atBottom {
		m.renderTranscriptWindowIntoViewport(transcriptWindowTail)
		m.transcript.GotoBottom()
		return
	}

	m.transcriptWindow.startBlock = position.startBlock
	m.transcriptWindow.startLine = position.startLine
	prepended := m.renderTranscriptWindowIntoViewport(transcriptWindowCurrent)
	m.transcript.SetYOffset(position.offset + prepended)
}

func (m *model) renderTranscriptContent() string {
	cells := make([]transcriptCell, 0, len(m.messages)+1)
	cells = append(cells, m.messages...)
	if m.live != nil && !m.live.IsEmpty() {
		cells = append(cells, m.live)
	}
	if len(cells) == 0 && m.shouldShowNamePrompt() {
		return ""
	}
	if len(cells) == 0 && m.shouldShowEmptyUserPrompt() {
		return m.renderEmptyUserPromptContent()
	}
	if len(cells) == 0 && m.showIntro {
		cells = append(cells, systemTranscriptCell{text: "Welcome to Morph TUI.\n\nThe interactive shell is ready."})
	}
	if len(cells) > 0 {
		m.showIntro = false
	}

	transcriptWidth := m.transcript.Width()
	if transcriptWidth <= 0 {
		transcriptWidth = m.getMainPaneWidth()
	}
	content := strings.Trim(m.renderHeaderWithWidth(transcriptWidth), "\n")
	cellsText := strings.Trim(m.renderTranscriptBodyCells(cells), "\n")
	cellsTextValue := str.String(cellsText)
	if cellsTextValue.Trim() != "" {
		content = strings.Join([]string{content, cellsText}, "\n\n")
	}

	return content
}

func (m model) renderTranscriptBodyCells(cells []transcriptCell) string {
	width := max(m.transcript.Width(), m.getMainPaneWidth())
	contentWidth := getPanelContentWidth(width)
	if contentWidth <= 0 {
		contentWidth = max(width, 1)
	}
	padding := getPanelHorizontalPadding(width)
	cellsText := renderTranscriptCellsWithPadding(
		cells,
		contentWidth,
		padding,
		m.toolAnimationFrame,
		m.transcriptCache,
	)
	cellsTextValue := str.String(cellsText)
	if cellsTextValue.Trim() == "" {
		return ""
	}

	return cellsText
}

func (m *model) updateTranscript(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.transcript, cmd = m.transcript.Update(msg)

	return *m, cmd
}

func (m *model) scrollTranscriptWithKey(msg tea.KeyPressMsg) bool {
	offset := m.transcript.YOffset()
	switch msg.Key().Code {
	case tea.KeyPgUp:
		m.transcript.PageUp()
		if m.transcript.YOffset() <= max(m.transcript.Height(), 1) {
			m.shiftTranscriptWindowUp()
		}
	case tea.KeyPgDown:
		m.transcript.PageDown()
		if m.transcript.YOffset()+m.transcript.Height() >=
			m.transcript.TotalLineCount()-max(m.transcript.Height(), 1) {
			m.shiftTranscriptWindowDown()
		}
	case tea.KeyHome:
		if m.selection.active {
			m.expandTranscriptSelectionToEdge(-1)
		} else {
			m.renderTranscriptWindowForScroll(transcriptWindowHead)
		}
		m.transcript.GotoTop()
	case tea.KeyEnd:
		m.jumpTranscriptToBottom()
		return true
	default:
		return false
	}
	m.markResponseTranscriptScrolled(offset, true)

	return true
}

func (m *model) updateTranscriptWithScrollTracking(msg tea.Msg) (tea.Model, tea.Cmd) {
	offset := m.transcript.YOffset()
	wheel, isWheel := msg.(tea.MouseWheelMsg)
	_, cmd := m.updateTranscript(msg)
	if isWheel && wheel.Button == tea.MouseWheelUp && m.transcript.YOffset() <= max(m.transcript.Height(), 1) {
		m.shiftTranscriptWindowUp()
	}
	if isWheel && wheel.Button == tea.MouseWheelDown &&
		m.transcript.YOffset()+m.transcript.Height() >=
			m.transcript.TotalLineCount()-max(m.transcript.Height(), 1) {
		m.shiftTranscriptWindowDown()
	}
	m.markResponseTranscriptScrolled(offset, true)

	return *m, cmd
}

func (m *model) markResponseTranscriptScrolled(previousOffset int, scrollInput bool) {
	if !m.responding {
		return
	}
	if m.isTranscriptAtAbsoluteBottom() {
		m.responseTranscriptFollow = true
		m.responseTranscriptScrolled = false
		return
	}

	if scrollInput || m.transcript.YOffset() != previousOffset {
		m.stopFollowingResponseTranscript()
	}
}

func (m *model) stopFollowingResponseTranscript() {
	m.responseTranscriptScrolled = true
	m.responseTranscriptFollow = false
}

func (m *model) applyTUIMessage(msg any) tea.Cmd {
	switch value := msg.(type) {
	case assistantTextDeltaMsg:
		if isReasoningDeltaChannel(value.Channel) {
			m.appendReasoningDelta(value.Text)
		} else {
			m.collapseCurrentReasoningTranscript()
			m.appendAssistantDelta(value.Text)
		}
	case assistantResponseCompletedMsg:
		m.completeAssistantResponse(value.Text, 0)
	case reasoningCompletedMsg:
		m.completeReasoningTranscript(value.Duration)
	case sessionErrorMsg:
		m.collapseCurrentReasoningTranscript()
		m.addTranscriptMessage(value)
		return m.setStatus("response failed")
	case toolInvocationStartedMsg:
		m.collapseCurrentReasoningTranscript()
		m.responseRunningToolCount++
		m.addTranscriptMessage(value)
		return m.startToolAnimation()
	case toolInvocationCompletedMsg:
		m.addTranscriptMessage(value)
		if m.responseRunningToolCount > 0 {
			m.responseRunningToolCount--
		}
		if m.responseRunningToolCount == 0 {
			m.toolAnimationActive = false
		}
		return m.startThinkingComposer()
	case safetyEventMsg:
		m.addTranscriptMessage(value)
	case manualCompactionMsg:
		m.addTranscriptMessage(value)
		if value.State.isInProgress() {
			return m.startToolAnimation()
		}
		if value.State.Status == "succeeded" {
			return loadSessionContextCmd(m.chatCtx, m.contextLoader, m.getCurrentSessionID())
		}
	case permissionApprovalMsg:
		m.updatePermissionApproval(value)
	}

	return nil
}

func (m *model) updatePermissionApproval(message permissionApprovalMsg) {
	cell := tuiMessageToTranscriptCell(message)
	if cell == nil {
		return
	}
	if m.approvalMessageIndices == nil {
		m.approvalMessageIndices = make(map[string]int)
	}
	if m.pendingApprovalMessages == nil {
		m.pendingApprovalMessages = make(map[string]permissionApprovalMsg)
	}
	if index, ok := m.approvalMessageIndices[message.RequestID]; ok && index >= 0 && index < len(m.messages) {
		m.applyAction(replaceTranscriptCellAction{Index: index, Cell: cell})
	} else {
		m.approvalMessageIndices[message.RequestID] = len(m.messages)
		m.applyAction(appendTranscriptCellAction{Cell: cell})
	}
	if message.Status == string(permissions.ApprovalPending) {
		if _, exists := m.pendingApprovalMessages[message.RequestID]; !exists {
			m.pendingApprovalOrder = append(m.pendingApprovalOrder, message.RequestID)
		}
		m.pendingApprovalMessages[message.RequestID] = message
	} else {
		delete(m.pendingApprovalMessages, message.RequestID)
		m.pendingApprovalOrder = removeApprovalRequestID(m.pendingApprovalOrder, message.RequestID)
	}
	m.setCurrentPendingApproval()
	m.refreshTranscriptContentAfterMessageUpdate()
	m.resize()
}

func (m *model) setCurrentPendingApproval() {
	previousID := m.pendingApprovalID
	m.pendingApprovalID = ""
	for len(m.pendingApprovalOrder) > 0 {
		id := m.pendingApprovalOrder[0]
		message, ok := m.pendingApprovalMessages[id]
		if ok {
			m.pendingApprovalID = id
			if previousID != id || !m.isPermissionApprovalCommandView() {
				m.showPermissionApprovalCommandView(message)
			}
			return
		}
		m.pendingApprovalOrder = m.pendingApprovalOrder[1:]
	}
	if m.isPermissionApprovalCommandView() {
		next := m.hideCommandView()
		*m = next
	}
}

func removeApprovalRequestID(values []string, id string) []string {
	for index, value := range values {
		if value == id {
			return append(values[:index], values[index+1:]...)
		}
	}

	return values
}

func permissionApprovalText(message permissionApprovalMsg) string {
	if message.Status != string(permissions.ApprovalPending) {
		text := "Permission " + message.Status
		if message.Scope != "" {
			text += " (" + message.Scope + ")"
		}
		return text + " — " + message.Summary
	}

	prompt := permissions.GetApprovalPrompt(
		message.Summary,
		message.Effects,
		message.Reason,
		message.Operations,
		message.ExpiresAt,
	)
	parts := []string{"Permission approval required", "Operation: " + prompt.Summary}
	if len(prompt.Effects) > 0 {
		parts = append(parts, "Effects: "+strings.Join(prompt.Effects, ", "))
	}
	if prompt.Reason != "" {
		parts = append(parts, "Reason: "+prompt.Reason)
	}
	if len(prompt.Operations) > 0 {
		parts = append(parts, "Operations:")
		for index, operation := range prompt.Operations {
			parts = append(parts, fmt.Sprintf("%d. %s", index+1, operation))
		}
	}
	if expiry := permissions.GetApprovalExpiryText(prompt.ExpiresAt, currentTime()); expiry != "" {
		parts = append(parts, "Expires: "+expiry)
	}
	return strings.Join(parts, "\n")
}

func (m *model) addTranscriptMessage(msg any) {
	if cell := tuiMessageToTranscriptCell(msg); cell != nil && !cell.IsEmpty() {
		if toolCell, ok := cell.(toolTranscriptCell); ok &&
			(toolCell.completed || toolCell.terminalStatus != "") &&
			m.mergeTerminalToolTranscriptCell(toolCell) {
			m.refreshTranscriptContentAfterMessageUpdate()
			m.resize()
			return
		}

		m.applyAction(appendTranscriptCellAction{Cell: cell})
		if m.responding {
			m.setTranscriptContentForResponseUpdate()
		} else {
			m.setTranscriptContent()
		}
		m.resize()
	}
}

func (m *model) mergeTerminalToolTranscriptCell(completed toolTranscriptCell) bool {
	idValue := str.String(completed.id)
	id := idValue.Trim()
	if id == "" {
		return false
	}

	startIndex := 0
	if m.responding && m.responseStartMessageIndex > 0 && m.responseStartMessageIndex <= len(m.messages) {
		startIndex = m.responseStartMessageIndex
	}

	for index := len(m.messages) - 1; index >= startIndex; index-- {
		existing, ok := m.messages[index].(toolTranscriptCell)
		idValue2 := str.String(existing.id)
		if !ok || idValue2.Trim() != id {
			continue
		}

		m.applyAction(replaceTranscriptCellAction{
			Index: index,
			Cell:  mergeToolTranscriptCells(existing, completed),
		})
		return true
	}

	return false
}

func mergeToolTranscriptCells(existing toolTranscriptCell, completed toolTranscriptCell) toolTranscriptCell {
	merged := existing
	actionValue := str.String(merged.action)
	if actionValue.Trim() == "" {
		merged.action = completed.action
	}
	detailValue := str.String(merged.detail)
	if detailValue.Trim() == "" ||
		isToolTranscriptFallbackDetail(merged) && !isToolTranscriptFallbackDetail(completed) {
		merged.detail = completed.detail
	}
	merged.planState = mergePlanToolDisplayState(merged.planState, completed.planState)
	merged.processState = mergeProcessToolDisplayState(merged.processState, completed.processState)
	if merged.startedAt.IsZero() {
		merged.startedAt = completed.startedAt
	}
	if !completed.completedAt.IsZero() {
		merged.completedAt = completed.completedAt
	}
	idValue3 := str.String(merged.id)
	if idValue3.Trim() == "" {
		merged.id = completed.id
	}
	merged.completed = completed.completed
	merged.terminalStatus = completed.terminalStatus
	if completed.failure != "" {
		merged.failure = completed.failure
	}
	if completed.hasArtifact {
		merged.artifact = completed.artifact
		merged.hasArtifact = true
	}
	if completed.artifactStatus != "" {
		merged.artifactStatus = completed.artifactStatus
	}

	return merged
}

func isToolTranscriptFallbackDetail(cell toolTranscriptCell) bool {
	detail := str.String(cell.detail).Trim()
	action := str.String(cell.action).Trim()
	if detail == "" || action == "" {
		return false
	}

	return getToolActionName(detail) == action
}

func (m *model) failRunningToolTranscriptCells(failedAt time.Time, failure string) {
	m.setRunningToolTranscriptCellsTerminal(failedAt, toolTranscriptTerminalStatusFailed, failure)
}

func (m *model) interruptRunningToolTranscriptCells(interruptedAt time.Time) {
	m.setRunningToolTranscriptCellsTerminal(interruptedAt, toolTranscriptTerminalStatusInterrupted, "")
}

func (m *model) setRunningToolTranscriptCellsTerminal(
	terminalAt time.Time,
	status toolTranscriptTerminalStatus,
	failure string,
) {
	startIndex := m.responseStartMessageIndex
	if startIndex < 0 || startIndex > len(m.messages) {
		startIndex = 0
	}

	changed := false
	for index := startIndex; index < len(m.messages); index++ {
		cell, ok := m.messages[index].(toolTranscriptCell)
		if !ok || cell.completed || cell.terminalStatus != "" {
			continue
		}
		cell.terminalStatus = status
		cell.completedAt = terminalAt
		if cell.action == "Browser" {
			cell.failure = failure
		}
		m.applyAction(replaceTranscriptCellAction{Index: index, Cell: cell})
		changed = true
	}
	if changed {
		m.refreshTranscriptContentAfterMessageUpdate()
	}
}

func (m *model) refreshTranscriptContentAfterMessageUpdate() {
	if m.responding {
		m.setTranscriptContentForResponseUpdate()
		return
	}

	m.setTranscriptContent()
}

func (m *model) appendReasoningDelta(delta string) {
	cell := newReasoningTranscriptCell(delta, currentTime())
	if cell == nil || cell.IsEmpty() {
		return
	}
	if m.reasoningStartedAt.IsZero() {
		m.reasoningStartedAt = currentTime()
	}

	if len(m.messages) > 0 && m.messages[len(m.messages)-1].Kind() == transcriptCellReasoning {
		index := len(m.messages) - 1
		m.applyAction(replaceTranscriptCellAction{
			Index: index,
			Cell:  appendReasoningTranscriptCell(m.messages[index], delta),
		})
		m.trackReasoningTranscriptIndex(index)
	} else {
		m.applyAction(appendTranscriptCellAction{Cell: cell})
		m.trackReasoningTranscriptIndex(len(m.messages) - 1)
	}

	m.setTranscriptContentForResponseUpdate()
	m.resize()
}

func (m *model) appendAssistantDelta(delta string) {
	if delta == "" {
		return
	}

	m.stream.Add(delta)
	m.applyAction(setLiveTranscriptCellAction{Cell: assistantTranscriptCell{text: m.stream.Render()}})
	m.setTranscriptContentForResponseUpdate()
	m.resize()
}

func (m *model) completeAssistantResponse(text string, duration time.Duration) {
	reply := text
	replyValue := str.String(reply)
	if replyValue.Trim() == "" {
		reply = m.stream.Finalize()
	} else {
		m.stream.Reset()
	}
	replyValue2 := str.String(reply)
	if replyValue2.Trim() == "" {
		m.applyAction(setLiveTranscriptCellAction{})
		m.collapseCurrentReasoningTranscript()
		m.setTranscriptContentAfterResponseCompletion()
		m.resize()
		return
	}

	m.collapseCurrentReasoningTranscript()
	m.applyAction(appendTranscriptCellAction{Cell: assistantTranscriptCell{text: reply, duration: normalizeResponseDuration(duration)}})
	m.applyAction(setLiveTranscriptCellAction{})
	m.setTranscriptContentAfterResponseCompletion()
	m.resize()
}

func normalizeResponseDuration(duration time.Duration) time.Duration {
	if duration <= 0 {
		return 0
	}

	return duration.Round(time.Second)
}

func (m *model) setTranscriptContentAfterResponseCompletion() {
	m.setTranscriptContentForResponseUpdate()
}

func (m *model) setTranscriptContentForResponseUpdate() {
	if m.transcriptRenderSuppressed {
		m.transcriptRenderPending = true
		return
	}
	m.setTranscriptContentForResponseUpdateNow()
}

func (m *model) setTranscriptContentForResponseUpdateNow() {
	m.resizeTranscriptIfLayoutChanged()
	if m.responding && m.responseTranscriptFollow && !m.responseTranscriptScrolled {
		m.setTranscriptContent()
		return
	}

	m.setTranscriptContentForActiveTurn()
}

func (m *model) beginTranscriptUpdate() {
	m.transcriptRenderSuppressed = true
}

func (m *model) finishTranscriptUpdate() {
	m.transcriptRenderSuppressed = false
	m.streamingFlushDirty = false
	m.streamingFlushPending = false
	m.flushDeferredTranscriptUpdate()
}

func (m *model) finishStreamingTranscriptUpdate(responseID int) tea.Cmd {
	m.transcriptRenderSuppressed = false
	now := currentTime()
	if m.streamingRenderAt.IsZero() || !now.Before(m.streamingRenderAt.Add(streamingTranscriptRenderInterval)) {
		m.streamingFlushDirty = false
		m.streamingFlushPending = false
		m.flushDeferredTranscriptUpdate()
		return nil
	}

	m.streamingFlushDirty = true
	if m.streamingFlushPending {
		return nil
	}

	m.streamingFlushPending = true
	delay := m.streamingRenderAt.Add(streamingTranscriptRenderInterval).Sub(now)
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return streamingTranscriptFlushMsg{ResponseID: responseID}
	})
}

func (m model) flushStreamingTranscript(msg streamingTranscriptFlushMsg) (tea.Model, tea.Cmd) {
	if !m.isActiveResponse(msg.ResponseID) || !m.streamingFlushPending {
		return m, nil
	}

	m.streamingFlushPending = false
	m.streamingFlushDirty = false
	m.flushDeferredTranscriptUpdate()
	return m, nil
}

func (m *model) flushDeferredTranscriptUpdate() {
	renderPending := m.transcriptRenderPending
	resizePending := m.transcriptResizePending
	m.transcriptRenderPending = false
	m.transcriptResizePending = false

	if resizePending || renderPending {
		m.resize()
	}
	if renderPending {
		m.setTranscriptContentForResponseUpdateNow()
	}
}

func (m *model) completeReasoningTranscript(duration time.Duration) {
	if duration <= 0 {
		duration = time.Second
	}

	index, ok := m.getCurrentReasoningTranscriptIndex()
	if !ok {
		return
	}

	m.replaceReasoningTranscriptCellWithThought(index, duration)
}

func (m *model) clearReasoningTranscriptState() {
	m.reasoningStartedAt = time.Time{}
	m.reasoningMessageIndex = -1
	m.reasoningMessageIndices = nil
}

func (m *model) trackReasoningTranscriptIndex(index int) {
	m.reasoningMessageIndex = index
	if index < 0 {
		return
	}
	for _, existing := range m.reasoningMessageIndices {
		if existing == index {
			return
		}
	}

	m.reasoningMessageIndices = append(m.reasoningMessageIndices, index)
}

func (m model) hasActiveReasoningTranscriptCells() bool {
	return len(m.getActiveReasoningTranscriptIndices()) > 0
}

func (m model) getCurrentReasoningTranscriptIndex() (int, bool) {
	if m.reasoningMessageIndex >= 0 &&
		m.reasoningMessageIndex < len(m.messages) &&
		m.messages[m.reasoningMessageIndex] != nil &&
		m.messages[m.reasoningMessageIndex].Kind() == transcriptCellReasoning {
		return m.reasoningMessageIndex, true
	}

	for index := len(m.reasoningMessageIndices) - 1; index >= 0; index-- {
		messageIndex := m.reasoningMessageIndices[index]
		if messageIndex < 0 || messageIndex >= len(m.messages) || m.messages[messageIndex] == nil {
			continue
		}
		if m.messages[messageIndex].Kind() == transcriptCellReasoning {
			return messageIndex, true
		}
	}

	return -1, false
}

func (m model) getActiveReasoningTranscriptIndices() []int {
	seen := map[int]bool{}
	indices := make([]int, 0, len(m.reasoningMessageIndices)+1)
	for _, index := range append(append([]int{}, m.reasoningMessageIndices...), m.reasoningMessageIndex) {
		if seen[index] || index < 0 || index >= len(m.messages) {
			continue
		}
		if m.messages[index] == nil || m.messages[index].Kind() != transcriptCellReasoning {
			continue
		}

		seen[index] = true
		indices = append(indices, index)
	}

	return indices
}

func (m *model) collapseCurrentReasoningTranscript() {
	index, ok := m.getCurrentReasoningTranscriptIndex()
	if !ok {
		return
	}

	duration := m.getReasoningTranscriptDuration(index, currentTime())
	m.replaceReasoningTranscriptCellWithThought(index, duration)
}

func (m model) getReasoningTranscriptDuration(index int, endedAt time.Time) time.Duration {
	if index < 0 || index >= len(m.messages) || m.messages[index] == nil {
		return time.Second
	}

	cell, ok := m.messages[index].(reasoningTranscriptCell)
	if !ok || cell.startedAt.IsZero() {
		if m.reasoningStartedAt.IsZero() {
			return time.Second
		}

		return normalizeThoughtDuration(endedAt.Sub(m.reasoningStartedAt).Round(time.Second))
	}

	return normalizeThoughtDuration(endedAt.Sub(cell.startedAt).Round(time.Second))
}

func (m *model) replaceReasoningTranscriptCellWithThought(index int, duration time.Duration) {
	m.applyAction(replaceTranscriptCellAction{
		Index: index,
		Cell:  thoughtTranscriptCell{duration: normalizeThoughtDuration(duration)},
	})
	m.untrackReasoningTranscriptIndex(index)
	if !m.hasActiveReasoningTranscriptCells() {
		m.clearReasoningTranscriptState()
	}
	if m.responding {
		m.setTranscriptContentForResponseUpdate()
	} else {
		m.setTranscriptContent()
	}
	m.resize()
}

func (m *model) untrackReasoningTranscriptIndex(index int) {
	next := m.reasoningMessageIndices[:0]
	for _, existing := range m.reasoningMessageIndices {
		if existing != index {
			next = append(next, existing)
		}
	}
	m.reasoningMessageIndices = next
	if m.reasoningMessageIndex == index {
		m.reasoningMessageIndex = -1
	}
}

func normalizeThoughtDuration(duration time.Duration) time.Duration {
	if duration <= 0 {
		return time.Second
	}

	return duration
}

func newReasoningTranscriptCell(text string, startedAt time.Time) transcriptCell {
	textValue := str.String(text)
	if textValue.Trim() == "" {
		return nil
	}

	return reasoningTranscriptCell{text: text, startedAt: startedAt}
}

func appendReasoningTranscriptCell(cell transcriptCell, delta string) transcriptCell {
	deltaValue := str.String(delta)
	if deltaValue.Trim() == "" {
		return cell
	}

	reasoningCell, ok := cell.(reasoningTranscriptCell)
	if !ok {
		return newReasoningTranscriptCell(delta, currentTime())
	}

	reasoningCell.text += delta
	return reasoningCell
}

func isReasoningDeltaChannel(channel string) bool {
	channelValue := str.String(channel)
	return channelValue.Normalized() == "reasoning"
}
