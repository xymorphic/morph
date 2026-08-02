package daemon

import (
	"context"
	"io"
	"os"

	urfavecli "github.com/urfave/cli/v3"

	morphagent "github.com/xymorphic/morph/internal/agent"
	"github.com/xymorphic/morph/internal/config"
	models "github.com/xymorphic/morph/internal/model"
	"github.com/xymorphic/morph/pkg/logutils"
)

type agentRunner interface {
	Start(context.Context) error
	morphagent.ServiceAPI
}

type closeableAgentRunner interface {
	Close() error
}

type sessionAgentRunner interface {
	StartSessionRunner(context.Context) error
}

var daemonLog = logutils.Module("daemon")

var daemonDependencies = Dependencies{}

var startupOutput io.Writer = os.Stdout

func SetOutput(w io.Writer) io.Writer {
	previous := startupOutput
	if w == nil {
		startupOutput = io.Discard
		return previous
	}
	startupOutput = w
	return previous
}

func RunWithConfigRestarts(ctx context.Context, cmd *urfavecli.Command, deps Dependencies) error {
	previous := daemonDependencies
	daemonDependencies = deps
	defer func() {
		daemonDependencies = previous
	}()

	return runDaemonWithConfigRestarts(ctx, cmd, daemonConfigWatchDebounce)
}

func RunOnce(ctx context.Context, cfg *config.Config) error {
	return runDaemonOnce(ctx, cfg)
}

func newAgentRunnerImpl(
	ctx context.Context,
	cfg *config.Config,
	modelClient,
	summaryClient,
	rerankerClient models.Client,
) agentRunner {
	return morphagent.NewAgent(ctx, cfg, modelClient, summaryClient, rerankerClient)
}

var newAgentRunner = newAgentRunnerImpl
