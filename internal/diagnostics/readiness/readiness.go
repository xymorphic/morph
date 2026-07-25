package readiness

import (
	"context"
	"fmt"
	"strings"

	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/diagnostics"
	"github.com/wandxy/morph/internal/profile"
	"github.com/wandxy/morph/pkg/str"
)

type Status = diagnostics.Status

const (
	StatusPass = diagnostics.StatusPass
	StatusWarn = diagnostics.StatusWarn
	StatusFail = diagnostics.StatusFail
)

type Action struct {
	Command     string
	Description string
}

type Check struct {
	Name    string
	Status  Status
	Message string
	Actions []Action
}

type Group struct {
	Name   string
	Checks []Check
}

type Report struct {
	Groups []Group
}

type Options struct {
	Config     *config.Config
	Profile    profile.Profile
	EnvPath    string
	ConfigPath string
}

func Build(ctx context.Context, opts Options) Report {
	cfg := opts.Config
	if cfg != nil {
		cfg.Normalize()
	}

	return Report{Groups: []Group{
		buildProfileGroup(opts.Profile, opts.EnvPath, opts.ConfigPath),
		buildRuntimeGroup(ctx, opts.Profile),
		buildRPCAuthGroup(cfg, opts.Profile),
		buildModelGroup(ctx, cfg),
		buildSessionGroup(cfg),
		buildMemoryGroup(cfg),
		buildSearchGroup(cfg),
		buildSafetyGroup(cfg),
		buildPermissionGroup(ctx, cfg, opts.Profile),
		buildGatewayGroup(cfg),
		buildAutomationGroup(ctx, cfg, opts.Profile),
		buildBrowserGroup(ctx, cfg),
		buildCapabilityGroup(cfg),
	}}
}

func (r Report) HasFailures() bool {
	for _, group := range r.Groups {
		for _, check := range group.Checks {
			if check.Status == StatusFail {
				return true
			}
		}
	}

	return false
}

func (r Report) Summary() string {
	parts := make([]string, 0)
	for _, group := range r.Groups {
		for _, check := range group.Checks {
			if check.Status == StatusFail {
				parts = append(parts, fmt.Sprintf("%s %s: %s", group.Name, check.Name, check.Message))
			}
		}
	}
	if len(parts) == 0 {
		return "readiness checks passed"
	}

	return strings.Join(parts, "; ")
}

func check(name string, status Status, message string, actions ...Action) Check {
	nameValue := str.String(name)
	messageValue := str.String(message)
	return Check{
		Name:    nameValue.Trim(),
		Status:  status,
		Message: messageValue.Trim(),
		Actions: append([]Action(nil), actions...),
	}
}

func commandAction(command string, description string) Action {
	commandValue := str.String(command)
	descriptionValue := str.String(description)
	return Action{
		Command:     commandValue.Trim(),
		Description: descriptionValue.Trim(),
	}
}
