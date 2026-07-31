package tui

import (
	"fmt"
	"strings"
	"time"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/wandxy/morph/internal/trace"
	"github.com/wandxy/morph/pkg/str"
	"github.com/wandxy/morph/pkg/terminalmd"
)

type transcriptCellKind string

const (
	transcriptCellUser       transcriptCellKind = "user"
	transcriptCellAssistant  transcriptCellKind = "assistant"
	transcriptCellThinking   transcriptCellKind = "thinking"
	transcriptCellThought    transcriptCellKind = "thought"
	transcriptCellTool       transcriptCellKind = "tool"
	transcriptCellSafety     transcriptCellKind = "safety"
	transcriptCellError      transcriptCellKind = "error"
	transcriptCellSystem     transcriptCellKind = "system"
	transcriptCellCompaction transcriptCellKind = "compaction"
)

const userTranscriptPrompt = inputPrompt

var thinkingSummaryMarkdownRenderer = terminalmd.NewRenderer(terminalmd.Options{Width: 1 << 20})

type transcriptRenderContext struct {
	Width   int
	Padding int
	Frame   int
	Now     time.Time
	Cache   *transcriptRenderCache
}

type transcriptCell interface {
	Kind() transcriptCellKind
	PlainText() string
	IsEmpty() bool
}

type userTranscriptCell struct {
	text string
}

type assistantTranscriptCell struct {
	text     string
	duration time.Duration
}

type thinkingTranscriptCell struct {
	id        string
	summary   string
	startedAt time.Time
	duration  time.Duration
	completed bool
	expanded  bool
}

type thoughtTranscriptCell struct {
	duration time.Duration
}

type safetyTranscriptCell struct {
	action     string
	findingIDs []string
}

type errorTranscriptCell struct {
	message string
}

type systemTranscriptCell struct {
	text string
}

type permissionApprovalTranscriptCell struct {
	message permissionApprovalMsg
}

type manualCompactionTranscriptCell struct {
	state manualCompactionState
}

func (cell userTranscriptCell) Kind() transcriptCellKind {
	return transcriptCellUser
}

func (cell userTranscriptCell) PlainText() string {
	if cell.IsEmpty() {
		return ""
	}

	textValue := str.String(cell.text)
	return "You: " + textValue.Trim()
}

func (cell userTranscriptCell) IsEmpty() bool {
	textValue2 := str.String(cell.text)
	return textValue2.Trim() == ""
}

func (cell assistantTranscriptCell) Kind() transcriptCellKind {
	return transcriptCellAssistant
}

func (cell assistantTranscriptCell) PlainText() string {
	if cell.IsEmpty() {
		return ""
	}

	text := "Morph: " + cell.text
	if cell.duration > 0 {
		text += "\nWorked for " + formatToolTranscriptDuration(cell.duration)
	}

	return text
}

func (cell assistantTranscriptCell) IsEmpty() bool {
	textValue3 := str.String(cell.text)
	return textValue3.Trim() == ""
}

func (cell thinkingTranscriptCell) Kind() transcriptCellKind {
	return transcriptCellThinking
}

func (cell thinkingTranscriptCell) PlainText() string {
	if cell.IsEmpty() {
		return ""
	}

	if cell.completed && !cell.expanded {
		return "Thought: " + formatToolTranscriptDuration(cell.duration)
	}

	text := "Thinking: " + getThinkingSummaryDisplayText(cell.summary)
	if cell.completed {
		text += "\nThought: " + formatToolTranscriptDuration(cell.duration)
	}
	return text
}

func (cell thinkingTranscriptCell) IsEmpty() bool {
	textValue4 := str.String(cell.summary)
	return textValue4.Trim() == ""
}

func getThinkingSummaryDisplayText(summary string) string {
	summary = strings.ReplaceAll(summary, "\r\n", "\n")
	summary = strings.ReplaceAll(summary, "\r", "\n")
	summary = strings.ReplaceAll(summary, `\*\*\*\*`, "**\n**")
	summary = strings.ReplaceAll(summary, "****", "**\n**")
	summary = strings.ReplaceAll(summary, "\n", "\n\n")
	rendered, err := thinkingSummaryMarkdownRenderer.Render(summary)
	if err == nil {
		summary = xansi.Strip(rendered)
	}

	lines := strings.Split(summary, "\n")
	normalized := lines[:0]
	for _, line := range lines {
		line = strings.NewReplacer(
			"**", "",
			"__", "",
			"~~", "",
		).Replace(line)
		line = strings.Trim(line, "*_~`")
		lineValue := str.String(line)
		if line = lineValue.Trim(); line != "" {
			normalized = append(normalized, line)
		}
	}

	return strings.Join(normalized, "\n")
}

