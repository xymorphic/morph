package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/muesli/reflow/wordwrap"

	"github.com/wandxy/morph/internal/permissions"
	"github.com/wandxy/morph/internal/trace"
	"github.com/wandxy/morph/pkg/str"
)

type transcriptRenderer interface {
	RenderCell(transcriptCell, transcriptRenderContext) string
	RenderCells([]transcriptCell, transcriptRenderContext) string
}

type lipglossTranscriptRenderer struct{}

const (
	assistantTranscriptIndicatorGlyph = "◉ "
	assistantTranscriptWorkGlyph      = "◷ "
)

var defaultTranscriptRenderer transcriptRenderer = lipglossTranscriptRenderer{}

func (lipglossTranscriptRenderer) RenderCell(cell transcriptCell, ctx transcriptRenderContext) string {
	if cell == nil || cell.IsEmpty() {
		return ""
	}

	switch value := cell.(type) {
	case userTranscriptCell:
		return renderUserTranscriptCell(value.text, ctx.Width)
	case assistantTranscriptCell:
		return renderAssistantTranscriptCell(value, ctx.Width)
	case thinkingTranscriptCell:
		return renderThinkingTranscriptCell(value, ctx.Width)
	case thoughtTranscriptCell:
		return renderThoughtTranscriptCell(formatToolTranscriptDuration(value.duration))
	case safetyTranscriptCell:
		return transcriptCellLabelStyle(transcriptCellSafety).Render("Safety:") + " " + value.safetyText()
	case errorTranscriptCell:
		return renderErrorTranscriptCell(value.message, ctx.Width)
	case systemTranscriptCell:
		return renderMarkdownForTranscript(value.text, ctx.Width)
	case permissionApprovalTranscriptCell:
		return renderPermissionApprovalTranscriptCell(value, ctx.Width, ctx.Now)
	case manualCompactionTranscriptCell:
		return renderManualCompactionCell(value, ctx)
	case toolTranscriptCell:
		group := toolTranscriptGroup{action: value.action}
		group.add(value)
		return renderToolTranscriptGroupWithContext(group, ctx)
	default:
		return ""
	}
}

func renderPermissionApprovalTranscriptCell(cell permissionApprovalTranscriptCell, width int, now time.Time) string {
	message := cell.message
	if message.Status != string(permissions.ApprovalPending) {
		return renderResolvedPermissionApproval(message, width)
	}

	prompt := permissions.GetApprovalPrompt(
		message.Summary,
		message.Effects,
		message.Reason,
		message.Operations,
		message.ExpiresAt,
	)
	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(defaultTUITheme.NoticeForeground)).
		Render(permissionStatusIcon + " Permission approval required")
	lines := []string{title}
	lines = append(lines, renderPermissionApprovalField("Operation", prompt.Summary, width)...)
	if len(prompt.Effects) > 0 {
		lines = append(lines, renderPermissionApprovalField("Effects", strings.Join(prompt.Effects, ", "), width)...)
	}
	if prompt.Reason != "" {
		lines = append(lines, renderPermissionApprovalField("Reason", prompt.Reason, width)...)
	}
	if len(prompt.Operations) > 0 {
		lines = append(lines, renderPermissionApprovalOperations(prompt.Operations, width)...)
	}
	if expiry := permissions.GetApprovalExpiryText(prompt.ExpiresAt, now); expiry != "" {
		lines = append(lines, renderPermissionApprovalField(
			"Expires",
			expiry,
			width,
		)...)
	}

	return strings.Join(lines, "\n")
}

