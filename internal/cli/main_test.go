package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	urfavecli "github.com/urfave/cli/v3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/constants"
	agentstub "github.com/xymorphic/morph/internal/mocks/agentstub"
	modelprovider "github.com/xymorphic/morph/internal/model/provider"
	provider_ollama "github.com/xymorphic/morph/internal/model/provider_ollama"
	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/internal/profile"
	rpcclient "github.com/xymorphic/morph/internal/rpc/client"
	"github.com/xymorphic/morph/internal/rpc/rpcmeta"
	"github.com/xymorphic/morph/internal/runtime"
	"github.com/xymorphic/morph/internal/trace"
	agent "github.com/xymorphic/morph/pkg/agent"
	agentsession "github.com/xymorphic/morph/pkg/agent/session"
	"github.com/xymorphic/morph/pkg/logutils"
)

func TestNewMainAction_TreatsUnknownArgsAsChat(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "OPENROUTER_API_KEY", "MORPH_MODEL_BASE_URL",
		"MORPH_LOG_LEVEL", "MORPH_LOG_NO_COLOR", "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetMainActionState(t)

	var output bytes.Buffer
	stub := &agentstub.AgentServiceStub{Reply: "hello back"}
	cmd := newMainActionTestCommand(&output, func(context.Context, *config.Config) (rpcclient.ChatClient, error) {
		return stub, nil
	})

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--name", "flag-agent",
		"--rpc.address", "127.0.0.1",
		"--rpc.port", "50051",
		"hello",
		"world",
	})
	require.NoError(t, err)
	require.Equal(t, "hello world", stub.ChatInput)
	outgoingMetadata, ok := metadata.FromOutgoingContext(stub.RespondContext)
	require.True(t, ok)
	incoming := metadata.NewIncomingContext(context.Background(), outgoingMetadata)
	require.Equal(t, permissions.SurfaceCLI, rpcmeta.PermissionSurfaceFromIncomingContext(incoming))
	preset, ok := rpcmeta.PermissionPresetFromIncomingContext(incoming)
	require.True(t, ok)
	require.Equal(t, permissions.PresetCustom, preset)
	require.Empty(t, stub.RespondOptions.Instruct)
	require.Equal(t, "default", stub.EnqueuedMessage.SessionID)
	require.True(t, stub.Closed)
	require.Equal(t, "hello back\n\n\x1b[90mWorked for 0s\x1b[0m\n", output.String())
}

func TestNewMainAction_ShowsHelpForEmptyMessageWithDefaultOptions(t *testing.T) {
	cmd := &urfavecli.Command{
		Name:   "morph",
		Action: NewMainAction(MainActionOptions{}),
	}

	err := cmd.Run(context.Background(), []string{"morph", "  "})

	require.NoError(t, err)
}

func TestNewMainAction_ReturnsLoadConfigError(t *testing.T) {
	clearEnv(t, "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetMainActionState(t)

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("models:\n  main:\n    name: ["), 0o600))

	cmd := newMainActionTestCommandWithOptions(io.Discard, MainActionOptions{
		EnsureDaemonRunning: func(context.Context, *config.Config) (func() error, error) {
			t.Fatal("daemon should not start when config loading fails")
			return nil, nil
		},
	})

	err := cmd.Run(context.Background(), []string{"morph", "--config", configPath, "hello"})

	require.Error(t, err)
	require.ErrorContains(t, err, "failed to parse config file")
}

func TestNewMainAction_ReturnsRuntimeEndpointError(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_CONFIG", "MORPH_ENV_FILE", "MORPH_RPC_ADDRESS", "MORPH_RPC_PORT")
	resetMainActionState(t)

	active := profile.WithMetadataPaths(profile.Profile{Name: "work", HomeDir: t.TempDir()})
	profile.SetActive(active)
	require.NoError(t, os.WriteFile(active.RuntimePath, []byte(`{`), 0o600))

	cmd := newMainActionTestCommandWithOptions(io.Discard, MainActionOptions{
		EnsureDaemonRunning: func(context.Context, *config.Config) (func() error, error) {
			t.Fatal("daemon should not start when runtime endpoint cannot be resolved")
			return nil, nil
		},
	})

	err := cmd.Run(context.Background(), []string{"morph", "--name", "flag-agent", "hello"})

	require.ErrorContains(t, err, "parse runtime metadata")
}

func TestNewMainAction_UsesProfileConfigAndEnv(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "OPENROUTER_API_KEY", "MORPH_LOG_LEVEL",
		"MORPH_CONFIG", "MORPH_ENV_FILE", "MORPH_PROFILE")
	resetMainActionState(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	profileHome := filepath.Join(home, ".morph", "profiles", "work")
	require.NoError(t, os.MkdirAll(profileHome, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(profileHome, "config.yaml"), []byte(`
name: profile-agent
models:
`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(profileHome, ".env"), []byte("MORPH_LOG_LEVEL=debug\n"), 0o600))

	var got *config.Config
	stub := &agentstub.AgentServiceStub{Reply: "hello back"}
	cmd := newMainActionTestCommand(io.Discard, func(_ context.Context, cfg *config.Config) (rpcclient.ChatClient, error) {
		got = cfg
		return stub, nil
	})

	err := cmd.Run(context.Background(), []string{"morph", "--profile", "Work", "hello"})

	require.NoError(t, err)
	require.Equal(t, "hello", stub.ChatInput)
	require.NotNil(t, got)
	require.Equal(t, "profile-agent", got.Name)
	require.Equal(t, "debug", got.Log.Level)
}

func TestNewMainAction_UsesProfileRuntimeEndpoint(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "OPENROUTER_API_KEY", "MORPH_LOG_LEVEL",
		"MORPH_CONFIG", "MORPH_ENV_FILE", "MORPH_PROFILE", "MORPH_RPC_ADDRESS", "MORPH_RPC_PORT")
	resetMainActionState(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, listener.Close())
	})

	profileHome := filepath.Join(home, ".morph", "profiles", "work")
	require.NoError(t, os.MkdirAll(profileHome, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(profileHome, "config.yaml"), []byte(`
name: profile-agent
models:
`), 0o600))
	profile.SetActive(profile.WithMetadataPaths(profile.Profile{Name: "work", HomeDir: profileHome}))
	port := listener.Addr().(*net.TCPAddr).Port
	_, err = runtime.WriteActive("127.0.0.1", port)
	require.NoError(t, err)

	var got *config.Config
	stub := &agentstub.AgentServiceStub{Reply: "hello back"}
	cmd := newMainActionTestCommand(io.Discard, func(_ context.Context, cfg *config.Config) (rpcclient.ChatClient, error) {
		got = cfg
		return stub, nil
	})

	err = cmd.Run(context.Background(), []string{"morph", "--profile", "Work", "hello"})

	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "127.0.0.1", got.RPC.Address)
	require.Equal(t, port, got.RPC.Port)
}

func TestNewMainAction_ForwardsInstruct(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "OPENROUTER_API_KEY", "MORPH_MODEL_BASE_URL",
		"MORPH_LOG_LEVEL", "MORPH_LOG_NO_COLOR", "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetMainActionState(t)

	stub := &agentstub.AgentServiceStub{Reply: "hello back"}
	cmd := newMainActionTestCommand(io.Discard, func(context.Context, *config.Config) (rpcclient.ChatClient, error) {
		return stub, nil
	})

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--name", "flag-agent",
		"--instruct", "be terse",
		"hello",
	})
	require.NoError(t, err)
	require.Equal(t, "hello", stub.EnqueuedMessage.Message)
}

