package e2e

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/constants"
	models "github.com/xymorphic/morph/internal/model"
	"github.com/xymorphic/morph/internal/profile"
	storage "github.com/xymorphic/morph/internal/state/core"
	agent "github.com/xymorphic/morph/pkg/agent"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
)

func TestNewHarness_InMemoryConfigSmoke(t *testing.T) {
	spec := testHarnessSpec(t)
	client := NewTextClient("hello from morph")

	harness, err := NewHarness(context.Background(), HarnessOptions{
		Spec:        spec,
		Config:      testHarnessConfig(),
		ModelClient: client,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, harness.Close())
	})

	result, err := harness.Send(context.Background(), RootChatRequest{Message: "hello"})
	require.NoError(t, err)
	assert.Equal(t, "hello from morph", result.Reply)
	assert.NotEmpty(t, result.SessionID)
	assert.Empty(t, result.Events)

	messages, err := harness.Messages(context.Background(), result.SessionID)
	require.NoError(t, err)
	require.Len(t, messages, 2)
	assert.Equal(t, morphmsg.RoleUser, messages[0].Role)
	assert.Equal(t, "hello", messages[0].Content)
	assert.Equal(t, morphmsg.RoleAssistant, messages[1].Role)
	assert.Equal(t, "hello from morph", messages[1].Content)

	cfg := harness.Config()
	require.NotNil(t, cfg)
	assert.Equal(t, "Test Morph", cfg.Name)
}

func TestNewHarness_RealConfigLoadAndEnvOverride(t *testing.T) {
	spec := testHarnessSpec(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(""+
		"name: File Morph\n"+
		"models:\n"+
		"  main:\n"+
		"    name: test-model\n"+
		"    stream: true\n"+
		"storage:\n"+
		"  backend: sqlite\n"+
		"search:\n"+
		"  vector:\n"+
		"    enabled: false\n"), 0o644))
	spec.Config = ConfigInput{
		ConfigFilePath: configPath,
		Env: map[string]string{
			"MORPH_NAME":         "Env Morph",
			"OPENROUTER_API_KEY": "env-key",
			"MORPH_MODEL_STREAM": "false",
		},
	}

	harness, err := NewHarness(context.Background(), HarnessOptions{
		Spec:        spec,
		ModelClient: NewTextClient("loaded"),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, harness.Close())
	})

	cfg := harness.Config()
	require.NotNil(t, cfg)
	assert.Equal(t, "Env Morph", cfg.Name)
	require.NotNil(t, cfg.Models.Main.Stream)
	assert.False(t, *cfg.Models.Main.Stream)

	result, err := harness.Send(context.Background(), RootChatRequest{Message: "ping"})
	require.NoError(t, err)
	assert.Equal(t, "loaded", result.Reply)
}

func TestNewHarness_ToolCallModelDoubleSmoke(t *testing.T) {
	spec := testHarnessSpec(t)
	client := NewToolClient(
		models.ToolCall{ID: "call-1", Name: "time", Input: "{}"},
		"time handled",
	)

	harness, err := NewHarness(context.Background(), HarnessOptions{
		Spec:        spec,
		Config:      testHarnessConfig(),
		ModelClient: client,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, harness.Close())
	})

	result, err := harness.Send(context.Background(), RootChatRequest{Message: "what time is it"})
	require.NoError(t, err)
	assert.Equal(t, "time handled", result.Reply)

	requests := client.Requests()
	require.Len(t, requests, 2)
	require.Len(t, requests[1].Messages, 3)

	messages, err := harness.Messages(context.Background(), result.SessionID)
	require.NoError(t, err)
	require.Len(t, messages, 4)
	assert.Equal(t, morphmsg.RoleUser, messages[0].Role)
	assert.Equal(t, morphmsg.RoleAssistant, messages[1].Role)
	assert.Len(t, messages[1].ToolCalls, 1)
	assert.Equal(t, "call-1", messages[1].ToolCalls[0].ID)
	assert.Equal(t, morphmsg.RoleTool, messages[2].Role)
	assert.Equal(t, "call-1", messages[2].ToolCallID)
	assert.Equal(t, morphmsg.RoleAssistant, messages[3].Role)
	assert.Equal(t, "time handled", messages[3].Content)
}

