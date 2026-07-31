package daemon

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/require"
	urfavecli "github.com/urfave/cli/v3"
	morphauth "github.com/wandxy/morph/internal/auth"
	authstorememory "github.com/wandxy/morph/internal/auth/storememory"
	"github.com/wandxy/morph/internal/automation"
	"github.com/wandxy/morph/internal/brand"
	"github.com/wandxy/morph/internal/browser"
	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/constants"
	"github.com/wandxy/morph/internal/credential"
	morphgateway "github.com/wandxy/morph/internal/gateway"
	agentstub "github.com/wandxy/morph/internal/mocks/agentstub"
	models "github.com/wandxy/morph/internal/model"
	modelclient "github.com/wandxy/morph/internal/model/client"
	modelprovider "github.com/wandxy/morph/internal/model/provider"
	provider_openai "github.com/wandxy/morph/internal/model/provider_openai"
	"github.com/wandxy/morph/internal/permissions"
	"github.com/wandxy/morph/internal/profile"
	morphruntime "github.com/wandxy/morph/internal/runtime"
	"github.com/wandxy/morph/internal/state/storememory"
	"github.com/wandxy/morph/pkg/logutils"
	"github.com/wandxy/morph/pkg/str"
	"google.golang.org/grpc"
)

type modelClientFactoryStub struct {
	newClient func(modelclient.ClientRequest) (models.Client, error)
}

type approvalAgentRunnerStub struct {
	*agentstub.AgentRunnerStub
	approvalService *permissions.ApprovalService
}

type browserServiceStub struct {
	close func(context.Context) error
}

type browserApprovalServiceStub struct {
	browserServiceStub
	approver permissions.Approver
}

type identityBinderStub struct {
	commandKey []byte
	browserKey []byte
}

func (s *identityBinderStub) SetCommandIdentityKey(key []byte) {
	s.commandKey = append([]byte(nil), key...)
}

func (s *identityBinderStub) SetAttachmentIdentityKey(key []byte) {
	s.browserKey = append([]byte(nil), key...)
}

func (s *identityBinderStub) Close(context.Context) error {
	return nil
}

func (s *browserApprovalServiceStub) SetApprover(approver permissions.Approver) {
	s.approver = approver
}

func (s browserServiceStub) Close(ctx context.Context) error {
	if s.close != nil {
		return s.close(ctx)
	}

	return nil
}

func (s approvalAgentRunnerStub) ApprovalService() *permissions.ApprovalService {
	return s.approvalService
}

func (s modelClientFactoryStub) NewClient(req modelclient.ClientRequest) (models.Client, error) {
	return s.newClient(req)
}

type gatewayManagerStub struct {
	start  func(context.Context, config.GatewayConfig, morphgateway.AgentService) error
	stop   func(context.Context) error
	status morphgateway.Status
}

func (s gatewayManagerStub) Start(ctx context.Context, cfg config.GatewayConfig, responder morphgateway.AgentService) error {
	if s.start != nil {
		return s.start(ctx, cfg, responder)
	}

	return nil
}

func (s gatewayManagerStub) Status() morphgateway.Status {
	return s.status
}

func (s gatewayManagerStub) Stop(ctx context.Context) error {
	if s.stop != nil {
		return s.stop(ctx)
	}

	return nil
}

func init() {
	logutils.SetOutput(io.Discard)
	daemonDependencies = testDaemonDependencies()
}

func TestNewCommand_BuildsConfigFromFlags(t *testing.T) {
	isolateCommandProfile(t)
	original := config.Get()
	originalNewAgentRunner := newAgentRunner
	originalServeGRPC := serveRPC
	originalOpenRPCListener := openRPCListener
	originalStartupOutput := startupOutput

	t.Cleanup(func() {
		config.Set(original)
		newAgentRunner = originalNewAgentRunner
		serveRPC = originalServeGRPC
		openRPCListener = originalOpenRPCListener
		startupOutput = originalStartupOutput
		logutils.SetOutput(io.Discard)
	})

	config.Set(nil)
	configFile := ""
	serverURL := newOpenRouterModelsServer(t, "gpt-4o-mini")
	runCalled := false
	serveCalled := false
	startupBuffer := &bytes.Buffer{}
	logBuffer := &bytes.Buffer{}
	startupOutput = startupBuffer
	logutils.SetOutput(logBuffer)

	newAgentRunner = func(_ context.Context, cfg *config.Config, modelClient, summaryClient, rerankerClient models.Client) agentRunner {
		return &agentstub.AgentRunnerStub{
			StartFunc: func(context.Context) error {
				runCalled = true
				return nil
			},
		}
	}

	serveRPC = func(ctx context.Context, cfg *config.Config, app agentRunner, _ net.Listener, _ gatewayManager) error {
		serveCalled = true
		require.Equal(t, "127.0.0.1", cfg.RPC.Address)
		require.Equal(t, 6000, cfg.RPC.Port)
		require.NotNil(t, app)
		return nil
	}
	openRPCListener = func(*config.Config) (net.Listener, error) {
		return noopListener{}, nil
	}

	cmd := newRootCommandForTest(&configFile)
	require.NoError(t, cmd.Run(context.Background(), []string{
		"morph",
		"--name", "flag-agent",
		"--model", "gpt-4o-mini",
		"--model.provider", "openrouter",
		"--model.api-key", "flag-key",
		"--model.base-url", serverURL,
		"--rpc.address", "127.0.0.1",
		"--rpc.port", "6000",
		"--trace.enabled",
		"--trace.disk.dir", "/tmp/morph-traces",
		"--log.level", "debug",
		"daemon",
	}))

	cfg := config.Get()
	require.Equal(t, "flag-agent", cfg.Name)
	require.Equal(t, "gpt-4o-mini", cfg.Models.Main.Name)
	require.Equal(t, "openrouter", cfg.Models.Main.Provider)
	require.Equal(t, "flag-key", cfg.Models.Providers["openrouter"].APIKey)
	require.Equal(t, serverURL, cfg.Models.Main.BaseURL)
	require.Equal(t, "127.0.0.1", cfg.RPC.Address)
	require.Equal(t, 6000, cfg.RPC.Port)
	require.True(t, cfg.Trace.Enabled)
	require.Equal(t, "/tmp/morph-traces", cfg.Trace.Disk.Dir)
	require.Equal(t, "debug", cfg.Log.Level)
	require.False(t, cfg.Log.NoColor)
	require.True(t, runCalled)
	require.True(t, serveCalled)

	startupOutput := stripANSI(startupBuffer.String())
	require.Regexp(t, regexp.MustCompile(`(?m)^ +│   Version: dev \(commit unknown\)$`), startupOutput)
	require.Regexp(t, regexp.MustCompile(`(?m)^ +│   Instance: flag-agent$`), startupOutput)
	require.Regexp(t, regexp.MustCompile(`(?m)^ +│   Profile: default$`), startupOutput)
	require.Contains(t, startupOutput, "Summary provider: openrouter")
	require.Contains(t, startupOutput, "Gateway: disabled")
	require.Contains(t, startupOutput, "Logs: debug (color)")
	require.Contains(t, startupBuffer.String(), "\x1b[38;5;38m")
	require.Contains(t, startupBuffer.String(), "\x1b[38;5;44m")
	require.Contains(t, startupBuffer.String(), "\x1b[38;5;49m")
	require.Contains(t, startupBuffer.String(), "\x1b[38;5;48m")
	require.Contains(t, startupBuffer.String(), "\x1b[38;5;83m")
	require.NotContains(t, startupBuffer.String(), "██   ██  █████  ███    ██ ██████")
	require.NotContains(t, startupBuffer.String(), AppDescription)
	require.Contains(t, startupBuffer.String(), "Version")
	require.Contains(t, startupBuffer.String(), "Instance")
	require.Contains(t, startupBuffer.String(), "Profile")
	require.Contains(t, startupBuffer.String(), "flag-agent")
	require.Contains(t, startupBuffer.String(), "Summary model")
	require.Contains(t, startupBuffer.String(), "gpt-4o-mini")
	require.Contains(t, startupBuffer.String(), "Summary provider")
	require.Contains(t, startupBuffer.String(), "Storage")
	require.Contains(t, startupBuffer.String(), "sqlite")
	require.Contains(t, startupBuffer.String(), "RPC")
	require.Contains(t, startupBuffer.String(), "127.0.0.1:6000")
	require.Contains(t, startupBuffer.String(), "Streaming")
	require.Contains(t, startupBuffer.String(), "true")
	require.Contains(t, startupBuffer.String(), "Debug requests")
	require.Contains(t, startupBuffer.String(), "enabled")
	require.Contains(t, startupBuffer.String(), "Traces")
	require.Contains(t, startupBuffer.String(), "enabled (/tmp/morph-traces)")
	require.Contains(t, startupBuffer.String(), "Safety")
	require.Contains(t, startupBuffer.String(), "input=enabled, output=enabled, pii=enabled")
	require.Contains(t, startupBuffer.String(), "Reranker")

	logOutput := stripANSI(logBuffer.String())
	require.Contains(t, logOutput, "Configuration loaded")
	require.Contains(t, logOutput, "Vector retrieval configured")
	require.Contains(t, logOutput, "Starting Morph services")
	require.NotContains(t, logOutput, "name=flag-agent")
	require.NotContains(t, logOutput, "model=gpt-4o-mini")
	require.NotContains(t, logOutput, "provider=openrouter")
	require.NotContains(t, logOutput, "summaryModel=gpt-4o-mini")
	require.NotContains(t, logOutput, "summaryProvider=openrouter")
	require.NotContains(t, logOutput, "storage=sqlite")
	require.NotContains(t, logOutput, "inputSafety=true")
	require.NotContains(t, logOutput, "outputSafety=true")
	require.NotContains(t, logOutput, "piiSafety=true")
	require.NotContains(t, logOutput, "rpcEndpoint=0.0.0.0:6000")
	require.NotContains(t, logOutput, "streaming=true")
	require.NotContains(t, logOutput, "traceEnabled=true")
	require.NotContains(t, logOutput, "traceDir=/tmp/morph-traces")
	require.NotContains(t, logOutput, "embeddingModel=")
	require.NotContains(t, logOutput, "embeddingProvider=")
	require.NotContains(t, logOutput, "reranker=")
	require.NotContains(t, logOutput, "service=morph")
	require.NotContains(t, logOutput, "rpcAddress=0.0.0.0 rpcEndpoint=0.0.0.0:6000 rpcPort=6000")
}

func TestNewCommand_RestartsDaemonWhenConfigFileChanges(t *testing.T) {
	isolateCommandProfile(t)
	stubOpenRPCListener(t)
	originalFactory := modelClientFactory
	originalRunner := newAgentRunner
	originalServe := serveRPC
	originalOutput := startupOutput
	originalDebounce := daemonConfigWatchDebounce
	events, _, restoreWatcher := stubConfigWatcher()
	t.Cleanup(func() {
		modelClientFactory = originalFactory
		newAgentRunner = originalRunner
		serveRPC = originalServe
		startupOutput = originalOutput
		daemonConfigWatchDebounce = originalDebounce
		restoreWatcher()
	})

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	writeUpTestConfig(t, configPath, "first")

	modelClientFactory = modelClientFactoryStub{
		newClient: func(modelclient.ClientRequest) (models.Client, error) {
			return &provider_openai.OpenAIClient{}, nil
		},
	}
	startupOutput = io.Discard
	daemonConfigWatchDebounce = 10 * time.Millisecond

	var runnersMu sync.Mutex
	runners := make([]*agentstub.AgentRunnerStub, 0, 2)
	newAgentRunner = func(context.Context, *config.Config, models.Client, models.Client, models.Client) agentRunner {
		runner := &agentstub.AgentRunnerStub{}
		runnersMu.Lock()
		runners = append(runners, runner)
		runnersMu.Unlock()
		return runner
	}

	servedNames := make(chan string, 2)
	firstServeStarted := make(chan struct{})
	serveRPC = func(ctx context.Context, cfg *config.Config, _ agentRunner, _ net.Listener, _ gatewayManager) error {
		servedNames <- cfg.Name
		if cfg.Name == "first" {
			close(firstServeStarted)
			<-ctx.Done()
		}

		return nil
	}

	configFile := ""
	cmd := newRootCommandForTest(&configFile)
	done := make(chan error, 1)
	go func() {
		done <- cmd.Run(context.Background(), []string{"morph", "--config", configPath, "daemon"})
	}()

	select {
	case <-firstServeStarted:
	case <-time.After(time.Second):
		t.Fatal("first daemon run did not start")
	}

	writeUpTestConfig(t, configPath, "second")
	events <- fsnotify.Event{Name: configPath, Op: fsnotify.Write}

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not restart after config change")
	}

	require.Equal(t, "first", <-servedNames)
	require.Equal(t, "second", <-servedNames)
	runnersMu.Lock()
	defer runnersMu.Unlock()

	require.Len(t, runners, 2)
	require.True(t, runners[0].Closed)
	require.True(t, runners[1].Closed)
}

func TestNewCommand_KeepsRunningWhenChangedConfigIsInvalid(t *testing.T) {
	isolateCommandProfile(t)
	stubOpenRPCListener(t)
	originalFactory := modelClientFactory
	originalRunner := newAgentRunner
	originalServe := serveRPC
	originalOutput := startupOutput
	originalDebounce := daemonConfigWatchDebounce
	events, _, restoreWatcher := stubConfigWatcher()
	t.Cleanup(func() {
		modelClientFactory = originalFactory
		newAgentRunner = originalRunner
		serveRPC = originalServe
		startupOutput = originalOutput
		daemonConfigWatchDebounce = originalDebounce
		restoreWatcher()
	})

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	writeUpTestConfig(t, configPath, "stable")

	modelClientFactory = modelClientFactoryStub{
		newClient: func(modelclient.ClientRequest) (models.Client, error) {
			return &provider_openai.OpenAIClient{}, nil
		},
	}
	newAgentRunner = func(context.Context, *config.Config, models.Client, models.Client, models.Client) agentRunner {
		return &agentstub.AgentRunnerStub{}
	}
	startupOutput = io.Discard
	daemonConfigWatchDebounce = 10 * time.Millisecond

	serveStarted := make(chan struct{})
	serveDone := make(chan struct{})
	serveCalls := make(chan string, 2)
	serveRPC = func(ctx context.Context, cfg *config.Config, _ agentRunner, _ net.Listener, _ gatewayManager) error {
		serveCalls <- cfg.Name
		close(serveStarted)
		<-ctx.Done()
		close(serveDone)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	configFile := ""
	cmd := newRootCommandForTest(&configFile)
	done := make(chan error, 1)
	go func() {
		done <- cmd.Run(ctx, []string{"morph", "--config", configPath, "daemon"})
	}()

	select {
	case <-serveStarted:
	case <-time.After(time.Second):
		t.Fatal("daemon did not start")
	}
	select {
	case name := <-serveCalls:
		require.Equal(t, "stable", name)
	default:
		t.Fatal("initial daemon run was not recorded")
	}

	require.NoError(t, os.WriteFile(configPath, []byte("name: ["), 0o600))
	events <- fsnotify.Event{Name: configPath, Op: fsnotify.Write}
	require.Never(t, func() bool {
		select {
		case <-serveCalls:
			return true
		default:
			return false
		}
	}, 100*time.Millisecond, 10*time.Millisecond, "invalid config restarted daemon")

	cancel()
	select {
	case <-serveDone:
	case <-time.After(time.Second):
		t.Fatal("daemon did not stop after context cancellation")
	}
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("command did not return after context cancellation")
	}
}

