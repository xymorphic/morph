package config

import (
	"testing"

	"github.com/stretchr/testify/require"

	commandplan "github.com/xymorphic/morph/internal/command"
	"github.com/xymorphic/morph/internal/constants"
	modelprovider "github.com/xymorphic/morph/internal/model/provider"
	"github.com/xymorphic/morph/internal/permissions"
)

func TestNewDefaultConfig_ReturnsIndependentConfig(t *testing.T) {
	first := NewDefaultConfig()
	second := NewDefaultConfig()

	require.Equal(t, DefaultConfig.Models.Main.Name, first.Models.Main.Name)
	require.Equal(t, DefaultConfig.Models.Main.Provider, first.Models.Main.Provider)
	require.Empty(t, first.Web.Provider)
	require.Equal(t, DefaultConfig.RPC.Address, first.RPC.Address)
	require.Equal(t, DefaultConfig.RPC.Port, first.RPC.Port)
	require.True(t, first.FS.NoProfileAccess)
	require.True(t, first.InputSafetyEnabled())
	require.True(t, first.OutputSafetyEnabled())
	require.True(t, first.OutputPIIRedactionEnabled())
	require.False(t, first.RetainUnsafeEnabled())
	require.NotEmpty(t, first.FS.Roots)
	require.Equal(t, constants.RerankerDeterministic, first.Reranker.Type)
	require.Equal(t, constants.RerankerDeterministic, first.RerankerEffective())
	require.Equal(t, constants.DefaultProfileRerankerMaxCandidates, first.Reranker.MaxCandidates)
	require.Equal(t, constants.DefaultProfileRerankerMaxCandidateTextChars, first.Reranker.MaxCandidateTextChars)
	require.Equal(t, constants.DefaultProfileRerankerMaxOutputTokens, first.Reranker.MaxOutputTokens)
	require.Equal(t, map[string]RerankerOverrideConfig{
		"memory_episodic_extraction": {Type: constants.RerankerLLM},
		"memory_promotion":           {Type: constants.RerankerLLM},
		"memory_reflection":          {Type: constants.RerankerLLM},
	}, first.Reranker.Overrides)

	*first.Safety.Input = false
	*first.Safety.Output = false
	*first.Safety.PII = false
	first.FS.Roots[0] = "mutated"
	first.Reranker.Overrides["memory_reflection"] = RerankerOverrideConfig{Type: constants.RerankerNoop}

	require.True(t, *second.Safety.Input)
	require.True(t, *second.Safety.Output)
	require.True(t, *second.Safety.PII)
	require.NotEqual(t, "mutated", second.FS.Roots[0])
	require.True(t, *DefaultConfig.TUI.ThinkingComposer)
	require.True(t, *DefaultConfig.Safety.Input)
	require.True(t, *DefaultConfig.Safety.Output)
	require.True(t, *DefaultConfig.Safety.PII)
	require.Equal(t, constants.RerankerLLM, second.Reranker.Overrides["memory_reflection"].Type)
	require.Equal(t, constants.RerankerLLM, DefaultConfig.Reranker.Overrides["memory_reflection"].Type)
}

func TestNewProfileConfig_LeavesModelSelectionEmpty(t *testing.T) {
	cfg := NewProfileConfig()

	require.Empty(t, cfg.Models.Main.Name)
	require.Empty(t, cfg.Models.Main.Provider)
	require.Empty(t, cfg.Models.Main.API)
	require.Empty(t, cfg.Models.Main.BaseURL)
	require.Empty(t, cfg.Models.Summary.Name)
	require.Empty(t, cfg.Models.Summary.Provider)
	require.Empty(t, cfg.Models.Summary.API)
	require.Empty(t, cfg.Models.Summary.BaseURL)
	require.Empty(t, cfg.Models.Embedding.Name)
	require.Empty(t, cfg.Models.Embedding.Provider)
	require.Empty(t, cfg.Models.Embedding.API)
	require.Empty(t, cfg.Models.Embedding.BaseURL)
	require.NotEmpty(t, cfg.FS.Roots)
	require.Equal(t, DefaultConfig.RPC.Address, cfg.RPC.Address)
	require.Equal(t, permissions.PresetApproveForMe, cfg.Permissions.Preset)
}