func TestNewMainAction_ForwardsSessionID(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "OPENROUTER_API_KEY", "MORPH_MODEL_BASE_URL",
		"MORPH_LOG_LEVEL", "MORPH_LOG_NO_COLOR", "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetMainActionState(t)

	stub := &agentstub.AgentServiceStub{Reply: "hello back"}
	cmd := newMainActionTestCommand(io.Discard, func(context.Context, *config.Config) (rpcclient.ChatClient, error) {
		return stub, nil
	})

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--name", "flag-agent",
		"--session", "project-a",
		"hello",
	})
	require.NoError(t, err)
	require.Equal(t, "project-a", stub.EnqueuedMessage.SessionID)
}

func TestNewMainAction_StreamsOutput(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "OPENROUTER_API_KEY", "MORPH_MODEL_BASE_URL",
		"MORPH_LOG_LEVEL", "MORPH_LOG_NO_COLOR", "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetMainActionState(t)

	var output bytes.Buffer
	stub := &agentstub.AgentServiceStub{Reply: "hello back", Deltas: []string{"hello ", "back"}}
	cmd := newMainActionTestCommand(&output, func(context.Context, *config.Config) (rpcclient.ChatClient, error) {
		return stub, nil
	})

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--name", "flag-agent",
		"--model.stream=true",
		"hello",
	})
	require.NoError(t, err)
	require.Equal(t, "hello back\n\n\x1b[90mWorked for 0s\x1b[0m\n", output.String())
}

func TestNewMainAction_LabelsReasoningAndWorkDuration(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "OPENROUTER_API_KEY", "MORPH_MODEL_BASE_URL",
		"MORPH_LOG_LEVEL", "MORPH_LOG_NO_COLOR", "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetMainActionState(t)

	var output bytes.Buffer
	stub := &agentstub.AgentServiceStub{
		Reply: "thinking done",
		Events: []rpcclient.Event{
			{Kind: agent.EventKindTextDelta, Channel: "reasoning", Text: "thinking"},
			{Kind: agent.EventKindTextDelta, Channel: "assistant", Text: " done"},
		},
	}
	cmd := newMainActionTestCommandWithOptions(&output, MainActionOptions{
		NewChatClient: func(context.Context, *config.Config) (rpcclient.ChatClient, error) {
			return stub, nil
		},
		Now: sequenceClock(
			time.Unix(0, 0),
			time.Unix(0, 0),
			time.Unix(5, 0),
			time.Unix(120, 0),
		),
	})

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--name", "flag-agent",
		"--model.stream=true",
		"hello",
	})
	require.NoError(t, err)
	require.Equal(t, "thinking done\n\n\x1b[90mWorked for 0s\x1b[0m\n", output.String())
}

func TestNewMainAction_StylesReasoningWhenOnlyLogNoColor(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "OPENROUTER_API_KEY", "MORPH_MODEL_BASE_URL",
		"MORPH_LOG_LEVEL", "MORPH_LOG_NO_COLOR", "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetMainActionState(t)

	var output bytes.Buffer
	stub := &agentstub.AgentServiceStub{
		Reply: "thinking done",
		Events: []rpcclient.Event{
			{Kind: agent.EventKindTextDelta, Channel: "reasoning", Text: "thinking"},
			{Kind: agent.EventKindTextDelta, Channel: "assistant", Text: " done"},
		},
	}
	cmd := newMainActionTestCommand(&output, func(context.Context, *config.Config) (rpcclient.ChatClient, error) {
		return stub, nil
	})

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--name", "flag-agent",
		"--model.stream=true",
		"--log.no-color=true",
		"hello",
	})
	require.NoError(t, err)
	require.Equal(t, "thinking done\n\n\x1b[90mWorked for 0s\x1b[0m\n", output.String())
}

func TestNewMainAction_DoesNotStyleReasoningWithNoColorAlias(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "OPENROUTER_API_KEY", "MORPH_MODEL_BASE_URL",
		"MORPH_LOG_LEVEL", "MORPH_LOG_NO_COLOR", "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetMainActionState(t)

	var output bytes.Buffer
	stub := &agentstub.AgentServiceStub{
		Reply: "thinking done",
		Events: []rpcclient.Event{
			{Kind: agent.EventKindTextDelta, Channel: "reasoning", Text: "thinking"},
			{Kind: agent.EventKindTextDelta, Channel: "assistant", Text: " done"},
		},
	}
	cmd := newMainActionTestCommand(&output, func(context.Context, *config.Config) (rpcclient.ChatClient, error) {
		return stub, nil
	})

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--name", "flag-agent",
		"--model.stream=true",
		"--no-color",
		"hello",
	})

	require.NoError(t, err)
	require.NotContains(t, output.String(), "\x1b[")
	require.Equal(t, "thinking done\n\nWorked for 0s\n", output.String())
}

func TestChatStreamFormatter_CompletesReasoningBoundaryAfterOneNewline(t *testing.T) {
	formatter := newChatStreamFormatter(config.NewDefaultConfig(), sequenceClock(
		time.Unix(0, 0),
		time.Unix(0, 0),
		time.Unix(5, 0),
	), false)

	output := formatter.Format(rpcclient.Event{
		Kind:    agent.EventKindTextDelta,
		Channel: "reasoning",
		Text:    "thinking\n",
	})
	output += formatter.Format(rpcclient.Event{
		Kind:    agent.EventKindTextDelta,
		Channel: "assistant",
		Text:    "done",
	})

	require.Equal(t, "\x1b[90mthinking\n\x1b[0m\n\x1b[90mThought for 5s\x1b[0m\n\ndone", output)
}

func TestChatStreamFormatter_DoesNotAddBoundaryAfterTwoReasoningNewlines(t *testing.T) {
	formatter := newChatStreamFormatter(config.NewDefaultConfig(), sequenceClock(
		time.Unix(0, 0),
		time.Unix(0, 0),
		time.Unix(5, 0),
	), false)

	output := formatter.Format(rpcclient.Event{
		Kind:    agent.EventKindTextDelta,
		Channel: "reasoning",
		Text:    "thinking\n\n",
	})
	output += formatter.Format(rpcclient.Event{
		Kind:    agent.EventKindTextDelta,
		Channel: "assistant",
		Text:    "done",
	})

	require.Equal(t, "\x1b[90mthinking\n\n\x1b[0m\x1b[90mThought for 5s\x1b[0m\n\ndone", output)
}

func TestNewMainAction_IgnoresTraceEvents(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "OPENROUTER_API_KEY", "MORPH_MODEL_BASE_URL",
		"MORPH_LOG_LEVEL", "MORPH_LOG_NO_COLOR", "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetMainActionState(t)

	var output bytes.Buffer
	traceEvent := trace.Event{Type: trace.EvtInputSafetyBlocked, Payload: map[string]any{"blocked": true}}
	stub := &agentstub.AgentServiceStub{
		Reply: "hello back",
		Events: []rpcclient.Event{
			{Kind: agent.EventKindTrace, TraceEvent: &traceEvent},
			{Kind: agent.EventKindTextDelta, Channel: "assistant", Text: "hello back"},
		},
	}
	cmd := newMainActionTestCommand(&output, func(context.Context, *config.Config) (rpcclient.ChatClient, error) {
		return stub, nil
	})

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--name", "flag-agent",
		"--model.stream=true",
		"hello",
	})
	require.NoError(t, err)
	require.Equal(t, "hello back\n\n\x1b[90mWorked for 0s\x1b[0m\n", output.String())
}

