package guardrails

import (
	"encoding/json"
	"reflect"
	"regexp"

	"github.com/xymorphic/morph/pkg/str"
)

// SafetyCategory classifies safety findings by policy area.
type SafetyCategory string

const (
	SafetyCategoryPromptInjection         SafetyCategory = "prompt_injection"
	SafetyCategoryPromptExfiltration      SafetyCategory = "prompt_exfiltration"
	SafetyCategoryInstructionManipulation SafetyCategory = "instruction_manipulation"
	SafetyCategorySecretExfiltration      SafetyCategory = "secret_exfiltration"
	SafetyCategoryHiddenInstruction       SafetyCategory = "hidden_or_obfuscated_instruction"
	SafetyCategorySuspiciousToolCoercion  SafetyCategory = "suspicious_tool_use_coercion"
)

// SafetyFindingID identifies a specific safety finding.
type SafetyFindingID string

const (
	SafetyFindingPromptInjection      SafetyFindingID = "prompt_injection"
	SafetyFindingPromptExfiltration   SafetyFindingID = "prompt_exfiltration"
	SafetyFindingDeceptionHide        SafetyFindingID = "deception_hide"
	SafetyFindingSystemPromptOverride SafetyFindingID = "sys_prompt_override"
	SafetyFindingDisregardRules       SafetyFindingID = "disregard_rules"
	SafetyFindingBypassRestrictions   SafetyFindingID = "bypass_restrictions"
	SafetyFindingHTMLCommentInjection SafetyFindingID = "html_comment_injection"
	SafetyFindingHiddenDiv            SafetyFindingID = "hidden_div"
	SafetyFindingTranslateExecute     SafetyFindingID = "translate_execute"
	SafetyFindingCurlSecretExfil      SafetyFindingID = "exfil_curl"
	SafetyFindingReadSecrets          SafetyFindingID = "read_secrets"
	SafetyFindingInvisibleUnicode     SafetyFindingID = "invisible_unicode"
	SafetyFindingOutputPromptLeak     SafetyFindingID = "output_prompt_leak"
	SafetyFindingToolSchemaLeak       SafetyFindingID = "tool_schema_leak"
	SafetyFindingInstructionNameLeak  SafetyFindingID = "instruction_name_leak"
)

const defaultSafetyRefusal = "I can't help reveal, override, or manipulate hidden instructions, secrets, or safety controls. I can still explain the public behavior at a high level."

var outputLeakPatterns = []threatPattern{
	{
		re: regexp.MustCompile(
			`(?im)^\s{0,3}#{1,6}\s+(?:Base Instructions|Environment Context|Memory Context|Planning Policy|Pre-Context-Loss Memory Flush|Memory (?:[A-Za-z]+(?: [A-Za-z]+){0,2}) Guidance|Tool Guidance)\b`,
		),
		id:       SafetyFindingOutputPromptLeak,
		category: SafetyCategoryHiddenInstruction,
	},
	{
		re:       regexp.MustCompile(`(?is)"tools"\s*:\s*\[.*"(?:input_schema|parameters)"`),
		id:       SafetyFindingToolSchemaLeak,
		category: SafetyCategoryHiddenInstruction,
	},
	{
		re: regexp.MustCompile(
			`(?i)\b(?:planning\.policy|environment\.context|memory\.context|tool\.(?:session_search|session_messages|memory_extract|memory_add|memory_update|memory_delete))\b`,
		),
		id:       SafetyFindingInstructionNameLeak,
		category: SafetyCategoryHiddenInstruction,
	},
}

// SafetyFinding describes safety finding.
type SafetyFinding struct {
	ID       SafetyFindingID
	Category SafetyCategory
	Message  string
	Source   string
}

// InputSafetyResult contains findings from input safety checks.
type InputSafetyResult struct {
	Allowed        bool
	Blocked        bool
	RefusalMessage string
	Findings       []SafetyFinding
}

// OutputSafetyResult contains findings from output safety checks.
type OutputSafetyResult struct {
	Content        string
	Blocked        bool
	Redacted       bool
	RefusalMessage string
	Findings       []SafetyFinding
}

// UntrustedContentSafetyResult contains findings from untrusted-content checks.
type UntrustedContentSafetyResult struct {
	Content  string
	Blocked  bool
	Redacted bool
	Findings []SafetyFinding
}

// CheckInputSafety checks input safety.
func CheckInputSafety(content, source string) InputSafetyResult {
	findings := findSafetyFindings(content, source)
	if len(findings) == 0 {
		return InputSafetyResult{Allowed: true}
	}

	return InputSafetyResult{
		Blocked:        true,
		RefusalMessage: defaultSafetyRefusal,
		Findings:       findings,
	}
}

// CheckOutputSafety checks output safety.
func CheckOutputSafety(content, source string, redactor Redactor) OutputSafetyResult {
	if redactor == nil {
		redactor = NewRedactor()
	}

	redactedContent, _ := redactor.Sanitize(content).(string)
	if redactedContent == "" && content != "" {
		redactedContent = content
	}

	findings := findSafetyFindings(redactedContent, source)
	findings = appendOutputLeakFindings(findings, redactedContent, source)
	redacted := isRedactedOutput(content, redactedContent)
	if len(findings) == 0 {
		return OutputSafetyResult{
			Content:  redactedContent,
			Redacted: redacted,
		}
	}

	return OutputSafetyResult{
		Content:        defaultSafetyRefusal,
		Blocked:        true,
		Redacted:       redacted,
		RefusalMessage: defaultSafetyRefusal,
		Findings:       findings,
	}
}

// CheckUntrustedContentSafety checks untrusted content safety.
func CheckUntrustedContentSafety(content, source string, redactor Redactor) UntrustedContentSafetyResult {
	if redactor == nil {
		redactor = NewRedactor()
	}

	redactedContent, _ := redactor.Sanitize(content).(string)
	if redactedContent == "" && content != "" {
		redactedContent = content
	}

	scanned := SafetyScan(redactedContent, source)
	return UntrustedContentSafetyResult{
		Content:  scanned.Content,
		Blocked:  scanned.Blocked,
		Redacted: isRedactedOutput(content, redactedContent),
		Findings: scanned.Findings,
	}
}

func isRedactedOutput(original, sanitized string) bool {
	if original == sanitized {
		return false
	}

	var originalJSON any
	var sanitizedJSON any
	if json.Unmarshal([]byte(original), &originalJSON) == nil &&
		json.Unmarshal([]byte(sanitized), &sanitizedJSON) == nil {
		return !reflect.DeepEqual(originalJSON, sanitizedJSON)
	}

	return true
}

func appendOutputLeakFindings(findings []SafetyFinding, content, source string) []SafetyFinding {
	for _, pattern := range outputLeakPatterns {
		if pattern.re.MatchString(content) {
			findings = appendSafetyFinding(findings, SafetyFinding{
				ID:       pattern.id,
				Category: pattern.category,
				Source:   source,
			})
		}
	}

	return findings
}

func (finding SafetyFinding) LogFields() map[string]string {
	fields := map[string]string{
		"id":       string(finding.ID),
		"category": string(finding.Category),
	}
	sourceValue := str.String(finding.Source)
	if source := sourceValue.Trim(); source != "" {
		fields["source"] = source
	}

	return fields
}

func getSafetyFindingIDs(findings []SafetyFinding) []string {
	ids := make([]string, 0, len(findings))
	for _, finding := range findings {
		ids = append(ids, string(finding.ID))
	}

	return ids
}
