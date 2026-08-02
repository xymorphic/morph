package diagnostics

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/pkg/str"
)

var (
	osStat                  = os.Stat
	resolveSummaryModelAuth = func(cfg *config.Config) (config.ModelAuth, error) {
		return cfg.ResolveSummaryModelAuth()
	}
)

// Status identifies the result of one diagnostics check.
type Status string

const (
	StatusPass Status = "pass"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

// Check describes one diagnostics probe and its result.
type Check struct {
	Name    string
	Status  Status
	Message string
}

// Report contains the full diagnostics output.
type Report struct {
	Checks []Check
}

type BuildOptions struct {
	Validate          func(*config.Config) error
	CheckModelAuth    bool
	ValidationPass    string
	ModelAuthWarnOnly bool
}

// Build runs diagnostics checks and returns a report.
func Build(envPath, configPath string, cfg *config.Config, loadErr error) Report {
	return BuildWithOptions(envPath, configPath, cfg, loadErr, BuildOptions{CheckModelAuth: true})
}

func BuildWithOptions(envPath, configPath string, cfg *config.Config, loadErr error, opts BuildOptions) Report {
	report := Report{
		Checks: []Check{
			buildFileCheck("env file", envPath, true),
			buildFileCheck("config file", configPath, false),
		},
	}

	if loadErr != nil {
		report.Checks = append(report.Checks, Check{
			Name:    "config load",
			Status:  StatusFail,
			Message: loadErr.Error(),
		})
		return report
	}

	if cfg == nil {
		report.Checks = append(report.Checks, Check{
			Name:    "config load",
			Status:  StatusFail,
			Message: "config is required",
		})
		return report
	}

	validate := opts.Validate
	if validate == nil {
		validate = (*config.Config).Validate
	}
	validationPassValue := str.String(opts.ValidationPass)
	validationPass := validationPassValue.Trim()
	if validationPass == "" {
		validationPass = "configuration is valid"
	}
	if err := validate(cfg); err != nil {
		report.Checks = append(report.Checks, Check{
			Name:    "config validation",
			Status:  StatusFail,
			Message: err.Error(),
		})
	} else {
		report.Checks = append(report.Checks, Check{
			Name:    "config validation",
			Status:  StatusPass,
			Message: validationPass,
		})
	}

	if !opts.CheckModelAuth {
		return report
	}

	auth, err := cfg.ResolveModelAuth()
	if err != nil {
		status := StatusFail
		if opts.ModelAuthWarnOnly {
			status = StatusWarn
		}
		report.Checks = append(report.Checks, Check{
			Name:    "model auth",
			Status:  status,
			Message: err.Error(),
		})
	} else {
		report.Checks = append(report.Checks, Check{
			Name:    "model auth",
			Status:  StatusPass,
			Message: fmt.Sprintf("resolved auth for provider %q", auth.Provider),
		})
		report.Checks = append(report.Checks, buildBaseURLCheck("model base URL", auth.BaseURL))

		summaryAuth, sumErr := resolveSummaryModelAuth(cfg)
		if sumErr != nil {
			report.Checks = append(report.Checks, Check{
				Name:    "summary model auth",
				Status:  StatusFail,
				Message: sumErr.Error(),
			})
		} else if !config.ModelAuthEqual(auth, summaryAuth) {
			report.Checks = append(report.Checks, Check{
				Name:    "summary model auth",
				Status:  StatusPass,
				Message: fmt.Sprintf("resolved summary auth for provider %q", summaryAuth.Provider),
			})
			report.Checks = append(report.Checks, buildBaseURLCheck("summary model base URL", summaryAuth.BaseURL))
		}
	}

	return report
}

func (r Report) HasFailures() bool {
	for _, check := range r.Checks {
		if check.Status == StatusFail {
			return true
		}
	}

	return false
}

func (r Report) Summary() string {
	parts := make([]string, 0, len(r.Checks))
	for _, check := range r.Checks {
		if check.Status == StatusFail {
			parts = append(parts, fmt.Sprintf("%s: %s", check.Name, check.Message))
		}
	}

	if len(parts) == 0 {
		return "startup diagnostics passed"
	}

	return strings.Join(parts, "; ")
}

func (r Report) FirstFailure() string {
	for _, check := range r.Checks {
		if check.Status == StatusFail {
			return check.Message
		}
	}

	return ""
}

func buildFileCheck(name, path string, optional bool) Check {
	pathValue := str.String(path)
	trimmed := pathValue.Trim()
	if trimmed == "" {
		return Check{
			Name:    name,
			Status:  StatusWarn,
			Message: "not set",
		}
	}

	info, err := osStat(trimmed)
	if err == nil {
		if info.IsDir() {
			return Check{
				Name:    name,
				Status:  StatusFail,
				Message: fmt.Sprintf("%q is a directory", trimmed),
			}
		}

		return Check{
			Name:    name,
			Status:  StatusPass,
			Message: fmt.Sprintf("found %q", trimmed),
		}
	}

	if os.IsNotExist(err) && optional {
		return Check{
			Name:    name,
			Status:  StatusWarn,
			Message: fmt.Sprintf("%q not found; continuing without it", trimmed),
		}
	}

	if os.IsNotExist(err) {
		return Check{
			Name:    name,
			Status:  StatusWarn,
			Message: fmt.Sprintf("%q not found; continuing without file values", trimmed),
		}
	}

	return Check{
		Name:    name,
		Status:  StatusFail,
		Message: err.Error(),
	}
}

func buildBaseURLCheck(name, raw string) Check {
	rawValue := str.String(raw)
	trimmed := rawValue.Trim()
	if trimmed == "" {
		return Check{
			Name:    name,
			Status:  StatusPass,
			Message: "using provider default base URL",
		}
	}

	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return Check{
			Name:    name,
			Status:  StatusFail,
			Message: fmt.Sprintf("%q is not a valid absolute URL", trimmed),
		}
	}

	return Check{
		Name:    name,
		Status:  StatusPass,
		Message: fmt.Sprintf("using %q", trimmed),
	}
}
