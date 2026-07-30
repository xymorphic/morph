package tui

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	cli "github.com/urfave/cli/v3"

	morphcli "github.com/wandxy/morph/internal/cli"
	"github.com/wandxy/morph/internal/config"
	rpcclient "github.com/wandxy/morph/internal/rpc/client"
	storage "github.com/wandxy/morph/internal/state/core"
	tui "github.com/wandxy/morph/internal/tui/app"
	agentsession "github.com/wandxy/morph/pkg/agent/session"
	"github.com/wandxy/morph/pkg/logutils"
)

var daemonLog = logutils.Module("daemon")

type fakeProgram struct {
	model tea.Model
	err   error
	ran   bool
}

func (program *fakeProgram) Run() (tea.Model, error) {
	program.ran = true
	return program.model, program.err
}

type fakeModel struct{}

func (fakeModel) Init() tea.Cmd {
	return nil
}

func (fakeModel) Update(tea.Msg) (tea.Model, tea.Cmd) {
	return fakeModel{}, nil
}

func (fakeModel) View() tea.View {
	return tea.NewView("")
}

func TestRun_StartsProgram(t *testing.T) {
	originalNewProgram := newProgram
	originalLoadCommandModel := loadCommandModel
	t.Cleanup(func() {
		newProgram = originalNewProgram
		loadCommandModel = originalLoadCommandModel
	})

	program := &fakeProgram{}
	newProgram = func(model tea.Model) programRunner {
		program.model = model
		return program
	}
	loadCommandModel = func(context.Context, *cli.Command) (tea.Model, func(), error) {
		return fakeModel{}, func() {}, nil
	}

	err := Run(context.Background(), &cli.Command{})

	require.NoError(t, err)
	require.True(t, program.ran)
	require.IsType(t, fakeModel{}, program.model)
}

func TestRun_ReturnsProgramError(t *testing.T) {
	originalNewProgram := newProgram
	originalLoadCommandModel := loadCommandModel
	t.Cleanup(func() {
		newProgram = originalNewProgram
		loadCommandModel = originalLoadCommandModel
	})

	expectedErr := errors.New("terminal unavailable")
	newProgram = func(model tea.Model) programRunner {
		return &fakeProgram{model: model, err: expectedErr}
	}
	loadCommandModel = func(context.Context, *cli.Command) (tea.Model, func(), error) {
		return fakeModel{}, func() {}, nil
	}

	err := Run(context.Background(), &cli.Command{})

	require.ErrorIs(t, err, expectedErr)
}

func TestRun_ReturnsModelLoadError(t *testing.T) {
	originalLoadCommandModel := loadCommandModel
	t.Cleanup(func() {
		loadCommandModel = originalLoadCommandModel
	})

	expectedErr := errors.New("rpc unavailable")
	loadCommandModel = func(context.Context, *cli.Command) (tea.Model, func(), error) {
		return nil, nil, expectedErr
	}

	err := Run(context.Background(), &cli.Command{})

	require.ErrorIs(t, err, expectedErr)
}

