package readiness

import (
	"context"
	"fmt"
	"time"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/internal/profile"
)

var openPermissionReadinessStore = openProfileReadinessStore

func buildPermissionGroup(ctx context.Context, cfg *config.Config, activeProfile profile.Profile) Group {
	if cfg == nil {
		return Group{Name: "permissions", Checks: []Check{check("config", StatusFail, "config is required")}}
	}

	checks := []Check{buildPermissionPolicyCheck(cfg.Permissions), buildPermissionSurfaceCheck(cfg.Permissions)}
	checks = append(checks, buildPermissionGrantCheck(ctx, cfg, activeProfile))

	return Group{Name: "permissions", Checks: checks}
}

func buildPermissionPolicyCheck(policy permissions.Policy) Check {
	if err := policy.Validate(); err != nil {
		return check("policy", StatusFail, err.Error())
	}
	if policy.EffectivePreset() == permissions.PresetFullAccess {
		return check(
			"policy",
			StatusWarn,
			"full access bypasses permission rules, approvals, command policy, and filesystem roots",
		)
	}

	return check("policy", StatusPass, "permission policy is valid")
}

func buildPermissionSurfaceCheck(policy permissions.Policy) Check {
	policy = policy.Effective()
	for kind, decision := range policy.SurfaceKindDefaults {
		if decision == permissions.DecisionAsk && kind != permissions.SurfaceKindLocal {
			return check(
				"unattended approvals",
				StatusFail,
				fmt.Sprintf("%s surfaces cannot wait for interactive approval", kind),
			)
		}
	}
	for surface, decision := range policy.SurfaceDefaults {
		if decision == permissions.DecisionAsk && surface != permissions.SurfaceCLI && surface != permissions.SurfaceTUI {
			return check(
				"unattended approvals",
				StatusFail,
				fmt.Sprintf("%s cannot wait for interactive approval", surface),
			)
		}
	}

	return check("unattended approvals", StatusPass, "unattended surfaces fail closed")
}

func buildPermissionGrantCheck(ctx context.Context, cfg *config.Config, activeProfile profile.Profile) Check {
	storeCfg := *cfg
	storeCfg.Search.Vector.Enabled = false
	store, err := openPermissionReadinessStore(&storeCfg, activeProfile)
	if err != nil {
		return check("grants", StatusWarn, "permission grants cannot be inspected")
	}
	if closer, ok := store.(interface{ Close() error }); ok {
		defer func() { _ = closer.Close() }()
	}
	permissionStore, ok := store.Permission()
	if !ok || permissionStore == nil {
		return check("grants", StatusFail, "permission store is not supported")
	}
	grants, err := permissionStore.ListApprovalGrants(ctx, permissions.GrantQuery{Status: permissions.GrantActive})
	if err != nil {
		return check("grants", StatusFail, err.Error())
	}
	now := time.Now().UTC()
	stale := 0
	for _, grant := range grants {
		if grant.IsExpiredAt(now) {
			stale++
		}
	}
	if stale > 0 {
		return check("grants", StatusWarn, fmt.Sprintf("%d active grants are stale", stale))
	}

	return check("grants", StatusPass, fmt.Sprintf("%d active grants are current", len(grants)))
}
