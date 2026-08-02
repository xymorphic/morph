package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xymorphic/morph/internal/constants"
	modelprovider "github.com/xymorphic/morph/internal/model/provider"
	"github.com/xymorphic/morph/internal/profile"
)

func TestLoad_PersonalitiesParseNormalizeAndResolveSoulPaths(t *testing.T) {
	originalProfile := profile.Active()
	t.Cleanup(func() {
		profile.SetActive(originalProfile)
	})

	profileHome := t.TempDir()
	profile.SetActive(profile.WithMetadataPaths(profile.Profile{Name: "work", HomeDir: profileHome}))

	profileSoul := filepath.Join(profileHome, "personalities", "researcher", "SOUL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(profileSoul), 0o700))
	require.NoError(t, os.WriteFile(profileSoul, []byte("profile soul"), 0o600))

	configDir := t.TempDir()
	configSoul := filepath.Join(configDir, "personalities", "reviewer", "SOUL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(configSoul), 0o700))
	require.NoError(t, os.WriteFile(configSoul, []byte("config soul"), 0o600))

	configPath := filepath.Join(configDir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
personalities:
  Researcher:
    soul: personalities/researcher/SOUL.md
    instruct: " Prefer evidence-backed answers. "
    state: ""
    memory:
      pinned: true
      retrieval: true
      write: false
      episodic: false
      reflection: true
      promotion: false
      flush: true
    tools:
      fs: true
      net: true
      exec: false
      mem: read
    model:
      name: gpt-4o-mini
      provider: OpenRouter
      api: openai-responses
      baseUrl: " https://models.example "
      stream: false
    maxIterations: 7
  reviewer:
    soul: personalities/reviewer/SOUL.md
    state: readonly
`), 0o600))

	cfg, err := Load("", configPath)
	require.NoError(t, err)

	researcher := cfg.Personalities["researcher"]
	require.Equal(t, profileSoul, researcher.Soul)
	require.Equal(t, "Prefer evidence-backed answers.", researcher.Instruct)
	require.Equal(t, personalityStateShared, researcher.State)
	require.True(t, getBoolValue(researcher.Memory.Pinned))
	require.True(t, getBoolValue(researcher.Memory.Retrieval))
	require.False(t, getBoolValue(researcher.Memory.Write))
	require.False(t, getBoolValue(researcher.Memory.Episodic))
	require.True(t, getBoolValue(researcher.Memory.Reflection))
	require.False(t, getBoolValue(researcher.Memory.Promotion))
	require.True(t, getBoolValue(researcher.Memory.Flush))
	require.True(t, getBoolValue(researcher.Tools.Filesystem))
	require.True(t, getBoolValue(researcher.Tools.Network))
	require.False(t, getBoolValue(researcher.Tools.Exec))
	require.Equal(t, personalityToolMemoryRead, researcher.Tools.Memory)
	require.Equal(t, "gpt-4o-mini", researcher.Model.Name)
	require.Equal(t, "openrouter", researcher.Model.Provider)
	require.Equal(t, "openai-responses", researcher.Model.API)
	require.Equal(t, "https://models.example", researcher.Model.BaseURL)
	require.False(t, getBoolValueDefault(researcher.Model.Stream, true))
	require.Equal(t, 7, researcher.MaxIterations)

	reviewer := cfg.Personalities["reviewer"]
	require.Equal(t, configSoul, reviewer.Soul)
	require.Equal(t, personalityStateReadonly, reviewer.State)
}

func TestResolvePersonalitySoulPath_LeavesEmptyAndAbsolutePaths(t *testing.T) {
	absolutePath := filepath.Join(string(os.PathSeparator), "profiles", "researcher", "SOUL.md")

	require.Empty(t, resolvePersonalitySoulPath("", t.TempDir()))
	require.Equal(t, absolutePath, resolvePersonalitySoulPath(absolutePath, t.TempDir()))
}

func TestConfig_ValidatePersonalityNames(t *testing.T) {
	err := (&Config{
		Personalities: map[string]PersonalityConfig{
			"work/team": {},
		},
	}).Validate()
	require.EqualError(t, err, `invalid personality name "work/team": must match [a-zA-Z0-9][a-zA-Z0-9_-]{0,63}`)

	err = (&Config{
		Personalities: map[string]PersonalityConfig{
			"Researcher": {},
			"researcher": {},
		},
	}).Validate()
	require.EqualError(t, err, `duplicate personality name "researcher" conflicts with "Researcher"`)
}

func TestConfig_ValidateAcceptsValidPersonalitySettings(t *testing.T) {
	cfg := &Config{
		Name: "test-agent",
		Models: ModelsConfig{
			Providers: map[string]ProviderModelConfig{"openrouter": {APIKey: "test-key"}},
			Main:      MainModelConfig{Name: constants.DefaultModel, Provider: "openrouter"},
		},
		Personalities: map[string]PersonalityConfig{
			"Researcher": {
				State: personalityStateIsolated,
				Tools: PersonalityToolsConfig{
					Memory: personalityToolMemoryWrite,
				},
				Model: MainModelConfig{
					Name:     "gpt-4o-mini",
					Provider: "OpenAI",
					API:      modelprovider.APIOpenAIResponses,
				},
				MaxIterations: 3,
			},
			"reviewer": {
				State: personalityStateReadonly,
			},
		},
	}

	require.NoError(t, cfg.Validate())
	require.Contains(t, cfg.Personalities, "researcher")
	require.Equal(t, "openai", cfg.Personalities["researcher"].Model.Provider)
	require.Equal(t, modelprovider.APIOpenAIResponses, cfg.Personalities["researcher"].Model.API)
	require.Equal(t, personalityStateReadonly, cfg.Personalities["reviewer"].State)
}

func TestConfig_ValidatePersonalitySettings(t *testing.T) {
	cases := []struct {
		name          string
		personality   PersonalityConfig
		expectedError string
	}{
		{
			name:          "invalid state",
			personality:   PersonalityConfig{State: "solo"},
			expectedError: "personalities.researcher.state must be one of: shared, isolated, readonly",
		},
		{
			name:          "invalid memory tool mode",
			personality:   PersonalityConfig{Tools: PersonalityToolsConfig{Memory: "admin"}},
			expectedError: "personalities.researcher.tools.mem must be one of: none, read, write",
		},
		{
			name:          "invalid max iterations",
			personality:   PersonalityConfig{MaxIterations: -1},
			expectedError: "personalities.researcher.maxIterations must be non-negative",
		},
		{
			name:          "invalid model name",
			personality:   PersonalityConfig{Model: MainModelConfig{Name: "/gpt-4o-mini"}},
			expectedError: "personalities.researcher.model.name is invalid",
		},
		{
			name:          "invalid model provider",
			personality:   PersonalityConfig{Model: MainModelConfig{Provider: "other"}},
			expectedError: "personalities.researcher.model.provider must be one of: anthropic, github-copilot, ollama, openai, openai-codex, openrouter",
		},
		{
			name:          "invalid model api mode",
			personality:   PersonalityConfig{Model: MainModelConfig{API: "other"}},
			expectedError: "personalities.researcher.model.api must be one of: anthropic-messages, ollama-native, openai-completions, openai-responses",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := (&Config{
				Personalities: map[string]PersonalityConfig{
					"researcher": tc.personality,
				},
			}).Validate()

			require.EqualError(t, err, tc.expectedError)
		})
	}
}