func TestRenderStartupPanel_DisablesColorWhenRequested(t *testing.T) {
	output := renderStartupPanel(&config.Config{
		Name:   "daemon",
		Models: config.ModelsConfig{Main: config.MainModelConfig{Name: "gpt-4o-mini", Provider: "openrouter", Stream: new(false)}},
		RPC:    config.RPCConfig{Address: "127.0.0.1", Port: 50051},
		Log:    config.LogConfig{Level: "info", NoColor: true},
		Debug:  config.DebugConfig{Requests: true},
		Trace:  config.TraceConfig{Enabled: true, Disk: config.TraceDiskConfig{Dir: "/tmp/morph-traces"}},
	})

	require.NotContains(t, output, "\x1b[90m")
	require.NotContains(t, output, "\x1b[38;5;")
	require.Regexp(t, regexp.MustCompile(`(?m)^ +│   Version: dev \(commit unknown\)$`), output)
	require.Regexp(t, regexp.MustCompile(`(?m)^ +│   Instance: daemon$`), output)
	require.Regexp(t, regexp.MustCompile(`(?m)^ +│   Profile: default$`), output)
	require.Contains(t, output, "Summary model: gpt-4o-mini")
	require.Contains(t, output, "RPC: 127.0.0.1:50051")
	require.NotContains(t, output, AppDescription)
	require.Contains(t, output, "Instance: daemon")
	require.Contains(t, output, "Profile: default")
	require.Contains(t, output, "Summary model: gpt-4o-mini")
	require.Contains(t, output, "Summary provider: openrouter")
	require.Contains(t, output, "Storage: sqlite")
	require.Contains(t, output, "Streaming: false")
	require.Contains(t, output, "Gateway: disabled")
	require.Contains(t, output, "Debug requests: enabled")
	require.Contains(t, output, "Traces: enabled (/tmp/morph-traces)")
	require.Contains(t, output, "Safety: input=enabled, output=enabled, pii=enabled")
	require.NotContains(t, output, "Ready to accept RPC connections.")
}

func TestRenderStartupPanel_IncludesActiveProfile(t *testing.T) {
	original := profile.Active()
	profile.SetActive(profile.Profile{Name: "work"})
	t.Cleanup(func() {
		profile.SetActive(original)
	})

	output := renderStartupPanel(&config.Config{
		Name:   "daemon",
		Models: config.ModelsConfig{Main: config.MainModelConfig{Name: "gpt-4o-mini", Provider: "openrouter"}},
		Log:    config.LogConfig{NoColor: true},
	})

	require.Contains(t, output, "Profile: work")
}

func TestRenderStartupPanel_WarnsWhenFullAccessIsEnabled(t *testing.T) {
	output := renderStartupPanel(&config.Config{
		Name:        "daemon",
		Models:      config.ModelsConfig{Main: config.MainModelConfig{Name: "gpt-4o-mini", Provider: "openrouter"}},
		Log:         config.LogConfig{NoColor: true},
		Permissions: permissions.Policy{Preset: permissions.PresetFullAccess},
	})

	require.Contains(t, output, "Permissions: full_access (UNSAFE: command and filesystem guardrails bypassed)")
}

func TestRenderStartupPanel_DescribesPermissionPresets(t *testing.T) {
	cfg := &config.Config{
		Name: "daemon",
		Models: config.ModelsConfig{Main: config.MainModelConfig{
			Name: "gpt-4o-mini", Provider: "openrouter",
		}},
		Log: config.LogConfig{NoColor: true},
	}

	cfg.Permissions = permissions.Policy{
		Preset: permissions.PresetApproveForMe,
		Rules:  []permissions.Rule{{Name: "allow clock", Decision: permissions.DecisionAllow}},
	}
	require.Contains(t, renderStartupPanel(cfg), "Permissions: Approve for me (customized)")

	cfg.Permissions = permissions.Policy{Preset: permissions.PresetCustom}
	require.Contains(t, renderStartupPanel(cfg), "Permissions: Custom")
}

func TestGetStartupProfileName_DefaultsWhenActiveProfileIsEmpty(t *testing.T) {
	original := profile.Active()
	profile.SetActive(profile.Profile{})
	t.Cleanup(func() {
		profile.SetActive(original)
	})

	require.Equal(t, profile.DefaultName, getStartupProfileName())
}

func TestRenderStartupPanel_IncludesGatewaySummaryWithoutSecrets(t *testing.T) {
	output := renderStartupPanel(&config.Config{
		Name:   "daemon",
		Models: config.ModelsConfig{Main: config.MainModelConfig{Name: "gpt-4o-mini", Provider: "openrouter"}},
		RPC:    config.RPCConfig{Address: "127.0.0.1", Port: 50051},
		Gateway: config.GatewayConfig{
			Enabled:   true,
			Address:   "127.0.0.1",
			Port:      50052,
			AuthToken: "MORPH_GATEWAY_AUTH_TOKEN",
			Telegram: config.GatewayTelegramConfig{
				Enabled:  true,
				Mode:     config.GatewayTelegramModePolling,
				BotToken: "MORPH_GATEWAY_TELEGRAM_BOT_TOKEN",
			},
			Slack: config.GatewaySlackConfig{
				Enabled:  true,
				Mode:     config.GatewaySlackModeSocket,
				BotToken: "MORPH_GATEWAY_SLACK_BOT_TOKEN",
				AppToken: "MORPH_GATEWAY_SLACK_APP_TOKEN",
			},
		},
		Log: config.LogConfig{Level: "info", NoColor: true},
	})

	require.Contains(t, output, "Gateway: 127.0.0.1:50052 telegram=polling slack=socket")
	require.NotContains(t, output, "MORPH_GATEWAY_AUTH_TOKEN")
	require.NotContains(t, output, "MORPH_GATEWAY_TELEGRAM_BOT_TOKEN")
	require.NotContains(t, output, "MORPH_GATEWAY_SLACK_BOT_TOKEN")
	require.NotContains(t, output, "MORPH_GATEWAY_SLACK_APP_TOKEN")
}

func TestRenderStartupPanel_IncludesSafetyMode(t *testing.T) {
	inputSafety := false
	outputSafety := true
	piiSafety := true
	output := renderStartupPanel(&config.Config{
		Name:   "daemon",
		Models: config.ModelsConfig{Main: config.MainModelConfig{Name: "gpt-4o-mini", Provider: "openrouter"}},
		RPC:    config.RPCConfig{Address: "127.0.0.1", Port: 50051},
		Log:    config.LogConfig{Level: "info", NoColor: true},
		Safety: config.SafetyConfig{
			Input:  &inputSafety,
			Output: &outputSafety,
			PII:    &piiSafety,
		},
	})

	require.Contains(t, output, "Safety: input=disabled, output=enabled, pii=enabled")
}

func TestRenderStartupPanel_WarnsWhenUnsafeEvidenceRetentionIsEnabled(t *testing.T) {
	output := renderStartupPanel(&config.Config{
		Name:   "daemon",
		Models: config.ModelsConfig{Main: config.MainModelConfig{Name: "gpt-4o-mini", Provider: "openrouter"}},
		Log:    config.LogConfig{NoColor: true},
		Dev:    config.DevConfig{RetainUnsafe: true},
	})

	require.Contains(t, output, "Unsafe evidence: PLAINTEXT; same-user/full-access readable; cleanup is manual")
}

func TestRenderStartupPanel_IncludesEmbeddingModelWhenVectorEnabled(t *testing.T) {
	rerankDisabled := false
	output := renderStartupPanel(&config.Config{
		Name: "daemon",
		Models: config.ModelsConfig{
			Main:      config.MainModelConfig{Name: "gpt-4o-mini", Provider: "openrouter"},
			Embedding: config.EmbeddingModelConfig{Name: "text-embedding-3-small", Provider: "openai"},
		},
		Search:   config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true}},
		RPC:      config.RPCConfig{Address: "127.0.0.1", Port: 50051},
		Log:      config.LogConfig{Level: "info", NoColor: true},
		Reranker: config.RerankerConfig{Enabled: &rerankDisabled},
	})

	require.Contains(t, output, "Embedding model: text-embedding-3-small")
	require.Contains(t, output, "Embedding provider: openai")
	require.Contains(t, output, "Reranker: deterministic")
}

func TestRenderStartupPanel_HidesEmbeddingModelWhenVectorDisabled(t *testing.T) {
	output := renderStartupPanel(&config.Config{
		Name: "daemon",
		Models: config.ModelsConfig{
			Main:      config.MainModelConfig{Name: "gpt-4o-mini", Provider: "openrouter"},
			Embedding: config.EmbeddingModelConfig{Name: "text-embedding-3-small"},
		},
		RPC: config.RPCConfig{Address: "127.0.0.1", Port: 50051},
		Log: config.LogConfig{Level: "info", NoColor: true},
	})

	require.NotContains(t, output, "Embedding model")
	require.NotContains(t, output, "Embedding provider")
}

func TestRenderStartupPanel_IncludesStorageBackend(t *testing.T) {
	output := renderStartupPanel(&config.Config{
		Name:    "daemon",
		Models:  config.ModelsConfig{Main: config.MainModelConfig{Name: "gpt-4o-mini", Provider: "openrouter"}},
		Storage: config.StorageConfig{Backend: "memory"},
		RPC:     config.RPCConfig{Address: "127.0.0.1", Port: 50051},
		Log:     config.LogConfig{Level: "info", NoColor: true},
	})

	require.Contains(t, output, "Storage: memory")
}

func TestRenderStartupPanel_IncludesEffectiveSummaryModelAndProvider(t *testing.T) {
	output := renderStartupPanel(&config.Config{
		Name: "daemon",
		Models: config.ModelsConfig{
			Main:    config.MainModelConfig{Name: "gpt-4o-mini", Provider: "openrouter"},
			Summary: config.SummaryModelConfig{Name: "gpt-4o-mini", Provider: "openai"},
		},
		RPC: config.RPCConfig{Address: "127.0.0.1", Port: 50051},
		Log: config.LogConfig{Level: "info", NoColor: true},
	})

	require.Contains(t, output, "Summary model: gpt-4o-mini")
	require.Contains(t, output, "Summary provider: openai")
}

func TestSetOutput_SwitchesWriterAndRestoresPrevious(t *testing.T) {
	stdout := SetOutput(nil)
	require.Equal(t, io.Discard, startupOutput)
	t.Cleanup(func() { SetOutput(stdout) })

	buf := &bytes.Buffer{}
	prev := SetOutput(buf)
	require.Equal(t, io.Discard, prev)
	require.Equal(t, buf, startupOutput)

	restored := SetOutput(stdout)
	require.Equal(t, buf, restored)
	require.Equal(t, stdout, startupOutput)
}

func TestRenderStartupPanel_NilConfigReturnsBadgeOnly(t *testing.T) {
	out := renderStartupPanel(nil)
	require.Equal(t, getStartupBadge(), out)
}

func TestStartupBadgeAlignsMarkAndText(t *testing.T) {
	badge := joinStartupBanner(brand.Mark, "Morph\n1.2.3 (commit abc123)")
	lines := strings.Split(badge, "\n")

	require.Len(t, lines, 5)
	for _, line := range lines {
		require.Equal(t, len([]rune(lines[0])), len([]rune(line)))
	}
	require.Contains(t, lines[1], "Morph")
	require.Contains(t, lines[2], "1.2.3 (commit abc123)")
	require.Contains(t, lines[2], "░████████")
}

func TestRenderStartupPanel_IncludesSummaryProviderAndAPIWhenDistinct(t *testing.T) {
	cfg := &config.Config{
		Name: "daemon",
		Models: config.ModelsConfig{
			Main:    config.MainModelConfig{Name: "gpt-4o-mini", Provider: "openrouter", API: modelprovider.APIOpenAICompletions},
			Summary: config.SummaryModelConfig{Provider: "openai", API: modelprovider.APIOpenAIResponses},
		},
		RPC: config.RPCConfig{Address: "127.0.0.1", Port: 50051},
		Log: config.LogConfig{Level: "info", NoColor: true},
	}
	cfg.Normalize()

	out := renderStartupPanel(cfg)
	require.Contains(t, out, "Summary provider: openai")
	require.Contains(t, out, "Summary API: openai-responses")
}

func TestOpenRPCListener_ReturnsListenError(t *testing.T) {
	orig := listenFunc
	t.Cleanup(func() { listenFunc = orig })

	listenFunc = func(string, string) (net.Listener, error) {
		return nil, errors.New("listen boom")
	}

	_, err := openRPCListener(&config.Config{
		RPC: config.RPCConfig{Address: "127.0.0.1", Port: 50051},
	})

	require.EqualError(t, err, "listen boom")
}

func TestConfigFileFingerprintHandlesMissingAndChangedFiles(t *testing.T) {
	originalStat := osStat
	t.Cleanup(func() { osStat = originalStat })

	_, err := getConfigFileFingerprint(" ")
	require.EqualError(t, err, "config path is required")

	missingPath := filepath.Join(t.TempDir(), "missing.yaml")
	fingerprint, err := getConfigFileFingerprint(missingPath)
	require.NoError(t, err)
	require.Equal(t, configFileFingerprint{}, fingerprint)

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	writeUpTestConfig(t, configPath, "first")

	fingerprint, err = getConfigFileFingerprint(configPath)
	require.NoError(t, err)
	require.NotEqual(t, configFileFingerprint{}, fingerprint)

	current, changed, err := hasConfigFileChanged(configPath, fingerprint)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, fingerprint, current)

	writeUpTestConfig(t, configPath, "second")

	current, changed, err = hasConfigFileChanged(configPath, fingerprint)
	require.NoError(t, err)
	require.True(t, changed)
	require.NotEqual(t, fingerprint, current)

	osStat = func(string) (os.FileInfo, error) {
		return nil, errors.New("stat failed")
	}
	_, err = getConfigFileFingerprint(configPath)
	require.EqualError(t, err, "stat failed")
	_, _, err = hasConfigFileChanged(configPath, fingerprint)
	require.EqualError(t, err, "stat failed")
}

