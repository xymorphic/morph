package environment

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/agent/runcontext"
	commandplan "github.com/xymorphic/morph/internal/command"
	"github.com/xymorphic/morph/internal/environment/planstore"
	"github.com/xymorphic/morph/internal/environment/process"
	"github.com/xymorphic/morph/internal/environment/sessionmessages"
	"github.com/xymorphic/morph/internal/environment/sessionsearch"
	"github.com/xymorphic/morph/internal/guardrails"
	morphmemory "github.com/xymorphic/morph/internal/memory"
	"github.com/xymorphic/morph/internal/memory/episodic"
	storage "github.com/xymorphic/morph/internal/state/core"
	statemanager "github.com/xymorphic/morph/internal/state/manager"
	memory "github.com/xymorphic/morph/internal/state/storememory"
	messages "github.com/xymorphic/morph/pkg/agent/message"
	"github.com/xymorphic/morph/pkg/nanoid"
)

var runtimeSearchSessionID = nanoid.MustFromSeed(storage.SessionIDPrefix, "runtime-search", "EnvironmentRuntimeTestSeed")

func TestNewRuntime_DefaultsRootToCWDAndNormalizesPolicy(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	duplicated := commandplan.Selector{Executable: "git", ArgumentPrefix: []string{"push"}}
	runtime := NewRuntime(nil, guardrails.CommandPolicy{
		DenyCommands: []commandplan.Selector{duplicated, duplicated},
	}, nil)

	require.Equal(t, []string{dir}, runtime.FilePolicy().Roots)
	require.Equal(t, []commandplan.Selector{{
		Executable:     "git",
		ArgumentPrefix: []string{"push"},
		Modes:          []commandplan.Mode{commandplan.ModeDirect, commandplan.ModePOSIXShell},
	}}, runtime.CommandPolicy().DenyCommands)
	require.IsType(t, &process.DefaultManager{}, runtime.processMgr)
	require.IsType(t, &planstore.MemoryPlanStore{}, runtime.plans)
}

func TestRuntime_CommandIdentityKeyIsIsolatedAndCanBindProfileCredential(t *testing.T) {
	var nilRuntime *Runtime
	require.Nil(t, nilRuntime.CommandIdentityKey())
	nilRuntime.setCommandIdentityKey([]byte("ignored"))

	left := NewRuntime([]string{t.TempDir()}, guardrails.CommandPolicy{}, nil)
	right := NewRuntime([]string{t.TempDir()}, guardrails.CommandPolicy{}, nil)

	require.Len(t, left.CommandIdentityKey(), 32)
	require.NotEqual(t, left.CommandIdentityKey(), right.CommandIdentityKey())

	credential := []byte("0123456789abcdef0123456789abcdef")
	left.setCommandIdentityKey(credential)
	right.setCommandIdentityKey(credential)
	require.Equal(t, left.CommandIdentityKey(), right.CommandIdentityKey())

	returned := left.CommandIdentityKey()
	returned[0] ^= 0xff
	require.NotEqual(t, returned, left.CommandIdentityKey())

	before := left.CommandIdentityKey()
	left.setCommandIdentityKey(nil)
	require.Equal(t, before, left.CommandIdentityKey())
}

func TestRuntime_BrowserServiceReportsAvailability(t *testing.T) {
	var nilRuntime *Runtime
	service, ok, err := nilRuntime.BrowserService(context.Background())
	require.EqualError(t, err, "runtime is required")
	require.False(t, ok)
	require.Nil(t, service)

	runtime := NewRuntime([]string{t.TempDir()}, guardrails.CommandPolicy{}, nil)
	service, ok, err = runtime.BrowserService(context.Background())
	require.NoError(t, err)
	require.False(t, ok)
	require.Nil(t, service)

	want := &browserServiceTestStub{}
	runtime.browser = want
	service, ok, err = runtime.BrowserService(context.Background())
	require.NoError(t, err)
	require.True(t, ok)
	require.Same(t, want, service)
}

func TestNewRuntime_FallsBackWhenGetwdFails(t *testing.T) {
	originalGetwd := getwd

	t.Cleanup(func() {
		getwd = originalGetwd
	})

	getwd = func() (string, error) {
		return "", errors.New("getwd failed")
	}

	runtime := NewRuntime(nil, guardrails.CommandPolicy{}, nil)

	expectedRoot, err := filepath.Abs(".")
	require.NoError(t, err)
	require.Equal(t, []string{filepath.Clean(expectedRoot)}, runtime.FilePolicy().Roots)
}