func (cell thoughtTranscriptCell) Kind() transcriptCellKind {
	return transcriptCellThought
}

func (cell thoughtTranscriptCell) PlainText() string {
	if cell.IsEmpty() {
		return ""
	}

	return "Thought: " + formatToolTranscriptDuration(cell.duration)
}

func (cell thoughtTranscriptCell) IsEmpty() bool {
	return cell.duration <= 0
}

func (cell safetyTranscriptCell) Kind() transcriptCellKind {
	return transcriptCellSafety
}

func (cell safetyTranscriptCell) PlainText() string {
	if cell.IsEmpty() {
		return ""
	}

	return "Safety: " + cell.safetyText()
}

func (cell safetyTranscriptCell) IsEmpty() bool {
	safetyTextValue := str.String(cell.safetyText())
	return safetyTextValue.Trim() == ""
}

func (cell safetyTranscriptCell) safetyText() string {
	parts := []string{}
	actionValue := str.String(cell.action)
	if action := actionValue.Trim(); action != "" {
		parts = append(parts, action)
	}
	if len(cell.findingIDs) > 0 {
		parts = append(parts, strings.Join(cell.findingIDs, ", "))
	}

	return strings.Join(parts, ": ")
}

func (cell errorTranscriptCell) Kind() transcriptCellKind {
	return transcriptCellError
}

func (cell errorTranscriptCell) PlainText() string {
	if cell.IsEmpty() {
		return ""
	}

	messageValue := str.String(cell.message)
	return "Error: " + messageValue.Trim()
}

func (cell errorTranscriptCell) IsEmpty() bool {
	messageValue2 := str.String(cell.message)
	return messageValue2.Trim() == ""
}

func (cell systemTranscriptCell) Kind() transcriptCellKind {
	return transcriptCellSystem
}

func (cell systemTranscriptCell) PlainText() string {
	textValue5 := str.String(cell.text)
	return textValue5.Trim()
}

func (cell systemTranscriptCell) IsEmpty() bool {
	textValue6 := str.String(cell.text)
	return textValue6.Trim() == ""
}

func (cell permissionApprovalTranscriptCell) Kind() transcriptCellKind {
	return transcriptCellSystem
}

func (cell permissionApprovalTranscriptCell) PlainText() string {
	return permissionApprovalText(cell.message)
}

func (cell permissionApprovalTranscriptCell) IsEmpty() bool {
	return strings.TrimSpace(cell.PlainText()) == ""
}

func (cell manualCompactionTranscriptCell) Kind() transcriptCellKind {
	return transcriptCellCompaction
}

func (cell manualCompactionTranscriptCell) PlainText() string {
	if cell.IsEmpty() {
		return ""
	}

	return cell.state.displayText()
}

func (cell manualCompactionTranscriptCell) IsEmpty() bool {
	displayTextValue := str.String(cell.state.displayText())
	return displayTextValue.Trim() == ""
}

func renderTranscriptCells(cells []transcriptCell) string {
	return renderTranscriptCellsWithWidth(cells, defaultWidth)
}

func renderTranscriptCellsWithWidth(cells []transcriptCell, width int) string {
	return renderTranscriptCellsWithFrame(cells, width, 0)
}

func renderTranscriptCellsWithFrame(cells []transcriptCell, width int, frame int) string {
	ctx := transcriptRenderContext{Width: width, Frame: frame, Now: currentTime()}
	return defaultTranscriptRenderer.RenderCells(cells, ctx)
}

func renderTranscriptCellsWithFrameAndCache(
	cells []transcriptCell,
	width int,
	frame int,
	cache *transcriptRenderCache,
) string {
	ctx := transcriptRenderContext{Width: width, Frame: frame, Now: currentTime(), Cache: cache}
	return defaultTranscriptRenderer.RenderCells(cells, ctx)
}