func TestNewFSNotifyConfigWatcherWatchesConfigDirectory(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	watcher, err := newFSNotifyConfigWatcher(configPath)
	require.NoError(t, err)
	require.NotNil(t, watcher.events)
	require.NotNil(t, watcher.errors)
	require.NoError(t, watcher.close())

	_, err = newFSNotifyConfigWatcher(" ")
	require.EqualError(t, err, "config path is required")
}

func TestNewFSNotifyConfigWatcherReturnsSetupErrors(t *testing.T) {
	originalCreate := createFSNotifyWatcher
	originalMkdir := mkdirAllConfigWatchDir
	originalAdd := addConfigWatchDir
	t.Cleanup(func() {
		createFSNotifyWatcher = originalCreate
		mkdirAllConfigWatchDir = originalMkdir
		addConfigWatchDir = originalAdd
	})

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	createFSNotifyWatcher = func() (*fsnotify.Watcher, error) {
		return nil, errors.New("create watcher failed")
	}
	_, err := newFSNotifyConfigWatcher(configPath)
	require.EqualError(t, err, "create watcher failed")

	createFSNotifyWatcher = originalCreate
	mkdirAllConfigWatchDir = func(string, os.FileMode) error {
		return errors.New("mkdir failed")
	}
	_, err = newFSNotifyConfigWatcher(configPath)
	require.EqualError(t, err, "mkdir failed")

	mkdirAllConfigWatchDir = originalMkdir
	addConfigWatchDir = func(*fsnotify.Watcher, string) error {
		return errors.New("add watch failed")
	}
	_, err = newFSNotifyConfigWatcher(configPath)
	require.EqualError(t, err, "add watch failed")
}

func TestIsConfigFileWatchEventFiltersPathAndOperation(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")

	require.True(t, isConfigFileWatchEvent(fsnotify.Event{Name: configPath, Op: fsnotify.Write}, configPath))
	require.True(t, isConfigFileWatchEvent(fsnotify.Event{Name: configPath, Op: fsnotify.Create}, configPath))
	require.True(t, isConfigFileWatchEvent(fsnotify.Event{Name: configPath, Op: fsnotify.Rename}, configPath))
	require.True(t, isConfigFileWatchEvent(fsnotify.Event{Name: configPath, Op: fsnotify.Remove}, configPath))
	require.False(t, isConfigFileWatchEvent(fsnotify.Event{Name: "", Op: fsnotify.Write}, configPath))
	require.False(t, isConfigFileWatchEvent(fsnotify.Event{Name: filepath.Join(filepath.Dir(configPath), "other.yaml"), Op: fsnotify.Write}, configPath))
	require.False(t, isConfigFileWatchEvent(fsnotify.Event{Name: configPath, Op: fsnotify.Chmod}, configPath))
}

func TestRunDaemonWithConfigRestartsReturnsConfigFingerprintError(t *testing.T) {
	isolateCommandProfile(t)
	originalStat := osStat
	t.Cleanup(func() { osStat = originalStat })

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	writeUpTestConfig(t, configPath, "daemon")
	osStat = func(string) (os.FileInfo, error) {
		return nil, errors.New("stat failed")
	}

	err := runParsedDaemonCommand(t, []string{"--config", configPath, "daemon"}, func(ctx context.Context, cmd *urfavecli.Command) error {
		return runDaemonWithConfigRestarts(ctx, cmd, 0)
	})

	require.EqualError(t, err, "stat failed")
}

func TestRunDaemonUntilConfigChangeReturnsWatcherSetupErrorBeforeStartingDaemon(t *testing.T) {
	restore := stubDaemonStartup(t)
	defer restore()
	originalWatcher := newConfigWatcher
	t.Cleanup(func() { newConfigWatcher = originalWatcher })

	newConfigWatcher = func(string) (configWatcher, error) {
		return configWatcher{}, errors.New("watcher failed")
	}
	serveRPC = func(_ context.Context, _ *config.Config, _ agentRunner, _ net.Listener, _ gatewayManager) error {
		t.Fatal("serveRPC should not run when config watcher setup fails")
		return nil
	}

	snapshot := daemonConfigSnapshot{
		cfg:    newUpTestConfig("daemon"),
		inputs: ConfigInputs{ConfigPath: filepath.Join(t.TempDir(), "config.yaml")},
	}
	_, restart, err := runDaemonUntilConfigChange(context.Background(), nil, snapshot, 10*time.Millisecond)

	require.False(t, restart)
	require.EqualError(t, err, "watcher failed")
}

func TestRunDaemonUntilConfigChangeReturnsDaemonError(t *testing.T) {
	restore := stubDaemonStartup(t)
	defer restore()
	_, _, restoreWatcher := stubConfigWatcher()
	defer restoreWatcher()

	serveRPC = func(_ context.Context, _ *config.Config, _ agentRunner, _ net.Listener, _ gatewayManager) error {
		return errors.New("serve failed")
	}

	snapshot := daemonConfigSnapshot{
		cfg:    newUpTestConfig("daemon"),
		inputs: ConfigInputs{ConfigPath: filepath.Join(t.TempDir(), "config.yaml")},
	}
	_, restart, err := runDaemonUntilConfigChange(context.Background(), nil, snapshot, 10*time.Millisecond)

	require.False(t, restart)
	require.EqualError(t, err, "serve failed")
}

func TestRunDaemonUntilConfigChangeReturnsStopErrorAfterContextCancel(t *testing.T) {
	restore := stubDaemonStartup(t)
	defer restore()
	_, _, restoreWatcher := stubConfigWatcher()
	defer restoreWatcher()

	serveStarted := make(chan struct{})
	serveRPC = func(ctx context.Context, _ *config.Config, _ agentRunner, _ net.Listener, _ gatewayManager) error {
		close(serveStarted)
		<-ctx.Done()
		return errors.New("stop failed")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		snapshot := daemonConfigSnapshot{
			cfg:    newUpTestConfig("daemon"),
			inputs: ConfigInputs{ConfigPath: filepath.Join(t.TempDir(), "config.yaml")},
		}
		_, _, err := runDaemonUntilConfigChange(ctx, nil, snapshot, 10*time.Millisecond)
		done <- err
	}()

	select {
	case <-serveStarted:
	case <-time.After(time.Second):
		t.Fatal("daemon run did not start")
	}
	cancel()

	select {
	case err := <-done:
		require.EqualError(t, err, "stop failed")
	case <-time.After(time.Second):
		t.Fatal("daemon run did not stop")
	}
}

func TestRunDaemonUntilConfigChangeIgnoresConfigStatError(t *testing.T) {
	restore := stubDaemonStartup(t)
	defer restore()
	events, _, restoreWatcher := stubConfigWatcher()
	defer restoreWatcher()

	originalStat := osStat
	t.Cleanup(func() { osStat = originalStat })
	osStat = func(string) (os.FileInfo, error) {
		return nil, errors.New("stat failed")
	}

	serveStarted := make(chan struct{})
	serveRPC = func(ctx context.Context, _ *config.Config, _ agentRunner, _ net.Listener, _ gatewayManager) error {
		close(serveStarted)
		<-ctx.Done()
		return nil
	}

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		snapshot := daemonConfigSnapshot{
			cfg:    newUpTestConfig("daemon"),
			inputs: ConfigInputs{ConfigPath: configPath},
		}
		_, _, err := runDaemonUntilConfigChange(ctx, nil, snapshot, 10*time.Millisecond)
		done <- err
	}()

	select {
	case <-serveStarted:
	case <-time.After(time.Second):
		t.Fatal("daemon run did not start")
	}
	events <- fsnotify.Event{Name: configPath, Op: fsnotify.Write}
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("daemon run did not stop")
	}
}

func TestRunDaemonUntilConfigChangeHandlesWatcherNoise(t *testing.T) {
	restore := stubDaemonStartup(t)
	defer restore()
	events, errorsCh, restoreWatcher := stubConfigWatcher()
	defer restoreWatcher()

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	writeUpTestConfig(t, configPath, "daemon")
	fingerprint, err := getConfigFileFingerprint(configPath)
	require.NoError(t, err)

	serveStarted := make(chan struct{})
	serveRPC = func(ctx context.Context, _ *config.Config, _ agentRunner, _ net.Listener, _ gatewayManager) error {
		close(serveStarted)
		<-ctx.Done()
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		snapshot := daemonConfigSnapshot{
			cfg:         newUpTestConfig("daemon"),
			inputs:      ConfigInputs{ConfigPath: configPath},
			fingerprint: fingerprint,
		}
		_, _, err := runDaemonUntilConfigChange(ctx, nil, snapshot, 10*time.Millisecond)
		done <- err
	}()

	select {
	case <-serveStarted:
	case <-time.After(time.Second):
		t.Fatal("daemon run did not start")
	}

	errorsCh <- errors.New("watch failed")
	events <- fsnotify.Event{Name: filepath.Join(filepath.Dir(configPath), "other.yaml"), Op: fsnotify.Write}
	events <- fsnotify.Event{Name: configPath, Op: fsnotify.Write}
	close(errorsCh)
	close(events)
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("daemon run did not stop")
	}
}

func TestConfigReloadTimerResetAndStop(t *testing.T) {
	timer, reload := resetConfigReloadTimer(nil, 0)
	require.NotNil(t, timer)
	require.NotNil(t, reload)

	timer, reload = resetConfigReloadTimer(timer, time.Hour)
	require.NotNil(t, timer)
	require.NotNil(t, reload)

	timer.Reset(0)
	select {
	case <-timer.C:
	case <-time.After(time.Second):
		t.Fatal("timer did not fire")
	}

	timer, reload = resetConfigReloadTimer(timer, time.Hour)
	require.NotNil(t, timer)
	require.NotNil(t, reload)
	stopConfigReloadTimer(timer)

	expiredForReset := time.NewTimer(0)
	time.Sleep(10 * time.Millisecond)
	timer, reload = resetConfigReloadTimer(expiredForReset, time.Hour)
	require.NotNil(t, timer)
	require.NotNil(t, reload)
	stopConfigReloadTimer(timer)

	expiredForStop := time.NewTimer(0)
	time.Sleep(10 * time.Millisecond)
	stopConfigReloadTimer(expiredForStop)

	stoppedForStop := time.NewTimer(time.Hour)
	require.True(t, stoppedForStop.Stop())
	stopConfigReloadTimer(stoppedForStop)
	stopConfigReloadTimer(nil)
}

func TestRunDaemonUntilConfigChangeReturnsShutdownErrorOnRestart(t *testing.T) {
	restore := stubDaemonStartup(t)
	defer restore()
	events, _, restoreWatcher := stubConfigWatcher()
	defer restoreWatcher()

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	writeUpTestConfig(t, configPath, "first")
	fingerprint, err := getConfigFileFingerprint(configPath)
	require.NoError(t, err)

	serveStarted := make(chan struct{})
	serveRPC = func(ctx context.Context, _ *config.Config, _ agentRunner, _ net.Listener, _ gatewayManager) error {
		close(serveStarted)
		<-ctx.Done()
		return errors.New("shutdown failed")
	}

	done := make(chan error, 1)
	err = runParsedDaemonCommand(t, []string{"--config", configPath, "daemon"}, func(ctx context.Context, cmd *urfavecli.Command) error {
		go func() {
			snapshot := daemonConfigSnapshot{
				cfg:         newUpTestConfig("first"),
				inputs:      ConfigInputs{ConfigPath: configPath},
				fingerprint: fingerprint,
			}
			_, _, err := runDaemonUntilConfigChange(ctx, cmd, snapshot, 10*time.Millisecond)
			done <- err
		}()

		select {
		case <-serveStarted:
		case <-time.After(time.Second):
			t.Fatal("daemon run did not start")
		}
		writeUpTestConfig(t, configPath, "second")
		events <- fsnotify.Event{Name: configPath, Op: fsnotify.Write}

		select {
		case err := <-done:
			return err
		case <-time.After(time.Second):
			t.Fatal("daemon run did not stop")
			return nil
		}
	})

	require.EqualError(t, err, "shutdown failed")
}

func TestRunDaemonOnceClosesAgentAndReturnsCloseError(t *testing.T) {
	isolateCommandProfile(t)
	stubOpenRPCListener(t)
	originalFactory := modelClientFactory
	originalRunner := newAgentRunner
	originalServe := serveRPC
	originalOutput := startupOutput
	t.Cleanup(func() {
		modelClientFactory = originalFactory
		newAgentRunner = originalRunner
		serveRPC = originalServe
		startupOutput = originalOutput
	})

	modelClientFactory = modelClientFactoryStub{
		newClient: func(modelclient.ClientRequest) (models.Client, error) {
			return &provider_openai.OpenAIClient{}, nil
		},
	}
	startupOutput = io.Discard

	runner := &agentstub.AgentRunnerStub{
		AgentServiceStub: agentstub.AgentServiceStub{CloseErr: errors.New("close failed")},
	}
	newAgentRunner = func(context.Context, *config.Config, models.Client, models.Client, models.Client) agentRunner {
		return runner
	}
	serveRPC = func(_ context.Context, _ *config.Config, _ agentRunner, _ net.Listener, _ gatewayManager) error {
		return nil
	}

	cfg := newUpTestConfig("daemon")
	err := runDaemonOnce(context.Background(), cfg)

	require.EqualError(t, err, "close failed")
	require.True(t, runner.Closed)
}

func TestRunDaemonOnceIgnoresMissingCredentialLockCloseError(t *testing.T) {
	isolateCommandProfile(t)
	stubOpenRPCListener(t)
	originalFactory := modelClientFactory
	originalRunner := newAgentRunner
	originalServe := serveRPC
	originalOutput := startupOutput
	t.Cleanup(func() {
		modelClientFactory = originalFactory
		newAgentRunner = originalRunner
		serveRPC = originalServe
		startupOutput = originalOutput
	})

	modelClientFactory = modelClientFactoryStub{
		newClient: func(modelclient.ClientRequest) (models.Client, error) {
			return &provider_openai.OpenAIClient{}, nil
		},
	}
	startupOutput = io.Discard

	runner := &agentstub.AgentRunnerStub{
		AgentServiceStub: agentstub.AgentServiceStub{
			CloseErr: &os.PathError{
				Op:   "lstat",
				Path: filepath.Join(t.TempDir(), "auth.json.lock"),
				Err:  os.ErrNotExist,
			},
		},
	}
	newAgentRunner = func(context.Context, *config.Config, models.Client, models.Client, models.Client) agentRunner {
		return runner
	}
	serveRPC = func(_ context.Context, _ *config.Config, _ agentRunner, _ net.Listener, _ gatewayManager) error {
		return nil
	}

	err := runDaemonOnce(context.Background(), newUpTestConfig("daemon"))

	require.NoError(t, err)
	require.True(t, runner.Closed)
}

