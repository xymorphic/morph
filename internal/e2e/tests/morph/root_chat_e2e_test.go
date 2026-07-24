package morph

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	urfavecli "github.com/urfave/cli/v3"

	daemoncmd "github.com/wandxy/morph/cmd/daemon"
	doctorcmd "github.com/wandxy/morph/cmd/doctor"
	configcmd "github.com/wandxy/morph/cmd/morph/configcmd"
	profilecmd "github.com/wandxy/morph/cmd/profile"
	sessioncmd "github.com/wandxy/morph/cmd/session"
	tracecmd "github.com/wandxy/morph/cmd/trace"
	morphcli "github.com/wandxy/morph/internal/cli"
	commandplan "github.com/wandxy/morph/internal/command"
	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/datadir"
	"github.com/wandxy/morph/internal/e2e"
	models "github.com/wandxy/morph/internal/model"
	"github.com/wandxy/morph/internal/profile"
	rpcclient "github.com/wandxy/morph/internal/rpc/client"
	storage "github.com/wandxy/morph/internal/state/core"
	morphmsg "github.com/wandxy/morph/pkg/agent/message"
	"github.com/wandxy/morph/pkg/logutils"
	"github.com/wandxy/morph/pkg/str"
)

func init() {
	logutils.SetOutput(io.Discard)
}

func Test_E2E_MorphRootChat_SimpleAnswer(t *testing.T) {
	resetRootChatE2E(t)

	h := newRPCHarness(t, filepath.Join(t.TempDir(), "morph-home"), e2e.NewTextClient("hello back"), nil)
	configPath := writeRPCConfig(t, h.Address(), h.Port(), e2e.RPCConfigOptions{Name: "yaml-agent"})

	output, err := runRootChatCommand(t, "morph", "--config", configPath, "hello", "world")
	require.NoError(t, err)
	assert.Equal(t, "hello back\n", output)

	messages, err := h.Messages(context.Background(), "")
	require.NoError(t, err)
	require.Len(t, messages, 2)
	assert.Equal(t, morphmsg.RoleUser, messages[0].Role)
	assert.Equal(t, "hello world", messages[0].Content)
	assert.Equal(t, morphmsg.RoleAssistant, messages[1].Role)
	assert.Equal(t, "hello back", messages[1].Content)
}

func Test_E2E_MorphRootChat_StreamingAnswer(t *testing.T) {
	resetRootChatE2E(t)

	h := newRPCHarness(t, filepath.Join(t.TempDir(), "morph-home"), e2e.NewClient(e2e.Step{
		Response: &models.Response{OutputText: "answer"},
		Stream: []models.StreamDelta{
			{Channel: models.StreamChannelReasoning, Text: "thinking"},
			{Channel: models.StreamChannelAssistant, Text: "answer"},
		},
	}), nil)
	configPath := writeRPCConfig(t, h.Address(), h.Port(), e2e.RPCConfigOptions{
		Name:   "yaml-agent",
		Stream: true,
	})

	output, err := runRootChatCommand(t, "morph", "--config", configPath, "hello")
	require.NoError(t, err)
	assert.Equal(t, "\x1b[90mthinking\x1b[0m\n\n\x1b[90mThought for 0s\x1b[0m\n\nanswer\n\n\x1b[90mWorked for 0s\x1b[0m\n", output)
}

func Test_E2E_MorphRootChat_ExplicitSession(t *testing.T) {
	resetRootChatE2E(t)

	sessionID := "ses_123456789012345678901"
	h := newRPCHarness(t, filepath.Join(t.TempDir(), "morph-home"), e2e.NewTextClient("session reply"), nil)
	configPath := writeRPCConfig(t, h.Address(), h.Port(), e2e.RPCConfigOptions{Name: "yaml-agent"})
	client, err := h.Client(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, client.Close())
	})

	autoSwitch := false
	_, err = client.Session.CreateWithOptions(context.Background(), rpcclient.CreateSessionOptions{
		ID:         sessionID,
		AutoSwitch: &autoSwitch,
	})
	require.NoError(t, err)

	output, err := runRootChatCommand(t, "morph", "--config", configPath, "--session", sessionID, "hello")
	require.NoError(t, err)
	assert.Equal(t, "session reply\n", output)

	messages, err := h.Messages(context.Background(), sessionID)
	require.NoError(t, err)
	require.Len(t, messages, 2)
	assert.Equal(t, "hello", messages[0].Content)
	assert.Equal(t, "session reply", messages[1].Content)
}

func Test_E2E_MorphRootChat_RequestInstruct(t *testing.T) {
	resetRootChatE2E(t)

	h := newRPCHarness(t, filepath.Join(t.TempDir(), "morph-home"), e2e.NewClient(e2e.Step{
		Check: func(req models.Request) error {
			if !strings.Contains(req.Instructions, "be brief") {
				return errors.New("request instruct missing from model instructions")
			}

			return nil
		},
		Response: &models.Response{OutputText: "brief"},
	}), nil)
	configPath := writeRPCConfig(t, h.Address(), h.Port(), e2e.RPCConfigOptions{Name: "yaml-agent"})

	output, err := runRootChatCommand(t, "morph", "--config", configPath, "--instruct", "be brief", "hello")
	require.NoError(t, err)
	assert.Equal(t, "brief\n", output)
}

func Test_E2E_MorphRootChat_MultiTurnContinuity(t *testing.T) {
	resetRootChatE2E(t)

	h := newRPCHarness(t, filepath.Join(t.TempDir(), "morph-home"), e2e.NewClient(
		e2e.OutputTextStep("first reply"),
		e2e.Step{
			Check: func(req models.Request) error {
				if len(req.Messages) != 3 {
					return fmt.Errorf("expected 3 messages in follow-up request, got %d", len(req.Messages))
				}
				if req.Messages[0].Role != morphmsg.RoleUser || req.Messages[0].Content != "first turn" {
					return errors.New("missing first user message in follow-up request")
				}
				if req.Messages[1].Role != morphmsg.RoleAssistant || req.Messages[1].Content != "first reply" {
					return errors.New("missing first assistant reply in follow-up request")
				}
				if req.Messages[2].Role != morphmsg.RoleUser || req.Messages[2].Content != "second turn" {
					return errors.New("missing second user message in follow-up request")
				}

				return nil
			},
			Response: &models.Response{OutputText: "second reply"},
		},
	), nil)
	configPath := writeRPCConfig(t, h.Address(), h.Port(), e2e.RPCConfigOptions{Name: "yaml-agent"})

	firstOutput, err := runRootChatCommand(t, "morph", "--config", configPath, "first", "turn")
	require.NoError(t, err)
	assert.Equal(t, "first reply\n", firstOutput)

	secondOutput, err := runRootChatCommand(t, "morph", "--config", configPath, "second", "turn")
	require.NoError(t, err)
	assert.Equal(t, "second reply\n", secondOutput)

	messages, err := h.Messages(context.Background(), "")
	require.NoError(t, err)
	require.Len(t, messages, 4)
	assert.Equal(t, []morphmsg.Role{
		morphmsg.RoleUser,
		morphmsg.RoleAssistant,
		morphmsg.RoleUser,
		morphmsg.RoleAssistant,
	}, []morphmsg.Role{messages[0].Role, messages[1].Role, messages[2].Role, messages[3].Role})
	assert.Equal(t, "first turn", messages[0].Content)
	assert.Equal(t, "first reply", messages[1].Content)
	assert.Equal(t, "second turn", messages[2].Content)
	assert.Equal(t, "second reply", messages[3].Content)
}