func TestNewMainAction_ResolvesPermissionApprovalFromInteractiveRootChat(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "OPENROUTER_API_KEY", "MORPH_MODEL_BASE_URL",
		"MORPH_LOG_LEVEL", "MORPH_LOG_NO_COLOR", "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetMainActionState(t)

	var output bytes.Buffer
	api := &mainActionPermissionAPIStub{}
	traceEvent := trace.Event{
		Type: trace.EvtPermissionApprovalChanged,
		Payload: trace.PermissionApprovalPayload{
			RequestID: "approval_1",
			Status:    string(permissions.ApprovalPending),
			Effects:   []string{string(permissions.EffectWrite)},
			Summary:   "write_file · update file",
			Reason:    "file write requires approval",
			ExpiresAt: time.Now().Add(time.Minute),
		},
	}
	stub := &agentstub.AgentServiceStub{
		Reply: "file updated",
		Events: []rpcclient.Event{
			{Kind: agent.EventKindTrace, TraceEvent: &traceEvent},
			{Kind: agent.EventKindTextDelta, Channel: "assistant", Text: "file updated"},
		},
	}
	client := &mainActionPermissionClient{AgentServiceStub: stub, api: api}
	cmd := newMainActionTestCommandWithOptions(&output, MainActionOptions{
		Input: strings.NewReader("y\n"),
		IsInteractive: func(io.Reader, io.Writer) bool {
			return true
		},
		NewChatClient: func(context.Context, *config.Config) (rpcclient.ChatClient, error) {
			return client, nil
		},
	})

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--name", "flag-agent",
		"--model.stream=true",
		"hello",
	})

	require.NoError(t, err)
	require.Empty(t, api.requestID)
	require.Contains(t, output.String(), "file updated")
}

func TestNewMainAction_ReturnsApprovalRequiredForNonInteractiveRootChat(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "OPENROUTER_API_KEY", "MORPH_MODEL_BASE_URL",
		"MORPH_LOG_LEVEL", "MORPH_LOG_NO_COLOR", "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetMainActionState(t)

	var output bytes.Buffer
	api := &mainActionPermissionAPIStub{}
	traceEvent := trace.Event{
		Type: trace.EvtPermissionApprovalChanged,
		Payload: map[string]any{
			"request_id":        "approval_1",
			"status":            string(permissions.ApprovalPending),
			"operation_summary": "write_file · update file",
		},
	}
	stub := &agentstub.AgentServiceStub{
		Reply:  "approval pending",
		Events: []rpcclient.Event{{Kind: agent.EventKindTrace, TraceEvent: &traceEvent}},
	}
	client := &mainActionPermissionClient{AgentServiceStub: stub, api: api}
	cmd := newMainActionTestCommandWithOptions(&output, MainActionOptions{
		Input: strings.NewReader(""),
		IsInteractive: func(io.Reader, io.Writer) bool {
			return false
		},
		NewChatClient: func(context.Context, *config.Config) (rpcclient.ChatClient, error) {
			return client, nil
		},
	})

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--name", "flag-agent",
		"--model.stream=false",
		"hello",
	})

	require.NoError(t, err)
	require.Empty(t, api.requestID)
	require.Equal(t, "approval pending\n", output.String())
}

func TestRootChatApprovalHandler_DeniesAndHidesUnsafeAlwaysApproval(t *testing.T) {
	var output bytes.Buffer
	api := &mainActionPermissionAPIStub{}
	handler := newRootChatApprovalHandler(strings.NewReader("a\nn\n"), &output, api, true)
	traceEvent := trace.Event{
		Type: trace.EvtPermissionApprovalChanged,
		Payload: trace.PermissionApprovalPayload{
			RequestID: "approval_1",
			Status:    string(permissions.ApprovalPending),
			Effects:   []string{string(permissions.EffectExecution)},
			Summary:   "run_command · execute process",
		},
	}

	handled, err := handler.Handle(context.Background(), rpcclient.Event{
		Kind:       agent.EventKindTrace,
		TraceEvent: &traceEvent,
	})

	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, "approval_1", api.requestID)
	require.False(t, api.approved)
	require.Empty(t, api.scope)
	require.NotContains(t, output.String(), "[a]")
	require.Contains(t, output.String(), "Choose y, s, n")
	require.Contains(t, output.String(), "Permission denied")
}

func TestRootChatApprovalHandler_DisplaysRelativeExpiry(t *testing.T) {
	now := time.Now()
	expiresAt := now.Add(2*time.Minute + time.Second)
	var output bytes.Buffer
	handler := newRootChatApprovalHandler(
		strings.NewReader("n\n"),
		&output,
		&mainActionPermissionAPIStub{},
		true,
	)
	handler.now = func() time.Time { return now }
	traceEvent := trace.Event{
		Type: trace.EvtPermissionApprovalChanged,
		Payload: trace.PermissionApprovalPayload{
			RequestID: "approval_1",
			Status:    string(permissions.ApprovalPending),
			Summary:   "write_file · update file",
			ExpiresAt: expiresAt,
		},
	}

	handled, err := handler.Handle(context.Background(), rpcclient.Event{
		Kind:       agent.EventKindTrace,
		TraceEvent: &traceEvent,
	})

	require.NoError(t, err)
	require.True(t, handled)
	require.Contains(t, output.String(), "  Expires    3m")
}

func TestRootChatApprovalHandler_DisplaysStructuredBatchOperations(t *testing.T) {
	var output bytes.Buffer
	handler := newRootChatApprovalHandler(
		strings.NewReader("y\n"),
		&output,
		&mainActionPermissionAPIStub{},
		true,
	)
	handler.width = 55
	traceEvent := trace.Event{
		Type: trace.EvtPermissionApprovalChanged,
		Payload: trace.PermissionApprovalPayload{
			RequestID: "approval_1",
			Status:    string(permissions.ApprovalPending),
			Summary:   "run_command · approve 3 operations",
			Reason: "command structure is incomplete and requires explicit approval. " +
				"Approve all 3 operations: legacy details",
			Operations: []string{
				"run_command · read file",
				"run_command · execute process export",
				"run_command · execute process printf",
			},
		},
	}

	handled, err := handler.Handle(context.Background(), rpcclient.Event{
		Kind:       agent.EventKindTrace,
		TraceEvent: &traceEvent,
	})

	require.NoError(t, err)
	require.True(t, handled)
	require.Contains(t, output.String(), "  Reason     command structure is incomplete and\n")
	require.Contains(t, output.String(), "             requires explicit approval.")
	require.Contains(t, output.String(), "  Operations\n")
	require.Contains(t, output.String(), "    1. run_command · read file\n")
	require.Contains(t, output.String(), "    3. run_command · execute process printf\n")
	require.Contains(t, output.String(), "[s] Allow for session")
}

