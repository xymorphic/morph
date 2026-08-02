package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/xymorphic/morph/internal/trace"
	"github.com/xymorphic/morph/pkg/str"
)

type toolTranscriptRenderer struct{}

var defaultToolTranscriptRenderer = toolTranscriptRenderer{}

type processToolDetailGroupKey struct {
	operation trace.ProcessToolOperation
	target    string
}

func (toolTranscriptRenderer) RenderGroup(
	group toolTranscriptGroup,
	ctx transcriptRenderContext,
) string {
	return renderToolTranscriptGroupContent(group, ctx)
}

func renderToolTranscriptGroupContent(group toolTranscriptGroup, ctx transcriptRenderContext) string {
	actionValue := str.String(group.action)
	action := actionValue.Trim()
	if action == "" {
		action = "Tool"
	}
	if action == "Run" {
		return renderRunTranscriptGroup(group, ctx)
	}
	outcome := group.outcome()
	completed := outcome == toolTranscriptGroupOutcomeCompleted ||
		outcome == toolTranscriptGroupOutcomeCompletedWithIssues
	failed := outcome == toolTranscriptGroupOutcomeFailed
	interrupted := outcome == toolTranscriptGroupOutcomeInterrupted
	completedWithIssues := outcome == toolTranscriptGroupOutcomeCompletedWithIssues

	headerTitle := getToolTranscriptTitle(action, completed, group.details)
	if action == "Browser" && completedWithIssues {
		headerTitle = "Browser Actions Completed with Issues"
	} else if action == "Browser" {
		headerTitle = getBrowserToolTranscriptTitle(group.details, completed, failed, interrupted)
	} else if completedWithIssues {
		headerTitle = action + " Completed with Issues"
	} else if failed {
		headerTitle = "Failed " + action
	} else if interrupted {
		headerTitle = "Interrupted " + action
	}
	headerDuration := ""
	if len(group.details) == 1 {
		headerDuration = renderToolTranscriptDuration(group.details[0], ctx.Now)
	}
	header := lipgloss.NewStyle().
		Foreground(lipgloss.Color(getToolTranscriptDotColor(outcome))).
		Bold(true).
		Render(getToolTranscriptDot(outcome, ctx.Frame)) +
		lipgloss.NewStyle().
			Foreground(lipgloss.Color(defaultTUITheme.ToolTitle)).
			Render(" "+headerTitle) +
		lipgloss.NewStyle().
			Foreground(lipgloss.Color(defaultTUITheme.ToolDetail)).
			Render(headerDuration)

	details := make([]toolTranscriptDetail, 0, len(group.details))
	if shouldRenderToolTranscriptBranches(action) {
		for _, detail := range group.details {
			textValue := str.String(detail.text)
			if textValue.Trim() == "" && detail.planState == nil && detail.processState == nil {
				continue
			}
			if shouldSkipToolTranscriptBranch(action, completed, detail) {
				continue
			}
			actionValue2 := str.String(action)
			if actionValue2.Trim() == "Plan" && getPlanToolBranchDetail(detail.planState, detail.completed) == "" {
				continue
			}
			actionValue3 := str.String(action)
			if actionValue3.Trim() == "Process" && getProcessToolBranchDetail(detail.processState, detail.completed) == "" {
				continue
			}
			details = append(details, detail)
		}
	}
	if action == "Process" {
		details = compactProcessToolTranscriptDetails(details)
	}
	if len(details) == 0 {
		return header
	}

	branchStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(defaultTUITheme.ToolBranch))
	detailStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(defaultTUITheme.ToolDetail))
	lines := []string{header}
	for index, detail := range details {
		branch := "├"
		if index == len(details)-1 {
			branch = "└"
		}
		detailText := getToolTranscriptBranchDisplayDetail(group.action, detail)
		if action == "Browser" && len(group.details) > 1 {
			detailText = getBrowserToolGroupedBranchDetail(detail)
		}
		duration := renderToolTranscriptDuration(detail, ctx.Now)
		if action == "Browser" && len(group.details) == 1 {
			duration = ""
		}
		lines = append(lines, "  "+branchStyle.Render(branch)+" "+renderToolBranchDetail(detailText, duration, detailStyle))
		if detail.hasArtifact {
			for _, row := range renderBrowserArtifactRows(detail.artifact, detail.artifactStatus, ctx.Now) {
				lines = append(lines, "    "+row)
			}
		}
	}

	return strings.Join(lines, "\n")
}