func TestRunDaemonOnceReturnsRPCListenerErrorBeforeStartingAgent(t *testing.T) {
	isolateCommandProfile(t)
	originalFactory := modelClientFactory
	originalRunner := newAgentRunner
	originalOpenRPCListener := openRPCListener
	originalServe := serveRPC
	originalOutput := startupOutput
	t.Cleanup(func() {
		modelClientFactory = originalFactory
		newAgentRunner = originalRunner
		openRPCListener = originalOpenRPCListener
		serveRPC = originalServe
		startupOutput = originalOutput
	})

	modelClientFactory = modelClientFactoryStub{
		newClient: func(modelclient.ClientRequest) (models.Client, error) {
			return &provider_openai.OpenAIClient{}, nil
		},
	}
	openRPCListener = func(*config.Config) (net.Listener, error) {
		return nil, errors.New("listen failed")
	}
	startupOutput = io.Discard

	newAgentRunner = func(context.Context, *config.Config, models.Client, models.Client, models.Client) agentRunner {
		t.Fatal("agent runner should not be created when RPC listener setup fails")
		return nil
	}
	serveRPC = func(_ context.Context, _ *config.Config, _ agentRunner, _ net.Listener, _ gatewayManager) error {
		t.Fatal("serveRPC should not run when RPC listener setup fails")
		return nil
	}

	err := runDaemonOnce(context.Background(), newUpTestConfig("daemon"))

	require.EqualError(t, err, "listen failed")
}

func TestRunDaemonOnceKeepsServeErrorWhenCloseAlsoFails(t *testing.T) {
	isolateCommandProfile(t)
	stubOpenRPCListener(t)
	originalFactory := modelClientFactory
	originalRunner := newAgentRunner
	originalServe := serveRPC
	originalOutput := startupOutput
	t.Cleanup(func() {
		modelClientFactory = originalFactory
		newAgentRunner = originalRunner
		serveRPC = originalServe
		startupOutput = originalOutput
	})

	modelClientFactory = modelClientFactoryStub{
		newClient: func(modelclient.ClientRequest) (models.Client, error) {
			return &provider_openai.OpenAIClient{}, nil
		},
	}
	startupOutput = io.Discard

	runner := &agentstub.AgentRunnerStub{
		AgentServiceStub: agentstub.AgentServiceStub{CloseErr: errors.New("close failed")},
	}
	newAgentRunner = func(context.Context, *config.Config, models.Client, models.Client, models.Client) agentRunner {
		return runner
	}
	serveRPC = func(_ context.Context, _ *config.Config, _ agentRunner, _ net.Listener, _ gatewayManager) error {
		return errors.New("serve failed")
	}

	cfg := newUpTestConfig("daemon")
	err := runDaemonOnce(context.Background(), cfg)

	require.EqualError(t, err, "serve failed")
	require.True(t, runner.Closed)
}

func TestRunDaemonOnceReturnsSummaryClientFactoryError(t *testing.T) {
	isolateCommandProfile(t)
	t.Setenv("OPENAI_API_KEY", "openai-key")
	originalFactory := modelClientFactory
	originalServe := serveRPC
	originalOutput := startupOutput
	t.Cleanup(func() {
		modelClientFactory = originalFactory
		serveRPC = originalServe
		startupOutput = originalOutput
	})

	var calls int
	modelClientFactory = modelClientFactoryStub{
		newClient: func(modelclient.ClientRequest) (models.Client, error) {
			calls++
			if calls == 2 {
				return nil, errors.New("summary client failed")
			}
			return &provider_openai.OpenAIClient{}, nil
		},
	}
	serveRPC = func(_ context.Context, _ *config.Config, _ agentRunner, _ net.Listener, _ gatewayManager) error {
		t.Fatal("serveRPC should not run")
		return nil
	}
	startupOutput = io.Discard

	cfg := newUpTestConfig("daemon")
	cfg.Models.Summary.Provider = "openai"
	cfg.Models.Summary.BaseURL = "https://openai.example/v1"
	cfg.Normalize()

	err := runDaemonOnce(context.Background(), cfg)

	require.EqualError(t, err, "summary client failed")
	require.Equal(t, 2, calls)
}

func TestRunDaemonOnceReturnsRerankerClientFactoryError(t *testing.T) {
	isolateCommandProfile(t)
	originalFactory := modelClientFactory
	originalResolve := resolveRerankerAuth
	originalServe := serveRPC
	originalOutput := startupOutput
	t.Cleanup(func() {
		modelClientFactory = originalFactory
		resolveRerankerAuth = originalResolve
		serveRPC = originalServe
		startupOutput = originalOutput
	})

	var calls int
	modelClientFactory = modelClientFactoryStub{
		newClient: func(modelclient.ClientRequest) (models.Client, error) {
			calls++
			if calls == 2 {
				return nil, errors.New("reranker client failed")
			}
			return &provider_openai.OpenAIClient{}, nil
		},
	}
	resolveRerankerAuth = func(*config.Config) (config.ModelAuth, error) {
		return config.ModelAuth{Provider: "anthropic", API: modelprovider.APIAnthropicMessages, APIKey: "reranker-key"}, nil
	}
	serveRPC = func(_ context.Context, _ *config.Config, _ agentRunner, _ net.Listener, _ gatewayManager) error {
		t.Fatal("serveRPC should not run")
		return nil
	}
	startupOutput = io.Discard

	cfg := newUpTestConfig("daemon")
	cfg.Search.Vector.Enabled = true
	cfg.Reranker.Type = constants.RerankerLLM
	cfg.Normalize()

	err := runDaemonOnce(context.Background(), cfg)

	require.EqualError(t, err, "reranker client failed")
	require.Equal(t, 2, calls)
}

func TestRunDaemonOnceStartsWhenRerankerAuthFails(t *testing.T) {
	isolateCommandProfile(t)
	stubOpenRPCListener(t)
	originalFactory := modelClientFactory
	originalResolve := resolveRerankerAuth
	originalServe := serveRPC
	originalOutput := startupOutput
	t.Cleanup(func() {
		modelClientFactory = originalFactory
		resolveRerankerAuth = originalResolve
		serveRPC = originalServe
		startupOutput = originalOutput
	})

	modelClientFactory = modelClientFactoryStub{
		newClient: func(modelclient.ClientRequest) (models.Client, error) {
			return &provider_openai.OpenAIClient{}, nil
		},
	}
	resolveRerankerAuth = func(*config.Config) (config.ModelAuth, error) {
		return config.ModelAuth{}, errors.New("reranker auth failed")
	}
	served := false
	serveRPC = func(_ context.Context, _ *config.Config, _ agentRunner, _ net.Listener, _ gatewayManager) error {
		served = true
		return nil
	}
	startupOutput = io.Discard

	cfg := newUpTestConfig("daemon")
	cfg.Search.Vector.Enabled = true
	cfg.Reranker.Type = constants.RerankerLLM
	cfg.Normalize()

	err := runDaemonOnce(context.Background(), cfg)

	require.NoError(t, err)
	require.True(t, served)
}

func TestServeRPC_ReturnsWhenGRPCServeFails(t *testing.T) {
	setTestActiveProfile(t)
	origListen := listenFunc
	origServe := grpcServerServe
	t.Cleanup(func() {
		listenFunc = origListen
		grpcServerServe = origServe
	})

	listenFunc = net.Listen
	grpcServerServe = func(*grpc.Server, net.Listener) error {
		return errors.New("serve boom")
	}

	cfg := &config.Config{RPC: config.RPCConfig{Address: "127.0.0.1", Port: 0}}
	err := serveRPC(context.Background(), cfg, &agentstub.AgentRunnerStub{}, openTestRPCListener(t, cfg), nil)

	require.EqualError(t, err, "serve boom")
}

func TestBuildAutomationService_WiresGatewayDeliverySink(t *testing.T) {
	originalNewAutomationService := newAutomationService
	originalNewGatewayDeliverySink := newGatewayAutomationDeliverySink
	t.Cleanup(func() {
		newAutomationService = originalNewAutomationService
		newGatewayAutomationDeliverySink = originalNewGatewayDeliverySink
	})

	expectedSink := morphgateway.NewAutomationDeliverySink(config.GatewayConfig{})
	newGatewayAutomationDeliverySink = func(cfg config.GatewayConfig) automation.DeliverySink {
		require.True(t, cfg.Enabled)
		return expectedSink
	}
	var serviceOptions automation.ServiceOptions
	newAutomationService = func(
		_ automation.Store,
		_ automation.Runner,
		opts automation.ServiceOptions,
	) (*automation.Service, error) {
		serviceOptions = opts
		return nil, nil
	}
	agent := &agentstub.AgentRunnerStub{AgentServiceStub: agentstub.AgentServiceStub{
		AutomationStoreValue: storememory.NewStore(),
		AutomationStoreOK:    true,
	}}

	_, err := buildAutomationService(
		context.Background(),
		&config.Config{Gateway: config.GatewayConfig{Enabled: true}},
		agent,
	)

	require.NoError(t, err)
	require.Same(t, expectedSink, serviceOptions.DeliverySink)

	serviceOptions = automation.ServiceOptions{}
	_, err = buildAutomationService(context.Background(), nil, agent)
	require.NoError(t, err)
	require.Nil(t, serviceOptions.DeliverySink)
}

func TestBuildAutomationService_WiresPermissionApprover(t *testing.T) {
	originalNewAutomationService := newAutomationService
	t.Cleanup(func() { newAutomationService = originalNewAutomationService })
	store := storememory.NewStore()
	approvalService, err := permissions.NewApprovalService(store, permissions.ApprovalOptions{})
	require.NoError(t, err)
	agent := approvalAgentRunnerStub{
		AgentRunnerStub: &agentstub.AgentRunnerStub{AgentServiceStub: agentstub.AgentServiceStub{
			AutomationStoreValue: store, AutomationStoreOK: true,
		}},
		approvalService: approvalService,
	}
	var serviceOptions automation.ServiceOptions
	newAutomationService = func(
		_ automation.Store,
		_ automation.Runner,
		opts automation.ServiceOptions,
	) (*automation.Service, error) {
		serviceOptions = opts
		return nil, nil
	}

	_, err = buildAutomationService(context.Background(), &config.Config{}, agent)
	require.NoError(t, err)
	require.Same(t, approvalService, serviceOptions.PermissionApprover)
}

func TestBuildAutomationService_HandlesUnavailableDependencies(t *testing.T) {
	service, err := buildAutomationService(context.Background(), &config.Config{}, nil)
	require.NoError(t, err)
	require.Nil(t, service)

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	service, err = buildAutomationService(canceledCtx, &config.Config{}, &agentstub.AgentRunnerStub{})
	require.NoError(t, err)
	require.Nil(t, service)

	expected := errors.New("automation store failed")
	agent := &agentstub.AgentRunnerStub{AgentServiceStub: agentstub.AgentServiceStub{
		AutomationStoreErr: expected,
	}}
	service, err = buildAutomationService(context.Background(), &config.Config{}, agent)
	require.ErrorIs(t, err, expected)
	require.Nil(t, service)

	agent.AutomationStoreErr = nil
	service, err = buildAutomationService(context.Background(), &config.Config{}, agent)
	require.NoError(t, err)
	require.Nil(t, service)
}

func TestBuildBrowserService_WiresEnabledDaemonRuntime(t *testing.T) {
	original := newBrowserService
	t.Cleanup(func() { newBrowserService = original })
	cfg := config.NewDefaultConfig()
	cfg.Browser.Enabled = true
	var receivedConfig config.BrowserConfig
	var receivedChecker permissions.Checker
	var receivedBackend browser.Backend
	expected := browserServiceStub{}
	newBrowserService = func(
		_ context.Context,
		browserConfig config.BrowserConfig,
		checker permissions.Checker,
		backend browser.Backend,
		_ ...browser.ServiceOption,
	) (browserService, error) {
		receivedConfig = browserConfig
		receivedChecker = checker
		receivedBackend = backend
		return expected, nil
	}

	service, err := buildBrowserService(context.Background(), cfg)
	require.NoError(t, err)
	require.Equal(t, expected, service)
	require.Equal(t, cfg.Browser, receivedConfig)
	require.NotNil(t, receivedChecker)
	require.IsType(t, browser.ChromiumBackend{}, receivedBackend)
}

func TestBuildBrowserService_SkipsDisabledRuntimeAndPropagatesConstructionFailure(t *testing.T) {
	original := newBrowserService
	t.Cleanup(func() { newBrowserService = original })
	called := false
	expected := errors.New("browser construction failed")
	newBrowserService = func(
		context.Context,
		config.BrowserConfig,
		permissions.Checker,
		browser.Backend,
		...browser.ServiceOption,
	) (browserService, error) {
		called = true
		return nil, expected
	}

	service, err := buildBrowserService(context.Background(), nil)
	require.NoError(t, err)
	require.Nil(t, service)
	service, err = buildBrowserService(context.Background(), &config.Config{})
	require.NoError(t, err)
	require.Nil(t, service)
	require.False(t, called)
	cfg := config.NewDefaultConfig()
	cfg.Browser.Enabled = true
	service, err = buildBrowserService(context.Background(), cfg)
	require.ErrorIs(t, err, expected)
	require.Nil(t, service)
}

func TestServeRPC_RejectsInvalidConfiguredMorphIdentity(t *testing.T) {
	originalProfile := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(originalProfile)
	})
	home := t.TempDir()
	profile.SetActive(profile.Profile{Name: "test", HomeDir: home})
	cfg := config.NewDefaultConfig()
	cfg.RPC.Address = "127.0.0.1"
	cfg.RPC.Port = 0
	cfg.Auth.Key = "invalid"

	err := serveRPC(
		context.Background(), cfg, &agentstub.AgentRunnerStub{}, openTestRPCListener(t, cfg), nil,
	)
	require.ErrorContains(t, err, "read Morph identity key")
}

func TestRecoverRPCIdentityRotation_FinalizesCommittedTransition(t *testing.T) {
	ctx := context.Background()
	identityStore := credential.NewFileStore(filepath.Join(t.TempDir(), "auth.json"))
	current, err := identityStore.LoadOrCreateIdentity()
	require.NoError(t, err)
	_, next, err := identityStore.PrepareIdentityRotation()
	require.NoError(t, err)
	authStore := authstorememory.New()
	service, err := morphauth.NewService(morphauth.ServiceOptions{
		Audience: "morph-rpc:test", Store: authStore,
		SessionMaxTTL: 24 * time.Hour,
	})
	require.NoError(t, err)
	_, err = service.SeedRoot(ctx, current, "owner")
	require.NoError(t, err)
	require.NoError(t, service.RotateRoot(ctx, current.ID, next, "owner"))

	require.NoError(t, recoverRPCIdentityRotation(ctx, identityStore, authStore))
	record, found, err := identityStore.LoadMorphAuth()
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, next.ID, record.IdentityID)
	require.Nil(t, record.Pending)
}