func renderTranscriptCellsWithPadding(
	cells []transcriptCell,
	width int,
	padding int,
	frame int,
	cache *transcriptRenderCache,
) string {
	ctx := transcriptRenderContext{
		Width: width, Padding: padding, Frame: frame, Now: currentTime(), Cache: cache,
	}
	return defaultTranscriptRenderer.RenderCells(cells, ctx)
}

type toolTranscriptTerminalStatus string

const (
	toolTranscriptTerminalStatusFailed      toolTranscriptTerminalStatus = "failed"
	toolTranscriptTerminalStatusInterrupted toolTranscriptTerminalStatus = "interrupted"
)

type toolTranscriptGroupOutcome string

const (
	toolTranscriptGroupOutcomeRunning             toolTranscriptGroupOutcome = "running"
	toolTranscriptGroupOutcomeCompleted           toolTranscriptGroupOutcome = "completed"
	toolTranscriptGroupOutcomeCompletedWithIssues toolTranscriptGroupOutcome = "completed_with_issues"
	toolTranscriptGroupOutcomeFailed              toolTranscriptGroupOutcome = "failed"
	toolTranscriptGroupOutcomeInterrupted         toolTranscriptGroupOutcome = "interrupted"
)

type toolTranscriptCell struct {
	id             string
	action         string
	detail         string
	planState      *trace.PlanToolState
	processState   *trace.ProcessToolState
	startedAt      time.Time
	completedAt    time.Time
	completed      bool
	terminalStatus toolTranscriptTerminalStatus
	failure        string
	artifact       browserArtifact
	hasArtifact    bool
	artifactStatus string
}

type toolTranscriptDetail struct {
	id             string
	text           string
	planState      *trace.PlanToolState
	processState   *trace.ProcessToolState
	startedAt      time.Time
	completedAt    time.Time
	completed      bool
	terminalStatus toolTranscriptTerminalStatus
	failure        string
	artifact       browserArtifact
	hasArtifact    bool
	artifactStatus string
}

type toolTranscriptGroup struct {
	action           string
	details          []toolTranscriptDetail
	seenIDs          map[string]bool
	completedIDs     map[string]bool
	terminalStatuses map[string]toolTranscriptTerminalStatus
	completed        bool
	terminalStatus   toolTranscriptTerminalStatus
}

func (cell toolTranscriptCell) Kind() transcriptCellKind {
	return transcriptCellTool
}

func (cell toolTranscriptCell) PlainText() string {
	return toolTranscriptPlainText(cell)
}

func (cell toolTranscriptCell) IsEmpty() bool {
	actionValue2 := str.String(cell.action)
	detailValue := str.String(cell.detail)
	return actionValue2.Trim() == "" && detailValue.
		Trim() == "" &&
		cell.planState == nil &&
		cell.processState == nil
}

func newToolTranscriptCell(
	id string,
	name string,
	detail string,
	planState *trace.PlanToolState,
	processState *trace.ProcessToolState,
	startedAt time.Time,
	completedAt time.Time,
	completed bool,
) transcriptCell {
	nameValue := str.String(name)
	name = nameValue.Trim()
	if name == "" {
		return nil
	}

	detail = normalizeToolTranscriptDetail(detail)
	if detail == "" && planState == nil && processState == nil {
		detail = name
	}
	idValue := str.String(id)
	return toolTranscriptCell{
		id:           idValue.Trim(),
		action:       getToolActionName(name),
		detail:       detail,
		planState:    planState,
		processState: processState,
		startedAt:    startedAt,
		completedAt:  completedAt,
		completed:    completed,
	}
}

func toolTranscriptPlainText(cell toolTranscriptCell) string {
	actionValue3 := str.String(cell.action)
	action := actionValue3.Trim()
	if action == "" {
		return ""
	}
	lines := []string{}
	idValue2 := str.String(cell.id)
	if id := idValue2.Trim(); id != "" {
		lines = append(lines, "id: "+id)
	}
	detailValue2 := str.String(cell.detail)
	lines = append(lines, "detail: "+detailValue2.Trim())
	if !cell.startedAt.IsZero() {
		lines = append(lines, "started_at: "+cell.startedAt.UTC().Format(time.RFC3339Nano))
	}
	if !cell.completedAt.IsZero() {
		lines = append(lines, "completed_at: "+cell.completedAt.UTC().Format(time.RFC3339Nano))
	}
	if cell.completed {
		lines = append(lines, "status: completed")
	} else if cell.terminalStatus != "" {
		lines = append(lines, "status: "+string(cell.terminalStatus))
	}
	if cell.failure != "" {
		lines = append(lines, "failure: "+cell.failure)
	}

	return fmt.Sprintf("Tool %s:\n%s", action, strings.Join(lines, "\n"))
}

