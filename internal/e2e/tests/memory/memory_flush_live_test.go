package memory

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/config"
	e2e "github.com/xymorphic/morph/internal/e2e"
	models "github.com/xymorphic/morph/internal/model"
	storage "github.com/xymorphic/morph/internal/state/core"
	statemanager "github.com/xymorphic/morph/internal/state/manager"
	"github.com/xymorphic/morph/internal/trace"
	morphmsg "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/str"
)

func TestLiveMemoryFlushRunsBeforeCompaction(t *testing.T) {
	envValue := str.String(os.Getenv("MORPH_E2E_LIVE"))
	if envValue.Trim() != "1" {
		t.Skip("set MORPH_E2E_LIVE=1 to run live LLM e2e tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)

	harness, cfg, summaryClient := newLiveMemoryFlushHarness(t, ctx)
	closed := false
	t.Cleanup(func() {
		if !closed {
			require.NoError(t, harness.Close())
		}
	})

	manager := loadLiveMemoryStateManager(t, cfg, summaryClient)
	sessionID := seedLiveMemoryFlushHistory(t, ctx, manager, storage.DefaultSessionID, "blue-harbor")

	_, err := harness.CompactSession(ctx, sessionID)
	require.NoError(t, err)

	events := requireLiveMemoryFlushTrace(t, ctx, manager, sessionID, "compression")
	require.Less(
		t,
		liveTraceEventIndex(events, trace.EvtMemoryFlushCompleted),
		liveTraceEventIndex(events, trace.EvtContextCompactionRunning),
	)
}

func TestLiveMemoryFlushCanProduceMemoryBeforeCompaction(t *testing.T) {
	envValue2 := str.String(os.Getenv("MORPH_E2E_LIVE"))
	if envValue2.Trim() != "1" {
		t.Skip("set MORPH_E2E_LIVE=1 to run live LLM e2e tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)

	harness, cfg, summaryClient := newLiveMemoryFlushHarness(t, ctx, liveMemoryFlushHarnessOptions{
		MaxCalls: 1,
	})
	closed := false
	t.Cleanup(func() {
		if !closed {
			require.NoError(t, harness.Close())
		}
	})

	manager := loadLiveMemoryStateManager(t, cfg, summaryClient)
	store := loadLiveMemoryStore(t, cfg, summaryClient)
	sessionID := seedLiveMemoryFlushOutcomeHistory(t, ctx, manager, storage.DefaultSessionID, "lighthouse-cohort")

	_, err := harness.CompactSession(ctx, sessionID)
	require.NoError(t, err)

	events := requireLiveMemoryFlushTrace(t, ctx, manager, sessionID, "compression")
	require.Contains(t, liveTraceEventTypes(events), trace.EvtMemoryFlushWriteRequested)

	item := waitForLiveMemoryFlushMemory(t, ctx, store, sessionID, "lighthouse-cohort")
	require.Contains(t, strings.ToLower(getLiveMemorySearchableText(item)), "lighthouse-cohort")
}

func TestLiveMemoryFlushDoesNotRunBeforeSessionSwitch(t *testing.T) {
	envValue3 := str.String(os.Getenv("MORPH_E2E_LIVE"))
	if envValue3.Trim() != "1" {
		t.Skip("set MORPH_E2E_LIVE=1 to run live LLM e2e tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)

	harness, cfg, summaryClient := newLiveMemoryFlushHarness(t, ctx)
	closed := false
	t.Cleanup(func() {
		if !closed {
			require.NoError(t, harness.Close())
		}
	})

	manager := loadLiveMemoryStateManager(t, cfg, summaryClient)
	sessionID := seedLiveMemoryFlushHistory(t, ctx, manager, storage.DefaultSessionID, "green-harbor")
	target, err := harness.CreateSession(ctx, "")
	require.NoError(t, err)

	require.NoError(t, harness.UseSession(ctx, target.ID))

	result, err := manager.ListTraceEvents(ctx, storage.TraceQuery{SessionID: sessionID, Limit: 50})
	require.NoError(t, err)
	for _, event := range result.Events {
		require.NotEqual(t, trace.EvtMemoryFlushCompleted, event.Type)
		require.NotEqual(t, trace.EvtMemoryFlushFailed, event.Type)
		require.NotEqual(t, trace.EvtMemoryFlushSkipped, event.Type)
	}
}

func TestLiveMemoryFlushRunsBeforeAgentClose(t *testing.T) {
	envValue4 := str.String(os.Getenv("MORPH_E2E_LIVE"))
	if envValue4.Trim() != "1" {
		t.Skip("set MORPH_E2E_LIVE=1 to run live LLM e2e tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)

	harness, cfg, summaryClient := newLiveMemoryFlushHarness(t, ctx)
	manager := loadLiveMemoryStateManager(t, cfg, summaryClient)
	sessionID := seedLiveMemoryFlushHistory(t, ctx, manager, storage.DefaultSessionID, "silver-harbor")

	require.NoError(t, harness.Close())

	events := requireLiveMemoryFlushTrace(t, ctx, manager, sessionID, "controlled exit")
	require.Contains(t, liveTraceEventTypes(events), trace.EvtMemoryFlushCompleted)
}

type liveMemoryFlushHarnessOptions struct {
	WriteEnabled bool
	MaxCalls     int
}

func newLiveMemoryFlushHarness(
	t *testing.T,
	ctx context.Context,
	options ...liveMemoryFlushHarnessOptions,
) (*e2e.Harness, *config.Config, models.Client) {
	t.Helper()

	opts := liveMemoryFlushHarnessOptions{MaxCalls: 1}
	if len(options) > 0 {
		opts = options[0]
	}

	home := t.TempDir()
	spec := e2e.DefaultSpec(home)
	cfg := loadProductionConfigForLiveMemoryE2E(t, spec)
	setLiveMemoryE2EConfig(cfg, spec)

	enabled := true
	disabled := false
	cfg.Cap.Memory = &enabled
	cfg.Memory.Write.Enabled = &disabled
	if opts.WriteEnabled {
		cfg.Memory.Write.Enabled = &enabled
	}
	cfg.Memory.Flush.Enabled = &enabled
	cfg.Memory.Flush.MaxCalls = opts.MaxCalls
	cfg.Memory.Flush.MaxOutputTokens = 96
	cfg.Memory.Flush.Timeout = 20 * time.Second
	cfg.Memory.Episodic.Enabled = &disabled
	cfg.Memory.Reflection.Enabled = &disabled
	cfg.Memory.Promotion.Enabled = &disabled
	cfg.Trace.Enabled = true
	cfg.Trace.Database.Enabled = &enabled
	cfg.Trace.Disk.Enabled = &disabled
	cfg.Compaction.Enabled = &enabled
	cfg.Normalize()

	modelClient, summaryClient, err := e2e.NewLiveClients(cfg)
	require.NoError(t, err)
	recordingModelClient := &recordingLiveModelClient{client: modelClient}

	harness, err := e2e.NewHarness(ctx, e2e.HarnessOptions{
		Spec:          spec,
		Config:        cfg,
		ModelClient:   recordingModelClient,
		SummaryClient: summaryClient,
	})
	require.NoError(t, err)

	return harness, cfg, summaryClient
}

func seedLiveMemoryFlushHistory(
	t *testing.T,
	ctx context.Context,
	manager *statemanager.Manager,
	sessionID string,
	codename string,
) string {
	t.Helper()

	session, err := manager.Resolve(ctx, sessionID)
	require.NoError(t, err)

	messages := []morphmsg.Message{
		{
			Role: morphmsg.RoleUser,
			Content: strings.Join([]string{
				"For the Orion dashboard refresh, let's use " + codename + " as the launch codename in status updates.",
				"The API rollout stays separate, but the dashboard refresh should use that name.",
			}, " "),
		},
		{Role: morphmsg.RoleAssistant, Content: "Got it. I will use " + codename + " for the Orion dashboard refresh."},
	}
	for range 8 {
		messages = append(messages,
			morphmsg.Message{Role: morphmsg.RoleUser, Content: "We checked one more rollout note and kept the dashboard plan unchanged."},
			morphmsg.Message{Role: morphmsg.RoleAssistant, Content: "Noted. The dashboard plan is unchanged."},
		)
	}

	require.NoError(t, manager.AppendMessages(ctx, session.ID, messages))
	require.NoError(t, manager.UpdateLastPromptTokens(ctx, session.ID, 2048))

	return session.ID
}

func seedLiveMemoryFlushOutcomeHistory(
	t *testing.T,
	ctx context.Context,
	manager *statemanager.Manager,
	sessionID string,
	label string,
) string {
	t.Helper()

	session, err := manager.Resolve(ctx, sessionID)
	require.NoError(t, err)

	messages := []morphmsg.Message{
		{
			Role: morphmsg.RoleUser,
			Content: strings.Join([]string{
				"Going forward, in monthly operations notes, label the long-running enterprise beta segment " + label + ".",
				"Use the label only for that segment; the self-serve beta group keeps its current name.",
			}, " "),
		},
		{Role: morphmsg.RoleAssistant, Content: "Understood. Monthly operations notes will use " + label + " for the enterprise beta segment."},
	}
	for range 8 {
		messages = append(messages,
			morphmsg.Message{Role: morphmsg.RoleUser, Content: "We reviewed another operations note and left the segment naming decision unchanged."},
			morphmsg.Message{Role: morphmsg.RoleAssistant, Content: "Noted. The segment naming decision remains unchanged."},
		)
	}

	require.NoError(t, manager.AppendMessages(ctx, session.ID, messages))
	require.NoError(t, manager.UpdateLastPromptTokens(ctx, session.ID, 2048))

	return session.ID
}

func waitForLiveMemoryFlushMemory(
	t *testing.T,
	ctx context.Context,
	store liveMemoryStore,
	sessionID string,
	required ...string,
) storage.MemoryItem {
	t.Helper()

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		item, ok := getLiveMemoryFlushMemory(ctx, t, store, sessionID, required...)
		if ok {
			return item
		}

		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for memory flush memory; memories: %s", getLiveMemoryDump(context.Background(), t, store, sessionID))
		case <-ticker.C:
		}
	}
}

func getLiveMemoryFlushMemory(
	ctx context.Context,
	t *testing.T,
	store liveMemoryStore,
	sessionID string,
	required ...string,
) (storage.MemoryItem, bool) {
	t.Helper()
	sessionIDValue := str.String(sessionID)
	result, err := store.SearchMemory(ctx, storage.MemorySearchQuery{
		SessionID: sessionIDValue.Trim(),
		Statuses: []storage.MemoryStatus{
			storage.MemoryStatusCandidate,
			storage.MemoryStatusActive,
		},
		Limit: 20,
	})
	require.NoError(t, err)

	for _, hit := range result.Hits {
		if hasLiveMemoryText(hit.Item, required...) {
			return hit.Item, true
		}
	}

	return storage.MemoryItem{}, false
}

func requireLiveMemoryFlushTrace(
	t *testing.T,
	ctx context.Context,
	manager *statemanager.Manager,
	sessionID string,
	trigger string,
) []storage.TraceEvent {
	t.Helper()

	traceCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		result, err := manager.ListTraceEvents(ctx, storage.TraceQuery{SessionID: sessionID, Limit: 200})
		require.NoError(t, err)

		if hasLiveMemoryFlushTrace(result.Events, trigger) {
			return result.Events
		}

		select {
		case <-traceCtx.Done():
			t.Fatalf("timed out waiting for memory flush trace %q; events: %v", trigger, liveTraceEventTypes(result.Events))
		case <-ticker.C:
		}
	}
}

func hasLiveMemoryFlushTrace(events []storage.TraceEvent, trigger string) bool {
	required := map[string]bool{
		trace.EvtMemoryFlushStarted:        false,
		trace.EvtMemoryFlushModelRequested: false,
		trace.EvtMemoryFlushCompleted:      false,
	}

	for _, event := range events {
		if _, ok := required[event.Type]; !ok {
			continue
		}
		if liveTracePayloadString(event, "trigger") != trigger {
			continue
		}

		required[event.Type] = true
	}

	for _, ok := range required {
		if !ok {
			return false
		}
	}

	return true
}

func liveTracePayloadString(event storage.TraceEvent, key string) string {
	payload, ok := event.Payload.(map[string]any)
	if !ok {
		return ""
	}

	value, ok := payload[key].(string)
	if !ok {
		return ""
	}
	valueText := str.String(value)
	return valueText.Trim()
}

func liveTraceEventTypes(events []storage.TraceEvent) []string {
	types := make([]string, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}

	return types
}

func liveTraceEventIndex(events []storage.TraceEvent, eventType string) int {
	for idx, event := range events {
		if event.Type == eventType {
			return idx
		}
	}

	return -1
}