func TestCloneConfig_ClonesNestedValues(t *testing.T) {
	cfg := Config{
		Exec: ExecConfig{AllowCommands: []commandplan.Selector{{
			Executable: "git", ArgumentPrefix: []string{"status"}, RequireComplete: new(true),
		}}},
		Permissions: permissions.Policy{Rules: []permissions.Rule{{
			Name: "allow git", Commands: []commandplan.Selector{{
				Executable: "git", ExactArguments: []string{"status"}, RequireComplete: new(true),
			}},
		}}},
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{
				"openai": {
					Headers: map[string]string{"X-Test": "value"},
					Models: map[string]ProviderModelMetadata{
						"gpt-4o": {
							SupportsTools:    new(true),
							SupportsVision:   new(true),
							Reasoning:        new(true),
							ReasoningEfforts: []modelprovider.ReasoningEffort{"low", "high"},
							ReasoningSummary: new(true),
						},
					},
				},
			},
		},
		Personalities: map[string]PersonalityConfig{
			"researcher": {
				Memory: PersonalityMemoryConfig{
					Pinned: new(true),
				},
				Tools: PersonalityToolsConfig{
					Filesystem: new(true),
				},
				Model: MainModelConfig{
					Stream: new(false),
				},
			},
		},
	}

	cloned := cloneConfig(cfg)
	cloned.Models.Providers["openai"].Headers["X-Test"] = "changed"
	*cloned.Models.Providers["openai"].Models["gpt-4o"].SupportsTools = false
	*cloned.Models.Providers["openai"].Models["gpt-4o"].SupportsVision = false
	*cloned.Models.Providers["openai"].Models["gpt-4o"].Reasoning = false
	cloned.Models.Providers["openai"].Models["gpt-4o"].ReasoningEfforts[0] = "changed"
	*cloned.Models.Providers["openai"].Models["gpt-4o"].ReasoningSummary = false
	*cloned.Personalities["researcher"].Memory.Pinned = false
	*cloned.Personalities["researcher"].Tools.Filesystem = false
	*cloned.Personalities["researcher"].Model.Stream = true
	cloned.Exec.AllowCommands[0].ArgumentPrefix[0] = "push"
	*cloned.Exec.AllowCommands[0].RequireComplete = false
	cloned.Permissions.Rules[0].Commands[0].ExactArguments[0] = "push"
	*cloned.Permissions.Rules[0].Commands[0].RequireComplete = false

	require.Equal(t, "value", cfg.Models.Providers["openai"].Headers["X-Test"])
	require.True(t, *cfg.Models.Providers["openai"].Models["gpt-4o"].SupportsTools)
	require.True(t, *cfg.Models.Providers["openai"].Models["gpt-4o"].SupportsVision)
	require.True(t, *cfg.Models.Providers["openai"].Models["gpt-4o"].Reasoning)
	require.Equal(
		t,
		[]modelprovider.ReasoningEffort{"low", "high"},
		cfg.Models.Providers["openai"].Models["gpt-4o"].ReasoningEfforts,
	)
	require.True(t, *cfg.Models.Providers["openai"].Models["gpt-4o"].ReasoningSummary)
	require.True(t, *cfg.Personalities["researcher"].Memory.Pinned)
	require.True(t, *cfg.Personalities["researcher"].Tools.Filesystem)
	require.False(t, *cfg.Personalities["researcher"].Model.Stream)
	require.Equal(t, []string{"status"}, cfg.Exec.AllowCommands[0].ArgumentPrefix)
	require.True(t, *cfg.Exec.AllowCommands[0].RequireComplete)
	require.Equal(t, []string{"status"}, cfg.Permissions.Rules[0].Commands[0].ExactArguments)
	require.True(t, *cfg.Permissions.Rules[0].Commands[0].RequireComplete)
}

func TestCloneProviderModelMetadata_ReturnsNilForEmptyInput(t *testing.T) {
	require.Nil(t, cloneProviderModelMetadata(nil))
}
