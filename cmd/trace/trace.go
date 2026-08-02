package trace

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	cli "github.com/urfave/cli/v3"

	morphcli "github.com/xymorphic/morph/internal/cli"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/datadir"
	"github.com/xymorphic/morph/internal/trace/inspect"
	"github.com/xymorphic/morph/pkg/logutils"
	"github.com/xymorphic/morph/pkg/str"
)

var log = logutils.Module("trace")

func NewCommand() *cli.Command {
	return &cli.Command{
		Name:  "trace",
		Usage: "Inspect stored session traces",
		Commands: []*cli.Command{
			newViewCommand(),
			newUnsafeCommand(),
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			return cli.ShowSubcommandHelp(cmd)
		},
	}
}

func newViewCommand() *cli.Command {
	return &cli.Command{
		Name:  "view",
		Usage: "Serve the local trace session viewer",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "trace-dir",
				Usage: "Directory containing trace session .jsonl files",
			},
			&cli.StringFlag{
				Name:  "listen",
				Usage: "Listener address for the local trace viewer",
				Value: "127.0.0.1:0",
			},
			&cli.StringFlag{
				Name:  "username",
				Usage: "Basic auth username for the trace viewer",
			},
			&cli.StringFlag{
				Name:  "password",
				Usage: "Basic auth password for the trace viewer",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, inputs, err := morphcli.LoadConfig(cmd)
			if err != nil {
				return err
			}
			morphcli.ApplyConfigOverrides(cmd, cfg)
			morphcli.AddStartupFilesystemRoots(cfg, inputs)
			config.Set(cfg)
			_ = logutils.ConfigureLogger("morph", cfg.Log.NoColor)
			logutils.SetLogLevel(cfg.Log.Level)
			literalValue := str.String(cmd.String("trace-dir"))
			traceDir := literalValue.Trim()
			if traceDir == "" {
				dirValue := str.String(cfg.Trace.Disk.Dir)
				traceDir = dirValue.Trim()
			}
			if traceDir == "" {
				traceDir = datadir.DebugTraceDir()
			}

			app := inspect.NewApp(traceDir)
			if err := inspect.ConfigureStateProvider(cfg, app); err != nil {
				return err
			}
			if err := app.Validate(); err != nil {
				return err
			}
			literalValue2 := str.String(cmd.String("username"))
			username := literalValue2.Trim()
			password := cmd.String("password")
			if (username == "") != (password == "") {
				return fmt.Errorf("trace viewer basic auth requires both username and password")
			}
			if username != "" {
				app.SetBasicAuth(username, password)
			}
			literalValue3 := str.String(cmd.String("listen"))
			listenAddr := literalValue3.Trim()
			if listenAddr == "" {
				listenAddr = "127.0.0.1:0"
			}

			log.Info().
				Str("traceDir", traceDir).
				Str("listen", listenAddr).
				Msg("Starting trace viewer")

			return serve(ctx, app, traceDir, listenAddr)
		},
	}
}

func serve(ctx context.Context, app *inspect.App, traceDir string, listenAddr string) error {
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()

	server := &http.Server{
		Handler: app.Handler(),
	}

	url := fmt.Sprintf("http://%s", listener.Addr().String())
	log.Info().
		Str("traceDir", traceDir).
		Str("listen", listener.Addr().String()).
		Str("url", url).
		Msg("Trace viewer listening")

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Serve(listener)
	}()

	select {
	case err := <-serverErr:
		if err == nil || err == http.ErrServerClosed {
			return nil
		}

		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		err := <-serverErr
		if err == nil || err == http.ErrServerClosed {
			return nil
		}

		return err
	}
}
