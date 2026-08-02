package readiness

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/moby/moby/client"
	"github.com/wandxy/morph/internal/config"
	executiondocker "github.com/wandxy/morph/internal/execution/docker"
)

func buildExecutionGroup(ctx context.Context, cfg *config.Config) Group {
	if cfg == nil {
		return Group{
			Name:   "execution",
			Checks: []Check{check("backend", StatusFail, "config is required")},
		}
	}
	if cfg.Execution.Backend == config.ExecutionBackendLocal {
		return Group{
			Name: "execution",
			Checks: []Check{
				check(
					"backend",
					StatusWarn,
					"local execution is enabled and commands are not container-contained",
				),
			},
		}
	}

	docker := cfg.Execution.Docker
	checks := []Check{
		check(
			"backend",
			StatusPass,
			fmt.Sprintf(
				"Docker %s scope is configured with %s workspace and %s network",
				docker.Scope,
				docker.Workspace.Mode,
				docker.Network,
			),
		),
		check("image", StatusPass, "sandbox image is pinned by digest: "+docker.Image),
	}

	endpoint := strings.TrimPrefix(docker.Endpoint, "unix://")
	if strings.HasPrefix(endpoint, "/") {
		if info, err := os.Stat(endpoint); err != nil {
			checks = append(
				checks,
				check("engine", StatusFail, "Docker socket is unavailable: "+err.Error()),
			)
		} else if info.Mode()&os.ModeSocket == 0 {
			checks = append(
				checks,
				check("engine", StatusFail, "configured Docker endpoint is not a local socket"),
			)
		} else if info.Mode().Perm()&0o002 != 0 {
			checks = append(
				checks,
				check("engine", StatusFail, "configured Docker socket is world-writable"),
			)
		} else {
			checks = append(checks, check("engine", StatusPass, "local Docker socket is available"))
		}
	}

	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	engine, err := executiondocker.NewClient(docker.Endpoint)
	if err != nil {
		checks = append(
			checks,
			check("engine-api", StatusFail, "Docker client initialization failed: "+err.Error()),
		)
		return Group{
			Name:   "execution",
			Checks: checks,
		}
	}
	defer func() { _ = engine.Close() }()

	if _, err := engine.Ping(probeCtx); err != nil {
		checks = append(
			checks,
			check("engine-api", StatusFail, "Docker Engine is unreachable: "+err.Error()),
		)
		return Group{
			Name:   "execution",
			Checks: checks,
		}
	}
	checks = append(
		checks,
		check("engine-api", StatusPass, "Docker Engine API negotiation succeeded"),
	)

	info, err := engine.Engine().Info(probeCtx, client.InfoOptions{})
	if err != nil {
		checks = append(
			checks,
			check(
				"engine-security",
				StatusFail,
				"Docker security posture is unavailable: "+err.Error(),
			),
		)
		return Group{
			Name:   "execution",
			Checks: checks,
		}
	}
	if info.Info.OSType != "linux" {
		checks = append(
			checks,
			check("engine-platform", StatusFail, "Docker must provide Linux containers"),
		)
	} else {
		checks = append(
			checks,
			check(
				"engine-platform",
				StatusPass,
				fmt.Sprintf(
					"Linux/%s engine with cgroup %s",
					info.Info.Architecture,
					info.Info.CgroupVersion,
				),
			),
		)
	}

	if !info.Info.MemoryLimit || !info.Info.PidsLimit ||
		(!info.Info.CPUCfsQuota && !info.Info.CPUShares) {
		checks = append(
			checks,
			check(
				"resource-controls",
				StatusFail,
				"Docker does not report all required memory, CPU, and PID controls",
			),
		)
	} else {
		checks = append(
			checks,
			check(
				"resource-controls",
				StatusPass,
				"memory, CPU, and PID controls are available",
			),
		)
	}

	security := strings.ToLower(strings.Join(info.Info.SecurityOptions, " "))
	if strings.Contains(security, "rootless") {
		checks = append(checks, check("daemon-trust", StatusPass, "rootless Docker is active"))
	} else {
		checks = append(
			checks,
			check(
				"daemon-trust",
				StatusWarn,
				"Docker is rootful; daemon control is host-root authority",
			),
		)
	}
	if !strings.Contains(security, "seccomp") {
		checks = append(
			checks,
			check("seccomp", StatusWarn, "Docker does not report a seccomp security option"),
		)
	} else {
		checks = append(checks, check("seccomp", StatusPass, "Docker seccomp support is active"))
	}
	if strings.Contains(security, "apparmor") || strings.Contains(security, "selinux") {
		checks = append(checks, check("lsm", StatusPass, "Docker reports an active host LSM"))
	} else {
		checks = append(
			checks,
			check(
				"lsm",
				StatusWarn,
				"Docker does not report AppArmor or SELinux confinement",
			),
		)
	}

	inspect, err := engine.Engine().ImageInspect(probeCtx, docker.Image)
	if err != nil {
		checks = append(
			checks,
			check("image-present", StatusFail, "pinned sandbox image is unavailable: "+err.Error()),
		)
	} else {
		checks = append(
			checks,
			check("image-present", StatusPass, "pinned sandbox image is available locally"),
		)

		contract, contractErr := executiondocker.LoadImageContract(docker.Contract)
		if contractErr != nil {
			checks = append(
				checks,
				check(
					"image-contract",
					StatusFail,
					"sandbox image contract is unavailable: "+contractErr.Error(),
				),
			)
		} else if inspect.Os != contract.GOOS ||
			!contract.SupportsArchitecture(inspect.Architecture) ||
			inspect.Config == nil ||
			inspect.Config.User != contract.User ||
			!slices.Equal(
				[]string(inspect.Config.Entrypoint),
				[]string{contract.Helper},
			) {
			checks = append(
				checks,
				check(
					"image-contract",
					StatusFail,
					"sandbox image does not match the configured contract",
				),
			)
		} else {
			checks = append(
				checks,
				check(
					"image-contract",
					StatusPass,
					"sandbox image platform and user match the configured contract",
				),
			)
		}
	}

	signatureCtx, signatureCancel := context.WithTimeout(ctx, 30*time.Second)
	defer signatureCancel()
	if err := executiondocker.VerifyImageSignature(signatureCtx, docker.Image); err != nil {
		checks = append(checks, check("image-signature", StatusFail, err.Error()))
	} else {
		checks = append(
			checks,
			check("image-signature", StatusPass, "sandbox image signature is trusted"),
		)
	}

	return Group{
		Name:   "execution",
		Checks: checks,
	}
}
