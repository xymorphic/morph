package tui

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/xymorphic/morph/internal/guardrails"
	"github.com/xymorphic/morph/internal/trace"
	"github.com/xymorphic/morph/pkg/str"
)

var runToolLegacyTimeoutPattern = regexp.MustCompile(`\s+\(([0-9]+(?:\.[0-9]+)?s)\)$`)
var runToolTimeoutHintPattern = regexp.MustCompile(`\s+\[(?:terminates in|timeout) [^\]]+\]`)

type toolDisplaySpec struct {
	inputDetail  func(map[string]any) string
	outputDetail func(map[string]any) string
	inputState   func(map[string]any) *trace.PlanToolState
	outputState  func(map[string]any) *trace.PlanToolState
	branchDetail func(string, bool) string
}

func getToolInputDisplayDetail(name string, input string) string {
	var fields map[string]any
	inputValue := str.String(input)
	if err := json.Unmarshal([]byte(inputValue.Trim()), &fields); err != nil {
		return ""
	}

	spec := getToolDisplaySpec(name)
	if spec.inputDetail == nil {
		return ""
	}

	return spec.inputDetail(fields)
}

func getToolInputDisplayState(name string, input string) *trace.PlanToolState {
	var fields map[string]any
	inputValue2 := str.String(input)
	if err := json.Unmarshal([]byte(inputValue2.Trim()), &fields); err != nil {
		return nil
	}

	spec := getToolDisplaySpec(name)
	if spec.inputState == nil {
		return nil
	}

	return spec.inputState(fields)
}

func getToolOutputDisplayDetail(name string, output string) string {
	var fields map[string]any
	outputValue := str.String(output)
	if err := json.Unmarshal([]byte(outputValue.Trim()), &fields); err != nil {
		return ""
	}

	spec := getToolDisplaySpec(name)
	if spec.outputDetail == nil {
		return ""
	}

	return spec.outputDetail(fields)
}

func getToolFailureDisplayDetail(name string, output string) string {
	if getToolActionName(name) != "Browser" {
		return ""
	}

	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &envelope); err != nil || len(envelope.Error) == 0 {
		return ""
	}

	var failure struct {
		Message   string `json:"message"`
		Retryable bool   `json:"retryable"`
	}
	if err := json.Unmarshal(envelope.Error, &failure); err != nil {
		return ""
	}

	message := strings.TrimSpace(failure.Message)
	if message == "" {
		return ""
	}

	return formatToolFailureDisplayMessage(message, failure.Retryable)
}

func formatToolFailureDisplayMessage(message string, retryable bool) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}

	messageRunes := []rune(message)
	messageRunes[0] = unicode.ToUpper(messageRunes[0])
	message = string(messageRunes)
	if retryable {
		message += " · retryable"
	}

	return message
}

func getToolOutputDisplayState(name string, output string) *trace.PlanToolState {
	var fields map[string]any
	outputValue2 := str.String(output)
	if err := json.Unmarshal([]byte(outputValue2.Trim()), &fields); err != nil {
		return nil
	}

	spec := getToolDisplaySpec(name)
	if spec.outputState == nil {
		return nil
	}

	return spec.outputState(fields)
}

func getToolInputProcessDisplayState(name string, input string) *trace.ProcessToolState {
	if getToolActionName(name) != "Process" {
		return nil
	}

	return trace.ProcessToolInputState(input)
}

func getToolOutputProcessDisplayState(name string, output string) *trace.ProcessToolState {
	if getToolActionName(name) != "Process" {
		return nil
	}

	return trace.ProcessToolOutputState(output)
}

func getToolBranchDisplayDetail(action string, detail string, completed bool) string {
	spec := getToolDisplaySpecForAction(action)
	if spec.branchDetail == nil {
		detailValue := str.String(detail)
		return detailValue.Trim()
	}

	return spec.branchDetail(detail, completed)
}

func getToolTranscriptBranchDisplayDetail(action string, detail toolTranscriptDetail) string {
	actionValue := str.String(action)
	if actionValue.Trim() == "Plan" {
		return getPlanToolBranchDetail(detail.planState, detail.completed)
	}
	actionValue2 := str.String(action)
	if actionValue2.Trim() == "Process" {
		if branch := getProcessToolBranchDetail(detail.processState, detail.completed); branch != "" {
			return branch
		}

		textValue := str.String(detail.text)
		return textValue.Trim()
	}
	if actionValue2.Trim() == "Browser" && detail.terminalStatus != "" {
		return getBrowserToolTerminalBranchDetail(detail.text, detail.failure)
	}

	return getToolBranchDisplayDetail(action, detail.text, detail.completed)
}