func TestRecoverRPCIdentityRotation_AbortsUncommittedTransition(t *testing.T) {
	ctx := context.Background()
	identityStore := credential.NewFileStore(filepath.Join(t.TempDir(), "auth.json"))
	current, err := identityStore.LoadOrCreateIdentity()
	require.NoError(t, err)
	_, _, err = identityStore.PrepareIdentityRotation()
	require.NoError(t, err)
	authStore := authstorememory.New()
	service, err := morphauth.NewService(morphauth.ServiceOptions{
		Audience: "morph-rpc:test", Store: authStore,
		SessionMaxTTL: 24 * time.Hour,
	})
	require.NoError(t, err)
	_, err = service.SeedRoot(ctx, current, "owner")
	require.NoError(t, err)

	require.NoError(t, recoverRPCIdentityRotation(ctx, identityStore, authStore))
	record, found, err := identityStore.LoadMorphAuth()
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, current.ID, record.IdentityID)
	require.Nil(t, record.Pending)
}

func TestSeedRPCAuthRoot_RotatesConfiguredIdentityAtNextGeneration(t *testing.T) {
	store := authstorememory.New()
	service, err := morphauth.NewService(morphauth.ServiceOptions{
		Audience: "morph-rpc:test", Store: store,
	})
	require.NoError(t, err)
	current, err := morphauth.GenerateIdentity(1)
	require.NoError(t, err)
	require.NoError(t, seedRPCAuthRoot(
		context.Background(), service, current, "owner", true,
	))
	next, err := morphauth.GenerateIdentity(2)
	require.NoError(t, err)
	require.NoError(t, seedRPCAuthRoot(
		context.Background(), service, next, "owner", true,
	))

	previous, err := store.GetAuthorization(context.Background(), current.ID)
	require.NoError(t, err)
	require.Equal(t, morphauth.StatusRevoked, previous.Status)
	replacement, err := store.GetAuthorization(context.Background(), next.ID)
	require.NoError(t, err)
	require.Equal(t, morphauth.StatusActive, replacement.Status)

	skipped, err := morphauth.GenerateIdentity(4)
	require.NoError(t, err)
	err = seedRPCAuthRoot(context.Background(), service, skipped, "owner", true)
	require.EqualError(t, err,
		"configured Morph identity replacement must use a new key and the next auth generation")
}

func TestPrepareIdentityRotationApply_RebindsRuntimeSecrets(t *testing.T) {
	store := credential.NewFileStore(filepath.Join(t.TempDir(), "auth.json"))
	_, err := store.LoadOrCreateIdentity()
	require.NoError(t, err)
	_, next, err := store.PrepareIdentityRotation()
	require.NoError(t, err)
	agent := &identityBinderStub{}
	browserRuntime := &identityBinderStub{}

	apply, err := prepareIdentityRotationApply(
		store, agent, browserRuntime,
	)(context.Background(), next.ID, next.Generation)
	require.NoError(t, err)
	require.Empty(t, agent.commandKey)
	require.Empty(t, browserRuntime.browserKey)
	apply()

	expectedCommand, err := morphauth.DeriveSecret(next, "command-approval")
	require.NoError(t, err)
	expectedBrowser, err := morphauth.DeriveSecret(next, "browser-attachment")
	require.NoError(t, err)
	require.Equal(t, expectedCommand, agent.commandKey)
	require.Equal(t, expectedBrowser, browserRuntime.browserKey)
}

func TestServeRPC_RejectsMissingProfileHomeForStableIdentity(t *testing.T) {
	originalProfile := profile.Active()
	profile.SetActive(profile.Profile{})
	t.Cleanup(func() { profile.SetActive(originalProfile) })
	cfg := config.NewDefaultConfig()
	cfg.RPC.Address = "127.0.0.1"
	cfg.RPC.Port = 0

	err := serveRPC(
		context.Background(), cfg, &agentstub.AgentRunnerStub{}, openTestRPCListener(t, cfg), nil,
	)

	require.EqualError(t, err, "active profile home is required for Morph identity")
}

func TestCloseBrowserService_UsesBoundedContextAndReturnsFailure(t *testing.T) {
	original := stopBrowserTimeout
	stopBrowserTimeout = time.Second
	t.Cleanup(func() { stopBrowserTimeout = original })
	require.NoError(t, closeBrowserService(nil))
	expected := errors.New("close failed")
	service := browserServiceStub{close: func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.WithinDuration(t, time.Now().Add(time.Second), deadline, 100*time.Millisecond)
		return expected
	}}
	require.ErrorIs(t, closeBrowserService(service), expected)
}

func TestServeRPC_ClosesBrowserRuntimeDuringShutdown(t *testing.T) {
	originalService := newBrowserService
	originalServe := grpcServerServe
	originalProfile := profile.Active()
	t.Cleanup(func() {
		newBrowserService = originalService
		grpcServerServe = originalServe
		profile.SetActive(originalProfile)
	})
	profile.SetActive(profile.Profile{Name: "test", HomeDir: t.TempDir()})

	closed := make(chan struct{})
	newBrowserService = func(
		context.Context,
		config.BrowserConfig,
		permissions.Checker,
		browser.Backend,
		...browser.ServiceOption,
	) (browserService, error) {
		return browserServiceStub{close: func(context.Context) error {
			close(closed)
			return nil
		}}, nil
	}
	started := make(chan struct{})
	grpcServerServe = func(server *grpc.Server, listener net.Listener) error {
		close(started)
		return server.Serve(listener)
	}

	cfg := config.NewDefaultConfig()
	cfg.Browser.Enabled = true
	cfg.RPC.Address = "127.0.0.1"
	cfg.RPC.Port = 0
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serveRPC(ctx, cfg, &agentstub.AgentRunnerStub{}, openTestRPCListener(t, cfg), nil)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("RPC server did not start")
	}
	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("RPC server did not stop")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("browser runtime was not closed")
	}
}

func TestServeRPC_ReturnsNilWhenGRPCServeReturnsServerStopped(t *testing.T) {
	setTestActiveProfile(t)
	origListen := listenFunc
	origServe := grpcServerServe
	t.Cleanup(func() {
		listenFunc = origListen
		grpcServerServe = origServe
	})

	listenFunc = net.Listen
	grpcServerServe = func(*grpc.Server, net.Listener) error {
		return grpc.ErrServerStopped
	}

	cfg := &config.Config{RPC: config.RPCConfig{Address: "127.0.0.1", Port: 0}}
	err := serveRPC(context.Background(), cfg, &agentstub.AgentRunnerStub{}, openTestRPCListener(t, cfg), nil)

	require.NoError(t, err)
}

func TestServeRPC_WiresApprovalServiceIntoBrowserRuntime(t *testing.T) {
	setTestActiveProfile(t)
	originalService := newBrowserService
	originalServe := grpcServerServe
	t.Cleanup(func() {
		newBrowserService = originalService
		grpcServerServe = originalServe
	})

	browserRuntime := &browserApprovalServiceStub{}
	newBrowserService = func(
		context.Context,
		config.BrowserConfig,
		permissions.Checker,
		browser.Backend,
		...browser.ServiceOption,
	) (browserService, error) {
		return browserRuntime, nil
	}
	grpcServerServe = func(*grpc.Server, net.Listener) error {
		return grpc.ErrServerStopped
	}
	approvalService := &permissions.ApprovalService{}
	agent := approvalAgentRunnerStub{
		AgentRunnerStub: &agentstub.AgentRunnerStub{}, approvalService: approvalService,
	}
	cfg := config.NewDefaultConfig()
	cfg.Browser.Enabled = true
	cfg.RPC.Port = 0

	err := serveRPC(context.Background(), cfg, agent, openTestRPCListener(t, cfg), nil)
	require.NoError(t, err)
	require.Same(t, approvalService, browserRuntime.approver)
}

func TestServeRPC_WritesRuntimeMetadataWithActualPort(t *testing.T) {
	origListen := listenFunc
	origServe := grpcServerServe
	originalProfile := profile.Active()
	t.Cleanup(func() {
		listenFunc = origListen
		grpcServerServe = origServe
		profile.SetActive(originalProfile)
	})

	home := t.TempDir()
	profileHome := filepath.Join(home, ".morph", "profiles", "work")
	profile.SetActive(profile.WithMetadataPaths(profile.Profile{Name: "work", HomeDir: profileHome}))
	listenFunc = net.Listen
	grpcServerServe = func(*grpc.Server, net.Listener) error {
		return grpc.ErrServerStopped
	}

	cfg := &config.Config{RPC: config.RPCConfig{Address: "127.0.0.1", Port: 0}}
	lis := openTestRPCListener(t, cfg)
	err := serveRPC(context.Background(), cfg, &agentstub.AgentRunnerStub{}, lis, nil)

	require.NoError(t, err)
	require.Greater(t, cfg.RPC.Port, 0)
	data, err := os.ReadFile(filepath.Join(profileHome, "runtime.json"))
	require.NoError(t, err)
	require.Contains(t, string(data), `"profile": "work"`)
	require.Contains(t, string(data), `"address": "127.0.0.1"`)
	require.Contains(t, string(data), `"port": `)
}

func TestNewAgentRunnerImpl_ReturnsAgent(t *testing.T) {
	cfg := &config.Config{
		Name:   "t",
		Models: config.ModelsConfig{Providers: map[string]config.ProviderModelConfig{"openrouter": {APIKey: "k"}}, Main: config.MainModelConfig{Name: "gpt-4o-mini", Provider: "openrouter"}},
	}
	cfg.Normalize()

	mc, err := provider_openai.NewOpenAIClient("k", models.APIOpenAIResponses)
	require.NoError(t, err)
	sc, err := provider_openai.NewOpenAIClient("k", models.APIOpenAIResponses)
	require.NoError(t, err)

	r := newAgentRunnerImpl(context.Background(), cfg, mc, sc, sc)
	require.NotNil(t, r)
}

func TestServeRPC_StopsWhenContextCancelled(t *testing.T) {
	setTestActiveProfile(t)
	orig := listenFunc
	t.Cleanup(func() { listenFunc = orig })

	ctx, cancel := context.WithCancel(context.Background())
	cfg := &config.Config{RPC: config.RPCConfig{Address: "127.0.0.1", Port: 0}}
	done := make(chan error, 1)
	go func() {
		done <- serveRPC(ctx, cfg, &agentstub.AgentRunnerStub{}, openTestRPCListener(t, cfg), nil)
	}()

	time.Sleep(150 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("serveRPC did not return after context cancel")
	}
}