func renderResolvedPermissionApproval(message permissionApprovalMsg, width int) string {
	status := "Permission " + message.Status
	if message.Scope != "" {
		status += " (" + message.Scope + ")"
	}
	statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(defaultTUITheme.MutedText))
	renderedStatus := statusStyle.Render(status)
	switch message.Status {
	case string(permissions.ApprovalApproved):
		icon := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(defaultTUITheme.ToolCompletedDot)).
			Render(permissionStatusIcon)
		renderedStatus = icon + " " + renderedStatus
	case string(permissions.ApprovalDenied):
		icon := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(defaultTUITheme.ToolDeletion)).
			Render(permissionStatusIcon)
		renderedStatus = icon + " " + renderedStatus
	}
	lines := []string{renderedStatus}
	lines = append(lines, renderPermissionApprovalField("Operation", message.Summary, width)...)

	return strings.Join(lines, "\n")
}

func renderPermissionApprovalField(label string, value string, width int) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	prefix := fmt.Sprintf("  %-9s  ", label)
	styledPrefix := lipgloss.NewStyle().
		Foreground(lipgloss.Color(defaultTUITheme.MutedText)).
		Render(prefix)
	wrapWidth := max(width-lipgloss.Width(prefix), 1)
	wrapped := strings.Split(wordwrap.String(value, wrapWidth), "\n")
	lines := make([]string, len(wrapped))
	for index, line := range wrapped {
		if index == 0 {
			lines[index] = styledPrefix + line
			continue
		}
		lines[index] = strings.Repeat(" ", lipgloss.Width(prefix)) + line
	}

	return lines
}

func renderPermissionApprovalOperations(operations []string, width int) []string {
	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(defaultTUITheme.MutedText)).
		Render("  Operations")
	lines := []string{title}
	for index, operation := range operations {
		prefix := fmt.Sprintf("    %d. ", index+1)
		wrapWidth := max(width-lipgloss.Width(prefix), 1)
		wrapped := strings.Split(wordwrap.String(operation, wrapWidth), "\n")
		for lineIndex, line := range wrapped {
			if lineIndex == 0 {
				lines = append(lines, prefix+line)
				continue
			}
			lines = append(lines, strings.Repeat(" ", lipgloss.Width(prefix))+line)
		}
	}

	return lines
}

func (renderer lipglossTranscriptRenderer) RenderCells(cells []transcriptCell, ctx transcriptRenderContext) string {
	blocks := getTranscriptRenderBlocks(cells)
	rendered := make([]string, 0, len(blocks))
	for _, block := range blocks {
		lines := block.renderLines(renderer, ctx)
		if len(lines) > 0 {
			rendered = append(rendered, strings.Join(lines, "\n"))
		}
	}

	return strings.Join(rendered, "\n\n")
}

type transcriptRenderBlock struct {
	cell        transcriptCell
	toolGroup   toolTranscriptGroup
	isToolGroup bool
}

func getTranscriptRenderBlocks(cells []transcriptCell) []transcriptRenderBlock {
	cells = compactMatchedToolTranscriptCells(cells)
	cells = compactConsecutiveProcessToolAttemptCells(cells)
	cells = compactConsecutiveManualCompactionCells(cells)
	blocks := make([]transcriptRenderBlock, 0, len(cells))
	var toolGroup *toolTranscriptGroup
	for _, cell := range cells {
		if cell == nil || cell.IsEmpty() {
			continue
		}

		if toolCell, ok := cell.(toolTranscriptCell); ok {
			if toolGroup == nil || toolGroup.action != toolCell.action {
				appendToolTranscriptRenderBlock(&blocks, &toolGroup)
			}
			if toolGroup == nil {
				toolGroup = &toolTranscriptGroup{action: toolCell.action}
			}
			toolGroup.add(toolCell)
			continue
		}

		appendToolTranscriptRenderBlock(&blocks, &toolGroup)
		blocks = append(blocks, transcriptRenderBlock{cell: cell})
	}
	appendToolTranscriptRenderBlock(&blocks, &toolGroup)

	return blocks
}

func appendToolTranscriptRenderBlock(blocks *[]transcriptRenderBlock, group **toolTranscriptGroup) {
	if *group == nil {
		return
	}

	*blocks = append(*blocks, transcriptRenderBlock{toolGroup: **group, isToolGroup: true})
	*group = nil
}