func getToolDisplaySpec(name string) toolDisplaySpec {
	switch normalizeToolDisplayName(name) {
	case "search_files":
		return toolDisplaySpec{
			inputDetail: getSearchFilesToolDisplayDetail,
		}
	case "read", "read_file", "view_file", "open_file", "cat":
		return toolDisplaySpec{
			inputDetail: func(fields map[string]any) string {
				return getPathToolDisplayDetail(name, fields)
			},
		}
	case "write", "write_file", "edit_file", "create_file":
		return toolDisplaySpec{
			inputDetail: func(fields map[string]any) string {
				return getPathToolDisplayDetail(name, fields)
			},
		}
	case "patch", "apply_patch":
		return toolDisplaySpec{
			inputDetail: func(fields map[string]any) string {
				return getPatchToolDisplayDetail(name, fields)
			},
		}
	}

	action := getToolActionName(name)
	spec := getToolDisplaySpecForAction(action)
	if spec.inputDetail != nil || spec.outputDetail != nil || spec.inputState != nil ||
		spec.outputState != nil || spec.branchDetail != nil {
		return spec
	}

	if isGenericToolParamDisplayEnabled(name) {
		return toolDisplaySpec{
			inputDetail: func(fields map[string]any) string {
				return getGenericToolDisplayDetail(name, fields)
			},
		}
	}

	return toolDisplaySpec{}
}

func normalizeToolDisplayName(name string) string {
	nameValue := str.String(name)
	name = nameValue.Normalized()
	return strings.ReplaceAll(name, "-", "_")
}

func getToolDisplaySpecForAction(action string) toolDisplaySpec {
	actionValue3 := str.String(action)
	switch actionValue3.Trim() {
	case "Run":
		return toolDisplaySpec{
			inputDetail:  getRunToolDisplayDetail,
			branchDetail: normalizeRunToolDetailText,
		}
	case "Web Search", "Memory Search", "Session Search":
		return toolDisplaySpec{
			inputDetail: getSearchToolDisplayDetail,
		}
	case "Plan":
		return toolDisplaySpec{
			inputState:  getPlanToolInputDisplayState,
			outputState: getPlanToolOutputDisplayState,
		}
	case "Memory Extract":
		return toolDisplaySpec{branchDetail: getStaticToolBranchDetail("Extract memories")}
	case "Memory Add":
		return toolDisplaySpec{branchDetail: getStaticToolBranchDetail("Add memory")}
	case "Memory Update":
		return toolDisplaySpec{branchDetail: getStaticToolBranchDetail("Update memory")}
	case "Memory Delete":
		return toolDisplaySpec{branchDetail: getStaticToolBranchDetail("Delete memory")}
	case "Session Messages":
		return toolDisplaySpec{
			inputDetail:  getSessionMessagesToolDisplayDetail,
			branchDetail: getSessionMessagesToolBranchDetail,
		}
	case "Automation":
		return toolDisplaySpec{
			inputDetail:  getAutomationToolDisplayDetail,
			branchDetail: getAutomationToolBranchDetail,
		}
	case "Browser":
		return toolDisplaySpec{
			inputDetail:  getBrowserToolDisplayDetail,
			outputDetail: getBrowserToolOutputDisplayDetail,
			branchDetail: getBrowserToolBranchDetail,
		}
	default:
		return toolDisplaySpec{}
	}
}

func getBrowserToolOutputDisplayDetail(fields map[string]any) string {
	kind := str.String(getMapString(fields, "kind")).Normalized()
	handle := strings.TrimSpace(getMapString(fields, "handle"))
	if kind == "" || handle == "" {
		return ""
	}
	name := strings.TrimSpace(getMapString(fields, "name"))
	if name == "" {
		name = kind
	}
	detail := name + " · " + handle
	if size := formatOptionalToolNumber(fields["size"]); size != "" {
		detail += " · " + size + " bytes"
	}
	return kind + ":" + detail
}

func getBrowserToolDisplayDetail(fields map[string]any) string {
	action := str.String(getMapString(fields, "action")).Normalized()
	if action == "" {
		return "browser"
	}
	target := getBrowserToolDisplayTarget(action, fields)
	if target == "" {
		return action
	}

	return action + ":" + target
}

func getBrowserToolDisplayTarget(action string, fields map[string]any) string {
	switch action {
	case "status", "profiles":
		return ""
	case "start":
		return getBrowserToolNamedTarget("Profile", getMapString(fields, "profile"))
	case "stop", "tabs":
		return getBrowserToolNamedTarget("Session", getMapString(fields, "session_id"))
	case "open", "navigate":
		return getBrowserToolURLTarget(getMapString(fields, "url"))
	case "click", "type", "select", "upload", "download":
		return getBrowserToolNamedTarget("Element", getMapString(fields, "ref"))
	case "accept_dialog", "dismiss_dialog":
		return getBrowserToolNamedTarget("Dialog", getMapString(fields, "ref"))
	case "screenshot":
		capture := "Viewport"
		if fullPage, ok := fields["full_page"].(bool); ok && fullPage {
			capture = "Full page"
		}
		return getBrowserToolDisplayParts(capture, getBrowserToolTabTarget(fields))
	case "wait":
		context := getBrowserToolTabTarget(fields)
		if strings.TrimSpace(getMapString(fields, "condition")) == "visible" {
			context = getBrowserToolNamedTarget("Element", getMapString(fields, "ref"))
		}
		return getBrowserToolDisplayParts(
			getBrowserToolWaitTarget(getMapString(fields, "condition")),
			context,
		)
	default:
		return getBrowserToolTabTarget(fields)
	}
}

