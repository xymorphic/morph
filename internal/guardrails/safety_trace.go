package guardrails

import "github.com/xymorphic/morph/pkg/str"

// SafetyTracePayloadOptions controls safety trace payload.
type SafetyTracePayloadOptions struct {
	SessionID     string
	Source        string
	Action        string
	ContentLength int
	Blocked       bool
	Redacted      bool
	Refusal       string
	Findings      []SafetyFinding
}

// SafetyTracePayload converts a safety finding into trace payload fields.
func SafetyTracePayload(opts SafetyTracePayloadOptions) map[string]any {
	actionValue := str.String(opts.Action)
	payload := map[string]any{
		"action":         actionValue.Trim(),
		"blocked":        opts.Blocked,
		"redacted":       opts.Redacted,
		"content_length": opts.ContentLength,
		"findings":       SafetyFindingLogFields(opts.Findings),
	}
	sessionIDValue := str.String(opts.SessionID)
	if sessionID := sessionIDValue.Trim(); sessionID != "" {
		payload["session_id"] = sessionID
	}
	sourceValue := str.String(opts.Source)
	if source := sourceValue.Trim(); source != "" {
		payload["source"] = source
	}
	refusalValue := str.String(opts.Refusal)
	if refusal := refusalValue.Trim(); refusal != "" {
		payload["refusal"] = refusal
	}

	return payload
}

// SafetyFindingLogFields converts a safety finding into structured log fields.
func SafetyFindingLogFields(findings []SafetyFinding) []map[string]string {
	if len(findings) == 0 {
		return nil
	}

	fields := make([]map[string]string, 0, len(findings))
	for _, finding := range findings {
		fields = append(fields, finding.LogFields())
	}

	return fields
}