func (block transcriptRenderBlock) renderLines(
	renderer lipglossTranscriptRenderer,
	ctx transcriptRenderContext,
) []string {
	if block.isToolGroup {
		return renderCachedToolTranscriptGroupLines(block.toolGroup, ctx)
	}

	return renderer.renderCellLines(block.cell, ctx)
}

func (renderer lipglossTranscriptRenderer) renderCellLines(cell transcriptCell, ctx transcriptRenderContext) []string {
	if ctx.Cache == nil || !isTranscriptCellIdentityCacheable(cell) || isTranscriptCellFrameAnimated(cell) {
		return getPaddedTranscriptLines(renderer.RenderCell(cell, ctx), ctx.Padding)
	}

	key := getTranscriptCellRenderCacheKey(cell, ctx)
	if rendered, ok := ctx.Cache.get(key); ok {
		return rendered
	}

	rendered := getPaddedTranscriptLines(renderer.RenderCell(cell, ctx), ctx.Padding)
	ctx.Cache.set(key, rendered)
	return rendered
}

func getPaddedTranscriptLines(rendered string, padding int) []string {
	if rendered == "" {
		return nil
	}

	lines := strings.Split(rendered, "\n")
	if padding <= 0 {
		return lines
	}

	prefix := strings.Repeat(" ", padding)
	for index := range lines {
		if lines[index] != "" {
			lines[index] = prefix + lines[index]
		}
	}

	return lines
}

func compactMatchedToolTranscriptCells(cells []transcriptCell) []transcriptCell {
	if len(cells) <= 1 {
		return cells
	}

	compacted := make([]transcriptCell, 0, len(cells))
	toolIndexes := map[string]int{}
	for _, cell := range cells {
		if _, ok := cell.(userTranscriptCell); ok {
			toolIndexes = map[string]int{}
		}

		toolCell, ok := cell.(toolTranscriptCell)
		if !ok {
			compacted = append(compacted, cell)
			continue
		}
		idValue := str.String(toolCell.id)
		id := idValue.Trim()
		if id == "" {
			compacted = append(compacted, cell)
			continue
		}

		if toolCell.completed || toolCell.terminalStatus != "" {
			if index, ok := toolIndexes[id]; ok {
				if existing, existingOK := compacted[index].(toolTranscriptCell); existingOK {
					compacted[index] = mergeToolTranscriptCells(existing, toolCell)
					continue
				}
			}
		}

		toolIndexes[id] = len(compacted)
		compacted = append(compacted, cell)
	}

	return compacted
}

func compactConsecutiveManualCompactionCells(cells []transcriptCell) []transcriptCell {
	if len(cells) <= 1 {
		return cells
	}

	compacted := make([]transcriptCell, 0, len(cells))
	for _, cell := range cells {
		if _, ok := cell.(manualCompactionTranscriptCell); ok {
			if len(compacted) > 0 {
				if _, previousOK := compacted[len(compacted)-1].(manualCompactionTranscriptCell); previousOK {
					compacted[len(compacted)-1] = cell
					continue
				}
			}
		}
		compacted = append(compacted, cell)
	}

	return compacted
}

func compactConsecutiveProcessToolAttemptCells(cells []transcriptCell) []transcriptCell {
	if len(cells) <= 1 {
		return cells
	}

	compacted := make([]transcriptCell, 0, len(cells))
	for index := 0; index < len(cells); {
		cell := cells[index]
		compacted = append(compacted, cell)

		toolCell, ok := cell.(toolTranscriptCell)
		if !ok || !isProcessToolTranscriptCell(toolCell) {
			index++
			continue
		}

		nextIndex := index + 1
		pending := make([]transcriptCell, 0, 1)
		for nextIndex < len(cells) {
			next := cells[nextIndex]
			if next == nil || next.IsEmpty() {
				nextIndex++
				continue
			}
			if _, ok := next.(thoughtTranscriptCell); ok {
				pending = append(pending, next)
				nextIndex++
				continue
			}

			nextToolCell, ok := next.(toolTranscriptCell)
			if !ok || !isEquivalentProcessToolAttempt(toolCell, nextToolCell) {
				break
			}

			compacted = append(compacted, nextToolCell)
			pending = pending[:0]
			nextIndex++
		}

		if nextIndex > index+1 {
			compacted = append(compacted, pending...)
			index = nextIndex
			continue
		}
		index++
	}

	return compacted
}

