package types

import envplanstore "github.com/xymorphic/morph/internal/environment/planstore"

const (
	PlanStatusPending    = envplanstore.PlanStatusPending
	PlanStatusInProgress = envplanstore.PlanStatusInProgress
	PlanStatusCompleted  = envplanstore.PlanStatusCompleted
	PlanStatusCancelled  = envplanstore.PlanStatusCancelled
)

// PlanStep aliases envplanstore.PlanStep at this package boundary.
type PlanStep = envplanstore.PlanStep

// PartialPlanStep aliases envplanstore.PartialPlanStep at this package boundary.
type PartialPlanStep = envplanstore.PartialPlanStep

// Plan aliases envplanstore.Plan at this package boundary.
type Plan = envplanstore.Plan

// PlanSummary aliases envplanstore.PlanSummary at this package boundary.
type PlanSummary = envplanstore.PlanSummary

// ValidatePlan checks that a plan has usable steps and statuses.
func ValidatePlan(plan Plan) error {
	return envplanstore.ValidatePlan(plan)
}

// ValidPlanStatus reports whether status is accepted by the plan store.
func ValidPlanStatus(status string) bool {
	return envplanstore.ValidPlanStatus(status)
}
