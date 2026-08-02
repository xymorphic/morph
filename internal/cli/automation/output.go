package automation

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	coreautomation "github.com/xymorphic/morph/internal/automation"
)

func writeAutomationStatus(status coreautomation.Status) error {
	var output strings.Builder
	appendOutputSection(&output, "Automation scheduler")
	appendOutputField(&output, "Running", strconv.FormatBool(status.Running))
	appendOutputField(&output, "Jobs", strconv.Itoa(status.JobCount))
	appendOutputField(&output, "Running jobs", strconv.Itoa(status.RunningCount))
	appendOutputField(&output, "Started at", formatTime(status.StartedAt))
	appendOutputField(&output, "Next wake at", formatTime(status.NextWakeAt))
	return writeAutomationOutput(output.String())
}

func writeJobList(jobs []coreautomation.Job) error {
	return writeAutomationOutput(jobListToText(jobs))
}

func jobListToText(jobs []coreautomation.Job) string {
	if len(jobs) == 0 {
		return "No automation jobs found.\n"
	}

	var output strings.Builder
	table := tabwriter.NewWriter(&output, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(table, "ID\tNAME\tENABLED\tSCHEDULE\tNEXT RUN\tLAST STATUS")
	for _, job := range jobs {
		_, _ = fmt.Fprintf(
			table,
			"%s\t%s\t%t\t%s\t%s\t%s\n",
			job.ID,
			getDisplayText(job.Name),
			job.Enabled,
			getDisplayText(formatSchedule(job.Schedule)),
			formatTimeInScheduleTimezone(job.State.NextRunAt, job.Schedule),
			getDisplayText(string(job.State.LastStatus)),
		)
	}
	_ = table.Flush()
	return output.String()
}

func writeRunList(runs []coreautomation.Run) error {
	return writeAutomationOutput(runListToText(runs))
}

func runListToText(runs []coreautomation.Run) string {
	if len(runs) == 0 {
		return "No automation runs found.\n"
	}

	var output strings.Builder
	table := tabwriter.NewWriter(&output, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(table, "RUN ID\tJOB ID\tSTATUS\tSTARTED\tDURATION\tDELIVERY")
	for _, run := range runs {
		_, _ = fmt.Fprintf(
			table,
			"%s\t%s\t%s\t%s\t%s\t%s\n",
			run.ID,
			run.JobID,
			getDisplayText(string(run.Status)),
			formatTime(run.StartedAt),
			run.Duration,
			getDisplayText(string(run.DeliveryStatus)),
		)
	}
	_ = table.Flush()
	return output.String()
}

func writeRunSummary(run coreautomation.Run) error {
	var output strings.Builder
	appendRunDetails(
		&output,
		"Run",
		run,
		run.SessionID,
		run.DeliveryStatus,
		run.DeliveryError,
	)
	return writeAutomationOutput(output.String())
}

func writeDiagnosticFindings(findings []coreautomation.DiagnosticFinding) error {
	return writeAutomationOutput(diagnosticFindingsToText(findings))
}

func diagnosticFindingsToText(findings []coreautomation.DiagnosticFinding) string {
	if len(findings) == 0 {
		return "Automation diagnostics\n  Status:               passed\n"
	}

	var output strings.Builder
	output.WriteString("Automation diagnostics\n")
	for index, finding := range findings {
		if index > 0 {
			output.WriteByte('\n')
		}
		fmt.Fprintf(&output, "[%s] %s\n", strings.ToUpper(string(finding.Severity)), finding.Code)
		appendOutputField(&output, "Job ID", finding.JobID)
		appendOutputField(&output, "Message", finding.Message)
		appendOutputField(&output, "Action", finding.Action)
	}

	return output.String()
}

func writeInspection(inspection coreautomation.RunInspection) error {
	return writeAutomationOutput(inspectionToText(inspection))
}

func inspectionToText(inspection coreautomation.RunInspection) string {
	var output strings.Builder
	appendInspectedJob(&output, inspection.Job)
	appendInspectedRun(&output, inspection)
	appendRecentFailures(&output, inspection.RecentFailures)
	return output.String()
}

func appendInspectedJob(output *strings.Builder, job coreautomation.Job) {
	appendOutputSection(output, "Job")
	appendOutputField(output, "ID", job.ID)
	appendOutputField(output, "Name", job.Name)
	appendOutputField(output, "Description", job.Description)
	appendOutputField(output, "Enabled", strconv.FormatBool(job.Enabled))
	appendOutputField(output, "Created at", formatTime(job.CreatedAt))
	appendOutputField(output, "Updated at", formatTime(job.UpdatedAt))
	appendOutputField(output, "Profile", job.Profile)
	appendOutputField(output, "Session target", getSessionTargetDisplay(job.SessionTarget))
	appendOutputField(output, "Delete after run", strconv.FormatBool(job.DeleteAfterRun))

	appendOutputSection(output, "Schedule")
	appendOutputField(output, "Kind", string(job.Schedule.Kind))
	appendOutputField(output, "At", formatTime(job.Schedule.At))
	appendOutputField(output, "Every", job.Schedule.Every.String())
	appendOutputField(output, "Cron", job.Schedule.Cron)
	appendOutputField(output, "Timezone", job.Schedule.Timezone)

	appendOutputSection(output, "Payload")
	appendOutputField(output, "Kind", string(job.Payload.Kind))
	appendOutputField(output, "Prompt", job.Payload.Prompt)
	appendOutputField(output, "System event", job.Payload.SystemEvent)
	appendOutputField(output, "Model", job.Payload.Model)
	appendOutputField(output, "Provider", job.Payload.Provider)
	appendOutputField(output, "Base URL", job.Payload.BaseURL)
	appendOutputField(output, "No timeout", strconv.FormatBool(job.Payload.NoTimeout))
	appendOutputField(output, "Max runtime", job.Payload.MaxRuntime.String())
	appendOutputField(output, "Max iterations", strconv.Itoa(job.Payload.MaxIterations))
	appendOutputField(output, "Retry attempts", strconv.Itoa(job.Payload.RetryAttempts))
	appendOutputField(output, "Retry backoff", job.Payload.RetryBackoff.String())
	appendOutputField(output, "Retry max delay", job.Payload.RetryMaxDelay.String())
	appendOutputField(output, "Tool groups", getStringListDisplay(job.Payload.ToolGroups))
	appendOutputField(output, "Metadata", getMetadataDisplay(job.Payload.Metadata))

	appendOutputSection(output, "Delivery")
	appendOutputField(output, "Mode", getDeliveryModeDisplay(job.Delivery.Mode))
	appendOutputField(output, "Channel", job.Delivery.Channel)
	appendOutputField(output, "Target", job.Delivery.Target)
	appendOutputField(output, "Thread ID", job.Delivery.ThreadID)
	appendOutputField(output, "Webhook URL", job.Delivery.WebhookURL)
	appendOutputField(output, "Best effort", strconv.FormatBool(job.Delivery.BestEffort))
	appendOutputField(output, "Failure target", job.Delivery.FailureTarget)
	appendOutputField(output, "Failure after", strconv.Itoa(job.Delivery.FailureAfter))
	appendOutputField(output, "Failure cooldown", job.Delivery.FailureCooldown.String())

	appendOutputSection(output, "State")
	appendOutputField(output, "Next run at", formatTimeInScheduleTimezone(job.State.NextRunAt, job.Schedule))
	appendOutputField(output, "Running at", formatTime(job.State.RunningAt))
	appendOutputField(output, "Last run at", formatTime(job.State.LastRunAt))
	appendOutputField(output, "Last status", string(job.State.LastStatus))
	appendOutputField(output, "Last error", job.State.LastError)
	appendOutputField(output, "Last duration", job.State.LastDuration.String())
	appendOutputField(output, "Consecutive errors", strconv.Itoa(job.State.ConsecutiveErrors))
	appendOutputField(output, "Last failure notice", formatTime(job.State.LastFailureNoticeAt))
}

func appendInspectedRun(output *strings.Builder, inspection coreautomation.RunInspection) {
	if inspection.LastRun.ID == "" {
		appendOutputSection(output, "Last run")
		appendOutputField(output, "Status", "none")
		return
	}

	appendRunDetails(
		output,
		"Last run",
		inspection.LastRun,
		inspection.SessionID,
		inspection.DeliveryStatus,
		inspection.DeliveryError,
	)
}

func appendRunDetails(
	output *strings.Builder,
	title string,
	run coreautomation.Run,
	sessionID string,
	deliveryStatus coreautomation.DeliveryStatus,
	deliveryError string,
) {
	appendOutputSection(output, title)
	appendOutputField(output, "ID", run.ID)
	appendOutputField(output, "Job ID", run.JobID)
	appendOutputField(output, "Status", string(run.Status))
	appendOutputField(output, "Started at", formatTime(run.StartedAt))
	appendOutputField(output, "Ended at", formatTime(run.EndedAt))
	appendOutputField(output, "Duration", run.Duration.String())
	appendOutputField(output, "Output", run.Output)
	appendOutputField(output, "Error", run.Error)
	appendOutputField(output, "Session ID", sessionID)
	appendOutputField(output, "Trace session", sessionID)
	appendOutputField(output, "Delivery status", string(deliveryStatus))
	appendOutputField(output, "Delivery error", deliveryError)
	appendOutputField(output, "Model", run.Model)
	appendOutputField(output, "Provider", run.Provider)
	appendOutputField(output, "Input tokens", strconv.Itoa(run.Usage.InputTokens))
	appendOutputField(output, "Output tokens", strconv.Itoa(run.Usage.OutputTokens))
	appendOutputField(output, "Total tokens", strconv.Itoa(run.Usage.TotalTokens))
	appendOutputField(output, "Cache read tokens", strconv.Itoa(run.Usage.CacheReadTokens))
	appendOutputField(output, "Cache write tokens", strconv.Itoa(run.Usage.CacheWriteTokens))
}

func appendRecentFailures(output *strings.Builder, runs []coreautomation.Run) {
	appendOutputSection(output, "Recent failures")
	if len(runs) == 0 {
		appendOutputField(output, "Status", "none")
		return
	}

	for _, run := range runs {
		appendOutputField(output, "Run ID", run.ID)
		appendOutputField(output, "Started at", formatTime(run.StartedAt))
		appendOutputField(output, "Error", run.Error)
	}
}

func appendOutputSection(output *strings.Builder, title string) {
	if output.Len() > 0 {
		output.WriteByte('\n')
	}
	output.WriteString(title)
	output.WriteByte('\n')
}

func appendOutputField(output *strings.Builder, label string, value string) {
	fmt.Fprintf(output, "  %-21s %s\n", label+":", getDisplayText(value))
}

func writeAutomationOutput(value string) error {
	_, err := fmt.Fprint(automationOutput, value)
	return err
}

func getDisplayText(value string) string {
	if value == "" {
		return "-"
	}
	if strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\t") {
		return strconv.Quote(value)
	}

	return value
}

func getSessionTargetDisplay(target string) string {
	if target == "" {
		return "isolated (default)"
	}

	return target
}

func getDeliveryModeDisplay(mode coreautomation.DeliveryMode) string {
	if mode == "" {
		return "none (default)"
	}

	return string(mode)
}

func getStringListDisplay(values []string) string {
	if len(values) == 0 {
		return "-"
	}

	displayValues := make([]string, 0, len(values))
	for _, value := range values {
		displayValues = append(displayValues, getDisplayText(value))
	}

	return strings.Join(displayValues, ", ")
}

func getMetadataDisplay(metadata map[string]string) string {
	if len(metadata) == 0 {
		return "-"
	}

	keys := slices.Sorted(maps.Keys(metadata))
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, getDisplayText(key)+"="+getDisplayText(metadata[key]))
	}

	return strings.Join(values, ", ")
}