func getBrowserToolURLTarget(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Hostname() == "" {
		return ""
	}

	return parsed.Scheme + "://" + parsed.Host
}

func getBrowserToolTabTarget(fields map[string]any) string {
	return getBrowserToolNamedTarget("Tab", getMapString(fields, "tab_id"))
}

func getBrowserToolNamedTarget(label string, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	return label + " " + value
}

func getBrowserToolDisplayParts(values ...string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			parts = append(parts, value)
		}
	}

	return strings.Join(parts, " · ")
}

func getBrowserToolWaitTarget(condition string) string {
	switch strings.TrimSpace(condition) {
	case "load":
		return "Page load"
	case "text":
		return "Text appears"
	case "url":
		return "URL matches"
	case "visible":
		return "Element becomes visible"
	default:
		return "Condition"
	}
}

func getBrowserToolBranchDetail(detail string, _ bool) string {
	_, target, _ := strings.Cut(detail, ":")

	return strings.TrimSpace(target)
}

func getBrowserToolTerminalBranchDetail(detail string, failure string) string {
	_, target, _ := strings.Cut(detail, ":")

	return getBrowserToolDisplayParts(strings.TrimSpace(target), failure)
}

func getAutomationToolDisplayDetail(fields map[string]any) string {
	actionValue := str.String(getMapString(fields, "action"))
	action := actionValue.Normalized()
	if action == "" {
		return "manage"
	}

	target := getMapString(fields, "id")
	switch action {
	case "add":
		job, _ := fields["job"].(map[string]any)
		target = firstNonEmptyToolDisplay(getMapString(job, "name"), getMapString(job, "id"))
	case "runs":
		query, _ := fields["run_query"].(map[string]any)
		target = getMapString(query, "job_id")
	}

	if target == "" {
		return action
	}

	return action + ":" + target
}

func getAutomationToolBranchDetail(detail string, completed bool) string {
	action, target := parseAutomationToolDisplayDetail(detail)
	if completed {
		switch action {
		case "status":
			return "Checked automation status"
		case "list":
			return "Listed automations"
		case "add":
			return automationToolActionTarget("Added automation", target)
		case "update":
			return automationToolActionTarget("Updated automation", target)
		case "pause":
			return automationToolActionTarget("Paused automation", target)
		case "resume":
			return automationToolActionTarget("Resumed automation", target)
		case "run":
			return automationToolActionTarget("Ran automation", target)
		case "remove":
			return automationToolActionTarget("Removed automation", target)
		case "runs":
			if target != "" {
				return "Listed runs for " + target
			}
			return "Listed automation runs"
		default:
			return "Managed automations"
		}
	}

	switch action {
	case "status":
		return "Checking automation status"
	case "list":
		return "Listing automations"
	case "add":
		return automationToolActionTarget("Adding automation", target)
	case "update":
		return automationToolActionTarget("Updating automation", target)
	case "pause":
		return automationToolActionTarget("Pausing automation", target)
	case "resume":
		return automationToolActionTarget("Resuming automation", target)
	case "run":
		return automationToolActionTarget("Running automation", target)
	case "remove":
		return automationToolActionTarget("Removing automation", target)
	case "runs":
		if target != "" {
			return "Listing runs for " + target
		}
		return "Listing automation runs"
	default:
		return "Managing automations"
	}
}

func parseAutomationToolDisplayDetail(detail string) (string, string) {
	detailValue := str.String(detail)
	action, target, _ := strings.Cut(detailValue.Trim(), ":")
	actionValue := str.String(action)
	targetValue := str.String(target)
	return actionValue.Normalized(), targetValue.Trim()
}

func automationToolActionTarget(label string, target string) string {
	targetValue := str.String(target)
	if target := targetValue.Trim(); target != "" {
		return label + " " + target
	}

	return label
}

func getStaticToolBranchDetail(label string) func(string, bool) string {
	return func(string, bool) string {
		return label
	}
}

func getPlanToolInputDisplayState(fields map[string]any) *trace.PlanToolState {
	steps, hasSteps := fields["steps"]
	if !hasSteps || steps == nil {
		return &trace.PlanToolState{Operation: trace.PlanToolOperationRead}
	}

	stepCount := len(getMapAnySlice(fields, "steps"))
	if clearCompleted, _ := fields["clear_completed"].(bool); clearCompleted {
		return &trace.PlanToolState{
			Operation:    trace.PlanToolOperationClearCompleted,
			ChangedCount: stepCount,
		}
	}

	return &trace.PlanToolState{
		Operation:    trace.PlanToolOperationUpdate,
		ChangedCount: stepCount,
	}
}

