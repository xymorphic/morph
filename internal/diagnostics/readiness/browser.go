package readiness

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xymorphic/morph/internal/browser"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/permissions"
)

var discoverBrowserExecutable = browser.DiscoverChromiumExecutable

var dialBrowserEndpoint = func(ctx context.Context, network, address string) error {
	dialer := net.Dialer{Timeout: 2 * time.Second}
	connection, err := dialer.DialContext(ctx, network, address)
	if err != nil {
		return err
	}
	return connection.Close()
}

func buildBrowserGroup(ctx context.Context, cfg *config.Config) Group {
	if cfg == nil {
		return Group{Name: "browser", Checks: []Check{check("config", StatusFail, "config is required")}}
	}
	checks := []Check{
		buildBrowserEnablementCheck(cfg),
		buildBrowserExecutableCheck(cfg),
		buildBrowserProfileCheck(ctx, cfg),
		buildBrowserSecurityCheck(cfg),
		buildBrowserStorageCheck(cfg),
	}

	return Group{Name: "browser", Checks: checks}
}

func buildBrowserEnablementCheck(cfg *config.Config) Check {
	capability := cfg.Cap.Browser != nil && *cfg.Cap.Browser
	if !cfg.Browser.Enabled && !capability {
		return check("status", StatusPass, "browser service and capability are disabled")
	}
	if cfg.Browser.Enabled != capability {
		return check(
			"status", StatusWarn,
			fmt.Sprintf("service enabled=%t, capability enabled=%t; both must be enabled for agent use", cfg.Browser.Enabled, capability),
		)
	}

	return check("status", StatusPass, "browser service and capability are enabled")
}

func buildBrowserExecutableCheck(cfg *config.Config) Check {
	if !cfg.Browser.Enabled || !hasManagedBrowserProfile(cfg.Browser.Profiles) {
		return check("executable", StatusPass, "local Chromium is not required")
	}
	executable, err := discoverBrowserExecutable(cfg.Browser.Executable)
	if err != nil {
		return check(
			"executable", StatusFail, err.Error(),
			commandAction("morph config set browser.executable <path>", "configure a Chromium executable"),
		)
	}

	return check("executable", StatusPass, "Chromium is available at "+executable)
}

func buildBrowserProfileCheck(ctx context.Context, cfg *config.Config) Check {
	if len(cfg.Browser.Profiles) == 0 {
		return check("profiles", StatusFail, "no browser profiles are configured")
	}
	if !cfg.Browser.Enabled {
		return check(
			"profiles", StatusPass,
			fmt.Sprintf("%d profiles configured; endpoint readiness is skipped while browser is disabled", len(cfg.Browser.Profiles)),
		)
	}
	remote := 0
	for _, profile := range cfg.Browser.Profiles {
		if profile.Mode != config.BrowserProfileRemoteCDP && profile.Mode != config.BrowserProfileExistingSession {
			continue
		}
		remote++
		if err := checkBrowserEndpoint(ctx, cfg.Browser.Network, profile.CDPEndpoint); err != nil {
			return check("profiles", StatusWarn, fmt.Sprintf("profile %q endpoint is not ready: %v", profile.Name, err))
		}
	}

	return check(
		"profiles", StatusPass,
		fmt.Sprintf("%d profiles configured, default=%q, remote=%d", len(cfg.Browser.Profiles), cfg.Browser.DefaultProfile, remote),
	)
}

func buildBrowserSecurityCheck(cfg *config.Config) Check {
	if cfg.Permissions.EffectivePreset() == permissions.PresetFullAccess {
		return check("security", StatusWarn, "full access bypasses browser network guardrails")
	}
	if !cfg.Browser.Network.StrictEnabled() {
		return check("security", StatusWarn, "strict browser network policy is disabled")
	}
	exceptions := len(cfg.Browser.Network.DevelopmentAllowedHosts) + len(cfg.Browser.Network.DevelopmentAllowedCIDRs)
	if exceptions > 0 {
		return check("security", StatusWarn, fmt.Sprintf("strict network policy has %d development exceptions", exceptions))
	}

	return check("security", StatusPass, "strict network policy is enabled without development exceptions")
}

func buildBrowserStorageCheck(cfg *config.Config) Check {
	roots := []string{cfg.Browser.ProfileRoot, cfg.Browser.TemporaryRoot, cfg.Browser.Artifacts.Root}
	stale := 0
	for _, root := range roots {
		if !filepath.IsAbs(root) {
			return check("storage", StatusFail, "browser managed roots must be absolute")
		}
		info, err := os.Stat(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return check("storage", StatusWarn, "browser managed root cannot be inspected")
		}
		if !info.IsDir() {
			return check("storage", StatusFail, "browser managed root is not a directory")
		}
	}
	stale += countStaleBrowserDirectories(cfg.Browser.TemporaryRoot, "browser-profile-", cfg.Browser.Artifacts.Retention)
	stale += countStaleBrowserDirectories(
		filepath.Join(cfg.Browser.TemporaryRoot, "uploads"), "browser_", cfg.Browser.Artifacts.Retention,
	)
	stale += countStaleBrowserDirectories(
		filepath.Join(cfg.Browser.Artifacts.Root, ".downloads"), "browser_", cfg.Browser.Artifacts.Retention,
	)
	if stale > 0 {
		return check("storage", StatusWarn, fmt.Sprintf("%d stale browser runtime directories need cleanup", stale))
	}

	return check("storage", StatusPass, "browser managed roots have no stale runtime state")
}

func hasManagedBrowserProfile(profiles []config.BrowserProfileConfig) bool {
	for _, profile := range profiles {
		if profile.Mode == config.BrowserProfileManagedEphemeral || profile.Mode == config.BrowserProfileManagedPersistent {
			return true
		}
	}

	return false
}

func checkBrowserEndpoint(ctx context.Context, networkConfig config.BrowserNetworkConfig, raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" {
		return fmt.Errorf("endpoint is invalid")
	}
	method := "CONNECT"
	target, err := permissions.NetworkTargetFromURL(raw, method, permissions.NetworkRequestCDP)
	if err != nil {
		return err
	}
	policy, err := browser.NewNetworkPolicy(networkConfig)
	if err != nil {
		return err
	}
	if _, err := policy.Resolve(ctx, target); err != nil {
		return err
	}
	port := parsed.Port()
	if port == "" {
		if parsed.Scheme == "https" || parsed.Scheme == "wss" {
			port = "443"
		} else {
			port = "80"
		}
	}

	return dialBrowserEndpoint(ctx, "tcp", net.JoinHostPort(parsed.Hostname(), port))
}

func countStaleBrowserDirectories(root, prefix string, retention time.Duration) int {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	cutoff := time.Now().Add(-retention)
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		info, err := entry.Info()
		if err == nil && info.ModTime().Before(cutoff) {
			count++
		}
	}

	return count
}
