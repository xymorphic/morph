package cli

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	urfavecli "github.com/urfave/cli/v3"

	clidaemon "github.com/wandxy/morph/internal/cli/daemon"
	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/permissions"
	"github.com/wandxy/morph/internal/profile"
	rpcclient "github.com/wandxy/morph/internal/rpc/client"
	morphruntime "github.com/wandxy/morph/internal/runtime"
	"github.com/wandxy/morph/pkg/logutils"
	"github.com/wandxy/morph/pkg/str"
)

const (
	defaultDaemonBootstrapInitialTimeout = 250 * time.Millisecond
	defaultDaemonBootstrapReadyTimeout   = 10 * time.Second
	defaultDaemonBootstrapPollInterval   = 100 * time.Millisecond
)

var (
	checkDaemonRPC                = checkDaemonRPCImpl
	checkDaemonHealth             = checkDaemonHealthImpl
	probeActiveRuntime            = morphruntime.Probe
	daemonStatusNow               = time.Now
	startDaemonRuntime            = startDaemonRuntimeImpl
	runDaemonRuntimeOnce          = RunDaemonOnce
	setDaemonOutput               = clidaemon.SetOutput
	runDaemonWithConfigRestarts   = clidaemon.RunWithConfigRestarts
	runDaemonOnce                 = clidaemon.RunOnce
	daemonBootstrapInitialTimeout = defaultDaemonBootstrapInitialTimeout
	daemonBootstrapReadyTimeout   = defaultDaemonBootstrapReadyTimeout
	daemonBootstrapPollInterval   = defaultDaemonBootstrapPollInterval
	newDaemonHealthClient         = func(
		ctx context.Context,
		cfg *config.Config,
		address string,
		port int,
	) (daemonHealthClient, error) {
		return rpcclient.NewClient(ctx, rpcclient.OptionsWithConfigAuth(rpcclient.Options{
			Address:           address,
			Port:              port,
			PermissionSurface: permissions.SurfaceCLI,
			PermissionPreset:  cfg.Permissions.EffectivePreset(),
			AuthMethods:       []string{"/grpc.health.v1.Health/Check"},
		}, cfg))
	}
)

type daemonHealthClient interface {
	CheckHealth(context.Context) (string, error)
	Close() error
}

type DaemonStatus struct {
	State     string
	Health    string
	Profile   string
	PID       int
	Address   string
	Port      int
	StartedAt time.Time
	Uptime    time.Duration
}

func SetDaemonOutput(w io.Writer) io.Writer {
	return setDaemonOutput(w)
}

func RunDaemonWithConfigRestarts(ctx context.Context, cmd *urfavecli.Command) error {
	return runDaemonWithConfigRestarts(ctx, cmd, daemonDependencies())
}

func RunDaemonOnce(ctx context.Context, cfg *config.Config) error {
	return runDaemonOnce(ctx, cfg)
}

func GetDaemonStatus(ctx context.Context) (DaemonStatus, error) {
	activeProfile := profile.Active()
	probe := probeActiveRuntime(ctx, activeProfile)
	status := daemonStatusFromProbe(probe)
	if probe.State == morphruntime.ProbeStateMissing {
		return status, nil
	}
	if probe.State != morphruntime.ProbeStateReady {
		if probe.Err != nil {
			return status, fmt.Errorf("daemon is %s: %w", probe.State, probe.Err)
		}

		return status, fmt.Errorf("daemon is %s", probe.State)
	}

	cfg, err := config.Load(activeProfile.EnvPath, activeProfile.ConfigPath)
	if err != nil {
		return status, fmt.Errorf("load daemon health configuration: %w", err)
	}

	health, err := checkDaemonHealth(ctx, cfg, status.Address, status.Port)
	if err != nil {
		return status, fmt.Errorf("daemon health check failed: %w", err)
	}

	status.State = "running"
	status.Health = health
	return status, nil
}

func EnsureDaemonRunning(ctx context.Context, cfg *config.Config) (func() error, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}

	checkCtx, cancel := context.WithTimeout(ctx, daemonBootstrapInitialTimeout)
	err := checkDaemonRPC(checkCtx, cfg)
	cancel()
	if err == nil {
		return nil, nil
	}

	cleanup, err := startDaemonRuntime(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := waitForDaemonRPC(ctx, cfg, daemonBootstrapReadyTimeout); err != nil {
		if cleanupErr := cleanup(); cleanupErr != nil {
			return nil, fmt.Errorf("start Morph daemon: cleanup after readiness failure: %w", cleanupErr)
		}

		addressValue := str.String(cfg.RPC.Address)
		return nil, fmt.Errorf("start Morph daemon: RPC did not become ready at %s:%d: %w", addressValue.
			Trim(), cfg.RPC.Port, err)
	}

	return cleanup, nil
}