func (group *toolTranscriptGroup) add(cell toolTranscriptCell) {
	if group == nil {
		return
	}

	idValue3 := str.String(cell.id)
	if id := idValue3.Trim(); id != "" {
		if group.seenIDs == nil {
			group.seenIDs = map[string]bool{}
		}
		if cell.completed {
			if group.completedIDs == nil {
				group.completedIDs = map[string]bool{}
			}
			group.completedIDs[id] = true
		}
		if cell.terminalStatus != "" {
			if group.terminalStatuses == nil {
				group.terminalStatuses = map[string]toolTranscriptTerminalStatus{}
			}
			group.terminalStatuses[id] = cell.terminalStatus
		}
		if group.seenIDs[id] {
			group.mergeToolTranscriptCell(id, cell)
			return
		}
		group.seenIDs[id] = true
	} else {
		group.completed = group.completed || cell.completed
		if cell.terminalStatus != "" {
			group.terminalStatus = cell.terminalStatus
		}
	}
	detailValue3 := str.String(cell.detail)
	detail := detailValue3.Trim()
	if detail == "" {
		actionValue4 := str.String(cell.action)
		detail = actionValue4.Trim()
	}
	if detail != "" || cell.planState != nil || cell.processState != nil {
		idValue4 := str.String(cell.id)
		group.details = append(group.details, toolTranscriptDetail{
			id:             idValue4.Trim(),
			text:           detail,
			planState:      clonePlanToolDisplayState(cell.planState),
			processState:   cloneProcessToolDisplayState(cell.processState),
			startedAt:      cell.startedAt,
			completedAt:    cell.completedAt,
			completed:      cell.completed,
			terminalStatus: cell.terminalStatus,
			failure:        cell.failure,
			artifact:       cell.artifact,
			hasArtifact:    cell.hasArtifact,
			artifactStatus: cell.artifactStatus,
		})
	}
}

func (group *toolTranscriptGroup) mergeToolTranscriptCell(id string, cell toolTranscriptCell) {
	for index := range group.details {
		if group.details[index].id != id {
			continue
		}

		if group.details[index].startedAt.IsZero() {
			group.details[index].startedAt = cell.startedAt
		}
		if !cell.completedAt.IsZero() {
			group.details[index].completedAt = cell.completedAt
		}
		if cell.completed {
			group.details[index].completed = true
		}
		if cell.terminalStatus != "" {
			group.details[index].terminalStatus = cell.terminalStatus
		}
		if cell.failure != "" {
			group.details[index].failure = cell.failure
		}
		if merged := mergePlanToolDisplayState(group.details[index].planState, cell.planState); merged != nil {
			group.details[index].planState = merged
		}
		if merged := mergeProcessToolDisplayState(group.details[index].processState, cell.processState); merged != nil {
			group.details[index].processState = merged
		}
		if group.details[index].text == "" {
			group.details[index].text = cell.detail
		}
		if cell.hasArtifact {
			group.details[index].artifact = cell.artifact
			group.details[index].hasArtifact = true
		}
		if cell.artifactStatus != "" {
			group.details[index].artifactStatus = cell.artifactStatus
		}
		return
	}
}

func renderCachedToolTranscriptGroupLines(group toolTranscriptGroup, ctx transcriptRenderContext) []string {
	if ctx.Cache == nil || isToolTranscriptGroupFrameAnimated(group) {
		return getPaddedTranscriptLines(renderToolTranscriptGroupWithContext(group, ctx), ctx.Padding)
	}

	key, ok := getToolTranscriptGroupRenderCacheKey(group, ctx)
	if !ok {
		return getPaddedTranscriptLines(renderToolTranscriptGroupWithContext(group, ctx), ctx.Padding)
	}
	if rendered, ok := ctx.Cache.get(key); ok {
		return rendered
	}

	rendered := getPaddedTranscriptLines(renderToolTranscriptGroupWithContext(group, ctx), ctx.Padding)
	ctx.Cache.set(key, rendered)
	return rendered
}

