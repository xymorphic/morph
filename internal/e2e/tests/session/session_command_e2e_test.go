package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sessioncmd "github.com/xymorphic/morph/cmd/session"
	"github.com/xymorphic/morph/internal/e2e"
	models "github.com/xymorphic/morph/internal/model"
	"github.com/xymorphic/morph/internal/profile"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/logutils"
)

func init() {
	logutils.SetOutput(io.Discard)
}

func Test_E2E_SessionCommand_CreateSessionViaRPCSmoke(t *testing.T) {
	h, err := e2e.NewDefaultRPCHarness(
		context.Background(),
		t.TempDir()+"/morph-home",
		e2e.NewTextClient("ok"),
		e2e.DefaultConfig(e2e.ConfigOptions{StorageBackend: "memory"}),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, h.Close())
	})

	output, err := runSessionCommand(t, h, "session", "new", "ses_123456789012345678901")
	require.NoError(t, err)
	require.Equal(t, "ses_123456789012345678901\n", output)
}

func Test_E2E_SessionCommand_CreateListUseCurrentAndChatFlow(t *testing.T) {
	newSessionHarness := func(t *testing.T) *e2e.RPCHarness {
		home := t.TempDir() + "/morph-home"
		h, err := e2e.NewDefaultRPCHarness(
			context.Background(),
			home,
			e2e.NewTextClient("session reply"),
			e2e.DefaultConfig(e2e.ConfigOptions{StorageBackend: "sqlite"}),
		)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, h.Close()) })
		return h
	}

	createSession := func(t *testing.T, h *e2e.RPCHarness, sessionID string) {
		output, err := runSessionCommand(t, h, "session", "new", sessionID)
		require.NoError(t, err)
		assert.Equal(t, sessionID+"\n", output)
	}

	t.Run("Create sessions", func(t *testing.T) {
		h := newSessionHarness(t)

		createSession(t, h, "ses_123456789012345678901")
		createSession(t, h, "ses_123456789012345678902")

		_, err := runSessionCommand(t, h, "session", "new", "ses_1234567890123")
		require.Error(t, err)
		assert.ErrorContains(t, err, "session id must be a valid ses_ nanoid")

		_, err = runSessionCommand(t, h, "session", "new", "ses_123456789012345678902")
		require.Error(t, err)
		assert.ErrorContains(t, err, "session already exists")
	})

	t.Run("List sessions", func(t *testing.T) {
		h := newSessionHarness(t)
		createSession(t, h, "ses_123456789012345678901")
		createSession(t, h, "ses_123456789012345678902")

		output, err := runSessionCommand(t, h, "session", "list")
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(output, "ID"))
		assert.Contains(t, output, "default")
		assert.Contains(t, output, "ses_123456789012345678901")
		assert.Contains(t, output, "ses_123456789012345678902")
	})

	t.Run("Switch and check current session", func(t *testing.T) {
		h := newSessionHarness(t)
		createSession(t, h, "ses_123456789012345678901")
		createSession(t, h, "ses_123456789012345678902")

		output, err := runSessionCommand(t, h, "session", "use", "ses_123456789012345678902")
		require.NoError(t, err)
		assert.Equal(t, "ses_123456789012345678902\n", output)

		output, err = runSessionCommand(t, h, "session", "current")
		require.NoError(t, err)
		assert.Contains(t, output, "Session\n")
		assert.Contains(t, output, "ID:                  ses_123456789012345678902")
	})

	t.Run("Send message and check chat flow", func(t *testing.T) {
		h := newSessionHarness(t)
		createSession(t, h, "ses_123456789012345678902")

		result, err := h.Send(context.Background(), e2e.RootChatRequest{
			Message:   "hello from selected session",
			SessionID: "ses_123456789012345678902",
		})
		require.NoError(t, err)
		assert.Equal(t, "session reply", result.Reply)
		assert.Equal(t, "ses_123456789012345678902", result.SessionID)
	})

	t.Run("Fetch messages and validate", func(t *testing.T) {
		h := newSessionHarness(t)
		createSession(t, h, "ses_123456789012345678902")

		_, err := h.Send(context.Background(), e2e.RootChatRequest{
			Message:   "hello from selected session",
			SessionID: "ses_123456789012345678902",
		})
		require.NoError(t, err)

		messages, err := h.Messages(context.Background(), "ses_123456789012345678902")
		require.NoError(t, err)
		require.Len(t, messages, 2)
		assert.Equal(t, []morphmsg.Role{morphmsg.RoleUser, morphmsg.RoleAssistant}, []morphmsg.Role{messages[0].Role, messages[1].Role})
		assert.Equal(t, "hello from selected session", messages[0].Content)
		assert.Equal(t, "session reply", messages[1].Content)
	})
}

func Test_E2E_SessionCommand_DefaultSessionBehavior(t *testing.T) {
	home := t.TempDir() + "/morph-home"

	h, err := e2e.NewDefaultRPCHarness(
		context.Background(),
		home,
		e2e.NewTextClient("default reply"),
		e2e.DefaultConfig(e2e.ConfigOptions{StorageBackend: "sqlite"}),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, h.Close())
	})

	result, err := h.Send(context.Background(), e2e.RootChatRequest{Message: "hello default"})
	require.NoError(t, err)
	assert.Equal(t, "default reply", result.Reply)
	assert.Equal(t, "default", result.SessionID)

	output, err := runSessionCommand(t, h, "session", "current")
	require.NoError(t, err)
	assert.Contains(t, output, "Session\n")
	assert.Contains(t, output, "ID:                  default")

	output, err = runSessionCommand(t, h, "session", "list")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(output, "ID"))
	assert.Contains(t, output, "default")
}