func getPlanToolOutputDisplayState(fields map[string]any) *trace.PlanToolState {
	fields = getPlanToolOutputFields(fields)
	summary, _ := fields["summary"].(map[string]any)
	return &trace.PlanToolState{
		TotalCount:     getMapNumber(summary, "total"),
		CompletedCount: getMapNumber(summary, "completed"),
		Changes:        getPlanToolChanges(fields["changes"]),
	}
}

func getPlanToolOutputFields(fields map[string]any) map[string]any {
	if len(fields) == 0 || fields["summary"] != nil || fields["changes"] != nil {
		return fields
	}

	output, ok := fields["output"].(string)
	outputValue3 := str.String(output)
	if !ok || outputValue3.Trim() == "" {
		return fields
	}

	unwrapped := map[string]any{}
	outputValue4 := str.String(output)
	if err := json.Unmarshal([]byte(outputValue4.Trim()), &unwrapped); err != nil {
		return fields
	}
	if len(unwrapped) == 0 {
		return fields
	}

	return unwrapped
}

func getPlanToolBranchDetail(state *trace.PlanToolState, completed bool) string {
	if state == nil {
		return ""
	}

	switch state.Operation {
	case trace.PlanToolOperationRead:
		if completed && state.TotalCount > 0 {
			return fmt.Sprintf("Found %s", formatTaskCount(state.TotalCount))
		}
		if completed {
			return ""
		}

		return "Read current plan"
	case trace.PlanToolOperationClearCompleted:
		if state.ChangedCount > 0 {
			return fmt.Sprintf("Cleared %s", formatTaskCount(state.ChangedCount))
		}
		if completed {
			return ""
		}

		return "Cleared completed tasks"
	default:
		if detail := getPlanToolChangeBranchDetail(state.Changes); detail != "" {
			return detail
		}
		if completed && state.TotalCount > 0 && state.CompletedCount == state.TotalCount {
			return fmt.Sprintf("Completed all %s", formatTaskCount(state.TotalCount))
		}
		if !completed && state.ChangedCount > 0 {
			return fmt.Sprintf("Updated %s", formatTaskCount(state.ChangedCount))
		}

		return ""
	}
}

func clonePlanToolDisplayState(state *trace.PlanToolState) *trace.PlanToolState {
	if state == nil {
		return nil
	}

	cloned := *state
	cloned.Changes = append([]trace.PlanToolChange(nil), state.Changes...)
	return &cloned
}

func mergePlanToolDisplayState(current *trace.PlanToolState, next *trace.PlanToolState) *trace.PlanToolState {
	if current == nil && next == nil {
		return nil
	}
	if current == nil {
		return clonePlanToolDisplayState(next)
	}
	if next == nil {
		return clonePlanToolDisplayState(current)
	}

	merged := *current
	if merged.Operation == "" {
		merged.Operation = next.Operation
	}
	if next.Operation != "" &&
		merged.Operation != trace.PlanToolOperationRead &&
		merged.Operation != trace.PlanToolOperationClearCompleted {
		merged.Operation = next.Operation
	}
	if next.ChangedCount > 0 {
		merged.ChangedCount = next.ChangedCount
	}
	if next.TotalCount > 0 {
		merged.TotalCount = next.TotalCount
	}
	if next.CompletedCount > 0 {
		merged.CompletedCount = next.CompletedCount
	}
	if len(next.Changes) > 0 {
		merged.Changes = append([]trace.PlanToolChange(nil), next.Changes...)
	}

	return &merged
}

func getProcessToolBranchDetail(state *trace.ProcessToolState, completed bool) string {
	if state == nil {
		return ""
	}
	if detail := getProcessToolErrorDetail(state); detail != "" {
		return detail
	}

	switch state.Operation {
	case trace.ProcessToolOperationStart:
		if completed {
			return getProcessToolStatusDetail(state)
		}

		return firstNonEmptyToolDisplay(state.Command, "Start process")
	case trace.ProcessToolOperationStatus:
		if completed {
			return getProcessToolStatusDetail(state)
		}

		return firstNonEmptyToolDisplay(state.ProcessID, "Check process status")
	case trace.ProcessToolOperationRead:
		if completed {
			return getProcessToolOutputDetail(state)
		}

		return firstNonEmptyToolDisplay(state.ProcessID, "Read process output")
	case trace.ProcessToolOperationStop:
		if completed {
			return getProcessToolStatusDetail(state)
		}

		return firstNonEmptyToolDisplay(state.ProcessID, "Stop process")
	case trace.ProcessToolOperationList:
		if completed {
			return fmt.Sprintf("Found %s", formatProcessCount(state.Count))
		}

		return ""
	default:
		return getProcessToolStatusDetail(state)
	}
}

func getProcessToolErrorDetail(state *trace.ProcessToolState) string {
	if state == nil {
		return ""
	}

	errorValue := str.String(state.Error)
	message := errorValue.Trim()
	if message == "" {
		return ""
	}

	prefix := "Failed"
	if state.Operation != "" {
		operationValue := str.String(string(state.Operation))
		prefix = operationValue.Trim() + " failed"
	}
	errorCodeValue := str.String(state.ErrorCode)
	if code := errorCodeValue.Trim(); code != "" {
		return prefix + ": " + message + " (" + code + ")"
	}

	return prefix + ": " + message
}