func renderToolTranscriptGroup(group toolTranscriptGroup, frame int) string {
	return renderToolTranscriptGroupWithContext(
		group,
		transcriptRenderContext{Frame: frame, Now: currentTime()},
	)
}

func renderToolTranscriptGroupWithContext(group toolTranscriptGroup, ctx transcriptRenderContext) string {
	return defaultToolTranscriptRenderer.RenderGroup(group, ctx)
}

func (group toolTranscriptGroup) outcome() toolTranscriptGroupOutcome {
	if len(group.seenIDs) == 0 {
		switch {
		case group.completed && group.terminalStatus != "":
			return toolTranscriptGroupOutcomeCompletedWithIssues
		case group.completed:
			return toolTranscriptGroupOutcomeCompleted
		case group.terminalStatus == toolTranscriptTerminalStatusFailed:
			return toolTranscriptGroupOutcomeFailed
		case group.terminalStatus == toolTranscriptTerminalStatusInterrupted:
			return toolTranscriptGroupOutcomeInterrupted
		default:
			return toolTranscriptGroupOutcomeRunning
		}
	}

	completedCount := 0
	failedCount := 0
	interruptedCount := 0
	for id := range group.seenIDs {
		switch group.terminalStatuses[id] {
		case toolTranscriptTerminalStatusFailed:
			failedCount++
		case toolTranscriptTerminalStatusInterrupted:
			interruptedCount++
		default:
			if group.completedIDs[id] {
				completedCount++
				continue
			}
			return toolTranscriptGroupOutcomeRunning
		}
	}

	switch {
	case completedCount > 0 && failedCount+interruptedCount > 0:
		return toolTranscriptGroupOutcomeCompletedWithIssues
	case completedCount > 0:
		return toolTranscriptGroupOutcomeCompleted
	case failedCount > 0:
		return toolTranscriptGroupOutcomeFailed
	case interruptedCount > 0:
		return toolTranscriptGroupOutcomeInterrupted
	default:
		return toolTranscriptGroupOutcomeRunning
	}
}

func getToolActionName(name string) string {
	nameValue2 := str.String(name)
	normalized := nameValue2.Normalized()
	normalized = strings.ReplaceAll(normalized, "-", "_")
	switch normalized {
	case "read", "read_file", "view_file", "open_file", "cat":
		return "Read"
	case "write", "write_file", "edit_file", "create_file":
		return "Write"
	case "patch", "apply_patch":
		return "Patch"
	case "web_search", "search_web", "search", "web":
		return "Web Search"
	case "web_extract", "extract_web":
		return "Web Extract"
	case "plan", "plan_tool", "update_plan":
		return "Plan"
	case "search_files":
		return "Search Files"
	case "session_search", "search_session":
		return "Session Search"
	case "memory_search", "search_memory", "memory":
		return "Memory Search"
	case "memory_extract", "extract_memory":
		return "Memory Extract"
	case "memory_add", "add_memory":
		return "Memory Add"
	case "memory_update", "update_memory":
		return "Memory Update"
	case "memory_delete", "delete_memory":
		return "Memory Delete"
	case "session_message", "session_messages":
		return "Session Messages"
	case "process":
		return "Process"
	case "exec", "exec_command", "run", "run_command", "shell", "bash":
		return "Run"
	default:
		return humanizeToolActionName(name)
	}
}

func normalizeToolTranscriptDetail(detail string) string {
	detailValue4 := str.String(detail)
	return strings.Join(strings.Fields(detailValue4.Trim()), " ")
}

func humanizeToolActionName(name string) string {
	nameValue3 := str.String(name)
	parts := strings.FieldsFunc(nameValue3.Trim(), func(r rune) bool {
		return r == '_' || r == '-' || r == ' '
	})
	for index, part := range parts {
		if part == "" {
			continue
		}

		runes := []rune(strings.ToLower(part))
		runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
		parts[index] = string(runes)
	}

	return strings.Join(parts, " ")
}