func TestNewRuntime_NormalizesConfiguredRoots(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "workspace")

	runtime := NewRuntime([]string{root, filepath.Join(root, ".")}, guardrails.CommandPolicy{}, nil)

	require.Equal(t, []string{root}, runtime.FilePolicy().Roots)
}

func TestRuntime_FilePolicyHandlesNilReceiver(t *testing.T) {
	var runtime *Runtime

	require.Equal(t, guardrails.NormalizeRoots(nil), runtime.FilePolicy().Roots)
}

func TestRuntime_CommandPolicyHandlesNilReceiver(t *testing.T) {
	var runtime *Runtime

	require.Equal(t, guardrails.CommandPolicy{}.Normalize(), runtime.CommandPolicy())
}

func TestRuntime_PlanMethodsDelegateToStore(t *testing.T) {
	runtime := NewRuntime([]string{t.TempDir()}, guardrails.CommandPolicy{}, nil)

	replaced, err := runtime.ReplacePlan("session-1", planstore.Plan{
		Steps: []planstore.PlanStep{
			{
				ID:      "step-1",
				Content: "First",
				Status:  planstore.PlanStatusInProgress,
			},
		},
	})
	require.NoError(t, err)

	merged, err := runtime.MergePlan("session-1", []planstore.PartialPlanStep{{
		ID:      "step-2",
		Content: ptrTo("Second"),
		Status:  ptrTo(planstore.PlanStatusPending),
	}}, "updated", false)
	require.NoError(t, err)

	cleared := runtime.ClearPlan("session-1")

	require.Len(t, replaced.Steps, 1)
	require.Len(t, merged.Steps, 2)
	require.Equal(t, "updated", merged.Explanation)
	require.Equal(t, planstore.Plan{}, cleared)
	require.Equal(t, planstore.Plan{}, runtime.GetPlan("session-1"))
}

func TestRuntime_PlanMethodsHandleNilReceiver(t *testing.T) {
	var runtime *Runtime

	require.Equal(t, planstore.Plan{}, runtime.GetPlan("session-1"))

	replaced, err := runtime.ReplacePlan("session-1", planstore.Plan{})
	require.Equal(t, planstore.Plan{}, replaced)
	require.EqualError(t, err, "plan store is required")

	require.Equal(t, planstore.Plan{}, runtime.ClearPlan("session-1"))

	_, err = runtime.MergePlan("session-1", nil, "", false)
	require.EqualError(t, err, "plan store is required")

	runtime.HydratePlan("session-1", planstore.Plan{
		Steps: []planstore.PlanStep{
			{
				ID:      "step-1",
				Content: "First",
				Status:  planstore.PlanStatusInProgress,
			},
		},
	})

	require.Equal(t, planstore.Plan{}, runtime.GetPlan("session-1"))
}

func TestRuntime_HydratePlanDelegatesToStore(t *testing.T) {
	runtime := NewRuntime([]string{t.TempDir()}, guardrails.CommandPolicy{}, nil)

	runtime.HydratePlan("session-1", planstore.Plan{
		Steps: []planstore.PlanStep{
			{
				ID:      "step-1",
				Content: "First",
				Status:  planstore.PlanStatusInProgress,
			},
		},
		Explanation: "restored",
	})

	require.Equal(t, planstore.Plan{
		Steps: []planstore.PlanStep{
			{
				ID:      "step-1",
				Content: "First",
				Status:  planstore.PlanStatusInProgress,
			},
		},
		Explanation: "restored",
	}, runtime.GetPlan("session-1"))
}