func getProcessToolStatusDetail(state *trace.ProcessToolState) string {
	if state == nil {
		return ""
	}

	parts := []string{}
	if state.ProcessID != "" {
		parts = append(parts, state.ProcessID)
	}
	if state.Status != "" {
		parts = append(parts, state.Status)
	}
	if state.ExitCode != nil {
		parts = append(parts, fmt.Sprintf("exit %d", *state.ExitCode))
	}
	if len(parts) > 0 {
		return strings.Join(parts, " ")
	}
	commandValue := str.String(state.Command)
	return commandValue.Trim()
}

func getProcessToolOutputDetail(state *trace.ProcessToolState) string {
	parts := []string{}
	if state.ProcessID != "" {
		parts = append(parts, state.ProcessID)
	}
	parts = append(
		parts,
		formatProcessBytes(state.StdoutBytes)+" stdout",
		formatProcessBytes(state.StderrBytes)+" stderr",
	)

	return strings.Join(parts, " ")
}

func cloneProcessToolDisplayState(state *trace.ProcessToolState) *trace.ProcessToolState {
	if state == nil {
		return nil
	}

	cloned := *state
	if state.ExitCode != nil {
		cloned.ExitCode = new(*state.ExitCode)
	}

	return &cloned
}

func mergeProcessToolDisplayState(current *trace.ProcessToolState, next *trace.ProcessToolState) *trace.ProcessToolState {
	if current == nil && next == nil {
		return nil
	}
	if current == nil {
		return cloneProcessToolDisplayState(next)
	}
	if next == nil {
		return cloneProcessToolDisplayState(current)
	}

	merged := *current
	if next.Operation != "" {
		merged.Operation = next.Operation
	}
	if next.ProcessID != "" {
		merged.ProcessID = next.ProcessID
	}
	if next.Command != "" {
		merged.Command = next.Command
	}
	if next.Status != "" {
		merged.Status = next.Status
	}
	if next.ExitCode != nil {
		merged.ExitCode = new(*next.ExitCode)
	} else if current.ExitCode != nil {
		merged.ExitCode = new(*current.ExitCode)
	}
	if next.StdoutBytes != 0 {
		merged.StdoutBytes = next.StdoutBytes
	}
	if next.StderrBytes != 0 {
		merged.StderrBytes = next.StderrBytes
	}
	if next.Count != 0 {
		merged.Count = next.Count
	}
	if next.ErrorCode != "" {
		merged.ErrorCode = next.ErrorCode
	}
	if next.Error != "" {
		merged.Error = next.Error
	}

	return &merged
}

func hasProcessToolError(state *trace.ProcessToolState) bool {
	if state == nil {
		return false
	}

	errorValue2 := str.String(state.Error)
	return errorValue2.Trim() != ""
}

func formatProcessBytes(value int) string {
	if value < 0 {
		value = 0
	}

	return fmt.Sprintf("%dB", value)
}

func formatProcessCount(value int) string {
	if value == 1 {
		return "1 process"
	}

	return fmt.Sprintf("%d processes", value)
}

func firstNonEmptyToolDisplay(values ...string) string {
	for _, value := range values {
		valueText := str.String(value)
		if valueText := valueText.Trim(); valueText != "" {
			return valueText
		}
	}

	return ""
}

func getPlanToolChanges(value any) []trace.PlanToolChange {
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return nil
	}

	changes := make([]trace.PlanToolChange, 0, len(items))
	for _, item := range items {
		fields, ok := item.(map[string]any)
		if !ok {
			continue
		}
		change := trace.PlanToolChange{
			Index:  getMapNumber(fields, "index"),
			ID:     getMapString(fields, "id"),
			Action: getMapString(fields, "action"),
			Fields: getMapStringSlice(fields, "fields"),
		}
		if change.Index == 0 && change.ID == "" && change.Action == "" {
			continue
		}
		changes = append(changes, change)
	}
	if len(changes) == 0 {
		return nil
	}

	return changes
}