func Test_E2E_SessionCommand_PersistenceCompactionStatusAndSummaryReuse(t *testing.T) {
	home := t.TempDir() + "/morph-home"

	modelClient := e2e.NewClient(
		e2e.OutputTextStep("reply 1"),
		e2e.OutputTextStep("reply 2"),
		e2e.OutputTextStep("reply 3"),
		e2e.OutputTextStep("reply 4"),
		e2e.OutputTextStep("reply 5"),
	)
	summaryClient := e2e.NewClient(
		e2e.OutputTextStep("Turn Planning"),
		e2e.OutputTextStep("no durable memory to flush"),
		e2e.OutputTextStep(`{"session_summary":"Older context","current_task":"Continue helping","discoveries":["Saved summary"],"open_questions":[],"next_actions":["Answer the next turn"]}`),
		e2e.OutputTextStep("no durable memory to flush"),
	)

	h1 := newPersistentSessionHarness(t, home, modelClient, summaryClient)

	var sessionID string
	for i := 1; i <= 5; i++ {
		result, err := h1.Send(context.Background(), e2e.RootChatRequest{Message: fmt.Sprintf("turn %d", i)})
		require.NoError(t, err)
		assert.Equal(t, fmt.Sprintf("reply %d", i), result.Reply)
		if sessionID == "" {
			sessionID = result.SessionID
		}
	}
	require.Equal(t, "default", sessionID)

	compactOutput, err := runSessionCommand(t, h1, "session", "compact", sessionID)
	require.NoError(t, err)
	assert.Contains(t, compactOutput, "Session ID:          default")
	assert.Contains(t, compactOutput, "Source end offset:   2")
	assert.Contains(t, compactOutput, "Source messages:     10")

	statusOutput, err := runSessionCommand(t, h1, "session", "status", sessionID)
	require.NoError(t, err)
	assert.Contains(t, statusOutput, "ID:                  default")
	assert.Contains(t, statusOutput, "Compaction status:   succeeded")
	assert.Contains(t, statusOutput, "Offset:              2")
	assert.Contains(t, statusOutput, "Size:                10")

	require.NoError(t, h1.Close())

	followupClient := e2e.NewClient(e2e.Step{
		Check: func(req models.Request) error {
			if !strings.Contains(req.Instructions, "# Session Summary\n\nOlder context") {
				return fmt.Errorf("expected persisted session summary in instructions")
			}
			if len(req.Messages) != 9 {
				return fmt.Errorf("expected 9 messages after summary trimming, got %d", len(req.Messages))
			}
			if req.Messages[0].Role != morphmsg.RoleUser || req.Messages[0].Content != "turn 2" {
				return errors.New("expected trimmed history to start at the second turn")
			}
			if req.Messages[7].Role != morphmsg.RoleAssistant || req.Messages[7].Content != "reply 5" {
				return errors.New("expected latest retained assistant reply before follow-up")
			}
			if req.Messages[8].Role != morphmsg.RoleUser || req.Messages[8].Content != "after restart" {
				return errors.New("expected follow-up user message after restart")
			}

			return nil
		},
		Response: &models.Response{OutputText: "reply after restart"},
	})

	h2 := newPersistentSessionHarness(t, home, followupClient, e2e.NewTextClient(`{"session_summary":"ignored","current_task":"","discoveries":[],"open_questions":[],"next_actions":[]}`))

	result, err := h2.Send(context.Background(), e2e.RootChatRequest{Message: "after restart"})
	require.NoError(t, err)
	assert.Equal(t, "reply after restart", result.Reply)
	assert.Equal(t, "default", result.SessionID)

	output, err := runSessionCommand(t, h2, "session", "current")
	require.NoError(t, err)
	assert.Contains(t, output, "ID:                  default")
	assert.Contains(t, output, "Title:               Turn Planning")
}

func runSessionCommand(t *testing.T, h *e2e.RPCHarness, args ...string) (string, error) {
	t.Helper()

	originalProfile := profile.Active()
	profileHome := filepath.Join(t.TempDir(), "profile")
	require.NoError(t, os.MkdirAll(profileHome, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(profileHome, "config.yaml"), []byte(h.ConfigFileContents()), 0o600))
	profile.SetActive(profile.WithMetadataPaths(profile.Profile{Name: "session-e2e", HomeDir: profileHome}))

	var output bytes.Buffer
	previousOutput := sessioncmd.SetOutput(&output)
	t.Cleanup(func() {
		sessioncmd.SetOutput(previousOutput)
		profile.SetActive(originalProfile)
	})

	err := sessioncmd.NewCommand().Run(context.Background(), args)
	return output.String(), err
}

func newPersistentSessionHarness(t *testing.T, home string, modelClient, summaryClient models.Client) *e2e.RPCHarness {
	t.Helper()

	h, err := e2e.NewRPCHarness(context.Background(), e2e.HarnessOptions{
		Spec:          e2e.DefaultSpec(home),
		Config:        e2e.DefaultConfig(e2e.ConfigOptions{StorageBackend: "sqlite"}),
		ModelClient:   modelClient,
		SummaryClient: summaryClient,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		if h != nil {
			_ = h.Close()
		}
	})

	return h
}
