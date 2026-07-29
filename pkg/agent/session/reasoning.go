package session

import "strings"

type ReasoningEffort string

type ReasoningCapability struct {
	Efforts       []ReasoningEffort
	DefaultEffort ReasoningEffort
	Summary       bool
}

type ReasoningResolutionSource string

const (
	ReasoningResolutionSourceNone            ReasoningResolutionSource = ""
	ReasoningResolutionSourceSessionOverride ReasoningResolutionSource = "session_override"
	ReasoningResolutionSourceProfileDefault  ReasoningResolutionSource = "profile_default"
	ReasoningResolutionSourceCatalogDefault  ReasoningResolutionSource = "catalog_default"
)

type ReasoningFallbackCode string

const (
	ReasoningFallbackNone                       ReasoningFallbackCode = ""
	ReasoningFallbackSessionOverrideUnsupported ReasoningFallbackCode = "session_override_unsupported"
	ReasoningFallbackProfileDefaultUnsupported  ReasoningFallbackCode = "profile_default_unsupported"
	ReasoningFallbackCatalogDefault             ReasoningFallbackCode = "catalog_default"
	ReasoningFallbackNonAdjustable              ReasoningFallbackCode = "non_adjustable"
	ReasoningFallbackCatalogMiss                ReasoningFallbackCode = "catalog_miss"
	ReasoningFallbackAPIUnsupported             ReasoningFallbackCode = "api_unsupported"
)

type ReasoningModelTuple struct {
	Provider    string
	API         string
	Model       string
	DisplayName string
}

type ReasoningClaimContext struct {
	Model          ReasoningModelTuple
	Capability     ReasoningCapability
	ProfileDefault ReasoningEffort
	Reasoning      bool
	CatalogFound   bool
	APISupported   bool
}

type ReasoningSnapshot struct {
	Provider string
	API      string
	Model    string
	Effort   ReasoningEffort
	Summary  bool
}

func ResolveReasoningSnapshot(
	context ReasoningClaimContext,
	sessionOverride string,
) ReasoningSnapshot {
	settings := ResolveReasoningSettings(ReasoningResolutionInput{
		Model:           context.Model,
		Capability:      context.Capability,
		SessionOverride: ReasoningEffort(sessionOverride),
		ProfileDefault:  context.ProfileDefault,
		Reasoning:       context.Reasoning,
		CatalogFound:    context.CatalogFound,
		APISupported:    context.APISupported,
	})

	return ReasoningSnapshot{
		Provider: settings.Model.Provider,
		API:      settings.Model.API,
		Model:    settings.Model.Model,
		Effort:   settings.EffectiveEffort,
		Summary:  settings.SummarySupported,
	}
}

type ReasoningResolutionInput struct {
	Model           ReasoningModelTuple
	Capability      ReasoningCapability
	SessionOverride ReasoningEffort
	ProfileDefault  ReasoningEffort
	ActiveRun       *ReasoningSnapshot
	Reasoning       bool
	CatalogFound    bool
	APISupported    bool
}

type ReasoningSettings struct {
	Model             ReasoningModelTuple
	SupportedEfforts  []ReasoningEffort
	SessionOverride   ReasoningEffort
	ProfileDefault    ReasoningEffort
	CatalogDefault    ReasoningEffort
	EffectiveEffort   ReasoningEffort
	DormantEffort     ReasoningEffort
	ActiveRunSnapshot *ReasoningSnapshot
	Source            ReasoningResolutionSource
	Fallback          ReasoningFallbackCode
	Reasoning         bool
	Adjustable        bool
	SummarySupported  bool
}