func getPlanToolChangeBranchDetail(changes []trace.PlanToolChange) string {
	if len(changes) == 0 {
		return ""
	}

	if len(changes) > 2 {
		return getPlanToolChangeSummary(changes)
	}

	parts := make([]string, 0, len(changes))
	for _, change := range changes {
		part := getPlanToolChangeText(change)
		if part == "" {
			continue
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return ""
	}

	return strings.Join(parts, "; ")
}

func getPlanToolChangeSummary(changes []trace.PlanToolChange) string {
	type summaryKey struct {
		action string
		fields string
	}

	counts := map[summaryKey]int{}
	order := make([]string, 0, len(changes))
	for _, change := range changes {
		actionValue4 := str.String(change.Action)
		action := actionValue4.Normalized()
		if action == "" {
			continue
		}
		fields := ""
		if action == "updated" {
			fields = getPlanToolUpdatedFieldsLabel(change.Fields)
		}
		key := summaryKey{action: action, fields: fields}
		if _, ok := counts[key]; !ok {
			order = append(order, action+"\x00"+fields)
		}
		counts[key]++
	}
	if len(order) == 0 {
		return ""
	}

	parts := make([]string, 0, len(order))
	for _, raw := range order {
		action, fields, _ := strings.Cut(raw, "\x00")
		key := summaryKey{action: action, fields: fields}
		label := getPlanToolChangeSummaryLabel(action, fields, counts[key])
		if label != "" {
			parts = append(parts, label)
		}
	}
	if len(parts) == 0 {
		return ""
	}

	return strings.Join(parts, "; ")
}

func getPlanToolChangeSummaryLabel(action string, fields string, count int) string {
	if count <= 0 {
		return ""
	}

	switch action {
	case "added":
		return "Added " + formatTaskCount(count)
	case "completed":
		return "Completed " + formatTaskCount(count)
	case "cancelled":
		return "Cancelled " + formatTaskCount(count)
	case "removed":
		return "Removed " + formatTaskCount(count)
	case "updated":
		if fields != "" {
			return "Updated " + fields + " for " + formatTaskCount(count)
		}

		return ""
	default:
		return capitalizePlanToolChangeAction(action) + " " + formatTaskCount(count)
	}
}

func capitalizePlanToolChangeAction(action string) string {
	actionValue5 := str.String(action)
	action = actionValue5.Trim()
	if action == "" {
		return ""
	}

	return strings.ToUpper(action[:1]) + action[1:]
}

func getPlanToolChangeText(change trace.PlanToolChange) string {
	actionValue6 := str.String(change.Action)
	action := actionValue6.Normalized()
	if action == "" {
		return ""
	}

	subject := "Task"
	if change.Index > 0 {
		subject = fmt.Sprintf("Task %d", change.Index)
	}

	switch action {
	case "added":
		return subject + " added"
	case "completed":
		return subject + " completed"
	case "cancelled":
		return subject + " cancelled"
	case "removed":
		return subject + " removed"
	case "updated":
		if label := getPlanToolUpdatedFieldsLabel(change.Fields); label != "" {
			return subject + " " + label + " updated"
		}

		return ""
	default:
		return subject + " " + action
	}
}

func getPlanToolUpdatedFieldsLabel(fields []string) string {
	fields = normalizePlanToolChangedFields(fields)
	if len(fields) == 0 {
		return ""
	}

	if len(fields) == 1 {
		switch fields[0] {
		case "content":
			return "content"
		case "status":
			return "status"
		default:
			return fields[0]
		}
	}

	return strings.Join(fields, "+")
}

func normalizePlanToolChangedFields(fields []string) []string {
	if len(fields) == 0 {
		return nil
	}

	seen := map[string]struct{}{}
	result := make([]string, 0, len(fields))
	for _, field := range fields {
		fieldValue := str.String(field)
		field = fieldValue.Normalized()
		if field == "" {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		result = append(result, field)
	}
	if len(result) == 0 {
		return nil
	}

	return result
}

func formatTaskCount(count int) string {
	if count == 1 {
		return "1 task"
	}

	return fmt.Sprintf("%d tasks", count)
}

func isGenericToolParamDisplayEnabled(name string) bool {
	nameValue2 := str.String(name)
	switch nameValue2.Normalized() {
	case "list_files":
		return true
	default:
		return false
	}
}

func getGenericToolDisplayDetail(name string, fields map[string]any) string {
	nameValue3 := str.String(name)
	name = nameValue3.Trim()
	if name == "" || len(fields) == 0 {
		return ""
	}

	keys := make([]string, 0, len(fields))
	for key, value := range fields {
		keyValue := str.String(key)
		if keyValue.Trim() == "" || isEmptyToolInputValue(value) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		keyValue2 := str.String(key)
		parts = append(parts, keyValue2.Trim()+"="+formatToolInputValueForKey(key, fields[key]))
	}

	return name + "(" + strings.Join(parts, " ") + ")"
}

func getRunToolDisplayDetail(fields map[string]any) string {
	command := getMapString(fields, "command")
	if command == "" {
		return ""
	}

	args := getMapStringSlice(fields, "args")
	if len(args) == 0 {
		return appendToolTimeout(command, fields["timeout_seconds"])
	}

	parts := append([]string{command}, args...)
	for index, part := range parts {
		parts[index] = shellQuoteCommandPart(part)
	}

	return appendToolTimeout(strings.Join(parts, " "), fields["timeout_seconds"])
}

func getSearchToolDisplayDetail(fields map[string]any) string {
	query := getMapString(fields, "query", "q", "search_query")
	if query == "" {
		return ""
	}

	sanitized, _ := guardrails.NewRedactor().Sanitize(query).(string)
	sanitized = truncateToolDetail(sanitized, 80)
	if sanitized == "" {
		return ""
	}

	return `Search "` + strings.ReplaceAll(sanitized, `"`, `'`) + `"`
}

func getSearchFilesToolDisplayDetail(fields map[string]any) string {
	pattern := getMapString(fields, "pattern", "query", "q")
	if pattern == "" {
		return ""
	}

	sanitized, _ := guardrails.NewRedactor().Sanitize(pattern).(string)
	sanitized = truncateToolDetail(sanitized, 80)
	if sanitized == "" {
		return ""
	}

	detail := `Search "` + strings.ReplaceAll(sanitized, `"`, `'`) + `"`
	if path := getToolDisplayPath(fields); path != "" {
		detail += " in " + path
	}
	if maxResults := formatOptionalToolNumber(fields["max_results"]); maxResults != "" {
		detail += " max_results=" + maxResults
	}

	return detail
}

func getSessionMessagesToolDisplayDetail(fields map[string]any) string {
	keys := []string{
		"session_id",
		"message_ids",
		"anchor_message_id",
		"offset_start",
		"offset_end",
		"before",
		"after",
		"max_chars",
	}
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		if part := getSessionMessagesToolParam(key, fields[key]); part != "" {
			parts = append(parts, part)
		}
	}

	if len(parts) == 0 {
		return "session_messages()"
	}

	return "session_messages(" + strings.Join(parts, " ") + ")"
}

func getSessionMessagesToolBranchDetail(detail string, _ bool) string {
	detailValue2 := str.String(detail)
	detail = detailValue2.Trim()
	if detail == "" || normalizeToolDisplayName(detail) == "session_messages" ||
		normalizeToolDisplayName(detail) == "session_message" {
		return "session_messages()"
	}

	return detail
}

func getSessionMessagesToolParam(key string, value any) string {
	keyValue3 := str.String(key)
	key = keyValue3.Trim()
	if key == "" || isEmptyToolInputValue(value) {
		return ""
	}
	if key == "message_ids" {
		ids := formatToolInputValueForKey(key, value)
		if ids == "" {
			return ""
		}

		return key + "=" + ids
	}
	formatted := formatToolInputValueForKey(key, value)
	if formatted == "" || formatted == "0" {
		return ""
	}

	return key + "=" + formatted
}

func getPathToolDisplayDetail(name string, fields map[string]any) string {
	path := getToolDisplayPath(fields)
	if path == "" {
		return ""
	}
	nameValue4 := str.String(name)
	return nameValue4.Trim() + " " + path
}

func getPatchToolDisplayDetail(name string, fields map[string]any) string {
	patch := getMapString(fields, "patch", "diff", "unified_diff")
	path, added, removed := getPatchToolDisplaySummary(patch)
	if path == "" {
		path = getToolDisplayPath(fields)
	}
	nameValue5 := str.String(name)
	parts := []string{nameValue5.Trim()}
	if path != "" {
		parts = append(parts, path)
	}
	if added > 0 || removed > 0 {
		parts = append(parts, fmt.Sprintf("+%d -%d", added, removed))
	}

	return strings.Join(parts, " ")
}

func getToolDisplayPath(fields map[string]any) string {
	path := getMapString(fields, "path", "file", "filepath", "filename")
	if path == "" {
		return ""
	}

	sanitized, _ := guardrails.NewRedactor().Sanitize(path).(string)
	return shortenToolPath(sanitized, 42)
}

func getPatchToolDisplaySummary(patch string) (string, int, int) {
	var path string
	added := 0
	removed := 0

	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ "):
			trimPrefixValue := str.String(strings.TrimPrefix(line, "+++ "))
			candidate := normalizePatchToolPath(trimPrefixValue.Trim())
			if candidate != "" && candidate != "/dev/null" {
				path = candidate
			}
		case strings.HasPrefix(line, "--- "):
			if path == "" {
				trimPrefixValue2 := str.String(strings.TrimPrefix(line, "--- "))
				candidate := normalizePatchToolPath(trimPrefixValue2.Trim())
				if candidate != "" && candidate != "/dev/null" {
					path = candidate
				}
			}
		case strings.HasPrefix(line, "+"):
			added++
		case strings.HasPrefix(line, "-"):
			removed++
		}
	}

	if path != "" {
		sanitized, _ := guardrails.NewRedactor().Sanitize(path).(string)
		path = shortenToolPath(sanitized, 42)
	}

	return path, added, removed
}