func TestRootChatApprovalHandler_ReturnsResolutionFailure(t *testing.T) {
	expected := errors.New("approval store unavailable")
	handler := newRootChatApprovalHandler(
		strings.NewReader("s\n"),
		io.Discard,
		&mainActionPermissionAPIStub{err: expected},
		true,
	)
	traceEvent := trace.Event{
		Type: trace.EvtPermissionApprovalChanged,
		Payload: trace.PermissionApprovalPayload{
			RequestID: "approval_1",
			Status:    string(permissions.ApprovalPending),
			Summary:   "write_file · update file",
		},
	}

	handled, err := handler.Handle(context.Background(), rpcclient.Event{
		Kind:       agent.EventKindTrace,
		TraceEvent: &traceEvent,
	})

	require.True(t, handled)
	require.ErrorIs(t, err, expected)
	require.ErrorContains(t, err, "resolve permission approval")
}

func TestRootChatApprovalHandler_StopsWaitingWhenRequestExpires(t *testing.T) {
	input, writer := io.Pipe()
	t.Cleanup(func() {
		require.NoError(t, input.Close())
		require.NoError(t, writer.Close())
	})
	handler := newRootChatApprovalHandler(input, io.Discard, &mainActionPermissionAPIStub{}, true)
	traceEvent := trace.Event{
		Type: trace.EvtPermissionApprovalChanged,
		Payload: trace.PermissionApprovalPayload{
			RequestID: "approval_1",
			Status:    string(permissions.ApprovalPending),
			Summary:   "write_file · update file",
			ExpiresAt: time.Now().Add(-time.Second),
		},
	}

	handled, err := handler.Handle(context.Background(), rpcclient.Event{
		Kind:       agent.EventKindTrace,
		TraceEvent: &traceEvent,
	})

	require.True(t, handled)
	require.EqualError(t, err, "permission approval approval_1 expired")
}

func TestNewMainAction_ReturnsStreamRespondError(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "MORPH_MODEL_BASE_URL",
		"MORPH_LOG_LEVEL", "MORPH_LOG_NO_COLOR", "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetMainActionState(t)

	expectedErr := errors.New("stream failed")
	stub := &agentstub.AgentServiceStub{Reply: "partial", RespondErr: expectedErr}
	cmd := newMainActionTestCommand(io.Discard, func(context.Context, *config.Config) (rpcclient.ChatClient, error) {
		return stub, nil
	})

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--name", "flag-agent",
		"--model.stream=true",
		"hello",
	})

	require.ErrorIs(t, err, expectedErr)
}

func TestNewMainAction_ReturnsStreamFinishWriteError(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "MORPH_MODEL_BASE_URL",
		"MORPH_LOG_LEVEL", "MORPH_LOG_NO_COLOR", "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetMainActionState(t)

	stub := &agentstub.AgentServiceStub{Reply: "hello back"}
	cmd := newMainActionTestCommand(failingWriter{}, func(context.Context, *config.Config) (rpcclient.ChatClient, error) {
		return stub, nil
	})

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--name", "flag-agent",
		"--model.stream=true",
		"hello",
	})

	require.ErrorIs(t, err, io.ErrClosedPipe)
}

func TestNewMainAction_ReturnsNonStreamRespondError(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "MORPH_MODEL_BASE_URL",
		"MORPH_LOG_LEVEL", "MORPH_LOG_NO_COLOR", "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetMainActionState(t)

	expectedErr := errors.New("respond failed")
	stub := &agentstub.AgentServiceStub{RespondErr: expectedErr}
	cmd := newMainActionTestCommand(io.Discard, func(context.Context, *config.Config) (rpcclient.ChatClient, error) {
		return stub, nil
	})

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--name", "flag-agent",
		"--model.stream=false",
		"hello",
	})

	require.ErrorIs(t, err, expectedErr)
}

func TestNewMainAction_ReturnsNonStreamWriteError(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "MORPH_MODEL_BASE_URL",
		"MORPH_LOG_LEVEL", "MORPH_LOG_NO_COLOR", "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetMainActionState(t)

	stub := &agentstub.AgentServiceStub{Reply: "hello back"}
	cmd := newMainActionTestCommand(failingWriter{}, func(context.Context, *config.Config) (rpcclient.ChatClient, error) {
		return stub, nil
	})

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--name", "flag-agent",
		"--model.stream=false",
		"hello",
	})

	require.ErrorIs(t, err, io.ErrClosedPipe)
}

