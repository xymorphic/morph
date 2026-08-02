package agent

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xymorphic/morph/internal/config"
	instruct "github.com/xymorphic/morph/internal/instructions"
	"github.com/xymorphic/morph/internal/mocks"
	models "github.com/xymorphic/morph/internal/model"
	"github.com/xymorphic/morph/internal/permissions"
	storage "github.com/xymorphic/morph/internal/state/core"
	morphtools "github.com/xymorphic/morph/internal/tools"
	agenttool "github.com/xymorphic/morph/pkg/agent/tool"
)

func TestEnvironmentContextHelpersNormalizeStableValues(t *testing.T) {
	require.Equal(t, "+01:30", getTimezoneOffset(90*60))
	require.Equal(t, "-02:00", getTimezoneOffset(-2*3600))
	require.Equal(t, []string{"alpha", "beta"}, sortedUnique([]string{" beta ", "alpha", "beta", ""}))
	require.Equal(t, []string{"read", "write"}, getActiveToolNames([]models.ToolDefinition{
		{Name: "write"},
		{Name: "read"},
		{Name: "read"},
	}))
	require.Equal(t, []string{"core", "extra"}, getActiveToolGroups([]agenttool.Group{
		{Name: "extra"},
		{Name: "core"},
		{Name: "core"},
	}))
	require.Equal(t, " first ", getFirstNonEmpty(" first ", "second"))
	require.Equal(t, "second", getFirstNonEmpty("", " second "))
	root := t.TempDir()
	require.Equal(t, []string{root}, getFilesystemRoots(nil, root))
	require.Empty(t, getEnvironmentTimezone(time.Time{}))
	require.Empty(t, getEnvironmentTimezone(time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("", 3600))))
	require.Equal(t, "WAT", getEnvironmentTimezone(time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("WAT", 3600))))
	require.Nil(t, agentToolGroupsFromToolsGroups(nil))
	require.Equal(t, instruct.EnvironmentContextInstructionName, (*Turn)(nil).buildEnvironmentContextInstruction(nil).Name)
	_, ok := (*Turn)(nil).getActiveToolPolicy()
	require.False(t, ok)
}

func TestTurn_BuildEnvironmentContextInstructionUsesConfigAndToolPolicy(t *testing.T) {
	now := time.Date(2026, 5, 29, 10, 0, 0, 0, time.FixedZone("WAT", 3600))
	originalNow := environmentContextNow
	originalGetwd := environmentContextGetwd
	environmentContextNow = func() time.Time { return now }
	environmentContextGetwd = func() (string, error) { return "/tmp/morph", nil }
	t.Cleanup(func() {
		environmentContextNow = originalNow
		environmentContextGetwd = originalGetwd
	})

	turn := &Turn{
		cfg: &config.Config{
			Platform: "darwin",
			Models: config.ModelsConfig{
				Main:    config.MainModelConfig{Name: "main", Provider: "openai", API: models.APIOpenAIResponses},
				Summary: config.SummaryModelConfig{Name: "summary", Provider: "anthropic", API: models.APIAnthropicMessages},
			},
			Web: config.WebConfig{Provider: "search"},
		},
		sessionID: "session-1",
		sessionOrigin: storage.SessionOrigin{
			Source:         storage.SessionOriginSourceTelegram,
			ConversationID: "-100",
			ThreadID:       "7",
		},
		env: &mocks.EnvironmentStub{
			Policy: morphtools.Policy{
				Platform:     "linux",
				Capabilities: morphtools.Capabilities{Filesystem: true, Network: true},
			},
			ToolRegistry: &mocks.ToolRegistryStub{
				Groups: []morphtools.Group{{Name: "core"}, {Name: "search"}},
			},
		},
	}

	instruction := turn.buildEnvironmentContextInstruction([]models.ToolDefinition{
		{Name: "web_search"},
		{Name: "time"},
	})

	require.Equal(t, instruct.EnvironmentContextInstructionName, instruction.Name)
	require.Equal(t, fmt.Sprintf(`# Environment Context

- Current date: 2026-05-29
- Current time: 2026-05-29T10:00:00+01:00
- Timezone: WAT
- OS: %s
- Architecture: %s
- Platform: darwin
- Working directory: /tmp/morph
- Filesystem roots: /tmp/morph
- Capabilities: filesystem=true, network=true, exec=false, memory=false, browser=false
- Active tool groups: core, search
- Active tools: time, web_search
- Model: main
- Summary model: summary
- Model provider: openai
- Summary model provider: anthropic
- API: openai-responses
- Web provider: search
- Session ID: session-1
- Session origin: source=telegram; conversation=-100; thread=7
- Channel response guidance: The user is reading this in Telegram. Keep replies chat-friendly, concise, and readable on mobile. Use ordinary Markdown; the delivery layer handles Telegram formatting. Prefer short paragraphs and bullets, and avoid markdown tables and raw unsupported HTML.`, runtime.GOOS, runtime.GOARCH), instruction.Value)
}

func TestTurn_BuildEnvironmentContextInstructionExposesFullFilesystemAccess(t *testing.T) {
	originalGetwd := environmentContextGetwd
	environmentContextGetwd = func() (string, error) { return "/workspace/morph", nil }
	t.Cleanup(func() { environmentContextGetwd = originalGetwd })

	turn := &Turn{
		cfg: &config.Config{
			FS:          config.FSConfig{Roots: []string{"/workspace/morph"}},
			Permissions: permissions.Policy{Preset: permissions.PresetFullAccess},
		},
	}

	instruction := turn.buildEnvironmentContextInstruction(nil)

	require.Contains(t, instruction.Value, "Filesystem access: unrestricted (full_access)")
	require.Contains(t, instruction.Value, "absolute paths anywhere on this computer are allowed")
	require.NotContains(t, instruction.Value, "Filesystem roots:")
}

func TestTurn_BuildEnvironmentContextInstructionUsesSessionPermissionPreset(t *testing.T) {
	originalGetwd := environmentContextGetwd
	environmentContextGetwd = func() (string, error) { return "/workspace/morph", nil }
	t.Cleanup(func() { environmentContextGetwd = originalGetwd })

	turn := &Turn{
		ctx: permissions.WithPreset(context.Background(), permissions.PresetFullAccess),
		cfg: &config.Config{
			FS:          config.FSConfig{Roots: []string{"/workspace/morph"}},
			Permissions: permissions.Policy{Preset: permissions.PresetAskForApproval},
		},
	}

	instruction := turn.buildEnvironmentContextInstruction(nil)

	require.Contains(t, instruction.Value, "Filesystem access: unrestricted (full_access)")
	require.NotContains(t, instruction.Value, "Filesystem roots:")
}

func TestTurn_ActiveToolPolicyAndGroupsFallbackToCoreRegistry(t *testing.T) {
	registry := &toolGroupRegistryStub{
		groups: []agenttool.Group{{Name: "core", Tools: []string{"time"}}},
	}
	turn := &Turn{
		toolRegistry: registry,
		toolPolicy: agenttool.Policy{
			Platform:     "linux",
			Capabilities: agenttool.Capabilities{Exec: true},
		},
	}

	policy, ok := turn.getActiveToolPolicy()
	require.True(t, ok)
	require.Equal(t, "linux", policy.Platform)
	require.True(t, policy.Capabilities.Exec)
	require.Equal(t, registry.groups, turn.getActiveToolGroups())
	require.False(t, isEmptyToolPolicy(policy))
	require.Nil(t, (*Turn)(nil).getActiveToolGroups())
	require.Nil(t, (&Turn{}).getActiveToolGroups())
}