func normalizePatchToolPath(path string) string {
	pathValue := str.String(path)
	path = pathValue.Trim()
	path = strings.TrimPrefix(path, "a/")
	path = strings.TrimPrefix(path, "b/")
	return strings.Trim(path, `"`)
}

func getMapString(fields map[string]any, keys ...string) string {
	for _, key := range keys {
		value, _ := fields[key].(string)
		value2 := str.String(value)
		if value = value2.Trim(); value != "" {
			return value
		}
	}

	return ""
}

func getMapStringSlice(fields map[string]any, key string) []string {
	raw, ok := fields[key].([]any)
	if !ok {
		return nil
	}

	values := make([]string, 0, len(raw))
	for _, value := range raw {
		text, ok := value.(string)
		if !ok {
			continue
		}
		textValue2 := str.String(text)
		if text = textValue2.Trim(); text != "" {
			values = append(values, text)
		}
	}

	return values
}

func getMapAnySlice(fields map[string]any, key string) []any {
	raw, ok := fields[key].([]any)
	if !ok {
		return nil
	}

	return raw
}

func getMapNumber(fields map[string]any, key string) int {
	value, ok := fields[key]
	if !ok {
		return 0
	}

	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	default:
		return 0
	}
}

func isEmptyToolInputValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		typedValue := str.String(typed)
		return typedValue.Trim() == ""
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	default:
		return false
	}
}