func Test_E2E_MorphRootChat_ConfigPrecedenceYAML(t *testing.T) {
	resetRootChatE2E(t)

	h := newRPCHarness(t, filepath.Join(t.TempDir(), "morph-home"), e2e.NewTextClient("yaml reply"), nil)
	configPath := writeRPCConfig(t, h.Address(), h.Port(), e2e.RPCConfigOptions{Name: "yaml-agent"})

	output, err := runRootChatCommand(t, "morph", "--config", configPath, "hello")
	require.NoError(t, err)
	assert.Equal(t, "yaml reply\n", output)
}

func Test_E2E_MorphRootChat_ConfigPrecedenceEnvOverridesYAML(t *testing.T) {
	resetRootChatE2E(t)

	h := newRPCHarness(t, filepath.Join(t.TempDir(), "morph-home"), e2e.NewTextClient("env reply"), nil)
	configPath := writeRPCConfig(t, h.Address(), h.Port(), e2e.RPCConfigOptions{Name: "yaml-agent"})
	envPath := writeRootChatEnv(t, "MORPH_NAME=env-agent\n")

	output, err := runRootChatCommand(t, "morph", "--env-file", envPath, "--config", configPath, "hello")
	require.NoError(t, err)
	assert.Equal(t, "env reply\n", output)
}

func Test_E2E_MorphRootChat_ConfigPrecedenceCLIOverridesEnvAndYAML(t *testing.T) {
	resetRootChatE2E(t)

	h := newRPCHarness(t, filepath.Join(t.TempDir(), "morph-home"), e2e.NewTextClient("cli reply"), nil)
	configPath := writeRPCConfig(t, h.Address(), h.Port(), e2e.RPCConfigOptions{Name: "yaml-agent"})
	envPath := writeRootChatEnv(t, "MORPH_NAME=env-agent\n")

	output, err := runRootChatCommand(
		t,
		"morph",
		"--env-file", envPath,
		"--config", configPath,
		"--name", "cli-agent",
		"hello",
	)
	require.NoError(t, err)
	assert.Equal(t, "cli reply\n", output)
}

func Test_E2E_MorphRootChat_StartsDaemonAndReturnsModelErrorWhenUnconfigured(t *testing.T) {
	userHome, err := os.UserHomeDir()
	require.NoError(t, err)

	resetRootChatE2E(t)
	activeProfile := profile.Active()
	require.NotEmpty(t, activeProfile.HomeDir)
	require.Equal(t,
		filepath.Join(activeProfile.HomeDir, "data", "state.db"),
		datadir.StateDBPath(),
	)
	require.NotEqual(t,
		filepath.Join(userHome, ".morph", "profiles", profile.DefaultName, "data", "state.db"),
		datadir.StateDBPath(),
	)

	port, err := strconv.Atoi(nextTestPort(t))
	require.NoError(t, err)

	configPath := writeRPCConfig(t, "127.0.0.1", port, e2e.RPCConfigOptions{
		Name: "yaml-agent",
	})

	output, err := runRootChatCommand(t, "morph", "--config", configPath, "hello")
	require.Error(t, err)
	assert.Empty(t, output)
	assert.Contains(t, err.Error(), "model is required")
}