func TestNewHarness_Errors(t *testing.T) {
	validSpec := testHarnessSpec(t)
	validConfig := testHarnessConfig()
	validClient := NewTextClient("ok")

	t.Run("invalid spec", func(t *testing.T) {
		_, err := NewHarness(context.Background(), HarnessOptions{})
		require.Error(t, err)
		assert.EqualError(t, err, "e2e entrypoint is required")
	})

	t.Run("missing model client", func(t *testing.T) {
		_, err := NewHarness(context.Background(), HarnessOptions{
			Spec:   validSpec,
			Config: validConfig,
		})
		require.Error(t, err)
		assert.EqualError(t, err, "e2e harness model client is required")
	})

	t.Run("real config with in-memory config provided", func(t *testing.T) {
		spec := validSpec
		spec.Config = ConfigInput{ConfigFilePath: filepath.Join(t.TempDir(), "config.yaml")}
		require.NoError(t, os.WriteFile(spec.Config.ConfigFilePath, []byte("model:\n  name: test-model\n"), 0o644))

		_, err := NewHarness(context.Background(), HarnessOptions{
			Spec:        spec,
			Config:      validConfig,
			ModelClient: validClient,
		})
		require.Error(t, err)
		assert.EqualError(t, err, "e2e harness must not use in-memory config when real config inputs are present")
	})

	t.Run("in-memory mode without config", func(t *testing.T) {
		_, err := NewHarness(context.Background(), HarnessOptions{
			Spec:        validSpec,
			ModelClient: validClient,
		})
		require.Error(t, err)
		assert.EqualError(t, err, "e2e harness requires config for in-memory mode")
	})

	t.Run("bad isolation path", func(t *testing.T) {
		spec := validSpec
		spec.Isolation.StoragePath = filepath.Join(t.TempDir(), "wrong.db")
		_, err := NewHarness(context.Background(), HarnessOptions{
			Spec:        spec,
			Config:      validConfig,
			ModelClient: validClient,
		})
		require.Error(t, err)
		assert.EqualError(t, err, "e2e isolation storage path must match profile home state db")
	})

	t.Run("bad isolation data dir", func(t *testing.T) {
		spec := validSpec
		spec.Isolation.DataDir = filepath.Join(t.TempDir(), "custom")
		spec.Isolation.StoragePath = filepath.Join(filepath.Dir(spec.Isolation.DataDir), "data", "state.db")
		_, err := NewHarness(context.Background(), HarnessOptions{
			Spec:        spec,
			Config:      validConfig,
			ModelClient: validClient,
		})
		require.Error(t, err)
		assert.EqualError(t, err, "e2e isolation data dir must match profile home data dir")
	})

	t.Run("agent start error", func(t *testing.T) {
		cfg := testHarnessConfig()
		cfg.Storage.Backend = "bogus"
		_, err := NewHarness(context.Background(), HarnessOptions{
			Spec:        validSpec,
			Config:      cfg,
			ModelClient: validClient,
		})
		require.Error(t, err)
		assert.EqualError(t, err, "storage backend must be one of: memory, sqlite")
	})

	t.Run("inspect store open error", func(t *testing.T) {
		original := openHarnessInspectStore
		openHarnessInspectStore = func(*config.Config) (storage.Store, error) {
			return nil, errors.New("inspect failed")
		}
		t.Cleanup(func() {
			openHarnessInspectStore = original
		})

		_, err := NewHarness(context.Background(), HarnessOptions{
			Spec:        validSpec,
			Config:      validConfig,
			ModelClient: validClient,
		})
		require.Error(t, err)
		assert.EqualError(t, err, "inspect failed")
	})
}

func TestHarnessCloseAndConfigNilPaths(t *testing.T) {
	assert.NoError(t, (*Harness)(nil).Close())
	assert.Nil(t, (*Harness)(nil).Config())
	assert.Nil(t, (&Harness{}).Config())
	assert.Empty(t, (*Harness)(nil).Stdout())
	assert.Empty(t, (*Harness)(nil).Stderr())
}