func waitForDaemonRPC(ctx context.Context, cfg *config.Config, timeout time.Duration) error {
	if timeout <= 0 {
		return checkDaemonRPC(ctx, cfg)
	}

	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		checkCtx, cancel := context.WithTimeout(ctx, daemonBootstrapInitialTimeout)
		err := checkDaemonRPC(checkCtx, cfg)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		if time.Now().Add(daemonBootstrapPollInterval).After(deadline) {
			return lastErr
		}

		timer := time.NewTimer(daemonBootstrapPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func checkDaemonRPCImpl(ctx context.Context, cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("config is required")
	}

	addressValue2 := str.String(cfg.RPC.Address)
	address := addressValue2.Trim()
	if address == "" {
		return fmt.Errorf("rpc address is required")
	}
	if cfg.RPC.Port <= 0 {
		return fmt.Errorf("rpc port must be greater than zero")
	}

	_, err := checkDaemonHealth(ctx, cfg, address, cfg.RPC.Port)
	return err
}

func checkDaemonHealthImpl(
	ctx context.Context,
	cfg *config.Config,
	address string,
	port int,
) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("config is required")
	}
	addressValue3 := str.String(address)
	address = addressValue3.Trim()
	if address == "" {
		return "", fmt.Errorf("rpc address is required")
	}
	if port <= 0 {
		return "", fmt.Errorf("rpc port must be greater than zero")
	}

	client, err := newDaemonHealthClient(ctx, cfg, address, port)
	if err != nil {
		return "", err
	}
	defer func() { _ = client.Close() }()

	return client.CheckHealth(ctx)
}

func daemonStatusFromProbe(probe morphruntime.ProbeResult) DaemonStatus {
	metadata := probe.Metadata
	profileValue := str.String(metadata.Profile)
	addressValue4 := str.String(metadata.RPC.Address)
	status := DaemonStatus{
		State:     string(probe.State),
		Profile:   profileValue.Trim(),
		PID:       metadata.PID,
		Address:   addressValue4.Trim(),
		Port:      metadata.RPC.Port,
		StartedAt: metadata.StartedAt,
	}
	if status.Profile == "" {
		status.Profile = profile.DefaultName
	}
	if !status.StartedAt.IsZero() {
		status.Uptime = max(daemonStatusNow().Sub(status.StartedAt).Round(time.Second), 0)
	}

	return status
}

func startDaemonRuntimeImpl(ctx context.Context, cfg *config.Config) (func() error, error) {
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	previousOutput := SetDaemonOutput(io.Discard)
	previousConsoleEnabled := logutils.SetConsoleEnabled(false)
	var once sync.Once
	var cleanupErr error

	go func() {
		done <- runDaemonRuntimeOnce(runCtx, cfg)
	}()

	cleanup := func() error {
		once.Do(func() {
			cancel()
			cleanupErr = <-done
			SetDaemonOutput(previousOutput)
			logutils.SetConsoleEnabled(previousConsoleEnabled)
		})
		return cleanupErr
	}

	return cleanup, nil
}

func daemonDependencies() clidaemon.Dependencies {
	return clidaemon.Dependencies{
		LoadConfig: func(cmd *urfavecli.Command) (*config.Config, clidaemon.ConfigInputs, error) {
			cfg, inputs, err := LoadConfig(cmd)
			return cfg, daemonConfigInputs(inputs), err
		},
		ApplyConfigOverrides: func(cmd *urfavecli.Command, cfg *config.Config) {
			ApplyConfigOverrides(cmd, cfg)
		},
		AddStartupFilesystemRoots: func(cfg *config.Config, inputs clidaemon.ConfigInputs) {
			AddStartupFilesystemRoots(cfg, ConfigInputs{
				Profile:    inputs.Profile,
				EnvPath:    inputs.EnvPath,
				ConfigPath: inputs.ConfigPath,
			})
		},
		SafetySummary: SafetySummary,
	}
}

func daemonConfigInputs(inputs ConfigInputs) clidaemon.ConfigInputs {
	return clidaemon.ConfigInputs{
		Profile:    inputs.Profile,
		EnvPath:    inputs.EnvPath,
		ConfigPath: inputs.ConfigPath,
	}
}
