package daemon

import (
	"context"
	"fmt"

	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/datadir"
	"github.com/wandxy/morph/pkg/logutils"
)

func runDaemonOnce(ctx context.Context, cfg *config.Config) error {
	config.Set(cfg)
	_ = logutils.ConfigureLogger("morph", cfg.Log.NoColor)
	logutils.SetLogLevel(cfg.Log.Level)

	runtimeCfg := prepareDaemonRuntimeConfig(cfg)
	config.Set(runtimeCfg)

	if _, err := fmt.Fprint(startupOutput, renderStartupPanel(runtimeCfg)); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(startupOutput); err != nil {
		return err
	}

	daemonLog.Info().Msg("Configuration loaded")
	if runtimeCfg.RetainUnsafeEnabled() {
		daemonLog.Warn().
			Str("directory", datadir.UnsafeEvidenceDir()).
			Msg("Unsafe evidence plaintext retention is enabled; same-user and full-access tools can read it; cleanup is manual")
	}
	if runtimeCfg.Search.Vector.Enabled {
		daemonLog.Info().Msg("Vector retrieval configured")
	}

	daemonLog.Info().Msg("Starting Morph services")

	modelClient, summaryClient, rerankerClient, err := buildDaemonModelClients(runtimeCfg)
	if err != nil {
		return err
	}

	lis, err := openRPCListener(runtimeCfg)
	if err != nil {
		return err
	}
	defer func() { _ = lis.Close() }()

	agent := newAgentRunner(ctx, runtimeCfg, modelClient, summaryClient, rerankerClient)
	if err := agent.Start(ctx); err != nil {
		_ = lis.Close()
		return err
	}
	if runner, ok := agent.(sessionAgentRunner); ok {
		if err := runner.StartSessionRunner(ctx); err != nil {
			_ = lis.Close()
			return err
		}
	}

	err = serveDaemonServices(ctx, runtimeCfg, agent, lis)
	if closer, ok := agent.(closeableAgentRunner); ok {
		if closeErr := closer.Close(); err == nil {
			if isMissingCredentialLockError(closeErr) {
				daemonLog.Debug().Err(closeErr).Msg("Ignoring missing credential lock during shutdown")
			} else {
				err = closeErr
			}
		}
	}

	return err
}