func TestHarnessSendAndMessagesErrors(t *testing.T) {
	t.Run("nil harness send", func(t *testing.T) {
		_, err := (*Harness)(nil).Send(context.Background(), RootChatRequest{Message: "hello"})
		require.Error(t, err)
		assert.EqualError(t, err, "e2e harness is required")
	})

	t.Run("invalid request", func(t *testing.T) {
		spec := testHarnessSpec(t)
		h, err := NewHarness(context.Background(), HarnessOptions{
			Spec:        spec,
			Config:      testHarnessConfig(),
			ModelClient: NewTextClient("ok"),
		})
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, h.Close())
		})

		_, err = h.Send(context.Background(), RootChatRequest{})
		require.Error(t, err)
		assert.EqualError(t, err, "e2e root chat message is required")
		assert.Equal(t, "e2e root chat message is required", h.Stderr())
	})

	t.Run("agent missing", func(t *testing.T) {
		_, err := (&Harness{}).Send(context.Background(), RootChatRequest{Message: "hello"})
		require.Error(t, err)
		assert.EqualError(t, err, "e2e harness is required")
	})

	t.Run("respond error", func(t *testing.T) {
		h := &Harness{agent: harnessAgentStub{respondErr: errors.New("respond failed")}, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
		_, err := h.Send(context.Background(), RootChatRequest{Message: "hello"})
		require.Error(t, err)
		assert.EqualError(t, err, "respond failed")
		assert.Equal(t, "respond failed", h.Stderr())
	})

	t.Run("current session error after send", func(t *testing.T) {
		h := &Harness{agent: harnessAgentStub{reply: "ok", currentErr: errors.New("current failed")}, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
		_, err := h.Send(context.Background(), RootChatRequest{Message: "hello"})
		require.Error(t, err)
		assert.EqualError(t, err, "current failed")
		assert.Equal(t, "current failed", h.Stderr())
	})

	t.Run("explicit session id skips current lookup", func(t *testing.T) {
		h := &Harness{agent: harnessAgentStub{reply: "ok", currentErr: errors.New("unused")}, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
		result, err := h.Send(context.Background(), RootChatRequest{Message: "hello", SessionID: "ses_test"})
		require.NoError(t, err)
		assert.Equal(t, "ses_test", result.SessionID)
		assert.Equal(t, "ok", result.Reply)
	})

	t.Run("events are captured", func(t *testing.T) {
		h := &Harness{agent: harnessAgentStub{
			reply:   "ok",
			current: "ses_current",
			events: []agent.Event{
				{Kind: agent.EventKindTextDelta, Channel: "assistant", Text: "a"},
				{Kind: agent.EventKindTextDelta, Channel: "reasoning", Text: "b"},
			},
		}, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
		result, err := h.Send(context.Background(), RootChatRequest{Message: "hello"})
		require.NoError(t, err)
		require.Len(t, result.Events, 2)
		assert.Equal(t, "assistant", result.Events[0].Channel)
		assert.Equal(t, "a", result.Events[0].Text)
		assert.Equal(t, "reasoning", result.Events[1].Channel)
		assert.Equal(t, "b", result.Events[1].Text)
		assert.Equal(t, "ab", h.Stdout())
	})

	t.Run("nil harness messages", func(t *testing.T) {
		_, err := (*Harness)(nil).Messages(context.Background(), "")
		require.Error(t, err)
		assert.EqualError(t, err, "e2e harness is required")
	})

	t.Run("messages current session lookup", func(t *testing.T) {
		h := &Harness{
			agent:        harnessAgentStub{current: "ses_current"},
			inspectStore: &storageStoreStub{messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}}},
		}
		messages, err := h.Messages(context.Background(), "")
		require.NoError(t, err)
		require.Len(t, messages, 1)
		assert.Equal(t, "hello", messages[0].Content)
	})

	t.Run("messages current session error", func(t *testing.T) {
		h := &Harness{
			agent:        harnessAgentStub{currentErr: errors.New("current failed")},
			inspectStore: &storageStoreStub{},
		}
		_, err := h.Messages(context.Background(), "")
		require.Error(t, err)
		assert.EqualError(t, err, "current failed")
	})

	t.Run("messages unavailable for memory store", func(t *testing.T) {
		spec := testHarnessSpec(t)
		cfg := testHarnessConfig()
		cfg.Storage.Backend = "memory"

		harness, err := NewHarness(context.Background(), HarnessOptions{
			Spec:        spec,
			Config:      cfg,
			ModelClient: NewTextClient("ok"),
		})
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, harness.Close())
		})

		_, err = harness.Messages(context.Background(), "")
		require.Error(t, err)
		assert.EqualError(t, err, "e2e harness message inspection is unavailable for non-persistent storage")
	})

	t.Run("turn messages delegates to agent", func(t *testing.T) {
		expected := []morphmsg.Message{
			{Role: morphmsg.RoleUser, Content: "hello"},
			{Role: morphmsg.RoleAssistant, Content: "ok"},
		}
		h := &Harness{agent: &harnessAgentStub{turnMessages: expected}}

		messages, err := h.TurnMessages()
		require.NoError(t, err)
		assert.Equal(t, expected, messages)
	})

	t.Run("turn messages require harness agent support", func(t *testing.T) {
		_, err := (*Harness)(nil).TurnMessages()
		require.Error(t, err)
		assert.EqualError(t, err, "e2e harness is required")

		_, err = (&Harness{}).TurnMessages()
		require.Error(t, err)
		assert.EqualError(t, err, "e2e harness is required")

		_, err = (&Harness{agent: harnessAgentStub{}}).TurnMessages()
		require.Error(t, err)
		assert.EqualError(t, err, "e2e harness turn message inspection is unavailable")
	})

	t.Run("session management delegates to agent", func(t *testing.T) {
		stub := &harnessAgentStub{created: storage.Session{ID: "ses_created"}}
		h := &Harness{agent: stub}

		created, err := h.CreateSession(context.Background(), "")
		require.NoError(t, err)
		assert.Equal(t, "ses_created", created.ID)

		require.NoError(t, h.UseSession(context.Background(), "ses_created"))
		assert.Equal(t, "ses_created", stub.usedID)
	})

	t.Run("compaction delegates to agent", func(t *testing.T) {
		expected := agent.CompactSessionResult{SessionID: "ses_compacted", SourceMessageCount: 10}
		h := &Harness{agent: &harnessAgentStub{compact: expected}}

		result, err := h.CompactSession(context.Background(), "ses_compacted")
		require.NoError(t, err)
		assert.Equal(t, expected, result)
	})

	t.Run("session and compaction require harness agent support", func(t *testing.T) {
		h := &Harness{agent: harnessAgentStub{}}

		_, err := h.CreateSession(context.Background(), "")
		require.Error(t, err)
		assert.EqualError(t, err, "e2e harness session management is unavailable")

		err = h.UseSession(context.Background(), "ses_missing")
		require.Error(t, err)
		assert.EqualError(t, err, "e2e harness session management is unavailable")

		_, err = h.CompactSession(context.Background(), "ses_missing")
		require.Error(t, err)
		assert.EqualError(t, err, "e2e harness compaction is unavailable")
	})
}