func getBrowserToolGroupedBranchDetail(detail toolTranscriptDetail) string {
	action, target, _ := strings.Cut(detail.text, ":")
	label := strings.TrimPrefix(getBrowserActionLabel(strings.TrimSpace(action)), "Browser ")
	if target == "" {
		return getBrowserToolDisplayParts(label, detail.failure)
	}

	return getBrowserToolDisplayParts(label, strings.TrimSpace(target), detail.failure)
}

func shouldRenderToolTranscriptBranches(action string) bool {
	actionValue4 := str.String(action)
	switch actionValue4.Trim() {
	case "Session Messages", "Session Search", "Time", "Web Extract":
		return false
	default:
		return true
	}
}

func shouldSkipToolTranscriptBranch(action string, completed bool, detail toolTranscriptDetail) bool {
	actionValue5 := str.String(action)
	switch actionValue5.Trim() {
	case "Browser":
		if detail.terminalStatus != "" {
			return getBrowserToolTerminalBranchDetail(detail.text, detail.failure) == ""
		}

		return getBrowserToolBranchDetail(detail.text, detail.completed) == ""
	case "Plan":
	default:
		return false
	}
	if !completed || detail.planState == nil {
		return false
	}

	return isGenericPlanInputState(detail.planState)
}

func isGenericPlanInputState(state *trace.PlanToolState) bool {
	if state == nil {
		return false
	}
	if len(state.Changes) > 0 || state.TotalCount > 0 || state.CompletedCount > 0 {
		return false
	}

	switch state.Operation {
	case trace.PlanToolOperationUpdate, trace.PlanToolOperationClearCompleted:
		return state.ChangedCount > 0
	default:
		return false
	}
}

func renderToolBranchDetail(detail string, duration string, style lipgloss.Style) string {
	detailValue := str.String(detail)
	detail = detailValue.Trim()
	if detail == "" {
		return style.Render(duration)
	}

	parts := strings.Fields(detail)
	rendered := make([]string, 0, len(parts))
	for _, part := range parts {
		switch {
		case isToolDiffAdditionToken(part):
			rendered = append(rendered, lipgloss.NewStyle().Foreground(lipgloss.Color(defaultTUITheme.ToolAddition)).Render(part))
		case isToolDiffRemovalToken(part):
			rendered = append(rendered, lipgloss.NewStyle().Foreground(lipgloss.Color(defaultTUITheme.ToolDeletion)).Render(part))
		default:
			rendered = append(rendered, style.Render(part))
		}
	}

	return strings.Join(rendered, style.Render(" ")) + style.Render(duration)
}

func isToolDiffAdditionToken(value string) bool {
	return isToolSignedNumberToken(value, '+')
}

func isToolDiffRemovalToken(value string) bool {
	return isToolSignedNumberToken(value, '-')
}