func TestServeRPC_ReturnsPostShutdownServeError(t *testing.T) {
	setTestActiveProfile(t)
	origListen := listenFunc
	origServe := grpcServerServe
	origPost := postShutdownServeErrHook
	t.Cleanup(func() {
		listenFunc = origListen
		grpcServerServe = origServe
		postShutdownServeErrHook = origPost
	})

	listenFunc = net.Listen
	grpcServerServe = func(srv *grpc.Server, lis net.Listener) error {
		return srv.Serve(lis)
	}
	postShutdownServeErrHook = func(error) error {
		return errors.New("post shutdown")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cfg := &config.Config{RPC: config.RPCConfig{Address: "127.0.0.1", Port: 0}}
	done := make(chan error, 1)
	go func() {
		done <- serveRPC(ctx, cfg, &agentstub.AgentRunnerStub{}, openTestRPCListener(t, cfg), nil)
	}()

	time.Sleep(150 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.EqualError(t, err, "post shutdown")
	case <-time.After(15 * time.Second):
		t.Fatal("serveRPC did not return")
	}
}

func TestServeRPC_ForcesStopWhenGracefulShutdownSlow(t *testing.T) {
	setTestActiveProfile(t)
	origListen := listenFunc
	origServe := grpcServerServe
	origTimeout := serveRPCShutdownTimeout
	origGraceful := grpcGracefulStop
	t.Cleanup(func() {
		listenFunc = origListen
		grpcServerServe = origServe
		serveRPCShutdownTimeout = origTimeout
		grpcGracefulStop = origGraceful
	})

	listenFunc = net.Listen
	grpcServerServe = func(srv *grpc.Server, lis net.Listener) error {
		return srv.Serve(lis)
	}
	serveRPCShutdownTimeout = 10 * time.Millisecond
	grpcGracefulStop = func(srv *grpc.Server) {
		time.Sleep(200 * time.Millisecond)
		srv.GracefulStop()
	}

	ctx, cancel := context.WithCancel(context.Background())
	cfg := &config.Config{RPC: config.RPCConfig{Address: "127.0.0.1", Port: 0}}
	done := make(chan error, 1)
	go func() {
		done <- serveRPC(ctx, cfg, &agentstub.AgentRunnerStub{}, openTestRPCListener(t, cfg), nil)
	}()

	time.Sleep(150 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("serveRPC did not return")
	}
}

func TestServeDaemonServices_DisabledGatewayInitializesStatusAndRunsOnlyRPC(t *testing.T) {
	origServe := serveRPC
	t.Cleanup(func() {
		serveRPC = origServe
	})

	serveCalled := false
	serveRPC = func(_ context.Context, _ *config.Config, _ agentRunner, _ net.Listener, manager gatewayManager) error {
		serveCalled = true
		require.NotNil(t, manager)
		status := manager.Status()
		require.Equal(t, morphgateway.StateDisabled, status.State)
		require.Equal(t, "127.0.0.1", status.Address)
		require.Equal(t, 50052, status.Port)
		return nil
	}

	cfg := &config.Config{Gateway: config.GatewayConfig{Address: "127.0.0.1", Port: 50052}}
	err := serveDaemonServices(context.Background(), cfg, &agentstub.AgentRunnerStub{}, noopListener{})

	require.NoError(t, err)
	require.True(t, serveCalled)
}

func TestServeDaemonServices_EnabledGatewayStopsWithRPC(t *testing.T) {
	origServe := serveRPC
	origGateway := newGatewayManager
	t.Cleanup(func() {
		serveRPC = origServe
		newGatewayManager = origGateway
	})

	started := false
	stopped := false
	agent := &agentstub.AgentRunnerStub{}
	newGatewayManager = func() gatewayManager {
		return gatewayManagerStub{
			start: func(_ context.Context, _ config.GatewayConfig, responder morphgateway.AgentService) error {
				started = true
				require.Same(t, agent, responder)
				return nil
			},
			stop: func(context.Context) error {
				stopped = true
				return nil
			},
		}
	}
	serveRPC = func(_ context.Context, _ *config.Config, _ agentRunner, _ net.Listener, _ gatewayManager) error {
		return nil
	}

	cfg := &config.Config{Gateway: config.GatewayConfig{Enabled: true}}
	err := serveDaemonServices(context.Background(), cfg, agent, noopListener{})

	require.NoError(t, err)
	require.True(t, started)
	require.True(t, stopped)
}

func TestLogGatewayStartedIncludesConfiguredChannels(t *testing.T) {
	var output bytes.Buffer
	logutils.SetOutput(&output)
	logutils.ConfigureLogger("morph", true)
	t.Cleanup(func() {
		logutils.SetOutput(io.Discard)
		logutils.ConfigureLogger("morph", true)
	})

	logGatewayStarted(config.GatewayConfig{
		Address: "127.0.0.1",
		Port:    50052,
		Telegram: config.GatewayTelegramConfig{
			Enabled: true,
			Mode:    config.GatewayTelegramModePolling,
		},
		Slack: config.GatewaySlackConfig{
			Enabled: true,
			Mode:    config.GatewaySlackModeSocket,
		},
	})

	logOutput := output.String()
	require.Contains(t, logOutput, "Gateway started")
	require.Contains(t, logOutput, "gatewayAddress=127.0.0.1")
	require.Contains(t, logOutput, "gatewayPort=50052")
	require.Contains(t, logOutput, "telegramMode=polling")
	require.Contains(t, logOutput, "slackMode=socket")
}

func TestServeDaemonServices_ReturnsGatewayStartError(t *testing.T) {
	origServe := serveRPC
	origGateway := newGatewayManager
	t.Cleanup(func() {
		serveRPC = origServe
		newGatewayManager = origGateway
	})

	lis := &closeTrackingListener{}
	newGatewayManager = func() gatewayManager {
		return gatewayManagerStub{
			start: func(context.Context, config.GatewayConfig, morphgateway.AgentService) error {
				return errors.New("gateway start failed")
			},
		}
	}
	serveRPC = func(_ context.Context, _ *config.Config, _ agentRunner, _ net.Listener, _ gatewayManager) error {
		t.Fatal("serveRPC should not run when gateway startup fails")
		return nil
	}

	cfg := &config.Config{Gateway: config.GatewayConfig{Enabled: true}}
	err := serveDaemonServices(context.Background(), cfg, &agentstub.AgentRunnerStub{}, lis)

	require.EqualError(t, err, "gateway start failed")
	require.True(t, lis.closed)
}

func TestServeDaemonServices_GatewayStopDoesNotStopRPC(t *testing.T) {
	origServe := serveRPC
	origGateway := newGatewayManager
	t.Cleanup(func() {
		serveRPC = origServe
		newGatewayManager = origGateway
	})

	rpcStarted := make(chan struct{})
	gatewayStopped := make(chan struct{})
	rpcStopped := make(chan error, 1)
	var stopOnce sync.Once
	newGatewayManager = func() gatewayManager {
		return gatewayManagerStub{
			stop: func(context.Context) error {
				stopOnce.Do(func() { close(gatewayStopped) })
				return nil
			},
		}
	}
	serveRPC = func(ctx context.Context, _ *config.Config, _ agentRunner, _ net.Listener, manager gatewayManager) error {
		close(rpcStarted)
		require.NoError(t, manager.Stop(context.Background()))
		<-ctx.Done()
		rpcStopped <- nil
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	cfg := &config.Config{Gateway: config.GatewayConfig{Enabled: true}}
	go func() {
		done <- serveDaemonServices(ctx, cfg, &agentstub.AgentRunnerStub{}, noopListener{})
	}()

	select {
	case <-rpcStarted:
	case <-time.After(time.Second):
		t.Fatal("RPC did not start")
	}
	select {
	case <-gatewayStopped:
	case <-time.After(time.Second):
		t.Fatal("gateway stop was not called")
	}

	select {
	case err := <-done:
		t.Fatalf("daemon stopped after gateway stop: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	require.NoError(t, <-done)
	require.NoError(t, <-rpcStopped)
}

func TestServeDaemonServices_ContextCancelStopsGatewayAndRPC(t *testing.T) {
	origServe := serveRPC
	origGateway := newGatewayManager
	t.Cleanup(func() {
		serveRPC = origServe
		newGatewayManager = origGateway
	})

	gatewayStopped := make(chan struct{})
	newGatewayManager = func() gatewayManager {
		return gatewayManagerStub{
			stop: func(context.Context) error {
				close(gatewayStopped)
				return nil
			},
		}
	}
	rpcStopped := make(chan struct{})
	serveRPC = func(ctx context.Context, _ *config.Config, _ agentRunner, _ net.Listener, _ gatewayManager) error {
		<-ctx.Done()
		close(rpcStopped)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		cfg := &config.Config{Gateway: config.GatewayConfig{Enabled: true}}
		done <- serveDaemonServices(ctx, cfg, &agentstub.AgentRunnerStub{}, noopListener{})
	}()

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("daemon services did not stop after context cancellation")
	}
	select {
	case <-gatewayStopped:
	default:
		t.Fatal("gateway was not stopped")
	}
	select {
	case <-rpcStopped:
	default:
		t.Fatal("RPC was not stopped")
	}
}

func TestStopGatewayWithTimeoutAttemptsStopWhenManagerReturnsError(t *testing.T) {
	stopped := false
	stopGatewayWithTimeout(gatewayManagerStub{
		stop: func(context.Context) error {
			stopped = true
			return errors.New("stop failed")
		},
	})

	require.True(t, stopped)
}

func TestNewCommand_ReturnsConfigLoadError(t *testing.T) {
	isolateCommandProfile(t)
	origServe := serveRPC
	t.Cleanup(func() { serveRPC = origServe })
	serveRPC = func(_ context.Context, _ *config.Config, _ agentRunner, _ net.Listener, _ gatewayManager) error {
		t.Fatal("serveRPC should not run")
		return nil
	}

	badPath := filepath.Join(t.TempDir(), "bad.yaml")
	require.NoError(t, os.WriteFile(badPath, []byte(":\ninvalid"), 0o600))

	configFile := ""
	cmd := newRootCommandForTest(&configFile)
	err := cmd.Run(context.Background(), []string{"morph", "--config", badPath, "daemon"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to parse config file")
}

func openTestRPCListener(t *testing.T, cfg *config.Config) net.Listener {
	t.Helper()

	lis, err := openRPCListener(cfg)
	require.NoError(t, err)

	return lis
}

func setTestActiveProfile(t *testing.T) {
	t.Helper()
	original := profile.Active()
	profile.SetActive(profile.Profile{Name: "test", HomeDir: t.TempDir()})
	t.Cleanup(func() {
		profile.SetActive(original)
	})
}

func stubOpenRPCListener(t *testing.T) {
	t.Helper()

	original := openRPCListener
	openRPCListener = func(*config.Config) (net.Listener, error) {
		return noopListener{}, nil
	}
	t.Cleanup(func() {
		openRPCListener = original
	})
}

type noopListener struct{}

func (noopListener) Accept() (net.Conn, error) {
	return nil, errors.New("listener closed")
}

func (noopListener) Close() error {
	return nil
}

func (noopListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0}
}

type closeTrackingListener struct {
	closed bool
}

func (l *closeTrackingListener) Accept() (net.Conn, error) {
	return nil, errors.New("listener closed")
}

func (l *closeTrackingListener) Close() error {
	l.closed = true
	return nil
}

func (l *closeTrackingListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0}
}

type startupWriteFailAlways struct{}

func (startupWriteFailAlways) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

type startupWriteFailAfterFirst struct {
	n int
}

func (w *startupWriteFailAfterFirst) Write(p []byte) (int, error) {
	w.n++
	if w.n == 1 {
		return len(p), nil
	}
	return 0, errors.New("write failed")
}

func TestNewCommand_ReturnsStartupOutputError(t *testing.T) {
	isolateCommandProfile(t)
	stubOpenRPCListener(t)
	origOut := startupOutput
	origServe := serveRPC
	t.Cleanup(func() {
		startupOutput = origOut
		serveRPC = origServe
	})

	serveRPC = func(_ context.Context, _ *config.Config, _ agentRunner, _ net.Listener, _ gatewayManager) error {
		return nil
	}

	configFile := ""
	cmd := newRootCommandForTest(&configFile)
	args := []string{
		"morph",
		"--name", "x",
		"--model", "gpt-4o-mini",
		"--model.provider", "openrouter",
		"--model.api-key", "k",
		"--rpc.address", "127.0.0.1",
		"--rpc.port", "50051",
		"daemon",
	}

	startupOutput = startupWriteFailAlways{}
	err := cmd.Run(context.Background(), args)
	require.Error(t, err)

	startupOutput = &startupWriteFailAfterFirst{}
	err = cmd.Run(context.Background(), args)
	require.Error(t, err)
}

func TestNewCommand_ReturnsModelClientFactoryError(t *testing.T) {
	isolateCommandProfile(t)
	stubOpenRPCListener(t)
	origFactory := modelClientFactory
	origServe := serveRPC
	t.Cleanup(func() {
		modelClientFactory = origFactory
		serveRPC = origServe
	})

	serveRPC = func(_ context.Context, _ *config.Config, _ agentRunner, _ net.Listener, _ gatewayManager) error {
		return nil
	}
	modelClientFactory = modelClientFactoryStub{
		newClient: func(modelclient.ClientRequest) (models.Client, error) {
			return nil, errors.New("model factory boom")
		},
	}

	configFile := ""
	cmd := newRootCommandForTest(&configFile)
	serverURL := newOpenRouterModelsServer(t, "gpt-4o-mini")
	err := cmd.Run(context.Background(), []string{
		"morph",
		"--name", "flag-agent",
		"--model", "gpt-4o-mini",
		"--model.provider", "openrouter",
		"--model.api-key", "flag-key",
		"--model.base-url", serverURL,
		"--rpc.address", "127.0.0.1",
		"--rpc.port", "50051",
		"daemon",
	})
	require.EqualError(t, err, "model factory boom")
}

func TestNewCommand_StartsWhenSummaryAuthCannotResolve(t *testing.T) {
	isolateCommandProfile(t)
	stubOpenRPCListener(t)
	origResolve := resolveSummaryAuth
	origServe := serveRPC
	t.Cleanup(func() {
		resolveSummaryAuth = origResolve
		serveRPC = origServe
	})

	served := false
	serveRPC = func(_ context.Context, cfg *config.Config, _ agentRunner, _ net.Listener, _ gatewayManager) error {
		served = true
		return nil
	}
	resolveSummaryAuth = func(*config.Config) (config.ModelAuth, error) {
		return config.ModelAuth{}, errors.New("summary auth boom")
	}

	configFile := ""
	cmd := newRootCommandForTest(&configFile)
	serverURL := newOpenRouterModelsServer(t, "gpt-4o-mini")
	err := cmd.Run(context.Background(), []string{
		"morph",
		"--name", "flag-agent",
		"--model", "gpt-4o-mini",
		"--model.provider", "openrouter",
		"--model.api-key", "flag-key",
		"--model.base-url", serverURL,
		"--rpc.address", "127.0.0.1",
		"--rpc.port", "50051",
		"daemon",
	})
	require.NoError(t, err)
	require.True(t, served)
}

func TestNewCommand_ReturnsSecondModelClientFactoryError(t *testing.T) {
	isolateCommandProfile(t)
	stubOpenRPCListener(t)
	t.Setenv("OPENAI_API_KEY", "openai-key")
	origFactory := modelClientFactory
	origServe := serveRPC
	t.Cleanup(func() {
		modelClientFactory = origFactory
		serveRPC = origServe
	})

	serveRPC = func(_ context.Context, _ *config.Config, _ agentRunner, _ net.Listener, _ gatewayManager) error {
		return nil
	}

	var n int
	modelClientFactory = modelClientFactoryStub{
		newClient: func(modelclient.ClientRequest) (models.Client, error) {
			n++
			if n == 1 {
				return &provider_openai.OpenAIClient{}, nil
			}
			return nil, errors.New("summary client boom")
		},
	}

	configFile := ""
	cmd := newRootCommandForTest(&configFile)
	serverURL := newOpenRouterModelsServer(t, "gpt-4o-mini")
	err := cmd.Run(context.Background(), []string{
		"morph",
		"--name", "flag-agent",
		"--model", "gpt-4o-mini",
		"--model.provider", "openrouter",
		"--model.api-key", "flag-key",
		"--model.base-url", serverURL,
		"--model.summary-provider", "openai",
		"--model.summary-base-url", "https://api.openai.com/v1",
		"--rpc.address", "127.0.0.1",
		"--rpc.port", "50051",
		"daemon",
	})
	require.EqualError(t, err, "summary client boom")
}

func TestNewCommand_PassesResolvedAuthToModelClientFactory(t *testing.T) {
	isolateCommandProfile(t)
	stubOpenRPCListener(t)
	t.Setenv("OPENAI_API_KEY", "openai-key")
	origFactory := modelClientFactory
	origRunner := newAgentRunner
	origServe := serveRPC
	origStartupOutput := startupOutput
	t.Cleanup(func() {
		modelClientFactory = origFactory
		newAgentRunner = origRunner
		serveRPC = origServe
		startupOutput = origStartupOutput
	})

	var calls []modelclient.ClientRequest
	modelClientFactory = modelClientFactoryStub{
		newClient: func(req modelclient.ClientRequest) (models.Client, error) {
			calls = append(calls, req)
			return &provider_openai.OpenAIClient{}, nil
		},
	}
	newAgentRunner = func(context.Context, *config.Config, models.Client, models.Client, models.Client) agentRunner {
		return &agentstub.AgentRunnerStub{}
	}
	serveRPC = func(_ context.Context, _ *config.Config, _ agentRunner, _ net.Listener, _ gatewayManager) error {
		return nil
	}
	startupOutput = io.Discard

	configFile := ""
	cmd := newRootCommandForTest(&configFile)
	serverURL := newOpenRouterModelsServer(t, "gpt-4o-mini")
	require.NoError(t, cmd.Run(context.Background(), []string{
		"morph",
		"--name", "flag-agent",
		"--model", "gpt-4o-mini",
		"--model.provider", "openrouter",
		"--model.api-key", "router-key",
		"--model.base-url", serverURL,
		"--model.summary-provider", "openai",
		"--model.summary-base-url", "https://openai.example/v1",
		"--rpc.address", "127.0.0.1",
		"--rpc.port", "50051",
		"daemon",
	}))

	require.Equal(t, []modelclient.ClientRequest{
		{
			Role:       modelclient.ModelRoleMain,
			Model:      "gpt-4o-mini",
			Provider:   "openrouter",
			API:        modelprovider.APIOpenAIResponses,
			APIKey:     "router-key",
			BaseURL:    serverURL,
			MaxRetries: constants.DefaultModelMaxRetries,
		},
		{
			Role:       modelclient.ModelRoleSummary,
			Model:      "gpt-4o-mini",
			Provider:   "openai",
			API:        modelprovider.APIOpenAIResponses,
			APIKey:     "openai-key",
			BaseURL:    "https://openai.example/v1",
			MaxRetries: constants.DefaultModelMaxRetries,
		},
	}, calls)
}

func TestNewCommand_PassesSeparateRerankerClientWhenAuthDiffers(t *testing.T) {
	isolateCommandProfile(t)
	stubOpenRPCListener(t)
	t.Setenv("MORPH_RERANKER_TYPE", constants.RerankerLLM)
	origFactory := modelClientFactory
	origRunner := newAgentRunner
	origServe := serveRPC
	origStartupOutput := startupOutput
	origResolveRerankerAuth := resolveRerankerAuth
	t.Cleanup(func() {
		modelClientFactory = origFactory
		newAgentRunner = origRunner
		serveRPC = origServe
		startupOutput = origStartupOutput
		resolveRerankerAuth = origResolveRerankerAuth
	})

	rerankerAuth := config.ModelAuth{
		Provider: "anthropic",
		API:      modelprovider.APIAnthropicMessages,
		APIKey:   "reranker-key",
		BaseURL:  "https://anthropic.example",
	}
	resolveRerankerAuth = func(*config.Config) (config.ModelAuth, error) {
		return rerankerAuth, nil
	}

	var calls []modelclient.ClientRequest
	clients := make([]models.Client, 0, 2)
	modelClientFactory = modelClientFactoryStub{
		newClient: func(req modelclient.ClientRequest) (models.Client, error) {
			calls = append(calls, req)
			client := &provider_openai.OpenAIClient{}
			clients = append(clients, client)
			return client, nil
		},
	}
	newAgentRunner = func(_ context.Context, _ *config.Config, modelClient, summaryClient, rerankerClient models.Client) agentRunner {
		require.Same(t, modelClient, summaryClient)
		require.NotSame(t, modelClient, rerankerClient)
		return &agentstub.AgentRunnerStub{}
	}
	serveRPC = func(_ context.Context, _ *config.Config, _ agentRunner, _ net.Listener, _ gatewayManager) error {
		return nil
	}
	startupOutput = io.Discard

	configFile := ""
	cmd := newRootCommandForTest(&configFile)
	serverURL := newOpenRouterModelsServer(t, "gpt-4o-mini")
	require.NoError(t, cmd.Run(context.Background(), []string{
		"morph",
		"--name", "flag-agent",
		"--model", "gpt-4o-mini",
		"--model.provider", "openrouter",
		"--model.api-key", "router-key",
		"--model.base-url", serverURL,
		"--rpc.address", "127.0.0.1",
		"--rpc.port", "50051",
		"daemon",
	}))

	require.Len(t, clients, 2)
	require.Equal(t, []modelclient.ClientRequest{
		{
			Role:       modelclient.ModelRoleMain,
			Model:      "gpt-4o-mini",
			Provider:   "openrouter",
			API:        modelprovider.APIOpenAIResponses,
			APIKey:     "router-key",
			BaseURL:    serverURL,
			MaxRetries: constants.DefaultModelMaxRetries,
		},
		{
			Role:       modelclient.ModelRoleReranker,
			Model:      "gpt-4o-mini",
			Provider:   "anthropic",
			API:        modelprovider.APIAnthropicMessages,
			APIKey:     "reranker-key",
			BaseURL:    "https://anthropic.example",
			MaxRetries: constants.DefaultModelMaxRetries,
		},
	}, calls)
}

func TestNewCommand_ReturnsAgentStartError(t *testing.T) {
	isolateCommandProfile(t)
	stubOpenRPCListener(t)
	origRunner := newAgentRunner
	origServe := serveRPC
	t.Cleanup(func() {
		newAgentRunner = origRunner
		serveRPC = origServe
	})

	serveRPC = func(_ context.Context, _ *config.Config, _ agentRunner, _ net.Listener, _ gatewayManager) error {
		t.Fatal("serveRPC should not run when Start fails")
		return nil
	}

	newAgentRunner = func(context.Context, *config.Config, models.Client, models.Client, models.Client) agentRunner {
		return &agentstub.AgentRunnerStub{
			StartFunc: func(context.Context) error {
				return errors.New("start failed")
			},
		}
	}

	configFile := ""
	cmd := newRootCommandForTest(&configFile)
	serverURL := newOpenRouterModelsServer(t, "gpt-4o-mini")
	err := cmd.Run(context.Background(), []string{
		"morph",
		"--name", "flag-agent",
		"--model", "gpt-4o-mini",
		"--model.provider", "openrouter",
		"--model.api-key", "flag-key",
		"--model.base-url", serverURL,
		"--rpc.address", "127.0.0.1",
		"--rpc.port", "50051",
		"daemon",
	})
	require.EqualError(t, err, "start failed")
}

func TestNewCommand_UsesSeparateSummaryClientWhenAuthDiffers(t *testing.T) {
	isolateCommandProfile(t)
	stubOpenRPCListener(t)
	t.Setenv("OPENAI_API_KEY", "openai-key")
	original := config.Get()
	originalNewAgentRunner := newAgentRunner
	originalServeGRPC := serveRPC
	originalStartupOutput := startupOutput

	t.Cleanup(func() {
		config.Set(original)
		newAgentRunner = originalNewAgentRunner
		serveRPC = originalServeGRPC
		startupOutput = originalStartupOutput
		logutils.SetOutput(io.Discard)
	})

	config.Set(nil)
	configFile := ""
	serverURL := newOpenRouterModelsServer(t, "gpt-4o-mini")
	runCalled := false
	serveCalled := false
	startupOutput = io.Discard
	logutils.SetOutput(io.Discard)

	newAgentRunner = func(_ context.Context, cfg *config.Config, modelClient, summaryClient, rerankerClient models.Client) agentRunner {
		require.NotSame(t, modelClient, summaryClient)
		return &agentstub.AgentRunnerStub{
			StartFunc: func(context.Context) error {
				runCalled = true
				return nil
			},
		}
	}

	serveRPC = func(_ context.Context, _ *config.Config, _ agentRunner, _ net.Listener, _ gatewayManager) error {
		serveCalled = true
		return nil
	}

	cmd := newRootCommandForTest(&configFile)
	require.NoError(t, cmd.Run(context.Background(), []string{
		"morph",
		"--name", "flag-agent",
		"--model", "gpt-4o-mini",
		"--model.provider", "openrouter",
		"--model.api-key", "flag-key",
		"--model.base-url", serverURL,
		"--model.summary-provider", "openai",
		"--model.summary-base-url", "https://api.openai.com/v1",
		"--rpc.address", "127.0.0.1",
		"--rpc.port", "50051",
		"daemon",
	}))

	require.True(t, runCalled)
	require.True(t, serveCalled)
}

func TestNewCommand_StartsWithoutConfiguredModel(t *testing.T) {
	isolateCommandProfile(t)
	stubOpenRPCListener(t)
	originalServeGRPC := serveRPC
	originalFactory := modelClientFactory

	t.Cleanup(func() {
		serveRPC = originalServeGRPC
		modelClientFactory = originalFactory
	})

	served := false
	serveRPC = func(_ context.Context, cfg *config.Config, _ agentRunner, _ net.Listener, _ gatewayManager) error {
		served = true
		require.False(t, cfg.Gateway.Enabled)
		require.False(t, cfg.Search.Vector.Enabled)
		require.False(t, cfg.MemoryEnabled())
		return nil
	}
	modelClientFactory = modelClientFactoryStub{
		newClient: func(modelclient.ClientRequest) (models.Client, error) {
			t.Fatal("model client should not be created without a configured model")
			return nil, nil
		},
	}

	configFile := ""
	cmd := newRootCommandForTest(&configFile)
	err := cmd.Run(context.Background(), []string{
		"morph",
		"--name", "flag-agent",
		"--model", "",
		"--model.provider", "openrouter",
		"--model.api-key", "",
		"--gateway.enabled",
		"--gateway.telegram.enabled",
		"daemon",
	})

	require.NoError(t, err)
	require.True(t, served)
}

func newRootCommandForTest(configFile *string) *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "morph",
		Flags: daemonRootFlags(configFile),
		Commands: []*urfavecli.Command{
			newDaemonCommandForTest(),
		},
	}
}

func newDaemonCommandForTest() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "daemon",
		Usage: "Start the Morph daemon",
		Flags: []urfavecli.Flag{daemonPersistentInstructFlag()},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			return runDaemonWithConfigRestarts(ctx, cmd, daemonConfigWatchDebounce)
		},
	}
}

