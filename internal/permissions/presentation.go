package permissions

import (
	"slices"
	"strconv"
	"strings"
	"time"
)

type ApprovalPrompt struct {
	Summary    string
	Effects    []string
	Reason     string
	Operations []string
	ExpiresAt  time.Time
}

type ApprovalChoice struct {
	Label    string
	Detail   string
	Key      rune
	Approved bool
	Scope    GrantScope
}

func GetApprovalPrompt(
	summary string,
	effects []string,
	reason string,
	operations []string,
	expiresAt time.Time,
) ApprovalPrompt {
	reason, legacyOperations := splitBatchApprovalReason(reason)
	if len(operations) == 0 {
		operations = legacyOperations
	}

	return ApprovalPrompt{
		Summary:    strings.TrimSpace(summary),
		Effects:    slices.Clone(effects),
		Reason:     reason,
		Operations: getNormalizedApprovalOperations(operations),
		ExpiresAt:  expiresAt,
	}
}

func GetApprovalChoices(effects []string) []ApprovalChoice {
	choices := []ApprovalChoice{
		{
			Label: "Allow once", Detail: "approve this request only", Key: 'y',
			Approved: true, Scope: GrantOnce,
		},
		{
			Label: "Allow for session", Detail: "remember this approval for this session", Key: 's',
			Approved: true, Scope: GrantSession,
		},
	}
	if isAlwaysApprovalChoiceAvailable(effects) {
		choices = append(choices, ApprovalChoice{
			Label: "Always allow", Detail: "remember this approval until revoked", Key: 'a',
			Approved: true, Scope: GrantAlways,
		})
	}

	return append(choices, ApprovalChoice{
		Label: "Deny", Detail: "deny this request only", Key: 'n',
	})
}

func GetApprovalChoiceByKey(choices []ApprovalChoice, key rune) (ApprovalChoice, bool) {
	for _, choice := range choices {
		if choice.Key == key {
			return choice, true
		}
	}

	return ApprovalChoice{}, false
}

func GetApprovalExpiryText(expiresAt time.Time, now time.Time) string {
	if expiresAt.IsZero() {
		return ""
	}
	if now.IsZero() {
		now = time.Now()
	}
	remaining := expiresAt.Sub(now)
	if remaining <= 0 {
		return "expired"
	}

	minutes := (remaining + time.Minute - time.Nanosecond) / time.Minute
	return strconv.FormatInt(int64(minutes), 10) + "m"
}

func getNormalizedApprovalOperations(operations []string) []string {
	result := make([]string, 0, len(operations))
	for _, operation := range operations {
		operation = strings.TrimSpace(operation)
		if operation != "" {
			result = append(result, operation)
		}
	}

	return result
}

func splitBatchApprovalReason(reason string) (string, []string) {
	const (
		batchPrefix = "Approve all "
		separator   = " operations: "
	)

	reason = strings.TrimSpace(reason)
	start := strings.LastIndex(reason, batchPrefix)
	if start < 0 {
		return reason, nil
	}
	batch := reason[start:]
	separatorIndex := strings.Index(batch, separator)
	if separatorIndex < len(batchPrefix) {
		return reason, nil
	}
	if _, err := strconv.Atoi(batch[len(batchPrefix):separatorIndex]); err != nil {
		return reason, nil
	}
	operationText := strings.TrimSpace(batch[separatorIndex+len(separator):])
	if operationText == "" {
		return reason, nil
	}

	return strings.TrimSpace(reason[:start]), strings.Split(operationText, "; ")
}

func isAlwaysApprovalChoiceAvailable(effects []string) bool {
	for _, effect := range effects {
		if !isAlwaysApprovalEffectAllowed(Effect(effect)) {
			return false
		}
	}

	return true
}