func isToolSignedNumberToken(value string, sign byte) bool {
	if len(value) < 2 || value[0] != sign {
		return false
	}

	for _, r := range value[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}

func renderRunTranscriptGroup(group toolTranscriptGroup, ctx transcriptRenderContext) string {
	count := len(group.details)
	if count == 0 {
		count = 1
	}

	noun := "shell command"
	if count != 1 {
		noun = "shell commands"
	}
	verb := "Running"
	suffix := "…"
	outcome := group.outcome()
	completed := outcome == toolTranscriptGroupOutcomeCompleted
	failed := outcome == toolTranscriptGroupOutcomeFailed
	interrupted := outcome == toolTranscriptGroupOutcomeInterrupted
	completedWithIssues := outcome == toolTranscriptGroupOutcomeCompletedWithIssues
	if failed {
		verb = "Failed"
		suffix = ""
	} else if interrupted {
		verb = "Interrupted"
		suffix = ""
	} else if completed {
		verb = "Ran"
		suffix = ""
	} else if completedWithIssues {
		verb = "Ran"
		suffix = " with issues"
	}
	header := lipgloss.NewStyle().
		Foreground(lipgloss.Color(getToolTranscriptDotColor(outcome))).
		Bold(true).
		Render(getToolTranscriptDot(outcome, ctx.Frame)) +
		lipgloss.NewStyle().
			Foreground(lipgloss.Color(defaultTUITheme.ToolTitle)).
			Render(" "+verb+" ") +
		lipgloss.NewStyle().
			Foreground(lipgloss.Color(defaultTUITheme.UserTranscriptText)).
			Bold(true).
			Render(fmt.Sprintf("%d", count)) +
		lipgloss.NewStyle().
			Foreground(lipgloss.Color(defaultTUITheme.ToolTitle)).
			Render(" "+noun+suffix)

	detailStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(defaultTUITheme.ToolDetail))
	branchStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(defaultTUITheme.ToolBranch))
	lines := []string{header}
	for index, detail := range group.details {
		branch := "├"
		if index == len(group.details)-1 {
			branch = "└"
		}
		detailText := getToolTranscriptBranchDisplayDetail(group.action, detail)
		lines = append(lines, "  "+branchStyle.Render(branch)+" "+detailStyle.Render("$ "+detailText+renderToolTranscriptDuration(detail, ctx.Now)))
	}

	return strings.Join(lines, "\n")
}

func renderToolTranscriptDuration(detail toolTranscriptDetail, now time.Time) string {
	duration := getToolTranscriptDuration(detail, now)
	if duration <= 0 {
		return ""
	}

	return " (" + formatToolTranscriptDuration(duration) + ")"
}

func getToolTranscriptDuration(detail toolTranscriptDetail, now time.Time) time.Duration {
	if detail.startedAt.IsZero() {
		return 0
	}

	end := detail.completedAt
	if end.IsZero() {
		end = now
	}
	if end.IsZero() {
		end = currentTime()
	}
	if end.Before(detail.startedAt) {
		return 0
	}

	return end.Sub(detail.startedAt).Round(time.Second)
}

func formatToolTranscriptDuration(duration time.Duration) string {
	seconds := int(duration.Seconds())
	if seconds < 1 {
		seconds = 1
	}

	return fmt.Sprintf("%ds", seconds)
}

func getToolTranscriptDotColor(outcome toolTranscriptGroupOutcome) string {
	switch outcome {
	case toolTranscriptGroupOutcomeFailed, toolTranscriptGroupOutcomeInterrupted:
		return defaultTUITheme.ToolDeletion
	case toolTranscriptGroupOutcomeCompleted:
		return defaultTUITheme.ToolCompletedDot
	case toolTranscriptGroupOutcomeCompletedWithIssues:
		return defaultTUITheme.ToolWarningDot
	default:
		return defaultTUITheme.ToolRunningDot
	}
}

func getToolTranscriptDot(outcome toolTranscriptGroupOutcome, frame int) string {
	if outcome != toolTranscriptGroupOutcomeRunning {
		return "●"
	}

	frames := []string{"●", "◖", "◐", "◗", "●", "◔"}
	index := frame % len(frames)
	if index < 0 {
		index += len(frames)
	}

	return frames[index]
}

