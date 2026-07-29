package session

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveReasoningSettings_PrecedenceAndFallbacks(t *testing.T) {
	capability := ReasoningCapability{
		Efforts:       []ReasoningEffort{"none", "low", "medium", "high"},
		DefaultEffort: "medium",
		Summary:       true,
	}
	tests := []struct {
		name            string
		sessionOverride ReasoningEffort
		profileDefault  ReasoningEffort
		wantEffort      ReasoningEffort
		wantSource      ReasoningResolutionSource
		wantFallback    ReasoningFallbackCode
	}{
		{
			name:            "session override",
			sessionOverride: "HIGH",
			profileDefault:  "low",
			wantEffort:      "high",
			wantSource:      ReasoningResolutionSourceSessionOverride,
		},
		{
			name:           "profile default",
			profileDefault: "low",
			wantEffort:     "low",
			wantSource:     ReasoningResolutionSourceProfileDefault,
		},
		{
			name:         "catalog default",
			wantEffort:   "medium",
			wantSource:   ReasoningResolutionSourceCatalogDefault,
			wantFallback: ReasoningFallbackCatalogDefault,
		},
		{
			name:            "dormant session override",
			sessionOverride: "xhigh",
			profileDefault:  "low",
			wantEffort:      "low",
			wantSource:      ReasoningResolutionSourceProfileDefault,
			wantFallback:    ReasoningFallbackSessionOverrideUnsupported,
		},
		{
			name:           "unsupported profile",
			profileDefault: "xhigh",
			wantEffort:     "medium",
			wantSource:     ReasoningResolutionSourceCatalogDefault,
			wantFallback:   ReasoningFallbackProfileDefaultUnsupported,
		},
		{
			name:            "session fallback remains primary reason",
			sessionOverride: "max",
			profileDefault:  "xhigh",
			wantEffort:      "medium",
			wantSource:      ReasoningResolutionSourceCatalogDefault,
			wantFallback:    ReasoningFallbackSessionOverrideUnsupported,
		},
		{
			name:            "explicit none is not unset",
			sessionOverride: "none",
			wantEffort:      "none",
			wantSource:      ReasoningResolutionSourceSessionOverride,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := ResolveReasoningSettings(ReasoningResolutionInput{
				Model: ReasoningModelTuple{
					Provider: "openai",
					API:      "openai-responses",
					Model:    "gpt-5.5",
				},
				Capability:      capability,
				SessionOverride: tt.sessionOverride,
				ProfileDefault:  tt.profileDefault,
				Reasoning:       true,
				CatalogFound:    true,
				APISupported:    true,
			})

			require.Equal(t, tt.wantEffort, settings.EffectiveEffort)
			require.Equal(t, tt.wantSource, settings.Source)
			require.Equal(t, tt.wantFallback, settings.Fallback)
			switch tt.wantFallback {
			case ReasoningFallbackSessionOverrideUnsupported:
				require.Equal(t, tt.sessionOverride, settings.DormantEffort)
			case ReasoningFallbackProfileDefaultUnsupported:
				require.Equal(t, tt.profileDefault, settings.DormantEffort)
			default:
				require.Empty(t, settings.DormantEffort)
			}
			require.True(t, settings.Adjustable)
			require.True(t, settings.SummarySupported)
			require.Equal(t, tt.sessionOverride, settings.SessionOverride)
			require.Equal(t, capability.Efforts, settings.SupportedEfforts)
		})
	}
}

func TestResolveReasoningSettings_PreservesDormantOverrideAcrossModelSwitch(t *testing.T) {
	input := ReasoningResolutionInput{
		SessionOverride: "xhigh",
		Reasoning:       true,
		CatalogFound:    true,
		APISupported:    true,
		Capability: ReasoningCapability{
			Efforts:       []ReasoningEffort{"low", "medium", "high"},
			DefaultEffort: "medium",
		},
	}

	unsupported := ResolveReasoningSettings(input)
	require.Equal(t, ReasoningEffort("xhigh"), unsupported.SessionOverride)
	require.Equal(t, ReasoningFallbackSessionOverrideUnsupported, unsupported.Fallback)
	require.Equal(t, ReasoningEffort("medium"), unsupported.EffectiveEffort)

	input.Capability.Efforts = append(input.Capability.Efforts, "xhigh")
	supported := ResolveReasoningSettings(input)
	require.Equal(t, ReasoningEffort("xhigh"), supported.EffectiveEffort)
	require.Equal(t, ReasoningResolutionSourceSessionOverride, supported.Source)
	require.Empty(t, supported.Fallback)
}

