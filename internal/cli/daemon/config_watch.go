package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
	urfavecli "github.com/urfave/cli/v3"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/diagnostics"
	"github.com/xymorphic/morph/pkg/str"
)

var osStat = os.Stat

var daemonConfigWatchDebounce = 200 * time.Millisecond

var newConfigWatcher = newFSNotifyConfigWatcher

var createFSNotifyWatcher = fsnotify.NewWatcher

var mkdirAllConfigWatchDir = os.MkdirAll

var addConfigWatchDir = func(watcher *fsnotify.Watcher, path string) error {
	return watcher.Add(path)
}

type daemonConfigSnapshot struct {
	cfg         *config.Config
	inputs      ConfigInputs
	fingerprint configFileFingerprint
}

type configFileFingerprint struct {
	modTime time.Time
	size    int64
}

func loadDaemonConfig(cmd *urfavecli.Command) (daemonConfigSnapshot, error) {
	cfg, inputs, err := daemonDependencies.loadConfig(cmd)
	if err != nil {
		return daemonConfigSnapshot{}, err
	}

	daemonDependencies.applyConfigOverrides(cmd, cfg)
	daemonDependencies.addStartupFilesystemRoots(cfg, inputs)
	report := diagnostics.BuildWithOptions(inputs.EnvPath, inputs.ConfigPath, cfg, nil, diagnostics.BuildOptions{
		Validate:       (*config.Config).ValidateRelaxed,
		CheckModelAuth: false,
		ValidationPass: "daemon configuration is valid",
	})
	if report.HasFailures() {
		return daemonConfigSnapshot{}, errors.New(report.FirstFailure())
	}

	fingerprint, err := getConfigFileFingerprint(inputs.ConfigPath)
	if err != nil {
		return daemonConfigSnapshot{}, err
	}

	return daemonConfigSnapshot{
		cfg:         cfg,
		inputs:      inputs,
		fingerprint: fingerprint,
	}, nil
}

func getConfigFileFingerprint(path string) (configFileFingerprint, error) {
	pathValue := str.String(path)
	path = pathValue.Trim()
	if path == "" {
		return configFileFingerprint{}, errors.New("config path is required")
	}

	info, err := osStat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return configFileFingerprint{}, nil
		}

		return configFileFingerprint{}, err
	}

	return configFileFingerprint{modTime: info.ModTime(), size: info.Size()}, nil
}

func hasConfigFileChanged(path string, previous configFileFingerprint) (configFileFingerprint, bool, error) {
	current, err := getConfigFileFingerprint(path)
	if err != nil {
		return configFileFingerprint{}, false, err
	}

	return current, current != previous, nil
}

type configWatcher struct {
	events <-chan fsnotify.Event
	errors <-chan error
	close  func() error
}

func newFSNotifyConfigWatcher(configPath string) (configWatcher, error) {
	configPathValue := str.String(configPath)
	configPath = configPathValue.Trim()
	if configPath == "" {
		return configWatcher{}, errors.New("config path is required")
	}

	watcher, err := createFSNotifyWatcher()
	if err != nil {
		return configWatcher{}, err
	}

	configDir := filepath.Dir(configPath)
	if err := mkdirAllConfigWatchDir(configDir, 0o700); err != nil {
		_ = watcher.Close()
		return configWatcher{}, err
	}
	if err := addConfigWatchDir(watcher, configDir); err != nil {
		_ = watcher.Close()
		return configWatcher{}, err
	}

	return configWatcher{
		events: watcher.Events,
		errors: watcher.Errors,
		close:  watcher.Close,
	}, nil
}

func isConfigFileWatchEvent(event fsnotify.Event, configPath string) bool {
	nameValue := str.String(event.Name)
	if nameValue.Trim() == "" {
		return false
	}

	eventPath := filepath.Clean(event.Name)
	targetPath := filepath.Clean(configPath)
	if eventPath != targetPath {
		return false
	}

	reloadOps := fsnotify.Write | fsnotify.Create | fsnotify.Rename | fsnotify.Remove
	return event.Op&reloadOps != 0
}

func runDaemonWithConfigRestarts(ctx context.Context, cmd *urfavecli.Command, debounce time.Duration) error {
	if debounce <= 0 {
		debounce = daemonConfigWatchDebounce
	}

	snapshot, err := loadDaemonConfig(cmd)
	if err != nil {
		return err
	}

	for {
		next, restart, err := runDaemonUntilConfigChange(ctx, cmd, snapshot, debounce)
		if err != nil || !restart {
			return err
		}

		snapshot = next
	}
}

func runDaemonUntilConfigChange(
	ctx context.Context,
	cmd *urfavecli.Command,
	snapshot daemonConfigSnapshot,
	debounce time.Duration,
) (daemonConfigSnapshot, bool, error) {
	watcher, err := newConfigWatcher(snapshot.inputs.ConfigPath)
	if err != nil {
		return daemonConfigSnapshot{}, false, err
	}
	defer func() { _ = watcher.close() }()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- runDaemonOnce(runCtx, snapshot.cfg)
	}()

	var reload <-chan time.Time
	var timer *time.Timer
	defer stopConfigReloadTimer(timer)

	lastFingerprint := snapshot.fingerprint
	lastInvalidFingerprint := configFileFingerprint{size: -1}
	for {
		select {
		case err := <-done:
			return daemonConfigSnapshot{}, false, err
		case <-ctx.Done():
			err := waitForDaemonStop(cancel, done)
			return daemonConfigSnapshot{}, false, err
		case err, ok := <-watcher.errors:
			if !ok {
				watcher.errors = nil
				continue
			}
			daemonLog.Error().Err(err).Msg("Config file watcher failed")
		case event, ok := <-watcher.events:
			if !ok {
				watcher.events = nil
				continue
			}
			if !isConfigFileWatchEvent(event, snapshot.inputs.ConfigPath) {
				continue
			}
			timer, reload = resetConfigReloadTimer(timer, debounce)
		case <-reload:
			reload = nil
			fingerprint, changed, err := hasConfigFileChanged(snapshot.inputs.ConfigPath, lastFingerprint)
			if err != nil {
				if fingerprint != lastInvalidFingerprint {
					daemonLog.Error().Err(err).Msg("Config reload check failed")
					lastInvalidFingerprint = fingerprint
				}
				continue
			}
			if !changed {
				continue
			}

			next, err := loadDaemonConfig(cmd)
			if err != nil {
				if fingerprint != lastInvalidFingerprint {
					daemonLog.Error().Err(err).Msg("Config reload validation failed")
					lastInvalidFingerprint = fingerprint
				}
				lastFingerprint = fingerprint
				continue
			}

			daemonLog.Info().Msg("Configuration changed; restarting Morph services")
			cancel()
			if err := <-done; err != nil {
				return daemonConfigSnapshot{}, false, err
			}

			return next, true, nil
		}
	}
}

func waitForDaemonStop(cancel context.CancelFunc, done <-chan error) error {
	cancel()
	return <-done
}

func resetConfigReloadTimer(timer *time.Timer, debounce time.Duration) (*time.Timer, <-chan time.Time) {
	if debounce <= 0 {
		debounce = daemonConfigWatchDebounce
	}

	if timer == nil {
		timer = time.NewTimer(debounce)
		return timer, timer.C
	}

	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(debounce)

	return timer, timer.C
}

func stopConfigReloadTimer(timer *time.Timer) {
	if timer == nil {
		return
	}

	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}