func getToolTranscriptTitle(action string, completed bool, details []toolTranscriptDetail) string {
	actionValue6 := str.String(action)
	switch actionValue6.Trim() {
	case "Plan":
		return getPlanToolTranscriptTitle(getPlanToolTranscriptOperation(details), completed)
	case "Process":
		return getProcessToolTranscriptTitle(getProcessToolTranscriptOperation(details), completed, details)
	case "Memory Search":
		if completed {
			return "Searched Memory"
		}

		return "Searching Memory"
	case "Memory Extract":
		if completed {
			return "Extracted Memory"
		}

		return "Extracting Memory"
	case "Memory Add":
		if completed {
			return "Added Memory"
		}

		return "Adding Memory"
	case "Memory Update":
		if completed {
			return "Updated Memory"
		}

		return "Updating Memory"
	case "Memory Delete":
		if completed {
			return "Deleted Memory"
		}

		return "Deleting Memory"
	case "Search Files":
		if completed {
			return "Searched Files"
		}

		return "Searching Files"
	case "Session Messages":
		if completed {
			return "Fetched Session Messages"
		}

		return "Fetching Session Messages"
	case "Session Search":
		if completed {
			return "Searched Session"
		}

		return "Searching Session"
	case "Web Extract":
		if completed {
			return "Extraction finished"
		}

		return "Extracting from web"
	case "Time":
		if completed {
			return "Checked time"
		}

		return "Checking time"
	case "Automation":
		return getAutomationToolTranscriptTitle(details, completed)
	case "Browser":
		return getBrowserToolTranscriptTitle(details, completed, false, false)
	}

	if !completed {
		return action
	}
	actionValue7 := str.String(action)
	switch actionValue7.Trim() {
	case "Run":
		return "Ran"
	case "Write":
		return "Wrote"
	case "Web Search":
		return "Searched"
	case "Read":
		return "Read"
	case "Patch":
		return "Patch"
	default:
		actionValue8 := str.String(action)
		return actionValue8.Trim()
	}
}

func getBrowserToolTranscriptTitle(
	details []toolTranscriptDetail,
	completed bool,
	failed bool,
	interrupted bool,
) string {
	action := ""
	for _, detail := range details {
		candidate, _, _ := strings.Cut(detail.text, ":")
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || strings.EqualFold(candidate, "browser") {
			continue
		}
		if action != "" && action != candidate {
			return getBrowserToolFallbackTitle(completed, failed, interrupted)
		}
		action = candidate
	}
	if failed {
		return getBrowserActionFailedTitle(action)
	}
	if interrupted {
		return getBrowserActionInterruptedTitle(action)
	}
	if completed {
		return getBrowserActionCompletedTitle(action)
	}

	return getBrowserActionPendingTitle(action)
}

func getBrowserToolFallbackTitle(completed, failed, interrupted bool) string {
	switch {
	case failed:
		return "Browser Actions Failed"
	case interrupted:
		return "Browser Actions Interrupted"
	case completed:
		return "Completed Browser Actions"
	default:
		return "Running Browser Actions"
	}
}

type browserActionTitles struct {
	label       string
	pending     string
	completed   string
	failed      string
	interrupted string
}