func TestResolveReasoningSettings_ReportsUnavailableCapability(t *testing.T) {
	tests := []struct {
		name         string
		input        ReasoningResolutionInput
		wantFallback ReasoningFallbackCode
		wantDormant  ReasoningEffort
	}{
		{
			name: "api unsupported",
			input: ReasoningResolutionInput{
				CatalogFound:    true,
				SessionOverride: "future-level",
			},
			wantFallback: ReasoningFallbackAPIUnsupported,
			wantDormant:  "future-level",
		},
		{
			name: "catalog miss",
			input: ReasoningResolutionInput{
				APISupported:   true,
				ProfileDefault: "high",
			},
			wantFallback: ReasoningFallbackCatalogMiss,
			wantDormant:  "high",
		},
		{
			name: "fixed reasoning with session override",
			input: ReasoningResolutionInput{
				APISupported:    true,
				CatalogFound:    true,
				Reasoning:       true,
				SessionOverride: "high",
			},
			wantFallback: ReasoningFallbackSessionOverrideUnsupported,
			wantDormant:  "high",
		},
		{
			name: "fixed reasoning with profile default",
			input: ReasoningResolutionInput{
				APISupported:   true,
				CatalogFound:   true,
				Reasoning:      true,
				ProfileDefault: "medium",
			},
			wantFallback: ReasoningFallbackProfileDefaultUnsupported,
			wantDormant:  "medium",
		},
		{
			name: "non reasoning",
			input: ReasoningResolutionInput{
				APISupported: true,
				CatalogFound: true,
			},
			wantFallback: ReasoningFallbackNonAdjustable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := ResolveReasoningSettings(tt.input)
			require.Equal(t, tt.wantFallback, settings.Fallback)
			require.False(t, settings.Adjustable)
			require.Empty(t, settings.EffectiveEffort)
			require.Equal(t, tt.wantDormant, settings.DormantEffort)
		})
	}
}

func TestResolveReasoningSettings_ClonesOrderedEffortsAndActiveSnapshot(t *testing.T) {
	efforts := []ReasoningEffort{"high", "low"}
	active := ReasoningSnapshot{Provider: "openai", API: "responses", Model: "gpt-5.5", Effort: "low"}
	settings := ResolveReasoningSettings(ReasoningResolutionInput{
		Capability: ReasoningCapability{
			Efforts:       efforts,
			DefaultEffort: "high",
		},
		ActiveRun:    &active,
		Reasoning:    true,
		CatalogFound: true,
		APISupported: true,
	})

	efforts[0] = "changed"
	active.Effort = "changed"
	require.Equal(t, []ReasoningEffort{"high", "low"}, settings.SupportedEfforts)
	require.NotNil(t, settings.ActiveRunSnapshot)
	require.Equal(t, ReasoningEffort("low"), settings.ActiveRunSnapshot.Effort)
}

func TestResolveReasoningSnapshotUsesCanonicalResolvedPolicy(t *testing.T) {
	snapshot := ResolveReasoningSnapshot(ReasoningClaimContext{
		Model: ReasoningModelTuple{
			Provider: "openai",
			API:      "openai-responses",
			Model:    "gpt-5.5",
		},
		Capability: ReasoningCapability{
			Efforts:       []ReasoningEffort{"low", "medium", "high"},
			DefaultEffort: "medium",
			Summary:       true,
		},
		ProfileDefault: "low",
		Reasoning:      true,
		CatalogFound:   true,
		APISupported:   true,
	}, "HIGH")

	require.Equal(t, ReasoningSnapshot{
		Provider: "openai",
		API:      "openai-responses",
		Model:    "gpt-5.5",
		Effort:   "high",
		Summary:  true,
	}, snapshot)
}