func TestDefaultTUIFactories_ConstructProgramAndClient(t *testing.T) {
	runner := newProgram(fakeModel{})
	require.NotNil(t, runner)

	client, err := newTUIChatClient(context.Background(), &config.Config{
		RPC: config.RPCConfig{Address: "127.0.0.1", Port: 1},
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	require.NoError(t, client.Close())
}

func TestGetTUIClientOptions_AuthorizesRequiredServices(t *testing.T) {
	options := getTUIClientOptions(&config.Config{
		RPC: config.RPCConfig{Address: "127.0.0.1", Port: 8080},
	})

	require.ElementsMatch(t, []string{
		"/morph.v1.SessionService",
		"/morph.v1.ModelService",
		"/morph.v1.PermissionService",
		"/morph.v1.BrowserService",
	}, options.AuthServices)
}

func TestLoadTUICommandModel_UsesConfiguredRPCClientAndCleanup(t *testing.T) {
	originalNewTUIChatClient := newTUIChatClient
	originalEnsureTUIDaemonRunning := ensureTUIDaemonRunning
	t.Cleanup(func() {
		newTUIChatClient = originalNewTUIChatClient
		ensureTUIDaemonRunning = originalEnsureTUIDaemonRunning
		logutils.SetOutput(nil)
		logutils.SetConsoleEnabled(true)
		logutils.SetFileOutput(nil)
	})

	home := t.TempDir()
	t.Setenv("HOME", home)
	profileHome := filepath.Join(home, ".morph", "profiles", "work")
	require.NoError(t, os.MkdirAll(profileHome, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(profileHome, ".env"), nil, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(profileHome, "config.yaml"), []byte(`
name: tui-agent
rpc:
  address: 127.0.0.2
  port: 45678
models:
tui:
  thinkingComposer: false
`), 0o600))

	client := &fakeTUIChatClient{}
	var gotRPC config.RPCConfig
	var gotEnsureRPC config.RPCConfig
	daemonCleaned := false
	ensureTUIDaemonRunning = func(
		_ context.Context,
		cfg *config.Config,
	) (func() error, error) {
		gotEnsureRPC = cfg.RPC
		daemonLog.Info().Msg("daemon bootstrap log should not reach console")
		return func() error {
			daemonCleaned = true
			return nil
		}, nil
	}
	newTUIChatClient = func(_ context.Context, cfg *config.Config) (tuiClient, error) {
		gotRPC = cfg.RPC
		return client, nil
	}

	logOutput := &bytes.Buffer{}
	fileLogOutput := &bytes.Buffer{}
	logutils.SetOutput(logOutput)
	logutils.SetFileOutput(fileLogOutput)

	var cleanup func()
	cmd := newTUITestRootCommand(func(ctx context.Context, cmd *cli.Command) error {
		var err error
		_, cleanup, err = loadTUICommandModel(ctx, cmd)
		return err
	})

	err := cmd.Run(context.Background(), []string{"morph", "--profile", "work"})

	require.NoError(t, err)
	require.Equal(t, config.RPCConfig{Address: "127.0.0.2", Port: 45678}, gotEnsureRPC)
	require.Equal(t, config.RPCConfig{Address: "127.0.0.2", Port: 45678}, gotRPC)
	require.NotNil(t, cleanup)
	cleanup()
	require.True(t, client.closed)
	require.True(t, daemonCleaned)

	require.Empty(t, logOutput.String())
	require.Contains(t, fileLogOutput.String(), `"message":"daemon bootstrap log should not reach console"`)
	require.Contains(t, fileLogOutput.String(), `"module":"daemon"`)
}

func TestLoadTUICommandModel_ReturnsConfigLoadError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, "bad-config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("{"), 0o600))

	cmd := newTUITestRootCommand(func(ctx context.Context, cmd *cli.Command) error {
		_, _, err := loadTUICommandModel(ctx, cmd)
		return err
	})

	err := cmd.Run(context.Background(), []string{"morph", "--config", configPath})

	require.Error(t, err)
	require.ErrorContains(t, err, "yaml")
}

func TestLoadTUICommandModel_IgnoresStaleRuntimeMetadata(t *testing.T) {
	originalNewTUIChatClient := newTUIChatClient
	originalEnsureTUIDaemonRunning := ensureTUIDaemonRunning
	t.Cleanup(func() {
		newTUIChatClient = originalNewTUIChatClient
		ensureTUIDaemonRunning = originalEnsureTUIDaemonRunning
	})

	home := t.TempDir()
	t.Setenv("HOME", home)
	profileHome := filepath.Join(home, ".morph", "profiles", "work")
	require.NoError(t, os.MkdirAll(profileHome, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(profileHome, ".env"), nil, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(profileHome, "runtime.json"), []byte("{"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(profileHome, "config.yaml"), []byte(`
name: tui-agent
models:
`), 0o600))

	ensureTUIDaemonRunning = func(context.Context, *config.Config) (func() error, error) {
		return func() error { return nil }, nil
	}
	newTUIChatClient = func(context.Context, *config.Config) (tuiClient, error) {
		return &fakeTUIChatClient{}, nil
	}

	cmd := newTUITestRootCommand(func(ctx context.Context, cmd *cli.Command) error {
		_, cleanup, err := loadTUICommandModel(ctx, cmd)
		if cleanup != nil {
			cleanup()
		}
		return err
	})

	err := cmd.Run(context.Background(), []string{"morph", "--profile", "work"})

	require.NoError(t, err)
}

func TestLoadTUICommandModel_ReturnsClientCreationError(t *testing.T) {
	originalNewTUIChatClient := newTUIChatClient
	originalEnsureTUIDaemonRunning := ensureTUIDaemonRunning
	t.Cleanup(func() {
		newTUIChatClient = originalNewTUIChatClient
		ensureTUIDaemonRunning = originalEnsureTUIDaemonRunning
	})

	home := t.TempDir()
	t.Setenv("HOME", home)
	profileHome := filepath.Join(home, ".morph", "profiles", "work")
	require.NoError(t, os.MkdirAll(profileHome, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(profileHome, ".env"), nil, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(profileHome, "config.yaml"), []byte(`
name: tui-agent
rpc:
  address: 127.0.0.2
  port: 45678
models:
`), 0o600))

	expectedErr := errors.New("client unavailable")
	daemonCleaned := false
	ensureTUIDaemonRunning = func(context.Context, *config.Config) (func() error, error) {
		return func() error {
			daemonCleaned = true
			return nil
		}, nil
	}
	newTUIChatClient = func(context.Context, *config.Config) (tuiClient, error) {
		return nil, expectedErr
	}

	cmd := newTUITestRootCommand(func(ctx context.Context, cmd *cli.Command) error {
		_, _, err := loadTUICommandModel(ctx, cmd)
		return err
	})

	err := cmd.Run(context.Background(), []string{"morph", "--profile", "work"})

	require.ErrorIs(t, err, expectedErr)
	require.True(t, daemonCleaned)
}

func TestLoadTUICommandModel_ReturnsDaemonBootstrapError(t *testing.T) {
	originalNewTUIChatClient := newTUIChatClient
	originalEnsureTUIDaemonRunning := ensureTUIDaemonRunning
	t.Cleanup(func() {
		newTUIChatClient = originalNewTUIChatClient
		ensureTUIDaemonRunning = originalEnsureTUIDaemonRunning
	})

	home := t.TempDir()
	t.Setenv("HOME", home)
	profileHome := filepath.Join(home, ".morph", "profiles", "work")
	require.NoError(t, os.MkdirAll(profileHome, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(profileHome, ".env"), nil, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(profileHome, "config.yaml"), []byte(`
name: tui-agent
rpc:
  address: 127.0.0.2
  port: 45678
models:
`), 0o600))

	expectedErr := errors.New("daemon unavailable")
	ensureTUIDaemonRunning = func(context.Context, *config.Config) (func() error, error) {
		return nil, expectedErr
	}
	newTUIChatClient = func(context.Context, *config.Config) (tuiClient, error) {
		t.Fatal("client should not be created when daemon bootstrap fails")
		return nil, nil
	}

	cmd := newTUITestRootCommand(func(ctx context.Context, cmd *cli.Command) error {
		_, _, err := loadTUICommandModel(ctx, cmd)
		return err
	})

	err := cmd.Run(context.Background(), []string{"morph", "--profile", "work"})

	require.ErrorIs(t, err, expectedErr)
}

func newTUITestRootCommand(action func(context.Context, *cli.Command) error) *cli.Command {
	envFile := ".env"
	configFile := "config.yaml"

	return &cli.Command{
		Flags: morphcli.RootFlags(&envFile, &configFile),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return action(ctx, cmd)
		},
	}
}

type fakeTUIChatClient struct {
	closed bool
}

func (c *fakeTUIChatClient) EnqueueMessage(
	context.Context,
	rpcclient.EnqueueMessageOptions,
) (rpcclient.SessionQueueEntry, error) {
	return rpcclient.SessionQueueEntry{}, nil
}

func (c *fakeTUIChatClient) State(
	context.Context,
	string,
) (rpcclient.SessionExecutionState, error) {
	return rpcclient.SessionExecutionState{}, nil
}

func (c *fakeTUIChatClient) SetReasoningEffort(
	context.Context,
	rpcclient.SetReasoningEffortOptions,
) (agentsession.ReasoningSettings, error) {
	return agentsession.ReasoningSettings{}, nil
}

func (c *fakeTUIChatClient) Observe(
	context.Context,
	string,
	int64,
	func(rpcclient.SessionEvent) error,
) error {
	return nil
}

func (c *fakeTUIChatClient) EditQueuedMessage(
	context.Context,
	string,
	string,
	string,
) (rpcclient.SessionQueueEntry, error) {
	return rpcclient.SessionQueueEntry{}, nil
}

func (c *fakeTUIChatClient) RemoveQueuedMessage(
	context.Context,
	string,
	string,
) (rpcclient.SessionQueueEntry, error) {
	return rpcclient.SessionQueueEntry{}, nil
}

func (c *fakeTUIChatClient) PromoteQueuedMessage(
	context.Context,
	string,
	string,
) (rpcclient.SessionQueueEntry, error) {
	return rpcclient.SessionQueueEntry{}, nil
}

func (c *fakeTUIChatClient) SteerQueuedMessage(
	context.Context,
	string,
	string,
) (rpcclient.SessionQueueEntry, error) {
	return rpcclient.SessionQueueEntry{}, nil
}

func (c *fakeTUIChatClient) InterruptRun(
	context.Context,
	string,
) (rpcclient.SessionActiveRun, bool, error) {
	return rpcclient.SessionActiveRun{}, false, nil
}

func (c *fakeTUIChatClient) SessionAPI() rpcclient.SessionAPI {
	return c
}

func (c *fakeTUIChatClient) ModelAPI() rpcclient.ModelAPI {
	return c
}

func (c *fakeTUIChatClient) BrowserAPI() rpcclient.BrowserAPI {
	return nil
}

func (c *fakeTUIChatClient) ListProviders(context.Context) (rpcclient.ProviderList, error) {
	return rpcclient.ProviderList{}, nil
}

func (c *fakeTUIChatClient) RuntimeModel(context.Context) (rpcclient.ModelRuntime, error) {
	return rpcclient.ModelRuntime{}, nil
}

func (c *fakeTUIChatClient) ListModels(context.Context, ...rpcclient.ModelListOptions) (rpcclient.ModelList, error) {
	return rpcclient.ModelList{}, nil
}

func (c *fakeTUIChatClient) SelectModel(context.Context, string, ...rpcclient.ModelSelectOptions) (rpcclient.ModelOption, error) {
	return rpcclient.ModelOption{}, nil
}

func (c *fakeTUIChatClient) SetProviderAPIKey(context.Context, string, string) error {
	return nil
}

func (c *fakeTUIChatClient) Timeline(
	context.Context,
	rpcclient.SessionTimelineOptions,
) (rpcclient.SessionTimeline, error) {
	return rpcclient.SessionTimeline{}, nil
}

func (c *fakeTUIChatClient) Compact(context.Context, string) (rpcclient.CompactSessionResult, error) {
	return rpcclient.CompactSessionResult{}, nil
}

func (c *fakeTUIChatClient) Create(context.Context, string) (storage.Session, error) {
	return storage.Session{}, nil
}

func (c *fakeTUIChatClient) CreateWithOptions(
	context.Context,
	rpcclient.CreateSessionOptions,
) (storage.Session, error) {
	return storage.Session{}, nil
}

func (c *fakeTUIChatClient) List(context.Context, ...rpcclient.SessionListOptions) ([]storage.Session, error) {
	return nil, nil
}

func (c *fakeTUIChatClient) Use(context.Context, string) error {
	return nil
}

func (c *fakeTUIChatClient) Archive(context.Context, string) error {
	return nil
}

func (c *fakeTUIChatClient) Unarchive(context.Context, string) (storage.Session, error) {
	return storage.Session{}, nil
}

func (c *fakeTUIChatClient) Rename(context.Context, string, string) (storage.Session, error) {
	return storage.Session{}, nil
}

func (c *fakeTUIChatClient) Current(context.Context) (storage.Session, error) {
	return storage.Session{}, nil
}

func (c *fakeTUIChatClient) Repair(
	context.Context,
	rpcclient.RepairSessionOptions,
) (rpcclient.RepairSessionResult, error) {
	return rpcclient.RepairSessionResult{}, nil
}

func (c *fakeTUIChatClient) Status(context.Context, string) (rpcclient.ContextStatus, error) {
	return rpcclient.ContextStatus{}, nil
}

func (c *fakeTUIChatClient) Close() error {
	c.closed = true
	return nil
}

var _ tui.SessionTimelineLoader = (*fakeTUIChatClient)(nil)