func runParsedDaemonCommand(t *testing.T, args []string, action func(context.Context, *urfavecli.Command) error) error {
	t.Helper()

	configFile := ""
	root := &urfavecli.Command{
		Name:  "morph",
		Flags: daemonRootFlags(&configFile),
		Commands: []*urfavecli.Command{
			{
				Name:   "daemon",
				Action: action,
			},
		},
	}

	return root.Run(context.Background(), append([]string{"morph"}, args...))
}

func TestRunWithConfigRestartsUsesDependenciesAndRestoresPrevious(t *testing.T) {
	expectedErr := errors.New("load failed")
	previous := Dependencies{SafetySummary: func(*config.Config) string { return "previous" }}
	daemonDependencies = previous
	t.Cleanup(func() {
		daemonDependencies = testDaemonDependencies()
	})

	err := RunWithConfigRestarts(context.Background(), &urfavecli.Command{}, Dependencies{
		LoadConfig: func(*urfavecli.Command) (*config.Config, ConfigInputs, error) {
			require.Equal(t, "custom", daemonDependencies.safetySummary(&config.Config{}))
			return nil, ConfigInputs{}, expectedErr
		},
		SafetySummary: func(*config.Config) string { return "custom" },
	})

	require.ErrorIs(t, err, expectedErr)
	require.Equal(t, "previous", daemonDependencies.safetySummary(&config.Config{}))
}

func TestRunOnceDelegatesToDaemonRuntime(t *testing.T) {
	originalFactory := modelClientFactory
	originalRunner := newAgentRunner
	originalServe := serveRPC
	originalOpenRPCListener := openRPCListener
	originalOutput := startupOutput
	t.Cleanup(func() {
		modelClientFactory = originalFactory
		newAgentRunner = originalRunner
		serveRPC = originalServe
		openRPCListener = originalOpenRPCListener
		startupOutput = originalOutput
	})

	modelClientFactory = modelClientFactoryStub{
		newClient: func(modelclient.ClientRequest) (models.Client, error) {
			return &provider_openai.OpenAIClient{}, nil
		},
	}
	newAgentRunner = func(context.Context, *config.Config, models.Client, models.Client, models.Client) agentRunner {
		return &agentstub.AgentRunnerStub{}
	}
	openRPCListener = func(*config.Config) (net.Listener, error) {
		return noopListener{}, nil
	}
	served := false
	serveRPC = func(context.Context, *config.Config, agentRunner, net.Listener, gatewayManager) error {
		served = true
		return nil
	}
	startupOutput = io.Discard

	err := RunOnce(context.Background(), newUpTestConfig("daemon"))

	require.NoError(t, err)
	require.True(t, served)
}

func TestDependenciesDefaults(t *testing.T) {
	cfg := &config.Config{}

	_, _, err := Dependencies{}.loadConfig(&urfavecli.Command{})

	require.EqualError(t, err, "daemon config loader is required")
	require.Empty(t, Dependencies{}.safetySummary(cfg))
	require.NotPanics(t, func() {
		Dependencies{}.applyConfigOverrides(&urfavecli.Command{}, cfg)
		Dependencies{}.addStartupFilesystemRoots(cfg, ConfigInputs{})
	})
}

func TestUnavailableModelClientCompleteStreamReturnsError(t *testing.T) {
	expectedErr := errors.New("model unavailable")
	client := unavailableModelClient{err: expectedErr}

	resp, err := client.CompleteStream(context.Background(), models.Request{}, func(models.StreamDelta) {})

	require.Nil(t, resp)
	require.ErrorIs(t, err, expectedErr)
}

func TestRerankerModelClientRequiredReturnsFalseForDisabledCases(t *testing.T) {
	rerankerDisabled := false
	rerankDisabled := false

	require.False(t, rerankerModelClientRequired(nil))
	require.False(t, rerankerModelClientRequired(&config.Config{}))
	require.False(t, rerankerModelClientRequired(&config.Config{
		Search:   config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true}},
		Reranker: config.RerankerConfig{Enabled: &rerankerDisabled},
	}))
	require.False(t, rerankerModelClientRequired(&config.Config{
		Search: config.SearchConfig{
			Vector:       config.SearchVectorConfig{Enabled: true},
			EnableRerank: &rerankDisabled,
		},
	}))
	require.False(t, rerankerModelClientRequired(&config.Config{
		Search: config.SearchConfig{Vector: config.SearchVectorConfig{Enabled: true}},
		Reranker: config.RerankerConfig{
			Type:      constants.RerankerDeterministic,
			Overrides: map[string]config.RerankerOverrideConfig{"memory": {Type: constants.RerankerDeterministic}},
		},
	}))
}

func TestPrepareDaemonRuntimeConfigEdgeCases(t *testing.T) {
	require.Nil(t, prepareDaemonRuntimeConfig(nil))
	require.False(t, hasDaemonModelSelection(nil))

	ready := newUpTestConfig("ready")
	require.Same(t, ready, prepareDaemonRuntimeConfig(ready))

	noRuntimeChanges := newUpTestConfig("no-runtime-changes")
	noRuntimeChanges.Search.Vector.Enabled = false
	require.Same(t, noRuntimeChanges, prepareDaemonRuntimeConfig(noRuntimeChanges))

	needsGatewayRuntime := newUpTestConfig("gateway")
	needsGatewayRuntime.Models.Main.Name = ""
	needsGatewayRuntime.Gateway.Enabled = true
	needsGatewayRuntime.Search.Vector.Enabled = true
	needsGatewayRuntime.Models.Summary.Name = "gpt-4o-mini"
	needsGatewayRuntime.Models.Embedding.Name = "text-embedding-3-small"
	needsGatewayRuntime.Models.Embedding.Provider = "openrouter"
	needsGatewayRuntime.Normalize()

	got := prepareDaemonRuntimeConfig(needsGatewayRuntime)

	require.NotSame(t, needsGatewayRuntime, got)
	require.False(t, got.Gateway.Enabled)
	require.True(t, got.Search.Vector.Enabled)
}

func TestLoadDaemonConfigReturnsValidationFailure(t *testing.T) {
	previousDeps := daemonDependencies
	t.Cleanup(func() {
		daemonDependencies = previousDeps
	})

	cfg := newUpTestConfig("invalid")
	cfg.RPC.Port = -1
	daemonDependencies = Dependencies{
		LoadConfig: func(*urfavecli.Command) (*config.Config, ConfigInputs, error) {
			return cfg, ConfigInputs{
				EnvPath:    filepath.Join(t.TempDir(), ".env"),
				ConfigPath: filepath.Join(t.TempDir(), "config.yaml"),
			}, nil
		},
		SafetySummary: func(*config.Config) string { return "" },
	}

	_, err := loadDaemonConfig(&urfavecli.Command{})

	require.Error(t, err)
}

