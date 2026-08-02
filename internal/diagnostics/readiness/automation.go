package readiness

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/xymorphic/morph/internal/automation"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/profile"
	storage "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/internal/state/storememory"
	"github.com/xymorphic/morph/internal/state/storesqlite"
	"github.com/xymorphic/morph/pkg/str"
)

var openAutomationReadinessStore = openProfileReadinessStore

func buildAutomationGroup(ctx context.Context, cfg *config.Config, activeProfile profile.Profile) Group {
	if cfg == nil {
		return Group{Name: "automation", Checks: []Check{check("config", StatusFail, "config is required")}}
	}

	storeCfg := *cfg
	storeCfg.Search.Vector.Enabled = false
	store, err := openAutomationReadinessStore(&storeCfg, activeProfile)
	if err != nil {
		return Group{Name: "automation", Checks: []Check{
			check("scheduler", StatusWarn, "scheduler cannot verify state store"),
			check("store", StatusFail, err.Error()),
		}}
	}
	if closer, ok := store.(interface{ Close() error }); ok {
		defer func() { _ = closer.Close() }()
	}
	automationStore, ok := store.Automation()
	if !ok || automationStore == nil {
		return Group{Name: "automation", Checks: []Check{
			check("scheduler", StatusWarn, "automation scheduler has no supported store"),
			check("store", StatusFail, "automation store is not supported"),
		}}
	}

	list, err := automationStore.ListJobs(ctx, automation.JobQuery{IncludeDisabled: true})
	if err != nil {
		return Group{Name: "automation", Checks: []Check{
			check("scheduler", StatusWarn, "scheduler cannot inspect jobs"),
			check("store", StatusFail, err.Error()),
		}}
	}

	findings := automation.DiagnoseJobs(list.Jobs, automation.DiagnosticOptions{})
	return Group{Name: "automation", Checks: buildAutomationChecks(list.Jobs, findings)}
}

func openProfileReadinessStore(
	cfg *config.Config,
	activeProfile profile.Profile,
) (storage.Store, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}

	backend := str.String(cfg.Storage.Backend).Normalized()
	switch backend {
	case "", "sqlite":
		activeProfile = profile.WithMetadataPaths(activeProfile)
		homeDir := str.String(activeProfile.HomeDir).Trim()
		if homeDir == "" {
			activeProfile = profile.WithMetadataPaths(profile.Active())
			homeDir = str.String(activeProfile.HomeDir).Trim()
		}
		if homeDir == "" {
			return nil, errors.New("automation profile home is required")
		}

		return storesqlite.NewStore(filepath.Join(homeDir, "data", "state.db"))
	case "memory":
		return storememory.NewStore(), nil
	default:
		return nil, errors.New("storage backend must be one of: memory, sqlite")
	}
}

func buildAutomationChecks(jobs []automation.Job, findings []automation.DiagnosticFinding) []Check {
	invalidScheduleMessage := "no invalid schedules found"
	stuckRunningMessage := "no stuck running jobs found"
	deliveryTargetsMessage := "delivery targets look valid"
	if len(jobs) == 0 {
		invalidScheduleMessage = "no automation jobs to check"
		stuckRunningMessage = "no automation jobs to check"
		deliveryTargetsMessage = "no automation jobs to check"
	}

	return []Check{
		check("scheduler", StatusPass, "automation scheduler state is inspectable"),
		check("store", StatusPass, fmt.Sprintf("%d automation jobs reachable", len(jobs))),
		buildAutomationFindingCheck("invalid schedules", findings, "invalid_schedule", invalidScheduleMessage),
		buildAutomationFindingCheck("stuck running", findings, "stuck_running", stuckRunningMessage),
		buildAutomationDeliveryCheck(findings, deliveryTargetsMessage),
	}
}

func buildAutomationDeliveryCheck(findings []automation.DiagnosticFinding, passMessage string) Check {
	deliveryCodes := map[string]struct{}{
		"delivery_webhook_url_missing": {},
		"delivery_target_incomplete":   {},
		"delivery_origin_missing":      {},
		"delivery_mode_unsupported":    {},
	}
	matches := make([]automation.DiagnosticFinding, 0)
	for _, finding := range findings {
		if _, ok := deliveryCodes[finding.Code]; ok {
			matches = append(matches, finding)
		}
	}
	if len(matches) == 0 {
		return check("delivery targets", StatusPass, passMessage)
	}

	return automationFindingsToCheck("delivery targets", matches)
}

func buildAutomationFindingCheck(
	name string,
	findings []automation.DiagnosticFinding,
	code string,
	passMessage string,
) Check {
	matches := make([]automation.DiagnosticFinding, 0)
	for _, finding := range findings {
		if finding.Code == code {
			matches = append(matches, finding)
		}
	}
	if len(matches) == 0 {
		return check(name, StatusPass, passMessage)
	}

	return automationFindingsToCheck(name, matches)
}

func automationFindingsToCheck(name string, findings []automation.DiagnosticFinding) Check {
	status := StatusWarn
	actions := make([]Action, 0, len(findings))
	for _, finding := range findings {
		if finding.Severity == automation.DiagnosticSeverityError {
			status = StatusFail
		}
		if finding.Action != "" {
			actions = append(actions, commandAction(finding.Action, finding.Message))
		}
	}

	message := findings[0].Message
	if len(findings) > 1 {
		message = fmt.Sprintf("%s and %d more", message, len(findings)-1)
	}

	return check(name, status, message, actions...)
}