func TestOpenInspectStoreAndHelpers(t *testing.T) {
	t.Run("nil config", func(t *testing.T) {
		store, err := openInspectStore(nil)
		require.Error(t, err)
		assert.Nil(t, store)
		assert.EqualError(t, err, "config is required")
	})

	t.Run("memory backend", func(t *testing.T) {
		store, err := openInspectStore(&config.Config{Storage: config.StorageConfig{Backend: "memory"}})
		require.NoError(t, err)
		assert.Nil(t, store)
	})

	t.Run("sqlite inspect store disables reranker without mutating config", func(t *testing.T) {
		setProfileHome(t, t.TempDir())

		cfg := testHarnessConfig()
		cfg.Storage.Backend = "sqlite"
		cfg.Search.Vector.Enabled = true
		cfg.Reranker.Type = constants.RerankerLLM

		store, err := openInspectStore(cfg)
		require.NoError(t, err)
		require.NotNil(t, store)
		assert.Nil(t, cfg.Reranker.Enabled)
		assert.Nil(t, cfg.Search.EnableRerank)
	})

	t.Run("write output helpers", func(t *testing.T) {
		h := &Harness{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
		h.writeStdout("hello")
		h.writeStdout("   ")
		h.writeStderr("warn")
		h.writeStderr("")
		assert.Equal(t, "hello", h.Stdout())
		assert.Equal(t, "warn", h.Stderr())
	})

	t.Run("capture env restore", func(t *testing.T) {
		require.NoError(t, os.Setenv("MORPH_E2E_CAPTURE_ENV", "old"))
		restore := captureEnv(map[string]string{
			"MORPH_E2E_CAPTURE_ENV": "new",
			"MORPH_E2E_CAPTURE_NEW": "x",
		})
		require.NoError(t, os.Setenv("MORPH_E2E_CAPTURE_ENV", "new"))
		require.NoError(t, os.Setenv("MORPH_E2E_CAPTURE_NEW", "x"))
		restore()
		assert.Equal(t, "old", os.Getenv("MORPH_E2E_CAPTURE_ENV"))
		assert.Empty(t, os.Getenv("MORPH_E2E_CAPTURE_NEW"))
	})

	t.Run("apply env with explicit profile", func(t *testing.T) {
		spec := testHarnessSpec(t)
		spec.Config.Env = map[string]string{profile.EnvName: "research"}
		restore, err := applyHarnessEnv(spec)
		require.NoError(t, err)
		t.Cleanup(restore)
		assert.Equal(t, "research", os.Getenv(profile.EnvName))
		assert.Equal(t, "research", profile.Active().Name)
	})

	t.Run("apply env set failure", func(t *testing.T) {
		original := setHarnessEnv
		setHarnessEnv = func(string, string) error { return errors.New("setenv failed") }
		t.Cleanup(func() {
			setHarnessEnv = original
		})

		spec := testHarnessSpec(t)
		spec.Config.Env = map[string]string{profile.EnvName: "research"}

		_, err := applyHarnessEnv(spec)
		require.Error(t, err)
		assert.EqualError(t, err, "setenv failed")
	})
}

func TestStorageStoreStub_NoOpMethods(t *testing.T) {
	store := &storageStoreStub{
		messages: []morphmsg.Message{{Role: morphmsg.RoleUser, Content: "hello"}},
	}

	require.NoError(t, store.Save(context.Background(), storage.Session{}))

	session, ok, err := store.Get(context.Background(), "ses_test", storage.SessionGetOptions{})
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, storage.Session{}, session)

	sessions, err := store.List(context.Background(), storage.SessionListOptions{})
	require.NoError(t, err)
	assert.Nil(t, sessions)

	require.NoError(t, store.Delete(context.Background(), "ses_test"))
	require.NoError(t, store.AppendMessages(context.Background(), "ses_test", nil))

	messages, err := store.GetMessages(context.Background(), "ses_test", storage.MessageQueryOptions{})
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, "hello", messages[0].Content)

	searchResults, err := store.SearchMessages(context.Background(), "ses_test", storage.SearchMessageOptions{})
	require.NoError(t, err)
	assert.Nil(t, searchResults)

	count, err := store.CountMessages(context.Background(), "ses_test", storage.MessageQueryOptions{})
	require.NoError(t, err)
	assert.Zero(t, count)

	message, found, err := store.GetMessage(context.Background(), "ses_test", 0)
	require.NoError(t, err)
	assert.False(t, found)
	assert.Equal(t, morphmsg.Message{}, message)

	records, err := store.GetMessagesByIDs(context.Background(), "ses_test", []uint{1})
	require.NoError(t, err)
	assert.Nil(t, records)

	records, err = store.GetMessageWindow(context.Background(), "ses_test", 1, 1, 1)
	require.NoError(t, err)
	assert.Nil(t, records)

	require.NoError(t, store.SaveSummary(context.Background(), storage.SessionSummary{}))

	summary, summaryFound, err := store.GetSummary(context.Background(), "ses_test")
	require.NoError(t, err)
	assert.False(t, summaryFound)
	assert.Equal(t, storage.SessionSummary{}, summary)

	require.NoError(t, store.DeleteSummary(context.Background(), "ses_test"))
	require.NoError(t, store.DeleteExpiredArchives(context.Background(), time.Now()))
	require.NoError(t, store.ClearMessages(context.Background(), "ses_test"))
	require.NoError(t, store.SetCurrent(context.Background(), "ses_test"))

	current, currentOK, err := store.Current(context.Background())
	require.NoError(t, err)
	assert.False(t, currentOK)
	assert.Empty(t, current)

	addr := stubAddr("pipe")
	assert.Equal(t, "pipe", addr.Network())
	assert.Equal(t, "pipe", addr.String())

	listener := stubListener{addr: addr}
	conn, err := listener.Accept()
	require.Error(t, err)
	assert.Nil(t, conn)
	assert.EqualError(t, err, "accept unsupported")
	assert.Equal(t, addr, listener.Addr())
	assert.NoError(t, listener.Close())
}

func testHarnessSpec(t *testing.T) HarnessSpec {
	t.Helper()
	return DefaultSpec(filepath.Join(t.TempDir(), "morph-home"))
}

func testHarnessConfig() *config.Config {
	return DefaultConfig(ConfigOptions{})
}

func setProfileHome(t *testing.T, home string) {
	t.Helper()

	original := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(original)
	})
	profile.SetActive(profile.Profile{Name: "test", HomeDir: home})
}