func isProcessToolTranscriptCell(cell toolTranscriptCell) bool {
	actionValue := str.String(cell.action)
	return actionValue.Trim() == "Process" && cell.processState != nil
}

func isEquivalentProcessToolAttempt(current toolTranscriptCell, next toolTranscriptCell) bool {
	if !isProcessToolTranscriptCell(current) || !isProcessToolTranscriptCell(next) {
		return false
	}
	if current.id != "" && current.id == next.id {
		return true
	}

	currentKey := getProcessToolCellGroupKey(current)
	nextKey := getProcessToolCellGroupKey(next)
	return currentKey.operation != "" &&
		currentKey.operation == nextKey.operation &&
		currentKey.target != "" &&
		currentKey.target == nextKey.target
}

func getProcessToolCellGroupKey(cell toolTranscriptCell) processToolDetailGroupKey {
	if cell.processState == nil {
		return processToolDetailGroupKey{}
	}

	processIDValue := str.String(cell.processState.ProcessID)
	target := processIDValue.Trim()
	if cell.processState.Operation == trace.ProcessToolOperationStart || target == "" {
		commandValue := str.String(cell.processState.Command)
		target = commandValue.Trim()
	}

	return processToolDetailGroupKey{operation: cell.processState.Operation, target: target}
}

func renderUserTranscriptCell(body string, width int) string {
	contentWidth := max(width, 1)
	wrapWidth := max(contentWidth-lipgloss.Width(userTranscriptPrompt), 1)
	bodyValue := str.String(body)
	lines := strings.Split(bodyValue.Trim(), "\n")
	rendered := make([]string, 0, len(lines))
	for _, line := range lines {
		lineValue := str.String(line)
		for _, wrapped := range strings.Split(wordwrap.String(lineValue.Trim(), wrapWidth), "\n") {
			wrappedValue := str.String(wrapped)
			if wrappedValue.Trim() != "" {
				rendered = append(rendered, renderUserTranscriptLine(wrapped, contentWidth, len(rendered) == 0))
			}
		}
	}
	if len(rendered) == 0 {
		return ""
	}

	rendered = append([]string{renderUserTranscriptVerticalPadding(contentWidth, "▄")}, rendered...)
	rendered = append(rendered, renderUserTranscriptVerticalPadding(contentWidth, "▀"))

	return strings.Join(rendered, "\n")
}

func renderUserTranscriptLine(text string, width int, showPrompt bool) string {
	prefix := renderUserTranscriptContinuationPrefix()
	if showPrompt {
		prefix = renderUserTranscriptPrompt()
	}
	message := renderUserTranscriptText(text)
	usedWidth := lipgloss.Width(userTranscriptPrompt) + lipgloss.Width(text)
	filler := renderUserTranscriptFiller(max(width-usedWidth, 0))

	return prefix + message + filler
}

func renderUserTranscriptPrompt() string {
	return lipgloss.NewStyle().
		Background(lipgloss.Color(defaultTUITheme.UserTranscriptBackground)).
		Foreground(lipgloss.Color(defaultTUITheme.UserTranscriptPrompt)).
		Render(userTranscriptPrompt)
}

func renderUserTranscriptContinuationPrefix() string {
	return renderUserTranscriptFiller(lipgloss.Width(userTranscriptPrompt))
}