func TestBuildDaemonMainModelClientMissingProviderAndAuthError(t *testing.T) {
	missingProvider := newUpTestConfig("missing-provider")
	missingProvider.Models.Main.Provider = " "

	client, auth, err := buildDaemonMainModelClient(missingProvider)

	require.NoError(t, err)
	require.Empty(t, auth)
	require.IsType(t, unavailableModelClient{}, client)

	missingAuth := newUpTestConfig("missing-auth")
	missingAuth.Models.Providers = nil
	missingAuth.Models.Main.APIKey = ""

	client, auth, err = buildDaemonMainModelClient(missingAuth)

	require.NoError(t, err)
	require.Empty(t, auth)
	require.IsType(t, unavailableModelClient{}, client)
}

func TestBuildDaemonMainModelClientReturnsFactoryError(t *testing.T) {
	originalFactory := modelClientFactory
	t.Cleanup(func() {
		modelClientFactory = originalFactory
	})

	expectedErr := errors.New("factory failed")
	modelClientFactory = modelClientFactoryStub{
		newClient: func(modelclient.ClientRequest) (models.Client, error) {
			return nil, expectedErr
		},
	}

	client, auth, err := buildDaemonMainModelClient(newUpTestConfig("factory-error"))

	require.Nil(t, client)
	require.Empty(t, auth)
	require.ErrorIs(t, err, expectedErr)
}

func TestServeDaemonServicesUsesRPCWhenConfigIsNil(t *testing.T) {
	originalServe := serveRPC
	t.Cleanup(func() {
		serveRPC = originalServe
	})

	called := false
	serveRPC = func(_ context.Context, cfg *config.Config, _ agentRunner, _ net.Listener, _ gatewayManager) error {
		called = true
		require.Nil(t, cfg)
		return nil
	}

	err := serveDaemonServices(context.Background(), nil, &agentstub.AgentRunnerStub{}, noopListener{})

	require.NoError(t, err)
	require.True(t, called)
}

func TestOpenRPCListenerReturnsRuntimeMetadataWriteError(t *testing.T) {
	origListen := listenFunc
	origWriteRuntimeMetadata := writeRuntimeMetadata
	originalProfile := profile.Active()
	t.Cleanup(func() {
		listenFunc = origListen
		writeRuntimeMetadata = origWriteRuntimeMetadata
		profile.SetActive(originalProfile)
	})

	expectedErr := errors.New("metadata failed")
	listener := &closeTrackingListener{}
	listenFunc = func(string, string) (net.Listener, error) {
		return listener, nil
	}
	writeRuntimeMetadata = func(string, int) (morphruntime.Metadata, error) {
		return morphruntime.Metadata{}, expectedErr
	}
	profile.SetActive(profile.Profile{RuntimePath: filepath.Join(t.TempDir(), "runtime.json")})

	lis, err := openRPCListener(&config.Config{RPC: config.RPCConfig{Address: "127.0.0.1", Port: 50051}})

	require.Nil(t, lis)
	require.ErrorIs(t, err, expectedErr)
	require.True(t, listener.closed)
}

func TestStartupHelperEdgeCases(t *testing.T) {
	originalVersion := constants.AppVersion
	originalCommit := constants.CommitHash
	t.Cleanup(func() {
		constants.AppVersion = originalVersion
		constants.CommitHash = originalCommit
	})

	constants.AppVersion = ""
	constants.CommitHash = ""

	require.Equal(t, "dev (commit unknown)", formatStartupVersion())
	require.Equal(t, []string{""}, renderStartupLogoLines(nil, 1, true))
	require.Nil(t, splitStartupLines(""))
	require.Equal(t, "long", centerStartupLine("long", 2))
	require.Equal(t, []string{"a", ""}, padStartupBlockVertically([]string{"a"}, 2))
	require.Equal(t, "", getStartupBannerLine([]string{"a"}, -1))
	require.Equal(t, "", getStartupBannerLine([]string{"a"}, 1))
	require.Equal(t, "sqlite", getEffectiveStorageBackend(nil))
	require.Equal(t, "sqlite", getEffectiveStorageBackend(&config.Config{}))
}

func daemonRootFlags(configFile *string) []urfavecli.Flag {
	flags := []urfavecli.Flag{
		&urfavecli.StringFlag{Name: "config"},
		&urfavecli.StringFlag{Name: "name"},
		&urfavecli.StringFlag{Name: "model"},
		&urfavecli.StringFlag{Name: "model.provider"},
		&urfavecli.StringFlag{Name: "model.api-key"},
		&urfavecli.StringFlag{Name: "model.base-url"},
		&urfavecli.StringFlag{Name: "model.summary-provider"},
		&urfavecli.StringFlag{Name: "model.summary-base-url"},
		&urfavecli.StringFlag{Name: "rpc.address"},
		&urfavecli.IntFlag{Name: "rpc.port"},
		&urfavecli.BoolFlag{Name: "gateway.enabled"},
		&urfavecli.BoolFlag{Name: "gateway.telegram.enabled"},
		&urfavecli.StringFlag{Name: "log.level"},
		&urfavecli.BoolFlag{Name: "trace.enabled"},
		&urfavecli.StringFlag{Name: "trace.disk.dir"},
	}
	if configFile != nil {
		flags[0] = &urfavecli.StringFlag{Name: "config", Destination: configFile}
	}

	return flags
}

func daemonPersistentInstructFlag() urfavecli.Flag {
	return &urfavecli.StringFlag{Name: "instruct"}
}

func testDaemonDependencies() Dependencies {
	return Dependencies{
		LoadConfig: func(cmd *urfavecli.Command) (*config.Config, ConfigInputs, error) {
			inputs, err := testDaemonConfigInputs(cmd)
			if err != nil {
				return nil, ConfigInputs{}, err
			}

			cfg, err := config.Load(inputs.EnvPath, inputs.ConfigPath)
			if err != nil {
				return nil, inputs, err
			}

			return cfg, inputs, nil
		},
		ApplyConfigOverrides: testApplyDaemonConfigOverrides,
		AddStartupFilesystemRoots: func(cfg *config.Config, inputs ConfigInputs) {
			roots := make([]string, 0, 2)
			if !cfg.FS.NoProfileAccess {
				roots = append(roots, inputs.Profile.HomeDir)
			}
			if cwd, err := os.Getwd(); err == nil {
				roots = append(roots, cwd)
			}
			config.AddFilesystemRoots(cfg, roots...)
		},
		SafetySummary: func(cfg *config.Config) string {
			return "input=" + daemonSafetyLabel(cfg.InputSafetyEnabled()) +
				", output=" + daemonSafetyLabel(cfg.OutputSafetyEnabled()) +
				", pii=" + daemonSafetyLabel(cfg.OutputPIIRedactionEnabled())
		},
	}
}

func testDaemonConfigInputs(cmd *urfavecli.Command) (ConfigInputs, error) {
	resolved := profile.Active()
	homeDirValue := str.String(resolved.HomeDir)
	if homeDirValue.Trim() == "" {
		var err error
		resolved, err = profile.Resolve(profile.ResolveOptions{})
		if err != nil {
			return ConfigInputs{}, err
		}
	}

	resolved = profile.WithMetadataPaths(resolved)
	profile.SetActive(resolved)
	inputs := ConfigInputs{
		Profile:    resolved,
		EnvPath:    resolved.EnvPath,
		ConfigPath: resolved.ConfigPath,
	}
	if value, ok := testCommandString(cmd, "config"); ok {
		inputs.ConfigPath = value
	}

	return inputs, nil
}

func testApplyDaemonConfigOverrides(cmd *urfavecli.Command, cfg *config.Config) {
	if value, ok := testCommandString(cmd, "name"); ok {
		cfg.Name = value
	}
	if value, ok := testCommandString(cmd, "model"); ok {
		cfg.Models.Main.Name = value
	}
	if value, ok := testCommandString(cmd, "model.provider"); ok {
		cfg.Models.Main.Provider = value
	}
	if value, ok := testCommandString(cmd, "model.base-url"); ok {
		cfg.Models.Main.BaseURL = value
	}
	if value, ok := testCommandString(cmd, "model.summary-provider"); ok {
		cfg.Models.Summary.Provider = value
	}
	if value, ok := testCommandString(cmd, "model.summary-base-url"); ok {
		cfg.Models.Summary.BaseURL = value
	}
	if value, ok := testCommandString(cmd, "model.api-key"); ok {
		providerValue := str.String(cfg.Models.Main.Provider)
		provider := providerValue.Trim()
		if provider == "" {
			provider = constants.DefaultModelProvider
		}
		if cfg.Models.Providers == nil {
			cfg.Models.Providers = map[string]config.ProviderModelConfig{}
		}
		providerCfg := cfg.Models.Providers[provider]
		providerCfg.APIKey = value
		cfg.Models.Providers[provider] = providerCfg
	}
	if value, ok := testCommandString(cmd, "rpc.address"); ok {
		cfg.RPC.Address = value
	}
	if value, ok := testCommandInt(cmd, "rpc.port"); ok {
		cfg.RPC.Port = value
	}
	if value, ok := testCommandBool(cmd, "gateway.enabled"); ok {
		cfg.Gateway.Enabled = value
	}
	if value, ok := testCommandBool(cmd, "gateway.telegram.enabled"); ok {
		cfg.Gateway.Telegram.Enabled = value
	}
	if value, ok := testCommandString(cmd, "log.level"); ok {
		cfg.Log.Level = value
	}
	if value, ok := testCommandBool(cmd, "trace.enabled"); ok {
		cfg.Trace.Enabled = value
	}
	if value, ok := testCommandString(cmd, "trace.disk.dir"); ok {
		cfg.Trace.Disk.Dir = value
	}
}

func testCommandString(cmd *urfavecli.Command, name string) (string, bool) {
	for _, candidate := range cmd.Lineage() {
		if candidate.IsSet(name) {
			nameValue := str.String(candidate.String(name))
			return nameValue.Trim(), true
		}
	}

	return "", false
}

func testCommandInt(cmd *urfavecli.Command, name string) (int, bool) {
	for _, candidate := range cmd.Lineage() {
		if candidate.IsSet(name) {
			return candidate.Int(name), true
		}
	}

	return 0, false
}

func testCommandBool(cmd *urfavecli.Command, name string) (bool, bool) {
	for _, candidate := range cmd.Lineage() {
		if candidate.IsSet(name) {
			return candidate.Bool(name), true
		}
	}

	return false, false
}

func daemonSafetyLabel(enabled bool) string {
	if enabled {
		return "enabled"
	}

	return "disabled"
}

func isolateCommandProfile(t *testing.T) {
	t.Helper()

	originalProfile := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(originalProfile)
	})
	profile.SetActive(profile.Profile{})
	t.Setenv("HOME", t.TempDir())
	clearDaemonEnv(
		t,
		profile.EnvName,
		"MORPH_ENV_FILE",
		"MORPH_CONFIG",
		"MORPH_NAME",
		"MORPH_MODEL",
		"MORPH_MODEL_PROVIDER",
		"MORPH_MODEL_API",
		"MORPH_MODEL_API_KEY",
		"MORPH_MODEL_BASE_URL",
		"MORPH_MODEL_MAX_RETRIES",
		"MORPH_MODEL_SUMMARY",
		"MORPH_MODEL_SUMMARY_PROVIDER",
		"MORPH_MODEL_SUMMARY_API",
		"MORPH_MODEL_SUMMARY_API_KEY",
		"MORPH_MODEL_SUMMARY_BASE_URL",
		"MORPH_RPC_ADDRESS",
		"MORPH_RPC_PORT",
		"MORPH_LOG_LEVEL",
		"MORPH_LOG_NO_COLOR",
		"MORPH_DEBUG_REQUESTS",
		"MORPH_SAFETY_INPUT",
		"MORPH_SAFETY_OUTPUT",
		"MORPH_SAFETY_PII",
	)
}

func stubDaemonStartup(t *testing.T) func() {
	t.Helper()

	isolateCommandProfile(t)
	originalFactory := modelClientFactory
	originalRunner := newAgentRunner
	originalServe := serveRPC
	originalOpenRPCListener := openRPCListener
	originalOutput := startupOutput

	modelClientFactory = modelClientFactoryStub{
		newClient: func(modelclient.ClientRequest) (models.Client, error) {
			return &provider_openai.OpenAIClient{}, nil
		},
	}
	newAgentRunner = func(context.Context, *config.Config, models.Client, models.Client, models.Client) agentRunner {
		return &agentstub.AgentRunnerStub{}
	}
	openRPCListener = func(*config.Config) (net.Listener, error) {
		return noopListener{}, nil
	}
	startupOutput = io.Discard

	return func() {
		modelClientFactory = originalFactory
		newAgentRunner = originalRunner
		serveRPC = originalServe
		openRPCListener = originalOpenRPCListener
		startupOutput = originalOutput
	}
}

func stubConfigWatcher() (chan fsnotify.Event, chan error, func()) {
	originalWatcher := newConfigWatcher
	events := make(chan fsnotify.Event, 16)
	errors := make(chan error, 16)
	newConfigWatcher = func(string) (configWatcher, error) {
		return configWatcher{
			events: events,
			errors: errors,
			close:  func() error { return nil },
		}, nil
	}

	return events, errors, func() {
		newConfigWatcher = originalWatcher
	}
}

func newUpTestConfig(name string) *config.Config {
	cfg := config.NewDefaultConfig()
	cfg.Name = name
	cfg.Models.Main.Name = "gpt-4o-mini"
	cfg.Models.Main.Provider = "openrouter"
	cfg.Models.Providers = map[string]config.ProviderModelConfig{
		"openrouter": {APIKey: "test-key"},
	}
	cfg.RPC.Address = "127.0.0.1"
	cfg.RPC.Port = 0
	cfg.Log.NoColor = true
	cfg.Normalize()

	return cfg
}

func writeUpTestConfig(t *testing.T, path string, name string) {
	t.Helper()

	writeConfigFile(t, path, newUpTestConfig(name))
}

func writeConfigFile(t *testing.T, path string, cfg *config.Config) {
	t.Helper()

	data, err := cfg.ToYAML()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, data, 0o600))
}

func clearDaemonEnv(t *testing.T, keys ...string) {
	t.Helper()
	keys = append(keys, "OPENAI_API_KEY", "OPENROUTER_API_KEY", "ANTHROPIC_API_KEY", "COPILOT_GITHUB_TOKEN")

	for _, key := range keys {
		original, ok := os.LookupEnv(key)
		if ok {
			t.Cleanup(func() {
				require.NoError(t, os.Setenv(key, original))
			})
		} else {
			t.Cleanup(func() {
				require.NoError(t, os.Unsetenv(key))
			})
		}
		require.NoError(t, os.Unsetenv(key))
	}
}

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(value string) string {
	return ansiPattern.ReplaceAllString(value, "")
}

func newOpenRouterModelsServer(t *testing.T, model string) string {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/models", r.URL.Path)
		_, _ = w.Write([]byte(`{"data":[{"id":"` + model + `","context_length":128000}]}`))
	}))
	t.Cleanup(server.Close)

	return server.URL
}