func TestRuntime_SearchSessionDelegatesToStateManager(t *testing.T) {
	store := memory.NewStore()
	manager, err := statemanager.NewManager(store, time.Minute, time.Hour)
	require.NoError(t, err)
	require.NoError(t, manager.Save(context.Background(), memory.Session{ID: runtimeSearchSessionID}))
	require.NoError(t, manager.AppendMessages(context.Background(), runtimeSearchSessionID, []messages.Message{
		{
			Role:      messages.RoleUser,
			Content:   "hello world",
			CreatedAt: time.Now().UTC(),
		},
		{
			Role:       messages.RoleTool,
			Name:       "process",
			Content:    `{"process":{"id":"proc_1","status":"running"}}`,
			ToolCallID: "call-1",
			CreatedAt:  time.Now().UTC(),
		},
	}))

	runtime := NewRuntime([]string{t.TempDir()}, guardrails.CommandPolicy{}, manager)

	results, err := runtime.SearchSession(context.Background(), sessionsearch.SessionSearchRequest{
		SessionID:  runtimeSearchSessionID,
		Query:      "running",
		Role:       "tool",
		ToolName:   "process",
		MaxResults: 5,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, runtimeSearchSessionID, results[0].SessionID)
	require.Len(t, results[0].Messages, 1)
	require.Equal(t, "tool", results[0].Messages[0].Role)
	require.Equal(t, "process", results[0].Messages[0].ToolName)
	require.NotZero(t, results[0].Messages[0].MessageID)
}

func TestRuntime_SearchSessionSupportsCrossSessionScope(t *testing.T) {
	store := memory.NewStore()
	manager, err := statemanager.NewManager(store, time.Minute, time.Hour)
	require.NoError(t, err)

	otherSessionID := nanoid.MustFromSeed(storage.SessionIDPrefix, "runtime-search-other", "EnvironmentRuntimeTestSeed")

	require.NoError(t, manager.Save(context.Background(), memory.Session{ID: runtimeSearchSessionID}))
	require.NoError(t, manager.Save(context.Background(), memory.Session{ID: otherSessionID}))
	require.NoError(t, manager.AppendMessages(context.Background(), runtimeSearchSessionID, []messages.Message{
		{
			Role:      messages.RoleUser,
			Content:   "origin needle",
			CreatedAt: time.Now().UTC(),
		},
	}))
	require.NoError(t, manager.AppendMessages(context.Background(), otherSessionID, []messages.Message{
		{
			Role:      messages.RoleUser,
			Content:   "other needle",
			CreatedAt: time.Now().UTC(),
		},
	}))

	runtime := NewRuntime([]string{t.TempDir()}, guardrails.CommandPolicy{}, manager)

	results, err := runtime.SearchSession(context.Background(), sessionsearch.SessionSearchRequest{
		IgnoreSessionID: runtimeSearchSessionID,
		Query:           "needle",
		MaxResults:      5,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, otherSessionID, results[0].SessionID)
	require.Equal(t, "other needle", results[0].Messages[0].Snippet)
}

func TestRuntime_SearchSessionHandlesNilReceiver(t *testing.T) {
	var runtime *Runtime

	_, err := runtime.SearchSession(context.Background(), sessionsearch.SessionSearchRequest{SessionID: runtimeSearchSessionID, Query: "hello"})
	require.EqualError(t, err, "state manager is required")
}

func ptrTo[T any](value T) *T {
	return &value
}

func TestRuntime_GetSessionMessagesDelegatesToStateManager(t *testing.T) {
	store := memory.NewStore()
	manager, err := statemanager.NewManager(store, time.Minute, time.Hour)
	require.NoError(t, err)
	require.NoError(t, manager.Save(context.Background(), memory.Session{ID: runtimeSearchSessionID}))
	require.NoError(t, manager.AppendMessages(context.Background(), runtimeSearchSessionID, []messages.Message{
		{
			Role:      messages.RoleUser,
			Content:   "hello world",
			CreatedAt: time.Now().UTC(),
		},
		{
			Role:       messages.RoleTool,
			Name:       "process",
			Content:    `{"process":{"id":"proc_1","status":"running"}}`,
			ToolCallID: "call-1",
			CreatedAt:  time.Now().UTC(),
		},
	}))

	runtime := NewRuntime([]string{t.TempDir()}, guardrails.CommandPolicy{}, manager)
	offsetStart := 0
	offsetEnd := 2

	response, err := runtime.GetSessionMessages(context.Background(), sessionmessages.SessionMessagesRequest{
		SessionID:   runtimeSearchSessionID,
		OffsetStart: &offsetStart,
		OffsetEnd:   &offsetEnd,
	})
	require.NoError(t, err)
	require.Equal(t, runtimeSearchSessionID, response.SessionID)
	require.Len(t, response.Messages, 2)
	require.Equal(t, []int{0, 1}, []int{response.Messages[0].Offset, response.Messages[1].Offset})
}

func TestRuntime_GetSessionMessagesSupportsCurrentSessionMessageIDLookup(t *testing.T) {
	store := memory.NewStore()
	manager, err := statemanager.NewManager(store, time.Minute, time.Hour)
	require.NoError(t, err)
	require.NoError(t, manager.Save(context.Background(), memory.Session{ID: runtimeSearchSessionID}))
	require.NoError(t, manager.UseSession(context.Background(), runtimeSearchSessionID))
	require.NoError(t, manager.AppendMessages(context.Background(), runtimeSearchSessionID, []messages.Message{
		{
			ID:        2,
			Role:      messages.RoleAssistant,
			Content:   "beta",
			CreatedAt: time.Now().UTC(),
		},
		{
			ID:        4,
			Role:      messages.RoleUser,
			Content:   "delta",
			CreatedAt: time.Now().UTC().Add(time.Second),
		},
	}))

	runtime := NewRuntime([]string{t.TempDir()}, guardrails.CommandPolicy{}, manager)

	response, err := runtime.GetSessionMessages(context.Background(), sessionmessages.SessionMessagesRequest{
		MessageIDs: []uint{4, 2},
	})
	require.NoError(t, err)
	require.Equal(t, runtimeSearchSessionID, response.SessionID)
	require.Len(t, response.Messages, 2)
	require.Equal(t, []uint{2, 4}, []uint{response.Messages[0].MessageID, response.Messages[1].MessageID})
	require.Equal(t, []int{0, 1}, []int{response.Messages[0].Offset, response.Messages[1].Offset})
}

func TestRuntime_GetSessionMessagesSupportsAnchorWindowAndTruncation(t *testing.T) {
	store := memory.NewStore()
	manager, err := statemanager.NewManager(store, time.Minute, time.Hour)
	require.NoError(t, err)
	require.NoError(t, manager.Save(context.Background(), memory.Session{ID: runtimeSearchSessionID}))
	require.NoError(t, manager.AppendMessages(context.Background(), runtimeSearchSessionID, []messages.Message{
		{
			ID:        1,
			Role:      messages.RoleUser,
			Content:   "alpha",
			CreatedAt: time.Now().UTC(),
		},
		{
			ID:         2,
			Role:       messages.RoleTool,
			Name:       "process",
			Content:    "process-running",
			ToolCallID: "call-1",
			CreatedAt:  time.Now().UTC().Add(time.Second),
		},
		{
			ID:        3,
			Role:      messages.RoleAssistant,
			Content:   "delta",
			CreatedAt: time.Now().UTC().Add(2 * time.Second),
		},
	}))

	runtime := NewRuntime([]string{t.TempDir()}, guardrails.CommandPolicy{}, manager)

	response, err := runtime.GetSessionMessages(context.Background(), sessionmessages.SessionMessagesRequest{
		SessionID:       runtimeSearchSessionID,
		AnchorMessageID: 2,
		Before:          1,
		After:           1,
		MaxChars:        4,
	})
	require.NoError(t, err)
	require.True(t, response.Truncated)
	require.Len(t, response.Messages, 3)
	require.Equal(t, []uint{1, 2, 3}, []uint{response.Messages[0].MessageID, response.Messages[1].MessageID, response.Messages[2].MessageID})
	require.Equal(t, []int{0, 1, 2}, []int{response.Messages[0].Offset, response.Messages[1].Offset, response.Messages[2].Offset})
	require.Equal(t, "proc", response.Messages[1].Content)
	require.True(t, response.Messages[1].Truncated)
	require.Equal(t, "process", response.Messages[1].ToolName)
}

func TestRuntime_GetSessionMessagesHandlesNilReceiver(t *testing.T) {
	var runtime *Runtime
	offsetStart := 0
	offsetEnd := 1

	_, err := runtime.GetSessionMessages(context.Background(), sessionmessages.SessionMessagesRequest{
		SessionID:   runtimeSearchSessionID,
		OffsetStart: &offsetStart,
		OffsetEnd:   &offsetEnd,
	})
	require.EqualError(t, err, "state manager is required")
}

func TestRuntime_SearchMemoryHandlesUnavailableProviders(t *testing.T) {
	tests := []struct {
		name    string
		runtime *Runtime
		message string
	}{
		{name: "nil runtime", message: "memory search is not configured"},
		{name: "nil provider", runtime: &Runtime{}, message: "memory search is not configured"},
		{name: "provider without search", runtime: &Runtime{memory: memoryProviderWithoutSearch{}}, message: "memory search is not supported by provider"},
		{name: "search capability disabled", runtime: &Runtime{memory: &memorySearchProviderStub{}}, message: "memory search is not supported by provider"},
		{name: "capability error", runtime: &Runtime{memory: &memorySearchProviderStub{capsErr: errors.New("capability failed")}}, message: "capability failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.runtime.SearchMemory(context.Background(), morphmemory.SearchQuery{Text: "hello"})

			require.EqualError(t, err, tt.message)
			require.Empty(t, result)
		})
	}
}

func TestRuntime_SearchMemorySearchesProvider(t *testing.T) {
	provider := &memorySearchProviderStub{
		caps: morphmemory.Capabilities{SupportsSearch: true},
		searchResult: morphmemory.SearchResult{Hits: []morphmemory.SearchHit{{
			Item: morphmemory.MemoryItem{
				ID:     "mem_123",
				Status: morphmemory.StatusActive,
				Text:   "hello",
			},
		}}},
	}
	runtime := &Runtime{memory: provider}
	query := morphmemory.SearchQuery{Text: "hello", Limit: 3}

	result, err := runtime.SearchMemory(context.Background(), query)

	require.NoError(t, err)
	require.Equal(t, query, provider.searchQuery)
	require.Equal(t, provider.searchResult, result)
}

func TestRuntime_ExtractEpisodesRejectsUnsupportedProviders(t *testing.T) {
	provider := &memoryExtractionProviderStub{
		memorySearchProviderStub: memorySearchProviderStub{
			caps: morphmemory.Capabilities{SupportsSearch: true},
		},
	}
	runtime := &Runtime{
		memory: provider,
	}

	result, err := runtime.ExtractEpisodes(context.Background(), episodic.Request{})

	require.EqualError(t, err, "memory extraction is not supported by provider")
	require.Empty(t, result)
}

func TestRuntime_ExtractEpisodesValidatesDependenciesAndCapabilities(t *testing.T) {
	_, err := (*Runtime)(nil).ExtractEpisodes(context.Background(), episodic.Request{})
	require.EqualError(t, err, "memory provider is required")

	runtime := &Runtime{}
	_, err = runtime.ExtractEpisodes(context.Background(), episodic.Request{})
	require.EqualError(t, err, "memory provider is required")

	runtime.memory = &memorySearchProviderStub{caps: morphmemory.Capabilities{SupportsEpisodeRecording: true}}
	_, err = runtime.ExtractEpisodes(context.Background(), episodic.Request{})
	require.EqualError(t, err, "memory extraction is not supported by provider")

	capsErr := errors.New("capability failed")
	provider := &memoryExtractionProviderStub{
		memorySearchProviderStub: memorySearchProviderStub{capsErr: capsErr},
	}
	runtime.memory = provider
	_, err = runtime.ExtractEpisodes(context.Background(), episodic.Request{})
	require.ErrorIs(t, err, capsErr)
}

func TestRuntime_ExtractEpisodesRunsExtractor(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	manager, err := statemanager.NewManager(store, time.Minute, time.Hour)
	require.NoError(t, err)
	provider, err := morphmemory.NewFromManager(manager, morphmemory.Options{
		ModelClient: environmentEpisodicModelClientStub(),
		Model:       "test-model",
	})
	require.NoError(t, err)

	require.NoError(t, manager.Save(ctx, storage.Session{ID: storage.DefaultSessionID}))
	require.NoError(t, manager.AppendMessages(ctx, storage.DefaultSessionID, []messages.Message{
		{
			ID:      1,
			Role:    messages.RoleUser,
			Content: "Remember the runtime extraction path.",
		},
	}))

	runtime := &Runtime{stateMgr: manager, memory: provider}

	result, err := runtime.ExtractEpisodes(ctx, episodic.Request{
		SessionID:      storage.DefaultSessionID,
		WindowSize:     1,
		MaxWindowChars: 1000,
	})

	require.NoError(t, err)
	require.Equal(t, 1, result.WriteCount)
	require.Equal(t, 1, result.MessageCount)
}

func TestRuntime_MemoryWriteValidatesDependenciesAndCapabilities(t *testing.T) {
	supported, err := (*Runtime)(nil).SupportsMemoryWrite(context.Background())
	require.NoError(t, err)
	require.False(t, supported)

	_, err = (*Runtime)(nil).RecordSemanticMemory(context.Background(), morphmemory.SemanticRecord{})
	require.EqualError(t, err, "memory write is not configured")

	runtime := &Runtime{}
	_, err = runtime.RecordSemanticMemory(context.Background(), morphmemory.SemanticRecord{})
	require.EqualError(t, err, "memory write is not configured")

	runtime.memory = &memorySearchProviderStub{caps: morphmemory.Capabilities{SupportsWrite: true}}
	_, err = runtime.RecordSemanticMemory(context.Background(), morphmemory.SemanticRecord{})
	require.EqualError(t, err, "semantic memory write is not supported by provider")

	_, err = runtime.RecordProceduralMemory(context.Background(), morphmemory.ProceduralRecord{})
	require.EqualError(t, err, "procedural memory write is not supported by provider")

	runtime.memory = &memorySearchProviderStub{
		caps: morphmemory.Capabilities{SupportsWrite: true, SupportsSemanticRecording: true},
	}
	_, err = runtime.RecordSemanticMemory(context.Background(), morphmemory.SemanticRecord{})
	require.EqualError(t, err, "semantic memory write is not supported by provider")

	runtime.memory = &memorySearchProviderStub{
		caps: morphmemory.Capabilities{SupportsWrite: true, SupportsProceduralRecording: true},
	}
	_, err = runtime.RecordProceduralMemory(context.Background(), morphmemory.ProceduralRecord{})
	require.EqualError(t, err, "procedural memory write is not supported by provider")

	_, err = (*Runtime)(nil).RecordProceduralMemory(context.Background(), morphmemory.ProceduralRecord{})
	require.EqualError(t, err, "memory write is not configured")

	_, err = runtime.PromoteMemoryCandidate(context.Background(), morphmemory.PromotionRequest{})
	require.EqualError(t, err, "memory write is not supported by provider")

	_, err = runtime.UpdateMemory(context.Background(), morphmemory.UpdateRequest{})
	require.EqualError(t, err, "memory write is not supported by provider")

	_, err = (*Runtime)(nil).PromoteMemoryCandidate(context.Background(), morphmemory.PromotionRequest{})
	require.EqualError(t, err, "memory write is not configured")

	runtime.memory = &memoryWriteProviderStub{
		memoryExtractionProviderStub: memoryExtractionProviderStub{
			memorySearchProviderStub: memorySearchProviderStub{
				capsErr: errors.New("capability failed"),
			},
		},
	}
	err = runtime.DeleteMemory(context.Background(), morphmemory.DeleteRequest{})
	require.EqualError(t, err, "capability failed")

	_, err = runtime.RecordSemanticMemory(context.Background(), morphmemory.SemanticRecord{})
	require.EqualError(t, err, "capability failed")

	_, err = runtime.RecordProceduralMemory(context.Background(), morphmemory.ProceduralRecord{})
	require.EqualError(t, err, "capability failed")
}

func TestRuntime_MemoryWriteCallsProvider(t *testing.T) {
	provider := &memoryWriteProviderStub{
		memoryExtractionProviderStub: memoryExtractionProviderStub{
			memorySearchProviderStub: memorySearchProviderStub{
				caps: morphmemory.Capabilities{
					SupportsWrite:               true,
					SupportsDelete:              true,
					SupportsSemanticRecording:   true,
					SupportsProceduralRecording: true,
				},
			},
		},
	}
	runtime := &Runtime{memory: provider}
	item := morphmemory.MemoryItem{ID: "mem_candidate", Kind: morphmemory.KindSemantic}

	_, err := runtime.RecordSemanticMemory(context.Background(), morphmemory.SemanticRecord{Item: item})
	require.NoError(t, err)
	require.Equal(t, item, provider.semanticRecord.Item)

	procedural := morphmemory.MemoryItem{ID: "mem_procedural_candidate", Kind: morphmemory.KindProcedural}
	_, err = runtime.RecordProceduralMemory(context.Background(), morphmemory.ProceduralRecord{Item: procedural})
	require.NoError(t, err)
	require.Equal(t, procedural, provider.proceduralRecord.Item)

	_, err = runtime.PromoteMemoryCandidate(context.Background(), morphmemory.PromotionRequest{ID: item.ID})
	require.NoError(t, err)
	require.Equal(t, item.ID, provider.promotionRequest.ID)

	_, err = runtime.UpdateMemory(context.Background(), morphmemory.UpdateRequest{ID: "mem_old", Replacement: item})
	require.NoError(t, err)
	require.Equal(t, "mem_old", provider.updateRequest.ID)
	require.Equal(t, item, provider.updateRequest.Replacement)

	err = runtime.DeleteMemory(context.Background(), morphmemory.DeleteRequest{ID: item.ID})
	require.NoError(t, err)
	require.Equal(t, item.ID, provider.deleteRequest.ID)
}

func TestRuntime_MemoryWriteAppliesSessionLineage(t *testing.T) {
	provider := &memoryWriteProviderStub{
		memoryExtractionProviderStub: memoryExtractionProviderStub{
			memorySearchProviderStub: memorySearchProviderStub{
				caps: morphmemory.Capabilities{
					SupportsWrite:               true,
					SupportsDelete:              true,
					SupportsSemanticRecording:   true,
					SupportsProceduralRecording: true,
				},
			},
		},
	}
	runtime := &Runtime{memory: provider}
	parentID := nanoid.MustFromSeed(storage.SessionIDPrefix, "parent", "RuntimeMemoryLineageTestSeed")
	childID := nanoid.MustFromSeed(storage.SessionIDPrefix, "child", "RuntimeMemoryLineageTestSeed")
	parent, err := runcontext.NewParent(parentID)
	require.NoError(t, err)
	child, err := parent.NewChild(runcontext.ChildOptions{
		ChildSessionID:  childID,
		RunID:           "run_runtime",
		PersonalityName: "researcher",
		StateMode:       runcontext.StateModeIsolated,
		ProfileName:     "work",
	})
	require.NoError(t, err)
	ctx := runcontext.WithContext(context.Background(), child)
	item := morphmemory.MemoryItem{
		ID: "mem_candidate",
		Metadata: map[string]string{
			morphmemory.MemoryMetadataTrigger: "episodic_extraction",
		},
		SourceLinks: []morphmemory.SourceLink{{SessionID: parentID, MessageIDs: []uint{1}}},
	}

	_, err = runtime.RecordSemanticMemory(ctx, morphmemory.SemanticRecord{Item: item})
	require.NoError(t, err)
	require.Equal(t, "episodic_extraction", provider.semanticRecord.Item.Metadata[morphmemory.MemoryMetadataTrigger])
	require.Equal(t, parentID, provider.semanticRecord.Item.Metadata[morphmemory.MemoryMetadataSourceSessionID])
	require.Equal(t, childID, provider.semanticRecord.Item.SourceLinks[0].ChildSessionID)

	_, err = runtime.RecordProceduralMemory(ctx, morphmemory.ProceduralRecord{Item: item})
	require.NoError(t, err)
	require.Equal(t, "episodic_extraction", provider.proceduralRecord.Item.Metadata[morphmemory.MemoryMetadataTrigger])
	require.Equal(t, parentID, provider.proceduralRecord.Item.Metadata[morphmemory.MemoryMetadataSourceSessionID])
	require.Equal(t, childID, provider.proceduralRecord.Item.SourceLinks[0].ChildSessionID)

	_, err = runtime.UpdateMemory(ctx, morphmemory.UpdateRequest{ID: "mem_old", Replacement: item})
	require.NoError(t, err)
	require.Equal(t, parentID, provider.updateRequest.Replacement.Metadata[morphmemory.MemoryMetadataSourceSessionID])
	require.Equal(t, parentID, provider.updateRequest.Replacement.SourceLinks[0].ParentSessionID)
}