func Test_E2E_MorphStartup_InvalidConfigBlocksStartup(t *testing.T) {
	resetRootChatE2E(t)

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
name: config-agent
models:
  providers:
    openrouter:
      apiKey: config-key
  main:
    name: openai/gpt-4o-mini
    provider: openrouter
log:
  level: trace
`), 0o600))

	_, err := runRootChatCommand(t, "morph", "--config", configPath, "--rpc.port", nextTestPort(t), "daemon")
	require.Error(t, err)
	require.ErrorContains(t, err, "log level must be one of debug, info, warn, or error")
}

func Test_E2E_MorphRootChat_FileGuardrailFailureReturnsCoherentAnswer(t *testing.T) {
	resetRootChatE2E(t)

	outsidePath := filepath.Join(t.TempDir(), "outside.txt")
	require.NoError(t, os.WriteFile(outsidePath, []byte("secret"), 0o600))

	client := e2e.NewClient(
		e2e.ToolStep(models.ToolCall{
			ID:    "call-1",
			Name:  "read_file",
			Input: fmt.Sprintf(`{"path":%q}`, outsidePath),
		}),
		e2e.Step{
			Check: e2e.ToolError("call-1", "read_file", "path_outside_roots", "path is outside allowed roots"),
			Response: &models.Response{
				OutputText: "I can't read that path because it is outside the allowed workspace.",
			},
		},
	)

	h := newRPCHarness(t, filepath.Join(t.TempDir(), "morph-home"), client, e2e.DefaultConfig(e2e.ConfigOptions{StorageBackend: "sqlite"}))
	configPath := writeRPCConfig(t, h.Address(), h.Port(), e2e.RPCConfigOptions{Name: "yaml-agent"})

	output, err := runRootChatCommand(t, "morph", "--config", configPath, "read the file outside the workspace")
	require.NoError(t, err)
	assert.Equal(t, "I can't read that path because it is outside the allowed workspace.\n", output)
}

func Test_E2E_MorphRootChat_CommandDeniedReturnsCoherentAnswer(t *testing.T) {
	resetRootChatE2E(t)

	cfg := e2e.DefaultConfig(e2e.ConfigOptions{StorageBackend: "sqlite"})
	cfg.Exec.DenyCommands = []commandplan.Selector{{Executable: "git", ArgumentPrefix: []string{"push"}}}

	client := e2e.NewClient(
		e2e.ToolStep(models.ToolCall{
			ID:    "call-1",
			Name:  "run_command",
			Input: `{"command":"git","args":["push","origin","main"]}`,
		}),
		e2e.Step{
			Check: e2e.ToolError("call-1", "run_command", "permission_denied", "matched typed deny rule"),
			Response: &models.Response{
				OutputText: "I can't run that command because command execution policy denies it.",
			},
		},
	)

	h := newRPCHarness(t, filepath.Join(t.TempDir(), "morph-home"), client, cfg)
	configPath := writeRPCConfig(t, h.Address(), h.Port(), e2e.RPCConfigOptions{Name: "yaml-agent"})

	output, err := runRootChatCommand(t, "morph", "--config", configPath, "push the current branch")
	require.NoError(t, err)
	assert.Equal(t, "I can't run that command because command execution policy denies it.\n", output)
}

func Test_E2E_MorphRootChat_CommandApprovalRequiresInteractiveTerminal(t *testing.T) {
	resetRootChatE2E(t)

	cfg := e2e.DefaultConfig(e2e.ConfigOptions{StorageBackend: "sqlite"})
	cfg.Exec.AskCommands = []commandplan.Selector{{Executable: "git", ArgumentPrefix: []string{"push"}}}

	client := e2e.NewClient(
		e2e.ToolStep(models.ToolCall{
			ID:    "call-1",
			Name:  "run_command",
			Input: `{"command":"git","args":["push","origin","main"]}`,
		}),
	)

	h := newRPCHarness(t, filepath.Join(t.TempDir(), "morph-home"), client, cfg)
	configPath := writeRPCConfig(t, h.Address(), h.Port(), e2e.RPCConfigOptions{Name: "yaml-agent"})

	output, err := runRootChatCommand(t, "morph", "--config", configPath, "push the current branch")
	require.ErrorContains(t, err, "approval required for run_command · approve 2 operations")
	require.ErrorContains(t, err, "root chat input and output must be an interactive terminal")
	assert.Empty(t, output)
}

func Test_E2E_MorphRootChat_TimeToolSynthesizesFinalAnswer(t *testing.T) {
	resetRootChatE2E(t)

	toolCall := models.ToolCall{ID: "call-1", Name: "time", Input: "{}"}
	h := newRPCHarness(t, filepath.Join(t.TempDir(), "morph-home"), e2e.NewClient(
		e2e.ToolStep(toolCall),
		e2e.Step{
			Check: e2e.CombineChecks(
				e2e.AssertToolRoundTrip(toolCall),
				e2e.ToolOutputString("call-1", "time", func(value string) error {
					valueText := str.String(value)
					_, err := time.Parse(time.RFC3339, valueText.Trim())
					if err != nil {
						return fmt.Errorf("expected RFC3339 time output: %w", err)
					}
					return nil
				}),
			),
			Response: &models.Response{OutputText: "The current time has been retrieved."},
		},
	), nil)
	configPath := writeRPCConfig(t, h.Address(), h.Port(), e2e.RPCConfigOptions{Name: "yaml-agent"})

	output, err := runRootChatCommand(t, "morph", "--config", configPath, "what time is it?")
	require.NoError(t, err)
	assert.Equal(t, "The current time has been retrieved.\n", output)
}

func Test_E2E_MorphRootChat_ReadFileToolSynthesizesFinalAnswer(t *testing.T) {
	resetRootChatE2E(t)

	home := filepath.Join(t.TempDir(), "morph-home")
	workspace := filepath.Join(home, "workspace")
	require.NoError(t, os.MkdirAll(workspace, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "notes.txt"), []byte("hello from file"), 0o600))
	cfg := e2e.DefaultConfig(e2e.ConfigOptions{StorageBackend: "sqlite"})
	cfg.FS.Roots = []string{workspace}

	toolCall := models.ToolCall{ID: "call-1", Name: "read_file", Input: `{"path":"notes.txt"}`}
	h := newRPCHarness(t, home, e2e.NewClient(
		e2e.ToolStep(toolCall),
		e2e.Step{
			Check: e2e.CombineChecks(
				e2e.AssertToolRoundTrip(toolCall),
				e2e.ToolOutputJSON("call-1", "read_file", func(payload map[string]any) error {
					sprintValue := str.String(fmt.Sprint(payload["path"]))
					if sprintValue.Trim() != "notes.txt" {
						return fmt.Errorf("expected read path notes.txt, got %v", payload["path"])
					}
					if !strings.Contains(fmt.Sprint(payload["content"]), "hello from file") {
						return fmt.Errorf("expected file content in tool output, got %v", payload["content"])
					}
					return nil
				}),
			),
			Response: &models.Response{OutputText: "The file says hello from file."},
		},
	), cfg)
	configPath := writeRPCConfig(t, h.Address(), h.Port(), e2e.RPCConfigOptions{Name: "yaml-agent"})

	output, err := runRootChatCommand(t, "morph", "--config", configPath, "read notes.txt and summarize it")
	require.NoError(t, err)
	assert.Equal(t, "The file says hello from file.\n", output)
}

func Test_E2E_MorphRootChat_WriteFileToolSynthesizesFinalAnswer(t *testing.T) {
	resetRootChatE2E(t)

	home := filepath.Join(t.TempDir(), "morph-home")
	workspace := filepath.Join(home, "workspace")
	require.NoError(t, os.MkdirAll(workspace, 0o755))
	cfg := e2e.DefaultConfig(e2e.ConfigOptions{StorageBackend: "sqlite"})
	cfg.FS.Roots = []string{workspace}

	toolCall := models.ToolCall{
		ID:    "call-1",
		Name:  "write_file",
		Input: `{"path":"drafts/out.txt","content":"written by tool","create_dirs":true}`,
	}
	h := newRPCHarness(t, home, e2e.NewClient(
		e2e.ToolStep(toolCall),
		e2e.Step{
			Check: e2e.CombineChecks(
				e2e.AssertToolRoundTrip(toolCall),
				e2e.ToolOutputJSON("call-1", "write_file", func(payload map[string]any) error {
					sprintValue2 := str.String(fmt.Sprint(payload["path"]))
					if sprintValue2.Trim() != "drafts/out.txt" {
						return fmt.Errorf("expected write path drafts/out.txt, got %v", payload["path"])
					}
					if fmt.Sprint(payload["created"]) != "true" {
						return fmt.Errorf("expected created=true, got %v", payload["created"])
					}
					raw, err := os.ReadFile(filepath.Join(workspace, "drafts", "out.txt"))
					if err != nil {
						return err
					}
					if string(raw) != "written by tool" {
						return fmt.Errorf("expected written file content, got %q", string(raw))
					}
					return nil
				}),
			),
			Response: &models.Response{OutputText: "I wrote the requested file."},
		},
	), cfg)
	configPath := writeRPCConfig(t, h.Address(), h.Port(), e2e.RPCConfigOptions{Name: "yaml-agent"})

	output, err := runRootChatCommand(t, "morph", "--config", configPath, "write drafts/out.txt with the requested content")
	require.NoError(t, err)
	assert.Equal(t, "I wrote the requested file.\n", output)
}

func Test_E2E_MorphRootChat_RunCommandToolSynthesizesFinalAnswer(t *testing.T) {
	resetRootChatE2E(t)

	home := filepath.Join(t.TempDir(), "morph-home")
	workspace := filepath.Join(home, "workspace")
	require.NoError(t, os.MkdirAll(workspace, 0o755))
	cfg := e2e.DefaultConfig(e2e.ConfigOptions{StorageBackend: "sqlite"})
	cfg.FS.Roots = []string{workspace}

	toolCall := models.ToolCall{
		ID:    "call-1",
		Name:  "run_command",
		Input: `{"command":"pwd"}`,
	}
	h := newRPCHarness(t, home, e2e.NewClient(
		e2e.ToolStep(toolCall),
		e2e.Step{
			Check: e2e.CombineChecks(
				e2e.AssertToolRoundTrip(toolCall),
				e2e.ToolOutputJSON("call-1", "run_command", func(payload map[string]any) error {
					if fmt.Sprint(payload["exit_code"]) != "0" {
						return fmt.Errorf("expected exit_code=0, got %v", payload["exit_code"])
					}

					expected := filepath.Join(home, "workspace")
					if !strings.Contains(fmt.Sprint(payload["stdout"]), expected) {
						return fmt.Errorf("expected stdout to contain %q, got %v", expected, payload["stdout"])
					}
					return nil
				}),
			),
			Response: &models.Response{OutputText: "The command ran successfully in the workspace."},
		},
	), cfg)
	configPath := writeRPCConfig(t, h.Address(), h.Port(), e2e.RPCConfigOptions{Name: "yaml-agent"})

	output, err := runRootChatCommand(t, "morph", "--config", configPath, "run pwd and tell me where you are")
	require.NoError(t, err)
	assert.Equal(t, "The command ran successfully in the workspace.\n", output)
}

func Test_E2E_MorphRootChat_PlanToolPersistsAcrossLaterTurn(t *testing.T) {
	resetRootChatE2E(t)

	toolCall := models.ToolCall{
		ID:   "call-1",
		Name: "plan_tool",
		Input: `{"steps":[{"id":"step-1","content":"Inspect the bug","status":"in_progress"},` +
			`{"id":"step-2","content":"Write the fix","status":"pending"}],` +
			`"explanation":"track the current work"}`,
	}

	h := newRPCHarness(t, filepath.Join(t.TempDir(), "morph-home"), e2e.NewClient(
		e2e.ToolStep(toolCall),
		e2e.Step{
			Check: e2e.CombineChecks(
				e2e.AssertToolRoundTrip(toolCall),
				e2e.ToolOutputJSON("call-1", "plan_tool", func(payload map[string]any) error {
					steps, ok := payload["steps"].([]any)
					if !ok || len(steps) != 2 {
						return fmt.Errorf("expected two plan steps, got %v", payload["steps"])
					}
					if !strings.Contains(fmt.Sprint(steps[0]), "Inspect the bug") {
						return fmt.Errorf("expected persisted plan content, got %v", steps[0])
					}
					return nil
				}),
			),
			Response: &models.Response{OutputText: "Plan saved."},
		},
		e2e.Step{
			Check: func(req models.Request) error {
				if !strings.Contains(req.Instructions, "# Plan Context") {
					return errors.New("expected hydrated plan context in instructions")
				}
				if !strings.Contains(req.Instructions, "- [in_progress] Inspect the bug") {
					return errors.New("expected active plan step in later turn instructions")
				}
				if !strings.Contains(req.Instructions, "- [pending] Write the fix") {
					return errors.New("expected pending plan step in later turn instructions")
				}
				if !strings.Contains(req.Instructions, "track the current work") {
					return errors.New("expected plan explanation in later turn instructions")
				}

				return nil
			},
			Response: &models.Response{OutputText: "I still have the saved plan in context."},
		},
	), nil)
	configPath := writeRPCConfig(t, h.Address(), h.Port(), e2e.RPCConfigOptions{Name: "yaml-agent"})

	firstOutput, err := runRootChatCommand(t, "morph", "--config", configPath, "create a plan for this task")
	require.NoError(t, err)
	assert.Equal(t, "Plan saved.\n", firstOutput)

	secondOutput, err := runRootChatCommand(t, "morph", "--config", configPath, "what is the current plan?")
	require.NoError(t, err)
	assert.Equal(t, "I still have the saved plan in context.\n", secondOutput)
}

func Test_E2E_MorphRootChat_SessionSearchRetrievesPriorContextDuringTask(t *testing.T) {
	resetRootChatE2E(t)

	toolCall := models.ToolCall{
		ID:    "call-1",
		Name:  "session_search",
		Input: fmt.Sprintf(`{"session_id":%q,"query":"ORION","role":"user","max_results":3}`, storage.DefaultSessionID),
	}

	h := newRPCHarness(t, filepath.Join(t.TempDir(), "morph-home"), e2e.NewClient(
		e2e.OutputTextStep("Stored the ORION codename for later."),
		e2e.ToolStep(toolCall),
		e2e.Step{
			Check: e2e.CombineChecks(
				e2e.AssertToolRoundTrip(toolCall),
				e2e.ToolOutputJSON("call-1", "session_search", func(payload map[string]any) error {
					results, ok := payload["results"].([]any)
					if !ok || len(results) == 0 {
						return fmt.Errorf("expected non-empty session search results, got %v", payload["results"])
					}

					first, ok := results[0].(map[string]any)
					if !ok {
						return fmt.Errorf("expected structured session search result, got %T", results[0])
					}
					messages, ok := first["messages"].([]any)
					if !ok || len(messages) == 0 {
						return fmt.Errorf("expected grouped message hits, got %v", first["messages"])
					}
					message, ok := messages[0].(map[string]any)
					if !ok {
						return fmt.Errorf("expected structured message hit, got %T", messages[0])
					}
					if fmt.Sprint(message["role"]) != "user" {
						return fmt.Errorf("expected user-role search result, got %v", message["role"])
					}
					if !strings.Contains(fmt.Sprint(message["snippet"]), "ORION") {
						return fmt.Errorf("expected ORION in search snippet, got %v", message["snippet"])
					}

					return nil
				}),
			),
			Response: &models.Response{OutputText: "The earlier codename was ORION."},
		},
	), nil)
	configPath := writeRPCConfig(t, h.Address(), h.Port(), e2e.RPCConfigOptions{Name: "yaml-agent"})

	firstOutput, err := runRootChatCommand(t, "morph", "--config", configPath, "Remember the project codename ORION.")
	require.NoError(t, err)
	assert.Equal(t, "Stored the ORION codename for later.\n", firstOutput)

	secondOutput, err := runRootChatCommand(t, "morph", "--config", configPath, "What codename did I mention earlier?")
	require.NoError(t, err)
	assert.Equal(t, "The earlier codename was ORION.\n", secondOutput)
}

func Test_E2E_MorphRootChat_SessionSearchFilteringInfluencesFinalAnswer(t *testing.T) {
	resetRootChatE2E(t)

	toolCall := models.ToolCall{
		ID:    "call-1",
		Name:  "session_search",
		Input: fmt.Sprintf(`{"session_id":%q,"query":"keyword","role":"user","max_results":5}`, storage.DefaultSessionID),
	}

	h := newRPCHarness(t, filepath.Join(t.TempDir(), "morph-home"), e2e.NewClient(
		e2e.OutputTextStep("The keyword is ASSISTANT-ONLY."),
		e2e.ToolStep(toolCall),
		e2e.Step{
			Check: e2e.CombineChecks(
				e2e.AssertToolRoundTrip(toolCall),
				e2e.ToolOutputJSON("call-1", "session_search", func(payload map[string]any) error {
					results, ok := payload["results"].([]any)
					if !ok || len(results) == 0 {
						return fmt.Errorf("expected filtered session search results, got %v", payload["results"])
					}

					for _, raw := range results {
						result, ok := raw.(map[string]any)
						if !ok {
							return fmt.Errorf("expected structured session search result, got %T", raw)
						}
						messages, ok := result["messages"].([]any)
						if !ok || len(messages) == 0 {
							return fmt.Errorf("expected grouped message hits, got %v", result["messages"])
						}
						for _, rawMessage := range messages {
							message, ok := rawMessage.(map[string]any)
							if !ok {
								return fmt.Errorf("expected structured message hit, got %T", rawMessage)
							}
							if fmt.Sprint(message["role"]) != "user" {
								return fmt.Errorf("expected only user-role search results, got %v", message["role"])
							}
							if strings.Contains(fmt.Sprint(message["snippet"]), "ASSISTANT-ONLY") {
								return fmt.Errorf("expected assistant-only content to be filtered out, got %v", message["snippet"])
							}
						}
					}

					return nil
				}),
			),
			Response: &models.Response{OutputText: "The user-side keyword was USER-ONLY."},
		},
	), nil)
	configPath := writeRPCConfig(t, h.Address(), h.Port(), e2e.RPCConfigOptions{Name: "yaml-agent"})

	firstOutput, err := runRootChatCommand(t, "morph", "--config", configPath, "The keyword is USER-ONLY.")
	require.NoError(t, err)
	assert.Equal(t, "The keyword is ASSISTANT-ONLY.\n", firstOutput)

	secondOutput, err := runRootChatCommand(t, "morph", "--config", configPath, "Which keyword did I provide earlier?")
	require.NoError(t, err)
	assert.Equal(t, "The user-side keyword was USER-ONLY.\n", secondOutput)
}

func Test_E2E_MorphRootChat_DisabledFilesystemCapabilityOmitsFileTools(t *testing.T) {
	resetRootChatE2E(t)

	cfg := e2e.DefaultConfig(e2e.ConfigOptions{StorageBackend: "sqlite"})
	cfg.Cap.Filesystem = new(false)

	h := newRPCHarness(t, filepath.Join(t.TempDir(), "morph-home"), e2e.NewClient(e2e.Step{
		Check: e2e.MissingTools("list_files", "read_file", "search_files", "write_file", "patch"),
		Response: &models.Response{
			OutputText: "Filesystem access is unavailable in this run.",
		},
	}), cfg)
	configPath := writeRPCConfig(t, h.Address(), h.Port(), e2e.RPCConfigOptions{Name: "yaml-agent"})

	output, err := runRootChatCommand(t, "morph", "--config", configPath, "list the workspace files")
	require.NoError(t, err)
	assert.Equal(t, "Filesystem access is unavailable in this run.\n", output)
}

func Test_E2E_MorphRootChat_DisabledExecCapabilityOmitsExecTools(t *testing.T) {
	resetRootChatE2E(t)

	cfg := e2e.DefaultConfig(e2e.ConfigOptions{StorageBackend: "sqlite"})
	cfg.Cap.Exec = new(false)

	h := newRPCHarness(t, filepath.Join(t.TempDir(), "morph-home"), e2e.NewClient(e2e.Step{
		Check: e2e.MissingTools("run_command", "process"),
		Response: &models.Response{
			OutputText: "Command execution is unavailable in this run.",
		},
	}), cfg)
	configPath := writeRPCConfig(t, h.Address(), h.Port(), e2e.RPCConfigOptions{Name: "yaml-agent"})

	output, err := runRootChatCommand(t, "morph", "--config", configPath, "run git status")
	require.NoError(t, err)
	assert.Equal(t, "Command execution is unavailable in this run.\n", output)
}

func Test_E2E_MorphRootChat_DisabledNetworkCapabilityOmitsWebTools(t *testing.T) {
	resetRootChatE2E(t)

	cfg := e2e.DefaultConfig(e2e.ConfigOptions{StorageBackend: "sqlite"})
	cfg.Cap.Network = new(false)
	cfg.Web.Provider = "firecrawl"
	cfg.Web.APIKey = "test-key"

	h := newRPCHarness(t, filepath.Join(t.TempDir(), "morph-home"), e2e.NewClient(e2e.Step{
		Check: e2e.MissingTools("web_search", "web_extract"),
		Response: &models.Response{
			OutputText: "Network-backed web tools are unavailable in this run.",
		},
	}), cfg)
	configPath := writeRPCConfig(t, h.Address(), h.Port(), e2e.RPCConfigOptions{Name: "yaml-agent"})

	output, err := runRootChatCommand(t, "morph", "--config", configPath, "search the web for recent news")
	require.NoError(t, err)
	assert.Equal(t, "Network-backed web tools are unavailable in this run.\n", output)
}

func Test_E2E_MorphRootChat_WebToolsAppearWhenProviderConfigured(t *testing.T) {
	resetRootChatE2E(t)

	provider := e2e.NewProvider(e2e.ProviderResponse{Body: `{"data":{"web":[]}}`})
	t.Cleanup(provider.Close)

	cfg := newWebConfig(provider.URL())
	h := newRPCHarness(t, filepath.Join(t.TempDir(), "morph-home"), e2e.NewClient(e2e.Step{
		Check: func(req models.Request) error {
			foundSearch := false
			foundExtract := false
			for _, tool := range req.Tools {
				if tool.Name == "web_search" {
					foundSearch = true
				}
				if tool.Name == "web_extract" {
					foundExtract = true
				}
			}
			if !foundSearch || !foundExtract {
				return fmt.Errorf("expected web_search and web_extract tools, got %+v", req.Tools)
			}

			return nil
		},
		Response: &models.Response{OutputText: "Web tools are available."},
	}), cfg)
	configPath := writeRPCConfig(t, h.Address(), h.Port(), e2e.RPCConfigOptions{Name: "yaml-agent"})

	output, err := runRootChatCommand(t, "morph", "--config", configPath, "search the web for docs")
	require.NoError(t, err)
	assert.Equal(t, "Web tools are available.\n", output)
}

func Test_E2E_MorphRootChat_WebExtractBlockedDomainIsEnforced(t *testing.T) {
	resetRootChatE2E(t)

	provider := e2e.NewProvider(e2e.ProviderResponse{
		Body: `{"data":{"markdown":"should not be fetched","metadata":{"sourceURL":"https://blocked.example/page","title":"Blocked"}}}`,
	})
	t.Cleanup(provider.Close)

	cfg := newWebConfig(provider.URL())
	cfg.Web.BlockedDomainsEnabled = true
	cfg.Web.BlockedDomains = []string{"blocked.example"}

	toolCall := models.ToolCall{
		ID:    "call-1",
		Name:  "web_extract",
		Input: `{"urls":["https://blocked.example/page"],"format":"markdown"}`,
	}
	h := newRPCHarness(t, filepath.Join(t.TempDir(), "morph-home"), e2e.NewClient(
		e2e.ToolStep(toolCall),
		e2e.Step{
			Check: e2e.CombineChecks(
				e2e.AssertToolRoundTrip(toolCall),
				e2e.ToolOutputJSON("call-1", "web_extract", func(payload map[string]any) error {
					results, ok := payload["results"].([]any)
					if !ok || len(results) != 1 {
						return fmt.Errorf("expected one blocked extract result, got %v", payload["results"])
					}

					result, ok := results[0].(map[string]any)
					if !ok {
						return fmt.Errorf("expected structured blocked extract result, got %T", results[0])
					}
					if fmt.Sprint(result["url"]) != "https://blocked.example/page" {
						return fmt.Errorf("expected blocked url in result, got %v", result["url"])
					}
					if !strings.Contains(fmt.Sprint(result["error"]), "blocked by configured website blocklist policy") {
						return fmt.Errorf("expected blocked-domain error, got %v", result["error"])
					}

					return nil
				}),
			),
			Response: &models.Response{OutputText: "That domain is blocked by policy."},
		},
	), cfg)
	configPath := writeRPCConfig(t, h.Address(), h.Port(), e2e.RPCConfigOptions{Name: "yaml-agent"})

	output, err := runRootChatCommand(t, "morph", "--config", configPath, "extract https://blocked.example/page")
	require.NoError(t, err)
	assert.Equal(t, "That domain is blocked by policy.\n", output)
	assert.Empty(t, provider.Requests())
}

func Test_E2E_MorphRootChat_WebSearchCachedProviderPathRemainsCorrect(t *testing.T) {
	resetRootChatE2E(t)

	provider := e2e.NewProvider(e2e.ProviderResponse{
		Body: `{"data":{"web":[{"title":"Cache Docs","url":"https://example.com/docs","description":"Cached docs result"}]}}`,
	})
	t.Cleanup(provider.Close)

	cfg := newWebConfig(provider.URL())
	cfg.Web.CacheTTL = time.Hour

	toolCall := models.ToolCall{
		ID:    "call-1",
		Name:  "web_search",
		Input: `{"query":"cached docs","count":1}`,
	}
	h := newRPCHarness(t, filepath.Join(t.TempDir(), "morph-home"), e2e.NewClient(
		e2e.ToolStep(toolCall),
		e2e.Step{
			Check: e2e.CombineChecks(
				e2e.AssertToolRoundTrip(toolCall),
				e2e.ToolOutputJSON("call-1", "web_search", func(payload map[string]any) error {
					results, ok := payload["results"].([]any)
					if !ok || len(results) != 1 {
						return fmt.Errorf("expected cached search result, got %v", payload["results"])
					}

					result, ok := results[0].(map[string]any)
					if !ok {
						return fmt.Errorf("expected structured search result, got %T", results[0])
					}
					if fmt.Sprint(result["Title"]) != "Cache Docs" {
						return fmt.Errorf("expected cached search title, got %v", result["Title"])
					}

					return nil
				}),
			),
			Response: &models.Response{OutputText: "Cached docs found."},
		},
		e2e.ToolStep(toolCall),
		e2e.Step{
			Check: e2e.CombineChecks(
				e2e.AssertToolRoundTrip(toolCall),
				e2e.ToolOutputJSON("call-1", "web_search", func(payload map[string]any) error {
					results, ok := payload["results"].([]any)
					if !ok || len(results) != 1 {
						return fmt.Errorf("expected cached search result on second turn, got %v", payload["results"])
					}
					return nil
				}),
			),
			Response: &models.Response{OutputText: "Cached docs found again."},
		},
	), cfg)
	configPath := writeRPCConfig(t, h.Address(), h.Port(), e2e.RPCConfigOptions{Name: "yaml-agent"})

	firstOutput, err := runRootChatCommand(t, "morph", "--config", configPath, "search for cached docs")
	require.NoError(t, err)
	assert.Equal(t, "Cached docs found.\n", firstOutput)

	secondOutput, err := runRootChatCommand(t, "morph", "--config", configPath, "search for cached docs again")
	require.NoError(t, err)
	assert.Equal(t, "Cached docs found again.\n", secondOutput)

	requests := provider.Requests()
	require.Len(t, requests, 1)
	assert.Equal(t, "/v2/search", requests[0].Path)
}

func Test_E2E_MorphRootChat_LargeWebExtractSummarizationPathRemainsCorrect(t *testing.T) {
	resetRootChatE2E(t)

	provider := e2e.NewProvider(e2e.ProviderResponse{
		Body: `{"data":{"markdown":"` + strings.Repeat("A", 80) + `","metadata":{"sourceURL":"https://example.com/long","title":"Long Article"}}}`,
	})
	t.Cleanup(provider.Close)

	cfg := newWebConfig(provider.URL())
	cfg.Web.ExtractMinSummarizeChars = 10
	cfg.Web.ExtractMaxSummaryChars = 40
	cfg.Web.ExtractMaxSummaryChunkChars = 50
	cfg.Web.ExtractRefusalThresholdChars = 1000

	toolCall := models.ToolCall{
		ID:    "call-1",
		Name:  "web_extract",
		Input: `{"urls":["https://example.com/long"],"summarize":true}`,
	}
	modelClient := e2e.NewClient(
		e2e.ToolStep(toolCall),
		e2e.Step{
			Check: e2e.CombineChecks(
				e2e.AssertToolRoundTrip(toolCall),
				e2e.ToolOutputJSON("call-1", "web_extract", func(payload map[string]any) error {
					results, ok := payload["results"].([]any)
					if !ok || len(results) != 1 {
						return fmt.Errorf("expected summarized extract result, got %v", payload["results"])
					}

					result, ok := results[0].(map[string]any)
					if !ok {
						return fmt.Errorf("expected structured summarized extract result, got %T", results[0])
					}
					if fmt.Sprint(result["content_format"]) != "summary" {
						return fmt.Errorf("expected summary content format, got %v", result["content_format"])
					}
					if fmt.Sprint(result["summarized"]) != "true" {
						return fmt.Errorf("expected summarized=true, got %v", result["summarized"])
					}
					if !strings.Contains(fmt.Sprint(result["content"]), "final condensed summary") {
						return fmt.Errorf("expected final summary content, got %v", result["content"])
					}
					if fmt.Sprint(result["source_content_chars"]) != "80" {
						return fmt.Errorf("expected source_content_chars=80, got %v", result["source_content_chars"])
					}

					return nil
				}),
			),
			Response: &models.Response{OutputText: "Here is the condensed extract summary."},
		},
	)
	summaryClient := e2e.NewClient(
		e2e.OutputTextStep("chunk one summary"),
		e2e.OutputTextStep("chunk two summary"),
		e2e.Step{
			Check: func(req models.Request) error {
				if len(req.Messages) != 1 {
					return fmt.Errorf("expected synthesis summary request, got %d messages", len(req.Messages))
				}

				content := req.Messages[0].Content
				if !strings.Contains(content, "Chunk 1 Summary:\nchunk one summary") {
					return errors.New("expected first chunk summary in synthesis request")
				}
				if !strings.Contains(content, "Chunk 2 Summary:\nchunk two summary") {
					return errors.New("expected second chunk summary in synthesis request")
				}

				return nil
			},
			Response: &models.Response{OutputText: "final condensed summary"},
		},
	)

	home := filepath.Join(t.TempDir(), "morph-home")
	h, err := e2e.NewRPCHarness(context.Background(), e2e.HarnessOptions{
		Spec:          e2e.DefaultSpec(home),
		Config:        cfg,
		ModelClient:   modelClient,
		SummaryClient: summaryClient,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, h.Close())
	})

	configPath := writeRPCConfig(t, h.Address(), h.Port(), e2e.RPCConfigOptions{Name: "yaml-agent"})

	output, err := runRootChatCommand(t, "morph", "--config", configPath, "extract and summarize the long article")
	require.NoError(t, err)
	assert.Equal(t, "Here is the condensed extract summary.\n", output)
}

func Test_E2E_MorphRootChat_IterationBudgetExhaustionFallsBackCoherently(t *testing.T) {
	resetRootChatE2E(t)

	cfg := e2e.DefaultConfig(e2e.ConfigOptions{StorageBackend: "sqlite"})
	cfg.Session.MaxIterations = 1

	h := newRPCHarness(t, filepath.Join(t.TempDir(), "morph-home"), e2e.NewClient(
		e2e.ToolStep(models.ToolCall{ID: "call-1", Name: "time", Input: "{}"}),
		e2e.Step{
			Check: func(req models.Request) error {
				if len(req.Tools) != 0 {
					return fmt.Errorf("expected summary fallback request without tools, got %d", len(req.Tools))
				}
				if !strings.Contains(req.Instructions, "Remaining iteration budget: 0.") {
					return errors.New("expected summary fallback instructions")
				}

				return e2e.ToolMessagePresent("call-1", "time")(req)
			},
			Response: &models.Response{
				OutputText: "I hit the iteration limit before finishing, so I am returning a summary instead.",
			},
		},
	), cfg)
	configPath := writeRPCConfig(t, h.Address(), h.Port(), e2e.RPCConfigOptions{Name: "yaml-agent"})

	output, err := runRootChatCommand(t, "morph", "--config", configPath, "what time is it?")
	require.NoError(t, err)
	assert.Equal(t, "I hit the iteration limit before finishing, so I am returning a summary instead.\n", output)
}

func Test_E2E_MorphLiveHarness_OneTurnDirectAnswer(t *testing.T) {
	ctx := newLiveRootChatContext(t)
	sessionID := createLiveSession(t, ctx.harness, "ses_123456789012345678901")

	_, err := e2e.RunLiveScenario(
		"one-turn-direct-answer",
		"Reply with the token ALPHA-42 and nothing else.",
		ctx.artifactDir,
		func(prompt string) (string, error) {
			return runRootChatCommand(t, "morph", "--config", ctx.configFile, "--session", sessionID, prompt)
		},
		func(output string) error {
			if !strings.Contains(strings.ToUpper(output), "ALPHA-42") {
				return fmt.Errorf("expected ALPHA-42 in live output, got %q", output)
			}

			return nil
		},
	)
	require.NoError(t, err)
}

func Test_E2E_MorphLiveHarness_ToolUsing(t *testing.T) {
	ctx := newLiveRootChatContext(t)
	ctx.skipIfNonPersistentStorage(t)
	sessionID := createLiveSession(t, ctx.harness, "ses_123456789012345678902")

	_, err := e2e.RunLiveScenario(
		"tool-using",
		"Use tools to inspect the configured filesystem roots and tell me whether "+ctx.markerPath+" exists. Reply with FOUND or MISSING only.",
		ctx.artifactDir,
		func(prompt string) (string, error) {
			return runRootChatCommand(t, "morph", "--config", ctx.configFile, "--session", sessionID, prompt)
		},
		func(output string) error {
			if !strings.Contains(strings.ToUpper(output), "FOUND") {
				return fmt.Errorf("expected FOUND in live tool output, got %q", output)
			}

			messages, err := ctx.harness.Messages(context.Background(), sessionID)
			if err != nil {
				return err
			}

			for _, message := range messages {
				if message.Role != morphmsg.RoleTool {
					continue
				}

				nameValue := str.String(message.Name)
				switch nameValue.Trim() {
				case "list_files", "read_file", "search_files":
					return nil
				}
			}

			return errors.New("expected a filesystem tool message in live scenario")
		},
	)
	require.NoError(t, err)
}

func Test_E2E_MorphLiveHarness_MultiTurnContinuity(t *testing.T) {
	ctx := newLiveRootChatContext(t)
	sessionID := createLiveSession(t, ctx.harness, "ses_123456789012345678903")

	_, err := e2e.RunLiveScenario(
		"multi-turn",
		"Remember the token BRAVO-77 for this session, then return it on a follow-up turn.",
		ctx.artifactDir,
		func(string) (string, error) {
			firstOutput, err := runRootChatCommand(
				t,
				"morph",
				"--config", ctx.configFile,
				"--session", sessionID,
				"Remember the token BRAVO-77 for this session. Reply with STORED only.",
			)
			if err != nil {
				return "", err
			}
			if !strings.Contains(strings.ToUpper(firstOutput), "STORED") {
				return "", fmt.Errorf("expected STORED in first live turn, got %q", firstOutput)
			}

			return runRootChatCommand(
				t,
				"morph",
				"--config", ctx.configFile,
				"--session", sessionID,
				"What token did I ask you to remember for this session? Reply with the token only.",
			)
		},
		func(output string) error {
			if !strings.Contains(strings.ToUpper(output), "BRAVO-77") {
				return fmt.Errorf("expected BRAVO-77 in live output, got %q", output)
			}

			return nil
		},
	)
	require.NoError(t, err)
}

func Test_E2E_MorphLiveHarness_MemorySensitive(t *testing.T) {
	ctx := newLiveRootChatContext(t)
	sessionID := createLiveSession(t, ctx.harness, "ses_123456789012345678904")

	_, err := e2e.RunLiveScenario(
		"memory-sensitive",
		"Store three mappings and answer with only the requested one on a follow-up turn.",
		ctx.artifactDir,
		func(string) (string, error) {
			firstOutput, err := runRootChatCommand(
				t,
				"morph",
				"--config", ctx.configFile,
				"--session", sessionID,
				"Store these mappings for this session: ALDER=17, BIRCH=29, CEDAR=43. Reply with STORED only.",
			)
			if err != nil {
				return "", err
			}
			if !strings.Contains(strings.ToUpper(firstOutput), "STORED") {
				return "", fmt.Errorf("expected STORED in memory setup turn, got %q", firstOutput)
			}

			return runRootChatCommand(
				t,
				"morph",
				"--config", ctx.configFile,
				"--session", sessionID,
				"What number is paired with BIRCH? Reply with BIRCH=29 only.",
			)
		},
		func(output string) error {
			normalized := strings.ToUpper(output)
			if !strings.Contains(normalized, "BIRCH=29") {
				return fmt.Errorf("expected BIRCH=29 in live output, got %q", output)
			}
			if strings.Contains(normalized, "ALDER") || strings.Contains(normalized, "CEDAR") {
				return fmt.Errorf("expected focused memory answer without distractors, got %q", output)
			}
			return nil
		},
	)
	require.NoError(t, err)
}

func TestPrepareLiveRootChatConfig_PreservesStorageBackend(t *testing.T) {
	cfg := e2e.DefaultConfig(e2e.ConfigOptions{StorageBackend: "memory"})
	prepareLiveRootChatConfig(cfg, "/tmp/live-workspace")

	require.Equal(t, []string{"/tmp/live-workspace"}, cfg.FS.Roots)
	require.Equal(t, "memory", cfg.Storage.Backend)
}

func resetRootChatE2E(t *testing.T) {
	t.Helper()

	originalProfile := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(originalProfile)
	})

	config.Set(config.NewDefaultConfig())
	testHome := t.TempDir()
	profileHome := filepath.Join(testHome, ".morph", "profiles", profile.DefaultName)
	profile.SetActive(profile.WithMetadataPaths(profile.Profile{
		Name:    profile.DefaultName,
		HomeDir: profileHome,
	}))
	t.Setenv("HOME", testHome)

	clearEnvKeys(
		t,
		"MORPH_NAME",
		"MORPH_MODEL_STREAM",
		"MORPH_SESSION_INSTRUCT",
		"MORPH_LOG_NO_COLOR",
		"MORPH_RPC_ADDRESS",
		"MORPH_RPC_PORT",
		"MORPH_CONFIG",
		"MORPH_ENV_FILE",
		profile.EnvName,
	)
}

func clearEnvKeys(t *testing.T, keys ...string) {
	t.Helper()
	keys = append(keys, "OPENAI_API_KEY", "OPENROUTER_API_KEY", "ANTHROPIC_API_KEY", "COPILOT_GITHUB_TOKEN")

	for _, key := range keys {
		original, ok := os.LookupEnv(key)
		if ok {
			t.Cleanup(func() {
				_ = os.Setenv(key, original)
			})
		} else {
			t.Cleanup(func() {
				_ = os.Unsetenv(key)
			})
		}
		_ = os.Unsetenv(key)
	}
}

func newRPCHarness(t *testing.T, home string, client models.Client, cfg *config.Config) *e2e.RPCHarness {
	t.Helper()

	if cfg == nil {
		cfg = e2e.DefaultConfig(e2e.ConfigOptions{StorageBackend: "sqlite"})
	}

	h, err := e2e.NewDefaultRPCHarness(context.Background(), home, client, cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, h.Close())
	})

	return h
}

func writeRPCConfig(t *testing.T, address string, port int, opts e2e.RPCConfigOptions) string {
	t.Helper()

	path, err := e2e.WriteRPCConfigFile(t.TempDir(), address, port, opts)
	require.NoError(t, err)
	return path
}

func writeRootChatEnv(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func runRootChatCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var output bytes.Buffer
	cmd := newRootChatCommand(&output)

	err := cmd.Run(context.Background(), args)
	return output.String(), err
}

func newRootChatCommand(output io.Writer) *urfavecli.Command {
	envFile := ".env"
	configFile := "config.yaml"

	return &urfavecli.Command{
		Name:        "morph",
		Usage:       "Run and manage your Morph daemon",
		Description: morphcli.AppDescription,
		Flags:       append(morphcli.RootFlags(&envFile, &configFile), morphcli.RequestInstructFlag()),
		Commands: []*urfavecli.Command{
			doctorcmd.NewCommand(),
			configcmd.NewCommand(output),
			profilecmd.NewCommand(),
			sessioncmd.NewCommand(),
			tracecmd.NewCommand(),
			daemoncmd.NewCommand(),
		},
		Action: morphcli.NewMainAction(morphcli.MainActionOptions{
			Output: output,
		}),
	}
}

func newWebConfig(baseURL string) *config.Config {
	cfg := e2e.DefaultConfig(e2e.ConfigOptions{StorageBackend: "sqlite"})
	cfg.Web.Provider = "firecrawl"
	baseURLValue := str.String(baseURL)
	cfg.Web.BaseURL = baseURLValue.Trim()
	cfg.Web.APIKey = "test-key"
	return cfg
}

type liveRootChatContext struct {
	harness        *e2e.RPCHarness
	configFile     string
	artifactDir    string
	markerPath     string
	storageBackend string
}

func newLiveRootChatContext(t *testing.T) liveRootChatContext {
	t.Helper()
	resetRootChatE2E(t)
	envValue := str.String(os.Getenv("MORPH_E2E_LIVE"))
	if envValue.Trim() != "1" {
		t.Skip("set MORPH_E2E_LIVE=1 to run live harness e2e")
	}

	home := filepath.Join(t.TempDir(), "morph-home")
	workspace := filepath.Join(home, "workspace")

	configPath, envPath := resolveLiveInputs(t)
	cfg, err := config.Load(envPath, configPath)
	require.NoError(t, err)
	prepareLiveRootChatConfig(cfg, workspace)

	modelClient, summaryClient, err := e2e.NewLiveClients(cfg)
	require.NoError(t, err)

	h, err := e2e.NewRPCHarness(context.Background(), e2e.HarnessOptions{
		Spec:          e2e.DefaultSpec(home),
		Config:        cfg,
		ModelClient:   modelClient,
		SummaryClient: summaryClient,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, h.Close())
	})

	require.NoError(t, os.MkdirAll(workspace, 0o755))

	markerPath := filepath.Join(workspace, "live-marker.txt")
	require.NoError(t, os.WriteFile(markerPath, []byte("live marker"), 0o600))
	backendValue := str.String(cfg.Storage.Backend)
	return liveRootChatContext{
		harness:        h,
		configFile:     writeRPCConfig(t, h.Address(), h.Port(), e2e.RPCConfigOptions{Name: "live-agent"}),
		artifactDir:    e2e.DefaultLiveArtifactDir(os.Getenv("MORPH_E2E_LIVE_ARTIFACT_DIR")),
		markerPath:     markerPath,
		storageBackend: backendValue.Normalized(),
	}
}

func prepareLiveRootChatConfig(cfg *config.Config, workspace string) {
	cfg.FS.Roots = []string{workspace}
}

func (ctx liveRootChatContext) skipIfNonPersistentStorage(t *testing.T) {
	t.Helper()

	if ctx.storageBackend == "memory" {
		t.Skip("live scenario requires persistent storage for message inspection")
	}
}

func resolveLiveInputs(t *testing.T) (string, string) {
	t.Helper()
	envValue2 := str.String(os.Getenv("MORPH_E2E_LIVE_CONFIG"))
	configPath := envValue2.Trim()
	if configPath == "" {
		candidate := filepath.Join(repoRoot(t), "config.yaml")
		if _, err := os.Stat(candidate); err == nil {
			configPath = candidate
		}
	}
	if configPath == "" {
		t.Skip("set MORPH_E2E_LIVE_CONFIG or provide config.yaml at the repo root to run live harness e2e")
	}
	envValue3 := str.String(os.Getenv("MORPH_E2E_LIVE_ENV_FILE"))
	envPath := envValue3.Trim()
	if envPath == "" {
		candidate := filepath.Join(repoRoot(t), ".env")
		if _, err := os.Stat(candidate); err == nil {
			envPath = candidate
		}
	}

	return configPath, envPath
}

func repoRoot(t *testing.T) string {
	t.Helper()

	root, err := getRepoRoot()
	require.NoError(t, err)
	return root
}

func getRepoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("resolve repo root caller")
	}

	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", "..")), nil
}

func nextTestPort(t *testing.T) string {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = lis.Close() }()

	port := lis.Addr().(*net.TCPAddr).Port
	return strconv.Itoa(port)
}

func createLiveSession(t *testing.T, h *e2e.RPCHarness, sessionID string) string {
	t.Helper()

	client, err := h.Client(context.Background())
	require.NoError(t, err)
	defer func() {
		require.NoError(t, client.Close())
	}()

	autoSwitch := false
	_, err = client.Session.CreateWithOptions(context.Background(), rpcclient.CreateSessionOptions{
		ID:         sessionID,
		AutoSwitch: &autoSwitch,
	})
	require.NoError(t, err)
	return sessionID
}