func renderUserTranscriptText(text string) string {
	return lipgloss.NewStyle().
		Background(lipgloss.Color(defaultTUITheme.UserTranscriptBackground)).
		Foreground(lipgloss.Color(defaultTUITheme.UserTranscriptText)).
		Render(text)
}

func renderUserTranscriptFiller(width int) string {
	return lipgloss.NewStyle().
		Background(lipgloss.Color(defaultTUITheme.UserTranscriptBackground)).
		Render(strings.Repeat(" ", max(width, 0)))
}

func renderUserTranscriptVerticalPadding(width int, glyph string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(defaultTUITheme.UserTranscriptBackground)).
		Render(strings.Repeat(glyph, max(width, 0)))
}

func renderAssistantTranscriptCell(cell assistantTranscriptCell, width int) string {
	renderMarkdownForTranscriptValue := str.String(renderMarkdownForTranscript(cell.text, width))
	rendered := renderMarkdownForTranscriptValue.Trim()
	if rendered == "" {
		return ""
	}

	lines := strings.Split(rendered, "\n")
	for index, line := range lines {
		if index == 0 {
			lines[index] = renderAssistantTranscriptIndicator() + line
			continue
		}

		lines[index] = renderAssistantTranscriptContinuationPrefix() + line
	}

	if cell.duration > 0 {
		lines = append(lines, "", renderAssistantTranscriptWorkLabel(cell.duration))
	}

	return strings.Join(lines, "\n")
}

func renderAssistantTranscriptIndicator() string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(defaultTUITheme.NoticeForeground)).
		Render(assistantTranscriptIndicatorGlyph)
}

func renderAssistantTranscriptContinuationPrefix() string {
	return strings.Repeat(" ", lipgloss.Width(assistantTranscriptIndicatorGlyph))
}

func renderAssistantTranscriptWorkLabel(duration time.Duration) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(defaultTUITheme.MutedText)).
		Render(assistantTranscriptWorkGlyph + "Worked for " + formatToolTranscriptDuration(duration))
}

func renderThinkingTranscriptCell(cell thinkingTranscriptCell, width int) string {
	contentWidth := max(width, 1)
	wrapWidth := max(contentWidth-4, 1)
	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(defaultTUITheme.ToolTitle))
	branchStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(defaultTUITheme.ToolBranch))
	textStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(defaultTUITheme.ToolDetail))
	bodyValue2 := str.String(cell.summary)
	lines := strings.Split(bodyValue2.Trim(), "\n")
	summaryLines := make([]string, 0, len(lines))
	for _, line := range lines {
		lineValue2 := str.String(line)
		line = lineValue2.Trim()
		if line != "" {
			summaryLines = append(summaryLines, line)
		}
	}
	if len(summaryLines) == 0 {
		return ""
	}

	title := titleStyle.Render("Thinking")
	if cell.completed {
		duration := formatToolTranscriptDuration(normalizeThoughtDuration(cell.duration))
		label := "View thinking"
		if cell.expanded {
			label = "Hide thinking"
		}
		title = renderThoughtTranscriptCell(duration) + "  " +
			renderThinkingTranscriptToggleLink(cell.id, label)
		if !cell.expanded {
			return title
		}
	}

	rendered := []string{title}
	first := true
	for _, line := range summaryLines {
		for _, wrapped := range strings.Split(wordwrap.String(line, wrapWidth), "\n") {
			wrappedValue2 := str.String(wrapped)
			wrapped = wrappedValue2.Trim()
			if wrapped == "" {
				continue
			}

			branch := "  "
			if first {
				branch = "└ "
				first = false
			}
			rendered = append(rendered, "  "+branchStyle.Render(branch)+textStyle.Render(wrapped))
		}
	}

	return strings.Join(rendered, "\n")
}

func renderThoughtTranscriptCell(body string) string {
	bodyValue3 := str.String(body)
	duration := bodyValue3.Trim()
	if duration == "" {
		return ""
	}

	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(defaultTUITheme.ToolBranch)).
		Render("Thought for " + duration)
}

