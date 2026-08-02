package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
	cli "github.com/urfave/cli/v3"

	morphcli "github.com/xymorphic/morph/internal/cli"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/permissions"
	rpcclient "github.com/xymorphic/morph/internal/rpc/client"
	tui "github.com/xymorphic/morph/internal/tui/app"
	"github.com/xymorphic/morph/pkg/logutils"
)

type programRunner interface {
	Run() (tea.Model, error)
}

var newProgram = func(model tea.Model) programRunner {
	return tea.NewProgram(model)
}

type tuiClient interface {
	rpcclient.ChatAPI
	SessionAPI() rpcclient.SessionAPI
	ModelAPI() rpcclient.ModelAPI
	BrowserAPI() rpcclient.BrowserAPI
	Close() error
}

var newTUIChatClient = func(ctx context.Context, cfg *config.Config) (tuiClient, error) {
	return rpcclient.NewClient(ctx, getTUIClientOptions(cfg))
}

func getTUIClientOptions(cfg *config.Config) rpcclient.Options {
	return rpcclient.OptionsWithConfigAuth(rpcclient.Options{
		Address:           cfg.RPC.Address,
		Port:              cfg.RPC.Port,
		PermissionSurface: permissions.SurfaceTUI,
		AuthServices: []string{
			"/morph.v1.SessionService",
			"/morph.v1.ModelService",
			"/morph.v1.PermissionService",
			"/morph.v1.BrowserService",
		},
	}, cfg)
}

var (
	ensureTUIDaemonRunning = morphcli.EnsureDaemonRunningWithConfigRestarts
	loadCommandModel       = loadTUICommandModel
)

func loadTUICommandModel(ctx context.Context, cmd *cli.Command) (tea.Model, func(), error) {
	cfg, inputs, err := morphcli.LoadConfig(cmd)
	if err != nil {
		return nil, nil, err
	}

	morphcli.ApplyConfigOverrides(cmd, cfg)
	morphcli.AddStartupFilesystemRoots(cfg, inputs)

	config.Set(cfg)
	logutils.SetConsoleEnabled(false)
	_ = logutils.ConfigureLogger("morph", cfg.Log.NoColor)
	logutils.SetLogLevel(cfg.Log.Level)

	daemonCleanup, err := ensureTUIDaemonRunning(ctx, cmd, cfg)
	if err != nil {
		return nil, nil, err
	}

	client, err := newTUIChatClient(ctx, cfg)
	if err != nil {
		if daemonCleanup != nil {
			_ = daemonCleanup()
		}
		return nil, nil, err
	}

	cleanup := func() {
		_ = client.Close()
		if daemonCleanup != nil {
			_ = daemonCleanup()
		}
	}

	return tui.NewModelWithClientContextAndConfig(ctx, client, cfg), cleanup, nil
}