func ResolveReasoningSettings(input ReasoningResolutionInput) ReasoningSettings {
	settings := ReasoningSettings{
		Model:            input.Model,
		SessionOverride:  input.SessionOverride,
		ProfileDefault:   input.ProfileDefault,
		CatalogDefault:   input.Capability.DefaultEffort,
		Reasoning:        input.Reasoning,
		SummarySupported: input.Capability.Summary,
		SupportedEfforts: append(
			[]ReasoningEffort(nil),
			input.Capability.Efforts...,
		),
	}
	if input.ActiveRun != nil {
		active := *input.ActiveRun
		settings.ActiveRunSnapshot = &active
	}

	switch {
	case !input.APISupported:
		settings.Fallback = ReasoningFallbackAPIUnsupported
		settings.DormantEffort = getDormantReasoningEffort(input)
		return settings
	case !input.CatalogFound:
		settings.Fallback = ReasoningFallbackCatalogMiss
		settings.DormantEffort = getDormantReasoningEffort(input)
		return settings
	case !input.Reasoning || len(input.Capability.Efforts) == 0:
		switch {
		case strings.TrimSpace(string(input.SessionOverride)) != "":
			settings.Fallback = ReasoningFallbackSessionOverrideUnsupported
			settings.DormantEffort = input.SessionOverride
		case strings.TrimSpace(string(input.ProfileDefault)) != "":
			settings.Fallback = ReasoningFallbackProfileDefaultUnsupported
			settings.DormantEffort = input.ProfileDefault
		default:
			settings.Fallback = ReasoningFallbackNonAdjustable
		}
		return settings
	}

	settings.Adjustable = true
	if effort, ok := getSupportedReasoningEffort(input.SessionOverride, input.Capability.Efforts); ok {
		settings.EffectiveEffort = effort
		settings.Source = ReasoningResolutionSourceSessionOverride
		return settings
	}

	sessionUnsupported := strings.TrimSpace(string(input.SessionOverride)) != ""
	if effort, ok := getSupportedReasoningEffort(input.ProfileDefault, input.Capability.Efforts); ok {
		settings.EffectiveEffort = effort
		settings.Source = ReasoningResolutionSourceProfileDefault
		if sessionUnsupported {
			settings.Fallback = ReasoningFallbackSessionOverrideUnsupported
			settings.DormantEffort = input.SessionOverride
		}
		return settings
	}

	profileUnsupported := strings.TrimSpace(string(input.ProfileDefault)) != ""
	if effort, ok := getSupportedReasoningEffort(
		input.Capability.DefaultEffort,
		input.Capability.Efforts,
	); ok {
		settings.EffectiveEffort = effort
		settings.Source = ReasoningResolutionSourceCatalogDefault
		switch {
		case sessionUnsupported:
			settings.Fallback = ReasoningFallbackSessionOverrideUnsupported
			settings.DormantEffort = input.SessionOverride
		case profileUnsupported:
			settings.Fallback = ReasoningFallbackProfileDefaultUnsupported
			settings.DormantEffort = input.ProfileDefault
		default:
			settings.Fallback = ReasoningFallbackCatalogDefault
		}
		return settings
	}

	switch {
	case sessionUnsupported:
		settings.Fallback = ReasoningFallbackSessionOverrideUnsupported
		settings.DormantEffort = input.SessionOverride
	case profileUnsupported:
		settings.Fallback = ReasoningFallbackProfileDefaultUnsupported
		settings.DormantEffort = input.ProfileDefault
	default:
		settings.Fallback = ReasoningFallbackCatalogDefault
	}
	return settings
}

func getDormantReasoningEffort(input ReasoningResolutionInput) ReasoningEffort {
	if strings.TrimSpace(string(input.SessionOverride)) != "" {
		return input.SessionOverride
	}
	if strings.TrimSpace(string(input.ProfileDefault)) != "" {
		return input.ProfileDefault
	}
	return ""
}

func getSupportedReasoningEffort(
	value ReasoningEffort,
	supported []ReasoningEffort,
) (ReasoningEffort, bool) {
	requested := strings.TrimSpace(string(value))
	if requested == "" {
		return "", false
	}
	for _, effort := range supported {
		if strings.EqualFold(string(effort), requested) {
			return effort, true
		}
	}

	return "", false
}