var browserTranscriptActionTitles = map[string]browserActionTitles{
	"status": {
		label: "Browser Status", pending: "Checking Browser Status", completed: "Checked Browser Status",
		failed: "Failed to Check Browser Status", interrupted: "Browser Status Check Interrupted",
	},
	"start": {
		label: "Browser Start", pending: "Starting Browser", completed: "Started Browser",
		failed: "Failed to Start Browser", interrupted: "Browser Start Interrupted",
	},
	"stop": {
		label: "Browser Stop", pending: "Stopping Browser", completed: "Stopped Browser",
		failed: "Failed to Stop Browser", interrupted: "Browser Stop Interrupted",
	},
	"profiles": {
		label: "Browser Profile Listing", pending: "Listing Browser Profiles", completed: "Listed Browser Profiles",
		failed: "Failed to List Browser Profiles", interrupted: "Browser Profile Listing Interrupted",
	},
	"tabs": {
		label: "Browser Tab Listing", pending: "Listing Browser Tabs", completed: "Listed Browser Tabs",
		failed: "Failed to List Browser Tabs", interrupted: "Browser Tab Listing Interrupted",
	},
	"open": {
		label: "Browser Tab Opening", pending: "Opening Browser Tab", completed: "Opened Browser Tab",
		failed: "Failed to Open Browser Tab", interrupted: "Browser Tab Opening Interrupted",
	},
	"focus": {
		label: "Browser Tab Focus", pending: "Focusing Browser Tab", completed: "Focused Browser Tab",
		failed: "Failed to Focus Browser Tab", interrupted: "Browser Tab Focus Interrupted",
	},
	"close": {
		label: "Browser Tab Closure", pending: "Closing Browser Tab", completed: "Closed Browser Tab",
		failed: "Failed to Close Browser Tab", interrupted: "Browser Tab Closure Interrupted",
	},
	"navigate": {
		label: "Browser Navigation", pending: "Navigating Browser", completed: "Navigated Browser",
		failed: "Browser Navigation Failed", interrupted: "Browser Navigation Interrupted",
	},
	"reload": {
		label: "Browser Reload", pending: "Reloading Browser Page", completed: "Reloaded Browser Page",
		failed: "Failed to Reload Browser Page", interrupted: "Browser Reload Interrupted",
	},
	"snapshot": {
		label: "Browser Snapshot", pending: "Capturing Snapshot", completed: "Captured Snapshot",
		failed: "Failed to Capture Snapshot", interrupted: "Snapshot Capture Interrupted",
	},
	"screenshot": {
		label: "Browser Screenshot", pending: "Capturing Browser Screenshot", completed: "Captured Browser Screenshot",
		failed: "Failed to Capture Browser Screenshot", interrupted: "Browser Screenshot Interrupted",
	},
	"pdf": {
		label: "Browser PDF", pending: "Creating Browser PDF", completed: "Created Browser PDF",
		failed: "Failed to Create Browser PDF", interrupted: "Browser PDF Creation Interrupted",
	},
	"console": {
		label: "Browser Console Read", pending: "Reading Browser Console", completed: "Read Browser Console",
		failed: "Failed to Read Browser Console", interrupted: "Browser Console Read Interrupted",
	},
	"click": {
		label: "Browser Element Click", pending: "Clicking Browser Element", completed: "Clicked Browser Element",
		failed: "Failed to Click Browser Element", interrupted: "Browser Element Click Interrupted",
	},
	"type": {
		label: "Browser Element Typing", pending: "Typing into Browser Element", completed: "Typed into Browser Element",
		failed: "Failed to Type into Browser Element", interrupted: "Browser Element Typing Interrupted",
	},
	"press": {
		label: "Browser Key Press", pending: "Pressing Browser Key", completed: "Pressed Browser Key",
		failed: "Failed to Press Browser Key", interrupted: "Browser Key Press Interrupted",
	},
	"scroll": {
		label: "Browser Page Scroll", pending: "Scrolling Browser Page", completed: "Scrolled Browser Page",
		failed: "Failed to Scroll Browser Page", interrupted: "Browser Page Scroll Interrupted",
	},
	"select": {
		label: "Browser Option Selection", pending: "Selecting Browser Option", completed: "Selected Browser Option",
		failed: "Failed to Select Browser Option", interrupted: "Browser Option Selection Interrupted",
	},
	"upload": {
		label: "Browser File Upload", pending: "Uploading File to Browser", completed: "Uploaded File to Browser",
		failed: "Failed to Upload File to Browser", interrupted: "Browser File Upload Interrupted",
	},
	"download": {
		label: "Browser File Download", pending: "Downloading File from Browser", completed: "Downloaded File from Browser",
		failed: "Failed to Download File from Browser", interrupted: "Browser File Download Interrupted",
	},
	"export_artifact": {
		label: "Browser Artifact Export", pending: "Exporting Browser Artifact", completed: "Exported Browser Artifact",
		failed: "Failed to Export Browser Artifact", interrupted: "Browser Artifact Export Interrupted",
	},
	"accept_dialog": {
		label: "Browser Dialog Acceptance", pending: "Accepting Browser Dialog", completed: "Accepted Browser Dialog",
		failed: "Failed to Accept Browser Dialog", interrupted: "Browser Dialog Acceptance Interrupted",
	},
	"dismiss_dialog": {
		label: "Browser Dialog Dismissal", pending: "Dismissing Browser Dialog", completed: "Dismissed Browser Dialog",
		failed: "Failed to Dismiss Browser Dialog", interrupted: "Browser Dialog Dismissal Interrupted",
	},
	"wait": {
		label: "Browser Condition Wait", pending: "Waiting for Browser Condition", completed: "Browser Condition Satisfied",
		failed: "Browser Wait Failed", interrupted: "Browser Wait Interrupted",
	},
	"back": {
		label: "Browser Back Navigation", pending: "Navigating Back in Browser", completed: "Navigated Back in Browser",
		failed: "Failed to Navigate Back in Browser", interrupted: "Browser Back Navigation Interrupted",
	},
	"forward": {
		label: "Browser Forward Navigation", pending: "Navigating Forward in Browser", completed: "Navigated Forward in Browser",
		failed: "Failed to Navigate Forward in Browser", interrupted: "Browser Forward Navigation Interrupted",
	},
}