func renderThinkingTranscriptToggleLink(id string, label string) string {
	labelValue := str.String(label)
	label = labelValue.Trim()
	if label == "" {
		return ""
	}

	rendered := lipgloss.NewStyle().
		Underline(true).
		Foreground(lipgloss.Color(defaultTUITheme.NoticeForeground)).
		Render(label)
	target := getThinkingTranscriptLink(id)
	if target == "" {
		return rendered
	}

	return "\x1b]8;;" + target + "\a" + rendered + "\x1b]8;;\a"
}

func renderErrorTranscriptCell(message string, width int) string {
	messageValue := str.String(message)
	message = messageValue.Trim()
	if message == "" {
		return ""
	}

	contentWidth := max(width, 1)
	bodyWidth := max(contentWidth-2, 1)
	background := lipgloss.Color(defaultTUITheme.InputFrameBackground)
	titleStyle := transcriptCellLabelStyle(transcriptCellError).Background(background)
	descriptionStyle := lipgloss.NewStyle().
		Background(background).
		MaxWidth(bodyWidth).
		Foreground(lipgloss.Color(defaultTUITheme.MutedText))
	bodyStyle := lipgloss.NewStyle().
		Background(background).
		MaxWidth(bodyWidth).
		Foreground(lipgloss.Color(defaultTUITheme.ToolDetail))
	commandStyle := lipgloss.NewStyle().
		Background(background).
		MaxWidth(bodyWidth).
		Foreground(lipgloss.Color("15"))
	title := titleStyle.Render("Error")
	content := []string{title}
	if command, description, instruction, ok := getErrorTranscriptCommandInstruction(message); ok {
		content[0] = title + descriptionStyle.Render(" - "+description)
		commandValue2 := str.String(wordwrap.String(command, bodyWidth))
		instructionValue := str.String(wordwrap.String(instruction, bodyWidth))
		content = append(
			content,
			"",
			commandStyle.Render(commandValue2.Trim()),
			"",
			bodyStyle.Render(instructionValue.Trim()),
		)
	} else {
		messageValue3 := str.String(wordwrap.String(message, bodyWidth))
		content = append(content, "", bodyStyle.Render(messageValue3.Trim()))
	}

	return lipgloss.NewStyle().
		Width(contentWidth).
		Background(background).
		Padding(1, 1).
		Render(strings.Join(content, "\n"))
}

func getErrorTranscriptCommandInstruction(message string) (string, string, string, bool) {
	messageValue2 := str.String(message)
	message = messageValue2.Trim()
	const prefix = "run "
	const suffix = " in a new terminal"
	if !strings.HasPrefix(message, prefix) || !strings.HasSuffix(message, suffix) {
		return "", "", "", false
	}
	trimSuffixValue := str.String(strings.TrimSuffix(strings.TrimPrefix(message, prefix), suffix))
	command := trimSuffixValue.Trim()
	if command == "" {
		return "", "", "", false
	}

	return command, "Model authentication is required.", "Run this command in a new terminal.", true
}

func transcriptCellLabelStyle(kind transcriptCellKind) lipgloss.Style {
	style := lipgloss.NewStyle().Bold(true)
	switch kind {
	case transcriptCellUser:
		return style.Foreground(lipgloss.Color("39"))
	case transcriptCellAssistant:
		return style.Foreground(lipgloss.Color("83"))
	case transcriptCellThinking:
		return style.Foreground(lipgloss.Color("246"))
	case transcriptCellThought:
		return style.Foreground(lipgloss.Color("244"))
	case transcriptCellTool:
		return style.Foreground(lipgloss.Color("214"))
	case transcriptCellSafety:
		return style.Foreground(lipgloss.Color("203"))
	case transcriptCellError:
		return style.Foreground(lipgloss.Color("196"))
	default:
		return style.Foreground(lipgloss.Color("244"))
	}
}