func TestNewMainAction_DoesNotForwardConfiguredInstruct(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "OPENROUTER_API_KEY", "MORPH_MODEL_BASE_URL",
		"MORPH_LOG_LEVEL", "MORPH_LOG_NO_COLOR", "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetMainActionState(t)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: config-agent
models:
  providers:
    openrouter:
      apiKey: config-key
  main:
    name: gpt-4o-mini
    provider: openrouter
session:
  instruct: be terse
`), 0o600))

	stub := &agentstub.AgentServiceStub{Reply: "hello back"}
	cmd := newMainActionTestCommand(io.Discard, func(context.Context, *config.Config) (rpcclient.ChatClient, error) {
		return stub, nil
	})

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--config", configPath,
		"hello",
	})
	require.NoError(t, err)
	require.Empty(t, stub.RespondOptions.Instruct)
}

func TestNewMainAction_EnsuresDaemonAndCleansStartedRuntime(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetMainActionState(t)

	started := false
	cleaned := false
	stub := &agentstub.AgentServiceStub{Reply: "hello back"}
	cmd := newMainActionTestCommandWithOptions(io.Discard, MainActionOptions{
		NewChatClient: func(context.Context, *config.Config) (rpcclient.ChatClient, error) {
			require.True(t, started)
			require.False(t, cleaned)
			return stub, nil
		},
		EnsureDaemonRunning: func(context.Context, *config.Config) (func() error, error) {
			started = true
			return func() error {
				cleaned = true
				return nil
			}, nil
		},
	})

	err := cmd.Run(context.Background(), []string{"morph", "--name", "flag-agent", "hello"})

	require.NoError(t, err)
	require.True(t, cleaned)
	require.True(t, stub.Closed)
}

func TestNewMainAction_PullsOllamaModelBeforeStartingDaemon(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "MORPH_MODEL_BASE_URL",
		"MORPH_CONFIG", "MORPH_ENV_FILE")
	resetMainActionState(t)

	pulled := false
	started := false
	var output bytes.Buffer
	stub := &agentstub.AgentServiceStub{Reply: "hello back"}
	cmd := newMainActionTestCommandWithOptions(&output, MainActionOptions{
		PullOllamaModel: func(
			_ context.Context,
			baseURL string,
			model string,
			headers map[string]string,
			onProgress func(provider_ollama.PullProgress),
		) error {
			require.False(t, started)
			require.Equal(t, constants.DefaultOllamaBaseURL, baseURL)
			require.Equal(t, "qwen3:8b", model)
			require.Nil(t, headers)
			require.NotNil(t, onProgress)
			onProgress(provider_ollama.PullProgress{Status: "pulling manifest"})
			onProgress(provider_ollama.PullProgress{Status: "downloading", Completed: 25, Total: 100})
			pulled = true
			return nil
		},
		EnsureDaemonRunning: func(context.Context, *config.Config) (func() error, error) {
			require.True(t, pulled)
			started = true
			return nil, nil
		},
		NewChatClient: func(context.Context, *config.Config) (rpcclient.ChatClient, error) {
			require.True(t, started)
			stub.RuntimeModelResult = rpcclient.ModelRuntime{
				Provider: constants.ModelProviderOllama,
				API:      modelprovider.APIOllamaNative,
				Model:    "qwen3:8b",
				BaseURL:  constants.DefaultOllamaBaseURL,
			}
			return stub, nil
		},
	})

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--provider", "ollama",
		"--model", "qwen3:8b",
		"--pull",
		"hello",
	})

	require.NoError(t, err)
	require.True(t, pulled)
	require.Equal(t, "hello", stub.ChatInput)
	require.Equal(t, strings.Join([]string{
		"Ollama pull: pulling manifest",
		"Ollama pull: downloading 25%",
		"hello back",
		"",
		"\x1b[90mWorked for 0s\x1b[0m",
		"",
	}, "\n"), output.String())
}

func TestNewMainAction_PullQuietSuppressesProgressOutput(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "MORPH_MODEL_BASE_URL",
		"MORPH_CONFIG", "MORPH_ENV_FILE")
	resetMainActionState(t)

	var output bytes.Buffer
	stub := &agentstub.AgentServiceStub{Reply: "hello back"}
	cmd := newMainActionTestCommandWithOptions(&output, MainActionOptions{
		PullOllamaModel: func(
			_ context.Context,
			_ string,
			_ string,
			_ map[string]string,
			onProgress func(provider_ollama.PullProgress),
		) error {
			require.Nil(t, onProgress)
			return nil
		},
		NewChatClient: func(context.Context, *config.Config) (rpcclient.ChatClient, error) {
			stub.RuntimeModelResult = rpcclient.ModelRuntime{
				Provider: constants.ModelProviderOllama,
				API:      modelprovider.APIOllamaNative,
				Model:    "qwen3:8b",
				BaseURL:  constants.DefaultOllamaBaseURL,
			}
			return stub, nil
		},
	})

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--provider", "ollama",
		"--model", "qwen3:8b",
		"--pull",
		"--pull-quiet",
		"hello",
	})

	require.NoError(t, err)
	require.Equal(t, "hello back\n\n\x1b[90mWorked for 0s\x1b[0m\n", output.String())
}

func TestNewMainAction_PullRejectsNonOllamaProvider(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "MORPH_MODEL_BASE_URL",
		"MORPH_CONFIG", "MORPH_ENV_FILE")
	resetMainActionState(t)

	cmd := newMainActionTestCommandWithOptions(io.Discard, MainActionOptions{
		PullOllamaModel: func(
			context.Context,
			string,
			string,
			map[string]string,
			func(provider_ollama.PullProgress),
		) error {
			t.Fatal("pull should not run for non-Ollama providers")
			return nil
		},
		EnsureDaemonRunning: func(context.Context, *config.Config) (func() error, error) {
			t.Fatal("daemon should not start when --pull is invalid")
			return nil, nil
		},
	})

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--provider", "openrouter",
		"--pull",
		"hello",
	})

	require.EqualError(t, err, `--pull is only supported with provider "ollama"`)
}

func TestNewMainAction_RejectsInvalidModelAPIOverride(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "MORPH_MODEL_BASE_URL",
		"MORPH_CONFIG", "MORPH_ENV_FILE")
	resetMainActionState(t)

	cmd := newMainActionTestCommandWithOptions(io.Discard, MainActionOptions{
		EnsureDaemonRunning: func(context.Context, *config.Config) (func() error, error) {
			t.Fatal("daemon should not start when --model.api is invalid")
			return nil, nil
		},
		NewChatClient: func(context.Context, *config.Config) (rpcclient.ChatClient, error) {
			t.Fatal("chat client should not be created when --model.api is invalid")
			return nil, nil
		},
	})

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--provider", "ollama",
		"--model", "lfm2.5-thinking",
		"--base-url", constants.DefaultOllamaBaseURL,
		"--model.api", "wrong",
		"hello",
	})

	require.EqualError(t, err, "model API must be one of: anthropic-messages, ollama-native, openai-completions, openai-responses")
}

func TestValidateRootChatModelConfigRejectsNil(t *testing.T) {
	err := validateRootChatModelConfig(nil)

	require.EqualError(t, err, "config is required")
}

func TestNewMainAction_RejectsExplicitModelOverrideWhenDaemonModelDiffers(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "MORPH_MODEL_BASE_URL",
		"MORPH_CONFIG", "MORPH_ENV_FILE")
	resetMainActionState(t)

	stub := &agentstub.AgentServiceStub{
		Reply: "should not respond",
		RuntimeModelResult: rpcclient.ModelRuntime{
			Provider: constants.ModelProviderOpenAI,
			API:      modelprovider.APIOpenAIResponses,
			Model:    "gpt-5.5",
			BaseURL:  constants.DefaultOpenAIBaseURL,
		},
	}
	cmd := newMainActionTestCommand(io.Discard, func(context.Context, *config.Config) (rpcclient.ChatClient, error) {
		return stub, nil
	})

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--provider", "ollama",
		"--model", "qwen3:8b",
		"hello",
	})

	require.ErrorContains(t, err, `running daemon uses provider="openai" model="gpt-5.5"`)
	require.ErrorContains(t, err, `requested provider="ollama" model="qwen3:8b"`)
	require.Empty(t, stub.ChatInput)
}

func TestNewMainAction_AllowsExplicitModelOverrideWhenDaemonModelMatches(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "MORPH_MODEL_BASE_URL",
		"MORPH_CONFIG", "MORPH_ENV_FILE")
	resetMainActionState(t)

	stub := &agentstub.AgentServiceStub{
		Reply: "hello back",
		RuntimeModelResult: rpcclient.ModelRuntime{
			Provider: constants.ModelProviderOllama,
			API:      modelprovider.APIOllamaNative,
			Model:    "qwen3:8b",
			BaseURL:  constants.DefaultOllamaBaseURL + "/",
		},
	}
	cmd := newMainActionTestCommand(io.Discard, func(context.Context, *config.Config) (rpcclient.ChatClient, error) {
		return stub, nil
	})

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--provider", "ollama",
		"--model", "qwen3:8b",
		"hello",
	})

	require.NoError(t, err)
	require.Equal(t, "hello", stub.ChatInput)
}

func TestNewMainAction_AllowsOllamaModelOverrideWhenLatestTagIsImplicit(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "MORPH_MODEL_BASE_URL",
		"MORPH_CONFIG", "MORPH_ENV_FILE")
	resetMainActionState(t)

	stub := &agentstub.AgentServiceStub{
		Reply: "hello back",
		RuntimeModelResult: rpcclient.ModelRuntime{
			Provider: constants.ModelProviderOllama,
			API:      modelprovider.APIOllamaNative,
			Model:    "lfm2.5-thinking:latest",
			BaseURL:  constants.DefaultOllamaBaseURL,
		},
	}
	cmd := newMainActionTestCommand(io.Discard, func(context.Context, *config.Config) (rpcclient.ChatClient, error) {
		return stub, nil
	})

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--provider", "ollama",
		"--model", "lfm2.5-thinking",
		"hello",
	})

	require.NoError(t, err)
	require.Equal(t, "hello", stub.ChatInput)
}

func TestNewMainAction_RejectsExplicitModelOverrideWhenDaemonIdentityUnavailable(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "MORPH_MODEL_BASE_URL",
		"MORPH_CONFIG", "MORPH_ENV_FILE")
	resetMainActionState(t)

	client := &chatOnlyClient{}
	cmd := newMainActionTestCommand(io.Discard, func(context.Context, *config.Config) (rpcclient.ChatClient, error) {
		return client, nil
	})

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--provider", "ollama",
		"--model", "qwen3:8b",
		"hello",
	})

	require.EqualError(t, err, "running daemon model identity is not available")
	require.Empty(t, client.message)
}

func TestNewMainAction_ReturnsDaemonIdentityError(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "MORPH_MODEL_BASE_URL",
		"MORPH_CONFIG", "MORPH_ENV_FILE")
	resetMainActionState(t)

	expectedErr := errors.New("identity unavailable")
	stub := &agentstub.AgentServiceStub{Err: expectedErr}
	cmd := newMainActionTestCommand(io.Discard, func(context.Context, *config.Config) (rpcclient.ChatClient, error) {
		return stub, nil
	})

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--provider", "ollama",
		"--model", "qwen3:8b",
		"hello",
	})

	require.ErrorIs(t, err, expectedErr)
	require.ErrorContains(t, err, "check running daemon model")
	require.Empty(t, stub.ChatInput)
}

func TestRootChatModelRuntimeHelpersHandleEmptyInputs(t *testing.T) {
	require.False(t, hasRootChatModelOverride(nil))
	require.Zero(t, rootChatModelRuntimeFromConfig(nil))

	runtime := normalizeRootChatModelRuntime(rpcclient.ModelRuntime{
		Provider:      " Ollama ",
		API:           " OLLAMA-NATIVE ",
		Model:         " qwen3:8b ",
		BaseURL:       " http://127.0.0.1:11434/ ",
		ContextLength: -1,
	})

	require.Equal(t, rpcclient.ModelRuntime{
		Provider: constants.ModelProviderOllama,
		API:      modelprovider.APIOllamaNative,
		Model:    "qwen3:8b",
		BaseURL:  constants.DefaultOllamaBaseURL,
	}, runtime)
}

func TestPullSelectedOllamaModel_RejectsInvalidInputs(t *testing.T) {
	err := pullSelectedOllamaModel(
		t.Context(),
		nil,
		func(context.Context, string, string, map[string]string, func(provider_ollama.PullProgress)) error {
			t.Fatal("pull should not run without config")
			return nil
		},
		nil,
	)
	require.EqualError(t, err, "config is required")

	err = pullSelectedOllamaModel(t.Context(), &config.Config{}, nil, nil)
	require.EqualError(t, err, "ollama puller is required")

	cfg := config.NewDefaultConfig()
	cfg.Models.Main.Provider = constants.ModelProviderOllama
	cfg.Models.Main.API = modelprovider.APIOpenAIResponses

	err = pullSelectedOllamaModel(
		t.Context(),
		cfg,
		func(context.Context, string, string, map[string]string, func(provider_ollama.PullProgress)) error {
			t.Fatal("pull should not run for unsupported Ollama API")
			return nil
		},
		nil,
	)
	require.EqualError(t, err, "--pull is only supported with Ollama chat APIs")

	profileHome := t.TempDir()
	require.NoError(t, os.Chmod(profileHome, 0o700))
	profile.SetActive(profile.Profile{HomeDir: profileHome})
	require.NoError(t, os.WriteFile(filepath.Join(profileHome, "auth.json"), []byte("{bad json"), 0o600))

	cfg.Models.Main.API = modelprovider.APIOllamaNative
	err = pullSelectedOllamaModel(
		t.Context(),
		cfg,
		func(context.Context, string, string, map[string]string, func(provider_ollama.PullProgress)) error {
			t.Fatal("pull should not run when model auth cannot be resolved")
			return nil
		},
		nil,
	)
	require.ErrorContains(t, err, "parse credential store")
}

func TestNewMainAction_PullProgressKeepsRecentLines(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_MODEL", "MORPH_MODEL_PROVIDER", "MORPH_MODEL_BASE_URL",
		"MORPH_CONFIG", "MORPH_ENV_FILE")
	resetMainActionState(t)

	var output bytes.Buffer
	stub := &agentstub.AgentServiceStub{Reply: "hello back"}
	cmd := newMainActionTestCommandWithOptions(&output, MainActionOptions{
		PullOllamaModel: func(
			_ context.Context,
			_ string,
			_ string,
			_ map[string]string,
			onProgress func(provider_ollama.PullProgress),
		) error {
			for _, progress := range []provider_ollama.PullProgress{
				{Status: "pulling manifest"},
				{Status: "pulling a", Completed: 10, Total: 100},
				{Status: "pulling b", Completed: 20, Total: 100},
				{Status: "pulling c", Completed: 30, Total: 100},
				{Status: "pulling c", Completed: 30, Total: 100},
				{Status: "pulling d", Completed: 40, Total: 100},
				{Status: "verifying sha256 digest"},
				{Status: "verifying sha256 digest"},
				{Status: "success"},
				{Status: "success"},
			} {
				onProgress(progress)
			}

			return nil
		},
		NewChatClient: func(context.Context, *config.Config) (rpcclient.ChatClient, error) {
			stub.RuntimeModelResult = rpcclient.ModelRuntime{
				Provider: constants.ModelProviderOllama,
				API:      modelprovider.APIOllamaNative,
				Model:    "qwen3:8b",
				BaseURL:  constants.DefaultOllamaBaseURL,
			}
			return stub, nil
		},
	})

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--provider", "ollama",
		"--model", "qwen3:8b",
		"--pull",
		"hello",
	})

	require.NoError(t, err)
	require.Equal(t, strings.Join([]string{
		"Ollama pull: pulling b 20%",
		"Ollama pull: pulling c 30%",
		"Ollama pull: pulling d 40%",
		"Ollama pull: verifying sha256 digest",
		"Ollama pull: success",
		"hello back",
		"",
		"\x1b[90mWorked for 0s\x1b[0m",
		"",
	}, "\n"), output.String())
}

func TestPullProgressPrinter_IgnoresDisabledAndEmptyProgress(t *testing.T) {
	require.Nil(t, newPullProgressPrinter(io.Discard, false))
	require.Nil(t, newPullProgressPrinter(nil, true))

	var nilPrinter *pullProgressPrinter
	nilPrinter.Progress(provider_ollama.PullProgress{Status: "pulling manifest"})
	nilPrinter.Finish()
	require.Nil(t, nilPrinter.Lines())

	var output bytes.Buffer
	printer := newPullProgressPrinter(&output, true)
	require.NotNil(t, printer)

	printer.Progress(provider_ollama.PullProgress{Status: " "})
	printer.Finish()

	require.Empty(t, output.String())
}

func TestPullProgressPrinter_LiveFinishResetsCurrentLine(t *testing.T) {
	var output bytes.Buffer
	printer := &pullProgressPrinter{
		output:   &output,
		live:     true,
		lines:    []string{"Ollama pull: success"},
		rendered: 1,
	}

	printer.Finish()

	require.Equal(t, "\r\x1b[2K", output.String())
}

func TestPullProgressPrinter_LiveFinishDoesNothingBeforeRender(t *testing.T) {
	var output bytes.Buffer
	printer := &pullProgressPrinter{
		output: &output,
		live:   true,
		lines:  []string{"Ollama pull: success"},
	}

	printer.Finish()

	require.Empty(t, output.String())
}

func TestPullProgressPrinter_RepaintsLiveProgressWindow(t *testing.T) {
	var output bytes.Buffer
	printer := &pullProgressPrinter{output: &output, live: true}

	for _, status := range []string{
		"pulling manifest",
		"pulling a",
		"pulling b",
		"pulling b",
		"pulling c",
		"pulling d",
		"success",
		"success",
	} {
		printer.Progress(provider_ollama.PullProgress{Status: status})
	}

	require.Equal(t, []string{
		"Ollama pull: pulling a",
		"Ollama pull: pulling b",
		"Ollama pull: pulling c",
		"Ollama pull: pulling d",
		"Ollama pull: success",
	}, printer.lines)
	lines := printer.Lines()
	require.Equal(t, printer.lines, lines)
	lines[0] = "changed"
	require.Equal(t, "Ollama pull: pulling a", printer.lines[0])
	require.Equal(t, pullProgressLineLimit, printer.rendered)
	require.Contains(t, output.String(), "\x1b[5F")
}

func TestIsTerminalWriterChecksFileDescriptors(t *testing.T) {
	require.False(t, isTerminalWriter(fakeFDWriter{fd: ^uintptr(0)}))
}

func TestNewDefaultChatClientUsesRPCConfig(t *testing.T) {
	client, err := newDefaultChatClient(context.Background(), &config.Config{
		RPC: config.RPCConfig{
			Address: "127.0.0.1",
			Port:    1,
		},
	})
	require.NoError(t, err)

	require.NoError(t, client.Close())
}

func TestChatStreamFormatter_DefaultClockAndNilFinish(t *testing.T) {
	formatter := newChatStreamFormatter(config.NewDefaultConfig(), nil, false)

	require.NotNil(t, formatter.now)
	require.False(t, formatter.turnStarted.IsZero())
	require.Empty(t, (*chatStreamFormatter)(nil).Finish())
}

func TestChatStreamFormatter_IgnoresEmptyOutput(t *testing.T) {
	formatter := newChatStreamFormatter(config.NewDefaultConfig(), sequenceClock(time.Unix(0, 0)), false)

	output := formatter.Format(rpcclient.Event{
		Kind:    agent.EventKindTextDelta,
		Channel: "assistant",
	})

	require.Empty(t, output)
}

func TestChatStreamFormatter_FinishesActiveReasoning(t *testing.T) {
	formatter := newChatStreamFormatter(config.NewDefaultConfig(), sequenceClock(
		time.Unix(0, 0),
		time.Unix(0, 0),
		time.Unix(5, 0),
		time.Unix(65, 500*int64(time.Millisecond)),
	), true)

	output := formatter.Format(rpcclient.Event{
		Kind:    agent.EventKindTextDelta,
		Channel: "reasoning",
		Text:    "thinking",
	})
	output += formatter.Finish()

	require.Equal(t, strings.Join([]string{
		"thinking",
		"",
		"Thought for 5s",
		"",
		"Worked for 1m6s",
		"",
	}, "\n"), output)
}

func TestChatStreamFormatter_NormalizesTerminalLinefeeds(t *testing.T) {
	formatter := newChatStreamFormatter(config.NewDefaultConfig(), sequenceClock(
		time.Unix(0, 0),
		time.Unix(2, 0),
	), true)
	formatter.terminalLinefeeds = true

	output := formatter.Format(rpcclient.Event{
		Kind:    agent.EventKindTextDelta,
		Channel: "assistant",
		Text:    "hello\nworld\r\nagain",
	})
	output += formatter.Finish()

	require.Equal(t, "hello\r\nworld\r\nagain\r\n\r\nWorked for 2s\r\n", output)
}

func TestNormalizeTerminalLinefeedsPreservesExistingCRLF(t *testing.T) {
	require.Equal(t, "a\r\nb\r\nc", normalizeTerminalLinefeeds("a\nb\r\nc"))
}

func TestFormatChatEvent(t *testing.T) {
	traceEvent := trace.Event{Type: trace.EvtInputSafetyBlocked}
	require.Empty(t, FormatChatEvent(config.NewDefaultConfig(), rpcclient.Event{TraceEvent: &traceEvent}))
	require.Equal(t, "\x1b[90mthinking\x1b[0m", FormatChatEvent(config.NewDefaultConfig(), rpcclient.Event{
		Kind:    agent.EventKindTextDelta,
		Channel: "reasoning",
		Text:    "thinking",
	}))
	require.Equal(t, "thinking", formatChatEvent(nil, rpcclient.Event{
		Kind:    agent.EventKindTextDelta,
		Channel: "reasoning",
		Text:    "thinking",
	}, false))
}

func TestNewMainAction_ReturnsDaemonStartErrorBeforeCreatingClient(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetMainActionState(t)

	expectedErr := errors.New("daemon unavailable")
	cmd := newMainActionTestCommandWithOptions(io.Discard, MainActionOptions{
		NewChatClient: func(context.Context, *config.Config) (rpcclient.ChatClient, error) {
			t.Fatal("chat client should not be created when daemon startup fails")
			return nil, nil
		},
		EnsureDaemonRunning: func(context.Context, *config.Config) (func() error, error) {
			return nil, expectedErr
		},
	})

	err := cmd.Run(context.Background(), []string{"morph", "--name", "flag-agent", "hello"})

	require.ErrorIs(t, err, expectedErr)
}

func TestNewMainAction_ReturnsRPCError(t *testing.T) {
	clearEnv(t, "MORPH_NAME", "MORPH_CONFIG", "MORPH_ENV_FILE")
	resetMainActionState(t)

	cmd := newMainActionTestCommand(io.Discard, func(context.Context, *config.Config) (rpcclient.ChatClient, error) {
		return nil, status.Error(codes.Unavailable, "connection refused")
	})

	err := cmd.Run(context.Background(), []string{
		"morph",
		"--name", "flag-agent",
		"hello",
	})
	require.EqualError(t, err, "rpc error: code = Unavailable desc = connection refused")
}

func newMainActionTestCommand(output io.Writer, newChatClient NewChatClientFunc) *urfavecli.Command {
	return newMainActionTestCommandWithOptions(output, MainActionOptions{
		NewChatClient:       newChatClient,
		EnsureDaemonRunning: noopEnsureDaemonRunning,
	})
}

func newMainActionTestCommandWithOptions(output io.Writer, opts MainActionOptions) *urfavecli.Command {
	envFile := ".env"
	configFile := "config.yaml"
	opts.Output = output
	if opts.EnsureDaemonRunning == nil {
		opts.EnsureDaemonRunning = noopEnsureDaemonRunning
	}
	if opts.Now == nil {
		opts.Now = func() time.Time {
			return time.Unix(0, 0)
		}
	}

	return &urfavecli.Command{
		Name:   "morph",
		Flags:  append(RootFlags(&envFile, &configFile), RequestInstructFlag()),
		Action: NewMainAction(opts),
	}
}

func noopEnsureDaemonRunning(context.Context, *config.Config) (func() error, error) {
	return nil, nil
}

type chatOnlyClient struct {
	message string
	closed  bool
}

type mainActionPermissionClient struct {
	*agentstub.AgentServiceStub
	api rpcclient.PermissionAPI
}

func (c *mainActionPermissionClient) PermissionAPI() rpcclient.PermissionAPI {
	return c.api
}

type mainActionPermissionAPIStub struct {
	requestID string
	approved  bool
	scope     permissions.GrantScope
	err       error
}

func (s *mainActionPermissionAPIStub) ListApprovalRequests(
	context.Context,
	permissions.ApprovalQuery,
) ([]permissions.ApprovalRequest, error) {
	return nil, s.err
}

func (s *mainActionPermissionAPIStub) GetApprovalRequest(
	context.Context,
	string,
) (permissions.ApprovalRequest, bool, error) {
	return permissions.ApprovalRequest{}, false, s.err
}

func (s *mainActionPermissionAPIStub) ResolveApprovalRequest(
	_ context.Context,
	id string,
	approved bool,
	scope permissions.GrantScope,
) (permissions.ApprovalRequest, error) {
	s.requestID = id
	s.approved = approved
	s.scope = scope

	return permissions.ApprovalRequest{
		ID: id, Status: permissions.ApprovalApproved, Scope: scope,
	}, s.err
}

func (s *mainActionPermissionAPIStub) ListApprovalGrants(
	context.Context,
	permissions.GrantQuery,
) ([]permissions.ApprovalGrant, error) {
	return nil, s.err
}

func (s *mainActionPermissionAPIStub) GetApprovalGrant(
	context.Context,
	string,
) (permissions.ApprovalGrant, bool, error) {
	return permissions.ApprovalGrant{}, false, s.err
}

func (s *mainActionPermissionAPIStub) RevokeApprovalGrant(
	context.Context,
	string,
) (permissions.ApprovalGrant, error) {
	return permissions.ApprovalGrant{}, s.err
}

func (s *mainActionPermissionAPIStub) DeleteApprovalRecord(
	context.Context,
	string,
) (permissions.ApprovalDeleteResult, error) {
	return permissions.ApprovalDeleteResult{}, s.err
}

func (s *mainActionPermissionAPIStub) PruneApprovals(
	context.Context,
	bool,
) (permissions.ApprovalPruneResult, error) {
	return permissions.ApprovalPruneResult{}, s.err
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

type fakeFDWriter struct {
	fd uintptr
}

func (w fakeFDWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func (w fakeFDWriter) Fd() uintptr {
	return w.fd
}

func (c *chatOnlyClient) EnqueueMessage(
	_ context.Context,
	opts rpcclient.EnqueueMessageOptions,
) (rpcclient.SessionQueueEntry, error) {
	c.message = opts.Message
	return rpcclient.SessionQueueEntry{}, nil
}

func (c *chatOnlyClient) State(context.Context, string) (rpcclient.SessionExecutionState, error) {
	return rpcclient.SessionExecutionState{}, nil
}

func (c *chatOnlyClient) Observe(
	context.Context,
	string,
	int64,
	func(rpcclient.SessionEvent) error,
) error {
	return nil
}

func (c *chatOnlyClient) EditQueuedMessage(
	context.Context,
	string,
	string,
	string,
) (rpcclient.SessionQueueEntry, error) {
	return rpcclient.SessionQueueEntry{}, nil
}

func (c *chatOnlyClient) RemoveQueuedMessage(
	context.Context,
	string,
	string,
) (rpcclient.SessionQueueEntry, error) {
	return rpcclient.SessionQueueEntry{}, nil
}

func (c *chatOnlyClient) PromoteQueuedMessage(
	context.Context,
	string,
	string,
) (rpcclient.SessionQueueEntry, error) {
	return rpcclient.SessionQueueEntry{}, nil
}

func (c *chatOnlyClient) SteerQueuedMessage(
	context.Context,
	string,
	string,
) (rpcclient.SessionQueueEntry, error) {
	return rpcclient.SessionQueueEntry{}, nil
}

func (c *chatOnlyClient) InterruptRun(
	context.Context,
	string,
) (rpcclient.SessionActiveRun, bool, error) {
	return rpcclient.SessionActiveRun{}, false, nil
}

func (c *chatOnlyClient) Close() error {
	c.closed = true
	return nil
}

func sequenceClock(times ...time.Time) func() time.Time {
	index := 0
	return func() time.Time {
		if len(times) == 0 {
			return time.Unix(0, 0)
		}
		if index >= len(times) {
			return times[len(times)-1]
		}

		value := times[index]
		index++
		return value
	}
}

func resetMainActionState(t *testing.T) {
	t.Helper()

	originalConfig := config.Get()
	originalProfile := profile.Active()
	t.Cleanup(func() {
		config.Set(originalConfig)
		profile.SetActive(originalProfile)
	})

	logutils.SetOutput(io.Discard)
	config.Set(nil)
	t.Setenv("HOME", t.TempDir())
	profile.SetActive(profile.Profile{})
}

func TestRootChatTerminalErrors(t *testing.T) {
	queueTests := []struct {
		name  string
		entry rpcclient.SessionQueueEntry
		want  string
	}{
		{
			name:  "failed detail",
			entry: rpcclient.SessionQueueEntry{Status: agentsession.QueueStatusFailed, LastError: "provider unavailable"},
			want:  "provider unavailable",
		},
		{
			name:  "failed default",
			entry: rpcclient.SessionQueueEntry{Status: agentsession.QueueStatusFailed},
			want:  "session run failed",
		},
		{
			name:  "cancelled",
			entry: rpcclient.SessionQueueEntry{Status: agentsession.QueueStatusCancelled},
			want:  "session run cancelled",
		},
		{
			name:  "completed",
			entry: rpcclient.SessionQueueEntry{Status: agentsession.QueueStatusCompleted},
		},
	}
	for _, test := range queueTests {
		t.Run("queue "+test.name, func(t *testing.T) {
			err := rootChatQueueTerminalError(test.entry)
			if test.want == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, test.want)
		})
	}

	runTests := []struct {
		name string
		run  rpcclient.SessionActiveRun
		want string
	}{
		{
			name: "failed detail",
			run:  rpcclient.SessionActiveRun{Status: agentsession.RunStatusFailed, LastError: "provider unavailable"},
			want: "provider unavailable",
		},
		{
			name: "failed default",
			run:  rpcclient.SessionActiveRun{Status: agentsession.RunStatusFailed},
			want: "session run failed",
		},
		{
			name: "interrupted detail",
			run:  rpcclient.SessionActiveRun{Status: agentsession.RunStatusInterrupted, Reason: "user_interrupt"},
			want: "session run interrupted: user_interrupt",
		},
		{
			name: "interrupted default",
			run:  rpcclient.SessionActiveRun{Status: agentsession.RunStatusInterrupted},
			want: "session run interrupted",
		},
		{
			name: "cancelled",
			run:  rpcclient.SessionActiveRun{Status: agentsession.RunStatusCancelled},
			want: "session run cancelled",
		},
		{
			name: "completed",
			run:  rpcclient.SessionActiveRun{Status: agentsession.RunStatusCompleted},
		},
	}
	for _, test := range runTests {
		t.Run("run "+test.name, func(t *testing.T) {
			err := rootChatRunTerminalError(test.run)
			if test.want == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, test.want)
		})
	}
}