func getBrowserActionLabel(action string) string {
	if titles, ok := browserTranscriptActionTitles[action]; ok {
		return titles.label
	}
	if action == "" {
		return "Browser Action"
	}

	return "Browser " + strings.ReplaceAll(action, "_", " ")
}

func getBrowserActionPendingTitle(action string) string {
	if titles, ok := browserTranscriptActionTitles[action]; ok {
		return titles.pending
	}

	return "Running " + getBrowserActionLabel(action)
}

func getBrowserActionCompletedTitle(action string) string {
	if titles, ok := browserTranscriptActionTitles[action]; ok {
		return titles.completed
	}

	return "Completed " + getBrowserActionLabel(action)
}

func getBrowserActionFailedTitle(action string) string {
	if titles, ok := browserTranscriptActionTitles[action]; ok {
		return titles.failed
	}

	return getBrowserActionLabel(action) + " Failed"
}

func getBrowserActionInterruptedTitle(action string) string {
	if titles, ok := browserTranscriptActionTitles[action]; ok {
		return titles.interrupted
	}

	return getBrowserActionLabel(action) + " Interrupted"
}

func getAutomationToolTranscriptTitle(details []toolTranscriptDetail, completed bool) string {
	action := ""
	for _, detail := range details {
		candidate, _ := parseAutomationToolDisplayDetail(detail.text)
		if candidate == "" {
			continue
		}
		if action != "" && action != candidate {
			if completed {
				return "Managed Automations"
			}

			return "Managing Automations"
		}
		action = candidate
	}

	if completed {
		switch action {
		case "status":
			return "Checked Automation Status"
		case "list":
			return "Listed Automations"
		case "add":
			return "Added Automation"
		case "update":
			return "Updated Automation"
		case "pause":
			return "Paused Automation"
		case "resume":
			return "Resumed Automation"
		case "run":
			return "Ran Automation"
		case "remove":
			return "Removed Automation"
		case "runs":
			return "Listed Automation Runs"
		default:
			return "Managed Automation"
		}
	}

	switch action {
	case "status":
		return "Checking Automation Status"
	case "list":
		return "Listing Automations"
	case "add":
		return "Adding Automation"
	case "update":
		return "Updating Automation"
	case "pause":
		return "Pausing Automation"
	case "resume":
		return "Resuming Automation"
	case "run":
		return "Running Automation"
	case "remove":
		return "Removing Automation"
	case "runs":
		return "Listing Automation Runs"
	default:
		return "Managing Automation"
	}
}

func getProcessToolTranscriptTitle(
	operation trace.ProcessToolOperation,
	completed bool,
	details []toolTranscriptDetail,
) string {
	if completed && hasOnlyProcessToolTranscriptErrors(details) {
		return "Process failed"
	}

	switch operation {
	case trace.ProcessToolOperationStart:
		if completed {
			return "Process started"
		}

		return "Starting process"
	case trace.ProcessToolOperationStatus:
		if completed {
			if status := getProcessToolTranscriptStatus(details); status != "" {
				return "Process " + status
			}

			return "Process status checked"
		}

		return "Checking process"
	case trace.ProcessToolOperationRead:
		if completed {
			return "Output read"
		}

		return "Reading process output"
	case trace.ProcessToolOperationStop:
		if completed {
			return "Process stopped"
		}

		return "Stopping process"
	case trace.ProcessToolOperationList:
		if completed {
			return "Listed processes"
		}

		return "Listing processes"
	default:
		if completed {
			return "Process updated"
		}

		return "Processing"
	}
}

func hasOnlyProcessToolTranscriptErrors(details []toolTranscriptDetail) bool {
	foundProcess := false
	for _, detail := range details {
		if detail.processState == nil {
			continue
		}

		foundProcess = true
		if !hasProcessToolError(detail.processState) {
			return false
		}
	}

	return foundProcess
}