func formatOptionalToolNumber(value any) string {
	switch typed := value.(type) {
	case float64:
		if typed <= 0 {
			return ""
		}
	case float32:
		if typed <= 0 {
			return ""
		}
	case int:
		if typed <= 0 {
			return ""
		}
	case int64:
		if typed <= 0 {
			return ""
		}
	case int32:
		if typed <= 0 {
			return ""
		}
	}

	formatted := formatToolInputNumber(value)
	if formatted == "0" {
		return ""
	}

	return formatted
}

func formatToolInputNumber(value any) string {
	switch typed := value.(type) {
	case float64:
		return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", typed), "0"), ".")
	case float32:
		return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", typed), "0"), ".")
	case int:
		return fmt.Sprintf("%d", typed)
	case int64:
		return fmt.Sprintf("%d", typed)
	case int32:
		return fmt.Sprintf("%d", typed)
	case json.Number:
		textValue3 := str.String(typed.String())
		return textValue3.Trim()
	default:
		return ""
	}
}

func formatToolInputValueForKey(key string, value any) string {
	switch typed := value.(type) {
	case string:
		sanitized, _ := guardrails.NewRedactor().Sanitize(typed).(string)
		keyValue4 := str.String(key)
		if strings.EqualFold(keyValue4.Trim(), "path") {
			return shortenToolPath(sanitized, 42)
		}
		return truncateToolDetail(sanitized, 60)
	case float64:
		return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", typed), "0"), ".")
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return truncateToolDetail(fmt.Sprintf("%v", typed), 60)
		}
		return truncateToolDetail(string(data), 60)
	}
}

func shortenToolPath(path string, limit int) string {
	pathValue2 := str.String(path)
	path = strings.Join(strings.Fields(pathValue2.Trim()), " ")
	if limit <= 0 {
		return path
	}

	runes := []rune(path)
	if len(runes) <= limit {
		return path
	}
	if limit <= 5 {
		return string(runes[:limit])
	}

	separator := "/"
	if strings.Contains(path, "\\") && !strings.Contains(path, "/") {
		separator = "\\"
	}
	parts := strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	tail := ""
	if len(parts) > 0 {
		tail = parts[len(parts)-1]
	}
	if tail == "" {
		return truncateToolDetail(path, limit)
	}

	tailRunes := []rune(tail)
	if len(tailRunes)+5 >= limit {
		return "..." + separator + string(tailRunes[max(len(tailRunes)-(limit-4), 0):])
	}

	prefixLimit := limit - len(tailRunes) - 4
	prefix := string(runes[:max(prefixLimit, 1)])

	return strings.TrimRight(prefix, `/\`) + separator + "..." + separator + tail
}

func shellQuoteCommandPart(value string) string {
	if value == "" {
		return "''"
	}
	if strings.ContainsAny(value, " \t\n\"'\\$&|;()<>*?![]{}") {
		return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
	}

	return value
}

func appendToolTimeout(command string, raw any) string {
	timeout, ok := raw.(float64)
	if !ok || timeout <= 0 {
		return command
	}

	return command + " [timeout " + formatToolTimeoutSeconds(timeout) + "s]"
}

func formatToolTimeoutSeconds(timeout float64) string {
	return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", timeout), "0"), ".")
}

func normalizeRunToolDetailText(detail string, completed bool) string {
	detailValue3 := str.String(detail)
	detail = detailValue3.Trim()
	if detail == "" {
		return detail
	}
	if completed {
		detail = runToolTimeoutHintPattern.ReplaceAllString(detail, "")
		replaceAllStringValue := str.String(runToolLegacyTimeoutPattern.ReplaceAllString(detail, ""))
		return replaceAllStringValue.Trim()
	}
	if strings.Contains(detail, "[timeout ") || strings.Contains(detail, "[terminates in ") {
		return detail
	}

	return runToolLegacyTimeoutPattern.ReplaceAllString(detail, " [timeout $1]")
}

func truncateToolDetail(value string, limit int) string {
	value3 := str.String(value)
	value = strings.Join(strings.Fields(value3.Trim()), " ")
	if limit <= 0 {
		return value
	}

	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 3 {
		return string(runes[:limit])
	}

	return string(runes[:limit-3]) + "..."
}