func compactProcessToolTranscriptDetails(details []toolTranscriptDetail) []toolTranscriptDetail {
	if len(details) <= 1 {
		return details
	}

	type groupState struct {
		failedCount int
		lastFailed  *toolTranscriptDetail
		lastSuccess *toolTranscriptDetail
	}

	groups := map[processToolDetailGroupKey]*groupState{}
	order := make([]processToolDetailGroupKey, 0, len(details))
	for _, detail := range details {
		key := getProcessToolDetailGroupKey(detail)
		if key.operation == "" {
			key.operation = trace.ProcessToolOperation(detail.text)
		}
		state := groups[key]
		if state == nil {
			state = &groupState{}
			groups[key] = state
			order = append(order, key)
		}

		copied := detail
		if hasProcessToolError(detail.processState) {
			state.failedCount++
			state.lastFailed = &copied
			continue
		}
		state.lastSuccess = &copied
	}

	if len(order) == 0 {
		return details
	}

	result := make([]toolTranscriptDetail, 0, len(order)*2)
	for _, key := range order {
		state := groups[key]
		if state == nil {
			continue
		}
		if state.failedCount > 0 {
			failed := processToolFailedAttemptDetail(state.failedCount, state.lastFailed)
			if failed.text != "" || failed.processState != nil {
				result = append(result, failed)
			}
		}
		if state.lastSuccess != nil {
			result = append(result, *state.lastSuccess)
			continue
		}
		if state.lastFailed != nil && state.failedCount == 0 {
			result = append(result, *state.lastFailed)
		}
	}
	if len(result) == 0 {
		return details
	}

	return result
}

func getProcessToolDetailGroupKey(detail toolTranscriptDetail) processToolDetailGroupKey {
	state := detail.processState
	if state == nil {
		return processToolDetailGroupKey{}
	}
	processIDValue := str.String(state.ProcessID)
	target := processIDValue.Trim()
	if state.Operation == trace.ProcessToolOperationStart || target == "" {
		commandValue := str.String(state.Command)
		target = commandValue.Trim()
	}

	return processToolDetailGroupKey{operation: state.Operation, target: target}
}

func processToolFailedAttemptDetail(count int, detail *toolTranscriptDetail) toolTranscriptDetail {
	if detail == nil || count <= 0 {
		return toolTranscriptDetail{}
	}

	errorValue := str.String(detail.processState.Error)
	message := errorValue.Trim()
	if message == "" {
		message = "unknown error"
	}

	noun := "attempt"
	if count != 1 {
		noun = "attempts"
	}
	copied := *detail
	copied.text = fmt.Sprintf("Failed %d %s: %s", count, noun, message)
	copied.processState = nil
	return copied
}

func getProcessToolTranscriptOperation(details []toolTranscriptDetail) trace.ProcessToolOperation {
	for _, detail := range details {
		if detail.processState != nil && detail.processState.Operation != "" {
			return detail.processState.Operation
		}
	}

	return ""
}

func getProcessToolTranscriptStatus(details []toolTranscriptDetail) string {
	for index := len(details) - 1; index >= 0; index-- {
		if details[index].processState == nil {
			continue
		}

		statusValue := str.String(details[index].processState.Status)
		if status := statusValue.Trim(); status != "" {
			return status
		}
	}

	return ""
}

func getPlanToolTranscriptTitle(operation string, completed bool) string {
	switch operation {
	case "read":
		if completed {
			return "Plan read"
		}

		return "Reading plan"
	case "clear_completed":
		if completed {
			return "Plan cleared"
		}

		return "Clearing completed plan steps"
	default:
		if completed {
			return "Plan updated"
		}

		return "Updating plan"
	}
}

func getPlanToolTranscriptOperation(details []toolTranscriptDetail) string {
	for _, detail := range details {
		if detail.planState == nil {
			continue
		}

		switch detail.planState.Operation {
		case trace.PlanToolOperationRead:
			return "read"
		case trace.PlanToolOperationClearCompleted:
			return "clear_completed"
		case trace.PlanToolOperationUpdate:
			return "update"
		}
	}

	return "update"
}
